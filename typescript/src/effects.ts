/**
 * The effects regime: the `ctx.effects` handle, `Unsettled` carriers, dry
 * mode's would-do log, and the runtime seal that makes a preview honest.
 *
 * Two rules govern the whole module and override any local convenience:
 *
 * - FAIL CLOSED. When the framework cannot prove an operation is safe to
 *   preview, it stops with a precise error instead of guessing.
 * - ZERO INFERENCE. Nothing is inferred -- not classification, not whether an
 *   argument is a path, not whether a resource is current.
 *
 * The method set is CLOSED at eight (`run`, `spawn`, `write`, `mkdir`,
 * `remove`, `rename`, `chmod`, `http`). CACHE_WRITE has no public method: it is
 * minted only by framework-internal code (schema dump, coverage shards and
 * manifest) and is unreachable from application code.
 *
 * Every effect method's declared return type is its SETTLED shape and nothing
 * else -- there is no `| Unsettled` union anywhere in the surface and no
 * narrowing predicate. In dry mode the runtime value at those positions is the
 * `Unsettled` Proxy, which the static type deliberately does not mention: a
 * handler that only forwards it never notices, and a handler that extracts from
 * it or branches on it trips the runtime seal and truncates the preview. One
 * handler body, both modes.
 *
 * Parity source: python/strictcli/__init__.py (the reference implementation).
 */

import { spawnSync } from "node:child_process";
import {
	chmodSync,
	mkdirSync,
	renameSync,
	rmSync,
	writeFileSync,
} from "node:fs";
import { httpSync, spawnConcurrent } from "./effects_exec.js";
import {
	DryRunTruncated,
	EffectFailed,
	errDryRunTruncated,
	errEffectArgvEmpty,
	errEffectArgvNotSequence,
	errEffectGrantKindMismatch,
	errEffectGrantOnObserve,
	errEffectGrantUndeclared,
	errEffectHTTPFailed,
	errEffectHTTPMethodNotString,
	errEffectModeNotInt,
	errEffectMutatingInReadOnly,
	errEffectOptionNotAccepted,
	errEffectOutputNotUTF8,
	errEffectParamNotStringish,
	errEffectParamRejectsCarrier,
	errEffectRunFailed,
	errEffectRunNotAllowlisted,
} from "./errors.js";
import { type TraceIdentity, traceChildEnv } from "./trace.js";

// --- Effect kinds ---

/**
 * The coarse taxonomy of an effect, and the set a Grant may be declared for.
 * `cache_write` is deliberately absent: it is unreachable from application
 * code, so nothing could ever use such a grant.
 */
export type EffectKind =
	| "proc_mutate"
	| "proc_spawn"
	| "file_write"
	| "net_mutate";

const PROC_MUTATE = "proc_mutate";
const PROC_SPAWN = "proc_spawn";
const FILE_WRITE = "file_write";
const NET_MUTATE = "net_mutate";
/** Framework-blessed cache writes. No public method mints one. */
export const CACHE_WRITE = "cache_write";

const GRANTABLE_KINDS: readonly string[] = [
	PROC_MUTATE,
	PROC_SPAWN,
	FILE_WRITE,
	NET_MUTATE,
];

/** True when `kind` names one of the four grantable effect kinds. */
export function isGrantableKind(kind: unknown): kind is EffectKind {
	return typeof kind === "string" && GRANTABLE_KINDS.includes(kind);
}

/** The two legal command classifications. There is no default. */
export type Effect = "read_only" | "mutating";

// --- Grants ---

/**
 * A per-command, per-effect-kind authorization with a mandatory reason.
 *
 * A grant is not permission to do something otherwise forbidden; it is a
 * LABELLED reason that surfaces in the preview so a reviewer reading a dry run
 * sees why a dangerous step is there.
 */
export interface Grant {
	/** Matches [a-z][a-z0-9-]*; unique within the command. */
	readonly name: string;
	/** Non-empty; rendered verbatim in the log's grant suffix. */
	readonly reason: string;
	readonly kind: EffectKind;
}

/**
 * Declares that a handler deliberately accepts and forwards its arguments.
 * Inert in TypeScript beyond the schema emission (guard v2's enforcement is
 * Python-only -- a TS handler takes a typed args object, which cannot be
 * introspected for a var-keyword parameter), but declared in all three
 * implementations so the API surface stays in parity.
 */
export interface Forwarding {
	readonly reason: string;
}

// --- Result shapes ---

/**
 * The result of a subprocess that ran to completion.
 *
 * `stdout`/`stderr` are the child's output decoded as UTF-8 strictly, with a
 * single trailing newline removed if present -- the form that can be forwarded
 * straight into a later effect's argv.
 */
export interface Completed {
	readonly exitCode: number;
	readonly stdout: string;
	readonly stderr: string;
}

/** The result of an HTTP request. Header names are lower-cased. */
export interface Response {
	readonly status: number;
	readonly body: Uint8Array;
	readonly headers: Readonly<Record<string, string>>;
}

/** A handle for a started-but-not-awaited child process. */
export interface Spawned {
	readonly pid: number;
	/**
	 * Waits for the child and returns its Completed result. `check` mirrors
	 * `run`'s opt-out: with the default `true` a nonzero exit throws
	 * EffectFailed. Because a spawned child always streams, the returned
	 * `stdout`/`stderr` are empty strings.
	 */
	wait(opts?: { readonly check?: boolean }): Completed;
}

/** Concrete Completed (a class so the forwarding boundary can recognize it). */
class CompletedResult implements Completed {
	constructor(
		readonly exitCode: number,
		readonly stdout: string,
		readonly stderr: string,
	) {}
}

/** Concrete Response (a class so the forwarding boundary can recognize it). */
class ResponseResult implements Response {
	constructor(
		readonly status: number,
		readonly body: Uint8Array,
		readonly headers: Readonly<Record<string, string>>,
	) {}
}

