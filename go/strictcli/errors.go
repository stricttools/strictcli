package strictcli

import (
	"fmt"
	"strings"
)

// errors.go centralizes all error/panic format templates used across the
// strictcli package. Functions are grouped by their original source file for
// traceability. Message strings are byte-identical to the originals — this is
// a pure extraction, not a rewrite.

// ---------------------------------------------------------------------------
// strictcli.go — type constructors (ListOf, DictOf)
// ---------------------------------------------------------------------------

func errListOfBadItemType(itemType FlagType) string {
	return fmt.Sprintf("ListOf: item type must be str, int, or float, got %d", itemType)
}

func errDictOfBadValueType(valueType FlagType) string {
	return fmt.Sprintf("DictOf: value type must be str, int, or float, got %d", valueType)
}

// ---------------------------------------------------------------------------
// strictcli.go — option constructors (WithConfigConflictMode, etc.)
// ---------------------------------------------------------------------------

func errWithConfigConflictModeBadMode(mode string) string {
	return fmt.Sprintf("WithConfigConflictMode: mode must be \"cli-wins\" or \"error\", got %q", mode)
}

func errHandshakeEnvVarEmptyHelp(envVar string) string {
	return fmt.Sprintf("handshake env var %q: help must be a non-empty string", envVar)
}

func errDuplicateHandshakeEnvVar(envVar string) string {
	return fmt.Sprintf("duplicate handshake env var %q", envVar)
}

func errConnectionEnvVarEmptyHelp(envVar string) string {
	return fmt.Sprintf("connection env var %q: help must be a non-empty string", envVar)
}

func errDuplicateConnectionEnvVar(envVar string) string {
	return fmt.Sprintf("duplicate connection env var %q", envVar)
}

func errConnectionEnvIsAlreadyInfraRoot(ev string) string {
	return fmt.Sprintf("connection env var %q is already declared as an infra root", ev)
}

func errConnectionEnvIsAlreadyHandshake(ev string) string {
	return fmt.Sprintf("connection env var %q is already declared as a handshake env var", ev)
}

func errConnectionURLFlagUnbound(flagName string) string {
	return fmt.Sprintf("flag %q: connection-URL flag must bind to a declared connection env", flagName)
}

func errConnectionEnvWithoutURLFlag(flagName string) string {
	return fmt.Sprintf("flag %q: connection env binding requires the flag to be marked as a connection-URL flag", flagName)
}

func errConnectionEnvWithPerFlagEnv(flagName string) string {
	return fmt.Sprintf("flag %q: a connection-URL binding cannot be combined with a per-flag env var", flagName)
}

func errFlagConnectionEnvUndeclared(flagName string, envVar string) string {
	return fmt.Sprintf("flag %q: connection-URL flag binds to undeclared connection env %q; declare it as a connection env", flagName, envVar)
}

func errConnectionValueUndeclared(envVar string) string {
	return fmt.Sprintf("ConnectionEnvValue: %q is not a declared connection env var", envVar)
}

func errConflictModeBadMode(mode string) string {
	return fmt.Sprintf("ConflictMode: mode must be \"cli-wins\" or \"error\", got %q", mode)
}

// ---------------------------------------------------------------------------
// strictcli.go — tag validation
// ---------------------------------------------------------------------------

func errInvalidTagName(t string) string {
	return fmt.Sprintf("invalid tag name %q: must match [a-z][a-z0-9-]*", t)
}

// ---------------------------------------------------------------------------
// strictcli.go — NewArg validation
// ---------------------------------------------------------------------------

const errArgHelpEmpty = "Arg.help must be a non-empty string"

func errArgListTypeRequiresVariadic(name string) string {
	return fmt.Sprintf("Arg %q: list type requires variadic=true", name)
}

func errArgListItemTypeBad(name string) string {
	return fmt.Sprintf("Arg %q: list item type must be str, int, or float", name)
}

func errArgDictTypeNotSupported(name string) string {
	return fmt.Sprintf("Arg %q: dict type is not supported on positional arguments", name)
}

func errArgTypeBad(t FlagType) string {
	return fmt.Sprintf("Arg.type must be str, bool, int, or float, got %d", t)
}

func errArgChoicesIncompatibleBool(name string) string {
	return fmt.Sprintf("Arg %q: choices is incompatible with type=bool", name)
}

// errFlagChoiceMagnitude / errArgChoiceMagnitude are §12.14's guard, one
// condition on two surfaces. The published `value_schema` fragment carries a
// declaration's choices as a JSON Schema `enum`, and a reader that parses JSON
// numbers as IEEE-754 doubles -- which every reader of a dumped schema is
// entitled to be -- reads a DIFFERENT integer back, so the framework refuses
// the declaration rather than publishing a fragment it knows will be misread.
//
// The clause after the colon is reused byte-for-byte from the payload regime's
// own magnitude guard: the same condition at a second boundary, and a second
// wording for one fact is what the reuse rule prevents. Float choices are
// deliberately exempt -- the canonical float form is by construction the
// shortest string that round-trips to the identical double.
func errFlagChoiceMagnitude(name string, v int) string {
	return fmt.Sprintf("Flag %q: choice %d: %s", name, v, pdetailMagnitude)
}

func errArgChoiceMagnitude(name string, v int) string {
	return fmt.Sprintf("Arg %q: choice %d: %s", name, v, pdetailMagnitude)
}

func errArgChoicesEmpty(name string) string {
	return fmt.Sprintf("Arg %q: choices must be a non-empty list", name)
}

func errArgChoiceTypeMismatch(name string, c interface{}, typeName string) string {
	return fmt.Sprintf("Arg %q: choice %v is not of type %s", name, c, typeName)
}

func errArgStrDefaultTypeMismatch(name string, gotType string) string {
	return fmt.Sprintf("Arg %q: type=str requires a str default, got '%s'", name, gotType)
}

func errArgIntDefaultTypeMismatch(name string, gotType string) string {
	return fmt.Sprintf("Arg %q: type=int requires an int default, got '%s'", name, gotType)
}

func errArgFloatDefaultTypeMismatch(name string, gotType string) string {
	return fmt.Sprintf("Arg %q: type=float requires a float default, got '%s'", name, gotType)
}

func errArgBoolDefaultTypeMismatch(name string, gotType string) string {
	return fmt.Sprintf("Arg %q: type=bool requires a bool default, got '%s'", name, gotType)
}

func errArgDefaultNotInChoices(name string, dflt interface{}, choicesStr string) string {
	return fmt.Sprintf("Arg %q: default '%v' is not in choices [%s]", name, dflt, choicesStr)
}

// ---------------------------------------------------------------------------
// strictcli.go — retired choices
//
// The value-level twin of the deprecated-command construct: a spelling a
// declaration used to accept, carrying the message that names its replacement.
// Every sentence below names the CONCEPT ("retired choice") rather than a
// declaration spelling, so all three implementations share one signature and
// the parity checker needs no per-language exclusion for the family.
//
// Values are interpolated pre-formatted through each implementation's own
// error-value formatter, which is what keeps an int, a float and a string
// rendering identically in all three.
// ---------------------------------------------------------------------------

func errFlagRetiredChoiceIsLive(name string, value string) string {
	return fmt.Sprintf(
		"Flag %q: retired choice '%s' is also a live choice: a value is live or retired, never both",
		name, value,
	)
}

func errArgRetiredChoiceIsLive(name string, value string) string {
	return fmt.Sprintf(
		"Arg %q: retired choice '%s' is also a live choice: a value is live or retired, never both",
		name, value,
	)
}

func errFlagRetiredChoiceDuplicate(name string, value string) string {
	return fmt.Sprintf("Flag %q: retired choice '%s' is declared twice", name, value)
}

func errArgRetiredChoiceDuplicate(name string, value string) string {
	return fmt.Sprintf("Arg %q: retired choice '%s' is declared twice", name, value)
}

func errFlagRetiredChoiceMessageEmpty(name string, value string) string {
	return fmt.Sprintf("Flag %q: retired choice '%s': message must be a non-empty string", name, value)
}

func errArgRetiredChoiceMessageEmpty(name string, value string) string {
	return fmt.Sprintf("Arg %q: retired choice '%s': message must be a non-empty string", name, value)
}

func errFlagRetiredChoicesIncompatibleBool(name string) string {
	return fmt.Sprintf("Flag %q: retired choices are incompatible with type=bool", name)
}

func errArgRetiredChoicesIncompatibleBool(name string) string {
	return fmt.Sprintf("Arg %q: retired choices are incompatible with type=bool", name)
}

func errFlagDefaultIsRetiredChoice(name string, value string) string {
	return fmt.Sprintf("Flag %q: default '%s' is a retired choice", name, value)
}

func errArgDefaultIsRetiredChoice(name string, value string) string {
	return fmt.Sprintf("Arg %q: default '%s' is a retired choice", name, value)
}

func errFlagRetiredChoicesRequireChoices(name string) string {
	return fmt.Sprintf("Flag %q: retired choices require choices", name)
}

func errArgRetiredChoicesRequireChoices(name string) string {
	return fmt.Sprintf("Arg %q: retired choices require choices", name)
}

// ---------------------------------------------------------------------------
// strictcli.go — validateFlagConfig
// ---------------------------------------------------------------------------

const errFlagHelpEmpty = "Flag.help must be a non-empty string"

const errFlagForceReserved = "flag 'force' is a reserved name; use a qualified name like 'force-overwrite' or 'force-delete'"

func errFlagNoPrefixReserved(name string) string {
	return fmt.Sprintf("flag '%s': names starting with 'no-' are reserved for the negation system; use a positive name instead", name)
}

func errFlagRepeatableIncompatibleBool(name string) string {
	return fmt.Sprintf("Flag %q: repeatable is incompatible with type=bool", name)
}

// errFlagDictCannotCombineChoices is the compound refusal narrowed to the one
// carrier it is true of (§25.5): a list carrier's choices constrain its
// elements and publish as an enum inside `items`, but a dict's keys are
// structurally strings and its values are not a closed set.
func errFlagDictCannotCombineChoices(name string) string {
	return fmt.Sprintf("Flag %q: dict type cannot be combined with choices", name)
}

