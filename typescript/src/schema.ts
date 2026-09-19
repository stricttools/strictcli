/**
 * Schema dump (--dump-schema): builds the machine-readable schema dict and
 * writes .strictcli/schema.json describing every command, group, flag, and
 * arg, at SCHEMA VERSION 2 (contract §25).
 *
 * Everything about the format is cross-language and pinned: the closed
 * four-keyword `value_schema` fragment and its key order (§25.2), arity as
 * value shape (§25.3), the choices split (§25.5), the selector encoding
 * (§25.6), the declared key order at every depth (§25.9), the rewritten
 * `defaults` block (§25.10), the behavioral-completeness keys (§25.11) and the
 * byte canon (§25.8). No implementation sorts keys at serialization time; the
 * three keyed blocks that ARE sorted (`deprecated`, `tag_contracts`, `checks`)
 * are sorted because no implementation retains a declaration order for them.
 *
 * The byte canon makes the committed file dumper-independent: a repository
 * whose `.strictcli/schema.json` is written sometimes by this implementation
 * and sometimes by the Python or Go one must see a diff exactly when something
 * changed. Numbers are the one place TypeScript needs its own writer -- bigint
 * values are bare integer tokens and floats are SCF tokens, neither of which
 * JSON.stringify can emit -- and its string escaping is JSON.stringify's,
 * which already IS the canon (raw non-ASCII, literal `<`, `>`, `&` and `/`,
 * and a lone surrogate escaped).
 *
 * The one remaining TS-side delta is not a format rule: dict defaults are
 * emitted with sorted keys (the TS Map display convention). project_id comes
 * from package.json "name" (the ecosystem analog of Python's pyproject.toml
 * [project].name and Go's go.mod module path).
 */

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import type { AppImpl, GroupImpl, RegisteredCommand } from "./app.js";
import type { ConfigFieldRt } from "./config.js";
import { resolveConstraints } from "./constraints.js";
import type { Effect, Forwarding, Grant } from "./effects.js";
import {
	errCannotDetermineProjectIDNoName,
	errCannotDetermineProjectIDNoPackageJson,
	errCannotDetermineProjectIDReadError,
	errSchemaMismatch,
} from "./errors.js";
import {
	type AnyArg,
	type AnyChoice,
	type AnyChoiceFlag,
	type AnyCommand,
	type AnyDecl,
	type AnyFlag,
	type ArgOptsView,
	CHOICE_VALUE_KEY,
	type ChoiceRecordView,
	flagOpts,
	type RetiredChoiceRecordView,
	scalarFragment,
	schemaKind,
	type UpdateOf,
	valueSchemaFragment,
} from "./factories.js";
import { formatFloatCanonical } from "./float.js";
import { isInfraRootPath, serializeInfraMarker } from "./infra.js";
import { serializeUpdateOf } from "./update.js";
import { formatValueForError } from "./values.js";

// --- JSON writer (2-space indent, bigint/float machine-channel tokens) ---

/**
 * Serializes a schema value as pretty JSON with 2-space indentation,
 * mirroring Python json.dumps(schema, indent=2): ": " key separator, one
 * item per line, empty containers as {} / []. BigInt values become bare
 * integer tokens and floats become SCF tokens (JSON.stringify can emit
 * neither), which is why this is a custom writer. Plain objects keep
 * insertion order; Maps are emitted with sorted keys (the TS dict display
 * convention). No trailing newline -- the file writer appends it.
 */
export function schemaJson(value: unknown, indent = 0): string {
	const pad = "  ".repeat(indent);
	const inner = "  ".repeat(indent + 1);
	if (value === null || value === undefined) {
		return "null";
	}
	switch (typeof value) {
		case "bigint":
			return value.toString();
		case "number":
			return formatFloatCanonical(value);
		case "boolean":
			return value ? "true" : "false";
		case "string":
			return JSON.stringify(value);
		case "object": {
			if (Array.isArray(value)) {
				if (value.length === 0) {
					return "[]";
				}
				const items = value.map((el) => inner + schemaJson(el, indent + 1));
				return `[\n${items.join(",\n")}\n${pad}]`;
			}
			const entries =
				value instanceof Map
					? [...(value as Map<unknown, unknown>).entries()]
							.map(([k, v]): [string, unknown] => [String(k), v])
							.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
					: Object.entries(value).filter(([, v]) => v !== undefined);
			if (entries.length === 0) {
				return "{}";
			}
			const items = entries.map(
				([k, v]) =>
					`${inner}${JSON.stringify(k)}: ${schemaJson(v, indent + 1)}`,
			);
			return `{\n${items.join(",\n")}\n${pad}}`;
		}
		default:
			// function/symbol cannot appear in schema dicts.
			throw new Error(
				`internal: unserializable schema value of type ${typeof value}`,
			);
	}
}

