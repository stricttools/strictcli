/**
 * The constraint system (contract §26), end to end: engagement, the two
 * predicates, nesting, the two pinned violation sentences, the `Constraints:`
 * help block, the schema catalogue and the MCP projection.
 *
 * The two real fleet sites §26.7 pins are reproduced verbatim as apps, because
 * they are the shapes that forced the operand generalization: safegit's
 * `author rewrite` (an at-least-one over two all-or-none pairs, by name) and
 * saferm's `purge` (one at-least-one over a variadic ARG and three flags).
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { App } from "../src/app.js";
import {
	allOrNone,
	arg,
	atLeastOne,
	choice,
	choiceFlag,
	createApp,
	defineReadOnlyCommand,
	flag,
	implies,
	requires,
	t,
} from "../src/index.js";
import { asToolsForApp, buildJSONSchema } from "../src/tool.js";

const ok = () => undefined;

function makeApp(): App {
	return createApp({ name: "myapp", version: "1.0.0", help: "test app" });
}

/** safegit `author rewrite`, as §26.7 pins its migrated declaration. */
function authorApp(): App {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("rewrite", {
			help: "rewrite author identity across history",
			flags: {
				old_name: flag("old-name", t.str, {
					help: "current author or committer display name",
					presence: "optional",
				}),
				new_name: flag("new-name", t.str, {
					help: "new display name",
					presence: "optional",
				}),
				old_email: flag("old-email", t.str, {
					help: "current author or committer email address",
					presence: "optional",
				}),
				new_email: flag("new-email", t.str, {
					help: "new email address",
					presence: "optional",
				}),
			},
			constraints: [
				allOrNone({
					name: "author-name",
					members: [{ name: "old-name" }, { name: "new-name" }],
				}),
				allOrNone({
					name: "author-email",
					members: [{ name: "old-email" }, { name: "new-email" }],
				}),
				atLeastOne({
					name: "author-change",
					members: [{ name: "author-name" }, { name: "author-email" }],
				}),
			],
			handler: ok,
		}),
	);
	return app;
}

/** saferm `purge`, as §26.7 pins its migrated declaration. */
function purgeApp(): App {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("purge", {
			help: "permanently destroy archived items",
			flags: {
				older_than: flag("older-than", t.str, {
					help: "Purge items older than duration",
					presence: "optional",
				}),
				larger_than: flag("larger-than", t.str, {
					help: "Only purge items larger than this size",
					presence: "optional",
				}),
				all: flag("all", t.bool, {
					help: "Select all archived items for permanent destruction",
					presence: "default",
					default: false,
				}),
			},
			args: [
				arg("targets", t.str, {
					help: "Record UUIDs or numeric database IDs",
					variadic: true,
					presence: "optional",
				}),
			],
			constraints: [
				atLeastOne({
					name: "purge-selection",
					members: [
						{ name: "targets", when: "non_empty" },
						{ name: "older-than" },
						{ name: "larger-than" },
						{ name: "all", when: "true" },
					],
				}),
			],
			handler: ok,
		}),
	);
	return app;
}

// --- The two violation sentences (§12.15) ---

test("at-least-one: a vacuous group is violated, and the members render structurally", async () => {
	const r = await authorApp().test(["rewrite"]);
	assert.equal(r.exitCode, 1);
	assert.equal(
		r.stderr,
		'error: constraint "author-change": at least one of (--old-name with --new-name), (--old-email with --new-email) is required\n' +
			"try 'myapp rewrite --help'\n",
	);
});

test("at-least-one: engaging one member satisfies it, and a second is never refused", async () => {
	assert.equal(
		(
			await authorApp().test([
				"rewrite",
				"--old-name",
				"aa",
				"--new-name",
				"bb",
			])
		).exitCode,
		0,
	);
	// No upper bound: this family is NOT exclusivity (§26.1).
	assert.equal(
		(
			await authorApp().test([
				"rewrite",
				"--old-name",
				"aa",
				"--new-name",
				"bb",
				"--old-email",
				"cc",
				"--new-email",
				"dd",
			])
		).exitCode,
		0,
	);
});

test("all-or-none: a child violation reports instead of its parent (§26.4's order)", async () => {
	const r = await authorApp().test(["rewrite", "--old-name", "aa"]);
	assert.equal(
		r.stderr,
		'error: constraint "author-name": --old-name, --new-name must be used together\n' +
			"try 'myapp rewrite --help'\n",
	);
});

