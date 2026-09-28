package strictcli

import (
	"encoding/json"
	"strings"
	"testing"
)

// The framework's help and version commands: help pages by address, per-flag
// help, --depth, the help document under --json (which replaced
// --dump-schema), and the refusals that point at the right spelling.

func helpApp() *App {
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(BoolFlag("trace", "trace every step", Default(false)))
	app.Command("run", "run something", roHandler, WithEffect(EffectReadOnly),
		WithFlags(
			StringFlag("device", "which GPU to target", Required(), Short("d")),
			BoolFlag("fast", "skip the slow checks", Default(false)),
			ChoiceFlag("via", "delivery channel", Required(),
				Choice("email", "as an email", StringFlag("subject", "the subject line", Required())),
				Choice("sms", "as a text")),
		))
	db := app.Group("db", "database commands")
	db.Command("migrate", "run migrations", roHandler, WithEffect(EffectReadOnly))
	sub := db.Group("backup", "backup commands")
	sub.Command("take", "take a backup", roHandler, WithEffect(EffectReadOnly))
	return app
}

func mustRun(t *testing.T, app *App, argv ...string) Result {
	t.Helper()
	r := app.Test(argv)
	if r.ExitCode != 0 {
		t.Fatalf("%v: exit %d, stderr=%q", argv, r.ExitCode, r.Stderr)
	}
	return r
}

func mustRefuse(t *testing.T, app *App, want string, argv ...string) Result {
	t.Helper()
	r := app.Test(argv)
	if r.ExitCode != 1 {
		t.Fatalf("%v: exit %d, stdout=%q", argv, r.ExitCode, r.Stdout)
	}
	if !strings.HasPrefix(r.Stderr, "error: "+want+"\n") {
		t.Fatalf("%v: stderr=%q, want it to start with %q", argv, r.Stderr, "error: "+want)
	}
	return r
}

func TestHelpCommand_MatchesTheHelpFlag(t *testing.T) {
	app := helpApp()
	for _, pair := range [][2][]string{
		{{"help"}, {"--help"}},
		{{"help", "db"}, {"db", "--help"}},
		{{"help", "db", "backup"}, {"db", "backup", "--help"}},
		{{"help", "run"}, {"run", "--help"}},
		{{"help", "db", "migrate"}, {"db", "migrate", "--help"}},
	} {
		got := mustRun(t, app, pair[0]...).Stdout
		want := mustRun(t, app, pair[1]...).Stdout
		if got != want {
			t.Fatalf("%v:\n%s\n!= %v:\n%s", pair[0], got, pair[1], want)
		}
	}
}

func TestHelpCommand_RootAndGroupPagesNameTheHelpCommand(t *testing.T) {
	app := helpApp()
	if r := mustRun(t, app, "help"); !strings.HasSuffix(r.Stdout, "\nUse 'myapp help <command>' for more information.\n") {
		t.Fatalf("root page footer: %q", r.Stdout)
	}
	if r := mustRun(t, app, "help", "db"); !strings.HasSuffix(r.Stdout, "\nUse 'myapp help db <command>' for more information.\n") {
		t.Fatalf("group page footer: %q", r.Stdout)
	}
}

func TestHelpCommand_OneFlag(t *testing.T) {
	app := helpApp()
	r := mustRun(t, app, "help", "run", "--device")
	want := "myapp run -- run something\n\nFlags:\n  --device, -d <str>    which GPU to target [required]\n"
	if r.Stdout != want {
		t.Fatalf("got %q\nwant %q", r.Stdout, want)
	}
	// A scoped flag shows the selector and the choice that own it.
	r = mustRun(t, app, "help", "run", "--subject")
	want = "myapp run -- run something\n\nFlags:\n" +
		"  --via <choice>         delivery channel [required]\n" +
		"    email                as an email\n" +
		"      --subject <str>    the subject line [required]\n"
	if r.Stdout != want {
		t.Fatalf("got %q\nwant %q", r.Stdout, want)
	}
	// A global flag is addressed through a command and renders in its section.
	r = mustRun(t, app, "help", "run", "--trace")
	want = "myapp run -- run something\n\nGlobal flags:\n  --trace, --no-trace    trace every step [default: false]\n"
	if r.Stdout != want {
		t.Fatalf("got %q\nwant %q", r.Stdout, want)
	}
}

