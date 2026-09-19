package strictcli

import (
	"os"
	"strings"
)

// The scoped-selector parse engine (contract §24.3).
//
// Parsing is PHASED, and the phases are what make order independence, the
// distinct out-of-scope error, and that error's priority over a missing
// required flag fall out instead of being special-cased:
//
//	1. Tokenize   every occurrence, without interpreting any of it.
//	2. Elect      outermost first, then recursively inside each elected choice.
//	3. Scope      validate the scope membership of every supplied flag.
//	4. Values     resolve values and presence within the live scopes only.
//
// Error precedence is pinned by that order -- election -> scope -> value ->
// presence -- so a command line with several problems reports the same error
// every time and never one that depends on declaration order.

// electionOrigin records where an election came from. An election from a
// non-CLI source names itself in every message it causes (§24.6).
type electionOrigin struct {
	kind string // "cli", "env", "config", "default"
	name string // env var name or config key; empty for cli/default
}

// clause renders the origin as one of §12.13's three pinned clauses. A
// command-line election renders the EMPTY clause.
func (o electionOrigin) clause() string {
	switch o.kind {
	case "env":
		return errElectionOriginEnv(o.name)
	case "config":
		return errElectionOriginConfig(o.name)
	case "default":
		return errElectionOriginDefault
	}
	return ""
}

// liveScope is one elected scope: the flags declared in it, plus the selectors
// among them with their resolved elections.
type liveScope struct {
	path  []pathSeg
	flags []*Flag
	sels  []*liveSel
}

// liveSel is one selector's resolved election.
type liveSel struct {
	sel    *Flag
	path   []pathSeg // the path of the scope the selector is DECLARED in
	choice *ChoiceDecl
	origin electionOrigin
	inner  *liveScope
	// unsatisfied records a REQUIRED selector that elected nothing. Its refusal
	// is deferred until after scope validation, which is part of the election
	// phase's contract rather than an implementation detail (§24.3).
	unsatisfied bool
	// declineClause is §21.4's clause when a member was declined.
	declineClause string
	// memberImplied records a member flag whose value is implied by the
	// selector's own default electing it (a payload-less bool member).
	memberImplied bool
	// recordField records an election supplied inside a RECORD's fields, which
	// makes it one of that record's fields for §18.26 item 253's purposes.
	recordField bool
}

// ambientSource is the environment/config lookup the election phase needs. It is
// an interface so the argv path and the programmatic path can supply their own
// (the programmatic path consults neither).
type ambientSource struct {
	hermetic   bool
	configData map[string]interface{}
}

func (a ambientSource) env(name string) (string, bool) {
	if a.hermetic || name == "" {
		return "", false
	}
	return os.LookupEnv(name)
}

func (a ambientSource) config(param string) (interface{}, bool) {
	if a.hermetic || a.configData == nil {
		return nil, false
	}
	v, ok := a.configData[param]
	return v, ok
}

// electionState is the per-parse election result for a whole command.
type electionState struct {
	cmd  *Command
	root *liveScope
	// live records every flag whose scope is elected.
	live map[*Flag]bool
	// bySel maps a selector to its resolved election.
	bySel map[*Flag]*liveSel
	// skipped collects the conditional bindings this run did NOT consult, one
	// line per binding in declaration order (§24.6).
	skipped []string
}

// electedNames is what a supplied CLI token set says about a selector. The argv
// path fills it from tokenization; the programmatic path fills it from the
// elected record it was handed.
type suppliedElections struct {
	// tokenValues maps a token-spelled selector's name to the choice names
	// supplied on the command line, in command-line order.
	tokenValues map[string][]string
	// memberElected reports, per member flag name, whether the invocation
	// elected it (true), declined it (false), or said nothing (absent).
	memberElected map[string]bool
	// suppliedNames is every flag name the invocation named at all.
	suppliedNames map[string]bool
	// preElected short-circuits election for the programmatic front door, where
	// the caller hands over an already-elected record.
	preElected map[*Flag]*ChoiceDecl
	// recordElected records the selectors whose election was read out of a
	// RECORD's fields. Such an election is a FIELD of that record, so it earns
	// the label every field of a record earns (§18.26 item 253).
	recordElected map[*Flag]bool
}

