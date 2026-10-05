package strictcli

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	tomledit "github.com/smm-h/go-toml-edit"
)

// CheckContext provides project context to check implementations.
type CheckContext interface {
	ProjectRoot() string
}

// ConnectionEnvReader is an OPTIONAL capability a CheckContext may expose: the
// value of a declared connection env (WithConnectionEnv), read live -- EXCEPT
// under --hermetic, where it resolves as absent ("", false) so a check can skip
// visibly instead of connecting. The check command wraps the tool-supplied
// CheckContext in a value that satisfies this interface, backed by the app's
// declared connection envs and the invocation's hermetic state. Checks that need
// a connection URL type-assert the context to this interface:
//
//	if r, ok := ctx.(strictcli.ConnectionEnvReader); ok {
//	    dsn, present := r.ConnectionEnvValue("DATABASE_URL")
//	    ...
//	}
//
// IsHermetic reports whether the invocation ran under --hermetic. It exists so a
// check can DISTINGUISH the two cases that ConnectionEnvValue's present=false
// otherwise conflates: "--hermetic suppressed the connection env" vs "the env
// var is simply unset". A check that layers config fallbacks below the env must
// honor hermetic even when the env is unset -- otherwise it falls through to a
// config URL and connects, violating the hermetic guarantee. The pattern is:
//
//	dsn, present := r.ConnectionEnvValue("DATABASE_URL")
//	if !present {
//	    if r.IsHermetic() {
//	        return rep.Skipped("hermetic: connection suppressed")
//	    }
//	    // env unset but not hermetic -- config fallback is allowed here
//	}
type ConnectionEnvReader interface {
	ConnectionEnvValue(envVar string) (value string, present bool)
	IsHermetic() bool
}

// checkContextWithConn wraps a tool-supplied CheckContext, delegating
// ProjectRoot while adding connection-env access backed by the framework's
// infraAccess snapshot. It is what the check command hands to check functions so
// they can read declared connection envs (hermetic-suppressed) without the tool
// having to implement anything beyond ProjectRoot.
type checkContextWithConn struct {
	CheckContext
	infra *infraAccess
}

// ConnectionEnvValue implements ConnectionEnvReader.
func (w checkContextWithConn) ConnectionEnvValue(envVar string) (string, bool) {
	if w.infra != nil && w.infra.connections[envVar] {
		if w.infra.hermetic {
			return "", false
		}
		return os.LookupEnv(envVar)
	}
	panic(errConnectionValueUndeclared(envVar))
}

// IsHermetic implements ConnectionEnvReader: reports whether the invocation ran
// under --hermetic. Mirrors the hermetic flag captured on the framework infra
// snapshot; false when no infra is present.
func (w checkContextWithConn) IsHermetic() bool {
	return w.infra != nil && w.infra.hermetic
}

// checkProblem is a single minted finding: text plus severity. It is
// unexported and has no public constructor -- problems are minted only via
// reporter methods (Warn/Error).
type checkProblem struct {
	text     string
	severity string // "error" or "warn"
}

// CheckOutcome is the ceiling-typed result of a check implementation. Its
// fields are unexported so callers cannot forge one: a valid CheckOutcome is
// obtained ONLY through reporter methods (Passed/Skipped/Found). The zero value
// has minted=false and is rejected by the runner (belt-and-braces against an
// impl that returns something a reporter did not mint).
type CheckOutcome struct {
	minted   bool           // proves this value came from a reporter
	kind     string         // "passed", "skipped", "found", "off"
	message  string         // pass/found message or skip reason
	problems []checkProblem // accumulated problems (only for kind == "found")
	// notes is an informational, verdict-inert channel: notes are recorded
	// unconditionally on ANY outcome (including a pass) via reporter.Note. They
	// are PROVABLY inert -- excluded from status derivation, gating, problem
	// ordering, and exit codes. They surface only under --verbose and in JSON.
	notes []string
}

// orderedProblems returns the outcome's problems grouped by severity: all
// error-severity problems first, then all warn-severity problems. Insertion
// order is preserved within each group.
func (o CheckOutcome) orderedProblems() []checkProblem {
	var errs, warns []checkProblem
	for _, p := range o.problems {
		if p.severity == "error" {
			errs = append(errs, p)
		} else {
			warns = append(warns, p)
		}
	}
	return append(errs, warns...)
}

