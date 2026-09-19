/**
 * Parse pipeline: reserved-flag pre-scan, two-phase global-flag parsing,
 * command-token parsing, env/config/default resolution, and constraint
 * validation. Mirrors Go doParse/extractGlobalFlags/parseCommand
 * (strictcli.go, parse.go) with Python _parse/_parse_global_flags/
 * _parse_command as the divergence ground truth.
 *
 * Internal contract: helpers throw ParseError; doParse converts them into
 * "parse-error" outcomes at the two sibling catch boundaries (global-flag
 * parsing without a command prefix, command parsing with the full
 * "app path command" prefix). Help/version/schema/mcp requests are outcome
 * variants, not exceptions; rendering them is help.ts/app-runner territory.
 */

import type { AppImpl, GroupImpl, RegisteredCommand } from "./app.js";
import { newStdinTracker, type StdinTracker } from "./atprefix.js";
import { coerceConfigScalarLong, widenJsonIntegers } from "./config.js";
import {
	constraintIndex,
	type EngagementProbe,
	isEngaged,
	type ResolvedConstraint,
	type ResolvedCoOccurrence,
	renderMembers,
	resolveConstraints,
} from "./constraints.js";
import type { ReservedFlags } from "./context.js";
import { resolveEnvValue } from "./env.js";
import {
	errAllOrNoneTogether,
	errArgumentWrapped,
	errAtLeastOneRequired,
	errBoolFlagNoValue,
	errBoolNegationNoValue,
	errConfigValueDuplicate,
	errConfigValueError,
	errDryRunNotSupported,
	errFlagRequired,
	errFlagRequiresFlag,
	errFlagRequiresValue,
	errFlagSetInBothAndConfig,
	errFlagSetInBothCliAndConfig,
	errFlagValueError,
	errHermeticConfigMutuallyExclusive,
	errHermeticWithConfigCommands,
	errImpliesConflict,
	errMissingRequiredArgument,
	errMutexDeclineClause,
	errUnexpectedArgument,
	errUnknownFlag,
	errUpdateValueAndUnset,
	ParseError,
} from "./errors.js";
import {
	type AnyArg,
	type AnyCommand,
	type AnyDecl,
	type AnyFlag,
	type ArgOptsView,
	buildScopeIndex,
	type ConflictMode,
	choiceValues,
	elemSchemaOf,
	flagOpts,
	flagParamName,
	requiredFlagForm,
	type ScopeIndexEntry,
	schemaKind,
	unsetFlagName,
} from "./factories.js";
import { isInfraRootPath, resolveInfraRootPath } from "./infra.js";
import { resolveCommand } from "./routing.js";
import {
	installScopedDefaultApplier,
	installScopedValueParser,
	type Occurrence,
	type Occurrences,
	type ParseProblem,
	parseScopes,
	STAGE,
	throwFirstProblem,
} from "./scopeparse.js";
import { SourcedStore, type SourceLabel } from "./sources.js";
import { evaluateUpdate, type UpdateState } from "./update.js";
import {
	appendListValue,
	coerceArgValue,
	coerceToScalar,
	findDuplicate,
	formatValueForError,
	parseDictValue,
	storeDictEntries,
	validateChoices,
} from "./values.js";

// --- Parameter naming ---

// Declared in factories.ts (where the declaration tree lives) and re-exported
// here, which is the import site the rest of the package already uses.
export { flagParamName };

// --- Config seam (Phase 5 fills; the default provider supplies no data) ---

export interface ConfigLoadResult {
	/** Param-name-keyed raw config values; null when no config file applies. */
	readonly data: Readonly<Record<string, unknown>> | null;
	/** Non-empty when the config file exists but cannot be parsed. */
	readonly parseErr?: string;
}

/**
 * Injectable config-values provider. Phase 5 implements file loading and
 * per-flag coercion; the parse pipeline owns precedence and conflict
 * semantics so they are already exact here.
 */
export interface ConfigProvider {
	load(runtimePathOverride: string | undefined): ConfigLoadResult;
	/** Coerces a raw config value to f's type; throws Error with the bare message. */
	coerce(f: AnyFlag, value: unknown): unknown;
	/**
	 * Step-2.5 config-field validation (Python _validate_config_fields):
	 * required bound fields must exist with the declared type, and every config
	 * key must be known. Returns an error message, or undefined when all pass
	 * (always undefined when the app declares no config fields).
	 */
	validateFields(
		cmdConfigFields: readonly string[],
		data: Readonly<Record<string, unknown>>,
	): string | undefined;
}

export const emptyConfigProvider: ConfigProvider = {
	load: () => ({ data: null }),
	coerce: () => {
		throw new Error("internal: no config provider installed");
	},
	validateFields: () => undefined,
};

/** Resolved per-parse config state threaded through the flag-resolution passes. */
interface ConfigContext {
	readonly data: Readonly<Record<string, unknown>> | null;
	readonly coerce: ConfigProvider["coerce"];
	readonly conflictMode: ConflictMode;
}

function coerceConfigValue(
	cfg: ConfigContext,
	f: AnyFlag,
	value: unknown,
): unknown {
	try {
		return cfg.coerce(f, value);
	} catch (e) {
		throw new ParseError(errConfigValueError(f.name, (e as Error).message));
	}
}

function effectiveConflictMode(cfg: ConfigContext, f: AnyFlag): string {
	return flagOpts(f).conflictMode ?? cfg.conflictMode;
}

/**
 * Conflict-mode equality (pinned by the siblings): scalars exact, plain lists
 * order-sensitive, unique flags order-insensitive multiset equality.
 */
export function valuesEqualForConflict(
	cliVal: unknown,
	configVal: unknown,
	f: AnyFlag,
): boolean {
	if (
		flagOpts(f).unique === true &&
		Array.isArray(cliVal) &&
		Array.isArray(configVal)
	) {
		return multisetEqual(cliVal, configVal);
	}
	return deepEqualValues(cliVal, configVal);
}

function deepEqualValues(a: unknown, b: unknown): boolean {
	if (Object.is(a, b)) {
		return true;
	}
	if (Array.isArray(a) && Array.isArray(b)) {
		return a.length === b.length && a.every((v, i) => deepEqualValues(v, b[i]));
	}
	if (a instanceof Map && b instanceof Map) {
		if (a.size !== b.size) {
			return false;
		}
		for (const [k, v] of a) {
			if (!b.has(k) || !deepEqualValues(v, b.get(k))) {
				return false;
			}
		}
		return true;
	}
	return false;
}

function multisetEqual(a: readonly unknown[], b: readonly unknown[]): boolean {
	if (a.length !== b.length) {
		return false;
	}
	// Keyed by type + rendered value, mirroring Go's "%T:%v" counting.
	const counts = new Map<string, number>();
	const keyOf = (v: unknown): string => `${typeof v}:${String(v)}`;
	for (const v of a) {
		counts.set(keyOf(v), (counts.get(keyOf(v)) ?? 0) + 1);
	}
	for (const v of b) {
		counts.set(keyOf(v), (counts.get(keyOf(v)) ?? 0) - 1);
	}
	for (const c of counts.values()) {
		if (c !== 0) {
			return false;
		}
	}
	return true;
}

// --- Flag token lookups ---

interface FlagLookups {
	readonly long: Map<string, AnyFlag>;
	readonly short: Map<string, AnyFlag>;
	readonly negation: Map<string, AnyFlag>;
	/**
	 * The clear vocabulary's minted spelling (contract §27.6). It is
	 * framework-owned and reaches the handler on the Context; it is NOT
	 * negatable, so `--no-unset-<x>` names nothing and is refused by the
	 * ordinary unknown-flag path.
	 */
	readonly unset: Map<string, AnyFlag>;
}

function isNegatableBool(f: AnyFlag): boolean {
	return requiredFlagForm(f) === "negatable-bool";
}

function addToLookups(lookups: FlagLookups, flags: readonly AnyFlag[]): void {
	for (const f of flags) {
		lookups.long.set(`--${f.name}`, f);
		const short = flagOpts(f).short;
		if (short !== undefined && short !== "") {
			lookups.short.set(`-${short}`, f);
		}
		if (isNegatableBool(f)) {
			lookups.negation.set(`--no-${f.name}`, f);
		}
		if (flagOpts(f).nullable === true) {
			lookups.unset.set(`--${unsetFlagName(f.name)}`, f);
		}
	}
}

function newLookups(flags: readonly AnyFlag[]): FlagLookups {
	const lookups: FlagLookups = {
		long: new Map(),
		short: new Map(),
		negation: new Map(),
		unset: new Map(),
	};
	addToLookups(lookups, flags);
	return lookups;
}

// --- Raw CLI value parsing (shared by global and command token loops) ---

/**
 * Parses one raw CLI value for a non-bool flag and stores it into cliSet,
 * handling dict merge (duplicate keys are hard errors), list append with
 * unique enforcement, and scalar coercion with @-prefix resolution.
 */
