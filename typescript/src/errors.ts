/**
 * errors.ts centralizes every user-facing error and panic message template
 * used across the strictcli package, mirroring go/strictcli/errors.go
 * one-to-one. That mirror means the
 * same section grouping (headers keep the Go source-file labels for catalog
 * traceability -- conformance/check_error_parity.py extracts the Go catalog
 * from those sections), same "(parse-time)" section markers, and byte-identical
 * output for identical inputs.
 *
 * Conventions:
 * - Go %q slots are reproduced via q() (strconv.Quote semantics).
 * - Slots that embed pre-formatted values (Go %v / %T, and any float) take the
 *   already-formatted string as the parameter, so this module stays
 *   formatting-agnostic (the shortest-canonical float formatter lands in
 *   float.ts in a later subphase).
 * - Go %d slots take number parameters.
 * - Go error-typed parameters become errStr: string (the message text).
 */

import { PDETAIL_MAGNITUDE } from "./payload_schema.js";

/** Thrown for registration-time validation failures (Go: panic / Python: ValueError). */
export class RegistrationError extends Error {
	constructor(message: string) {
		super(message);
		this.name = "RegistrationError";
	}
}

/** Thrown for parse-time failures (printed to stderr, process exits with code 1). Internal; not re-exported. */
export class ParseError extends Error {
	constructor(message: string) {
		super(message);
		this.name = "ParseError";
	}
}

/**
 * Thrown by app.call() / app.jsonSchema() when programmatic invocation fails
 * (unknown command, missing required flags, mutex violations, dependency
 * errors). Mirrors Go's InvokeError (invoke.go) and Python's InvokeError.
 */
export class InvokeError extends Error {
	constructor(message: string) {
		super(message);
		this.name = "InvokeError";
	}
}

/**
 * Thrown when an effect operation fails: a `run` whose child exits nonzero, an
 * `http` whose status is outside 200-299, or invalid UTF-8 on a captured
 * stream. A failed operation is an error, not a value; `check: false` opts a
 * single call out. Mirrors Python's EffectFailed and Go's non-nil error.
 */
export class EffectFailed extends Error {
	constructor(message: string) {
		super(message);
		this.name = "EffectFailed";
	}
}

/**
 * Thrown when handler code extracts from or branches on an Unsettled carrier.
 * Internal (never re-exported): the framework catches it at the dispatch
 * boundary, prints the already-recorded would-do log, and truncates.
 *
 * TS ceiling: unlike Python's BaseException-derived twin, a handler's
 * `catch (e)` CAN swallow this. The dispatch sites therefore also consult the
 * effects handle's `truncated` record after the handler returns, so a
 * swallowed truncation still fails closed.
 */
export class DryRunTruncated extends Error {
	/**
	 * The three values §12.5's text is built from, kept apart from it so the
	 * envelope's preview_error can carry them as members (§19.3) without
	 * re-parsing the rendered message.
	 */
	readonly step: number;
	readonly cmdPath: string;
	readonly brand: string;

	constructor(message: string, step: number, cmdPath: string, brand: string) {
		super(message);
		this.name = "DryRunTruncated";
		this.step = step;
		this.cmdPath = cmdPath;
		this.brand = brand;
	}
}

/**
 * Go strconv.Quote: wrap in double quotes, escape backslash and double quote,
 * use the standard named escapes for ASCII control characters, and \xNN for
 * the rest of the control range. Code points above 0x7f pass through -- Go
 * would escape non-printable Unicode, but every %q slot in this catalog
 * receives ASCII identifiers (flag/check/command names, env vars, modes).
 */
function q(s: string): string {
	let out = '"';
	for (const ch of s) {
		const code = ch.codePointAt(0) as number;
		if (ch === '"' || ch === "\\") {
			out += `\\${ch}`;
		} else if (code >= 0x20 && code !== 0x7f) {
			out += ch;
		} else {
			switch (ch) {
				case "\x07":
					out += "\\a";
					break;
				case "\b":
					out += "\\b";
					break;
				case "\f":
					out += "\\f";
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
				case "\x0b":
					out += "\\v";
					break;
				default:
					out += `\\x${code.toString(16).padStart(2, "0")}`;
					break;
			}
		}
	}
	return `${out}"`;
}

// ---------------------------------------------------------------------------
// strictcli.go — type constructors (ListOf, DictOf)
// ---------------------------------------------------------------------------

export function errListOfBadItemType(itemType: number): string {
	return `ListOf: item type must be str, int, or float, got ${itemType}`;
}

export function errDictOfBadValueType(valueType: number): string {
	return `DictOf: value type must be str, int, or float, got ${valueType}`;
}

// ---------------------------------------------------------------------------
// strictcli.go — option constructors (WithConfigConflictMode, etc.)
// ---------------------------------------------------------------------------

export function errWithConfigConflictModeBadMode(mode: string): string {
	return `WithConfigConflictMode: mode must be "cli-wins" or "error", got ${q(mode)}`;
}

export function errHandshakeEnvVarEmptyHelp(envVar: string): string {
	return `handshake env var ${q(envVar)}: help must be a non-empty string`;
}

export function errDuplicateHandshakeEnvVar(envVar: string): string {
	return `duplicate handshake env var ${q(envVar)}`;
}

export function errConnectionEnvVarEmptyHelp(envVar: string): string {
	return `connection env var ${q(envVar)}: help must be a non-empty string`;
}

export function errConnectionEnvIsAlreadyInfraRoot(ev: string): string {
	return `connection env var ${q(ev)} is already declared as an infra root`;
}

export function errConnectionEnvIsAlreadyHandshake(ev: string): string {
	return `connection env var ${q(ev)} is already declared as a handshake env var`;
}

export function errConnectionURLFlagUnbound(flagName: string): string {
	return `flag ${q(flagName)}: connection-URL flag must bind to a declared connection env`;
}

export function errConnectionEnvWithoutURLFlag(flagName: string): string {
	return `flag ${q(flagName)}: connection env binding requires the flag to be marked as a connection-URL flag`;
}

export function errConnectionEnvWithPerFlagEnv(flagName: string): string {
	return `flag ${q(flagName)}: a connection-URL binding cannot be combined with a per-flag env var`;
}

export function errFlagConnectionEnvUndeclared(
	flagName: string,
	envVar: string,
): string {
	return `flag ${q(flagName)}: connection-URL flag binds to undeclared connection env ${q(envVar)}; declare it as a connection env`;
}

export function errConflictModeBadMode(mode: string): string {
	return `ConflictMode: mode must be "cli-wins" or "error", got ${q(mode)}`;
}

// ---------------------------------------------------------------------------
// strictcli.go — tag validation
// ---------------------------------------------------------------------------

export function errInvalidTagName(t: string): string {
	return `invalid tag name ${q(t)}: must match [a-z][a-z0-9-]*`;
}

// ---------------------------------------------------------------------------
// strictcli.go — NewArg validation
// ---------------------------------------------------------------------------

export function errArgHelpEmpty(): string {
	return "Arg.help must be a non-empty string";
}

export function errArgListTypeRequiresVariadic(name: string): string {
	return `Arg ${q(name)}: list type requires variadic=true`;
}

export function errArgListItemTypeBad(name: string): string {
	return `Arg ${q(name)}: list item type must be str, int, or float`;
}

export function errArgDictTypeNotSupported(name: string): string {
	return `Arg ${q(name)}: dict type is not supported on positional arguments`;
}

export function errArgTypeBad(t: number): string {
	return `Arg.type must be str, bool, int, or float, got ${t}`;
}

export function errArgChoicesIncompatibleBool(name: string): string {
	return `Arg ${q(name)}: choices is incompatible with type=bool`;
}

export function errArgChoicesEmpty(name: string): string {
	return `Arg ${q(name)}: choices must be a non-empty list`;
}

export function errArgChoiceTypeMismatch(
	name: string,
	c: string,
	typeName: string,
): string {
	return `Arg ${q(name)}: choice ${c} is not of type ${typeName}`;
}

// The three arg-side list-default templates the presence round left
// unreachable are deleted rather than retained: a list-typed arg must be
// variadic (errArgListTypeOnArgsRequiresVariadicTrue), and a variadic arg
// refuses any default (errArgVariadicDefault), so no arg can carry a list
// default for them to describe. Their flag-side counterparts stay: a list
// FLAG can declare a default, empty or not.

export function errArgStrDefaultTypeMismatch(
	name: string,
	gotType: string,
): string {
	return `Arg ${q(name)}: type=str requires a str default, got '${gotType}'`;
}

export function errArgIntDefaultTypeMismatch(
	name: string,
	gotType: string,
): string {
	return `Arg ${q(name)}: type=int requires an int default, got '${gotType}'`;
}

export function errArgFloatDefaultTypeMismatch(
	name: string,
	gotType: string,
): string {
	return `Arg ${q(name)}: type=float requires a float default, got '${gotType}'`;
}

export function errArgBoolDefaultTypeMismatch(
	name: string,
	gotType: string,
): string {
	return `Arg ${q(name)}: type=bool requires a bool default, got '${gotType}'`;
}

export function errArgDefaultNotInChoices(
	name: string,
	dflt: string,
	choicesStr: string,
): string {
	return `Arg ${q(name)}: default '${dflt}' is not in choices [${choicesStr}]`;
}

// ---------------------------------------------------------------------------
// strictcli.go — retired choices
//
// The value-level twin of the deprecated-command construct: a spelling a
// declaration used to accept, carrying the message that names its
// replacement. Every sentence names the CONCEPT ("retired choice") rather
// than a declaration spelling, so all three implementations share one
// signature and the parity checker needs no per-language exclusion.
//
// Values are interpolated pre-formatted through each implementation's own
// error-value formatter, which is what keeps an int, a float and a string
// rendering identically in all three.
// ---------------------------------------------------------------------------

export function errFlagRetiredChoiceIsLive(
	name: string,
	value: string,
): string {
	return `Flag ${q(name)}: retired choice '${value}' is also a live choice: a value is live or retired, never both`;
}

export function errArgRetiredChoiceIsLive(name: string, value: string): string {
	return `Arg ${q(name)}: retired choice '${value}' is also a live choice: a value is live or retired, never both`;
}

export function errFlagRetiredChoiceDuplicate(
	name: string,
	value: string,
): string {
	return `Flag ${q(name)}: retired choice '${value}' is declared twice`;
}

export function errArgRetiredChoiceDuplicate(
	name: string,
	value: string,
): string {
	return `Arg ${q(name)}: retired choice '${value}' is declared twice`;
}

export function errFlagRetiredChoiceMessageEmpty(
	name: string,
	value: string,
): string {
	return `Flag ${q(name)}: retired choice '${value}': message must be a non-empty string`;
}

export function errArgRetiredChoiceMessageEmpty(
	name: string,
	value: string,
): string {
	return `Arg ${q(name)}: retired choice '${value}': message must be a non-empty string`;
}

export function errFlagRetiredChoicesIncompatibleBool(name: string): string {
	return `Flag ${q(name)}: retired choices are incompatible with type=bool`;
}

export function errArgRetiredChoicesIncompatibleBool(name: string): string {
	return `Arg ${q(name)}: retired choices are incompatible with type=bool`;
}

export function errFlagDefaultIsRetiredChoice(
	name: string,
	value: string,
): string {
	return `Flag ${q(name)}: default '${value}' is a retired choice`;
}

export function errArgDefaultIsRetiredChoice(
	name: string,
	value: string,
): string {
	return `Arg ${q(name)}: default '${value}' is a retired choice`;
}

export function errFlagRetiredChoiceTypeMismatch(
	name: string,
	value: string,
	typeName: string,
): string {
	return `Flag ${q(name)}: retired choice '${value}' is not of type ${typeName}`;
}

export function errArgRetiredChoiceTypeMismatch(
	name: string,
	value: string,
	typeName: string,
): string {
	return `Arg ${q(name)}: retired choice '${value}' is not of type ${typeName}`;
}

export function errFlagRetiredChoicesRequireChoices(name: string): string {
	return `Flag ${q(name)}: retired choices require choices`;
}

export function errArgRetiredChoicesRequireChoices(name: string): string {
	return `Arg ${q(name)}: retired choices require choices`;
}

// ---------------------------------------------------------------------------
// strictcli.go — validateFlagConfig
// ---------------------------------------------------------------------------

export function errFlagHelpEmpty(): string {
	return "Flag.help must be a non-empty string";
}

export function errFlagForceReserved(): string {
	return "flag 'force' is a reserved name; use a qualified name like 'force-overwrite' or 'force-delete'";
}

export function errFlagNoPrefixReserved(name: string): string {
	return `flag '${name}': names starting with 'no-' are reserved for the negation system; use a positive name instead`;
}

export function errFlagRepeatableIncompatibleBool(name: string): string {
	return `Flag ${q(name)}: repeatable is incompatible with type=bool`;
}

// The compound-wide `choices is incompatible with compound types (list/dict)`
// template lived here with no caller: the dict branch raises
// errFlagDictCannotCombineChoices and the list branch ACCEPTS choices, because
// a list carrier with choices is exactly the declaration whose enum goes inside
// `items` (§25.5). One condition keeps one sentence, and the narrow one is the
// sentence, so the wide template is deleted rather than kept unreachable.

export function errFlagRepeatableRequiresExplicitUnique(name: string): string {
	return `Flag ${q(name)}: repeatable requires explicit unique (unique=True or unique=False)`;
}

