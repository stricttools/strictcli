package strictcli

import (
	"fmt"
	"strings"
)

// The constraint system (contract §26).
//
// Two co-occurrence families -- at-least-one and all-or-none -- plus the two
// one-way dependency rules `Requires` and `Implies`, all four declared through
// `WithConstraints`, all four carrying a mandatory name.
//
// Exactly-one is NOT here and cannot return: every exactly-one shape is a
// selector (§24), and this file contains no cardinality parameter, no min/max
// pair and no numeric interior through which an upper bound could be
// reintroduced (§26.1, §26.14). at-least-one has no upper bound at all: engaging
// two members, or all of them, satisfies it exactly as engaging one does, and it
// is never described as exclusivity.

// --- Families and election selectors ---

type constraintFamily int

const (
	familyAtLeastOne constraintFamily = iota
	familyAllOrNone
	familyRequires
	familyImplies
)

// schemaType is the `type` value the schema catalogue publishes (§25.7).
func (fam constraintFamily) schemaType() string {
	switch fam {
	case familyAtLeastOne:
		return "at_least_one"
	case familyAllOrNone:
		return "all_or_none"
	case familyRequires:
		return "requires"
	default:
		return "implies"
	}
}

// coOccurrence reports the two families that take members and can be nested.
// `Requires` and `Implies` are rules rather than co-occurrence predicates, and
// "engaged" has no meaning for them (§26.2).
func (fam constraintFamily) coOccurrence() bool {
	return fam == familyAtLeastOne || fam == familyAllOrNone
}

// connector joins a nested constraint's own operands when it renders inside a
// sentence: ` with ` for all-or-none, ` or ` for at-least-one (§12.15).
func (fam constraintFamily) connector() string {
	if fam == familyAllOrNone {
		return " with "
	}
	return " or "
}

// whenSelector is the closed three-value election vocabulary (§26.3). It
// replaces the type-dispatched rules the parser used to apply on its own: what
// counts as "chosen" is a declaration, never parser lore.
type whenSelector int

const (
	whenPresentSel whenSelector = iota
	whenTrueSel
	whenNonEmptySel
)

// schemaWord is the value the schema publishes for a member's `when` (§25.7).
func (w whenSelector) schemaWord() string {
	switch w {
	case whenTrueSel:
		return "true"
	case whenNonEmptySel:
		return "non_empty"
	default:
		return "present"
	}
}

// --- The declaration surface (§26.6) ---

// Constraint is a declared rule over a command's flags, args and other
// constraints. The interface is closed: the only values that satisfy it come
// from AtLeastOne, AllOrNone, Requires and Implies, so a struct literal cannot
// declare a constraint and therefore cannot declare a half-formed one.
type Constraint interface{ isConstraint() }

// ConstraintMember is one operand of a co-occurrence constraint, produced by
// Member. Its fields are unexported for the reason the Constraint interface is
// closed: a member carries an election, and a literal could omit it.
type ConstraintMember struct {
	name    string
	when    whenSelector
	whenSet bool
}

// MemberOption declares a member's election selector.
type MemberOption func(*ConstraintMember)

