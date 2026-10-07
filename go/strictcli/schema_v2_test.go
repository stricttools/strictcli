package strictcli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The schema format, version 2 (effects contract §25): the fragment subset, the
// arity rule, the choices and selector encodings, the byte canon, the key
// order, the rewritten defaults block and the behavioral-completeness keys.

func schemaTestApp(t *testing.T, opts ...AppOption) *App {
	t.Helper()
	chdirTemp(t)
	return NewApp("testapp", "1.0.0", "A test app", opts...)
}

// dumpText returns the exact bytes `help --json` prints for the whole app,
// with project_id -- the one line that names each language's own program --
// read as "testproject", so the fixtures below compare with the siblings'.
func dumpText(t *testing.T, app *App) string {
	t.Helper()
	r := app.Test([]string{"help", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("help --json: exit %d: %s", r.ExitCode, r.Stderr)
	}
	return strings.Replace(r.Stdout, `"project_id": "github.com/stricttools/strictcli/go",`, `"project_id": "testproject",`, 1)
}

func dumpJSON(t *testing.T, app *App) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(dumpText(t, app)), &out); err != nil {
		t.Fatalf("the dump is not valid JSON: %v", err)
	}
	return out
}

func noop(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }

// --- §25.8: the byte canon ---

// One small app, pinned as bytes: layout, key order and escaping in one
// assertion, because they are one encoding.
func TestTheWholeDocumentByteForByte(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("greet", "Greet — with ünicode & <html> and a/slash", noop,
		WithEffect(EffectReadOnly),
		WithFlags(FloatFlag("ratio", "The ratio", Default(1e-7))))

	text := dumpText(t, app)
	tail := text[strings.Index(text, `  "project_id"`):]
	want := `  "project_id": "testproject",
  "name": "testapp",
  "version": "1.0.0",
  "help": "A test app",
  "commands": {
    "greet": {
      "name": "greet",
      "help": "Greet — with ünicode & <html> and a/slash",
      "effect": "read_only",
      "flags": [
        {
          "name": "ratio",
          "help": "The ratio",
          "value_schema": {
            "type": "number"
          },
          "presence": "default",
          "default": 1e-7
        }
      ]
    }
  }
}
`
	if tail != want {
		t.Fatalf("the document's bytes changed:\n--- got ---\n%s\n--- want ---\n%s", tail, want)
	}
}

func TestNonASCIIIsRawAndHTMLSignificantCharactersAreLiteral(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "café <b> & </b> a/b", noop, WithEffect(EffectReadOnly))
	text := dumpText(t, app)
	if !strings.Contains(text, "café <b> & </b> a/b") {
		t.Fatalf("the help text was escaped:\n%s", text)
	}
	if strings.Contains(text, `\u`) {
		t.Fatalf("a character was escaped as \\uXXXX:\n%s", text)
	}
	if strings.Contains(text, `\/`) {
		t.Fatalf("a slash was escaped:\n%s", text)
	}
}

func TestAControlCharacterUsesJSONsOwnEscape(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "two\nlines\tapart", noop, WithEffect(EffectReadOnly))
	if !strings.Contains(dumpText(t, app), `"help": "two\nlines\tapart"`) {
		t.Fatalf("a control character did not use JSON's short escape")
	}
}

// encoding/json renders 1e-07 where the canonical float form writes 1e-7, which
// is the concrete defect §25.8 names: Go owns the formatter and did not use it
// where the bytes are committed.
func TestEveryFloatGoesThroughTheCanonicalFloatForm(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithFlags(
			FloatFlag("small", "A small one", Default(1e-7)),
			FloatFlag("big", "A big one", Default(1e21)),
			FloatFlag("whole", "A whole one", Default(2.0)),
		))
	text := dumpText(t, app)
	for _, want := range []string{`"default": 1e-7`, `"default": 1e+21`, `"default": 2.0`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "1e-07") {
		t.Fatalf("a float was rendered by encoding/json:\n%s", text)
	}
}

func TestIntegersAreBareTokens(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithFlags(IntFlag("count", "How many", Default(5))))
	text := dumpText(t, app)
	if !strings.Contains(text, "\"default\": 5\n") {
		t.Fatalf("an integer is not a bare token:\n%s", text)
	}
	if strings.Contains(text, `"default": 5.0`) {
		t.Fatalf("an integer was rendered as a float:\n%s", text)
	}
}

func TestTheLayoutIsTwoSpaceIndentOneMemberPerLine(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly))
	text := dumpText(t, app)
	if !strings.HasPrefix(text, "{\n  \"schema_version\": 2,\n  \"defaults\": {\n") {
		t.Fatalf("the document does not open in canonical layout:\n%s", text[:80])
	}
	// Empty containers are inline, never split across lines.
	for _, want := range []string{`"proc_observe_allowlist": [],`, `"commands": {},`} {
		if !strings.Contains(text, want) {
			t.Fatalf("an empty container was not rendered inline (%s)", want)
		}
	}
}

func TestExactlyOneTrailingNewline(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly))
	text := dumpText(t, app)
	if !strings.HasSuffix(text, "}\n") || strings.HasSuffix(text, "}\n\n") {
		t.Fatalf("the file does not end in exactly one newline: %q", text[len(text)-4:])
	}
}

// --- §25.9: the canonical key order ---