export function errFlagUniqueRequiresRepeatable(name: string): string {
	return `Flag ${q(name)}: unique requires repeatable=True`;
}

export function errFlagEnvSeparatorRequiresRepeatable(name: string): string {
	return `Flag ${q(name)}: env_separator requires repeatable=True`;
}

export function errFlagEnvSeparatorRequiresEnv(name: string): string {
	return `Flag ${q(name)}: env_separator requires env`;
}

export function errFlagRepeatableEnvRequiresSeparator(name: string): string {
	return `Flag ${q(name)}: repeatable flag with env requires env_separator`;
}

export function errFlagEnvSeparatorSingleChar(name: string): string {
	return `Flag ${q(name)}: env_separator must be a single character`;
}

export function errFlagEnvSeparatorBackslash(name: string): string {
	return `Flag ${q(name)}: env_separator cannot be a backslash`;
}

export function errFlagChoicesIncompatibleBool(name: string): string {
	return `Flag ${q(name)}: choices is incompatible with type=bool`;
}

export function errFlagChoicesEmpty(name: string): string {
	return `Flag ${q(name)}: choices must be a non-empty list`;
}

export function errFlagChoiceTypeMismatch(
	name: string,
	c: string,
	typeName: string,
): string {
	return `Flag ${q(name)}: choice ${c} is not of type ${typeName}`;
}

export function errFlagIntDefaultTypeMismatch(
	name: string,
	gotType: string,
): string {
	return `Flag ${q(name)}: type=int requires an int default, got '${gotType}'`;
}

export function errFlagFloatDefaultTypeMismatch(
	name: string,
	gotType: string,
): string {
	return `Flag ${q(name)}: type=float requires a float default, got '${gotType}'`;
}

export function errFlagDictDefaultMustBeMap(name: string): string {
	return `Flag ${q(name)}: dict flag default must be a map[string]interface{}`;
}

export function errFlagDefaultValueForKey(
	name: string,
	k: string,
	errStr: string,
): string {
	return `Flag ${q(name)}: default value for key ${q(k)}: ${errStr}`;
}

export function errFlagListDefaultMustBeSlice(name: string): string {
	return `Flag ${q(name)}: list flag default must be a []interface{}`;
}

export function errFlagDefaultElementError(
	name: string,
	i: number,
	errStr: string,
): string {
	return `Flag ${q(name)}: default element ${i}: ${errStr}`;
}

export function errFlagRepeatableDefaultMustBeList(name: string): string {
	return `Flag ${q(name)}: repeatable flag default must be a list`;
}

export function errFlagDefaultElementTypeMismatch(
	name: string,
	i: number,
	typeName: string,
): string {
	return `Flag ${q(name)}: default element ${i} is not of type ${typeName}`;
}

export function errFlagDefaultNotInChoices(
	name: string,
	dflt: string,
	choicesStr: string,
): string {
	return `Flag ${q(name)}: default '${dflt}' is not in choices [${choicesStr}]`;
}

// ---------------------------------------------------------------------------
// factories.ts — Python-wording registration templates
//
// Python is the divergence ground truth for these registration errors; the
// TS factories emit Python's wording (with Go-style double-quoted names).
// Go's counterparts use different wording or typed constructors -- see the
// go exclusions for these signatures in check_error_parity.py.
// ---------------------------------------------------------------------------

export function errFlagDictCannotCombineRepeatable(name: string): string {
	return `Flag "${name}": dict type cannot be combined with repeatable=True`;
}

export function errFlagDictCannotCombineUnique(name: string): string {
	return `Flag "${name}": dict type cannot be combined with unique`;
}

export function errFlagDictCannotCombineChoices(name: string): string {
	return `Flag "${name}": dict type cannot be combined with choices`;
}

export function errFlagConflictModeBad(name: string, gotRepr: string): string {
	return `Flag "${name}": conflict_mode must be "cli-wins" or "error", got ${gotRepr}`;
}

export function errFlagDictCannotUseEnvSeparator(name: string): string {
	return `Flag "${name}": dict type cannot use env_separator (env vars are parsed as JSON)`;
}

export function errFlagDictDefaultKeyMustBeString(
	name: string,
	keyRepr: string,
): string {
	return `Flag "${name}": dict default key ${keyRepr} must be a string`;
}

export function errArgDictTypeNotSupportedOnArgs(name: string): string {
	return `Arg "${name}": dict type is not supported on args`;
}

export function errArgListTypeOnArgsRequiresVariadicTrue(name: string): string {
	return `Arg "${name}": list type on args requires variadic=True`;
}

export function errCommandImpliesValueMustBeBool(
	cmdName: string,
	typeName: string,
): string {
	return `command "${cmdName}": Implies value must be a bool, got '${typeName}'`;
}

// ---------------------------------------------------------------------------
// strictcli.go — NewApp
// ---------------------------------------------------------------------------

export function errAppHelpEmpty(): string {
	return "App.help must be a non-empty string";
}

export function errDuplicateInfraRootEnvVar(envVar: string): string {
	return `duplicate infra root env var ${q(envVar)}`;
}

export function errHandshakeIsAlreadyInfraRoot(ev: string): string {
	return `handshake env var ${q(ev)} is already declared as an infra root`;
}

export function errCannotUseBothChecksAndEmbed(): string {
	return "cannot use both WithChecks and WithChecksEmbed";
}

export function errChecksPathNotExist(path: string): string {
	return `checks_path does not exist: ${path}`;
}

export function errChecksTomlAppMismatch(
	appName: string,
	expected: string,
): string {
	return `checks.toml: app ${q(appName)} does not match app name ${q(expected)}`;
}

/**
 * The retired boolean option's refusal (contract §12.12: one sentence, each
 * language's own spellings inside it -- `testCoverage` / `testCoverageDir`
 * here, `test_coverage` / `test_coverage_dir` in Python, `WithTestCoverage` /
 * `WithTestCoverageDir` in Go).
 */
export function errTestCoverageBooleanRetired(): string {
	return "testCoverage is not accepted; declare the directory holding coverage/ and test-coverage.json with testCoverageDir";
}

// ---------------------------------------------------------------------------
// strictcli.go — check registration
// ---------------------------------------------------------------------------

export function errCannotRegisterCheckNotEnabled(name: string): string {
	return `cannot register check ${q(name)}: checks not enabled`;
}

export function errCannotRegisterCheckNotDeclared(name: string): string {
	return `cannot register check ${q(name)}: not declared in checks.toml`;
}

export function errCheckDuplicateRegistration(name: string): string {
	return `check ${q(name)}: duplicate registration`;
}

export function errCheckSeverityMismatch(
	name: string,
	severity: string,
	used: string,
	want: string,
): string {
	return `check ${q(name)}: declared severity ${q(severity)} in checks.toml but registered via ${used}; use ${want}`;
}

// ---------------------------------------------------------------------------
// strictcli.go — TagContract
// ---------------------------------------------------------------------------

// errInvalidTagName is reused from the tag validation section above.

export function errTagContractViolation(
	cmdName: string,
	tag: string,
	requiredFlag: string,
): string {
	return `command ${q(cmdName)}: tag ${q(tag)} requires flag "--${requiredFlag}"`;
}

// ---------------------------------------------------------------------------
// strictcli.go — validateConfigFieldBindings
// ---------------------------------------------------------------------------

export function errCommandConfigFieldsUnknownField(
	cmdName: string,
	field: string,
): string {
	return `command ${q(cmdName)}: config_fields references unknown config field ${q(field)}`;
}

// ---------------------------------------------------------------------------
// strictcli.go — checkFlagConfigFieldDefault
// ---------------------------------------------------------------------------

export function errConfigFieldFlagDefaultDisagree(
	cfName: string,
	flagName: string,
	cfDefault: string,
	flagDefault: string,
): string {
	return `config field ${q(cfName)} collides with flag ${q(flagName)} but their defaults disagree (${cfDefault} vs ${flagDefault}); remove one default or make them equal`;
}

// ---------------------------------------------------------------------------
// strictcli.go — resolveInfraRootPath
// ---------------------------------------------------------------------------

export function errRelativeToRootUndeclared(envVar: string): string {
	return `RelativeToRoot references undeclared infra root ${q(envVar)}; declare it as an infra root`;
}

// ---------------------------------------------------------------------------
// strictcli.go — validateFlagInfraMarker
// ---------------------------------------------------------------------------

export function errFlagRelativeToRootUndeclared(
	flagName: string,
	envVar: string,
): string {
	return `flag ${q(flagName)}: RelativeToRoot references undeclared infra root ${q(envVar)}; declare it as an infra root`;
}

// The command-scoped variant mirrors Python's _build_and_validate_command
// message (the divergence ground truth). Go has no command-context marker
// validation -- it validates per-flag -- so this template has no errors.go
// counterpart (see check_error_parity.py, "InfraEnv structural" exclusions).
export function errCommandFlagRelativeToRootUndeclared(
	cmdName: string,
	flagName: string,
	envVar: string,
): string {
	return `command ${q(cmdName)}: flag ${q(flagName)}: RelativeToRoot references undeclared infra root ${q(envVar)}; declare it as an infra root`;
}

// ---------------------------------------------------------------------------
// strictcli.go — command registration
// ---------------------------------------------------------------------------

export function errCommandMissingHelp(name: string): string {
	return `command ${q(name)}: missing help text`;
}

export function errCommandPassthroughCannotHave(
	name: string,
	parts: string,
): string {
	return `command ${q(name)}: passthrough commands cannot have ${parts}`;
}

export function errGlobalFlagNameReserved(name: string): string {
	return `global flag name ${q(name)} is reserved`;
}

export function errGlobalShortFlagReserved(short: string): string {
	return `global short flag ${q(short)} is reserved`;
}

export function errDuplicateGlobalFlag(name: string): string {
	return `duplicate global flag name ${q(name)}`;
}

export function errGroupHelpEmpty(): string {
	return "Group.help must be a non-empty string";
}

export function errGroupCollidesWithCommand(name: string): string {
	return `group ${q(name)} collides with an existing command`;
}

export function errGroupAlreadyRegistered(name: string): string {
	return `group ${q(name)} is already registered`;
}

export function errCommandCollidesWithGroup(name: string): string {
	return `command ${q(name)} collides with an existing group`;
}

export function errDeprecatedNameEmpty(): string {
	return "deprecated command name must be a non-empty string";
}

export function errDeprecatedMessageEmpty(name: string): string {
	return `deprecated command ${q(name)}: message must not be empty`;
}

export function errDeprecatedCollidesCommand(name: string): string {
	return `deprecated command ${q(name)} collides with an existing command`;
}

export function errDeprecatedCollidesGroup(name: string): string {
	return `deprecated command ${q(name)} collides with an existing group`;
}

export function errDeprecatedAlreadyRegistered(name: string): string {
	return `deprecated command ${q(name)} is already registered`;
}

// ---------------------------------------------------------------------------
// strictcli.go — buildAndValidateCommand
// ---------------------------------------------------------------------------

// The two mutex-group registration templates that lived here are DELETED with
// the construct (§21's supersession box): `MutexGroup` is removed from all
// three implementations, so "mutex group must have at least 2 flags" and
// "appears in multiple mutex groups" describe declarations that can no longer
// be written. Their replacements are the selector's own guards (§12.13's
// `errSelectorNoChoices` and the co-electable name-reuse family).

export function errCommandFlagCollidesGlobal(
	name: string,
	flagName: string,
): string {
	return `command ${q(name)}: flag ${q(flagName)} collides with a global flag`;
}

export function errCommandDuplicateFlag(
	name: string,
	flagName: string,
): string {
	return `command ${q(name)}: duplicate flag name ${q(flagName)}`;
}

export function errCommandDuplicateArg(name: string, argName: string): string {
	return `command ${q(name)}: duplicate arg name ${q(argName)}`;
}

export function errCommandAtMostOneVariadic(name: string): string {
	return `command ${q(name)}: at most one variadic arg is allowed`;
}

export function errCommandVariadicMustBeLast(
	name: string,
	argName: string,
): string {
	return `command ${q(name)}: variadic arg ${q(argName)} must be the last arg`;
}

export function errCommandFlagMissingHelp(
	name: string,
	flagName: string,
): string {
	return `command ${q(name)}: flag ${q(flagName)} missing help text`;
}

export function errCommandEnvVarPrefix(
	name: string,
	envVar: string,
	flagName: string,
	expectedPrefix: string,
): string {
	return `command ${q(name)}: env var ${q(envVar)} for flag ${q(flagName)} must start with ${q(expectedPrefix)} (or set prefixed=false)`;
}

export function errCommandRequiresSameFlag(name: string, flag: string): string {
	return `command ${q(name)}: Requires flag and depends_on cannot be the same (${q(flag)})`;
}

export function errCommandImpliesSameFlag(name: string, flag: string): string {
	return `command ${q(name)}: Implies flag and implies cannot be the same (${q(flag)})`;
}

export function errCommandImpliesTriggerNotBool(
	name: string,
	flagName: string,
): string {
	return `command ${q(name)}: Implies trigger flag ${q(flagName)} must be a bool flag`;
}

export function errCommandImpliesTargetNotBool(
	name: string,
	flagName: string,
): string {
	return `command ${q(name)}: Implies target flag ${q(flagName)} must be a bool flag`;
}

// ---------------------------------------------------------------------------
// strictcli.go — validateScalarType (parse-time)
// ---------------------------------------------------------------------------