// Member references a flag, a positional arg or another named constraint of the
// same command. A member declares no presence and no help of its own: it is a
// reference, and the declaration it names carries every fact about the value.
//
// The default election is WhenPresent(); a BOOL member must declare its
// election explicitly (§26.3), because `present` on a bool means `--no-x`
// engages a constraint while selecting nothing.
func Member(name string, opts ...MemberOption) ConstraintMember {
	m := ConstraintMember{name: name, when: whenPresentSel}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

// WhenPresent engages the member when its value was PROVIDED -- cli, env, config
// or implied, never a default and never infra (§23.6).
func WhenPresent() MemberOption {
	return func(m *ConstraintMember) { m.when = whenPresentSel; m.whenSet = true }
}

// WhenTrue engages the member when it was provided AND resolves to true. Legal
// on bool declarations only.
func WhenTrue() MemberOption {
	return func(m *ConstraintMember) { m.when = whenTrueSel; m.whenSet = true }
}

// WhenNonEmpty engages the member when it was provided AND resolves to a
// non-empty string, list or map. Legal on strings and collections only.
func WhenNonEmpty() MemberOption {
	return func(m *ConstraintMember) { m.when = whenNonEmptySel; m.whenSet = true }
}

// constraintDecl is the one internal representation of all four kinds. There is
// deliberately no Group(min, max): the two co-occurrence families are two
// different predicates rather than two points on a cardinality axis (§26.1).
type constraintDecl struct {
	family    constraintFamily
	name      string
	members   []ConstraintMember
	flag      string // Requires / Implies
	dependsOn string // Requires
	implies   string // Implies
	value     bool   // Implies

	// resolved is filled at registration, in members' declaration order.
	resolved []resolvedMember
}

func (constraintDecl) isConstraint() {}

// AtLeastOne declares that at least one member must be engaged. Members MAY
// co-occur: it has no upper bound and never refuses a second member.
//
// Two named members precede the variadic tail, so a one-member constraint is a
// COMPILE error. A caller holding a slice writes
// AtLeastOne(n, ms[0], ms[1], ms[2:]...), which fails at the caller rather than
// inside the framework.
func AtLeastOne(name string, a, b ConstraintMember, rest ...ConstraintMember) Constraint {
	return constraintDecl{family: familyAtLeastOne, name: name, members: append([]ConstraintMember{a, b}, rest...)}
}

// AllOrNone declares that either every member is engaged or none is. With
// nothing engaged it is vacuously satisfied -- that is the "none" half of its
// own name.
func AllOrNone(name string, a, b ConstraintMember, rest ...ConstraintMember) Constraint {
	return constraintDecl{family: familyAllOrNone, name: name, members: append([]ConstraintMember{a, b}, rest...)}
}

// Requires declares that providing one flag requires another to be provided.
// Its operands are flags only, by name, at root scope: it takes no arg, no
// nested constraint and no election selector (§26.13).
func Requires(name, flag, dependsOn string) Constraint {
	return constraintDecl{family: familyRequires, name: name, flag: flag, dependsOn: dependsOn}
}

// Implies declares that providing one bool flag automatically sets another bool
// flag to a value. An explicitly contradicting value for the target is a parse
// error. Operand vocabulary is `Requires`'s.
func Implies(name, flag, implies string, value bool) Constraint {
	return constraintDecl{family: familyImplies, name: name, flag: flag, implies: implies, value: value}
}

// --- Resolved members ---

type memberKind int

const (
	memberKindFlag memberKind = iota
	memberKindArg
	memberKindConstraint
)

func (k memberKind) schemaWord() string {
	switch k {
	case memberKindArg:
		return "arg"
	case memberKindConstraint:
		return "constraint"
	default:
		return "flag"
	}
}

// resolvedMember is a member name after registration resolved it against the
// command's one namespace. The resolved kind is what the schema publishes, so a
// consumer never has to search the flag and arg lists to learn what a name
// refers to (§26.11).
type resolvedMember struct {
	kind memberKind
	name string
	when whenSelector
	// idx indexes cmd.flags, cmd.args or cmd.constraints by kind. A flag that
	// resolved inside a choice scope carries -1 and a scopePath, and is refused
	// by the scope pass.
	idx       int
	scopePath string
}

// --- Registration (§26.8) ---
//
// The passes run in the pinned order over the whole constraint set, so three
// implementations report the same FIRST error for a declaration with two
// faults. The order runs from the constraint's own identity outward to the
// declarations it names, so a message never blames a member for a fault in the
// constraint that names it.
func validateConstraints(cmdName string, cmd *Command) {
	// Pass 1: name legality -- charset, duplicates, collision with a flag or
	// arg name. The collision refusal is what keeps a bare member name total.
	byName := make(map[string]int, len(cmd.constraints))
	for i := range cmd.constraints {
		c := &cmd.constraints[i]
		if !isKebabName(c.name) {
			panic(errConstraintNameCharset(cmdName, c.name))
		}
		if _, dup := byName[c.name]; dup {
			panic(errConstraintNameDuplicate(cmdName, c.name))
		}
		if cmd.rootFlagIndex(c.name) >= 0 || cmd.argIndex(c.name) >= 0 {
			panic(errConstraintNameCollides(cmdName, c.name))
		}
		byName[c.name] = i
	}

	// Pass 2: member arity. Go-EXCLUDED -- AtLeastOne and AllOrNone take two
	// named members before the variadic tail, so a one-member constraint does
	// not compile and errConstraintMinMembers has no reachable state here.

	// Pass 3: member resolution. Every name resolves to exactly one flag, arg
	// or constraint; unknown, ambiguous and duplicated names refuse here.
	for i := range cmd.constraints {
		c := &cmd.constraints[i]
		if !c.family.coOccurrence() {
			// Requires / Implies: flags only, by name. Their own guards --
			// same-flag operands and the two bool-operand rules -- are the
			// TRAILING phase below, so an operand naming nothing is refused as
			// unknown here first.
			cmd.resolveConstraintFlag(cmdName, c.name, c.flag)
			if c.family == familyRequires {
				cmd.resolveConstraintFlag(cmdName, c.name, c.dependsOn)
			} else {
				cmd.resolveConstraintFlag(cmdName, c.name, c.implies)
			}
			continue
		}
		seen := make(map[string]bool, len(c.members))
		c.resolved = make([]resolvedMember, 0, len(c.members))
		for _, m := range c.members {
			flagIdx := cmd.rootFlagIndex(m.name)
			argIdx := cmd.argIndex(m.name)
			if flagIdx >= 0 && argIdx >= 0 {
				panic(errConstraintMemberAmbiguous(cmdName, c.name, m.name))
			}
			var r resolvedMember
			switch {
			case flagIdx >= 0:
				r = resolvedMember{kind: memberKindFlag, name: m.name, when: m.when, idx: flagIdx}
			case argIdx >= 0:
				r = resolvedMember{kind: memberKindArg, name: m.name, when: m.when, idx: argIdx}
			default:
				if refIdx, ok := byName[m.name]; ok {
					r = resolvedMember{kind: memberKindConstraint, name: m.name, when: m.when, idx: refIdx}
					break
				}
				// A SCOPED flag resolves as a flag and is refused by the scope
				// pass below; reporting it as unknown would name the wrong
				// fault (§24.8).
				if path := cmd.index.scopedFlagPath(m.name); path != nil {
					r = resolvedMember{kind: memberKindFlag, name: m.name, when: m.when, idx: -1, scopePath: renderScopePath(path)}
					break
				}
				panic(errConstraintMemberUnknown(cmdName, c.name, m.name))
			}
			if seen[m.name] {
				panic(errConstraintMemberDuplicate(cmdName, c.name, m.name))
			}
			seen[m.name] = true
			c.resolved = append(c.resolved, r)
		}
	}

	// Pass 4: scope. The scope already IS the constraint, so a constraint
	// naming a flag declared inside one is refused (§24.8).
	for i := range cmd.constraints {
		c := &cmd.constraints[i]
		for _, r := range c.resolved {
			if r.scopePath != "" {
				panic(errConstraintReferencesScopedFlag(cmdName, c.name, r.name, r.scopePath))
			}
		}
	}

	// Pass 5: nesting legality. A nested member is one of the two co-occurrence
	// families, carries no election of its own, and the reference graph is
	// acyclic. Depth is unlimited, as §24.7's nesting is.
	for i := range cmd.constraints {
		c := &cmd.constraints[i]
		for mi, r := range c.resolved {
			if r.kind != memberKindConstraint {
				continue
			}
			if !cmd.constraints[r.idx].family.coOccurrence() {
				panic(errConstraintNestedFamily(cmdName, c.name, r.name))
			}
			if c.members[mi].whenSet {
				panic(errConstraintNestedWhen(cmdName, c.name, r.name))
			}
		}
	}
	cmd.checkConstraintCycles(cmdName)

	// Pass 6: election legality -- `when` against the member's declared type,
	// including the bool refusal. A selector that cannot be evaluated against
	// the declared type is a mis-declaration, not a no-op.
	for i := range cmd.constraints {
		c := &cmd.constraints[i]
		for mi, r := range c.resolved {
			if r.kind == memberKindConstraint {
				continue
			}
			typ, sized, isBool := cmd.memberValueShape(r)
			token := cmd.renderResolvedMember(r)
			if isBool && !c.members[mi].whenSet {
				panic(errConstraintMemberBoolWhen(cmdName, c.name, token))
			}
			switch r.when {
			case whenTrueSel:
				if !isBool {
					panic(errConstraintWhenTrueNotBool(cmdName, c.name, token, typ))
				}
			case whenNonEmptySel:
				if !sized {
					panic(errConstraintWhenNonEmptyNotSized(cmdName, c.name, token, typ))
				}
			}
		}
	}

	// Pass 7: presence legality. A constraint never subtracts from a
	// declaration -- it adds a rule on top of one -- so no member may declare
	// requiredness (§26.5, amending §23.5).
	for i := range cmd.constraints {
		c := &cmd.constraints[i]
		for _, r := range c.resolved {
			switch r.kind {
			case memberKindFlag:
				if cmd.flags[r.idx].presence == presenceRequired {
					panic(errConstraintMemberRequired(cmdName, c.name, cmd.renderResolvedMember(r)))
				}
			case memberKindArg:
				if cmd.args[r.idx].presence == presenceRequired {
					panic(errConstraintMemberRequired(cmdName, c.name, cmd.renderResolvedMember(r)))
				}
			}
		}
	}

	// Trailing phase: the two dependency families' own guards, which predate
	// this round and are about the declarations a rule names rather than about
	// the constraint system. They run AFTER every shared pass, so an operand
	// that names nothing is refused as unknown before a guard reads the name it
	// carries; pass 3 resolved every operand, so each one is a root flag here.
	for i := range cmd.constraints {
		c := &cmd.constraints[i]
		switch c.family {
		case familyRequires:
			if c.flag == c.dependsOn {
				panic(errCommandRequiresSameFlag(cmdName, c.flag))
			}
		case familyImplies:
			if c.flag == c.implies {
				panic(errCommandImpliesSameFlag(cmdName, c.flag))
			}
			if cmd.flags[cmd.rootFlagIndex(c.flag)].Type != TypeBool {
				panic(errCommandImpliesTriggerNotBool(cmdName, c.flag))
			}
			if cmd.flags[cmd.rootFlagIndex(c.implies)].Type != TypeBool {
				panic(errCommandImpliesTargetNotBool(cmdName, c.implies))
			}
		}
	}
}

func (c *Command) rootFlagIndex(name string) int {
	for i := range c.flags {
		if c.flags[i].Name == name {
			return i
		}
	}
	return -1
}

func (c *Command) argIndex(name string) int {
	for i := range c.args {
		if c.args[i].Name == name {
			return i
		}
	}
	return -1
}

// resolveConstraintFlag is the `Requires` / `Implies` operand refusal: unknown
// names keep the flag noun (§26.13), and a scoped operand is refused by the
// scope rule rather than reported as unknown.
func (c *Command) resolveConstraintFlag(cmdName, constraintName, flagName string) {
	if c.rootFlagIndex(flagName) >= 0 {
		return
	}
	if path := c.index.scopedFlagPath(flagName); path != nil {
		panic(errConstraintReferencesScopedFlag(cmdName, constraintName, flagName, renderScopePath(path)))
	}
	panic(errConstraintUnknownFlag(cmdName, constraintName, flagName))
}

// memberValueShape answers the three questions the election guards ask of a
// member's declaration: the framework's own type word, whether the value has a
// size, and whether it is a bool.
//
// A value with a size is never a bool, whatever its element type: a VARIADIC
// bool arg's value is a sequence, so `non_empty` tests it, `true` cannot, and
// omitting `when` is legal -- the mandatory-election refusal exists because
// `present` on a bool lets `--no-x` engage a constraint while selecting nothing,
// and a variadic arg has no negated spelling at all (§26.3). There is no
// flag-side twin: a repeatable bool flag is refused at declaration.
func (c *Command) memberValueShape(r resolvedMember) (typeWord string, sized bool, isBool bool) {
	var t FlagType
	var repeatable bool
	switch r.kind {
	case memberKindFlag:
		f := &c.flags[r.idx]
		t, repeatable = f.Type, f.Repeatable
	case memberKindArg:
		a := &c.args[r.idx]
		t, repeatable = a.Type, a.IsVariadic
	}
	isBool = t == TypeBool && !repeatable
	sized = t == TypeStr || IsCompoundType(t) || repeatable
	return memberTypeWord(t, repeatable), sized, isBool
}

// memberTypeWord renders §12.15's closed `<t>` vocabulary: the four scalar
// words, the collection spellings `list[<elem>]` and `dict[<value>]` formed from
// them, and `choice flag`.
//
// A value with a size renders its COLLECTION spelling, because what the sentence
// describes is the value the selector would be evaluated against; the element
// word alone would name the type of a thing the member does not hold. A declared
// compound type already carries its collection word and is never wrapped twice.
// A selector renders the CONSTRUCT: its value is a record neither selector can
// test, and `choice` alone is the dump's enum spelling, which reads here as a
// value word for a value that does not exist.
func memberTypeWord(t FlagType, repeatable bool) string {
	if t == TypeChoice {
		return "choice flag"
	}
	if repeatable && IsScalarType(t) {
		return "list[" + flagTypeName[t] + "]"
	}
	return flagTypeName[t]
}

// checkConstraintCycles walks the nesting graph depth-first from every
// constraint in declaration order. A constraint naming itself is the degenerate
// case and takes the same template.
func (c *Command) checkConstraintCycles(cmdName string) {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	color := make([]int, len(c.constraints))
	var stack []int
	var walk func(i int)
	walk = func(i int) {
		color[i] = grey
		stack = append(stack, i)
		for _, r := range c.constraints[i].resolved {
			if r.kind != memberKindConstraint {
				continue
			}
			if color[r.idx] == grey {
				// The path starts and ends at the same name, beginning at the
				// FIRST participant in declaration order -- which is not
				// necessarily the one the walk entered the cycle through, so
				// the cycle is rotated onto it (§12.15).
				entry := 0
				for si, node := range stack {
					if node == r.idx {
						entry = si
						break
					}
				}
				cycle := stack[entry:]
				open := 0
				for ci, node := range cycle {
					if node < cycle[open] {
						open = ci
					}
				}
				names := make([]string, 0, len(cycle)+1)
				for _, node := range append(append([]int{}, cycle[open:]...), cycle[:open]...) {
					names = append(names, c.constraints[node].name)
				}
				names = append(names, names[0])
				panic(errConstraintCycle(cmdName, strings.Join(names, " -> ")))
			}
			if color[r.idx] == white {
				walk(r.idx)
			}
		}
		stack = stack[:len(stack)-1]
		color[i] = black
	}
	for i := range c.constraints {
		if color[i] == white {
			walk(i)
		}
	}
}

// --- Member rendering (§12.15) ---
//
// The rendering is STRUCTURAL, never nominal: a nested member renders its own
// operands and never its name. The constraint's name identifies the rule that
// failed and appears once, in the prefix; a member list names tokens the reader
// can type.

// renderResolvedMember renders one member: `--name` for a flag, the bare name
// for an arg, and a parenthesized operand list for a nested constraint.
func (c *Command) renderResolvedMember(r resolvedMember) string {
	switch r.kind {
	case memberKindArg:
		return r.name
	case memberKindConstraint:
		nested := &c.constraints[r.idx]
		parts := make([]string, 0, len(nested.resolved))
		for _, nr := range nested.resolved {
			parts = append(parts, c.renderResolvedMember(nr))
		}
		return "(" + strings.Join(parts, nested.family.connector()) + ")"
	default:
		return "--" + r.name
	}
}

// renderMemberList joins a constraint's members with ", " in declaration order.
func (c *Command) renderMemberList(decl *constraintDecl) string {
	parts := make([]string, 0, len(decl.resolved))
	for _, r := range decl.resolved {
		parts = append(parts, c.renderResolvedMember(r))
	}
	return strings.Join(parts, ", ")
}

// --- Evaluation (§26.4) ---

// argInput answers arg-side provided-ness, which the flag-side sourced store
// does not hold. An arg is provided when the invocation supplied a positional
// token for it, or a key for it at a machine door, and never when a declared
// default or an optional absence filled it. For a VARIADIC arg provided means at
// least one element, which is why an explicitly supplied empty array is not a
// provision (§26.3).
type argInput struct {
	provided map[string]bool
	values   map[string]interface{}
}

func newArgInput(cmd *Command, positionals positionalInput) argInput {
	in := argInput{provided: map[string]bool{}, values: map[string]interface{}{}}
	if positionals.preTyped {
		for i := range cmd.args {
			a := &cmd.args[i]
			raw, ok := positionals.byName[a.Name]
			if !ok {
				continue
			}
			if a.IsVariadic {
				if vals, isList := raw.([]interface{}); isList && len(vals) == 0 {
					continue
				}
			}
			in.provided[a.Name] = true
			in.values[a.Name] = raw
		}
		return in
	}
	posIdx := 0
	for i := range cmd.args {
		a := &cmd.args[i]
		if a.IsVariadic {
			remaining := positionals.tokens[posIdx:]
			if len(remaining) > 0 {
				in.provided[a.Name] = true
				in.values[a.Name] = remaining
			}
			posIdx = len(positionals.tokens)
			continue
		}
		if posIdx < len(positionals.tokens) {
			in.provided[a.Name] = true
			in.values[a.Name] = positionals.tokens[posIdx]
			posIdx++
		}
	}
	return in
}

// constraintEval evaluates one invocation's constraint set.
type constraintEval struct {
	cmd   *Command
	store *sourcedStore
	args  argInput
	// est answers a SELECTOR member's engagement. A selector's elected record
	// only reaches the sourced store when defaults are applied, which is after
	// the constraints run, so its provided-ness is read from the election that
	// produced it (§26.2).
	est *electionState
	// engagedMemo caches a nested constraint's engagement, which propagates
	// upward. Satisfaction never does.
	engagedMemo map[int]bool
}

// evaluateConstraints runs the co-occurrence families and `Requires` in
// declaration order, children before parents. It returns the first violation's
// sentence, or "".
//
// A violated nested constraint reports its own sentence and its parent is never
// evaluated: an operator who typed one half of a pair is told the pair is
// incomplete, not that the whole selection is missing.
func evaluateConstraints(cmd *Command, store *sourcedStore, positionals positionalInput, est *electionState) string {
	if len(cmd.constraints) == 0 {
		return ""
	}
	ev := &constraintEval{cmd: cmd, store: store, args: newArgInput(cmd, positionals), est: est, engagedMemo: map[int]bool{}}
	for i := range cmd.constraints {
		c := &cmd.constraints[i]
		switch {
		case c.family.coOccurrence():
			if errMsg := ev.check(i); errMsg != "" {
				return errMsg
			}
		case c.family == familyRequires:
			if store.isPresentForDeps(c.flag) && !store.isPresentForDeps(c.dependsOn) {
				return errFlagRequiresFlag(c.name, c.flag, c.dependsOn)
			}
		}
	}
	return ""
}

// check evaluates one co-occurrence constraint's subtree, post-order.
func (ev *constraintEval) check(i int) string {
	c := &ev.cmd.constraints[i]
	engagedCount := 0
	for _, r := range c.resolved {
		if r.kind == memberKindConstraint {
			if _, done := ev.engagedMemo[r.idx]; !done {
				if errMsg := ev.check(r.idx); errMsg != "" {
					return errMsg
				}
			}
		}
		if ev.engaged(r) {
			engagedCount++
		}
	}
	ev.engagedMemo[i] = engagedCount > 0
	if c.family == familyAtLeastOne {
		if engagedCount == 0 {
			return errAtLeastOneRequired(c.name, ev.cmd.renderMemberList(c), ev.declineClause(c))
		}
		return ""
	}
	// all-or-none: every member engaged or none. With nothing engaged it is
	// vacuously true -- the "none" half of its own name, not a loophole.
	if engagedCount > 0 && engagedCount < len(c.resolved) {
		return errAllOrNoneTogether(c.name, ev.cmd.renderMemberList(c))
	}
	return ""
}

// engaged answers §26.4's predicate for one member.
func (ev *constraintEval) engaged(r resolvedMember) bool {
	if r.kind == memberKindConstraint {
		if cached, ok := ev.engagedMemo[r.idx]; ok {
			return cached
		}
		// Engagement of a nested member that was never walked (a cycle-free
		// graph always walks it first, so this is the defensive branch).
		engaged := false
		for _, nr := range ev.cmd.constraints[r.idx].resolved {
			if ev.engaged(nr) {
				engaged = true
			}
		}
		ev.engagedMemo[r.idx] = engaged
		return engaged
	}
	provided, value := ev.memberValue(r)
	if !provided {
		return false
	}
	switch r.when {
	case whenTrueSel:
		b, ok := value.(bool)
		return ok && b
	case whenNonEmptySel:
		return isNonEmptyValue(value)
	default:
		return true
	}
}

func (ev *constraintEval) memberValue(r resolvedMember) (bool, interface{}) {
	if r.kind == memberKindArg {
		if !ev.args.provided[r.name] {
			return false, nil
		}
		return true, ev.args.values[r.name]
	}
	// A selector engages when the INVOCATION elected it and not when a default
	// election did, which is §18.28 item 264's answer read through §26.4's
	// predicate. Its value is a record, so `present` is the only election legal
	// on it and there is nothing further to test.
	if f := &ev.cmd.flags[r.idx]; f.Type == TypeChoice {
		if ev.est == nil {
			return false, nil
		}
		ls, ok := ev.est.bySel[f]
		if !ok || ls.unsatisfied || electionSource(ls.origin) == SourceDefault {
			return false, nil
		}
		return true, nil
	}
	if !ev.store.isPresentForDeps(r.name) {
		return false, nil
	}
	v, _ := ev.store.get(r.name)
	return true, v
}

// declineClause appends §21.4's decline clause verbatim when a bool member
// declaring WhenTrue() was provided as false, naming the FIRST such member in
// declaration order. There is deliberately no analogous clause for an empty
// WhenNonEmpty() member: empty-value legality belongs to the flag's own value
// validation, never to the layer above it (§12.15).
//
// The scan runs the violated constraint's own member list and does NOT descend
// into a nested constraint's members. The clause teaches about the sentence it
// is appended to, and that sentence lists the parent's operands: a nested
// constraint renders structurally, so a `--no-<x>` two levels down names a token
// the reader is not looking at, and that nested constraint has its own line and
// its own sentence to carry it. A decline inside a nested group also leaves that
// group vacuous rather than violated (§26.4), so the fact the clause would teach
// is not the fact the parent's violation reports.
func (ev *constraintEval) declineClause(c *constraintDecl) string {
	for _, r := range c.resolved {
		if r.kind == memberKindConstraint || r.when != whenTrueSel {
			continue
		}
		provided, value := ev.memberValue(r)
		if !provided {
			continue
		}
		if b, ok := value.(bool); ok && !b {
			return errMutexDeclineClause(r.name)
		}
	}
	return ""
}

// isNonEmptyValue is the `non_empty` predicate over every shape a provided value
// can take at either door: a raw argv token, a token list, or an already-typed
// value from a machine door.
func isNonEmptyValue(v interface{}) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case []string:
		return len(t) > 0
	case []interface{}:
		return len(t) > 0
	case map[string]interface{}:
		return len(t) > 0
	default:
		return true
	}
}

