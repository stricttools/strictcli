package strictcli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// passOutcome / failOutcome / warnOutcome / skipOutcome are in-package test
// minters. They exercise the real reporter minting path (the only way to obtain
// a CheckOutcome) so that behavioral tests can express expected outcomes
// concisely. failOutcome/warnOutcome mint one problem per variadic arg (or a
// single problem from the message when none are given).
func passOutcome(msg string) CheckOutcome {
	r := &ErrorReporter{}
	return r.Passed(msg)
}

func skipOutcome(reason string) CheckOutcome {
	r := &ErrorReporter{}
	return r.Skipped(reason)
}

func failOutcome(msg string, problems ...string) CheckOutcome {
	r := &ErrorReporter{}
	if len(problems) == 0 {
		r.Error(msg)
	}
	for _, p := range problems {
		r.Error(p)
	}
	return r.Found(msg)
}

func warnOutcome(msg string, problems ...string) CheckOutcome {
	r := &ErrorReporter{}
	if len(problems) == 0 {
		r.Warn(msg)
	}
	for _, p := range problems {
		r.Warn(p)
	}
	return r.Found(msg)
}

func TestCheckOutcome_DerivedStatus(t *testing.T) {
	// A minted outcome's verdict is derived: passed => pass, skipped => skip,
	// found with an error problem => fail, found with only warns => warn.
	cases := []struct {
		outcome CheckOutcome
		want    string
	}{
		{passOutcome("all good"), "pass"},
		{skipOutcome("n/a"), "skip"},
		{failOutcome("broken", "detail1", "detail2"), "fail"},
		{warnOutcome("minor"), "warn"},
	}
	for _, c := range cases {
		if got := deriveStatus(c.outcome); got != c.want {
			t.Errorf("deriveStatus = %q, want %q", got, c.want)
		}
	}
	// Mixed: an error check that reports both an error and a warn derives FAIL,
	// and preserves both problems (error grouped before warn).
	er := &ErrorReporter{}
	er.Warn("a warning")
	er.Error("an error")
	mixed := er.Found("mixed findings")
	if deriveStatus(mixed) != "fail" {
		t.Fatalf("mixed outcome should derive fail, got %q", deriveStatus(mixed))
	}
	ordered := mixed.orderedProblems()
	if len(ordered) != 2 || ordered[0].severity != "error" || ordered[1].severity != "warn" {
		t.Fatalf("expected error-first ordering, got %+v", ordered)
	}
}

func writeToml(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "checks.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write checks.toml: %v", err)
	}
	return path
}

func TestLoadChecksToml_Valid(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
app = "testapp"

[checks.lint-code]
description = "Checks lint-code"
subject = "quality"
tags = ["code", "fast"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.check-deps]
description = "Checks check-deps"
subject = "quality"
tags = ["deps"]
severity = "warn"
fast = false
pure = false
needs_network = true
depends_on = ["lint-code"]
`)

	_, defs, order, err := loadChecksToml(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(defs))
	}
	if len(order) != 2 {
		t.Fatalf("expected 2 names in order, got %d", len(order))
	}

	t.Run("lint-code fields", func(t *testing.T) {
		d := defs["lint-code"]
		if d == nil {
			t.Fatal("lint-code not found")
		}
		if d.name != "lint-code" {
			t.Errorf("expected name 'lint-code', got %q", d.name)
		}
		if len(d.tags) != 2 || d.tags[0] != "code" || d.tags[1] != "fast" {
			t.Errorf("unexpected tags: %v", d.tags)
		}
		if d.severity != "error" {
			t.Errorf("expected severity 'error', got %q", d.severity)
		}
		if !d.fast {
			t.Error("expected fast=true")
		}
		if !d.pure {
			t.Error("expected pure=true")
		}
		if d.needsNetwork {
			t.Error("expected needs_network=false")
		}
		if len(d.dependsOn) != 0 {
			t.Errorf("expected empty depends_on, got %v", d.dependsOn)
		}
		if d.impl != nil {
			t.Error("expected impl to be nil")
		}
	})

	t.Run("check-deps fields", func(t *testing.T) {
		d := defs["check-deps"]
		if d == nil {
			t.Fatal("check-deps not found")
		}
		if d.severity != "warn" {
			t.Errorf("expected severity 'warn', got %q", d.severity)
		}
		if d.fast {
			t.Error("expected fast=false")
		}
		if d.pure {
			t.Error("expected pure=false")
		}
		if !d.needsNetwork {
			t.Error("expected needs_network=true")
		}
		if len(d.dependsOn) != 1 || d.dependsOn[0] != "lint-code" {
			t.Errorf("unexpected depends_on: %v", d.dependsOn)
		}
	})
}

func TestLoadChecksToml_MissingField(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{
			name: "missing tags",
			toml: `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`,
			wantErr: `checks.toml: check "foo": missing required field "tags"`,
		},
		{
			name: "missing severity",
			toml: `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
fast = true
pure = true
needs_network = false
depends_on = []
`,
			wantErr: `checks.toml: check "foo": missing required field "severity"`,
		},
		{
			name: "missing fast",
			toml: `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
pure = true
needs_network = false
depends_on = []
`,
			wantErr: `checks.toml: check "foo": missing required field "fast"`,
		},
		{
			name: "missing pure",
			toml: `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
needs_network = false
depends_on = []
`,
			wantErr: `checks.toml: check "foo": missing required field "pure"`,
		},
		{
			name: "missing needs_network",
			toml: `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
pure = true
depends_on = []
`,
			wantErr: `checks.toml: check "foo": missing required field "needs_network"`,
		},
		{
			name: "missing depends_on",
			toml: `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
pure = true
needs_network = false
`,
			wantErr: `checks.toml: check "foo": missing required field "depends_on"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeToml(t, dir, tc.toml)
			_, _, _, err := loadChecksToml(path)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() != tc.wantErr {
				t.Errorf("expected error %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestLoadChecksToml_WrongTypes(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr string
	}{
		{
			name: "tags is string instead of array",
			toml: `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = "not-an-array"
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`,
			wantErr: `checks.toml: check "foo": "tags" must be a list of strings`,
		},
		{
			name: "severity is integer",
			toml: `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = 42
fast = true
pure = true
needs_network = false
depends_on = []
`,
			wantErr: "checks.toml: check \"foo\": \"severity\" must be \"error\" or \"warn\", got '*'",
		},
		{
			name: "fast is string",
			toml: `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
fast = "yes"
pure = true
needs_network = false
depends_on = []
`,
			wantErr: `checks.toml: check "foo": "fast" must be a boolean, got str`,
		},
		{
			name: "invalid severity value",
			toml: `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "critical"
fast = true
pure = true
needs_network = false
depends_on = []
`,
			wantErr: `checks.toml: check "foo": "severity" must be "error" or "warn", got "critical"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeToml(t, dir, tc.toml)
			_, _, _, err := loadChecksToml(path)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() != tc.wantErr {
				t.Errorf("expected error %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestLoadChecksToml_UnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
extra_field = "bad"
`)
	_, _, _, err := loadChecksToml(path)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), `unknown field "extra_field"`) {
		t.Errorf("expected error about unknown field, got %q", err.Error())
	}
}

func TestLoadChecksToml_UnknownTopLevelKey(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[metadata]
version = "1.0"
`)
	_, _, _, err := loadChecksToml(path)
	if err == nil {
		t.Fatal("expected error for unknown top-level key")
	}
	if !strings.Contains(err.Error(), `unknown top-level key "metadata"`) {
		t.Errorf("expected error about unknown top-level key, got %q", err.Error())
	}
}

func TestLoadChecksToml_InvalidCheckName(t *testing.T) {
	tests := []struct {
		name      string
		checkName string
	}{
		{"starts with number", "1bad"},
		{"uppercase", "BadName"},
		{"contains underscore", "bad_name"},
		{"starts with hyphen", "-bad"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			toml := `
app = "testapp"
[checks.` + tc.checkName + `]
tags = ["a"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`
			path := writeToml(t, dir, toml)
			_, _, _, err := loadChecksToml(path)
			if err == nil {
				t.Fatal("expected error for invalid check name")
			}
			if !strings.Contains(err.Error(), "invalid check name") {
				t.Errorf("expected error about invalid check name, got %q", err.Error())
			}
		})
	}
}

func TestLoadChecksToml_DependsOnValidation(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = ["nonexistent"]
`)
	_, _, _, err := loadChecksToml(path)
	if err == nil {
		t.Fatal("expected error for depends_on referencing unknown check")
	}
	if !strings.Contains(err.Error(), `depends_on references unknown check "nonexistent"`) {
		t.Errorf("expected error about unknown check reference, got %q", err.Error())
	}
}

func TestLoadChecksToml_EmptyTags(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = []
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`)
	_, defs, _, err := loadChecksToml(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs["foo"].tags) != 0 {
		t.Errorf("expected empty tags, got %v", defs["foo"].tags)
	}
}

func TestLoadChecksToml_FileNotFound(t *testing.T) {
	_, _, _, err := loadChecksToml("/nonexistent/checks.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadChecksToml_MissingChecksKey(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
[something]
foo = "bar"
`)
	_, _, _, err := loadChecksToml(path)
	if err == nil {
		t.Fatal("expected error for missing checks key")
	}
	if !strings.Contains(err.Error(), `unknown top-level key "something"`) {
		t.Errorf("expected error about unknown top-level key, got %q", err.Error())
	}
}

func TestLoadChecksToml_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, ``)
	_, _, _, err := loadChecksToml(path)
	if err == nil {
		t.Fatal("expected error for empty file")
	}
	if !strings.Contains(err.Error(), `missing required top-level key "app"`) {
		t.Errorf("expected error about missing app key, got %q", err.Error())
	}
}

// writeChecksFile writes TOML content to a temp file and returns its path.
// Used with WithChecks(path) to enable the check system without CWD discovery.
func writeChecksFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "checks.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write checks.toml: %v", err)
	}
	return path
}

// testCheckContext is a minimal CheckContext for testing.
type testCheckContext struct {
	root string
}

func (c *testCheckContext) ProjectRoot() string { return c.root }

// --- Discovery and Registration tests ---

const validChecksToml = `
app = "testapp"

[checks.lint-code]
description = "Checks lint-code"
subject = "quality"
tags = ["code", "fast"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.check-deps]
description = "Checks check-deps"
subject = "quality"
tags = ["deps"]
severity = "warn"
fast = false
pure = false
needs_network = true
depends_on = ["lint-code"]
`

func TestNewApp_WithChecks(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	if !app.checksEnabled {
		t.Fatal("expected checksEnabled to be true")
	}
	if len(app.checkDefs) != 2 {
		t.Fatalf("expected 2 check defs, got %d", len(app.checkDefs))
	}
	if app.checkDefs["lint-code"] == nil {
		t.Fatal("expected lint-code check def")
	}
	if app.checkDefs["check-deps"] == nil {
		t.Fatal("expected check-deps check def")
	}
}

func TestNewApp_NoWithChecks(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")
	if app.checksEnabled {
		t.Fatal("expected checksEnabled to be false when no WithChecks")
	}
	if app.checkDefs != nil {
		t.Fatal("expected checkDefs to be nil")
	}
}

func TestRegisterCheck_DeclaredName(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("lint-code", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})

	if app.checkDefs["lint-code"].impl == nil {
		t.Fatal("expected impl to be registered")
	}
}