func errFlagRepeatableRequiresExplicitUnique(name string) string {
	return fmt.Sprintf("Flag %q: repeatable requires explicit unique (unique=True or unique=False)", name)
}

func errFlagUniqueRequiresRepeatable(name string) string {
	return fmt.Sprintf("Flag %q: unique requires repeatable=True", name)
}

func errFlagEnvSeparatorRequiresRepeatable(name string) string {
	return fmt.Sprintf("Flag %q: env_separator requires repeatable=True", name)
}

func errFlagEnvSeparatorRequiresEnv(name string) string {
	return fmt.Sprintf("Flag %q: env_separator requires env", name)
}

func errFlagRepeatableEnvRequiresSeparator(name string) string {
	return fmt.Sprintf("Flag %q: repeatable flag with env requires env_separator", name)
}

func errFlagEnvSeparatorSingleChar(name string) string {
	return fmt.Sprintf("Flag %q: env_separator must be a single character", name)
}

func errFlagEnvSeparatorBackslash(name string) string {
	return fmt.Sprintf("Flag %q: env_separator cannot be a backslash", name)
}

func errFlagChoicesIncompatibleBool(name string) string {
	return fmt.Sprintf("Flag %q: choices is incompatible with type=bool", name)
}

func errFlagChoicesEmpty(name string) string {
	return fmt.Sprintf("Flag %q: choices must be a non-empty list", name)
}

func errFlagChoiceTypeMismatch(name string, c interface{}, typeName string) string {
	return fmt.Sprintf("Flag %q: choice %v is not of type %s", name, c, typeName)
}

func errFlagIntDefaultTypeMismatch(name string, gotType string) string {
	return fmt.Sprintf("Flag %q: type=int requires an int default, got '%s'", name, gotType)
}

func errFlagFloatDefaultTypeMismatch(name string, gotType string) string {
	return fmt.Sprintf("Flag %q: type=float requires a float default, got '%s'", name, gotType)
}

func errFlagDictDefaultMustBeMap(name string) string {
	return fmt.Sprintf("Flag %q: dict flag default must be a map[string]interface{}", name)
}

func errFlagDefaultValueForKey(name string, k string, errStr string) string {
	return fmt.Sprintf("Flag %q: default value for key %q: %s", name, k, errStr)
}

func errFlagListDefaultMustBeSlice(name string) string {
	return fmt.Sprintf("Flag %q: list flag default must be a []interface{}", name)
}

func errFlagDefaultElementError(name string, i int, errStr string) string {
	return fmt.Sprintf("Flag %q: default element %d: %s", name, i, errStr)
}

func errFlagRepeatableDefaultMustBeList(name string) string {
	return fmt.Sprintf("Flag %q: repeatable flag default must be a list", name)
}

func errFlagDefaultElementTypeMismatch(name string, i int, typeName string) string {
	return fmt.Sprintf("Flag %q: default element %d is not of type %s", name, i, typeName)
}

func errFlagDefaultNotInChoices(name string, dflt interface{}, choicesStr string) string {
	return fmt.Sprintf("Flag %q: default '%v' is not in choices [%s]", name, dflt, choicesStr)
}

// ---------------------------------------------------------------------------
// strictcli.go — the presence declaration (contract §12.12, §23)
//
// Every template here is registration-time. The sentence is byte-identical
// across the three implementations and the SPELLINGS inside it are Go's own:
// Required() / Optional() / Default(<value>) for flags, ArgRequired() /
// ArgOptional() / ArgDefault(<value>) for args.
// ---------------------------------------------------------------------------

func errFlagPresenceUndeclared(name string) string {
	return fmt.Sprintf("Flag %q: presence is undeclared: declare exactly one of Required(), Optional(), or Default(<value>)", name)
}

func errArgPresenceUndeclared(name string) string {
	return fmt.Sprintf("Arg %q: presence is undeclared: declare exactly one of ArgRequired(), ArgOptional(), or ArgDefault(<value>)", name)
}

func errFlagPresenceDeclaredTwice(name, first, second string) string {
	return fmt.Sprintf("Flag %q: presence is declared twice: %s and %s cannot be combined; declare exactly one", name, first, second)
}

func errArgPresenceDeclaredTwice(name, first, second string) string {
	return fmt.Sprintf("Arg %q: presence is declared twice: %s and %s cannot be combined; declare exactly one", name, first, second)
}

func errFlagDefaultNullNotOptional(name string) string {
	return fmt.Sprintf("Flag %q: Default(nil) does not declare optionality: use Optional() (it delivers nil when the flag is absent)", name)
}

func errArgDefaultNullNotOptional(name string) string {
	return fmt.Sprintf("Arg %q: ArgDefault(nil) does not declare optionality: use ArgOptional() (it delivers nil when the arg is absent)", name)
}

// errFlagMutexMemberRequired is DELETED with MutexGroup (contract §21's
// supersession box, §18.15 item 178). The rule INVERTED: a member flag now MUST
// declare Required(), read as "required once this member is elected", which is
// errMemberFlagPresence. The two state opposite rules about the same
// declaration, so the old template is deleted rather than reworded.

func errArgVariadicDefault(name string) string {
	return fmt.Sprintf("Arg %q: a variadic arg cannot declare ArgDefault(): it always delivers a list, so declare ArgRequired() for at least one value or ArgOptional() for possibly none", name)
}

// ---------------------------------------------------------------------------
// strictcli.go — NewApp
// ---------------------------------------------------------------------------

const errAppHelpEmpty = "App.help must be a non-empty string"

func errDuplicateInfraRootEnvVar(envVar string) string {
	return fmt.Sprintf("duplicate infra root env var %q", envVar)
}

func errHandshakeIsAlreadyInfraRoot(ev string) string {
	return fmt.Sprintf("handshake env var %q is already declared as an infra root", ev)
}

const errCannotUseBothChecksAndEmbed = "cannot use both WithChecks and WithChecksEmbed"

func errChecksPathNotExist(path string) string {
	return fmt.Sprintf("checks_path does not exist: %s", path)
}

func errChecksTomlAppMismatch(appName string, expected string) string {
	return fmt.Sprintf("checks.toml: app %q does not match app name %q", appName, expected)
}

// errTestCoverageBooleanRetired is the retired boolean option's refusal
// (contract §12.12: one sentence, each language's own spellings inside it --
// WithTestCoverage / WithTestCoverageDir here, test_coverage /
// test_coverage_dir in Python, testCoverage / testCoverageDir in TypeScript).
const errTestCoverageBooleanRetired = "WithTestCoverage is not accepted; declare the directory holding coverage/ and test-coverage.json with WithTestCoverageDir"

// ---------------------------------------------------------------------------
// strictcli.go — check registration
// ---------------------------------------------------------------------------

func errCannotRegisterCheckNotEnabled(name string) string {
	return fmt.Sprintf("cannot register check %q: checks not enabled", name)
}

func errCannotRegisterCheckNotDeclared(name string) string {
	return fmt.Sprintf("cannot register check %q: not declared in checks.toml", name)
}

func errCheckDuplicateRegistration(name string) string {
	return fmt.Sprintf("check %q: duplicate registration", name)
}

func errCheckSeverityMismatch(name string, severity string, used string, want string) string {
	return fmt.Sprintf(
		"check %q: declared severity %q in checks.toml but registered via %s; use %s",
		name, severity, used, want,
	)
}

// ---------------------------------------------------------------------------
// strictcli.go — TagContract
// ---------------------------------------------------------------------------

// errInvalidTagName is reused from the tag validation section above.

// ---------------------------------------------------------------------------
// strictcli.go — validateConfigFieldBindings
// ---------------------------------------------------------------------------

func errCommandConfigFieldsUnknownField(cmdName string, field string) string {
	return fmt.Sprintf("command %q: config_fields references unknown config field %q", cmdName, field)
}

// ---------------------------------------------------------------------------
// strictcli.go — checkFlagConfigFieldDefault
// ---------------------------------------------------------------------------

func errConfigFieldFlagDefaultDisagree(cfName string, flagName string, cfDefault interface{}, flagDefault interface{}) string {
	return fmt.Sprintf(
		"config field %q collides with flag %q but their defaults disagree (%v vs %v); remove one default or make them equal",
		cfName, flagName, cfDefault, flagDefault,
	)
}

// ---------------------------------------------------------------------------
// strictcli.go — resolveInfraRootPath
// ---------------------------------------------------------------------------

func errRelativeToRootUndeclared(envVar string) error {
	return fmt.Errorf("RelativeToRoot references undeclared infra root %q; declare it as an infra root", envVar)
}

// ---------------------------------------------------------------------------
// strictcli.go — validateFlagInfraMarker
// ---------------------------------------------------------------------------

func errFlagRelativeToRootUndeclared(flagName string, envVar string) string {
	return fmt.Sprintf("flag %q: RelativeToRoot references undeclared infra root %q; declare it as an infra root", flagName, envVar)
}

// ---------------------------------------------------------------------------
// strictcli.go — command registration
// ---------------------------------------------------------------------------

func errCommandMissingHelp(name string) string {
	return fmt.Sprintf("command %q: missing help text", name)
}

func errCommandPassthroughCannotHave(name string, parts string) string {
	return fmt.Sprintf("command %q: passthrough commands cannot have %s", name, parts)
}

func errGlobalFlagNameReserved(name string) string {
	return fmt.Sprintf("global flag name %q is reserved", name)
}

func errGlobalShortFlagReserved(short string) string {
	return fmt.Sprintf("global short flag %q is reserved", short)
}

func errDuplicateGlobalFlag(name string) string {
	return fmt.Sprintf("duplicate global flag name %q", name)
}

const errGroupHelpEmpty = "Group.help must be a non-empty string"

func errGroupCollidesWithCommand(name string) string {
	return fmt.Sprintf("group %q collides with an existing command", name)
}

func errGroupAlreadyRegistered(name string) string {
	return fmt.Sprintf("group %q is already registered", name)
}

func errCommandCollidesWithGroup(name string) string {
	return fmt.Sprintf("command %q collides with an existing group", name)
}

const errDeprecatedNameEmpty = "deprecated command name must be a non-empty string"

func errDeprecatedMessageEmpty(name string) string {
	return fmt.Sprintf("deprecated command %q: message must not be empty", name)
}