export function errExpectedStrGot(typeDesc: string): string {
	return `expected str, got ${typeDesc}`;
}

export function errExpectedIntGot(typeDesc: string): string {
	return `expected int, got ${typeDesc}`;
}

export function errExpectedFloatGot(typeDesc: string): string {
	return `expected float, got ${typeDesc}`;
}

export function errExpectedBoolGot(typeDesc: string): string {
	return `expected bool, got ${typeDesc}`;
}

// ---------------------------------------------------------------------------
// strictcli.go — doParse hermetic mode (parse-time)
// ---------------------------------------------------------------------------

export function errHermeticConfigMutuallyExclusive(): string {
	return "--hermetic and --config are mutually exclusive";
}

export function errHermeticWithConfigCommands(): string {
	return "--hermetic cannot be used with config commands";
}

// ---------------------------------------------------------------------------
// parse.go — strict parsing
// ---------------------------------------------------------------------------

export function errExpectedBoolean(s: string): string {
	return `expected boolean, got '${s}'`;
}

export function errExpectedInteger(s: string): string {
	return `expected integer, got '${s}'`;
}

export function errExpectedFloat(s: string): string {
	return `expected float, got '${s}'`;
}

export function errNaNNotAllowed(): string {
	return "NaN is not allowed";
}

export function errInfNotAllowed(): string {
	return "Inf is not allowed";
}

// ---------------------------------------------------------------------------
// parse.go — resolveAtPrefix @-prefix resolution (parse-time)
// ---------------------------------------------------------------------------

export function errAtPrefixStdinOnce(flagName: string): string {
	return `--${flagName}: stdin (@-) can only be used once per invocation`;
}

export function errAtPrefixCannotReadStdin(flagName: string): string {
	return `--${flagName}: cannot read stdin`;
}

export function errAtPrefixFileTooLarge(flagName: string): string {
	return `--${flagName}: file exceeds 1 MB limit`;
}

export function errAtPrefixFileNotFound(
	flagName: string,
	path: string,
): string {
	return `--${flagName}: file not found: ${path}`;
}

export function errAtPrefixCannotReadFile(
	flagName: string,
	path: string,
): string {
	return `--${flagName}: cannot read file: ${path}`;
}

// ---------------------------------------------------------------------------
// parse.go / strictcli.go — flag token parsing (parse-time)
// (parseCommand and extractGlobalFlags share these templates)
// ---------------------------------------------------------------------------

export function errBoolFlagNoValue(flagPart: string): string {
	return `flag '${flagPart}' is a boolean flag and does not take a value`;
}

export function errBoolNegationNoValue(flagPart: string): string {
	return `flag '${flagPart}' is a boolean negation and does not take a value`;
}

export function errUnknownFlag(tok: string): string {
	return `unknown flag '${tok}'`;
}

export function errFlagRequiresValue(tok: string): string {
	return `flag '${tok}' requires a value`;
}

export function errFlagDuplicateValue(flagName: string, value: string): string {
	return `--${flagName}: duplicate value '${value}'`;
}

// ---------------------------------------------------------------------------
// parse.go / strictcli.go — env var resolution (parse-time)
// (parseCommand and extractGlobalFlags share these templates)
// ---------------------------------------------------------------------------

export function errWrappedFromEnvVar(errStr: string, envVar: string): string {
	return `${errStr} (from env var '${envVar}')`;
}

export function errListFlagEnvRequiresSeparator(flagName: string): string {
	return `--${flagName}: list flag with env requires env_separator`;
}

export function errFlagDuplicateValueFromEnv(
	flagName: string,
	value: string,
	envVar: string,
): string {
	return `--${flagName}: duplicate value '${value}' (from env var '${envVar}')`;
}

export function errInvalidBoolEnvValue(
	envVal: string,
	envVar: string,
	flagName: string,
): string {
	return `invalid boolean value '${envVal}' for env var '${envVar}' (flag '--${flagName}')`;
}

export function errFlagErrFromEnvVar(
	flagName: string,
	errStr: string,
	envVar: string,
): string {
	return `--${flagName}: ${errStr} (from env var '${envVar}')`;
}

// ---------------------------------------------------------------------------
// parse.go / strictcli.go — config value resolution (parse-time)
// (parseCommand and extractGlobalFlags share these templates)
// ---------------------------------------------------------------------------

export function errConfigValueError(flagName: string, errStr: string): string {
	return `--${flagName}: config value error: ${errStr}`;
}

export function errFlagSetInBothAndConfig(
	flagName: string,
	existingSource: string,
): string {
	return `flag '${flagName}' set in both ${existingSource} and config; remove one`;
}

export function errConfigValueDuplicate(
	flagName: string,
	value: string,
): string {
	return `--${flagName}: config value error: duplicate value '${value}'`;
}

export function errFlagSetInBothCliAndConfig(flagName: string): string {
	return `flag '${flagName}' set in both cli and config; remove one`;
}

// ---------------------------------------------------------------------------
// parse.go — validateAndBuildKwargs (parse-time)
// (constraints, custom validation, positional args)
// ---------------------------------------------------------------------------

export function errMutuallyExclusive(setFlags: string): string {
	return `${setFlags} are mutually exclusive`;
}

export function errOneOfRequired(names: string, clause: string): string {
	return `one of ${names} is required${clause}`;
}

export function errMutexRedundantNegation(
	declined: string,
	elected: string,
	clause: string,
): string {
	return `${declined} cannot be combined with --${elected}${clause}`;
}

export function errMutexDeclineClause(name: string): string {
	return ` (--no-${name} declines an option; it does not choose one)`;
}

/**
 * The prefix every constraint sentence carries, parse-time and
 * registration-time alike (§12.15). The name takes DOUBLE quotes because the
 * catalog quotes by kind of thing rather than by category of message: a
 * declared identifier is double-quoted (`command "<name>"`, `Flag "<name>"`)
 * and a token the operator typed is single-quoted -- and a constraint name is
 * a declared identifier no invocation ever contains.
 */
export function constraintPrefix(c: string): string {
	return `constraint ${q(c)}: `;
}

export function errImpliesConflict(
	c: string,
	flag: string,
	neg: string,
	target: string,
	explicitNeg: string,
): string {
	return `${constraintPrefix(c)}flag '--${flag}' implies '--${neg}${target}', but '--${explicitNeg}${target}' was explicitly provided`;
}

/**
 * at-least-one's violation (§12.15). `<members>` is the whole member list by
 * §12.15's rendering rule, in declaration order; `<clause>` is §21.4's decline
 * clause verbatim, appended when a bool member declaring `when: "true"` was
 * provided false.
 *
 * This family is NEVER exclusivity: it has no upper bound and never refuses a
 * second member (§26.1). The shared clause is a fact about a negated bool, not
 * a claim about the construct.
 */
export function errAtLeastOneRequired(
	c: string,
	members: string,
	clause: string,
): string {
	return `${constraintPrefix(c)}at least one of ${members} is required${clause}`;
}

/**
 * all-or-none's violation (§12.15). Every member is listed, engaged or not,
 * which is the shipped `flags --a, --b must be used together` behaviour
 * carried over; the noun `flags` is dropped because a member may be a
 * positional arg or a nested constraint.
 */
export function errAllOrNoneTogether(c: string, members: string): string {
	return `${constraintPrefix(c)}${members} must be used together`;
}

export function errFlagRequiresFlag(
	c: string,
	flag: string,
	dependsOn: string,
): string {
	return `${constraintPrefix(c)}flag '--${flag}' requires '--${dependsOn}'`;
}

export function errFlagValueError(flagName: string, msg: string): string {
	return `--${flagName}: ${msg}`;
}

export function errMissingRequiredArgument(name: string): string {
	return `missing required argument '${name}'`;
}

export function errUnexpectedArgument(value: string): string {
	return `unexpected argument '${value}'`;
}

// ---------------------------------------------------------------------------
// parse.go — validateChoices (parse-time)
// ---------------------------------------------------------------------------

export function errArgInvalidChoice(
	name: string,
	value: string,
	choices: string,
): string {
	return `argument '${name}': invalid value '${value}', must be one of: ${choices}`;
}

export function errFlagInvalidChoice(
	name: string,
	value: string,
	choices: string,
): string {
	return `--${name}: invalid value '${value}', must be one of: ${choices}`;
}

// The retired-choice refusal, checked before the invalid-value one so a reader
// who typed a spelling that used to work is told what replaced it rather than
// being handed the list it is missing from. The command-level twin is
// "command '<name>' is deprecated: <message>".

export function errArgRetiredChoice(
	name: string,
	value: string,
	message: string,
): string {
	return `argument '${name}': value '${value}' retired: ${message}`;
}

export function errFlagRetiredChoice(
	name: string,
	value: string,
	message: string,
): string {
	return `--${name}: value '${value}' retired: ${message}`;
}

// ---------------------------------------------------------------------------
// values.ts — typed value parsing (parse-time)
//
// Python-wording positional-arg error wrappers (Python's generic
// "argument '<name>': ..." prefix); Go produces typed errors at the parse
// level with a different prefix -- see the go exclusions in
// check_error_parity.py.
// ---------------------------------------------------------------------------

export function errArgumentWrapped(argName: string, msg: string): string {
	return `argument '${argName}': ${msg}`;
}

export function errArgumentExpectedFloat(argName: string, raw: string): string {
	return `argument '${argName}': expected float, got '${raw}'`;
}

// ---------------------------------------------------------------------------
// values.ts — dict flag parsing (parse-time)
//
// Python-wording templates (Python is the divergence ground truth for dict
// flag parsing; Go coerces dict values with different inline messages in
// parse.go -- check_error_parity.py carries the go exclusions for these
// signatures). Type-description slots take pre-formatted names from the
// jsonConfigTypename / jsonNativeTypename vocabularies in values.ts.
// ---------------------------------------------------------------------------

export function errDictJsonValueForKeyMustBeString(
	flagName: string,
	key: string,
	typeDesc: string,
): string {
	return `--${flagName}: JSON value for key '${key}' must be a string, got ${typeDesc}`;
}

export function errDictJsonValueForKeyMustBeInteger(
	flagName: string,
	key: string,
	typeDesc: string,
): string {
	return `--${flagName}: JSON value for key '${key}' must be an integer, got ${typeDesc}`;
}

export function errDictJsonValueForKeyMustBeNumber(
	flagName: string,
	key: string,
	typeDesc: string,
): string {
	return `--${flagName}: JSON value for key '${key}' must be a number, got ${typeDesc}`;
}

export function errDictUnsupportedValueType(
	flagName: string,
	valueSchema: string,
): string {
	return `--${flagName}: unsupported value type ${valueSchema}`;
}

export function errDictInvalidJson(flagName: string, errStr: string): string {
	return `--${flagName}: invalid JSON: ${errStr}`;
}

export function errDictJsonValueMustBeObject(
	flagName: string,
	typeDesc: string,
): string {
	return `--${flagName}: JSON value must be an object, got ${typeDesc}`;
}

export function errDictExpectedKeyValueOrJson(
	flagName: string,
	raw: string,
): string {
	return `--${flagName}: expected key=value or JSON, got '${raw}'`;
}

export function errDictEmptyKey(flagName: string, raw: string): string {
	return `--${flagName}: empty key in '${raw}'`;
}

export function errDictValueForKey(
	flagName: string,
	key: string,
	errStr: string,
): string {
	return `--${flagName}: value for key '${key}': ${errStr}`;
}

export function errDictDuplicateKey(flagName: string, key: string): string {
	return `--${flagName}: duplicate key '${key}'`;
}

export function errDictInvalidJsonInEnvVar(
	flagName: string,
	envVar: string,
	errStr: string,
): string {
	return `--${flagName}: invalid JSON in env var '${envVar}': ${errStr}`;
}

export function errDictEnvVarMustBeJsonObject(
	flagName: string,
	envVar: string,
	typeDesc: string,
): string {
	return `--${flagName}: env var '${envVar}' must be a JSON object, got ${typeDesc}`;
}

// ---------------------------------------------------------------------------
// config.go — ConfigField registration
// ---------------------------------------------------------------------------

export function errConfigFieldNameInvalid(name: string): string {
	return `ConfigField name ${q(name)} is invalid: must match [a-z][a-z0-9_]*(.[a-z][a-z0-9_]*)* (lowercase, dots for sections)`;
}

export function errConfigFieldNameReserved(name: string): string {
	return `config field name ${q(name)} is reserved: names starting with underscore are reserved for framework fields`;
}

export function errConfigFieldHelpRequired(name: string): string {
	return `config field ${q(name)}: help text is required`;
}

export function errConfigFieldTypeBad(t: number | string): string {
	return `ConfigField.type must be str, bool, int, or float, got ${t}`;
}

export function errDuplicateConfigField(name: string): string {
	return `duplicate config field name ${q(name)}`;
}

export function errConfigFieldConflictsFramework(name: string): string {
	return `config field name ${q(name)} conflicts with framework field`;
}

// ---------------------------------------------------------------------------
// config.go — framework field registration
// ---------------------------------------------------------------------------

export function errFrameworkFieldMustStartUnderscore(name: string): string {
	return `framework field name ${q(name)} must start with underscore`;
}

export function errFrameworkFieldNameInvalid(name: string): string {
	return `framework field ${q(name)}: invalid name, must match [a-z][a-z0-9_]*(.[a-z][a-z0-9_]*)* (lowercase, dots for sections)`;
}

