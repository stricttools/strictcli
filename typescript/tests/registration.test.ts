import { strict as assert } from "node:assert";
import { homedir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import type { AppImpl } from "../src/app.js";
import type { App } from "../src/index.js";
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
	flagSet,
	implies,
	memberChoiceFlag,
	readOnlyPassthrough,
	requires,
	t,
} from "../src/index.js";

// Expected messages are the captured Python implementation ground truth (the
// divergence oracle); most are byte-identical to the Go catalog as well.
function rejects(fn: () => unknown, message: string): void {
	assert.throws(fn, { name: "RegistrationError", message });
}

// Bypass the type layer to exercise runtime guards the way an untyped JS
// caller could (never is assignable to every parameter type).
function loose(v: unknown): never {
	return v as never;
}

// --- Flag validation ---

test("flag: help, force ban, no- prefix ban", () => {
	rejects(
		() => flag("target", t.str, { help: "  ", presence: "required" }),
		"Flag.help must be a non-empty string",
	);
	rejects(
		() => flag("force", t.str, { help: "hh", presence: "required" }),
		"flag 'force' is a reserved name; use a qualified name like 'force-overwrite' or 'force-delete'",
	);
	rejects(
		() =>
			flag("no-frame", t.bool, {
				help: "hh",
				presence: "default",
				default: true,
			}),
		"flag 'no-frame': names starting with 'no-' are reserved for the negation system; use a positive name instead",
	);
});

test("flag: dict carriers reject repeatable, unique, choices, envSeparator", () => {
	rejects(
		() =>
			flag(
				"meta",
				t.dict(t.int),
				loose({
					help: "hh",
					repeatable: true,
					presence: "default",
					default: new Map(),
				}),
			),
		'Flag "meta": dict type cannot be combined with repeatable=True',
	);
	rejects(
		() =>
			flag(
				"meta",
				t.dict(t.int),
				loose({
					help: "hh",
					unique: true,
					presence: "default",
					default: new Map(),
				}),
			),
		'Flag "meta": dict type cannot be combined with unique',
	);
	rejects(
		() =>
			flag(
				"meta",
				t.dict(t.int),
				loose({
					help: "hh",
					choices: [{ value: 1n }],
					presence: "default",
					default: new Map(),
				}),
			),
		'Flag "meta": dict type cannot be combined with choices',
	);
	rejects(
		() =>
			flag(
				"meta",
				t.dict(t.int),
				loose({
					help: "hh",
					envSeparator: ",",
					presence: "default",
					default: new Map(),
				}),
			),
		'Flag "meta": dict type cannot use env_separator (env vars are parsed as JSON)',
	);
});

test("flag: repeatable constraint web", () => {
	rejects(
		() =>
			flag(
				"chatter",
				t.bool,
				loose({
					help: "hh",
					presence: "default",
					default: false,
					repeatable: true,
				}),
			),
		'Flag "chatter": repeatable is incompatible with type=bool',
	);
	// TS-only: scalar repeatable flags do not exist -- list carriers ARE the
	// repeatable flags (no sibling analog for this inexpressible state).
	rejects(
		() =>
			flag(
				"tag",
				t.str,
				loose({ help: "hh", repeatable: true, presence: "required" }),
			),
		'Flag "tag": repeatable requires a list type',
	);
	rejects(
		() =>
			flag(
				"tag",
				t.str,
				loose({ help: "hh", unique: true, presence: "required" }),
			),
		'Flag "tag": unique requires repeatable=True',
	);
});

test("flag: envSeparator constraint web", () => {
	rejects(
		() =>
			flag(
				"tag",
				t.str,
				loose({ help: "hh", envSeparator: ",", presence: "required" }),
			),
		'Flag "tag": env_separator requires repeatable=True',
	);
	rejects(
		() =>
			flag("tag", t.list(t.str), {
				help: "hh",
				envSeparator: ",",
				presence: "default",
				default: [],
			}),
		'Flag "tag": env_separator requires env',
	);
	rejects(
		() =>
			flag("tag", t.list(t.str), {
				help: "hh",
				env: "TAGS",
				presence: "default",
				default: [],
			}),
		'Flag "tag": repeatable flag with env requires env_separator',
	);
	rejects(
		() =>
			flag("tag", t.list(t.str), {
				help: "hh",
				env: "TAGS",
				envSeparator: ",,",
				presence: "default",
				default: [],
			}),
		'Flag "tag": env_separator must be a single character',
	);
	rejects(
		() =>
			flag("tag", t.list(t.str), {
				help: "hh",
				env: "TAGS",
				envSeparator: "\\",
				presence: "default",
				default: [],
			}),
		'Flag "tag": env_separator cannot be a backslash',
	);
	// Positive: the full valid combination.
	const ok = flag("tag", t.list(t.str), {
		help: "hh",
		env: "TAGS",
		envSeparator: ",",
		unique: true,
		presence: "default",
		default: [],
	});
	assert.equal(ok.schema, "list[str]");
});

test("flag: conflictMode must be cli-wins or error", () => {
	rejects(
		() =>
			flag(
				"target",
				t.str,
				loose({ help: "hh", conflictMode: "merge", presence: "required" }),
			),
		'Flag "target": conflict_mode must be "cli-wins" or "error", got \'merge\'',
	);
	assert.equal(
		flag("target", t.str, {
			help: "hh",
			conflictMode: "error",
			presence: "required",
		}).opts.conflictMode,
		"error",
	);
});

test("flag: choices validation", () => {
	rejects(
		() =>
			flag(
				"chatter",
				t.bool,
				loose({
					help: "hh",
					presence: "default",
					default: false,
					choices: [{ value: true }],
				}),
			),
		'Flag "chatter": choices is incompatible with type=bool',
	);
	rejects(
		() =>
			flag(
				"fmt",
				t.str,
				loose({ help: "hh", choices: [], presence: "required" }),
			),
		'Flag "fmt": choices must be a non-empty list',
	);
	rejects(
		() =>
			flag(
				"fmt",
				t.str,
				loose({
					help: "hh",
					choices: [{ value: "aa" }, { value: 5n }],
					presence: "required",
				}),
			),
		'Flag "fmt": choice 5 is not of type str',
	);
	rejects(
		() =>
			flag(
				"lvl",
				t.int,
				loose({
					help: "hh",
					choices: [{ value: 1n }, { value: "xx" }],
					presence: "required",
				}),
			),
		"Flag \"lvl\": choice 'xx' is not of type int",
	);
	rejects(
		() =>
			flag(
				"ratio",
				t.float,
				loose({
					help: "hh",
					choices: [{ value: 1.5 }, { value: 2n }],
					presence: "required",
				}),
			),
		'Flag "ratio": choice 2 is not of type float',
	);
	// Python parity: choices on LIST flags are allowed and validate elements
	// against the item type (Go rejects; Python is the divergence oracle).
	const ok = flag("tag", t.list(t.str), {
		help: "hh",
		choices: [{ value: "aa" }, { value: "bb" }],
		presence: "default",
		default: [],
	});
	assert.deepEqual(ok.opts.choices, [{ value: "aa" }, { value: "bb" }]);
	rejects(
		() =>
			flag(
				"tag",
				t.list(t.int),
				loose({
					help: "hh",
					choices: [{ value: 1n }, { value: "xx" }],
					presence: "default",
					default: [],
				}),
			),
		"Flag \"tag\": choice 'xx' is not of type int",
	);
});

