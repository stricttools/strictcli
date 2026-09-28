package strictcli

import (
	"strings"
	"testing"
)

// The naming rule: every identifier a caller types or references is lowercase
// kebab-case of at least two characters, and a short form is one ASCII letter.
// Each refusal names the rule, so each test also applies the fix it names (a
// conforming spelling) and sees the declaration accepted.

var nonKebabNames = []string{"Compile", "compile_all", "COMPILE", "c", "fast-", "a--b", "-x", "9lives", "déploy", ""}

var kebabNames = []string{"compile", "compile-all", "c0", "x86-64", "ab"}

func namingApp() *App { return NewApp("myapp", "1.0.0", "test app") }

func roHandler(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) }

func TestNaming_CommandNames(t *testing.T) {
	for _, n := range nonKebabNames {
		if n == "" {
			continue
		}
		mustPanicWith(t, errCommandNameInvalid(n), func() {
			namingApp().Command(n, "x", roHandler, WithEffect(EffectReadOnly))
		})
		mustPanicWith(t, errCommandNameInvalid(n), func() {
			namingApp().Group("grp", "a group").Command(n, "x", roHandler, WithEffect(EffectReadOnly))
		})
	}
	for _, n := range kebabNames {
		namingApp().Command(n, "x", roHandler, WithEffect(EffectReadOnly))
		namingApp().Group("grp", "a group").Command(n, "x", roHandler, WithEffect(EffectReadOnly))
	}
}

func TestNaming_GroupAndDeprecatedNames(t *testing.T) {
	for _, n := range []string{"Db", "db_tools", "d", "db-"} {
		mustPanicWith(t, errGroupNameInvalid(n), func() { namingApp().Group(n, "a group") })
		mustPanicWith(t, errGroupNameInvalid(n), func() { namingApp().Group("grp", "g").Group(n, "a group") })
		mustPanicWith(t, errDeprecatedNameInvalid(n), func() { namingApp().Deprecated(n, "gone") })
		mustPanicWith(t, errDeprecatedNameInvalid(n), func() { namingApp().Group("grp", "g").Deprecated(n, "gone") })
	}
	app := namingApp()
	app.Group("db-tools", "a group").Group("sub", "a sub group")
	app.Deprecated("old-cmd", "gone")
}

func TestNaming_FlagNames(t *testing.T) {
	for _, n := range []string{"Device", "device_id", "DEVICE", "d", "m", "F", "device-"} {
		mustPanicWith(t, errFlagNameInvalid(n), func() { StringFlag(n, "help text", Required()) })
		mustPanicWith(t, errFlagNameInvalid(n), func() { BoolFlag(n, "help text", Default(false)) })
		mustPanicWith(t, errFlagNameInvalid(n), func() { ChoiceFlag(n, "pick", Required(), Choice("one", "the first"), Choice("two", "the second")) })
		// A choice-scoped flag at depth runs the same rule.
		mustPanicWith(t, errFlagNameInvalid(n), func() {
			ChoiceFlag("via", "pick", Required(), Choice("email", "as mail", StringFlag(n, "help text", Required())), Choice("sms", "as text"))
		})
	}
	StringFlag("device-id", "help text", Required())
	ChoiceFlag("via", "pick", Required(), Choice("email", "as mail", StringFlag("subject-line", "help text", Required())), Choice("sms", "as text"))
}

func TestNaming_MemberChoiceNamesAreFlagNames(t *testing.T) {
	// A member choice's name is its member flag's name, built by a flag
	// constructor, so the flag rule refuses it there.
	mustPanicWith(t, errFlagNameInvalid("w"), func() {
		MemberChoiceFlag("profile", "which profile", Required(),
			MemberChoice(BoolFlag("w", "the work profile", Required()), "the work profile"),
			MemberChoice(BoolFlag("home", "the home profile", Required()), "the home profile"))
	})
	MemberChoiceFlag("profile", "which profile", Required(),
		MemberChoice(BoolFlag("work", "the work profile", Required()), "the work profile"),
		MemberChoice(BoolFlag("home", "the home profile", Required()), "the home profile"))
}

func TestNaming_ShortForms(t *testing.T) {
	for _, s := range []string{"1", "ab", "é", "-", "_"} {
		mustPanicWith(t, errFlagShortInvalid("device", s), func() { StringFlag("device", "help text", Required(), Short(s)) })
	}
	for _, s := range []string{"d", "D", "F", "m"} {
		StringFlag("device", "help text", Required(), Short(s))
	}
}

func TestNaming_GlobalFlagNames(t *testing.T) {
	// The flag constructor refuses the name before the global registration
	// sees it, so the app's own global path is covered by construction.
	mustPanicWith(t, errFlagNameInvalid("Verbose_out"), func() {
		namingApp().GlobalFlag(BoolFlag("Verbose_out", "more", Default(false)))
	})
	namingApp().GlobalFlag(BoolFlag("verbose-out", "more", Default(false)))
}

func TestNaming_ChoiceNames(t *testing.T) {
	for _, n := range []string{"a", "Email", "e_mail", "email-"} {
		mustPanicWith(t, errChoiceNameCharset("via", n), func() {
			ChoiceFlag("via", "pick", Required(), Choice(n, "one choice"), Choice("sms", "text"))
		})
	}
	ChoiceFlag("via", "pick", Required(), Choice("e-mail", "one choice"), Choice("sms", "text"))
}

func TestNaming_TagNames(t *testing.T) {
	for _, n := range []string{"fast-", "a--b", "x", "Fast"} {
		mustPanicWith(t, errInvalidTagName(n), func() {
			namingApp().Command("cmd", "x", roHandler, WithEffect(EffectReadOnly), WithTags(n))
		})
		mustPanicWith(t, errInvalidTagName(n), func() { namingApp().Group("grp", "g", n) })
		mustPanicWith(t, errInvalidTagName(n), func() { namingApp().TagContract(n, "force-overwrite") })
	}
	namingApp().Command("cmd", "x", roHandler, WithEffect(EffectReadOnly), WithTags("fast-lane"))
}

