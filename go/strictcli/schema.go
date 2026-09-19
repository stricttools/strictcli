package strictcli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The dumped schema, version 2 (effects contract §25).
//
// Every flag and arg entry carries a `value_schema`: a real JSON Schema
// fragment from the closed four-keyword subset (`type`, `items`,
// `additionalProperties`, `enum`) with JSON Schema's own type names. The v1
// `type` key is gone, and so is `repeatable` -- arity is a property of the
// VALUE, so a repeatable scalar flag publishes the identical array fragment a
// `list[T]` flag does. A SELECTOR carries no fragment at all, and its absence
// is the declaration: a variant is inexpressible in the closed subset, and the
// presence of `elect_by` is what tells a reader which shape it is holding.
//
// Keys are emitted in the order §25.9 pins, at every depth, and the document is
// written by the canonical writer in schema_json.go.

// flagTypeName maps FlagType to its string representation. The dumped schema no
// longer uses it -- v2 publishes fragments -- but help rendering and several
// error templates name a carrier in strictcli's own vocabulary.
var flagTypeName = map[FlagType]string{
	TypeStr:       "str",
	TypeBool:      "bool",
	TypeInt:       "int",
	TypeFloat:     "float",
	TypeChoice:    "choice",
	TypeListStr:   "list[str]",
	TypeListInt:   "list[int]",
	TypeListFloat: "list[float]",
	TypeDictStr:   "dict[str]",
	TypeDictInt:   "dict[int]",
	TypeDictFloat: "dict[float]",
}

// jsonSchemaTypeName maps a SCALAR carrier to JSON Schema's own type name.
// `<T>` is always JSON Schema's name, never strictcli's (§25.2).
var jsonSchemaTypeName = map[FlagType]string{
	TypeStr:   "string",
	TypeBool:  "boolean",
	TypeInt:   "integer",
	TypeFloat: "number",
}

// scalarFragment is one scalar row of §25.2's fragment table, with its `enum`
// if the declaration carries choices. Keys are emitted in the subset's own
// order: type, items, additionalProperties, enum.
func scalarFragment(t FlagType, choices []interface{}) *schemaObject {
	frag := newSchemaObject().set("type", jsonSchemaTypeName[t])
	if choices != nil {
		frag.set("enum", append([]interface{}{}, choices...))
	}
	return frag
}

// flagValueSchema is the fragment describing the value a FLAG delivers.
//
// A `list[T]` flag and a repeatable scalar `T` flag converge on the same array
// fragment, which is what §25.3 means by arity being a property of the value
// rather than of the spelling. A dict's keys are `string` structurally, so
// `additionalProperties` carrying the value type is a complete description.
//
// An optional flag emits the plain type: there is no `null` in any fragment and
// no type list, because presence is the sole authority on absence.
func flagValueSchema(f *Flag) *schemaObject {
	if IsDictType(f.Type) {
		return newSchemaObject().
			set("type", "object").
			set("additionalProperties", scalarFragment(ItemType(f.Type), nil))
	}
	if IsListType(f.Type) || f.Repeatable {
		return newSchemaObject().
			set("type", "array").
			set("items", scalarFragment(ItemType(f.Type), f.Choices))
	}
	return scalarFragment(f.Type, f.Choices)
}

// argValueSchema is the fragment for a positional arg, in either spelling of a
// variadic: the element-carrier one and the list-carrier one publish the same
// array row (§25.4).
func argValueSchema(a *Arg) *schemaObject {
	if a.IsVariadic {
		return newSchemaObject().
			set("type", "array").
			set("items", scalarFragment(ItemType(a.Type), a.Choices))
	}
	return scalarFragment(a.Type, a.Choices)
}

// serializeDefault converts a default value to a JSON-serializable form. A
// RelativeToRoot marker (InfraRootPath) has unexported fields that would marshal
// to an empty object; instead it is serialized machine-stably as
// {"relative_to_root": {"env_var": ..., "parts": [...]}} -- only the declared env
// var and path parts, never a resolved machine-specific path. This shape is
// identical to the Python implementation so the schema cross-language byte-compares.
// All other values pass through unchanged.
func serializeDefault(v interface{}) interface{} {
	m, ok := v.(InfraRootPath)
	if !ok {
		return v
	}
	parts := make([]interface{}, len(m.parts))
	for i, p := range m.parts {
		parts[i] = p
	}
	return newSchemaObject().set(
		"relative_to_root",
		newSchemaObject().set("env_var", m.envVar).set("parts", parts),
	)
}

