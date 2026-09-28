package strictcli

import (
	"reflect"
	"strings"
)

// The update-command construct (contract §27).
//
// An update command declares ONE record -- the resource it changes, the write
// mode it changes it under, the declarations that name WHICH instance, and the
// declarations that name WHAT changes -- and the framework derives from it the
// mutating-default ban (§27.1), the at-least-one-property rule (§27.4), the
// write set and its two renderings (§27.5), and the clear vocabulary (§27.6).
//
// Absence resolving to a VALUE is banned; absence BOUNDING SCOPE is what a
// sparse update is (§27.13). The three properties that keep the second half
// legitimate are enforced here rather than promised: the write set is derived
// from ONE predicate (§23.6's provided, no source filter), it is never empty,
// and it is never invisible.

// WriteMode is the update's write mode. It is string-based on purpose: its zero
// value renders "" in errUpdateWriteModeInvalid, so the sentence stays
// byte-identical with its siblings' (§12.16, §27.8).
type WriteMode string

const (
	// WriteSparse sends only the provided properties; the resource's other
	// properties are not part of the request.
	WriteSparse WriteMode = "sparse"
	// WriteFullReplace sends the whole resource: the provided properties from
	// this invocation, and every other property read back and re-sent.
	WriteFullReplace WriteMode = "full_replace"
)

// The two parentheticals the write set's human line carries, a function of the
// write mode alone and always present (§27.5).
const (
	writeModeParenSparse      = "(other properties unchanged)"
	writeModeParenFullReplace = "(other properties are re-sent as read)"
)

// The two clauses the MCP description block's last line opens with -- the
// human log's two parentheticals in the same words (§27.10).
const (
	writeModeClauseSparse      = "left unchanged"
	writeModeClauseFullReplace = "re-sent as read"
)

// UpdateOption is one part of an update declaration. The interface is closed:
// the only values that satisfy it come from Identity and Properties, so a
// struct literal cannot declare half a record.
type UpdateOption interface{ applyUpdate(*updateDecl) }

type updateOptFunc func(*updateDecl)

func (fn updateOptFunc) applyUpdate(d *updateDecl) { fn(d) }

// Identity names the flags and args that say WHICH resource instance is being
// updated. It may be empty, and its members may be flags or positional args --
// a positional is the ordinary CLI spelling for naming a target
// (`myapp update-record <record-id>`). An identity member may also be a
// token- or member-spelled choice flag, which is how a resource with two
// addressing modes names itself (§27.3).
func Identity(names ...string) UpdateOption {
	return updateOptFunc(func(d *updateDecl) {
		d.identity = append(d.identity, names...)
	})
}

// Properties names the flags that say WHAT changes. Two parameters -- one named
// plus the variadic tail -- put a COMPILE-time floor of one on the list, which
// is §26.6's idiom at the arity this construct needs; the registration guard
// survives for the caller that omits the option entirely.
//
// A property is a flag, never a positional arg, and never a choice flag. It
// declares Optional() and nothing else: absence IS untouched (§27.3).
func Properties(first string, rest ...string) UpdateOption {
	return updateOptFunc(func(d *updateDecl) {
		d.properties = append(d.properties, first)
		d.properties = append(d.properties, rest...)
	})
}

// WithUpdateOf declares that this command performs a partial update of one
// resource (§27.2).
//
// The mode is a POSITIONAL parameter, which is Go's spelling of mandatory:
// there is no option to forget and no zero-valued struct field to fill in
// silently. Declaring it on a read_only command is a registration-time hard
// error -- a command that changes nothing writes no properties -- so an update
// command is always mutating, which is what makes §27.1's ban apply to every
// one of its declarations without a second rule.
func WithUpdateOf(resource string, mode WriteMode, opts ...UpdateOption) CmdOption {
	return func(c *Command) {
		d := &updateDecl{resource: resource, mode: mode}
		for _, opt := range opts {
			opt.applyUpdate(d)
		}
		c.updateOf = d
	}
}

