import { strict as assert } from "node:assert";
import { test } from "node:test";
import { newStdinTracker } from "../src/atprefix.js";
import {
	appendListValue,
	coerceArgValue,
	coerceToScalar,
	findDuplicate,
	formatDictForDisplay,
	formatValueForError,
	parseBoolStrict,
	parseDictEnvValue,
	parseDictValue,
	parseFloatStrictFlag,
	parseFloatStrictValue,
	parseIntStrict,
	splitEscaped,
	storeDictEntries,
	validateChoices,
} from "../src/values.js";

function parseError(message: string): { name: string; message: string } {
	return { name: "ParseError", message };
}

// --- parseBoolStrict ---

test("parseBoolStrict accepts the six sibling strings case-insensitively", () => {
	for (const s of ["1", "true", "yes", "TRUE", "Yes", "YES", "True"]) {
		assert.equal(parseBoolStrict(s), true, s);
	}
	for (const s of ["0", "false", "no", "FALSE", "No", "NO", "False"]) {
		assert.equal(parseBoolStrict(s), false, s);
	}
});

test("parseBoolStrict rejects everything else with the sibling message", () => {
	for (const s of ["maybe", "", "   ", " true", "yess", "2"]) {
		assert.throws(
			() => parseBoolStrict(s),
			parseError(`expected boolean, got '${s}'`),
		);
	}
});

// --- parseIntStrict ---

test("parseIntStrict accepts Go strconv.Atoi forms", () => {
	assert.equal(parseIntStrict("42"), 42n);
	assert.equal(parseIntStrict("-7"), -7n);
	assert.equal(parseIntStrict("007"), 7n); // conformance: boundary leading zeros
	assert.equal(parseIntStrict("+5"), 5n); // conformance: boundary plus sign
	assert.equal(parseIntStrict("-0"), 0n);
	assert.equal(parseIntStrict("0"), 0n);
	// Signed-64-bit bounds are inclusive.
	assert.equal(parseIntStrict("9223372036854775807"), 2n ** 63n - 1n);
	assert.equal(parseIntStrict("-9223372036854775808"), -(2n ** 63n));
});

test("parseIntStrict rejections carry the sibling message", () => {
	const bad = [
		" 42 ", // surrounding whitespace (conformance: env var with whitespace)
		"12abc", // conformance: partially numeric
		"abc",
		"",
		"+",
		"-",
		"1_000", // Python-only underscore form; TS follows the stricter Go side
		"1e5",
		"0x10",
		"1.0",
		"99999999999999999999", // conformance: int overflow
		"9223372036854775808", // int64 max + 1
		"-9223372036854775809", // int64 min - 1
	];
	for (const s of bad) {
		assert.throws(
			() => parseIntStrict(s),
			parseError(`expected integer, got '${s}'`),
		);
	}
});

// --- parseFloatStrictValue / parseFloatStrictFlag ---

test("parseFloatStrictValue accepts the sibling-intersection decimal grammar", () => {
	assert.equal(parseFloatStrictValue("3.14"), 3.14);
	assert.equal(parseFloatStrictValue(".5"), 0.5);
	assert.equal(parseFloatStrictValue("5."), 5);
	assert.equal(parseFloatStrictValue("+.5"), 0.5);
	assert.equal(parseFloatStrictValue("-2.5"), -2.5);
	assert.equal(parseFloatStrictValue("1e5"), 100000);
	assert.equal(parseFloatStrictValue("1E5"), 100000);
	assert.equal(parseFloatStrictValue("5.e3"), 5000);
	assert.equal(parseFloatStrictValue("1_000.5"), 1000.5); // both siblings accept digit underscores
	assert.equal(parseFloatStrictValue("1e-400"), 0); // underflow to zero, like both siblings
	assert.ok(Object.is(parseFloatStrictValue("-0.0"), -0));
});

