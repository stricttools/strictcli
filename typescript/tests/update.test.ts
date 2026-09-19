/**
 * The update-command construct (contract §27, §12.16).
 *
 * The fixture is the contract's own worked example -- a sparse DNS-record
 * update with two identity members and three properties, one of them nullable
 * and one of them a bool -- so every rendering below is checked against the
 * declaration the contract renders in its own text.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import type { App } from "../src/app.js";
import {
	arg,
	atLeastOne,
	choice,
	choiceFlag,
	createApp,
	defineMutatingCommand,
	defineReadOnlyCommand,
	flag,
	implies,
	mutatingPassthrough,
	relativeToRoot,
	t,
} from "../src/index.js";
import { schemaJson } from "../src/schema.js";
import { tempDir } from "./helpers.js";

const ok = () => undefined;

function tiny(): App {
	return createApp({ name: "t", version: "1.0.0", help: "t" });
}

/** The contract's own worked example (§27.8). */
function updateFixture(
	handler: (args: never, ctx: never) => number | undefined = ok,
	extra: Record<string, unknown> = {},
): App {
	const app = createApp({
		name: "dnsapp",
		version: "1.0.0",
		help: "manage DNS",
	});
	app.command(
		defineMutatingCommand("update-record", {
			help: "change one DNS record in place",
			updateOf: {
				resource: "dns-record",
				writeMode: "sparse",
				identity: ["zone", "record-id"],
				properties: ["content", "ttl", "proxied"],
			},
			flags: {
				zone: flag("zone", t.str, {
					help: "zone the record belongs to",
					presence: "required",
				}),
				record_id: flag("record-id", t.str, {
					help: "identifier of the record to change",
					presence: "required",
				}),
				content: flag("content", t.str, {
					help: "record content",
					presence: "optional",
				}),
				ttl: flag("ttl", t.int, {
					help: "time to live in seconds",
					presence: "optional",
					nullable: true,
				}),
				proxied: flag("proxied", t.bool, {
					help: "whether the record is proxied",
					presence: "optional",
				}),
			},
			handler: handler as never,
			...extra,
		}),
	);
	return app;
}

function message(fn: () => unknown): string {
	try {
		fn();
	} catch (e) {
		return (e as Error).message;
	}
	assert.fail("expected a registration error");
}

// --- §27.1: the mutating-default ban ---

test("the ban refuses a value default on a flag of a mutating command", () => {
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					flags: {
						ttl: flag("ttl", t.int, {
							help: "time to live",
							presence: "default",
							default: 300n,
						}),
					},
					handler: ok,
				}),
			),
		),
		'command "u": flag \'--ttl\' declares presence: "default" with default: 300 on a mutating command: absence would write a value the invocation never stated (declare presence: "required" or presence: "optional", or apply the fallback in the handler and say so in its help)',
	);
});

test("the ban refuses a value default on a positional arg", () => {
	// The presence spelling inside the sentence takes the FLAG spelling even
	// when the subject is an arg (§12.16): the prefix names the command rather
	// than a surface.
	assert.match(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					args: [
						arg("target", t.str, {
							help: "where",
							presence: "default",
							default: "prod",
						}),
					],
					handler: ok,
				}),
			),
		),
		/^command "u": argument 'target' declares presence: "default" with default: prod on a mutating command/,
	);
});

test("the ban reaches every scalar, the empty ones included", () => {
	const refuse = (name: string, f: unknown): string =>
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					flags: { [name]: f } as never,
					handler: ok,
				}),
			),
		);
	assert.match(
		refuse(
			"content",
			flag("content", t.str, {
				help: "content",
				presence: "default",
				default: "",
			}),
		),
		/flag '--content' declares presence: "default" with default: {2}on a mutating command/,
	);
	assert.match(
		refuse(
			"ttl",
			flag("ttl", t.int, { help: "ttl", presence: "default", default: 0n }),
		),
		/flag '--ttl' declares presence: "default" with default: 0 on a mutating command/,
	);
	assert.match(
		refuse(
			"proxied",
			flag("proxied", t.bool, {
				help: "proxied",
				presence: "default",
				default: false,
			}),
		),
		/flag '--proxied' declares presence: "default" with default: false on a mutating command/,
	);
	assert.match(
		refuse(
			"proxied",
			flag("proxied", t.bool, {
				help: "proxied",
				presence: "default",
				default: true,
			}),
		),
		/flag '--proxied' declares presence: "default" with default: true on a mutating command/,
	);
	assert.match(
		refuse(
			"rate",
			flag("rate", t.float, {
				help: "rate",
				presence: "default",
				default: 1.5,
			}),
		),
		/flag '--rate' declares presence: "default" with default: 1.5 on a mutating command/,
	);
});

test("the ban reaches a NON-EMPTY compound default", () => {
	assert.match(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					flags: {
						tag: flag("tag", t.list(t.str), {
							help: "tags",
							presence: "default",
							default: ["a"],
						}),
					},
					handler: ok,
				}),
			),
		),
		/flag '--tag' declares presence: "default" with default: a on a mutating command/,
	);
});

