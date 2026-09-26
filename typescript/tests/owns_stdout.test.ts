/**
 * Stdout ownership: the ownsStdout declaration (effects contract §19.6).
 *
 * A command whose stdout IS the artifact declares it, and in machine mode the
 * envelope moves to stderr so the artifact's bytes are untouched. Outside
 * machine mode the declaration changes nothing at all.
 *
 * The handler writes its document through ctx.document(), which app.test()
 * captures as stdout.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import {
	type App,
	createApp,
	defineMutatingCommand,
	defineReadOnlyCommand,
} from "../src/index.js";
import { dumpSchemaCore } from "../src/schema.js";
import { envelope } from "./envelope_helpers.js";

const DOC = '{"artifact":"v1"}';

/** Runs app.test(argv), returning both captured streams. */
async function captureRun(
	app: App,
	argv: string[],
): Promise<{ stdout: string; stderr: string }> {
	const r = await app.test(argv);
	return { stdout: r.stdout, stderr: r.stderr };
}

function dumpApp(): App {
	const app = createApp({ name: "app", version: "1.0.0", help: "app" });
	app.command(
		defineReadOnlyCommand("dump", {
			help: "dump",
			ownsStdout: true,
			handler: (_args, ctx) => {
				ctx.document().write(`${DOC}\n`);
				return 0;
			},
		}),
	);
	return app;
}

test("ownsStdout: outside machine mode the declaration changes nothing", async () => {
	const r = await captureRun(dumpApp(), ["dump"]);
	assert.equal(r.stdout, `${DOC}\n`);
	assert.equal(r.stderr, "");
});

test("ownsStdout: the document keeps stdout and the envelope moves to stderr", async () => {
	const r = await captureRun(dumpApp(), ["--json", "dump"]);
	assert.equal(r.stdout, `${DOC}\n`);
	const env = envelope(r.stderr);
	assert.equal(env.command, "dump");
	assert.equal(env.exit_code, 0);
});

test("ownsStdout: the diagnostics move with the envelope", async () => {
	const app = createApp({ name: "app", version: "1.0.0", help: "app" });
	app.command(
		defineReadOnlyCommand("dump", {
			help: "dump",
			ownsStdout: true,
			handler: (_args, ctx) => {
				ctx.info("wrote 1 row");
				ctx.warn("provisional");
				return 0;
			},
		}),
	);
	const r = await app.test(["--json", "dump"]);
	assert.equal(r.stdout, "");
	assert.deepEqual(envelope(r.stderr).diagnostics, [
		{ level: "info", message: "wrote 1 row" },
		{ level: "warn", message: "provisional" },
	]);
});

test("ownsStdout: an undeclared command keeps the envelope on stdout", async () => {
	const app = createApp({ name: "app", version: "1.0.0", help: "app" });
	app.command(
		defineReadOnlyCommand("plain", { help: "plain", handler: () => 0 }),
	);
	const r = await app.test(["--json", "plain"]);
	assert.equal(envelope(r.stdout).command, "plain");
	assert.equal(r.stderr, "");
});

test("ownsStdout: a preview's envelope moves too", async () => {
	const app = createApp({ name: "app", version: "1.0.0", help: "app" });
	app.command(
		defineMutatingCommand("dump", {
			help: "dump",
			ownsStdout: true,
			handler: (_args, ctx) => {
				ctx.effects.write("out.sql", "x");
				return 0;
			},
		}),
	);
	const r = await app.test(["--json", "--dry-run", "dump"]);
	assert.equal(r.stdout, "");
	const env = envelope(r.stderr);
	assert.equal(env.dry_run, true);
	assert.deepEqual(
		(env.preview as Record<string, unknown>[]).map((rec) => rec.verb),
		["write"],
	);
});

test("ownsStdout: a declared payload still rides the envelope on stderr", async () => {
	const app = createApp({ name: "app", version: "1.0.0", help: "app" });
	app.command(
		defineReadOnlyCommand("dump", {
			help: "dump",
			ownsStdout: true,
			payloadSchema: { type: "object" },
			handler: (_args, ctx) => {
				ctx.payload({ rows: 3 });
				return 0;
			},
		}),
	);
	const r = await app.test(["--json", "dump"]);
	assert.equal(r.stdout, "");
	assert.deepEqual(envelope(r.stderr).payload, { rows: 3 });
});

test("ownsStdout: the schema dump publishes the key only when true", () => {
	const app = createApp({ name: "app", version: "1.0.0", help: "app" });
	app.command(
		defineReadOnlyCommand("dump", {
			help: "dump",
			ownsStdout: true,
			handler: () => 0,
		}),
	);
	app.command(
		defineReadOnlyCommand("plain", { help: "plain", handler: () => 0 }),
	);
	const commands = dumpSchemaCore(
		app as unknown as Parameters<typeof dumpSchemaCore>[0],
	).commands as Record<string, Record<string, unknown>>;
	assert.equal((commands.dump as Record<string, unknown>).owns_stdout, true);
	assert.equal(
		"owns_stdout" in (commands.plain as Record<string, unknown>),
		false,
	);
});
