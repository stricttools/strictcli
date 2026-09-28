package strictcli

import (
	"sort"
	"strings"
)

// The scoped-selector construct (effects contract §24).
//
// A choice is a declaration scope. A SELECTOR is a flag that elects exactly one
// of its declared choices, and each choice owns the flags that exist only while
// it is elected. The command itself is the ROOT scope, which is what makes every
// rule below uniform at every depth.
//
// Two spellings, one machinery:
//
//   - TOKEN spelling  -- ChoiceFlag("via", ...) with Choice("email", ...): the
//     selector has a token of its own (`--via email`).
//   - MEMBER spelling -- MemberChoiceFlag("profile", ...) with
//     MemberChoice(StringFlag("work", ...), ...): each choice is spelled as its
//     own flag, no selector token is ever typed, and the selector's name exists
//     only as the handler key and as the noun help and errors use. This is what
//     subsumed and replaced MutexGroup (§21's supersession box).

// scopeReservedValueName is the reserved field name a member-spelled choice's
// own payload is delivered under, in the handler record and in the dumped
// schema alike (§24.4, §24.7, §25.6).
const scopeReservedValueName = "value"

// ChoiceDecl is one choice of a selector: a name, its mandatory help, and the
// scope of flags that exist only while it is elected.
//
// It is BOTH the declaration and the identity token a handler switches on, so
// e.Is(ViaEmail) and When(ViaEmail, ...) are compile-checked references: a typo
// does not compile, and a renamed choice breaks every site that names it.
type ChoiceDecl struct {
	// Name is the choice's name. Under member spelling it IS the electing
	// flag's name.
	Name string
	// Help is mandatory: a choice always carries help, which is why a selector
	// always renders as a help block (§24.10).
	Help string
	// Flags is the choice's scope. Under member spelling Flags[0] is the
	// electing member flag.
	Flags []Flag

	// member records that this choice was declared with MemberChoice.
	member bool
	// ownerSel is the selector name this choice was first attached to. Aliasing
	// one choice value into two selectors would make its identity ambiguous at
	// Match time, so it is refused.
	ownerSel string
}

// applyFlag makes a choice declaration usable in a selector constructor's
// variadic, beside Required() and Short("v"). This is what FlagOption became an
// interface for (§24.12, ruling S12).
func (c *ChoiceDecl) applyFlag(f *Flag) {
	f.choiceDecls = append(f.choiceDecls, c)
}

// Choice declares a token-spelled choice and its scope. The scope is ordinary
// Flag values from the ordinary constructors, so presence, shorts and nested
// selectors compose without a second vocabulary.
//
//	sc.ChoiceFlag("via", "delivery channel", sc.Required(), sc.Short("v"),
//	    sc.Choice("email", "deliver the notification as an email message",
//	        sc.StringFlag("subject", "subject line", sc.Required()),
//	    ),
//	    sc.Choice("sms", "deliver the notification as a text message",
//	        sc.StringFlag("phone-number", "destination number", sc.Required()),
//	    ),
//	)
func Choice(name, help string, flags ...Flag) *ChoiceDecl {
	return &ChoiceDecl{Name: name, Help: help, Flags: append([]Flag{}, flags...)}
}

// MemberChoice declares a member-spelled choice: the flag that elects it (and
// carries its payload, when it has one) plus any further scoped flags.
//
// The member flag declares Required(), read as "required once this member is
// elected". A payload-carrying member delivers its value under the reserved
// field name "value"; a bool member is payload-less and delivers no value field.
func MemberChoice(memberFlag Flag, help string, scope ...Flag) *ChoiceDecl {
	flags := append([]Flag{memberFlag}, scope...)
	return &ChoiceDecl{Name: memberFlag.Name, Help: help, Flags: flags, member: true}
}