// --- Serializers (field order is §25.9's declared order) ---

/** One check definition, as the schema reads it. */
interface CheckDefView {
	readonly tags: readonly string[];
	readonly severity: string;
	readonly fast: boolean;
	readonly pure: boolean;
	readonly needsNetwork: boolean;
	readonly dependsOn: readonly string[];
	readonly scope: string;
}

/** A keyed block emitted SORTED ascending by key (§25.9's second rule). */
function sortedRecord(
	entries: ReadonlyMap<string, string>,
): Record<string, string> {
	const out: Record<string, string> = {};
	for (const key of [...entries.keys()].sort()) {
		out[key] = entries.get(key) as string;
	}
	return out;
}

/**
 * A value flag's (or arg's) `choices=` entries, as the records item 164 made
 * them (§25.5). The machine-readable half of a choices declaration lives in
 * the fragment's `enum`; this is the human-readable half, which JSON Schema
 * has no vocabulary for. `help` is OMITTED when the entry declares none, so
 * the two spellings of "no help" -- an absent one and Go's empty string --
 * cannot produce different bytes for the same declaration.
 */
function serializeChoiceRecords(
	records: readonly ChoiceRecordView[],
): Record<string, unknown>[] {
	return records.map((r) => {
		const entry: Record<string, unknown> = { value: r.value };
		if (r.help !== undefined && r.help !== "") {
			entry.help = r.help;
		}
		return entry;
	});
}

/** A declared default, as the schema publishes it (marker shape preserved). */
function serializeDefaultValue(value: unknown): unknown {
	return isInfraRootPath(value) ? serializeInfraMarker(value) : value;
}

/**
 * Serializes a Flag (contract §25). Keys are emitted in the canonical order
 * §25.9 pins for a flag entry; nothing is sorted at serialization time.
 */
function serializeFlag(f: AnyFlag): Record<string, unknown> {
	const o = flagOpts(f);
	const kind = schemaKind(f.schema);
	const d: Record<string, unknown> = {
		name: f.name,
		help: o.help,
		// The value's shape is a real JSON Schema fragment now, and the v1
		// `type` key -- which had three spellings across three implementations
		// -- is gone with `repeatable`, whose fact the shape already carries
		// (§25.2, §25.3).
		value_schema: valueSchemaFragment(f),
	};
	if (o.short !== undefined) {
		d.short = o.short;
	}
	// presence: ALWAYS emitted (contract §13's presence-round amendment). The
	// requiredness erasure -- a required flag and an optional one serializing
	// identically -- is what let schema parity pass by three implementations
	// agreeing about a fact none of them emitted.
	d.presence = o.presence;
	// default: emitted exactly when presence is "default", and then ALWAYS,
	// whatever the value: [], {}, "", false and 0 are declarations now, so the
	// omit-when-empty compound rules are deleted. A RelativeToRoot marker keeps
	// its machine-stable shape.
	if (o.presence === "default") {
		const dflt = o.default;
		d.default = isInfraRootPath(dflt)
			? serializeInfraMarker(dflt)
			: kind === "list"
				? [...(dflt as unknown[])]
				: dflt;
	}
	if (o.env !== undefined) {
		d.env = o.env;
	}
	if (o.envSeparator !== undefined) {
		d.env_separator = o.envSeparator;
	}
	// Omitted when true, which is the framework's behavior: the key appears
	// exactly on the flags that depart from it (§25.11).
	if (o.prefixed === false) {
		d.prefixed = false;
	}
	if (o.choices !== undefined) {
		d.choices = serializeChoiceRecords(o.choices);
	}
	if (o.retiredChoices !== undefined) {
		d.retired_choices = serializeRetiredChoices(o.retiredChoices);
	}
	if (o.unique === true) {
		d.unique = true;
	}
	// Per-flag conflict mode: serialized only when explicitly set. Absence
	// means "inherit the app default", which v2 publishes as
	// `config_conflict_mode`, so the effective mode is finally computable from
	// the dump alone (§25.11).
	if (o.conflictMode !== undefined) {
		d.conflict_mode = o.conflictMode;
	}
	// negatable: bool flags always emit it (null covers non-bools).
	if (f.schema === "bool") {
		d.negatable = o.negatable !== false;
	}
	// Emitted only when declared true; absence means the property cannot be
	// cleared, which is the baseline. There is NO second flag entry for the
	// minted `--unset-<prop>`: it is derived from this key exactly as
	// `--no-<x>` is derived from `negatable`, and the dump publishes
	// declarations (contract §13's amendment, §27.9).
	if (o.nullable === true) {
		d.nullable = true;
	}
	return d;
}