func TestRegisterCheck_UndeclaredName_Panics(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for undeclared check name")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "not declared in checks.toml") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()

	app.RegisterErrorCheck("nonexistent", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
}

func TestRegisterCheck_DuplicatePanics(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("lint-code", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for duplicate registration")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "duplicate registration") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()

	app.RegisterErrorCheck("lint-code", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
}

func TestRegisterCheck_NoToml_Panics(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when checks not enabled")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "checks not enabled") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()

	app.RegisterErrorCheck("foo", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
}

func TestDoubleEntry_DeclaredButNotRegistered(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome {
		fmt.Print("hello")
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	// Only register one of two checks
	app.RegisterErrorCheck("lint-code", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	// check-deps is NOT registered

	r := app.Test([]string{"greet"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "checks declared in checks.toml but not registered") {
		t.Fatalf("expected error about unregistered checks, got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "check-deps") {
		t.Fatalf("expected check-deps in error message, got %q", r.Stderr)
	}
}

func TestDoubleEntry_AllRegistered_NoError(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome {
		fmt.Print("hello")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	app.RegisterErrorCheck("lint-code", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterWarnCheck("check-deps", func(ctx CheckContext, _ *WarnReporter) CheckOutcome {
		return passOutcome("ok")
	})

	r := app.Test([]string{"greet"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if strings.Contains(r.Stdout, "hello") == false {
		t.Fatalf("expected stdout to contain 'hello', got %q", r.Stdout)
	}
}

// --- Runner tests ---

func makeCheckDefs(defs map[string]struct {
	tags      []string
	severity  string
	dependsOn []string
	impl      func(CheckContext) CheckOutcome
}) map[string]*checkDef {
	result := make(map[string]*checkDef, len(defs))
	for name, d := range defs {
		result[name] = &checkDef{
			name:      name,
			tags:      d.tags,
			severity:  d.severity,
			fast:      true,
			pure:      true,
			dependsOn: d.dependsOn,
			impl:      d.impl,
		}
	}
	return result
}

func TestRunChecks_SinglePass(t *testing.T) {
	defs := makeCheckDefs(map[string]struct {
		tags      []string
		severity  string
		dependsOn []string
		impl      func(CheckContext) CheckOutcome
	}{
		"check-a": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome {
				return passOutcome("all good")
			},
		},
	})

	ctx := &testCheckContext{root: "/tmp/test"}
	results, _, exitCode := runChecks(defs, []string{"check-a"}, ctx, nil, false)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status() != "pass" {
		t.Fatalf("expected pass, got %q", results[0].Status())
	}
}

func TestRunChecks_SingleFail(t *testing.T) {
	defs := makeCheckDefs(map[string]struct {
		tags      []string
		severity  string
		dependsOn []string
		impl      func(CheckContext) CheckOutcome
	}{
		"check-a": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome {
				return failOutcome("broken")
			},
		},
	})

	ctx := &testCheckContext{root: "/tmp/test"}
	results, _, exitCode := runChecks(defs, []string{"check-a"}, ctx, nil, false)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if results[0].Status() != "fail" {
		t.Fatalf("expected fail, got %q", results[0].Status())
	}
}

func TestRunChecks_DependencyChain_Pass(t *testing.T) {
	callOrder := []string{}
	defs := makeCheckDefs(map[string]struct {
		tags      []string
		severity  string
		dependsOn []string
		impl      func(CheckContext) CheckOutcome
	}{
		"check-b": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome {
				callOrder = append(callOrder, "check-b")
				return passOutcome("ok")
			},
		},
		"check-a": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{"check-b"},
			impl: func(ctx CheckContext) CheckOutcome {
				callOrder = append(callOrder, "check-a")
				return passOutcome("ok")
			},
		},
	})

	ctx := &testCheckContext{root: "/tmp/test"}
	order, err := resolveCheckOrder(defs, map[string]bool{"check-a": true, "check-b": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	results, _, exitCode := runChecks(defs, order, ctx, nil, false)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// check-b should run before check-a
	if len(callOrder) != 2 || callOrder[0] != "check-b" || callOrder[1] != "check-a" {
		t.Fatalf("expected call order [check-b, check-a], got %v", callOrder)
	}
}

func TestRunChecks_DependencyFailure_Skip(t *testing.T) {
	defs := makeCheckDefs(map[string]struct {
		tags      []string
		severity  string
		dependsOn []string
		impl      func(CheckContext) CheckOutcome
	}{
		"check-b": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome {
				return failOutcome("broken")
			},
		},
		"check-a": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{"check-b"},
			impl: func(ctx CheckContext) CheckOutcome {
				t.Fatal("check-a should not have been called")
				return CheckOutcome{}
			},
		},
	})

	ctx := &testCheckContext{root: "/tmp/test"}
	// Order: check-b first, then check-a
	results, _, exitCode := runChecks(defs, []string{"check-b", "check-a"}, ctx, nil, false)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Status() != "fail" {
		t.Fatalf("expected check-b fail, got %q", results[0].Status())
	}
	if results[1].Status() != "skip" {
		t.Fatalf("expected check-a skip, got %q", results[1].Status())
	}
	if !strings.Contains(results[1].Outcome.message, `dependency "check-b" failed`) {
		t.Fatalf("expected skip message about check-b, got %q", results[1].Outcome.message)
	}
}

func TestRunChecks_TransitiveSkip(t *testing.T) {
	defs := makeCheckDefs(map[string]struct {
		tags      []string
		severity  string
		dependsOn []string
		impl      func(CheckContext) CheckOutcome
	}{
		"check-c": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome {
				return failOutcome("broken")
			},
		},
		"check-b": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{"check-c"},
			impl: func(ctx CheckContext) CheckOutcome {
				t.Fatal("check-b should not run")
				return CheckOutcome{}
			},
		},
		"check-a": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{"check-b"},
			impl: func(ctx CheckContext) CheckOutcome {
				t.Fatal("check-a should not run")
				return CheckOutcome{}
			},
		},
	})

	ctx := &testCheckContext{root: "/tmp/test"}
	results, _, exitCode := runChecks(defs, []string{"check-c", "check-b", "check-a"}, ctx, nil, false)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Status() != "fail" {
		t.Fatalf("expected check-c fail, got %q", results[0].Status())
	}
	if results[1].Status() != "skip" {
		t.Fatalf("expected check-b skip, got %q", results[1].Status())
	}
	if results[2].Status() != "skip" {
		t.Fatalf("expected check-a skip, got %q", results[2].Status())
	}
}

func TestResolveCheckOrder_CycleDetection(t *testing.T) {
	defs := map[string]*checkDef{
		"check-a": {
			name:      "check-a",
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{"check-b"},
		},
		"check-b": {
			name:      "check-b",
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{"check-a"},
		},
	}

	_, err := resolveCheckOrder(defs, map[string]bool{"check-a": true, "check-b": true})
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
	if !strings.Contains(err.Error(), "check dependency cycle") {
		t.Fatalf("expected cycle error, got %q", err.Error())
	}
}

func TestResolveCheckOrder_ThreeNodeCycle(t *testing.T) {
	defs := map[string]*checkDef{
		"a": {name: "a", tags: []string{"t"}, severity: "error", dependsOn: []string{"c"}},
		"b": {name: "b", tags: []string{"t"}, severity: "error", dependsOn: []string{"a"}},
		"c": {name: "c", tags: []string{"t"}, severity: "error", dependsOn: []string{"b"}},
	}

	_, err := resolveCheckOrder(defs, map[string]bool{"a": true, "b": true, "c": true})
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
	if !strings.Contains(err.Error(), "check dependency cycle") {
		t.Fatalf("expected cycle error, got %q", err.Error())
	}
}

func TestFilterChecks_ByTag(t *testing.T) {
	defs := map[string]*checkDef{
		"lint-code":  {name: "lint-code", tags: []string{"code", "fast"}, severity: "error"},
		"check-deps": {name: "check-deps", tags: []string{"deps"}, severity: "warn"},
		"lint-docs":  {name: "lint-docs", tags: []string{"docs", "fast"}, severity: "error"},
	}

	selected, err := filterChecks(defs, "fast", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedNames := []string{"lint-code", "lint-docs"}
	actualNames := sortedKeys(selected)
	sort.Strings(expectedNames)
	if len(actualNames) != len(expectedNames) {
		t.Fatalf("expected %v, got %v", expectedNames, actualNames)
	}
	for i := range expectedNames {
		if actualNames[i] != expectedNames[i] {
			t.Fatalf("expected %v, got %v", expectedNames, actualNames)
		}
	}
}

func TestFilterChecks_ByGlob(t *testing.T) {
	defs := map[string]*checkDef{
		"lint-code":  {name: "lint-code", tags: []string{"code"}, severity: "error"},
		"lint-docs":  {name: "lint-docs", tags: []string{"docs"}, severity: "error"},
		"check-deps": {name: "check-deps", tags: []string{"deps"}, severity: "warn"},
	}

	selected, err := filterChecks(defs, "", "lint-*", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	actualNames := sortedKeys(selected)
	expectedNames := []string{"lint-code", "lint-docs"}
	if len(actualNames) != len(expectedNames) {
		t.Fatalf("expected %v, got %v", expectedNames, actualNames)
	}
	for i := range expectedNames {
		if actualNames[i] != expectedNames[i] {
			t.Fatalf("expected %v, got %v", expectedNames, actualNames)
		}
	}
}

func TestFilterChecks_RunAll(t *testing.T) {
	defs := map[string]*checkDef{
		"lint-code":  {name: "lint-code", tags: []string{"code"}, severity: "error"},
		"check-deps": {name: "check-deps", tags: []string{"deps"}, severity: "warn"},
	}

	selected, err := filterChecks(defs, "", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(selected) != 2 {
		t.Fatalf("expected 2 selected checks, got %d", len(selected))
	}
}

func TestFilterChecks_TagAndGlob_Intersection(t *testing.T) {
	defs := map[string]*checkDef{
		"lint-code":  {name: "lint-code", tags: []string{"code", "fast"}, severity: "error"},
		"lint-docs":  {name: "lint-docs", tags: []string{"docs"}, severity: "error"},
		"check-deps": {name: "check-deps", tags: []string{"deps", "fast"}, severity: "warn"},
	}

	// tag "fast" matches lint-code and check-deps
	// glob "lint-*" matches lint-code and lint-docs
	// intersection: lint-code only
	selected, err := filterChecks(defs, "fast", "lint-*", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	actualNames := sortedKeys(selected)
	if len(actualNames) != 1 || actualNames[0] != "lint-code" {
		t.Fatalf("expected [lint-code], got %v", actualNames)
	}
}

func TestResolveCheckOrder_DependencyPullIn(t *testing.T) {
	// check-a depends on check-b, only check-a is selected
	// check-b should be pulled in
	defs := map[string]*checkDef{
		"check-a": {name: "check-a", tags: []string{"code"}, severity: "error", dependsOn: []string{"check-b"}},
		"check-b": {name: "check-b", tags: []string{"deps"}, severity: "error", dependsOn: []string{}},
	}

	order, err := resolveCheckOrder(defs, map[string]bool{"check-a": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 2 {
		t.Fatalf("expected 2 checks in order (pull-in), got %d: %v", len(order), order)
	}
	// check-b must come before check-a
	bIdx := -1
	aIdx := -1
	for i, name := range order {
		if name == "check-b" {
			bIdx = i
		}
		if name == "check-a" {
			aIdx = i
		}
	}
	if bIdx >= aIdx {
		t.Fatalf("expected check-b before check-a, got order: %v", order)
	}
}

func TestRunChecks_WarnExitsNonzero(t *testing.T) {
	defs := makeCheckDefs(map[string]struct {
		tags      []string
		severity  string
		dependsOn []string
		impl      func(CheckContext) CheckOutcome
	}{
		"check-a": {
			tags:      []string{"fast"},
			severity:  "warn",
			dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome {
				return warnOutcome("minor issue")
			},
		},
	})

	ctx := &testCheckContext{root: "/tmp/test"}

	// A warning makes the run exit 1.
	_, _, exitCode := runChecks(defs, []string{"check-a"}, ctx, nil, false)
	if exitCode != 1 {
		t.Fatalf("expected exit code 1 for a warning, got %d", exitCode)
	}
}

func TestFilterChecks_NeitherFilter(t *testing.T) {
	defs := map[string]*checkDef{
		"lint-code": {name: "lint-code", tags: []string{"code"}, severity: "error"},
	}

	selected, err := filterChecks(defs, "", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(selected) != 0 {
		t.Fatalf("expected empty selection, got %d", len(selected))
	}
}

func TestResolveCheckOrder_NoDependencies(t *testing.T) {
	defs := map[string]*checkDef{
		"check-a": {name: "check-a", tags: []string{"fast"}, severity: "error", dependsOn: []string{}},
		"check-b": {name: "check-b", tags: []string{"fast"}, severity: "error", dependsOn: []string{}},
	}

	order, err := resolveCheckOrder(defs, map[string]bool{"check-a": true, "check-b": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(order))
	}
	// Should be alphabetically sorted since no dependencies constrain order
	if order[0] != "check-a" || order[1] != "check-b" {
		t.Fatalf("expected [check-a, check-b], got %v", order)
	}
}

func TestRunChecks_WarnDependency_RunsDependent(t *testing.T) {
	// A warn satisfies a dependency: only FAIL (or cascade-skip) skips
	// dependents. The warn still makes the run exit non-zero when
	// the warning makes the run exit 1, but the dependent must run.
	aRan := false
	defs := makeCheckDefs(map[string]struct {
		tags      []string
		severity  string
		dependsOn []string
		impl      func(CheckContext) CheckOutcome
	}{
		"check-b": {
			tags:      []string{"fast"},
			severity:  "warn",
			dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome {
				return warnOutcome("warning")
			},
		},
		"check-a": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{"check-b"},
			impl: func(ctx CheckContext) CheckOutcome {
				aRan = true
				return passOutcome("ok")
			},
		},
	})

	ctx := &testCheckContext{root: "/tmp/test"}
	results, _, exitCode := runChecks(defs, []string{"check-b", "check-a"}, ctx, nil, false)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1 (a warning), got %d", exitCode)
	}
	if !aRan {
		t.Fatal("expected check-a to run when dependency warned")
	}
	if results[0].Status() != "warn" {
		t.Fatalf("expected check-b warn, got %q", results[0].Status())
	}
	if results[1].Status() != "pass" {
		t.Fatalf("expected check-a pass, got %q", results[1].Status())
	}
}

func TestRunChecks_WarnDependency_TransitiveDependentsRun(t *testing.T) {
	// warn -> dependent -> transitive dependent: the whole chain runs.
	defs := makeCheckDefs(map[string]struct {
		tags      []string
		severity  string
		dependsOn []string
		impl      func(CheckContext) CheckOutcome
	}{
		"check-c": {
			tags:      []string{"fast"},
			severity:  "warn",
			dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome {
				return warnOutcome("warning")
			},
		},
		"check-b": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{"check-c"},
			impl: func(ctx CheckContext) CheckOutcome {
				return passOutcome("ok")
			},
		},
		"check-a": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{"check-b"},
			impl: func(ctx CheckContext) CheckOutcome {
				return passOutcome("ok")
			},
		},
	})

	ctx := &testCheckContext{root: "/tmp/test"}
	results, _, exitCode := runChecks(defs, []string{"check-c", "check-b", "check-a"}, ctx, nil, false)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1 (a warning), got %d", exitCode)
	}
	if results[0].Status() != "warn" {
		t.Fatalf("expected check-c warn, got %q", results[0].Status())
	}
	if results[1].Status() != "pass" {
		t.Fatalf("expected check-b pass, got %q", results[1].Status())
	}
	if results[2].Status() != "pass" {
		t.Fatalf("expected check-a pass, got %q", results[2].Status())
	}
}

func TestRunChecks_WarnDependency_Runs(t *testing.T) {
	defs := makeCheckDefs(map[string]struct {
		tags      []string
		severity  string
		dependsOn []string
		impl      func(CheckContext) CheckOutcome
	}{
		"check-b": {
			tags:      []string{"fast"},
			severity:  "warn",
			dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome {
				return warnOutcome("warning")
			},
		},
		"check-a": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{"check-b"},
			impl: func(ctx CheckContext) CheckOutcome {
				return passOutcome("ok")
			},
		},
	})

	ctx := &testCheckContext{root: "/tmp/test"}
	results, _, exitCode := runChecks(defs, []string{"check-b", "check-a"}, ctx, nil, false)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1 (a warning), got %d", exitCode)
	}
	if results[0].Status() != "warn" {
		t.Fatalf("expected check-b warn, got %q", results[0].Status())
	}
	if results[1].Status() != "pass" {
		t.Fatalf("expected check-a pass, got %q", results[1].Status())
	}
}

// --- check command tests ---

const twoChecksToml = `
app = "testapp"

[checks.version-consistency]
description = "Checks version-consistency"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.changelog-coverage]
description = "Checks changelog-coverage"
subject = "quality"
tags = ["changelog", "pre-push"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = ["version-consistency"]
`

// makeAppWithChecks creates an app with checks enabled via WithChecks(path).
// Both checks pass by default. Override impls as needed after calling this.
func makeAppWithChecks(t *testing.T, checksPath string) *App {
	t.Helper()
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	if !app.checksEnabled {
		t.Fatal("expected checksEnabled after setting up checks dir")
	}
	return app
}

func TestCheckCommand_NoFlags_ShowsHelp(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.SetCheckContext(func() CheckContext {
		return &testCheckContext{root: emptyProjectRoot}
	})

	r := app.Test([]string{"check"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "check") {
		t.Fatalf("expected help output containing 'check', got %q", r.Stdout)
	}
	// Should mention --all flag in help
	if !strings.Contains(r.Stdout, "--all") {
		t.Fatalf("expected help output containing '--all', got %q", r.Stdout)
	}
}

func TestCheckCommand_List_Human(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})

	r := app.Test([]string{"check", "--list"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "NAME") {
		t.Fatalf("expected header NAME, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "changelog-coverage") {
		t.Fatalf("expected changelog-coverage in output, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "version-consistency") {
		t.Fatalf("expected version-consistency in output, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "release") {
		t.Fatalf("expected 'release' tag in output, got %q", r.Stdout)
	}
}

func TestCheckCommand_List_JSON(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})

	r := app.Test([]string{"check", "--list", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}

	// Parse JSON output
	var entries []struct {
		Name     string   `json:"name"`
		Tags     []string `json:"tags"`
		Severity string   `json:"severity"`
	}
	output := string(envelopePayload(t, r.Stdout))
	if err := json.Unmarshal([]byte(output), &entries); err != nil {
		t.Fatalf("failed to parse JSON: %v; output=%q", err, output)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// Sorted alphabetically: changelog-coverage first
	if entries[0].Name != "changelog-coverage" {
		t.Fatalf("expected first entry 'changelog-coverage', got %q", entries[0].Name)
	}
	if entries[1].Name != "version-consistency" {
		t.Fatalf("expected second entry 'version-consistency', got %q", entries[1].Name)
	}
}

func TestCheckCommand_All_Passing(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("1.0.0 across 2 targets")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("all commits covered")
	})
	app.SetCheckContext(func() CheckContext {
		return &testCheckContext{root: emptyProjectRoot}
	})

	r := app.Test([]string{"check", "--all"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q stdout=%q", r.ExitCode, r.Stderr, r.Stdout)
	}
	if !strings.Contains(r.Stdout, "PASS") {
		t.Fatalf("expected PASS in output, got %q", r.Stdout)
	}
}

func TestCheckCommand_All_WithFailure(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return failOutcome("version mismatch", "pyproject: 1.0.0", "package.json: 1.0.1")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		t.Fatal("changelog-coverage should not run if version-consistency fails")
		return CheckOutcome{}
	})
	app.SetCheckContext(func() CheckContext {
		return &testCheckContext{root: emptyProjectRoot}
	})

	r := app.Test([]string{"check", "--all"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "FAIL") {
		t.Fatalf("expected FAIL in output, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "SKIP") {
		t.Fatalf("expected SKIP in output (dependency skip), got %q", r.Stdout)
	}
}

func TestCheckCommand_TagFilter(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.SetCheckContext(func() CheckContext {
		return &testCheckContext{root: emptyProjectRoot}
	})

	// Only version-consistency has tag "release"
	r := app.Test([]string{"check", "--tag", "release"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "version-consistency") {
		t.Fatalf("expected version-consistency in output, got %q", r.Stdout)
	}
}

func TestCheckCommand_NameGlob(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.SetCheckContext(func() CheckContext {
		return &testCheckContext{root: emptyProjectRoot}
	})

	r := app.Test([]string{"check", "--name", "version-*"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "version-consistency") {
		t.Fatalf("expected version-consistency in output, got %q", r.Stdout)
	}
	// Should NOT contain changelog-coverage since it's not matched by version-*
	// (though it may appear as a pulled-in dependency -- version-consistency has no deps on it)
}

func TestCheckCommand_DryRun_RunsThePureChecks(t *testing.T) {
	// --dry-run IS the purity partition: both checks in this TOML are pure, so
	// both really run and the plan is empty.
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	var ran []string
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, r *ErrorReporter) CheckOutcome {
		ran = append(ran, "version-consistency")
		return r.Passed("versions agree")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, r *ErrorReporter) CheckOutcome {
		ran = append(ran, "changelog-coverage")
		return r.Passed("covered")
	})
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: emptyProjectRoot} })

	r := app.Test([]string{"--dry-run", "check", "--all"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if len(ran) != 2 {
		t.Fatalf("expected both pure checks to run, ran=%v", ran)
	}
	if !strings.Contains(r.Stdout, "PASS  version-consistency") {
		t.Fatalf("expected the executed result rows, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "Would run 0 checks:") {
		t.Fatalf("expected an empty plan, got %q", r.Stdout)
	}
}

func TestCheckCommand_DryRun_ListsTheImpureChecks(t *testing.T) {
	checksPath := writeChecksFile(t, partitionToml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	var ran []string
	for _, name := range []string{"pure-a", "net-b", "impure-c", "dep-on-impure", "dep-on-pure"} {
		n := name
		app.RegisterErrorCheck(n, func(ctx CheckContext, r *ErrorReporter) CheckOutcome {
			ran = append(ran, n)
			return r.Passed(n + " ok")
		})
	}
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: emptyProjectRoot} })

	r := app.Test([]string{"--dry-run", "check", "--all"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	sort.Strings(ran)
	if strings.Join(ran, ",") != "dep-on-pure,pure-a" {
		t.Fatalf("expected only the pure partition to run, ran=%v", ran)
	}
	if !strings.Contains(r.Stdout, "Would run 3 checks:") {
		t.Fatalf("expected three listed checks, got %q", r.Stdout)
	}
	// dep-on-impure is pure but joins the listing (its dependency did not run),
	// so the annotation still distinguishes the two reasons.
	if !strings.Contains(r.Stdout, "dep-on-impure (depends on: impure-c) [pure]") {
		t.Fatalf("expected the listed pure dependent, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "[impure]") {
		t.Fatalf("expected an [impure] annotation, got %q", r.Stdout)
	}
}

func TestCheckCommand_DryRun_FailingPureCheckFailsTheRun(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, r *ErrorReporter) CheckOutcome {
		r.Error("versions disagree")
		return r.Found("version mismatch")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, r *ErrorReporter) CheckOutcome {
		return r.Passed("covered")
	})
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: emptyProjectRoot} })

	r := app.Test([]string{"--dry-run", "check", "--all"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1 from a real failure found by the rehearsal, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "FAIL  version-consistency") {
		t.Fatalf("expected the failing row, got %q", r.Stdout)
	}
}

func TestCheckCommand_All_JSON(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("covered")
	})
	app.SetCheckContext(func() CheckContext {
		return &testCheckContext{root: emptyProjectRoot}
	})

	r := app.Test([]string{"check", "--all", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}

	var entries []struct {
		Name    string   `json:"name"`
		Status  string   `json:"status"`
		Message string   `json:"message"`
		Details []string `json:"details"`
	}
	output := string(envelopePayload(t, r.Stdout))
	if err := json.Unmarshal([]byte(output), &entries); err != nil {
		t.Fatalf("failed to parse JSON: %v; output=%q", err, output)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Status != "pass" {
			t.Fatalf("expected pass, got %q for %q", e.Status, e.Name)
		}
	}
}

func TestCheckCommand_All_Verbose(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("covered")
	})
	app.SetCheckContext(func() CheckContext {
		return &testCheckContext{root: emptyProjectRoot}
	})

	// A passing outcome carries NO problems (Passed hard-errors if any were
	// reported), so --verbose has nothing extra to reveal for a pure PASS: the
	// run succeeds and shows PASS rows, but no problem lines appear.
	r := app.Test([]string{"--verbose", "check", "--all"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "PASS") {
		t.Fatalf("expected PASS in verbose output, got %q", r.Stdout)
	}
	if strings.Contains(r.Stdout, "[error]") || strings.Contains(r.Stdout, "[warn]") {
		t.Fatalf("passing checks must show no problem lines, got %q", r.Stdout)
	}
}

func TestCheckCommand_IgnoreWarningsIsGone(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return warnOutcome("tag not pushed")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.SetCheckContext(func() CheckContext {
		return &testCheckContext{root: emptyProjectRoot}
	})

	// A warning makes check exit 1: check is the full report.
	r := app.Test([]string{"check", "--all"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1 for a warning, got %d", r.ExitCode)
	}

	// The --ignore-warnings escape hatch no longer exists.
	r = app.Test([]string{"check", "--all", "--ignore-warnings"})
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "--ignore-warnings") {
		t.Fatalf("expected --ignore-warnings to be refused, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestCheckCommand_NotInHelp_WithoutToml(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", r.ExitCode)
	}
	if strings.Contains(r.Stdout, "check") {
		t.Fatalf("check command should NOT appear in help when no TOML, got %q", r.Stdout)
	}
}

func TestCheckCommand_NoContextFactory_Error(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	// Deliberately NOT calling SetCheckContext

	r := app.Test([]string{"check", "--all"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1 without context factory, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "no check context factory set") {
		t.Fatalf("expected error about missing context factory, got stderr=%q", r.Stderr)
	}
}

// --- Schema integration tests ---

func TestDumpSchema_WithChecks(t *testing.T) {
	chdirTemp(t)
	checksPath := writeChecksFile(t, twoChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.Command("noop", "does nothing", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"help", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}

	data := []byte(r.Stdout)

	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("failed to parse schema JSON: %v", err)
	}

	checksRaw, ok := schema["checks"]
	if !ok {
		t.Fatal("expected 'checks' key in schema, not found")
	}
	checks, ok := checksRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("expected checks to be a map, got %T", checksRaw)
	}
	if len(checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(checks))
	}

	// Verify version-consistency metadata
	vcRaw, ok := checks["version-consistency"]
	if !ok {
		t.Fatal("expected version-consistency in checks")
	}
	vc := vcRaw.(map[string]interface{})
	if vc["severity"] != "error" {
		t.Errorf("expected severity 'error', got %v", vc["severity"])
	}
	if vc["fast"] != true {
		t.Errorf("expected fast=true, got %v", vc["fast"])
	}
	if vc["pure"] != true {
		t.Errorf("expected pure=true, got %v", vc["pure"])
	}
	if vc["needs_network"] != false {
		t.Errorf("expected needs_network=false, got %v", vc["needs_network"])
	}
	vcTags, ok := vc["tags"].([]interface{})
	if !ok {
		t.Fatalf("expected tags to be array, got %T", vc["tags"])
	}
	if len(vcTags) != 1 || vcTags[0] != "release" {
		t.Errorf("expected tags [release], got %v", vcTags)
	}
	vcDeps, ok := vc["depends_on"].([]interface{})
	if !ok {
		t.Fatalf("expected depends_on to be array, got %T", vc["depends_on"])
	}
	if len(vcDeps) != 0 {
		t.Errorf("expected empty depends_on, got %v", vcDeps)
	}

	// Verify changelog-coverage metadata
	ccRaw, ok := checks["changelog-coverage"]
	if !ok {
		t.Fatal("expected changelog-coverage in checks")
	}
	cc := ccRaw.(map[string]interface{})
	if cc["severity"] != "error" {
		t.Errorf("expected severity 'error', got %v", cc["severity"])
	}
	ccTags, ok := cc["tags"].([]interface{})
	if !ok {
		t.Fatalf("expected tags to be array, got %T", cc["tags"])
	}
	if len(ccTags) != 2 {
		t.Errorf("expected 2 tags, got %v", ccTags)
	}
	ccDeps, ok := cc["depends_on"].([]interface{})
	if !ok {
		t.Fatalf("expected depends_on to be array, got %T", cc["depends_on"])
	}
	if len(ccDeps) != 1 || ccDeps[0] != "version-consistency" {
		t.Errorf("expected depends_on [version-consistency], got %v", ccDeps)
	}
}

func TestDumpSchema_WithoutChecks(t *testing.T) {
	chdirTemp(t)
	app := NewApp("testapp", "1.0.0", "test app")
	app.Command("noop", "does nothing", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"help", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}

	data := []byte(r.Stdout)

	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("failed to parse schema JSON: %v", err)
	}

	if _, ok := schema["checks"]; ok {
		t.Fatal("expected no 'checks' key in schema when checks are disabled")
	}
}