export function errFrameworkFieldHelpRequired(name: string): string {
	return `framework field ${q(name)}: help text is required`;
}

export function errDuplicateFrameworkField(name: string): string {
	return `duplicate framework field name ${q(name)}`;
}

export function errFrameworkFieldConflictsUser(name: string): string {
	return `framework field name ${q(name)} conflicts with user config field`;
}

// ---------------------------------------------------------------------------
// config.go — validateConfigFieldDefault
// ---------------------------------------------------------------------------

export function errConfigFieldDefaultMismatch(
	name: string,
	value: string,
	typeName: string,
): string {
	return `ConfigField ${q(name)}: default value ${value} does not match type ${typeName}`;
}

// ---------------------------------------------------------------------------
// config.go — coerceConfigScalarLong (long type names)
// (the float branch reuses errExpectedFloatGot from the validateScalarType
// section above)
// ---------------------------------------------------------------------------

export function errConfigExpectedBooleanGot(typeDesc: string): string {
	return `expected boolean, got ${typeDesc}`;
}

export function errConfigExpectedIntegerGotFloat(): string {
	return "expected integer, got float";
}

export function errConfigExpectedIntegerGot(typeDesc: string): string {
	return `expected integer, got ${typeDesc}`;
}

export function errConfigExpectedStringGot(typeDesc: string): string {
	return `expected string, got ${typeDesc}`;
}

export function errConfigUnsupportedFlagType(t: number): string {
	return `unsupported flag type ${t}`;
}

// ---------------------------------------------------------------------------
// config.go — coerceConfigScalarShort (short type names)
// (the bool/int/float/str branches reuse errExpectedBoolGot, errExpectedIntGot,
// errExpectedFloatGot, and errExpectedStrGot from the validateScalarType
// section above; the unsupported-type branch reuses errConfigUnsupportedFlagType)
// ---------------------------------------------------------------------------

export function errConfigExpectedIntGotFloat(): string {
	return "expected int, got float";
}

// ---------------------------------------------------------------------------
// config.go — coerceConfigValue (compound config value coercion)
// ---------------------------------------------------------------------------

export function errConfigExpectedObjectForDictFlag(typeDesc: string): string {
	return `expected object for dict flag, got ${typeDesc}`;
}

export function errConfigDictKeyTypeMismatch(
	k: string,
	wantType: string,
	gotType: string,
): string {
	// Divergence: Go spells the key %q (double quotes), Python '{k}' (single).
	// Python is the divergence ground truth.
	return `key '${k}': expected ${wantType}, got ${gotType}`;
}

export function errConfigExpectedArrayForListFlag(typeDesc: string): string {
	return `expected array for list flag, got ${typeDesc}`;
}

export function errConfigElementTypeMismatch(
	i: number,
	wantType: string,
	gotType: string,
): string {
	return `element ${i}: expected ${wantType}, got ${gotType}`;
}

export function errConfigExpectedScalarGotArray(): string {
	return "expected scalar, got array";
}

export function errConfigExpectedArrayForRepeatableFlag(
	typeDesc: string,
): string {
	return `expected array for repeatable flag, got ${typeDesc}`;
}

// ---------------------------------------------------------------------------
// routing.go — resolveCommand (parse-time)
// ---------------------------------------------------------------------------

export function errCommandDeprecated(token: string, msg: string): string {
	return `command '${token}' is deprecated: ${msg}`;
}

export function errUnknownCommandInGroup(
	token: string,
	groupPath: string,
): string {
	return `unknown command '${token}' in '${groupPath}'`;
}

export function errUnknownCommand(token: string): string {
	return `unknown command '${token}'`;
}

export function errNoCommandSpecified(): string {
	return "no command specified";
}

// ---------------------------------------------------------------------------
// invoke.go — invoke (parse-time)
// ---------------------------------------------------------------------------

export function errPassthroughArgsNotStringSlice(): string {
	return "passthrough command: _args must be []string";
}

export function errUnknownParameterForPassthroughCommand(
	key: string,
	commandPath: string,
): string {
	return `unknown parameter ${q(key)} for passthrough command ${q(commandPath)}`;
}

export function errUnknownParameterForCommand(
	paramName: string,
	commandPath: string,
): string {
	return `unknown parameter ${q(paramName)} for command ${q(commandPath)}`;
}

/**
 * Python divergence (the ground truth for call()): a path that resolves to a
 * group raises "'path' is a group, not a command" (Go says "no command
 * resolved from path: <path>"; no conformance case distinguishes them).
 */
export function errCallPathIsGroup(commandPath: string): string {
	return `'${commandPath}' is a group, not a command`;
}

// ---------------------------------------------------------------------------
// invoke.go — coerceInvokeDict
// ---------------------------------------------------------------------------

export function errDictFlagExpectedMapType(
	name: string,
	gotType: string,
): string {
	return `dict flag ${q(name)}: expected map type, got ${gotType}`;
}

// ---------------------------------------------------------------------------
// check.go — reporter methods
// ---------------------------------------------------------------------------

export function errNoteTextEmpty(): string {
	return "note text must be a non-empty string";
}

export function errProblemTextEmpty(): string {
	return "problem text must be a non-empty string";
}

export function errOutcomeMessageEmpty(): string {
	return "outcome message must be a non-empty string";
}

export function errPassedWithProblems(): string {
	return "problems were reported; a check that found problems cannot pass -- use found instead";
}

export function errSkipReasonEmpty(): string {
	return "skip reason must be a non-empty string";
}

export function errSkippedWithProblems(): string {
	return "problems were reported; a check that found problems cannot skip";
}

export function errFoundNoProblems(): string {
	return "no problems were reported; nothing found means pass -- use passed instead";
}

// ---------------------------------------------------------------------------
// checks/framework.ts — outcome mint guard (Python _CheckOutcome.__post_init__)
//
// Go seals CheckOutcome structurally (unexported fields); Python guards the
// constructor with a module-private mint token and raises TypeError. TS uses
// the token approach, so it needs Python's guard message (with the class name
// unprefixed, matching the public TS surface).
// ---------------------------------------------------------------------------

export function errCheckOutcomeDirectConstruction(): string {
	return "CheckOutcome cannot be constructed directly; obtain one from a reporter (passed/skipped/found)";
}

// ---------------------------------------------------------------------------
// check.go — deriveStatus
// ---------------------------------------------------------------------------

export function errUnknownCheckOutcomeKind(kind: string): string {
	return `unknown check outcome kind ${q(kind)}`;
}

// ---------------------------------------------------------------------------
// check.go — addCheckDef
// ---------------------------------------------------------------------------

export function errDuplicateCheckDef(name: string): string {
	return `duplicate check definition ${q(name)}`;
}

// ---------------------------------------------------------------------------
// effects_bypass.go — the effects-bypass check's input rule
//
// A release-blocking check reads only inputs the repository owns, so the lint
// enumerates its files through git and refuses a project root that is not
// inside a work tree. There is no filesystem-walk fallback and no flag that
// turns the rule off: a root git cannot answer for is a verdict the check
// declines to give.
// ---------------------------------------------------------------------------

export function errEffectsBypassNotAWorkTree(root: string): string {
	return `effects-bypass: project root '${root}' is not a git work tree; the check reads only repository-owned files`;
}

// ---------------------------------------------------------------------------
// check.go — parseChecksToml
// ---------------------------------------------------------------------------

export function errChecksTomlParse(errStr: string): string {
	return `checks.toml: ${errStr}`;
}

export function errChecksTomlUnknownTopLevelKey(key: string): string {
	return `checks.toml: unknown top-level key ${q(key)}`;
}

export function errChecksTomlMissingApp(): string {
	return 'checks.toml: missing required top-level key "app"';
}

export function errChecksTomlAppNotString(): string {
	return 'checks.toml: "app" must be a non-empty string';
}

export function errChecksTomlChecksMustBeTable(): string {
	return "checks.toml: [checks] must be a table";
}

export function errChecksTomlInvalidCheckName(name: string): string {
	return `checks.toml: invalid check name ${q(name)} (must match [a-z][a-z0-9-]*)`;
}

export function errChecksTomlCheckMustBeTable(name: string): string {
	return `checks.toml: check ${q(name)} must be a table`;
}

export function errChecksTomlUnknownField(name: string, field: string): string {
	return `checks.toml: check ${q(name)}: unknown field ${q(field)}`;
}

export function errChecksTomlMissingField(name: string, field: string): string {
	return `checks.toml: check ${q(name)}: missing required field ${q(field)}`;
}

export function errChecksTomlTagsMustBeStrings(name: string): string {
	return `checks.toml: check ${q(name)}: "tags" must be a list of strings`;
}

export function errChecksTomlTagsEntriesMustBeStrings(name: string): string {
	return `checks.toml: check ${q(name)}: "tags" entries must be non-empty strings`;
}

export function errChecksTomlSeverityInvalid(
	name: string,
	raw: string,
): string {
	return `checks.toml: check ${q(name)}: "severity" must be "error" or "warn", got ${q(raw)}`;
}

export function errChecksTomlBoolFieldInvalid(
	name: string,
	field: string,
	typeDesc: string,
): string {
	return `checks.toml: check ${q(name)}: ${q(field)} must be a boolean, got ${typeDesc}`;
}

export function errChecksTomlDependsOnMustBeStrings(name: string): string {
	return `checks.toml: check ${q(name)}: "depends_on" must be a list of strings`;
}

export function errChecksTomlDependsOnEntriesMustBeStrings(
	name: string,
): string {
	return `checks.toml: check ${q(name)}: "depends_on" entries must be strings`;
}

export function errChecksTomlScopeMustBeString(
	name: string,
	typeDesc: string,
): string {
	return `checks.toml: check ${q(name)}: "scope" must be a string, got ${typeDesc}`;
}

export function errChecksTomlDependsOnUnknown(
	name: string,
	dep: string,
): string {
	return `checks.toml: check ${q(name)}: depends_on references unknown check ${q(dep)}`;
}

// ---------------------------------------------------------------------------
// check_runner.go
// ---------------------------------------------------------------------------

export function errCheckDependencyCycleInvolving(name: string): string {
	return `check dependency cycle detected involving ${q(name)}`;
}

export function errCheckDependencyCycle(cyclePath: string): string {
	return `check dependency cycle: ${cyclePath}`;
}

export function errCheckDependencyCycleDetected(): string {
	return "check dependency cycle detected";
}

export function errCheckOutcomeNotMinted(name: string): string {
	return `check ${q(name)} returned an outcome not minted by its reporter; use reporter methods (Passed/Skipped/Found)`;
}

export function errInvalidGlobPattern(pattern: string, errStr: string): string {
	return `invalid glob pattern ${q(pattern)}: ${errStr}`;
}

// ---------------------------------------------------------------------------
// check_provider.go
// ---------------------------------------------------------------------------

export function errCheckProviderSeverityMismatch(
	name: string,
	severity: string,
	used: string,
	want: string,
): string {
	return `check ${q(name)}: declared severity ${q(severity)} but registered via ${used}; use ${want}`;
}

// ---------------------------------------------------------------------------
// checks/provider.ts — provider materialization guards (Python-wording)
//
// Python register_check_provider / _materialize_check_providers is the
// divergence ground truth for these runtime guards; Go's provider surface is
// statically typed and needs none of them -- see the go exclusions in
// check_error_parity.py.
// ---------------------------------------------------------------------------

export function errCheckProviderMustBeCallable(): string {
	return "check provider must be callable";
}

export function errCheckProviderMustReturnList(typeDesc: string): string {
	return `check provider must return a list of CheckSpec, got ${typeDesc}`;
}

export function errCheckProviderNonCheckSpec(valueRepr: string): string {
	return `check provider returned a non-CheckSpec value: ${valueRepr}`;
}

// ---------------------------------------------------------------------------
// check_public.go
// ---------------------------------------------------------------------------

export function errChecksNotEnabled(): string {
	return "checks are not enabled on this App";
}

// ---------------------------------------------------------------------------
// schema.go
// ---------------------------------------------------------------------------

// The three project_id templates mirror Go's decomposition (not found / read
// error / no identifying directive) with the TS ecosystem project file:
// package.json "name" is the analog of Go's go.mod module path and Python's
// pyproject.toml [project].name. Each language names its own file here (the
// parity checker excludes these as language-specific).

export function errCannotDetermineProjectIDNoPackageJson(): string {
	return "Cannot determine project_id: package.json not found";
}

export function errCannotDetermineProjectIDReadError(errStr: string): string {
	return `Cannot determine project_id: error reading package.json: ${errStr}`;
}

export function errCannotDetermineProjectIDNoName(): string {
	return "Cannot determine project_id: no name field in package.json";
}

export function errSchemaMismatch(existingID: string, newID: string): string {
	return `Schema mismatch: existing schema belongs to project '${existingID}', not '${newID}'. Run from the correct project directory.`;
}

// ---------------------------------------------------------------------------
// tagdsl.go
// ---------------------------------------------------------------------------

export function errTagExprUnexpectedChar(ch: string, pos: number): string {
	return `tag expression: unexpected character ${q(ch)} at position ${pos}`;
}

export function errTagExprEmpty(): string {
	return "tag expression: empty expression";
}

export function errTagExprUnexpectedToken(val: string, pos: number): string {
	return `tag expression: unexpected token ${q(val)} at position ${pos}`;
}

export function errTagExprUnexpectedEnd(pos: number): string {
	return `tag expression: unexpected end of expression at position ${pos}`;
}

