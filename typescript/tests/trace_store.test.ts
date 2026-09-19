/**
 * The process trace store (docs/process-trace-store.md).
 *
 * Every test here pins HOME to a temp directory, which is what makes the suite
 * hermetic: the store path is the literal `~/.local/share/strictcli/trace/`,
 * expanded from HOME and nothing else, so a poisoned HOME is a private store.
 *
 * The reader used below is a TEST-side reader on purpose. The framework exposes
 * no accessor for ancestry (effects contract §20.2) and no code path branches
 * on the store's content -- a consumer that wants the chain parses the
 * environment variable and reads the store itself, exactly as this file does.
 *
 * Byte-level expectations mirror the Python reference implementation
 * (python/strictcli/__init__.py and python/tests/test_trace_store.py).
 */

import { strict as assert } from "node:assert";
import { execFileSync } from "node:child_process";
import {
	appendFileSync,
	chmodSync,
	existsSync,
	mkdirSync,
	readdirSync,
	readFileSync,
	rmSync,
	statSync,
	writeFileSync,
} from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import {
	type App,
	type AppSpec,
	createApp,
	defineMutatingCommand,
	defineReadOnlyCommand,
	type MutatingContext,
	type ReadOnlyContext,
} from "../src/index.js";
import {
	TRACE_PARENT_ENV,
	TRACE_PARTITION_RE,
	type TraceIdentity,
	traceChildEnv,
	traceLabel,
	traceLabelStartMs,
	traceStoreDir,
	traceTimestamp,
	traceWriteEntry,
	ulidMint,
	ulidTimestamp,
	ulidValid,
} from "../src/trace.js";
import { tempDir } from "./helpers.js";

const ENTRY_KEYS = [
	"id",
	"parent_id",
	"app",
	"version",
	"command",
	"dry_run",
	"machine_mode",
	"quiet",
	"verbose",
	"approve_consequential",
	"effect",
	"pid",
	"spawned_at",
];

const MS_PER_HOUR = 3600000;
const ROLL_BYTES = 8 * 1024 * 1024;

/**
 * Pins HOME to a fresh temp directory and clears any inherited ancestry, then
 * restores both. The store this test writes to is therefore its own.
 */
interface HomeState {
	readonly home: string;
	readonly priorHome: string | undefined;
	readonly priorParent: string | undefined;
}

function enterHome(): HomeState {
	const home = tempDir("sc-trace-");
	const state: HomeState = {
		home,
		priorHome: process.env.HOME,
		priorParent: process.env[TRACE_PARENT_ENV],
	};
	process.env.HOME = home;
	delete process.env[TRACE_PARENT_ENV];
	return state;
}

function leaveHome(state: HomeState): void {
	const dir = storeDir(state.home);
	if (existsSync(dir)) {
		chmodSync(dir, 0o700);
	}
	if (state.priorHome === undefined) {
		delete process.env.HOME;
	} else {
		process.env.HOME = state.priorHome;
	}
	if (state.priorParent === undefined) {
		delete process.env[TRACE_PARENT_ENV];
	} else {
		process.env[TRACE_PARENT_ENV] = state.priorParent;
	}
	rmSync(state.home, { recursive: true, force: true });
}

function withHome<T>(body: (home: string) => T): T {
	const state = enterHome();
	try {
		return body(state.home);
	} finally {
		leaveHome(state);
	}
}

/** The awaiting form: a synchronous finally would tear the store down mid-run. */
async function withHomeAsync(
	body: (home: string) => Promise<void>,
): Promise<void> {
	const state = enterHome();
	try {
		await body(state.home);
	} finally {
		leaveHome(state);
	}
}

function storeDir(home: string): string {
	return join(home, ".local", "share", "strictcli", "trace");
}

/**
 * Partition files, in name order. Anything else in the directory -- the
 * failure marker included -- is ignored, per the spec's reader rule.
 */
function partitions(home: string): string[] {
	const dir = storeDir(home);
	if (!existsSync(dir)) {
		return [];
	}
	return readdirSync(dir)
		.filter((name) => TRACE_PARTITION_RE.test(name))
		.sort()
		.map((name) => join(dir, name));
}

interface ReadResult {
	readonly entries: Record<string, unknown>[];
	readonly anomalies: string[];
}