// --- Help rendering (§26.10) ---

// constraintHelpSentence renders one constraint's rule for the `Constraints:`
// block. A member's presence part is never repeated here: every flag line
// already carries exactly one (§23.8), and a constraint states a rule over
// members rather than a property of one.
func (c *Command) constraintHelpSentence(decl *constraintDecl) string {
	switch decl.family {
	case familyAtLeastOne:
		return "at least one of " + c.renderMemberList(decl)
	case familyAllOrNone:
		return "all or none of " + c.renderMemberList(decl)
	case familyRequires:
		return fmt.Sprintf("--%s requires --%s", decl.flag, decl.dependsOn)
	default:
		if !decl.value {
			return fmt.Sprintf("--%s implies --no-%s", decl.flag, decl.implies)
		}
		return fmt.Sprintf("--%s implies --%s", decl.flag, decl.implies)
	}
}

// --- Schema encoding (§25.7's rewritten catalogue, §26.11) ---
//
// The encoding is COMPLETE rather than indicative: the resolved kind is
// published so no name lookup is needed, and nesting is published as
// constraint-kind members rather than flattened into leaves.
func serializeConstraints(cmd *Command) []interface{} {
	var out []interface{}
	for i := range cmd.constraints {
		c := &cmd.constraints[i]
		entry := newSchemaObject().set("type", c.family.schemaType()).set("name", c.name)
		switch c.family {
		case familyAtLeastOne, familyAllOrNone:
			members := make([]interface{}, 0, len(c.resolved))
			for _, r := range c.resolved {
				m := newSchemaObject().set("kind", r.kind.schemaWord()).set("name", r.name)
				// `when` is ALWAYS emitted on a flag or arg member and NEVER on
				// a constraint member, which is why it takes no `defaults`
				// block entry: the block maps what an omitted key means, and
				// this key is never omitted.
				if r.kind != memberKindConstraint {
					m.set("when", r.when.schemaWord())
				}
				members = append(members, m)
			}
			entry.set("members", members)
		case familyRequires:
			entry.set("flag", c.flag).set("depends_on", c.dependsOn)
		case familyImplies:
			entry.set("flag", c.flag).set("implies", c.implies).set("value", c.value)
		}
		out = append(out, entry)
	}
	return out
}