export function parseRawFlagValue(
	f: AnyFlag,
	raw: string,
	cliSet: Map<string, unknown>,
	tracker: StdinTracker,
): void {
	const kind = schemaKind(f.schema);
	const elem = elemSchemaOf(f.carrier);
	if (kind === "dict") {
		const entries = parseDictValue(f.name, raw, elem);
		let target = cliSet.get(f.name) as Map<string, unknown> | undefined;
		if (target === undefined) {
			target = new Map();
			cliSet.set(f.name, target);
		}
		storeDictEntries(target, entries, f.name);
		return;
	}
	const value = coerceToScalar(f.name, raw, elem, tracker);
	if (kind === "list") {
		let list = cliSet.get(f.name) as unknown[] | undefined;
		if (list === undefined) {
			list = [];
			cliSet.set(f.name, list);
		}
		appendListValue(list, value, flagOpts(f).unique === true, f.name);
		return;
	}
	cliSet.set(f.name, value);
}

// ONE coercion path for scoped and unscoped flags alike: a flag three scopes
// down merges dicts, appends to lists with unique enforcement and resolves
// @-prefixes exactly as a command-level flag does (contract §24.3's
// "unaffected by the construct" list).
installScopedValueParser(parseRawFlagValue);

// ONE declared-default path too: a scoped flag's RelativeToRoot default
// resolves through the app's infrastructure roots exactly as a root flag's
// does, and delivers the resolved path rather than the marker (§23, §24.6).
installScopedDefaultApplier((f, infraRoots) =>
	applyFlagDefault(f, "", infraRoots),
);

// --- Env and config resolution passes ---

function resolveEnvForFlags(
	flags: readonly AnyFlag[],
	cliSet: Map<string, unknown>,
	envNames: Set<string>,
	tracker: StdinTracker,
): void {
	for (const f of flags) {
		if (cliSet.has(f.name)) {
			continue;
		}
		// A connection-URL binding uses the declared connection env as the flag's
		// env source (registration enforces they are not both set). This loop is
		// only reached when !hermetic, so connection envs are hermetic-suppressed.
		const o = flagOpts(f);
		const envVar = o.env ?? o.connectionEnv;
		if (envVar === undefined) {
			continue;
		}
		const envVal = process.env[envVar];
		if (envVal === undefined) {
			continue;
		}
		cliSet.set(f.name, resolveEnvValue(f, envVar, envVal, tracker));
		envNames.add(f.name);
	}
}

/**
 * Applies config values to flags not set by CLI or env; in conflict mode
 * "error" a diverging config+cli/env overlap is a hard error. The
 * existingSource callback names the side config collided with.
 */
function applyConfigToFlags(
	flags: readonly AnyFlag[],
	cliSet: Map<string, unknown>,
	configNames: Set<string>,
	cfg: ConfigContext,
	existingSource: (f: AnyFlag) => string,
): void {
	const data = cfg.data;
	if (data === null) {
		return;
	}
	for (const f of flags) {
		const param = flagParamName(f.name);
		if (!Object.hasOwn(data, param)) {
			continue;
		}
		if (cliSet.has(f.name)) {
			// Conflict ONLY when the values diverge; identical values agree.
			if (effectiveConflictMode(cfg, f) === "error") {
				const coerced = coerceConfigValue(cfg, f, data[param]);
				if (!valuesEqualForConflict(cliSet.get(f.name), coerced, f)) {
					throw new ParseError(
						errFlagSetInBothAndConfig(f.name, existingSource(f)),
					);
				}
			}
			continue; // cli-wins, or error mode with matching values
		}
		const coerced = coerceConfigValue(cfg, f, data[param]);
		if (flagOpts(f).unique === true && Array.isArray(coerced)) {
			const dup = findDuplicate(coerced);
			if (dup !== undefined) {
				throw new ParseError(
					errConfigValueDuplicate(f.name, formatValueForError(dup)),
				);
			}
		}
		cliSet.set(f.name, coerced);
		configNames.add(f.name);
	}
}

// --- Defaults ---

interface DefaultedValue {
	readonly value: unknown;
	readonly source: SourceLabel;
}

/**
 * Resolves the value of a flag that was not provided by CLI, env, or config,
 * from its DECLARED presence (contract §23.1) -- nothing is inferred from the
 * shape of the default any more. There is no mutex-member exemption either:
 * the construct that needed one is deleted, and a choice's scoped flags are
 * resolved by the scope that elected them (contract §23.4's box, §24.1).
 *
 * Throws ParseError when the flag declares `required`. prefix is "" for
 * command flags and "global " for global flags. A relativeToRoot() marker
 * default resolves through the declared infra roots and reports source "infra"
 * (distinguishable from a plain default); hermetic mode never suppresses it
 * (roots were resolved at construction, with no argv dependency).
 * Exported for invoke.ts (programmatic invocation applies global defaults).
 */
export function applyFlagDefault(
	f: AnyFlag,
	prefix: string,
	infraRoots: ReadonlyMap<string, string>,
): DefaultedValue {
	const o = flagOpts(f);
	if (o.presence === "optional") {
		// Absence delivered AS absence, for every carrier: a compound flag that
		// wants an empty collection declares `default: []` / `default: new Map()`.
		return { value: undefined, source: "default" };
	}
	if (o.presence === "default") {
		const dflt = o.default;
		if (isInfraRootPath(dflt)) {
			try {
				return {
					value: resolveInfraRootPath(dflt, infraRoots),
					source: "infra",
				};
			} catch (e) {
				throw new ParseError(`${prefix}${(e as Error).message}`);
			}
		}
		const kind = schemaKind(f.schema);
		if (kind === "dict") {
			return {
				value: new Map(dflt as Map<string, unknown>),
				source: "default",
			};
		}
		if (kind === "list") {
			return { value: [...(dflt as unknown[])], source: "default" };
		}
		return { value: dflt, source: "default" };
	}
	// The one required-flag sentence, rendered from the declaration. The scoped
	// sites append their suffix to this same sentence (§12.13).
	throw new ParseError(
		`${prefix}${errFlagRequired(f.name, requiredFlagForm(f))}`,
	);
}

// --- Command parsing ---

export interface ParsedCommand {
	readonly kwargs: Record<string, unknown>;
	/** Global flag values parsed from post-command tokens, param-name-keyed. */
	readonly postGlobalValues: Record<string, unknown>;
	/** Param-name -> source label for command flags and post-command globals. */
	readonly sources: Record<string, string>;
	/**
	 * Conditional bindings whose scope was not elected, named one line per
	 * binding in declaration order (contract §24.6). Diagnostics, not errors:
	 * the run continues, and they are shown only under --verbose.
	 */
	readonly skippedBindings: readonly string[];
	/**
	 * The write set of a command declaring `updateOf` (contract §27.5), and
	 * null on every command that declares none. The would-do log's unnumbered
	 * line and the envelope's `writes` member both render it.
	 */
	readonly writes: UpdateState | null;
	/**
	 * The properties this invocation CLEARED (§27.6): `--unset-<prop>` on the
	 * command line, or `null` on the property's own key at a machine door.
	 * `ctx.unset` answers off it.
	 */
	readonly unsets: ReadonlySet<string>;
}

/**
 * The token-level surface of the whole declaration TREE: every name a scoped
 * flag, a token-spelled selector or a member-spelled election puts on the
 * command line. Root-level ordinary flags are NOT here -- they keep the
 * existing eager pipeline, which is what makes this construct cost nothing on
 * a command that declares no selector.
 */
interface ScopedTokenTarget {
	readonly name: string;
	readonly takesValue: boolean;
}

interface ScopedLookups {
	readonly long: Map<string, ScopedTokenTarget>;
	readonly short: Map<string, ScopedTokenTarget>;
	readonly negation: Map<string, ScopedTokenTarget>;
	/** True when the command declares no selector at all. */
	readonly empty: boolean;
}

function newScopedLookups(decls: readonly AnyDecl[]): ScopedLookups {
	const long = new Map<string, ScopedTokenTarget>();
	const short = new Map<string, ScopedTokenTarget>();
	const negation = new Map<string, ScopedTokenTarget>();
	const index = buildScopeIndex(decls);
	for (const [name, entries] of index) {
		const first = entries[0] as ScopeIndexEntry;
		// A root-level ordinary flag is not part of the scoped surface.
		const isScoped = entries.some(
			(e) => e.path.length > 0 || e.decl.kind === "choice-flag",
		);
		if (!isScoped) {
			continue;
		}
		const target: ScopedTokenTarget = {
			name,
			takesValue: first.takesValue,
		};
		long.set(`--${name}`, target);
		if (!first.takesValue) {
			// A payload-less member declines with `--no-<name>` (§21.2, carried
			// over to member spelling), and a scoped bool negates as any bool does.
			negation.set(`--no-${name}`, target);
		}
		for (const e of entries) {
			// A member election's token carries a short like any other scoped
			// token: the member flag IS what a reader types (§24.4).
			const sh = e.short;
			if (typeof sh === "string" && sh !== "") {
				short.set(`-${sh}`, target);
			}
		}
	}
	return { long, short, negation, empty: long.size === 0 };
}

/**
 * Records one raw occurrence of a scoped surface name, in command-line order.
 * `seq` is the occurrence's position in the whole token scan -- the same
 * counter root occurrences take -- so the value phase can report a scoped
 * coercion failure against a root one by the order they were TYPED (§24.3,
 * §18.28 item 262).
 */
