/**
 * Parse-pipeline tests. Expected outputs are derived from the conformance
 * suite (conformance/cases/*.json) -- each test names its source case where
 * one exists. The mini-runner below is the smallest seam over doParse:
 * argv in, exact stdout/stderr/exit out. The full run()/test() surface is
 * covered in app_run.test.ts.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { AppImpl, AppSpec } from "../src/app.js";
import { Context } from "../src/context.js";
import type {
	AllOrNone,
	AnyCommand,
	AnyFlag,
	AtLeastOne,
	ChoiceRecord,
	PassthroughArgs,
	PassthroughDef,
} from "../src/factories.js";
import { formatFloatCanonical } from "../src/float.js";
import {
	allOrNone,
	arg,
	atLeastOne,
	choice,
	choiceFlag,
	createApp,
	defineReadOnlyCommand,
	deprecated,
	flag,
	implies,
	memberChoiceFlag,
	readOnlyPassthrough,
	relativeToRoot,
	requires,
	t,
} from "../src/index.js";
import {
	type ConfigProvider,
	type DoParseDeps,
	doParse,
	flagParamName,
	formatParseErrorOutput,
	type ParseOutcome,
	preScanReservedFlags,
	tokensContainHelp,
} from "../src/parse.js";

// --- Mini-runner seam ---

/** Ctx whose streams go nowhere; mini-runner handlers print via `out`. */
function discardCtx(): Context {
	const discard = { write: () => {} };
	return new Context(discard, discard, {}, null);
}

interface RunResult {
	readonly kind: ParseOutcome["kind"];
	readonly stdout: string;
	readonly stderr: string;
	readonly exitCode: number;
	readonly outcome: ParseOutcome;
}

/**
 * Runs the parse pipeline and, for command/passthrough outcomes, invokes the
 * handler. Handlers print by pushing onto the shared `out` array (joined with
 * newlines, mirroring conformance stdout comparison). Help outcomes are
 * represented structurally only -- help *rendering* is tested elsewhere.
 */
async function run(
	app: AppImpl,
	argv: readonly string[],
	out: readonly string[] = [],
	deps?: DoParseDeps,
): Promise<RunResult> {
	const outcome = doParse(app, argv, deps);
	const done = (
		stdout: string,
		stderr: string,
		exitCode: number,
	): RunResult => ({
		kind: outcome.kind,
		stdout,
		stderr,
		exitCode,
		outcome,
	});
	switch (outcome.kind) {
		case "help":
		case "dump-schema":
		case "lint-framework-use":
		case "mcp":
			return done("", "", 0);
		case "version":
			return done(outcome.text, "", 0);
		case "parse-error":
			return done(
				"",
				formatParseErrorOutput(app, outcome.message, outcome.commandPrefix),
				1,
			);
		case "passthrough": {
			const def = outcome.cmd.def as PassthroughDef<string>;
			const args: PassthroughArgs = {
				name: outcome.cmd.name,
				args: outcome.args,
				globals: outcome.globalKwargs,
			};
			const result = await def.handler(args, discardCtx());
			return done(out.join("\n"), "", typeof result === "number" ? result : 0);
		}
		case "command": {
			const def = outcome.cmd.def as AnyCommand;
			const result = await def.handler(outcome.kwargs as never, discardCtx());
			return done(out.join("\n"), "", typeof result === "number" ? result : 0);
		}
	}
}

/** Conformance handler-prints value formatting (ref_python.py semantics). */
function fmt(v: unknown): string {
	if (v === undefined || v === null) {
		return "None";
	}
	switch (typeof v) {
		case "boolean":
			return v ? "true" : "false";
		case "bigint":
			return v.toString();
		case "number":
			return formatFloatCanonical(v);
		case "string":
			return v;
		default:
			break;
	}
	if (Array.isArray(v)) {
		return v.map(fmt).join(",");
	}
	if (v instanceof Map) {
		return [...v.keys()]
			.sort()
			.map((k) => `${k}=${fmt(v.get(k))}`)
			.join(",");
	}
	return String(v);
}

function makeApp(spec?: Partial<AppSpec>): AppImpl {
	return createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
		...spec,
	}) as AppImpl;
}

/** Expected two-line parse-error stderr surface. */
function errOut(msg: string, prefix = "myapp"): string {
	return `error: ${msg}\ntry '${prefix} --help'\n`;
}

async function withEnv<T>(
	vars: Record<string, string>,
	fn: () => Promise<T>,
): Promise<T> {
	const saved = new Map<string, string | undefined>();
	for (const [k, v] of Object.entries(vars)) {
		saved.set(k, process.env[k]);
		process.env[k] = v;
	}
	try {
		return await fn();
	} finally {
		for (const [k, v] of saved) {
			if (v === undefined) {
				delete process.env[k];
			} else {
				process.env[k] = v;
			}
		}
	}
}

function fakeConfig(data: Record<string, unknown>): ConfigProvider {
	return {
		load: () => ({ data }),
		// Tests supply pre-typed values; config.ts owns real coercion.
		coerce: (_f, v) => v,
		validateFields: () => undefined,
	};
}

// =========================================================================
// flags.json
// =========================================================================

test("flags: str flag with space and equals syntax", async () => {
	for (const argv of [
		["cmd", "--target", "foo"],
		["cmd", "--target=foo"],
	]) {
		const out: string[] = [];
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					target: flag("target", t.str, {
						help: "the target",
						presence: "required",
					}),
				},
				handler: (a) => {
					out.push(`target=${fmt(a.target)}`);
				},
			}),
		);
		const r = await run(app, argv, out);
		assert.equal(r.exitCode, 0);
		assert.equal(r.stdout, "target=foo");
	}
});

function boolApp(out: string[], negatable?: false): AppImpl {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				chatter:
					negatable === false
						? flag("chatter", t.bool, {
								help: "be chatter",
								presence: "default",
								default: false,
								negatable: false,
							})
						: flag("chatter", t.bool, {
								help: "be chatter",
								presence: "default",
								default: false,
							}),
			},
			handler: (a) => {
				out.push(`chatter=${fmt(a.chatter)}`);
			},
		}),
	);
	return app;
}

test("flags: bool present/absent/negation", async () => {
	const cases: readonly [string[], string][] = [
		[["cmd", "--chatter"], "chatter=true"],
		[["cmd"], "chatter=false"],
		[["cmd", "--no-chatter"], "chatter=false"],
	];
	for (const [argv, expected] of cases) {
		const out: string[] = [];
		const r = await run(boolApp(out), argv, out);
		assert.equal(r.exitCode, 0);
		assert.equal(r.stdout, expected);
	}
});

test("flags: short flags for bool and str", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				chatter: flag("chatter", t.bool, {
					help: "be chatter",
					short: "V",
					presence: "default",
					default: false,
				}),
				target: flag("target", t.str, {
					help: "the target",
					short: "t",
					presence: "default",
					default: "none",
				}),
			},
			handler: (a) => {
				out.push(`chatter=${fmt(a.chatter)} target=${fmt(a.target)}`);
			},
		}),
	);
	const r = await run(app, ["cmd", "-V", "-t", "foo"], out);
	assert.equal(r.stdout, "chatter=true target=foo");
});

test("flags: str flag default omitted/provided", async () => {
	for (const [argv, expected] of [
		[["cmd"], "format=text"],
		[["cmd", "--format", "json"], "format=json"],
	] as const) {
		const out: string[] = [];
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					format: flag("format", t.str, {
						help: "output format",
						presence: "default",
						default: "text",
					}),
				},
				handler: (a) => {
					out.push(`format=${fmt(a.format)}`);
				},
			}),
		);
		const r = await run(app, [...argv], out);
		assert.equal(r.stdout, expected);
	}
});

test("flags: required str flag missing produces exact error", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					presence: "required",
				}),
			},
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["cmd"]);
	assert.equal(r.exitCode, 1);
	assert.equal(r.stderr, errOut("flag '--target' is required", "myapp cmd"));
});

test("flags: bool flag=value syntax is rejected", async () => {
	const out: string[] = [];
	const r = await run(boolApp(out), ["cmd", "--chatter=true"], out);
	assert.equal(r.exitCode, 1);
	assert.equal(
		r.stderr,
		errOut(
			"flag '--chatter' is a boolean flag and does not take a value",
			"myapp cmd",
		),
	);
});

test("flags: negatable false rejects --no-flag as unknown", async () => {
	const out: string[] = [];
	const r = await run(boolApp(out, false), ["cmd", "--no-chatter"], out);
	assert.equal(r.exitCode, 1);
	assert.equal(r.stderr, errOut("unknown flag '--no-chatter'", "myapp cmd"));
});

test("flags: required bool must-be-passed messages", async () => {
	// required_bools.json shapes: negatable and non-negatable required bools.
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				watch: flag("watch", t.bool, {
					help: "watch mode",
					presence: "required",
				}),
			},
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["cmd"]);
	assert.equal(
		r.stderr,
		errOut(
			"flag '--watch' must be passed as --watch or --no-watch",
			"myapp cmd",
		),
	);

	const app2 = makeApp();
	app2.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				watch: flag("watch", t.bool, {
					help: "watch mode",
					negatable: false,
					presence: "required",
				}),
			},
			handler: () => undefined,
		}),
	);
	const r2 = await run(app2, ["cmd"]);
	assert.equal(
		r2.stderr,
		errOut("flag '--watch' must be passed as --watch", "myapp cmd"),
	);
});

// =========================================================================
// errors.json
// =========================================================================

test("errors: unknown flag (bare and equals form)", async () => {
	for (const argv of [
		["cmd", "--unknown"],
		["cmd", "--unknown=value"],
	]) {
		const out: string[] = [];
		const r = await run(boolApp(out), argv, out);
		assert.equal(r.exitCode, 1);
		assert.equal(r.stderr, errOut("unknown flag '--unknown'", "myapp cmd"));
	}
});

test("errors: unknown short flag treated as positional", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["cmd", "-x"]);
	assert.equal(r.exitCode, 1);
	assert.equal(r.stderr, errOut("unexpected argument '-x'", "myapp cmd"));
});

test("errors: extra positional arg", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["cmd", "surprise"]);
	assert.equal(r.stderr, errOut("unexpected argument 'surprise'", "myapp cmd"));
});

test("errors: flag requires a value", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					presence: "required",
				}),
			},
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["cmd", "--target"]);
	assert.equal(
		r.stderr,
		errOut("flag '--target' requires a value", "myapp cmd"),
	);
});

test("errors: unknown command uses app-level try hint", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["unknown"]);
	assert.equal(r.exitCode, 1);
	assert.equal(r.stderr, errOut("unknown command 'unknown'"));
});

test("errors: bool negation with value is rejected", async () => {
	const out: string[] = [];
	const r = await run(boolApp(out), ["cmd", "--no-chatter=true"], out);
	assert.equal(
		r.stderr,
		errOut(
			"flag '--no-chatter' is a boolean negation and does not take a value",
			"myapp cmd",
		),
	);
});

test("errors: str flag consumes flag-like next token as its value", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					presence: "required",
				}),
				chatter: flag("chatter", t.bool, {
					help: "be chatter",
					presence: "default",
					default: false,
				}),
			},
			handler: (a) => {
				out.push(`target=${fmt(a.target)} chatter=${fmt(a.chatter)}`);
			},
		}),
	);
	const r1 = await run(app, ["cmd", "--target", "--unknown"], out);
	assert.equal(r1.exitCode, 0);
	assert.equal(r1.stdout, "target=--unknown chatter=false");
	out.length = 0;
	const r2 = await run(app, ["cmd", "--target", "--chatter"], out);
	assert.equal(r2.stdout, "target=--chatter chatter=false");
});

