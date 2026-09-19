/**
 * The scoped-selector parser (contract §24.3): elections, scope-membership
 * validation, and per-scope value resolution.
 *
 * Parsing is PHASED, and the phases are what make order independence, the
 * distinct out-of-scope error, and that error's priority over a missing
 * required flag fall out instead of being special-cased:
 *
 *   1. Tokenize every occurrence, without interpreting any of it (parse.ts).
 *   2. Resolve elections, outermost first, then recursively inside each
 *      elected choice.
 *   3. Validate scope membership of every supplied flag.
 *   4. Resolve values and presence within the live scopes only.
 *
 * Error precedence is pinned by that order -- election -> scope -> value ->
 * presence -- so a command line with several problems reports the same error
 * every time and never one that depends on declaration order. `--via sms
 * --subject hi` says *`--subject` belongs to `email`*, never *`--phone-number`
 * is required*: the spelling mistake is reported before its consequence.
 */

import type { StdinTracker } from "./atprefix.js";
import { attachProvidedFields } from "./elected.js";
import { resolveEnvValue } from "./env.js";
import {
	errAmbientBindingSkippedConfig,
	errAmbientBindingSkippedEnv,
	errConfigValueError,
	errElectionOriginConfig,
	errElectionOriginDefault,
	errElectionOriginEnv,
	errElectionOriginSuffix,
	errFlagInvalidChoice,
	errFlagOutOfScope,
	errFlagRequired,
	errFlagValueError,
	errMutexDeclineClause,
	errMutexRedundantNegation,
	errMutuallyExclusive,
	errOneOfRequired,
	errScopeSuffix,
	errScopeWhyElected,
	errScopeWhyNoMemberElected,
	errScopeWhyNotProvided,
	errSelectorElectedTwice,
	ParseError,
} from "./errors.js";
import {
	type AnyChoiceFlag,
	type AnyDecl,
	type AnyFlag,
	buildScopeIndex,
	CHOICE_TAG_KEY,
	CHOICE_VALUE_KEY,
	choiceValues,
	flagOpts,
	flagParamName,
	memberList,
	requiredFlagForm,
	type ScopeIndexEntry,
	type ScopeStep,
	scopePath,
	surfaceNames,
} from "./factories.js";
import type { SourceLabel } from "./sources.js";
import {
	formatChoices,
	formatValueForError,
	validateChoices,
} from "./values.js";

/**
 * One positive occurrence: the raw token text, and the position the token scan
 * gave it. The position is what makes the VALUE phase report in command-line
 * order across root and scoped flags alike (§24.3, §18.28 item 262) -- a scope
 * is a position on the command line, never a group behind the root flags.
 */
export interface OccurrenceValue {
	/** The raw token text, uninterpreted. */
	readonly raw: string;
	/** This occurrence's position in the whole token scan, root and scoped alike. */
	readonly seq: number;
}

/** One surface name's raw occurrences, collected before anything is interpreted. */
export interface Occurrence {
	/** Every positive occurrence, in command-line order. */
	readonly positive: OccurrenceValue[];
	/** Whether `--no-<name>` was typed (a bool declines; it elects nothing). */
	negated: boolean;
}

/** Raw occurrences by dash name, produced by parse.ts's token loop. */
export type Occurrences = Map<string, Occurrence>;

/**
 * The phases, as comparable stage numbers (§24.3's precedence rule).
 *
 * `shape` is the token scan's own structural verdict -- which flag a token
 * names, and whether it consumes the next argv element. It precedes every
 * election because it is decided before any of them: a name the declaration
 * never mentions, or a value-taking token with nothing after it, is a fact
 * about the command line's shape and cannot wait for an election to be read
 * (§18.19 item 224).
 */
export const STAGE = {
	shape: 0,
	election: 1,
	scope: 2,
	value: 3,
	presence: 4,
} as const;