export function errTagExprExpectedRParen(pos: number): string {
	return `tag expression: expected ")" at position ${pos}`;
}

// ---------------------------------------------------------------------------
// context.go
// ---------------------------------------------------------------------------

export function errInfraValueUndeclared(envVar: string): string {
	return `InfraValue: ${q(envVar)} is not a declared infra root, handshake, or connection env var`;
}

export function errConnectionValueUndeclared(envVar: string): string {
	return `ConnectionEnvValue: ${q(envVar)} is not a declared connection env var`;
}

export function errNoSourceInfo(name: string): string {
	return `no source info for flag ${q(name)}`;
}

// ---------------------------------------------------------------------------
// outcome.go / Python _interpret_handler_return
// ---------------------------------------------------------------------------

// Python's template with the permitted types renamed to the TS contract
// (number | undefined | outcome(...)); the conformance oracle pins only the
// "command handler must return" prefix (outcome_contract.json, bad-return).
export function errHandlerReturnInvalid(got: string): string {
	return `command handler must return number (exit code), undefined (exit 0), or strictcli.outcome(...); got ${got}`;
}

export function errOutcomeExitCodeNotInteger(got: string): string {
	return `strictcli.outcome: exit_code must be an integer number; got ${got}`;
}

export function errGetNoSuchKey(name: string): string {
	return `strictcli.Get: no such key ${q(name)}`;
}

export function errGetKeyNil(name: string): string {
	return `strictcli.Get: key ${q(name)} is nil (not provided); use GetOpt for optional values`;
}

export function errGetTypeMismatch(
	name: string,
	gotType: string,
	wantType: string,
): string {
	return `strictcli.Get: key ${q(name)} has dynamic type ${gotType}, want ${wantType}`;
}

export function errGetOptNoSuchKey(name: string): string {
	return `strictcli.GetOpt: no such key ${q(name)}`;
}

export function errGetOptTypeMismatch(
	name: string,
	gotType: string,
	wantType: string,
): string {
	return `strictcli.GetOpt: key ${q(name)} has dynamic type ${gotType}, want ${wantType}`;
}

// ---------------------------------------------------------------------------
// tool.go
// ---------------------------------------------------------------------------

export function errJsonSchemaRouteError(errMsg: string): string {
	return `JsonSchema: ${errMsg}`;
}

export function errJsonSchemaIsGroup(commandPath: string): string {
	return `JsonSchema: '${commandPath}' is a group, not a command`;
}

export function errRouterCommandMustBeString(): string {
	return "command must be a string";
}

// ---------------------------------------------------------------------------
// effects.go — command classification (registration-time)
// ---------------------------------------------------------------------------

export function errFlagNameReservedByFramework(name: string): string {
	return `flag name '${name}' is reserved by the framework (dry-run, approve-consequential, quiet, verbose)`;
}

/**
 * The machine-mode flag's ban (§12.1, §19.1). --json is framework-owned: it
 * selects machine mode and is delivered on the Context, never as a handler
 * kwarg. The ban is the unconditional every-level one, exactly as the
 * quartet's is.
 */
export function errFlagNameJsonReserved(): string {
	return "flag name 'json' is reserved by the framework: --json selects machine mode";
}

/**
 * The outright `yes` ban (§12.1). `yes` owns no framework flag any more --
 * --approve-consequential replaced --yes -- but a private --yes would restate
 * it in a spelling that IS muscle memory, which is exactly what the rename
 * removed.
 */
export function errFlagNameYesBanned(): string {
	return "flag name 'yes' is banned by the framework: the confirmation skip is --approve-consequential";
}

/**
 * The consent PARAMETER ban (§12.1). `approve_consequential` is how a caller
 * states consent programmatically and over MCP, so a command may not declare
 * a flag or a positional arg of that name -- otherwise the same command means
 * different things on different channels.
 */
export function errFlagNameConsentReserved(): string {
	return "flag name 'approve_consequential' is reserved by the framework: it names the programmatic consent parameter";
}

export function errArgNameConsentReserved(): string {
	return "arg name 'approve_consequential' is reserved by the framework: it names the programmatic consent parameter";
}

export function errCommandEffectMissing(name: string): string {
	return `command ${q(name)}: effect classification is required (effect="read_only" or effect="mutating")`;
}

export function errCommandEffectInvalid(name: string, value: string): string {
	return `command ${q(name)}: invalid effect "${value}": must be "read_only" or "mutating"`;
}

export function errDeprecatedCommandEffect(name: string): string {
	return `deprecated command ${q(name)}: effect classification does not apply (a deprecated command has no handler)`;
}

/**
 * §8.1's declaration guard: classification answers "should a dry run record
 * rather than execute?" and consequential answers "are these effects worth
 * interrupting someone for?". A command that changes nothing has no effects to
 * weigh.
 */
export function errCommandReadOnlyConsequential(name: string): string {
	return `command ${q(name)}: a read_only command cannot be consequential (a command that changes nothing has nothing to confirm)`;
}

/**
 * Mirrors the guard above for the dry-run declaration: a command that changes
 * nothing records nothing, so a preview of it can never be dishonest and there
 * is no reason to refuse one.
 */
export function errCommandReadOnlyDryRunUnsupported(name: string): string {
	return `command ${q(name)}: a read_only command cannot declare dry_run_supported=false (a command that changes nothing has no effects a preview could misrepresent)`;
}

/**
 * The mandatory-reason guard. The reason is shown in help and in the parse-time
 * refusal, so a declaration without one leaves an operator staring at a refusal
 * with no explanation.
 */
export function errCommandDryRunReasonMissing(name: string): string {
	return `command ${q(name)}: dry_run_supported=false requires a non-empty dry_run_unsupported_reason (say what a preview cannot honestly show)`;
}

/**
 * The orphan-reason guard. Go has no analog: WithDryRunUnsupported is its only
 * way to set either field and it always sets both, so the shape is
 * unrepresentable there.
 */
export function errCommandDryRunReasonWithoutDeclaration(name: string): string {
	return `command ${q(name)}: dry_run_unsupported_reason requires dry_run_supported=false (there is nothing to explain while dry run is supported)`;
}

// ---------------------------------------------------------------------------
// effects.go — guard v2 and declared forwarding (registration-time)
//
// errHandlerVarKeywordUndeclared has no TS enforcement surface: a TS handler
// takes a typed args object, which cannot be introspected for a var-keyword
// parameter (contract §10.3). The template exists so the three catalogs stay
// in parity; check_error_parity.py carries the impl_exclusions rationale.
// ---------------------------------------------------------------------------

export function errHandlerVarKeywordUndeclared(name: string): string {
	return `command ${q(name)}: handler accepts **kwargs but the command does not declare forwarding; add forwarding=Forwarding(reason=...) or name every parameter explicitly`;
}

export function errForwardingReasonEmpty(name: string): string {
	return `command ${q(name)}: forwarding reason must be a non-empty string`;
}

export function errFrameworkInternalHandlerForeign(name: string): string {
	return `command ${q(name)}: handler is marked framework-internal but is not defined in the strictcli module`;
}

// ---------------------------------------------------------------------------
// effects.go — grant declaration (registration-time)
// ---------------------------------------------------------------------------

export function errGrantReasonEmpty(name: string, grant: string): string {
	return `command ${q(name)}: grant '${grant}' reason must be a non-empty string`;
}

export function errGrantDuplicate(name: string, grant: string): string {
	return `command ${q(name)}: duplicate grant '${grant}'`;
}

export function errGrantNameInvalid(name: string, grant: string): string {
	return `command ${q(name)}: invalid grant name '${grant}': must match [a-z][a-z0-9-]*`;
}

export function errGrantKindInvalid(
	name: string,
	grant: string,
	kind: string,
): string {
	return `command ${q(name)}: grant '${grant}' has invalid kind '${kind}': must be one of proc_mutate, proc_spawn, file_write, net_mutate`;
}

// ---------------------------------------------------------------------------
// effects.go — proc_observe_allowlist declaration (registration-time)
//
// The empty-prefix ban is shared with Go (a const there) and Python. The
// element-type guard is Python-and-TS: Go's WithProcObserveAllowlist takes
// [][]string, so a non-string element is inexpressible there.
// ---------------------------------------------------------------------------

export function errProcObserveAllowlistNotStrings(gotType: string): string {
	return `proc_observe_allowlist entries must be lists of strings, got ${gotType}`;
}

export function errProcObserveAllowlistEmptyPrefix(): string {
	return "proc_observe_allowlist entries must not be empty";
}

// ---------------------------------------------------------------------------
// effects.go — effect call-time errors
// ---------------------------------------------------------------------------

export function errEffectMutatingInReadOnly(
	name: string,
	method: string,
): string {
	return `command ${q(name)} is classified read_only; effects.${method} is a mutating operation`;
}

export function errEffectRunNotAllowlisted(name: string, argv: string): string {
	return `command ${q(name)} is classified read_only; effects.run argv ${argv} is not on the app's proc_observe_allowlist`;
}

export function errEffectGrantUndeclared(name: string, grant: string): string {
	return `command ${q(name)}: grant '${grant}' is not declared on this command`;
}

export function errEffectGrantKindMismatch(
	name: string,
	grant: string,
	declaredKind: string,
	usedKind: string,
): string {
	return `command ${q(name)}: grant '${grant}' is declared for kind ${declaredKind} but was used for a ${usedKind} effect`;
}

export function errEffectGrantOnObserve(name: string, grant: string): string {
	return `command ${q(name)}: grant '${grant}' cannot be used on an observe (an allowlisted effects.run changes nothing)`;
}

// ---------------------------------------------------------------------------
// effects.go — effect failure and parameter rejection (parse-time)
//
// Contract §12.8. These reach a handler's effect call through argv like any
// parse-time error, so they share that category and are coverage-checked by
// conformance cases.
// ---------------------------------------------------------------------------

export function errEffectRunFailed(
	name: string,
	method: string,
	argv: string,
	code: number,
): string {
	return `command ${q(name)}: effects.${method} failed: ${argv} exited ${code}`;
}

export function errEffectHTTPFailed(
	name: string,
	httpMethod: string,
	url: string,
	status: number,
): string {
	return `command ${q(name)}: effects.http failed: ${httpMethod} ${url} returned ${status}`;
}

export function errEffectOutputNotUTF8(name: string, method: string): string {
	return `command ${q(name)}: effects.${method} produced output that is not valid UTF-8`;
}

export function errEffectParamRejectsCarrier(
	name: string,
	method: string,
	param: string,
): string {
	return `command ${q(name)}: effects.${method} parameter '${param}' does not accept an unsettled value`;
}

export function errEffectOptionNotAccepted(
	name: string,
	method: string,
	opt: string,
): string {
	return `command ${q(name)}: effects.${method} does not accept option '${opt}'`;
}

// ---------------------------------------------------------------------------
// effects.go — effect argument type guards (call-time)
//
// Python raises these as inline TypeErrors; the TS type-name slots carry the
// TS runtime vocabulary, so check_error_parity.py excludes the type slot.
// ---------------------------------------------------------------------------

export function errEffectParamNotStringish(
	name: string,
	method: string,
	param: string,
	gotType: string,
): string {
	return `command ${q(name)}: effects.${method} parameter '${param}' must be a string, a path, or a forwarded effect result; got ${gotType}`;
}

export function errEffectArgvNotSequence(
	name: string,
	method: string,
	gotType: string,
): string {
	return `command ${q(name)}: effects.${method} argv must be a sequence of strings, not ${gotType}`;
}

export function errEffectArgvEmpty(name: string, method: string): string {
	return `command ${q(name)}: effects.${method} argv must not be empty`;
}

export function errEffectModeNotInt(name: string, gotType: string): string {
	return `command ${q(name)}: effects.chmod parameter 'mode' must be an int, got ${gotType}`;
}

export function errEffectHTTPMethodNotString(
	name: string,
	gotType: string,
): string {
	return `command ${q(name)}: effects.http parameter 'method' must be a string, got ${gotType}`;
}

// ---------------------------------------------------------------------------
// context.ts — the payload API's call-time errors (§19.4)
// ---------------------------------------------------------------------------

/**
 * Fires when a handler calls ctx.payload on a command that declares no payload
 * schema. Registration cannot see that a handler intends to call it, so call
 * time is the earliest honest point at which the missing declaration can be
 * named.
 */
export function errPayloadNoSchema(name: string): string {
	return `command ${q(name)}: ctx.payload requires a declared payload schema`;
}

/**
 * Fires on a second payload call in one dispatch. Two payloads are two answers
 * to a question with one slot; picking either silently is the kind of guess
 * this regime does not make.
 */
export function errPayloadAlreadySet(name: string): string {
	return `command ${q(name)}: ctx.payload was already called (a dispatch carries at most one payload)`;
}

/**
 * Fires at registration when a declared payload schema leaves the closed
 * subset (§19.5). `path` names the position inside the declared literal
 * (rooted at payload_schema) and `detail` names the violated rule; both are
 * byte-identical across the three implementations, pinned by
 * conformance/payload_schema_vectors.json.
 */
export function errPayloadSchemaInvalid(
	name: string,
	path: string,
	detail: string,
): string {
	return `command ${q(name)}: payload schema is invalid at ${path}: ${detail}`;
}

/**
 * Fires at emission when a payload deviates from its declared schema (§19.5).
 * `path` names the position inside the value (rooted at payload) and `detail`
 * names the violated constraint, so a wrong shape fails here instead of
 * shipping.
 */