func errDeprecatedCollidesCommand(name string) string {
	return fmt.Sprintf("deprecated command %q collides with an existing command", name)
}

func errDeprecatedCollidesGroup(name string) string {
	return fmt.Sprintf("deprecated command %q collides with an existing group", name)
}

func errDeprecatedAlreadyRegistered(name string) string {
	return fmt.Sprintf("deprecated command %q is already registered", name)
}

// ---------------------------------------------------------------------------
// strictcli.go — buildAndValidateCommand
// ---------------------------------------------------------------------------

func errCommandFlagCollidesGlobal(name string, flagName string) string {
	return fmt.Sprintf("command %q: flag %q collides with a global flag", name, flagName)
}

func errCommandDuplicateFlag(name string, flagName string) string {
	return fmt.Sprintf("command %q: duplicate flag name %q", name, flagName)
}

func errCommandDuplicateArg(name string, argName string) string {
	return fmt.Sprintf("command %q: duplicate arg name %q", name, argName)
}

func errCommandAtMostOneVariadic(name string) string {
	return fmt.Sprintf("command %q: at most one variadic arg is allowed", name)
}

func errCommandVariadicMustBeLast(name string, argName string) string {
	return fmt.Sprintf("command %q: variadic arg %q must be the last arg", name, argName)
}

func errCommandFlagMissingHelp(name string, flagName string) string {
	return fmt.Sprintf("command %q: flag %q missing help text", name, flagName)
}

func errCommandEnvVarPrefix(name string, envVar string, flagName string, expectedPrefix string) string {
	return fmt.Sprintf(
		"command %q: env var %q for flag %q must start with %q (or set prefixed=false)",
		name, envVar, flagName, expectedPrefix,
	)
}

func errCommandRequiresSameFlag(name string, flag string) string {
	return fmt.Sprintf("command %q: Requires flag and depends_on cannot be the same (%q)", name, flag)
}

func errCommandImpliesSameFlag(name string, flag string) string {
	return fmt.Sprintf("command %q: Implies flag and implies cannot be the same (%q)", name, flag)
}

func errCommandImpliesTriggerNotBool(name string, flagName string) string {
	return fmt.Sprintf("command %q: Implies trigger flag %q must be a bool flag", name, flagName)
}

func errCommandImpliesTargetNotBool(name string, flagName string) string {
	return fmt.Sprintf("command %q: Implies target flag %q must be a bool flag", name, flagName)
}

// ---------------------------------------------------------------------------
// strictcli.go — validateScalarType (parse-time)
// ---------------------------------------------------------------------------

func errExpectedStrGot(typeDesc string) string {
	return fmt.Sprintf("expected str, got %s", typeDesc)
}

func errExpectedIntGot(typeDesc string) string {
	return fmt.Sprintf("expected int, got %s", typeDesc)
}

func errExpectedFloatGot(typeDesc string) string {
	return fmt.Sprintf("expected float, got %s", typeDesc)
}

func errExpectedBoolGot(typeDesc string) string {
	return fmt.Sprintf("expected bool, got %s", typeDesc)
}

// ---------------------------------------------------------------------------
// strictcli.go — doParse hermetic mode (parse-time)
// ---------------------------------------------------------------------------

const errHermeticConfigMutuallyExclusive = "--hermetic and --config are mutually exclusive"

const errHermeticWithConfigCommands = "--hermetic cannot be used with config commands"

// ---------------------------------------------------------------------------
// parse.go — strict parsing
// ---------------------------------------------------------------------------

func errExpectedBoolean(s string) error {
	return fmt.Errorf("expected boolean, got '%s'", s)
}

func errExpectedInteger(s string) error {
	return fmt.Errorf("expected integer, got '%s'", s)
}

func errExpectedFloat(s string) error {
	return fmt.Errorf("expected float, got '%s'", s)
}

func errNaNNotAllowed() error {
	return fmt.Errorf("NaN is not allowed")
}

func errInfNotAllowed() error {
	return fmt.Errorf("Inf is not allowed")
}

// ---------------------------------------------------------------------------
// parse.go — resolveAtPrefix @-prefix resolution (parse-time)
// ---------------------------------------------------------------------------

func errAtPrefixStdinOnce(flagName string) string {
	return fmt.Sprintf("--%s: stdin (@-) can only be used once per invocation", flagName)
}

func errAtPrefixCannotReadStdin(flagName string) string {
	return fmt.Sprintf("--%s: cannot read stdin", flagName)
}

func errAtPrefixFileTooLarge(flagName string) string {
	return fmt.Sprintf("--%s: file exceeds 1 MB limit", flagName)
}

func errAtPrefixFileNotFound(flagName string, path string) string {
	return fmt.Sprintf("--%s: file not found: %s", flagName, path)
}

func errAtPrefixCannotReadFile(flagName string, path string) string {
	return fmt.Sprintf("--%s: cannot read file: %s", flagName, path)
}

// ---------------------------------------------------------------------------
// parse.go / strictcli.go — flag token parsing (parse-time)
// (parseCommand and extractGlobalFlags share these templates)
// ---------------------------------------------------------------------------

func errBoolFlagNoValue(flagPart string) string {
	return fmt.Sprintf("flag '%s' is a boolean flag and does not take a value", flagPart)
}

func errBoolNegationNoValue(flagPart string) string {
	return fmt.Sprintf("flag '%s' is a boolean negation and does not take a value", flagPart)
}

func errUnknownFlag(tok string) string {
	return fmt.Sprintf("unknown flag '%s'", tok)
}

func errFlagRequiresValue(tok string) string {
	return fmt.Sprintf("flag '%s' requires a value", tok)
}

func errFlagDuplicateValue(flagName string, value string) string {
	return fmt.Sprintf("--%s: duplicate value '%s'", flagName, value)
}

// ---------------------------------------------------------------------------
// parse.go / strictcli.go — env var resolution (parse-time)
// (parseCommand and extractGlobalFlags share these templates)
// ---------------------------------------------------------------------------

func errWrappedFromEnvVar(errStr string, envVar string) string {
	return fmt.Sprintf("%s (from env var '%s')", errStr, envVar)
}

func errListFlagEnvRequiresSeparator(flagName string) string {
	return fmt.Sprintf("--%s: list flag with env requires env_separator", flagName)
}

func errFlagDuplicateValueFromEnv(flagName string, value string, envVar string) string {
	return fmt.Sprintf(
		"--%s: duplicate value '%s' (from env var '%s')",
		flagName, value, envVar,
	)
}

func errInvalidBoolEnvValue(envVal string, envVar string, flagName string) string {
	return fmt.Sprintf(
		"invalid boolean value '%s' for env var '%s' (flag '--%s')",
		envVal, envVar, flagName,
	)
}

func errFlagErrFromEnvVar(flagName string, errStr string, envVar string) string {
	return fmt.Sprintf(
		"--%s: %s (from env var '%s')",
		flagName, errStr, envVar,
	)
}

// ---------------------------------------------------------------------------
// parse.go / strictcli.go — config value resolution (parse-time)
// (parseCommand and extractGlobalFlags share these templates)
// ---------------------------------------------------------------------------

func errConfigValueError(flagName string, errStr string) string {
	return fmt.Sprintf("--%s: config value error: %s", flagName, errStr)
}

func errFlagSetInBothAndConfig(flagName string, existingSource string) string {
	return fmt.Sprintf(
		"flag '%s' set in both %s and config; remove one",
		flagName, existingSource,
	)
}

func errConfigValueDuplicate(flagName string, value string) string {
	return fmt.Sprintf("--%s: config value error: duplicate value '%s'", flagName, value)
}

func errFlagSetInBothCliAndConfig(flagName string) string {
	return fmt.Sprintf("flag '%s' set in both cli and config; remove one", flagName)
}

// ---------------------------------------------------------------------------
// parse.go — validateAndBuildKwargs (parse-time)
// (mutex, dependencies, custom validation, positional args)
// ---------------------------------------------------------------------------

func errMutuallyExclusive(setFlags string) string {
	return fmt.Sprintf("%s are mutually exclusive", setFlags)
}

func errOneOfRequired(names string, clause string) string {
	return fmt.Sprintf("one of %s is required%s", names, clause)
}

func errMutexRedundantNegation(declined string, elected string, clause string) string {
	return fmt.Sprintf("%s cannot be combined with --%s%s", declined, elected, clause)
}

func errMutexDeclineClause(name string) string {
	return fmt.Sprintf(" (--no-%s declines an option; it does not choose one)", name)
}

// The four constraint violation sentences all carry the `constraint "<c>": `
// prefix (§12.15): the declared name is the only identifier the reader can carry
// back to the source. `Requires` and `Implies` keep their sentence byte for byte
// after the prefix (§26.13).

func errImpliesConflict(c string, flag string, neg string, target string, explicitNeg string) string {
	return fmt.Sprintf(
		"constraint %q: flag '--%s' implies '--%s%s', but '--%s%s' was explicitly provided",
		c, flag, neg, target, explicitNeg, target,
	)
}

// errAtLeastOneRequired renders the whole member list by §12.15's rule, in
// declaration order. `clause` is §21.4's decline clause verbatim, appended when
// a bool member declaring WhenTrue() was provided as false -- a fact about a
// negated bool, never a claim that this family is exclusivity.
func errAtLeastOneRequired(c string, members string, clause string) string {
	return fmt.Sprintf("constraint %q: at least one of %s is required%s", c, members, clause)
}

// errAllOrNoneTogether lists EVERY member, engaged or not, which is the shipped
// message's behaviour carried over. The noun `flags` is gone: a member may be a
// positional arg or a nested constraint, and the `--` and bare-name spellings
// already say which kind each one is.
func errAllOrNoneTogether(c string, members string) string {
	return fmt.Sprintf("constraint %q: %s must be used together", c, members)
}

func errFlagRequiresFlag(c string, flag string, dependsOn string) string {
	return fmt.Sprintf("constraint %q: flag '--%s' requires '--%s'", c, flag, dependsOn)
}

func errFlagValueError(flagName string, msg string) string {
	return fmt.Sprintf("--%s: %s", flagName, msg)
}

func errMissingRequiredArgument(name string) string {
	return fmt.Sprintf("missing required argument '%s'", name)
}

