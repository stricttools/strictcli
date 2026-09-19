/**
 * Value engine: strict scalar parsing (bool/int/float), choices matching,
 * error-message value formatting, and compound (list/dict) parsing.
 *
 * Behavior is pinned to the siblings (go/strictcli/parse.go and Python
 * __init__.py); where they diverge, the conformance suite is the oracle, and
 * where the oracle is silent, dict parsing follows Python (the captured
 * ground truth for divergences -- see conformance/check_error_parity.py
 * exclusions) while numeric acceptance follows Go (the stricter side).
 *
 * All message templates come from the errors.ts catalog, including the
 * Python-wording dict and positional-arg templates (their dedicated
 * "values.ts" sections there carry the divergence notes).
 */

import { resolveAtPrefix, type StdinTracker } from "./atprefix.js";
import {
	errArgInvalidChoice,
	errArgRetiredChoice,
	errArgumentExpectedFloat,
	errArgumentWrapped,
	errDictDuplicateKey,
	errDictEmptyKey,
	errDictEnvVarMustBeJsonObject,
	errDictExpectedKeyValueOrJson,
	errDictInvalidJson,
	errDictInvalidJsonInEnvVar,
	errDictJsonValueForKeyMustBeInteger,
	errDictJsonValueForKeyMustBeNumber,
	errDictJsonValueForKeyMustBeString,
	errDictJsonValueMustBeObject,
	errDictUnsupportedValueType,
	errDictValueForKey,
	errExpectedBoolean,
	errExpectedFloat,
	errExpectedInteger,
	errFlagDuplicateValue,
	errFlagInvalidChoice,
	errFlagRetiredChoice,
	errInfNotAllowed,
	errNaNNotAllowed,
	ParseError,
} from "./errors.js";
// Type-only, so the factories -> values value import stays the single runtime
// edge between the two modules.
import type { RetiredChoiceRecordView } from "./factories.js";
import { formatFloatCanonical } from "./float.js";
import type { ScalarSchema } from "./types.js";

// --- Strict scalar parsing ---

/**
 * Strict boolean parse: 1|true|yes -> true, 0|false|no -> false
 * (case-insensitive). Everything else is a ParseError.
 */
export function parseBoolStrict(s: string): boolean {
	switch (s.toLowerCase()) {
		case "1":
		case "true":
		case "yes":
			return true;
		case "0":
		case "false":
		case "no":
			return false;
		default:
			throw new ParseError(errExpectedBoolean(s));
	}
}

// Go strconv.Atoi acceptance: optional sign, decimal digits only (leading
// zeros allowed, no whitespace, no underscores, no exponent), 64-bit signed
// bounds. Python additionally accepts digit-group underscores ("1_000"); the
// conformance suite is silent there, so TS follows the stricter Go side.
const INT_RE = /^[+-]?[0-9]+$/;
const INT64_MIN = -(2n ** 63n);
const INT64_MAX = 2n ** 63n - 1n;

/** Strict integer parse to bigint with signed-64-bit bounds. */
export function parseIntStrict(s: string): bigint {
	if (!INT_RE.test(s)) {
		throw new ParseError(errExpectedInteger(s));
	}
	const n = BigInt(s);
	if (n < INT64_MIN || n > INT64_MAX) {
		throw new ParseError(errExpectedInteger(s));
	}
	return n;
}

// Decimal float grammar accepted by BOTH siblings: optional sign, digits with
// optional single underscores between them (both Go ParseFloat and Python
// float() accept "1_000.5"), optional fraction, optional decimal exponent.
// Go-only hex floats ("0x10p2") are rejected, matching Python.
const FLOAT_DIGITS = "[0-9](?:_?[0-9])*";
const FLOAT_RE = new RegExp(
	`^[+-]?(?:${FLOAT_DIGITS}(?:\\.(?:${FLOAT_DIGITS})?)?|\\.${FLOAT_DIGITS})(?:[eE][+-]?${FLOAT_DIGITS})?$`,
);

