package strictcli

import (
	"fmt"
	"strings"
)

func formatVersion(app *App) string {
	return fmt.Sprintf("%s %s", app.Name, app.Version)
}

func formatAppHelp(app *App) string {
	return formatAppHelpDepth(app, 1)
}

// helpTreeRow is one line of a Commands or Groups section: a path relative to
// the page's own node, and its help.
type helpTreeRow struct {
	path string
	help string
}

// collectHelpTree lists the visible commands and groups within depth levels
// below one node, depth-first in declaration order: the node's own commands,
// then each group followed by what it holds. Level 1 is the node's own
// children, which is all --help lists.
func collectHelpTree(cmdOrder []string, cmds map[string]*Command, groupOrder []string, groups map[string]*Group,
	prefix string, level, depth int, commandsOut, groupsOut *[]helpTreeRow) {
	for _, name := range cmdOrder {
		if cmd := cmds[name]; !cmd.Hidden {
			*commandsOut = append(*commandsOut, helpTreeRow{prefix + name, cmd.Help})
		}
	}
	for _, name := range groupOrder {
		grp := groups[name]
		if grp.Hidden {
			continue
		}
		*groupsOut = append(*groupsOut, helpTreeRow{prefix + name, grp.Help})
		if level < depth {
			collectHelpTree(grp.order, grp.Commands, grp.groupOrder, grp.Groups, prefix+name+" ", level+1, depth, commandsOut, groupsOut)
		}
	}
}

// helpTreeSection renders one titled section of rows, aligned on its own
// column, or nothing when there are no rows.
func helpTreeSection(title string, rows []helpTreeRow) []string {
	if len(rows) == 0 {
		return nil
	}
	lines := []string{"", title}
	maxLen := 0
	for _, r := range rows {
		if len(r.path) > maxLen {
			maxLen = len(r.path)
		}
	}
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("  %s%s%s", r.path, strings.Repeat(" ", maxLen-len(r.path)+4), r.help))
	}
	return lines
}

// formatAppHelpDepth is the app's help page listing depth levels of its
// command tree; depth 1 is the page --help shows.
func formatAppHelpDepth(app *App, depth int) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("%s v%s -- %s", app.Name, app.Version, app.Help))

	var cmdRows, groupRows []helpTreeRow
	collectHelpTree(app.cmdOrder, app.commands, app.groupOrder, app.groups, "", 1, depth, &cmdRows, &groupRows)
	lines = append(lines, helpTreeSection("Commands:", cmdRows)...)
	lines = append(lines, helpTreeSection("Groups:", groupRows)...)

	if len(app.deprecated) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Deprecated:")
		maxLen := 0
		for _, d := range app.deprecated {
			if len(d.Name) > maxLen {
				maxLen = len(d.Name)
			}
		}
		for _, d := range app.deprecated {
			padding := maxLen - len(d.Name) + 4
			lines = append(lines, fmt.Sprintf("  %s%s%s", d.Name, strings.Repeat(" ", padding), d.Message))
		}
	}

	// Global flags section. App-level help renders name + short + help only
	// (no type spec, no metadata), mirroring Python's _format_app_help and the
	// TypeScript formatAppHelp. This is intentionally simpler than the
	// command-level Global flags section, which uses buildFlagSpec/buildFlagMeta.
	if len(app.globalFlags) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Global flags:")
		specs := make([]string, len(app.globalFlags))
		maxSpec := 0
		for i, f := range app.globalFlags {
			spec := "--" + f.Name
			if f.Short != "" {
				spec += ", -" + f.Short
			}
			specs[i] = spec
			if len(spec) > maxSpec {
				maxSpec = len(spec)
			}
		}
		for i, f := range app.globalFlags {
			padding := maxSpec - len(specs[i]) + 4
			lines = append(lines, fmt.Sprintf("  %s%s%s", specs[i], strings.Repeat(" ", padding), f.Help))
		}
	}

	if len(app.infraRootOrder) > 0 || len(app.handshakeOrder) > 0 || len(app.connectionOrder) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Infrastructure:")
		lines = append(lines, "  (location/handshake env vars; not suppressed by --hermetic)")
		maxLen := 0
		for _, ev := range app.infraRootOrder {
			if len(ev) > maxLen {
				maxLen = len(ev)
			}
		}
		for _, ev := range app.handshakeOrder {
			if len(ev) > maxLen {
				maxLen = len(ev)
			}
		}
		for _, ev := range app.connectionOrder {
			if len(ev) > maxLen {
				maxLen = len(ev)
			}
		}
		for _, ev := range app.infraRootOrder {
			padding := maxLen - len(ev) + 4
			var def string
			for _, d := range app.infraRootDecls {
				if d.envVar == ev {
					def = d.defaultPath
					break
				}
			}
			lines = append(lines, fmt.Sprintf("  %s%sroot (default: %s)", ev, strings.Repeat(" ", padding), def))
		}
		for _, ev := range app.handshakeOrder {
			padding := maxLen - len(ev) + 4
			lines = append(lines, fmt.Sprintf("  %s%s%s", ev, strings.Repeat(" ", padding), app.handshakeEnvs[ev]))
		}
		for _, ev := range app.connectionOrder {
			padding := maxLen - len(ev) + 4
			lines = append(lines, fmt.Sprintf("  %s%sconnection URL, suppressed by --hermetic (%s)", ev, strings.Repeat(" ", padding), app.connectionEnvs[ev]))
		}
	}

	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("Use '%s help <command>' for more information.", app.Name))

	return strings.Join(lines, "\n")
}