// ChoiceFlag declares a token-spelled selector: a flag whose value elects
// exactly one of its declared choices.
//
// A selector declares Required() or Default(<choice name>). Optional() is
// refused: an absent selection is a choice nobody named, so name it (§24.5).
func ChoiceFlag(name, help string, opts ...FlagOption) Flag {
	return newSelectorFlag(name, help, false, opts)
}

// MemberChoiceFlag declares a member-spelled selector: its choices are elected
// by their own flags and no selector token is ever typed.
//
// It is a TWIN CONSTRUCTOR rather than an option, so the spelling is one
// declaration instead of two that must agree (§24.12).
func MemberChoiceFlag(name, help string, opts ...FlagOption) Flag {
	return newSelectorFlag(name, help, true, opts)
}

func newSelectorFlag(name, help string, memberSpelled bool, opts []FlagOption) Flag {
	f := Flag{
		Name:          name,
		Type:          TypeChoice,
		Help:          help,
		Prefixed:      true,
		memberSpelled: memberSpelled,
	}
	for _, opt := range opts {
		opt.applyFlag(&f)
	}
	validateFlagConfig(&f)
	validateSelectorDecl(&f)
	return f
}

// choiceNames returns a selector's declared choice names in declaration order.
func choiceNames(sel *Flag) []string {
	out := make([]string, len(sel.choiceDecls))
	for i, c := range sel.choiceDecls {
		out[i] = c.Name
	}
	return out
}

// findChoice returns the named choice of a selector, or nil.
func findChoice(sel *Flag, name string) *ChoiceDecl {
	for _, c := range sel.choiceDecls {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// memberTokens renders a member-spelled selector's members as CLI tokens, in
// declaration order. Member lists are UNQUOTED and comma-joined, which is
// §21.4's existing spelling (`one of --a, --b is required`).
func memberTokens(sel *Flag) []string {
	out := make([]string, len(sel.choiceDecls))
	for i, c := range sel.choiceDecls {
		out[i] = "--" + c.Name
	}
	return out
}

// memberFlag returns the electing flag of a member-spelled choice.
func memberFlag(ch *ChoiceDecl) *Flag {
	return &ch.Flags[0]
}

// choiceCarriesPayload reports whether a member choice carries a value. A bool
// member is payload-less: electing it is the whole of what it says.
func choiceCarriesPayload(ch *ChoiceDecl) bool {
	return ch.member && memberFlag(ch).Type != TypeBool
}

// ---------------------------------------------------------------------------
// Scope paths (§12.13's pinned format)
// ---------------------------------------------------------------------------

// pathSeg is one election on a scope path: the selector and the choice elected
// from it.
type pathSeg struct {
	sel *Flag
	ch  *ChoiceDecl
}

// renderSeg renders one scope-path segment. A token-spelled segment is
// `--<selector> <choice>`; a member-spelled segment is `--<choice>` -- the
// member's own flag, which is the only token a reader ever types.
func renderSeg(s pathSeg) string {
	if s.sel.memberSpelled {
		return "--" + s.ch.Name
	}
	return "--" + s.sel.Name + " " + s.ch.Name
}

// renderScopePath renders a scope path: one segment per election, outermost
// first, joined by a single space. Callers quote it.
func renderScopePath(path []pathSeg) string {
	parts := make([]string, len(path))
	for i, s := range path {
		parts[i] = renderSeg(s)
	}
	return strings.Join(parts, " ")
}

// quotedScopePath is renderScopePath wrapped in the single quotes every
// template that names a scope path uses.
func quotedScopePath(path []pathSeg) string {
	return "'" + renderScopePath(path) + "'"
}

// scopeSuffix is the clause appended to a presence message when the flag or the
// selector lives inside a scope. It is EMPTY at root scope, which is what makes
// every root-scope message byte-identical to what it was before this round.
func scopeSuffix(path []pathSeg) string {
	if len(path) == 0 {
		return ""
	}
	return errScopeSuffix(renderScopePath(path))
}

// ---------------------------------------------------------------------------
// Delivery (§24.1, §24.9)
// ---------------------------------------------------------------------------

// Fields is one elected choice's scope, keyed by parameter name (dashes become
// underscores, exactly as handler kwargs are keyed). It is a named map type over
// map[string]interface{}, so Get[T] works on it unchanged.
type Fields map[string]interface{}

// Elected is the ONE tagged value a selector delivers: the elected choice plus
// that choice's fields. Sub-flags are never top-level handler arguments at any
// depth, so §23's delivery invariant -- every declared top-level key is always
// present -- is untouched rather than merely compatible.
type Elected struct {
	// Fields carries the elected choice's scope. A member-spelled choice's own
	// payload arrives under the reserved name "value".
	Fields Fields

	decl *ChoiceDecl
	sel  *Flag
	// provided answers §23.6's question for each field, evaluated inside the
	// scope: true when the invocation caused the value, false when the
	// declaration did.
	provided map[string]bool
}

// Name returns the elected choice's name.
func (e *Elected) Name() string { return e.decl.Name }

// Is reports whether this is the given choice. The argument is the very
// *ChoiceDecl the declaration used, so a misspelled or stale case is a compile
// error rather than a silently-never-taken branch.
func (e *Elected) Is(c *ChoiceDecl) bool { return e.decl == c }

// Provided reports whether the INVOCATION caused this field's value, and false
// when the declaration did (contract §23.6, evaluated inside the scope).
//
// Scoped provided-ness is answered by the delivered record and NOT by
// ctx.Provided: a scoped name is not unique command-wide, so a scoped flag is
// not in the context's per-parse store at all (§24.9). Accepts dashed or
// underscored names, underscore form tried first.
//
// It panics on a name the elected scope does not declare, with the same text
// ctx.Source and ctx.Provided use for an unknown name.
func (e *Elected) Provided(name string) bool {
	key := strings.ReplaceAll(name, "-", "_")
	if v, ok := e.provided[key]; ok {
		return v
	}
	if v, ok := e.provided[name]; ok {
		return v
	}
	panic(errNoSourceInfo(name))
}

// String renders the elected choice and its fields, for %v and test diffs.
func (e *Elected) String() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ":" + formatValueForError(e.Fields[k])
	}
	return e.decl.Name + "[" + strings.Join(parts, " ") + "]"
}

