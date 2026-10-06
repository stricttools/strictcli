/**
 * Runner tests: DAG order with dependency pull-in, cycle detection, the
 * dependency-failure cascade, warn/skip non-cascade, the purity partition,
 * durationMs timing, and the programmatic app.runChecks surface.
 *
 * GROUND TRUTH: byte-level expectations captured on 2026-07-19 from the
 * Python implementation (scratchpad pychecks.py / pychecks2.py) and
 * cross-checked against go/strictcli/testdata/cases/checks.json + check_purity.json.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import { setTimeout as sleep } from "node:timers/promises";
import type { App, CheckContext } from "../src/index.js";
import { createTestApp as createApp, EMPTY_PROJECT_ROOT } from "./helpers.js";

/** The framework's would-do log header (dry mode's primary output). */
const DRY_RUN_HEADER = "DRY RUN \u2014 no changes were made. Would do:\n";

// A dedicated EMPTY root: checks that statically analyse the consumer's
// sources (effects-bypass) walk it, so it must not be a shared scratch dir.
const CTX: CheckContext = { projectRoot: EMPTY_PROJECT_ROOT };

function checkBody(
	severity: "error" | "warn",
	extra: Record<string, string> = {},
): string {
	const lines = [
		'tags = ["release"]',
		`severity = "${severity}"`,
		"fast = true",
		"pure = true",
		"needs_network = false",
		extra.depends_on !== undefined
			? `depends_on = ${extra.depends_on}`
			: "depends_on = []",
	];
	return `${lines.join("\n")}\n`;
}

/** The 4-check mirror app used to capture the Python ground-truth bytes. */
function mirrorApp(): App {
	const toml = `app = "testapp"
[checks.lint]
description = "Checks lint"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.format]
description = "Checks format"
subject = "quality"
tags = ["dev", "quality"]
severity = "warn"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.compile]
description = "Checks compile"
subject = "quality"
tags = ["release"]
severity = "error"
fast = false
pure = false
needs_network = false
depends_on = []

[checks.deploy-gate]
description = "Checks deploy-gate"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = true
depends_on = ["compile", "lint"]
scope = "changelog"
`;
	const app = createApp({
		name: "testapp",
		version: "1.0.0",
		help: "test",
		checksEmbed: toml,
	});
	app.errorCheck("lint", (_c, r) => {
		r.note("scanned 5 files");
		return r.passed("all good");
	});
	app.warnCheck("format", (_c, r) => {
		r.warn("style issue A");
		return r.found("style issues");
	});
	app.errorCheck("compile", (_c, r) => {
		r.error("compile failed hard");
		r.warn("also a warning");
		return r.found("compile failed");
	});
	app.errorCheck("deploy-gate", (_c, r) => r.passed("gate ok"));
	app.setCheckContext(() => CTX);
	return app;
}

test("dry-run: the pure partition runs, the rest keeps Kahn order and annotations", async () => {
	const result = await mirrorApp().test(["--dry-run", "check", "--all"]);
	// format WARNs, which is a real result and therefore a real nonzero exit.
	assert.equal(result.exitCode, 1);
	assert.equal(
		result.stdout,
		"WARN  format    style issues\n" +
			"        [warn] style issue A\n" +
			"PASS  lint      all good\n" +
			"Would run 2 checks:\n" +
			"  1. compile [impure]\n" +
			"  2. deploy-gate (depends on: compile, lint) [impure]\n" +
			// `check` is read_only, so the framework's would-do log is the
			// header with an empty body.
			`${DRY_RUN_HEADER}`,
	);
});

test("cascade: dependency FAIL skips dependents with exact skip message", async () => {
	const result = await mirrorApp().test(["check", "--all"]);
	assert.equal(result.exitCode, 1);
	assert.equal(
		result.stdout,
		"FAIL  compile        compile failed\n" +
			"        [error] compile failed hard\n" +
			"        [warn] also a warning\n" +
			"WARN  format         style issues\n" +
			"        [warn] style issue A\n" +
			"PASS  lint           all good\n" +
			'SKIP  deploy-gate    skipped: dependency "compile" failed\n',
	);
});

