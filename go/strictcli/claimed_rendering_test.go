package strictcli

import (
	"encoding/json"
	"testing"
)

// Claimed rendering: Effects.Recorded / Effects.RenderLog (contract §19.7).
//
// Calling Recorded claims the render; RenderLog produces byte-identical bytes
// wherever the handler puts them; a claim that never rendered is re-rendered at
// the seam; and in machine mode RenderLog is a no-op.

const claimedLog = "DRY RUN — no changes were made. Would do:\n" +
	"  1. mkdir: build\n" +
	"  2. write: VERSION (5 bytes)\n"

func claimedApp(claim, render, prints bool) *App {
	app := NewApp("app", "1.0.0", "app")
	app.Command("build", "build", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Effects().Mkdir("build")
		ctx.Effects().Write("VERSION", "1.2.3")
		if claim {
			ctx.Effects().Recorded()
		}
		if render {
			ctx.Effects().RenderLog()
		}
		if prints {
			ctx.Out("summary")
		}
		return Exit(0)
	}, WithEffect(EffectMutating))
	return app
}

func TestUnclaimedTheFrameworkRendersAfterTheHandler(t *testing.T) {
	r := claimedApp(false, false, true).Test([]string{"--dry-run", "build"})
	if want := "summary\n" + claimedLog; r.Stdout != want {
		t.Fatalf("stdout=%q want %q", r.Stdout, want)
	}
}

func TestRenderLogPutsTheIdenticalBytesFirst(t *testing.T) {
	r := claimedApp(false, true, true).Test([]string{"--dry-run", "build"})
	if want := claimedLog + "summary\n"; r.Stdout != want {
		t.Fatalf("stdout=%q want %q", r.Stdout, want)
	}
	if r.Stderr != "" {
		t.Fatalf("stderr=%q", r.Stderr)
	}
}

func TestARenderedLogIsNotRepeatedAtTheSeam(t *testing.T) {
	r := claimedApp(false, true, false).Test([]string{"--dry-run", "build"})
	if r.Stdout != claimedLog {
		t.Fatalf("stdout=%q want %q", r.Stdout, claimedLog)
	}
}

func TestClaimedButNeverRenderedIsReRenderedAtTheSeam(t *testing.T) {
	r := claimedApp(true, false, true).Test([]string{"--dry-run", "build"})
	if want := "summary\n" + claimedLog; r.Stdout != want {
		t.Fatalf("stdout=%q want %q", r.Stdout, want)
	}
}

func TestRecordedReturnsTheStructuredRecords(t *testing.T) {
	app := NewApp("app", "1.0.0", "app")
	var seen []map[string]interface{}
	app.Command("build", "build", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Effects().Mkdir("build")
		seen = ctx.Effects().Recorded()
		return Exit(0)
	}, WithEffect(EffectMutating))
	app.Test([]string{"--dry-run", "build"})
	if len(seen) != 1 {
		t.Fatalf("recorded = %v", seen)
	}
	rec := seen[0]
	if rec["seq"] != 1 || rec["kind"] != "file_write" || rec["verb"] != "mkdir" ||
		rec["detail"] != "build" || rec["recorded"] != true {
		t.Fatalf("record = %v", rec)
	}
}

// effectlessApp records nothing at all: a live run must PERFORM whatever it
// records, and the two cases below are about RenderLog's own output.
func effectlessApp() *App {
	app := NewApp("app", "1.0.0", "app")
	app.Command("build", "build", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Effects().RenderLog()
		ctx.Out("summary")
		return Exit(0)
	}, WithEffect(EffectMutating))
	return app
}

func TestRenderLogEmitsNothingOutsideDryMode(t *testing.T) {
	r := effectlessApp().Test([]string{"build"})
	if r.Stdout != "summary\n" || r.Stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", r.Stdout, r.Stderr)
	}
}

func TestRenderLogRendersTheBareHeaderWithNoEffects(t *testing.T) {
	r := effectlessApp().Test([]string{"--dry-run", "build"})
	want := "DRY RUN — no changes were made. Would do:\nsummary\n"
	if r.Stdout != want {
		t.Fatalf("stdout=%q want %q", r.Stdout, want)
	}
}

func TestRenderLogIsANoOpInMachineMode(t *testing.T) {
	r := claimedApp(false, true, false).Test([]string{"--json", "--dry-run", "build"})
	var env struct {
		Preview []map[string]interface{} `json:"preview"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil {
		t.Fatalf("stdout is not one JSON document: %v (stdout=%q)", err, r.Stdout)
	}
	if len(env.Preview) != 2 {
		t.Fatalf("the preview rides the envelope unconditionally, got %v", env.Preview)
	}
	if r.Stderr != "" {
		t.Fatalf("stderr=%q", r.Stderr)
	}
}
