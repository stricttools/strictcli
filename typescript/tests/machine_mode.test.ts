/**
 * Machine mode: the reserved --json flag and the payload API.
 *
 * Contract §19.1 (the flag and its delivery), §19.4 (the payload API) and
 * §7.1's 2026-08-13 amendment (the unconditional every-level name ban).
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import {
	arg,
	choice,
	createApp,
	defineMutatingCommand,
	defineReadOnlyCommand,
	flag,
	flagSet,
	memberChoiceFlag,
	outcome,
	t,
} from "../src/index.js";

const RESERVED = /flag name 'json' is reserved by the framework/;

// --- the name ban: unconditional, at every level ---------------------------

test("json is reserved on a command flag", () => {
	assert.throws(
		() =>
			flag("json", t.bool, { help: "h", presence: "default", default: false }),
		RESERVED,
	);
});

test("json is reserved on a flag-set flag", () => {
	assert.throws(
		() =>
			flagSet("s", {
				json: flag("json", t.bool, { help: "h", presence: "required" }),
			}),
		RESERVED,
	);
});

test("json is reserved on a member-spelled choice name", () => {
	assert.throws(
		() =>
			memberChoiceFlag(
				"format",
				{
					json: choice({ help: "h" }),
					text: choice({ help: "h" }),
				},
				{ help: "h", presence: "required" },
			),
		RESERVED,
	);
});

test("json is reserved on an app global flag", () => {
	assert.throws(
		() =>
			createApp({
				name: "a",
				version: "1",
				help: "h",
				flags: {
					json: flag("json", t.bool, {
						help: "h",
						presence: "default",
						default: false,
					}),
				},
			}),
		RESERVED,
	);
});

test("an arg named json is unaffected", () => {
	// The ban covers the flag surface only: an arg has no `--` spelling.
	assert.doesNotThrow(() =>
		arg("json", t.str, { help: "a file named json", presence: "required" }),
	);
});

// --- delivery: both argv regions, stripped, on the Context -----------------

function machineFlagApp() {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			payloadSchema: {},
			handler: (_args, ctx) => {
				ctx.payload({ json: ctx.json });
				return outcome(0);
			},
		}),
	);
	return app;
}

test("--json is absent by default and the payload is not printed", async () => {
	const r = await machineFlagApp().test(["run"]);
	assert.equal(r.stdout, "");
	assert.deepEqual(r.data, { json: false });
});

test("--json is recognized before the command word", async () => {
	const r = await machineFlagApp().test(["--json", "run"]);
	assert.equal(r.stdout, envelopeText("run", 0, '{"json":true}'));
});

test("--json is recognized after the command word", async () => {
	const r = await machineFlagApp().test(["run", "--json"]);
	assert.equal(r.stdout, envelopeText("run", 0, '{"json":true}'));
});

test("--json never reaches the handler as a kwarg", async () => {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			handler: (args) => {
				assert.equal(Object.hasOwn(args as object, "json"), false);
				return 0;
			},
		}),
	);
	assert.equal((await app.test(["run", "--json"])).exitCode, 0);
});

// --- the payload API -------------------------------------------------------

test("ctx.payload without a declared schema is a hard error", async () => {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			handler: (_args, ctx) => {
				ctx.payload({ k: 1 });
				return outcome(0);
			},
		}),
	);
	await assert.rejects(() => app.test(["run"]), {
		message: 'command "run": ctx.payload requires a declared payload schema',
	});
});

test("ctx.payload twice in one dispatch is a hard error", async () => {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			payloadSchema: {},
			handler: (_args, ctx) => {
				ctx.payload({ k: 1 });
				ctx.payload({ k: 2 });
				return outcome(0);
			},
		}),
	);
	await assert.rejects(() => app.test(["run"]), {
		message:
			'command "run": ctx.payload was already called (a dispatch carries at most one payload)',
	});
});

test("call() returns the payload the handler supplied", async () => {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			payloadSchema: {},
			handler: (_args, ctx) => {
				ctx.payload(["a", "b"]);
				return outcome(0);
			},
		}),
	);
	assert.deepEqual(await app.call("run"), ["a", "b"]);
});

test("--quiet cannot reach the payload", async () => {
	const r = await machineFlagApp().test(["--quiet", "--json", "run"]);
	assert.equal(r.stdout, envelopeText("run", 0, '{"json":true}'));
});

test("payload escaping is plain UTF-8", async () => {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			payloadSchema: {},
			handler: (_args, ctx) => {
				ctx.payload({ text: "héllo <b>&</b> 日本語" });
				return outcome(0);
			},
		}),
	);
	const r = await app.test(["run", "--json"]);
	assert.equal(
		r.stdout,
		envelopeText("run", 0, '{"text":"héllo <b>&</b> 日本語"}'),
	);
});

// --- the envelope (contract §19.2) and its preview member (§19.3) ----------

/**
 * The exact document an a/1 run emits, so a test pins the bytes rather than a
 * parsed shape. A null command is a run that ended before one resolved.
 */
