/**
 * Test-coverage instrumentation tests: shard files written by test(), the
 * canonical manifest, and the built-in cli-test-coverage provider check.
 *
 * Each test chdirs into a fresh temp directory and declares that directory as
 * the app's source-tree root, so the relative paths below and the derived
 * coverage paths name the same files. Expectations derive from
 * go/strictcli/testdata/cases/test_coverage.json and go/strictcli/coverage.go /
 * Python _test_coverage_provider.
 */

import { strict as assert } from "node:assert";
import {
	existsSync,
	mkdirSync,
	readdirSync,
	readFileSync,
	writeFileSync,
} from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import type { CheckContext } from "../src/index.js";
import { type App, defineReadOnlyCommand } from "../src/index.js";
import { createTestApp as createApp, tempDir } from "./helpers.js";

const CTX: CheckContext = { projectRoot: "." };

async function inTempDir<T>(fn: (dir: string) => Promise<T>): Promise<T> {
	const oldCwd = process.cwd();
	const dir = tempDir("strictcli-coverage-");
	process.chdir(dir);
	try {
		return await fn(dir);
	} finally {
		process.chdir(oldCwd);
	}
}

/**
 * The coverage directory relative to the declared source-tree root, spelled
 * out here rather than read from the implementation so the tests pin the
 * layout itself.
 */
const COVERAGE_REL = join(".strictmetadata", ".cli-test-coverage");

/**
 * Creates the coverage directory under dir and returns dir, the root an app
 * declares through sourceTreeRoot. Coverage turns on only when that directory
 * already exists at construction, which is what makes an installed
 * distribution -- whose root is not a source checkout -- uninstrumented.
 */
function declaredSourceTreeRoot(dir: string): string {
	mkdirSync(join(dir, COVERAGE_REL), { recursive: true });
	return dir;
}

/** The three-command mirror app from conformance test_coverage.json. */
function coverageApp(): App {
	const app = createApp({
		name: "testapp",
		version: "1.0.0",
		help: "test",
		sourceTreeRoot: declaredSourceTreeRoot(process.cwd()),
	});
	for (const [name, help, prints] of [
		["deploy", "deploy the app", "deployed"],
		["status", "show status", "ok"],
		["build", "build the app", "built"],
	] as const) {
		app.command(
			defineReadOnlyCommand(name, {
				help,
				handler: (_args, ctx) => {
					ctx.info(prints);
					return 0;
				},
			}),
		);
	}
	app.setCheckContext(() => CTX);
	return app;
}

test("sourceTreeRoot shards on test()", async () => {
	await inTempDir(async () => {
		const app = coverageApp();
		await app.test(["deploy"]);
		await app.test(["deploy"]);
		const shards = readdirSync(join(COVERAGE_REL, "shards"));
		assert.equal(shards.length, 1);
		const shard = shards[0] as string;
		// One shard per process, named <pid>.jsonl (sibling parity: no counter).
		assert.equal(shard, `${process.pid}.jsonl`);
		assert.equal(
			readFileSync(join(COVERAGE_REL, "shards", shard), "utf8"),
			'{"command":"deploy"}\n{"command":"deploy"}\n',
		);
	});
});

test("partial coverage fails naming every uncovered command, sorted", async () => {
	await inTempDir(async () => {
		const app = coverageApp();
		await app.test(["deploy"]);
		const result = await app.test(["check", "--all"]);
		assert.equal(result.exitCode, 1);
		assert.equal(
			result.stdout,
			"FAIL  cli-test-coverage    2 command(s) with zero test coverage\n" +
				"        [error] no test coverage for command: build\n" +
				"        [error] no test coverage for command: status\n",
		);
	});
});

