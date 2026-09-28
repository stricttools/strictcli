/**
 * Check command surface tests: the 8 flags, list/dry-run/help/no-match
 * branches, text and JSON formatters (including --verbose notes, durations,
 * and the count summary), and scope emission in list JSON.
 *
 * GROUND TRUTH: byte-level expectations were captured on 2026-07-19 by
 * running the Python implementation over the same mirror app (scratchpad
 * pychecks.py); durations are normalized before comparison since they are
 * wall-clock. Cross-checked against conformance/cases/checks.json +
 * check_notes.json + check_purity.json.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { CheckContext } from "../src/index.js";
import {
	type App,
	flag,
	formatCheckResults,
	formatCheckResultsJSON,
	t,
} from "../src/index.js";
import { envelopePayloadText } from "./envelope_helpers.js";
import { createTestApp as createApp, EMPTY_PROJECT_ROOT } from "./helpers.js";

/** The framework's would-do log header (dry mode's primary output). */
const DRY_RUN_HEADER = "DRY RUN \u2014 no changes were made. Would do:\n";

// A dedicated EMPTY root: checks that statically analyse the consumer's
// sources (effects-bypass) walk it, so it must not be a shared scratch dir.
const CTX: CheckContext = { projectRoot: EMPTY_PROJECT_ROOT };

/** The 4-check mirror app used to capture the Python ground-truth bytes. */
function mirrorApp(): App {
	const toml = `app = "testapp"
[checks.lint]
description = "Checks lint"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.format]
description = "Checks format"
subject = "quality"
tags = ["dev", "quality"]
severity = "warn"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.compile]
description = "Checks compile"
subject = "quality"
tags = ["release"]
severity = "error"
fast = false
pure = false
needs_network = false
depends_on = []

[checks.deploy-gate]
description = "Checks deploy-gate"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = true
depends_on = ["compile", "lint"]
scope = "changelog"
`;
	const app = createApp({
		name: "testapp",
		version: "1.0.0",
		help: "test",
		checksEmbed: toml,
	});
	app.errorCheck("lint", (_c, r) => {
		r.note("scanned 5 files");
		return r.passed("all good");
	});
	app.warnCheck("format", (_c, r) => {
		r.warn("style issue A");
		return r.found("style issues");
	});
	app.errorCheck("compile", (_c, r) => {
		r.error("compile failed hard");
		r.warn("also a warning");
		return r.found("compile failed");
	});
	app.errorCheck("deploy-gate", (_c, r) => r.passed("gate ok"));
	app.setCheckContext(() => CTX);
	return app;
}

/** Replaces wall-clock "(Nms)" durations with "(0ms)" for byte comparison. */
function zeroDurations(s: string): string {
	return s.replaceAll(/\((\d+)ms\)/g, "(0ms)");
}

test("check --list: aligned human table sorted by name", async () => {
	const result = await mirrorApp().test(["check", "--list"]);
	assert.equal(result.exitCode, 0);
	assert.equal(
		result.stdout,
		"NAME          TAGS           SEVERITY   VALUE   SOURCE\n" +
			"compile       release        error      error   default\n" +
			"deploy-gate   release        error      error   default\n" +
			"format        dev, quality   warn       warn    default\n" +
			"lint          release        error      error   default\n",
	);
});

test("check --list --json: compact entries, scope only when non-empty", async () => {
	const result = await mirrorApp().test(["check", "--list", "--json"]);
	assert.equal(result.exitCode, 0);
	assert.equal(
		envelopePayloadText(result.stdout),
		'[{"name":"compile","tags":["release"],"severity":"error","value":"error","source":"default"},' +
			'{"name":"deploy-gate","tags":["release"],"severity":"error","value":"error","source":"default","scope":"changelog"},' +
			'{"name":"format","tags":["dev","quality"],"severity":"warn","value":"warn","source":"default"},' +
			'{"name":"lint","tags":["release"],"severity":"error","value":"error","source":"default"}]\n',
	);
});

test("check --list on an app-only checks.toml prints No checks defined.", async () => {
	const app = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: 'app = "t"\n',
	});
	const result = await app.test(["check", "--list"]);
	assert.equal(result.exitCode, 0);
	assert.equal(result.stdout, "No checks defined.\n");
});

