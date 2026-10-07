package strictcli

import (
	"fmt"
	"sort"
	"strings"
)

// enableChecks turns on the check system exactly once. It flips checksEnabled,
// initializes the check registry if absent, and registers the auto-generated
// "check" command a single time. Idempotent: calling it again is a no-op, which
// prevents double-registration (Command appends to cmdOrder on every call).
func (a *App) enableChecks() {
	if a.checksEnabled {
		return
	}
	a.checksEnabled = true
	if a.checkDefs == nil {
		a.checkDefs = make(map[string]*checkDef)
	}
	a.registerCheckCommand()
	// The built-in effects-bypass lint rides the same provider hook the built-in
	// cli-test-coverage check uses. Appended directly (not through
	// RegisterCheckProvider) because that method routes back here.
	a.checkProviders = append(a.checkProviders, a.effectsBypassProvider)
	a.providerMaterialized = false
	a.providerMaterializedCwd = ""
}

// checkCommandHelp and failingChecksCommandHelp are the two check commands'
// one-line help texts.
const (
	checkCommandHelp         = "Run project checks registered via the check framework and report results"
	failingChecksCommandHelp = "Run project checks and report only error-level failures, exiting nonzero when any exist"
)

// checkHookFlagHelp is the --hook flag's help: what it does plus every
// declared hook and the tag expression it selects, sorted by hook name.
func checkHookFlagHelp(hooks map[string]string) string {
	if len(hooks) == 0 {
		return "Run the checks a hook declared in checks.toml selects (no hooks are declared)"
	}
	names := sortedHookNames(hooks)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%s (tag '%s')", n, hooks[n])
	}
	return "Run the checks a hook declared in checks.toml selects: " + strings.Join(parts, ", ")
}