/**
 * Serializes an Arg (contract §25).
 *
 * `variadic` SURVIVES the arity rule that deleted `repeatable`, and the
 * asymmetry is deliberate: it names a token-consumption rule -- this arg takes
 * every remaining positional token, and only the last arg may -- which a
 * consumer needs in order to render `<files>...` in a usage line.
 */
function serializeArg(a: AnyArg): Record<string, unknown> {
	const o = a.opts as ArgOptsView;
	const d: Record<string, unknown> = {
		name: a.name,
		help: o.help,
		value_schema: valueSchemaFragment(a),
	};
	// The arg entry's `required` key is deleted: it was the arg-side spelling
	// of the same fact, and keeping it beside `presence` would put two keys on
	// one fact (contract §13's presence-round amendment).
	d.presence = o.presence;
	if (o.presence === "default") {
		d.default = serializeDefaultValue(o.default);
	}
	if (o.variadic === true) {
		d.variadic = true;
	}
	if (o.choices !== undefined) {
		d.choices = serializeChoiceRecords(o.choices);
	}
	if (o.retiredChoices !== undefined) {
		d.retired_choices = serializeRetiredChoices(o.retiredChoices);
	}
	return d;
}

/**
 * A declaration's retired spellings, as a map from spelling to message,
 * SORTED ascending by key -- the treatment a group's `deprecated` map already
 * gets, and for the same reason: a keyed object whose declaration order no
 * implementation is required to retain has sort order as its only reachable
 * canon.
 *
 * The key is the spelling rendered through the error-value formatter, so an
 * int, a float and a string key identically in all three implementations. The
 * map is omitted entirely when nothing is retired.
 */
function serializeRetiredChoices(
	retired: readonly RetiredChoiceRecordView[],
): Record<string, string> {
	const messages = new Map<string, string>();
	for (const rc of retired) {
		messages.set(formatValueForError(rc.value), rc.message);
	}
	const out: Record<string, string> = {};
	for (const key of [...messages.keys()].sort()) {
		out[key] = messages.get(key) as string;
	}
	return out;
}

/** Serializes one declaration: an ordinary flag, or a SELECTOR. */
function serializeDecl(d: AnyDecl): Record<string, unknown> {
	return d.kind === "flag" ? serializeFlag(d) : serializeSelector(d);
}

/**
 * One choice of one selector: `name`, `help`, and its scope (§25.6).
 *
 * `flags` is omitted when the scope is empty. A member-spelled choice's
 * payload is the FIRST entry of that array, under the reserved name `value`
 * with `presence: "required"` -- the payload is supplied by electing the
 * member, and required-once-elected is exactly what a member flag's presence
 * means. The scope's own declared flags follow it, in declaration order.
 */
function serializeChoiceObject(
	name: string,
	c: AnyChoice,
): Record<string, unknown> {
	const entry: Record<string, unknown> = { name, help: c.help };
	const scope: Record<string, unknown>[] = [];
	if (c.value !== undefined) {
		// The payload carries its OWN help, declared beside its carrier: §25.6's
		// `value` entry is an ordinary scoped-flag entry and its `help` is the
		// payload's, never the choice's. The choice's help documents what
		// electing it means and is what the member's help line renders.
		// Key order is §25.9's flag-entry order, `short` included: the entry is
		// an ordinary flag entry and the member's short is that flag's short.
		scope.push(
			c.value.short === undefined
				? {
						name: CHOICE_VALUE_KEY,
						help: c.value.help,
						value_schema: scalarFragment(c.value.carrier.schema, undefined),
						presence: "required",
					}
				: {
						name: CHOICE_VALUE_KEY,
						help: c.value.help,
						value_schema: scalarFragment(c.value.carrier.schema, undefined),
						short: c.value.short,
						presence: "required",
					},
		);
	}
	for (const sub of Object.values(c.flags)) {
		scope.push(serializeDecl(sub));
	}
	if (scope.length > 0) {
		entry.flags = scope;
	}
	return entry;
}