/**
 * One recorded problem: reported in stage order, then -- within the value
 * stage -- in command-line order, then in recording order.
 *
 * `seq` is the position of the token whose COERCION produced the problem, and
 * it is set for exactly those: a value is coerced as its token is consumed, so
 * a coercion failure is ordered by the command line (§24.3, §18.28 item 262).
 * Everything else in the value stage -- a `choices` refusal, a `validate`
 * refusal, an env or config coercion -- belongs to a later pass over the
 * declarations, carries no position, and is therefore reported after every
 * coercion failure, which is §18.20 item 226's pinned exception. The two
 * programmatic doors set no position at all, so their declaration-ordered
 * sweep (§18.25 item 249) is decided by recording order alone.
 */
export interface ParseProblem {
	readonly stage: number;
	readonly message: string;
	readonly seq?: number;
}

/** The config seam this module needs (parse.ts owns loading and precedence). */
export interface ScopeConfig {
	readonly data: Readonly<Record<string, unknown>> | null;
	coerce(f: AnyFlag, value: unknown): unknown;
}

/**
 * Everything phases 2-4 read. It is a snapshot taken by parse.ts once the
 * token scan is done: nothing here is looked up again while the phases run, so
 * the same command line and the same environment always produce the same
 * elections.
 */
export interface ScopeParseInput {
	/** The command's root declarations, in declaration order. */
	readonly decls: readonly AnyDecl[];
	/** Every surface name's raw occurrences, as the token scan collected them. */
	readonly occ: Occurrences;
	/** Hermetic mode: env vars and config are not consulted for anything. */
	readonly hermetic: boolean;
	/** The loaded config, or null when the app declares none. */
	readonly cfg: ScopeConfig | null;
	/** The per-parse stdin claim, so `@-` can be consumed exactly once. */
	readonly tracker: StdinTracker;
	/** The app's resolved infrastructure roots, keyed by env var name. */
	readonly infraRoots: ReadonlyMap<string, string>;
}

/** What phases 2-4 produced: the records, and everything parse.ts reports from. */
export interface ScopeParseResult {
	/** Elected record per selector, keyed by the selector's dash name. */
	readonly records: Map<string, unknown>;
	/** The selector's own source label, for ctx.source / ctx.provided. */
	readonly sources: Map<string, SourceLabel>;
	/** Every problem the phases recorded, unordered -- the stage decides. */
	readonly problems: ParseProblem[];
	/** Every surface name that belongs to a scope the invocation made live. */
	readonly liveNames: Set<string>;
	/**
	 * Conditional bindings that were NOT consulted because their scope was not
	 * elected -- diagnostics, never errors, surfaced under --verbose (§24.6).
	 */
	readonly skippedBindings: string[];
}

/** How a selector's election came about, as the pinned origin clause (§12.13). */
type Origin = "" | string;

/**
 * One selector's settled election: which choice, and how it came about. Both
 * front doors record one per selector, so a later refusal can name the
 * election that caused it without re-deriving anything (§12.13, §24.11).
 */
export interface Election {
	/** The choice name elected, or undefined when nothing was. */
	readonly elected: string | undefined;
	/** The pinned origin clause: empty for a command-line election. */
	readonly origin: Origin;
}

/** What an election lookup answers, for either front door's own record of them. */
export type ElectionLookup = (sel: AnyChoiceFlag) => Election | undefined;

/**
 * One run's accumulating state, threaded through the phases. Package-internal:
 * the input is read-only for its whole life and everything else is written
 * exactly once per declaration the phases visit.
 */
interface Run {
	/** The snapshot the phases read. */
	readonly input: ScopeParseInput;
	/** Problems recorded so far, in recording order within each stage. */
	readonly problems: ParseProblem[];
	/** Each selector's settled election, for the refusals that name one. */
	readonly elections: Map<AnyChoiceFlag, Election>;
	/** Every surface name a live scope claims (phase 3 reads it). */
	readonly liveNames: Set<string>;
	/** The §24.6 diagnostics: bindings inside scopes that were not elected. */
	readonly skippedBindings: string[];
	/** The elected record per root selector, keyed by its dash name. */
	readonly records: Map<string, unknown>;
	/** Each root selector's own source label. */
	readonly sources: Map<string, SourceLabel>;
}