// The two carve-outs and the two exemptions, each of which must REGISTER.
test("the ban's carve-outs and exemptions all register", () => {
	// An empty collection declares no elements.
	tiny().command(
		defineMutatingCommand("u", {
			help: "update",
			flags: {
				tag: flag("tag", t.list(t.str), {
					help: "tags",
					presence: "default",
					default: [],
				}),
				header: flag("header", t.dict(t.str), {
					help: "headers",
					presence: "default",
					default: new Map(),
				}),
			},
			handler: ok,
		}),
	);
	// A relativeToRoot default decides WHERE, never WHAT.
	createApp({
		name: "t",
		version: "1.0.0",
		help: "t",
		infraRoot: { T_HOME: "~/.t" },
	}).command(
		defineMutatingCommand("u", {
			help: "update",
			flags: {
				path: flag("path", t.str, {
					help: "a path",
					presence: "default",
					default: relativeToRoot("T_HOME", "store"),
				}),
			},
			handler: ok,
		}),
	);
	// A read_only command writes no value, invented or otherwise.
	tiny().command(
		defineReadOnlyCommand("r", {
			help: "read",
			flags: {
				ttl: flag("ttl", t.int, {
					help: "ttl",
					presence: "default",
					default: 300n,
				}),
			},
			handler: ok,
		}),
	);
	// An app-level global is NOT reached (§27.1's stated hole).
	createApp({
		name: "t",
		version: "1.0.0",
		help: "t",
		flags: {
			depth: flag("depth", t.int, {
				help: "depth",
				presence: "default",
				default: 3n,
			}),
		},
	}).command(defineMutatingCommand("u", { help: "update", handler: ok }));
});

test("the ban reaches a flag set's flag, per attaching command", () => {
	// A shared flag set carrying a default is legal; ATTACHING it to a mutating
	// command is not -- the ban is evaluated per command, over the flags that
	// command carries (§27.1, §18.33 item 302).
	const shared = {
		kind: "flag-set" as const,
		name: "shared",
		flags: {
			ttl: flag("ttl", t.int, {
				help: "time to live",
				presence: "default" as const,
				default: 300n,
			}),
		},
	};
	tiny().command(
		defineReadOnlyCommand("r", {
			help: "read",
			flagSets: [shared],
			handler: ok,
		}),
	);
	assert.match(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					flagSets: [shared],
					handler: ok,
				}),
			),
		),
		/command "u": flag '--ttl' declares presence: "default" with default: 300 on a mutating command/,
	);
});

test("the ban spares the selector and reaches its scope, at every depth", () => {
	// A choice name is not a value written to anything: it names which scope is
	// live. The flags INSIDE the scope are ordinary flags of a mutating
	// command, reached at every depth.
	assert.match(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					flags: {
						via: choiceFlag(
							"via",
							{
								webhook: choice({
									help: "post to a URL",
									flags: {
										retries: flag("retries", t.int, {
											help: "attempts",
											presence: "default",
											default: 3n,
										}),
									},
								}),
								email: choice({
									help: "send an email",
									flags: {
										subject: flag("subject", t.str, {
											help: "subject",
											presence: "optional",
										}),
									},
								}),
							},
							{ help: "channel", presence: "default", default: "webhook" },
						),
					},
					handler: ok,
				}),
			),
		),
		/command "u": flag '--retries' declares presence: "default" with default: 3 on a mutating command/,
	);
	tiny().command(
		defineMutatingCommand("u", {
			help: "update",
			flags: {
				via: choiceFlag(
					"via",
					{
						webhook: choice({
							help: "post to a URL",
							flags: {
								retries: flag("retries", t.int, {
									help: "attempts",
									presence: "optional",
								}),
							},
						}),
						email: choice({
							help: "send an email",
							flags: {
								subject: flag("subject", t.str, {
									help: "subject",
									presence: "optional",
								}),
							},
						}),
					},
					{ help: "channel", presence: "default", default: "webhook" },
				),
			},
			handler: ok,
		}),
	);
});

// --- §27.2, §27.3, §27.11: the registration guards, in the pinned order ---

test("update_of on a read_only command is refused", () => {
	assert.equal(
		message(() =>
			tiny().command(
				defineReadOnlyCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						properties: ["content"],
					},
					flags: {
						content: flag("content", t.str, {
							help: "content",
							presence: "optional",
						}),
					},
					handler: ok,
				}),
			),
		),
		'command "u": a read_only command cannot declare update_of (a command that changes nothing writes no properties)',
	);
});

test("the write mode's vocabulary is closed", () => {
	// TypeScript's reachable input is a WIDENED caller past the literal union
	// (§12.13 item 213's treatment).
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "patch" as "sparse",
						properties: ["content"],
					},
					flags: {
						content: flag("content", t.str, {
							help: "content",
							presence: "optional",
						}),
					},
					handler: ok,
				}),
			),
		),
		'command "u": invalid write_mode "patch": must be "sparse" or "full_replace"',
	);
});

test("the resource name takes the constraint-name charset", () => {
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "DNS_Record",
						writeMode: "sparse",
						properties: ["content"],
					},
					flags: {
						content: flag("content", t.str, {
							help: "content",
							presence: "optional",
						}),
					},
					handler: ok,
				}),
			),
		),
		'command "u": update resource "DNS_Record" must match [a-z][a-z0-9-]*',
	);
});

test("an update with no properties is refused", () => {
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						identity: ["id"],
						properties: [] as unknown as readonly ["id"],
					},
					flags: {
						id: flag("id", t.str, { help: "id", presence: "required" }),
					},
					handler: ok,
				}),
			),
		),
		'command "u": update of "thing" declares no properties: an update with nothing to write is not an update',
	);
});