test("full coverage passes and writes the canonical sorted manifest", async () => {
	await inTempDir(async () => {
		const app = coverageApp();
		await app.test(["deploy"]);
		await app.test(["status"]);
		await app.test(["build"]);
		const result = await app.test(["check", "--all"]);
		assert.equal(result.exitCode, 0);
		assert.equal(
			result.stdout,
			"PASS  cli-test-coverage    all 3 commands have test coverage\n",
		);
		// Manifest: sorted covered commands, 2-space indent, trailing newline.
		// Running `check --all` via test() records "check" itself too.
		assert.equal(
			readFileSync(join(COVERAGE_REL, "manifest.json"), "utf8"),
			'[\n  "build",\n  "check",\n  "deploy",\n  "status"\n]\n',
		);
	});
});

test("zero coverage state skips (no manifest, no shards)", async () => {
	await inTempDir(async (dir) => {
		const app = coverageApp();
		// Run the check via the programmatic API so no shard is written first.
		const { results, exitCode } = await app.runChecks(CTX, {
			nameGlob: "cli-test-coverage",
		});
		assert.equal(exitCode, 0);
		const r = results[0];
		assert.ok(r !== undefined);
		assert.equal(r.status, "skip");
		assert.equal(
			r.message,
			`no coverage state at ${join(dir, COVERAGE_REL)} -- cli-test-coverage` +
				" applies to the app's own development tree",
		);
	});
});

test("committed manifest yields a deterministic pass with no shards", async () => {
	await inTempDir(async () => {
		mkdirSync(COVERAGE_REL, { recursive: true });
		writeFileSync(
			join(COVERAGE_REL, "manifest.json"),
			'[\n  "build",\n  "deploy",\n  "status"\n]\n',
		);
		const app = coverageApp();
		const { results, exitCode } = await app.runChecks(CTX, {
			nameGlob: "cli-test-coverage",
		});
		assert.equal(exitCode, 0);
		const r = results[0];
		assert.ok(r !== undefined);
		assert.equal(r.status, "pass");
		assert.equal(r.message, "all 3 commands have test coverage");
		// Byte-identical union: a pure check must not dirty the manifest.
		assert.equal(
			readFileSync(join(COVERAGE_REL, "manifest.json"), "utf8"),
			'[\n  "build",\n  "deploy",\n  "status"\n]\n',
		);
	});
});

test("partial manifest fails honestly and rewrites the monotonic union", async () => {
	await inTempDir(async () => {
		mkdirSync(COVERAGE_REL, { recursive: true });
		writeFileSync(join(COVERAGE_REL, "manifest.json"), '[\n  "deploy"\n]\n');
		const app = coverageApp();
		await app.test(["status"]);
		const result = await app.test(["check", "--all"]);
		assert.equal(result.exitCode, 1);
		assert.equal(
			result.stdout,
			"FAIL  cli-test-coverage    1 command(s) with zero test coverage\n" +
				"        [error] no test coverage for command: build\n",
		);
		// Manifest is the union of its prior contents and the merged shards
		// (test() also records "check" itself when running check --all).
		assert.equal(
			readFileSync(join(COVERAGE_REL, "manifest.json"), "utf8"),
			'[\n  "check",\n  "deploy",\n  "status"\n]\n',
		);
	});
});

test("coverage paths are anchored to the declared directory", async () => {
	await inTempDir(async (dir) => {
		const app = coverageApp();
		const foreign = tempDir("strictcli-foreign-");
		process.chdir(foreign);
		try {
			// Recording from a foreign cwd still shards into the app's own tree.
			await app.test(["deploy"]);
			await app.test(["status"]);
			await app.test(["build"]);
			assert.equal(readdirSync(join(dir, COVERAGE_REL, "shards")).length, 1);
			assert.deepEqual(readdirSync(foreign), []);
			// The check evaluated from the foreign cwd reads the app's state.
			const result = await app.test(["check", "--all"]);
			assert.equal(result.exitCode, 0);
			assert.match(result.stdout, /all 3 commands have test coverage/);
			assert.ok(existsSync(join(dir, COVERAGE_REL, "manifest.json")));
		} finally {
			process.chdir(dir);
		}
	});
});