// =========================================================================
// args.json / typed_args.json / variadic.json
// =========================================================================

test("args: single required arg and missing required arg", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("greet", {
			help: "say hello",
			args: [
				arg("name", t.str, { help: "who to greet", presence: "required" }),
			],
			handler: (a) => {
				out.push(`hello ${fmt(a.name)}`);
			},
		}),
	);
	const r = await run(app, ["greet", "world"], out);
	assert.equal(r.stdout, "hello world");
	const r2 = await run(app, ["greet"], out);
	assert.equal(r2.exitCode, 1);
	assert.equal(
		r2.stderr,
		errOut("missing required argument 'name'", "myapp greet"),
	);
});

test("args: two positional args in order", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("copy", {
			help: "copy files",
			args: [
				arg("src", t.str, { help: "source file", presence: "required" }),
				arg("dst", t.str, { help: "destination file", presence: "required" }),
			],
			handler: (a) => {
				out.push(`${fmt(a.src)}->${fmt(a.dst)}`);
			},
		}),
	);
	const r = await run(app, ["copy", "a.txt", "b.txt"], out);
	assert.equal(r.stdout, "a.txt->b.txt");
});

test("args: optional arg with default, provided and omitted", async () => {
	for (const [argv, expected] of [
		[["cmd", "/tmp/foo"], "path=/tmp/foo"],
		[["cmd"], "path=."],
	] as const) {
		const out: string[] = [];
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				args: [
					arg("path", t.str, {
						help: "project dir",
						presence: "default",
						default: ".",
					}),
				],
				handler: (a) => {
					out.push(`path=${fmt(a.path)}`);
				},
			}),
		);
		const r = await run(app, [...argv], out);
		assert.equal(r.stdout, expected);
	}
});

test("args: required first, optional second with default", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [
				arg("src", t.str, { help: "source file", presence: "required" }),
				arg("dst", t.str, {
					help: "destination",
					presence: "default",
					default: "out",
				}),
			],
			handler: (a) => {
				out.push(`${fmt(a.src)}:${fmt(a.dst)}`);
			},
		}),
	);
	const r = await run(app, ["cmd", "input.txt"], out);
	assert.equal(r.stdout, "input.txt:out");
});

test("args: optional arg without default, omitted gives None", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [arg("path", t.str, { help: "project dir", presence: "optional" })],
			handler: (a) => {
				out.push(`path=${fmt(a.path)}`);
			},
		}),
	);
	const r = await run(app, ["cmd"], out);
	assert.equal(r.stdout, "path=None");
});

test("args: double dash stops flag parsing", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				chatter: flag("chatter", t.bool, {
					help: "be chatter",
					presence: "default",
					default: false,
				}),
			},
			args: [arg("path", t.str, { help: "a path", presence: "required" })],
			handler: (a) => {
				out.push(`chatter=${fmt(a.chatter)} path=${fmt(a.path)}`);
			},
		}),
	);
	const r = await run(app, ["cmd", "--", "--not-a-flag"], out);
	assert.equal(r.stdout, "chatter=false path=--not-a-flag");
});

test("typed args: int/float/bool coercion and exact errors", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [arg("count", t.int, { help: "how many", presence: "required" })],
			handler: (a) => {
				out.push(`count=${fmt(a.count)}`);
			},
		}),
	);
	const r = await run(app, ["cmd", "42"], out);
	assert.equal(r.stdout, "count=42");
	const r2 = await run(app, ["cmd", "abc"], out);
	assert.equal(
		r2.stderr,
		errOut("argument 'count': expected integer, got 'abc'", "myapp cmd"),
	);

	const out3: string[] = [];
	const app3 = makeApp();
	app3.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [arg("flag", t.bool, { help: "a bool", presence: "required" })],
			handler: (a) => {
				out3.push(`flag=${fmt(a.flag)}`);
			},
		}),
	);
	assert.equal((await run(app3, ["cmd", "true"], out3)).stdout, "flag=true");
	const r4 = await run(app3, ["cmd", "maybe"], out3);
	assert.equal(
		r4.stderr,
		errOut("argument 'flag': expected boolean, got 'maybe'", "myapp cmd"),
	);

	const out5: string[] = [];
	const app5 = makeApp();
	app5.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [arg("rate", t.float, { help: "the rate", presence: "required" })],
			handler: (a) => {
				out5.push(`rate=${fmt(a.rate)}`);
			},
		}),
	);
	assert.equal((await run(app5, ["cmd", "3.14"], out5)).stdout, "rate=3.14");
	const r6 = await run(app5, ["cmd", "xyz"], out5);
	assert.equal(
		r6.stderr,
		errOut("argument 'rate': expected float, got 'xyz'", "myapp cmd"),
	);
});

function variadicApp(out: string[], required: boolean): AppImpl {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [
				required
					? arg("files", t.str, {
							help: "files to process",
							variadic: true,
							presence: "required",
						})
					: arg("files", t.str, {
							help: "files to process",
							variadic: true,
							presence: "optional",
						}),
			],
			handler: (a) => {
				out.push(`files=${fmt(a.files)}`);
			},
		}),
	);
	return app;
}

test("variadic: collects values, required/optional zero-value behavior", async () => {
	const out: string[] = [];
	assert.equal(
		(await run(variadicApp(out, true), ["cmd", "a", "b", "c"], out)).stdout,
		"files=a,b,c",
	);
	const r = await run(variadicApp([], true), ["cmd"]);
	assert.equal(
		r.stderr,
		errOut("missing required argument 'files'", "myapp cmd"),
	);
	const out2: string[] = [];
	assert.equal(
		(await run(variadicApp(out2, false), ["cmd"], out2)).stdout,
		"files=",
	);
	const out3: string[] = [];
	assert.equal(
		(await run(variadicApp(out3, true), ["cmd", "--", "-a", "-b"], out3))
			.stdout,
		"files=-a,-b",
	);
});

test("variadic: with preceding required arg", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [
				arg("target", t.str, { help: "the target", presence: "required" }),
				arg("files", t.str, {
					help: "files",
					variadic: true,
					presence: "required",
				}),
			],
			handler: (a) => {
				out.push(`target=${fmt(a.target)} files=${fmt(a.files)}`);
			},
		}),
	);
	const r = await run(app, ["cmd", "target", "f1", "f2"], out);
	assert.equal(r.stdout, "target=target files=f1,f2");
});

test("typed args: negative int after double dash", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [
				arg("offset", t.int, { help: "the offset", presence: "required" }),
			],
			handler: (a) => {
				out.push(`offset=${fmt(a.offset)}`);
			},
		}),
	);
	const r = await run(app, ["cmd", "--", "-5"], out);
	assert.equal(r.stdout, "offset=-5");
});

// =========================================================================
// nesting.json (routing through groups)
// =========================================================================

function nestedApp(out: string[]): AppImpl {
	const app = makeApp();
	const dns = app.group("dns", { help: "manage DNS" });
	const zone = dns.group("zone", { help: "manage zones" });
	zone.command(
		defineReadOnlyCommand("list", {
			help: "list all zones",
			handler: () => {
				out.push("listing zones");
			},
		}),
	);
	zone.command(
		defineReadOnlyCommand("create", {
			help: "create a zone",
			flags: {
				name: flag("name", t.str, { help: "zone name", presence: "required" }),
			},
			handler: (a) => {
				out.push(`created ${fmt(a.name)}`);
			},
		}),
	);
	return app;
}

test("nesting: group command dispatch and flags", async () => {
	const out: string[] = [];
	const app = makeApp();
	const config = app.group("config", { help: "manage configuration" });
	config.command(
		defineReadOnlyCommand("set", {
			help: "set a config value",
			flags: {
				key: flag("key", t.str, { help: "config key", presence: "required" }),
				value: flag("value", t.str, {
					help: "config value",
					presence: "required",
				}),
			},
			handler: (a) => {
				out.push(`${fmt(a.key)}=${fmt(a.value)}`);
			},
		}),
	);
	const r = await run(
		app,
		["config", "set", "--key", "name", "--value", "strictcli"],
		out,
	);
	assert.equal(r.stdout, "name=strictcli");
});

test("nesting: unknown group subcommand error includes path", async () => {
	const app = makeApp();
	const config = app.group("config", { help: "manage configuration" });
	config.command(
		defineReadOnlyCommand("show", {
			help: "display config",
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["config", "delete"]);
	assert.equal(r.exitCode, 1);
	assert.equal(
		r.stderr,
		errOut("unknown command 'delete' in 'config'", "myapp config"),
	);
});

test("nesting: 3-level dispatch, flags, and unknown command", async () => {
	const out: string[] = [];
	const app = nestedApp(out);
	assert.equal(
		(await run(app, ["dns", "zone", "list"], out)).stdout,
		"listing zones",
	);
	out.length = 0;
	assert.equal(
		(await run(app, ["dns", "zone", "create", "--name", "example.com"], out))
			.stdout,
		"created example.com",
	);
	const r = await run(app, ["dns", "zone", "delete"]);
	assert.equal(
		r.stderr,
		errOut("unknown command 'delete' in 'dns zone'", "myapp dns zone"),
	);
});

test("nesting: 4-level dispatch", async () => {
	const out: string[] = [];
	const app = makeApp();
	app
		.group("cloud", { help: "cloud operations" })
		.group("compute", { help: "compute resources" })
		.group("instance", { help: "manage instances" })
		.command(
			defineReadOnlyCommand("list", {
				help: "list instances",
				handler: () => {
					out.push("listing instances");
				},
			}),
		);
	const r = await run(app, ["cloud", "compute", "instance", "list"], out);
	assert.equal(r.stdout, "listing instances");
});

test("nesting: group with no subcommand or --help yields group help outcome", async () => {
	const app = nestedApp([]);
	for (const argv of [
		["dns", "zone"],
		["dns", "zone", "--help"],
		["dns", "zone", "-h"],
	]) {
		const r = await run(app, argv);
		assert.equal(r.kind, "help");
		assert.equal(r.exitCode, 0);
		const outcome = r.outcome as Extract<ParseOutcome, { kind: "help" }>;
		assert.equal(outcome.target.level, "group");
		if (outcome.target.level === "group") {
			assert.equal(outcome.target.group.name, "zone");
			assert.deepEqual(outcome.target.path, ["dns", "zone"]);
		}
	}
});

test("nesting: mixed groups and commands at same level", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("version", {
			help: "show version",
			handler: () => {
				out.push("1.0.0");
			},
		}),
	);
	const config = app.group("config", { help: "manage configuration" });
	config.command(
		defineReadOnlyCommand("show", {
			help: "display config",
			handler: () => {
				out.push("showing config");
			},
		}),
	);
	const remote = config.group("remote", { help: "manage remotes" });
	remote.command(
		defineReadOnlyCommand("list", {
			help: "list remotes",
			handler: () => {
				out.push("listing remotes");
			},
		}),
	);
	const r = await run(app, ["config", "remote", "list"], out);
	assert.equal(r.stdout, "listing remotes");
});

// =========================================================================
// global_flags.json
// =========================================================================

function globalApp(out: string[]): AppImpl {
	const app = makeApp({
		flags: {
			chatter: flag("chatter", t.bool, {
				help: "enable chatter output",
				presence: "default",
				default: false,
			}),
			settings: flag("settings", t.str, {
				help: "settings path",
				presence: "default",
				default: "/etc/myapp",
			}),
		},
	});
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: (_a, _ctx) => {
				out.push("ran");
			},
		}),
	);
	return app;
}

