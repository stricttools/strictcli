/**
 * The framework-use lint (effects contract §28): the reserved
 * `--lint-framework-use` flag and the TypeScript scanner behind it.
 */

import { strict as assert } from "node:assert";
import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { createApp, defineReadOnlyCommand } from "../src/index.js";
import { scanFrameworkUse } from "../src/lint_framework_use.js";
import { tempDir } from "./helpers.js";

const PACKAGE = JSON.stringify({ name: "tool", bin: { tool: "src/cli.ts" } });

/** A fixture project in a git work tree; returns its root. */
function project(files: Record<string, string>, repo = true): string {
	const root = tempDir("sc-lint-use-");
	for (const [rel, body] of Object.entries(files)) {
		const path = join(root, rel);
		mkdirSync(join(path, ".."), { recursive: true });
		writeFileSync(path, body);
	}
	if (repo) {
		execFileSync("git", ["init", "-q"], { cwd: root, stdio: "ignore" });
	}
	return root;
}

/** The finding lines a scan of `cli` (the bin entry's source) reports. */
function lines(cli: string, extra: Record<string, string> = {}): string[] {
	const root = project({
		"package.json": PACKAGE,
		"src/cli.ts": cli,
		...extra,
	});
	const scan = scanFrameworkUse(root, join(root, "src/cli.ts"));
	assert.equal(scan.kind, "findings", JSON.stringify(scan));
	return scan.kind === "findings"
		? scan.findings.map((f) => `${f.path}:${f.line}: ${f.rule}: ${f.message}`)
		: [];
}

/** The `<path>:<line>: <rule>` prefixes of the findings. */
function where(cli: string, extra: Record<string, string> = {}): string[] {
	return lines(cli, extra).map((l) => l.split(": ").slice(0, 2).join(": "));
}

const EXIT_MSG =
	"ends the process outside the framework's exit step; return from the handler, or end the command early with throw new ExitNow(code, message)";

test("lint: each process-exit construct is a finding with its canonical spelling", () => {
	assert.deepEqual(
		lines(
			[
				"process.exit(1);",
				"process.abort();",
				"process.exitCode = 2;",
				"const c = process.exitCode;",
			].join("\n"),
		),
		[
			`src/cli.ts:1: process-exit: process.exit ${EXIT_MSG}`,
			`src/cli.ts:2: process-exit: process.abort ${EXIT_MSG}`,
			`src/cli.ts:3: process-exit: process.exitCode ${EXIT_MSG}`,
		],
	);
});

test("lint: stdout, stderr, argv, and environment constructs", () => {
	const found = lines(
		[
			'console.log("x");',
			'console.error("x");',
			'process.stdout.write("x");',
			'process.stderr.write("x");',
			"const a = process.argv;",
			"const e = process.env.HOME;",
			"console.table([]);",
			"const b = process.execArgv;",
		].join("\n"),
	);
	assert.deepEqual(found, [
		"src/cli.ts:1: stdout-write: console.log writes to stdout outside the framework; write the command's answer with ctx.out, its machine output with ctx.payload, or a document with ctx.document() on a command that owns stdout",
		"src/cli.ts:2: stderr-write: console.error writes to stderr outside the framework; report through ctx.warn or ctx.error",
		"src/cli.ts:3: stdout-write: process.stdout writes to stdout outside the framework; write the command's answer with ctx.out, its machine output with ctx.payload, or a document with ctx.document() on a command that owns stdout",
		"src/cli.ts:4: stderr-write: process.stderr writes to stderr outside the framework; report through ctx.warn or ctx.error",
		"src/cli.ts:5: argv-access: process.argv reads or edits the command line outside the framework; declare a flag or an argument",
		"src/cli.ts:6: environment-read: process.env reads the environment outside the declared mechanisms; declare a flag's environment binding, a handshake, a connection, or a location root",
		"src/cli.ts:7: stdout-write: console.table writes to stdout outside the framework; write the command's answer with ctx.out, its machine output with ctx.payload, or a document with ctx.document() on a command that owns stdout",
		"src/cli.ts:8: argv-access: process.execArgv reads or edits the command line outside the framework; declare a flag or an argument",
	]);
});