test("group commands are covered by their dotted path", async () => {
	await inTempDir(async () => {
		const app = createApp({
			name: "testapp",
			version: "1.0.0",
			help: "test",
			sourceTreeRoot: declaredSourceTreeRoot(process.cwd()),
		});
		const infra = app.group("infra", { help: "infra commands" });
		infra.command(
			defineReadOnlyCommand("deploy", {
				help: "deploy infra",
				handler: () => 0,
			}),
		);
		app.setCheckContext(() => CTX);

		await app.test(["infra", "deploy"]);
		const shards = readdirSync(join(COVERAGE_REL, "shards"));
		const shard = shards[0] as string;
		assert.equal(
			readFileSync(join(COVERAGE_REL, "shards", shard), "utf8"),
			'{"command":"infra.deploy"}\n',
		);
		const result = await app.test(["check", "--all"]);
		assert.equal(result.exitCode, 0);
		assert.match(result.stdout, /all 1 commands have test coverage/);
	});
});

test("the injected check command is excluded from the coverage surface", async () => {
	await inTempDir(async () => {
		// An app with zero user commands: the surface minus "check" is empty,
		// so even a lone check run (which shards "check" itself) passes.
		const app = createApp({
			name: "empty",
			version: "1.0.0",
			help: "test",
			sourceTreeRoot: declaredSourceTreeRoot(process.cwd()),
		});
		app.setCheckContext(() => CTX);
		const result = await app.test(["check", "--all"]);
		assert.equal(result.exitCode, 0);
		assert.match(result.stdout, /all 0 commands have test coverage/);
	});
});

test("shards merge across multiple files in the coverage dir", async () => {
	await inTempDir(async () => {
		const app = coverageApp();
		await app.test(["deploy"]);
		// Simulate a second process shard by writing another file directly.
		writeFileSync(
			join(COVERAGE_REL, "shards", "99999.jsonl"),
			'{"command":"status"}\n{"command":"build"}\n',
		);
		const result = await app.test(["check", "--all"]);
		assert.equal(result.exitCode, 0);
		assert.match(result.stdout, /all 3 commands have test coverage/);
	});
});

test("run() does not record coverage (test-only instrumentation)", async () => {
	await inTempDir(async () => {
		const app = coverageApp();
		await app.run(["deploy"]);
		process.exitCode = 0; // reset the exit code run() set
		// Nothing recorded, so the lazy directory was never created either.
		assert.equal(existsSync(join(COVERAGE_REL, "shards")), false);
	});
});

// The coverage directory is lazy. Shards are written only on the test-harness
// paths (test() and call()), so a plain CLI invocation must leave no
// .strictmetadata/ behind in whatever directory it was run from. Sibling parity:
// python/tests/test_coverage.py TestCoverageDirectoryIsLazy and
// go/strictcli/coverage_test.go TestCoverageDirectoryIsLazy_*.

test("coverage directory is lazy: construction leaves no coverage/", async () => {
	await inTempDir(async (dir) => {
		const app = coverageApp();
		assert.ok(app !== undefined);
		assert.equal(existsSync(join(dir, COVERAGE_REL, "shards")), false);
	});
});

test("coverage directory is lazy: recording creates the directory", async () => {
	await inTempDir(async (dir) => {
		const app = coverageApp();
		await app.test(["deploy"]);
		assert.ok(
			existsSync(join(dir, COVERAGE_REL, "shards", `${process.pid}.jsonl`)),
		);
	});
});

test("coverage directory is lazy: the check skips when it is absent", async () => {
	await inTempDir(async (dir) => {
		const app = coverageApp();
		assert.equal(existsSync(join(dir, COVERAGE_REL, "shards")), false);

		const { results } = await app.runChecks(CTX, {
			nameGlob: "cli-test-coverage",
		});
		const r = results[0];
		assert.ok(r !== undefined);
		assert.equal(r.status, "skip");
	});
});

