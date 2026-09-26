package strictcli

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Stdout ownership: the OwnsStdout declaration (effects contract §19.6).
//
// A command whose stdout IS the artifact declares it, and in machine mode the
// envelope moves to stderr so the artifact's bytes are untouched. Outside
// machine mode the declaration changes nothing at all.

const ownsStdoutDoc = `{"artifact":"v1"}`

func ownsStdoutApp(opts ...CmdOption) *App {
	app := NewApp("app", "1.0.0", "app")
	all := append([]CmdOption{WithEffect(EffectReadOnly), OwnsStdout()}, opts...)
	app.Command("dump", "dump", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		fmt.Fprintln(ctx.Document(), ownsStdoutDoc)
		return Exit(0)
	}, all...)
	return app
}

func TestOwnsStdoutChangesNothingOutsideMachineMode(t *testing.T) {
	r := ownsStdoutApp().Test([]string{"dump"})
	if r.ExitCode != 0 || r.Stdout != ownsStdoutDoc+"\n" || r.Stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestOwnsStdoutMovesTheEnvelopeToStderr(t *testing.T) {
	r := ownsStdoutApp().Test([]string{"--json", "dump"})
	if r.Stdout != ownsStdoutDoc+"\n" {
		t.Fatalf("the artifact must own stdout byte-exactly, got %q", r.Stdout)
	}
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stderr), &env); err != nil {
		t.Fatalf("stderr is not the envelope: %v (stderr=%q)", err, r.Stderr)
	}
	if env["command"] != "dump" || env["exit_code"].(float64) != 0 {
		t.Fatalf("envelope = %v", env)
	}
}

func TestOwnsStdoutMovesTheDiagnosticsWithTheEnvelope(t *testing.T) {
	app := NewApp("app", "1.0.0", "app")
	app.Command("dump", "dump", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Info("wrote 1 row")
		ctx.Warn("provisional")
		fmt.Fprintln(ctx.Document(), ownsStdoutDoc)
		return Exit(0)
	}, WithEffect(EffectReadOnly), OwnsStdout())
	r := app.Test([]string{"--json", "dump"})
	if r.Stdout != ownsStdoutDoc+"\n" {
		t.Fatalf("stdout=%q", r.Stdout)
	}
	var env struct {
		Diagnostics []diagnosticRecord `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(r.Stderr), &env); err != nil {
		t.Fatalf("stderr is not the envelope: %v", err)
	}
	want := []diagnosticRecord{
		{Level: "info", Message: "wrote 1 row"},
		{Level: "warn", Message: "provisional"},
	}
	if len(env.Diagnostics) != len(want) {
		t.Fatalf("diagnostics = %v", env.Diagnostics)
	}
	for i, d := range env.Diagnostics {
		if d != want[i] {
			t.Fatalf("diagnostics[%d] = %v, want %v", i, d, want[i])
		}
	}
}

func TestAnUndeclaredCommandKeepsTheEnvelopeOnStdout(t *testing.T) {
	app := NewApp("app", "1.0.0", "app")
	app.Command("plain", "plain", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	r := app.Test([]string{"--json", "plain"})
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil {
		t.Fatalf("stdout is not the envelope: %v (stdout=%q)", err, r.Stdout)
	}
	if r.Stderr != "" {
		t.Fatalf("stderr=%q", r.Stderr)
	}
}

func TestOwnsStdoutMovesAPreviewEnvelopeToo(t *testing.T) {
	app := NewApp("app", "1.0.0", "app")
	app.Command("dump", "dump", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Effects().Write("out.sql", "x")
		fmt.Fprintln(ctx.Document(), "-- sql")
		return Exit(0)
	}, WithEffect(EffectMutating), OwnsStdout())
	r := app.Test([]string{"--json", "--dry-run", "dump"})
	if r.Stdout != "-- sql\n" {
		t.Fatalf("stdout=%q", r.Stdout)
	}
	var env struct {
		DryRun  bool                     `json:"dry_run"`
		Preview []map[string]interface{} `json:"preview"`
	}
	if err := json.Unmarshal([]byte(r.Stderr), &env); err != nil {
		t.Fatalf("stderr is not the envelope: %v", err)
	}
	if !env.DryRun || len(env.Preview) != 1 || env.Preview[0]["verb"] != "write" {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestOwnsStdoutIsPublishedBySchemaDumpOnlyWhenTrue(t *testing.T) {
	app := NewApp("app", "1.0.0", "app")
	app.Command("dump", "dump", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly), OwnsStdout())
	app.Command("plain", "plain", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	schema := dumpSchemaCore(app)
	commands := schema["commands"].(map[string]interface{})
	dump := commands["dump"].(map[string]interface{})
	if dump["owns_stdout"] != true {
		t.Fatalf("dump entry = %v", dump)
	}
	plain := commands["plain"].(map[string]interface{})
	if _, present := plain["owns_stdout"]; present {
		t.Fatalf("the baseline must be omitted, plain entry = %v", plain)
	}
}