// presenceName renders a resolved presence declaration for the dumped schema.
// The three names are the canonical ones the contract pins (§13, §23.7).
func presenceName(p presenceKind) string {
	switch p {
	case presenceRequired:
		return "required"
	case presenceOptional:
		return "optional"
	case presenceDefault:
		return "default"
	}
	// Unreachable: an undeclared flag or arg does not register.
	return ""
}

// serializeChoiceRecords is a value flag's (or arg's) `choices=` entries, as
// the records item 164 made them (§25.5).
//
// The machine-readable half of a choices declaration lives in the fragment's
// `enum`; this is the human-readable half, which JSON Schema has no vocabulary
// for. `help` is OMITTED when the entry declares none, so Go's empty-string
// spelling of "no help" and an absent one cannot produce different bytes for
// the same declaration.
func serializeChoiceRecords(records []ChoiceValue) []interface{} {
	out := make([]interface{}, 0, len(records))
	for _, r := range records {
		entry := newSchemaObject().set("value", r.Value)
		if r.Help != "" {
			entry.set("help", r.Help)
		}
		out = append(out, entry)
	}
	return out
}

// serializeRetiredChoices publishes a declaration's retired spellings as a map
// from the spelling to its message, SORTED ascending by key -- the treatment a
// group's `deprecated` map already gets, and for the same reason: a keyed
// object whose declaration order no implementation is required to retain has
// sort order as its only reachable canon.
//
// The key is the spelling rendered through the error-value formatter, so an
// int, a float and a string key identically in all three implementations. The
// map is omitted entirely when nothing is retired.
func serializeRetiredChoices(retired []RetiredChoiceValue) *schemaObject {
	messages := make(map[string]string, len(retired))
	for _, rc := range retired {
		messages[formatValueForError(rc.Value)] = rc.Message
	}
	out := newSchemaObject()
	for _, key := range sortedStringMapKeys(messages) {
		out.set(key, messages[key])
	}
	return out
}

// serializeFlagMember serializes one member of a command's flag list: a
// selector when it declares choices, an ordinary flag entry otherwise. Flags
// and selectors share ONE array, interleaved in declaration order -- a selector
// IS a flag (§24.2), and `elect_by` is what tells a reader which it is holding.
func serializeFlagMember(f *Flag) *schemaObject {
	if f.Type == TypeChoice {
		return serializeSelector(f)
	}
	return serializeFlag(f)
}

// serializeFlag converts a Flag to an ordered entry, in §25.9's key order:
// name, help, value_schema, short, presence, default, env, env_separator,
// prefixed, choices, retired_choices, elect_by, unique, conflict_mode,
// negatable, nullable.
func serializeFlag(f *Flag) *schemaObject {
	d := newSchemaObject().
		set("name", f.Name).
		set("help", f.Help).
		set("value_schema", flagValueSchema(f))
	if f.Short != "" {
		d.set("short", f.Short)
	}
	// Presence is ALWAYS emitted: it is a mandatory declaration, so there is no
	// baseline to omit against (contract §13's presence-round amendment).
	d.set("presence", presenceName(f.presence))
	// `default` is emitted exactly when presence is "default", and then always,
	// whatever the value: [], {}, "", false and 0 are declarations rather than
	// the absence of one.
	if f.presence == presenceDefault {
		d.set("default", serializeDefault(f.Default))
	}
	if f.Env != "" {
		d.set("env", f.Env)
	}
	if f.EnvSeparator != "" {
		d.set("env_separator", f.EnvSeparator)
	}
	// Omitted when true, which is the framework's own behavior: the key appears
	// exactly on the flags that depart from it (§25.11).
	if !f.Prefixed {
		d.set("prefixed", false)
	}
	if f.choiceRecords != nil {
		d.set("choices", serializeChoiceRecords(f.choiceRecords))
	}
	if len(f.retiredChoices) > 0 {
		d.set("retired_choices", serializeRetiredChoices(f.retiredChoices))
	}
	if f.Unique {
		d.set("unique", true)
	}
	// Per-flag conflict mode: serialized only when explicitly set. Absence means
	// "inherit the app default", which v2 publishes as `config_conflict_mode`,
	// so the effective mode is finally computable from the dump alone (§25.11).
	if f.hasConflictMode {
		d.set("conflict_mode", f.ConflictMode)
	}
	if f.Type == TypeBool && !f.Repeatable {
		d.set("negatable", f.Negatable)
	}
	// Emitted only when declared true; absence means the property cannot be
	// cleared, which is the baseline. There is NO second flag entry for the
	// minted `--unset-<prop>`: it is derived from this key exactly as
	// `--no-<x>` is derived from `negatable`, and the dump publishes
	// declarations (contract §13's amendment, §27.9).
	if f.Nullable {
		d.set("nullable", true)
	}
	return d
}

