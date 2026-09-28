package strictcli

import (
	"strings"
)

// routeResult holds the outcome of command routing through the group/command tree.
type routeResult struct {
	// Exactly one of cmd, lastGroup, or err will be meaningful.
	cmd       *Command // resolved command, nil if routing stopped at a group or errored
	lastGroup *Group   // deepest group reached (set even when cmd is found)
	path      []string // group names traversed to reach the result
	rest      []string // remaining tokens after routing consumed path + command name

	// Error cases
	err string // non-empty if routing failed (deprecated, unknown, no command)
	// commandPrefix is set for error messages that need the full path context
	commandPrefix string
	// helpAtGroup is true when the user requested help at a group level
	helpAtGroup bool
}

// resolveCommand traverses the group/command/deprecated tree using tokens from
// rest, returning the resolved command or an error. It does not perform command
// parsing or help formatting — callers use the returned routeResult to decide
// the next step.
func (a *App) resolveCommand(rest []string) routeResult {
	currentGroups := a.groups
	currentCommands := a.commands
	currentDeprecated := a.deprecatedMap
	var path []string
	var lastGroup *Group

	for len(rest) > 0 {
		token := rest[0]

		// Check groups first
		if grp, ok := currentGroups[token]; ok {
			path = append(path, token)
			lastGroup = grp
			rest = rest[1:]

			// Help at this level. A --help/-h that LEADS the remaining tokens
			// requests this group's help whatever follows it -- `myapp db
			// --help ./m` is help, not an unknown command. Python and
			// TypeScript route on that leading token alone; requiring it to be
			// the only token left was Go's divergence.
			if len(rest) == 0 || rest[0] == "--help" || rest[0] == "-h" {
				return routeResult{lastGroup: lastGroup, path: path, rest: rest, helpAtGroup: true}
			}

			// Descend into subgroup
			currentGroups = grp.Groups
			currentCommands = grp.Commands
			currentDeprecated = grp.deprecatedMap
			continue
		}

		// Check commands
		if cmd, ok := currentCommands[token]; ok {
			return routeResult{cmd: cmd, lastGroup: lastGroup, path: path, rest: rest[1:]}
		}

		// Check deprecated commands
		if msg, ok := currentDeprecated[token]; ok {
			return routeResult{
				err:  errCommandDeprecated(token, msg),
				path: path, rest: rest,
			}
		}

		// help and version are framework commands at the root only; their names
		// are reserved at every level, so inside a group they point at the root.
		if len(path) > 0 && token == helpCommandName {
			return routeResult{err: errHelpInGroup(a.helpLine(path...)), path: path, rest: rest}
		}
		if len(path) > 0 && token == versionCommandName {
			return routeResult{err: errVersionInGroup(a.Name + " " + versionCommandName), path: path, rest: rest}
		}

		// Unknown command — include path in error message
		if len(path) > 0 {
			prefix := strings.Join(append([]string{a.Name}, path...), " ")
			return routeResult{
				err:           errUnknownCommandInGroup(token, strings.Join(path, " ")),
				commandPrefix: prefix,
				path:          path, rest: rest,
			}
		}
		return routeResult{
			err:  errUnknownCommand(token),
			path: path, rest: rest,
		}
	}

	// Loop ended without finding a command — exhausted by group traversal
	if lastGroup != nil {
		return routeResult{lastGroup: lastGroup, path: path, rest: rest, helpAtGroup: true}
	}
	return routeResult{err: errNoCommandSpecified, path: path, rest: rest}
}
