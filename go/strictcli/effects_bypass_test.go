package strictcli

// The built-in effects-bypass check provider (stdlib go/ast).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeGoFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// gitRun runs one git command inside a fixture repository.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
}

// gitTempDir is a temp directory that is a git work tree. The lint reads only
// repository-owned files, so a fixture root that is not a repository is
// refused rather than scanned.
func gitTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	return dir
}

// scanRepo scans a fixture repository, failing the test on the input-rule
// refusal so each caller keeps asserting findings alone.
func scanRepo(t *testing.T, dir string) []bypassFinding {
	t.Helper()
	findings, err := scanEffectsBypasses(dir)
	if err != nil {
		t.Fatalf("scanning %s: %v", dir, err)
	}
	return findings
}

// bypassOffender is one fixture file: a handler-shaped function carrying one
// direct effect call.
const bypassOffender = `package app

import "os"

func deploy(ctx *Ctx) int {
	ctx.Effects().Run(nil)
	os.RemoveAll("x")
	return 0
}
`

func TestBypassLintReadsOnlyRepositoryOwnedFiles(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "tracked.go", bypassOffender)
	gitRun(t, dir, "add", "tracked.go")
	writeGoFile(t, dir, "untracked.go", bypassOffender)
	writeGoFile(t, dir, ".gitignore", "ignored.go\n")
	writeGoFile(t, dir, "ignored.go", bypassOffender)

	findings := scanRepo(t, dir)

	if len(findings) != 2 {
		t.Fatalf("expected the tracked and the unignored file, got %#v", findings)
	}
	if findings[0].file != "tracked.go" || findings[1].file != "untracked.go" {
		t.Fatalf("unexpected files %#v", findings)
	}
}

func TestBypassLintRefusesARootOutsideAGitWorkTree(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "handler.go", bypassOffender)

	findings, err := scanEffectsBypasses(dir)

	if err == nil {
		t.Fatalf("expected a refusal, got findings %#v", findings)
	}
	if err.Error() != errEffectsBypassNotAWorkTree(dir) {
		t.Fatalf("unexpected refusal %q", err.Error())
	}
}

func TestBypassCheckReportsTheInputRuleRefusal(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "handler.go", bypassOffender)
	app := NewApp("testapp", "1.0.0", "test app")
	app.RegisterCheckProvider(func() []CheckSpec { return nil })
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: dir} })

	r := app.Test([]string{"check", "--name", "effects-bypass"})

	if r.ExitCode == 0 {
		t.Fatalf("expected a failing check, got exit 0; stdout=%q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, errEffectsBypassNotAWorkTree(dir)) {
		t.Fatalf("expected the input-rule refusal, got %q", r.Stdout)
	}
	if strings.Contains(r.Stdout, "route it through ctx.Effects()") {
		t.Fatalf("a refused root is never scanned, got %q", r.Stdout)
	}
}

func TestBypassLintFlagsDirectProcessCall(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "handler.go", `package app

import "os/exec"

func deploy(ctx *Ctx) int {
	ctx.Effects().Run([]interface{}{"make"})
	exec.Command("git", "push").Run()
	return 0
}
`)
	findings := scanRepo(t, dir)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %#v", findings)
	}
	if findings[0].target != "exec.Command" || findings[0].fn != "deploy" {
		t.Fatalf("unexpected finding %#v", findings[0])
	}
}

func TestBypassLintFlagsFilesystemAndNetwork(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "handler.go", `package app

import (
	"net/http"
	"os"
)

func publish(ctx *Ctx) int {
	ctx.Effects().Mkdir("d")
	os.WriteFile("x", nil, 0o644)
	os.RemoveAll("y")
	http.Post("https://x.test", "text/plain", nil)
	return 0
}
`)
	findings := scanRepo(t, dir)
	targets := make([]string, 0, len(findings))
	for _, f := range findings {
		targets = append(targets, f.target)
	}
	want := []string{"os.WriteFile", "os.RemoveAll", "http.Post"}
	if len(targets) != len(want) {
		t.Fatalf("targets = %v want %v", targets, want)
	}
	for i := range want {
		if targets[i] != want[i] {
			t.Fatalf("targets = %v want %v", targets, want)
		}
	}
}

func TestBypassLintIgnoresFunctionsThatNeverOptIn(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "plain.go", `package app

import "os"

func housekeeping() {
	os.RemoveAll("scratch")
}
`)
	if findings := scanRepo(t, dir); len(findings) != 0 {
		t.Fatalf("a function that never reaches for ctx.Effects() is not a finding: %#v", findings)
	}
}

