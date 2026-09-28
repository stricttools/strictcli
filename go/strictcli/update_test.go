package strictcli

import (
	"encoding/json"
	"strings"
	"testing"
)

// The update-command construct (contract §27, §12.16).
//
// The fixture is the contract's own worked example -- a sparse DNS-record
// update with two identity members and three properties, one of them nullable
// and one of them a bool -- so every rendering below is checked against the
// declaration the contract renders in its own text.

func updateFixture(t *testing.T, handler func(ctx *Context, args map[string]interface{}) Outcome, opts ...CmdOption) *App {
	t.Helper()
	app := NewApp("dnsapp", "1.0.0", "manage DNS")
	if handler == nil {
		handler = noop
	}
	all := append([]CmdOption{
		WithEffect(EffectMutating),
		WithUpdateOf("dns-record", WriteSparse,
			Identity("zone", "record-id"),
			Properties("content", "ttl", "proxied"),
		),
		WithFlags(
			StringFlag("zone", "zone the record belongs to", Required()),
			StringFlag("record-id", "identifier of the record to change", Required()),
			StringFlag("content", "record content", Optional()),
			IntFlag("ttl", "time to live in seconds", Optional(), Nullable()),
			BoolFlag("proxied", "whether the record is proxied", Optional()),
		),
	}, opts...)
	app.Command("update-record", "change one DNS record in place", handler, all...)
	return app
}

// --- §27.1: the mutating-default ban ---

func TestMutatingDefaultIsRefusedOnAFlag(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	expectPanic(t, `command "uu": flag '--ttl' declares Default(300) on a mutating command: absence would write a value the invocation never stated (declare Required() or Optional(), or apply the fallback in the handler and say so in its help)`, func() {
		app.Command("uu", "update", noop, WithEffect(EffectMutating),
			WithFlags(IntFlag("ttl", "time to live", Default(300))))
	})
}

func TestMutatingDefaultIsRefusedOnAPositionalArg(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	// The presence spelling inside the sentence takes the FLAG spelling even
	// when the subject is an arg (§12.16): the prefix names the command rather
	// than a surface.
	expectPanic(t, `command "uu": argument 'target' declares Default(prod) on a mutating command`, func() {
		app.Command("uu", "update", noop, WithEffect(EffectMutating),
			WithArgs(NewArg("target", "where", ArgDefault("prod"))))
	})
}

func TestMutatingDefaultIsRefusedOnEveryScalarIncludingTheEmptyOnes(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag Flag
		want string
	}{
		{"empty string", StringFlag("content", "content", Default("")), `flag '--content' declares Default() on a mutating command`},
		{"zero", IntFlag("ttl", "ttl", Default(0)), `flag '--ttl' declares Default(0) on a mutating command`},
		{"false", BoolFlag("proxied", "proxied", Default(false)), `flag '--proxied' declares Default(false) on a mutating command`},
		{"true", BoolFlag("proxied", "proxied", Default(true)), `flag '--proxied' declares Default(true) on a mutating command`},
		{"float", FloatFlag("rate", "rate", Default(1.5)), `flag '--rate' declares Default(1.5) on a mutating command`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := NewApp("tt", "1.0.0", "tt")
			expectPanic(t, tc.want, func() {
				app.Command("uu", "update", noop, WithEffect(EffectMutating), WithFlags(tc.flag))
			})
		})
	}
}

func TestMutatingDefaultIsRefusedOnANonEmptyCompound(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	expectPanic(t, `command "uu": flag '--tag' declares Default([aa]) on a mutating command`, func() {
		app.Command("uu", "update", noop, WithEffect(EffectMutating),
			WithFlags(StringFlag("tag", "tags", Repeatable(), Unique(false), Default([]interface{}{"aa"}))))
	})
}

// The two carve-outs and the two exemptions, each of which must REGISTER.
func TestMutatingDefaultCarveOutsAndExemptions(t *testing.T) {
	t.Run("an empty collection declares no elements", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		app.Command("uu", "update", noop, WithEffect(EffectMutating), WithFlags(
			StringFlag("tag", "tags", Repeatable(), Unique(false), Default([]interface{}{})),
			DictFlag(TypeStr, "header", "headers", Unique(false), Default(map[string]interface{}{})),
		))
	})
	t.Run("a RelativeToRoot default decides where, never what", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt", WithInfraRoot("T_HOME", "~/.t"))
		app.Command("uu", "update", noop, WithEffect(EffectMutating),
			WithFlags(StringFlag("path", "a path", Default(RelativeToRoot("T_HOME", "store")))))
	})
	t.Run("a read_only command writes no value, invented or otherwise", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		app.Command("rr", "read", noop, WithEffect(EffectReadOnly),
			WithFlags(IntFlag("ttl", "ttl", Default(300))))
	})
	t.Run("an app-level global is not reached", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		app.GlobalFlag(IntFlag("depth", "depth", Default(3)))
		app.Command("uu", "update", noop, WithEffect(EffectMutating))
	})
}