func keyOrder(t *testing.T, text, from string) []string {
	t.Helper()
	// The keys of one object, read off the rendered document at one indent.
	start := strings.Index(text, from)
	if start < 0 {
		t.Fatalf("%q is not in the document", from)
	}
	indent := ""
	for i := start - 1; i >= 0 && text[i] == ' '; i-- {
		indent += " "
	}
	start -= len(indent)
	var out []string
	depth := 0
	for _, line := range strings.Split(text[start:], "\n") {
		trimmed := strings.TrimSpace(line)
		if depth == 0 && strings.HasPrefix(line, indent+`"`) {
			out = append(out, strings.SplitN(strings.TrimPrefix(trimmed, `"`), `"`, 2)[0])
		}
		depth += strings.Count(trimmed, "{") + strings.Count(trimmed, "[")
		depth -= strings.Count(trimmed, "}") + strings.Count(trimmed, "]")
		if depth < 0 {
			break
		}
	}
	return out
}

func TestTheTopLevelOrder(t *testing.T) {
	app := schemaTestApp(t, WithEnvPrefix("MYAPP"), WithConfig(), WithConfigFormat("toml"))
	app.ConfigField("db.url", ConfigFieldHelp("Database URL"))
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly))
	app.Deprecated("old", "gone")
	text := dumpText(t, app)
	got := keyOrder(t, text, `"schema_version"`)
	want := []string{
		"schema_version", "defaults", "project_id", "name", "version", "help",
		"env_prefix", "config", "config_format", "commands", "groups",
		"deprecated", "config_fields",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("top-level key order = %v, want %v", got, want)
	}
}

func TestTheFlagEntryOrder(t *testing.T) {
	app := schemaTestApp(t, WithEnvPrefix("MYAPP"))
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithFlags(StringFlag("target", "The target", Short("t"),
			Default("prod"), Env("MYAPP_TARGET"), Prefixed(false),
			Choices(Ch("prod", "production"), Ch("dev", "")),
			ConflictMode("error"))))
	got := keyOrder(t, dumpText(t, app), `"name": "target"`)
	want := []string{
		"name", "help", "value_schema", "short", "presence", "default", "env",
		"prefixed", "choices", "conflict_mode",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("flag entry key order = %v, want %v", got, want)
	}
}

// A variadic arg refuses any default, so the order is pinned across two
// entries: their two shapes cover every key the arg entry can carry.
func TestTheArgEntryOrder(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("defaulted", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithArgs(NewArg("env", "The environment", ArgDefault("dev"),
			ArgChoices(Ch("dev", "the dev one"), Ch("prod", "")))))
	app.Command("variadic", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithArgs(NewArg("files", "The files", Variadic(), ArgRequired(),
			ArgChoices(Ch("a", "the a one"), Ch("b", "")))))
	text := dumpText(t, app)
	got := keyOrder(t, text, `"name": "env"`)
	want := []string{"name", "help", "value_schema", "presence", "default", "choices"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("defaulted arg key order = %v, want %v", got, want)
	}
	got = keyOrder(t, text, `"name": "files"`)
	want = []string{"name", "help", "value_schema", "presence", "variadic", "choices"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("variadic arg key order = %v, want %v", got, want)
	}
}

func TestCommandsKeepDeclarationOrderAndDeprecatedIsSorted(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("zebra", "Last declared, first alphabetically it is not", noop, WithEffect(EffectReadOnly))
	app.Command("alpha", "Declared second", noop, WithEffect(EffectReadOnly))
	app.Deprecated("zed", "gone")
	app.Deprecated("abacus", "gone")
	text := dumpText(t, app)
	if strings.Index(text, `"zebra"`) > strings.Index(text, `"alpha"`) {
		t.Fatalf("commands lost their declaration order:\n%s", text)
	}
	if strings.Index(text, `"abacus"`) > strings.Index(text, `"zed"`) {
		t.Fatalf("deprecated entries are not sorted by key:\n%s", text)
	}
}

// --- §25.2, §25.3: the fragment subset and the arity rule ---

func TestARepeatableScalarPublishesTheArrayFragment(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithFlags(
			StringFlag("tag", "A tag", Repeatable(), Unique(false), Default([]interface{}{})),
			ListFlag(TypeStr, "label", "A label", Unique(false), Default([]interface{}{})),
		))
	data := dumpJSON(t, app)
	flags := data["commands"].(map[string]interface{})["noop"].(map[string]interface{})["flags"].([]interface{})
	repeatable := flags[0].(map[string]interface{})
	list := flags[1].(map[string]interface{})
	want := map[string]interface{}{
		"type":  "array",
		"items": map[string]interface{}{"type": "string"},
	}
	for i, entry := range []map[string]interface{}{repeatable, list} {
		frag := entry["value_schema"].(map[string]interface{})
		if len(frag) != 2 || frag["type"] != want["type"] ||
			frag["items"].(map[string]interface{})["type"] != "string" {
			t.Fatalf("entry %d fragment = %v, want %v", i, frag, want)
		}
		if _, present := entry["repeatable"]; present {
			t.Fatalf("the `repeatable` key survived: %v", entry)
		}
	}
}

func TestAnOptionalFlagEmitsThePlainTypeWithNoNull(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithFlags(StringFlag("target", "The target", Optional())))
	text := dumpText(t, app)
	if !strings.Contains(text, `"value_schema": {
            "type": "string"
          }`) {
		t.Fatalf("an optional flag's fragment is not the plain type:\n%s", text)
	}
	if strings.Contains(text, `"null"`) {
		t.Fatalf("a fragment carries null:\n%s", text)
	}
}