func formatGroupHelp(app *App, group *Group, path []string) string {
	return formatGroupHelpDepth(app, group, path, 1)
}

// formatGroupHelpDepth is a group's help page listing depth levels below it.
func formatGroupHelpDepth(app *App, group *Group, path []string, depth int) string {
	var lines []string
	fullPath := strings.Join(path, " ")
	lines = append(lines, fmt.Sprintf("%s %s -- %s", app.Name, fullPath, group.Help))

	var cmdRows, groupRows []helpTreeRow
	collectHelpTree(group.order, group.Commands, group.groupOrder, group.Groups, "", 1, depth, &cmdRows, &groupRows)
	lines = append(lines, helpTreeSection("Commands:", cmdRows)...)
	lines = append(lines, helpTreeSection("Groups:", groupRows)...)

	if len(group.deprecated) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Deprecated:")
		maxLen := 0
		for _, d := range group.deprecated {
			if len(d.Name) > maxLen {
				maxLen = len(d.Name)
			}
		}
		for _, d := range group.deprecated {
			padding := maxLen - len(d.Name) + 4
			lines = append(lines, fmt.Sprintf("  %s%s%s", d.Name, strings.Repeat(" ", padding), d.Message))
		}
	}

	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("Use '%s help %s <command>' for more information.", app.Name, fullPath))

	return strings.Join(lines, "\n")
}

// formatDryRunSection renders the `Dry run:` section of command help, or
// nothing. It appears only for a command that declares
// dry_run_supported=false: the baseline (dry run works) needs no announcement,
// and a section on every command would be noise. Byte-identical across
// implementations.
func formatDryRunSection(cmd *Command) []string {
	if cmd.DryRunSupported {
		return nil
	}
	return []string{
		"",
		"Dry run:",
		fmt.Sprintf("  --dry-run is not supported: %s", cmd.DryRunUnsupportedReason),
	}
}

// fmtCommandHeader is the first line of a command's help page.
func fmtCommandHeader(app *App, cmd *Command, prefix string) string {
	return fmt.Sprintf("%s %s%s -- %s", app.Name, prefix, cmd.Name, cmd.Help)
}

// renderFlagEntries renders a flag block's entries on one alignment column.
func renderFlagEntries(entries []flagHelpEntry) []string {
	var lines []string
	maxSpec := 0
	for _, e := range entries {
		if len(e.spec) > maxSpec {
			maxSpec = len(e.spec)
		}
	}
	for _, e := range entries {
		if e.right == "" {
			lines = append(lines, "  "+e.spec)
			continue
		}
		padding := maxSpec - len(e.spec) + 4
		lines = append(lines, fmt.Sprintf("  %s%s%s", e.spec, strings.Repeat(" ", padding), e.right))
	}
	return lines
}

