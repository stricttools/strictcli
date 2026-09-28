/**
 * Programmatic invocation: app.call(commandPath, kwargs) runs a command
 * in-process with pre-typed values, bypassing CLI parsing and env resolution.
 * It also skips config loading and stdin handling. Mirrors Go invoke.go with
 * Python _invoke/call as the divergence ground truth where the two differ
 * (group-path message, undefined-return value).
 *
 * commandPath uses dot-separated segments ("deploy", "dns.zone.create").
 * kwargs keys use underscored parameter names ("dry_run", not "--dry-run").
 * Passthrough commands take the raw argument list under the special "_args"
 * key. Failures throw InvokeError (errors.ts) instead of printing to stderr.
 */

import type { AppImpl, RegisteredCommand } from "./app.js";
import { recordCoverage } from "./checks/coverage.js";
import { validateCheckRegistrations } from "./checks/framework.js";
import { coerceConfigValueForFlag, widenJsonIntegers } from "./config.js";
import {
	attachUpdateState,
	Context,
	contextPayload,
	endDispatch,
	NO_RESERVED_FLAGS,
	type ReservedFlags,
	type Writer,
} from "./context.js";
import type { Effect, Effects } from "./effects.js";
import { attachProvidedFields } from "./elected.js";
import {
	errCallConsequentialUnconsented,
	errCallPathIsGroup,
	errDictFlagExpectedMapType,
	errElectionOriginDefault,
	errElectionOriginSuffix,
	errFlagInvalidChoice,
	errFlagRequired,
	errFlagRequiresValue,
	errOneOfRequired,
	errPassthroughArgsNotStringSlice,
	errScopeSuffix,
	errUnknownParameterForCommand,
	errUnknownParameterForPassthroughCommand,
	InvokeError,
	ParseError,
} from "./errors.js";
import { ExitError, ExitNow } from "./exits.js";
import {
	type AnyChoiceFlag,
	type AnyCommand,
	type AnyFlag,
	CHOICE_TAG_KEY,
	CHOICE_VALUE_KEY,
	flagOpts,
	memberList,
	type PassthroughDef,
	requiredFlagForm,
	type ScopeStep,
	schemaKind,
	scopePath,
} from "./factories.js";
import { buildInfraAccess } from "./infra.js";
import { interpretHandlerReturn } from "./outcome.js";
import {
	applyFlagDefault,
	flagParamName,
	validateAndBuildKwargs,
} from "./parse.js";
import { loadRequirements } from "./requirements.js";
import { resolveCommand } from "./routing.js";
import {
	applyScopedDefault,
	firstProblem,
	type ParseProblem,
	STAGE,
} from "./scopeparse.js";
import { SourcedStore } from "./sources.js";
import type { UpdateState } from "./update.js";
import { formatChoices } from "./values.js";

/** Sinks for invoke contexts: structured data flows back through the Outcome. */
const discard: Writer = { write: () => {} };

/**
 * Per-call state that is NOT a handler kwarg. TS takes it as a trailing
 * options object -- the idiomatic counterpart of Python's keyword-only
 * argument and Go's variadic CallOption.
 */
export interface CallOptions {
	/**
	 * The caller's explicit consent, the programmatic counterpart of the CLI's
	 * --approve-consequential. A command that declares itself consequential is
	 * refused without it; read-only and plain mutating commands are
	 * unaffected.
	 */
	readonly approveConsequential?: boolean;
}

/**
 * Reads a registered command's effects-regime classification (schema.ts
 * serializeCommand reads the same two carrier fields). Shared by the invoke
 * consent check and the tool/MCP descriptors.
 */
export function commandClassification(cmd: RegisteredCommand): {
	readonly effect: Effect;
	readonly consequential: boolean;
} {
	const carrier = cmd.def as {
		readonly effect: Effect;
		readonly consequential?: boolean;
	};
	return {
		effect: carrier.effect,
		consequential: carrier.consequential === true,
	};
}

function isConsequential(cmd: RegisteredCommand): boolean {
	return commandClassification(cmd).consequential;
}

/** Converts a parameter name like "dry_run" back to a flag name "dry-run". */
export function paramToFlagName(param: string): string {
	return param.replaceAll("_", "-");
}

/** Runtime type name for the dict-coercion error (TS vocabulary; Go %T slot). */
function invokeTypeName(v: unknown): string {
	if (v === null) {
		return "null";
	}
	if (Array.isArray(v)) {
		return "Array";
	}
	if (typeof v === "object") {
		const ctor = (v as { constructor?: { name?: string } }).constructor?.name;
		return ctor !== undefined && ctor !== "" ? ctor : "object";
	}
	return typeof v;
}