/**
 * Runs phases 2-4 over a command's root declarations. Nothing throws: every
 * problem is recorded with its stage, and parse.ts reports the first one in
 * stage order (which is what makes precedence independent of declaration
 * order).
 */
export function parseScopes(input: ScopeParseInput): ScopeParseResult {
	const run: Run = {
		input,
		problems: [],
		elections: new Map(),
		liveNames: new Set(),
		skippedBindings: [],
		records: new Map(),
		sources: new Map(),
	};
	resolveScope(input.decls, [], null, run);
	validateScopeMembership(run);
	collectSkippedBindings(input.decls, [], run);
	return {
		records: run.records,
		sources: run.sources,
		problems: run.problems,
		liveNames: run.liveNames,
		skippedBindings: run.skippedBindings,
	};
}

// --- Phase 3: scope membership ---

/**
 * Anything supplied that no live scope claims. The token loop already refused
 * names the declaration never mentions, so what is left here is exactly the
 * out-of-scope case, which gets its own sentence -- deliberately NOT "unknown
 * flag": the flag is declared, it is simply not in the elected scope.
 */
function validateScopeMembership(run: Run): void {
	const index = buildScopeIndex(run.input.decls);
	for (const name of run.input.occ.keys()) {
		if (run.liveNames.has(name)) {
			continue;
		}
		const entries = index.get(name);
		if (entries === undefined || entries.length === 0) {
			continue;
		}
		const first = entries[0] as ScopeIndexEntry;
		run.problems.push({
			stage: STAGE.scope,
			// A member-spelled election's own name belongs to its PARENT scope,
			// and the index already records it there -- naming the member's own
			// election as its owner would say the flag is only valid under itself.
			message: outOfScopeMessage(
				name,
				entries.map((e) => e.path),
				first.path,
				(sel) => run.elections.get(sel),
			),
		});
	}
}

/**
 * The out-of-scope refusal, composed once for BOTH front doors (§24.11): the
 * machine boundary refuses a wrong combination with the CLI parser's own
 * sentence, which means this renderer and not a second one -- scope paths in
 * the CLI form §12.13 pins, choice names as declared.
 *
 * `owners` is every scope that declares the name, in declaration order;
 * `blamed` is the path whose unsatisfied election the `<why>` clause names.
 */
export function outOfScopeMessage(
	name: string,
	owners: readonly (readonly ScopeStep[])[],
	blamed: readonly ScopeStep[],
	electionOf: ElectionLookup,
): string {
	return errFlagOutOfScope(
		name,
		owners.map((p) => `'${scopePath(p)}'`).join(" or "),
		whyOutOfScope(blamed, electionOf),
	);
}

/**
 * The `<why>` clause: the FIRST (outermost) unsatisfied election on the first
 * owner's path, never the innermost one. A flag two levels down whose outer
 * election is the one that failed blames the OUTER election, because that is
 * the token the user would have to change (§24.3, §12.13).
 */
function whyOutOfScope(
	path: readonly ScopeStep[],
	electionOf: ElectionLookup,
): string {
	const satisfied: ScopeStep[] = [];
	for (const step of path) {
		const el = electionOf(step.selector);
		if (el !== undefined && el.elected === step.choiceName) {
			satisfied.push(step);
			continue;
		}
		const sel = step.selector;
		const electedName = el?.elected;
		if (electedName !== undefined) {
			const electedPath = scopePath([
				...satisfied,
				{ selector: sel, choiceName: electedName },
			]);
			return errScopeWhyElected(electedPath, el?.origin ?? "");
		}
		return sel.electBy === "member-flags"
			? errScopeWhyNoMemberElected(memberList(sel))
			: errScopeWhyNotProvided(sel.name);
	}
	// Every election on the path was satisfied, so the name is live after all.
	return errScopeWhyNotProvided(
		(path[0] as ScopeStep | undefined)?.selector.name ?? "",
	);
}

// --- Phases 2 and 4: elections, then values within the live scopes ---

/**
 * Resolves one scope: every declaration in it, in declaration order. Ordinary
 * flags resolve their own value and presence; a selector resolves its
 * election and then recurses into the elected choice.
 *
 * `out` is null for the root scope, whose ordinary flags are resolved by
 * parse.ts's existing root pipeline (env, config, implies, constraints).
 */