// Nullable declares that this property can be CLEARED, minting `--unset-<prop>`
// (§27.6). It is an ordinary FlagOption beside Required(), Optional() and
// Default(v), and it is legal only on a property of an update.
//
// The minted flag delivers no kwarg of its own: an unset property delivers
// absence, reports Provided() true, and is answered by ctx.Unset(name). It is
// not negatable -- `--no-unset-<prop>` says exactly what absence already says.
func Nullable() FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.Nullable = true
	})
}

// updateDecl is the internal representation of one command's update record.
type updateDecl struct {
	resource   string
	mode       WriteMode
	identity   []string
	properties []string

	// propFlags indexes cmd.flags, one entry per declared property, in
	// declaration order. Filled at registration.
	propFlags []int
}

// unsetFlagName is the flag the framework mints for a nullable property.
func unsetFlagName(prop string) string { return "unset-" + prop }

// --- Registration (§27.11) ---
//
// The steps run in the pinned order over the whole declaration, so three
// implementations report the same FIRST error for a declaration with two
// faults. The order runs from the command's own classification, through the
// record's identity, outward to the declarations it names -- §26.8's direction
// -- and each step crosses the whole declaration before the next begins, so a
// message never blames a name for a fault in the record that names it.
func validateUpdate(cmdName string, cmd *Command, globalFlags []Flag) {
	// Step 1: the mutating-default ban. It runs first because it is a fact
	// about the command's CLASSIFICATION and is independent of whether an
	// update is declared at all.
	validateMutatingDefaults(cmdName, cmd)

	d := cmd.updateOf

	// Step 2: classification legality.
	if d != nil && cmd.Effect == EffectReadOnly {
		panic(errUpdateOnReadOnly(cmdName))
	}

	if d != nil {
		// Step 3: record legality -- the resource name's charset, the write
		// mode's vocabulary, at least one property.
		if !isKebabName(d.resource) {
			panic(errUpdateResourceCharset(cmdName, d.resource))
		}
		if d.mode != WriteSparse && d.mode != WriteFullReplace {
			panic(errUpdateWriteModeInvalid(cmdName, string(d.mode)))
		}
		if len(d.properties) == 0 {
			panic(errUpdatePropertiesEmpty(cmdName, d.resource))
		}

		// Step 4: name resolution. Every name in either list resolves to
		// exactly one flag or arg; unknown, ambiguous, duplicated and
		// both-roles names refuse here.
		type resolvedRole struct {
			property bool
			flagIdx  int
			argIdx   int
			scope    string
		}
		roles := map[string]*resolvedRole{}
		var scoped []string
		resolve := func(name string, property bool) {
			flagIdx := cmd.rootFlagIndex(name)
			argIdx := cmd.argIndex(name)
			if flagIdx >= 0 && argIdx >= 0 {
				panic(errUpdateNameAmbiguous(cmdName, d.resource, name))
			}
			path := ""
			if flagIdx < 0 && argIdx < 0 {
				// A SCOPED flag resolves as a flag and is refused by the scope
				// step below; reporting it as unknown would name the wrong
				// fault (§24.8, §27.3).
				p := cmd.index.scopedFlagPath(name)
				if p == nil {
					panic(errUpdateNameUnknown(cmdName, d.resource, name))
				}
				path = renderScopePath(p)
			}
			if prev, seen := roles[name]; seen {
				if prev.property == property {
					panic(errUpdateNameDuplicate(cmdName, d.resource, name))
				}
				panic(errUpdateNameBothRoles(cmdName, d.resource, name))
			}
			roles[name] = &resolvedRole{property: property, flagIdx: flagIdx, argIdx: argIdx, scope: path}
			if path != "" {
				scoped = append(scoped, name)
			}
		}
		for _, name := range d.identity {
			resolve(name, false)
		}
		for _, name := range d.properties {
			resolve(name, true)
		}

		// Step 5: scope. A property inside a scope would have a write-set
		// membership the argv and flat doors could answer and the record door
		// could not -- one rule with three answers (§27.3).
		for _, name := range scoped {
			panic(errUpdateReferencesScopedFlag(cmdName, d.resource, name, roles[name].scope))
		}

		// Step 6: role legality -- a property that is a positional arg, then a
		// property that is a choice flag.
		for _, name := range d.properties {
			if roles[name].argIdx >= 0 {
				panic(errUpdatePropertyIsArg(cmdName, d.resource, name))
			}
		}
		for _, name := range d.properties {
			if cmd.flags[roles[name].flagIdx].Type == TypeChoice {
				panic(errUpdatePropertyIsChoiceFlag(cmdName, d.resource, name))
			}
		}

		// Step 7: presence legality. A property the invocation must always
		// supply is written in every invocation, which makes the at-least-one
		// rule unfireable and turns a sparse update into a partial full replace
		// under a name that denies it. A property declaring a DEFAULT was
		// already refused by step 1's ban, an update command being mutating by
		// step 2's own guard.
		for _, name := range d.properties {
			f := &cmd.flags[roles[name].flagIdx]
			if f.presence == presenceRequired {
				panic(errUpdatePropertyPresence(cmdName, d.resource, renderDeclFlag(name)))
			}
		}

		d.propFlags = make([]int, 0, len(d.properties))
		for _, name := range d.properties {
			d.propFlags = append(d.propFlags, roles[name].flagIdx)
		}
	}

	// Step 8: the clear vocabulary. It runs LAST because the name reservation
	// is the only step that reads the flag namespace back after the property
	// set is known.
	properties := map[string]bool{}
	if d != nil {
		for _, name := range d.properties {
			properties[name] = true
		}
	}
	for i := range cmd.flags {
		f := &cmd.flags[i]
		if f.Nullable && !properties[f.Name] {
			panic(errNullableNotProperty(cmdName, renderDeclFlag(f.Name)))
		}
	}
	if d == nil {
		return
	}
	for _, name := range d.properties {
		if !cmd.flags[cmd.rootFlagIndex(name)].Nullable {
			continue
		}
		minted := unsetFlagName(name)
		// The whole flag namespace this command's tokenizer reads, which
		// includes the app's globals: they are recognized after the command
		// name too, so a global of the minted name would be unreachable.
		if cmd.rootFlagIndex(minted) >= 0 || cmd.index.scopedFlagPath(minted) != nil {
			panic(errUnsetNameReserved(cmdName, name))
		}
		for i := range globalFlags {
			if globalFlags[i].Name == minted {
				panic(errUnsetNameReserved(cmdName, name))
			}
		}
	}
}