/**
 * Converts a caller-provided value to the internal representation expected by
 * the validation pipeline (Go coerceInvokeValue), and CHECKS it against the
 * declaration it was supplied against (contract §24.11, §23.4).
 *
 * "Pre-typed" has always meant ALREADY OF THE DECLARED TYPE, never exempt from
 * the declaration: a front door that does no parsing still has a declaration
 * saying what the value may be, and the closed-set half of it (`choices`) is
 * consulted on this path already. So the type half is consulted too, by the
 * check the config reader runs over an already-typed document -- a flat object
 * and a config document pose the identical question.
 *
 * `null` and `undefined` are legal for nothing. Optionality has ONE spelling
 * (§23.4): a flag that may be absent declares `optional` and is delivered
 * absent when its key is simply not there, so a null says nothing the
 * declaration cannot already say -- and on a required flag it would answer the
 * presence rule with a value the declaration forbids.
 *
 * Dict flags keep their own shape refusal, which is the one both this door and
 * Go's already print, and their entries are then checked like any others.
 */
function coerceInvokeValue(f: AnyFlag, value: unknown): unknown {
	let checked = value;
	if (schemaKind(f.schema) === "dict") {
		checked = Object.fromEntries(coerceInvokeDict(f, value));
	}
	try {
		return coerceConfigValueForFlag(widenJsonIntegers(checked), f);
	} catch (e) {
		throw new InvokeError(`--${f.name}: ${(e as Error).message}`);
	}
}

/**
 * The refusal one pre-typed value earns against its declaration, or undefined
 * when the declaration accepts it. It is the VALUE phase's own question
 * (§24.11 item 240), so the flat door asks it where that phase runs -- after
 * every election and scope decision, and before a presence problem is
 * reported -- rather than leaving it to the conversion's downstream call().
 */
export function preTypedValueRefusal(
	f: AnyFlag,
	value: unknown,
): string | undefined {
	const outcome = preTypedValueOutcome(f, value);
	return outcome.ok ? undefined : outcome.message;
}

/**
 * The same question with the ACCEPTED value kept: what the declaration turns a
 * pre-typed value into, or the refusal it earns.
 *
 * A caller that has more of the declaration left to consult -- the flat door's
 * custom callback, which runs on the value the handler will receive and not on
 * the object the caller wrote -- needs the coerced value, and coercing twice to
 * get it would run a declaration's own conversions twice.
 */
export function preTypedValueOutcome(
	f: AnyFlag,
	value: unknown,
):
	| { readonly ok: true; readonly value: unknown }
	| { readonly ok: false; readonly message: string } {
	try {
		return { ok: true, value: coerceInvokeValue(f, value) };
	} catch (e) {
		return { ok: false, message: (e as Error).message };
	}
}

function coerceInvokeDict(f: AnyFlag, value: unknown): Map<string, unknown> {
	if (value instanceof Map) {
		return value as Map<string, unknown>;
	}
	if (typeof value === "object" && value !== null && !Array.isArray(value)) {
		return new Map(Object.entries(value));
	}
	throw new InvokeError(
		errDictFlagExpectedMapType(f.name, invokeTypeName(value)),
	);
}

/**
 * Marks a record the flat door built for an election the DECLARATION made
 * (§24.5's defaulted selection), so `call()` labels the selector's own value
 * `default` rather than `cli`.
 *
 * The flat door converts its object into the records `call()` takes, and a
 * defaulted selection has to be materialized there whenever the caller supplied
 * a field of the elected scope beside it -- the value must reach the handler
 * exactly as the command line delivers it under the same election. Without this
 * mark the materialization would itself answer §23.6's question ("did the
 * invocation cause this value") with `cli` for a record the caller contributed
 * no election to, which is the answer neither the command line nor the record
 * door gives.
 *
 * It is a symbol, so it is invisible to `Object.keys` (the unknown-key sweep),
 * to JSON, and to every handler: it says something about how the record was
 * produced, never something the record contains.
 */
const DECLARATION_ELECTED = Symbol("strictcli.declarationElected");

/** Marks one record as the declaration's election (flat door only). */
export function markDeclarationElected(record: Record<string, unknown>): void {
	Object.defineProperty(record, DECLARATION_ELECTED, {
		value: true,
		enumerable: false,
	});
}

/** Whether a supplied selector value carries the mark above. */
function isDeclarationElected(value: unknown): boolean {
	return (
		typeof value === "object" &&
		value !== null &&
		(value as Record<symbol, unknown>)[DECLARATION_ELECTED] === true
	);
}

/**
 * The keys of one record the FLAT door read out of the caller's own object,
 * carried on the record it converts so `provided()` can answer §23.6's question
 * about them (§18.29 item 268).
 *
 * The record door labels every field it delivers the declaration's, because a
 * constructed scope fills its declared defaults before anything can look
 * (§18.26 item 253). The flat door is not in that position: a key it read is a
 * value the caller wrote, which is decidable and decided right there -- so the
 * conversion carries the answer forward instead of losing it on the way through
 * a record. A record without the mark is a record door's, and answers as that
 * door answers.
 *
 * A symbol for the same reason the mark above is one: it says something about
 * how the record was produced, never something the record contains.
 */