test("the name-resolution refusals", () => {
	// unknown -- reachable only through a widened caller, which is exactly the
	// covering input §12.13 item 213 establishes.
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						properties: ["nope"] as unknown as readonly ["content"],
					},
					flags: {
						content: flag("content", t.str, {
							help: "content",
							presence: "optional",
						}),
					},
					handler: ok,
				}),
			),
		),
		'command "u": update of "thing" references unknown name "nope"',
	);
	// ambiguous
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						identity: ["target"],
						properties: ["content"],
					},
					flags: {
						target: flag("target", t.str, {
							help: "a flag",
							presence: "optional",
						}),
						content: flag("content", t.str, {
							help: "content",
							presence: "optional",
						}),
					},
					args: [
						arg("target", t.str, { help: "an arg", presence: "optional" }),
					],
					handler: ok,
				}),
			),
		),
		'command "u": update of "thing" references "target", which names both a flag and a positional arg',
	);
	// duplicated
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						properties: ["content", "content"],
					},
					flags: {
						content: flag("content", t.str, {
							help: "content",
							presence: "optional",
						}),
					},
					handler: ok,
				}),
			),
		),
		'command "u": update of "thing" declares "content" twice',
	);
	// both roles
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						identity: ["content"],
						properties: ["content"],
					},
					flags: {
						content: flag("content", t.str, {
							help: "content",
							presence: "optional",
						}),
					},
					handler: ok,
				}),
			),
		),
		'command "u": update of "thing" declares "content" as both identity and property',
	);
	// scoped -- a scoped flag resolves and is refused by the scope step, never
	// reported as unknown (§24.8, §27.3).
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						properties: ["subject"] as unknown as readonly ["via"],
					},
					flags: {
						via: choiceFlag(
							"via",
							{
								email: choice({
									help: "by email",
									flags: {
										subject: flag("subject", t.str, {
											help: "subject",
											presence: "optional",
										}),
									},
								}),
								sms: choice({
									help: "by sms",
									flags: {
										number: flag("number", t.str, {
											help: "number",
											presence: "optional",
										}),
									},
								}),
							},
							{ help: "channel", presence: "required" },
						),
					},
					handler: ok,
				}),
			),
		),
		`command "u": update of "thing" references 'subject', which is declared under '--via email': an update's identity and properties are declared at root scope only`,
	);
});

test("a property may not be a positional arg", () => {
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						properties: ["content"],
					},
					args: [
						arg("content", t.str, { help: "content", presence: "optional" }),
					],
					handler: ok,
				}),
			),
		),
		'command "u": update of "thing" property "content" is a positional arg: a property must be individually omissible and clearable, and only a flag is',
	);
});

test("a property may not be a choice flag", () => {
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						properties: ["via"],
					},
					flags: {
						via: choiceFlag(
							"via",
							{
								email: choice({ help: "by email" }),
								sms: choice({ help: "by sms" }),
							},
							{ help: "channel", presence: "required" },
						),
					},
					handler: ok,
				}),
			),
		),
		`command "u": update of "thing" property '--via' is a choice flag: an elected record is a selection, not a property value`,
	);
});

test("a property declares optional and nothing else", () => {
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						properties: ["content"],
					},
					flags: {
						content: flag("content", t.str, {
							help: "content",
							presence: "required",
						}),
					},
					handler: ok,
				}),
			),
		),
		`command "u": update of "thing" property flag '--content' declares presence: "required": a property is absent exactly when it is not being written, and the presence declaration for that is presence: "optional"`,
	);
});

test("an identity member may be an arg or a choice flag, and may be optional", () => {
	tiny().command(
		defineMutatingCommand("u", {
			help: "update",
			updateOf: {
				resource: "thing",
				writeMode: "sparse",
				identity: ["record-id", "addressing", "name"],
				properties: ["content"],
			},
			flags: {
				addressing: choiceFlag(
					"addressing",
					{
						"by-id": choice({ help: "address it by id" }),
						"by-name": choice({ help: "address it by name" }),
					},
					{ help: "how the resource is addressed", presence: "required" },
				),
				name: flag("name", t.str, { help: "the name", presence: "optional" }),
				content: flag("content", t.str, {
					help: "content",
					presence: "optional",
				}),
			},
			args: [arg("record-id", t.str, { help: "the id", presence: "required" })],
			handler: ok,
		}),
	);
});

test("nullable off a property is refused", () => {
	// On a command with no update at all.
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					flags: {
						content: flag("content", t.str, {
							help: "content",
							presence: "optional",
							nullable: true,
						}),
					},
					handler: ok,
				}),
			),
		),
		`command "u": flag '--content' declares nullable: true but is not a property of an update: only a property can be cleared`,
	);
	// On an identity member of an update.
	assert.match(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						identity: ["zone"],
						properties: ["content"],
					},
					flags: {
						zone: flag("zone", t.str, {
							help: "zone",
							presence: "optional",
							nullable: true,
						}),
						content: flag("content", t.str, {
							help: "content",
							presence: "optional",
						}),
					},
					handler: ok,
				}),
			),
		),
		/command "u": flag '--zone' declares nullable: true but is not a property of an update/,
	);
});

test("the minted unset name is reserved", () => {
	assert.equal(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						properties: ["content"],
					},
					flags: {
						content: flag("content", t.str, {
							help: "content",
							presence: "optional",
							nullable: true,
						}),
						unset_content: flag("unset-content", t.str, {
							help: "a flag of that name",
							presence: "optional",
						}),
					},
					handler: ok,
				}),
			),
		),
		`command "u": flag name "unset-content" is reserved: property '--content' declares nullable: true, which mints '--unset-content'`,
	);
});

test("the unset-name reservation reaches the app's globals", () => {
	// A global is recognized after the command name too, so a global of the
	// minted name would be unreachable behind the clear spelling.
	const app = createApp({
		name: "t",
		version: "1.0.0",
		help: "t",
		flags: {
			unset_content: flag("unset-content", t.str, {
				help: "a global of that name",
				presence: "optional",
			}),
		},
	});
	assert.equal(
		message(() =>
			app.command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						properties: ["content"],
					},
					flags: {
						content: flag("content", t.str, {
							help: "content",
							presence: "optional",
							nullable: true,
						}),
					},
					handler: ok,
				}),
			),
		),
		`command "u": flag name "unset-content" is reserved: property '--content' declares nullable: true, which mints '--unset-content'`,
	);
});