// renderDeclFlag is §12.16's `<decl>` for a flag; renderDeclArg is its
// positional twin. A single name standing alone inside a sentence is quoted,
// and the quoting differs by surface exactly as it does everywhere else.
func renderDeclFlag(name string) string { return "flag '--" + name + "'" }

func renderDeclArg(name string) string { return "argument '" + name + "'" }

// validateMutatingDefaults is §27.1's ban: on a command declaring
// effect="mutating", a flag or a positional arg may not declare a value
// default.
//
// The reason is one sentence and every cell is derived from it: absence must
// never resolve to a value the invocation did not state, because on a mutating
// command a value the framework picked is a value the framework WRITES.
//
// It is evaluated PER COMMAND, over the flags and args that command carries --
// its own, its flag sets' (already merged into cmd.flags here) and its
// selectors' scoped flags at every depth -- so a shared flag set carrying a
// default is legal and attaching it to a mutating command is not. App-level
// global flags are NOT reached: a global has no classification of its own, and
// there is no command at the point of its declaration to key on (§27.1's
// stated hole).
func validateMutatingDefaults(cmdName string, cmd *Command) {
	if cmd.Effect != EffectMutating {
		return
	}
	var walk func(flags []Flag)
	walk = func(flags []Flag) {
		for i := range flags {
			f := &flags[i]
			// A SELECTOR's default names which scope is live rather than a
			// value written to anything, and B2's own remedy for an absent
			// selection is to name the choice -- which is what a default
			// election does. The scope beneath it is another matter: those are
			// ordinary flags of a mutating command, reached at every depth.
			if f.Type == TypeChoice {
				for _, ch := range f.choiceDecls {
					walk(ch.Flags)
				}
				continue
			}
			if f.presence != presenceDefault {
				continue
			}
			if !bannedDefault(f.Default) {
				continue
			}
			panic(errMutatingDefault(cmdName, renderDeclFlag(f.Name), formatValueForError(f.Default)))
		}
	}
	walk(cmd.flags)
	for i := range cmd.args {
		a := &cmd.args[i]
		if a.presence != presenceDefault || !bannedDefault(a.Default) {
			continue
		}
		panic(errMutatingDefault(cmdName, renderDeclArg(a.Name), formatValueForError(a.Default)))
	}
}