// --- The MCP projection and its declared lossiness policy (§26.12) ---

// constraintFidelity is one constraint's projection verdict. There are exactly
// two: `exact` (a keyword expresses the rule completely) and `partial` (what can
// be emitted is emitted and the remainder is stated in the tool description).
// There is no third verdict in which a rule reaches the boundary unstated.
type constraintFidelity struct {
	decl   *constraintDecl
	reason string // "" for exact; one of the closed set otherwise
}

const (
	fidelityReasonSelectors = `the "true" and "non_empty" selectors`
	fidelityReasonNesting   = "the nested grouping"
	fidelityReasonInjection = "the injection"
)

// mcpPropertyName is the key a caller writes at a machine door: an underscored
// flag name, or a positional arg's own name.
func (c *Command) mcpPropertyName(r resolvedMember) string {
	if r.kind == memberKindArg {
		return r.name
	}
	return flagParamName(r.name)
}

// leafProperties flattens a co-occurrence constraint to the property names of
// every flag and arg beneath it, in declaration order.
func (c *Command) leafProperties(decl *constraintDecl) []interface{} {
	var out []interface{}
	for _, r := range decl.resolved {
		if r.kind == memberKindConstraint {
			out = append(out, c.leafProperties(&c.constraints[r.idx])...)
			continue
		}
		out = append(out, c.mcpPropertyName(r))
	}
	return out
}