/** Concrete Spawned (a class so the forwarding boundary can reject it). */
class SpawnedResult implements Spawned {
	constructor(
		readonly pid: number,
		private readonly child: { wait(): number },
		private readonly cmdPath: string,
		private readonly argvText: string,
	) {}

	wait(opts: { readonly check?: boolean } = {}): Completed {
		rejectUnacceptedOptions(this.cmdPath, "spawn", opts, WAIT_OPTION_KEYS);
		const code = this.child.wait();
		if (opts.check !== false && code !== 0) {
			throw new EffectFailed(
				errEffectRunFailed(this.cmdPath, "spawn", this.argvText, code),
			);
		}
		return new CompletedResult(code, "", "");
	}
}

// --- Unsettled carriers ---

/**
 * The internal brand the effects API reads at the forwarding boundary. It is
 * one of exactly three exemptions from the Proxy's throwing traps.
 */
const UNSETTLED_BRAND = Symbol("strictcli.unsettled");
const NODE_INSPECT = Symbol.for("nodejs.util.inspect.custom");

interface UnsettledState {
	readonly brand: string;
	/**
	 * Void results (write/mkdir/remove/rename/chmod) and spawn results have no
	 * scalar projection, so they are never forwardable -- in either mode.
	 */
	readonly forwardable: boolean;
	readonly cmdPath: string;
	readonly log: EffectLog;
}

/**
 * Builds the runtime carrier: a Proxy whose get/set/has/deleteProperty/apply/
 * ownKeys traps all throw the truncation error, with exactly three exemptions
 * (the internal brand symbol, Symbol.toStringTag, and the Node inspect symbol,
 * so console/debugger output never itself detonates). `valueOf`, `toString`
 * and Symbol.toPrimitive are NOT exempt -- they throw.
 *
 * Accepted TS ceiling: plain-JS truthiness (`if (carrier)`) and `===` cannot be
 * trapped by a Proxy. Those two gaps are lint-visible only (the effects-bypass
 * check names them explicitly) and are recorded, not fixed.
 */
function makeUnsettled(state: UnsettledState): never {
	// A callable target so the `apply` trap is reachable.
	const target = function unsettled(): void {} as unknown as object;
	const boom = (): never => {
		throw state.log.truncate(state.cmdPath, state.brand);
	};
	return new Proxy(target, {
		get(_t, prop): unknown {
			if (prop === UNSETTLED_BRAND) {
				return state;
			}
			if (prop === Symbol.toStringTag) {
				return "Unsettled";
			}
			if (prop === NODE_INSPECT) {
				return (): string => `Unsettled(${state.brand})`;
			}
			return boom();
		},
		set: boom,
		has: boom,
		deleteProperty: boom,
		apply: boom,
		ownKeys: boom,
	}) as never;
}

/** Reads a value's carrier state without tripping the seal, or undefined. */
function unsettledStateOf(value: unknown): UnsettledState | undefined {
	if (value === null || value === undefined) {
		return undefined;
	}
	if (typeof value !== "object" && typeof value !== "function") {
		return undefined;
	}
	return (value as Record<symbol, unknown>)[UNSETTLED_BRAND] as
		| UnsettledState
		| undefined;
}

/** True when the value is any carrier or settled effect result. */
function isEffectValue(value: unknown): boolean {
	return (
		unsettledStateOf(value) !== undefined ||
		value instanceof CompletedResult ||
		value instanceof ResponseResult ||
		value instanceof SpawnedResult
	);
}

// --- The structured effect log ---

/** One entry in the structured effect log (the conformance surface's shape). */
export interface EffectRecord {
	readonly seq: number;
	readonly kind: string;
	readonly verb: string;
	readonly detail: string;
	readonly bytes: number | null;
	readonly resource: string | undefined;
	readonly skipIfCurrent: string | undefined;
	readonly grant: string | undefined;
	readonly grantReason: string | undefined;
	/** true when the effect was RECORDED instead of performed. */
	readonly recorded: boolean;
}

/** The would-do log's header line. The dash is U+2014 EM DASH. */
export const DRY_RUN_HEADER = "DRY RUN — no changes were made. Would do:";

/** Renders one record as a would-do log line (without the two-space indent). */
function renderRecord(rec: EffectRecord): string {
	let line = `${rec.seq}. ${rec.verb}: ${rec.detail}`;
	if (rec.grant !== undefined) {
		line += ` (granted: ${rec.grant} — ${rec.grantReason})`;
	}
	if (rec.skipIfCurrent !== undefined) {
		line += ` [unless resource '${rec.skipIfCurrent}' already current]`;
	}
	return line;
}

/** The ordered effect records produced by one dispatch. */
export class EffectLog {
	readonly records: EffectRecord[] = [];
	/**
	 * Set the first time a carrier is extracted from. The dispatch sites
	 * consult it after the handler returns so a handler that SWALLOWS the
	 * thrown DryRunTruncated still fails closed (the TS ceiling for Python's
	 * BaseException-derived twin).
	 */
	truncated: DryRunTruncated | null = null;

	/**
	 * Claimed rendering (contract §19.7). `claimed` is set by
	 * `ctx.effects.recorded()`; `handlerRendered` by
	 * `ctx.effects.renderLog()`. The seam skips its own emission only when the
	 * handler both claimed AND rendered -- a claim that never rendered is
	 * re-rendered there, so §3.5's guarantee survives the claim intact.
	 */
	claimed = false;
	handlerRendered = false;

	/**
	 * An update command's write-set line (contract §27.5), rendered between the
	 * header and the first effect and taking NO sequence number -- the counter
	 * is contiguous over rendered EFFECTS, and a write set is not one. Empty on
	 * every command that declares no update, and set only for a dry run: a live
	 * run's write set rides the envelope instead.
	 */
	writeSetLine = "";