func TestAVariadicArgPublishesTheArrayFragmentInEitherSpelling(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("scalar", "Element carrier", noop, WithEffect(EffectReadOnly),
		WithArgs(NewArg("files", "The files", Variadic(), ArgRequired())))
	app.Command("carrier", "List carrier", noop, WithEffect(EffectReadOnly),
		WithArgs(NewArg("files", "The files", ArgType(ListOf(TypeStr)), Variadic(), ArgRequired())))
	data := dumpJSON(t, app)
	cmds := data["commands"].(map[string]interface{})
	for _, name := range []string{"scalar", "carrier"} {
		args := cmds[name].(map[string]interface{})["args"].([]interface{})
		frag := args[0].(map[string]interface{})["value_schema"].(map[string]interface{})
		if frag["type"] != "array" || frag["items"].(map[string]interface{})["type"] != "string" {
			t.Fatalf("%s: fragment = %v", name, frag)
		}
	}
}

// --- §25.5: the enum in the fragment, the records in the sibling key ---

func TestChoicesSplitIntoTheEnumAndTheRecords(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithFlags(StringFlag("target", "The target", Optional(),
			Choices(Ch("head", "the current commit only"), Ch("branches", "")))))
	data := dumpJSON(t, app)
	flag := data["commands"].(map[string]interface{})["noop"].(map[string]interface{})["flags"].([]interface{})[0].(map[string]interface{})
	frag := flag["value_schema"].(map[string]interface{})
	enum := frag["enum"].([]interface{})
	if len(enum) != 2 || enum[0] != "head" || enum[1] != "branches" {
		t.Fatalf("enum = %v", enum)
	}
	records := flag["choices"].([]interface{})
	first := records[0].(map[string]interface{})
	second := records[1].(map[string]interface{})
	if first["value"] != "head" || first["help"] != "the current commit only" {
		t.Fatalf("first record = %v", first)
	}
	// `help` is omitted when the entry declares none: an empty string and an
	// absent one must not produce different bytes for one declaration.
	if len(second) != 1 || second["value"] != "branches" {
		t.Fatalf("second record = %v, want just its value", second)
	}
}

func TestAnArrayShapedCarriersEnumLivesInsideItems(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithFlags(StringFlag("tag", "A tag", Repeatable(), Unique(false), Optional(),
			Choices(Ch("a", ""), Ch("b", "")))))
	data := dumpJSON(t, app)
	flag := data["commands"].(map[string]interface{})["noop"].(map[string]interface{})["flags"].([]interface{})[0].(map[string]interface{})
	frag := flag["value_schema"].(map[string]interface{})
	if _, present := frag["enum"]; present {
		t.Fatalf("the enum sits at the fragment root: %v", frag)
	}
	items := frag["items"].(map[string]interface{})
	if len(items["enum"].([]interface{})) != 2 {
		t.Fatalf("items = %v", items)
	}
}