// anyOfBranches builds one branch per member: a nested all-or-none becomes ONE
// branch listing all of its leaves, and a nested at-least-one's branches are
// INLINED into the parent's, both of which are exact.
func (c *Command) anyOfBranches(decl *constraintDecl) []interface{} {
	var out []interface{}
	for _, r := range decl.resolved {
		if r.kind == memberKindConstraint {
			nested := &c.constraints[r.idx]
			if nested.family == familyAtLeastOne {
				out = append(out, c.anyOfBranches(nested)...)
				continue
			}
			out = append(out, map[string]interface{}{"required": c.leafProperties(nested)})
			continue
		}
		out = append(out, map[string]interface{}{"required": []interface{}{c.mcpPropertyName(r)}})
	}
	return out
}

// hasNonPresentSelector reports whether any leaf beneath a constraint declares
// an election `required` cannot express.
func (c *Command) hasNonPresentSelector(decl *constraintDecl) bool {
	for _, r := range decl.resolved {
		if r.kind == memberKindConstraint {
			if c.hasNonPresentSelector(&c.constraints[r.idx]) {
				return true
			}
			continue
		}
		if r.when != whenPresentSel {
			return true
		}
	}
	return false
}

func (c *Command) hasConstraintMember(decl *constraintDecl) bool {
	for _, r := range decl.resolved {
		if r.kind == memberKindConstraint {
			return true
		}
	}
	return false
}