/**
 * One partition's entries and anomalies. A torn or malformed line is recorded
 * verbatim as an anomaly and skipped -- never discarded silently.
 */
function entriesIn(path: string): ReadResult {
	const entries: Record<string, unknown>[] = [];
	const anomalies: string[] = [];
	for (const line of readFileSync(path, "utf8").split("\n")) {
		if (line === "") {
			continue;
		}
		let obj: unknown;
		try {
			obj = JSON.parse(line);
		} catch {
			anomalies.push(line);
			continue;
		}
		if (
			typeof obj !== "object" ||
			obj === null ||
			Object.keys(obj).length !== ENTRY_KEYS.length ||
			!ENTRY_KEYS.every((k) => Object.hasOwn(obj as object, k))
		) {
			anomalies.push(line);
			continue;
		}
		entries.push(obj as Record<string, unknown>);
	}
	return { entries, anomalies };
}

function readEntries(home: string): ReadResult {
	const entries: Record<string, unknown>[] = [];
	const anomalies: string[] = [];
	for (const path of partitions(home)) {
		const found = entriesIn(path);
		entries.push(...found.entries);
		anomalies.push(...found.anomalies);
	}
	return { entries, anomalies };
}

/**
 * The spec's lookup rule (docs/process-trace-store.md, Partitions): binary-
 * search the sorted labels for the greatest label NOT AFTER the identifier's
 * embedded timestamp, read that partition, and on a miss walk backward through
 * older partitions until the entry is found or the partitions are exhausted.
 * The backward walk is required for correctness: the clamp invariant is
 * one-sided, so a file that has not rolled keeps taking entries after a
 * newer-labelled partition exists.
 *
 * `walkBack: false` is the pre-amendment rule -- one binary search and nothing
 * else -- kept so a test can pin what it misses.
 */
function resolveEntry(
	home: string,
	entryId: string,
	walkBack = true,
): Record<string, unknown> | null {
	const ms = ulidTimestamp(entryId);
	if (ms === null) {
		return null;
	}
	const parts = partitions(home);
	const labels = parts.map((p) =>
		(p.split("/").pop() as string).replace(/\.jsonl$/, ""),
	);
	const target = traceLabel(ms);
	// The greatest label not after the target.
	let lo = 0;
	let hi = labels.length;
	while (lo < hi) {
		const mid = (lo + hi) >> 1;
		if ((labels[mid] as string) <= target) {
			lo = mid + 1;
		} else {
			hi = mid;
		}
	}
	for (let index = lo - 1; index >= 0; index--) {
		for (const entry of entriesIn(parts[index] as string).entries) {
			if (entry.id === entryId) {
				return entry;
			}
		}
		if (!walkBack) {
			return null;
		}
	}
	return null;
}

/**
 * Walks parent_id to the root, resolving each link through the store's own
 * lookup rule; a dangling reference ends the walk.
 */
function flattenAncestry(home: string, leafId: string): string[] {
	const chain: string[] = [];
	let current: string | null = leafId;
	while (current !== null) {
		const entry = resolveEntry(home, current);
		if (entry === null) {
			return chain;
		}
		chain.push(current);
		current = entry.parent_id as string | null;
	}
	return chain;
}

function identity(overrides: Partial<TraceIdentity> = {}): TraceIdentity {
	return {
		app: "app",
		version: "1.2.3",
		command: "build.run",
		dryRun: false,
		machineMode: false,
		quiet: false,
		verbose: false,
		approveConsequential: false,
		effect: "mutating",
		...overrides,
	};
}

function mustWrite(id: TraceIdentity = identity()): string {
	const written = traceWriteEntry(id);
	assert.notEqual(written, null, "traceWriteEntry reported a failure");
	return written as string;
}

/** An app with one mutating command whose handler is the test body. */
function mutApp(
	handler: (ctx: MutatingContext) => number | undefined,
	spec: Partial<AppSpec> = {},
): App {
	const app = createApp({
		name: "app",
		version: "1.2.3",
		help: "h",
		...spec,
	});
	app.command(
		defineMutatingCommand("go", {
			help: "do the thing",
			handler: (_a, ctx) => handler(ctx),
		}),
	);
	return app;
}