// reporterCore holds the problem accumulator and the shared minting methods
// (Warn/Passed/Skipped/Found) promoted into both reporter types. Error-minting
// lives ONLY on ErrorReporter, so WarnReporter structurally lacks it (calling
// Error on a *WarnReporter is a compile error).
type reporterCore struct {
	problems []checkProblem
	// notes accumulates informational messages recorded via Note. Notes are
	// carried onto the minted outcome but never influence status, gating, or
	// exit codes -- they are a verdict-inert reporting channel.
	notes []string
}

// Note records an informational note. Non-empty text is required. Notes are
// allowed on EVERY outcome, including a pass -- they never cause the
// problems-present hard-errors that passed()/skipped() enforce. Notes are
// verdict-inert: they surface only under --verbose and in JSON output.
func (r *reporterCore) Note(text string) {
	if strings.TrimSpace(text) == "" {
		panic(errNoteTextEmpty)
	}
	r.notes = append(r.notes, text)
}

// Warn mints a warn-severity problem. Non-empty text is required.
//
// Reporter validation messages are worded identically to the Python
// implementation (method-agnostic phrasing, no "Warn:"/"warn:" prefix) so the
// two implementations are byte-for-byte in parity -- see conformance/
// check_error_parity.py, which scans these panics.
func (r *reporterCore) Warn(text string) {
	if strings.TrimSpace(text) == "" {
		panic(errProblemTextEmpty)
	}
	r.problems = append(r.problems, checkProblem{text: text, severity: "warn"})
}

// Passed finalizes a terminal PASS outcome. It hard-errors if any problems were
// accumulated (an impl that found problems cannot claim it passed -- use Found).
func (r *reporterCore) Passed(message string) CheckOutcome {
	if strings.TrimSpace(message) == "" {
		panic(errOutcomeMessageEmpty)
	}
	if len(r.problems) > 0 {
		panic(errPassedWithProblems)
	}
	return CheckOutcome{minted: true, kind: "passed", message: message, notes: append([]string(nil), r.notes...)}
}

// Skipped finalizes a terminal SKIP outcome. It hard-errors if any problems were
// accumulated.
func (r *reporterCore) Skipped(reason string) CheckOutcome {
	if strings.TrimSpace(reason) == "" {
		panic(errSkipReasonEmpty)
	}
	if len(r.problems) > 0 {
		panic(errSkippedWithProblems)
	}
	return CheckOutcome{minted: true, kind: "skipped", message: reason, notes: append([]string(nil), r.notes...)}
}

// Found finalizes an outcome carrying the accumulated problems. It hard-errors
// when no problems were accumulated (nothing found means the check passed -- say
// so explicitly with Passed).
func (r *reporterCore) Found(message string) CheckOutcome {
	if strings.TrimSpace(message) == "" {
		panic(errOutcomeMessageEmpty)
	}
	if len(r.problems) == 0 {
		panic(errFoundNoProblems)
	}
	return CheckOutcome{
		minted:   true,
		kind:     "found",
		message:  message,
		problems: append([]checkProblem(nil), r.problems...),
		notes:    append([]string(nil), r.notes...),
	}
}

// WarnReporter is handed to warn-severity check impls. It can mint warn-severity
// problems and terminal outcomes but structurally LACKS error-minting: there is
// no Error method in its method set, so an attempt to raise an error-severity
// problem from a warn check fails to compile.
type WarnReporter struct {
	reporterCore
}

// ErrorReporter is handed to error-severity check impls. It has everything
// WarnReporter has PLUS Error (mints an error-severity problem).
type ErrorReporter struct {
	reporterCore
}

// Error mints an error-severity problem. Non-empty text is required. This method
// exists only on ErrorReporter -- see the WarnReporter doc comment.
func (r *ErrorReporter) Error(text string) {
	if strings.TrimSpace(text) == "" {
		panic(errProblemTextEmpty)
	}
	r.problems = append(r.problems, checkProblem{text: text, severity: "error"})
}

// deriveStatus maps a minted CheckOutcome to a display/verdict label.
// found + any error-severity problem => "fail"; found + only warns => "warn";
// passed => "pass"; skipped => "skip"; off (the check's resolved value turned
// it off) => "off".
func deriveStatus(o CheckOutcome) string {
	switch o.kind {
	case "passed":
		return "pass"
	case "skipped":
		return "skip"
	case "off":
		return "off"
	case "found":
		for _, p := range o.problems {
			if p.severity == "error" {
				return "fail"
			}
		}
		return "warn"
	default:
		panic(errUnknownCheckOutcomeKind(o.kind))
	}
}