/**
 * A selector's declared default, as the flat map §25.6 pins:
 * `{"choice": "<name>", "<field>": <value>, ...}` -- the choice's name under
 * the reserved key `choice`, followed by each field that HAS a value in the
 * default selection, in declaration order.
 *
 * TypeScript's default names a choice and the scope supplies the values, so a
 * field with no declared default is omitted -- which is unambiguous because
 * `null` is not a declarable default anywhere in the framework. A nested
 * selector is excluded: it publishes its own entry with its own default.
 */
function serializeSelectorDefault(sel: AnyChoiceFlag): Record<string, unknown> {
	const elected = sel.opts.default as string;
	const flat: Record<string, unknown> = { choice: elected };
	for (const sub of Object.values(sel.choices[elected]?.flags ?? {})) {
		if (sub.kind !== "flag") {
			continue;
		}
		const o = flagOpts(sub);
		if (o.presence !== "default") {
			continue;
		}
		flat[sub.name] = serializeDefaultValue(o.default);
	}
	return flat;
}

/**
 * Serializes one selector, in the encoding §25.6 pins.
 *
 * A selector flag has NO `value_schema`, and its absence is the declaration: a
 * selector's value is a variant -- one tagged record chosen from several, each
 * with a different set of fields -- and the closed four-keyword subset cannot
 * express one. Publishing a wrong fragment would be worse than publishing
 * none, because a reader would validate against it.
 *
 * `elect_by` is the discriminator: an entry carrying it is a selector, and its
 * `choices` are choice objects; an entry without it is an ordinary flag, and
 * its `choices` (if any) are value records. Each scoped entry is a FULL flag
 * entry, which is what makes recursion free -- a nested selector is an entry
 * inside a `flags` array carrying its own `choices` and `elect_by`, to any
 * depth.
 */
function serializeSelector(sel: AnyChoiceFlag): Record<string, unknown> {
	const d: Record<string, unknown> = {
		name: sel.name,
		help: sel.opts.help,
	};
	if (sel.opts.short !== undefined) {
		d.short = sel.opts.short;
	}
	d.presence = sel.opts.presence;
	if (sel.opts.presence === "default") {
		d.default = serializeSelectorDefault(sel);
	}
	if (sel.opts.env !== undefined) {
		d.env = sel.opts.env;
	}
	d.choices = Object.entries(sel.choices).map(([name, c]) =>
		serializeChoiceObject(name, c),
	);
	d.elect_by = sel.electBy;
	return d;
}

/**
 * Builds the constraint catalogue (§25.7's amendment, §26.11). Four `type`
 * values, a mandatory `name` on every entry, and a `members` array of
 * `{kind, name, when}` records in declaration order.
 *
 * The encoding is COMPLETE rather than indicative: the resolved `kind` is
 * published so a consumer never has to search the flag and arg lists to learn
 * what a name refers to, and the nesting is published as constraint-kind
 * members rather than flattened into leaves -- a partially encoded constraint
 * is not a legal intermediate state.
 */
function serializeConstraints(def: AnyCommand): Record<string, unknown>[] {
	const out: Record<string, unknown>[] = [];
	// The `mutex` entry is DELETED with the construct (§21's box) and
	// `co_required` goes the same way (§26.1): an exactly-one shape is a
	// selector now, and all-or-none absorbed co-required by rename.
	for (const c of resolveConstraints(def)) {
		switch (c.kind) {
			case "at-least-one":
			case "all-or-none":
				out.push({
					type: c.kind === "at-least-one" ? "at_least_one" : "all_or_none",
					name: c.name,
					members: c.members.map((m) => {
						const entry: Record<string, unknown> = {
							kind: m.kind,
							name: m.name,
						};
						// Always emitted on a flag or arg member, never on a
						// constraint member: a defaulted key that could be omitted is
						// an erasure, and this format exists to end erasures. It
						// therefore takes no `defaults` block entry.
						if (m.when !== undefined) {
							entry.when = m.when;
						}
						return entry;
					}),
				});
				break;
			case "requires":
				out.push({
					type: "requires",
					name: c.name,
					flag: c.flag,
					depends_on: c.dependsOn,
				});
				break;
			case "implies":
				out.push({
					type: "implies",
					name: c.name,
					flag: c.flag,
					implies: c.implies,
					value: c.value,
				});
				break;
		}
	}
	return out;
}