function recordOccurrence(
	occ: Occurrences,
	name: string,
	raw: string | undefined,
	seq: number,
): void {
	const entry: Occurrence = occ.get(name) ?? { positive: [], negated: false };
	if (raw === undefined) {
		entry.negated = true;
	} else {
		entry.positive.push({ raw, seq });
	}
	occ.set(name, entry);
}

/**
 * Parses tokens against a resolved command's flags and args. Global flags are
 * also recognized in post-command tokens and returned separately so the
 * caller can merge them with pre-command globals. Throws ParseError.
 */
export function parseCommand(
	cmd: RegisteredCommand,
	tokens: readonly string[],
	globalFlags: readonly AnyFlag[],
	cfg: ConfigContext | null,
	tracker: StdinTracker,
	hermetic: boolean,
	infraRoots: ReadonlyMap<string, string>,
): ParsedCommand {
	if (cmd.def.kind !== "command") {
		throw new Error(
			"internal: parseCommand requires a non-passthrough command",
		);
	}
	const def = cmd.def;
	const lookups = newLookups(cmd.flags);
	addToLookups(lookups, globalFlags);
	const globalFlagNames = new Set(globalFlags.map((f) => f.name));
	const scoped = newScopedLookups(def.allDecls);

	const cliSet = new Map<string, unknown>();
	const positionals: string[] = [];
	// Phase 1: tokenize every occurrence WITHOUT interpreting any of it. The
	// scan decides SHAPE only -- which flag a token names, whether it consumes
	// the next argv element -- and records each root-scope occurrence
	// uninterpreted; every coercion runs afterwards, with the election and
	// scope phases in between. So a structural problem is reported ahead of a
	// value that will not parse whichever came first on the command line, on
	// every command rather than only on one that declares a selector (contract
	// §24.3, §18.19 item 224).
	const occ: Occurrences = new Map();
	const problems: ParseProblem[] = [];
	/** A structural verdict of the scan itself: it outranks every election. */
	const record = (message: string): void => {
		problems.push({ stage: STAGE.shape, message });
	};
	/**
	 * One root-scope occurrence, uninterpreted: a raw token, or a bool, plus
	 * the position the scan gave it.
	 */
	const rootOccs: {
		readonly f: AnyFlag;
		readonly raw: string | boolean;
		readonly seq: number;
	}[] = [];
	/**
	 * The scan's own counter, shared by root and scoped occurrences: the value
	 * phase is ONE sweep in command-line order over both (§24.3, §18.27 item
	 * 257), so the two kinds of occurrence must be positioned on one scale.
	 */
	let seq = 0;
	/** The properties `--unset-<prop>` cleared, in command-line order (§27.6). */
	const unsetOccs: string[] = [];
	const pushRoot = (f: AnyFlag, raw: string | boolean): void => {
		rootOccs.push({ f, raw, seq: seq++ });
	};
	const pushScoped = (name: string, raw: string | undefined): void => {
		recordOccurrence(occ, name, raw, seq++);
	};

	let i = 0;
	let stopFlags = false;
	while (i < tokens.length) {
		const tok = tokens[i] as string;

		if (stopFlags || !tok.startsWith("-") || tok === "-") {
			positionals.push(tok);
			i++;
			continue;
		}

		if (tok === "--") {
			stopFlags = true;
			i++;
			continue;
		}

		// --flag=value form
		if (tok.startsWith("--") && tok.includes("=")) {
			const eqPos = tok.indexOf("=");
			const flagPart = tok.slice(0, eqPos);
			const valuePart = tok.slice(eqPos + 1);
			const f = lookups.long.get(flagPart);
			const sc = scoped.long.get(flagPart);
			if (f !== undefined) {
				if (f.schema === "bool") {
					record(errBoolFlagNoValue(flagPart));
				} else {
					pushRoot(f, valuePart);
				}
			} else if (sc !== undefined) {
				if (!sc.takesValue) {
					record(errBoolFlagNoValue(flagPart));
				} else {
					pushScoped(sc.name, valuePart);
				}
			} else if (
				lookups.negation.has(flagPart) ||
				scoped.negation.has(flagPart)
			) {
				record(errBoolNegationNoValue(flagPart));
			} else {
				record(errUnknownFlag(flagPart));
			}
			i++;
			continue;
		}

		// --no-flag negation
		const negated = lookups.negation.get(tok);
		if (negated !== undefined) {
			pushRoot(negated, false);
			i++;
			continue;
		}
		const scopedNegated = scoped.negation.get(tok);
		if (scopedNegated !== undefined) {
			pushScoped(scopedNegated.name, undefined);
			i++;
			continue;
		}

		// --unset-flag: the clear vocabulary's minted spelling. It carries no
		// value of its own -- clearing is one act, not a value -- and it is
		// checked against the property's own occurrences below. The `=` form
		// above never consults this table: `--unset-x=v` names no declaration
		// and takes the ordinary unknown-flag path.
		const cleared = lookups.unset.get(tok);
		if (cleared !== undefined) {
			unsetOccs.push(cleared.name);
			i++;
			continue;
		}

		// --flag (long form without =)
		if (tok.startsWith("--")) {
			const f = lookups.long.get(tok);
			const sc = scoped.long.get(tok);
			if (f === undefined && sc === undefined) {
				record(errUnknownFlag(tok));
				i++;
				continue;
			}
			const takesValue =
				f !== undefined
					? f.schema !== "bool"
					: (sc as ScopedTokenTarget).takesValue;
			if (!takesValue) {
				if (f !== undefined) {
					pushRoot(f, true);
				} else {
					pushScoped((sc as ScopedTokenTarget).name, "true");
				}
				i++;
				continue;
			}
			if (i + 1 >= tokens.length) {
				record(errFlagRequiresValue(tok));
				i++;
				continue;
			}
			if (f !== undefined) {
				pushRoot(f, tokens[i + 1] as string);
			} else {
				pushScoped((sc as ScopedTokenTarget).name, tokens[i + 1] as string);
			}
			i += 2;
			continue;
		}

		// -x (short form)
		if (tok.length === 2) {
			const f = lookups.short.get(tok);
			const sc = scoped.short.get(tok);
			if (f !== undefined || sc !== undefined) {
				const takesValue =
					f !== undefined
						? f.schema !== "bool"
						: (sc as ScopedTokenTarget).takesValue;
				if (!takesValue) {
					if (f !== undefined) {
						pushRoot(f, true);
					} else {
						pushScoped((sc as ScopedTokenTarget).name, "true");
					}
					i++;
					continue;
				}
				if (i + 1 >= tokens.length) {
					record(errFlagRequiresValue(tok));
					i++;
					continue;
				}
				if (f !== undefined) {
					pushRoot(f, tokens[i + 1] as string);
				} else {
					pushScoped((sc as ScopedTokenTarget).name, tokens[i + 1] as string);
				}
				i += 2;
				continue;
			}
		}

		// Token starts with "-" but matches no known flag; treat as a
		// positional arg (e.g. negative numbers like -7, -3.14).
		positionals.push(tok);
		i++;
	}

	// A property written AND cleared in one invocation is refused BEFORE either
	// is read: the two tokens state opposite things about one property, and no
	// order of application makes one of them true (§27.6). The refusal is a
	// value-stage verdict that outranks every coercion -- position -1, since it
	// is decided before the first token is coerced rather than at any one
	// token's place in the line -- so a structural verdict still wins and an
	// election or scope refusal still runs first.
	const unsets = new Set(unsetOccs);
	if (unsets.size > 0) {
		for (const { f } of rootOccs) {
			if (unsets.has(f.name)) {
				problems.push({
					stage: STAGE.value,
					message: errUpdateValueAndUnset(f.name),
					seq: -1,
				});
				break;
			}
		}
	}

	// The value pass over the root-scope occurrences. It runs after the whole
	// scan, so a coercion failure can never outrank a structural verdict; each
	// failure carries the position of the token that produced it, so the value
	// stage reports root and scoped coercions in ONE command-line order rather
	// than root-first (contract §24.3, §18.28 item 262). Nothing here partitions
	// the two: the scopes' own value phase runs below and its failures are
	// positioned on the same scale.
	for (const { f, raw, seq } of rootOccs) {
		if (typeof raw === "boolean") {
			cliSet.set(f.name, raw);
			continue;
		}
		try {
			parseRawFlagValue(f, raw, cliSet, tracker);
		} catch (e) {
			if (e instanceof ParseError) {
				problems.push({ stage: STAGE.value, message: e.message, seq });
				continue;
			}
			throw e;
		}
	}

	// A cleared property is SUPPLIED: the value it delivers is absence, the same
	// undefined an untouched property delivers, and the two are told apart by
	// `provided` -- true here, because the invocation caused the write -- and by
	// `ctx.unset` (§27.6). Entering it in the map also stops env, config and the
	// declared-default step from filling the gap the clear opened.
	for (const name of unsets) {
		cliSet.set(name, undefined);
	}

	// Phases 2-4: elections outermost first, scope-membership validation, then
	// values and presence within the live scopes only.
	const scopeResult = parseScopes({
		decls: def.allDecls,
		occ,
		hermetic,
		cfg,
		tracker,
		infraRoots,
	});
	problems.push(...scopeResult.problems);
	throwFirstProblem(problems);

	const envNames = new Set<string>();
	const configNames = new Set<string>();

	if (!hermetic) {
		resolveEnvForFlags(cmd.flags, cliSet, envNames, tracker);
		if (cfg !== null) {
			applyConfigToFlags(cmd.flags, cliSet, configNames, cfg, (f) =>
				envNames.has(f.name) ? "env" : "cli",
			);
			// Config-conflict detection for GLOBAL flags parsed AFTER the command
			// name. Detection ONLY: config values for globals were already applied
			// during the pre-command pass. Globals reaching cliSet here are purely
			// CLI-parsed, so the divergence source is always "cli".
			if (cfg.data !== null) {
				for (const f of globalFlags) {
					if (!cliSet.has(f.name)) {
						continue;
					}
					const param = flagParamName(f.name);
					if (!Object.hasOwn(cfg.data, param)) {
						continue;
					}
					if (effectiveConflictMode(cfg, f) !== "error") {
						continue;
					}
					const coerced = coerceConfigValue(cfg, f, cfg.data[param]);
					if (!valuesEqualForConflict(cliSet.get(f.name), coerced, f)) {
						throw new ParseError(errFlagSetInBothCliAndConfig(f.name));
					}
				}
			}
		}
	}

	const store = new SourcedStore();
	for (const [k, v] of cliSet) {
		if (envNames.has(k)) {
			store.set(k, v, "env");
		} else if (configNames.has(k)) {
			store.set(k, v, "config");
		} else {
			store.set(k, v, "cli");
		}
	}

	// The elected records enter the same sourced store every flag value uses,
	// so ctx.source and ctx.provided answer for a selector's own key exactly
	// as they do for any flag (§24.5, §24.9).
	for (const [name, value] of scopeResult.records) {
		store.set(name, value, scopeResult.sources.get(name) ?? "cli");
	}

	return validateAndBuildKwargs(
		cmd,
		def.args,
		store,
		{ kind: "tokens", values: positionals },
		globalFlagNames,
		infraRoots,
		[...scopeResult.records.keys()],
		scopeResult.skippedBindings,
		unsets,
	);
}