func formatCommandHelp(app *App, cmd *Command, prefix string) string {
	var lines []string
	lines = append(lines, fmtCommandHeader(app, cmd, prefix))

	// Rendered before the passthrough early-return: a passthrough command can
	// declare the refusal too, and its help is the only place the reason would
	// otherwise be visible.
	lines = append(lines, formatDryRunSection(cmd)...)

	// Passthrough commands: minimal help (no flags/args sections)
	if cmd.Passthrough {
		lines = append(lines, formatRequirementsSection(cmd.requires)...)
		return strings.Join(lines, "\n")
	}

	if len(cmd.args) > 0 || cmd.argsAfterSeparator != nil {
		lines = append(lines, "")
		lines = append(lines, "Arguments:")
		// The content-keyed block rule reaches positional args too: an arg whose
		// choices entries carry help drops the one-line `[choices: ...]` meta
		// and renders its entries two columns beneath its own line (contract
		// §24.10, §18.19 item 218). ONE alignment column across the whole
		// section, deepest entry included -- the `Arguments:` section has its
		// own column and never shares the flag block's.
		var rows []flagHelpEntry
		for _, a := range cmd.args {
			displayName := a.Name
			if a.IsVariadic {
				displayName = a.Name + "..."
			}
			block := choiceRecordsCarryHelp(a.choiceRecords)
			var metaParts []string
			if a.Type != TypeStr {
				metaParts = append(metaParts, fmt.Sprintf("type: %s", flagTypeName[a.Type]))
			}
			if a.Choices != nil && !block {
				metaParts = append(metaParts, fmt.Sprintf("choices: %s", formatChoices(a.Choices)))
			}
			// Exactly one presence part, last on the line (contract §23.8).
			// A required positional renders [required] like a required flag:
			// there is no usage line, so it was the one declaration whose
			// presence a reader could not see.
			switch a.presence {
			case presenceRequired:
				metaParts = append(metaParts, "required")
			case presenceOptional:
				metaParts = append(metaParts, "optional")
			default:
				metaParts = append(metaParts, fmt.Sprintf("default: %s", formatDefaultValue(a.Default)))
			}
			var sb strings.Builder
			for i, part := range metaParts {
				if i > 0 {
					sb.WriteString(" ")
				}
				sb.WriteString("[")
				sb.WriteString(part)
				sb.WriteString("]")
			}
			rows = append(rows, flagHelpEntry{spec: "  " + displayName, right: a.Help + " " + sb.String()})
			if !block {
				continue
			}
			for _, cv := range a.choiceRecords {
				rows = append(rows, flagHelpEntry{
					spec:  "    " + formatValueForError(cv.Value),
					right: cv.Help,
				})
			}
		}
		// The receiver of the arguments after "--" (WithArgsAfterSeparator)
		// renders last, spelled as it is typed. It always holds a list, empty
		// without a "--", so its one presence part is [optional].
		if r := cmd.argsAfterSeparator; r != nil {
			rows = append(rows, flagHelpEntry{spec: "  -- " + r.name + "...", right: r.help + " [optional]"})
		}
		maxSpec := 0
		for _, row := range rows {
			if len(row.spec) > maxSpec {
				maxSpec = len(row.spec)
			}
		}
		for _, row := range rows {
			padding := maxSpec - len(row.spec) + 4
			line := row.spec + strings.Repeat(" ", padding) + row.right
			lines = append(lines, strings.TrimRight(line, " "))
		}
	}

	// Command flags, including every choice block at every depth. ONE alignment
	// column is computed across the whole command's flag block, deepest entry
	// included, so help text starts in the same column everywhere on the page
	// (contract §24.10). The mutex section is gone with MutexGroup: a
	// member-spelled selector renders inline, in declaration order.
	if len(cmd.flags) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Flags:")
		lines = append(lines, renderFlagEntries(collectFlagHelpEntries(cmd.flags, 0))...)
	}

	// The `Constraints:` section (contract §26.10), after the last of the
	// Arguments/Flags blocks. A declared rule that decides whether an
	// invocation is accepted must be visible in the help the operator already
	// read -- and the declared NAME renders, in the position a flag name
	// occupies, because that name is the identifier a violation prints.
	//
	// ONE alignment column computed across the constraint block alone: it never
	// shares the flag block's column, which is §24.10's rule for `Arguments:`
	// applied to a third section. Declaration order, one line per constraint
	// INCLUDING nested ones -- a nested constraint is both a rule of its own and
	// an operand, so it has its own line and appears inside its parent's.
	if len(cmd.constraints) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Constraints:")
		maxSpec := 0
		specs := make([]string, len(cmd.constraints))
		for i := range cmd.constraints {
			specs[i] = "  " + cmd.constraints[i].name
			if len(specs[i]) > maxSpec {
				maxSpec = len(specs[i])
			}
		}
		for i := range cmd.constraints {
			padding := maxSpec - len(specs[i]) + 4
			lines = append(lines, specs[i]+strings.Repeat(" ", padding)+cmd.constraintHelpSentence(&cmd.constraints[i]))
		}
	}

	// What the command needs at run time, before the global flags.
	lines = append(lines, formatRequirementsSection(cmd.requires)...)

	// Global flags section
	if len(app.globalFlags) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Global flags:")
		specs := make([]string, len(app.globalFlags))
		maxSpec := 0
		for i, f := range app.globalFlags {
			specs[i] = buildFlagSpec(f)
			if len(specs[i]) > maxSpec {
				maxSpec = len(specs[i])
			}
		}
		for i, f := range app.globalFlags {
			padding := maxSpec - len(specs[i]) + 4
			meta := buildFlagMeta(f)
			lines = append(lines, fmt.Sprintf("  %s%s%s%s", specs[i], strings.Repeat(" ", padding), f.Help, meta))
		}
	}

	return strings.Join(lines, "\n")
}