// --- WithChecks tests ---

func TestChecksPath_ValidPath(t *testing.T) {
	tomlPath := writeChecksFile(t, validChecksToml)

	app := NewApp("testapp", "1.0.0", "test app", WithChecks(tomlPath))
	if !app.checksEnabled {
		t.Fatal("expected checksEnabled to be true with WithChecks")
	}
	if len(app.checkDefs) != 2 {
		t.Fatalf("expected 2 check defs, got %d", len(app.checkDefs))
	}
	if app.checkDefs["lint-code"] == nil {
		t.Fatal("expected lint-code check def")
	}
	if app.checkDefs["check-deps"] == nil {
		t.Fatal("expected check-deps check def")
	}
}

func TestChecksPath_NonexistentFile(t *testing.T) {
	bogusPath := filepath.Join(t.TempDir(), "nonexistent", "checks.toml")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nonexistent checks_path")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", r, r)
		}
		expected := "checks_path does not exist: " + bogusPath
		if msg != expected {
			t.Errorf("expected panic %q, got %q", expected, msg)
		}
	}()

	NewApp("testapp", "1.0.0", "test app", WithChecks(bogusPath))
}

// --- New tests for app field and explicit WithChecks ---

func TestLoadChecksToml_MissingAppField(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`)
	_, _, _, err := loadChecksToml(path)
	if err == nil {
		t.Fatal("expected error for missing app field")
	}
	expected := `checks.toml: missing required top-level key "app"`
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestLoadChecksToml_AppFieldWrongType(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
app = 42
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`)
	_, _, _, err := loadChecksToml(path)
	if err == nil {
		t.Fatal("expected error for wrong app type")
	}
	expected := `checks.toml: "app" must be a non-empty string`
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestLoadChecksToml_AppFieldEmpty(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
app = ""
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`)
	_, _, _, err := loadChecksToml(path)
	if err == nil {
		t.Fatal("expected error for empty app field")
	}
	expected := `checks.toml: "app" must be a non-empty string`
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

func TestLoadChecksToml_AppFieldOnly(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `app = "testapp"`)
	appName, defs, order, err := loadChecksToml(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if appName != "testapp" {
		t.Errorf("expected app name 'testapp', got %q", appName)
	}
	if len(defs) != 0 {
		t.Errorf("expected 0 check defs, got %d", len(defs))
	}
	if len(order) != 0 {
		t.Errorf("expected 0 order entries, got %d", len(order))
	}
}

func TestNewApp_AppMismatch(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
app = "wrong"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for app name mismatch")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `app "wrong" does not match app name "testapp"`) {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()

	NewApp("testapp", "1.0.0", "test app", WithChecks(path))
}

func TestNewApp_NoWithChecks_CWDIgnored(t *testing.T) {
	// Without WithChecks, checks should NOT be enabled — regardless of what
	// exists on disk. No need to chdir; the code never probes CWD.
	app := NewApp("testapp", "1.0.0", "test app")
	if app.checksEnabled {
		t.Fatal("expected checksEnabled to be false when WithChecks is not used")
	}
}

func TestRegisterCheck_NotEnabled_Message(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when checks not enabled")
		}
		msg := fmt.Sprintf("%v", r)
		expected := `cannot register check "foo": checks not enabled`
		if msg != expected {
			t.Fatalf("expected panic %q, got %q", expected, msg)
		}
	}()

	app.RegisterErrorCheck("foo", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
}

// --- WithChecksEmbed tests ---

func TestNewApp_WithChecksEmbed(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app", WithChecksEmbed([]byte(validChecksToml)))
	if !app.checksEnabled {
		t.Fatal("expected checksEnabled to be true")
	}
	if len(app.checkDefs) != 2 {
		t.Fatalf("expected 2 check defs, got %d", len(app.checkDefs))
	}
	if app.checkDefs["lint-code"] == nil {
		t.Fatal("expected lint-code check def")
	}
	if app.checkDefs["check-deps"] == nil {
		t.Fatal("expected check-deps check def")
	}
}

func TestWithChecksEmbed_And_WithChecks_Panics(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when both WithChecks and WithChecksEmbed are used")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", r, r)
		}
		expected := "cannot use both WithChecks and WithChecksEmbed"
		if msg != expected {
			t.Errorf("expected panic %q, got %q", expected, msg)
		}
	}()

	NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath), WithChecksEmbed([]byte(validChecksToml)))
}

func TestWithChecksEmbed_InvalidToml(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for invalid TOML data")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", r, r)
		}
		if !strings.Contains(msg, "checks.toml:") {
			t.Errorf("expected panic message to contain 'checks.toml:', got %q", msg)
		}
	}()

	NewApp("testapp", "1.0.0", "test app", WithChecksEmbed([]byte("not valid toml {{{{")))
}

func TestWithChecksEmbed_WrongAppName(t *testing.T) {
	toml := `
app = "wrong"

[checks.lint-code]
description = "Checks lint-code"
subject = "quality"
tags = ["code"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for wrong app name")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", r, r)
		}
		expected := `checks.toml: app "wrong" does not match app name "testapp"`
		if msg != expected {
			t.Errorf("expected panic %q, got %q", expected, msg)
		}
	}()

	NewApp("testapp", "1.0.0", "test app", WithChecksEmbed([]byte(toml)))
}

