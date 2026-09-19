package strictcli

import (
	"fmt"
	"strings"
	"testing"
)

// Retired choices: spellings a value flag or positional arg used to accept,
// each carrying the message that names its replacement. The value-level twin of
// the deprecated-command construct. Parse-time refusal, registration-time
// guards, and the schema's `retired_choices` map.

// --- Parse time: the refusal ---

func TestRetiredChoiceOnAFlagIsRefused(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format",
			Choices(Ch("text", ""), Ch("json", "")),
			RetiredChoices(RetiredChoice("xml", "use 'json' (XML output was dropped)")),
			Required())))
	r := app.Test([]string{"cmd", "--format", "xml"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "--format: value 'xml' retired: use 'json' (XML output was dropped)"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr should contain %q, got %q", want, r.Stderr)
	}
}

func TestRetiredChoiceOnAnArgIsRefused(t *testing.T) {
	app := simpleApp("cmd", "a command", "mode={mode}",
		WithArgs(NewArg("mode", "the mode",
			ArgChoices(Ch("fast", ""), Ch("slow", "")),
			ArgRetiredChoices(RetiredChoice("turbo", "use 'fast' (turbo was renamed)")),
			ArgRequired())))
	r := app.Test([]string{"cmd", "turbo"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "argument 'mode': value 'turbo' retired: use 'fast' (turbo was renamed)"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr should contain %q, got %q", want, r.Stderr)
	}
}

// A retired spelling is checked BEFORE the invalid-value check, so the sentence
// a user reads names the replacement rather than listing the live choices.
func TestRetiredBeatsInvalidValue(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format",
			Choices(Ch("text", ""), Ch("json", "")),
			RetiredChoices(RetiredChoice("xml", "use 'json'")),
			Required())))
	r := app.Test([]string{"cmd", "--format", "xml"})
	if strings.Contains(r.Stderr, "must be one of") {
		t.Fatalf("the retired sentence must win over the invalid-value one, got %q", r.Stderr)
	}
}

// An unrelated bad value still takes the invalid-value sentence.
func TestANonRetiredBadValueStillTakesTheInvalidValueSentence(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format",
			Choices(Ch("text", ""), Ch("json", "")),
			RetiredChoices(RetiredChoice("xml", "use 'json'")),
			Required())))
	r := app.Test([]string{"cmd", "--format", "yaml"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "invalid value 'yaml', must be one of: text, json") {
		t.Fatalf("expected the invalid-value sentence, got %q", r.Stderr)
	}
}

func TestALiveValueIsStillAccepted(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format",
			Choices(Ch("text", ""), Ch("json", "")),
			RetiredChoices(RetiredChoice("xml", "use 'json'")),
			Required())))
	r := app.Test([]string{"cmd", "--format", "json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "format=json") {
		t.Fatalf("stdout should contain 'format=json', got %q", r.Stdout)
	}
}

// The retired check rides the same funnel every source resolves through, so an
// env-supplied retired value is refused exactly like a typed one.
func TestARetiredValueFromAnEnvVarIsRefused(t *testing.T) {
	t.Setenv("MYAPP_FORMAT", "xml")
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format",
			Choices(Ch("text", ""), Ch("json", "")),
			RetiredChoices(RetiredChoice("xml", "use 'json'")),
			Env("MYAPP_FORMAT"), Required())))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, "value 'xml' retired: use 'json'") {
		t.Fatalf("stderr should carry the retired sentence, got %q", r.Stderr)
	}
}

// An int-typed retired spelling formats through the ordinary error-value
// formatter, like every other value in a message.
func TestARetiredIntChoice(t *testing.T) {
	app := simpleApp("cmd", "a command", "port={port}",
		WithFlags(IntFlag("port", "the port",
			Choices(Ch(80, ""), Ch(443, "")),
			RetiredChoices(RetiredChoice(8080, "use 443 (the plaintext port was dropped)")),
			Required())))
	r := app.Test([]string{"cmd", "--port", "8080"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "--port: value '8080' retired: use 443 (the plaintext port was dropped)"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr should contain %q, got %q", want, r.Stderr)
	}
}

// A repeatable flag validates element-wise, so one retired element refuses the
// whole invocation.
func TestARetiredElementOfARepeatableFlag(t *testing.T) {
	app := simpleApp("cmd", "a command", "tags={tags}",
		WithFlags(StringFlag("tag", "a tag", Repeatable(), Unique(false),
			Choices(Ch("alpha", ""), Ch("beta", "")),
			RetiredChoices(RetiredChoice("gamma", "use 'beta'")),
			Default([]interface{}{}))))
	r := app.Test([]string{"cmd", "--tag", "alpha", "--tag", "gamma"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--tag: value 'gamma' retired: use 'beta'") {
		t.Fatalf("stderr should carry the retired sentence, got %q", r.Stderr)
	}
}

// --- Help output is unchanged: a retired value is not a choice ---

func TestHelpNeverListsARetiredValue(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format",
			Choices(Ch("text", ""), Ch("json", "")),
			RetiredChoices(RetiredChoice("xml", "use 'json'")),
			Required())))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if strings.Contains(r.Stdout, "xml") {
		t.Fatalf("help must never name a retired value, got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "text") || !strings.Contains(r.Stdout, "json") {
		t.Fatalf("help should still list the live choices, got %q", r.Stdout)
	}
}