test("all-or-none: a vacuous group is satisfied -- the 'none' half of its own name", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "hh",
			flags: {
				aa: flag("aa", t.str, { help: "hh", presence: "optional" }),
				bb: flag("bb", t.str, { help: "hh", presence: "optional" }),
			},
			constraints: [
				allOrNone({ name: "pair", members: [{ name: "aa" }, { name: "bb" }] }),
			],
			handler: ok,
		}),
	);
	assert.equal((await app.test(["cmd"])).exitCode, 0);
});

test("engagement propagates upward, satisfaction does not: two vacuous pairs leave the parent unsatisfied", async () => {
	// safegit's shipped hand guard, expressed. Nothing typed engages nothing,
	// so both pairs are vacuously satisfied and the parent still fires.
	const r = await authorApp().test(["rewrite"]);
	assert.match(r.stderr, /"author-change"/);
});

// --- The election vocabulary (§26.3) ---

test("when: `true` counts only a true value, and the decline clause teaches", async () => {
	const r = await purgeApp().test(["purge", "--no-all"]);
	assert.equal(
		r.stderr,
		'error: constraint "purge-selection": at least one of targets, --older-than, --larger-than, --all is required ' +
			"(--no-all declines an option; it does not choose one)\n" +
			"try 'myapp purge --help'\n",
	);
	assert.equal((await purgeApp().test(["purge", "--all"])).exitCode, 0);
});

test("when: an arg member engages from its own positional tokens, at the argv door", async () => {
	assert.equal((await purgeApp().test(["purge"])).exitCode, 1);
	assert.equal((await purgeApp().test(["purge", "abc"])).exitCode, 0);
	// `non_empty` on a variadic arg is equal to `present` (§26.3): an empty
	// token list is no provision at all.
	assert.equal(
		(await purgeApp().test(["purge", "--older-than", "7d"])).exitCode,
		0,
	);
});

test("when: `present` on a str member engages even for an empty string (§26.7's pin)", async () => {
	// A2 places empty-value legality on the flag's own validation, never on
	// the layer above it, so `--older-than ""` ENGAGES the constraint.
	assert.equal(
		(await purgeApp().test(["purge", "--older-than", ""])).exitCode,
		0,
	);
});

test("when: a declared default never engages, and an implied value does", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "hh",
			flags: {
				trigger: flag("trigger", t.bool, {
					help: "hh",
					presence: "default",
					default: false,
				}),
				target: flag("target", t.bool, {
					help: "hh",
					presence: "optional",
				}),
				other: flag("other", t.str, { help: "hh", presence: "optional" }),
			},
			constraints: [
				implies({
					name: "spread",
					flag: "trigger",
					implies: "target",
					value: true,
				}),
				atLeastOne({
					name: "sel",
					members: [{ name: "target", when: "true" }, { name: "other" }],
				}),
			],
			handler: ok,
		}),
	);
	// Nothing supplied: the trigger's own default never fires the implication,
	// and the defaulted/absent members leave the at-least-one vacuous.
	assert.equal((await app.test(["cmd"])).exitCode, 1);
	// The implication runs BEFORE the constraint set, so an implied value can
	// engage a member (§26.4).
	assert.equal((await app.test(["cmd", "--trigger"])).exitCode, 0);
});

test("when: an arg member engages at the machine door exactly as at the argv door", async () => {
	const app = purgeApp();
	await assert.rejects(app.call("purge", {}), {
		name: "InvokeError",
		message:
			'constraint "purge-selection": at least one of targets, --older-than, --larger-than, --all is required',
	});
	// An explicitly supplied EMPTY array is the flat spelling of no tokens at
	// all, so it is not a provision.
	await assert.rejects(app.call("purge", { targets: [] }), {
		name: "InvokeError",
		message:
			'constraint "purge-selection": at least one of targets, --older-than, --larger-than, --all is required',
	});
	await app.call("purge", { targets: ["abc"] });
	await app.call("purge", { all: true });
});