test("check --all: grouped problems (errors before warns), cascade skip", async () => {
	const result = await mirrorApp().test(["check", "--all"]);
	assert.equal(result.exitCode, 1);
	assert.equal(
		result.stdout,
		"FAIL  compile        compile failed\n" +
			"        [error] compile failed hard\n" +
			"        [warn] also a warning\n" +
			"WARN  format         style issues\n" +
			"        [warn] style issue A\n" +
			"PASS  lint           all good\n" +
			'SKIP  deploy-gate    skipped: dependency "compile" failed\n',
	);
	// Notes stay hidden without --verbose, even on failing checks.
	assert.doesNotMatch(result.stdout, /\[note\]/);
});

test("check --all --verbose: durations, notes, and count summary", async () => {
	const result = await mirrorApp().test(["--verbose", "check", "--all"]);
	assert.equal(result.exitCode, 1);
	assert.match(result.stdout, /\(\d+ms\)/);
	assert.equal(
		zeroDurations(result.stdout),
		"FAIL  compile        compile failed (0ms)\n" +
			"        [error] compile failed hard\n" +
			"        [warn] also a warning\n" +
			"WARN  format         style issues (0ms)\n" +
			"        [warn] style issue A\n" +
			"PASS  lint           all good (0ms)\n" +
			"        [note] scanned 5 files\n" +
			'SKIP  deploy-gate    skipped: dependency "compile" failed (0ms)\n' +
			"\n" +
			"1 passed / 1 failed / 1 warned / 1 skipped\n",
	);
});

test("check --all --json: notes and duration_ms always present", async () => {
	const result = await mirrorApp().test(["check", "--all", "--json"]);
	assert.equal(result.exitCode, 1);
	assert.equal(
		envelopePayloadText(result.stdout).replaceAll(
			/"duration_ms":\d+/g,
			'"duration_ms":0',
		),
		'[{"name":"compile","status":"fail","message":"compile failed","problems":[{"severity":"error","text":"compile failed hard"},{"severity":"warn","text":"also a warning"}],"notes":[],"duration_ms":0},' +
			'{"name":"format","status":"warn","message":"style issues","problems":[{"severity":"warn","text":"style issue A"}],"notes":[],"duration_ms":0},' +
			'{"name":"lint","status":"pass","message":"all good","problems":[],"notes":["scanned 5 files"],"duration_ms":0},' +
			'{"name":"deploy-gate","status":"skip","message":"skipped: dependency \\"compile\\" failed","problems":[],"notes":[],"duration_ms":0}]\n',
	);
});

test("check --tag: DSL filtering; --name: glob filtering", async () => {
	const byTag = await mirrorApp().test(["check", "--tag", "dev & quality"]);
	assert.equal(byTag.exitCode, 1);
	assert.match(byTag.stdout, /WARN {2}format/);
	assert.doesNotMatch(byTag.stdout, /lint/);

	const byName = await mirrorApp().test(["check", "--name", "lin*"]);
	assert.equal(byName.exitCode, 0);
	assert.equal(byName.stdout, "PASS  lint    all good\n");
});

test("check with non-matching filter prints the no-match message", async () => {
	const result = await mirrorApp().test(["check", "--tag", "nonexistent"]);
	assert.equal(result.exitCode, 0);
	assert.equal(result.stdout, "No checks matched the given filters.\n");
});

test("check: a warn-only run exits 1 and --ignore-warnings is refused", async () => {
	const toml =
		'app = "t"\n[checks.lint]\ndescription = "Checks lint"\nsubject = "quality"\ntags = ["x"]\nseverity = "error"\nfast = true\npure = true\nneeds_network = false\ndepends_on = []\n';
	const app = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: toml,
	});
	app.errorCheck("lint", (_c, r) => {
		r.warn("minor issues");
		return r.found("minor issues");
	});
	app.setCheckContext(() => CTX);
	const strict = await app.test(["check", "--all"]);
	assert.equal(strict.exitCode, 1);
	assert.match(strict.stdout, /WARN/);
	const refused = await app.test(["check", "--all", "--ignore-warnings"]);
	assert.equal(refused.exitCode, 1);
	assert.match(refused.stderr, /--ignore-warnings/);
});