func TestMutatingDefaultReachesAFlagSetsFlag(t *testing.T) {
	// A shared flag set carrying a default is legal; ATTACHING it to a mutating
	// command is not -- the ban is evaluated per command, over the flags that
	// command carries (§27.1, §18.33 item 302).
	set := FlagSet{Name: "shared", Flags: []Flag{IntFlag("ttl", "time to live", Default(300))}}
	readOnly := NewApp("tt", "1.0.0", "tt")
	readOnly.Command("rr", "read", noop, WithEffect(EffectReadOnly), WithFlagSets(set))

	app := NewApp("tt", "1.0.0", "tt")
	expectPanic(t, `command "uu": flag '--ttl' declares Default(300) on a mutating command`, func() {
		app.Command("uu", "update", noop, WithEffect(EffectMutating), WithFlagSets(set))
	})
}

func TestMutatingDefaultSparesTheSelectorAndReachesItsScope(t *testing.T) {
	// A choice name is not a value written to anything: it names which scope is
	// live. The flags INSIDE the scope are ordinary flags of a mutating
	// command, reached at every depth.
	app := NewApp("tt", "1.0.0", "tt")
	expectPanic(t, `command "uu": flag '--retries' declares Default(3) on a mutating command`, func() {
		app.Command("uu", "update", noop, WithEffect(EffectMutating),
			WithFlags(ChoiceFlag("via", "channel", Default("webhook"),
				Choice("webhook", "post to a URL", IntFlag("retries", "attempts", Default(3))),
				Choice("email", "send an email", StringFlag("subject", "subject", Optional())),
			)))
	})

	ok := NewApp("tt", "1.0.0", "tt")
	ok.Command("uu", "update", noop, WithEffect(EffectMutating),
		WithFlags(ChoiceFlag("via", "channel", Default("webhook"),
			Choice("webhook", "post to a URL", IntFlag("retries", "attempts", Optional())),
			Choice("email", "send an email", StringFlag("subject", "subject", Optional())),
		)))
}

// --- §27.2, §27.3, §27.11: the registration guards, in the pinned order ---

func TestUpdateOnReadOnlyIsRefused(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	expectPanic(t, `command "uu": a read_only command cannot declare update_of (a command that changes nothing writes no properties)`, func() {
		app.Command("uu", "update", noop, WithEffect(EffectReadOnly),
			WithUpdateOf("thing", WriteSparse, Properties("content")),
			WithFlags(StringFlag("content", "content", Optional())))
	})
}

func TestUpdateWriteModeVocabulary(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	// Go's reachable input is the ZERO VALUE of the string-based WriteMode,
	// which renders "".
	expectPanic(t, `command "uu": invalid write_mode "": must be "sparse" or "full_replace"`, func() {
		var unset WriteMode
		app.Command("uu", "update", noop, WithEffect(EffectMutating),
			WithUpdateOf("thing", unset, Properties("content")),
			WithFlags(StringFlag("content", "content", Optional())))
	})
}

func TestUpdateResourceCharset(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	expectPanic(t, `command "uu": update resource "DNS_Record" must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*`, func() {
		app.Command("uu", "update", noop, WithEffect(EffectMutating),
			WithUpdateOf("DNS_Record", WriteSparse, Properties("content")),
			WithFlags(StringFlag("content", "content", Optional())))
	})
}

func TestUpdateWithNoPropertiesIsRefused(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	expectPanic(t, `command "uu": update of "thing" declares no properties: an update with nothing to write is not an update`, func() {
		app.Command("uu", "update", noop, WithEffect(EffectMutating),
			WithUpdateOf("thing", WriteSparse, Identity("id")),
			WithFlags(StringFlag("id", "id", Required())))
	})
}

func TestUpdateNameResolutionRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []CmdOption
		want string
	}{
		{
			"unknown",
			[]CmdOption{
				WithUpdateOf("thing", WriteSparse, Properties("nope")),
				WithFlags(StringFlag("content", "content", Optional())),
			},
			`command "uu": update of "thing" references unknown name "nope"`,
		},
		{
			"ambiguous",
			[]CmdOption{
				WithUpdateOf("thing", WriteSparse, Identity("target"), Properties("content")),
				WithFlags(StringFlag("target", "a flag", Optional()), StringFlag("content", "content", Optional())),
				WithArgs(NewArg("target", "an arg", ArgOptional())),
			},
			`command "uu": update of "thing" references "target", which names both a flag and a positional arg`,
		},
		{
			"duplicated",
			[]CmdOption{
				WithUpdateOf("thing", WriteSparse, Properties("content", "content")),
				WithFlags(StringFlag("content", "content", Optional())),
			},
			`command "uu": update of "thing" declares "content" twice`,
		},
		{
			"both roles",
			[]CmdOption{
				WithUpdateOf("thing", WriteSparse, Identity("content"), Properties("content")),
				WithFlags(StringFlag("content", "content", Optional())),
			},
			`command "uu": update of "thing" declares "content" as both identity and property`,
		},
		{
			"scoped",
			[]CmdOption{
				WithUpdateOf("thing", WriteSparse, Properties("subject")),
				WithFlags(ChoiceFlag("via", "channel", Required(),
					Choice("email", "by email", StringFlag("subject", "subject", Optional())),
					Choice("sms", "by sms", StringFlag("number", "number", Optional())),
				)),
			},
			`command "uu": update of "thing" references 'subject', which is declared under '--via email': an update's identity and properties are declared at root scope only`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := NewApp("tt", "1.0.0", "tt")
			expectPanic(t, tc.want, func() {
				app.Command("uu", "update", noop, append([]CmdOption{WithEffect(EffectMutating)}, tc.opts...)...)
			})
		})
	}
}

