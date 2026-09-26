package strictcli

// The would-do log renders on every exit path out of a dry-mode dispatch, not
// just the normal return.
//
// (*App).Run exits the process and (*App).Test cannot survive a panicking
// handler (its capture pipes are never drained), so these tests re-invoke the
// test binary as a helper and assert on the child's streams and exit code --
// the same shape effects_confirm_test.go uses for the confirm protocol.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const exitPathArgvEnv = "STRICTCLI_EXIT_PATH_ARGV"

const exitPathLog = "DRY RUN — no changes were made. Would do:\n" +
	"  1. write: report.txt (2 bytes)\n"

// TestExitPathRunHelper is the child process: every command records one effect
// and then leaves the dispatch by a different route.
func TestExitPathRunHelper(t *testing.T) {
	argv, armed := os.LookupEnv(exitPathArgvEnv)
	if !armed {
		t.Skip("helper process; armed only when re-invoked by an exit-path test")
	}
	app := NewApp("app", "1.0.0", "exit-path fixture")
	record := func(ctx *Context) { ctx.Effects().Write("report.txt", "ok") }

	app.Command("returns", "return a non-zero exit code",
		func(ctx *Context, kwargs map[string]interface{}) Outcome {
			record(ctx)
			return Exit(3)
		}, WithEffect(EffectMutating))
	app.Command("panics", "panic after recording",
		func(ctx *Context, kwargs map[string]interface{}) Outcome {
			record(ctx)
			panic("kaboom")
		}, WithEffect(EffectMutating))
	app.Command("exits", "call os.Exit after recording",
		func(ctx *Context, kwargs map[string]interface{}) Outcome {
			record(ctx)
			os.Exit(1)
			return Exit(0)
		}, WithEffect(EffectMutating))
	app.Command("looks", "a read_only command that panics",
		func(ctx *Context, kwargs map[string]interface{}) Outcome {
			panic("kaboom")
		}, WithEffect(EffectReadOnly))
	grp := app.Group("release", "release commands")
	grp.Command("run", "panic from a nested command",
		func(ctx *Context, kwargs map[string]interface{}) Outcome {
			record(ctx)
			panic("kaboom")
		}, WithEffect(EffectMutating))

	// A panic that escapes the framework would otherwise unwind into the
	// testing package, which writes its own "--- FAIL" banner to the child's
	// stdout and would pollute the very stream under assertion. Handling it
	// here reproduces the runtime's shape (message on stderr, non-zero exit)
	// while leaving stdout to the app alone.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic: %v\n", r)
			os.Exit(2)
		}
	}()
	os.Args = append([]string{"app"}, strings.Fields(argv)...)
	app.Run()
}

func runExitPathHelper(t *testing.T, argv string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestExitPathRunHelper$")
	cmd.Env = append(os.Environ(), exitPathArgvEnv+"="+argv)
	cmd.Stdin = strings.NewReader("")
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("helper failed: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), errb.String(), code
}

func TestWouldDoLogRendersOnANonZeroExit(t *testing.T) {
	stdout, stderr, code := runExitPathHelper(t, "--dry-run returns")
	if code != 3 {
		t.Fatalf("expected the handler's exit code 3, got %d (stderr=%q)", code, stderr)
	}
	if stdout != exitPathLog {
		t.Fatalf("stdout=%q want %q", stdout, exitPathLog)
	}
	if strings.Contains(stderr, "aborted") {
		t.Fatalf("a deliberate exit is not an abort, stderr=%q", stderr)
	}
}

func TestWouldDoLogRendersWhenTheHandlerPanics(t *testing.T) {
	stdout, stderr, _ := runExitPathHelper(t, "--dry-run panics")
	if stdout != exitPathLog {
		t.Fatalf("a panicking handler still owes the recorded preview; stdout=%q", stdout)
	}
	want := "error: dry-run preview ends at step 2: panics aborted — the preview above may be incomplete"
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr=%q want it to contain %q", stderr, want)
	}
	// The panic itself is not swallowed: the runtime still reports it.
	if !strings.Contains(stderr, "kaboom") {
		t.Fatalf("the panic must continue untouched, stderr=%q", stderr)
	}
}

