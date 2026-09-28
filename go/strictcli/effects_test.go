package strictcli

// The effects regime: classification, the reserved quartet, the handle, the
// carriers, grants, observes, the confirm protocol and the runtime seal.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- helpers ---------------------------------------------------------------

// mustPanic runs fn and returns the panic value as a string.
func mustPanic(t *testing.T, fn func()) string {
	t.Helper()
	var got string
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected a panic, got none")
			}
			switch v := r.(type) {
			case string:
				got = v
			case error:
				got = v.Error()
			default:
				t.Fatalf("unexpected panic value %#v", r)
			}
		}()
		fn()
	}()
	return got
}

// effectsApp builds a one-command app whose handler is fn.
func effectsApp(effect string, fn func(ctx *Context) Outcome, opts ...CmdOption) *App {
	app := NewApp("app", "1.0.0", "effects fixture")
	all := append([]CmdOption{WithEffect(effect)}, opts...)
	app.Command("go", "run the fixture handler",
		func(ctx *Context, kwargs map[string]interface{}) Outcome { return fn(ctx) }, all...)
	return app
}

// --- reserved-name bans (§12.1) --------------------------------------------

func TestReservedQuartetBannedOnCommandFlags(t *testing.T) {
	for _, name := range []string{"dry-run", "approve-consequential", "quiet", "verbose"} {
		got := mustPanic(t, func() { BoolFlag(name, "help text", Default(false)) })
		want := "flag name '" + name + "' is reserved by the framework (dry-run, approve-consequential, quiet, verbose)"
		if got != want {
			t.Fatalf("%s: got %q want %q", name, got, want)
		}
	}
}

func TestReservedQuartetBannedOnEveryFlagConstructor(t *testing.T) {
	cases := []func(){
		func() { StringFlag("verbose", "h", Required()) },
		func() { IntFlag("quiet", "h", Required()) },
		func() { FloatFlag("approve-consequential", "h", Required()) },
		func() { ListFlag(TypeStr, "dry-run", "h", Unique(true), Repeatable(), Default([]interface{}{})) },
		func() { DictFlag(TypeStr, "verbose", "h", Default(map[string]interface{}{})) },
	}
	for i, fn := range cases {
		got := mustPanic(t, fn)
		if !strings.Contains(got, "is reserved by the framework") {
			t.Fatalf("case %d: got %q", i, got)
		}
	}
}

func TestReservedQuartetBannedOnGlobalFlags(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	// The flag constructors ban the quartet first, so reach the global-flag
	// path with a hand-built Flag.
	got := mustPanic(t, func() { app.GlobalFlag(Flag{Name: "verbose", Type: TypeBool, Help: "h"}) })
	if got != "flag name 'verbose' is reserved by the framework (dry-run, approve-consequential, quiet, verbose)" {
		t.Fatalf("got %q", got)
	}
}

// `yes` owns no framework flag any more, but it stays banned so nobody
// reintroduces a private --yes meaning the same thing (§12.1).
func TestYesIsBannedOutright(t *testing.T) {
	want := "flag name 'yes' is banned by the framework: the confirmation skip is --approve-consequential"
	if got := mustPanic(t, func() { BoolFlag("yes", "h", Default(false)) }); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	app := NewApp("app", "1.0.0", "h")
	if got := mustPanic(t, func() { app.GlobalFlag(Flag{Name: "yes", Type: TypeBool, Help: "h"}) }); got != want {
		t.Fatalf("global flag: got %q want %q", got, want)
	}
}

func TestReservedQuartetLeavesShortNamesAndArgsAlone(t *testing.T) {
	// Short names are unaffected; positional arg names have no -- spelling.
	f := StringFlag("target", "h", Short("y"), Required())
	if f.Short != "y" {
		t.Fatalf("short flag 'y' must stay available")
	}
	a := NewArg("verbose", "h", ArgRequired())
	if a.Name != "verbose" {
		t.Fatalf("arg names are unaffected by the ban")
	}
}

// The programmatic consent PARAMETER name is reserved on both surfaces. Go's
// map kwargs make it structurally harmless here, but the name is framework
// vocabulary across every implementation, so registration refuses it
// identically in all three.
func TestConsentParamNameBannedOnFlags(t *testing.T) {
	want := "flag name 'approve_consequential' is reserved by the framework: it names the programmatic consent parameter"
	cases := []func(){
		func() { BoolFlag("approve_consequential", "h", Default(false)) },
		func() { StringFlag("approve_consequential", "h", Required()) },
		func() { IntFlag("approve_consequential", "h", Required()) },
	}
	for i, fn := range cases {
		if got := mustPanic(t, fn); got != want {
			t.Fatalf("case %d: got %q want %q", i, got, want)
		}
	}
}

func TestConsentParamNameBannedOnArgs(t *testing.T) {
	want := "arg name 'approve_consequential' is reserved by the framework: it names the programmatic consent parameter"
	if got := mustPanic(t, func() { NewArg("approve_consequential", "h", ArgRequired()) }); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestConsentParamBanLeavesOtherArgNamesAlone(t *testing.T) {
	for _, name := range []string{"verbose", "approve", "approve-consequential"} {
		if a := NewArg(name, "h", ArgRequired()); a.Name != name {
			t.Fatalf("arg %q must stay available", name)
		}
	}
}

func TestOutputIsNotReserved(t *testing.T) {
	f := StringFlag("output", "h", Required())
	if f.Name != "output" {
		t.Fatal("--output must stay available to apps")
	}
}

// --- classification (§12.2) -------------------------------------------------

func TestClassificationIsMandatory(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	got := mustPanic(t, func() {
		app.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) })
	})
	want := `command "go": effect classification is required (effect="read_only" or effect="mutating")`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestClassificationIsMandatoryOnGroupCommands(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	grp := app.Group("grp", "h")
	got := mustPanic(t, func() {
		grp.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) })
	})
	if !strings.Contains(got, "effect classification is required") {
		t.Fatalf("got %q", got)
	}
}

func TestClassificationIsMandatoryOnPassthrough(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	got := mustPanic(t, func() {
		app.Passthrough("pt", "h", func(ctx *Context, name string, args []string, globals map[string]interface{}) int { return 0 })
	})
	if !strings.Contains(got, "effect classification is required") {
		t.Fatalf("got %q", got)
	}
}

func TestClassificationRejectsAnInvalidValue(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	got := mustPanic(t, func() {
		app.Command("go", "h",
			func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) },
			WithEffect("destructive"))
	})
	want := `command "go": invalid effect "destructive": must be "read_only" or "mutating"`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDeprecatedCommandsAreClassificationExempt(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	app.Deprecated("old", "use new instead") // no classification: fine
	got := mustPanic(t, func() { app.Deprecated("older", "gone", WithEffect(EffectMutating)) })
	want := `deprecated command "older": effect classification does not apply (a deprecated command has no handler)`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	grp := app.Group("grp", "h")
	got = mustPanic(t, func() { grp.Deprecated("sub", "gone", WithEffect(EffectReadOnly)) })
	if got != `deprecated command "sub": effect classification does not apply (a deprecated command has no handler)` {
		t.Fatalf("got %q", got)
	}
}

// --- grants (§12.7, §12.4) --------------------------------------------------

func TestGrantDeclarationValidation(t *testing.T) {
	newApp := func(grants ...Grant) func() {
		return func() {
			app := NewApp("app", "1.0.0", "h")
			app.Command("go", "h",
				func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) },
				WithEffect(EffectMutating), WithGrants(grants...))
		}
	}
	cases := []struct {
		grants []Grant
		want   string
	}{
		{[]Grant{{Name: "Push", Reason: "r", Kind: ProcMutate}},
			`command "go": invalid grant name 'Push': must match [a-z][a-z0-9-]*`},
		{[]Grant{{Name: "push", Reason: "r", Kind: ProcMutate}, {Name: "push", Reason: "r2", Kind: FileWrite}},
			`command "go": duplicate grant 'push'`},
		{[]Grant{{Name: "push", Reason: "  ", Kind: ProcMutate}},
			`command "go": grant 'push' reason must be a non-empty string`},
		{[]Grant{{Name: "push", Reason: "r", Kind: CacheWrite}},
			`command "go": grant 'push' has invalid kind 'cache_write': must be one of proc_mutate, proc_spawn, file_write, net_mutate`},
	}
	for _, c := range cases {
		if got := mustPanic(t, newApp(c.grants...)); got != c.want {
			t.Fatalf("got %q want %q", got, c.want)
		}
	}
}

