/**
 * The built-in `effects-bypass` check provider: the direct-call lint and the
 * two TypeScript-specific Proxy ceilings the runtime seal cannot catch.
 */

import { strict as assert } from "node:assert";
import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import type { AppImpl } from "../src/app.js";
import {
	BypassRootNotAWorkTree,
	scanEffectsBypasses,
} from "../src/checks/effects_bypass.js";
import { errEffectsBypassNotAWorkTree } from "../src/errors.js";
import {
	type App,
	type CheckContext,
	createApp,
	defineMutatingCommand,
} from "../src/index.js";
import { envelopePayload } from "./envelope_helpers.js";
import { tempDir } from "./helpers.js";

/** Runs one git command inside a fixture repository. */
function git(root: string, ...args: string[]): void {
	execFileSync("git", args, { cwd: root, stdio: "ignore" });
}

/**
 * A fixture project root. It is a git work tree: the lint reads only
 * repository-owned files, so a root that is not a repository is refused rather
 * than scanned. `loose` builds the same tree without the repository.
 */
function project(files: Record<string, string>): string {
	const root = loose(files);
	git(root, "init", "-q");
	return root;
}

function loose(files: Record<string, string>): string {
	const root = tempDir("sc-bypass-");
	for (const [rel, body] of Object.entries(files)) {
		const path = join(root, rel);
		mkdirSync(join(path, ".."), { recursive: true });
		writeFileSync(path, body);
	}
	return root;
}

const OFFENDING = `
import { rmSync } from "node:fs";
export function deploy(ctx) {
	ctx.effects.run(["make"]);
	rmSync("x");
}
`;

test("bypass: only repository-owned files are read", () => {
	const root = project({
		"tracked.ts": OFFENDING,
		"untracked.ts": OFFENDING,
		".gitignore": "ignored.ts\n",
		"ignored.ts": OFFENDING,
	});
	git(root, "add", "tracked.ts");

	const files = scanEffectsBypasses(root).map((f) => f.file);

	assert.deepEqual(files, ["tracked.ts", "untracked.ts"]);
});

test("bypass: a project root outside a git work tree is refused", () => {
	const root = loose({ "cli.ts": OFFENDING });

	assert.throws(
		() => scanEffectsBypasses(root),
		(e: unknown) =>
			e instanceof BypassRootNotAWorkTree &&
			e.message === errEffectsBypassNotAWorkTree(root),
	);
});

test("bypass: the check reports the input-rule refusal", async () => {
	const app = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: 'app = "t"\n',
	});
	const root = loose({ "cli.ts": OFFENDING });
	app.setCheckContext(() => ({ projectRoot: root }));

	const r = await app.test(["check", "--name", "effects-bypass"]);

	assert.equal(r.exitCode, 1);
	assert.ok(r.stdout.includes(errEffectsBypassNotAWorkTree(root)));
	assert.ok(!r.stdout.includes("route it through ctx.effects"));
});

test("bypass: a handler that opted in must route everything through ctx.effects", () => {
	const root = project({
		"cli.ts": `
import { writeFileSync } from "node:fs";
export function deploy(ctx) {
	ctx.effects.run(["make"]);
	writeFileSync("out.txt", "x");
}
`,
	});
	const findings = scanEffectsBypasses(root);
	assert.equal(findings.length, 1);
	assert.equal(findings[0]?.kind, "call");
	assert.equal(findings[0]?.file, "cli.ts");
	assert.equal(findings[0]?.line, 5);
	assert.match(findings[0]?.text ?? "", /calls writeFileSync directly/);
});

test("bypass: a function that never opts in is not a finding", () => {
	const root = project({
		"util.ts": `
import { writeFileSync } from "node:fs";
export function unrelated() {
	writeFileSync("out.txt", "x");
}
`,
	});
	assert.deepEqual(scanEffectsBypasses(root), []);
});

