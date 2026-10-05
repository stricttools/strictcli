package strictcli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// helper to build a simple app with one command that prints a template
func simpleApp(cmdName, cmdHelp, handlerPrints string, opts ...CmdOption) *App {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command(cmdName, cmdHelp, func(ctx *Context, args map[string]interface{}) Outcome {
		out := handlerPrints
		for k, v := range args {
			out = strings.ReplaceAll(out, "{"+k+"}", formatValue(v))
		}
		// In machine mode the runtime guard refuses a raw stdout write
		// (contract §19.12), so the answer goes through ctx.Out there.
		if ctx.JSON() {
			ctx.Out(out)
		} else {
			ctx.Out(out)
		}
		return Exit(0)
	}, append(opts, WithEffect(EffectReadOnly))...)
	return app
}

// formatValue formats a value the way conformance tests expect
func formatValue(v interface{}) string {
	if v == nil {
		return "None"
	}
	switch val := v.(type) {
	case bool:
		if val {
			return "true"
		}
		return "false"
	case []interface{}:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = fmt.Sprintf("%v", item)
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprintf("%v", val)
	}
}

// --- Basic tests ---

func TestBasicDispatch(t *testing.T) {
	app := simpleApp("greet", "say hello", "hello")
	r := app.Test([]string{"greet"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "hello") {
		t.Fatalf("stdout should contain 'hello', got %q", r.Stdout)
	}
}

func TestBasicUnknownCommand(t *testing.T) {
	app := simpleApp("greet", "say hello", "hello")
	r := app.Test([]string{"deploy"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown command") {
		t.Fatalf("stderr should contain 'unknown command', got %q", r.Stderr)
	}
}

func TestBasicNoArgs(t *testing.T) {
	app := simpleApp("greet", "say hello", "hello")
	r := app.Test([]string{})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "myapp v1.0.0") {
		t.Fatalf("stdout should contain version, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "Commands:") {
		t.Fatalf("stdout should contain 'Commands:', got %q", r.Stdout)
	}
}

func TestVersionFlag(t *testing.T) {
	app := NewApp("myapp", "2.5.0", "test app")
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r := app.Test([]string{"--version"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "myapp 2.5.0") {
		t.Fatalf("stdout should contain 'myapp 2.5.0', got %q", r.Stdout)
	}
}

func TestShortVersionFlag(t *testing.T) {
	app := NewApp("myapp", "2.5.0", "test app")
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r := app.Test([]string{"-v"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "myapp 2.5.0") {
		t.Fatalf("stdout should contain 'myapp 2.5.0', got %q", r.Stdout)
	}
}

func TestHelpFlag(t *testing.T) {
	app := simpleApp("greet", "say hello", "hello")
	r := app.Test([]string{"--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "myapp v1.0.0") {
		t.Fatalf("stdout should contain version, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "test app") {
		t.Fatalf("stdout should contain help text, got %q", r.Stdout)
	}
}

func TestShortHelpFlag(t *testing.T) {
	app := simpleApp("greet", "say hello", "hello")
	r := app.Test([]string{"-h"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "myapp v1.0.0") {
		t.Fatalf("stdout should contain version, got %q", r.Stdout)
	}
}

func TestMultipleCommands(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("start", "start service", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("started")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	app.Command("stop", "stop service", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("stopped")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"stop"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "stopped") {
		t.Fatalf("stdout should contain 'stopped', got %q", r.Stdout)
	}
}

// --- Flag tests ---

func TestStrFlagSpaceSyntax(t *testing.T) {
	app := simpleApp("cmd", "a command", "target={target}",
		WithFlags(StringFlag("target", "the target", Required())))
	r := app.Test([]string{"cmd", "--target", "foo"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "target=foo") {
		t.Fatalf("stdout should contain 'target=foo', got %q", r.Stdout)
	}
}

func TestStrFlagEqualsSyntax(t *testing.T) {
	app := simpleApp("cmd", "a command", "target={target}",
		WithFlags(StringFlag("target", "the target", Required())))
	r := app.Test([]string{"cmd", "--target=bar"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "target=bar") {
		t.Fatalf("stdout should contain 'target=bar', got %q", r.Stdout)
	}
}

func TestBoolFlagPresent(t *testing.T) {
	app := simpleApp("cmd", "a command", "loud={loud}",
		WithFlags(BoolFlag("loud", "be loud", Default(false))))
	r := app.Test([]string{"cmd", "--loud"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "loud=true") {
		t.Fatalf("stdout should contain 'loud=true', got %q", r.Stdout)
	}
}

func TestBoolFlagAbsent(t *testing.T) {
	app := simpleApp("cmd", "a command", "loud={loud}",
		WithFlags(BoolFlag("loud", "be loud", Default(false))))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "loud=false") {
		t.Fatalf("stdout should contain 'loud=false', got %q", r.Stdout)
	}
}

func TestBoolFlagNegation(t *testing.T) {
	app := simpleApp("cmd", "a command", "loud={loud}",
		WithFlags(BoolFlag("loud", "be loud", Default(false))))
	r := app.Test([]string{"cmd", "--no-loud"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "loud=false") {
		t.Fatalf("stdout should contain 'loud=false', got %q", r.Stdout)
	}
}

func TestShortBoolFlag(t *testing.T) {
	app := simpleApp("cmd", "a command", "loud={loud}",
		WithFlags(BoolFlag("loud", "be loud", Short("V"), Default(false))))
	r := app.Test([]string{"cmd", "-V"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "loud=true") {
		t.Fatalf("stdout should contain 'loud=true', got %q", r.Stdout)
	}
}

func TestShortStrFlag(t *testing.T) {
	app := simpleApp("cmd", "a command", "target={target}",
		WithFlags(StringFlag("target", "the target", Short("t"), Required())))
	r := app.Test([]string{"cmd", "-t", "foo"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "target=foo") {
		t.Fatalf("stdout should contain 'target=foo', got %q", r.Stdout)
	}
}

func TestStrFlagDefault(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format", Default("text"))))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "format=text") {
		t.Fatalf("stdout should contain 'format=text', got %q", r.Stdout)
	}
}

func TestStrFlagDefaultOverride(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format", Default("text"))))
	r := app.Test([]string{"cmd", "--format", "json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "format=json") {
		t.Fatalf("stdout should contain 'format=json', got %q", r.Stdout)
	}
}

func TestRequiredStrFlagMissing(t *testing.T) {
	app := simpleApp("cmd", "a command", "target={target}",
		WithFlags(StringFlag("target", "the target", Required())))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "required") {
		t.Fatalf("stderr should contain 'required', got %q", r.Stderr)
	}
}

func TestBoolFlagEqualsSyntaxRejected(t *testing.T) {
	app := simpleApp("cmd", "a command", "loud={loud}",
		WithFlags(BoolFlag("loud", "be loud", Default(false))))
	r := app.Test([]string{"cmd", "--loud=true"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "boolean flag") {
		t.Fatalf("stderr should contain 'boolean flag', got %q", r.Stderr)
	}
}

// --- Arg tests ---

func TestSingleRequiredArg(t *testing.T) {
	app := simpleApp("greet", "say hello", "hello {name}",
		WithArgs(NewArg("name", "who to greet", ArgRequired())))
	r := app.Test([]string{"greet", "world"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "hello world") {
		t.Fatalf("stdout should contain 'hello world', got %q", r.Stdout)
	}
}

func TestMissingRequiredArg(t *testing.T) {
	app := simpleApp("greet", "say hello", "hello {name}",
		WithArgs(NewArg("name", "who to greet", ArgRequired())))
	r := app.Test([]string{"greet"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "missing required argument") {
		t.Fatalf("stderr should contain 'missing required argument', got %q", r.Stderr)
	}
}

func TestTwoPositionalArgs(t *testing.T) {
	app := simpleApp("copy", "copy files", "{src}->{dst}",
		WithArgs(NewArg("src", "source file", ArgRequired()), NewArg("dst", "destination file", ArgRequired())))
	r := app.Test([]string{"copy", "a.txt", "b.txt"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "a.txt->b.txt") {
		t.Fatalf("stdout should contain 'a.txt->b.txt', got %q", r.Stdout)
	}
}

func TestExtraPositionalArgRejected(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok")
	r := app.Test([]string{"cmd", "surprise"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unexpected argument") {
		t.Fatalf("stderr should contain 'unexpected argument', got %q", r.Stderr)
	}
}

func TestOptionalArgWithDefault(t *testing.T) {
	app := simpleApp("cmd", "a command", "path={path}",
		WithArgs(NewArg("path", "project dir", ArgDefault("."))))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "path=.") {
		t.Fatalf("stdout should contain 'path=.', got %q", r.Stdout)
	}
}

func TestOptionalArgProvided(t *testing.T) {
	app := simpleApp("cmd", "a command", "path={path}",
		WithArgs(NewArg("path", "project dir", ArgDefault("."))))
	r := app.Test([]string{"cmd", "/tmp/foo"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "path=/tmp/foo") {
		t.Fatalf("stdout should contain 'path=/tmp/foo', got %q", r.Stdout)
	}
}

func TestOptionalArgNoDefault(t *testing.T) {
	app := simpleApp("cmd", "a command", "path={path}",
		WithArgs(NewArg("path", "project dir", ArgOptional())))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "path=None") {
		t.Fatalf("stdout should contain 'path=None', got %q", r.Stdout)
	}
}

func TestDoubleDashStopsFlagParsing(t *testing.T) {
	app := simpleApp("cmd", "a command", "loud={loud} path={path}",
		WithFlags(BoolFlag("loud", "be loud", Default(false))),
		WithArgs(NewArg("path", "a path", ArgRequired())))
	r := app.Test([]string{"cmd", "--", "--not-a-flag"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "loud=false") {
		t.Fatalf("stdout should contain 'loud=false', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "path=--not-a-flag") {
		t.Fatalf("stdout should contain 'path=--not-a-flag', got %q", r.Stdout)
	}
}

// --- Int type tests ---

func TestIntFlagParses(t *testing.T) {
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the port", Required())))
	r := app.Test([]string{"cmd", "--port", "8080"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=8080") {
		t.Fatalf("stdout should contain 'port=8080', got %q", r.Stdout)
	}
}

func TestIntFlagDefault(t *testing.T) {
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the port", Default(8000))))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=8000") {
		t.Fatalf("stdout should contain 'port=8000', got %q", r.Stdout)
	}
}

func TestIntFlagBadValue(t *testing.T) {
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the port", Required())))
	r := app.Test([]string{"cmd", "--port", "abc"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "expected integer") {
		t.Fatalf("stderr should contain 'expected integer', got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "'abc'") {
		t.Fatalf("stderr should contain 'abc', got %q", r.Stderr)
	}
}

func TestIntFlagEqualsSyntax(t *testing.T) {
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the port", Required())))
	r := app.Test([]string{"cmd", "--port=8080"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=8080") {
		t.Fatalf("stdout should contain 'port=8080', got %q", r.Stdout)
	}
}

func TestIntFlagShort(t *testing.T) {
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the port", Short("p"), Required())))
	r := app.Test([]string{"cmd", "-p", "8080"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=8080") {
		t.Fatalf("stdout should contain 'port=8080', got %q", r.Stdout)
	}
}

func TestIntFlagRequired(t *testing.T) {
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the port", Required())))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "required") {
		t.Fatalf("stderr should contain 'required', got %q", r.Stderr)
	}
}

// --- Env tests ---

func TestEnvStrFlag(t *testing.T) {
	os.Setenv("MYAPP_TARGET", "from-env")
	defer os.Unsetenv("MYAPP_TARGET")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("target=" + formatValue(args["target"]))
		return Exit(0)
	}, WithFlags(StringFlag("target", "the target", Default("fallback"), Env("MYAPP_TARGET"))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "target=from-env") {
		t.Fatalf("stdout should contain 'target=from-env', got %q", r.Stdout)
	}
}

func TestEnvCLIOverrides(t *testing.T) {
	os.Setenv("MYAPP_TARGET", "from-env")
	defer os.Unsetenv("MYAPP_TARGET")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("target=" + formatValue(args["target"]))
		return Exit(0)
	}, WithFlags(StringFlag("target", "the target", Default("fallback"), Env("MYAPP_TARGET"))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd", "--target", "from-cli"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "target=from-cli") {
		t.Fatalf("stdout should contain 'target=from-cli', got %q", r.Stdout)
	}
}

func TestEnvBoolTrue(t *testing.T) {
	for _, val := range []string{"true", "1", "yes"} {
		os.Setenv("MYAPP_VERBOSE", val)
		app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
		app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
			ctx.Out("loud=" + formatValue(args["loud"]))
			return Exit(0)
		}, WithFlags(BoolFlag("loud", "be loud", Env("MYAPP_VERBOSE"), Default(false))), WithEffect(EffectReadOnly))

		r := app.Test([]string{"cmd"})
		if r.ExitCode != 0 {
			t.Fatalf("val=%q: expected exit 0, got %d", val, r.ExitCode)
		}
		if !strings.Contains(r.Stdout, "loud=true") {
			t.Fatalf("val=%q: stdout should contain 'loud=true', got %q", val, r.Stdout)
		}
		os.Unsetenv("MYAPP_VERBOSE")
	}
}

func TestEnvBoolFalse(t *testing.T) {
	for _, val := range []string{"false", "0", "no"} {
		os.Setenv("MYAPP_VERBOSE", val)
		app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
		app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
			ctx.Out("loud=" + formatValue(args["loud"]))
			return Exit(0)
		}, WithFlags(BoolFlag("loud", "be loud", Env("MYAPP_VERBOSE"), Default(false))), WithEffect(EffectReadOnly))

		r := app.Test([]string{"cmd"})
		if r.ExitCode != 0 {
			t.Fatalf("val=%q: expected exit 0, got %d", val, r.ExitCode)
		}
		if !strings.Contains(r.Stdout, "loud=false") {
			t.Fatalf("val=%q: stdout should contain 'loud=false', got %q", val, r.Stdout)
		}
		os.Unsetenv("MYAPP_VERBOSE")
	}
}

func TestEnvBoolInvalid(t *testing.T) {
	os.Setenv("MYAPP_VERBOSE", "maybe")
	defer os.Unsetenv("MYAPP_VERBOSE")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(BoolFlag("loud", "be loud", Env("MYAPP_VERBOSE"), Default(false))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "invalid boolean value") {
		t.Fatalf("stderr should contain 'invalid boolean value', got %q", r.Stderr)
	}
}

func TestEnvIntFlag(t *testing.T) {
	os.Setenv("MYAPP_PORT", "9090")
	defer os.Unsetenv("MYAPP_PORT")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("port=" + formatValue(args["port"]))
		return Exit(0)
	}, WithFlags(IntFlag("port", "the port", Default(80), Env("MYAPP_PORT"))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=9090") {
		t.Fatalf("stdout should contain 'port=9090', got %q", r.Stdout)
	}
}

func TestEnvIntBadValue(t *testing.T) {
	os.Setenv("MYAPP_PORT", "abc")
	defer os.Unsetenv("MYAPP_PORT")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(IntFlag("port", "the port", Default(80), Env("MYAPP_PORT"))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "expected integer") {
		t.Fatalf("stderr should contain 'expected integer', got %q", r.Stderr)
	}
}

// --- Choices tests ---

func TestChoicesValidStr(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format", Choices(Ch("text", ""), Ch("json", "")), Required())))
	r := app.Test([]string{"cmd", "--format", "json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "format=json") {
		t.Fatalf("stdout should contain 'format=json', got %q", r.Stdout)
	}
}

func TestChoicesInvalidStr(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format", Choices(Ch("text", ""), Ch("json", "")), Required())))
	r := app.Test([]string{"cmd", "--format", "xml"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "invalid value") {
		t.Fatalf("stderr should contain 'invalid value', got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "'xml'") {
		t.Fatalf("stderr should contain 'xml', got %q", r.Stderr)
	}
}

func TestChoicesValidInt(t *testing.T) {
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the port", Choices(Ch(80, ""), Ch(443, ""), Ch(8080, "")), Required())))
	r := app.Test([]string{"cmd", "--port", "443"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=443") {
		t.Fatalf("stdout should contain 'port=443', got %q", r.Stdout)
	}
}

func TestChoicesInvalidInt(t *testing.T) {
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the port", Choices(Ch(80, ""), Ch(443, ""), Ch(8080, "")), Required())))
	r := app.Test([]string{"cmd", "--port", "9090"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "invalid value") {
		t.Fatalf("stderr should contain 'invalid value', got %q", r.Stderr)
	}
}

func TestChoicesInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("format", "output format", Default("text"), Choices(Ch("text", ""), Ch("json", "")))))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "choices: text, json") {
		t.Fatalf("stdout should contain 'choices: text, json', got %q", r.Stdout)
	}
}

// --- Error tests ---

func TestUnknownFlag(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(BoolFlag("loud", "be loud", Default(false))))
	r := app.Test([]string{"cmd", "--unknown"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown flag") {
		t.Fatalf("stderr should contain 'unknown flag', got %q", r.Stderr)
	}
}

func TestUnknownShortFlag(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok")
	r := app.Test([]string{"cmd", "-x"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	// Tokens starting with single "-" that don't match any known flag are
	// treated as positional args (to support negative numbers like -7).
	// A command with no args then errors with "unexpected argument".
	if !strings.Contains(r.Stderr, "unexpected argument") {
		t.Fatalf("stderr should contain 'unexpected argument', got %q", r.Stderr)
	}
}

func TestFlagRequiresValue(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("target", "the target", Required())))
	r := app.Test([]string{"cmd", "--target"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "requires a value") {
		t.Fatalf("stderr should contain 'requires a value', got %q", r.Stderr)
	}
}

func TestErrorIncludesTryHint(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok")
	r := app.Test([]string{"unknown"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown command") {
		t.Fatalf("stderr should contain 'unknown command', got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "try 'myapp --help'") {
		t.Fatalf("stderr should contain try hint, got %q", r.Stderr)
	}
}

func TestErrorTryHintIncludesCommandPrefix(t *testing.T) {
	// Parse error on a top-level command should show "try 'myapp cmd --help'"
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("target", "the target", Required())))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "try 'myapp cmd --help'") {
		t.Fatalf("stderr should contain 'try 'myapp cmd --help'', got %q", r.Stderr)
	}
}

func TestErrorTryHintIncludesGroupCommandPrefix(t *testing.T) {
	// Parse error on a group command should show "try 'myapp config set --help'"
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("config", "manage configuration")
	g.Command("set", "set a config value", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("key", "config key", Required())), WithEffect(EffectReadOnly))
	r := app.Test([]string{"config", "set"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "try 'myapp config set --help'") {
		t.Fatalf("stderr should contain 'try 'myapp config set --help'', got %q", r.Stderr)
	}
}

func TestErrorTryHintUnknownCommandInGroup(t *testing.T) {
	// Unknown command within a group should show "try 'myapp config --help'"
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("config", "manage configuration")
	g.Command("show", "display config", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r := app.Test([]string{"config", "delete"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "try 'myapp config --help'") {
		t.Fatalf("stderr should contain 'try 'myapp config --help'', got %q", r.Stderr)
	}
}

func TestBoolNegationWithValueRejected(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(BoolFlag("loud", "be loud", Default(false))))
	r := app.Test([]string{"cmd", "--no-loud=true"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "boolean negation") {
		t.Fatalf("stderr should contain 'boolean negation', got %q", r.Stderr)
	}
}

// --- Repeatable tests ---

func TestRepeatableSingle(t *testing.T) {
	app := simpleApp("cmd", "a command", "tags={tag}",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--tag", "alpha"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=alpha") {
		t.Fatalf("stdout should contain 'tags=alpha', got %q", r.Stdout)
	}
}

func TestRepeatableMultiple(t *testing.T) {
	app := simpleApp("cmd", "a command", "tags={tag}",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--tag", "alpha", "--tag", "beta", "--tag", "gamma"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=alpha,beta,gamma") {
		t.Fatalf("stdout should contain 'tags=alpha,beta,gamma', got %q", r.Stdout)
	}
}

func TestRepeatableZero(t *testing.T) {
	app := simpleApp("cmd", "a command", "tags={tag}",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{}))))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=") {
		t.Fatalf("stdout should contain 'tags=', got %q", r.Stdout)
	}
}

func TestRepeatableInt(t *testing.T) {
	app := simpleApp("cmd", "a command", "ports={port}",
		WithFlags(IntFlag("port", "a port", Repeatable(), Unique(false), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--port", "80", "--port", "443"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "ports=80,443") {
		t.Fatalf("stdout should contain 'ports=80,443', got %q", r.Stdout)
	}
}

func TestRepeatableWithChoicesValid(t *testing.T) {
	app := simpleApp("cmd", "a command", "tags={tag}",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Choices(Ch("alpha", ""), Ch("beta", ""), Ch("gamma", "")), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--tag", "alpha", "--tag", "gamma"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=alpha,gamma") {
		t.Fatalf("stdout should contain 'tags=alpha,gamma', got %q", r.Stdout)
	}
}

func TestRepeatableWithChoicesInvalid(t *testing.T) {
	app := simpleApp("cmd", "a command", "tags={tag}",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Choices(Ch("alpha", ""), Ch("beta", "")), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--tag", "alpha", "--tag", "delta"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "invalid value") {
		t.Fatalf("stderr should contain 'invalid value', got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "'delta'") {
		t.Fatalf("stderr should contain 'delta', got %q", r.Stderr)
	}
}

func TestRepeatableEquals(t *testing.T) {
	app := simpleApp("cmd", "a command", "tags={tag}",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--tag=alpha", "--tag=beta"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=alpha,beta") {
		t.Fatalf("stdout should contain 'tags=alpha,beta', got %q", r.Stdout)
	}
}

func TestRepeatableShortFlag(t *testing.T) {
	app := simpleApp("cmd", "a command", "tags={tag}",
		WithFlags(StringFlag("tag", "a tag", Short("t"), Repeatable(), Unique(false), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "-t", "alpha", "-t", "beta"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=alpha,beta") {
		t.Fatalf("stdout should contain 'tags=alpha,beta', got %q", r.Stdout)
	}
}

func TestRepeatableInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "repeatable") {
		t.Fatalf("stdout should contain 'repeatable', got %q", r.Stdout)
	}
}

func TestRepeatableEnv(t *testing.T) {
	os.Setenv("MYAPP_TAG", "fromenv")
	defer os.Unsetenv("MYAPP_TAG")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Env("MYAPP_TAG"), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=fromenv") {
		t.Fatalf("stdout should contain 'tags=fromenv', got %q", r.Stdout)
	}
}

// The mutex tests that stood here are DELETED with MutexGroup (contract §21's
// supersession box). Their semantics -- election, exclusivity, the required
// error, and the help section -- live in member_election_test.go and
// selector_test.go, restated over the member-spelled selector that replaced the
// construct, with §21.4's sentences asserted byte-for-byte.

// --- Nesting (Group) tests ---

func TestGroupDispatch(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("config", "manage configuration")
	g.Command("show", "display config", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("showing config")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"config", "show"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "showing config") {
		t.Fatalf("stdout should contain 'showing config', got %q", r.Stdout)
	}
}

func TestGroupCommandWithFlags(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("config", "manage configuration")
	g.Command("set", "set a config value", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("%s=%s", args["key"], args["value"]))
		return Exit(0)
	}, WithFlags(
		StringFlag("key", "config key", Required()),
		StringFlag("value", "config value", Required()),
	), WithEffect(EffectReadOnly))
	r := app.Test([]string{"config", "set", "--key", "name", "--value", "strictcli"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "name=strictcli") {
		t.Fatalf("stdout should contain 'name=strictcli', got %q", r.Stdout)
	}
}

func TestGroupUnknownSubcommand(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("config", "manage configuration")
	g.Command("show", "display config", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r := app.Test([]string{"config", "delete"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown command") {
		t.Fatalf("stderr should contain 'unknown command', got %q", r.Stderr)
	}
}

func TestGroupHelp(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("config", "manage configuration")
	g.Command("show", "display config", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	g.Command("set", "set a config value", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r := app.Test([]string{"config", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "show") || !strings.Contains(r.Stdout, "set") {
		t.Fatalf("stdout should list subcommands, got %q", r.Stdout)
	}
}

// makeGroupHelpApp builds an app with one group holding a normal command and a
// passthrough command, used by the group-level help routing tests below.
func makeGroupHelpApp() *App {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("db", "database commands")
	g.Command("migrate", "run migrations",
		func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithEffect(EffectReadOnly))
	g.Command("shell", "raw shell",
		nil,
		WithPassthrough(func(ctx *Context, name string, args []string, globals map[string]interface{}) int {
			ctx.Out(name + ":" + strings.Join(args, ","))
			return 0
		}),
		WithEffect(EffectReadOnly))
	return app
}

// A --help/-h that LEADS the tokens remaining after a group name requests that
// group's help no matter what follows it. Python and TypeScript both route on
// the leading token alone; Go additionally required it to be the only token
// left, so `myapp db --help ./m` reported an unknown command instead. The
// three argv rows below are the differential fuzzer's reproducers.
func TestGroupHelpWithTrailingTokens(t *testing.T) {
	for _, argv := range [][]string{
		{"db", "-h", "./m"},
		{"db", "--help", "./m"},
		{"db", "--help", "--"},
		{"db", "--help", "migrate"},
		{"db", "--help", "nope"},
		{"db", "-h", "-h"},
	} {
		r := makeGroupHelpApp().Test(argv)
		if r.ExitCode != 0 {
			t.Fatalf("%v: expected exit 0, got %d; stderr=%q", argv, r.ExitCode, r.Stderr)
		}
		if !strings.Contains(r.Stdout, "myapp db -- database commands") {
			t.Fatalf("%v: expected group help, got %q", argv, r.Stdout)
		}
		if !strings.Contains(r.Stdout, "migrate") {
			t.Fatalf("%v: group help should list subcommands, got %q", argv, r.Stdout)
		}
	}
}

// The two boundaries the help rule declares stay boundaries at group level: a
// bare `--` after the group name is data (so the token after it is not help),
// and a passthrough command's own args are forwarded untouched. Verified to
// match Python and TypeScript byte-for-byte.
func TestGroupHelpBoundaries(t *testing.T) {
	// A bare `--` right after the group name is not help and is not a command.
	r := makeGroupHelpApp().Test([]string{"db", "--", "--help"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d; stdout=%q", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, "unknown command '--' in 'db'") {
		t.Fatalf("stderr = %q", r.Stderr)
	}

	// An unknown token before --help is still an unknown command.
	r = makeGroupHelpApp().Test([]string{"db", "nope", "--help"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d; stdout=%q", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, "unknown command 'nope' in 'db'") {
		t.Fatalf("stderr = %q", r.Stderr)
	}

	// A passthrough command inside the group: --help before the boundary is
	// the command's help, and everything after a bare `--` is forwarded.
	r = makeGroupHelpApp().Test([]string{"db", "shell", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("passthrough help: expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "myapp db shell -- raw shell") {
		t.Fatalf("passthrough help = %q", r.Stdout)
	}
	r = makeGroupHelpApp().Test([]string{"db", "shell", "--", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("passthrough forwarding: expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "shell:--,--help") {
		t.Fatalf("passthrough args = %q", r.Stdout)
	}
}

func TestGroupNoSubcommandShowsHelp(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("config", "manage configuration")
	g.Command("show", "display config", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r := app.Test([]string{"config"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "manage configuration") {
		t.Fatalf("stdout should contain group help, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "show") {
		t.Fatalf("stdout should list subcommands, got %q", r.Stdout)
	}
}

func TestGroupCommandHelpShowsPrefix(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("config", "manage configuration")
	g.Command("set", "set a config value", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("key", "config key", Required()), StringFlag("value", "config value", Required())), WithEffect(EffectReadOnly))
	r := app.Test([]string{"config", "set", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "config set") {
		t.Fatalf("stdout should contain 'config set', got %q", r.Stdout)
	}
}

func TestGroupUseHint(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("config", "manage configuration")
	g.Command("show", "display config", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r := app.Test([]string{"config", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "Use 'myapp help config <command>' for more information.") {
		t.Fatalf("stdout should contain use hint, got %q", r.Stdout)
	}
}

func TestAppHelpShowsGroups(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("config", "manage configuration")
	g.Command("show", "display config", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r := app.Test([]string{})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "Groups:") {
		t.Fatalf("stdout should contain 'Groups:', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "config") {
		t.Fatalf("stdout should contain 'config', got %q", r.Stdout)
	}
}

// --- FlagSet tests ---

func TestFlagSetSingleFlag(t *testing.T) {
	app := simpleApp("cmd", "a command", "loud={loud}",
		WithFlagSets(FlagSet{
			Name:  "loud",
			Flags: []Flag{BoolFlag("loud", "loud output", Default(false))},
		}))
	r := app.Test([]string{"cmd", "--loud"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "loud=true") {
		t.Fatalf("stdout should contain 'loud=true', got %q", r.Stdout)
	}
}

func TestFlagSetMultipleFlags(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format} color={color}",
		WithFlagSets(FlagSet{
			Name: "output",
			Flags: []Flag{
				StringFlag("format", "output format", Default("text")),
				BoolFlag("color", "use color", Default(false)),
			},
		}))
	r := app.Test([]string{"cmd", "--format", "json", "--color"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "format=json") {
		t.Fatalf("stdout should contain 'format=json', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "color=true") {
		t.Fatalf("stdout should contain 'color=true', got %q", r.Stdout)
	}
}

func TestFlagSetFlagsWithDefaults(t *testing.T) {
	app := simpleApp("deploy", "deploy the app", "token={token} insecure={insecure}",
		WithFlagSets(FlagSet{
			Name: "auth",
			Flags: []Flag{
				StringFlag("token", "auth token", Default("none")),
				BoolFlag("insecure", "skip TLS verification", Default(false)),
			},
		}))
	r := app.Test([]string{"deploy"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "token=none") {
		t.Fatalf("stdout should contain 'token=none', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "insecure=false") {
		t.Fatalf("stdout should contain 'insecure=false', got %q", r.Stdout)
	}
}

func TestFlagSetFlagsInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlagSets(FlagSet{
			Name:  "debug",
			Flags: []Flag{BoolFlag("debug", "enable debug mode", Default(false))},
		}))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "--debug") {
		t.Fatalf("stdout should contain '--debug', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "enable debug mode") {
		t.Fatalf("stdout should contain 'enable debug mode', got %q", r.Stdout)
	}
}

// --- Help format tests ---

func TestHelpShowsVersionAndCommands(t *testing.T) {
	app := NewApp("myapp", "3.0.0", "my cool app")
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	app.Command("test", "run tests", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r := app.Test([]string{})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	for _, want := range []string{"myapp v3.0.0", "my cool app", "Commands:", "run", "test"} {
		if !strings.Contains(r.Stdout, want) {
			t.Fatalf("stdout should contain %q, got %q", want, r.Stdout)
		}
	}
}

func TestCommandHelpShowsFlagsAndArgs(t *testing.T) {
	app := simpleApp("deploy", "deploy the app", "{target}:{sim_run}",
		WithArgs(NewArg("target", "deploy target", ArgRequired())),
		WithFlags(BoolFlag("sim-run", "preview changes", Default(false))))
	r := app.Test([]string{"deploy", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	for _, want := range []string{"--sim-run", "--no-sim-run", "target", "deploy the app"} {
		if !strings.Contains(r.Stdout, want) {
			t.Fatalf("stdout should contain %q, got %q", want, r.Stdout)
		}
	}
}

func TestStrFlagShowsTypeInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("output", "output path", Default("out.txt"))))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "<str>") {
		t.Fatalf("stdout should contain '<str>', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "default: out.txt") {
		t.Fatalf("stdout should contain 'default: out.txt', got %q", r.Stdout)
	}
}

func TestIntShowsTypeInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(IntFlag("port", "the port", Default(8000))))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "<int>") {
		t.Fatalf("stdout should contain '<int>', got %q", r.Stdout)
	}
}

func TestRequiredFlagInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("target", "the target", Required())))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "required") {
		t.Fatalf("stdout should contain 'required', got %q", r.Stdout)
	}
}

func TestOptionalArgDefaultInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("path", "project dir", ArgDefault("."))))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "[default: .]") {
		t.Fatalf("stdout should contain '[default: .]', got %q", r.Stdout)
	}
}

func TestOptionalArgNoDefaultInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("path", "project dir", ArgOptional())))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "[optional]") {
		t.Fatalf("stdout should contain '[optional]', got %q", r.Stdout)
	}
}

func TestUseHintInAppHelp(t *testing.T) {
	app := simpleApp("run", "run something", "ok")
	r := app.Test([]string{"--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "Use 'myapp help <command>' for more information.") {
		t.Fatalf("stdout should contain use hint, got %q", r.Stdout)
	}
}

func TestEnvInHelp(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("target", "the target", Default("x"), Env("MYAPP_TARGET"))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "env: MYAPP_TARGET") {
		t.Fatalf("stdout should contain 'env: MYAPP_TARGET', got %q", r.Stdout)
	}
}

func TestPrefixedFalseEnvVar(t *testing.T) {
	os.Setenv("SPECIAL", "works")
	defer os.Unsetenv("SPECIAL")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("target=" + formatValue(args["target"]))
		return Exit(0)
	}, WithFlags(StringFlag("target", "the target", Default("fallback"), Env("SPECIAL"), Prefixed(false))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "target=works") {
		t.Fatalf("stdout should contain 'target=works', got %q", r.Stdout)
	}
}

func TestEnvChoicesValid(t *testing.T) {
	os.Setenv("MYAPP_FORMAT", "json")
	defer os.Unsetenv("MYAPP_FORMAT")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("format=" + formatValue(args["format"]))
		return Exit(0)
	}, WithFlags(StringFlag("format", "output format", Default("text"), Env("MYAPP_FORMAT"), Choices(Ch("text", ""), Ch("json", "")))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "format=json") {
		t.Fatalf("stdout should contain 'format=json', got %q", r.Stdout)
	}
}

func TestGroupCommandGlobalFlagCollisionPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for global flag collision in group command, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "collides with a global flag") || !strings.Contains(msg, "loud") {
			t.Fatalf("panic message should mention flag 'loud' collides with global, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(BoolFlag("loud", "global verbosity", Default(false)))
	g := app.Group("config", "manage configuration")
	// This should panic: "loud" collides with the global flag
	g.Command("show", "display config", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(BoolFlag("loud", "local verbosity", Default(false))), WithEffect(EffectReadOnly))
}

func TestEnvChoicesInvalid(t *testing.T) {
	os.Setenv("MYAPP_FORMAT", "xml")
	defer os.Unsetenv("MYAPP_FORMAT")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("format", "output format", Default("text"), Env("MYAPP_FORMAT"), Choices(Ch("text", ""), Ch("json", "")))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "invalid value") {
		t.Fatalf("stderr should contain 'invalid value', got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "'xml'") {
		t.Fatalf("stderr should contain 'xml', got %q", r.Stderr)
	}
}

// --- all-or-none tests (contract §26) ---

func TestAllOrNoneBothProvided(t *testing.T) {
	app := simpleApp("cmd", "a command", "user={user} pass={pass}",
		WithFlags(
			StringFlag("user", "username", Default("none")),
			StringFlag("pass", "password", Default("none")),
		),
		WithConstraints(AllOrNone("credentials", Member("user"), Member("pass"))))
	r := app.Test([]string{"cmd", "--user", "admin", "--pass", "secret"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "user=admin") {
		t.Fatalf("stdout should contain 'user=admin', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "pass=secret") {
		t.Fatalf("stdout should contain 'pass=secret', got %q", r.Stdout)
	}
}

func TestAllOrNoneNeitherProvided(t *testing.T) {
	app := simpleApp("cmd", "a command", "user={user} pass={pass}",
		WithFlags(
			StringFlag("user", "username", Default("none")),
			StringFlag("pass", "password", Default("none")),
		),
		WithConstraints(AllOrNone("credentials", Member("user"), Member("pass"))))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "user=none") {
		t.Fatalf("stdout should contain 'user=none', got %q", r.Stdout)
	}
}

func TestAllOrNoneOneProvidedError(t *testing.T) {
	app := simpleApp("cmd", "a command", "user={user} pass={pass}",
		WithFlags(
			StringFlag("user", "username", Default("none")),
			StringFlag("pass", "password", Default("none")),
		),
		WithConstraints(AllOrNone("credentials", Member("user"), Member("pass"))))
	r := app.Test([]string{"cmd", "--user", "admin"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "must be used together") {
		t.Fatalf("stderr should contain 'must be used together', got %q", r.Stderr)
	}
}

// --- Requires tests ---

func TestRequiresFlagWithDependsOn(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format} output={output}",
		WithFlags(
			StringFlag("format", "output format", Default("text")),
			StringFlag("output", "output file", Default("out.txt")),
		),
		WithConstraints(Requires("output-needs-format", "output", "format")))
	r := app.Test([]string{"cmd", "--output", "file.txt", "--format", "json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "output=file.txt") {
		t.Fatalf("stdout should contain 'output=file.txt', got %q", r.Stdout)
	}
}

func TestRequiresFlagNotProvided(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format} output={output}",
		WithFlags(
			StringFlag("format", "output format", Default("text")),
			StringFlag("output", "output file", Default("out.txt")),
		),
		WithConstraints(Requires("output-needs-format", "output", "format")))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestRequiresDependsOnWithoutFlag(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format} output={output}",
		WithFlags(
			StringFlag("format", "output format", Default("text")),
			StringFlag("output", "output file", Default("out.txt")),
		),
		WithConstraints(Requires("output-needs-format", "output", "format")))
	// Only --format provided (DependsOn), not --output (Flag) -- should be ok (unidirectional)
	r := app.Test([]string{"cmd", "--format", "json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestRequiresFlagWithoutDependsOnError(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format} output={output}",
		WithFlags(
			StringFlag("format", "output format", Default("text")),
			StringFlag("output", "output file", Default("out.txt")),
		),
		WithConstraints(Requires("output-needs-format", "output", "format")))
	r := app.Test([]string{"cmd", "--output", "file.txt"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "requires") {
		t.Fatalf("stderr should contain 'requires', got %q", r.Stderr)
	}
}

// --- Dependency registration panic tests ---

func TestAllOrNoneUnknownMemberPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for an all-or-none naming an unknown member, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `constraint "credentials" references unknown member "nonexistent"`) {
			t.Fatalf("panic message should mention the unknown member, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("user", "username", Default("none"))),
		WithConstraints(AllOrNone("credentials", Member("user"), Member("nonexistent"))), WithEffect(EffectReadOnly))
}

func TestRequiresUnknownFlagPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for Requires with unknown flag, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `constraint "user-needs-missing" references unknown flag "nonexistent"`) {
			t.Fatalf("panic message should mention unknown flag, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("user", "username", Default("none"))),
		WithConstraints(Requires("user-needs-missing", "user", "nonexistent")), WithEffect(EffectReadOnly))
}

func TestRequiresSelfDependencyPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for Requires with self-dependency, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "cannot be the same") {
			t.Fatalf("panic message should mention self-dependency, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("user", "username", Default("none"))),
		WithConstraints(Requires("self", "user", "user")), WithEffect(EffectReadOnly))
}

func TestAllOrNoneDuplicateMemberPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for an all-or-none with a duplicate member, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `constraint "credentials" declares member "user" twice`) {
			t.Fatalf("panic message should mention the duplicate member, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(
			StringFlag("user", "username", Default("none")),
			StringFlag("pass", "password", Default("none")),
		),
		WithConstraints(AllOrNone("credentials", Member("user"), Member("user"))), WithEffect(EffectReadOnly))
}

// --- Implies tests ---

func TestImpliesTriggerSetsTarget(t *testing.T) {
	app := simpleApp("cmd", "a command", "fast={fast} embeddings={embeddings}",
		WithFlags(
			BoolFlag("fast", "enable fast mode", Default(false)),
			BoolFlag("embeddings", "enable embeddings", Default(false)),
		),
		WithConstraints(Implies("fast-declines-embeddings", "fast", "embeddings", false)))
	r := app.Test([]string{"cmd", "--fast"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "fast=true") {
		t.Fatalf("stdout should contain 'fast=true', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "embeddings=false") {
		t.Fatalf("stdout should contain 'embeddings=false', got %q", r.Stdout)
	}
}

func TestImpliesTriggerNotSetTargetGetsDefault(t *testing.T) {
	app := simpleApp("cmd", "a command", "fast={fast} embeddings={embeddings}",
		WithFlags(
			BoolFlag("fast", "enable fast mode", Default(false)),
			BoolFlag("embeddings", "enable embeddings", Default(false)),
		),
		WithConstraints(Implies("fast-declines-embeddings", "fast", "embeddings", false)))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "fast=false") {
		t.Fatalf("stdout should contain 'fast=false', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "embeddings=false") {
		t.Fatalf("stdout should contain 'embeddings=false', got %q", r.Stdout)
	}
}

func TestImpliesExplicitConflictError(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			BoolFlag("fast", "enable fast mode", Default(false)),
			BoolFlag("embeddings", "enable embeddings", Default(false)),
		),
		WithConstraints(Implies("fast-declines-embeddings", "fast", "embeddings", false)))
	// --fast implies embeddings=false, but user explicitly sets --embeddings (true)
	r := app.Test([]string{"cmd", "--fast", "--embeddings"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "implies") {
		t.Fatalf("stderr should contain 'implies', got %q", r.Stderr)
	}
}

func TestImpliesExplicitAgreementNoError(t *testing.T) {
	app := simpleApp("cmd", "a command", "fast={fast} embeddings={embeddings}",
		WithFlags(
			BoolFlag("fast", "enable fast mode", Default(false)),
			BoolFlag("embeddings", "enable embeddings", Default(false)),
		),
		WithConstraints(Implies("fast-declines-embeddings", "fast", "embeddings", false)))
	// --fast implies embeddings=false, and user explicitly sets --no-embeddings (false) -- agreement
	r := app.Test([]string{"cmd", "--fast", "--no-embeddings"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "fast=true") {
		t.Fatalf("stdout should contain 'fast=true', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "embeddings=false") {
		t.Fatalf("stdout should contain 'embeddings=false', got %q", r.Stdout)
	}
}

func TestImpliesUnknownTriggerFlagPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for Implies with unknown trigger flag, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `constraint "bad" references unknown flag "nonexistent"`) {
			t.Fatalf("panic message should mention unknown trigger flag, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(BoolFlag("embeddings", "enable embeddings", Default(false))),
		WithConstraints(Implies("bad", "nonexistent", "embeddings", false)), WithEffect(EffectReadOnly))
}

func TestImpliesUnknownTargetFlagPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for Implies with unknown target flag, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `constraint "bad" references unknown flag "nonexistent"`) {
			t.Fatalf("panic message should mention unknown target flag, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(BoolFlag("fast", "enable fast mode", Default(false))),
		WithConstraints(Implies("bad", "fast", "nonexistent", false)), WithEffect(EffectReadOnly))
}

func TestImpliesSelfImplicationPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for Implies with self-implication, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "cannot be the same") {
			t.Fatalf("panic message should mention self-implication, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(BoolFlag("fast", "enable fast mode", Default(false))),
		WithConstraints(Implies("bad", "fast", "fast", false)), WithEffect(EffectReadOnly))
}

func TestImpliesTriggerNotBoolFlagPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for Implies with non-bool trigger flag, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "must be a bool flag") || !strings.Contains(msg, "trigger") {
			t.Fatalf("panic message should mention trigger must be bool, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(
			StringFlag("mode", "the mode", Default("fast")),
			BoolFlag("embeddings", "enable embeddings", Default(false)),
		),
		WithConstraints(Implies("bad", "mode", "embeddings", false)), WithEffect(EffectReadOnly))
}

func TestImpliesTargetNotBoolFlagPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for Implies with non-bool target flag, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "must be a bool flag") || !strings.Contains(msg, "target") {
			t.Fatalf("panic message should mention target must be bool, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(
			BoolFlag("fast", "enable fast mode", Default(false)),
			StringFlag("output", "output format", Default("text")),
		),
		WithConstraints(Implies("bad", "fast", "output", false)), WithEffect(EffectReadOnly))
}

// --- Deprecated command tests ---

func TestDeprecatedCommandExitsWithError(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	app.Deprecated("deploy", "use 'run' instead")
	r := app.Test([]string{"deploy"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "deprecated") {
		t.Fatalf("stderr should contain 'deprecated', got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "use 'run' instead") {
		t.Fatalf("stderr should contain deprecation message, got %q", r.Stderr)
	}
}

func TestDeprecatedCommandInAppHelp(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	app.Deprecated("deploy", "use 'run' instead")
	r := app.Test([]string{"--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "Deprecated:") {
		t.Fatalf("stdout should contain 'Deprecated:', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "deploy") {
		t.Fatalf("stdout should contain 'deploy', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "use 'run' instead") {
		t.Fatalf("stdout should contain deprecation message, got %q", r.Stdout)
	}
}

func TestDeprecatedSubcommandInGroupHelp(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("config", "manage configuration")
	g.Command("show", "display config", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	g.Deprecated("dump", "use 'show' instead")
	r := app.Test([]string{"config", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "Deprecated:") {
		t.Fatalf("stdout should contain 'Deprecated:', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "dump") {
		t.Fatalf("stdout should contain 'dump', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "use 'show' instead") {
		t.Fatalf("stdout should contain deprecation message, got %q", r.Stdout)
	}
}

func TestDeprecatedSubcommandExitsWithError(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("config", "manage configuration")
	g.Command("show", "display config", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	g.Deprecated("dump", "use 'show' instead")
	r := app.Test([]string{"config", "dump"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "deprecated") {
		t.Fatalf("stderr should contain 'deprecated', got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "use 'show' instead") {
		t.Fatalf("stderr should contain deprecation message, got %q", r.Stderr)
	}
}

func TestNormalAndDeprecatedCoexist(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("running")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	app.Deprecated("deploy", "use 'run' instead")

	// Normal command still works
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "running") {
		t.Fatalf("stdout should contain 'running', got %q", r.Stdout)
	}

	// Deprecated command errors
	r = app.Test([]string{"deploy"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "deprecated") {
		t.Fatalf("stderr should contain 'deprecated', got %q", r.Stderr)
	}
}

func TestDeprecatedDuplicateNameWithCommandPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for deprecated command with duplicate name, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "collides with an existing command") {
			t.Fatalf("panic message should mention name collision, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	app.Deprecated("run", "this should panic")
}

func TestDeprecatedEmptyMessagePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for deprecated command with empty message, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "message must not be empty") {
			t.Fatalf("panic message should mention empty message, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Deprecated("deploy", "")
}

func TestImpliesEnvTrigger(t *testing.T) {
	os.Setenv("MYAPP_FAST", "true")
	defer os.Unsetenv("MYAPP_FAST")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("fast=" + formatValue(args["fast"]) + " embeddings=" + formatValue(args["embeddings"]))
		return Exit(0)
	}, WithFlags(
		BoolFlag("fast", "enable fast mode", Env("MYAPP_FAST"), Default(false)),
		BoolFlag("embeddings", "enable embeddings", Default(false)),
	), WithConstraints(Implies("fast-declines-embeddings", "fast", "embeddings", false)), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "fast=true") {
		t.Fatalf("stdout should contain 'fast=true', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "embeddings=false") {
		t.Fatalf("stdout should contain 'embeddings=false', got %q", r.Stdout)
	}
}

func TestAppCommands(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	handler := func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }
	app.Command("build", "build the project", handler, WithEffect(EffectReadOnly))
	app.Command("test", "run tests", handler, WithEffect(EffectReadOnly))

	cmds := app.Commands()
	if len(cmds) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(cmds))
	}
	if cmds["build"] == nil {
		t.Fatal("expected 'build' command to be present")
	}
	if cmds["test"] == nil {
		t.Fatal("expected 'test' command to be present")
	}
	if cmds["build"].Help != "build the project" {
		t.Fatalf("expected build help 'build the project', got %q", cmds["build"].Help)
	}
	if cmds["test"].Help != "run tests" {
		t.Fatalf("expected test help 'run tests', got %q", cmds["test"].Help)
	}
}

func TestAppGroups(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	handler := func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }

	grp := app.Group("config", "manage configuration")
	grp.Command("set", "set a value", handler, WithEffect(EffectReadOnly))
	grp.Command("get", "get a value", handler, WithEffect(EffectReadOnly))

	groups := app.Groups()
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	g := groups["config"]
	if g == nil {
		t.Fatal("expected 'config' group to be present")
	}
	if g.Help != "manage configuration" {
		t.Fatalf("expected group help 'manage configuration', got %q", g.Help)
	}
	if len(g.Commands) != 2 {
		t.Fatalf("expected 2 subcommands, got %d", len(g.Commands))
	}
	if g.Commands["set"] == nil {
		t.Fatal("expected 'set' subcommand to be present")
	}
	if g.Commands["get"] == nil {
		t.Fatal("expected 'get' subcommand to be present")
	}
}

func TestAppGlobalFlags(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(BoolFlag("loud", "enable loud output", Default(false)))
	app.GlobalFlag(StringFlag("output", "output format", Default("json")))

	flags := app.GlobalFlags()
	if len(flags) != 2 {
		t.Fatalf("expected 2 global flags, got %d", len(flags))
	}
	if flags[0].Name != "loud" {
		t.Fatalf("expected first flag name 'loud', got %q", flags[0].Name)
	}
	if flags[0].Type != TypeBool {
		t.Fatalf("expected first flag type TypeBool, got %v", flags[0].Type)
	}
	if flags[1].Name != "output" {
		t.Fatalf("expected second flag name 'output', got %q", flags[1].Name)
	}
	if flags[1].Type != TypeStr {
		t.Fatalf("expected second flag type TypeStr, got %v", flags[1].Type)
	}
}

func TestAppDeprecated(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Deprecated("deploy", "use 'release' instead")
	app.Deprecated("init", "use 'setup' instead")

	deprecated := app.DeprecatedCommands()
	if len(deprecated) != 2 {
		t.Fatalf("expected 2 deprecated commands, got %d", len(deprecated))
	}
	if deprecated["deploy"] != "use 'release' instead" {
		t.Fatalf("expected deploy message 'use 'release' instead', got %q", deprecated["deploy"])
	}
	if deprecated["init"] != "use 'setup' instead" {
		t.Fatalf("expected init message 'use 'setup' instead', got %q", deprecated["init"])
	}

	// Also test Group.DeprecatedCommands
	handler := func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }
	app.Command("run", "run something", handler, WithEffect(EffectReadOnly))
	grp := app.Group("config", "manage configuration")
	grp.Command("set", "set a value", handler, WithEffect(EffectReadOnly))
	grp.Deprecated("reset", "use 'set' with --default instead")

	grpDeprecated := grp.DeprecatedCommands()
	if len(grpDeprecated) != 1 {
		t.Fatalf("expected 1 deprecated group command, got %d", len(grpDeprecated))
	}
	if grpDeprecated["reset"] != "use 'set' with --default instead" {
		t.Fatalf("expected reset message, got %q", grpDeprecated["reset"])
	}
}

func TestDefaultNilDisplaysOptional(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("name", "a name", Optional())), WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd", "--help"})
	if !strings.Contains(r.Stdout, "[optional]") {
		t.Fatalf("expected [optional] in help output, got:\n%s", r.Stdout)
	}
	if strings.Contains(r.Stdout, "[required]") {
		t.Fatalf("expected no [required] in help output, got:\n%s", r.Stdout)
	}
}

func TestDefaultValueStillDisplays(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("name", "a name", Default("foo"))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd", "--help"})
	if !strings.Contains(r.Stdout, "[default: foo]") {
		t.Fatalf("expected [default: foo] in help output, got:\n%s", r.Stdout)
	}
}

func TestRequiredFlagStillDisplaysRequired(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("name", "a name", Required())), WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd", "--help"})
	if !strings.Contains(r.Stdout, "[required]") {
		t.Fatalf("expected [required] in help output, got:\n%s", r.Stdout)
	}
}

func TestHelpAfterFlags(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(BoolFlag("loud", "enable loud output", Default(false))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd", "--loud", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "cmd") {
		t.Fatalf("expected help output containing 'cmd', got:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "--loud") {
		t.Fatalf("expected help output containing '--loud', got:\n%s", r.Stdout)
	}
}

func TestHelpNotAfterSeparator(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprint(args["items"]))
		return Exit(0)
	}, WithArgs(NewArg("items", "items to process", Variadic(), ArgOptional())), WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd", "--", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	// Should NOT show help text -- --help after -- is a literal argument
	if strings.Contains(r.Stdout, "Flags:") {
		t.Fatalf("expected no help output, but got help text:\n%s", r.Stdout)
	}
}

// --- Float flag tests ---

func TestFloatFlagBasic(t *testing.T) {
	app := simpleApp("cmd", "a command", "rate={rate}",
		WithFlags(FloatFlag("rate", "the rate", Required())))
	r := app.Test([]string{"cmd", "--rate", "3.14"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "rate=3.14") {
		t.Fatalf("stdout should contain 'rate=3.14', got %q", r.Stdout)
	}
}

func TestFloatFlagEquals(t *testing.T) {
	app := simpleApp("cmd", "a command", "rate={rate}",
		WithFlags(FloatFlag("rate", "the rate", Required())))
	r := app.Test([]string{"cmd", "--rate=3.14"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "rate=3.14") {
		t.Fatalf("stdout should contain 'rate=3.14', got %q", r.Stdout)
	}
}

func TestFloatFlagShort(t *testing.T) {
	app := simpleApp("cmd", "a command", "rate={rate}",
		WithFlags(FloatFlag("rate", "the rate", Short("r"), Required())))
	r := app.Test([]string{"cmd", "-r", "3.14"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "rate=3.14") {
		t.Fatalf("stdout should contain 'rate=3.14', got %q", r.Stdout)
	}
}

func TestFloatFlagEnv(t *testing.T) {
	os.Setenv("MYAPP_RATE", "2.718")
	defer os.Unsetenv("MYAPP_RATE")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("rate=" + formatValue(args["rate"]))
		return Exit(0)
	}, WithFlags(FloatFlag("rate", "the rate", Default(1.0), Env("MYAPP_RATE"))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "rate=2.718") {
		t.Fatalf("stdout should contain 'rate=2.718', got %q", r.Stdout)
	}
}

func TestFloatFlagDefault(t *testing.T) {
	app := simpleApp("cmd", "a command", "rate={rate}",
		WithFlags(FloatFlag("rate", "the rate", Default(9.81))))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "rate=9.81") {
		t.Fatalf("stdout should contain 'rate=9.81', got %q", r.Stdout)
	}
}

func TestFloatFlagChoices(t *testing.T) {
	app := simpleApp("cmd", "a command", "rate={rate}",
		WithFlags(FloatFlag("rate", "the rate", Choices(Ch(1.0, ""), Ch(2.5, ""), Ch(3.14, "")), Required())))
	// Valid choice
	r := app.Test([]string{"cmd", "--rate", "2.5"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "rate=2.5") {
		t.Fatalf("stdout should contain 'rate=2.5', got %q", r.Stdout)
	}
	// Invalid choice
	r2 := app.Test([]string{"cmd", "--rate", "7.77"})
	if r2.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r2.ExitCode)
	}
	if !strings.Contains(r2.Stderr, "invalid value") {
		t.Fatalf("stderr should contain 'invalid value', got %q", r2.Stderr)
	}
}

func TestFloatFlagNegative(t *testing.T) {
	app := simpleApp("cmd", "a command", "temp={temp}",
		WithFlags(FloatFlag("temp", "temperature", Required())))
	r := app.Test([]string{"cmd", "--temp", "-40.5"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "temp=-40.5") {
		t.Fatalf("stdout should contain 'temp=-40.5', got %q", r.Stdout)
	}
}

func TestFloatFlagRejectNaN(t *testing.T) {
	app := simpleApp("cmd", "a command", "rate={rate}",
		WithFlags(FloatFlag("rate", "the rate", Required())))
	r := app.Test([]string{"cmd", "--rate", "NaN"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "NaN is not allowed") {
		t.Fatalf("stderr should contain 'NaN is not allowed', got %q", r.Stderr)
	}
}

func TestFloatFlagRejectInf(t *testing.T) {
	app := simpleApp("cmd", "a command", "rate={rate}",
		WithFlags(FloatFlag("rate", "the rate", Required())))
	for _, val := range []string{"Inf", "+Inf", "-Inf"} {
		r := app.Test([]string{"cmd", "--rate", val})
		if r.ExitCode != 1 {
			t.Fatalf("val=%q: expected exit 1, got %d", val, r.ExitCode)
		}
		if !strings.Contains(r.Stderr, "Inf is not allowed") {
			t.Fatalf("val=%q: stderr should contain 'Inf is not allowed', got %q", val, r.Stderr)
		}
	}
}

func TestFloatFlagRejectWhitespace(t *testing.T) {
	app := simpleApp("cmd", "a command", "rate={rate}",
		WithFlags(FloatFlag("rate", "the rate", Required())))
	for _, val := range []string{" 3.14", "3.14 ", " 3.14 "} {
		r := app.Test([]string{"cmd", "--rate", val})
		if r.ExitCode != 1 {
			t.Fatalf("val=%q: expected exit 1, got %d", val, r.ExitCode)
		}
		if !strings.Contains(r.Stderr, "expected float") {
			t.Fatalf("val=%q: stderr should contain 'expected float', got %q", val, r.Stderr)
		}
	}
}

func TestFloatFlagRepeatable(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("vals=" + formatValue(args["val"]))
		return Exit(0)
	}, WithFlags(FloatFlag("val", "a value", Repeatable(), Unique(false), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd", "--val", "1.1", "--val", "2.2", "--val", "3.3"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "vals=1.1,2.2,3.3") {
		t.Fatalf("stdout should contain 'vals=1.1,2.2,3.3', got %q", r.Stdout)
	}
}

func TestFloatFlagHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(FloatFlag("rate", "the rate", Required())))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "<float>") {
		t.Fatalf("help should contain '<float>', got:\n%s", r.Stdout)
	}
}

// --- Deep nesting (recursive group) tests ---

// make3LevelApp creates: app -> dns -> zone -> {list, create}
func make3LevelApp() *App {
	app := NewApp("nch", "1.0.0", "cloud tool")
	dns := app.Group("dns", "manage DNS")
	zone := dns.Group("zone", "manage DNS zones")
	zone.Command("list", "list all zones", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("listing zones")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	zone.Command("create", "create a zone", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("creating zone %s", args["name"]))
		return Exit(0)
	}, WithFlags(StringFlag("name", "zone name", Required())), WithEffect(EffectReadOnly))
	return app
}

// make4LevelApp creates: app -> level1 -> level2 -> level3 -> action
func make4LevelApp() *App {
	app := NewApp("deep", "1.0.0", "deeply nested app")
	g1 := app.Group("level1", "first level")
	g2 := g1.Group("level2", "second level")
	g3 := g2.Group("level3", "third level")
	g3.Command("action", "do the thing", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("action executed")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	return app
}

func TestDeepNesting3Level(t *testing.T) {
	app := make3LevelApp()
	r := app.Test([]string{"dns", "zone", "list"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "listing zones") {
		t.Fatalf("stdout should contain 'listing zones', got %q", r.Stdout)
	}

	// With flags
	r = app.Test([]string{"dns", "zone", "create", "--name", "example.com"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "creating zone example.com") {
		t.Fatalf("stdout should contain 'creating zone example.com', got %q", r.Stdout)
	}
}

func TestDeepNesting4Level(t *testing.T) {
	app := make4LevelApp()
	r := app.Test([]string{"level1", "level2", "level3", "action"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "action executed") {
		t.Fatalf("stdout should contain 'action executed', got %q", r.Stdout)
	}
}

func TestDeepNestingHelpAtEachLevel(t *testing.T) {
	app := make3LevelApp()

	// App level
	r := app.Test([]string{"--help"})
	if r.ExitCode != 0 {
		t.Fatalf("app help: expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "Groups:") || !strings.Contains(r.Stdout, "dns") {
		t.Fatalf("app help should show dns group, got %q", r.Stdout)
	}

	// Group level (dns)
	r = app.Test([]string{"dns", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("dns help: expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "Groups:") || !strings.Contains(r.Stdout, "zone") {
		t.Fatalf("dns help should show zone subgroup, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "nch dns -- manage DNS") {
		t.Fatalf("dns help header should have full path, got %q", r.Stdout)
	}

	// Subgroup level (dns zone)
	r = app.Test([]string{"dns", "zone", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("dns zone help: expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "Commands:") {
		t.Fatalf("dns zone help should show commands, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "list") || !strings.Contains(r.Stdout, "create") {
		t.Fatalf("dns zone help should show list and create, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "nch dns zone -- manage DNS zones") {
		t.Fatalf("dns zone help header should have full path, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "Use 'nch help dns zone <command>'") {
		t.Fatalf("dns zone help hint should have full path, got %q", r.Stdout)
	}

	// Command level
	r = app.Test([]string{"dns", "zone", "create", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("create help: expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "nch dns zone create") {
		t.Fatalf("create help should show full path, got %q", r.Stdout)
	}
}

func TestDeepNestingHelpAnywhere(t *testing.T) {
	app := make3LevelApp()

	// -h at group level
	r := app.Test([]string{"dns", "-h"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "nch dns") {
		t.Fatalf("help should contain 'nch dns', got %q", r.Stdout)
	}

	// -h at subgroup level
	r = app.Test([]string{"dns", "zone", "-h"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "nch dns zone") {
		t.Fatalf("help should contain 'nch dns zone', got %q", r.Stdout)
	}

	// --help after flags in deep command
	r = app.Test([]string{"dns", "zone", "create", "--name", "x", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "nch dns zone create") {
		t.Fatalf("help should contain full path, got %q", r.Stdout)
	}

	// 4-level help at each level
	app4 := make4LevelApp()
	r = app4.Test([]string{"level1", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("level1 help: expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "level2") {
		t.Fatalf("level1 help should show level2, got %q", r.Stdout)
	}

	r = app4.Test([]string{"level1", "level2", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("level2 help: expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "level3") {
		t.Fatalf("level2 help should show level3, got %q", r.Stdout)
	}

	r = app4.Test([]string{"level1", "level2", "level3", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("level3 help: expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "action") {
		t.Fatalf("level3 help should show action, got %q", r.Stdout)
	}

	r = app4.Test([]string{"level1", "level2", "level3", "action", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("action help: expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "deep level1 level2 level3 action") {
		t.Fatalf("action help should show full path, got %q", r.Stdout)
	}
}

func TestDeepNestingUnknownCommand(t *testing.T) {
	app := make3LevelApp()

	// Unknown at subgroup level
	r := app.Test([]string{"dns", "zone", "delete"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown command 'delete' in 'dns zone'") {
		t.Fatalf("stderr should contain path, got %q", r.Stderr)
	}

	// Unknown in 4-level
	app4 := make4LevelApp()
	r = app4.Test([]string{"level1", "level2", "level3", "bogus"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown command 'bogus' in 'level1 level2 level3'") {
		t.Fatalf("stderr should contain full path, got %q", r.Stderr)
	}

	// Unknown at top level has no path prefix
	r = app.Test([]string{"bogus"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown command 'bogus'") {
		t.Fatalf("stderr should contain 'unknown command', got %q", r.Stderr)
	}
	if strings.Contains(r.Stderr, "in '") {
		t.Fatalf("top-level unknown should not contain 'in', got %q", r.Stderr)
	}
}

func TestDeepNestingMixedGroupsAndCommands(t *testing.T) {
	app := NewApp("mix", "1.0.0", "mixed app")
	grp := app.Group("infra", "infrastructure")
	grp.Command("status", "show status", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("status ok")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	sub := grp.Group("network", "network management")
	sub.Command("list", "list networks", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("networks listed")
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	// Command in group works
	r := app.Test([]string{"infra", "status"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "status ok") {
		t.Fatalf("stdout should contain 'status ok', got %q", r.Stdout)
	}

	// Subgroup command works
	r = app.Test([]string{"infra", "network", "list"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "networks listed") {
		t.Fatalf("stdout should contain 'networks listed', got %q", r.Stdout)
	}

	// Help shows both commands and subgroups
	r = app.Test([]string{"infra", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "Commands:") {
		t.Fatalf("help should contain 'Commands:', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "status") {
		t.Fatalf("help should contain 'status', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "Groups:") {
		t.Fatalf("help should contain 'Groups:', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "network") {
		t.Fatalf("help should contain 'network', got %q", r.Stdout)
	}
}

func TestDeepNestingDeprecatedInSubgroup(t *testing.T) {
	app := NewApp("nch", "1.0.0", "cloud tool")
	dns := app.Group("dns", "manage DNS")
	zone := dns.Group("zone", "manage zones")
	zone.Command("list", "list zones", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("listing")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	zone.Deprecated("dump", "use 'list' instead")

	// Invoking deprecated command
	r := app.Test([]string{"dns", "zone", "dump"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "command 'dump' is deprecated: use 'list' instead") {
		t.Fatalf("stderr should contain deprecation message, got %q", r.Stderr)
	}

	// Deprecated shown in subgroup help
	r = app.Test([]string{"dns", "zone", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "Deprecated:") {
		t.Fatalf("help should contain 'Deprecated:', got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "dump") {
		t.Fatalf("help should contain 'dump', got %q", r.Stdout)
	}
}

func TestDeepNestingGlobalFlags(t *testing.T) {
	app := NewApp("nch", "1.0.0", "cloud tool")
	app.GlobalFlag(BoolFlag("loud", "enable loud output", Default(false)))
	dns := app.Group("dns", "manage DNS")
	zone := dns.Group("zone", "manage zones")
	zone.Command("list", "list zones", func(ctx *Context, args map[string]interface{}) Outcome {
		if args["loud"].(bool) {
			ctx.Out("loud listing")
		} else {
			ctx.Out("normal listing")
		}
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	// Global flag before command
	r := app.Test([]string{"--loud", "dns", "zone", "list"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "loud listing") {
		t.Fatalf("stdout should contain 'loud listing', got %q", r.Stdout)
	}

	// Without global flag
	r = app.Test([]string{"dns", "zone", "list"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "normal listing") {
		t.Fatalf("stdout should contain 'normal listing', got %q", r.Stdout)
	}

	// Global flag after command
	r = app.Test([]string{"dns", "zone", "list", "--loud"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "loud listing") {
		t.Fatalf("stdout should contain 'loud listing', got %q", r.Stdout)
	}
}

func TestDeepNestingNameCollision(t *testing.T) {
	// Subgroup and command name collision: command first, then subgroup
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic for command-then-group collision")
			}
			msg := fmt.Sprintf("%v", r)
			if !strings.Contains(msg, "collides with an existing command") {
				t.Fatalf("panic message should mention command collision, got %q", msg)
			}
		}()
		app := NewApp("test", "1.0.0", "test app")
		grp := app.Group("infra", "infra group")
		grp.Command("network", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
		grp.Group("network", "this conflicts")
	}()

	// Subgroup and command name collision: subgroup first, then command
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic for group-then-command collision")
			}
			msg := fmt.Sprintf("%v", r)
			if !strings.Contains(msg, "collides with an existing group") {
				t.Fatalf("panic message should mention group collision, got %q", msg)
			}
		}()
		app := NewApp("test", "1.0.0", "test app")
		grp := app.Group("infra", "infra group")
		grp.Group("network", "network subgroup")
		grp.Command("network", "this conflicts", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	}()

	// Deprecated and subgroup collision
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic for deprecated-then-group collision")
			}
			msg := fmt.Sprintf("%v", r)
			if !strings.Contains(msg, "collides with an existing group") {
				t.Fatalf("panic message should mention group collision, got %q", msg)
			}
		}()
		app := NewApp("test", "1.0.0", "test app")
		grp := app.Group("infra", "infra group")
		grp.Group("network", "network subgroup")
		grp.Deprecated("network", "removed")
	}()

	// Duplicate subgroup
	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic for duplicate subgroup")
			}
			msg := fmt.Sprintf("%v", r)
			if !strings.Contains(msg, "is already registered") {
				t.Fatalf("panic message should mention duplicate, got %q", msg)
			}
		}()
		app := NewApp("test", "1.0.0", "test app")
		grp := app.Group("infra", "infra group")
		grp.Group("network", "first")
		grp.Group("network", "second")
	}()
}

// --- Config tests ---

// configTestSetup sets XDG_CONFIG_HOME to a temp dir and returns a cleanup function.
func configTestSetup(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	oldVal, hadOld := os.LookupEnv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	return tmpDir, func() {
		if hadOld {
			os.Setenv("XDG_CONFIG_HOME", oldVal)
		} else {
			os.Unsetenv("XDG_CONFIG_HOME")
		}
	}
}

// writeConfig writes a JSON config file for the given app name under the given XDG dir.
func writeConfig(t *testing.T, xdgDir, appName string, data map[string]interface{}) {
	t.Helper()
	dir := filepath.Join(xdgDir, appName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
}

func TestConfigDisabledNoSubcommand(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	_ = tmpDir

	app := NewApp("testapp", "1.0.0", "test app")
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	// No config group should be registered
	if _, ok := app.Groups()["config"]; ok {
		t.Fatal("config group should not be registered when config is disabled")
	}

	// Trying "config" as a command should fail
	r := app.Test([]string{"config", "path"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown command") {
		t.Fatalf("stderr should mention unknown command, got %q", r.Stderr)
	}
}

func TestConfigEnabledHasSubcommands(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	_ = tmpDir

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	grps := app.Groups()
	configGrp, ok := grps["config"]
	if !ok {
		t.Fatal("config group should be registered when config is enabled")
	}

	// Check that all four subcommands exist
	for _, name := range []string{"show", "set", "path", "edit"} {
		if _, ok := configGrp.Commands[name]; !ok {
			t.Fatalf("config group should have '%s' subcommand", name)
		}
	}
}

func TestConfigPrecedence(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	// Config sets port=9999
	writeConfig(t, tmpDir, "testapp", map[string]interface{}{
		"port": float64(9999),
	})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("port=%d", args["port"]))
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Env("TESTAPP_PORT"), Default(8080)),
	), WithEffect(EffectReadOnly))

	// Default: 8080 (but config overrides to 9999)
	// Config value should win over default
	r := app.Test([]string{"serve"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=9999") {
		t.Fatalf("expected config value 9999, got %q", r.Stdout)
	}

	// CLI value should win over config
	r = app.Test([]string{"serve", "--port", "3000"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=3000") {
		t.Fatalf("expected CLI value 3000, got %q", r.Stdout)
	}

	// Env should win over config
	os.Setenv("TESTAPP_PORT", "5555")
	defer os.Unsetenv("TESTAPP_PORT")
	r = app.Test([]string{"serve"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=5555") {
		t.Fatalf("expected env value 5555, got %q", r.Stdout)
	}
}

func TestConfigInvalidJSON(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	// Write invalid JSON
	dir := filepath.Join(tmpDir, "testapp")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{bad json"), 0o644)

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("port=%d", args["port"]))
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"serve"})

	// Malformed config is a hard error with position information
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config file") {
		t.Fatalf("expected error about config file, got stderr=%q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "line") || !strings.Contains(r.Stderr, "column") {
		t.Fatalf("expected position info (line, column) in error, got stderr=%q", r.Stderr)
	}
}

func TestConfigSetCreatesFile(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithFlags(
		StringFlag("theme", "color theme", Default("")),
	), WithEffect(EffectReadOnly))

	// Run config set
	r := app.Test([]string{"config", "set", "theme", "--value", "dark"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	// Verify file was created
	path := filepath.Join(tmpDir, "testapp", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file should exist: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("config file should be valid JSON: %v", err)
	}
	if config["theme"] != "dark" {
		t.Fatalf("expected theme=dark, got %v", config["theme"])
	}
}

func TestConfigPath(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "path"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	expected := filepath.Join(tmpDir, "testapp", "config.json")
	if !strings.Contains(r.Stdout, expected) {
		t.Fatalf("expected path %q in stdout, got %q", expected, r.Stdout)
	}
}

func TestConfigShow(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	writeConfig(t, tmpDir, "testapp", map[string]interface{}{
		"port": float64(9999),
	})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
		StringFlag("host", "hostname", Default("localhost")),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	// port should show config source
	if !strings.Contains(r.Stdout, "port = 9999") {
		t.Fatalf("expected port=9999 in output, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "(source: config)") {
		t.Fatalf("expected source: config in output, got %q", r.Stdout)
	}

	// host should show default source
	if !strings.Contains(r.Stdout, "host = localhost") {
		t.Fatalf("expected host=localhost in output, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "host = localhost  (source: default)") {
		t.Fatalf("expected host with source: default in output, got %q", r.Stdout)
	}
}

// TestConfigShowCoercesConfigValuesToFlagType pins cross-language parity for
// `config show --plain`: a JSON config value for an int flag must display as
// an integer ("count = 42"), not as the raw float64 that encoding/json
// produces ("count = 42.0"). Python and TypeScript both display "42".
func TestConfigShowCoercesConfigValuesToFlagType(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	writeConfig(t, tmpDir, "testapp", map[string]interface{}{
		"count": float64(42),
		"rate":  3.14,
	})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		IntFlag("count", "a count", Default(0)),
		FloatFlag("rate", "a rate", Default(0.0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "count = 42  (source: config)") {
		t.Fatalf("expected 'count = 42  (source: config)' in output, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "rate = 3.14  (source: config)") {
		t.Fatalf("expected 'rate = 3.14  (source: config)' in output, got %q", r.Stdout)
	}
}

func TestConfigXDGHome(t *testing.T) {
	tmpDir := t.TempDir()
	oldVal, hadOld := os.LookupEnv("XDG_CONFIG_HOME")
	os.Setenv("XDG_CONFIG_HOME", tmpDir)
	defer func() {
		if hadOld {
			os.Setenv("XDG_CONFIG_HOME", oldVal)
		} else {
			os.Unsetenv("XDG_CONFIG_HOME")
		}
	}()

	// Verify configPath respects XDG_CONFIG_HOME
	path := configPath("myapp", "", "json")
	expected := filepath.Join(tmpDir, "myapp", "config.json")
	if path != expected {
		t.Fatalf("expected path %q, got %q", expected, path)
	}

	// Verify with a different XDG_CONFIG_HOME
	otherDir := t.TempDir()
	os.Setenv("XDG_CONFIG_HOME", otherDir)
	path = configPath("myapp", "", "json")
	expected = filepath.Join(otherDir, "myapp", "config.json")
	if path != expected {
		t.Fatalf("expected path %q, got %q", expected, path)
	}
}

// --- Dump schema tests ---

// chdirTemp changes to a temporary directory and restores the original on cleanup.
// It also creates a go.mod file so that --dump-schema can read project_id.
func chdirTemp(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Create go.mod for project_id resolution
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/testproject\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })
	return tmpDir
}

func TestDumpSchemaContents(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "2.3.4", "My great app")
	app.Command("deploy", "Deploy the app", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(
			StringFlag("target", "Deploy target", Short("t"), Choices(Ch("prod", ""), Ch("staging", "")), Required()),
			BoolFlag("force-deploy", "Force deploy", Default(false)),
		),
		WithArgs(
			NewArg("env", "Environment name", ArgRequired()),
		), WithEffect(EffectReadOnly),
	)

	r := app.Test([]string{"help", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	data := []byte(r.Stdout)
	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// App metadata
	if schema["name"] != "myapp" {
		t.Fatalf("expected name 'myapp', got %v", schema["name"])
	}
	if schema["version"] != "2.3.4" {
		t.Fatalf("expected version '2.3.4', got %v", schema["version"])
	}
	if schema["help"] != "My great app" {
		t.Fatalf("expected help 'My great app', got %v", schema["help"])
	}
	// env_prefix should be omitted when not set (default is null)
	if _, ok := schema["env_prefix"]; ok {
		t.Fatalf("expected env_prefix to be omitted, got %v", schema["env_prefix"])
	}
	// config should be omitted when false (default is false)
	if _, ok := schema["config"]; ok {
		t.Fatalf("expected config to be omitted, got %v", schema["config"])
	}
	// defaults object must be present
	defaults, ok := schema["defaults"]
	if !ok {
		t.Fatal("expected 'defaults' key in schema")
	}
	defaultsMap, ok := defaults.(map[string]interface{})
	if !ok {
		t.Fatalf("expected defaults to be a map, got %T", defaults)
	}
	for _, key := range []string{"app", "flag", "arg", "command", "group"} {
		if _, ok := defaultsMap[key]; !ok {
			t.Fatalf("expected defaults to contain key %q", key)
		}
	}

	// Commands
	cmds, ok := schema["commands"].(map[string]interface{})
	if !ok {
		t.Fatal("commands is not a map")
	}
	deploy, ok := cmds["deploy"].(map[string]interface{})
	if !ok {
		t.Fatal("deploy command not found")
	}
	if deploy["name"] != "deploy" {
		t.Fatalf("expected command name 'deploy', got %v", deploy["name"])
	}
	if deploy["help"] != "Deploy the app" {
		t.Fatalf("expected help 'Deploy the app', got %v", deploy["help"])
	}
	// passthrough should be omitted when false (default)
	if _, ok := deploy["passthrough"]; ok {
		t.Fatalf("expected passthrough to be omitted, got %v", deploy["passthrough"])
	}

	// Flags
	flags, ok := deploy["flags"].([]interface{})
	if !ok || len(flags) != 2 {
		t.Fatalf("expected 2 flags, got %v", deploy["flags"])
	}
	targetFlag := flags[0].(map[string]interface{})
	if targetFlag["name"] != "target" {
		t.Fatalf("expected flag name 'target', got %v", targetFlag["name"])
	}
	if got := targetFlag["value_schema"].(map[string]interface{})["type"]; got != "string" {
		t.Fatalf("expected fragment type 'string', got %v", got)
	}
	if targetFlag["short"] != "t" {
		t.Fatalf("expected short 't', got %v", targetFlag["short"])
	}
	choices, ok := targetFlag["choices"].([]interface{})
	if !ok || len(choices) != 2 {
		t.Fatalf("expected 2 choices, got %v", targetFlag["choices"])
	}
	// The sibling key carries the value-plus-help RECORDS; the values
	// themselves live in the fragment's `enum` (contract §25.5).
	if choices[0].(map[string]interface{})["value"] != "prod" ||
		choices[1].(map[string]interface{})["value"] != "staging" {
		t.Fatalf("expected the records for [prod, staging], got %v", choices)
	}
	enum := targetFlag["value_schema"].(map[string]interface{})["enum"].([]interface{})
	if enum[0] != "prod" || enum[1] != "staging" {
		t.Fatalf("expected the enum [prod, staging], got %v", enum)
	}
	// hidden should be omitted when false (default)
	if _, ok := targetFlag["hidden"]; ok {
		t.Fatalf("expected hidden to be omitted, got %v", targetFlag["hidden"])
	}

	forceFlag := flags[1].(map[string]interface{})
	if forceFlag["name"] != "force-deploy" {
		t.Fatalf("expected flag name 'force-deploy', got %v", forceFlag["name"])
	}
	if got := forceFlag["value_schema"].(map[string]interface{})["type"]; got != "boolean" {
		t.Fatalf("expected fragment type 'boolean', got %v", got)
	}
	if forceFlag["negatable"] != true {
		t.Fatalf("expected negatable true, got %v", forceFlag["negatable"])
	}
	if forceFlag["default"] != false {
		t.Fatalf("expected default false, got %v", forceFlag["default"])
	}

	// Args
	args, ok := deploy["args"].([]interface{})
	if !ok || len(args) != 1 {
		t.Fatalf("expected 1 arg, got %v", deploy["args"])
	}
	envArg := args[0].(map[string]interface{})
	if envArg["name"] != "env" {
		t.Fatalf("expected arg name 'env', got %v", envArg["name"])
	}
	if envArg["help"] != "Environment name" {
		t.Fatalf("expected arg help 'Environment name', got %v", envArg["help"])
	}
	// required should be omitted when true (default)
	if _, ok := envArg["required"]; ok {
		t.Fatalf("expected required to be omitted (default is true), got %v", envArg["required"])
	}
	// variadic should be omitted when false (default)
	if _, ok := envArg["variadic"]; ok {
		t.Fatalf("expected variadic to be omitted (default is false), got %v", envArg["variadic"])
	}
}

func TestDumpSchemaGroups(t *testing.T) {
	chdirTemp(t)
	app := NewApp("testapp", "1.0.0", "A test app")
	dns := app.Group("dns", "DNS management")
	dns.Command("list", "List DNS records", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	dns.Command("add", "Add a DNS record", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("type", "Record type", Required())), WithEffect(EffectReadOnly),
	)
	zone := dns.Group("zone", "Zone management")
	zone.Command("list", "List zones", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"help", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	data := []byte(r.Stdout)
	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	groups, ok := schema["groups"].(map[string]interface{})
	if !ok {
		t.Fatal("groups is not a map")
	}
	dnsGrp, ok := groups["dns"].(map[string]interface{})
	if !ok {
		t.Fatal("dns group not found")
	}
	if dnsGrp["name"] != "dns" {
		t.Fatalf("expected group name 'dns', got %v", dnsGrp["name"])
	}
	if dnsGrp["help"] != "DNS management" {
		t.Fatalf("expected help 'DNS management', got %v", dnsGrp["help"])
	}

	dnsCmds, ok := dnsGrp["commands"].(map[string]interface{})
	if !ok {
		t.Fatal("dns commands not a map")
	}
	if _, ok := dnsCmds["list"]; !ok {
		t.Fatal("dns list command not found")
	}
	if _, ok := dnsCmds["add"]; !ok {
		t.Fatal("dns add command not found")
	}
	addCmd := dnsCmds["add"].(map[string]interface{})
	addFlags := addCmd["flags"].([]interface{})
	if len(addFlags) != 1 {
		t.Fatalf("expected 1 flag on add, got %d", len(addFlags))
	}
	if addFlags[0].(map[string]interface{})["name"] != "type" {
		t.Fatalf("expected flag 'type', got %v", addFlags[0])
	}

	// Nested groups
	dnsGroups, ok := dnsGrp["groups"].(map[string]interface{})
	if !ok {
		t.Fatal("dns groups not a map")
	}
	zoneGrp, ok := dnsGroups["zone"].(map[string]interface{})
	if !ok {
		t.Fatal("zone group not found")
	}
	if zoneGrp["name"] != "zone" {
		t.Fatalf("expected group name 'zone', got %v", zoneGrp["name"])
	}
	if zoneGrp["help"] != "Zone management" {
		t.Fatalf("expected help 'Zone management', got %v", zoneGrp["help"])
	}
	zoneCmds, ok := zoneGrp["commands"].(map[string]interface{})
	if !ok {
		t.Fatal("zone commands not a map")
	}
	if _, ok := zoneCmds["list"]; !ok {
		t.Fatal("zone list command not found")
	}
}

func TestDumpSchemaGlobalFlags(t *testing.T) {
	chdirTemp(t)
	app := NewApp("testapp", "1.0.0", "A test app")
	app.GlobalFlag(BoolFlag("loud", "Loud output", Short("V"), Default(false)))
	app.GlobalFlag(StringFlag("output", "Output format", Default("text"), Choices(Ch("text", ""), Ch("json", ""))))
	app.Command("noop", "Does nothing", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"help", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	data := []byte(r.Stdout)
	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	gFlags, ok := schema["global_flags"].([]interface{})
	if !ok {
		t.Fatal("global_flags is not an array")
	}
	if len(gFlags) != 2 {
		t.Fatalf("expected 2 global flags, got %d", len(gFlags))
	}

	loud := gFlags[0].(map[string]interface{})
	if loud["name"] != "loud" {
		t.Fatalf("expected name 'loud', got %v", loud["name"])
	}
	if got := loud["value_schema"].(map[string]interface{})["type"]; got != "boolean" {
		t.Fatalf("expected fragment type 'boolean', got %v", got)
	}
	if loud["short"] != "V" {
		t.Fatalf("expected short 'V', got %v", loud["short"])
	}
	if loud["negatable"] != true {
		t.Fatalf("expected negatable true, got %v", loud["negatable"])
	}

	output := gFlags[1].(map[string]interface{})
	if output["name"] != "output" {
		t.Fatalf("expected name 'output', got %v", output["name"])
	}
	if got := output["value_schema"].(map[string]interface{})["type"]; got != "string" {
		t.Fatalf("expected fragment type 'string', got %v", got)
	}
	if output["default"] != "text" {
		t.Fatalf("expected default 'text', got %v", output["default"])
	}
	choices, ok := output["choices"].([]interface{})
	if !ok || len(choices) != 2 {
		t.Fatalf("expected 2 choices, got %v", output["choices"])
	}
	// negatable should be null for non-bool
	if output["negatable"] != nil {
		t.Fatalf("expected negatable nil for non-bool, got %v", output["negatable"])
	}
}

func TestDumpSchemaDeprecated(t *testing.T) {
	chdirTemp(t)
	app := NewApp("testapp", "1.0.0", "A test app")
	app.Command("new-cmd", "The new command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	app.Deprecated("old-cmd", "Use 'new-cmd' instead")

	// Also test group-level deprecated
	dns := app.Group("dns", "DNS management")
	dns.Command("list", "List records", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	dns.Deprecated("old-list", "Use 'list' instead")

	r := app.Test([]string{"help", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	data := []byte(r.Stdout)
	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	deprecated, ok := schema["deprecated"].(map[string]interface{})
	if !ok {
		t.Fatal("deprecated is not a map")
	}
	msg, ok := deprecated["old-cmd"]
	if !ok {
		t.Fatal("deprecated old-cmd not found")
	}
	if msg != "Use 'new-cmd' instead" {
		t.Fatalf("expected message \"Use 'new-cmd' instead\", got %v", msg)
	}

	// Group-level deprecated
	groups := schema["groups"].(map[string]interface{})
	dnsGrp := groups["dns"].(map[string]interface{})
	grpDeprecated := dnsGrp["deprecated"].(map[string]interface{})
	grpMsg, ok := grpDeprecated["old-list"]
	if !ok {
		t.Fatal("deprecated old-list not found in dns group")
	}
	if grpMsg != "Use 'list' instead" {
		t.Fatalf("expected message \"Use 'list' instead\", got %v", grpMsg)
	}
}

// --- @-prefix tests ---

func TestAtPrefixFileBasic(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "input.txt")
	os.WriteFile(tmpFile, []byte("hello world"), 0644)

	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Required())))
	r := app.Test([]string{"greet", "--msg", "@" + tmpFile})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "hello world\n" {
		t.Fatalf("expected 'hello world', got %q", r.Stdout)
	}
}

func TestAtPrefixFileMultiline(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "input.txt")
	os.WriteFile(tmpFile, []byte("line1\nline2\nline3"), 0644)

	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Required())))
	r := app.Test([]string{"greet", "--msg", "@" + tmpFile})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "line1\nline2\nline3\n" {
		t.Fatalf("expected 'line1\\nline2\\nline3', got %q", r.Stdout)
	}
}

func TestAtPrefixFileEmpty(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "empty.txt")
	os.WriteFile(tmpFile, []byte(""), 0644)

	app := simpleApp("greet", "say hello", ">{msg}<",
		WithFlags(StringFlag("msg", "message", Required())))
	r := app.Test([]string{"greet", "--msg", "@" + tmpFile})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "><\n" {
		t.Fatalf("expected '><', got %q", r.Stdout)
	}
}

func TestAtPrefixFileTrailingWhitespace(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "input.txt")
	os.WriteFile(tmpFile, []byte("hello  \n\n"), 0644)

	app := simpleApp("greet", "say hello", ">{msg}<",
		WithFlags(StringFlag("msg", "message", Required())))
	r := app.Test([]string{"greet", "--msg", "@" + tmpFile})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != ">hello<\n" {
		t.Fatalf("expected '>hello<', got %q", r.Stdout)
	}
}

func TestAtPrefixStdin(t *testing.T) {
	// Save original stdin and restore after test
	origStdin := os.Stdin
	defer func() { os.Stdin = origStdin }()

	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.Write([]byte("from stdin\n"))
		w.Close()
	}()

	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Required())))
	result := app.Test([]string{"greet", "--msg", "@-"})
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "from stdin\n" {
		t.Fatalf("expected 'from stdin', got %q", result.Stdout)
	}
}

func TestAtPrefixEscapeSingle(t *testing.T) {
	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Required())))
	r := app.Test([]string{"greet", "--msg", "@@foo"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "@foo\n" {
		t.Fatalf("expected '@foo', got %q", r.Stdout)
	}
}

func TestAtPrefixEscapeDouble(t *testing.T) {
	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Required())))
	r := app.Test([]string{"greet", "--msg", "@@@"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "@@\n" {
		t.Fatalf("expected '@@', got %q", r.Stdout)
	}
}

func TestAtPrefixFileNotFound(t *testing.T) {
	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Required())))
	r := app.Test([]string{"greet", "--msg", "@/nonexistent/path.txt"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--msg: file not found: /nonexistent/path.txt") {
		t.Fatalf("expected file not found error, got %q", r.Stderr)
	}
}

func TestAtPrefixFileTooLarge(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "large.txt")
	// Write just over 1MB
	data := make([]byte, 1024*1024+1)
	for i := range data {
		data[i] = 'x'
	}
	os.WriteFile(tmpFile, data, 0644)

	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Required())))
	r := app.Test([]string{"greet", "--msg", "@" + tmpFile})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--msg: file exceeds 1 MB limit") {
		t.Fatalf("expected file too large error, got %q", r.Stderr)
	}
}

func TestAtPrefixStdinDuplicate(t *testing.T) {
	origStdin := os.Stdin
	defer func() { os.Stdin = origStdin }()

	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.Write([]byte("data"))
		w.Close()
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprint(args["msg"]))
		return Exit(0)
	}, WithFlags(
		StringFlag("msg", "message", Required()),
		StringFlag("other", "other message", Required()),
	), WithEffect(EffectReadOnly))
	result := app.Test([]string{"greet", "--msg", "@-", "--other", "@-"})
	if result.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "--other: stdin (@-) can only be used once per invocation") {
		t.Fatalf("expected stdin duplicate error, got %q", result.Stderr)
	}
}

func TestAtPrefixStdinDuplicateGlobalAndCommand(t *testing.T) {
	origStdin := os.Stdin
	defer func() { os.Stdin = origStdin }()

	r, w, _ := os.Pipe()
	os.Stdin = r
	go func() {
		w.Write([]byte("data"))
		w.Close()
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(StringFlag("token", "auth token", Required()))
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprint(args["msg"]))
		return Exit(0)
	}, WithFlags(StringFlag("msg", "message", Required())), WithEffect(EffectReadOnly))

	result := app.Test([]string{"--token", "@-", "greet", "--msg", "@-"})
	if result.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, "--msg: stdin (@-) can only be used once per invocation") {
		t.Fatalf("expected stdin duplicate error, got %q", result.Stderr)
	}
}

func TestAtPrefixNonStringIgnored(t *testing.T) {
	app := simpleApp("greet", "say hello", "{count}",
		WithFlags(IntFlag("count", "count", Required())))
	r := app.Test([]string{"greet", "--count", "@5"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "expected integer") {
		t.Fatalf("expected integer parse error, got %q", r.Stderr)
	}
}

func TestAtPrefixEnvVar(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "env_input.txt")
	os.WriteFile(tmpFile, []byte("env value"), 0644)

	os.Setenv("TEST_MSG_AT", "@"+tmpFile)
	defer os.Unsetenv("TEST_MSG_AT")

	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Env("TEST_MSG_AT"), Prefixed(false), Required())))
	r := app.Test([]string{"greet"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "env value\n" {
		t.Fatalf("expected 'env value', got %q", r.Stdout)
	}
}

func TestAtPrefixEnvVarEscape(t *testing.T) {
	os.Setenv("TEST_MSG_ESC", "@@literal")
	defer os.Unsetenv("TEST_MSG_ESC")

	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Env("TEST_MSG_ESC"), Prefixed(false), Required())))
	r := app.Test([]string{"greet"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "@literal\n" {
		t.Fatalf("expected '@literal', got %q", r.Stdout)
	}
}

func TestAtPrefixGlobalFlag(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "token.txt")
	os.WriteFile(tmpFile, []byte("secret-token"), 0644)

	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(StringFlag("token", "auth token", Required()))
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprint(args["token"]))
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"--token", "@" + tmpFile, "greet"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "secret-token\n" {
		t.Fatalf("expected 'secret-token', got %q", r.Stdout)
	}
}

func TestAtPrefixEqualsForm(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "input.txt")
	os.WriteFile(tmpFile, []byte("equals-value"), 0644)

	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Required())))
	r := app.Test([]string{"greet", "--msg=@" + tmpFile})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "equals-value\n" {
		t.Fatalf("expected 'equals-value', got %q", r.Stdout)
	}
}

func TestAtPrefixShortForm(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "input.txt")
	os.WriteFile(tmpFile, []byte("short-value"), 0644)

	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Short("m"), Required())))
	r := app.Test([]string{"greet", "-m", "@" + tmpFile})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "short-value\n" {
		t.Fatalf("expected 'short-value', got %q", r.Stdout)
	}
}

func TestAtPrefixDefaultNotResolved(t *testing.T) {
	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Default("@not-a-file"))))
	r := app.Test([]string{"greet"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "@not-a-file\n" {
		t.Fatalf("expected '@not-a-file', got %q", r.Stdout)
	}
}

func TestAtPrefixFileIsDirectory(t *testing.T) {
	dir := t.TempDir()

	app := simpleApp("greet", "say hello", "{msg}",
		WithFlags(StringFlag("msg", "message", Required())))
	r := app.Test([]string{"greet", "--msg", "@" + dir})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--msg: cannot read file: "+dir) {
		t.Fatalf("expected cannot read file error, got %q", r.Stderr)
	}
}

func TestAtPrefixGlobalFlagEnv(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "token.txt")
	os.WriteFile(tmpFile, []byte("env-token  \n"), 0644)

	os.Setenv("TEST_TOKEN_AT", "@"+tmpFile)
	defer os.Unsetenv("TEST_TOKEN_AT")

	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(StringFlag("token", "auth token", Env("TEST_TOKEN_AT"), Prefixed(false), Required()))
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprint(args["token"]))
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"greet"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "env-token\n" {
		t.Fatalf("expected 'env-token', got %q", r.Stdout)
	}
}

func TestAtPrefixGlobalEqualsForm(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "input.txt")
	os.WriteFile(tmpFile, []byte("global-eq"), 0644)

	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(StringFlag("token", "auth token", Required()))
	app.Command("greet", "say hello", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprint(args["token"]))
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"--token=@" + tmpFile, "greet"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "global-eq\n" {
		t.Fatalf("expected 'global-eq', got %q", r.Stdout)
	}
}

// --- Red tests: Config TOML and type coercion bugs ---

// writeTomlConfig writes a TOML config file for the given app name under the given XDG dir.
func writeTomlConfig(t *testing.T, xdgDir, appName string, content string) {
	t.Helper()
	dir := filepath.Join(xdgDir, appName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write TOML config: %v", err)
	}
}

// Bug 0.1: loadConfig always uses json.Unmarshal, so TOML files are parsed as JSON,
// fail silently, and all flags get default values.
func TestTomlConfigLoading(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	tomlContent := "loud = 3\ndebug = true\nrate = 1.5\nthreshold = 3\n"
	writeTomlConfig(t, tmpDir, "tomlapp", tomlContent)

	app := NewApp("tomlapp", "1.0.0", "test app", WithConfig(), WithConfigFormat("toml"))
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("loud=%v debug=%v rate=%v threshold=%v",
			args["loud"], args["debug"], args["rate"], args["threshold"]))
		return Exit(0)
	}, WithFlags(
		IntFlag("loud", "verbosity level", Default(0)),
		BoolFlag("debug", "enable debug mode", Default(false)),
		FloatFlag("rate", "rate value", Default(0.0)),
		FloatFlag("threshold", "threshold value", Default(0.0)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	// These assertions should pass if TOML loading works, but currently fail
	// because loadConfig uses json.Unmarshal which silently fails on TOML.
	if !strings.Contains(r.Stdout, "loud=3") {
		t.Errorf("expected loud=3 from TOML config, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "debug=true") {
		t.Errorf("expected debug=true from TOML config, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "rate=1.5") {
		t.Errorf("expected rate=1.5 from TOML config, got %q", r.Stdout)
	}
	// TOML integer 3 assigned to a float flag should become 3 (float)
	if !strings.Contains(r.Stdout, "threshold=3") {
		t.Errorf("expected threshold=3 from TOML config, got %q", r.Stdout)
	}
}

// Bug 0.2: config set always writes JSON (json.MarshalIndent) regardless of format.
func TestConfigSetWritesToml(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("tomlsetapp", "1.0.0", "test app", WithConfig(), WithConfigFormat("toml"))
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		StringFlag("name", "a name", Default("")),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "set", "name", "--value", "alice"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	// Read the config file and verify it is TOML, not JSON
	path := filepath.Join(tmpDir, "tomlsetapp", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file should exist: %v", err)
	}

	content := string(data)
	// TOML format should not contain JSON braces
	if strings.Contains(content, "{") || strings.Contains(content, "}") {
		t.Errorf("config file should be TOML, not JSON; got:\n%s", content)
	}
	// Should contain a TOML key-value pair
	if !strings.Contains(content, "name") || !strings.Contains(content, "alice") {
		t.Errorf("config file should contain name = alice; got:\n%s", content)
	}
}

// Bug 0.3: config set stores raw string values from argv, so the file has
// "42", "true", "3.14" as JSON strings instead of typed JSON values.
func TestConfigSetWritesTypedValues(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("typedapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		IntFlag("count", "a count", Default(0)),
		BoolFlag("loud", "loud mode", Default(false)),
		FloatFlag("rate", "a rate", Default(0.0)),
	), WithEffect(EffectReadOnly))

	// Set each value via config set
	r := app.Test([]string{"config", "set", "count", "--value", "42"})
	if r.ExitCode != 0 {
		t.Fatalf("config set count: expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	r = app.Test([]string{"config", "set", "loud", "--value", "true"})
	if r.ExitCode != 0 {
		t.Fatalf("config set loud: expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	r = app.Test([]string{"config", "set", "rate", "--value", "3.14"})
	if r.ExitCode != 0 {
		t.Fatalf("config set rate: expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	// Read and parse the JSON config file
	path := filepath.Join(tmpDir, "typedapp", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file should exist: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("config file should be valid JSON: %v", err)
	}

	// count should be a JSON number (float64 after Go JSON decode), not string "42"
	if countVal, ok := config["count"]; !ok {
		t.Fatal("config should have 'count' key")
	} else if _, isString := countVal.(string); isString {
		t.Errorf("count should be a JSON number, not string %q", countVal)
	} else if countVal != float64(42) {
		t.Errorf("count should be 42, got %v (%T)", countVal, countVal)
	}

	// loud should be a JSON bool, not string "true"
	if loudVal, ok := config["loud"]; !ok {
		t.Fatal("config should have 'loud' key")
	} else if _, isString := loudVal.(string); isString {
		t.Errorf("loud should be a JSON bool, not string %q", loudVal)
	} else if loudVal != true {
		t.Errorf("loud should be true, got %v (%T)", loudVal, loudVal)
	}

	// rate should be a JSON number (float64), not string "3.14"
	if rateVal, ok := config["rate"]; !ok {
		t.Fatal("config should have 'rate' key")
	} else if _, isString := rateVal.(string); isString {
		t.Errorf("rate should be a JSON number, not string %q", rateVal)
	} else if rateVal != float64(3.14) {
		t.Errorf("rate should be 3.14, got %v (%T)", rateVal, rateVal)
	}
}

// Bug 0.5: config set accepts any key without validation against registered flags.
func TestConfigSetRejectsUnknownKey(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	_ = tmpDir

	app := NewApp("rejectapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		StringFlag("name", "a name", Default("")),
	), WithEffect(EffectReadOnly))

	// Setting a key that doesn't correspond to any registered flag should fail
	r := app.Test([]string{"config", "set", "nonexistent", "--value", "value"})
	if r.ExitCode == 0 {
		t.Errorf("expected nonzero exit code for unknown config key, got 0")
	}
	if !strings.Contains(r.Stderr, "nonexistent") {
		t.Errorf("stderr should mention the unknown key 'nonexistent', got %q", r.Stderr)
	}
}

func TestConfigSetTypedBool(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("boolapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithFlags(
		BoolFlag("debug", "debug mode", Default(false)),
	), WithEffect(EffectReadOnly))

	path := filepath.Join(tmpDir, "boolapp", "config.json")

	// "true" -> bool true
	r := app.Test([]string{"config", "set", "debug", "--value", "true"})
	if r.ExitCode != 0 {
		t.Fatalf("config set debug true: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	assertConfigValue(t, path, "debug", true)

	// "yes" -> bool true
	r = app.Test([]string{"config", "set", "debug", "--value", "yes"})
	if r.ExitCode != 0 {
		t.Fatalf("config set debug yes: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	assertConfigValue(t, path, "debug", true)

	// "1" -> bool true
	r = app.Test([]string{"config", "set", "debug", "--value", "1"})
	if r.ExitCode != 0 {
		t.Fatalf("config set debug 1: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	assertConfigValue(t, path, "debug", true)

	// "false" -> bool false
	r = app.Test([]string{"config", "set", "debug", "--value", "false"})
	if r.ExitCode != 0 {
		t.Fatalf("config set debug false: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	assertConfigValue(t, path, "debug", false)
}

func TestConfigSetTypedInt(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("intapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithFlags(
		IntFlag("count", "a count", Default(0)),
	), WithEffect(EffectReadOnly))

	path := filepath.Join(tmpDir, "intapp", "config.json")

	r := app.Test([]string{"config", "set", "count", "--value", "42"})
	if r.ExitCode != 0 {
		t.Fatalf("config set count 42: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	assertConfigValue(t, path, "count", float64(42))
}

func TestConfigSetTypedFloat(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("floatapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithFlags(
		FloatFlag("rate", "a rate", Default(0.0)),
	), WithEffect(EffectReadOnly))

	path := filepath.Join(tmpDir, "floatapp", "config.json")

	// "3.14" -> float64(3.14)
	r := app.Test([]string{"config", "set", "rate", "--value", "3.14"})
	if r.ExitCode != 0 {
		t.Fatalf("config set rate 3.14: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	assertConfigValue(t, path, "rate", float64(3.14))

	// "3" -> float64(3) (stored as 3.0 in JSON)
	r = app.Test([]string{"config", "set", "rate", "--value", "3"})
	if r.ExitCode != 0 {
		t.Fatalf("config set rate 3: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	assertConfigValue(t, path, "rate", float64(3))
}

func TestConfigSetBadValue(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("badvalapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithFlags(
		IntFlag("count", "a count", Default(0)),
		BoolFlag("debug", "debug mode", Default(false)),
		FloatFlag("rate", "a rate", Default(0.0)),
	), WithEffect(EffectReadOnly))

	// Bad int
	r := app.Test([]string{"config", "set", "count", "--value", "abc"})
	if r.ExitCode == 0 {
		t.Errorf("config set count abc: expected nonzero exit")
	}
	if !strings.Contains(r.Stderr, "expected integer") {
		t.Errorf("stderr should mention 'expected integer', got %q", r.Stderr)
	}

	// Bad bool
	r = app.Test([]string{"config", "set", "debug", "--value", "maybe"})
	if r.ExitCode == 0 {
		t.Errorf("config set debug maybe: expected nonzero exit")
	}
	if !strings.Contains(r.Stderr, "expected boolean") {
		t.Errorf("stderr should mention 'expected boolean', got %q", r.Stderr)
	}

	// Bad float
	r = app.Test([]string{"config", "set", "rate", "--value", "xyz"})
	if r.ExitCode == 0 {
		t.Errorf("config set rate xyz: expected nonzero exit")
	}
	if !strings.Contains(r.Stderr, "expected float") {
		t.Errorf("stderr should mention 'expected float', got %q", r.Stderr)
	}
}

func TestConfigSetUnknownKeyError(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("unkapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithFlags(
		StringFlag("name", "a name", Default("")),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "set", "xyz", "--value", "value"})
	if r.ExitCode == 0 {
		t.Errorf("expected nonzero exit for unknown key")
	}
	if !strings.Contains(r.Stderr, "unknown key 'xyz'") {
		t.Errorf("stderr should contain \"unknown key 'xyz'\", got %q", r.Stderr)
	}
}

func TestConfigSetRoundTripTyped(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	// buildApp creates a fresh app instance each time, so configData
	// is loaded from disk at construction time.
	buildApp := func() *App {
		app := NewApp("rtapp", "1.0.0", "test app", WithConfig())
		app.Command("show", "show values", func(ctx *Context, args map[string]interface{}) Outcome {
			ctx.Out(fmt.Sprintf("count=%d loud=%t rate=%.2f name=%s",
				args["count"], args["loud"], args["rate"], args["name"]))
			return Exit(0)
		}, WithFlags(
			IntFlag("count", "a count", Default(0)),
			BoolFlag("loud", "loud mode", Default(false)),
			FloatFlag("rate", "a rate", Default(0.0)),
			StringFlag("name", "a name", Default("")),
		), WithEffect(EffectReadOnly))
		return app
	}

	// Set typed values via config set
	for _, cmd := range [][]string{
		{"config", "set", "count", "--value", "7"},
		{"config", "set", "loud", "--value", "true"},
		{"config", "set", "rate", "--value", "2.5"},
		{"config", "set", "name", "--value", "hello"},
	} {
		app := buildApp()
		r := app.Test(cmd)
		if r.ExitCode != 0 {
			t.Fatalf("config set %v: exit %d, stderr=%q", cmd, r.ExitCode, r.Stderr)
		}
	}

	// Build a fresh app that loads the config from disk, then run a command
	app := buildApp()
	r := app.Test([]string{"show"})
	if r.ExitCode != 0 {
		t.Fatalf("show: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	expected := "count=7 loud=true rate=2.50 name=hello\n"
	if r.Stdout != expected {
		t.Errorf("expected %q, got %q", expected, r.Stdout)
	}
}

// assertConfigValue reads the JSON config file and checks that key has the expected value.
func assertConfigValue(t *testing.T, path, key string, expected interface{}) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file should exist at %s: %v", path, err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("config file should be valid JSON: %v", err)
	}
	val, ok := config[key]
	if !ok {
		t.Fatalf("config should have key '%s'", key)
	}
	if val != expected {
		t.Errorf("config['%s'] = %v (%T), expected %v (%T)", key, val, val, expected, expected)
	}
}

func TestTomlConfigPrecedence(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	// TOML config sets port=9999
	writeTomlConfig(t, tmpDir, "tomlprecapp", "port = 9999\n")

	app := NewApp("tomlprecapp", "1.0.0", "test app", WithConfig(), WithConfigFormat("toml"))
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("port=%d", args["port"]))
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Env("TOMLPREC_PORT"), Default(8080)),
	), WithEffect(EffectReadOnly))

	// Config value should win over default
	r := app.Test([]string{"serve"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=9999") {
		t.Fatalf("expected config value 9999, got %q", r.Stdout)
	}

	// CLI value should win over config
	r = app.Test([]string{"serve", "--port", "3000"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=3000") {
		t.Fatalf("expected CLI value 3000, got %q", r.Stdout)
	}

	// Env should win over config
	os.Setenv("TOMLPREC_PORT", "5555")
	defer os.Unsetenv("TOMLPREC_PORT")
	r = app.Test([]string{"serve"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=5555") {
		t.Fatalf("expected env value 5555, got %q", r.Stdout)
	}
}

func TestTomlConfigInvalidToml(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	// Write invalid TOML
	dir := filepath.Join(tmpDir, "tomlbadapp")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("= invalid toml [[["), 0o644)

	app := NewApp("tomlbadapp", "1.0.0", "test app", WithConfig(), WithConfigFormat("toml"))
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("port=%d", args["port"]))
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"serve"})

	// Malformed TOML is now a hard error with position information
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config file") {
		t.Fatalf("expected error about config file, got stderr=%q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "line") || !strings.Contains(r.Stderr, "column") {
		t.Fatalf("expected position info (line, column) in error, got stderr=%q", r.Stderr)
	}
}

func TestTomlConfigShow(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	writeTomlConfig(t, tmpDir, "tomlshowapp", "port = 9999\n")

	app := NewApp("tomlshowapp", "1.0.0", "test app", WithConfig(), WithConfigFormat("toml"))
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
		StringFlag("host", "hostname", Default("localhost")),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	// port should show config source with value from TOML
	if !strings.Contains(r.Stdout, "port = 9999") {
		t.Fatalf("expected port=9999 in output, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "(source: config)") {
		t.Fatalf("expected source: config in output, got %q", r.Stdout)
	}

	// host should show default source
	if !strings.Contains(r.Stdout, "host = localhost") {
		t.Fatalf("expected host=localhost in output, got %q", r.Stdout)
	}
}

func TestConfigShowJSON(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	writeConfig(t, tmpDir, "testapp", map[string]interface{}{
		"port": float64(9999),
	})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
		StringFlag("host", "hostname", Default("localhost")),
		BoolFlag("loud", "loud output", Default(false)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "show", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	var data map[string]struct {
		Value  interface{} `json:"value"`
		Source string      `json:"source"`
	}
	if err := json.Unmarshal(envelopePayload(t, r.Stdout), &data); err != nil {
		t.Fatalf("failed to parse JSON: %s\nstdout=%q", err, r.Stdout)
	}

	// port should be from config
	portEntry, ok := data["port"]
	if !ok {
		t.Fatal("expected 'port' key in JSON output")
	}
	if portEntry.Source != "config" {
		t.Fatalf("expected port source 'config', got %q", portEntry.Source)
	}
	// JSON numbers are float64
	if portEntry.Value.(float64) != 9999 {
		t.Fatalf("expected port value 9999, got %v", portEntry.Value)
	}

	// host should be default
	hostEntry, ok := data["host"]
	if !ok {
		t.Fatal("expected 'host' key in JSON output")
	}
	if hostEntry.Source != "default" {
		t.Fatalf("expected host source 'default', got %q", hostEntry.Source)
	}
	if hostEntry.Value.(string) != "localhost" {
		t.Fatalf("expected host value 'localhost', got %v", hostEntry.Value)
	}

	// loud (bool with no explicit default) should be default with false value
	verboseEntry, ok := data["loud"]
	if !ok {
		t.Fatal("expected 'loud' key in JSON output")
	}
	if verboseEntry.Source != "default" {
		t.Fatalf("expected loud source 'default', got %q", verboseEntry.Source)
	}

	// Keys must be sorted (check JSON string)
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			t.Fatalf("JSON keys not sorted: %v", keys)
		}
	}
}

func TestConfigShowNoFlagsError(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
	), WithEffect(EffectReadOnly))

	// The old "one of --plain, --json is required" mutex is gone with the local
	// --json flag: --json is framework-owned now (contract §7.5's sweep box),
	// so it cannot take part in a command-local mutex group.
	r := app.Test([]string{"config", "show"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port = ") {
		t.Fatalf("expected the human table, got %q", r.Stdout)
	}
}

func TestConfigShowBothFlagsError(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
	), WithEffect(EffectReadOnly))

	// --plain no longer competes with --json: machine mode wins.
	r := app.Test([]string{"config", "show", "--plain", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, `"source"`) {
		t.Fatalf("expected the machine payload, got %q", r.Stdout)
	}
}

func TestTomlConfigPath(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("tomlpathapp", "1.0.0", "test app", WithConfig(), WithConfigFormat("toml"))
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "path"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	expected := filepath.Join(tmpDir, "tomlpathapp", "config.toml")
	if !strings.Contains(r.Stdout, expected) {
		t.Fatalf("expected path %q in stdout, got %q", expected, r.Stdout)
	}
}

func TestTomlConfigSetRoundTrip(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	_ = tmpDir

	app := NewApp("tomlrtapp", "1.0.0", "test app", WithConfig(), WithConfigFormat("toml"))
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		StringFlag("name", "a name", Default("")),
	), WithEffect(EffectReadOnly))

	// Set a value via config set
	r := app.Test([]string{"config", "set", "name", "--value", "bob"})
	if r.ExitCode != 0 {
		t.Fatalf("config set failed: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}

	// Load the config and verify it round-trips
	configResult := loadConfig("tomlrtapp", "", "toml", false)
	if configResult.data["name"] != "bob" {
		t.Fatalf("expected name=bob from loadConfig, got %v", configResult.data["name"])
	}
}

func TestTomlConfigEmptyFile(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	// Write an empty TOML file
	writeTomlConfig(t, tmpDir, "tomlemptyapp", "")

	app := NewApp("tomlemptyapp", "1.0.0", "test app", WithConfig(), WithConfigFormat("toml"))
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("port=%d", args["port"]))
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
	), WithEffect(EffectReadOnly))

	// Empty TOML file should load without errors, using defaults
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=8080") {
		t.Fatalf("expected default value 8080, got %q", r.Stdout)
	}
}

func TestTomlConfigSetOverwrite(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("tomlowapp", "1.0.0", "test app", WithConfig(), WithConfigFormat("toml"))
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		StringFlag("color", "a color", Default("")),
	), WithEffect(EffectReadOnly))

	// Set initial value
	r := app.Test([]string{"config", "set", "color", "--value", "red"})
	if r.ExitCode != 0 {
		t.Fatalf("first config set failed: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}

	// Overwrite with new value
	r = app.Test([]string{"config", "set", "color", "--value", "blue"})
	if r.ExitCode != 0 {
		t.Fatalf("second config set failed: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}

	// Verify only the latest value persists
	path := filepath.Join(tmpDir, "tomlowapp", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file should exist: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "blue") {
		t.Fatalf("expected 'blue' in config file, got:\n%s", content)
	}
	if strings.Contains(content, "red") {
		t.Fatalf("'red' should have been overwritten, got:\n%s", content)
	}

	// Also verify via loadConfig
	configResult := loadConfig("tomlowapp", "", "toml", false)
	if configResult.data["color"] != "blue" {
		t.Fatalf("expected color=blue from loadConfig, got %v", configResult.data["color"])
	}
}

func TestTomlConfigSetWritesTypedValues(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("tomltypesapp", "1.0.0", "test app", WithConfig(), WithConfigFormat("toml"))
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		IntFlag("count", "a count", Default(0)),
		BoolFlag("debug", "enable debug", Default(false)),
		FloatFlag("rate", "a rate", Default(0.0)),
		StringFlag("name", "a name", Default("")),
	), WithEffect(EffectReadOnly))

	// Set each type via config set
	for _, tc := range []struct {
		key, val string
	}{
		{"count", "42"},
		{"debug", "true"},
		{"rate", "3.14"},
		{"name", "alice"},
	} {
		r := app.Test([]string{"config", "set", tc.key, "--value", tc.val})
		if r.ExitCode != 0 {
			t.Fatalf("config set %s %s failed: exit %d, stderr=%q", tc.key, tc.val, r.ExitCode, r.Stderr)
		}
	}

	// Read the raw TOML file from disk
	path := filepath.Join(tmpDir, "tomltypesapp", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file should exist: %v", err)
	}
	content := string(data)

	// Assert native TOML types (not stringified)
	mustContain := []string{
		"count = 42",
		"debug = true",
		"rate = 3.14",
		`name = "alice"`,
	}
	for _, want := range mustContain {
		if !strings.Contains(content, want) {
			t.Errorf("expected TOML file to contain %q, got:\n%s", want, content)
		}
	}

	// Assert values are NOT written as quoted strings
	mustNotContain := []string{
		`"42"`,
		`"true"`,
		`"3.14"`,
	}
	for _, bad := range mustNotContain {
		if strings.Contains(content, bad) {
			t.Errorf("TOML file should not contain stringified value %s, got:\n%s", bad, content)
		}
	}
}

func TestConfigSetNegativeInt(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("negintapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithFlags(
		IntFlag("count", "a count", Default(0)),
	), WithEffect(EffectReadOnly))

	path := filepath.Join(tmpDir, "negintapp", "config.json")

	r := app.Test([]string{"config", "set", "count", "--value", "-7"})
	if r.ExitCode != 0 {
		t.Fatalf("config set count -7: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	assertConfigValue(t, path, "count", float64(-7))
}

func TestConfigSetNegativeFloat(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := NewApp("negfloatapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithFlags(
		FloatFlag("rate", "a rate", Default(0.0)),
	), WithEffect(EffectReadOnly))

	path := filepath.Join(tmpDir, "negfloatapp", "config.json")

	r := app.Test([]string{"config", "set", "rate", "--value", "-3.14"})
	if r.ExitCode != 0 {
		t.Fatalf("config set rate -3.14: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	assertConfigValue(t, path, "rate", float64(-3.14))
}

// --- splitEscaped tests ---

func TestSplitEscapedBasic(t *testing.T) {
	got := splitEscaped("a,b,c", ',')
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitEscapedEscapedSep(t *testing.T) {
	got := splitEscaped("a\\,b,c", ',')
	want := []string{"a,b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitEscapedEmptyString(t *testing.T) {
	got := splitEscaped("", ',')
	if len(got) != 1 || got[0] != "" {
		t.Fatalf("got %v, want [\"\"]", got)
	}
}

func TestSplitEscapedSepOnly(t *testing.T) {
	got := splitEscaped(",", ',')
	want := []string{"", ""}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitEscapedSingleValue(t *testing.T) {
	got := splitEscaped("a", ',')
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("got %v, want [\"a\"]", got)
	}
}

func TestSplitEscapedEscapedBackslash(t *testing.T) {
	got := splitEscaped("a\\\\", ',')
	if len(got) != 1 || got[0] != "a\\\\" {
		t.Fatalf("got %v, want [\"a\\\\\\\\\"]", got)
	}
}

func TestSplitEscapedEscapedBackslashThenSep(t *testing.T) {
	got := splitEscaped("a\\\\,b", ',')
	want := []string{"a\\\\", "b"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitEscapedTrailingBackslash(t *testing.T) {
	got := splitEscaped("a\\", ',')
	if len(got) != 1 || got[0] != "a\\\\" {
		t.Fatalf("got %v, want [\"a\\\\\\\\\"]", got)
	}
}

func TestSplitEscapedMultipleEscapedSeps(t *testing.T) {
	got := splitEscaped("a\\,b\\,c", ',')
	if len(got) != 1 || got[0] != "a,b,c" {
		t.Fatalf("got %v, want [\"a,b,c\"]", got)
	}
}

func TestSplitEscapedDifferentSep(t *testing.T) {
	got := splitEscaped("a:b:c", ':')
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitEscapedDifferentSepEscaped(t *testing.T) {
	got := splitEscaped("a\\:b:c", ':')
	want := []string{"a:b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// --- findDuplicate tests ---

func TestFindDuplicateNone(t *testing.T) {
	got := findDuplicate([]interface{}{1, 2, 3})
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestFindDuplicateHasDup(t *testing.T) {
	got := findDuplicate([]interface{}{1, 2, 2, 3})
	if got != 2 {
		t.Fatalf("got %v, want 2", got)
	}
}

func TestFindDuplicateFirstDup(t *testing.T) {
	got := findDuplicate([]interface{}{1, 2, 3, 2, 1})
	if got != 2 {
		t.Fatalf("got %v, want 2", got)
	}
}

func TestFindDuplicateEmpty(t *testing.T) {
	got := findDuplicate([]interface{}{})
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestFindDuplicateSingleElement(t *testing.T) {
	got := findDuplicate([]interface{}{1})
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestFindDuplicateStrings(t *testing.T) {
	got := findDuplicate([]interface{}{"a", "b", "a"})
	if got != "a" {
		t.Fatalf("got %v, want \"a\"", got)
	}
}

func TestFindDuplicateAllSame(t *testing.T) {
	got := findDuplicate([]interface{}{5, 5, 5})
	if got != 5 {
		t.Fatalf("got %v, want 5", got)
	}
}

// --- formatValueForError tests ---

func TestFormatValueForErrorString(t *testing.T) {
	got := formatValueForError("hello")
	if got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestFormatValueForErrorInt(t *testing.T) {
	got := formatValueForError(3)
	if got != "3" {
		t.Fatalf("got %q, want %q", got, "3")
	}
}

func TestFormatValueForErrorFloatWhole(t *testing.T) {
	got := formatValueForError(float64(3))
	if got != "3.0" {
		t.Fatalf("got %q, want %q", got, "3.0")
	}
}

func TestFormatValueForErrorFloatFractional(t *testing.T) {
	got := formatValueForError(3.5)
	if got != "3.5" {
		t.Fatalf("got %q, want %q", got, "3.5")
	}
}

func TestFormatValueForErrorBoolTrue(t *testing.T) {
	got := formatValueForError(true)
	if got != "true" {
		t.Fatalf("got %q, want %q", got, "true")
	}
}

func TestFormatValueForErrorBoolFalse(t *testing.T) {
	got := formatValueForError(false)
	if got != "false" {
		t.Fatalf("got %q, want %q", got, "false")
	}
}

func TestFormatValueForErrorNegativeInt(t *testing.T) {
	got := formatValueForError(-7)
	if got != "-7" {
		t.Fatalf("got %q, want %q", got, "-7")
	}
}

func TestFormatValueForErrorNegativeFloat(t *testing.T) {
	got := formatValueForError(-3.14)
	if got != "-3.14" {
		t.Fatalf("got %q, want %q", got, "-3.14")
	}
}

func TestFormatValueForErrorZeroFloat(t *testing.T) {
	got := formatValueForError(float64(0))
	if got != "0.0" {
		t.Fatalf("got %q, want %q", got, "0.0")
	}
}

// --- typeName tests (extended with array) ---

func TestConfigTypeNameStr(t *testing.T) {
	if got := typeName("hello"); got != "str" {
		t.Fatalf("got %q, want %q", got, "str")
	}
}

func TestConfigTypeNameBool(t *testing.T) {
	if got := typeName(true); got != "bool" {
		t.Fatalf("got %q, want %q", got, "bool")
	}
}

func TestConfigTypeNameInt(t *testing.T) {
	if got := typeName(int64(42)); got != "int" {
		t.Fatalf("got %q, want %q", got, "int")
	}
}

func TestConfigTypeNameFloat(t *testing.T) {
	if got := typeName(float64(3.14)); got != "float" {
		t.Fatalf("got %q, want %q", got, "float")
	}
}

func TestConfigTypeNameNull(t *testing.T) {
	if got := typeName(nil); got != "null" {
		t.Fatalf("got %q, want %q", got, "null")
	}
}

func TestConfigTypeNameArray(t *testing.T) {
	if got := typeName([]interface{}{1, 2, 3}); got != "array" {
		t.Fatalf("got %q, want %q", got, "array")
	}
}

func TestConfigTypeNameBoolFalse(t *testing.T) {
	if got := typeName(false); got != "bool" {
		t.Fatalf("got %q, want %q", got, "bool")
	}
}

// --- Unique tests ---

func TestUniqueRequiresRepeatable(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for Unique on non-repeatable flag, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `unique requires repeatable=True`) {
			t.Fatalf("panic message should mention unique requires repeatable=True, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("tag", "a tag", Unique(true), Required())), WithEffect(EffectReadOnly))
}

func TestRepeatableRequiresExplicitUnique(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for Repeatable without Unique, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `repeatable requires explicit unique (unique=True or unique=False)`) {
			t.Fatalf("panic message should mention explicit unique, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Default([]interface{}{}))), WithEffect(EffectReadOnly))
}

// --- EnvSeparator tests ---

func TestEnvSeparatorRequiresRepeatable(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for EnvSeparator on non-repeatable flag, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `env_separator requires repeatable=True`) {
			t.Fatalf("panic message should mention env_separator requires repeatable=True, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("tag", "a tag", Env("MYAPP_TAG"), EnvSeparator(","), Required())), WithEffect(EffectReadOnly))
}

func TestEnvSeparatorRequiresEnv(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for EnvSeparator without Env, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `env_separator requires env`) {
			t.Fatalf("panic message should mention env_separator requires env, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))
}

func TestRepeatableEnvRequiresSeparator(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for repeatable+env without EnvSeparator, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `repeatable flag with env requires env_separator`) {
			t.Fatalf("panic message should mention env_separator requirement, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Env("MYAPP_TAG"), Default([]interface{}{}))), WithEffect(EffectReadOnly))
}

func TestEnvSeparatorMustBeSingleChar(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for multi-char EnvSeparator, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `env_separator must be a single character`) {
			t.Fatalf("panic message should mention single character, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Env("MYAPP_TAG"), EnvSeparator("::"), Default([]interface{}{}))), WithEffect(EffectReadOnly))
}

func TestEnvSeparatorCannotBeBackslash(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for backslash EnvSeparator, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, `env_separator cannot be a backslash`) {
			t.Fatalf("panic message should mention backslash, got %q", msg)
		}
	}()

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Env("MYAPP_TAG"), EnvSeparator("\\"), Default([]interface{}{}))), WithEffect(EffectReadOnly))
}

// --- Unique help text and enforcement tests ---

func TestUniqueHelpText(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(true), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	// [unique] must appear after [repeatable]
	if !strings.Contains(r.Stdout, "[repeatable] [unique]") {
		t.Fatalf("stdout should contain '[repeatable] [unique]', got %q", r.Stdout)
	}
}

func TestUniqueHelpTextNotShownWhenFalse(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if strings.Contains(r.Stdout, "[unique]") {
		t.Fatalf("stdout should NOT contain '[unique]' when unique=false, got %q", r.Stdout)
	}
}

// --- Env separator help text test ---

func TestEnvSeparatorHelpText(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Env("MY_TAGS"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "[env: MY_TAGS (sep: ,)]") {
		t.Fatalf("stdout should contain '[env: MY_TAGS (sep: ,)]', got %q", r.Stdout)
	}
}

// --- Unique CLI enforcement tests ---

func TestUniqueDuplicateError(t *testing.T) {
	app := simpleApp("cmd", "a command", "tags={tag}",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(true), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--tag", "a", "--tag", "a"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--tag: duplicate value 'a'") {
		t.Fatalf("stderr should contain '--tag: duplicate value \\'a\\'', got %q", r.Stderr)
	}
}

func TestUniqueDistinctValues(t *testing.T) {
	app := simpleApp("cmd", "a command", "tags={tag}",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(true), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--tag", "a", "--tag", "b"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=a,b") {
		t.Fatalf("stdout should contain 'tags=a,b', got %q", r.Stdout)
	}
}

func TestUniqueIntDedup(t *testing.T) {
	app := simpleApp("cmd", "a command", "counts={count}",
		WithFlags(IntFlag("count", "a count", Repeatable(), Unique(true), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--count", "1", "--count", "1"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--count: duplicate value '1'") {
		t.Fatalf("stderr should contain '--count: duplicate value \\'1\\'', got %q", r.Stderr)
	}
}

func TestUniqueGlobalFlag(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(StringFlag("tag", "a tag", Repeatable(), Unique(true), Default([]interface{}{})))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	// Distinct values should succeed
	r := app.Test([]string{"--tag", "a", "--tag", "b", "cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=a,b") {
		t.Fatalf("stdout should contain 'tags=a,b', got %q", r.Stdout)
	}
	// Duplicate values should fail
	app2 := NewApp("myapp", "1.0.0", "test app")
	app2.GlobalFlag(StringFlag("tag", "a tag", Repeatable(), Unique(true), Default([]interface{}{})))
	app2.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r2 := app2.Test([]string{"--tag", "a", "--tag", "a", "cmd"})
	if r2.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r2.ExitCode)
	}
	if !strings.Contains(r2.Stderr, "--tag: duplicate value 'a'") {
		t.Fatalf("stderr should contain '--tag: duplicate value \\'a\\'', got %q", r2.Stderr)
	}
}

// --- config array coercion for repeatable flags ---

func TestConfigArrayForRepeatableString(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "arrstrapp", map[string]interface{}{
		"tags": []interface{}{"a", "b", "c"},
	})
	app := NewApp("arrstrapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("val=" + formatValue(args["tags"]))
		return Exit(0)
	}, WithFlags(StringFlag("tags", "the tags", Repeatable(), Unique(false), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "val=a,b,c") {
		t.Fatalf("expected val=a,b,c, got %q", r.Stdout)
	}
}

func TestConfigArrayForRepeatableInt(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "arrintapp", map[string]interface{}{
		"nums": []interface{}{float64(1), float64(2), float64(3)},
	})
	app := NewApp("arrintapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("val=" + formatValue(args["nums"]))
		return Exit(0)
	}, WithFlags(IntFlag("nums", "the nums", Repeatable(), Unique(false), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "val=1,2,3") {
		t.Fatalf("expected val=1,2,3, got %q", r.Stdout)
	}
}

func TestConfigArrayForRepeatableFloat(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "arrfloatapp", map[string]interface{}{
		"rates": []interface{}{1.5, 2.5},
	})
	app := NewApp("arrfloatapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("val=" + formatValue(args["rates"]))
		return Exit(0)
	}, WithFlags(FloatFlag("rates", "the rates", Repeatable(), Unique(false), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "val=1.5,2.5") {
		t.Fatalf("expected val=1.5,2.5, got %q", r.Stdout)
	}
}

func TestConfigArrayForNonRepeatableError(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "arrscalarapp", map[string]interface{}{
		"target": []interface{}{"a", "b"},
	})
	app := NewApp("arrscalarapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("target", "the target", Default("x"))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "expected scalar, got array") {
		t.Fatalf("expected 'expected scalar, got array' in stderr, got %q", r.Stderr)
	}
}

func TestConfigScalarForRepeatableError(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "screpapp", map[string]interface{}{
		"tags": "single",
	})
	app := NewApp("screpapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("tags", "the tags", Repeatable(), Unique(false), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "expected array for repeatable flag") {
		t.Fatalf("expected 'expected array for repeatable flag' in stderr, got %q", r.Stderr)
	}
}

func TestConfigArrayBadElementType(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "arrbadapp", map[string]interface{}{
		"tags": []interface{}{"a", float64(123)},
	})
	app := NewApp("arrbadapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("tags", "the tags", Repeatable(), Unique(false), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "element 1: expected str, got int") {
		t.Fatalf("expected 'element 1: expected str, got int' in stderr, got %q", r.Stderr)
	}
}

func TestConfigEmptyArray(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "arremptyapp", map[string]interface{}{
		"tags": []interface{}{},
	})
	app := NewApp("arremptyapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("val=" + formatValue(args["tags"]))
		return Exit(0)
	}, WithFlags(StringFlag("tags", "the tags", Repeatable(), Unique(false), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "val=") {
		t.Fatalf("expected val= in stdout, got %q", r.Stdout)
	}
	// Empty array should produce empty string from formatValue
	if r.Stdout != "val=\n" {
		t.Fatalf("expected exactly 'val=', got %q", r.Stdout)
	}
}

func TestConfigSingleElementArray(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "arroneapp", map[string]interface{}{
		"tags": []interface{}{"a"},
	})
	app := NewApp("arroneapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("val=" + formatValue(args["tags"]))
		return Exit(0)
	}, WithFlags(StringFlag("tags", "the tags", Repeatable(), Unique(false), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "val=a") {
		t.Fatalf("expected val=a, got %q", r.Stdout)
	}
}

func TestConfigArrayPrecedenceCLIWins(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "arrprecapp", map[string]interface{}{
		"tags": []interface{}{"a", "b", "c"},
	})
	app := NewApp("arrprecapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("val=" + formatValue(args["tags"]))
		return Exit(0)
	}, WithFlags(StringFlag("tags", "the tags", Repeatable(), Unique(false), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run", "--tags", "x", "--tags", "y"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "val=x,y") {
		t.Fatalf("expected val=x,y, got %q", r.Stdout)
	}
}

// --- config array unique enforcement ---

func TestConfigUniqueEnforcement(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "cfguniqapp", map[string]interface{}{
		"tags": []interface{}{"a", "b", "a"},
	})
	app := NewApp("cfguniqapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("tags", "the tags", Repeatable(), Unique(true), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "duplicate value 'a'") {
		t.Fatalf("expected 'duplicate value' in stderr, got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "config value error") {
		t.Fatalf("expected 'config value error' in stderr, got %q", r.Stderr)
	}
}

func TestConfigUniqueNoDuplicates(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "cfguniqokapp", map[string]interface{}{
		"tags": []interface{}{"a", "b", "c"},
	})
	app := NewApp("cfguniqokapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("val=" + formatValue(args["tags"]))
		return Exit(0)
	}, WithFlags(StringFlag("tags", "the tags", Repeatable(), Unique(true), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "val=a,b,c") {
		t.Fatalf("expected val=a,b,c, got %q", r.Stdout)
	}
}

func TestConfigShowPlainArray(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "cfgshowapp", map[string]interface{}{
		"tags": []interface{}{"a", "b", "c"},
	})
	app := NewApp("cfgshowapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("tags", "the tags", Repeatable(), Unique(false), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, `tags = ["a", "b", "c"]  (source: config)`) {
		t.Fatalf("expected array display in stdout, got %q", r.Stdout)
	}
}

func TestConfigShowJSONArray(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "cfgjsonshowapp", map[string]interface{}{
		"tags": []interface{}{"x", "y"},
	})
	app := NewApp("cfgjsonshowapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("tags", "the tags", Repeatable(), Unique(false), Default([]interface{}{}))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"config", "show", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	var data map[string]struct {
		Value  interface{} `json:"value"`
		Source string      `json:"source"`
	}
	if err := json.Unmarshal(envelopePayload(t, r.Stdout), &data); err != nil {
		t.Fatalf("failed to parse JSON: %s\nstdout=%q", err, r.Stdout)
	}
	entry, ok := data["tags"]
	if !ok {
		t.Fatal("expected 'tags' key in JSON output")
	}
	if entry.Source != "config" {
		t.Fatalf("expected source 'config', got %q", entry.Source)
	}
	arr, ok := entry.Value.([]interface{})
	if !ok {
		t.Fatalf("expected array value, got %T", entry.Value)
	}
	if len(arr) != 2 || arr[0].(string) != "x" || arr[1].(string) != "y" {
		t.Fatalf("expected [x, y], got %v", arr)
	}
}

func TestConfigUniqueEnforcementGlobalFlag(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "cfgglbuniqapp", map[string]interface{}{
		"tags": []interface{}{"a", "b", "a"},
	})
	app := NewApp("cfgglbuniqapp", "1.0.0", "test app", WithConfig())
	app.GlobalFlag(StringFlag("tags", "the tags", Repeatable(), Unique(true), Default([]interface{}{})))
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "duplicate value 'a'") {
		t.Fatalf("expected 'duplicate value' in stderr, got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "config value error") {
		t.Fatalf("expected 'config value error' in stderr, got %q", r.Stderr)
	}
}

// --- Env separator parsing tests ---

func TestEnvSeparatorSplitsValue(t *testing.T) {
	os.Setenv("TAGS", "a,b,c")
	defer os.Unsetenv("TAGS")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Env("TAGS"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=a,b,c") {
		t.Fatalf("stdout should contain 'tags=a,b,c', got %q", r.Stdout)
	}
}

func TestEnvSeparatorEscapedSeparator(t *testing.T) {
	os.Setenv("TAGS", "a\\,b,c")
	defer os.Unsetenv("TAGS")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Env("TAGS"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=a,b,c") {
		t.Fatalf("stdout should contain 'tags=a,b,c', got %q", r.Stdout)
	}
}

func TestEnvSeparatorSingleValue(t *testing.T) {
	os.Setenv("TAGS", "a")
	defer os.Unsetenv("TAGS")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Env("TAGS"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=a") {
		t.Fatalf("stdout should contain 'tags=a', got %q", r.Stdout)
	}
}

func TestEnvSeparatorIntCoercion(t *testing.T) {
	os.Setenv("COUNTS", "1,2,3")
	defer os.Unsetenv("COUNTS")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("counts=" + formatValue(args["count"]))
		return Exit(0)
	}, WithFlags(IntFlag("count", "a count", Repeatable(), Unique(false), Env("COUNTS"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "counts=1,2,3") {
		t.Fatalf("stdout should contain 'counts=1,2,3', got %q", r.Stdout)
	}
}

func TestEnvSeparatorIntCoercionError(t *testing.T) {
	os.Setenv("COUNTS", "1,abc,3")
	defer os.Unsetenv("COUNTS")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(IntFlag("count", "a count", Repeatable(), Unique(false), Env("COUNTS"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--count: expected integer, got 'abc' (from env var 'COUNTS')") {
		t.Fatalf("stderr should contain per-element int error, got %q", r.Stderr)
	}
}

func TestEnvSeparatorUniqueDuplicateError(t *testing.T) {
	os.Setenv("TAGS", "a,b,a")
	defer os.Unsetenv("TAGS")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(true), Env("TAGS"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--tag: duplicate value 'a' (from env var 'TAGS')") {
		t.Fatalf("stderr should contain duplicate error, got %q", r.Stderr)
	}
}

func TestEnvSeparatorUniqueNoDuplicate(t *testing.T) {
	os.Setenv("TAGS", "a,b,c")
	defer os.Unsetenv("TAGS")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(true), Env("TAGS"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=a,b,c") {
		t.Fatalf("stdout should contain 'tags=a,b,c', got %q", r.Stdout)
	}
}

func TestEnvSeparatorCliOverridesEnv(t *testing.T) {
	os.Setenv("TAGS", "x,y,z")
	defer os.Unsetenv("TAGS")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Env("TAGS"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd", "--tag", "from-cli"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=from-cli") {
		t.Fatalf("stdout should contain 'tags=from-cli', got %q", r.Stdout)
	}
}

func TestEnvSeparatorColonSeparator(t *testing.T) {
	os.Setenv("PATHS", "/usr/bin:/usr/local/bin:/home/user/bin")
	defer os.Unsetenv("PATHS")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("paths=" + formatValue(args["path"]))
		return Exit(0)
	}, WithFlags(StringFlag("path", "a path", Repeatable(), Unique(false), Env("PATHS"), Prefixed(false), EnvSeparator(":"), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "paths=/usr/bin,/usr/local/bin,/home/user/bin") {
		t.Fatalf("stdout should contain paths, got %q", r.Stdout)
	}
}

func TestEnvSeparatorGlobalFlag(t *testing.T) {
	os.Setenv("TAGS", "a,b,c")
	defer os.Unsetenv("TAGS")

	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(StringFlag("tag", "a tag", Repeatable(), Unique(false), Env("TAGS"), Prefixed(false), EnvSeparator(","), Default([]interface{}{})))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=a,b,c") {
		t.Fatalf("stdout should contain 'tags=a,b,c', got %q", r.Stdout)
	}
}

func TestEnvSeparatorFloatCoercion(t *testing.T) {
	os.Setenv("RATES", "1.5,2.5,3.5")
	defer os.Unsetenv("RATES")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("rates=" + formatValue(args["rate"]))
		return Exit(0)
	}, WithFlags(FloatFlag("rate", "a rate", Repeatable(), Unique(false), Env("RATES"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "rates=1.5,2.5,3.5") {
		t.Fatalf("stdout should contain 'rates=1.5,2.5,3.5', got %q", r.Stdout)
	}
}

func TestEnvSeparatorFloatCoercionError(t *testing.T) {
	os.Setenv("RATES", "1.5,abc")
	defer os.Unsetenv("RATES")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(FloatFlag("rate", "a rate", Repeatable(), Unique(false), Env("RATES"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--rate: expected float, got 'abc' (from env var 'RATES')") {
		t.Fatalf("stderr should contain per-element float error, got %q", r.Stderr)
	}
}

func TestEnvSeparatorFloatNanError(t *testing.T) {
	os.Setenv("RATES", "1.5,NaN")
	defer os.Unsetenv("RATES")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(FloatFlag("rate", "a rate", Repeatable(), Unique(false), Env("RATES"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--rate: NaN is not allowed (from env var 'RATES')") {
		t.Fatalf("stderr should contain NaN error, got %q", r.Stderr)
	}
}

func TestEnvSeparatorAtPrefixPerElement(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "input.txt")
	os.WriteFile(tmpFile, []byte("from-file"), 0644)

	os.Setenv("TAGS", "@"+tmpFile+",literal-value")
	defer os.Unsetenv("TAGS")

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tag=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Env("TAGS"), Prefixed(false), EnvSeparator(","), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tag=from-file,literal-value") {
		t.Fatalf("stdout should contain 'tag=from-file,literal-value', got %q", r.Stdout)
	}
}

// --- Config set: repeatable flag support + --clear and --default ---

// configSetApp creates a test app with repeatable and scalar flags for config set tests.
func configSetApp() *App {
	app := NewApp("repapp", "1.0.0", "test app with repeatable flags", WithConfig())
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		StringFlag("tags", "tags", Repeatable(), Unique(false), Default([]interface{}{})),
		IntFlag("counts", "counts", Repeatable(), Unique(false), Default([]interface{}{})),
		FloatFlag("rates", "rates", Repeatable(), Unique(false), Default([]interface{}{})),
		IntFlag("ids", "unique ids", Repeatable(), Unique(true), Default([]interface{}{})),
		StringFlag("name", "name", Default("default")),
	), WithEffect(EffectReadOnly))
	return app
}

func TestConfigSetRepeatableString(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "tags", "--value", "a,b,c"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	path := filepath.Join(tmpDir, "repapp", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file should exist: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	arr, ok := config["tags"].([]interface{})
	if !ok {
		t.Fatalf("tags should be an array, got %T", config["tags"])
	}
	if len(arr) != 3 || arr[0] != "a" || arr[1] != "b" || arr[2] != "c" {
		t.Fatalf("expected [a,b,c], got %v", arr)
	}
}

func TestConfigSetRepeatableInt(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "counts", "--value", "1,2,3"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	path := filepath.Join(tmpDir, "repapp", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file should exist: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	arr, ok := config["counts"].([]interface{})
	if !ok {
		t.Fatalf("counts should be an array, got %T", config["counts"])
	}
	if len(arr) != 3 || arr[0] != float64(1) || arr[1] != float64(2) || arr[2] != float64(3) {
		t.Fatalf("expected [1,2,3], got %v", arr)
	}
}

func TestConfigSetRepeatableFloat(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "rates", "--value", "1.5,2.5,3.0"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	path := filepath.Join(tmpDir, "repapp", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file should exist: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	arr, ok := config["rates"].([]interface{})
	if !ok {
		t.Fatalf("rates should be an array, got %T", config["rates"])
	}
	if len(arr) != 3 || arr[0] != float64(1.5) || arr[1] != float64(2.5) || arr[2] != float64(3.0) {
		t.Fatalf("expected [1.5,2.5,3.0], got %v", arr)
	}
}

func TestConfigSetEscapedComma(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "tags", "--value", `a\,b,c`})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	path := filepath.Join(tmpDir, "repapp", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file should exist: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	arr, ok := config["tags"].([]interface{})
	if !ok {
		t.Fatalf("tags should be an array, got %T", config["tags"])
	}
	if len(arr) != 2 || arr[0] != "a,b" || arr[1] != "c" {
		t.Fatalf("expected [a,b c], got %v", arr)
	}
}

func TestConfigSetRepeatableUniqueValid(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "ids", "--value", "1,2,3"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestConfigSetRepeatableUniqueDuplicate(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "ids", "--value", "1,2,1"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config set: key 'ids': duplicate value '1'") {
		t.Fatalf("expected duplicate error, got %q", r.Stderr)
	}
}

func TestConfigSetRoundTripJSON(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "tags", "--value", "x,y"})
	if r.ExitCode != 0 {
		t.Fatalf("config set failed: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}

	r = app.Test([]string{"config", "show", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("config show failed: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	var result map[string]struct {
		Value  interface{} `json:"value"`
		Source string      `json:"source"`
	}
	if err := json.Unmarshal(envelopePayload(t, r.Stdout), &result); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	entry, ok := result["tags"]
	if !ok {
		t.Fatal("tags key missing from config show output")
	}
	if entry.Source != "config" {
		t.Fatalf("expected source=config, got %q", entry.Source)
	}
	arr, ok := entry.Value.([]interface{})
	if !ok {
		t.Fatalf("tags value should be array, got %T", entry.Value)
	}
	if len(arr) != 2 || arr[0] != "x" || arr[1] != "y" {
		t.Fatalf("expected [x,y], got %v", arr)
	}
}

func TestConfigSetRoundTripTOML(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	configFile := filepath.Join(tmpDir, "config.toml")
	app := NewApp("reptoml", "1.0.0", "test app", WithConfig(),
		WithConfigFormat("toml"), WithConfigPath(configFile))
	app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		StringFlag("tags", "tags", Repeatable(), Unique(false), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "set", "tags", "--value", "a,b"})
	if r.ExitCode != 0 {
		t.Fatalf("config set failed: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}

	r = app.Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("config show failed: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, `tags = ["a", "b"]  (source: config)`) {
		t.Fatalf("expected tags array in plain output, got %q", r.Stdout)
	}
}

func TestConfigSetClearRepeatable(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	writeConfig(t, tmpDir, "repapp", map[string]interface{}{
		"tags": []interface{}{"a", "b"},
	})

	app := configSetApp()
	r := app.Test([]string{"config", "set", "tags", "--clear"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	path := filepath.Join(tmpDir, "repapp", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file should exist: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	arr, ok := config["tags"].([]interface{})
	if !ok {
		t.Fatalf("tags should be an array, got %T", config["tags"])
	}
	if len(arr) != 0 {
		t.Fatalf("expected empty array, got %v", arr)
	}
}

func TestConfigSetClearScalarError(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "name", "--clear"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config set: --clear is only for repeatable flags") {
		t.Fatalf("expected scalar error, got %q", r.Stderr)
	}
}

func TestConfigSetDefaultRemovesKey(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	writeConfig(t, tmpDir, "repapp", map[string]interface{}{
		"name": "alice",
		"tags": []interface{}{"x"},
	})

	app := configSetApp()
	r := app.Test([]string{"config", "set", "name", "--default"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}

	path := filepath.Join(tmpDir, "repapp", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file should exist: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, exists := config["name"]; exists {
		t.Fatal("name key should have been removed from config")
	}
	// Other keys preserved
	if _, exists := config["tags"]; !exists {
		t.Fatal("tags key should still exist")
	}
}

func TestConfigSetDefaultNonexistentKeyError(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "name", "--default"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config set: key 'name' not in config") {
		t.Fatalf("expected not-in-config error, got %q", r.Stderr)
	}
}

// The three guards below are gone as GUARDS: `config set`'s write is an
// exactly-one selection over a value, a clear and a reset to default (contract
// §27.1, §18.33 item 304), so electing none and electing two are refused by the
// framework's own selector machinery, in its own sentences, before the handler
// runs. What used to be three hand-rolled checks is now the declaration.

func TestConfigSetElectingNothingIsRefused(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "name"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "one of --value, --clear, --default is required") {
		t.Fatalf("expected the unsatisfied-selector refusal, got %q", r.Stderr)
	}
}

func TestConfigSetValueWithClearIsRefused(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "tags", "--value", "a,b", "--clear"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--value and --clear are mutually exclusive") {
		t.Fatalf("expected the two-members refusal, got %q", r.Stderr)
	}
}

func TestConfigSetValueWithDefaultIsRefused(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "name", "--value", "alice", "--default"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--value and --default are mutually exclusive") {
		t.Fatalf("expected the two-members refusal, got %q", r.Stderr)
	}
}

func TestConfigSetClearAndDefaultIsRefused(t *testing.T) {
	_, cleanup := configTestSetup(t)
	defer cleanup()

	app := configSetApp()
	r := app.Test([]string{"config", "set", "tags", "--clear", "--default"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--clear and --default are mutually exclusive") {
		t.Fatalf("expected the two-members refusal, got %q", r.Stderr)
	}
}

// --- Repeatable default tests ---

func TestRepeatableDefaultApplied(t *testing.T) {
	// Register a repeatable string flag with a non-empty default
	// Run with no CLI args, no env, no config
	// Handler should receive ["a", "b"] (the declared default)
	// BUG: currently receives [] (empty slice)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{"a", "b"}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "tags=a,b\n" {
		t.Fatalf("expected stdout 'tags=a,b', got %q", r.Stdout)
	}
}

func TestRepeatableDefaultOverriddenByCLI(t *testing.T) {
	// CLI values completely replace the default
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{"a", "b"}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd", "--tag", "x", "--tag", "y"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "tags=x,y\n" {
		t.Fatalf("expected stdout 'tags=x,y', got %q", r.Stdout)
	}
}

func TestRepeatableDefaultOverriddenByEnv(t *testing.T) {
	// Env value replaces the default
	os.Setenv("MYAPP_TAG", "fromenv1,fromenv2")
	defer os.Unsetenv("MYAPP_TAG")

	app := NewApp("myapp", "1.0.0", "test app", WithEnvPrefix("MYAPP"))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Env("MYAPP_TAG"), EnvSeparator(","), Default([]interface{}{"a", "b"}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "tags=fromenv1,fromenv2\n" {
		t.Fatalf("expected stdout 'tags=fromenv1,fromenv2', got %q", r.Stdout)
	}
}

func TestRepeatableDefaultGlobalFlag(t *testing.T) {
	// Global flag with default, handler receives it
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{"x", "y"})))
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "tags=x,y\n" {
		t.Fatalf("expected stdout 'tags=x,y', got %q", r.Stdout)
	}
}

func TestRepeatableDefaultNotMutated(t *testing.T) {
	// Run the command twice, verify the second run still gets the original default
	// (copy prevents mutation)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		vals := args["tag"].([]interface{})
		ctx.Out("tags=" + formatValue(vals))
		// Attempt to mutate the slice
		if len(vals) > 0 {
			vals[0] = "MUTATED"
		}
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{"a", "b"}))), WithEffect(EffectReadOnly))

	r1 := app.Test([]string{"cmd"})
	if r1.ExitCode != 0 {
		t.Fatalf("run 1: expected exit 0, got %d: stderr=%q", r1.ExitCode, r1.Stderr)
	}
	if r1.Stdout != "tags=a,b\n" {
		t.Fatalf("run 1: expected stdout 'tags=a,b', got %q", r1.Stdout)
	}

	r2 := app.Test([]string{"cmd"})
	if r2.ExitCode != 0 {
		t.Fatalf("run 2: expected exit 0, got %d: stderr=%q", r2.ExitCode, r2.Stderr)
	}
	if r2.Stdout != "tags=a,b\n" {
		t.Fatalf("run 2: expected stdout 'tags=a,b', got %q (mutation leaked)", r2.Stdout)
	}
}

// --- Repeatable flag default validation ---

func TestRepeatableDefaultMustBeSlice(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		msg := fmt.Sprintf("%v", r)
		if msg != `Flag "tag": repeatable flag default must be a list` {
			t.Fatalf("unexpected panic: %s", msg)
		}
	}()
	app := NewApp("test", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default("not a slice"))), WithEffect(EffectReadOnly))
}

func TestRepeatableExplicitEmptyDefaultIsLegal(t *testing.T) {
	// An explicit empty collection is a DECLARATION now, not a redundancy
	// (contract §23.5): it registers, and absence delivers the declared [].
	var got interface{}
	var seen bool
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		got, seen = args["tag"], true
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{}))),
		WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if !seen {
		t.Fatal("handler did not run")
	}
	slice, ok := got.([]interface{})
	if !ok || len(slice) != 0 {
		t.Fatalf("expected the declared empty list, got %#v", got)
	}
}

func TestRepeatableDefaultWrongElementTypeStr(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		msg := fmt.Sprintf("%v", r)
		if msg != `Flag "tag": default element 0 is not of type str` {
			t.Fatalf("unexpected panic: %s", msg)
		}
	}()
	app := NewApp("test", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{1}))), WithEffect(EffectReadOnly))
}

func TestRepeatableDefaultWrongElementTypeInt(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		msg := fmt.Sprintf("%v", r)
		if msg != `Flag "port": default element 0 is not of type int` {
			t.Fatalf("unexpected panic: %s", msg)
		}
	}()
	app := NewApp("test", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(IntFlag("port", "a port", Repeatable(), Unique(false), Default([]interface{}{"x"}))), WithEffect(EffectReadOnly))
}

func TestRepeatableDefaultIntCoercedToFloat(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		vals := args["rate"].([]interface{})
		for _, v := range vals {
			if _, ok := v.(float64); !ok {
				ctx.Out(fmt.Sprintf("not float64: %T\n", v))
				return Exit(1)
			}
		}
		ctx.Out("ok")
		return Exit(0)
	}, WithFlags(FloatFlag("rate", "a rate", Repeatable(), Unique(false), Default([]interface{}{1, 2}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q stdout=%q", r.ExitCode, r.Stderr, r.Stdout)
	}
	if r.Stdout != "ok\n" {
		t.Fatalf("expected 'ok', got %q", r.Stdout)
	}
}

func TestRepeatableDefaultValid(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("tags=" + formatValue(args["tag"]))
		return Exit(0)
	}, WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{"x", "y"}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "tags=x,y\n" {
		t.Fatalf("expected 'tags=x,y', got %q", r.Stdout)
	}
}

func TestRepeatableDefaultShownInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{"x", "y"}))))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "[default: x, y]") {
		t.Fatalf("stdout should contain '[default: x, y]', got %q", r.Stdout)
	}
}

func TestRepeatableEmptyDefaultInHelp(t *testing.T) {
	// A declared empty collection renders [default: []]: it is a declaration,
	// and every line carries exactly one presence part (contract §23.8).
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "[default: []]") {
		t.Fatalf("stdout should contain '[default: []]', got %q", r.Stdout)
	}
}

func TestRepeatableOptionalInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false), Optional())))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "[optional]") {
		t.Fatalf("stdout should contain '[optional]', got %q", r.Stdout)
	}
}

func TestRepeatableDefaultIntInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(IntFlag("count", "a count", Repeatable(), Unique(false), Default([]interface{}{1, 2, 3}))))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "[default: 1, 2, 3]") {
		t.Fatalf("stdout should contain '[default: 1, 2, 3]', got %q", r.Stdout)
	}
}

func TestRepeatableDefaultFloatInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(FloatFlag("score", "a score", Repeatable(), Unique(false), Default([]interface{}{1.5, 2.5}))))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "[default: 1.5, 2.5]") {
		t.Fatalf("stdout should contain '[default: 1.5, 2.5]', got %q", r.Stdout)
	}
}

// --- Tag storage and validation ---

func TestWithTagsStoresTags(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithTags("json"), WithEffect(EffectReadOnly))
	cmd := app.Commands()["cmd"]
	if len(cmd.tags) != 1 || cmd.tags[0] != "json" {
		t.Fatalf("expected tags [json], got %v", cmd.tags)
	}
}

func TestWithTagsInvalidPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for invalid tag, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "invalid tag name") {
			t.Fatalf("expected panic about invalid tag name, got %q", msg)
		}
	}()
	NewApp("myapp", "1.0.0", "test app").Command("cmd", "a command",
		func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithTags("INVALID"), WithEffect(EffectReadOnly))
}

