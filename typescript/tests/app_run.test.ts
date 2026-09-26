/**
 * run()/test() dispatch tests: the execution surface (async handlers,
 * result interpretation, data emission, capture
 * mechanics, tag-contract enforcement). Byte expectations follow the Python
 * implementation (the divergence ground truth); e2e.test.ts pins the
 * conformance scenarios.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { ReadOnlyContext } from "../src/context.js";
import {
	createApp,
	defineReadOnlyCommand,
	flag,
	outcome,
	readOnlyPassthrough,
	t,
} from "../src/index.js";

test("test: result surface is stdout/stderr/exitCode with optional data", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			handler: (_args, ctx) => {
				ctx.info("out-line");
				ctx.warn("err-line");
				return 3;
			},
		}),
	);
	const r = await app.test(["run"]);
	assert.deepEqual(r, {
		stdout: "out-line\n",
		stderr: "warning: err-line\n",
		exitCode: 3,
	});
	assert.equal("data" in r, false);
});

test("test: captures console.log output from handlers", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			handler: () => {
				console.log("via console");
				return 0;
			},
		}),
	);
	const r = await app.test(["run"]);
	assert.equal(r.stdout, "via console\n");
});

test("test: async handlers are awaited", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("run", {
			payloadSchema: {},
			help: "run",
			handler: async (_args, ctx) => {
				await new Promise((resolve) => setTimeout(resolve, 5));
				ctx.info("after-await");
				ctx.payload({ done: true });
				return outcome(2);
			},
		}),
	);
	const r = await app.test(["run", "--json"]);
	assert.equal(
		r.stdout,
		'{"interface_version":3,"app":"myapp","app_version":"1.0.0","command":"run","exit_code":2,"payload":{"done":true},"output":null,"dry_run":false,"writes":null,"preview":[],"preview_error":null,"diagnostics":[{"level":"info","message":"after-await"}]}\n',
	);
	assert.equal(r.exitCode, 2);
	assert.deepEqual(r.data, { done: true });
});

test("test: outcome data prints one compact JSON line with BigInt tokens", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("run", {
			payloadSchema: {},
			help: "run",
			flags: {
				count: flag("count", t.int, {
					help: "count",
					presence: "default",
					default: 7n,
				}),
			},
			handler: (args, ctx) => {
				ctx.payload({ count: args.count, name: "x" });
				return outcome(0);
			},
		}),
	);
	const r = await app.test(["run", "--count", "9007199254740992", "--json"]);
	assert.equal(
		r.stdout,
		'{"interface_version":3,"app":"myapp","app_version":"1.0.0","command":"run","exit_code":0,"payload":{"count":9007199254740992,"name":"x"},"output":null,"dry_run":false,"writes":null,"preview":[],"preview_error":null,"diagnostics":[]}\n',
	);
	assert.deepEqual(r.data, { count: 9007199254740992n, name: "x" });
});

test("test: a BigInt payload past 2^53 is refused rather than emitted lossily", async () => {
	// Decision 16 (contract §19.5): the envelope is a public document and its
	// consuming ecosystem reads every number as a double, so a token no reader
	// can round-trip is refused at emission. A big identifier is a string by
	// declaration.
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("run", {
			payloadSchema: {},
			help: "run",
			flags: {
				count: flag("count", t.int, {
					help: "count",
					presence: "default",
					default: 7n,
				}),
			},
			handler: (args, ctx) => {
				ctx.payload({ count: args.count, name: "x" });
				return outcome(0);
			},
		}),
	);
	await assert.rejects(
		app.test(["run", "--count", "9007199254740993", "--json"]),
		(err: Error) =>
			err.message ===
			'command "run": payload does not satisfy the declared schema at ' +
				`payload["count"]: the number's magnitude exceeds 2^53 ` +
				"(declare a big identifier as a string)",
	);
});

test("test: bad handler returns are hard errors (propagate to the caller)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			handler: () => ["not-an-outcome"] as never,
		}),
	);
	// Python's test() lets the TypeError propagate (only SystemExit is
	// caught); the TS surface matches. Also covers "a returned Promise
	// resolving to a bad value" -- run/test await before interpreting.
	await assert.rejects(() => app.test(["run"]), {
		name: "TypeError",
		message:
			"command handler must return number (exit code), undefined (exit 0), or strictcli.outcome(...); got Array",
	});
	const asyncBad = createApp({ name: "myapp", version: "1.0.0", help: "h" });
	asyncBad.command(
		defineReadOnlyCommand("run", {
			help: "run",
			handler: async () => "nope" as never,
		}),
	);
	await assert.rejects(() => asyncBad.test(["run"]), {
		name: "TypeError",
		message:
			"command handler must return number (exit code), undefined (exit 0), or strictcli.outcome(...); got string",
	});
	// The console patches must be restored after the throw.
	const probe = await app.test(["--version"]);
	assert.equal(probe.stdout, "myapp 1.0.0\n");
});

test("test: passthrough handlers flow through the result contract", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		readOnlyPassthrough("exec", {
			payloadSchema: {},
			help: "exec",
			handler: (pt, ctx: ReadOnlyContext) => {
				ctx.info(`${pt.name}:${pt.args.join(",")}`);
				ctx.payload({ forwarded: pt.args.length });
				return outcome(4);
			},
		}),
	);
	const r = await app.test(["--json", "exec", "-x", "y"]);
	assert.equal(
		r.stdout,
		'{"interface_version":3,"app":"myapp","app_version":"1.0.0","command":"exec","exit_code":4,"payload":{"forwarded":2},"output":null,"dry_run":false,"writes":null,"preview":[],"preview_error":null,"diagnostics":[{"level":"info","message":"exec:-x,y"}]}\n',
	);
	assert.equal(r.exitCode, 4);
	assert.deepEqual(r.data, { forwarded: 2 });
});

test("test: ctx.source works during dispatch", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			flags: {
				sim_run: flag("sim-run", t.bool, {
					help: "dry",
					presence: "default",
					default: true,
				}),
			},
			handler: (_args, ctx) => {
				ctx.info(`cli=${ctx.source("sim-run")}`);
				return 0;
			},
		}),
	);
	assert.equal((await app.test(["run", "--sim-run"])).stdout, "cli=cli\n");
	assert.equal((await app.test(["run"])).stdout, "cli=default\n");
});

test("test: --mcp reports the Python in-process message", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(defineReadOnlyCommand("run", { help: "run", handler: () => 0 }));
	const r = await app.test(["--mcp"]);
	assert.equal(r.stderr, "error: --mcp requires interactive stdin/stdout\n");
	assert.equal(r.exitCode, 1);
});

test("test: tag-contract violations abort dispatch with error", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("greet", {
			help: "greet",
			tags: ["json"],
			handler: () => 0,
		}),
	);
	app.tagContract("json", "json");
	const r = await app.test(["greet"]);
	// Pinned by conformance command_tags.json ("tag contract violated").
	assert.equal(
		r.stderr,
		'error: command "greet": tag "json" requires flag "--json"\n',
	);
	assert.equal(r.exitCode, 1);
});

test("test: tag contracts satisfied by command or global flags pass", async () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
		flags: {
			as_json: flag("as-json", t.bool, {
				help: "json output",
				presence: "default",
				default: false,
			}),
		},
	});
	app.command(
		defineReadOnlyCommand("greet", {
			help: "greet",
			tags: ["json"],
			handler: (_args, ctx) => {
				ctx.info("hello");
				return 0;
			},
		}),
	);
	app.tagContract("json", "as-json");
	const r = await app.test(["greet"]);
	assert.equal(r.stdout, "hello\n");
	assert.equal(r.exitCode, 0);
});

test("test: tag contracts are enforced recursively; passthrough exempt", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	const grp = app.group("tools", { help: "tools" });
	grp.command(
		defineReadOnlyCommand("lint", {
			help: "lint",
			tags: ["json"],
			handler: () => 0,
		}),
	);
	app.tagContract("json", "json");
	const r = await app.test(["tools", "lint"]);
	assert.equal(
		r.stderr,
		'error: command "lint": tag "json" requires flag "--json"\n',
	);
	assert.equal(r.exitCode, 1);

	const ptApp = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
	});
	ptApp.command(
		readOnlyPassthrough("raw", {
			help: "raw",
			tags: ["json"],
			handler: () => 0,
		}),
	);
	ptApp.tagContract("json", "json");
	assert.equal((await ptApp.test(["raw"])).exitCode, 0);
});

test("run: writes to process streams and sets process.exitCode", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("run", {
			payloadSchema: {},
			help: "run",
			handler: (_args, ctx) => {
				ctx.info("hello-from-run");
				ctx.payload({ via: "run" });
				return outcome(5);
			},
		}),
	);
	// Capture the real process streams the same way test() does, to observe
	// what run() writes without spawning a child process.
	const chunks: string[] = [];
	const orig = process.stdout.write.bind(process.stdout);
	const origExitCode = process.exitCode;
	process.stdout.write = ((chunk: string | Uint8Array): boolean => {
		chunks.push(
			typeof chunk === "string" ? chunk : new TextDecoder().decode(chunk),
		);
		return true;
	}) as typeof process.stdout.write;
	try {
		await app.run(["run", "--json"]);
	} finally {
		process.stdout.write = orig;
	}
	assert.equal(
		chunks.join(""),
		'{"interface_version":3,"app":"myapp","app_version":"1.0.0","command":"run","exit_code":5,"payload":{"via":"run"},"output":null,"dry_run":false,"writes":null,"preview":[],"preview_error":null,"diagnostics":[{"level":"info","message":"hello-from-run"}]}\n',
	);
	assert.equal(process.exitCode, 5);
	process.exitCode = origExitCode;
});

test("run: defaults argv to process.argv.slice(2)", async () => {
	const app = createApp({ name: "myapp", version: "9.9.9", help: "test app" });
	app.command(defineReadOnlyCommand("run", { help: "run", handler: () => 0 }));
	const origArgv = process.argv;
	const chunks: string[] = [];
	const orig = process.stdout.write.bind(process.stdout);
	const origExitCode = process.exitCode;
	process.argv = [origArgv[0] as string, "myapp", "--version"];
	process.stdout.write = ((chunk: string | Uint8Array): boolean => {
		chunks.push(
			typeof chunk === "string" ? chunk : new TextDecoder().decode(chunk),
		);
		return true;
	}) as typeof process.stdout.write;
	try {
		await app.run();
	} finally {
		process.stdout.write = orig;
		process.argv = origArgv;
	}
	assert.equal(chunks.join(""), "myapp 9.9.9\n");
	assert.equal(process.exitCode, 0);
	process.exitCode = origExitCode;
});