func TestNaming_ConstraintGrantAndResourceNames(t *testing.T) {
	flags := WithFlags(StringFlag("file", "a file", Optional()), StringFlag("host", "a host", Optional()))
	mustPanicWith(t, errConstraintNameCharset("cmd", "p"), func() {
		namingApp().Command("cmd", "x", roHandler, WithEffect(EffectReadOnly), flags,
			WithConstraints(AtLeastOne("p", Member("file"), Member("host"))))
	})
	namingApp().Command("cmd", "x", roHandler, WithEffect(EffectReadOnly), flags,
		WithConstraints(AtLeastOne("pick", Member("file"), Member("host"))))

	mustPanicWith(t, errGrantNameInvalid("cmd", "push-"), func() {
		namingApp().Command("cmd", "x", roHandler, WithEffect(EffectMutating),
			WithGrants(Grant{Name: "push-", Reason: "r", Kind: ProcMutate}))
	})
	namingApp().Command("cmd", "x", roHandler, WithEffect(EffectMutating),
		WithGrants(Grant{Name: "push-it", Reason: "r", Kind: ProcMutate}))

	upd := func(resource string) {
		namingApp().Command("cmd", "x", roHandler, WithEffect(EffectMutating),
			WithFlags(StringFlag("content", "the content", Optional())),
			WithUpdateOf(resource, WriteSparse, Properties("content")))
	}
	mustPanicWith(t, errUpdateResourceCharset("cmd", "r"), func() { upd("r") })
	upd("dns-record")
}

func TestNaming_CheckAndHookNames(t *testing.T) {
	check := func(name string) string {
		return "app = \"myapp\"\n\n[checks." + name + "]\ndescription = \"d\"\nsubject = \"quality\"\ntags = [\"fast-lane\"]\n" +
			"severity = \"error\"\nfast = true\npure = true\nneeds_network = false\ndepends_on = []\n"
	}
	for _, n := range []string{"x", "lint-", "lint--code"} {
		_, _, _, _, err := parseChecksTomlWithHooks([]byte(check("\"" + n + "\"")))
		if err == nil || err.Error() != errChecksTomlInvalidCheckName(n).Error() {
			t.Fatalf("check name %q: got %v", n, err)
		}
	}
	if _, _, _, _, err := parseChecksTomlWithHooks([]byte(check("lint-code"))); err != nil {
		t.Fatalf("lint-code refused: %v", err)
	}
	hook := func(name string) string {
		return check("lint-code") + "\n[hooks.\"" + name + "\"]\ntag = \"fast-lane\"\n"
	}
	for _, n := range []string{"p", "pre-", "Pre-push"} {
		_, _, _, _, err := parseChecksTomlWithHooks([]byte(hook(n)))
		if err == nil || err.Error() != errChecksTomlInvalidHookName(n).Error() {
			t.Fatalf("hook name %q: got %v", n, err)
		}
	}
	if _, _, _, _, err := parseChecksTomlWithHooks([]byte(hook("pre-push"))); err != nil {
		t.Fatalf("pre-push refused: %v", err)
	}
}

func TestNaming_MessagesNameTheRule(t *testing.T) {
	for _, msg := range []string{
		errCommandNameInvalid("C"), errGroupNameInvalid("G"), errDeprecatedNameInvalid("D"),
		errFlagNameInvalid("F"), errInvalidTagName("T"), errChoiceNameCharset("s", "c"),
		errConstraintNameCharset("cmd", "c"), errGrantNameInvalid("cmd", "g"), errUpdateResourceCharset("cmd", "r"),
		errChecksTomlInvalidCheckName("c").Error(), errChecksTomlInvalidHookName("h").Error(),
	} {
		if !strings.Contains(msg, kebabNameClause) {
			t.Errorf("%q does not carry the naming rule's clause", msg)
		}
	}
}

// help and version are framework commands, reserved at every level.
func TestNaming_FrameworkCommandNamesReserved(t *testing.T) {
	for _, n := range []string{"help", "version"} {
		mustPanicWith(t, errFrameworkCommandName("command", n), func() {
			namingApp().Command(n, "x", roHandler, WithEffect(EffectReadOnly))
		})
		mustPanicWith(t, errFrameworkCommandName("command", n), func() {
			namingApp().Group("grp", "g").Group("sub", "s").Command(n, "x", roHandler, WithEffect(EffectReadOnly))
		})
		mustPanicWith(t, errFrameworkCommandName("command", n), func() {
			namingApp().Passthrough(n, "x", func(ctx *Context, name string, args []string, g map[string]interface{}) int { return 0 },
				WithEffect(EffectReadOnly))
		})
		mustPanicWith(t, errFrameworkCommandName("group", n), func() { namingApp().Group(n, "g") })
		mustPanicWith(t, errFrameworkCommandName("group", n), func() { namingApp().Group("grp", "g").Group(n, "g") })
		mustPanicWith(t, errFrameworkCommandName("deprecated command", n), func() { namingApp().Deprecated(n, "gone") })
		mustPanicWith(t, errFrameworkCommandName("deprecated command", n), func() {
			namingApp().Group("grp", "g").Deprecated(n, "gone")
		})
	}
	// The fix the refusal names: any other name.
	app := namingApp()
	app.Command("show-help", "x", roHandler, WithEffect(EffectReadOnly))
	app.Group("versions", "g").Command("list", "x", roHandler, WithEffect(EffectReadOnly))
}