// The pinned order matters only where one declaration carries two faults.
test("the registration order is pinned", () => {
	// The ban runs ahead of every update step.
	assert.match(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "BAD-NAME",
						writeMode: "sparse",
						properties: ["nope"] as unknown as readonly ["ttl"],
					},
					flags: {
						ttl: flag("ttl", t.int, {
							help: "ttl",
							presence: "default",
							default: 300n,
						}),
					},
					handler: ok,
				}),
			),
		),
		/flag '--ttl' declares presence: "default" with default: 300 on a mutating command/,
	);
	// Classification runs ahead of record legality.
	assert.match(
		message(() =>
			tiny().command(
				defineReadOnlyCommand("u", {
					help: "update",
					updateOf: {
						resource: "BAD-NAME",
						writeMode: "sparse",
						properties: ["nope"] as unknown as readonly [string],
					},
					handler: ok,
				}),
			),
		),
		/a read_only command cannot declare update_of/,
	);
	// The record's own legality runs ahead of the names it carries.
	assert.match(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "BAD-NAME",
						writeMode: "sparse",
						properties: ["nope"] as unknown as readonly [string],
					},
					handler: ok,
				}),
			),
		),
		/update resource "BAD-NAME" must match/,
	);
	// Role legality runs ahead of presence legality.
	assert.match(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						properties: ["content"],
					},
					args: [
						arg("content", t.str, { help: "content", presence: "required" }),
					],
					handler: ok,
				}),
			),
		),
		/property "content" is a positional arg/,
	);
	// The name reservation runs LAST: the nullable-off-a-property refusal is
	// step 8's first half and the reservation its second, so a declaration
	// carrying both reports the first.
	assert.match(
		message(() =>
			tiny().command(
				defineMutatingCommand("u", {
					help: "update",
					updateOf: {
						resource: "thing",
						writeMode: "sparse",
						properties: ["content"],
					},
					flags: {
						zone: flag("zone", t.str, {
							help: "zone",
							presence: "optional",
							nullable: true,
						}),
						content: flag("content", t.str, {
							help: "content",
							presence: "optional",
							nullable: true,
						}),
						unset_content: flag("unset-content", t.str, {
							help: "collides",
							presence: "optional",
						}),
					},
					handler: ok,
				}),
			),
		),
		/flag '--zone' declares nullable: true but is not a property of an update/,
	);
});

// --- §27.4: the at-least-one-property rule ---

test("at least one property is required", async () => {
	const r = await updateFixture().test([
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
	]);
	assert.equal(r.exitCode, 1);
	assert.ok(
		r.stderr.startsWith(
			'error: update "dns-record": at least one property is required: --content, --ttl, --proxied\n',
		),
		r.stderr,
	);
});

test("a negated bool property is a provision, not a decline", async () => {
	// Inside an update command `--no-proxied` WRITES false, so it satisfies the
	// rule rather than declining it -- which is why §12.16 pins that the
	// sentence carries no decline clause.
	let got: unknown;
	let provided = false;
	const app = updateFixture(((
		args: { proxied: unknown },
		ctx: { provided: (n: string) => boolean },
	) => {
		got = args.proxied;
		provided = ctx.provided("proxied");
		return 0;
	}) as never);
	const r = await app.test([
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
		"--no-proxied",
	]);
	assert.equal(r.exitCode, 0, r.stderr);
	assert.equal(got, false);
	assert.equal(provided, true);
});

test("an env-provided property satisfies the rule", async () => {
	// There is no source filter: the framework has exactly one definition of
	// "was this supplied" (§23.6), and the containment is that the write set is
	// rendered, so a configured value cannot join a write invisibly.
	const app = tiny();
	app.command(
		defineMutatingCommand("u", {
			help: "update",
			updateOf: {
				resource: "thing",
				writeMode: "sparse",
				properties: ["content", "ttl"],
			},
			flags: {
				content: flag("content", t.str, {
					help: "content",
					presence: "optional",
					env: "T_CONTENT",
				}),
				ttl: flag("ttl", t.int, { help: "ttl", presence: "optional" }),
			},
			handler: ok,
		}),
	);
	process.env.T_CONTENT = "from-env";
	try {
		const r = await app.test(["--json", "u"]);
		assert.equal(r.exitCode, 0, r.stderr);
		assert.ok(r.stdout.includes('"written":["content"]'), r.stdout);
	} finally {
		delete process.env.T_CONTENT;
	}
});

// --- §27.6: the clear vocabulary ---

test("an unset delivers absence and reports provided", async () => {
	let present = false;
	let value: unknown = "unset";
	let provided = false;
	let unset = false;
	let untouchedUnset = true;
	const app = updateFixture(((
		args: Record<string, unknown>,
		ctx: { provided: (n: string) => boolean; unset: (n: string) => boolean },
	) => {
		present = Object.hasOwn(args, "ttl");
		value = args.ttl;
		provided = ctx.provided("ttl");
		unset = ctx.unset("ttl");
		untouchedUnset = ctx.unset("content");
		return 0;
	}) as never);
	const r = await app.test([
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
		"--unset-ttl",
	]);
	assert.equal(r.exitCode, 0, r.stderr);
	assert.equal(present, true);
	assert.equal(value, undefined);
	assert.equal(provided, true);
	assert.equal(unset, true);
	assert.equal(untouchedUnset, false);
});