func TestGrantUndeclaredIsCallTimeError(t *testing.T) {
	var err error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		_, err = ctx.Effects().Run([]interface{}{"git", "push"}, UseGrant("push"))
		return Exit(0)
	})
	app.Test([]string{"--dry-run", "go"})
	want := `command "go": grant 'push' is not declared on this command`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
}

func TestGrantKindMismatchIsCallTimeError(t *testing.T) {
	var err error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		_, err = ctx.Effects().Mkdir("d", UseGrant("push"))
		return Exit(0)
	}, WithGrants(Grant{Name: "push", Reason: "r", Kind: ProcMutate}))
	app.Test([]string{"--dry-run", "go"})
	want := `command "go": grant 'push' is declared for kind proc_mutate but was used for a file_write effect`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
}

func TestGrantOnAnObserveIsCallTimeError(t *testing.T) {
	var err error
	app := NewApp("app", "1.0.0", "h",
		WithProcObserveAllowlist([][]string{{"git", "status"}}))
	app.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		_, err = ctx.Effects().Run([]interface{}{"git", "status"}, UseGrant("push"))
		return Exit(0)
	}, WithEffect(EffectMutating), WithGrants(Grant{Name: "push", Reason: "r", Kind: ProcMutate}))
	app.Test([]string{"--dry-run", "go"})
	want := `command "go": grant 'push' cannot be used on an observe (an allowlisted effects.run changes nothing)`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
}

// --- read-only enforcement and the allowlist (§9.1, §6.2) -------------------

func TestReadOnlyRejectsEveryMutatingMethod(t *testing.T) {
	type probe struct {
		method string
		call   func(*Effects) error
	}
	probes := []probe{
		{"write", func(e *Effects) error { _, err := e.Write("p", "c"); return err }},
		{"mkdir", func(e *Effects) error { _, err := e.Mkdir("p"); return err }},
		{"remove", func(e *Effects) error { _, err := e.Remove("p"); return err }},
		{"rename", func(e *Effects) error { _, err := e.Rename("a", "b"); return err }},
		{"chmod", func(e *Effects) error { _, err := e.Chmod("p", 0o755); return err }},
		{"http", func(e *Effects) error { _, err := e.HTTP("POST", "https://x.test"); return err }},
		{"spawn", func(e *Effects) error { _, err := e.Spawn([]interface{}{"x"}); return err }},
	}
	for _, p := range probes {
		var err error
		app := effectsApp(EffectReadOnly, func(ctx *Context) Outcome {
			err = p.call(ctx.Effects())
			return Exit(0)
		})
		app.Test([]string{"--dry-run", "go"})
		want := `command "go" is classified read_only; effects.` + p.method + " is a mutating operation"
		if err == nil || err.Error() != want {
			t.Fatalf("%s: got %v want %q", p.method, err, want)
		}
	}
}

func TestReadOnlyRejectsANonAllowlistedRun(t *testing.T) {
	var err error
	app := NewApp("app", "1.0.0", "h",
		WithProcObserveAllowlist([][]string{{"git", "status"}}))
	app.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		_, err = ctx.Effects().Run([]interface{}{"git", "push", "origin"})
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	app.Test([]string{"go"})
	want := `command "go" is classified read_only; effects.run argv git push origin is not on the app's proc_observe_allowlist`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
}

func TestAllowlistMatchesElementWisePrefixesOnly(t *testing.T) {
	app := NewApp("app", "1.0.0", "h",
		WithProcObserveAllowlist([][]string{{"git", "rev-parse"}}))
	e := newEffects(&Command{Name: "go", Effect: EffectReadOnly}, "go", true, &effectLog{},
		app.procObserveAllowlist, traceIdentity{}, nil, false)
	ops := func(vals ...string) []operand {
		out := make([]operand, 0, len(vals))
		for _, v := range vals {
			out = append(out, operand{value: v, rendered: v})
		}
		return out
	}
	if !e.isObserve(ops("git", "rev-parse", "HEAD")) {
		t.Fatal("prefix match must succeed")
	}
	if e.isObserve(ops("git")) {
		t.Fatal("a shorter argv than the prefix must not match")
	}
	if e.isObserve(ops("git", "rev-parse-x")) {
		t.Fatal("matching is string equality per element, not prefix-of-element")
	}
	if e.isObserve([]operand{{unsettled: true}, {value: "rev-parse"}}) {
		t.Fatal("an unsettled element can never match")
	}
}

func TestEmptyAllowlistPrefixIsRegistrationError(t *testing.T) {
	got := mustPanic(t, func() {
		NewApp("app", "1.0.0", "h", WithProcObserveAllowlist([][]string{{}}))
	})
	if got != "proc_observe_allowlist entries must not be empty" {
		t.Fatalf("got %q", got)
	}
}

// --- the would-do log (§3.2) ------------------------------------------------

func TestWouldDoLogRendersEveryVerb(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		e := ctx.Effects()
		e.Run([]interface{}{"make", "all"})
		e.Spawn([]interface{}{"daemon", "--start"})
		e.Write("VERSION", "1.2.3\n")
		e.Mkdir("build")
		e.Remove("stale")
		e.Rename("a.txt", "b.txt")
		e.Chmod("script.sh", 0o755)
		e.HTTP("POST", "https://api.test/x")
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	want := "DRY RUN — no changes were made. Would do:\n" +
		"  1. run: make all\n" +
		"  2. spawn: daemon --start\n" +
		"  3. write: VERSION (6 bytes)\n" +
		"  4. mkdir: build\n" +
		"  5. remove: stale\n" +
		"  6. rename: a.txt -> b.txt\n" +
		"  7. chmod: script.sh 0755\n" +
		"  8. net: POST https://api.test/x\n"
	if r.Stdout != want {
		t.Fatalf("got:\n%q\nwant:\n%q", r.Stdout, want)
	}
}

func TestGrantSuffixPrecedesConditionalSuffix(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().Run([]interface{}{"git", "push"},
			UseGrant("push"), Resource("remote:origin"), SkipIfCurrent("remote:origin"))
		return Exit(0)
	}, WithGrants(Grant{Name: "push", Reason: "release engine owns remote refs", Kind: ProcMutate}))
	r := app.Test([]string{"--dry-run", "go"})
	want := "DRY RUN — no changes were made. Would do:\n" +
		"  1. run: git push (granted: push — release engine owns remote refs)" +
		" [unless resource 'remote:origin' already current]\n"
	if r.Stdout != want {
		t.Fatalf("got %q want %q", r.Stdout, want)
	}
}

func TestConditionalAnnotationIsInertInLiveMode(t *testing.T) {
	dir := t.TempDir()
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().Mkdir(filepath.Join(dir, "made"),
			SkipIfCurrent("dir:made"), Resource("dir:made"))
		return Exit(0)
	})
	r := app.Test([]string{"go"})
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	// Real mode executes unconditionally: there is no currency machinery.
	if _, err := os.Stat(filepath.Join(dir, "made")); err != nil {
		t.Fatalf("skip_if_current must not gate execution: %v", err)
	}
	log := app.EffectLog()
	if len(log) != 1 || log[0]["recorded"] != false || log[0]["skip_if_current"] != "dir:made" {
		t.Fatalf("unexpected live-mode record: %#v", log)
	}
}