/** Serializes a registered command (regular or passthrough). */
function serializeCommand(rc: RegisteredCommand): Record<string, unknown> {
	const carrier = rc.def as {
		readonly effect: Effect;
		readonly consequential?: boolean;
		readonly dryRunSupported?: boolean;
		readonly dryRunUnsupportedReason?: string;
		readonly updateOf?: UpdateOf;
		readonly payloadSchema?: Readonly<Record<string, unknown>>;
		readonly ownsStdout?: boolean;
		readonly grants?: readonly Grant[];
		readonly forwarding?: Forwarding;
	};
	const d: Record<string, unknown> = {
		name: rc.name,
		help: rc.help,
		// Always emitted: classification is mandatory, so there is no default
		// to omit against.
		effect: carrier.effect,
	};
	// consequential: NOT mandatory; absence means "not consequential"
	// (contract §8.1, §13), so it is omitted when false.
	if (carrier.consequential === true) {
		d.consequential = true;
	}
	// Emitted only when declared: dry run is supported unless a command says
	// otherwise, so the pair appears exactly on the commands that refuse it.
	if (carrier.dryRunSupported === false) {
		d.dry_run_supported = false;
		d.dry_run_unsupported_reason = carrier.dryRunUnsupportedReason;
	}
	// The update declaration, published as TWO command-entry keys where it is
	// one nested record on the declaration surface (contract §13's amendment,
	// §27.9): a consumer asking what write mode a command has should not have
	// to descend, and a consumer WRITING a declaration must not be able to
	// spell half of one. The pair is atomic by construction rather than by a
	// guard -- the two facts are one declaration.
	if (carrier.updateOf !== undefined) {
		d.update_of = serializeUpdateOf(carrier.updateOf);
		d.write_mode = carrier.updateOf.writeMode;
	}
	// The payload contract, published verbatim (contract §19.5): the inline
	// literal is the sole canonical artifact, so the dump carries it as written
	// rather than a re-rendering of it.
	if (carrier.payloadSchema !== undefined) {
		d.payload_schema = carrier.payloadSchema;
	}
	// Emitted only when declared true; absence means the framework owns stdout,
	// which is the baseline (contract §13's 2026-08-13 amendment, §19.6).
	if (carrier.ownsStdout === true) {
		d.owns_stdout = true;
	}
	if (rc.kind === "passthrough") {
		d.passthrough = true;
	}
	// Flags and selectors share ONE array, interleaved in declaration order: a
	// selector IS a flag (§24.2), and the presence of `elect_by` is what tells
	// a reader which shape it is holding (§25.6).
	const decls = rc.kind === "command" ? (rc.def as AnyCommand).allDecls : [];
	if (decls.length > 0) {
		d.flags = decls.map(serializeDecl);
	}
	if (rc.kind === "command") {
		const def = rc.def as AnyCommand;
		// The grouping v1 discarded when it merged a set's flags into the
		// command's flag list. Members keep their ordinary entries above, so this
		// adds a grouping without duplicating a declaration (§25.11).
		if (def.flagSets.length > 0) {
			d.flag_sets = def.flagSets.map((fs) => ({
				name: fs.name,
				flags: Object.values(fs.flags).map((f) => f.name),
			}));
		}
		if (def.args.length > 0) {
			d.args = def.args.map(serializeArg);
		}
	}
	// tags: merged (own + inherited group) tags, already deduped and sorted.
	if (rc.tags.length > 0) {
		d.tags = [...rc.tags];
	}
	if (rc.kind === "command") {
		const def = rc.def as AnyCommand;
		const constraints = serializeConstraints(def);
		if (constraints.length > 0) {
			d.constraints = constraints;
		}
		if (rc.hidden) {
			d.hidden = true;
		}
		if (def.interactive) {
			d.interactive = true;
		}
		if (rc.configFields.length > 0) {
			d.config_fields = [...rc.configFields];
		}
	} else if (rc.hidden) {
		d.hidden = true;
	}
	if (carrier.grants !== undefined && carrier.grants.length > 0) {
		d.grants = carrier.grants.map((g) => ({
			name: g.name,
			reason: g.reason,
			kind: g.kind,
		}));
	}
	if (carrier.forwarding !== undefined) {
		d.forwarding = { reason: carrier.forwarding.reason };
	}
	return d;
}