func TestWithTagsDeduplicates(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithTags("json", "json"), WithEffect(EffectReadOnly))
	cmd := app.Commands()["cmd"]
	if len(cmd.tags) != 1 {
		t.Fatalf("expected 1 tag after dedup, got %d: %v", len(cmd.tags), cmd.tags)
	}
}

func TestWithTagsValidNames(t *testing.T) {
	// These should all be valid tag names
	validTags := []string{"json", "xml", "ab", "abc-def", "a1", "tag-with-numbers-123"}
	for _, tag := range validTags {
		app := NewApp("myapp", "1.0.0", "test app")
		app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
			WithTags(tag), WithEffect(EffectReadOnly))
		cmd := app.Commands()["cmd"]
		found := false
		for _, ct := range cmd.tags {
			if ct == tag {
				found = true
			}
		}
		if !found {
			t.Fatalf("tag %q should be stored", tag)
		}
	}
}

func TestWithTagsInvalidNames(t *testing.T) {
	invalidTags := []string{"", "A", "1abc", "-abc", "abc_def", "abc def"}
	for _, tag := range invalidTags {
		func() {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic for invalid tag %q, got none", tag)
				}
			}()
			NewApp("myapp", "1.0.0", "test app").Command("cmd", "a command",
				func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
				WithTags(tag), WithEffect(EffectReadOnly))
		}()
	}
}