test("a token-spelled selector is an ordinary member, engaged only by an actual election", async () => {
	const build = (): App => {
		const app = makeApp();
		app.command(
			defineReadOnlyCommand("send", {
				help: "hh",
				flags: {
					via: choiceFlag(
						"via",
						{ email: choice({ help: "email" }), sms: choice({ help: "sms" }) },
						{ help: "delivery channel", presence: "default", default: "email" },
					),
					dry: flag("dry", t.bool, {
						help: "hh",
						presence: "default",
						default: false,
					}),
				},
				constraints: [
					atLeastOne({
						name: "action",
						members: [{ name: "via" }, { name: "dry", when: "true" }],
					}),
				],
				handler: ok,
			}),
		);
		return app;
	};
	// A DEFAULT election is the declaration deciding, not the invocation.
	assert.equal((await build().test(["send"])).exitCode, 1);
	assert.equal((await build().test(["send", "--via", "sms"])).exitCode, 0);
});

// --- Help rendering (§26.10) ---

test("help: the Constraints: block renders names, sentences and its own column", async () => {
	const r = await authorApp().test(["rewrite", "--help"]);
	assert.equal(
		r.stdout,
		"myapp rewrite -- rewrite author identity across history\n" +
			"\n" +
			"Flags:\n" +
			"  --old-name <str>     current author or committer display name [optional]\n" +
			"  --new-name <str>     new display name [optional]\n" +
			"  --old-email <str>    current author or committer email address [optional]\n" +
			"  --new-email <str>    new email address [optional]\n" +
			"\n" +
			"Constraints:\n" +
			"  author-name      all or none of --old-name, --new-name\n" +
			"  author-email     all or none of --old-email, --new-email\n" +
			"  author-change    at least one of (--old-name with --new-name), (--old-email with --new-email)\n",
	);
});

test("help: requires and implies render for the first time, and no `=true` spelling is invented", async () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("build", {
			help: "build it",
			flags: {
				fast: flag("fast", t.bool, {
					help: "fast mode",
					presence: "default",
					default: false,
				}),
				embeddings: flag("embeddings", t.bool, {
					help: "enable embeddings",
					presence: "default",
					default: true,
				}),
				cert: flag("cert", t.str, { help: "cert path", presence: "optional" }),
				ssl: flag("ssl", t.bool, {
					help: "use ssl",
					presence: "default",
					default: false,
				}),
			},
			constraints: [
				implies({
					name: "fast-path",
					flag: "fast",
					implies: "embeddings",
					value: false,
				}),
				requires({ name: "cert-ssl", flag: "cert", dependsOn: "ssl" }),
			],
			handler: ok,
		}),
	);
	const r = await app.test(["build", "--help"]);
	assert.ok(
		r.stdout.endsWith(
			"Constraints:\n" +
				"  fast-path    --fast implies --no-embeddings\n" +
				"  cert-ssl     --cert requires --ssl\n",
		),
		r.stdout,
	);
});

// --- The schema catalogue (§25.7's amendment, §26.11) ---

test("schema: the constraint catalogue publishes nesting as constraint-kind members", async () => {
	const { dumpSchemaCore } = await import("../src/schema.js");
	const app = authorApp();
	const dumped = dumpSchemaCore(app as never) as {
		commands: Record<string, { constraints: unknown[] }>;
	};
	assert.deepEqual(dumped.commands.rewrite?.constraints, [
		{
			type: "all_or_none",
			name: "author-name",
			members: [
				{ kind: "flag", name: "old-name", when: "present" },
				{ kind: "flag", name: "new-name", when: "present" },
			],
		},
		{
			type: "all_or_none",
			name: "author-email",
			members: [
				{ kind: "flag", name: "old-email", when: "present" },
				{ kind: "flag", name: "new-email", when: "present" },
			],
		},
		{
			type: "at_least_one",
			name: "author-change",
			// A nested member carries no `when` at all: it has no election of
			// its own, and a defaulted key that could be omitted is an erasure.
			members: [
				{ kind: "constraint", name: "author-name" },
				{ kind: "constraint", name: "author-email" },
			],
		},
	]);
});

test("schema: an arg member publishes kind `arg`, and `when` is always emitted", async () => {
	const { dumpSchemaCore } = await import("../src/schema.js");
	const dumped = dumpSchemaCore(purgeApp() as never) as {
		commands: Record<string, { constraints: unknown[] }>;
	};
	assert.deepEqual(dumped.commands.purge?.constraints, [
		{
			type: "at_least_one",
			name: "purge-selection",
			members: [
				{ kind: "arg", name: "targets", when: "non_empty" },
				{ kind: "flag", name: "older-than", when: "present" },
				{ kind: "flag", name: "larger-than", when: "present" },
				{ kind: "flag", name: "all", when: "true" },
			],
		},
	]);
});

