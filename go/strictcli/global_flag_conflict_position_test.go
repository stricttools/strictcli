package strictcli

// Global-flag config-conflict position + post-command provenance.
//
// Global flags may appear before the command (`tool --g X cmd`) or after it
// (`tool cmd --g X`). Config-conflict detection (error mode) and source
// provenance must behave identically regardless of position.

import (
	"os"
	"strings"
	"testing"
)

func writeTestConfigJSON(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/config.json"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// buildGlobalConflictApp builds an app with a global str flag "settings" and a
// bare "run" command. If configContent is non-empty, config is enabled with
// the given conflict mode. flagConflictMode, when non-empty, sets a per-flag
// override. env, when non-empty, binds the global flag to that env var.
func buildGlobalConflictApp(t *testing.T, conflictMode, flagConflictMode, configContent, env string) *App {
	t.Helper()
	opts := []AppOption{}
	if configContent != "" {
		path := writeTestConfigJSON(t, configContent)
		opts = append(opts, WithConfig(), WithConfigPath(path))
		if conflictMode != "" {
			opts = append(opts, WithConfigConflictMode(conflictMode))
		}
	}
	app := NewApp("myapp", "1.0.0", "test app", opts...)

	flagOpts := []FlagOption{Default("default-val")}
	if env != "" {
		flagOpts = append(flagOpts, Env(env))
	}
	if flagConflictMode != "" {
		flagOpts = append(flagOpts, ConflictMode(flagConflictMode))
	}
	app.GlobalFlag(StringFlag("settings", "settings path", flagOpts...))

	app.Command("run", "run something", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	return app
}

func TestPostCommandGlobalConflictFiresAppErrorMode(t *testing.T) {
	app := buildGlobalConflictApp(t, "error", "", `{"settings": "from-config"}`, "")
	r := app.Test([]string{"run", "--settings", "from-cli"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d (stdout=%q)", r.ExitCode, r.Stdout)
	}
	want := "flag 'settings' set in both cli and config; remove one"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr %q does not contain %q", r.Stderr, want)
	}
}

func TestPostCommandGlobalConflictFiresPerFlagErrorMode(t *testing.T) {
	app := buildGlobalConflictApp(t, "cli-wins", "error", `{"settings": "from-config"}`, "")
	r := app.Test([]string{"run", "--settings", "from-cli"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d (stdout=%q)", r.ExitCode, r.Stdout)
	}
	want := "flag 'settings' set in both cli and config; remove one"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr %q does not contain %q", r.Stderr, want)
	}
}

func TestPreCommandGlobalConflictStillFires(t *testing.T) {
	app := buildGlobalConflictApp(t, "error", "", `{"settings": "from-config"}`, "")
	r := app.Test([]string{"--settings", "from-cli", "run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d (stdout=%q)", r.ExitCode, r.Stdout)
	}
	want := "flag 'settings' set in both cli and config; remove one"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr %q does not contain %q", r.Stderr, want)
	}
}

func TestPostCommandGlobalMatchingValuePasses(t *testing.T) {
	app := buildGlobalConflictApp(t, "error", "", `{"settings": "same"}`, "")
	r := app.Test([]string{"run", "--settings", "same"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", r.ExitCode, r.Stderr)
	}
}

func TestPostCommandGlobalAllowModeSilentWins(t *testing.T) {
	app := buildGlobalConflictApp(t, "cli-wins", "", `{"settings": "from-config"}`, "")
	r := app.Test([]string{"run", "--settings", "from-cli"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", r.ExitCode, r.Stderr)
	}
}

func TestEnvMatchedThenPostCliDivergesNamesCliAndConfig(t *testing.T) {
	os.Setenv("MYAPP_SETTINGS", "shared")
	defer os.Unsetenv("MYAPP_SETTINGS")
	app := buildGlobalConflictApp(t, "error", "", `{"settings": "shared"}`, "MYAPP_SETTINGS")
	r := app.Test([]string{"run", "--settings", "from-cli"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d (stdout=%q)", r.ExitCode, r.Stdout)
	}
	want := "flag 'settings' set in both cli and config; remove one"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr %q does not contain %q", r.Stderr, want)
	}
}

func TestPostCommandGlobalProvenanceIsCli(t *testing.T) {
	var capturedCtx *Context
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(StringFlag("settings", "settings path", Default("default-val")))
	app.Command("run", "run something", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"run", "--settings", "from-cli"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("settings") != "cli" {
		t.Fatalf("expected source 'cli' for settings, got %q", capturedCtx.Source("settings"))
	}
}

func TestPreCommandGlobalProvenanceIsCli(t *testing.T) {
	var capturedCtx *Context
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(StringFlag("settings", "settings path", Default("default-val")))
	app.Command("run", "run something", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"--settings", "from-cli", "run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("settings") != "cli" {
		t.Fatalf("expected source 'cli' for settings, got %q", capturedCtx.Source("settings"))
	}
}

func TestPostCommandGlobalProvenanceConfigWhenNotOnCli(t *testing.T) {
	path := writeTestConfigJSON(t, `{"settings": "from-config"}`)
	var capturedCtx *Context
	app := NewApp("myapp", "1.0.0", "test app", WithConfig(), WithConfigPath(path))
	app.GlobalFlag(StringFlag("settings", "settings path", Default("default-val")))
	app.Command("run", "run something", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d (stderr=%q)", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("settings") != "config" {
		t.Fatalf("expected source 'config' for settings, got %q", capturedCtx.Source("settings"))
	}
}