// --- Group inheritance ---

func TestGroupTagInheritance(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("grp", "a group", "json")
	g.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	cmd := g.Commands["cmd"]
	found := false
	for _, tag := range cmd.tags {
		if tag == "json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("command should inherit group tag 'json', got %v", cmd.tags)
	}
}

func TestNestedGroupTagCascade(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("outer", "outer group", "json")
	inner := g.Group("inner", "inner group", "xml")
	inner.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	cmd := inner.Commands["cmd"]
	hasJson := false
	hasXml := false
	for _, tag := range cmd.tags {
		if tag == "json" {
			hasJson = true
		}
		if tag == "xml" {
			hasXml = true
		}
	}
	if !hasJson || !hasXml {
		t.Fatalf("command should inherit both json and xml tags, got %v", cmd.tags)
	}
}

func TestCommandAndGroupTagsMerge(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("grp", "a group", "json")
	g.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithTags("xml"), WithEffect(EffectReadOnly))
	cmd := g.Commands["cmd"]
	hasJson := false
	hasXml := false
	for _, tag := range cmd.tags {
		if tag == "json" {
			hasJson = true
		}
		if tag == "xml" {
			hasXml = true
		}
	}
	if !hasJson || !hasXml {
		t.Fatalf("command should have both json and xml tags, got %v", cmd.tags)
	}
}

