/**
 * A child still running when its handler ends is killed and fails the run
 * (effects contract §19.11's box): the exit step settles every child the
 * handler started through `spawn` and neither waited on nor killed -- an
 * exited one is reaped and drained, a running one gets SIGTERM, then SIGKILL a
 * second later, and is named in an error diagnostic. `Spawned` can send a
 * signal and kill.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import { type App, createApp, defineMutatingCommand } from "../src/index.js";
import { envelope } from "./envelope_helpers.js";
import { runAppInChild } from "./helpers.js";

const KILLED =
	/^child process (\d+) was still running when the handler ended and was killed: (.*)$/;

function app(
	handler: Parameters<typeof defineMutatingCommand>[1]["handler"],
): App {
	const a = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	a.command(defineMutatingCommand("start", { help: "start", handler }));
	return a;
}

/** Blocks the thread, the way a synchronous handler's own work does. */
function sleepSync(ms: number): void {
	Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

function alive(pid: number): boolean {
	try {
		process.kill(pid, 0);
		return true;
	} catch {
		return false;
	}
}

test("unwaited child: a running child is killed and the run fails", async () => {
	let pid = 0;
	const r = await app((_args, ctx) => {
		pid = ctx.effects.spawn(["sleep", "30"]).pid;
		return 0;
	}).test(["start"]);
	assert.equal(r.exitCode, 1);
	assert.equal(r.stdout, "");
	assert.equal(
		r.stderr,
		`error: child process ${pid} was still running when the handler ended and was killed: sleep 30\n`,
	);
	assert.equal(alive(pid), false);
});

test("unwaited child: a nonzero status the handler chose is kept", async () => {
	const r = await app((_args, ctx) => {
		ctx.effects.spawn(["sleep", "30"]);
		return 3;
	}).test(["start"]);
	assert.equal(r.exitCode, 3);
});

test("unwaited child: children are named in spawn order", async () => {
	const r = await app((_args, ctx) => {
		ctx.effects.spawn(["sleep", "30"]);
		ctx.effects.spawn(["sleep", "31"]);
		return 0;
	}).test(["start"]);
	const argvs = r.stderr
		.trimEnd()
		.split("\n")
		.map((line) => KILLED.exec(line.replace(/^error: /, ""))?.[2]);
	assert.deepEqual(argvs, ["sleep 30", "sleep 31"]);
});

test("unwaited child: one ignoring SIGTERM is killed after one second", async () => {
	const started = Date.now();
	const r = await app((_args, ctx) => {
		ctx.effects.spawn(["sh", "-c", "trap '' TERM; exec sleep 30"]);
		sleepSync(200);
		return 0;
	}).test(["start"]);
	const elapsed = Date.now() - started;
	assert.equal(r.exitCode, 1);
	assert.ok(elapsed >= 1000 && elapsed < 10_000, `elapsed ${elapsed}`);
});

test("unwaited child: under --json the kill is a diagnostic and what the child wrote is kept", async () => {
	const r = await app((_args, ctx) => {
		ctx.effects.spawn(["sh", "-c", "echo early; exec sleep 30"]);
		sleepSync(300);
		return 0;
	}).test(["--json", "start"]);
	assert.equal(r.exitCode, 1);
	assert.equal(r.stderr, "");
	const env = envelope(r.stdout);
	assert.equal(env.exit_code, 1);
	assert.equal(env.output, "early\n");
	const diags = env.diagnostics as { level: string; message: string }[];
	assert.equal(diags.length, 1);
	assert.equal(diags[0]?.level, "error");
	assert.match(diags[0]?.message ?? "", KILLED);
});

test("unwaited child: an exited child left unwaited is drained, not an error", async () => {
	const r = await app((_args, ctx) => {
		ctx.effects.spawn(["echo", "hi"]);
		sleepSync(500);
		return 0;
	}).test(["--json", "start"]);
	assert.equal(r.exitCode, 0);
	const env = envelope(r.stdout);
	assert.equal(env.output, "hi\n");
	assert.deepEqual(env.diagnostics, []);
});

test("unwaited child: a waited child is not settled again", async () => {
	const r = await app((_args, ctx) => {
		ctx.effects.spawn(["true"]).wait();
		return 0;
	}).test(["start"]);
	assert.equal(r.exitCode, 0);
	assert.equal(r.stderr, "");
});

test("spawned: kill ends the child, counts as a wait, and a later wait reports its status", async () => {
	let pid = 0;
	let code = 0;
	const r = await app((_args, ctx) => {
		const child = ctx.effects.spawn(["sleep", "30"]);
		pid = child.pid;
		child.kill();
		code = child.wait({ check: false }).exitCode;
		return 0;
	}).test(["start"]);
	assert.equal(r.exitCode, 0);
	assert.equal(r.stderr, "");
	assert.equal(alive(pid), false);
	assert.notEqual(code, 0);
});

test("spawned: sendSignal delivers and leaves the wait to the handler", async () => {
	const r = await app((_args, ctx) => {
		const child = ctx.effects.spawn(["sleep", "30"]);
		child.sendSignal("SIGTERM");
		child.wait({ check: false });
		return 0;
	}).test(["start"]);
	assert.equal(r.exitCode, 0);
	assert.equal(r.stderr, "");
});

test("spawned: an exited child is left alone by sendSignal and kill", async () => {
	const r = await app((_args, ctx) => {
		const child = ctx.effects.spawn(["true"]);
		child.wait();
		child.sendSignal("SIGTERM");
		child.kill();
		return 0;
	}).test(["start"]);
	assert.equal(r.exitCode, 0);
	assert.equal(r.stderr, "");
});

test("spawned: kill and sendSignal truncate on an unsettled handle", async () => {
	for (const use of [
		(s: { kill(): void }) => s.kill(),
		(s: { sendSignal(sig: NodeJS.Signals): void }) => s.sendSignal("SIGTERM"),
	]) {
		const r = await app((_args, ctx) => {
			use(ctx.effects.spawn(["sleep", "30"]) as never);
			return 0;
		}).test(["--dry-run", "start"]);
		assert.equal(r.exitCode, 1);
		assert.match(r.stderr, /dry-run preview ends at step/);
	}
});

test("unwaited child: call() kills the child and reports the failed status", async () => {
	let pid = 0;
	const got = await app((_args, ctx) => {
		pid = ctx.effects.spawn(["sleep", "30"]).pid;
		return 0;
	}).call("start", {});
	assert.equal(got, 1);
	assert.equal(alive(pid), false);
});

test("unwaited child: signal handling lasts until the children are settled", () => {
	const r = runAppInChild(
		`
const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
app.command(defineMutatingCommand("start", {
	help: "start",
	handler: (_args, ctx) => {
		ctx.effects.spawn(["sh", "-c", "trap '' TERM; exec sleep 30"]);
		Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 200);
		// A separate process delivers SIGINT while the exit step waits for
		// the child to die.
		ctx.effects.spawn(["sh", "-c", "sleep 0.5; kill -INT " + process.pid]).pid;
		return 0;
	},
}));`,
		["start"],
	);
	assert.equal(r.status, 130, r.stderr);
	const lines = r.stderr.trimEnd().split("\n");
	assert.match(lines[0]?.replace(/^error: /, "") ?? "", KILLED);
	assert.equal(lines.at(-1), "error: canceled by signal SIGINT");
});
