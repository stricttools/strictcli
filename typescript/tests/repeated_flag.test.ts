/**
 * A flag that is not repeatable takes one value. Giving it more than once on
 * the command line is refused, naming the flag and its first two occurrences
 * as typed, rather than silently keeping the last one. Environment variables
 * and config files are not occurrences: their precedence is unchanged.
 */

import { strict as assert } from "node:assert";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import type { App, AppSpec } from "../src/app.js";
import {
	choice,
	choiceFlag,
	createApp,
	defineMutatingCommand,
	defineReadOnlyCommand,
	flag,
	memberChoiceFlag,
	t,
} from "../src/index.js";
import { tempDir } from "./helpers.js";

function repeatedApp(spec: Partial<AppSpec> = {}): App {
	const app = createApp({
		name: "myapp",
		version: "1.0.0",
		help: "test app",
		flags: {
			region: flag("region", t.str, {
				help: "the region",
				presence: "default",
				default: "eu",
				short: "r",
			}),
			zone: flag("zone", t.list(t.str), {
				help: "zones to use",
				unique: false,
				presence: "default",
				default: [],
			}),
		},
		...spec,
	});
	app.command(
		defineReadOnlyCommand("run", {
			help: "run it",
			flags: {
				commits: flag("commits", t.str, {
					help: "the commit range",
					presence: "optional",
					short: "c",
					env: "MYAPP_COMMITS",
				}),
				cache: flag("cache", t.bool, {
					help: "use the cache",
					presence: "default",
					default: true,
					short: "k",
				}),
				tag: flag("tag", t.list(t.str), {
					help: "tags to apply",
					unique: false,
					presence: "default",
					default: [],
				}),
				labels: flag("labels", t.dict(t.str), {
					help: "labels",
					presence: "default",
					default: new Map(),
				}),
			},
			handler: (a, ctx) => {
				const g = a as unknown as { region: string; zone: string[] };
				ctx.info(
					`commits=${a.commits} cache=${a.cache} tag=${a.tag.join(",")} ` +
						`labels=${[...a.labels].map(([k, v]) => `${k}:${v}`).join(",")} ` +
						`region=${g.region} zone=${g.zone.join(",")}`,
				);
				return 0;
			},
		}),
	);
	return app;
}

function msg(name: string, first: string, second: string): string {
	return `--${name}: given more than once, as '${first}' and '${second}'; it takes one value`;
}

async function refused(app: App, argv: string[], message: string) {
	const r = await app.test(argv);
	assert.equal(r.exitCode, 1, `${argv.join(" ")}: ${r.stderr}`);
	assert.ok(
		r.stderr.includes(`error: ${message}\n`),
		`${argv.join(" ")}: stderr=${JSON.stringify(r.stderr)}`,
	);
}

async function runs(app: App, argv: string[], fragment: string) {
	const r = await app.test(argv);
	assert.equal(r.exitCode, 0, `${argv.join(" ")}: ${r.stderr}`);
	assert.ok(
		r.stdout.includes(fragment),
		`${argv.join(" ")}: stdout=${JSON.stringify(r.stdout)}`,
	);
}

test("repeated flag: space form", async () => {
	await refused(
		repeatedApp(),
		["run", "--commits", "a", "--commits", "b"],
		msg("commits", "--commits a", "--commits b"),
	);
});

test("repeated flag: equals form", async () => {
	await refused(
		repeatedApp(),
		["run", "--commits=a", "--commits=b"],
		msg("commits", "--commits=a", "--commits=b"),
	);
});

test("repeated flag: a short alias mixed with the long form", async () => {
	await refused(
		repeatedApp(),
		["run", "-c", "a", "--commits", "b"],
		msg("commits", "-c a", "--commits b"),
	);
});

test("repeated flag: the same value twice is still refused", async () => {
	await refused(
		repeatedApp(),
		["run", "--commits", "a", "--commits", "a"],
		msg("commits", "--commits a", "--commits a"),
	);
});

test("repeated flag: names the first two of three", async () => {
	await refused(
		repeatedApp(),
		["run", "--commits", "a", "-c", "b", "--commits=c"],
		msg("commits", "--commits a", "-c b"),
	);
});

test("repeated flag: a bool given twice, and with its negation", async () => {
	await refused(
		repeatedApp(),
		["run", "--cache", "-k"],
		msg("cache", "--cache", "-k"),
	);
	await refused(
		repeatedApp(),
		["run", "--cache", "--no-cache"],
		msg("cache", "--cache", "--no-cache"),
	);
	await refused(
		repeatedApp(),
		["run", "--no-cache", "--no-cache"],
		msg("cache", "--no-cache", "--no-cache"),
	);
});

test("repeated flag: the refusal outranks a value that would not parse", async () => {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run it",
			flags: {
				count: flag("count", t.int, { help: "a count", presence: "optional" }),
			},
			handler: () => 0,
		}),
	);
	await refused(
		app,
		["run", "--count", "x", "--count", "5"],
		msg("count", "--count x", "--count 5"),
	);
});

test("repeated flag: passing it once runs", async () => {
	await runs(repeatedApp(), ["run", "--commits", "b"], "commits=b");
	await runs(repeatedApp(), ["run", "--no-cache"], "cache=false");
});

test("repeated flag: list and dict flags still collect", async () => {
	await runs(
		repeatedApp(),
		["run", "--tag", "a", "--tag", "b", "--labels", "x=1", "--labels", "y=2"],
		"tag=a,b labels=x:1,y:2",
	);
});

test("repeated flag: env is not repetition", async () => {
	const saved = process.env.MYAPP_COMMITS;
	process.env.MYAPP_COMMITS = "from-env";
	try {
		await runs(repeatedApp(), ["run", "--commits", "cli"], "commits=cli");
	} finally {
		if (saved === undefined) {
			delete process.env.MYAPP_COMMITS;
		} else {
			process.env.MYAPP_COMMITS = saved;
		}
	}
});