function roApp(
	handler: (ctx: ReadOnlyContext) => number | undefined,
	spec: Partial<AppSpec> = {},
): App {
	const app = createApp({
		name: "app",
		version: "1.2.3",
		help: "h",
		...spec,
	});
	app.command(
		defineReadOnlyCommand("go", {
			help: "look at the thing",
			handler: (_a, ctx) => handler(ctx),
		}),
	);
	return app;
}

// --- the store's location and shape ---------------------------------------

test("trace: the store path is the literal one", () => {
	withHome((home) => {
		assert.equal(traceStoreDir(), storeDir(home));
	});
});

test("trace: XDG_DATA_HOME is never consulted", () => {
	withHome((home) => {
		const prior = process.env.XDG_DATA_HOME;
		process.env.XDG_DATA_HOME = join(home, "elsewhere");
		try {
			assert.equal(traceStoreDir(), storeDir(home));
		} finally {
			if (prior === undefined) {
				delete process.env.XDG_DATA_HOME;
			} else {
				process.env.XDG_DATA_HOME = prior;
			}
		}
	});
});

test("trace: the directory is created on write with mode 0700", () => {
	withHome((home) => {
		mustWrite();
		assert.equal(statSync(storeDir(home)).mode & 0o777, 0o700);
	});
});

test("trace: a partition file is created with mode 0600", () => {
	withHome((home) => {
		mustWrite();
		assert.equal(statSync(partitions(home)[0] as string).mode & 0o777, 0o600);
	});
});

test("trace: a deleted store resumes from empty", () => {
	withHome((home) => {
		mustWrite();
		rmSync(storeDir(home), { recursive: true });
		mustWrite();
		assert.equal(readEntries(home).entries.length, 1);
	});
});

// --- the entry ------------------------------------------------------------

test("trace: every key is present with its pinned type", () => {
	withHome((home) => {
		const id = mustWrite(
			identity({
				quiet: true,
				verbose: true,
				machineMode: true,
				approveConsequential: true,
			}),
		);
		const { entries, anomalies } = readEntries(home);
		assert.deepEqual(anomalies, []);
		assert.equal(entries.length, 1);
		const entry = entries[0] as Record<string, unknown>;
		assert.deepEqual(Object.keys(entry), ENTRY_KEYS);
		assert.equal(entry.id, id);
		assert.equal(entry.parent_id, null);
		assert.equal(entry.app, "app");
		assert.equal(entry.version, "1.2.3");
		assert.equal(entry.command, "build.run");
		assert.equal(entry.dry_run, false);
		assert.equal(entry.machine_mode, true);
		assert.equal(entry.quiet, true);
		assert.equal(entry.verbose, true);
		assert.equal(entry.approve_consequential, true);
		assert.equal(entry.effect, "mutating");
		assert.equal(entry.pid, process.pid);
		assert.equal(entry.spawned_at, traceTimestamp(ulidTimestamp(id) as number));
	});
});

test("trace: command may be null", () => {
	withHome((home) => {
		mustWrite(identity({ command: null }));
		assert.equal(readEntries(home).entries[0]?.command, null);
	});
});

test("trace: one compact line per entry, terminated by exactly one newline", () => {
	withHome((home) => {
		mustWrite();
		mustWrite();
		mustWrite();
		const text = readFileSync(partitions(home)[0] as string, "utf8");
		assert.equal(text.split("\n").length - 1, 3);
		assert.ok(text.endsWith("\n"));
		assert.ok(!text.endsWith("\n\n"));
		assert.ok(!text.includes(", "));
		assert.ok(!text.includes('": '));
	});
});

test("trace: ids are distinct per entry", () => {
	withHome(() => {
		const ids = new Set<string>();
		for (let i = 0; i < 20; i++) {
			ids.add(mustWrite());
		}
		assert.equal(ids.size, 20);
	});
});

test("trace: the id is canonical under the strict profile", () => {
	withHome(() => {
		assert.ok(ulidValid(mustWrite()));
	});
});

test("trace: spawned_at renders three fractional digits in UTC", () => {
	assert.equal(traceTimestamp(0), "1970-01-01T00:00:00.000Z");
	assert.equal(traceTimestamp(1), "1970-01-01T00:00:00.001Z");
	assert.equal(traceTimestamp(1786594672913), "2026-08-13T04:17:52.913Z");
});

