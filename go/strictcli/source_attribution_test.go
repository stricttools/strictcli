package strictcli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Env and config source attribution
// ---------------------------------------------------------------------------

// TestEnvSourceLabel verifies that a flag set by an environment variable
// gets source "env" (not "cli").
func TestEnvSourceLabel(t *testing.T) {
	os.Setenv("MYAPP_LEVEL", "42")
	defer os.Unsetenv("MYAPP_LEVEL")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	var capturedCtx *Context
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Env("MYAPP_LEVEL"), Required()),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("level") != "env" {
		t.Fatalf("expected source 'env' for level, got %q", capturedCtx.Source("level"))
	}
}

// TestConfigSourceLabel verifies that a flag set by a config file
// gets source "config" (not "cli").
func TestConfigSourceLabel(t *testing.T) {
	// Create a temp config file
	tmpDir := t.TempDir()
	configFile := tmpDir + "/config.json"
	os.WriteFile(configFile, []byte(`{"level": 7}`), 0o644)

	app := NewApp("myapp", "1.0.0", "test app",
		WithConfig(), WithConfigPath(configFile))
	var capturedCtx *Context
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Default(0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("level") != "config" {
		t.Fatalf("expected source 'config' for level, got %q", capturedCtx.Source("level"))
	}
}

// TestCliOverridesConfig verifies that a flag set by CLI overriding config
// gets source "cli" (not "config").
func TestCliOverridesConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := tmpDir + "/config.json"
	os.WriteFile(configFile, []byte(`{"level": 7}`), 0o644)

	app := NewApp("myapp", "1.0.0", "test app",
		WithConfig(), WithConfigPath(configFile))
	var capturedCtx *Context
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Default(0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--level", "99"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("level") != "cli" {
		t.Fatalf("expected source 'cli' for level, got %q", capturedCtx.Source("level"))
	}
}

// TestCliOverridesEnv verifies that a flag set by CLI overriding env
// gets source "cli" (not "env").
func TestCliOverridesEnv(t *testing.T) {
	os.Setenv("MYAPP_LEVEL", "42")
	defer os.Unsetenv("MYAPP_LEVEL")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	var capturedCtx *Context
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Env("MYAPP_LEVEL"), Required()),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--level", "99"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("level") != "cli" {
		t.Fatalf("expected source 'cli' for level, got %q", capturedCtx.Source("level"))
	}
}

// TestEnvOverridesConfig verifies that a flag set by env overriding config
// gets source "env" (not "config").
func TestEnvOverridesConfig(t *testing.T) {
	os.Setenv("MYAPP_LEVEL", "42")
	defer os.Unsetenv("MYAPP_LEVEL")

	tmpDir := t.TempDir()
	configFile := tmpDir + "/config.json"
	os.WriteFile(configFile, []byte(`{"level": 7}`), 0o644)

	app := NewApp("myapp", "1.0.0", "test app",
		WithEnvPrefix("MYAPP"), WithConfig(), WithConfigPath(configFile))
	var capturedCtx *Context
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Env("MYAPP_LEVEL"), Default(0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("level") != "env" {
		t.Fatalf("expected source 'env' for level, got %q", capturedCtx.Source("level"))
	}
}

// TestDefaultWithConfigAvailableButAbsent verifies that a defaulted flag
// with config available but key absent gets source "default".
func TestDefaultWithConfigAvailableButAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := tmpDir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("myapp", "1.0.0", "test app",
		WithConfig(), WithConfigPath(configFile))
	var capturedCtx *Context
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Default(0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("level") != "default" {
		t.Fatalf("expected source 'default' for level, got %q", capturedCtx.Source("level"))
	}
}

// ---------------------------------------------------------------------------
// Global flag source attribution
// ---------------------------------------------------------------------------

// TestGlobalFlagEnvSource verifies that a global flag set by env var
// gets source "env".
func TestGlobalFlagEnvSource(t *testing.T) {
	os.Setenv("MYAPP_VERBOSE", "true")
	defer os.Unsetenv("MYAPP_VERBOSE")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.GlobalFlag(BoolFlag("loud", "loud mode", Env("MYAPP_VERBOSE"), Default(false)))
	var capturedCtx *Context
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("loud") != "env" {
		t.Fatalf("expected source 'env' for loud, got %q", capturedCtx.Source("loud"))
	}
}

// TestGlobalFlagConfigSource verifies that a global flag set by config
// gets source "config".
func TestGlobalFlagConfigSource(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := tmpDir + "/config.json"
	os.WriteFile(configFile, []byte(`{"loud": true}`), 0o644)

	app := NewApp("myapp", "1.0.0", "test app",
		WithConfig(), WithConfigPath(configFile))
	app.GlobalFlag(BoolFlag("loud", "loud mode", Default(false)))
	var capturedCtx *Context
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("loud") != "config" {
		t.Fatalf("expected source 'config' for loud, got %q", capturedCtx.Source("loud"))
	}
}

// TestGlobalFlagCliSource verifies that a global flag passed on CLI
// gets source "cli".
func TestGlobalFlagCliSource(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(BoolFlag("loud", "loud mode", Default(false)))
	var capturedCtx *Context
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"--loud", "run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("loud") != "cli" {
		t.Fatalf("expected source 'cli' for loud, got %q", capturedCtx.Source("loud"))
	}
}

