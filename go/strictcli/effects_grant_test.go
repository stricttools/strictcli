package strictcli

import "testing"

// NewGrant builds a grant authorizing one effect class, so a caller never
// spells the struct's field for it: a run may use a ProcMutate grant and is
// refused a FileWrite one.
func TestNewGrantAuthorizesItsEffectClass(t *testing.T) {
	g := NewGrant("push", "the release engine owns remote refs", ProcMutate)
	if g.Name != "push" || g.Reason != "the release engine owns remote refs" {
		t.Fatalf("NewGrant built %+v", g)
	}
	for class, wantExit := range map[string]int{ProcMutate: 0, FileWrite: 1} {
		app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
			if _, err := ctx.Effects().Run([]interface{}{"true"}, UseGrant("push")); err != nil {
				ctx.Error(err.Error())
				return Exit(1)
			}
			return Exit(0)
		}, WithGrants(NewGrant("push", "the release engine owns remote refs", class)))
		if r := app.Test([]string{"go"}); r.ExitCode != wantExit {
			t.Errorf("a run under a %s grant exited %d, want %d: %s", class, r.ExitCode, wantExit, r.Stderr)
		}
	}
}