func TestUpdatePropertyRoleAndPresenceRefusals(t *testing.T) {
	t.Run("a property may not be a positional arg", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		expectPanic(t, `command "uu": update of "thing" property "content" is a positional arg: a property must be individually omissible and clearable, and only a flag is`, func() {
			app.Command("uu", "update", noop, WithEffect(EffectMutating),
				WithUpdateOf("thing", WriteSparse, Properties("content")),
				WithArgs(NewArg("content", "content", ArgOptional())))
		})
	})
	t.Run("a property may not be a choice flag", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		expectPanic(t, `command "uu": update of "thing" property '--via' is a choice flag: an elected record is a selection, not a property value`, func() {
			app.Command("uu", "update", noop, WithEffect(EffectMutating),
				WithUpdateOf("thing", WriteSparse, Properties("via")),
				WithFlags(ChoiceFlag("via", "channel", Required(),
					Choice("email", "by email"), Choice("sms", "by sms"))))
		})
	})
	t.Run("a property declares optional and nothing else", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		expectPanic(t, `command "uu": update of "thing" property flag '--content' declares Required(): a property is absent exactly when it is not being written, and the presence declaration for that is Optional()`, func() {
			app.Command("uu", "update", noop, WithEffect(EffectMutating),
				WithUpdateOf("thing", WriteSparse, Properties("content")),
				WithFlags(StringFlag("content", "content", Required())))
		})
	})
}

func TestIdentityMayBeAnArgOrAChoiceFlagAndMayBeOptional(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	app.Command("uu", "update", noop, WithEffect(EffectMutating),
		WithUpdateOf("thing", WriteSparse,
			Identity("record-id", "addressing", "name"),
			Properties("content")),
		WithFlags(
			ChoiceFlag("addressing", "how the resource is addressed", Required(),
				Choice("by-id", "address it by id"),
				Choice("by-name", "address it by name"),
			),
			StringFlag("name", "the name", Optional()),
			StringFlag("content", "content", Optional()),
		),
		WithArgs(NewArg("record-id", "the id", ArgRequired())),
	)
}

func TestNullableIsRefusedOffAProperty(t *testing.T) {
	t.Run("on a command with no update at all", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		expectPanic(t, `command "uu": flag '--content' declares Nullable() but is not a property of an update: only a property can be cleared`, func() {
			app.Command("uu", "update", noop, WithEffect(EffectMutating),
				WithFlags(StringFlag("content", "content", Optional(), Nullable())))
		})
	})
	t.Run("on an identity member of an update", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		expectPanic(t, `command "uu": flag '--zone' declares Nullable() but is not a property of an update`, func() {
			app.Command("uu", "update", noop, WithEffect(EffectMutating),
				WithUpdateOf("thing", WriteSparse, Identity("zone"), Properties("content")),
				WithFlags(
					StringFlag("zone", "zone", Optional(), Nullable()),
					StringFlag("content", "content", Optional()),
				))
		})
	})
}

func TestUnsetNameIsReserved(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	expectPanic(t, `command "uu": flag name "unset-content" is reserved: property '--content' declares Nullable(), which mints '--unset-content'`, func() {
		app.Command("uu", "update", noop, WithEffect(EffectMutating),
			WithUpdateOf("thing", WriteSparse, Properties("content")),
			WithFlags(
				StringFlag("content", "content", Optional(), Nullable()),
				StringFlag("unset-content", "a flag of that name", Optional()),
			))
	})
}

// The pinned order matters only where one declaration carries two faults.
func TestUpdateRegistrationOrder(t *testing.T) {
	t.Run("the ban runs ahead of every update step", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		expectPanic(t, `flag '--ttl' declares Default(300) on a mutating command`, func() {
			app.Command("uu", "update", noop, WithEffect(EffectMutating),
				WithUpdateOf("BAD-NAME", WriteSparse, Properties("nope")),
				WithFlags(IntFlag("ttl", "ttl", Default(300))))
		})
	})
	t.Run("classification runs ahead of record legality", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		expectPanic(t, `a read_only command cannot declare update_of`, func() {
			app.Command("uu", "update", noop, WithEffect(EffectReadOnly),
				WithUpdateOf("BAD-NAME", WriteSparse, Properties("nope")))
		})
	})
	t.Run("the record's own legality runs ahead of the names it carries", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		expectPanic(t, `update resource "BAD-NAME" must be lowercase kebab-case`, func() {
			app.Command("uu", "update", noop, WithEffect(EffectMutating),
				WithUpdateOf("BAD-NAME", WriteSparse, Properties("nope")))
		})
	})
	t.Run("role legality runs ahead of presence legality", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		expectPanic(t, `property "content" is a positional arg`, func() {
			app.Command("uu", "update", noop, WithEffect(EffectMutating),
				WithUpdateOf("thing", WriteSparse, Properties("content")),
				WithArgs(NewArg("content", "content", ArgRequired())))
		})
	})
	t.Run("the name reservation runs last", func(t *testing.T) {
		app := NewApp("tt", "1.0.0", "tt")
		// The nullable-off-a-property refusal is step 8's first half and the
		// reservation its second, so a declaration carrying both reports the
		// first.
		expectPanic(t, `flag '--zone' declares Nullable() but is not a property of an update`, func() {
			app.Command("uu", "update", noop, WithEffect(EffectMutating),
				WithUpdateOf("thing", WriteSparse, Properties("content")),
				WithFlags(
					StringFlag("zone", "zone", Optional(), Nullable()),
					StringFlag("content", "content", Optional(), Nullable()),
					StringFlag("unset-content", "collides", Optional()),
				))
		})
	})
}

