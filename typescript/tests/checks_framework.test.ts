/**
 * Check framework core tests: checks.toml parsing/validation, sealed
 * outcomes, reporters, registration double-entry, and dispatch-time
 * registration validation.
 *
 * GROUND TRUTH: byte-level expectations were captured on 2026-07-19 by
 * running the Python implementation (scratchpad pychecks.py / pychecks2.py)
 * and cross-checked against conformance/cases/checks.json.
 */

import { strict as assert } from "node:assert";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import {
	CheckOutcome,
	deriveStatus,
	mintSkip,
	parseChecksToml,
} from "../src/checks/framework.js";
import { RegistrationError } from "../src/errors.js";
import { type App, ErrorReporter, WarnReporter } from "../src/index.js";
import { createTestApp as createApp, tempDir } from "./helpers.js";

const VALID_CHECK_BODY = `tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`;

function appWithEmbed(toml: string, name = "testapp"): App {
	return createApp({ name, version: "1.0.0", help: "test", checksEmbed: toml });
}

// --- checks.toml parse errors (RegistrationError at construction) ---

function assertEmbedThrows(toml: string, message: string): void {
	assert.throws(() => appWithEmbed(toml), {
		name: "RegistrationError",
		message,
	});
}

test("checks.toml: missing app field", () => {
	assertEmbedThrows(
		`[checks.lint]\n${VALID_CHECK_BODY}`,
		'checks.toml: missing required top-level key "app"',
	);
});

test("checks.toml: app must be a non-empty string", () => {
	assertEmbedThrows(
		'app = ""\n',
		'checks.toml: "app" must be a non-empty string',
	);
	assertEmbedThrows(
		"app = 42\n",
		'checks.toml: "app" must be a non-empty string',
	);
});

test("checks.toml: app name mismatch", () => {
	assertEmbedThrows(
		`app = "wrong"\n[checks.lint]\n${VALID_CHECK_BODY}`,
		'checks.toml: app "wrong" does not match app name "testapp"',
	);
});

test("checks.toml: unknown top-level key", () => {
	assertEmbedThrows(
		'app = "testapp"\nextra = 1\n',
		'checks.toml: unknown top-level key "extra"',
	);
});

test("checks.toml: checks must be a table", () => {
	assertEmbedThrows(
		'app = "testapp"\nchecks = [1]\n',
		"checks.toml: [checks] must be a table",
	);
});

test("checks.toml: invalid check name", () => {
	assertEmbedThrows(
		`app = "testapp"\n[checks.Bad]\n${VALID_CHECK_BODY}`,
		'checks.toml: invalid check name "Bad" (must match [a-z][a-z0-9-]*)',
	);
});

test("checks.toml: check must be a table", () => {
	assertEmbedThrows(
		'app = "testapp"\n[checks]\nlint = 3\n',
		'checks.toml: check "lint" must be a table',
	);
});

test("checks.toml: unknown field", () => {
	assertEmbedThrows(
		`app = "testapp"\n[checks.lint]\n${VALID_CHECK_BODY}bogus = 1\n`,
		'checks.toml: check "lint": unknown field "bogus"',
	);
});

test("checks.toml: missing required field, sorted-first when several missing", () => {
	// Only tags present: Python reports the alphabetically-first missing
	// required field (depends_on), not declaration order.
	assertEmbedThrows(
		'app = "testapp"\n[checks.lint]\ntags = []\n',
		'checks.toml: check "lint": missing required field "depends_on"',
	);
});

test("checks.toml: tags must be a list of non-empty strings", () => {
	assertEmbedThrows(
		'app = "testapp"\n[checks.lint]\ntags = "release"\nseverity = "error"\nfast = true\npure = true\nneeds_network = false\ndepends_on = []\n',
		'checks.toml: check "lint": "tags" must be a list of strings',
	);
	assertEmbedThrows(
		'app = "testapp"\n[checks.lint]\ntags = [" "]\nseverity = "error"\nfast = true\npure = true\nneeds_network = false\ndepends_on = []\n',
		'checks.toml: check "lint": "tags" entries must be non-empty strings',
	);
});

test("checks.toml: severity must be error or warn", () => {
	assertEmbedThrows(
		'app = "testapp"\n[checks.lint]\ntags = []\nseverity = "fatal"\nfast = true\npure = true\nneeds_network = false\ndepends_on = []\n',
		'checks.toml: check "lint": "severity" must be "error" or "warn", got "fatal"',
	);
});

test("checks.toml: bool fields must be booleans (Python type names)", () => {
	assertEmbedThrows(
		'app = "testapp"\n[checks.lint]\ntags = []\nseverity = "error"\nfast = 1\npure = true\nneeds_network = false\ndepends_on = []\n',
		'checks.toml: check "lint": "fast" must be a boolean, got int',
	);
	assertEmbedThrows(
		'app = "testapp"\n[checks.lint]\ntags = []\nseverity = "error"\nfast = true\npure = "yes"\nneeds_network = false\ndepends_on = []\n',
		'checks.toml: check "lint": "pure" must be a boolean, got str',
	);
});