test("lint: an import of process under another name is still process", () => {
	assert.deepEqual(
		where(
			[
				'import proc from "node:process";',
				'import * as p from "process";',
				"proc.exit(1);",
				"p.env.X;",
			].join("\n"),
		),
		["src/cli.ts:3: process-exit", "src/cli.ts:4: environment-read"],
	);
});

test("lint: a named import of a listed member is reported at the import line", () => {
	assert.deepEqual(
		where(
			[
				'import { argv, env as e, cwd } from "node:process";',
				'import { parseArgs } from "node:util";',
				"e.HOME;",
			].join("\n"),
		),
		[
			"src/cli.ts:1: argv-access",
			"src/cli.ts:1: environment-read",
			"src/cli.ts:2: argv-access",
		],
	);
});

test("lint: a destructuring of the bare process is reported at that line", () => {
	assert.deepEqual(
		where(
			["const { env, argv: a, pid } = process;", "const { x } = other;"].join(
				"\n",
			),
		),
		["src/cli.ts:1: argv-access", "src/cli.ts:1: environment-read"],
	);
});

test("lint: a member of something else, and a type-only import, are not findings", () => {
	assert.deepEqual(
		lines(
			[
				'import type { argv } from "node:process";',
				"obj.process.exit(1);",
				"logger.console.log(1);",
				'const s = "process.exit(1)";',
				"// process.exit(1)",
			].join("\n"),
		),
		[],
	);
});

test("lint: two constructs on one line are two findings", () => {
	assert.deepEqual(where("console.log(process.env.A, process.env.A);"), [
		"src/cli.ts:1: environment-read",
		"src/cli.ts:1: environment-read",
		"src/cli.ts:1: stdout-write",
	]);
});

test("lint: modules reachable from the bin entry are scanned; others and test files are not", () => {
	assert.deepEqual(
		where(
			[
				'import { run } from "./lib/run.js";',
				'export { x } from "./lib/reexported";',
				'const later = await import("./lazy.js");',
				'const legacy = require("./legacy.cjs");',
			].join("\n"),
			{
				"src/lib/run.ts": "process.exit(1);",
				"src/lib/reexported/index.ts": "process.exit(1);",
				"src/lazy.ts": "process.exit(1);",
				"src/legacy.cjs": "process.exit(1);",
				"src/unreached.ts": "process.exit(1);",
				"src/lib/run.test.ts": "process.exit(1);",
				"scripts/gen.ts": "process.exit(1);",
			},
		),
		[
			"src/lazy.ts:1: process-exit",
			"src/legacy.cjs:1: process-exit",
			"src/lib/reexported/index.ts:1: process-exit",
			"src/lib/run.ts:1: process-exit",
		],
	);
});

test("lint: a built bin target maps to its source through outDir and rootDir", () => {
	const root = project({
		"package.json": JSON.stringify({ name: "tool", bin: "dist/cli.js" }),
		"tsconfig.json":
			'{\n\t// built output\n\t"compilerOptions": { "outDir": "dist", "rootDir": "src", },\n}\n',
		".gitignore": "dist/\n",
		"src/cli.ts": "process.exit(1);",
		"dist/cli.js": "process.exit(1);",
	});
	const scan = scanFrameworkUse(root, join(root, "dist/cli.js"));
	assert.equal(scan.kind, "findings");
	assert.deepEqual(
		scan.kind === "findings" ? scan.findings.map((f) => f.path) : [],
		["src/cli.ts"],
	);
});