test("check with no flags shows the command help (Python branch order)", async () => {
	const expectedHelp =
		"testapp check -- Run project checks registered via the check framework and report results\n" +
		"\n" +
		"Flags:\n" +
		"  --all, --no-all      Run every registered check regardless of tag or name filters [default: false]\n" +
		"  --tag <str>          Tag DSL expression to select checks (e.g. 'changelog & !quality') [default: ]\n" +
		"  --name <str>         Glob pattern to filter checks by name (e.g. 'hash-*', '*coverage*') [default: ]\n" +
		"  --hook <str>         Run the checks a hook declared in checks.toml selects (no hooks are declared) [default: ]\n" +
		"  --list, --no-list    List all registered checks with their tags and values and exit without running [default: false]\n";
	const bare = await mirrorApp().test(["check"]);
	assert.equal(bare.exitCode, 0);
	assert.equal(bare.stdout, expectedHelp);
	// --dry-run without a filter is NOT a filter: still the help branch. It is
	// now the FRAMEWORK flag, so the run is also a dry run and the would-do log
	// follows -- header only, because `check` is read_only.
	const dry = await mirrorApp().test(["--dry-run", "check"]);
	assert.equal(dry.exitCode, 0);
	assert.equal(dry.stdout, expectedHelp + DRY_RUN_HEADER);
});

test("check --dry-run with a non-matching filter prints no-match, not a plan", async () => {
	const result = await mirrorApp().test([
		"--dry-run",
		"check",
		"--tag",
		"nonexistent",
	]);
	assert.equal(result.exitCode, 0);
	assert.equal(
		result.stdout,
		`No checks matched the given filters.\n${DRY_RUN_HEADER}`,
	);
});

test("check --dry-run: singular noun for a single listed check", async () => {
	const result = await mirrorApp().test([
		"--dry-run",
		"check",
		"--name",
		"compile",
	]);
	assert.equal(result.exitCode, 0);
	assert.equal(
		result.stdout,
		`Would run 1 check:\n  1. compile [impure]\n${DRY_RUN_HEADER}`,
	);
});

test("check --dry-run: a selected pure check really runs", async () => {
	const result = await mirrorApp().test([
		"--dry-run",
		"check",
		"--name",
		"lint",
	]);
	assert.equal(result.exitCode, 0);
	assert.equal(
		result.stdout,
		`PASS  lint    all good\nWould run 0 checks:\n${DRY_RUN_HEADER}`,
	);
});

test("dry-run purity annotations: pure=false and needs_network=true are impure", async () => {
	const toml = `app = "t"
[checks.deploy]
description = "Checks deploy"
subject = "quality"
tags = ["release"]
severity = "error"
fast = false
pure = false
needs_network = false
depends_on = []

[checks.fetch]
description = "Checks fetch"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = true
depends_on = []
`;
	const app = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: toml,
	});
	app.errorCheck("deploy", (_c, r) => r.passed("deployed"));
	app.errorCheck("fetch", (_c, r) => r.passed("fetched"));
	app.setCheckContext(() => CTX);
	const result = await app.test(["--dry-run", "check", "--all"]);
	assert.equal(result.exitCode, 0);
	assert.equal(
		result.stdout,
		`Would run 2 checks:\n  1. deploy [impure]\n  2. fetch [impure]\n${DRY_RUN_HEADER}`,
	);
});

test("check --dry-run: a pure check that fails makes the rehearsal fail", async () => {
	const toml = `app = "t"
[checks.lint]
description = "Checks lint"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.deploy]
description = "Checks deploy"
subject = "quality"
tags = ["release"]
severity = "error"
fast = false
pure = false
needs_network = false
depends_on = []
`;
	const app = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: toml,
	});
	app.errorCheck("lint", (_c, r) => {
		r.error("lint errors found");
		return r.found("lint errors found");
	});
	app.errorCheck("deploy", (_c, r) => r.passed("deployed"));
	app.setCheckContext(() => CTX);
	const result = await app.test(["--dry-run", "check", "--all"]);
	assert.equal(result.exitCode, 1);
	assert.equal(
		result.stdout,
		"FAIL  lint    lint errors found\n" +
			"        [error] lint errors found\n" +
			"Would run 1 check:\n  1. deploy [impure]\n" +
			`${DRY_RUN_HEADER}`,
	);
});