// A coverage directory that does not exist leaves coverage off. This is the
// installed distribution: the declared root is not a source checkout, so the
// app registers no check, computes no paths and creates nothing.
// Sibling parity: python/tests/test_coverage.py TestDeclaredDirectoryAbsent and
// go/strictcli/coverage_test.go TestCoverageDeclaredDirAbsent_*.

test("a declared directory that does not exist registers no check", async () => {
	await inTempDir(async (dir) => {
		const app = createApp({
			name: "testapp",
			version: "1.0.0",
			help: "test",
			sourceTreeRoot: join(dir, "nowhere"),
		});
		app.command(
			defineReadOnlyCommand("deploy", {
				help: "deploy the app",
				handler: () => 0,
			}),
		);
		app.setCheckContext(() => CTX);

		// The check system never turned on, so `check` is not a command either.
		const result = await app.test(["check", "--all"]);
		assert.equal(result.exitCode, 1);
		assert.equal(result.stdout.includes("cli-test-coverage"), false);
	});
});

test("a declared directory that does not exist creates nothing", async () => {
	await inTempDir(async (dir) => {
		const foreign = join(dir, "some-other-project");
		mkdirSync(foreign, { recursive: true });
		process.chdir(foreign);
		const app = createApp({
			name: "testapp",
			version: "1.0.0",
			help: "test",
			sourceTreeRoot: join(dir, "gone"),
		});
		app.command(
			defineReadOnlyCommand("deploy", {
				help: "deploy the app",
				handler: () => 0,
			}),
		);

		await app.test(["deploy"]);
		app.call("deploy", {});

		assert.equal(existsSync(join(dir, "gone")), false);
		assert.deepEqual(readdirSync(foreign), []);
		process.chdir(dir);
	});
});

test("the retired boolean is refused naming the root option", async () => {
	await inTempDir(async () => {
		assert.throws(
			() =>
				createApp({
					name: "testapp",
					version: "1.0.0",
					help: "test",
					// The retired spelling: refused at registration, never honoured.
					testCoverage: true,
				} as never),
			{
				message:
					"testCoverage is not accepted; declare the source-tree root with " +
					"sourceTreeRoot, which keeps test coverage in " +
					".strictmetadata/.cli-test-coverage/ under it",
			},
		);
	});
});

test("the retired directory option is refused naming the root option", async () => {
	await inTempDir(async (dir) => {
		assert.throws(
			() =>
				createApp({
					name: "testapp",
					version: "1.0.0",
					help: "test",
					testCoverageDir: dir,
				} as never),
			{
				message:
					"testCoverageDir is not accepted; declare the source-tree root " +
					"with sourceTreeRoot, which keeps test coverage in " +
					".strictmetadata/.cli-test-coverage/ under it",
			},
		);
	});
});

// The installed distribution whose declared root exists but holds no coverage
// directory: coverage stays off and nothing is created under the root.
test("a root without a coverage directory is off and untouched", async () => {
	await inTempDir(async (dir) => {
		const root = join(dir, "installed");
		mkdirSync(root);
		const app = createApp({
			name: "testapp",
			version: "1.0.0",
			help: "test",
			sourceTreeRoot: root,
		});
		app.command(
			defineReadOnlyCommand("deploy", {
				help: "deploy the app",
				handler: () => 0,
			}),
		);
		await app.test(["deploy"]);
		const result = await app.test(["check", "--all"]);
		assert.equal(result.stdout.includes("cli-test-coverage"), false);
		assert.deepEqual(readdirSync(root), []);
	});
});

test("shards and the manifest sit under the root's coverage directory", async () => {
	await inTempDir(async (dir) => {
		const app = coverageApp();
		await app.test(["deploy"]);
		const base = join(dir, ".strictmetadata", ".cli-test-coverage");
		assert.ok(existsSync(join(base, "shards", `${process.pid}.jsonl`)));
		await app.runChecks(CTX, { nameGlob: "cli-test-coverage" });
		const manifest = JSON.parse(
			readFileSync(join(base, "manifest.json"), "utf8"),
		);
		assert.ok(manifest.includes("deploy"));
	});
});
