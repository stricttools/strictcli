package strictcli

import (
	"os"
	"strings"
	"testing"
)

// The presence declaration (effects contract §23): every flag and every
// positional arg declares EXACTLY ONE of required, optional, or a value
// default, at registration, and nothing about presence is inferred from the
// shape of another declaration.
//
// Registration-error texts here are asserted BYTE-EXACT: they are pinned in
// §12.12, where the sentence is byte-identical across the three
// implementations and the spellings inside it are each language's own.

// ---------------------------------------------------------------------------
// The zero case: declaring nothing does not register
// ---------------------------------------------------------------------------

func TestFlagPresenceUndeclared(t *testing.T) {
	want := `Flag "target": presence is undeclared: declare exactly one of Required(), Optional(), or Default(<value>)`
	if got := mustPanic(t, func() { StringFlag("target", "where to deploy") }); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFlagPresenceUndeclaredEveryConstructor(t *testing.T) {
	cases := map[string]func(){
		"str":   func() { StringFlag("xx", "hh") },
		"bool":  func() { BoolFlag("xx", "hh") },
		"int":   func() { IntFlag("xx", "hh") },
		"float": func() { FloatFlag("xx", "hh") },
		"list":  func() { ListFlag(TypeStr, "xx", "hh", Unique(false)) },
		"dict":  func() { DictFlag(TypeStr, "xx", "hh", Unique(false)) },
	}
	want := `Flag "xx": presence is undeclared: declare exactly one of Required(), Optional(), or Default(<value>)`
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			if got := mustPanic(t, fn); got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestArgPresenceUndeclared(t *testing.T) {
	want := `Arg "target": presence is undeclared: declare exactly one of ArgRequired(), ArgOptional(), or ArgDefault(<value>)`
	if got := mustPanic(t, func() { NewArg("target", "the target") }); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestGlobalFlagPresenceUndeclared(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	want := `Flag "level": presence is undeclared: declare exactly one of Required(), Optional(), or Default(<value>)`
	got := mustPanic(t, func() {
		app.GlobalFlag(Flag{Name: "level", Type: TypeStr, Help: "verbosity"})
	})
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// The struct-literal trap, closed (§23.2)
//
// An exported Default field set directly on a Flag literal left hasDefault
// false and was SILENTLY IGNORED at parse time. Such a flag now declares no
// presence and does not register at all, so the value cannot be dropped.
// ---------------------------------------------------------------------------

func TestFlagStructLiteralWithDefaultFieldDoesNotRegister(t *testing.T) {
	want := `Flag "mode": presence is undeclared: declare exactly one of Required(), Optional(), or Default(<value>)`
	got := mustPanic(t, func() {
		app := NewApp("myapp", "1.0.0", "test app")
		app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
			return Exit(0)
		}, WithFlags(Flag{Name: "mode", Type: TypeStr, Help: "the mode", Default: "fast"}),
			WithEffect(EffectReadOnly))
	})
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFlagStructLiteralInFlagSetDoesNotRegister(t *testing.T) {
	want := `Flag "mode": presence is undeclared: declare exactly one of Required(), Optional(), or Default(<value>)`
	got := mustPanic(t, func() {
		app := NewApp("myapp", "1.0.0", "test app")
		app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
			return Exit(0)
		}, WithFlagSets(FlagSet{Name: "common", Flags: []Flag{
			{Name: "mode", Type: TypeStr, Help: "the mode", Default: "fast"},
		}}), WithEffect(EffectReadOnly))
	})
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Presence is mandatory at EVERY depth: a struct literal inside a choice scope
// declares none and does not register (contract §23.1 as amended by §18.15
// item 178, §24.1).
func TestFlagStructLiteralInScopeDoesNotRegister(t *testing.T) {
	want := `Flag "aa": presence is undeclared: declare exactly one of Required(), Optional(), or Default(<value>)`
	got := mustPanic(t, func() {
		ChoiceFlag("via", "delivery channel", Required(),
			Choice("email", "as an email", Flag{Name: "aa", Type: TypeStr, Help: "aa"}),
			Choice("sms", "as a text", StringFlag("bb", "bb", Optional())),
		)
	})
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestArgStructLiteralDoesNotRegister(t *testing.T) {
	want := `Arg "path": presence is undeclared: declare exactly one of ArgRequired(), ArgOptional(), or ArgDefault(<value>)`
	got := mustPanic(t, func() {
		app := NewApp("myapp", "1.0.0", "test app")
		app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
			return Exit(0)
		}, WithArgs(Arg{Name: "path", Help: "the path", Type: TypeStr}),
			WithEffect(EffectReadOnly))
	})
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Two declared: the message names the two, in canonical order
// ---------------------------------------------------------------------------

func TestFlagPresenceDeclaredTwice(t *testing.T) {
	cases := []struct {
		name string
		fn   func()
		want string
	}{
		{
			"required+optional",
			func() { StringFlag("xx", "hh", Required(), Optional()) },
			`Flag "xx": presence is declared twice: Required() and Optional() cannot be combined; declare exactly one`,
		},
		{
			"required+default",
			func() { StringFlag("xx", "hh", Required(), Default("fast")) },
			`Flag "xx": presence is declared twice: Required() and Default(fast) cannot be combined; declare exactly one`,
		},
		{
			"optional+default",
			func() { IntFlag("xx", "hh", Optional(), Default(5)) },
			`Flag "xx": presence is declared twice: Optional() and Default(5) cannot be combined; declare exactly one`,
		},
		{
			// Written the other way round, rendered the same way: the order is
			// canonical (required, optional, default), never the written one.
			"default+required",
			func() { StringFlag("xx", "hh", Default("fast"), Required()) },
			`Flag "xx": presence is declared twice: Required() and Default(fast) cannot be combined; declare exactly one`,
		},
		{
			"all three",
			func() { BoolFlag("xx", "hh", Default(true), Optional(), Required()) },
			`Flag "xx": presence is declared twice: Required() and Optional() cannot be combined; declare exactly one`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustPanic(t, tc.fn); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestArgPresenceDeclaredTwice(t *testing.T) {
	cases := []struct {
		name string
		fn   func()
		want string
	}{
		{
			"required+optional",
			func() { NewArg("xx", "hh", ArgRequired(), ArgOptional()) },
			`Arg "xx": presence is declared twice: ArgRequired() and ArgOptional() cannot be combined; declare exactly one`,
		},
		{
			// This pair used to be `required arg cannot have a default`, which
			// the two-declared error subsumes: it says the same thing and names
			// both spellings.
			"required+default",
			func() { NewArg("xx", "hh", ArgRequired(), ArgDefault("prod")) },
			`Arg "xx": presence is declared twice: ArgRequired() and ArgDefault(prod) cannot be combined; declare exactly one`,
		},
		{
			"optional+default",
			func() { NewArg("xx", "hh", ArgOptional(), ArgDefault("prod")) },
			`Arg "xx": presence is declared twice: ArgOptional() and ArgDefault(prod) cannot be combined; declare exactly one`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustPanic(t, tc.fn); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPresenceDeclaredTwiceRendersNilDefaultAsNilSpelling pins the second half
// of §12.12's implementation-sweep box (ledger item 154): the count check runs
// BEFORE the null-default refusal, so a nil default written beside a presence
// declaration reaches the declared-twice message -- and there it renders as
// `Default(nil)` / `ArgDefault(nil)` rather than through formatValueForError,
// because the message must name the spelling that was actually written.
//
// The redirect is reserved for the nil default written as the sole declaration
// (the two tests below).
func TestPresenceDeclaredTwiceRendersNilDefaultAsNilSpelling(t *testing.T) {
	cases := []struct {
		name string
		fn   func()
		want string
	}{
		{
			"flag required+nil default",
			func() { StringFlag("xx", "hh", Required(), Default(nil)) },
			`Flag "xx": presence is declared twice: Required() and Default(nil) cannot be combined; declare exactly one`,
		},
		{
			"flag optional+nil default",
			func() { StringFlag("xx", "hh", Optional(), Default(nil)) },
			`Flag "xx": presence is declared twice: Optional() and Default(nil) cannot be combined; declare exactly one`,
		},
		{
			// Written default-first, rendered the canonical way round.
			"flag nil default+required",
			func() { StringFlag("xx", "hh", Default(nil), Required()) },
			`Flag "xx": presence is declared twice: Required() and Default(nil) cannot be combined; declare exactly one`,
		},
		{
			"arg required+nil default",
			func() { NewArg("xx", "hh", ArgRequired(), ArgDefault(nil)) },
			`Arg "xx": presence is declared twice: ArgRequired() and ArgDefault(nil) cannot be combined; declare exactly one`,
		},
		{
			"arg optional+nil default",
			func() { NewArg("xx", "hh", ArgOptional(), ArgDefault(nil)) },
			`Arg "xx": presence is declared twice: ArgOptional() and ArgDefault(nil) cannot be combined; declare exactly one`,
		},
		{
			"arg nil default+optional",
			func() { NewArg("xx", "hh", ArgDefault(nil), ArgOptional()) },
			`Arg "xx": presence is declared twice: ArgOptional() and ArgDefault(nil) cannot be combined; declare exactly one`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustPanic(t, tc.fn); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The null-valued default: refused, and redirected to the one spelling
// ---------------------------------------------------------------------------

func TestFlagDefaultNilRedirectsToOptional(t *testing.T) {
	want := `Flag "format": Default(nil) does not declare optionality: use Optional() (it delivers nil when the flag is absent)`
	if got := mustPanic(t, func() { StringFlag("format", "output format", Default(nil)) }); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestArgDefaultNilRedirectsToArgOptional(t *testing.T) {
	want := `Arg "env": ArgDefault(nil) does not declare optionality: use ArgOptional() (it delivers nil when the arg is absent)`
	if got := mustPanic(t, func() { NewArg("env", "target env", ArgDefault(nil)) }); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// The mutex-member presence rule INVERTED (§21's supersession box, §12.13)
//
// errFlagMutexMemberRequired is deleted with MutexGroup. A member flag now MUST
// declare Required(), read as "required once this member is elected", and the
// tests for the inverted rule live in member_election_test.go. A scoped
// sub-flag may be optional, required or defaulted exactly as any flag is,
// resolved within its scope when that scope is elected (§23.5's superseded
// mutex row).
// ---------------------------------------------------------------------------

func TestScopedSubFlagTakesAllThreePresences(t *testing.T) {
	app := simpleApp("cmd", "a command", "via={via}",
		WithFlags(ChoiceFlag("via", "delivery channel", Required(),
			Choice("email", "as an email",
				StringFlag("subject", "subject line", Required()),
				StringFlag("cc", "carbon copy", Optional()),
				IntFlag("retries", "how many retries", Default(3)),
			),
			Choice("sms", "as a text", StringFlag("phone-number", "the number", Required())),
		)))
	r := app.Test([]string{"cmd", "--via", "email", "--subject", "hi"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "via=email[cc:<nil> retries:3 subject:hi]\n" {
		t.Fatalf("got %q", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// Delivery: optional means absence, and absence is delivered as absence
// ---------------------------------------------------------------------------

func TestOptionalScalarsDeliverNil(t *testing.T) {
	app := simpleApp("cmd", "a command", "s={ss} i={ii} f={ff} b={bb}",
		WithFlags(
			StringFlag("ss", "a string", Optional()),
			IntFlag("ii", "an int", Optional()),
			FloatFlag("ff", "a float", Optional()),
			BoolFlag("bb", "a bool", Optional()),
		))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "s=None i=None f=None b=None\n" {
		t.Fatalf("got %q", r.Stdout)
	}
}

// TestOptionalBoolIsRealTristate pins what retires the string-pseudo-bool
// idiom: --xx is true, --no-xx is false, absent is absent.
func TestOptionalBoolIsRealTristate(t *testing.T) {
	app := simpleApp("cmd", "a command", "b={bb}",
		WithFlags(BoolFlag("bb", "a bool", Optional())))
	for _, tc := range []struct{ argv, want string }{
		{"--bb", "b=true"},
		{"--no-bb", "b=false"},
		{"", "b=None"},
	} {
		argv := []string{"cmd"}
		if tc.argv != "" {
			argv = append(argv, tc.argv)
		}
		r := app.Test(argv)
		if r.ExitCode != 0 {
			t.Fatalf("%s: expected exit 0, got %d; stderr=%q", tc.argv, r.ExitCode, r.Stderr)
		}
		if r.Stdout != tc.want+"\n" {
			t.Fatalf("%s: got %q, want %q", tc.argv, r.Stdout, tc.want)
		}
	}
}

func TestRequiredBoolMustBePassed(t *testing.T) {
	app := simpleApp("cmd", "a command", "b={bb}",
		WithFlags(BoolFlag("bb", "a bool", Required())))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "flag '--bb' must be passed as --bb or --no-bb") {
		t.Fatalf("got %q", r.Stderr)
	}
}

// ---------------------------------------------------------------------------
// Compound flags declare presence honestly (§23.5)
// ---------------------------------------------------------------------------

func TestCompoundOptionalDeliversAbsenceNotEmpty(t *testing.T) {
	var got map[string]interface{}
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		got = kwargs
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeStr, "tag", "a tag", Unique(false), Optional()),
		DictFlag(TypeStr, "label", "a label", Unique(false), Optional()),
		StringFlag("note", "a note", Repeatable(), Unique(false), Optional()),
	), WithEffect(EffectReadOnly))
	if r := app.Test([]string{"cmd"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	for _, name := range []string{"tag", "label", "note"} {
		v, ok := got[name]
		if !ok {
			t.Fatalf("%s: expected a present key", name)
		}
		if v != nil {
			t.Fatalf("%s: expected absence, got %#v -- the silent empty collection is gone", name, v)
		}
	}
}

func TestCompoundExplicitEmptyDefaults(t *testing.T) {
	var got map[string]interface{}
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		got = kwargs
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeStr, "tag", "a tag", Unique(false), Default([]interface{}{})),
		DictFlag(TypeStr, "label", "a label", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))
	if r := app.Test([]string{"cmd"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if lst, ok := got["tag"].([]interface{}); !ok || len(lst) != 0 {
		t.Fatalf("expected the declared empty list, got %#v", got["tag"])
	}
	if m, ok := got["label"].(map[string]interface{}); !ok || len(m) != 0 {
		t.Fatalf("expected the declared empty dict, got %#v", got["label"])
	}
}

func TestCompoundRequiredNeedsAtLeastOneValue(t *testing.T) {
	app := simpleApp("cmd", "a command", "tags={tag} labels={label}",
		WithFlags(
			ListFlag(TypeStr, "tag", "a tag", Unique(false), Required()),
			DictFlag(TypeStr, "label", "a label", Unique(false), Optional()),
		))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "flag '--tag' is required") {
		t.Fatalf("got %q", r.Stderr)
	}
	if r := app.Test([]string{"cmd", "--tag", "aa"}); r.ExitCode != 0 {
		t.Fatalf("one occurrence satisfies requiredness: exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
}

// ---------------------------------------------------------------------------
// Positional args (§23.3)
// ---------------------------------------------------------------------------

func TestOptionalArgDeliversPresentKeyHoldingNil(t *testing.T) {
	var got map[string]interface{}
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		got = kwargs
		return Exit(0)
	}, WithArgs(NewArg("path", "the path", ArgOptional())), WithEffect(EffectReadOnly))
	if r := app.Test([]string{"cmd"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	v, ok := got["path"]
	if !ok {
		t.Fatal("an optional arg delivers a PRESENT key, never key-absence")
	}
	if v != nil {
		t.Fatalf("expected nil, got %#v", v)
	}
}

func TestVariadicArgPresence(t *testing.T) {
	app := simpleApp("cmd", "a command", "items={items}",
		WithArgs(NewArg("items", "the items", Variadic(), ArgRequired())))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("a required variadic needs at least one value: exit %d", r.ExitCode)
	}

	optional := simpleApp("cmd", "a command", "items={items}",
		WithArgs(NewArg("items", "the items", Variadic(), ArgOptional())))
	r = optional.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "items=\n" {
		t.Fatalf("an optional variadic delivers the empty list, got %q", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// Requiredness is satisfied by ANY source that provides a value (§23.5)
// ---------------------------------------------------------------------------

func TestRequiredSatisfiedByEnv(t *testing.T) {
	os.Setenv("MYAPP_LEVEL", "42")
	defer os.Unsetenv("MYAPP_LEVEL")
	app := simpleApp("cmd", "a command", "level={level}",
		WithFlags(IntFlag("level", "verbosity", Env("MYAPP_LEVEL"), Required())))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "level=42\n" {
		t.Fatalf("got %q", r.Stdout)
	}
}

func TestRequiredSatisfiedByConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := tmpDir + "/config.json"
	if err := os.WriteFile(configFile, []byte(`{"level": 7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	app := NewApp("myapp", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		if kwargs["level"] != 7 {
			t.Fatalf("expected 7, got %#v", kwargs["level"])
		}
		return Exit(0)
	}, WithFlags(IntFlag("level", "verbosity", Required())), WithEffect(EffectReadOnly))
	if r := app.Test([]string{"cmd"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestRequiredSatisfiedByImplication(t *testing.T) {
	app := simpleApp("cmd", "a command", "loud={loud} verbose_out={verbose_out}",
		WithFlags(
			BoolFlag("loud", "loud mode", Default(false)),
			BoolFlag("verbose-out", "verbose output", Required()),
		),
		WithConstraints(Implies("loud-is-verbose", "loud", "verbose-out", true)))
	// With the trigger, the injection satisfies requiredness.
	r := app.Test([]string{"cmd", "--loud"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "loud=true verbose_out=true\n" {
		t.Fatalf("got %q", r.Stdout)
	}
	// Without it, the required error fires normally.
	r = app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "flag '--verbose-out' must be passed as --verbose-out or --no-verbose-out") {
		t.Fatalf("got %q", r.Stderr)
	}
}

// §23.5's amended all-or-none row (§26.5, §18.30 item 274): a member that
// declares requiredness is a REGISTRATION ERROR. The shipped contract called
// the shape "legal, and stated because it is a surprising shape to write by
// accident", and the surprise was the whole objection: it silently means "every
// other member is required too", which already has a spelling.
func TestAllOrNoneWithARequiredMemberIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "identity" member '--cert' declares Required(): a member the invocation must always supply leaves the constraint nothing to decide`, func() {
		simpleApp("cmd", "a command", "cert={cert} key={key}",
			WithFlags(
				StringFlag("cert", "the certificate", Required()),
				StringFlag("key", "the private key", Optional()),
			),
			WithConstraints(AllOrNone("identity", Member("cert"), Member("key"))))
	})
}

// The at-least-one half of the same cell: a required member makes the
// constraint satisfied in every invocation, so it could never fire at all.
func TestAtLeastOneWithARequiredMemberIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "selection" member '--cert' declares Required(): a member the invocation must always supply leaves the constraint nothing to decide`, func() {
		simpleApp("cmd", "a command", "cert={cert} key={key}",
			WithFlags(
				StringFlag("cert", "the certificate", Required()),
				StringFlag("key", "the private key", Optional()),
			),
			WithConstraints(AtLeastOne("selection", Member("cert"), Member("key"))))
	})
}

// A DEFAULTED member stays legal, and its default never engages the constraint
// on its own: a group whose members are all defaulted stays vacuous until
// something supplies one (§26.5's `default` cells).
func TestAllOrNoneWithADefaultedMemberStaysVacuous(t *testing.T) {
	newApp := func() *App {
		return simpleApp("cmd", "a command", "cert={cert} key={key}",
			WithFlags(
				StringFlag("cert", "the certificate", Default("c.pem")),
				StringFlag("key", "the private key", Default("k.pem")),
			),
			WithConstraints(AllOrNone("identity", Member("cert"), Member("key"))))
	}
	r := newApp().Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "cert=c.pem key=k.pem\n" {
		t.Fatalf("got %q", r.Stdout)
	}
	r = newApp().Test([]string{"cmd", "--cert", "mine.pem"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: constraint \"identity\": --cert, --key must be used together\ntry 'myapp cmd --help'\n"
	if r.Stderr != want {
		t.Fatalf("stderr = %q, want %q", r.Stderr, want)
	}
}

// An Implies TRIGGER never fires from its own default (§23.5's Implies-trigger
// row): the trigger declares Default(true) and nothing supplies it, so the
// implied target keeps its own declaration rather than the implied value.
func TestImpliesTriggerNeverFiresFromItsOwnDefault(t *testing.T) {
	newApp := func() *App {
		return simpleApp("cmd", "a command", "release={release} signed={signed}",
			WithFlags(
				BoolFlag("release", "release build", Default(true)),
				BoolFlag("signed", "signed build", Optional()),
			),
			WithConstraints(Implies("release-is-signed", "release", "signed", true)))
	}
	// Trigger defaulted (not supplied): no implication.
	r := newApp().Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "release=true signed=None\n" {
		t.Fatalf("a defaulted trigger must not fire; got %q", r.Stdout)
	}
	// Supplying the very same value on the command line DOES fire it: the
	// difference is provision, not the value.
	r = newApp().Test([]string{"cmd", "--release"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "release=true signed=true\n" {
		t.Fatalf("a provided trigger must fire; got %q", r.Stdout)
	}
}

func TestRequiredURLClassFlagSatisfiedByConnectionEnv(t *testing.T) {
	// A URL-class flag binds to a declared connection env, and that value
	// satisfies requiredness like any other env value (§23.5's URL row).
	os.Setenv("DATABASE_URL", "postgres://from-env/db")
	defer os.Unsetenv("DATABASE_URL")
	app := NewApp("myapp", "1.0.0", "test app",
		WithConnectionEnv("DATABASE_URL", "Postgres connection string"))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		if kwargs["dsn"] != "postgres://from-env/db" {
			t.Fatalf("dsn = %#v", kwargs["dsn"])
		}
		return Exit(0)
	}, WithFlags(StringFlag("dsn", "connection string", Required(), ConnectionURLFlag("DATABASE_URL"))),
		WithEffect(EffectReadOnly))
	if r := app.Test([]string{"run"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
}

// ---------------------------------------------------------------------------
// ctx.Provided (§23.6)
// ---------------------------------------------------------------------------

func TestProvidedAcrossSources(t *testing.T) {
	os.Setenv("MYAPP_FROM_ENV", "ee")
	defer os.Unsetenv("MYAPP_FROM_ENV")
	tmpDir := t.TempDir()
	configFile := tmpDir + "/config.json"
	if err := os.WriteFile(configFile, []byte(`{"from_config": "cc"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	app := NewApp("myapp", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	var ctxCaptured *Context
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctxCaptured = ctx
		return Exit(0)
	}, WithFlags(
		StringFlag("from-cli", "cli", Optional()),
		StringFlag("from-env", "env", Env("MYAPP_FROM_ENV"), Optional()),
		StringFlag("from-config", "config", Optional()),
		StringFlag("defaulted", "defaulted", Default("dd")),
		StringFlag("absent", "absent", Optional()),
		BoolFlag("trigger", "trigger", Default(false)),
		BoolFlag("implied-target", "implied", Optional()),
	), WithConstraints(Implies("trigger-implies-target", "trigger", "implied-target", true)),
		WithEffect(EffectReadOnly))

	if r := app.Test([]string{"cmd", "--from-cli", "vv", "--trigger"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	cases := []struct {
		name       string
		wantSource string
		want       bool
	}{
		{"from-cli", "cli", true},
		{"from-env", "env", true},
		{"from-config", "config", true},
		{"implied-target", "implied", true},
		{"defaulted", "default", false},
		{"absent", "default", false},
	}
	for _, tc := range cases {
		if got := ctxCaptured.Source(tc.name); got != tc.wantSource {
			t.Fatalf("%s: source %q, want %q", tc.name, got, tc.wantSource)
		}
		if got := ctxCaptured.Provided(tc.name); got != tc.want {
			t.Fatalf("%s: Provided %v, want %v", tc.name, got, tc.want)
		}
		// Dashed and underscored names both resolve, exactly as Source does.
		if got := ctxCaptured.Provided(strings.ReplaceAll(tc.name, "-", "_")); got != tc.want {
			t.Fatalf("%s (underscored): Provided %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestProvidedIsFalseForInfraDefault(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app", WithInfraRoot("MYAPP_ROOT", "/tmp/myapp-root"))
	var ctxCaptured *Context
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctxCaptured = ctx
		return Exit(0)
	}, WithFlags(
		StringFlag("state", "state path", Default(RelativeToRoot("MYAPP_ROOT", "state"))),
	), WithEffect(EffectReadOnly))
	if r := app.Test([]string{"cmd"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if got := ctxCaptured.Source("state"); got != "infra" {
		t.Fatalf("source %q, want infra", got)
	}
	if ctxCaptured.Provided("state") {
		t.Fatal("a RelativeToRoot default is still a declared default: not provided")
	}
}

func TestProvidedUnknownNamePanicsLikeSource(t *testing.T) {
	var ctxCaptured *Context
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctxCaptured = ctx
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	if r := app.Test([]string{"cmd"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	want := `no source info for flag "nope"`
	if got := mustPanic(t, func() { ctxCaptured.Provided("nope") }); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := mustPanic(t, func() { ctxCaptured.Source("nope") }); got != want {
		t.Fatalf("Source: got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Help rendering: exactly one presence part, last on the line (§23.8)
// ---------------------------------------------------------------------------

func TestHelpRendersOnePresencePartPerFlag(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			StringFlag("req", "required flag", Required()),
			StringFlag("opt", "optional flag", Optional()),
			StringFlag("dfl", "defaulted flag", Default("xx")),
			BoolFlag("bdfl", "defaulted bool", Default(true)),
			ListFlag(TypeStr, "empty-list", "an empty list", Unique(false), Default([]interface{}{})),
			DictFlag(TypeStr, "empty-dict", "an empty dict", Unique(false), Default(map[string]interface{}{})),
			DictFlag(TypeStr, "full-dict", "a full dict", Unique(false), Default(map[string]interface{}{"bb": "2", "aa": "1"})),
		))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	for _, want := range []string{
		"required flag [required]",
		"optional flag [optional]",
		"defaulted flag [default: xx]",
		"defaulted bool [default: true]",
		"an empty list [list] [default: []]",
		"an empty dict [dict] [default: {}]",
		"a full dict [dict] [default: aa=1, bb=2]",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Fatalf("help missing %q; got:\n%s", want, r.Stdout)
		}
	}
	// Exactly one presence part per flag line.
	for _, line := range strings.Split(r.Stdout, "\n") {
		if !strings.HasPrefix(line, "  --") {
			continue
		}
		n := strings.Count(line, "[required]") + strings.Count(line, "[optional]") + strings.Count(line, "[default: ")
		if n != 1 {
			t.Fatalf("expected exactly one presence part, got %d in %q", n, line)
		}
	}
}

func TestHelpRendersArgPresence(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithArgs(
			NewArg("src", "the source", ArgRequired()),
			NewArg("dst", "the destination", ArgOptional()),
			NewArg("mode", "the mode", ArgDefault("fast")),
			// A bool default renders lowercase on the arg surface too.
			NewArg("force", "force it", ArgType(TypeBool), ArgDefault(false)),
			NewArg("deep", "go deep", ArgType(TypeBool), ArgDefault(true)),
		))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", r.ExitCode)
	}
	for _, want := range []string{
		"the source [required]",
		"the destination [optional]",
		"the mode [default: fast]",
		"force it [type: bool] [default: false]",
		"go deep [type: bool] [default: true]",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Fatalf("help missing %q; got:\n%s", want, r.Stdout)
		}
	}
}

// ---------------------------------------------------------------------------
// The dumped schema (§13's presence-round amendment)
// ---------------------------------------------------------------------------

func TestSchemaEmitsPresenceOnEveryFlagAndArg(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app")
	app.GlobalFlag(StringFlag("global-opt", "a global", Optional()))
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		StringFlag("req", "required", Required()),
		StringFlag("opt", "optional", Optional()),
		StringFlag("empty-str", "empty string default", Default("")),
		BoolFlag("false-bool", "false default", Default(false)),
		IntFlag("zero-int", "zero default", Default(0)),
		ListFlag(TypeStr, "empty-list", "empty list default", Unique(false), Default([]interface{}{})),
		DictFlag(TypeStr, "empty-dict", "empty dict default", Unique(false), Default(map[string]interface{}{})),
	), WithArgs(
		NewArg("src", "the source", ArgRequired()),
		NewArg("dst", "the destination", ArgOptional()),
		NewArg("mode", "the mode", ArgDefault("fast")),
	), WithEffect(EffectReadOnly))

	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	cmd := schema["commands"].(map[string]interface{})["cmd"].(map[string]interface{})

	flags := map[string]map[string]interface{}{}
	for _, raw := range cmd["flags"].([]interface{}) {
		f := raw.(map[string]interface{})
		flags[f["name"].(string)] = f
	}
	// presence is emitted on EVERY entry, and default exactly when presence is
	// "default" -- including for "", false, 0, [] and {}.
	wantPresence := map[string]string{
		"req": "required", "opt": "optional", "empty-str": "default",
		"false-bool": "default", "zero-int": "default",
		"empty-list": "default", "empty-dict": "default",
	}
	for name, want := range wantPresence {
		got, ok := flags[name]
		if !ok {
			t.Fatalf("flag %q missing from schema", name)
		}
		if got["presence"] != want {
			t.Fatalf("flag %q: presence %v, want %q", name, got["presence"], want)
		}
		_, hasDefault := got["default"]
		if hasDefault != (want == "default") {
			t.Fatalf("flag %q: default key present=%v, presence=%q", name, hasDefault, want)
		}
	}
	if flags["empty-str"]["default"] != "" {
		t.Fatalf(`expected "" default, got %#v`, flags["empty-str"]["default"])
	}
	if flags["false-bool"]["default"] != false {
		t.Fatalf("expected false default, got %#v", flags["false-bool"]["default"])
	}
	if flags["zero-int"]["default"] != 0 {
		t.Fatalf("expected 0 default, got %#v", flags["zero-int"]["default"])
	}
	if lst, ok := flags["empty-list"]["default"].([]interface{}); !ok || len(lst) != 0 {
		t.Fatalf("expected [] default, got %#v", flags["empty-list"]["default"])
	}
	if m, ok := flags["empty-dict"]["default"].(map[string]interface{}); !ok || len(m) != 0 {
		t.Fatalf("expected {} default, got %#v", flags["empty-dict"]["default"])
	}

	args := map[string]map[string]interface{}{}
	for _, raw := range cmd["args"].([]interface{}) {
		a := raw.(map[string]interface{})
		args[a["name"].(string)] = a
	}
	for name, want := range map[string]string{"src": "required", "dst": "optional", "mode": "default"} {
		if args[name]["presence"] != want {
			t.Fatalf("arg %q: presence %v, want %q", name, args[name]["presence"], want)
		}
		if _, ok := args[name]["required"]; ok {
			t.Fatalf("arg %q: the 'required' key is deleted", name)
		}
	}

	for _, raw := range schema["global_flags"].([]interface{}) {
		f := raw.(map[string]interface{})
		if f["name"] == "global-opt" && f["presence"] != "optional" {
			t.Fatalf("global flag presence %v, want optional", f["presence"])
		}
	}
}

func TestSchemaInfraMarkerDefaultReportsPresenceDefault(t *testing.T) {
	chdirTemp(t)
	app := NewApp("myapp", "1.0.0", "test app", WithInfraRoot("MYAPP_ROOT", "/tmp/myapp-root"))
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("state", "state path", Default(RelativeToRoot("MYAPP_ROOT", "state")))),
		WithEffect(EffectReadOnly))
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	cmd := schema["commands"].(map[string]interface{})["cmd"].(map[string]interface{})
	f := cmd["flags"].([]interface{})[0].(map[string]interface{})
	if f["presence"] != "default" {
		t.Fatalf("presence %v, want default", f["presence"])
	}
	marker, ok := f["default"].(map[string]interface{})
	if !ok || marker["relative_to_root"] == nil {
		t.Fatalf("expected the machine-stable marker shape, got %#v", f["default"])
	}
}

// ---------------------------------------------------------------------------
// The MCP projection collapses onto the declared field (§23.7)
// ---------------------------------------------------------------------------

func TestToolSchemaRequirednessFollowsDeclaredPresence(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		StringFlag("req-str", "required str", Required()),
		BoolFlag("req-bool", "required bool", Required()),
		ListFlag(TypeStr, "req-list", "required list", Unique(false), Required()),
		DictFlag(TypeStr, "req-dict", "required dict", Unique(false), Required()),
		StringFlag("opt-str", "optional str", Optional()),
		BoolFlag("dfl-bool", "defaulted bool", Default(false)),
	), WithArgs(
		NewArg("src", "the source", ArgRequired()),
		NewArg("dst", "the destination", ArgOptional()),
	), WithEffect(EffectReadOnly))

	schema := app.JsonSchema("cmd")
	required := map[string]bool{}
	for _, r := range schema["required"].([]interface{}) {
		required[r.(string)] = true
	}
	// Required BOOLS are in the array now: "bool flags always have a default"
	// is false by construction under the presence declaration.
	for _, name := range []string{"req_str", "req_bool", "req_list", "req_dict", "src"} {
		if !required[name] {
			t.Fatalf("%s should be required, got %v", name, schema["required"])
		}
	}
	for _, name := range []string{"opt_str", "dfl_bool", "dst"} {
		if required[name] {
			t.Fatalf("%s should NOT be required, got %v", name, schema["required"])
		}
	}
}
