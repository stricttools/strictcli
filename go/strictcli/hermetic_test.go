package strictcli

import (
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The --hermetic reserved flag.
// ---------------------------------------------------------------------------

// TestHermeticReservedName verifies that "hermetic" cannot be used as a
// user-defined global flag name.
func TestHermeticReservedName(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for reserved 'hermetic' global flag name")
		}
		msg := r.(string)
		if !strings.Contains(msg, "reserved") {
			t.Fatalf("expected 'reserved' in panic message, got: %s", msg)
		}
	}()
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(BoolFlag("hermetic", "should be rejected", Default(false)))
	_ = app
}

// TestHermeticSkipsEnv verifies that --hermetic ignores environment variables.
func TestHermeticSkipsEnv(t *testing.T) {
	os.Setenv("MYAPP_LEVEL", "42")
	defer os.Unsetenv("MYAPP_LEVEL")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	var capturedCtx *Context
	var levelVal interface{}
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		levelVal = kwargs["level"]
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Env("MYAPP_LEVEL"), Default(0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"--hermetic", "run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	// Under hermetic, env is ignored, so level should be default (0), source "default"
	if levelVal != 0 {
		t.Fatalf("expected level=0 (default), got %v", levelVal)
	}
	if capturedCtx.Source("level") != "default" {
		t.Fatalf("expected source 'default' for level under hermetic, got %q", capturedCtx.Source("level"))
	}
}

// TestHermeticSkipsEnvGlobalFlags verifies that --hermetic ignores env vars
// for global flags too.
func TestHermeticSkipsEnvGlobalFlags(t *testing.T) {
	os.Setenv("MYAPP_VERBOSE", "true")
	defer os.Unsetenv("MYAPP_VERBOSE")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.GlobalFlag(BoolFlag("loud", "loud mode", Env("MYAPP_VERBOSE"), Default(false)))
	var capturedCtx *Context
	var loudVal interface{}
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		loudVal = kwargs["loud"]
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"--hermetic", "run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if loudVal != false {
		t.Fatalf("expected loud=false (default), got %v", loudVal)
	}
	if capturedCtx.Source("loud") != "default" {
		t.Fatalf("expected source 'default' for loud under hermetic, got %q", capturedCtx.Source("loud"))
	}
}

// TestHermeticCLIFlagStillWorks verifies that flags passed on CLI work under hermetic.
func TestHermeticCLIFlagStillWorks(t *testing.T) {
	os.Setenv("MYAPP_LEVEL", "42")
	defer os.Unsetenv("MYAPP_LEVEL")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	var capturedCtx *Context
	var levelVal interface{}
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		levelVal = kwargs["level"]
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Env("MYAPP_LEVEL"), Default(0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"--hermetic", "run", "--level", "99"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if levelVal != 99 {
		t.Fatalf("expected level=99 (cli), got %v", levelVal)
	}
	if capturedCtx.Source("level") != "cli" {
		t.Fatalf("expected source 'cli' for level under hermetic, got %q", capturedCtx.Source("level"))
	}
}

// TestHermeticSkipsConfig verifies that --hermetic ignores config files.
func TestHermeticSkipsConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := tmpDir + "/config.json"
	os.WriteFile(configFile, []byte(`{"level": 7}`), 0o644)

	app := NewApp("myapp", "1.0.0", "test app",
		WithConfig(), WithConfigPath(configFile))
	var capturedCtx *Context
	var levelVal interface{}
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		capturedCtx = ctx
		levelVal = kwargs["level"]
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Default(0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"--hermetic", "run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if levelVal != 0 {
		t.Fatalf("expected level=0 (default), got %v", levelVal)
	}
	if capturedCtx.Source("level") != "default" {
		t.Fatalf("expected source 'default' for level under hermetic, got %q", capturedCtx.Source("level"))
	}
}

// TestHermeticConfigMutualExclusion verifies --hermetic + --config is an error.
func TestHermeticConfigMutualExclusion(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"--hermetic", "--config", "/tmp/foo.json", "run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--hermetic and --config are mutually exclusive") {
		t.Fatalf("expected mutual exclusion error, got: %s", r.Stderr)
	}
}

// TestHermeticConfigSubcommandError verifies --hermetic + config subcommand is an error.
func TestHermeticConfigSubcommandError(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"--hermetic", "config", "show"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--hermetic cannot be used with config commands") {
		t.Fatalf("expected config command error, got: %s", r.Stderr)
	}
}

// TestHermeticRequiredFlagMissing verifies that under --hermetic, a required flag
// that would normally be satisfied by env produces a hard error.
func TestHermeticRequiredFlagMissing(t *testing.T) {
	os.Setenv("MYAPP_NAME", "test-value")
	defer os.Unsetenv("MYAPP_NAME")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		StringFlag("name", "name to use", Env("MYAPP_NAME"), Required()),
	), WithEffect(EffectReadOnly))

	// Without hermetic, this would succeed (env provides the value).
	// With hermetic, env is ignored, so the required flag is missing.
	r := app.Test([]string{"--hermetic", "run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "required") {
		t.Fatalf("expected 'required' in error, got: %s", r.Stderr)
	}
}

// TestHermeticOnAppWithoutConfig verifies that --hermetic on an app
// without config enabled is fine (it still disables env).
func TestHermeticOnAppWithoutConfig(t *testing.T) {
	os.Setenv("MYAPP_LEVEL", "42")
	defer os.Unsetenv("MYAPP_LEVEL")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	var levelVal interface{}
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		levelVal = kwargs["level"]
		return Exit(0)
	}, WithFlags(
		IntFlag("level", "verbosity level", Env("MYAPP_LEVEL"), Default(0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"--hermetic", "run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected success, got exit code %d: %s", r.ExitCode, r.Stderr)
	}
	if levelVal != 0 {
		t.Fatalf("expected level=0 (default), got %v", levelVal)
	}
}

// TestHermeticRequiredBoolMissing verifies that under --hermetic, a required
// bool flag with no CLI value produces a hard error.
func TestHermeticRequiredBoolMissing(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		BoolFlag("loud", "enable loud", Required()),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"--hermetic", "run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "must be passed") {
		t.Fatalf("expected 'must be passed' in error, got: %s", r.Stderr)
	}
}

// TestHermeticAbsentFromSchema verifies that --hermetic is NOT
// listed in the schema output. We test this by checking that
// "hermetic" is not in global_flags (since it's intercepted in
// the pre-scan, not registered as a global flag).
func TestHermeticAbsentFromSchema(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(BoolFlag("loud", "enable loud", Default(false)))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	// Verify hermetic is not in globalFlags (which feed the schema)
	for _, gf := range app.globalFlags {
		if gf.Name == "hermetic" {
			t.Fatal("hermetic should not appear in global flags")
		}
	}
}
