package strictcli

import (
	"fmt"
	"strings"
)

// Tool is a descriptor for exposing CLI commands to tool-using LLM agents.
//
// Effect and Consequential publish the effects-regime classification BESIDE
// the argument schema (never inside it): a consumer rendering this tool must
// be able to see that the command changes things and that calling it requires
// stating consent. Same vocabulary as the schema dump: Effect is mandatory,
// Consequential defaults to false.
type Tool struct {
	Name          string
	Description   string
	Parameters    map[string]interface{}
	Effect        string
	Consequential bool
	Execute       func(map[string]interface{}, ...CallOption) (interface{}, error)
}

// buildJSONSchema builds a JSON Schema parameters object for a command's
// flags and positional args.
func buildJSONSchema(cmd *Command) map[string]interface{} {
	properties := map[string]interface{}{}
	// Initialized rather than declared nil: encoding/json renders a nil slice
	// as null, and "required": null is not valid JSON Schema. A command with
	// no required parameters must publish an empty list, as Python and
	// TypeScript do.
	required := []interface{}{}

	// The MCP projection is FLATTEN plus a description map (contract §24.11): a
	// selector contributes one enum property named after itself, every scoped
	// flag contributes a top-level property and NEVER appears in "required" --
	// its requiredness is conditional and the schema has no vocabulary for that
	// -- and a member-spelled selector projects identically to a token-spelled
	// one, because tokenization is a command-line fact and there are no tokens
	// at this boundary.
	addScopedProperties(properties, cmd.flags)

	for i := range cmd.flags {
		f := &cmd.flags[i]
		paramName := flagParamName(f.Name)

		if f.Type == TypeChoice {
			properties[paramName] = map[string]interface{}{
				"type":        "string",
				"enum":        choiceEnum(f),
				"description": f.Help,
			}
			if f.presence == presenceRequired {
				required = append(required, paramName)
			}
			continue
		}

		// The parameter's shape is the SAME fragment the dumped `value_schema`
		// publishes (contract §25.13), so an arity and an `enum` placement
		// cannot disagree between the two doors: the array rows carry their
		// `enum` inside `items`, describing an element rather than the array.
		// The one fact the doors state differently is nullability, folded into
		// the type list immediately below (§25.13's amendment, §27.10).
		prop := toPlain(flagValueSchema(f)).(map[string]interface{})
		prop["description"] = f.Help
		// A NULLABLE property publishes a type list including "null" (contract
		// §27.10). This projection is not bound by §25.2's four-keyword subset
		// -- it already emits anyOf and dependentRequired -- and here the null
		// is a VALUE the declaration names rather than a spelling of absence,
		// so publishing it in the type is publishing the declaration. A caller
		// that cannot see it cannot clear anything.
		if f.Nullable {
			if t, ok := prop["type"]; ok {
				prop["type"] = []interface{}{t, "null"}
			}
		}

		properties[paramName] = prop

		// A parameter is required iff its DECLARED presence is required
		// (contract §13's presence-round amendment, §23.7). The old
		// four-clause derivation is gone, and with it the exclusion of bools:
		// "bool flags always have a default" is false by construction now.
		if f.presence == presenceRequired {
			required = append(required, paramName)
		}
	}

	for i := range cmd.args {
		a := &cmd.args[i]
		prop := toPlain(argValueSchema(a)).(map[string]interface{})
		prop["description"] = a.Help

		properties[a.Name] = prop

		if a.presence == presenceRequired {
			required = append(required, a.Name)
		}
	}

	// The arguments after "--" (WithArgsAfterSeparator) are one list of
	// strings under the receiver's name; never required, since a command line
	// without "--" supplies none.
	if r := cmd.argsAfterSeparator; r != nil {
		properties[r.name] = map[string]interface{}{
			"type":        "array",
			"items":       map[string]interface{}{"type": "string"},
			"description": r.help,
		}
	}

	schema := map[string]interface{}{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
	// The constraint projection sits BESIDE properties and required on this one
	// flat object schema (contract §26.12), and carries the update's own
	// at-least-one-property branch ahead of it (§27.10). A property is never in
	// `required`: its requiredness IS the at-least-one rule, which `required`
	// cannot express. Enforcement at call time is
	// unchanged and total: the schema is advisory, the runtime refusal is the
	// authority.
	projectConstraints(cmd, schema)
	return schema
}

// JsonSchema produces a JSON Schema parameters object for a command's flags
// and positional args.
//
// commandPath is a dot-separated path to the command (e.g. "deploy" or
// "config.show").
//
// Returns a JSON Schema object with "type": "object", "properties",
// "required", and "additionalProperties": false.
//
// Panics if the command path is invalid or resolves to a group.
func (a *App) JsonSchema(commandPath string) map[string]interface{} {
	segments := strings.Split(commandPath, ".")
	route := a.resolveCommand(segments)
	if route.err != "" {
		panic(errJsonSchemaRouteError(route.err))
	}
	if route.cmd == nil {
		panic(errJsonSchemaIsGroup(commandPath))
	}
	return buildJSONSchema(route.cmd)
}

// AsTools exports non-hidden, non-interactive leaf commands as Tool
// descriptors. Returns a list of Tool objects, one per eligible command plus
// a router tool. Each tool's Execute function wraps App.Call().
func (a *App) AsTools() []Tool {
	var tools []Tool
	var commandPaths []string

	// Collect leaf commands from top-level in insertion order
	for _, name := range a.cmdOrder {
		cmd, ok := a.commands[name]
		if !ok {
			continue
		}
		if cmd.Hidden || cmd.Interactive {
			continue
		}
		tools = append(tools, a.makeTool(name, cmd))
		commandPaths = append(commandPaths, name)
	}

	// Collect leaf commands from groups (recursive) in insertion order
	for _, groupName := range a.groupOrder {
		grp, ok := a.groups[groupName]
		if !ok {
			continue
		}
		a.collectToolsFromGroup(grp, []string{groupName}, &tools, &commandPaths)
	}

	// Build the router tool
	tools = append(tools, a.makeRouterTool(commandPaths))

	return tools
}

// collectToolsFromGroup recursively collects non-hidden, non-interactive
// commands from a group and its subgroups.
func (a *App) collectToolsFromGroup(
	group *Group,
	path []string,
	tools *[]Tool,
	commandPaths *[]string,
) {
	if group.Hidden {
		return
	}

	// Commands in insertion order
	for _, cmdName := range group.order {
		cmd, ok := group.Commands[cmdName]
		if !ok {
			continue
		}
		if cmd.Hidden || cmd.Interactive {
			continue
		}
		dotted := strings.Join(append(path, cmdName), ".")
		*tools = append(*tools, a.makeTool(dotted, cmd))
		*commandPaths = append(*commandPaths, dotted)
	}

	// Subgroups in insertion order
	for _, subName := range group.groupOrder {
		subGroup, ok := group.Groups[subName]
		if !ok {
			continue
		}
		a.collectToolsFromGroup(subGroup, append(path, subName), tools, commandPaths)
	}
}

// makeTool builds a Tool for a single command.
func (a *App) makeTool(commandPath string, cmd *Command) Tool {
	return Tool{
		Name:          commandPath,
		Description:   toolDescription(cmd),
		Parameters:    buildJSONSchema(cmd),
		Effect:        cmd.Effect,
		Consequential: cmd.Consequential,
		Execute: func(kwargs map[string]interface{}, opts ...CallOption) (interface{}, error) {
			return a.Call(commandPath, kwargs, opts...)
		},
	}
}

// makeRouterTool builds the router tool that lists and dispatches to
// per-command tools.
func (a *App) makeRouterTool(commandPaths []string) Tool {
	// Copy to avoid aliasing
	paths := make([]string, len(commandPaths))
	copy(paths, commandPaths)

	// Build enum list as []interface{} for JSON schema
	enumValues := make([]interface{}, len(paths))
	for i, p := range paths {
		enumValues[i] = p
	}

	parameters := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "Command to execute (dot-separated path)",
				"enum":        enumValues,
			},
		},
		"required":             []interface{}{"command"},
		"additionalProperties": false,
	}

	// The router can reach a mutating command, so it classifies as mutating. It
	// is NOT itself consequential: the routed command's own requirement is
	// checked when the call reaches it, and the router forwards the caller's
	// consent unchanged. Marking the router consequential would demand consent
	// for routing to a read_only command, which confirms nothing.
	return Tool{
		Name:          a.Name,
		Description:   fmt.Sprintf("Route to %s commands", a.Name),
		Parameters:    parameters,
		Effect:        EffectMutating,
		Consequential: false,
		Execute: func(kwargs map[string]interface{}, opts ...CallOption) (interface{}, error) {
			cmdVal, ok := kwargs["command"]
			if !ok {
				// No command specified -- return list of available commands
				result := make([]string, len(paths))
				copy(result, paths)
				return result, nil
			}
			cmdPath, ok := cmdVal.(string)
			if !ok {
				return nil, &InvokeError{Message: "command must be a string"}
			}
			// Strip "command" before forwarding to Call
			fwd := make(map[string]interface{}, len(kwargs)-1)
			for k, v := range kwargs {
				if k != "command" {
					fwd[k] = v
				}
			}
			return a.Call(cmdPath, fwd, opts...)
		},
	}
}

