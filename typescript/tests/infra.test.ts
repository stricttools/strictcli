/**
 * Infrastructure env-var tests: eager root resolution, live handshake reads,
 * the relativeToRoot() marker (branding, defaults, provenance, registration
 * validation), hermetic immunity, and the help "Infrastructure:" section.
 * Byte expectations were captured from the Go implementation (help section)
 * and the Python implementation (marker repr, registration errors) -- the
 * unit-level pins for go/strictcli/testdata/cases/infra_env.json and the relevant
 * hermetic.json semantics.
 */

import { strict as assert } from "node:assert";
import { homedir } from "node:os";
import { test } from "node:test";
import type { App } from "../src/app.js";
import {
	choice,
	choiceFlag,
	createApp,
	defineReadOnlyCommand,
	flag,
	provided,
	t,
} from "../src/index.js";
import {
	buildInfraAccess,
	expandTilde,
	isInfraRootPath,
	relativeToRoot,
	resolveInfraRootPath,
	validateFlagInfraMarker,
} from "../src/infra.js";
import { dumpSchemaCore } from "../src/schema.js";

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

// --- Marker factory ---

test("infra: relativeToRoot mints a branded, frozen marker", () => {
	const m = relativeToRoot("MYAPP_HOME", "db.sqlite");
	assert.equal(isInfraRootPath(m), true);
	assert.equal(m.envVar, "MYAPP_HOME");
	assert.deepEqual([...m.parts], ["db.sqlite"]);
	assert.equal(Object.isFrozen(m), true);
	// Hand-forged structural copies are never recognized (mint set, no shape
	// detection -- the outcome() branding pattern).
	assert.equal(
		isInfraRootPath({ envVar: "MYAPP_HOME", parts: ["db.sqlite"] }),
		false,
	);
	assert.equal(isInfraRootPath(null), false);
	assert.equal(isInfraRootPath("MYAPP_HOME"), false);
});

test("infra: marker toString is the Python repr, including the empty-parts quirk", () => {
	// Captured from Python: repr(RelativeToRoot('E')) keeps the trailing ", ".
	assert.equal(String(relativeToRoot("E")), "RelativeToRoot('E', )");
	assert.equal(
		String(relativeToRoot("E", "aa", "bb")),
		"RelativeToRoot('E', 'aa', 'bb')",
	);
});

// --- Helpers ---

test("infra: expandTilde expands ~ and ~/ only", () => {
	assert.equal(expandTilde("~"), homedir());
	assert.equal(expandTilde("~/data"), `${homedir()}/data`);
	assert.equal(expandTilde("/opt/data"), "/opt/data");
	assert.equal(expandTilde("x~y"), "x~y");
});

test("infra: resolveInfraRootPath joins parts and rejects undeclared roots", () => {
	const roots = new Map([["MYAPP_HOME", "/opt/data"]]);
	assert.equal(
		resolveInfraRootPath(relativeToRoot("MYAPP_HOME", "db.sqlite"), roots),
		"/opt/data/db.sqlite",
	);
	assert.equal(
		resolveInfraRootPath(relativeToRoot("MYAPP_HOME"), roots),
		"/opt/data",
	);
	assert.throws(() => resolveInfraRootPath(relativeToRoot("NOPE"), roots), {
		message:
			'RelativeToRoot references undeclared infra root "NOPE"; declare it as an infra root',
	});
});

test("infra: buildInfraAccess snapshots roots and handshake names, null when empty", () => {
	assert.equal(buildInfraAccess(new Map(), new Map(), new Map(), false), null);
	const access = buildInfraAccess(
		new Map([["ROOT", "/r"]]),
		new Map([["HS", "help text"]]),
		new Map(),
		false,
	);
	assert.notEqual(access, null);
	assert.deepEqual([...(access?.roots ?? new Map())], [["ROOT", "/r"]]);
	assert.deepEqual([...(access?.handshakes ?? new Set())], ["HS"]);
});

// --- Eager root resolution at construction ---

function infraApp() {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "Test infra root resolution",
		infraRoot: { MYAPP_HOME: "/var/lib/myapp" },
	});
	app.command(
		defineReadOnlyCommand("run", {
			help: "Run it",
			flags: {
				db: flag("db", t.str, {
					help: "Database path",
					presence: "default",
					default: relativeToRoot("MYAPP_HOME", "db.sqlite"),
				}),
			},
			handler: (args, ctx) => {
				ctx.info(`${ctx.source("db")}:${args.db}`);
				return 0;
			},
		}),
	);
	return app;
}