// --- Explicit skip behavior tests ---

func TestRunChecks_ExplicitSkip_ExitZero(t *testing.T) {
	defs := makeCheckDefs(map[string]struct {
		tags      []string
		severity  string
		dependsOn []string
		impl      func(CheckContext) CheckOutcome
	}{
		"check-a": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome {
				return skipOutcome("not applicable")
			},
		},
	})

	ctx := &testCheckContext{root: "/tmp/test"}
	results, _, exitCode := runChecks(defs, []string{"check-a"}, ctx, nil, false)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0 for explicit skip, got %d", exitCode)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status() != "skip" {
		t.Fatalf("expected skip, got %q", results[0].Status())
	}
}

func TestRunChecks_ExplicitSkip_NoCascade(t *testing.T) {
	bRan := false
	defs := makeCheckDefs(map[string]struct {
		tags      []string
		severity  string
		dependsOn []string
		impl      func(CheckContext) CheckOutcome
	}{
		"check-a": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome {
				return skipOutcome("not applicable")
			},
		},
		"check-b": {
			tags:      []string{"fast"},
			severity:  "error",
			dependsOn: []string{"check-a"},
			impl: func(ctx CheckContext) CheckOutcome {
				bRan = true
				return passOutcome("ok")
			},
		},
	})

	ctx := &testCheckContext{root: "/tmp/test"}
	results, _, exitCode := runChecks(defs, []string{"check-a", "check-b"}, ctx, nil, false)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !bRan {
		t.Fatal("expected check-b to run (not cascade-skipped)")
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Status() != "skip" {
		t.Fatalf("expected check-a skip, got %q", results[0].Status())
	}
	if results[1].Status() != "pass" {
		t.Fatalf("expected check-b pass, got %q", results[1].Status())
	}
}