export function errPayloadInvalid(
	name: string,
	path: string,
	detail: string,
): string {
	return `command ${q(name)}: payload does not satisfy the declared schema at ${path}: ${detail}`;
}

// ---------------------------------------------------------------------------
// effects.go — effects handle availability (TS-only)
//
// Python raises RuntimeError from the ctx.effects property; Go's Effects()
// method is only reachable from a dispatched Context. The TS Context class is
// exported, so a consumer can construct one outside a dispatch and reach the
// getter -- this names that state instead of returning a half-armed handle.
// ---------------------------------------------------------------------------

export function errEffectsUnavailable(): string {
	return "ctx.effects is unavailable: this Context was constructed outside a command dispatch";
}

// ---------------------------------------------------------------------------
// effects.go — dry-run truncation (parse-time)
//
// The template carries its own "error: " prefix: it is written straight to
// stderr, not through the parse-error formatter.
// ---------------------------------------------------------------------------

export function errDryRunTruncated(
	step: number,
	cmd: string,
	brand: string,
): string {
	return `error: dry-run preview ends at step ${step}: ${cmd} branched on unsettled value ${brand} — cannot preview past this point`;
}

// ---------------------------------------------------------------------------
// effects.go — an aborted dry-run preview (parse-time)
//
// Same shape and prefix as the truncation error above: both say the preview
// ended before the handler finished, and they differ only in why and in what
// the reader may conclude. Written straight to stderr.
// ---------------------------------------------------------------------------

export function errDryRunAborted(step: number, cmd: string): string {
	return `error: dry-run preview ends at step ${step}: ${cmd} aborted — the preview above may be incomplete`;
}

// ---------------------------------------------------------------------------
// effects.go — the confirm protocol (parse-time)
// ---------------------------------------------------------------------------

export function promptConfirmConsequential(name: string): string {
	return `about to run consequential command '${name}'. Proceed? [y/N] `;
}

/**
 * The non-interactive refusal (contract §8.3).
 *
 * It names what is required -- confirmation, at a terminal -- and never the
 * token that lifts the requirement. A refusal that prints its own override is
 * not a seam: the reflex it teaches is to append the override and re-run, which
 * is the opposite of the judgement the declaration asks for.
 */
export function errConfirmNonInteractive(): string {
	return "error: stdin is not interactive; a consequential command must be confirmed at a terminal";
}

export function errConfirmDeclined(): string {
	return "aborted";
}

/**
 * The programmatic-path refusal (contract §8.5).
 *
 * Requiring confirmation is a property of the COMMAND, so every channel has to
 * honour it -- but a programmatic caller has no terminal to prompt. The refusal
 * makes the caller state, in the call, that it is proceeding without a human,
 * instead of the framework deciding that silently on its behalf.
 */
export function errCallConsequentialUnconsented(cmdPath: string): string {
	return `command '${cmdPath}' is consequential: the call must carry confirmation`;
}

// ---------------------------------------------------------------------------
// strictcli.go — doParse dry-run refusal (parse-time)
//
// Raised for a command that declares dry_run_supported=false: rather than
// render a preview that would misrepresent what running it does, the framework
// refuses the flag and repeats the declared reason.
// ---------------------------------------------------------------------------

export function errDryRunNotSupported(cmdPath: string, reason: string): string {
	return `--dry-run is not supported by command '${cmdPath}': ${reason}`;
}

// ---------------------------------------------------------------------------
// toml.ts — TOML 1.0 acceptance gate (TS-only; parse-time)
//
// No Go/Python counterpart: the siblings' TOML parsers (go-toml-edit, tomllib)
// are TOML-1.0-native and reject 1.1-only constructs with their own parser
// errors. The TS stack parses with smol-toml (which accepts TOML 1.1), so an
// explicit gate rejects the six 1.1-only constructs pinned in docs/history/_ts-port-spec.md.
// ---------------------------------------------------------------------------

export function errTomlBasicStringEscape(esc: string): string {
	return `invalid escape sequence '\\${esc}' in basic string (TOML 1.1 construct; strictcli requires TOML 1.0)`;
}

export function errTomlInlineTableNewline(): string {
	return "newline inside inline table (TOML 1.1 construct; strictcli requires TOML 1.0)";
}

export function errTomlInlineTableTrailingComma(): string {
	return "trailing comma in inline table (TOML 1.1 construct; strictcli requires TOML 1.0)";
}

export function errTomlTimeMissingSeconds(): string {
	return "time without seconds (TOML 1.1 construct; strictcli requires TOML 1.0)";
}

export function errTomlDatetimeMissingSeconds(): string {
	return "datetime without seconds (TOML 1.1 construct; strictcli requires TOML 1.0)";
}

// ---------------------------------------------------------------------------
// toml.ts — single-key splicer verification (TS-only)
//
// The comment/order-preserving `config set` splicer re-parses both file
// versions and asserts that only the target key changed. A verification
// failure is an internal invariant violation, never expected in normal use.
// ---------------------------------------------------------------------------

export function errTomlSpliceVerifyFailed(key: string): string {
	return `internal: TOML splice verification failed: keys other than "${key}" changed`;
}

export function errTomlSpliceKeyNotFound(key: string): string {
	return `internal: TOML splice: key "${key}" not found in document`;
}

// ---------------------------------------------------------------------------
// app.ts — config option validation (Python App.__post_init__ spelling)
//
// Go spells these via WithConfigFormat/WithConfigConflictMode panics (see the
// option-constructor section above); Python raises ValueError with the App.*
// spelling. Python is the divergence ground truth for registration errors, so
// the app-level checks use these templates. Slots take pre-formatted reprs.
// ---------------------------------------------------------------------------

export function errAppConfigFormatBad(gotRepr: string): string {
	return `App.config_format must be "json" or "toml", got ${gotRepr}`;
}

export function errAppConfigConflictModeBad(gotRepr: string): string {
	return `App.config_conflict_mode must be "cli-wins" or "error", got ${gotRepr}`;
}

// ---------------------------------------------------------------------------
// factories.ts — the presence declaration (registration-time, contract §12.12)
//
// Every flag and every arg declares EXACTLY ONE of required / optional / a
// value default (§23.1). The sentence in each template is byte-identical
// across the three implementations; the SPELLINGS inside it are each
// language's own, which is §12.10's handle-availability precedent applied to
// a larger triple.
// ---------------------------------------------------------------------------

/** TypeScript's spelling of the required declaration (flags and args alike). */
export const PRESENCE_SPELLING_REQUIRED = 'presence: "required"';
/** TypeScript's spelling of the optional declaration (flags and args alike). */
export const PRESENCE_SPELLING_OPTIONAL = 'presence: "optional"';
/**
 * TypeScript's spelling of the default declaration. The value clause is part
 * of the spelling: `presence: "default"` alone declares nothing, because the
 * union member carries the value.
 */
export function presenceSpellingDefault(formattedValue: string): string {
	return `presence: "default" with default: ${formattedValue}`;
}
/**
 * The default spelling WITHOUT its value clause, for the variadic-arg message:
 * that message is about the spelling being inapplicable, not about the value
 * that was written (§12.12).
 */
export const PRESENCE_SPELLING_DEFAULT_BARE = 'presence: "default"';
/**
 * The default spelling with an unfilled value clause, for the zero-declaration
 * message: nothing was written, so there is no value to render.
 */
export const PRESENCE_SPELLING_DEFAULT_PLACEHOLDER =
	'presence: "default" with default: <value>';

export function errFlagPresenceUndeclared(name: string): string {
	return `Flag ${q(name)}: presence is undeclared: declare exactly one of ${PRESENCE_SPELLING_REQUIRED}, ${PRESENCE_SPELLING_OPTIONAL}, or ${PRESENCE_SPELLING_DEFAULT_PLACEHOLDER}`;
}

export function errArgPresenceUndeclared(name: string): string {
	return `Arg ${q(name)}: presence is undeclared: declare exactly one of ${PRESENCE_SPELLING_REQUIRED}, ${PRESENCE_SPELLING_OPTIONAL}, or ${PRESENCE_SPELLING_DEFAULT_PLACEHOLDER}`;
}

export function errFlagPresenceDeclaredTwice(
	name: string,
	first: string,
	second: string,
): string {
	return `Flag ${q(name)}: presence is declared twice: ${first} and ${second} cannot be combined; declare exactly one`;
}

export function errArgPresenceDeclaredTwice(
	name: string,
	first: string,
	second: string,
): string {
	return `Arg ${q(name)}: presence is declared twice: ${first} and ${second} cannot be combined; declare exactly one`;
}

/**
 * One spelling per fact: the value-shaped spelling of optionality is refused
 * and redirected rather than accepted as a second synonym (§23.1, item 138).
 * The parenthetical is not decoration -- the value the reader wanted is
 * exactly what the redirected spelling delivers.
 */
export function errFlagDefaultNullNotOptional(name: string): string {
	return `Flag ${q(name)}: default: null does not declare optionality: use presence: "optional" (it delivers undefined when the flag is absent)`;
}

export function errArgDefaultNullNotOptional(name: string): string {
	return `Arg ${q(name)}: default: null does not declare optionality: use presence: "optional" (it delivers undefined when the arg is absent)`;
}

// §23.5's mutex row is superseded (§21's box, §18.15 item 178) and its
// template is DELETED rather than reworded: `errFlagMutexMemberRequired` said
// a member may NOT declare requiredness, and a member flag now MUST -- two
// opposite rules about the same declaration, which item 149's rule refuses to
// carry on one name. The live rule is §12.13's `errMemberFlagPresence`.

/** §23.3: a variadic arg always delivers a list, so the empty case is `optional`. */
export function errArgVariadicDefault(name: string): string {
	return `Arg ${q(name)}: a variadic arg cannot declare ${PRESENCE_SPELLING_DEFAULT_BARE}: it always delivers a list, so declare ${PRESENCE_SPELLING_REQUIRED} for at least one value or ${PRESENCE_SPELLING_OPTIONAL} for possibly none`;
}

/**
 * TypeScript-only, for a state no sibling can reach: `presence: "default"`
 * with no `default` value. Python's `default=<value>` and Go's `Default(v)`
 * ARE the value, so a half-written default declaration is inexpressible
 * there; TS's union member can be widened past the type system with the value
 * missing. Same precedent as the repeatable-requires-a-list-type message.
 */
export function errFlagDefaultValueMissing(name: string): string {
	return `Flag ${q(name)}: ${PRESENCE_SPELLING_DEFAULT_BARE} requires a default value: declare default: <value>, or ${PRESENCE_SPELLING_OPTIONAL} for no value`;
}

export function errArgDefaultValueMissing(name: string): string {
	return `Arg ${q(name)}: ${PRESENCE_SPELLING_DEFAULT_BARE} requires a default value: declare default: <value>, or ${PRESENCE_SPELLING_OPTIONAL} for no value`;
}

// ---------------------------------------------------------------------------
// factories.ts / parse.ts -- the scoped-selector construct (contract §12.13)
//
// The family splits across both parity categories: the election, scope and
// delivery templates are PARSE-time (stderr, exit 1); the declaration guards
// are REGISTRATION-time, in §12.10's and §12.12's class.
//
// Templates naming a SPELLING carry TypeScript's own noun phrase inside a
// byte-identical sentence (§12.12's mechanism): `<record-spelling>` is
// `{ value: <value>, help: "..." }`, `<selector-spelling>` is `choiceFlag(...)`
// and `<member-selector-spelling>` is `memberChoiceFlag(...)`.
// ---------------------------------------------------------------------------

/** TypeScript's spelling of a `choices` entry record (§24.2, §12.13). */
export const RECORD_SPELLING = '{ value: <value>, help: "..." }';
/** TypeScript's spelling of the selector constructor (§24.12, §12.13). */
export const SELECTOR_SPELLING = "choiceFlag(...)";
/** TypeScript's spelling of the member-spelled selector constructor (§24.12). */
export const MEMBER_SELECTOR_SPELLING = "memberChoiceFlag(...)";

/**
 * Where a payload-carrying member's short goes in this language: the payload
 * declaration IS the electing flag's declaration, the way `MemberChoice`'s
 * first argument is in Go and `member_value(...)` is in Python (§24.12).
 */
export const MEMBER_PAYLOAD_SHORT_SPELLING = "choice({ value: { short } })";

/** The `Choice "<c>" of "<sel>": ` prefix -- the round's one new prefix family. */
function choicePrefix(choiceName: string, selector: string): string {
	return `Choice ${q(choiceName)} of ${q(selector)}: `;
}

// --- Parse-time: a required flag that nothing supplied ---

/**
 * Which required-flag sentence a declaration takes. A bool's requirement names
 * the tokens that satisfy it -- `--x` IS the value and `--no-x` is the other
 * one, so "is required" would leave a reader looking for a value to type --
 * and a non-negatable bool names the only token it has.
 */
export type RequiredFlagForm = "negatable-bool" | "bool" | "value";

/**
 * The required-flag sentence, at root scope and inside a scope alike. It is a
 * COMPLETE sentence: a scoped site appends the scope clause and the origin
 * clause to it, in that order (§12.13), and never writes a sentence of its
 * own for the same condition.
 */