test("check run without a context factory is a stderr error, exit 1", async () => {
	const toml =
		'app = "t"\n[checks.a]\ndescription = "Checks a"\nsubject = "quality"\ntags = ["x"]\nseverity = "error"\nfast = true\npure = true\nneeds_network = false\ndepends_on = []\n';
	const app = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: toml,
	});
	app.errorCheck("a", (_c, r) => r.passed("ok"));
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 1);
	assert.equal(result.stdout, "");
	assert.equal(
		result.stderr,
		"error: no check context configured. Call app.setCheckContext(factory) before running.\n",
	);
});

test("malformed tag expression is reported through the error writer", async () => {
	const r = await mirrorApp().test(["check", "--tag", "x &"]);
	assert.equal(r.exitCode, 1);
	assert.equal(r.stdout, "");
	assert.equal(
		r.stderr,
		"error: tag expression: unexpected end of expression at position 3\n",
	);
	const j = await mirrorApp().test(["--json", "check", "--tag", "x &"]);
	assert.equal(j.stderr, "");
	assert.deepEqual(JSON.parse(j.stdout).diagnostics, [
		{
			level: "error",
			message: "tag expression: unexpected end of expression at position 3",
		},
	]);
});

test("check command is absent when checks are never enabled", async () => {
	const app = createApp({ name: "t", version: "1", help: "h" });
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 1);
	assert.match(result.stderr, /unknown command 'check'/);
	const help = await app.test(["--help"]);
	assert.doesNotMatch(help.stdout, /\bcheck\b/);
});

test("check command appears in app help when checks are enabled", async () => {
	const help = await mirrorApp().test(["--help"]);
	assert.match(
		help.stdout,
		/check +Run project checks registered via the check framework and report results/,
	);
});

test("scoped checks run normally (scope is parse-only, matching Go)", async () => {
	const toml =
		'app = "t"\n[checks.scoped-check]\ndescription = "Checks scoped-check"\nsubject = "quality"\ntags = ["release"]\nseverity = "error"\nfast = true\npure = true\nneeds_network = false\ndepends_on = []\nscope = "changelog"\n';
	const app = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: toml,
	});
	app.errorCheck("scoped-check", (_c, r) => r.passed("scoped check ok"));
	app.setCheckContext(() => CTX);
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 0);
	assert.equal(result.stdout, "PASS  scoped-check    scoped check ok\n");

	const listed = await app.test(["check", "--list", "--json"]);
	assert.match(listed.stdout, /"scope":"changelog"/);
});

test("a global flag colliding with a check flag is dropped from the command", async () => {
	const toml =
		'app = "g"\n[checks.a]\ndescription = "Checks a"\nsubject = "quality"\ntags = ["x"]\nseverity = "error"\nfast = true\npure = true\nneeds_network = false\ndepends_on = []\n';
	const app = createApp({
		name: "g",
		version: "1",
		help: "h",
		flags: {
			all: flag("all", t.bool, {
				help: "Global all toggle",
				presence: "default",
				default: false,
			}),
		},
		checksEmbed: toml,
	});
	app.errorCheck("a", (_c, r) => r.passed("ok"));
	app.setCheckContext(() => CTX);
	// The candidate `all` check flag is dropped; the global's value reaches
	// the handler under the same key.
	const human = await app.test(["check", "--all"]);
	assert.equal(human.exitCode, 0);
	assert.match(human.stdout, /PASS {2}a/);
	// The machine output is the command's payload, reached through the
	// framework-owned --json (contract §7.5's sweep box).
	const asJson = await app.test(["--json", "check", "--all"]);
	assert.equal(asJson.exitCode, 0);
	assert.match(asJson.stdout, /"name":"a"/);
});

test("check no longer declares --verbose or --dry-run", async () => {
	// Both names are reserved by the framework, so the candidates are dropped
	// and the handler reads ctx.verbose / ctx.dryRun instead.
	const help = await mirrorApp().test(["check"]);
	assert.doesNotMatch(help.stdout, /--verbose/);
	assert.doesNotMatch(help.stdout, /--dry-run/);
	// The framework-delivered values still drive the same behavior.
	const verbose = await mirrorApp().test(["--verbose", "check", "--all"]);
	assert.match(verbose.stdout, /passed \//);
});

// --- Formatter functions (public surface) ---

test("formatCheckResults returns empty string for no results", () => {
	assert.equal(formatCheckResults([], false), "");
	assert.equal(formatCheckResults([], true), "");
});

test("formatCheckResultsJSON serializes empty results as []", () => {
	assert.equal(formatCheckResultsJSON([]), "[]");
});
