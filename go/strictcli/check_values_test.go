package strictcli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// Check metadata, per-check values, named hook selections, and the
// failing-checks command.

func cvCheck(name string, tags []string, severity string, dependsOn ...string) string {
	quote := func(xs []string) string {
		q := make([]string, len(xs))
		for i, x := range xs {
			q[i] = fmt.Sprintf("%q", x)
		}
		return strings.Join(q, ", ")
	}
	return fmt.Sprintf("[checks.%s]\ndescription = \"Checks %s\"\nsubject = \"quality\"\ntags = [%s]\nseverity = %q\nfast = true\npure = true\nneeds_network = false\ndepends_on = [%s]\n\n",
		name, name, quote(tags), severity, quote(dependsOn))
}

const cvHooks = `
[hooks.pre-push]
tag = "prepush"

[hooks.pre-release]
tag = "preflight & !slow"
`

func cvToml() string {
	return "app = \"testapp\"\n\n" +
		cvCheck("lint", []string{"prepush"}, "error") +
		cvCheck("fmt", []string{"prepush"}, "warn") +
		cvCheck("docs", []string{"preflight"}, "error") +
		cvCheck("bench", []string{"preflight", "slow"}, "error") +
		cvHooks
}

// cvApp builds an app from toml whose checks pass unless outcomes names them.
func cvApp(t *testing.T, toml string, outcomes map[string]CheckOutcome, resolver func(string) (CheckValue, bool)) *App {
	t.Helper()
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(writeChecksFile(t, toml)))
	dropBuiltinCheckProviders(app)
	for _, name := range app.checkOrder {
		name := name
		if app.checkDefs[name].severity == "warn" {
			app.RegisterWarnCheck(name, func(CheckContext, *WarnReporter) CheckOutcome {
				if o, ok := outcomes[name]; ok {
					return o
				}
				return passOutcome(name + " ok")
			})
		} else {
			app.RegisterErrorCheck(name, func(CheckContext, *ErrorReporter) CheckOutcome {
				if o, ok := outcomes[name]; ok {
					return o
				}
				return passOutcome(name + " ok")
			})
		}
	}
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: emptyProjectRoot} })
	if resolver != nil {
		app.SetCheckValueResolver(resolver)
	}
	return app
}

func cvValues(m map[string][2]string) func(string) (CheckValue, bool) {
	return func(name string) (CheckValue, bool) {
		e, ok := m[name]
		if !ok {
			return CheckValue{}, false
		}
		return CheckValue{Value: e[0], Source: e[1]}, true
	}
}

type cvItem struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Value    string `json:"value"`
	Source   string `json:"source"`
}

func cvPayload(t *testing.T, r Result) []cvItem {
	t.Helper()
	var items []cvItem
	if err := json.Unmarshal(envelopePayload(t, r.Stdout), &items); err != nil {
		t.Fatalf("payload: %v (stdout=%q)", err, r.Stdout)
	}
	return items
}

func cvTomlError(t *testing.T, toml string) (msg string) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected a registration panic for:\n%s", toml)
		}
		msg = fmt.Sprint(r)
	}()
	NewApp("testapp", "1.0.0", "t", WithChecks(writeChecksFile(t, toml)))
	return ""
}

func cvTomlOK(t *testing.T, toml string) *App {
	t.Helper()
	return NewApp("testapp", "1.0.0", "t", WithChecks(writeChecksFile(t, toml)))
}

// --- check metadata --------------------------------------------------------

func cvOne(description, subject string) string {
	lines := []string{`app = "testapp"`, "", "[checks.lint]"}
	if description != "" {
		lines = append(lines, "description = "+description)
	}
	if subject != "" {
		lines = append(lines, "subject = "+subject)
	}
	lines = append(lines, `tags = ["a"]`, `severity = "error"`, "fast = true", "pure = true", "needs_network = false", "depends_on = []", "")
	return strings.Join(lines, "\n")
}

func TestCheckMetadata_MissingDescriptionAndItsFix(t *testing.T) {
	msg := cvTomlError(t, cvOne("", `"quality"`))
	if msg != `checks.toml: check "lint": missing required field "description"` {
		t.Fatalf("got %q", msg)
	}
	cvTomlOK(t, cvOne(`"Runs the linter"`, `"quality"`))
}