test("global_flags: bool before and after the command name", async () => {
	for (const argv of [
		["--chatter", "cmd"],
		["cmd", "--chatter"],
	]) {
		const out: string[] = [];
		const app = globalApp(out);
		const r = await run(app, argv, out);
		assert.equal(r.exitCode, 0);
		const o = r.outcome as Extract<ParseOutcome, { kind: "command" }>;
		assert.equal(o.kwargs.chatter, true);
		assert.equal(o.globalKwargs.chatter, true);
	}
});

test("global_flags: negation, str value, defaults", async () => {
	const out: string[] = [];
	const app = globalApp(out);
	const r = await run(app, ["--no-chatter", "cmd"], out);
	const o = r.outcome as Extract<ParseOutcome, { kind: "command" }>;
	assert.equal(o.kwargs.chatter, false);

	const r2 = await run(globalApp([]), ["--settings", "/tmp", "cmd"]);
	const o2 = r2.outcome as Extract<ParseOutcome, { kind: "command" }>;
	assert.equal(o2.kwargs.settings, "/tmp");

	const r3 = await run(globalApp([]), ["cmd"]);
	const o3 = r3.outcome as Extract<ParseOutcome, { kind: "command" }>;
	assert.equal(o3.kwargs.chatter, false);
	assert.equal(o3.kwargs.settings, "/etc/myapp");
});

test("global_flags: global int flag coerces to bigint", async () => {
	const out: string[] = [];
	const app = makeApp({
		flags: {
			port: flag("port", t.int, {
				help: "server port",
				presence: "default",
				default: 3000n,
			}),
		},
	});
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: (a) => {
				out.push(`port=${fmt((a as { port: bigint }).port)}`);
			},
		}),
	);
	const r = await run(app, ["--port", "8080", "cmd"], out);
	assert.equal(r.stdout, "port=8080");
});

test("global_flags: global flag from env var", async () => {
	await withEnv({ MYAPP_VERBOSE: "true" }, async () => {
		const app = makeApp({
			envPrefix: "MYAPP",
			flags: {
				chatter: flag("chatter", t.bool, {
					help: "enable chatter output",
					env: "MYAPP_VERBOSE",
					presence: "default",
					default: false,
				}),
			},
		});
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				handler: () => undefined,
			}),
		);
		const r = await run(app, ["cmd"]);
		const o = r.outcome as Extract<ParseOutcome, { kind: "command" }>;
		assert.equal(o.kwargs.chatter, true);
		assert.equal(o.sources.chatter, "env");
	});
});

test("global_flags: global and command flags together (conformance exact)", async () => {
	const out: string[] = [];
	const app = makeApp({
		flags: {
			chatter: flag("chatter", t.bool, {
				help: "enable chatter output",
				presence: "default",
				default: false,
			}),
			settings: flag("settings", t.str, {
				help: "settings path",
				presence: "default",
				default: "/etc/myapp",
			}),
		},
	});
	app.command(
		defineReadOnlyCommand("deploy", {
			help: "deploy the app",
			flags: {
				force_deploy: flag("force-deploy", t.bool, {
					help: "force deploy",
					presence: "default",
					default: false,
				}),
			},
			handler: (a) => {
				const g = a as unknown as {
					chatter: boolean;
					settings: string;
					force_deploy: boolean;
				};
				out.push(
					`chatter=${fmt(g.chatter)} settings=${fmt(g.settings)} force-deploy=${fmt(g.force_deploy)}`,
				);
			},
		}),
	);
	const r = await run(
		app,
		["--chatter", "--settings", "/tmp/cfg", "deploy", "--force-deploy"],
		out,
	);
	assert.equal(r.stdout, "chatter=true settings=/tmp/cfg force-deploy=true");
});

// =========================================================================
// mutex.json / cross_feature.json
// =========================================================================

function memberBoolApp(out: string[]): AppImpl {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				volume: memberChoiceFlag(
					"volume",
					{
						chatter: choice({ help: "chatter output" }),
						muted: choice({ help: "muted output" }),
					},
					{ help: "output volume", presence: "required" },
				),
			},
			handler: (a) => {
				out.push(`volume=${a.volume.choice}`);
			},
		}),
	);
	return app;
}

test("member spelling: nothing elected is exact one-of-required error", async () => {
	const r = await run(memberBoolApp([]), ["cmd"]);
	assert.equal(r.exitCode, 1);
	assert.equal(
		r.stderr,
		errOut("one of --chatter, --muted is required", "myapp cmd"),
	);
});

test("member spelling: one elected delivers one tagged record", async () => {
	const out: string[] = [];
	assert.equal(
		(await run(memberBoolApp(out), ["cmd", "--chatter"], out)).stdout,
		"volume=chatter",
	);
	const out2: string[] = [];
	assert.equal(
		(await run(memberBoolApp(out2), ["cmd", "--muted"], out2)).stdout,
		"volume=muted",
	);
});

test("member spelling: both elected is exact mutually-exclusive error", async () => {
	const r = await run(memberBoolApp([]), ["cmd", "--chatter", "--muted"]);
	assert.equal(
		r.stderr,
		errOut("--chatter and --muted are mutually exclusive", "myapp cmd"),
	);
});

function memberPayloadApp(out: string[]): AppImpl {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("fetch", {
			help: "fetch data",
			flags: {
				source: memberChoiceFlag(
					"source",
					{
						file: choice({
							help: "read from file",
							value: { carrier: t.str, help: "path to the file" },
						}),
						url: choice({
							help: "read from URL",
							value: { carrier: t.str, help: "the URL to read" },
						}),
					},
					{ help: "where to read from", presence: "required" },
				),
			},
			handler: (a) => {
				out.push(`source=${a.source.choice} value=${a.source.value}`);
			},
		}),
	);
	return app;
}

test("member spelling: a member carries its payload in the alternative that owns it", async () => {
	const out: string[] = [];
	assert.equal(
		(await run(memberPayloadApp(out), ["fetch", "--file", "data.txt"], out))
			.stdout,
		"source=file value=data.txt",
	);
	const r = await run(memberPayloadApp([]), [
		"fetch",
		"--file",
		"data.txt",
		"--url",
		"http://example.com",
	]);
	assert.equal(
		r.stderr,
		errOut("--file and --url are mutually exclusive", "myapp fetch"),
	);
});

test("member spelling: env elects nothing (contract §21.3, carried over)", async () => {
	const mk = (out: string[]): AppImpl => {
		const app = makeApp({ envPrefix: "MYAPP" });
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					source: memberChoiceFlag(
						"source",
						{
							file: choice({
								help: "read from file",
								value: { carrier: t.str, help: "path to the file" },
							}),
							url: choice({
								help: "read from URL",
								value: { carrier: t.str, help: "the URL to read" },
							}),
						},
						{ help: "where to read from", presence: "required" },
					),
				},
				handler: (a) => {
					out.push(`source=${a.source.choice} value=${a.source.value}`);
				},
			}),
		);
		return app;
	};
	// Env is not consulted for a member flag at all: the spelling exists to
	// make the operator choose IN the invocation.
	await withEnv({ MYAPP_FILE: "data.txt" }, async () => {
		const r = await run(mk([]), ["cmd"]);
		assert.equal(
			r.stderr,
			errOut("one of --file, --url is required", "myapp cmd"),
		);
	});
	await withEnv(
		{ MYAPP_FILE: "data.txt", MYAPP_URL: "http://example.com" },
		async () => {
			const r = await run(mk([]), ["cmd"]);
			assert.equal(
				r.stderr,
				errOut("one of --file, --url is required", "myapp cmd"),
			);
		},
	);
	await withEnv({ MYAPP_FILE: "data.txt" }, async () => {
		const out: string[] = [];
		assert.equal(
			(await run(mk(out), ["cmd", "--url", "http://example.com"], out)).stdout,
			"source=url value=http://example.com",
		);
	});
});

// =========================================================================
// Election semantics (effects contract §21, carried over to member spelling)
// =========================================================================

function electionApp(out: string[]): AppImpl {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("run", {
			help: "run it",
			flags: {
				scope: memberChoiceFlag(
					"scope",
					{
						profile: choice({
							help: "a profile",
							value: { carrier: t.str, help: "profile name" },
						}),
						"all-profiles": choice({ help: "every profile" }),
						"current-profile": choice({ help: "the current profile" }),
					},
					{ help: "which profiles", presence: "required" },
				),
			},
			handler: (a) => {
				const elected = a.scope;
				out.push(
					`scope=${elected.choice}` +
						("value" in elected ? ` value=${elected.value}` : ""),
				);
			},
		}),
	);
	return app;
}

test("election A1: a negated bool member elects nothing", async () => {
	const r = await run(electionApp([]), ["run", "--no-all-profiles"]);
	assert.equal(r.exitCode, 1);
	assert.equal(
		r.stderr,
		errOut(
			"one of --profile, --all-profiles, --current-profile is required " +
				"(--no-all-profiles declines an option; it does not choose one)",
			"myapp run",
		),
	);
});

test("election A1: a true bool member still elects", async () => {
	const out: string[] = [];
	assert.equal(
		(await run(electionApp(out), ["run", "--all-profiles"], out)).stdout,
		"scope=all-profiles",
	);
});

test("election A1: every member declined is an unsatisfied selector", async () => {
	const r = await run(electionApp([]), [
		"run",
		"--no-current-profile",
		"--no-all-profiles",
	]);
	assert.equal(
		r.stderr,
		errOut(
			"one of --profile, --all-profiles, --current-profile is required " +
				"(--no-all-profiles declines an option; it does not choose one)",
			"myapp run",
		),
	);
});

test("election A2: a string member elects on the empty string", async () => {
	const out: string[] = [];
	assert.equal(
		(await run(electionApp(out), ["run", "--profile", ""], out)).stdout,
		"scope=profile value=",
	);
});

test("election A3: no clause when nothing was declined", async () => {
	const r = await run(electionApp([]), ["run"]);
	assert.equal(
		r.stderr,
		errOut(
			"one of --profile, --all-profiles, --current-profile is required",
			"myapp run",
		),
	);
});

test("election A4: a redundant negation beside an election is an error", async () => {
	const r = await run(electionApp([]), [
		"run",
		"--profile",
		"work",
		"--no-all-profiles",
	]);
	assert.equal(
		r.stderr,
		errOut(
			"--no-all-profiles cannot be combined with --profile " +
				"(--no-all-profiles declines an option; it does not choose one)",
			"myapp run",
		),
	);
});

test("election A4: every declined member is named, in declaration order", async () => {
	const r = await run(electionApp([]), [
		"run",
		"--profile",
		"work",
		"--no-current-profile",
		"--no-all-profiles",
	]);
	assert.equal(
		r.stderr,
		errOut(
			"--no-all-profiles and --no-current-profile cannot be combined " +
				"with --profile " +
				"(--no-all-profiles declines an option; it does not choose one)",
			"myapp run",
		),
	);
});

test("election: two elections are still mutually exclusive", async () => {
	const r = await run(electionApp([]), [
		"run",
		"--profile",
		"work",
		"--all-profiles",
	]);
	assert.equal(
		r.stderr,
		errOut("--profile and --all-profiles are mutually exclusive", "myapp run"),
	);
});

test("election: a member-spelled selector may default to a payload-less member", async () => {
	// §21.3's "an unelected member delivers its declared default" RETIRES: an
	// unelected choice's scope is not delivered at all. What a member-spelled
	// selector may carry is a default ELECTION (§24.5).
	const mk = (out: string[]): AppImpl => {
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					volume: memberChoiceFlag(
						"volume",
						{
							loud: choice({ help: "loud" }),
							hushed: choice({ help: "hushed" }),
						},
						{ help: "output volume", presence: "default", default: "hushed" },
					),
				},
				handler: (a) => {
					out.push(`volume=${a.volume.choice}`);
				},
			}),
		);
		return app;
	};
	const out: string[] = [];
	assert.equal((await run(mk(out), ["cmd"], out)).stdout, "volume=hushed");
	const out2: string[] = [];
	assert.equal(
		(await run(mk(out2), ["cmd", "--loud"], out2)).stdout,
		"volume=loud",
	);
});

