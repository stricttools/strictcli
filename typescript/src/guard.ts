/**
 * The runtime guard (effects contract §19.12): in machine mode, while the
 * handler runs, `process.stdout.write` is replaced so that nothing reaches the
 * process stdout except through the framework, and `process.exit` is replaced
 * by a function that unwinds the handler instead of terminating the process.
 *
 * The framework's own writes never pass through the replacement: the dispatch
 * writes the `--json` document, the document writer's bytes, and `ctx.out`'s
 * human text through a writer bound to the ORIGINAL `write` it saved before
 * the handler started (see `realStdoutWriter`).
 *
 * Attribution. Under `app.run()` every write that reaches the replacement is
 * counted: the handler's span is the only code the process is running for this
 * command. Under `app.test()` the process is somebody else's -- a test runner
 * that multiplexes its own reporter protocol over process.stdout, and whose
 * frames flow through the stream machinery while an async handler awaits -- so
 * only writes made from the handler's own asynchronous context are counted, and
 * every other write goes to the original `write` unchanged. The handler's
 * context is established with AsyncLocalStorage, which follows promises,
 * timers, and event callbacks the handler starts.
 */

import { AsyncLocalStorage } from "node:async_hooks";
import {
	errProcessExitOutsideFramework,
	errStdoutWrittenOutsideFramework,
} from "./errors.js";
import { ProcessExitCalled } from "./exits.js";
import { jsonCompact } from "./outcome.js";

/** How many of the stray bytes the failure diagnostic quotes. */
const EXCERPT_BYTES = 4096;

/** Which writes a guard counts: every write, or only the handler's own. */
export type GuardAttribution = "process" | "handler";

type WriteFn = typeof process.stdout.write;
type ExitFn = typeof process.exit;

/** The active guards' handler contexts, keyed by the guard itself. */
const handlerContext = new AsyncLocalStorage<RuntimeGuard>();

/** A writer onto the process stdout that bypasses any later replacement. */
export interface RealStdoutWriter {
	write(text: string): void;
	writeBytes(chunk: Uint8Array): void;
}

/**
 * A writer bound to the `write` process.stdout carries NOW, so the framework's
 * own writes keep reaching the real stdout while a guard has replaced it.
 */
export function realStdoutWriter(): RealStdoutWriter {
	const stream = process.stdout;
	const write = stream.write;
	return {
		write: (text: string) => {
			write.call(stream, text);
		},
		writeBytes: (chunk: Uint8Array) => {
			write.call(stream, chunk);
		},
	};
}

/** One armed guard: the stdout replacement and the process-exit trap. */
export class RuntimeGuard {
	private readonly attribution: GuardAttribution;
	private readonly savedWrite: WriteFn;
	private readonly savedExit: ExitFn;
	private released = false;
	private total = 0;
	private head: Buffer[] = [];
	private headBytes = 0;

	private constructor(attribution: GuardAttribution) {
		this.attribution = attribution;
		this.savedWrite = process.stdout.write;
		this.savedExit = process.exit;
	}

	/** Arms the guard: replaces process.stdout.write and process.exit. */
	static install(attribution: GuardAttribution): RuntimeGuard {
		const guard = new RuntimeGuard(attribution);
		const stream = process.stdout;
		const replacedWrite = function (
			this: unknown,
			chunk: unknown,
			encoding?: unknown,
			cb?: unknown,
		): boolean {
			if (!guard.counts()) {
				return (guard.savedWrite as (...a: unknown[]) => boolean).apply(
					stream,
					[chunk, encoding, cb].filter((a) => a !== undefined),
				);
			}
			guard.take(chunk, typeof encoding === "string" ? encoding : undefined);
			const done = typeof encoding === "function" ? encoding : cb;
			if (typeof done === "function") {
				process.nextTick(done as () => void);
			}
			return true;
		};
		const replacedExit = (code?: unknown): never => {
			if (!guard.counts()) {
				return guard.savedExit.call(process, code as Parameters<ExitFn>[0]);
			}
			throw new ProcessExitCalled(code);
		};
		stream.write = replacedWrite as WriteFn;
		process.exit = replacedExit as ExitFn;
		return guard;
	}

	/** Runs the handler inside this guard's asynchronous context. */
	within<T>(fn: () => T): T {
		return handlerContext.run(this, fn);
	}

	/** Restores process.stdout.write and process.exit. Idempotent. */
	release(): void {
		if (this.released) {
			return;
		}
		this.released = true;
		process.stdout.write = this.savedWrite;
		process.exit = this.savedExit;
	}

	/**
	 * The failure diagnostic when any byte reached the replaced stdout, else
	 * null (contract §19.12): the total count and a JSON string literal of the
	 * first 4096 bytes, decoded with the WHATWG replacement rule.
	 */
	failure(): string | null {
		if (this.total === 0) {
			return null;
		}
		const bytes = Buffer.concat(this.head).subarray(0, EXCERPT_BYTES);
		const text = new TextDecoder("utf-8").decode(bytes);
		return errStdoutWrittenOutsideFramework(this.total, jsonCompact(text));
	}

	/** True until the guard is released. */
	get active(): boolean {
		return !this.released;
	}

	/** Counts a string as stray stdout bytes. */
	takeText(text: string): void {
		this.take(text, undefined);
	}

	private counts(): boolean {
		if (this.released) {
			return false;
		}
		return this.attribution === "process" || handlerContext.getStore() === this;
	}

	private take(chunk: unknown, encoding: string | undefined): void {
		let buf: Buffer;
		if (typeof chunk === "string") {
			buf = Buffer.from(chunk, (encoding ?? "utf8") as BufferEncoding);
		} else if (chunk instanceof Uint8Array) {
			buf = Buffer.from(chunk);
		} else {
			buf = Buffer.from(String(chunk), "utf8");
		}
		this.total += buf.length;
		if (this.headBytes < EXCERPT_BYTES) {
			this.head.push(buf);
			this.headBytes += buf.length;
		}
	}
}

/**
 * Counts `text` as a stray stdout write when it is made from inside a guarded
 * handler's asynchronous context, reporting whether it did. `app.test()`
 * reroutes `console.log` into its capture buffer; this is how that reroute
 * stays inside the guard instead of placing bytes beside the `--json` document.
 */
export function takeIfGuarded(text: string): boolean {
	const guard = handlerContext.getStore();
	if (guard === undefined || !guard.active) {
		return false;
	}
	guard.takeText(text);
	return true;
}

/** The diagnostic a trapped process exit ends the command with (§19.12). */
export function processExitDiagnostic(called: ProcessExitCalled): string {
	return errProcessExitOutsideFramework(called.named);
}