// --- RunChecks public API tests ---

func makeAppWithRegisteredChecks(t *testing.T, toml string) *App {
	t.Helper()
	checksPath := writeChecksFile(t, toml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	return app
}

const threeChecksToml = `
app = "testapp"

[checks.check-a]
description = "Checks check-a"
subject = "quality"
tags = ["fast"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.check-b]
description = "Checks check-b"
subject = "quality"
tags = ["slow"]
severity = "error"
fast = false
pure = true
needs_network = false
depends_on = ["check-a"]

[checks.check-c]
description = "Checks check-c"
subject = "quality"
tags = ["fast"]
severity = "warn"
fast = true
pure = true
needs_network = false
depends_on = []
`

func TestRunChecks_AllPass(t *testing.T) {
	app := makeAppWithRegisteredChecks(t, threeChecksToml)
	app.RegisterErrorCheck("check-a", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("check-b", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterWarnCheck("check-c", func(ctx CheckContext, _ *WarnReporter) CheckOutcome {
		return passOutcome("ok")
	})

	ctx := &testCheckContext{root: emptyProjectRoot}
	results, _, exitCode, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Status() != "pass" {
			t.Fatalf("expected pass for %q, got %q", r.Name, r.Status())
		}
	}
}

func TestRunChecks_OneFail(t *testing.T) {
	app := makeAppWithRegisteredChecks(t, threeChecksToml)
	app.RegisterErrorCheck("check-a", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return failOutcome("broken")
	})
	app.RegisterErrorCheck("check-b", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterWarnCheck("check-c", func(ctx CheckContext, _ *WarnReporter) CheckOutcome {
		return passOutcome("ok")
	})

	ctx := &testCheckContext{root: emptyProjectRoot}
	results, _, exitCode, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	// check-a fails, check-b depends on check-a so it's skipped
	foundFail := false
	for _, r := range results {
		if r.Status() == "fail" {
			foundFail = true
		}
	}
	if !foundFail {
		t.Fatal("expected at least one fail status")
	}
}

func TestRunChecks_TagFiltering(t *testing.T) {
	app := makeAppWithRegisteredChecks(t, threeChecksToml)
	app.RegisterErrorCheck("check-a", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("check-b", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		t.Fatal("check-b should not run with tag=fast")
		return CheckOutcome{}
	})
	app.RegisterWarnCheck("check-c", func(ctx CheckContext, _ *WarnReporter) CheckOutcome {
		return passOutcome("ok")
	})

	ctx := &testCheckContext{root: emptyProjectRoot}
	// Only "fast" tagged checks: check-a and check-c
	results, _, exitCode, err := app.RunChecks(ctx, RunChecksOptions{TagExpr: "fast"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestRunChecks_NameGlob(t *testing.T) {
	app := makeAppWithRegisteredChecks(t, threeChecksToml)
	app.RegisterErrorCheck("check-a", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("check-b", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		t.Fatal("check-b should not run with glob check-a")
		return CheckOutcome{}
	})
	app.RegisterWarnCheck("check-c", func(ctx CheckContext, _ *WarnReporter) CheckOutcome {
		return passOutcome("ok")
	})

	ctx := &testCheckContext{root: emptyProjectRoot}
	results, _, exitCode, err := app.RunChecks(ctx, RunChecksOptions{NameGlob: "check-a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "check-a" {
		t.Fatalf("expected check-a, got %q", results[0].Name)
	}
}

func TestRunChecks_RunAll(t *testing.T) {
	app := makeAppWithRegisteredChecks(t, threeChecksToml)
	app.RegisterErrorCheck("check-a", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("check-b", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterWarnCheck("check-c", func(ctx CheckContext, _ *WarnReporter) CheckOutcome {
		return passOutcome("ok")
	})

	ctx := &testCheckContext{root: emptyProjectRoot}
	results, _, _, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results for RunAll, got %d", len(results))
	}
}

func TestRunChecks_DependencyOrdering(t *testing.T) {
	app := makeAppWithRegisteredChecks(t, threeChecksToml)
	callOrder := []string{}
	app.RegisterErrorCheck("check-a", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		callOrder = append(callOrder, "check-a")
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("check-b", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		callOrder = append(callOrder, "check-b")
		return passOutcome("ok")
	})
	app.RegisterWarnCheck("check-c", func(ctx CheckContext, _ *WarnReporter) CheckOutcome {
		callOrder = append(callOrder, "check-c")
		return passOutcome("ok")
	})

	ctx := &testCheckContext{root: emptyProjectRoot}
	_, _, _, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// check-b depends on check-a, so check-a must come before check-b
	aIdx := -1
	bIdx := -1
	for i, name := range callOrder {
		if name == "check-a" {
			aIdx = i
		}
		if name == "check-b" {
			bIdx = i
		}
	}
	if aIdx == -1 || bIdx == -1 {
		t.Fatalf("expected both check-a and check-b in call order, got %v", callOrder)
	}
	if aIdx >= bIdx {
		t.Fatalf("expected check-a before check-b, got order %v", callOrder)
	}
}

func TestRunChecks_DependencyFailureCascade(t *testing.T) {
	app := makeAppWithRegisteredChecks(t, threeChecksToml)
	app.RegisterErrorCheck("check-a", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return failOutcome("broken")
	})
	app.RegisterErrorCheck("check-b", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		t.Fatal("check-b should be skipped due to check-a failure")
		return CheckOutcome{}
	})
	app.RegisterWarnCheck("check-c", func(ctx CheckContext, _ *WarnReporter) CheckOutcome {
		return passOutcome("ok")
	})

	ctx := &testCheckContext{root: emptyProjectRoot}
	results, _, exitCode, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	// Find check-b result -- it should be skip
	for _, r := range results {
		if r.Name == "check-b" {
			if r.Status() != "skip" {
				t.Fatalf("expected check-b to be skipped, got %q", r.Status())
			}
			return
		}
	}
	t.Fatal("check-b not found in results")
}

func TestRunChecks_WarnExitCode(t *testing.T) {
	app := makeAppWithRegisteredChecks(t, threeChecksToml)
	app.RegisterErrorCheck("check-a", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("check-b", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterWarnCheck("check-c", func(ctx CheckContext, _ *WarnReporter) CheckOutcome {
		return warnOutcome("minor issue")
	})

	ctx := &testCheckContext{root: emptyProjectRoot}
	_, _, exitCode, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("expected exit code 1 for a warning, got %d", exitCode)
	}
}

func TestRunChecks_NoMatches(t *testing.T) {
	app := makeAppWithRegisteredChecks(t, threeChecksToml)
	app.RegisterErrorCheck("check-a", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("check-b", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterWarnCheck("check-c", func(ctx CheckContext, _ *WarnReporter) CheckOutcome {
		return passOutcome("ok")
	})

	ctx := &testCheckContext{root: emptyProjectRoot}
	results, _, exitCode, err := app.RunChecks(ctx, RunChecksOptions{NameGlob: "nonexistent-*"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0 for no matches, got %d", exitCode)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestRunChecks_ErrorWhenChecksNotEnabled(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")

	ctx := &testCheckContext{root: emptyProjectRoot}
	_, _, _, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true})
	if err == nil {
		t.Fatal("expected error when checks not enabled")
	}
	if !strings.Contains(err.Error(), "checks are not enabled") {
		t.Fatalf("expected 'checks are not enabled' error, got %q", err.Error())
	}
}

func TestRunChecks_ErrorWhenRegistrationsIncomplete(t *testing.T) {
	app := makeAppWithRegisteredChecks(t, threeChecksToml)
	// Only register one of three checks
	app.RegisterErrorCheck("check-a", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})

	ctx := &testCheckContext{root: emptyProjectRoot}
	_, _, _, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true})
	if err == nil {
		t.Fatal("expected error when registrations incomplete")
	}
	if !strings.Contains(err.Error(), "checks declared in checks.toml but not registered") {
		t.Fatalf("expected registration error, got %q", err.Error())
	}
}

// --- FormatCheckResults tests ---

func TestFormatCheckResults_NonEmpty(t *testing.T) {
	results := []CheckRunResult{
		{Name: "check-a", Outcome: passOutcome("all good")},
		{Name: "check-b", Outcome: failOutcome("broken")},
	}
	out := FormatCheckResults(results, false)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !strings.Contains(out, "PASS") {
		t.Fatalf("expected PASS label, got %q", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Fatalf("expected FAIL label, got %q", out)
	}
}

func TestFormatCheckResults_Labels(t *testing.T) {
	results := []CheckRunResult{
		{Name: "a", Outcome: passOutcome("ok")},
		{Name: "b", Outcome: failOutcome("bad")},
		{Name: "c", Outcome: warnOutcome("hmm")},
		{Name: "d", Outcome: skipOutcome("skipped")},
	}
	out := FormatCheckResults(results, false)
	if !strings.Contains(out, "PASS") {
		t.Fatalf("expected PASS, got %q", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Fatalf("expected FAIL, got %q", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Fatalf("expected WARN, got %q", out)
	}
	if !strings.Contains(out, "SKIP") {
		t.Fatalf("expected SKIP, got %q", out)
	}
}

func TestFormatCheckResults_PassHasNoProblemsEvenVerbose(t *testing.T) {
	// A passing outcome carries no problems, so neither the default nor the
	// verbose formatting emits any problem lines for it.
	results := []CheckRunResult{
		{Name: "check-a", Outcome: passOutcome("ok")},
	}
	nonVerbose := FormatCheckResults(results, false)
	if strings.Contains(nonVerbose, "[error]") || strings.Contains(nonVerbose, "[warn]") {
		t.Fatalf("expected no problem lines for pass, got %q", nonVerbose)
	}
	verbose := FormatCheckResults(results, true)
	if strings.Contains(verbose, "[error]") || strings.Contains(verbose, "[warn]") {
		t.Fatalf("expected no problem lines for pass even verbose, got %q", verbose)
	}
	// The FAIL/WARN problem lines DO show. A failing outcome's error problems
	// appear under its row.
	failing := []CheckRunResult{
		{Name: "check-b", Outcome: failOutcome("broken", "detail-line-1")},
	}
	out := FormatCheckResults(failing, false)
	if !strings.Contains(out, "detail-line-1") {
		t.Fatalf("expected problem line for fail, got %q", out)
	}
}

func TestFormatCheckResults_NoTrailingNewline(t *testing.T) {
	results := []CheckRunResult{
		{Name: "check-a", Outcome: passOutcome("ok")},
	}
	out := FormatCheckResults(results, false)
	if strings.HasSuffix(out, "\n") {
		t.Fatalf("expected no trailing newline, got %q", out)
	}
}

func TestFormatCheckResults_EmptyInput(t *testing.T) {
	out := FormatCheckResults(nil, false)
	if out != "" {
		t.Fatalf("expected empty string for nil input, got %q", out)
	}
	out = FormatCheckResults([]CheckRunResult{}, false)
	if out != "" {
		t.Fatalf("expected empty string for empty input, got %q", out)
	}
}

// --- FormatCheckResultsJSON tests ---

func TestFormatCheckResultsJSON_ValidJSON(t *testing.T) {
	results := []CheckRunResult{
		{Name: "check-a", Outcome: passOutcome("ok")},
		{Name: "check-b", Outcome: failOutcome("bad", "line1")},
	}
	out := FormatCheckResultsJSON(results)
	var parsed []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v; output=%q", err, out)
	}
	if len(parsed) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(parsed))
	}
}

func TestFormatCheckResultsJSON_EmptyProblemsNotNull(t *testing.T) {
	results := []CheckRunResult{
		{Name: "check-a", Outcome: passOutcome("ok")},
	}
	out := FormatCheckResultsJSON(results)
	// A passing result carries no problems: the field must serialize as [] not null.
	if strings.Contains(out, `"problems":null`) {
		t.Fatalf("expected problems to be [], not null, got %q", out)
	}
	if !strings.Contains(out, `"problems":[]`) {
		t.Fatalf("expected problems:[], got %q", out)
	}
}

func TestFormatCheckResultsJSON_NoTrailingNewline(t *testing.T) {
	results := []CheckRunResult{
		{Name: "check-a", Outcome: passOutcome("ok")},
	}
	out := FormatCheckResultsJSON(results)
	if strings.HasSuffix(out, "\n") {
		t.Fatalf("expected no trailing newline, got %q", out)
	}
}

func TestFormatCheckResultsJSON_FieldValues(t *testing.T) {
	results := []CheckRunResult{
		{Name: "check-a", Outcome: failOutcome("broken", "d1", "d2")},
	}
	out := FormatCheckResultsJSON(results)
	var parsed []struct {
		Name     string `json:"name"`
		Status   string `json:"status"`
		Message  string `json:"message"`
		Problems []struct {
			Severity string `json:"severity"`
			Text     string `json:"text"`
		} `json:"problems"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(parsed))
	}
	e := parsed[0]
	if e.Name != "check-a" {
		t.Errorf("expected name 'check-a', got %q", e.Name)
	}
	if e.Status != "fail" {
		t.Errorf("expected status 'fail', got %q", e.Status)
	}
	if e.Message != "broken" {
		t.Errorf("expected message 'broken', got %q", e.Message)
	}
	if len(e.Problems) != 2 || e.Problems[0].Text != "d1" || e.Problems[1].Text != "d2" {
		t.Errorf("expected problems [d1, d2], got %+v", e.Problems)
	}
	if e.Problems[0].Severity != "error" || e.Problems[1].Severity != "error" {
		t.Errorf("expected error-severity problems, got %+v", e.Problems)
	}
}

func TestLoadChecksToml_ScopeFieldAccepted(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
app = "testapp"
[checks.scoped-check]
description = "Checks scoped-check"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
scope = "changelog"
`)
	_, defs, _, err := loadChecksToml(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d := defs["scoped-check"]
	if d == nil {
		t.Fatal("scoped-check not found")
	}
	if d.scope != "changelog" {
		t.Errorf("expected scope 'changelog', got %q", d.scope)
	}
}

func TestLoadChecksToml_ScopeFieldAbsentDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
app = "testapp"
[checks.no-scope]
description = "Checks no-scope"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`)
	_, defs, _, err := loadChecksToml(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d := defs["no-scope"]
	if d == nil {
		t.Fatal("no-scope not found")
	}
	if d.scope != "" {
		t.Errorf("expected empty scope, got %q", d.scope)
	}
}

func TestLoadChecksToml_ScopeFieldWrongType(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
app = "testapp"
[checks.bad-scope]
description = "Checks bad-scope"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
scope = 42
`)
	_, _, _, err := loadChecksToml(path)
	if err == nil {
		t.Fatal("expected error for non-string scope")
	}
	if !strings.Contains(err.Error(), `"scope" must be a string`) {
		t.Errorf("expected error about scope type, got %q", err.Error())
	}
}

func TestLoadChecksToml_UnknownFieldStillRejectedWithScope(t *testing.T) {
	dir := t.TempDir()
	path := writeToml(t, dir, `
app = "testapp"
[checks.foo]
description = "Checks foo"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
scope = "changelog"
bogus = true
`)
	_, _, _, err := loadChecksToml(path)
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
	if !strings.Contains(err.Error(), `unknown field "bogus"`) {
		t.Errorf("expected error about unknown field, got %q", err.Error())
	}
}

func TestAddCheckDef_RejectsDuplicate(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)

	err := app.addCheckDef(&checkDef{name: "lint-code"})
	if err == nil {
		t.Fatal("expected duplicate error, got nil")
	}
	if !strings.Contains(err.Error(), `duplicate check definition "lint-code"`) {
		t.Errorf("expected duplicate error, got %q", err.Error())
	}
}

func TestAddCheckDef_InsertsAndSortsOrder(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)

	if err := app.addCheckDef(&checkDef{name: "aaa-first"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.checkDefs["aaa-first"] == nil {
		t.Fatal("expected aaa-first to be inserted")
	}
	// checkOrder must stay sorted after a dynamic addition.
	if !sort.StringsAreSorted(app.checkOrder) {
		t.Errorf("expected checkOrder sorted, got %v", app.checkOrder)
	}
	if app.checkOrder[0] != "aaa-first" {
		t.Errorf("expected aaa-first first after sort, got %v", app.checkOrder)
	}
}

func TestEnableChecks_IdempotentCommandRegistration(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)

	// Calling enableChecks again must not double-register the check command.
	app.enableChecks()
	app.enableChecks()

	count := 0
	for _, name := range app.cmdOrder {
		if name == "check" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected check command registered exactly once, got %d in cmdOrder %v", count, app.cmdOrder)
	}
	if !app.checksEnabled {
		t.Error("expected checksEnabled to remain true")
	}
	if len(app.checkDefs) != 2 {
		t.Errorf("expected 2 check defs preserved, got %d", len(app.checkDefs))
	}
}

// --- ceiling-typed outcome / reporter tests ---

func TestReporter_PassedWithProblemsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic: Passed after a problem was reported")
		} else if !strings.Contains(fmt.Sprintf("%v", r), "cannot pass") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	rep := &ErrorReporter{}
	rep.Error("found something")
	rep.Passed("all good") // must panic: a check that found problems cannot pass
}