test("infra: root resolves from the env var at construction, not at parse", async () => {
	const app = await withEnv({ MYAPP_HOME: "/opt/data" }, () => infraApp());
	// Construction captured the value; a later env change must not matter.
	await withEnv({ MYAPP_HOME: "/changed/later" }, async () => {
		const r = await app.test(["run"]);
		assert.equal(r.exitCode, 0);
		assert.equal(r.stdout, "infra:/opt/data/db.sqlite\n");
	});
});

test("infra: root env value gets ~ expanded", async () => {
	const app = await withEnv({ MYAPP_HOME: "~/data" }, () => infraApp());
	const r = await app.test(["run"]);
	assert.equal(r.stdout, `infra:${homedir()}/data/db.sqlite\n`);
});

test("infra: default path gets ~ expanded when the env var is unset", async () => {
	const app = await withEnv({ SCRATCH_HOME_X: undefined }, () =>
		createApp({
			name: "myapp",
			version: "1.0.0",
			help: "tt",
			infraRoot: { SCRATCH_HOME_X: "~/scratch" },
		}),
	);
	app.command(
		defineReadOnlyCommand("show", {
			help: "show",
			handler: (_args, ctx) => {
				const [value, isSet] = ctx.infraValue("SCRATCH_HOME_X");
				ctx.info(`${value}:${isSet}`);
				return 0;
			},
		}),
	);
	const r = await app.test(["show"]);
	assert.equal(r.stdout, `${homedir()}/scratch:true\n`);
});

// --- The four infra_env.json conformance cases, at unit level ---

test("infra_env case: flag default resolves through root (env set), source is 'infra'", async () => {
	await withEnv({ MYAPP_HOME: "/opt/data" }, async () => {
		const r = await infraApp().test(["run"]);
		assert.equal(r.exitCode, 0);
		assert.equal(r.stdout, "infra:/opt/data/db.sqlite\n");
	});
});

test("infra_env case: root unset -> declared default resolves, source is 'infra'", async () => {
	await withEnv({ MYAPP_HOME: undefined }, async () => {
		const r = await infraApp().test(["run"]);
		assert.equal(r.exitCode, 0);
		assert.equal(r.stdout, "infra:/var/lib/myapp/db.sqlite\n");
	});
});

test("infra_env case: hermetic still resolves the marker (root has no argv dependency)", async () => {
	await withEnv({ MYAPP_HOME: "/opt/data" }, async () => {
		const r = await infraApp().test(["--hermetic", "run"]);
		assert.equal(r.exitCode, 0);
		assert.equal(r.stdout, "infra:/opt/data/db.sqlite\n");
	});
});

test("infra_env case: CLI value overrides the marker default, source is 'cli'", async () => {
	await withEnv({ MYAPP_HOME: "/opt/data" }, async () => {
		const r = await infraApp().test(["run", "--db", "/tmp/custom.db"]);
		assert.equal(r.exitCode, 0);
		assert.equal(r.stdout, "cli:/tmp/custom.db\n");
	});
});

// --- Hermetic interplay with per-flag env vars (hermetic.json semantics) ---

test("infra: hermetic suppresses the flag's env var but not the marker default", async () => {
	const build = () => {
		const app = createApp({
			name: "myapp",
			version: "1.0.0",
			help: "tt",
			infraRoot: { MYAPP_HOME: "/var/lib/myapp" },
		});
		app.command(
			defineReadOnlyCommand("run", {
				help: "run",
				flags: {
					db: flag("db", t.str, {
						help: "db",
						env: "MYAPP_DB",
						presence: "default",
						default: relativeToRoot("MYAPP_HOME", "db.sqlite"),
					}),
				},
				handler: (args, ctx) => {
					ctx.info(`${ctx.source("db")}:${args.db}`);
					return 0;
				},
			}),
		);
		return app;
	};
	await withEnv(
		{ MYAPP_HOME: "/opt/data", MYAPP_DB: "/from/env.db" },
		async () => {
			const plain = await build().test(["run"]);
			assert.equal(plain.stdout, "env:/from/env.db\n");
			const hermetic = await build().test(["--hermetic", "run"]);
			assert.equal(hermetic.stdout, "infra:/opt/data/db.sqlite\n");
		},
	);
});

// --- Global-flag marker defaults ---