func TestAbortedPreviewNamesTheDottedCommandPath(t *testing.T) {
	_, stderr, _ := runExitPathHelper(t, "--dry-run release run")
	if !strings.Contains(stderr, "release.run aborted") {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestAbortedReadOnlyPreviewIsHeaderWithEmptyBody(t *testing.T) {
	stdout, stderr, _ := runExitPathHelper(t, "--dry-run looks")
	if stdout != dryRunHeader+"\n" {
		t.Fatalf("stdout=%q want %q", stdout, dryRunHeader+"\n")
	}
	if !strings.Contains(stderr, "looks aborted") {
		t.Fatalf("stderr=%q", stderr)
	}
}

func TestNoWouldDoLogWhenAPanickingHandlerRanLive(t *testing.T) {
	stdout, stderr, _ := runExitPathHelper(t, "--yes looks")
	if strings.Contains(stdout, "DRY RUN") {
		t.Fatalf("a live run has no would-do log, stdout=%q", stdout)
	}
	if strings.Contains(stderr, "aborted — the preview") {
		t.Fatalf("a live run has no preview to qualify, stderr=%q", stderr)
	}
}

// os.Exit is the one exit path no framework can instrument: the process is gone
// before any deferred function runs. Pinned so the ceiling stays visible rather
// than being mistaken for the defect this file's other tests cover.
func TestOsExitFromAHandlerSkipsTheWouldDoLog(t *testing.T) {
	stdout, _, code := runExitPathHelper(t, "--dry-run exits")
	if code != 1 {
		t.Fatalf("expected os.Exit's code 1, got %d", code)
	}
	if stdout != "" {
		t.Fatalf("os.Exit runs no deferred code, so nothing can render; stdout=%q", stdout)
	}
}

// --- the seam itself (runSealed owns every render) --------------------------

// sealedFixture arms one dispatch and returns the effects handle plus the app,
// so a test can drive runSealed directly.
func sealedFixture(t *testing.T, dryRun bool) (*App, *Effects) {
	t.Helper()
	app := NewApp("app", "1.0.0", "seam fixture")
	app.Command("go", "h", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectMutating))
	app.beginDispatch()
	return app, app.armEffects(app.commands["go"], "go", dryRun, nil)
}

func TestRunSealedRendersTheLogOnANormalReturn(t *testing.T) {
	app, e := sealedFixture(t, true)
	e.Write("report.txt", "ok")
	var out, errb strings.Builder
	code := app.runSealed(sealedRun{stdout: &out, stderr: &errb, dryRun: true, cmdPath: "go"}, func() int { return 3 })
	if code != 3 {
		t.Fatalf("code=%d", code)
	}
	if out.String() != exitPathLog {
		t.Fatalf("stdout=%q", out.String())
	}
	if errb.String() != "" {
		t.Fatalf("stderr=%q", errb.String())
	}
}

func TestRunSealedRendersTheLogOnAPanicAndRepanics(t *testing.T) {
	app, e := sealedFixture(t, true)
	e.Write("report.txt", "ok")
	var out, errb strings.Builder
	got := mustPanic(t, func() {
		app.runSealed(sealedRun{stdout: &out, stderr: &errb, dryRun: true, cmdPath: "go"}, func() int { panic("kaboom") })
	})
	if got != "kaboom" {
		t.Fatalf("the panic must be re-raised untouched, got %q", got)
	}
	if out.String() != exitPathLog {
		t.Fatalf("stdout=%q", out.String())
	}
	want := "error: dry-run preview ends at step 2: go aborted — the preview above may be incomplete\n"
	if errb.String() != want {
		t.Fatalf("stderr=%q want %q", errb.String(), want)
	}
}

func TestRunSealedRendersNothingOutsideDryMode(t *testing.T) {
	app, e := sealedFixture(t, false)
	e.Write(filepath.Join(t.TempDir(), "report.txt"), "ok")
	var out, errb strings.Builder
	mustPanic(t, func() {
		app.runSealed(sealedRun{stdout: &out, stderr: &errb, dryRun: false, cmdPath: "go"}, func() int { panic("kaboom") })
	})
	if out.String() != "" || errb.String() != "" {
		t.Fatalf("stdout=%q stderr=%q", out.String(), errb.String())
	}
}

func TestRunSealedLeavesTheTruncationPathAlone(t *testing.T) {
	app, e := sealedFixture(t, true)
	u, _ := e.Run([]interface{}{"git", "status"})
	var out, errb strings.Builder
	code := app.runSealed(sealedRun{stdout: &out, stderr: &errb, dryRun: true, cmdPath: "go"}, func() int {
		u.Stdout() // extraction: truncates
		return 0
	})
	if code != 1 {
		t.Fatalf("the truncation path exits 1, got %d", code)
	}
	wantOut := "DRY RUN — no changes were made. Would do:\n  1. run: git status\n"
	if out.String() != wantOut {
		t.Fatalf("stdout=%q want %q", out.String(), wantOut)
	}
	wantErr := "error: dry-run preview ends at step 2: go branched on unsettled " +
		"value «step 1 output» — cannot preview past this point\n"
	if errb.String() != wantErr {
		t.Fatalf("stderr=%q want %q", errb.String(), wantErr)
	}
}

func TestMachineModeEnvelopeOnAnAbortedPreview(t *testing.T) {
	// The envelope is written at the same seam that renders the log in human
	// mode, and the abort marker's text rides preview_error instead of stderr
	// (§19.3). The panic itself still continues untouched.
	stdout, stderr, code := runExitPathHelper(t, "--json --dry-run panics")
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not one JSON document: %v (stdout=%q)", err, stdout)
	}
	// exit_code is "the process's exit status" (§19.2) and §3.5 leaves that
	// status to the language on this path: an unrecovered Go panic exits 2.
	// Asserting the envelope's number against the helper's OBSERVED status is
	// the point -- a hard-coded 1 is exactly the lie this pins shut.
	if code != goPanicExitStatus {
		t.Fatalf("an unrecovered panic exits %d, got %d (stderr=%q)", goPanicExitStatus, code, stderr)
	}
	if env["dry_run"] != true || env["exit_code"].(float64) != float64(code) {
		t.Fatalf("envelope = %v (process exited %d)", env, code)
	}
	if n := len(env["preview"].([]interface{})); n != 1 {
		t.Fatalf("preview holds %d records, want the one recorded before the panic", n)
	}
	pe, ok := env["preview_error"].(map[string]interface{})
	if !ok {
		t.Fatalf("preview_error = %v, want an object", env["preview_error"])
	}
	if pe["kind"] != "aborted" || pe["command"] != "panics" || pe["brand"] != nil {
		t.Fatalf("preview_error = %v", pe)
	}
	want := "error: dry-run preview ends at step 2: panics aborted — the preview above may be incomplete"
	if pe["message"] != want {
		t.Fatalf("message = %q, want %q", pe["message"], want)
	}
	if strings.Contains(stderr, "dry-run preview ends") {
		t.Fatalf("the marker rides the envelope in machine mode, stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "kaboom") {
		t.Fatalf("the panic must continue untouched, stderr=%q", stderr)
	}
}

func TestMachineModeEnvelopeOnALiveAbort(t *testing.T) {
	// The marker's text names a dry-run preview, so it is dry-mode-only --
	// exactly as the human stream's marker is.
	// "looks" records nothing, so a live run of it writes no file into the
	// package directory the way "panics" would.
	stdout, _, _ := runExitPathHelper(t, "--json looks")
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not one JSON document: %v (stdout=%q)", err, stdout)
	}
	if env["preview_error"] != nil {
		t.Fatalf("preview_error = %v, want null on a live abort", env["preview_error"])
	}
}