function resolveScope(
	decls: readonly AnyDecl[],
	path: readonly ScopeStep[],
	out: { values: Record<string, unknown>; provided: Set<string> } | null,
	run: Run,
): void {
	// The scope suffix and the origin suffix, in that order and never the other
	// way round (§12.13): where the requirement lives, then the ambient cause a
	// reader cannot see in their own command line. Every presence refusal this
	// scope produces carries the pair, and a decline clause goes after both.
	const suffix =
		errScopeSuffix(scopePath(path)) +
		errElectionOriginSuffix(electionOriginOf(path, run));
	for (const decl of decls) {
		for (const s of surfaceNames(decl)) {
			run.liveNames.add(s.name);
		}
		if (decl.kind === "choice-flag") {
			resolveSelector(decl, path, out, run, suffix);
			continue;
		}
		if (out !== null) {
			resolveScopedFlag(decl, out, run, suffix);
		}
	}
}

/** Resolves one selector's election and, when it elected, its chosen scope. */
function resolveSelector(
	sel: AnyChoiceFlag,
	path: readonly ScopeStep[],
	out: { values: Record<string, unknown>; provided: Set<string> } | null,
	run: Run,
	suffix: string,
): void {
	const election =
		sel.electBy === "member-flags"
			? electByMembers(sel, run, suffix)
			: electByToken(sel, run, suffix);
	run.elections.set(sel, election);
	const key = flagParamName(sel.name);
	if (election.elected === undefined) {
		return;
	}
	const chosen = sel.choices[election.elected];
	if (chosen === undefined) {
		return;
	}
	const record: Record<string, unknown> = {
		[CHOICE_TAG_KEY]: election.elected,
	};
	const provided = new Set<string>();
	if (chosen.value !== undefined) {
		const raw = run.input.occ.get(election.elected)?.positive[0]?.raw;
		if (raw !== undefined) {
			try {
				record[CHOICE_VALUE_KEY] = chosen.value.carrier.parse(raw);
				provided.add(CHOICE_VALUE_KEY);
			} catch (e) {
				run.problems.push({
					stage: STAGE.value,
					message: errFlagValueError(election.elected, (e as Error).message),
				});
			}
		}
	}
	resolveScope(
		Object.values(chosen.flags),
		[...path, { selector: sel, choiceName: election.elected }],
		{ values: record, provided },
		run,
	);
	attachProvidedFields(record, provided);
	if (out === null) {
		run.records.set(sel.name, record);
		run.sources.set(sel.name, sourceOfOrigin(election.origin));
	} else {
		out.values[key] = record;
		if (election.origin === "") {
			out.provided.add(key);
		}
	}
}

/** §23.6's source vocabulary, derived from the pinned origin clause. */
function sourceOfOrigin(origin: Origin): SourceLabel {
	if (origin === "") {
		return "cli";
	}
	if (origin === errElectionOriginDefault) {
		return "default";
	}
	return origin.startsWith(" from env var ") ? "env" : "config";
}

/**
 * `--via email`: the selector names the choice. A token-spelled selector is
 * an ordinary value flag whose value happens to name a choice, so CLI > env >
 * config > default applies unchanged (§24.6).
 */