// Elect builds an elected value for the programmatic front door: the value a
// selector takes in App.Call's kwargs is the same record a handler receives
// (contract §24.11).
//
//	app.Call("send", map[string]interface{}{
//	    "via": sc.Elect(ViaEmail, sc.Fields{"subject": "hi", "recipient": "a@b"}),
//	})
//
// The record still runs through the same election, scope and presence machinery
// the argv path uses, which is what makes the two front doors agree by
// construction rather than by test.
func Elect(ch *ChoiceDecl, fields Fields) *Elected {
	if fields == nil {
		fields = Fields{}
	}
	return &Elected{decl: ch, Fields: fields}
}

// GetElected returns the tagged value a selector delivered.
//
// It panics if the key is absent or does not hold an elected value. A selector
// always elects -- it declares Required() or a Default(), never Optional()
// (§24.5) -- so the returned pointer is never nil.
func GetElected(kwargs map[string]interface{}, name string) *Elected {
	v, ok := kwargs[name]
	if !ok {
		panic(errGetElectedNoSuchKey(name))
	}
	e, ok := v.(*Elected)
	if !ok || e == nil {
		panic(errGetElectedNotSelector(name, v))
	}
	return e
}

// MatchCase binds one declared choice to what to do with its scope.
type MatchCase[T any] struct {
	ch *ChoiceDecl
	fn func(Fields) T
}

// When builds a Match case. The choice is passed by reference, so the compiler
// has already checked that the case names a real choice.
func When[T any](ch *ChoiceDecl, fn func(Fields) T) MatchCase[T] {
	return MatchCase[T]{ch: ch, fn: fn}
}