// bannedDefault reports whether a declared default is a TOOL-PICKED VALUE.
//
// Every scalar is one, `""` and `0` included, and so is a NON-EMPTY list or
// dict: `Default([]interface{}{"a"})` is as tool-picked as `Default(300)`, and
// the shape of the container changes nothing about what reaches the write.
//
// Two carve-outs, both derived from the ban's own reason (§18.33 item 301):
//
//   - an EMPTY collection declares no elements, so no value the framework chose
//     can reach a write through it, and §23.5 made this spelling the explicit
//     replacement for the framework's own silent [];
//   - a RelativeToRoot default resolves a LOCATION under a declared
//     infrastructure root, deciding where a command writes and never what; its
//     source label is `infra` and Provided() answers false.
func bannedDefault(v interface{}) bool {
	switch v.(type) {
	case nil:
		// Default(nil) has its own refusal (§23.1) and never reaches a write.
		return false
	case InfraRootPath, *InfraRootPath:
		return false
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Slice, reflect.Map, reflect.Array:
		return reflect.ValueOf(v).Len() > 0
	}
	return true
}

// --- The write set (§27.5) ---

// updateState is one invocation's answer to an update declaration: the ordered
// pair of the properties it writes and the properties it clears, plus the two
// readings of "the rest". It is computed at parse time from §27.4's predicate
// and rendered wherever a run reports what it does.
type updateState struct {
	decl      *updateDecl
	written   []string // declared names, declaration order
	cleared   []string
	resent    []string
	untouched []string
}

// evaluateUpdate enforces the at-least-one-property rule and computes the write
// set (§27.4, §27.5).
//
// A property is provided exactly when §23.6's predicate says so, and there is
// no source filter: a value from env, from config or injected by an Implies is
// a provision. A negated bool property is a provision too -- inside an update
// command `--no-proxied` WRITES false -- and so is an unset, clearing being
// writing.
func evaluateUpdate(cmd *Command, store *sourcedStore, unsets map[string]bool) (*updateState, string) {
	d := cmd.updateOf
	if d == nil {
		return nil, ""
	}
	st := &updateState{decl: d}
	for _, name := range d.properties {
		switch {
		case unsets[name]:
			st.cleared = append(st.cleared, name)
		case store.isPresentForDeps(name):
			st.written = append(st.written, name)
		case d.mode == WriteFullReplace:
			// A full-replace write touches every property, so nothing is
			// untouched: the rest is read back and re-sent.
			st.resent = append(st.resent, name)
		default:
			st.untouched = append(st.untouched, name)
		}
	}
	if len(st.written) == 0 && len(st.cleared) == 0 {
		return nil, errUpdateNoProperty(d.resource, renderPropertyTokens(d.properties))
	}
	return st, ""
}

// renderPropertyTokens renders every declared property as a CLI token, unquoted
// and joined by ", " in declaration order (§12.16's list rule).
func renderPropertyTokens(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = "--" + n
	}
	return strings.Join(parts, ", ")
}

// paren is the trailing parenthetical of the human write-set line: a function
// of the write mode alone, and always present in both segment shapes.
func (st *updateState) paren() string {
	if st.decl.mode == WriteFullReplace {
		return writeModeParenFullReplace
	}
	return writeModeParenSparse
}

// logLine renders the would-do log's unnumbered write-set line (§27.5), without
// the two-space indent the log adds.
//
// Two segments, `writes:` first, separated by "; ", with an empty segment
// omitted entirely -- §27.4's rule guarantees at least one survives, so the
// line is never empty and never has to say that it is. Names are the
// properties' declared names WITHOUT the `--` prefix: the log is the human
// surface, where the reader knows a declaration by the name they type, and the
// write set is data, which is why the token's prefix comes off.
func (st *updateState) logLine() string {
	var segments []string
	if len(st.written) > 0 {
		segments = append(segments, "writes: "+strings.Join(st.written, ", "))
	}
	if len(st.cleared) > 0 {
		segments = append(segments, "clears: "+strings.Join(st.cleared, ", "))
	}
	return strings.Join(segments, "; ") + " " + st.paren()
}