const INF_NAMES = new Set([
	"inf",
	"-inf",
	"+inf",
	"infinity",
	"-infinity",
	"+infinity",
]);

const NAN_INF_MESSAGES: readonly string[] = [
	errNaNNotAllowed(),
	errInfNotAllowed(),
];

/**
 * Strict float parse: rejects surrounding whitespace, NaN, +/-Inf (by name
 * and by overflow -- Go rejects "1e400" as out of range; Python lets it
 * become inf, violating the cross-language "floats reject Inf" rule, so the
 * Go behavior wins).
 */
export function parseFloatStrictValue(s: string): number {
	const low = s.toLowerCase();
	if (low === "nan") {
		throw new ParseError(errNaNNotAllowed());
	}
	if (INF_NAMES.has(low)) {
		throw new ParseError(errInfNotAllowed());
	}
	if (!FLOAT_RE.test(s)) {
		throw new ParseError(errExpectedFloat(s));
	}
	const v = Number(s.replaceAll("_", ""));
	if (!Number.isFinite(v)) {
		throw new ParseError(errExpectedFloat(s));
	}
	return v;
}

/**
 * Float parse with flag-contextualized messages: NaN/Inf messages pass
 * through under the "--flag: " prefix; every other failure becomes
 * "--flag: expected float, got '<raw>'".
 */
export function parseFloatStrictFlag(flagName: string, raw: string): number {
	try {
		return parseFloatStrictValue(raw);
	} catch (e) {
		const msg = (e as Error).message;
		if (NAN_INF_MESSAGES.includes(msg)) {
			throw new ParseError(`--${flagName}: ${msg}`);
		}
		throw new ParseError(`--${flagName}: expected float, got '${raw}'`);
	}
}

// --- Scalar coercion with flag/arg context ---

/**
 * Coerces a raw string to a scalar schema with "--flag: " error context.
 * For str, resolves the @-prefix when a stdin tracker is provided (CLI and
 * env values resolve it; config values and args do not).
 */
export function coerceToScalar(
	flagName: string,
	raw: string,
	schema: ScalarSchema,
	tracker?: StdinTracker,
): string | bigint | number | boolean {
	switch (schema) {
		case "int":
			try {
				return parseIntStrict(raw);
			} catch (e) {
				throw new ParseError(`--${flagName}: ${(e as Error).message}`);
			}
		case "float":
			return parseFloatStrictFlag(flagName, raw);
		case "str":
			return tracker !== undefined
				? resolveAtPrefix(flagName, raw, tracker)
				: raw;
		case "bool":
			// Bool flags never take a raw value token (presence/negation only).
			return raw;
	}
}

/**
 * Coerces a raw positional-arg string with "argument '<name>': " error
 * context. No @-prefix resolution on args, matching the siblings.
 */
export function coerceArgValue(
	argName: string,
	raw: string,
	schema: ScalarSchema,
): string | bigint | number | boolean {
	switch (schema) {
		case "str":
			return raw;
		case "int":
			try {
				return parseIntStrict(raw);
			} catch (e) {
				throw new ParseError(errArgumentWrapped(argName, (e as Error).message));
			}
		case "float":
			try {
				return parseFloatStrictValue(raw);
			} catch (e) {
				const msg = (e as Error).message;
				if (NAN_INF_MESSAGES.includes(msg)) {
					throw new ParseError(errArgumentWrapped(argName, msg));
				}
				throw new ParseError(errArgumentExpectedFloat(argName, raw));
			}
		case "bool":
			try {
				return parseBoolStrict(raw);
			} catch (e) {
				throw new ParseError(errArgumentWrapped(argName, (e as Error).message));
			}
	}
}

// --- Value formatting for error messages ---

/**
 * Formats a value for error messages (without quotes): bools lowercase,
 * floats in SCF (always a decimal point), dicts as sorted key=value pairs.
 */