test("infra: global flag marker default resolves with source 'infra'", async () => {
	const app = await withEnv({ MYAPP_HOME: undefined }, () => {
		const a = createApp({
			name: "myapp",
			version: "1.0.0",
			help: "tt",
			infraRoot: { MYAPP_HOME: "/var/lib/myapp" },
			flags: {
				state_dir: flag("state-dir", t.str, {
					help: "State directory",
					presence: "default",
					default: relativeToRoot("MYAPP_HOME", "state"),
				}),
			},
		});
		a.command(
			defineReadOnlyCommand("run", {
				help: "run",
				handler: (args, ctx) => {
					const globals = args as Record<string, unknown>;
					ctx.info(`${ctx.source("state-dir")}:${globals.state_dir}`);
					return 0;
				},
			}),
		);
		return a;
	});
	const byDefault = await app.test(["run"]);
	assert.equal(byDefault.stdout, "infra:/var/lib/myapp/state\n");
	const byCli = await app.test(["--state-dir", "/x", "run"]);
	assert.equal(byCli.stdout, "cli:/x\n");
});

// --- Handshake env vars ---

test("infra: handshake values are read live, roots stay captured", async () => {
	const app = await withEnv(
		{ MYAPP_HOME: "/opt/data", MYAPP_ORCHESTRATED: undefined },
		() =>
			createApp({
				name: "myapp",
				version: "1.0.0",
				help: "tt",
				infraRoot: { MYAPP_HOME: "/var/lib/myapp" },
				handshakeEnv: { MYAPP_ORCHESTRATED: "set by the orchestrator" },
			}),
	);
	app.command(
		defineReadOnlyCommand("show", {
			help: "show",
			handler: (_args, ctx) => {
				const [hs, hsSet] = ctx.infraValue("MYAPP_ORCHESTRATED");
				const [root, rootSet] = ctx.infraValue("MYAPP_HOME");
				ctx.info(`hs=${hs},${hsSet} root=${root},${rootSet}`);
				return 0;
			},
		}),
	);
	// Unset at call time -> [undefined, false], even though the app was
	// constructed while other vars were set.
	await withEnv({ MYAPP_ORCHESTRATED: undefined }, async () => {
		const r = await app.test(["show"]);
		assert.equal(r.stdout, "hs=undefined,false root=/opt/data,true\n");
	});
	// Set at call time -> live value; the root remains the captured one.
	await withEnv(
		{ MYAPP_ORCHESTRATED: "1", MYAPP_HOME: "/changed" },
		async () => {
			const r = await app.test(["show"]);
			assert.equal(r.stdout, "hs=1,true root=/opt/data,true\n");
		},
	);
});

test("infra: infraValue on an undeclared var throws the sibling message", async () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "tt",
		infraRoot: { MYAPP_HOME: "/var/lib/myapp" },
	});
	app.command(
		defineReadOnlyCommand("show", {
			help: "show",
			handler: (_args, ctx) => {
				try {
					ctx.infraValue("OTHER");
				} catch (e) {
					ctx.info((e as Error).message);
				}
				return 0;
			},
		}),
	);
	const r = await app.test(["show"]);
	assert.equal(
		r.stdout,
		'InfraValue: "OTHER" is not a declared infra root, handshake, or connection env var\n',
	);
});

// --- Registration-time validation ---

test("infra: handshake help must be a non-empty string", () => {
	assert.throws(
		() =>
			createApp({
				name: "myapp",
				version: "1.0.0",
				help: "tt",
				handshakeEnv: { MYAPP_ORCHESTRATED: "   " },
			}),
		{
			message:
				'handshake env var "MYAPP_ORCHESTRATED": help must be a non-empty string',
		},
	);
});

test("infra: handshake var colliding with a declared root is rejected", () => {
	assert.throws(
		() =>
			createApp({
				name: "myapp",
				version: "1.0.0",
				help: "tt",
				infraRoot: { MYAPP_HOME: "/var/lib/myapp" },
				handshakeEnv: { MYAPP_HOME: "also a handshake" },
			}),
		{
			message:
				'handshake env var "MYAPP_HOME" is already declared as an infra root',
		},
	);
});

test("infra: global flag marker referencing an undeclared root is a hard error", () => {
	assert.throws(
		() =>
			createApp({
				name: "myapp",
				version: "1.0.0",
				help: "tt",
				flags: {
					db: flag("db", t.str, {
						help: "db",
						presence: "default",
						default: relativeToRoot("NOPE", "db.sqlite"),
					}),
				},
			}),
		{
			message:
				'flag "db": RelativeToRoot references undeclared infra root "NOPE"; declare it as an infra root',
		},
	);
});