func TestCheckMetadata_MissingSubjectAndItsFix(t *testing.T) {
	msg := cvTomlError(t, cvOne(`"Runs the linter"`, ""))
	if msg != `checks.toml: check "lint": missing required field "subject"` {
		t.Fatalf("got %q", msg)
	}
	cvTomlOK(t, cvOne(`"Runs the linter"`, `"quality"`))
}

func TestCheckMetadata_BothMissingNamesDescription(t *testing.T) {
	msg := cvTomlError(t, cvOne("", ""))
	if !strings.HasSuffix(msg, `missing required field "description"`) {
		t.Fatalf("got %q", msg)
	}
}

func TestCheckMetadata_DescriptionMustBeOneLine(t *testing.T) {
	for _, bad := range []string{`""`, `"   "`, `"two\nlines"`, "3"} {
		msg := cvTomlError(t, cvOne(bad, `"quality"`))
		if msg != `checks.toml: check "lint": "description" must be a non-empty single-line string` {
			t.Fatalf("%s: got %q", bad, msg)
		}
	}
	cvTomlOK(t, cvOne(`"one line"`, `"quality"`))
}

func TestCheckMetadata_SubjectGrammar(t *testing.T) {
	for _, bad := range []string{`""`, `"Quality"`, `"code quality"`, `"a_b"`, `"manifest"`, "1"} {
		msg := cvTomlError(t, cvOne(`"d"`, bad))
		if msg != `checks.toml: check "lint": "subject" must be lowercase letters, digits, and hyphens, and not "manifest"` {
			t.Fatalf("%s: got %q", bad, msg)
		}
	}
	cvTomlOK(t, cvOne(`"d"`, `"code-quality-2"`))
}

func TestCheckMetadata_InTheSchemaDump(t *testing.T) {
	app := cvApp(t, cvToml(), nil, nil)
	schema := dumpSchemaObject(app)
	checks := schema.get("checks").(*schemaObject)
	lint := checks.get("lint").(*schemaObject)
	keys := lint.keys
	if keys[len(keys)-2] != "description" || keys[len(keys)-1] != "subject" {
		t.Fatalf("key order: %v", keys)
	}
	if lint.get("description") != "Checks lint" || lint.get("subject") != "quality" {
		t.Fatalf("values: %v %v", lint.get("description"), lint.get("subject"))
	}
}

// --- hooks -----------------------------------------------------------------

func TestHooks_RunTheirSelection(t *testing.T) {
	r := cvApp(t, cvToml(), nil, nil).Test([]string{"check", "--hook", "pre-push"})
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "lint ok") || !strings.Contains(r.Stdout, "fmt ok") || strings.Contains(r.Stdout, "docs") {
		t.Fatalf("got %d %q", r.ExitCode, r.Stdout)
	}
}

func TestHooks_ExpressionKeepsNegation(t *testing.T) {
	r := cvApp(t, cvToml(), nil, nil).Test([]string{"check", "--hook", "pre-release"})
	if !strings.Contains(r.Stdout, "docs ok") || strings.Contains(r.Stdout, "bench") {
		t.Fatalf("got %q", r.Stdout)
	}
}

func TestHooks_ManualTagKeepsNegation(t *testing.T) {
	r := cvApp(t, cvToml(), nil, nil).Test([]string{"check", "--tag", "!prepush & !slow"})
	if !strings.Contains(r.Stdout, "docs ok") || strings.Contains(r.Stdout, "lint") || strings.Contains(r.Stdout, "bench") {
		t.Fatalf("got %q", r.Stdout)
	}
}

func TestHooks_FailingChecksTakesAHook(t *testing.T) {
	app := cvApp(t, cvToml(), map[string]CheckOutcome{"lint": failOutcome("broken", "bad line")}, nil)
	r := app.Test([]string{"failing-checks", "--hook", "pre-push"})
	if r.ExitCode != 1 || !strings.Contains(r.Stdout, "FAIL  lint") {
		t.Fatalf("got %d %q", r.ExitCode, r.Stdout)
	}
}