export function formatValueForError(value: unknown): string {
	switch (typeof value) {
		case "boolean":
			return value ? "true" : "false";
		case "number":
			return formatFloatCanonical(value);
		case "bigint":
			return value.toString();
		case "string":
			return value;
		default:
			if (value instanceof Map) {
				return formatDictForDisplay(value as ReadonlyMap<string, unknown>);
			}
			return String(value);
	}
}

/**
 * Renders a dict value as canonical deterministic text: keys sorted
 * ascending, "key=value" pairs joined by ", " (mirroring the CLI input
 * syntax), values via formatValueForError.
 */
export function formatDictForDisplay(m: ReadonlyMap<string, unknown>): string {
	return [...m.keys()]
		.sort()
		.map((k) => `${k}=${formatValueForError(m.get(k))}`)
		.join(", ");
}

// --- Choices ---

export function formatChoices(choices: readonly unknown[]): string {
	return choices.map(formatValueForError).join(", ");
}

/**
 * Validates a resolved value against declared choices. A missing value
 * (undefined/null) is exempt: it only arises from a flag or arg declaring
 * `presence: "optional"`, never from a supplied value, and absence is never
 * matched against choices (contract §23.5). Repeatable values validate
 * element-wise.
 *
 * A RETIRED spelling is checked first, so a reader who typed a value that used
 * to work is told what replaced it instead of being handed the list it is
 * missing from. Every source that reaches this funnel today -- command line,
 * env var, config file, and the programmatic doors -- takes the retired
 * refusal for free; nothing new is resolved here.
 */
export function validateChoices(
	name: string,
	val: unknown,
	repeatable: boolean,
	choices: readonly unknown[] | undefined,
	retired: readonly RetiredChoiceRecordView[] | undefined,
	isArg: boolean,
): void {
	if (
		(choices === undefined && retired === undefined) ||
		val === undefined ||
		val === null
	) {
		return;
	}
	const check = (v: unknown): void => {
		const rc = retired?.find((r) => r.value === v);
		if (rc !== undefined) {
			const formatted = formatValueForError(v);
			throw new ParseError(
				isArg
					? errArgRetiredChoice(name, formatted, rc.message)
					: errFlagRetiredChoice(name, formatted, rc.message),
			);
		}
		if (choices === undefined || choices.includes(v)) {
			return;
		}
		const formatted = formatValueForError(v);
		throw new ParseError(
			isArg
				? errArgInvalidChoice(name, formatted, formatChoices(choices))
				: errFlagInvalidChoice(name, formatted, formatChoices(choices)),
		);
	};
	if (repeatable) {
		if (!Array.isArray(val)) {
			return;
		}
		for (const v of val) {
			check(v);
		}
		return;
	}
	check(val);
}

// --- List accumulation ---

/**
 * Returns the first duplicate value, or undefined if all elements are
 * unique. SameValueZero semantics match the siblings' equality for the
 * scalar element types (str/int/float; -0 equals 0, NaN cannot occur).
 */
export function findDuplicate(values: readonly unknown[]): unknown {
	const seen = new Set<unknown>();
	for (const v of values) {
		if (seen.has(v)) {
			return v;
		}
		seen.add(v);
	}
	return undefined;
}

/**
 * Appends a coerced element to a list flag's accumulated values, enforcing
 * unique semantics after the append (the duplicate check sees the whole
 * accumulated list, matching Go's storeValue closure).
 */
export function appendListValue(
	list: unknown[],
	value: unknown,
	unique: boolean,
	flagName: string,
): void {
	list.push(value);
	if (unique) {
		const dup = findDuplicate(list);
		if (dup !== undefined) {
			throw new ParseError(
				errFlagDuplicateValue(flagName, formatValueForError(dup)),
			);
		}
	}
}

// --- Escaped splitting (env_separator) ---

/**
 * Splits value on sep, treating backslash as escape character. Escaped sep
 * becomes literal sep. All other backslash sequences (escaped backslash,
 * backslash before any other char, trailing backslash) are preserved exactly
 * as the siblings preserve them.
 */