// serializeChoiceObject is one choice of one selector: `name`, `help`, and its
// scope (§25.6). `flags` is omitted when the scope is empty.
//
// A member-spelled choice's PAYLOAD is the first entry of that array, under the
// reserved name `value` with `presence: "required"` -- the payload is supplied
// by electing the member, and required-once-elected is exactly what a member
// flag's presence means. A payload-less member has no `value` entry.
func serializeChoiceObject(ch *ChoiceDecl) *schemaObject {
	entry := newSchemaObject().set("name", ch.Name).set("help", ch.Help)
	var scope []interface{}
	start := 0
	if ch.member {
		start = 1
		if choiceCarriesPayload(ch) {
			payload := serializeFlag(memberFlag(ch))
			payload.set("name", scopeReservedValueName)
			payload.set("presence", presenceName(presenceRequired))
			scope = append(scope, payload)
		}
	}
	for j := start; j < len(ch.Flags); j++ {
		scope = append(scope, serializeFlagMember(&ch.Flags[j]))
	}
	if len(scope) > 0 {
		entry.set("flags", scope)
	}
	return entry
}

// serializeSelectorDefault renders a selector's declared default as the flat
// map §25.6 pins: `{"choice": "<name>", "<field>": <value>, ...}` -- the
// choice's name under the reserved key `choice`, then each field that HAS a
// value in the default selection, in declaration order.
//
// A field with no value is omitted, which is unambiguous because `null` is not a
// declarable default anywhere in the framework, and a nested selector is
// excluded: it publishes its own default on its own entry. Go's default names a
// choice whose scope registration already proved complete, which is what makes
// this the same map Python's default INSTANCE produces.
func serializeSelectorDefault(sel *Flag) *schemaObject {
	name, _ := sel.Default.(string)
	ch := findChoice(sel, name)
	flat := newSchemaObject().set("choice", name)
	if ch == nil {
		return flat
	}
	start := 0
	if ch.member {
		// Only a payload-less member can be a default, so the electing flag
		// contributes no field.
		start = 1
	}
	for j := start; j < len(ch.Flags); j++ {
		f := &ch.Flags[j]
		if f.Type == TypeChoice || f.presence != presenceDefault {
			continue
		}
		flat.set(f.Name, serializeDefault(f.Default))
	}
	return flat
}

// serializeSelector serializes one selector in the encoding §25.6 pins.
//
// A selector has NO `value_schema`. Its value is a variant -- one tagged record
// chosen from several, each with a different set of fields -- and the closed
// four-keyword subset cannot express one; publishing a wrong fragment would be
// worse than publishing none, because a reader would validate against it. Each
// scoped entry is a FULL flag entry, which is what makes recursion free: a
// nested selector is an entry inside a `flags` array carrying its own `choices`
// and `elect_by`, to any depth.
func serializeSelector(sel *Flag) *schemaObject {
	d := newSchemaObject().set("name", sel.Name).set("help", sel.Help)
	if sel.Short != "" {
		d.set("short", sel.Short)
	}
	d.set("presence", presenceName(sel.presence))
	if sel.presence == presenceDefault {
		d.set("default", serializeSelectorDefault(sel))
	}
	if sel.Env != "" {
		d.set("env", sel.Env)
	}
	choices := make([]interface{}, 0, len(sel.choiceDecls))
	for _, ch := range sel.choiceDecls {
		choices = append(choices, serializeChoiceObject(ch))
	}
	d.set("choices", choices)
	// The discriminator, in §24.12's own two-value vocabulary.
	if sel.memberSpelled {
		d.set("elect_by", "member-flags")
	} else {
		d.set("elect_by", "selector-token")
	}
	return d
}

// serializeArg converts an Arg to an ordered entry, in §25.9's key order:
// name, help, value_schema, presence, default, variadic, choices.
//
// `variadic` SURVIVES the arity rule that deleted `repeatable`, and the
// asymmetry is deliberate: it names a token-consumption rule -- this arg takes
// every remaining positional token, and only the last arg may -- which a
// consumer needs in order to render `<files>...` in a usage line.
func serializeArg(a *Arg) *schemaObject {
	d := newSchemaObject().
		set("name", a.Name).
		set("help", a.Help).
		set("value_schema", argValueSchema(a)).
		set("presence", presenceName(a.presence))
	if a.presence == presenceDefault {
		d.set("default", serializeDefault(a.Default))
	}
	if a.IsVariadic {
		d.set("variadic", true)
	}
	if a.choiceRecords != nil {
		d.set("choices", serializeChoiceRecords(a.choiceRecords))
	}
	if len(a.retiredChoices) > 0 {
		d.set("retired_choices", serializeRetiredChoices(a.retiredChoices))
	}
	return d
}