test("checks.toml: depends_on validation", () => {
	assertEmbedThrows(
		'app = "testapp"\n[checks.lint]\ntags = []\nseverity = "error"\nfast = true\npure = true\nneeds_network = false\ndepends_on = "x"\n',
		'checks.toml: check "lint": "depends_on" must be a list of strings',
	);
	assertEmbedThrows(
		'app = "testapp"\n[checks.lint]\ntags = []\nseverity = "error"\nfast = true\npure = true\nneeds_network = false\ndepends_on = [1]\n',
		'checks.toml: check "lint": "depends_on" entries must be strings',
	);
	assertEmbedThrows(
		'app = "testapp"\n[checks.lint]\ntags = []\nseverity = "error"\nfast = true\npure = true\nneeds_network = false\ndepends_on = ["ghost"]\n',
		'checks.toml: check "lint": depends_on references unknown check "ghost"',
	);
});

test("checks.toml: scope must be a string (parse-only field)", () => {
	assertEmbedThrows(
		`app = "testapp"\n[checks.lint]\n${VALID_CHECK_BODY}scope = 5\n`,
		'checks.toml: check "lint": "scope" must be a string, got int',
	);
});

test("checks.toml: malformed TOML is wrapped with the checks.toml prefix", () => {
	assert.throws(
		() => appWithEmbed("app = \n"),
		(e: unknown) => {
			assert.ok(e instanceof RegistrationError);
			assert.match(e.message, /^checks\.toml: /);
			return true;
		},
	);
});

test("checks.toml: app-only file is valid; scope is carried on defs", () => {
	const { appName, defs, order } = parseChecksToml('app = "solo"\n');
	assert.equal(appName, "solo");
	assert.equal(defs.size, 0);
	assert.deepEqual(order, []);

	const parsed = parseChecksToml(
		`app = "t"\n[checks.lint]\n${VALID_CHECK_BODY}scope = "changelog"\n`,
	);
	const def = parsed.defs.get("lint");
	assert.ok(def);
	assert.equal(def.scope, "changelog");
	assert.deepEqual(def.tags, ["release"]);
	assert.equal(def.severity, "error");
	assert.equal(def.fast, true);
	assert.equal(def.pure, true);
	assert.equal(def.needsNetwork, false);
	assert.deepEqual(def.dependsOn, []);
});

// --- checksPath / checksEmbed app options ---

test("cannot use both checksPath and checksEmbed", () => {
	assert.throws(
		() =>
			createApp({
				name: "t",
				version: "1",
				help: "h",
				checksPath: "/nope/checks.toml",
				checksEmbed: 'app = "t"\n',
			}),
		{ message: "cannot use both WithChecks and WithChecksEmbed" },
	);
});

test("checksPath must exist", () => {
	assert.throws(
		() =>
			createApp({
				name: "t",
				version: "1",
				help: "h",
				checksPath: "/no/such/checks.toml",
			}),
		{ message: "checks_path does not exist: /no/such/checks.toml" },
	);
});

test("checksPath loads a checks.toml file from disk", async () => {
	const dir = tempDir("strictcli-checks-");
	const path = join(dir, "checks.toml");
	writeFileSync(path, `app = "t"\n[checks.lint]\n${VALID_CHECK_BODY}`);
	const app = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksPath: path,
	});
	app.errorCheck("lint", (_ctx, r) => r.passed("all good"));
	app.setCheckContext(() => ({ projectRoot: "." }));
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 0);
	assert.equal(result.stdout, "PASS  lint    all good\n");
});

// --- Sealed outcomes and reporters ---

test("CheckOutcome cannot be constructed directly", () => {
	assert.throws(
		() => new CheckOutcome(Symbol("forged"), "passed", "m", [], []),
		{
			message:
				"CheckOutcome cannot be constructed directly; obtain one from a reporter (passed/skipped/found)",
		},
	);
});

test("mintSkip is the runner's internal skip mint", () => {
	const o = mintSkip('skipped: dependency "x" failed');
	assert.equal(o.kind, "skipped");
	assert.equal(deriveStatus(o), "skip");
});

test("reporter: passed/skipped/found minting rules", () => {
	const pass = new ErrorReporter().passed("ok");
	assert.equal(deriveStatus(pass), "pass");

	const skip = new ErrorReporter().skipped("not applicable");
	assert.equal(deriveStatus(skip), "skip");

	const failRep = new ErrorReporter();
	failRep.error("bad");
	assert.equal(deriveStatus(failRep.found("found bad")), "fail");

	const warnRep = new ErrorReporter();
	warnRep.warn("meh");
	assert.equal(deriveStatus(warnRep.found("found meh")), "warn");
});