export function errFlagRequired(name: string, form: RequiredFlagForm): string {
	if (form === "negatable-bool") {
		return `flag '--${name}' must be passed as --${name} or --no-${name}`;
	}
	if (form === "bool") {
		return `flag '--${name}' must be passed as --${name}`;
	}
	return `flag '--${name}' is required`;
}

// --- Parse-time: the scope suffix and the out-of-scope frame ---

/**
 * The scope suffix appended to a presence message when the flag or the
 * selector lives inside a scope. Empty at root scope, which is what makes
 * every root-scope message byte-identical to what it was before this round.
 */
export function errScopeSuffix(path: string): string {
	return path === "" ? "" : ` under '${path}'`;
}

/**
 * The round's central parse error, and deliberately NOT "unknown flag": the
 * flag is declared, it is simply not in the elected scope, and the sentence
 * names both sides. `owners` is one or more quoted scope paths joined by
 * ` or `; `why` is one of the three clause templates below.
 */
export function errFlagOutOfScope(
	x: string,
	owners: string,
	why: string,
): string {
	return `flag '--${x}' is only valid under ${owners}, but ${why}`;
}

/** Out-of-scope clause: another choice of the blocking selector was elected. */
export function errScopeWhyElected(path: string, origin: string): string {
	return `'${path}' was elected${origin}`;
}

/** Out-of-scope clause: a required token-spelled selector elected nothing. */
export function errScopeWhyNotProvided(sel: string): string {
	return `'--${sel}' was not provided`;
}

/** Out-of-scope clause: the member-spelled twin, which names no selector token. */
export function errScopeWhyNoMemberElected(members: string): string {
	return `none of ${members} was elected`;
}

// --- Parse-time: where an election came from ---

/** Origin clause: the election came from an environment variable. */
export function errElectionOriginEnv(envVar: string): string {
	return ` from env var '${envVar}'`;
}

/** Origin clause: the election came from a config key. */
export function errElectionOriginConfig(key: string): string {
	return ` from config key '${key}'`;
}

/** Origin clause: the declaration's own default elected. */
export const errElectionOriginDefault = " by default";

/**
 * The parenthesized wrapper composition produces (§18.18 item 212). A
 * command-line election produces the EMPTY suffix rather than a bare
 * `(elected)`: the wrapper exists exactly when the clause it wraps does.
 */
export function errElectionOriginSuffix(origin: string): string {
	return origin === "" ? "" : ` (elected${origin})`;
}

/**
 * A selector elected more than once. Last-wins is right for a plain flag and
 * wrong for an election, because discarding a value would discard a whole
 * scope with it. Values in command-line order, each quoted, joined by ` and `.
 */
export function errSelectorElectedTwice(sel: string, values: string): string {
	return `--${sel}: elected more than once, as ${values}`;
}

// --- Parse-time diagnostics: conditional ambient bindings (§24.6) ---

/**
 * A binding whose scope was not elected is never consulted -- not an error,
 * not a value. Every skipped binding is named under --verbose, one line per
 * binding, in declaration order, at debug level.
 */
export function errAmbientBindingSkippedEnv(
	envVar: string,
	x: string,
	path: string,
): string {
	return `not consulted: env var '${envVar}' binds flag '--${x}' under '${path}', which was not elected`;
}

export function errAmbientBindingSkippedConfig(
	key: string,
	x: string,
	path: string,
): string {
	return `not consulted: config key '${key}' binds flag '--${x}' under '${path}', which was not elected`;
}

// --- Registration guards (§12.13) ---

/**
 * Ruling B2 made structural: there is no at-most-one construct anywhere in
 * the framework, and this is the one place a consumer would otherwise
 * reintroduce it.
 */
export function errSelectorOptional(name: string): string {
	return `Flag ${q(name)}: a choice flag cannot declare ${PRESENCE_SPELLING_OPTIONAL}: an absent selection is a choice nobody named, so name it as a choice of its own`;
}

export function errSelectorNoChoices(name: string): string {
	return `Flag ${q(name)}: a choice flag must declare at least two choices`;
}

/**
 * Structurally unreachable through TypeScript's keyed choice map (object keys
 * are unique by construction); enforced defensively for a widened or
 * JSON-driven caller, exactly as the presence checks are.
 */
export function errChoiceDuplicateName(sel: string, c: string): string {
	return `Flag ${q(sel)}: choice ${q(c)} is declared twice`;
}

export function errChoiceHelpEmpty(sel: string, c: string): string {
	return `${choicePrefix(c, sel)}help text is required`;
}

/**
 * §24.7's choice-name charset. TypeScript spells a choice name as a property
 * key of the choice map, which imposes no charset of its own, so the rule is a
 * runtime check like every other name ban -- and it runs BEFORE member
 * spelling's flag-name bans, so a name that fails both is reported as the
 * charset failure it is.
 */
export function errChoiceNameCharset(sel: string, c: string): string {
	return `Flag ${q(sel)}: choice name ${q(c)} must match [a-z][a-z0-9-]*`;
}

export function errSelectorDefaultUnknownChoice(
	sel: string,
	defaultSpelling: string,
	names: string,
): string {
	return `Flag ${q(sel)}: ${defaultSpelling} names no declared choice: must be one of: ${names}`;
}

/**
 * A defaulted selection is complete: a choice plus every field its scope
 * needs, so a defaulted selection with an unsatisfied required sub-flag
 * cannot exist. Python-excluded -- Python's default IS a choice instance, so
 * the incomplete state is unconstructable rather than refused (§24.5).
 */
export function errSelectorDefaultIncomplete(
	sel: string,
	defaultSpelling: string,
	c: string,
	sub: string,
): string {
	return `Flag ${q(sel)}: ${defaultSpelling} elects choice ${q(c)}, whose scope declares the required flag '--${sub}': a defaulted selection must be complete with nothing typed`;
}

/**
 * The rule §12.12's `errFlagMutexMemberRequired` inverted: a member flag now
 * MUST declare requiredness, read as *required once this member is elected*.
 *
 * TypeScript's authored spelling satisfies the rule by construction -- a
 * member's payload is supplied by the token that elects it, so the choice
 * object has no presence slot at all. The guard fires only for a widened or
 * JSON-driven caller that writes a presence onto a choice.
 */
export function errMemberFlagPresence(sel: string, m: string): string {
	return `${choicePrefix(m, sel)}a member flag must declare ${PRESENCE_SPELLING_REQUIRED}, read as required once this member is elected`;
}

export function errMemberSelectorShort(sel: string): string {
	return `Flag ${q(sel)}: a member-spelled choice flag is never typed, so it cannot carry a short: declare the short on a member`;
}

export function errMemberDefaultCarriesValue(
	sel: string,
	defaultSpelling: string,
	c: string,
): string {
	return `Flag ${q(sel)}: ${defaultSpelling} elects choice ${q(c)}, whose flag carries a value nothing supplies: only a payload-less member can be a default`;
}

/**
 * A payload is delivered under the reserved name `value` and ONLY under
 * member spelling: a token-spelled choice is named by the token itself and
 * has no payload to carry (§24.4, §18.18 item 210).
 */
export function errTokenChoiceCarriesPayload(sel: string, c: string): string {
	return `${choicePrefix(c, sel)}a token-spelled choice cannot carry a payload: the token names the choice, and a choice that carries its own value belongs to a member-spelled choice flag, declared with ${MEMBER_SELECTOR_SPELLING}`;
}

/**
 * A short on a token-spelled choice names nothing: the selector's own value
 * names the choice, and a value has no short form. Only a member-spelled
 * choice puts a flag of its own on the command line (§24.4).
 */
export function errTokenChoiceCarriesShort(sel: string, c: string): string {
	return `${choicePrefix(c, sel)}a token-spelled choice cannot carry a short: the token names the choice, and only a member-spelled choice has a flag of its own to carry one`;
}

/**
 * One member, one place to declare its short (§24.4, §24.12). A
 * payload-carrying member's electing flag IS its payload declaration, so a
 * short beside the choice would be a second declaration that must agree.
 */
export function errMemberShortOnPayloadChoice(sel: string, c: string): string {
	return `${choicePrefix(c, sel)}a payload-carrying member declares its short on its payload: ${MEMBER_PAYLOAD_SHORT_SPELLING}`;
}

/**
 * §12.14's schema-v2 declaration guard, on the flag surface.
 *
 * The published `value_schema` fragment carries a declaration's choices as a
 * JSON Schema `enum`, and a reader that parses JSON numbers as IEEE-754
 * doubles -- which every reader of a `.strictcli/schema.json` is entitled to
 * be -- reads back a DIFFERENT integer. The framework refuses the declaration
 * rather than publishing a fragment it already knows will be misread.
 *
 * Float choices are deliberately exempt: the canonical float form is by
 * construction the shortest string that round-trips to the identical double,
 * so a double-parsing reader recovers the declared value exactly.
 *
 * `<v>` renders as the integer's decimal digits with a leading `-` when
 * negative and no separators -- `String(bigint)`, which coincides with
 * Python's `repr` and Go's `%d`.
 */
export function errFlagChoiceMagnitude(name: string, v: string): string {
	return `Flag ${q(name)}: choice ${v}: ${PDETAIL_MAGNITUDE}`;
}

/** §12.14's guard on the arg surface -- one condition, two surfaces. */
export function errArgChoiceMagnitude(name: string, v: string): string {
	return `Arg ${q(name)}: choice ${v}: ${PDETAIL_MAGNITUDE}`;
}

/**
 * The bare-value `choices` entry is deleted: an entry that may carry help and
 * an entry that carries none would be two spellings of one fact (§24.2).
 */
export function errChoicesEntryNotRecord(name: string, i: number): string {
	return `Flag ${q(name)}: choices entry ${i} is a bare value: declare it as ${RECORD_SPELLING}`;
}

/**
 * The arg-side sibling. §12.13 pins the flag spelling; positional args keep
 * `choices` in the same record form (§24.7), and this catalog twins every
 * flag/arg message that reaches both surfaces (errFlagChoicesEmpty /
 * errArgChoicesEmpty and the whole default-type-mismatch family), so an arg
 * reports itself as an arg rather than borrowing the flag prefix.
 */
export function errArgChoicesEntryNotRecord(name: string, i: number): string {
	return `Arg ${q(name)}: choices entry ${i} is a bare value: declare it as ${RECORD_SPELLING}`;
}

// --- Registration guards: reserved names inside a scope (§24.7) ---

export function errScopedNameChoiceReserved(c: string, sel: string): string {
	return `${choicePrefix(c, sel)}flag name 'choice' is reserved by the framework: it tags the delivered record`;
}

export function errScopedNameValueReserved(c: string, sel: string): string {
	return `${choicePrefix(c, sel)}flag name 'value' is reserved by the framework: it carries a member-spelled choice's own payload`;
}

// --- Registration guards: name collisions (§24.7) ---

export function errScopedNameCollidesRoot(
	c: string,
	sel: string,
	x: string,
): string {
	return `${choicePrefix(c, sel)}flag '--${x}' collides with a command-level flag of the same name: the scoped one could never be reached`;
}

export function errScopedNameCollidesSelector(
	c: string,
	sel: string,
	x: string,
): string {
	return `${choicePrefix(c, sel)}flag '--${x}' collides with the choice flag's own name`;
}

/**
 * One condition, one message: a name reused by sibling scopes whose two
 * declarations do not tokenize identically. The noun is VALUE SHAPE, §25.3's
 * word for type-and-arity together (§18.18 item 208).
 */
export function errSiblingScopeShapeMismatch(
	sel: string,
	x: string,
	a: string,
	b: string,
): string {
	return `Flag ${q(sel)}: flag '--${x}' is declared by choices ${q(a)} and ${q(b)} with different value shapes: sibling scopes may reuse a name only with an identical type and arity, because tokenizing '--${x}' cannot wait for an election`;
}

/**
 * Written against SIMULTANEOUSLY ELECTABLE scopes rather than siblings, so
 * that adopting multi-elect (§24.13) narrows an existing rule instead of
 * contradicting one.
 */
export function errCoElectableNameReuse(
	name: string,
	x: string,
	p1: string,
	p2: string,
): string {
	return `command ${q(name)}: flag '--${x}' is declared under '${p1}' and under '${p2}', which can be elected at the same time: simultaneously electable scopes may not reuse a flag name`;
}

export function errShortCollidesAcrossScopes(
	name: string,
	s: string,
	a: string,
	b: string,
): string {
	return `command ${q(name)}: short '-${s}' is claimed by '--${a}' and '--${b}', which can be elected at the same time`;
}

/**
 * `errSiblingScopeShapeMismatch`'s argument one token over (§18.19 item 221):
 * which NAME a reused short binds to is decided after the election, so the
 * short may only be reused when the token consumes argv identically whatever
 * the election decides. Same noun (value shape), same reason clause -- widened
 * rather than twinned into an arity-only sibling.
 *
 * The site is the COMMAND, because the two claimants live in different scopes
 * and neither one owns the collision.
 */
export function errShortShapeMismatch(
	name: string,
	s: string,
	a: string,
	b: string,
): string {
	return `command ${q(name)}: short '-${s}' is claimed by '--${a}' and '--${b}' with different value shapes: sibling scopes may reuse a short only with an identical type and arity, because tokenizing '-${s}' cannot wait for an election`;
}

/**
 * The ordering hazard shape comparison cannot see: a short reused across
 * sibling scopes AND claimed by a candidate that is itself an election token
 * is refused outright, because the binding resolves after the elections and an
 * election token has to be readable before any election has happened. There is
 * no shape that makes it legal, so the guard refuses rather than comparing.
 */