func TestHelpCommand_Depth(t *testing.T) {
	app := helpApp()
	r := mustRun(t, app, "help", "--depth", "2")
	want := "myapp v1.0.0 -- test app\n\n" +
		"Commands:\n  run           run something\n  db migrate    run migrations\n\n" +
		"Groups:\n  db           database commands\n  db backup    backup commands\n\n" +
		"Global flags:\n  --trace    trace every step\n\n" +
		"Use 'myapp help <command>' for more information.\n"
	if r.Stdout != want {
		t.Fatalf("got %q\nwant %q", r.Stdout, want)
	}
	r = mustRun(t, app, "help", "--depth=3", "db")
	want = "myapp db -- database commands\n\n" +
		"Commands:\n  migrate        run migrations\n  backup take    take a backup\n\n" +
		"Groups:\n  backup    backup commands\n\n" +
		"Use 'myapp help db <command>' for more information.\n"
	if r.Stdout != want {
		t.Fatalf("got %q\nwant %q", r.Stdout, want)
	}
	// Depth 1 is what --help shows.
	if mustRun(t, app, "help", "--depth", "1").Stdout != mustRun(t, app, "--help").Stdout {
		t.Fatal("--depth 1 differs from --help")
	}
}

func TestHelpCommand_Refusals(t *testing.T) {
	app := helpApp()
	mustRefuse(t, app, "unknown command 'nope'", "help", "nope")
	mustRefuse(t, app, "unknown command 'nope' in 'db'", "help", "db", "nope")
	mustRefuse(t, app, errHelpDepthValue("0"), "help", "--depth", "0", "db")
	mustRefuse(t, app, errHelpDepthValue("two"), "help", "--depth", "two", "db")
	mustRefuse(t, app, errHelpDepthMissing, "help", "--depth")
	mustRefuse(t, app, errHelpDepthOnCommand("run"), "help", "--depth", "2", "run")
	mustRefuse(t, app, errHelpUnknownOption("--all", "myapp"), "help", "--all")
	mustRefuse(t, app, errHelpUnknownFlag("run", "--devise", "--device, --fast, --via, --subject, --trace"), "help", "run", "--devise")
	mustRefuse(t, app, errHelpWordAfterCommand("extra", "run"), "help", "run", "extra")
	mustRefuse(t, app, errHelpAfterFlag("--fast"), "help", "run", "--device", "--fast")
	// Each refusal that names a spelling is followed by that spelling.
	mustRefuse(t, app, errHelpShortAddress("-d", "myapp help run --device"), "help", "run", "-d")
	mustRun(t, app, "help", "run", "--device")
	mustRefuse(t, app, errHelpFlagWithValue("--device=x", "myapp help run --device"), "help", "run", "--device=x")
	mustRefuse(t, app, errHelpNegatedFlag("--no-fast", "myapp help run --fast"), "help", "run", "--no-fast")
	mustRun(t, app, "help", "run", "--fast")
	mustRefuse(t, app, errHelpOptionAfterAddress("--depth", "myapp help --depth <int> db"), "help", "db", "--depth", "2")
	mustRun(t, app, "help", "--depth", "2", "db")
	mustRefuse(t, app, errHelpFlagOnGroup("--fast", "myapp help db <command> --fast"), "help", "db", "--fast")
	mustRefuse(t, app, errHelpFlagOnGroup("--trace", "myapp help <command> --trace"), "help", "--trace")
	mustRun(t, app, "help", "run", "--trace")
	mustRefuse(t, app, errHelpInGroup("myapp help db"), "db", "help")
	mustRefuse(t, app, errVersionInGroup("myapp version"), "db", "version")
}

func TestHelpCommand_OwnPages(t *testing.T) {
	app := helpApp()
	r := mustRun(t, app, "help", "--help")
	if !strings.HasPrefix(r.Stdout, "myapp help -- show the help of the app, a group, a command, or one of its flags\n") {
		t.Fatalf("help's own page: %q", r.Stdout)
	}
	if mustRun(t, app, "help", "run", "-h").Stdout != r.Stdout {
		t.Fatal("-h after help is help's own page")
	}
	r = mustRun(t, app, "version", "--help")
	if r.Stdout != "myapp version -- show the app's name and version\n\nWith --json, they are printed as JSON.\n" {
		t.Fatalf("version's own page: %q", r.Stdout)
	}
}