func TestAChoiceValueKeepsItsOwnType(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithFlags(
			IntFlag("count", "How many", Optional(), Choices(Ch(1, ""), Ch(2, ""))),
			FloatFlag("ratio", "The ratio", Optional(), Choices(Ch(0.5, ""), Ch(1.5, ""))),
		))
	text := dumpText(t, app)
	for _, want := range []string{`"value": 1`, `"value": 0.5`, `"enum": [`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"value": "1"`) {
		t.Fatalf("a choice value was stringified:\n%s", text)
	}
}

// --- §25.6: the selector encoding ---

// The whole `commands` block, verbatim: each scoped entry is a FULL flag entry
// with its own fragment and presence, which is what makes the encoding satisfy
// §24.11 rather than gesture at it, and what makes recursion free. These are
// the same bytes the Python implementation writes for the same declaration.
func TestTheSelectorEntryIsPublishedNested(t *testing.T) {
	app := schemaTestApp(t)
	// read_only: the entry under test is the SELECTOR's shape, and the two
	// declared defaults below would be refused on a mutating command by
	// §27.1's ban -- at every depth, the scoped one included.
	app.Command("send", "send one notification", noop, WithEffect(EffectReadOnly),
		WithFlags(
			ChoiceFlag("via", "delivery channel", Required(), Short("v"),
				Choice("email", "deliver the notification as an email message",
					StringFlag("subject", "subject line of the message", Required()),
					StringFlag("recipient", "destination email address", Required()),
				),
				Choice("webhook", "post the notification to a URL",
					IntFlag("retries", "delivery attempts before giving up", Default(3)),
				),
			),
			BoolFlag("dry", "print what would be sent", Default(false)),
		))
	text := dumpText(t, app)
	got := text[strings.Index(text, "\n  \"commands\"")+1:]
	want := `  "commands": {
    "send": {
      "name": "send",
      "help": "send one notification",
      "effect": "read_only",
      "flags": [
        {
          "name": "via",
          "help": "delivery channel",
          "short": "v",
          "presence": "required",
          "choices": [
            {
              "name": "email",
              "help": "deliver the notification as an email message",
              "flags": [
                {
                  "name": "subject",
                  "help": "subject line of the message",
                  "value_schema": {
                    "type": "string"
                  },
                  "presence": "required"
                },
                {
                  "name": "recipient",
                  "help": "destination email address",
                  "value_schema": {
                    "type": "string"
                  },
                  "presence": "required"
                }
              ]
            },
            {
              "name": "webhook",
              "help": "post the notification to a URL",
              "flags": [
                {
                  "name": "retries",
                  "help": "delivery attempts before giving up",
                  "value_schema": {
                    "type": "integer"
                  },
                  "presence": "default",
                  "default": 3
                }
              ]
            }
          ],
          "elect_by": "selector-token"
        },
        {
          "name": "dry",
          "help": "print what would be sent",
          "value_schema": {
            "type": "boolean"
          },
          "presence": "default",
          "default": false,
          "negatable": true
        }
      ]
    }
  }
}
`
	if got != want {
		t.Fatalf("the selector encoding's bytes changed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestANestedSelectorIsAnEntryInsideAScope(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("add", "add an entry", noop, WithEffect(EffectMutating),
		WithFlags(ChoiceFlag("visibility", "who the entry is for", Required(),
			Choice("user-facing", "shown to users",
				ChoiceFlag("type", "what kind of change", Required(),
					Choice("feature", "a new capability",
						StringFlag("headline", "the headline", Required())),
					Choice("fix", "a bug fix",
						StringFlag("symptom", "the symptom", Required())),
				)),
			Choice("internal", "not shown to users"),
		)))
	data := dumpJSON(t, app)
	outer := data["commands"].(map[string]interface{})["add"].(map[string]interface{})["flags"].([]interface{})[0].(map[string]interface{})
	if outer["name"] != "visibility" {
		t.Fatalf("outer = %v", outer["name"])
	}
	inner := outer["choices"].([]interface{})[0].(map[string]interface{})["flags"].([]interface{})[0].(map[string]interface{})
	if inner["name"] != "type" || inner["elect_by"] != "selector-token" {
		t.Fatalf("the nested selector is not a full entry: %v", inner)
	}
	if _, present := inner["value_schema"]; present {
		t.Fatalf("a nested selector carries a fragment: %v", inner)
	}
	// An empty scope has no `flags` key at all (the block's `choice` baseline).
	internal := outer["choices"].([]interface{})[1].(map[string]interface{})
	if len(internal) != 2 {
		t.Fatalf("an empty scope published a `flags` key: %v", internal)
	}
}

func TestAMemberPayloadIsTheFirstScopeEntryNamedValue(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("run", "run it", noop, WithEffect(EffectReadOnly),
		WithFlags(MemberChoiceFlag("target", "which profiles to operate on", Required(),
			MemberChoice(StringFlag("profile", "profile name", Required()),
				"operate on one named profile",
				BoolFlag("create-missing", "create the profile when absent", Default(false))),
			MemberChoice(BoolFlag("all-profiles", "every profile", Required()),
				"operate on every profile"),
		)))
	data := dumpJSON(t, app)
	sel := data["commands"].(map[string]interface{})["run"].(map[string]interface{})["flags"].([]interface{})[0].(map[string]interface{})
	if sel["elect_by"] != "member-flags" {
		t.Fatalf("elect_by = %v", sel["elect_by"])
	}
	scope := sel["choices"].([]interface{})[0].(map[string]interface{})["flags"].([]interface{})
	payload := scope[0].(map[string]interface{})
	if payload["name"] != "value" || payload["presence"] != "required" {
		t.Fatalf("the payload entry = %v", payload)
	}
	if payload["help"] != "profile name" ||
		payload["value_schema"].(map[string]interface{})["type"] != "string" {
		t.Fatalf("the payload is not a full flag entry: %v", payload)
	}
	// A bool-typed scoped flag emits `negatable`, because a scoped entry IS a
	// full flag entry.
	scoped := scope[1].(map[string]interface{})
	if scoped["name"] != "create-missing" || scoped["negatable"] != true {
		t.Fatalf("the scoped bool entry = %v", scoped)
	}
	// A payload-less member has no `value` entry: electing it is the whole of
	// what it says.
	allProfiles := sel["choices"].([]interface{})[1].(map[string]interface{})
	if _, present := allProfiles["flags"]; present {
		t.Fatalf("a payload-less member published a scope: %v", allProfiles)
	}
}

func TestADefaultedSelectorPublishesTheFlatMap(t *testing.T) {
	app := schemaTestApp(t)
	// read_only: the fact under test is the DEFAULTED SELECTOR's flat map, and
	// the scoped defaults it is built from are refused on a mutating command by
	// §27.1's ban.
	app.Command("send", "send one", noop, WithEffect(EffectReadOnly),
		WithFlags(ChoiceFlag("via", "delivery channel", Default("webhook"),
			Choice("webhook", "post to a URL",
				StringFlag("url", "the endpoint", Default("https://example.test/hook")),
				IntFlag("retries", "how many times to retry", Default(5)),
				StringFlag("token", "an auth token", Optional()),
				ChoiceFlag("encoding", "the body encoding", Default("json"),
					Choice("json", "JSON body"),
					Choice("form", "form-encoded body"),
				),
			),
			Choice("email", "by email", StringFlag("subject", "the subject", Required())),
		)))
	data := dumpJSON(t, app)
	sel := data["commands"].(map[string]interface{})["send"].(map[string]interface{})["flags"].([]interface{})[0].(map[string]interface{})
	if sel["presence"] != "default" {
		t.Fatalf("presence = %v", sel["presence"])
	}
	got := sel["default"].(map[string]interface{})
	// A field with no value is omitted, and a nested selector is excluded: it
	// publishes its own default on its own entry.
	if len(got) != 3 || got["choice"] != "webhook" ||
		got["url"] != "https://example.test/hook" || got["retries"] != float64(5) {
		t.Fatalf("the flat map = %v", got)
	}
}

// --- §25.10: the defaults block, rewritten ---

func TestTheDefaultsBlockIsTheCompleteOmissionMap(t *testing.T) {
	defaults := toPlain(buildSchemaDefaults()).(map[string]interface{})
	if defaults["schema_version"] != 2 {
		t.Fatalf("schema_version = %v", defaults["schema_version"])
	}
	flag := defaults["flag"].(map[string]interface{})
	for _, gone := range []string{"hidden", "default", "repeatable"} {
		if _, present := flag[gone]; present {
			t.Fatalf("flag baseline %q survived: %v", gone, flag)
		}
	}
	if flag["prefixed"] != true || flag["elect_by"] != nil {
		t.Fatalf("flag baselines = %v", flag)
	}
	arg := defaults["arg"].(map[string]interface{})
	if len(arg) != 2 || arg["variadic"] != false || arg["choices"] != nil {
		t.Fatalf("arg baselines = %v", arg)
	}
	// The two choice entities are what make the block a complete omission map.
	choice := defaults["choice"].(map[string]interface{})
	if len(choice["flags"].([]interface{})) != 0 {
		t.Fatalf("choice baselines = %v", choice)
	}
	record := defaults["choice_record"].(map[string]interface{})
	if v, ok := record["help"]; !ok || v != nil {
		t.Fatalf("choice_record baselines = %v", record)
	}
	app := defaults["app"].(map[string]interface{})
	for _, key := range []string{"config_format", "config_path", "config_conflict_mode", "checks", "config_fields", "infra"} {
		if _, present := app[key]; !present {
			t.Fatalf("app baseline %q is missing: %v", key, app)
		}
	}
	command := defaults["command"].(map[string]interface{})
	if _, present := command["flag_sets"]; !present {
		t.Fatalf("command baselines lost flag_sets: %v", command)
	}
	for _, entity := range []string{"config_field", "check", "infra"} {
		if _, present := defaults[entity]; !present {
			t.Fatalf("entity %q is missing from the block", entity)
		}
	}
}

// --- §25.11: behavioral completeness ---

func TestTheConfigKeysAreAbsentAtTheirBaselines(t *testing.T) {
	app := schemaTestApp(t, WithConfig())
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly))
	data := dumpJSON(t, app)
	for _, key := range []string{"config_format", "config_path", "config_conflict_mode"} {
		if _, present := data[key]; present {
			t.Fatalf("%q was emitted at its baseline", key)
		}
	}
}

func TestANonDefaultConfigFormatAndConflictModeAppear(t *testing.T) {
	app := schemaTestApp(t, WithConfig(), WithConfigFormat("toml"), WithConfigConflictMode("error"))
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly))
	data := dumpJSON(t, app)
	if data["config_format"] != "toml" || data["config_conflict_mode"] != "error" {
		t.Fatalf("the departures were not published: %v", data)
	}
}

func TestALiteralConfigPathIsPublishedAsDeclared(t *testing.T) {
	app := schemaTestApp(t, WithConfig(), WithConfigPath("/opt/data/config.json"))
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly))
	if got := dumpJSON(t, app)["config_path"]; got != "/opt/data/config.json" {
		t.Fatalf("config_path = %v", got)
	}
}

// The DECLARATION, never the machine's resolution of it.
func TestARelativeToRootConfigPathPublishesTheDeclaration(t *testing.T) {
	app := schemaTestApp(t, WithInfraRoot("TESTAPP_HOME", "~/.testapp"), WithConfig(),
		WithConfigPathRelativeToRoot("TESTAPP_HOME", "config.json"))
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly))
	got := dumpJSON(t, app)["config_path"].(map[string]interface{})["relative_to_root"].(map[string]interface{})
	if got["env_var"] != "TESTAPP_HOME" {
		t.Fatalf("config_path = %v", got)
	}
	if parts := got["parts"].([]interface{}); len(parts) != 1 || parts[0] != "config.json" {
		t.Fatalf("parts = %v", got["parts"])
	}
}

func TestPrefixedIsOmittedWhenTrueAndEmittedWhenFalse(t *testing.T) {
	app := schemaTestApp(t, WithEnvPrefix("MYAPP"))
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithFlags(
			StringFlag("inside", "Prefixed", Optional(), Env("MYAPP_INSIDE")),
			StringFlag("outside", "Not prefixed", Optional(), Env("FOREIGN"), Prefixed(false)),
		))
	data := dumpJSON(t, app)
	flags := data["commands"].(map[string]interface{})["noop"].(map[string]interface{})["flags"].([]interface{})
	if _, present := flags[0].(map[string]interface{})["prefixed"]; present {
		t.Fatalf("prefixed was emitted at its baseline: %v", flags[0])
	}
	if flags[1].(map[string]interface{})["prefixed"] != false {
		t.Fatalf("prefixed was not emitted on the departure: %v", flags[1])
	}
}

func TestFlagSetsRecordTheGroupingV1Discarded(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithFlags(StringFlag("own", "Its own flag", Optional())),
		WithFlagSets(FlagSet{Name: "common", Flags: []Flag{
			BoolFlag("verbose-output", "Say more", Optional()),
			StringFlag("region", "Where", Optional()),
		}}))
	data := dumpJSON(t, app)
	cmd := data["commands"].(map[string]interface{})["noop"].(map[string]interface{})
	sets := cmd["flag_sets"].([]interface{})
	if len(sets) != 1 {
		t.Fatalf("flag_sets = %v", sets)
	}
	set := sets[0].(map[string]interface{})
	names := set["flags"].([]interface{})
	if set["name"] != "common" || len(names) != 2 || names[0] != "verbose-output" {
		t.Fatalf("the set = %v", set)
	}
	// The members keep their ordinary entries: the key adds a grouping without
	// duplicating a declaration.
	if len(cmd["flags"].([]interface{})) != 3 {
		t.Fatalf("the member flags left the flag list: %v", cmd["flags"])
	}
}

func TestFlagSetsIsAbsentWhenTheCommandDeclaresNone(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly))
	cmd := dumpJSON(t, app)["commands"].(map[string]interface{})["noop"].(map[string]interface{})
	if _, present := cmd["flag_sets"]; present {
		t.Fatalf("flag_sets was emitted at its baseline: %v", cmd)
	}
}

// --- §25.7: the dumped checks block is a function of the declaration alone ---

func TestTheChecksBlockExcludesProviderSourcedNames(t *testing.T) {
	dir := t.TempDir()
	checksPath := filepath.Join(dir, "checks.toml")
	os.WriteFile(checksPath, []byte(`app = "testapp"