test("cascade: warn dependency satisfies the dependent (no cascade)", async () => {
	const toml = `app = "tt"\n[checks.compile]\ndescription = "Checks compile"\nsubject = "quality"\n${checkBody("error")}\n[checks.lint]\ndescription = "Checks lint"\nsubject = "quality"\n${checkBody("error", { depends_on: '["compile"]' })}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	app.errorCheck("compile", (_c, r) => {
		r.warn("compiled with warnings");
		return r.found("compiled with warnings");
	});
	app.errorCheck("lint", (_c, r) => r.passed("lint ok"));
	app.setCheckContext(() => CTX);
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 1); // warn still exits nonzero
	assert.match(result.stdout, /WARN {2}compile/);
	assert.match(result.stdout, /PASS {2}lint/);
	assert.doesNotMatch(result.stdout, /SKIP/);
});

test("cascade: explicit SKIP is not a failure, dependents still run", async () => {
	const toml = `app = "tt"\n[checks.compile]\ndescription = "Checks compile"\nsubject = "quality"\n${checkBody("error")}\n[checks.lint]\ndescription = "Checks lint"\nsubject = "quality"\n${checkBody("error", { depends_on: '["compile"]' })}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	app.errorCheck("compile", (_c, r) => r.skipped("not applicable"));
	app.errorCheck("lint", (_c, r) => r.passed("lint ok"));
	app.setCheckContext(() => CTX);
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 0);
	assert.match(result.stdout, /SKIP {2}compile/);
	assert.match(result.stdout, /PASS {2}lint/);
});

test("cascade: multi-level chain, mid-chain failure cascades transitively", async () => {
	const toml = `app = "tt"\n[checks.check-a]\ndescription = "Checks check-a"\nsubject = "quality"\n${checkBody("error")}\n[checks.check-b]\ndescription = "Checks check-b"\nsubject = "quality"\n${checkBody("error", { depends_on: '["check-a"]' })}\n[checks.check-c]\ndescription = "Checks check-c"\nsubject = "quality"\n${checkBody("error", { depends_on: '["check-b"]' })}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	app.errorCheck("check-a", (_c, r) => r.passed("a passed"));
	app.errorCheck("check-b", (_c, r) => {
		r.error("b failed");
		return r.found("b failed");
	});
	app.errorCheck("check-c", (_c, r) => r.passed("c passed"));
	app.setCheckContext(() => CTX);
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 1);
	assert.match(result.stdout, /PASS {2}check-a/);
	assert.match(result.stdout, /FAIL {2}check-b/);
	assert.match(
		result.stdout,
		/SKIP {2}check-c {4}skipped: dependency "check-b" failed/,
	);
});

test("cycle detection: exact Python cycle-path bytes", async () => {
	const toml = `app = "tt"\n[checks.aa]\ndescription = "Checks a"\nsubject = "quality"\n${checkBody("error", { depends_on: '["bb"]' })}\n[checks.bb]\ndescription = "Checks b"\nsubject = "quality"\n${checkBody("error", { depends_on: '["aa"]' })}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	app.errorCheck("aa", (_c, r) => r.passed("ok"));
	app.errorCheck("bb", (_c, r) => r.passed("ok"));
	app.setCheckContext(() => CTX);
	await assert.rejects(app.test(["check", "--all"]), {
		message: "check dependency cycle: aa -> bb -> aa",
	});
	await assert.rejects(app.runChecks(CTX, { runAll: true }), {
		message: "check dependency cycle: aa -> bb -> aa",
	});
});

test("filtered-dep pull-in: selecting only the dependent runs the dependency too", async () => {
	const toml = `app = "tt"\n[checks.compile]\ndescription = "Checks compile"\nsubject = "quality"\n${checkBody("error")}\n[checks.lint]\ndescription = "Checks lint"\nsubject = "quality"\n${checkBody("error", { depends_on: '["compile"]' })}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	app.errorCheck("compile", (_c, r) => r.passed("compiled ok"));
	app.errorCheck("lint", (_c, r) => r.passed("lint ok"));
	app.setCheckContext(() => CTX);
	const result = await app.test(["check", "--name", "lint"]);
	assert.equal(result.exitCode, 0);
	assert.equal(
		result.stdout,
		"PASS  compile    compiled ok\nPASS  lint       lint ok\n",
	);
});

test("non-minted outcome is a hard error (belt-and-braces)", async () => {
	const toml = `app = "tt"\n[checks.aa]\ndescription = "Checks a"\nsubject = "quality"\n${checkBody("error")}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	// Force an untyped bad return through the typed surface.
	app.errorCheck(
		"aa",
		(() => 42) as unknown as Parameters<typeof app.errorCheck>[1],
	);
	app.setCheckContext(() => CTX);
	await assert.rejects(app.test(["check", "--all"]), {
		message:
			'check "aa" returned an outcome not minted by its reporter; use reporter methods (Passed/Skipped/Found)',
	});
});