const SUPPLIED_KEYS = Symbol("strictcli.suppliedKeys");

/** Marks the keys one record was given by the caller (flat door only). */
export function markSuppliedKeys(
	record: Record<string, unknown>,
	keys: ReadonlySet<string>,
): void {
	Object.defineProperty(record, SUPPLIED_KEYS, {
		value: new Set(keys),
		enumerable: false,
	});
}

/** The keys the flat door read into this record; empty for every other door. */
function suppliedKeysOf(value: unknown): ReadonlySet<string> {
	if (typeof value !== "object" || value === null) {
		return new Set();
	}
	const marked = (value as Record<symbol, unknown>)[SUPPLIED_KEYS];
	return marked instanceof Set ? (marked as Set<string>) : new Set();
}

/** Wraps applyFlagDefault, converting its ParseError into an InvokeError. */
function applyFlagDefaultForInvoke(
	f: AnyFlag,
	prefix: string,
	infraRoots: ReadonlyMap<string, string>,
): { value: unknown; source: string } {
	try {
		return applyFlagDefault(f, prefix, infraRoots);
	} catch (e) {
		if (e instanceof ParseError) {
			throw new InvokeError(e.message);
		}
		throw e;
	}
}

/**
 * One SCOPED declaration's default at this door, through the seam the argv
 * path uses. The seam authors §12.13's suffix, so a marker naming an
 * undeclared infra root names the scope it was declared in here exactly as it
 * does on the command line -- this door reaches the declaration, never the
 * refusal's wording.
 */
function applyScopedDefaultForInvoke(
	f: AnyFlag,
	infraRoots: ReadonlyMap<string, string>,
	suffix: string,
): unknown {
	try {
		return applyScopedDefault(f, infraRoots, suffix).value;
	} catch (e) {
		if (e instanceof ParseError) {
			throw new InvokeError(e.message);
		}
		throw e;
	}
}

/**
 * Interprets the handler's (awaited) return for call():
 * - a payload supplied through ctx.payload -> the payload (contract §19.4)
 * - bare undefined -> undefined (Python's None; Go handlers cannot express it)
 * - otherwise -> the exit code
 */
function interpretForCall(
	result: unknown,
	ctx: Context,
	killedChildren: boolean,
): unknown {
	const interpreted = interpretHandlerReturn(result);
	const supplied = contextPayload(ctx);
	if (supplied.set) {
		return supplied.value;
	}
	if (result === undefined) {
		return undefined;
	}
	// A child still running when the handler ended fails the run on this door
	// too: the exit status it reports is 1 unless already nonzero (§19.11's
	// box).
	if (killedChildren && interpreted.exitCode === 0) {
		return 1;
	}
	return interpreted.exitCode;
}

/**
 * Runs a handler for call() and interprets how it ended. A command that ended
 * through an early exit rejects with ExitError carrying its code and message
 * (contract §19.9); every other throw propagates untouched. `ctx.signal` is
 * aborted when the dispatch ends, however it ends (§19.13).
 */
async function callHandler(
	invoke: () => unknown,
	ctx: Context,
	effects: Effects,
): Promise<unknown> {
	let result: unknown;
	try {
		result = await invoke();
	} catch (e) {
		// The children are settled on every way out, with no stream to name a
		// killed one on (§19.11's box).
		effects.settleChildren();
		endDispatch(ctx);
		if (e instanceof ExitNow) {
			const supplied = contextPayload(ctx);
			throw new ExitError(
				e.code,
				e.message,
				supplied.set ? supplied.value : null,
			);
		}
		throw e;
	}
	try {
		const killed = effects.settleChildren();
		return interpretForCall(result, ctx, killed.length > 0);
	} finally {
		endDispatch(ctx);
	}
}

/**
 * The implementation behind App.call(). Resolves the command, populates a
 * SourcedStore from kwargs (marked "cli" so mutex/dependency checks see
 * them), runs the shared validation pipeline, and awaits the handler.
 */
