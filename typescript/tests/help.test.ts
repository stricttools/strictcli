/**
 * Help rendering tests, byte-pinned against the conformance suite: every
 * expected string below is a `stdout_equals` value from conformance/cases/
 * (help.json, nesting.json, env.json, choices.json, int_type.json,
 * repeatable.json, mutex.json, global_flags.json, flag_sets.json,
 * passthrough.json) plus a trailing newline, or -- for app-level sections
 * with no conformance stdout_equals case (Deprecated / Global flags /
 * Infrastructure) -- the exact stdout captured from the Python
 * implementation's app.test() (see scripts noted inline).
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import {
	arg,
	choice,
	createApp,
	defineReadOnlyCommand,
	deprecated,
	flag,
	flagSet,
	memberChoiceFlag,
	readOnlyPassthrough,
	t,
} from "../src/index.js";

const ok = () => undefined;

test("help: app help shows version and commands (help.json)", async () => {
	const app = createApp({
		name: "myapp",
		version: "3.0.0",
		help: "my cool app",
	});
	app.command(
		defineReadOnlyCommand("run", { help: "run something", handler: ok }),
	);
	app.command(
		defineReadOnlyCommand("test", { help: "run tests", handler: ok }),
	);
	const r = await app.test([]);
	assert.equal(
		r.stdout,
		"myapp v3.0.0 -- my cool app\n\nCommands:\n  run     run something\n  test    run tests\n\nUse 'myapp help <command>' for more information.\n",
	);
	assert.equal(r.stderr, "");
	assert.equal(r.exitCode, 0);
});

test("help: command help shows flags and args (help.json)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("deploy", {
			help: "deploy the app",
			args: [
				arg("target", t.str, { help: "deploy target", presence: "required" }),
			],
			flags: {
				sim_run: flag("sim-run", t.bool, {
					help: "preview changes",
					presence: "default",
					default: false,
				}),
			},
			handler: ok,
		}),
	);
	const r = await app.test(["deploy", "--help"]);
	assert.equal(
		r.stdout,
		"myapp deploy -- deploy the app\n\nArguments:\n  target    deploy target [required]\n\nFlags:\n  --sim-run, --no-sim-run    preview changes [default: false]\n",
	);
	assert.equal(r.exitCode, 0);
});

test("help: command help via -h shows only the header (help.json)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("deploy", { help: "deploy the app", handler: ok }),
	);
	const r = await app.test(["deploy", "-h"]);
	assert.equal(r.stdout, "myapp deploy -- deploy the app\n");
});

test("help: str flag shows <str> and default (help.json)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				output: flag("output", t.str, {
					help: "output path",
					presence: "default",
					default: "out.txt",
				}),
			},
			handler: ok,
		}),
	);
	const r = await app.test(["cmd", "--help"]);
	assert.equal(
		r.stdout,
		"myapp cmd -- a command\n\nFlags:\n  --output <str>    output path [default: out.txt]\n",
	);
});

test("help: required flag shown as [required] (help.json)", async () => {
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
			handler: ok,
		}),
	);
	const r = await app.test(["cmd", "--help"]);
	assert.equal(
		r.stdout,
		"myapp cmd -- a command\n\nFlags:\n  --target <str>    the target [required]\n",
	);
});

test("help: explicitly-optional flag shows [optional] (Go Default(nil) fix)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					presence: "optional",
				}),
			},
			handler: ok,
		}),
	);
	const r = await app.test(["cmd", "--help"]);
	assert.equal(
		r.stdout,
		"myapp cmd -- a command\n\nFlags:\n  --target <str>    the target [optional]\n",
	);
});

test("help: app help shows groups (help.json)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	const grp = app.group("config", { help: "manage configuration" });
	grp.command(
		defineReadOnlyCommand("show", { help: "display config", handler: ok }),
	);
	const r = await app.test([]);
	assert.equal(
		r.stdout,
		"myapp v1.0.0 -- test app\n\nGroups:\n  config    manage configuration\n\nUse 'myapp help <command>' for more information.\n",
	);
});

test("help: optional arg shows default / [optional] (help.json)", async () => {
	const withDefault = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
	});
	withDefault.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [
				arg("path", t.str, {
					help: "project dir",
					presence: "default",
					default: ".",
				}),
			],
			handler: ok,
		}),
	);
	assert.equal(
		(await withDefault.test(["cmd", "--help"])).stdout,
		"myapp cmd -- a command\n\nArguments:\n  path    project dir [default: .]\n",
	);

	const noDefault = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
	});
	noDefault.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [arg("path", t.str, { help: "project dir", presence: "optional" })],
			handler: ok,
		}),
	);
	assert.equal(
		(await noDefault.test(["cmd", "--help"])).stdout,
		"myapp cmd -- a command\n\nArguments:\n  path    project dir [optional]\n",
	);
});

test("help: env var and choices metadata (env.json, choices.json)", async () => {
	const envApp = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
		envPrefix: "MYAPP",
	});
	envApp.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					presence: "default",
					default: "x",
					env: "MYAPP_TARGET",
				}),
			},
			handler: ok,
		}),
	);
	assert.equal(
		(await envApp.test(["cmd", "--help"])).stdout,
		"myapp cmd -- a command\n\nFlags:\n  --target <str>    the target [env: MYAPP_TARGET] [default: x]\n",
	);

	const choicesApp = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
	});
	choicesApp.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				format: flag("format", t.str, {
					help: "output format",
					choices: [{ value: "text" }, { value: "json" }],
					presence: "default",
					default: "text",
				}),
			},
			handler: ok,
		}),
	);
	assert.equal(
		(await choicesApp.test(["cmd", "--help"])).stdout,
		"myapp cmd -- a command\n\nFlags:\n  --format <str>    output format [choices: text, json] [default: text]\n",
	);
});

test("help: int flag shows <int> (int_type.json)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				port: flag("port", t.int, {
					help: "the port",
					presence: "default",
					default: 8000n,
				}),
			},
			handler: ok,
		}),
	);
	assert.equal(
		(await app.test(["cmd", "--help"])).stdout,
		"myapp cmd -- a command\n\nFlags:\n  --port <int>    the port [default: 8000]\n",
	);
});

test("help: repeatable list flags show [repeatable] (repeatable.json)", async () => {
	const bare = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	bare.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				tag: flag("tag", t.list(t.str), {
					help: "a tag",
					repeatable: true,
					presence: "default",
					default: [],
				}),
			},
			handler: ok,
		}),
	);
	assert.equal(
		(await bare.test(["cmd", "--help"])).stdout,
		"myapp cmd -- a command\n\nFlags:\n  --tag <str>    a tag [repeatable] [default: []]\n",
	);

	const withDefault = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
	});
	withDefault.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				tag: flag("tag", t.list(t.str), {
					help: "a tag",
					repeatable: true,
					presence: "default",
					default: ["x", "y"],
				}),
			},
			handler: ok,
		}),
	);
	assert.equal(
		(await withDefault.test(["cmd", "--help"])).stdout,
		"myapp cmd -- a command\n\nFlags:\n  --tag <str>    a tag [repeatable] [default: x, y]\n",
	);
});

// A non-empty dict default renders as sorted `key=value` pairs -- the exact
// cross-language form Go's formatDictForDisplay and Python's
// _format_dict_for_display produce, declaration order irrelevant.
test("help: a non-empty dict default renders sorted key=value pairs", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				meta: flag("meta", t.dict(t.int), {
					help: "metadata",
					presence: "default",
					default: new Map([
						["b", 2n],
						["a", 1n],
					]),
				}),
			},
			handler: ok,
		}),
	);
	assert.equal(
		(await app.test(["cmd", "--help"])).stdout,
		"myapp cmd -- a command\n\nFlags:\n  --meta <key=int>    metadata [dict] [default: a=1, b=2]\n",
	);
});

// Bool defaults render lowercase everywhere they render, positionals
// included: the arg line goes through the same presence renderer the flag
// line does.
test("help: bool defaults render lowercase on flags and on positionals", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				debug: flag("debug", t.bool, {
					help: "debug mode",
					presence: "default",
					default: true,
				}),
			},
			args: [
				arg("force", t.bool, {
					help: "force it",
					presence: "default",
					default: false,
				}),
				arg("trace", t.bool, {
					help: "trace it",
					presence: "default",
					default: true,
				}),
			],
			handler: ok,
		}),
	);
	assert.equal(
		(await app.test(["cmd", "--help"])).stdout,
		"myapp cmd -- a command\n\nArguments:\n" +
			"  force    force it [type: bool] [default: false]\n" +
			"  trace    trace it [type: bool] [default: true]\n\nFlags:\n" +
			"  --debug, --no-debug    debug mode [default: true]\n",
	);
});

test("help: a member-spelled selector renders as one indented block", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flags: {
				name: flag("name", t.str, {
					help: "your name",
					presence: "default",
					default: "anon",
				}),
				volume: memberChoiceFlag(
					"volume",
					{
						chatter: choice({ help: "chatter output" }),
						muted: choice({ help: "muted output" }),
					},
					{ help: "output volume", presence: "required" },
				),
			},
			handler: ok,
		}),
	);
	// A member-spelled selector has no token to render, so its LEFT column
	// carries its bare name and its right column carries its help, the
	// `(exactly one of the following)` clause and its presence part, in that
	// order, with the member flags two columns beneath it as ordinary flag
	// lines -- each ending with exactly one presence part of its own, because a
	// member is required once elected. One alignment column spans the whole
	// block (contract §24.10, §18.19 item 215's neighbours).
	assert.equal(
		(await app.test(["cmd", "--help"])).stdout,
		[
			"myapp cmd -- a command",
			"",
			"Flags:",
			"  --name <str>    your name [default: anon]",
			"  volume          output volume (exactly one of the following) [required]",
			"    --chatter     chatter output [required]",
			"    --muted       muted output [required]",
			"",
		].join("\n"),
	);
});

test("help: global flags in command help (global_flags.json)", async () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
		flags: {
			chatter: flag("chatter", t.bool, {
				help: "enable chatter output",
				presence: "default",
				default: false,
			}),
		},
	});
	app.command(defineReadOnlyCommand("cmd", { help: "a command", handler: ok }));
	assert.equal(
		(await app.test(["cmd", "--help"])).stdout,
		"myapp cmd -- a command\n\nGlobal flags:\n  --chatter, --no-chatter    enable chatter output [default: false]\n",
	);
});

test("help: nested command help shows the group prefix (nesting.json)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	const grp = app.group("config", { help: "manage configuration" });
	grp.command(
		defineReadOnlyCommand("set", {
			help: "set a config value",
			flags: {
				key: flag("key", t.str, { help: "config key", presence: "required" }),
				value: flag("value", t.str, {
					help: "config value",
					presence: "required",
				}),
			},
			handler: ok,
		}),
	);
	assert.equal(
		(await app.test(["config", "set", "--help"])).stdout,
		"myapp config set -- set a config value\n\nFlags:\n  --key <str>      config key [required]\n  --value <str>    config value [required]\n",
	);
});

test("help: 3-level nesting (nesting.json)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	const dns = app.group("dns", { help: "manage DNS" });
	dns.command(
		defineReadOnlyCommand("status", { help: "show DNS status", handler: ok }),
	);
	const zone = dns.group("zone", { help: "manage zones" });
	zone.command(
		defineReadOnlyCommand("list", { help: "list all zones", handler: ok }),
	);
	zone.command(
		defineReadOnlyCommand("create", {
			help: "create a zone",
			flags: {
				name: flag("name", t.str, { help: "zone name", presence: "required" }),
			},
			handler: ok,
		}),
	);

	assert.equal(
		(await app.test(["dns", "--help"])).stdout,
		"myapp dns -- manage DNS\n\nCommands:\n  status    show DNS status\n\nGroups:\n  zone    manage zones\n\nUse 'myapp help dns <command>' for more information.\n",
	);
	assert.equal(
		(await app.test(["dns", "zone", "--help"])).stdout,
		"myapp dns zone -- manage zones\n\nCommands:\n  list      list all zones\n  create    create a zone\n\nUse 'myapp help dns zone <command>' for more information.\n",
	);
	assert.equal(
		(await app.test(["dns", "zone", "create", "--help"])).stdout,
		"myapp dns zone create -- create a zone\n\nFlags:\n  --name <str>    zone name [required]\n",
	);
});

test("help: group help lists subcommands (nesting.json)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	const grp = app.group("config", { help: "manage configuration" });
	grp.command(
		defineReadOnlyCommand("show", { help: "display config", handler: ok }),
	);
	grp.command(
		defineReadOnlyCommand("set", { help: "set a config value", handler: ok }),
	);
	const expected =
		"myapp config -- manage configuration\n\nCommands:\n  show    display config\n  set     set a config value\n\nUse 'myapp help config <command>' for more information.\n";
	assert.equal((await app.test(["config", "--help"])).stdout, expected);
	// A bare group token also renders group help.
	assert.equal((await app.test(["config"])).stdout, expected);
});

test("help: passthrough command help is header-only (passthrough.json)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		readOnlyPassthrough("checkout", {
			help: "checkout a branch",
			handler: () => 0,
		}),
	);
	assert.equal(
		(await app.test(["checkout", "--help"])).stdout,
		"myapp checkout -- checkout a branch\n",
	);
	// Passthrough commands appear in the app-level Commands section.
	app.command(
		readOnlyPassthrough("status", { help: "show status", handler: () => 0 }),
	);
	assert.equal(
		(await app.test([])).stdout,
		"myapp v1.0.0 -- test app\n\nCommands:\n  checkout    checkout a branch\n  status      show status\n\nUse 'myapp help <command>' for more information.\n",
	);
});

test("help: flag-set flags render in the Flags section (flag_sets.json)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			flagSets: [
				flagSet("diagnostics", {
					debug: flag("debug", t.bool, {
						help: "enable debug mode",
						presence: "default",
						default: false,
					}),
				}),
			],
			handler: ok,
		}),
	);
	assert.equal(
		(await app.test(["cmd", "--help"])).stdout,
		"myapp cmd -- a command\n\nFlags:\n  --debug, --no-debug    enable debug mode [default: false]\n",
	);
});

// App-level sections with no conformance stdout_equals case: expected bytes
// captured by running the Python implementation's app.test() over mirror apps
// (scratchpad capture_help.py, 2026-07-19; Python is the divergence oracle).

test("help: app help shows the Deprecated section (Python-captured)", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("new-cmd", {
			help: "the replacement command",
			handler: ok,
		}),
	);
	app.deprecate(deprecated("old-cmd", "use 'new-cmd' instead"));
	assert.equal(
		(await app.test([])).stdout,
		"myapp v1.0.0 -- test app\n\nCommands:\n  new-cmd    the replacement command\n\nDeprecated:\n  old-cmd    use 'new-cmd' instead\n\nUse 'myapp help <command>' for more information.\n",
	);
});

test("help: app help shows Global flags without meta (Python-captured)", async () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
		flags: {
			chatter: flag("chatter", t.bool, {
				help: "enable chatter output",
				presence: "default",
				default: false,
				short: "V",
			}),
		},
	});
	app.command(defineReadOnlyCommand("cmd", { help: "a command", handler: ok }));
	assert.equal(
		(await app.test([])).stdout,
		"myapp v1.0.0 -- test app\n\nCommands:\n  cmd    a command\n\nGlobal flags:\n  --chatter, -V    enable chatter output\n\nUse 'myapp help <command>' for more information.\n",
	);
});

test("help: app help shows the Infrastructure section (Python-captured)", async () => {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
		infraRoot: { MYAPP_ROOT: "~/.myapp" },
		handshakeEnv: { MYAPP_ORCHESTRATED: "set by the orchestrator" },
	});
	app.command(defineReadOnlyCommand("cmd", { help: "a command", handler: ok }));
	assert.equal(
		(await app.test([])).stdout,
		"myapp v1.0.0 -- test app\n\nCommands:\n  cmd    a command\n\nInfrastructure:\n  (location/handshake env vars; not suppressed by --hermetic)\n  MYAPP_ROOT            root (default: ~/.myapp)\n  MYAPP_ORCHESTRATED    set by the orchestrator\n\nUse 'myapp help <command>' for more information.\n",
	);
});

// --- The presence part (contract §23.8) ---

test("help: every flag and arg renders exactly one presence part, last", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "a command",
			args: [
				arg("src", t.str, { help: "source", presence: "required" }),
				arg("dest", t.str, { help: "destination", presence: "optional" }),
				arg("mode", t.str, {
					help: "mode",
					presence: "default",
					default: "fast",
				}),
			],
			flags: {
				target: flag("target", t.str, {
					help: "the target",
					presence: "required",
				}),
				host: flag("host", t.str, { help: "the host", presence: "optional" }),
				// An empty collection is a declaration now, so it announces itself
				// rather than rendering as the one line with no presence part.
				tag: flag("tag", t.list(t.str), {
					help: "tags",
					presence: "default",
					default: [],
				}),
				meta: flag("meta", t.dict(t.int), {
					help: "metadata",
					presence: "default",
					default: new Map(),
				}),
				color: flag("color", t.bool, {
					help: "colorize",
					presence: "optional",
				}),
			},
			handler: ok,
		}),
	);
	assert.equal(
		(await app.test(["cmd", "--help"])).stdout,
		"myapp cmd -- a command\n" +
			"\n" +
			"Arguments:\n" +
			"  src     source [required]\n" +
			"  dest    destination [optional]\n" +
			"  mode    mode [default: fast]\n" +
			"\n" +
			"Flags:\n" +
			"  --target <str>         the target [required]\n" +
			"  --host <str>           the host [optional]\n" +
			"  --tag <str>            tags [list] [default: []]\n" +
			"  --meta <key=int>       metadata [dict] [default: {}]\n" +
			"  --color, --no-color    colorize [optional]\n",
	);
});