func TestBypassLintDoesNotFlagTheEffectsHandleItself(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "clean.go", `package app

func deploy(ctx *Ctx) int {
	ctx.Effects().Remove("stale")
	ctx.Effects().Mkdir("build")
	ctx.Effects().Rename("a", "b")
	return 0
}
`)
	if findings := scanRepo(t, dir); len(findings) != 0 {
		t.Fatalf("routing through the handle must be clean: %#v", findings)
	}
}

func TestBypassLintDoesNotFlagOrdinaryMapLookups(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "lookup.go", `package app

type store struct{}

func (s store) Get(k string) string { return k }

func deploy(ctx *Ctx, s store) int {
	ctx.Effects().Run([]interface{}{"make"})
	_ = s.Get("key")
	return 0
}
`)
	if findings := scanRepo(t, dir); len(findings) != 0 {
		t.Fatalf("a plain .Get on a non-network receiver is not a finding: %#v", findings)
	}
}

func TestBypassLintSkipsUnparseableFilesAndSkipDirs(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "broken.go", "package app\nfunc (\n")
	writeGoFile(t, dir, filepath.Join("vendor", "dep.go"), `package dep

import "os"

func deploy(ctx *Ctx) int {
	ctx.Effects().Run(nil)
	os.RemoveAll("x")
	return 0
}
`)
	if findings := scanRepo(t, dir); len(findings) != 0 {
		t.Fatalf("unparseable files and vendor/ are not evidence of a bypass: %#v", findings)
	}
}

func TestBypassLintMissingRootIsRefused(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	_, err := scanEffectsBypasses(missing)
	if err == nil || err.Error() != errEffectsBypassNotAWorkTree(missing) {
		t.Fatalf("a missing root is refused by the input rule, got %v", err)
	}
}

func TestBypassCheckRunsThroughTheProviderHook(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "handler.go", `package app

import "os/exec"

func deploy(ctx *Ctx) int {
	ctx.Effects().Run(nil)
	exec.Command("git", "push").Run()
	return 0
}
`)
	app := NewApp("testapp", "1.0.0", "test app")
	app.RegisterCheckProvider(func() []CheckSpec { return nil })
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: dir} })

	r := app.Test([]string{"check", "--name", "effects-bypass"})
	if r.ExitCode == 0 {
		t.Fatalf("expected a failing check, got exit 0; stdout=%q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "route it through ctx.Effects()") {
		t.Fatalf("expected the bypass finding, got %q", r.Stdout)
	}
}

func TestBypassCheckMetadataMatchesTheContract(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")
	app.RegisterCheckProvider(func() []CheckSpec { return nil })
	specs := app.effectsBypassProvider()
	if len(specs) != 3 {
		t.Fatalf("expected exactly three specs, got %d", len(specs))
	}
	want := []struct {
		name     string
		severity string
	}{
		{"effects-bypass", "error"},
		{"observe-allowlist-breadth", "warn"},
		{"consequential-grant-agreement", "warn"},
	}
	for i, w := range want {
		m := specs[i].meta
		if m.Name != w.name || m.Severity != w.severity || !m.Fast || !m.Pure || m.NeedsNetwork {
			t.Fatalf("metadata = %#v", m)
		}
		if len(m.Tags) != 2 || m.Tags[0] != "effects" || m.Tags[1] != "quality" {
			t.Fatalf("tags = %v", m.Tags)
		}
		if len(m.DependsOn) != 0 {
			t.Fatalf("depends_on = %v", m.DependsOn)
		}
	}
}

// §6.2's hazard, surfaced as a WARNING and never as an error.
//
// proc_observe_allowlist=[["git"]] makes EVERY git invocation an observe: it
// really executes under --dry-run, is never logged, and is legal in a read_only
// command. That may be exactly what the app wants -- the allowlist is a
// declared, source-visible choice that authorizes real execution in dry mode --
// so the framework says so out loud instead of inventing a specificity rule.
func TestObserveAllowlistBreadthWarnsOnSingleTokenPrefixes(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app",
		WithProcObserveAllowlist([][]string{{"git"}}))
	app.RegisterCheckProvider(func() []CheckSpec { return nil })
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: t.TempDir()} })
	r := app.Test([]string{"check", "--name", "observe-allowlist-breadth"})
	if !strings.Contains(r.Stdout, "WARN") {
		t.Fatalf("expected a WARN verdict, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "EVERY 'git' invocation becomes an observe") {
		t.Fatalf("expected the breadth warning, got %q", r.Stdout)
	}
	// A warning, not an error: --ignore-warnings clears it.
	if r2 := app.Test([]string{"check", "--name", "observe-allowlist-breadth", "--ignore-warnings"}); r2.ExitCode != 0 {
		t.Fatalf("expected --ignore-warnings to clear the warning, got %d", r2.ExitCode)
	}
}