// --- The MCP projection and the declared lossiness policy (§26.12) ---

function toolFor(app: App, name: string): { description: string } {
	const tool = asToolsForApp(app as never).find((tt) => tt.name === name);
	assert.ok(tool !== undefined, `no tool ${name}`);
	return tool;
}

test("mcp: safegit's site projects with no loss at all", () => {
	const app = authorApp();
	const cmd = (app as never as { commands: Map<string, unknown> }).commands.get(
		"rewrite",
	);
	const params = buildJSONSchema(cmd as never);
	// One `anyOf` branch per member; an all-or-none member becomes ONE branch
	// listing all of its leaves in `required`.
	assert.deepEqual(params.anyOf, [
		{ required: ["old_name", "new_name"] },
		{ required: ["old_email", "new_email"] },
	]);
	// Each all-or-none maps every member to all the others.
	assert.deepEqual(params.dependentRequired, {
		old_name: ["new_name"],
		new_name: ["old_name"],
		old_email: ["new_email"],
		new_email: ["old_email"],
	});
	// Exact everywhere, so no clause is appended.
	assert.ok(
		toolFor(app, "rewrite").description.endsWith(
			"Constraints (enforced at call time):\n" +
				"  all or none of: old_name, new_name\n" +
				"  all or none of: old_email, new_email\n" +
				"  at least one of: (old_name with new_name), (old_email with new_email)",
		),
		toolFor(app, "rewrite").description,
	);
});

test("mcp: a partial projection states its remainder from the closed reason set", () => {
	const app = purgeApp();
	const cmd = (app as never as { commands: Map<string, unknown> }).commands.get(
		"purge",
	);
	const params = buildJSONSchema(cmd as never);
	assert.deepEqual(params.anyOf, [
		{ required: ["targets"] },
		{ required: ["older_than"] },
		{ required: ["larger_than"] },
		{ required: ["all"] },
	]);
	assert.ok(
		toolFor(app, "purge").description.endsWith(
			"Constraints (enforced at call time):\n" +
				"  at least one of: targets, older_than, larger_than, all" +
				' -- not expressed in the schema: the "true" and "non_empty" selectors',
		),
		toolFor(app, "purge").description,
	);
});

test("mcp: requires projects dependentRequired, implies projects nothing and says so", () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("deploy", {
			help: "deploy",
			flags: {
				cert: flag("cert", t.str, { help: "hh", presence: "optional" }),
				ssl: flag("ssl", t.bool, {
					help: "hh",
					presence: "default",
					default: false,
				}),
				quiet_mode: flag("quiet-mode", t.bool, {
					help: "hh",
					presence: "default",
					default: false,
				}),
			},
			constraints: [
				requires({ name: "cert-ssl", flag: "cert", dependsOn: "ssl" }),
				implies({
					name: "hush",
					flag: "quiet-mode",
					implies: "ssl",
					value: true,
				}),
			],
			handler: ok,
		}),
	);
	const cmd = (app as never as { commands: Map<string, unknown> }).commands.get(
		"deploy",
	);
	const params = buildJSONSchema(cmd as never);
	assert.deepEqual(params.dependentRequired, { cert: ["ssl"] });
	assert.equal(params.anyOf, undefined);
	assert.ok(
		toolFor(app, "deploy").description.endsWith(
			"Constraints (enforced at call time):\n" +
				"  cert requires ssl\n" +
				"  quiet_mode implies ssl = true -- not expressed in the schema: the injection",
		),
		toolFor(app, "deploy").description,
	);
});

test("mcp: a FALSE implied value is a value, not a name", async () => {
	// §26.12's pin: `= false`, never `no_ssl`. A `no_`-prefixed property names
	// a key the schema does not carry and the caller can never send, and it
	// hides the one fact the line exists to publish. The CLI's `--no-`
	// negation stays where a reader types tokens (§26.10).
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("deploy", {
			help: "deploy",
			flags: {
				ssl: flag("ssl", t.bool, {
					help: "hh",
					presence: "default",
					default: true,
				}),
				insecure: flag("insecure", t.bool, {
					help: "hh",
					presence: "default",
					default: false,
				}),
			},
			constraints: [
				implies({
					name: "drop-ssl",
					flag: "insecure",
					implies: "ssl",
					value: false,
				}),
			],
			handler: ok,
		}),
	);
	assert.ok(
		toolFor(app, "deploy").description.endsWith(
			"  insecure implies ssl = false -- not expressed in the schema: the injection",
		),
		toolFor(app, "deploy").description,
	);
	// The help block keeps the token spelling, which is the surface where a
	// reader types tokens.
	assert.match(
		(await app.test(["deploy", "--help"])).stdout,
		/--insecure implies --no-ssl/,
	);
});

