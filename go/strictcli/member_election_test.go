package strictcli

import (
	"fmt"
	"strings"
	"testing"
)

// Member-spelled election semantics (effects contract §21, carried over by
// §24.4).
//
// MutexGroup is deleted and "exactly one of these" is a member-spelled selector
// now. Every sentence §21.4 pins survives VERBATIM through member spelling --
// which is what makes migrating a group change no user-visible text -- and the
// per-type election rules survive with it: a bool member elects only when its
// typed token resolves to true, every other type elects on presence with any
// value including "", and election is command-line only.

// electionApp builds one member-spelled selector: str + two negatable bools,
// the exact shape the deleted MutexGroup test declared.
func electionApp() *App {
	return simpleApp("run", "run it", "mode={mode}",
		WithFlags(MemberChoiceFlag("mode", "which profiles to run over", Required(),
			MemberChoice(StringFlag("profile", "a profile", Required()), "run over one named profile"),
			MemberChoice(BoolFlag("all-profiles", "every profile", Required()), "run over every profile"),
			MemberChoice(BoolFlag("current-profile", "the current profile", Required()), "run over the current profile"),
		)))
}

func TestMemberA1NegatedBoolElectsNothing(t *testing.T) {
	app := electionApp()
	r := app.Test([]string{"run", "--no-all-profiles"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: one of --profile, --all-profiles, --current-profile is required " +
		"(--no-all-profiles declines an option; it does not choose one)\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

func TestMemberA1TrueBoolStillElects(t *testing.T) {
	app := electionApp()
	r := app.Test([]string{"run", "--all-profiles"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "mode=all-profiles[]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestMemberA1AllMembersDeclined(t *testing.T) {
	app := electionApp()
	r := app.Test([]string{"run", "--no-current-profile", "--no-all-profiles"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: one of --profile, --all-profiles, --current-profile is required " +
		"(--no-all-profiles declines an option; it does not choose one)\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

func TestMemberA2StringElectsOnEmptyString(t *testing.T) {
	app := electionApp()
	r := app.Test([]string{"run", "--profile", ""})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "mode=profile[value:]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestMemberA3ClauseAbsentWhenNothingDeclined(t *testing.T) {
	app := electionApp()
	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: one of --profile, --all-profiles, --current-profile is required\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

func TestMemberA4RedundantNegation(t *testing.T) {
	app := electionApp()
	r := app.Test([]string{"run", "--profile", "work", "--no-all-profiles"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: --no-all-profiles cannot be combined with --profile " +
		"(--no-all-profiles declines an option; it does not choose one)\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

func TestMemberA4MultipleDeclinedMembers(t *testing.T) {
	app := electionApp()
	r := app.Test([]string{
		"run", "--profile", "work", "--no-current-profile", "--no-all-profiles",
	})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: --no-all-profiles and --no-current-profile cannot be combined " +
		"with --profile (--no-all-profiles declines an option; it does not choose one)\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

func TestMemberTwoElectionsStillMutuallyExclusive(t *testing.T) {
	app := electionApp()
	r := app.Test([]string{"run", "--profile", "work", "--all-profiles"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: --profile and --all-profiles are mutually exclusive\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// A5: a member-spelled selector elects from the COMMAND LINE ONLY. Env and
// config are not consulted for a member flag at all (§21.3, carried over by
// §24.6).
func TestMemberA5EnvDoesNotElect(t *testing.T) {
	t.Setenv("TEST_FILE_A5", "data.txt")
	app := simpleApp("fetch", "fetch data", "src={src}",
		WithFlags(MemberChoiceFlag("src", "where to read from", Required(),
			MemberChoice(StringFlag("file", "read from file", Required(),
				Env("TEST_FILE_A5"), Prefixed(false)), "read from a file"),
			MemberChoice(StringFlag("url", "read from URL", Required()), "read from a URL"),
		)))
	r := app.Test([]string{"fetch"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, "error: one of --file, --url is required\n") {
		t.Fatalf("stderr = %q", r.Stderr)
	}
}

func TestMemberA5EnvValueSuppressedBesideAnElection(t *testing.T) {
	t.Setenv("TEST_FILE_A5B", "data.txt")
	app := simpleApp("fetch", "fetch data", "src={src}",
		WithFlags(MemberChoiceFlag("src", "where to read from", Required(),
			MemberChoice(StringFlag("file", "read from file", Required(),
				Env("TEST_FILE_A5B"), Prefixed(false)), "read from a file"),
			MemberChoice(StringFlag("url", "read from URL", Required()), "read from a URL"),
		)))
	r := app.Test([]string{"fetch", "--url", "http://example.com"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	// The unelected member's scope is not delivered at all, so the env value
	// has nowhere to land (§21.3's struck "unelected member delivers its
	// declared default", restated by §24.1).
	want := "src=url[value:http://example.com]"
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("stdout = %q, want it to contain %q", r.Stdout, want)
	}
}

func TestMemberA5ConfigDoesNotElect(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"file": "from-config.txt"})

	makeApp := func() *App {
		app := NewApp("testapp", "1.0.0", "test app", WithConfig())
		app.Command("fetch", "fetch data", func(ctx *Context, args map[string]interface{}) Outcome {
			fmt.Print("src=" + formatValue(args["src"]))
			return Exit(0)
		}, WithFlags(MemberChoiceFlag("src", "where to read from", Required(),
			MemberChoice(StringFlag("file", "read from file", Required()), "read from a file"),
			MemberChoice(StringFlag("url", "read from URL", Required()), "read from a URL"),
		)), WithEffect(EffectReadOnly))
		return app
	}

	// A config value elects nothing, so the selector is unsatisfied.
	r := makeApp().Test([]string{"fetch"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, "error: one of --file, --url is required\n") {
		t.Fatalf("stderr = %q", r.Stderr)
	}

	// Beside a real election the config value is not consulted either.
	r = makeApp().Test([]string{"fetch", "--url", "u"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "src=url[value:u]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestMemberCallWithNoElectionIsRefused(t *testing.T) {
	app := electionApp()
	_, err := app.Call("run", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected an error from Call, got nil")
	}
	if !strings.Contains(
		err.Error(),
		"one of --profile, --all-profiles, --current-profile is required",
	) {
		t.Fatalf("error = %q", err.Error())
	}
}

// A member flag MUST declare Required(), read as "required once this member is
// elected". The rule INVERTS the deleted mutex-member rule, because the
// declaration changed (§12.13's errMemberFlagPresence, §21's box).
func TestMemberFlagMustDeclareRequired(t *testing.T) {
	expectPanic(t, `Choice "loud" of "volume": a member flag must declare Required(), read as required once this member is elected`, func() {
		MemberChoiceFlag("volume", "how loud", Required(),
			MemberChoice(BoolFlag("loud", "loud output", Optional()), "be loud"),
			MemberChoice(BoolFlag("hushed", "hushed output", Required()), "be quiet"),
		)
	})
}

// A member-spelled selector is never typed, so it cannot carry a short.
func TestMemberSelectorCannotCarryShort(t *testing.T) {
	expectPanic(t, `Flag "volume": a member-spelled choice flag is never typed, so it cannot carry a short: declare the short on a member`, func() {
		MemberChoiceFlag("volume", "how loud", Required(), Short("v"),
			MemberChoice(BoolFlag("loud", "loud output", Required()), "be loud"),
			MemberChoice(BoolFlag("hushed", "hushed output", Required()), "be quiet"),
		)
	})
}

// A defaulted member-spelled selector may only default to a PAYLOAD-LESS
// member, because a value-carrying member's value is supplied by the token that
// elects it and a default has no token (§24.5).
func TestMemberDefaultMustBePayloadLess(t *testing.T) {
	expectPanic(t, `Flag "volume": Default("profile") elects choice "profile", whose flag carries a value nothing supplies: only a payload-less member can be a default`, func() {
		MemberChoiceFlag("volume", "how loud", Default("profile"),
			MemberChoice(StringFlag("profile", "a profile", Required()), "use a profile"),
			MemberChoice(BoolFlag("hushed", "hushed output", Required()), "be quiet"),
		)
	})
}

func TestMemberDefaultElectsPayloadLessMember(t *testing.T) {
	app := simpleApp("cmd", "a command", "volume={volume}",
		WithFlags(MemberChoiceFlag("volume", "how loud", Default("hushed"),
			MemberChoice(StringFlag("profile", "a profile", Required()), "use a profile"),
			MemberChoice(BoolFlag("hushed", "hushed output", Required()), "be quiet"),
		)))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "volume=hushed[]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

// A member may own a SCOPE, which a mutex group could not express: the scoped
// flag parses under its own member and is a scope error under another.
func TestMemberOwnsAScope(t *testing.T) {
	app := simpleApp("run", "run it", "mode={mode}",
		WithFlags(MemberChoiceFlag("mode", "which profiles", Required(),
			MemberChoice(StringFlag("profile", "a profile", Required()), "one named profile",
				BoolFlag("create-missing", "create it if absent", Default(false)),
			),
			MemberChoice(BoolFlag("all-profiles", "every profile", Required()), "every profile"),
		)))

	r := app.Test([]string{"run", "--profile", "work", "--create-missing"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "mode=profile[create_missing:true value:work]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}

	r = app.Test([]string{"run", "--all-profiles", "--create-missing"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--create-missing' is only valid under '--profile', but '--all-profiles' was elected\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// --- A member's short (§24.4, §24.12) ---------------------------------------
//
// Go declares one Flag per member, so the short is that flag's own Short()
// whichever shape the member has -- no second spelling, and nothing about the
// construct is new here. What these lock in is the BEHAVIOR the siblings had
// to grow a declaration surface to reach: the token elects, a payload-carrying
// member's short consumes its value, and the short renders on the help line.

func memberShortApp() *App {
	return simpleApp("launch", "launch it", "start={start}",
		WithFlags(MemberChoiceFlag("start", "how to start", Required(),
			MemberChoice(StringFlag("role", "the role name", Required(), Short("r")), "one role"),
			MemberChoice(BoolFlag("cont", "continue the previous session", Required(), Short("c")), "continue the previous session"),
			MemberChoice(BoolFlag("plain", "a plain session", Required(), Short("p")), "a plain session"),
		)))
}

func TestMemberPayloadShortElects(t *testing.T) {
	r := memberShortApp().Test([]string{"launch", "-r", "admin"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "role") || !strings.Contains(r.Stdout, "admin") {
		t.Fatalf("stdout = %q, want the elected role with its payload", r.Stdout)
	}
}

// `-r X` is the member's own value, exactly as `--role X` is: the short
// consumes the next token even when it looks like another member's flag.
func TestMemberPayloadShortConsumesItsValue(t *testing.T) {
	r := memberShortApp().Test([]string{"launch", "-r", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "--plain") {
		t.Fatalf("stdout = %q, want the literal value it consumed", r.Stdout)
	}
}

func TestMemberPayloadLessShortElects(t *testing.T) {
	r := memberShortApp().Test([]string{"launch", "-c"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "cont") {
		t.Fatalf("stdout = %q, want the elected member", r.Stdout)
	}
}

func TestMemberShortElectionsStayMutuallyExclusive(t *testing.T) {
	r := memberShortApp().Test([]string{"launch", "-c", "-p"})
	want := "error: --cont and --plain are mutually exclusive\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// The short elects; the decline keeps its one spelling, and the message names
// the member's LONG form however it was typed (§21.2).
func TestMemberShortIsStillDeclinedByTheLongNegation(t *testing.T) {
	r := memberShortApp().Test([]string{"launch", "--no-cont", "-p"})
	want := "error: --no-cont cannot be combined with --plain " +
		"(--no-cont declines an option; it does not choose one)\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

func TestMemberShortRendersOnTheHelpLine(t *testing.T) {
	r := memberShortApp().Test([]string{"launch", "--help"})
	for _, want := range []string{"--role, -r <str>", "--cont, -c", "--plain, -p"} {
		if !strings.Contains(r.Stdout, want) {
			t.Fatalf("help = %q, want it to contain %q", r.Stdout, want)
		}
	}
}

// Two sibling members can never be live together, so a shared short is not a
// collision -- it is the election-token guard that refuses it (§18.19 item 221).
func TestTwoSiblingMembersMayNotShareAShort(t *testing.T) {
	expectPanic(t, `command "launch": short '-c' is reused by sibling scopes and also claimed by '--cont', which elects: an election token is read before any election has happened, so its short cannot be shared`, func() {
		simpleApp("launch", "launch it", "",
			WithFlags(MemberChoiceFlag("start", "how to start", Required(),
				MemberChoice(BoolFlag("cont", "continue", Required(), Short("c")), "continue"),
				MemberChoice(BoolFlag("clean", "start clean", Required(), Short("c")), "start clean"),
			)))
	})
}

func TestMemberShortCollidesWithACommandFlag(t *testing.T) {
	expectPanic(t, `command "launch": short '-r' is claimed by '--role' and '--repo', which can be elected at the same time`, func() {
		simpleApp("launch", "launch it", "",
			WithFlags(
				MemberChoiceFlag("start", "how to start", Required(),
					MemberChoice(StringFlag("role", "the role name", Required(), Short("r")), "one role"),
					MemberChoice(BoolFlag("plain", "a plain session", Required()), "a plain session"),
				),
				StringFlag("repo", "the repo", Optional(), Short("r")),
			))
	})
}