func TestObserveAllowlistBreadthPassesOnNarrowPrefixes(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app",
		WithProcObserveAllowlist([][]string{{"git", "status"}, {"gh", "release", "view"}}))
	app.RegisterCheckProvider(func() []CheckSpec { return nil })
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: t.TempDir()} })
	r := app.Test([]string{"check", "--name", "observe-allowlist-breadth"})
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "no single-token proc_observe_allowlist prefixes") {
		t.Fatalf("exit=%d stdout=%q", r.ExitCode, r.Stdout)
	}
}

// --- §11's scope: reachability from a registered command handler -----------

// Escape shape 1: opting in cannot be the trigger. A handler that mentions the
// handle nowhere is the easiest possible bypass, and a lint that only looked at
// effects-using functions would wave it straight through.
func TestBypassLintFlagsAHandlerThatNeverMentionsEffects(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "handler.go", `package app

import (
	"os"
	"os/exec"
)

func deploy(ctx *Context, args map[string]interface{}) Outcome {
	exec.Command("git", "push").Run()
	os.MkdirAll("build", 0o755)
	os.RemoveAll("stale")
	return Exit(0)
}
`)
	findings := scanRepo(t, dir)
	if len(findings) != 3 {
		t.Fatalf("expected 3 findings, got %#v", findings)
	}
	for _, f := range findings {
		if f.fn != "deploy" {
			t.Fatalf("unexpected finding %#v", f)
		}
	}
}

// Escape shape 2: reachability, not the immediate body.
func TestBypassLintFollowsAHelperCall(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "handler.go", `package app

import "os/exec"

func publish(path string) {
	exec.Command("rsync", path, "remote:/srv").Run()
}

func deploy(ctx *Context, args map[string]interface{}) Outcome {
	ctx.Effects().Run([]interface{}{"make", "build"})
	publish("build")
	return Exit(0)
}
`)
	findings := scanRepo(t, dir)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %#v", findings)
	}
	if findings[0].fn != "publish" || findings[0].target != "exec.Command" {
		t.Fatalf("unexpected finding %#v", findings[0])
	}
}

func TestBypassLintReachabilityIsTransitiveAndCrossFile(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "handler.go", `package app

func deploy(ctx *Context, args map[string]interface{}) Outcome {
	outer()
	return Exit(0)
}
`)
	writeGoFile(t, dir, "helpers.go", `package app

import "os"

func inner() { os.Remove("x") }

func outer() { inner() }
`)
	findings := scanRepo(t, dir)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %#v", findings)
	}
	if findings[0].fn != "inner" || findings[0].target != "os.Remove" {
		t.Fatalf("unexpected finding %#v", findings[0])
	}
}

// The scope is reachability, not "every function in the tree".
func TestBypassLintIgnoresAnUnreachableHelper(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "handler.go", `package app

import "os"

func neverCalled() { os.Remove("x") }

func deploy(ctx *Context, args map[string]interface{}) Outcome {
	ctx.Effects().Run([]interface{}{"make"})
	return Exit(0)
}
`)
	if findings := scanRepo(t, dir); len(findings) != 0 {
		t.Fatalf("expected no findings, got %#v", findings)
	}
}

// Package boundaries are respected: a same-named function in another directory
// is a different symbol and must not be pulled in.
func TestBypassLintDoesNotCrossPackageBoundaries(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "handler.go", `package app

func deploy(ctx *Context, args map[string]interface{}) Outcome {
	helper()
	return Exit(0)
}

func helper() {}
`)
	writeGoFile(t, dir, "other/other.go", `package other

import "os"

func helper() { os.Remove("x") }
`)
	if findings := scanRepo(t, dir); len(findings) != 0 {
		t.Fatalf("expected no findings, got %#v", findings)
	}
}

// `e := ctx.Effects()` then `e.Write(...)` is the same call as
// `ctx.Effects().Write(...)`; the handle itself must never read as a bypass.
func TestBypassLintAcceptsALocalHandleAlias(t *testing.T) {
	dir := gitTempDir(t)
	writeGoFile(t, dir, "handler.go", `package app

func deploy(ctx *Context, args map[string]interface{}) Outcome {
	e := ctx.Effects()
	e.Mkdir("build")
	e.Remove("stale")
	return Exit(0)
}
`)
	if findings := scanRepo(t, dir); len(findings) != 0 {
		t.Fatalf("expected no findings, got %#v", findings)
	}
}