func TestHooks_UnknownListsDeclaredAndTheFixClears(t *testing.T) {
	app := cvApp(t, cvToml(), nil, nil)
	r := app.Test([]string{"check", "--hook", "pre-pusj"})
	if r.ExitCode != 1 || !strings.HasPrefix(r.Stderr, "error: unknown hook \"pre-pusj\"; declared hooks: pre-push, pre-release\n") {
		t.Fatalf("got %d %q", r.ExitCode, r.Stderr)
	}
	if r := app.Test([]string{"check", "--hook", "pre-push"}); r.ExitCode != 0 {
		t.Fatalf("fix did not clear: %d %q", r.ExitCode, r.Stderr)
	}
}

func TestHooks_UnknownWithNoneDeclaredAndTheFixClears(t *testing.T) {
	toml := "app = \"testapp\"\n\n" + cvCheck("lint", []string{"prepush"}, "error")
	r := cvApp(t, toml, nil, nil).Test([]string{"failing-checks", "--hook", "pre-push"})
	if r.ExitCode != 1 || !strings.HasPrefix(r.Stderr, "error: unknown hook \"pre-push\"; checks.toml declares no hooks\n") {
		t.Fatalf("got %d %q", r.ExitCode, r.Stderr)
	}
	fixed := toml + "\n[hooks.pre-push]\ntag = \"prepush\"\n"
	if r := cvApp(t, fixed, nil, nil).Test([]string{"failing-checks", "--hook", "pre-push"}); r.ExitCode != 0 {
		t.Fatalf("fix did not clear: %d %q", r.ExitCode, r.Stderr)
	}
}

func TestHooks_CombineWithNoOtherSelection(t *testing.T) {
	app := cvApp(t, cvToml(), nil, nil)
	for _, other := range [][]string{{"--tag", "prepush"}, {"--name", "lint"}, {"--all"}} {
		r := app.Test(append([]string{"check", "--hook", "pre-push"}, other...))
		if r.ExitCode != 1 || !strings.HasPrefix(r.Stderr, "error: --hook cannot be combined with --all, --tag, or --name\n") {
			t.Fatalf("%v: got %d %q", other, r.ExitCode, r.Stderr)
		}
	}
	if r := app.Test([]string{"check", "--hook", "pre-push"}); r.ExitCode != 0 {
		t.Fatalf("fix did not clear: %d %q", r.ExitCode, r.Stderr)
	}
}

func TestHooks_AppearInHelp(t *testing.T) {
	for _, command := range []string{"check", "failing-checks"} {
		r := cvApp(t, cvToml(), nil, nil).Test([]string{command, "--help"})
		want := "Run the checks a hook declared in checks.toml selects: pre-push (tag 'prepush'), pre-release (tag 'preflight & !slow')"
		if !strings.Contains(r.Stdout, want) {
			t.Fatalf("%s help: %q", command, r.Stdout)
		}
	}
	toml := "app = \"testapp\"\n\n" + cvCheck("lint", []string{"x"}, "error")
	if r := cvApp(t, toml, nil, nil).Test([]string{"check", "--help"}); !strings.Contains(r.Stdout, "(no hooks are declared)") {
		t.Fatalf("got %q", r.Stdout)
	}
}

