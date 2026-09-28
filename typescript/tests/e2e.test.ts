/**
 * End-to-end byte-parity tests: the 12 curated scenarios from docs/history/_ts-port-spec.md
 * ("Curated example set for end-to-end byte-parity demos"), each run through
 * app.test() and asserted byte-identical to ground truth.
 *
 * GROUND TRUTH DERIVATION: every expected stdout/stderr/exit_code below was
 * captured on 2026-07-19 by RUNNING the Python implementation -- a mirror app
 * per scenario (matching the conformance case app definition) executed via
 * `uv run python` against python/strictcli, calling app.test(argv) and
 * recording the exact Result bytes (scratchpad capture_e2e.py). The embedded
 * strings are those bytes verbatim; they also satisfy the conformance
 * expectations of the source cases.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import {
	createApp,
	defineReadOnlyCommand,
	deprecated,
	flag,
	outcome,
	readOnlyPassthrough,
	t,
} from "../src/index.js";
import { envelopePayloadText } from "./envelope_helpers.js";

interface Expected {
	readonly stdout: string;
	readonly stderr: string;
	readonly exitCode: number;
}

async function expectBytes(
	app: {
		test(
			argv: readonly string[],
		): Promise<{ stdout: string; stderr: string; exitCode: number }>;
	},
	argv: readonly string[],
	expected: Expected,
): Promise<void> {
	const r = await app.test(argv);
	assert.equal(r.stdout, expected.stdout);
	assert.equal(r.stderr, expected.stderr);
	assert.equal(r.exitCode, expected.exitCode);
}

// 1. Help output (help.json: "help: app help shows version and commands")
test("e2e 1: app help", async () => {
	const app = createApp({
		name: "myapp",
		version: "3.0.0",
		help: "my cool app",
	});
	app.command(
		defineReadOnlyCommand("run", {
			help: "run something",
			handler: (_args, ctx) => {
				ctx.info("ok");
				return 0;
			},
		}),
	);
	app.command(
		defineReadOnlyCommand("test", {
			help: "run tests",
			handler: (_args, ctx) => {
				ctx.info("ok");
				return 0;
			},
		}),
	);
	await expectBytes(app, [], {
		stdout:
			"myapp v3.0.0 -- my cool app\n\nCommands:\n  run     run something\n  test    run tests\n\nUse 'myapp help <command>' for more information.\n",
		stderr: "",
		exitCode: 0,
	});
});

// 2. Flag parse success (flags.json: "flags: str flag with space syntax")
test("e2e 2: str flag with space syntax", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					presence: "required",
				}),
			},
			handler: (args, ctx) => {
				ctx.info(`target=${args.target}`);
				return 0;
			},
		}),
	);
	await expectBytes(app, ["cmd", "--target", "foo"], {
		stdout: "target=foo\n",
		stderr: "",
		exitCode: 0,
	});
});

// 3. Parse error with try-help trailer (errors.json: "errors: unknown flag")
test("e2e 3: unknown flag", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				chatter: flag("chatter", t.bool, {
					help: "be chatter",
					presence: "default",
					default: false,
				}),
			},
			handler: (_args, ctx) => {
				ctx.info("ok");
				return 0;
			},
		}),
	);
	await expectBytes(app, ["cmd", "--unknown"], {
		stdout: "",
		stderr: "error: unknown flag '--unknown'\ntry 'myapp cmd --help'\n",
		exitCode: 1,
	});
});

// 4. Choices error (choices.json: "choices: invalid str choice rejected")
test("e2e 4: invalid choice", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				format: flag("format", t.str, {
					help: "output format",
					choices: [{ value: "text" }, { value: "json" }],
					presence: "required",
				}),
			},
			handler: (args, ctx) => {
				ctx.info(`format=${args.format}`);
				return 0;
			},
		}),
	);
	await expectBytes(app, ["cmd", "--format", "xml"], {
		stdout: "",
		stderr:
			"error: --format: invalid value 'xml', must be one of: text, json\ntry 'myapp cmd --help'\n",
		exitCode: 1,
	});
});

// 5. Required-flag error (flags.json: "flags: required str flag missing")
test("e2e 5: required flag missing", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					presence: "required",
				}),
			},
			handler: (args, ctx) => {
				ctx.info(`target=${args.target}`);
				return 0;
			},
		}),
	);
	await expectBytes(app, ["cmd"], {
		stdout: "",
		stderr: "error: flag '--target' is required\ntry 'myapp cmd --help'\n",
		exitCode: 1,
	});
});

// 6. Negation (flags.json: "flags: bool flag --no-X negation")
test("e2e 6: bool negation", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				chatter: flag("chatter", t.bool, {
					help: "be chatter",
					presence: "default",
					default: false,
				}),
			},
			handler: (args, ctx) => {
				ctx.info(`chatter=${args.chatter ? "true" : "false"}`);
				return 0;
			},
		}),
	);
	await expectBytes(app, ["cmd", "--no-chatter"], {
		stdout: "chatter=false\n",
		stderr: "",
		exitCode: 0,
	});
});

// 7. Env resolution (env.json: "env: str flag from env var")
test("e2e 7: str flag from env var", async () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
		envPrefix: "MYAPP",
	});
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					presence: "default",
					default: "fallback",
					env: "MYAPP_TARGET",
				}),
			},
			handler: (args, ctx) => {
				ctx.info(`target=${args.target}`);
				return 0;
			},
		}),
	);
	const prev = process.env.MYAPP_TARGET;
	process.env.MYAPP_TARGET = "from-env";
	try {
		await expectBytes(app, ["cmd"], {
			stdout: "target=from-env\n",
			stderr: "",
			exitCode: 0,
		});
	} finally {
		if (prev === undefined) {
			delete process.env.MYAPP_TARGET;
		} else {
			process.env.MYAPP_TARGET = prev;
		}
	}
});

// 8. Unknown command (basic.json: "basic: unknown command error")
test("e2e 8: unknown command", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("greet", {
			help: "say hello",
			handler: (_args, ctx) => {
				ctx.info("hello");
				return 0;
			},
		}),
	);
	await expectBytes(app, ["deploy"], {
		stdout: "",
		stderr: "error: unknown command 'deploy'\ntry 'myapp --help'\n",
		exitCode: 1,
	});
});

// 9. Deprecated command (deprecated.json: "deprecated: invoke deprecated
// command prints message and exits 1")
test("e2e 9: deprecated command", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("new-cmd", {
			help: "the replacement command",
			handler: (_args, ctx) => {
				ctx.info("new");
				return 0;
			},
		}),
	);
	app.deprecate(deprecated("old-cmd", "use 'new-cmd' instead"));
	await expectBytes(app, ["old-cmd"], {
		stdout: "",
		stderr:
			"error: command 'old-cmd' is deprecated: use 'new-cmd' instead\ntry 'myapp --help'\n",
		exitCode: 1,
	});
});

// 10. Passthrough (passthrough.json: "passthrough: receives raw args")
test("e2e 10: passthrough receives raw args", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		readOnlyPassthrough("checkout", {
			help: "checkout a branch",
			handler: (pt, ctx) => {
				ctx.info(`${pt.name}:${pt.args.join(",")}`);
				return 0;
			},
		}),
	);
	await expectBytes(app, ["checkout", "-b", "feature"], {
		stdout: "checkout:-b,feature\n",
		stderr: "",
		exitCode: 0,
	});
});

// 11. Version (basic.json: "basic: --version flag")
test("e2e 11: --version", async () => {
	const app = createApp({ name: "myapp", version: "2.5.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("greet", {
			help: "say hello",
			handler: (_args, ctx) => {
				ctx.info("hello");
				return 0;
			},
		}),
	);
	await expectBytes(app, ["--version"], {
		stdout: "myapp 2.5.0\n",
		stderr: "",
		exitCode: 0,
	});
});

// 12. Data-outcome JSON line (outcome_contract.json: "outcome: data-only
// return prints one compact JSON line (byte-identical across languages)")
test("e2e 12: data outcome prints one compact JSON line", async () => {
	const app = createApp({
		name: "outcomeapp",
		version: "1.0.0",
		help: "Test the Outcome data contract",
	});
	app.command(
		defineReadOnlyCommand("run", {
			payloadSchema: {},
			help: "Return a data-only outcome",
			handler: (_args, ctx) => {
				ctx.payload({ count: 3, name: "strictcli" });
				return outcome(0);
			},
		}),
	);
	const r = await app.test(["run", "--json"]);
	assert.equal(
		envelopePayloadText(r.stdout),
		'{"count":3,"name":"strictcli"}\n',
	);
	assert.equal(r.stderr, "");
	assert.equal(r.exitCode, 0);
	assert.deepEqual(r.data, { count: 3, name: "strictcli" });
});
