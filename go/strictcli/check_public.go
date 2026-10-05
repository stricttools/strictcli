package strictcli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RunChecksOptions configures which checks to run.
type RunChecksOptions struct {
	TagExpr  string
	NameGlob string
	RunAll   bool
	// PureOnly enables the purity partition: only checks that are declared pure
	// AND do not need network access are executed; every other selected check is
	// returned in the impureListed name list (see RunChecks) without being run
	// and without contributing to the exit code. Off by default -- the zero
	// value preserves today's behavior byte-for-byte.
	PureOnly bool
}

// RunChecks executes checks programmatically and returns the executed results,
// the ordered names of checks left unexecuted by the purity partition, the exit
// code, and any error. The exit code follows the same rules as the check
// command: 0 when every executed check passes or skips, 1 for any
// failure/warn/cascade-skip; the error-level failures alone are the results
// whose Gated() is true. The check value resolver (SetCheckValueResolver)
// applies exactly as it does in the check command: a check it turns off is
// returned with the status "off", and a value it may not return is an error.
// impureListed is empty unless opts.PureOnly is set; listed checks contribute
// nothing to the exit code (a consumer renders them as e.g.
// "would run: <name> (impure)").
func (a *App) RunChecks(ctx CheckContext, opts RunChecksOptions) ([]CheckRunResult, []string, int, error) {
	return a.runSelectedChecks(ctx, opts, nil)
}

// runSelectedChecks is RunChecks for a run that belongs to a dispatch, whose
// effect log (cacheWrites) receives the CACHE_WRITEs of framework-owned checks.
func (a *App) runSelectedChecks(ctx CheckContext, opts RunChecksOptions, cacheWrites *effectLog) ([]CheckRunResult, []string, int, error) {
	if !a.checksEnabled {
		return nil, nil, 0, errChecksNotEnabled()
	}

	// Materialize provider-sourced checks before any registry read.
	a.materializeCheckProviders()

	if errMsg := a.validateCheckRegistrations(); errMsg != "" {
		return nil, nil, 0, fmt.Errorf("%s", errMsg)
	}

	selected, err := filterChecks(a.checkDefs, opts.TagExpr, opts.NameGlob, opts.RunAll)
	if err != nil {
		return nil, nil, 0, err
	}

	if len(selected) == 0 {
		return []CheckRunResult{}, nil, 0, nil
	}

	order, err := resolveCheckOrder(a.checkDefs, selected)
	if err != nil {
		return nil, nil, 0, err
	}

	values, err := a.resolveCheckValues(order)
	if err != nil {
		return nil, nil, 0, err
	}

	results, impureListed, exitCode := runChecks(a.checkDefs, order, ctx, values, opts.PureOnly, cacheWrites)
	return results, impureListed, exitCode, nil
}

// FormatCheckResults formats check results as a human-readable string.
// Layout: the derived status label, name, and message, with minted problems
// listed under the check row grouped by severity (error problems first, then
// warn problems), each tagged with its severity. Problems are shown for
// fail/warn/skip outcomes or when verbose is true. No trailing newline --
// callers use fmt.Println().
func FormatCheckResults(results []CheckRunResult, verbose bool) string {
	if len(results) == 0 {
		return ""
	}

	statusLabel := map[string]string{
		"pass": "PASS",
		"fail": "FAIL",
		"warn": "WARN",
		"skip": "SKIP",
		"off":  "OFF",
	}

	// Compute dynamic name column width
	nameWidth := 0
	for _, r := range results {
		if len(r.Name) > nameWidth {
			nameWidth = len(r.Name)
		}
	}

	var b strings.Builder
	var passed, failed, warned, skipped, off int
	for i, r := range results {
		status := r.Status()
		switch status {
		case "pass":
			passed++
		case "fail":
			failed++
		case "warn":
			warned++
		case "skip":
			skipped++
		case "off":
			off++
		}
		label := statusLabel[status]
		if label == "" {
			label = strings.ToUpper(status)
		}
		fmt.Fprintf(&b, "%-4s  %-*s    %s", label, nameWidth, r.Name, r.Outcome.message)
		// Under --verbose, append the per-check duration in a stable, pattern-
		// matchable shape: "(<n>ms)".
		if verbose {
			fmt.Fprintf(&b, " (%dms)", r.DurationMs)
		}

		showProblems := status == "fail" || status == "warn" || status == "skip" || verbose
		if showProblems {
			for _, p := range r.Outcome.orderedProblems() {
				fmt.Fprintf(&b, "\n        [%s] %s", p.severity, p.text)
			}
		}
		// Notes are verdict-inert and surface ONLY under --verbose, on every
		// outcome including a pass.
		if verbose {
			for _, n := range r.Outcome.notes {
				fmt.Fprintf(&b, "\n        [note] %s", n)
			}
		}

		if i < len(results)-1 {
			b.WriteByte('\n')
		}
	}
	// Under --verbose, append a trailing blank line and a count summary.
	if verbose {
		fmt.Fprintf(&b, "\n\n%d passed / %d failed / %d warned / %d skipped",
			passed, failed, warned, skipped)
		// A check turned off by its resolved value is counted only when there
		// is one, so a run without the resolver reads as it always has.
		if off > 0 {
			fmt.Fprintf(&b, " / %d off", off)
		}
	}
	return b.String()
}

// FormatCheckResultsJSON formats check results as a JSON array string. Each
// entry carries the derived status plus the minted problems (each with its
// severity and text). Empty problems serialize as [] rather than null. No
// trailing newline.
func FormatCheckResultsJSON(results []CheckRunResult) string {
	data, err := json.Marshal(checkResultItems(results))
	if err != nil {
		return fmt.Sprintf("error: %s", err)
	}
	return string(data)
}

type jsonProblem struct {
	Severity string `json:"severity"`
	Text     string `json:"text"`
}

type jsonResult struct {
	Name       string        `json:"name"`
	Status     string        `json:"status"`
	Message    string        `json:"message"`
	Problems   []jsonProblem `json:"problems"`
	Notes      []string      `json:"notes"`
	DurationMs int64         `json:"duration_ms"`
}

// checkResultItems is the check command's run payload (contract §19.4): the
// same records FormatCheckResultsJSON serializes, handed to the framework as
// data instead of as a string.
func checkResultItems(results []CheckRunResult) []jsonResult {
	entries := make([]jsonResult, len(results))
	for i, r := range results {
		problems := make([]jsonProblem, 0, len(r.Outcome.problems))
		for _, p := range r.Outcome.problems {
			problems = append(problems, jsonProblem{Severity: p.severity, Text: p.text})
		}
		notes := make([]string, 0, len(r.Outcome.notes))
		notes = append(notes, r.Outcome.notes...)
		entries[i] = jsonResult{
			Name:       r.Name,
			Status:     r.Status(),
			Message:    r.Outcome.message,
			Problems:   problems,
			Notes:      notes,
			DurationMs: r.DurationMs,
		}
	}
	return entries
}