func TestQuietNeverSuppressesTheWouldDoLog(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().Mkdir("d")
		ctx.Info("chatty")
		return Exit(0)
	})
	r := app.Test([]string{"--quiet", "--dry-run", "go"})
	if strings.Contains(r.Stdout, "chatty") {
		t.Fatalf("--quiet must hide ctx.Info, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "1. mkdir: d") {
		t.Fatalf("--quiet must never suppress the would-do log, got %q", r.Stdout)
	}
}

// --- write's carrier content and bytes: null (§3.2, §14.2) ------------------

func TestWriteWithUnsettledContentRendersTheBrandAndOmitsBytes(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		built, _ := ctx.Effects().Run([]interface{}{"make", "build"})
		ctx.Effects().Write("VERSION", built)
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	if !strings.Contains(r.Stdout, "2. write: VERSION («step 1 output»)") {
		t.Fatalf("got %q", r.Stdout)
	}
	rec := app.EffectLog()[1]
	if _, present := rec["bytes"]; present {
		t.Fatalf("an unsettled write must carry no byte count, got %#v", rec)
	}
}

func TestWriteWithSettledForwardedContentCountsRealBytes(t *testing.T) {
	app := NewApp("app", "1.0.0", "h", WithProcObserveAllowlist([][]string{echoPrefix()}))
	app.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		head, err := ctx.Effects().Run(echoArgv(), echoEnv("abc"))
		if err != nil {
			ctx.Error(err.Error())
			return Exit(1)
		}
		ctx.Effects().Write("VERSION", head)
		return Exit(0)
	}, WithEffect(EffectMutating))
	r := app.Test([]string{"--dry-run", "go"})
	if !strings.Contains(r.Stdout, "1. write: VERSION (3 bytes)") {
		t.Fatalf("a settled pre-mutation observe projects normally, got %q", r.Stdout)
	}
	if app.EffectLog()[0]["bytes"] != 3 {
		t.Fatalf("expected bytes=3, got %#v", app.EffectLog()[0])
	}
}

// --- void non-forwardability (§2.5.5) ---------------------------------------

func TestVoidResultsAreNeverForwardable(t *testing.T) {
	for _, mode := range []string{"dry", "live"} {
		var err error
		dir := t.TempDir()
		app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
			void, _ := ctx.Effects().Mkdir(filepath.Join(dir, "d"))
			_, err = ctx.Effects().Run([]interface{}{"echo", void})
			return Exit(0)
		})
		argv := []string{"go"}
		if mode == "dry" {
			argv = []string{"--dry-run", "go"}
		}
		app.Test(argv)
		want := `command "go": effects.run parameter 'argv[1]' does not accept an unsettled value`
		if err == nil || err.Error() != want {
			t.Fatalf("%s mode: got %v want %q", mode, err, want)
		}
	}
}

func TestSpawnedHasNoScalarProjection(t *testing.T) {
	var err error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		s, _ := ctx.Effects().Spawn([]interface{}{"daemon"})
		_, err = ctx.Effects().Write(s, "x")
		return Exit(0)
	})
	app.Test([]string{"--dry-run", "go"})
	want := `command "go": effects.write parameter 'path' does not accept an unsettled value`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
}

// --- inapplicable options (§12.8) -------------------------------------------

func TestInapplicableOptionIsACallTimeError(t *testing.T) {
	cases := []struct {
		name string
		call func(*Effects) error
		want string
	}{
		{"mkdir/stream", func(e *Effects) error { _, err := e.Mkdir("d", Stream(true)); return err },
			`command "go": effects.mkdir does not accept option 'stream'`},
		{"write/check", func(e *Effects) error { _, err := e.Write("p", "c", Check(false)); return err },
			`command "go": effects.write does not accept option 'check'`},
		{"run/body", func(e *Effects) error { _, err := e.Run([]interface{}{"x"}, Body([]byte("b"))); return err },
			`command "go": effects.run does not accept option 'body'`},
		{"http/cwd", func(e *Effects) error { _, err := e.HTTP("GET", "https://x.test", Cwd("/")); return err },
			`command "go": effects.http does not accept option 'cwd'`},
		{"spawn/check", func(e *Effects) error { _, err := e.Spawn([]interface{}{"x"}, Check(false)); return err },
			`command "go": effects.spawn does not accept option 'check'`},
		{"chmod/headers", func(e *Effects) error { _, err := e.Chmod("p", 0o755, Header("X", "1")); return err },
			`command "go": effects.chmod does not accept option 'headers'`},
		{"rename/env", func(e *Effects) error {
			_, err := e.Rename("a", "b", EffectEnv(map[string]string{"K": "V"}))
			return err
		},
			`command "go": effects.rename does not accept option 'env'`},
	}
	for _, c := range cases {
		var err error
		app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
			err = c.call(ctx.Effects())
			return Exit(0)
		})
		app.Test([]string{"--dry-run", "go"})
		if err == nil || err.Error() != c.want {
			t.Fatalf("%s: got %v want %q", c.name, err, c.want)
		}
	}
}

func TestWaitAcceptsOnlyCheck(t *testing.T) {
	s := Spawned{settled: true, cmdPath: "go"}
	_, err := s.Wait(Resource("r"))
	want := `command "go": effects.spawn does not accept option 'resource'`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
}

// --- the runtime seal at all five dispatch sites (§15) ----------------------

func TestSealTruncatesOnTheTestPath(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		c, _ := ctx.Effects().Run([]interface{}{"make", "build"})
		_ = c.ExitCode() // extraction
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if r.Stdout != "DRY RUN — no changes were made. Would do:\n  1. run: make build\n" {
		t.Fatalf("stdout=%q", r.Stdout)
	}
	want := "error: dry-run preview ends at step 2: go branched on unsettled value " +
		"«step 1 output» — cannot preview past this point\n"
	if r.Stderr != want {
		t.Fatalf("stderr=%q want %q", r.Stderr, want)
	}
}

func TestSealTruncatesOnThePassthroughTestPath(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	app.Passthrough("pt", "h", func(ctx *Context, name string, args []string, globals map[string]interface{}) int {
		c, _ := ctx.Effects().Run([]interface{}{"make", "build"})
		_ = c.Stdout()
		return 0
	}, WithEffect(EffectMutating))
	r := app.Test([]string{"--dry-run", "pt"})
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "dry-run preview ends at step 2: pt") {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestSealTruncatesOnTheInvokePath(t *testing.T) {
	// The programmatic path is never in dry mode, but the VOID carrier is never
	// settled in either mode, so extraction still truncates there.
	dir := t.TempDir()
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		void, _ := ctx.Effects().Mkdir(filepath.Join(dir, "d"))
		_ = void.Bool()
		return Exit(0)
	})
	_, err := app.Call("go", nil)
	if err == nil || !strings.Contains(err.Error(), "dry-run preview ends at step 2: go") {
		t.Fatalf("got %v", err)
	}
}

func TestSealTruncatesOnThePassthroughInvokePath(t *testing.T) {
	dir := t.TempDir()
	app := NewApp("app", "1.0.0", "h")
	app.Passthrough("pt", "h", func(ctx *Context, name string, args []string, globals map[string]interface{}) int {
		void, _ := ctx.Effects().Mkdir(filepath.Join(dir, "d"))
		_ = void.Int()
		return 0
	}, WithEffect(EffectMutating))
	_, err := app.Call("pt", map[string]interface{}{"_args": []string{}})
	if err == nil || !strings.Contains(err.Error(), "dry-run preview ends at step 2: pt") {
		t.Fatalf("got %v", err)
	}
}

func TestNonTruncationPanicsPropagate(t *testing.T) {
	app := effectsApp(EffectReadOnly, func(ctx *Context) Outcome { panic("boom") })
	got := mustPanic(t, func() { app.Test([]string{"go"}) })
	if got != "boom" {
		t.Fatalf("a non-truncation panic must be re-raised untouched, got %q", got)
	}
}

func TestEffectsHandleIsUnavailableOutsideDispatch(t *testing.T) {
	ctx := newContext(nil, nil, nil, nil, reservedFlags{}, nil)
	got := mustPanic(t, func() { ctx.Effects() })
	if got != errEffectsUnavailable {
		t.Fatalf("got %q", got)
	}
}