test("mcp: two at-least-one rules are conjoined rather than merged or dropped", () => {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("pick", {
			help: "pick",
			flags: {
				aa: flag("aa", t.str, { help: "hh", presence: "optional" }),
				bb: flag("bb", t.str, { help: "hh", presence: "optional" }),
				cc: flag("cc", t.str, { help: "hh", presence: "optional" }),
				dd: flag("dd", t.str, { help: "hh", presence: "optional" }),
			},
			constraints: [
				atLeastOne({
					name: "first",
					members: [{ name: "aa" }, { name: "bb" }],
				}),
				atLeastOne({
					name: "second",
					members: [{ name: "cc" }, { name: "dd" }],
				}),
			],
			handler: ok,
		}),
	);
	const cmd = (app as never as { commands: Map<string, unknown> }).commands.get(
		"pick",
	);
	const params = buildJSONSchema(cmd as never);
	assert.equal(params.anyOf, undefined);
	assert.deepEqual(params.allOf, [
		{ anyOf: [{ required: ["aa"] }, { required: ["bb"] }] },
		{ anyOf: [{ required: ["cc"] }, { required: ["dd"] }] },
	]);
});

test("mcp: an all-or-none over a nested group emits no keyword and names the nesting", () => {
	const app = authorApp();
	app.command(
		defineReadOnlyCommand("grouped", {
			help: "grouped",
			flags: {
				aa: flag("aa", t.str, { help: "hh", presence: "optional" }),
				bb: flag("bb", t.str, { help: "hh", presence: "optional" }),
				cc: flag("cc", t.str, { help: "hh", presence: "optional" }),
			},
			constraints: [
				atLeastOne({
					name: "inner",
					members: [{ name: "aa" }, { name: "bb" }],
				}),
				allOrNone({
					name: "outer",
					members: [{ name: "inner" }, { name: "cc" }],
				}),
			],
			handler: ok,
		}),
	);
	const cmd = (app as never as { commands: Map<string, unknown> }).commands.get(
		"grouped",
	);
	const params = buildJSONSchema(cmd as never);
	assert.deepEqual(params.dependentRequired, undefined);
	assert.ok(
		toolFor(app, "grouped").description.endsWith(
			"  at least one of: aa, bb\n" +
				"  all or none of: (aa or bb), cc -- not expressed in the schema: the nested grouping",
		),
		toolFor(app, "grouped").description,
	);
});

/**
 * An at-least-one over a nested pair whose selector sits `depth` levels below
 * the parent: depth 1 puts it on the nested pair's own member, depth 2 puts
 * it one level further down again.
 */
function nestedSelectorApp(depth: 1 | 2): App {
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("cmd", {
			help: "hh",
			flags: {
				aa: flag("aa", t.str, { help: "hh", presence: "optional" }),
				bb: flag("bb", t.bool, {
					help: "hh",
					presence: "default",
					default: false,
				}),
				cc: flag("cc", t.str, { help: "hh", presence: "optional" }),
			},
			constraints:
				depth === 1
					? [
							allOrNone({
								name: "inner",
								members: [{ name: "aa" }, { name: "bb", when: "true" }],
							}),
							atLeastOne({
								name: "outer",
								members: [{ name: "inner" }, { name: "cc" }],
							}),
						]
					: [
							allOrNone({
								name: "deep",
								members: [{ name: "aa" }, { name: "bb", when: "true" }],
							}),
							allOrNone({
								name: "inner",
								members: [{ name: "deep" }, { name: "cc" }],
							}),
							atLeastOne({
								name: "outer",
								members: [{ name: "inner" }, { name: "aa" }],
							}),
						],
			handler: ok,
		}),
	);
	return app;
}