test("reporter: problems block passed and skipped", () => {
	const r1 = new ErrorReporter();
	r1.error("x");
	assert.throws(() => r1.passed("nope"), {
		message:
			"problems were reported; a check that found problems cannot pass -- use found instead",
	});
	const r2 = new WarnReporter();
	r2.warn("x");
	assert.throws(() => r2.skipped("nope"), {
		message: "problems were reported; a check that found problems cannot skip",
	});
});

test("reporter: found without problems is a hard error", () => {
	assert.throws(() => new WarnReporter().found("nothing"), {
		message:
			"no problems were reported; nothing found means pass -- use passed instead",
	});
});

test("reporter: empty-text guards", () => {
	const r = new ErrorReporter();
	assert.throws(() => r.note("  "), {
		message: "note text must be a non-empty string",
	});
	assert.throws(() => r.warn(""), {
		message: "problem text must be a non-empty string",
	});
	assert.throws(() => r.error(" "), {
		message: "problem text must be a non-empty string",
	});
	assert.throws(() => r.passed(""), {
		message: "outcome message must be a non-empty string",
	});
	assert.throws(() => r.skipped("\t"), {
		message: "skip reason must be a non-empty string",
	});
	assert.throws(() => r.found(""), {
		message: "outcome message must be a non-empty string",
	});
});

test("reporter: notes are allowed on every outcome and verdict-inert", () => {
	const r = new ErrorReporter();
	r.note("scanned 5 files");
	r.note("no findings");
	const o = r.passed("clean");
	assert.deepEqual(o.notes, ["scanned 5 files", "no findings"]);
	assert.equal(deriveStatus(o), "pass");

	const r2 = new ErrorReporter();
	r2.note("ran deep scan");
	r2.error("blocking issue");
	const o2 = r2.found("gate failed");
	assert.deepEqual(o2.notes, ["ran deep scan"]);
	assert.equal(deriveStatus(o2), "fail");
});

test("WarnReporter structurally lacks error-minting", () => {
	const r = new WarnReporter();
	assert.equal((r as unknown as Record<string, unknown>).error, undefined);
});

// --- Registration double-entry ---

test("errorCheck: checks not enabled", () => {
	const app = createApp({ name: "t", version: "1", help: "h" });
	assert.throws(() => app.errorCheck("lint", (_c, r) => r.passed("ok")), {
		message: 'cannot register check "lint": checks not enabled',
	});
});

test("errorCheck: not declared in checks.toml", () => {
	const app = appWithEmbed(
		`app = "testapp"\n[checks.lint]\n${VALID_CHECK_BODY}`,
	);
	assert.throws(() => app.errorCheck("nope", (_c, r) => r.passed("ok")), {
		message: 'cannot register check "nope": not declared in checks.toml',
	});
});

test("errorCheck: duplicate registration", () => {
	const app = appWithEmbed(
		`app = "testapp"\n[checks.lint]\n${VALID_CHECK_BODY}`,
	);
	app.errorCheck("lint", (_c, r) => r.passed("ok"));
	assert.throws(() => app.errorCheck("lint", (_c, r) => r.passed("ok")), {
		message: 'check "lint": duplicate registration',
	});
});

test("registration severity cross-check, both directions", () => {
	const warnToml =
		'app = "testapp"\n[checks.w]\ntags = []\nseverity = "warn"\nfast = true\npure = true\nneeds_network = false\ndepends_on = []\n';
	const app = appWithEmbed(warnToml);
	assert.throws(() => app.errorCheck("w", (_c, r) => r.passed("ok")), {
		message:
			'check "w": declared severity "warn" in checks.toml but registered via app.errorCheck; use app.warnCheck',
	});

	const app2 = appWithEmbed(`app = "testapp"\n[checks.e]\n${VALID_CHECK_BODY}`);
	assert.throws(() => app2.warnCheck("e", (_c, r) => r.passed("ok")), {
		message:
			'check "e": declared severity "error" in checks.toml but registered via app.warnCheck; use app.errorCheck',
	});
});

test("declared-but-unregistered checks block dispatch (any argv)", async () => {
	const toml = `app = "testapp"\n[checks.lint]\n${VALID_CHECK_BODY}
[checks.format]
tags = ["dev"]
severity = "warn"
fast = true
pure = true
needs_network = false
depends_on = []
`;
	const app = appWithEmbed(toml);
	app.errorCheck("lint", (_c, r) => r.passed("ok"));
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 1);
	assert.equal(
		result.stderr,
		"error: checks declared in checks.toml but not registered: format\n",
	);
});
