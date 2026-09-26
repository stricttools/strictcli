/**
 * The framework-owned ways a command ends early (effects contract §19.9,
 * §19.12): the early exit a handler throws, the error the in-process door
 * rejects with when a command ended that way, and the private value the
 * process-exit trap throws in place of terminating the process.
 */

import { errExitNowCode, errExitNowMessageEmpty } from "./errors.js";

/**
 * Ends the command early, from anywhere in the handler's call stack, through
 * the one exit step (contract §19.9):
 *
 *     throw new ExitNow(3, "no manifest at ./m.toml");
 *
 * The handler's frames unwind (`finally` blocks run), then the exit step ends
 * the command with `code`: the message is printed as `error: <message>` in
 * human mode and is the last `error` diagnostic of the `--json` document in
 * machine mode, and a dry run still renders its would-do log.
 *
 * `code` must be an integer from 1 to 255 and `message` a non-empty string.
 * Either refusal throws from this constructor, before anything unwinds, so a
 * refused early exit is a programming error and never an early exit with a
 * substituted code. Thrown anywhere other than a command or passthrough
 * handler -- a `validate` callback, a check, code outside a dispatch -- it is
 * not recognized and unwinds as any other error does.
 */
export class ExitNow extends Error {
	/** The exit status the command ends with. */
	readonly code: number;

	constructor(code: number, message: string) {
		if (!Number.isInteger(code) || code < 1 || code > 255) {
			throw new Error(errExitNowCode(code));
		}
		if (typeof message !== "string" || message === "") {
			throw new Error(errExitNowMessageEmpty());
		}
		super(message);
		this.name = "ExitNow";
		this.code = code;
	}
}

/**
 * What `app.call()` rejects with when the command it ran ended through an
 * early exit (contract §19.9). It is not an `InvokeError`: an invocation error
 * means the call was refused before the handler ran, and an early exit means
 * the handler ran and ended with a failure. Its `message` is the handler's
 * message alone.
 */
export class ExitError extends Error {
	/** The exit status the handler's early exit named. */
	readonly code: number;

	constructor(code: number, message: string) {
		super(message);
		this.name = "ExitError";
		this.code = code;
	}
}

/**
 * Package-internal (NOT re-exported from index.ts): what the process-exit trap
 * throws in place of terminating the process (contract §19.12). It is not an
 * Error, so a handler's `catch (e) { if (e instanceof Error) ... }` does not
 * mistake it for a failure it could report.
 */
export class ProcessExitCalled {
	/** The code the handler passed, as it passed it. */
	readonly requested: unknown;

	constructor(requested: unknown) {
		this.requested = requested;
	}

	/** The code as the diagnostic names it: the requested code, 0 when absent. */
	get named(): string {
		const r = this.requested;
		return r === undefined || r === null ? "0" : String(r);
	}

	/** The status the command ends with: the requested code, or 1 for 0 or absent. */
	get status(): number {
		const n =
			typeof this.requested === "string"
				? Number.parseInt(this.requested, 10)
				: typeof this.requested === "number"
					? this.requested
					: 0;
		return Number.isInteger(n) && n !== 0 ? n : 1;
	}
}