test("flag: scalar default type checks (int and float only, like siblings)", () => {
	rejects(
		() =>
			flag(
				"count",
				t.int,
				loose({ help: "hh", presence: "default", default: 5 }),
			),
		"Flag \"count\": type=int requires an int default, got 'float'",
	);
	rejects(
		() =>
			flag(
				"count",
				t.int,
				loose({ help: "hh", presence: "default", default: "xx" }),
			),
		"Flag \"count\": type=int requires an int default, got 'str'",
	);
	rejects(
		() =>
			flag(
				"ratio",
				t.float,
				loose({ help: "hh", presence: "default", default: 5n }),
			),
		"Flag \"ratio\": type=float requires a float default, got 'int'",
	);
	rejects(
		() =>
			flag(
				"ratio",
				t.float,
				loose({ help: "hh", presence: "default", default: "xx" }),
			),
		"Flag \"ratio\": type=float requires a float default, got 'str'",
	);
});

test("flag: dict default shape checks", () => {
	rejects(
		() =>
			flag(
				"meta",
				t.dict(t.int),
				loose({ help: "hh", presence: "default", default: [1n] }),
			),
		'Flag "meta": dict flag default must be a Map',
	);
	// An explicit empty dict default is a DECLARATION now (contract §23.5), so
	// the redundancy error it used to raise is deleted (§12.12).
	assert.doesNotThrow(() =>
		flag("meta", t.dict(t.int), {
			help: "hh",
			presence: "default",
			default: new Map(),
		}),
	);
	rejects(
		() =>
			flag(
				"meta",
				t.dict(t.int),
				loose({
					help: "hh",
					presence: "default",
					default: new Map([["aa", "xx"]]),
				}),
			),
		"Flag \"meta\": dict default value for key 'aa' is not of type int",
	);
	rejects(
		() =>
			flag(
				"meta",
				t.dict(t.int),
				loose({
					help: "hh",
					presence: "default",
					default: new Map([[5n, 1n]]),
				}),
			),
		'Flag "meta": dict default key 5 must be a string',
	);
	const ok = flag("meta", t.dict(t.int), {
		help: "hh",
		presence: "default",
		default: new Map([["aa", 1n]]),
	});
	assert.equal(ok.schema, "dict[str,int]");
});

test("flag: list default shape checks", () => {
	rejects(
		() =>
			flag(
				"tag",
				t.list(t.str),
				loose({ help: "hh", presence: "default", default: "xx" }),
			),
		'Flag "tag": list flag default must be an array',
	);
	// Likewise for the empty list default: declaring it is how a list flag says
	// "empty when absent", and omitting it is now the error.
	assert.doesNotThrow(() =>
		flag("tag", t.list(t.str), {
			help: "hh",
			presence: "default",
			default: [],
		}),
	);
	rejects(
		() =>
			flag(
				"tag",
				t.list(t.str),
				loose({ help: "hh", presence: "default", default: ["aa", 5n] }),
			),
		'Flag "tag": default element 1 is not of type str',
	);
	rejects(
		() =>
			flag(
				"lvl",
				t.list(t.int),
				loose({ help: "hh", presence: "default", default: [1n, 2] }),
			),
		'Flag "lvl": default element 1 is not of type int',
	);
	const ok = flag("tag", t.list(t.str), {
		help: "hh",
		presence: "default",
		default: ["aa"],
	});
	assert.deepEqual(ok.opts.default, ["aa"]);
});

test("flag: default must be in choices (Python repr formatting)", () => {
	rejects(
		() =>
			flag("fmt", t.str, {
				help: "hh",
				choices: [{ value: "text" }, { value: "json" }],
				presence: "default",
				default: "xml",
			}),
		"Flag \"fmt\": default 'xml' is not in choices ['text', 'json']",
	);
	rejects(
		() =>
			flag("lvl", t.int, {
				help: "hh",
				choices: [{ value: 1n }, { value: 2n }],
				presence: "default",
				default: 5n,
			}),
		'Flag "lvl": default 5 is not in choices [1, 2]',
	);
	const ok = flag("fmt", t.str, {
		help: "hh",
		choices: [{ value: "text" }, { value: "json" }],
		presence: "default",
		default: "text",
	});
	assert.equal(ok.opts.default, "text");
});

// --- Arg validation ---

test("arg: help and required-default", () => {
	rejects(
		() => arg("src", t.str, { help: " ", presence: "required" }),
		"Arg.help must be a non-empty string",
	);
});

test("arg: compound carriers are rejected", () => {
	rejects(
		() => arg("vv", loose(t.dict(t.int)), { help: "hh", presence: "required" }),
		'Arg "vv": dict type is not supported on args',
	);
	rejects(
		() => arg("vv", loose(t.list(t.int)), { help: "hh", presence: "required" }),
		'Arg "vv": list type on args requires variadic=True',
	);
	// The refusal that stood here is DELETED (§25.4): a variadic arg takes
	// either spelling -- the element carrier plus `variadic: true`, or the list
	// carrier the siblings spell it with -- and both register, deliver the same
	// array and publish the same fragment. This widens the surface; the element
	// spelling stays legal and stays the idiomatic one.
	arg("vv", t.list(t.int), {
		help: "hh",
		variadic: true,
		presence: "required",
	});
});

test("arg: both variadic spellings deliver and publish identically (§25.4)", async () => {
	const mk = (listCarrier: boolean): App => {
		const app = createApp({
			name: "myapp",
			version: "1.0.0",
			help: "test app",
		});
		app.command(
			defineReadOnlyCommand("cmd", {
				help: "a command",
				args: [
					listCarrier
						? arg("files", t.list(t.str), {
								help: "the files",
								variadic: true,
								presence: "optional",
							})
						: arg("files", t.str, {
								help: "the files",
								variadic: true,
								presence: "optional",
							}),
				],
				handler: (a, ctx) => {
					ctx.info(`files=${(a.files as readonly string[]).join(",")}`);
					return 0;
				},
			}),
		);
		return app;
	};
	const elementSpelling = await mk(false).test(["cmd", "aa", "bb"]);
	const listSpelling = await mk(true).test(["cmd", "aa", "bb"]);
	assert.equal(listSpelling.stdout, "files=aa,bb\n");
	assert.equal(listSpelling.stdout, elementSpelling.stdout);
	assert.equal(
		(await mk(true).test(["cmd", "--help"])).stdout,
		(await mk(false).test(["cmd", "--help"])).stdout,
	);
	const dumped = (app: App): unknown =>
		(
			(
				app.dumpSchemaDict() as {
					commands: Record<string, { args: unknown[] }>;
				}
			).commands.cmd as { args: unknown[] }
		).args;
	assert.deepEqual(dumped(mk(true)), dumped(mk(false)));
	assert.deepEqual(dumped(mk(true)), [
		{
			name: "files",
			help: "the files",
			value_schema: { type: "array", items: { type: "string" } },
			presence: "optional",
			variadic: true,
		},
	]);
});