// --- carriers ---------------------------------------------------------------

func TestVoidCarrierExtractorsPanicInBothModes(t *testing.T) {
	u := Unsettled{brand: "«step 1 output»", cmdPath: "go"}
	for _, extract := range []func(){
		func() { _ = u.String() },
		func() { _ = u.Bytes() },
		func() { _ = u.Int() },
		func() { _ = u.Bool() },
	} {
		func() {
			defer func() {
				r := recover()
				if _, ok := r.(dryRunTruncation); !ok {
					t.Fatalf("expected a dryRunTruncation, got %#v", r)
				}
			}()
			extract()
		}()
	}
}

func TestSettledCarriersReturnRealValues(t *testing.T) {
	c := Completed{settled: true, exitCode: 3, stdout: "out", stderr: "err"}
	if c.ExitCode() != 3 || c.Stdout() != "out" || c.Stderr() != "err" {
		t.Fatal("a settled Completed must return its real values")
	}
	r := Response{settled: true, status: 201, body: []byte("b"),
		headers: map[string]string{"content-type": "application/json"}}
	if r.Status() != 201 || string(r.Body()) != "b" {
		t.Fatal("a settled Response must return its real values")
	}
	// Header names are lower-cased, so lookup does not depend on wire casing.
	if r.Header("Content-Type") != "application/json" {
		t.Fatalf("header lookup is case-insensitive, got %q", r.Header("Content-Type"))
	}
}

func TestPostMutationObserveYieldsAStaleBrand(t *testing.T) {
	app := NewApp("app", "1.0.0", "h", WithProcObserveAllowlist([][]string{{"git", "status"}}))
	app.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Effects().Run([]interface{}{"git", "tag", "v1"})
		stale, _ := ctx.Effects().Run([]interface{}{"git", "status"})
		ctx.Effects().Write("REPORT", stale)
		return Exit(0)
	}, WithEffect(EffectMutating))
	r := app.Test([]string{"--dry-run", "go"})
	if !strings.Contains(r.Stdout, "2. write: REPORT («stale: git status»)") {
		t.Fatalf("expected a stale brand, got %q", r.Stdout)
	}
}

func TestForwardingDoesNotConsumeACarrier(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		built, _ := ctx.Effects().Run([]interface{}{"make", "build"})
		ctx.Effects().Run([]interface{}{"ship", built})
		ctx.Effects().Run([]interface{}{"verify", built})
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	if !strings.Contains(r.Stdout, "2. run: ship «step 1 output»") ||
		!strings.Contains(r.Stdout, "3. run: verify «step 1 output»") {
		t.Fatalf("a carrier may be forwarded any number of times, got %q", r.Stdout)
	}
}

func TestCarrierAcceptingParameterRejectsAWrongType(t *testing.T) {
	var err error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		_, err = ctx.Effects().Mkdir(42)
		return Exit(0)
	})
	app.Test([]string{"--dry-run", "go"})
	if err == nil || !strings.Contains(err.Error(), "effects.mkdir parameter 'path' must be a string") {
		t.Fatalf("got %v", err)
	}
}

func TestEmptyArgvIsAnError(t *testing.T) {
	var err error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		_, err = ctx.Effects().Run(nil)
		return Exit(0)
	})
	app.Test([]string{"--dry-run", "go"})
	if err == nil || err.Error() != `command "go": effects.run argv must not be empty` {
		t.Fatalf("got %v", err)
	}
}

// --- live mode --------------------------------------------------------------

func TestLiveModePerformsAndRecordsWithRecordedFalse(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "VERSION")
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		e := ctx.Effects()
		e.Mkdir(filepath.Join(dir, "sub"))
		e.Write(target, "1.2.3\n")
		e.Chmod(target, 0o600)
		e.Rename(target, target+".bak")
		e.Remove(target + ".bak")
		return Exit(0)
	})
	r := app.Test([]string{"go"})
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if strings.Contains(r.Stdout, "DRY RUN") {
		t.Fatalf("live mode must not emit a would-do log, got %q", r.Stdout)
	}
	log := app.EffectLog()
	if len(log) != 5 {
		t.Fatalf("expected 5 records, got %#v", log)
	}
	for _, rec := range log {
		if rec["recorded"] != false {
			t.Fatalf("live entries carry recorded:false, got %#v", rec)
		}
	}
	if _, err := os.Stat(target + ".bak"); !os.IsNotExist(err) {
		t.Fatal("remove must have deleted the file")
	}
}

func TestRunFailureIsAnErrorAndCheckFalseOptsOut(t *testing.T) {
	app := NewApp("app", "1.0.0", "h", WithProcObserveAllowlist([][]string{echoPrefix()}))
	var failErr error
	var optedOut Completed
	app.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		_, failErr = ctx.Effects().Run(echoArgv(),
			EffectEnv(map[string]string{echoActiveEnv: "1", echoOutEnv: "", echoCodeEnv: "1"}))
		optedOut, _ = ctx.Effects().Run(echoArgv(),
			EffectEnv(map[string]string{echoActiveEnv: "1", echoOutEnv: "", echoCodeEnv: "1"}),
			Check(false))
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	app.Test([]string{"go"})
	if failErr == nil || !strings.Contains(failErr.Error(), "effects.run failed:") ||
		!strings.HasSuffix(failErr.Error(), "exited 1") {
		t.Fatalf("a nonzero exit must be an error, got %v", failErr)
	}
	if optedOut.ExitCode() != 1 {
		t.Fatalf("Check(false) must return the real exit code, got %d", optedOut.ExitCode())
	}
}

func TestRemoveToleratesAMissingPathAndMkdirAnExistingDir(t *testing.T) {
	dir := t.TempDir()
	var mkErr, rmErr error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		_, mkErr = ctx.Effects().Mkdir(dir) // already exists
		_, rmErr = ctx.Effects().Remove(filepath.Join(dir, "never-existed"))
		return Exit(0)
	})
	app.Test([]string{"go"})
	if mkErr != nil || rmErr != nil {
		t.Fatalf("mkdir=%v remove=%v", mkErr, rmErr)
	}
}

// --- the reserved quartet's delivery and position (§7.2) --------------------

func TestQuartetIsDeliveredOnTheContextNotAsKwargs(t *testing.T) {
	var seen map[string]interface{}
	var dry, approve, quiet, verbose bool
	app := NewApp("app", "1.0.0", "h")
	app.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		seen = kwargs
		dry, approve, quiet, verbose = ctx.DryRun(), ctx.ApproveConsequential(), ctx.Quiet(), ctx.Verbose()
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	app.Test([]string{"--dry-run", "--approve-consequential", "--quiet", "--verbose", "go"})
	for _, k := range []string{"dry_run", "approve_consequential", "quiet", "verbose"} {
		if _, present := seen[k]; present {
			t.Fatalf("the quartet must never reach handler kwargs, got %#v", seen)
		}
	}
	if !dry || !approve || !quiet || !verbose {
		t.Fatalf("quartet not delivered: %v %v %v %v", dry, approve, quiet, verbose)
	}
}

// The quartet is recognized ANYWHERE in argv, exactly like --help/-h
// (§7.2, amended 2026-08-04).

func TestQuartetIsRecognizedAfterTheCommandName(t *testing.T) {
	for _, c := range []struct {
		tok  string
		read func(*Context) bool
	}{
		{"--dry-run", (*Context).DryRun},
		{"--approve-consequential", (*Context).ApproveConsequential},
		{"--quiet", (*Context).Quiet},
		{"--verbose", (*Context).Verbose},
	} {
		var got bool
		app := effectsApp(EffectReadOnly, func(ctx *Context) Outcome {
			got = c.read(ctx)
			return Exit(0)
		})
		r := app.Test([]string{"go", c.tok})
		if r.ExitCode != 0 || !got {
			t.Fatalf("%s after the command name: exit=%d delivered=%v stderr=%q",
				c.tok, r.ExitCode, got, r.Stderr)
		}
	}
}

