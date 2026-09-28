package strictcli

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Machine mode: the reserved --json flag and the payload API.
//
// Contract §19.1 (the flag and its delivery), §19.4 (the payload API) and
// §7.1's 2026-08-13 amendment (the unconditional every-level name ban).

const jsonReservedMsg = "flag name 'json' is reserved by the framework"

// --- the name ban: unconditional, at every level ---------------------------

func TestJSONFlagNameIsReservedOnCommandFlags(t *testing.T) {
	expectPanicContaining(t, jsonReservedMsg, func() {
		BoolFlag("json", "output json", Default(false))
	})
}

func TestJSONFlagNameIsReservedOnGlobalFlags(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	expectPanicContaining(t, jsonReservedMsg, func() {
		app.GlobalFlag(Flag{Name: "json", Help: "output json", Type: TypeBool})
	})
}

func TestArgNamedJSONIsUnaffected(t *testing.T) {
	// The ban covers the flag surface only: an arg has no `--` spelling.
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cat", "cat a file", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithArgs(NewArg("json", "a file named json", ArgRequired())), WithEffect(EffectReadOnly))
	if r := app.Test([]string{"cat", "x"}); r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// --- delivery: both argv regions, stripped, on the Context -----------------

func machineFlagApp() *App {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"json": ctx.JSON()})
		return Exit(0)
	}, WithEffect(EffectReadOnly), PayloadSchema(map[string]interface{}{}))
	return app
}

func TestJSONFlagDelivery(t *testing.T) {
	cases := []struct {
		name       string
		argv       []string
		wantStdout string
	}{
		{"absent", []string{"run"}, ""},
		{"before the command", []string{"--json", "run"}, envelopeText("run", 0, "{\"json\":true}", false, "[]", "null", "[]")},
		{"after the command", []string{"run", "--json"}, envelopeText("run", 0, "{\"json\":true}", false, "[]", "null", "[]")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := machineFlagApp().Test(tc.argv)
			if r.ExitCode != 0 {
				t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
			}
			if r.Stdout != tc.wantStdout {
				t.Fatalf("stdout = %q, want %q", r.Stdout, tc.wantStdout)
			}
		})
	}
}

func TestJSONFlagIsNeverAHandlerKwarg(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		if _, ok := kwargs["json"]; ok {
			t.Fatal("--json must not reach the handler as a kwarg")
		}
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	if r := app.Test([]string{"run", "--json"}); r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

// --- the payload API -------------------------------------------------------

func TestPayloadWithoutADeclaredSchemaPanics(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"k": 1})
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	expectPanicContaining(t, "requires a declared payload schema", func() {
		app.Test([]string{"run"})
	})
}

func TestPayloadTwicePanics(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"k": 1})
		ctx.Payload(map[string]interface{}{"k": 2})
		return Exit(0)
	}, WithEffect(EffectReadOnly), PayloadSchema(map[string]interface{}{}))
	expectPanicContaining(t, "was already called", func() {
		app.Test([]string{"run"})
	})
}

func TestQuietNeverReachesThePayload(t *testing.T) {
	r := machineFlagApp().Test([]string{"--quiet", "--json", "run"})
	want := envelopeText("run", 0, "{\"json\":true}", false, "[]", "null", "[]")
	if r.Stdout != want {
		t.Fatalf("stdout = %q, want the envelope under --quiet %q", r.Stdout, want)
	}
}

func TestPayloadEscapingIsPlainUTF8(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"text": "héllo <b>&</b> 日本語"})
		return Exit(0)
	}, WithEffect(EffectReadOnly), PayloadSchema(map[string]interface{}{}))
	r := app.Test([]string{"run", "--json"})
	want := envelopeText("run", 0, "{\"text\":\"héllo <b>&</b> 日本語\"}", false, "[]", "null", "[]")
	if r.Stdout != want {
		t.Fatalf("stdout = %q, want %q", r.Stdout, want)
	}
}

// --- the envelope (contract §19.2) and its preview member (§19.3) ----------

// envelopeText builds the exact document a myapp/1.0.0 run emits, so a test
// pins the bytes rather than a parsed shape. An empty command means null: a
// run that ended before a command resolved.
func envelopeText(command string, exitCode int, payload string, dryRun bool, preview, previewError, diagnostics string) string {
	cmd := "null"
	if command != "" {
		cmd = `"` + command + `"`
	}
	return fmt.Sprintf(
		`{"interface_version":3,"app":"myapp","app_version":"1.0.0","command":%s,`+
			`"exit_code":%d,"payload":%s,"output":null,"dry_run":%t,"writes":null,"preview":%s,`+
			`"preview_error":%s,"diagnostics":%s}`+"\n",
		cmd, exitCode, payload, dryRun, preview, previewError, diagnostics)
}