export async function invokeApp(
	app: AppImpl,
	commandPath: string,
	kwargs: Readonly<Record<string, unknown>>,
	opts: CallOptions = {},
): Promise<unknown> {
	const approveConsequential = opts.approveConsequential === true;
	// The caller's declaration is delivered to the handler on the Context, so a
	// handler (or an audit record) can see how the run was consented to.
	const reserved: ReservedFlags = {
		...NO_RESERVED_FLAGS,
		approveConsequential,
	};
	// Validate registrations (Go invoke: check registrations + tag contracts).
	const checkErr = validateCheckRegistrations(app.checks);
	if (checkErr !== undefined) {
		throw new InvokeError(checkErr);
	}
	const tagErr = app.validateTagContracts();
	if (tagErr !== undefined) {
		throw new InvokeError(tagErr);
	}

	const route = resolveCommand(app, commandPath.split("."));
	if (route.err !== undefined) {
		throw new InvokeError(route.err);
	}
	if (route.cmd === undefined) {
		throw new InvokeError(errCallPathIsGroup(commandPath));
	}
	const cmd = route.cmd;

	// The consent check (contract §8.5). There is no terminal here, so the
	// confirm protocol's PROMPT cannot fire -- the caller must have said so in
	// the call. Checked before anything is dispatched or recorded.
	if (isConsequential(cmd) && !approveConsequential) {
		throw new InvokeError(errCallConsequentialUnconsented(commandPath));
	}

	// Start a new dispatch BEFORE coverage recording so pre-handler
	// CACHE_WRITEs land in this dispatch's effect log.
	app.beginDispatch();
	// Record test-coverage hit (command-level only).
	if (app.coverageShardPath !== undefined) {
		recordCoverage(app, commandPath);
	}

	if (cmd.kind === "passthrough") {
		return invokePassthrough(app, cmd, commandPath, kwargs, reserved);
	}
	const def = cmd.def as AnyCommand;

	// Reverse mapping: flag name (with dashes) -> flag definition.
	const flagByName = new Map<string, AnyFlag>();
	for (const f of cmd.flags) {
		flagByName.set(f.name, f);
	}
	const argNames = new Set(def.args.map((a) => a.name));

	// Selectors declared at command level, by dash name. `call()` takes the
	// ELECTED RECORD, pre-typed (contract §24.11): the programmatic front
	// door's contract is unchanged -- pre-typed values, no parsing -- so the
	// value for a selector is the same record a handler receives.
	const selectorByName = new Map<string, AnyChoiceFlag>();
	for (const d of def.allDecls) {
		if (d.kind === "choice-flag") {
			selectorByName.set(d.name, d);
		}
	}

	// Populate the sourced store from kwargs, mapping param names back to
	// flag names. Provided kwargs are marked "cli"; absent flags get their
	// defaults inside validateAndBuildKwargs.
	const store = new SourcedStore();
	// The key namespace here is the UNDERSCORED delivery-name space -- the
	// parameter name a handler receives, which is exactly what the flat schema
	// publishes (§24.11). A flag's dashed spelling is its command-line TOKEN,
	// and this door has no tokens, so a dashed key names nothing.
	const declaredByParam = new Map<string, string>();
	for (const name of [
		...flagByName.keys(),
		...selectorByName.keys(),
		...app.globalFlagNames,
	]) {
		declaredByParam.set(flagParamName(name), name);
	}
	// A key naming nothing this command declares is a fact about the object's
	// SHAPE, and shape is decided before every phase (§24.11, §24.3): an unknown
	// flag outranks every election, scope, value and presence problem on the
	// command line wherever it sits in argv, so an unknown key does the same
	// here. Checked in its own pass, ahead of the one that reads values, and the
	// supplied values are indexed by DECLARED name on the way through so every
	// pass below walks declarations rather than the caller's key order.
	const suppliedByFlagName = new Map<string, unknown>();
	for (const paramName of Object.keys(kwargs)) {
		const declared = declaredByParam.get(paramName);
		if (declared !== undefined) {
			suppliedByFlagName.set(declared, kwargs[paramName]);
			continue;
		}
		if (argNames.has(paramName)) {
			continue;
		}
		throw new InvokeError(
			errUnknownParameterForCommand(paramName, commandPath),
		);
	}

	// The phase order is the PARSER's, so it governs this door too (§24.3,
	// §18.24 item 243): every problem below is recorded with its stage and the
	// lowest one over the whole command decides, exactly as the flat door and
	// the command line select their refusal.
	const problems: ParseProblem[] = [];

	// Pass 1: every selector's election, command-wide and outermost first, with
	// the record facts that outrank an election recorded beside them. Walking
	// one selector to its END before starting the next decided every stage below
	// election for one selector before the next selector's record was looked at.
	const elections = new Map<AnyChoiceFlag, string>();
	for (const [name, sel] of selectorByName) {
		electFromRecord(
			sel,
			suppliedByFlagName.has(name),
			suppliedByFlagName.get(name),
			[],
			commandPath,
			elections,
			problems,
		);
	}

	// Pass 2: the values, in DECLARATION order. An object has no order of its
	// own, so the order the caller happened to write its keys in decides
	// nothing (§21.4's reason, applied to this door). A selector whose election
	// never settled is skipped: its refusal is already recorded, and there is
	// no elected scope to read values from.
	/**
	 * The properties this call CLEARED, spelled `null` on the property's own
	 * key at this door (contract §27.6).
	 */
	const unsets = new Set<string>();
	const recordValue = (f: AnyFlag, value: unknown): void => {
		// The one stated carve-out from "null is legal for nothing" (§24.11's
		// amendment, §27.6): a NULLABLE PROPERTY is the one declaration for
		// which null is a VALUE -- it means clear this property on the resource,
		// which is a write. It delivers absence, reports provided() true, and is
		// what ctx.unset answers true for. The carve-out is a property of the
		// DECLARATION and never of the door: a null anywhere else is refused by
		// the rule below, unchanged. TypeScript's `undefined` is not a second
		// spelling of it -- absence has its own spelling at this door, an absent
		// key, and a value the declaration names is not absence.
		if (value === null && flagOpts(f).nullable === true) {
			unsets.add(f.name);
			store.set(f.name, undefined, "cli");
			return;
		}
		const outcome = preTypedValueOutcome(f, value);
		if (!outcome.ok) {
			problems.push({ stage: STAGE.value, message: outcome.message });
			return;
		}
		store.set(f.name, outcome.value, "cli");
	};
	for (const d of def.allDecls) {
		const supplied = suppliedByFlagName.has(d.name);
		if (d.kind === "choice-flag") {
			const tag = elections.get(d);
			if (tag === undefined) {
				continue;
			}
			// The source answers §23.6's question about the ELECTION: a record the
			// caller wrote is the call's, and a record the declaration elected is
			// the declaration's however it reached this door -- including the flat
			// door's materialization of a defaulted selection, which is marked as
			// such precisely so the answer does not depend on which door was used.
			const value = suppliedByFlagName.get(d.name);
			store.set(
				d.name,
				supplied
					? buildElectedRecord(
							d,
							value as Record<string, unknown>,
							elections,
							app.infraRoots,
							problems,
							[],
							"",
						)
					: electDefaultRecord(d, tag, app.infraRoots, []),
				supplied && !isDeclarationElected(value) ? "cli" : "default",
			);
			continue;
		}
		if (supplied) {
			recordValue(d, suppliedByFlagName.get(d.name));
		}
	}
	// An app-level global is a declaration like any other, so a value supplied
	// for one is checked like any other (§24.11).
	for (const gf of app.globalFlags) {
		if (suppliedByFlagName.has(gf.name)) {
			recordValue(gf, suppliedByFlagName.get(gf.name));
		}
	}
	const first = firstProblem(problems);
	if (first !== undefined) {
		throw new InvokeError(first.message);
	}

	// Positionals from kwargs, BY NAME. They are handed to the pipeline AS
	// SUPPLIED: a positional arg is a declaration exactly as a flag is, so the
	// value supplied for one is checked against the type it declares rather than
	// stringified into a token the caller never wrote (§24.11) -- and it binds to
	// the arg its KEY names, never to the position the supplied subset happens to
	// leave it in, because a kwargs object has no order of its own (§24.11 item
	// 248, §21.4). An omitted key is the absence presence answers.
	const positionals = new Map<string, unknown>();
	for (const a of def.args) {
		if (Object.hasOwn(kwargs, a.name)) {
			positionals.set(a.name, kwargs[a.name]);
		}
	}

	let validated: Record<string, unknown>;
	let sources: Record<string, string>;
	let writes: UpdateState | null = null;
	try {
		const parsed = validateAndBuildKwargs(
			cmd,
			def.args,
			store,
			{ kind: "pre-typed", byName: positionals },
			app.globalFlagNames,
			app.infraRoots,
			[...selectorByName.keys()],
			[],
			unsets,
		);
		writes = parsed.writes;
		validated = { ...parsed.kwargs, ...parsed.postGlobalValues };
		sources = { ...parsed.sources };
	} catch (e) {
		if (e instanceof ParseError) {
			throw new InvokeError(e.message);
		}
		throw e;
	}

	// Apply global flag defaults for globals not provided in kwargs.
	for (const gf of app.globalFlags) {
		const param = flagParamName(gf.name);
		if (Object.hasOwn(validated, param)) {
			continue;
		}
		const { value, source } = applyFlagDefaultForInvoke(
			gf,
			"global ",
			app.infraRoots,
		);
		validated[param] = value;
		sources[param] = source;
	}

	// Stdout/stderr are discarded for invoke (Go io.Discard): structured data
	// flows back through the return value, not the streams.
	//
	// Programmatic dispatch never PROMPTS and never emits the non-TTY error:
	// the requirement is honoured by the consent check above instead.
	// --dry-run is likewise not reachable here (argv parsing is bypassed
	// entirely), so the effects handle is armed in live mode -- but it IS
	// armed, because the seal is mandatory at every ctx-construction site.
	const effects = app.armEffects(cmd, commandPath, false, reserved);
	const ctx = new Context(
		discard,
		discard,
		sources,
		buildInfraAccess(
			app.infraRoots,
			app.handshakeEnvs,
			app.connectionEnvs,
			false,
		),
		reserved,
		effects,
		cmd.name,
		def.payloadSchema ?? null,
		{
			ownsStdout: def.ownsStdout,
			hasRenderer: def.payloadRenderer !== undefined,
		},
	);
	attachUpdateState(ctx, writes, unsets);
	return await callHandler(
		() => {
			loadRequirements(ctx, def.requires, commandPath);
			return def.handler(validated as never, ctx);
		},
		ctx,
		effects,
	);
}

