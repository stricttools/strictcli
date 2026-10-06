/**
 * The framework's help and version commands: help pages by address, per-flag
 * help, --depth, the help document under --json (which replaced
 * --dump-schema), and the refusals that point at the right spelling. Every
 * refusal that names a spelling is followed by that spelling.
 */

import { strict as assert } from "node:assert";
import { existsSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { type AppImpl, createApp } from "../src/app.js";
import { projectIdForEntry } from "../src/help_command.js";
import {
	choice,
	choiceFlag,
	defineReadOnlyCommand,
	flag,
	t,
} from "../src/index.js";
import { dumpSchemaCore, schemaJson } from "../src/schema.js";
import { tempDir } from "./helpers.js";

function helpApp() {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
		flags: {
			trace: flag("trace", t.bool, {
				help: "trace every step",
				presence: "default",
				default: false,
			}),
		},
	});
	app.command(
		defineReadOnlyCommand("run", {
			help: "run something",
			flags: {
				device: flag("device", t.str, {
					help: "which GPU to target",
					presence: "required",
					short: "d",
				}),
				fast: flag("fast", t.bool, {
					help: "skip the slow checks",
					presence: "default",
					default: false,
				}),
				via: choiceFlag(
					"via",
					{
						email: choice({
							help: "as an email",
							flags: {
								subject: flag("subject", t.str, {
									help: "the subject line",
									presence: "required",
								}),
							},
						}),
						sms: choice({ help: "as a text" }),
					},
					{ help: "delivery channel", presence: "required" },
				),
			},
			handler: () => 0,
		}),
	);
	const db = app.group("db", { help: "database commands" });
	db.command(
		defineReadOnlyCommand("migrate", {
			help: "run migrations",
			handler: () => 0,
		}),
	);
	const backup = db.group("backup", { help: "backup commands" });
	backup.command(
		defineReadOnlyCommand("take", { help: "take a backup", handler: () => 0 }),
	);
	return app;
}

async function ok(app: ReturnType<typeof helpApp>, ...argv: string[]) {
	const r = await app.test(argv);
	assert.equal(r.exitCode, 0, `${argv.join(" ")}: ${r.stderr}`);
	return r;
}

async function refused(
	app: ReturnType<typeof helpApp>,
	message: string,
	...argv: string[]
) {
	const r = await app.test(argv);
	assert.equal(r.exitCode, 1, `${argv.join(" ")}: ${r.stdout}`);
	assert.ok(
		r.stderr.startsWith(`error: ${message}\n`),
		`${argv.join(" ")}: ${r.stderr}`,
	);
	return r;
}

const ENVELOPE = {
	interface_version: 3,
	app: "myapp",
	app_version: "1.0.0",
	exit_code: 0,
	payload: null,
	output: null,
	dry_run: false,
	writes: null,
	preview: [],
	preview_error: null,
	diagnostics: [],
};

test("the help command matches the --help flag", async () => {
	const app = helpApp();
	const pairs: [string[], string[]][] = [
		[["help"], ["--help"]],
		[
			["help", "db"],
			["db", "--help"],
		],
		[
			["help", "db", "backup"],
			["db", "backup", "--help"],
		],
		[
			["help", "run"],
			["run", "--help"],
		],
		[
			["help", "db", "migrate"],
			["db", "migrate", "--help"],
		],
	];
	for (const [command, flagForm] of pairs) {
		assert.equal(
			(await ok(app, ...command)).stdout,
			(await ok(app, ...flagForm)).stdout,
		);
	}
});

test("root and group pages name the help command", async () => {
	const app = helpApp();
	assert.ok(
		(await ok(app, "help")).stdout.endsWith(
			"\nUse 'myapp help <command>' for more information.\n",
		),
	);
	assert.ok(
		(await ok(app, "help", "db")).stdout.endsWith(
			"\nUse 'myapp help db <command>' for more information.\n",
		),
	);
});

test("one flag", async () => {
	const app = helpApp();
	assert.equal(
		(await ok(app, "help", "run", "--device")).stdout,
		"myapp run -- run something\n\nFlags:\n  --device, -d <str>    which GPU to target [required]\n",
	);
	assert.equal(
		(await ok(app, "help", "run", "--subject")).stdout,
		"myapp run -- run something\n\nFlags:\n" +
			"  --via <choice>         delivery channel [required]\n" +
			"    email                as an email\n" +
			"      --subject <str>    the subject line [required]\n",
	);
	assert.equal(
		(await ok(app, "help", "run", "--trace")).stdout,
		"myapp run -- run something\n\nGlobal flags:\n  --trace, --no-trace    trace every step [default: false]\n",
	);
});