// --- §27.4: the at-least-one-property rule ---

func TestAtLeastOnePropertyIsRequired(t *testing.T) {
	app := updateFixture(t, nil)
	r := app.Test([]string{"update-record", "--zone", "z1", "--record-id", "r7"})
	if r.ExitCode != 1 {
		t.Fatalf("exit = %d, stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: update \"dns-record\": at least one property is required: --content, --ttl, --proxied\n"
	if !strings.HasPrefix(r.Stderr, want) {
		t.Fatalf("stderr = %q, want prefix %q", r.Stderr, want)
	}
}

func TestANegatedBoolPropertyIsAProvisionAndNotADecline(t *testing.T) {
	// Inside an update command `--no-proxied` WRITES false, so it satisfies the
	// rule rather than declining it -- which is why §12.16 pins that the
	// sentence carries no decline clause.
	var got interface{}
	var provided bool
	app := updateFixture(t, func(ctx *Context, args map[string]interface{}) Outcome {
		got, provided = args["proxied"], ctx.Provided("proxied")
		return Exit(0)
	})
	r := app.Test([]string{"update-record", "--zone", "z1", "--record-id", "r7", "--no-proxied"})
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	if got != false || !provided {
		t.Fatalf("proxied = %v (provided %v), want false (provided true)", got, provided)
	}
}

func TestAnEnvProvidedPropertySatisfiesTheRule(t *testing.T) {
	// There is no source filter: the framework has exactly one definition of
	// "was this supplied" (§23.6), and the containment is that the write set is
	// rendered, so a configured value cannot join a write invisibly.
	app := NewApp("tt", "1.0.0", "tt")
	var writes *writesEnvelope
	app.Command("uu", "update", func(ctx *Context, args map[string]interface{}) Outcome {
		writes = ctx.writes.envelopeMember()
		return Exit(0)
	}, WithEffect(EffectMutating),
		WithUpdateOf("thing", WriteSparse, Properties("content", "ttl")),
		WithFlags(
			StringFlag("content", "content", Optional(), Env("T_CONTENT")),
			IntFlag("ttl", "ttl", Optional()),
		))
	t.Setenv("T_CONTENT", "from-env")
	r := app.Test([]string{"uu"})
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	if len(writes.Written) != 1 || writes.Written[0] != "content" {
		t.Fatalf("written = %v", writes.Written)
	}
}

// --- §27.6: the clear vocabulary ---

func TestUnsetDeliversAbsenceAndReportsProvided(t *testing.T) {
	var value interface{}
	var present bool
	var provided, unset, untouchedUnset bool
	app := updateFixture(t, func(ctx *Context, args map[string]interface{}) Outcome {
		value, present = args["ttl"]
		provided, unset = ctx.Provided("ttl"), ctx.Unset("ttl")
		untouchedUnset = ctx.Unset("content")
		return Exit(0)
	})
	r := app.Test([]string{"update-record", "--zone", "z1", "--record-id", "r7", "--unset-ttl"})
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	if !present || value != nil {
		t.Fatalf("ttl = %v (present %v), want a present nil", value, present)
	}
	if !provided || !unset || untouchedUnset {
		t.Fatalf("provided=%v unset=%v untouched-unset=%v", provided, unset, untouchedUnset)
	}
}

func TestUnsetAcceptsDashedAndUnderscoredNames(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	var dashed, underscored bool
	app.Command("uu", "update", func(ctx *Context, args map[string]interface{}) Outcome {
		dashed, underscored = ctx.Unset("phone-number"), ctx.Unset("phone_number")
		return Exit(0)
	}, WithEffect(EffectMutating),
		WithUpdateOf("thing", WriteSparse, Properties("phone-number")),
		WithFlags(StringFlag("phone-number", "the number", Optional(), Nullable())))
	if r := app.Test([]string{"uu", "--unset-phone-number"}); r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	if !dashed || !underscored {
		t.Fatalf("dashed=%v underscored=%v", dashed, underscored)
	}
}

func TestUnsetOnAnUnknownNamePanicsLikeProvided(t *testing.T) {
	app := updateFixture(t, func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Unset("nope")
		return Exit(0)
	})
	got := mustPanic(t, func() {
		app.Test([]string{"update-record", "--zone", "z1", "--record-id", "r7", "--content", "xx"})
	})
	if !strings.Contains(got, "nope") {
		t.Fatalf("panic = %q", got)
	}
}

func TestValueAndUnsetTogetherIsAParseError(t *testing.T) {
	app := updateFixture(t, nil)
	r := app.Test([]string{"update-record", "--zone", "z1", "--record-id", "r7", "--ttl", "300", "--unset-ttl"})
	if r.ExitCode != 1 {
		t.Fatalf("exit = %d", r.ExitCode)
	}
	want := "error: --ttl and --unset-ttl are mutually exclusive: a property is either written or cleared\n"
	if !strings.HasPrefix(r.Stderr, want) {
		t.Fatalf("stderr = %q, want prefix %q", r.Stderr, want)
	}
}

func TestTheMintedUnsetFlagIsNotNegatable(t *testing.T) {
	app := updateFixture(t, nil)
	r := app.Test([]string{"update-record", "--zone", "z1", "--record-id", "r7", "--no-unset-ttl"})
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "unknown flag '--no-unset-ttl'") {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestAnUnsetOnANonNullablePropertyNamesNothing(t *testing.T) {
	app := updateFixture(t, nil)
	r := app.Test([]string{"update-record", "--zone", "z1", "--record-id", "r7", "--unset-content"})
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "unknown flag '--unset-content'") {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestEmptyStringIsAnOrdinaryValue(t *testing.T) {
	var got interface{}
	app := updateFixture(t, func(ctx *Context, args map[string]interface{}) Outcome {
		got = args["content"]
		return Exit(0)
	})
	if r := app.Test([]string{"update-record", "--zone", "z1", "--record-id", "r7", "--content", ""}); r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr=%q", r.ExitCode, r.Stderr)
	}
	if got != "" {
		t.Fatalf("content = %v, want the empty string", got)
	}
}

// --- §27.5: the write set's two renderings ---

func TestTheWriteSetLineTakesNoSequenceNumber(t *testing.T) {
	app := updateFixture(t, func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Effects().HTTP("PATCH", "https://api.example.com/zones/z1/dns_records/r7")
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "update-record", "--zone", "z1", "--record-id", "r7",
		"--content", "1.2.3.4", "--unset-ttl"})
	want := "DRY RUN — no changes were made. Would do:\n" +
		"  writes: content; clears: ttl (other properties unchanged)\n" +
		"  1. net: PATCH https://api.example.com/zones/z1/dns_records/r7\n"
	if r.Stdout != want {
		t.Fatalf("got:\n%q\nwant:\n%q", r.Stdout, want)
	}
}

func TestTheWriteSetLinesPinnedForms(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		mode WriteMode
		want string
	}{
		{"one written", []string{"--content", "xx"}, WriteSparse, "  writes: content (other properties unchanged)"},
		{"two written, declaration order", []string{"--ttl", "5", "--content", "xx"}, WriteSparse, "  writes: content, ttl (other properties unchanged)"},
		{"both segments", []string{"--content", "xx", "--unset-ttl"}, WriteSparse, "  writes: content; clears: ttl (other properties unchanged)"},
		{"clears only", []string{"--unset-ttl"}, WriteSparse, "  clears: ttl (other properties unchanged)"},
		{"full replace", []string{"--content", "xx"}, WriteFullReplace, "  writes: content (other properties are re-sent as read)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := NewApp("dnsapp", "1.0.0", "manage DNS")
			app.Command("update-record", "change one DNS record in place", noop,
				WithEffect(EffectMutating),
				WithUpdateOf("dns-record", tc.mode, Properties("content", "ttl", "proxied")),
				WithFlags(
					StringFlag("content", "record content", Optional()),
					IntFlag("ttl", "time to live in seconds", Optional(), Nullable()),
					BoolFlag("proxied", "whether the record is proxied", Optional()),
				))
			r := app.Test(append([]string{"--dry-run", "update-record"}, tc.argv...))
			want := "DRY RUN — no changes were made. Would do:\n" + tc.want + "\n"
			if r.Stdout != want {
				t.Fatalf("got:\n%q\nwant:\n%q", r.Stdout, want)
			}
		})
	}
}