// flagHelpEntry is one rendered line of the command's flag block: the left
// column (indent included) and the right column (help plus metadata).
type flagHelpEntry struct {
	spec  string
	right string
}

// collectFlagHelpEntries renders a scope's flags, recursing into every choice.
//
// The layout (contract §24.10): a choice line indents TWO columns past its
// selector's line, and a choice's scoped flags indent TWO columns past their
// choice, so recursion adds four columns per level. indent is that column
// count, counted from the flag block's own left edge.
//
// A choice-carrying flag renders as an indented block IFF any of its choices
// carries help or a scope; otherwise it keeps the one-line `[choices: a, b, c]`
// form. A selector is therefore always a block (its choices carry mandatory
// help) and a value flag is a block exactly when its entries were given help.
func collectFlagHelpEntries(flags []Flag, indent int) []flagHelpEntry {
	var out []flagHelpEntry
	pad := strings.Repeat(" ", indent)
	choicePad := strings.Repeat(" ", indent+2)
	for i := range flags {
		f := &flags[i]
		if f.Type == TypeChoice && f.memberSpelled {
			// A member-spelled selector has no token to render, so its own line
			// carries its NAME in the left column -- the handler's key and the
			// noun help and errors use, never something a user types -- and in
			// the right column its help, the clause and its presence part, in
			// that order (contract §24.10). The member flags render as ordinary
			// flag lines two columns beneath it, exactly where a choice line
			// renders under a token-spelled selector.
			out = append(out, flagHelpEntry{
				spec:  pad + f.Name,
				right: f.Help + " " + memberSelectorHeading + buildFlagMeta(*f),
			})
			for _, ch := range f.choiceDecls {
				member := collectFlagHelpEntries(ch.Flags[:1], indent+2)
				member[0].spec = choicePad + buildMemberSpec(ch)
				// The CHOICE's help is what the member's line carries: the
				// member flag is the token, the choice is what electing it says.
				member[0].right = ch.Help + buildFlagMeta(ch.Flags[0])
				out = append(out, member...)
				out = append(out, collectFlagHelpEntries(ch.Flags[1:], indent+4)...)
			}
			continue
		}

		out = append(out, flagHelpEntry{
			spec:  pad + buildFlagSpec(*f),
			right: f.Help + buildFlagMeta(*f),
		})

		if f.Type == TypeChoice {
			for _, ch := range f.choiceDecls {
				out = append(out, flagHelpEntry{spec: choicePad + ch.Name, right: ch.Help})
				out = append(out, collectFlagHelpEntries(ch.Flags, indent+4)...)
			}
			continue
		}
		// A value flag's entries render in block form exactly when at least one
		// of them carries help; an entry with no help renders the value alone.
		if choiceRecordsCarryHelp(f.choiceRecords) {
			for _, cv := range f.choiceRecords {
				out = append(out, flagHelpEntry{
					spec:  choicePad + formatValueForError(cv.Value),
					right: cv.Help,
				})
			}
		}
	}
	return out
}

