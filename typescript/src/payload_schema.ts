/**
 * The declared payload schema's validator (effects contract §19.5).
 *
 * Two duties over one deliberately closed subset:
 *
 * - registration-time validation of the declared literal -- an unknown keyword
 *   anywhere is a hard error, which is what keeps the subset closed;
 * - emission-time validation of the value a handler supplies through
 *   `ctx.payload` -- a payload that deviates from its declaration fails here
 *   rather than shipping a wrong shape.
 *
 * Every detail string in this module is byte-identical to the Python and Go
 * validators'. They live here rather than in errors.ts on purpose: errors.ts is
 * the catalog conformance/check_error_parity.py extracts, and it carries the
 * two OUTER templates (errPayloadSchemaInvalid, errPayloadInvalid). The details
 * are pinned across implementations by the shared vectors at
 * go/strictcli/testdata/payload_schema_vectors.json instead.
 *
 * JavaScript's own hazard is closed here by construction: every property test
 * goes through `Object.hasOwn` and every key enumeration through
 * `Object.keys`, so `__proto__`, `toString` and `constructor` behave as the
 * ordinary keys they are. Two shipping JavaScript validators fail `required`
 * on exactly those keys today.
 */

/** The closed subset, in the order the "unknown keyword" message lists it. */
export const PAYLOAD_SCHEMA_KEYWORDS = [
	"additionalProperties",
	"const",
	"enum",
	"items",
	"properties",
	"required",
	"type",
] as const;

/** The JSON Schema type names the subset admits, sorted. */
export const PAYLOAD_JSON_TYPES = [
	"array",
	"boolean",
	"integer",
	"null",
	"number",
	"object",
	"string",
] as const;

/**
 * Decision 16's guard. Every IEEE-754 double whose magnitude exceeds 2^53 is
 * already an integer (the spacing between representable doubles is at least 1
 * from 2^52 upward), so "any integer above 2^53" and "any number above 2^53"
 * are the same set -- which is why the guard is a plain magnitude test.
 */
const PAYLOAD_MAX_MAGNITUDE = 2 ** 53;

const PDETAIL_NOT_JSON = "the value is not representable in JSON";
/**
 * Exported because §12.14's registration guard reuses this clause BYTE-FOR-BYTE
 * (`Flag "<name>": choice <v>: <clause>`): the same condition at a second
 * boundary -- there a value being written into the envelope, here one being
 * written into the schema file -- and a second wording for one fact is what
 * §12.13's reuse rule exists to prevent.
 */
export const PDETAIL_MAGNITUDE =
	"the number's magnitude exceeds 2^53 (declare a big identifier as a string)";
const PDETAIL_TYPE_SHAPE = '"type" must be a string or an array of strings';
const PDETAIL_TYPE_EMPTY = '"type" must not be an empty array';
const PDETAIL_PROPERTIES_SHAPE = '"properties" must be an object';
const PDETAIL_REQUIRED_SHAPE = '"required" must be an array of strings';
const PDETAIL_ENUM_SHAPE = '"enum" must be a non-empty array';
const PDETAIL_ADDPROPS_SHAPE =
	'"additionalProperties" must be a boolean or a schema object';
const PDETAIL_ENUM_MISMATCH =
	"the value is not one of the declared enum values";
const PDETAIL_CONST_MISMATCH = "the value does not equal the declared const";

/** One violation: where it is and what rule it broke. */
export interface PayloadFinding {
	readonly path: string;
	readonly detail: string;
}

function finding(path: string, detail: string): PayloadFinding {
	return { path, detail };
}

/**
 * §19.5's escaping regime applied to one string: escape exactly what JSON
 * mandates and emit everything else literally. `JSON.stringify` is deliberately
 * not used -- it escapes U+2028/U+2029, which JSON does not mandate and the two
 * siblings do not escape.
 */
function payloadQuote(s: string): string {
	let out = '"';
	for (const ch of s) {
		switch (ch) {
			case '"':
				out += '\\"';
				break;
			case "\\":
				out += "\\\\";
				break;
			case "\n":
				out += "\\n";
				break;
			case "\r":
				out += "\\r";
				break;
			case "\t":
				out += "\\t";
				break;
			case "\b":
				out += "\\b";
				break;
			case "\f":
				out += "\\f";
				break;
			default: {
				const code = ch.codePointAt(0) as number;
				if (code < 0x20) {
					out += `\\u${code.toString(16).padStart(4, "0")}`;
				} else {
					out += ch;
				}
			}
		}
	}
	return `${out}"`;
}