func TestTheWriteSetLineRendersInDryModeOnly(t *testing.T) {
	app := updateFixture(t, func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) })
	r := app.Test([]string{"update-record", "--zone", "z1", "--record-id", "r7", "--content", "xx"})
	if strings.Contains(r.Stdout, "writes:") {
		t.Fatalf("a live run printed the write-set line: %q", r.Stdout)
	}
}

func TestTheEnvelopeCarriesTheWriteSetInBothModes(t *testing.T) {
	for _, dry := range []bool{false, true} {
		app := updateFixture(t, nil)
		argv := []string{"--json", "update-record", "--zone", "z1", "--record-id", "r7",
			"--content", "1.2.3.4", "--unset-ttl"}
		if dry {
			argv = append([]string{"--dry-run"}, argv...)
		}
		r := app.Test(argv)
		var env map[string]interface{}
		if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil {
			t.Fatalf("stdout is not one JSON document: %v (%q)", err, r.Stdout)
		}
		if env["interface_version"] != float64(3) {
			t.Fatalf("interface_version = %v", env["interface_version"])
		}
		writes, ok := env["writes"].(map[string]interface{})
		if !ok {
			t.Fatalf("writes = %v", env["writes"])
		}
		if writes["resource"] != "dns-record" || writes["write_mode"] != "sparse" {
			t.Fatalf("writes = %v", writes)
		}
		for key, want := range map[string][]interface{}{
			"written":   {"content"},
			"cleared":   {"ttl"},
			"resent":    {},
			"untouched": {"proxied"},
		} {
			got := writes[key].([]interface{})
			if len(got) != len(want) {
				t.Fatalf("%s = %v, want %v", key, got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("%s = %v, want %v", key, got, want)
				}
			}
		}
	}
}

func TestTheEnvelopesWriteSetKeyOrderIsPinned(t *testing.T) {
	app := updateFixture(t, nil)
	r := app.Test([]string{"--json", "update-record", "--zone", "z1", "--record-id", "r7", "--content", "xx"})
	want := `"writes":{"resource":"dns-record","write_mode":"sparse","written":["content"],"cleared":[],"resent":[],"untouched":["ttl","proxied"]}`
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("stdout = %q, want it to contain %q", r.Stdout, want)
	}
}