func TestReporter_SkippedWithProblemsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic: Skipped after a problem was reported")
		}
	}()
	rep := &ErrorReporter{}
	rep.Warn("a warning")
	rep.Skipped("n/a") // must panic
}

func TestReporter_EmptyFoundPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic: Found with no problems")
		} else if !strings.Contains(fmt.Sprintf("%v", r), "use passed instead") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	rep := &ErrorReporter{}
	rep.Found("nothing here") // must panic: nothing found means pass
}

func TestReporter_EmptyInputsPanic(t *testing.T) {
	cases := []struct {
		name string
		fn   func()
	}{
		{"Warn empty", func() { (&ErrorReporter{}).Warn("") }},
		{"Error empty", func() { (&ErrorReporter{}).Error("") }},
		{"Passed empty", func() { (&ErrorReporter{}).Passed("") }},
		{"Skipped empty", func() { (&ErrorReporter{}).Skipped("") }},
		{"Found empty message", func() {
			rep := &ErrorReporter{}
			rep.Error("x")
			rep.Found("")
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("%s: expected panic for empty input", c.name)
				}
			}()
			c.fn()
		})
	}
}

func TestWarnReporter_LacksErrorMethod(t *testing.T) {
	// Structural guarantee: WarnReporter has no Error method, so a warn check
	// physically cannot mint an error-severity problem. This is enforced at
	// COMPILE time -- writing (&WarnReporter{}).Error("x") does not compile, so
	// a compile-failure proof is impossible from within a passing suite. We
	// assert the property via reflection as a regression guard against someone
	// accidentally promoting Error onto WarnReporter (e.g. by moving it to the
	// shared reporterCore).
	if _, ok := reflect.TypeOf(&WarnReporter{}).MethodByName("Error"); ok {
		t.Fatal("WarnReporter must NOT expose an Error method")
	}
	if _, ok := reflect.TypeOf(&ErrorReporter{}).MethodByName("Error"); !ok {
		t.Fatal("ErrorReporter must expose an Error method")
	}
	// Both reporters share the warn/passed/skipped/found minting surface.
	for _, m := range []string{"Warn", "Passed", "Skipped", "Found"} {
		if _, ok := reflect.TypeOf(&WarnReporter{}).MethodByName(m); !ok {
			t.Fatalf("WarnReporter must expose %s", m)
		}
		if _, ok := reflect.TypeOf(&ErrorReporter{}).MethodByName(m); !ok {
			t.Fatalf("ErrorReporter must expose %s", m)
		}
	}
}

func TestRegisterErrorCheck_OnWarnSeverity_Panics(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml) // check-deps is severity "warn"
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic registering a warn check via RegisterErrorCheck")
		}
		msg := fmt.Sprintf("%v", r)
		for _, want := range []string{"check-deps", `severity "warn"`, "RegisterErrorCheck", "use RegisterWarnCheck"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("panic message missing %q: %s", want, msg)
			}
		}
	}()
	app.RegisterErrorCheck("check-deps", func(ctx CheckContext, r *ErrorReporter) CheckOutcome {
		return r.Passed("ok")
	})
}

func TestRegisterWarnCheck_OnErrorSeverity_Panics(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml) // lint-code is severity "error"
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic registering an error check via RegisterWarnCheck")
		}
		msg := fmt.Sprintf("%v", r)
		for _, want := range []string{"lint-code", `severity "error"`, "RegisterWarnCheck", "use RegisterErrorCheck"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("panic message missing %q: %s", want, msg)
			}
		}
	}()
	app.RegisterWarnCheck("lint-code", func(ctx CheckContext, r *WarnReporter) CheckOutcome {
		return r.Passed("ok")
	})
}

func TestRunChecks_NonMintedOutcome_Panics(t *testing.T) {
	// Belt-and-braces: an impl that returns a zero (non-minted) CheckOutcome is
	// a hard error at the runner.
	defs := map[string]*checkDef{
		"check-a": {
			name: "check-a", tags: []string{"fast"}, severity: "error", dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome { return CheckOutcome{} },
		},
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for non-minted outcome")
		}
		if !strings.Contains(fmt.Sprintf("%v", r), "not minted by its reporter") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	runChecks(defs, []string{"check-a"}, &testCheckContext{root: emptyProjectRoot}, nil, false)
}

