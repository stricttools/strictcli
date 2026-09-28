import { strict as assert } from "node:assert";
import { test } from "node:test";
import {
	allOrNone,
	arg,
	atLeastOne,
	choice,
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

// --- Runtime: descriptor shapes ---

test("flag captures name, schema, and opts", () => {
	const f = flag("sim-run", t.bool, {
		help: "Dry run",
		presence: "default",
		default: true,
	});
	assert.equal(f.kind, "flag");
	assert.equal(f.name, "sim-run");
	assert.equal(f.schema, "bool");
	assert.equal(f.opts.help, "Dry run");
	assert.equal(f.opts.default, true);
});

test("flag registration errors match sibling messages", () => {
	assert.throws(
		() => flag("target", t.str, { help: "", presence: "required" }),
		{
			message: "Flag.help must be a non-empty string",
		},
	);
	assert.throws(
		() => flag("target", t.str, { help: "   ", presence: "required" }),
		{
			message: "Flag.help must be a non-empty string",
		},
	);
	assert.throws(
		() => flag("force", t.str, { help: "x", presence: "required" }),
		{
			message:
				"flag 'force' is a reserved name; use a qualified name like 'force-overwrite' or 'force-delete'",
		},
	);
	assert.throws(
		() =>
			flag("no-frame", t.bool, {
				help: "x",
				presence: "default",
				default: true,
			}),
		{
			message:
				"flag 'no-frame': names starting with 'no-' are reserved for the negation system; use a positive name instead",
		},
	);
});

test("arg registration errors match sibling messages", () => {
	assert.throws(() => arg("src", t.str, { help: "", presence: "required" }), {
		message: "Arg.help must be a non-empty string",
	});
	// The type system cannot excess-property-check generic constraints (spike
	// finding), so a widened option object still reaches the factory: the
	// two-declared error is what refuses it now that `required arg cannot have
	// a default` is deleted (contract §12.12).
	assert.throws(
		() =>
			arg("src", t.str, {
				help: "Source",
				presence: "required",
				default: "x",
			} as unknown as { help: string; presence: "required" }),
		{
			message:
				'Arg "src": presence is declared twice: presence: "required" and presence: "default" with default: x cannot be combined; declare exactly one',
		},
	);
});

test("constraint descriptors carry the mandatory name and the member records", () => {
	const alo = atLeastOne({
		name: "purge-selection",
		members: [{ name: "targets", when: "non_empty" }, { name: "all" }],
	});
	assert.deepEqual(alo, {
		kind: "at-least-one",
		name: "purge-selection",
		members: [{ name: "targets", when: "non_empty" }, { name: "all" }],
	});

	const aon = allOrNone({
		name: "author-name",
		members: [{ name: "old-name" }, { name: "new-name" }],
	});
	assert.deepEqual(aon, {
		kind: "all-or-none",
		name: "author-name",
		members: [{ name: "old-name" }, { name: "new-name" }],
	});

	const rq = requires({ name: "creds", flag: "password", dependsOn: "user" });
	assert.deepEqual(rq, {
		kind: "requires",
		name: "creds",
		flag: "password",
		dependsOn: "user",
	});

	const im = implies({
		name: "noise",
		flag: "debug",
		implies: "chatter",
		value: true,
	});
	assert.deepEqual(im, {
		kind: "implies",
		name: "noise",
		flag: "debug",
		implies: "chatter",
		value: true,
	});
});

test("flagSet and memberChoiceFlag hold keyed maps", () => {
	const common = flagSet("common", {
		chatter: flag("chatter", t.bool, {
			help: "Chatter",
			presence: "default",
			default: false,
		}),
	});
	assert.equal(common.kind, "flag-set");
	assert.equal(common.name, "common");
	assert.equal(common.flags.chatter.name, "chatter");

	const sel = memberChoiceFlag(
		"source",
		{
			file: choice({
				help: "From file",
				value: { carrier: t.str, help: "path to the file" },
			}),
			url: choice({
				help: "From URL",
				value: { carrier: t.str, help: "the URL to read" },
			}),
		},
		{ help: "Where to read from", presence: "required" },
	);
	assert.equal(sel.kind, "choice-flag");
	assert.equal(sel.electBy, "member-flags");
	// The payload is a carrier plus its own help, and both are readable off the
	// built descriptor: the schema dump publishes the pair.
	assert.equal(sel.choices.file.value?.carrier.schema, "str");
	assert.equal(sel.choices.file.value?.help, "path to the file");
});

test("defineReadOnlyCommand validates help, tags, and flag-map keys", () => {
	assert.throws(
		() => defineReadOnlyCommand("xx", { help: " ", handler: () => 0 }),
		{
			message: 'command "xx": missing help text',
		},
	);
	assert.throws(
		() =>
			defineReadOnlyCommand("xx", {
				help: "h",
				tags: ["Bad"],
				handler: () => 0,
			}),
		{
			message:
				'invalid tag name "Bad": must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*',
		},
	);
	assert.throws(
		() =>
			defineReadOnlyCommand("build", {
				help: "h",
				flags: {
					simRun: flag("sim-run", t.bool, {
						help: "h",
						presence: "default",
						default: false,
					}),
				},
				handler: () => 0,
			}),
		{
			message:
				"command \"build\": flags key 'simRun' must be the underscore form of flag 'sim-run' ('sim_run')",
		},
	);
});

test("passthrough and deprecated carriers", () => {
	const pt = readOnlyPassthrough("checkout", {
		help: "Forward to git checkout",
		handler: (args) => (args.args.length > 0 ? 0 : 1),
	});
	assert.equal(pt.kind, "passthrough");
	assert.equal(pt.name, "checkout");
	assert.equal(pt.hidden, false);

	const dep = deprecated("old-cmd", "use 'new-cmd' instead");
	assert.deepEqual(dep, {
		kind: "deprecated",
		name: "old-cmd",
		message: "use 'new-cmd' instead",
	});
	assert.throws(() => deprecated("old-cmd", "  "), {
		message: 'deprecated command "old-cmd": message must not be empty',
	});
});

// --- Type-level: per-carrier option typing ---
// Each case is wrapped in a never-invoked closure: the runtime validators now
// also reject these shapes, and only the compile error is under test here.

void [
	() =>
		flag("count", t.int, {
			help: "h",
			presence: "default",
			// @ts-expect-error int flags take bigint defaults, not number
			default: 5,
		}),
	() =>
		flag("tag", t.list(t.str), {
			help: "h",
			presence: "default",
			// @ts-expect-error list defaults are element arrays, not scalars
			default: "x",
		}),
	() =>
		flag("chatter", t.bool, {
			help: "h",
			presence: "default",
			default: false,
			// @ts-expect-error choices are incompatible with bool flags
			choices: [{ value: true }],
		}),
	() =>
		flag("target", t.str, {
			help: "h",
			// @ts-expect-error negatable is only meaningful for bool flags
			negatable: false,
			presence: "required",
		}),
	() =>
		flag("target", t.str, {
			help: "h",
			// @ts-expect-error unique requires a list carrier
			unique: true,
			presence: "required",
		}),
	() =>
		flag("meta", t.dict(t.int), {
			help: "h",
			// @ts-expect-error dict flags cannot use envSeparator (env vars are JSON)
			envSeparator: ",",
			presence: "default",
			default: new Map(),
		}),
	// @ts-expect-error dict carriers are not allowed on args
	() => arg("values", t.dict(t.int), { help: "h", presence: "required" }),
	// A list carrier on a NON-variadic arg is a registration error, not a type
	// error: only a variadic arg delivers a list, and the type system cannot
	// see which way `variadic` was declared here (§25.4).
	() =>
		flag("level", t.int, {
			help: "h",
			// @ts-expect-error choices elements must match the carrier's value type
			choices: [{ value: 1 }, { value: 2 }],
			presence: "required",
		}),
];

// --- Type-level: the presence union (contract §23.2) ---
// A `default` outside the "default" member does not type-check, the "default"
// member's `default` is not optional, and a declaration with no presence at
// all does not type-check either.

void [
	() =>
		// @ts-expect-error a required flag cannot carry a default value
		flag("target", t.str, { help: "h", presence: "required", default: "x" }),
	() =>
		// @ts-expect-error an optional flag cannot carry a default value
		flag("target", t.str, { help: "h", presence: "optional", default: "x" }),
	// @ts-expect-error presence: "default" without a default value is incomplete
	() => flag("target", t.str, { help: "h", presence: "default" }),
	// @ts-expect-error a flag that declares no presence does not register
	() => flag("target", t.str, { help: "h" }),
	() =>
		// @ts-expect-error null is not a spelling of optionality
		flag("target", t.str, { help: "h", presence: "optional", default: null }),
	() =>
		// @ts-expect-error a required arg cannot carry a default value
		arg("src", t.str, { help: "h", presence: "required", default: "x" }),
	// @ts-expect-error presence: "default" without a default value is incomplete
	() => arg("src", t.str, { help: "h", presence: "default" }),
	// @ts-expect-error an arg that declares no presence does not register
	() => arg("src", t.str, { help: "h" }),
	() =>
		// @ts-expect-error a variadic arg cannot declare a default
		arg("src", t.str, {
			help: "h",
			presence: "default",
			default: "x",
			variadic: true,
		}),
];