function pdetailUnknownKeyword(kw: string): string {
	return `unknown keyword ${payloadQuote(kw)} (the closed subset is: ${PAYLOAD_SCHEMA_KEYWORDS.join(", ")})`;
}

function pdetailUnknownType(t: string): string {
	return `unknown type ${payloadQuote(t)} (the JSON Schema types are: ${PAYLOAD_JSON_TYPES.join(", ")})`;
}

function pdetailSchemaNotObject(got: string): string {
	return `a schema must be an object, got ${got}`;
}

function pdetailTypeDuplicate(t: string): string {
	return `"type" has a duplicate entry ${payloadQuote(t)}`;
}

function pdetailRequiredDuplicate(k: string): string {
	return `"required" has a duplicate entry ${payloadQuote(k)}`;
}

function pdetailExpectedType(declared: string, got: string): string {
	return `expected type ${payloadQuote(declared)}, got ${got}`;
}

function pdetailExpectedTypes(
	declared: readonly string[],
	got: string,
): string {
	const inner = declared.map(payloadQuote).join(", ");
	return `expected type [${inner}], got ${got}`;
}

function pdetailRequiredMissing(k: string): string {
	return `required property ${payloadQuote(k)} is missing`;
}

function pdetailNotPermitted(k: string): string {
	return `property ${payloadQuote(k)} is not permitted (additionalProperties is false)`;
}

function payloadPathKey(path: string, key: string): string {
	return `${path}[${payloadQuote(key)}]`;
}

function payloadPathIndex(path: string, index: number): string {
	return `${path}[${index}]`;
}

/**
 * Compare two strings by Unicode code point, which is what Python's `sorted`
 * and Go's `sort.Strings` both do. JavaScript's default sort compares UTF-16
 * code units, which orders a supplementary-plane character before one in the
 * U+E000..U+FFFF range -- a divergence this closes rather than tolerates.
 */
function compareCodePoints(a: string, b: string): number {
	const as = Array.from(a);
	const bs = Array.from(b);
	const n = Math.min(as.length, bs.length);
	for (let i = 0; i < n; i++) {
		const ca = (as[i] as string).codePointAt(0) as number;
		const cb = (bs[i] as string).codePointAt(0) as number;
		if (ca !== cb) {
			return ca < cb ? -1 : 1;
		}
	}
	if (as.length === bs.length) {
		return 0;
	}
	return as.length < bs.length ? -1 : 1;
}

/** A record's own enumerable string keys, in code-point order. */
function sortedOwnKeys(obj: object): string[] {
	return Object.keys(obj).sort(compareCodePoints);
}

function isPlainRecord(v: unknown): v is Record<string, unknown> {
	return typeof v === "object" && v !== null && !Array.isArray(v);
}

/**
 * Renders a value as the tree the machine-mode serializer (outcome.ts
 * `jsonCompact`) will actually write, and returns `undefined` for the values
 * that serializer omits entirely.
 *
 * The round trip is the point, exactly as it is in Go: TypeScript's emitter has
 * its own rules -- BigInt becomes a bare integer token, a Map becomes an object
 * with sorted string keys, a `toJSON` method decides its own shape, and an
 * undefined-valued property is dropped -- and validating the pre-serialization
 * value would validate something the consumer never sees.
 *
 * BigInt is deliberately NOT collapsed to a number here: `int` values are
 * bigint end-to-end in this implementation, and 2^53+1 must reach the magnitude
 * guard as its exact value rather than as the 2^53 a double would have rounded
 * it to.
 */