[checks.declared-one]
description = "Checks declared-one"
subject = "quality"
tags = ["pre-release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
`), 0o644)
	app := schemaTestApp(t, WithChecks(checksPath))
	app.RegisterErrorCheck("declared-one", func(ctx CheckContext, r *ErrorReporter) CheckOutcome {
		return r.Passed("ok")
	})
	app.RegisterCheckProvider(func() []CheckSpec {
		return []CheckSpec{NewErrorCheckSpec(
			CheckSpecMeta{Name: "provided-one", Tags: []string{"pre-release"}, Severity: "error"},
			func(ctx CheckContext, r *ErrorReporter) CheckOutcome { return r.Passed("ok") },
		)}
	})
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: dir} })
	// A provider materializes into the same registry lazily, so a dump taken
	// after a check run used to differ from one taken before it.
	app.Test([]string{"check", "--all"})
	checks := dumpJSON(t, app)["checks"].(map[string]interface{})
	if _, present := checks["provided-one"]; present {
		t.Fatalf("a provider-sourced check reached the dump: %v", checks)
	}
	if _, present := checks["declared-one"]; !present {
		t.Fatalf("the declared check is missing: %v", checks)
	}
}

// An app whose only checks are provider-sourced publishes no block at all
// rather than an empty one, which is the baseline the `defaults` block states.
func TestTheChecksBlockIsOmittedWhenEveryCheckIsProviderSourced(t *testing.T) {
	app := schemaTestApp(t)
	app.RegisterCheckProvider(func() []CheckSpec {
		return []CheckSpec{NewErrorCheckSpec(
			CheckSpecMeta{Name: "provided-one", Tags: []string{"pre-release"}, Severity: "error"},
			func(ctx CheckContext, r *ErrorReporter) CheckOutcome { return r.Passed("ok") },
		)}
	})
	app.SetCheckContext(func() CheckContext { return &testCheckContext{root: t.TempDir()} })
	app.Test([]string{"check", "--all"})
	if _, present := dumpJSON(t, app)["checks"]; present {
		t.Fatalf("an all-provider app published a checks block")
	}
}

// --- §12.14: the 2^53 registration guard ---

// wideInt converts v to an int for a test whose values only exist where int is
// 64 bits wide, skipping the test on targets where int is 32 bits: there no int
// reaches 2^53 and the guard cannot fire. v passes through a variable because
// an untyped constant beyond 2^31 does not compile as an int on those targets.
func wideInt(t *testing.T, v int64) int {
	t.Helper()
	if strconv.IntSize < 64 {
		t.Skip("int is 32 bits wide on this target")
	}
	return int(v)
}

func TestAnIntChoiceBeyond2To53IsRefusedOnAFlag(t *testing.T) {
	beyond := wideInt(t, 9007199254740993)
	defer func() {
		got := recover()
		want := `Flag "id": choice 9007199254740993: the number's magnitude exceeds 2^53 ` +
			`(declare a big identifier as a string)`
		if got != want {
			t.Fatalf("panic = %v, want %q", got, want)
		}
	}()
	IntFlag("id", "an identifier", Optional(), Choices(Ch(beyond, ""), Ch(1, "")))
}