test("trace: a label is the range start in UTC", () => {
	assert.equal(traceLabel(1786594672913), "2026-08-13T04");
	assert.equal(traceLabel(0), "1970-01-01T00");
	for (const ms of [0, 1786594672913, 1234567890123]) {
		const label = traceLabel(ms);
		const start = traceLabelStartMs(label);
		assert.ok(start <= ms);
		assert.ok(ms - start < MS_PER_HOUR);
		assert.equal(traceLabel(start), label);
	}
});

// --- partitions -----------------------------------------------------------

test("trace: the first write creates the current hour", () => {
	withHome((home) => {
		mustWrite();
		assert.deepEqual(
			partitions(home).map((p) => p.split("/").pop()),
			[`${traceLabel(Date.now())}.jsonl`],
		);
	});
});

test("trace: writers append to the greatest-named file", () => {
	withHome((home) => {
		const dir = storeDir(home);
		mkdirSync(dir, { recursive: true, mode: 0o700 });
		writeFileSync(join(dir, "2020-01-01T00.jsonl"), "");
		writeFileSync(join(dir, "2020-01-01T05.jsonl"), "");
		mustWrite();
		assert.equal(readFileSync(join(dir, "2020-01-01T00.jsonl"), "utf8"), "");
		assert.notEqual(readFileSync(join(dir, "2020-01-01T05.jsonl"), "utf8"), "");
	});
});

test("trace: no roll when the hour advanced but the file is small", () => {
	withHome((home) => {
		const dir = storeDir(home);
		mkdirSync(dir, { recursive: true, mode: 0o700 });
		writeFileSync(join(dir, "2020-01-01T00.jsonl"), "x".repeat(1024));
		mustWrite();
		assert.equal(partitions(home).length, 1);
	});
});

test("trace: no roll when the file is large but the hour has not advanced", () => {
	withHome((home) => {
		const dir = storeDir(home);
		mkdirSync(dir, { recursive: true, mode: 0o700 });
		const label = traceLabel(Date.now());
		writeFileSync(join(dir, `${label}.jsonl`), Buffer.alloc(ROLL_BYTES));
		mustWrite();
		assert.equal(partitions(home).length, 1);
	});
});

test("trace: rolls with O_EXCL when both conditions hold", () => {
	withHome((home) => {
		const dir = storeDir(home);
		mkdirSync(dir, { recursive: true, mode: 0o700 });
		writeFileSync(join(dir, "2020-01-01T00.jsonl"), Buffer.alloc(ROLL_BYTES));
		mustWrite();
		const names = partitions(home).map((p) => p.split("/").pop());
		assert.deepEqual(names, [
			"2020-01-01T00.jsonl",
			`${traceLabel(Date.now())}.jsonl`,
		]);
		assert.ok(
			readFileSync(partitions(home)[1] as string, "utf8").endsWith("\n"),
		);
	});
});

test("trace: the clamp keeps every entry at or after its file's label", () => {
	// A partition labelled in the future stands for a clock that jumped
	// backwards. The minted timestamp clamps up to the range start. The clamp
	// is ONE-SIDED (spec page, amended at the lookup-rule audit): nothing bounds
	// an entry from above.
	withHome((home) => {
		const dir = storeDir(home);
		mkdirSync(dir, { recursive: true, mode: 0o700 });
		const label = traceLabel(Date.now() + 5 * MS_PER_HOUR);
		writeFileSync(join(dir, `${label}.jsonl`), "");
		const id = mustWrite();
		const start = traceLabelStartMs(label);
		assert.equal(ulidTimestamp(id), start);
		assert.equal(
			readEntries(home).entries[0]?.spawned_at,
			traceTimestamp(start),
		);
	});
});

test("trace: readers ignore files that are not partitions", () => {
	withHome((home) => {
		mustWrite();
		const dir = storeDir(home);
		writeFileSync(
			join(dir, "write-failure.marker"),
			"2020-01-01T00:00:00.000Z\n",
		);
		writeFileSync(join(dir, "notes.txt"), "not a partition\n");
		writeFileSync(join(dir, "2020-01-01T00.jsonl.bak"), "junk\n");
		const { entries, anomalies } = readEntries(home);
		assert.equal(entries.length, 1);
		assert.deepEqual(anomalies, []);
	});
});