// =========================================================================
// constraints.json
// =========================================================================

function allOrNoneApp(out: string[]): AppImpl {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				output: flag("output", t.str, {
					help: "output file",
					presence: "default",
					default: "none",
				}),
				format: flag("format", t.str, {
					help: "output format",
					presence: "default",
					default: "none",
				}),
			},
			constraints: [
				allOrNone({
					name: "report",
					members: [{ name: "output" }, { name: "format" }],
				}),
			],
			handler: (a) => {
				out.push(`output=${fmt(a.output)} format=${fmt(a.format)}`);
			},
		}),
	);
	return app;
}

test("constraints: all-or-none both/neither ok, one is exact error", async () => {
	const out: string[] = [];
	assert.equal(
		(
			await run(
				allOrNoneApp(out),
				["cmd", "--output", "file.txt", "--format", "json"],
				out,
			)
		).stdout,
		"output=file.txt format=json",
	);
	assert.equal((await run(allOrNoneApp([]), ["cmd"])).exitCode, 0);
	for (const argv of [
		["cmd", "--output", "file.txt"],
		["cmd", "--format", "json"],
	]) {
		const r = await run(allOrNoneApp([]), argv);
		assert.equal(
			r.stderr,
			errOut(
				'constraint "report": --output, --format must be used together',
				"myapp cmd",
			),
		);
	}
});

/**
 * §23.6's one predicate: an `infra` source is a RelativeToRoot default
 * resolved through a declared root -- still the declaration deciding, so it
 * never counts as supplied, and an all-or-none constraint holding such a flag
 * sees ZERO engaged members when nothing is typed.
 *
 * Two independent things produce that answer today: the predicate excludes
 * `infra`, and the dependency step runs BEFORE defaults are applied, so the
 * infra value is not even in the store yet. This test pins the composed rule
 * (§23.5's co-required row, now §26.1's all-or-none) so neither can drift
 * alone; the predicate's own
 * discriminating case is the validate step, which runs after defaults.
 */
function infraAllOrNoneApp(out: string[]): AppImpl {
	const app = makeApp({ infraRoot: { MYAPP_HOME: "/var/lib/myapp" } });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				db: flag("db", t.str, {
					help: "database path",
					presence: "default",
					default: relativeToRoot("MYAPP_HOME", "db.sqlite"),
				}),
				user: flag("user", t.str, {
					help: "database user",
					presence: "optional",
				}),
			},
			constraints: [
				allOrNone({
					name: "database",
					members: [{ name: "db" }, { name: "user" }],
				}),
			],
			handler: (a) => {
				out.push(`db=${fmt(a.db)} user=${fmt(a.user)}`);
			},
		}),
	);
	return app;
}

test("constraints: an infra default does not count as present for all-or-none", async () => {
	const out: string[] = [];
	const r = await run(infraAllOrNoneApp(out), ["cmd"], out);
	assert.equal(r.exitCode, 0);
	assert.equal(r.stderr, "");
	assert.equal(r.stdout, "db=/var/lib/myapp/db.sqlite user=None");
	// The store did resolve the default through the declared root -- it is a
	// present VALUE that is simply not a supplied one.
	assert.equal(
		r.outcome.kind === "command" ? r.outcome.sources.db : undefined,
		"infra",
	);

	// The other side of the same predicate: typing one member alone is still
	// a violation, because the infra-defaulted member is not provided.
	const solo = await run(infraAllOrNoneApp([]), ["cmd", "--user", "admin"]);
	assert.equal(
		solo.stderr,
		errOut(
			'constraint "database": --db, --user must be used together',
			"myapp cmd",
		),
	);
	// Both typed satisfies it.
	const both: string[] = [];
	const ok = await run(
		infraAllOrNoneApp(both),
		["cmd", "--db", "/tmp/x.sqlite", "--user", "admin"],
		both,
	);
	assert.equal(ok.stdout, "db=/tmp/x.sqlite user=admin");
});

test("dependencies: requires enforcement", async () => {
	const mk = (_out: string[]): AppImpl => {
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					chatter: flag("chatter", t.bool, {
						help: "chatter output",
						presence: "default",
						default: false,
					}),
					output: flag("output", t.str, {
						help: "output file",
						presence: "default",
						default: "none",
					}),
				},
				constraints: [
					requires({
						name: "chatter-target",
						flag: "chatter",
						dependsOn: "output",
					}),
				],
				handler: () => undefined,
			}),
		);
		return app;
	};
	assert.equal(
		(await run(mk([]), ["cmd", "--chatter", "--output", "log.txt"])).exitCode,
		0,
	);
	assert.equal((await run(mk([]), ["cmd"])).exitCode, 0);
	assert.equal((await run(mk([]), ["cmd", "--output", "log.txt"])).exitCode, 0);
	const r = await run(mk([]), ["cmd", "--chatter"]);
	assert.equal(
		r.stderr,
		errOut(
			"constraint \"chatter-target\": flag '--chatter' requires '--output'",
			"myapp cmd",
		),
	);
});

function impliesApp(out: string[], envPrefix?: string): AppImpl {
	const app = makeApp(envPrefix !== undefined ? { envPrefix } : {});
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				fast:
					envPrefix !== undefined
						? flag("fast", t.bool, {
								help: "fast mode",
								env: "MYAPP_FAST",
								presence: "default",
								default: false,
							})
						: flag("fast", t.bool, {
								help: "fast mode",
								presence: "default",
								default: false,
							}),
				embeddings: flag("embeddings", t.bool, {
					help: "enable embeddings",
					presence: "default",
					default: true,
				}),
			},
			constraints: [
				implies({
					name: "fast-path",
					flag: "fast",
					implies: "embeddings",
					value: false,
				}),
			],
			handler: (a) => {
				out.push(`fast=${fmt(a.fast)} embeddings=${fmt(a.embeddings)}`);
			},
		}),
	);
	return app;
}

test("dependencies: implies auto-set, default, conflict, agreement", async () => {
	const out: string[] = [];
	assert.equal(
		(await run(impliesApp(out), ["cmd", "--fast"], out)).stdout,
		"fast=true embeddings=false",
	);
	const out2: string[] = [];
	assert.equal(
		(await run(impliesApp(out2), ["cmd"], out2)).stdout,
		"fast=false embeddings=true",
	);
	const r = await run(impliesApp([]), ["cmd", "--fast", "--embeddings"]);
	assert.equal(
		r.stderr,
		errOut(
			"constraint \"fast-path\": flag '--fast' implies '--no-embeddings', but '--embeddings' was explicitly provided",
			"myapp cmd",
		),
	);
	const out3: string[] = [];
	assert.equal(
		(await run(impliesApp(out3), ["cmd", "--fast", "--no-embeddings"], out3))
			.stdout,
		"fast=true embeddings=false",
	);
});

// An Implies TRIGGER never fires from its own default (§23.5's Implies-trigger
// row). The trigger declares `default: true` and nothing supplies it, so the
// implied target keeps its own declaration; supplying the same value on the
// command line does fire it, which is what makes provision the distinguisher.
function impliesDefaultedTriggerApp(out: string[]): AppImpl {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				release: flag("release", t.bool, {
					help: "release build",
					presence: "default",
					default: true,
				}),
				signed: flag("signed", t.bool, {
					help: "signed build",
					presence: "optional",
				}),
			},
			constraints: [
				implies({
					name: "release-signing",
					flag: "release",
					implies: "signed",
					value: true,
				}),
			],
			handler: (a) => {
				out.push(`release=${fmt(a.release)} signed=${fmt(a.signed)}`);
			},
		}),
	);
	return app;
}

test("dependencies: a defaulted implies trigger does not fire", async () => {
	const out: string[] = [];
	assert.equal(
		(await run(impliesDefaultedTriggerApp(out), ["cmd"], out)).stdout,
		"release=true signed=None",
	);
	const out2: string[] = [];
	assert.equal(
		(await run(impliesDefaultedTriggerApp(out2), ["cmd", "--release"], out2))
			.stdout,
		"release=true signed=true",
	);
});

test("dependencies: implies env var trigger fires implication", async () => {
	await withEnv({ MYAPP_FAST: "true" }, async () => {
		const out: string[] = [];
		const r = await run(impliesApp(out, "MYAPP"), ["cmd"], out);
		assert.equal(r.stdout, "fast=true embeddings=false");
		const o = r.outcome as Extract<ParseOutcome, { kind: "command" }>;
		assert.equal(o.sources.embeddings, "implied");
	});
});

// =========================================================================
// deprecated.json
// =========================================================================

test("deprecated: invoking a deprecated command prints message and exits 1", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("new-cmd", {
			help: "the replacement command",
			handler: () => {
				out.push("new");
			},
		}),
	);
	app.deprecate(deprecated("old-cmd", "use 'new-cmd' instead"));
	const r = await run(app, ["old-cmd"]);
	assert.equal(r.exitCode, 1);
	assert.equal(
		r.stderr,
		errOut("command 'old-cmd' is deprecated: use 'new-cmd' instead"),
	);
});

// =========================================================================
// basic.json / boundary.json (app-level help, version, edge tokens)
// =========================================================================

test("basic: --version and -v produce 'name version'", async () => {
	const app = makeApp({ version: "2.5.0" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: () => undefined,
		}),
	);
	for (const argv of [["--version"], ["-v"]]) {
		const r = await run(app, argv);
		assert.equal(r.kind, "version");
		assert.equal(r.stdout, "myapp 2.5.0");
		assert.equal(r.exitCode, 0);
	}
});

test("basic: empty argv, --help, -h yield app help outcome", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("greet", {
			help: "say hello",
			handler: () => undefined,
		}),
	);
	for (const argv of [[], ["--help"], ["-h"]]) {
		const r = await run(app, argv);
		assert.equal(r.kind, "help");
		const o = r.outcome as Extract<ParseOutcome, { kind: "help" }>;
		assert.equal(o.target.level, "app");
	}
});

test("boundary: only double dash shows app help", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("greet", {
			help: "say hello",
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["--"]);
	assert.equal(r.kind, "help");
});

test("boundary: unknown flag with no command is an unknown command", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("greet", {
			help: "say hello",
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["--unknown-flag"]);
	assert.equal(r.stderr, errOut("unknown command '--unknown-flag'"));
});

test("boundary: empty flag value, dash positional, bare cmd --", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				name: flag("name", t.str, { help: "the name", presence: "required" }),
			},
			handler: (a) => {
				out.push(`name=${fmt(a.name)}`);
			},
		}),
	);
	assert.equal((await run(app, ["cmd", "--name", ""], out)).stdout, "name=");

	const out2: string[] = [];
	const app2 = makeApp();
	app2.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [arg("path", t.str, { help: "the path", presence: "required" })],
			handler: (a) => {
				out2.push(`path=${fmt(a.path)}`);
			},
		}),
	);
	assert.equal((await run(app2, ["cmd", "-"], out2)).stdout, "path=-");

	const out3: string[] = [];
	const app3 = makeApp();
	app3.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: () => {
				out3.push("ok");
			},
		}),
	);
	assert.equal((await run(app3, ["cmd", "--"], out3)).stdout, "ok");
});

