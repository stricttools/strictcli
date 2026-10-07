package strictcli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// errSpec builds a passing error-severity provider spec with the given name.
func errSpec(name string, tags ...string) CheckSpec {
	if len(tags) == 0 {
		tags = []string{"provider"}
	}
	return NewErrorCheckSpec(
		CheckSpecMeta{Name: name, Tags: tags, Severity: "error"},
		func(ctx CheckContext, r *ErrorReporter) CheckOutcome {
			return r.Passed(name + " ok")
		},
	)
}

func newProviderApp(t *testing.T) *App {
	t.Helper()
	app := NewApp("testapp", "1.0.0", "test app")
	app.SetCheckContext(func(*Context) (CheckContext, error) { return &testCheckContext{root: emptyProjectRoot}, nil })
	return app
}

// --- TOML-less app + provider = working check command ---

func TestProvider_TomlLessListExecution(t *testing.T) {
	app := newProviderApp(t)
	app.RegisterCheckProvider(func() []CheckSpec {
		return []CheckSpec{errSpec("prov-a"), errSpec("prov-b")}
	})
	if !app.checksEnabled {
		t.Fatal("registering a provider must enable the check system")
	}

	// --list
	r := app.Test([]string{"check", "--list"})
	if r.ExitCode != 0 {
		t.Fatalf("--list exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "prov-a") || !strings.Contains(r.Stdout, "prov-b") {
		t.Fatalf("--list missing provider checks: %q", r.Stdout)
	}

	// --dry-run
	r = app.Test([]string{"--dry-run", "check", "--all"})
	if !strings.Contains(r.Stdout, "prov-a") || !strings.Contains(r.Stdout, "prov-b") {
		t.Fatalf("--dry-run missing provider checks: %q", r.Stdout)
	}

	// execution
	r = app.Test([]string{"check", "--all"})
	if r.ExitCode != 0 {
		t.Fatalf("--all exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "prov-a ok") || !strings.Contains(r.Stdout, "prov-b ok") {
		t.Fatalf("execution missing provider results: %q", r.Stdout)
	}
}

func TestProvider_ProgrammaticRunChecks(t *testing.T) {
	app := newProviderApp(t)
	app.RegisterCheckProvider(func() []CheckSpec { return []CheckSpec{errSpec("prov-a")} })
	dropBuiltinCheckProviders(app)

	results, _, code, err := app.RunChecks(&testCheckContext{root: emptyProjectRoot}, RunChecksOptions{RunAll: true})
	if err != nil {
		t.Fatalf("RunChecks err: %v", err)
	}
	if code != 0 || len(results) != 1 || results[0].Name != "prov-a" {
		t.Fatalf("unexpected results: code=%d results=%+v", code, results)
	}
}

// --- TOML + provider coexist ---

func TestProvider_CoexistsWithToml(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterCheckProvider(func() []CheckSpec { return []CheckSpec{errSpec("prov-a")} })

	r := app.Test([]string{"check", "--list"})
	for _, want := range []string{"version-consistency", "changelog-coverage", "prov-a"} {
		if !strings.Contains(r.Stdout, want) {
			t.Fatalf("--list missing %q: %q", want, r.Stdout)
		}
	}
}

// --- collisions are hard errors ---

func TestProvider_CollisionWithToml(t *testing.T) {
	checksPath := writeChecksFile(t, twoChecksToml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	app.RegisterErrorCheck("version-consistency", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterErrorCheck("changelog-coverage", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.SetCheckContext(func(*Context) (CheckContext, error) { return &testCheckContext{root: emptyProjectRoot}, nil })
	app.RegisterCheckProvider(func() []CheckSpec { return []CheckSpec{errSpec("version-consistency")} })

	assertPanicContains(t, "duplicate check definition", func() {
		app.Test([]string{"check", "--list"})
	})
}

func TestProvider_CollisionBetweenProviders(t *testing.T) {
	app := newProviderApp(t)
	app.RegisterCheckProvider(func() []CheckSpec { return []CheckSpec{errSpec("dup")} })
	app.RegisterCheckProvider(func() []CheckSpec { return []CheckSpec{errSpec("dup")} })

	assertPanicContains(t, "duplicate check definition", func() {
		app.Test([]string{"check", "--list"})
	})
}

// --- provider raise = hard error in every mode ---

func TestProvider_PanicIsHardErrorInEveryMode(t *testing.T) {
	makeApp := func() *App {
		app := newProviderApp(t)
		app.RegisterCheckProvider(func() []CheckSpec { panic("provider boom") })
		return app
	}
	for _, mode := range [][]string{{"check", "--list"}, {"--dry-run", "check", "--all"}, {"check", "--all"}} {
		app := makeApp()
		assertPanicContains(t, "provider boom", func() { app.Test(mode) })
	}
	// programmatic
	app := makeApp()
	assertPanicContains(t, "provider boom", func() {
		app.RunChecks(&testCheckContext{root: emptyProjectRoot}, RunChecksOptions{RunAll: true})
	})
}

// --- honest-empty is a clean no-op ---

func TestProvider_HonestEmpty(t *testing.T) {
	app := newProviderApp(t)
	app.RegisterCheckProvider(func() []CheckSpec { return nil })
	dropBuiltinCheckProviders(app)

	r := app.Test([]string{"check", "--list"})
	if r.ExitCode != 0 {
		t.Fatalf("honest-empty list exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	results, _, code, err := app.RunChecks(&testCheckContext{root: emptyProjectRoot}, RunChecksOptions{RunAll: true})
	if err != nil || code != 0 || len(results) != 0 {
		t.Fatalf("honest-empty programmatic: err=%v code=%d results=%+v", err, code, results)
	}
}

// --- severity-form mismatch ---

func TestProvider_SeverityFormMismatch(t *testing.T) {
	app := newProviderApp(t)
	app.RegisterCheckProvider(func() []CheckSpec {
		// Declares "warn" but uses the error-form constructor.
		return []CheckSpec{NewErrorCheckSpec(
			CheckSpecMeta{Name: "mismatch", Tags: []string{"x"}, Severity: "warn"},
			func(ctx CheckContext, r *ErrorReporter) CheckOutcome { return r.Passed("ok") },
		)}
	})
	assertPanicContains(t, "declared severity", func() {
		app.Test([]string{"check", "--list"})
	})
}

// --- memoization: provider called once across repeated reads ---

func TestProvider_MemoizedOnce(t *testing.T) {
	app := newProviderApp(t)
	calls := 0
	app.RegisterCheckProvider(func() []CheckSpec {
		calls++
		return []CheckSpec{errSpec("prov-a")}
	})
	app.Test([]string{"check", "--list"})
	app.Test([]string{"--dry-run", "check", "--all"})
	app.Test([]string{"check", "--all"})
	app.RunChecks(&testCheckContext{root: emptyProjectRoot}, RunChecksOptions{RunAll: true})
	if calls != 1 {
		t.Fatalf("provider should be called once across reads, got %d", calls)
	}
}

// --- cwd-change re-materialization ---

func TestProvider_CwdChangeRematerializes(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	orig, _ := os.Getwd()
	defer os.Chdir(orig)

	app := newProviderApp(t)
	calls := 0
	app.RegisterCheckProvider(func() []CheckSpec {
		calls++
		cwd, _ := os.Getwd()
		name := "in-a"
		if strings.HasPrefix(cwd, dirB) {
			name = "in-b"
		}
		return []CheckSpec{errSpec(name)}
	})

	if err := os.Chdir(dirA); err != nil {
		t.Fatal(err)
	}
	r := app.Test([]string{"check", "--list"})
	if !strings.Contains(r.Stdout, "in-a") {
		t.Fatalf("expected in-a check in dirA: %q", r.Stdout)
	}
	// Repeated read in same cwd does not re-run.
	app.Test([]string{"check", "--list"})
	if calls != 1 {
		t.Fatalf("expected 1 call in dirA, got %d", calls)
	}

	if err := os.Chdir(dirB); err != nil {
		t.Fatal(err)
	}
	r = app.Test([]string{"check", "--list"})
	if calls != 2 {
		t.Fatalf("expected re-materialization after cwd change, calls=%d", calls)
	}
	if !strings.Contains(r.Stdout, "in-b") || strings.Contains(r.Stdout, "in-a") {
		t.Fatalf("expected only in-b check in dirB: %q", r.Stdout)
	}
}

// --- reset ---

func TestProvider_ResetCache(t *testing.T) {
	app := newProviderApp(t)
	calls := 0
	app.RegisterCheckProvider(func() []CheckSpec {
		calls++
		return []CheckSpec{errSpec("prov-a")}
	})
	app.Test([]string{"check", "--list"})
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	app.ResetCheckProviderCache()
	// After reset the provider def is gone until the next read re-materializes.
	if _, ok := app.checkDefs["prov-a"]; ok {
		t.Fatal("reset should drop provider-sourced defs")
	}
	app.Test([]string{"check", "--list"})
	if calls != 2 {
		t.Fatalf("expected re-materialization after reset, calls=%d", calls)
	}
}

// --- list JSON carries provider severity ---

func TestProvider_ListJSON(t *testing.T) {
	app := newProviderApp(t)
	app.RegisterCheckProvider(func() []CheckSpec {
		return []CheckSpec{NewWarnCheckSpec(
			CheckSpecMeta{Name: "warn-prov", Tags: []string{"w"}, Severity: "warn"},
			func(ctx CheckContext, r *WarnReporter) CheckOutcome { return r.Passed("ok") },
		)}
	})
	dropBuiltinCheckProviders(app)
	r := app.Test([]string{"check", "--list", "--json"})
	var entries []struct {
		Name     string `json:"name"`
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal(envelopePayload(t, r.Stdout), &entries); err != nil {
		t.Fatalf("json parse: %v; out=%q", err, r.Stdout)
	}
	if len(entries) != 1 || entries[0].Name != "warn-prov" || entries[0].Severity != "warn" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

// assertPanicContains asserts fn panics with a message containing want.
func assertPanicContains(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		rec := recover()
		if rec == nil {
			t.Fatalf("expected panic containing %q, got none", want)
		}
		msg := ""
		switch v := rec.(type) {
		case error:
			msg = v.Error()
		case string:
			msg = v
		default:
			t.Fatalf("unexpected panic value type %T: %v", rec, rec)
		}
		if !strings.Contains(msg, want) {
			t.Fatalf("panic %q does not contain %q", msg, want)
		}
	}()
	fn()
}