// --- the lookup rule ------------------------------------------------------

/**
 * Builds the store the lookup-rule audit constructed, using the real writer.
 *
 * 1. A partition labelled for the PREVIOUS hour is the greatest-named file, so
 *    the writer appends to it and mints a timestamp in the CURRENT hour: that
 *    entry is stranded above its own file's label.
 * 2. Padding that file past the roll threshold makes the next write roll, and
 *    the new partition is labelled for the current hour -- the very label the
 *    stranded entry's timestamp points at.
 *
 * Returns [strandedId, rolledId]. When `link` is true the rolled entry inherits
 * the stranded one as its parent, so the pair is a chain crossing the strand.
 */
function strandedStore(home: string, link = false): [string, string] {
	const dir = storeDir(home);
	mkdirSync(dir, { recursive: true, mode: 0o700 });
	const prevLabel = traceLabel(Date.now() - MS_PER_HOUR);
	const prev = join(dir, `${prevLabel}.jsonl`);
	writeFileSync(prev, "");
	const stranded = mustWrite();
	// Padding past the roll threshold. It is one anomalous line, which every
	// reader here skips.
	appendFileSync(prev, `${"x".repeat(ROLL_BYTES)}\n`);
	if (link) {
		process.env[TRACE_PARENT_ENV] = stranded;
	}
	const rolled = mustWrite();
	if (link) {
		delete process.env[TRACE_PARENT_ENV];
	}
	assert.equal(partitions(home).length, 2);
	return [stranded, rolled];
}

test("trace: the range is not bounded at the top", () => {
	// The falsifying store: an entry whose timestamp is at or beyond the NEXT
	// partition's label, living in the older file.
	withHome((home) => {
		const [stranded] = strandedStore(home);
		const parts = partitions(home);
		const older = entriesIn(parts[0] as string).entries;
		assert.equal(older.length, 1);
		assert.equal(older[0]?.id, stranded);
		const newerLabel = (parts[1] as string)
			.split("/")
			.pop()
			?.replace(/\.jsonl$/, "") as string;
		assert.ok(
			(ulidTimestamp(stranded) as number) >= traceLabelStartMs(newerLabel),
		);
	});
});

test("trace: a stranded entry is found by walking backward", () => {
	withHome((home) => {
		const [stranded] = strandedStore(home);
		assert.equal(resolveEntry(home, stranded)?.id, stranded);
	});
});

test("trace: one binary search alone misses the stranded entry", () => {
	// The rule the spec page carried until the lookup-rule audit: search the
	// partition the timestamp points at and stop. It reports a live entry as
	// missing, which a consumer records as a dangling parent.
	withHome((home) => {
		const [stranded] = strandedStore(home);
		assert.equal(resolveEntry(home, stranded, false), null);
	});
});

test("trace: the unstranded entry is found by the first search", () => {
	withHome((home) => {
		const [, rolled] = strandedStore(home);
		assert.notEqual(resolveEntry(home, rolled, false), null);
	});
});

test("trace: a chain across the strand still flattens", () => {
	withHome((home) => {
		const [stranded, rolled] = strandedStore(home, true);
		assert.deepEqual(flattenAncestry(home, rolled), [rolled, stranded]);
	});
});

test("trace: an identifier no store holds resolves to nothing", () => {
	withHome((home) => {
		mustWrite();
		assert.equal(resolveEntry(home, "01JZ8X4M6N7QK2WVBD3F5RTYAC"), null);
	});
});

test("trace: an unparseable identifier resolves to nothing", () => {
	withHome((home) => {
		mustWrite();
		assert.equal(resolveEntry(home, "not-a-ulid"), null);
	});
});

// --- propagation ----------------------------------------------------------

test("trace: parent_id is the inherited identifier", () => {
	withHome((home) => {
		const parent = ulidMint(1786594672913);
		process.env[TRACE_PARENT_ENV] = parent;
		mustWrite();
		assert.equal(readEntries(home).entries[0]?.parent_id, parent);
	});
});