func TestHooks_DeclarationRefusalsAndTheirFix(t *testing.T) {
	base := "app = \"testapp\"\n\n" + cvCheck("lint", []string{"prepush"}, "error")
	cases := []struct{ toml, msg string }{
		{base + "[hooks.Pre-Push]\ntag = \"prepush\"\n", `checks.toml: invalid hook name "Pre-Push" (must match [a-z][a-z0-9-]*)`},
		{base + "[hooks]\npre-push = \"prepush\"\n", `checks.toml: hook "pre-push" must be a table`},
		{base + "[hooks.pre-push]\ntag = \"prepush\"\nname = \"lint\"\n", `checks.toml: hook "pre-push": unknown field "name"`},
		{base + "[hooks.pre-push]\n", `checks.toml: hook "pre-push": missing required field "tag"`},
		{base + "[hooks.pre-push]\ntag = \"\"\n", `checks.toml: hook "pre-push": "tag" must be a non-empty string`},
		{base + "[hooks.pre-push]\ntag = \"prepush &\"\n", `checks.toml: hook "pre-push": tag expression: unexpected end of expression at position 9`},
		{"app = \"testapp\"\nhooks = 3\n\n" + cvCheck("lint", []string{"prepush"}, "error"), `checks.toml: [hooks] must be a table`},
	}
	for _, c := range cases {
		if msg := cvTomlError(t, c.toml); msg != c.msg {
			t.Fatalf("got %q, want %q", msg, c.msg)
		}
	}
	app := cvTomlOK(t, base+"[hooks.pre-push]\ntag = \"prepush\"\n")
	if len(app.checkHooks) != 1 || app.checkHooks["pre-push"] != "prepush" {
		t.Fatalf("hooks: %v", app.checkHooks)
	}
}

// --- per-check values ------------------------------------------------------

func TestCheckValues_OffDoesNotRun(t *testing.T) {
	called := false
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(writeChecksFile(t, cvToml())))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("lint", func(CheckContext, *ErrorReporter) CheckOutcome { called = true; return passOutcome("x") })
	app.RegisterWarnCheck("fmt", func(CheckContext, *WarnReporter) CheckOutcome { return passOutcome("fmt ok") })
	app.RegisterErrorCheck("docs", func(CheckContext, *ErrorReporter) CheckOutcome { return passOutcome("docs ok") })
	app.RegisterErrorCheck("bench", func(CheckContext, *ErrorReporter) CheckOutcome { return passOutcome("bench ok") })
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: emptyProjectRoot} })
	app.SetCheckValueResolver(cvValues(map[string][2]string{"lint": {"off", "demo:lint in options/quality.toml"}}))
	r := app.Test([]string{"check", "--hook", "pre-push"})
	if called || r.ExitCode != 0 || !strings.Contains(r.Stdout, "OFF   lint    off: demo:lint in options/quality.toml") {
		t.Fatalf("got called=%v %d %q", called, r.ExitCode, r.Stdout)
	}
	items := cvPayload(t, app.Test([]string{"check", "--hook", "pre-push", "--json"}))
	for _, it := range items {
		if it.Name == "lint" && (it.Status != "off" || it.Message != "off: demo:lint in options/quality.toml") {
			t.Fatalf("lint item: %+v", it)
		}
	}
}

func TestCheckValues_WarnReportsFailuresAsWarnings(t *testing.T) {
	app := cvApp(t, cvToml(), map[string]CheckOutcome{"lint": failOutcome("2 problems", "bad a", "bad b")},
		cvValues(map[string][2]string{"lint": {"warn", "demo:lint in q.toml"}}))
	r := app.Test([]string{"check", "--name", "lint"})
	if r.ExitCode != 1 || !strings.Contains(r.Stdout, "WARN  lint") || !strings.Contains(r.Stdout, "[warn] bad a") || strings.Contains(r.Stdout, "[error]") {
		t.Fatalf("got %d %q", r.ExitCode, r.Stdout)
	}
	f := app.Test([]string{"failing-checks", "--name", "lint"})
	if f.ExitCode != 0 || f.Stdout != "" {
		t.Fatalf("failing-checks: %d %q", f.ExitCode, f.Stdout)
	}
}

func TestCheckValues_ErrorRunsAsRegistered(t *testing.T) {
	app := cvApp(t, cvToml(), map[string]CheckOutcome{"lint": failOutcome("broken", "bad")},
		cvValues(map[string][2]string{"lint": {"error", "demo:lint in q.toml"}}))
	if r := app.Test([]string{"failing-checks", "--name", "lint"}); r.ExitCode != 1 || !strings.Contains(r.Stdout, "FAIL  lint") {
		t.Fatalf("got %d %q", r.ExitCode, r.Stdout)
	}
}