test("arg: choices validation", () => {
	rejects(
		() =>
			arg(
				"vv",
				t.bool,
				loose({ help: "hh", choices: [{ value: true }], presence: "required" }),
			),
		'Arg "vv": choices is incompatible with type=bool',
	);
	rejects(
		() =>
			arg(
				"vv",
				t.str,
				loose({ help: "hh", choices: [], presence: "required" }),
			),
		'Arg "vv": choices must be a non-empty list',
	);
	rejects(
		() =>
			arg(
				"vv",
				t.str,
				loose({
					help: "hh",
					choices: [{ value: "aa" }, { value: 5n }],
					presence: "required",
				}),
			),
		'Arg "vv": choice 5 is not of type str',
	);
	// Variadic args may declare choices (validated per element at parse time).
	const ok = arg("vv", t.str, {
		help: "hh",
		variadic: true,
		choices: [{ value: "aa" }, { value: "bb" }],
		presence: "required",
	});
	assert.deepEqual(ok.opts.choices, [{ value: "aa" }, { value: "bb" }]);
});

test("arg: default type checks for all four types", () => {
	rejects(
		() =>
			arg("vv", t.str, loose({ help: "hh", presence: "default", default: 5n })),
		"Arg \"vv\": type=str requires a str default, got 'int'",
	);
	rejects(
		() =>
			arg(
				"vv",
				t.int,
				loose({ help: "hh", presence: "default", default: "xx" }),
			),
		"Arg \"vv\": type=int requires an int default, got 'str'",
	);
	rejects(
		() =>
			arg(
				"vv",
				t.float,
				loose({ help: "hh", presence: "default", default: "xx" }),
			),
		"Arg \"vv\": type=float requires a float default, got 'str'",
	);
	rejects(
		() =>
			arg(
				"vv",
				t.bool,
				loose({ help: "hh", presence: "default", default: 5n }),
			),
		"Arg \"vv\": type=bool requires a bool default, got 'int'",
	);
});

test("arg: default must be in choices (Python repr formatting)", () => {
	rejects(
		() =>
			arg("vv", t.str, {
				help: "hh",
				choices: [{ value: "aa" }, { value: "bb" }],
				presence: "default",
				default: "cc",
			}),
		"Arg \"vv\": default 'cc' is not in choices ['aa', 'bb']",
	);
	rejects(
		() =>
			arg("vv", t.int, {
				help: "hh",
				choices: [{ value: 1n }, { value: 2n }],
				presence: "default",
				default: 5n,
			}),
		'Arg "vv": default 5 is not in choices [1, 2]',
	);
});

// --- twin command factory validation ---

const strFlag = (name: string) =>
	flag(name, t.str, { help: "hh", presence: "required" });
/** Mutex members declare their own absence (contract §23.5's mutex row). */
const optStrFlag = (name: string) =>
	flag(name, t.str, { help: "hh", presence: "optional" });
const boolFlag = (name: string) =>
	flag(name, t.bool, { help: "hh", presence: "default", default: false });

test("command: missing help", () => {
	rejects(
		() => defineReadOnlyCommand("xx", { help: " ", handler: () => 0 }),
		'command "xx": missing help text',
	);
	rejects(
		() => readOnlyPassthrough("xx", { help: " ", handler: () => 0 }),
		'command "xx": missing help text',
	);
});

test("command: flag-map keys must be underscore forms (flags, flagSets, mutex)", () => {
	rejects(
		() =>
			defineReadOnlyCommand("build", {
				help: "hh",
				flags: {
					simRun: flag("sim-run", t.bool, {
						help: "hh",
						presence: "default",
						default: false,
					}),
				},
				handler: () => 0,
			}),
		"command \"build\": flags key 'simRun' must be the underscore form of flag 'sim-run' ('sim_run')",
	);
	rejects(
		() =>
			defineReadOnlyCommand("build", {
				help: "hh",
				flagSets: [flagSet("fs", loose({ wrong: strFlag("right") }))],
				handler: () => 0,
			}),
		"command \"build\": flags key 'wrong' must be the underscore form of flag 'right' ('right')",
	);
});

test("selector: at least two choices, and no co-electable name reuse", () => {
	rejects(
		() =>
			choiceFlag("via", loose({ email: choice({ help: "email" }) }), {
				help: "hh",
				presence: "required",
			}),
		'Flag "via": a choice flag must declare at least two choices',
	);
	// Two selectors on one command can be elected at the same time, so they
	// may not reuse a flag name (contract §24.7).
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: {
					via: choiceFlag(
						"via",
						{
							email: choice({
								help: "email",
								flags: { subject: optStrFlag("subject") },
							}),
							sms: choice({ help: "sms" }),
						},
						{ help: "hh", presence: "required" },
					),
					mode: choiceFlag(
						"mode",
						{
							quick: choice({
								help: "quick",
								flags: { subject: optStrFlag("subject") },
							}),
							slow: choice({ help: "slow" }),
						},
						{ help: "hh", presence: "required" },
					),
				},
				handler: () => 0,
			}),
		"command \"cmd\": flag '--subject' is declared under '--via email' and under '--mode quick', which can be elected at the same time: simultaneously electable scopes may not reuse a flag name",
	);
});

test("command: duplicate flag and arg names", () => {
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: strFlag("aa") },
				flagSets: [flagSet("fs", { aa: strFlag("aa") })],
				handler: () => 0,
			}),
		'command "cmd": duplicate flag name "aa"',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				args: [
					arg("xx", t.str, { help: "hh", presence: "required" }),
					arg("xx", t.str, { help: "hh", presence: "required" }),
				],
				handler: () => 0,
			}),
		'command "cmd": duplicate arg name "xx"',
	);
});

test("command: variadic arg constraints", () => {
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				args: [
					arg("xx", t.str, {
						help: "hh",
						variadic: true,
						presence: "required",
					}),
					arg("yy", t.str, {
						help: "hh",
						variadic: true,
						presence: "required",
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": at most one variadic arg is allowed',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				args: [
					arg("xx", t.str, {
						help: "hh",
						variadic: true,
						presence: "required",
					}),
					arg("yy", t.str, { help: "hh", presence: "required" }),
				],
				handler: () => 0,
			}),
		'command "cmd": variadic arg "xx" must be the last arg',
	);
});

// --- The constraint system (contract §26, §12.15) ---