	/** True when the handler already produced the log's bytes (§19.7). */
	seamSuppressed(): boolean {
		return this.claimed && this.handlerRendered;
	}

	/**
	 * TWO counters, deliberately. Would-do numbering is the numbering of the
	 * RENDERED lines: it feeds the log's `<N>.` prefix, the `«step N output»`
	 * brand and the truncation error's "ends at step N". CACHE_WRITEs are never
	 * rendered, so they must never consume one of those numbers -- otherwise a
	 * coverage-instrumented run would silently start its preview at `2.`. They
	 * get their own sequence instead, so every record still carries a `seq`.
	 */
	private renderedCount = 0;
	private cachedCount = 0;

	append(rec: EffectRecord): void {
		this.records.push(rec);
		if (rec.kind === CACHE_WRITE) {
			this.cachedCount += 1;
		} else {
			this.renderedCount += 1;
		}
	}

	/** The next would-do number. Pure: callers may ask without appending. */
	nextSeq(): number {
		return this.renderedCount + 1;
	}

	/** The next CACHE_WRITE number, on its own counter. */
	nextCacheSeq(): number {
		return this.cachedCount + 1;
	}

	/** Renders the would-do log. CACHE_WRITEs are never written to it. */
	render(): string {
		const lines = [DRY_RUN_HEADER];
		if (this.writeSetLine !== "") {
			lines.push(`  ${this.writeSetLine}`);
		}
		for (const rec of this.records) {
			if (rec.kind === CACHE_WRITE) {
				continue;
			}
			lines.push(`  ${renderRecord(rec)}`);
		}
		return lines.join("\n");
	}

	/** The structured records, with the pinned snake_case wire keys. */
	toList(): Record<string, unknown>[] {
		return this.records.map((rec) => {
			const d: Record<string, unknown> = {
				seq: rec.seq,
				kind: rec.kind,
				verb: rec.verb,
				detail: rec.detail,
				recorded: rec.recorded,
			};
			if (rec.bytes !== null) {
				d.bytes = rec.bytes;
			}
			if (rec.resource !== undefined) {
				d.resource = rec.resource;
			}
			if (rec.skipIfCurrent !== undefined) {
				d.skip_if_current = rec.skipIfCurrent;
			}
			if (rec.grant !== undefined) {
				d.grant = rec.grant;
			}
			return d;
		});
	}

	/** Mints (and remembers) the truncation error for a carrier extraction. */
	truncate(cmdPath: string, brand: string): DryRunTruncated {
		const step = this.nextSeq();
		const err = new DryRunTruncated(
			errDryRunTruncated(step, cmdPath, brand),
			step,
			cmdPath,
			brand,
		);
		this.truncated ??= err;
		return err;
	}

	/** Records a framework-blessed CACHE_WRITE (executes even in dry mode). */
	recordCacheWrite(path: string): void {
		this.append({
			seq: this.nextCacheSeq(),
			kind: CACHE_WRITE,
			verb: "cache",
			detail: path,
			bytes: null,
			resource: undefined,
			skipIfCurrent: undefined,
			grant: undefined,
			grantReason: undefined,
			recorded: false,
		});
	}
}

// --- Option-key validation ---

/**
 * Canonical snake_case option names, so the rendered
 * errEffectOptionNotAccepted message is byte-identical across the three
 * implementations even though TypeScript spells the key camelCase.
 */
const CANONICAL_OPTION_NAMES: Readonly<Record<string, string>> = {
	resource: "resource",
	skipIfCurrent: "skip_if_current",
	grant: "grant",
	cwd: "cwd",
	env: "env",
	check: "check",
	stream: "stream",
	body: "body",
	headers: "headers",
};

const COMMON_OPTION_KEYS = ["resource", "skipIfCurrent", "grant"] as const;
const RUN_OPTION_KEYS = [
	...COMMON_OPTION_KEYS,
	"cwd",
	"env",
	"check",
	"stream",
];
const SPAWN_OPTION_KEYS = [...COMMON_OPTION_KEYS, "cwd", "env"];
const PATH_OPTION_KEYS = [...COMMON_OPTION_KEYS];
const HTTP_OPTION_KEYS = [...COMMON_OPTION_KEYS, "body", "headers", "check"];
const WAIT_OPTION_KEYS = ["check"];

/**
 * An option a method does not accept is a call-time HARD ERROR. Silently
 * ignoring an inapplicable option is the one outcome declare-everything cannot
 * have.
 */
function rejectUnacceptedOptions(
	cmdPath: string,
	method: string,
	opts: object,
	accepted: readonly string[],
): void {
	for (const key of Object.keys(opts)) {
		if ((opts as Record<string, unknown>)[key] === undefined) {
			// An explicitly-undefined key is the absent key: TS callers spread
			// optional values, and the siblings never see such a keyword at all.
			continue;
		}
		if (!accepted.includes(key)) {
			throw new Error(
				errEffectOptionNotAccepted(
					cmdPath,
					method,
					CANONICAL_OPTION_NAMES[key] ?? key,
				),
			);
		}
	}
}

// --- Shared decoding ---

const STRICT_UTF8 = new TextDecoder("utf-8", { fatal: true });

/** Decodes captured output as UTF-8 strictly, dropping one trailing newline. */
function decodeEffectOutput(
	data: Uint8Array | null | undefined,
	cmdPath: string,
	method: string,
): string {
	if (data === null || data === undefined) {
		return "";
	}
	let text: string;
	try {
		text = STRICT_UTF8.decode(data);
	} catch {
		throw new EffectFailed(errEffectOutputNotUTF8(cmdPath, method));
	}
	return text.endsWith("\n") ? text.slice(0, -1) : text;
}