test("trace: a malformed inherited value records a null parent", () => {
	for (const polluted of [
		"",
		"not-a-ulid",
		"01jz8x4m6n7qk2wvbd3f5rtyac",
		"ZZZZZZZZZZZZZZZZZZZZZZZZZZ",
		"01JZ8X4M6N7QK2WVBD3F5RTYA",
	]) {
		withHome((home) => {
			process.env[TRACE_PARENT_ENV] = polluted;
			mustWrite(); // never bricks the run
			const { entries, anomalies } = readEntries(home);
			assert.equal(entries[0]?.parent_id, null, polluted);
			assert.deepEqual(anomalies, [], polluted);
		});
	}
});

test("trace: a dangling parent is legal by design", () => {
	withHome((home) => {
		process.env[TRACE_PARENT_ENV] = "01JZ8X4M6N7QK2WVBD3F5RTYAC";
		const id = mustWrite();
		assert.deepEqual(flattenAncestry(home, id), [id]);
	});
});

test("trace: the framework's composition wins over a handler's env", () => {
	withHome((home) => {
		process.env[TRACE_PARENT_ENV] = "01JZ8X4M6N7QK2WVBD3F5RTYAC";
		const child = traceChildEnv(
			{
				[TRACE_PARENT_ENV]: "01JZ8X4M6N7QK2WVBD3F5RTYAC",
				MY_KEY: "mine",
			},
			identity(),
		);
		const id = readEntries(home).entries[0]?.id;
		assert.equal(child[TRACE_PARENT_ENV], id);
		assert.equal(child.MY_KEY, "mine");
		// The spawning process's own environment is never mutated.
		assert.equal(process.env[TRACE_PARENT_ENV], "01JZ8X4M6N7QK2WVBD3F5RTYAC");
	});
});

test("trace: a broken store removes the variable from the child", () => {
	// A lost record must not silently re-attribute the child to its
	// grandparent, so the inherited value is dropped rather than passed on.
	withHome((home) => {
		breakStore(home);
		process.env[TRACE_PARENT_ENV] = "01JZ8X4M6N7QK2WVBD3F5RTYAC";
		const child = traceChildEnv(undefined, identity());
		assert.equal(Object.hasOwn(child, TRACE_PARENT_ENV), false);
	});
});

// --- the seam -------------------------------------------------------------

test("trace: a live run writes one entry", async () => {
	await withHomeAsync(async (home) => {
		const app = mutApp((ctx) => {
			ctx.effects.run(["true"]);
			return 0;
		});
		assert.equal((await app.test(["go"])).exitCode, 0);
		const { entries } = readEntries(home);
		assert.equal(entries.length, 1);
		assert.equal(entries[0]?.command, "go");
		assert.equal(entries[0]?.dry_run, false);
		assert.equal(entries[0]?.effect, "mutating");
	});
});

test("trace: a live spawn writes one entry", async () => {
	await withHomeAsync(async (home) => {
		const app = mutApp((ctx) => {
			ctx.effects.spawn(["true"]).wait();
			return 0;
		});
		await app.test(["go"]);
		assert.equal(readEntries(home).entries.length, 1);
	});
});

test("trace: the child receives this entry's identifier", async () => {
	await withHomeAsync(async (home) => {
		let seen = "";
		const app = mutApp((ctx) => {
			seen = ctx.effects.run([
				"sh",
				"-c",
				'printf %s "$STRICTCLI_TRACE_PARENT"',
			]).stdout;
			return 0;
		});
		await app.test(["go"]);
		const { entries } = readEntries(home);
		assert.equal(entries.length, 1);
		assert.equal(seen, entries[0]?.id);
	});
});

test("trace: a recorded dry-mode spawn writes nothing", async () => {
	await withHomeAsync(async (home) => {
		const app = mutApp((ctx) => {
			ctx.effects.spawn(["true"]);
			return 0;
		});
		await app.test(["--dry-run", "go"]);
		assert.equal(readEntries(home).entries.length, 0);
	});
});

test("trace: a recorded dry-mode run writes nothing", async () => {
	await withHomeAsync(async (home) => {
		const app = mutApp((ctx) => {
			ctx.effects.run(["true"]);
			return 0;
		});
		await app.test(["--dry-run", "go"]);
		assert.equal(readEntries(home).entries.length, 0);
	});
});