test("infra: command flag marker referencing an undeclared root is a hard error", () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "tt" });
	const cmd = defineReadOnlyCommand("run", {
		help: "run",
		flags: {
			db: flag("db", t.str, {
				help: "db",
				presence: "default",
				default: relativeToRoot("NOPE"),
			}),
		},
		handler: () => 0,
	});
	// Python's command-scoped message (the divergence ground truth).
	assert.throws(() => app.command(cmd), {
		message:
			'command "run": flag "db": RelativeToRoot references undeclared infra root "NOPE"; declare it as an infra root',
	});
});

test("infra: group-nested command flag markers are validated too", () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "tt" });
	const grp = app.group("db", { help: "db group" });
	assert.throws(
		() =>
			grp.command(
				defineReadOnlyCommand("init", {
					help: "init",
					flags: {
						path: flag("path", t.str, {
							help: "path",
							presence: "default",
							default: relativeToRoot("NOPE"),
						}),
					},
					handler: () => 0,
				}),
			),
		{
			message:
				'command "init": flag "path": RelativeToRoot references undeclared infra root "NOPE"; declare it as an infra root',
		},
	);
});

test("infra: declared markers register cleanly at every level", () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "tt",
		infraRoot: { MYAPP_HOME: "/var/lib/myapp" },
		flags: {
			state_dir: flag("state-dir", t.str, {
				help: "state",
				presence: "default",
				default: relativeToRoot("MYAPP_HOME", "state"),
			}),
		},
	});
	const grp = app.group("db", { help: "db group" });
	grp.command(
		defineReadOnlyCommand("init", {
			help: "init",
			flags: {
				path: flag("path", t.str, {
					help: "path",
					presence: "default",
					default: relativeToRoot("MYAPP_HOME", "db.sqlite"),
				}),
			},
			handler: () => 0,
		}),
	);
});

test("infra: validateFlagInfraMarker ignores non-marker defaults", () => {
	const roots = new Map<string, string>();
	validateFlagInfraMarker(
		flag("plain", t.str, { help: "pp", presence: "default", default: "xx" }),
		roots,
	);
	validateFlagInfraMarker(
		flag("opt", t.str, { help: "oo", presence: "optional" }),
		roots,
	);
	validateFlagInfraMarker(
		flag("req", t.str, { help: "rr", presence: "required" }),
		roots,
	);
});

test("infra: marker on an int flag is rejected like Python (type mismatch)", () => {
	assert.throws(
		() =>
			flag("nn", t.int, {
				help: "nn",
				presence: "default",
				default: relativeToRoot("MYAPP_HOME"),
			}),
		{
			message: `Flag "nn": type=int requires an int default, got 'RelativeToRoot'`,
		},
	);
});

test("infra: marker default vs choices renders the Python repr", () => {
	// Captured from Python: the marker repr (with the trailing ", " for empty
	// parts) lands inside the choices-mismatch message.
	assert.throws(
		() =>
			flag("cc", t.str, {
				help: "cc",
				choices: [{ value: "aa" }, { value: "bb" }],
				presence: "default",
				default: relativeToRoot("MYAPP_HOME"),
			}),
		{
			message: `Flag "cc": default RelativeToRoot('MYAPP_HOME', ) is not in choices ['aa', 'bb']`,
		},
	);
});

// --- Help rendering ---

test("infra: app help renders the Infrastructure section (Go byte parity)", async () => {
	// Byte-captured from the Go implementation. SCRATCH_HOME_X is set in the
	// environment to prove help shows the DECLARED default, not the resolved
	// value, and that the annotation line documents hermetic immunity.
	const app = await withEnv({ SCRATCH_HOME_X: "/opt/data" }, () => {
		const a = createApp({
			name: "myapp",
			version: "1.0.0",
			help: "Test infra help",
			infraRoot: {
				MYAPP_HOME: "/var/lib/myapp",
				SCRATCH_HOME_X: "~/scratch",
			},
			handshakeEnv: { MYAPP_ORCHESTRATED: "set by the orchestrator" },
		});
		a.command(
			defineReadOnlyCommand("run", { help: "Run it", handler: () => 0 }),
		);
		return a;
	});
	const r = await app.test(["--help"]);
	assert.equal(r.exitCode, 0);
	assert.equal(
		r.stdout,
		"myapp v1.0.0 -- Test infra help\n" +
			"\n" +
			"Commands:\n" +
			"  run    Run it\n" +
			"\n" +
			"Infrastructure:\n" +
			"  (location/handshake env vars; not suppressed by --hermetic)\n" +
			"  MYAPP_HOME            root (default: /var/lib/myapp)\n" +
			"  SCRATCH_HOME_X        root (default: ~/scratch)\n" +
			"  MYAPP_ORCHESTRATED    set by the orchestrator\n" +
			"\n" +
			"Use 'myapp help <command>' for more information.\n",
	);
});

