package strictcli

import (
	"encoding/json"
	"strings"
	"testing"
)

// The constraint system (contract §26) and its message catalog (§12.15).
//
// Every sentence below is asserted BYTE-EXACT: the two violation templates are
// parse-time and the registration guards are registration-time, and both
// classes are what three implementations must agree on.

// --- Registration: name legality (§26.8 pass 1) ---

func TestConstraintNameCharsetIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint name "Author_Name" must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("old-name", "the old name", Optional()),
				StringFlag("new-name", "the new name", Optional()),
			),
			WithConstraints(AllOrNone("Author_Name", Member("old-name"), Member("new-name"))))
	})
}

func TestConstraintDuplicateNameIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": duplicate constraint name "pair"`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("aa", "the a", Optional()),
				StringFlag("bb", "the b", Optional()),
			),
			WithConstraints(
				AllOrNone("pair", Member("aa"), Member("bb")),
				AtLeastOne("pair", Member("aa"), Member("bb")),
			))
	})
}

func TestConstraintNameCollidingWithAFlagIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint name "aa" is already a flag or arg name: a member reference resolves by name and would be ambiguous`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("aa", "the a", Optional()),
				StringFlag("bb", "the b", Optional()),
			),
			WithConstraints(AllOrNone("aa", Member("aa"), Member("bb"))))
	})
}

func TestConstraintNameCollidingWithAnArgIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint name "targets" is already a flag or arg name: a member reference resolves by name and would be ambiguous`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("aa", "the a", Optional()),
				StringFlag("bb", "the b", Optional()),
			),
			WithArgs(NewArg("targets", "the targets", Variadic(), ArgOptional())),
			WithConstraints(AllOrNone("targets", Member("aa"), Member("bb"))))
	})
}

// --- Registration: member resolution (§26.8 pass 3) ---

func TestConstraintUnknownMemberIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "pair" references unknown member "ghost"`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(StringFlag("aa", "the a", Optional())),
			WithConstraints(AllOrNone("pair", Member("aa"), Member("ghost"))))
	})
}

// A command may declare a flag and an arg of one name today, because duplicate
// flag names and duplicate arg names are checked separately. This round refuses
// to GUESS inside that state rather than fixing it (§26.2).
func TestConstraintAmbiguousMemberIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "pair" references "target", which names both a flag and a positional arg`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("target", "the flag one", Optional()),
				StringFlag("bb", "the b", Optional()),
			),
			WithArgs(NewArg("target", "the arg one", ArgOptional())),
			WithConstraints(AllOrNone("pair", Member("target"), Member("bb"))))
	})
}

func TestConstraintDuplicateMemberIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "pair" declares member "aa" twice`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(StringFlag("aa", "the a", Optional())),
			WithConstraints(AllOrNone("pair", Member("aa"), Member("aa"))))
	})
}

func TestRequiresUnknownFlagKeepsTheFlagNoun(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "needs" references unknown flag "ghost"`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(StringFlag("aa", "the a", Optional())),
			WithConstraints(Requires("needs", "aa", "ghost")))
	})
}

// The dependency families' own guards are a TRAILING phase after the seven
// pinned steps, so an operand naming nothing is refused as unknown before the
// same-flag guard ever reads it (§26.8). Both operands are the same undeclared
// name here, which is the state where the two guards compete.
func TestRequiresUnknownFlagIsRefusedBeforeTheSameFlagGuard(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "rr" references unknown flag "zzz"`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(StringFlag("aa", "the a", Optional())),
			WithConstraints(Requires("rr", "zzz", "zzz")))
	})
}

func TestImpliesUnknownFlagIsRefusedBeforeTheSameFlagGuard(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "ii" references unknown flag "zzz"`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(BoolFlag("aa", "the a", Default(false))),
			WithConstraints(Implies("ii", "zzz", "zzz", true)))
	})
}

// --- Registration: scope (§26.8 pass 4, §24.8) ---

func TestConstraintMemberInsideAScopeIsRefused(t *testing.T) {
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

// A member-spelled choice's own flag is declared INSIDE that choice's scope, so
// naming it is the scope refusal too -- the path renders as the member token
// itself (§12.13's member-spelled segment).
func TestConstraintMemberNamingAMemberFlagIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "pick" references 'file', which is declared under '--file': constraints operate at root scope only`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("host", "the host", Optional()),
				MemberChoiceFlag("src", "where to read from", Required(),
					MemberChoice(StringFlag("file", "read from file", Required()), "read from a file"),
					MemberChoice(StringFlag("url", "read from a URL", Required()), "read from a URL"),
				),
			),
			WithConstraints(AtLeastOne("pick", Member("file"), Member("host"))))
	})
}

// --- Registration: nesting legality (§26.8 pass 5) ---

func TestNestingADependencyFamilyIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "outer" references constraint "inner", which declares a one-way dependency rather than a co-occurrence rule: only at-least-one and all-or-none can be members of another constraint`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("aa", "the a", Optional()),
				StringFlag("bb", "the b", Optional()),
			),
			WithConstraints(
				Requires("inner", "aa", "bb"),
				AtLeastOne("outer", Member("inner"), Member("aa")),
			))
	})
}

func TestNestedMemberDeclaringAnElectionIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "outer" member "inner" is a constraint and cannot declare an election: a nested constraint is engaged when its own members are`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("aa", "the a", Optional()),
				StringFlag("bb", "the b", Optional()),
			),
			WithConstraints(
				AllOrNone("inner", Member("aa"), Member("bb")),
				AtLeastOne("outer", Member("inner", WhenPresent()), Member("aa")),
			))
	})
}

func TestConstraintCycleIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraints form a cycle: outer -> inner -> outer`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("aa", "the a", Optional()),
				StringFlag("bb", "the b", Optional()),
			),
			WithConstraints(
				AtLeastOne("outer", Member("inner"), Member("aa")),
				AllOrNone("inner", Member("outer"), Member("bb")),
			))
	})
}

// The path opens on the participant declared FIRST, not on the one the walk
// entered the cycle through (§12.15). Here the walk starts at "xx", enters the
// cycle at "cc" and closes it at "bb", but "bb" is declared before "cc".
func TestConstraintCyclePathOpensOnTheEarliestDeclaredParticipant(t *testing.T) {
	expectPanic(t, `command "cmd": constraints form a cycle: bb -> cc -> bb`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(StringFlag("aa", "the a", Optional())),
			WithConstraints(
				AtLeastOne("xx", Member("cc"), Member("aa")),
				AtLeastOne("bb", Member("cc"), Member("aa")),
				AtLeastOne("cc", Member("bb"), Member("aa")),
			))
	})
}

// A constraint naming itself is the degenerate case of the same condition and
// takes the same template, never a second one (§12.15).
func TestConstraintSelfReferenceIsTheSameCycleTemplate(t *testing.T) {
	expectPanic(t, `command "cmd": constraints form a cycle: loop -> loop`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(StringFlag("aa", "the a", Optional())),
			WithConstraints(AtLeastOne("loop", Member("loop"), Member("aa"))))
	})
}

// --- Registration: election legality (§26.8 pass 6) ---

func TestBoolMemberMustDeclareItsElection(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "sel" member '--all' is a bool and must declare its election: WhenTrue() counts only a true value, WhenPresent() counts any`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				BoolFlag("all", "everything", Default(false)),
				StringFlag("older-than", "a duration", Optional()),
			),
			WithConstraints(AtLeastOne("sel", Member("all"), Member("older-than"))))
	})
}

func TestWhenTrueOnANonBoolIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "sel" member '--older-than' declares WhenTrue(), which needs a bool; '--older-than' is a str`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("older-than", "a duration", Optional()),
				StringFlag("larger-than", "a size", Optional()),
			),
			WithConstraints(AtLeastOne("sel", Member("older-than", WhenTrue()), Member("larger-than"))))
	})
}

func TestWhenNonEmptyOnAnIntIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "sel" member '--count' declares WhenNonEmpty(), which needs a string or a collection; '--count' is a int`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				IntFlag("count", "how many", Optional()),
				StringFlag("larger-than", "a size", Optional()),
			),
			WithConstraints(AtLeastOne("sel", Member("count", WhenNonEmpty()), Member("larger-than"))))
	})
}

// A selector's value is a record, so `true` and `non_empty` have nothing to test
// on it and `present` is the only election it takes (§26.2). The word names the
// CONSTRUCT rather than a value: `choice` alone is the dump's enum spelling and
// would read here as a value word for a value that does not exist (§12.15,
// §18.31 item 289).
func TestWhenNonEmptyOnASelectorIsRefused(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "sel" member '--mode' declares WhenNonEmpty(), which needs a string or a collection; '--mode' is a choice flag`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				ChoiceFlag("mode", "the mode", Required(),
					Choice("aa", "choice a"), Choice("bb", "choice b")),
				StringFlag("larger-than", "a size", Optional()),
			),
			WithConstraints(AtLeastOne("sel", Member("mode", WhenNonEmpty()), Member("larger-than"))))
	})
}

