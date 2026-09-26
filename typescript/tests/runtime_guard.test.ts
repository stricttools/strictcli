/**
 * The runtime guard (effects contract §19.12): in machine mode, while the
 * handler runs, process.stdout.write is replaced so a byte that reaches stdout
 * outside the framework fails the run, and process.exit is trapped so a call
 * to it ends the command through the exit step.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import {
	type App,
	createApp,
	defineReadOnlyCommand,
	ExitNow,
} from "../src/index.js";
import { envelope } from "./envelope_helpers.js";
import { runAppInChild } from "./helpers.js";

function app(handler: () => unknown): App {
	const a = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	a.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: handler as () => number,
		}),
	);
	return a;
}

/** An app whose one command runs `handlerBody`, for a child process. */
function childApp(handlerBody: string, ownsStdout = false): string {
	return `
const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
app.command(defineReadOnlyCommand("cmd", {
	help: "a command",
	ownsStdout: ${ownsStdout},
	handler: (_args, ctx) => { ${handlerBody} },
}));`;
}

const STRAY_DIAGNOSTIC =
	'stdout written outside the framework: 6 bytes: "stray\\n"';

test("runtime guard: a direct stdout write under --json fails the run and names the bytes", async () => {
	const r = runAppInChild(
		childApp('process.stdout.write("stray\\n"); return 0;'),
		["--json", "cmd"],
	);
	assert.equal(r.status, 1);
	const env = envelope(r.stdout);
	assert.equal(env.exit_code, 1);
	assert.deepEqual(env.diagnostics, [
		{ level: "error", message: STRAY_DIAGNOSTIC },
	]);
	assert.equal(r.stderr, "");
});

test("runtime guard: app.test() counts a handler's direct write and its console.log", async () => {
	const r = await app(() => {
		process.stdout.write("stray\n");
		console.log("more");
		return 0;
	}).test(["--json", "cmd"]);
	assert.equal(r.exitCode, 1);
	const env = envelope(r.stdout);
	assert.deepEqual(env.diagnostics, [
		{
			level: "error",
			message:
				'stdout written outside the framework: 11 bytes: "stray\\nmore\\n"',
		},
	]);
});

test("runtime guard: an async handler's writes after an await are counted", async () => {
	const r = await app(async () => {
		await new Promise((resolve) => setImmediate(resolve));
		process.stdout.write("late");
		return 0;
	}).test(["--json", "cmd"]);
	assert.equal(r.exitCode, 1);
	assert.deepEqual(envelope(r.stdout).diagnostics, [
		{
			level: "error",
			message: 'stdout written outside the framework: 4 bytes: "late"',
		},
	]);
});

test("runtime guard: a nonzero status the handler chose is kept", async () => {
	const r = await app(() => {
		process.stdout.write("stray\n");
		return 4;
	}).test(["--json", "cmd"]);
	assert.equal(r.exitCode, 4);
});

test("runtime guard: the excerpt is the first 4096 bytes and the count is the total", async () => {
	const r = await app(() => {
		process.stdout.write("a".repeat(5000));
		return 0;
	}).test(["--json", "cmd"]);
	const diag = (envelope(r.stdout).diagnostics as { message: string }[])[0];
	assert.equal(
		diag?.message,
		`stdout written outside the framework: 5000 bytes: "${"a".repeat(4096)}"`,
	);
});

test("runtime guard: an early exit keeps its code and the guard diagnostic follows its message", async () => {
	const r = await app(() => {
		process.stdout.write("stray\n");
		throw new ExitNow(3, "stopped");
	}).test(["--json", "cmd"]);
	assert.equal(r.exitCode, 3);
	assert.deepEqual(envelope(r.stdout).diagnostics, [
		{ level: "error", message: "stopped" },
		{ level: "error", message: STRAY_DIAGNOSTIC },
	]);
});

test("runtime guard: human mode redirects nothing", async () => {
	const r = runAppInChild(
		childApp('process.stdout.write("stray\\n"); return 0;'),
		["cmd"],
	);
	assert.equal(r.stdout, "stray\n");
	assert.equal(r.status, 0);
});

test("runtime guard: process.stdout.write and process.exit are restored after the handler", async () => {
	const write = process.stdout.write;
	const exit = process.exit;
	await app(() => 0).test(["--json", "cmd"]);
	assert.equal(process.stdout.write, write);
	assert.equal(process.exit, exit);
});

test("runtime guard: an owns-stdout command's raw write fails the run while its document is kept", async () => {
	const r = runAppInChild(
		childApp(
			'ctx.document().write("DOC\\n"); process.stdout.write("x"); return 0;',
			true,
		),
		["--json", "cmd"],
	);
	assert.equal(r.stdout, "DOC\n");
	assert.equal(r.status, 1);
	assert.deepEqual(envelope(r.stderr).diagnostics, [
		{
			level: "error",
			message: 'stdout written outside the framework: 1 bytes: "x"',
		},
	]);
});

test("runtime guard: a trapped process exit ends the run with the requested code", async () => {
	const r = runAppInChild(childApp("process.exit(5);"), ["--json", "cmd"]);
	assert.equal(r.status, 5);
	assert.deepEqual(envelope(r.stdout).diagnostics, [
		{
			level: "error",
			message: "process exit called outside the framework with code 5",
		},
	]);
});

test("runtime guard: a trapped process exit with code 0 fails the run with 1", async () => {
	const r = await app(() => {
		process.exit(0);
	}).test(["--json", "cmd"]);
	assert.equal(r.exitCode, 1);
	assert.deepEqual(envelope(r.stdout).diagnostics, [
		{
			level: "error",
			message: "process exit called outside the framework with code 0",
		},
	]);
});
