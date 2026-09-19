/**
 * Test-coverage instrumentation tests: shard files written by test(), the
 * canonical manifest, and the built-in cli-test-coverage provider check.
 *
 * Each test chdirs into a fresh temp directory and declares that directory's
 * .strictcli as the app's coverage directory, so the relative paths below and
 * the declared paths name the same files. Expectations derive from
 * conformance/cases/test_coverage.json and go/strictcli/coverage.go /
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
 * Creates the directory an app declares through testCoverageDir and returns
 * its absolute path. The option takes effect only when the directory already
 * exists at construction, which is what makes an installed distribution --
 * whose declared path is gone -- uninstrumented.
 */
function declaredCoverageDir(dir: string): string {
	const declared = join(dir, ".strictcli");
	mkdirSync(declared, { recursive: true });
	return declared;
}

/** The three-command mirror app from conformance test_coverage.json. */
function coverageApp(): App {
	const app = createApp({
		name: "testapp",
		version: "1.0.0",
		help: "test",
		testCoverageDir: declaredCoverageDir(process.cwd()),
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

test("testCoverageDir shards on test()", async () => {
	await inTempDir(async () => {
		const app = coverageApp();
		await app.test(["deploy"]);
		await app.test(["deploy"]);
		const shards = readdirSync(join(".strictcli", "coverage"));
		assert.equal(shards.length, 1);
		const shard = shards[0] as string;
		// One shard per process, named <pid>.jsonl (sibling parity: no counter).
		assert.equal(shard, `${process.pid}.jsonl`);
		assert.equal(
			readFileSync(join(".strictcli", "coverage", shard), "utf8"),
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
			readFileSync(join(".strictcli", "test-coverage.json"), "utf8"),
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
			`no coverage state at ${join(dir, ".strictcli")} -- cli-test-coverage` +
				" applies to the app's own development tree",
		);
	});
});

test("committed manifest yields a deterministic pass with no shards", async () => {
	await inTempDir(async () => {
		mkdirSync(".strictcli", { recursive: true });
		writeFileSync(
			join(".strictcli", "test-coverage.json"),
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
			readFileSync(join(".strictcli", "test-coverage.json"), "utf8"),
			'[\n  "build",\n  "deploy",\n  "status"\n]\n',
		);
	});
});

test("partial manifest fails honestly and rewrites the monotonic union", async () => {
	await inTempDir(async () => {
		mkdirSync(".strictcli", { recursive: true });
		writeFileSync(
			join(".strictcli", "test-coverage.json"),
			'[\n  "deploy"\n]\n',
		);
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
			readFileSync(join(".strictcli", "test-coverage.json"), "utf8"),
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
			assert.equal(readdirSync(join(dir, ".strictcli", "coverage")).length, 1);
			assert.deepEqual(readdirSync(foreign), []);
			// The check evaluated from the foreign cwd reads the app's state.
			const result = await app.test(["check", "--all"]);
			assert.equal(result.exitCode, 0);
			assert.match(result.stdout, /all 3 commands have test coverage/);
			assert.ok(existsSync(join(dir, ".strictcli", "test-coverage.json")));
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
			testCoverageDir: declaredCoverageDir(process.cwd()),
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
		const shards = readdirSync(join(".strictcli", "coverage"));
		const shard = shards[0] as string;
		assert.equal(
			readFileSync(join(".strictcli", "coverage", shard), "utf8"),
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
			testCoverageDir: declaredCoverageDir(process.cwd()),
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
			join(".strictcli", "coverage", "99999.jsonl"),
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
		assert.equal(existsSync(join(".strictcli", "coverage")), false);
	});
});

// The coverage directory is lazy. Shards are written only on the test-harness
// paths (test() and call()), so a plain CLI invocation must leave no
// .strictcli/ behind in whatever directory it was run from. Sibling parity:
// python/tests/test_coverage.py TestCoverageDirectoryIsLazy and
// go/strictcli/coverage_test.go TestCoverageDirectoryIsLazy_*.

test("coverage directory is lazy: construction leaves no coverage/", async () => {
	await inTempDir(async (dir) => {
		const app = coverageApp();
		assert.ok(app !== undefined);
		assert.equal(existsSync(join(dir, ".strictcli", "coverage")), false);
	});
});

test("coverage directory is lazy: recording creates the directory", async () => {
	await inTempDir(async (dir) => {
		const app = coverageApp();
		await app.test(["deploy"]);
		assert.ok(
			existsSync(join(dir, ".strictcli", "coverage", `${process.pid}.jsonl`)),
		);
	});
});

test("coverage directory is lazy: the check skips when it is absent", async () => {
	await inTempDir(async (dir) => {
		const app = coverageApp();
		assert.equal(existsSync(join(dir, ".strictcli", "coverage")), false);

		const { results } = await app.runChecks(CTX, {
			nameGlob: "cli-test-coverage",
		});
		const r = results[0];
		assert.ok(r !== undefined);
		assert.equal(r.status, "skip");
	});
});

// A declared directory that does not exist leaves coverage off. This is the
// installed distribution: the path naming the source checkout is simply not
// there, so the app registers no check, computes no paths and creates nothing.
// Sibling parity: python/tests/test_coverage.py TestDeclaredDirectoryAbsent and
// go/strictcli/coverage_test.go TestCoverageDeclaredDirAbsent_*.

test("a declared directory that does not exist registers no check", async () => {
	await inTempDir(async (dir) => {
		const app = createApp({
			name: "testapp",
			version: "1.0.0",
			help: "test",
			testCoverageDir: join(dir, "nowhere", ".strictcli"),
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
			testCoverageDir: join(dir, "gone", ".strictcli"),
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

test("the retired boolean is refused naming the directory option", async () => {
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
					"testCoverage is not accepted; declare the directory holding " +
					"coverage/ and test-coverage.json with testCoverageDir",
			},
		);
	});
});