/**
 * The positional values reaching the pipeline, and how they were produced
 * (contract §24.11). `tokens` are argv strings the parser must READ, in the
 * ORDER they were typed; a `pre-typed` value comes from a programmatic front
 * door already of the declared type, is CHECKED against the declaration
 * instead, and is carried BY NAME -- a kwargs object has no order of its own
 * (§21.4), so the key is the binding and an absent key is the absence presence
 * answers (§24.11 item 248). The caller says which it has: the pipeline never
 * guesses from the value's runtime type, because "already a string" is exactly
 * what a str declaration expects and what an int declaration must refuse.
 */
export type Positionals =
	| { readonly kind: "tokens"; readonly values: readonly string[] }
	| {
			readonly kind: "pre-typed";
			readonly byName: ReadonlyMap<string, unknown>;
	  };

/**
 * One positional value becomes the type its arg declares. A token is parsed;
 * a pre-typed value is checked, with the arg's own "argument '<name>':"
 * prefix over the sentences the config reader uses (§24.11, §23.4).
 *
 * *Pre-typed* means ALREADY OF THE DECLARED TYPE, never exempt from the
 * declaration: an arg is a declaration exactly as a flag is, so a value
 * supplied for one is checked exactly as a flag's is, and nothing is
 * stringified on the way in.
 */
function coercePositional(
	a: AnyArg,
	raw: unknown,
	kind: Positionals["kind"],
): unknown {
	const schema = elemSchemaOf(a.carrier);
	if (kind === "tokens") {
		return coerceArgValue(a.name, raw as string, schema);
	}
	try {
		return coerceConfigScalarLong(widenJsonIntegers(raw), schema);
	} catch (e) {
		throw new ParseError(errArgumentWrapped(a.name, (e as Error).message));
	}
}

/**
 * Argv's positionals: the tokens bind to the args IN ORDER, because order is
 * the only thing a command line has to say which arg a token is for.
 */
function resolveArgTokens(
	args: readonly AnyArg[],
	tokens: readonly string[],
	argValues: Map<string, unknown>,
): void {
	const lastArg =
		args.length > 0 ? (args[args.length - 1] as AnyArg) : undefined;
	const hasVariadic = lastArg?.opts.variadic === true;
	const fixedArgs = hasVariadic ? args.slice(0, -1) : args;
	fixedArgs.forEach((a, idx) => {
		if (idx < tokens.length) {
			argValues.set(a.name, coercePositional(a, tokens[idx], "tokens"));
		} else if (a.opts.presence === "required") {
			throw new ParseError(errMissingRequiredArgument(a.name));
		} else if (a.opts.presence === "default") {
			argValues.set(a.name, a.opts.default);
		} else {
			// An optional arg delivers absence as a PRESENT key holding undefined
			// (contract §23.3): key-absence delivery was rejected for the round.
			argValues.set(a.name, undefined);
		}
	});
	if (hasVariadic && lastArg !== undefined) {
		const remaining = tokens.slice(fixedArgs.length);
		if (lastArg.opts.presence === "required" && remaining.length === 0) {
			throw new ParseError(errMissingRequiredArgument(lastArg.name));
		}
		argValues.set(
			lastArg.name,
			remaining.map((p) => coercePositional(lastArg, p, "tokens")),
		);
	} else if (tokens.length > args.length) {
		throw new ParseError(errUnexpectedArgument(String(tokens[args.length])));
	}
}

/**
 * A programmatic door's positionals: each value binds to the arg its KEY NAMES
 * (§24.11 item 248). A kwargs object has no order of its own (§21.4), so
 * position is not a binding a caller can express here -- reading the supplied
 * subset densely would hand a value the caller wrote under one name to whatever
 * arg the omissions left in that slot, and then refuse it in the name of an arg
 * nobody supplied.
 *
 * Presence is answered by the key's ABSENCE, exactly as the argv path answers
 * it with a token that was never typed: an omitted optional arg is delivered as
 * a present key holding absence, an omitted defaulted one takes its declared
 * default, and an omitted required one keeps the argv path's own sentence.
 *
 * A variadic arg is a SEQUENCE of positionals rather than one value of a
 * collection type, so an array is one element per entry -- each checked on its
 * own -- and anything else is the single element it looks like.
 */
function resolvePreTypedArgs(
	args: readonly AnyArg[],
	byName: ReadonlyMap<string, unknown>,
	argValues: Map<string, unknown>,
): void {
	for (const a of args) {
		if (byName.has(a.name)) {
			const raw = byName.get(a.name);
			if (a.opts.variadic === true) {
				const items = Array.isArray(raw) ? raw : [raw];
				if (items.length === 0 && a.opts.presence === "required") {
					// An empty array is the flat spelling of no tokens at all.
					throw new ParseError(errMissingRequiredArgument(a.name));
				}
				argValues.set(
					a.name,
					items.map((item) => coercePositional(a, item, "pre-typed")),
				);
				continue;
			}
			argValues.set(a.name, coercePositional(a, raw, "pre-typed"));
			continue;
		}
		if (a.opts.presence === "required") {
			throw new ParseError(errMissingRequiredArgument(a.name));
		}
		if (a.opts.variadic === true) {
			argValues.set(a.name, []);
			continue;
		}
		if (a.opts.presence === "default") {
			argValues.set(a.name, a.opts.default);
			continue;
		}
		argValues.set(a.name, undefined);
	}
}

// --- The constraint set at parse time (contract §26.4, §26.9) ---

/** One positional arg's engagement facts, the arg-side of §26.3's predicate. */
interface ArgEngagement {
	/**
	 * The invocation supplied a positional token for it, or a key for it at a
	 * machine door -- never the declaration's default and never an optional
	 * absence. For a VARIADIC arg, provided means at least one element, which
	 * is why an explicitly supplied empty array is not a provision: the
	 * implementations already read an empty array as "no tokens at all".
	 */
	readonly provided: boolean;
	/** Non-empty by §26.3's sizes; equal to `provided` on a variadic arg. */
	readonly nonEmpty: boolean;
}

/**
 * The arg-side provided-ness the flag-side sourced store cannot answer: it
 * holds flags only. Computed from the RAW positionals rather than from the
 * resolved values, so it gives the same answer at the argv door and at both
 * machine doors without moving arg coercion ahead of the constraint check --
 * which would report a bad positional's type before the missing half of a
 * pair (§18.29 items 267-268).
 */
