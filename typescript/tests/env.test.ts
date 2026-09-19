import { strict as assert } from "node:assert";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { newStdinTracker } from "../src/atprefix.js";
import { resolveEnvValue } from "../src/env.js";
import { flag, t } from "../src/index.js";
import { tempDir } from "./helpers.js";

const dir = tempDir("strictcli-env-");

function parseError(message: string): { name: string; message: string } {
	return { name: "ParseError", message };
}

const tracker = newStdinTracker;

// --- Scalars ---

test("str env values pass through verbatim (conformance boundary cases)", () => {
	const f = flag("name", t.str, {
		help: "h",
		env: "MYAPP_NAME",
		presence: "required",
	});
	assert.equal(resolveEnvValue(f, "MYAPP_NAME", "", tracker()), "");
	assert.equal(
		resolveEnvValue(f, "MYAPP_NAME", "  hello  ", tracker()),
		"  hello  ",
	);
	assert.equal(resolveEnvValue(f, "MYAPP_NAME", "--foo", tracker()), "--foo");
});

test("str env values resolve the @-prefix", () => {
	const f = flag("msg", t.str, {
		help: "h",
		env: "MYAPP_MSG",
		presence: "required",
	});
	assert.equal(resolveEnvValue(f, "MYAPP_MSG", "@@lit", tracker()), "@lit");
	const p = join(dir, "content.txt");
	writeFileSync(p, "from file\n");
	assert.equal(
		resolveEnvValue(f, "MYAPP_MSG", `@${p}`, tracker()),
		"from file",
	);
	// @-prefix errors carry no env suffix (sibling parity).
	assert.throws(
		() => resolveEnvValue(f, "MYAPP_MSG", "@missing.txt", tracker()),
		parseError("--msg: file not found: missing.txt"),
	);
});

test("bool env strings follow the strict six-string rule", () => {
	const f = flag("chatter", t.bool, {
		help: "h",
		env: "MYAPP_VERBOSE",
		presence: "default",
		default: false,
	});
	for (const [s, want] of [
		["TRUE", true],
		["True", true],
		["1", true],
		["YES", true],
		["no", false],
		["FALSE", false],
		["False", false],
		["0", false],
	] as const) {
		assert.equal(resolveEnvValue(f, "MYAPP_VERBOSE", s, tracker()), want, s);
	}
	// conformance boundary.json: invalid, empty, and whitespace-only values
	for (const s of ["maybe", "", "   "]) {
		assert.throws(
			() => resolveEnvValue(f, "MYAPP_VERBOSE", s, tracker()),
			parseError(
				`invalid boolean value '${s}' for env var 'MYAPP_VERBOSE' (flag '--chatter')`,
			),
		);
	}
});

test("int env values parse strictly with the env-suffixed message", () => {
	const f = flag("port", t.int, {
		help: "h",
		env: "MYAPP_PORT",
		presence: "required",
	});
	assert.equal(resolveEnvValue(f, "MYAPP_PORT", "8080", tracker()), 8080n);
	// conformance int_type.json / boundary.json
	assert.throws(
		() => resolveEnvValue(f, "MYAPP_PORT", "abc", tracker()),
		parseError(
			"--port: expected integer, got 'abc' (from env var 'MYAPP_PORT')",
		),
	);
	assert.throws(
		() => resolveEnvValue(f, "MYAPP_PORT", " 42 ", tracker()),
		parseError(
			"--port: expected integer, got ' 42 ' (from env var 'MYAPP_PORT')",
		),
	);
});

test("float env values parse strictly with the env-suffixed message", () => {
	const f = flag("rate", t.float, {
		help: "h",
		env: "MYAPP_RATE",
		presence: "required",
	});
	assert.equal(resolveEnvValue(f, "MYAPP_RATE", "2.5", tracker()), 2.5);
	// conformance float_type.json: NaN from env var
	assert.throws(
		() => resolveEnvValue(f, "MYAPP_RATE", "nan", tracker()),
		parseError("--rate: NaN is not allowed (from env var 'MYAPP_RATE')"),
	);
	assert.throws(
		() => resolveEnvValue(f, "MYAPP_RATE", "abc", tracker()),
		parseError("--rate: expected float, got 'abc' (from env var 'MYAPP_RATE')"),
	);
});