test("bypass: calls THROUGH ctx.effects are never findings", () => {
	const root = project({
		"cli.ts": `
export function deploy(ctx) {
	ctx.effects.run(["make"]);
	ctx.effects.write("a", "b");
	ctx.effects.mkdir("d");
	ctx.effects.remove("d");
	ctx.effects.rename("a", "b");
	ctx.effects.chmod("a", 0o755);
	ctx.effects.spawn(["x"]);
	ctx.effects.http("GET", "https://x.test");
}
`,
	});
	assert.deepEqual(scanEffectsBypasses(root), []);
});

test("bypass: process, filesystem and network families are all covered", () => {
	const root = project({
		"cli.ts": `
import { spawnSync } from "node:child_process";
import { rmSync } from "node:fs";
import http from "node:http";
export function deploy(ctx) {
	ctx.effects.run(["make"]);
	spawnSync("ls");
	rmSync("d");
	http.request("https://x.test");
	fetch("https://y.test");
}
`,
	});
	const kinds = scanEffectsBypasses(root).map((f) => f.text.split(" ")[1]);
	assert.deepEqual(kinds, ["spawnSync", "rmSync", "http.request", "fetch"]);
});

test("bypass: an ordinary map.get() inside a handler is NOT a finding", () => {
	const root = project({
		"cli.ts": `
export function deploy(ctx, table) {
	ctx.effects.run(["make"]);
	const v = table.get("k");
	return v;
}
`,
	});
	assert.deepEqual(scanEffectsBypasses(root), []);
});

// --- receiver-scoped process leaves ----------------------------------------
//
// `exec` is child_process' shell-runner AND RegExp.prototype.exec, a pure
// in-process string match. Flagging the latter produces a finding whose own
// remediation ("route it through ctx.effects") cannot be followed: the handle's
// closed method set has no in-process-observe method. So `exec` is a finding
// only through a receiver the analyser can read as child_process, mirroring the
// receiver gating the network leaves already have. Bare `exec(...)` stays a
// finding: TypeScript's analyser has no import resolution (§17), and an
// unqualified `exec(` in a reachable scope is the child_process import in every
// realistic reading.

test("bypass: a RegExp exec() is NOT a process finding", () => {
	const root = project({
		"cli.ts": `
const VERSION = /v(\\d+)/;
export function deploy(ctx, line) {
	ctx.effects.run(["make"]);
	return VERSION.exec(line);
}
`,
	});
	assert.deepEqual(scanEffectsBypasses(root), []);
});

test("bypass: child_process.exec() through a receiver is still a finding", () => {
	const root = project({
		"cli.ts": `
import cp from "node:child_process";
export function deploy(ctx) {
	ctx.effects.run(["make"]);
	cp.exec("git push");
}
`,
	});
	const findings = scanEffectsBypasses(root);
	assert.equal(findings.length, 1, JSON.stringify(findings));
	assert.match(findings[0]?.text ?? "", /calls cp\.exec directly/);
});

test("bypass: a bare exec() is still a finding", () => {
	const root = project({
		"cli.ts": `
import { exec } from "node:child_process";
export function deploy(ctx) {
	ctx.effects.run(["make"]);
	exec("git push");
}
`,
	});
	const findings = scanEffectsBypasses(root);
	assert.equal(findings.length, 1, JSON.stringify(findings));
	assert.match(findings[0]?.text ?? "", /calls exec directly/);
});

test("bypass: the truthiness ceiling is named explicitly", () => {
	const root = project({
		"cli.ts": `
export function deploy(ctx) {
	const built = ctx.effects.run(["make"]);
	if (built) {
		return 1;
	}
	return 0;
}
`,
	});
	const findings = scanEffectsBypasses(root);
	assert.equal(findings.length, 1);
	assert.equal(findings[0]?.kind, "ceiling");
	assert.match(findings[0]?.text ?? "", /a Proxy cannot trap truthiness/);
});