// serializeCommand converts a Command to an ordered entry.
// Fields matching their defaults are omitted; see buildSchemaDefaults().
func serializeCommand(cmd *Command) *schemaObject {
	m := newSchemaObject().
		set("name", cmd.Name).
		set("help", cmd.Help).
		// Always emitted: classification is mandatory, so there is no default
		// to omit against.
		set("effect", cmd.Effect)
	// consequential: NOT mandatory; absence means "not consequential"
	// (contract §8.1, §13), so it is omitted when false.
	if cmd.Consequential {
		m.set("consequential", true)
	}
	// Emitted only when declared: dry run is supported unless a command says
	// otherwise, so the pair appears exactly on the commands that refuse it.
	if !cmd.DryRunSupported {
		m.set("dry_run_supported", false)
		m.set("dry_run_unsupported_reason", cmd.DryRunUnsupportedReason)
	}
	// The update declaration, published as TWO command-entry keys where it is
	// one nested record on the declaration surface (contract §13's amendment,
	// §27.9): a consumer asking what write mode a command has should not have
	// to descend, and a consumer WRITING a declaration must not be able to
	// spell half of one. The pair is atomic by construction rather than by a
	// guard -- the two facts are one declaration.
	if cmd.updateOf != nil {
		m.set("update_of", serializeUpdateOf(cmd.updateOf))
		m.set("write_mode", string(cmd.updateOf.mode))
	}
	// The payload contract, published verbatim (contract §19.5): the inline
	// literal is the sole canonical artifact, so the dump carries it as
	// written rather than a re-rendering of it.
	if cmd.PayloadSchema != nil {
		m.set("payload_schema", cmd.PayloadSchema)
	}
	// Emitted only when declared true; absence means the framework owns stdout,
	// which is the baseline (contract §13's 2026-08-13 amendment, §19.6).
	if cmd.OwnsStdout {
		m.set("owns_stdout", true)
	}
	if cmd.Passthrough {
		m.set("passthrough", true)
	}
	if len(cmd.flags) > 0 {
		flags := make([]interface{}, 0, len(cmd.flags))
		for i := range cmd.flags {
			flags = append(flags, serializeFlagMember(&cmd.flags[i]))
		}
		m.set("flags", flags)
	}
	// The grouping v1 discarded when it merged a set's flags into the command's
	// flag list. Members keep their ordinary entries above, so this adds a
	// grouping without duplicating a declaration (§25.11).
	if len(cmd.flagSets) > 0 {
		sets := make([]interface{}, 0, len(cmd.flagSets))
		for _, fs := range cmd.flagSets {
			names := make([]interface{}, len(fs.Flags))
			for i, f := range fs.Flags {
				names[i] = f.Name
			}
			sets = append(sets, newSchemaObject().set("name", fs.Name).set("flags", names))
		}
		m.set("flag_sets", sets)
	}
	if len(cmd.args) > 0 {
		args := make([]interface{}, 0, len(cmd.args))
		for i := range cmd.args {
			args = append(args, serializeArg(&cmd.args[i]))
		}
		m.set("args", args)
	}
	if len(cmd.tags) > 0 {
		sorted := make([]string, len(cmd.tags))
		copy(sorted, cmd.tags)
		sort.Strings(sorted)
		tags := make([]interface{}, len(sorted))
		for i, t := range sorted {
			tags[i] = t
		}
		m.set("tags", tags)
	}
	constraints := serializeConstraints(cmd)
	if len(constraints) > 0 {
		m.set("constraints", constraints)
	}
	if cmd.Hidden {
		m.set("hidden", true)
	}
	if cmd.Interactive {
		m.set("interactive", true)
	}
	if len(cmd.configFields) > 0 {
		cfList := make([]interface{}, len(cmd.configFields))
		for i, f := range cmd.configFields {
			cfList[i] = f
		}
		m.set("config_fields", cfList)
	}
	// grants: omitted when empty; entries in declaration order.
	if len(cmd.Grants) > 0 {
		grants := make([]interface{}, 0, len(cmd.Grants))
		for _, g := range cmd.Grants {
			grants = append(grants, newSchemaObject().
				set("name", g.Name).
				set("reason", g.Reason).
				set("kind", g.Kind))
		}
		m.set("grants", grants)
	}
	// forwarding: omitted when absent. The private framework-internal marker is
	// NOT emitted.
	if cmd.Forwarding != nil {
		m.set("forwarding", newSchemaObject().set("reason", cmd.Forwarding.Reason))
	}
	return m
}

