package strictcli

import (
	"strconv"
	"strings"
)

// The framework's own commands. `help` shows the help of the app, a group, a
// command, or one flag, as text or, under --json, as the help document (the
// app's schema, version 2). `version` shows the app's name and
// version. Both are recognized as the first command word; their names are
// reserved at every level of the command tree (frameworkCommandNames).

const (
	helpCommandName    = "help"
	versionCommandName = "version"
)

// helpRequest is a parsed `help` invocation: help's own options, then the
// address -- zero or more groups, optionally a command, optionally one of its
// flags spelled as typed.
type helpRequest struct {
	own       bool // --help / -h after `help`: the help command's own page
	depth     int  // 0 when --depth was not given
	groupPath []string
	group     *Group // the addressed group, nil at the root or on a command
	cmd       *Command
	flag      string // the addressed flag's long name, without the dashes
	global    bool   // the addressed flag is an app global
	address   []string
}

// helpAddressPrefix is "<app> help" followed by the address words so far.
func (a *App) helpLine(words ...string) string {
	return strings.Join(append([]string{a.Name, helpCommandName}, words...), " ")
}

// parseHelpCommand parses the words after `help`. It returns the request or a
// parse error.
func (a *App) parseHelpCommand(args []string) (helpRequest, string) {
	var req helpRequest
	for _, tok := range args {
		if tok == "--" {
			break
		}
		if tok == "--help" || tok == "-h" {
			return helpRequest{own: true}, ""
		}
	}
	i := 0
	// Help's own options come first, before the address.
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		tok := args[i]
		switch {
		case tok == "--depth":
			if i+1 >= len(args) {
				return req, errHelpDepthMissing
			}
			n, ok := parseHelpDepth(args[i+1])
			if !ok {
				return req, errHelpDepthValue(args[i+1])
			}
			req.depth = n
			i += 2
		case strings.HasPrefix(tok, "--depth="):
			v := strings.TrimPrefix(tok, "--depth=")
			n, ok := parseHelpDepth(v)
			if !ok {
				return req, errHelpDepthValue(v)
			}
			req.depth = n
			i++
		case a.isGlobalFlagToken(tok):
			return req, errHelpFlagOnGroup(tok, a.helpLine("<command>", tok))
		default:
			return req, errHelpUnknownOption(tok, a.Name)
		}
	}
	groups, commands, deprecated := a.groups, a.commands, a.deprecatedMap
	for ; i < len(args); i++ {
		tok := args[i]
		if req.cmd != nil {
			if req.flag != "" {
				return req, errHelpAfterFlag(tok)
			}
			if !strings.HasPrefix(tok, "-") {
				return req, errHelpWordAfterCommand(tok, strings.Join(req.address, " "))
			}
			if err := a.resolveHelpFlag(&req, tok); err != "" {
				return req, err
			}
			continue
		}
		if strings.HasPrefix(tok, "-") {
			if tok == "--depth" || strings.HasPrefix(tok, "--depth=") {
				return req, errHelpOptionAfterAddress("--depth",
					a.helpLine(append([]string{"--depth", "<int>"}, req.address...)...))
			}
			return req, errHelpFlagOnGroup(tok, a.helpLine(append(append([]string{}, req.address...), "<command>", tok)...))
		}
		if grp, ok := groups[tok]; ok {
			req.groupPath = append(req.groupPath, tok)
			req.address = append(req.address, tok)
			req.group = grp
			groups, commands, deprecated = grp.Groups, grp.Commands, grp.deprecatedMap
			continue
		}
		if cmd, ok := commands[tok]; ok {
			req.cmd = cmd
			req.group = nil
			req.address = append(req.address, tok)
			continue
		}
		if msg, ok := deprecated[tok]; ok {
			return req, errCommandDeprecated(tok, msg)
		}
		if len(req.groupPath) > 0 {
			return req, errUnknownCommandInGroup(tok, strings.Join(req.groupPath, " "))
		}
		return req, errUnknownCommand(tok)
	}
	if req.cmd != nil && req.depth != 0 {
		return req, errHelpDepthOnCommand(strings.Join(req.address, " "))
	}
	return req, ""
}