test("infra: command help renders the marker default as the Python repr", async () => {
	const app = await withEnv({ MYAPP_HOME: undefined }, () => infraApp());
	const r = await app.test(["run", "--help"]);
	assert.equal(r.exitCode, 0);
	// Byte-captured from the Python implementation.
	assert.equal(
		r.stdout,
		"myapp run -- Run it\n" +
			"\n" +
			"Flags:\n" +
			"  --db <str>    Database path [default: RelativeToRoot('MYAPP_HOME', 'db.sqlite')]\n",
	);
});

// --- A marker default INSIDE an elected scope (contract §24.6, §23) ---

/**
 * A selector whose elected scope declares a `RelativeToRoot` default. The
 * declaration means the same thing one level down as it does at root, so the
 * scope must deliver the RESOLVED path -- never the opaque marker, which no
 * command line can produce and no handler can read.
 */
function scopedInfraApp(): App {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "tt",
		infraRoot: { MYAPP_HOME: "/var/lib/myapp" },
	});
	app.command(
		defineReadOnlyCommand("send", {
			help: "send",
			flags: {
				via: choiceFlag(
					"via",
					{
						email: choice({
							help: "email",
							flags: {
								subject: flag("subject", t.str, {
									help: "the subject",
									presence: "required",
								}),
								store: flag("store", t.str, {
									help: "where the copy goes",
									presence: "default",
									default: relativeToRoot("MYAPP_HOME", "mail", "outbox"),
								}),
							},
						}),
						sms: choice({ help: "sms" }),
					},
					{ help: "delivery channel", presence: "required" },
				),
			},
			handler: (args, ctx) => {
				if (args.via.choice === "email") {
					ctx.info(`${args.via.store}:${provided(args.via, "store")}`);
				}
				return 0;
			},
		}),
	);
	return app;
}

test("infra: a scoped marker default resolves through the declared root", async () => {
	await withEnv({ MYAPP_HOME: "/opt/data" }, async () => {
		const r = await scopedInfraApp().test([
			"send",
			"--via",
			"email",
			"--subject",
			"hi",
		]);
		assert.equal(r.exitCode, 0);
		// The resolved path, and NOT provided: the declaration decided it, which
		// is what the "infra" source label means (§23.6).
		assert.equal(r.stdout, "/opt/data/mail/outbox:false\n");
	});
});

test("infra: the root is unset, so the scoped marker takes the declared path", async () => {
	await withEnv({ MYAPP_HOME: undefined }, async () => {
		const r = await scopedInfraApp().test([
			"send",
			"--via",
			"email",
			"--subject",
			"hi",
		]);
		assert.equal(r.stdout, "/var/lib/myapp/mail/outbox:false\n");
	});
});

test("infra: hermetic resolves a scoped marker too (no argv dependency)", async () => {
	await withEnv({ MYAPP_HOME: "/opt/data" }, async () => {
		const r = await scopedInfraApp().test([
			"--hermetic",
			"send",
			"--via",
			"email",
			"--subject",
			"hi",
		]);
		assert.equal(r.stdout, "/opt/data/mail/outbox:false\n");
	});
});

