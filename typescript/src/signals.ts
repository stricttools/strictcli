/**
 * Framework-owned signal handling on the CLI dispatch path (effects contract
 * §19.13). While the handler runs under `app.run()`, SIGINT and SIGTERM are
 * caught: the first one aborts the handler's `ctx.signal` and does nothing
 * else, and both listeners are removed at once, so a second signal of either
 * kind gets Node's default action and a handler that never returns can still be
 * stopped. When the handler returns, the exit step ends the command with
 * 128 + the signal's number and the diagnostic naming it.
 *
 * `app.test()`, `app.call()`, and the MCP server install nothing: an
 * in-process caller owns its own process's signals.
 */

import { constants } from "node:os";
import { errCanceledBySignal } from "./errors.js";

/** The two signals the framework handles. */
export type HandledSignal = "SIGINT" | "SIGTERM";

const HANDLED: readonly HandledSignal[] = ["SIGINT", "SIGTERM"];

/** One installed pair of listeners, for one handler's span. */
export class SignalWatch {
	private received: HandledSignal | null = null;
	private readonly listeners = new Map<HandledSignal, () => void>();
	private released = false;

	private constructor(private readonly controller: AbortController) {}

	/** Installs the listeners; the first signal aborts `controller`. */
	static install(controller: AbortController): SignalWatch {
		const watch = new SignalWatch(controller);
		for (const sig of HANDLED) {
			const listener = (): void => watch.onSignal(sig);
			watch.listeners.set(sig, listener);
			process.on(sig, listener);
		}
		return watch;
	}

	/** Removes the listeners, restoring both signals' default action. Idempotent. */
	release(): void {
		if (this.released) {
			return;
		}
		this.released = true;
		for (const [sig, listener] of this.listeners) {
			process.removeListener(sig, listener);
		}
	}

	/** The exit status and diagnostic a received signal ends the command with. */
	ending(): { readonly status: number; readonly message: string } | null {
		if (this.received === null) {
			return null;
		}
		return {
			status: 128 + constants.signals[this.received],
			message: errCanceledBySignal(this.received),
		};
	}

	private onSignal(sig: HandledSignal): void {
		if (this.received !== null) {
			return;
		}
		this.received = sig;
		this.release();
		this.controller.abort(new Error(errCanceledBySignal(sig)));
	}
}