function argEngagements(
	args: readonly AnyArg[],
	positionals: Positionals,
): ReadonlyMap<string, ArgEngagement> {
	const out = new Map<string, ArgEngagement>();
	const sized = (v: unknown): boolean => {
		if (typeof v === "string") {
			return v.length > 0;
		}
		if (Array.isArray(v)) {
			return v.length > 0;
		}
		if (v instanceof Map) {
			return v.size > 0;
		}
		return v !== undefined && v !== null;
	};
	if (positionals.kind === "pre-typed") {
		for (const a of args) {
			if (!positionals.byName.has(a.name)) {
				out.set(a.name, { provided: false, nonEmpty: false });
				continue;
			}
			const raw = positionals.byName.get(a.name);
			if (a.opts.variadic === true) {
				const items = Array.isArray(raw) ? raw : [raw];
				const provided = items.length > 0;
				out.set(a.name, { provided, nonEmpty: provided });
				continue;
			}
			out.set(a.name, { provided: true, nonEmpty: sized(raw) });
		}
		return out;
	}
	const tokens = positionals.values;
	const lastArg =
		args.length > 0 ? (args[args.length - 1] as AnyArg) : undefined;
	const hasVariadic = lastArg?.opts.variadic === true;
	const fixedArgs = hasVariadic ? args.slice(0, -1) : args;
	fixedArgs.forEach((a, idx) => {
		const supplied = idx < tokens.length;
		out.set(a.name, {
			provided: supplied,
			nonEmpty: supplied && sized(tokens[idx]),
		});
	});
	if (hasVariadic && lastArg !== undefined) {
		const provided = tokens.slice(fixedArgs.length).length > 0;
		out.set(lastArg.name, { provided, nonEmpty: provided });
	}
	return out;
}

/**
 * Evaluates every constraint of a command: children before parents, siblings
 * in declaration order (§26.4). A violated nested constraint reports its own
 * sentence and its parent is never evaluated -- the only order that reports
 * the fixable fact, since an operator who typed one half of a pair must be
 * told the pair is incomplete rather than that the whole selection is
 * missing.
 */
function evaluateConstraints(
	def: AnyCommand,
	store: SourcedStore,
	args: readonly AnyArg[],
	positionals: Positionals,
): void {
	if (def.constraints.length === 0) {
		return;
	}
	const resolved = resolveConstraints(def);
	const index = constraintIndex(resolved);
	const argFacts = argEngagements(args, positionals);

	const probe: EngagementProbe = (m) => {
		if (m.kind === "arg") {
			const facts = argFacts.get(m.name);
			if (facts === undefined) {
				return false;
			}
			return m.when === "non_empty" ? facts.nonEmpty : facts.provided;
		}
		if (!store.isPresentForDeps(m.name)) {
			return false;
		}
		const v = store.get(m.name);
		if (m.when === "true") {
			return v === true;
		}
		if (m.when === "non_empty") {
			if (typeof v === "string") {
				return v.length > 0;
			}
			if (Array.isArray(v)) {
				return v.length > 0;
			}
			if (v instanceof Map) {
				return v.size > 0;
			}
			return v !== undefined && v !== null;
		}
		return true;
	};

	/**
	 * §21.4's decline clause verbatim, appended when a bool member declaring
	 * `when: "true"` was provided as FALSE, naming the first such member in
	 * declaration order. Reusing it is not a claim that at-least-one is
	 * exclusivity: the clause is about a negated bool, which is the same fact
	 * under both constructs (§12.15).
	 */
	const declineClause = (c: ResolvedCoOccurrence): string => {
		for (const m of c.members) {
			if (m.kind !== "flag" || m.when !== "true") {
				continue;
			}
			if (store.isPresentForDeps(m.name) && store.get(m.name) === false) {
				return errMutexDeclineClause(m.name);
			}
		}
		return "";
	};

	const settled = new Set<string>();
	const check = (c: ResolvedConstraint): void => {
		if (settled.has(c.name)) {
			return;
		}
		settled.add(c.name);
		if (c.kind === "requires") {
			if (
				store.isPresentForDeps(c.flag) &&
				!store.isPresentForDeps(c.dependsOn)
			) {
				throw new ParseError(errFlagRequiresFlag(c.name, c.flag, c.dependsOn));
			}
			return;
		}
		if (c.kind === "implies") {
			// The conflict fired during injection; there is nothing left to test.
			return;
		}
		// Children first, in declaration order.
		for (const m of c.members) {
			if (m.kind !== "constraint") {
				continue;
			}
			const nested = index.get(m.name);
			if (nested !== undefined) {
				check(nested);
			}
		}
		const engaged = c.members.filter((m) => isEngaged(m, index, probe));
		if (c.kind === "at-least-one") {
			// A vacuous at-least-one is violated, which is the whole of what it
			// says. A nested member counts toward its parent only when engaged,
			// so two vacuous pairs leave the parent unsatisfied (§26.4).
			if (engaged.length === 0) {
				throw new ParseError(
					errAtLeastOneRequired(
						c.name,
						renderMembers(c.members, index, "cli"),
						declineClause(c),
					),
				);
			}
			return;
		}
		// all-or-none: every member engaged, or none. With nothing engaged it
		// is VACUOUSLY true -- the "none" half of its own name.
		if (engaged.length > 0 && engaged.length < c.members.length) {
			throw new ParseError(
				errAllOrNoneTogether(c.name, renderMembers(c.members, index, "cli")),
			);
		}
	};

	for (const c of resolved) {
		check(c);
	}
}

/**
 * Second half of command parsing: implies resolution,
 * dependency checks, defaults, choices, custom validation, positional-arg
 * resolution, and kwargs assembly, all on sourced values. Exported for
 * invoke.ts, which feeds it a store populated from pre-typed kwargs.
 */
export function validateAndBuildKwargs(
	cmd: RegisteredCommand,
	args: readonly AnyArg[],
	store: SourcedStore,
	positionals: Positionals,
	globalFlagNames: ReadonlySet<string>,
	infraRoots: ReadonlyMap<string, string>,
	selectorNames: readonly string[] = [],
	skippedBindings: readonly string[] = [],
	unsets: ReadonlySet<string> = new Set(),
): ParsedCommand {
	if (cmd.def.kind !== "command") {
		throw new Error("internal: passthrough commands are not parsed");
	}
	const def = cmd.def;

	// The mutex block that stood here is DELETED with the construct (contract
	// §21's supersession box). Its three sentences survive as member spelling's
	// errors, evaluated by the election phase in scopeparse.ts -- where the
	// scope tree, not a group, decides which alternative is live.

	// Implies resolution, before every other constraint, so an implied value
	// can engage a member (§26.4). Its injection order is untouched by the
	// constraint round (§26.13).
	for (const dep of def.constraints) {
		if (dep.kind !== "implies") {
			continue;
		}
		if (!store.isPresentForDeps(dep.flag)) {
			continue;
		}
		if (store.has(dep.implies)) {
			if (store.get(dep.implies) !== dep.value) {
				const neg = dep.value ? "" : "no-";
				const explicitNeg = dep.value ? "no-" : "";
				throw new ParseError(
					errImpliesConflict(dep.name, dep.flag, neg, dep.implies, explicitNeg),
				);
			}
		} else {
			store.set(dep.implies, dep.value, "implied");
		}
	}

	// The constraint set: cli, env, config and implied engage; a declared
	// default does not (§26.3, §23.6). Evaluated BEFORE defaults are applied,
	// exactly where the dependency families ran.
	evaluateConstraints(def, store, args, positionals);

	// The at-least-one-property rule, and the write set it guarantees is never
	// empty (contract §27.4, §27.5). It is the framework's own rule about a
	// declared property set rather than a constraint the command wrote, so it
	// is evaluated after every declared constraint has had its say -- and, like
	// them, BEFORE defaults are applied, over the one provided-ness predicate
	// with no source filter.
	const writes = evaluateUpdate(def, store, unsets);

	// Defaults. Every flag resolves from its own declared presence: the
	// exemption that handed a mutex member an absent value its declaration
	// never asked for is deleted (contract §23.4).
	for (const f of cmd.flags) {
		if (store.has(f.name)) {
			continue;
		}
		const { value, source } = applyFlagDefault(f, "", infraRoots);
		store.set(f.name, value, source);
	}

	// Choices
	for (const f of cmd.flags) {
		if (store.has(f.name)) {
			const declared = flagOpts(f).choices;
			validateChoices(
				f.name,
				store.get(f.name),
				schemaKind(f.schema) === "list",
				declared === undefined ? undefined : choiceValues(declared),
				flagOpts(f).retiredChoices,
				false,
			);
		}
	}

	// Custom validation. It runs on a SUPPLIED value only (§23.5's validate
	// row): never on absence, and never on a declared default -- a default is
	// the declaration's own value, already the author's to get right, and
	// isPresentForDeps is the one predicate that says the invocation caused
	// the value.
	for (const f of cmd.flags) {
		const validate = flagOpts(f).validate;
		if (validate === undefined || !store.isPresentForDeps(f.name)) {
			continue;
		}
		const val = store.get(f.name);
		const check = (v: unknown): void => {
			try {
				validate(v as never);
			} catch (e) {
				throw new ParseError(errFlagValueError(f.name, (e as Error).message));
			}
		};
		if (schemaKind(f.schema) === "list") {
			if (Array.isArray(val)) {
				for (const v of val) {
					check(v);
				}
			}
		} else if (val !== undefined && val !== null) {
			check(val);
		}
	}

	// Positional args
	const argValues = new Map<string, unknown>();
	if (positionals.kind === "pre-typed") {
		resolvePreTypedArgs(args, positionals.byName, argValues);
	} else {
		resolveArgTokens(args, positionals.values, argValues);
	}

	// Arg choices (after type coercion)
	for (const a of args) {
		if (argValues.has(a.name)) {
			const opts = a.opts as ArgOptsView;
			validateChoices(
				a.name,
				argValues.get(a.name),
				a.opts.variadic === true,
				opts.choices === undefined ? undefined : choiceValues(opts.choices),
				opts.retiredChoices,
				true,
			);
		}
	}

	// kwargs (command flags, selectors and args only; doParse merges globals)
	const kwargs: Record<string, unknown> = {};
	for (const f of cmd.flags) {
		kwargs[flagParamName(f.name)] = store.get(f.name);
	}
	// A selector contributes exactly ONE key -- its own -- at any depth, which
	// is what keeps §23's delivery invariant untouched rather than merely
	// compatible: sub-flags are never top-level handler arguments (§24.1).
	for (const name of selectorNames) {
		kwargs[flagParamName(name)] = store.get(name);
	}
	for (const a of args) {
		if (argValues.has(a.name)) {
			kwargs[a.name] = argValues.get(a.name);
		}
	}

	const postGlobalValues: Record<string, unknown> = {};
	for (const name of globalFlagNames) {
		if (store.has(name)) {
			postGlobalValues[flagParamName(name)] = store.get(name);
		}
	}

	const rawSources = store.sourceMap();
	const sources: Record<string, string> = {};
	for (const f of cmd.flags) {
		const s = rawSources.get(f.name);
		if (s !== undefined) {
			sources[flagParamName(f.name)] = s;
		}
	}
	for (const name of selectorNames) {
		const s = rawSources.get(name);
		if (s !== undefined) {
			sources[flagParamName(name)] = s;
		}
	}
	// Globals parsed post-command emit their source label too (always "cli"
	// here; env/config for globals resolve in the pre-command pass). Without
	// this, `tool cmd --global X` would report source "default".
	for (const name of globalFlagNames) {
		const s = rawSources.get(name);
		if (s !== undefined) {
			sources[flagParamName(name)] = s;
		}
	}

	return { kwargs, postGlobalValues, sources, skippedBindings, writes, unsets };
}