func TestAnIntChoiceBeyond2To53IsRefusedOnAnArg(t *testing.T) {
	beyond := wideInt(t, -9007199254740993)
	defer func() {
		got := recover()
		want := `Arg "id": choice -9007199254740993: the number's magnitude exceeds 2^53 ` +
			`(declare a big identifier as a string)`
		if got != want {
			t.Fatalf("panic = %v, want %q", got, want)
		}
	}()
	NewArg("id", "an identifier", ArgType(TypeInt), ArgRequired(),
		ArgChoices(Ch(beyond, ""), Ch(1, "")))
}

// Exactly 2^53 is representable, so it is not refused; float choices are
// deliberately exempt, because the canonical float form round-trips exactly.
func TestTheMagnitudeGuardFiresOnlyWhereInformationIsLost(t *testing.T) {
	IntFlag("id", "an identifier", Optional(), Choices(Ch(wideInt(t, 9007199254740992), ""), Ch(wideInt(t, -9007199254740992), "")))
	FloatFlag("ratio", "a ratio", Optional(), Choices(Ch(1e300, ""), Ch(-1e300, "")))
}

// --- §25.4: one declaration, one published shape ---

// A variadic arg declared with the list-typed spelling may carry choices, the
// same way the scalar spelling always could: once the two spellings are one
// declaration with one published shape, a ban that fires on one of them is two
// rules for one fact.
func TestAListTypedVariadicArgMayCarryChoices(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithArgs(NewArg("envs", "the environments", ArgType(ListOf(TypeStr)), Variadic(),
			ArgRequired(), ArgChoices(Ch("dev", ""), Ch("prod", "")))))
	data := dumpJSON(t, app)
	arg := data["commands"].(map[string]interface{})["noop"].(map[string]interface{})["args"].([]interface{})[0].(map[string]interface{})
	items := arg["value_schema"].(map[string]interface{})["items"].(map[string]interface{})
	if len(items["enum"].([]interface{})) != 2 {
		t.Fatalf("the enum did not reach items: %v", arg["value_schema"])
	}
}

