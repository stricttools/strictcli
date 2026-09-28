package strictcli

import (
	"os"
	"strings"
	"testing"
)

// The scoped-selector construct (effects contract §24, §12.13).
//
// A choice is a declaration scope. These tests pin the token spelling, the
// phased parse and its election -> scope -> value -> presence precedence, the
// registration guards, delivery, help rendering and the MCP projection.

// --- shared declarations. A *ChoiceDecl is an IDENTITY value, so the handler
// switches on the very value the declaration used.

var (
	viaEmail = Choice("email", "deliver the notification as an email message",
		StringFlag("subject", "subject line of the message", Required()),
		StringFlag("recipient", "destination email address", Required()),
	)
	viaSMS = Choice("sms", "deliver the notification as a text message",
		StringFlag("phone-number", "destination number in E.164 form", Required()),
	)
	viaWebhook = Choice("webhook", "POST the notification to a URL",
		StringFlag("url", "destination URL", Required()),
		IntFlag("retries", "how many times to retry", Default(3)),
	)
)

func notifyApp() *App {
	return simpleApp("send", "send one notification", "via={via}",
		WithFlags(
			ChoiceFlag("via", "delivery channel", Required(), Short("v"), viaEmail, viaSMS, viaWebhook),
			BoolFlag("dry", "print what would be sent", Default(false)),
		))
}

// --- Election and order independence (§24.1, §24.3) ---