func TestVersionCommand(t *testing.T) {
	app := helpApp()
	if r := mustRun(t, app, "version"); r.Stdout != "myapp 1.0.0\n" {
		t.Fatalf("version: %q", r.Stdout)
	}
	if mustRun(t, app, "version").Stdout != mustRun(t, app, "--version").Stdout {
		t.Fatal("version and --version differ")
	}
	r := mustRun(t, app, "version", "--json")
	if r.Stdout != "{\n  \"name\": \"myapp\",\n  \"version\": \"1.0.0\"\n}\n" {
		t.Fatalf("version --json stdout: %q", r.Stdout)
	}
	if r.Stderr != envelopeV3("version", 0, "null", "null", "[]") {
		t.Fatalf("version --json stderr: %q", r.Stderr)
	}
	mustRefuse(t, app, errVersionArgs("extra"), "version", "extra")
	// --version is text only; the refusal names the machine form, which works.
	mustRefuse(t, app, errVersionTextOnly("myapp version --json"), "--version", "--json")
	mustRun(t, app, "version", "--json")
}

func TestHelpFlagIsTextOnly(t *testing.T) {
	app := helpApp()
	for _, tc := range []struct {
		argv []string
		fix  string
	}{
		{[]string{"--help", "--json"}, "myapp help --json"},
		{[]string{"--json"}, "myapp help --json"},
		{[]string{"db", "--help", "--json"}, "myapp help db --json"},
		{[]string{"run", "--help", "--json"}, "myapp help run --json"},
		{[]string{"db", "migrate", "--json", "-h"}, "myapp help db migrate --json"},
	} {
		mustRefuse(t, app, errHelpTextOnly(tc.fix), tc.argv...)
		mustRun(t, app, strings.Fields(tc.fix)[1:]...)
	}
}

func TestHelpDocument_WholeAppIsTheSchema(t *testing.T) {
	app := helpApp()
	r := mustRun(t, app, "help", "--json")
	schema, err := dumpSchemaOrdered(app)
	if err != nil {
		t.Fatal(err)
	}
	text, err := canonicalJSON(schema)
	if err != nil {
		t.Fatal(err)
	}
	if r.Stdout != text+"\n" {
		t.Fatalf("help --json is not the schema document:\n%s", r.Stdout)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["project_id"] != "github.com/stricttools/strictcli/go" {
		t.Fatalf("project_id from build information: %v", doc["project_id"])
	}
	if _, ok := doc["address"]; ok {
		t.Fatal("the whole app carries no address")
	}
	if r.Stderr != envelopeV3("help", 0, "null", "null", "[]") {
		t.Fatalf("help --json stderr: %q", r.Stderr)
	}
}

func helpDoc(t *testing.T, app *App, argv ...string) map[string]interface{} {
	t.Helper()
	r := mustRun(t, app, argv...)
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		t.Fatalf("%v: %v\n%s", argv, err, r.Stdout)
	}
	return doc
}

func keysOf(v interface{}) []string {
	m, _ := v.(map[string]interface{})
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestHelpDocument_Slices(t *testing.T) {
	app := helpApp()
	doc := helpDoc(t, app, "help", "db", "migrate", "--json")
	if got := doc["address"].([]interface{}); len(got) != 2 || got[0] != "db" || got[1] != "migrate" {
		t.Fatalf("address: %v", got)
	}
	if doc["commands"] != nil {
		t.Fatalf("root commands kept: %v", keysOf(doc["commands"]))
	}
	db := doc["groups"].(map[string]interface{})["db"].(map[string]interface{})
	if k := keysOf(db["commands"]); len(k) != 1 || k[0] != "migrate" {
		t.Fatalf("db commands: %v", k)
	}
	if db["groups"] != nil {
		t.Fatal("db groups kept on a command address")
	}

	doc = helpDoc(t, app, "help", "run", "--subject", "--json")
	run := doc["commands"].(map[string]interface{})["run"].(map[string]interface{})
	flags := run["flags"].([]interface{})
	if len(flags) != 1 || flags[0].(map[string]interface{})["name"] != "via" {
		t.Fatalf("flags of a scoped-flag address: %v", flags)
	}
	choices := flags[0].(map[string]interface{})["choices"].([]interface{})
	if len(choices) != 1 || choices[0].(map[string]interface{})["name"] != "email" {
		t.Fatalf("choices: %v", choices)
	}
	if doc["global_flags"] != nil {
		t.Fatal("global flags kept on a command-flag address")
	}

	doc = helpDoc(t, app, "help", "--depth", "1", "--json")
	if doc["depth"] != float64(1) {
		t.Fatalf("depth: %v", doc["depth"])
	}
	db = doc["groups"].(map[string]interface{})["db"].(map[string]interface{})
	if db["commands"] != nil || db["groups"] != nil {
		t.Fatal("depth 1 keeps a group's children")
	}
}

func TestDumpSchemaFlagIsRefused(t *testing.T) {
	app := helpApp()
	mustRefuse(t, app, errDumpSchemaRemoved("myapp help --json"), "--dump-schema")
	mustRun(t, app, "help", "--json")
}