test("repeated flag: config is not repetition", async () => {
	const path = join(tempDir("repeated-flag-"), "config.json");
	writeFileSync(path, '{"commits": "from-config"}');
	await runs(
		repeatedApp({ config: true, configPath: path }),
		["run", "--commits", "cli"],
		"commits=cli",
	);
});

test("repeated flag: a global before, after, and on both sides of the command", async () => {
	await refused(
		repeatedApp(),
		["--region", "us", "-r", "ap", "run"],
		msg("region", "--region us", "-r ap"),
	);
	await refused(
		repeatedApp(),
		["run", "--region=us", "--region=ap"],
		msg("region", "--region=us", "--region=ap"),
	);
	await refused(
		repeatedApp(),
		["--region", "us", "run", "-r", "ap"],
		msg("region", "--region us", "-r ap"),
	);
	await runs(repeatedApp(), ["--zone", "a", "--zone", "b", "run"], "zone=a,b");
});

test("repeated flag: reports the first second occurrence", async () => {
	await refused(
		repeatedApp(),
		["run", "--cache", "--commits", "a", "--commits", "b", "--no-cache"],
		msg("commits", "--commits a", "--commits b"),
	);
});

function scopedApp(): App {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("send", {
			help: "send it",
			flags: {
				via: choiceFlag(
					"via",
					{
						email: choice({
							help: "an email message",
							flags: {
								subject: flag("subject", t.str, {
									help: "the subject",
									presence: "required",
									short: "s",
								}),
								urgent: flag("urgent", t.bool, {
									help: "mark urgent",
									presence: "default",
									default: false,
								}),
							},
						}),
						sms: choice({
							help: "a text message",
							flags: {
								phone: flag("phone", t.str, {
									help: "destination",
									presence: "required",
								}),
							},
						}),
					},
					{ help: "delivery channel", presence: "required" },
				),
				mode: memberChoiceFlag(
					"mode",
					{
						profile: choice({
							help: "one named profile",
							value: { carrier: t.str, help: "a profile" },
						}),
						"all-profiles": choice({ help: "every profile" }),
					},
					{
						help: "which profiles",
						presence: "default",
						default: "all-profiles",
					},
				),
			},
			handler: () => 0,
		}),
	);
	return app;
}

test("repeated flag: scoped flags", async () => {
	await refused(
		scopedApp(),
		["send", "--via", "email", "-s", "hi", "--subject", "yo"],
		msg("subject", "-s hi", "--subject yo"),
	);
	await refused(
		scopedApp(),
		["send", "--via", "email", "-s", "hi", "--urgent", "--no-urgent"],
		msg("urgent", "--urgent", "--no-urgent"),
	);
});

test("repeated flag: member flags", async () => {
	await refused(
		scopedApp(),
		[
			"send",
			"--via",
			"sms",
			"--phone",
			"1",
			"--profile",
			"a",
			"--profile",
			"b",
		],
		msg("profile", "--profile a", "--profile b"),
	);
	await refused(
		scopedApp(),
		[
			"send",
			"--via",
			"sms",
			"--phone",
			"1",
			"--all-profiles",
			"--no-all-profiles",
		],
		msg("all-profiles", "--all-profiles", "--no-all-profiles"),
	);
});

test("repeated flag: a selector elected twice keeps its own sentence", async () => {
	await refused(
		scopedApp(),
		["send", "--via", "email", "--via", "sms"],
		"--via: elected more than once, as 'email' and 'sms'",
	);
});

test("repeated flag: precedence against scope and presence", async () => {
	await refused(
		scopedApp(),
		["send", "--via", "sms", "--subject", "a", "--subject", "b"],
		"flag '--subject' is only valid under '--via email', but '--via sms' was elected",
	);
	await refused(
		scopedApp(),
		[
			"send",
			"--via",
			"email",
			"--subject",
			"a",
			"--subject",
			"b",
			"--all-profiles",
		],
		msg("subject", "--subject a", "--subject b"),
	);
});

function updateApp(): App {
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
				identity: ["zone"],
				properties: ["ttl", "proxied"],
			},
			flags: {
				zone: flag("zone", t.str, { help: "the zone", presence: "required" }),
				ttl: flag("ttl", t.int, {
					help: "time to live",
					presence: "optional",
					nullable: true,
				}),
				proxied: flag("proxied", t.bool, {
					help: "proxied",
					presence: "optional",
				}),
			},
			handler: () => 0,
		}),
	);
	return app;
}

test("repeated flag: update commands", async () => {
	await refused(
		updateApp(),
		["update-record", "--zone", "z", "--unset-ttl", "--unset-ttl"],
		msg("unset-ttl", "--unset-ttl", "--unset-ttl"),
	);
	await refused(
		updateApp(),
		["update-record", "--zone", "z", "--ttl", "1", "--ttl", "2", "--unset-ttl"],
		"--ttl and --unset-ttl are mutually exclusive: a property is either written or cleared",
	);
	await refused(
		updateApp(),
		["update-record", "--zone", "z", "--proxied", "--no-proxied"],
		msg("proxied", "--proxied", "--no-proxied"),
	);
});

test("repeated flag: the reserved --config takes one path", async () => {
	const path = join(tempDir("repeated-flag-"), "config.json");
	writeFileSync(path, "{}");
	const app = repeatedApp({ config: true });
	await refused(
		app,
		["--config", path, `--config=${path}`, "run"],
		msg("config", `--config ${path}`, `--config=${path}`),
	);
	await runs(app, ["--config", path, "run"], "commits=undefined");
});