// --- Global flag extraction (pre-command phase) ---

export interface ExtractedGlobals {
	/** Param-name-keyed resolved global flag values. */
	readonly values: Record<string, unknown>;
	/** Param-name -> source label. */
	readonly sources: Record<string, string>;
	/** Tokens from the first non-global token onward (command region). */
	readonly remaining: readonly string[];
}

/**
 * Scans argv for global flag tokens before the command name. Stops at the
 * first non-flag token (the command name), at "--" (kept in remaining), or at
 * an unknown flag-like token. Resolves env, config, defaults, and choices for
 * global flags. Throws ParseError.
 */
export function extractGlobalFlags(
	app: AppImpl,
	argv: readonly string[],
	hermetic: boolean,
	tracker: StdinTracker,
	cfg: ConfigContext | null,
): ExtractedGlobals {
	if (app.globalFlags.length === 0) {
		return { values: {}, sources: {}, remaining: argv };
	}
	const lookups = newLookups(app.globalFlags);
	const cliSet = new Map<string, unknown>();

	let remaining: readonly string[] | null = null;
	let i = 0;
	while (i < argv.length) {
		const tok = argv[i] as string;

		// -- stops global flag parsing; include it in remaining
		if (tok === "--") {
			remaining = argv.slice(i);
			break;
		}

		// --flag=value form
		if (tok.startsWith("--") && tok.includes("=")) {
			const eqPos = tok.indexOf("=");
			const flagPart = tok.slice(0, eqPos);
			const valuePart = tok.slice(eqPos + 1);
			const f = lookups.long.get(flagPart);
			if (f !== undefined) {
				if (f.schema === "bool") {
					throw new ParseError(errBoolFlagNoValue(flagPart));
				}
				parseRawFlagValue(f, valuePart, cliSet, tracker);
				i++;
				continue;
			}
			if (lookups.negation.has(flagPart)) {
				throw new ParseError(errBoolNegationNoValue(flagPart));
			}
			// Not a global flag -- this is the command region.
			remaining = argv.slice(i);
			break;
		}

		// --no-flag negation
		const negated = lookups.negation.get(tok);
		if (negated !== undefined) {
			cliSet.set(negated.name, false);
			i++;
			continue;
		}

		// --flag (long form)
		const longFlag = tok.startsWith("--") ? lookups.long.get(tok) : undefined;
		if (longFlag !== undefined) {
			if (longFlag.schema === "bool") {
				cliSet.set(longFlag.name, true);
				i++;
			} else {
				if (i + 1 >= argv.length) {
					throw new ParseError(errFlagRequiresValue(tok));
				}
				parseRawFlagValue(longFlag, argv[i + 1] as string, cliSet, tracker);
				i += 2;
			}
			continue;
		}

		// -x (short form)
		const shortFlag =
			tok.startsWith("-") && tok.length === 2
				? lookups.short.get(tok)
				: undefined;
		if (shortFlag !== undefined) {
			if (shortFlag.schema === "bool") {
				cliSet.set(shortFlag.name, true);
				i++;
			} else {
				if (i + 1 >= argv.length) {
					throw new ParseError(errFlagRequiresValue(tok));
				}
				parseRawFlagValue(shortFlag, argv[i + 1] as string, cliSet, tracker);
				i += 2;
			}
			continue;
		}

		// Not a global flag -- command name or unknown token.
		remaining = argv.slice(i);
		break;
	}
	if (remaining === null) {
		remaining = [];
	}

	const sources: Record<string, string> = {};
	for (const name of cliSet.keys()) {
		sources[flagParamName(name)] = "cli";
	}

	if (!hermetic) {
		const envNames = new Set<string>();
		resolveEnvForFlags(app.globalFlags, cliSet, envNames, tracker);
		for (const name of envNames) {
			sources[flagParamName(name)] = "env";
		}
		if (cfg !== null) {
			const configNames = new Set<string>();
			applyConfigToFlags(
				app.globalFlags,
				cliSet,
				configNames,
				cfg,
				(f) => sources[flagParamName(f.name)] ?? "cli",
			);
			for (const name of configNames) {
				sources[flagParamName(name)] = "config";
			}
		}
	}

	// Defaults for global flags not set anywhere
	for (const f of app.globalFlags) {
		if (cliSet.has(f.name)) {
			continue;
		}
		const { value, source } = applyFlagDefault(f, "global ", app.infraRoots);
		cliSet.set(f.name, value);
		sources[flagParamName(f.name)] = source;
	}

	// Choices for global flags
	for (const f of app.globalFlags) {
		if (cliSet.has(f.name)) {
			const declared = flagOpts(f).choices;
			validateChoices(
				f.name,
				cliSet.get(f.name),
				schemaKind(f.schema) === "list",
				declared === undefined ? undefined : choiceValues(declared),
				flagOpts(f).retiredChoices,
				false,
			);
		}
	}

	const values: Record<string, unknown> = {};
	for (const [name, v] of cliSet) {
		values[flagParamName(name)] = v;
	}
	return { values, sources, remaining };
}

// --- Reserved-flag pre-scan ---

export interface PreScanResult {
	readonly dumpSchema: boolean;
	readonly serveMcp: boolean;
	readonly hermetic: boolean;
	readonly configPath: string | undefined;
	readonly err: string | undefined;
	/** The effects-regime reserved quartet, delivered on the Context. */
	readonly dryRun: boolean;
	readonly approveConsequential: boolean;
	readonly quiet: boolean;
	readonly verbose: boolean;
	/** Machine mode (contract §19.1), reserved beside the quartet. */
	readonly json: boolean;
	/** argv with --config/--config=value/--hermetic/the quartet stripped out. */
	readonly cleanedArgv: readonly string[];
}

/**
 * argv token -> pre-scan result key. The quartet plus --json, which reads
 * exactly as the quartet does in BOTH argv regions (contract §7.1's amendment,
 * §7.2) without joining the set.
 */
const RESERVED_QUARTET_TOKENS: ReadonlyMap<
	string,
	"dryRun" | "approveConsequential" | "quiet" | "verbose" | "json"
> = new Map([
	["--dry-run", "dryRun" as const],
	["--approve-consequential", "approveConsequential" as const],
	["--quiet", "quiet" as const],
	["--verbose", "verbose" as const],
	["--json", "json" as const],
]);

/**
 * Pre-scan for the framework-owned reserved flags. Two regions, two rulesets
 * (contract §7.2, amended):
 *
 * - The pre-command region -- before the first non-flag token, before "--" --
 *   recognizes every reserved flag (--dump-schema, --mcp, --config, --hermetic
 *   and the quartet). Known global flags and their values are skipped so a
 *   global-flag value that looks like a command name does not end it early.
 * - The command region recognizes ONLY the quartet
 *   (--dry-run/--approve-consequential/--quiet/--verbose), anywhere, exactly
 *   like --help/-h.
 *   --hermetic/--config/--dump-schema/--mcp stay pre-command-only. See
 *   scanCommandRegionQuartet.
 *
 * The quartet is stripped from argv here and delivered on the Context, never as
 * handler kwargs -- injecting four mandatory parameters into every handler
 * would contradict guard v2, and the Context needs the values for its own
 * output gating regardless.
 */