test("mcp: at-least-one fidelity is RECURSIVE -- a nested selector makes it partial", () => {
	// §26.12's amendment: an at-least-one is exact when no election selector
	// appears at ANY depth beneath it. A nested member's leaves are what the
	// parent's branch publishes in `required`, so a `when: "true"` two levels
	// down is a rule the parent's own `anyOf` states falsely.
	for (const depth of [1, 2] as const) {
		const desc = toolFor(nestedSelectorApp(depth), "cmd").description;
		const line = desc
			.split("\n")
			.find((l) => l.startsWith("  at least one of:"));
		assert.ok(line !== undefined, desc);
		assert.ok(
			line.endsWith(
				' -- not expressed in the schema: the "true" and "non_empty" selectors',
			),
			`depth ${depth}: ${line}`,
		);
	}
});

test("mcp: safegit's nesting stays exact -- the property is the absent selector", () => {
	// §26.7's site declares four flags and no selector at any depth, which is
	// what made it the no-loss example rather than the nesting itself.
	const desc = toolFor(authorApp(), "rewrite").description;
	assert.ok(
		desc.endsWith(
			"  at least one of: (old_name with new_name), (old_email with new_email)",
		),
		desc,
	);
});

test("mcp: the rule keywords sit after `required` and before `additionalProperties`", () => {
	// §26.12's pin: the rule-carrying keywords sit beside the two keys they
	// qualify, ahead of the key that closes the object.
	const rewrite = (
		authorApp() as never as { commands: Map<string, unknown> }
	).commands.get("rewrite");
	assert.deepEqual(Object.keys(buildJSONSchema(rewrite as never)), [
		"type",
		"properties",
		"required",
		"anyOf",
		"dependentRequired",
		"additionalProperties",
	]);
	// Two at-least-one rules put `allOf` in the same position.
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("pick", {
			help: "pick",
			flags: {
				aa: flag("aa", t.str, { help: "hh", presence: "optional" }),
				bb: flag("bb", t.str, { help: "hh", presence: "optional" }),
				cc: flag("cc", t.str, { help: "hh", presence: "optional" }),
				dd: flag("dd", t.str, { help: "hh", presence: "optional" }),
			},
			constraints: [
				atLeastOne({
					name: "first",
					members: [{ name: "aa" }, { name: "bb" }],
				}),
				atLeastOne({
					name: "second",
					members: [{ name: "cc" }, { name: "dd" }],
				}),
			],
			handler: ok,
		}),
	);
	const pick = (
		app as never as { commands: Map<string, unknown> }
	).commands.get("pick");
	assert.deepEqual(Object.keys(buildJSONSchema(pick as never)), [
		"type",
		"properties",
		"required",
		"allOf",
		"additionalProperties",
	]);
	// A command with no constraints keeps the plain four-key shape.
	const plain = makeApp();
	plain.command(
		defineReadOnlyCommand("bare", {
			help: "bare",
			flags: { aa: flag("aa", t.str, { help: "hh", presence: "optional" }) },
			handler: ok,
		}),
	);
	const bare = (
		plain as never as { commands: Map<string, unknown> }
	).commands.get("bare");
	assert.deepEqual(Object.keys(buildJSONSchema(bare as never)), [
		"type",
		"properties",
		"required",
		"additionalProperties",
	]);
});

test("violation: the decline clause searches DIRECT members only", async () => {
	// §12.15's pin: the clause teaches about the sentence it is appended to,
	// and that sentence lists the PARENT's operands. A `--no-bb` one level down
	// names a token the reader is not looking at, and the nested group is left
	// vacuous rather than violated.
	const r = await nestedSelectorApp(1).test(["cmd", "--no-bb"]);
	assert.equal(
		r.stderr,
		'error: constraint "outer": at least one of (--aa with --bb), --cc is required\n' +
			"try 'myapp cmd --help'\n",
	);
	// The same decline on a DIRECT member does append the clause.
	const app = makeApp();
	app.command(
		defineReadOnlyCommand("direct", {
			help: "hh",
			flags: {
				bb: flag("bb", t.bool, {
					help: "hh",
					presence: "default",
					default: false,
				}),
				cc: flag("cc", t.str, { help: "hh", presence: "optional" }),
			},
			constraints: [
				atLeastOne({
					name: "outer",
					members: [{ name: "bb", when: "true" }, { name: "cc" }],
				}),
			],
			handler: ok,
		}),
	);
	assert.equal(
		(await app.test(["direct", "--no-bb"])).stderr,
		'error: constraint "outer": at least one of --bb, --cc is required ' +
			"(--no-bb declines an option; it does not choose one)\n" +
			"try 'myapp direct --help'\n",
	);
});
