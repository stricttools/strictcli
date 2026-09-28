package strictcli

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// CheckRunResult holds the outcome of running a single check. The verdict is
// derived from the minted CheckOutcome -- the runner's exit/cascade logic and
// the formatters all consume the same derived accessors (one source of truth).
type CheckRunResult struct {
	Name    string
	Outcome CheckOutcome
	// DurationMs is the wall-clock time in integer milliseconds spent inside the
	// check impl. It is captured around the impl call only; checks that never
	// execute (cascade-skipped) carry 0. Purely informational -- it never
	// affects status or exit codes.
	DurationMs int64
}

// Status returns the derived label ("pass", "fail", "warn", "skip", "off")
// used for display and JSON output.
func (r CheckRunResult) Status() string {
	return deriveStatus(r.Outcome)
}

// Gated reports whether the outcome carries an error-severity problem (derived
// FAIL). Cascade (skipping dependents) and the FAIL exit key on this predicate.
func (r CheckRunResult) Gated() bool {
	return r.Status() == "fail"
}

// Warned reports whether the outcome carries only warn-severity problems
// (derived WARN).
func (r CheckRunResult) Warned() bool {
	return r.Status() == "warn"
}

// resolveCheckOrder builds a DAG from depends_on, expands the selected set to include
// transitive dependencies (pull-in), detects cycles, and returns a topological order.
// Uses Kahn's algorithm.
func resolveCheckOrder(checkDefs map[string]*checkDef, selected map[string]bool) ([]string, error) {
	// Expand selected to include transitive dependencies
	expanded := make(map[string]bool)
	var expand func(name string) error
	visited := make(map[string]bool) // for cycle detection during expansion
	expand = func(name string) error {
		if expanded[name] {
			return nil
		}
		if visited[name] {
			// Build cycle path for error message
			return errCheckDependencyCycleInvolving(name)
		}
		visited[name] = true
		def := checkDefs[name]
		for _, dep := range def.dependsOn {
			if err := expand(dep); err != nil {
				return err
			}
		}
		visited[name] = false
		expanded[name] = true
		return nil
	}

	// Expand all selected checks
	sortedSelected := sortedKeys(selected)
	for _, name := range sortedSelected {
		if err := expand(name); err != nil {
			return nil, err
		}
	}

	// Build the subgraph of only expanded nodes
	// Compute in-degrees and adjacency (within expanded set only)
	inDegree := make(map[string]int)
	dependents := make(map[string][]string) // dep -> list of checks that depend on it
	for name := range expanded {
		inDegree[name] = 0
	}
	for name := range expanded {
		def := checkDefs[name]
		for _, dep := range def.dependsOn {
			if expanded[dep] {
				inDegree[name]++
				dependents[dep] = append(dependents[dep], name)
			}
		}
	}

	// Kahn's algorithm with sorted queue for deterministic output
	var queue []string
	for name := range expanded {
		if inDegree[name] == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue)

	var order []string
	for len(queue) > 0 {
		// Pop first (sorted order ensures determinism)
		node := queue[0]
		queue = queue[1:]
		order = append(order, node)

		// Sort dependents for deterministic ordering
		deps := dependents[node]
		sort.Strings(deps)
		for _, dep := range deps {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
				// Re-sort queue to maintain sorted order
				sort.Strings(queue)
			}
		}
	}

	// If not all expanded nodes are in order, there's a cycle
	if len(order) != len(expanded) {
		// Find the cycle for a helpful error message
		cyclePath := findCycle(checkDefs, expanded, inDegree)
		if cyclePath != "" {
			return nil, errCheckDependencyCycle(cyclePath)
		}
		return nil, errCheckDependencyCycleDetected()
	}

	return order, nil
}

// findCycle finds and formats a cycle path among nodes with non-zero in-degree.
func findCycle(checkDefs map[string]*checkDef, expanded map[string]bool, inDegree map[string]int) string {
	// Find nodes still with non-zero in-degree (part of a cycle)
	remaining := make(map[string]bool)
	for name, deg := range inDegree {
		if deg > 0 {
			remaining[name] = true
		}
	}
	if len(remaining) == 0 {
		return ""
	}

	// Pick the lexicographically smallest starting node for determinism
	var start string
	for name := range remaining {
		if start == "" || name < start {
			start = name
		}
	}

	// Follow dependencies to trace the cycle
	visited := make(map[string]bool)
	var path []string
	current := start
	for {
		if visited[current] {
			// Found cycle start; trim path to the cycle
			for i, name := range path {
				if name == current {
					cycle := append(path[i:], current)
					return strings.Join(cycle, " -> ")
				}
			}
			break
		}
		visited[current] = true
		path = append(path, current)

		// Follow first dependency that's in remaining set
		def := checkDefs[current]
		var next string
		sortedDeps := make([]string, len(def.dependsOn))
		copy(sortedDeps, def.dependsOn)
		sort.Strings(sortedDeps)
		for _, dep := range sortedDeps {
			if remaining[dep] {
				next = dep
				break
			}
		}
		if next == "" {
			break
		}
		current = next
	}
	return ""
}