func errUnexpectedArgument(value string) string {
	return fmt.Sprintf("unexpected argument '%s'", value)
}

// ---------------------------------------------------------------------------
// parse.go — validateChoices (parse-time)
// ---------------------------------------------------------------------------

func errArgInvalidChoice(name string, value string, choices string) string {
	return fmt.Sprintf(
		"argument '%s': invalid value '%v', must be one of: %s",
		name, value, choices,
	)
}

func errFlagInvalidChoice(name string, value string, choices string) string {
	return fmt.Sprintf(
		"--%s: invalid value '%v', must be one of: %s",
		name, value, choices,
	)
}

// The retired-choice refusal, checked before the invalid-value one so a reader
// who typed a spelling that used to work is told what replaced it rather than
// being handed the list it is missing from. The command-level twin is
// "command '<name>' is deprecated: <message>".

func errArgRetiredChoice(name string, value string, message string) string {
	return fmt.Sprintf(
		"argument '%s': value '%v' retired: %s",
		name, value, message,
	)
}

func errFlagRetiredChoice(name string, value string, message string) string {
	return fmt.Sprintf(
		"--%s: value '%v' retired: %s",
		name, value, message,
	)
}

// ---------------------------------------------------------------------------
// config.go — ConfigField registration
// ---------------------------------------------------------------------------

func errConfigFieldNameInvalid(name string) string {
	return fmt.Sprintf("ConfigField name %q is invalid: must match [a-z][a-z0-9_]*(.[a-z][a-z0-9_]*)* (lowercase, dots for sections)", name)
}

func errConfigFieldNameReserved(name string) string {
	return fmt.Sprintf("config field name %q is reserved: names starting with underscore are reserved for framework fields", name)
}

func errConfigFieldHelpRequired(name string) string {
	return fmt.Sprintf("config field %q: help text is required", name)
}

func errConfigFieldTypeBad(t FlagType) string {
	return fmt.Sprintf("ConfigField.type must be str, bool, int, or float, got %d", t)
}

func errDuplicateConfigField(name string) string {
	return fmt.Sprintf("duplicate config field name %q", name)
}

func errConfigFieldConflictsFramework(name string) string {
	return fmt.Sprintf("config field name %q conflicts with framework field", name)
}

// ---------------------------------------------------------------------------
// config.go — framework field registration
// ---------------------------------------------------------------------------

func errFrameworkFieldMustStartUnderscore(name string) string {
	return fmt.Sprintf("framework field name %q must start with underscore", name)
}

func errFrameworkFieldNameInvalid(name string) string {
	return fmt.Sprintf("framework field %q: invalid name, must match [a-z][a-z0-9_]*(.[a-z][a-z0-9_]*)* (lowercase, dots for sections)", name)
}

func errFrameworkFieldHelpRequired(name string) string {
	return fmt.Sprintf("framework field %q: help text is required", name)
}

func errDuplicateFrameworkField(name string) string {
	return fmt.Sprintf("duplicate framework field name %q", name)
}

func errFrameworkFieldConflictsUser(name string) string {
	return fmt.Sprintf("framework field name %q conflicts with user config field", name)
}

// ---------------------------------------------------------------------------
// config.go — validateConfigFieldDefault
// ---------------------------------------------------------------------------

func errConfigFieldDefaultMismatch(name string, value interface{}, typeName string) string {
	return fmt.Sprintf("ConfigField %q: default value %v does not match type %s", name, value, typeName)
}

// ---------------------------------------------------------------------------
// config.go — coerceConfigScalarLong (long type names)
// (the float branch reuses errExpectedFloatGot from the validateScalarType
// section above)
// ---------------------------------------------------------------------------

func errConfigExpectedBooleanGot(typeDesc string) string {
	return fmt.Sprintf("expected boolean, got %s", typeDesc)
}

const errConfigExpectedIntegerGotFloat = "expected integer, got float"

func errConfigExpectedIntegerGot(typeDesc string) string {
	return fmt.Sprintf("expected integer, got %s", typeDesc)
}

func errConfigExpectedStringGot(typeDesc string) string {
	return fmt.Sprintf("expected string, got %s", typeDesc)
}

func errConfigUnsupportedFlagType(t FlagType) string {
	return fmt.Sprintf("unsupported flag type %d", t)
}

// ---------------------------------------------------------------------------
// config.go — coerceConfigScalarShort (short type names)
// (the bool/int/float/str branches reuse errExpectedBoolGot, errExpectedIntGot,
// errExpectedFloatGot, and errExpectedStrGot from the validateScalarType
// section above; the unsupported-type branch reuses errConfigUnsupportedFlagType)
// ---------------------------------------------------------------------------

const errConfigExpectedIntGotFloat = "expected int, got float"

// ---------------------------------------------------------------------------
// config.go — coerceConfigValue (compound config value coercion)
// ---------------------------------------------------------------------------

func errConfigExpectedObjectForDictFlag(typeDesc string) string {
	return fmt.Sprintf("expected object for dict flag, got %s", typeDesc)
}

func errConfigDictKeyTypeMismatch(k string, wantType string, gotType string) string {
	return fmt.Sprintf("key %q: expected %s, got %s", k, wantType, gotType)
}

func errConfigExpectedArrayForListFlag(typeDesc string) string {
	return fmt.Sprintf("expected array for list flag, got %s", typeDesc)
}

func errConfigElementTypeMismatch(i int, wantType string, gotType string) string {
	return fmt.Sprintf("element %d: expected %s, got %s", i, wantType, gotType)
}

const errConfigExpectedScalarGotArray = "expected scalar, got array"

func errConfigExpectedArrayForRepeatableFlag(typeDesc string) string {
	return fmt.Sprintf("expected array for repeatable flag, got %s", typeDesc)
}

// ---------------------------------------------------------------------------
// routing.go — resolveCommand (parse-time)
// ---------------------------------------------------------------------------

func errCommandDeprecated(token string, msg string) string {
	return fmt.Sprintf("command '%s' is deprecated: %s", token, msg)
}

func errUnknownCommandInGroup(token string, groupPath string) string {
	return fmt.Sprintf("unknown command '%s' in '%s'", token, groupPath)
}

func errUnknownCommand(token string) string {
	return fmt.Sprintf("unknown command '%s'", token)
}

const errNoCommandSpecified = "no command specified"

// ---------------------------------------------------------------------------
// invoke.go — invoke (parse-time)
// ---------------------------------------------------------------------------

const errPassthroughArgsNotStringSlice = "passthrough command: _args must be []string"

func errUnknownParameterForPassthroughCommand(key string, commandPath string) string {
	return fmt.Sprintf("unknown parameter %q for passthrough command %q", key, commandPath)
}

func errUnknownParameterForCommand(paramName string, commandPath string) string {
	return fmt.Sprintf("unknown parameter %q for command %q", paramName, commandPath)
}

// ---------------------------------------------------------------------------
// invoke.go — coerceInvokeDict
// ---------------------------------------------------------------------------

func errDictFlagExpectedMapType(name string, value interface{}) string {
	return fmt.Sprintf("dict flag %q: expected map type, got %T", name, value)
}

// ---------------------------------------------------------------------------
// check.go — reporter methods
// ---------------------------------------------------------------------------

const errNoteTextEmpty = "note text must be a non-empty string"

const errProblemTextEmpty = "problem text must be a non-empty string"

const errOutcomeMessageEmpty = "outcome message must be a non-empty string"

const errPassedWithProblems = "problems were reported; a check that found problems cannot pass -- use found instead"

const errSkipReasonEmpty = "skip reason must be a non-empty string"

const errSkippedWithProblems = "problems were reported; a check that found problems cannot skip"

const errFoundNoProblems = "no problems were reported; nothing found means pass -- use passed instead"

// ---------------------------------------------------------------------------
// check.go — deriveStatus
// ---------------------------------------------------------------------------

func errUnknownCheckOutcomeKind(kind string) string {
	return fmt.Sprintf("unknown check outcome kind %q", kind)
}

// ---------------------------------------------------------------------------
// check.go — addCheckDef
// ---------------------------------------------------------------------------

func errDuplicateCheckDef(name string) error {
	return fmt.Errorf("duplicate check definition %q", name)
}

// ---------------------------------------------------------------------------
// check.go — parseChecksToml
// ---------------------------------------------------------------------------

func errChecksTomlParse(err error) error {
	return fmt.Errorf("checks.toml: %s", err)
}

func errChecksTomlUnknownTopLevelKey(key string) error {
	return fmt.Errorf("checks.toml: unknown top-level key %q", key)
}

func errChecksTomlMissingApp() error {
	return fmt.Errorf("checks.toml: missing required top-level key \"app\"")
}

func errChecksTomlAppNotString() error {
	return fmt.Errorf("checks.toml: \"app\" must be a non-empty string")
}

func errChecksTomlChecksMustBeTable() error {
	return fmt.Errorf("checks.toml: [checks] must be a table")
}

func errChecksTomlInvalidCheckName(name string) error {
	return fmt.Errorf("checks.toml: invalid check name %q (must match [a-z][a-z0-9-]*)", name)
}

func errChecksTomlCheckMustBeTable(name string) error {
	return fmt.Errorf("checks.toml: check %q must be a table", name)
}

func errChecksTomlUnknownField(name string, field string) error {
	return fmt.Errorf("checks.toml: check %q: unknown field %q", name, field)
}

func errChecksTomlMissingField(name string, field string) error {
	return fmt.Errorf("checks.toml: check %q: missing required field %q", name, field)
}

func errChecksTomlTagsMustBeStrings(name string) error {
	return fmt.Errorf("checks.toml: check %q: \"tags\" must be a list of strings", name)
}

func errChecksTomlTagsEntriesMustBeStrings(name string) error {
	return fmt.Errorf("checks.toml: check %q: \"tags\" entries must be non-empty strings", name)
}

func errChecksTomlSeverityInvalid(name string, raw interface{}) error {
	return fmt.Errorf("checks.toml: check %q: \"severity\" must be \"error\" or \"warn\", got %q", name, raw)
}

func errChecksTomlBoolFieldInvalid(name string, field string, raw interface{}) error {
	return fmt.Errorf("checks.toml: check %q: %q must be a boolean, got %s", name, field, tomlTypeName(raw))
}