function electByToken(sel: AnyChoiceFlag, run: Run, suffix: string): Election {
	const occ = run.input.occ.get(sel.name);
	const typed = occ?.positive ?? [];
	if (typed.length > 1) {
		// Last-wins is right for a plain flag and wrong for an election, because
		// discarding a value would discard a whole scope with it.
		run.problems.push({
			stage: STAGE.election,
			message: errSelectorElectedTwice(
				sel.name,
				typed.map((v) => `'${v.raw}'`).join(" and "),
			),
		});
		return { elected: undefined, origin: "" };
	}
	const named = (raw: string, origin: Origin): Election => {
		if (!Object.hasOwn(sel.choices, raw)) {
			run.problems.push({
				stage: STAGE.election,
				message: errFlagInvalidChoice(
					sel.name,
					formatValueForError(raw),
					formatChoices(Object.keys(sel.choices)),
				),
			});
			return { elected: undefined, origin };
		}
		return { elected: raw, origin };
	};
	const first = typed[0];
	if (first !== undefined) {
		return named(first.raw, "");
	}
	const ambient = ambientElection(sel, run);
	if (ambient !== undefined) {
		return named(ambient.raw, ambient.origin);
	}
	const o = sel.opts;
	if (o.presence === "default" && o.default !== undefined) {
		return { elected: o.default, origin: errElectionOriginDefault };
	}
	// A required selector that elected nothing is recorded and DEFERRED here
	// rather than refused: scope validation may report a token the reader
	// actually typed, which is the more useful of the two true statements
	// (§24.3).
	run.problems.push({
		stage: STAGE.presence,
		message: `flag '--${sel.name}' is required${suffix}`,
	});
	return { elected: undefined, origin: "" };
}

/** The env-then-config lookup a token-spelled selector takes (§24.6). */
function ambientElection(
	sel: AnyChoiceFlag,
	run: Run,
): { raw: string; origin: string } | undefined {
	if (run.input.hermetic) {
		return undefined;
	}
	const envVar = sel.opts.env;
	if (envVar !== undefined) {
		const value = process.env[envVar];
		if (value !== undefined) {
			return { raw: value, origin: errElectionOriginEnv(envVar) };
		}
	}
	const data = run.input.cfg?.data;
	if (data != null) {
		const key = flagParamName(sel.name);
		if (Object.hasOwn(data, key) && typeof data[key] === "string") {
			return {
				raw: data[key] as string,
				origin: errElectionOriginConfig(key),
			};
		}
	}
	return undefined;
}

/**
 * `--profile work` / `--all-profiles`: each choice is spelled as its own flag.
 * This is §21 restated as a scope tree -- a bool member elects only on true,
 * `--no-<name>` DECLINES rather than choosing, a redundant negation beside a
 * real election is a parse error, and election is COMMAND-LINE ONLY (§24.4,
 * §24.6). Its three error sentences are §21.4's, carried over verbatim.
 */
function electByMembers(
	sel: AnyChoiceFlag,
	run: Run,
	suffix: string,
): Election {
	const elected: string[] = [];
	const declined: string[] = [];
	for (const choiceName of Object.keys(sel.choices)) {
		const occ = run.input.occ.get(choiceName);
		if (occ === undefined) {
			continue;
		}
		if (occ.positive.length > 0) {
			elected.push(choiceName);
		}
		if (occ.negated) {
			declined.push(choiceName);
		}
	}
	const firstDeclined = declined[0];
	const clause =
		firstDeclined !== undefined ? errMutexDeclineClause(firstDeclined) : "";
	if (elected.length > 1) {
		run.problems.push({
			stage: STAGE.election,
			message: errMutuallyExclusive(elected.map((c) => `--${c}`).join(" and ")),
		});
		return { elected: undefined, origin: "" };
	}
	const sole = elected[0];
	if (sole !== undefined) {
		if (declined.length > 0) {
			run.problems.push({
				stage: STAGE.election,
				message: errMutexRedundantNegation(
					declined.map((c) => `--no-${c}`).join(" and "),
					sole,
					clause,
				),
			});
			return { elected: undefined, origin: "" };
		}
		return { elected: sole, origin: "" };
	}
	const o = sel.opts;
	if (o.presence === "default" && o.default !== undefined) {
		return { elected: o.default, origin: errElectionOriginDefault };
	}
	run.problems.push({
		stage: STAGE.presence,
		// Scope suffix, origin suffix, THEN the decline clause, in that order and
		// no other (§12.13): the scope names where the requirement lives and
		// closes the sentence, and the two parentheticals follow it -- the decline
		// clause last, because it is a note about a token that WAS typed.
		message: errOneOfRequired(memberList(sel), `${suffix}${clause}`),
	});
	return { elected: undefined, origin: "" };
}