// projectConstraints emits the JSON Schema keywords a command's constraints
// support and returns every constraint's fidelity, in declaration order. The
// keywords sit BESIDE `properties` and `required` on one flat object schema,
// add no structure, and degrade safely: a client that ignores an unknown keyword
// sends a call the framework refuses at call time with the parser's own
// sentence. The runtime refusal is the authority; the schema is advisory.
func projectConstraints(cmd *Command, schema map[string]interface{}) []constraintFidelity {
	var fidelities []constraintFidelity
	var anyOfs []interface{}
	dependentRequired := map[string]interface{}{}

	// The at-least-one-property rule is NOT a constraint (contract §26.14's
	// answer, §27.4) but it borrows this machinery, wrapping pin included: it
	// counts as an `anyOf`-producing rule, and its branch comes FIRST, it being
	// the command's own declaration rather than an entry in the constraint
	// list. Merging it into a constraint's `anyOf` would be the silent
	// weakening item 284 already refused.
	if branches := updateAnyOfBranches(cmd); len(branches) > 0 {
		anyOfs = append(anyOfs, map[string]interface{}{"anyOf": branches})
	}

	addDependent := func(key string, values []interface{}) {
		existing, ok := dependentRequired[key].([]interface{})
		if !ok {
			dependentRequired[key] = values
			return
		}
		for _, v := range values {
			found := false
			for _, e := range existing {
				if e == v {
					found = true
					break
				}
			}
			if !found {
				existing = append(existing, v)
			}
		}
		dependentRequired[key] = existing
	}

	for i := range cmd.constraints {
		c := &cmd.constraints[i]
		switch c.family {
		case familyAtLeastOne:
			anyOfs = append(anyOfs, map[string]interface{}{"anyOf": cmd.anyOfBranches(c)})
			reason := ""
			if cmd.hasNonPresentSelector(c) {
				reason = fidelityReasonSelectors
			}
			fidelities = append(fidelities, constraintFidelity{decl: c, reason: reason})
		case familyAllOrNone:
			// `dependentRequired` cannot carry a group as an operand, and it
			// says a key is PRESENT rather than true or non-empty.
			switch {
			case cmd.hasConstraintMember(c):
				fidelities = append(fidelities, constraintFidelity{decl: c, reason: fidelityReasonNesting})
			case cmd.hasNonPresentSelector(c):
				fidelities = append(fidelities, constraintFidelity{decl: c, reason: fidelityReasonSelectors})
			default:
				props := cmd.leafProperties(c)
				for _, p := range props {
					others := make([]interface{}, 0, len(props)-1)
					for _, q := range props {
						if q != p {
							others = append(others, q)
						}
					}
					addDependent(p.(string), others)
				}
				fidelities = append(fidelities, constraintFidelity{decl: c})
			}
		case familyRequires:
			addDependent(flagParamName(c.flag), []interface{}{flagParamName(c.dependsOn)})
			fidelities = append(fidelities, constraintFidelity{decl: c})
		case familyImplies:
			// It injects a value rather than constraining the input, so there
			// is nothing for a schema to say.
			fidelities = append(fidelities, constraintFidelity{decl: c, reason: fidelityReasonInjection})
		}
	}

	switch len(anyOfs) {
	case 0:
	case 1:
		branch := anyOfs[0].(map[string]interface{})
		schema["anyOf"] = branch["anyOf"]
	default:
		// One object schema can carry one `anyOf`; a conjunction of them is an
		// `allOf`, which keeps every at-least-one exact rather than inventing a
		// third fidelity verdict for the second one.
		schema["allOf"] = anyOfs
	}
	if len(dependentRequired) > 0 {
		schema["dependentRequired"] = dependentRequired
	}
	return fidelities
}