func errChecksTomlDependsOnMustBeStrings(name string) error {
	return fmt.Errorf("checks.toml: check %q: \"depends_on\" must be a list of strings", name)
}

func errChecksTomlDependsOnEntriesMustBeStrings(name string) error {
	return fmt.Errorf("checks.toml: check %q: \"depends_on\" entries must be strings", name)
}

func errChecksTomlScopeMustBeString(name string, raw interface{}) error {
	return fmt.Errorf("checks.toml: check %q: \"scope\" must be a string, got %s", name, tomlTypeName(raw))
}

func errChecksTomlDependsOnUnknown(name string, dep string) error {
	return fmt.Errorf("checks.toml: check %q: depends_on references unknown check %q", name, dep)
}

// ---------------------------------------------------------------------------
// check_runner.go
// ---------------------------------------------------------------------------

func errCheckDependencyCycleInvolving(name string) error {
	return fmt.Errorf("check dependency cycle detected involving %q", name)
}

func errCheckDependencyCycle(cyclePath string) error {
	return fmt.Errorf("check dependency cycle: %s", cyclePath)
}

func errCheckDependencyCycleDetected() error {
	return fmt.Errorf("check dependency cycle detected")
}

func errCheckOutcomeNotMinted(name string) string {
	return fmt.Sprintf("check %q returned an outcome not minted by its reporter; use reporter methods (Passed/Skipped/Found)", name)
}

func errInvalidGlobPattern(pattern string, err error) error {
	return fmt.Errorf("invalid glob pattern %q: %s", pattern, err)
}

// ---------------------------------------------------------------------------
// check_provider.go
// ---------------------------------------------------------------------------

func errCheckProviderSeverityMismatch(name string, severity string, used string, want string) string {
	return fmt.Sprintf(
		"check %q: declared severity %q but registered via %s; use %s",
		name, severity, used, want,
	)
}

// ---------------------------------------------------------------------------
// check_public.go
// ---------------------------------------------------------------------------

func errChecksNotEnabled() error {
	return fmt.Errorf("checks are not enabled on this App")
}

// ---------------------------------------------------------------------------
// schema.go
// ---------------------------------------------------------------------------

func errCannotDetermineProjectIDNoGoMod() error {
	return fmt.Errorf("Cannot determine project_id: go.mod not found")
}

func errCannotDetermineProjectIDReadError(err error) error {
	return fmt.Errorf("Cannot determine project_id: error reading go.mod: %w", err)
}

func errCannotDetermineProjectIDNoModule() error {
	return fmt.Errorf("Cannot determine project_id: no module directive in go.mod")
}

// errSchemaValueUnserializable names a value the canonical writer cannot
// encode. It is a framework bug rather than a user mistake -- every value the
// schema holds comes from a declaration surface whose types are closed.
func errSchemaValueUnserializable(v interface{}) error {
	return fmt.Errorf("schema value of unserializable type: %T", v)
}

func errSchemaMismatch(existingID string, newID string) error {
	return fmt.Errorf(
		"Schema mismatch: existing schema belongs to project '%s', not '%s'. Run from the correct project directory.",
		existingID, newID,
	)
}

// ---------------------------------------------------------------------------
// tagdsl.go
// ---------------------------------------------------------------------------

func errTagExprUnexpectedChar(ch string, pos int) error {
	return fmt.Errorf("tag expression: unexpected character %q at position %d", ch, pos)
}

func errTagExprEmpty() error {
	return fmt.Errorf("tag expression: empty expression")
}

func errTagExprUnexpectedToken(val string, pos int) error {
	return fmt.Errorf("tag expression: unexpected token %q at position %d", val, pos)
}

func errTagExprUnexpectedEnd(pos int) error {
	return fmt.Errorf("tag expression: unexpected end of expression at position %d", pos)
}

func errTagExprExpectedRParen(pos int) error {
	return fmt.Errorf("tag expression: expected \")\" at position %d", pos)
}

// ---------------------------------------------------------------------------
// context.go
// ---------------------------------------------------------------------------

func errInfraValueUndeclared(envVar string) string {
	return fmt.Sprintf("InfraValue: %q is not a declared infra root, handshake, or connection env var", envVar)
}

func errNoSourceInfo(name string) string {
	return fmt.Sprintf("no source info for flag %q", name)
}

// ---------------------------------------------------------------------------
// outcome.go
// ---------------------------------------------------------------------------

func errGetNoSuchKey(name string) string {
	return fmt.Sprintf("strictcli.Get: no such key %q", name)
}

func errGetKeyNil(name string) string {
	return fmt.Sprintf("strictcli.Get: key %q is nil (not provided); use GetOpt for optional values", name)
}

func errGetTypeMismatch(name string, v interface{}, zero interface{}) string {
	return fmt.Sprintf("strictcli.Get: key %q has dynamic type %T, want %T", name, v, zero)
}

func errGetOptNoSuchKey(name string) string {
	return fmt.Sprintf("strictcli.GetOpt: no such key %q", name)
}

func errGetOptTypeMismatch(name string, v interface{}, zero interface{}) string {
	return fmt.Sprintf("strictcli.GetOpt: key %q has dynamic type %T, want %T", name, v, zero)
}

// ---------------------------------------------------------------------------
// tool.go
// ---------------------------------------------------------------------------

func errJsonSchemaRouteError(errMsg string) string {
	return fmt.Sprintf("JsonSchema: %s", errMsg)
}

func errJsonSchemaIsGroup(commandPath string) string {
	return fmt.Sprintf("JsonSchema: '%s' is a group, not a command", commandPath)
}

// ---------------------------------------------------------------------------
// effects.go — the effects regime
//
// Registration-time bans, classification, grants, declared forwarding and the
// §12.4 call-time effect errors. The §12.8 failure family, the §12.5 truncation
// error and the §12.6 confirm protocol each get their own (parse-time) section
// below, matching the contract's category placement. Message strings are
// byte-identical to the Python and TypeScript catalogs.
// ---------------------------------------------------------------------------

// errFlagNameReservedByFramework is the reserved-quartet ban (§12.1). It is
// raised from validateFlagConfig (the same site as the 'force' ban) and
// additionally from the global-flag validation path.
func errFlagNameReservedByFramework(name string) string {
	return fmt.Sprintf("flag name '%s' is reserved by the framework (dry-run, approve-consequential, quiet, verbose)", name)
}

// errFlagNameJSONReserved is the machine-mode flag's ban (§12.1, §19.1).
// --json is framework-owned: it selects machine mode and is delivered on the
// Context, never as a handler kwarg. The ban is the unconditional every-level
// one, exactly as the quartet's is.
const errFlagNameJSONReserved = "flag name 'json' is reserved by the framework: --json selects machine mode"

// errFlagNameYesBanned is the outright `yes` ban (§12.1). `yes` owns no
// framework flag any more -- --approve-consequential replaced --yes -- but a
// private --yes would restate it in a spelling that IS muscle memory, which is
// exactly what the rename removed.
const errFlagNameYesBanned = "flag name 'yes' is banned by the framework: the confirmation skip is --approve-consequential"

// errFlagNameConsentReserved and errArgNameConsentReserved are the consent
// PARAMETER ban (§12.1). `approve_consequential` is how a caller states
// consent programmatically and over MCP, so a command may not declare a flag
// or a positional arg of that name -- otherwise the same command means
// different things on different channels.
const errFlagNameConsentReserved = "flag name 'approve_consequential' is reserved by the framework: it names the programmatic consent parameter"

const errArgNameConsentReserved = "arg name 'approve_consequential' is reserved by the framework: it names the programmatic consent parameter"

func errCommandEffectMissing(name string) string {
	return fmt.Sprintf("command %q: effect classification is required (effect=\"read_only\" or effect=\"mutating\")", name)
}

func errCommandEffectInvalid(name string, value string) string {
	return fmt.Sprintf("command %q: invalid effect %q: must be \"read_only\" or \"mutating\"", name, value)
}

func errDeprecatedCommandEffect(name string) string {
	return fmt.Sprintf("deprecated command %q: effect classification does not apply (a deprecated command has no handler)", name)
}

// errCommandReadOnlyConsequential is §8.1's declaration guard: classification
// answers "should a dry run record rather than execute?" and consequential
// answers "are these effects worth interrupting someone for?". A command that
// changes nothing has no effects to weigh.
func errCommandReadOnlyConsequential(name string) string {
	return fmt.Sprintf("command %q: a read_only command cannot be consequential (a command that changes nothing has nothing to confirm)", name)
}

// errCommandReadOnlyDryRunUnsupported mirrors the guard above for the dry-run
// declaration: a command that changes nothing records nothing, so a preview of
// it can never be dishonest and there is no reason to refuse one.
func errCommandReadOnlyDryRunUnsupported(name string) string {
	return fmt.Sprintf("command %q: a read_only command cannot declare dry_run_supported=false (a command that changes nothing has no effects a preview could misrepresent)", name)
}

// errCommandDryRunReasonMissing is the mandatory-reason guard. The reason is
// shown in help and in the parse-time refusal, so a declaration without one
// leaves an operator staring at a refusal with no explanation.
func errCommandDryRunReasonMissing(name string) string {
	return fmt.Sprintf("command %q: dry_run_supported=false requires a non-empty dry_run_unsupported_reason (say what a preview cannot honestly show)", name)
}

// errCommandDryRunReasonWithoutDeclaration is the orphan-reason guard.
// WithDryRunUnsupported always sets both fields, but Command's fields are
// exported and CmdOption is a plain func(*Command), so a caller can reach the
// reason-without-declaration state directly. Silently ignoring it would leave
// an author believing --dry-run is refused when it is honored.
func errCommandDryRunReasonWithoutDeclaration(name string) string {
	return fmt.Sprintf("command %q: dry_run_unsupported_reason requires dry_run_supported=false (there is nothing to explain while dry run is supported)", name)
}