func newSuppliedElections() *suppliedElections {
	return &suppliedElections{
		tokenValues:   map[string][]string{},
		memberElected: map[string]bool{},
		suppliedNames: map[string]bool{},
		preElected:    map[*Flag]*ChoiceDecl{},
		recordElected: map[*Flag]bool{},
	}
}

// elect runs phase 2 over a command's whole scope tree.
func elect(cmd *Command, sup *suppliedElections, amb ambientSource) (*electionState, string) {
	st := &electionState{
		cmd:   cmd,
		live:  map[*Flag]bool{},
		bySel: map[*Flag]*liveSel{},
	}
	root, errMsg := st.electScope(cmd.flags, nil, sup, amb)
	if errMsg != "" {
		return nil, errMsg
	}
	st.root = root
	// Every binding in a scope that was NOT elected is named, in declaration
	// order, after the whole tree is resolved.
	st.collectSkippedBindings(cmd.index, amb)
	return st, ""
}

func (st *electionState) electScope(flags []Flag, path []pathSeg, sup *suppliedElections, amb ambientSource) (*liveScope, string) {
	scope := &liveScope{path: path}
	for i := range flags {
		f := &flags[i]
		st.live[f] = true
		scope.flags = append(scope.flags, f)
	}
	for i := range flags {
		f := &flags[i]
		if f.Type != TypeChoice {
			continue
		}
		ls, errMsg := st.electOne(f, path, sup, amb)
		if errMsg != "" {
			return nil, errMsg
		}
		st.bySel[f] = ls
		scope.sels = append(scope.sels, ls)
		if ls.choice == nil {
			continue
		}
		sub := make([]pathSeg, len(path), len(path)+1)
		copy(sub, path)
		sub = append(sub, pathSeg{sel: f, ch: ls.choice})
		inner, errMsg := st.electScope(ls.choice.Flags, sub, sup, amb)
		if errMsg != "" {
			return nil, errMsg
		}
		ls.inner = inner
	}
	return scope, ""
}

func (st *electionState) electOne(sel *Flag, path []pathSeg, sup *suppliedElections, amb ambientSource) (*liveSel, string) {
	ls := &liveSel{sel: sel, path: path, recordField: sup.recordElected[sel]}
	// The programmatic front door hands over an already-elected record.
	if ch, ok := sup.preElected[sel]; ok {
		ls.choice = ch
		ls.origin = electionOrigin{kind: "cli"}
		return ls, ""
	}
	if sel.memberSpelled {
		return st.electMembers(sel, ls, sup)
	}
	return st.electToken(sel, ls, sup, amb)
}

// electToken resolves a token-spelled selector. It is an ordinary value flag
// whose value happens to name a choice, so CLI > env > config > default applies
// unchanged (§24.6).
func (st *electionState) electToken(sel *Flag, ls *liveSel, sup *suppliedElections, amb ambientSource) (*liveSel, string) {
	if vals, ok := sup.tokenValues[sel.Name]; ok && len(vals) > 0 {
		// Last-wins is right for a plain flag and wrong for an election:
		// discarding a value would discard a whole scope with it.
		if len(vals) > 1 {
			return nil, errSelectorElectedTwice(sel.Name, vals)
		}
		ch := findChoice(sel, vals[0])
		if ch == nil {
			return nil, errFlagInvalidChoice(sel.Name, vals[0], strings.Join(choiceNames(sel), ", "))
		}
		ls.choice = ch
		ls.origin = electionOrigin{kind: "cli"}
		return ls, ""
	}
	if v, ok := amb.env(sel.Env); ok {
		ch := findChoice(sel, v)
		if ch == nil {
			return nil, errFlagErrFromEnvVar(sel.Name, errFlagInvalidChoice(sel.Name, v, strings.Join(choiceNames(sel), ", ")), sel.Env)
		}
		ls.choice = ch
		ls.origin = electionOrigin{kind: "env", name: sel.Env}
		return ls, ""
	}
	param := flagParamName(sel.Name)
	if v, ok := amb.config(param); ok {
		s, isStr := v.(string)
		if !isStr {
			return nil, errConfigValueError(sel.Name, errConfigExpectedStringGot(typeName(v)))
		}
		ch := findChoice(sel, s)
		if ch == nil {
			return nil, errConfigValueError(sel.Name, errFlagInvalidChoice(sel.Name, s, strings.Join(choiceNames(sel), ", ")))
		}
		ls.choice = ch
		ls.origin = electionOrigin{kind: "config", name: param}
		return ls, ""
	}
	if sel.presence == presenceDefault {
		ls.choice = findChoice(sel, sel.Default.(string))
		ls.origin = electionOrigin{kind: "default"}
		return ls, ""
	}
	// Required and nothing elected. The refusal waits for scope validation: a
	// scoped flag the reader actually typed names a token they would have to
	// change, and that is the more useful of the two true statements (§24.3).
	ls.unsatisfied = true
	return ls, ""
}

