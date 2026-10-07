package strictcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The framework's own commands report through the error writer (contract
// §19.14's box): `error: <message>` once in human mode, an unprefixed error
// diagnostic under --json, nothing on stderr.

func frameworkConfigApp(t *testing.T, exists bool) (*App, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if exists {
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := NewApp("myapp", "1.0.0", "test app", WithConfig(), WithConfigPath(path))
	app.Command("run", "run", func(ctx *Context, _ map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly), WithFlags(IntFlag("count", "how many", Default(0))))
	return app, path
}

func TestConfigSetUnknownKeyPrintsOnceWithThePrefix(t *testing.T) {
	app, _ := frameworkConfigApp(t, false)
	r := app.Test([]string{"config", "set", "nope", "--value", "1"})
	if r.ExitCode != 1 || r.Stdout != "" || r.Stderr != "error: config set: unknown key 'nope'\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestConfigSetBadValuePrintsOnceWithThePrefix(t *testing.T) {
	app, _ := frameworkConfigApp(t, false)
	r := app.Test([]string{"config", "set", "count", "--value", "abc"})
	if r.Stderr != "error: config set: key 'count': expected integer, got 'abc'\n" {
		t.Fatalf("stderr=%q", r.Stderr)
	}
}

func TestConfigSetErrorUnderJSONIsADiagnostic(t *testing.T) {
	app, _ := frameworkConfigApp(t, false)
	r := app.Test([]string{"--json", "config", "set", "nope", "--value", "1"})
	if r.ExitCode != 1 || r.Stderr != "" ||
		!strings.Contains(r.Stdout, `"diagnostics":[{"level":"error","message":"config set: unknown key 'nope'"}]`) {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestConfigInitRefusalGoesThroughTheWriter(t *testing.T) {
	app, path := frameworkConfigApp(t, true)
	r := app.Test([]string{"config", "init"})
	if r.ExitCode != 1 || r.Stderr != "error: config init: config file already exists: "+path+"\n" {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	r = app.Test([]string{"--json", "config", "init"})
	if r.Stderr != "" || !strings.Contains(r.Stdout, `{"level":"error","message":"config init: config file already exists: `+path+`"}`) {
		t.Fatalf("stdout=%q stderr=%q", r.Stdout, r.Stderr)
	}
}

func TestCheckTagExpressionErrorGoesThroughTheWriter(t *testing.T) {
	checksPath := writeChecksFile(t, validChecksToml)
	app := NewApp("testapp", "1.0.0", "test app", WithChecks(checksPath))
	dropBuiltinCheckProviders(app)
	app.RegisterErrorCheck("lint-code", func(ctx CheckContext, _ *ErrorReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.RegisterWarnCheck("check-deps", func(ctx CheckContext, _ *WarnReporter) CheckOutcome {
		return passOutcome("ok")
	})
	app.SetCheckContext(func(*Context) (CheckContext, error) { return nil, nil })
	r := app.Test([]string{"check", "--tag", "(("})
	if r.ExitCode != 1 || r.Stdout != "" || r.Stderr != "error: tag expression: unexpected end of expression at position 2\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
}