test("parseFloatStrictValue rejects NaN and Inf by name with exact messages", () => {
	for (const s of ["nan", "NaN", "NAN"]) {
		assert.throws(
			() => parseFloatStrictValue(s),
			parseError("NaN is not allowed"),
		);
	}
	for (const s of [
		"inf",
		"Inf",
		"-inf",
		"+inf",
		"infinity",
		"-Infinity",
		"+INFINITY",
	]) {
		assert.throws(
			() => parseFloatStrictValue(s),
			parseError("Inf is not allowed"),
		);
	}
});

test("parseFloatStrictValue rejects malformed and overflowing input", () => {
	const bad = [
		" 1.5", // leading whitespace
		"1.5 ", // trailing whitespace
		"abc",
		"",
		"+nan", // signed nan is not a special name in either sibling
		"0x10", // Python rejects hex; Go-only hex floats are not ported
		"0x10p2",
		"1_.5", // underscore not between digits
		"_1",
		"1__0",
		"1e400", // overflows to Inf: Go rejects (value out of range)
		"-1e400",
	];
	for (const s of bad) {
		assert.throws(
			() => parseFloatStrictValue(s),
			parseError(`expected float, got '${s}'`),
		);
	}
});

test("parseFloatStrictFlag contextualizes messages with the flag name", () => {
	assert.equal(parseFloatStrictFlag("rate", "2.5"), 2.5);
	assert.throws(
		() => parseFloatStrictFlag("rate", "abc"),
		parseError("--rate: expected float, got 'abc'"),
	);
	assert.throws(
		() => parseFloatStrictFlag("rate", "nan"),
		parseError("--rate: NaN is not allowed"),
	);
	assert.throws(
		() => parseFloatStrictFlag("rate", "inf"),
		parseError("--rate: Inf is not allowed"),
	);
});

// --- coerceToScalar / coerceArgValue ---

test("coerceToScalar adds --flag context and resolves @-prefix for str", () => {
	assert.equal(coerceToScalar("port", "8080", "int"), 8080n);
	assert.throws(
		() => coerceToScalar("port", "abc", "int"),
		parseError("--port: expected integer, got 'abc'"),
	);
	// conformance at_prefix.json: non-string flag ignores @ prefix
	assert.throws(
		() => coerceToScalar("count", "@5", "int"),
		parseError("--count: expected integer, got '@5'"),
	);
	assert.equal(coerceToScalar("rate", "1.5", "float"), 1.5);
	// str with a tracker resolves @@; without a tracker it passes through.
	assert.equal(
		coerceToScalar("msg", "@@lit", "str", newStdinTracker()),
		"@lit",
	);
	assert.equal(coerceToScalar("msg", "@@lit", "str"), "@@lit");
	assert.equal(
		coerceToScalar("msg", "plain", "str", newStdinTracker()),
		"plain",
	);
});

test("coerceArgValue uses the argument error prefix", () => {
	assert.equal(coerceArgValue("count", "5", "int"), 5n);
	assert.equal(coerceArgValue("name", "x", "str"), "x");
	assert.equal(coerceArgValue("flagish", "true", "bool"), true);
	assert.equal(coerceArgValue("rate", "0.5", "float"), 0.5);
	assert.throws(
		() => coerceArgValue("count", "abc", "int"),
		parseError("argument 'count': expected integer, got 'abc'"),
	);
	assert.throws(
		() => coerceArgValue("rate", "abc", "float"),
		parseError("argument 'rate': expected float, got 'abc'"),
	);
	assert.throws(
		() => coerceArgValue("rate", "nan", "float"),
		parseError("argument 'rate': NaN is not allowed"),
	);
	assert.throws(
		() => coerceArgValue("ok", "maybe", "bool"),
		parseError("argument 'ok': expected boolean, got 'maybe'"),
	);
});

// --- formatValueForError / formatDictForDisplay ---