func TestGroupOwnTagsOnly(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("outer", "outer group", "json")
	inner := g.Group("inner", "inner group", "xml")
	inner.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	// inner's own tags should be just ["xml"], not ["json", "xml"]
	if len(inner.tags) != 1 || inner.tags[0] != "xml" {
		t.Fatalf("inner group own tags should be [xml], got %v", inner.tags)
	}
	// But inner's accumulated tags should include both
	hasJson := false
	hasXml := false
	for _, tag := range inner.accumulatedTags {
		if tag == "json" {
			hasJson = true
		}
		if tag == "xml" {
			hasXml = true
		}
	}
	if !hasJson || !hasXml {
		t.Fatalf("inner group accumulated tags should have both json and xml, got %v", inner.accumulatedTags)
	}
}

// --- Tag contracts ---

func TestTagContractSatisfied(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.TagContract("json", "as-json")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("ok")
		return Exit(0)
	}, WithTags("json"), WithFlags(BoolFlag("as-json", "output json", Default(false))), WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd", "--as-json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestTagContractViolated(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.TagContract("json", "as-json")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithTags("json"), WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, `command "cmd": tag "json" requires flag "--as-json"`) {
		t.Fatalf("expected tag contract error, got %q", r.Stderr)
	}
}

func TestTagContractExactErrorMessage(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.TagContract("json", "as-json")
	app.Command("foo", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithTags("json"), WithEffect(EffectReadOnly))
	r := app.Test([]string{"foo"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	expected := `command "foo": tag "json" requires flag "--as-json"`
	if !strings.Contains(r.Stderr, expected) {
		t.Fatalf("expected error %q in stderr, got %q", expected, r.Stderr)
	}
}

func TestTagContractOnInheritedTag(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.TagContract("json", "as-json")
	g := app.Group("grp", "a group", "json")
	// Command inherits "json" tag from group but has no --json flag -> violation
	g.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r := app.Test([]string{"grp", "cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1 for inherited tag contract violation, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, `tag "json" requires flag "--as-json"`) {
		t.Fatalf("expected tag contract error for inherited tag, got %q", r.Stderr)
	}
}

func TestTagContractUntaggedCommandNotChecked(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.TagContract("json", "as-json")
	// Command has no tags -- should not be affected by the contract
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("ok")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0 for untagged command, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestTagContractPassthroughExempt(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.TagContract("json", "as-json")
	app.Passthrough("cmd", "a command", func(ctx *Context, name string, args []string, globals map[string]interface{}) int {
		return 0
	}, WithTags("json"), WithEffect(EffectReadOnly))
	// Passthrough commands are exempt from tag contracts
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0 for passthrough (exempt from contracts), got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestTagContractRegisteredAfterCommands(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	// Register command first, then the contract
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithTags("json"), WithEffect(EffectReadOnly))
	app.TagContract("json", "as-json")
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1 for contract registered after command, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, `tag "json" requires flag "--as-json"`) {
		t.Fatalf("expected tag contract error, got %q", r.Stderr)
	}
}

func TestTagContractMultipleContracts(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.TagContract("json", "as-json")
	app.TagContract("loud", "loud")
	// Command has "json" tag but not --json flag, and "loud" tag but not --loud flag
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithTags("json", "loud"), WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	// Should report at least one violation
	if !strings.Contains(r.Stderr, "requires flag") {
		t.Fatalf("expected tag contract error, got %q", r.Stderr)
	}
}

func TestTagContractInvalidTagPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for invalid tag in TagContract, got none")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "invalid tag name") {
			t.Fatalf("expected panic about invalid tag name, got %q", msg)
		}
	}()
	app := NewApp("myapp", "1.0.0", "test app")
	app.TagContract("INVALID", "json")
}