test("constraint: name legality -- charset, duplicates, flag/arg collision", () => {
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				constraints: [
					allOrNone({
						name: "Bad-Name",
						members: [{ name: "aa" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint name "Bad-Name" must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				constraints: [
					allOrNone({
						name: "pair",
						members: [{ name: "aa" }, { name: "bb" }],
					}),
					atLeastOne({
						name: "pair",
						members: [{ name: "aa" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": duplicate constraint name "pair"',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				constraints: [
					allOrNone({ name: "aa", members: [{ name: "aa" }, { name: "bb" }] }),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint name "aa" is already a flag or arg name: a member reference resolves by name and would be ambiguous',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				args: [arg("target", t.str, { help: "hh", presence: "optional" })],
				constraints: [
					allOrNone({
						name: "target",
						members: [{ name: "aa" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint name "target" is already a flag or arg name: a member reference resolves by name and would be ambiguous',
	);
});

test("constraint: the two-member floor is a compile error, and a widened caller still refuses", () => {
	// The typed `members` tuple makes a one-member constraint a COMPILE error
	// (§26.6); the runtime guard is reachable only through a widened or
	// JSON-shaped caller, which is exactly the covering input its conformance
	// case asserts.
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa") },
				constraints: [
					allOrNone(loose({ name: "pair", members: [{ name: "aa" }] })),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "pair" must declare at least two members, got 1',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				constraints: [
					allOrNone(loose({ name: "pair", members: ["aa", { name: "bb" }] })),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "pair" member 0 is a bare name: declare it as { name: "<x>" }',
	);
});

test("constraint: the record-shape sweep is set-wide and precedes the arity count", () => {
	// §26.8's pinned order (item 293, as corrected by item 299): the
	// member-record-shape checks run as a SET-WIDE step between steps 1 and 2,
	// so a bare-string member in a LATER constraint refuses ahead of an
	// EARLIER constraint's arity fault -- the framework has not agreed to read
	// entries whose shape it has not yet accepted.
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				constraints: [
					allOrNone(loose({ name: "one", members: [{ name: "aa" }] })),
					allOrNone(loose({ name: "two", members: ["aa", { name: "bb" }] })),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "two" member 0 is a bare name: declare it as { name: "<x>" }',
	);
});

test("constraint: member resolution -- unknown, ambiguous, duplicate", () => {
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa") },
				constraints: [
					allOrNone({
						name: "pair",
						members: [{ name: "aa" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "pair" references unknown member "bb"',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				args: [arg("aa", t.str, { help: "hh", presence: "optional" })],
				constraints: [
					allOrNone({
						name: "pair",
						members: [{ name: "aa" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "pair" references "aa", which names both a flag and a positional arg',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				constraints: [
					allOrNone({
						name: "pair",
						members: [{ name: "aa" }, { name: "bb" }, { name: "aa" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "pair" declares member "aa" twice',
	);
});

test("constraint: nesting legality -- family, election, cycles", () => {
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: boolFlag("aa"), bb: boolFlag("bb") },
				constraints: [
					requires({ name: "dep", flag: "aa", dependsOn: "bb" }),
					atLeastOne({
						name: "top",
						members: [{ name: "dep" }, { name: "aa", when: "true" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "top" references constraint "dep", which declares a one-way dependency rather than a co-occurrence rule: only at-least-one and all-or-none can be members of another constraint',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				constraints: [
					allOrNone({
						name: "pair",
						members: [{ name: "aa" }, { name: "bb" }],
					}),
					atLeastOne({
						name: "top",
						members: [{ name: "pair", when: "present" }, { name: "aa" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "top" member "pair" is a constraint and cannot declare an election: a nested constraint is engaged when its own members are',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				constraints: [
					atLeastOne({
						name: "loop",
						members: [{ name: "aa" }, { name: "loop" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraints form a cycle: loop -> loop',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				constraints: [
					atLeastOne({
						name: "one",
						members: [{ name: "aa" }, { name: "two" }],
					}),
					allOrNone({
						name: "two",
						members: [{ name: "bb" }, { name: "one" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraints form a cycle: one -> two -> one',
	);
});

test("constraint: election legality -- the bool refusal and the two type guards", () => {
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { all: boolFlag("all"), bb: optStrFlag("bb") },
				constraints: [
					atLeastOne({
						name: "sel",
						members: [{ name: "all" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "sel" member \'--all\' is a bool and must declare its election: when: "true" counts only a true value, when: "present" counts any',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				constraints: [
					atLeastOne({
						name: "sel",
						members: [{ name: "aa", when: "true" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "sel" member \'--aa\' declares when: "true", which needs a bool; \'--aa\' is a str',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: {
					nn: flag("nn", t.int, { help: "hh", presence: "optional" }),
					bb: optStrFlag("bb"),
				},
				constraints: [
					atLeastOne({
						name: "sel",
						members: [{ name: "nn", when: "non_empty" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "sel" member \'--nn\' declares when: "non_empty", which needs a string or a collection; \'--nn\' is a int',
	);
	// A scalar bool ARG reaches the same refusal, and renders BARE (§12.15's
	// member rule is all four member-naming guards').
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { bb: optStrFlag("bb") },
				args: [arg("force-it", t.bool, { help: "hh", presence: "optional" })],
				constraints: [
					atLeastOne({
						name: "sel",
						members: [{ name: "force-it" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "sel" member \'force-it\' is a bool and must declare its election: when: "true" counts only a true value, when: "present" counts any',
	);
});

test("constraint: `<t>` is the framework's own closed vocabulary", () => {
	// §12.15's pinned set: the four scalar words, the collection spellings
	// `list[<elem>]` and `dict[<value>]` formed from them, and `choice flag`.
	// A dict renders its VALUE type in ONE argument -- the key type is `str`
	// by construction, so the carrier's `dict[str,<v>]` spelling is a carrier
	// spelling and never reaches a message.
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: {
					labels: flag("labels", t.dict(t.int), {
						help: "hh",
						presence: "optional",
					}),
					bb: optStrFlag("bb"),
				},
				constraints: [
					atLeastOne({
						name: "sel",
						members: [{ name: "labels", when: "true" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "sel" member \'--labels\' declares when: "true", which needs a bool; \'--labels\' is a dict[int]',
	);
	// A repeatable flag holds a sequence, so it renders its COLLECTION
	// spelling: the word names the value the selector would be evaluated
	// against.
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: {
					tags: flag("tags", t.list(t.str), {
						help: "hh",
						presence: "optional",
						repeatable: true,
					}),
					bb: optStrFlag("bb"),
				},
				constraints: [
					atLeastOne({
						name: "sel",
						members: [{ name: "tags", when: "true" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "sel" member \'--tags\' declares when: "true", which needs a bool; \'--tags\' is a list[str]',
	);
	// A variadic arg is the same fact on the arg side, and it renders BARE in
	// both positions of the sentence (§12.15's member rule, all four guards).
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { bb: optStrFlag("bb") },
				args: [
					arg("targets", t.str, {
						help: "hh",
						variadic: true,
						presence: "optional",
					}),
				],
				constraints: [
					atLeastOne({
						name: "sel",
						members: [{ name: "targets", when: "true" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "sel" member \'targets\' declares when: "true", which needs a bool; \'targets\' is a list[str]',
	);
	// `list[bool]` is the one shape that reaches the word: a variadic bool arg,
	// which classifies as SIZED and never as bool (§26.3), so `true` is what
	// the sentence refuses rather than what it demands.
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { bb: optStrFlag("bb") },
				args: [
					arg("flags", t.bool, {
						help: "hh",
						variadic: true,
						presence: "optional",
					}),
				],
				constraints: [
					atLeastOne({
						name: "sel",
						members: [{ name: "flags", when: "true" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "sel" member \'flags\' declares when: "true", which needs a bool; \'flags\' is a list[bool]',
	);
	// A non-variadic arg keeps its scalar word.
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { bb: optStrFlag("bb") },
				args: [arg("count", t.int, { help: "hh", presence: "optional" })],
				constraints: [
					atLeastOne({
						name: "sel",
						members: [{ name: "count", when: "non_empty" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "sel" member \'count\' declares when: "non_empty", which needs a string or a collection; \'count\' is a int',
	);
	// A token-spelled selector names its CONSTRUCT: its value is a record
	// neither `true` nor `non_empty` can test.
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: {
					via: choiceFlag(
						"via",
						{ email: choice({ help: "email" }), sms: choice({ help: "sms" }) },
						{ help: "hh", presence: "required" },
					),
					bb: optStrFlag("bb"),
				},
				constraints: [
					atLeastOne({
						name: "sel",
						members: [{ name: "via", when: "true" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "sel" member \'--via\' declares when: "true", which needs a bool; \'--via\' is a choice flag',
	);
});

test("constraint: a variadic bool arg is sized, so it declares no election", () => {
	// §26.3's mandatory-election refusal has no referent on a variadic arg:
	// its value is a sequence whatever its element type, it has no `--no-`
	// spelling, and no way to be provided while selecting nothing.
	assert.ok(
		defineReadOnlyCommand("cmd", {
			help: "hh",
			flags: { bb: optStrFlag("bb") },
			args: [
				arg("flags", t.bool, {
					help: "hh",
					variadic: true,
					presence: "optional",
				}),
			],
			constraints: [
				atLeastOne({
					name: "sel",
					members: [{ name: "flags" }, { name: "bb" }],
				}),
			],
			handler: () => 0,
		}),
	);
	// And `non_empty` is legal on it, which is what `sized` means.
	assert.ok(
		defineReadOnlyCommand("cmd2", {
			help: "hh",
			flags: { bb: optStrFlag("bb") },
			args: [
				arg("flags", t.bool, {
					help: "hh",
					variadic: true,
					presence: "optional",
				}),
			],
			constraints: [
				atLeastOne({
					name: "sel",
					members: [{ name: "flags", when: "non_empty" }, { name: "bb" }],
				}),
			],
			handler: () => 0,
		}),
	);
});

test("constraint: presence legality -- a required member is refused, flag and arg alike", () => {
	// §26.5's INVERSION: the shipped exempt-from-required rule has no referent
	// after the presence round, and what survives is a refusal. Membership
	// neither makes a flag required nor exempts it from being required.
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: strFlag("aa"), bb: optStrFlag("bb") },
				constraints: [
					allOrNone({
						name: "pair",
						members: [{ name: "aa" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "pair" member \'--aa\' declares presence: "required": a member the invocation must always supply leaves the constraint nothing to decide',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { bb: optStrFlag("bb") },
				args: [arg("targets", t.str, { help: "hh", presence: "required" })],
				constraints: [
					atLeastOne({
						name: "sel",
						members: [{ name: "targets" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "sel" member \'targets\' declares presence: "required": a member the invocation must always supply leaves the constraint nothing to decide',
	);
});

test("constraint: the resolution order runs identity-outward across the whole set", () => {
	// §26.8: name legality for every constraint before member arity for any,
	// so a message never blames a member for a fault in the constraint that
	// names it.
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: optStrFlag("aa"), bb: optStrFlag("bb") },
				constraints: [
					allOrNone(loose({ name: "first", members: [{ name: "aa" }] })),
					allOrNone({
						name: "Second",
						members: [{ name: "aa" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint name "Second" must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*',
	);
});

test("constraint: the dependency families' own guards are a TRAILING phase", () => {
	// §26.8: the four legacy Requires/Implies guards run after all seven
	// co-occurrence steps, not folded into step 6. A required member is a
	// fault inside the seven, so it is reported before an Implies whose
	// trigger is not a bool.
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: {
					src: optStrFlag("src"),
					loud: boolFlag("loud"),
					aa: strFlag("aa"),
					bb: optStrFlag("bb"),
				},
				constraints: [
					implies({ name: "imp", flag: "src", implies: "loud", value: true }),
					allOrNone({
						name: "pair",
						members: [{ name: "aa" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "pair" member \'--aa\' declares presence: "required": a member the invocation must always supply leaves the constraint nothing to decide',
	);
	// The same ordering for the bool-election refusal, which is step 6 itself.
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: {
					src: optStrFlag("src"),
					loud: boolFlag("loud"),
					quietish: boolFlag("quietish"),
					bb: optStrFlag("bb"),
				},
				constraints: [
					implies({ name: "imp", flag: "src", implies: "loud", value: true }),
					allOrNone({
						name: "pair",
						members: [{ name: "quietish" }, { name: "bb" }],
					}),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "pair" member \'--quietish\' is a bool and must declare its election: when: "true" counts only a true value, when: "present" counts any',
	);
});

test("constraint: Requires reference validation (unknown reported before same-flag)", () => {
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				constraints: [requires({ name: "dep", flag: "aa", dependsOn: "aa" })],
				handler: () => 0,
			}),
		'command "cmd": constraint "dep" references unknown flag "aa"',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: strFlag("aa") },
				constraints: [requires({ name: "dep", flag: "aa", dependsOn: "aa" })],
				handler: () => 0,
			}),
		'command "cmd": Requires flag and depends_on cannot be the same ("aa")',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: strFlag("aa") },
				constraints: [requires({ name: "dep", flag: "aa", dependsOn: "bb" })],
				handler: () => 0,
			}),
		'command "cmd": constraint "dep" references unknown flag "bb"',
	);
});

test("constraint: Implies reference validation", () => {
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				constraints: [
					implies({ name: "imp", flag: "aa", implies: "bb", value: true }),
				],
				handler: () => 0,
			}),
		'command "cmd": constraint "imp" references unknown flag "aa"',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: boolFlag("aa") },
				constraints: [
					implies({ name: "imp", flag: "aa", implies: "aa", value: true }),
				],
				handler: () => 0,
			}),
		'command "cmd": Implies flag and implies cannot be the same ("aa")',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: strFlag("aa"), bb: boolFlag("bb") },
				constraints: [
					implies({ name: "imp", flag: "aa", implies: "bb", value: true }),
				],
				handler: () => 0,
			}),
		'command "cmd": Implies trigger flag "aa" must be a bool flag',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: boolFlag("aa"), bb: strFlag("bb") },
				constraints: [
					implies({ name: "imp", flag: "aa", implies: "bb", value: true }),
				],
				handler: () => 0,
			}),
		'command "cmd": Implies target flag "bb" must be a bool flag',
	);
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				flags: { aa: boolFlag("aa"), bb: boolFlag("bb") },
				constraints: [
					implies(loose({ name: "imp", flag: "aa", implies: "bb", value: 5n })),
				],
				handler: () => 0,
			}),
		"command \"cmd\": Implies value must be a bool, got 'int'",
	);
});

test("command: tag name validation and dedup", () => {
	rejects(
		() =>
			defineReadOnlyCommand("cmd", {
				help: "hh",
				tags: ["Bad"],
				handler: () => 0,
			}),
		'invalid tag name "Bad": must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*',
	);
	const cmd = defineReadOnlyCommand("cmd", {
		help: "hh",
		tags: ["bb", "aa", "bb"],
		handler: () => 0,
	});
	assert.deepEqual(cmd.tags, ["bb", "aa"]);
});

// --- createApp validation ---

function makeApp(extra?: Partial<Parameters<typeof createApp>[0]>): AppImpl {
	return createApp({
		name: "myapp",
		version: "1.0.0",
		help: "my cool app",
		...extra,
	}) as AppImpl;
}

test("createApp: version and help are required non-empty", () => {
	rejects(
		() => createApp({ name: "myapp", version: " ", help: "hh" }),
		"App.version must be a non-empty string",
	);
	rejects(
		() => createApp(loose({ name: "myapp", help: "hh" })),
		"App.version must be a non-empty string",
	);
	rejects(
		() => createApp({ name: "myapp", version: "1.0.0", help: " " }),
		"App.help must be a non-empty string",
	);
});

test("createApp: the reserved global flag names are rejected", () => {
	for (const name of [
		"help",
		"version",
		"dump-schema",
		"mcp",
		"config",
		"hermetic",
		"lint-framework-use",
	]) {
		const key = name.replaceAll("-", "_");
		rejects(
			() => makeApp({ flags: { [key]: strFlag(name) } }),
			`global flag name "${name}" is reserved`,
		);
	}
});

test("createApp: reserved global short flags are rejected", () => {
	rejects(
		() =>
			makeApp({
				flags: {
					muted: flag("muted", t.bool, {
						help: "hh",
						short: "v",
						presence: "default",
						default: false,
					}),
				},
			}),
		'global short flag "v" is reserved',
	);
});

test("createApp: global flag map keys must be underscore forms", () => {
	rejects(
		() =>
			makeApp({
				flags: {
					simRun: flag("sim-run", t.bool, {
						help: "hh",
						presence: "default",
						default: false,
					}),
				},
			}),
		"App.flags key 'simRun' must be the underscore form of flag 'sim-run' ('sim_run')",
	);
});

test("createApp: handshake env var validation", () => {
	rejects(
		() => makeApp({ handshakeEnv: { MY_VAR: "  " } }),
		'handshake env var "MY_VAR": help must be a non-empty string',
	);
	rejects(
		() =>
			makeApp({
				infraRoot: { MY_ROOT: "~/x" },
				handshakeEnv: { MY_ROOT: "hello" },
			}),
		'handshake env var "MY_ROOT" is already declared as an infra root',
	);
});

test("createApp: infra roots resolve eagerly (env override, tilde expansion)", () => {
	process.env.STRICTCLI_TS_REG_TEST_ROOT = "~/from-env";
	try {
		const app = makeApp({
			infraRoot: {
				STRICTCLI_TS_REG_TEST_ROOT: "/unused-default",
				STRICTCLI_TS_REG_TEST_OTHER: "~/other-root",
			},
		});
		assert.equal(
			app.infraRoots.get("STRICTCLI_TS_REG_TEST_ROOT"),
			join(homedir(), "from-env"),
		);
		assert.equal(app.infraRootFromEnv.get("STRICTCLI_TS_REG_TEST_ROOT"), true);
		assert.equal(
			app.infraRoots.get("STRICTCLI_TS_REG_TEST_OTHER"),
			join(homedir(), "other-root"),
		);
		assert.equal(
			app.infraRootFromEnv.get("STRICTCLI_TS_REG_TEST_OTHER"),
			false,
		);
		assert.equal(
			app.infraRootDefaults.get("STRICTCLI_TS_REG_TEST_ROOT"),
			"/unused-default",
		);
	} finally {
		delete process.env.STRICTCLI_TS_REG_TEST_ROOT;
	}
});

// --- App-level registration ---

test("app.command: command flags may not collide with global flags", () => {
	const app = makeApp({ flags: { chatter: boolFlag("chatter") } });
	rejects(
		() =>
			app.command(
				defineReadOnlyCommand("cmd", {
					help: "hh",
					flags: { chatter: boolFlag("chatter") },
					handler: () => 0,
				}),
			),
		'command "cmd": flag "chatter" collides with a global flag',
	);
});

test("app.command: env prefix enforcement", () => {
	const app = makeApp({ envPrefix: "MYAPP" });
	rejects(
		() =>
			app.command(
				defineReadOnlyCommand("cmd", {
					help: "hh",
					flags: {
						target: flag("target", t.str, {
							help: "hh",
							env: "TGT",
							presence: "required",
						}),
					},
					handler: () => 0,
				}),
			),
		'command "cmd": env var "TGT" for flag "target" must start with "MYAPP_" (or set prefixed=false)',
	);
	// prefixed: false opts out; a conforming prefix passes.
	app.command(
		defineReadOnlyCommand("ok", {
			help: "hh",
			flags: {
				target: flag("target", t.str, {
					help: "hh",
					env: "TGT",
					prefixed: false,
					presence: "required",
				}),
				output: flag("output", t.str, {
					help: "hh",
					env: "MYAPP_OUTPUT",
					presence: "required",
				}),
			},
			handler: () => 0,
		}),
	);
	assert.ok(app.commands.has("ok"));
});

test("app: registration order is preserved (commands, groups, global flags)", () => {
	const app = makeApp({
		flags: { zeta: strFlag("zeta"), alpha: strFlag("alpha") },
	});
	app.command(defineReadOnlyCommand("bravo", { help: "hh", handler: () => 0 }));
	app.command(defineReadOnlyCommand("alpha", { help: "hh", handler: () => 0 }));
	app.command(readOnlyPassthrough("zulu", { help: "hh", handler: () => 0 }));
	app.group("mike", { help: "hh" });
	app.group("kilo", { help: "hh" });
	assert.deepEqual([...app.commands.keys()], ["bravo", "alpha", "zulu"]);
	assert.deepEqual([...app.groups.keys()], ["mike", "kilo"]);
	assert.deepEqual(
		app.globalFlags.map((f) => f.name),
		["zeta", "alpha"],
	);
});

test("app: top-level re-registration overwrites in place (sibling parity)", () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", { help: "first", handler: () => 0 }),
	);
	app.command(defineReadOnlyCommand("other", { help: "hh", handler: () => 0 }));
	app.command(
		defineReadOnlyCommand("cmd", { help: "second", handler: () => 0 }),
	);
	assert.deepEqual([...app.commands.keys()], ["cmd", "other"]);
	assert.equal(app.commands.get("cmd")?.help, "second");
});

test("app.command: merged declaration order is flags, then flag sets", () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("deploy", {
			help: "hh",
			flags: {
				region: strFlag("region"),
				source: memberChoiceFlag(
					"source",
					{
						"from-file": choice({
							help: "hh",
							value: { carrier: t.str, help: "path to the file" },
						}),
						"from-url": choice({
							help: "hh",
							value: { carrier: t.str, help: "the URL to read" },
						}),
					},
					{ help: "hh", presence: "required" },
				),
			},
			flagSets: [flagSet("common", { chatter: boolFlag("chatter") })],
			handler: () => 0,
		}),
	);
	const reg = app.commands.get("deploy");
	assert.ok(reg);
	// A selector contributes ONE entry, its own: its choices' flags are
	// reachable only through it (§24.1).
	assert.deepEqual(
		(reg.def as { allDecls: readonly { name: string }[] }).allDecls.map(
			(d) => d.name,
		),
		["region", "source", "chatter"],
	);
	assert.deepEqual(
		reg.flags.map((f) => f.name),
		["region", "chatter"],
	);
});

// --- Groups ---

test("group: help and tag validation", () => {
	const app = makeApp();
	rejects(
		() => app.group("dns", { help: " " }),
		"Group.help must be a non-empty string",
	);
	rejects(
		() => app.group("dns", { help: "hh", tags: ["Bad"] }),
		'invalid tag name "Bad": must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*',
	);
});

test("group: nested collision checks", () => {
	const app = makeApp();
	const dns = app.group("dns", { help: "DNS tools" });
	dns.group("zone", { help: "Zone tools" });
	rejects(
		() => dns.group("zone", { help: "again" }),
		'group "zone" is already registered',
	);
	rejects(
		() =>
			dns.command(
				defineReadOnlyCommand("zone", { help: "hh", handler: () => 0 }),
			),
		'command "zone" collides with an existing group',
	);
	dns.command(defineReadOnlyCommand("list", { help: "hh", handler: () => 0 }));
	rejects(
		() => dns.group("list", { help: "hh" }),
		'group "list" collides with an existing command',
	);
});

test("group: arbitrary nesting depth with sorted tag accumulation", () => {
	const app = makeApp();
	const dns = app.group("dns", { help: "DNS", tags: ["net"] });
	const zone = dns.group("zone", {
		help: "Zones",
		tags: ["zone-ops", "alpha"],
	});
	const record = zone.group("record", { help: "Records" });
	record.command(
		defineReadOnlyCommand("create", {
			help: "hh",
			tags: ["beta"],
			handler: () => 0,
		}),
	);
	const dnsImpl = app.groups.get("dns");
	assert.ok(dnsImpl);
	const zoneImpl = dnsImpl.groups.get("zone");
	assert.ok(zoneImpl);
	const recordImpl = zoneImpl.groups.get("record");
	assert.ok(recordImpl);
	assert.deepEqual(recordImpl.accumulatedTags, ["alpha", "net", "zone-ops"]);
	assert.deepEqual(recordImpl.commands.get("create")?.tags, [
		"alpha",
		"beta",
		"net",
		"zone-ops",
	]);
});

test("group: hidden and tags are stored", () => {
	const app = makeApp();
	const g = app.group("internal", {
		help: "hh",
		hidden: true,
		tags: ["bb", "aa"],
	});
	assert.equal(g.hidden, true);
	assert.deepEqual(g.tags, ["bb", "aa"]);
});

// --- Deprecated commands ---

test("deprecated: factory validates name and message", () => {
	rejects(
		() => deprecated(" ", "use other"),
		"deprecated command name must be a non-empty string",
	);
	rejects(
		() => deprecated("old-cmd", "  "),
		'deprecated command "old-cmd": message must not be empty',
	);
});

test("deprecate: collision checks at app and group level", () => {
	const app = makeApp();
	app.command(defineReadOnlyCommand("cmd", { help: "hh", handler: () => 0 }));
	app.group("grp", { help: "hh" });
	rejects(
		() => app.deprecate(deprecated("cmd", "use other")),
		'deprecated command "cmd" collides with an existing command',
	);
	rejects(
		() => app.deprecate(deprecated("grp", "use other")),
		'deprecated command "grp" collides with an existing group',
	);
	app.deprecate(deprecated("old-cmd", "use 'cmd' instead"));
	rejects(
		() => app.deprecate(deprecated("old-cmd", "again")),
		'deprecated command "old-cmd" is already registered',
	);
	assert.equal(app.deprecated.get("old-cmd"), "use 'cmd' instead");

	const grp = app.groups.get("grp");
	assert.ok(grp);
	grp.command(defineReadOnlyCommand("sub", { help: "hh", handler: () => 0 }));
	rejects(
		() => grp.deprecate(deprecated("sub", "gone")),
		'deprecated command "sub" collides with an existing command',
	);
	grp.deprecate(deprecated("old-sub", "gone"));
	assert.equal(grp.deprecated.get("old-sub"), "gone");
});

// --- Tag contracts ---

test("tagContract: validates the tag name and stores the contract", () => {
	const app = makeApp();
	rejects(
		() => app.tagContract("Bad", "sim-run"),
		'invalid tag name "Bad": must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*',
	);
	app.tagContract("release", "sim-run");
	assert.equal(app.tagContracts.get("release"), "sim-run");
});

// --- Integration: inference flows through app.command(defineReadOnlyCommand(...)) ---

type Equals<A, B> =
	(<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2
		? true
		: false;
type Assert<T extends true> = T;

const deployCmd = defineReadOnlyCommand("deploy", {
	help: "Deploy the service",
	flags: {
		region: flag("region", t.str, {
			help: "Region",
			choices: [{ value: "eu" }, { value: "us" }],
			presence: "default",
			default: "eu",
		}),
		replicas: flag("replicas", t.int, {
			help: "Replica count",
			presence: "required",
		}),
	},
	flagSets: [
		flagSet("common", {
			chatter: flag("chatter", t.bool, {
				help: "Chatter",
				presence: "default",
				default: false,
			}),
		}),
	],
	args: [arg("service", t.str, { help: "Service name", presence: "required" })],
	constraints: [
		requires({ name: "scale", flag: "replicas", dependsOn: "region" }),
	],
	handler: (args) => {
		type _Args = Assert<
			Equals<
				typeof args,
				{
					region: string;
					replicas: bigint;
					chatter: boolean;
					service: string;
				}
			>
		>;
		return args.chatter ? Number(args.replicas) : 0;
	},
});

test("integration: precisely-typed command registers with derived data intact", () => {
	const app = makeApp();
	app.command(deployCmd);
	const reg = app.commands.get("deploy");
	assert.ok(reg);
	assert.equal(reg.kind, "command");
	assert.deepEqual(
		reg.flags.map((f) => f.name),
		["region", "replicas", "chatter"],
	);
	assert.deepEqual(reg.tags, []);
});

// --- The presence declaration (contract §23.1, §12.12) ---
// Every message here is byte-exact: the sentence is shared across the three
// implementations and the spellings inside it are TypeScript's own.

test("presence: declaring nothing does not register", () => {
	rejects(
		() => flag("target", t.str, loose({ help: "hh" })),
		'Flag "target": presence is undeclared: declare exactly one of presence: "required", presence: "optional", or presence: "default" with default: <value>',
	);
	rejects(
		() => arg("src", t.str, loose({ help: "hh" })),
		'Arg "src": presence is undeclared: declare exactly one of presence: "required", presence: "optional", or presence: "default" with default: <value>',
	);
	// A presence value outside the closed set declares none of the three.
	rejects(
		() => flag("target", t.str, loose({ help: "hh", presence: "maybe" })),
		'Flag "target": presence is undeclared: declare exactly one of presence: "required", presence: "optional", or presence: "default" with default: <value>',
	);
});

test("presence: declaring two does not register, in canonical order", () => {
	rejects(
		() =>
			flag(
				"target",
				t.str,
				loose({ help: "hh", presence: "required", default: "xx" }),
			),
		'Flag "target": presence is declared twice: presence: "required" and presence: "default" with default: xx cannot be combined; declare exactly one',
	);
	// Written default-first; the message still renders required/optional first.
	rejects(
		() =>
			flag(
				"target",
				t.str,
				loose({ default: "xx", presence: "optional", help: "hh" }),
			),
		'Flag "target": presence is declared twice: presence: "optional" and presence: "default" with default: xx cannot be combined; declare exactly one',
	);
	rejects(
		() =>
			arg(
				"src",
				t.str,
				loose({ help: "hh", presence: "required", default: "xx" }),
			),
		'Arg "src": presence is declared twice: presence: "required" and presence: "default" with default: xx cannot be combined; declare exactly one',
	);
	rejects(
		() =>
			flag(
				"count",
				t.int,
				loose({ help: "hh", presence: "optional", default: 5n }),
			),
		'Flag "count": presence is declared twice: presence: "optional" and presence: "default" with default: 5 cannot be combined; declare exactly one',
	);
});

test("presence: a null-valued default redirects to the optional spelling", () => {
	// The redirect fires when the null default is the SOLE declaration -- the
	// old idiom it exists to teach (ledger item 154).
	rejects(
		() => flag("target", t.str, loose({ help: "hh", default: null })),
		'Flag "target": default: null does not declare optionality: use presence: "optional" (it delivers undefined when the flag is absent)',
	);
	rejects(
		() => arg("src", t.str, loose({ help: "hh", default: null })),
		'Arg "src": default: null does not declare optionality: use presence: "optional" (it delivers undefined when the arg is absent)',
	);
	// The two-part default spelling carrying null is still ONE declaration, so
	// it redirects too rather than reading as a combination.
	rejects(
		() =>
			flag(
				"target",
				t.str,
				loose({ help: "hh", presence: "default", default: null }),
			),
		'Flag "target": default: null does not declare optionality: use presence: "optional" (it delivers undefined when the flag is absent)',
	);
	rejects(
		() =>
			arg(
				"src",
				t.str,
				loose({ help: "hh", presence: "default", default: null }),
			),
		'Arg "src": default: null does not declare optionality: use presence: "optional" (it delivers undefined when the arg is absent)',
	);
});

test("presence: the two-declared error wins over the null-default redirect", () => {
	// The count check runs first (§12.12's implementation-sweep box, ledger
	// item 154): a null default written BESIDE a presence declaration is a
	// combination error naming both spellings, not a redirect. `default?: never`
	// refuses the pairing at compile time, so only an untyped or JSON-driven
	// caller reaches it -- which the conformance harness is.
	rejects(
		() =>
			flag(
				"target",
				t.str,
				loose({ help: "hh", presence: "optional", default: null }),
			),
		'Flag "target": presence is declared twice: presence: "optional" and presence: "default" with default: null cannot be combined; declare exactly one',
	);
	rejects(
		() =>
			flag(
				"target",
				t.str,
				loose({ help: "hh", presence: "required", default: null }),
			),
		'Flag "target": presence is declared twice: presence: "required" and presence: "default" with default: null cannot be combined; declare exactly one',
	);
	rejects(
		() =>
			arg(
				"src",
				t.str,
				loose({ help: "hh", presence: "optional", default: null }),
			),
		'Arg "src": presence is declared twice: presence: "optional" and presence: "default" with default: null cannot be combined; declare exactly one',
	);
	rejects(
		() =>
			arg(
				"src",
				t.str,
				loose({ help: "hh", presence: "required", default: null }),
			),
		'Arg "src": presence is declared twice: presence: "required" and presence: "default" with default: null cannot be combined; declare exactly one',
	);
});

test('presence: "default" without a value does not register (TS-only)', () => {
	// No sibling can express a half-written default declaration: Python's
	// default=<value> and Go's Default(v) ARE the value.
	rejects(
		() => flag("target", t.str, loose({ help: "hh", presence: "default" })),
		'Flag "target": presence: "default" requires a default value: declare default: <value>, or presence: "optional" for no value',
	);
	rejects(
		() => arg("src", t.str, loose({ help: "hh", presence: "default" })),
		'Arg "src": presence: "default" requires a default value: declare default: <value>, or presence: "optional" for no value',
	);
});

test("presence: a selector cannot declare optional, and refuses with a redirect", () => {
	// The type union has no "optional" member, so only a widened caller
	// reaches the registration refusal (ruling B2 made structural, §24.5).
	rejects(
		() =>
			choiceFlag(
				"via",
				{ email: choice({ help: "email" }), sms: choice({ help: "sms" }) },
				loose({ help: "hh", presence: "optional" }),
			),
		'Flag "via": a choice flag cannot declare presence: "optional": an absent selection is a choice nobody named, so name it as a choice of its own',
	);
	// A member flag's presence is `required once this member is elected`, and
	// TypeScript's spelling has no slot for anything else -- a widened caller
	// writing one is refused (§12.13's errMemberFlagPresence).
	rejects(
		() =>
			memberChoiceFlag(
				"scope",
				{
					all: loose({ ...choice({ help: "hh" }), presence: "optional" }),
					one: choice({ help: "hh" }),
				},
				{ help: "hh", presence: "required" },
			),
		'Choice "all" of "scope": a member flag must declare presence: "required", read as required once this member is elected',
	);
});

test("presence: a variadic arg cannot declare a default", () => {
	rejects(
		() =>
			arg(
				"files",
				t.str,
				loose({
					help: "hh",
					variadic: true,
					presence: "default",
					default: "xx",
				}),
			),
		'Arg "files": a variadic arg cannot declare presence: "default": it always delivers a list, so declare presence: "required" for at least one value or presence: "optional" for possibly none',
	);
	// Both other declarations are legal on a variadic arg.
	assert.doesNotThrow(() =>
		arg("files", t.str, { help: "hh", variadic: true, presence: "required" }),
	);
	assert.doesNotThrow(() =>
		arg("files", t.str, { help: "hh", variadic: true, presence: "optional" }),
	);
});

test("presence: an optional flag composes with choices and never checks absence", () => {
	// The default-in-choices check applies to declared VALUES only (§23.5).
	assert.doesNotThrow(() =>
		flag("format", t.str, {
			help: "hh",
			presence: "optional",
			choices: [{ value: "text" }, { value: "json" }],
		}),
	);
	rejects(
		() =>
			flag("format", t.str, {
				help: "hh",
				presence: "default",
				default: "yaml",
				choices: [{ value: "text" }, { value: "json" }],
			}),
		"Flag \"format\": default 'yaml' is not in choices ['text', 'json']",
	);
});

// --- Type-level negative cases ---
// Never-invoked closures: only the compile errors are under test.

void [
	// @ts-expect-error version is a required createApp field
	() => createApp({ name: "xx", help: "hh" }),
	// @ts-expect-error help is a required createApp field
	() => createApp({ name: "xx", version: "1.0.0" }),
	(app: ReturnType<typeof createApp>) =>
		// @ts-expect-error deprecated carriers register via app.deprecate, not app.command
		app.command(deprecated("old", "gone")),
	() =>
		// @ts-expect-error repeatable is not available on scalar carriers
		flag("tag", t.str, { help: "hh", repeatable: true, presence: "required" }),
	() =>
		createApp({
			name: "xx",
			version: "1.0.0",
			help: "hh",
			// @ts-expect-error configFormat is a closed union
			configFormat: "yaml",
		}),
];