async function invokePassthrough(
	app: AppImpl,
	cmd: RegisteredCommand,
	commandPath: string,
	kwargs: Readonly<Record<string, unknown>>,
	reserved: ReservedFlags,
): Promise<unknown> {
	let args: readonly string[] = [];
	if (Object.hasOwn(kwargs, "_args")) {
		const rawArgs = kwargs._args;
		if (
			!Array.isArray(rawArgs) ||
			!rawArgs.every((a) => typeof a === "string")
		) {
			throw new InvokeError(errPassthroughArgsNotStringSlice());
		}
		args = rawArgs;
	}

	const globalParamNames = new Set(
		app.globalFlags.map((gf) => flagParamName(gf.name)),
	);
	for (const key of Object.keys(kwargs)) {
		if (key === "_args") {
			continue;
		}
		if (!globalParamNames.has(key)) {
			throw new InvokeError(
				errUnknownParameterForPassthroughCommand(key, commandPath),
			);
		}
	}

	const globals: Record<string, unknown> = {};
	for (const gf of app.globalFlags) {
		const param = flagParamName(gf.name);
		if (Object.hasOwn(kwargs, param)) {
			globals[param] = kwargs[param];
		} else {
			globals[param] = applyFlagDefaultForInvoke(
				gf,
				"global ",
				app.infraRoots,
			).value;
		}
	}

	const def = cmd.def as PassthroughDef<string>;
	const effects = app.armEffects(cmd, commandPath, false, reserved);
	const ctx = new Context(
		discard,
		discard,
		{},
		buildInfraAccess(
			app.infraRoots,
			app.handshakeEnvs,
			app.connectionEnvs,
			false,
		),
		reserved,
		effects,
		cmd.name,
		def.payloadSchema ?? null,
		{
			ownsStdout: def.ownsStdout,
			hasRenderer: def.payloadRenderer !== undefined,
		},
	);
	return await callHandler(
		() => {
			loadRequirements(ctx, def.requires, commandPath);
			return def.handler({ name: cmd.name, args, globals }, ctx);
		},
		ctx,
		effects,
	);
}