func TestWhenNonEmptyIsLegalOnAListFlagAndAVariadicArg(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(ListFlag(TypeStr, "tag", "a tag", Unique(true), Optional())),
		WithArgs(NewArg("targets", "the targets", Variadic(), ArgOptional())),
		WithConstraints(AtLeastOne("sel", Member("tag", WhenNonEmpty()), Member("targets", WhenNonEmpty()))))
	if r := app.Test([]string{"cmd", "one"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
}

// A value with a SIZE renders its collection spelling, because what the sentence
// describes is the value the selector would be evaluated against and that value
// is a sequence. The element word alone would name the type of a thing the
// member does not hold (§12.15, §18.31 item 289).
func TestSizedMembersRenderTheirCollectionSpelling(t *testing.T) {
	// A repeatable scalar flag: `str` names its element, `list[str]` its value.
	expectPanic(t, `command "cmd": constraint "sel" member '--tag' declares WhenTrue(), which needs a bool; '--tag' is a list[str]`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("tag", "a tag", Repeatable(), Unique(false), Optional()),
				StringFlag("larger-than", "a size", Optional()),
			),
			WithConstraints(AtLeastOne("sel", Member("tag", WhenTrue()), Member("larger-than"))))
	})
	// A variadic arg, rendered bare by §12.15's member rule in both halves.
	expectPanic(t, `command "cmd": constraint "sel" member 'targets' declares WhenTrue(), which needs a bool; 'targets' is a list[str]`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(StringFlag("larger-than", "a size", Optional())),
			WithArgs(NewArg("targets", "the targets", Variadic(), ArgOptional())),
			WithConstraints(AtLeastOne("sel", Member("targets", WhenTrue()), Member("larger-than"))))
	})
	// A declared list flag already carries its collection type and is not
	// wrapped a second time.
	expectPanic(t, `command "cmd": constraint "sel" member '--tag' declares WhenTrue(), which needs a bool; '--tag' is a list[int]`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				ListFlag(TypeInt, "tag", "a tag", Unique(true), Optional()),
				StringFlag("larger-than", "a size", Optional()),
			),
			WithConstraints(AtLeastOne("sel", Member("tag", WhenTrue()), Member("larger-than"))))
	})
	// A dict renders its VALUE type in one argument: the key type is `str` by
	// construction, so a two-argument word states a fact no declaration varies.
	expectPanic(t, `command "cmd": constraint "sel" member '--env' declares WhenTrue(), which needs a bool; '--env' is a dict[int]`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				DictFlag(TypeInt, "env", "an env pair", Unique(true), Optional()),
				StringFlag("larger-than", "a size", Optional()),
			),
			WithConstraints(AtLeastOne("sel", Member("env", WhenTrue()), Member("larger-than"))))
	})
}

// A variadic bool arg's value is a sequence whatever its element type, so it
// classifies as SIZED and never as bool: `non_empty` tests it, omitting `when`
// is legal, and `true` is refused with `<t>` reading `list[bool]`. The
// mandatory-election refusal's own reason has no referent here -- a variadic arg
// has no `--no-` spelling and no way to be provided while selecting nothing
// (§26.3, §18.31 item 290).
func TestVariadicBoolArgClassifiesAsSized(t *testing.T) {
	sizedApp := func(opts ...MemberOption) *App {
		return simpleApp("cmd", "a command", "ok",
			WithFlags(StringFlag("older-than", "a duration", Optional())),
			WithArgs(NewArg("flags", "the flags", ArgType(TypeBool), Variadic(), ArgOptional())),
			WithConstraints(AtLeastOne("sel", Member("flags", opts...), Member("older-than"))))
	}
	// Omitting `when` registers, and the arg's tokens engage the member.
	if r := sizedApp().Test([]string{"cmd", "true"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r := sizedApp().Test([]string{"cmd"}); r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d; stdout=%q", r.ExitCode, r.Stdout)
	}
	// `non_empty` is legal on it.
	if r := sizedApp(WhenNonEmpty()).Test([]string{"cmd", "false"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	// `true` is not, and the sentence names the sequence.
	expectPanic(t, `command "cmd": constraint "sel" member 'flags' declares WhenTrue(), which needs a bool; 'flags' is a list[bool]`, func() {
		sizedApp(WhenTrue())
	})
}

// --- Registration: presence legality (§26.8 pass 7, §26.5) ---

func TestArgMemberDeclaringRequiredIsRefusedWithTheBareName(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "sel" member 'targets' declares Required(): a member the invocation must always supply leaves the constraint nothing to decide`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(StringFlag("older-than", "a duration", Optional())),
			WithArgs(NewArg("targets", "the targets", Variadic(), ArgRequired())),
			WithConstraints(AtLeastOne("sel", Member("targets"), Member("older-than"))))
	})
}

// --- The pinned pass order (§26.8) ---
//
// A declaration with two faults reports the earlier PASS, never whichever fault
// a single walk would have met first.

func TestNameLegalityIsReportedBeforeMemberResolution(t *testing.T) {
	expectPanic(t, `command "cmd": constraint name "BAD" must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(StringFlag("aa", "the a", Optional())),
			WithConstraints(AllOrNone("BAD", Member("aa"), Member("ghost"))))
	})
}

func TestMemberResolutionIsReportedBeforeElectionLegality(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "sel" references unknown member "ghost"`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				BoolFlag("all", "everything", Default(false)),
				StringFlag("aa", "the a", Optional()),
			),
			WithConstraints(AllOrNone("sel", Member("all"), Member("ghost"))))
	})
}

func TestElectionLegalityIsReportedBeforePresenceLegality(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "sel" member '--aa' declares WhenTrue(), which needs a bool; '--aa' is a str`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("aa", "the a", Optional()),
				StringFlag("bb", "the b", Required()),
			),
			WithConstraints(AllOrNone("sel", Member("aa", WhenTrue()), Member("bb"))))
	})
}

