/**
 * A child's stdout in machine mode (effects contract §19.11): the stdout of a
 * child run through `spawn`, or through `run` with `stream` true, is captured
 * into the `--json` document's `output` member instead of reaching stdout
 * beside it. Human mode, and an owns-stdout command in either mode, stream to
 * the real stdout as before -- those cases run in a child process, whose
 * stdout is not the test runner's.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import { type App, createApp, defineMutatingCommand } from "../src/index.js";
import { envelope } from "./envelope_helpers.js";
import { runAppInChild } from "./helpers.js";

function app(
	handler: Parameters<typeof defineMutatingCommand>[1]["handler"],
): App {
	const a = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	a.command(defineMutatingCommand("build", { help: "build", handler }));
	return a;
}

test("child stdout: under --json a streamed run's stdout goes into output, in arrival order", async () => {
	const r = await app((_args, ctx) => {
		ctx.out("before");
		ctx.effects.run(["echo", "performed"], { stream: true });
		ctx.out("after");
		return 0;
	}).test(["--json", "build"]);
	assert.equal(r.exitCode, 0);
	const env = envelope(r.stdout);
	assert.equal(env.output, "before\nperformed\nafter\n");
});

test("child stdout: invalid UTF-8 from a child is replaced, never dropped", async () => {
	const r = await app((_args, ctx) => {
		ctx.effects.run(["printf", "a\\377b"], { stream: true });
		return 0;
	}).test(["--json", "build"]);
	assert.equal(envelope(r.stdout).output, "a�b");
});

test("child stdout: a spawned child's stdout is captured by its wait()", async () => {
	const r = await app((_args, ctx) => {
		const child = ctx.effects.spawn(["echo", "spawned"]);
		child.wait();
		ctx.out("done");
		return 0;
	}).test(["--json", "build"]);
	assert.equal(envelope(r.stdout).output, "spawned\ndone\n");
});

test("child stdout: a spawned child never waited on is drained by the exit step", async () => {
	const r = await app((_args, ctx) => {
		ctx.effects.spawn(["echo", "unwaited"]);
		return 0;
	}).test(["--json", "build"]);
	assert.equal(envelope(r.stdout).output, "unwaited\n");
});

test("child stdout: a run with stream false still captures into its Completed", async () => {
	let seen = "";
	const r = await app((_args, ctx) => {
		seen = ctx.effects.run(["echo", "quiet"]).stdout;
		return 0;
	}).test(["--json", "build"]);
	assert.equal(seen, "quiet");
	assert.equal(envelope(r.stdout).output, null);
});

test("child stdout: human mode streams a child to the real stdout", () => {
	const r = runAppInChild(
		`
const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
app.command(defineMutatingCommand("build", {
	help: "build",
	handler: (_args, ctx) => { ctx.effects.run(["echo", "performed"], { stream: true }); return 0; },
}));`,
		["build"],
	);
	assert.equal(r.status, 0);
	assert.equal(r.stdout, "performed\n");
});

test("child stdout: on an owns-stdout command a child's stdout is part of the document in machine mode", () => {
	const r = runAppInChild(
		`
const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
app.command(defineMutatingCommand("dump", {
	help: "dump",
	ownsStdout: true,
	handler: (_args, ctx) => { ctx.effects.run(["echo", "CREATE TABLE t;"], { stream: true }); return 0; },
}));`,
		["--json", "dump"],
	);
	assert.equal(r.status, 0);
	assert.equal(r.stdout, "CREATE TABLE t;\n");
	assert.equal(envelope(r.stderr).output, null);
});