// writesEnvelope is the envelope's `writes` member (§19.2's amendment, §27.5).
// The struct's field order is the pinned key order.
type writesEnvelope struct {
	Resource  string   `json:"resource"`
	WriteMode string   `json:"write_mode"`
	Written   []string `json:"written"`
	Cleared   []string `json:"cleared"`
	Resent    []string `json:"resent"`
	Untouched []string `json:"untouched"`
}

// envelopeMember renders the machine rendering of the write set. The four
// arrays hold UNDERSCORED parameter names in declaration order and partition
// the declared property set exactly: every property appears in exactly one of
// them. `resent` and `untouched` are the two readings of "the rest", and
// exactly one of them is ever non-empty.
func (st *updateState) envelopeMember() *writesEnvelope {
	return &writesEnvelope{
		Resource:  st.decl.resource,
		WriteMode: string(st.decl.mode),
		Written:   paramNames(st.written),
		Cleared:   paramNames(st.cleared),
		Resent:    paramNames(st.resent),
		Untouched: paramNames(st.untouched),
	}
}

func paramNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, flagParamName(n))
	}
	return out
}

// --- The MCP projection (§27.10) ---

// updateAnyOfBranches projects the at-least-one-property rule as one `required`
// branch per property, in declaration order. Its fidelity is EXACT: the rule IS
// provision at this door -- a supplied key is a provided property, a null is a
// supplied key and a clear, a false is a supplied key and a write -- so
// `required` states the whole rule with nothing left over.
func updateAnyOfBranches(cmd *Command) []interface{} {
	if cmd.updateOf == nil {
		return nil
	}
	out := make([]interface{}, 0, len(cmd.updateOf.properties))
	for _, name := range cmd.updateOf.properties {
		out = append(out, map[string]interface{}{
			"required": []interface{}{flagParamName(name)},
		})
	}
	return out
}

// updateDescriptionLines renders the tool description's update block (§27.10),
// in the shape §24.11's scope block and §26.12's constraint block already
// established. Members render in PROPERTY names, like every other member in
// this block: the caller writes keys, not argv.
func updateDescriptionLines(cmd *Command) (string, []string) {
	d := cmd.updateOf
	if d == nil {
		return "", nil
	}
	header := "Update of \"" + d.resource + "\" (write mode: " + string(d.mode) + "):"
	var lines []string
	// The `identifies:` line is omitted when the resource declares no identity
	// members.
	if len(d.identity) > 0 {
		lines = append(lines, "identifies: "+strings.Join(paramNames(d.identity), ", "))
	}
	lines = append(lines, "writes: "+strings.Join(paramNames(d.properties), ", ")+" -- at least one is required")
	clause := writeModeClauseSparse
	if d.mode == WriteFullReplace {
		clause = writeModeClauseFullReplace
	}
	last := "a property that is not supplied is " + clause
	// The `; null clears <list>` clause appears only when at least one property
	// is nullable, naming them in declaration order.
	var nullable []string
	for i, name := range d.properties {
		if cmd.flags[d.propFlags[i]].Nullable {
			nullable = append(nullable, flagParamName(name))
		}
	}
	if len(nullable) > 0 {
		last += "; null clears " + strings.Join(nullable, ", ")
	}
	return header, append(lines, last)
}

// --- The schema encoding (§27.9) ---

// serializeUpdateOf publishes the declaration COMPLETELY rather than
// indicatively: a consumer reconstructs the rule without re-reading the
// declaration. Names are published in the DECLARED spelling, matching the flag
// entry's own `name`; the underscored spelling belongs to the machine doors.
func serializeUpdateOf(d *updateDecl) *schemaObject {
	identity := make([]interface{}, 0, len(d.identity))
	for _, n := range d.identity {
		identity = append(identity, n)
	}
	properties := make([]interface{}, 0, len(d.properties))
	for _, n := range d.properties {
		properties = append(properties, n)
	}
	return newSchemaObject().
		set("resource", d.resource).
		set("identity", identity).
		set("properties", properties)
}