/** Serializes a Group (recursive). Own tags only, not accumulated. */
function serializeGroup(grp: GroupImpl): Record<string, unknown> {
	const d: Record<string, unknown> = {
		name: grp.name,
		help: grp.help,
	};
	if (grp.commands.size > 0) {
		const commands: Record<string, unknown> = {};
		for (const [name, cmd] of grp.commands) {
			commands[name] = serializeCommand(cmd);
		}
		d.commands = commands;
	}
	if (grp.groups.size > 0) {
		const groups: Record<string, unknown> = {};
		for (const [name, sub] of grp.groups) {
			groups[name] = serializeGroup(sub);
		}
		d.groups = groups;
	}
	// Sorted ascending by key, exactly as the app-level block is: no
	// implementation retains a declaration order for `deprecated`, and a canon
	// that cannot be produced from what an implementation holds is not a canon
	// (§25.9).
	if (grp.deprecated.size > 0) {
		d.deprecated = sortedRecord(grp.deprecated);
	}
	if (grp.tags.length > 0) {
		d.tags = [...grp.tags].sort();
	}
	if (grp.hidden) {
		d.hidden = true;
	}
	return d;
}

/**
 * Returns the canonical defaults object for the schema. Consumers use this
 * to reconstruct omitted fields.
 */
function buildSchemaDefaults(): Record<string, unknown> {
	return {
		schema_version: 2n,
		app: {
			env_prefix: null,
			config: false,
			config_format: "json",
			config_path: null,
			config_conflict_mode: "cli-wins",
			proc_observe_allowlist: [],
			global_flags: [],
			commands: {},
			groups: {},
			deprecated: {},
			tag_contracts: {},
			checks: {},
			config_fields: {},
			infra: {},
		},
		// `default` has NO baseline on a flag or an arg: since presence became
		// the authority it is emitted exactly when `presence` is `"default"`,
		// and a `null` baseline for it would state something false. Nor does
		// `value_schema`: a selector carries no fragment at all and its absence
		// IS the declaration, so every answer a baseline could give is false for
		// the one entry that omits the key (§25.10).
		flag: {
			short: null,
			env: null,
			env_separator: null,
			prefixed: true,
			choices: null,
			elect_by: null,
			unique: false,
			conflict_mode: null,
			negatable: null,
			nullable: false,
		},
		arg: {
			variadic: false,
			choices: null,
		},
		// The two choice entities, which is what makes this block the complete
		// omission map it is defined to be: a selector choice object's `flags` is
		// omitted when the scope is empty, and a value-flag choice record's
		// `help` is omitted when the entry declares none.
		choice: { flags: [] },
		choice_record: { help: null },
		command: {
			consequential: false,
			dry_run_supported: true,
			dry_run_unsupported_reason: null,
			update_of: null,
			write_mode: null,
			payload_schema: null,
			owns_stdout: false,
			passthrough: false,
			flags: [],
			flag_sets: [],
			args: [],
			tags: [],
			constraints: [],
			hidden: false,
			interactive: false,
			config_fields: [],
			grants: [],
			forwarding: null,
		},
		group: {
			commands: {},
			groups: {},
			deprecated: {},
			tags: [],
			hidden: false,
		},
		config_field: { default: null, bound_commands: [] },
		check: { scope: null },
		infra: { roots: [], handshakes: [], connections: [] },
	};
}

// --- Core schema production (CWD-free) ---

/** Records which commands (space-joined paths) bind each config field. */
function collectConfigFieldBindings(app: AppImpl): Map<string, string[]> {
	const bindings = new Map<string, string[]>();
	for (const name of app.configFields.keys()) {
		bindings.set(name, []);
	}
	const collect = (
		commands: ReadonlyMap<string, RegisteredCommand>,
		path: readonly string[],
	): void => {
		for (const cmd of commands.values()) {
			const cmdPath = [...path, cmd.name].join(" ");
			for (const cfName of cmd.configFields) {
				bindings.get(cfName)?.push(cmdPath);
			}
		}
	};
	const collectGroup = (grp: GroupImpl, path: readonly string[]): void => {
		const groupPath = [...path, grp.name];
		collect(grp.commands, groupPath);
		for (const sub of grp.groups.values()) {
			collectGroup(sub, groupPath);
		}
	};
	collect(app.commands, []);
	for (const grp of app.groups.values()) {
		collectGroup(grp, []);
	}
	return bindings;
}

/**
 * Serializes one declared config field entry (§25.7).
 *
 * Config fields are scalar-only in every implementation, so the fragment is
 * always a scalar row. `required` STAYS beside it: it is not §23's presence
 * declaration under another name -- a config field has no CLI surface and no
 * three-way declaration, and `required` there means "the config file must
 * contain it".
 */