func TestCheckValues_NoValueMeansDefault(t *testing.T) {
	app := cvApp(t, cvToml(), nil, func(string) (CheckValue, bool) { return CheckValue{}, false })
	for _, it := range cvPayload(t, app.Test([]string{"check", "--list", "--json"})) {
		if it.Value != it.Severity || it.Source != "default" {
			t.Fatalf("item: %+v", it)
		}
	}
}

func TestCheckValues_AboveRegisteredSeverityIsRefusedAndTheFixClears(t *testing.T) {
	app := cvApp(t, cvToml(), nil, cvValues(map[string][2]string{"fmt": {"error", "demo:fmt in q.toml"}}))
	r := app.Test([]string{"check", "--name", "fmt"})
	want := "error: check \"fmt\": the check value resolver returned \"error\" (from demo:fmt in q.toml) for a check registered as \"warn\"; a check value may lower a check's severity, never raise it\n"
	if r.ExitCode != 1 || !strings.HasPrefix(r.Stderr, want) {
		t.Fatalf("got %d %q", r.ExitCode, r.Stderr)
	}
	fixed := cvApp(t, cvToml(), nil, cvValues(map[string][2]string{"fmt": {"warn", "demo:fmt in q.toml"}}))
	if r := fixed.Test([]string{"check", "--name", "fmt"}); r.ExitCode != 0 {
		t.Fatalf("fix did not clear: %d %q", r.ExitCode, r.Stderr)
	}
}

func TestCheckValues_OutsideTheThreeIsRefusedAndTheFixClears(t *testing.T) {
	app := cvApp(t, cvToml(), nil, cvValues(map[string][2]string{"lint": {"on", "demo:lint in q.toml"}}))
	r := app.Test([]string{"check", "--list"})
	if r.ExitCode != 1 || !strings.HasPrefix(r.Stderr, "error: check \"lint\": the check value resolver returned \"on\"; a check value is one of error, warn, off\n") {
		t.Fatalf("got %d %q", r.ExitCode, r.Stderr)
	}
	fixed := cvApp(t, cvToml(), nil, cvValues(map[string][2]string{"lint": {"off", "demo:lint in q.toml"}}))
	if r := fixed.Test([]string{"check", "--list"}); r.ExitCode != 0 {
		t.Fatalf("fix did not clear: %d %q", r.ExitCode, r.Stderr)
	}
}

func TestCheckValues_EmptySourceIsRefusedAndTheFixClears(t *testing.T) {
	app := cvApp(t, cvToml(), nil, cvValues(map[string][2]string{"lint": {"off", ""}}))
	r := app.Test([]string{"failing-checks", "--all"})
	if r.ExitCode != 1 || !strings.HasPrefix(r.Stderr, "error: check \"lint\": the check value resolver returned \"off\" with an empty source; name where the value came from\n") {
		t.Fatalf("got %d %q", r.ExitCode, r.Stderr)
	}
	fixed := cvApp(t, cvToml(), nil, cvValues(map[string][2]string{"lint": {"off", "demo:lint"}}))
	if r := fixed.Test([]string{"failing-checks", "--all"}); r.ExitCode != 0 {
		t.Fatalf("fix did not clear: %d %q", r.ExitCode, r.Stderr)
	}
}

func cvDepToml() string {
	return "app = \"testapp\"\n\n" + cvCheck("base", []string{"t"}, "error") + cvCheck("top", []string{"t"}, "error", "base")
}

func cvStatuses(items []cvItem) map[string]string {
	m := map[string]string{}
	for _, it := range items {
		m[it.Name] = it.Status
	}
	return m
}

func TestCheckValues_WarnDependencyDoesNotBlock(t *testing.T) {
	app := cvApp(t, cvDepToml(), map[string]CheckOutcome{"base": failOutcome("base broken", "x")},
		cvValues(map[string][2]string{"base": {"warn", "demo:base"}}))
	got := cvStatuses(cvPayload(t, app.Test([]string{"check", "--all", "--json"})))
	if got["base"] != "warn" || got["top"] != "pass" {
		t.Fatalf("got %v", got)
	}
}