// Scope adapter asymmetry (deliberate, documented): Python exposes a
// set_scope_adapter hook that projects a check's context or skips it. Go has no
// scope adapter yet -- no Go consumer needs scoped checks. When the first Go
// scope consumer appears, add SetScopeAdapter here with the same contract as
// Python's (returns a replacement context OR a skip directive; it can no longer
// mint arbitrary outcomes), and wire it into runChecks alongside the cascade
// logic. Until then, checkDef.scope is parsed and carried but never consulted at
// run time on the Go side.

// checkDef holds the definition of a single check loaded from checks.toml.
type checkDef struct {
	name         string
	tags         []string
	severity     string // "error" or "warn"
	fast         bool
	pure         bool
	needsNetwork bool
	dependsOn    []string
	scope        string // optional, defaults to ""
	// description and subject are the two metadata fields every checks.toml
	// declaration carries; empty on provider-sourced checks, which have no TOML.
	description string
	subject     string
	// impl is the wrapped runner installed at registration time. It constructs
	// the appropriate reporter and invokes the user's function. nil until
	// registered via RegisterErrorCheck/RegisterWarnCheck.
	impl     checkImpl
	implForm string // "error" or "warn" -- the registration form, for the severity cross-check
}

// checkImpl runs one check. cacheWrites is the effect log of the dispatch the
// check run belongs to, where a framework-owned check records its CACHE_WRITEs;
// nil when the run belongs to no dispatch (RunChecks called directly). A
// consumer's check never sees it: registration wraps the consumer's function.
type checkImpl func(ctx CheckContext, cacheWrites *effectLog) CheckOutcome

// kebabNameRe is the naming rule's pattern: lowercase kebab-case, no leading,
// trailing or doubled hyphen. isKebabName adds the two-character minimum.
var kebabNameRe = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// isKebabName reports whether name satisfies the naming rule every identifier
// a caller types or references follows (kebabNameClause).
func isKebabName(name string) bool {
	return len(name) >= 2 && kebabNameRe.MatchString(name)
}

// knownCheckFields enumerates the allowed fields in a check definition table.
var knownCheckFields = map[string]bool{
	"tags":          true,
	"severity":      true,
	"fast":          true,
	"pure":          true,
	"needs_network": true,
	"depends_on":    true,
	"description":   true,
	"subject":       true,
	"scope":         true,
}

// checkSubjectRe is a check's options subject: the subject file its option
// belongs in, named in the options model's value-name grammar. "manifest"
// names the options directory's own manifest and is refused separately.
var checkSubjectRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// addCheckDef inserts a check definition into the registry, rejecting
// duplicate names as a hard error. It maintains checkOrder in sorted order so
// that dynamic additions keep deterministic listing. This is the single
// internal insertion point for check definitions (TOML loading routes through
// it; future provider-sourced defs will too).
func (a *App) addCheckDef(def *checkDef) error {
	if a.checkDefs == nil {
		a.checkDefs = make(map[string]*checkDef)
	}
	if _, exists := a.checkDefs[def.name]; exists {
		return errDuplicateCheckDef(def.name)
	}
	a.checkDefs[def.name] = def
	a.checkOrder = append(a.checkOrder, def.name)
	a.resortCheckOrder()
	return nil
}

// resortCheckOrder re-sorts checkOrder so that additions made after the initial
// parse remain in deterministic (sorted) order.
func (a *App) resortCheckOrder() {
	sort.Strings(a.checkOrder)
}

// loadChecksToml reads a checks.toml file from disk and parses it.
func loadChecksToml(path string) (string, map[string]*checkDef, []string, error) {
	appName, defs, names, _, err := loadChecksTomlWithHooks(path)
	return appName, defs, names, err
}

// loadChecksTomlWithHooks is loadChecksToml plus the declared hooks.
func loadChecksTomlWithHooks(path string) (string, map[string]*checkDef, []string, map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, nil, nil, err
	}
	return parseChecksTomlWithHooks(data)
}

// parseChecksToml parses TOML bytes and returns the app name, validated check definitions,
// and check names in sorted order (for deterministic listing). The declared
// hooks are validated too; parseChecksTomlWithHooks returns them.
func parseChecksToml(data []byte) (string, map[string]*checkDef, []string, error) {
	appName, defs, names, _, err := parseChecksTomlWithHooks(data)
	return appName, defs, names, err
}

