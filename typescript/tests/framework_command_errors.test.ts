/**
 * The framework's own commands report through the error writer (effects
 * contract §19.14's box): `error: <message>` once in human mode, an unprefixed
 * error diagnostic under --json, nothing on stderr.
 */

import { strict as assert } from "node:assert";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import {
	type App,
	createApp,
	defineReadOnlyCommand,
	flag,
	t,
} from "../src/index.js";
import { tempDir } from "./helpers.js";

function configApp(exists: boolean): { app: App; path: string } {
	const path = join(tempDir("strictcli-cfg-"), "config.json");
	if (exists) {
		writeFileSync(path, "{}\n");
	}
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
		config: true,
		configPath: path,
	});
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			flags: {
				count: flag("count", t.int, {
					help: "how many",
					presence: "default",
					default: 0n,
				}),
			},
			handler: () => 0,
		}),
	);
	return { app, path };
}

test("config set: an unknown key prints once with the prefix", async () => {
	const { app } = configApp(false);
	const r = await app.test(["config", "set", "nope", "--value", "1"]);
	assert.equal(r.exitCode, 1);
	assert.equal(r.stdout, "");
	assert.equal(r.stderr, "error: config set: unknown key 'nope'\n");
});

test("config set: under --json the error is a diagnostic and stderr stays empty", async () => {
	const { app } = configApp(false);
	const r = await app.test(["--json", "config", "set", "nope", "--value", "1"]);
	assert.equal(r.exitCode, 1);
	assert.equal(r.stderr, "");
	assert.deepEqual(JSON.parse(r.stdout).diagnostics, [
		{ level: "error", message: "config set: unknown key 'nope'" },
	]);
});

test("config init: refusing an existing file goes through the writer", async () => {
	const { app, path } = configApp(true);
	const r = await app.test(["config", "init"]);
	assert.equal(r.exitCode, 1);
	assert.equal(
		r.stderr,
		`error: config init: config file already exists: ${path}\n`,
	);
});

test("config edit: a failing editor is reported once through the writer", async () => {
	const { app } = configApp(true);
	const saved = process.env.EDITOR;
	process.env.EDITOR = "false";
	try {
		const r = await app.test(["config", "edit"]);
		assert.equal(r.exitCode, 1);
		assert.match(r.stderr, /^error: editor failed: [^\n]*\n$/);
		assert.doesNotMatch(r.stderr, /^error: error: /);
	} finally {
		if (saved === undefined) {
			delete process.env.EDITOR;
		} else {
			process.env.EDITOR = saved;
		}
	}
});
