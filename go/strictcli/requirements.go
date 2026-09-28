package strictcli

import (
	"fmt"
	"strings"
)

// Declared runtime requirements. A program declares what a command needs at
// run time -- a system library loaded when the command runs, an executable, a
// device -- ONCE, as a Requirement value, and every command that needs it
// references that value. Before the handler runs, on every door (Run, Test,
// Call and the MCP server) and in dry mode too, the framework loads each of
// the command's requirements; a missing one ends the command with exit 1 and
// one error naming what is missing and how to install it, so a command that
// needs nothing never fails for want of something it does not use. The
// handler reads a loaded value through Need.

// Requirement is one runtime requirement: a name, one line saying what it is,
// one line saying how to install it, and the function that loads it.
type Requirement[T any] struct {
	name    string
	help    string
	install string
	load    func() (T, error)
}

// AnyRequirement is any Requirement, whatever it loads. It is what WithRequires
// takes; only NewRequirement makes one.
type AnyRequirement interface {
	requirementName() string
	requirementHelp() string
	requirementInstall() string
	loadAny() (interface{}, error)
}

func (r *Requirement[T]) requirementName() string    { return r.name }
func (r *Requirement[T]) requirementHelp() string    { return r.help }
func (r *Requirement[T]) requirementInstall() string { return r.install }
func (r *Requirement[T]) loadAny() (interface{}, error) {
	return r.load()
}

// NewRequirement declares a runtime requirement. name follows the naming rule;
// help and install are one non-empty line each; load returns the loaded value,
// or an error saying why it is not available.
func NewRequirement[T any](name, help, install string, load func() (T, error)) *Requirement[T] {
	if !isKebabName(name) {
		panic(errRequirementNameInvalid(name))
	}
	if !isOneLine(help) {
		panic(errRequirementHelpInvalid(name))
	}
	if !isOneLine(install) {
		panic(errRequirementInstallInvalid(name))
	}
	if load == nil {
		panic(errRequirementLoadMissing(name))
	}
	return &Requirement[T]{name: name, help: help, install: install, load: load}
}

func isOneLine(s string) bool {
	return strings.TrimSpace(s) != "" && !strings.ContainsAny(s, "\r\n")
}

// WithRequires declares what the command needs at run time.
func WithRequires(reqs ...AnyRequirement) CmdOption {
	return func(c *Command) {
		c.requires = append(c.requires, reqs...)
	}
}

// validateRequires refuses a requirement a command references twice.
func validateRequires(cmdName string, reqs []AnyRequirement) {
	seen := make(map[string]bool, len(reqs))
	for _, r := range reqs {
		if seen[r.requirementName()] {
			panic(errRequirementDeclaredTwice(cmdName, r.requirementName()))
		}
		seen[r.requirementName()] = true
	}
}

// registerRequirements refuses a second requirement value under a name the app
// already knows: a requirement is declared once and referenced everywhere.
func (a *App) registerRequirements(cmd *Command) {
	if a.requirements == nil {
		a.requirements = map[string]AnyRequirement{}
	}
	for _, r := range cmd.requires {
		if known, ok := a.requirements[r.requirementName()]; ok && known != r {
			panic(errRequirementNameReused(r.requirementName()))
		}
		a.requirements[r.requirementName()] = r
	}
}

// loadRequirements loads a command's requirements in declaration order, before
// its handler runs, and ends the command through ExitNow at the first one that
// is not available.
func loadRequirements(ctx *Context, cmd *Command, cmdPath string) {
	if len(cmd.requires) == 0 {
		return
	}
	loaded := make(map[string]interface{}, len(cmd.requires))
	for _, r := range cmd.requires {
		value, err := r.loadAny()
		if err != nil {
			ExitNow(1, errRequirementUnavailable(cmdPath, r.requirementName(), r.requirementHelp(), err.Error(), r.requirementInstall()))
		}
		loaded[r.requirementName()] = value
	}
	ctx.loadedRequirements = loaded
	ctx.declaredRequirements = cmd.requires
}

// Need returns the value a requirement the command declared loaded before the
// handler ran. Asking for a requirement the command did not declare panics.
func Need[T any](ctx *Context, r *Requirement[T]) T {
	for _, d := range ctx.declaredRequirements {
		if d == AnyRequirement(r) {
			return ctx.loadedRequirements[r.name].(T)
		}
	}
	panic(errNeedUndeclared(ctx.commandName, r.name))
}

// serializeRequires is the help document's `requires` entry of a command.
func serializeRequires(reqs []AnyRequirement) []interface{} {
	out := make([]interface{}, len(reqs))
	for i, r := range reqs {
		out[i] = newSchemaObject().
			set("name", r.requirementName()).
			set("help", r.requirementHelp()).
			set("install", r.requirementInstall())
	}
	return out
}

// formatRequirementsSection is the `Requirements:` section of command help.
func formatRequirementsSection(reqs []AnyRequirement) []string {
	if len(reqs) == 0 {
		return nil
	}
	lines := []string{"", "Requirements:"}
	width := 0
	for _, r := range reqs {
		if len(r.requirementName()) > width {
			width = len(r.requirementName())
		}
	}
	for _, r := range reqs {
		lines = append(lines, fmt.Sprintf("  %s%s%s; install it: %s", r.requirementName(),
			strings.Repeat(" ", width-len(r.requirementName())+4), r.requirementHelp(), r.requirementInstall()))
	}
	return lines
}