test("formatValueForError renders each type canonically", () => {
	assert.equal(formatValueForError(true), "true");
	assert.equal(formatValueForError(false), "false");
	assert.equal(formatValueForError(5n), "5");
	assert.equal(formatValueForError("x"), "x");
	assert.equal(formatValueForError(1e21), "1e+21");
	assert.equal(formatValueForError(1.0), "1.0");
	assert.equal(formatValueForError(-0), "-0.0");
	assert.equal(
		formatValueForError(
			new Map<string, unknown>([
				["b", 2n],
				["a", 1n],
			]),
		),
		"a=1, b=2",
	);
});

test("formatDictForDisplay sorts keys and uses key=value pairs", () => {
	const m = new Map<string, unknown>([
		["zeta", 1.5],
		["alpha", "v"],
		["mid", true],
	]);
	assert.equal(formatDictForDisplay(m), "alpha=v, mid=true, zeta=1.5");
	assert.equal(formatDictForDisplay(new Map()), "");
});

// --- validateChoices ---

test("validateChoices produces sibling-exact flag and arg messages", () => {
	// conformance choices.json: invalid str choice
	assert.throws(
		() =>
			validateChoices(
				"format",
				"xml",
				false,
				["text", "json"],
				undefined,
				false,
			),
		parseError("--format: invalid value 'xml', must be one of: text, json"),
	);
	assert.throws(
		() =>
			validateChoices(
				"format",
				"xml",
				false,
				["text", "json"],
				undefined,
				true,
			),
		parseError(
			"argument 'format': invalid value 'xml', must be one of: text, json",
		),
	);
	// conformance float_format.json: attempted value echoed canonically
	assert.throws(
		() => validateChoices("rate", 1e21, false, [1.5, 2.5], undefined, false),
		parseError("--rate: invalid value '1e+21', must be one of: 1.5, 2.5"),
	);
	assert.throws(
		() => validateChoices("rate", -0, false, [1.5, 2.5], undefined, false),
		parseError("--rate: invalid value '-0.0', must be one of: 1.5, 2.5"),
	);
	// Valid values, missing values, and repeatable element-wise checks pass.
	validateChoices("format", "json", false, ["text", "json"], undefined, false);
	validateChoices(
		"format",
		undefined,
		false,
		["text", "json"],
		undefined,
		false,
	);
	validateChoices("format", null, false, ["text", "json"], undefined, false);
	validateChoices("n", [1n, 2n], true, [1n, 2n, 3n], undefined, false);
	assert.throws(
		() => validateChoices("n", [1n, 4n], true, [1n, 2n, 3n], undefined, false),
		parseError("--n: invalid value '4', must be one of: 1, 2, 3"),
	);
});

// --- findDuplicate / appendListValue ---

test("findDuplicate returns the first duplicate or undefined", () => {
	assert.equal(findDuplicate(["a", "b", "a", "b"]), "a");
	assert.equal(findDuplicate([1n, 2n, 3n]), undefined);
	assert.equal(findDuplicate([1.5, 2.5, 1.5]), 1.5);
	assert.equal(findDuplicate([]), undefined);
});

test("appendListValue enforces unique semantics on the accumulated list", () => {
	const list: unknown[] = [];
	appendListValue(list, "a", true, "tag");
	appendListValue(list, "b", true, "tag");
	assert.deepEqual(list, ["a", "b"]);
	assert.throws(
		() => appendListValue(list, "a", true, "tag"),
		parseError("--tag: duplicate value 'a'"),
	);
	// Without unique, duplicates accumulate freely.
	const plain: unknown[] = [];
	appendListValue(plain, "a", false, "tag");
	appendListValue(plain, "a", false, "tag");
	assert.deepEqual(plain, ["a", "a"]);
});

// --- splitEscaped ---

test("splitEscaped ports the sibling escape semantics exactly", () => {
	assert.deepEqual(splitEscaped("a,b,c", ","), ["a", "b", "c"]);
	assert.deepEqual(splitEscaped("a\\,b,c", ","), ["a,b", "c"]);
	assert.deepEqual(splitEscaped("/usr/bin:/usr/local/bin", ":"), [
		"/usr/bin",
		"/usr/local/bin",
	]);
	assert.deepEqual(splitEscaped("", ","), [""]);
	assert.deepEqual(splitEscaped(",a,", ","), ["", "a", ""]);
	// Escaped backslash and trailing backslash both become double backslashes
	// (sibling-exact, however surprising).
	assert.deepEqual(splitEscaped("a\\\\,b", ","), ["a\\\\", "b"]);
	assert.deepEqual(splitEscaped("a\\", ","), ["a\\\\"]);
	// Backslash before any other char is preserved as-is.
	assert.deepEqual(splitEscaped("a\\xb", ","), ["a\\xb"]);
});

