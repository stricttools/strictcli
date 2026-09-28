/**
 * The naming rule and the framework-command reservation.
 *
 * Every identifier a caller types or references is lowercase kebab-case of at
 * least two characters, and a short form is one ASCII letter. `help` and
 * `version` are framework commands, reserved at every level of the command
 * tree. Each refusal names the rule, so each test also applies the fix it names
 * (a conforming spelling) and sees the declaration accepted.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createApp } from "../src/app.js";
import { parseChecksToml } from "../src/checks/framework.js";
import {
	atLeastOne,
	choice,
	choiceFlag,
	defineMutatingCommand,
	defineReadOnlyCommand,
	deprecated,
	flag,
	memberChoiceFlag,
	t,
} from "../src/index.js";

const CLAUSE =
	"must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*";

const NON_KEBAB = [
	"Compile",
	"compile_all",
	"COMPILE",
	"c",
	"fast-",
	"a--b",
	"-x",
	"9lives",
	"déploy",
];
const KEBAB = ["compile", "compile-all", "c0", "x86-64", "ab"];

function app() {
	return createApp({ name: "myapp", version: "1.0.0", help: "test app" });
}

function loose(v: unknown): never {
	return v as never;
}

function rejects(fn: () => unknown, message: string): void {
	assert.throws(fn, (e: unknown) => {
		assert.equal((e as Error).message, message);
		return true;
	});
}

function cmd(name: string) {
	return defineReadOnlyCommand(loose(name), { help: "x", handler: () => 0 });
}

test("command names follow the naming rule at every depth", () => {
	for (const n of NON_KEBAB) {
		rejects(() => app().command(cmd(n)), `command name "${n}" ${CLAUSE}`);
	}
	for (const n of KEBAB) {
		app().command(cmd(n));
		app().group("grp", { help: "a group" }).command(cmd(n));
	}
});

test("group and deprecated command names follow the naming rule", () => {
	for (const n of ["Db", "db_tools", "d", "db-"]) {
		rejects(
			() => app().group(n, { help: "a group" }),
			`group name "${n}" ${CLAUSE}`,
		);
		rejects(
			() => app().group("grp", { help: "g" }).group(n, { help: "a group" }),
			`group name "${n}" ${CLAUSE}`,
		);
		rejects(
			() => deprecated(loose(n), "gone"),
			`deprecated command name "${n}" ${CLAUSE}`,
		);
	}
	const a = app();
	a.group("db-tools", { help: "a group" }).group("sub", {
		help: "a sub group",
	});
	a.deprecate(deprecated("old-cmd", "gone"));
});

test("flag names follow the naming rule, at every depth", () => {
	for (const n of ["Device", "device_id", "DEVICE", "d", "m", "F", "device-"]) {
		rejects(
			() => flag(loose(n), t.str, { help: "help text", presence: "required" }),
			`flag name "${n}" ${CLAUSE}`,
		);
		rejects(
			() =>
				choiceFlag(
					loose(n),
					{
						one: choice({ help: "the first" }),
						two: choice({ help: "the second" }),
					},
					{ help: "pick", presence: "required" },
				),
			`flag name "${n}" ${CLAUSE}`,
		);
	}
	flag("device-id", t.str, { help: "help text", presence: "required" });
});

test("a member choice's name is a flag name", () => {
	rejects(
		() =>
			memberChoiceFlag(
				"profile",
				{
					w: choice({ help: "the work profile" }),
					home: choice({ help: "home" }),
				},
				{ help: "which profile", presence: "required" },
			),
		`flag name "w" ${CLAUSE}`,
	);
	memberChoiceFlag(
		"profile",
		{
			work: choice({ help: "the work profile" }),
			home: choice({ help: "home" }),
		},
		{ help: "which profile", presence: "required" },
	);
});

test("short forms are one ASCII letter", () => {
	for (const s of ["1", "ab", "é", "-", "_"]) {
		rejects(
			() =>
				flag("device", t.str, { help: "h", presence: "required", short: s }),
			`Flag "device": short form "${s}" must be one ASCII letter (a-z or A-Z)`,
		);
		rejects(
			() =>
				memberChoiceFlag(
					"profile",
					{
						work: choice({ help: "work", short: s }),
						home: choice({ help: "home" }),
					},
					{ help: "which profile", presence: "required" },
				),
			`Flag "work": short form "${s}" must be one ASCII letter (a-z or A-Z)`,
		);
	}
	for (const s of ["d", "D", "F", "m"]) {
		flag("device", t.str, { help: "h", presence: "required", short: s });
	}
});

test("token choice names follow the naming rule", () => {
	for (const n of ["a", "Email", "e_mail", "email-"]) {
		rejects(
			() =>
				choiceFlag(
					"via",
					loose({
						[n]: choice({ help: "one" }),
						sms: choice({ help: "text" }),
					}),
					{ help: "pick", presence: "required" },
				),
			`Flag "via": choice name "${n}" ${CLAUSE}`,
		);
	}
	choiceFlag(
		"via",
		{ "e-mail": choice({ help: "one" }), sms: choice({ help: "text" }) },
		{ help: "pick", presence: "required" },
	);
});

test("tag names follow the naming rule", () => {
	for (const tag of ["fast-", "a--b", "x", "Fast"]) {
		const msg = `invalid tag name "${tag}": ${CLAUSE}`;
		rejects(
			() =>
				app().command(
					defineReadOnlyCommand("cmd", {
						help: "x",
						tags: [tag],
						handler: () => 0,
					}),
				),
			msg,
		);
		rejects(() => app().group("grp", { help: "g", tags: [tag] }), msg);
	}
	app().command(
		defineReadOnlyCommand("cmd", {
			help: "x",
			tags: ["fast-lane"],
			handler: () => 0,
		}),
	);
});

test("constraint, grant and update resource names follow the naming rule", () => {
	const constrained = (name: string) =>
		defineReadOnlyCommand("cmd", {
			help: "x",
			flags: {
				file: flag("file", t.str, { help: "a file", presence: "optional" }),
				host: flag("host", t.str, { help: "a host", presence: "optional" }),
			},
			constraints: [
				atLeastOne({ name, members: [{ name: "file" }, { name: "host" }] }),
			],
			handler: () => 0,
		});
	rejects(
		() => constrained("p"),
		`command "cmd": constraint name "p" ${CLAUSE}`,
	);
	constrained("pick");

	const granted = (name: string) =>
		defineMutatingCommand("cmd", {
			help: "x",
			grants: [{ name, reason: "r", kind: "proc_mutate" }],
			handler: () => 0,
		});
	rejects(
		() => granted("push-"),
		`command "cmd": invalid grant name 'push-': ${CLAUSE}`,
	);
	granted("push-it");

	const updated = (resource: string) =>
		defineMutatingCommand("cmd", {
			help: "x",
			flags: {
				content: flag("content", t.str, {
					help: "the content",
					presence: "optional",
				}),
			},
			updateOf: { resource, writeMode: "sparse", properties: ["content"] },
			handler: () => 0,
		});
	rejects(() => updated("r"), `command "cmd": update resource "r" ${CLAUSE}`);
	updated("dns-record");
});

function checksToml(name: string, hook?: string): string {
	let text =
		`app = "myapp"\n\n[checks.${name}]\ndescription = "d"\nsubject = "quality"\n` +
		'tags = ["fast-lane"]\nseverity = "error"\nfast = true\npure = true\n' +
		"needs_network = false\ndepends_on = []\n";
	if (hook !== undefined) {
		text += `\n[hooks."${hook}"]\ntag = "fast-lane"\n`;
	}
	return text;
}

test("check and hook names follow the naming rule", () => {
	for (const n of ["x", "lint-", "lint--code"]) {
		rejects(
			() => parseChecksToml(checksToml(`"${n}"`)),
			`checks.toml: invalid check name "${n}" (${CLAUSE})`,
		);
	}
	parseChecksToml(checksToml("lint-code"));
	for (const h of ["p", "pre-", "Pre-push"]) {
		rejects(
			() => parseChecksToml(checksToml("lint-code", h)),
			`checks.toml: invalid hook name "${h}" (${CLAUSE})`,
		);
	}
	parseChecksToml(checksToml("lint-code", "pre-push"));
});

test("help and version are reserved at every level of the command tree", () => {
	const reserved =
		"is reserved: help and version are framework commands at every level of the command tree";
	for (const n of ["help", "version"]) {
		rejects(() => app().command(cmd(n)), `command name "${n}" ${reserved}`);
		rejects(
			() => app().group(n, { help: "g" }),
			`group name "${n}" ${reserved}`,
		);
		rejects(
			() => app().group("grp", { help: "g" }).group(n, { help: "g" }),
			`group name "${n}" ${reserved}`,
		);
		rejects(
			() => deprecated(loose(n), "gone"),
			`deprecated command name "${n}" ${reserved}`,
		);
	}
	// The fix the refusal names: any other name.
	const a = app();
	a.command(cmd("show-help"));
	a.group("versions", { help: "g" }).command(cmd("list"));
});

test("an empty short is no short", () => {
	flag("device", t.str, { help: "h", presence: "required", short: "" });
});