func TestCheckValues_ErrorDependencyStillBlocks(t *testing.T) {
	app := cvApp(t, cvDepToml(), map[string]CheckOutcome{"base": failOutcome("broken", "x")}, nil)
	got := cvStatuses(cvPayload(t, app.Test([]string{"check", "--name", "top", "--json"})))
	if got["base"] != "fail" || got["top"] != "skip" {
		t.Fatalf("got %v", got)
	}
}

func TestCheckValues_OffDependencyDoesNotBlock(t *testing.T) {
	app := cvApp(t, cvDepToml(), map[string]CheckOutcome{"base": failOutcome("base broken", "x")},
		cvValues(map[string][2]string{"base": {"off", "demo:base"}}))
	r := app.Test([]string{"check", "--name", "top"})
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "OFF   base    off: demo:base") || !strings.Contains(r.Stdout, "PASS  top ") {
		t.Fatalf("got %d %q", r.ExitCode, r.Stdout)
	}
}

func TestCheckValues_BrokenCheckResolvedWarnStillFails(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(writeChecksFile(t, "app = \"testapp\"\n\n"+cvCheck("lint", []string{"t"}, "error"))))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("lint", func(CheckContext, *ErrorReporter) CheckOutcome { panic(checkAborted{msg: "kaboom"}) })
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: emptyProjectRoot} })
	app.SetCheckValueResolver(cvValues(map[string][2]string{"lint": {"warn", "demo:lint"}}))
	r := app.Test([]string{"failing-checks", "--name", "lint"})
	if r.ExitCode != 1 || !strings.Contains(r.Stdout, `check "lint" aborted with checkAborted: kaboom`) {
		t.Fatalf("got %d %q", r.ExitCode, r.Stdout)
	}
}

func TestCheckValues_VerboseSummaryCountsOff(t *testing.T) {
	app := cvApp(t, cvToml(), nil, cvValues(map[string][2]string{"lint": {"off", "demo:lint"}}))
	if r := app.Test([]string{"--verbose", "check", "--hook", "pre-push"}); !strings.Contains(r.Stdout, "1 passed / 0 failed / 0 warned / 0 skipped / 1 off") {
		t.Fatalf("got %q", r.Stdout)
	}
}

func TestCheckValues_RunChecksAppliesTheResolver(t *testing.T) {
	app := cvApp(t, cvToml(), map[string]CheckOutcome{"lint": failOutcome("broken", "x")},
		cvValues(map[string][2]string{"lint": {"warn", "demo:lint"}, "docs": {"off", "demo:docs"}}))
	results, _, exitCode, err := app.RunChecks(&testCheckContext{root: emptyProjectRoot}, RunChecksOptions{RunAll: true})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]string{}
	for _, r := range results {
		by[r.Name] = r.Status()
		if r.Gated() {
			t.Fatalf("%s gated", r.Name)
		}
	}
	if by["lint"] != "warn" || by["docs"] != "off" || exitCode != 1 {
		t.Fatalf("got %v exit %d", by, exitCode)
	}
	bad := cvApp(t, cvToml(), nil, cvValues(map[string][2]string{"fmt": {"error", "demo:fmt"}}))
	if _, _, _, err := bad.RunChecks(&testCheckContext{root: emptyProjectRoot}, RunChecksOptions{RunAll: true}); err == nil || !strings.Contains(err.Error(), "never raise it") {
		t.Fatalf("expected refusal, got %v", err)
	}
}

func TestCheckList_ShowsValueAndSource(t *testing.T) {
	app := cvApp(t, cvToml(), nil, cvValues(map[string][2]string{
		"lint": {"off", "demo:lint in options/quality.toml"},
		"docs": {"warn", "demo:docs in options/docs.toml"},
	}))
	r := app.Test([]string{"check", "--list"})
	want := "NAME    TAGS              SEVERITY   VALUE   SOURCE\n" +
		"bench   preflight, slow   error      error   default\n" +
		"docs    preflight         error      warn    demo:docs in options/docs.toml\n" +
		"fmt     prepush           warn       warn    default\n" +
		"lint    prepush           error      off     demo:lint in options/quality.toml\n"
	if r.ExitCode != 0 || r.Stdout != want {
		t.Fatalf("got %d %q", r.ExitCode, r.Stdout)
	}
	for _, it := range cvPayload(t, app.Test([]string{"check", "--list", "--json"})) {
		if it.Name == "lint" && (it.Value != "off" || it.Source != "demo:lint in options/quality.toml") {
			t.Fatalf("lint: %+v", it)
		}
	}
	if r := app.Test([]string{"failing-checks", "--list"}); r.ExitCode != 0 || !strings.HasPrefix(r.Stdout, "NAME ") {
		t.Fatalf("failing-checks --list: %d %q", r.ExitCode, r.Stdout)
	}
}