// --- Schema output ---

func TestSchemaTaggedCommand(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithTags("xml", "json"), WithEffect(EffectReadOnly))
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	tags, ok := cmd["tags"]
	if !ok {
		t.Fatal("expected 'tags' in command schema")
	}
	tagArr := tags.([]interface{})
	if len(tagArr) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(tagArr))
	}
	// Tags should be sorted alphabetically
	if tagArr[0] != "json" || tagArr[1] != "xml" {
		t.Fatalf("expected tags [json, xml], got %v", tagArr)
	}
}

func TestSchemaTagsSorted(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithTags("zebra", "alpha", "middle"), WithEffect(EffectReadOnly))
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	tagArr := cmd["tags"].([]interface{})
	if len(tagArr) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(tagArr))
	}
	if tagArr[0] != "alpha" || tagArr[1] != "middle" || tagArr[2] != "zebra" {
		t.Fatalf("expected tags [alpha, middle, zebra], got %v", tagArr)
	}
}

func TestSchemaGroupOwnTags(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	g := app.Group("grp", "a group", "beta", "alpha")
	g.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	groups := schema["groups"].(map[string]interface{})
	grp := groups["grp"].(map[string]interface{})
	tags, ok := grp["tags"]
	if !ok {
		t.Fatal("expected 'tags' in group schema")
	}
	tagArr := tags.([]interface{})
	if len(tagArr) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(tagArr))
	}
	// Tags should be sorted
	if tagArr[0] != "alpha" || tagArr[1] != "beta" {
		t.Fatalf("expected tags [alpha, beta], got %v", tagArr)
	}
}