// --- Registration-time hard errors ---

func expectRegistrationPanic(t *testing.T, want string, build func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected a registration panic containing %q, got none", want)
		}
		msg := fmt.Sprintf("%v", r)
		if !strings.Contains(msg, want) {
			t.Fatalf("panic message should contain %q, got %q", want, msg)
		}
	}()
	build()
}

func TestRetiredSpellingEqualToALiveChoicePanics(t *testing.T) {
	expectRegistrationPanic(t,
		`Flag "format": retired choice 'json' is also a live choice: a value is live or retired, never both`,
		func() {
			simpleApp("cmd", "a command", "x",
				WithFlags(StringFlag("format", "output format",
					Choices(Ch("text", ""), Ch("json", "")),
					RetiredChoices(RetiredChoice("json", "use 'text'")),
					Required())))
		})
}

func TestArgRetiredSpellingEqualToALiveChoicePanics(t *testing.T) {
	expectRegistrationPanic(t,
		`Arg "mode": retired choice 'fast' is also a live choice: a value is live or retired, never both`,
		func() {
			simpleApp("cmd", "a command", "x",
				WithArgs(NewArg("mode", "the mode",
					ArgChoices(Ch("fast", ""), Ch("slow", "")),
					ArgRetiredChoices(RetiredChoice("fast", "use 'slow'")),
					ArgRequired())))
		})
}

func TestDuplicateRetiredSpellingPanics(t *testing.T) {
	expectRegistrationPanic(t,
		`Flag "format": retired choice 'xml' is declared twice`,
		func() {
			simpleApp("cmd", "a command", "x",
				WithFlags(StringFlag("format", "output format",
					Choices(Ch("text", ""), Ch("json", "")),
					RetiredChoices(
						RetiredChoice("xml", "use 'json'"),
						RetiredChoice("xml", "use 'text'"),
					),
					Required())))
		})
}

func TestEmptyRetiredMessagePanics(t *testing.T) {
	expectRegistrationPanic(t,
		`Flag "format": retired choice 'xml': message must be a non-empty string`,
		func() {
			simpleApp("cmd", "a command", "x",
				WithFlags(StringFlag("format", "output format",
					Choices(Ch("text", ""), Ch("json", "")),
					RetiredChoices(RetiredChoice("xml", "")),
					Required())))
		})
}

func TestRetiredChoicesOnABoolPanics(t *testing.T) {
	expectRegistrationPanic(t,
		`Flag "cache": retired choices are incompatible with type=bool`,
		func() {
			simpleApp("cmd", "a command", "x",
				WithFlags(BoolFlag("cache", "use the cache",
					RetiredChoices(RetiredChoice("on", "use --cache")),
					Default(false))))
		})
}

func TestADefaultNamingARetiredSpellingPanics(t *testing.T) {
	expectRegistrationPanic(t,
		`Flag "format": default 'xml' is a retired choice`,
		func() {
			simpleApp("cmd", "a command", "x",
				WithFlags(StringFlag("format", "output format",
					Choices(Ch("text", ""), Ch("json", "")),
					RetiredChoices(RetiredChoice("xml", "use 'json'")),
					Default("xml"))))
		})
}

func TestArgDefaultNamingARetiredSpellingPanics(t *testing.T) {
	expectRegistrationPanic(t,
		`Arg "mode": default 'turbo' is a retired choice`,
		func() {
			simpleApp("cmd", "a command", "x",
				WithArgs(NewArg("mode", "the mode",
					ArgChoices(Ch("fast", ""), Ch("slow", "")),
					ArgRetiredChoices(RetiredChoice("turbo", "use 'fast'")),
					ArgDefault("turbo"))))
		})
}

// The seventh guard: a declaration that could never match. RetiredChoice(8080)
// on a string flag is dead -- the parse-time comparison is type-aware, so no
// invocation could reach it, and the spelling the author meant to retire stays
// accepted.
func TestRetiredChoiceOfTheWrongTypePanics(t *testing.T) {
	expectRegistrationPanic(t,
		`Flag "format": retired choice '8080' is not of type str`,
		func() {
			simpleApp("cmd", "a command", "x",
				WithFlags(StringFlag("format", "output format",
					Choices(Ch("text", ""), Ch("json", "")),
					RetiredChoices(RetiredChoice(8080, "use 'json'")),
					Required())))
		})
}