func TestQuartetIsRecognizedAfterANestedGroupSubcommand(t *testing.T) {
	var dry bool
	app := NewApp("app", "1.0.0", "h")
	inner := app.Group("outer", "outer group").Group("inner", "inner group")
	inner.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		dry = ctx.DryRun()
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"outer", "inner", "go", "--dry-run"})
	if r.ExitCode != 0 || !dry {
		t.Fatalf("exit=%d dry=%v stderr=%q", r.ExitCode, dry, r.Stderr)
	}
}

func TestQuartetIsRecognizedBetweenAGroupAndItsSubcommand(t *testing.T) {
	var dry bool
	app := NewApp("app", "1.0.0", "h")
	grp := app.Group("grp", "a group")
	grp.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		dry = ctx.DryRun()
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"grp", "--dry-run", "go"})
	if r.ExitCode != 0 || !dry {
		t.Fatalf("exit=%d dry=%v stderr=%q", r.ExitCode, dry, r.Stderr)
	}
}

func TestQuartetIsStrippedFromArgvAfterTheCommandName(t *testing.T) {
	var seen interface{}
	var quiet bool
	app := NewApp("app", "1.0.0", "h")
	app.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		seen = kwargs["name"]
		quiet = ctx.Quiet()
		return Exit(0)
	}, WithEffect(EffectReadOnly), WithArgs(NewArg("name", "a positional", ArgRequired())))
	r := app.Test([]string{"go", "--quiet", "value"})
	if r.ExitCode != 0 || seen != "value" || !quiet {
		t.Fatalf("exit=%d name=%v quiet=%v stderr=%q", r.ExitCode, seen, quiet, r.Stderr)
	}
}

func TestATokenAfterDoubleDashIsDataNotAReservedFlag(t *testing.T) {
	var seen interface{}
	var dry bool
	app := NewApp("app", "1.0.0", "h")
	app.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		seen = kwargs["rest"]
		dry = ctx.DryRun()
		return Exit(0)
	}, WithEffect(EffectReadOnly), WithArgs(NewArg("rest", "trailing args", Variadic(), ArgRequired())))
	r := app.Test([]string{"go", "--", "--dry-run"})
	if r.ExitCode != 0 || dry {
		t.Fatalf("a token after -- must stay data: exit=%d dry=%v stderr=%q",
			r.ExitCode, dry, r.Stderr)
	}
	rest, _ := seen.([]interface{})
	if len(rest) != 1 || rest[0] != "--dry-run" {
		t.Fatalf("rest=%#v", seen)
	}
}

func TestHermeticStaysPreCommandOnly(t *testing.T) {
	// Only the quartet moved: --hermetic/--config/--dump-schema/--mcp are still
	// recognized in the pre-command region only.
	app := effectsApp(EffectReadOnly, func(ctx *Context) Outcome { return Exit(0) })
	r := app.Test([]string{"go", "--hermetic"})
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "unknown flag '--hermetic'") {
		t.Fatalf("post-command --hermetic must be an unknown-flag error, got exit=%d stderr=%q",
			r.ExitCode, r.Stderr)
	}
}

func TestReadOnlyStillRejectsAMutatingEffectUnderAPostCommandDryRun(t *testing.T) {
	// Per-command applicability is unchanged, wherever --dry-run appeared.
	var err error
	app := effectsApp(EffectReadOnly, func(ctx *Context) Outcome {
		_, err = ctx.Effects().Mkdir("d")
		return Exit(0)
	})
	app.Test([]string{"go", "--dry-run"})
	want := `command "go" is classified read_only; effects.mkdir is a mutating operation`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want %q", err, want)
	}
}

func TestPassthroughArgsKeepTheQuartetOpaque(t *testing.T) {
	// The one boundary the quartet does not cross: a passthrough's args belong
	// to the child process and are forwarded byte-for-byte.
	for _, c := range []struct {
		name     string
		argv     []string
		wantArgs []string
		wantCtx  bool
	}{
		{"top level", []string{"exec", "--verbose", "child"},
			[]string{"--verbose", "child"}, false},
		{"pre-command escape hatch", []string{"--verbose", "exec", "--verbose", "child"},
			[]string{"--verbose", "child"}, true},
	} {
		var gotArgs []string
		var gotCtx bool
		app := NewApp("app", "1.0.0", "h")
		app.Passthrough("exec", "run something",
			func(ctx *Context, name string, args []string, globals map[string]interface{}) int {
				gotArgs = args
				gotCtx = ctx.Verbose()
				return 0
			}, WithEffect(EffectReadOnly))
		r := app.Test(c.argv)
		if r.ExitCode != 0 {
			t.Fatalf("%s: exit=%d stderr=%q", c.name, r.ExitCode, r.Stderr)
		}
		if strings.Join(gotArgs, ",") != strings.Join(c.wantArgs, ",") {
			t.Fatalf("%s: args=%#v want %#v", c.name, gotArgs, c.wantArgs)
		}
		if gotCtx != c.wantCtx {
			t.Fatalf("%s: ctx.Verbose()=%v want %v", c.name, gotCtx, c.wantCtx)
		}
	}
}

func TestPassthroughUnderAGroupKeepsTheQuartetOpaque(t *testing.T) {
	var gotArgs []string
	var gotVerbose bool
	app := NewApp("app", "1.0.0", "h")
	grp := app.Group("grp", "a group")
	grp.Command("exec", "run something", nil,
		WithPassthrough(func(ctx *Context, name string, args []string, globals map[string]interface{}) int {
			gotArgs = args
			gotVerbose = ctx.Verbose()
			return 0
		}), WithEffect(EffectReadOnly))
	r := app.Test([]string{"grp", "exec", "--verbose", "child"})
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if strings.Join(gotArgs, ",") != "--verbose,child" || gotVerbose {
		t.Fatalf("args=%#v verbose=%v", gotArgs, gotVerbose)
	}
}

func TestQuietDominatesVerbose(t *testing.T) {
	emit := func(ctx *Context) Outcome {
		ctx.Debug("dbg")
		ctx.Info("inf")
		ctx.Warn("wrn")
		ctx.Error("err")
		return Exit(0)
	}
	cases := []struct {
		argv      []string
		wantOut   []string
		absentOut []string
	}{
		{[]string{"go"}, []string{"inf"}, []string{"dbg"}},
		{[]string{"--verbose", "go"}, []string{"dbg", "inf"}, nil},
		{[]string{"--quiet", "go"}, nil, []string{"dbg", "inf"}},
		{[]string{"--quiet", "--verbose", "go"}, nil, []string{"dbg", "inf"}},
	}
	for _, c := range cases {
		r := effectsApp(EffectReadOnly, emit).Test(c.argv)
		for _, want := range c.wantOut {
			if !strings.Contains(r.Stdout, want) {
				t.Fatalf("%v: expected %q in stdout, got %q", c.argv, want, r.Stdout)
			}
		}
		for _, absent := range c.absentOut {
			if strings.Contains(r.Stdout, absent) {
				t.Fatalf("%v: %q must be hidden, got %q", c.argv, absent, r.Stdout)
			}
		}
		// warn and error are never suppressed.
		if !strings.Contains(r.Stderr, "wrn") || !strings.Contains(r.Stderr, "err") {
			t.Fatalf("%v: warn/error must never be suppressed, got %q", c.argv, r.Stderr)
		}
	}
}

// --- the confirm protocol (§8) ----------------------------------------------