test("boundary: int forms 007, +5, overflow, 12abc", async () => {
	const mk = (out: string[]): AppImpl => {
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					port: flag("port", t.int, { help: "the port", presence: "required" }),
				},
				handler: (a) => {
					out.push(`port=${fmt(a.port)}`);
				},
			}),
		);
		return app;
	};
	const out: string[] = [];
	assert.equal(
		(await run(mk(out), ["cmd", "--port", "007"], out)).stdout,
		"port=7",
	);
	const out2: string[] = [];
	assert.equal(
		(await run(mk(out2), ["cmd", "--port", "+5"], out2)).stdout,
		"port=5",
	);
	assert.equal(
		(await run(mk([]), ["cmd", "--port", "99999999999999999999"])).stderr,
		errOut("--port: expected integer, got '99999999999999999999'", "myapp cmd"),
	);
	assert.equal(
		(await run(mk([]), ["cmd", "--port", "12abc"])).stderr,
		errOut("--port: expected integer, got '12abc'", "myapp cmd"),
	);
});

test("boundary: env var edge values", async () => {
	const mkBool = (): AppImpl => {
		const app = makeApp({ envPrefix: "MYAPP" });
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					chatter: flag("chatter", t.bool, {
						help: "be chatter",
						env: "MYAPP_VERBOSE",
						presence: "default",
						default: false,
					}),
				},
				handler: () => undefined,
			}),
		);
		return app;
	};
	await withEnv({ MYAPP_VERBOSE: "maybe" }, async () => {
		assert.equal(
			(await run(mkBool(), ["cmd"])).stderr,
			errOut(
				"invalid boolean value 'maybe' for env var 'MYAPP_VERBOSE' (flag '--chatter')",
				"myapp cmd",
			),
		);
	});
	await withEnv({ MYAPP_VERBOSE: "TRUE" }, async () => {
		const r = await run(mkBool(), ["cmd"]);
		const o = r.outcome as Extract<ParseOutcome, { kind: "command" }>;
		assert.equal(o.kwargs.chatter, true);
	});

	const mkInt = (): AppImpl => {
		const app = makeApp({ envPrefix: "MYAPP" });
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					port: flag("port", t.int, {
						help: "the port",
						env: "MYAPP_PORT",
						presence: "default",
						default: 1n,
					}),
				},
				handler: () => undefined,
			}),
		);
		return app;
	};
	await withEnv({ MYAPP_PORT: " 42 " }, async () => {
		assert.equal(
			(await run(mkInt(), ["cmd"])).stderr,
			errOut(
				"--port: expected integer, got ' 42 ' (from env var 'MYAPP_PORT')",
				"myapp cmd",
			),
		);
	});

	// env value that looks like a flag stays a plain value
	const out: string[] = [];
	const app = makeApp({ envPrefix: "MYAPP" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					env: "MYAPP_TARGET",
					presence: "default",
					default: "x",
				}),
			},
			handler: (a) => {
				out.push(`target=${fmt(a.target)}`);
			},
		}),
	);
	await withEnv({ MYAPP_TARGET: "--foo" }, async () => {
		assert.equal((await run(app, ["cmd"], out)).stdout, "target=--foo");
	});
});

// =========================================================================
// env.json (precedence CLI > env > default)
// =========================================================================

test("env: str flag from env var; CLI overrides env", async () => {
	const mk = (out: string[]): AppImpl => {
		const app = makeApp({ envPrefix: "MYAPP" });
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					target: flag("target", t.str, {
						help: "the target",
						env: "MYAPP_TARGET",
						presence: "default",
						default: "default-target",
					}),
				},
				handler: (a) => {
					out.push(`target=${fmt(a.target)}`);
				},
			}),
		);
		return app;
	};
	await withEnv({ MYAPP_TARGET: "from-env" }, async () => {
		const out: string[] = [];
		assert.equal((await run(mk(out), ["cmd"], out)).stdout, "target=from-env");
		const out2: string[] = [];
		assert.equal(
			(await run(mk(out2), ["cmd", "--target", "from-cli"], out2)).stdout,
			"target=from-cli",
		);
	});
});

// =========================================================================
// repeatable.json / compound_types.json / env_separator.json
// =========================================================================

function tagsApp(
	out: string[],
	opts?: {
		choices?: readonly [ChoiceRecord<string>, ...ChoiceRecord<string>[]];
		default?: string[];
	},
): AppImpl {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				tag: flag("tag", t.list(t.str), {
					help: "a tag",
					short: "t",
					...(opts?.choices !== undefined ? { choices: opts.choices } : {}),
					presence: "default",
					default: opts?.default ?? [],
				}),
			},
			handler: (a) => {
				out.push(`tags=${fmt(a.tag)}`);
			},
		}),
	);
	return app;
}

test("repeatable: occurrences accumulate; zero gives empty", async () => {
	const out: string[] = [];
	assert.equal(
		(
			await run(
				tagsApp(out),
				["cmd", "--tag", "alpha", "--tag", "beta", "--tag", "gamma"],
				out,
			)
		).stdout,
		"tags=alpha,beta,gamma",
	);
	const out2: string[] = [];
	assert.equal((await run(tagsApp(out2), ["cmd"], out2)).stdout, "tags=");
	const out3: string[] = [];
	assert.equal(
		(await run(tagsApp(out3), ["cmd", "--tag=alpha", "--tag=beta"], out3))
			.stdout,
		"tags=alpha,beta",
	);
	const out4: string[] = [];
	assert.equal(
		(await run(tagsApp(out4), ["cmd", "-t", "alpha", "-t", "beta"], out4))
			.stdout,
		"tags=alpha,beta",
	);
});

test("repeatable: int elements coerce; invalid element errors", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				ids: flag("ids", t.list(t.int), {
					help: "the ids",
					presence: "default",
					default: [],
				}),
			},
			handler: (a) => {
				out.push(`ids=${fmt(a.ids)}`);
			},
		}),
	);
	assert.equal(
		(await run(app, ["cmd", "--ids", "1", "--ids", "2", "--ids", "3"], out))
			.stdout,
		"ids=1,2,3",
	);
	const r = await run(app, ["cmd", "--ids", "abc"]);
	assert.equal(
		r.stderr,
		errOut("--ids: expected integer, got 'abc'", "myapp cmd"),
	);
});

test("repeatable: choices validated per element", async () => {
	const out: string[] = [];
	assert.equal(
		(
			await run(
				tagsApp(out, { choices: [{ value: "alpha" }, { value: "beta" }] }),
				["cmd", "--tag", "alpha", "--tag", "beta"],
				out,
			)
		).stdout,
		"tags=alpha,beta",
	);
	const r = await run(
		tagsApp([], { choices: [{ value: "alpha" }, { value: "beta" }] }),
		["cmd", "--tag", "alpha", "--tag", "delta"],
	);
	assert.equal(
		r.stderr,
		errOut(
			"--tag: invalid value 'delta', must be one of: alpha, beta",
			"myapp cmd",
		),
	);
});

test("repeatable: default applied at runtime; CLI replaces (no merge)", async () => {
	const out: string[] = [];
	assert.equal(
		(await run(tagsApp(out, { default: ["x", "y"] }), ["cmd"], out)).stdout,
		"tags=x,y",
	);
	const out2: string[] = [];
	assert.equal(
		(
			await run(
				tagsApp(out2, { default: ["x", "y"] }),
				["cmd", "--tag", "z"],
				out2,
			)
		).stdout,
		"tags=z",
	);
});

test("dict: key=value entries, missing equals, duplicate key", async () => {
	const mk = (out: string[]): AppImpl => {
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					header: flag("header", t.dict(t.str), {
						help: "a header",
						presence: "default",
						default: new Map(),
					}),
				},
				handler: (a) => {
					out.push(`headers=${fmt(a.header)}`);
				},
			}),
		);
		return app;
	};
	const out: string[] = [];
	assert.equal(
		(
			await run(
				mk(out),
				["cmd", "--header", "content-type=text/html", "--header", "b=2"],
				out,
			)
		).stdout,
		"headers=b=2,content-type=text/html",
	);
	assert.equal(
		(await run(mk([]), ["cmd", "--header", "no-equals-here"])).stderr,
		errOut(
			"--header: expected key=value or JSON, got 'no-equals-here'",
			"myapp cmd",
		),
	);
	assert.equal(
		(await run(mk([]), ["cmd", "--header", "a=1", "--header", "a=2"])).stderr,
		errOut("--header: duplicate key 'a'", "myapp cmd"),
	);
});

test("env_separator: split, escapes, unique and coercion errors", async () => {
	const mkTags = (out: string[], unique: boolean): AppImpl => {
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					tag: flag("tag", t.list(t.str), {
						help: "a tag",
						env: "TAGS",
						envSeparator: ",",
						prefixed: false,
						unique,
						presence: "default",
						default: [],
					}),
				},
				handler: (a) => {
					out.push(`tags=${fmt(a.tag)}`);
				},
			}),
		);
		return app;
	};
	await withEnv({ TAGS: "a,b,c" }, async () => {
		const out: string[] = [];
		assert.equal(
			(await run(mkTags(out, false), ["cmd"], out)).stdout,
			"tags=a,b,c",
		);
	});
	await withEnv({ TAGS: "a\\,b,c" }, async () => {
		const out: string[] = [];
		// Escaped separator joins the first two segments: elements "a,b" and "c".
		assert.equal(
			(await run(mkTags(out, false), ["cmd"], out)).stdout,
			"tags=a,b,c",
		);
	});
	await withEnv({ TAGS: "a,b,a" }, async () => {
		assert.equal(
			(await run(mkTags([], true), ["cmd"])).stderr,
			errOut("--tag: duplicate value 'a' (from env var 'TAGS')", "myapp cmd"),
		);
	});

	const mkCounts = (): AppImpl => {
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					count: flag("count", t.list(t.int), {
						help: "the counts",
						env: "COUNTS",
						envSeparator: ",",
						prefixed: false,
						unique: false,
						presence: "default",
						default: [],
					}),
				},
				handler: () => undefined,
			}),
		);
		return app;
	};
	await withEnv({ COUNTS: "1,abc,3" }, async () => {
		assert.equal(
			(await run(mkCounts(), ["cmd"])).stderr,
			errOut(
				"--count: expected integer, got 'abc' (from env var 'COUNTS')",
				"myapp cmd",
			),
		);
	});
});

// =========================================================================
// choices.json
// =========================================================================

test("choices: invalid str choice rejected with exact message", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				format: flag("format", t.str, {
					help: "output format",
					choices: [{ value: "text" }, { value: "json" }],
					presence: "default",
					default: "text",
				}),
			},
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["cmd", "--format", "xml"]);
	assert.equal(
		r.stderr,
		errOut(
			"--format: invalid value 'xml', must be one of: text, json",
			"myapp cmd",
		),
	);
	assert.equal((await run(app, ["cmd", "--format", "json"])).exitCode, 0);
});

test("choices: optional arg with choices -- omitted ok, invalid rejected", async () => {
	const mk = (out: string[]): AppImpl => {
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				args: [
					arg("env", t.str, {
						help: "target env",
						presence: "optional",
						choices: [{ value: "dev" }, { value: "prod" }],
					}),
				],
				handler: (a) => {
					out.push(`env=${fmt(a.env)}`);
				},
			}),
		);
		return app;
	};
	const out: string[] = [];
	assert.equal((await run(mk(out), ["cmd"], out)).stdout, "env=None");
	assert.equal(
		(await run(mk([]), ["cmd", "local"])).stderr,
		errOut(
			"argument 'env': invalid value 'local', must be one of: dev, prod",
			"myapp cmd",
		),
	);
});

test("choices: int arg choices format values without quotes-mismatch", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [
				arg("level", t.int, {
					help: "the level",
					choices: [{ value: 0n }, { value: 1n }, { value: 2n }],
					presence: "required",
				}),
			],
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["cmd", "5"]);
	assert.equal(
		r.stderr,
		errOut(
			"argument 'level': invalid value '5', must be one of: 0, 1, 2",
			"myapp cmd",
		),
	);
});