function normalizePayload(v: unknown): unknown {
	if (v === null) {
		return null;
	}
	switch (typeof v) {
		case "bigint":
		case "boolean":
		case "string":
			return v;
		case "number":
			return Number.isFinite(v) ? v : NOT_REPRESENTABLE;
		case "undefined":
		case "function":
		case "symbol":
			return OMITTED;
		case "object": {
			if (Array.isArray(v)) {
				return v.map((el) => {
					const n = normalizePayload(el);
					return n === OMITTED ? null : n;
				});
			}
			if (v instanceof Map) {
				const entries = [...(v as Map<unknown, unknown>).entries()]
					.map(([k, val]): [string, unknown] => [String(k), val])
					.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
				// A null-prototype object: writing `out.__proto__ = v` on an
				// ordinary object literal would set the prototype instead of
				// creating the own property the emitter will write.
				const out = Object.create(null) as Record<string, unknown>;
				for (const [k, val] of entries) {
					const n = normalizePayload(val);
					if (n !== OMITTED) {
						out[k] = n;
					}
				}
				return out;
			}
			const withToJSON = v as { toJSON?: (key?: string) => unknown };
			if (typeof withToJSON.toJSON === "function") {
				return normalizePayload(withToJSON.toJSON());
			}
			// Null-prototype again, for the same reason: `__proto__` is an
			// ordinary key here, never a prototype assignment.
			const out = Object.create(null) as Record<string, unknown>;
			for (const [k, val] of Object.entries(v)) {
				const n = normalizePayload(val);
				if (n !== OMITTED) {
					out[k] = n;
				}
			}
			return out;
		}
		default:
			return NOT_REPRESENTABLE;
	}
}

/** Marker for a value the serializer drops (undefined, a function, a symbol). */
const OMITTED = Symbol("omitted");
/** Marker for a value no JSON document can carry (NaN, an infinity). */
const NOT_REPRESENTABLE = Symbol("not-representable");

/**
 * The JSON kind of a normalized value, or null when it is not representable.
 *
 * "integer" is reported for any number with a zero fractional part, which is
 * JSON Schema's own reading of the type and the only one three languages can
 * agree on -- TypeScript has no separate integer type at all.
 */
export function payloadKind(value: unknown): string | null {
	if (value === null) {
		return "null";
	}
	switch (typeof value) {
		case "boolean":
			return "boolean";
		case "string":
			return "string";
		case "bigint":
			return "integer";
		case "number":
			if (!Number.isFinite(value)) {
				return null;
			}
			return Number.isInteger(value) ? "integer" : "number";
		case "object":
			if (Array.isArray(value)) {
				return "array";
			}
			return "object";
		default:
			return null;
	}
}

const PAYLOAD_MAX_MAGNITUDE_BIG = BigInt(2) ** BigInt(53);

function payloadOverMagnitude(value: unknown): boolean {
	if (typeof value === "bigint") {
		return (
			value > PAYLOAD_MAX_MAGNITUDE_BIG || value < -PAYLOAD_MAX_MAGNITUDE_BIG
		);
	}
	return typeof value === "number" && Math.abs(value) > PAYLOAD_MAX_MAGNITUDE;
}

/**
 * The document check: representability and the magnitude guard, recursively,
 * over the WHOLE value before any keyword is consulted. A payload that could
 * not be emitted at all is reported as that rather than as a type mismatch.
 * Arrays traverse in index order, objects in sorted-key order.
 */
export function payloadScanValue(
	value: unknown,
	path: string,
): PayloadFinding | null {
	const kind = payloadKind(value);
	if (kind === null) {
		return finding(path, PDETAIL_NOT_JSON);
	}
	if (payloadOverMagnitude(value)) {
		return finding(path, PDETAIL_MAGNITUDE);
	}
	if (kind === "array") {
		const items = value as unknown[];
		for (let i = 0; i < items.length; i++) {
			const f = payloadScanValue(items[i], payloadPathIndex(path, i));
			if (f !== null) {
				return f;
			}
		}
	} else if (kind === "object") {
		const obj = value as Record<string, unknown>;
		for (const key of sortedOwnKeys(obj)) {
			const f = payloadScanValue(obj[key], payloadPathKey(path, key));
			if (f !== null) {
				return f;
			}
		}
	}
	return null;
}

/**
 * JSON-value equality, used by `enum` and `const`. Type-aware on purpose: a
 * boolean is never equal to a number, and two numbers are equal when their
 * values are, so 1 matches a declared 1.0.
 */