test("infra: a scoped marker resolves at BOTH programmatic front doors", async () => {
	await withEnv({ MYAPP_HOME: "/opt/data" }, async () => {
		// The record door: call() takes the elected record pre-typed, and the
		// fields it does not carry come from the declaration -- resolved.
		let seen: unknown;
		const build = (): App => {
			const app = createApp({
				name: "myapp",
				version: "1.0.0",
				help: "tt",
				infraRoot: { MYAPP_HOME: "/var/lib/myapp" },
			});
			app.command(
				defineReadOnlyCommand("send", {
					help: "send",
					flags: {
						via: choiceFlag(
							"via",
							{
								email: choice({
									help: "email",
									flags: {
										subject: flag("subject", t.str, {
											help: "the subject",
											presence: "required",
										}),
										store: flag("store", t.str, {
											help: "where the copy goes",
											presence: "default",
											default: relativeToRoot("MYAPP_HOME", "mail", "outbox"),
										}),
									},
								}),
								sms: choice({ help: "sms" }),
							},
							{ help: "delivery channel", presence: "required" },
						),
					},
					handler: (args) => {
						seen = args.via.choice === "email" ? args.via.store : undefined;
						return 0;
					},
				}),
			);
			return app;
		};
		await build().call("send", { via: { choice: "email", subject: "hi" } });
		assert.equal(seen, "/opt/data/mail/outbox");
		// The flat machine door reaches the same declaration through the same
		// conversion, so it delivers the same resolved path.
		seen = undefined;
		const send = build()
			.asTools()
			.find((x) => x.name === "send");
		assert.ok(send);
		await send.execute({ via: "email", subject: "hi" });
		assert.equal(seen, "/opt/data/mail/outbox");
	});
});

// --- A marker inside a DEFAULTED selection (contract §24.5, §18.26 item 256) ---

/**
 * §24.5 says a defaulted selection is COMPLETE and delivered as declared, and
 * "as declared" means the declaration's SEMANTICS, never its raw objects: a
 * `RelativeToRoot` sitting inside the selection a selector's own `default`
 * names is the same declared default one frame further in, so it resolves at
 * delivery -- at every door and at every depth, with `provided` false.
 *
 * Handing the marker object over instead would deliver a handler something no
 * command line can produce, and something the identical declaration one
 * presence away already resolves.
 *
 * The selector's default NAMES A CHOICE here, so nothing holds a pre-built
 * instance: the scope is rebuilt from its declarations and every field runs the
 * ordinary default path (electDefaultRecord at the programmatic doors, the
 * scoped default applier on the argv path).
 */
function defaultedSelectionInfraApp(seen?: Record<string, unknown>): App {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "tt",
		infraRoot: { MYAPP_HOME: "/var/lib/myapp" },
	});
	app.command(
		defineReadOnlyCommand("send", {
			help: "send",
			flags: {
				via: choiceFlag(
					"via",
					{
						email: choice({
							help: "email",
							flags: {
								store: flag("store", t.str, {
									help: "where the copy goes",
									presence: "default",
									default: relativeToRoot("MYAPP_HOME", "mail", "outbox"),
								}),
								format: choiceFlag(
									"format",
									{
										plain: choice({
											help: "plain text",
											flags: {
												sheet: flag("sheet", t.str, {
													help: "the style sheet",
													presence: "default",
													default: relativeToRoot("MYAPP_HOME", "plain.css"),
												}),
											},
										}),
										rich: choice({ help: "rich text" }),
									},
									{
										help: "the body format",
										presence: "default",
										default: "plain",
									},
								),
							},
						}),
						sms: choice({ help: "sms" }),
					},
					{ help: "delivery channel", presence: "default", default: "email" },
				),
			},
			handler: (args) => {
				if (seen !== undefined && args.via.choice === "email") {
					Object.assign(seen, {
						store: args.via.store,
						storeProvided: provided(args.via, "store"),
						sheet:
							args.via.format.choice === "plain"
								? args.via.format.sheet
								: undefined,
					});
				}
				return 0;
			},
		}),
	);
	return app;
}

test("infra: a defaulted selection's marker resolves on the argv path", async () => {
	await withEnv({ MYAPP_HOME: "/opt/data" }, async () => {
		const seen: Record<string, unknown> = {};
		// Nobody elected anything: the whole selection comes from the
		// declaration, nested selector included.
		const r = await defaultedSelectionInfraApp(seen).test(["send"]);
		assert.equal(r.exitCode, 0);
		assert.equal(seen.store, "/opt/data/mail/outbox");
		// The declaration decided it, which is what "infra" means (§23.6).
		assert.equal(seen.storeProvided, false);
		assert.equal(seen.sheet, "/opt/data/plain.css");
		// The root has no argv dependency, so --hermetic changes nothing.
		const hermetic: Record<string, unknown> = {};
		await defaultedSelectionInfraApp(hermetic).test(["--hermetic", "send"]);
		assert.equal(hermetic.store, "/opt/data/mail/outbox");
		assert.equal(hermetic.sheet, "/opt/data/plain.css");
	});
});