// errHandlerVarKeywordUndeclared exists for catalog parity only. Guard v2's
// ENFORCEMENT is Python-only: a Go handler takes map[string]interface{}, which
// carries no var-keyword parameter to introspect. The declaration
// (WithForwarding) exists in all three so the API surface stays in parity.
func errHandlerVarKeywordUndeclared(name string) string {
	return fmt.Sprintf("command %q: handler accepts **kwargs but the command does not declare forwarding; add forwarding=Forwarding(reason=...) or name every parameter explicitly", name)
}

func errForwardingReasonEmpty(name string) string {
	return fmt.Sprintf("command %q: forwarding reason must be a non-empty string", name)
}

func errFrameworkInternalHandlerForeign(name string) string {
	return fmt.Sprintf("command %q: handler is marked framework-internal but is not defined in the strictcli module", name)
}

// --- grant declarations (registration-time) ---

func errGrantReasonEmpty(name string, grant string) string {
	return fmt.Sprintf("command %q: grant '%s' reason must be a non-empty string", name, grant)
}

func errGrantDuplicate(name string, grant string) string {
	return fmt.Sprintf("command %q: duplicate grant '%s'", name, grant)
}

func errGrantNameInvalid(name string, grant string) string {
	return fmt.Sprintf("command %q: invalid grant name '%s': must match [a-z][a-z0-9-]*", name, grant)
}

func errGrantKindInvalid(name string, grant string, kind string) string {
	return fmt.Sprintf("command %q: grant '%s' has invalid kind '%s': must be one of proc_mutate, proc_spawn, file_write, net_mutate", name, grant, kind)
}

const errProcObserveAllowlistEmptyPrefix = "proc_observe_allowlist entries must not be empty"

// --- effect call-time errors ---

func errEffectMutatingInReadOnly(name string, method string) string {
	return fmt.Sprintf("command %q is classified read_only; effects.%s is a mutating operation", name, method)
}

func errEffectRunNotAllowlisted(name string, argv string) string {
	return fmt.Sprintf("command %q is classified read_only; effects.run argv %s is not on the app's proc_observe_allowlist", name, argv)
}

func errEffectGrantUndeclared(name string, grant string) string {
	return fmt.Sprintf("command %q: grant '%s' is not declared on this command", name, grant)
}

func errEffectGrantKindMismatch(name string, grant string, declaredKind string, usedKind string) string {
	return fmt.Sprintf("command %q: grant '%s' is declared for kind %s but was used for a %s effect", name, grant, declaredKind, usedKind)
}

func errEffectGrantOnObserve(name string, grant string) string {
	return fmt.Sprintf("command %q: grant '%s' cannot be used on an observe (an allowlisted effects.run changes nothing)", name, grant)
}

// ---------------------------------------------------------------------------
// effects.go — effect failure and parameter rejection (parse-time)
//
// Contract §12.8. These reach a handler's effect call through argv like any
// parse-time error, so they share that category and are coverage-checked by
// conformance cases.
// ---------------------------------------------------------------------------

func errEffectRunFailed(name string, method string, argv string, code int) string {
	return fmt.Sprintf("command %q: effects.%s failed: %s exited %d", name, method, argv, code)
}

func errEffectHTTPFailed(name string, httpMethod string, url string, status int) string {
	return fmt.Sprintf("command %q: effects.http failed: %s %s returned %d", name, httpMethod, url, status)
}

func errEffectOutputNotUTF8(name string, method string) string {
	return fmt.Sprintf("command %q: effects.%s produced output that is not valid UTF-8", name, method)
}

func errEffectParamRejectsCarrier(name string, method string, param string) string {
	return fmt.Sprintf("command %q: effects.%s parameter '%s' does not accept an unsettled value", name, method, param)
}

func errEffectOptionNotAccepted(name string, method string, opt string) string {
	return fmt.Sprintf("command %q: effects.%s does not accept option '%s'", name, method, opt)
}

// ---------------------------------------------------------------------------
// effects.go — effect argument type guards and handle availability
//
// Contract §12.10. Registration-time in the parity taxonomy: they are argument
// guards, not the §12.8 failure family. Go's static typing makes three of the
// family's members inexpressible (argv is []any, mode is int, the HTTP method
// is string), which check_error_parity.py records as exclusions.
// ---------------------------------------------------------------------------

func errEffectParamType(name string, method string, param string, got string) string {
	return fmt.Sprintf("command %q: effects.%s parameter '%s' must be a string, a path, or a forwarded effect result; got %s", name, method, param, got)
}

func errEffectArgvEmpty(name string, method string) string {
	return fmt.Sprintf("command %q: effects.%s argv must not be empty", name, method)
}

const errEffectsUnavailable = "ctx.Effects() is unavailable: this Context was constructed outside a command dispatch"

// ---------------------------------------------------------------------------
// context.go — the payload API's call-time errors (§19.4)
// ---------------------------------------------------------------------------

// errPayloadNoSchema fires when a handler calls ctx.payload on a command that
// declares no payload schema. Registration cannot see that a handler intends
// to call it, so call time is the earliest honest point at which the missing
// declaration can be named.
func errPayloadNoSchema(name string) string {
	return fmt.Sprintf("command %q: ctx.payload requires a declared payload schema", name)
}

// errPayloadAlreadySet fires on a second payload call in one dispatch. Two
// payloads are two answers to a question with one slot; picking either
// silently is the kind of guess this regime does not make.
func errPayloadAlreadySet(name string) string {
	return fmt.Sprintf("command %q: ctx.payload was already called (a dispatch carries at most one payload)", name)
}

// errPayloadSchemaInvalid fires at registration when a declared payload schema
// leaves the closed subset (§19.5). path names the position inside the
// declared literal (rooted at payload_schema) and detail names the violated
// rule; both are byte-identical across the three implementations, pinned by
// conformance/payload_schema_vectors.json.
func errPayloadSchemaInvalid(name, path, detail string) string {
	return fmt.Sprintf("command %q: payload schema is invalid at %s: %s", name, path, detail)
}

// errPayloadInvalid fires at emission when a payload deviates from its
// declared schema (§19.5). path names the position inside the value (rooted at
// payload) and detail names the violated constraint, so a wrong shape fails
// here instead of shipping.
func errPayloadInvalid(name, path, detail string) string {
	return fmt.Sprintf("command %q: payload does not satisfy the declared schema at %s: %s", name, path, detail)
}

// ---------------------------------------------------------------------------
// effects.go — dry-run truncation (parse-time)
//
// The template carries its own "error: " prefix: it is written straight to
// stderr, not through the parse-error formatter.
// ---------------------------------------------------------------------------

func errDryRunTruncated(step int, cmd string, brand string) string {
	return fmt.Sprintf("error: dry-run preview ends at step %d: %s branched on unsettled value %s — cannot preview past this point", step, cmd, brand)
}

// ---------------------------------------------------------------------------
// effects.go — an aborted dry-run preview (parse-time)
//
// Same shape and prefix as the truncation error above: both say the preview
// ended before the handler finished, and they differ only in why and in what
// the reader may conclude. Written straight to stderr.
// ---------------------------------------------------------------------------

func errDryRunAborted(step int, cmd string) string {
	return fmt.Sprintf("error: dry-run preview ends at step %d: %s aborted — the preview above may be incomplete", step, cmd)
}

// ---------------------------------------------------------------------------
// effects.go — the confirm protocol (parse-time)
// ---------------------------------------------------------------------------

func promptConfirmConsequential(name string) string {
	return fmt.Sprintf("about to run consequential command '%s'. Proceed? [y/N] ", name)
}

// errConfirmNonInteractive is the non-interactive refusal (contract §8.3).
//
// It names what is required -- confirmation, at a terminal -- and never the
// token that lifts the requirement. A refusal that prints its own override is
// not a seam: the reflex it teaches is to append the override and re-run, which
// is the opposite of the judgement the declaration asks for.
const errConfirmNonInteractive = "error: stdin is not interactive; a consequential command must be confirmed at a terminal"

const errConfirmDeclined = "aborted"

// errCallConsequentialUnconsented is the programmatic-path refusal (contract
// §8.5).
//
// Requiring confirmation is a property of the COMMAND, so every channel has to
// honour it -- but a programmatic caller has no terminal to prompt. The refusal
// makes the caller state, in the call, that it is proceeding without a human,
// instead of the framework deciding that silently on its behalf.
func errCallConsequentialUnconsented(cmdPath string) string {
	return fmt.Sprintf("command '%s' is consequential: the call must carry confirmation", cmdPath)
}

// ---------------------------------------------------------------------------
// strictcli.go — doParse dry-run refusal (parse-time)
//
// Raised for a command that declares dry_run_supported=false: rather than
// render a preview that would misrepresent what running it does, the framework
// refuses the flag and repeats the declared reason.
// ---------------------------------------------------------------------------

func errDryRunNotSupported(cmdPath string, reason string) string {
	return fmt.Sprintf("--dry-run is not supported by command '%s': %s", cmdPath, reason)
}

// ---------------------------------------------------------------------------
// selector.go / parse.go — the scoped-selector construct (contract §12.13, §24)
//
// The family splits across both parity categories, and the split is pinned
// rather than derived: the ELECTION, SCOPE and DELIVERY templates are
// parse-time (stderr, exit 1); the DECLARATION GUARDS are registration-time, in
// §12.10's and §12.12's class. Templates that name a spelling carry Go's own
// noun phrase inside a byte-identical sentence -- Required(), Optional(),
// Default(<value>), Ch(<value>, "<help>"), ChoiceFlag(...),
// MemberChoiceFlag(...).
//
// The scope path is itself a pinned format (renderScopePath in selector.go):
// one segment per election, outermost first, joined by a single space; a
// token-spelled segment is `--<selector> <choice>`, a member-spelled segment is
// `--<choice>`; single-quoted wherever a template names one.
// ---------------------------------------------------------------------------

// errScopeSuffix is the clause appended to a presence message when the flag or
// the selector lives inside a scope. Empty at root scope.
func errScopeSuffix(path string) string {
	return fmt.Sprintf(" under '%s'", path)
}

// errFlagOutOfScope is the round's central error, and deliberately NOT "unknown
// flag": the flag is declared, it is simply not in the elected scope, and the
// sentence names both sides.
func errFlagOutOfScope(x string, owners string, why string) string {
	return fmt.Sprintf("flag '--%s' is only valid under %s, but %s", x, owners, why)
}