export function errShortOnAmbiguousElection(
	name: string,
	s: string,
	x: string,
): string {
	return `command ${q(name)}: short '-${s}' is reused by sibling scopes and also claimed by '--${x}', which elects: an election token is read before any election has happened, so its short cannot be shared`;
}

/** A positional's meaning cannot depend on an election typed after it (§24.7). */
export function errScopedPositional(c: string, sel: string): string {
	return `${choicePrefix(c, sel)}positional args cannot be declared inside a choice scope: a positional's meaning would depend on an election that may be typed after it`;
}

/**
 * The scope already IS the constraint, and expressing one fact in two
 * mechanisms is how the two disagree later (§24.8). The refusal names the
 * CONSTRAINT rather than its family (§12.15's amendment table): one of the
 * three family words (`CoRequired`) no longer exists, and a mandatory name is
 * a better identifier than a family word ever was.
 */
export function errConstraintReferencesScopedFlag(
	name: string,
	c: string,
	x: string,
	path: string,
): string {
	return `command ${q(name)}: constraint ${q(c)} references '${x}', which is declared under '${path}': constraints operate at root scope only`;
}

// --- Registration guards: the constraint system (§12.15, §26.8) ---

/** The three `when` spellings, per §12.15's per-language substitution table. */
export const WHEN_PRESENT_SPELLING = 'when: "present"';
export const WHEN_TRUE_SPELLING = 'when: "true"';
export const WHEN_NON_EMPTY_SPELLING = 'when: "non_empty"';
/**
 * `<constraint-member-spelling>`: TypeScript's member is a plain object
 * literal, matching the value-flag choice record (§26.6). The `<x>` slot is
 * literal here, as Python's `Member("<x>")` is -- the template names the
 * shape, and the entry that violated it carries no name to substitute.
 */
export const CONSTRAINT_MEMBER_SPELLING = '{ name: "<x>" }';

export function errConstraintNameCharset(name: string, c: string): string {
	return `command ${q(name)}: constraint name ${q(c)} must match [a-z][a-z0-9-]*`;
}

export function errConstraintNameDuplicate(name: string, c: string): string {
	return `command ${q(name)}: duplicate constraint name ${q(c)}`;
}

export function errConstraintNameCollides(name: string, c: string): string {
	return `command ${q(name)}: constraint name ${q(c)} is already a flag or arg name: a member reference resolves by name and would be ambiguous`;
}

/**
 * Reachable only through a WIDENED or JSON-shaped caller: `members` is typed
 * `readonly [ConstraintMember, ConstraintMember, ...ConstraintMember[]]`, so
 * the two-member floor is a COMPILE error for an ordinary caller (§26.6).
 * The runtime guard stays because the covering input exists -- the treatment
 * §12.13 item 213 established. Go-excluded: its constructors take two named
 * members before the variadic tail.
 */
export function errConstraintMinMembers(
	name: string,
	c: string,
	n: number,
): string {
	return `command ${q(name)}: constraint ${q(c)} must declare at least two members, got ${n}`;
}

/**
 * `errChoicesEntryNotRecord`'s exact discipline one construct over (§26.6): a
 * spelling that lets one member carry an election and another not carry the
 * word for it is two spellings for one fact.
 */
export function errConstraintMemberNotRecord(
	name: string,
	c: string,
	i: number,
): string {
	return `command ${q(name)}: constraint ${q(c)} member ${i} is a bare name: declare it as ${CONSTRAINT_MEMBER_SPELLING}`;
}

export function errConstraintMemberUnknown(
	name: string,
	c: string,
	x: string,
): string {
	return `command ${q(name)}: constraint ${q(c)} references unknown member ${q(x)}`;
}

export function errConstraintMemberAmbiguous(
	name: string,
	c: string,
	x: string,
): string {
	return `command ${q(name)}: constraint ${q(c)} references ${q(x)}, which names both a flag and a positional arg`;
}

export function errConstraintMemberDuplicate(
	name: string,
	c: string,
	x: string,
): string {
	return `command ${q(name)}: constraint ${q(c)} declares member ${q(x)} twice`;
}

/**
 * §26.5's inversion: a constraint never subtracts from a declaration, so
 * membership neither makes a flag required nor exempts it from being
 * required -- and a member that declares requiredness leaves the constraint
 * nothing to decide. `<x>` renders by §12.15's single-member quoting rule.
 */
export function errConstraintMemberRequired(
	name: string,
	c: string,
	member: string,
): string {
	return `command ${q(name)}: constraint ${q(c)} member ${member} declares ${PRESENCE_SPELLING_REQUIRED}: a member the invocation must always supply leaves the constraint nothing to decide`;
}

/**
 * The default `when` is `present`, and omitting it on a BOOL is refused: that
 * default would otherwise mean `--no-all` engages a constraint while selecting
 * nothing -- the mutex survey's shipped-dangerous class arriving in a new
 * family by omission (§26.3).
 */
export function errConstraintMemberBoolWhen(
	name: string,
	c: string,
	member: string,
): string {
	return `command ${q(name)}: constraint ${q(c)} member ${member} is a bool and must declare its election: ${WHEN_TRUE_SPELLING} counts only a true value, ${WHEN_PRESENT_SPELLING} counts any`;
}

/**
 * `<t>` is the framework's OWN type word (`str`, `bool`, `int`, `float`, and
 * the compound spellings the existing type errors use), never a language type
 * name. The member renders once quoted after `member` and once more as the
 * subject of the trailing clause -- the same rendering both times.
 */
export function errConstraintWhenTrueNotBool(
	name: string,
	c: string,
	member: string,
	t: string,
): string {
	return `command ${q(name)}: constraint ${q(c)} member ${member} declares ${WHEN_TRUE_SPELLING}, which needs a bool; ${member} is a ${t}`;
}

export function errConstraintWhenNonEmptyNotSized(
	name: string,
	c: string,
	member: string,
	t: string,
): string {
	return `command ${q(name)}: constraint ${q(c)} member ${member} declares ${WHEN_NON_EMPTY_SPELLING}, which needs a string or a collection; ${member} is a ${t}`;
}

export function errConstraintNestedWhen(
	name: string,
	c: string,
	x: string,
): string {
	return `command ${q(name)}: constraint ${q(c)} member ${q(x)} is a constraint and cannot declare an election: a nested constraint is engaged when its own members are`;
}

export function errConstraintNestedFamily(
	name: string,
	c: string,
	x: string,
): string {
	return `command ${q(name)}: constraint ${q(c)} references constraint ${q(x)}, which declares a one-way dependency rather than a co-occurrence rule: only at-least-one and all-or-none can be members of another constraint`;
}

/**
 * `<path>` renders the participating names joined by ` -> `, starting AND
 * ending at the same name, beginning at the first participant in declaration
 * order. A constraint naming itself is the degenerate case and takes this same
 * template, never a second one (§12.15).
 */
export function errConstraintCycle(name: string, path: string): string {
	return `command ${q(name)}: constraints form a cycle: ${path}`;
}

/**
 * `Requires` and `Implies` keep the flags-only operand vocabulary (§26.13),
 * so their unknown-name refusal keeps the flag noun. The two shipped
 * family-specific templates collapse onto the constraint's own name, which is
 * what tells the reader which declaration to fix.
 */
export function errConstraintUnknownFlag(
	name: string,
	c: string,
	x: string,
): string {
	return `command ${q(name)}: constraint ${q(c)} references unknown flag ${q(x)}`;
}

// ---------------------------------------------------------------------------
// factories.ts / parse.ts / update.ts — the update-command construct
// (contract §12.16, §27)
//
// The family splits across both parity categories: the two VIOLATION
// templates are parse-time (stderr, exit 1); the fifteen DECLARATION GUARDS
// are registration-time, in §12.10's and §12.12's class.
//
// `<decl>` is `flag '--<x>'` for a flag and `argument '<x>'` for a positional
// arg. The presence spellings inside these sentences take the FLAG spelling
// even when the subject is an arg -- TypeScript spells both operand kinds one
// way already, so the distinction §18.31 item 287 records for Go costs nothing
// here.
//
// The prefix is `command "<name>": ` on every registration guard, and
// `update "<resource>": ` on the one violation the construct owns.
// ---------------------------------------------------------------------------

/** TypeScript's spelling of the clear declaration (§12.16's added row). */
export const NULLABLE_SPELLING = "nullable: true";

/**
 * The ONLY guard in this family that fires on a command declaring no update at
 * all: the ban keys on `effect: "mutating"` (§27.1), where the rest keys on
 * `updateOf`. The value renders through the same formatter every other
 * declaration guard uses (§12.12).
 */
export function errMutatingDefault(
	name: string,
	decl: string,
	formattedValue: string,
): string {
	return `command ${q(name)}: ${decl} declares ${presenceSpellingDefault(formattedValue)} on a mutating command: absence would write a value the invocation never stated (declare ${PRESENCE_SPELLING_REQUIRED} or ${PRESENCE_SPELLING_OPTIONAL}, or apply the fallback in the handler and say so in its help)`;
}

export function errUpdateOnReadOnly(name: string): string {
	return `command ${q(name)}: a read_only command cannot declare update_of (a command that changes nothing writes no properties)`;
}

/**
 * Reachable in all three implementations, and the reachable inputs differ: in
 * TypeScript it is a widened caller past the literal union (§12.13 item 213's
 * treatment). The sentence is byte-identical in each because the value
 * formatter is.
 */
export function errUpdateWriteModeInvalid(name: string, v: string): string {
	return `command ${q(name)}: invalid write_mode ${q(v)}: must be "sparse" or "full_replace"`;
}

export function errUpdateResourceCharset(name: string, r: string): string {
	return `command ${q(name)}: update resource ${q(r)} must match [a-z][a-z0-9-]*`;
}

export function errUpdatePropertiesEmpty(name: string, r: string): string {
	return `command ${q(name)}: update of ${q(r)} declares no properties: an update with nothing to write is not an update`;
}

export function errUpdateNameUnknown(
	name: string,
	r: string,
	x: string,
): string {
	return `command ${q(name)}: update of ${q(r)} references unknown name ${q(x)}`;
}

export function errUpdateNameAmbiguous(
	name: string,
	r: string,
	x: string,
): string {
	return `command ${q(name)}: update of ${q(r)} references ${q(x)}, which names both a flag and a positional arg`;
}

export function errUpdateNameDuplicate(
	name: string,
	r: string,
	x: string,
): string {
	return `command ${q(name)}: update of ${q(r)} declares ${q(x)} twice`;
}

export function errUpdateNameBothRoles(
	name: string,
	r: string,
	x: string,
): string {
	return `command ${q(name)}: update of ${q(r)} declares ${q(x)} as both identity and property`;
}

export function errUpdateReferencesScopedFlag(
	name: string,
	r: string,
	x: string,
	path: string,
): string {
	return `command ${q(name)}: update of ${q(r)} references '${x}', which is declared under '${path}': an update's identity and properties are declared at root scope only`;
}

/**
 * Covers `required` ONLY: a property declaring a default is refused by
 * errMutatingDefault four steps earlier (§27.11's order), an update command
 * being mutating by §27.2's own guard, so the two never compete.
 */
export function errUpdatePropertyPresence(
	name: string,
	r: string,
	decl: string,
): string {
	return `command ${q(name)}: update of ${q(r)} property ${decl} declares ${PRESENCE_SPELLING_REQUIRED}: a property is absent exactly when it is not being written, and the presence declaration for that is ${PRESENCE_SPELLING_OPTIONAL}`;
}

export function errUpdatePropertyIsArg(
	name: string,
	r: string,
	x: string,
): string {
	return `command ${q(name)}: update of ${q(r)} property ${q(x)} is a positional arg: a property must be individually omissible and clearable, and only a flag is`;
}

export function errUpdatePropertyIsChoiceFlag(
	name: string,
	r: string,
	x: string,
): string {
	return `command ${q(name)}: update of ${q(r)} property '--${x}' is a choice flag: an elected record is a selection, not a property value`;
}

export function errNullableNotProperty(name: string, decl: string): string {
	return `command ${q(name)}: ${decl} declares ${NULLABLE_SPELLING} but is not a property of an update: only a property can be cleared`;
}

export function errUnsetNameReserved(name: string, x: string): string {
	return `command ${q(name)}: flag name "unset-${x}" is reserved: property '--${x}' declares ${NULLABLE_SPELLING}, which mints '--unset-${x}'`;
}

// --- The two violations (parse-time) ---

/**
 * Names EVERY declared property, whether or not it is nullable: naming only
 * the ones a reader has not used would require the framework to guess which
 * one was meant.
 *
 * There is deliberately NO decline clause. §12.15 appends §21.4's clause when
 * a bool member was provided false; the analogous input here is the opposite
 * fact, because inside an update command `--no-proxied` PROVIDES the property
 * with the value false (§27.7), so it satisfies this rule rather than
 * declining it and this sentence cannot fire in its presence.
 */
export function errUpdateNoProperty(
	resource: string,
	properties: string,
): string {
	return `update ${q(resource)}: at least one property is required: ${properties}`;
}

/**
 * COMMAND LINE only: it is a collision between two tokens, and the machine
 * doors have one key per property (§27.6), so no door can reach the state.
 */
export function errUpdateValueAndUnset(x: string): string {
	return `--${x} and --unset-${x} are mutually exclusive: a property is either written or cleared`;
}