// --- Elected records on the programmatic front door (contract §24.11) ---

/**
 * Pass 1 over one selector: which choice the record elects, plus the facts
 * about the record that outrank an election -- its own shape, the payload key
 * a payload-carrying member's election needs, and a key the elected scope
 * never declared. Nothing throws: each problem carries its stage and the
 * caller reports the lowest one over the WHOLE command (§24.3, §24.11).
 *
 * The walk descends into the elected choice's own selectors, outermost first,
 * so every election in the tree is settled before any value is read. `path` is
 * the ELECTIONS ABOVE this selector, so every sentence below renders §12.13's
 * whole path at whatever depth it is written from.
 */
function electFromRecord(
	sel: AnyChoiceFlag,
	supplied: boolean,
	value: unknown,
	path: readonly ScopeStep[],
	commandPath: string,
	elections: Map<AnyChoiceFlag, string>,
	problems: ParseProblem[],
): void {
	const suffix = errScopeSuffix(scopePath(path));
	if (!supplied) {
		const o = sel.opts;
		if (o.presence === "default" && o.default !== undefined) {
			// A defaulted selection is COMPLETE by registration (§24.5), so
			// nothing under it can be missing and the walk stops here.
			elections.set(sel, o.default);
			return;
		}
		problems.push({
			stage: STAGE.presence,
			message:
				sel.electBy === "member-flags"
					? errOneOfRequired(memberList(sel), suffix)
					: `${errFlagRequired(sel.name, "value")}${suffix}`,
		});
		return;
	}
	if (typeof value !== "object" || value === null) {
		problems.push({
			stage: STAGE.shape,
			message: `flag '--${sel.name}': the elected value must be a record carrying its '${CHOICE_TAG_KEY}' tag`,
		});
		return;
	}
	const raw = value as Record<string, unknown>;
	const tag = raw[CHOICE_TAG_KEY];
	if (typeof tag !== "string" || !Object.hasOwn(sel.choices, tag)) {
		problems.push({
			stage: STAGE.shape,
			message: errFlagInvalidChoice(
				sel.name,
				String(tag),
				formatChoices(Object.keys(sel.choices)),
			),
		});
		return;
	}
	elections.set(sel, tag);
	const chosen = sel.choices[tag] as NonNullable<(typeof sel.choices)[string]>;
	const scope: readonly ScopeStep[] = [
		...path,
		{ selector: sel, choiceName: tag },
	];
	// The record's key namespace is the ELECTED CHOICE'S OWN SCOPE: the tag key,
	// the payload key where the choice carries one, and the parameters that
	// scope declares at that level (§24.11 item 246). A key outside that set
	// names nothing the command declares, which is the same fact about the
	// object's SHAPE the flat door refuses one level up -- so it takes that
	// door's sentence with §12.13's scope suffix saying where, and is recorded
	// ahead of every other fact this record carries.
	//
	// The out-of-scope template is refused here: it names a flag the command
	// DECLARES against the scopes that own it, and a key naming nothing anywhere
	// has no other side to name -- rendering it `--bogus` would claim a
	// declaration that does not exist.
	const declared = new Set<string>([
		CHOICE_TAG_KEY,
		CHOICE_VALUE_KEY,
		...Object.keys(chosen.flags),
	]);
	for (const key of Object.keys(raw)) {
		if (!declared.has(key)) {
			problems.push({
				stage: STAGE.shape,
				message:
					errUnknownParameterForCommand(key, commandPath) +
					errScopeSuffix(scopePath(scope)),
			});
		}
	}
	if (chosen.value !== undefined && !Object.hasOwn(raw, CHOICE_VALUE_KEY)) {
		// A member flag is elected by its own token and that token CARRIES the
		// payload, so a record electing one with no `value` is the command line's
		// own `--<member>` with nothing after it, and takes that sentence -- a
		// fact about the record's shape, which is why it outranks an election.
		// The ordinary required-flag message is refused for it: a member flag's
		// scope path stops at the scope that OWNS it (§12.13), so that message
		// would render the member as its own owner.
		problems.push({
			stage: STAGE.shape,
			message: errFlagRequiresValue(`--${tag}`),
		});
	}
	for (const [key, sub] of Object.entries(chosen.flags)) {
		if (sub.kind === "choice-flag") {
			electFromRecord(
				sub,
				Object.hasOwn(raw, key),
				raw[key],
				scope,
				commandPath,
				elections,
				problems,
			);
		}
	}
}

