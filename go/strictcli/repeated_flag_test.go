package strictcli

import (
	"strings"
	"testing"
)

// A flag that is not repeatable takes one value. Giving it more than once on
// the command line is refused, naming the flag and its first two occurrences
// as typed, rather than silently keeping the last one. Environment variables
// and config files are not occurrences: their precedence is unchanged.

func repeatedFlagApp(opts ...AppOption) *App {
	app := NewApp("myapp", "1.0.0", "test app", opts...)
	app.GlobalFlag(StringFlag("region", "the region", Default("eu"), Short("r")))
	app.GlobalFlag(StringFlag("zone", "zones to use", Repeatable(), Unique(false), Default([]interface{}{})))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		out := "commits={commits} cache={cache} tag={tag} labels={labels} region={region} zone={zone}"
		for k, v := range args {
			out = strings.ReplaceAll(out, "{"+k+"}", formatValue(v))
		}
		ctx.Out(out)
		return Exit(0)
	}, WithEffect(EffectReadOnly), WithFlags(
		StringFlag("commits", "the commit range", Optional(), Short("c"), Env("MYAPP_COMMITS")),
		BoolFlag("cache", "use the cache", Default(true), Short("k")),
		StringFlag("tag", "tags to apply", Repeatable(), Unique(false), Default([]interface{}{})),
		DictFlag(TypeStr, "labels", "labels", Unique(false), Default(map[string]interface{}{})),
	))
	return app
}