// The dependency families' own guards are a TRAILING phase after pass 7, so a
// member declaring Required() outranks a same-flag `Requires` over operands
// that both resolve (§26.8). Only the phase boundary is under test here: the
// same-flag state is otherwise legal to reach.
func TestPresenceLegalityIsReportedBeforeTheSameFlagGuard(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "sel" member '--bb' declares Required(): a member the invocation must always supply leaves the constraint nothing to decide`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("aa", "the a", Optional()),
				StringFlag("bb", "the b", Required()),
			),
			WithConstraints(
				AllOrNone("sel", Member("aa"), Member("bb")),
				Requires("rr", "aa", "aa"),
			))
	})
}

// The same boundary for the other guard in the trailing phase: a non-bool
// `Implies` trigger is read after pass 7 has refused the required member.
func TestPresenceLegalityIsReportedBeforeTheImpliesTriggerGuard(t *testing.T) {
	expectPanic(t, `command "cmd": constraint "sel" member '--bb' declares Required(): a member the invocation must always supply leaves the constraint nothing to decide`, func() {
		simpleApp("cmd", "a command", "ok",
			WithFlags(
				StringFlag("aa", "the a", Optional()),
				StringFlag("bb", "the b", Required()),
				BoolFlag("tt", "the target", Default(false)),
			),
			WithConstraints(
				AllOrNone("sel", Member("aa"), Member("bb")),
				Implies("ii", "aa", "tt", true),
			))
	})
}

// --- Parse time: the two violation sentences (§12.15) ---

func purgeApp() *App {
	return simpleApp("purge", "purge things", "targets={targets} older={older_than} all={all}",
		WithFlags(
			StringFlag("older-than", "purge items older than duration", Optional()),
			StringFlag("larger-than", "only purge items larger than this size", Optional()),
			BoolFlag("all", "select all archived items", Default(false)),
		),
		WithArgs(NewArg("targets", "record UUIDs or numeric database IDs", Variadic(), ArgOptional())),
		WithConstraints(
			AtLeastOne("purge-selection",
				Member("targets", WhenNonEmpty()),
				Member("older-than"),
				Member("larger-than"),
				Member("all", WhenTrue()),
			),
		))
}

// saferm's `purge`, migrated (§26.7): one at-least-one over a variadic arg and
// three flags, with the hand guard deleted.
func TestAtLeastOneOverAnArgAndThreeFlags(t *testing.T) {
	r := purgeApp().Test([]string{"purge"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d; stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: constraint \"purge-selection\": at least one of targets, --older-than, --larger-than, --all is required\ntry 'myapp purge --help'\n"
	if r.Stderr != want {
		t.Fatalf("stderr = %q, want %q", r.Stderr, want)
	}
	// A positional token engages the variadic arg member.
	if r := purgeApp().Test([]string{"purge", "abc"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	// A flag engages it too, and members MAY co-occur: at-least-one has no
	// upper bound and never refuses a second member.
	if r := purgeApp().Test([]string{"purge", "abc", "--older-than", "3d"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
}

// `all` declares WhenTrue(), which is A1 as a declaration: `--no-all` engages
// nothing, and §12.15's decline clause is what tells the operator so.
func TestAtLeastOneDeclineClause(t *testing.T) {
	r := purgeApp().Test([]string{"purge", "--no-all"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: constraint \"purge-selection\": at least one of targets, --older-than, --larger-than, --all is required (--no-all declines an option; it does not choose one)\ntry 'myapp purge --help'\n"
	if r.Stderr != want {
		t.Fatalf("stderr = %q, want %q", r.Stderr, want)
	}
	// `--all` itself engages the constraint.
	if r := purgeApp().Test([]string{"purge", "--all"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
}

// The clause searches DIRECT members only. It teaches about the sentence it is
// appended to, and that sentence lists the PARENT's operands: a `--no-<x>` two
// levels down names a token the reader is not looking at, and a decline inside a
// nested group leaves that group vacuous rather than violated, so the fact the
// clause would teach is not the fact the parent reports (§12.15, §18.31 item
// 290).
func TestDeclineClauseSearchesDirectMembersOnly(t *testing.T) {
	app := func() *App {
		return simpleApp("cmd", "a command", "ok",
			WithFlags(
				BoolFlag("all", "everything", Default(false)),
				StringFlag("scope", "the scope", Optional()),
				StringFlag("older-than", "a duration", Optional()),
			),
			WithConstraints(
				AllOrNone("inner", Member("all", WhenTrue()), Member("scope")),
				AtLeastOne("outer", Member("inner"), Member("older-than")),
			))
	}
	r := app().Test([]string{"cmd", "--no-all"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: constraint \"outer\": at least one of (--all with --scope), --older-than is required\ntry 'myapp cmd --help'\n"
	if r.Stderr != want {
		t.Fatalf("stderr = %q, want %q", r.Stderr, want)
	}
	// The same declined bool as a DIRECT member of the violated constraint does
	// carry the clause: the token is in the sentence the reader is looking at.
	direct := simpleApp("cmd", "a command", "ok",
		WithFlags(
			BoolFlag("all", "everything", Default(false)),
			StringFlag("older-than", "a duration", Optional()),
		),
		WithConstraints(AtLeastOne("outer", Member("all", WhenTrue()), Member("older-than"))))
	r = direct.Test([]string{"cmd", "--no-all"})
	want = "error: constraint \"outer\": at least one of --all, --older-than is required (--no-all declines an option; it does not choose one)\ntry 'myapp cmd --help'\n"
	if r.Stderr != want {
		t.Fatalf("stderr = %q, want %q", r.Stderr, want)
	}
}

// There is deliberately no analogous clause for an EMPTY WhenNonEmpty() member:
// A2 places empty-value legality on the flag's own value validation, never on
// the layer above it (§12.15).
func TestNoDeclineClauseForAnEmptyNonEmptyMember(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			ListFlag(TypeStr, "tag", "a tag", Unique(true), Optional()),
			StringFlag("older-than", "a duration", Optional()),
		),
		WithConstraints(AtLeastOne("sel", Member("tag", WhenNonEmpty()), Member("older-than"))))
	r := app.Test([]string{"cmd"})
	want := "error: constraint \"sel\": at least one of --tag, --older-than is required\ntry 'myapp cmd --help'\n"
	if r.Stderr != want {
		t.Fatalf("stderr = %q, want %q", r.Stderr, want)
	}
}

// An empty string IS provided, so `present` engages on it: refusing `""` is the
// flag's own validation's job (§26.7's second pin).
func TestPresentEngagesOnAnEmptyString(t *testing.T) {
	if r := purgeApp().Test([]string{"purge", "--older-than", ""}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
}

// --- safegit's `author rewrite`: at-least-one over two all-or-none pairs ---

func authorRewriteApp() *App {
	return simpleApp("rewrite", "rewrite author identity across history", "ok",
		WithFlags(
			StringFlag("old-name", "current author or committer display name", Optional()),
			StringFlag("new-name", "new display name", Optional()),
			StringFlag("old-email", "current author or committer email address", Optional()),
			StringFlag("new-email", "new email address", Optional()),
		),
		WithConstraints(
			AllOrNone("author-name", Member("old-name"), Member("new-name")),
			AllOrNone("author-email", Member("old-email"), Member("new-email")),
			AtLeastOne("author-change", Member("author-name"), Member("author-email")),
		))
}

// Two vacuous pairs leave the parent unsatisfied: a nested member counts toward
// its parent ONLY when engaged (§26.4), which is safegit's hand guard expressed.
func TestNestedVacuousPairsLeaveTheParentUnsatisfied(t *testing.T) {
	r := authorRewriteApp().Test([]string{"rewrite"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: constraint \"author-change\": at least one of (--old-name with --new-name), (--old-email with --new-email) is required\ntry 'myapp rewrite --help'\n"
	if r.Stderr != want {
		t.Fatalf("stderr = %q, want %q", r.Stderr, want)
	}
}

// Children before parents: an operator who typed one half of a pair is told the
// PAIR is incomplete, not that the whole selection is missing (§26.4).
func TestAViolatedChildReportsBeforeItsParent(t *testing.T) {
	r := authorRewriteApp().Test([]string{"rewrite", "--old-name", "old"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: constraint \"author-name\": --old-name, --new-name must be used together\ntry 'myapp rewrite --help'\n"
	if r.Stderr != want {
		t.Fatalf("stderr = %q, want %q", r.Stderr, want)
	}
}

func TestOneCompletePairSatisfiesTheParent(t *testing.T) {
	r := authorRewriteApp().Test([]string{"rewrite", "--old-email", "a@b", "--new-email", "c@d"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
}

// --- Engagement, vacuity and the pipeline position (§26.4) ---

func TestVacuousAllOrNoneIsSatisfied(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			StringFlag("aa", "the a", Optional()),
			StringFlag("bb", "the b", Optional()),
		),
		WithConstraints(AllOrNone("pair", Member("aa"), Member("bb"))))
	if r := app.Test([]string{"cmd"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
}

// A declared default is not a provision, so it cannot engage a member: the
// constraints run BEFORE defaults are applied (§26.4).
func TestADefaultNeverEngagesAMember(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			StringFlag("aa", "the a", Default("xx")),
			StringFlag("bb", "the b", Optional()),
		),
		WithConstraints(AtLeastOne("sel", Member("aa"), Member("bb"))))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("a default must not engage the constraint; got exit %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, `constraint "sel": at least one of --aa, --bb is required`) {
		t.Fatalf("got %q", r.Stderr)
	}
}

// An IMPLIED value can engage one, because `Implies` resolves first (§26.4).
func TestAnImpliedValueEngagesAMember(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			BoolFlag("trigger", "the trigger", Default(false)),
			BoolFlag("target", "the target", Optional()),
			StringFlag("other", "the other", Optional()),
		),
		WithConstraints(
			Implies("trigger-implies-target", "trigger", "target", true),
			AtLeastOne("sel", Member("target", WhenTrue()), Member("other")),
		))
	if r := app.Test([]string{"cmd", "--trigger"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r := app.Test([]string{"cmd"}); r.ExitCode != 1 {
		t.Fatalf("expected exit 1 without the trigger, got %d", r.ExitCode)
	}
}

// Siblings evaluate in DECLARATION order (§26.4).
func TestSiblingsEvaluateInDeclarationOrder(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			StringFlag("aa", "the a", Optional()),
			StringFlag("bb", "the b", Optional()),
			StringFlag("cc", "the c", Optional()),
			StringFlag("dd", "the d", Optional()),
		),
		WithConstraints(
			AllOrNone("first", Member("aa"), Member("bb")),
			AllOrNone("second", Member("cc"), Member("dd")),
		))
	r := app.Test([]string{"cmd", "--aa", "1", "--cc", "2"})
	if !strings.Contains(r.Stderr, `constraint "first"`) {
		t.Fatalf("expected the first constraint to report, got %q", r.Stderr)
	}
}

// --- Arg-side provided-ness at both doors (§26.3) ---

func TestArgMemberProvidednessAtTheMachineDoor(t *testing.T) {
	app := purgeApp()
	if ir := app.invoke("purge", map[string]interface{}{"targets": []interface{}{"one"}}); ir.err != "" {
		t.Fatalf("a supplied variadic must engage the member, got %q", ir.err)
	}
	// An explicitly supplied EMPTY array is not a provision.
	ir := app.invoke("purge", map[string]interface{}{"targets": []interface{}{}})
	if !strings.Contains(ir.err, `constraint "purge-selection": at least one of targets, --older-than, --larger-than, --all is required`) {
		t.Fatalf("an empty array must not engage the member, got %q", ir.err)
	}
	// The key's absence is the same answer.
	ir = app.invoke("purge", map[string]interface{}{})
	if !strings.Contains(ir.err, `constraint "purge-selection"`) {
		t.Fatalf("expected the violation, got %q", ir.err)
	}
}

func TestNonVariadicArgMemberEngagesOnItsToken(t *testing.T) {
	newApp := func() *App {
		return simpleApp("cmd", "a command", "ok",
			WithFlags(StringFlag("aa", "the a", Optional())),
			WithArgs(NewArg("name", "the name", ArgOptional())),
			WithConstraints(AllOrNone("pair", Member("name"), Member("aa"))))
	}
	r := newApp().Test([]string{"cmd", "nn"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	want := "error: constraint \"pair\": name, --aa must be used together\ntry 'myapp cmd --help'\n"
	if r.Stderr != want {
		t.Fatalf("stderr = %q, want %q", r.Stderr, want)
	}
	if r := newApp().Test([]string{"cmd", "nn", "--aa", "1"}); r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r := newApp().Test([]string{"cmd"}); r.ExitCode != 0 {
		t.Fatalf("the vacuous case must be satisfied, got exit %d", r.ExitCode)
	}
}

// --- A selector as a member (§26.2) ---
//
// A token-spelled choice flag is an ordinary root-scope flag here. It engages
// when the INVOCATION elected it and not when a default election did, which is
// §18.28 item 264's answer read through §26.4's predicate.

func selectorMemberApp() *App {
	return simpleApp("cmd", "a command", "ok",
		WithFlags(
			ChoiceFlag("via", "delivery channel", Default("email"),
				Choice("email", "an email message"),
				Choice("sms", "a text message")),
			StringFlag("note", "a note", Optional()),
		),
		WithConstraints(AtLeastOne("sel", Member("via"), Member("note"))))
}

func TestASelectorEngagesOnlyWhenTheInvocationElectedIt(t *testing.T) {
	r := selectorMemberApp().Test([]string{"cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("a DEFAULT election must not engage the member; got exit %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, `constraint "sel": at least one of --via, --note is required`) {
		t.Fatalf("got %q", r.Stderr)
	}
	if r := selectorMemberApp().Test([]string{"cmd", "--via", "sms"}); r.ExitCode != 0 {
		t.Fatalf("an elected selector must engage the member; got exit %d, stderr=%q", r.ExitCode, r.Stderr)
	}
}

// --- `Requires` and `Implies` keep their sentences after the prefix (§26.13) ---

func TestRequiresViolationCarriesTheConstraintPrefix(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			StringFlag("output", "the output", Optional()),
			StringFlag("format", "the format", Optional()),
		),
		WithConstraints(Requires("output-needs-format", "output", "format")))
	r := app.Test([]string{"cmd", "--output", "f.txt"})
	want := "error: constraint \"output-needs-format\": flag '--output' requires '--format'\ntry 'myapp cmd --help'\n"
	if r.Stderr != want {
		t.Fatalf("stderr = %q, want %q", r.Stderr, want)
	}
}

func TestImpliesConflictCarriesTheConstraintPrefix(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			BoolFlag("fast", "fast mode", Default(false)),
			BoolFlag("embeddings", "embeddings", Default(false)),
		),
		WithConstraints(Implies("fast-declines-embeddings", "fast", "embeddings", false)))
	r := app.Test([]string{"cmd", "--fast", "--embeddings"})
	want := "error: constraint \"fast-declines-embeddings\": flag '--fast' implies '--no-embeddings', but '--embeddings' was explicitly provided\ntry 'myapp cmd --help'\n"
	if r.Stderr != want {
		t.Fatalf("stderr = %q, want %q", r.Stderr, want)
	}
}

// --- Help rendering (§26.10) ---

func TestHelpRendersTheConstraintsBlock(t *testing.T) {
	app := authorRewriteApp()
	r := app.Test([]string{"rewrite", "--help"})
	want := `Constraints:
  author-name      all or none of --old-name, --new-name
  author-email     all or none of --old-email, --new-email
  author-change    at least one of (--old-name with --new-name), (--old-email with --new-email)`
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("help = %q, want it to contain %q", r.Stdout, want)
	}
	// The block's own alignment column: it never shares the flag block's.
	if !strings.Contains(r.Stdout, "  --old-name <str>     current author or committer display name [optional]") {
		t.Fatalf("the flag block lost its own column: %q", r.Stdout)
	}
}

func TestHelpRendersEveryFamilysSentence(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			StringFlag("aa", "the a", Optional()),
			StringFlag("bb", "the b", Optional()),
			BoolFlag("trigger", "the trigger", Default(false)),
			BoolFlag("target", "the target", Optional()),
		),
		WithConstraints(
			AtLeastOne("one-of", Member("aa"), Member("bb")),
			Requires("a-needs-b", "aa", "bb"),
			Implies("trigger-declines", "trigger", "target", false),
		))
	r := app.Test([]string{"cmd", "--help"})
	want := `Constraints:
  one-of              at least one of --aa, --bb
  a-needs-b           --aa requires --bb
  trigger-declines    --trigger implies --no-target`
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("help = %q, want it to contain %q", r.Stdout, want)
	}
}

// --- Schema encoding (§25.7's rewritten catalogue, §26.11) ---

func TestSchemaEncodesMembersAndNesting(t *testing.T) {
	chdirTemp(t)
	app := authorRewriteApp()
	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}
	cmd := schema["commands"].(map[string]interface{})["rewrite"].(map[string]interface{})
	constraints := cmd["constraints"].([]interface{})
	if len(constraints) != 3 {
		t.Fatalf("expected 3 constraints, got %d", len(constraints))
	}
	first := constraints[0].(map[string]interface{})
	if first["type"] != "all_or_none" || first["name"] != "author-name" {
		t.Fatalf("unexpected first entry %v", first)
	}
	// A nested constraint is encoded as a constraint-kind member rather than
	// flattened into leaves, and carries NO `when`.
	parent := constraints[2].(map[string]interface{})
	members := parent["members"].([]interface{})
	m0 := members[0].(map[string]interface{})
	if m0["kind"] != "constraint" || m0["name"] != "author-name" {
		t.Fatalf("unexpected nested member %v", m0)
	}
	if _, ok := m0["when"]; ok {
		t.Fatalf("a constraint member must not carry `when`: %v", m0)
	}
}

// `when` is ALWAYS emitted on a flag or arg member, in the pinned key order
// kind, name, when (§25.7's amendment).
func TestSchemaMemberKeyOrderAndAlwaysEmittedWhen(t *testing.T) {
	chdirTemp(t)
	app := purgeApp()
	text := dumpText(t, app)
	want := `          "members": [
            {
              "kind": "arg",
              "name": "targets",
              "when": "non_empty"
            },
            {
              "kind": "flag",
              "name": "older-than",
              "when": "present"
            },
            {
              "kind": "flag",
              "name": "larger-than",
              "when": "present"
            },
            {
              "kind": "flag",
              "name": "all",
              "when": "true"
            }
          ]`
	if !strings.Contains(text, want) {
		t.Fatalf("dump = %s\nwant it to contain %s", text, want)
	}
}

// --- The MCP projection (§26.12) ---

func TestAtLeastOneProjectsAnyOfBranches(t *testing.T) {
	app := purgeApp()
	schema := app.JsonSchema("purge")
	branches, ok := schema["anyOf"].([]interface{})
	if !ok {
		t.Fatalf("expected an anyOf, got %v", schema["anyOf"])
	}
	got, _ := json.Marshal(branches)
	want := `[{"required":["targets"]},{"required":["older_than"]},{"required":["larger_than"]},{"required":["all"]}]`
	if string(got) != want {
		t.Fatalf("anyOf = %s, want %s", got, want)
	}
}

// safegit's site projects with NO loss at all: a nested all-or-none becomes ONE
// branch listing its leaves.
// One object schema carries one `anyOf`, so a command declaring TWO at-least-one
// constraints emits a conjunction of them: merging their branches would publish
// "satisfy either rule" where the declaration says both must hold, and dropping
// one is the silent omission §26.12 forbids (§18.31 item 284).
func TestTwoAtLeastOnesWrapInAllOf(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			StringFlag("aa", "the a", Optional()),
			StringFlag("bb", "the b", Optional()),
			StringFlag("cc", "the c", Optional()),
			StringFlag("dd", "the d", Optional()),
		),
		WithConstraints(
			AtLeastOne("first", Member("aa"), Member("bb")),
			AtLeastOne("second", Member("cc"), Member("dd")),
		))
	schema := app.JsonSchema("cmd")
	if _, present := schema["anyOf"]; present {
		t.Fatalf("two at-least-one constraints emit no bare anyOf, got %v", schema["anyOf"])
	}
	got, _ := json.Marshal(schema["allOf"])
	want := `[{"anyOf":[{"required":["aa"]},{"required":["bb"]}]},{"anyOf":[{"required":["cc"]},{"required":["dd"]}]}]`
	if string(got) != want {
		t.Fatalf("allOf = %s, want %s", got, want)
	}
}

func TestNestedAllOrNoneBecomesOneAnyOfBranch(t *testing.T) {
	app := authorRewriteApp()
	schema := app.JsonSchema("rewrite")
	got, _ := json.Marshal(schema["anyOf"])
	want := `[{"required":["old_name","new_name"]},{"required":["old_email","new_email"]}]`
	if string(got) != want {
		t.Fatalf("anyOf = %s, want %s", got, want)
	}
	// The two pairs themselves project dependentRequired, exactly.
	dep, _ := json.Marshal(schema["dependentRequired"])
	wantDep := `{"new_email":["old_email"],"new_name":["old_name"],"old_email":["new_email"],"old_name":["new_name"]}`
	if string(dep) != wantDep {
		t.Fatalf("dependentRequired = %s, want %s", dep, wantDep)
	}
}

func TestRequiresProjectsDependentRequired(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			StringFlag("output", "the output", Optional()),
			StringFlag("format", "the format", Optional()),
		),
		WithConstraints(Requires("output-needs-format", "output", "format")))
	schema := app.JsonSchema("cmd")
	got, _ := json.Marshal(schema["dependentRequired"])
	if string(got) != `{"output":["format"]}` {
		t.Fatalf("dependentRequired = %s", got)
	}
}

// The description block: every constraint has a line, and a PARTIAL projection
// states its remainder from the closed reason set.
func TestToolDescriptionNamesEveryConstraint(t *testing.T) {
	app := purgeApp()
	tools := app.AsTools()
	var desc string
	for _, tool := range tools {
		if tool.Name == "purge" {
			desc = tool.Description
		}
	}
	want := "Constraints (enforced at call time):\n" +
		`  at least one of: targets, older_than, larger_than, all -- not expressed in the schema: the "true" and "non_empty" selectors`
	if !strings.Contains(desc, want) {
		t.Fatalf("description = %q, want it to contain %q", desc, want)
	}
}

func TestToolDescriptionExactProjectionHasNoClause(t *testing.T) {
	app := authorRewriteApp()
	tools := app.AsTools()
	var desc string
	for _, tool := range tools {
		if tool.Name == "rewrite" {
			desc = tool.Description
		}
	}
	want := "Constraints (enforced at call time):\n" +
		"  all or none of: old_name, new_name\n" +
		"  all or none of: old_email, new_email\n" +
		"  at least one of: (old_name with new_name), (old_email with new_email)"
	if !strings.Contains(desc, want) {
		t.Fatalf("description = %q, want it to contain %q", desc, want)
	}
	if strings.Contains(desc, "not expressed in the schema") {
		t.Fatalf("an exact projection appends no clause: %q", desc)
	}
}

// `implies` injects a value rather than constraining the input, so it projects
// nothing and states the whole rule in words; a nested all-or-none states its
// grouping.
func TestPartialProjectionReasons(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			BoolFlag("trigger", "the trigger", Default(false)),
			BoolFlag("target", "the target", Optional()),
			StringFlag("aa", "the a", Optional()),
			StringFlag("bb", "the b", Optional()),
		),
		WithConstraints(
			AllOrNone("inner", Member("aa"), Member("bb")),
			AllOrNone("outer", Member("inner"), Member("target", WhenTrue())),
			Implies("trigger-implies-target", "trigger", "target", true),
		))
	tools := app.AsTools()
	desc := tools[0].Description
	if !strings.Contains(desc, "  all or none of: (aa with bb), target -- not expressed in the schema: the nested grouping") {
		t.Fatalf("expected the nested-grouping reason, got %q", desc)
	}
	if !strings.Contains(desc, "  trigger implies target = true -- not expressed in the schema: the injection") {
		t.Fatalf("expected the injection reason, got %q", desc)
	}
}

// The two dependency families' MCP sentences, in property names: no `--` prefix
// and no `--no-` spelling, because the caller writes keys rather than argv, and
// a FALSE value is a value rather than a name -- `no_<target>` would describe a
// key the schema does not carry (§26.12, §18.31 item 283).
func TestDependencySentencesInThePropertyNameBlock(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok",
		WithFlags(
			BoolFlag("fast-path", "the trigger", Default(false)),
			BoolFlag("embeddings", "the target", Optional()),
			StringFlag("sign-with", "the signer", Optional()),
			StringFlag("key-id", "the key", Optional()),
		),
		WithConstraints(
			Requires("signing", "sign-with", "key-id"),
			Implies("fast-path-implies", "fast-path", "embeddings", false),
		))
	desc := app.AsTools()[0].Description
	want := "Constraints (enforced at call time):\n" +
		"  sign_with requires key_id\n" +
		"  fast_path implies embeddings = false -- not expressed in the schema: the injection"
	if !strings.Contains(desc, want) {
		t.Fatalf("description = %q, want it to contain %q", desc, want)
	}
}

// Enforcement at call time is unchanged and TOTAL: every constraint is
// evaluated at the machine doors exactly as at the argv door (§26.12).
func TestConstraintsAreEnforcedAtTheMachineDoor(t *testing.T) {
	app := authorRewriteApp()
	ir := app.invoke("rewrite", map[string]interface{}{"old_name": "old"})
	if !strings.Contains(ir.err, `constraint "author-name": --old-name, --new-name must be used together`) {
		t.Fatalf("got %q", ir.err)
	}
}
