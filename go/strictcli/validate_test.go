package strictcli

import (
	"fmt"
	"strings"
	"testing"
)

// The custom validator runs on a SUPPLIED value only: never on the declared
// default, and never on absence (contract §23.5's validate row). A default is
// the declaration deciding, and the declaration is not something the
// invocation asked to have validated.

func positiveInt() FlagOption {
	return ValidateFn(func(v interface{}) error {
		n, ok := v.(int)
		if !ok {
			return fmt.Errorf("must be an integer, got %T", v)
		}
		if n <= 0 {
			return fmt.Errorf("must be a positive integer")
		}
		return nil
	})
}

func TestValidateRunsOnASuppliedValue(t *testing.T) {
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the port", Required(), positiveInt())))
	r := app.Test([]string{"cmd", "--port", "8080"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "port=8080\n" {
		t.Fatalf("expected 'port=8080', got %q", r.Stdout)
	}
	r = app.Test([]string{"cmd", "--port", "0"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--port: must be a positive integer") {
		t.Fatalf("expected validator error, got %q", r.Stderr)
	}
}

func TestValidateNeverRunsOnTheDeclaredDefault(t *testing.T) {
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the port", Default(-5), positiveInt())))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "port=-5\n" {
		t.Fatalf("expected 'port=-5', got %q", r.Stdout)
	}
}

func TestValidateRunsOnAValueSuppliedOverTheDefault(t *testing.T) {
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the port", Default(-5), positiveInt())))
	r := app.Test([]string{"cmd", "--port", "0"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, "--port: must be a positive integer") {
		t.Fatalf("expected validator error, got %q", r.Stderr)
	}
}

func TestValidateNeverRunsOnTheDeclaredListDefault(t *testing.T) {
	// A repeatable flag's declared default is a declaration too: its elements
	// are never handed to the validator.
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the ports", Repeatable(), Unique(false),
			Default([]interface{}{-5, -7}), positiveInt())))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "port=-5,-7\n" {
		t.Fatalf("expected 'port=-5,-7', got %q", r.Stdout)
	}
	// A supplied occurrence is validated.
	r = app.Test([]string{"cmd", "--port", "0"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, "--port: must be a positive integer") {
		t.Fatalf("expected validator error, got %q", r.Stderr)
	}
}

func TestValidateNeverRunsOnAnInfraDefault(t *testing.T) {
	// A RelativeToRoot default is still a declared default (source "infra"),
	// so the validator never sees it.
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("db=%v", kwargs["db"]))
		return Exit(0)
	}, WithFlags(
		StringFlag("db", "db path", Default(RelativeToRoot("MYAPP_HOME", "db.sqlite")),
			ValidateFn(func(v interface{}) error {
				return fmt.Errorf("the validator must not run here")
			})),
	), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "db=/var/lib/myapp/db.sqlite\n" {
		t.Fatalf("expected the resolved infra default, got %q", r.Stdout)
	}
}

func TestValidateRunsOnAnEnvSuppliedValue(t *testing.T) {
	// Env is a supplying source, so the value is validated even though no CLI
	// token carried it -- and the declared default it overrode was not.
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("port=%v", kwargs["port"]))
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "the port", Default(-5), Env("MYAPP_PORT"), positiveInt()),
	), WithEffect(EffectReadOnly))
	t.Setenv("MYAPP_PORT", "0")
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, "--port: must be a positive integer") {
		t.Fatalf("expected validator error, got %q", r.Stderr)
	}
}

func TestValidateNeverRunsOnAbsence(t *testing.T) {
	// A custom validator must not run for a flag that was not passed
	// (resolved value nil) -- there is no value to validate.
	app := simpleApp("cmd", "a command", "name={name}",
		WithFlags(StringFlag("name", "a name", Optional(),
			ValidateFn(func(v interface{}) error {
				s, ok := v.(string)
				if !ok {
					return fmt.Errorf("validator received non-string value %v", v)
				}
				if s == "bad" {
					return fmt.Errorf("bad name")
				}
				return nil
			}))))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "name=None\n" {
		t.Fatalf("expected 'name=None', got %q", r.Stdout)
	}
	// Passed value still validated.
	r = app.Test([]string{"cmd", "--name", "bad"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1 for invalid value, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--name: bad name") {
		t.Fatalf("expected validator error, got %q", r.Stderr)
	}
}
