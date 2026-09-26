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
	 * was started with its stdout captured, `onStdout` receives every captured
	 * byte exactly once, on the first call, before the code is returned.
	 */
	wait(): number;
}

/** Worker body: starts the child, then signals pid and exit code in turn. */
const SPAWN_WORKER_SOURCE = `
const { workerData } = require("node:worker_threads");
const { spawn } = require("node:child_process");
const { argv, cwd, env, captureStdout, pidBuf, doneBuf, pidPort, donePort } = workerData;
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
	const captured = [];
	if (captureStdout) {
		child.stdout.on("data", (chunk) => captured.push(chunk));
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
		signal(doneFlag, donePort, {
			code: code === null ? 1 : code,
			stdout: captureStdout ? new Uint8Array(Buffer.concat(captured)) : null,
		});
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
 * inheriting the process stdout, and every byte it wrote is handed to
 * `onStdout` when the child is waited on (contract §19.11). Its stderr is
 * inherited either way.
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
	const worker = new Worker(SPAWN_WORKER_SOURCE, {
		eval: true,
		workerData: {
			argv: [...argv],
			cwd: cwd ?? null,
			env: env === undefined ? null : { ...env },
			captureStdout: onStdout !== undefined,
			pidBuf,
			doneBuf,
			pidPort: pidChannel.port2,
			donePort: doneChannel.port2,
		},
		transferList: [pidChannel.port2, doneChannel.port2],
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

	let exitCode: number | undefined;
	return {
		pid,
		wait(): number {
			if (exitCode !== undefined) {
				return exitCode;
			}
			const done = blockingReceive(
				new Int32Array(doneBuf),
				doneChannel.port1,
			) as
				| { code?: number; error?: string; stdout?: Uint8Array | null }
				| undefined;
			void worker.terminate();
			if (done === undefined || done.error !== undefined) {
				throw new Error(
					done?.error ?? "spawn wait failed: worker produced no result",
				);
			}
			exitCode = done.code as number;
			if (
				onStdout !== undefined &&
				done.stdout !== undefined &&
				done.stdout !== null
			) {
				onStdout(done.stdout);
			}
			return exitCode;
		},
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