test("choices: a scoped flag's choices are not validated when its scope is not elected", async () => {
	const out: string[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				sink: choiceFlag(
					"sink",
					{
						render: choice({
							help: "render the report",
							flags: {
								format: flag("format", t.str, {
									help: "output format",
									presence: "optional",
									choices: [{ value: "text" }, { value: "json" }],
								}),
							},
						}),
						write: choice({
							help: "write the report",
							flags: {
								output: flag("output", t.str, {
									help: "output path",
									presence: "optional",
								}),
							},
						}),
					},
					{ help: "where the report goes", presence: "required" },
				),
			},
			handler: (a) => {
				out.push(
					a.sink.choice === "write"
						? `write output=${fmt(a.sink.output)}`
						: `render format=${fmt(a.sink.format)}`,
				);
			},
		}),
	);
	const r = await run(
		app,
		["cmd", "--sink", "write", "--output", "out.txt"],
		out,
	);
	assert.equal(r.stdout, "write output=out.txt");
});

// =========================================================================
// Custom validate callbacks
// =========================================================================

test("validate: rejecting validator produces --flag: message", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				port: flag("port", t.int, {
					help: "the port",
					presence: "default",
					default: 1n,
					validate: (v) => {
						if (v > 65535n) {
							throw new Error("port must be <= 65535");
						}
					},
				}),
			},
			handler: () => undefined,
		}),
	);
	assert.equal((await run(app, ["cmd", "--port", "80"])).exitCode, 0);
	const r = await run(app, ["cmd", "--port", "70000"]);
	assert.equal(r.stderr, errOut("--port: port must be <= 65535", "myapp cmd"));
});

test("validate: list validator runs per element", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				tag: flag("tag", t.list(t.str), {
					help: "a tag",
					validate: (v) => {
						if (v.startsWith("x")) {
							throw new Error(`bad tag '${v}'`);
						}
					},
					presence: "default",
					default: [],
				}),
			},
			handler: () => undefined,
		}),
	);
	assert.equal((await run(app, ["cmd", "--tag", "ok"])).exitCode, 0);
	const r = await run(app, ["cmd", "--tag", "ok", "--tag", "xbad"]);
	assert.equal(r.stderr, errOut("--tag: bad tag 'xbad'", "myapp cmd"));
});

/**
 * §23.5's validate row: a validator runs on a SUPPLIED value only. A declared
 * default is the declaration's own value and is never handed to the
 * validator, so a default the validator would reject still parses -- and the
 * validator is not called at all.
 */
function rejectEverythingApp(calls: unknown[]): AppImpl {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				port: flag("port", t.int, {
					help: "the port",
					presence: "default",
					default: 70000n,
					env: "MYAPP_PORT",
					validate: (v) => {
						calls.push(v);
						throw new Error("port must be <= 65535");
					},
				}),
			},
			handler: () => undefined,
		}),
	);
	return app;
}

test("validate: never runs on the declared default", async () => {
	const calls: unknown[] = [];
	const r = await run(rejectEverythingApp(calls), ["cmd"]);
	assert.equal(r.exitCode, 0);
	assert.equal(r.stderr, "");
	assert.deepEqual(calls, []);
});

test("validate: runs on a supplied value from cli and from env", async () => {
	const cliCalls: unknown[] = [];
	const cli = await run(rejectEverythingApp(cliCalls), ["cmd", "--port", "1"]);
	assert.equal(
		cli.stderr,
		errOut("--port: port must be <= 65535", "myapp cmd"),
	);
	assert.deepEqual(cliCalls, [1n]);

	const envCalls: unknown[] = [];
	await withEnv({ MYAPP_PORT: "2" }, async () => {
		const r = await run(rejectEverythingApp(envCalls), ["cmd"]);
		assert.equal(
			r.stderr,
			errOut("--port: port must be <= 65535", "myapp cmd"),
		);
	});
	assert.deepEqual(envCalls, [2n]);
});

test("validate: never runs on a list flag's declared default", async () => {
	const calls: unknown[] = [];
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				tag: flag("tag", t.list(t.str), {
					help: "a tag",
					presence: "default",
					default: ["xbad"],
					validate: (v) => {
						calls.push(v);
						if (v.startsWith("x")) {
							throw new Error(`bad tag '${v}'`);
						}
					},
				}),
			},
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["cmd"]);
	assert.equal(r.exitCode, 0);
	assert.deepEqual(calls, []);
});

test("validate: never runs on a RelativeToRoot infra default", async () => {
	// The reachable case for the predicate excluding `infra`: the validate
	// step runs AFTER defaults, so a resolved infra default is sitting in the
	// store when it runs. It is still the declaration's own value, so the
	// validator is not called and a value it would reject still parses.
	const calls: unknown[] = [];
	const build = (): AppImpl => {
		const app = makeApp({ infraRoot: { MYAPP_HOME: "/var/lib/myapp" } });
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					db: flag("db", t.str, {
						help: "database path",
						presence: "default",
						default: relativeToRoot("MYAPP_HOME", "db.sqlite"),
						validate: (v) => {
							calls.push(v);
							throw new Error("no db path is acceptable");
						},
					}),
				},
				handler: () => undefined,
			}),
		);
		return app;
	};
	const r = await run(build(), ["cmd"]);
	assert.equal(r.exitCode, 0);
	assert.equal(r.stderr, "");
	assert.deepEqual(calls, []);
	// A typed value on the same flag is supplied, so it does validate.
	const typed = await run(build(), ["cmd", "--db", "/tmp/x"]);
	assert.equal(
		typed.stderr,
		errOut("--db: no db path is acceptable", "myapp cmd"),
	);
	assert.deepEqual(calls, ["/tmp/x"]);
});

test("validate: never runs on an implied flag's fallback default", async () => {
	// The implied source IS supplied (§23.6), so the injected value validates;
	// without the trigger the same flag falls back to its default and does not.
	const calls: unknown[] = [];
	const build = (): AppImpl => {
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					fast: flag("fast", t.bool, {
						help: "fast",
						presence: "default",
						default: false,
					}),
					unsafe: flag("unsafe", t.bool, {
						help: "unsafe",
						presence: "default",
						default: true,
						validate: (v) => {
							calls.push(v);
							throw new Error("no unsafe");
						},
					}),
				},
				constraints: [
					implies({
						name: "fast-unsafe",
						flag: "fast",
						implies: "unsafe",
						value: true,
					}),
				],
				handler: () => undefined,
			}),
		);
		return app;
	};
	assert.equal((await run(build(), ["cmd"])).exitCode, 0);
	assert.deepEqual(calls, []);
	const r = await run(build(), ["cmd", "--fast"]);
	assert.equal(r.stderr, errOut("--unsafe: no unsafe", "myapp cmd"));
	assert.deepEqual(calls, [true]);
});

// =========================================================================
// passthrough.json
// =========================================================================

test("passthrough: receives raw args and pre-command globals", async () => {
	const out: string[] = [];
	const app = makeApp({
		flags: {
			chatter: flag("chatter", t.bool, {
				help: "enable chatter output",
				presence: "default",
				default: false,
			}),
		},
	});
	app.command(
		readOnlyPassthrough("checkout", {
			help: "git checkout passthrough",
			handler: (a) => {
				out.push(`${a.name}:${a.args.join(",")}`);
				out.push(`chatter=${fmt(a.globals.chatter)}`);
			},
		}),
	);
	const r = await run(app, ["--chatter", "checkout", "-b", "feature"], out);
	assert.equal(r.exitCode, 0);
	assert.equal(r.stdout, "checkout:-b,feature\nchatter=true");
	const o = r.outcome as Extract<ParseOutcome, { kind: "passthrough" }>;
	assert.deepEqual(o.args, ["-b", "feature"]);
	assert.equal(o.cmdPath, "checkout");
});

// =========================================================================
// exit codes and command help recognition
// =========================================================================

test("handler numeric return becomes the exit code", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("fail", { help: "always fails", handler: () => 3 }),
	);
	const r = await run(app, ["fail"]);
	assert.equal(r.exitCode, 3);
});

test("help: --help/-h recognized anywhere in command tokens, but not after --", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					presence: "required",
				}),
			},
			args: [arg("path", t.str, { help: "a path", presence: "optional" })],
			handler: () => undefined,
		}),
	);
	for (const argv of [
		["cmd", "--help"],
		["cmd", "-h"],
		["cmd", "--target", "x", "--help"],
	]) {
		const r = await run(app, argv);
		assert.equal(r.kind, "help");
		const o = r.outcome as Extract<ParseOutcome, { kind: "help" }>;
		assert.equal(o.target.level, "command");
	}
	// After -- the token is a literal positional, not a help request.
	const r = await run(app, ["cmd", "--target", "x", "--", "--help"]);
	assert.equal(r.kind, "command");
});

// =========================================================================
// hermetic.json + reserved-flag pre-scan
// =========================================================================

test("hermetic: env vars are ignored; source is 'default'", async () => {
	const out: string[] = [];
	const app = makeApp({ envPrefix: "MYAPP" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run it",
			flags: {
				level: flag("level", t.int, {
					help: "the level",
					env: "MYAPP_LEVEL",
					presence: "default",
					default: 0n,
				}),
			},
			handler: (a) => {
				out.push(`level=${fmt(a.level)}`);
			},
		}),
	);
	await withEnv({ MYAPP_LEVEL: "42" }, async () => {
		const r = await run(app, ["--hermetic", "run"], out);
		assert.equal(r.stdout, "level=0");
		const o = r.outcome as Extract<ParseOutcome, { kind: "command" }>;
		assert.equal(o.sources.level, "default");
	});
});

test("hermetic: required flag missing errors even when env is set", async () => {
	const app = makeApp({ envPrefix: "MYAPP" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run it",
			flags: {
				name: flag("name", t.str, {
					help: "the name",
					env: "MYAPP_NAME",
					presence: "required",
				}),
			},
			handler: () => undefined,
		}),
	);
	await withEnv({ MYAPP_NAME: "test-value" }, async () => {
		const r = await run(app, ["--hermetic", "run"]);
		assert.equal(r.stderr, errOut("flag '--name' is required", "myapp run"));
	});
});

test("hermetic: --hermetic and --config are mutually exclusive", async () => {
	const app = makeApp({ config: true });
	app.command(
		defineReadOnlyCommand("run", { help: "run it", handler: () => undefined }),
	);
	const r = await run(app, [
		"--hermetic",
		"--config",
		"/tmp/nonexistent.json",
		"run",
	]);
	assert.equal(
		r.stderr,
		errOut("--hermetic and --config are mutually exclusive"),
	);
});

test("hermetic: --hermetic cannot be used with config commands", async () => {
	const app = makeApp({ config: true });
	const config = app.group("config", { help: "manage configuration" });
	config.command(
		defineReadOnlyCommand("show", {
			help: "display config",
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["--hermetic", "config", "show"]);
	assert.equal(
		r.stderr,
		errOut("--hermetic cannot be used with config commands"),
	);
});

test("prescan: --config on a non-config app is rejected", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["--config", "x.json", "cmd"]);
	assert.equal(
		r.stderr,
		errOut("--config is not available: this app does not use config files"),
	);
});

test("prescan: --config requires a value (bare and equals forms)", async () => {
	const app = makeApp({ config: true });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: () => undefined,
		}),
	);
	assert.equal(
		(await run(app, ["--config"])).stderr,
		errOut("flag '--config' requires a value"),
	);
	assert.equal(
		(await run(app, ["--config=", "cmd"])).stderr,
		errOut("flag '--config' requires a value"),
	);
});