func TestConfirmDecisionGrammar(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	grave := &Command{Name: "deploy", Effect: EffectMutating, Consequential: true}
	plain := &Command{Name: "build", Effect: EffectMutating}
	readOnly := &Command{Name: "status", Effect: EffectReadOnly}

	if got := app.confirmDecision(readOnly, "status", true, strings.NewReader(""), discardWriter()); got != confirmProceed {
		t.Fatal("read_only commands never prompt")
	}
	if got := app.confirmDecision(plain, "build", false, strings.NewReader(""), discardWriter()); got != confirmProceed {
		t.Fatal("a mutating command that is not consequential never prompts")
	}
	if got := app.confirmDecision(grave, "deploy", false, strings.NewReader(""), discardWriter()); got != confirmNonInteractive {
		t.Fatal("a non-TTY stdin must produce the non-interactive outcome")
	}
	proceed := []string{"y\n", "Y\n", "y"}
	for _, in := range proceed {
		if got := app.confirmDecision(grave, "deploy", true, strings.NewReader(in), discardWriter()); got != confirmProceed {
			t.Fatalf("%q must proceed", in)
		}
	}
	decline := []string{"\n", "n\n", "yes\n", "Yes\n", "", "no\n"}
	for _, in := range decline {
		if got := app.confirmDecision(grave, "deploy", true, strings.NewReader(in), discardWriter()); got != confirmDeclined {
			t.Fatalf("%q must decline", in)
		}
	}
}

func TestConfirmIsSkippedByApprovalAndByDryRun(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	grave := &Command{Name: "deploy", Effect: EffectMutating, Consequential: true}
	app.lastApproveConsequential = true
	if got := app.confirmDecision(grave, "deploy", false, strings.NewReader(""), discardWriter()); got != confirmProceed {
		t.Fatal("--approve-consequential must skip the prompt and the non-TTY error")
	}
	app.lastApproveConsequential, app.lastDryRun = false, true
	if got := app.confirmDecision(grave, "deploy", false, strings.NewReader(""), discardWriter()); got != confirmProceed {
		t.Fatal("--dry-run must skip the prompt")
	}
}

func TestConfirmPromptIsByteExact(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	var buf strings.Builder
	app.confirmDecision(&Command{Name: "run", Effect: EffectMutating, Consequential: true}, "release.run", true,
		strings.NewReader("y\n"), &buf)
	want := "about to run consequential command 'release.run'. Proceed? [y/N] "
	if buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}
}

func TestConsequentialPassthroughIsNotExemptFromConfirm(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	pt := &Command{Name: "pt", Effect: EffectMutating, Passthrough: true, Consequential: true}
	if got := app.confirmDecision(pt, "pt", false, strings.NewReader(""), discardWriter()); got != confirmNonInteractive {
		t.Fatal("a consequential passthrough prompts like any other consequential command")
	}
	plain := &Command{Name: "pt", Effect: EffectMutating, Passthrough: true}
	if got := app.confirmDecision(plain, "pt", false, strings.NewReader(""), discardWriter()); got != confirmProceed {
		t.Fatal("a mutating passthrough that is not consequential never prompts")
	}
}

func TestProgrammaticDispatchNeverPrompts(t *testing.T) {
	// Test() behaves as if --approve-consequential were passed, and Call()
	// never prompts either -- it takes its consent as an option, so a
	// consented call runs straight through with no prompt and no non-TTY
	// error.
	ran := false
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		ran = true
		return Exit(0)
	}, WithConsequential())
	if r := app.Test([]string{"go"}); r.ExitCode != 0 || !ran {
		t.Fatalf("Test() must not prompt: exit=%d ran=%v stderr=%q", r.ExitCode, ran, r.Stderr)
	}
	ran = false
	if _, err := app.Call("go", nil, WithApproveConsequential()); err != nil || !ran {
		t.Fatalf("Call() must not prompt: err=%v ran=%v", err, ran)
	}
}

// TestCallRefusesAnUnconsentedConsequentialCommand pins the programmatic
// channel's half of the requirement: there is no terminal to prompt, so the
// caller has to state consent in the call or be refused.
func TestCallRefusesAnUnconsentedConsequentialCommand(t *testing.T) {
	ran := false
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		ran = true
		return Exit(0)
	}, WithConsequential())
	_, err := app.Call("go", nil)
	if err == nil {
		t.Fatal("an unconsented consequential Call must be refused")
	}
	want := "command 'go' is consequential: the call must carry confirmation"
	if err.Error() != want {
		t.Fatalf("got %q want %q", err.Error(), want)
	}
	if ran {
		t.Fatal("the handler must not run")
	}
}

// TestCallNeedsNoConsentForUnaffectedCommands: classification alone never
// demands consent -- only the consequential declaration does.
func TestCallNeedsNoConsentForUnaffectedCommands(t *testing.T) {
	for _, effect := range []string{EffectReadOnly, EffectMutating} {
		ran := false
		app := effectsApp(effect, func(ctx *Context) Outcome {
			ran = true
			return Exit(0)
		})
		if _, err := app.Call("go", nil); err != nil || !ran {
			t.Fatalf("%s: err=%v ran=%v", effect, err, ran)
		}
	}
}

// TestCallConsentReachesTheHandler: the caller's declaration is visible on the
// Context, so a handler (or an audit record) can see it.
func TestCallConsentReachesTheHandler(t *testing.T) {
	var approved bool
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		approved = ctx.ApproveConsequential()
		return Exit(0)
	}, WithConsequential())
	if _, err := app.Call("go", nil, WithApproveConsequential()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved {
		t.Fatal("ctx.ApproveConsequential() must report the call's consent")
	}
}

func TestCtxApproveConsequentialReflectsTheActualFlag(t *testing.T) {
	// Prompt suppression is a property of the dispatch path, not of
	// ctx.ApproveConsequential(): Test() never prompts, but the accessor still
	// reports whether --approve-consequential was passed.
	var approved bool
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		approved = ctx.ApproveConsequential()
		return Exit(0)
	}, WithConsequential())
	app.Test([]string{"go"})
	if approved {
		t.Fatal("ctx.ApproveConsequential() must be false when the flag was not passed")
	}
	app.Test([]string{"--approve-consequential", "go"})
	if !approved {
		t.Fatal("ctx.ApproveConsequential() must be true when the flag was passed")
	}
}

func TestStdinIsInteractiveOnAPipeIsFalse(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	if stdinIsInteractive() {
		t.Fatal("a pipe is not a character device")
	}
}

// The null device IS a character device, so the mode check alone read it as
// interactive -- and every subprocess launched with a null stdin with it, which
// is what CI runners and test harnesses do. Python's isatty() and Node's isTTY
// both report false there, so the same invocation prompted on Go and
// hard-errored on the other two.
func TestStdinIsInteractiveOnTheNullDeviceIsFalse(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip("no null device")
	}
	defer f.Close()
	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old }()
	if stdinIsInteractive() {
		t.Fatal("the null device must never read as interactive")
	}
}

func TestStdinIsInteractiveOnARegularFileIsFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(path, []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old }()
	if stdinIsInteractive() {
		t.Fatal("a regular file is not a character device")
	}
}

// --- framework-internal marker (§10.4) --------------------------------------

func TestFrameworkInternalMarkerRejectsAForeignHandler(t *testing.T) {
	// The marker itself is unreachable from consumer code (withFrameworkInternal
	// is unexported), so the hardening is exercised where it lives: a handler
	// whose function pointer does not resolve into this package fails the check,
	// and one that does passes it. A closure written in this test file is by
	// construction in-package, which is exactly why it cannot stand in for the
	// foreign case.
	if handlerIsFrameworkDefined(strings.ToUpper) {
		t.Fatal("a handler from another package must not pass the module check")
	}
	if handlerIsFrameworkDefined(nil) {
		t.Fatal("a nil handler must not pass the module check")
	}
	if !handlerIsFrameworkDefined(newEffects) {
		t.Fatal("a handler defined in this package must pass the module check")
	}
	// And the message the failure raises is the pinned template.
	want := `command "sneaky": handler is marked framework-internal but is not defined in the strictcli module`
	if got := errFrameworkInternalHandlerForeign("sneaky"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFrameworkInternalCommandsDeclareForwarding(t *testing.T) {
	app := NewApp("app", "1.0.0", "h", WithConfig())
	cfg := app.groups["config"]
	want := map[string]string{
		"path": EffectReadOnly, "show": EffectReadOnly,
		"set": EffectMutating, "init": EffectMutating, "edit": EffectMutating,
	}
	for name, effect := range want {
		cmd, ok := cfg.Commands[name]
		if !ok {
			t.Fatalf("config %s missing", name)
		}
		if cmd.Effect != effect {
			t.Fatalf("config %s effect = %q want %q", name, cmd.Effect, effect)
		}
		if cmd.Forwarding == nil || cmd.Forwarding.Reason != frameworkInternalForwardingReason {
			t.Fatalf("config %s forwarding = %#v", name, cmd.Forwarding)
		}
		if !cmd.frameworkInternal {
			t.Fatalf("config %s is not marked framework-internal", name)
		}
	}
}

func TestForwardingReasonMustBeNonEmpty(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	got := mustPanic(t, func() {
		app.Command("go", "h",
			func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) },
			WithEffect(EffectReadOnly), WithForwarding("  "))
	})
	if got != `command "go": forwarding reason must be a non-empty string` {
		t.Fatalf("got %q", got)
	}
}