test("bypass: the identity-comparison ceiling is named explicitly", () => {
	const root = project({
		"cli.ts": `
export function deploy(ctx) {
	const built = ctx.effects.run(["make"]);
	return built === undefined ? 1 : 0;
}
`,
	});
	const findings = scanEffectsBypasses(root);
	assert.equal(findings.length, 1);
	assert.equal(findings[0]?.kind, "ceiling");
	assert.match(findings[0]?.text ?? "", /a Proxy cannot trap ===/);
});

test("bypass: forwarding a carrier is legal and produces no finding", () => {
	const root = project({
		"cli.ts": `
export function deploy(ctx) {
	const built = ctx.effects.run(["make"]);
	ctx.effects.write("out", built);
	ctx.effects.run(["upload", built]);
}
`,
	});
	assert.deepEqual(scanEffectsBypasses(root), []);
});

test("bypass: template literals do not stall the scanner", () => {
	// The compiler's scanner cannot leave a template-substitution state without
	// the parser's cooperation; the tokenizer asserts progress explicitly.
	const root = project({
		"cli.ts": `
export function deploy(ctx, v) {
	const msg = \`version \${v} of \${v.name} (\${v.tag})\`;
	ctx.effects.run(["echo", msg]);
	return msg.length;
}
`,
	});
	assert.deepEqual(scanEffectsBypasses(root), []);
});

test("bypass: node_modules and build output are skipped", () => {
	const root = project({
		"node_modules/dep/index.js": `
export function f(ctx) { ctx.effects.run([]); require("node:fs").rmSync("x"); }
`,
		"dist/bundle.js": `
export function g(ctx) { ctx.effects.run([]); require("node:fs").rmSync("x"); }
`,
	});
	assert.deepEqual(scanEffectsBypasses(root), []);
});

test("bypass: a missing project root is refused by the input rule", () => {
	const missing = join(tmpdir(), "sc-does-not-exist");
	assert.throws(
		() => scanEffectsBypasses(missing),
		(e: unknown) =>
			e instanceof BypassRootNotAWorkTree &&
			e.message === errEffectsBypassNotAWorkTree(missing),
	);
});

// --- The provider wiring (§11) ---

test("bypass: the provider registers inside the checks-enable path", async () => {
	const app = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: 'app = "t"\n',
	});
	const root = project({});
	const ctx: CheckContext = { projectRoot: root };
	app.setCheckContext(() => ctx);
	const listed = await app.test(["check", "--list", "--json"]);
	assert.match(listed.stdout, /"name":"effects-bypass"/);
	assert.match(listed.stdout, /"tags":\["effects","quality"\]/);
	assert.match(listed.stdout, /"severity":"error"/);
});

test("bypass: the check passes on a clean project and fails on a dirty one", async () => {
	const clean = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: 'app = "t"\n',
	});
	clean.setCheckContext(() => ({ projectRoot: project({}) }));
	const ok = await clean.test(["check", "--all"]);
	assert.equal(ok.exitCode, 0);
	assert.match(ok.stdout, /PASS {2}effects-bypass/);
	assert.match(ok.stdout, /no direct effect calls bypass ctx\.effects/);

	const dirty = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: 'app = "t"\n',
	});
	dirty.setCheckContext(() => ({
		projectRoot: project({
			"cli.ts": `
export function deploy(ctx) {
	ctx.effects.run(["make"]);
	require("node:fs").rmSync("x");
}
`,
		}),
	}));
	const bad = await dirty.test(["check", "--all"]);
	assert.equal(bad.exitCode, 1);
	assert.match(bad.stdout, /FAIL {2}effects-bypass/);
	assert.match(bad.stdout, /1 direct effect call\(s\) bypassing ctx\.effects/);
});