func parseEnvelope(t *testing.T, stdout string) map[string]interface{} {
	t.Helper()
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not one JSON document: %v (stdout=%q)", err, stdout)
	}
	return env
}

func plainApp() *App {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	return app
}

func TestEnvelopeOnAPlainExit(t *testing.T) {
	r := plainApp().Test([]string{"--json", "run"})
	want := envelopeText("run", 0, "null", false, "[]", "null", "[]")
	if r.Stdout != want {
		t.Fatalf("stdout = %q, want %q", r.Stdout, want)
	}
	if r.Stderr != "" {
		t.Fatalf("stderr = %q, want empty", r.Stderr)
	}
}

func TestEnvelopeCarriesTheExitCode(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(3)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"--json", "run"})
	if r.ExitCode != 3 {
		t.Fatalf("exit=%d, want 3", r.ExitCode)
	}
	if want := envelopeText("run", 3, "null", false, "[]", "null", "[]"); r.Stdout != want {
		t.Fatalf("stdout = %q, want %q", r.Stdout, want)
	}
}

func TestEnvelopeCarriesTheDottedCommandPath(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	grp := app.Group("grp", "a group")
	grp.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"--json", "grp", "run"})
	if want := envelopeText("grp.run", 0, "null", false, "[]", "null", "[]"); r.Stdout != want {
		t.Fatalf("stdout = %q, want %q", r.Stdout, want)
	}
}

func TestEnvelopeCarriesTheDryRunFlag(t *testing.T) {
	r := plainApp().Test([]string{"--json", "--dry-run", "run"})
	if want := envelopeText("run", 0, "null", true, "[]", "null", "[]"); r.Stdout != want {
		t.Fatalf("stdout = %q, want %q", r.Stdout, want)
	}
}

func diagnosticsApp() *App {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Info("starting")
		ctx.Warn("careful")
		ctx.Debug("detail")
		ctx.Error("bad")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	return app
}

const allDiagnostics = `[{"level":"info","message":"starting"},` +
	`{"level":"warn","message":"careful"},` +
	`{"level":"debug","message":"detail"},` +
	`{"level":"error","message":"bad"}]`

func TestEnvelopeCarriesEveryDiagnosticInEmissionOrder(t *testing.T) {
	r := diagnosticsApp().Test([]string{"--json", "run"})
	if want := envelopeText("run", 0, "null", false, "[]", "null", allDiagnostics); r.Stdout != want {
		t.Fatalf("stdout = %q, want %q", r.Stdout, want)
	}
	if r.Stderr != "" {
		t.Fatalf("stderr = %q, want empty: the writers write nothing in machine mode", r.Stderr)
	}
}

func TestQuietCannotReachTheEnvelope(t *testing.T) {
	// Contract §19.2: structurally exempt -- quiet governs the human stream,
	// and debug rides without --verbose because the envelope's content is a
	// function of what the run produced.
	r := diagnosticsApp().Test([]string{"--quiet", "--json", "run"})
	if want := envelopeText("run", 0, "null", false, "[]", "null", allDiagnostics); r.Stdout != want {
		t.Fatalf("stdout = %q, want %q", r.Stdout, want)
	}
}

func TestDiagnosticsAreUnchangedOutsideMachineMode(t *testing.T) {
	r := diagnosticsApp().Test([]string{"run"})
	if r.Stdout != "starting\n" {
		t.Fatalf("stdout = %q, want %q", r.Stdout, "starting\n")
	}
	if r.Stderr != "warning: careful\nerror: bad\n" {
		t.Fatalf("stderr = %q, want %q", r.Stderr, "warning: careful\nerror: bad\n")
	}
}

func TestEnvelopeOnAnUnknownCommand(t *testing.T) {
	r := plainApp().Test([]string{"--json", "nope"})
	if r.ExitCode != 1 {
		t.Fatalf("exit=%d, want 1", r.ExitCode)
	}
	if want := envelopeText("", 1, "null", false, "[]", "null", "[]"); r.Stdout != want {
		t.Fatalf("stdout = %q, want %q", r.Stdout, want)
	}
	if !strings.Contains(r.Stderr, "unknown command 'nope'") {
		t.Fatalf("stderr = %q, want the parse error", r.Stderr)
	}
}

