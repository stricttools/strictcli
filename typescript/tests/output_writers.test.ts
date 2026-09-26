/**
 * The framework's output writers: `ctx.out` and the `--json` document's
 * `output` member (effects contract §19.10, §19.2's box), a declared payload
 * renderer (§19.10), the owns-stdout document writer (§19.6's box), and the
 * human-mode prefixes of `ctx.warn` and `ctx.error` (§19.14).
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import {
	type App,
	createApp,
	defineMutatingCommand,
	defineReadOnlyCommand,
	outcome,
} from "../src/index.js";
import { envelope } from "./envelope_helpers.js";

function app(): App {
	return createApp({ name: "myapp", version: "1.0.0", help: "test app" });
}

function answerApp(): App {
	const a = app();
	a.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: (_args, ctx) => {
				ctx.info("loading");
				ctx.out("hello");
				ctx.out("world");
				return 0;
			},
		}),
	);
	return a;
}

test("ctx.out: human mode writes each text and a newline to stdout", async () => {
	const r = await answerApp().test(["cmd"]);
	assert.equal(r.stdout, "loading\nhello\nworld\n");
	assert.equal(r.stderr, "");
});

test("ctx.out: --quiet hides info but never the answer", async () => {
	const r = await answerApp().test(["--quiet", "cmd"]);
	assert.equal(r.stdout, "hello\nworld\n");
});

test("ctx.out: under --json the bytes go into the output member", async () => {
	for (const argv of [
		["--json", "cmd"],
		["--json", "--quiet", "cmd"],
	]) {
		const r = await answerApp().test(argv);
		const env = envelope(r.stdout);
		assert.equal(env.output, "hello\nworld\n");
		assert.deepEqual(env.diagnostics, [{ level: "info", message: "loading" }]);
		assert.equal(r.stdout.split("\n").length, 2, "one document, one newline");
	}
});

test("ctx.out: the output member is null when nothing was written", async () => {
	const a = app();
	a.command(
		defineReadOnlyCommand("cmd", { help: "a command", handler: () => 0 }),
	);
	const env = envelope((await a.test(["--json", "cmd"])).stdout);
	assert.equal(env.interface_version, 3);
	assert.equal(env.output, null);
	assert.deepEqual(Object.keys(env), [
		"interface_version",
		"app",
		"app_version",
		"command",
		"exit_code",
		"payload",
		"output",
		"dry_run",
		"writes",
		"preview",
		"preview_error",
		"diagnostics",
	]);
});

test("ctx.out: a run that ended before a command resolved carries a null output", async () => {
	const env = envelope((await answerApp().test(["--json", "nosuch"])).stdout);
	assert.equal(env.interface_version, 3);
	assert.equal(env.output, null);
});

test("ctx.out: refused on a command that owns stdout", async () => {
	const a = app();
	a.command(
		defineReadOnlyCommand("dump", {
			help: "dump",
			ownsStdout: true,
			handler: (_args, ctx) => {
				ctx.out("x");
				return 0;
			},
		}),
	);
	await assert.rejects(() => a.test(["dump"]), {
		message:
			'command "dump": ctx.out is refused on a command that owns stdout: write the document through ctx.document',
	});
});

function statusApp(
	handler: (ctx: { payload(v: unknown): void; out(t: string): void }) => void,
): App {
	const a = app();
	a.command(
		defineMutatingCommand("status", {
			help: "status",
			payloadSchema: { type: "object" },
			payloadRenderer: (p) => {
				const v = p as { table: string; rows: number };
				return `${v.table}: ${v.rows} rows`;
			},
			handler: (_args, ctx) => {
				ctx.effects.write("out.txt", "hi");
				ctx.info("syncing");
				handler(ctx);
				return outcome(0);
			},
		}),
	);
	return a;
}

const supply = (ctx: { payload(v: unknown): void }): void =>
	ctx.payload({ table: "users", rows: 3 });

test("payload renderer: human mode prints the rendering after the handler's output and before the log", async () => {
	const r = await statusApp(supply).test(["--dry-run", "status"]);
	assert.equal(
		r.stdout,
		"syncing\nusers: 3 rows\nDRY RUN — no changes were made. Would do:\n  1. write: out.txt (2 bytes)\n",
	);
});

test("payload renderer: --quiet does not hide the rendering", async () => {
	const r = await statusApp(supply).test(["--quiet", "--dry-run", "status"]);
	assert.match(r.stdout, /^users: 3 rows\n/);
});

test("payload renderer: under --json only the payload is emitted", async () => {
	let calls = 0;
	const a = app();
	a.command(
		defineReadOnlyCommand("status", {
			help: "status",
			payloadSchema: { type: "object" },
			payloadRenderer: () => {
				calls++;
				return "rendered";
			},
			handler: (_args, ctx) => {
				ctx.payload({ rows: 3 });
				return 0;
			},
		}),
	);
	const env = envelope((await a.test(["--json", "status"])).stdout);
	assert.deepEqual(env.payload, { rows: 3 });
	assert.equal(env.output, null);
	assert.equal(calls, 0);
});

test("payload renderer: no payload renders nothing", async () => {
	const r = await statusApp(() => {}).test(["--dry-run", "status"]);
	assert.doesNotMatch(r.stdout, /rows/);
});

test("payload renderer: ctx.out is refused on a command that declares one", async () => {
	await assert.rejects(
		() => statusApp((ctx) => ctx.out("x")).test(["--dry-run", "status"]),
		{
			message:
				'command "status": ctx.out is refused on a command that declares a payload renderer: the rendering is its human output',
		},
	);
});

test("payload renderer: declaring one without a payload schema is a registration error", () => {
	assert.throws(
		() =>
			defineReadOnlyCommand("status", {
				help: "status",
				payloadRenderer: () => "",
				handler: () => 0,
			}),
		{
			message:
				'command "status": a payload renderer requires a declared payload schema',
		},
	);
});

test("payload renderer: declaring one on an owns-stdout command is a registration error", () => {
	assert.throws(
		() =>
			defineReadOnlyCommand("dump", {
				help: "dump",
				payloadSchema: {},
				ownsStdout: true,
				payloadRenderer: () => "",
				handler: () => 0,
			}),
		{
			message:
				'command "dump": a payload renderer cannot be declared on a command that owns stdout',
		},
	);
});

function dumpApp(): App {
	const a = app();
	a.command(
		defineReadOnlyCommand("dump", {
			help: "dump",
			ownsStdout: true,
			handler: (_args, ctx) => {
				const doc = ctx.document();
				doc.write("CREATE TABLE t ");
				doc.write(new TextEncoder().encode("(id int);\n"));
				return 0;
			},
		}),
	);
	return a;
}

test("ctx.document: human mode and --quiet write the bytes to stdout unchanged", async () => {
	for (const argv of [["dump"], ["--quiet", "dump"]]) {
		const r = await dumpApp().test(argv);
		assert.equal(r.stdout, "CREATE TABLE t (id int);\n");
		assert.equal(r.stderr, "");
	}
});

test("ctx.document: under --json the document keeps stdout and the --json document goes to stderr", async () => {
	const r = await dumpApp().test(["--json", "dump"]);
	assert.equal(r.exitCode, 0);
	assert.equal(r.stdout, "CREATE TABLE t (id int);\n");
	const env = envelope(r.stderr);
	assert.equal(env.output, null);
	assert.deepEqual(env.diagnostics, []);
});

test("ctx.document: refused on a command that does not own stdout", async () => {
	const a = app();
	a.command(
		defineReadOnlyCommand("dump", {
			help: "dump",
			handler: (_args, ctx) => {
				ctx.document();
				return 0;
			},
		}),
	);
	await assert.rejects(() => a.test(["dump"]), {
		message:
			'command "dump": ctx.document requires the owns-stdout declaration',
	});
});

function diagnosticsApp(messages: [string, string][]): App {
	const a = app();
	a.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: (_args, ctx) => {
				for (const [level, msg] of messages) {
					if (level === "warn") ctx.warn(msg);
					else if (level === "error") ctx.error(msg);
					else if (level === "info") ctx.info(msg);
					else ctx.debug(msg);
				}
				return 0;
			},
		}),
	);
	return a;
}

test("prefixes: human mode prints warning: and error: before the message, also under --quiet", async () => {
	for (const argv of [["cmd"], ["--quiet", "cmd"]]) {
		const r = await diagnosticsApp([
			["warn", "disk almost full"],
			["error", "upload failed"],
		]).test(argv);
		assert.equal(r.stderr, "warning: disk almost full\nerror: upload failed\n");
	}
});

test("prefixes: added once before the whole message, whatever it says", async () => {
	const r = await diagnosticsApp([["error", "error: two\nlines"]]).test([
		"cmd",
	]);
	assert.equal(r.stderr, "error: error: two\nlines\n");
});

test("prefixes: info and debug stay unprefixed", async () => {
	const r = await diagnosticsApp([
		["info", "loading"],
		["debug", "cache hit"],
	]).test(["--verbose", "cmd"]);
	assert.equal(r.stdout, "loading\ncache hit\n");
});

test("prefixes: under --json the diagnostics carry the messages unprefixed", async () => {
	const r = await diagnosticsApp([
		["warn", "disk almost full"],
		["error", "upload failed"],
	]).test(["--json", "cmd"]);
	assert.deepEqual(envelope(r.stdout).diagnostics, [
		{ level: "warn", message: "disk almost full" },
		{ level: "error", message: "upload failed" },
	]);
});