export function payloadDeepEqual(a: unknown, b: unknown): boolean {
	const ka = payloadKind(a);
	const kb = payloadKind(b);
	if (ka === null || kb === null) {
		return false;
	}
	const aNum = ka === "integer" || ka === "number";
	const bNum = kb === "integer" || kb === "number";
	if (aNum && bNum) {
		// Both sides are past the magnitude guard, so Number() is exact for a
		// bigint here and a mixed bigint/number comparison stays sound.
		return Number(a as number | bigint) === Number(b as number | bigint);
	}
	if (ka !== kb) {
		return false;
	}
	switch (ka) {
		case "null":
			return true;
		case "boolean":
		case "string":
			return a === b;
		case "array": {
			const xs = a as unknown[];
			const ys = b as unknown[];
			if (xs.length !== ys.length) {
				return false;
			}
			return xs.every((x, i) => payloadDeepEqual(x, ys[i]));
		}
		default: {
			const xs = a as Record<string, unknown>;
			const ys = b as Record<string, unknown>;
			const xk = Object.keys(xs);
			const yk = Object.keys(ys);
			if (xk.length !== yk.length) {
				return false;
			}
			return xk.every(
				(k) => Object.hasOwn(ys, k) && payloadDeepEqual(xs[k], ys[k]),
			);
		}
	}
}

function payloadTypeMatches(declared: string, kind: string): boolean {
	if (declared === "integer") {
		return kind === "integer";
	}
	if (declared === "number") {
		return kind === "integer" || kind === "number";
	}
	return declared === kind;
}

function stringList(v: unknown): string[] | null {
	if (!Array.isArray(v)) {
		return null;
	}
	const out: string[] = [];
	for (const x of v) {
		if (typeof x !== "string") {
			return null;
		}
		out.push(x);
	}
	return out;
}

/**
 * The registration-time duty (§19.5): one declared schema literal, validated
 * as written over the closed subset. The keyword scan is sorted, so which of
 * several violations is reported never depends on an object's key order.
 */
export function validatePayloadSchema(
	schema: unknown,
	path = "payload_schema",
): PayloadFinding | null {
	if (!isPlainRecord(schema)) {
		return finding(
			path,
			pdetailSchemaNotObject(payloadKind(schema) ?? "unsupported"),
		);
	}

	for (const kw of sortedOwnKeys(schema)) {
		if (!(PAYLOAD_SCHEMA_KEYWORDS as readonly string[]).includes(kw)) {
			return finding(path, pdetailUnknownKeyword(kw));
		}
	}

	if (Object.hasOwn(schema, "type")) {
		const t = schema.type;
		if (typeof t === "string") {
			if (!(PAYLOAD_JSON_TYPES as readonly string[]).includes(t)) {
				return finding(path, pdetailUnknownType(t));
			}
		} else if (Array.isArray(t)) {
			if (t.length === 0) {
				return finding(path, PDETAIL_TYPE_EMPTY);
			}
			const names = stringList(t);
			if (names === null) {
				return finding(path, PDETAIL_TYPE_SHAPE);
			}
			const seen: string[] = [];
			for (const n of names) {
				if (seen.includes(n)) {
					return finding(path, pdetailTypeDuplicate(n));
				}
				seen.push(n);
			}
			for (const n of names) {
				if (!(PAYLOAD_JSON_TYPES as readonly string[]).includes(n)) {
					return finding(path, pdetailUnknownType(n));
				}
			}
		} else {
			return finding(path, PDETAIL_TYPE_SHAPE);
		}
	}

	if (Object.hasOwn(schema, "required")) {
		const names = stringList(schema.required);
		if (names === null) {
			return finding(path, PDETAIL_REQUIRED_SHAPE);
		}
		const seen: string[] = [];
		for (const n of names) {
			if (seen.includes(n)) {
				return finding(path, pdetailRequiredDuplicate(n));
			}
			seen.push(n);
		}
	}

	if (Object.hasOwn(schema, "enum")) {
		const values = schema.enum;
		if (!Array.isArray(values) || values.length === 0) {
			return finding(path, PDETAIL_ENUM_SHAPE);
		}
		for (let i = 0; i < values.length; i++) {
			const f = payloadScanValue(
				values[i],
				payloadPathIndex(`${path}.enum`, i),
			);
			if (f !== null) {
				return f;
			}
		}
	}

	if (Object.hasOwn(schema, "const")) {
		const f = payloadScanValue(schema.const, `${path}.const`);
		if (f !== null) {
			return f;
		}
	}

	if (Object.hasOwn(schema, "properties")) {
		const props = schema.properties;
		if (!isPlainRecord(props)) {
			return finding(path, PDETAIL_PROPERTIES_SHAPE);
		}
		for (const key of sortedOwnKeys(props)) {
			const f = validatePayloadSchema(
				props[key],
				payloadPathKey(`${path}.properties`, key),
			);
			if (f !== null) {
				return f;
			}
		}
	}

	if (Object.hasOwn(schema, "items")) {
		const f = validatePayloadSchema(schema.items, `${path}.items`);
		if (f !== null) {
			return f;
		}
	}

	if (Object.hasOwn(schema, "additionalProperties")) {
		const ap = schema.additionalProperties;
		if (typeof ap !== "boolean") {
			if (!isPlainRecord(ap)) {
				return finding(path, PDETAIL_ADDPROPS_SHAPE);
			}
			const f = validatePayloadSchema(ap, `${path}.additionalProperties`);
			if (f !== null) {
				return f;
			}
		}
	}

	return null;
}