// constraintDescriptionLines renders the tool description's constraint block:
// one line per constraint in declaration order, members in PROPERTY names
// (never CLI tokens -- the caller writes keys, not argv), and a partial
// projection appending its closed-set reason. An exact projection appends no
// clause at all, so the presence of a clause tells a reader exactly where the
// schema is weaker than the rule.
func constraintDescriptionLines(cmd *Command, fidelities []constraintFidelity) []string {
	var lines []string
	for _, f := range fidelities {
		line := cmd.constraintDescriptionSentence(f.decl)
		if f.reason != "" {
			line += " -- not expressed in the schema: " + f.reason
		}
		lines = append(lines, line)
	}
	return lines
}

func (c *Command) constraintDescriptionSentence(decl *constraintDecl) string {
	switch decl.family {
	case familyAtLeastOne:
		return "at least one of: " + c.renderPropertyMembers(decl)
	case familyAllOrNone:
		return "all or none of: " + c.renderPropertyMembers(decl)
	case familyRequires:
		return fmt.Sprintf("%s requires %s", flagParamName(decl.flag), flagParamName(decl.dependsOn))
	default:
		// A false value is a VALUE rather than a name: rendering it by
		// prefixing the property with `no_` would describe a key the schema
		// does not carry and the caller can never send, and it would hide the
		// one fact the line exists to publish -- what the framework will set.
		// The CLI's `--no-` negation stays where a reader types tokens (§26.10).
		return fmt.Sprintf("%s implies %s = %v", flagParamName(decl.flag), flagParamName(decl.implies), decl.value)
	}
}

// renderPropertyMembers is §12.15's member rendering in property names: a
// nested constraint still renders structurally, parenthesized and joined by its
// own family's connector.
func (c *Command) renderPropertyMembers(decl *constraintDecl) string {
	parts := make([]string, 0, len(decl.resolved))
	for _, r := range decl.resolved {
		parts = append(parts, c.renderPropertyMember(r))
	}
	return strings.Join(parts, ", ")
}

func (c *Command) renderPropertyMember(r resolvedMember) string {
	if r.kind != memberKindConstraint {
		return c.mcpPropertyName(r)
	}
	nested := &c.constraints[r.idx]
	parts := make([]string, 0, len(nested.resolved))
	for _, nr := range nested.resolved {
		parts = append(parts, c.renderPropertyMember(nr))
	}
	return "(" + strings.Join(parts, nested.family.connector()) + ")"
}