/**
 * One ordinary flag inside a live scope. §23 applies again unchanged, one
 * level down: an optional sub-flag delivers absence as a present FIELD of the
 * record, never a missing one, and a required one is required.
 */
function resolveScopedFlag(
	f: AnyFlag,
	out: { values: Record<string, unknown>; provided: Set<string> },
	run: Run,
	suffix: string,
): void {
	const key = flagParamName(f.name);
	const o = flagOpts(f);
	const occ = run.input.occ.get(f.name);
	const store = new Map<string, unknown>();
	let source: SourceLabel | undefined;

	if (occ !== undefined && (occ.positive.length > 0 || occ.negated)) {
		if (f.schema === "bool") {
			store.set(f.name, occ.positive.length > 0);
		} else {
			for (const { raw, seq } of occ.positive) {
				try {
					parseScopedRawValue(f, raw, store, run.input.tracker);
				} catch (e) {
					// The token's own position: a scoped value is coerced where its
					// token sits on the command line, exactly as a root one is
					// (§24.3, §18.28 item 262).
					run.problems.push({
						stage: STAGE.value,
						message: (e as Error).message,
						seq,
					});
					return;
				}
			}
		}
		source = "cli";
	} else if (!run.input.hermetic && o.env !== undefined) {
		const envVal = process.env[o.env];
		if (envVal !== undefined) {
			try {
				store.set(f.name, resolveEnvValue(f, o.env, envVal, run.input.tracker));
				source = "env";
			} catch (e) {
				run.problems.push({
					stage: STAGE.value,
					message: (e as Error).message,
				});
				return;
			}
		}
	}
	if (
		source === undefined &&
		!run.input.hermetic &&
		run.input.cfg?.data != null
	) {
		const data = run.input.cfg.data;
		if (Object.hasOwn(data, key)) {
			try {
				store.set(f.name, run.input.cfg.coerce(f, data[key]));
				source = "config";
			} catch (e) {
				run.problems.push({
					stage: STAGE.value,
					message: errConfigValueError(f.name, (e as Error).message),
				});
				return;
			}
		}
	}

	if (source === undefined) {
		// The declaration decides. A required sub-flag carries the scope it is
		// required under and the origin of a non-command-line election (both in
		// `suffix`), so a refusal never blames a command line without the cause.
		if (o.presence === "required") {
			run.problems.push({
				stage: STAGE.presence,
				// The ROOT sentence plus the suffix, never a sentence of its own: a
				// required bool inside a scope names the tokens that satisfy it
				// exactly as it does at root, and the suffix says where the
				// requirement lives (§12.13).
				message: `${errFlagRequired(f.name, requiredFlagForm(f))}${suffix}`,
			});
			return;
		}
		// §23 applies one level down UNCHANGED, and that includes what a declared
		// default IS: a compound is copied so a handler cannot mutate the
		// declaration, and a RelativeToRoot marker is RESOLVED through the app's
		// declared infrastructure roots. Delivering the marker itself would hand
		// a handler an opaque object no command line can produce, where the same
		// declaration at root delivers the resolved path.
		out.values[key] = applyScopedDefault(f, run.input.infraRoots, suffix).value;
		return;
	}

	const value = store.get(f.name);
	try {
		validateChoices(
			f.name,
			value,
			f.schema.startsWith("list["),
			o.choices === undefined ? undefined : choiceValues(o.choices),
			o.retiredChoices,
			false,
		);
	} catch (e) {
		run.problems.push({ stage: STAGE.value, message: (e as Error).message });
		return;
	}
	if (!recordValidateRefusal(f, value, run.problems)) {
		return;
	}
	out.values[key] = value;
	out.provided.add(key);
}

/**
 * One flag's custom callback over a value the INVOCATION supplied, recorded as
 * a value-stage problem rather than thrown. Returns whether the value survived.
 *
 * The callback is a property of a SUPPLIED value (§23.5): the caller is the one
 * that decides whether it runs, never the door the value arrived through, so
 * every door that can tell a supplied value from a declared one asks this --
 * the command line's scoped values here, and the flat machine form's in
 * tool.ts. A list runs the callback per element and stops at the first refusal,
 * because the refusal names the value and a second one would name a value the
 * caller has not been told about yet.
 */