/**
 * The keyword half of the emission-time duty.
 *
 * Check order is pinned so that a value violating several constraints always
 * reports the same one: type, then const, then enum, then (for an object)
 * required, declared properties in sorted key order, and finally the additional
 * properties in sorted key order; then (for an array) the items.
 */
export function validatePayloadInstance(
	value: unknown,
	schema: Record<string, unknown>,
	path: string,
): PayloadFinding | null {
	const kind = payloadKind(value);
	if (kind === null) {
		return finding(path, PDETAIL_NOT_JSON);
	}

	if (Object.hasOwn(schema, "type")) {
		const t = schema.type;
		if (typeof t === "string") {
			if (!payloadTypeMatches(t, kind)) {
				return finding(path, pdetailExpectedType(t, kind));
			}
		} else {
			const names = stringList(t) ?? [];
			if (!names.some((n) => payloadTypeMatches(n, kind))) {
				return finding(path, pdetailExpectedTypes(names, kind));
			}
		}
	}

	if (Object.hasOwn(schema, "const")) {
		if (!payloadDeepEqual(value, schema.const)) {
			return finding(path, PDETAIL_CONST_MISMATCH);
		}
	}

	if (Object.hasOwn(schema, "enum")) {
		const values = Array.isArray(schema.enum) ? schema.enum : [];
		if (!values.some((entry) => payloadDeepEqual(value, entry))) {
			return finding(path, PDETAIL_ENUM_MISMATCH);
		}
	}

	if (kind === "object") {
		const obj = value as Record<string, unknown>;
		const props = isPlainRecord(schema.properties) ? schema.properties : null;
		const required = stringList(schema.required);
		if (required !== null) {
			for (const name of required) {
				if (!Object.hasOwn(obj, name)) {
					return finding(path, pdetailRequiredMissing(name));
				}
			}
		}
		if (props !== null) {
			for (const key of sortedOwnKeys(props)) {
				if (!Object.hasOwn(obj, key)) {
					continue;
				}
				const sub = props[key];
				if (!isPlainRecord(sub)) {
					continue;
				}
				const f = validatePayloadInstance(
					obj[key],
					sub,
					payloadPathKey(path, key),
				);
				if (f !== null) {
					return f;
				}
			}
		}
		if (Object.hasOwn(schema, "additionalProperties")) {
			const ap = schema.additionalProperties;
			if (ap !== true) {
				const sub = isPlainRecord(ap) ? ap : null;
				for (const key of sortedOwnKeys(obj)) {
					if (props !== null && Object.hasOwn(props, key)) {
						continue;
					}
					if (ap === false) {
						return finding(path, pdetailNotPermitted(key));
					}
					if (sub === null) {
						continue;
					}
					const f = validatePayloadInstance(
						obj[key],
						sub,
						payloadPathKey(path, key),
					);
					if (f !== null) {
						return f;
					}
				}
			}
		}
	}

	if (kind === "array" && Object.hasOwn(schema, "items")) {
		const sub = schema.items;
		if (isPlainRecord(sub)) {
			const items = value as unknown[];
			for (let i = 0; i < items.length; i++) {
				const f = validatePayloadInstance(
					items[i],
					sub,
					payloadPathIndex(path, i),
				);
				if (f !== null) {
					return f;
				}
			}
		}
	}

	return null;
}