// --- dict parsing (Python ground truth, captured by running the Python impl) ---

test("parseDictValue key=value form coerces by value schema", () => {
	assert.deepEqual(parseDictValue("meta", "a=3", "int"), new Map([["a", 3n]]));
	assert.deepEqual(
		parseDictValue("hdr", "content-type=text/html", "str"),
		new Map([["content-type", "text/html"]]),
	);
	// Split on the FIRST equals; the rest is the value.
	assert.deepEqual(
		parseDictValue("hdr", "k=a=b", "str"),
		new Map([["k", "a=b"]]),
	);
	assert.deepEqual(
		parseDictValue("rate", "k=1.5", "float"),
		new Map([["k", 1.5]]),
	);
});

test("parseDictValue key=value errors match the Python messages", () => {
	assert.throws(
		() => parseDictValue("meta", "noeq", "int"),
		parseError("--meta: expected key=value or JSON, got 'noeq'"),
	);
	assert.throws(
		() => parseDictValue("meta", "=5", "int"),
		parseError("--meta: empty key in '=5'"),
	);
	assert.throws(
		() => parseDictValue("meta", "a=xyz", "int"),
		parseError("--meta: value for key 'a': expected integer, got 'xyz'"),
	);
	// Float value errors carry no key context (Python parity).
	assert.throws(
		() => parseDictValue("rate", "k=nan", "float"),
		parseError("--rate: NaN is not allowed"),
	);
	assert.throws(
		() => parseDictValue("rate", "k=abc", "float"),
		parseError("--rate: expected float, got 'abc'"),
	);
	// Leading whitespace disables JSON detection (Python startswith, no trim).
	assert.throws(
		() => parseDictValue("meta", ' {"a": 1}', "int"),
		parseError(`--meta: expected key=value or JSON, got ' {"a": 1}'`),
	);
});

test("parseDictValue JSON form coerces values and keeps int/float distinct", () => {
	assert.deepEqual(
		parseDictValue("meta", '{"a": 1, "b": 2}', "int"),
		new Map([
			["a", 1n],
			["b", 2n],
		]),
	);
	// JSON ints are unbounded (Python parity; int64 bounds apply to key=value only).
	assert.deepEqual(
		parseDictValue("meta", '{"a": 99999999999999999999999}', "int"),
		new Map([["a", 99999999999999999999999n]]),
	);
	assert.deepEqual(
		parseDictValue("meta", '{"a": -3}', "int"),
		new Map([["a", -3n]]),
	);
	// Float schema accepts both int and float tokens.
	assert.deepEqual(
		parseDictValue("rate", '{"a": 5}', "float"),
		new Map([["a", 5]]),
	);
	assert.deepEqual(
		parseDictValue("rate", '{"a": 2.5}', "float"),
		new Map([["a", 2.5]]),
	);
	// Duplicate keys inside ONE JSON object are last-wins (json.loads parity).
	assert.deepEqual(
		parseDictValue("meta", '{"a": 1, "a": 2}', "int"),
		new Map([["a", 2n]]),
	);
});