func TestForwardingIsEmittedInTheSchema(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	app.Command("go", "h",
		func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) },
		WithEffect(EffectReadOnly), WithForwarding("wraps a generated CLI"))
	schema := dumpSchemaCore(app)
	cmd := schema["commands"].(map[string]interface{})["go"].(map[string]interface{})
	fw, ok := cmd["forwarding"].(map[string]interface{})
	if !ok || fw["reason"] != "wraps a generated CLI" {
		t.Fatalf("forwarding = %#v", cmd["forwarding"])
	}
	if _, present := cmd["framework_internal"]; present {
		t.Fatal("the private marker must never be emitted in the schema")
	}
}

// --- framework-blessed cache writes (§9.2) ----------------------------------

func TestSchemaDumpRecordsACacheWrite(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	if err := os.WriteFile("go.mod", []byte("module example.com/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := effectsApp(EffectReadOnly, func(ctx *Context) Outcome { return Exit(0) })
	if r := app.Test([]string{"--dump-schema"}); r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	log := app.EffectLog()
	if len(log) != 1 || log[0]["kind"] != CacheWrite || log[0]["verb"] != "cache" ||
		log[0]["recorded"] != false {
		t.Fatalf("expected one recorded:false cache write, got %#v", log)
	}
}

func TestCacheWritesAreNeverInTheWouldDoLog(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().Mkdir("d")
		return Exit(0)
	})
	app.beginDispatch()
	app.recordCacheWrite("/tmp/.strictcli/schema.json")
	rendered := app.renderWouldDoLog()
	if strings.Contains(rendered, "cache") {
		t.Fatalf("cache writes must never appear in the would-do log, got %q", rendered)
	}
}

// io_Discard is a tiny local helper so the confirm tests do not import io.
func discardWriter() *strings.Builder { return &strings.Builder{} }

// --- schema fields (§13) ----------------------------------------------------

func TestSchemaAlwaysEmitsEffect(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	app.Command("ro", "h",
		func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) },
		WithEffect(EffectReadOnly))
	app.Command("mu", "h",
		func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) },
		WithEffect(EffectMutating))
	schema := dumpSchemaCore(app)
	cmds := schema["commands"].(map[string]interface{})
	if cmds["ro"].(map[string]interface{})["effect"] != EffectReadOnly {
		t.Fatalf("ro: %#v", cmds["ro"])
	}
	if cmds["mu"].(map[string]interface{})["effect"] != EffectMutating {
		t.Fatalf("mu: %#v", cmds["mu"])
	}
}

func TestSchemaOmitsGrantsAndForwardingWhenEmpty(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	app.Command("go", "h",
		func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) },
		WithEffect(EffectReadOnly))
	cmd := dumpSchemaCore(app)["commands"].(map[string]interface{})["go"].(map[string]interface{})
	if _, present := cmd["grants"]; present {
		t.Fatal("grants must be omitted when empty")
	}
	if _, present := cmd["forwarding"]; present {
		t.Fatal("forwarding must be omitted when absent")
	}
}

func TestSchemaEmitsProcObserveAllowlistInDeclarationOrder(t *testing.T) {
	app := NewApp("app", "1.0.0", "h", WithProcObserveAllowlist([][]string{
		{"git", "status"}, {"gh", "release", "view"},
	}))
	schema := dumpSchemaCore(app)
	got := schema["proc_observe_allowlist"].([]interface{})
	if len(got) != 2 {
		t.Fatalf("allowlist = %#v", got)
	}
	first := got[0].([]interface{})
	second := got[1].([]interface{})
	if first[0] != "git" || first[1] != "status" || len(second) != 3 || second[2] != "view" {
		t.Fatalf("allowlist = %#v", got)
	}
}

func TestSchemaOmitsProcObserveAllowlistWhenEmpty(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	if _, present := dumpSchemaCore(app)["proc_observe_allowlist"]; present {
		t.Fatal("the allowlist must be omitted when empty")
	}
}

func TestDeprecatedCommandsCarryNoEffectInTheSchema(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	app.Deprecated("old", "gone")
	schema := dumpSchemaCore(app)
	dep := schema["deprecated"].(map[string]interface{})
	if dep["old"] != "gone" {
		t.Fatalf("deprecated = %#v", dep)
	}
	if cmds, present := schema["commands"]; present {
		if _, isCmd := cmds.(map[string]interface{})["old"]; isCmd {
			t.Fatal("a deprecated entry must not be a command entry")
		}
	}
}

// --- check-command subsumption (§7.5) ---------------------------------------

func TestCheckDryRunEmitsTheFrameworkWouldDoHeader(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")
	app.RegisterCheckProvider(func() []CheckSpec {
		return []CheckSpec{NewErrorCheckSpec(
			CheckSpecMeta{Name: "prov-a", Tags: []string{"t"}, Severity: "error"},
			func(ctx CheckContext, r *ErrorReporter) CheckOutcome { return r.Passed("ok") },
		)}
	})
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: emptyProjectRoot} })
	r := app.Test([]string{"--dry-run", "check", "--all"})
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "Would run") {
		t.Fatalf("the check handler's own listing must still print, got %q", r.Stdout)
	}
	// check is read_only, so the would-do body is always empty -- but the header
	// is still emitted, because --dry-run is now the framework flag.
	if !strings.HasSuffix(r.Stdout, "DRY RUN — no changes were made. Would do:\n") {
		t.Fatalf("expected a trailing header-only would-do log, got %q", r.Stdout)
	}
}

func TestCheckCommandDropsTheReservedFlagsAndFiltersGlobalCollisions(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")
	app.GlobalFlag(BoolFlag("all", "app-level all", Default(false)))
	app.RegisterCheckProvider(func() []CheckSpec { return nil })
	cmd := app.commands["check"]
	names := make(map[string]bool, len(cmd.flags))
	for _, f := range cmd.flags {
		names[f.Name] = true
	}
	// All three reserved names are absent from the candidate list entirely
	// (§7.5 and its 2026-08-13 sweep box).
	for _, banned := range []string{"verbose", "dry-run", "json"} {
		if names[banned] {
			t.Fatalf("check must not declare a %q flag", banned)
		}
	}
	if names["all"] {
		t.Fatal("a candidate colliding with a global flag must be filtered out")
	}
	for _, kept := range []string{"tag", "name", "hook", "list"} {
		if !names[kept] {
			t.Fatalf("check lost its %q flag", kept)
		}
	}
}

// --- execution details (§2.5.2, §2.5.4) -------------------------------------

func TestEnvMergesOverTheInheritedEnvironment(t *testing.T) {
	os.Setenv("STRICTCLI_MERGE_KEEP", "kept")
	defer os.Unsetenv("STRICTCLI_MERGE_KEEP")
	merged := mergedEnv(map[string]string{"STRICTCLI_MERGE_ADD": "added"})
	var sawKeep, sawAdd bool
	for _, entry := range merged {
		if entry == "STRICTCLI_MERGE_KEEP=kept" {
			sawKeep = true
		}
		if entry == "STRICTCLI_MERGE_ADD=added" {
			sawAdd = true
		}
	}
	if !sawKeep || !sawAdd {
		t.Fatalf("env must merge over, not replace: keep=%v add=%v", sawKeep, sawAdd)
	}
	if mergedEnv(nil) != nil {
		t.Fatal("no env option means inherit unchanged")
	}
}