test("unset accepts dashed and underscored names", async () => {
	let dashed = false;
	let underscored = false;
	const app = tiny();
	app.command(
		defineMutatingCommand("u", {
			help: "update",
			updateOf: {
				resource: "thing",
				writeMode: "sparse",
				properties: ["phone-number"],
			},
			flags: {
				phone_number: flag("phone-number", t.str, {
					help: "the number",
					presence: "optional",
					nullable: true,
				}),
			},
			handler: ((_a: unknown, ctx: { unset: (n: string) => boolean }) => {
				dashed = ctx.unset("phone-number");
				underscored = ctx.unset("phone_number");
				return 0;
			}) as never,
		}),
	);
	const r = await app.test(["u", "--unset-phone-number"]);
	assert.equal(r.exitCode, 0, r.stderr);
	assert.equal(dashed, true);
	assert.equal(underscored, true);
});

test("unset on an unknown name throws like provided", async () => {
	const app = updateFixture(((
		_a: unknown,
		ctx: { unset: (n: string) => boolean },
	) => {
		ctx.unset("nope");
		return 0;
	}) as never);
	await assert.rejects(
		() =>
			app.test([
				"update-record",
				"--zone",
				"z1",
				"--record-id",
				"r7",
				"--content",
				"x",
			]),
		/nope/,
	);
});

test("a value and an unset together is a parse error", async () => {
	const r = await updateFixture().test([
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
		"--ttl",
		"300",
		"--unset-ttl",
	]);
	assert.equal(r.exitCode, 1);
	assert.ok(
		r.stderr.startsWith(
			"error: --ttl and --unset-ttl are mutually exclusive: a property is either written or cleared\n",
		),
		r.stderr,
	);
});

test("the minted unset flag is not negatable", async () => {
	const r = await updateFixture().test([
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
		"--no-unset-ttl",
	]);
	assert.equal(r.exitCode, 1);
	assert.ok(r.stderr.includes("unknown flag '--no-unset-ttl'"), r.stderr);
});

test("an unset on a non-nullable property names nothing", async () => {
	const r = await updateFixture().test([
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
		"--unset-content",
	]);
	assert.equal(r.exitCode, 1);
	assert.ok(r.stderr.includes("unknown flag '--unset-content'"), r.stderr);
});

test("--unset-x=value takes the ordinary unknown-flag path", async () => {
	const r = await updateFixture().test([
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
		"--unset-ttl=5",
	]);
	assert.equal(r.exitCode, 1);
	assert.ok(r.stderr.includes("unknown flag '--unset-ttl'"), r.stderr);
});

test("the empty string is an ordinary value", async () => {
	let got: unknown;
	const app = updateFixture(((args: { content: unknown }) => {
		got = args.content;
		return 0;
	}) as never);
	const r = await app.test([
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
		"--content",
		"",
	]);
	assert.equal(r.exitCode, 0, r.stderr);
	assert.equal(got, "");
});

// --- §27.5: the write set's two renderings ---

test("the write-set line takes no sequence number", async () => {
	const app = updateFixture(((
		_a: unknown,
		ctx: { effects: { http: (m: string, u: string) => unknown } },
	) => {
		ctx.effects.http(
			"PATCH",
			"https://api.example.com/zones/z1/dns_records/r7",
		);
		return 0;
	}) as never);
	const r = await app.test([
		"--dry-run",
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
		"--content",
		"1.2.3.4",
		"--unset-ttl",
	]);
	assert.equal(
		r.stdout,
		"DRY RUN — no changes were made. Would do:\n" +
			"  writes: content; clears: ttl (other properties unchanged)\n" +
			"  1. net: PATCH https://api.example.com/zones/z1/dns_records/r7\n",
	);
});

test("the write-set line's pinned forms", async () => {
	const cases: [string, string[], "sparse" | "full_replace", string][] = [
		[
			"one written",
			["--content", "x"],
			"sparse",
			"  writes: content (other properties unchanged)",
		],
		[
			"two written, declaration order",
			["--ttl", "5", "--content", "x"],
			"sparse",
			"  writes: content, ttl (other properties unchanged)",
		],
		[
			"both segments",
			["--content", "x", "--unset-ttl"],
			"sparse",
			"  writes: content; clears: ttl (other properties unchanged)",
		],
		[
			"clears only",
			["--unset-ttl"],
			"sparse",
			"  clears: ttl (other properties unchanged)",
		],
		[
			"full replace",
			["--content", "x"],
			"full_replace",
			"  writes: content (other properties are re-sent as read)",
		],
	];
	for (const [name, argv, mode, want] of cases) {
		const app = createApp({
			name: "dnsapp",
			version: "1.0.0",
			help: "manage DNS",
		});
		app.command(
			defineMutatingCommand("update-record", {
				help: "change one DNS record in place",
				updateOf: {
					resource: "dns-record",
					writeMode: mode,
					properties: ["content", "ttl", "proxied"],
				},
				flags: {
					content: flag("content", t.str, {
						help: "record content",
						presence: "optional",
					}),
					ttl: flag("ttl", t.int, {
						help: "time to live in seconds",
						presence: "optional",
						nullable: true,
					}),
					proxied: flag("proxied", t.bool, {
						help: "whether the record is proxied",
						presence: "optional",
					}),
				},
				handler: ok,
			}),
		);
		const r = await app.test(["--dry-run", "update-record", ...argv]);
		assert.equal(
			r.stdout,
			`DRY RUN — no changes were made. Would do:\n${want}\n`,
			name,
		);
	}
});

test("the write-set line renders in dry mode only", async () => {
	const r = await updateFixture().test([
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
		"--content",
		"x",
	]);
	assert.ok(!r.stdout.includes("writes:"), r.stdout);
});