// parseCheckHooks validates the [hooks.<name>] tables: each names one check
// selection (a tag expression) that --hook <name> runs.
func parseCheckHooks(raw interface{}) (map[string]string, error) {
	table, ok := raw.(map[string]interface{})
	if !ok {
		return nil, errChecksTomlHooksMustBeTable()
	}
	names := make([]string, 0, len(table))
	for name := range table {
		names = append(names, name)
	}
	sort.Strings(names)
	hooks := make(map[string]string, len(names))
	for _, name := range names {
		if !isKebabName(name) {
			return nil, errChecksTomlInvalidHookName(name)
		}
		fields, ok := table[name].(map[string]interface{})
		if !ok {
			return nil, errChecksTomlHookMustBeTable(name)
		}
		var unknown []string
		for field := range fields {
			if field != "tag" {
				unknown = append(unknown, field)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return nil, errChecksTomlHookUnknownField(name, unknown[0])
		}
		tagRaw, ok := fields["tag"]
		if !ok {
			return nil, errChecksTomlHookMissingTag(name)
		}
		tag, ok := tagRaw.(string)
		if !ok || strings.TrimSpace(tag) == "" {
			return nil, errChecksTomlHookTagInvalid(name)
		}
		if _, err := matchTagExpr(tag, map[string]bool{}); err != nil {
			return nil, errChecksTomlHookTagExpr(name, err)
		}
		hooks[name] = tag
	}
	return hooks, nil
}

// parseChecksTomlWithHooks is parseChecksToml plus the declared hooks (hook
// name -> tag expression).
func parseChecksTomlWithHooks(data []byte) (string, map[string]*checkDef, []string, map[string]string, error) {
	// Unmarshal into a generic map for strict validation
	rawPtr, err := tomledit.Unmarshal[map[string]interface{}](data)
	if err != nil {
		return "", nil, nil, nil, errChecksTomlParse(err)
	}
	raw := *rawPtr

	// Validate top-level keys: only "app", "checks" and "hooks" are allowed
	for key := range raw {
		if key != "checks" && key != "app" && key != "hooks" {
			return "", nil, nil, nil, errChecksTomlUnknownTopLevelKey(key)
		}
	}

	// Validate required "app" field
	appRaw, ok := raw["app"]
	if !ok {
		return "", nil, nil, nil, errChecksTomlMissingApp()
	}
	appName, ok := appRaw.(string)
	if !ok || appName == "" {
		return "", nil, nil, nil, errChecksTomlAppNotString()
	}

	hooks := map[string]string{}
	if hooksRaw, ok := raw["hooks"]; ok {
		if hooks, err = parseCheckHooks(hooksRaw); err != nil {
			return "", nil, nil, nil, err
		}
	}

	// Handle missing [checks] section gracefully — a file with just app = "x" is valid
	checksRaw, ok := raw["checks"]
	if !ok {
		return appName, make(map[string]*checkDef), nil, hooks, nil
	}

	checksMap, ok := checksRaw.(map[string]interface{})
	if !ok {
		return "", nil, nil, nil, errChecksTomlChecksMustBeTable()
	}

	result := make(map[string]*checkDef, len(checksMap))

	// Sort check names for deterministic error ordering
	names := make([]string, 0, len(checksMap))
	for name := range checksMap {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		val := checksMap[name]

		// Validate check name
		if !isKebabName(name) {
			return "", nil, nil, nil, errChecksTomlInvalidCheckName(name)
		}

		fields, ok := val.(map[string]interface{})
		if !ok {
			return "", nil, nil, nil, errChecksTomlCheckMustBeTable(name)
		}

		// Reject unknown fields
		for field := range fields {
			if !knownCheckFields[field] {
				return "", nil, nil, nil, errChecksTomlUnknownField(name, field)
			}
		}

		def := &checkDef{name: name}

		// Parse tags (required, []string — may be empty)
		if err := parseCheckTags(name, fields, def); err != nil {
			return "", nil, nil, nil, err
		}

		// Parse severity (required, "error" or "warn")
		if err := parseCheckSeverity(name, fields, def); err != nil {
			return "", nil, nil, nil, err
		}

		// Parse fast (required, bool)
		if err := parseCheckBool(name, fields, "fast", &def.fast); err != nil {
			return "", nil, nil, nil, err
		}

		// Parse pure (required, bool)
		if err := parseCheckBool(name, fields, "pure", &def.pure); err != nil {
			return "", nil, nil, nil, err
		}

		// Parse needs_network (required, bool)
		if err := parseCheckBool(name, fields, "needs_network", &def.needsNetwork); err != nil {
			return "", nil, nil, nil, err
		}

		// Parse depends_on (required, []string, can be empty)
		if err := parseCheckDependsOn(name, fields, def); err != nil {
			return "", nil, nil, nil, err
		}

		// Parse description (required, one line of prose)
		descRaw, ok := fields["description"]
		if !ok {
			return "", nil, nil, nil, errChecksTomlMissingField(name, "description")
		}
		desc, ok := descRaw.(string)
		if !ok || strings.TrimSpace(desc) == "" || strings.ContainsAny(desc, "\n\r") {
			return "", nil, nil, nil, errChecksTomlDescriptionInvalid(name)
		}
		def.description = desc

		// Parse subject (required, the options subject file its option belongs in)
		subjectRaw, ok := fields["subject"]
		if !ok {
			return "", nil, nil, nil, errChecksTomlMissingField(name, "subject")
		}
		subject, ok := subjectRaw.(string)
		if !ok || !checkSubjectRe.MatchString(subject) || subject == "manifest" {
			return "", nil, nil, nil, errChecksTomlSubjectInvalid(name)
		}
		def.subject = subject

		// Parse scope (optional, string, default "")
		if scopeRaw, ok := fields["scope"]; ok {
			scopeStr, ok := scopeRaw.(string)
			if !ok {
				return "", nil, nil, nil, errChecksTomlScopeMustBeString(name, scopeRaw)
			}
			def.scope = scopeStr
		}

		result[name] = def
	}

	// Validate depends_on references
	for _, name := range names {
		def := result[name]
		for _, dep := range def.dependsOn {
			if _, ok := result[dep]; !ok {
				return "", nil, nil, nil, errChecksTomlDependsOnUnknown(name, dep)
			}
		}
	}

	return appName, result, names, hooks, nil
}

// parseCheckTags extracts and validates the "tags" field.
func parseCheckTags(name string, fields map[string]interface{}, def *checkDef) error {
	raw, ok := fields["tags"]
	if !ok {
		return errChecksTomlMissingField(name, "tags")
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return errChecksTomlTagsMustBeStrings(name)
	}
	tags := make([]string, len(arr))
	for i, v := range arr {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return errChecksTomlTagsEntriesMustBeStrings(name)
		}
		tags[i] = s
	}
	def.tags = tags
	return nil
}