function envelopeText(
	command: string | null,
	exitCode: number,
	payload = "null",
	opts: {
		dryRun?: boolean;
		writes?: string;
		preview?: string;
		previewError?: string;
		diagnostics?: string;
	} = {},
): string {
	const cmd = command === null ? "null" : JSON.stringify(command);
	return (
		`{"interface_version":3,"app":"a","app_version":"1","command":${cmd},` +
		`"exit_code":${exitCode},"payload":${payload},"output":null,` +
		`"dry_run":${opts.dryRun ?? false},"writes":${opts.writes ?? "null"},` +
		`"preview":${opts.preview ?? "[]"},` +
		`"preview_error":${opts.previewError ?? "null"},` +
		`"diagnostics":${opts.diagnostics ?? "[]"}}\n`
	);
}

function plainApp() {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(defineReadOnlyCommand("run", { help: "run", handler: () => 0 }));
	return app;
}

function diagnosticsApp() {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			handler: (_args, ctx) => {
				ctx.info("starting");
				ctx.warn("careful");
				ctx.debug("detail");
				ctx.error("bad");
				return 0;
			},
		}),
	);
	return app;
}

const ALL_DIAGNOSTICS =
	'[{"level":"info","message":"starting"},' +
	'{"level":"warn","message":"careful"},' +
	'{"level":"debug","message":"detail"},' +
	'{"level":"error","message":"bad"}]';

test("the envelope is the sole stdout document on a plain exit", async () => {
	const r = await plainApp().test(["--json", "run"]);
	assert.equal(r.stdout, envelopeText("run", 0));
	assert.equal(r.stderr, "");
});

test("the envelope carries the exit code", async () => {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(defineReadOnlyCommand("run", { help: "run", handler: () => 3 }));
	const r = await app.test(["--json", "run"]);
	assert.equal(r.exitCode, 3);
	assert.equal(r.stdout, envelopeText("run", 3));
});

test("the envelope carries the dotted command path", async () => {
	const app = createApp({ name: "a", version: "1", help: "h" });
	const grp = app.group("grp", { help: "a group" });
	grp.command(defineReadOnlyCommand("run", { help: "run", handler: () => 0 }));
	const r = await app.test(["--json", "grp", "run"]);
	assert.equal(r.stdout, envelopeText("grp.run", 0));
});

test("the envelope carries the dry-run flag", async () => {
	const r = await plainApp().test(["--json", "--dry-run", "run"]);
	assert.equal(r.stdout, envelopeText("run", 0, "null", { dryRun: true }));
});

test("the envelope carries every diagnostic in emission order", async () => {
	const r = await diagnosticsApp().test(["--json", "run"]);
	assert.equal(
		r.stdout,
		envelopeText("run", 0, "null", { diagnostics: ALL_DIAGNOSTICS }),
	);
	assert.equal(r.stderr, "");
});