export function preScanReservedFlags(
	app: AppImpl,
	argv: readonly string[],
): PreScanResult {
	const knownFlags = new Map<string, boolean>(); // token -> takes a value
	for (const f of app.globalFlags) {
		const takesValue = f.schema !== "bool";
		knownFlags.set(`--${f.name}`, takesValue);
		const short = flagOpts(f).short;
		if (short !== undefined && short !== "") {
			knownFlags.set(`-${short}`, takesValue);
		}
		if (isNegatableBool(f)) {
			knownFlags.set(`--no-${f.name}`, false);
		}
	}

	let hermetic = false;
	let configPath: string | undefined;
	const quartet = {
		dryRun: false,
		approveConsequential: false,
		quiet: false,
		verbose: false,
		json: false,
	};
	const excludeIndices = new Set<number>();
	const done = (err?: string): PreScanResult => {
		const cleanedArgv =
			excludeIndices.size > 0
				? argv.filter((_, j) => !excludeIndices.has(j))
				: argv;
		return {
			dumpSchema: false,
			serveMcp: false,
			hermetic,
			configPath,
			err,
			...quartet,
			cleanedArgv,
		};
	};

	// Index where the command region begins; -1 means "never reached one"
	// (a bare -- or an unknown flag-like token ended the scan for good).
	let commandRegionFrom = -1;
	let i = 0;
	while (i < argv.length) {
		const tok = argv[i] as string;

		// -- terminates the whole scan: everything after it is data
		if (tok === "--") {
			break;
		}
		// Non-flag token = the command token: the command region starts here
		if (!tok.startsWith("-") || tok === "-") {
			commandRegionFrom = i;
			break;
		}

		if (tok === "--dump-schema") {
			return { ...done(), dumpSchema: true };
		}
		if (tok === "--mcp") {
			return { ...done(), serveMcp: true };
		}
		if (tok === "--hermetic") {
			hermetic = true;
			excludeIndices.add(i);
			i++;
			continue;
		}
		// The reserved quartet: booleans, no values, stripped from argv and
		// delivered on the Context (never as handler kwargs).
		const quartetKey = RESERVED_QUARTET_TOKENS.get(tok);
		if (quartetKey !== undefined) {
			quartet[quartetKey] = true;
			excludeIndices.add(i);
			i++;
			continue;
		}
		if (tok.startsWith("--config=")) {
			if (!app.configEnabled) {
				return done(
					"--config is not available: this app does not use config files",
				);
			}
			const val = tok.slice("--config=".length);
			if (val === "") {
				return done(errFlagRequiresValue("--config"));
			}
			configPath = val;
			excludeIndices.add(i);
			i++;
			continue;
		}
		if (tok === "--config") {
			if (!app.configEnabled) {
				return done(
					"--config is not available: this app does not use config files",
				);
			}
			if (i + 1 >= argv.length) {
				return done(errFlagRequiresValue("--config"));
			}
			configPath = argv[i + 1] as string;
			excludeIndices.add(i);
			excludeIndices.add(i + 1);
			i += 2;
			continue;
		}

		// Known global flag with --flag=value form: skip
		if (tok.startsWith("--") && tok.includes("=")) {
			const flagPart = tok.slice(0, tok.indexOf("="));
			if (knownFlags.has(flagPart)) {
				i++;
				continue;
			}
			break; // unknown flag-like token before command name: stop
		}

		// Known global flag: skip it (and its value if non-bool)
		const takesValue = knownFlags.get(tok);
		if (takesValue !== undefined) {
			i += takesValue ? 2 : 1;
			continue;
		}

		break; // unknown flag-like token before command name: stop
	}

	if (commandRegionFrom >= 0) {
		scanCommandRegionQuartet(
			app,
			argv,
			commandRegionFrom,
			quartet,
			excludeIndices,
		);
	}

	return done();
}

/**
 * Recognizes the reserved quartet in the command region of argv.
 *
 * Contract §7.2 (amended 2026-08-04): the quartet's four tokens are
 * recognized ANYWHERE in argv, exactly like --help/-h, because their
 * applicability is per-command -- requiring them before the command name was
 * backwards. Only the quartet is recognized here; --hermetic, --config,
 * --dump-schema and --mcp remain pre-command-only.
 *
 * The scan stops for good at two boundaries:
 *
 * - a bare "--", after which every token is positional data;
 * - a passthrough command's name, after which every token belongs to the child
 *   process and is forwarded byte-for-byte. Eating a child's own --verbose
 *   would silently change what the child does.
 *
 * Routing tokens are walked through the group/command tree so a quartet token
 * may sit anywhere among them. Nothing here throws: routing failures are the
 * real parse's job.
 *
 * Both boundaries are visible in the `dryRunSupported: false` refusal, which
 * reads the flag this scan resolved. `app cmd -- --dry-run` and
 * `app passthrough --dry-run` are NOT refused, because in neither case did the
 * operator ask this app for a dry run: after `--` the token is the command's
 * own data, and after a passthrough's name it is the child process's flag.
 * `app --dry-run passthrough` IS refused -- there the token is unambiguously
 * addressed to this app.
 */
function scanCommandRegionQuartet(
	app: AppImpl,
	argv: readonly string[],
	start: number,
	quartet: {
		dryRun: boolean;
		approveConsequential: boolean;
		quiet: boolean;
		verbose: boolean;
		json: boolean;
	},
	excludeIndices: Set<number>,
): void {
	let groups: ReadonlyMap<string, GroupImpl> = app.groups;
	let commands: ReadonlyMap<string, RegisteredCommand> = app.commands;
	let routingDone = false;

	for (let i = start; i < argv.length; i++) {
		const tok = argv[i] as string;

		if (tok === "--") {
			return;
		}

		if (tok.startsWith("-") && tok !== "-") {
			const key = RESERVED_QUARTET_TOKENS.get(tok);
			if (key !== undefined) {
				quartet[key] = true;
				excludeIndices.add(i);
			}
			continue;
		}

		// A non-flag token: a routing token until routing resolves.
		if (!routingDone) {
			const grp = groups.get(tok);
			if (grp !== undefined) {
				groups = grp.groups;
				commands = grp.commands;
				continue;
			}
			const cmd = commands.get(tok);
			if (cmd !== undefined && cmd.kind === "passthrough") {
				return;
			}
			// Resolved a normal command, or hit an unknown/deprecated token the
			// real parse will report: routing is over either way.
			routingDone = true;
		}
	}
}

/** Narrows a pre-scan result to the Context-delivered flag values. */
export function reservedFlagsOf(pre: PreScanResult): ReservedFlags {
	return {
		dryRun: pre.dryRun,
		approveConsequential: pre.approveConsequential,
		quiet: pre.quiet,
		verbose: pre.verbose,
		json: pre.json,
	};
}

// --- doParse ---

export type HelpTarget =
	| { readonly level: "app" }
	| {
			readonly level: "group";
			readonly group: GroupImpl;
			readonly path: readonly string[];
	  }
	| {
			readonly level: "command";
			readonly cmd: RegisteredCommand;
			readonly path: readonly string[];
	  };

export type ParseOutcome =
	| { readonly kind: "help"; readonly target: HelpTarget }
	| { readonly kind: "version"; readonly text: string }
	| { readonly kind: "dump-schema" }
	| { readonly kind: "mcp" }
	| {
			readonly kind: "parse-error";
			readonly message: string;
			readonly commandPrefix?: string;
			/**
			 * The reserved-flag state the pre-scan resolved, carried so a run
			 * that ended before a command resolved still knows whether it is in
			 * machine mode and owes an envelope (contract §19.2).
			 */
			readonly reserved: ReservedFlags;
	  }
	| {
			readonly kind: "command";
			readonly cmd: RegisteredCommand;
			readonly cmdPath: string;
			readonly kwargs: Record<string, unknown>;
			readonly globalKwargs: Record<string, unknown>;
			readonly sources: Record<string, string>;
			readonly hermetic: boolean;
			readonly reserved: ReservedFlags;
			/**
			 * Conditional bindings whose scope was not elected (§24.6). Named
			 * under --verbose at debug level, carried in machine mode's
			 * diagnostics whatever the human stream did.
			 */
			readonly skippedBindings: readonly string[];
			/**
			 * The write set of a command declaring `updateOf` (contract §27.5),
			 * null on every command that declares none, and carried in both
			 * modes: it is a function of the declaration and the invocation.
			 */
			readonly writes: UpdateState | null;
			/** The properties this invocation CLEARED (§27.6). */
			readonly unsets: ReadonlySet<string>;
	  }
	| {
			readonly kind: "passthrough";
			readonly cmd: RegisteredCommand;
			readonly cmdPath: string;
			readonly args: readonly string[];
			readonly globalKwargs: Record<string, unknown>;
			readonly hermetic: boolean;
			readonly reserved: ReservedFlags;
	  };

