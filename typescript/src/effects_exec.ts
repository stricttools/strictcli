/**
 * Synchronous execution primitives for the two effect methods Node cannot
 * perform synchronously on the main thread: `spawn` (a concurrent child whose
 * exit status must be readable later, synchronously, through `Spawned.wait()`)
 * and `http`.
 *
 * The effects API is synchronous by contract (§2.5.6 pins
 * `ctx.effects.run(argv, opts?) => Completed`, not a Promise), because a
 * forwarded carrier has to arrive at a later effect's parameter as a value,
 * not as a pending promise. `run` gets that for free from spawnSync; these two
 * do not, and each uses the smallest mechanism that gives it:
 *
 * - `spawn`: a worker thread starts the child and signals the main thread
 *   through a SharedArrayBuffer flag (pid on start, exit code on close). The
 *   main thread blocks with Atomics.wait, which is permitted on Node's main
 *   thread, and drains the payload with receiveMessageOnPort.
 * - `http`: a short-lived child `node` process performs the fetch and writes a
 *   JSON envelope to stdout. No worker machinery is needed for a single
 *   request/response round trip, and a child process cannot leak a live handle
 *   back into the parent's event loop.
 *
 * Neither mechanism degrades: a failure to start is thrown, never swallowed.
 */

import { spawnSync } from "node:child_process";
import {
	MessageChannel,
	type MessagePort,
	receiveMessageOnPort,
	Worker,
} from "node:worker_threads";

/** A started child whose exit status can be awaited synchronously later. */
export interface SpawnedChild {
	readonly pid: number;
	/**
	 * Blocks until the child exits and returns its exit code. When the child
	 * was started with its stdout captured, every captured byte has been handed
	 * to `onStdout` by the time the code is returned.
	 */
	wait(): number;
	/** Whether the child has exited, without waiting. */
	hasExited(): boolean;
	/** Blocks up to `ms` milliseconds for the child to exit; reports whether it did. */
	waitFor(ms: number): boolean;
	/** Sends `signal` unless the child has already exited. */
	signal(signal: NodeJS.Signals | number): void;
	/**
	 * Hands every captured chunk that has arrived so far to `onStdout`, in
	 * arrival order, without waiting (contract §19.11's box).
	 */
	pump(): void;
}

/**
 * Worker body: starts the child, forwards each captured stdout chunk the
 * moment it arrives, then signals pid and exit code in turn. The exit is
 * signaled on "close", after the last chunk was forwarded.
 */
const SPAWN_WORKER_SOURCE = `
const { workerData } = require("node:worker_threads");
const { spawn } = require("node:child_process");
const { argv, cwd, env, captureStdout, pidBuf, doneBuf, pidPort, donePort, chunkPort } = workerData;
const pidFlag = new Int32Array(pidBuf);
const doneFlag = new Int32Array(doneBuf);
function signal(flag, port, msg) {
	port.postMessage(msg);
	Atomics.store(flag, 0, 1);
	Atomics.notify(flag, 0);
}
try {
	const child = spawn(argv[0], argv.slice(1), {
		cwd: cwd === null ? undefined : cwd,
		env: env === null ? process.env : env,
		stdio: captureStdout ? ["inherit", "pipe", "inherit"] : "inherit",
	});
	if (captureStdout) {
		child.stdout.on("data", (chunk) => chunkPort.postMessage(new Uint8Array(chunk)));
	}
	let started = false;
	child.on("spawn", () => {
		started = true;
		signal(pidFlag, pidPort, { pid: child.pid });
	});
	child.on("error", (e) => {
		const message = String((e && e.message) || e);
		if (!started) {
			started = true;
			signal(pidFlag, pidPort, { error: message });
		}
		signal(doneFlag, donePort, { error: message });
	});
	child.on("close", (code) => {
		signal(doneFlag, donePort, { code: code === null ? 1 : code });
	});
} catch (e) {
	const message = String((e && e.message) || e);
	signal(pidFlag, pidPort, { error: message });
	signal(doneFlag, donePort, { error: message });
}
`;

/** Blocks the calling thread until `flag` is set, then drains one message. */
function blockingReceive(flag: Int32Array, port: MessagePort): unknown {
	while (Atomics.load(flag, 0) === 0) {
		Atomics.wait(flag, 0, 0);
	}
	const envelope = receiveMessageOnPort(port);
	return envelope?.message;
}

/**
 * Starts a child concurrently and returns a handle whose `wait()` blocks the
 * main thread synchronously. Throws if the child cannot be started.
 *
 * With `onStdout`, the child's stdout is read through a pipe instead of
 * inheriting the process stdout, and every chunk it writes is handed to
 * `onStdout` in arrival order: by `pump()`, and at the latest when the child
 * is waited on (contract §19.11). Its stderr is inherited either way.
 */