// checkAborted is a panic value with an Error() method, standing in for a
// broken check impl in the containment tests below.
type checkAborted struct{ msg string }

func (e checkAborted) Error() string { return e.msg }

func TestRunChecks_PanickingImpl_FailsOnlyItself(t *testing.T) {
	defs := map[string]*checkDef{
		"check-a": {
			name: "check-a", tags: []string{"fast"}, severity: "error", fast: true, pure: true,
			dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome {
				panic(checkAborted{msg: "boom"})
			},
		},
		"check-b": {
			name: "check-b", tags: []string{"fast"}, severity: "error", fast: true, pure: true,
			dependsOn: []string{},
			impl:      func(ctx CheckContext) CheckOutcome { return passOutcome("b ok") },
		},
	}
	results, _, exitCode := runChecks(defs, []string{"check-a", "check-b"},
		&testCheckContext{root: emptyProjectRoot}, nil, false)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Status() != "fail" {
		t.Fatalf("expected the panicking check to fail, got %q", results[0].Status())
	}
	want := `check "check-a" aborted with checkAborted: boom`
	if results[0].Outcome.message != want {
		t.Fatalf("expected message %q, got %q", want, results[0].Outcome.message)
	}
	if len(results[0].Outcome.problems) != 1 ||
		results[0].Outcome.problems[0].severity != "error" ||
		results[0].Outcome.problems[0].text != want {
		t.Fatalf("expected one error problem carrying the attribution, got %+v",
			results[0].Outcome.problems)
	}
	if results[1].Status() != "pass" {
		t.Fatalf("expected the second check to still run, got %q", results[1].Status())
	}
}

func TestRunChecks_PanickingImpl_CascadeSkipsDependents(t *testing.T) {
	defs := map[string]*checkDef{
		"check-a": {
			name: "check-a", tags: []string{"fast"}, severity: "error", fast: true, pure: true,
			dependsOn: []string{},
			impl:      func(ctx CheckContext) CheckOutcome { panic(checkAborted{msg: "boom"}) },
		},
		"check-b": {
			name: "check-b", tags: []string{"fast"}, severity: "error", fast: true, pure: true,
			dependsOn: []string{"check-a"},
			impl:      func(ctx CheckContext) CheckOutcome { return passOutcome("b ok") },
		},
	}
	results, _, exitCode := runChecks(defs, []string{"check-a", "check-b"},
		&testCheckContext{root: emptyProjectRoot}, nil, false)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if results[1].Status() != "skip" {
		t.Fatalf("expected the dependent to cascade-skip, got %q", results[1].Status())
	}
}

func TestRunChecks_PanickingWarnCheck_StillFails(t *testing.T) {
	// A warn check's findings are warnings; a broken check still fails.
	defs := map[string]*checkDef{
		"check-a": {
			name: "check-a", tags: []string{"fast"}, severity: "warn", fast: true, pure: true,
			dependsOn: []string{},
			impl:      func(ctx CheckContext) CheckOutcome { panic(checkAborted{msg: "boom"}) },
		},
	}
	results, _, exitCode := runChecks(defs, []string{"check-a"},
		&testCheckContext{root: emptyProjectRoot}, nil, false)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if results[0].Status() != "fail" {
		t.Fatalf("expected fail, got %q", results[0].Status())
	}
}

func TestCheckAbortAttribution_TypeAndMessageRendering(t *testing.T) {
	cases := []struct {
		name  string
		value interface{}
		want  string
	}{
		{"error value", checkAborted{msg: "boom"}, `check "c" aborted with checkAborted: boom`},
		{"pointer to error", &checkAborted{msg: "boom"}, `check "c" aborted with checkAborted: boom`},
		{"panicked string", "boom", `check "c" aborted with string: boom`},
		{"empty message", checkAborted{msg: ""}, `check "c" aborted with checkAborted`},
		{"non-error value", 42, `check "c" aborted with int: 42`},
	}
	for _, tc := range cases {
		got := mintCheckAbort("c", tc.value)
		if got.message != tc.want {
			t.Fatalf("%s: expected %q, got %q", tc.name, tc.want, got.message)
		}
		if !got.minted {
			t.Fatalf("%s: the contained outcome must be minted", tc.name)
		}
	}
}

func TestCheckRunResult_GatedWarnedConsistency(t *testing.T) {
	cases := []struct {
		outcome CheckOutcome
		status  string
		gated   bool
		warned  bool
	}{
		{passOutcome("ok"), "pass", false, false},
		{skipOutcome("n/a"), "skip", false, false},
		{failOutcome("bad", "x"), "fail", true, false},
		{warnOutcome("hmm"), "warn", false, true},
	}
	for _, c := range cases {
		r := CheckRunResult{Name: "c", Outcome: c.outcome}
		if r.Status() != c.status {
			t.Errorf("Status()=%q want %q", r.Status(), c.status)
		}
		if r.Gated() != c.gated {
			t.Errorf("Gated()=%v want %v (status %q)", r.Gated(), c.gated, c.status)
		}
		if r.Warned() != c.warned {
			t.Errorf("Warned()=%v want %v (status %q)", r.Warned(), c.warned, c.status)
		}
	}
}

func TestRunChecks_ErrorCheckOnlyWarns_NoCascade(t *testing.T) {
	// An error-severity check whose impl only reports warnings derives WARN,
	// which must NOT cascade to dependents (cascade keys on Gated/error only).
	bRan := false
	defs := map[string]*checkDef{
		"check-a": {
			name: "check-a", tags: []string{"fast"}, severity: "error", dependsOn: []string{},
			impl: func(ctx CheckContext) CheckOutcome { return warnOutcome("soft issue") },
		},
		"check-b": {
			name: "check-b", tags: []string{"fast"}, severity: "error", dependsOn: []string{"check-a"},
			impl: func(ctx CheckContext) CheckOutcome { bRan = true; return passOutcome("ok") },
		},
	}
	results, _, exitCode := runChecks(defs, []string{"check-a", "check-b"}, &testCheckContext{root: emptyProjectRoot}, nil, false)
	if !bRan {
		t.Fatal("dependent must run when dependency only warned")
	}
	if exitCode != 1 {
		t.Fatalf("expected exit 1 (warn, not ignored), got %d", exitCode)
	}
	if results[0].Status() != "warn" || results[1].Status() != "pass" {
		t.Fatalf("expected [warn, pass], got [%s, %s]", results[0].Status(), results[1].Status())
	}
}

func TestFormatCheckResults_MixedOutcomeShowsBothGroups(t *testing.T) {
	// A FAIL outcome carrying both error and warn problems shows both groups,
	// error problems first then warn problems.
	rep := &ErrorReporter{}
	rep.Warn("a warning line")
	rep.Error("an error line")
	mixed := rep.Found("mixed findings")
	out := FormatCheckResults([]CheckRunResult{{Name: "chk", Outcome: mixed}}, false)
	if !strings.Contains(out, "FAIL") {
		t.Fatalf("expected FAIL label, got %q", out)
	}
	eIdx := strings.Index(out, "[error] an error line")
	wIdx := strings.Index(out, "[warn] a warning line")
	if eIdx < 0 || wIdx < 0 {
		t.Fatalf("expected both problem groups, got %q", out)
	}
	if eIdx > wIdx {
		t.Fatalf("expected error problems before warn problems, got %q", out)
	}
}

// --- purity partition tests ---

// partitionToml mixes pure/network/impure checks and dependency edges so the
// purity partition can be exercised end to end. pure-a and dep-on-pure execute;
// net-b (needs network), impure-c (pure=false), and dep-on-impure (pure but
// depends on the listed impure-c) are listed, not executed.
const partitionToml = `
app = "testapp"

[checks.pure-a]
description = "Checks pure-a"
subject = "quality"
tags = ["p"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.net-b]
description = "Checks net-b"
subject = "quality"
tags = ["p"]
severity = "error"
fast = true
pure = true
needs_network = true
depends_on = []

[checks.impure-c]
description = "Checks impure-c"
subject = "quality"
tags = ["p"]
severity = "error"
fast = true
pure = false
needs_network = false
depends_on = []

[checks.dep-on-impure]
description = "Checks dep-on-impure"
subject = "quality"
tags = ["p"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = ["impure-c"]

[checks.dep-on-pure]
description = "Checks dep-on-pure"
subject = "quality"
tags = ["p"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = ["pure-a"]
`

func registerAllPassing(t *testing.T, app *App, names ...string) {
	t.Helper()
	for _, n := range names {
		app.RegisterErrorCheck(n, func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
			return passOutcome("ok")
		})
	}
}

func TestRunChecks_Partition_ExecutesPureListsImpure(t *testing.T) {
	app := makeAppWithRegisteredChecks(t, partitionToml)
	registerAllPassing(t, app, "pure-a", "net-b", "impure-c", "dep-on-impure", "dep-on-pure")

	ctx := &testCheckContext{root: emptyProjectRoot}
	results, impureListed, exitCode, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true, PureOnly: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit 0 (listed checks contribute no exit code), got %d", exitCode)
	}

	executed := map[string]bool{}
	for _, r := range results {
		executed[r.Name] = true
		if r.Status() != "pass" {
			t.Fatalf("expected pass for executed %q, got %q", r.Name, r.Status())
		}
	}
	wantExecuted := map[string]bool{"pure-a": true, "dep-on-pure": true}
	if !reflect.DeepEqual(executed, wantExecuted) {
		t.Fatalf("executed set = %v, want %v", executed, wantExecuted)
	}

	listed := map[string]bool{}
	for _, n := range impureListed {
		listed[n] = true
	}
	wantListed := map[string]bool{"net-b": true, "impure-c": true, "dep-on-impure": true}
	if !reflect.DeepEqual(listed, wantListed) {
		t.Fatalf("impureListed set = %v, want %v", listed, wantListed)
	}
	// Listed checks must NOT appear in results (outcome vocabulary stays pure).
	for _, r := range results {
		if wantListed[r.Name] {
			t.Fatalf("listed check %q leaked into results", r.Name)
		}
	}
}

func TestRunChecks_Partition_PureDependingOnImpureIsListed(t *testing.T) {
	// The decided cascade rule: a pure check whose dependency is impure (hence
	// unexecuted) cannot verify its precondition, so it joins the listing rather
	// than executing or failing.
	app := makeAppWithRegisteredChecks(t, partitionToml)
	ran := map[string]bool{}
	for _, n := range []string{"pure-a", "net-b", "impure-c", "dep-on-impure", "dep-on-pure"} {
		name := n
		app.RegisterErrorCheck(name, func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
			ran[name] = true
			return passOutcome("ok")
		})
	}

	ctx := &testCheckContext{root: emptyProjectRoot}
	_, impureListed, _, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true, PureOnly: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ran["dep-on-impure"] {
		t.Fatal("dep-on-impure (pure, depends on listed impure-c) must NOT execute")
	}
	found := false
	for _, n := range impureListed {
		if n == "dep-on-impure" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dep-on-impure in impureListed, got %v", impureListed)
	}
}