function serializeConfigField(
	cf: ConfigFieldRt,
	boundCommands: readonly string[],
): Record<string, unknown> {
	const entry: Record<string, unknown> = {
		value_schema: scalarFragment(cf.schema, undefined),
		help: cf.help,
		required: cf.required,
	};
	if (cf.hasDefault) {
		entry.default = cf.default;
	}
	if (boundCommands.length > 0) {
		entry.bound_commands = [...boundCommands];
	}
	return entry;
}

/**
 * Builds the full schema dict, excluding project_id.
 *
 * This is the CWD-free, filesystem-free core of schema production. It reads
 * only the in-memory App; project_id is the only field that requires reading
 * package.json from the CWD, so it is added later by the file-writer path.
 * Fields matching their defaults are omitted; see buildSchemaDefaults().
 */
export function dumpSchemaCore(app: AppImpl): Record<string, unknown> {
	const schema: Record<string, unknown> = {
		schema_version: 2n,
		defaults: buildSchemaDefaults(),
		name: app.name,
		version: app.version,
		help: app.help,
	};
	if (app.envPrefix !== undefined) {
		schema.env_prefix = app.envPrefix;
	}
	if (app.configEnabled) {
		schema.config = true;
	}
	// The three app-level config keys v1 was blind to. Until v2 an app could
	// relocate every user's config file, or switch it from JSON to TOML, while
	// its dumped schema stayed byte-identical (§25.11).
	if (app.configFormat !== "json") {
		schema.config_format = app.configFormat;
	}
	// The DECLARATION, never the resolution: a declared literal path as
	// declared, and a relativeToRoot() marker in its machine-stable shape. The
	// resolved absolute path is a property of the dumping machine.
	if (app.configPathDeclared !== undefined) {
		schema.config_path = serializeDefaultValue(app.configPathDeclared);
	}
	if (app.configConflictMode !== "cli-wins") {
		schema.config_conflict_mode = app.configConflictMode;
	}
	if (app.procObserveAllowlist.length > 0) {
		schema.proc_observe_allowlist = app.procObserveAllowlist.map((p) => [...p]);
	}
	if (app.globalFlags.length > 0) {
		schema.global_flags = app.globalFlags.map(serializeFlag);
	}
	if (app.commands.size > 0) {
		const commands: Record<string, unknown> = {};
		for (const [name, cmd] of app.commands) {
			commands[name] = serializeCommand(cmd);
		}
		schema.commands = commands;
	}
	if (app.groups.size > 0) {
		const groups: Record<string, unknown> = {};
		for (const [name, grp] of app.groups) {
			groups[name] = serializeGroup(grp);
		}
		schema.groups = groups;
	}
	// `deprecated`, `tag_contracts` and `checks` are emitted SORTED ascending by
	// key: no implementation retains a declaration order for all three, and a
	// canon that cannot be produced from what an implementation holds is not a
	// canon (§25.9). Every key here is ASCII by registration rule, so byte,
	// code-point and UTF-16 order coincide.
	if (app.deprecated.size > 0) {
		schema.deprecated = sortedRecord(app.deprecated);
	}
	if (app.tagContracts.size > 0) {
		schema.tag_contracts = sortedRecord(app.tagContracts);
	}
	// checks: only present when checks are enabled. Provider-sourced checks
	// (registerCheckProvider) are deliberately EXCLUDED: providers materialize
	// lazily per-cwd at check-run time, so they are not part of the static,
	// committed schema. The schema describes the declared surface, not the
	// dynamically-materialized one.
	if (app.checks.enabled) {
		const checksMap: Record<string, unknown> = {};
		for (const name of [...app.checks.defs.keys()].sort()) {
			if (app.checks.providerSourcedNames.has(name)) {
				continue;
			}
			const def = app.checks.defs.get(name) as CheckDefView;
			const entry: Record<string, unknown> = {
				tags: [...def.tags],
				severity: def.severity,
				fast: def.fast,
				pure: def.pure,
				needs_network: def.needsNetwork,
				depends_on: [...def.dependsOn],
			};
			if (def.scope !== "") {
				entry.scope = def.scope;
			}
			checksMap[name] = entry;
		}
		// Omitted when empty, which is the baseline the `defaults` block states:
		// an app whose only checks are provider-sourced publishes no block at all
		// rather than an empty one.
		if (Object.keys(checksMap).length > 0) {
			schema.checks = checksMap;
		}
	}
	if (app.configFields.size > 0) {
		const bindings = collectConfigFieldBindings(app);
		const cfSchema: Record<string, unknown> = {};
		for (const [name, cf] of app.configFields) {
			cfSchema[name] = serializeConfigField(cf, bindings.get(name) ?? []);
		}
		schema.config_fields = cfSchema;
	}
	// infra: only present when roots or handshake vars are declared. Resolved
	// root values are intentionally EXCLUDED -- the schema must be
	// machine-stable (not machine-specific). Only the declared env var and
	// default path (both stable declarations) are emitted for roots.
	if (
		app.infraRootDefaults.size > 0 ||
		app.handshakeEnvs.size > 0 ||
		app.connectionEnvs.size > 0
	) {
		const infra: Record<string, unknown> = {};
		if (app.infraRootDefaults.size > 0) {
			infra.roots = [...app.infraRootDefaults].map(([envVar, dflt]) => ({
				env_var: envVar,
				default: dflt,
			}));
		}
		if (app.handshakeEnvs.size > 0) {
			infra.handshakes = [...app.handshakeEnvs].map(([envVar, helpText]) => ({
				env_var: envVar,
				help: helpText,
			}));
		}
		if (app.connectionEnvs.size > 0) {
			infra.connections = [...app.connectionEnvs].map(([envVar, helpText]) => ({
				env_var: envVar,
				help: helpText,
			}));
		}
		schema.infra = infra;
	}
	return schema;
}