func TestArgRetiredChoiceOfTheWrongTypePanics(t *testing.T) {
	expectRegistrationPanic(t,
		`Arg "mode": retired choice '8080' is not of type str`,
		func() {
			simpleApp("cmd", "a command", "x",
				WithArgs(NewArg("mode", "the mode",
					ArgChoices(Ch("fast", ""), Ch("slow", "")),
					ArgRetiredChoices(RetiredChoice(8080, "use 'fast'")),
					ArgRequired())))
		})
}

func TestRetiredChoicesWithoutChoicesPanics(t *testing.T) {
	expectRegistrationPanic(t,
		`Flag "format": retired choices require choices`,
		func() {
			simpleApp("cmd", "a command", "x",
				WithFlags(StringFlag("format", "output format",
					RetiredChoices(RetiredChoice("xml", "use 'json'")),
					Required())))
		})
}

func TestArgRetiredChoicesWithoutChoicesPanics(t *testing.T) {
	expectRegistrationPanic(t,
		`Arg "mode": retired choices require choices`,
		func() {
			simpleApp("cmd", "a command", "x",
				WithArgs(NewArg("mode", "the mode",
					ArgRetiredChoices(RetiredChoice("turbo", "use 'fast'")),
					ArgRequired())))
		})
}

// --- The schema dump ---

func TestTheSchemaPublishesRetiredChoicesSortedByKey(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("cmd", "a command", noop, WithEffect(EffectReadOnly),
		WithFlags(StringFlag("format", "output format",
			Choices(Ch("text", ""), Ch("json", "")),
			RetiredChoices(
				RetiredChoice("xml", "use 'json'"),
				RetiredChoice("ascii", "use 'text'"),
			),
			Required())),
		WithArgs(NewArg("mode", "the mode",
			ArgChoices(Ch("fast", "")),
			ArgRetiredChoices(RetiredChoice("turbo", "use 'fast'")),
			ArgRequired())))

	text := dumpText(t, app)
	// Sorted ascending by key, the way a group's `deprecated` map is emitted.
	want := `          "retired_choices": {
            "ascii": "use 'text'",
            "xml": "use 'json'"
          }`
	if !strings.Contains(text, want) {
		t.Fatalf("the dump should carry the sorted retired_choices map, got:\n%s", text)
	}
	if !strings.Contains(text, `"retired_choices": {
            "turbo": "use 'fast'"
          }`) {
		t.Fatalf("the arg entry should carry its retired_choices map, got:\n%s", text)
	}
}

func TestTheSchemaOmitsRetiredChoicesWhenNoneAreDeclared(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("cmd", "a command", noop, WithEffect(EffectReadOnly),
		WithFlags(StringFlag("format", "output format",
			Choices(Ch("text", ""), Ch("json", "")), Required())))
	if strings.Contains(dumpText(t, app), "retired_choices") {
		t.Fatal("retired_choices must be omitted when nothing is retired")
	}
}

// The published `value_schema` enum is the LIVE set: a retired spelling is not
// a valid value, so nothing that validates against the fragment may name it.
func TestTheValueSchemaFragmentExcludesRetiredSpellings(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("cmd", "a command", noop, WithEffect(EffectReadOnly),
		WithFlags(StringFlag("format", "output format",
			Choices(Ch("text", ""), Ch("json", "")),
			RetiredChoices(RetiredChoice("xml", "use 'json'")),
			Required())))
	doc := dumpJSON(t, app)
	cmds := doc["commands"].(map[string]interface{})
	flags := cmds["cmd"].(map[string]interface{})["flags"].([]interface{})
	fragment := flags[0].(map[string]interface{})["value_schema"].(map[string]interface{})
	enum := fragment["enum"].([]interface{})
	if len(enum) != 2 {
		t.Fatalf("the enum should carry the two live choices, got %v", enum)
	}
	for _, v := range enum {
		if v == "xml" {
			t.Fatalf("a retired spelling must never reach the fragment, got %v", enum)
		}
	}
}

// The MCP tool projection reads the same fragment, so it excludes the retired
// spellings for the same reason.
func TestTheToolProjectionExcludesRetiredSpellings(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", noop, WithEffect(EffectReadOnly),
		WithFlags(StringFlag("format", "output format",
			Choices(Ch("text", ""), Ch("json", "")),
			RetiredChoices(RetiredChoice("xml", "use 'json'")),
			Required())))
	tools := app.AsTools()
	if len(tools) == 0 {
		t.Fatal("expected at least one tool descriptor")
	}
	rendered := fmt.Sprintf("%v", tools[0].Parameters)
	if strings.Contains(rendered, "xml") {
		t.Fatalf("a retired spelling must never reach the tool schema, got %s", rendered)
	}
}