func TestSelectorElectsAndDelivers(t *testing.T) {
	app := notifyApp()
	r := app.Test([]string{"send", "--via", "email", "--subject", "hi", "--recipient", "a@b"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "via=email[recipient:a@b subject:hi]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestSelectorOrderIndependence(t *testing.T) {
	app := notifyApp()
	a := app.Test([]string{"send", "--subject", "hi", "--recipient", "a@b", "--via", "email"})
	b := notifyApp().Test([]string{"send", "--via", "email", "--subject", "hi", "--recipient", "a@b"})
	if a.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", a.ExitCode, a.Stderr)
	}
	if a.Stdout != b.Stdout {
		t.Fatalf("order changed the result: %q vs %q", a.Stdout, b.Stdout)
	}
}

func TestSelectorShortElects(t *testing.T) {
	app := notifyApp()
	r := app.Test([]string{"send", "-v", "sms", "--phone-number", "+15550100"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "via=sms[phone_number:+15550100]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

// --- The out-of-scope error and its three "why" clauses (§12.13) ---

func TestOutOfScopeNamesBothSides(t *testing.T) {
	app := notifyApp()
	r := app.Test([]string{"send", "--via", "sms", "--subject", "hi"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--subject' is only valid under '--via email', but '--via sms' was elected\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// The spelling mistake is reported before its consequence: the scope error
// beats "--phone-number is required" (§24.3's precedence rule).
func TestScopeErrorBeatsMissingRequired(t *testing.T) {
	app := notifyApp()
	r := app.Test([]string{"send", "--via", "sms", "--subject", "hi"})
	if strings.Contains(r.Stderr, "phone-number") {
		t.Fatalf("presence error reported before the scope error: %q", r.Stderr)
	}
}

func TestOutOfScopeWhenSelectorNotProvided(t *testing.T) {
	app := notifyApp()
	r := app.Test([]string{"send", "--subject", "hi"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: flag '--subject' is only valid under '--via email', but '--via' was not provided\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// A required selector that elected nothing and had no scoped flag supplied is
// the ORDINARY required-flag error (§24.3).
func TestRequiredSelectorWithNothingSupplied(t *testing.T) {
	app := notifyApp()
	r := app.Test([]string{"send"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "error: flag '--via' is required\n") {
		t.Fatalf("stderr = %q", r.Stderr)
	}
}

// A name reused by sibling scopes lists both owners, joined by " or ".
func TestOutOfScopeListsEveryOwner(t *testing.T) {
	app := simpleApp("cmd", "a command", "mode={mode}",
		WithFlags(ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "mode a", StringFlag("target", "the target", Optional())),
			Choice("bb", "mode b", StringFlag("target", "the target", Optional())),
			Choice("cc", "mode c", StringFlag("other", "something else", Optional())),
		)))
	r := app.Test([]string{"cmd", "--mode", "cc", "--target", "xx"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--target' is only valid under '--mode aa' or '--mode bb', but '--mode cc' was elected\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// --- Recursion, and blaming the OUTERMOST unsatisfied election (§24.3) ---

func recursiveApp() *App {
	return simpleApp("add", "add a changelog entry", "visibility={visibility}",
		WithFlags(ChoiceFlag("visibility", "who the entry is for", Required(),
			Choice("user-facing", "shown to users",
				ChoiceFlag("type", "what kind of change", Required(),
					Choice("feature", "a new capability",
						StringFlag("headline", "one-line summary", Required())),
					Choice("fix", "a bug fix",
						StringFlag("symptom", "the user-visible symptom", Required())),
				),
			),
			Choice("internal", "not shown to users"),
		)))
}

func TestSelectorRecursesToAnyDepth(t *testing.T) {
	app := recursiveApp()
	r := app.Test([]string{"add", "--visibility", "user-facing", "--type", "feature", "--headline", "xx"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "visibility=user-facing[type:feature[headline:xx]]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestOutOfScopeBlamesOutermostElection(t *testing.T) {
	app := recursiveApp()
	r := app.Test([]string{"add", "--visibility", "internal", "--headline", "xx"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--headline' is only valid under '--visibility user-facing --type feature', " +
		"but '--visibility internal' was elected\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

func TestRequiredFlagCarriesTheScopeSuffix(t *testing.T) {
	app := notifyApp()
	r := app.Test([]string{"send", "--via", "email", "--subject", "hi"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "error: flag '--recipient' is required under '--via email'\n") {
		t.Fatalf("stderr = %q", r.Stderr)
	}
}

// --- Double election (§12.13) ---

func TestSelectorElectedTwice(t *testing.T) {
	app := notifyApp()
	r := app.Test([]string{"send", "--via", "email", "--via", "sms"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: --via: elected more than once, as 'email' and 'sms'\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

func TestSelectorUnknownChoiceReusesInvalidValue(t *testing.T) {
	app := notifyApp()
	r := app.Test([]string{"send", "--via", "carrier-pigeon"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: --via: invalid value 'carrier-pigeon', must be one of: email, sms, webhook\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// --- Sources, origins and conditional bindings (§24.6) ---

func TestSelectorElectsFromEnv(t *testing.T) {
	t.Setenv("NOTIFY_VIA", "sms")
	app := simpleApp("send", "send one notification", "via={via}",
		WithFlags(ChoiceFlag("via", "delivery channel", Required(),
			Env("NOTIFY_VIA"), Prefixed(false),
			Choice("email", "as an email", StringFlag("subject", "subject line", Required())),
			Choice("sms", "as a text", StringFlag("phone-number", "the number", Required())),
		)))
	r := app.Test([]string{"send", "--phone-number", "+15550100"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "via=sms[phone_number:+15550100]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

// An election from a non-CLI source NAMES ITSELF in every message it causes,
// because otherwise a refusal blames a command line that does not contain the
// cause (§24.6, §12.13's origin clauses).
func TestAmbientElectionNamesItselfInAScopeError(t *testing.T) {
	t.Setenv("NOTIFY_VIA", "sms")
	app := simpleApp("send", "send one notification", "via={via}",
		WithFlags(ChoiceFlag("via", "delivery channel", Required(),
			Env("NOTIFY_VIA"), Prefixed(false),
			Choice("email", "as an email", StringFlag("subject", "subject line", Required())),
			Choice("sms", "as a text", StringFlag("phone-number", "the number", Required())),
		)))
	r := app.Test([]string{"send", "--subject", "hi"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--subject' is only valid under '--via email', but '--via sms' was elected from env var 'NOTIFY_VIA'\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

func TestAmbientElectionNamesItselfInAPresenceError(t *testing.T) {
	t.Setenv("NOTIFY_VIA", "sms")
	app := simpleApp("send", "send one notification", "via={via}",
		WithFlags(ChoiceFlag("via", "delivery channel", Required(),
			Env("NOTIFY_VIA"), Prefixed(false),
			Choice("email", "as an email", StringFlag("subject", "subject line", Required())),
			Choice("sms", "as a text", StringFlag("phone-number", "the number", Required())),
		)))
	r := app.Test([]string{"send"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--phone-number' is required under '--via sms' (elected from env var 'NOTIFY_VIA')\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// An env or config binding on a scoped flag is a CONDITIONAL BINDING: consulted
// when its scope is elected, and otherwise NEVER consulted -- not an error, and
// not a value. Every skipped binding is named under --verbose (§24.6).
func conditionalBindingApp() *App {
	return simpleApp("send", "send one notification", "via={via}",
		WithFlags(ChoiceFlag("via", "delivery channel", Required(),
			Choice("email", "as an email",
				StringFlag("subject", "subject line", Optional(),
					Env("NOTIFY_SUBJECT"), Prefixed(false))),
			Choice("sms", "as a text", StringFlag("phone-number", "the number", Required())),
		)))
}

func TestConditionalBindingConsultedWhenElected(t *testing.T) {
	t.Setenv("NOTIFY_SUBJECT", "from-env")
	r := conditionalBindingApp().Test([]string{"send", "--via", "email"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "via=email[subject:from-env]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestConditionalBindingNotConsultedWhenNotElected(t *testing.T) {
	t.Setenv("NOTIFY_SUBJECT", "from-env")
	r := conditionalBindingApp().Test([]string{"send", "--via", "sms", "--phone-number", "+1"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if strings.Contains(r.Stdout, "from-env") {
		t.Fatalf("an unelected scope's binding was consulted: %q", r.Stdout)
	}
	// Hidden by default.
	if strings.Contains(r.Stdout, "not consulted") {
		t.Fatalf("the diagnostic is not hidden by default: %q", r.Stdout)
	}
}

func TestSkippedBindingIsNamedUnderVerbose(t *testing.T) {
	t.Setenv("NOTIFY_SUBJECT", "from-env")
	r := conditionalBindingApp().Test([]string{"send", "--verbose", "--via", "sms", "--phone-number", "+1"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	want := "not consulted: env var 'NOTIFY_SUBJECT' binds flag '--subject' under '--via email', which was not elected"
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("stdout = %q, want it to contain %q", r.Stdout, want)
	}
}

// The enumeration is over bindings that CARRIED A VALUE. An env var that is not
// set carried nothing, so it is named nowhere -- naming it would report a
// binding the run could not have consulted even with its scope elected (§24.6).
func TestUnsetEnvBindingIsNamedNowhere(t *testing.T) {
	// t.Setenv registers the restore of the pre-test state; the unset that
	// follows is what the test actually needs.
	t.Setenv("NOTIFY_SUBJECT", "placeholder")
	os.Unsetenv("NOTIFY_SUBJECT")
	r := conditionalBindingApp().Test([]string{"send", "--verbose", "--via", "sms", "--phone-number", "+1"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if strings.Contains(r.Stdout, "not consulted") {
		t.Fatalf("an unset env var was named as a skipped binding: %q", r.Stdout)
	}
}

func TestSelectorSourceFollowsElection(t *testing.T) {
	var gotSource, gotProvided string
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("send", "send it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		gotSource = ctx.Source("via")
		if ctx.Provided("via") {
			gotProvided = "yes"
		} else {
			gotProvided = "no"
		}
		return Exit(0)
	}, WithFlags(ChoiceFlag("via", "delivery channel", Default("sms"),
		Choice("email", "as an email", StringFlag("subject", "subject line", Optional())),
		Choice("sms", "as a text", StringFlag("phone-number", "the number", Optional())),
	)), WithEffect(EffectReadOnly))

	if r := app.Test([]string{"send"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if gotSource != "default" || gotProvided != "no" {
		t.Fatalf("defaulted election: source=%q provided=%q", gotSource, gotProvided)
	}
	if r := app.Test([]string{"send", "--via", "email"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if gotSource != "cli" || gotProvided != "yes" {
		t.Fatalf("typed election: source=%q provided=%q", gotSource, gotProvided)
	}
}

// ctx.provided and ctx.source deliberately do NOT see scope interiors (§24.9).
func TestContextDoesNotSeeScopeInteriors(t *testing.T) {
	var panicMsg string
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("send", "send it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		defer func() {
			if r := recover(); r != nil {
				panicMsg, _ = r.(string)
			}
		}()
		ctx.Provided("subject")
		return Exit(0)
	}, WithFlags(ChoiceFlag("via", "delivery channel", Required(),
		Choice("email", "as an email", StringFlag("subject", "subject line", Optional())),
		Choice("sms", "as a text", StringFlag("phone-number", "the number", Optional())),
	)), WithEffect(EffectReadOnly))
	app.Test([]string{"send", "--via", "email", "--subject", "hi"})
	if panicMsg != `no source info for flag "subject"` {
		t.Fatalf("panic = %q, want the unknown-name error", panicMsg)
	}
}

// --- Delivery: Match, When, Is, Get and Provided (§24.9, §24.12) ---

func TestMatchDispatchesOnIdentity(t *testing.T) {
	var line string
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("send", "send it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		via := GetElected(kwargs, "via")
		line = Match(via,
			When(viaEmail, func(f Fields) string { return "email:" + Get[string](f, "subject") }),
			When(viaSMS, func(f Fields) string { return "sms:" + Get[string](f, "phone_number") }),
			When(viaWebhook, func(f Fields) string { return "hook:" + Get[string](f, "url") }),
		)
		return Exit(0)
	}, WithFlags(ChoiceFlag("via", "delivery channel", Required(), viaEmail, viaSMS, viaWebhook)),
		WithEffect(EffectReadOnly))

	if r := app.Test([]string{"send", "--via", "sms", "--phone-number", "+1"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if line != "sms:+1" {
		t.Fatalf("line = %q", line)
	}
}

func TestMatchIsExhaustiveAtDispatch(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("send", "send it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		via := GetElected(kwargs, "via")
		_ = Match(via,
			When(viaEmail, func(f Fields) string { return "email" }),
			When(viaSMS, func(f Fields) string { return "sms" }),
		)
		return Exit(0)
	}, WithFlags(ChoiceFlag("via", "delivery channel", Required(), viaEmail, viaSMS, viaWebhook)),
		WithEffect(EffectReadOnly))

	expectPanic(t, `strictcli.Match: choice flag "via" has no case for webhook`, func() {
		app.Test([]string{"send", "--via", "sms", "--phone-number", "+1"})
	})
}

func TestElectedProvidedAnswersForItsOwnFields(t *testing.T) {
	var providedURL, providedRetries bool
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("send", "send it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		e := GetElected(kwargs, "via")
		providedURL = e.Provided("url")
		providedRetries = e.Provided("retries")
		return Exit(0)
	}, WithFlags(ChoiceFlag("via", "delivery channel", Required(), viaEmail, viaSMS, viaWebhook)),
		WithEffect(EffectReadOnly))

	if r := app.Test([]string{"send", "--via", "webhook", "--url", "http://x"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !providedURL {
		t.Fatal("url was supplied by the invocation, so Provided must be true")
	}
	if providedRetries {
		t.Fatal("retries came from its declared default, so Provided must be false")
	}
}

func TestElectProgrammaticFrontDoor(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("send", "send it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(ChoiceFlag("via", "delivery channel", Required(), viaEmail, viaSMS, viaWebhook)),
		WithEffect(EffectReadOnly))

	if _, err := app.Call("send", map[string]interface{}{
		"via": Elect(viaEmail, Fields{"subject": "hi", "recipient": "a@b"}),
	}); err != nil {
		t.Fatalf("Call error: %v", err)
	}
	e := GetElected(captured, "via")
	if !e.Is(viaEmail) {
		t.Fatalf("elected = %q", e.Name())
	}
	if got := Get[string](e.Fields, "subject"); got != "hi" {
		t.Fatalf("subject = %q", got)
	}
}

// The programmatic front door runs the SAME election, scope and presence
// machinery the argv path uses, so a missing required sub-flag is refused with
// the CLI's own sentence (§24.11).
func TestElectIncompleteRecordIsRefused(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("send", "send it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(ChoiceFlag("via", "delivery channel", Required(), viaEmail, viaSMS, viaWebhook)),
		WithEffect(EffectReadOnly))

	_, err := app.Call("send", map[string]interface{}{"via": Elect(viaEmail, Fields{"subject": "hi"})})
	if err == nil {
		t.Fatal("expected an error for an incomplete elected record")
	}
	if !strings.Contains(err.Error(), "flag '--recipient' is required under '--via email'") {
		t.Fatalf("error = %q", err.Error())
	}
}

// The flat machine form -- the choice name plus top-level scoped parameters --
// is converted into the same record at the protocol boundary (§24.11).
func TestFlatMachineFormElectsThroughTheSameMachinery(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("send", "send it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(ChoiceFlag("via", "delivery channel", Required(), viaEmail, viaSMS, viaWebhook)),
		WithEffect(EffectReadOnly))

	if _, err := app.Call("send", map[string]interface{}{
		"via": "sms", "phone_number": "+15550100",
	}); err != nil {
		t.Fatalf("Call error: %v", err)
	}
	e := GetElected(captured, "via")
	if !e.Is(viaSMS) {
		t.Fatalf("elected = %q", e.Name())
	}

	// A wrong combination is refused at call time with the CLI's own sentence.
	_, err := app.Call("send", map[string]interface{}{"via": "sms", "subject": "hi"})
	if err == nil {
		t.Fatal("expected a scope error")
	}
	if !strings.Contains(err.Error(), "flag '--subject' is only valid under '--via email', but '--via sms' was elected") {
		t.Fatalf("error = %q", err.Error())
	}
}

// --- Registration guards (§12.13's table) ---

func TestSelectorCannotBeOptional(t *testing.T) {
	expectPanic(t, `Flag "via": a choice flag cannot declare Optional(): an absent selection is a choice nobody named, so name it as a choice of its own`, func() {
		ChoiceFlag("via", "delivery channel", Optional(),
			Choice("aa", "choice a"), Choice("bb", "choice b"))
	})
}

func TestSelectorNeedsTwoChoices(t *testing.T) {
	expectPanic(t, `Flag "via": a choice flag must declare at least two choices`, func() {
		ChoiceFlag("via", "delivery channel", Required(), Choice("aa", "choice a"))
	})
}

func TestSelectorDuplicateChoiceName(t *testing.T) {
	expectPanic(t, `Flag "via": choice "aa" is declared twice`, func() {
		ChoiceFlag("via", "delivery channel", Required(),
			Choice("aa", "choice a"), Choice("aa", "choice a again"))
	})
}

func TestChoiceHelpIsRequired(t *testing.T) {
	expectPanic(t, `Choice "aa" of "via": help text is required`, func() {
		ChoiceFlag("via", "delivery channel", Required(),
			Choice("aa", ""), Choice("bb", "choice b"))
	})
}

func TestSelectorDefaultMustNameAChoice(t *testing.T) {
	expectPanic(t, `Flag "via": Default(carrier) names no declared choice: must be one of: aa, bb`, func() {
		ChoiceFlag("via", "delivery channel", Default("carrier"),
			Choice("aa", "choice a"), Choice("bb", "choice b"))
	})
}

// A defaulted selection is COMPLETE, and Go's mechanism for that is a
// registration check (§24.5; the template is Python-excluded).
func TestSelectorDefaultMustBeComplete(t *testing.T) {
	expectPanic(t, `Flag "via": Default("aa") elects choice "aa", whose scope declares the required flag '--target': a defaulted selection must be complete with nothing typed`, func() {
		ChoiceFlag("via", "delivery channel", Default("aa"),
			Choice("aa", "choice a", StringFlag("target", "the target", Required())),
			Choice("bb", "choice b"))
	})
}

// Electing a choice on the command line never borrows the default's values.
func TestElectionNeverBorrowsTheDefault(t *testing.T) {
	app := simpleApp("cmd", "a command", "mode={mode}",
		WithFlags(ChoiceFlag("mode", "the mode", Default("quick"),
			Choice("quick", "quick mode"),
			Choice("full", "full mode", StringFlag("target", "the target", Required())),
		)))
	r := app.Test([]string{"cmd", "--mode", "full"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, "error: flag '--target' is required under '--mode full'\n") {
		t.Fatalf("stderr = %q", r.Stderr)
	}
}

func TestTokenChoiceCannotCarryAPayload(t *testing.T) {
	expectPanic(t, `Choice "profile" of "via": a token-spelled choice cannot carry a payload: the token names the choice, and a choice that carries its own value belongs to a member-spelled choice flag, declared with MemberChoiceFlag(...)`, func() {
		ChoiceFlag("via", "delivery channel", Required(),
			MemberChoice(StringFlag("profile", "a profile", Required()), "use a profile"),
			Choice("bb", "choice b"))
	})
}

func TestMemberChoiceFlagRequiresMemberChoices(t *testing.T) {
	expectPanic(t, `Choice "bb" of "via": a member-spelled choice flag declares its choices with MemberChoice(...), which names the flag that elects the choice`, func() {
		MemberChoiceFlag("via", "delivery channel", Required(),
			MemberChoice(StringFlag("profile", "a profile", Required()), "use a profile"),
			Choice("bb", "choice b"))
	})
}

// --- Reserved names inside a scope (§12.13, S15) ---

func TestScopedNameChoiceIsReserved(t *testing.T) {
	expectPanic(t, `Choice "aa" of "via": flag name 'choice' is reserved by the framework: it tags the delivered record`, func() {
		ChoiceFlag("via", "delivery channel", Required(),
			Choice("aa", "choice a", StringFlag("choice", "a choice", Optional())),
			Choice("bb", "choice b"))
	})
}

func TestScopedNameValueIsReserved(t *testing.T) {
	expectPanic(t, `Choice "aa" of "via": flag name 'value' is reserved by the framework: it carries a member-spelled choice's own payload`, func() {
		ChoiceFlag("via", "delivery channel", Required(),
			Choice("aa", "choice a", StringFlag("value", "a value", Optional())),
			Choice("bb", "choice b"))
	})
}

// Every EXISTING name ban re-runs at every depth. A ban enforced only against a
// flat root list is this construct's most likely correctness defect (§24.7).
func TestEveryNameBanReRunsAtEveryDepth(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"dry-run", "reserved by the framework"},
		{"json", "reserved by the framework"},
		{"yes", "banned"},
		{"force", "reserved name"},
		{"no-cache", "reserved"},
		{"approve-consequential", "reserved"},
	}
	for _, c := range cases {
		func() {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("flag %q was accepted three scopes down", c.name)
				}
				if msg, ok := r.(string); !ok || !strings.Contains(msg, c.want) {
					t.Fatalf("flag %q: panic %v, want it to contain %q", c.name, r, c.want)
				}
			}()
			ChoiceFlag("outer", "the outer selector", Required(),
				Choice("aa", "choice a",
					ChoiceFlag("inner", "the inner selector", Required(),
						Choice("xx", "choice x", StringFlag(c.name, "a flag", Optional())),
						Choice("yy", "choice y"),
					)),
				Choice("bb", "choice b"))
		}()
	}
}

// --- Name collisions (§12.13, §24.7) ---

func TestScopedNameCollidesWithRootFlag(t *testing.T) {
	expectPanic(t, `Choice "aa" of "mode": flag '--target' collides with a command-level flag of the same name: the scoped one could never be reached`, func() {
		simpleApp("cmd", "a command", "ok", WithFlags(
			StringFlag("target", "the target", Optional()),
			ChoiceFlag("mode", "the mode", Required(),
				Choice("aa", "choice a", StringFlag("target", "the target", Optional())),
				Choice("bb", "choice b")),
		))
	})
}

func TestScopedNameCollidesWithSelectorName(t *testing.T) {
	expectPanic(t, `Choice "aa" of "mode": flag '--mode' collides with the choice flag's own name`, func() {
		ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "choice a", StringFlag("mode", "the mode again", Optional())),
			Choice("bb", "choice b"))
	})
}

func TestSiblingScopesMayReuseANameWithTheSameShape(t *testing.T) {
	app := simpleApp("cmd", "a command", "mode={mode}",
		WithFlags(ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "choice a", StringFlag("target", "the target", Optional())),
			Choice("bb", "choice b", StringFlag("target", "the target", Optional())),
		)))
	r := app.Test([]string{"cmd", "--mode", "bb", "--target", "xx"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "mode=bb[target:xx]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestSiblingScopeTypeMismatchIsRefused(t *testing.T) {
	expectPanic(t, `Flag "mode": flag '--target' is declared by choices "aa" and "bb" with different value shapes: sibling scopes may reuse a name only with an identical type and arity, because tokenizing '--target' cannot wait for an election`, func() {
		ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "choice a", StringFlag("target", "the target", Optional())),
			Choice("bb", "choice b", IntFlag("target", "the target", Optional())),
		)
	})
}

// The template covers type AND arity in one sentence (§18.18 item 208).
func TestSiblingScopeArityMismatchIsRefused(t *testing.T) {
	expectPanic(t, `Flag "mode": flag '--target' is declared by choices "aa" and "bb" with different value shapes`, func() {
		ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "choice a", StringFlag("target", "the target", Optional())),
			Choice("bb", "choice b", ListFlag(TypeStr, "target", "the targets", Optional(), Unique(false))),
		)
	})
}

func TestSimultaneouslyElectableScopesMayNotReuseAName(t *testing.T) {
	expectPanic(t, `command "cmd": flag '--target' is declared under '--one aa' and under '--two cc', which can be elected at the same time: simultaneously electable scopes may not reuse a flag name`, func() {
		simpleApp("cmd", "a command", "ok", WithFlags(
			ChoiceFlag("one", "the first selector", Required(),
				Choice("aa", "choice a", StringFlag("target", "the target", Optional())),
				Choice("bb", "choice b")),
			ChoiceFlag("two", "the second selector", Required(),
				Choice("cc", "choice c", StringFlag("target", "the target", Optional())),
				Choice("dd", "choice d")),
		))
	})
}

func TestShortsAreClaimedAcrossSimultaneouslyLiveScopes(t *testing.T) {
	expectPanic(t, `command "cmd": short '-t' is claimed by '--target' and '--tag', which can be elected at the same time`, func() {
		simpleApp("cmd", "a command", "ok", WithFlags(
			ChoiceFlag("one", "the first selector", Required(),
				Choice("aa", "choice a", StringFlag("target", "the target", Optional(), Short("t"))),
				Choice("bb", "choice b")),
			ChoiceFlag("two", "the second selector", Required(),
				Choice("cc", "choice c", StringFlag("tag", "the tag", Optional(), Short("t"))),
				Choice("dd", "choice d")),
		))
	})
}

func TestSiblingScopesMayReuseAShort(t *testing.T) {
	app := simpleApp("cmd", "a command", "mode={mode}",
		WithFlags(ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "choice a", StringFlag("target", "the target", Optional(), Short("t"))),
			Choice("bb", "choice b", StringFlag("tag", "the tag", Optional(), Short("t"))),
		)))
	r := app.Test([]string{"cmd", "--mode", "bb", "-t", "xx"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "mode=bb[tag:xx]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

// --- Constraints operate at root scope only (§24.8) ---

func TestConstraintNamingAScopedFlagIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "target-needs-host" references 'target', which is declared under '--mode aa': constraints operate at root scope only`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("host", "the host", Optional()),
				ChoiceFlag("mode", "the mode", Required(),
					Choice("aa", "choice a", StringFlag("target", "the target", Optional())),
					Choice("bb", "choice b")),
			),
			WithConstraints(Requires("target-needs-host", "target", "host")))
	})
}

func TestAllOrNoneNamingAScopedFlagIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "pair" references 'target', which is declared under '--mode aa': constraints operate at root scope only`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("host", "the host", Optional()),
				ChoiceFlag("mode", "the mode", Required(),
					Choice("aa", "choice a", StringFlag("target", "the target", Optional())),
					Choice("bb", "choice b")),
			),
			WithConstraints(AllOrNone("pair", Member("host"), Member("target"))))
	})
}

// --- The value-flag record (§24.2, §24.12) ---

func TestChoicesEntryMustBeARecord(t *testing.T) {
	expectPanic(t, `Flag "target": choices entry 1 is a bare value: declare it as Ch(<value>, "<help>")`, func() {
		StringFlag("target", "the target", Optional(),
			Choices(Ch("head", "push only the current HEAD branch"), ChoiceValue{Value: "tags"}))
	})
}

func TestChoicesRecordWithNoHelpKeepsTheOneLineForm(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("target", "what to push", Optional(),
			Choices(Ch("head", ""), Ch("tags", "")))))
	r := app.Test([]string{"cmd", "--help"})
	if !strings.Contains(r.Stdout, "[choices: head, tags]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

// --- Help rendering (§24.10) ---

func TestSelectorHelpRendersTheIndentedBlock(t *testing.T) {
	app := notifyApp()
	r := app.Test([]string{"send", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	for _, want := range []string{
		"  --via, -v <choice>",
		"    email  ",
		"      --subject <str>",
		"      --recipient <str>",
		"    sms  ",
		"      --phone-number <str>",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Fatalf("help missing %q:\n%s", want, r.Stdout)
		}
	}
	// One alignment column across the whole block, deepest entry included, and
	// §23.8's exactly-one-presence-part invariant at every depth.
	for _, line := range strings.Split(r.Stdout, "\n") {
		if strings.Contains(line, "--subject") && !strings.HasSuffix(line, "[required]") {
			t.Fatalf("scoped flag line has no presence part: %q", line)
		}
	}
}

func TestValueFlagHelpRendersTheBlockOnceAnEntryCarriesHelp(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(StringFlag("target", "what to push", Optional(),
			Choices(Ch("head", "push only the current HEAD branch"), Ch("tags", "")))))
	r := app.Test([]string{"cmd", "--help"})
	if strings.Contains(r.Stdout, "[choices:") {
		t.Fatalf("expected block form, got the one-line form:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "    head") || !strings.Contains(r.Stdout, "push only the current HEAD branch") {
		t.Fatalf("help missing the entry block:\n%s", r.Stdout)
	}
	// An entry with no help renders the value alone.
	for _, line := range strings.Split(r.Stdout, "\n") {
		if strings.TrimSpace(line) == "tags" {
			return
		}
	}
	t.Fatalf("expected a bare 'tags' line:\n%s", r.Stdout)
}

func TestMemberSelectorHelpRendersTheHeading(t *testing.T) {
	app := electionApp()
	r := app.Test([]string{"run", "--help"})
	// The clause rides the RIGHT column, after the selector's help and before
	// its presence part (§24.10); the left column carries the selector's own
	// name, which is never a token.
	if !strings.Contains(r.Stdout, "(exactly one of the following) [required]") {
		t.Fatalf("help missing the member heading:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "\n    --profile <str>") {
		t.Fatalf("help missing the member flag line two columns beneath:\n%s", r.Stdout)
	}
}

// --- MCP projection: flatten plus a description map (§24.11) ---

func TestMCPProjectionFlattensAndExcludesScopedFromRequired(t *testing.T) {
	app := notifyApp()
	schema := app.JsonSchema("send")
	props := schema["properties"].(map[string]interface{})

	via, ok := props["via"].(map[string]interface{})
	if !ok {
		t.Fatalf("no 'via' property: %v", props)
	}
	if via["type"] != "string" {
		t.Fatalf("via type = %v", via["type"])
	}
	enum := via["enum"].([]interface{})
	if len(enum) != 3 || enum[0] != "email" {
		t.Fatalf("via enum = %v", enum)
	}
	for _, name := range []string{"subject", "recipient", "phone_number", "url", "retries"} {
		if _, ok := props[name]; !ok {
			t.Fatalf("scoped flag %q is missing from the flat schema: %v", name, props)
		}
	}
	required := schema["required"].([]interface{})
	for _, r := range required {
		if r != "via" {
			t.Fatalf("a scoped flag reached 'required': %v", required)
		}
	}
	if len(required) != 1 {
		t.Fatalf("required = %v, want just the selector", required)
	}
}

func TestMCPDescriptionCarriesTheScopeBlock(t *testing.T) {
	app := notifyApp()
	var tool *Tool
	for i, tl := range app.AsTools() {
		if tl.Name == "send" {
			tool = &app.AsTools()[i]
		}
	}
	if tool == nil {
		t.Fatal("no 'send' tool")
	}
	want := "Scoped parameters (enforced at call time):\n" +
		"  via=email: subject (required), recipient (required)\n" +
		"  via=sms: phone_number (required)\n" +
		"  via=webhook: url (required), retries (default: 3)"
	if !strings.Contains(tool.Description, want) {
		t.Fatalf("description = %q, want it to contain %q", tool.Description, want)
	}
}

func TestMCPDescriptionRendersAnEmptyScope(t *testing.T) {
	app := recursiveApp()
	tools := app.AsTools()
	var desc string
	for _, tl := range tools {
		if tl.Name == "add" {
			desc = tl.Description
		}
	}
	want := "  visibility=user-facing type=feature: headline (required)"
	if !strings.Contains(desc, want) {
		t.Fatalf("description = %q, want it to contain %q", desc, want)
	}
	if !strings.Contains(desc, "  visibility=internal: (no parameters)") {
		t.Fatalf("description = %q, want the empty-scope line", desc)
	}
}

// A member-spelled selector projects IDENTICALLY to a token-spelled one:
// tokenization is a command-line fact and there are no tokens at this boundary.
func TestMCPProjectionOfAMemberSpelledSelector(t *testing.T) {
	app := electionApp()
	schema := app.JsonSchema("run")
	props := schema["properties"].(map[string]interface{})
	mode := props["mode"].(map[string]interface{})
	if mode["type"] != "string" {
		t.Fatalf("mode type = %v", mode["type"])
	}
	enum := mode["enum"].([]interface{})
	if len(enum) != 3 || enum[0] != "profile" {
		t.Fatalf("mode enum = %v", enum)
	}
	// A member's payload flattens under the MEMBER's own flag name.
	if _, ok := props["profile"]; !ok {
		t.Fatalf("member payload missing from the flat schema: %v", props)
	}
}

// --- Unaffected by the construct, re-verified per surface (§24.3) ---

func TestScopedBoolNegationStillWorks(t *testing.T) {
	app := simpleApp("cmd", "a command", "mode={mode}",
		WithFlags(ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "choice a", BoolFlag("cache", "use the cache", Default(true))),
			Choice("bb", "choice b"),
		)))
	r := app.Test([]string{"cmd", "--mode", "aa", "--no-cache"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "mode=aa[cache:false]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestScopedRepeatableAndDictSubFlags(t *testing.T) {
	app := simpleApp("cmd", "a command", "mode={mode}",
		WithFlags(ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "choice a",
				ListFlag(TypeStr, "tag", "a tag", Optional(), Unique(false)),
				DictFlag(TypeStr, "label", "a label", Optional(), Unique(false)),
			),
			Choice("bb", "choice b"),
		)))
	r := app.Test([]string{"cmd", "--mode", "aa", "--tag", "xx", "--tag", "yy", "--label", "k=v"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "label:k=v") || !strings.Contains(r.Stdout, "tag:[xx yy]") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestScopedFlagAtPrefixResolves(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/body.txt"
	if err := writeFileForTest(path, "from-file"); err != nil {
		t.Fatalf("write: %v", err)
	}
	app := simpleApp("cmd", "a command", "mode={mode}",
		WithFlags(ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "choice a", StringFlag("body", "the body", Optional())),
			Choice("bb", "choice b"),
		)))
	r := app.Test([]string{"cmd", "--mode", "aa", "--body", "@" + path})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "body:from-file") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

// A scoped flag's coercion failure is a VALUE error, and it is reported after
// election and scope but before presence (§24.3's precedence).
func TestScopedValueErrorAfterScopeBeforePresence(t *testing.T) {
	app := simpleApp("cmd", "a command", "mode={mode}",
		WithFlags(ChoiceFlag("mode", "the mode", Required(),
			Choice("aa", "choice a",
				IntFlag("count", "how many", Optional()),
				StringFlag("target", "the target", Required()),
			),
			Choice("bb", "choice b"),
		)))
	r := app.Test([]string{"cmd", "--mode", "aa", "--count", "nope"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--count: expected integer") {
		t.Fatalf("stderr = %q, want the value error before the presence error", r.Stderr)
	}
}

// The skipped-binding diagnostics ride machine mode's diagnostics array at
// level "debug", whatever the human stream did (§24.6, §19.2).
func TestSkippedBindingRidesMachineDiagnostics(t *testing.T) {
	t.Setenv("NOTIFY_SUBJECT", "from-env")
	r := conditionalBindingApp().Test([]string{"send", "--json", "--via", "sms", "--phone-number", "+1"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, `"level":"debug"`) ||
		!strings.Contains(r.Stdout, "not consulted: env var 'NOTIFY_SUBJECT'") {
		t.Fatalf("envelope = %q", r.Stdout)
	}
}

// writeFileForTest writes a fixture file for the @-prefix test.
func writeFileForTest(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}

// --- Schema dump: the selector rides §25.6's encoding ---
//
// A selector's value shape is a variant, which the closed JSON Schema subset
// cannot express, so the entry carries no fragment at all and its `choices` are
// choice objects. `elect_by` is the discriminator. The full encoding is pinned
// in schema_v2_test.go; this is the selector round's own end-state check that a
// dump no longer flattens the construct away.
func TestSchemaDumpCarriesTheSelectorEntry(t *testing.T) {
	chdirTemp(t)
	app := notifyApp()
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	cmd := schema["commands"].(map[string]interface{})["send"].(map[string]interface{})
	flags := cmd["flags"].([]interface{})
	var via map[string]interface{}
	for _, f := range flags {
		m := f.(map[string]interface{})
		if m["name"] == "via" {
			via = m
		}
	}
	if via == nil {
		t.Fatalf("no 'via' flag entry: %v", flags)
	}
	if _, present := via["value_schema"]; present {
		t.Fatalf("a selector entry must carry no fragment: %v", via)
	}
	if via["elect_by"] != "selector-token" {
		t.Fatalf("elect_by = %v", via["elect_by"])
	}
	if via["presence"] != "required" {
		t.Fatalf("presence = %v", via["presence"])
	}
}

// --- The remaining pinned origin clauses and "why" clause (§12.13) ---

func TestElectionFromConfigNamesItself(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	writeConfig(t, tmpDir, "testapp", map[string]interface{}{"via": "sms"})

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("send", "send it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(ChoiceFlag("via", "delivery channel", Required(),
		Choice("email", "as an email", StringFlag("subject", "subject line", Required())),
		Choice("sms", "as a text", StringFlag("phone-number", "the number", Required())),
	)), WithEffect(EffectReadOnly))

	r := app.Test([]string{"send", "--subject", "hi"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--subject' is only valid under '--via email', but '--via sms' was elected from config key 'via'\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

func TestElectionByDefaultNamesItself(t *testing.T) {
	app := simpleApp("send", "send it", "via={via}",
		WithFlags(ChoiceFlag("via", "delivery channel", Default("sms"),
			Choice("email", "as an email", StringFlag("subject", "subject line", Optional())),
			Choice("sms", "as a text", StringFlag("phone-number", "the number", Optional())),
		)))
	r := app.Test([]string{"send", "--subject", "hi"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--subject' is only valid under '--via email', but '--via sms' was elected by default\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// The member-spelled twin of "'--<sel>' was not provided": no selector token is
// ever typed, so the clause names the members instead.
func TestOutOfScopeWhenNoMemberElected(t *testing.T) {
	app := simpleApp("run", "run it", "mode={mode}",
		WithFlags(MemberChoiceFlag("mode", "which profiles", Required(),
			MemberChoice(StringFlag("profile", "a profile", Required()), "one named profile",
				BoolFlag("create-missing", "create it if absent", Default(false)),
			),
			MemberChoice(BoolFlag("all-profiles", "every profile", Required()), "every profile"),
		)))
	r := app.Test([]string{"run", "--create-missing"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--create-missing' is only valid under '--profile', but none of --profile, --all-profiles was elected\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// A required MEMBER-spelled selector one level down carries the scope suffix.
func TestNestedMemberSelectorCarriesTheScopeSuffix(t *testing.T) {
	app := simpleApp("cmd", "a command", "mode={mode}",
		WithFlags(ChoiceFlag("mode", "the mode", Required(),
			Choice("advanced", "the advanced mode",
				MemberChoiceFlag("profile-set", "which profiles", Required(),
					MemberChoice(StringFlag("profile", "a profile", Required()), "one named profile"),
					MemberChoice(BoolFlag("all-profiles", "every profile", Required()), "every profile"),
				)),
			Choice("simple", "the simple mode"),
		)))
	r := app.Test([]string{"cmd", "--mode", "advanced"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: one of --profile, --all-profiles is required under '--mode advanced'\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// --- Match's other two dispatch guards (Go-only, §24.12) ---

func TestMatchRefusesAForeignCase(t *testing.T) {
	other := Choice("email", "a choice of another selector", StringFlag("subject", "ss", Optional()))
	ChoiceFlag("elsewhere", "another selector", Required(), other, Choice("zz", "choice z"))

	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("send", "send it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		_ = Match(GetElected(kwargs, "via"),
			When(other, func(f Fields) string { return "other" }),
			When(viaSMS, func(f Fields) string { return "sms" }),
			When(viaWebhook, func(f Fields) string { return "hook" }),
		)
		return Exit(0)
	}, WithFlags(ChoiceFlag("via", "delivery channel", Required(), viaEmail, viaSMS, viaWebhook)),
		WithEffect(EffectReadOnly))

	expectPanic(t, `strictcli.Match: case "email" is not a choice of choice flag "via"`, func() {
		app.Test([]string{"send", "--via", "sms", "--phone-number", "+1"})
	})
}

func TestChoiceValueBelongsToExactlyOneSelector(t *testing.T) {
	shared := Choice("aa", "choice a")
	ChoiceFlag("first", "the first selector", Required(), shared, Choice("bb", "choice b"))
	expectPanic(t, `Choice "aa" of "second": a choice value belongs to exactly one choice flag; it is already declared by "first"`, func() {
		ChoiceFlag("second", "the second selector", Required(), shared, Choice("cc", "choice c"))
	})
}

// --- Item 223: a member choice name IS a flag name (§24.7) ---

// A member choice's name is the flag that elects it, so a command-level flag of
// the same name is TWO DECLARATIONS OF ONE FLAG NAME rather than an unreachable
// scoped flag. The plain duplicate-flag error is the one that says so.
func TestMemberChoiceNameCollidingWithARootFlagIsADuplicateFlagName(t *testing.T) {
	expectPanic(t, `command "cmd": duplicate flag name "profile"`, func() {
		simpleApp("cmd", "a command", "ok", WithFlags(
			StringFlag("profile", "a command-level flag", Optional()),
			MemberChoiceFlag("mode", "which profiles", Required(),
				MemberChoice(StringFlag("profile", "a profile", Required()), "one named profile"),
				MemberChoice(BoolFlag("all-profiles", "every profile", Required()), "every profile"),
			),
		))
	})
}

// The same collision one level down: depth changes nothing about what the name
// IS, so the sentence is the same one.
func TestNestedMemberChoiceNameCollidingWithARootFlagIsADuplicateFlagName(t *testing.T) {
	expectPanic(t, `command "cmd": duplicate flag name "profile"`, func() {
		simpleApp("cmd", "a command", "ok", WithFlags(
			StringFlag("profile", "a command-level flag", Optional()),
			ChoiceFlag("mode", "the mode", Required(),
				Choice("advanced", "the advanced mode",
					MemberChoiceFlag("profile-set", "which profiles", Required(),
						MemberChoice(StringFlag("profile", "a profile", Required()), "one named profile"),
						MemberChoice(BoolFlag("all-profiles", "every profile", Required()), "every profile"),
					)),
				Choice("simple", "the simple mode")),
		))
	})
}

// A member's SCOPE is not the member: an ordinary scoped flag colliding with a
// command-level flag keeps the unreachable-scoped-flag sentence.
func TestScopedFlagUnderAMemberCollidingWithARootFlagKeepsItsOwnError(t *testing.T) {
	expectPanic(t, `Choice "profile" of "mode": flag '--create-missing' collides with a command-level flag of the same name: the scoped one could never be reached`, func() {
		simpleApp("cmd", "a command", "ok", WithFlags(
			BoolFlag("create-missing", "a command-level flag", Default(false)),
			MemberChoiceFlag("mode", "which profiles", Required(),
				MemberChoice(StringFlag("profile", "a profile", Required()), "one named profile",
					BoolFlag("create-missing", "create it if absent", Default(false)),
				),
				MemberChoice(BoolFlag("all-profiles", "every profile", Required()), "every profile"),
			),
		))
	})
}

// --- §24.7's choice-name charset ---

func TestChoiceNameCharsetIsEnforcedOnTokenSpelling(t *testing.T) {
	expectPanic(t, `Flag "via": choice name "Email" must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*`, func() {
		ChoiceFlag("via", "delivery channel", Required(),
			Choice("Email", "an email message"),
			Choice("sms", "a text message"))
	})
}

// A member choice's name is its member flag's name, so the flag constructor's
// naming rule refuses it before the selector sees it.
func TestChoiceNameCharsetIsEnforcedOnMemberSpelling(t *testing.T) {
	expectPanic(t, `flag name "all_profiles" must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*`, func() {
		MemberChoiceFlag("mode", "which profiles", Required(),
			MemberChoice(StringFlag("profile", "a profile", Required()), "one named profile"),
			MemberChoice(BoolFlag("all_profiles", "every profile", Required()), "every profile"))
	})
}

func TestChoiceNameCharsetRejectsALeadingDigitAndALeadingDash(t *testing.T) {
	expectPanic(t, `Flag "via": choice name "2fa" must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*`, func() {
		ChoiceFlag("via", "delivery channel", Required(),
			Choice("2fa", "second factor"),
			Choice("sms", "a text message"))
	})
	expectPanic(t, `Flag "via": choice name "-email" must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*`, func() {
		ChoiceFlag("via", "delivery channel", Required(),
			Choice("-email", "an email message"),
			Choice("sms", "a text message"))
	})
}

// The naming rule: digits and single inner dashes after the first character
// are legal; a trailing or doubled dash is not.
func TestChoiceNameCharsetAcceptsDigitsAndDashes(t *testing.T) {
	ChoiceFlag("via", "delivery channel", Required(),
		Choice("email-2", "an email message"),
		Choice("sms-v2", "a text message"))
	for _, bad := range []string{"sms-", "sms--v2"} {
		expectPanic(t, `Flag "via": choice name "`+bad+`" must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*`, func() {
			ChoiceFlag("via", "delivery channel", Required(),
				Choice("email-2", "an email message"),
				Choice(bad, "a text message"))
		})
	}
}

// --- Item 224: the owners clause of a MEMBER flag stops at its owning scope ---

// scopedMemberApp declares a member-spelled selector one level inside a
// token-spelled one, which is the only shape where a member flag can be
// supplied out of scope at all: its own selector is unreachable.
func scopedMemberApp() *App {
	return simpleApp("run", "run it", "mode={mode}",
		WithFlags(ChoiceFlag("mode", "the mode", Required(),
			Choice("advanced", "the advanced mode",
				MemberChoiceFlag("profile-set", "which profiles", Required(),
					MemberChoice(StringFlag("profile", "a profile", Required()), "one named profile",
						BoolFlag("create-missing", "create it if absent", Default(false)),
					),
					MemberChoice(BoolFlag("all-profiles", "every profile", Required()), "every profile"),
				)),
			Choice("simple", "the simple mode"),
		)))
}

// A member flag is declared BY its own election, not under it: the scope path
// that owns it is the path without that last segment.
func TestOutOfScopeMemberFlagNamesTheOwningScopeOnly(t *testing.T) {
	r := scopedMemberApp().Test([]string{"run", "--mode", "simple", "--profile", "work"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--profile' is only valid under '--mode advanced', but '--mode simple' was elected\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// The other why-clause, over the same owners clause.
func TestOutOfScopeMemberFlagNamesTheOwningScopeOnlyWhenNothingWasElected(t *testing.T) {
	r := scopedMemberApp().Test([]string{"run", "--profile", "work"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--profile' is only valid under '--mode advanced', but '--mode' was not provided\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// A flag in the member's SCOPE keeps every segment, the member's election
// included: it really is declared under that election.
func TestOutOfScopeFlagUnderAMemberKeepsTheWholePath(t *testing.T) {
	r := scopedMemberApp().Test([]string{"run", "--mode", "simple", "--create-missing"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--create-missing' is only valid under '--mode advanced --profile', but '--mode simple' was elected\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// A live member's sibling scope is still refused with the whole path -- the
// member's own election is live here, so nothing is trimmed by accident.
func TestScopedFlagOfANonElectedSiblingMemberKeepsTheWholePath(t *testing.T) {
	r := scopedMemberApp().Test([]string{"run", "--mode", "advanced", "--all-profiles", "--create-missing"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: flag '--create-missing' is only valid under '--mode advanced --profile', but '--mode advanced --all-profiles' was elected\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}