// --- project_id and the file-writer path (CWD-dependent) ---

/** Reads the project name from package.json in the current working directory. */
function readProjectId(): string {
	let raw: string;
	try {
		raw = readFileSync("package.json", "utf8");
	} catch {
		throw new Error(errCannotDetermineProjectIDNoPackageJson());
	}
	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch (e) {
		throw new Error(errCannotDetermineProjectIDReadError((e as Error).message));
	}
	const name =
		typeof parsed === "object" && parsed !== null
			? (parsed as { name?: unknown }).name
			: undefined;
	if (typeof name !== "string" || name === "") {
		throw new Error(errCannotDetermineProjectIDNoName());
	}
	return name;
}

/**
 * Produces the full schema dict including project_id (reads the CWD).
 * project_id is inserted immediately after defaults so the on-disk layout is
 * stable and byte-identical to the core dict once project_id is removed
 * (Python's _dump_schema layout).
 */
function dumpSchema(app: AppImpl): Record<string, unknown> {
	const core = dumpSchemaCore(app);
	const projectId = readProjectId();
	const result: Record<string, unknown> = {};
	for (const [key, value] of Object.entries(core)) {
		result[key] = value;
		if (key === "defaults") {
			result.project_id = projectId;
		}
	}
	return result;
}

/**
 * Verifies that an existing schema file belongs to the same project. Throws
 * on mismatch. Silently passes on: missing file, unreadable file, JSON
 * without a project_id field, non-string project_id, or matching project_id.
 */
function checkSchemaProjectId(filePath: string, newProjectId: string): void {
	let raw: string;
	try {
		raw = readFileSync(filePath, "utf8");
	} catch {
		return;
	}
	let existing: unknown;
	try {
		existing = JSON.parse(raw);
	} catch {
		return;
	}
	if (typeof existing !== "object" || existing === null) {
		return;
	}
	const existingId = (existing as { project_id?: unknown }).project_id;
	if (typeof existingId !== "string") {
		return;
	}
	if (existingId !== newProjectId) {
		throw new Error(errSchemaMismatch(existingId, newProjectId));
	}
}

/**
 * Writes the schema (2-space indent, trailing newline) to the app's declared
 * location and returns the absolute path. The location is decided once, at
 * construction (`schemaPath`, or the framework's ".strictcli/schema.json"
 * anchored at the construction-time cwd) -- never at the caller's working
 * directory at dump time.
 */
export function writeSchema(app: AppImpl): string {
	const schema = dumpSchema(app);
	const filePath = app.schemaOutPath;
	mkdirSync(dirname(filePath), { recursive: true });
	checkSchemaProjectId(filePath, schema.project_id as string);
	writeFileSync(filePath, `${schemaJson(schema)}\n`);
	// A framework-blessed CACHE_WRITE (the closed list of three sites).
	app.recordCacheWrite(resolve(filePath));
	return resolve(filePath);
}
