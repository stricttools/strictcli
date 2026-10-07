package strictcli

// Coverage of the WithArgsAfterSeparator command declaration: the tokens after
// a bare "--" reach the handler through ctx.ArgsAfterSeparator() unparsed,
// the tokens before it parse as always, a command without the declaration
// keeps its positional reading of them, and the declaration shows in help,
// in the help document, in the tool schema, and on the programmatic door.

import (
	"reflect"
	"strings"
	"testing"
)

const separatorHelp = "arguments for the child, passed as they are"

// separatorApp builds a read-only "run" command that declares the receiver and
// a --name flag, recording what its handler saw into *got and *name.
func separatorApp(got *[]string, name *interface{}, opts ...CmdOption) *App {
	app := NewApp("app", "1.0.0", "separator fixture")
	all := append([]CmdOption{
		WithEffect(EffectReadOnly),
		WithArgsAfterSeparator("child-args", separatorHelp),
		WithFlags(StringFlag("name", "a name", Optional(), Short("n"))),
	}, opts...)
	app.Command("run", "run the child",
		func(ctx *Context, kwargs map[string]interface{}) Outcome {
			*got = ctx.ArgsAfterSeparator()
			*name = kwargs["name"]
			return Exit(0)
		}, all...)
	return app
}

func TestArgsAfterSeparatorReachTheContextUnparsed(t *testing.T) {
	var got []string
	var name interface{}
	app := separatorApp(&got, &name)
	r := app.Test([]string{"run", "--name", "x", "--", "--dry-run", "-n", "--help", "--", "plain"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s", r.ExitCode, r.Stderr)
	}
	want := []string{"--dry-run", "-n", "--help", "--", "plain"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ArgsAfterSeparator() = %q, want %q", got, want)
	}
	if name != "x" {
		t.Fatalf("--name = %v, want x", name)
	}
}

func TestArgsAfterSeparatorAreEmptyWithoutASeparator(t *testing.T) {
	var got []string
	var name interface{}
	app := separatorApp(&got, &name)
	for _, argv := range [][]string{{"run"}, {"run", "--"}} {
		got = nil
		if r := app.Test(argv); r.ExitCode != 0 {
			t.Fatalf("%q: exit %d: %s", argv, r.ExitCode, r.Stderr)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("%q: ArgsAfterSeparator() = %#v, want an empty non-nil slice", argv, got)
		}
	}
}

func TestAStrayPositionalBeforeTheSeparatorIsStillRefused(t *testing.T) {
	var got []string
	var name interface{}
	app := separatorApp(&got, &name)
	r := app.Test([]string{"run", "stray", "--", "a"})
	if r.ExitCode != 1 {
		t.Fatalf("exit %d, want 1", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unexpected argument 'stray'") {
		t.Fatalf("stderr = %q, want the unexpected-argument refusal", r.Stderr)
	}
	if got != nil {
		t.Fatalf("the handler ran: %q", got)
	}
}

func TestASeparatorThatIsAFlagValueIsTheValue(t *testing.T) {
	var got []string
	var name interface{}
	app := separatorApp(&got, &name)
	r := app.Test([]string{"run", "-n", "--", "--", "a"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s", r.ExitCode, r.Stderr)
	}
	if name != "--" {
		t.Fatalf("-n = %v, want --", name)
	}
	if !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("ArgsAfterSeparator() = %q, want [a]", got)
	}
}

func TestDeclaredArgsTakeTheTokensBeforeTheSeparator(t *testing.T) {
	var got []string
	var files interface{}
	app := NewApp("app", "1.0.0", "separator fixture")
	app.Command("run", "run the child",
		func(ctx *Context, kwargs map[string]interface{}) Outcome {
			got = ctx.ArgsAfterSeparator()
			files = kwargs["files"]
			return Exit(0)
		},
		WithEffect(EffectReadOnly),
		WithArgs(NewArg("files", "the files", Variadic(), ArgOptional())),
		WithArgsAfterSeparator("child-args", separatorHelp))
	r := app.Test([]string{"run", "a", "b", "--", "c", "d"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s", r.ExitCode, r.Stderr)
	}
	if !reflect.DeepEqual(files, []interface{}{"a", "b"}) {
		t.Fatalf("files = %v, want [a b]", files)
	}
	if !reflect.DeepEqual(got, []string{"c", "d"}) {
		t.Fatalf("ArgsAfterSeparator() = %q, want [c d]", got)
	}
}

func TestACommandWithoutTheDeclarationKeepsTokensAfterTheSeparatorAsPositionals(t *testing.T) {
	var files interface{}
	app := NewApp("app", "1.0.0", "separator fixture")
	app.Command("run", "run the child",
		func(ctx *Context, kwargs map[string]interface{}) Outcome {
			files = kwargs["files"]
			return Exit(0)
		},
		WithEffect(EffectReadOnly),
		WithArgs(NewArg("files", "the files", Variadic(), ArgOptional())))
	r := app.Test([]string{"run", "a", "--", "-b", "c"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s", r.ExitCode, r.Stderr)
	}
	if !reflect.DeepEqual(files, []interface{}{"a", "-b", "c"}) {
		t.Fatalf("files = %v, want [a -b c]", files)
	}
}

func TestArgsAfterSeparatorOnACommandWithoutTheDeclarationPanics(t *testing.T) {
	ctx := newContext(nil, nil, nil, nil, reservedFlags{}, nil)
	ctx.bindCommand(&Command{Name: "plain"})
	mustPanicWith(t, errArgsAfterSeparatorUndeclared("plain"), func() { ctx.ArgsAfterSeparator() })
}

// --- help, help document, tool schema ---

func TestCommandHelpShowsTheReceiverUnderArguments(t *testing.T) {
	var got []string
	var name interface{}
	app := separatorApp(&got, &name)
	r := app.Test([]string{"run", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s", r.ExitCode, r.Stderr)
	}
	want := "Arguments:\n  -- child-args...    " + separatorHelp + " [optional]\n"
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("help = %q, want it to contain %q", r.Stdout, want)
	}
}

func TestTheHelpDocumentPublishesTheReceiver(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("run", "run the child", noop,
		WithEffect(EffectReadOnly), WithArgsAfterSeparator("child-args", separatorHelp))
	app.Command("plain", "a plain command", noop, WithEffect(EffectReadOnly))
	doc := dumpJSON(t, app)
	commands := doc["commands"].(map[string]interface{})
	run := commands["run"].(map[string]interface{})
	want := map[string]interface{}{"name": "child-args", "help": separatorHelp}
	if !reflect.DeepEqual(run["args_after_separator"], want) {
		t.Fatalf("args_after_separator = %v, want %v", run["args_after_separator"], want)
	}
	if _, present := commands["plain"].(map[string]interface{})["args_after_separator"]; present {
		t.Fatal("args_after_separator emitted on a command that does not declare it")
	}
	defaults := doc["defaults"].(map[string]interface{})["command"].(map[string]interface{})
	if v, ok := defaults["args_after_separator"]; !ok || v != nil {
		t.Fatalf("command default args_after_separator = %v (present %v), want null", v, ok)
	}
}

func TestTheToolSchemaPublishesTheReceiverAsAStringList(t *testing.T) {
	var got []string
	var name interface{}
	app := separatorApp(&got, &name)
	schema := app.JsonSchema("run")
	prop := schema["properties"].(map[string]interface{})["child-args"]
	want := map[string]interface{}{
		"type":        "array",
		"items":       map[string]interface{}{"type": "string"},
		"description": separatorHelp,
	}
	if !reflect.DeepEqual(prop, want) {
		t.Fatalf("child-args property = %v, want %v", prop, want)
	}
	for _, req := range schema["required"].([]interface{}) {
		if req == "child-args" {
			t.Fatal("the receiver is listed as required")
		}
	}
}

// --- the programmatic door ---

func TestCallDeliversTheReceiverListUnderItsName(t *testing.T) {
	var got []string
	var name interface{}
	app := separatorApp(&got, &name)
	for _, raw := range []interface{}{[]string{"a", "--b"}, []interface{}{"a", "--b"}} {
		got = nil
		if _, err := app.Call("run", map[string]interface{}{"child-args": raw}); err != nil {
			t.Fatalf("Call(%#v): %v", raw, err)
		}
		if !reflect.DeepEqual(got, []string{"a", "--b"}) {
			t.Fatalf("Call(%#v): ArgsAfterSeparator() = %q", raw, got)
		}
	}
	got = nil
	if _, err := app.Call("run", map[string]interface{}{}); err != nil {
		t.Fatalf("Call without the receiver: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("Call without the receiver: ArgsAfterSeparator() = %#v, want empty", got)
	}
}

func TestCallRefusesAReceiverValueThatIsNotAStringList(t *testing.T) {
	var got []string
	var name interface{}
	app := separatorApp(&got, &name)
	for _, raw := range []interface{}{"a", []interface{}{"a", 1}, nil} {
		_, err := app.Call("run", map[string]interface{}{"child-args": raw})
		if err == nil || err.Error() != errArgsAfterSeparatorNotStrings("child-args", "run") {
			t.Fatalf("Call(%#v): err = %v, want %q", raw, err, errArgsAfterSeparatorNotStrings("child-args", "run"))
		}
	}
}

// --- registration ---

func TestArgsAfterSeparatorRegistrationRefusals(t *testing.T) {
	handler := func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) }
	cases := []struct {
		label string
		want  string
		opts  []CmdOption
	}{
		{"empty name", errArgsAfterSeparatorNameEmpty("run"),
			[]CmdOption{WithArgsAfterSeparator(" ", separatorHelp)}},
		{"empty help", errArgsAfterSeparatorHelpEmpty("run", "child-args"),
			[]CmdOption{WithArgsAfterSeparator("child-args", " ")}},
		{"declared twice", errArgsAfterSeparatorDeclaredTwice("run"),
			[]CmdOption{WithArgsAfterSeparator("child-args", separatorHelp), WithArgsAfterSeparator("more", separatorHelp)}},
		{"an arg's name", errArgsAfterSeparatorNameTaken("run", "files"),
			[]CmdOption{WithArgs(NewArg("files", "the files", ArgOptional())), WithArgsAfterSeparator("files", separatorHelp)}},
		{"a flag's parameter name", errArgsAfterSeparatorNameTaken("run", "child_args"),
			[]CmdOption{WithFlags(StringFlag("child-args", "a flag", Optional())), WithArgsAfterSeparator("child_args", separatorHelp)}},
		{"the consent parameter", errArgsAfterSeparatorNameTaken("run", "approve-consequential"),
			[]CmdOption{WithArgsAfterSeparator("approve-consequential", separatorHelp)}},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			app := NewApp("app", "1.0.0", "separator fixture")
			mustPanicWith(t, c.want, func() {
				app.Command("run", "run the child", handler,
					append([]CmdOption{WithEffect(EffectReadOnly)}, c.opts...)...)
			})
		})
	}
}

func TestArgsAfterSeparatorIsRefusedOnAPassthrough(t *testing.T) {
	app := NewApp("app", "1.0.0", "separator fixture")
	mustPanicWith(t, errCommandPassthroughCannotHave("wrap", "a receiver of the arguments after --"), func() {
		app.Passthrough("wrap", "forward to a child",
			func(ctx *Context, name string, args []string, globals map[string]interface{}) int { return 0 },
			WithEffect(EffectReadOnly), WithArgsAfterSeparator("child-args", separatorHelp))
	})
}