test("bypass: the provider check is excluded from the static schema", () => {
	const app = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: 'app = "t"\n',
	});
	// Provider-sourced checks materialize lazily per cwd, so they are not part
	// of the committed schema -- and a block with nothing left in it is omitted
	// entirely, which is the baseline the `defaults` block states (§25.7).
	const schema = app.dumpSchemaDict();
	assert.equal(schema.checks, undefined);
});

test("bypass: the provider is identifiable so tests can drop it", () => {
	const app = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: 'app = "t"\n',
	}) as unknown as AppImpl;
	assert.deepEqual(
		app.checks.providers.map((p) => p.name),
		["effectsBypassCheckProvider"],
	);
});

// --- §11's scope: reachability from a registered command handler -----------
//
// TypeScript's ceiling is real and recorded in §17: `typescript@7` ships the
// scanner but NO in-process parser, and this check is declared `fast` + `pure`,
// so building a real syntax tree (which means spawning the native language
// server against a resolved tsconfig) is off the table. What the scanner CAN
// give is brace structure plus token adjacency, and that is enough for a
// token-level function table and an intra-FILE call graph rooted at
// `handler:` properties. Cross-file reachability and any form of scope or type
// resolution are the residual gap.

test("bypass: a handler property that never mentions effects is analysed", () => {
	// Escape shape 1: opting in cannot be the trigger.
	const root = project({
		"cli.ts": `
import { spawnSync } from "node:child_process";
import { mkdirSync, rmSync } from "node:fs";

export const cmd = defineMutatingCommand("deploy", {
	help: "h",
	handler: (args, ctx) => {
		spawnSync("git", ["push"]);
		mkdirSync("build");
		rmSync("stale");
		return 0;
	},
});
`,
	});
	const findings = scanEffectsBypasses(root);
	assert.equal(findings.length, 3, JSON.stringify(findings));
	assert.ok(findings.every((f) => f.kind === "call"));
});

test("bypass: a bypass one helper-call away from a handler is analysed", () => {
	// Escape shape 2: reachability, not the immediate block.
	const root = project({
		"cli.ts": `
import { spawnSync } from "node:child_process";

function publish(path) {
	spawnSync("rsync", [path, "remote:/srv"]);
}

export const cmd = defineMutatingCommand("deploy", {
	help: "h",
	handler: (args, ctx) => {
		ctx.effects.run(["make", "build"]);
		publish("build");
		return 0;
	},
});
`,
	});
	const findings = scanEffectsBypasses(root);
	assert.equal(findings.length, 1, JSON.stringify(findings));
	assert.match(findings[0]?.text ?? "", /calls spawnSync directly/);
});

test("bypass: helper reachability is transitive", () => {
	const root = project({
		"cli.ts": `
import { rmSync } from "node:fs";

const inner = () => {
	rmSync("x");
};

function outer() {
	inner();
}

export const cmd = defineMutatingCommand("deploy", {
	help: "h",
	handler: (args, ctx) => {
		outer();
		return 0;
	},
});
`,
	});
	const findings = scanEffectsBypasses(root);
	assert.equal(findings.length, 1, JSON.stringify(findings));
});

test("bypass: a named function referenced as handler: is a root", () => {
	const root = project({
		"cli.ts": `
import { spawnSync } from "node:child_process";

function deploy(args, ctx) {
	spawnSync("docker", ["build", "."]);
	return 0;
}

export const cmd = defineMutatingCommand("deploy", { help: "h", handler: deploy });
`,
	});
	const findings = scanEffectsBypasses(root);
	assert.equal(findings.length, 1, JSON.stringify(findings));
});

test("bypass: an unreachable helper is still not analysed", () => {
	const root = project({
		"cli.ts": `
import { rmSync } from "node:fs";

function neverCalled() {
	rmSync("x");
}

export const cmd = defineMutatingCommand("deploy", {
	help: "h",
	handler: (args, ctx) => {
		ctx.effects.run(["make"]);
		return 0;
	},
});
`,
	});
	assert.deepEqual(scanEffectsBypasses(root), []);
});

