package strictcli

import (
	"bytes"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// The early exit (effects contract §19.9): ExitNow ends a command from
// anywhere in its call stack through the one exit step.

// failDeep is a helper three frames below the handler: the shape the early
// exit exists for.
func failDeep(code int, msg string) { failDeeper(code, msg) }
func failDeeper(code int, msg string) {
	ExitNow(code, msg)
}

func earlyExitApp(handler func(ctx *Context, kwargs map[string]interface{}) Outcome, opts ...CmdOption) *App {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", handler, append([]CmdOption{WithEffect(EffectReadOnly)}, opts...)...)
	return app
}

func TestExitNowFromAHelperEndsTheCommandWithTheCode(t *testing.T) {
	var deferred atomic.Bool
	app := earlyExitApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		defer deferred.Store(true)
		failDeep(3, "no manifest at ./m.toml")
		return Exit(0)
	})
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 3 || r.Stdout != "" || r.Stderr != "error: no manifest at ./m.toml\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
	if !deferred.Load() {
		t.Fatal("the handler's deferred function did not run")
	}
}

func TestExitNowUnderJSONIsTheLastErrorDiagnostic(t *testing.T) {
	app := earlyExitApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Warn("cache is stale")
		failDeep(3, "no manifest at ./m.toml")
		return Exit(0)
	})
	r := app.Test([]string{"--json", "cmd"})
	want := `{"interface_version":3,"app":"myapp","app_version":"1.0.0","command":"cmd","exit_code":3,"payload":null,"output":null,"dry_run":false,"writes":null,"preview":[],"preview_error":null,"diagnostics":[{"level":"warn","message":"cache is stale"},{"level":"error","message":"no manifest at ./m.toml"}]}` + "\n"
	if r.ExitCode != 3 || r.Stdout != want || r.Stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestExitNowKeepsAPayloadSuppliedBeforeIt(t *testing.T) {
	app := earlyExitApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"done": 2})
		ExitNow(4, "stopped")
		return Exit(0)
	}, PayloadSchema(map[string]interface{}{"type": "object"}))
	r := app.Test([]string{"--json", "cmd"})
	if r.ExitCode != 4 || !strings.Contains(r.Stdout, `"payload":{"done":2}`) {
		t.Fatalf("exit=%d stdout=%q", r.ExitCode, r.Stdout)
	}
}

func TestExitNowRefusesOutOfRangeCodesAndEmptyMessages(t *testing.T) {
	cases := []struct {
		code int
		msg  string
		want string
	}{
		{0, "done", errExitNowCode(0)},
		{256, "too big", errExitNowCode(256)},
		{-1, "negative", errExitNowCode(-1)},
		{2, "", errExitNowMessageEmpty},
	}
	for _, c := range cases {
		func() {
			defer func() {
				r := recover()
				if r != c.want {
					t.Fatalf("ExitNow(%d, %q) panicked with %v, want %q", c.code, c.msg, r, c.want)
				}
			}()
			ExitNow(c.code, c.msg)
		}()
	}
}

func TestExitNowInAPassthroughHandler(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Passthrough("pt", "a passthrough", func(ctx *Context, name string, args []string, globals map[string]interface{}) int {
		ExitNow(5, "child tool missing")
		return 0
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"pt", "x"})
	if r.ExitCode != 5 || r.Stderr != "error: child tool missing\n" {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestExitNowUnderDryRunRendersTheLogThenTheMessage(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("build", "build", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		if _, err := ctx.Effects().Write("out.txt", "hi"); err != nil {
			t.Fatal(err)
		}
		ExitNow(3, "no manifest")
		return Exit(0)
	}, WithEffect(EffectMutating))
	r := app.Test([]string{"--dry-run", "build"})
	if r.ExitCode != 3 || r.Stdout != "DRY RUN — no changes were made. Would do:\n  1. write: out.txt (2 bytes)\n" || r.Stderr != "error: no manifest\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestCallReturnsExitErrorOnAnEarlyExit(t *testing.T) {
	app := earlyExitApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		failDeep(3, "no manifest at ./m.toml")
		return Exit(0)
	})
	_, err := app.Call("cmd", nil)
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %#v, want *ExitError", err)
	}
	if ee.Code != 3 || ee.Message != "no manifest at ./m.toml" || ee.Error() != "no manifest at ./m.toml" {
		t.Fatalf("ExitError = %#v (%q)", ee, ee.Error())
	}
	var ie *InvokeError
	if errors.As(err, &ie) {
		t.Fatal("an early exit is not an InvokeError")
	}
}

func TestMCPToolsCallReportsAnEarlyExitWithItsCode(t *testing.T) {
	app := earlyExitApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		failDeep(3, "no manifest")
		return Exit(0)
	})
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"cmd","arguments":{}}}` + "\n")
	var out bytes.Buffer
	app.serveMCPIO(in, &out)
	if !strings.Contains(out.String(), `"isError":true`) || !strings.Contains(out.String(), `exit code 3: no manifest`) {
		t.Fatalf("mcp output = %s", out.String())
	}
}

// --- strictcli.Go ----------------------------------------------------------

func TestGoDeliversAnExitNowRaisedOnItsGoroutine(t *testing.T) {
	app := earlyExitApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		Go(ctx, func() { failDeep(6, "worker failed") })
		<-ctx.Done()
		return Exit(0)
	})
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 6 || r.Stderr != "error: worker failed\n" {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestGoWaitsForEveryFunctionBeforeTheExitStep(t *testing.T) {
	var finished atomic.Bool
	release := make(chan struct{})
	app := earlyExitApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		Go(ctx, func() {
			<-release
			finished.Store(true)
		})
		close(release)
		return Exit(0)
	})
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 || !finished.Load() {
		t.Fatalf("exit=%d finished=%v", r.ExitCode, finished.Load())
	}
}

func TestGoRepanicsAPanicOnTheHandlersGoroutine(t *testing.T) {
	app := earlyExitApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		Go(ctx, func() { panic("worker exploded") })
		<-ctx.Done()
		return Exit(0)
	})
	defer func() {
		if r := recover(); r != "worker exploded" {
			t.Fatalf("recovered %v, want the worker's panic", r)
		}
	}()
	app.Test([]string{"cmd"})
	t.Fatal("the worker's panic did not propagate")
}

func TestTheHandlersOwnEndingStandsOverACapturedValue(t *testing.T) {
	app := earlyExitApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		Go(ctx, func() { ExitNow(6, "worker failed") })
		<-ctx.Done()
		ExitNow(3, "handler gave up")
		return Exit(0)
	})
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 3 || r.Stderr != "error: handler gave up\n" {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestGoOutsideADispatchIsRefused(t *testing.T) {
	defer func() {
		if r := recover(); r != errEffectsUnavailable {
			t.Fatalf("recovered %v", r)
		}
	}()
	Go(newContext(nil, nil, nil, nil, reservedFlags{}, nil), func() {})
	t.Fatal("Go accepted a Context constructed outside a dispatch")
}

func TestTheContextIsCanceledWhenTheDispatchEnds(t *testing.T) {
	var ctxSeen *Context
	app := earlyExitApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctxSeen = ctx
		return Exit(0)
	})
	app.Test([]string{"cmd"})
	select {
	case <-ctxSeen.Done():
	default:
		t.Fatal("Done() is still open after the dispatch ended")
	}
}