// serializeConstraints lives in constraints.go, beside the declarations it
// encodes. The "mutex" entry was deleted with MutexGroup and "co_required" with
// CoRequired: exactly-one left the constraint system for the selector, and
// all-or-none absorbed co-required by rename (contract §25.7's amendment).

// serializeGroup converts a Group to an ordered entry (recursive), in §25.9's
// key order: name, help, commands, groups, deprecated, tags, hidden.
func serializeGroup(grp *Group) *schemaObject {
	m := newSchemaObject().set("name", grp.Name).set("help", grp.Help)
	// commands and groups keep DECLARATION order, which every implementation
	// retains (§25.9).
	if len(grp.Commands) > 0 {
		commands := newSchemaObject()
		for _, name := range grp.order {
			commands.set(name, serializeCommand(grp.Commands[name]))
		}
		m.set("commands", commands)
	}
	if len(grp.Groups) > 0 {
		groups := newSchemaObject()
		for _, name := range grp.groupOrder {
			groups.set(name, serializeGroup(grp.Groups[name]))
		}
		m.set("groups", groups)
	}
	// `deprecated` is emitted SORTED ascending by key: no implementation retains
	// a declaration order for it (§25.9).
	if len(grp.deprecatedMap) > 0 {
		deprecated := newSchemaObject()
		for _, name := range sortedStringMapKeys(grp.deprecatedMap) {
			deprecated.set(name, grp.deprecatedMap[name])
		}
		m.set("deprecated", deprecated)
	}
	// tags: own tags only, not accumulated
	if len(grp.tags) > 0 {
		sorted := make([]string, len(grp.tags))
		copy(sorted, grp.tags)
		sort.Strings(sorted)
		tags := make([]interface{}, len(sorted))
		for i, t := range sorted {
			tags[i] = t
		}
		m.set("tags", tags)
	}
	if grp.Hidden {
		m.set("hidden", true)
	}
	return m
}

// sortedStringMapKeys returns a string-keyed map's keys, ascending. Every key
// that reaches it is ASCII by registration rule, so byte order, code-point
// order and UTF-16 order coincide (§25.9).
func sortedStringMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// buildSchemaDefaults is the machine-readable map of what an OMITTED key means
// (contract §25.10).
//
// Keys with no baseline are absent from this block on purpose, and that list is
// exactly the set of always-emitted facts: `name`, `help`, `version`,
// `schema_version`, `project_id`, `effect`, `presence`, `value_schema` on every
// entry that has one, a choice object's `name` and `help`, a choice record's
// `value`, a config field's `help` and `required`, and a check's six mandatory
// fields.
//
// `default` on a flag or arg has no baseline either: since presence became the
// authority it is emitted exactly when `presence` is `"default"`, and a `null`
// baseline for it would state something false. `value_schema`'s one exception
// is not an omission at a baseline: a SELECTOR carries no fragment at all and
// its absence IS the declaration, so a baseline would have to say what an
// absent fragment means and every answer it could give is false for the one
// entry that omits the key.
func buildSchemaDefaults() *schemaObject {
	return newSchemaObject().
		set("schema_version", 2).
		set("app", newSchemaObject().
			set("env_prefix", nil).
			set("config", false).
			set("config_format", "json").
			set("config_path", nil).
			set("config_conflict_mode", "cli-wins").
			set("proc_observe_allowlist", []interface{}{}).
			set("global_flags", []interface{}{}).
			set("commands", newSchemaObject()).
			set("groups", newSchemaObject()).
			set("deprecated", newSchemaObject()).
			set("tag_contracts", newSchemaObject()).
			set("checks", newSchemaObject()).
			set("config_fields", newSchemaObject()).
			set("infra", newSchemaObject())).
		set("flag", newSchemaObject().
			set("short", nil).
			set("env", nil).
			set("env_separator", nil).
			set("prefixed", true).
			set("choices", nil).
			set("elect_by", nil).
			set("unique", false).
			set("conflict_mode", nil).
			set("negatable", nil).
			set("nullable", false)).
		set("arg", newSchemaObject().
			set("variadic", false).
			set("choices", nil)).
		// The two choice entities, which is what makes this block the complete
		// omission map it is defined to be: a selector choice object's `flags`
		// is omitted when the scope is empty, and a value-flag choice record's
		// `help` is omitted when the entry declares none.
		set("choice", newSchemaObject().set("flags", []interface{}{})).
		set("choice_record", newSchemaObject().set("help", nil)).
		set("command", newSchemaObject().
			set("consequential", false).
			set("dry_run_supported", true).
			set("dry_run_unsupported_reason", nil).
			set("update_of", nil).
			set("write_mode", nil).
			set("payload_schema", nil).
			set("owns_stdout", false).
			set("passthrough", false).
			set("flags", []interface{}{}).
			set("flag_sets", []interface{}{}).
			set("args", []interface{}{}).
			set("tags", []interface{}{}).
			set("constraints", []interface{}{}).
			set("hidden", false).
			set("interactive", false).
			set("config_fields", []interface{}{}).
			set("grants", []interface{}{}).
			set("forwarding", nil)).
		set("group", newSchemaObject().
			set("commands", newSchemaObject()).
			set("groups", newSchemaObject()).
			set("deprecated", newSchemaObject()).
			set("tags", []interface{}{}).
			set("hidden", false)).
		set("config_field", newSchemaObject().
			set("default", nil).
			set("bound_commands", []interface{}{})).
		set("check", newSchemaObject().set("scope", nil)).
		set("infra", newSchemaObject().
			set("roots", []interface{}{}).
			set("handshakes", []interface{}{}).
			set("connections", []interface{}{}))
}