export function splitEscaped(value: string, sep: string): string[] {
	const parts: string[] = [];
	let current = "";
	let i = 0;
	while (i < value.length) {
		const ch = value.charAt(i);
		if (ch === "\\") {
			if (i + 1 < value.length) {
				const next = value.charAt(i + 1);
				if (next === sep) {
					current += sep;
				} else if (next === "\\") {
					current += "\\\\";
				} else {
					current += `\\${next}`;
				}
				i += 2;
			} else {
				// Trailing backslash: the siblings append a double backslash.
				current += "\\\\";
				i++;
			}
		} else if (ch === sep) {
			parts.push(current);
			current = "";
			i++;
		} else {
			current += ch;
			i++;
		}
	}
	parts.push(current);
	return parts;
}

// --- Dict parsing (Python ground truth; see check_error_parity exclusions) ---

/**
 * A JSON number annotated with its source token so int and float tokens stay
 * distinguishable ("3" vs "3.0" both become the JS number 3). Produced by
 * the number-tagging reviver, consumed by the dict value coercers.
 */
interface TaggedJsonNumber {
	readonly jsonNumber: true;
	/** True when the source token has no fraction or exponent part. */
	readonly isInt: boolean;
	readonly num: number;
	readonly source: string;
}

function isTaggedNumber(v: unknown): v is TaggedJsonNumber {
	return (
		typeof v === "object" &&
		v !== null &&
		(v as TaggedJsonNumber).jsonNumber === true
	);
}

/** JSON.parse with every number wrapped as a TaggedJsonNumber. Throws SyntaxError. */
function parseJsonTagged(text: string): unknown {
	return JSON.parse(
		text,
		(_key: string, value: unknown, context?: { source?: string }) => {
			if (typeof value === "number" && context?.source !== undefined) {
				const tagged: TaggedJsonNumber = {
					jsonNumber: true,
					isInt: !/[.eE]/.test(context.source),
					num: value,
					source: context.source,
				};
				return tagged;
			}
			return value;
		},
	);
}

/** Python _config_typename vocabulary for JSON-decoded values. */
function jsonConfigTypename(v: unknown): string {
	if (isTaggedNumber(v)) {
		return v.isInt ? "int" : "float";
	}
	if (typeof v === "boolean") {
		return "bool";
	}
	if (typeof v === "string") {
		return "str";
	}
	if (v === null) {
		return "null";
	}
	if (Array.isArray(v)) {
		return "array";
	}
	return "object";
}

/**
 * Python native type names (type(x).__name__ vocabulary), used by the
 * env-var "must be a JSON object" message.
 */
function jsonNativeTypename(v: unknown): string {
	if (isTaggedNumber(v)) {
		return v.isInt ? "int" : "float";
	}
	if (typeof v === "boolean") {
		return "bool";
	}
	if (typeof v === "string") {
		return "str";
	}
	if (v === null) {
		return "NoneType";
	}
	if (Array.isArray(v)) {
		return "list";
	}
	return "dict";
}

function isJsonObject(v: unknown): v is Record<string, unknown> {
	return (
		typeof v === "object" &&
		v !== null &&
		!Array.isArray(v) &&
		!isTaggedNumber(v)
	);
}

/**
 * Coerces a JSON-decoded dict value to the declared value schema. JSON int
 * values are parsed to bigint from the source token (arbitrary precision,
 * matching Python's unbounded JSON ints -- the int64 bound applies to CLI
 * key=value parsing only, exactly as in Python).
 */
function coerceDictJsonValue(
	flagName: string,
	key: string,
	value: unknown,
	valueSchema: ScalarSchema,
): string | bigint | number {
	switch (valueSchema) {
		case "str":
			if (typeof value === "string") {
				return value;
			}
			throw new ParseError(
				errDictJsonValueForKeyMustBeString(
					flagName,
					key,
					jsonConfigTypename(value),
				),
			);
		case "int":
			if (isTaggedNumber(value) && value.isInt) {
				return BigInt(value.source);
			}
			throw new ParseError(
				errDictJsonValueForKeyMustBeInteger(
					flagName,
					key,
					jsonConfigTypename(value),
				),
			);
		case "float":
			if (isTaggedNumber(value)) {
				return value.num;
			}
			throw new ParseError(
				errDictJsonValueForKeyMustBeNumber(
					flagName,
					key,
					jsonConfigTypename(value),
				),
			);
		default:
			// Unreachable: dict value schemas exclude bool at the type level.
			throw new ParseError(errDictUnsupportedValueType(flagName, valueSchema));
	}
}

