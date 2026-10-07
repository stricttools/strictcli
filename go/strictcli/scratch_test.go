package strictcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scratchHome pins HOME to a fresh temp directory, under which the scratch
// directories of the test's runs are made.
func scratchHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// scratchApp is an app whose commands record the scratch directory their
// handler was given, and write a file into it the way a program the handler
// runs would.
func scratchApp(t *testing.T, seen *string, seenErr *error) *App {
	t.Helper()
	handler := func(ctx *Context, _ map[string]interface{}) Outcome {
		dir, err := ctx.ScratchDir()
		*seen, *seenErr = dir, err
		if err != nil {
			ctx.Error(err.Error())
			return Exit(1)
		}
		again, err := ctx.ScratchDir()
		if err != nil || again != dir {
			ctx.Error("a second call gave another scratch directory")
			return Exit(1)
		}
		if err := os.WriteFile(filepath.Join(dir, "cache"), []byte("x"), 0o644); err != nil {
			ctx.Error(err.Error())
			return Exit(1)
		}
		return Exit(0)
	}
	app := NewApp("app", "1.0.0", "scratch fixture")
	app.Command("list", "list", handler, WithEffect(EffectReadOnly), WithScratchDir())
	app.Command("plan", "plan", handler, WithEffect(EffectMutating), WithScratchDir())
	app.Command("plain", "plain", handler, WithEffect(EffectReadOnly))
	return app
}

func TestAReadOnlyCommandDeclaringAScratchDirectoryGetsOneOutsideTheRepositoryRemovedAfterTheRun(t *testing.T) {
	home := scratchHome(t)
	var seen string
	var seenErr error
	r := scratchApp(t, &seen, &seenErr).Test([]string{"list"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	root := filepath.Join(home, ".cache", "strictcli", "scratch")
	if filepath.Dir(seen) != root {
		t.Fatalf("the scratch directory %s is not under %s", seen, root)
	}
	if _, err := os.Lstat(seen); !os.IsNotExist(err) {
		t.Fatalf("the scratch directory %s was not removed after the run: %v", seen, err)
	}
}

func TestAScratchDirectoryIsNoWriteUnderDryRun(t *testing.T) {
	scratchHome(t)
	var seen string
	var seenErr error
	r := scratchApp(t, &seen, &seenErr).Test([]string{"plan", "--dry-run"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if seen == "" {
		t.Fatal("the handler got no scratch directory")
	}
	if strings.Contains(r.Stdout, seen) || strings.Contains(r.Stdout, "mkdir") || strings.Contains(r.Stdout, "remove") {
		t.Fatalf("the scratch directory was recorded as an effect:\n%s", r.Stdout)
	}
	if _, err := os.Lstat(seen); !os.IsNotExist(err) {
		t.Fatalf("the scratch directory %s was not removed after the run: %v", seen, err)
	}
}

func TestAScratchDirectoryIsRefusedToACommandThatDoesNotDeclareOne(t *testing.T) {
	home := scratchHome(t)
	var seen string
	var seenErr error
	r := scratchApp(t, &seen, &seenErr).Test([]string{"plain"})
	if r.ExitCode == 0 || seenErr == nil || !strings.Contains(seenErr.Error(), "WithScratchDir") {
		t.Fatalf("an undeclared scratch directory was not refused: exit %d, %v", r.ExitCode, seenErr)
	}
	if _, err := os.Lstat(filepath.Join(home, ".cache")); !os.IsNotExist(err) {
		t.Fatalf("a refused scratch directory left something behind: %v", err)
	}
}

func TestAScratchDirectoryIsMadeOnlyWhenTheHandlerAsksForIt(t *testing.T) {
	home := scratchHome(t)
	app := NewApp("app", "1.0.0", "scratch fixture")
	app.Command("list", "list", func(ctx *Context, _ map[string]interface{}) Outcome { return Exit(0) },
		WithEffect(EffectReadOnly), WithScratchDir())
	if r := app.Test([]string{"list"}); r.ExitCode != 0 {
		t.Fatalf("exit %d: %s", r.ExitCode, r.Stderr)
	}
	if _, err := os.Lstat(filepath.Join(home, ".cache")); !os.IsNotExist(err) {
		t.Fatalf("a run that never asked for its scratch directory made one: %v", err)
	}
}

func TestAScratchDirectoryOfACallIsRemovedAfterIt(t *testing.T) {
	scratchHome(t)
	var seen string
	var seenErr error
	if _, err := scratchApp(t, &seen, &seenErr).Call("list", map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	if seen == "" {
		t.Fatal("the handler got no scratch directory")
	}
	if _, err := os.Lstat(seen); !os.IsNotExist(err) {
		t.Fatalf("the scratch directory %s was not removed after the call: %v", seen, err)
	}
}

func TestTheCheckCommandsGiveTheCheckContextFactoryAScratchDirectory(t *testing.T) {
	scratchHome(t)
	checksPath := writeChecksFile(t, twoChecksToml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	for _, name := range []string{"version-consistency", "changelog-coverage"} {
		app.RegisterErrorCheck(name, func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
			return passOutcome("ok")
		})
	}
	var seen string
	app.SetCheckContext(func(ctx *Context) (CheckContext, error) {
		dir, err := ctx.ScratchDir()
		seen = dir
		return &testCheckContext{root: emptyProjectRoot}, err
	})
	for _, command := range []string{"check", "failing-checks"} {
		seen = ""
		r := app.Test([]string{command, "--all"})
		if r.ExitCode != 0 || seen == "" {
			t.Fatalf("%s: exit %d, scratch %q: %s%s", command, r.ExitCode, seen, r.Stdout, r.Stderr)
		}
		if _, err := os.Lstat(seen); !os.IsNotExist(err) {
			t.Fatalf("%s: the scratch directory %s was not removed: %v", command, seen, err)
		}
	}
}