class CheckAborted extends Error {}

test("a throwing impl is contained as its own failure and the next check still runs", async () => {
	const toml = `app = "tt"\n[checks.broken]\ndescription = "Checks broken"\nsubject = "quality"\n${checkBody("error")}\n[checks.fine]\ndescription = "Checks fine"\nsubject = "quality"\n${checkBody("error")}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	app.errorCheck("broken", () => {
		throw new CheckAborted("boom");
	});
	app.errorCheck("fine", (_c, r) => r.passed("all good"));
	app.setCheckContext(() => CTX);
	const result = await app.test(["check", "--tag", "release"]);
	assert.equal(result.exitCode, 1);
	assert.equal(
		result.stdout,
		'FAIL  broken    check "broken" aborted with CheckAborted: boom\n' +
			'        [error] check "broken" aborted with CheckAborted: boom\n' +
			"PASS  fine      all good\n",
	);
});

test("a contained abort cascade-skips its dependents", async () => {
	const toml = `app = "tt"\n[checks.broken]\ndescription = "Checks broken"\nsubject = "quality"\n${checkBody("error")}\n[checks.dependent]\ndescription = "Checks dependent"\nsubject = "quality"\n${checkBody("error", { depends_on: '["broken"]' })}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	app.errorCheck("broken", () => {
		throw new CheckAborted("boom");
	});
	app.errorCheck("dependent", (_c, r) => r.passed("never runs"));
	app.setCheckContext(() => CTX);
	const { results, exitCode } = await app.runChecks(CTX, { runAll: true });
	assert.equal(exitCode, 1);
	assert.equal(results[0]?.status, "fail");
	assert.equal(results[1]?.status, "skip");
});

test("a warn-severity check that throws still fails", async () => {
	const toml = `app = "tt"\n[checks.broken]\ndescription = "Checks broken"\nsubject = "quality"\n${checkBody("warn")}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	app.warnCheck("broken", () => {
		throw new CheckAborted("boom");
	});
	app.setCheckContext(() => CTX);
	const { results, exitCode } = await app.runChecks(CTX, { runAll: true });
	assert.equal(exitCode, 1);
	assert.equal(results[0]?.status, "fail");
});

test("abort attribution: type names, an empty message, and a thrown primitive", async () => {
	const toml = `app = "tt"\n[checks.aa]\ndescription = "Checks a"\nsubject = "quality"\n${checkBody("error")}`;
	const thrown: [unknown, string][] = [
		[new CheckAborted("boom"), 'check "aa" aborted with CheckAborted: boom'],
		[new TypeError("bad"), 'check "aa" aborted with TypeError: bad'],
		[new CheckAborted(""), 'check "aa" aborted with CheckAborted'],
		["boom", 'check "aa" aborted with string: boom'],
	];
	for (const [value, want] of thrown) {
		const app = createApp({
			name: "tt",
			version: "1",
			help: "hh",
			checksEmbed: toml,
		});
		app.errorCheck("aa", () => {
			throw value;
		});
		app.setCheckContext(() => CTX);
		const { results } = await app.runChecks(CTX, { runAll: true });
		assert.equal(results[0]?.outcome.message, want);
		assert.deepEqual(
			results[0]?.outcome.problems.map((p) => [p.severity, p.text]),
			[["error", want]],
		);
	}
});

test("durationMs: integer wall-clock around the impl only; cascade-skips carry 0", async () => {
	const toml = `app = "tt"\n[checks.slow]\ndescription = "Checks slow"\nsubject = "quality"\n${checkBody("error")}\n[checks.dep]\ndescription = "Checks dep"\nsubject = "quality"\n${checkBody("error", { depends_on: '["slow"]' })}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	app.errorCheck("slow", async (_c, r) => {
		await sleep(15);
		r.error("bad");
		return r.found("slow failed");
	});
	app.errorCheck("dep", (_c, r) => r.passed("never runs"));
	app.setCheckContext(() => CTX);
	const { results, exitCode } = await app.runChecks(CTX, { runAll: true });
	assert.equal(exitCode, 1);
	assert.equal(results.length, 2);
	const slow = results[0];
	const dep = results[1];
	assert.ok(slow !== undefined && dep !== undefined);
	assert.equal(slow.name, "slow");
	assert.ok(Number.isInteger(slow.durationMs));
	assert.ok(slow.durationMs >= 10, `durationMs=${slow.durationMs}`);
	assert.equal(dep.name, "dep");
	assert.equal(dep.status, "skip");
	assert.equal(dep.durationMs, 0);
});