// readProjectID reads the module path from go.mod in the current working directory.
func readProjectID() (string, error) {
	f, err := os.Open("go.mod")
	if err != nil {
		return "", errCannotDetermineProjectIDNoGoMod()
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", errCannotDetermineProjectIDReadError(err)
	}
	return "", errCannotDetermineProjectIDNoModule()
}

// dumpSchemaObject builds the full ordered schema document, excluding
// project_id.
//
// This is the CWD-free, filesystem-free core of schema production. It reads
// only the in-memory App (name, version, help, flags, commands, groups, etc.).
// project_id is the only field that requires reading go.mod from the CWD, so it
// is added later by the file-writer path (dumpSchemaOrdered). This function
// cannot fail. Fields matching their defaults are omitted; see
// buildSchemaDefaults().
func dumpSchemaObject(app *App) *schemaObject {
	schema := newSchemaObject().
		set("schema_version", 2).
		set("defaults", buildSchemaDefaults()).
		set("name", app.Name).
		set("version", app.Version).
		set("help", app.Help)

	if app.EnvPrefix != "" {
		schema.set("env_prefix", app.EnvPrefix)
	}
	if app.configEnabled {
		schema.set("config", true)
	}
	// The three app-level config keys v1 was blind to. Until v2 an app could
	// relocate every user's config file, or switch it from JSON to TOML, while
	// its dumped schema stayed byte-identical (§25.11).
	if app.configFormat != "" && app.configFormat != "json" {
		schema.set("config_format", app.configFormat)
	}
	// The DECLARATION, never the resolution: a declared literal path as
	// declared, and a RelativeToRoot in its machine-stable marker shape. The
	// resolved absolute path is a property of the dumping machine, and a
	// RelativeToRoot declaration is resolved eagerly INTO configPathOverride at
	// construction, which is why the reference is consulted first.
	if app.configPathRef != nil {
		schema.set("config_path", serializeDefault(*app.configPathRef))
	} else if app.configPathOverride != "" {
		schema.set("config_path", app.configPathOverride)
	}
	if app.configConflictMode != "" && app.configConflictMode != "cli-wins" {
		schema.set("config_conflict_mode", app.configConflictMode)
	}

	// proc_observe_allowlist: omitted when empty; prefixes in declaration order.
	if len(app.procObserveAllowlist) > 0 {
		allowlist := make([]interface{}, 0, len(app.procObserveAllowlist))
		for _, prefix := range app.procObserveAllowlist {
			entry := make([]interface{}, len(prefix))
			for i, p := range prefix {
				entry[i] = p
			}
			allowlist = append(allowlist, entry)
		}
		schema.set("proc_observe_allowlist", allowlist)
	}

	if len(app.globalFlags) > 0 {
		globalFlags := make([]interface{}, 0, len(app.globalFlags))
		for i := range app.globalFlags {
			globalFlags = append(globalFlags, serializeFlagMember(&app.globalFlags[i]))
		}
		schema.set("global_flags", globalFlags)
	}

	// commands and groups keep DECLARATION order (§25.9).
	if len(app.commands) > 0 {
		commands := newSchemaObject()
		for _, name := range app.cmdOrder {
			commands.set(name, serializeCommand(app.commands[name]))
		}
		schema.set("commands", commands)
	}
	if len(app.groups) > 0 {
		groups := newSchemaObject()
		for _, name := range app.groupOrder {
			groups.set(name, serializeGroup(app.groups[name]))
		}
		schema.set("groups", groups)
	}

	// `deprecated`, `tag_contracts` and `checks` are emitted SORTED ascending by
	// key: no implementation retains a declaration order for all three, and a
	// canon that cannot be produced from what an implementation holds is not a
	// canon (§25.9).
	if len(app.deprecatedMap) > 0 {
		deprecated := newSchemaObject()
		for _, name := range sortedStringMapKeys(app.deprecatedMap) {
			deprecated.set(name, app.deprecatedMap[name])
		}
		schema.set("deprecated", deprecated)
	}
	if len(app.tagContracts) > 0 {
		tagContracts := newSchemaObject()
		for _, tag := range sortedStringMapKeys(app.tagContracts) {
			tagContracts.set(tag, app.tagContracts[tag])
		}
		schema.set("tag_contracts", tagContracts)
	}

	if app.checksEnabled {
		checks := newSchemaObject()
		names := make([]string, 0, len(app.checkDefs))
		for name := range app.checkDefs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			// The dumped `checks` block must be a function of the DECLARATION
			// alone. A provider materializes into the same registry lazily and
			// per-cwd, so iterating the whole registry made a dump taken after a
			// check run differ from one taken before it (§25.7). The exclusion
			// is structural here rather than a comment.
			if app.providerSourcedNames[name] {
				continue
			}
			def := app.checkDefs[name]
			entry := newSchemaObject().
				set("tags", def.tags).
				set("severity", def.severity).
				set("fast", def.fast).
				set("pure", def.pure).
				set("needs_network", def.needsNetwork).
				set("depends_on", def.dependsOn)
			if def.scope != "" {
				entry.set("scope", def.scope)
			}
			checks.set(name, entry)
		}
		// Omitted when empty, which is the baseline the `defaults` block states:
		// an app whose only checks are provider-sourced publishes no block at
		// all rather than an empty one.
		if len(checks.keys) > 0 {
			schema.set("checks", checks)
		}
	}

	// config_fields: only present when config fields are declared, in
	// declaration order.
	if len(app.configFields) > 0 {
		cfSchema := newSchemaObject()
		for _, name := range app.configFieldOrder {
			cf := app.configFields[name]
			// Config fields are scalar-only in every implementation, so the
			// fragment is always a scalar row. `required` STAYS beside it: it is
			// not §23's presence declaration under another name -- a config
			// field has no CLI surface and no three-way declaration, and
			// `required` there means "the config file must contain it".
			entry := newSchemaObject().
				set("value_schema", scalarFragment(cf.Type, nil)).
				set("help", cf.Help).
				set("required", cf.Required)
			if cf.HasDefault {
				entry.set("default", cf.Default)
			}
			if bound := boundCommandsFor(app, name); len(bound) > 0 {
				cmds := make([]interface{}, len(bound))
				for i, c := range bound {
					cmds[i] = c
				}
				entry.set("bound_commands", cmds)
			}
			cfSchema.set(name, entry)
		}
		schema.set("config_fields", cfSchema)
	}

	// infra: only present when infrastructure roots or handshake vars are
	// declared. Resolved root values are intentionally EXCLUDED -- the schema
	// must be machine-stable (not machine-specific). Only the declared env var
	// and default path (both stable declarations) are emitted for roots.
	if len(app.infraRootOrder) > 0 || len(app.handshakeOrder) > 0 || len(app.connectionOrder) > 0 {
		infra := newSchemaObject()
		if len(app.infraRootOrder) > 0 {
			roots := make([]interface{}, 0, len(app.infraRootOrder))
			for _, ev := range app.infraRootOrder {
				var def string
				for _, d := range app.infraRootDecls {
					if d.envVar == ev {
						def = d.defaultPath
						break
					}
				}
				roots = append(roots, newSchemaObject().set("env_var", ev).set("default", def))
			}
			infra.set("roots", roots)
		}
		if len(app.handshakeOrder) > 0 {
			handshakes := make([]interface{}, 0, len(app.handshakeOrder))
			for _, ev := range app.handshakeOrder {
				handshakes = append(handshakes, newSchemaObject().
					set("env_var", ev).
					set("help", app.handshakeEnvs[ev]))
			}
			infra.set("handshakes", handshakes)
		}
		if len(app.connectionOrder) > 0 {
			connections := make([]interface{}, 0, len(app.connectionOrder))
			for _, ev := range app.connectionOrder {
				connections = append(connections, newSchemaObject().
					set("env_var", ev).
					set("help", app.connectionEnvs[ev]))
			}
			infra.set("connections", connections)
		}
		schema.set("infra", infra)
	}
	return schema
}