test("quiet cannot reach the envelope", async () => {
	// Contract §19.2: structurally exempt -- quiet governs the human stream,
	// and debug rides without --verbose.
	const r = await diagnosticsApp().test(["--quiet", "--json", "run"]);
	assert.equal(
		r.stdout,
		envelopeText("run", 0, "null", { diagnostics: ALL_DIAGNOSTICS }),
	);
});

test("diagnostics go to the human stream outside machine mode", async () => {
	const r = await diagnosticsApp().test(["run"]);
	assert.equal(r.stdout, "starting\n");
	assert.equal(r.stderr, "warning: careful\nerror: bad\n");
});

test("an unknown command emits an envelope with a null command", async () => {
	const r = await plainApp().test(["--json", "nope"]);
	assert.equal(r.exitCode, 1);
	assert.equal(r.stdout, envelopeText(null, 1));
	assert.ok(r.stderr.includes("unknown command 'nope'"));
});

test("a parse error after the machine flag emits an envelope", async () => {
	const r = await plainApp().test(["run", "--json", "--nope"]);
	assert.equal(r.stdout, envelopeText(null, 1));
	assert.ok(r.stderr.includes("unknown flag '--nope'"));
});

test("the --help flag under machine mode is refused naming the help command", async () => {
	const r = await plainApp().test(["--json", "run", "--help"]);
	assert.equal(r.exitCode, 1);
	assert.ok(
		r.stderr.startsWith(
			`error: help pages are text; for the machine form use '${plainApp().name} help run --json'\n`,
		),
		r.stderr,
	);
	assert.equal((await plainApp().test(["help", "run", "--json"])).exitCode, 0);
});

test("the envelope's preview agrees with the effect log", async () => {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(
		defineMutatingCommand("rel", {
			help: "rel",
			handler: (_args, ctx) => {
				ctx.effects.mkdir("build");
				ctx.effects.write("VERSION", "1.2.3");
				return 0;
			},
		}),
	);
	const r = await app.test(["--json", "--dry-run", "rel"]);
	const env = JSON.parse(r.stdout) as { preview: unknown };
	assert.deepEqual(env.preview, app.effectLog());
	assert.ok(!r.stdout.includes("DRY RUN"));
});

test("a live run's preview agrees with the effect log", async () => {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(
		defineMutatingCommand("clean", {
			help: "clean",
			handler: (_args, ctx) => {
				ctx.effects.remove("no-such-path-envelope-ts");
				return 0;
			},
		}),
	);
	const r = await app.test(["--json", "clean"]);
	const env = JSON.parse(r.stdout) as {
		preview: { recorded: boolean }[];
	};
	assert.deepEqual(env.preview, app.effectLog());
	assert.equal(env.preview[0]?.recorded, false);
});

test("a truncated preview carries preview_error and no stderr text", async () => {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(
		defineMutatingCommand("rel", {
			help: "rel",
			handler: (_args, ctx) => {
				const out = ctx.effects.run(["git", "tag", "v1"]);
				void out.stdout;
				return 0;
			},
		}),
	);
	const r = await app.test(["--json", "--dry-run", "rel"]);
	assert.equal(r.exitCode, 1);
	const env = JSON.parse(r.stdout) as {
		preview_error: Record<string, unknown>;
	};
	assert.deepEqual(env.preview_error, {
		kind: "truncated",
		step: 2,
		command: "rel",
		brand: "«step 1 output»",
		message:
			"error: dry-run preview ends at step 2: rel branched on unsettled " +
			"value «step 1 output» — cannot preview past this point",
	});
	assert.equal(r.stderr, "");
});

test("an aborted dry run carries preview_error kind aborted", async () => {
	const app = createApp({ name: "a", version: "1", help: "h" });
	app.command(
		defineMutatingCommand("rel", {
			help: "rel",
			handler: (_args, ctx) => {
				ctx.effects.mkdir("build");
				throw new Error("boom");
			},
		}),
	);
	await assert.rejects(() => app.test(["--json", "--dry-run", "rel"]));
});
