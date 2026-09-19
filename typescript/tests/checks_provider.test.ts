/**
 * Check provider tests: spec builders, registration enabling the check
 * system, lazy materialization memoized per cwd, the reset hook, and the
 * runtime guards (severity mismatch, duplicate names, non-CheckSpec values).
 *
 * GROUND TRUTH: expectations derived from conformance/cases/providers.json
 * plus Python register_check_provider / _materialize_check_providers
 * semantics (2026-07-19).
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { CheckContext, CheckSpec as CheckSpecType } from "../src/index.js";
import {
	type App,
	CheckSpec,
	errorCheckSpec,
	warnCheckSpec,
} from "../src/index.js";
import {
	createTestApp as createApp,
	EMPTY_PROJECT_ROOT,
	tempDir,
} from "./helpers.js";

// A dedicated EMPTY root: checks that statically analyse the consumer's
// sources (effects-bypass) walk it, so it must not be a shared scratch dir.
const CTX: CheckContext = { projectRoot: EMPTY_PROJECT_ROOT };

function bareApp(name = "t"): App {
	return createApp({ name, version: "1", help: "h" });
}

function passingSpec(name: string): CheckSpecType {
	return errorCheckSpec({
		name,
		tags: ["release"],
		fast: true,
		pure: true,
		needsNetwork: false,
		dependsOn: [],
		impl: (_ctx, r) => r.passed(`${name} ok`),
	});
}

test("provider check runs and passes; registering enables the check command", async () => {
	const app = bareApp();
	app.registerCheckProvider(() => [
		errorCheckSpec({
			name: "prov-lint",
			tags: ["release"],
			fast: true,
			pure: true,
			needsNetwork: false,
			dependsOn: [],
			impl: (_ctx, r) => r.passed("provider lint ok"),
		}),
	]);
	app.setCheckContext(() => CTX);
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 0);
	assert.equal(result.stdout, "PASS  prov-lint    provider lint ok\n");
});

test("provider check fail outcome exits 1", async () => {
	const app = bareApp();
	app.registerCheckProvider(() => [
		errorCheckSpec({
			name: "prov-lint",
			tags: ["release"],
			fast: true,
			pure: true,
			needsNetwork: false,
			dependsOn: [],
			impl: (_ctx, r) => {
				r.error("lint errors found");
				return r.found("lint errors found");
			},
		}),
	]);
	app.setCheckContext(() => CTX);
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 1);
	assert.match(result.stdout, /FAIL {2}prov-lint/);
});

test("warn-severity provider spec mints a warning", async () => {
	const app = bareApp();
	app.registerCheckProvider(() => [
		warnCheckSpec({
			name: "prov-fmt",
			tags: ["dev"],
			fast: true,
			pure: true,
			needsNetwork: false,
			dependsOn: [],
			impl: (_ctx, r) => {
				r.warn("style issues");
				return r.found("style issues");
			},
		}),
	]);
	app.setCheckContext(() => CTX);
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 1);
	assert.match(result.stdout, /WARN {2}prov-fmt/);
});

test("severity mismatch is a hard error at materialization", async () => {
	const app = bareApp();
	app.registerCheckProvider(() => [
		errorCheckSpec({
			name: "prov-x",
			tags: ["release"],
			severity: "warn",
			fast: true,
			pure: true,
			needsNetwork: false,
			dependsOn: [],
			impl: (_ctx, r) => r.passed("never runs"),
		}),
	]);
	app.setCheckContext(() => CTX);
	await assert.rejects(app.test(["check", "--all"]), {
		message:
			'check "prov-x": declared severity "warn" but registered via errorCheckSpec; use warnCheckSpec',
	});
});

test("duplicate provider check name is a hard error", async () => {
	const app = bareApp();
	app.registerCheckProvider(() => [passingSpec("dup"), passingSpec("dup")]);
	app.setCheckContext(() => CTX);
	await assert.rejects(app.test(["check", "--all"]), {
		message: 'duplicate check definition "dup"',
	});
});

test("provider name colliding with a TOML-declared check is a hard error", async () => {
	const toml =
		'app = "t"\n[checks.lint]\ntags = ["x"]\nseverity = "error"\nfast = true\npure = true\nneeds_network = false\ndepends_on = []\n';
	const app = createApp({
		name: "t",
		version: "1",
		help: "h",
		checksEmbed: toml,
	});
	app.errorCheck("lint", (_c, r) => r.passed("ok"));
	app.registerCheckProvider(() => [passingSpec("lint")]);
	app.setCheckContext(() => CTX);
	await assert.rejects(app.test(["check", "--all"]), {
		message: 'duplicate check definition "lint"',
	});
});

test("provider checks are selectable via --name and listed", async () => {
	const app = bareApp();
	app.registerCheckProvider(() => [
		passingSpec("prov-a"),
		passingSpec("prov-b"),
	]);
	app.setCheckContext(() => CTX);
	const result = await app.test(["check", "--name", "prov-a"]);
	assert.equal(result.exitCode, 0);
	assert.match(result.stdout, /prov-a/);
	assert.doesNotMatch(result.stdout, /prov-b/);

	const listed = await app.test(["check", "--list"]);
	assert.match(listed.stdout, /prov-a/);
	assert.match(listed.stdout, /prov-b/);
});

test("honest-empty providers (undefined or []) are valid no-ops", async () => {
	const app = bareApp();
	app.registerCheckProvider(() => []);
	app.registerCheckProvider(() => undefined);
	app.setCheckContext(() => CTX);
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 0);
	assert.equal(result.stdout, "No checks matched the given filters.\n");
});

test("non-callable provider is rejected at registration", () => {
	const app = bareApp();
	assert.throws(
		() =>
			app.registerCheckProvider(
				"nope" as unknown as Parameters<typeof app.registerCheckProvider>[0],
			),
		{ message: "check provider must be callable" },
	);
});

test("provider returning a non-list or non-CheckSpec values is a hard error", async () => {
	const app = bareApp();
	app.registerCheckProvider(
		(() => "specs") as unknown as Parameters<
			typeof app.registerCheckProvider
		>[0],
	);
	app.setCheckContext(() => CTX);
	await assert.rejects(app.test(["check", "--all"]), {
		message: "check provider must return a list of CheckSpec, got str",
	});

	const app2 = bareApp();
	app2.registerCheckProvider((() => [
		{ name: "forged" },
	]) as unknown as Parameters<typeof app2.registerCheckProvider>[0]);
	app2.setCheckContext(() => CTX);
	await assert.rejects(app2.test(["check", "--all"]), (e: unknown) => {
		assert.match(
			(e as Error).message,
			/^check provider returned a non-CheckSpec value: /,
		);
		return true;
	});
});

test("a throwing provider is a hard error in every mode", async () => {
	const app = bareApp();
	app.registerCheckProvider(() => {
		throw new Error("provider exploded");
	});
	app.setCheckContext(() => CTX);
	await assert.rejects(app.test(["check", "--list"]), {
		message: "provider exploded",
	});
});

test("CheckSpec cannot be constructed directly", () => {
	assert.throws(
		() =>
			new CheckSpec(
				Symbol("forged"),
				{
					name: "x",
					tags: [],
					severity: "error",
					fast: true,
					pure: true,
					needsNetwork: false,
					dependsOn: [],
					scope: "",
				},
				(_ctx) => {
					throw new Error("unreachable");
				},
				"error",
			),
		{
			message:
				"CheckSpec cannot be constructed directly; use errorCheckSpec or warnCheckSpec",
		},
	);
});

test("spec builders carry all 8 meta fields plus implForm and scope", () => {
	const spec = warnCheckSpec({
		name: "s",
		tags: ["a", "b"],
		fast: false,
		pure: false,
		needsNetwork: true,
		dependsOn: ["d"],
		scope: "changelog",
		impl: (_ctx, r) => r.passed("ok"),
	});
	assert.equal(spec.name, "s");
	assert.deepEqual(spec.tags, ["a", "b"]);
	assert.equal(spec.severity, "warn");
	assert.equal(spec.fast, false);
	assert.equal(spec.pure, false);
	assert.equal(spec.needsNetwork, true);
	assert.deepEqual(spec.dependsOn, ["d"]);
	assert.equal(spec.scope, "changelog");
	assert.equal(spec.implForm, "warn");
});

test("materialization is memoized per cwd; reset re-runs providers", async () => {
	const app = bareApp();
	let calls = 0;
	app.registerCheckProvider(() => {
		calls++;
		return [passingSpec("prov-memo")];
	});
	app.setCheckContext(() => CTX);
	await app.test(["check", "--all"]);
	await app.test(["check", "--all"]);
	assert.equal(calls, 1);

	app.resetCheckProviderCache();
	const result = await app.test(["check", "--all"]);
	assert.equal(calls, 2);
	assert.equal(result.exitCode, 0);
});

test("a cwd change re-runs providers; provider defs are dropped first", async () => {
	const app = bareApp();
	let calls = 0;
	app.registerCheckProvider(() => {
		calls++;
		return [passingSpec("prov-cwd")];
	});
	app.setCheckContext(() => CTX);
	const oldCwd = process.cwd();
	try {
		await app.test(["check", "--all"]);
		assert.equal(calls, 1);
		process.chdir(tempDir("strictcli-provcwd-"));
		const result = await app.test(["check", "--all"]);
		assert.equal(calls, 2);
		// Re-materialization did not trip the duplicate-name guard: the stale
		// provider-sourced def was dropped before the provider re-ran.
		assert.equal(result.exitCode, 0);
	} finally {
		process.chdir(oldCwd);
	}
});

test("registering a new provider invalidates prior materialization", async () => {
	const app = bareApp();
	app.registerCheckProvider(() => [passingSpec("prov-one")]);
	app.setCheckContext(() => CTX);
	await app.test(["check", "--all"]);
	app.registerCheckProvider(() => [passingSpec("prov-two")]);
	const result = await app.test(["check", "--all"]);
	assert.equal(result.exitCode, 0);
	assert.match(result.stdout, /prov-one/);
	assert.match(result.stdout, /prov-two/);
});

test("runChecks also materializes provider-sourced checks", async () => {
	const app = bareApp();
	app.registerCheckProvider(() => [passingSpec("prov-api")]);
	const { results, exitCode } = await app.runChecks(CTX, { runAll: true });
	assert.equal(exitCode, 0);
	assert.deepEqual(
		results.map((r) => r.name),
		["prov-api"],
	);
});
