package strictcli

import (
	"strings"
	"testing"
)

// --- Exit and the machine payload via Test and Call ---

func TestExitOutcomeSetsExitCodeNoData(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(3)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 3 {
		t.Fatalf("exit = %d, want 3", r.ExitCode)
	}
	if r.Data != nil {
		t.Fatalf("Data = %v, want nil", r.Data)
	}
	if r.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", r.Stdout)
	}
}

func TestPayloadPrintsJSONAndCaptures(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("info", "get info", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"name": "widget", "count": 42})
		return Exit(0)
	}, WithEffect(EffectReadOnly), PayloadSchema(map[string]interface{}{}))
	// The payload is printed only in machine mode (contract §19.4).
	r := app.Test([]string{"info", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d: %s", r.ExitCode, r.Stderr)
	}
	// Data captured for programmatic callers.
	data, ok := r.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Data type = %T, want map", r.Data)
	}
	if data["name"] != "widget" {
		t.Fatalf("Data[name] = %v, want widget", data["name"])
	}
	// JSON printed to stdout (mirrors Run behavior).
	if !strings.Contains(r.Stdout, `"name":"widget"`) {
		t.Fatalf("Stdout = %q, want JSON with name", r.Stdout)
	}
}

func TestNoPayloadIsANullEnvelopeMember(t *testing.T) {
	// The envelope is never conditional (§19.3): a run that supplied no
	// payload still emits the document, with payload null.
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("info", "get info", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"info", "--json"})
	if want := envelopeText("info", 0, "null", false, "[]", "null", "[]"); r.Stdout != want {
		t.Fatalf("Stdout = %q, want %q", r.Stdout, want)
	}
	if r.Data != nil {
		t.Fatalf("Data = %v, want nil", r.Data)
	}
}

func TestNoPayloadPrintsNothingOutsideMachineMode(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("info", "get info", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	if r := app.Test([]string{"info"}); r.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty", r.Stdout)
	}
}

func TestPayloadIsCapturedButNotPrintedOutsideMachineMode(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("info", "get info", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"name": "widget"})
		return Exit(0)
	}, WithEffect(EffectReadOnly), PayloadSchema(map[string]interface{}{}))
	r := app.Test([]string{"info"})
	if r.Stdout != "" {
		t.Fatalf("Stdout = %q, want empty outside machine mode", r.Stdout)
	}
	data, ok := r.Data.(map[string]interface{})
	if !ok || data["name"] != "widget" {
		t.Fatalf("Data = %v, want the captured payload", r.Data)
	}
}

func TestPayloadReturnedViaCall(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("store", "store data", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{Get[string](kwargs, "key"): Get[string](kwargs, "value")})
		return Exit(0)
	}, WithFlags(
		StringFlag("key", "data key", Required()),
		StringFlag("value", "data value", Required()),
	), WithEffect(EffectReadOnly), PayloadSchema(map[string]interface{}{}))
	result, err := app.Call("store", map[string]interface{}{"key": "status", "value": "active"})
	if err != nil {
		t.Fatalf("Call error: %v", err)
	}
	data, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("result type = %T, want map", result)
	}
	if data["status"] != "active" {
		t.Fatalf("data[status] = %v, want active", data["status"])
	}
}

// --- Get ---

func TestGetReturnsTypedValue(t *testing.T) {
	kwargs := map[string]interface{}{"name": "x", "count": 7, "on": true}
	if got := Get[string](kwargs, "name"); got != "x" {
		t.Fatalf("Get string = %q", got)
	}
	if got := Get[int](kwargs, "count"); got != 7 {
		t.Fatalf("Get int = %d", got)
	}
	if got := Get[bool](kwargs, "on"); got != true {
		t.Fatalf("Get bool = %v", got)
	}
}

func TestGetPanicsOnAbsentKey(t *testing.T) {
	expectPanic(t, "no such key", func() { Get[string](map[string]interface{}{}, "missing") })
}