// Match dispatches on the elected choice and is EXHAUSTIVE AGAINST THE
// DECLARATION: it compares the cases to the selector's choice list and panics
// naming what is missing.
//
// Go has no sealed union, so the check is at dispatch rather than at compile
// time. It cannot be defeated by a typo (cases are references) and it cannot go
// stale (adding a choice breaks every Match that omits it, on the first call).
// §17's accepted-ceiling reading applies.
func Match[T any](e *Elected, cases ...MatchCase[T]) T {
	byDecl := make(map[*ChoiceDecl]func(Fields) T, len(cases))
	for _, c := range cases {
		if findChoice(e.sel, c.ch.Name) != c.ch {
			panic(errMatchForeignCase(e.sel.Name, c.ch.Name))
		}
		if _, dup := byDecl[c.ch]; dup {
			panic(errMatchDuplicateCase(e.sel.Name, c.ch.Name))
		}
		byDecl[c.ch] = c.fn
	}
	var missing []string
	for _, ch := range e.sel.choiceDecls {
		if _, ok := byDecl[ch]; !ok {
			missing = append(missing, ch.Name)
		}
	}
	if len(missing) > 0 {
		panic(errMatchMissingCases(e.sel.Name, missing))
	}
	return byDecl[e.decl](e.Fields)
}

// ---------------------------------------------------------------------------
// Registration validation
// ---------------------------------------------------------------------------