// --- Programmatic runChecks surface ---

test("runChecks: not enabled is a hard error", async () => {
	const app = createApp({ name: "tt", version: "1", help: "hh" });
	await assert.rejects(app.runChecks(CTX), {
		message: "checks are not enabled on this App",
	});
});

test("runChecks: empty selection returns empty results and exit 0", async () => {
	const toml = `app = "tt"\n[checks.aa]\ndescription = "Checks a"\nsubject = "quality"\n${checkBody("error")}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	app.errorCheck("aa", (_c, r) => r.passed("ok"));
	const out = await app.runChecks(CTX);
	assert.deepEqual(out.results, []);
	assert.deepEqual(out.impureListed, []);
	assert.equal(out.exitCode, 0);
});

test("runChecks: result accessors, and a warning exits nonzero", async () => {
	const toml = `app = "tt"\n[checks.aa]\ndescription = "Checks a"\nsubject = "quality"\n${checkBody("warn")}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	app.warnCheck("aa", (_c, r) => {
		r.warn("minor issues");
		r.note("checked stuff");
		return r.found("minor issues");
	});
	const strict = await app.runChecks(CTX, { runAll: true });
	assert.equal(strict.exitCode, 1);
	const r = strict.results[0];
	assert.ok(r !== undefined);
	assert.equal(r.name, "aa");
	assert.equal(r.status, "warn");
	assert.equal(r.message, "minor issues");
	assert.deepEqual(
		r.problems.map((p) => ({ ...p })),
		[{ severity: "warn", text: "minor issues" }],
	);
	assert.deepEqual([...r.notes], ["checked stuff"]);
	assert.equal(r.gated(), false);
	assert.equal(r.warned(), true);
});

test("runChecks: tag/glob filters intersect", async () => {
	const toml = `app = "tt"
[checks.check-alpha]
description = "Checks check-alpha"
subject = "quality"
tags = ["xx"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.check-beta]
description = "Checks check-beta"
subject = "quality"
tags = ["xx"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.check-gamma]
description = "Checks check-gamma"
subject = "quality"
tags = ["yy"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	app.errorCheck("check-alpha", (_c, r) => r.passed("alpha passed"));
	app.errorCheck("check-beta", (_c, r) => r.passed("beta passed"));
	app.errorCheck("check-gamma", (_c, r) => r.passed("gamma passed"));
	const out = await app.runChecks(CTX, {
		tagExpr: "xx",
		nameGlob: "check-a*",
	});
	assert.deepEqual(
		out.results.map((r) => r.name),
		["check-alpha"],
	);
	assert.equal(out.exitCode, 0);
});

test("runChecks: purity partition lists impure checks and their dependents", async () => {
	const toml = `app = "tt"
[checks.pure-a]
description = "Checks pure-a"
subject = "quality"
tags = ["xx"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.impure-b]
description = "Checks impure-b"
subject = "quality"
tags = ["xx"]
severity = "error"
fast = true
pure = false
needs_network = false
depends_on = []

[checks.net-c]
description = "Checks net-c"
subject = "quality"
tags = ["xx"]
severity = "error"
fast = true
pure = true
needs_network = true
depends_on = []

[checks.pure-d]
description = "Checks pure-d"
subject = "quality"
tags = ["xx"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = ["impure-b"]
`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	const ran: string[] = [];
	for (const name of ["pure-a", "impure-b", "net-c", "pure-d"]) {
		app.errorCheck(name, (_c, r) => {
			ran.push(name);
			return r.passed(`${name} ok`);
		});
	}
	const out = await app.runChecks(CTX, { runAll: true, pureOnly: true });
	// Only the pure, network-free check with no listed dependencies executes;
	// pure-d joins the listing because its dependency impure-b was listed.
	assert.deepEqual(ran, ["pure-a"]);
	assert.deepEqual(
		out.results.map((r) => r.name),
		["pure-a"],
	);
	assert.deepEqual(out.impureListed, ["impure-b", "net-c", "pure-d"]);
	assert.equal(out.exitCode, 0);
});

test("runChecks: unregistered declared check is a thrown error", async () => {
	const toml = `app = "tt"\n[checks.aa]\ndescription = "Checks a"\nsubject = "quality"\n${checkBody("error")}`;
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: toml,
	});
	await assert.rejects(app.runChecks(CTX, { runAll: true }), {
		message: "checks declared in checks.toml but not registered: aa",
	});
});