/** Runtime type name for the effect argument type-guard messages. */
export function effectTypeName(v: unknown): string {
	if (v === null) {
		return "null";
	}
	if (Array.isArray(v)) {
		return "Array";
	}
	if (v instanceof Uint8Array) {
		return "Uint8Array";
	}
	if (typeof v === "object") {
		const ctor = (v as { constructor?: { name?: string } }).constructor?.name;
		return ctor !== undefined && ctor !== "" ? ctor : "object";
	}
	return typeof v;
}

// --- The effects handle ---

/** What the effects handle needs to know about the running command. */
export interface EffectsCommandView {
	/** The dotted command path (`release.run`, not the app name). */
	readonly cmdPath: string;
	readonly effect: Effect;
	readonly grants: readonly Grant[];
}

/** The read-only slice of the effects handle: observes and nothing else. */
export interface ReadOnlyEffects {
	/**
	 * Runs a subprocess to completion. In a read_only command this is legal
	 * only when the argv matches a prefix on the app's procObserveAllowlist
	 * (an observe): it executes even in dry mode, returns a real value, and is
	 * never written to the would-do log.
	 */
	run(
		argv: readonly (string | Completed | Response)[],
		opts?: {
			readonly cwd?: string;
			readonly env?: Readonly<Record<string, string>>;
			readonly check?: boolean;
			readonly stream?: boolean;
			readonly resource?: string;
			readonly skipIfCurrent?: string;
			readonly grant?: string;
		},
	): Completed;
	/**
	 * Returns the records recorded so far in this dispatch (contract §19.7).
	 *
	 * The shape is §14.2's, the same one §14.3's accessor returns for the
	 * whole run. Calling it CLAIMS the render: the framework's own
	 * end-of-dispatch emission is suppressed for the rest of the run, so a
	 * handler can put the preview where it wants it. A claim that never
	 * renders is re-rendered at the seam -- claiming moves the render, it can
	 * never remove it (§3.5). In machine mode claiming changes nothing.
	 */
	recorded(): Record<string, unknown>[];
	/**
	 * Renders the would-do log in §3.2's exact form, here (contract §19.7).
	 *
	 * Byte-identical to what the framework would have emitted at the end of
	 * the dispatch -- one renderer, one record list -- so this moves the
	 * preview in the stream and never changes its content. Calling it also
	 * claims the render, which is what keeps the log from appearing twice. A
	 * no-op in machine mode and outside dry mode, in both cases because those
	 * are exactly the runs where the framework's own emission produces
	 * nothing.
	 */
	renderLog(): void;
}

/**
 * The full effects handle: exactly eight effect methods, and the set is
 * CLOSED. §19.7's two claimed-rendering calls ride the same handle and mint no
 * effect at all.
 */
export interface MutatingEffects extends ReadOnlyEffects {
	/** Starts a subprocess without waiting. Spawning is itself an effect. */
	spawn(
		argv: readonly (string | Completed | Response)[],
		opts?: {
			readonly cwd?: string;
			readonly env?: Readonly<Record<string, string>>;
			readonly resource?: string;
			readonly skipIfCurrent?: string;
			readonly grant?: string;
		},
	): Spawned;
	/** Writes bytes to a path. */
	write(
		path: string | Completed | Response,
		content: string | Uint8Array | Completed | Response,
		opts?: {
			readonly resource?: string;
			readonly skipIfCurrent?: string;
			readonly grant?: string;
		},
	): void;
	/** Creates a directory, parents included; an existing one is not an error. */
	mkdir(
		path: string | Completed | Response,
		opts?: {
			readonly resource?: string;
			readonly skipIfCurrent?: string;
			readonly grant?: string;
		},
	): void;
	/** Removes a file, symlink or directory tree recursively; missing is fine. */
	remove(
		path: string | Completed | Response,
		opts?: {
			readonly resource?: string;
			readonly skipIfCurrent?: string;
			readonly grant?: string;
		},
	): void;
	/** Moves/renames a path. */
	rename(
		src: string | Completed | Response,
		dst: string | Completed | Response,
		opts?: {
			readonly resource?: string;
			readonly skipIfCurrent?: string;
			readonly grant?: string;
		},
	): void;
	/** Changes a path's mode (rendered in the log as leading-zero octal). */
	chmod(
		path: string | Completed | Response,
		mode: number,
		opts?: {
			readonly resource?: string;
			readonly skipIfCurrent?: string;
			readonly grant?: string;
		},
	): void;
	/** Performs a network request. */
	http(
		method: string,
		url: string | Completed | Response,
		opts?: {
			readonly body?: Uint8Array;
			readonly headers?: Readonly<Record<string, string>>;
			readonly check?: boolean;
			readonly resource?: string;
			readonly skipIfCurrent?: string;
			readonly grant?: string;
		},
	): Response;
}

interface CommonOpts {
	readonly resource?: string;
	readonly skipIfCurrent?: string;
	readonly grant?: string;
}

/** `[runtimeValue, rendered]`; runtimeValue is null when unsettled. */
type Operand = [string | null, string];

export class Effects implements MutatingEffects {
	private readonly cmdPath: string;
	private readonly effect: Effect;
	private readonly grants: ReadonlyMap<string, Grant>;
	private readonly dryRun: boolean;
	private readonly log: EffectLog;
	private readonly allowlist: readonly (readonly string[])[];
	private readonly trace: TraceIdentity;
	private mutationRecorded = false;
	/**
	 * The human stream renderLog() writes to, and the mode that makes it a
	 * no-op (contract §19.7).
	 */
	private readonly out: { write(text: string): void } | undefined;
	private readonly json: boolean;
	/**
	 * Where a child's stdout goes instead of the process stdout, or undefined
	 * when children inherit it (contract §19.11): set in machine mode on a
	 * command that does not own stdout.
	 */
	private readonly childStdout: ((text: string) => void) | undefined;
	/** Captured spawns not yet waited on, drained by the exit step. */
	private readonly pendingSpawns: { wait(): number }[] = [];