func sortedHookNames(hooks map[string]string) []string {
	names := make([]string, 0, len(hooks))
	for n := range hooks {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// registerCheckCommand registers the auto-generated "check" and
// "failing-checks" commands. Called from enableChecks when the check system is
// turned on.
func (a *App) registerCheckCommand() {
	a.registerOneCheckCommand("check", false)
	a.registerOneCheckCommand("failing-checks", true)
}

// registerOneCheckCommand registers one of the two check commands. "check" is
// the full report and exits nonzero on any failure or warning;
// "failing-checks" runs the same selection and reports only the error-level
// failures, exiting nonzero exactly when one exists.
func (a *App) registerOneCheckCommand(commandName string, failingOnly bool) {
	handler := func(ctx *Context, args map[string]interface{}) Outcome {
		// Materialize provider-sourced checks before any registry read (covers
		// the --list and execution branches below).
		a.materializeCheckProviders()

		runAll := Get[bool](args, "all")
		tagExpr := Get[string](args, "tag")
		nameGlob := Get[string](args, "name")
		hook := Get[string](args, "hook")
		list := Get[bool](args, "list")
		// --verbose, --dry-run and --json are framework-owned reserved names,
		// so the check command declares none of them and reads their values off
		// the Context instead. The machine output is this command's payload
		// (contract §19.4), which is why --json is not a flag here any more.
		// Nothing below branches on ctx.JSON(): the payload call is
		// mode-independent (§19.4) and every human line goes through a context
		// writer, so machine mode carries the text as diagnostics and stdout
		// keeps exactly one document (§19.1).
		verbose := ctx.Verbose()
		dryRun := ctx.DryRun()

		if list {
			return Exit(a.checkList(ctx))
		}

		// A named hook stands for its declared selection, and is the whole
		// selection: it combines with no other selection flag.
		if hook != "" {
			if runAll || tagExpr != "" || nameGlob != "" {
				ctx.Error(errCheckHookCombined)
				return Exit(1)
			}
			expr, ok := a.checkHooks[hook]
			if !ok {
				if len(a.checkHooks) > 0 {
					ctx.Error(errCheckHookUnknown(hook, strings.Join(sortedHookNames(a.checkHooks), ", ")))
				} else {
					ctx.Error(errCheckHookNoneDeclared(hook))
				}
				return Exit(1)
			}
			tagExpr = expr
		}

		if !(runAll || tagExpr != "" || nameGlob != "") {
			// No flags: show help
			cmd := a.commands[commandName]
			ctx.Info(formatCommandHelp(a, cmd, ""))
			return Exit(0)
		}

		// --dry-run is not a separate branch: it selects the purity partition,
		// so the checks declared pure really run and only the impure remainder
		// is rendered as the would-run plan.
		return Exit(a.checkRun(ctx, runAll, tagExpr, nameGlob, verbose, dryRun, failingOnly))
	}
	// Filter out candidate flags that already exist as global flags to avoid
	// collisions -- the handler absorbs global flag values automatically.
	// --verbose, --dry-run and --json are absent from the candidate list
	// entirely: all three names are now reserved by the framework and their
	// values arrive on the Context (§7.5 and its 2026-08-13 sweep box).
	globalFlagNames := make(map[string]bool, len(a.globalFlags))
	for _, gf := range a.globalFlags {
		globalFlagNames[gf.Name] = true
	}
	candidates := []Flag{
		BoolFlag("all", "Run every registered check regardless of tag or name filters", Default(false)),
		StringFlag("tag", "Tag DSL expression to select checks (e.g. 'changelog & !quality')", Default("")),
		StringFlag("name", "Glob pattern to filter checks by name (e.g. 'hash-*', '*coverage*')", Default("")),
		StringFlag("hook", checkHookFlagHelp(a.checkHooks), Default("")),
		BoolFlag("list", "List all registered checks with their tags and values and exit without running", Default(false)),
	}
	extraFlags := make([]Flag, 0, len(candidates))
	for _, f := range candidates {
		if !globalFlagNames[f.Name] {
			extraFlags = append(extraFlags, f)
		}
	}
	help := checkCommandHelp
	if failingOnly {
		help = failingChecksCommandHelp
	}
	// read_only: the check command's only writes are framework-blessed
	// CACHE_WRITEs (the coverage manifest), which never trip enforcement.
	// It declares a scratch directory, which the check context factory may
	// ask for, so a check can run a program that writes for itself (a
	// package manager's cache) without writing anywhere else.
	a.registerFrameworkCommand(commandName, help,
		EffectReadOnly, handler, WithFlags(extraFlags...),
		PayloadSchema(checkPayloadSchema), WithScratchDir())
}

// registerFrameworkCommand registers one of strictcli's own auto-registered
// commands through the single validated registration path. Their handlers
// absorb the app's app-defined global flag values, which is legal only because
// they declare forwarding, and the private framework-internal marker makes the
// framework verify that the handler really is defined in this package.
func (a *App) registerFrameworkCommand(name, help, effect string, handler func(ctx *Context, args map[string]interface{}) Outcome, opts ...CmdOption) {
	all := append([]CmdOption{
		WithEffect(effect),
		WithForwarding(frameworkInternalForwardingReason),
		withFrameworkInternal(),
	}, opts...)
	a.Command(name, help, handler, all...)
}

// registerFrameworkSubcommand is registerFrameworkCommand for a command living
// inside a framework-owned group (the five `config` subcommands).
func registerFrameworkSubcommand(grp *Group, name, help, effect string, handler func(ctx *Context, args map[string]interface{}) Outcome, opts ...CmdOption) {
	all := append([]CmdOption{
		WithEffect(effect),
		WithForwarding(frameworkInternalForwardingReason),
		withFrameworkInternal(),
	}, opts...)
	grp.Command(name, help, handler, all...)
}

// wrapCheckContext augments a tool-supplied CheckContext with connection-env
// access derived from the framework *Context's infra snapshot. When the app
// declares no connection envs, the base context is returned unchanged so the
// common case is unaffected.
func (a *App) wrapCheckContext(base CheckContext, frameworkCtx *Context) CheckContext {
	if len(a.connectionEnvs) == 0 {
		return base
	}
	var infra *infraAccess
	if frameworkCtx != nil {
		infra = frameworkCtx.infra
	}
	return checkContextWithConn{CheckContext: base, infra: infra}
}

// checkPayloadSchema is the check command's machine payload contract (contract
// §19.5). Both of the command's machine shapes -- the listing (--list) and the
// run results -- are arrays of objects. Framework-owned literal, byte-identical
// across the three implementations.
var checkPayloadSchema = map[string]interface{}{
	"type":  "array",
	"items": map[string]interface{}{"type": "object"},
}

// checkList implements the --list mode. The payload is supplied
// unconditionally (§19.4) and the human table goes through the context writer,
// so machine mode carries it as one diagnostic instead of a second stdout
// document. Every check's effective value (after the check value resolver) is
// resolved first; a value the resolver may not return is this command's error.
func (a *App) checkList(ctx *Context) int {
	values, err := a.resolveCheckValues(a.checkOrder)
	if err != nil {
		ctx.Error(err.Error())
		return 1
	}
	ctx.Payload(a.checkListItems(values))
	return a.checkListHuman(ctx, values)
}

// checkListHuman writes an aligned table of checks through the context writer.
// The whole table is ONE Info call, so machine mode carries it as a single
// diagnostic rather than one per row.
func (a *App) checkListHuman(ctx *Context, values map[string]resolvedCheckValue) int {
	order := a.checkOrder
	if len(order) == 0 {
		ctx.Info("No checks defined.")
		return 0
	}

	// Compute column widths
	maxName := len("NAME")
	maxTags := len("TAGS")
	for _, name := range order {
		if len(name) > maxName {
			maxName = len(name)
		}
		def := a.checkDefs[name]
		tagsStr := strings.Join(def.tags, ", ")
		if len(tagsStr) > maxTags {
			maxTags = len(tagsStr)
		}
	}
	sevWidth := len("SEVERITY")
	valueWidth := len("VALUE")

	lines := []string{fmt.Sprintf("%-*s   %-*s   %-*s   %-*s   %s", maxName, "NAME", maxTags, "TAGS", sevWidth, "SEVERITY", valueWidth, "VALUE", "SOURCE")}
	for _, name := range order {
		def := a.checkDefs[name]
		tagsStr := strings.Join(def.tags, ", ")
		v := values[name]
		lines = append(lines, fmt.Sprintf("%-*s   %-*s   %-*s   %-*s   %s", maxName, name, maxTags, tagsStr, sevWidth, def.severity, valueWidth, v.value, v.source))
	}
	ctx.Info(strings.Join(lines, "\n"))
	return 0
}

type checkEntry struct {
	Name     string   `json:"name"`
	Tags     []string `json:"tags"`
	Severity string   `json:"severity"`
	Value    string   `json:"value"`
	Source   string   `json:"source"`
	Scope    string   `json:"scope,omitempty"`
}

// checkListItems is the --list mode's machine payload (contract §19.4). Each
// entry carries the check's effective value and where that value came from.
func (a *App) checkListItems(values map[string]resolvedCheckValue) []checkEntry {
	entries := make([]checkEntry, len(a.checkOrder))
	for i, name := range a.checkOrder {
		def := a.checkDefs[name]
		entries[i] = checkEntry{
			Name:     name,
			Tags:     def.tags,
			Severity: def.severity,
			Value:    values[name].value,
			Source:   values[name].source,
			Scope:    def.scope,
		}
	}
	return entries
}

// checkDryRunPlan prints the would-run plan for the checks a dry run did NOT
// execute -- the purity partition's remainder (the impure checks and any check
// whose dependency was listed). The header is printed even when nothing was
// left over: an empty plan is a statement ("everything selected ran"), the same
// way the framework's own would-do log prints its header with an empty body.
//
// The whole plan is ONE Info call, so machine mode carries it as a single
// diagnostic (contract §19.1).
func (a *App) checkDryRunPlan(ctx *Context, listed []string) {
	noun := "checks"
	if len(listed) == 1 {
		noun = "check"
	}
	lines := []string{fmt.Sprintf("Would run %d %s:", len(listed), noun)}
	for i, name := range listed {
		def := a.checkDefs[name]
		purity := "impure"
		if checkIsPure(def) {
			purity = "pure"
		}
		if len(def.dependsOn) > 0 {
			lines = append(lines, fmt.Sprintf("  %d. %s (depends on: %s) [%s]", i+1, name, strings.Join(def.dependsOn, ", "), purity))
		} else {
			lines = append(lines, fmt.Sprintf("  %d. %s [%s]", i+1, name, purity))
		}
	}
	ctx.Info(strings.Join(lines, "\n"))
}

// checkRun executes checks and formats output. The framework *Context (which
// carries the invocation's infra snapshot, including declared connection envs
// and the hermetic flag) is no longer discarded: the tool-supplied CheckContext
// is wrapped so check functions can read declared connection envs via the
// ConnectionEnvReader capability (hermetic-suppressed).
// Under dryRun it runs the purity partition instead: the checks declared pure
// (and free of network) execute, and the impure remainder is printed as the
// would-run plan after the results.
func (a *App) checkRun(frameworkCtx *Context, runAll bool, tagExpr, nameGlob string, verbose, dryRun, failingOnly bool) int {
	if a.checkContextFactory == nil {
		frameworkCtx.Error("no check context factory set (call SetCheckContext before running checks)")
		return 1
	}

	base, err := a.checkContextFactory(frameworkCtx)
	if err != nil {
		frameworkCtx.Error(err.Error())
		return 1
	}
	ctx := a.wrapCheckContext(base, frameworkCtx)
	results, impureListed, exitCode, err := a.runSelectedChecks(ctx, RunChecksOptions{
		TagExpr:  tagExpr,
		NameGlob: nameGlob,
		RunAll:   runAll,
		PureOnly: dryRun,
	}, frameworkCtx.effects.log)
	if err != nil {
		frameworkCtx.Error(err.Error())
		return 1
	}

	// Nothing executed AND nothing listed means the filters selected nothing --
	// under the purity partition an empty result set alone does not.
	if len(results) == 0 && len(impureListed) == 0 {
		frameworkCtx.Info("No checks matched the given filters.")
		return 0
	}

	if failingOnly {
		// Only the error-level failures, after the resolver: a check resolved
		// to warn reported its failures as warnings, so it is not among them.
		failures := make([]CheckRunResult, 0, len(results))
		for _, r := range results {
			if r.Gated() {
				failures = append(failures, r)
			}
		}
		results = failures
		exitCode = 0
		if len(failures) > 0 {
			exitCode = 1
		}
	}

	frameworkCtx.Payload(checkResultItems(results))
	if out := FormatCheckResults(results, verbose); out != "" {
		frameworkCtx.Info(out)
	}
	if dryRun {
		a.checkDryRunPlan(frameworkCtx, impureListed)
	}

	return exitCode
}
