/**
 * Config subsystem: file loading (JSON + TOML), value coercion, config
 * fields, the --config/XDG path model, and five auto-registered `config`
 * subcommands. The subcommands are show, set, path, edit, and init.
 *
 * Parity sources: go/strictcli/config.go and the Python config sections;
 * where they diverge, Python is the ground truth (per the port convention),
 * pinned by conformance/cases/config*.json. Subcommand output strings that
 * are inline fmt/f-strings in BOTH siblings stay inline here too (the
 * values.ts precedent); genuinely new templates (TOML 1.0 gate, app-level
 * config option validation) live in errors.ts.
 *
 * Value model: config ints are bigint end-to-end (JSON int tokens and TOML
 * integers), floats are number, dict-flag values coerce to Map. The JSON
 * loader is a small strict parser (not JSON.parse) because the sibling
 * behavior needs two things V8 cannot give: the int/float distinction from
 * the source token, and 1-based line/column error positions for the
 * "config file <path>: <msg> (line X, column Y)" surface.
 */

import { existsSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import type { AppImpl, RegisteredCommand } from "./app.js";
import {
	defineFrameworkCommand,
	GroupImpl,
	markFrameworkHandler,
} from "./app.js";
import type { Context } from "./context.js";
import {
	errConfigDictKeyTypeMismatch,
	errConfigElementTypeMismatch,
	errConfigExpectedArrayForRepeatableFlag,
	errConfigExpectedBooleanGot,
	errConfigExpectedIntegerGot,
	errConfigExpectedObjectForDictFlag,
	errConfigExpectedScalarGotArray,
	errConfigExpectedStringGot,
	errConfigFieldConflictsFramework,
	errConfigFieldDefaultMismatch,
	errConfigFieldFlagDefaultDisagree,
	errConfigFieldHelpRequired,
	errConfigFieldNameInvalid,
	errConfigFieldNameReserved,
	errConfigFieldTypeBad,
	errDuplicateConfigField,
	errDuplicateFrameworkField,
	errExpectedBoolGot,
	errExpectedFloatGot,
	errExpectedIntGot,
	errExpectedStrGot,
	errFrameworkFieldConflictsUser,
	errFrameworkFieldHelpRequired,
	errFrameworkFieldMustStartUnderscore,
	errFrameworkFieldNameInvalid,
	RegistrationError,
} from "./errors.js";
import {
	type AnyCommand,
	type AnyFlag,
	arg,
	choice,
	elemSchemaOf,
	flag,
	flagOpts,
	flagParamName,
	memberChoiceFlag,
	type Presence,
	pyRepr,
	schemaKind,
} from "./factories.js";
import { formatFloatCanonical } from "./float.js";
import { expandTilde, isInfraRootPath, serializeInfraMarker } from "./infra.js";
// Type-only, deliberately: parse.ts reads this module's pre-typed value check,
// so a runtime import back the other way would close a cycle.
import type { ConfigLoadResult, ConfigProvider } from "./parse.js";
import {
	deepEqualTrees,
	parseTomlConfig,
	renderTomlValue,
	TomlLoadFailure,
	tomlDeleteKey,
	tomlSetKey,
} from "./toml.js";
import type { Carrier, ScalarSchema } from "./types.js";
import { t } from "./types.js";
import {
	findDuplicate,
	formatValueForError,
	parseBoolStrict,
	parseFloatStrictValue,
	parseIntStrict,
	splitEscaped,
} from "./values.js";

// --- Strict JSON parsing (positions + bigint ints) ---

/** A JSON config document failed to parse. line/column are 1-based; offset is 0-based. */
export class JsonLoadFailure extends Error {
	readonly line: number;
	readonly column: number;
	readonly offset: number;

	constructor(message: string, line: number, column: number, offset: number) {
		super(message);
		this.name = "JsonLoadFailure";
		this.line = line;
		this.column = column;
		this.offset = offset;
	}

	/** Python str(JSONDecodeError) form: "msg: line L column C (char N)". */
	pyDecodeErrorString(): string {
		return `${this.message}: line ${this.line} column ${this.column} (char ${this.offset})`;
	}
}

const JSON_WS = new Set([" ", "\t", "\n", "\r"]);
const JSON_NUMBER_RE = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/;

/** 1-based (line, column) of a character offset, matching Python's math. */
function textPosition(text: string, offset: number): [number, number] {
	let line = 1;
	let last = -1;
	for (let i = 0; i < offset && i < text.length; i++) {
		if (text[i] === "\n") {
			line++;
			last = i;
		}
	}
	return [line, offset - last];
}

/**
 * Minimal strict JSON parser: objects become null-prototype records
 * (insertion order preserved, no prototype pollution), integer tokens become
 * bigint, fraction/exponent tokens become number. Error messages use the
 * Python json vocabulary ("Expecting value", ...), since Python is the
 * message ground truth and V8's messages carry no reliable position.
 */
export function parseJsonConfig(text: string): unknown {
	let i = 0;

	const fail = (msg: string, at = i): never => {
		const [line, column] = textPosition(text, at);
		throw new JsonLoadFailure(msg, line, column, at);
	};

	const skipWs = (): void => {
		while (i < text.length && JSON_WS.has(text[i] as string)) {
			i++;
		}
	};

	const parseString = (): string => {
		const start = i;
		i++; // opening quote
		let out = "";
		for (;;) {
			if (i >= text.length) {
				fail("Unterminated string starting at", start);
			}
			const ch = text[i] as string;
			if (ch === '"') {
				i++;
				return out;
			}
			if (ch === "\\") {
				const esc = text[i + 1];
				switch (esc) {
					case '"':
					case "\\":
					case "/":
						out += esc;
						i += 2;
						break;
					case "b":
						out += "\b";
						i += 2;
						break;
					case "f":
						out += "\f";
						i += 2;
						break;
					case "n":
						out += "\n";
						i += 2;
						break;
					case "r":
						out += "\r";
						i += 2;
						break;
					case "t":
						out += "\t";
						i += 2;
						break;
					case "u": {
						const hex = text.slice(i + 2, i + 6);
						if (!/^[0-9a-fA-F]{4}$/.test(hex)) {
							fail("Invalid \\uXXXX escape", i);
						}
						out += String.fromCharCode(Number.parseInt(hex, 16));
						i += 6;
						break;
					}
					default:
						fail("Invalid \\escape", i);
				}
				continue;
			}
			if ((ch.codePointAt(0) as number) < 0x20) {
				fail("Invalid control character at", i);
			}
			out += ch;
			i++;
		}
	};

	const parseValue = (): unknown => {
		if (i >= text.length) {
			fail("Expecting value");
		}
		const ch = text[i] as string;
		if (ch === "{") {
			i++;
			skipWs();
			const obj: Record<string, unknown> = Object.create(null);
			if (text[i] === "}") {
				i++;
				return obj;
			}
			for (;;) {
				if (text[i] !== '"') {
					fail("Expecting property name enclosed in double quotes");
				}
				const key = parseString();
				skipWs();
				if (text[i] !== ":") {
					fail("Expecting ':' delimiter");
				}
				i++;
				skipWs();
				obj[key] = parseValue();
				skipWs();
				if (text[i] === ",") {
					i++;
					skipWs();
					continue;
				}
				if (text[i] === "}") {
					i++;
					return obj;
				}
				fail("Expecting ',' delimiter");
			}
		}
		if (ch === "[") {
			i++;
			skipWs();
			const arr: unknown[] = [];
			if (text[i] === "]") {
				i++;
				return arr;
			}
			for (;;) {
				arr.push(parseValue());
				skipWs();
				if (text[i] === ",") {
					i++;
					skipWs();
					continue;
				}
				if (text[i] === "]") {
					i++;
					return arr;
				}
				fail("Expecting ',' delimiter");
			}
		}
		if (ch === '"') {
			return parseString();
		}
		if (ch === "-" || (ch >= "0" && ch <= "9")) {
			const m = JSON_NUMBER_RE.exec(text.slice(i));
			if (m === null) {
				fail("Expecting value");
			}
			const tok = (m as RegExpExecArray)[0];
			i += tok.length;
			return /[.eE]/.test(tok) ? Number(tok) : BigInt(tok);
		}
		if (text.startsWith("true", i)) {
			i += 4;
			return true;
		}
		if (text.startsWith("false", i)) {
			i += 5;
			return false;
		}
		if (text.startsWith("null", i)) {
			i += 4;
			return null;
		}
		fail("Expecting value");
		return undefined; // unreachable
	};

	skipWs();
	const value = parseValue();
	skipWs();
	if (i < text.length) {
		fail("Extra data");
	}
	return value;
}

// --- Nested plain-object helpers (dot-separated config keys) ---

function isRecord(v: unknown): v is Record<string, unknown> {
	return (
		typeof v === "object" &&
		v !== null &&
		!Array.isArray(v) &&
		!(v instanceof Map) &&
		!(v instanceof Date)
	);
}

/** Looks up a dot-separated key; ok=false when any segment is missing/non-map. */
export function nestedGet(
	data: Record<string, unknown>,
	dottedKey: string,
): { readonly ok: boolean; readonly value: unknown } {
	const parts = dottedKey.split(".");
	let current: unknown = data;
	for (const part of parts.slice(0, -1)) {
		if (!isRecord(current) || !Object.hasOwn(current, part)) {
			return { ok: false, value: undefined };
		}
		current = current[part];
	}
	const last = parts[parts.length - 1] as string;
	if (!isRecord(current) || !Object.hasOwn(current, last)) {
		return { ok: false, value: undefined };
	}
	return { ok: true, value: current[last] };
}

/** Sets a dot-separated key, creating (or replacing non-map) intermediates. */
export function nestedSet(
	data: Record<string, unknown>,
	dottedKey: string,
	value: unknown,
): void {
	const parts = dottedKey.split(".");
	let current = data;
	for (const part of parts.slice(0, -1)) {
		const next = current[part];
		if (isRecord(next)) {
			current = next;
		} else {
			const created: Record<string, unknown> = Object.create(null);
			current[part] = created;
			current = created;
		}
	}
	current[parts[parts.length - 1] as string] = value;
}

/** Deletes a dot-separated key, pruning now-empty intermediate maps. */
export function nestedDelete(
	data: Record<string, unknown>,
	dottedKey: string,
): boolean {
	const parts = dottedKey.split(".");
	const parents: [Record<string, unknown>, string][] = [];
	let current = data;
	for (const part of parts.slice(0, -1)) {
		const next = current[part];
		if (!isRecord(next)) {
			return false;
		}
		parents.push([current, part]);
		current = next;
	}
	const last = parts[parts.length - 1] as string;
	if (!Object.hasOwn(current, last)) {
		return false;
	}
	delete current[last];
	for (let i = parents.length - 1; i >= 0; i--) {
		const [parent, key] = parents[i] as [Record<string, unknown>, string];
		const child = parent[key] as Record<string, unknown>;
		if (Object.keys(child).length === 0) {
			delete parent[key];
		}
	}
	return true;
}

/** Flattens a nested record to dot-separated leaf key paths. */
export function collectNestedKeys(
	data: Record<string, unknown>,
	prefix = "",
): string[] {
	const keys: string[] = [];
	for (const [k, v] of Object.entries(data)) {
		const fullKey = prefix === "" ? k : `${prefix}.${k}`;
		if (isRecord(v)) {
			keys.push(...collectNestedKeys(v, fullKey));
		} else {
			keys.push(fullKey);
		}
	}
	return keys;
}

// --- Config file path and loading ---

/**
 * The config file path for an app: the override (with ~ expanded, Python
 * behavior) when present, else $XDG_CONFIG_HOME/<name>/config.<ext> with
 * ~/.config as the XDG fallback.
 */
export function configFilePath(
	appName: string,
	override: string | undefined,
	format: string,
): string {
	if (override !== undefined && override !== "") {
		return expandTilde(override);
	}
	const configHome = process.env.XDG_CONFIG_HOME ?? join(homedir(), ".config");
	const ext = format === "toml" ? "toml" : "json";
	return join(configHome, appName, `config.${ext}`);
}

export interface ConfigFileResult {
	readonly data: Record<string, unknown>;
	/** Non-empty when the file exists but is malformed (with position info). */
	readonly parseErr?: string;
}

/**
 * Loads the config file. Missing file with isRuntimeFlag (the user passed
 * --config) is a hard error; missing file otherwise is soft (empty data).
 * Malformed files are always hard errors with 1-based position info.
 */
export function loadConfigFile(
	appName: string,
	override: string | undefined,
	format: string,
	isRuntimeFlag: boolean,
): ConfigFileResult {
	const path = configFilePath(appName, override, format);
	let text: string;
	try {
		text = readFileSync(path, "utf8");
	} catch {
		if (isRuntimeFlag) {
			return { data: {}, parseErr: `config file not found: ${path}` };
		}
		return { data: {} };
	}
	if (format === "toml") {
		try {
			return { data: parseTomlConfig(text) };
		} catch (e) {
			if (e instanceof TomlLoadFailure) {
				const pos =
					e.line !== undefined ? ` (line ${e.line}, column ${e.column})` : "";
				return {
					data: {},
					parseErr: `config file ${path}: ${e.message}${pos}`,
				};
			}
			throw e;
		}
	}
	let parsed: unknown;
	try {
		parsed = parseJsonConfig(text);
	} catch (e) {
		if (e instanceof JsonLoadFailure) {
			return {
				data: {},
				parseErr: `config file ${path}: ${e.message} (line ${e.line}, column ${e.column})`,
			};
		}
		throw e;
	}
	if (!isRecord(parsed)) {
		// The Go side hard-errors here (cannot unmarshal into a map); Python
		// tolerates it and misbehaves later. Hard error, with position 1:1.
		return {
			data: {},
			parseErr: `config file ${path}: expected object, got ${configTypename(parsed)} (line 1, column 1)`,
		};
	}
	return { data: parsed };
}

// --- Type names and coercion ---

/** Python _config_typename vocabulary for config-decoded values. */
export function configTypename(v: unknown): string {
	if (typeof v === "boolean") {
		return "bool";
	}
	if (typeof v === "bigint") {
		return "int";
	}
	if (typeof v === "number") {
		return "float";
	}
	if (typeof v === "string") {
		return "str";
	}
	if (v === null || v === undefined) {
		return "null";
	}
	if (Array.isArray(v)) {
		return "array";
	}
	if (isRecord(v) || v instanceof Map) {
		return "object";
	}
	const ctor = (v as { constructor?: { name?: string } }).constructor?.name;
	return ctor !== undefined && ctor !== "" ? ctor : typeof v;
}

/**
 * Coerces one config value to a scalar schema with the long type-name
 * vocabulary ("expected boolean/integer/string/float"), the flag coercion
 * path. Throws a plain Error with the bare message.
 *
 * Exported because a pre-typed value handed to a programmatic front door poses
 * the identical question -- does this value satisfy the declared type
 * (§24.11)? -- and parse.ts asks it of every supplied positional.
 */
export function coerceConfigScalarLong(
	value: unknown,
	schema: ScalarSchema,
): unknown {
	switch (schema) {
		case "bool":
			if (typeof value === "boolean") {
				return value;
			}
			throw new Error(errConfigExpectedBooleanGot(configTypename(value)));
		case "int":
			if (typeof value === "bigint") {
				return value;
			}
			// Floats never coerce to int (Python semantics; Go accepts integral
			// floats, Python is the divergence ground truth).
			throw new Error(errConfigExpectedIntegerGot(configTypename(value)));
		case "float":
			if (typeof value === "bigint") {
				return Number(value);
			}
			if (typeof value === "number") {
				return value;
			}
			throw new Error(errExpectedFloatGot(configTypename(value)));
		case "str":
			if (typeof value === "string") {
				return value;
			}
			throw new Error(errConfigExpectedStringGot(configTypename(value)));
	}
}

/**
 * JSON has no bigint, so an integer arriving over the machine boundary is a
 * `number` where every other layer of this implementation carries one as a
 * `bigint` -- this module's own JSON parser turns an integer token into a
 * bigint before any value is checked. This is that same normalization for the
 * values a caller hands in directly, and it runs whatever the declaration
 * says: it decides what the value IS, which is what a refusal must name, and
 * only then does the declaration decide whether that is acceptable. A
 * fractional number, a string and a null are all left exactly as they are.
 */
export function widenJsonIntegers(value: unknown): unknown {
	const widen = (v: unknown): unknown =>
		typeof v === "number" && Number.isInteger(v) ? BigInt(v) : v;
	if (Array.isArray(value)) {
		return value.map(widen);
	}
	if (isRecord(value)) {
		return Object.fromEntries(
			Object.entries(value).map(([k, v]) => [k, widen(v)]),
		);
	}
	return widen(value);
}

/**
 * Coerces a config value to a flag's type: dict flags take objects (-> Map),
 * list flags take arrays, scalars take scalars. Throws a plain Error with the
 * bare message; parse.ts wraps it as "--flag: config value error: <msg>".
 */
export function coerceConfigValueForFlag(value: unknown, f: AnyFlag): unknown {
	const kind = schemaKind(f.schema);
	const elem = elemSchemaOf(f.carrier);
	if (kind === "dict") {
		if (!isRecord(value)) {
			throw new Error(
				errConfigExpectedObjectForDictFlag(configTypename(value)),
			);
		}
		const result = new Map<string, unknown>();
		for (const [k, v] of Object.entries(value)) {
			try {
				result.set(k, coerceConfigScalarLong(v, elem));
			} catch {
				throw new Error(
					errConfigDictKeyTypeMismatch(k, elem, configTypename(v)),
				);
			}
		}
		return result;
	}
	if (Array.isArray(value)) {
		if (kind !== "list") {
			throw new Error(errConfigExpectedScalarGotArray());
		}
		const result: unknown[] = [];
		for (const [i, el] of value.entries()) {
			try {
				result.push(coerceConfigScalarLong(el, elem));
			} catch {
				throw new Error(
					errConfigElementTypeMismatch(i, elem, configTypename(el)),
				);
			}
		}
		return result;
	}
	if (kind === "list") {
		throw new Error(
			errConfigExpectedArrayForRepeatableFlag(configTypename(value)),
		);
	}
	return coerceConfigScalarLong(value, f.schema as ScalarSchema);
}

// --- Config fields ---

/**
 * Declares a typed config file field. Fields with no default are required
 * (the config system errors when they are missing); fields with a default
 * are optional. Dots in the field name form TOML sections.
 */
export interface ConfigFieldSpec<Out = unknown> {
	/** The scalar type carrier (t.str, t.bool, t.int, or t.float). */
	readonly type: Carrier<Out, ScalarSchema>;
	/** Help text displayed in `config show` output. */
	readonly help: string;
	/** Default value; omit to make the field required. */
	readonly default?: Out;
}

/** Runtime record of a declared config field. */
export interface ConfigFieldRt {
	readonly name: string;
	readonly schema: ScalarSchema;
	readonly help: string;
	readonly default: unknown;
	readonly hasDefault: boolean;
	/** Computed: no default means required. */
	readonly required: boolean;
}

const CONFIG_FIELD_NAME_RE = /^_?[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$/;
const SCALAR_SCHEMAS: ReadonlySet<string> = new Set([
	"str",
	"bool",
	"int",
	"float",
]);

function matchesScalarSchema(schema: ScalarSchema, v: unknown): boolean {
	switch (schema) {
		case "str":
			return typeof v === "string";
		case "bool":
			return typeof v === "boolean";
		case "int":
			return typeof v === "bigint";
		case "float":
			return typeof v === "number";
	}
}

/**
 * Registration-time agreement check for a flag colliding with a config field
 * (a validation-only coexistence): explicit defaults on both sides must be
 * equal. A flag has a default exactly when its DECLARED presence is "default"
 * (contract §23.1) -- never when its default value happens to be neither
 * undefined nor null, which would stand the value's shape in for the
 * declaration.
 */
export function checkFlagConfigFieldDefault(
	flagName: string,
	flagPresence: Presence,
	flagDefault: unknown,
	cf: ConfigFieldRt,
): void {
	const flagHas = flagPresence === "default";
	if (flagHas && cf.hasDefault && !deepEqualTrees(flagDefault, cf.default)) {
		throw new RegistrationError(
			errConfigFieldFlagDefaultDisagree(
				cf.name,
				flagName,
				pyRepr(cf.default),
				pyRepr(flagDefault),
			),
		);
	}
}

/** Declares a typed config file field on the app (App.configField delegate). */
export function registerConfigField(
	app: AppImpl,
	name: string,
	spec: ConfigFieldSpec,
): void {
	// Python check order: reserved prefix, duplicates, framework conflicts,
	// then the ConfigField construction checks (help, type, name, default).
	if (name.startsWith("_")) {
		throw new RegistrationError(errConfigFieldNameReserved(name));
	}
	if (app.configFields.has(name)) {
		throw new RegistrationError(errDuplicateConfigField(name));
	}
	if (app.frameworkFields.has(name)) {
		throw new RegistrationError(errConfigFieldConflictsFramework(name));
	}
	if (typeof spec.help !== "string" || spec.help.trim() === "") {
		throw new RegistrationError(errConfigFieldHelpRequired(name));
	}
	const schema = (spec.type as { schema?: unknown } | undefined)?.schema;
	if (typeof schema !== "string" || !SCALAR_SCHEMAS.has(schema)) {
		throw new RegistrationError(errConfigFieldTypeBad(String(schema)));
	}
	if (!CONFIG_FIELD_NAME_RE.test(name)) {
		throw new RegistrationError(errConfigFieldNameInvalid(name));
	}
	const hasDefault = "default" in spec;
	if (
		hasDefault &&
		!matchesScalarSchema(schema as ScalarSchema, spec.default)
	) {
		throw new RegistrationError(
			errConfigFieldDefaultMismatch(name, pyRepr(spec.default), schema),
		);
	}
	const cf: ConfigFieldRt = {
		name,
		schema: schema as ScalarSchema,
		help: spec.help,
		default: spec.default,
		hasDefault,
		required: !hasDefault,
	};
	// A config field colliding with an existing flag's param name annotates
	// the flag; their defaults must agree. Flags registered after this field
	// are checked from the command-registration side instead.
	for (const f of collectAllFlags(app)) {
		if (flagParamName(f.name) === name) {
			checkFlagConfigFieldDefault(
				f.name,
				flagOpts(f).presence,
				flagOpts(f).default,
				cf,
			);
		}
	}
	app.configFields.set(name, cf);
}

/**
 * Declares an internal framework config field (underscore-prefixed names,
 * never exposed to users). Framework fields are always required-shaped (no
 * default) and exist for key-recognition only.
 */
export function registerFrameworkField(
	app: AppImpl,
	name: string,
	type: Carrier<unknown, ScalarSchema>,
	help: string,
): void {
	if (!name.startsWith("_")) {
		throw new RegistrationError(errFrameworkFieldMustStartUnderscore(name));
	}
	if (!CONFIG_FIELD_NAME_RE.test(name)) {
		throw new RegistrationError(errFrameworkFieldNameInvalid(name));
	}
	if (typeof help !== "string" || help.trim() === "") {
		throw new RegistrationError(errFrameworkFieldHelpRequired(name));
	}
	const schema = (type as { schema?: unknown } | undefined)?.schema;
	if (typeof schema !== "string" || !SCALAR_SCHEMAS.has(schema)) {
		throw new RegistrationError(errConfigFieldTypeBad(String(schema)));
	}
	if (app.frameworkFields.has(name)) {
		throw new RegistrationError(errDuplicateFrameworkField(name));
	}
	if (app.configFields.has(name)) {
		throw new RegistrationError(errFrameworkFieldConflictsUser(name));
	}
	app.frameworkFields.set(name, {
		name,
		schema: schema as ScalarSchema,
		help,
		default: undefined,
		hasDefault: false,
		required: true,
	});
}

/** Short-type-name check of a config file value against a field's declared type. */
function checkConfigFieldType(
	cf: ConfigFieldRt,
	value: unknown,
): string | undefined {
	const got = configTypename(value);
	switch (cf.schema) {
		case "bool":
			if (typeof value !== "boolean") {
				return errExpectedBoolGot(got);
			}
			break;
		case "int":
			if (typeof value !== "bigint") {
				return errExpectedIntGot(got);
			}
			break;
		case "float":
			if (typeof value !== "bigint" && typeof value !== "number") {
				return errExpectedFloatGot(got);
			}
			break;
		case "str":
			if (typeof value !== "string") {
				return errExpectedStrGot(got);
			}
			break;
	}
	return undefined;
}

/**
 * Parse-time config-field validation (Python step 2.5): every bound required
 * field must exist with the declared type, and every key in the config file
 * must be known (a flag param name, config field, or framework field).
 * Returns an error message, or undefined when all checks pass.
 */
export function validateConfigFieldsForCommand(
	app: AppImpl,
	cmdConfigFields: readonly string[],
	data: Readonly<Record<string, unknown>>,
): string | undefined {
	for (const fieldName of cmdConfigFields) {
		const cf = app.configFields.get(fieldName);
		if (cf === undefined) {
			// Defensive: bindings are validated at registration.
			return `config field "${fieldName}" is not registered`;
		}
		const found = nestedGet(data as Record<string, unknown>, fieldName);
		if (!found.ok) {
			if (cf.required) {
				return `required config field "${fieldName}" is missing from config file`;
			}
			continue;
		}
		const err = checkConfigFieldType(cf, found.value);
		if (err !== undefined) {
			return `config field "${fieldName}": ${err}`;
		}
	}
	const knownKeys = new Set<string>();
	for (const f of collectAllFlags(app)) {
		knownKeys.add(flagParamName(f.name));
	}
	for (const name of app.configFields.keys()) {
		knownKeys.add(name);
	}
	for (const name of app.frameworkFields.keys()) {
		knownKeys.add(name);
	}
	for (const key of collectNestedKeys(data as Record<string, unknown>)) {
		if (!knownKeys.has(key)) {
			return `unknown key "${key}" in config file`;
		}
	}
	return undefined;
}

// --- Flag collection and colliding fields ---

/**
 * All flags visible to the config system: global flags plus every command's
 * flags across all groups (first occurrence per name wins), skipping the
 * auto-generated config group itself.
 */
export function collectAllFlags(app: AppImpl): AnyFlag[] {
	const flags: AnyFlag[] = [...app.globalFlags];
	const seen = new Set<string>(flags.map((f) => f.name));
	const addFrom = (commands: ReadonlyMap<string, RegisteredCommand>): void => {
		for (const cmd of commands.values()) {
			for (const f of cmd.flags) {
				if (!seen.has(f.name)) {
					flags.push(f);
					seen.add(f.name);
				}
			}
		}
	};
	addFrom(app.commands);
	const walkGroup = (grp: GroupImpl): void => {
		addFrom(grp.commands);
		for (const sub of grp.groups.values()) {
			walkGroup(sub);
		}
	};
	for (const [name, grp] of app.groups) {
		if (name === "config") {
			continue; // the auto-generated config group
		}
		walkGroup(grp);
	}
	return flags;
}

/**
 * Config fields whose name equals a flag's param name, keyed by that name.
 * Such fields are validation-only: they annotate the colliding flag and
 * render once (on the flag), not as a separate config key.
 */
export function collidingConfigFields(
	app: AppImpl,
): Map<string, ConfigFieldRt> {
	const result = new Map<string, ConfigFieldRt>();
	if (app.configFields.size === 0) {
		return result;
	}
	const flagParams = new Set(
		collectAllFlags(app).map((f) => flagParamName(f.name)),
	);
	for (const [name, cf] of app.configFields) {
		if (flagParams.has(name)) {
			result.set(name, cf);
		}
	}
	return result;
}

// --- Display formatting ---

/** Python json string escaping (ensure_ascii=True). */
function pyJsonString(s: string): string {
	let out = '"';
	for (const ch of s) {
		const code = ch.codePointAt(0) as number;
		if (ch === '"' || ch === "\\") {
			out += `\\${ch}`;
		} else if (ch === "\n") {
			out += "\\n";
		} else if (ch === "\r") {
			out += "\\r";
		} else if (ch === "\t") {
			out += "\\t";
		} else if (ch === "\b") {
			out += "\\b";
		} else if (ch === "\f") {
			out += "\\f";
		} else if (code < 0x20 || code > 0x7e) {
			if (code > 0xffff) {
				// Astral plane: UTF-16 surrogate pair, like Python's json.
				for (let k = 0; k < ch.length; k++) {
					out += `\\u${ch.charCodeAt(k).toString(16).padStart(4, "0")}`;
				}
			} else {
				out += `\\u${code.toString(16).padStart(4, "0")}`;
			}
		} else {
			out += ch;
		}
	}
	return `${out}"`;
}

/**
 * Python-json.dumps-shaped serialization: ", "/": " separators (or indented
 * layout), bigint as bare integer tokens, floats in SCF, Maps as objects with
 * sorted keys (the TS dict display rule), plain objects in insertion order
 * unless sortKeys.
 */
export function jsonDumpsPy(
	value: unknown,
	indent?: number,
	sortKeys = false,
): string {
	const pad = (level: number): string =>
		indent === undefined ? "" : " ".repeat(indent * level);
	const dump = (v: unknown, level: number): string => {
		if (v === null || v === undefined) {
			return "null";
		}
		switch (typeof v) {
			case "boolean":
				return v ? "true" : "false";
			case "bigint":
				return v.toString();
			case "number":
				return formatFloatCanonical(v);
			case "string":
				return pyJsonString(v);
			default:
				break;
		}
		if (Array.isArray(v)) {
			if (v.length === 0) {
				return "[]";
			}
			const items = v.map((el) => dump(el, level + 1));
			if (indent === undefined) {
				return `[${items.join(", ")}]`;
			}
			return `[\n${items.map((s) => pad(level + 1) + s).join(",\n")}\n${pad(level)}]`;
		}
		let entries: [string, unknown][];
		if (v instanceof Map) {
			entries = [...(v as Map<unknown, unknown>).entries()]
				.map(([k, val]): [string, unknown] => [String(k), val])
				.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
		} else if (isRecord(v)) {
			entries = Object.entries(v);
			if (sortKeys) {
				entries.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
			}
		} else {
			return pyJsonString(String(v));
		}
		if (entries.length === 0) {
			return "{}";
		}
		const items = entries.map(
			([k, val]) => `${pyJsonString(k)}: ${dump(val, level + 1)}` as const,
		);
		if (indent === undefined) {
			return `{${items.join(", ")}}`;
		}
		return `{\n${items.map((s) => pad(level + 1) + s).join(",\n")}\n${pad(level)}}`;
	};
	return dump(value, 0);
}

/** Formats a config value for `config show` output (Python _format_config_value). */
export function formatConfigValue(v: unknown): string {
	if (v === null || v === undefined) {
		return "<nil>";
	}
	// A marker renders as the DECLARATION -- `RelativeToRoot('E', 'x')`, the
	// form the siblings print byte-for-byte -- because `config show` displays
	// what the configuration says, where the resolved path is what a run
	// produces (§18.27 item 261, §25.10). Serializing the marker object would
	// print its own internals, which no declaration was written in.
	if (isInfraRootPath(v)) {
		return String(v);
	}
	if (v instanceof Map || Array.isArray(v) || isRecord(v)) {
		return jsonDumpsPy(v);
	}
	if (typeof v === "boolean") {
		return v ? "true" : "false";
	}
	if (typeof v === "number") {
		return formatFloatCanonical(v);
	}
	if (typeof v === "bigint") {
		return v.toString();
	}
	if (typeof v === "string") {
		return v;
	}
	return String(v);
}

/**
 * Effective value and source for a flag in the `config show` context.
 * Precedence: env > config > default. "cli" is structurally impossible here
 * (config show is a subcommand; the app's own flags were never passed).
 */
export function resolveFlagShowSource(
	f: AnyFlag,
	configData: Readonly<Record<string, unknown>>,
): [value: unknown, source: string] {
	const envVar = flagOpts(f).env;
	if (envVar !== undefined) {
		const envVal = process.env[envVar];
		if (envVal !== undefined) {
			// Coerce for display; on failure show the raw string (the parse-time
			// error path handles actual errors). Python keys the coercion on
			// f.type, which is the ELEMENT type for repeatable flags and `dict`
			// (no scalar branch) for dict flags -- so dict flags stay raw.
			const elem = elemSchemaOf(f.carrier);
			try {
				if (f.schema === "bool") {
					return [parseBoolStrict(envVal), "env"];
				}
				if (schemaKind(f.schema) !== "dict") {
					if (elem === "int") {
						return [parseIntStrict(envVal), "env"];
					}
					if (elem === "float") {
						return [parseFloatStrictValue(envVal), "env"];
					}
				}
			} catch {
				return [envVal, "env"];
			}
			return [envVal, "env"];
		}
	}
	const param = flagParamName(f.name);
	if (Object.hasOwn(configData, param)) {
		return [configData[param], "config"];
	}
	// The declared presence decides, for every carrier: a compound flag that
	// declares `optional` shows absence, exactly as a scalar one does, and the
	// silent []/{} normalization is gone with the derivation behind it
	// (contract §23.4).
	const o = flagOpts(f);
	return o.presence === "default"
		? [o.default, "default"]
		: [undefined, "default"];
}

// --- Template generation (config init) ---

/** Renders a flag/field default as a TOML token, Python-repr-ing markers. */
function renderTemplateTomlValue(v: unknown): string {
	if (isInfraRootPath(v)) {
		return renderTomlValue(String(v));
	}
	return renderTomlValue(v);
}

/**
 * A flag's declared default for template rendering, or undefined when the
 * flag declares `required` or `optional` -- those declare no value, so the
 * template has nothing to render for them (contract §23.4).
 */
function normalizeFlagTemplateDefault(f: AnyFlag): unknown {
	const o = flagOpts(f);
	return o.presence === "default" ? o.default : undefined;
}

/** TOML template with comments (Python _generate_config_template_toml). */
export function generateTomlTemplate(app: AppImpl): string {
	const lines: string[] = [];
	const flags = collectAllFlags(app);
	const colliding = collidingConfigFields(app);

	for (const f of flags) {
		const param = flagParamName(f.name);
		let comment = `# ${f.opts.help}`;
		const cfCollide = colliding.get(param);
		if (cfCollide !== undefined) {
			comment += ` -- ${cfCollide.help}`;
		}
		lines.push(comment);
		const dflt = normalizeFlagTemplateDefault(f);
		if (dflt !== undefined && dflt !== null) {
			lines.push(`${param} = ${renderTemplateTomlValue(dflt)}`);
		} else {
			lines.push(`# ${param} =`);
		}
		lines.push("");
	}

	const topLevel: ConfigFieldRt[] = [];
	const sections = new Map<string, ConfigFieldRt[]>();
	for (const [name, cf] of app.configFields) {
		if (colliding.has(name)) {
			continue; // rendered once, on the flag line above
		}
		const parts = name.split(".");
		if (parts.length === 1) {
			topLevel.push(cf);
		} else {
			const section = parts[0] as string;
			const list = sections.get(section);
			if (list === undefined) {
				sections.set(section, [cf]);
			} else {
				list.push(cf);
			}
		}
	}

	for (const cf of topLevel) {
		const req = cf.required ? " (required)" : "";
		lines.push(`# ${cf.help}${req}`);
		if (!cf.required) {
			lines.push(`${cf.name} = ${renderTemplateTomlValue(cf.default)}`);
		} else {
			lines.push(`# ${cf.name} =`);
		}
		lines.push("");
	}

	for (const [section, fields] of sections) {
		lines.push(`[${section}]`);
		for (const cf of fields) {
			const subKey = cf.name.split(".").slice(1).join(".");
			const deeperParts = subKey.split(".");
			const req = cf.required ? " (required)" : "";
			if (deeperParts.length > 1) {
				const subSection = `${section}.${deeperParts[0]}`;
				const leafKey = deeperParts.slice(1).join(".");
				lines.push("");
				lines.push(`[${subSection}]`);
				lines.push(`# ${cf.help}${req}`);
				if (!cf.required) {
					lines.push(`${leafKey} = ${renderTemplateTomlValue(cf.default)}`);
				} else {
					lines.push(`# ${leafKey} =`);
				}
			} else {
				lines.push(`# ${cf.help}${req}`);
				if (!cf.required) {
					lines.push(`${subKey} = ${renderTemplateTomlValue(cf.default)}`);
				} else {
					lines.push(`# ${subKey} =`);
				}
			}
		}
		lines.push("");
	}

	return lines.length > 0 ? `${lines.join("\n")}\n` : "";
}

/** JSON template (Python _generate_config_template_json). */
export function generateJsonTemplate(app: AppImpl): string {
	const data: Record<string, unknown> = Object.create(null);
	const flags = collectAllFlags(app);
	const colliding = collidingConfigFields(app);

	for (const f of flags) {
		const param = flagParamName(f.name);
		const dflt = normalizeFlagTemplateDefault(f);
		data[param] =
			dflt === undefined || dflt === null
				? null
				: isInfraRootPath(dflt)
					? String(dflt)
					: dflt;
	}
	for (const [name, cf] of app.configFields) {
		if (colliding.has(name)) {
			continue;
		}
		nestedSet(data, name, cf.required ? null : cf.default);
	}
	return `${jsonDumpsPy(data, 2)}\n`;
}

// --- Config file mutation (config set) ---

function readFileOrEmpty(path: string): string {
	try {
		return readFileSync(path, "utf8");
	} catch {
		return "";
	}
}

/**
 * Persists `key` = `value` THROUGH `ctx.effects`: the TOML path uses the
 * comment-preserving splicer on the file bytes; JSON re-serializes the
 * in-memory data (already mutated). Returns the handler exit code.
 *
 * The write is a `FILE_WRITE` on the effects handle, not a bare
 * `writeFileSync`: `config set` is classified `mutating`, so under `--dry-run`
 * the write must be RECORDED, never performed. A framework command that printed
 * "DRY RUN — no changes were made." while rewriting the user's config file
 * would be the loudest possible counterexample to its own regime.
 */
function writeConfigSet(
	app: AppImpl,
	data: Record<string, unknown>,
	path: string,
	key: string,
	value: unknown,
	ctx: Context,
): number {
	nestedSet(data, key, value);
	if (app.configFormat === "toml") {
		const text = readFileOrEmpty(path);
		let newText: string;
		try {
			newText = tomlSetKey(text, key, value);
		} catch (e) {
			ctx.error(`cannot update config: ${(e as Error).message}`);
			return 1;
		}
		ctx.effects.write(path, newText);
		return 0;
	}
	ctx.effects.write(path, `${jsonDumpsPy(data, 2)}\n`);
	return 0;
}

/**
 * Removes `key` and persists. Returns "absent" when the key was not in the
 * loaded data, otherwise the handler exit code. The write rides `ctx.effects`
 * for the reason spelled out above.
 */
function writeConfigUnset(
	app: AppImpl,
	data: Record<string, unknown>,
	path: string,
	key: string,
	ctx: Context,
): number | "absent" {
	if (!nestedDelete(data, key)) {
		return "absent";
	}
	if (app.configFormat === "toml") {
		const text = readFileOrEmpty(path);
		let newText: string;
		try {
			newText = tomlDeleteKey(text, key);
		} catch (e) {
			ctx.error(`cannot update config: ${(e as Error).message}`);
			return 1;
		}
		ctx.effects.write(path, newText);
		return 0;
	}
	ctx.effects.write(path, `${jsonDumpsPy(data, 2)}\n`);
	return 0;
}

/**
 * Records/performs the config file's parent directory creation.
 *
 * The existence probe is an ordinary filesystem READ (never an effect), and
 * branching on it is branching on a real value, so the preview walks straight
 * through it in both modes. Probing keeps the preview honest: a `mkdir` line
 * appears only when a directory would really be created.
 */
function ensureConfigDir(path: string, ctx: Context): void {
	const dir = dirname(path);
	if (dir !== "" && !existsSync(dir)) {
		ctx.effects.mkdir(dir);
	}
}

// --- config set handlers ---

/**
 * `config set`'s arguments: the key, and the elected member of its write
 * selection (contract §27.1, §18.33 item 304).
 *
 * The write is an EXACTLY-ONE SELECTION over a value, a clear and a reset to
 * default -- a member-spelled selector (§24.4). The shape it replaced was two
 * bools declaring `default: false` plus an optional positional, with four
 * hand-rolled guards holding its illegal corners shut, and §27.1's
 * mutating-default ban refuses exactly that: a framework cannot ship a
 * registration guard its own command does not pass, and an exemption for
 * framework-owned commands would be the escape hatch this regime refuses
 * everywhere else.
 */
interface ConfigSetArgs {
	readonly key: string;
	readonly write:
		| { readonly choice: "value"; readonly value: string }
		| { readonly choice: "clear" }
		| { readonly choice: "default" };
}

/** `config set` for a config field (Python _config_set_field). */
function configSetField(
	app: AppImpl,
	key: string,
	write: ConfigSetArgs["write"],
	cf: ConfigFieldRt,
	data: Record<string, unknown>,
	path: string,
	ctx: Context,
): number {
	// The elected member says what to write. The hand-rolled "exactly one of
	// the three" guards are gone with the bools that made them expressible --
	// electing none is the framework's own unsatisfied-selector refusal, and
	// electing two is unrepresentable (§27.1).
	if (write.choice === "clear") {
		ctx.error("config set: --clear is only for repeatable flags");
		return 1;
	}
	if (write.choice === "default") {
		const r = writeConfigUnset(app, data, path, key, ctx);
		if (r === "absent") {
			ctx.error(`config set: key '${key}' not in config`);
			return 1;
		}
		return r;
	}
	// The value member carries its payload under the reserved field name.
	const value = write.value;
	let typed: unknown;
	try {
		typed = coerceSetScalar(value, cf.schema);
	} catch (e) {
		ctx.error(`config set: key '${key}': ${(e as Error).message}`);
		return 1;
	}
	return writeConfigSet(app, data, path, key, typed, ctx);
}

/** Strict string-to-scalar coercion for config set values. */
function coerceSetScalar(raw: string, schema: ScalarSchema): unknown {
	switch (schema) {
		case "bool":
			return parseBoolStrict(raw);
		case "int":
			return parseIntStrict(raw);
		case "float":
			// parseFloatStrictValue already produces the sibling messages:
			// NaN/Inf pass through, everything else is "expected float, got '.'".
			return parseFloatStrictValue(raw);
		case "str":
			return raw;
	}
}

/** `config set` for a registered flag (the main path). */
function configSetFlag(
	app: AppImpl,
	key: string,
	write: ConfigSetArgs["write"],
	f: AnyFlag,
	data: Record<string, unknown>,
	path: string,
	ctx: Context,
): number {
	const kind = schemaKind(f.schema);
	const elem = elemSchemaOf(f.carrier);

	// The elected member says what to write. "--clear and --default are
	// mutually exclusive", "cannot provide a value with --clear" and "provide a
	// value, --clear, or --default" are all unrepresentable now: exactly one
	// member is elected, and electing none is the framework's own
	// unsatisfied-selector refusal (§27.1).
	if (write.choice === "clear") {
		let cleared: unknown;
		if (kind === "dict") {
			cleared = Object.create(null) as Record<string, unknown>;
		} else if (kind === "list") {
			cleared = [];
		} else {
			ctx.error("config set: --clear is only for repeatable flags");
			return 1;
		}
		return writeConfigSet(app, data, path, key, cleared, ctx);
	}

	if (write.choice === "default") {
		const r = writeConfigUnset(app, data, path, key, ctx);
		if (r === "absent") {
			ctx.error(`config set: key '${key}' not in config`);
			return 1;
		}
		return r;
	}

	// The value member carries its payload under the reserved field name.
	const value = write.value;
	let typed: unknown;
	if (kind === "dict") {
		// Dict flags take a JSON object value (Python semantics).
		let parsed: unknown;
		try {
			parsed = parseJsonConfig(value as string);
		} catch (e) {
			const detail =
				e instanceof JsonLoadFailure
					? e.pyDecodeErrorString()
					: (e as Error).message;
			ctx.error(`config set: key '${key}': invalid JSON: ${detail}`);
			return 1;
		}
		if (!isRecord(parsed)) {
			ctx.error(`config set: key '${key}': expected JSON object`);
			return 1;
		}
		const typedDict: Record<string, unknown> = Object.create(null);
		for (const [dk, dv] of Object.entries(parsed)) {
			try {
				typedDict[dk] = coerceConfigScalarLong(dv, elem);
			} catch (e) {
				ctx.error(
					`config set: key '${key}': value for '${dk}': ${(e as Error).message}`,
				);
				return 1;
			}
		}
		typed = typedDict;
	} else if (kind === "list") {
		const parts = splitEscaped(value as string, ",");
		const coerced: unknown[] = [];
		for (const p of parts) {
			try {
				coerced.push(coerceSetScalar(p, elem));
			} catch (e) {
				ctx.error(`config set: key '${key}': ${(e as Error).message}`);
				return 1;
			}
		}
		if (flagOpts(f).unique === true) {
			const dup = findDuplicate(coerced);
			if (dup !== undefined) {
				ctx.error(
					`config set: key '${key}': duplicate value '${formatValueForError(dup)}'`,
				);
				return 1;
			}
		}
		typed = coerced;
	} else {
		try {
			typed = coerceSetScalar(value as string, f.schema as ScalarSchema);
		} catch (e) {
			ctx.error(`config set: key '${key}': ${(e as Error).message}`);
			return 1;
		}
	}

	return writeConfigSet(app, data, path, key, typed, ctx);
}

// --- config show handler ---

/**
 * Recursively sorts object keys, matching Go's map marshaling. Used by the
 * framework's own machine payloads so the three implementations emit
 * byte-identical documents; a consumer's payload is emitted as supplied.
 */
function deepSorted(value: unknown): unknown {
	if (Array.isArray(value)) {
		return value.map(deepSorted);
	}
	if (typeof value === "object" && value !== null) {
		const src = value as Record<string, unknown>;
		const out: Record<string, unknown> = {};
		for (const key of Object.keys(src).sort()) {
			out[key] = deepSorted(src[key]);
		}
		return out;
	}
	return value;
}

/**
 * config show's machine payload contract (contract §19.5): one object keyed by
 * flag/config-field name, plus the "__infrastructure__" entry. The keys are
 * dynamic, so the declaration names the container only. Framework-owned
 * literal, byte-identical across the three implementations.
 */
const CONFIG_SHOW_PAYLOAD_SCHEMA: Readonly<Record<string, unknown>> = {
	type: "object",
};

function configShowHandler(app: AppImpl, ctx: Context): number {
	// --json is framework-owned (contract §19.1): the object below is this
	// command's payload, not a locally-flagged print, and it is supplied
	// UNCONDITIONALLY (§19.4). Instance validation lives at the emission seam,
	// so a config value machine mode could not carry -- a float above 2^53 --
	// costs the human rendering nothing.
	if (app.configParseErr !== undefined) {
		ctx.error(app.configParseErr);
		return 1;
	}
	const configData = (app.configData ?? {}) as Record<string, unknown>;
	const allFlags = collectAllFlags(app);
	const colliding = collidingConfigFields(app);

	const result: Record<string, unknown> = Object.create(null);
	for (const f of allFlags) {
		const param = flagParamName(f.name);
		const [value, source] = resolveFlagShowSource(f, configData);
		// The machine form publishes the declaration too, in §13's
		// machine-stable marker shape -- the one the dumped schema already uses
		// (§25.10) -- rather than whatever the runtime's own object happens to
		// serialize as.
		result[param] = {
			value: isInfraRootPath(value)
				? serializeInfraMarker(value)
				: (value ?? null),
			source,
		};
	}
	for (const [cfName, cf] of app.configFields) {
		if (colliding.has(cfName)) {
			continue; // validation-only: rendered once, on the flag entry
		}
		const found = nestedGet(configData, cfName);
		let value: unknown;
		let source: string;
		if (found.ok) {
			value = found.value;
			source = "config";
		} else if (cf.hasDefault) {
			value = cf.default;
			source = "default";
		} else {
			value = null;
			source = "not set";
		}
		const entry: Record<string, unknown> = {
			value: value ?? null,
			source,
			type: cf.schema,
			required: cf.required,
			help: cf.help,
		};
		if (cf.hasDefault) {
			entry.default = cf.default;
		}
		result[cfName] = entry;
	}
	if (
		app.infraRoots.size > 0 ||
		app.handshakeEnvs.size > 0 ||
		app.connectionEnvs.size > 0
	) {
		const infra: Record<string, unknown> = Object.create(null);
		for (const [ev, resolved] of app.infraRoots) {
			infra[ev] = {
				kind: "root",
				source: app.infraRootFromEnv.get(ev) === true ? "env" : "default",
				resolved,
			};
		}
		for (const [ev, helpText] of app.handshakeEnvs) {
			const live = process.env[ev];
			const entry: Record<string, unknown> = {
				kind: "handshake",
				set: live !== undefined,
				help: helpText,
			};
			if (live !== undefined) {
				entry.value = live;
			}
			infra[ev] = entry;
		}
		for (const [ev, helpText] of app.connectionEnvs) {
			const live = process.env[ev];
			const entry: Record<string, unknown> = {
				kind: "connection",
				set: live !== undefined,
				help: helpText,
			};
			if (live !== undefined) {
				entry.value = live;
			}
			infra[ev] = entry;
		}
		result.__infrastructure__ = infra;
	}
	// Sorted keys at every level: the three implementations build this
	// object in three orders (Go marshals a map, which sorts recursively),
	// and the payload is compared byte-for-byte by conformance.
	ctx.payload(deepSorted(result));

	// The human rendering is unconditional too, and goes through the context
	// writers, so in machine mode the same lines ride the envelope's
	// diagnostics (§19.1) exactly as the check command's table does.
	for (const f of allFlags) {
		const param = flagParamName(f.name);
		const [value, source] = resolveFlagShowSource(f, configData);
		let line = `${param} = ${formatConfigValue(value)}  (source: ${source})`;
		const cfCollide = colliding.get(param);
		if (cfCollide !== undefined) {
			line += `  -- ${cfCollide.help}`;
		}
		ctx.info(line);
	}
	const nonColliding = [...app.configFields.entries()].filter(
		([name]) => !colliding.has(name),
	);
	if (nonColliding.length > 0) {
		ctx.info("");
		ctx.info("Config fields:");
		for (const [cfName, cf] of nonColliding) {
			const found = nestedGet(configData, cfName);
			let value: unknown;
			let source: string;
			if (found.ok) {
				value = found.value;
				source = "config";
			} else if (cf.hasDefault) {
				value = cf.default;
				source = "default";
			} else {
				value = undefined;
				source = "not set";
			}
			const reqStr = cf.required ? "required" : "optional";
			ctx.info(
				`  ${cfName} (${cf.schema}, ${reqStr}) = ${formatConfigValue(value)}  (source: ${source})  -- ${cf.help}`,
			);
		}
	}
	if (
		app.infraRoots.size > 0 ||
		app.handshakeEnvs.size > 0 ||
		app.connectionEnvs.size > 0
	) {
		ctx.info("");
		ctx.info("Infrastructure:");
		for (const [ev, resolved] of app.infraRoots) {
			const src = app.infraRootFromEnv.get(ev) === true ? "env-set" : "default";
			ctx.info(`  ${ev} (root) = ${resolved}  (source: ${src})`);
		}
		for (const [ev, helpText] of app.handshakeEnvs) {
			const live = process.env[ev];
			if (live !== undefined) {
				ctx.info(`  ${ev} (handshake) = ${live}  (set)  -- ${helpText}`);
			} else {
				ctx.info(`  ${ev} (handshake) = <unset>  -- ${helpText}`);
			}
		}
		for (const [ev, helpText] of app.connectionEnvs) {
			const live = process.env[ev];
			if (live !== undefined) {
				ctx.info(`  ${ev} (connection) = ${live}  (set)  -- ${helpText}`);
			} else {
				ctx.info(`  ${ev} (connection) = <unset>  -- ${helpText}`);
			}
		}
	}
	return 0;
}

// --- The auto-registered config group ---

/**
 * Registers the `config` command group (path/show/set/edit/init) on the app.
 * Commands are installed directly (bypassing the app-context collision
 * checks), mirroring Python's direct Command construction -- user global
 * flags named e.g. "json" must not collide with config subcommand flags.
 */
export function registerConfigGroup(app: AppImpl): void {
	const grp = new GroupImpl(
		"config",
		"Manage persistent configuration values stored in the config file",
		[],
		[],
		false,
		app,
	);
	app.groups.set("config", grp);

	// The five subcommands go through the SINGLE validated registration path,
	// exactly like every consumer command: there is no direct-carrier
	// construction bypass left. Their handlers absorb the app's app-defined
	// global flag values, which is legal only because they declare forwarding,
	// and the framework-internal marker makes registration verify that each
	// handler is one strictcli itself minted.
	const install = (def: AnyCommand): void => {
		grp.command(def);
	};

	install(
		defineFrameworkCommand("path", "read_only", {
			help: "Print the absolute path to this application's config file and nothing else, so the value can be piped straight into another command. The path is $XDG_CONFIG_HOME/<app>/config.<toml|json> (falling back to ~/.config), or the explicit override the application was built with. Printing it does not create the file, and reports the same path whether or not one exists yet.",
			handler: markFrameworkHandler((_args: unknown, ctx: Context) => {
				ctx.info(
					configFilePath(app.name, app.configPathOverride, app.configFormat),
				);
				return 0;
			}) as never,
		}),
	);

	install(
		defineFrameworkCommand("show", "read_only", {
			help: "Show every flag and config field with its effective value and where that value came from, resolved through the precedence chain environment variable, then config file, then declared default. Declared infrastructure roots, handshake and connection environment variables are listed too. Choose --plain for an aligned human-readable table; the framework-owned --json yields the same information as a machine-readable object carrying each entry's type, default and help text.",
			// --plain is the only local flag left: the machine form moved to the
			// framework-owned --json (contract §7.5's sweep box), which cannot
			// be declared here, so the two-flag mutex group went with it.
			flags: {
				plain: flag("plain", t.bool, {
					help: "Display config values in a human-readable table format",
					presence: "default",
					default: false,
				}),
			},
			payloadSchema: CONFIG_SHOW_PAYLOAD_SCHEMA,
			handler: markFrameworkHandler((_args: unknown, ctx: Context) =>
				configShowHandler(app, ctx),
			) as never,
		}),
	);

	install(
		defineFrameworkCommand("set", "mutating", {
			help: "Write a persistent value into the config file so it overrides a flag's declared default on every later run. The value is coerced to the flag's own type and rejected if it does not fit: repeatable flags take a comma-separated list (backslash-escape a literal comma) and are checked for duplicates, dict flags take a JSON object. Use --default to drop a key back to its default, and --clear to empty a repeatable flag.",
			args: [
				arg("key", t.str, {
					help: "The config key to set, matching a registered flag name",
					presence: "required",
				}),
			],
			flags: {
				// The write selection (contract §27.1, §18.33 item 304): an
				// exactly-one selection over a value, a clear and a reset to the
				// declared default. The three illegal corners the old bools made
				// expressible are unrepresentable now.
				write: memberChoiceFlag(
					"write",
					{
						value: choice({
							help: "Write a value at the key",
							value: {
								carrier: t.str,
								help: "Write this value at the key, coerced to the key's own type (comma-separated for a repeatable flag, backslash-escaping a literal comma; a JSON object for a dict flag)",
							},
						}),
						clear: choice({ help: "Clear a repeatable flag" }),
						default: choice({ help: "Reset the key to its declared default" }),
					},
					{
						help: "What to write at the key: a value, a clear, or a reset to the declared default",
						presence: "required",
					},
				),
			},
			handler: markFrameworkHandler((args: ConfigSetArgs, ctx: Context) =>
				configSetDispatch(app, args, ctx),
			) as never,
		}),
	);

	install(
		defineFrameworkCommand("edit", "mutating", {
			help: "Open this application's config file in the editor named by $EDITOR, falling back to vi. The parent directory and an empty config file are created first if they do not exist, so the editor always opens something. Launching the editor counts as a mutation: under --dry-run the command records the editor invocation and opens nothing.",
			interactive: true,
			handler: markFrameworkHandler((_args: unknown, ctx: Context) => {
				const path = configFilePath(
					app.name,
					app.configPathOverride,
					app.configFormat,
				);
				ensureConfigDir(path, ctx);
				if (!existsSync(path)) {
					ctx.effects.write(path, app.configFormat === "toml" ? "" : "{}\n");
				}
				const editor = process.env.EDITOR || "vi";
				// LAUNCHING AN EDITOR IS A MUTATION. Routed through the handle, a
				// dry run records `run: <editor> <path>` and never opens anything;
				// a bare spawnSync here would open the user's editor during a run
				// that announced it would change nothing.
				//
				// check: true (the default) is what keeps the preview walking: a
				// failed operation is an error, not a value (§2.5.4), so nothing
				// here ever reads an exit code off a carrier.
				try {
					ctx.effects.run([editor, path], { stream: true });
				} catch (e) {
					ctx.error(`editor failed: ${(e as Error).message}`);
					return 1;
				}
				return 0;
			}) as never,
		}),
	);

	install(
		defineFrameworkCommand("init", "mutating", {
			help: "Create a starter config file listing every flag and config field the application declares, each commented with its help text, type and default value, so the file documents itself. The format follows whichever of TOML or JSON the application was built for. Refuses with an error if a config file already exists rather than overwriting it; the created path is printed on success.",
			handler: markFrameworkHandler((_args: unknown, ctx: Context) => {
				const path = configFilePath(
					app.name,
					app.configPathOverride,
					app.configFormat,
				);
				if (existsSync(path)) {
					ctx.error(`config init: config file already exists: ${path}`);
					return 1;
				}
				ensureConfigDir(path, ctx);
				const content =
					app.configFormat === "toml"
						? generateTomlTemplate(app)
						: generateJsonTemplate(app);
				ctx.effects.write(path, content);
				ctx.info(path);
				return 0;
			}) as never,
		}),
	);
}

function configSetDispatch(
	app: AppImpl,
	args: ConfigSetArgs,
	ctx: Context,
): number {
	const key = args.key;
	const path = configFilePath(
		app.name,
		app.configPathOverride,
		app.configFormat,
	);
	// Every mutation this handler performs rides ctx.effects: the command is
	// classified `mutating`, so a dry run must RECORD them and change nothing.
	ensureConfigDir(path, ctx);
	// The data loaded at parse time (Python uses _config_data the same way).
	const existing = (app.configData ?? {}) as Record<string, unknown>;

	const allFlags = collectAllFlags(app);
	const matchedFlag = allFlags.find((f) => flagParamName(f.name) === key);
	const matchedField =
		matchedFlag === undefined ? app.configFields.get(key) : undefined;
	if (matchedFlag === undefined && matchedField === undefined) {
		ctx.error(`config set: unknown key '${key}'`);
		return 1;
	}
	if (matchedField !== undefined) {
		return configSetField(
			app,
			key,
			args.write,
			matchedField,
			existing,
			path,
			ctx,
		);
	}
	return configSetFlag(
		app,
		key,
		args.write,
		matchedFlag as AnyFlag,
		existing,
		path,
		ctx,
	);
}

// --- The parse-pipeline config provider ---

/**
 * The ConfigProvider installed by app.dispatch: loads the config file per
 * parse (recording data and parse errors on the app for the config
 * subcommands), coerces raw config values to flag types, and runs the
 * step-2.5 config-field validation.
 */
export function makeConfigProvider(app: AppImpl): ConfigProvider {
	return {
		load(runtimePathOverride: string | undefined): ConfigLoadResult {
			app.configParseErr = undefined;
			// no-default-config-path: without an explicit --config, nothing loads.
			if (app.noDefaultConfigPath && runtimePathOverride === undefined) {
				app.configData = {};
				return { data: {} };
			}
			const override = runtimePathOverride ?? app.configPathOverride;
			const result = loadConfigFile(
				app.name,
				override,
				app.configFormat,
				runtimePathOverride !== undefined,
			);
			if (result.parseErr !== undefined) {
				app.configData = {};
				app.configParseErr = result.parseErr;
				return { data: {}, parseErr: result.parseErr };
			}
			app.configData = result.data;
			return { data: result.data };
		},
		coerce(f: AnyFlag, value: unknown): unknown {
			return coerceConfigValueForFlag(value, f);
		},
		validateFields(
			cmdConfigFields: readonly string[],
			data: Readonly<Record<string, unknown>>,
		): string | undefined {
			// Python gates step 2.5 on the app having declared config fields.
			if (app.configFields.size === 0) {
				return undefined;
			}
			return validateConfigFieldsForCommand(app, cmdConfigFields, data);
		},
	};
}