// validateSelectorDecl runs every rule a selector can be judged by on its own,
// without knowing the command it will be registered on. The command-level rules
// (root collisions, co-electable name and short reuse, constraints naming a
// scoped flag) run in buildAndValidateCommand, where the whole tree is visible.
func validateSelectorDecl(sel *Flag) {
	if len(sel.choiceDecls) < 2 {
		panic(errSelectorNoChoices(sel.Name))
	}
	// An absent selection is a choice nobody named, so the refusal redirects to
	// naming it (§24.5, ruling B2 made structural).
	if sel.presence == presenceOptional {
		panic(errSelectorOptional(sel.Name))
	}
	if sel.memberSpelled && sel.Short != "" {
		panic(errMemberSelectorShort(sel.Name))
	}

	seen := make(map[string]bool, len(sel.choiceDecls))
	for _, ch := range sel.choiceDecls {
		if seen[ch.Name] {
			panic(errChoiceDuplicateName(sel.Name, ch.Name))
		}
		seen[ch.Name] = true
		if strings.TrimSpace(ch.Help) == "" {
			panic(errChoiceHelpEmpty(sel.Name, ch.Name))
		}
		// One charset for both spellings: under member spelling the name IS the
		// flag that elects the choice, and under token spelling it is the value
		// that names it (§24.7).
		if !isKebabName(ch.Name) {
			panic(errChoiceNameCharset(sel.Name, ch.Name))
		}
		if ch.ownerSel != "" && ch.ownerSel != sel.Name {
			panic(errChoiceAliased(ch.Name, ch.ownerSel, sel.Name))
		}
		ch.ownerSel = sel.Name

		if sel.memberSpelled {
			if !ch.member {
				panic(errMemberChoiceRequired(sel.Name, ch.Name))
			}
			// A member flag MUST declare requiredness, read as "required once
			// this member is elected" (§24.4). The rule inverts the deleted
			// mutex-member rule because the declaration changed.
			if memberFlag(ch).presence != presenceRequired {
				panic(errMemberFlagPresence(sel.Name, ch.Name))
			}
		} else if ch.member {
			// A token-spelled choice is named by the token itself and has no
			// payload to carry (§24.4).
			panic(errTokenChoiceCarriesPayload(sel.Name, ch.Name))
		}

		// The two names the delivered record uses are reserved inside every
		// scope (§24.7). Every OTHER name ban already ran in the scoped flag's
		// own constructor, at every depth.
		start := 0
		if ch.member {
			start = 1
		}
		for j := range ch.Flags {
			sub := &ch.Flags[j]
			resolveFlagPresence(sub)
			if sub.Name == "choice" {
				panic(errScopedNameChoiceReserved(ch.Name, sel.Name))
			}
			if j >= start && sub.Name == scopeReservedValueName {
				panic(errScopedNameValueReserved(ch.Name, sel.Name))
			}
			if sub.Name == sel.Name {
				panic(errScopedNameCollidesSelector(ch.Name, sel.Name, sub.Name))
			}
		}
	}

	// Sibling scopes may reuse a name only with an identical VALUE SHAPE (type
	// and arity): two choices of one selector can never be elected together, so
	// the name is unambiguous at delivery -- but tokenization precedes election,
	// so the token's arity may not depend on the outcome (§24.7, §12.13's
	// widened errSiblingScopeShapeMismatch).
	type shapeSite struct {
		flag   *Flag
		choice string
	}
	shapes := map[string]shapeSite{}
	for _, ch := range sel.choiceDecls {
		for j := range ch.Flags {
			sub := &ch.Flags[j]
			if prev, reused := shapes[sub.Name]; reused {
				if !sameValueShape(prev.flag, sub) {
					panic(errSiblingScopeShapeMismatch(sel.Name, sub.Name, prev.choice, ch.Name))
				}
				continue
			}
			shapes[sub.Name] = shapeSite{flag: sub, choice: ch.Name}
		}
	}

	// A defaulted selection is COMPLETE: a choice plus every field its scope
	// needs, so a defaulted selection with an unsatisfied required sub-flag
	// cannot exist (§24.5). Go's default names a choice, so completeness is a
	// registration check.
	if sel.presence == presenceDefault {
		name, ok := sel.Default.(string)
		if !ok || !seen[name] {
			panic(errSelectorDefaultUnknownChoice(sel.Name, sel.Default, strings.Join(choiceNames(sel), ", ")))
		}
		ch := findChoice(sel, name)
		start := 0
		if ch.member {
			// A value-carrying member's value is supplied by the token that
			// elects it, and a default has no token.
			if choiceCarriesPayload(ch) {
				panic(errMemberDefaultCarriesValue(sel.Name, ch.Name))
			}
			start = 1
		}
		for j := start; j < len(ch.Flags); j++ {
			if ch.Flags[j].presence == presenceRequired {
				panic(errSelectorDefaultIncomplete(sel.Name, ch.Name, ch.Flags[j].Name))
			}
		}
	}
}

// valueShape is a declaration's type and arity together, which is §25.3's own
// word for the pair and exactly what its `value_schema` fragment would publish.
type valueShape struct {
	t          FlagType
	repeatable bool
}

func shapeOf(f *Flag) valueShape {
	return valueShape{t: f.Type, repeatable: f.Repeatable}
}

// sameValueShape reports whether two declarations tokenize and deliver
// identically: same type and same arity.
func sameValueShape(a, b *Flag) bool {
	return shapeOf(a) == shapeOf(b)
}

// ---------------------------------------------------------------------------
// The flag index: every declaration site in the scope tree
// ---------------------------------------------------------------------------

// flagSite is one declaration of a flag name, together with the scope path it
// sits in. A name may have several sites when mutually exclusive scopes reuse
// it.
type flagSite struct {
	flag *Flag
	path []pathSeg // empty at root scope
}

// choice returns the choice owning this site's scope, or nil at root.
func (s *flagSite) choice() *ChoiceDecl {
	if len(s.path) == 0 {
		return nil
	}
	return s.path[len(s.path)-1].ch
}

// elects reports whether this site's declaration is itself an election token: a
// token-spelled selector, or a member flag, which elects by being typed. A
// member-spelled selector is not one -- it has no token at all, which is why it
// cannot carry a short in the first place.
func (s *flagSite) elects() bool {
	if s.flag.Type == TypeChoice && !s.flag.memberSpelled {
		return true
	}
	ch := s.choice()
	return ch != nil && ch.member && memberFlag(ch) == s.flag
}

