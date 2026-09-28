/**
 * Registration-time enforcement of the effects regime: the reserved flag
 * quartet's unconditional name ban, mandatory classification through the twin
 * factories, grant and forwarding declarations, and the framework-internal
 * marker's module verification.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import {
	type AppImpl,
	createApp,
	defineFrameworkCommand,
	FRAMEWORK_INTERNAL_FORWARDING_REASON,
	markFrameworkHandler,
} from "../src/app.js";
import { RegistrationError } from "../src/errors.js";
import type { AnyCommand } from "../src/factories.js";
import {
	arg,
	choice,
	defineMutatingCommand,
	defineReadOnlyCommand,
	deprecated,
	flag,
	flagSet,
	memberChoiceFlag,
	mutatingPassthrough,
	readOnlyPassthrough,
	t,
} from "../src/index.js";

const RESERVED = ["dry-run", "approve-consequential", "quiet", "verbose"];

function reservedMessage(name: string): string {
	return `flag name '${name}' is reserved by the framework (dry-run, approve-consequential, quiet, verbose)`;
}

// `yes` owns no framework flag any more, but it stays banned so nobody
// reintroduces a private --yes meaning the same thing (§12.1).
const YES_BAN_MESSAGE =
	"flag name 'yes' is banned by the framework: the confirmation skip is --approve-consequential";

// --- §7.1 the unconditional name ban ---

test("reserved quartet: flag() refuses each reserved name", () => {
	for (const name of RESERVED) {
		assert.throws(
			() =>
				flag(name, t.bool, { help: "hh", presence: "default", default: false }),
			{
				name: "RegistrationError",
				message: reservedMessage(name),
			},
		);
	}
});

test("reserved names: `yes` is banned outright", () => {
	assert.throws(
		() =>
			flag("yes", t.bool, { help: "hh", presence: "default", default: false }),
		{
			name: "RegistrationError",
			message: YES_BAN_MESSAGE,
		},
	);
	assert.throws(
		() =>
			createApp({
				name: "tt",
				version: "1",
				help: "hh",
				flags: {
					yes: flag("yes", t.bool, {
						help: "hh",
						presence: "default",
						default: false,
					}),
				},
			}),
		{ message: YES_BAN_MESSAGE },
	);
});

test("reserved quartet: the ban applies at every level, not just globals", () => {
	// Command flags, flag-set flags and mutex-group flags all go through the
	// same flag() factory, so the ban is unconditional by construction.
	for (const name of RESERVED) {
		assert.throws(() =>
			flag(name, t.bool, { help: "hh", presence: "default", default: false }),
		);
	}
	// A flag set and a mutex group cannot even be built with one.
	assert.throws(() =>
		flagSet("common", {
			quiet: flag("quiet", t.bool, {
				help: "hh",
				presence: "default",
				default: false,
			}),
		}),
	);
	// The bans re-run at every depth, on a member-spelled choice name too: a
	// member's name IS a flag name (contract §24.7).
	assert.throws(() =>
		memberChoiceFlag(
			"consent",
			{
				yes: choice({ help: "hh" }),
				no_thanks: choice({ help: "hh" }),
			},
			{ help: "hh", presence: "required" },
		),
	);
});

test("reserved quartet: the global-flag path carries the same message", () => {
	// flag() bans the name first, so the global path is reached only by an
	// untyped caller forging a descriptor.
	const forged = {
		kind: "flag" as const,
		name: "verbose",
		schema: "bool" as const,
		carrier: t.bool,
		opts: { help: "hh", presence: "default" as const, default: false },
	};
	assert.throws(
		() =>
			createApp({
				name: "tt",
				version: "1",
				help: "hh",
				flags: { verbose: forged },
			}),
		{ message: reservedMessage("verbose") },
	);
});

test("reserved quartet: --output is explicitly NOT reserved", () => {
	assert.doesNotThrow(() =>
		flag("output", t.str, {
			help: "where to write",
			presence: "default",
			default: "-",
		}),
	);
});

test("reserved quartet: short names are unaffected by the ban", () => {
	assert.doesNotThrow(() =>
		flag("quietly", t.bool, {
			help: "hh",
			short: "q",
			presence: "default",
			default: false,
		}),
	);
});

test("reserved quartet: arg names are unaffected (an arg has no -- spelling)", () => {
	assert.doesNotThrow(() =>
		arg("verbose", t.str, { help: "hh", presence: "required" }),
	);
});

// --- §7.1 the consent PARAMETER name, reserved on both surfaces ---

const CONSENT_FLAG_BAN =
	"flag name 'approve_consequential' is reserved by the framework: it names the programmatic consent parameter";
const CONSENT_ARG_BAN =
	"arg name 'approve_consequential' is reserved by the framework: it names the programmatic consent parameter";

test("consent parameter: flag() refuses the underscore spelling", () => {
	assert.throws(
		() =>
			flag("approve_consequential", t.bool, {
				help: "hh",
				presence: "default",
				default: false,
			}),
		{ name: "RegistrationError", message: CONSENT_FLAG_BAN },
	);
	assert.throws(
		() =>
			flag("approve_consequential", t.str, {
				help: "hh",
				presence: "required",
			}),
		{
			message: CONSENT_FLAG_BAN,
		},
	);
});

test("consent parameter: arg() refuses it too", () => {
	assert.throws(
		() =>
			arg("approve_consequential", t.str, { help: "hh", presence: "required" }),
		{
			name: "RegistrationError",
			message: CONSENT_ARG_BAN,
		},
	);
});

test("consent parameter: only that one name reaches the arg surface", () => {
	assert.doesNotThrow(() =>
		arg("approve", t.str, { help: "hh", presence: "required" }),
	);
	assert.doesNotThrow(() =>
		arg("approve-consequential", t.str, { help: "hh", presence: "required" }),
	);
});

// --- §1.1 mandatory classification ---

test("classification: the twin factories splice in the effect", () => {
	assert.equal(
		defineReadOnlyCommand("aa", { help: "hh", handler: () => 0 }).effect,
		"read_only",
	);
	assert.equal(
		defineMutatingCommand("bb", { help: "hh", handler: () => 0 }).effect,
		"mutating",
	);
	assert.equal(
		readOnlyPassthrough("cc", { help: "hh", handler: () => 0 }).effect,
		"read_only",
	);
	assert.equal(
		mutatingPassthrough("dd", { help: "hh", handler: () => 0 }).effect,
		"mutating",
	);
});

test("classification: a carrier with no effect is a registration error", () => {
	const app = createApp({ name: "tt", version: "1", help: "hh" });
	const forged = {
		...defineReadOnlyCommand("xx", { help: "hh", handler: () => 0 }),
		effect: undefined,
	} as unknown as AnyCommand;
	assert.throws(() => app.command(forged), {
		name: "RegistrationError",
		message:
			'command "xx": effect classification is required (effect="read_only" or effect="mutating")',
	});
});

test("classification: an invalid effect is a registration error", () => {
	const app = createApp({ name: "tt", version: "1", help: "hh" });
	const forged = {
		...defineReadOnlyCommand("xx", { help: "hh", handler: () => 0 }),
		effect: "maybe",
	} as unknown as AnyCommand;
	assert.throws(() => app.command(forged), {
		name: "RegistrationError",
		message:
			'command "xx": invalid effect "maybe": must be "read_only" or "mutating"',
	});
});

test("classification: deprecated commands are exempt, and carrying one errors", () => {
	const app = createApp({ name: "tt", version: "1", help: "hh" });
	// The exempt path: no effect, no error.
	assert.doesNotThrow(() => app.deprecate(deprecated("old", "gone")));
	const forged = {
		...deprecated("older", "gone"),
		effect: "read_only",
	} as unknown as ReturnType<typeof deprecated>;
	assert.throws(() => app.deprecate(forged), {
		name: "RegistrationError",
		message:
			'deprecated command "older": effect classification does not apply (a deprecated command has no handler)',
	});
});

test("classification: deprecated entries stay out of the command schema", () => {
	const app = createApp({ name: "tt", version: "1", help: "hh" });
	app.deprecate(deprecated("old", "gone"));
	const schema = app.dumpSchemaDict();
	assert.deepEqual(schema.deprecated, { old: "gone" });
	assert.equal(schema.commands, undefined);
});

// --- §6.1 grant declarations ---

test("grants: names must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*", () => {
	assert.throws(
		() =>
			defineMutatingCommand("go", {
				help: "hh",
				grants: [{ name: "Push", reason: "rr", kind: "proc_mutate" }],
				handler: () => 0,
			}),
		{
			message:
				"command \"go\": invalid grant name 'Push': must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*",
		},
	);
});

test("grants: duplicate names are rejected", () => {
	assert.throws(
		() =>
			defineMutatingCommand("go", {
				help: "hh",
				grants: [
					{ name: "push", reason: "rr", kind: "proc_mutate" },
					{ name: "push", reason: "r2", kind: "net_mutate" },
				],
				handler: () => 0,
			}),
		{ message: "command \"go\": duplicate grant 'push'" },
	);
});

test("grants: the reason is mandatory and non-empty", () => {
	assert.throws(
		() =>
			defineMutatingCommand("go", {
				help: "hh",
				grants: [{ name: "push", reason: "  ", kind: "proc_mutate" }],
				handler: () => 0,
			}),
		{
			message: "command \"go\": grant 'push' reason must be a non-empty string",
		},
	);
});

test("grants: the kind must be one of the four grantable kinds", () => {
	assert.throws(
		() =>
			defineMutatingCommand("go", {
				help: "hh",
				grants: [
					{
						name: "push",
						reason: "rr",
						kind: "cache_write" as never,
					},
				],
				handler: () => 0,
			}),
		{
			message:
				"command \"go\": grant 'push' has invalid kind 'cache_write': must be one of proc_mutate, proc_spawn, file_write, net_mutate",
		},
	);
});

test("grants: a passthrough may declare them too", () => {
	const def = mutatingPassthrough("exec", {
		help: "hh",
		grants: [
			{ name: "run-any", reason: "opaque by design", kind: "proc_mutate" },
		],
		handler: () => 0,
	});
	assert.equal(def.grants.length, 1);
});

// --- §10.2 declared forwarding ---

test("forwarding: the reason is mandatory and non-empty", () => {
	assert.throws(
		() =>
			defineReadOnlyCommand("go", {
				help: "hh",
				forwarding: { reason: "" },
				handler: () => 0,
			}),
		{ message: 'command "go": forwarding reason must be a non-empty string' },
	);
});

test("forwarding: a declared reason reaches the schema", () => {
	const app = createApp({ name: "tt", version: "1", help: "hh" });
	app.command(
		defineReadOnlyCommand("wrap", {
			help: "hh",
			forwarding: { reason: "wraps another CLI" },
			handler: () => 0,
		}),
	);
	const commands = app.dumpSchemaDict().commands as Record<
		string,
		Record<string, unknown>
	>;
	assert.deepEqual(commands.wrap?.forwarding, { reason: "wraps another CLI" });
});

// --- §10.4 the framework-internal marker and its module verification ---

test("framework-internal: the six auto-registered commands declare forwarding", () => {
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		config: true,
		checksEmbed: 'app = "tt"\n',
	});
	const schema = app.dumpSchemaDict();
	const commands = schema.commands as Record<string, Record<string, unknown>>;
	assert.deepEqual(commands.check?.forwarding, {
		reason: FRAMEWORK_INTERNAL_FORWARDING_REASON,
	});
	const groups = schema.groups as Record<string, Record<string, unknown>>;
	const cfg = (groups.config as Record<string, unknown>).commands as Record<
		string,
		Record<string, unknown>
	>;
	for (const name of ["path", "show", "set", "edit", "init"]) {
		assert.deepEqual(
			cfg[name]?.forwarding,
			{ reason: FRAMEWORK_INTERNAL_FORWARDING_REASON },
			name,
		);
	}
});

test("framework-internal: the five config subcommands carry §9.2's classifications", () => {
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		config: true,
	});
	const groups = app.dumpSchemaDict().groups as Record<
		string,
		Record<string, unknown>
	>;
	const cfg = (groups.config as Record<string, unknown>).commands as Record<
		string,
		Record<string, unknown>
	>;
	assert.equal(cfg.show?.effect, "read_only");
	assert.equal(cfg.path?.effect, "read_only");
	assert.equal(cfg.set?.effect, "mutating");
	assert.equal(cfg.init?.effect, "mutating");
	assert.equal(cfg.edit?.effect, "mutating");
});

test("framework-internal: `check` classifies read_only", () => {
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: 'app = "tt"\n',
	});
	const commands = app.dumpSchemaDict().commands as Record<
		string,
		Record<string, unknown>
	>;
	assert.equal(commands.check?.effect, "read_only");
});

test("framework-internal: the marker is not emitted in the schema", () => {
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		checksEmbed: 'app = "tt"\n',
	});
	const commands = app.dumpSchemaDict().commands as Record<
		string,
		Record<string, unknown>
	>;
	assert.ok(!("frameworkInternal" in (commands.check as object)));
});

test("framework-internal: a FOREIGN handler carrying the marker fails registration", () => {
	const app = createApp({ name: "tt", version: "1", help: "hh" });
	// defineFrameworkCommand is package-internal, but a consumer reaching it by
	// any route -- monkey-patching, prototype tampering, reflection -- gets a
	// carrier whose handler is NOT in the framework WeakSet.
	const foreign = defineFrameworkCommand("sneaky", "mutating", {
		help: "hh",
		handler: (() => 0) as never,
	});
	assert.throws(() => app.command(foreign), {
		name: "RegistrationError",
		message:
			'command "sneaky": handler is marked framework-internal but is not defined in the strictcli module',
	});
});

test("framework-internal: a marked handler registers cleanly", () => {
	const app = createApp({ name: "tt", version: "1", help: "hh" });
	const ours = defineFrameworkCommand("blessed", "read_only", {
		help: "hh",
		handler: markFrameworkHandler(() => 0) as never,
	});
	assert.doesNotThrow(() => app.command(ours));
});

test("framework-internal: verification keys on identity, not on the name", () => {
	const app = createApp({ name: "tt", version: "1", help: "hh" });
	const blessed = markFrameworkHandler(function checkHandler() {
		return 0;
	});
	// A DIFFERENT function with the same `name` is not the blessed identity.
	const impostor = { checkHandler: () => 0 }.checkHandler;
	assert.equal(impostor.name, blessed.name);
	assert.throws(
		() =>
			app.command(
				defineFrameworkCommand("xx", "read_only", {
					help: "hh",
					handler: impostor as never,
				}),
			),
		{ message: /is not defined in the strictcli module/ },
	);
});

test("framework-internal: the marker is unreachable from the public spec", () => {
	// There is no `frameworkInternal` key in any options object: passing one
	// through the public factory is dropped, so the carrier is unmarked and the
	// verification never fires.
	const def = defineReadOnlyCommand("xx", {
		help: "hh",
		handler: () => 0,
		...({ frameworkInternal: true } as object),
	} as never);
	assert.ok(!("frameworkInternal" in (def as object)));
});

// --- §7.5 check-command subsumption ---

test("check subsumption: the app-level allowlist reaches the schema", () => {
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		procObserveAllowlist: [
			["git", "status"],
			["gh", "release", "view"],
		],
	});
	assert.deepEqual(app.dumpSchemaDict().proc_observe_allowlist, [
		["git", "status"],
		["gh", "release", "view"],
	]);
});

test("check subsumption: an empty allowlist is omitted from the schema", () => {
	const app = createApp({ name: "tt", version: "1", help: "hh" });
	assert.ok(!("proc_observe_allowlist" in app.dumpSchemaDict()));
});

test("allowlist: entries must be non-empty lists of strings", () => {
	assert.throws(
		() =>
			createApp({
				name: "tt",
				version: "1",
				help: "hh",
				procObserveAllowlist: [[]],
			}),
		{ message: "proc_observe_allowlist entries must not be empty" },
	);
	assert.throws(
		() =>
			createApp({
				name: "tt",
				version: "1",
				help: "hh",
				procObserveAllowlist: [[1 as never]],
			}),
		{
			message:
				"proc_observe_allowlist entries must be lists of strings, got number",
		},
	);
});

// --- The single validated registration path (§10.4) ---

test("registration: framework commands go through the same validated path", () => {
	const app = createApp({
		name: "tt",
		version: "1",
		help: "hh",
		config: true,
		checksEmbed: 'app = "tt"\n',
	}) as unknown as AppImpl;
	// Every registered command -- consumer or framework -- lands in the same
	// RegisteredCommand map with a classified carrier.
	const cmd = app.commands.get("check");
	assert.ok(cmd !== undefined);
	assert.equal((cmd.def as AnyCommand).effect, "read_only");
	const cfg = app.groups.get("config");
	assert.ok(cfg !== undefined);
	for (const [, rc] of cfg.commands) {
		assert.ok(
			["read_only", "mutating"].includes((rc.def as AnyCommand).effect),
		);
	}
});

test("registration: RegistrationError is the thrown type for every ban", () => {
	assert.throws(
		() =>
			flag("quiet", t.bool, {
				help: "hh",
				presence: "default",
				default: false,
			}),
		RegistrationError,
	);
});