test("trace: an allowlisted observe in dry mode writes an entry", async () => {
	// An observe genuinely executes in dry mode, so a real child starts -- and
	// the entry carries dry_run: true, which is the only way that field can
	// ever be true.
	await withHomeAsync(async (home) => {
		const app = roApp(
			(ctx) => {
				ctx.effects.run(["true", "--ok"]);
				return 0;
			},
			{ procObserveAllowlist: [["true", "--ok"]] },
		);
		await app.test(["--dry-run", "go"]);
		const { entries } = readEntries(home);
		assert.equal(entries.length, 1);
		assert.equal(entries[0]?.dry_run, true);
		assert.equal(entries[0]?.effect, "read_only");
	});
});

test("trace: a stale observe in dry mode writes nothing", async () => {
	// After a recorded mutation the observe is not executed at all, so no child
	// starts and no entry is written.
	await withHomeAsync(async (home) => {
		const app = mutApp(
			(ctx) => {
				ctx.effects.mkdir(join(home, "d"));
				ctx.effects.run(["true", "--ok"]);
				return 0;
			},
			{ procObserveAllowlist: [["true", "--ok"]] },
		);
		await app.test(["--dry-run", "go"]);
		assert.equal(readEntries(home).entries.length, 0);
	});
});

test("trace: the reserved-flag state and machine mode are recorded", async () => {
	await withHomeAsync(async (home) => {
		const app = mutApp((ctx) => {
			ctx.effects.run(["true"]);
			return 0;
		});
		await app.test(["go", "--quiet", "--json", "--approve-consequential"]);
		const entry = readEntries(home).entries[0] as Record<string, unknown>;
		assert.equal(entry.quiet, true);
		assert.equal(entry.verbose, false);
		assert.equal(entry.machine_mode, true);
		assert.equal(entry.approve_consequential, true);
	});
});

test("trace: argv is never recorded", async () => {
	await withHomeAsync(async (home) => {
		const app = mutApp((ctx) => {
			ctx.effects.run(["true", "s3cr3t-token"]);
			return 0;
		});
		await app.test(["go"]);
		const text = readFileSync(partitions(home)[0] as string, "utf8");
		assert.ok(!text.includes("s3cr3t-token"));
	});
});

// --- failure policy -------------------------------------------------------

function breakStore(home: string): string {
	const dir = storeDir(home);
	mkdirSync(dir, { recursive: true, mode: 0o700 });
	chmodSync(dir, 0o500);
	return dir;
}

test("trace: a write failure never fails the run and prints nothing", async () => {
	await withHomeAsync(async (home) => {
		breakStore(home);
		const app = mutApp((ctx) => {
			ctx.effects.run(["true"]);
			return 0;
		});
		const r = await app.test(["go"]);
		assert.equal(r.exitCode, 0);
		assert.equal(r.stdout, "");
		assert.equal(r.stderr, "");
	});
});

test("trace: a write failure returns no identifier", () => {
	withHome((home) => {
		breakStore(home);
		assert.equal(traceWriteEntry(identity()), null);
	});
});

test("trace: the first failure writes the write-once marker", () => {
	withHome((home) => {
		const dir = storeDir(home);
		mkdirSync(dir, { recursive: true, mode: 0o700 });
		// A directory the writer can create the marker in, but whose partition
		// path cannot be opened as a file.
		mkdirSync(join(dir, `${traceLabel(Date.now())}.jsonl`));
		assert.equal(traceWriteEntry(identity()), null);
		const marker = join(dir, "write-failure.marker");
		const text = readFileSync(marker, "utf8");
		assert.equal(text.split("\n").length - 1, 1);
		assert.ok(text.endsWith("\n"));
		assert.ok(
			/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/.test(text.slice(0, -1)),
		);
		// Write-once: three more failures leave the first content untouched.
		for (let i = 0; i < 3; i++) {
			traceWriteEntry(identity());
		}
		assert.equal(readFileSync(marker, "utf8"), text);
	});
});