/**
 * Pass 2 over one selector: the record the handler receives, built from the
 * election pass 1 already settled. Every value is checked against the
 * declaration it was supplied against, a required sub-flag that nothing
 * supplied is refused, an optional one delivers absence as a PRESENT field,
 * and a defaulted one is filled from the declaration.
 *
 * Nothing throws: a value refusal and a scoped presence refusal each carry
 * their stage into the same list pass 1 wrote to, so the phases -- and not the
 * order this walk happens to reach them in -- decide which is reported.
 *
 * `path` is the elections ABOVE this selector, so a scoped presence refusal
 * three levels down names the whole path §12.13 renders, exactly as the argv
 * path names it. `origin` is the pinned clause of the OUTERMOST election above
 * that no caller made -- empty while every election on the path was the
 * caller's, and the declaration's clause once one of them was the flat door's
 * materialized default (§12.13, §18.19 item 216, §18.28 items 263 and 264).
 */
function buildElectedRecord(
	sel: AnyChoiceFlag,
	raw: Record<string, unknown>,
	elections: ReadonlyMap<AnyChoiceFlag, string>,
	infraRoots: ReadonlyMap<string, string>,
	problems: ParseProblem[],
	path: readonly ScopeStep[],
	origin: string,
): unknown {
	const tag = elections.get(sel) as string;
	const chosen = sel.choices[tag] as NonNullable<(typeof sel.choices)[string]>;
	const out: Record<string, unknown> = { [CHOICE_TAG_KEY]: tag };
	// A record the flat door materialized from a selector's own default is the
	// DECLARATION's election however far in it sits, and the outermost such
	// election is the one the clause names.
	const electionOrigin =
		origin !== ""
			? origin
			: isDeclarationElected(raw)
				? errElectionOriginDefault
				: "";
	const take = (f: AnyFlag, key: string, value: unknown): void => {
		if (
			(value === undefined || value === null) &&
			f.opts.presence === "optional"
		) {
			// The ONE place an explicit nothing is legal (§18.26 item 252). A scope
			// is an object here, and an object has no way to omit a key it is
			// declaring: `{subject: undefined}` is exactly how a caller writes an
			// optional property that is not set, so it IS the absence key omission
			// spells at the flat boundary -- and it delivers §23.4's own delivery,
			// a present key holding nothing. Every other presence, and every value
			// at the flat boundary, is unchanged: null is legal for nothing there,
			// where absence already has a spelling of its own.
			out[key] = undefined;
			return;
		}
		const outcome = preTypedValueOutcome(f, value);
		if (!outcome.ok) {
			problems.push({ stage: STAGE.value, message: outcome.message });
			return;
		}
		out[key] = outcome.value;
	};
	if (chosen.value !== undefined) {
		// The member's payload is declared by its own flag -- named by the
		// member's own token and required once the member is elected (§24.4) --
		// so the value supplied for it is checked against that declaration.
		take(
			{
				kind: "flag",
				name: tag,
				schema: chosen.value.carrier.schema,
				carrier: chosen.value.carrier,
				opts: { help: chosen.value.help, presence: "required" },
			},
			CHOICE_VALUE_KEY,
			raw[CHOICE_VALUE_KEY],
		);
	}
	const scope: readonly ScopeStep[] = [
		...path,
		{ selector: sel, choiceName: tag },
	];
	// The scope suffix and the origin suffix, in that order and never the other
	// way round (§12.13): where the declaration sits, then the ambient cause a
	// reader cannot see in their own call. The clause is empty for a caller's own
	// election, which is what a caller-supplied record always is.
	const suffix =
		errScopeSuffix(scopePath(scope)) + errElectionOriginSuffix(electionOrigin);
	for (const [key, sub] of Object.entries(chosen.flags)) {
		if (sub.kind === "choice-flag") {
			const subTag = elections.get(sub);
			if (subTag === undefined) {
				continue;
			}
			if (Object.hasOwn(raw, key)) {
				out[key] = buildElectedRecord(
					sub,
					raw[key] as Record<string, unknown>,
					elections,
					infraRoots,
					problems,
					scope,
					electionOrigin,
				);
			} else {
				out[key] = electDefaultRecord(sub, subTag, infraRoots, scope);
			}
			continue;
		}
		if (Object.hasOwn(raw, key)) {
			take(sub, key, raw[key]);
			continue;
		}
		if (sub.opts.presence === "required") {
			// The ROOT sentence plus the suffix (§12.13): a required bool inside a
			// scope names the tokens that satisfy it exactly as it does at root, and
			// the suffix says where the requirement lives and what elected it.
			problems.push({
				stage: STAGE.presence,
				message: `${errFlagRequired(sub.name, requiredFlagForm(sub))}${suffix}`,
			});
			continue;
		}
		// The declaration decides, exactly as it does on the argv path: a compound
		// default is copied and a RelativeToRoot marker resolves through the app's
		// declared infrastructure roots (§23, §24.6).
		out[key] = applyScopedDefaultForInvoke(sub, infraRoots, suffix);
	}
	// Every field the RECORD door delivers reports the DECLARATION, so
	// `provided()` answers false for all of them (§18.26 item 253). The
	// supplied-versus-declared distinction is not decidable at every
	// implementation's record door -- a constructed scope fills its declared
	// defaults before anything can look -- and one accessor answering three ways
	// for one call is the divergence parity forbids, so the door that can answer
	// least decides what the shared answer is. The `cli` heuristic is refused by
	// name: labelling a field by whether its value DIFFERS from the declaration
	// answers a value comparison instead of §23.6's question, and answers false
	// for a caller who deliberately supplied exactly the default.
	//
	// The FLAT door is not in that position and does not inherit the answer
	// (§18.29 item 268): it reads the caller's own keys, so it marks them on the
	// record it converts and those fields report the call, exactly as the command
	// line reports the same values.
	attachProvidedFields(out, new Set(suppliedKeysOf(raw)));
	return out;
}