	constructor(
		cmd: EffectsCommandView,
		dryRun: boolean,
		log: EffectLog,
		allowlist: readonly (readonly string[])[],
		trace: TraceIdentity,
		out?: { write(text: string): void },
		json = false,
		childStdout?: (text: string) => void,
	) {
		this.cmdPath = cmd.cmdPath;
		this.effect = cmd.effect;
		this.grants = new Map(cmd.grants.map((g) => [g.name, g]));
		this.dryRun = dryRun;
		this.log = log;
		this.allowlist = allowlist;
		this.trace = trace;
		this.out = out;
		this.json = json;
		this.childStdout = childStdout;
	}

	/**
	 * Package-internal: waits for every captured spawn the handler never
	 * waited on, so its stdout reaches the `output` member no later than the
	 * exit step (contract §19.11).
	 */
	drainCapturedSpawns(): void {
		for (const child of this.pendingSpawns.splice(0)) {
			child.wait();
		}
	}

	/** The capture callback for a child's raw stdout bytes, or undefined. */
	private stdoutCapture(): ((bytes: Uint8Array) => void) | undefined {
		const sink = this.childStdout;
		if (sink === undefined) {
			return undefined;
		}
		return (bytes) => sink(decodeChildStdout(bytes));
	}

	// -- claimed rendering (contract §19.7) ------------------------------

	recorded(): Record<string, unknown>[] {
		this.log.claimed = true;
		return this.log.toList();
	}

	renderLog(): void {
		this.log.claimed = true;
		if (this.json || !this.dryRun) {
			return;
		}
		this.log.handlerRendered = true;
		const sink = this.out ?? process.stdout;
		sink.write(`${this.log.render()}\n`);
	}

	// -- helpers ---------------------------------------------------------

	/** Hard-errors when a carrier reaches a parameter that cannot take one. */
	private rejectCarrierParams(
		method: string,
		params: Readonly<Record<string, unknown>>,
	): void {
		for (const [param, value] of Object.entries(params)) {
			if (isEffectValue(value)) {
				throw new Error(
					errEffectParamRejectsCarrier(this.cmdPath, method, param),
				);
			}
			if (
				typeof value === "object" &&
				value !== null &&
				!(value instanceof Uint8Array)
			) {
				for (const inner of Object.values(value as Record<string, unknown>)) {
					if (isEffectValue(inner)) {
						throw new Error(
							errEffectParamRejectsCarrier(this.cmdPath, method, param),
						);
					}
				}
			}
		}
	}

	/**
	 * Resolves a carrier-accepting parameter through its shape's declared
	 * SCALAR PROJECTION: Completed -> stdout, Response -> decoded body,
	 * Spawned -> none, void -> none.
	 */
	private operand(value: unknown, method: string, param: string): Operand {
		const state = unsettledStateOf(value);
		if (state !== undefined) {
			if (!state.forwardable) {
				throw new Error(
					errEffectParamRejectsCarrier(this.cmdPath, method, param),
				);
			}
			return [null, state.brand];
		}
		if (value instanceof SpawnedResult) {
			throw new Error(
				errEffectParamRejectsCarrier(this.cmdPath, method, param),
			);
		}
		if (value instanceof CompletedResult) {
			return [value.stdout, value.stdout];
		}
		if (value instanceof ResponseResult) {
			const text = decodeEffectOutput(value.body, this.cmdPath, "http");
			return [text, text];
		}
		if (typeof value === "string") {
			return [value, value];
		}
		throw new Error(
			errEffectParamNotStringish(
				this.cmdPath,
				method,
				param,
				effectTypeName(value),
			),
		);
	}

	/**
	 * Resolves `write`'s content. The rendered form is the encoded byte count
	 * for a settled value, and the forwarded carrier's brand when the content is
	 * UNSETTLED: nothing produced those bytes, and the framework will not invent
	 * a count.
	 */
	private contentOperand(value: unknown): [Uint8Array | null, string] {
		if (value instanceof Uint8Array) {
			return [value, `${value.byteLength} bytes`];
		}
		if (typeof value === "string") {
			const data = new TextEncoder().encode(value);
			return [data, `${data.byteLength} bytes`];
		}
		const [runtime, rendered] = this.operand(value, "write", "content");
		if (runtime === null) {
			return [null, rendered];
		}
		const data = new TextEncoder().encode(runtime);
		return [data, `${data.byteLength} bytes`];
	}

	/** Read-only enforcement plus grant validation, at CALL time. */
	private authorize(
		method: string,
		kind: EffectKind,
		grant: string | undefined,
	): Grant | undefined {
		if (this.effect === "read_only") {
			throw new Error(errEffectMutatingInReadOnly(this.cmdPath, method));
		}
		return this.checkGrant(kind, grant);
	}

	private checkGrant(
		kind: EffectKind,
		grant: string | undefined,
	): Grant | undefined {
		if (grant === undefined) {
			return undefined;
		}
		const declared = this.grants.get(grant);
		if (declared === undefined) {
			throw new Error(errEffectGrantUndeclared(this.cmdPath, grant));
		}
		if (declared.kind !== kind) {
			throw new Error(
				errEffectGrantKindMismatch(this.cmdPath, grant, declared.kind, kind),
			);
		}
		return declared;
	}

	private record(spec: {
		kind: string;
		verb: string;
		detail: string;
		opts: CommonOpts;
		grant: Grant | undefined;
		bytes?: number | null;
		recorded: boolean;
	}): EffectRecord {
		const rec: EffectRecord = {
			seq: this.log.nextSeq(),
			kind: spec.kind,
			verb: spec.verb,
			detail: spec.detail,
			bytes: spec.bytes ?? null,
			resource: spec.opts.resource,
			skipIfCurrent: spec.opts.skipIfCurrent,
			grant: spec.grant?.name,
			grantReason: spec.grant?.reason,
			recorded: spec.recorded,
		};
		this.log.append(rec);
		return rec;
	}