func errScopeWhyElected(path string, origin string) string {
	return fmt.Sprintf("'%s' was elected%s", path, origin)
}

func errScopeWhyNotProvided(sel string) string {
	return fmt.Sprintf("'--%s' was not provided", sel)
}

func errScopeWhyNoMemberElected(members string) string {
	return fmt.Sprintf("none of %s was elected", members)
}

// The three election-origin clauses. An election from a non-CLI source names
// itself in every message it causes, because otherwise a refusal blames a
// command line that does not contain the cause (§24.6).
func errElectionOriginEnv(varName string) string {
	return fmt.Sprintf(" from env var '%s'", varName)
}

func errElectionOriginConfig(key string) string {
	return fmt.Sprintf(" from config key '%s'", key)
}

const errElectionOriginDefault = " by default"

// errElectionOriginSuffix is the parenthesized form composition produces. It is
// appended AFTER the scope suffix and never before it, and a command-line
// election produces the EMPTY suffix rather than a bare "(elected)".
func errElectionOriginSuffix(origin string) string {
	if origin == "" {
		return ""
	}
	return fmt.Sprintf(" (elected%s)", origin)
}

// errSelectorElectedTwice: last-wins is right for a plain flag and wrong for an
// election, because discarding a value would discard a whole scope with it.
func errSelectorElectedTwice(sel string, values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = "'" + v + "'"
	}
	return fmt.Sprintf("--%s: elected more than once, as %s", sel, strings.Join(quoted, " and "))
}

// errAmbientBindingSkippedEnv / errAmbientBindingSkippedConfig name a
// conditional binding the run did not consult. They are DIAGNOSTICS, not
// errors: no "error: " prefix, the debug channel, and the run continues.
func errAmbientBindingSkippedEnv(varName, x, path string) string {
	return fmt.Sprintf("not consulted: env var '%s' binds flag '--%s' under '%s', which was not elected", varName, x, path)
}

func errAmbientBindingSkippedConfig(key, x, path string) string {
	return fmt.Sprintf("not consulted: config key '%s' binds flag '--%s' under '%s', which was not elected", key, x, path)
}

// --- Registration guards (§12.13's table) ---

func errSelectorOptional(name string) string {
	return fmt.Sprintf("Flag %q: a choice flag cannot declare Optional(): an absent selection is a choice nobody named, so name it as a choice of its own", name)
}

func errSelectorNoChoices(name string) string {
	return fmt.Sprintf("Flag %q: a choice flag must declare at least two choices", name)
}

func errChoiceDuplicateName(sel, c string) string {
	return fmt.Sprintf("Flag %q: choice %q is declared twice", sel, c)
}

func errChoiceHelpEmpty(sel, c string) string {
	return fmt.Sprintf("Choice %q of %q: help text is required", c, sel)
}

// errChoiceNameCharset: under member spelling a choice name IS a flag name, and
// a name that could not be a flag name would make the two spellings declare
// different things (§24.7). One charset, both spellings.
func errChoiceNameCharset(sel, c string) string {
	return fmt.Sprintf("Flag %q: choice name %q must match [a-z][a-z0-9-]*", sel, c)
}

func errSelectorDefaultUnknownChoice(sel string, v interface{}, names string) string {
	return fmt.Sprintf("Flag %q: Default(%s) names no declared choice: must be one of: %s", sel, formatValueForError(v), names)
}

func errSelectorDefaultIncomplete(sel, c, sub string) string {
	return fmt.Sprintf("Flag %q: Default(%q) elects choice %q, whose scope declares the required flag '--%s': a defaulted selection must be complete with nothing typed", sel, c, c, sub)
}

func errMemberFlagPresence(sel, m string) string {
	return fmt.Sprintf("Choice %q of %q: a member flag must declare Required(), read as required once this member is elected", m, sel)
}

func errMemberSelectorShort(sel string) string {
	return fmt.Sprintf("Flag %q: a member-spelled choice flag is never typed, so it cannot carry a short: declare the short on a member", sel)
}

func errMemberDefaultCarriesValue(sel, c string) string {
	return fmt.Sprintf("Flag %q: Default(%q) elects choice %q, whose flag carries a value nothing supplies: only a payload-less member can be a default", sel, c, c)
}

func errTokenChoiceCarriesPayload(sel, c string) string {
	return fmt.Sprintf("Choice %q of %q: a token-spelled choice cannot carry a payload: the token names the choice, and a choice that carries its own value belongs to a member-spelled choice flag, declared with MemberChoiceFlag(...)", c, sel)
}

func errChoicesEntryNotRecord(name string, i int) string {
	return fmt.Sprintf("Flag %q: choices entry %d is a bare value: declare it as Ch(<value>, \"<help>\")", name, i)
}

// errArgChoicesEntryNotRecord is the ARG twin of errChoicesEntryNotRecord
// (§12.13, §18.19 item 219). Positionals keep `choices` in the record spelling,
// and this catalogue twins every flag/arg message that reaches both surfaces,
// so an arg reports itself as an arg rather than borrowing the flag prefix.
// `<i>` is 0-based in both, as it is in every entry template.
func errArgChoicesEntryNotRecord(name string, i int) string {
	return fmt.Sprintf("Arg %q: choices entry %d is a bare value: declare it as Ch(<value>, \"<help>\")", name, i)
}

// errMemberChoiceRequired is GO-ONLY, and it is the mirror of
// errTokenChoiceCarriesPayload: Go's two selector constructors are twins, so a
// plain Choice(...) can reach MemberChoiceFlag(...) the way a MemberChoice(...)
// can reach ChoiceFlag(...). Python spells member election with a keyword and
// TypeScript's factory takes its own choice shape, so neither sibling has an
// input that could produce this state (§12.12's per-language precedent).
func errMemberChoiceRequired(sel, c string) string {
	return fmt.Sprintf("Choice %q of %q: a member-spelled choice flag declares its choices with MemberChoice(...), which names the flag that elects the choice", c, sel)
}

// errChoiceAliased is GO-ONLY: a *ChoiceDecl is an identity value, so the same
// value can be written into two selectors, and its identity would then be
// ambiguous at Match time. Python's choice classes and TypeScript's keyed map
// have no aliasing site.
func errChoiceAliased(c, firstSel, secondSel string) string {
	return fmt.Sprintf("Choice %q of %q: a choice value belongs to exactly one choice flag; it is already declared by %q", c, secondSel, firstSel)
}

// --- Reserved names inside a scope (§12.13, S15) ---

func errScopedNameChoiceReserved(c, sel string) string {
	return fmt.Sprintf("Choice %q of %q: flag name 'choice' is reserved by the framework: it tags the delivered record", c, sel)
}

func errScopedNameValueReserved(c, sel string) string {
	return fmt.Sprintf("Choice %q of %q: flag name 'value' is reserved by the framework: it carries a member-spelled choice's own payload", c, sel)
}

// --- Name collisions (§12.13, §24.7) ---

func errScopedNameCollidesRoot(c, sel, x string) string {
	return fmt.Sprintf("Choice %q of %q: flag '--%s' collides with a command-level flag of the same name: the scoped one could never be reached", c, sel, x)
}

func errScopedNameCollidesSelector(c, sel, x string) string {
	return fmt.Sprintf("Choice %q of %q: flag '--%s' collides with the choice flag's own name", c, sel, x)
}

func errSiblingScopeShapeMismatch(sel, x, a, b string) string {
	return fmt.Sprintf("Flag %q: flag '--%s' is declared by choices %q and %q with different value shapes: sibling scopes may reuse a name only with an identical type and arity, because tokenizing '--%s' cannot wait for an election", sel, x, a, b, x)
}

func errCoElectableNameReuse(name, x, p1, p2 string) string {
	return fmt.Sprintf("command %q: flag '--%s' is declared under '%s' and under '%s', which can be elected at the same time: simultaneously electable scopes may not reuse a flag name", name, x, p1, p2)
}

func errShortCollidesAcrossScopes(name, s, a, b string) string {
	return fmt.Sprintf("command %q: short '-%s' is claimed by '--%s' and '--%s', which can be elected at the same time", name, s, a, b)
}

// errShortShapeMismatch is errSiblingScopeShapeMismatch's exact argument one
// token over (§12.13, §18.19 item 221): which NAME a reused short binds to is
// decided after the election, so the short may only be reused when the token
// consumes argv identically whatever the election decides. Stated over SCOPES,
// so two sibling member scopes are covered by the same words.
func errShortShapeMismatch(name, s, a, b string) string {
	return fmt.Sprintf("command %q: short '-%s' is claimed by '--%s' and '--%s' with different value shapes: sibling scopes may reuse a short only with an identical type and arity, because tokenizing '-%s' cannot wait for an election", name, s, a, b, s)
}

// errShortOnAmbiguousElection closes what shape comparison cannot see: a short
// reused across sibling scopes AND claimed by a candidate that is itself an
// election token is refused outright, because the binding resolves after the
// elections and an election token must be readable before any election has
// happened (§12.13, §18.19 item 221). There is no shape that makes it legal.
func errShortOnAmbiguousElection(name, s, x string) string {
	return fmt.Sprintf("command %q: short '-%s' is reused by sibling scopes and also claimed by '--%s', which elects: an election token is read before any election has happened, so its short cannot be shared", name, s, x)
}

// --- A constraint naming a scoped flag (§12.13, §24.8) ---
//
// The sentence names the CONSTRAINT rather than the family (§12.13's amendment,
// §18.30 item 270): `CoRequired` no longer exists and every constraint declares
// a mandatory name, which identifies the declaration to fix better than the
// family word ever did. The trailing clause drops `dependency` because after
// §26 the noun for all four kinds is `constraint`.

func errConstraintReferencesScopedFlag(name, c, x, path string) string {
	return fmt.Sprintf("command %q: constraint %q references '%s', which is declared under '%s': constraints operate at root scope only", name, c, x, path)
}

// --- The constraint system's registration guards (§12.15, §26.8) ---
//
// Every one is registration-time and lives in the `command "<name>": ` prefix
// family. `errConstraintMinMembers` and `errConstraintMemberNotRecord` are
// Go-EXCLUDED by construction: the constructors take two named members before
// the variadic tail, so a one-member constraint does not compile, and a member
// is a typed value rather than a record-or-bare-name union.