// parseHelpDepth accepts an integer of at least 1 in plain decimal form.
func parseHelpDepth(v string) (int, bool) {
	if v == "" || v[0] == '+' || (len(v) > 1 && v[0] == '0') {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// resolveHelpFlag resolves one flag address on the addressed command: its own
// flags at every scope, then the app's globals. Other spellings of a declared
// flag (a short form, a value, a negation, a minted clear) are refused naming
// the long form.
func (a *App) resolveHelpFlag(req *helpRequest, tok string) string {
	path := strings.Join(req.address, " ")
	fix := func(name string) string { return a.helpLine(append(append([]string{}, req.address...), "--"+name)...) }
	typed := typedFlagNames(req.cmd.flags)
	for _, g := range a.globalFlags {
		typed = append(typed, g.Name)
	}
	has := func(name string) bool {
		for _, n := range typed {
			if n == name {
				return true
			}
		}
		return false
	}
	if !strings.HasPrefix(tok, "--") {
		short := strings.TrimPrefix(tok, "-")
		if long := shortOwner(req.cmd.flags, a.globalFlags, short); long != "" {
			return errHelpShortAddress(tok, fix(long))
		}
		return unknownHelpFlag(path, tok, typed)
	}
	name := strings.TrimPrefix(tok, "--")
	if eq := strings.Index(name, "="); eq >= 0 {
		if has(name[:eq]) {
			return errHelpFlagWithValue(tok, fix(name[:eq]))
		}
		return unknownHelpFlag(path, tok, typed)
	}
	if has(name) {
		req.flag = name
		req.address = append(req.address, tok)
		req.global = !declaresFlag(req.cmd.flags, name)
		return ""
	}
	for _, prefix := range []string{"no-", unsetFlagName("")} {
		if base := strings.TrimPrefix(name, prefix); base != name && has(base) {
			return errHelpNegatedFlag(tok, fix(base))
		}
	}
	return unknownHelpFlag(path, tok, typed)
}

func unknownHelpFlag(path, tok string, typed []string) string {
	if len(typed) == 0 {
		return errHelpUnknownFlagNoFlags(path, tok)
	}
	parts := make([]string, len(typed))
	for i, n := range typed {
		parts[i] = "--" + n
	}
	return errHelpUnknownFlag(path, tok, strings.Join(parts, ", "))
}

// typedFlagNames lists the long names a command's flags put on the command
// line, depth-first in declaration order: ordinary flags, token-spelled
// selectors, member flags, and every choice's scoped flags.
func typedFlagNames(flags []Flag) []string {
	var out []string
	for i := range flags {
		f := &flags[i]
		if f.Type != TypeChoice {
			out = append(out, f.Name)
			continue
		}
		if !f.memberSpelled {
			out = append(out, f.Name)
		}
		for _, ch := range f.choiceDecls {
			scope := ch.Flags
			if ch.member {
				out = append(out, ch.Flags[0].Name)
				scope = ch.Flags[1:]
			}
			out = append(out, typedFlagNames(scope)...)
		}
	}
	return out
}

func declaresFlag(flags []Flag, name string) bool {
	for _, n := range typedFlagNames(flags) {
		if n == name {
			return true
		}
	}
	return false
}

// shortOwner returns the long name of the flag declaring a short form.
func shortOwner(flags []Flag, globals []Flag, short string) string {
	var walk func(fs []Flag) string
	walk = func(fs []Flag) string {
		for i := range fs {
			f := &fs[i]
			if f.Short == short && f.Short != "" && !(f.Type == TypeChoice && f.memberSpelled) {
				return f.Name
			}
			if f.Type == TypeChoice {
				for _, ch := range f.choiceDecls {
					if found := walk(ch.Flags); found != "" {
						return found
					}
				}
			}
		}
		return ""
	}
	if found := walk(flags); found != "" {
		return found
	}
	return walk(globals)
}

// pruneFlagsForHelp returns the flag declarations a one-flag help page shows:
// the declarations of that name, with the selector and choice lines above a
// scoped one. A pruned selector keeps a pointer to its full declaration so its
// presence part renders as it does on the whole page.
func pruneFlagsForHelp(flags []Flag, name string) []Flag {
	var out []Flag
	for i := range flags {
		f := flags[i]
		if f.Type != TypeChoice {
			if f.Name == name {
				out = append(out, f)
			}
			continue
		}
		if !f.memberSpelled && f.Name == name {
			out = append(out, f)
			continue
		}
		var kept []*ChoiceDecl
		for _, ch := range f.choiceDecls {
			if ch.member && ch.Flags[0].Name == name {
				kept = append(kept, ch)
				continue
			}
			scope := ch.Flags
			var head []Flag
			if ch.member {
				head = ch.Flags[:1]
				scope = ch.Flags[1:]
			}
			sub := pruneFlagsForHelp(scope, name)
			if len(sub) == 0 {
				continue
			}
			c := *ch
			c.Flags = append(append([]Flag{}, head...), sub...)
			kept = append(kept, &c)
		}
		if len(kept) == 0 {
			continue
		}
		full := flags[i]
		f.choiceDecls = kept
		f.helpFull = &full
		out = append(out, f)
	}
	return out
}

// formatFlagHelp renders the one-flag page: the command's header line, then
// the section holding the flag, as the command's page renders it.
func formatFlagHelp(app *App, req helpRequest) string {
	prefix := ""
	if len(req.address) > 2 {
		prefix = strings.Join(req.address[:len(req.address)-2], " ") + " "
	}
	lines := []string{fmtCommandHeader(app, req.cmd, prefix)}
	if req.global {
		for _, f := range app.globalFlags {
			if f.Name != req.flag {
				continue
			}
			lines = append(lines, "", "Global flags:",
				"  "+buildFlagSpec(f)+strings.Repeat(" ", 4)+f.Help+buildFlagMeta(f))
		}
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "", "Flags:")
	lines = append(lines, renderFlagEntries(collectFlagHelpEntries(pruneFlagsForHelp(req.cmd.flags, req.flag), 0))...)
	return strings.Join(lines, "\n")
}

// formatHelpOwnPage is the help command's own page.
func formatHelpOwnPage(app *App) string {
	return strings.Join([]string{
		app.Name + " help -- show the help of the app, a group, a command, or one of its flags",
		"",
		"Arguments:",
		"  address...    groups, then a command, then one of its flags, as typed after '" + app.Name + "' [optional]",
		"",
		"Flags:",
		"  --depth <int>    levels of groups and commands to list below the address, written before it [optional]",
		"",
		"With --json, the help document is printed as JSON.",
	}, "\n")
}

// formatVersionOwnPage is the version command's own page.
func formatVersionOwnPage(app *App) string {
	return app.Name + " version -- show the app's name and version\n\nWith --json, they are printed as JSON."
}

// helpText renders a parsed help request as text.
func (a *App) helpText(req helpRequest) string {
	switch {
	case req.own:
		return formatHelpOwnPage(a)
	case req.flag != "":
		return formatFlagHelp(a, req)
	case req.cmd != nil:
		prefix := ""
		if len(req.address) > 1 {
			prefix = strings.Join(req.address[:len(req.address)-1], " ") + " "
		}
		return formatCommandHelp(a, req.cmd, prefix)
	case req.group != nil:
		return formatGroupHelpDepth(a, req.group, req.groupPath, max(req.depth, 1))
	default:
		return formatAppHelpDepth(a, max(req.depth, 1))
	}
}

// helpDocument renders a parsed help request as the help document: the whole
// app's schema, or the same document pruned to the address, with `address`
// (and `depth`) recording the selection.
func (a *App) helpDocument(req helpRequest) (string, error) {
	doc, err := dumpSchemaOrdered(a)
	if err != nil {
		return "", err
	}
	anchor := "project_id"
	if len(req.address) > 0 {
		addr := make([]interface{}, len(req.address))
		for i, w := range req.address {
			addr[i] = w
		}
		doc.insertAfter(anchor, "address", addr)
		anchor = "address"
	}
	if req.depth > 0 {
		doc.insertAfter(anchor, "depth", req.depth)
	}
	node := doc
	for _, g := range req.groupPath {
		node.delete("commands")
		node.delete("deprecated")
		groups := node.get("groups").(*schemaObject)
		child := groups.get(g).(*schemaObject)
		node.set("groups", newSchemaObject().set(g, child))
		node = child
	}
	switch {
	case req.cmd != nil:
		node.delete("groups")
		node.delete("deprecated")
		entry := node.get("commands").(*schemaObject).get(req.cmd.Name).(*schemaObject)
		node.set("commands", newSchemaObject().set(req.cmd.Name, entry))
		if req.flag != "" {
			filterFlagEntries(entry, "flags", req.flag)
			filterFlagEntries(doc, "global_flags", req.flag)
		}
	case req.depth > 0:
		pruneGroupDepth(node, req.depth)
	}
	text, err := canonicalJSON(doc)
	if err != nil {
		return "", err
	}
	return text + "\n", nil
}

// pruneGroupDepth keeps depth levels of the command tree below node; a group at
// the last level keeps its own entry and loses its children.
func pruneGroupDepth(node *schemaObject, depth int) {
	groups, ok := node.get("groups").(*schemaObject)
	if !ok {
		return
	}
	for _, name := range groups.keys {
		g := groups.get(name).(*schemaObject)
		if depth <= 1 {
			g.delete("commands")
			g.delete("groups")
			g.delete("deprecated")
			continue
		}
		pruneGroupDepth(g, depth-1)
	}
}

// filterFlagEntries keeps, in obj[key], the flag entries that declare name
// somewhere in their subtree, pruning selectors to the choices that do. An
// emptied list is removed, as an empty one is omitted everywhere else.
func filterFlagEntries(obj *schemaObject, key, name string) {
	list, ok := obj.get(key).([]interface{})
	if !ok {
		return
	}
	kept := filterFlagList(list, name)
	if len(kept) == 0 {
		obj.delete(key)
		return
	}
	obj.set(key, kept)
}

func filterFlagList(list []interface{}, name string) []interface{} {
	var kept []interface{}
	for _, item := range list {
		entry, ok := item.(*schemaObject)
		if !ok {
			continue
		}
		electBy, isSelector := entry.get("elect_by").(string)
		if !isSelector {
			if entry.get("name") == name {
				kept = append(kept, entry)
			}
			continue
		}
		if electBy == "selector-token" && entry.get("name") == name {
			kept = append(kept, entry)
			continue
		}
		choices, _ := entry.get("choices").([]interface{})
		var keptChoices []interface{}
		for _, c := range choices {
			ch := c.(*schemaObject)
			if electBy == "member-flags" && ch.get("name") == name {
				keptChoices = append(keptChoices, ch)
				continue
			}
			if scope, ok := ch.get("flags").([]interface{}); ok {
				if sub := filterFlagList(scope, name); len(sub) > 0 {
					ch.set("flags", sub)
					keptChoices = append(keptChoices, ch)
				}
			}
		}
		if len(keptChoices) > 0 {
			entry.set("choices", keptChoices)
			kept = append(kept, entry)
		}
	}
	return kept
}

// versionDocument is `version --json`'s document.
func (a *App) versionDocument() (string, error) {
	text, err := canonicalJSON(newSchemaObject().set("name", a.Name).set("version", a.Version))
	if err != nil {
		return "", err
	}
	return text + "\n", nil
}

// dispatchFrameworkCommand handles `help` and `version` as the first command
// word. ok is false when rest names neither.
func (a *App) dispatchFrameworkCommand(rest []string) (parseResult, bool) {
	if len(rest) == 0 {
		return parseResult{}, false
	}
	switch rest[0] {
	case helpCommandName:
		prefix := a.Name + " " + helpCommandName
		req, err := a.parseHelpCommand(rest[1:])
		if err != "" {
			return parseResult{parseErr: err, commandPrefix: prefix}, true
		}
		if !a.lastJSON || req.own {
			if a.lastJSON {
				return parseResult{parseErr: errHelpTextOnly(prefix + " --json"), commandPrefix: prefix}, true
			}
			return parseResult{helpText: a.helpText(req)}, true
		}
		doc, derr := a.helpDocument(req)
		if derr != nil {
			return parseResult{parseErr: derr.Error(), commandPrefix: prefix}, true
		}
		return parseResult{frameworkDoc: doc, frameworkCommand: helpCommandName}, true
	case versionCommandName:
		prefix := a.Name + " " + versionCommandName
		for _, tok := range rest[1:] {
			if tok == "--help" || tok == "-h" {
				if a.lastJSON {
					return parseResult{parseErr: errHelpTextOnly(a.Name + " " + helpCommandName + " --json"), commandPrefix: prefix}, true
				}
				return parseResult{helpText: formatVersionOwnPage(a)}, true
			}
		}
		if len(rest) > 1 {
			return parseResult{parseErr: errVersionArgs(rest[1]), commandPrefix: prefix}, true
		}
		if !a.lastJSON {
			return parseResult{versionText: formatVersion(a)}, true
		}
		doc, err := a.versionDocument()
		if err != nil {
			return parseResult{parseErr: err.Error(), commandPrefix: prefix}, true
		}
		return parseResult{frameworkDoc: doc, frameworkCommand: versionCommandName}, true
	}
	return parseResult{}, false
}

// isGlobalFlagToken reports whether tok spells one of the app's global flags as
// a long name, the one flag spelling that can reach help before any address.
func (a *App) isGlobalFlagToken(tok string) bool {
	for _, g := range a.globalFlags {
		if tok == "--"+g.Name {
			return true
		}
	}
	return false
}