/**
 * The record a selector's own `default` elects. A defaulted selection is
 * COMPLETE by registration (§24.5), so every sub-flag resolves from its own
 * declaration and nothing can be missing.
 *
 * `path` is the elections ABOVE this selector, so a refusal from inside the
 * selection names the whole path §12.13 renders. This selection is the
 * DECLARATION's, which is the ambient cause a reader cannot see in their own
 * call, so the origin clause names it -- and it is the outermost non-caller
 * election on the path by construction, because a caller-supplied record never
 * reaches this walk.
 */
function electDefaultRecord(
	sel: AnyChoiceFlag,
	choiceName: string,
	infraRoots: ReadonlyMap<string, string>,
	path: readonly ScopeStep[],
): unknown {
	const chosen = sel.choices[choiceName];
	const out: Record<string, unknown> = { [CHOICE_TAG_KEY]: choiceName };
	const scope: readonly ScopeStep[] = [...path, { selector: sel, choiceName }];
	const suffix =
		errScopeSuffix(scopePath(scope)) +
		errElectionOriginSuffix(errElectionOriginDefault);
	for (const [key, sub] of Object.entries(chosen?.flags ?? {})) {
		if (sub.kind === "choice-flag") {
			out[key] =
				sub.opts.default === undefined
					? undefined
					: electDefaultRecord(sub, sub.opts.default, infraRoots, scope);
			continue;
		}
		out[key] = applyScopedDefaultForInvoke(sub, infraRoots, suffix);
	}
	attachProvidedFields(out, new Set());
	return out;
}