/**
 * The registration-time entry point: normalize the declared literal the way
 * the schema dump will emit it, then validate it over the closed subset.
 */
export function validatePayloadSchemaLiteral(
	schema: Readonly<Record<string, unknown>>,
): PayloadFinding | null {
	const normalized = normalizePayload(schema);
	if (normalized === OMITTED || normalized === NOT_REPRESENTABLE) {
		return finding("payload_schema", PDETAIL_NOT_JSON);
	}
	return validatePayloadSchema(normalized);
}

/**
 * The whole emission-time duty: normalize through the serializer's own rules,
 * run the document check, then the keywords.
 */
export function validatePayloadValue(
	value: unknown,
	schema: Record<string, unknown>,
): PayloadFinding | null {
	const normalized = normalizePayload(value);
	if (normalized === OMITTED || normalized === NOT_REPRESENTABLE) {
		return finding("payload", PDETAIL_NOT_JSON);
	}
	const f = payloadScanValue(normalized, "payload");
	if (f !== null) {
		return f;
	}
	const normalizedSchema = normalizePayload(schema);
	if (!isPlainRecord(normalizedSchema)) {
		return finding("payload", PDETAIL_NOT_JSON);
	}
	return validatePayloadInstance(normalized, normalizedSchema, "payload");
}

// ---------------------------------------------------------------------------
// Builder sugar for the declared payload schema (contract §19.5, decision 14)
//
// Pure constructors of literals, and nothing more. They add no vocabulary and
// no semantics: each one produces exactly the object an author could have
// written, that object is the canonical artifact, and it passes the identical
// registration-time validation. A builder is a convenience for writing the
// canonical artifact, never an alternative to it -- which is why none of them
// validates anything: an unknown type name written through `schemaType` is
// rejected at registration exactly as a hand-written literal would be.
//
// The one-to-one mapping onto the closed subset is pinned across the three
// implementations by go/strictcli/testdata/payload_schema_builders.json.
// ---------------------------------------------------------------------------

/** `{"type": ...}` -- one name, or a list of them for nullability. */
export function schemaType(
	...names: readonly string[]
): Record<string, unknown> {
	if (names.length === 1) {
		return { type: names[0] };
	}
	return { type: [...names] };
}

/** `{"type": "array", "items": ...}`. */
export function schemaArray(
	items: Readonly<Record<string, unknown>>,
): Record<string, unknown> {
	return { type: "array", items };
}

/** The keywords `schemaObject` accepts, each emitted only when supplied. */
export interface SchemaObjectOpts {
	readonly properties?: Readonly<Record<string, unknown>>;
	readonly required?: readonly string[];
	readonly additionalProperties?: boolean | Readonly<Record<string, unknown>>;
}

/**
 * `{"type": "object", ...}`.
 *
 * Each keyword is emitted only when supplied, so `schemaObject()` is the bare
 * `{"type": "object"}` and an omitted `additionalProperties` means the keyword
 * is absent rather than `true` -- the same behaviour, not the same declaration.
 */
export function schemaObject(
	opts: SchemaObjectOpts = {},
): Record<string, unknown> {
	const out: Record<string, unknown> = { type: "object" };
	if (opts.properties !== undefined) {
		out.properties = opts.properties;
	}
	if (opts.required !== undefined) {
		out.required = [...opts.required];
	}
	if (opts.additionalProperties !== undefined) {
		out.additionalProperties = opts.additionalProperties;
	}
	return out;
}

/** `{"enum": [...]}`. */
export function schemaEnum(
	...values: readonly unknown[]
): Record<string, unknown> {
	return { enum: [...values] };
}

/** `{"const": ...}`. */
export function schemaConst(value: unknown): Record<string, unknown> {
	return { const: value };
}