test("bypass: a local alias of the effects handle is not a bypass", () => {
	const root = project({
		"cli.ts": `
export const cmd = defineMutatingCommand("deploy", {
	help: "h",
	handler: (args, ctx) => {
		const e = ctx.effects;
		e.mkdir("build");
		e.rm("stale");
		return 0;
	},
});
`,
	});
	assert.deepEqual(scanEffectsBypasses(root), []);
});

// --- §6.2's hazard, surfaced as a WARNING and never as an error -------------
//
// procObserveAllowlist: [["git"]] makes EVERY git invocation an observe: it
// really executes under --dry-run, is never logged, and is legal in a read_only
// command. That may be exactly what the app wants -- the allowlist is a
// declared, source-visible choice that authorizes real execution in dry mode --
// so the framework says so out loud instead of inventing a specificity rule.

function breadthApp(allowlist: readonly (readonly string[])[]): App {
	const app = createApp({
		name: "testapp",
		version: "1.0.0",
		help: "test app",
		procObserveAllowlist: allowlist,
	});
	app.registerCheckProvider(() => []);
	const root = tempDir("sc-breadth-");
	app.setCheckContext((): CheckContext => ({ projectRoot: root }));
	return app;
}

test("breadth: a single-token allowlist prefix warns", async () => {
	const app = breadthApp([["git"]]);
	const r = await app.test(["check", "--name", "observe-allowlist-breadth"]);
	assert.ok(r.stdout.includes("WARN"), r.stdout);
	assert.ok(
		r.stdout.includes("EVERY 'git' invocation becomes an observe"),
		r.stdout,
	);
	assert.ok(r.stdout.includes("really executes under --dry-run"), r.stdout);
});

test("breadth: the verdict is a warning, not an error", async () => {
	const app = breadthApp([["git"]]);
	const r = await app.test([
		"check",
		"--name",
		"observe-allowlist-breadth",
		"--ignore-warnings",
	]);
	assert.equal(r.exitCode, 0, r.stdout);
});

test("breadth: multi-token prefixes pass", async () => {
	const app = breadthApp([
		["git", "status"],
		["gh", "release", "view"],
	]);
	const r = await app.test(["check", "--name", "observe-allowlist-breadth"]);
	assert.equal(r.exitCode, 0, r.stdout);
	assert.ok(
		r.stdout.includes("no single-token proc_observe_allowlist prefixes"),
		r.stdout,
	);
});

test("breadth: the check is registered with warn severity", async () => {
	const app = breadthApp([]);
	const r = await app.test(["check", "--list", "--json"]);
	const entry = (
		envelopePayload(r.stdout) as { name: string; severity: string }[]
	).find((e) => e.name === "observe-allowlist-breadth");
	assert.equal(entry?.severity, "warn");
});

// --- §8.1's declaration vs §6.1's grants ------------------------------------
//
// A grant exists so a reviewer reading a preview sees WHY a dangerous step is
// there -- the same judgement `consequential` makes. The check fires only for
// the two kinds that leave this process (proc_mutate runs another program,
// net_mutate changes remote state); a file_write or a proc_spawn is local and
// ordinarily recoverable, and flagging those would re-create the noise the
// consequential declaration exists to remove.
//
// It is a WARNING, not an error, for the same reason: an error would push
// consumers to declare consequential reflexively to clear a gate, which is the
// exact reflex the redesign removed.

function grantApp(
	kind: "proc_mutate" | "proc_spawn" | "file_write" | "net_mutate",
	consequential: boolean,
): App {
	const app = createApp({
		name: "testapp",
		version: "1.0.0",
		help: "test app",
	});
	app.command(
		defineMutatingCommand("release", {
			help: "h",
			...(consequential ? { consequential: true as const } : {}),
			grants: [
				{ name: "push", reason: "the release engine owns remote refs", kind },
			],
			handler: () => 0,
		}),
	);
	app.registerCheckProvider(() => []);
	const root = tempDir("sc-grant-");
	app.setCheckContext((): CheckContext => ({ projectRoot: root }));
	return app;
}