test("depth", async () => {
	const app = helpApp();
	assert.equal(
		(await ok(app, "help", "--depth", "2")).stdout,
		"myapp v1.0.0 -- test app\n\n" +
			"Commands:\n  run           run something\n  db migrate    run migrations\n\n" +
			"Groups:\n  db           database commands\n  db backup    backup commands\n\n" +
			"Global flags:\n  --trace    trace every step\n\n" +
			"Use 'myapp help <command>' for more information.\n",
	);
	assert.equal(
		(await ok(app, "help", "--depth=3", "db")).stdout,
		"myapp db -- database commands\n\n" +
			"Commands:\n  migrate        run migrations\n  backup take    take a backup\n\n" +
			"Groups:\n  backup    backup commands\n\n" +
			"Use 'myapp help db <command>' for more information.\n",
	);
	assert.equal(
		(await ok(app, "help", "--depth", "1")).stdout,
		(await ok(app, "--help")).stdout,
	);
});

test("refusals and their fixes", async () => {
	const app = helpApp();
	await refused(app, "unknown command 'nope'", "help", "nope");
	await refused(app, "unknown command 'nope' in 'db'", "help", "db", "nope");
	for (const bad of ["0", "two", "01"]) {
		await refused(
			app,
			`help: --depth: invalid value '${bad}': must be an integer of at least 1`,
			"help",
			"--depth",
			bad,
			"db",
		);
	}
	await refused(app, "help: --depth requires a value", "help", "--depth");
	await refused(
		app,
		"help: --depth lists the command tree below the app or a group; 'run' is a command",
		"help",
		"--depth",
		"2",
		"run",
	);
	await refused(
		app,
		"help: unknown option '--all': help's only option is --depth <int>, and a flag is addressed after its command: 'myapp help <command> --all'",
		"help",
		"--all",
	);
	await refused(
		app,
		"command 'run' has no flag '--devise'; its flags: --device, --fast, --via, --subject, --trace",
		"help",
		"run",
		"--devise",
	);
	await refused(
		app,
		"help: 'extra' follows the command 'run'; only one of its flags may follow a command (--<flag>)",
		"help",
		"run",
		"extra",
	);
	await refused(
		app,
		"help: '--fast' follows the flag address; an address ends at one flag",
		"help",
		"run",
		"--device",
		"--fast",
	);
	await refused(
		app,
		"help: '-d' is a short form; address the flag by its long name: 'myapp help run --device'",
		"help",
		"run",
		"-d",
	);
	await ok(app, "help", "run", "--device");
	await refused(
		app,
		"help: '--device=x' carries a value; a flag address is the flag alone: 'myapp help run --device'",
		"help",
		"run",
		"--device=x",
	);
	await refused(
		app,
		"help: '--no-fast' is another spelling of a declared flag; address the declaration: 'myapp help run --fast'",
		"help",
		"run",
		"--no-fast",
	);
	await ok(app, "help", "run", "--fast");
	await refused(
		app,
		"help: --depth is help's own option and goes before the address: 'myapp help --depth <int> db'",
		"help",
		"db",
		"--depth",
		"2",
	);
	await ok(app, "help", "--depth", "2", "db");
	await refused(
		app,
		"help: '--fast' names a flag, and a flag is addressed after its command: 'myapp help db <command> --fast'",
		"help",
		"db",
		"--fast",
	);
	await refused(
		app,
		"help: '--trace' names a flag, and a flag is addressed after its command: 'myapp help <command> --trace'",
		"help",
		"--trace",
	);
	await ok(app, "help", "run", "--trace");
	await refused(
		app,
		"'help' is a framework command at the root: use 'myapp help db'",
		"db",
		"help",
	);
	await refused(
		app,
		"'version' is a framework command at the root: use 'myapp version'",
		"db",
		"version",
	);
	await ok(app, "help", "db");
	await ok(app, "version");
});

test("own pages", async () => {
	const app = helpApp();
	const page = (await ok(app, "help", "--help")).stdout;
	assert.ok(
		page.startsWith(
			"myapp help -- show the help of the app, a group, a command, or one of its flags\n",
		),
	);
	assert.equal((await ok(app, "help", "run", "-h")).stdout, page);
	assert.equal(
		(await ok(app, "version", "--help")).stdout,
		"myapp version -- show the app's name and version\n\nWith --json, they are printed as JSON.\n",
	);
});