export function recordValidateRefusal(
	f: AnyFlag,
	value: unknown,
	problems: ParseProblem[],
): boolean {
	const validate = flagOpts(f).validate;
	if (validate === undefined) {
		return true;
	}
	const check = (v: unknown): boolean => {
		try {
			validate(v as never);
			return true;
		} catch (e) {
			problems.push({
				stage: STAGE.value,
				message: errFlagValueError(f.name, (e as Error).message),
			});
			return false;
		}
	};
	if (Array.isArray(value)) {
		for (const v of value) {
			if (!check(v)) {
				return false;
			}
		}
		return true;
	}
	if (value === undefined || value === null) {
		return true;
	}
	return check(value);
}

/**
 * The origin clause of the OUTERMOST non-CLI election on a path (§18.19 item
 * 216) -- the ambient cause a reader cannot see in their own command line.
 * Empty when every election on the path was typed. Naming the innermost
 * election instead would blame a token the reader did not have to change, and
 * would fall silent entirely whenever the inner election was the typed one.
 */
function electionOriginOf(path: readonly ScopeStep[], run: Run): Origin {
	for (const step of path) {
		const origin = run.elections.get(step.selector)?.origin ?? "";
		if (origin !== "") {
			return origin;
		}
	}
	return "";
}

/**
 * Value coercion for a scoped flag. Deferred until phase 4 on purpose: a
 * coercion failure is a VALUE-stage problem, and a flag whose scope was never
 * elected is never coerced at all.
 */
function parseScopedRawValue(
	f: AnyFlag,
	raw: string,
	store: Map<string, unknown>,
	tracker: StdinTracker,
): void {
	// Imported lazily through parse.ts's shared helper to keep one coercion
	// path for scoped and unscoped flags alike.
	scopedValueParser(f, raw, store, tracker);
}

/**
 * Installed by parse.ts at module load: the ONE raw-value coercion path, so a
 * scoped flag and a root flag coerce identically (dict merge, list append
 * with unique enforcement, scalar coercion with @-prefix resolution).
 */
let scopedValueParser: (
	f: AnyFlag,
	raw: string,
	store: Map<string, unknown>,
	tracker: StdinTracker,
) => void = () => {
	throw new Error("internal: scoped value parser not installed");
};

/** Package-internal wiring (parse.ts owns the coercion implementation). */
export function installScopedValueParser(
	fn: (
		f: AnyFlag,
		raw: string,
		store: Map<string, unknown>,
		tracker: StdinTracker,
	) => void,
): void {
	scopedValueParser = fn;
}

/**
 * Installed by parse.ts at module load: the ONE declared-default path, so a
 * scoped flag's default is applied exactly as a root flag's is -- compounds
 * copied, and a RelativeToRoot marker resolved through the declared roots the
 * snapshot carries. Reached only for a presence the declaration ANSWERS
 * (optional or default), so the one refusal it can produce is a marker naming
 * a root the app never declared -- which applyScopedDefault below is where
 * every door hears about.
 */
let scopedDefaultApplier: (
	f: AnyFlag,
	infraRoots: ReadonlyMap<string, string>,
) => { value: unknown } = () => {
	throw new Error("internal: scoped default applier not installed");
};

/** Package-internal wiring (parse.ts owns the default implementation). */
export function installScopedDefaultApplier(
	fn: (
		f: AnyFlag,
		infraRoots: ReadonlyMap<string, string>,
	) => { value: unknown },
): void {
	scopedDefaultApplier = fn;
}

/**
 * One scoped declaration's default, applied through the seam above with
 * §12.13's suffix on whatever it refuses.
 *
 * Registration never looks inside a scope, so a `RelativeToRoot` naming an
 * undeclared infra root is refused HERE, at delivery -- and a refusal from
 * inside a scope says where the declaration sits, exactly as every other
 * scoped refusal does. Authoring the suffix at this ONE seam is what makes
 * every door carry it: a door that reached the default itself would print the
 * marker's bare sentence and name no scope at all.
 *
 * `suffix` is the scope suffix and the origin suffix already composed, in that
 * order and never the other way round.
 */
