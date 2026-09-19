/**
 * Retired choices: the spellings a value flag or positional arg used to
 * accept, each carrying the message that names its replacement.
 *
 * The value-level twin of the deprecated-command construct. A retired value is
 * refused at parse time ahead of the invalid-value check, and it is never a
 * choice -- so help, the published `value_schema` enum, and the MCP projection
 * derived from it name the live set only.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import { arg, flag } from "../src/factories.js";
import { createApp, defineReadOnlyCommand, t } from "../src/index.js";

const LIVE = [{ value: "text" }, { value: "json" }] as const;
const RETIRED = [
	{ value: "xml", message: "use 'json' (XML output was dropped)" },
] as const;

type RetiredTuple = readonly [
	{ readonly value: string; readonly message: string },
	...{ readonly value: string; readonly message: string }[],
];

function formatApp(opts: {
	readonly retiredChoices?: RetiredTuple;
	readonly env?: string;
}) {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				format: flag("format", t.str, {
					help: "output format",
					choices: LIVE,
					...(opts.retiredChoices === undefined
						? {}
						: { retiredChoices: opts.retiredChoices }),
					...(opts.env === undefined ? {} : { env: opts.env }),
					presence: "required",
				}),
			},
			handler: (args, ctx) => {
				ctx.info(`format=${args.format}`);
			},
		}),
	);
	return app;
}

// --- Parse time: the refusal ---

test("a retired flag value is refused, naming its replacement", async () => {
	const app = formatApp({ retiredChoices: RETIRED });
	const r = await app.test(["cmd", "--format", "xml"]);
	assert.equal(r.exitCode, 1);
	assert.match(
		r.stderr,
		/--format: value 'xml' retired: use 'json' \(XML output was dropped\)/,
	);
});

test("a retired arg value is refused, naming its replacement", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [
				arg("mode", t.str, {
					help: "the mode",
					choices: [{ value: "fast" }, { value: "slow" }],
					retiredChoices: [
						{ value: "turbo", message: "use 'fast' (turbo was renamed)" },
					],
					presence: "required",
				}),
			],
			handler: (args, ctx) => {
				ctx.info(`mode=${args.mode}`);
			},
		}),
	);
	const r = await app.test(["cmd", "turbo"]);
	assert.equal(r.exitCode, 1);
	assert.match(
		r.stderr,
		/argument 'mode': value 'turbo' retired: use 'fast' \(turbo was renamed\)/,
	);
});

test("the retired sentence wins over the invalid-value one", async () => {
	const app = formatApp({ retiredChoices: RETIRED });
	const r = await app.test(["cmd", "--format", "xml"]);
	assert.equal(r.stderr.includes("must be one of"), false);
});

test("an unrelated bad value still takes the invalid-value sentence", async () => {
	const app = formatApp({ retiredChoices: RETIRED });
	const r = await app.test(["cmd", "--format", "yaml"]);
	assert.equal(r.exitCode, 1);
	assert.match(r.stderr, /invalid value 'yaml', must be one of: text, json/);
});

test("a live value is still accepted", async () => {
	const app = formatApp({ retiredChoices: RETIRED });
	const r = await app.test(["cmd", "--format", "json"]);
	assert.equal(r.exitCode, 0);
	assert.match(r.stdout, /format=json/);
});

test("a retired value from an env var is refused", async () => {
	process.env.MYAPP_FORMAT = "xml";
	try {
		const app = formatApp({ retiredChoices: RETIRED, env: "MYAPP_FORMAT" });
		const r = await app.test(["cmd"]);
		assert.equal(r.exitCode, 1);
		assert.match(r.stderr, /value 'xml' retired: use 'json'/);
	} finally {
		delete process.env.MYAPP_FORMAT;
	}
});

test("a retired int spelling formats through the error-value formatter", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				port: flag("port", t.int, {
					help: "the port",
					choices: [{ value: 80n }, { value: 443n }],
					retiredChoices: [
						{
							value: 8080n,
							message: "use 443 (the plaintext port was dropped)",
						},
					],
					presence: "required",
				}),
			},
			handler: () => undefined,
		}),
	);
	const r = await app.test(["cmd", "--port", "8080"]);
	assert.equal(r.exitCode, 1);
	assert.match(
		r.stderr,
		/--port: value '8080' retired: use 443 \(the plaintext port was dropped\)/,
	);
});

test("a retired element of a list flag is refused", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				tag: flag("tag", t.list(t.str), {
					help: "a tag",
					choices: [{ value: "alpha" }, { value: "beta" }],
					retiredChoices: [{ value: "gamma", message: "use 'beta'" }],
					presence: "default",
					default: [],
				}),
			},
			handler: () => undefined,
		}),
	);
	const r = await app.test(["cmd", "--tag", "alpha", "--tag", "gamma"]);
	assert.equal(r.exitCode, 1);
	assert.match(r.stderr, /--tag: value 'gamma' retired: use 'beta'/);
});

// --- Help output is unchanged ---

test("help never lists a retired value", async () => {
	const app = formatApp({ retiredChoices: RETIRED });
	const r = await app.test(["cmd", "--help"]);
	assert.equal(r.exitCode, 0);
	assert.match(r.stdout, /\[choices: text, json\]/);
	assert.equal(r.stdout.includes("xml"), false);
});

// --- Registration-time hard errors ---

function registrationError(build: () => unknown): string {
	try {
		build();
	} catch (e) {
		return (e as Error).message;
	}
	assert.fail("expected a registration error, got none");
}

test("a retired spelling equal to a live choice is refused", () => {
	const msg = registrationError(() =>
		flag("format", t.str, {
			help: "output format",
			choices: LIVE,
			retiredChoices: [{ value: "json", message: "use 'text'" }],
			presence: "required",
		}),
	);
	assert.equal(
		msg,
		`Flag "format": retired choice 'json' is also a live choice: a value is live or retired, never both`,
	);
});

test("an arg retired spelling equal to a live choice is refused", () => {
	const msg = registrationError(() =>
		arg("mode", t.str, {
			help: "the mode",
			choices: [{ value: "fast" }, { value: "slow" }],
			retiredChoices: [{ value: "fast", message: "use 'slow'" }],
			presence: "required",
		}),
	);
	assert.equal(
		msg,
		`Arg "mode": retired choice 'fast' is also a live choice: a value is live or retired, never both`,
	);
});

test("a duplicate retired spelling is refused", () => {
	const msg = registrationError(() =>
		flag("format", t.str, {
			help: "output format",
			choices: LIVE,
			retiredChoices: [
				{ value: "xml", message: "use 'json'" },
				{ value: "xml", message: "use 'text'" },
			],
			presence: "required",
		}),
	);
	assert.equal(msg, `Flag "format": retired choice 'xml' is declared twice`);
});

test("an empty retired message is refused", () => {
	const msg = registrationError(() =>
		flag("format", t.str, {
			help: "output format",
			choices: LIVE,
			retiredChoices: [{ value: "xml", message: "" }],
			presence: "required",
		}),
	);
	assert.equal(
		msg,
		`Flag "format": retired choice 'xml': message must be a non-empty string`,
	);
});

test("retired choices on a bool are refused", () => {
	const msg = registrationError(() =>
		flag("cache", t.bool, {
			help: "use the cache",
			// The type forbids this shape; the runtime guard is what a JS caller
			// and the conformance harness reach.
			retiredChoices: [{ value: "on", message: "use --cache" }] as never,
			presence: "default",
			default: false,
		}),
	);
	assert.equal(
		msg,
		`Flag "cache": retired choices are incompatible with type=bool`,
	);
});

test("a default naming a retired spelling is refused", () => {
	const msg = registrationError(() =>
		flag("format", t.str, {
			help: "output format",
			choices: LIVE,
			retiredChoices: RETIRED,
			presence: "default",
			default: "xml",
		}),
	);
	assert.equal(msg, `Flag "format": default 'xml' is a retired choice`);
});

test("an arg default naming a retired spelling is refused", () => {
	const msg = registrationError(() =>
		arg("mode", t.str, {
			help: "the mode",
			choices: [{ value: "fast" }, { value: "slow" }],
			retiredChoices: [{ value: "turbo", message: "use 'fast'" }],
			presence: "default",
			default: "turbo",
		}),
	);
	assert.equal(msg, `Arg "mode": default 'turbo' is a retired choice`);
});

// The seventh guard: a declaration that could never match. A retired spelling
// of the wrong type is dead -- the parse-time comparison is type-aware, so no
// invocation could reach it, and the spelling the author meant to retire stays
// accepted. TypeScript's `retiredChoices` entries are typed against the
// declaration, so reaching the runtime guard takes the same `loose` cast the
// live choice type-mismatch test uses; a JS consumer needs no cast at all.
function loose(v: unknown): never {
	return v as never;
}

test("a retired spelling of the wrong type is refused", () => {
	const msg = registrationError(() =>
		flag(
			"format",
			t.str,
			loose({
				help: "output format",
				choices: LIVE,
				retiredChoices: [{ value: 8080n, message: "use 'json'" }],
				presence: "required",
			}),
		),
	);
	assert.equal(msg, `Flag "format": retired choice '8080' is not of type str`);
});

test("an arg retired spelling of the wrong type is refused", () => {
	const msg = registrationError(() =>
		arg(
			"mode",
			t.str,
			loose({
				help: "the mode",
				choices: [{ value: "fast" }, { value: "slow" }],
				retiredChoices: [{ value: 8080n, message: "use 'fast'" }],
				presence: "required",
			}),
		),
	);
	assert.equal(msg, `Arg "mode": retired choice '8080' is not of type str`);
});

test("retired choices without choices are refused", () => {
	const msg = registrationError(() =>
		flag("format", t.str, {
			help: "output format",
			retiredChoices: RETIRED,
			presence: "required",
		}),
	);
	assert.equal(msg, `Flag "format": retired choices require choices`);
});

test("arg retired choices without choices are refused", () => {
	const msg = registrationError(() =>
		arg("mode", t.str, {
			help: "the mode",
			retiredChoices: [{ value: "turbo", message: "use 'fast'" }],
			presence: "required",
		}),
	);
	assert.equal(msg, `Arg "mode": retired choices require choices`);
});

// --- The schema dump ---

function flagEntryOf(retiredChoices?: RetiredTuple): Record<string, unknown> {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				format: flag("format", t.str, {
					help: "output format",
					choices: LIVE,
					...(retiredChoices === undefined ? {} : { retiredChoices }),
					presence: "required",
				}),
			},
			handler: () => undefined,
		}),
	);
	const dict = app.dumpSchemaDict() as {
		commands: { cmd: { flags: Record<string, unknown>[] } };
	};
	return dict.commands.cmd.flags[0] as Record<string, unknown>;
}

test("the schema publishes retired_choices sorted ascending by key", () => {
	const entry = flagEntryOf([
		{ value: "xml", message: "use 'json'" },
		{ value: "ascii", message: "use 'text'" },
	]);
	assert.deepEqual(Object.keys(entry.retired_choices as object), [
		"ascii",
		"xml",
	]);
	assert.deepEqual(entry.retired_choices, {
		ascii: "use 'text'",
		xml: "use 'json'",
	});
});

test("the schema omits retired_choices when none are declared", () => {
	assert.equal("retired_choices" in flagEntryOf(), false);
});

test("the value_schema fragment carries the live enum only", () => {
	const fragment = flagEntryOf([{ value: "xml", message: "use 'json'" }])
		.value_schema as { enum: readonly unknown[] };
	assert.deepEqual(fragment.enum, ["text", "json"]);
});

test("the arg entry publishes its own retired_choices map", () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [
				arg("mode", t.str, {
					help: "the mode",
					choices: [{ value: "fast" }],
					retiredChoices: [{ value: "turbo", message: "use 'fast'" }],
					presence: "required",
				}),
			],
			handler: () => undefined,
		}),
	);
	const dict = app.dumpSchemaDict() as {
		commands: { cmd: { args: Record<string, unknown>[] } };
	};
	const entry = dict.commands.cmd.args[0] as Record<string, unknown>;
	assert.deepEqual(entry.retired_choices, {
		turbo: "use 'fast'",
	});
});