test("infra: a defaulted selection's marker resolves at both programmatic doors", async () => {
	await withEnv({ MYAPP_HOME: "/opt/data" }, async () => {
		// The record door, with the selector omitted entirely.
		const record: Record<string, unknown> = {};
		await defaultedSelectionInfraApp(record).call("send", {});
		assert.equal(record.store, "/opt/data/mail/outbox");
		assert.equal(record.storeProvided, false);
		assert.equal(record.sheet, "/opt/data/plain.css");
		// The record door with the OUTER selection elected by hand: the nested
		// selector is still the declaration's, one frame further in.
		const nested: Record<string, unknown> = {};
		await defaultedSelectionInfraApp(nested).call("send", {
			via: { choice: "email" },
		});
		assert.equal(nested.store, "/opt/data/mail/outbox");
		assert.equal(nested.sheet, "/opt/data/plain.css");
		// The flat machine door.
		const flat: Record<string, unknown> = {};
		const send = defaultedSelectionInfraApp(flat)
			.asTools()
			.find((x) => x.name === "send");
		assert.ok(send);
		await send.execute({});
		assert.equal(flat.store, "/opt/data/mail/outbox");
		assert.equal(flat.sheet, "/opt/data/plain.css");
	});
});

test("infra: the root is unset, so a defaulted selection takes the declared path", async () => {
	await withEnv({ MYAPP_HOME: undefined }, async () => {
		const seen: Record<string, unknown> = {};
		await defaultedSelectionInfraApp(seen).test(["send"]);
		assert.equal(seen.store, "/var/lib/myapp/mail/outbox");
		const record: Record<string, unknown> = {};
		await defaultedSelectionInfraApp(record).call("send", {});
		assert.equal(record.store, "/var/lib/myapp/mail/outbox");
	});
});

test("infra: the schema publishes the marker, never its resolution", async () => {
	// The schema is the other direction and stays there (§25.10): a dump is a
	// property of the DECLARATION where a delivery is a property of the run.
	await withEnv({ MYAPP_HOME: "/opt/data" }, () => {
		const schema = dumpSchemaCore(
			defaultedSelectionInfraApp() as never,
		) as unknown as {
			commands: Record<string, { flags: { name: string; default: unknown }[] }>;
		};
		const send = schema.commands.send;
		assert.ok(send);
		const via = send.flags.find((f) => f.name === "via");
		assert.ok(via);
		assert.deepEqual(via.default, {
			choice: "email",
			store: {
				relative_to_root: { env_var: "MYAPP_HOME", parts: ["mail", "outbox"] },
			},
		});
	});
});

// --- An undeclared root inside a scope names WHERE (§12.13, §18.26) ---

/**
 * Registration never looks inside a scope, so a marker naming a root the app
 * does not declare is refused where it RESOLVES -- at delivery, one level down.
 * The refusal is the marker's own sentence plus §12.13's suffix saying where
 * the declaration sits, and the suffix is composed exactly as every other
 * scoped refusal composes it: the scope path, then the origin clause naming an
 * election the reader cannot see in their own command line.
 *
 * Every door reaches the declaration through ONE seam, so every door says it:
 * the command line, `call()`'s record, and the flat machine form that converts
 * into that record.
 *
 * `--format` nests a second marker one level deeper, so the same states can be
 * asked at depth, where the suffix names the whole path.
 */
function undeclaredRootScopeApp(defaulted: boolean): App {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "tt",
		infraRoot: { MYAPP_HOME: "/var/lib/myapp" },
	});
	app.command(
		defineReadOnlyCommand("send", {
			help: "send",
			flags: {
				via: choiceFlag(
					"via",
					{
						email: choice({
							help: "email",
							flags: {
								cfg: flag("cfg", t.str, {
									help: "the config file",
									presence: "default",
									default: relativeToRoot("NOPE", "x.toml"),
								}),
							},
						}),
						sms: choice({
							help: "sms",
							flags: {
								format: choiceFlag(
									"format",
									{
										plain: choice({
											help: "plain text",
											flags: {
												sheet: flag("sheet", t.str, {
													help: "the style sheet",
													presence: "default",
													default: relativeToRoot("ALSO-NOPE", "plain.css"),
												}),
											},
										}),
										rich: choice({ help: "rich text" }),
									},
									{
										help: "the body format",
										presence: "default",
										default: "plain",
									},
								),
							},
						}),
					},
					defaulted
						? {
								help: "delivery channel",
								presence: "default",
								default: "email",
							}
						: { help: "delivery channel", presence: "required" },
				),
			},
			handler: () => 0,
		}),
	);
	return app;
}