test("the version command", async () => {
	const app = helpApp();
	assert.equal((await ok(app, "version")).stdout, "myapp 1.0.0\n");
	assert.equal(
		(await ok(app, "version")).stdout,
		(await ok(app, "--version")).stdout,
	);
	const r = await ok(app, "version", "--json");
	assert.equal(r.stdout, '{\n  "name": "myapp",\n  "version": "1.0.0"\n}\n');
	assert.deepEqual(JSON.parse(r.stderr), { ...ENVELOPE, command: "version" });
	await refused(
		app,
		"version takes no arguments, got 'extra'",
		"version",
		"extra",
	);
	await refused(
		app,
		"the version line is text; for the machine form use 'myapp version --json'",
		"--version",
		"--json",
	);
	await ok(app, "version", "--json");
});

test("the help flag is text only", async () => {
	for (const [argv, fix] of [
		[["--help", "--json"], "myapp help --json"],
		[["--json"], "myapp help --json"],
		[["db", "--help", "--json"], "myapp help db --json"],
		[["run", "--help", "--json"], "myapp help run --json"],
		[["db", "migrate", "--json", "-h"], "myapp help db migrate --json"],
	] as const) {
		const app = helpApp();
		await refused(
			app,
			`help pages are text; for the machine form use '${fix}'`,
			...argv,
		);
		await ok(app, ...fix.split(" ").slice(1));
	}
});

test("the whole app's document is the schema", async () => {
	const app = helpApp();
	const r = await ok(app, "help", "--json");
	const doc = JSON.parse(r.stdout);
	// The test program's entry lies in the strictcli package, whose name the
	// project_id is (the nearest package.json above it).
	assert.equal(doc.project_id, "strictcli");
	assert.equal("address" in doc, false);
	const core = dumpSchemaCore(app as unknown as AppImpl);
	const expected: Record<string, unknown> = {};
	for (const [key, value] of Object.entries(core)) {
		expected[key] = value;
		if (key === "defaults") {
			expected.project_id = "strictcli";
		}
	}
	assert.equal(r.stdout, `${schemaJson(expected)}\n`);
	assert.deepEqual(JSON.parse(r.stderr), { ...ENVELOPE, command: "help" });
});

test("slices", async () => {
	const app = helpApp();
	let doc = JSON.parse(
		(await ok(app, "help", "db", "migrate", "--json")).stdout,
	);
	assert.deepEqual(doc.address, ["db", "migrate"]);
	assert.equal("commands" in doc, false);
	assert.deepEqual(Object.keys(doc.groups.db.commands), ["migrate"]);
	assert.equal("groups" in doc.groups.db, false);

	doc = JSON.parse(
		(await ok(app, "help", "run", "--subject", "--json")).stdout,
	);
	const flags = doc.commands.run.flags;
	assert.deepEqual(
		flags.map((f: { name: string }) => f.name),
		["via"],
	);
	assert.deepEqual(
		flags[0].choices.map((c: { name: string }) => c.name),
		["email"],
	);
	assert.equal("global_flags" in doc, false);

	doc = JSON.parse((await ok(app, "help", "--depth", "1", "--json")).stdout);
	assert.equal(doc.depth, 1);
	assert.equal("commands" in doc.groups.db, false);
	assert.equal("groups" in doc.groups.db, false);
});

test("--dump-schema is refused naming the help document", async () => {
	const app = helpApp();
	await refused(
		app,
		"--dump-schema is not supported; the app's help document is printed by 'myapp help --json'",
		"--dump-schema",
	);
	await ok(app, "help", "--json");
});

test("a project is named by the nearest package.json above the entry", () => {
	const dir = tempDir("strictcli-project-id-");
	writeFileSync(join(dir, "package.json"), '{"name": "example-tool"}\n');
	const entry = join(dir, "cli.js");
	writeFileSync(entry, "");
	assert.equal(projectIdForEntry(entry), "example-tool");
	const bare = tempDir("strictcli-no-project-");
	assert.throws(
		() => projectIdForEntry(join(bare, "cli.js")),
		/^Error: cannot determine project_id: no package\.json above /,
	);
	assert.equal(existsSync(join(dir, ".strictmetadata")), false);
});

test("an unreadable or nameless package.json is refused naming it", () => {
	const dir = tempDir("strictcli-project-id-");
	const entry = join(dir, "cli.js");
	writeFileSync(entry, "");
	for (const content of ["{broken", "{}", '{"name": ""}', '{"name": 42}']) {
		writeFileSync(join(dir, "package.json"), content);
		assert.throws(
			() => projectIdForEntry(entry),
			/^Error: cannot determine project_id: '.*package\.json' (is not valid JSON|declares no name)/,
		);
	}
});