// parseCheckSeverity extracts and validates the "severity" field.
func parseCheckSeverity(name string, fields map[string]interface{}, def *checkDef) error {
	raw, ok := fields["severity"]
	if !ok {
		return errChecksTomlMissingField(name, "severity")
	}
	s, ok := raw.(string)
	if !ok || (s != "error" && s != "warn") {
		return errChecksTomlSeverityInvalid(name, raw)
	}
	def.severity = s
	return nil
}

// parseCheckBool extracts and validates a required boolean field.
func parseCheckBool(name string, fields map[string]interface{}, field string, target *bool) error {
	raw, ok := fields[field]
	if !ok {
		return errChecksTomlMissingField(name, field)
	}
	b, ok := raw.(bool)
	if !ok {
		return errChecksTomlBoolFieldInvalid(name, field, raw)
	}
	*target = b
	return nil
}

// parseCheckDependsOn extracts and validates the "depends_on" field.
func parseCheckDependsOn(name string, fields map[string]interface{}, def *checkDef) error {
	raw, ok := fields["depends_on"]
	if !ok {
		return errChecksTomlMissingField(name, "depends_on")
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return errChecksTomlDependsOnMustBeStrings(name)
	}
	deps := make([]string, len(arr))
	for i, v := range arr {
		s, ok := v.(string)
		if !ok {
			return errChecksTomlDependsOnEntriesMustBeStrings(name)
		}
		deps[i] = s
	}
	def.dependsOn = deps
	return nil
}

// tomlTypeName returns a Python-compatible type name for a TOML-decoded value.
// Matches Python's type(val).__name__ output for cross-language error parity.
func tomlTypeName(v interface{}) string {
	switch v.(type) {
	case bool:
		return "bool"
	case int64:
		return "int"
	case float64:
		return "float"
	case string:
		return "str"
	case []interface{}:
		return "list"
	case map[string]interface{}:
		return "dict"
	case nil:
		return "NoneType"
	default:
		return fmt.Sprintf("%T", v)
	}
}