func TestSchemaNoTagsOmitted(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	if _, ok := cmd["tags"]; ok {
		t.Fatal("tags should be omitted when empty")
	}
}

func TestSchemaTagsInDefaults(t *testing.T) {
	defaults := toPlain(buildSchemaDefaults()).(map[string]interface{})
	cmdDefaults := defaults["command"].(map[string]interface{})
	if _, ok := cmdDefaults["tags"]; !ok {
		t.Fatal("expected 'tags' in command defaults")
	}
	grpDefaults := defaults["group"].(map[string]interface{})
	if _, ok := grpDefaults["tags"]; !ok {
		t.Fatal("expected 'tags' in group defaults")
	}
}

// --- Schema enrichment tests ---

func TestSchemaVersion(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("noop", "does nothing", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	sv, ok := schema["schema_version"]
	if !ok {
		t.Fatal("expected 'schema_version' in schema")
	}
	if sv != 2 {
		t.Fatalf("expected schema_version 2, got %v", sv)
	}
}

func TestSchemaVersionInDefaults(t *testing.T) {
	defaults := toPlain(buildSchemaDefaults()).(map[string]interface{})
	sv, ok := defaults["schema_version"]
	if !ok {
		t.Fatal("expected 'schema_version' in defaults")
	}
	if sv != 2 {
		t.Fatalf("expected schema_version default 2, got %v", sv)
	}
}

// TestSchemaConstraintsMutex is DELETED with the "mutex" constraint entry
// (contract §21's supersession box): exactly-one left the constraint system
// entirely and is a member-spelled selector now, which is a FLAG entry rather
// than a constraint. The selector's own dump encoding belongs to the schema-v2
// amendment (§24.11, §24.15).

func TestSchemaConstraintsAllOrNone(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(
			StringFlag("username", "Username", Optional()),
			StringFlag("password", "Password", Optional()),
		),
		WithConstraints(AllOrNone("credentials", Member("username"), Member("password"))), WithEffect(EffectReadOnly),
	)
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	constraints := cmd["constraints"].([]interface{})
	if len(constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(constraints))
	}
	c := constraints[0].(map[string]interface{})
	if c["type"] != "all_or_none" {
		t.Fatalf("expected type 'all_or_none', got %v", c["type"])
	}
	if c["name"] != "credentials" {
		t.Fatalf("expected name 'credentials', got %v", c["name"])
	}
	members := c["members"].([]interface{})
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	first := members[0].(map[string]interface{})
	if first["kind"] != "flag" || first["name"] != "username" || first["when"] != "present" {
		t.Fatalf("unexpected first member %v", first)
	}
	second := members[1].(map[string]interface{})
	if second["kind"] != "flag" || second["name"] != "password" || second["when"] != "present" {
		t.Fatalf("unexpected second member %v", second)
	}
}

func TestSchemaConstraintsRequires(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(
			StringFlag("format", "Output format", Optional()),
			StringFlag("output", "Output file", Optional()),
		),
		WithConstraints(Requires("output-needs-format", "output", "format")), WithEffect(EffectReadOnly),
	)
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	constraints := cmd["constraints"].([]interface{})
	if len(constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(constraints))
	}
	c := constraints[0].(map[string]interface{})
	if c["type"] != "requires" {
		t.Fatalf("expected type 'requires', got %v", c["type"])
	}
	if c["name"] != "output-needs-format" {
		t.Fatalf("expected name 'output-needs-format', got %v", c["name"])
	}
	if c["flag"] != "output" {
		t.Fatalf("expected flag 'output', got %v", c["flag"])
	}
	if c["depends_on"] != "format" {
		t.Fatalf("expected depends_on 'format', got %v", c["depends_on"])
	}
}

func TestSchemaConstraintsImplies(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(
			BoolFlag("loud", "Loud output", Default(false)),
			BoolFlag("debug", "Debug mode", Default(false)),
		),
		WithConstraints(Implies("debug-is-loud", "debug", "loud", true)), WithEffect(EffectReadOnly),
	)
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	constraints := cmd["constraints"].([]interface{})
	if len(constraints) != 1 {
		t.Fatalf("expected 1 constraint, got %d", len(constraints))
	}
	c := constraints[0].(map[string]interface{})
	if c["type"] != "implies" {
		t.Fatalf("expected type 'implies', got %v", c["type"])
	}
	if c["name"] != "debug-is-loud" {
		t.Fatalf("expected name 'debug-is-loud', got %v", c["name"])
	}
	if c["flag"] != "debug" {
		t.Fatalf("expected flag 'debug', got %v", c["flag"])
	}
	if c["implies"] != "loud" {
		t.Fatalf("expected implies 'loud', got %v", c["implies"])
	}
	if c["value"] != true {
		t.Fatalf("expected value true, got %v", c["value"])
	}
}

func TestSchemaConstraintsMixed(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(
			StringFlag("host", "Hostname", Optional()),
			StringFlag("port", "Port", Optional()),
			BoolFlag("loud", "Loud output", Default(false)),
			BoolFlag("debug", "Debug mode", Default(false)),
		),
		WithConstraints(
			AllOrNone("endpoint", Member("host"), Member("port")),
			Requires("port-needs-host", "port", "host"),
			Implies("debug-is-loud", "debug", "loud", true),
		), WithEffect(EffectReadOnly),
	)
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	constraints := cmd["constraints"].([]interface{})
	// 3 constraints; the mutex and co_required entries are deleted with their
	// constructs (§25.7's amendment).
	if len(constraints) != 3 {
		t.Fatalf("expected 3 constraints, got %d", len(constraints))
	}
	// all_or_none, requires, implies in declaration order
	if constraints[0].(map[string]interface{})["type"] != "all_or_none" {
		t.Fatalf("expected first constraint to be all_or_none, got %v", constraints[0])
	}
	if constraints[1].(map[string]interface{})["type"] != "requires" {
		t.Fatalf("expected second constraint to be requires, got %v", constraints[1])
	}
	if constraints[2].(map[string]interface{})["type"] != "implies" {
		t.Fatalf("expected third constraint to be implies, got %v", constraints[2])
	}
}

func TestSchemaConstraintsOmittedWhenEmpty(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	if _, ok := cmd["constraints"]; ok {
		t.Fatal("constraints should be omitted when empty")
	}
}

func TestSchemaConstraintsInDefaults(t *testing.T) {
	defaults := toPlain(buildSchemaDefaults()).(map[string]interface{})
	cmdDefaults := defaults["command"].(map[string]interface{})
	if _, ok := cmdDefaults["constraints"]; !ok {
		t.Fatal("expected 'constraints' in command defaults")
	}
}

func TestSchemaTagContracts(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.TagContract("release", "sim-run")
	app.Command("deploy", "Deploy app", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(BoolFlag("sim-run", "Dry run mode", Default(false))),
		WithTags("release"), WithEffect(EffectReadOnly),
	)
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	tc, ok := schema["tag_contracts"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'tag_contracts' in schema")
	}
	if tc["release"] != "sim-run" {
		t.Fatalf("expected tag_contracts['release'] = 'sim-run', got %v", tc["release"])
	}
}

func TestSchemaTagContractsOmittedWhenEmpty(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("noop", "does nothing", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	if _, ok := schema["tag_contracts"]; ok {
		t.Fatal("tag_contracts should be omitted when empty")
	}
}

func TestSchemaTagContractsInDefaults(t *testing.T) {
	defaults := toPlain(buildSchemaDefaults()).(map[string]interface{})
	appDefaults := defaults["app"].(map[string]interface{})
	if _, ok := appDefaults["tag_contracts"]; !ok {
		t.Fatal("expected 'tag_contracts' in app defaults")
	}
}

func TestSchemaArgDefault(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithArgs(
			NewArg("target", "Target name", ArgDefault("prod")),
		), WithEffect(EffectReadOnly),
	)
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	args := cmd["args"].([]interface{})
	if len(args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(args))
	}
	arg := args[0].(map[string]interface{})
	if arg["default"] != "prod" {
		t.Fatalf("expected arg default 'prod', got %v", arg["default"])
	}
	if arg["presence"] != "default" {
		t.Fatalf("expected presence 'default', got %v", arg["presence"])
	}
	if _, ok := arg["required"]; ok {
		t.Fatal("the arg-side 'required' key is deleted; presence is the one key")
	}
}

func TestSchemaArgDefaultOmittedWhenNotSet(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithArgs(
			NewArg("target", "Target name", ArgRequired()),
		), WithEffect(EffectReadOnly),
	)
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	args := cmd["args"].([]interface{})
	arg := args[0].(map[string]interface{})
	if _, ok := arg["default"]; ok {
		t.Fatal("arg default should be omitted when not set")
	}
}

// `default` has NO baseline on either entry since presence became the
// authority: it is emitted exactly when `presence` is "default", so a `null`
// baseline for it would state something false (contract §25.10).
func TestSchemaDefaultHasNoBaselineOnEitherEntry(t *testing.T) {
	defaults := toPlain(buildSchemaDefaults()).(map[string]interface{})
	for _, entity := range []string{"arg", "flag"} {
		if _, ok := defaults[entity].(map[string]interface{})["default"]; ok {
			t.Fatalf("%s defaults must carry no 'default' baseline", entity)
		}
	}
}

func TestSchemaArgOptional(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithArgs(
			NewArg("target", "Target name", ArgOptional()),
		), WithEffect(EffectReadOnly),
	)
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	args := cmd["args"].([]interface{})
	arg := args[0].(map[string]interface{})
	// An optional arg declares no value, so no 'default' key is emitted --
	// its presence is the whole declaration.
	if arg["presence"] != "optional" {
		t.Fatalf("expected presence 'optional', got %v", arg["presence"])
	}
	if _, ok := arg["default"]; ok {
		t.Fatal("an optional arg must not emit a 'default' key")
	}
}

// ---------------------------------------------------------------------------
// Typed positional args
// ---------------------------------------------------------------------------

func TestArgTypeInt(t *testing.T) {
	app := simpleApp("cmd", "a command", "val={count}",
		WithArgs(NewArg("count", "how many", ArgType(TypeInt), ArgRequired())))
	r := app.Test([]string{"cmd", "42"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "val=42\n" {
		t.Fatalf("expected 'val=42', got %q", r.Stdout)
	}
}

func TestArgTypeIntInvalid(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("count", "how many", ArgType(TypeInt), ArgRequired())))
	r := app.Test([]string{"cmd", "abc"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "argument 'count': expected integer, got 'abc'") {
		t.Fatalf("expected int parse error, got %q", r.Stderr)
	}
}

func TestArgTypeFloat(t *testing.T) {
	app := simpleApp("cmd", "a command", "val={ratio}",
		WithArgs(NewArg("ratio", "the ratio", ArgType(TypeFloat), ArgRequired())))
	r := app.Test([]string{"cmd", "3.14"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "val=3.14\n" {
		t.Fatalf("expected 'val=3.14', got %q", r.Stdout)
	}
}

func TestArgTypeFloatInvalid(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("ratio", "the ratio", ArgType(TypeFloat), ArgRequired())))
	r := app.Test([]string{"cmd", "notnum"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "argument 'ratio': expected float, got 'notnum'") {
		t.Fatalf("expected float parse error, got %q", r.Stderr)
	}
}

func TestArgTypeFloatRejectsNaN(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("val", "a float", ArgType(TypeFloat), ArgRequired())))
	r := app.Test([]string{"cmd", "NaN"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "argument 'val': NaN is not allowed") {
		t.Fatalf("expected NaN rejection, got %q", r.Stderr)
	}
}

func TestArgTypeFloatRejectsInf(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("val", "a float", ArgType(TypeFloat), ArgRequired())))
	r := app.Test([]string{"cmd", "Inf"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "argument 'val': Inf is not allowed") {
		t.Fatalf("expected Inf rejection, got %q", r.Stderr)
	}
}

func TestArgTypeBool(t *testing.T) {
	app := simpleApp("cmd", "a command", "val={flag}",
		WithArgs(NewArg("flag", "a flag value", ArgType(TypeBool), ArgRequired())))
	r := app.Test([]string{"cmd", "true"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "val=true\n" {
		t.Fatalf("expected 'val=true', got %q", r.Stdout)
	}
}

func TestArgTypeBoolInvalid(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("flag", "a flag value", ArgType(TypeBool), ArgRequired())))
	r := app.Test([]string{"cmd", "maybe"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "argument 'flag': expected boolean, got 'maybe'") {
		t.Fatalf("expected bool parse error, got %q", r.Stderr)
	}
}

func TestArgTypeBoolCaseInsensitive(t *testing.T) {
	app := simpleApp("cmd", "a command", "val={flag}",
		WithArgs(NewArg("flag", "a flag value", ArgType(TypeBool), ArgRequired())))
	for _, val := range []string{"yes", "YES", "Yes", "1", "true", "TRUE"} {
		r := app.Test([]string{"cmd", val})
		if r.ExitCode != 0 {
			t.Fatalf("value %q: expected exit 0, got %d; stderr=%q", val, r.ExitCode, r.Stderr)
		}
		if r.Stdout != "val=true\n" {
			t.Fatalf("value %q: expected 'val=true', got %q", val, r.Stdout)
		}
	}
	for _, val := range []string{"no", "NO", "No", "0", "false", "FALSE"} {
		r := app.Test([]string{"cmd", val})
		if r.ExitCode != 0 {
			t.Fatalf("value %q: expected exit 0, got %d; stderr=%q", val, r.ExitCode, r.Stderr)
		}
		if r.Stdout != "val=false\n" {
			t.Fatalf("value %q: expected 'val=false', got %q", val, r.Stdout)
		}
	}
}

func TestArgStrDefaultType(t *testing.T) {
	// Default type is str -- no coercion applied
	app := simpleApp("cmd", "a command", "val={name}",
		WithArgs(NewArg("name", "the name", ArgRequired())))
	r := app.Test([]string{"cmd", "42"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if r.Stdout != "val=42\n" {
		t.Fatalf("expected 'val=42', got %q", r.Stdout)
	}
}

func TestArgChoicesStr(t *testing.T) {
	app := simpleApp("cmd", "a command", "val={env}",
		WithArgs(NewArg("env", "target env", ArgChoices(Ch("dev", ""), Ch("staging", ""), Ch("prod", "")), ArgRequired())))
	r := app.Test([]string{"cmd", "prod"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "val=prod\n" {
		t.Fatalf("expected 'val=prod', got %q", r.Stdout)
	}
}

func TestArgChoicesStrInvalid(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("env", "target env", ArgChoices(Ch("dev", ""), Ch("staging", ""), Ch("prod", "")), ArgRequired())))
	r := app.Test([]string{"cmd", "local"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "argument 'env': invalid value 'local', must be one of: dev, staging, prod") {
		t.Fatalf("expected choices error, got %q", r.Stderr)
	}
}

func TestArgChoicesInt(t *testing.T) {
	app := simpleApp("cmd", "a command", "val={level}",
		WithArgs(NewArg("level", "log level", ArgType(TypeInt), ArgChoices(Ch(1, ""), Ch(2, ""), Ch(3, "")), ArgRequired())))
	r := app.Test([]string{"cmd", "2"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "val=2\n" {
		t.Fatalf("expected 'val=2', got %q", r.Stdout)
	}
}

func TestArgChoicesIntInvalid(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("level", "log level", ArgType(TypeInt), ArgChoices(Ch(1, ""), Ch(2, ""), Ch(3, "")), ArgRequired())))
	r := app.Test([]string{"cmd", "5"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "argument 'level': invalid value '5', must be one of: 1, 2, 3") {
		t.Fatalf("expected choices error, got %q", r.Stderr)
	}
}

func TestArgChoicesBoolPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for choices with bool arg")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "choices is incompatible with type=bool") {
			t.Fatalf("expected choices+bool error, got %q", msg)
		}
	}()
	NewArg("flag", "a bool arg", ArgType(TypeBool), ArgChoices(Ch(true, ""), Ch(false, "")), ArgRequired())
}

func TestArgChoicesEmptyPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty choices")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "choices must be a non-empty list") {
			t.Fatalf("expected empty choices error, got %q", msg)
		}
	}()
	NewArg("name", "a name", ArgChoices(), ArgRequired())
}

func TestArgChoicesTypeMismatchPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for choice type mismatch")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "is not of type int") {
			t.Fatalf("expected type mismatch error, got %q", msg)
		}
	}()
	NewArg("count", "how many", ArgType(TypeInt), ArgChoices(Ch("one", ""), Ch("two", "")), ArgRequired())
}

func TestArgDefaultTypeMismatchPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for default type mismatch")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "type=int requires an int default") {
			t.Fatalf("expected type mismatch error, got %q", msg)
		}
	}()
	NewArg("count", "how many", ArgType(TypeInt), ArgDefault("not-int"))
}

func TestArgDefaultNotInChoicesPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for default not in choices")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "is not in choices") {
			t.Fatalf("expected 'not in choices' error, got %q", msg)
		}
	}()
	NewArg("env", "target", ArgDefault("local"), ArgChoices(Ch("dev", ""), Ch("prod", "")))
}

func TestVariadicTypedArg(t *testing.T) {
	app := simpleApp("cmd", "a command", "vals={nums}",
		WithArgs(NewArg("nums", "numbers", ArgType(TypeInt), Variadic(), ArgRequired())))
	r := app.Test([]string{"cmd", "1", "2", "3"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "vals=1,2,3\n" {
		t.Fatalf("expected 'vals=1,2,3', got %q", r.Stdout)
	}
}

func TestVariadicTypedArgInvalid(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("nums", "numbers", ArgType(TypeInt), Variadic(), ArgRequired())))
	r := app.Test([]string{"cmd", "1", "abc", "3"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "argument 'nums': expected integer, got 'abc'") {
		t.Fatalf("expected int parse error, got %q", r.Stderr)
	}
}

func TestVariadicArgWithChoices(t *testing.T) {
	app := simpleApp("cmd", "a command", "vals={items}",
		WithArgs(NewArg("items", "items", ArgChoices(Ch("a", ""), Ch("b", ""), Ch("c", "")), Variadic(), ArgRequired())))
	r := app.Test([]string{"cmd", "a", "c"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	r = app.Test([]string{"cmd", "a", "d"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "argument 'items': invalid value 'd', must be one of: a, b, c") {
		t.Fatalf("expected choices error for variadic, got %q", r.Stderr)
	}
}

func TestArgTypeInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("count", "how many", ArgType(TypeInt), ArgRequired())))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "[type: int]") {
		t.Fatalf("expected '[type: int]' in help, got %q", r.Stdout)
	}
}

func TestArgTypeStrNotInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("name", "the name", ArgRequired())))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if strings.Contains(r.Stdout, "[type:") {
		t.Fatalf("str type should not show [type:] in help, got %q", r.Stdout)
	}
}

func TestArgChoicesInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("env", "target env", ArgChoices(Ch("dev", ""), Ch("prod", "")), ArgRequired())))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "[choices: dev, prod]") {
		t.Fatalf("expected '[choices: dev, prod]' in help, got %q", r.Stdout)
	}
}

func TestArgTypeAndChoicesInHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("level", "log level", ArgType(TypeInt), ArgChoices(Ch(1, ""), Ch(2, ""), Ch(3, "")), ArgRequired())))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "[type: int]") {
		t.Fatalf("expected '[type: int]' in help, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "[choices: 1, 2, 3]") {
		t.Fatalf("expected '[choices: 1, 2, 3]' in help, got %q", r.Stdout)
	}
}

func TestSchemaArgType(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("go.mod", []byte("module test\n"), 0o644)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithArgs(
			NewArg("count", "how many", ArgType(TypeInt), ArgRequired()),
			NewArg("name", "the name", ArgRequired()),
		), WithEffect(EffectReadOnly),
	)
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	args := cmd["args"].([]interface{})

	// Every arg entry carries a fragment now -- there is nothing to
	// reconstruct, which is why `arg.type` left the defaults block (§25.10).
	intArg := args[0].(map[string]interface{})
	if got := intArg["value_schema"].(map[string]interface{})["type"]; got != "integer" {
		t.Fatalf("expected fragment type 'integer', got %v", got)
	}
	strArg := args[1].(map[string]interface{})
	if got := strArg["value_schema"].(map[string]interface{})["type"]; got != "string" {
		t.Fatalf("expected fragment type 'string', got %v", got)
	}
	if _, ok := strArg["type"]; ok {
		t.Fatalf("the v1 'type' key is deleted, got %v", strArg["type"])
	}
}

func TestSchemaArgChoices(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("go.mod", []byte("module test\n"), 0o644)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithArgs(
			NewArg("env", "target", ArgChoices(Ch("dev", ""), Ch("prod", "")), ArgRequired()),
		), WithEffect(EffectReadOnly),
	)
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	args := cmd["args"].([]interface{})
	arg := args[0].(map[string]interface{})
	choices, ok := arg["choices"]
	if !ok {
		t.Fatal("expected 'choices' in schema arg")
	}
	choicesList := choices.([]interface{})
	if len(choicesList) != 2 {
		t.Fatalf("expected 2 choices, got %d", len(choicesList))
	}
	// The sibling key carries RECORDS; the values live in the fragment's enum
	// (contract §25.5). `help` is omitted when the entry declares none.
	first := choicesList[0].(map[string]interface{})
	if first["value"] != "dev" || len(first) != 1 {
		t.Fatalf("expected {value: dev}, got %v", first)
	}
	enum := arg["value_schema"].(map[string]interface{})["enum"].([]interface{})
	if len(enum) != 2 || enum[0] != "dev" || enum[1] != "prod" {
		t.Fatalf("expected the enum [dev, prod], got %v", enum)
	}
}

func TestSchemaArgTypeInDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("go.mod", []byte("module test\n"), 0o644)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	defaults := schema["defaults"].(map[string]interface{})
	argDefaults := defaults["arg"].(map[string]interface{})
	// `arg.type` is DELETED from the block: `value_schema` is always emitted,
	// so there is nothing to reconstruct (§25.10).
	if _, ok := argDefaults["type"]; ok {
		t.Fatalf("expected no arg 'type' baseline, got %v", argDefaults["type"])
	}
	if argDefaults["choices"] != nil {
		t.Fatalf("expected arg default choices nil, got %v", argDefaults["choices"])
	}
}

func TestArgIntNegativeValue(t *testing.T) {
	app := simpleApp("cmd", "a command", "val={count}",
		WithArgs(NewArg("count", "how many", ArgType(TypeInt), ArgRequired())))
	r := app.Test([]string{"cmd", "--", "-7"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "val=-7\n" {
		t.Fatalf("expected 'val=-7', got %q", r.Stdout)
	}
}

func TestArgFloatDefaultType(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for float default type mismatch")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "type=float requires a float default") {
			t.Fatalf("expected type mismatch error, got %q", msg)
		}
	}()
	NewArg("ratio", "the ratio", ArgType(TypeFloat), ArgDefault("nope"))
}

func TestArgBoolDefaultType(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for bool default type mismatch")
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, "type=bool requires a bool default") {
			t.Fatalf("expected type mismatch error, got %q", msg)
		}
	}()
	NewArg("flag", "a flag", ArgType(TypeBool), ArgDefault("nope"))
}

func TestArgChoicesFloat(t *testing.T) {
	app := simpleApp("cmd", "a command", "val={ratio}",
		WithArgs(NewArg("ratio", "the ratio", ArgType(TypeFloat), ArgChoices(Ch(1.0, ""), Ch(2.5, ""), Ch(3.14, "")), ArgRequired())))
	r := app.Test([]string{"cmd", "2.5"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "val=2.5\n" {
		t.Fatalf("expected 'val=2.5', got %q", r.Stdout)
	}
}

func TestArgChoicesFloatInvalid(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("ratio", "the ratio", ArgType(TypeFloat), ArgChoices(Ch(1.0, ""), Ch(2.5, ""), Ch(3.14, "")), ArgRequired())))
	r := app.Test([]string{"cmd", "9.99"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "argument 'ratio': invalid value '9.99', must be one of:") {
		t.Fatalf("expected choices error, got %q", r.Stderr)
	}
}

func TestArgMixedTypedAndStr(t *testing.T) {
	// First arg is str, second is int -- ensures independent coercion
	app := simpleApp("cmd", "a command", "name={name} count={count}",
		WithArgs(
			NewArg("name", "the name", ArgRequired()),
			NewArg("count", "how many", ArgType(TypeInt), ArgRequired()),
		))
	r := app.Test([]string{"cmd", "hello", "5"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "name=hello count=5\n" {
		t.Fatalf("expected 'name=hello count=5', got %q", r.Stdout)
	}
}

func TestArgIntWithOptionalDefault(t *testing.T) {
	app := simpleApp("cmd", "a command", "val={port}",
		WithArgs(NewArg("port", "the port", ArgType(TypeInt), ArgDefault(8080))))
	// With value
	r := app.Test([]string{"cmd", "3000"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "val=3000\n" {
		t.Fatalf("expected 'val=3000', got %q", r.Stdout)
	}
	// Without value: should use default
	r = app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "val=8080\n" {
		t.Fatalf("expected 'val=8080', got %q", r.Stdout)
	}
}

func TestSchemaArgNoChoicesOmitted(t *testing.T) {
	t.Chdir(t.TempDir())
	os.WriteFile("go.mod", []byte("module test\n"), 0o644)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithArgs(NewArg("name", "the name", ArgRequired())), WithEffect(EffectReadOnly),
	)
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	cmd := commands["cmd"].(map[string]interface{})
	args := cmd["args"].([]interface{})
	arg := args[0].(map[string]interface{})
	if _, ok := arg["choices"]; ok {
		t.Fatal("choices should be omitted when not set")
	}
}

func TestArgIntWithWhitespace(t *testing.T) {
	// Strict int parsing rejects whitespace
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(NewArg("count", "how many", ArgType(TypeInt), ArgRequired())))
	r := app.Test([]string{"cmd", " 42"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "argument 'count': expected integer, got ' 42'") {
		t.Fatalf("expected strict int error, got %q", r.Stderr)
	}
}

// --- Hidden / Interactive tests ---

func TestHiddenCommandNotInHelp(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("visible", "A visible command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	app.Command("secret", "A secret command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithHidden(), WithEffect(EffectReadOnly))
	r := app.Test([]string{"--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "visible") {
		t.Fatal("expected 'visible' in help output")
	}
	if strings.Contains(r.Stdout, "secret") {
		t.Fatal("hidden command 'secret' should not appear in help")
	}
}

func TestHiddenCommandStillRoutable(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("secret", "A secret command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("secret-executed")
		return Exit(0)
	}, WithHidden(), WithEffect(EffectReadOnly))
	r := app.Test([]string{"secret"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "secret-executed\n" {
		t.Fatalf("expected 'secret-executed', got %q", r.Stdout)
	}
}

func TestHiddenGroupNotInHelp(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	grp := app.Group("visible-group", "A visible group")
	grp.Command("sub", "A subcommand", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	hiddenGrp := app.Group("secret-group", "A hidden group")
	hiddenGrp.Hidden = true
	hiddenGrp.Command("sub", "A subcommand", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "visible-group") {
		t.Fatal("expected 'visible-group' in help output")
	}
	if strings.Contains(r.Stdout, "secret-group") {
		t.Fatal("hidden group 'secret-group' should not appear in help")
	}
}

func TestHiddenGroupStillRoutable(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	grp := app.Group("secret-group", "A hidden group")
	grp.Hidden = true
	grp.Command("sub", "A subcommand", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("hidden-group-cmd")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"secret-group", "sub"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "hidden-group-cmd\n" {
		t.Fatalf("expected 'hidden-group-cmd', got %q", r.Stdout)
	}
}

func TestHiddenCommandInGroupNotInGroupHelp(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	grp := app.Group("tools", "Tool commands")
	grp.Command("visible", "A visible subcommand", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	grp.Command("hidden-sub", "A hidden subcommand", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithHidden(), WithEffect(EffectReadOnly))
	r := app.Test([]string{"tools", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "visible") {
		t.Fatal("expected 'visible' in group help")
	}
	if strings.Contains(r.Stdout, "hidden-sub") {
		t.Fatal("hidden command 'hidden-sub' should not appear in group help")
	}
}

func TestHiddenSubgroupNotInGroupHelp(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	grp := app.Group("tools", "Tool commands")
	visibleSub := grp.Group("public", "Public subgroup")
	visibleSub.Command("cmd", "A command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	hiddenSub := grp.Group("internal", "Internal subgroup")
	hiddenSub.Hidden = true
	hiddenSub.Command("cmd", "A command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"tools", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "public") {
		t.Fatal("expected 'public' in group help")
	}
	if strings.Contains(r.Stdout, "internal") {
		t.Fatal("hidden subgroup 'internal' should not appear in group help")
	}
}

func TestInteractiveCommandInHelp(t *testing.T) {
	// Interactive commands are visible in help (only hidden from tool export)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("edit", "Edit something interactively", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithInteractive(), WithEffect(EffectReadOnly))
	r := app.Test([]string{"--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "edit") {
		t.Fatal("interactive command 'edit' should appear in help")
	}
}

func TestConfigEditIsInteractive(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app", WithConfig())
	grp, ok := app.Groups()["config"]
	if !ok {
		t.Fatal("expected config group to be registered")
	}
	editCmd, ok := grp.Commands["edit"]
	if !ok {
		t.Fatal("expected config edit command to be registered")
	}
	if !editCmd.Interactive {
		t.Fatal("config edit command should be interactive")
	}
}

func TestSchemaHiddenCommand(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test\n"), 0o644)
	t.Chdir(tmpDir)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("secret", "A secret command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithHidden(), WithEffect(EffectReadOnly))
	app.Command("visible", "A visible command", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema failed: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	secretCmd := commands["secret"].(map[string]interface{})
	if secretCmd["hidden"] != true {
		t.Fatalf("expected hidden=true for secret command, got %v", secretCmd["hidden"])
	}
	visibleCmd := commands["visible"].(map[string]interface{})
	if _, ok := visibleCmd["hidden"]; ok {
		t.Fatal("hidden should be omitted when false")
	}
}

func TestSchemaInteractiveCommand(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test\n"), 0o644)
	t.Chdir(tmpDir)
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("edit", "Edit something", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithInteractive(), WithEffect(EffectReadOnly))
	app.Command("list", "List things", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema failed: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	editCmd := commands["edit"].(map[string]interface{})
	if editCmd["interactive"] != true {
		t.Fatalf("expected interactive=true for edit command, got %v", editCmd["interactive"])
	}
	listCmd := commands["list"].(map[string]interface{})
	if _, ok := listCmd["interactive"]; ok {
		t.Fatal("interactive should be omitted when false")
	}
}

func TestSchemaHiddenGroup(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module test\n"), 0o644)
	t.Chdir(tmpDir)
	app := NewApp("myapp", "1.0.0", "test app")
	grp := app.Group("secret", "A hidden group")
	grp.Hidden = true
	grp.Command("sub", "A sub", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	visibleGrp := app.Group("public", "A public group")
	visibleGrp.Command("sub", "A sub", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema failed: %v", err)
	}
	groups := schema["groups"].(map[string]interface{})
	secretGrp := groups["secret"].(map[string]interface{})
	if secretGrp["hidden"] != true {
		t.Fatalf("expected hidden=true for secret group, got %v", secretGrp["hidden"])
	}
	publicGrp := groups["public"].(map[string]interface{})
	if _, ok := publicGrp["hidden"]; ok {
		t.Fatal("hidden should be omitted when false for group")
	}
}

func TestSchemaHiddenInteractiveInDefaults(t *testing.T) {
	defaults := toPlain(buildSchemaDefaults()).(map[string]interface{})
	cmdDefaults := defaults["command"].(map[string]interface{})
	if cmdDefaults["hidden"] != false {
		t.Fatalf("expected command.hidden default to be false, got %v", cmdDefaults["hidden"])
	}
	if cmdDefaults["interactive"] != false {
		t.Fatalf("expected command.interactive default to be false, got %v", cmdDefaults["interactive"])
	}
	grpDefaults := defaults["group"].(map[string]interface{})
	if grpDefaults["hidden"] != false {
		t.Fatalf("expected group.hidden default to be false, got %v", grpDefaults["hidden"])
	}
}

func TestAllHiddenCommandsNoCommandsSection(t *testing.T) {
	// When all commands are hidden, the Commands section should not appear in help
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("secret1", "Secret 1", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithHidden(), WithEffect(EffectReadOnly))
	app.Command("secret2", "Secret 2", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithHidden(), WithEffect(EffectReadOnly))
	r := app.Test([]string{"--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if strings.Contains(r.Stdout, "Commands:") {
		t.Fatal("Commands section should not appear when all commands are hidden")
	}
}

func TestAllHiddenGroupsNoGroupsSection(t *testing.T) {
	// When all groups are hidden, the Groups section should not appear in help
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("visible", "Visible", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	grp1 := app.Group("g1", "Group 1")
	grp1.Hidden = true
	grp1.Command("sub", "Sub", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	grp2 := app.Group("g2", "Group 2")
	grp2.Hidden = true
	grp2.Command("sub", "Sub", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r := app.Test([]string{"--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	if strings.Contains(r.Stdout, "Groups:") {
		t.Fatal("Groups section should not appear when all groups are hidden")
	}
}

// --- hard-error config loading tests ---

func TestConfigMalformedTOMLHardError(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	dir := filepath.Join(tmpDir, "testapp")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("key = [unclosed"), 0o644)

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigFormat("toml"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithFlags(
		StringFlag("name", "a name", Default("")),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config file") {
		t.Fatalf("expected 'config file' in error, got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "line") || !strings.Contains(r.Stderr, "column") {
		t.Fatalf("expected position info in error, got %q", r.Stderr)
	}
}

func TestConfigMalformedJSONHardError(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	dir := filepath.Join(tmpDir, "testapp")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"key": bad}`), 0o644)

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithFlags(
		StringFlag("name", "a name", Default("")),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config file") {
		t.Fatalf("expected 'config file' in error, got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "line") || !strings.Contains(r.Stderr, "column") {
		t.Fatalf("expected position info in error, got %q", r.Stderr)
	}
}

func TestConfigMissingViaRuntimeFlag(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	_ = tmpDir

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"--config", "/nonexistent/path/config.json", "run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config file not found") {
		t.Fatalf("expected 'config file not found' in error, got %q", r.Stderr)
	}
}

func TestConfigMissingViaWithConfigPathIsSoft(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	_ = tmpDir

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigPath("/nonexistent/path/config.json"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("ok"))
		return Exit(0)
	}, WithFlags(
		StringFlag("name", "a name", Default("default")),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "ok") {
		t.Fatalf("expected 'ok' in stdout, got %q", r.Stdout)
	}
}

func TestConfigMissingXDGIsSoft(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	_ = tmpDir

	// No config file written -- XDG path doesn't exist
	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("ok"))
		return Exit(0)
	}, WithFlags(
		StringFlag("name", "a name", Default("default")),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestConfigShowOnBrokenConfigShowsError(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	dir := filepath.Join(tmpDir, "testapp")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{broken`), 0o644)

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config file") {
		t.Fatalf("expected config error in stderr, got %q", r.Stderr)
	}
}

func TestConfigEditOnBrokenConfigStillWorks(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	dir := filepath.Join(tmpDir, "testapp")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{broken`), 0o644)

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	// config path should still work on broken config
	r := app.Test([]string{"config", "path"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0 for config path on broken config, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestConfigDuplicateKeyTOMLHardError(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	dir := filepath.Join(tmpDir, "testapp")
	os.MkdirAll(dir, 0o755)
	// Duplicate key in TOML
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("name = \"a\"\nname = \"b\"\n"), 0o644)

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigFormat("toml"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithFlags(
		StringFlag("name", "a name", Default("")),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1 for duplicate key, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config file") {
		t.Fatalf("expected 'config file' in error, got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "line") || !strings.Contains(r.Stderr, "column") {
		t.Fatalf("expected position info in error, got %q", r.Stderr)
	}
}

// --- conflict mode tests ---

func TestConfigConflictModeDefault(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"name": "from-config"})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("name=%s", args["name"]))
		return Exit(0)
	}, WithFlags(
		StringFlag("name", "a name", Default("")),
	), WithEffect(EffectReadOnly))

	// Default mode (cli-wins): CLI overrides config silently
	r := app.Test([]string{"run", "--name", "from-cli"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "name=from-cli") {
		t.Fatalf("expected CLI value to win, got %q", r.Stdout)
	}
}

func TestConfigConflictModeErrorCLI(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"name": "from-config"})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigConflictMode("error"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("name=%s", args["name"]))
		return Exit(0)
	}, WithFlags(
		StringFlag("name", "a name", Default("")),
	), WithEffect(EffectReadOnly))

	// Conflict mode error: config + CLI is a conflict
	r := app.Test([]string{"run", "--name", "from-cli"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "name") {
		t.Fatalf("expected flag name in error, got %q", r.Stderr)
	}
	if !strings.Contains(r.Stderr, "cli") && !strings.Contains(r.Stderr, "config") {
		t.Fatalf("expected both sources in error, got %q", r.Stderr)
	}
}

func TestConfigConflictModeErrorEnv(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"name": "from-config"})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigConflictMode("error"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("name=%s", args["name"]))
		return Exit(0)
	}, WithFlags(
		StringFlag("name", "a name", Default(""), Env("TESTAPP_NAME"), Prefixed(false)),
	), WithEffect(EffectReadOnly))

	os.Setenv("TESTAPP_NAME", "from-env")
	defer os.Unsetenv("TESTAPP_NAME")

	// Conflict mode error: config + env is a conflict
	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "name") {
		t.Fatalf("expected flag name in error, got %q", r.Stderr)
	}
}

func TestConfigConflictModeImpliedExcluded(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"loud": true})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigConflictMode("error"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("loud=%v", args["loud"]))
		return Exit(0)
	}, WithFlags(
		BoolFlag("debug", "enable debug", Default(false)),
		BoolFlag("loud", "be loud", Default(false)),
	), WithConstraints(
		Implies("debug-is-loud", "debug", "loud", true),
	), WithEffect(EffectReadOnly))

	// Implied source should NOT trigger conflict with config
	r := app.Test([]string{"run", "--debug"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0 (implied not a conflict), got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestConfigConflictModeFiresBeforeElection(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	// Under divergence-awareness a conflict requires the config value to differ
	// from the CLI value. Config sets format_json=false while the CLI passes
	// --format-json (true): values diverge -> conflict fires. The CLI also
	// passes --format-yaml so the election would also fire -- conflict wins.
	// This is item 119's carve-out, which §21's supersession box keeps
	// untouched over member spelling.
	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"format_json": false})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigConflictMode("error"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(
			BoolFlag("format-json", "output as JSON", Default(false)),
			BoolFlag("format-yaml", "output as YAML", Default(false)),
		), WithEffect(EffectReadOnly),
	)

	// The conflict fires on the config/CLI divergence.
	r := app.Test([]string{"run", "--format-json", "--format-yaml"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	// Should mention conflict
	if !strings.Contains(r.Stderr, "set in both") {
		t.Fatalf("expected conflict error, got %q", r.Stderr)
	}
}

// --- divergence-aware conflict mode + per-flag override ---

func TestConflictErrorIdenticalScalarPasses(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"target": "same"})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigConflictMode("error"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("target=%s", args["target"]))
		return Exit(0)
	}, WithFlags(StringFlag("target", "a target", Default("default-val"))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--target", "same"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0 (identical values agree), got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "target=same") {
		t.Fatalf("expected target=same, got %q", r.Stdout)
	}
}

func TestConflictPerFlagErrorBeatsAppCliWins(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"target": "from-config"})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigConflictMode("cli-wins"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("target", "a target", Default("default-val"), ConflictMode("error"))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--target", "from-cli"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1 (per-flag error), got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "set in both cli and config; remove one") {
		t.Fatalf("expected conflict error, got %q", r.Stderr)
	}
}

func TestConflictPerFlagCliWinsBeatsAppError(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"target": "from-config"})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigConflictMode("error"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("target=%s", args["target"]))
		return Exit(0)
	}, WithFlags(StringFlag("target", "a target", Default("default-val"), ConflictMode("cli-wins"))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--target", "from-cli"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0 (per-flag cli-wins), got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "target=from-cli") {
		t.Fatalf("expected target=from-cli, got %q", r.Stdout)
	}
}

func TestConflictRepeatableOrderSensitive(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"target": []interface{}{"a", "b"}})

	newApp := func() *App {
		app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigConflictMode("error"))
		app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
			WithFlags(ListFlag(TypeStr, "target", "targets", Unique(false), Default([]interface{}{}))), WithEffect(EffectReadOnly))
		return app
	}

	// Same order -> equal -> pass
	r := newApp().Test([]string{"run", "--target", "a", "--target", "b"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0 (same order), got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	// Different order -> divergent -> error
	r2 := newApp().Test([]string{"run", "--target", "b", "--target", "a"})
	if r2.ExitCode != 1 {
		t.Fatalf("expected exit 1 (order-sensitive divergence), got %d: stderr=%q", r2.ExitCode, r2.Stderr)
	}
	if !strings.Contains(r2.Stderr, "set in both") {
		t.Fatalf("expected conflict error, got %q", r2.Stderr)
	}
}

func TestConflictUniqueOrderInsensitive(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"target": []interface{}{"a", "b"}})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigConflictMode("error"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(ListFlag(TypeStr, "target", "targets", Unique(true), Default([]interface{}{}))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--target", "b", "--target", "a"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0 (multiset equal), got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestConflictMalformedConfigValueErrorsCleanly(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"target": "not-an-int"})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigConflictMode("error"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(IntFlag("target", "a target", Default(0))), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--target", "5"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1 (clean config value error), got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "config value error") {
		t.Fatalf("expected config value error, got %q", r.Stderr)
	}
}

func TestConflictModeInvalidPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic on invalid conflict mode")
		}
	}()
	StringFlag("x", "help", ConflictMode("bogus"), Required())
}

// TestDumpSchemaDictNoCWD verifies App.DumpSchemaDict() returns the schema core
// without project_id and without any CWD/filesystem access. DumpSchemaDict reads
// only the in-memory App, so it never touches go.mod or the CWD -- no chdir is
// needed to prove this (project_id is absent regardless of the working dir).
func TestDumpSchemaDictNoCWD(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "A test app")
	app.Command("greet", "Say hello", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	d := app.DumpSchemaDict()
	if d["schema_version"] != 2 {
		t.Fatalf("expected schema_version 2, got %v", d["schema_version"])
	}
	if d["version"] != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %v", d["version"])
	}
	if d["name"] != "testapp" {
		t.Fatalf("expected name testapp, got %v", d["name"])
	}
	if _, ok := d["project_id"]; ok {
		t.Fatalf("expected project_id to be absent, got %v", d["project_id"])
	}
	if _, ok := d["commands"]; !ok {
		t.Fatalf("expected commands key present")
	}
}

// TestDumpSchemaDictEqualsFileMinusProjectID verifies the method output equals
// the file-writer output with project_id removed, byte-identical by construction.
func TestDumpSchemaDictEqualsFileMinusProjectID(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module example.com/testproject\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// t.Chdir changes into tmpDir and restores the original CWD when the test
	// ends, keeping the process CWD hygienic under shuffled test ordering.
	t.Chdir(tmpDir)
	app := NewApp("myapp", "2.3.4", "My great app", WithEnvPrefix("MYAPP"))
	app.Command("deploy", "Deploy the app", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(BoolFlag("force-deploy", "Force deploy", Default(false))), WithEffect(EffectReadOnly),
	)

	r := app.Test([]string{"help", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	data := []byte(r.Stdout)
	var written map[string]interface{}
	if err := json.Unmarshal(data, &written); err != nil {
		t.Fatal(err)
	}
	delete(written, "project_id")

	method := app.DumpSchemaDict()

	wb, _ := json.MarshalIndent(written, "", "  ")
	mb, _ := json.MarshalIndent(method, "", "  ")
	if string(wb) != string(mb) {
		t.Fatalf("method output != file output minus project_id:\nmethod=%s\nfile  =%s", mb, wb)
	}
}