	private carrier(seq: number, forwardable: boolean): never {
		this.mutationRecorded = true;
		return makeUnsettled({
			brand: `«step ${seq} output»`,
			forwardable,
			cmdPath: this.cmdPath,
			log: this.log,
		});
	}

	private stale(descr: string): never {
		return makeUnsettled({
			brand: `«stale: ${descr}»`,
			forwardable: true,
			cmdPath: this.cmdPath,
			log: this.log,
		});
	}

	/** Element-wise argv-prefix matching by string equality. Nothing else. */
	private isObserve(argv: readonly (string | null)[]): boolean {
		for (const prefix of this.allowlist) {
			if (prefix.length > argv.length) {
				continue;
			}
			if (prefix.every((p, i) => argv[i] === p)) {
				return true;
			}
		}
		return false;
	}

	private resolveArgv(
		argv: unknown,
		method: string,
	): [(string | null)[], string[]] {
		if (!Array.isArray(argv)) {
			throw new Error(
				errEffectArgvNotSequence(this.cmdPath, method, effectTypeName(argv)),
			);
		}
		if (argv.length === 0) {
			throw new Error(errEffectArgvEmpty(this.cmdPath, method));
		}
		const runtime: (string | null)[] = [];
		const rendered: string[] = [];
		argv.forEach((element, i) => {
			const [r, text] = this.operand(element, method, `argv[${i}]`);
			runtime.push(r);
			rendered.push(text);
		});
		return [runtime, rendered];
	}

	private settled(value: string | null, method: string, param: string): string {
		if (value === null) {
			// Unreachable: an unsettled operand only survives in dry mode, where
			// nothing executes. Kept as a fail-closed backstop.
			throw new Error(
				errEffectParamRejectsCarrier(this.cmdPath, method, param),
			);
		}
		return value;
	}

	private settledArgv(
		runtime: readonly (string | null)[],
		method: string,
	): string[] {
		return runtime.map((el, i) => this.settled(el, method, `argv[${i}]`));
	}

	/** `env` merges OVER the inherited environment, never replacing it. */
	private mergedEnv(
		env: Readonly<Record<string, string>> | undefined,
	): Record<string, string> | undefined {
		if (env === undefined) {
			return undefined;
		}
		const merged: Record<string, string> = {};
		for (const [k, v] of Object.entries(process.env)) {
			if (v !== undefined) {
				merged[k] = v;
			}
		}
		for (const [k, v] of Object.entries(env)) {
			merged[String(k)] = String(v);
		}
		return merged;
	}

	// -- the eight methods -----------------------------------------------

	run(
		argv: readonly (string | Completed | Response)[],
		opts: {
			readonly cwd?: string;
			readonly env?: Readonly<Record<string, string>>;
			readonly check?: boolean;
			readonly stream?: boolean;
			readonly resource?: string;
			readonly skipIfCurrent?: string;
			readonly grant?: string;
		} = {},
	): Completed {
		rejectUnacceptedOptions(this.cmdPath, "run", opts, RUN_OPTION_KEYS);
		this.rejectCarrierParams("run", {
			cwd: opts.cwd,
			env: opts.env,
			check: opts.check,
			stream: opts.stream,
			resource: opts.resource,
			skipIfCurrent: opts.skipIfCurrent,
			grant: opts.grant,
		});
		const [runtime, rendered] = this.resolveArgv(argv, "run");
		const joined = rendered.join(" ");

		if (this.isObserve(runtime)) {
			// An observe changes nothing: it is legal in a read_only command,
			// never written to the would-do log, and never carries a grant.
			if (opts.grant !== undefined) {
				throw new Error(errEffectGrantOnObserve(this.cmdPath, opts.grant));
			}
			if (this.dryRun && this.mutationRecorded) {
				return this.stale(joined);
			}
			return this.execRun(runtime, joined, opts, "run");
		}

		if (this.effect === "read_only") {
			throw new Error(errEffectRunNotAllowlisted(this.cmdPath, joined));
		}
		const declared = this.checkGrant(PROC_MUTATE, opts.grant);

		if (this.dryRun) {
			const rec = this.record({
				kind: PROC_MUTATE,
				verb: "run",
				detail: joined,
				opts,
				grant: declared,
				recorded: true,
			});
			return this.carrier(rec.seq, true);
		}
		this.record({
			kind: PROC_MUTATE,
			verb: "run",
			detail: joined,
			opts,
			grant: declared,
			recorded: false,
		});
		return this.execRun(runtime, joined, opts, "run");
	}

	spawn(
		argv: readonly (string | Completed | Response)[],
		opts: {
			readonly cwd?: string;
			readonly env?: Readonly<Record<string, string>>;
			readonly resource?: string;
			readonly skipIfCurrent?: string;
			readonly grant?: string;
		} = {},
	): Spawned {
		rejectUnacceptedOptions(this.cmdPath, "spawn", opts, SPAWN_OPTION_KEYS);
		this.rejectCarrierParams("spawn", {
			cwd: opts.cwd,
			env: opts.env,
			resource: opts.resource,
			skipIfCurrent: opts.skipIfCurrent,
			grant: opts.grant,
		});
		const [runtime, rendered] = this.resolveArgv(argv, "spawn");
		const joined = rendered.join(" ");
		const declared = this.authorize("spawn", PROC_SPAWN, opts.grant);

		if (this.dryRun) {
			const rec = this.record({
				kind: PROC_SPAWN,
				verb: "spawn",
				detail: joined,
				opts,
				grant: declared,
				recorded: true,
			});
			// A Spawned has no scalar projection, so its carrier is not
			// forwardable.
			return this.carrier(rec.seq, false);
		}
		this.record({
			kind: PROC_SPAWN,
			verb: "spawn",
			detail: joined,
			opts,
			grant: declared,
			recorded: false,
		});
		const capture = this.stdoutCapture();
		const child = spawnConcurrent(
			this.settledArgv(runtime, "spawn"),
			opts.cwd,
			traceChildEnv(this.mergedEnv(opts.env), this.trace),
			capture,
		);
		if (capture !== undefined) {
			this.pendingSpawns.push(child);
		}
		return new SpawnedResult(child.pid, child, this.cmdPath, joined);
	}

