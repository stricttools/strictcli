/**
 * Connection-env primitive tests: app-level declaration, lazy read, hermetic
 * suppression, check-side access (ConnectionEnvReader), registration-time
 * enforcement, and precedence (cli > env). Parity with the Go and Python
 * connection-env tests.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { CheckContext, ConnectionEnvReader } from "../src/index.js";
import { defineReadOnlyCommand, flag, t } from "../src/index.js";
import { dumpSchemaCore } from "../src/schema.js";
import { createTestApp as createApp } from "./helpers.js";

async function withEnv<T>(
	vars: Record<string, string | undefined>,
	fn: () => Promise<T> | T,
): Promise<T> {
	const saved = new Map<string, string | undefined>();
	for (const [k, v] of Object.entries(vars)) {
		saved.set(k, process.env[k]);
		if (v === undefined) {
			delete process.env[k];
		} else {
			process.env[k] = v;
		}
	}
	try {
		return await fn();
	} finally {
		for (const [k, v] of saved) {
			if (v === undefined) {
				delete process.env[k];
			} else {
				process.env[k] = v;
			}
		}
	}
}

function connApp() {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		connectionEnv: { DATABASE_URL: "conn" },
	});
	app.command(
		defineReadOnlyCommand("run", {
			help: "run it",
			flags: {
				dsn: flag("dsn", t.str, {
					help: "connection string",
					presence: "optional",
					connectionUrl: true,
					connectionEnv: "DATABASE_URL",
				}),
			},
			handler: (args, ctx) => {
				ctx.info(`${ctx.source("dsn")}:${args.dsn}`);
				return 0;
			},
		}),
	);
	return app;
}

// --- Declaration + help + schema ---

test("connection: help renders the suppressed-by-hermetic line", async () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		connectionEnv: { DATABASE_URL: "Postgres connection string" },
	});
	app.command(
		defineReadOnlyCommand("run", { help: "run it", handler: () => 0 }),
	);
	const r = await app.test(["--help"]);
	assert.ok(r.stdout.includes("Infrastructure:"));
	assert.ok(
		r.stdout.includes(
			"connection URL, suppressed by --hermetic (Postgres connection string)",
		),
		r.stdout,
	);
});

test("connection: schema dump lists connections", () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		connectionEnv: { DATABASE_URL: "Postgres connection string" },
	});
	const schema = dumpSchemaCore(app as never) as {
		infra: { connections: unknown[] };
	};
	assert.deepEqual(schema.infra.connections, [
		{ env_var: "DATABASE_URL", help: "Postgres connection string" },
	]);
});

// --- Lazy read + precedence (cli > env) + hermetic ---

test("connection: lazy read from env, source is 'env'", async () => {
	await withEnv({ DATABASE_URL: "postgres://from-env/db" }, async () => {
		const r = await connApp().test(["run"]);
		assert.equal(r.exitCode, 0, r.stderr);
		assert.equal(r.stdout, "env:postgres://from-env/db\n");
	});
});

test("connection: CLI beats env, source is 'cli'", async () => {
	await withEnv({ DATABASE_URL: "postgres://from-env/db" }, async () => {
		const r = await connApp().test(["run", "--dsn", "postgres://from-cli/db"]);
		assert.equal(r.exitCode, 0, r.stderr);
		assert.equal(r.stdout, "cli:postgres://from-cli/db\n");
	});
});

test("connection: hermetic suppresses the env read", async () => {
	await withEnv({ DATABASE_URL: "postgres://from-env/db" }, async () => {
		const r = await connApp().test(["--hermetic", "run"]);
		assert.equal(r.exitCode, 0, r.stderr);
		assert.ok(!r.stdout.includes("env:postgres://from-env/db"), r.stdout);
	});
});

// --- Presence: a REQUIRED connection-URL flag (contract §23.5's env row) ---

// The bound connection env must satisfy requiredness on its own: that is
// §23.5's env row, and the URL-class row adds no guard on top of it.
function requiredConnApp() {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		connectionEnv: { DATABASE_URL: "Postgres connection string" },
	});
	app.command(
		defineReadOnlyCommand("run", {
			help: "run it",
			flags: {
				dsn: flag("dsn", t.str, {
					help: "connection string",
					presence: "required",
					connectionUrl: true,
					connectionEnv: "DATABASE_URL",
				}),
			},
			handler: (args, ctx) => {
				ctx.info(`${ctx.source("dsn")}:${ctx.provided("dsn")}:${args.dsn}`);
				return 0;
			},
		}),
	);
	return app;
}

test("connection: a required connection flag is satisfied by the env", async () => {
	await withEnv({ DATABASE_URL: "postgres://from-env/db" }, async () => {
		const r = await requiredConnApp().test(["run"]);
		assert.equal(r.exitCode, 0, r.stderr);
		assert.equal(r.stdout, "env:true:postgres://from-env/db\n");
	});
});

test("connection: a required connection flag with the env unset is the required error", async () => {
	await withEnv({ DATABASE_URL: undefined }, async () => {
		const r = await requiredConnApp().test(["run"]);
		assert.equal(r.exitCode, 1);
		assert.equal(
			r.stderr,
			"error: flag '--dsn' is required\ntry 'myapp run --help'\n",
		);
	});
});

test("connection: CLI beats the env on a required connection flag", async () => {
	await withEnv({ DATABASE_URL: "postgres://from-env/db" }, async () => {
		const r = await requiredConnApp().test([
			"run",
			"--dsn",
			"postgres://from-cli/db",
		]);
		assert.equal(r.exitCode, 0, r.stderr);
		assert.equal(r.stdout, "cli:true:postgres://from-cli/db\n");
	});
});

test("connection: hermetic suppresses the env, so a required connection flag errors", async () => {
	await withEnv({ DATABASE_URL: "postgres://from-env/db" }, async () => {
		const r = await requiredConnApp().test(["--hermetic", "run"]);
		assert.equal(r.exitCode, 1);
		assert.equal(
			r.stderr,
			"error: flag '--dsn' is required\ntry 'myapp run --help'\n",
		);
	});
});

// --- Handler-side infraValue / connectionEnvValue ---

test("connection: infraValue and connectionEnvValue resolve live", async () => {
	await withEnv({ DATABASE_URL: "postgres://live/db" }, async () => {
		const app = createApp({
			name: "myapp",
			version: "1.0.0",
			help: "t",
			connectionEnv: { DATABASE_URL: "conn" },
		});
		app.command(
			defineReadOnlyCommand("run", {
				help: "run",
				handler: (_args, ctx) => {
					const [cv, cs] = ctx.connectionEnvValue("DATABASE_URL");
					const [iv, is] = ctx.infraValue("DATABASE_URL");
					ctx.info(`${cv}:${cs}:${iv}:${is}`);
					return 0;
				},
			}),
		);
		const r = await app.test(["run"]);
		assert.equal(r.stdout, "postgres://live/db:true:postgres://live/db:true\n");
	});
});

test("connection: hermetic makes connectionEnvValue absent", async () => {
	await withEnv({ DATABASE_URL: "postgres://live/db" }, async () => {
		const app = createApp({
			name: "myapp",
			version: "1.0.0",
			help: "t",
			connectionEnv: { DATABASE_URL: "conn" },
		});
		app.command(
			defineReadOnlyCommand("run", {
				help: "run",
				handler: (_args, ctx) => {
					const [cv, cs] = ctx.connectionEnvValue("DATABASE_URL");
					ctx.info(`${cv}:${cs}`);
					return 0;
				},
			}),
		);
		const r = await app.test(["--hermetic", "run"]);
		assert.equal(r.stdout, "undefined:false\n");
	});
});

// --- Check-side access via ConnectionEnvReader ---

function connCheckApp() {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		connectionEnv: { DATABASE_URL: "conn" },
		checksEmbed: `app = "myapp"
[checks.db-reachable]
description = "Checks db-reachable"
subject = "quality"
tags = ["db"]
severity = "error"
fast = true
pure = false
needs_network = true
depends_on = []
`,
	});
	app.errorCheck("db-reachable", (ctx, r) => {
		const reader = ctx as unknown as ConnectionEnvReader;
		r.note(`hermetic=${reader.isHermetic()}`);
		const [dsn, present] = reader.connectionEnvValue("DATABASE_URL");
		if (!present) {
			// Distinguish hermetic suppression from a plainly-unset env: a
			// consumer layering config fallbacks below the env must honor
			// hermetic (skip) rather than fall through and connect.
			if (reader.isHermetic()) {
				return r.skipped("DATABASE_URL suppressed by --hermetic");
			}
			return r.skipped("DATABASE_URL unset");
		}
		r.note(`dsn=${dsn}`);
		return r.passed("connection env visible");
	});
	const ctx: CheckContext = { projectRoot: "." };
	app.setCheckContext(() => ctx);
	return app;
}

test("connection: check reads the connection env", async () => {
	await withEnv({ DATABASE_URL: "postgres://check/db" }, async () => {
		const r = await connCheckApp().test(["--verbose", "check", "--tag", "db"]);
		assert.ok(r.stdout.includes("dsn=postgres://check/db"), r.stdout);
		assert.ok(r.stdout.includes("PASS"), r.stdout);
		// A non-hermetic invocation reports isHermetic()===false to the check.
		assert.ok(r.stdout.includes("hermetic=false"), r.stdout);
	});
});

test("connection: check skips under hermetic", async () => {
	await withEnv({ DATABASE_URL: "postgres://check/db" }, async () => {
		const r = await connCheckApp().test([
			"--verbose",
			"--hermetic",
			"check",
			"--tag",
			"db",
		]);
		assert.ok(r.stdout.includes("SKIP"), r.stdout);
		assert.ok(!r.stdout.includes("dsn="), r.stdout);
		// The check SEES hermetic (isHermetic()===true) and skips for that reason.
		assert.ok(r.stdout.includes("hermetic=true"), r.stdout);
		assert.ok(r.stdout.includes("suppressed by --hermetic"), r.stdout);
	});
});

// Documents the exact gap isHermetic() closes: under --hermetic with the env var
// UNSET, connectionEnvValue returns present=false (indistinguishable from a
// plain unset), yet isHermetic() returns true -- so a check layering config
// fallbacks can honor hermetic instead of connecting via a config URL.
test("connection: check sees hermetic even when env is unset (conflation case)", async () => {
	await withEnv({ DATABASE_URL: undefined }, async () => {
		const r = await connCheckApp().test([
			"--verbose",
			"--hermetic",
			"check",
			"--tag",
			"db",
		]);
		assert.ok(r.stdout.includes("hermetic=true"), r.stdout);
		assert.ok(r.stdout.includes("suppressed by --hermetic"), r.stdout);
		assert.ok(!r.stdout.includes("DATABASE_URL unset"), r.stdout);
	});
});

// Counterpart: env unset and NOT hermetic -> isHermetic()===false, so a consumer
// is free to consult a config fallback (here reported as the plain-unset skip).
test("connection: env unset without hermetic reports isHermetic()===false", async () => {
	await withEnv({ DATABASE_URL: undefined }, async () => {
		const r = await connCheckApp().test(["--verbose", "check", "--tag", "db"]);
		assert.ok(r.stdout.includes("hermetic=false"), r.stdout);
		assert.ok(r.stdout.includes("DATABASE_URL unset"), r.stdout);
	});
});

// --- Registration-time enforcement ---

test("connection: URL-class flag with no binding is a registration error", () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		connectionEnv: { DATABASE_URL: "conn" },
	});
	assert.throws(
		() =>
			app.command(
				defineReadOnlyCommand("run", {
					help: "run",
					flags: {
						dsn: flag("dsn", t.str, {
							help: "dsn",
							presence: "optional",
							connectionUrl: true,
						}),
					},
					handler: () => 0,
				}),
			),
		/must bind to a declared connection env/,
	);
});

test("connection: binding to an undeclared connection env is a registration error", () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		connectionEnv: { DATABASE_URL: "conn" },
	});
	assert.throws(
		() =>
			app.command(
				defineReadOnlyCommand("run", {
					help: "run",
					flags: {
						dsn: flag("dsn", t.str, {
							help: "dsn",
							presence: "optional",
							connectionUrl: true,
							connectionEnv: "OTHER_URL",
						}),
					},
					handler: () => 0,
				}),
			),
		/undeclared connection env/,
	);
});

test("connection: binding without the URL marker is a registration error", () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		connectionEnv: { DATABASE_URL: "conn" },
	});
	assert.throws(
		() =>
			app.command(
				defineReadOnlyCommand("run", {
					help: "run",
					flags: {
						dsn: flag("dsn", t.str, {
							help: "dsn",
							presence: "optional",
							connectionEnv: "DATABASE_URL",
						}),
					},
					handler: () => 0,
				}),
			),
		/requires the flag to be marked as a connection-URL flag/,
	);
});

test("connection: binding plus per-flag env is a registration error", () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "t",
		connectionEnv: { DATABASE_URL: "conn" },
	});
	assert.throws(
		() =>
			app.command(
				defineReadOnlyCommand("run", {
					help: "run",
					flags: {
						dsn: flag("dsn", t.str, {
							help: "dsn",
							presence: "optional",
							env: "SOMETHING_ELSE",
							connectionUrl: true,
							connectionEnv: "DATABASE_URL",
						}),
					},
					handler: () => 0,
				}),
			),
		/cannot be combined with a per-flag env var/,
	);
});

test("connection: empty help is a registration error", () => {
	assert.throws(
		() =>
			createApp({
				name: "myapp",
				version: "1.0.0",
				help: "t",
				connectionEnv: { DATABASE_URL: "" },
			}),
		/help must be a non-empty string/,
	);
});

test("connection: colliding with a root is a registration error", () => {
	assert.throws(
		() =>
			createApp({
				name: "myapp",
				version: "1.0.0",
				help: "t",
				infraRoot: { SHARED: "/var/lib" },
				connectionEnv: { SHARED: "conn" },
			}),
		/already declared as an infra root/,
	);
});

test("connection: colliding with a handshake is a registration error", () => {
	assert.throws(
		() =>
			createApp({
				name: "myapp",
				version: "1.0.0",
				help: "t",
				handshakeEnv: { SHARED: "handshake" },
				connectionEnv: { SHARED: "conn" },
			}),
		/already declared as a handshake env var/,
	);
});
