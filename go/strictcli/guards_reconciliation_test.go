package strictcli

import "testing"

// The corrections and spellings the three scoped-selector implementations
// forced (effects contract §18.19, items 213-224). Each test names the item it
// pins; the sentences and rendered forms are the contract's, not Go's.

// --- Item 219: the Arg twin of errChoicesEntryNotRecord ---

func TestArgChoicesBareValueReportsTheArgPrefix(t *testing.T) {
	defer func() {
		got := recover()
		want := `Arg "target": choices entry 0 is a bare value: declare it as Ch(<value>, "<help>")`
		if got != want {
			t.Fatalf("panic = %v, want %q", got, want)
		}
	}()
	NewArg("target", "what to push", ArgRequired(),
		ArgChoices(ChoiceValue{Value: "head"}))
}

func TestFlagChoicesBareValueKeepsTheFlagPrefix(t *testing.T) {
	defer func() {
		got := recover()
		want := `Flag "target": choices entry 1 is a bare value: declare it as Ch(<value>, "<help>")`
		if got != want {
			t.Fatalf("panic = %v, want %q", got, want)
		}
	}()
	StringFlag("target", "what to push", Optional(),
		Choices(Ch("head", "the head"), ChoiceValue{Value: "tags"}))
}

// --- Item 221: the two short-reuse guards, over SCOPES ---

func TestShortShapeMismatchAcrossSiblingScopes(t *testing.T) {
	defer func() {
		got := recover()
		want := `command "run": short '-t' is claimed by '--target' and '--tag' with different ` +
			`value shapes: sibling scopes may reuse a short only with an identical type and ` +
			`arity, because tokenizing '-t' cannot wait for an election`
		if got != want {
			t.Fatalf("panic = %v, want %q", got, want)
		}
	}()
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly), WithFlags(
		ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "mode a", StringFlag("target", "the target", Required(), Short("t"))),
			Choice("bb", "mode b", StringFlag("tag", "the tag", Required(), Short("t"), Repeatable(), Unique(false))),
		)))
}

func TestShortReuseIsLegalWhenSiblingScopesTokenizeIdentically(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly), WithFlags(
		ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "mode a", StringFlag("target", "the target", Required(), Short("t"))),
			Choice("bb", "mode b", StringFlag("tag", "the tag", Required(), Short("t"))),
		)))
	r := app.Test([]string{"run", "--mode", "aa", "-t", "xx"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
}

func TestShortOnAnAmbiguousElectionIsRefusedForANestedSelector(t *testing.T) {
	defer func() {
		got := recover()
		want := `command "run": short '-t' is reused by sibling scopes and also claimed by ` +
			`'--transport', which elects: an election token is read before any election has ` +
			`happened, so its short cannot be shared`
		if got != want {
			t.Fatalf("panic = %v, want %q", got, want)
		}
	}()
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly), WithFlags(
		ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "mode a",
				ChoiceFlag("transport", "the transport", Required(), Short("t"),
					Choice("tcp", "over TCP"),
					Choice("udp", "over UDP"),
				)),
			Choice("bb", "mode b", StringFlag("tag", "the tag", Required(), Short("t"))),
		)))
}

// The short-claim table must walk MEMBER scopes too: two member flags of one
// member-spelled selector are sibling scopes, and each elects by being typed.
func TestShortOnAnAmbiguousElectionIsRefusedForTwoMemberFlags(t *testing.T) {
	defer func() {
		got := recover()
		want := `command "run": short '-p' is reused by sibling scopes and also claimed by ` +
			`'--profile', which elects: an election token is read before any election has ` +
			`happened, so its short cannot be shared`
		if got != want {
			t.Fatalf("panic = %v, want %q", got, want)
		}
	}()
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly), WithFlags(
		MemberChoiceFlag("target", "which profiles", Required(),
			MemberChoice(StringFlag("profile", "one named profile", Required(), Short("p")), "one profile"),
			MemberChoice(StringFlag("pattern", "a glob", Required(), Short("p")), "matching profiles"),
		)))
}

// A short reused between two sibling MEMBER scopes' ordinary flags stays legal
// when the two declarations tokenize identically.
func TestShortReuseBetweenMemberScopesIsLegalWhenShapesAgree(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly), WithFlags(
		MemberChoiceFlag("target", "which profiles", Required(),
			MemberChoice(BoolFlag("one", "one profile", Required()), "one profile",
				StringFlag("name", "its name", Required(), Short("n"))),
			MemberChoice(BoolFlag("many", "many profiles", Required()), "many profiles",
				StringFlag("names", "their names", Required(), Short("n"))),
		)))
	r := app.Test([]string{"run", "--one", "-n", "xx"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
}