test("the envelope carries the write set in both modes", async () => {
	for (const dry of [false, true]) {
		const argv = [
			"--json",
			"update-record",
			"--zone",
			"z1",
			"--record-id",
			"r7",
			"--content",
			"1.2.3.4",
			"--unset-ttl",
		];
		const r = await updateFixture().test(dry ? ["--dry-run", ...argv] : argv);
		const env = JSON.parse(r.stdout) as Record<string, unknown>;
		assert.equal(env.interface_version, 2);
		assert.deepEqual(env.writes, {
			resource: "dns-record",
			write_mode: "sparse",
			written: ["content"],
			cleared: ["ttl"],
			resent: [],
			untouched: ["proxied"],
		});
	}
});

test("the envelope's write-set key order is pinned", async () => {
	const r = await updateFixture().test([
		"--json",
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
		"--content",
		"x",
	]);
	assert.ok(
		r.stdout.includes(
			'"writes":{"resource":"dns-record","write_mode":"sparse","written":["content"],"cleared":[],"resent":[],"untouched":["ttl","proxied"]}',
		),
		r.stdout,
	);
});

test("full replace swaps resent and untouched", async () => {
	const app = tiny();
	app.command(
		defineMutatingCommand("u", {
			help: "update",
			updateOf: {
				resource: "thing",
				writeMode: "full_replace",
				properties: ["content", "ttl"],
			},
			flags: {
				content: flag("content", t.str, {
					help: "content",
					presence: "optional",
				}),
				ttl: flag("ttl", t.int, { help: "ttl", presence: "optional" }),
			},
			handler: ok,
		}),
	);
	const r = await app.test(["--json", "u", "--content", "x"]);
	assert.ok(
		r.stdout.includes(
			'"writes":{"resource":"thing","write_mode":"full_replace","written":["content"],"cleared":[],"resent":["ttl"],"untouched":[]}',
		),
		r.stdout,
	);
});

test("a command with no update carries a null writes member", async () => {
	const app = tiny();
	app.command(defineReadOnlyCommand("r", { help: "read", handler: ok }));
	const r = await app.test(["--json", "r"]);
	assert.ok(r.stdout.includes('"writes":null'), r.stdout);
});

test("the envelope uses underscored parameter names", async () => {
	const app = tiny();
	app.command(
		defineMutatingCommand("u", {
			help: "update",
			updateOf: {
				resource: "thing",
				writeMode: "sparse",
				properties: ["phone-number", "display-name"],
			},
			flags: {
				phone_number: flag("phone-number", t.str, {
					help: "the number",
					presence: "optional",
				}),
				display_name: flag("display-name", t.str, {
					help: "the name",
					presence: "optional",
				}),
			},
			handler: ok,
		}),
	);
	const r = await app.test(["--json", "u", "--phone-number", "555"]);
	assert.ok(
		r.stdout.includes(
			'"written":["phone_number"],"cleared":[],"resent":[],"untouched":["display_name"]',
		),
		r.stdout,
	);
});

test("the human line uses declared names without the prefix", async () => {
	const app = tiny();
	app.command(
		defineMutatingCommand("u", {
			help: "update",
			updateOf: {
				resource: "thing",
				writeMode: "sparse",
				properties: ["phone-number"],
			},
			flags: {
				phone_number: flag("phone-number", t.str, {
					help: "the number",
					presence: "optional",
				}),
			},
			handler: ok,
		}),
	);
	const r = await app.test(["--dry-run", "u", "--phone-number", "555"]);
	assert.ok(
		r.stdout.includes("  writes: phone-number (other properties unchanged)\n"),
		r.stdout,
	);
});

// --- §27.6: help rendering ---

test("a nullable property renders its minted spelling on one line", async () => {
	const r = await updateFixture().test(["update-record", "--help"]);
	for (const want of [
		"--content <str>",
		"--ttl <int>, --unset-ttl",
		"--proxied, --no-proxied",
	]) {
		assert.ok(r.stdout.includes(want), `${want}\n${r.stdout}`);
	}
	// One presence part per line, and no second line for the minted spelling.
	assert.equal(r.stdout.split("--unset-ttl").length - 1, 1, r.stdout);
	assert.equal(r.stdout.split("[optional]").length - 1, 3, r.stdout);
});

test("a nullable bool renders all three spellings", async () => {
	const app = tiny();
	app.command(
		defineMutatingCommand("u", {
			help: "update",
			updateOf: {
				resource: "thing",
				writeMode: "sparse",
				properties: ["proxied"],
			},
			flags: {
				proxied: flag("proxied", t.bool, {
					help: "whether the record is proxied",
					presence: "optional",
					nullable: true,
				}),
			},
			handler: ok,
		}),
	);
	const r = await app.test(["u", "--help"]);
	assert.ok(
		r.stdout.includes("--proxied, --no-proxied, --unset-proxied"),
		r.stdout,
	);
});

// --- §27.9: the schema encoding ---

function dumpText(app: App): string {
	const old = process.cwd();
	process.chdir(tempDir("strictcli-update-"));
	try {
		return schemaJson(app.dumpSchemaDict());
	} finally {
		process.chdir(old);
	}
}