// --- failing-checks --------------------------------------------------------

func TestFailingChecks_PrintsOnlyErrorLevelFailures(t *testing.T) {
	app := cvApp(t, cvToml(), map[string]CheckOutcome{
		"lint": failOutcome("lint broken", "bad"),
		"fmt":  warnOutcome("fmt drift", "drift"),
	}, nil)
	r := app.Test([]string{"failing-checks", "--all"})
	if r.ExitCode != 1 || r.Stdout != "FAIL  lint    lint broken\n        [error] bad\n" {
		t.Fatalf("got %d %q", r.ExitCode, r.Stdout)
	}
	items := cvPayload(t, app.Test([]string{"failing-checks", "--all", "--json"}))
	if len(items) != 1 || items[0].Name != "lint" {
		t.Fatalf("items: %+v", items)
	}
}

func TestFailingChecks_WarningsAloneExitZero(t *testing.T) {
	app := cvApp(t, cvToml(), map[string]CheckOutcome{"fmt": warnOutcome("fmt drift", "drift")}, nil)
	if r := app.Test([]string{"failing-checks", "--all"}); r.ExitCode != 0 || r.Stdout != "" {
		t.Fatalf("got %d %q", r.ExitCode, r.Stdout)
	}
	if c := app.Test([]string{"check", "--all"}); c.ExitCode != 1 || !strings.Contains(c.Stdout, "WARN  fmt") {
		t.Fatalf("check: %d %q", c.ExitCode, c.Stdout)
	}
}

func TestFailingChecks_NoFlagsShowsHelp(t *testing.T) {
	r := cvApp(t, cvToml(), nil, nil).Test([]string{"failing-checks"})
	if r.ExitCode != 0 || !strings.HasPrefix(r.Stdout, "testapp failing-checks -- ") {
		t.Fatalf("got %d %q", r.ExitCode, r.Stdout)
	}
}

func TestFailingChecks_TakesTagAndName(t *testing.T) {
	app := cvApp(t, cvToml(), map[string]CheckOutcome{"lint": failOutcome("broken", "x")}, nil)
	if r := app.Test([]string{"failing-checks", "--tag", "prepush"}); r.ExitCode != 1 {
		t.Fatalf("tag: %d", r.ExitCode)
	}
	if r := app.Test([]string{"failing-checks", "--name", "docs"}); r.ExitCode != 0 {
		t.Fatalf("name: %d", r.ExitCode)
	}
}

func TestFailingChecks_InAppHelpWithTheSameFlags(t *testing.T) {
	app := cvApp(t, cvToml(), nil, nil)
	r := app.Test([]string{"--help"})
	re := regexp.MustCompile(`(?m)^  failing-checks\s+Run project checks and report only error-level failures, exiting nonzero when any exist$`)
	if !re.MatchString(r.Stdout) {
		t.Fatalf("app help: %q", r.Stdout)
	}
	names := map[string]bool{}
	for _, f := range app.commands["failing-checks"].flags {
		names[f.Name] = true
	}
	for _, want := range []string{"all", "tag", "name", "hook", "list"} {
		if !names[want] {
			t.Fatalf("missing flag %q: %v", want, names)
		}
	}
	if len(names) != 5 {
		t.Fatalf("flags: %v", names)
	}
	if r := app.Test([]string{"failing-checks", "--all", "--ignore-warnings"}); r.ExitCode != 1 || !strings.Contains(r.Stderr, "--ignore-warnings") {
		t.Fatalf("got %d %q", r.ExitCode, r.Stderr)
	}
}