func TestRunChecks_Partition_FailedPureDependencyCascadesOverListing(t *testing.T) {
	// A genuinely FAILED pure dependency cascade-skips its pure dependent even
	// under PureOnly -- the failed-dependency cascade takes precedence over the
	// purity listing (the dependent is skipped, not listed).
	app := makeAppWithRegisteredChecks(t, partitionToml)
	app.RegisterErrorCheck("pure-a", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return failOutcome("pure-a failed", "boom")
	})
	registerAllPassing(t, app, "net-b", "impure-c", "dep-on-impure", "dep-on-pure")

	ctx := &testCheckContext{root: emptyProjectRoot}
	results, impureListed, exitCode, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true, PureOnly: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byName := map[string]string{}
	for _, r := range results {
		byName[r.Name] = r.Status()
	}
	if byName["pure-a"] != "fail" {
		t.Fatalf("pure-a status = %q, want fail", byName["pure-a"])
	}
	if byName["dep-on-pure"] != "skip" {
		t.Fatalf("dep-on-pure status = %q, want skip (cascade over listing)", byName["dep-on-pure"])
	}
	for _, n := range impureListed {
		if n == "dep-on-pure" {
			t.Fatal("dep-on-pure must cascade-skip, not be listed")
		}
	}
	if exitCode != 1 {
		t.Fatalf("exit = %d, want 1 (failed pure check gates)", exitCode)
	}
}

func TestRunChecks_Partition_ListedCheckContributesNoExitCode(t *testing.T) {
	// A check listed (not executed) under PureOnly contributes NOTHING to the
	// exit code -- even an impl that would fail never runs, so it cannot gate.
	app := makeAppWithRegisteredChecks(t, partitionToml)
	ran := map[string]bool{}
	app.RegisterErrorCheck("impure-c", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		ran["impure-c"] = true
		return failOutcome("would fail", "nope")
	})
	registerAllPassing(t, app, "pure-a", "net-b", "dep-on-impure", "dep-on-pure")

	ctx := &testCheckContext{root: emptyProjectRoot}
	_, impureListed, exitCode, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true, PureOnly: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ran["impure-c"] {
		t.Fatal("impure-c is listed under pure_only and must never execute")
	}
	listed := false
	for _, n := range impureListed {
		if n == "impure-c" {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("expected impure-c in impureListed, got %v", impureListed)
	}
	if exitCode != 0 {
		t.Fatalf("exit = %d, want 0 (a listed unexecuted check cannot gate)", exitCode)
	}
}

func TestRunChecks_Partition_OffIsUnchanged(t *testing.T) {
	// With PureOnly off, every selected check executes and nothing is listed.
	app := makeAppWithRegisteredChecks(t, partitionToml)
	registerAllPassing(t, app, "pure-a", "net-b", "impure-c", "dep-on-impure", "dep-on-pure")

	ctx := &testCheckContext{root: emptyProjectRoot}
	results, impureListed, exitCode, err := app.RunChecks(ctx, RunChecksOptions{RunAll: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit 0, got %d", exitCode)
	}
	if len(results) != 5 {
		t.Fatalf("expected all 5 checks executed, got %d", len(results))
	}
	if len(impureListed) != 0 {
		t.Fatalf("expected empty impureListed with partition off, got %v", impureListed)
	}
}

func TestCheckCommand_DryRun_NoMatchingChecks(t *testing.T) {
	// An empty selection reports no match rather than an empty plan -- the
	// no-match branch runs before the partition, exactly as in a full run.
	checksPath := writeChecksFile(t, twoChecksToml)
	app := makeAppWithChecks(t, checksPath)
	registerAllPassing(t, app, "version-consistency", "changelog-coverage")
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: emptyProjectRoot} })

	r := app.Test([]string{"--dry-run", "check", "--tag", "nonexistent"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "No checks matched the given filters.") {
		t.Fatalf("expected the no-match message, got %q", r.Stdout)
	}
	if strings.Contains(r.Stdout, "Would run") {
		t.Fatalf("expected no plan for an empty selection, got %q", r.Stdout)
	}
}

// Ensure unused imports don't cause errors
var _ = sort.Strings
var _ = fmt.Sprintf

// --- Verdict-inert notes channel + honest --verbose ---

// passWithNotes / foundWithNotes are in-package minters that attach notes via
// the real reporter path (Note), so notes tests exercise the sealed model.
func passWithNotes(msg string, notes ...string) CheckOutcome {
	r := &ErrorReporter{}
	for _, n := range notes {
		r.Note(n)
	}
	return r.Passed(msg)
}

func TestReporterNote_CarriedOnPass(t *testing.T) {
	o := passWithNotes("all good", "cached", "shortcut")
	if deriveStatus(o) != "pass" {
		t.Fatalf("expected pass, got %q", deriveStatus(o))
	}
	if len(o.notes) != 2 || o.notes[0] != "cached" || o.notes[1] != "shortcut" {
		t.Fatalf("expected notes carried, got %+v", o.notes)
	}
}

func TestReporterNote_CarriedOnFoundAndSkip(t *testing.T) {
	er := &ErrorReporter{}
	er.Note("aside")
	er.Error("real problem")
	found := er.Found("bad")
	if deriveStatus(found) != "fail" || len(found.notes) != 1 || found.notes[0] != "aside" {
		t.Fatalf("found: expected note carried, got status=%q notes=%+v", deriveStatus(found), found.notes)
	}

	sr := &ErrorReporter{}
	sr.Note("skip context")
	skip := sr.Skipped("nothing to do")
	if deriveStatus(skip) != "skip" || len(skip.notes) != 1 {
		t.Fatalf("skip: expected note carried, got status=%q notes=%+v", deriveStatus(skip), skip.notes)
	}
}

func TestReporterNote_AvailableOnWarnReporter(t *testing.T) {
	wr := &WarnReporter{}
	wr.Note("warn-reporter note")
	o := wr.Passed("fine")
	if len(o.notes) != 1 || o.notes[0] != "warn-reporter note" {
		t.Fatalf("expected note on warn reporter, got %+v", o.notes)
	}
}

func TestReporterNote_EmptyPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on empty note")
		}
		if !strings.Contains(fmt.Sprint(r), "note text must be a non-empty string") {
			t.Fatalf("unexpected panic message: %v", r)
		}
	}()
	(&ErrorReporter{}).Note("   ")
}

func TestReporterNote_DoesNotDefeatPassedSeal(t *testing.T) {
	// A note must NOT let a check with problems claim it passed.
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic: problems + Passed")
		}
		if !strings.Contains(fmt.Sprint(r), "cannot pass") {
			t.Fatalf("unexpected panic message: %v", r)
		}
	}()
	er := &ErrorReporter{}
	er.Note("informational")
	er.Warn("a real problem")
	er.Passed("nope")
}

func TestFormatCheckResults_NotesHiddenNormalShownVerbose(t *testing.T) {
	results := []CheckRunResult{
		{Name: "a", Outcome: passWithNotes("ok", "visible note")},
	}
	normal := FormatCheckResults(results, false)
	if strings.Contains(normal, "[note]") || strings.Contains(normal, "visible note") {
		t.Fatalf("notes must be hidden in normal mode, got %q", normal)
	}
	verbose := FormatCheckResults(results, true)
	if !strings.Contains(verbose, "        [note] visible note") {
		t.Fatalf("expected note line in verbose, got %q", verbose)
	}
}

func TestFormatCheckResults_NormalModeUnchanged(t *testing.T) {
	// Pin the exact normal-mode output; notes/duration/summary must not leak.
	results := []CheckRunResult{
		{Name: "alpha", Outcome: passWithNotes("alpha OK", "note")},
		{Name: "beta", Outcome: passOutcome("beta OK")},
	}
	out := FormatCheckResults(results, false)
	want := "PASS  alpha    alpha OK\nPASS  beta     beta OK"
	if out != want {
		t.Fatalf("normal output changed:\n got %q\nwant %q", out, want)
	}
}

func TestFormatCheckResults_VerboseDurationShape(t *testing.T) {
	results := []CheckRunResult{
		{Name: "a", Outcome: passOutcome("ok"), DurationMs: 12},
	}
	out := FormatCheckResults(results, true)
	if !strings.Contains(out, "(12ms)") {
		t.Fatalf("expected stable duration shape (12ms), got %q", out)
	}
}

func TestFormatCheckResults_VerboseCountSummary(t *testing.T) {
	results := []CheckRunResult{
		{Name: "a", Outcome: passOutcome("ok")},
		{Name: "b", Outcome: failOutcome("bad", "x")},
		{Name: "c", Outcome: warnOutcome("hmm", "y")},
		{Name: "d", Outcome: skipOutcome("skip")},
	}
	out := FormatCheckResults(results, true)
	if !strings.HasSuffix(out, "1 passed / 1 failed / 1 warned / 1 skipped") {
		t.Fatalf("expected trailing count summary, got %q", out)
	}
	if !strings.Contains(out, "\n\n1 passed / 1 failed / 1 warned / 1 skipped") {
		t.Fatalf("expected blank line before summary, got %q", out)
	}
}

func TestFormatCheckResultsJSON_NotesAndDurationAlways(t *testing.T) {
	results := []CheckRunResult{
		{Name: "a", Outcome: passWithNotes("ok", "n1"), DurationMs: 7},
		{Name: "b", Outcome: passOutcome("ok2")},
	}
	out := FormatCheckResultsJSON(results)
	var parsed []struct {
		Notes      []string `json:"notes"`
		DurationMs int64    `json:"duration_ms"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(parsed))
	}
	if len(parsed[0].Notes) != 1 || parsed[0].Notes[0] != "n1" || parsed[0].DurationMs != 7 {
		t.Fatalf("entry 0: expected notes=[n1] duration=7, got %+v", parsed[0])
	}
	// Additive keys ALWAYS present, even with no notes: empty [] not null.
	if parsed[1].Notes == nil {
		t.Fatalf("entry 1: notes must serialize as [] not null")
	}
	if !strings.Contains(out, `"notes":[]`) {
		t.Fatalf("expected empty notes to serialize as [], got %q", out)
	}
}

func TestRunChecks_DurationFieldPresent(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("covered")
	})
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: emptyProjectRoot} })
	results, _, _, err := app.RunChecks(&testCheckContext{root: emptyProjectRoot}, RunChecksOptions{RunAll: true})
	if err != nil {
		t.Fatalf("RunChecks error: %v", err)
	}
	for _, r := range results {
		if r.DurationMs < 0 {
			t.Fatalf("expected non-negative duration, got %d for %q", r.DurationMs, r.Name)
		}
	}
}

func TestCheckCommand_Verbose_NotesDurationSummary(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, r *ErrorReporter) CheckOutcome {
		r.Note("a verbose-only note")
		return r.Passed("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("covered")
	})
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: emptyProjectRoot} })

	r := app.Test([]string{"--verbose", "check", "--all"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "[note] a verbose-only note") {
		t.Fatalf("expected note in verbose output, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "ms)") {
		t.Fatalf("expected duration in verbose output, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "2 passed / 0 failed / 0 warned / 0 skipped") {
		t.Fatalf("expected count summary, got %q", r.Stdout)
	}

	// Normal mode must reveal none of it.
	n := app.Test([]string{"check", "--all"})
	if strings.Contains(n.Stdout, "[note]") || strings.Contains(n.Stdout, "passed /") || strings.Contains(n.Stdout, "ms)") {
		t.Fatalf("normal mode leaked verbose-only content, got %q", n.Stdout)
	}
}

func TestCheckCommand_VerboseHelpText(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("covered")
	})
	r := app.Test([]string{"check", "--help"})
	// --verbose and --dry-run are framework-owned reserved names, so the check
	// command no longer declares flags of its own for them and its help must not
	// advertise them.
	if strings.Contains(r.Stdout, "--verbose") || strings.Contains(r.Stdout, "--dry-run") {
		t.Fatalf("check help must not declare the framework-owned flags, got %q", r.Stdout)
	}
	if strings.Contains(r.Stdout, "Show full details for passing checks") {
		t.Fatalf("stale/lying verbose help text still present, got %q", r.Stdout)
	}
}