test("the dump publishes the update pair and nullable", () => {
	const app = createApp({
		name: "dnsapp",
		version: "1.0.0",
		help: "manage DNS",
	});
	app.command(
		defineMutatingCommand("update-record", {
			help: "change one DNS record in place",
			updateOf: {
				resource: "dns-record",
				writeMode: "sparse",
				identity: ["zone", "record-id"],
				properties: ["content", "ttl"],
			},
			flags: {
				zone: flag("zone", t.str, {
					help: "zone the record belongs to",
					presence: "required",
				}),
				record_id: flag("record-id", t.str, {
					help: "identifier of the record",
					presence: "required",
				}),
				content: flag("content", t.str, {
					help: "record content",
					presence: "optional",
				}),
				ttl: flag("ttl", t.int, {
					help: "time to live",
					presence: "optional",
					nullable: true,
				}),
			},
			handler: ok,
		}),
	);
	const text = dumpText(app);
	// The pair sits immediately after the dry-run pair's position and ahead of
	// the payload keys (§25.9), and the names are the DECLARED spelling.
	assert.ok(
		text.includes(`      "effect": "mutating",
      "update_of": {
        "resource": "dns-record",
        "identity": [
          "zone",
          "record-id"
        ],
        "properties": [
          "content",
          "ttl"
        ]
      },
      "write_mode": "sparse",`),
		text,
	);
	assert.ok(
		text.includes(`          "presence": "optional",
          "nullable": true`),
		text,
	);
	// No second entry for the minted spelling: the dump publishes declarations.
	assert.ok(!text.includes("unset-ttl"), text);
});

test("a command with no update omits the pair", () => {
	const app = tiny();
	app.command(defineReadOnlyCommand("r", { help: "read", handler: ok }));
	const text = dumpText(app);
	// Read past the `defaults` block, which carries both keys' baselines.
	const entries = text.slice(text.indexOf('\n  "commands"'));
	assert.ok(!entries.includes("update_of"), entries);
	assert.ok(!entries.includes("write_mode"), entries);
});

test("an update with no identity publishes an empty array", () => {
	const app = tiny();
	app.command(
		defineMutatingCommand("u", {
			help: "update",
			updateOf: {
				resource: "thing",
				writeMode: "full_replace",
				properties: ["content"],
			},
			flags: {
				content: flag("content", t.str, {
					help: "content",
					presence: "optional",
				}),
			},
			handler: ok,
		}),
	);
	assert.ok(
		dumpText(app).includes(`      "update_of": {
        "resource": "thing",
        "identity": [],
        "properties": [
          "content"
        ]
      },
      "write_mode": "full_replace",`),
		dumpText(app),
	);
});

// --- §27.10: the MCP projection ---

test("the at-least-one-property rule projects as a bare anyOf", () => {
	const app = updateFixture();
	const schema = app.jsonSchema("update-record");
	const branches = schema.anyOf as Record<string, string[]>[];
	assert.equal(branches.length, 3);
	assert.deepEqual(
		branches.map((b) => b.required),
		[["content"], ["ttl"], ["proxied"]],
	);
	// A property is never in `required`: its requiredness IS the rule.
	assert.deepEqual(schema.required, ["zone", "record_id"]);
});

test("the update's branch comes first inside the allOf", () => {
	const app = updateFixture(ok, {
		constraints: [
			atLeastOne({
				name: "addressing",
				members: [{ name: "content" }, { name: "ttl" }],
			}),
		],
	});
	const schema = app.jsonSchema("update-record");
	assert.equal(schema.anyOf, undefined);
	const allOf = schema.allOf as { anyOf: unknown[] }[];
	assert.equal(allOf.length, 2);
	assert.equal(allOf[0]?.anyOf.length, 3);
});

test("a nullable property publishes a type list", () => {
	const app = updateFixture();
	const schema = app.jsonSchema("update-record");
	const props = schema.properties as Record<string, Record<string, unknown>>;
	assert.deepEqual(props.ttl?.type, ["integer", "null"]);
	assert.equal(props.content?.type, "string");
});

test("the update description block", () => {
	const tool = updateFixture()
		.asTools()
		.find((x) => x.name === "update-record");
	assert.equal(
		tool?.description,
		"change one DNS record in place\n\n" +
			'Update of "dns-record" (write mode: sparse):\n' +
			"  identifies: zone, record_id\n" +
			"  writes: content, ttl, proxied -- at least one is required\n" +
			"  a property that is not supplied is left unchanged; null clears ttl",
	);
});

test("the block omits identifies and the null clause when neither applies", () => {
	const app = tiny();
	app.command(
		defineMutatingCommand("u", {
			help: "update it",
			updateOf: {
				resource: "thing",
				writeMode: "full_replace",
				properties: ["content"],
			},
			flags: {
				content: flag("content", t.str, {
					help: "content",
					presence: "optional",
				}),
			},
			handler: ok,
		}),
	);
	const tool = app.asTools().find((x) => x.name === "u");
	assert.equal(
		tool?.description,
		"update it\n\n" +
			'Update of "thing" (write mode: full_replace):\n' +
			"  writes: content -- at least one is required\n" +
			"  a property that is not supplied is re-sent as read",
	);
});

// --- §24.11's carve-out: the machine doors ---

test("null on a nullable property's key is the clear", async () => {
	let present = false;
	let value: unknown = "unset";
	let provided = false;
	let unset = false;
	const app = updateFixture(((
		args: Record<string, unknown>,
		ctx: { provided: (n: string) => boolean; unset: (n: string) => boolean },
	) => {
		present = Object.hasOwn(args, "ttl");
		value = args.ttl;
		provided = ctx.provided("ttl");
		unset = ctx.unset("ttl");
		return 0;
	}) as never);
	await app.call("update-record", { zone: "z1", record_id: "r7", ttl: null });
	assert.equal(present, true);
	assert.equal(value, undefined);
	assert.equal(provided, true);
	assert.equal(unset, true);
});