// --- §8.1's declaration vs §6.1's grants -----------------------------------
//
// A grant exists so a reviewer reading a preview sees WHY a dangerous step is
// there -- the same judgement WithConsequential makes. The check fires only for
// the two kinds that leave this process (proc_mutate runs another program,
// net_mutate changes remote state); a file_write or a proc_spawn is local and
// ordinarily recoverable, and flagging those would re-create the noise the
// consequential declaration exists to remove.
//
// It is a WARNING, not an error, for the same reason: an error would push
// consumers to declare consequential reflexively to clear a gate, which is the
// exact reflex the redesign removed.

func grantAgreementApp(t *testing.T, kind string, opts ...CmdOption) *App {
	t.Helper()
	app := NewApp("testapp", "1.0.0", "test app")
	all := append([]CmdOption{
		WithEffect(EffectMutating),
		WithGrants(Grant{Name: "push", Reason: "the release engine owns remote refs", Kind: kind}),
	}, opts...)
	app.Command("release", "release", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, all...)
	app.RegisterCheckProvider(func() []CheckSpec { return nil })
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: t.TempDir()} })
	return app
}

func TestConsequentialGrantAgreementWarnsOnAProcMutateGrant(t *testing.T) {
	app := grantAgreementApp(t, ProcMutate)
	r := app.Test([]string{"check", "--name", "consequential-grant-agreement"})
	if !strings.Contains(r.Stdout, "WARN") {
		t.Fatalf("expected a WARN verdict, got %q", r.Stdout)
	}
	want := "command 'release' declares grant 'push' (kind proc_mutate) but is not consequential"
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("expected %q, got %q", want, r.Stdout)
	}
	if r2 := app.Test([]string{"check", "--name", "consequential-grant-agreement", "--ignore-warnings"}); r2.ExitCode != 0 {
		t.Fatalf("expected --ignore-warnings to clear the warning, got %d", r2.ExitCode)
	}
}

func TestConsequentialGrantAgreementWarnsOnANetMutateGrant(t *testing.T) {
	r := grantAgreementApp(t, NetMutate).Test([]string{"check", "--name", "consequential-grant-agreement"})
	if !strings.Contains(r.Stdout, "kind net_mutate") {
		t.Fatalf("expected the net_mutate warning, got %q", r.Stdout)
	}
}

func TestConsequentialGrantAgreementPassesWhenTheCommandIsConsequential(t *testing.T) {
	r := grantAgreementApp(t, ProcMutate, WithConsequential()).
		Test([]string{"check", "--name", "consequential-grant-agreement"})
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "every escaping grant sits on a consequential command") {
		t.Fatalf("exit=%d stdout=%q", r.ExitCode, r.Stdout)
	}
}

func TestConsequentialGrantAgreementIgnoresLocalKinds(t *testing.T) {
	for _, kind := range []string{FileWrite, ProcSpawn} {
		r := grantAgreementApp(t, kind).Test([]string{"check", "--name", "consequential-grant-agreement"})
		if r.ExitCode != 0 {
			t.Fatalf("%s must not be flagged: exit=%d stdout=%q", kind, r.ExitCode, r.Stdout)
		}
	}
}

func TestConsequentialGrantAgreementNamesTheDottedPath(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")
	grp := app.Group("release", "release")
	grp.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectMutating),
		WithGrants(Grant{Name: "push", Reason: "owns remote refs", Kind: ProcMutate}))
	app.RegisterCheckProvider(func() []CheckSpec { return nil })
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: t.TempDir()} })
	r := app.Test([]string{"check", "--name", "consequential-grant-agreement"})
	if !strings.Contains(r.Stdout, "command 'release.run' declares grant 'push'") {
		t.Fatalf("expected the dotted path, got %q", r.Stdout)
	}
}

// --- §8.1's declaration guard ----------------------------------------------

func TestReadOnlyCannotBeConsequential(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	want := `command "look": a read_only command cannot be consequential (a command that changes nothing has nothing to confirm)`
	got := mustPanic(t, func() {
		app.Command("look", "h", func(ctx *Context, args map[string]interface{}) Outcome {
			return Exit(0)
		}, WithEffect(EffectReadOnly), WithConsequential())
	})
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestConsequentialIsNotMandatory(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	app.Command("build", "h", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectMutating))
	if app.commands["build"].Consequential {
		t.Fatal("absence must mean not consequential")
	}
}

func TestConsequentialIsEmittedInTheSchema(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	app.Command("plain", "h", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectMutating))
	app.Command("grave", "h", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectMutating), WithConsequential())
	if _, present := toPlain(serializeCommand(app.commands["plain"])).(map[string]interface{})["consequential"]; present {
		t.Fatal("consequential must be omitted when false")
	}
	if got := toPlain(serializeCommand(app.commands["grave"])).(map[string]interface{})["consequential"]; got != true {
		t.Fatalf("consequential = %v, want true", got)
	}
}