func TestEnvelopeOnAParseErrorAfterTheFlag(t *testing.T) {
	r := plainApp().Test([]string{"run", "--json", "--nope"})
	if want := envelopeText("", 1, "null", false, "[]", "null", "[]"); r.Stdout != want {
		t.Fatalf("stdout = %q, want %q", r.Stdout, want)
	}
	if !strings.Contains(r.Stderr, "unknown flag '--nope'") {
		t.Fatalf("stderr = %q, want the parse error", r.Stderr)
	}
}

// The --help flag is text only: under --json it is refused, naming the help
// command that prints the machine form, and that command works.
func TestHelpFlagUnderMachineModeIsRefused(t *testing.T) {
	r := plainApp().Test([]string{"--json", "run", "--help"})
	if r.ExitCode != 1 {
		t.Fatalf("exit=%d, want 1", r.ExitCode)
	}
	if !strings.HasPrefix(r.Stderr, "error: "+errHelpTextOnly("myapp help run --json")+"\n") {
		t.Fatalf("stderr=%q", r.Stderr)
	}
	if r := plainApp().Test([]string{"help", "run", "--json"}); r.ExitCode != 0 {
		t.Fatalf("the named fix fails: exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestAStaleMachineFlagNeverDecidesTheNextRun(t *testing.T) {
	app := plainApp()
	app.Test([]string{"--json", "run"})
	if r := app.Test([]string{"run"}); r.Stdout != "" {
		t.Fatalf("stdout = %q, want empty", r.Stdout)
	}
}

func TestEnvelopePreviewAgreesWithTheEffectLog(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("rel", "rel", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Effects().Mkdir("build")
		ctx.Effects().Write("VERSION", "1.2.3")
		return Exit(0)
	}, WithEffect(EffectMutating))

	r := app.Test([]string{"--json", "--dry-run", "rel"})
	env := parseEnvelope(t, r.Stdout)
	preview, _ := json.Marshal(env["preview"])
	log, _ := json.Marshal(app.EffectLog())
	if !reflect.DeepEqual(string(preview), string(log)) {
		t.Fatalf("preview=%s, effect log=%s", preview, log)
	}
	if !strings.Contains(r.Stdout, `"detail":"VERSION (5 bytes)"`) {
		t.Fatalf("the write record is missing from the preview: %q", r.Stdout)
	}
	if strings.Contains(r.Stdout, "DRY RUN") {
		t.Fatalf("machine mode renders no would-do text: %q", r.Stdout)
	}
}

func TestEnvelopePreviewAgreesWithTheEffectLogOnALiveRun(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("clean", "clean", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Effects().Remove("no-such-path-envelope-go")
		return Exit(0)
	}, WithEffect(EffectMutating))

	r := app.Test([]string{"--json", "clean"})
	env := parseEnvelope(t, r.Stdout)
	preview, _ := json.Marshal(env["preview"])
	log, _ := json.Marshal(app.EffectLog())
	if string(preview) != string(log) {
		t.Fatalf("preview=%s, effect log=%s", preview, log)
	}
	if !strings.Contains(string(preview), `"recorded":false`) {
		t.Fatalf("a live run's records carry recorded:false, got %s", preview)
	}
}

func TestEnvelopeCarriesTheTruncationAsPreviewError(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("rel", "rel", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		out, err := ctx.Effects().Run([]interface{}{"git", "tag", "v1"})
		if err != nil {
			return Exit(1)
		}
		_ = out.Stdout()
		return Exit(0)
	}, WithEffect(EffectMutating))

	r := app.Test([]string{"--json", "--dry-run", "rel"})
	if r.ExitCode != 1 {
		t.Fatalf("exit=%d, want 1", r.ExitCode)
	}
	env := parseEnvelope(t, r.Stdout)
	pe, ok := env["preview_error"].(map[string]interface{})
	if !ok {
		t.Fatalf("preview_error = %v, want an object", env["preview_error"])
	}
	if pe["kind"] != "truncated" || pe["command"] != "rel" || pe["brand"] != "«step 1 output»" {
		t.Fatalf("preview_error = %v", pe)
	}
	want := "error: dry-run preview ends at step 2: rel branched on unsettled value " +
		"«step 1 output» — cannot preview past this point"
	if pe["message"] != want {
		t.Fatalf("message = %q, want %q", pe["message"], want)
	}
	if r.Stderr != "" {
		t.Fatalf("stderr = %q: the text rides preview_error in machine mode", r.Stderr)
	}
}

// envelopePayload returns the envelope's payload member as raw JSON, for tests
// that assert on a command's machine payload rather than on the whole document.
func envelopePayload(t *testing.T, stdout string) []byte {
	t.Helper()
	var env struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &env); err != nil {
		t.Fatalf("stdout is not an envelope: %v (stdout=%q)", err, stdout)
	}
	return env.Payload
}