test("prescan: --dump-schema and --mcp intercept in the pre-command region only", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: () => undefined,
		}),
	);
	assert.equal(doParse(app, ["--dump-schema"]).kind, "dump-schema");
	assert.equal(doParse(app, ["--mcp"]).kind, "mcp");
	// After the command token they are ordinary unknown flags.
	const r = await run(app, ["cmd", "--dump-schema"]);
	assert.equal(r.stderr, errOut("unknown flag '--dump-schema'", "myapp cmd"));
});

test("prescan: global flag value that looks like a command name is skipped", () => {
	const app = makeApp({
		flags: {
			settings: flag("settings", t.str, {
				help: "settings path",
				presence: "default",
				default: "/etc",
			}),
		},
	});
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: () => undefined,
		}),
	);
	const pre = preScanReservedFlags(app as AppImpl, [
		"--settings",
		"cmd",
		"--hermetic",
		"cmd",
	]);
	assert.equal(pre.hermetic, true);
	assert.deepEqual(pre.cleanedArgv, ["--settings", "cmd", "cmd"]);
});

// =========================================================================
// provenance.json (source labels)
// =========================================================================

test("provenance: cli vs default; post- and pre-command global cli", async () => {
	const app = makeApp({
		flags: {
			settings: flag("settings", t.str, {
				help: "settings path",
				presence: "default",
				default: "/etc/myapp",
			}),
		},
	});
	app.command(
		defineReadOnlyCommand("run", {
			help: "run it",
			flags: {
				output: flag("output", t.str, {
					help: "output file",
					presence: "default",
					default: "none",
				}),
				level: flag("level", t.int, {
					help: "the level",
					presence: "default",
					default: 5n,
				}),
			},
			handler: () => undefined,
		}),
	);
	const r = await run(app, ["run", "--output", "file.txt"]);
	const o = r.outcome as Extract<ParseOutcome, { kind: "command" }>;
	assert.equal(o.sources.output, "cli");
	assert.equal(o.sources.level, "default");
	assert.equal(o.sources.settings, "default");

	const r2 = await run(app, ["run", "--settings", "from-cli"]);
	const o2 = r2.outcome as Extract<ParseOutcome, { kind: "command" }>;
	assert.equal(o2.sources.settings, "cli");
	assert.equal(o2.kwargs.settings, "from-cli");

	const r3 = await run(app, ["--settings", "from-cli", "run"]);
	const o3 = r3.outcome as Extract<ParseOutcome, { kind: "command" }>;
	assert.equal(o3.sources.settings, "cli");
});

test("provenance: env label; CLI overrides env with label cli", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("run", {
			help: "run it",
			flags: {
				level: flag("level", t.int, {
					help: "the level",
					env: "PROV_LEVEL",
					prefixed: false,
					presence: "default",
					default: 5n,
				}),
			},
			handler: () => undefined,
		}),
	);
	await withEnv({ PROV_LEVEL: "42" }, async () => {
		const r = await run(app, ["run"]);
		const o = r.outcome as Extract<ParseOutcome, { kind: "command" }>;
		assert.equal(o.sources.level, "env");
		assert.equal(o.kwargs.level, 42n);
		const r2 = await run(app, ["run", "--level", "99"]);
		const o2 = r2.outcome as Extract<ParseOutcome, { kind: "command" }>;
		assert.equal(o2.sources.level, "cli");
	});
});

// =========================================================================
// Config seam (provider injection): precedence and conflicts
// =========================================================================

test("config: fills flags not set by CLI or env; CLI wins by default", async () => {
	const mk = (out: string[]): AppImpl => {
		const app = makeApp({ config: true });
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					mode: flag("mode", t.str, {
						help: "the mode",
						presence: "default",
						default: "normal",
					}),
				},
				handler: (a) => {
					out.push(`mode=${fmt(a.mode)}`);
				},
			}),
		);
		return app;
	};
	const deps: DoParseDeps = { config: fakeConfig({ mode: "from-config" }) };
	const out: string[] = [];
	const r = await run(mk(out), ["cmd"], out, deps);
	assert.equal(r.stdout, "mode=from-config");
	const o = r.outcome as Extract<ParseOutcome, { kind: "command" }>;
	assert.equal(o.sources.mode, "config");

	const out2: string[] = [];
	const r2 = await run(mk(out2), ["cmd", "--mode", "cli-val"], out2, deps);
	assert.equal(r2.stdout, "mode=cli-val");
});

test("config: conflict mode error rejects diverging cli+config", async () => {
	const app = makeApp({ config: true, configConflictMode: "error" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				mode: flag("mode", t.str, {
					help: "the mode",
					presence: "default",
					default: "normal",
				}),
			},
			handler: () => undefined,
		}),
	);
	const deps: DoParseDeps = { config: fakeConfig({ mode: "config-val" }) };
	const r = await run(app, ["cmd", "--mode", "cli-val"], [], deps);
	assert.equal(
		r.stderr,
		errOut("flag 'mode' set in both cli and config; remove one", "myapp cmd"),
	);
	// Matching values are not a conflict.
	const r2 = await run(app, ["cmd", "--mode", "config-val"], [], deps);
	assert.equal(r2.exitCode, 0);
});

test("config: post-command global conflict detection (error mode)", async () => {
	const app = makeApp({
		config: true,
		configConflictMode: "error",
		flags: {
			settings: flag("settings", t.str, {
				help: "settings path",
				presence: "default",
				default: "/etc/myapp",
			}),
		},
	});
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			handler: () => undefined,
		}),
	);
	const deps: DoParseDeps = { config: fakeConfig({ settings: "/cfg" }) };
	const r = await run(app, ["cmd", "--settings", "/other"], [], deps);
	assert.equal(
		r.stderr,
		errOut(
			"flag 'settings' set in both cli and config; remove one",
			"myapp cmd",
		),
	);
});

test("config: repeatable config value overrides default", async () => {
	const out: string[] = [];
	const app = makeApp({ config: true });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				tag: flag("tag", t.list(t.str), {
					help: "a tag",
					presence: "default",
					default: ["x", "y"],
				}),
			},
			handler: (a) => {
				out.push(`tags=${fmt(a.tag)}`);
			},
		}),
	);
	const deps: DoParseDeps = { config: fakeConfig({ tag: ["from-config"] }) };
	const r = await run(app, ["cmd"], out, deps);
	assert.equal(r.stdout, "tags=from-config");
});

test("config: hermetic skips config entirely", async () => {
	const out: string[] = [];
	const app = makeApp({ config: true });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				mode: flag("mode", t.str, {
					help: "the mode",
					presence: "default",
					default: "normal",
				}),
			},
			handler: (a) => {
				out.push(`mode=${fmt(a.mode)}`);
			},
		}),
	);
	const deps: DoParseDeps = { config: fakeConfig({ mode: "from-config" }) };
	const r = await run(app, ["--hermetic", "cmd"], out, deps);
	assert.equal(r.stdout, "mode=normal");
});

// =========================================================================
// keyword-ish names and param mapping
// =========================================================================

test("flagParamName maps dashes to underscores (no Python keyword suffix)", () => {
	assert.equal(flagParamName("sim-run"), "sim_run");
	assert.equal(flagParamName("--sim-run"), "sim_run");
	assert.equal(flagParamName("global"), "global");
});

test("tokensContainHelp respects the -- separator", () => {
	assert.equal(tokensContainHelp(["--target", "x", "--help"]), true);
	assert.equal(tokensContainHelp(["-h"]), true);
	assert.equal(tokensContainHelp(["--", "--help"]), false);
	assert.equal(tokensContainHelp(["a", "b"]), false);
});

// =========================================================================
// The presence declaration (contract §23)
// =========================================================================

/** Builds a one-command app whose single flag is declared as given. */
function appWithFlag(f: AnyFlag): AppImpl {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: { [flagParamName(f.name)]: f } as Record<string, AnyFlag>,
			handler: () => 0,
		}),
	);
	return app;
}

async function kwargsOf(
	app: AppImpl,
	argv: readonly string[],
): Promise<Record<string, unknown>> {
	const r = await run(app, argv);
	const o = r.outcome as Extract<ParseOutcome, { kind: "command" }>;
	assert.equal(r.kind, "command", r.stderr);
	return o.kwargs;
}

async function sourcesOf(
	app: AppImpl,
	argv: readonly string[],
): Promise<Record<string, string>> {
	const r = await run(app, argv);
	const o = r.outcome as Extract<ParseOutcome, { kind: "command" }>;
	assert.equal(r.kind, "command", r.stderr);
	return o.sources;
}

test("presence: an optional flag delivers absence, whatever the carrier", async () => {
	const scalar = appWithFlag(
		flag("target", t.str, { help: "the target", presence: "optional" }),
	);
	assert.deepEqual(await kwargsOf(scalar, ["cmd"]), { target: undefined });

	const list = appWithFlag(
		flag("tag", t.list(t.str), { help: "tags", presence: "optional" }),
	);
	// Absence, NOT the empty list the framework used to invent (§23.4).
	assert.deepEqual(await kwargsOf(list, ["cmd"]), { tag: undefined });
	assert.deepEqual(await kwargsOf(list, ["cmd", "--tag", "a"]), { tag: ["a"] });

	const dict = appWithFlag(
		flag("meta", t.dict(t.int), { help: "meta", presence: "optional" }),
	);
	assert.deepEqual(await kwargsOf(dict, ["cmd"]), { meta: undefined });
});

test("presence: an optional bool is a real tri-state", async () => {
	const app = appWithFlag(
		flag("color", t.bool, { help: "colorize", presence: "optional" }),
	);
	assert.deepEqual(await kwargsOf(app, ["cmd"]), { color: undefined });
	assert.deepEqual(await kwargsOf(app, ["cmd", "--color"]), { color: true });
	assert.deepEqual(await kwargsOf(app, ["cmd", "--no-color"]), {
		color: false,
	});
});

test("presence: a declared empty collection default is delivered as declared", async () => {
	const list = appWithFlag(
		flag("tag", t.list(t.str), {
			help: "tags",
			presence: "default",
			default: [],
		}),
	);
	assert.deepEqual(await kwargsOf(list, ["cmd"]), { tag: [] });

	const dict = appWithFlag(
		flag("meta", t.dict(t.int), {
			help: "meta",
			presence: "default",
			default: new Map(),
		}),
	);
	assert.deepEqual(await kwargsOf(dict, ["cmd"]), { meta: new Map() });
});

test("presence: a required compound flag must be supplied", async () => {
	const list = appWithFlag(
		flag("tag", t.list(t.str), { help: "tags", presence: "required" }),
	);
	const r = await run(list, ["cmd"]);
	assert.equal(r.exitCode, 1);
	assert.equal(r.stderr, errOut("flag '--tag' is required", "myapp cmd"));
	assert.deepEqual(await kwargsOf(list, ["cmd", "--tag", "a"]), { tag: ["a"] });

	const dict = appWithFlag(
		flag("meta", t.dict(t.int), { help: "meta", presence: "required" }),
	);
	assert.equal(
		(await run(dict, ["cmd"])).stderr,
		errOut("flag '--meta' is required", "myapp cmd"),
	);
});

test("presence: requiredness is satisfied by env, not only by a CLI token", async () => {
	const app = makeApp({ envPrefix: "MYAPP" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					env: "MYAPP_TARGET",
					presence: "required",
				}),
			},
			handler: () => 0,
		}),
	);
	await withEnv({ MYAPP_TARGET: "from-env" }, async () => {
		assert.deepEqual(await kwargsOf(app, ["cmd"]), { target: "from-env" });
	});
});