// electMembers resolves a member-spelled selector. Election is COMMAND-LINE
// ONLY (§21.3 carried over by §24.6), and §21.4's three errors survive verbatim.
func (st *electionState) electMembers(sel *Flag, ls *liveSel, sup *suppliedElections) (*liveSel, string) {
	var elected []*ChoiceDecl
	var declined []string
	firstDeclined := ""
	for _, ch := range sel.choiceDecls {
		chosen, named := sup.memberElected[ch.Name]
		if !named {
			continue
		}
		if !chosen {
			// `--no-<name>` DECLINES: it names the member and states it is not
			// the choice. It elects nothing.
			declined = append(declined, "--no-"+ch.Name)
			if firstDeclined == "" {
				firstDeclined = ch.Name
			}
			continue
		}
		elected = append(elected, ch)
	}
	if firstDeclined != "" {
		ls.declineClause = errMutexDeclineClause(firstDeclined)
	}
	if len(elected) > 1 {
		toks := make([]string, len(elected))
		for i, ch := range elected {
			toks[i] = "--" + ch.Name
		}
		return nil, errMutuallyExclusive(strings.Join(toks, " and "))
	}
	if len(elected) == 1 {
		if len(declined) > 0 {
			return nil, errMutexRedundantNegation(strings.Join(declined, " and "), elected[0].Name, ls.declineClause)
		}
		ls.choice = elected[0]
		ls.origin = electionOrigin{kind: "cli"}
		return ls, ""
	}
	if sel.presence == presenceDefault {
		// Registration already guaranteed the default elects a payload-less
		// member, whose own value is implied true.
		ls.choice = findChoice(sel, sel.Default.(string))
		ls.origin = electionOrigin{kind: "default"}
		ls.memberImplied = true
		return ls, ""
	}
	ls.unsatisfied = true
	return ls, ""
}