func assertRefused(t *testing.T, app *App, argv []string, want string) {
	t.Helper()
	r := app.Test(argv)
	if r.ExitCode != 1 {
		t.Fatalf("%v: expected exit 1, got %d (stdout=%q stderr=%q)", argv, r.ExitCode, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "error: "+want+"\n") {
		t.Fatalf("%v: stderr = %q, want it to contain %q", argv, r.Stderr, want)
	}
}

func assertRuns(t *testing.T, app *App, argv []string, want string) {
	t.Helper()
	r := app.Test(argv)
	if r.ExitCode != 0 {
		t.Fatalf("%v: expected exit 0, got %d (stderr=%q)", argv, r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("%v: stdout = %q, want it to contain %q", argv, r.Stdout, want)
	}
}

func TestRepeatedFlagSpaceForm(t *testing.T) {
	assertRefused(t, repeatedFlagApp(), []string{"run", "--commits", "a", "--commits", "b"},
		"--commits: given more than once, as '--commits a' and '--commits b'; it takes one value")
}

func TestRepeatedFlagEqualsForm(t *testing.T) {
	assertRefused(t, repeatedFlagApp(), []string{"run", "--commits=a", "--commits=b"},
		"--commits: given more than once, as '--commits=a' and '--commits=b'; it takes one value")
}

func TestRepeatedFlagShortAndLong(t *testing.T) {
	assertRefused(t, repeatedFlagApp(), []string{"run", "-c", "a", "--commits", "b"},
		"--commits: given more than once, as '-c a' and '--commits b'; it takes one value")
}

func TestRepeatedFlagSameValueStillRefused(t *testing.T) {
	assertRefused(t, repeatedFlagApp(), []string{"run", "--commits", "a", "--commits", "a"},
		"--commits: given more than once, as '--commits a' and '--commits a'; it takes one value")
}

func TestRepeatedFlagNamesFirstTwoOfThree(t *testing.T) {
	assertRefused(t, repeatedFlagApp(), []string{"run", "--commits", "a", "-c", "b", "--commits=c"},
		"--commits: given more than once, as '--commits a' and '-c b'; it takes one value")
}

func TestRepeatedBoolTwice(t *testing.T) {
	assertRefused(t, repeatedFlagApp(), []string{"run", "--cache", "-k"},
		"--cache: given more than once, as '--cache' and '-k'; it takes one value")
}

func TestRepeatedBoolWithNegation(t *testing.T) {
	assertRefused(t, repeatedFlagApp(), []string{"run", "--cache", "--no-cache"},
		"--cache: given more than once, as '--cache' and '--no-cache'; it takes one value")
	assertRefused(t, repeatedFlagApp(), []string{"run", "--no-cache", "--no-cache"},
		"--cache: given more than once, as '--no-cache' and '--no-cache'; it takes one value")
}

// The refusal is structural: it outranks a value that would not parse.
func TestRepeatedFlagOutranksCoercion(t *testing.T) {
	app := simpleApp("run", "run it", "count={count}", WithFlags(IntFlag("count", "a count", Optional())))
	assertRefused(t, app, []string{"run", "--count", "x", "--count", "5"},
		"--count: given more than once, as '--count x' and '--count 5'; it takes one value")
}

// The fix the message names clears it: passing the flag once runs.
func TestRepeatedFlagPassingOnceRuns(t *testing.T) {
	assertRuns(t, repeatedFlagApp(), []string{"run", "--commits", "b"}, "commits=b")
	assertRuns(t, repeatedFlagApp(), []string{"run", "--no-cache"}, "cache=false")
}

func TestRepeatableAndDictFlagsStillCollect(t *testing.T) {
	assertRuns(t, repeatedFlagApp(), []string{"run", "--tag", "a", "--tag", "b", "--labels", "x=1", "--labels", "y=2"},
		"tag=a,b labels=map[x:1 y:2]")
}

// Env and config are sources, not occurrences: the command line still wins.
func TestRepeatedFlagEnvIsNotRepetition(t *testing.T) {
	t.Setenv("MYAPP_COMMITS", "from-env")
	assertRuns(t, repeatedFlagApp(), []string{"run", "--commits", "cli"}, "commits=cli")
}

func TestRepeatedFlagConfigIsNotRepetition(t *testing.T) {
	path := writeTestConfigJSON(t, `{"commits": "from-config"}`)
	assertRuns(t, repeatedFlagApp(WithConfig(), WithConfigPath(path)), []string{"run", "--commits", "cli"}, "commits=cli")
}

func TestRepeatedGlobalBeforeCommand(t *testing.T) {
	assertRefused(t, repeatedFlagApp(), []string{"--region", "us", "-r", "ap", "run"},
		"--region: given more than once, as '--region us' and '-r ap'; it takes one value")
}

func TestRepeatedGlobalAfterCommand(t *testing.T) {
	assertRefused(t, repeatedFlagApp(), []string{"run", "--region=us", "--region=ap"},
		"--region: given more than once, as '--region=us' and '--region=ap'; it takes one value")
}

func TestRepeatedGlobalAcrossTheCommand(t *testing.T) {
	assertRefused(t, repeatedFlagApp(), []string{"--region", "us", "run", "-r", "ap"},
		"--region: given more than once, as '--region us' and '-r ap'; it takes one value")
}

func TestRepeatableGlobalStillCollects(t *testing.T) {
	assertRuns(t, repeatedFlagApp(), []string{"--zone", "a", "--zone", "b", "run"}, "zone=a,b")
}

// A repeatable global given on both sides of the command collects every
// occurrence in command-line order; it used to keep only the post-command ones.
func TestRepeatableGlobalCollectsAcrossTheCommand(t *testing.T) {
	assertRuns(t, repeatedFlagApp(), []string{"--zone", "a", "run", "--zone", "b"}, "zone=a,b")
	assertRuns(t, repeatedFlagApp(), []string{"--zone=a", "--zone", "b", "run", "--zone", "c", "--zone=d"}, "zone=a,b,c,d")
	assertRuns(t, repeatedFlagApp(), []string{"run", "--zone", "b"}, "zone=b")
}

func TestUniqueRepeatableGlobalRefusesADuplicateAcrossTheCommand(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(IntFlag("port", "ports", Repeatable(), Unique(true), Optional()))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out("port=" + formatValue(args["port"]))
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	assertRefused(t, app, []string{"--port", "1", "run", "--port", "1"}, "--port: duplicate value '1'")
	assertRuns(t, app, []string{"--port", "1", "run", "--port", "2"}, "port=1,2")
}

// Env and config are sources, not occurrences: never joined to the command line.
func TestRepeatableGlobalAcrossTheCommandIgnoresEnvAndConfig(t *testing.T) {
	path := writeTestConfigJSON(t, `{"zone": ["from-config"]}`)
	app := repeatedFlagApp(WithConfig(), WithConfigPath(path))
	assertRuns(t, app, []string{"--zone", "a", "run", "--zone", "b"}, "zone=a,b")
	assertRuns(t, repeatedFlagApp(WithConfig(), WithConfigPath(path)), []string{"run", "--zone", "b"}, "zone=b")
}

func repeatedScopedApp() *App {
	return simpleApp("send", "send it", "via={via} mode={mode}", WithFlags(
		ChoiceFlag("via", "delivery channel", Required(),
			Choice("email", "an email message", StringFlag("subject", "the subject", Required(), Short("s"))),
			Choice("sms", "a text message", StringFlag("phone", "destination", Required())),
		),
		MemberChoiceFlag("mode", "which profiles", Default("all-profiles"),
			MemberChoice(StringFlag("profile", "a profile", Required()), "one named profile"),
			MemberChoice(BoolFlag("all-profiles", "every profile", Required()), "every profile"),
		),
	))
}

func TestRepeatedScopedFlag(t *testing.T) {
	assertRefused(t, repeatedScopedApp(), []string{"send", "--via", "email", "-s", "hi", "--subject", "yo"},
		"--subject: given more than once, as '-s hi' and '--subject yo'; it takes one value")
}

func TestRepeatedMemberFlag(t *testing.T) {
	assertRefused(t, repeatedScopedApp(), []string{"send", "--via", "sms", "--phone", "1", "--profile", "a", "--profile", "b"},
		"--profile: given more than once, as '--profile a' and '--profile b'; it takes one value")
	assertRefused(t, repeatedScopedApp(), []string{"send", "--via", "sms", "--phone", "1", "--all-profiles", "--no-all-profiles"},
		"--all-profiles: given more than once, as '--all-profiles' and '--no-all-profiles'; it takes one value")
}

// A selector's double election keeps its own sentence, which names values.
func TestSelectorElectedTwiceKeepsItsSentence(t *testing.T) {
	assertRefused(t, repeatedScopedApp(), []string{"send", "--via", "email", "--via", "sms"},
		"--via: elected more than once, as 'email' and 'sms'")
}

// A clear is one act: `--unset-x` given twice is refused like any repeated
// flag, and a value beside a clear keeps its own sentence, which outranks it.
func TestRepeatedUnsetIsRefused(t *testing.T) {
	app := updateFixture(t, nil)
	assertRefused(t, app, []string{"update-record", "--zone", "z1", "--record-id", "r7", "--unset-ttl", "--unset-ttl"},
		"--unset-ttl: given more than once, as '--unset-ttl' and '--unset-ttl'; it takes one value")
	assertRefused(t, app, []string{"update-record", "--zone", "z1", "--record-id", "r7", "--ttl", "1", "--ttl", "2", "--unset-ttl"},
		"--ttl and --unset-ttl are mutually exclusive: a property is either written or cleared")
}

// Inside an update `--no-x` writes false, so `--x --no-x` would write two
// opposite values; it is refused like any other repetition.
func TestRepeatedUpdateBoolWithNegation(t *testing.T) {
	assertRefused(t, updateFixture(t, nil), []string{"update-record", "--zone", "z1", "--record-id", "r7", "--proxied", "--no-proxied"},
		"--proxied: given more than once, as '--proxied' and '--no-proxied'; it takes one value")
}

// The reserved --config takes one path, in either spelling.
func TestRepeatedConfigIsRefused(t *testing.T) {
	path := writeTestConfigJSON(t, `{}`)
	app := repeatedFlagApp(WithConfig())
	assertRefused(t, app, []string{"--config", path, "--config=" + path, "run"},
		"--config: given more than once, as '--config "+path+"' and '--config="+path+"'; it takes one value")
	assertRuns(t, app, []string{"--config", path, "run"}, "commits=")
}

// A repeated flag is reported before the command region is even routed when it
// is a pre-command global, and at the first second occurrence otherwise.
func TestRepeatedFlagReportsFirstSecondOccurrence(t *testing.T) {
	assertRefused(t, repeatedFlagApp(), []string{"run", "--cache", "--commits", "a", "--commits", "b", "--no-cache"},
		"--commits: given more than once, as '--commits a' and '--commits b'; it takes one value")
}

// A repetition is structural: an out-of-scope flag still outranks it, and it
// outranks a missing required flag.
func TestRepeatedFlagPrecedence(t *testing.T) {
	assertRefused(t, repeatedScopedApp(), []string{"send", "--via", "sms", "--subject", "a", "--subject", "b"},
		"flag '--subject' is only valid under '--via email', but '--via sms' was elected")
	assertRefused(t, repeatedScopedApp(), []string{"send", "--via", "email", "--subject", "a", "--subject", "b", "--all-profiles"},
		"--subject: given more than once, as '--subject a' and '--subject b'; it takes one value")
}

func TestRepeatedScopedBoolWithNegation(t *testing.T) {
	app := simpleApp("send", "send it", "via={via}", WithFlags(
		ChoiceFlag("via", "delivery channel", Required(),
			Choice("email", "an email message", BoolFlag("urgent", "mark urgent", Default(false))),
			Choice("sms", "a text message"),
		),
	))
	assertRefused(t, app, []string{"send", "--via", "email", "--urgent", "--no-urgent"},
		"--urgent: given more than once, as '--urgent' and '--no-urgent'; it takes one value")
}