func errConstraintNameCharset(name, c string) string {
	return fmt.Sprintf("command %q: constraint name %q must match [a-z][a-z0-9-]*", name, c)
}

func errConstraintNameDuplicate(name, c string) string {
	return fmt.Sprintf("command %q: duplicate constraint name %q", name, c)
}

func errConstraintNameCollides(name, c string) string {
	return fmt.Sprintf("command %q: constraint name %q is already a flag or arg name: a member reference resolves by name and would be ambiguous", name, c)
}

func errConstraintMemberUnknown(name, c, x string) string {
	return fmt.Sprintf("command %q: constraint %q references unknown member %q", name, c, x)
}

func errConstraintMemberAmbiguous(name, c, x string) string {
	return fmt.Sprintf("command %q: constraint %q references %q, which names both a flag and a positional arg", name, c, x)
}

func errConstraintMemberDuplicate(name, c, x string) string {
	return fmt.Sprintf("command %q: constraint %q declares member %q twice", name, c, x)
}

// errConstraintMemberRequired takes the member RENDERED by §12.15's rule --
// `--old-name` for a flag, bare `targets` for an arg -- and quotes it as the
// single-member rule pins. One template with one substitution: its prefix names
// the constraint rather than a surface, so §12.12's Flag/Arg twinning does not
// apply and the spelling inside it is the flag one for both operand kinds.
func errConstraintMemberRequired(name, c, x string) string {
	return fmt.Sprintf("command %q: constraint %q member '%s' declares Required(): a member the invocation must always supply leaves the constraint nothing to decide", name, c, x)
}

func errConstraintMemberBoolWhen(name, c, x string) string {
	return fmt.Sprintf("command %q: constraint %q member '%s' is a bool and must declare its election: WhenTrue() counts only a true value, WhenPresent() counts any", name, c, x)
}

func errConstraintWhenTrueNotBool(name, c, x, t string) string {
	return fmt.Sprintf("command %q: constraint %q member '%s' declares WhenTrue(), which needs a bool; '%s' is a %s", name, c, x, x, t)
}

func errConstraintWhenNonEmptyNotSized(name, c, x, t string) string {
	return fmt.Sprintf("command %q: constraint %q member '%s' declares WhenNonEmpty(), which needs a string or a collection; '%s' is a %s", name, c, x, x, t)
}

func errConstraintNestedWhen(name, c, x string) string {
	return fmt.Sprintf("command %q: constraint %q member %q is a constraint and cannot declare an election: a nested constraint is engaged when its own members are", name, c, x)
}

func errConstraintNestedFamily(name, c, x string) string {
	return fmt.Sprintf("command %q: constraint %q references constraint %q, which declares a one-way dependency rather than a co-occurrence rule: only at-least-one and all-or-none can be members of another constraint", name, c, x)
}

func errConstraintCycle(name, path string) string {
	return fmt.Sprintf("command %q: constraints form a cycle: %s", name, path)
}

// errConstraintUnknownFlag is the unknown-name refusal for `Requires` and
// `Implies`, whose operand vocabulary is flags only (§26.13), so the noun stays
// `flag` where the member families say `member`.
func errConstraintUnknownFlag(name, c, x string) string {
	return fmt.Sprintf("command %q: constraint %q references unknown flag %q", name, c, x)
}

// ---------------------------------------------------------------------------
// update.go — the update-command construct (contract §12.16, §27)
//
// The family splits across both parity categories the way §12.13's and §12.15's
// do: the two VIOLATION templates are parse-time (stderr, exit 1); the
// declaration guards are registration-time, in §12.10's and §12.12's class.
//
// `<decl>` is `flag '--<x>'` for a flag and `argument '<x>'` for a positional
// arg (renderDeclFlag / renderDeclArg in update.go). The presence spellings
// inside these sentences take the FLAG spelling even when the subject is an
// arg -- Required(), Optional(), Default(<value>) -- because the prefix names
// the COMMAND rather than a surface, so the sentence never claims to quote the
// arg surface's own option (§12.16, §18.31 item 287).
//
// The prefix is `command "<name>": ` on every registration guard, and
// `update "<resource>": ` on the one violation the construct owns.
// ---------------------------------------------------------------------------

// errMutatingDefault is the ONLY guard in this family that fires on a command
// declaring no update at all: the ban keys on effect="mutating" (§27.1), where
// the rest keys on update_of.
func errMutatingDefault(name, decl, value string) string {
	return fmt.Sprintf("command %q: %s declares Default(%s) on a mutating command: absence would write a value the invocation never stated (declare Required() or Optional(), or apply the fallback in the handler and say so in its help)", name, decl, value)
}

func errUpdateOnReadOnly(name string) string {
	return fmt.Sprintf("command %q: a read_only command cannot declare update_of (a command that changes nothing writes no properties)", name)
}

// errUpdateWriteModeInvalid is reachable in all three implementations, and the
// reachable inputs differ: in Go it is the ZERO VALUE of the string-based
// WriteMode type, which renders "". The sentence is byte-identical in each
// because the value formatter is.
func errUpdateWriteModeInvalid(name, v string) string {
	return fmt.Sprintf("command %q: invalid write_mode %q: must be \"sparse\" or \"full_replace\"", name, v)
}

func errUpdateResourceCharset(name, r string) string {
	return fmt.Sprintf("command %q: update resource %q must match [a-z][a-z0-9-]*", name, r)
}

func errUpdatePropertiesEmpty(name, r string) string {
	return fmt.Sprintf("command %q: update of %q declares no properties: an update with nothing to write is not an update", name, r)
}

func errUpdateNameUnknown(name, r, x string) string {
	return fmt.Sprintf("command %q: update of %q references unknown name %q", name, r, x)
}

func errUpdateNameAmbiguous(name, r, x string) string {
	return fmt.Sprintf("command %q: update of %q references %q, which names both a flag and a positional arg", name, r, x)
}

func errUpdateNameDuplicate(name, r, x string) string {
	return fmt.Sprintf("command %q: update of %q declares %q twice", name, r, x)
}

func errUpdateNameBothRoles(name, r, x string) string {
	return fmt.Sprintf("command %q: update of %q declares %q as both identity and property", name, r, x)
}

func errUpdateReferencesScopedFlag(name, r, x, path string) string {
	return fmt.Sprintf("command %q: update of %q references '%s', which is declared under '%s': an update's identity and properties are declared at root scope only", name, r, x, path)
}

// errUpdatePropertyPresence covers `required` ONLY: a property declaring a
// default is refused by errMutatingDefault four steps earlier (§27.11's order),
// an update command being mutating by §27.2's own guard, so the two never
// compete for one declaration.
func errUpdatePropertyPresence(name, r, decl string) string {
	return fmt.Sprintf("command %q: update of %q property %s declares Required(): a property is absent exactly when it is not being written, and the presence declaration for that is Optional()", name, r, decl)
}

func errUpdatePropertyIsArg(name, r, x string) string {
	return fmt.Sprintf("command %q: update of %q property %q is a positional arg: a property must be individually omissible and clearable, and only a flag is", name, r, x)
}

func errUpdatePropertyIsChoiceFlag(name, r, x string) string {
	return fmt.Sprintf("command %q: update of %q property '--%s' is a choice flag: an elected record is a selection, not a property value", name, r, x)
}

func errNullableNotProperty(name, decl string) string {
	return fmt.Sprintf("command %q: %s declares Nullable() but is not a property of an update: only a property can be cleared", name, decl)
}

func errUnsetNameReserved(name, x string) string {
	return fmt.Sprintf("command %q: flag name \"unset-%s\" is reserved: property '--%s' declares Nullable(), which mints '--unset-%s'", name, x, x, x)
}

// --- The two violations (parse-time) ---

// errUpdateNoProperty names EVERY declared property, whether or not it is
// nullable: naming only the ones a reader has not used would require the
// framework to guess which one was meant.
//
// There is deliberately NO decline clause. §12.15 appends §21.4's clause when a
// bool member was provided false; the analogous input here is the opposite
// fact, because inside an update command `--no-proxied` PROVIDES the property
// with the value false (§27.7), so it satisfies this rule rather than declining
// it and this sentence cannot fire in its presence.
func errUpdateNoProperty(resource, properties string) string {
	return fmt.Sprintf("update %q: at least one property is required: %s", resource, properties)
}

// errUpdateValueAndUnset is COMMAND LINE only: it is a collision between two
// tokens, and the machine doors have one key per property (§27.6), so no door
// can reach the state.
func errUpdateValueAndUnset(x string) string {
	return fmt.Sprintf("--%s and --unset-%s are mutually exclusive: a property is either written or cleared", x, x)
}

// --- Delivery-side panics (Go-only: Match is exhaustive at dispatch) ---

func errGetElectedNoSuchKey(name string) string {
	return fmt.Sprintf("strictcli.GetElected: no such key %q", name)
}

func errGetElectedNotSelector(name string, v interface{}) string {
	return fmt.Sprintf("strictcli.GetElected: key %q has dynamic type %T, want *strictcli.Elected", name, v)
}

func errMatchForeignCase(sel, c string) string {
	return fmt.Sprintf("strictcli.Match: case %q is not a choice of choice flag %q", c, sel)
}

func errMatchDuplicateCase(sel, c string) string {
	return fmt.Sprintf("strictcli.Match: choice %q of choice flag %q has two cases", c, sel)
}

func errMatchMissingCases(sel string, missing []string) string {
	return fmt.Sprintf("strictcli.Match: choice flag %q has no case for %s", sel, strings.Join(missing, ", "))
}

// errElectNotAChoice is raised on the programmatic front door when Elect names a
// choice the selector does not declare.
func errElectNotAChoice(sel, c string) string {
	return fmt.Sprintf("--%s: elected value names choice %q, which is not declared by this choice flag", sel, c)
}

// errSelectorValueNotElected is raised on the programmatic front door when a
// selector's kwarg is neither an elected record nor a choice name.
func errSelectorValueNotElected(sel string, v interface{}) string {
	return fmt.Sprintf("--%s: a choice flag's value must be strictcli.Elect(<choice>, ...) or a choice name, got %T", sel, v)
}