// --- Lists (env_separator) ---

test("list env values split on the separator with escapes", () => {
	const f = flag("tag", t.list(t.str), {
		help: "h",
		env: "TAGS",
		envSeparator: ",",
		presence: "default",
		default: [],
	});
	assert.deepEqual(resolveEnvValue(f, "TAGS", "a,b,c", tracker()), [
		"a",
		"b",
		"c",
	]);
	// conformance env_separator.json: escaped separator, single value
	assert.deepEqual(resolveEnvValue(f, "TAGS", "a\\,b,c", tracker()), [
		"a,b",
		"c",
	]);
	assert.deepEqual(resolveEnvValue(f, "TAGS", "solo", tracker()), ["solo"]);

	const colon = flag("path", t.list(t.str), {
		help: "h",
		env: "PATHS",
		envSeparator: ":",
		presence: "default",
		default: [],
	});
	assert.deepEqual(
		resolveEnvValue(colon, "PATHS", "/usr/bin:/usr/local/bin", tracker()),
		["/usr/bin", "/usr/local/bin"],
	);
});

test("list element coercion errors carry the env suffix", () => {
	const counts = flag("count", t.list(t.int), {
		help: "h",
		env: "COUNTS",
		envSeparator: ",",
		presence: "default",
		default: [],
	});
	assert.deepEqual(resolveEnvValue(counts, "COUNTS", "1,2,3", tracker()), [
		1n,
		2n,
		3n,
	]);
	// conformance env_separator.json: int coercion error per element
	assert.throws(
		() => resolveEnvValue(counts, "COUNTS", "1,abc,3", tracker()),
		parseError("--count: expected integer, got 'abc' (from env var 'COUNTS')"),
	);

	const rates = flag("rate", t.list(t.float), {
		help: "h",
		env: "RATES",
		envSeparator: ",",
		presence: "default",
		default: [],
	});
	assert.deepEqual(
		resolveEnvValue(rates, "RATES", "1.5,2.5", tracker()),
		[1.5, 2.5],
	);
	// conformance env_separator.json: float NaN error per element
	assert.throws(
		() => resolveEnvValue(rates, "RATES", "1.5,nan", tracker()),
		parseError("--rate: NaN is not allowed (from env var 'RATES')"),
	);
});

test("list unique enforcement from env", () => {
	const f = flag("tag", t.list(t.str), {
		help: "h",
		env: "TAGS",
		envSeparator: ",",
		unique: true,
		presence: "default",
		default: [],
	});
	assert.deepEqual(resolveEnvValue(f, "TAGS", "a,b", tracker()), ["a", "b"]);
	// conformance env_separator.json: unique enforcement from env
	assert.throws(
		() => resolveEnvValue(f, "TAGS", "a,b,a", tracker()),
		parseError("--tag: duplicate value 'a' (from env var 'TAGS')"),
	);
});

test("list str elements resolve the @-prefix", () => {
	const f = flag("msg", t.list(t.str), {
		help: "h",
		env: "MSGS",
		envSeparator: ",",
		presence: "default",
		default: [],
	});
	assert.deepEqual(resolveEnvValue(f, "MSGS", "@@a,plain", tracker()), [
		"@a",
		"plain",
	]);
});

// --- Dicts (env vars are JSON) ---

test("dict env values parse as a whole JSON object", () => {
	const f = flag("meta", t.dict(t.int), {
		help: "h",
		env: "MYAPP_META",
		presence: "default",
		default: new Map(),
	});
	assert.deepEqual(
		resolveEnvValue(f, "MYAPP_META", '{"a": 7, "b": -2}', tracker()),
		new Map([
			["a", 7n],
			["b", -2n],
		]),
	);
	assert.throws(() => resolveEnvValue(f, "MYAPP_META", "{bad", tracker()), {
		name: "ParseError",
		message: /^--meta: invalid JSON in env var 'MYAPP_META': /,
	});
	assert.throws(
		() => resolveEnvValue(f, "MYAPP_META", "[1,2]", tracker()),
		parseError("--meta: env var 'MYAPP_META' must be a JSON object, got list"),
	);
	assert.throws(
		() => resolveEnvValue(f, "MYAPP_META", '{"a": 1.5}', tracker()),
		parseError("--meta: JSON value for key 'a' must be an integer, got float"),
	);
});
