/**
 * The early exit (effects contract §19.9): `throw new ExitNow(code, message)`
 * ends the command from anywhere in the handler's call stack through the one
 * exit step, and the in-process door rejects with ExitError.
 */

import { strict as assert } from "node:assert";
import { Readable } from "node:stream";
import { test } from "node:test";
import {
	type App,
	createApp,
	defineMutatingCommand,
	defineReadOnlyCommand,
	ExitError,
	ExitNow,
	InvokeError,
} from "../src/index.js";
import { envelope } from "./envelope_helpers.js";

function app(): App {
	return createApp({ name: "myapp", version: "1.0.0", help: "test app" });
}

/** A helper three frames below the handler, as a consumer's die() would be. */
function requireManifest(present: boolean): void {
	if (!present) {
		throw new ExitNow(3, "no manifest at ./m.toml");
	}
}

function earlyExitApp(ran: string[] = []): App {
	const a = app();
	a.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			payloadSchema: {},
			handler: (_args, ctx) => {
				ctx.warn("cache is stale");
				ctx.payload({ checked: 2 });
				try {
					requireManifest(false);
				} finally {
					ran.push("finally");
				}
				return 0;
			},
		}),
	);
	return a;
}

test("early exit: human mode prints error: <message> and exits with the code", async () => {
	const ran: string[] = [];
	const r = await earlyExitApp(ran).test(["cmd"]);
	assert.equal(r.exitCode, 3);
	assert.equal(r.stdout, "");
	assert.equal(
		r.stderr,
		"warning: cache is stale\nerror: no manifest at ./m.toml\n",
	);
	assert.deepEqual(ran, ["finally"]);
});

test("early exit: a payload supplied before it is kept", async () => {
	const r = await earlyExitApp().test(["cmd"]);
	assert.deepEqual(r.data, { checked: 2 });
});

test("early exit: under --json the message is the last error diagnostic", async () => {
	const r = await earlyExitApp().test(["--json", "cmd"]);
	assert.equal(r.exitCode, 3);
	assert.equal(r.stderr, "");
	const env = envelope(r.stdout);
	assert.equal(env.interface_version, 3);
	assert.equal(env.exit_code, 3);
	assert.deepEqual(env.payload, { checked: 2 });
	assert.deepEqual(env.diagnostics, [
		{ level: "warn", message: "cache is stale" },
		{ level: "error", message: "no manifest at ./m.toml" },
	]);
});

test("early exit: --quiet never hides the message", async () => {
	const r = await earlyExitApp().test(["--quiet", "cmd"]);
	assert.equal(r.exitCode, 3);
	assert.match(r.stderr, /error: no manifest at \.\/m\.toml\n$/);
});

test("early exit: a dry run renders its would-do log as for a return", async () => {
	const a = app();
	a.command(
		defineMutatingCommand("build", {
			help: "build",
			handler: (_args, ctx) => {
				ctx.effects.write("out.txt", "hi");
				throw new ExitNow(4, "stopped");
			},
		}),
	);
	const r = await a.test(["--dry-run", "build"]);
	assert.equal(r.exitCode, 4);
	assert.equal(
		r.stdout,
		"DRY RUN — no changes were made. Would do:\n  1. write: out.txt (2 bytes)\n",
	);
	assert.equal(r.stderr, "error: stopped\n");
});

test("early exit: the constructor refuses code 0, codes above 255, non-integers, and an empty message", () => {
	assert.throws(() => new ExitNow(0, "done"), {
		message:
			"early exit requires an exit code between 1 and 255, got 0: a successful run ends with a return from the handler",
	});
	assert.throws(() => new ExitNow(256, "big"), /got 256:/);
	assert.throws(() => new ExitNow(2.5, "half"), /got 2\.5:/);
	assert.throws(() => new ExitNow(2, ""), {
		message: "early exit requires a non-empty message",
	});
});

test("early exit: a refused early exit unwinds as a programming error", async () => {
	const a = app();
	a.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: () => {
				throw new ExitNow(0, "done");
			},
		}),
	);
	await assert.rejects(() => a.test(["cmd"]), /got 0:/);
});

test("early exit: app.call() rejects with ExitError carrying the code and the message", async () => {
	const a = earlyExitApp();
	const e = await a.call("cmd", {}).then(
		() => assert.fail("call resolved"),
		(err: unknown) => err,
	);
	assert.ok(e instanceof ExitError);
	assert.ok(!(e instanceof InvokeError));
	assert.equal(e.code, 3);
	assert.equal(e.message, "no manifest at ./m.toml");
	// The payload supplied before the early exit rides the error, as test()
	// reports it (§19.9's box).
	assert.deepEqual(e.payload, { checked: 2 });
	assert.deepEqual((await a.test(["cmd"])).data, { checked: 2 });
});

test("early exit: ExitError's payload is null when the command supplied none", async () => {
	const a = app();
	a.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: () => {
				throw new ExitNow(3, "gone");
			},
		}),
	);
	const e = await a.call("cmd", {}).then(
		() => assert.fail("call resolved"),
		(err: unknown) => err,
	);
	assert.ok(e instanceof ExitError);
	assert.equal(e.payload, null);
});

test("early exit: an MCP tools/call answers isError with the code and the message", async () => {
	const chunks: string[] = [];
	const request = {
		jsonrpc: "2.0",
		id: 1,
		method: "tools/call",
		params: {
			name: "cmd",
			arguments: {},
			_meta: {
				"io.modelcontextprotocol/protocolVersion": "2026-07-28",
				"io.modelcontextprotocol/clientCapabilities": {},
			},
		},
	};
	await earlyExitApp().serveMcp({
		input: Readable.from(`${JSON.stringify(request)}\n`),
		output: { write: (s) => chunks.push(s) },
	});
	const resp = JSON.parse(chunks.join("").trim()) as {
		result: { isError?: boolean; content: { text: string }[] };
	};
	assert.equal(resp.result.isError, true);
	assert.equal(
		resp.result.content[0]?.text,
		"exit code 3: no manifest at ./m.toml",
	);
});

test("early exit: thrown by a passthrough handler it ends the command the same way", async () => {
	const a = app();
	const { readOnlyPassthrough } = await import("../src/index.js");
	a.command(
		readOnlyPassthrough("pt", {
			help: "forward",
			handler: () => {
				throw new ExitNow(9, "child missing");
			},
		}),
	);
	const r = await a.test(["pt"]);
	assert.equal(r.exitCode, 9);
	assert.equal(r.stderr, "error: child missing\n");
});