const undeclaredRoot =
	'RelativeToRoot references undeclared infra root "NOPE"; declare it as an infra root';
const undeclaredNestedRoot =
	'RelativeToRoot references undeclared infra root "ALSO-NOPE"; declare it as an infra root';

/** The refusal one promise produced, or a failure when it was accepted. */
async function refusalOf(p: Promise<unknown>): Promise<string> {
	try {
		await p;
	} catch (e) {
		return (e as Error).message;
	}
	throw new Error("the call was accepted; want a refusal");
}

function toolNamed(app: App, name: string) {
	const found = app.asTools().find((x) => x.name === name);
	assert.ok(found, `no tool named ${name}`);
	return found;
}

test("infra: a scoped undeclared root carries the scope suffix at every door", async () => {
	const want = `${undeclaredRoot} under '--via email'`;
	// The command line, where the election was typed: the scope path and no
	// origin clause, because there is no ambient cause to name.
	assert.equal(
		(await undeclaredRootScopeApp(false).test(["send", "--via", "email"]))
			.stderr,
		`error: ${want}\ntry 'myapp send --help'\n`,
	);
	// The record door, whose caller elected the same choice by hand.
	assert.equal(
		await refusalOf(
			undeclaredRootScopeApp(false).call("send", { via: { choice: "email" } }),
		),
		want,
	);
	// The flat machine form converts into that record and reaches the same seam.
	assert.equal(
		await refusalOf(
			toolNamed(undeclaredRootScopeApp(false), "send").execute({
				via: "email",
			}),
		),
		want,
	);
});

test("infra: a defaulted selection's undeclared root names the origin too", async () => {
	// Nobody elected anything: the declaration did, and the origin clause says
	// so -- the ambient cause a reader cannot see in their own command line.
	const want = `${undeclaredRoot} under '--via email' (elected by default)`;
	assert.equal(
		(await undeclaredRootScopeApp(true).test(["send"])).stderr,
		`error: ${want}\ntry 'myapp send --help'\n`,
	);
	assert.equal(
		await refusalOf(undeclaredRootScopeApp(true).call("send", {})),
		want,
	);
	// The flat machine form materializes the record the declaration's default
	// names before call() sees anything -- but the election is still the
	// DECLARATION's, so the clause composes here exactly as it does at the other
	// two doors (§18.28 items 263 and 264). A conversion is not a caller.
	assert.equal(
		await refusalOf(
			toolNamed(undeclaredRootScopeApp(true), "send").execute({}),
		),
		want,
	);
});

test("infra: the suffix names the WHOLE path at depth", async () => {
	// Two levels down, with the outer election typed and the inner one the
	// declaration's: the path names both, and the origin clause names the
	// election that was not typed.
	const want = `${undeclaredNestedRoot} under '--via sms --format plain' (elected by default)`;
	assert.equal(
		(await undeclaredRootScopeApp(false).test(["send", "--via", "sms"])).stderr,
		`error: ${want}\ntry 'myapp send --help'\n`,
	);
	assert.equal(
		await refusalOf(
			undeclaredRootScopeApp(false).call("send", { via: { choice: "sms" } }),
		),
		want,
	);
	// Both elections typed by the caller: the path is the same and the origin
	// clause is empty, because nothing ambient decided.
	const typed = `${undeclaredNestedRoot} under '--via sms --format plain'`;
	assert.equal(
		(
			await undeclaredRootScopeApp(false).test([
				"send",
				"--via",
				"sms",
				"--format",
				"plain",
			])
		).stderr,
		`error: ${typed}\ntry 'myapp send --help'\n`,
	);
	assert.equal(
		await refusalOf(
			undeclaredRootScopeApp(false).call("send", {
				via: { choice: "sms", format: { choice: "plain" } },
			}),
		),
		typed,
	);
	// The flat door, with the outer election the caller's and the INNER one the
	// declaration's: the clause names the inner election, which is the outermost
	// one no caller made (§18.19 item 216, §18.28 item 264).
	assert.equal(
		await refusalOf(
			toolNamed(undeclaredRootScopeApp(false), "send").execute({ via: "sms" }),
		),
		want,
	);
	// Both elections the caller's at that door too: no clause.
	assert.equal(
		await refusalOf(
			toolNamed(undeclaredRootScopeApp(false), "send").execute({
				via: "sms",
				format: "plain",
			}),
		),
		typed,
	);
});