// memberSelectorHeading is the clause a member-spelled selector's own line
// carries in place of a token it never has (contract §24.10).
const memberSelectorHeading = "(exactly one of the following)"

// buildMemberSpec is the left-column spec for one member flag under member
// spelling: the token that elects the choice, its short, and its payload's type
// when it carries one.
//
// It is NOT buildFlagSpec: a payload-less member is a bool flag whose negation
// DECLINES rather than naming a choice (§21.2), so rendering `--no-<member>`
// beside it would offer the decline as if it were a way of electing. The line
// names the one token that elects.
func buildMemberSpec(ch *ChoiceDecl) string {
	f := memberFlag(ch)
	spec := "--" + f.Name
	if f.Short != "" {
		spec += ", -" + f.Short
	}
	if choiceCarriesPayload(ch) {
		spec += " <" + flagTypeName[f.Type] + ">"
	}
	return spec
}

// formatSelectorDefaultForHelp renders a defaulted selector's presence part as
// the COMPLETE elected value, because that is what a default is (§24.5, §24.10):
//
//	[default: <choice> (<field>=<value>, ...)]
//
// The elected choice's own scalar fields in declaration order, each rendered by
// the value formatter every other presence part uses, joined by ", ". A choice
// whose scope is empty -- or whose only fields are nested selectors -- renders
// `[default: <choice>]` with no parenthesized part at all, never an empty `()`.
//
// A NESTED selector inside the defaulted scope is not expanded inline: it opens
// its own line in the block, where its own presence part states its own default
// by this same rule. A field that carries no value (an optional one) is omitted
// for the same reason the dumped flat map omits it -- there is no value to
// render, and completeness is what a required one is guaranteed by.
func formatSelectorDefaultForHelp(sel *Flag) string {
	if sel.helpFull != nil {
		sel = sel.helpFull
	}
	name, ok := sel.Default.(string)
	if !ok {
		return formatDefaultValue(sel.Default)
	}
	ch := findChoice(sel, name)
	if ch == nil {
		return formatDefaultValue(sel.Default)
	}
	start := 0
	if ch.member {
		// The member flag itself elects; a defaulted member is payload-less, so
		// it contributes no field.
		start = 1
	}
	var parts []string
	for j := start; j < len(ch.Flags); j++ {
		f := ch.Flags[j]
		if f.Type == TypeChoice || f.presence != presenceDefault {
			continue
		}
		parts = append(parts, f.Name+"="+formatFlagDefaultForHelp(f))
	}
	if len(parts) == 0 {
		return ch.Name
	}
	return ch.Name + " (" + strings.Join(parts, ", ") + ")"
}

// choiceRecordsCarryHelp reports whether any entry of a value flag's choices
// list was given help, which is what promotes the flag to block rendering.
func choiceRecordsCarryHelp(records []ChoiceValue) bool {
	for _, cv := range records {
		if cv.Help != "" {
			return true
		}
	}
	return false
}