// choiceEnum renders a selector's declared choice names as a JSON Schema enum.
func choiceEnum(sel *Flag) []interface{} {
	out := make([]interface{}, len(sel.choiceDecls))
	for i, c := range sel.choiceDecls {
		out[i] = c.Name
	}
	return out
}

// addScopedProperties flattens every scoped flag into the command's own
// argument object, at every depth. A member-spelled choice's payload flattens
// under the MEMBER's own flag name, which the framework already guarantees
// unique command-wide (contract §24.11). Nothing added here ever joins the
// schema's "required" array.
//
// The electing member flag is NOT a scoped flag: a payload-carrying member
// contributes exactly its payload property, and a payload-less one contributes
// nothing at all -- electing it is the whole of what it says, and the
// selector's own enum already publishes that. A boolean property named after it
// would be a second spelling of an election the schema already carries.
func addScopedProperties(properties map[string]interface{}, flags []Flag) {
	var walk func(flags []Flag, owner *ChoiceDecl)
	walk = func(flags []Flag, owner *ChoiceDecl) {
		for i := range flags {
			f := &flags[i]
			if owner != nil {
				if owner.member && memberFlag(owner) == f {
					if choiceCarriesPayload(owner) {
						properties[flagParamName(f.Name)] = scopedPropertySchema(f)
					}
				} else {
					properties[flagParamName(f.Name)] = scopedPropertySchema(f)
				}
			}
			if f.Type != TypeChoice {
				continue
			}
			for _, ch := range f.choiceDecls {
				walk(ch.Flags, ch)
			}
		}
	}
	walk(flags, nil)
}