export interface DoParseDeps {
	readonly config?: ConfigProvider;
}

/** Checks if --help or -h appears in tokens before any "--" separator. */
export function tokensContainHelp(tokens: readonly string[]): boolean {
	for (const tok of tokens) {
		if (tok === "--") {
			return false;
		}
		if (tok === "--help" || tok === "-h") {
			return true;
		}
	}
	return false;
}

function parseErrorOutcome(
	e: unknown,
	reserved: ReservedFlags,
	commandPrefix?: string,
): ParseOutcome {
	if (e instanceof ParseError) {
		return {
			kind: "parse-error",
			message: e.message,
			...(commandPrefix !== undefined ? { commandPrefix } : {}),
			reserved,
		};
	}
	throw e;
}

/**
 * Parses argv (without program name) into a ParseOutcome. Exactly one variant
 * applies: help, version, dump-schema, mcp, parse-error, command, or
 * passthrough.
 */
export function doParse(
	app: AppImpl,
	argv: readonly string[],
	deps?: DoParseDeps,
): ParseOutcome {
	// Fresh stdin tracking per parse invocation (@- is single-use).
	const tracker = newStdinTracker();

	// App-level --help/-h and --version/-v as the only token
	if (
		argv.length === 0 ||
		(argv.length === 1 && (argv[0] === "--help" || argv[0] === "-h"))
	) {
		return { kind: "help", target: { level: "app" } };
	}
	if (argv.length === 1 && (argv[0] === "--version" || argv[0] === "-v")) {
		return { kind: "version", text: `${app.name} ${app.version}` };
	}

	const pre = preScanReservedFlags(app, argv);
	if (pre.dumpSchema) {
		return { kind: "dump-schema" };
	}
	if (pre.serveMcp) {
		return { kind: "mcp" };
	}
	if (pre.err !== undefined) {
		return {
			kind: "parse-error",
			message: pre.err,
			reserved: reservedFlagsOf(pre),
		};
	}
	if (pre.hermetic && pre.configPath !== undefined) {
		return {
			kind: "parse-error",
			message: errHermeticConfigMutuallyExclusive(),
			reserved: reservedFlagsOf(pre),
		};
	}

	// Config loading (config.ts provider). Hermetic skips it entirely.
	const provider = deps?.config ?? emptyConfigProvider;
	let cfg: ConfigContext | null = null;
	let configLoadErr: string | undefined;
	if (app.configEnabled && !pre.hermetic) {
		const loaded = provider.load(pre.configPath);
		if (loaded.parseErr !== undefined && loaded.parseErr !== "") {
			configLoadErr = loaded.parseErr;
			cfg = {
				data: {},
				coerce: provider.coerce.bind(provider),
				conflictMode: app.configConflictMode,
			};
		} else {
			cfg = {
				data: loaded.data,
				coerce: provider.coerce.bind(provider),
				conflictMode: app.configConflictMode,
			};
		}
	}

	// Global flags from cleaned argv (--config/--hermetic stripped)
	let globals: ExtractedGlobals;
	try {
		globals = extractGlobalFlags(
			app,
			pre.cleanedArgv,
			pre.hermetic,
			tracker,
			cfg,
		);
	} catch (e) {
		return parseErrorOutcome(e, reservedFlagsOf(pre));
	}

	// If global flag parsing stopped at --, strip it before routing
	let rest = globals.remaining;
	if (rest.length > 0 && rest[0] === "--") {
		rest = rest.slice(1);
	}

	// After extracting globals, check for help/version again
	if (
		rest.length === 0 ||
		(rest.length === 1 && (rest[0] === "--help" || rest[0] === "-h"))
	) {
		return { kind: "help", target: { level: "app" } };
	}
	if (rest.length === 1 && (rest[0] === "--version" || rest[0] === "-v")) {
		return { kind: "version", text: `${app.name} ${app.version}` };
	}

	const route = resolveCommand(app, rest);
	if (route.err !== undefined) {
		return {
			kind: "parse-error",
			message: route.err,
			...(route.commandPrefix !== undefined
				? { commandPrefix: route.commandPrefix }
				: {}),
			reserved: reservedFlagsOf(pre),
		};
	}
	if (route.helpAtGroup) {
		return {
			kind: "help",
			target: {
				level: "group",
				group: route.lastGroup as GroupImpl,
				path: route.path,
			},
		};
	}

	const cmd = route.cmd as RegisteredCommand;
	const cmdRest = route.rest;
	const path = route.path;
	const cmdPath = [...path, cmd.name].join(".");

	// Command-level --help anywhere in remaining tokens (before any "--")
	if (tokensContainHelp(cmdRest)) {
		return { kind: "help", target: { level: "command", cmd, path } };
	}

	// A command that declares dryRunSupported: false refuses --dry-run here, on
	// every argv path (run/test/harness) at once, and AFTER the command-help
	// check above so `--help` always beats the refusal: asking what a command
	// does must never be answered with a refusal to preview it. `pre.dryRun`
	// covers both `app --dry-run cmd` and `app cmd --dry-run`; see
	// scanCommandRegionQuartet for the two boundaries that make a trailing
	// --dry-run invisible here (a bare `--`, and a passthrough command's name).
	const dryRunDef = cmd.def as {
		readonly dryRunSupported?: boolean;
		readonly dryRunUnsupportedReason?: string;
	};
	if (pre.dryRun && dryRunDef.dryRunSupported === false) {
		return {
			kind: "parse-error",
			message: errDryRunNotSupported(
				cmdPath,
				dryRunDef.dryRunUnsupportedReason ?? "",
			),
			reserved: reservedFlagsOf(pre),
		};
	}

	// Config subcommand exemption (self-lock prevention): the config
	// subcommands (edit/path/set/show) work on broken configs. The provider
	// stashed configLoadErr on the app at load time for `config show`.
	const isConfigSubcommand = path.length > 0 && path[0] === "config";
	if (pre.hermetic && isConfigSubcommand) {
		return {
			kind: "parse-error",
			message: errHermeticWithConfigCommands(),
			reserved: reservedFlagsOf(pre),
		};
	}
	if (configLoadErr !== undefined && !isConfigSubcommand) {
		return {
			kind: "parse-error",
			message: configLoadErr,
			reserved: reservedFlagsOf(pre),
		};
	}

	// Step 2.5 (Python): validate declared config fields, exempting config
	// subcommands. Hermetic mode skips config semantics entirely (cfg is null).
	if (cfg !== null && !isConfigSubcommand) {
		const fieldErr = provider.validateFields(cmd.configFields, cfg.data ?? {});
		if (fieldErr !== undefined) {
			return {
				kind: "parse-error",
				message: fieldErr,
				reserved: reservedFlagsOf(pre),
			};
		}
	}

	// Passthrough: skip all flag/arg parsing, forward raw args
	if (cmd.kind === "passthrough") {
		return {
			kind: "passthrough",
			cmd,
			cmdPath,
			args: cmdRest,
			globalKwargs: globals.values,
			hermetic: pre.hermetic,
			reserved: reservedFlagsOf(pre),
		};
	}

	let parsed: ParsedCommand;
	try {
		parsed = parseCommand(
			cmd,
			cmdRest,
			app.globalFlags,
			cfg,
			tracker,
			pre.hermetic,
			app.infraRoots,
		);
	} catch (e) {
		return parseErrorOutcome(
			e,
			reservedFlagsOf(pre),
			[app.name, ...path, cmd.name].join(" "),
		);
	}

	// Merge global values: post-command globals override pre-command ones
	const globalKwargs: Record<string, unknown> = {
		...globals.values,
		...parsed.postGlobalValues,
	};
	const kwargs: Record<string, unknown> = { ...parsed.kwargs, ...globalKwargs };

	// Merge global sources into command sources. For a global set
	// post-command, parseCommand already placed the correct (cli) source, so
	// the pre-command label (typically "default") must NOT overwrite it.
	const sources: Record<string, string> = { ...parsed.sources };
	for (const [k, v] of Object.entries(globals.sources)) {
		if (Object.hasOwn(parsed.postGlobalValues, k)) {
			continue; // post-command position wins
		}
		sources[k] = v;
	}

	return {
		kind: "command",
		cmd,
		cmdPath,
		kwargs,
		globalKwargs,
		sources,
		hermetic: pre.hermetic,
		reserved: reservedFlagsOf(pre),
		skippedBindings: parsed.skippedBindings,
		writes: parsed.writes,
		unsets: parsed.unsets,
	};
}

// --- Parse-error surface ---

/**
 * Renders the exact two-line stderr surface for a parse error:
 * "error: <msg>\ntry '<prefix> --help'\n". The prefix is the routed command
 * prefix when available, else the app name.
 */
export function formatParseErrorOutput(
	app: AppImpl,
	message: string,
	commandPrefix?: string,
): string {
	const prefix =
		commandPrefix !== undefined && commandPrefix !== ""
			? commandPrefix
			: app.name;
	return `error: ${message}\ntry '${prefix} --help'\n`;
}