test("presence: a required member is refused at registration, in both families", () => {
	// §23.5's `CoRequired`/`required` cell shipped as "legal, and stated
	// because it is a surprising shape to write by accident". §26.5 inverts
	// it: the surprise was the whole objection, and a member the invocation
	// must always supply turns all-or-none into "every other member is
	// required too" -- a declaration that already has a spelling. In
	// at-least-one the constraint would be satisfied in every invocation and
	// could never fire at all.
	const build = (
		make: (
			members: readonly [{ name: string }, { name: string }],
		) => AllOrNone | AtLeastOne,
	): (() => unknown) => {
		return () =>
			defineReadOnlyCommand("cmd", {
				help: "a command",
				flags: {
					cert: flag("cert", t.str, {
						help: "the certificate",
						presence: "required",
					}),
					key: flag("key", t.str, {
						help: "the private key",
						presence: "optional",
					}),
				},
				constraints: [make([{ name: "cert" }, { name: "key" }])],
				handler: () => 0,
			});
	};
	const expected =
		'command "cmd": constraint "tls" member \'--cert\' declares presence: "required": a member the invocation must always supply leaves the constraint nothing to decide';
	assert.throws(
		build((members) => allOrNone({ name: "tls", members })),
		{ name: "RegistrationError", message: expected },
	);
	assert.throws(
		build((members) => atLeastOne({ name: "tls", members })),
		{ name: "RegistrationError", message: expected },
	);
});

test("presence: an optional arg delivers a present key holding absence", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [
				arg("src", t.str, { help: "source", presence: "required" }),
				arg("dest", t.str, { help: "destination", presence: "optional" }),
			],
			handler: () => 0,
		}),
	);
	const kwargs = await kwargsOf(app, ["cmd", "a"]);
	// The KEY is present -- key-absence delivery was rejected for the round.
	assert.ok("dest" in kwargs);
	assert.deepEqual(kwargs, { src: "a", dest: undefined });
	assert.deepEqual(await kwargsOf(app, ["cmd", "a", "b"]), {
		src: "a",
		dest: "b",
	});
});

test("presence: a variadic arg's presence decides whether none is legal", async () => {
	const mk = (presence: "required" | "optional"): AppImpl => {
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				args: [
					arg("files", t.str, { help: "files", variadic: true, presence }),
				],
				handler: () => 0,
			}),
		);
		return app;
	};
	assert.deepEqual(await kwargsOf(mk("optional"), ["cmd"]), { files: [] });
	assert.equal(
		(await run(mk("required"), ["cmd"])).stderr,
		errOut("missing required argument 'files'", "myapp cmd"),
	);
});

test("presence: a scoped sub-flag's own declaration decides its absence", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				source: choiceFlag(
					"source",
					{
						local: choice({
							help: "read locally",
							flags: {
								from_file: flag("from-file", t.str, {
									help: "from a file",
									presence: "optional",
								}),
								from_url: flag("from-url", t.str, {
									help: "from a URL",
									presence: "default",
									default: "https://example.test",
								}),
							},
						}),
						remote: choice({ help: "read remotely" }),
					},
					{ help: "where to read from", presence: "required" },
				),
			},
			handler: () => 0,
		}),
	);
	// §23 applies again unchanged one level down: an optional sub-flag
	// delivers absence as a PRESENT field of the record, a defaulted one its
	// declared default, and a sub-flag is never a top-level handler argument.
	assert.deepEqual(
		await kwargsOf(app, ["cmd", "--source", "local", "--from-file", "f"]),
		{
			source: {
				choice: "local",
				from_file: "f",
				from_url: "https://example.test",
			},
		},
	);
	assert.deepEqual(await kwargsOf(app, ["cmd", "--source", "remote"]), {
		source: { choice: "remote" },
	});
});

test("presence: an optional flag that received nothing reports source default", async () => {
	const app = appWithFlag(
		flag("target", t.str, { help: "the target", presence: "optional" }),
	);
	// No seventh source label is minted: an optional declaration deciding on
	// absence IS the declaration deciding (§23.6).
	assert.deepEqual(await sourcesOf(app, ["cmd"]), { target: "default" });
	assert.deepEqual(await sourcesOf(app, ["cmd", "--target", "x"]), {
		target: "cli",
	});
});

// ---------------------------------------------------------------------------
// Parse-problem precedence: structure before value (§24.3, §18.19 item 224)
//
// The phase order is a property of the parser, not of the declaration: an
// unknown flag, an unknown choice and a scope violation are facts about the
// command line's SHAPE, and shape is decided before any token's text is
// interpreted as a value -- on every command, selector-free ones included.
// ---------------------------------------------------------------------------

function precedenceApp(): AppImpl {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
	}) as unknown as AppImpl;
	(app as unknown as { command: (d: unknown) => void }).command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				count: flag("count", t.int, { help: "how many", presence: "optional" }),
				ratio: flag("ratio", t.float, {
					help: "the ratio",
					presence: "optional",
				}),
				mode: flag("mode", t.str, {
					help: "the mode",
					presence: "optional",
					choices: [{ value: "fast" }, { value: "slow" }],
				}),
			},
			handler: () => 0,
		}),
	);
	return app;
}

test("precedence: an unknown flag outranks a value that will not coerce", async () => {
	const r = await run(precedenceApp(), ["cmd", "--count", "abc", "--unknown"]);
	assert.equal(r.exitCode, 1);
	assert.match(r.stderr, /^error: unknown flag '--unknown'\n/);
});

test("precedence: the same holds when the bad value comes second", async () => {
	const r = await run(precedenceApp(), ["cmd", "--unknown", "--count", "abc"]);
	assert.match(r.stderr, /^error: unknown flag '--unknown'\n/);
});

test("precedence: an unknown flag outranks a float that will not coerce", async () => {
	const r = await run(precedenceApp(), ["cmd", "--ratio", "nope", "--unknown"]);
	assert.match(r.stderr, /^error: unknown flag '--unknown'\n/);
});

test("precedence: an unknown flag outranks an invalid choice", async () => {
	const r = await run(precedenceApp(), [
		"cmd",
		"--mode",
		"sideways",
		"--unknown",
	]);
	assert.match(r.stderr, /^error: unknown flag '--unknown'\n/);
});

test("precedence: a value problem alone still reports itself", async () => {
	// Precedence ORDERS problems; it never suppresses one.
	const r = await run(precedenceApp(), ["cmd", "--count", "abc"]);
	assert.match(r.stderr, /^error: --count: expected integer, got 'abc'\n/);
});

test("precedence: value problems keep command-line order among themselves", async () => {
	const a = await run(precedenceApp(), [
		"cmd",
		"--count",
		"abc",
		"--ratio",
		"nope",
	]);
	assert.match(a.stderr, /^error: --count: /);
	const b = await run(precedenceApp(), [
		"cmd",
		"--ratio",
		"nope",
		"--count",
		"abc",
	]);
	assert.match(b.stderr, /^error: --ratio: /);
});

test("precedence: a missing value is structural and outranks a bad value", async () => {
	// `--flag` with nothing after it is a token-consumption fact, decided in
	// the scan itself.
	const r = await run(precedenceApp(), ["cmd", "--count", "abc", "--ratio"]);
	assert.match(r.stderr, /^error: flag '--ratio' requires a value\n/);
});

test("precedence: an unknown flag outranks an unknown choice on a selector", async () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
	}) as unknown as AppImpl;
	(app as unknown as { command: (d: unknown) => void }).command(
		defineReadOnlyCommand("send", {
			help: "send it",
			flags: {
				via: choiceFlag(
					"via",
					{
						email: choice({ help: "by email" }),
						sms: choice({ help: "by text" }),
					},
					{ help: "delivery channel", presence: "required" },
				),
			},
			handler: () => 0,
		}),
	);
	const r = await run(app, ["send", "--via", "pigeon", "--unknown"]);
	assert.match(r.stderr, /^error: unknown flag '--unknown'\n/);
});

// ---------------------------------------------------------------------------
// The argv value phase's own order: COMMAND-LINE order, root and scoped alike
// (§24.3, §18.28 item 262), with §18.20 item 226's exception intact -- every
// coercion failure is reported before any `validate` refusal.
//
// The command's declaration order decides the two PROGRAMMATIC doors' sweep
// (§18.25 item 249) and nothing on this path: a root flag's refusal does not
// outrank a scoped one for being root, nor for being declared first.
// ---------------------------------------------------------------------------

/**
 * A root int declared BEFORE the selector, a scoped int inside it, and a root
 * int declared AFTER it -- so a reported refusal names which of the three
 * orders the value phase used.
 */
function argvOrderApp(): AppImpl {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
	}) as unknown as AppImpl;
	(app as unknown as { command: (d: unknown) => void }).command(
		defineReadOnlyCommand("run", {
			help: "run it",
			flags: {
				before: flag("before", t.int, {
					help: "a root int declared before the selector",
					presence: "optional",
				}),
				via: choiceFlag(
					"via",
					{
						email: choice({
							help: "by email",
							flags: {
								subject: flag("subject", t.str, {
									help: "subject line",
									presence: "optional",
									validate: (v: string) => {
										if (v === "bad") {
											throw new Error("subject is not allowed");
										}
									},
								}),
								retries: flag("retries", t.int, {
									help: "delivery attempts",
									presence: "optional",
								}),
							},
						}),
						sms: choice({ help: "by text" }),
					},
					{ help: "delivery channel", presence: "required" },
				),
				after: flag("after", t.int, {
					help: "a root int declared after the selector",
					presence: "optional",
				}),
			},
			handler: () => 0,
		}),
	);
	return app;
}

test("argv value order: a scoped coercion failure typed first outranks a root one", async () => {
	const r = await run(argvOrderApp(), [
		"run",
		"--via",
		"email",
		"--retries",
		"nope",
		"--before",
		"nope",
	]);
	assert.match(r.stderr, /^error: --retries: expected integer, got 'nope'\n/);
});

test("argv value order: the root flag wins when its token comes first", async () => {
	const r = await run(argvOrderApp(), [
		"run",
		"--via",
		"email",
		"--before",
		"nope",
		"--retries",
		"nope",
	]);
	assert.match(r.stderr, /^error: --before: expected integer, got 'nope'\n/);
});

test("argv value order: a root flag declared AFTER the selector orders the same way", async () => {
	const first = await run(argvOrderApp(), [
		"run",
		"--via",
		"email",
		"--retries",
		"nope",
		"--after",
		"nope",
	]);
	assert.match(
		first.stderr,
		/^error: --retries: expected integer, got 'nope'\n/,
	);
	const second = await run(argvOrderApp(), [
		"run",
		"--via",
		"email",
		"--after",
		"nope",
		"--retries",
		"nope",
	]);
	assert.match(
		second.stderr,
		/^error: --after: expected integer, got 'nope'\n/,
	);
});

test("argv value order: coercion outranks validate whatever either's position", async () => {
	// §18.20 item 226: a value is coerced as its token is consumed, and
	// `validate` runs in a later pass -- so no argv order and no declaration
	// order produces the refusal ahead of the coercion failure.
	for (const argv of [
		["run", "--via", "email", "--subject", "bad", "--retries", "nope"],
		["run", "--via", "email", "--retries", "nope", "--subject", "bad"],
		["run", "--via", "email", "--subject", "bad", "--before", "nope"],
		["run", "--via", "email", "--before", "nope", "--subject", "bad"],
	]) {
		const r = await run(argvOrderApp(), argv);
		assert.match(r.stderr, /^error: --(retries|before): expected integer/);
	}
});

test("argv value order: a validate refusal alone still reports itself", async () => {
	const r = await run(argvOrderApp(), [
		"run",
		"--via",
		"email",
		"--subject",
		"bad",
	]);
	assert.match(r.stderr, /^error: --subject: subject is not allowed\n/);
});