export function spawnConcurrent(
	argv: readonly string[],
	cwd: string | undefined,
	env: Readonly<Record<string, string>> | undefined,
	onStdout?: (bytes: Uint8Array) => void,
): SpawnedChild {
	const pidBuf = new SharedArrayBuffer(4);
	const doneBuf = new SharedArrayBuffer(4);
	const pidChannel = new MessageChannel();
	const doneChannel = new MessageChannel();
	const chunkChannel = new MessageChannel();
	const worker = new Worker(SPAWN_WORKER_SOURCE, {
		eval: true,
		// The body is a CommonJS script. A worker inherits the process's
		// execArgv by default, and under `node --input-type=module` that would
		// evaluate it as a module where `require` does not exist: the worker
		// would die before signaling, and the main thread would wait forever.
		execArgv: [],
		workerData: {
			argv: [...argv],
			cwd: cwd ?? null,
			env: env === undefined ? null : { ...env },
			captureStdout: onStdout !== undefined,
			pidBuf,
			doneBuf,
			pidPort: pidChannel.port2,
			donePort: doneChannel.port2,
			chunkPort: chunkChannel.port2,
		},
		transferList: [pidChannel.port2, doneChannel.port2, chunkChannel.port2],
	});
	// The worker must not hold the process open once the child is done.
	worker.unref();

	const started = blockingReceive(new Int32Array(pidBuf), pidChannel.port1) as
		| { pid?: number; error?: string }
		| undefined;
	if (started === undefined || started.error !== undefined) {
		void worker.terminate();
		throw new Error(
			started?.error ?? "spawn failed: worker produced no result",
		);
	}
	const pid = started.pid as number;
	const doneFlag = new Int32Array(doneBuf);

	const pump = (): void => {
		if (onStdout === undefined) {
			return;
		}
		for (;;) {
			const chunk = receiveMessageOnPort(chunkChannel.port1);
			if (chunk === undefined) {
				return;
			}
			onStdout(chunk.message as Uint8Array);
		}
	};

	let exitCode: number | undefined;
	return {
		pid,
		wait(): number {
			if (exitCode !== undefined) {
				return exitCode;
			}
			const done = blockingReceive(doneFlag, doneChannel.port1) as
				| { code?: number; error?: string }
				| undefined;
			// Every chunk was forwarded before the exit was signaled.
			pump();
			void worker.terminate();
			if (done === undefined || done.error !== undefined) {
				throw new Error(
					done?.error ?? "spawn wait failed: worker produced no result",
				);
			}
			exitCode = done.code as number;
			return exitCode;
		},
		hasExited(): boolean {
			return Atomics.load(doneFlag, 0) !== 0;
		},
		waitFor(ms: number): boolean {
			if (Atomics.load(doneFlag, 0) === 0) {
				Atomics.wait(doneFlag, 0, 0, ms);
			}
			return Atomics.load(doneFlag, 0) !== 0;
		},
		signal(signal: NodeJS.Signals | number): void {
			if (Atomics.load(doneFlag, 0) !== 0) {
				return;
			}
			try {
				process.kill(pid, signal);
			} catch (e) {
				// The child exited between the check and the signal.
				if ((e as NodeJS.ErrnoException).code !== "ESRCH") {
					throw e;
				}
			}
		},
		pump,
	};
}

/** The wire shape the fetch child writes to stdout. */
interface HttpChildResult {
	readonly status?: number;
	readonly headers?: Record<string, string>;
	readonly bodyB64?: string;
	readonly error?: string;
}

/** Child body: reads the request envelope from stdin, prints the response. */
const HTTP_CHILD_SOURCE = `
const req = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const init = { method: req.method, headers: req.headers || undefined };
if (req.bodyB64 !== null) { init.body = Buffer.from(req.bodyB64, "base64"); }
fetch(req.url, init).then(async (res) => {
	const headers = {};
	res.headers.forEach((v, k) => { headers[k.toLowerCase()] = v; });
	const buf = Buffer.from(await res.arrayBuffer());
	process.stdout.write(JSON.stringify({
		status: res.status, headers, bodyB64: buf.toString("base64"),
	}));
}).catch((e) => {
	process.stdout.write(JSON.stringify({ error: String((e && e.message) || e) }));
});
`;

/** A performed HTTP request's raw result. */
export interface HttpResult {
	readonly status: number;
	readonly body: Uint8Array;
	readonly headers: Record<string, string>;
}

/** Performs an HTTP request synchronously. Throws when the request fails. */
export function httpSync(
	method: string,
	url: string,
	body: Uint8Array | undefined,
	headers: Readonly<Record<string, string>> | undefined,
): HttpResult {
	const request = {
		method,
		url,
		headers: headers === undefined ? null : { ...headers },
		bodyB64: body === undefined ? null : Buffer.from(body).toString("base64"),
	};
	const child = spawnSync(process.execPath, ["-e", HTTP_CHILD_SOURCE], {
		input: JSON.stringify(request),
		maxBuffer: 256 * 1024 * 1024,
	});
	if (child.error !== undefined && child.error !== null) {
		throw child.error;
	}
	const raw = child.stdout?.toString("utf8") ?? "";
	let parsed: HttpChildResult;
	try {
		parsed = JSON.parse(raw) as HttpChildResult;
	} catch {
		const detail = child.stderr?.toString("utf8").trim() ?? "";
		throw new Error(
			`http request failed: no response envelope${detail !== "" ? `: ${detail}` : ""}`,
		);
	}
	if (parsed.error !== undefined) {
		throw new Error(parsed.error);
	}
	return {
		status: parsed.status as number,
		body: new Uint8Array(Buffer.from(parsed.bodyB64 as string, "base64")),
		headers: parsed.headers as Record<string, string>,
	};
}