func TestGetPanicsOnNilValue(t *testing.T) {
	expectPanic(t, "is nil", func() { Get[string](map[string]interface{}{"k": nil}, "k") })
}

func TestGetPanicsOnWrongType(t *testing.T) {
	expectPanic(t, "dynamic type", func() { Get[string](map[string]interface{}{"k": 5}, "k") })
}

// --- GetOpt ---

func TestGetOptPresent(t *testing.T) {
	v, ok := GetOpt[string](map[string]interface{}{"k": "v"}, "k")
	if !ok || v != "v" {
		t.Fatalf("GetOpt = (%q,%v), want (v,true)", v, ok)
	}
}

func TestGetOptNilReturnsFalse(t *testing.T) {
	v, ok := GetOpt[string](map[string]interface{}{"k": nil}, "k")
	if ok || v != "" {
		t.Fatalf("GetOpt = (%q,%v), want (\"\",false)", v, ok)
	}
}

func TestGetOptPanicsOnAbsentKey(t *testing.T) {
	expectPanic(t, "no such key", func() { GetOpt[string](map[string]interface{}{}, "missing") })
}

func TestGetOptPanicsOnWrongType(t *testing.T) {
	expectPanic(t, "dynamic type", func() { GetOpt[int](map[string]interface{}{"k": "s"}, "k") })
}

// --- Passthrough receives a Context ---

func TestPassthroughReceivesContext(t *testing.T) {
	var gotCtx bool
	app := NewApp("myapp", "1.0.0", "test app")
	app.Passthrough("exec", "execute", func(ctx *Context, name string, args []string, globals map[string]interface{}) int {
		gotCtx = ctx != nil
		ctx.Warn("passthrough ran")
		return 7
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"exec", "a", "b"})
	if !gotCtx {
		t.Fatal("passthrough did not receive a non-nil Context")
	}
	if r.ExitCode != 7 {
		t.Fatalf("exit = %d, want 7", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "passthrough ran") {
		t.Fatalf("Stderr = %q, want warning", r.Stderr)
	}
}

// --- Globals are merged into the handler kwargs ---

func TestGlobalsMergedIntoKwargs(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(BoolFlag("loud", "loud", Default(false)))
	var captured bool
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = Get[bool](kwargs, "loud")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"--loud", "run"})
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d: %s", r.ExitCode, r.Stderr)
	}
	if !captured {
		t.Fatal("global flag 'loud' not visible in handler kwargs")
	}
}

// TestTestCaptureLargeOutputNotTruncated verifies Test() captures handler
// output larger than the OS pipe buffer (~64KB) without truncation or
// deadlock. Regression: fixed 64KB read buffers truncated
// output and a single non-draining read could block the writing handler.
func TestTestCaptureLargeOutputNotTruncated(t *testing.T) {
	const n = 500 * 1024 // 500KB, well beyond a 64KB pipe buffer
	payload := strings.Repeat("a", n)

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("emit", "emit a large payload", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Info(payload)  // stdout
		ctx.Error(payload) // stderr
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"emit"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d, stderr len %d", r.ExitCode, len(r.Stderr))
	}
	// Info/Error append a trailing newline, and Error prefixes "error: ".
	if len(r.Stdout) != n+1 {
		t.Fatalf("stdout truncated: expected %d bytes, got %d", n+1, len(r.Stdout))
	}
	if len(r.Stderr) != len(errorPrefix)+n+1 {
		t.Fatalf("stderr truncated: expected %d bytes, got %d", len(errorPrefix)+n+1, len(r.Stderr))
	}
	if strings.TrimRight(r.Stdout, "\n") != payload {
		t.Fatal("stdout content corrupted")
	}
	if strings.TrimRight(r.Stderr, "\n") != errorPrefix+payload {
		t.Fatal("stderr content corrupted")
	}
}