// checkIsPure reports whether a check is executable under the purity partition:
// it must be declared pure AND not require network access. Everything else is
// "impure" for partition purposes.
func checkIsPure(def *checkDef) bool {
	return def.pure && !def.needsNetwork
}

// checkAbortText builds the attribution line for a check whose impl aborted.
// It names the check, the panic value's type (so a framework bug stays
// identifiable) and the value's own message. An empty message drops the colon
// rather than emitting a dangling one.
func checkAbortText(name, typeName, message string) string {
	if message == "" {
		return fmt.Sprintf("check %q aborted with %s", name, typeName)
	}
	return fmt.Sprintf("check %q aborted with %s: %s", name, typeName, message)
}

// panicTypeName renders a recovered value's type WITHOUT its package
// qualifier or pointer star, so the three implementations name the same type
// the same way (Python's type(exc).__name__ and TypeScript's constructor name
// carry no package either).
func panicTypeName(r interface{}) string {
	name := strings.TrimPrefix(fmt.Sprintf("%T", r), "*")
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// panicMessage renders a recovered value's own message: an error's Error(), a
// panicked string as-is, anything else through %v.
func panicMessage(r interface{}) string {
	switch v := r.(type) {
	case error:
		return v.Error()
	case string:
		return v
	default:
		return fmt.Sprintf("%v", r)
	}
}

// mintCheckAbort mints the outcome reported for a check whose impl panicked:
// a found outcome carrying a single error-severity problem, so it derives
// FAIL, fails the run, and cascade-skips its dependents exactly like any other
// failing check. Every rendering surface (the result row, the problem line,
// both JSON fields) carries the full attribution.
func mintCheckAbort(name string, r interface{}) CheckOutcome {
	text := checkAbortText(name, panicTypeName(r), panicMessage(r))
	return CheckOutcome{
		minted:   true,
		kind:     "found",
		message:  text,
		problems: []checkProblem{{text: text, severity: "error"}},
	}
}

// runCheckImpl invokes one check impl, containing a panic as that check's own
// failure. One broken check must not abort the whole run: every other selected
// check still executes, and the run still fails because this check failed.
func runCheckImpl(name string, impl func(CheckContext) CheckOutcome, ctx CheckContext) (o CheckOutcome, aborted bool) {
	defer func() {
		if r := recover(); r != nil {
			o = mintCheckAbort(name, r)
			aborted = true
		}
	}()
	return impl(ctx), false
}

// runChecks executes checks in order, skipping dependents of failed checks.
// Returns results, the ordered names of checks that were NOT executed because of
// the purity partition (empty unless pureOnly is set), and an exit code (0 when
// every executed check passes or skips, 1 otherwise -- a warning counts).
//
// values maps a check name to its resolved value (see resolveCheckValues); a
// check absent from it runs as registered. A check resolved "off" does not run:
// it gets an OFF row, cascades nothing, and leaves the exit code alone. A check
// resolved "warn" runs and has its failures reported as warnings, so it never
// blocks its dependents.
//
// Purity partition (pureOnly): when set, only checks that are pure and do not
// need network EXECUTE; every other selected check is listed (its name appended
// to impureListed) but NOT run and contributes NOTHING to the exit code. A check
// also joins the listing if any of its dependencies was listed -- an unexecuted
// dependency means the check's precondition cannot be verified, so it cannot run
// either. The failed-dependency cascade takes precedence over the listing: a
// genuinely failed (executed) pure dependency still cascade-skips its dependents
// as usual.
func runChecks(checkDefs map[string]*checkDef, order []string, ctx CheckContext, values map[string]resolvedCheckValue, pureOnly bool) ([]CheckRunResult, []string, int) {
	results := make([]CheckRunResult, 0, len(order))
	// Track checks whose dependents should be cascade-skipped. Cascade keys
	// ONLY on a derived FAIL (Gated: an error-severity problem present) or a
	// cascade-skip. A WARN outcome satisfies the dependency (dependents still
	// run) and only affects the exit code -- warn-severity checks physically
	// cannot cascade because WarnReporter lacks error-minting. An explicit
	// SKIP from an impl is NOT a failure -- dependents still run.
	failedChecks := make(map[string]bool)
	// Track checks that were listed (not executed) under the purity partition,
	// so that dependents whose precondition cannot be verified join the listing.
	listedChecks := make(map[string]bool)
	var impureListed []string

	exitCode := 0
	for _, name := range order {
		def := checkDefs[name]
		value := def.severity
		if v, ok := values[name]; ok {
			value = v.value
			// A check its resolved value turned off never runs, whatever its
			// dependencies did: it is shown as off, with where that came from.
			if value == "off" {
				results = append(results, CheckRunResult{Name: name, Outcome: mintCheckOff(v.source)})
				continue
			}
		}

		// Check if any dependency failed -- skip if so
		skipReason := ""
		for _, dep := range def.dependsOn {
			if failedChecks[dep] {
				skipReason = fmt.Sprintf("skipped: dependency %q failed", dep)
				break
			}
		}

		if skipReason != "" {
			// Internally minted skip outcome (in-package construction is the
			// runner's own mint; user impls can only mint via reporters).
			o := CheckOutcome{minted: true, kind: "skipped", message: skipReason}
			results = append(results, CheckRunResult{Name: name, Outcome: o})
			failedChecks[name] = true
			exitCode = 1
			continue
		}

		// Purity partition: list (do not execute) impure checks and any check
		// that depends on a listed one. Listed checks contribute no exit code.
		if pureOnly {
			listed := !checkIsPure(def)
			if !listed {
				for _, dep := range def.dependsOn {
					if listedChecks[dep] {
						listed = true
						break
					}
				}
			}
			if listed {
				listedChecks[name] = true
				impureListed = append(impureListed, name)
				continue
			}
		}

		// Run the check, capturing wall-clock duration around the impl call only.
		start := time.Now()
		o, aborted := runCheckImpl(name, def.impl, ctx)
		durationMs := time.Since(start).Milliseconds()
		// Belt-and-braces: an impl must return a reporter-minted outcome. A
		// contained abort mints its own, so the assertion only reaches values
		// the impl really returned.
		if !aborted && !o.minted {
			panic(errCheckOutcomeNotMinted(name))
		}
		// A check resolved to warn reports its failures as warnings. A broken
		// check fails whatever its value: resolving it to warn lowers what its
		// findings mean, not what an abort means.
		if value == "warn" && !aborted {
			o = mintAsWarnings(o)
		}
		r := CheckRunResult{Name: name, Outcome: o, DurationMs: durationMs}
		results = append(results, r)

		switch {
		case r.Gated():
			failedChecks[name] = true
			exitCode = 1
		case r.Warned():
			// Warn satisfies the dependency (no cascade), but still makes
			// the run exit non-zero.
			exitCode = 1
			// pass / skip: not a failure, no cascade, no exit code change.
		}
	}

	return results, impureListed, exitCode
}

// filterChecks selects checks based on tag expression, name glob, or runAll flag.
// If both tagExpr and nameGlob are provided, the result is their intersection.
func filterChecks(checkDefs map[string]*checkDef, tagExpr string, nameGlob string, runAll bool) (map[string]bool, error) {
	if runAll {
		result := make(map[string]bool, len(checkDefs))
		for name := range checkDefs {
			result[name] = true
		}
		return result, nil
	}

	var tagMatches map[string]bool
	if tagExpr != "" {
		tagMatches = make(map[string]bool)
		for name, def := range checkDefs {
			tagSet := make(map[string]bool, len(def.tags))
			for _, t := range def.tags {
				tagSet[t] = true
			}
			match, err := matchTagExpr(tagExpr, tagSet)
			if err != nil {
				return nil, err
			}
			if match {
				tagMatches[name] = true
			}
		}
	}

	var globMatches map[string]bool
	if nameGlob != "" {
		globMatches = make(map[string]bool)
		for name := range checkDefs {
			matched, err := path.Match(nameGlob, name)
			if err != nil {
				return nil, errInvalidGlobPattern(nameGlob, err)
			}
			if matched {
				globMatches[name] = true
			}
		}
	}

	// Determine result based on which filters are active
	if tagMatches != nil && globMatches != nil {
		// Intersection
		result := make(map[string]bool)
		for name := range tagMatches {
			if globMatches[name] {
				result[name] = true
			}
		}
		return result, nil
	}
	if tagMatches != nil {
		return tagMatches, nil
	}
	if globMatches != nil {
		return globMatches, nil
	}

	// Neither filter provided
	return make(map[string]bool), nil
}

// sortedKeys returns the keys of a map[string]bool sorted alphabetically.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
