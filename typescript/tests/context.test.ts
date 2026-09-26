import { strict as assert } from "node:assert";
import { test } from "node:test";
import { Context, type Writer } from "../src/context.js";

function sink(): { chunks: string[]; writer: Writer } {
	const chunks: string[] = [];
	return { chunks, writer: { write: (s: string) => chunks.push(s) } };
}

test("context: info writes to stdout, warn/error to stderr, debug is gated", () => {
	const out = sink();
	const err = sink();
	const ctx = new Context(out.writer, err.writer, {}, null);
	ctx.info("i");
	// debug is hidden by default: it is shown only under --verbose.
	ctx.debug("d");
	ctx.warn("w");
	ctx.error("e");
	assert.deepEqual(out.chunks, ["i\n"]);
	assert.deepEqual(err.chunks, ["warning: w\n", "error: e\n"]);
});

test("context: source accepts underscored and dashed names", () => {
	const out = sink();
	const ctx = new Context(out.writer, out.writer, { dry_run: "cli" }, null);
	assert.equal(ctx.source("dry_run"), "cli");
	assert.equal(ctx.source("dry-run"), "cli");
});

test("context: source tries the underscore form first", () => {
	const out = sink();
	// A pathological map with both spellings: the underscore form wins,
	// mirroring the siblings' lookup order.
	const ctx = new Context(
		out.writer,
		out.writer,
		{ dry_run: "env", "dry-run": "cli" },
		null,
	);
	assert.equal(ctx.source("dry-run"), "env");
});

test("context: source throws the sibling message for unknown flags", () => {
	const out = sink();
	const ctx = new Context(out.writer, out.writer, {}, null);
	assert.throws(() => ctx.source("nope"), {
		message: 'no source info for flag "nope"',
	});
});

test("context: infraValue throws not-declared without infra wiring", () => {
	const out = sink();
	const ctx = new Context(out.writer, out.writer, {}, null);
	assert.throws(() => ctx.infraValue("MYTOOL_ROOT"), {
		message:
			'InfraValue: "MYTOOL_ROOT" is not a declared infra root, handshake, or connection env var',
	});
});

test("context: infraValue resolves declared roots and live handshakes", () => {
	const out = sink();
	const ctx = new Context(
		out.writer,
		out.writer,
		{},
		{
			roots: new Map([["MYTOOL_ROOT", "/resolved/root"]]),
			handshakes: new Set(["MYTOOL_HANDSHAKE"]),
			connections: new Set<string>(),
			hermetic: false,
		},
	);
	assert.deepEqual(ctx.infraValue("MYTOOL_ROOT"), ["/resolved/root", true]);
	// Handshake read is LIVE from the environment.
	delete process.env.MYTOOL_HANDSHAKE;
	assert.deepEqual(ctx.infraValue("MYTOOL_HANDSHAKE"), [undefined, false]);
	process.env.MYTOOL_HANDSHAKE = "1";
	try {
		assert.deepEqual(ctx.infraValue("MYTOOL_HANDSHAKE"), ["1", true]);
	} finally {
		delete process.env.MYTOOL_HANDSHAKE;
	}
	assert.throws(() => ctx.infraValue("OTHER"), {
		message:
			'InfraValue: "OTHER" is not a declared infra root, handshake, or connection env var',
	});
});

// --- ctx.provided (contract §23.6) ---

test("context: provided is true for the sources the invocation supplies", () => {
	const out = sink();
	const ctx = new Context(
		out.writer,
		out.writer,
		{
			typed: "cli",
			from_env: "env",
			from_config: "config",
			implied_by: "implied",
			declared: "default",
			rooted: "infra",
		},
		null,
	);
	// The invocation caused the value.
	assert.equal(ctx.provided("typed"), true);
	assert.equal(ctx.provided("from_env"), true);
	assert.equal(ctx.provided("from_config"), true);
	assert.equal(ctx.provided("implied_by"), true);
	// The declaration caused the value: a declared default, and a
	// RelativeToRoot default whose label merely says WHICH default it was.
	assert.equal(ctx.provided("declared"), false);
	assert.equal(ctx.provided("rooted"), false);
});

test("context: provided accepts dashed or underscored names, underscore first", () => {
	const out = sink();
	const ctx = new Context(
		out.writer,
		out.writer,
		{ from_file: "cli", "from-file": "default" },
		null,
	);
	assert.equal(ctx.provided("from-file"), true);
	assert.equal(ctx.provided("from_file"), true);
});

test("context: provided rejects an unknown name exactly as source does", () => {
	const out = sink();
	const ctx = new Context(out.writer, out.writer, { known: "cli" }, null);
	assert.throws(() => ctx.provided("nope"), {
		message: 'no source info for flag "nope"',
	});
	assert.throws(() => ctx.source("nope"), {
		message: 'no source info for flag "nope"',
	});
});