// TestGlobalFlagDefaultSource verifies that a global flag with only a default
// gets source "default".
func TestGlobalFlagDefaultSource(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(BoolFlag("loud", "loud mode", Default(false)))
	var capturedCtx *Context
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if capturedCtx.Source("loud") != "default" {
		t.Fatalf("expected source 'default' for loud, got %q", capturedCtx.Source("loud"))
	}
}

// ---------------------------------------------------------------------------
// config show reports env source
// ---------------------------------------------------------------------------

// TestConfigShowReportsEnv verifies that config show reports "env" for a
// flag whose value comes from an environment variable. This is new capability
// -- config show previously could not detect env sources.
func TestConfigShowReportsEnv(t *testing.T) {
	os.Setenv("MYAPP_LEVEL", "42")
	defer os.Unsetenv("MYAPP_LEVEL")

	app := NewApp("myapp", "1.0.0", "test app",
		WithEnvPrefix("MYAPP"), WithConfig())
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Env("MYAPP_LEVEL"), Default(0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "show", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(envelopePayload(t, r.Stdout), &result); err != nil {
		t.Fatalf("failed to parse JSON: %s\nstdout: %s", err, r.Stdout)
	}
	levelEntry, ok := result["level"].(map[string]interface{})
	if !ok {
		t.Fatal("missing 'level' in config show output")
	}
	if levelEntry["source"] != "env" {
		t.Fatalf("expected source 'env' for level in config show, got %q", levelEntry["source"])
	}
}

// TestConfigShowReportsConfig verifies that config show reports "config" for
// a flag whose value comes from the config file.
func TestConfigShowReportsConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := tmpDir + "/config.json"
	os.WriteFile(configFile, []byte(`{"level": 7}`), 0o644)

	app := NewApp("myapp", "1.0.0", "test app",
		WithConfig(), WithConfigPath(configFile))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Default(0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "show", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(envelopePayload(t, r.Stdout), &result); err != nil {
		t.Fatalf("failed to parse JSON: %s", err)
	}
	levelEntry := result["level"].(map[string]interface{})
	if levelEntry["source"] != "config" {
		t.Fatalf("expected source 'config' for level, got %q", levelEntry["source"])
	}
}

// TestConfigShowReportsDefault verifies that config show reports "default"
// for a flag with no config or env value.
func TestConfigShowReportsDefault(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Default(0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "show", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(envelopePayload(t, r.Stdout), &result); err != nil {
		t.Fatalf("failed to parse JSON: %s", err)
	}
	levelEntry := result["level"].(map[string]interface{})
	if levelEntry["source"] != "default" {
		t.Fatalf("expected source 'default' for level, got %q", levelEntry["source"])
	}
}

// ---------------------------------------------------------------------------
// Invoke/call-path provenance
// ---------------------------------------------------------------------------

// TestInvokeProvidedKwargSourceIsCli verifies that a kwarg provided via
// invoke gets source "cli".
func TestInvokeProvidedKwargSourceIsCli(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	var capturedCtx *Context
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Default(0)),
	), WithEffect(EffectReadOnly))

	ir := app.invoke("run", map[string]interface{}{"level": 42})
	if ir.err != "" {
		t.Fatalf("invoke failed: %s", ir.err)
	}
	if capturedCtx.Source("level") != "cli" {
		t.Fatalf("expected source 'cli' for level, got %q", capturedCtx.Source("level"))
	}
}

// TestInvokeAbsentKwargSourceIsDefault verifies that a kwarg not provided
// via invoke (and thus defaulted) gets source "default".
func TestInvokeAbsentKwargSourceIsDefault(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	var capturedCtx *Context
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Default(0)),
	), WithEffect(EffectReadOnly))

	ir := app.invoke("run", map[string]interface{}{})
	if ir.err != "" {
		t.Fatalf("invoke failed: %s", ir.err)
	}
	if capturedCtx.Source("level") != "default" {
		t.Fatalf("expected source 'default' for level, got %q", capturedCtx.Source("level"))
	}
}

// ---------------------------------------------------------------------------
// Byte-parity: config show output format
// ---------------------------------------------------------------------------

// TestConfigShowPlainFormat verifies that the plain output format matches
// between Go and Python (param = value  (source: label)).
func TestConfigShowPlainFormat(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Default(0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	// Should contain "level = 0  (source: default)"
	if !strings.Contains(r.Stdout, "level = 0  (source: default)") {
		t.Fatalf("expected 'level = 0  (source: default)' in output, got: %s", r.Stdout)
	}
}