func buildFlagSpec(f Flag) string {
	var parts []string
	if f.Type == TypeBool && f.Negatable {
		parts = append(parts, fmt.Sprintf("--%s, --no-%s", f.Name, f.Name))
		if f.Short != "" {
			parts = append(parts, "-"+f.Short)
		}
	} else {
		parts = append(parts, "--"+f.Name)
		if f.Short != "" {
			parts = append(parts, "-"+f.Short)
		}
	}
	spec := strings.Join(parts, ", ")
	if IsDictType(f.Type) {
		valTypeName := flagTypeName[ItemType(f.Type)]
		spec += fmt.Sprintf(" <key=%s>", valTypeName)
	} else if IsListType(f.Type) {
		itemTypeName := flagTypeName[ItemType(f.Type)]
		spec += fmt.Sprintf(" <%s>", itemTypeName)
	} else {
		switch f.Type {
		case TypeStr:
			spec += " <str>"
		case TypeInt:
			spec += " <int>"
		case TypeFloat:
			spec += " <float>"
		case TypeChoice:
			spec += " <choice>"
		}
	}
	// A nullable property renders its minted clear spelling on the SAME line,
	// exactly as a negatable bool renders `--x, --no-x` (contract §27.6): one
	// line, one help text, and one presence part -- §23.8's invariant is
	// untouched, because the minted spelling is a second way to write to one
	// declaration rather than a second declaration.
	if f.Nullable {
		spec += ", --" + unsetFlagName(f.Name)
	}
	return spec
}

func buildFlagMeta(f Flag) string {
	var metaParts []string
	if IsDictType(f.Type) {
		metaParts = append(metaParts, "dict")
	} else if IsListType(f.Type) {
		metaParts = append(metaParts, "list")
	} else if f.Repeatable {
		metaParts = append(metaParts, "repeatable")
	}
	if f.Unique {
		metaParts = append(metaParts, "unique")
	}
	// The one-line choices form is kept only while the flag renders as one
	// line: once any entry carries help, the entries render as a block instead
	// (contract §24.10).
	if f.Choices != nil && !choiceRecordsCarryHelp(f.choiceRecords) {
		metaParts = append(metaParts, "choices: "+formatChoices(f.Choices))
	}
	if f.Env != "" {
		if f.EnvSeparator != "" {
			metaParts = append(metaParts, fmt.Sprintf("env: %s (sep: %s)", f.Env, f.EnvSeparator))
		} else {
			metaParts = append(metaParts, "env: "+f.Env)
		}
	}
	// Exactly one presence part, last on the line (contract §23.8).
	switch f.presence {
	case presenceRequired:
		metaParts = append(metaParts, "required")
	case presenceOptional:
		metaParts = append(metaParts, "optional")
	default:
		metaParts = append(metaParts, "default: "+formatFlagDefaultForHelp(f))
	}

	var sb strings.Builder
	for i, part := range metaParts {
		if i > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString("[")
		sb.WriteString(part)
		sb.WriteString("]")
	}
	return " " + sb.String()
}

// formatFlagDefaultForHelp renders a DECLARED default value for the help line's
// presence part. A declared empty collection renders as the literal `[]` / `{}`
// rather than as nothing: under the presence declaration it is a declaration,
// and a declaration that rendered blank would leave the flag as the one line
// with no presence part (contract §23.8).
func formatFlagDefaultForHelp(f Flag) string {
	if f.Type == TypeChoice {
		return formatSelectorDefaultForHelp(&f)
	}
	if f.Type == TypeBool && !f.Repeatable {
		if def, ok := f.Default.(bool); ok && def {
			return "true"
		}
		return "false"
	}
	if IsDictType(f.Type) {
		m, ok := f.Default.(map[string]interface{})
		if !ok {
			return formatDefaultValue(f.Default)
		}
		if len(m) == 0 {
			return "{}"
		}
		return formatDictForDisplay(m)
	}
	if f.Repeatable {
		items, ok := f.Default.([]interface{})
		if !ok {
			return formatDefaultValue(f.Default)
		}
		if len(items) == 0 {
			return "[]"
		}
		parts := make([]string, len(items))
		for i, elem := range items {
			parts[i] = formatValueForError(elem)
		}
		return strings.Join(parts, ", ")
	}
	return formatDefaultValue(f.Default)
}