test("parseDictValue JSON coercion errors match the Python messages", () => {
	// "3.0" and "3e2" are float tokens even though their JS number is integral.
	for (const raw of ['{"a": 1.5}', '{"a": 3.0}', '{"a": 3e2}']) {
		assert.throws(
			() => parseDictValue("meta", raw, "int"),
			parseError(
				"--meta: JSON value for key 'a' must be an integer, got float",
			),
		);
	}
	assert.throws(
		() => parseDictValue("meta", '{"a": true}', "int"),
		parseError("--meta: JSON value for key 'a' must be an integer, got bool"),
	);
	assert.throws(
		() => parseDictValue("meta", '{"a": null}', "int"),
		parseError("--meta: JSON value for key 'a' must be an integer, got null"),
	);
	assert.throws(
		() => parseDictValue("meta", '{"a": [1]}', "int"),
		parseError("--meta: JSON value for key 'a' must be an integer, got array"),
	);
	assert.throws(
		() => parseDictValue("meta", '{"a": {"b": 1}}', "int"),
		parseError("--meta: JSON value for key 'a' must be an integer, got object"),
	);
	assert.throws(
		() => parseDictValue("hdr", '{"a": 5}', "str"),
		parseError("--hdr: JSON value for key 'a' must be a string, got int"),
	);
	assert.throws(
		() => parseDictValue("rate", '{"a": true}', "float"),
		parseError("--rate: JSON value for key 'a' must be a number, got bool"),
	);
	// The JSON parser's own message is engine-specific; only the prefix is pinned.
	assert.throws(() => parseDictValue("meta", "{bad", "int"), {
		name: "ParseError",
		message: /^--meta: invalid JSON: /,
	});
});

test("storeDictEntries rejects keys already accumulated", () => {
	const target = new Map<string, unknown>();
	storeDictEntries(target, parseDictValue("meta", "a=3", "int"), "meta");
	storeDictEntries(target, parseDictValue("meta", "b=4", "int"), "meta");
	assert.deepEqual(
		target,
		new Map([
			["a", 3n],
			["b", 4n],
		]),
	);
	assert.throws(
		() =>
			storeDictEntries(target, parseDictValue("meta", "a=5", "int"), "meta"),
		parseError("--meta: duplicate key 'a'"),
	);
	assert.throws(
		() =>
			storeDictEntries(
				target,
				parseDictValue("meta", '{"a": 1}', "int"),
				"meta",
			),
		parseError("--meta: duplicate key 'a'"),
	);
});

test("parseDictEnvValue requires a JSON object with Python-native typenames", () => {
	assert.deepEqual(
		parseDictEnvValue("meta", "MYAPP_META", '{"a": 7}', "int"),
		new Map([["a", 7n]]),
	);
	assert.throws(() => parseDictEnvValue("meta", "MYAPP_META", "{bad", "int"), {
		name: "ParseError",
		message: /^--meta: invalid JSON in env var 'MYAPP_META': /,
	});
	assert.throws(
		() => parseDictEnvValue("meta", "MYAPP_META", "[1,2]", "int"),
		parseError("--meta: env var 'MYAPP_META' must be a JSON object, got list"),
	);
	assert.throws(
		() => parseDictEnvValue("meta", "MYAPP_META", '"str"', "int"),
		parseError("--meta: env var 'MYAPP_META' must be a JSON object, got str"),
	);
	assert.throws(
		() => parseDictEnvValue("meta", "MYAPP_META", "3", "int"),
		parseError("--meta: env var 'MYAPP_META' must be a JSON object, got int"),
	);
	assert.throws(
		() => parseDictEnvValue("meta", "MYAPP_META", "3.5", "int"),
		parseError("--meta: env var 'MYAPP_META' must be a JSON object, got float"),
	);
	assert.throws(
		() => parseDictEnvValue("meta", "MYAPP_META", "true", "int"),
		parseError("--meta: env var 'MYAPP_META' must be a JSON object, got bool"),
	);
	assert.throws(
		() => parseDictEnvValue("meta", "MYAPP_META", "null", "int"),
		parseError(
			"--meta: env var 'MYAPP_META' must be a JSON object, got NoneType",
		),
	);
	// Value coercion errors carry the same messages as the CLI JSON path.
	assert.throws(
		() => parseDictEnvValue("meta", "MYAPP_META", '{"a": 1.5}', "int"),
		parseError("--meta: JSON value for key 'a' must be an integer, got float"),
	);
});