// electsMember reports whether this site IS the electing flag of a
// member-spelled choice: the declaration whose name is the choice's name, and
// therefore a flag name command-wide (§24.7).
func electsMember(s *flagSite) bool {
	ch := s.choice()
	return ch != nil && ch.member && memberFlag(ch) == s.flag
}

// ownerScopePath is the scope a site's declaration BELONGS TO. For every
// ordinary flag that is the site's own path, but a member flag is declared BY
// the last election on its path rather than under it, so the scope that owns it
// is the path without that segment (§12.13, §18.19 item 224).
func ownerScopePath(s *flagSite) []pathSeg {
	if electsMember(s) && len(s.path) > 0 {
		return s.path[:len(s.path)-1]
	}
	return s.path
}

// selector returns the selector owning this site's scope, or nil at root.
func (s *flagSite) selector() *Flag {
	if len(s.path) == 0 {
		return nil
	}
	return s.path[len(s.path)-1].sel
}

// flagIndex is a command's whole declaration tree, flattened by name.
type flagIndex struct {
	// order is every declared name, in declaration order (outermost first).
	order []string
	// sites maps a name to every site declaring it, in declaration order.
	sites map[string][]*flagSite
	// shortOrder / shorts do the same for short forms.
	shortOrder []string
	shorts     map[string][]*flagSite
	// hasSelectors reports whether the command declares any selector at all.
	hasSelectors bool
}

// buildFlagIndex walks a command's whole scope tree.
func buildFlagIndex(flags []Flag) *flagIndex {
	idx := &flagIndex{sites: map[string][]*flagSite{}, shorts: map[string][]*flagSite{}}
	var walk func(flags []Flag, path []pathSeg)
	walk = func(flags []Flag, path []pathSeg) {
		for i := range flags {
			f := &flags[i]
			site := &flagSite{flag: f, path: path}
			if _, seen := idx.sites[f.Name]; !seen {
				idx.order = append(idx.order, f.Name)
			}
			idx.sites[f.Name] = append(idx.sites[f.Name], site)
			if f.Short != "" {
				if _, seen := idx.shorts[f.Short]; !seen {
					idx.shortOrder = append(idx.shortOrder, f.Short)
				}
				idx.shorts[f.Short] = append(idx.shorts[f.Short], site)
			}
			if f.Type != TypeChoice {
				continue
			}
			idx.hasSelectors = true
			for _, ch := range f.choiceDecls {
				sub := make([]pathSeg, len(path), len(path)+1)
				copy(sub, path)
				sub = append(sub, pathSeg{sel: f, ch: ch})
				walk(ch.Flags, sub)
			}
		}
	}
	walk(flags, nil)
	return idx
}

// divergence returns the index of the first path position where two scope paths
// choose differently under the SAME selector. It returns -1 when no such
// position exists, which means the two scopes are simultaneously electable
// (one is an ancestor of the other, or they branch on two different selectors).
func divergence(a, b []pathSeg) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i].sel != b[i].sel {
			return -1
		}
		if a[i].ch != b[i].ch {
			return i
		}
	}
	return -1
}