	write(
		path: string | Completed | Response,
		content: string | Uint8Array | Completed | Response,
		opts: CommonOpts = {},
	): void {
		rejectUnacceptedOptions(this.cmdPath, "write", opts, PATH_OPTION_KEYS);
		this.rejectCarrierParams("write", {
			resource: opts.resource,
			skipIfCurrent: opts.skipIfCurrent,
			grant: opts.grant,
		});
		const [rtPath, renderedPath] = this.operand(path, "write", "path");
		const [data, renderedContent] = this.contentOperand(content);
		const detail = `${renderedPath} (${renderedContent})`;
		const declared = this.authorize("write", FILE_WRITE, opts.grant);
		const bytes = data === null ? null : data.byteLength;

		if (this.dryRun) {
			const rec = this.record({
				kind: FILE_WRITE,
				verb: "write",
				detail,
				opts,
				grant: declared,
				bytes,
				recorded: true,
			});
			// The declared return type is `void`, but dry mode must hand back the
			// carrier so a later forward of a VOID result is rejected as such
			// (errEffectParamRejectsCarrier) instead of as a non-string. carrier()
			// is typed `never` precisely so the settled-only surface holds.
			// biome-ignore lint/correctness/noVoidTypeReturn: the dry-mode carrier is the value at a void-typed position
			return this.carrier(rec.seq, false);
		}
		this.record({
			kind: FILE_WRITE,
			verb: "write",
			detail,
			opts,
			grant: declared,
			bytes,
			recorded: false,
		});
		writeFileSync(
			this.settled(rtPath, "write", "path"),
			data as Uint8Array<ArrayBuffer>,
		);
	}

	mkdir(path: string | Completed | Response, opts: CommonOpts = {}): void {
		// The dry-mode carrier flows out through pathEffect: a void result must
		// still be a CARRIER so a later forward is rejected as unsettled.
		// biome-ignore lint/correctness/noVoidTypeReturn: the dry-mode carrier is the value at a void-typed position
		return this.pathEffect("mkdir", path, opts, (p) =>
			mkdirSync(p, { recursive: true }),
		);
	}

	remove(path: string | Completed | Response, opts: CommonOpts = {}): void {
		// biome-ignore lint/correctness/noVoidTypeReturn: the dry-mode carrier is the value at a void-typed position
		return this.pathEffect("remove", path, opts, (p) =>
			rmSync(p, { recursive: true, force: true }),
		);
	}

	rename(
		src: string | Completed | Response,
		dst: string | Completed | Response,
		opts: CommonOpts = {},
	): void {
		rejectUnacceptedOptions(this.cmdPath, "rename", opts, PATH_OPTION_KEYS);
		this.rejectCarrierParams("rename", {
			resource: opts.resource,
			skipIfCurrent: opts.skipIfCurrent,
			grant: opts.grant,
		});
		const [rtSrc, rSrc] = this.operand(src, "rename", "src");
		const [rtDst, rDst] = this.operand(dst, "rename", "dst");
		const detail = `${rSrc} -> ${rDst}`;
		const declared = this.authorize("rename", FILE_WRITE, opts.grant);

		if (this.dryRun) {
			const rec = this.record({
				kind: FILE_WRITE,
				verb: "rename",
				detail,
				opts,
				grant: declared,
				recorded: true,
			});
			// The declared return type is `void`, but dry mode must hand back the
			// carrier so a later forward of a VOID result is rejected as such
			// (errEffectParamRejectsCarrier) instead of as a non-string. carrier()
			// is typed `never` precisely so the settled-only surface holds.
			// biome-ignore lint/correctness/noVoidTypeReturn: the dry-mode carrier is the value at a void-typed position
			return this.carrier(rec.seq, false);
		}
		this.record({
			kind: FILE_WRITE,
			verb: "rename",
			detail,
			opts,
			grant: declared,
			recorded: false,
		});
		renameSync(
			this.settled(rtSrc, "rename", "src"),
			this.settled(rtDst, "rename", "dst"),
		);
	}

	chmod(
		path: string | Completed | Response,
		mode: number,
		opts: CommonOpts = {},
	): void {
		rejectUnacceptedOptions(this.cmdPath, "chmod", opts, PATH_OPTION_KEYS);
		this.rejectCarrierParams("chmod", {
			mode,
			resource: opts.resource,
			skipIfCurrent: opts.skipIfCurrent,
			grant: opts.grant,
		});
		if (typeof mode !== "number" || !Number.isInteger(mode)) {
			throw new Error(errEffectModeNotInt(this.cmdPath, effectTypeName(mode)));
		}
		const [rtPath, rPath] = this.operand(path, "chmod", "path");
		const detail = `${rPath} 0${mode.toString(8)}`;
		const declared = this.authorize("chmod", FILE_WRITE, opts.grant);

		if (this.dryRun) {
			const rec = this.record({
				kind: FILE_WRITE,
				verb: "chmod",
				detail,
				opts,
				grant: declared,
				recorded: true,
			});
			// The declared return type is `void`, but dry mode must hand back the
			// carrier so a later forward of a VOID result is rejected as such
			// (errEffectParamRejectsCarrier) instead of as a non-string. carrier()
			// is typed `never` precisely so the settled-only surface holds.
			// biome-ignore lint/correctness/noVoidTypeReturn: the dry-mode carrier is the value at a void-typed position
			return this.carrier(rec.seq, false);
		}
		this.record({
			kind: FILE_WRITE,
			verb: "chmod",
			detail,
			opts,
			grant: declared,
			recorded: false,
		});
		chmodSync(this.settled(rtPath, "chmod", "path"), mode);
	}