/**
 * Parses one dict flag occurrence from the CLI: either "key=value" (split on
 * the first "="), or a JSON object when the raw value starts with "{" (no
 * leading-whitespace tolerance, matching Python). Returns the parsed entries
 * in input order; duplicate keys within one JSON object are last-wins
 * (JSON.parse semantics, same as Python's json.loads).
 */
export function parseDictValue(
	flagName: string,
	raw: string,
	valueSchema: ScalarSchema,
): Map<string, unknown> {
	if (raw.startsWith("{")) {
		let parsed: unknown;
		try {
			parsed = parseJsonTagged(raw);
		} catch (e) {
			throw new ParseError(errDictInvalidJson(flagName, (e as Error).message));
		}
		if (!isJsonObject(parsed)) {
			throw new ParseError(
				errDictJsonValueMustBeObject(flagName, jsonNativeTypename(parsed)),
			);
		}
		const result = new Map<string, unknown>();
		for (const [k, v] of Object.entries(parsed)) {
			result.set(k, coerceDictJsonValue(flagName, k, v, valueSchema));
		}
		return result;
	}
	const eqIdx = raw.indexOf("=");
	if (eqIdx < 0) {
		throw new ParseError(errDictExpectedKeyValueOrJson(flagName, raw));
	}
	const key = raw.slice(0, eqIdx);
	const valStr = raw.slice(eqIdx + 1);
	if (key === "") {
		throw new ParseError(errDictEmptyKey(flagName, raw));
	}
	let coerced: string | bigint | number;
	switch (valueSchema) {
		case "int":
			try {
				coerced = parseIntStrict(valStr);
			} catch (e) {
				throw new ParseError(
					errDictValueForKey(flagName, key, (e as Error).message),
				);
			}
			break;
		case "float":
			// No key context on float value errors, matching Python's
			// _float_parse_error call in _parse_dict_value.
			coerced = parseFloatStrictFlag(flagName, valStr);
			break;
		default:
			coerced = valStr;
			break;
	}
	return new Map([[key, coerced]]);
}

/**
 * Merges parsed entries into a dict flag's accumulated value. A key already
 * present from an earlier occurrence is a hard error (Python semantics;
 * check_error_parity.py records Go's silent-overwrite as the excluded side).
 */
export function storeDictEntries(
	target: Map<string, unknown>,
	entries: ReadonlyMap<string, unknown>,
	flagName: string,
): void {
	for (const [k, v] of entries) {
		if (target.has(k)) {
			throw new ParseError(errDictDuplicateKey(flagName, k));
		}
		target.set(k, v);
	}
}

/**
 * Parses a dict flag's env var value, which must be a whole JSON object.
 * Coercion errors carry the same messages as the CLI JSON path (no env
 * suffix), matching Python.
 */
export function parseDictEnvValue(
	flagName: string,
	envVar: string,
	envVal: string,
	valueSchema: ScalarSchema,
): Map<string, unknown> {
	let parsed: unknown;
	try {
		parsed = parseJsonTagged(envVal);
	} catch (e) {
		throw new ParseError(
			errDictInvalidJsonInEnvVar(flagName, envVar, (e as Error).message),
		);
	}
	if (!isJsonObject(parsed)) {
		throw new ParseError(
			errDictEnvVarMustBeJsonObject(
				flagName,
				envVar,
				jsonNativeTypename(parsed),
			),
		);
	}
	const result = new Map<string, unknown>();
	for (const [k, v] of Object.entries(parsed)) {
		result.set(k, coerceDictJsonValue(flagName, k, v, valueSchema));
	}
	return result;
}