test("the flat door spells the clear as null on the property's own key", async () => {
	// One key per property at every door, no minted parameter name to collide
	// with a declared flag (§27.6). The tool descriptor's execute is the flat
	// door MCP calls through.
	let unset = false;
	let value: unknown = "set";
	const app = updateFixture(((
		args: Record<string, unknown>,
		ctx: { unset: (n: string) => boolean },
	) => {
		value = args.ttl;
		unset = ctx.unset("ttl");
		return 0;
	}) as never);
	const tool = app.asTools().find((x) => x.name === "update-record");
	await tool?.execute({ zone: "z1", record_id: "r7", ttl: null });
	assert.equal(value, undefined);
	assert.equal(unset, true);
});

test("null on a non-nullable property is still refused", async () => {
	await assert.rejects(
		() =>
			updateFixture().call("update-record", {
				zone: "z1",
				record_id: "r7",
				content: null,
			}),
		/content/,
	);
});

test("the at-least-one rule is enforced at the machine door", async () => {
	await assert.rejects(
		() =>
			updateFixture().call("update-record", { zone: "z1", record_id: "r7" }),
		(e: Error) => {
			assert.equal(
				e.message,
				'update "dns-record": at least one property is required: --content, --ttl, --proxied',
			);
			return true;
		},
	);
});

// --- §27.12: composition ---

test("dryRunSupported: false composes with an update", async () => {
	// The human write-set line then never renders, there being no dry run to
	// render it in; the envelope's member still does, in a live run.
	const app = updateFixture(ok, {
		dryRunSupported: false,
		dryRunUnsupportedReason: "the API has no preview endpoint",
	});
	const r = await app.test([
		"--dry-run",
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
		"--content",
		"x",
	]);
	assert.equal(r.exitCode, 1);
	assert.ok(r.stderr.includes("the API has no preview endpoint"), r.stderr);
	const live = await updateFixture(ok, {
		dryRunSupported: false,
		dryRunUnsupportedReason: "the API has no preview endpoint",
	}).test([
		"--json",
		"update-record",
		"--zone",
		"z1",
		"--record-id",
		"r7",
		"--content",
		"x",
	]);
	assert.ok(live.stdout.includes('"written":["content"]'), live.stdout);
});

test("an update command declares constraints like any command", async () => {
	// Alternative addressing over two optional identity members IS an
	// at-least-one, which is the intended composition (§27.12).
	const app = tiny();
	app.command(
		defineMutatingCommand("u", {
			help: "update",
			updateOf: {
				resource: "thing",
				writeMode: "sparse",
				identity: ["id", "name"],
				properties: ["content"],
			},
			flags: {
				id: flag("id", t.str, { help: "by id", presence: "optional" }),
				name: flag("name", t.str, { help: "by name", presence: "optional" }),
				content: flag("content", t.str, {
					help: "content",
					presence: "optional",
				}),
			},
			constraints: [
				atLeastOne({
					name: "addressing",
					members: [{ name: "id" }, { name: "name" }],
				}),
			],
			handler: ok,
		}),
	);
	const r = await app.test(["u", "--content", "x"]);
	assert.equal(r.exitCode, 1);
	assert.ok(r.stderr.includes('constraint "addressing"'), r.stderr);
});

test("an implied property is a provision", async () => {
	// A value injected by an `implies` exists only because the invocation
	// contained the trigger, so it is provided (§23.6) and joins the write set
	// -- the write set has no source filter (§27.4).
	const app = tiny();
	app.command(
		defineMutatingCommand("u", {
			help: "update",
			updateOf: {
				resource: "thing",
				writeMode: "sparse",
				properties: ["proxied"],
			},
			flags: {
				secure: flag("secure", t.bool, {
					help: "turn on the secure mode",
					presence: "optional",
				}),
				proxied: flag("proxied", t.bool, {
					help: "whether the record is proxied",
					presence: "optional",
				}),
			},
			constraints: [
				implies({
					name: "secure-proxies",
					flag: "secure",
					implies: "proxied",
					value: true,
				}),
			],
			handler: ok,
		}),
	);
	const r = await app.test(["--json", "u", "--secure"]);
	assert.ok(r.stdout.includes('"written":["proxied"]'), r.stdout);
});

test("clearing a bool property", async () => {
	// Every type may be nullable, the four scalars and the compounds alike:
	// clearing is a fact about the resource's field, not about the value's
	// shape (§27.6).
	let value: unknown = "set";
	let unset = false;
	const app = tiny();
	app.command(
		defineMutatingCommand("u", {
			help: "update",
			updateOf: {
				resource: "thing",
				writeMode: "sparse",
				properties: ["proxied"],
			},
			flags: {
				proxied: flag("proxied", t.bool, {
					help: "whether the record is proxied",
					presence: "optional",
					nullable: true,
				}),
			},
			handler: ((
				args: { proxied: unknown },
				ctx: { unset: (n: string) => boolean },
			) => {
				value = args.proxied;
				unset = ctx.unset("proxied");
				return 0;
			}) as never,
		}),
	);
	const r = await app.test(["u", "--unset-proxied"]);
	assert.equal(r.exitCode, 0, r.stderr);
	assert.equal(value, undefined);
	assert.equal(unset, true);
});

test("a passthrough carries its update declaration", () => {
	// The passthrough early-return sits ahead of §27.11's steps in all three
	// implementations, so a passthrough that declares an update keeps the
	// declaration and publishes it: it can declare no flags, so it can name no
	// property, and §27 authors no guard for a state its own refusal already
	// makes unusable.
	const app = tiny();
	app.command(
		mutatingPassthrough("exec", {
			help: "forward everything",
			updateOf: {
				resource: "thing",
				writeMode: "sparse",
				properties: ["content"],
			},
			handler: () => 0,
		}),
	);
	assert.ok(dumpText(app).includes('"resource": "thing"'));
});