// scopedPropertySchema is one scoped flag's JSON Schema property, derived from
// the same fragment the dump publishes (contract §25.13).
func scopedPropertySchema(f *Flag) map[string]interface{} {
	if f.Type == TypeChoice {
		return map[string]interface{}{
			"type":        "string",
			"enum":        choiceEnum(f),
			"description": f.Help,
		}
	}
	prop := toPlain(flagValueSchema(f)).(map[string]interface{})
	prop["description"] = f.Help
	return prop
}

// toolDescription is a command's help plus, when it declares any selector, the
// deterministic scope block an agent reads to learn the constraint it cannot
// see in the flat schema (contract §24.11).
//
//	Scoped parameters (enforced at call time):
//	  via=email: subject (required), recipient (required)
//	  via=sms: phone_number (required)
//	  visibility=user-facing type=feature: (no parameters)
//
// One line per scope, at every depth, in declaration order. The key is the
// scope's path rendered as `<selector>=<choice>` segments joined by a single
// space, using the property names the schema publishes rather than the flags a
// CLI user types.
func toolDescription(cmd *Command) string {
	out := cmd.Help
	if cmd.index != nil && cmd.index.hasSelectors {
		if lines := scopeDescriptionLines(cmd.flags, nil); len(lines) > 0 {
			out += "\n\nScoped parameters (enforced at call time):\n  " + strings.Join(lines, "\n  ")
		}
	}
	// The constraint block, in the scope block's shape, appended after it when
	// both exist and separated from it by a blank line (contract §26.12). A
	// constraint is NEVER silently dropped from a tool schema: every kind has a
	// line here, and a partial projection states its remainder in words.
	if len(cmd.constraints) > 0 {
		fidelities := projectConstraints(cmd, map[string]interface{}{})
		if lines := constraintDescriptionLines(cmd, fidelities); len(lines) > 0 {
			out += "\n\nConstraints (enforced at call time):\n  " + strings.Join(lines, "\n  ")
		}
	}
	// The update block, appended after both when they exist and separated by a
	// blank line -- the established order (contract §27.10).
	if header, lines := updateDescriptionLines(cmd); header != "" {
		out += "\n\n" + header + "\n  " + strings.Join(lines, "\n  ")
	}
	return out
}

// scopeDescriptionLines renders one line per scope, depth-first in declaration
// order.
func scopeDescriptionLines(flags []Flag, prefix []string) []string {
	var out []string
	for i := range flags {
		f := &flags[i]
		if f.Type != TypeChoice {
			continue
		}
		for _, ch := range f.choiceDecls {
			// The selector is the schema PROPERTY name; the choice is the enum
			// value the schema publishes, carried unchanged.
			key := append(append([]string{}, prefix...), flagParamName(f.Name)+"="+ch.Name)
			out = append(out, strings.Join(key, " ")+": "+scopeParameterList(ch))
			out = append(out, scopeDescriptionLines(ch.Flags, key)...)
		}
	}
	return out
}

// scopeParameterList renders one scope's parameters in declaration order, each
// with its presence in parentheses. An empty scope renders "(no parameters)".
func scopeParameterList(ch *ChoiceDecl) string {
	var parts []string
	for i := range ch.Flags {
		f := &ch.Flags[i]
		if ch.member && i == 0 && !choiceCarriesPayload(ch) {
			continue
		}
		parts = append(parts, flagParamName(f.Name)+" ("+scopePresenceWord(f)+")")
	}
	if len(parts) == 0 {
		return "(no parameters)"
	}
	return strings.Join(parts, ", ")
}

func scopePresenceWord(f *Flag) string {
	switch f.presence {
	case presenceRequired:
		return "required"
	case presenceOptional:
		return "optional"
	default:
		return "default: " + formatValueForError(f.Default)
	}
}
