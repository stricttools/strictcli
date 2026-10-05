package strictcli

import (
	"testing"
)

func TestTagContractSatisfiedByGlobalFlag(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(BoolFlag("as-json", "output json", Default(false)))
	app.TagContract("json", "as-json")
	app.Command("cmd", "a command", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("ok")
		return Exit(0)
	}, WithTags("json"), WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0 (global flag satisfies contract), got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "ok\n" {
		t.Fatalf("expected stdout %q, got %q", "ok", r.Stdout)
	}
}
