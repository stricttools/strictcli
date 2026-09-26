/**
 * Framework-owned signal handling (effects contract §19.13). Under app.run()
 * the first SIGINT or SIGTERM aborts ctx.signal; when the handler returns, the
 * command exits 128 + the signal's number with the diagnostic naming it. A
 * second signal gets the default action. The signal-driven cases run in a
 * child process: signals are process-wide.
 */

import { strict as assert } from "node:assert";
import { spawn } from "node:child_process";
import { test } from "node:test";
import { createApp, defineReadOnlyCommand } from "../src/index.js";
import { envelope } from "./envelope_helpers.js";
import { childAppScript, runAppInChild } from "./helpers.js";

/** A command that signals itself, waits for the abort, and then answers. */
function selfSignalApp(signal: string, after: string): string {
	return `
const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
app.command(defineReadOnlyCommand("cmd", {
	help: "a command",
	handler: async (_args, ctx) => {
		ctx.info("working");
		process.kill(process.pid, ${JSON.stringify(signal)});
		// A signal listener does not keep the event loop alive; the timer does.
		const keepAlive = setInterval(() => {}, 1000);
		await new Promise((resolve) => ctx.signal.addEventListener("abort", resolve));
		clearInterval(keepAlive);
		${after}
	},
}));`;
}

test("signals: SIGTERM during the handler cancels ctx.signal and exits 143", () => {
	const r = runAppInChild(
		selfSignalApp("SIGTERM", 'ctx.out("stopping"); return 0;'),
		["cmd"],
	);
	assert.equal(r.status, 143);
	assert.equal(r.stdout, "working\nstopping\n");
	assert.equal(r.stderr, "error: canceled by signal SIGTERM\n");
});

test("signals: SIGINT exits 130", () => {
	const r = runAppInChild(selfSignalApp("SIGINT", "return 0;"), ["cmd"]);
	assert.equal(r.status, 130);
	assert.equal(r.stderr, "error: canceled by signal SIGINT\n");
});

test("signals: the signal's status replaces the one the handler chose, an early exit's included", () => {
	const r = runAppInChild(
		selfSignalApp("SIGTERM", 'throw new ExitNow(7, "gave up");'),
		["cmd"],
	);
	assert.equal(r.status, 143);
	assert.equal(r.stderr, "error: gave up\nerror: canceled by signal SIGTERM\n");
});

test("signals: under --json the document carries 143 and the signal diagnostic last", () => {
	const r = runAppInChild(
		selfSignalApp("SIGTERM", 'ctx.out("stopping"); return 0;'),
		["--json", "cmd"],
	);
	assert.equal(r.status, 143);
	const env = envelope(r.stdout);
	assert.equal(env.exit_code, 143);
	assert.equal(env.output, "stopping\n");
	assert.deepEqual(env.diagnostics, [
		{ level: "info", message: "working" },
		{ level: "error", message: "canceled by signal SIGTERM" },
	]);
});

test("signals: a second signal gets the default action", async () => {
	const body = `
const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
app.command(defineReadOnlyCommand("cmd", {
	help: "a command",
	handler: async (_args, ctx) => {
		ctx.signal.addEventListener("abort", () => process.stderr.write("aborted\\n"));
		process.stderr.write("ready\\n");
		await new Promise(() => setInterval(() => {}, 1000));
	},
}));`;
	const child = spawn(
		process.execPath,
		["--input-type=module", "-e", childAppScript(body, ["cmd"])],
		{ stdio: ["ignore", "pipe", "pipe"] },
	);
	let stderr = "";
	let sent = 0;
	child.stderr.setEncoding("utf8");
	child.stderr.on("data", (chunk: string) => {
		stderr += chunk;
		if (sent === 0 && stderr.includes("ready\n")) {
			sent = 1;
			child.kill("SIGTERM");
		} else if (sent === 1 && stderr.includes("aborted\n")) {
			sent = 2;
			child.kill("SIGTERM");
		}
	});
	const [status, signal] = await new Promise<[number | null, string | null]>(
		(resolve) => child.on("exit", (code, sig) => resolve([code, sig])),
	);
	assert.equal(status, null);
	assert.equal(signal, "SIGTERM");
});

test("signals: app.test() installs no signal handling, and ctx.signal aborts when the dispatch ends", async () => {
	let during = -1;
	let signal: AbortSignal | undefined;
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: (_args, ctx) => {
				during = process.listenerCount("SIGTERM");
				signal = ctx.signal;
				assert.equal(ctx.signal.aborted, false);
				return 0;
			},
		}),
	);
	const before = process.listenerCount("SIGTERM");
	await app.test(["cmd"]);
	assert.equal(during, before);
	assert.equal(signal?.aborted, true);
});