test("grant agreement: a proc_mutate grant on a non-consequential command warns", async () => {
	const app = grantApp("proc_mutate", false);
	const r = await app.test([
		"check",
		"--name",
		"consequential-grant-agreement",
	]);
	assert.ok(r.stdout.includes("WARN"), r.stdout);
	assert.ok(
		r.stdout.includes(
			"command 'release' declares grant 'push' (kind proc_mutate) but is not consequential",
		),
		r.stdout,
	);
});

test("grant agreement: a net_mutate grant warns too", async () => {
	const r = await grantApp("net_mutate", false).test([
		"check",
		"--name",
		"consequential-grant-agreement",
	]);
	assert.ok(r.stdout.includes("kind net_mutate"), r.stdout);
});

test("grant agreement: the verdict is a warning, not an error", async () => {
	const r = await grantApp("proc_mutate", false).test([
		"check",
		"--name",
		"consequential-grant-agreement",
		"--ignore-warnings",
	]);
	assert.equal(r.exitCode, 0, r.stdout);
});

test("grant agreement: a consequential command passes", async () => {
	const r = await grantApp("proc_mutate", true).test([
		"check",
		"--name",
		"consequential-grant-agreement",
	]);
	assert.equal(r.exitCode, 0, r.stdout);
	assert.ok(
		r.stdout.includes("every escaping grant sits on a consequential command"),
		r.stdout,
	);
});

test("grant agreement: the local kinds are not flagged", async () => {
	for (const kind of ["file_write", "proc_spawn"] as const) {
		const r = await grantApp(kind, false).test([
			"check",
			"--name",
			"consequential-grant-agreement",
		]);
		assert.equal(r.exitCode, 0, `${kind}: ${r.stdout}`);
	}
});

test("grant agreement: a grouped command is named by its dotted path", async () => {
	const app = createApp({
		name: "testapp",
		version: "1.0.0",
		help: "test app",
	});
	const grp = app.group("release", { help: "h" });
	grp.command(
		defineMutatingCommand("run", {
			help: "h",
			grants: [
				{ name: "push", reason: "owns remote refs", kind: "proc_mutate" },
			],
			handler: () => 0,
		}),
	);
	app.registerCheckProvider(() => []);
	const root = tempDir("sc-grant-");
	app.setCheckContext((): CheckContext => ({ projectRoot: root }));
	const r = await app.test([
		"check",
		"--name",
		"consequential-grant-agreement",
	]);
	assert.ok(
		r.stdout.includes("command 'release.run' declares grant 'push'"),
		r.stdout,
	);
});

test("grant agreement: the check is registered with warn severity", async () => {
	const r = await grantApp("proc_mutate", true).test([
		"check",
		"--list",
		"--json",
	]);
	const entry = (
		envelopePayload(r.stdout) as {
			name: string;
			severity: string;
			tags: string[];
		}[]
	).find((e) => e.name === "consequential-grant-agreement");
	assert.equal(entry?.severity, "warn");
	assert.deepEqual([...(entry?.tags ?? [])].sort(), ["effects", "quality"]);
});

test("grant agreement: consequential is emitted in the schema, omitted when false", async () => {
	const app = createApp({ name: "t", version: "1", help: "h" });
	app.command(defineMutatingCommand("plain", { help: "h", handler: () => 0 }));
	app.command(
		defineMutatingCommand("grave", {
			help: "h",
			consequential: true,
			handler: () => 0,
		}),
	);
	const schema = (app as unknown as AppImpl).dumpSchemaDict() as {
		commands: Record<string, Record<string, unknown>>;
	};
	assert.equal(schema.commands.plain?.consequential, undefined);
	assert.equal(schema.commands.grave?.consequential, true);
});