// validateCommandScopes enforces the rules that need the whole command in view:
// root-versus-scoped collisions, simultaneously-electable name and short reuse,
// and the shape rule for mutually exclusive scopes that reuse a name at a depth
// deeper than one selector's own siblings.
func validateCommandScopes(cmdName string, idx *flagIndex) {
	for _, name := range idx.order {
		sites := idx.sites[name]
		for i := 0; i < len(sites); i++ {
			for j := i + 1; j < len(sites); j++ {
				a, b := sites[i], sites[j]
				rootA, rootB := len(a.path) == 0, len(b.path) == 0
				if rootA && rootB {
					// Two root declarations of one name: the pre-existing
					// duplicate-flag error already covers it.
					continue
				}
				if rootA || rootB {
					scoped := a
					root := b
					if rootA {
						scoped, root = b, a
					}
					// A scoped flag may not reuse the name of the selector that
					// owns it -- checked here too, because a nested selector's
					// own name reaches this pairing rather than the per-selector
					// pass.
					if onPath(scoped.path, root.flag) {
						panic(errScopedNameCollidesSelector(scoped.choice().Name, scoped.selector().Name, name))
					}
					// Under member spelling a choice name IS a flag name
					// (§24.7), so a member flag and a command-level flag of that
					// name are two declarations of ONE flag name rather than a
					// scoped flag nothing could reach. The plain duplicate-flag
					// error is the sentence that says so (§18.19 item 223).
					if electsMember(scoped) {
						panic(errCommandDuplicateFlag(cmdName, name))
					}
					panic(errScopedNameCollidesRoot(scoped.choice().Name, scoped.selector().Name, name))
				}
				d := divergence(a.path, b.path)
				if d < 0 {
					panic(errCoElectableNameReuse(cmdName, name, renderScopePath(a.path), renderScopePath(b.path)))
				}
				if !sameValueShape(a.flag, b.flag) {
					panic(errSiblingScopeShapeMismatch(a.path[d].sel.Name, name, a.path[d].ch.Name, b.path[d].ch.Name))
				}
			}
		}
	}

	// Shorts are claimed across every simultaneously live scope; mutually
	// exclusive scopes may reuse one.
	for _, short := range idx.shortOrder {
		sites := idx.shorts[short]
		for i := 0; i < len(sites); i++ {
			for j := i + 1; j < len(sites); j++ {
				a, b := sites[i], sites[j]
				if a.flag.Name == b.flag.Name {
					continue
				}
				if divergence(a.path, b.path) < 0 {
					panic(errShortCollidesAcrossScopes(cmdName, short, a.flag.Name, b.flag.Name))
				}
			}
		}
	}

	// What survives the pass above is SIBLING reuse: one short claimed by two
	// or more names that can never be live together. §24.7 permits it, and the
	// two guards below are what it permits it subject to (§12.13, §18.19 item
	// 221). Both are stated over SCOPES rather than over choices of a
	// token-spelled selector, which is what covers two sibling MEMBER scopes
	// with the same words -- the index walks member scopes, so a member flag's
	// own short is a claimant here like any other.
	for _, short := range idx.shortOrder {
		sites := idx.shorts[short]
		var names []string
		seen := map[string]bool{}
		for _, s := range sites {
			if !seen[s.flag.Name] {
				seen[s.flag.Name] = true
				names = append(names, s.flag.Name)
			}
		}
		if len(names) < 2 {
			continue
		}
		shapes := map[valueShape]bool{}
		for _, name := range names {
			for _, s := range sites {
				if s.flag.Name != name {
					continue
				}
				if s.elects() {
					panic(errShortOnAmbiguousElection(cmdName, short, name))
				}
				shapes[shapeOf(s.flag)] = true
			}
		}
		if len(shapes) > 1 {
			panic(errShortShapeMismatch(cmdName, short, names[0], names[1]))
		}
	}
}

// onPath reports whether the given flag is one of the selectors on a path.
func onPath(path []pathSeg, f *Flag) bool {
	for _, s := range path {
		if s.sel == f {
			return true
		}
	}
	return false
}

// scopedFlagPath returns the scope path of a scoped flag name, or nil when the
// name is declared at root scope only. Used by the constraint guard, which
// refuses a dependency naming a scoped flag (§24.8).
func (idx *flagIndex) scopedFlagPath(name string) []pathSeg {
	sites, ok := idx.sites[name]
	if !ok {
		return nil
	}
	for _, s := range sites {
		if len(s.path) == 0 {
			return nil
		}
	}
	return sites[0].path
}