export function applyScopedDefault(
	f: AnyFlag,
	infraRoots: ReadonlyMap<string, string>,
	suffix: string,
): { value: unknown } {
	try {
		return scopedDefaultApplier(f, infraRoots);
	} catch (e) {
		if (e instanceof ParseError) {
			throw new ParseError(`${e.message}${suffix}`);
		}
		throw e;
	}
}

// --- The conditional-binding diagnostics (§24.6) ---

/**
 * Every env var or config key bound to a flag whose scope was NOT elected,
 * named one line per binding, in declaration order.
 *
 * The binding's condition is written in the declaration (the flag sits in a
 * scope), the framework evaluates the same condition the same way every run,
 * and the same command line plus the same environment always produces the
 * same values -- which is what keeps this inside the no-silent-degradation
 * rule rather than beside it. What is refused is the SILENT part, and it is
 * refused by surfacing.
 */
function collectSkippedBindings(
	decls: readonly AnyDecl[],
	path: readonly ScopeStep[],
	run: Run,
): void {
	for (const decl of decls) {
		if (decl.kind === "choice-flag") {
			const elected = run.elections.get(decl)?.elected;
			for (const [choiceName, c] of Object.entries(decl.choices)) {
				const next = [...path, { selector: decl, choiceName }];
				if (choiceName === elected) {
					collectSkippedBindings(Object.values(c.flags), next, run);
				} else {
					recordSkipped(Object.values(c.flags), next, run);
				}
			}
		}
	}
}

/** Names every binding inside a scope that was not elected, at any depth. */
function recordSkipped(
	decls: readonly AnyDecl[],
	path: readonly ScopeStep[],
	run: Run,
): void {
	const rendered = scopePath(path);
	for (const decl of decls) {
		if (decl.kind === "choice-flag") {
			for (const [choiceName, c] of Object.entries(decl.choices)) {
				recordSkipped(
					Object.values(c.flags),
					[...path, { selector: decl, choiceName }],
					run,
				);
			}
			continue;
		}
		const o = flagOpts(decl);
		if (!run.input.hermetic && o.env !== undefined) {
			const value = process.env[o.env];
			if (value !== undefined) {
				run.skippedBindings.push(
					errAmbientBindingSkippedEnv(o.env, decl.name, rendered),
				);
			}
		}
		const data = run.input.cfg?.data;
		if (!run.input.hermetic && data != null) {
			const key = flagParamName(decl.name);
			if (Object.hasOwn(data, key)) {
				run.skippedBindings.push(
					errAmbientBindingSkippedConfig(key, decl.name, rendered),
				);
			}
		}
	}
}

/**
 * The problem to report: the lowest stage; among equals the earliest COMMAND-
 * LINE position; among those the one recorded first. Undefined when the run had
 * none.
 *
 * Every front door selects its refusal through this one function, which is what
 * makes §24.3's precedence a property of the phases rather than of each caller's
 * walk order. A problem with no position sorts after every positioned one, which
 * is how the value stage reports every coercion failure ahead of any `validate`
 * refusal (§18.20 item 226) and how the programmatic doors keep their
 * declaration-ordered sweep (§18.25 item 249): they position nothing.
 */
export function firstProblem(
	problems: readonly ParseProblem[],
): ParseProblem | undefined {
	let best: ParseProblem | undefined;
	for (const p of problems) {
		if (best === undefined || p.stage < best.stage) {
			best = p;
			continue;
		}
		if (p.stage === best.stage && positionOf(p) < positionOf(best)) {
			best = p;
		}
	}
	return best;
}

/** A problem's command-line position; unpositioned problems sort last. */
function positionOf(p: ParseProblem): number {
	return p.seq ?? Number.POSITIVE_INFINITY;
}

/** Throws the first problem in stage order, or returns when there are none. */
export function throwFirstProblem(problems: readonly ParseProblem[]): void {
	const best = firstProblem(problems);
	if (best === undefined) {
		return;
	}
	throw new ParseError(best.message);
}