// collectSkippedBindings names every conditional binding this run did not
// consult: an env var or config key bound to a flag whose scope was not elected
// (§24.6). One line per binding, in declaration order.
func (st *electionState) collectSkippedBindings(idx *flagIndex, amb ambientSource) {
	if idx == nil {
		return
	}
	for _, name := range idx.order {
		for _, site := range idx.sites[name] {
			if len(site.path) == 0 || st.live[site.flag] {
				continue
			}
			path := renderScopePath(site.path)
			// The enumeration is over bindings that CARRIED A VALUE: amb.env is
			// the same lookup an elected scope would have made, so an unset var
			// (or one suppressed by hermetic) names nothing, exactly as the
			// config branch below requires the key to be present.
			if _, ok := amb.env(site.flag.Env); ok {
				st.skipped = append(st.skipped, errAmbientBindingSkippedEnv(site.flag.Env, name, path))
			}
			param := flagParamName(name)
			if _, ok := amb.config(param); ok {
				st.skipped = append(st.skipped, errAmbientBindingSkippedConfig(param, name, path))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Phase 3: scope validation
// ---------------------------------------------------------------------------

// checkScope refuses every supplied flag that is declared only under a scope
// that was not elected. suppliedOrder is the order names were first named on the
// command line, so the error names the token the reader typed first.
func (st *electionState) checkScope(suppliedOrder []string) string {
	for _, name := range suppliedOrder {
		sites, ok := st.cmd.index.sites[name]
		if !ok {
			continue // a global flag, or a name the tokenizer already refused
		}
		liveHere := false
		for _, s := range sites {
			if st.live[s.flag] || st.isLiveElectionToken(s) {
				liveHere = true
				break
			}
		}
		if liveHere {
			continue
		}
		owners := make([]string, 0, len(sites))
		for _, s := range sites {
			owners = append(owners, quotedScopePath(ownerScopePath(s)))
		}
		return errFlagOutOfScope(name, strings.Join(owners, " or "), st.why(sites[0].path))
	}
	return ""
}

// isLiveElectionToken reports whether a site is the ELECTING flag of a
// member-spelled choice whose selector is itself reachable. Such a token is not
// a scoped flag for scope-validation purposes: naming it IS the election, and
// declining it (`--no-x`) is a legal statement about the selector rather than a
// flag supplied outside its scope. §21.4's decline errors then report it.
func (st *electionState) isLiveElectionToken(s *flagSite) bool {
	if !electsMember(s) {
		return false
	}
	sel := s.selector()
	if _, resolved := st.bySel[sel]; !resolved {
		return false
	}
	return true
}

// why names the FIRST (outermost) unsatisfied election on a path, never the
// innermost one: a flag two levels down whose outer election is the one that
// failed blames the outer election, because that is the token the user would
// have to change (§12.13, §24.3).
func (st *electionState) why(path []pathSeg) string {
	for i, seg := range path {
		ls, ok := st.bySel[seg.sel]
		if !ok {
			// The selector itself sits in a scope that was not elected, so an
			// outer segment already explained it.
			continue
		}
		if ls.choice == seg.ch {
			continue
		}
		if ls.choice == nil {
			if seg.sel.memberSpelled {
				return errScopeWhyNoMemberElected(strings.Join(memberTokens(seg.sel), ", "))
			}
			return errScopeWhyNotProvided(seg.sel.Name)
		}
		elected := make([]pathSeg, i, i+1)
		copy(elected, path[:i])
		elected = append(elected, pathSeg{sel: seg.sel, ch: ls.choice})
		return errScopeWhyElected(renderScopePath(elected), ls.origin.clause())
	}
	// Unreachable for a flag that is genuinely out of scope; kept total.
	return errScopeWhyNotProvided(path[0].sel.Name)
}

// ---------------------------------------------------------------------------
// Presence messages inside a scope
// ---------------------------------------------------------------------------

// ambientOriginSuffix renders the parenthesized origin clause for a scope. It
// names the OUTERMOST non-CLI election on the path -- the ambient cause a reader
// cannot see in their own command line -- and is empty when every election on
// the path was typed.
func (st *electionState) ambientOriginSuffix(path []pathSeg) string {
	for _, seg := range path {
		ls, ok := st.bySel[seg.sel]
		if !ok {
			continue
		}
		if c := ls.origin.clause(); c != "" {
			return errElectionOriginSuffix(c)
		}
	}
	return ""
}

// scopedPresenceSuffix is the scope suffix plus the origin suffix, in that order
// and never the other way round (§12.13).
func (st *electionState) scopedPresenceSuffix(path []pathSeg) string {
	return scopeSuffix(path) + st.ambientOriginSuffix(path)
}

// unsatisfiedSelectorError renders the deferred refusal of a required selector
// that elected nothing, once scope validation has passed.
func (st *electionState) unsatisfiedSelectorError(ls *liveSel) string {
	suffix := st.scopedPresenceSuffix(ls.path)
	if ls.sel.memberSpelled {
		return errOneOfRequired(strings.Join(memberTokens(ls.sel), ", "), suffix+ls.declineClause)
	}
	return "flag '--" + ls.sel.Name + "' is required" + suffix
}

// walkLive visits every live scope, outermost first, in declaration order.
func (st *electionState) walkLive(fn func(scope *liveScope)) {
	var walk func(s *liveScope)
	walk = func(s *liveScope) {
		if s == nil {
			return
		}
		fn(s)
		for _, ls := range s.sels {
			walk(ls.inner)
		}
	}
	walk(st.root)
}

// firstUnsatisfied returns the outermost-first, declaration-order first selector
// that is required and elected nothing.
func (st *electionState) firstUnsatisfied() *liveSel {
	var found *liveSel
	st.walkLive(func(s *liveScope) {
		if found != nil {
			return
		}
		for _, ls := range s.sels {
			if ls.unsatisfied {
				found = ls
				return
			}
		}
	})
	return found
}

// ---------------------------------------------------------------------------
// Phase 1's output, and phase 4's resolution
// ---------------------------------------------------------------------------

// occKind is what a token said about a flag, before any interpretation.
type occKind int

const (
	occValue   occKind = iota // `--x v` or `--x=v`
	occBool                   // `--x` on a bool flag
	occNegated                // `--no-x`
	// occUnset is `--unset-x` on a nullable property of an update command
	// (§27.6). It supplies the property -- clearing is writing -- and delivers
	// absence with Provided() true.
	occUnset
)

// occurrence is one command-line mention of a flag, in command-line order.
type occurrence struct {
	name string
	// short is set when the token was a short form. A short may be claimed by
	// two mutually exclusive scopes, so name is provisional until the election
	// is resolved (§24.7).
	short string
	raw   string
	kind  occKind
}

// isMemberFlagName reports whether a name is the electing flag of a
// member-spelled choice.
func isMemberFlagName(idx *flagIndex, name string) bool {
	for _, s := range idx.sites[name] {
		ch := s.choice()
		if ch != nil && ch.member && memberFlag(ch) == s.flag {
			return true
		}
	}
	return false
}

// liveFlagFor resolves a supplied name to the declaration that is actually live
// this invocation. Scope validation has already guaranteed one exists.
func (st *electionState) liveFlagFor(cmd *Command, globalByName map[string]*Flag, name string) *Flag {
	for _, s := range cmd.index.sites[name] {
		if st.live[s.flag] {
			return s.flag
		}
	}
	return globalByName[name]
}

// liveFlagForShort resolves a short form to the declaration that is live this
// invocation.
func (st *electionState) liveFlagForShort(cmd *Command, short string) *Flag {
	for _, s := range cmd.index.shorts[short] {
		if st.live[s.flag] {
			return s.flag
		}
	}
	return nil
}

// electionSource maps an election's origin onto the framework's source
// vocabulary, so ctx.Source("via") and ctx.Provided("via") answer for a selector
// exactly as they answer for any flag (§24.5).
func electionSource(o electionOrigin) Source {
	switch o.kind {
	case "env":
		return SourceEnv
	case "config":
		return SourceConfig
	case "default":
		return SourceDefault
	}
	return SourceCLI
}

// resolveElected builds the ONE tagged value a selector delivers: the elected
// choice plus that choice's fields, resolved within the live scope only.
func (st *electionState) resolveElected(ls *liveSel, cliByFlag map[*Flag]interface{}, amb ambientSource, infraRoots map[string]string, stdinConsumedBy **string) (*Elected, string) {
	ch := ls.choice
	scope := ls.inner
	fields := Fields{}
	provided := map[string]bool{}
	suffix := st.scopedPresenceSuffix(scope.path)

	for i, f := range scope.flags {
		isMember := ch.member && i == 0
		key := flagParamName(f.Name)
		if isMember {
			// A payload is exactly one value, delivered under the reserved name
			// "value" (§24.4, §24.7). A payload-less (bool) member carries none:
			// electing it is the whole of what it says.
			if !choiceCarriesPayload(ch) {
				continue
			}
			key = scopeReservedValueName
		}

		if f.Type == TypeChoice {
			inner := st.bySel[f]
			if inner.unsatisfied {
				return nil, st.unsatisfiedSelectorError(inner)
			}
			nested, errMsg := st.resolveElected(inner, cliByFlag, amb, infraRoots, stdinConsumedBy)
			if errMsg != "" {
				return nil, errMsg
			}
			fields[key] = nested
			// An election supplied inside a record is one of that record's
			// fields, and every field a record supplies is labelled `default`
			// (§18.26 item 253).
			provided[key] = electionSource(inner.origin) != SourceDefault && !inner.recordField
			continue
		}

		val, src, errMsg := st.resolveScopedValue(f, isMember, ls, cliByFlag, amb, infraRoots, stdinConsumedBy, suffix)
		if errMsg != "" {
			return nil, errMsg
		}
		// The RECORD door runs the TYPE check and nothing else: the closed set
		// and the custom validation are deferred behind the distinction that
		// door cannot make (§18.26 item 254). The custom callback needs no
		// suppression of its own -- a record field is labelled `default`, and
		// §23.4 already says validate never runs on one -- but the closed-set
		// check runs on every resolved value, so this door's fields are
		// exempted here. The argv and flat doors are untouched.
		_, fromRecord := cliByFlag[f].(recordSupplied)
		if !fromRecord {
			if errMsg := validateChoices(f.Name, val, f.Repeatable, f.Choices, f.retiredChoices, false); errMsg != "" {
				return nil, errMsg
			}
		}
		isProvided := src == SourceCLI || src == SourceEnv || src == SourceConfig
		if f.Validate != nil && isProvided && val != nil {
			if f.Repeatable {
				if vals, ok := val.([]interface{}); ok {
					for _, v := range vals {
						if err := f.Validate(v); err != nil {
							return nil, errFlagValueError(f.Name, err.Error())
						}
					}
				}
			} else if err := f.Validate(val); err != nil {
				return nil, errFlagValueError(f.Name, err.Error())
			}
		}
		fields[key] = val
		provided[key] = isProvided
	}

	return &Elected{decl: ch, sel: ls.sel, Fields: fields, provided: provided}, ""
}

// resolveScopedValue applies CLI > env > config > default to one flag inside an
// elected scope. An env or config binding on a scoped flag is a CONDITIONAL
// BINDING: consulted exactly when its scope is elected, and otherwise never
// consulted at all (§24.6). A MEMBER flag consults neither, ever -- §21.3's
// rule, carried over with its reason intact.
func (st *electionState) resolveScopedValue(f *Flag, isMember bool, ls *liveSel, cliByFlag map[*Flag]interface{}, amb ambientSource, infraRoots map[string]string, stdinConsumedBy **string, suffix string) (interface{}, Source, string) {
	if v, ok := cliByFlag[f]; ok {
		if rs, isRecord := v.(recordSupplied); isRecord {
			// The RECORD door labels every field it delivers `default`: a scope
			// class fills a declared default at construction, so a field holding
			// its declared default cannot be told from one the caller wrote, and
			// `provided` answers "did the invocation cause this value" rather
			// than "does this value differ from the declaration" (§18.26 item
			// 253, §23.6). The one exception -- a RelativeToRoot default
			// resolved at this door, which reports `infra` -- needs nothing
			// here: an unsupplied field takes applyFlagDefault's marker branch
			// below, exactly as it does at every other door.
			return rs.value, SourceDefault, ""
		}
		return v, SourceCLI, ""
	}
	if isMember {
		if ls.memberImplied {
			// The selector's own default elected this payload-less member; the
			// declaration decided, so the source stays "default".
			return true, SourceDefault, ""
		}
		// A member flag is elected by its own token, and that token CARRIES the
		// payload: `--profile work`. So a payload-carrying member elected with
		// no value is the command line's own `--profile` with nothing after it,
		// and the refusal is that sentence. Only a programmatic door reaches
		// this state -- a record whose Fields omit "value", or a flat object
		// electing the member through the selector's key without its payload
		// key (§24.11). The ordinary required-flag message is refused for it,
		// because a member flag's scope path stops at the scope that OWNS it
		// (§12.13), so that message would render the member as its own owner.
		return nil, SourceCLI, errFlagRequiresValue("--" + f.Name)
	}
	if envVal, ok := amb.env(f.Env); ok {
		val, errStr := resolveFlagEnvValue(f, envVal, stdinConsumedBy)
		if errStr != "" {
			return nil, SourceEnv, errStr
		}
		return val, SourceEnv, ""
	}
	if raw, ok := amb.config(flagParamName(f.Name)); ok {
		coerced, errStr := coerceConfigValue(raw, f)
		if errStr != "" {
			return nil, SourceConfig, errConfigValueError(f.Name, errStr)
		}
		if f.Unique {
			if arr, ok := coerced.([]interface{}); ok {
				if dup := findDuplicate(arr); dup != nil {
					return nil, SourceConfig, errConfigValueDuplicate(f.Name, formatValueForError(dup))
				}
			}
		}
		return coerced, SourceConfig, ""
	}
	val, src, errMsg := applyFlagDefault(f, "", infraRoots)
	if errMsg != "" {
		// The scope suffix and the origin suffix, in that order (§12.13).
		return nil, src, errMsg + suffix
	}
	return val, src, ""
}