func TestFullReplaceSwapsResentAndUntouched(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	app.Command("uu", "update", noop, WithEffect(EffectMutating),
		WithUpdateOf("thing", WriteFullReplace, Properties("content", "ttl")),
		WithFlags(
			StringFlag("content", "content", Optional()),
			IntFlag("ttl", "ttl", Optional()),
		))
	r := app.Test([]string{"--json", "uu", "--content", "xx"})
	want := `"writes":{"resource":"thing","write_mode":"full_replace","written":["content"],"cleared":[],"resent":["ttl"],"untouched":[]}`
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("stdout = %q, want it to contain %q", r.Stdout, want)
	}
}

func TestACommandWithNoUpdateCarriesANullWritesMember(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	app.Command("rr", "read", noop, WithEffect(EffectReadOnly))
	r := app.Test([]string{"--json", "rr"})
	if !strings.Contains(r.Stdout, `"writes":null`) {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestTheEnvelopeUsesUnderscoredParameterNames(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	app.Command("uu", "update", noop, WithEffect(EffectMutating),
		WithUpdateOf("thing", WriteSparse, Properties("phone-number", "display-name")),
		WithFlags(
			StringFlag("phone-number", "the number", Optional()),
			StringFlag("display-name", "the name", Optional()),
		))
	r := app.Test([]string{"--json", "uu", "--phone-number", "555"})
	if !strings.Contains(r.Stdout, `"written":["phone_number"],"cleared":[],"resent":[],"untouched":["display_name"]`) {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestTheHumanLineUsesDeclaredNamesWithoutThePrefix(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	app.Command("uu", "update", noop, WithEffect(EffectMutating),
		WithUpdateOf("thing", WriteSparse, Properties("phone-number")),
		WithFlags(StringFlag("phone-number", "the number", Optional())))
	r := app.Test([]string{"--dry-run", "uu", "--phone-number", "555"})
	if !strings.Contains(r.Stdout, "  writes: phone-number (other properties unchanged)\n") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

// --- §27.6: help rendering ---

func TestNullableRendersItsMintedSpellingOnOneLine(t *testing.T) {
	app := updateFixture(t, nil)
	r := app.Test([]string{"update-record", "--help"})
	for _, want := range []string{
		"--content <str>",
		"--ttl <int>, --unset-ttl",
		"--proxied, --no-proxied",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Fatalf("help does not contain %q:\n%s", want, r.Stdout)
		}
	}
	// One presence part per line, and no second line for the minted spelling.
	if strings.Count(r.Stdout, "--unset-ttl") != 1 {
		t.Fatalf("the minted spelling rendered more than once:\n%s", r.Stdout)
	}
	if strings.Count(r.Stdout, "[optional]") != 3 {
		t.Fatalf("expected exactly three presence parts:\n%s", r.Stdout)
	}
}

func TestANullableBoolRendersAllThreeSpellings(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	app.Command("uu", "update", noop, WithEffect(EffectMutating),
		WithUpdateOf("thing", WriteSparse, Properties("proxied")),
		WithFlags(BoolFlag("proxied", "whether the record is proxied", Optional(), Nullable())))
	r := app.Test([]string{"uu", "--help"})
	if !strings.Contains(r.Stdout, "--proxied, --no-proxied, --unset-proxied") {
		t.Fatalf("help:\n%s", r.Stdout)
	}
}

// --- §27.9: the schema encoding ---

func TestTheDumpPublishesTheUpdatePairAndNullable(t *testing.T) {
	chdirTemp(t)
	app := NewApp("dnsapp", "1.0.0", "manage DNS")
	app.Command("update-record", "change one DNS record in place", noop,
		WithEffect(EffectMutating),
		WithUpdateOf("dns-record", WriteSparse,
			Identity("zone", "record-id"), Properties("content", "ttl")),
		WithFlags(
			StringFlag("zone", "zone the record belongs to", Required()),
			StringFlag("record-id", "identifier of the record", Required()),
			StringFlag("content", "record content", Optional()),
			IntFlag("ttl", "time to live", Optional(), Nullable()),
		))
	text := dumpText(t, app)
	// The pair sits immediately after the dry-run pair's position and ahead of
	// the payload keys (§25.9), and the names are the DECLARED spelling.
	want := `      "effect": "mutating",
      "update_of": {
        "resource": "dns-record",
        "identity": [
          "zone",
          "record-id"
        ],
        "properties": [
          "content",
          "ttl"
        ]
      },
      "write_mode": "sparse",`
	if !strings.Contains(text, want) {
		t.Fatalf("dump:\n%s", text)
	}
	if !strings.Contains(text, `          "presence": "optional",
          "nullable": true`) {
		t.Fatalf("the flag entry does not carry nullable last:\n%s", text)
	}
	// No second entry for the minted spelling: the dump publishes declarations.
	if strings.Contains(text, "unset-ttl") {
		t.Fatalf("the dump minted a second flag entry:\n%s", text)
	}
}

func TestACommandWithNoUpdateOmitsThePair(t *testing.T) {
	chdirTemp(t)
	app := NewApp("tt", "1.0.0", "tt")
	app.Command("rr", "read", noop, WithEffect(EffectReadOnly))
	// Read past the `defaults` block, which carries both keys' baselines.
	text := dumpText(t, app)
	entries := text[strings.Index(text, "\n  \"commands\""):]
	if strings.Contains(entries, "update_of") || strings.Contains(entries, "write_mode") {
		t.Fatalf("dump:\n%s", text)
	}
}

// --- §27.10: the MCP projection ---

func TestTheAtLeastOnePropertyRuleProjectsAsABareAnyOf(t *testing.T) {
	app := updateFixture(t, nil)
	schema := app.JsonSchema("update-record")
	branches, ok := schema["anyOf"].([]interface{})
	if !ok || len(branches) != 3 {
		t.Fatalf("anyOf = %v", schema["anyOf"])
	}
	for i, want := range []string{"content", "ttl", "proxied"} {
		req := branches[i].(map[string]interface{})["required"].([]interface{})
		if len(req) != 1 || req[0] != want {
			t.Fatalf("branch %d = %v, want [%s]", i, req, want)
		}
	}
	// A property is never in `required`: its requiredness IS the rule.
	for _, name := range schema["required"].([]interface{}) {
		if name == "content" || name == "ttl" || name == "proxied" {
			t.Fatalf("a property reached required: %v", schema["required"])
		}
	}
	if len(schema["required"].([]interface{})) != 2 {
		t.Fatalf("required = %v, want the two identity flags", schema["required"])
	}
}

func TestTheUpdateBranchComesFirstInsideTheAllOf(t *testing.T) {
	app := updateFixture(t, nil, WithConstraints(
		AtLeastOne("addressing", Member("content"), Member("ttl")),
	))
	schema := app.JsonSchema("update-record")
	if _, bare := schema["anyOf"]; bare {
		t.Fatalf("a command with two anyOf-producing rules emitted a bare anyOf: %v", schema)
	}
	allOf := schema["allOf"].([]interface{})
	if len(allOf) != 2 {
		t.Fatalf("allOf = %v", allOf)
	}
	first := allOf[0].(map[string]interface{})["anyOf"].([]interface{})
	if len(first) != 3 {
		t.Fatalf("the update's branch is not first: %v", allOf)
	}
}

func TestANullablePropertyPublishesATypeList(t *testing.T) {
	app := updateFixture(t, nil)
	schema := app.JsonSchema("update-record")
	props := schema["properties"].(map[string]interface{})
	ttl := props["ttl"].(map[string]interface{})
	list, ok := ttl["type"].([]interface{})
	if !ok || len(list) != 2 || list[0] != "integer" || list[1] != "null" {
		t.Fatalf("ttl type = %v", ttl["type"])
	}
	if _, isList := props["content"].(map[string]interface{})["type"].([]interface{}); isList {
		t.Fatalf("a non-nullable property published a type list: %v", props["content"])
	}
}

func TestTheUpdateDescriptionBlock(t *testing.T) {
	app := updateFixture(t, nil)
	var got string
	for _, tool := range app.AsTools() {
		if tool.Name == "update-record" {
			got = tool.Description
		}
	}
	want := "change one DNS record in place\n\n" +
		"Update of \"dns-record\" (write mode: sparse):\n" +
		"  identifies: zone, record_id\n" +
		"  writes: content, ttl, proxied -- at least one is required\n" +
		"  a property that is not supplied is left unchanged; null clears ttl"
	if got != want {
		t.Fatalf("description:\n%q\nwant:\n%q", got, want)
	}
}

func TestTheUpdateDescriptionBlockOmitsIdentifiesAndTheNullClause(t *testing.T) {
	app := NewApp("tt", "1.0.0", "tt")
	app.Command("uu", "update it", noop, WithEffect(EffectMutating),
		WithUpdateOf("thing", WriteFullReplace, Properties("content")),
		WithFlags(StringFlag("content", "content", Optional())))
	var got string
	for _, tool := range app.AsTools() {
		if tool.Name == "uu" {
			got = tool.Description
		}
	}
	want := "update it\n\n" +
		"Update of \"thing\" (write mode: full_replace):\n" +
		"  writes: content -- at least one is required\n" +
		"  a property that is not supplied is re-sent as read"
	if got != want {
		t.Fatalf("description:\n%q\nwant:\n%q", got, want)
	}
}

// --- §24.11's carve-out: the machine doors ---

func TestNullOnANullablePropertysKeyIsTheClear(t *testing.T) {
	var value interface{}
	var present, provided, unset bool
	app := updateFixture(t, func(ctx *Context, args map[string]interface{}) Outcome {
		value, present = args["ttl"]
		provided, unset = ctx.Provided("ttl"), ctx.Unset("ttl")
		return Exit(0)
	})
	if _, err := app.Call("update-record", map[string]interface{}{
		"zone": "z1", "record_id": "r7", "ttl": nil,
	}); err != nil {
		t.Fatalf("call error: %v", err)
	}
	if !present || value != nil {
		t.Fatalf("ttl = %v (present %v)", value, present)
	}
	if !provided || !unset {
		t.Fatalf("provided=%v unset=%v", provided, unset)
	}
}

func TestNullOnANonNullablePropertyIsStillRefused(t *testing.T) {
	app := updateFixture(t, nil)
	_, err := app.Call("update-record", map[string]interface{}{
		"zone": "z1", "record_id": "r7", "content": nil,
	})
	if err == nil {
		t.Fatal("a null on a non-nullable property was accepted")
	}
	if !strings.Contains(err.Error(), "content") {
		t.Fatalf("error = %v", err)
	}
}

func TestTheAtLeastOneRuleIsEnforcedAtTheMachineDoor(t *testing.T) {
	app := updateFixture(t, nil)
	_, err := app.Call("update-record", map[string]interface{}{"zone": "z1", "record_id": "r7"})
	if err == nil {
		t.Fatal("a call writing nothing was accepted")
	}
	want := `update "dns-record": at least one property is required: --content, --ttl, --proxied`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// --- §27.12: composition ---

func TestDryRunUnsupportedComposesWithAnUpdate(t *testing.T) {
	// The human write-set line then never renders, there being no dry run to
	// render it in; the envelope's member still does, in a live run.
	app := updateFixture(t, nil, WithDryRunUnsupported("the API has no preview endpoint"))
	r := app.Test([]string{"--dry-run", "update-record", "--zone", "z1", "--record-id", "r7", "--content", "xx"})
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "the API has no preview endpoint") {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	live := app.Test([]string{"--json", "update-record", "--zone", "z1", "--record-id", "r7", "--content", "xx"})
	if !strings.Contains(live.Stdout, `"written":["content"]`) {
		t.Fatalf("stdout = %q", live.Stdout)
	}
}

func TestAnUpdateCommandDeclaresConstraintsLikeAnyCommand(t *testing.T) {
	// Alternative addressing over two optional identity members IS an
	// AtLeastOne, which is the intended composition (§27.12).
	app := NewApp("tt", "1.0.0", "tt")
	app.Command("uu", "update", noop, WithEffect(EffectMutating),
		WithUpdateOf("thing", WriteSparse, Identity("id", "name"), Properties("content")),
		WithFlags(
			StringFlag("id", "by id", Optional()),
			StringFlag("name", "by name", Optional()),
			StringFlag("content", "content", Optional()),
		),
		WithConstraints(AtLeastOne("addressing", Member("id"), Member("name"))))
	r := app.Test([]string{"uu", "--content", "xx"})
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, `constraint "addressing"`) {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestAnImpliedPropertyIsAProvision(t *testing.T) {
	// A value injected by an Implies exists only because the invocation
	// contained the trigger, so it is provided (§23.6) and joins the write set
	// -- the write set has no source filter (§27.4).
	app := NewApp("tt", "1.0.0", "tt")
	app.Command("uu", "update", noop, WithEffect(EffectMutating),
		WithUpdateOf("thing", WriteSparse, Properties("proxied")),
		WithFlags(
			BoolFlag("secure", "turn on the secure mode", Optional()),
			BoolFlag("proxied", "whether the record is proxied", Optional()),
		),
		WithConstraints(Implies("secure-proxies", "secure", "proxied", true)))
	r := app.Test([]string{"--json", "uu", "--secure"})
	if !strings.Contains(r.Stdout, `"written":["proxied"]`) {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestAnUpdateWithNoIdentityPublishesAnEmptyArray(t *testing.T) {
	// All three keys are always present inside the object; identity is []
	// when the resource declares none (contract §13's amendment).
	chdirTemp(t)
	app := NewApp("tt", "1.0.0", "tt")
	app.Command("uu", "update", noop, WithEffect(EffectMutating),
		WithUpdateOf("thing", WriteFullReplace, Properties("content")),
		WithFlags(StringFlag("content", "content", Optional())))
	text := dumpText(t, app)
	want := `      "update_of": {
        "resource": "thing",
        "identity": [],
        "properties": [
          "content"
        ]
      },
      "write_mode": "full_replace",`
	if !strings.Contains(text, want) {
		t.Fatalf("dump:\n%s", text)
	}
}

func TestClearingABoolProperty(t *testing.T) {
	// Every type may be nullable, the four scalars and the compounds alike:
	// clearing is a fact about the resource's field, not about the value's
	// shape (§27.6).
	var value interface{}
	var unset bool
	app := NewApp("tt", "1.0.0", "tt")
	app.Command("uu", "update", func(ctx *Context, args map[string]interface{}) Outcome {
		value, unset = args["proxied"], ctx.Unset("proxied")
		return Exit(0)
	}, WithEffect(EffectMutating),
		WithUpdateOf("thing", WriteSparse, Properties("proxied")),
		WithFlags(BoolFlag("proxied", "whether the record is proxied", Optional(), Nullable())))
	if r := app.Test([]string{"uu", "--unset-proxied"}); r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if value != nil || !unset {
		t.Fatalf("proxied = %v (unset %v)", value, unset)
	}
}

func TestTheUnsetNameReservationReachesTheAppsGlobals(t *testing.T) {
	// A global is recognized after the command name too, so a global of the
	// minted name would be unreachable behind the clear spelling.
	app := NewApp("tt", "1.0.0", "tt")
	app.GlobalFlag(StringFlag("unset-content", "a global of that name", Optional()))
	expectPanic(t, `command "uu": flag name "unset-content" is reserved: property '--content' declares Nullable(), which mints '--unset-content'`, func() {
		app.Command("uu", "update", noop, WithEffect(EffectMutating),
			WithUpdateOf("thing", WriteSparse, Properties("content")),
			WithFlags(StringFlag("content", "content", Optional(), Nullable())))
	})
}