// --- §25.13: the MCP projection derives from the same arity rule ---

func TestTheToolSchemaCarriesAnArrayEnumInsideItems(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly),
		WithFlags(StringFlag("tag", "A tag", Repeatable(), Unique(false), Optional(),
			Choices(Ch("a", ""), Ch("b", "")))),
		WithArgs(NewArg("envs", "the environments", Variadic(), ArgRequired(),
			ArgChoices(Ch("dev", ""), Ch("prod", "")))))
	schema := app.JsonSchema("noop")
	props := schema["properties"].(map[string]interface{})
	for _, name := range []string{"tag", "envs"} {
		prop := props[name].(map[string]interface{})
		if prop["type"] != "array" {
			t.Fatalf("%s: type = %v", name, prop["type"])
		}
		if _, present := prop["enum"]; present {
			t.Fatalf("%s: the enum sits at the property root: %v", name, prop)
		}
		items := prop["items"].(map[string]interface{})
		if len(items["enum"].([]interface{})) != 2 {
			t.Fatalf("%s: items = %v", name, items)
		}
	}
}

// The Python implementation writes these exact bytes for the same declaration,
// which is what §25.8's whole point is: a repository whose schema file is
// written sometimes by one implementation and sometimes by another sees a diff
// exactly when something changed. The fixture is the minimal app, so what it
// pins is the `defaults` block -- the largest fixed region of every dump.
func TestTheDefaultsBlockMatchesTheSiblingImplementationsBytes(t *testing.T) {
	app := schemaTestApp(t)
	app.Command("noop", "Does nothing", noop, WithEffect(EffectReadOnly))
	got := dumpText(t, app)
	want := pythonMinimalDump
	if got != want {
		t.Fatalf("the dump diverged from the sibling implementations:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// pythonMinimalDump is the Python implementation's dump of the minimal app,
// captured verbatim.
const pythonMinimalDump = `{
  "schema_version": 2,
  "defaults": {
    "schema_version": 2,
    "app": {
      "env_prefix": null,
      "config": false,
      "config_format": "json",
      "config_path": null,
      "config_conflict_mode": "cli-wins",
      "proc_observe_allowlist": [],
      "global_flags": [],
      "commands": {},
      "groups": {},
      "deprecated": {},
      "tag_contracts": {},
      "checks": {},
      "config_fields": {},
      "infra": {}
    },
    "flag": {
      "short": null,
      "env": null,
      "env_separator": null,
      "prefixed": true,
      "choices": null,
      "elect_by": null,
      "unique": false,
      "conflict_mode": null,
      "negatable": null,
      "nullable": false
    },
    "arg": {
      "variadic": false,
      "choices": null
    },
    "choice": {
      "flags": []
    },
    "choice_record": {
      "help": null
    },
    "command": {
      "consequential": false,
      "dry_run_supported": true,
      "dry_run_unsupported_reason": null,
      "update_of": null,
      "write_mode": null,
      "payload_schema": null,
      "owns_stdout": false,
      "passthrough": false,
      "flags": [],
      "flag_sets": [],
      "args": [],
      "args_after_separator": null,
      "tags": [],
      "constraints": [],
      "hidden": false,
      "interactive": false,
      "config_fields": [],
      "grants": [],
      "forwarding": null,
      "requires": []
    },
    "group": {
      "commands": {},
      "groups": {},
      "deprecated": {},
      "tags": [],
      "hidden": false
    },
    "config_field": {
      "default": null,
      "bound_commands": []
    },
    "check": {
      "scope": null
    },
    "infra": {
      "roots": [],
      "handshakes": [],
      "connections": []
    }
  },
  "project_id": "testproject",
  "name": "testapp",
  "version": "1.0.0",
  "help": "A test app",
  "commands": {
    "noop": {
      "name": "noop",
      "help": "Does nothing",
      "effect": "read_only"
    }
  }
}
`

// A richer declaration, pinned against the Python implementation's bytes for
// the same app: choices with and without help, both array spellings, a dict
// carrier, a RelativeToRoot default, a member-spelled selector with a payload,
// an arg block, a constraint, a grant and the infra block. This is what §25.8
// buys -- one canon, three writers, one diff.
func TestARichDeclarationMatchesTheSiblingImplementationsBytes(t *testing.T) {
	app := schemaTestApp(t,
		WithEnvPrefix("MYAPP"),
		WithProcObserveAllowlist([][]string{{"git", "status"}}),
		WithInfraRoot("MYAPP_HOME", "~/.myapp"),
		WithHandshakeEnv("MYAPP_PARENT", "set by the invoking process"),
	)
	app.Command("deploy", "Deploy it", noop,
		WithEffect(EffectMutating), WithConsequential(), WithTags("release"),
		WithGrants(Grant{Name: "write", Reason: "writes the release", Kind: "file_write"}),
		WithConstraints(Requires("region-needs-target", "region", "target")),
		WithFlags(
			// Optional(), not Default("prod"): the command is mutating, and
			// §27.1's ban refuses a declared value default on one -- at every
			// depth, which is why `create-missing` below moved too. The empty
			// collection defaults and the RelativeToRoot default stay, being
			// the ban's two carve-outs.
			StringFlag("target", "Where to deploy", Short("t"), Optional(),
				Env("MYAPP_TARGET"), Choices(Ch("prod", "production"), Ch("dev", ""))),
			StringFlag("region", "Which region", Optional()),
			StringFlag("tag", "A tag", Repeatable(), Unique(true), Default([]interface{}{})),
			DictFlag(TypeStr, "header", "HTTP headers", Unique(false), Default(map[string]interface{}{})),
			BoolFlag("verbose-output", "Say more", Optional()),
			StringFlag("path", "A path", Default(RelativeToRoot("MYAPP_HOME", "store"))),
			MemberChoiceFlag("target-set", "which profiles to operate on", Required(),
				MemberChoice(StringFlag("profile", "profile name", Required()),
					"operate on one named profile",
					BoolFlag("create-missing", "create the profile when absent", Optional())),
				MemberChoice(BoolFlag("all-profiles", "every profile", Required()),
					"operate on every profile"),
			),
		),
		WithArgs(
			NewArg("env", "the environment", ArgRequired(),
				ArgChoices(Ch("dev", "the dev one"), Ch("prod", ""))),
			NewArg("files", "the files", Variadic(), ArgOptional()),
		),
	)
	text := dumpText(t, app)
	got := text[strings.Index(text, "\n  \"project_id\"")+1:]
	if got != pythonRichDump {
		t.Fatalf("the dump diverged from the sibling implementations:\n--- got ---\n%s\n--- want ---\n%s", got, pythonRichDump)
	}
}

// pythonRichDump is the Python implementation's dump of the same declaration,
// captured verbatim from `project_id` onward.
//
// The `constraints` entry carries the mandatory `name` §25.7's amendment added
// (§18.30 item 277): the key order is `type`, `name`, `flag`, `depends_on`, and
// the sibling implementations converge on it.
const pythonRichDump = `  "project_id": "testproject",
  "name": "testapp",
  "version": "1.0.0",
  "help": "A test app",
  "env_prefix": "MYAPP",
  "proc_observe_allowlist": [
    [
      "git",
      "status"
    ]
  ],
  "commands": {
    "deploy": {
      "name": "deploy",
      "help": "Deploy it",
      "effect": "mutating",
      "consequential": true,
      "flags": [
        {
          "name": "target",
          "help": "Where to deploy",
          "value_schema": {
            "type": "string",
            "enum": [
              "prod",
              "dev"
            ]
          },
          "short": "t",
          "presence": "optional",
          "env": "MYAPP_TARGET",
          "choices": [
            {
              "value": "prod",
              "help": "production"
            },
            {
              "value": "dev"
            }
          ]
        },
        {
          "name": "region",
          "help": "Which region",
          "value_schema": {
            "type": "string"
          },
          "presence": "optional"
        },
        {
          "name": "tag",
          "help": "A tag",
          "value_schema": {
            "type": "array",
            "items": {
              "type": "string"
            }
          },
          "presence": "default",
          "default": [],
          "unique": true
        },
        {
          "name": "header",
          "help": "HTTP headers",
          "value_schema": {
            "type": "object",
            "additionalProperties": {
              "type": "string"
            }
          },
          "presence": "default",
          "default": {}
        },
        {
          "name": "verbose-output",
          "help": "Say more",
          "value_schema": {
            "type": "boolean"
          },
          "presence": "optional",
          "negatable": true
        },
        {
          "name": "path",
          "help": "A path",
          "value_schema": {
            "type": "string"
          },
          "presence": "default",
          "default": {
            "relative_to_root": {
              "env_var": "MYAPP_HOME",
              "parts": [
                "store"
              ]
            }
          }
        },
        {
          "name": "target-set",
          "help": "which profiles to operate on",
          "presence": "required",
          "choices": [
            {
              "name": "profile",
              "help": "operate on one named profile",
              "flags": [
                {
                  "name": "value",
                  "help": "profile name",
                  "value_schema": {
                    "type": "string"
                  },
                  "presence": "required"
                },
                {
                  "name": "create-missing",
                  "help": "create the profile when absent",
                  "value_schema": {
                    "type": "boolean"
                  },
                  "presence": "optional",
                  "negatable": true
                }
              ]
            },
            {
              "name": "all-profiles",
              "help": "operate on every profile"
            }
          ],
          "elect_by": "member-flags"
        }
      ],
      "args": [
        {
          "name": "env",
          "help": "the environment",
          "value_schema": {
            "type": "string",
            "enum": [
              "dev",
              "prod"
            ]
          },
          "presence": "required",
          "choices": [
            {
              "value": "dev",
              "help": "the dev one"
            },
            {
              "value": "prod"
            }
          ]
        },
        {
          "name": "files",
          "help": "the files",
          "value_schema": {
            "type": "array",
            "items": {
              "type": "string"
            }
          },
          "presence": "optional",
          "variadic": true
        }
      ],
      "tags": [
        "release"
      ],
      "constraints": [
        {
          "type": "requires",
          "name": "region-needs-target",
          "flag": "region",
          "depends_on": "target"
        }
      ],
      "grants": [
        {
          "name": "write",
          "reason": "writes the release",
          "kind": "file_write"
        }
      ]
    }
  },
  "infra": {
    "roots": [
      {
        "env_var": "MYAPP_HOME",
        "default": "~/.myapp"
      }
    ],
    "handshakes": [
      {
        "env_var": "MYAPP_PARENT",
        "help": "set by the invoking process"
      }
    ]
  }
}
`
