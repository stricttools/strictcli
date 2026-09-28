package strictcli

import "strings"

// CheckValue is the value an app's check value resolver assigns one check.
//
// Value is one of "error", "warn", "off": "error" runs the check as registered,
// "warn" runs it and reports its failures as warnings (never blocking), "off"
// does not run it (the check is shown as off, with its source). Source says
// where the value came from -- for example an options entry id and the file
// holding it -- and is shown beside the value. A value may lower a check's
// registered severity, never raise it: a warn-registered check resolved to
// "error" is refused when the checks are selected.
type CheckValue struct {
	Value  string
	Source string
}

// checkValueDefaultSource is the source shown for a check the resolver assigns
// no value: it runs at its registered severity.
const checkValueDefaultSource = "default"

// resolvedCheckValue is one check's effective value and where it came from.
type resolvedCheckValue struct {
	value  string
	source string
}

// SetCheckValueResolver sets the resolver that assigns each check its value.
//
// The resolver is called with a check name and returns the check's CheckValue
// and true, or false to leave the check at its registered severity (shown with
// the source "default"). The check command, the failing-checks command, and
// RunChecks apply it: "off" does not run the check, "warn" reports its failures
// as warnings, "error" runs it as registered. A value may lower a check's
// registered severity, never raise it; a warn-registered check resolved to
// "error" is refused when the checks are selected, as is a value outside the
// three or an empty source.
//
// depends_on is unaffected: a dependency still runs first, and a dependency
// resolved to "warn" cannot fail, so it blocks nothing.
func (a *App) SetCheckValueResolver(resolver func(name string) (CheckValue, bool)) {
	a.checkValueResolver = resolver
}

// resolveCheckValues resolves each named check's effective value and source,
// refusing the first value the resolver may not return.
func (a *App) resolveCheckValues(names []string) (map[string]resolvedCheckValue, error) {
	values := make(map[string]resolvedCheckValue, len(names))
	for _, name := range names {
		severity := a.checkDefs[name].severity
		var cv CheckValue
		ok := false
		if a.checkValueResolver != nil {
			cv, ok = a.checkValueResolver(name)
		}
		if !ok {
			values[name] = resolvedCheckValue{value: severity, source: checkValueDefaultSource}
			continue
		}
		switch cv.Value {
		case "error", "warn", "off":
		default:
			return nil, stringError(errCheckValueInvalid(name, cv.Value))
		}
		if strings.TrimSpace(cv.Source) == "" {
			return nil, stringError(errCheckValueSourceEmpty(name, cv.Value))
		}
		if cv.Value == "error" && severity == "warn" {
			return nil, stringError(errCheckValueAboveSeverity(name, cv.Value, cv.Source, severity))
		}
		values[name] = resolvedCheckValue{value: cv.Value, source: cv.Source}
	}
	return values, nil
}

// stringError is an error carrying a catalog message verbatim.
type stringError string

func (e stringError) Error() string { return string(e) }

// mintCheckOff is the runner-internal outcome for a check its resolved value
// turned off: it did not run, and the row says so and where the value came
// from.
func mintCheckOff(source string) CheckOutcome {
	return CheckOutcome{minted: true, kind: "off", message: "off: " + source}
}

// mintAsWarnings re-mints the outcome of a check resolved to "warn": every
// error-severity problem is reported as a warning, so the outcome derives WARN
// (never FAIL) and blocks nothing. Other outcomes are returned as-is.
func mintAsWarnings(o CheckOutcome) CheckOutcome {
	if o.kind != "found" {
		return o
	}
	problems := make([]checkProblem, len(o.problems))
	for i, p := range o.problems {
		problems[i] = checkProblem{text: p.text, severity: "warn"}
	}
	o.problems = problems
	return o
}