test("lint: template substitutions and regular expressions are read as code and as literals", () => {
	assert.deepEqual(
		where(
			[
				// biome-ignore lint/suspicious/noTemplateCurlyInString: the fixture's source text holds a template literal
				"const s = `it's ${process.env.A} and process.exit(1)`;",
				"const r = /process.exit(\\d)/.test(s);",
				"process.abort();",
			].join("\n"),
		),
		["src/cli.ts:1: environment-read", "src/cli.ts:3: process-exit"],
	);
});

test("lint: a reachable file the scanner cannot read is refused", () => {
	const root = project({
		"package.json": PACKAGE,
		"src/cli.ts": 'import "./broken.js";',
		"src/broken.ts": 'const s = "unterminated;\n',
	});
	assert.deepEqual(scanFrameworkUse(root, join(root, "src/cli.ts")), {
		kind: "refused",
		message:
			"--lint-framework-use: source file 'src/broken.ts' does not parse: unterminated literal at line 1",
	});
});

test("lint: the refusals", () => {
	const noManifest = project({ "src/cli.ts": "" });
	assert.deepEqual(
		scanFrameworkUse(noManifest, join(noManifest, "src/cli.ts")),
		{
			kind: "refused",
			message: `--lint-framework-use: no package.json in the working directory '${noManifest}'; run the program from its project root`,
		},
	);

	const notRepo = project({ "package.json": PACKAGE, "src/cli.ts": "" }, false);
	assert.deepEqual(scanFrameworkUse(notRepo, join(notRepo, "src/cli.ts")), {
		kind: "refused",
		message: `--lint-framework-use: project root '${notRepo}' is not a git work tree; the scan reads only repository-owned files`,
	});

	const other = project({
		"package.json": PACKAGE,
		"src/cli.ts": "",
		"src/x.ts": "",
	});
	assert.deepEqual(scanFrameworkUse(other, join(other, "src/x.ts")), {
		kind: "refused",
		message: `--lint-framework-use: the package.json in '${other}' does not declare this program`,
	});

	const unmapped = project({
		"package.json": JSON.stringify({
			name: "tool",
			bin: { tool: "dist/cli.js" },
		}),
		".gitignore": "dist/\n",
		"dist/cli.js": "",
	});
	assert.deepEqual(scanFrameworkUse(unmapped, join(unmapped, "dist/cli.js")), {
		kind: "refused",
		message:
			"--lint-framework-use: bin entry 'tool' resolves to no repository-owned source file",
	});
});

function lintApp() {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", { help: "a command", handler: () => 0 }),
	);
	return app;
}

test("lint flag: it must be the only argument", async () => {
	for (const argv of [
		["--lint-framework-use", "cmd"],
		["--verbose", "--lint-framework-use"],
		["--lint-framework-use", "--help"],
	]) {
		const r = await lintApp().test(argv);
		assert.equal(r.exitCode, 1);
		assert.equal(r.stdout, "");
		assert.match(
			r.stderr,
			/^error: --lint-framework-use takes no other arguments\n/,
		);
	}
});

test("lint flag: after the command word it is an unknown flag", async () => {
	const r = await lintApp().test(["cmd", "--lint-framework-use"]);
	assert.equal(r.exitCode, 1);
	assert.match(r.stderr, /error: unknown flag '--lint-framework-use'/);
});

test("lint flag: run alone it scans the working directory's program", async () => {
	// The test runner is not the program package.json declares, so the scan
	// is refused by the identity check -- which is the flag reaching the scan.
	const root = project({ "package.json": PACKAGE, "src/cli.ts": "" });
	const cwd = process.cwd();
	process.chdir(root);
	try {
		const r = await lintApp().test(["--lint-framework-use"]);
		assert.equal(r.exitCode, 1);
		assert.equal(r.stdout, "");
		assert.equal(
			r.stderr,
			`error: --lint-framework-use: the package.json in '${root}' does not declare this program\n`,
		);
	} finally {
		process.chdir(cwd);
	}
});
