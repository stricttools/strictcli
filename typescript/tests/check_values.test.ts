/**
 * Check metadata, per-check values, named hook selections, and the
 * failing-checks command.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import type {
	App,
	CheckContext,
	CheckOutcome,
	CheckValue,
	ErrorReporter,
} from "../src/index.js";
import { envelopePayload } from "./envelope_helpers.js";
import { createTestApp as createApp, EMPTY_PROJECT_ROOT } from "./helpers.js";

const CTX: CheckContext = { projectRoot: EMPTY_PROJECT_ROOT };

function check(
	name: string,
	tags: string[],
	severity: "error" | "warn" = "error",
	dependsOn: string[] = [],
): string {
	const q = (xs: string[]): string => xs.map((x) => `"${x}"`).join(", ");
	return (
		`[checks.${name}]\n` +
		`description = "Checks ${name}"\n` +
		`subject = "quality"\n` +
		`tags = [${q(tags)}]\n` +
		`severity = "${severity}"\n` +
		"fast = true\npure = true\nneeds_network = false\n" +
		`depends_on = [${q(dependsOn)}]\n\n`
	);
}

const HOOKS = `
[hooks.pre-push]
tag = "prepush"

[hooks.pre-release]
tag = "preflight & !slow"
`;

const TOML =
	'app = "testapp"\n\n' +
	check("lint", ["prepush"]) +
	check("fmt", ["prepush"], "warn") +
	check("docs", ["preflight"]) +
	check("bench", ["preflight", "slow"]) +
	HOOKS;

type Outcomes = Record<string, (r: ErrorReporter) => CheckOutcome>;

function fail(message: string, ...problems: string[]) {
	return (r: ErrorReporter): CheckOutcome => {
		for (const p of problems) {
			r.error(p);
		}
		return r.found(message);
	};
}

function warn(message: string, ...problems: string[]) {
	return (r: ErrorReporter): CheckOutcome => {
		for (const p of problems) {
			r.warn(p);
		}
		return r.found(message);
	};
}

function makeApp(
	toml: string = TOML,
	outcomes: Outcomes = {},
	resolver?: (name: string) => CheckValue | undefined,
): App {
	const app = createApp({
		name: "testapp",
		version: "1.0.0",
		help: "test app",
		checksEmbed: toml,
	});
	const severities = new Map<string, string>();
	for (const m of toml.matchAll(
		/\[checks\.([a-z0-9-]+)\][\s\S]*?severity = "(\w+)"/g,
	)) {
		severities.set(m[1] as string, m[2] as string);
	}
	for (const [name, severity] of severities) {
		const impl = (r: ErrorReporter): CheckOutcome => {
			const o = outcomes[name];
			return o === undefined ? r.passed(`${name} ok`) : o(r);
		};
		if (severity === "warn") {
			app.warnCheck(name, (_c, r) => impl(r as unknown as ErrorReporter));
		} else {
			app.errorCheck(name, (_c, r) => impl(r));
		}
	}
	app.setCheckContext(() => CTX);
	if (resolver !== undefined) {
		app.setCheckValueResolver(resolver);
	}
	return app;
}

function values(
	m: Record<string, [CheckValue["value"] | string, string]>,
): (name: string) => CheckValue | undefined {
	return (name) => {
		const e = m[name];
		return e === undefined
			? undefined
			: ({ value: e[0], source: e[1] } as CheckValue);
	};
}

type Item = Record<string, unknown>;

function items(stdout: string): Item[] {
	return envelopePayload(stdout) as Item[];
}

function registrationError(toml: string): string {
	try {
		createApp({ name: "testapp", version: "1", help: "t", checksEmbed: toml });
	} catch (e) {
		return (e as Error).message;
	}
	throw new Error(`expected a registration error for:\n${toml}`);
}

function registers(toml: string): App {
	return createApp({
		name: "testapp",
		version: "1",
		help: "t",
		checksEmbed: toml,
	});
}

// --- check metadata ---------------------------------------------------------

function one(description?: string, subject?: string): string {
	const lines = ['app = "testapp"', "", "[checks.lint]"];
	if (description !== undefined) {
		lines.push(`description = ${description}`);
	}
	if (subject !== undefined) {
		lines.push(`subject = ${subject}`);
	}
	lines.push(
		'tags = ["a"]',
		'severity = "error"',
		"fast = true",
		"pure = true",
		"needs_network = false",
		"depends_on = []",
		"",
	);
	return lines.join("\n");
}

test("metadata: a missing description is refused and adding it clears", () => {
	assert.equal(
		registrationError(one(undefined, '"quality"')),
		'checks.toml: check "lint": missing required field "description"',
	);
	registers(one('"Runs the linter"', '"quality"'));
});

test("metadata: a missing subject is refused and adding it clears", () => {
	assert.equal(
		registrationError(one('"Runs the linter"', undefined)),
		'checks.toml: check "lint": missing required field "subject"',
	);
	registers(one('"Runs the linter"', '"quality"'));
});

test("metadata: both missing names description first", () => {
	assert.match(
		registrationError(one()),
		/missing required field "description"$/,
	);
});

test("metadata: description must be one non-empty line", () => {
	for (const bad of ['""', '"   "', '"two\\nlines"', "3"]) {
		assert.equal(
			registrationError(one(bad, '"quality"')),
			'checks.toml: check "lint": "description" must be a non-empty single-line string',
		);
	}
	registers(one('"one line"', '"quality"'));
});

test("metadata: subject grammar is enforced", () => {
	for (const bad of [
		'""',
		'"Quality"',
		'"code quality"',
		'"a_b"',
		'"manifest"',
		"1",
	]) {
		assert.equal(
			registrationError(one('"d"', bad)),
			'checks.toml: check "lint": "subject" must be lowercase letters, digits, and hyphens, and not "manifest"',
		);
	}
	registers(one('"d"', '"code-quality-2"'));
});

test("metadata: description and subject reach the schema dump", () => {
	const schema = makeApp().dumpSchemaDict();
	const lint = (schema.checks as Record<string, Record<string, unknown>>)
		.lint as Record<string, unknown>;
	assert.equal(lint.description, "Checks lint");
	assert.equal(lint.subject, "quality");
	assert.deepEqual(Object.keys(lint).slice(-2), ["description", "subject"]);
});

// --- hooks ------------------------------------------------------------------

test("hooks: a hook runs its selection", async () => {
	const r = await makeApp().test(["check", "--hook", "pre-push"]);
	assert.equal(r.exitCode, 0);
	assert.match(r.stdout, /lint ok/);
	assert.match(r.stdout, /fmt ok/);
	assert.doesNotMatch(r.stdout, /docs/);
});

test("hooks: a hook's expression keeps negation, and so does --tag", async () => {
	const h = await makeApp().test(["check", "--hook", "pre-release"]);
	assert.match(h.stdout, /docs ok/);
	assert.doesNotMatch(h.stdout, /bench/);
	const m = await makeApp().test(["check", "--tag", "!prepush & !slow"]);
	assert.match(m.stdout, /docs ok/);
	assert.doesNotMatch(m.stdout, /lint|bench/);
});

test("hooks: failing-checks takes a hook", async () => {
	const r = await makeApp(TOML, { lint: fail("broken", "bad line") }).test([
		"failing-checks",
		"--hook",
		"pre-push",
	]);
	assert.equal(r.exitCode, 1);
	assert.match(r.stdout, /FAIL {2}lint/);
});

test("hooks: an unknown hook lists the declared ones, and a declared one clears it", async () => {
	const app = makeApp();
	const r = await app.test(["check", "--hook", "pre-pusj"]);
	assert.equal(r.exitCode, 1);
	assert.ok(
		r.stderr.startsWith(
			'error: unknown hook "pre-pusj"; declared hooks: pre-push, pre-release\n',
		),
		r.stderr,
	);
	assert.equal((await app.test(["check", "--hook", "pre-push"])).exitCode, 0);
});

test("hooks: an unknown hook with none declared, and declaring it clears", async () => {
	const toml = `app = "testapp"\n\n${check("lint", ["prepush"])}`;
	const r = await makeApp(toml).test(["failing-checks", "--hook", "pre-push"]);
	assert.equal(r.exitCode, 1);
	assert.ok(
		r.stderr.startsWith(
			'error: unknown hook "pre-push"; checks.toml declares no hooks\n',
		),
		r.stderr,
	);
	const fixed = `${toml}\n[hooks.pre-push]\ntag = "prepush"\n`;
	assert.equal(
		(await makeApp(fixed).test(["failing-checks", "--hook", "pre-push"]))
			.exitCode,
		0,
	);
});

test("hooks: --hook combines with no other selection flag", async () => {
	const app = makeApp();
	for (const other of [["--tag", "prepush"], ["--name", "lint"], ["--all"]]) {
		const r = await app.test(["check", "--hook", "pre-push", ...other]);
		assert.equal(r.exitCode, 1);
		assert.ok(
			r.stderr.startsWith(
				"error: --hook cannot be combined with --all, --tag, or --name\n",
			),
			r.stderr,
		);
	}
	assert.equal((await app.test(["check", "--hook", "pre-push"])).exitCode, 0);
});

test("hooks: declared hooks appear in help", async () => {
	for (const command of ["check", "failing-checks"]) {
		const r = await makeApp().test([command, "--help"]);
		assert.ok(
			r.stdout.includes(
				"Run the checks a hook declared in checks.toml selects: pre-push (tag 'prepush'), pre-release (tag 'preflight & !slow')",
			),
			r.stdout,
		);
	}
	const none = await makeApp(`app = "testapp"\n\n${check("lint", ["x"])}`).test(
		["check", "--help"],
	);
	assert.match(none.stdout, /\(no hooks are declared\)/);
});

test("hooks: declaration refusals, and the fix clears each", () => {
	const base = `app = "testapp"\n\n${check("lint", ["prepush"])}`;
	const cases: [string, string][] = [
		[
			`${base}[hooks.Pre-Push]\ntag = "prepush"\n`,
			'checks.toml: invalid hook name "Pre-Push" (must match [a-z][a-z0-9-]*)',
		],
		[
			`${base}[hooks]\npre-push = "prepush"\n`,
			'checks.toml: hook "pre-push" must be a table',
		],
		[
			`${base}[hooks.pre-push]\ntag = "prepush"\nname = "lint"\n`,
			'checks.toml: hook "pre-push": unknown field "name"',
		],
		[
			`${base}[hooks.pre-push]\n`,
			'checks.toml: hook "pre-push": missing required field "tag"',
		],
		[
			`${base}[hooks.pre-push]\ntag = ""\n`,
			'checks.toml: hook "pre-push": "tag" must be a non-empty string',
		],
		[
			`${base}[hooks.pre-push]\ntag = "prepush &"\n`,
			'checks.toml: hook "pre-push": tag expression: unexpected end of expression at position 9',
		],
		[
			`app = "testapp"\nhooks = 3\n\n${check("lint", ["prepush"])}`,
			"checks.toml: [hooks] must be a table",
		],
	];
	for (const [toml, message] of cases) {
		assert.equal(registrationError(toml), message);
	}
	registers(`${base}[hooks.pre-push]\ntag = "prepush"\n`);
});

// --- per-check values -------------------------------------------------------

test("values: off does not run the check and shows it with its source", async () => {
	let called = false;
	const app = makeApp(
		TOML,
		{
			lint: (r) => {
				called = true;
				return r.passed("x");
			},
		},
		values({ lint: ["off", "demo:lint in options/quality.toml"] }),
	);
	const r = await app.test(["check", "--hook", "pre-push"]);
	assert.equal(called, false);
	assert.equal(r.exitCode, 0);
	assert.ok(
		r.stdout.includes("OFF   lint    off: demo:lint in options/quality.toml"),
		r.stdout,
	);
	const j = items(
		(await app.test(["check", "--hook", "pre-push", "--json"])).stdout,
	);
	const lint = j.find((i) => i.name === "lint") as Item;
	assert.equal(lint.status, "off");
	assert.equal(lint.message, "off: demo:lint in options/quality.toml");
});

test("values: warn reports failures as warnings", async () => {
	const app = makeApp(
		TOML,
		{ lint: fail("2 problems", "bad a", "bad b") },
		values({ lint: ["warn", "demo:lint in q.toml"] }),
	);
	const r = await app.test(["check", "--name", "lint"]);
	assert.equal(r.exitCode, 1);
	assert.match(r.stdout, /WARN {2}lint/);
	assert.match(r.stdout, /\[warn\] bad a/);
	assert.doesNotMatch(r.stdout, /\[error\]/);
	const f = await app.test(["failing-checks", "--name", "lint"]);
	assert.equal(f.exitCode, 0);
	assert.equal(f.stdout, "");
});

test("values: error runs as registered", async () => {
	const app = makeApp(
		TOML,
		{ lint: fail("broken", "bad") },
		values({ lint: ["error", "demo:lint in q.toml"] }),
	);
	const r = await app.test(["failing-checks", "--name", "lint"]);
	assert.equal(r.exitCode, 1);
	assert.match(r.stdout, /FAIL {2}lint/);
});

test("values: no value means the registered severity from default", async () => {
	const app = makeApp(TOML, {}, () => undefined);
	for (const i of items(
		(await app.test(["check", "--list", "--json"])).stdout,
	)) {
		assert.equal(i.value, i.severity);
		assert.equal(i.source, "default");
	}
});

test("values: raising above the registered severity is refused, and lowering clears it", async () => {
	const app = makeApp(
		TOML,
		{},
		values({ fmt: ["error", "demo:fmt in q.toml"] }),
	);
	const r = await app.test(["check", "--name", "fmt"]);
	assert.equal(r.exitCode, 1);
	assert.ok(
		r.stderr.startsWith(
			'error: check "fmt": the check value resolver returned "error" (from demo:fmt in q.toml) for a check registered as "warn"; a check value may lower a check\'s severity, never raise it\n',
		),
		r.stderr,
	);
	const fixed = makeApp(
		TOML,
		{},
		values({ fmt: ["warn", "demo:fmt in q.toml"] }),
	);
	assert.equal((await fixed.test(["check", "--name", "fmt"])).exitCode, 0);
});

test("values: a value outside the three is refused, and a valid one clears it", async () => {
	const app = makeApp(
		TOML,
		{},
		values({ lint: ["on", "demo:lint in q.toml"] }),
	);
	const r = await app.test(["check", "--list"]);
	assert.equal(r.exitCode, 1);
	assert.ok(
		r.stderr.startsWith(
			'error: check "lint": the check value resolver returned "on"; a check value is one of error, warn, off\n',
		),
		r.stderr,
	);
	const fixed = makeApp(
		TOML,
		{},
		values({ lint: ["off", "demo:lint in q.toml"] }),
	);
	assert.equal((await fixed.test(["check", "--list"])).exitCode, 0);
});

test("values: an empty source is refused, and naming one clears it", async () => {
	const app = makeApp(TOML, {}, values({ lint: ["off", ""] }));
	const r = await app.test(["failing-checks", "--all"]);
	assert.equal(r.exitCode, 1);
	assert.ok(
		r.stderr.startsWith(
			'error: check "lint": the check value resolver returned "off" with an empty source; name where the value came from\n',
		),
		r.stderr,
	);
	const fixed = makeApp(TOML, {}, values({ lint: ["off", "demo:lint"] }));
	assert.equal((await fixed.test(["failing-checks", "--all"])).exitCode, 0);
});

test("values: the resolver must be callable", () => {
	const app = makeApp();
	assert.throws(
		() =>
			app.setCheckValueResolver(
				"off" as unknown as (name: string) => CheckValue | undefined,
			),
		/check value resolver must be callable/,
	);
});

const DEP_TOML = `app = "testapp"\n\n${check("base", ["t"])}${check("top", ["t"], "error", ["base"])}`;

function statuses(list: Item[]): Record<string, unknown> {
	return Object.fromEntries(list.map((i) => [i.name, i.status]));
}

test("values: a warn dependency does not block its dependent", async () => {
	const app = makeApp(
		DEP_TOML,
		{ base: fail("base broken", "x") },
		values({ base: ["warn", "demo:base"] }),
	);
	assert.deepEqual(
		statuses(items((await app.test(["check", "--all", "--json"])).stdout)),
		{ base: "warn", top: "pass" },
	);
});

test("values: an error dependency still blocks", async () => {
	const app = makeApp(DEP_TOML, { base: fail("broken", "x") });
	assert.deepEqual(
		statuses(
			items((await app.test(["check", "--name", "top", "--json"])).stdout),
		),
		{ base: "fail", top: "skip" },
	);
});

test("values: an off dependency does not block its dependent", async () => {
	const app = makeApp(
		DEP_TOML,
		{ base: fail("base broken", "x") },
		values({ base: ["off", "demo:base"] }),
	);
	const r = await app.test(["check", "--name", "top"]);
	assert.equal(r.exitCode, 0);
	assert.ok(r.stdout.includes("OFF   base    off: demo:base"), r.stdout);
	assert.ok(r.stdout.includes("PASS  top "), r.stdout);
});

test("values: a broken check resolved to warn still fails", async () => {
	class Kaboom extends Error {}
	const app = makeApp(
		TOML,
		{
			lint: () => {
				throw new Kaboom("kaboom");
			},
		},
		values({ lint: ["warn", "demo:lint"] }),
	);
	const r = await app.test(["failing-checks", "--name", "lint"]);
	assert.equal(r.exitCode, 1);
	assert.ok(
		r.stdout.includes('check "lint" aborted with Kaboom: kaboom'),
		r.stdout,
	);
});

test("values: the verbose summary counts off checks", async () => {
	const app = makeApp(TOML, {}, values({ lint: ["off", "demo:lint"] }));
	const r = await app.test(["--verbose", "check", "--hook", "pre-push"]);
	assert.ok(
		r.stdout.includes("1 passed / 0 failed / 0 warned / 0 skipped / 1 off"),
		r.stdout,
	);
});

test("values: runChecks applies the resolver and refuses a bad value", async () => {
	const app = makeApp(
		TOML,
		{ lint: fail("broken", "x") },
		values({ lint: ["warn", "demo:lint"], docs: ["off", "demo:docs"] }),
	);
	const { results, exitCode } = await app.runChecks(CTX, { runAll: true });
	const by = Object.fromEntries(results.map((r) => [r.name, r.status]));
	assert.equal(by.lint, "warn");
	assert.equal(by.docs, "off");
	assert.equal(exitCode, 1);
	assert.ok(results.every((r) => !r.gated()));
	const bad = makeApp(TOML, {}, values({ fmt: ["error", "demo:fmt"] }));
	await assert.rejects(bad.runChecks(CTX, { runAll: true }), /never raise it/);
});

test("list: shows each check's value and source", async () => {
	const app = makeApp(
		TOML,
		{},
		values({
			lint: ["off", "demo:lint in options/quality.toml"],
			docs: ["warn", "demo:docs in options/docs.toml"],
		}),
	);
	const r = await app.test(["check", "--list"]);
	assert.equal(r.exitCode, 0);
	assert.equal(
		r.stdout,
		"NAME    TAGS              SEVERITY   VALUE   SOURCE\n" +
			"bench   preflight, slow   error      error   default\n" +
			"docs    preflight         error      warn    demo:docs in options/docs.toml\n" +
			"fmt     prepush           warn       warn    default\n" +
			"lint    prepush           error      off     demo:lint in options/quality.toml\n",
	);
	const lint = items(
		(await app.test(["check", "--list", "--json"])).stdout,
	).find((i) => i.name === "lint") as Item;
	assert.equal(lint.value, "off");
	assert.equal(lint.source, "demo:lint in options/quality.toml");
	const f = await app.test(["failing-checks", "--list"]);
	assert.equal(f.exitCode, 0);
	assert.ok(f.stdout.startsWith("NAME "));
});

// --- failing-checks ---------------------------------------------------------

test("failing-checks: prints only error-level failures", async () => {
	const app = makeApp(TOML, {
		lint: fail("lint broken", "bad"),
		fmt: warn("fmt drift", "drift"),
	});
	const r = await app.test(["failing-checks", "--all"]);
	assert.equal(r.exitCode, 1);
	assert.equal(r.stdout, "FAIL  lint    lint broken\n        [error] bad\n");
	const j = items(
		(await app.test(["failing-checks", "--all", "--json"])).stdout,
	);
	assert.deepEqual(
		j.map((i) => i.name),
		["lint"],
	);
});

test("failing-checks: warnings alone exit zero and print nothing", async () => {
	const app = makeApp(TOML, { fmt: warn("fmt drift", "drift") });
	const r = await app.test(["failing-checks", "--all"]);
	assert.equal(r.exitCode, 0);
	assert.equal(r.stdout, "");
	const c = await app.test(["check", "--all"]);
	assert.equal(c.exitCode, 1);
	assert.match(c.stdout, /WARN {2}fmt/);
});

test("failing-checks: no flags shows its help", async () => {
	const r = await makeApp().test(["failing-checks"]);
	assert.equal(r.exitCode, 0);
	assert.ok(r.stdout.startsWith("testapp failing-checks -- "), r.stdout);
});

test("failing-checks: takes --tag and --name", async () => {
	const app = makeApp(TOML, { lint: fail("broken", "x") });
	assert.equal(
		(await app.test(["failing-checks", "--tag", "prepush"])).exitCode,
		1,
	);
	assert.equal(
		(await app.test(["failing-checks", "--name", "docs"])).exitCode,
		0,
	);
});

test("failing-checks: appears in app help and refuses --ignore-warnings", async () => {
	const app = makeApp();
	const r = await app.test(["--help"]);
	assert.match(
		r.stdout,
		/^ {2}failing-checks\s+Run project checks and report only error-level failures, exiting nonzero when any exist$/m,
	);
	const refused = await app.test([
		"failing-checks",
		"--all",
		"--ignore-warnings",
	]);
	assert.equal(refused.exitCode, 1);
	assert.match(refused.stderr, /--ignore-warnings/);
});