// boundCommandsFor lists the command paths binding one config field, in
// declaration order.
func boundCommandsFor(app *App, name string) []string {
	var bound []string
	for _, cmdName := range app.cmdOrder {
		cmd := app.commands[cmdName]
		for _, f := range cmd.configFields {
			if f == name {
				bound = append(bound, cmdName)
				break
			}
		}
	}
	var searchGroup func(g *Group, prefix string)
	searchGroup = func(g *Group, prefix string) {
		for _, cmdName := range g.order {
			cmd := g.Commands[cmdName]
			for _, f := range cmd.configFields {
				if f == name {
					bound = append(bound, prefix+cmdName)
					break
				}
			}
		}
		for _, grpName := range g.groupOrder {
			searchGroup(g.Groups[grpName], prefix+grpName+" ")
		}
	}
	for _, grpName := range app.groupOrder {
		searchGroup(app.groups[grpName], grpName+" ")
	}
	return bound
}

// dumpSchemaCore returns the schema core as plain Go maps, excluding
// project_id. The written document's key order lives in the ordered form; a
// caller of this reads fields by name.
func dumpSchemaCore(app *App) map[string]interface{} {
	return toPlain(dumpSchemaObject(app)).(map[string]interface{})
}

// dumpSchemaOrdered produces the full ordered document including project_id
// (reads the CWD). project_id sits immediately after `defaults`, so removing it
// leaves the CWD-free core byte-identical.
func dumpSchemaOrdered(app *App) (*schemaObject, error) {
	projectID, err := readProjectID()
	if err != nil {
		return nil, err
	}
	schema := dumpSchemaObject(app)
	schema.insertAfter("defaults", "project_id", projectID)
	return schema, nil
}