func TestEnvOverridesAnInheritedValue(t *testing.T) {
	os.Setenv("STRICTCLI_MERGE_OVERRIDE", "old")
	defer os.Unsetenv("STRICTCLI_MERGE_OVERRIDE")
	merged := mergedEnv(map[string]string{"STRICTCLI_MERGE_OVERRIDE": "new"})
	count := 0
	for _, entry := range merged {
		if strings.HasPrefix(entry, "STRICTCLI_MERGE_OVERRIDE=") {
			count++
			if entry != "STRICTCLI_MERGE_OVERRIDE=new" {
				t.Fatalf("expected the override to win, got %q", entry)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one entry for the overridden var, got %d", count)
	}
}

func TestDecodeEffectOutputIsStrictUTF8AndTrimsOneNewline(t *testing.T) {
	got, err := decodeEffectOutput([]byte("hello\n"), "go", "run")
	if err != nil || got != "hello" {
		t.Fatalf("got %q err=%v", got, err)
	}
	got, err = decodeEffectOutput([]byte("a\n\n"), "go", "run")
	if err != nil || got != "a\n" {
		t.Fatalf("exactly one trailing newline is removed, got %q", got)
	}
	_, err = decodeEffectOutput([]byte{0xff, 0xfe}, "go", "run")
	want := `command "go": effects.run produced output that is not valid UTF-8`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
}

func TestSpawnRunsAndWaitCarriesTheExitCode(t *testing.T) {
	var pid int
	var code int
	var waitErr error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		s, err := ctx.Effects().Spawn(echoArgv(),
			EffectEnv(map[string]string{echoActiveEnv: "1", echoOutEnv: "spawned"}))
		if err != nil {
			ctx.Error(err.Error())
			return Exit(1)
		}
		pid = s.PID()
		var done Completed
		done, waitErr = s.Wait()
		if waitErr == nil {
			code = done.ExitCode()
		}
		return Exit(0)
	})
	r := app.Test([]string{"go"})
	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if pid <= 0 || waitErr != nil || code != 0 {
		t.Fatalf("pid=%d code=%d err=%v", pid, code, waitErr)
	}
	log := app.EffectLog()
	if len(log) != 1 || log[0]["kind"] != ProcSpawn || log[0]["recorded"] != false {
		t.Fatalf("live spawn record = %#v", log)
	}
}

func TestSpawnWaitFailureIsAnErrorAndCheckFalseOptsOut(t *testing.T) {
	var failErr error
	var optedOutCode int
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		failEnv := EffectEnv(map[string]string{echoActiveEnv: "1", echoOutEnv: "", echoCodeEnv: "1"})
		s, _ := ctx.Effects().Spawn(echoArgv(), failEnv)
		_, failErr = s.Wait()
		s2, _ := ctx.Effects().Spawn(echoArgv(), failEnv)
		done, _ := s2.Wait(Check(false))
		optedOutCode = done.ExitCode()
		return Exit(0)
	})
	app.Test([]string{"go"})
	if failErr == nil || !strings.Contains(failErr.Error(), "effects.spawn failed:") {
		t.Fatalf("a nonzero spawned exit must be an error, got %v", failErr)
	}
	if optedOutCode != 1 {
		t.Fatalf("Check(false) must return the real exit code, got %d", optedOutCode)
	}
}

func TestExtractingFromAnUnsettledSpawnedTruncates(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		s, _ := ctx.Effects().Spawn([]interface{}{"daemon"})
		_ = s.PID()
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "«step 1 output»") {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestWaitingOnAnUnsettledSpawnedTruncates(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		s, _ := ctx.Effects().Spawn([]interface{}{"daemon"})
		s.Wait()
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "dry-run preview ends at step 2") {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestCoverageShardRecordsACacheWrite(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	app := NewApp("app", "1.0.0", "h",
		WithTestCoverageDir(declaredCoverageDir(t, dir)))
	app.Command("go", "h",
		func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) },
		WithEffect(EffectReadOnly))
	app.Test([]string{"go"})
	log := app.EffectLog()
	if len(log) != 1 || log[0]["kind"] != CacheWrite || log[0]["recorded"] != false {
		t.Fatalf("expected one recorded:false cache write, got %#v", log)
	}
	if !strings.HasSuffix(log[0]["detail"].(string), ".jsonl") {
		t.Fatalf("expected the coverage shard path, got %#v", log[0])
	}
}

// A cache write is never rendered, so it must never take a would-do number.
// The same counter feeds the log lines, the «step N output» brand and the
// truncation error's "ends at step N": an invisible record shifting it would
// move user-visible numbering for no visible reason.
func TestCacheWritesDoNotConsumeWouldDoNumbers(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	app := NewApp("app", "1.0.0", "h",
		WithTestCoverageDir(declaredCoverageDir(t, dir)))
	app.Command("rel", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		c, _ := ctx.Effects().Run([]any{"git", "tag", "v1"})
		ctx.Effects().Run([]any{"push", c})
		return Exit(0)
	}, WithEffect(EffectMutating))
	r := app.Test([]string{"--dry-run", "rel"})
	want := "DRY RUN — no changes were made. Would do:\n" +
		"  1. run: git tag v1\n" +
		"  2. run: push «step 1 output»\n"
	if r.Stdout != want {
		t.Fatalf("stdout = %q, want %q", r.Stdout, want)
	}
	sawCache := false
	var appSeqs []int
	for _, rec := range app.EffectLog() {
		if rec["kind"] == CacheWrite {
			sawCache = true
			continue
		}
		appSeqs = append(appSeqs, rec["seq"].(int))
	}
	if !sawCache {
		t.Fatal("expected a cache write in the log")
	}
	if len(appSeqs) != 2 || appSeqs[0] != 1 || appSeqs[1] != 2 {
		t.Fatalf("application effect seqs = %v, want [1 2]", appSeqs)
	}
}

func TestCacheWritesDoNotShiftTheTruncationStep(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	app := NewApp("app", "1.0.0", "h",
		WithTestCoverageDir(declaredCoverageDir(t, dir)))
	app.Command("rel", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		c, _ := ctx.Effects().Run([]any{"git", "tag", "v1"})
		_ = c.Stdout() // extraction: truncates
		return Exit(0)
	}, WithEffect(EffectMutating))
	r := app.Test([]string{"--dry-run", "rel"})
	want := "error: dry-run preview ends at step 2: rel branched on unsettled " +
		"value «step 1 output» — cannot preview past this point"
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, want) {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// Go is immune to a carrier reaching `check` or `stream` (§2.5.5's excluding
// side) by construction: both options are minted by constructors typed
// func(bool) EffectOption, so a carrier there does not compile. Python and
// TypeScript, whose options are keyword arguments / an options object, need a
// runtime guard; this test pins the reason Go does not.
func TestCheckAndStreamOptionsAreStaticallyBool(t *testing.T) {
	var check func(bool) EffectOption = Check
	var stream func(bool) EffectOption = Stream
	if check(true).name != "check" || stream(true).name != "stream" {
		t.Fatal("expected the canonical snake_case option names")
	}
}

// The exit hook is the Go counterpart of Python's atexit and Node's
// process.on("exit"): Run ends in os.Exit, so a caller that needs to read a
// post-dispatch diagnostic (EffectLog above all) has no other seam. Run itself
// cannot be exercised in-process, so the seam is pinned directly.
func TestSetExitHookRunsOnce(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	calls := 0
	app.SetExitHook(func() { calls++ })
	app.runExitHook()
	if calls != 1 {
		t.Fatalf("expected the registered hook to run once, got %d calls", calls)
	}
}

func TestRunExitHookIsANoOpWhenUnset(t *testing.T) {
	app := NewApp("app", "1.0.0", "h")
	app.runExitHook() // must not panic
}