	http(
		method: string,
		url: string | Completed | Response,
		opts: {
			readonly body?: Uint8Array;
			readonly headers?: Readonly<Record<string, string>>;
			readonly check?: boolean;
			readonly resource?: string;
			readonly skipIfCurrent?: string;
			readonly grant?: string;
		} = {},
	): Response {
		rejectUnacceptedOptions(this.cmdPath, "http", opts, HTTP_OPTION_KEYS);
		this.rejectCarrierParams("http", {
			method,
			body: opts.body,
			headers: opts.headers,
			check: opts.check,
			resource: opts.resource,
			skipIfCurrent: opts.skipIfCurrent,
			grant: opts.grant,
		});
		if (typeof method !== "string") {
			throw new Error(
				errEffectHTTPMethodNotString(this.cmdPath, effectTypeName(method)),
			);
		}
		const [rtUrl, rUrl] = this.operand(url, "http", "url");
		const detail = `${method} ${rUrl}`;
		const declared = this.authorize("http", NET_MUTATE, opts.grant);

		if (this.dryRun) {
			const rec = this.record({
				kind: NET_MUTATE,
				verb: "net",
				detail,
				opts,
				grant: declared,
				recorded: true,
			});
			return this.carrier(rec.seq, true);
		}
		this.record({
			kind: NET_MUTATE,
			verb: "net",
			detail,
			opts,
			grant: declared,
			recorded: false,
		});
		return this.execHttp(method, this.settled(rtUrl, "http", "url"), opts);
	}

	// -- shared execution paths ------------------------------------------

	private pathEffect(
		verb: "mkdir" | "remove",
		path: unknown,
		opts: CommonOpts,
		perform: (p: string) => void,
	): void {
		rejectUnacceptedOptions(this.cmdPath, verb, opts, PATH_OPTION_KEYS);
		this.rejectCarrierParams(verb, {
			resource: opts.resource,
			skipIfCurrent: opts.skipIfCurrent,
			grant: opts.grant,
		});
		const [rtPath, rPath] = this.operand(path, verb, "path");
		const declared = this.authorize(verb, FILE_WRITE, opts.grant);

		if (this.dryRun) {
			const rec = this.record({
				kind: FILE_WRITE,
				verb,
				detail: rPath,
				opts,
				grant: declared,
				recorded: true,
			});
			// The declared return type is `void`, but dry mode must hand back the
			// carrier so a later forward of a VOID result is rejected as such
			// (errEffectParamRejectsCarrier) instead of as a non-string. carrier()
			// is typed `never` precisely so the settled-only surface holds.
			// biome-ignore lint/correctness/noVoidTypeReturn: the dry-mode carrier is the value at a void-typed position
			return this.carrier(rec.seq, false);
		}
		this.record({
			kind: FILE_WRITE,
			verb,
			detail: rPath,
			opts,
			grant: declared,
			recorded: false,
		});
		perform(this.settled(rtPath, verb, "path"));
	}

	private execRun(
		runtime: readonly (string | null)[],
		joined: string,
		opts: {
			readonly cwd?: string;
			readonly env?: Readonly<Record<string, string>>;
			readonly check?: boolean;
			readonly stream?: boolean;
		},
		method: string,
	): Completed {
		const argv = this.settledArgv(runtime, method);
		const stream = opts.stream === true;
		// A streamed child in machine mode writes into the `output` member
		// instead of beside the --json document (§19.11).
		const capture = stream ? this.stdoutCapture() : undefined;
		const res = spawnSync(argv[0] as string, argv.slice(1), {
			cwd: opts.cwd,
			env: traceChildEnv(this.mergedEnv(opts.env), this.trace),
			stdio: !stream
				? "pipe"
				: capture !== undefined
					? ["inherit", "pipe", "inherit"]
					: "inherit",
			maxBuffer: 256 * 1024 * 1024,
		});
		if (
			capture !== undefined &&
			res.stdout !== null &&
			res.stdout !== undefined
		) {
			capture(res.stdout);
		}
		if (res.error !== undefined && res.error !== null) {
			throw res.error;
		}
		const code = res.status === null ? 1 : res.status;
		const out = stream
			? ""
			: decodeEffectOutput(res.stdout, this.cmdPath, method);
		const err = stream
			? ""
			: decodeEffectOutput(res.stderr, this.cmdPath, method);
		if (opts.check !== false && code !== 0) {
			throw new EffectFailed(
				errEffectRunFailed(this.cmdPath, method, joined, code),
			);
		}
		return new CompletedResult(code, out, err);
	}

	private execHttp(
		method: string,
		url: string,
		opts: {
			readonly body?: Uint8Array;
			readonly headers?: Readonly<Record<string, string>>;
			readonly check?: boolean;
		},
	): Response {
		const res = httpSync(method, url, opts.body, opts.headers);
		if (opts.check !== false && (res.status < 200 || res.status > 299)) {
			throw new EffectFailed(
				errEffectHTTPFailed(this.cmdPath, method, url, res.status),
			);
		}
		return new ResponseResult(res.status, res.body, res.headers);
	}
}

/**
 * Decodes a captured child's stdout for the `output` member (contract §19.2's
 * box): UTF-8, each maximal invalid subsequence replaced by one U+FFFD -- the
 * WHATWG decoder's rule, which is TextDecoder's default.
 */
function decodeChildStdout(bytes: Uint8Array): string {
	return new TextDecoder("utf-8").decode(bytes);
}