// dumpSchema produces the full schema as plain maps, including project_id.
func dumpSchema(app *App) (map[string]interface{}, error) {
	schema, err := dumpSchemaOrdered(app)
	if err != nil {
		return nil, err
	}
	return toPlain(schema).(map[string]interface{}), nil
}

// DumpSchemaDict returns the app's full schema as a map, excluding project_id.
//
// This is the public, CWD-free accessor for the schema. Unlike the
// --dump-schema flag (which writes the app's declared schema location and derives
// project_id from go.mod in the current working directory), this method reads
// only the in-memory App and performs no filesystem or CWD access, and cannot
// fail. The returned map is equivalent to the written schema file with the
// project_id field removed.
func (a *App) DumpSchemaDict() map[string]interface{} {
	return dumpSchemaCore(a)
}

// checkSchemaProjectID verifies that an existing schema file belongs to the
// same project. Returns an error on mismatch. Silently passes on: missing
// file, unreadable file, JSON without project_id field, or matching project_id.
func checkSchemaProjectID(filePath string, newProjectID string) error {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var existing map[string]interface{}
	if err := json.Unmarshal(raw, &existing); err != nil {
		return nil
	}
	existingID, ok := existing["project_id"]
	if !ok {
		return nil
	}
	existingIDStr, ok := existingID.(string)
	if !ok {
		return nil
	}
	if existingIDStr != newProjectID {
		return errSchemaMismatch(existingIDStr, newProjectID)
	}
	return nil
}

// writeSchema writes the schema to the app's declared location and returns the
// path. The location is decided once, at construction (WithSchemaPath /
// WithSchemaPathRelativeToRoot, or the framework's ".strictcli/schema.json"
// anchored at the construction-time cwd) -- never at the caller's working
// directory at dump time.
//
// The bytes are the canon's (§25.8), not encoding/json's: a repository whose
// schema file is written sometimes by this implementation and sometimes by
// another must see a diff exactly when something changed.
func writeSchema(app *App) (string, error) {
	schema, err := dumpSchemaOrdered(app)
	if err != nil {
		return "", err
	}
	text, err := canonicalJSON(schema)
	if err != nil {
		return "", err
	}
	filePath := app.schemaOutPath
	if dirPath := filepath.Dir(filePath); dirPath != "" {
		if err := os.MkdirAll(dirPath, 0o755); err != nil {
			return "", err
		}
	}
	newProjectID, _ := schema.get("project_id").(string)
	if err := checkSchemaProjectID(filePath, newProjectID); err != nil {
		return "", err
	}
	// Exactly one trailing newline at end of file.
	if err := os.WriteFile(filePath, []byte(text+"\n"), 0o644); err != nil {
		return "", err
	}
	// Framework-blessed CACHE_WRITE: recorded in the structured effect log,
	// never in the would-do log, and performed even in dry mode.
	app.recordCacheWrite(filePath)
	// Return absolute path for output
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return filePath, nil
	}
	return absPath, nil
}