test("trace: a marker that cannot be written is swallowed too", () => {
	withHome((home) => {
		const dir = breakStore(home);
		assert.equal(traceWriteEntry(identity()), null);
		assert.equal(existsSync(join(dir, "write-failure.marker")), false);
	});
});

test("trace: a store path that is a file is swallowed", () => {
	withHome((home) => {
		const parent = join(home, ".local", "share", "strictcli");
		mkdirSync(parent, { recursive: true });
		writeFileSync(join(parent, "trace"), "not a directory");
		assert.equal(traceWriteEntry(identity()), null);
	});
});

// --- malformed data -------------------------------------------------------

test("trace: a torn line is skipped and recorded as an anomaly", () => {
	withHome((home) => {
		mustWrite();
		appendFileSync(
			partitions(home)[0] as string,
			'{"id":"01JZ8X4M6N7QK2WVBD3F5RTYAC","parent\n',
		);
		mustWrite();
		const { entries, anomalies } = readEntries(home);
		assert.equal(entries.length, 2);
		assert.equal(anomalies.length, 1);
	});
});

test("trace: a truncated final line does not disturb writers", () => {
	withHome((home) => {
		mustWrite();
		appendFileSync(
			partitions(home)[0] as string,
			'{"id":"01JZ8X4M6N7QK2WVBD3F5R',
		);
		assert.notEqual(traceWriteEntry(identity()), null);
		assert.equal(
			existsSync(join(storeDir(home), "write-failure.marker")),
			false,
		);
		const { entries, anomalies } = readEntries(home);
		assert.equal(entries.length, 1);
		assert.equal(anomalies.length, 1);
	});
});

test("trace: an entry missing a key is an anomaly, not a default", () => {
	withHome((home) => {
		mustWrite();
		appendFileSync(
			partitions(home)[0] as string,
			'{"id":"01JZ8X4M6N7QK2WVBD3F5RTYAC"}\n',
		);
		const { entries, anomalies } = readEntries(home);
		assert.equal(entries.length, 1);
		assert.equal(anomalies.length, 1);
	});
});

// --- the chain ------------------------------------------------------------

const DIST_INDEX = join(
	dirname(fileURLToPath(import.meta.url)),
	"..",
	"..",
	"dist",
	"index.js",
);

const CHAIN_SCRIPT = `
import { arg, createApp, defineMutatingCommand, t } from ${JSON.stringify(DIST_INDEX)};

const app = createApp({ name: "chain", version: "9.9.9", help: "chain" });
app.command(
	defineMutatingCommand("go", {
		help: "start the next link",
		args: [arg("depth", t.int, { help: "how many more children to start", presence: "required" })],
		handler: (args, ctx) => {
			const depth = Number(args.depth);
			if (depth > 0) {
				ctx.effects
					.spawn([process.execPath, process.argv[1], "go", String(depth - 1)])
					.wait();
			}
			return 0;
		},
	}),
);
await app.run();
`;

test("trace: a three-deep spawn chain yields a flattened ancestry", () => {
	withHome((home) => {
		const script = join(home, "chain.mjs");
		writeFileSync(script, CHAIN_SCRIPT);
		const env: Record<string, string | undefined> = {
			...process.env,
			HOME: home,
		};
		delete env[TRACE_PARENT_ENV];
		execFileSync(process.execPath, [script, "go", "3"], {
			env,
			stdio: "pipe",
			timeout: 60000,
		});

		const { entries, anomalies } = readEntries(home);
		assert.deepEqual(anomalies, []);
		// Four processes, three of which start a child: three entries.
		assert.equal(entries.length, 3);
		const parents = new Set(entries.map((e) => e.parent_id));
		const leaves = entries.filter((e) => !parents.has(e.id));
		assert.equal(leaves.length, 1);
		const chain = flattenAncestry(home, leaves[0]?.id as string);
		assert.equal(chain.length, 3);
		const byId = new Map(entries.map((e) => [e.id as string, e]));
		assert.equal(byId.get(chain[2] as string)?.parent_id, null);
		assert.equal(
			new Set(chain.map((id) => byId.get(id)?.pid)).size,
			3,
			"the chain must witness three distinct pids",
		);
		for (const entry of entries) {
			assert.equal(entry.app, "chain");
			assert.equal(entry.command, "go");
		}
	});
});
