package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	tomledit "github.com/smm-h/go-toml-edit"
	"github.com/stricttools/strictcli/go/strictcli"
)

// Suppress unused-import errors when templates have no substitutions.
var _ = strings.ReplaceAll
var _ = fmt.Println
var _ = sort.Strings

// goPanicExitStatus is the status the Go runtime gives a process whose panic
// was never recovered.
const goPanicExitStatus = 2

// dispatching is set immediately before app.Run(). It is what lets the recover
// below tell the harness's two panic sources apart, which they are not
// interchangeable:
//
//   - BEFORE it, a panic is a REGISTRATION error -- Go's idiomatic spelling of
//     the ValueError ref_python raises and the throw the TS harness makes. All
//     three harnesses report it as "error: <message>" and exit 1, and ~118
//     cases pin that. Nothing in the framework has claimed an exit status at
//     that point: a registration error precedes machine mode and emits no
//     envelope.
//   - AFTER it, a panic is a handler ABORT, and the framework has by then
//     written an envelope whose exit_code says what status this process will
//     leave with (effects contract §19.2 read through §3.5: the panic is not
//     handled, so the status is the language's own). Substituting 1 there
//     would make the envelope contradict its own process, so the harness
//     reproduces Go's 2.
//
// Either way the recover normalizes only the crash REPORT: the sibling
// harnesses print the message as "error: <message>", and dumping Go's
// goroutine trace instead would make every aborting case's stderr incomparable
// for a reason that has nothing to do with the framework.
var dispatching bool

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", r)
			if dispatching {
				os.Exit(goPanicExitStatus)
			}
			os.Exit(1)
		}
	}()

	defPath := os.Getenv("CONFORMANCE_APP_DEF")
	if defPath == "" {
		fmt.Fprintln(os.Stderr, "CONFORMANCE_APP_DEF environment variable not set")
		os.Exit(2)
	}

	data, err := os.ReadFile(defPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read app def: %v\n", err)
		os.Exit(2)
	}

	var appDef map[string]interface{}
	if err := json.Unmarshal(data, &appDef); err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse app def: %v\n", err)
		os.Exit(2)
	}

	// Build app options.
	var appOpts []strictcli.AppOption
	if v, ok := appDef["env_prefix"]; ok {
		appOpts = append(appOpts, strictcli.WithEnvPrefix(v.(string)))
	}
	if v, ok := appDef["config"]; ok && v.(bool) {
		appOpts = append(appOpts, strictcli.WithConfig())
	}
	if v, ok := appDef["config_path"]; ok && v != nil {
		appOpts = append(appOpts, strictcli.WithConfigPath(v.(string)))
	}
	// The config path declared as a marker relative to an infra root: the same
	// declaration the flag side spells with default_relative_to_root, resolved
	// eagerly at construction.
	if v, ok := appDef["config_path_relative_to_root"]; ok && v != nil {
		m := v.(map[string]interface{})
		var parts []string
		if ps, ok := m["parts"]; ok {
			for _, p := range ps.([]interface{}) {
				parts = append(parts, p.(string))
			}
		}
		appOpts = append(appOpts, strictcli.WithConfigPathRelativeToRoot(
			m["env_var"].(string), parts...))
	}
	if v, ok := appDef["schema_path"]; ok && v != nil {
		appOpts = append(appOpts, strictcli.WithSchemaPath(v.(string)))
	}
	if v, ok := appDef["config_format"]; ok && v.(string) != "json" {
		appOpts = append(appOpts, strictcli.WithConfigFormat(v.(string)))
	}
	if v, ok := appDef["config_conflict_mode"]; ok && v.(string) != "cli-wins" {
		appOpts = append(appOpts, strictcli.WithConfigConflictMode(v.(string)))
	}
	if v, ok := appDef["infra_root"]; ok {
		for envVar, def := range v.(map[string]interface{}) {
			appOpts = append(appOpts, strictcli.WithInfraRoot(envVar, def.(string)))
		}
	}
	if v, ok := appDef["handshake_env"]; ok {
		for envVar, hlp := range v.(map[string]interface{}) {
			appOpts = append(appOpts, strictcli.WithHandshakeEnv(envVar, hlp.(string)))
		}
	}
	if v, ok := appDef["checks_toml"]; ok {
		appOpts = append(appOpts, strictcli.WithChecksEmbed([]byte(v.(string))))
	}
	if v, ok := appDef["test_coverage_dir"]; ok {
		appOpts = append(appOpts, strictcli.WithTestCoverageDir(v.(string)))
	}
	if v, ok := appDef["proc_observe_allowlist"]; ok {
		var prefixes [][]string
		for _, p := range v.([]interface{}) {
			var prefix []string
			for _, e := range p.([]interface{}) {
				prefix = append(prefix, e.(string))
			}
			prefixes = append(prefixes, prefix)
		}
		appOpts = append(appOpts, strictcli.WithProcObserveAllowlist(prefixes))
	}

	app := strictcli.NewApp(
		appDef["name"].(string),
		appDef["version"].(string),
		appDef["help"].(string),
		appOpts...,
	)

	// Register config fields (before commands, since commands may bind to them).
	if cfs, ok := appDef["config_fields_def"]; ok {
		for _, item := range cfs.([]interface{}) {
			cfDef := item.(map[string]interface{})
			cfName := cfDef["name"].(string)
			cfHelp := cfDef["help"].(string)
			var cfOpts []strictcli.ConfigFieldOption
			cfType := "str"
			if t, ok := cfDef["type"]; ok {
				cfType = t.(string)
			}
			switch cfType {
			case "bool":
				cfOpts = append(cfOpts, strictcli.ConfigFieldType(strictcli.TypeBool))
			case "int":
				cfOpts = append(cfOpts, strictcli.ConfigFieldType(strictcli.TypeInt))
			case "float":
				cfOpts = append(cfOpts, strictcli.ConfigFieldType(strictcli.TypeFloat))
			default:
				cfOpts = append(cfOpts, strictcli.ConfigFieldType(strictcli.TypeStr))
			}
			cfOpts = append(cfOpts, strictcli.ConfigFieldHelp(cfHelp))
			if v, ok := cfDef["default"]; ok {
				switch cfType {
				case "bool":
					cfOpts = append(cfOpts, strictcli.ConfigFieldDefault(v.(bool)))
				case "int":
					cfOpts = append(cfOpts, strictcli.ConfigFieldDefault(int(v.(float64))))
				case "float":
					cfOpts = append(cfOpts, strictcli.ConfigFieldDefault(v.(float64)))
				default:
					cfOpts = append(cfOpts, strictcli.ConfigFieldDefault(v.(string)))
				}
			}
			app.ConfigField(cfName, cfOpts...)
		}
	}

	// Register global flags.
	var globalFlags []map[string]interface{}
	if gf, ok := appDef["global_flags"]; ok {
		for _, item := range gf.([]interface{}) {
			fd := item.(map[string]interface{})
			globalFlags = append(globalFlags, fd)
			app.GlobalFlag(buildFlag(fd))
		}
	}

	// Register groups (recursive).
	if groups, ok := appDef["groups"]; ok {
		for _, g := range groups.([]interface{}) {
			buildGroup(g.(map[string]interface{}), app, globalFlags)
		}
	}

	// Register top-level commands.
	if cmds, ok := appDef["commands"]; ok {
		for _, c := range cmds.([]interface{}) {
			registerCommand(c.(map[string]interface{}), appTarget{app}, globalFlags, app)
		}
	}

	// Register tag contracts.
	if tc, ok := appDef["tag_contracts"]; ok {
		for tag, contract := range tc.(map[string]interface{}) {
			cd := contract.(map[string]interface{})
			app.TagContract(tag, cd["requires_flag"].(string))
		}
	}

	// Register checks. The registration FORM (error vs warn) is derived from the
	// check's declared severity in the embedded checks_toml -- there is no
	// per-check registration field. The case only specifies what the impl mints
	// (mint + message + problems); the reporter is minted here per severity.
	if _, ok := appDef["checks_toml"]; ok {
		if checks, ok := appDef["checks"]; ok {
			severities := checkSeverities(appDef["checks_toml"].(string))
			for _, c := range checks.([]interface{}) {
				cd := c.(map[string]interface{})
				cname := cd["name"].(string)
				// An aborting impl mints nothing: it panics instead, which is
				// how a case reaches the runner's per-check containment.
				if aborts, ok := cd["aborts"].(bool); ok && aborts {
					if severities[cname] == "warn" {
						app.RegisterWarnCheck(cname, func(ctx strictcli.CheckContext, r *strictcli.WarnReporter) strictcli.CheckOutcome {
							panic(CheckAborted{msg: checkAbortMessage})
						})
					} else {
						app.RegisterErrorCheck(cname, func(ctx strictcli.CheckContext, r *strictcli.ErrorReporter) strictcli.CheckOutcome {
							panic(CheckAborted{msg: checkAbortMessage})
						})
					}
					continue
				}
				// Capture for closure.
				m, msg, probs, notes := cd["mint"].(string), cd["message"].(string), parseProblems(cd), parseNotes(cd)
				if severities[cname] == "warn" {
					app.RegisterWarnCheck(cname, func(ctx strictcli.CheckContext, r *strictcli.WarnReporter) strictcli.CheckOutcome {
						return mintWarnOutcome(r, m, msg, probs, notes)
					})
				} else {
					app.RegisterErrorCheck(cname, func(ctx strictcli.CheckContext, r *strictcli.ErrorReporter) strictcli.CheckOutcome {
						return mintErrorOutcome(r, m, msg, probs, notes)
					})
				}
			}
		}
	}

	// Register check providers. Each provider is a list of specs it returns;
	// every spec carries its 8 meta fields inline (providers have no TOML). The
	// registration form (NewErrorCheckSpec vs NewWarnCheckSpec) is the spec's
	// impl_form (defaults to its meta severity); a spec whose impl_form differs
	// from its severity pins the materialization-time severity-mismatch panic.
	if providers, ok := appDef["providers"]; ok {
		for _, prov := range providers.([]interface{}) {
			specDefs := prov.([]interface{})
			app.RegisterCheckProvider(func() []strictcli.CheckSpec {
				var specs []strictcli.CheckSpec
				for _, s := range specDefs {
					sd := s.(map[string]interface{})
					meta := providerSpecMeta(sd)
					m, msg, probs, notes := sd["mint"].(string), sd["message"].(string), parseProblems(sd), parseNotes(sd)
					implForm := meta.Severity
					if v, ok := sd["impl_form"]; ok {
						implForm = v.(string)
					}
					if implForm == "warn" {
						specs = append(specs, strictcli.NewWarnCheckSpec(meta,
							func(ctx strictcli.CheckContext, r *strictcli.WarnReporter) strictcli.CheckOutcome {
								return mintWarnOutcome(r, m, msg, probs, notes)
							}))
					} else {
						specs = append(specs, strictcli.NewErrorCheckSpec(meta,
							func(ctx strictcli.CheckContext, r *strictcli.ErrorReporter) strictcli.CheckOutcome {
								return mintErrorOutcome(r, m, msg, probs, notes)
							}))
					}
				}
				return specs
			})
		}
	}

	_, hasToml := appDef["checks_toml"]
	_, hasProviders := appDef["providers"]
	_, hasTestCoverage := appDef["test_coverage_dir"]
	if hasToml || hasProviders || hasTestCoverage {
		app.SetCheckContext(func() strictcli.CheckContext {
			return &testCheckCtx{}
		})
	}

	// Write config_content_late AFTER construction but BEFORE run
	if v, ok := appDef["config_content_late"]; ok {
		configPath := ""
		if cp, ok := appDef["config_path"]; ok && cp != nil {
			configPath = cp.(string)
		}
		if configPath != "" {
			os.WriteFile(configPath, []byte(v.(string)), 0o644)
		}
	}

	// Pre-test argv lists: run app.Test() for each before the main app.Run().
	// Used by test_coverage_dir conformance cases to generate shard files before
	// the check command runs.
	if v, ok := appDef["pre_test"]; ok {
		for _, item := range v.([]interface{}) {
			var argv []string
			for _, arg := range item.([]interface{}) {
				argv = append(argv, arg.(string))
			}
			app.Test(argv)
		}
	}

	// Tool descriptor dump: the exported classification, one line per tool.
	if v, ok := appDef["dump_tools"]; ok && v == true {
		for _, tl := range app.AsTools() {
			fmt.Printf("tool: %s effect=%s consequential=%t\n",
				tl.Name, tl.Effect, tl.Consequential)
		}
	}

	// Programmatic calls: the App.Call() channel, which argv cannot reach.
	if v, ok := appDef["pre_call"]; ok {
		for _, item := range v.([]interface{}) {
			spec := item.(map[string]interface{})
			cmdPath := spec["command"].(string)
			kwargs := map[string]interface{}{}
			if raw, ok := spec["kwargs"]; ok {
				kwargs = raw.(map[string]interface{})
			}
			kwargs = recordKwargs(kwargs)
			var callOpts []strictcli.CallOption
			if raw, ok := spec["approve_consequential"]; ok && raw == true {
				callOpts = append(callOpts, strictcli.WithApproveConsequential())
			}
			if _, err := app.Call(cmdPath, kwargs, callOpts...); err != nil {
				fmt.Fprintf(os.Stderr, "call error: %s\n", err.Error())
			} else {
				fmt.Printf("call ok: %s\n", cmdPath)
			}
		}
	}

	// The confirm protocol's interactive branch is otherwise unreachable from a
	// subprocess: a case's stdin is a pipe, and a pipe is not a TTY in any of
	// the three implementations, so every consequential case would take the
	// non-interactive error branch. The framework's test-only confirm seam
	// says the answer channel IS interactive and leaves the answer itself
	// coming from the case's real stdin -- WHERE the answer comes from, never
	// WHETHER the protocol runs.
	if v, ok := appDef["confirm_stdin_interactive"]; ok && v.(bool) {
		app.SetConfirmIO(&strictcli.ConfirmIO{
			IsInteractive: func() bool { return true },
			In:            os.Stdin,
		})
	}

	// The structured effect-log side channel (§14.3): the same env-var file
	// handoff as CONFORMANCE_APP_DEF. App.Run ends in os.Exit, so the write
	// rides SetExitHook -- the Go counterpart of the Python ref's atexit and
	// the TS harness's process.on("exit").
	if logPath := os.Getenv("CONFORMANCE_EFFECT_LOG"); logPath != "" {
		app.SetExitHook(func() {
			records := app.EffectLog()
			if records == nil {
				records = []map[string]interface{}{}
			}
			// encoding/json sorts map keys, which is what §14.3 asks for.
			data, err := json.Marshal(records)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to marshal effect log: %v\n", err)
				return
			}
			if err := os.WriteFile(logPath, data, 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "failed to write effect log: %v\n", err)
			}
		})
	}

	dispatching = true
	app.Run()
}

type testCheckCtx struct{}

func (c *testCheckCtx) ProjectRoot() string { return "." }

// recordKwargs converts a pre_call kwargs object into the record front door's
// own Go vocabulary: a value spelled as the flat map §25.6 publishes -- the
// choice's name under the reserved key `choice` followed by the fields that
// scope carries -- becomes strictcli.Elect(<choice>, Fields{...}), recursively,
// so a nested selector inside a record is elected at its own level (§24.11).
// A map naming no declared choice is handed through untouched: that IS the
// state the record door's wrong-choice refusal is asserted against.
func recordKwargs(kwargs map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(kwargs))
	for k, v := range kwargs {
		out[k] = recordValue(k, v)
	}
	return out
}

func recordValue(key string, v interface{}) interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	name, ok := m["choice"].(string)
	if !ok {
		return v
	}
	decl, ok := choiceRegistry[key+"/"+name]
	if !ok {
		decl, ok = choiceRegistry[strings.ReplaceAll(key, "_", "-")+"/"+name]
	}
	if !ok {
		// A name-only fallback, so a case can hand ONE selector a choice its
		// sibling declared -- the input the record door's wrong-choice refusal
		// is asserted against (§18.25 item 251).
		decl, ok = choiceRegistry["*/"+name]
	}
	if !ok {
		return v
	}
	fields := strictcli.Fields{}
	for fk, fv := range m {
		if fk == "choice" {
			continue
		}
		fields[strings.ReplaceAll(fk, "-", "_")] = recordValue(fk, fv)
	}
	return strictcli.Elect(decl, fields)
}

// checkProblemSpec is a single problem the case asks the impl to mint.
type checkProblemSpec struct {
	severity string // "error" or "warn"
	text     string
}

// parseProblems extracts the optional "problems" list from a check/spec def.
func parseProblems(cd map[string]interface{}) []checkProblemSpec {
	var problems []checkProblemSpec
	if p, ok := cd["problems"]; ok {
		for _, item := range p.([]interface{}) {
			pm := item.(map[string]interface{})
			problems = append(problems, checkProblemSpec{
				severity: pm["severity"].(string),
				text:     pm["text"].(string),
			})
		}
	}
	return problems
}

// parseNotes extracts the optional "notes" list from a check/spec def. Notes are
// verdict-inert informational strings replayed onto the reporter via Note().
func parseNotes(cd map[string]interface{}) []string {
	var notes []string
	if n, ok := cd["notes"]; ok {
		for _, item := range n.([]interface{}) {
			notes = append(notes, item.(string))
		}
	}
	return notes
}

// providerSpecMeta builds a CheckSpecMeta from a provider spec def, carrying all
// eight declarative meta fields inline (providers have no TOML).
func providerSpecMeta(sd map[string]interface{}) strictcli.CheckSpecMeta {
	toStrList := func(key string) []string {
		var out []string
		if v, ok := sd[key]; ok && v != nil {
			for _, x := range v.([]interface{}) {
				out = append(out, x.(string))
			}
		}
		return out
	}
	scope := ""
	if v, ok := sd["scope"]; ok && v != nil {
		scope = v.(string)
	}
	return strictcli.CheckSpecMeta{
		Name:         sd["name"].(string),
		Tags:         toStrList("tags"),
		Severity:     sd["severity"].(string),
		Fast:         sd["fast"].(bool),
		Pure:         sd["pure"].(bool),
		NeedsNetwork: sd["needs_network"].(bool),
		DependsOn:    toStrList("depends_on"),
		Scope:        scope,
	}
}

// checkSeverities parses the embedded checks_toml and returns a name->severity
// map. The registration form is derived from this (there is no per-check
// registration field in the case). A parse failure yields an empty map, which
// falls back to error-severity registration -- the strictcli severity
// cross-check would then surface any genuine mismatch as a panic.
func checkSeverities(tomlStr string) map[string]string {
	result := map[string]string{}
	rawPtr, err := tomledit.Unmarshal[map[string]interface{}]([]byte(tomlStr))
	if err != nil {
		return result
	}
	raw := *rawPtr
	checks, ok := raw["checks"].(map[string]interface{})
	if !ok {
		return result
	}
	for name, v := range checks {
		if fields, ok := v.(map[string]interface{}); ok {
			if sev, ok := fields["severity"].(string); ok {
				result[name] = sev
			}
		}
	}
	return result
}

// checkAbortMessage is what an `aborts` check impl panics with. The sibling
// harnesses raise/throw the identical text carried by a type spelled the same,
// so the framework's containment line is byte-identical across targets.
const checkAbortMessage = "conformance: check aborted"

// CheckAborted is the panic value an `aborts` check impl carries. Its NAME is
// part of the contract: Python raises a CheckAborted exception and TypeScript
// throws a CheckAborted error, and the framework prints the unqualified type
// name, so all three containment lines read the same.
type CheckAborted struct{ msg string }

// Error makes CheckAborted an error, which is how the Go framework recovers its
// message (the sibling harnesses' types carry theirs the same way).
func (e CheckAborted) Error() string { return e.msg }

// mintErrorOutcome replays the case's problems onto an ErrorReporter and mints
// the requested terminal outcome.
func mintErrorOutcome(r *strictcli.ErrorReporter, mint, message string, problems []checkProblemSpec, notes []string) strictcli.CheckOutcome {
	for _, n := range notes {
		r.Note(n)
	}
	for _, p := range problems {
		if p.severity == "error" {
			r.Error(p.text)
		} else {
			r.Warn(p.text)
		}
	}
	switch mint {
	case "passed":
		return r.Passed(message)
	case "skipped":
		return r.Skipped(message)
	default:
		return r.Found(message)
	}
}

// mintWarnOutcome replays the case's problems onto a WarnReporter (which can
// only mint warn-severity problems) and mints the requested terminal outcome.
func mintWarnOutcome(r *strictcli.WarnReporter, mint, message string, problems []checkProblemSpec, notes []string) strictcli.CheckOutcome {
	for _, n := range notes {
		r.Note(n)
	}
	for _, p := range problems {
		r.Warn(p.text)
	}
	switch mint {
	case "passed":
		return r.Passed(message)
	case "skipped":
		return r.Skipped(message)
	default:
		return r.Found(message)
	}
}

// target abstracts over App and Group for command registration.
type target interface {
	Command(name, help string, handler func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome, opts ...strictcli.CmdOption)
	Deprecated(name, message string, opts ...strictcli.CmdOption)
}

type appTarget struct{ a *strictcli.App }

func (t appTarget) Command(name, help string, handler func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome, opts ...strictcli.CmdOption) {
	t.a.Command(name, help, handler, opts...)
}
func (t appTarget) Deprecated(name, message string, opts ...strictcli.CmdOption) {
	t.a.Deprecated(name, message, opts...)
}

type groupTarget struct{ g *strictcli.Group }

func (t groupTarget) Command(name, help string, handler func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome, opts ...strictcli.CmdOption) {
	t.g.Command(name, help, handler, opts...)
}
func (t groupTarget) Deprecated(name, message string, opts ...strictcli.CmdOption) {
	t.g.Deprecated(name, message, opts...)
}

// elemTypeOf returns the scalar type inside a compound type name:
// "list[int]" -> "int", "dict[str,float]" -> "float", "str" -> "str".
func elemTypeOf(ftype string) string {
	if strings.HasPrefix(ftype, "list[") {
		return ftype[5 : len(ftype)-1]
	}
	if strings.HasPrefix(ftype, "dict[") {
		return ftype[9 : len(ftype)-1]
	}
	return ftype
}

// convertScalarDefault converts one JSON scalar into the Go type the named
// scalar type declares. A value that does not match its declared type carries
// over unconverted, so the framework mints its own default-type registration
// error rather than the harness panicking first.
func convertScalarDefault(scalarType string, v interface{}) interface{} {
	switch scalarType {
	case "int":
		if f, ok := v.(float64); ok {
			return int(f)
		}
	case "float":
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return v
}

// choiceRecords converts a declaration's record-shaped `choices_<T>` key into
// the ChoiceValue list Go's Choices/ArgChoices take (contract §24.2). A
// `choices=` entry is ALWAYS a record -- a value plus optional help -- and Go
// spells "no help" as the empty string for lack of optional parameters.
//
// The typed split survives on the input side alone: JSON cannot tell an integer
// choice from a float one, and the harness must pick a typed constructor.
func choiceRecords(d map[string]interface{}) ([]strictcli.ChoiceValue, bool) {
	for key, conv := range map[string]func(interface{}) interface{}{
		"choices_str":   func(v interface{}) interface{} { return v.(string) },
		"choices_int":   func(v interface{}) interface{} { return int(v.(float64)) },
		"choices_float": func(v interface{}) interface{} { return v.(float64) },
	} {
		raw, ok := d[key]
		if !ok {
			continue
		}
		var out []strictcli.ChoiceValue
		for _, item := range raw.([]interface{}) {
			rec, isRecord := item.(map[string]interface{})
			if !isRecord {
				// A BARE entry is spellable so the deleted-entry refusal has a
				// covering input (§12.13). Go's bare spelling is a struct
				// literal, which reaches the constructor without Ch's mark and
				// is refused there.
				out = append(out, strictcli.ChoiceValue{Value: conv(item)})
				continue
			}
			help := ""
			if h, ok := rec["help"]; ok {
				help = h.(string)
			}
			out = append(out, strictcli.Ch(conv(rec["value"]), help))
		}
		return out, true
	}
	return nil, false
}

// retiredChoiceRecords converts a declaration's record-shaped
// `retired_choices_<T>` key into the RetiredChoiceValue list Go's
// RetiredChoices/ArgRetiredChoices take. The typed split mirrors
// choiceRecords's, for the same input-side reason.
func retiredChoiceRecords(d map[string]interface{}) ([]strictcli.RetiredChoiceValue, bool) {
	for key, conv := range map[string]func(interface{}) interface{}{
		"retired_choices_str":   func(v interface{}) interface{} { return v.(string) },
		"retired_choices_int":   func(v interface{}) interface{} { return int(v.(float64)) },
		"retired_choices_float": func(v interface{}) interface{} { return v.(float64) },
	} {
		raw, ok := d[key]
		if !ok {
			continue
		}
		var out []strictcli.RetiredChoiceValue
		for _, item := range raw.([]interface{}) {
			rec, isRecord := item.(map[string]interface{})
			if !isRecord {
				// A bare entry is only refusable in Python, whose keyword takes
				// a list of anything; Go's variadic parameter is typed, so the
				// bare spelling reaches here as a value with no message and is
				// answered by the empty-message guard.
				out = append(out, strictcli.RetiredChoiceValue{Value: conv(item)})
				continue
			}
			out = append(out, strictcli.RetiredChoice(
				conv(rec["value"]), rec["message"].(string),
			))
		}
		return out, true
	}
	return nil, false
}

// isSelector reports whether a flag definition declares the scoped-selector
// construct. `elect_by` is the input-side discriminator exactly as it is the
// dump's (§13's item-207 box, §25.6).
func isSelector(fd map[string]interface{}) bool {
	_, ok := fd["elect_by"]
	return ok
}

// buildChoice materializes one choice object onto Go's identity values
// (§24.12): Choice(name, help, scope...) for a token-spelled choice, and
// MemberChoice(memberFlag, help, scope...) for a member-spelled one.
//
// A choice carrying a `value` payload always becomes a MemberChoice, member
// spelling or not -- which is exactly how a case reaches
// errTokenChoiceCarriesPayload on the token-spelled side.
// choiceRegistry holds every choice declaration the app definition built,
// keyed by "<selector name>/<choice name>". The record front door takes the
// declaration itself (strictcli.Elect(<choice>, Fields{...})), so a case that
// spells an elected record as the flat map §25.6 publishes is materialized
// through this registry -- the Go shape of what the Python reference builds as
// a choice instance and TypeScript already receives as a tagged object.
var choiceRegistry = map[string]*strictcli.ChoiceDecl{}

func buildChoice(cd map[string]interface{}, memberSpelled bool) *strictcli.ChoiceDecl {
	name := cd["name"].(string)
	help := cd["help"].(string)

	var scope []strictcli.Flag
	if flags, ok := cd["flags"]; ok {
		for _, f := range flags.([]interface{}) {
			scope = append(scope, buildFlag(f.(map[string]interface{})))
		}
	}

	payload, carriesPayload := cd["value"].(map[string]interface{})
	if !carriesPayload && !memberSpelled {
		return strictcli.Choice(name, help, scope...)
	}

	// The electing flag. A payload-carrying member is a flag of the payload's
	// own type carrying the payload's help (which is what the dump publishes as
	// the `value` entry); a payload-less member is a bool, and electing it is
	// the whole of what it says.
	memberOpts := []strictcli.FlagOption{}
	// The member's short. Go declares it on the electing flag whichever shape
	// the member has, so the two case spellings -- the payload's `short` and
	// the choice's own -- both land here (§24.4, §24.12).
	if s, ok := payload["short"].(string); ok && carriesPayload {
		memberOpts = append(memberOpts, strictcli.Short(s))
	} else if s, ok := cd["short"].(string); ok {
		memberOpts = append(memberOpts, strictcli.Short(s))
	}
	// The member flag's presence is `required`. The case may spell something
	// else, and only so errMemberFlagPresence has a covering input.
	switch presenceOf(cd) {
	case "optional":
		memberOpts = append(memberOpts, strictcli.Optional())
	default:
		memberOpts = append(memberOpts, strictcli.Required())
	}
	var member strictcli.Flag
	if carriesPayload {
		phelp := payload["help"].(string)
		switch payload["type"].(string) {
		case "int":
			member = strictcli.IntFlag(name, phelp, memberOpts...)
		case "float":
			member = strictcli.FloatFlag(name, phelp, memberOpts...)
		default:
			member = strictcli.StringFlag(name, phelp, memberOpts...)
		}
	} else {
		member = strictcli.BoolFlag(name, help, memberOpts...)
	}
	return strictcli.MemberChoice(member, help, scope...)
}

// presenceOf reads a choice object's optional member-presence key.
func presenceOf(cd map[string]interface{}) string {
	if v, ok := cd["presence"]; ok {
		return v.(string)
	}
	return "required"
}

// buildSelectorFlag constructs a selector from a flag definition carrying
// `elect_by` (contract §24.12).
func buildSelectorFlag(fd map[string]interface{}) strictcli.Flag {
	name := fd["name"].(string)
	help := fd["help"].(string)
	memberSpelled := fd["elect_by"].(string) == "member-flags"

	var opts []strictcli.FlagOption
	if v, ok := fd["short"]; ok {
		opts = append(opts, strictcli.Short(v.(string)))
	}
	if v, ok := fd["presence"]; ok {
		switch v.(string) {
		case "required":
			opts = append(opts, strictcli.Required())
		case "optional":
			opts = append(opts, strictcli.Optional())
		}
	}
	if v, ok := fd["default"]; ok {
		// Go's default NAMES a choice (§24.5); the case spells the same flat
		// map §25.6 publishes, and completeness is guaranteed by
		// errSelectorDefaultIncomplete rather than by the map's own fields.
		if m, ok := v.(map[string]interface{}); ok {
			opts = append(opts, strictcli.Default(m["choice"]))
		} else {
			opts = append(opts, strictcli.Default(v))
		}
	}
	if v, ok := fd["env"]; ok {
		opts = append(opts, strictcli.Env(v.(string)))
	}
	if v, ok := fd["prefixed"]; ok {
		opts = append(opts, strictcli.Prefixed(v.(bool)))
	}
	if v, ok := fd["conflict_mode"]; ok {
		opts = append(opts, strictcli.ConflictMode(v.(string)))
	}
	if chs, ok := fd["choices"]; ok {
		for _, c := range chs.([]interface{}) {
			cd := c.(map[string]interface{})
			decl := buildChoice(cd, memberSpelled)
			if cn, ok := cd["name"].(string); ok {
				choiceRegistry[name+"/"+cn] = decl
				choiceRegistry["*/"+cn] = decl
			}
			opts = append(opts, decl)
		}
	}
	if memberSpelled {
		return strictcli.MemberChoiceFlag(name, help, opts...)
	}
	return strictcli.ChoiceFlag(name, help, opts...)
}

// buildConstraintMembers reads a co-occurrence constraint's member array. Go's
// constructors take two named members before the variadic tail, so a case
// declaring fewer than two members cannot be expressed here at all -- the
// two-member floor is a COMPILE-time rule in this implementation (§26.6), which
// is why errConstraintMinMembers has no Go covering case.
func buildConstraintMembers(cd map[string]interface{}) (strictcli.ConstraintMember, strictcli.ConstraintMember, []strictcli.ConstraintMember) {
	raw := cd["members"].([]interface{})
	built := make([]strictcli.ConstraintMember, 0, len(raw))
	for _, m := range raw {
		md := m.(map[string]interface{})
		var opts []strictcli.MemberOption
		if w, ok := md["when"]; ok {
			switch w.(string) {
			case "present":
				opts = append(opts, strictcli.WhenPresent())
			case "true":
				opts = append(opts, strictcli.WhenTrue())
			case "non_empty":
				opts = append(opts, strictcli.WhenNonEmpty())
			}
		}
		built = append(built, strictcli.Member(md["name"].(string), opts...))
	}
	return built[0], built[1], built[2:]
}

// buildFlag constructs a strictcli.Flag from a JSON flag definition.
func buildFlag(fd map[string]interface{}) strictcli.Flag {
	if isSelector(fd) {
		return buildSelectorFlag(fd)
	}
	name := fd["name"].(string)
	help := fd["help"].(string)
	ftype := "str"
	if t, ok := fd["type"]; ok {
		ftype = t.(string)
	}

	var opts []strictcli.FlagOption

	if v, ok := fd["short"]; ok {
		opts = append(opts, strictcli.Short(v.(string)))
	}
	// The presence declaration (contract §23). "required" and "optional" have
	// their own spellings; "default" IS Default(v) in Go, so it is emitted by
	// the default block below. Each key is honoured independently so a case can
	// spell an illegal combination -- two declarations, or none -- and assert
	// the registration error it produces.
	if v, ok := fd["presence"]; ok {
		switch v.(string) {
		case "required":
			opts = append(opts, strictcli.Required())
		case "optional":
			opts = append(opts, strictcli.Optional())
		}
	}
	if v, ok := fd["default_relative_to_root"]; ok {
		rtr := v.(map[string]interface{})
		var parts []string
		if ps, ok := rtr["parts"]; ok {
			for _, p := range ps.([]interface{}) {
				parts = append(parts, p.(string))
			}
		}
		opts = append(opts, strictcli.Default(strictcli.RelativeToRoot(rtr["env_var"].(string), parts...)))
	}
	if v, ok := fd["default"]; ok {
		if v == nil {
			opts = append(opts, strictcli.Default(nil))
		} else if arr, ok := v.([]interface{}); ok {
			// List default: the elements carry the flag's ELEMENT type, which
			// is what sits inside the brackets of "list[int]".
			elem := elemTypeOf(ftype)
			converted := make([]interface{}, len(arr))
			for i, e := range arr {
				converted[i] = convertScalarDefault(elem, e)
			}
			opts = append(opts, strictcli.Default(converted))
		} else if m, ok := v.(map[string]interface{}); ok {
			// Dict default (contract §23.5's dict row): Go's dict flags take a
			// map[string]interface{} whose values carry the element type.
			elem := elemTypeOf(ftype)
			converted := make(map[string]interface{}, len(m))
			for k, e := range m {
				converted[k] = convertScalarDefault(elem, e)
			}
			opts = append(opts, strictcli.Default(converted))
		} else {
			opts = append(opts, strictcli.Default(convertScalarDefault(ftype, v)))
		}
	}
	if v, ok := fd["env"]; ok {
		opts = append(opts, strictcli.Env(v.(string)))
	}
	if v, ok := fd["prefixed"]; ok {
		opts = append(opts, strictcli.Prefixed(v.(bool)))
	}
	if vals, ok := choiceRecords(fd); ok {
		opts = append(opts, strictcli.Choices(vals...))
	}
	if vals, ok := retiredChoiceRecords(fd); ok {
		opts = append(opts, strictcli.RetiredChoices(vals...))
	}
	if v, ok := fd["repeatable"]; ok && v.(bool) {
		opts = append(opts, strictcli.Repeatable())
	}
	if v, ok := fd["unique"]; ok {
		opts = append(opts, strictcli.Unique(v.(bool)))
	} else if strings.HasPrefix(ftype, "list[") || strings.HasPrefix(ftype, "dict[") {
		// Compound types in Go require explicit unique; default to false
		// (Python auto-defaults this for list types and disallows it for dict types)
		opts = append(opts, strictcli.Unique(false))
	}
	if v, ok := fd["conflict_mode"]; ok {
		opts = append(opts, strictcli.ConflictMode(v.(string)))
	}
	if v, ok := fd["env_separator"]; ok {
		opts = append(opts, strictcli.EnvSeparator(v.(string)))
	}
	if v, ok := fd["negatable"]; ok && !v.(bool) {
		opts = append(opts, strictcli.NegatableOpt(false))
	}
	// The clear vocabulary's declaration (contract §27.6): legal only on a
	// property of an update, and what mints `--unset-<prop>`.
	if v, ok := fd["nullable"]; ok && v.(bool) {
		opts = append(opts, strictcli.Nullable())
	}
	// The corpus's one expressible validator shape (case-schema `validate`):
	// a callable refusing named values with a fixed message. Comparison goes
	// through each language's own default formatting of the value, so one
	// reject list means the same set in all three.
	if v, ok := fd["validate"]; ok {
		spec := v.(map[string]interface{})
		rejects := map[string]bool{}
		for _, r := range spec["rejects"].([]interface{}) {
			rejects[fmt.Sprintf("%v", r)] = true
		}
		message := spec["message"].(string)
		opts = append(opts, strictcli.ValidateFn(func(val interface{}) error {
			if rejects[fmt.Sprintf("%v", val)] {
				return errors.New(message)
			}
			return nil
		}))
	}

	switch ftype {
	case "bool":
		return strictcli.BoolFlag(name, help, opts...)
	case "int":
		return strictcli.IntFlag(name, help, opts...)
	case "float":
		return strictcli.FloatFlag(name, help, opts...)
	case "list[str]":
		return strictcli.ListFlag(strictcli.TypeStr, name, help, opts...)
	case "list[int]":
		return strictcli.ListFlag(strictcli.TypeInt, name, help, opts...)
	case "list[float]":
		return strictcli.ListFlag(strictcli.TypeFloat, name, help, opts...)
	case "dict[str,str]":
		return strictcli.DictFlag(strictcli.TypeStr, name, help, opts...)
	case "dict[str,int]":
		return strictcli.DictFlag(strictcli.TypeInt, name, help, opts...)
	case "dict[str,float]":
		return strictcli.DictFlag(strictcli.TypeFloat, name, help, opts...)
	default:
		return strictcli.StringFlag(name, help, opts...)
	}
}

// buildArg constructs a strictcli.Arg from a JSON arg definition.
func buildArg(ad map[string]interface{}) strictcli.Arg {
	name := ad["name"].(string)
	help := ad["help"].(string)

	var opts []strictcli.ArgOption

	atype := "str"
	if t, ok := ad["type"]; ok {
		atype = t.(string)
	}
	switch atype {
	case "bool":
		opts = append(opts, strictcli.ArgType(strictcli.TypeBool))
	case "int":
		opts = append(opts, strictcli.ArgType(strictcli.TypeInt))
	case "float":
		opts = append(opts, strictcli.ArgType(strictcli.TypeFloat))
	// The list-carrier spelling of a variadic arg (§25.4): one declaration with
	// one published shape, whichever of the two spellings declared it.
	case "list[str]":
		opts = append(opts, strictcli.ArgType(strictcli.TypeListStr))
	case "list[int]":
		opts = append(opts, strictcli.ArgType(strictcli.TypeListInt))
	case "list[float]":
		opts = append(opts, strictcli.ArgType(strictcli.TypeListFloat))
	}

	// The presence declaration, arg side (contract §23.3). "default" is spelled
	// by ArgDefault(v) itself, below.
	if v, ok := ad["presence"]; ok {
		switch v.(string) {
		case "required":
			opts = append(opts, strictcli.ArgRequired())
		case "optional":
			opts = append(opts, strictcli.ArgOptional())
		}
	}
	if v, ok := ad["default"]; ok {
		if v == nil {
			opts = append(opts, strictcli.ArgDefault(nil))
		} else {
			switch atype {
			case "int":
				opts = append(opts, strictcli.ArgDefault(int(v.(float64))))
			case "float":
				opts = append(opts, strictcli.ArgDefault(v.(float64)))
			case "bool":
				opts = append(opts, strictcli.ArgDefault(v.(bool)))
			default:
				opts = append(opts, strictcli.ArgDefault(v.(string)))
			}
		}
	}
	if v, ok := ad["variadic"]; ok && v.(bool) {
		opts = append(opts, strictcli.Variadic())
	}
	if vals, ok := choiceRecords(ad); ok {
		opts = append(opts, strictcli.ArgChoices(vals...))
	}
	if vals, ok := retiredChoiceRecords(ad); ok {
		opts = append(opts, strictcli.ArgRetiredChoices(vals...))
	}

	return strictcli.NewArg(name, help, opts...)
}

// buildCmdOptions constructs CmdOption list from a command definition.
func buildCmdOptions(cmdDef map[string]interface{}) []strictcli.CmdOption {
	var opts []strictcli.CmdOption

	// Args.
	if args, ok := cmdDef["args"]; ok {
		var argList []strictcli.Arg
		for _, a := range args.([]interface{}) {
			argList = append(argList, buildArg(a.(map[string]interface{})))
		}
		opts = append(opts, strictcli.WithArgs(argList...))
	}

	// Direct flags.
	if flags, ok := cmdDef["flags"]; ok {
		var flagList []strictcli.Flag
		for _, f := range flags.([]interface{}) {
			flagList = append(flagList, buildFlag(f.(map[string]interface{})))
		}
		opts = append(opts, strictcli.WithFlags(flagList...))
	}

	// Flag sets.
	if flagSets, ok := cmdDef["flag_sets"]; ok {
		for _, t := range flagSets.([]interface{}) {
			td := t.(map[string]interface{})
			fsName := td["name"].(string)
			var fsFlags []strictcli.Flag
			for _, f := range td["flags"].([]interface{}) {
				fsFlags = append(fsFlags, buildFlag(f.(map[string]interface{})))
			}
			opts = append(opts, strictcli.WithFlagSets(strictcli.FlagSet{Name: fsName, Flags: fsFlags}))
		}
	}

	// Constraints (contract §26). The case-schema key is `constraints`, and each
	// entry carries the mandatory name plus, for the two co-occurrence
	// families, a member array of `{name, when?}` records.
	if cs, ok := cmdDef["constraints"]; ok {
		var list []strictcli.Constraint
		for _, c := range cs.([]interface{}) {
			cd := c.(map[string]interface{})
			name := cd["name"].(string)
			switch cd["type"].(string) {
			case "at_least_one":
				a, b, rest := buildConstraintMembers(cd)
				list = append(list, strictcli.AtLeastOne(name, a, b, rest...))
			case "all_or_none":
				a, b, rest := buildConstraintMembers(cd)
				list = append(list, strictcli.AllOrNone(name, a, b, rest...))
			case "requires":
				list = append(list, strictcli.Requires(name, cd["flag"].(string), cd["depends_on"].(string)))
			case "implies":
				list = append(list, strictcli.Implies(name, cd["flag"].(string), cd["implies"].(string), cd["value"].(bool)))
			}
		}
		opts = append(opts, strictcli.WithConstraints(list...))
	}

	// Tags.
	if tags, ok := cmdDef["tags"]; ok {
		var tagList []string
		for _, t := range tags.([]interface{}) {
			tagList = append(tagList, t.(string))
		}
		opts = append(opts, strictcli.WithTags(tagList...))
	}

	// Config fields.
	if cfs, ok := cmdDef["config_fields"]; ok {
		var cfNames []string
		for _, f := range cfs.([]interface{}) {
			cfNames = append(cfNames, f.(string))
		}
		opts = append(opts, strictcli.WithConfigFields(cfNames...))
	}

	// Hidden.
	if v, ok := cmdDef["hidden"]; ok && v.(bool) {
		opts = append(opts, strictcli.WithHidden())
	}

	// Interactive.
	if v, ok := cmdDef["interactive"]; ok && v.(bool) {
		opts = append(opts, strictcli.WithInteractive())
	}

	// The effects regime: classification (§1.1), grants (§6.1) and declared
	// forwarding (§10.2). A case that omits `effect` is asserting the
	// registration hard error, so the option is only appended when declared.
	if v, ok := cmdDef["effect"]; ok {
		opts = append(opts, strictcli.WithEffect(v.(string)))
	}
	// `consequential` is NOT mandatory (§8.1): absence means "not
	// consequential", so the option is only appended when declared.
	if v, ok := cmdDef["consequential"]; ok && v.(bool) {
		opts = append(opts, strictcli.WithConsequential())
	}
	// `dry_run_supported` is likewise NOT mandatory: absence means supported.
	// Only false is declarable, and Go's single option carries the mandatory
	// reason, so the two case keys collapse into one call here.
	if v, ok := cmdDef["dry_run_supported"]; ok && !v.(bool) {
		reason, _ := cmdDef["dry_run_unsupported_reason"].(string)
		opts = append(opts, strictcli.WithDryRunUnsupported(reason))
	} else if reason, ok := cmdDef["dry_run_unsupported_reason"].(string); ok {
		// The orphan state: a reason with no declaration. WithDryRunUnsupported
		// cannot express it (it always sets both fields), but CmdOption is a
		// plain func(*Command) over exported fields, which is exactly the route
		// a consumer could take -- and exactly what the framework's guard
		// rejects.
		opts = append(opts, func(c *strictcli.Command) {
			c.DryRunUnsupportedReason = reason
		})
	}
	// The update-command construct (contract §27). The case schema follows the
	// DECLARATION side -- ONE record carrying its write mode (§13's amendment)
	// -- and Go's spelling takes the mode as a positional parameter, so an
	// absent or unknown write_mode reaches the constructor as a WriteMode whose
	// value is outside the vocabulary. That is the covering input for
	// errUpdateWriteModeInvalid, which is why the case schema does not require
	// the key.
	if v, ok := cmdDef["update_of"]; ok {
		ud := v.(map[string]interface{})
		resource, _ := ud["resource"].(string)
		mode, _ := ud["write_mode"].(string)
		var updOpts []strictcli.UpdateOption
		if names, ok := ud["identity"]; ok {
			var list []string
			for _, n := range names.([]interface{}) {
				list = append(list, n.(string))
			}
			updOpts = append(updOpts, strictcli.Identity(list...))
		}
		if names, ok := ud["properties"]; ok {
			var list []string
			for _, n := range names.([]interface{}) {
				list = append(list, n.(string))
			}
			// An empty list declares no properties at all: Properties() has a
			// compile-time floor of one, and the registration guard is what
			// refuses the caller that omits the option entirely.
			if len(list) > 0 {
				updOpts = append(updOpts, strictcli.Properties(list[0], list[1:]...))
			}
		}
		opts = append(opts, strictcli.WithUpdateOf(resource, strictcli.WriteMode(mode), updOpts...))
	}
	// The machine payload's declared schema (§19.5). A handler_returns of kind
	// "data"/"exit_data" supplies a payload, and ctx.Payload refuses to run on
	// a command that declares no schema -- so the harness declares the
	// permissive literal for exactly those commands. The literal is identical
	// in all three harnesses, which is what keeps the schema dump in parity.
	// A case may declare its own literal with "payload_schema", which is how
	// the closed subset's enforcement becomes observable through the CLI: the
	// schema dump publishes it verbatim and a payload is validated against it.
	if declared, ok := cmdDef["payload_schema"].(map[string]interface{}); ok {
		opts = append(opts, strictcli.PayloadSchema(declared))
	} else if hr, ok := cmdDef["handler_returns"].(map[string]interface{}); ok {
		if k, _ := hr["kind"].(string); k == "data" || k == "exit_data" {
			opts = append(opts, strictcli.PayloadSchema(map[string]interface{}{}))
		}
	} else if v, ok := cmdDef["handler_payloads_recorded"].(bool); ok && v {
		opts = append(opts, strictcli.PayloadSchema(map[string]interface{}{}))
	}
	// Stdout ownership (§19.6): the command's own document keeps stdout and the
	// envelope moves to stderr in machine mode.
	if v, ok := cmdDef["owns_stdout"].(bool); ok && v {
		opts = append(opts, strictcli.OwnsStdout())
	}
	if v, ok := cmdDef["grants"]; ok {
		var grants []strictcli.Grant
		for _, item := range v.([]interface{}) {
			g := item.(map[string]interface{})
			grants = append(grants, strictcli.Grant{
				Name:   g["name"].(string),
				Reason: g["reason"].(string),
				Kind:   g["kind"].(string),
			})
		}
		opts = append(opts, strictcli.WithGrants(grants...))
	}
	if v, ok := cmdDef["forwarding"]; ok {
		opts = append(opts, strictcli.WithForwarding(
			v.(map[string]interface{})["reason"].(string),
		))
	}

	return opts
}

// --- the effects vocabulary (effects contract §14.4) ---------------------
//
// `handler_effects` is materialized identically by all three harnesses:
// iterate the array in order, call the named method with EXACTLY the keys the
// entry declares (no per-method filtering -- a case declaring a key the method
// does not accept is asserting the error), and keep the returned carrier in a
// per-run map indexed by position so `forward_from` / `extract_from` can
// reference it.
//
// Go's carriers have no exported method in common (§2.5.3), so the map holds
// them as `any`: forwarding passes the value straight into the effects API's
// `any` parameter, and extraction type-switches to the shape's own extractor.

// effectOptions builds the option variadic from the keys the entry declares,
// in the harnesses' shared order.
func effectOptions(e map[string]interface{}) []strictcli.EffectOption {
	var opts []strictcli.EffectOption
	if v, ok := e["stream"]; ok {
		opts = append(opts, strictcli.Stream(v.(bool)))
	}
	if v, ok := e["resource"]; ok {
		opts = append(opts, strictcli.Resource(v.(string)))
	}
	if v, ok := e["skip_if_current"]; ok {
		opts = append(opts, strictcli.SkipIfCurrent(v.(string)))
	}
	if v, ok := e["grant"]; ok {
		opts = append(opts, strictcli.UseGrant(v.(string)))
	}
	return opts
}

// extractCarrier reads a concrete value out of a carrier -- the illegal use
// that trips the runtime seal and truncates the preview (§4.4). Each shape has
// its own extractor; all four panic with the truncation error when unsettled.
func extractCarrier(c interface{}) {
	switch v := c.(type) {
	case strictcli.Completed:
		_ = v.Stdout()
	case strictcli.Response:
		_ = v.Body()
	case strictcli.Spawned:
		_ = v.PID()
	case strictcli.Unsettled:
		_ = v.Bool()
	}
}

// runHandlerDiagnostics issues the declared Context diagnostic calls, in
// order. The four levels are gated by --quiet / --verbose (effects contract
// §7.4); the harness does no gating of its own, it just calls the method.
func runHandlerDiagnostics(ctx *strictcli.Context, entries []interface{}) {
	for _, raw := range entries {
		d := raw.(map[string]interface{})
		msg := d["message"].(string)
		switch d["level"].(string) {
		case "debug":
			ctx.Debug(msg)
		case "info":
			ctx.Info(msg)
		case "warn":
			ctx.Warn(msg)
		case "error":
			ctx.Error(msg)
		default:
			panic(fmt.Sprintf("unknown handler_diagnostics level: %v", d["level"]))
		}
	}
}

// runHandlerEffects issues the declared effect calls, in order.
func runHandlerEffects(ctx *strictcli.Context, entries []interface{}) {
	eff := map[int]interface{}{}
	for i, item := range entries {
		e := item.(map[string]interface{})
		method := e["method"].(string)

		if v, ok := e["extract_from"]; ok {
			// Terminal by construction: the extraction truncates the run.
			extractCarrier(eff[int(v.(float64))])
			return
		}

		var fwd interface{}
		hasFwd := false
		if v, ok := e["forward_from"]; ok {
			fwd = eff[int(v.(float64))]
			hasFwd = true
		}
		opts := effectOptions(e)

		var carrier interface{}
		var err error
		switch method {
		case "run", "spawn":
			var argv []any
			if v, ok := e["argv"]; ok {
				for _, a := range v.([]interface{}) {
					argv = append(argv, a.(string))
				}
			}
			if hasFwd {
				argv = append(argv, fwd)
			}
			if method == "run" {
				carrier, err = ctx.Effects().Run(argv, opts...)
			} else {
				carrier, err = ctx.Effects().Spawn(argv, opts...)
			}
		case "write":
			var path any = e["path"].(string)
			var content any
			if hasFwd {
				content = fwd
			} else {
				content = e["content"].(string)
			}
			carrier, err = ctx.Effects().Write(path, content, opts...)
		case "mkdir", "remove":
			var path any
			if hasFwd {
				path = fwd
			} else {
				path = e["path"].(string)
			}
			if method == "mkdir" {
				carrier, err = ctx.Effects().Mkdir(path, opts...)
			} else {
				carrier, err = ctx.Effects().Remove(path, opts...)
			}
		case "rename":
			var dst any
			if hasFwd {
				dst = fwd
			} else {
				dst = e["to"].(string)
			}
			carrier, err = ctx.Effects().Rename(e["path"].(string), dst, opts...)
		case "chmod":
			var path any
			if hasFwd {
				path = fwd
			} else {
				path = e["path"].(string)
			}
			mode, perr := strconv.ParseInt(e["mode"].(string), 8, 32)
			if perr != nil {
				panic(perr)
			}
			carrier, err = ctx.Effects().Chmod(path, int(mode), opts...)
		case "http":
			var url any
			if hasFwd {
				url = fwd
			} else {
				url = e["url"].(string)
			}
			carrier, err = ctx.Effects().HTTP(e["http_method"].(string), url, opts...)
		}
		if err != nil {
			// The Go idiom for what Python and TypeScript raise (§2.5.4, §17).
			// The harness surfaces it the same way the siblings' uncaught
			// exception surfaces: "error: <msg>" on stderr, exit 1.
			panic(err.Error())
		}
		eff[i+1] = carrier
	}
}

// ---------------------------------------------------------------------------
// Delivered records in handler templates (contract §24.1, §24.9)
//
// `{sel}` renders the whole record, `{sel.<field>}` reaches into it to any
// depth, `{sel.choice}` is the tag, and `{provided:sel.<field>}` is the
// RECORD's own provided-ness accessor -- the context-level one deliberately
// does not see scope interiors. All three harnesses render a record the same
// way: `<choice>` when its scope is empty, `<choice>(<field>=<value>, ...)`
// otherwise, fields in declaration order with the payload first.
// ---------------------------------------------------------------------------

// findChoiceDef returns the case's choice object of the given name.
func findChoiceDef(fd map[string]interface{}, name string) map[string]interface{} {
	chs, ok := fd["choices"]
	if !ok {
		return nil
	}
	for _, c := range chs.([]interface{}) {
		cd := c.(map[string]interface{})
		if cd["name"].(string) == name {
			return cd
		}
	}
	return nil
}

// recordFieldOrder is the declared field order of one choice's scope: the
// member payload first (where §25.6 places it), then the scope's own flags.
func recordFieldOrder(cd map[string]interface{}) []string {
	if cd == nil {
		return nil
	}
	var out []string
	if _, ok := cd["value"]; ok {
		out = append(out, "value")
	}
	if flags, ok := cd["flags"]; ok {
		for _, f := range flags.([]interface{}) {
			name := f.(map[string]interface{})["name"].(string)
			out = append(out, strings.ReplaceAll(name, "-", "_"))
		}
	}
	return out
}

// findFieldDef returns the scoped flag definition of the given key.
func findFieldDef(cd map[string]interface{}, key string) map[string]interface{} {
	if cd == nil {
		return nil
	}
	flags, ok := cd["flags"]
	if !ok {
		return nil
	}
	for _, f := range flags.([]interface{}) {
		fd := f.(map[string]interface{})
		if strings.ReplaceAll(fd["name"].(string), "-", "_") == key {
			return fd
		}
	}
	return nil
}

// formatFloatSCF renders a float in the strictcli canonical float form, the
// one decimal form the three implementations share byte for byte
// (go/strictcli/float.go, typescript/src/float.ts, and Python's repr, which is
// where conformance/float_vectors.json's expected strings come from).
//
// The harness needs its own copy because the Go package's formatter is
// unexported, and it cannot use fmt's %v: %v renders an integer-valued float
// as `0` where the canon renders `0.0`, and picks its own notation switch
// points. The TypeScript harness imports the real formatter and the Python
// harness gets the canon from `str()`, so before this the Go harness was the
// only target whose template output was not SCF -- which the differential
// fuzzer's constraint family found the first time a generated float flag was
// supplied an integral value.
func formatFloatSCF(v float64) string {
	if v == 0 {
		if math.Signbit(v) {
			return "-0.0"
		}
		return "0.0"
	}
	abs := math.Abs(v)
	if abs >= 1e-6 && abs < 1e21 {
		s := strconv.FormatFloat(v, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	s := strconv.FormatFloat(v, 'e', -1, 64)
	mantissa, exponent, _ := strings.Cut(s, "e")
	sign := "+"
	if strings.HasPrefix(exponent, "-") {
		sign = "-"
	}
	digits := strings.TrimLeft(strings.TrimLeft(exponent, "+-"), "0")
	if digits == "" {
		digits = "0"
	}
	return mantissa + "e" + sign + digits
}

// renderElem renders one delivered element of the template vocabulary. It is
// %v for every type the harness has always rendered that way, and the canonical
// form for a float (formatFloatSCF's own comment says why).
func renderElem(v interface{}) string {
	if f, ok := v.(float64); ok {
		return formatFloatSCF(f)
	}
	return fmt.Sprintf("%v", v)
}

// renderScoped renders one value out of a delivered record.
func renderScoped(v interface{}, def map[string]interface{}) string {
	switch tv := v.(type) {
	case nil:
		return "None"
	case *strictcli.Elected:
		return renderElected(tv, def)
	case bool:
		if tv {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", tv)
	case []interface{}:
		parts := make([]string, len(tv))
		for i, e := range tv {
			parts[i] = renderElem(e)
		}
		return strings.Join(parts, ",")
	case map[string]interface{}:
		keys := make([]string, 0, len(tv))
		for k := range tv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + "=" + renderElem(tv[k])
		}
		return strings.Join(parts, ",")
	default:
		return renderElem(v)
	}
}

// renderElected renders a whole delivered record.
func renderElected(e *strictcli.Elected, fd map[string]interface{}) string {
	cd := findChoiceDef(fd, e.Name())
	order := recordFieldOrder(cd)
	if len(order) == 0 {
		return e.Name()
	}
	parts := make([]string, len(order))
	for i, key := range order {
		parts[i] = key + "=" + renderScoped(e.Fields[key], findFieldDef(cd, key))
	}
	return e.Name() + "(" + strings.Join(parts, ", ") + ")"
}

// selWalk resolves a dotted scoped reference, returning the value it names and
// the declaration that value came from.
func selWalk(v interface{}, def map[string]interface{}, parts []string) (interface{}, map[string]interface{}) {
	for _, p := range parts {
		e, ok := v.(*strictcli.Elected)
		if !ok {
			panic(fmt.Sprintf("conformance harness: %q walks through a non-record", p))
		}
		if p == "choice" {
			return e.Name(), nil
		}
		key := strings.ReplaceAll(p, "-", "_")
		cd := findChoiceDef(def, e.Name())
		v = e.Fields[key]
		def = findFieldDef(cd, key)
	}
	return v, def
}

// selProvided answers the record's own provided-ness for a dotted reference:
// the walk stops one segment short and the last segment is the field name.
func selProvided(root *strictcli.Elected, def map[string]interface{}, parts []string) bool {
	v, d := selWalk(root, def, parts[:len(parts)-1])
	e, ok := v.(*strictcli.Elected)
	if !ok {
		panic("conformance harness: provided reference walks through a non-record")
	}
	_ = d
	return e.Provided(parts[len(parts)-1])
}

// templateRefs extracts the `{<prefix><name>}` references a template makes,
// in first-appearance order. Dotted names are scoped references and belong to
// the record walk, not to the context's store.
func templateRefs(template, prefix string) []string {
	var out []string
	needle := "{" + prefix
	rest := template
	for {
		i := strings.Index(rest, needle)
		if i < 0 {
			return out
		}
		rest = rest[i+len(needle):]
		j := strings.Index(rest, "}")
		if j < 0 {
			return out
		}
		if name := rest[:j]; !strings.Contains(name, ".") {
			out = append(out, name)
		}
		rest = rest[j:]
	}
}

// scopedRefs extracts the dotted references a template makes into a selector's
// delivered record, in first-appearance order.
func scopedRefs(template, prefix, sel string) []string {
	var out []string
	needle := "{" + prefix + sel + "."
	rest := template
	for {
		i := strings.Index(rest, needle)
		if i < 0 {
			return out
		}
		rest = rest[i+len(needle):]
		j := strings.Index(rest, "}")
		if j < 0 {
			return out
		}
		out = append(out, rest[:j])
		rest = rest[j:]
	}
}

// collectAllFlagDefs gathers all flag definitions for a command (global + direct + flag sets).
func collectAllFlagDefs(cmdDef map[string]interface{}, globalFlags []map[string]interface{}) []map[string]interface{} {
	var all []map[string]interface{}

	// Global flags first.
	all = append(all, globalFlags...)

	// Direct command flags.
	if flags, ok := cmdDef["flags"]; ok {
		for _, f := range flags.([]interface{}) {
			all = append(all, f.(map[string]interface{}))
		}
	}

	// Flag set flags.
	if flagSets, ok := cmdDef["flag_sets"]; ok {
		for _, t := range flagSets.([]interface{}) {
			td := t.(map[string]interface{})
			for _, f := range td["flags"].([]interface{}) {
				all = append(all, f.(map[string]interface{}))
			}
		}
	}

	return all
}

// handlerAbortMessage is what a `handler_aborts` handler panics with. The
// sibling harnesses raise/throw the identical text, and all three surface it
// as "error: <message>", so an aborting case's stderr is byte-identical
// across targets.
const handlerAbortMessage = "conformance: handler aborted"

// runHandlerClaim performs the claimed-rendering calls (effects contract
// §19.7). It runs AFTER handler_effects and BEFORE handler_diagnostics /
// handler_prints, so a rendered log lands ahead of the handler's own output --
// which is exactly the ordering the feature exists to make possible.
func runHandlerClaim(ctx *strictcli.Context, cmdDef map[string]interface{}) {
	if v, ok := cmdDef["handler_claims_log"].(bool); ok && v {
		ctx.Effects().Recorded()
	}
	if v, ok := cmdDef["handler_payloads_recorded"].(bool); ok && v {
		verbs := []string{}
		for _, rec := range ctx.Effects().Recorded() {
			verbs = append(verbs, rec["verb"].(string))
		}
		ctx.Payload(verbs)
	}
	if v, ok := cmdDef["handler_renders_log"].(bool); ok && v {
		ctx.Effects().RenderLog()
	}
}

// makeHandler builds a normal command handler function from a command definition.
func makeHandler(cmdDef map[string]interface{}, globalFlags []map[string]interface{}) func(ctx *strictcli.Context, args map[string]interface{}) strictcli.Outcome {
	// handler_aborts: the handler unwinds instead of returning, after its
	// effects and diagnostics have run. This is the language-neutral way to
	// reach the framework's aborted-preview path -- Go's Outcome type makes
	// handler_returns kind "bad" unrepresentable, but a panic is not.
	if v, ok := cmdDef["handler_aborts"]; ok && v.(bool) {
		effects, _ := cmdDef["handler_effects"].([]interface{})
		diags, _ := cmdDef["handler_diagnostics"].([]interface{})
		return func(ctx *strictcli.Context, args map[string]interface{}) strictcli.Outcome {
			runHandlerEffects(ctx, effects)
			runHandlerClaim(ctx, cmdDef)
			runHandlerDiagnostics(ctx, diags)
			panic(handlerAbortMessage)
		}
	}

	// handler_returns pins an explicit Outcome (survivor-contract cases): an
	// exit-only, data-only, exit+data, or None-equivalent return. When present,
	// the template-printing path is skipped entirely.
	if hrRaw, ok := cmdDef["handler_returns"]; ok {
		hr := hrRaw.(map[string]interface{})
		kind := hr["kind"].(string)
		// Inexpressible kinds are a HARD ERROR at registration, never a silent
		// mapping to something else. "bad" (a non-Outcome return) has no Go
		// representation, and quietly falling through to Exit(0) would turn a
		// case whose target restriction was dropped into a false pass.
		switch kind {
		case "exit", "data", "exit_data", "none":
		default:
			panic(fmt.Sprintf(
				"conformance harness: handler_returns kind %q is inexpressible "+
					"in Go's Outcome type; the case must restrict its targets",
				kind,
			))
		}
		code := 0
		if v, ok := hr["code"]; ok {
			code = int(v.(float64))
		}
		data := hr["data"]
		effects, _ := cmdDef["handler_effects"].([]interface{})
		diags, _ := cmdDef["handler_diagnostics"].([]interface{})
		return func(ctx *strictcli.Context, args map[string]interface{}) strictcli.Outcome {
			runHandlerEffects(ctx, effects)
			runHandlerClaim(ctx, cmdDef)
			runHandlerDiagnostics(ctx, diags)
			switch kind {
			case "data":
				ctx.Payload(data)
				return strictcli.Exit(0)
			case "exit_data":
				ctx.Payload(data)
				return strictcli.Exit(code)
			default: // "exit" or "none" (Go has no None; None maps to Exit(0))
				return strictcli.Exit(code)
			}
		}
	}

	// handler_effects runs BEFORE the handler_prints path and does not replace
	// it (§14.4). A handler_effects-only command declares no template.
	handlerEffects, _ := cmdDef["handler_effects"].([]interface{})
	handlerDiagnostics, _ := cmdDef["handler_diagnostics"].([]interface{})
	template := ""
	hasTemplate := false
	if v, ok := cmdDef["handler_prints"]; ok {
		template = v.(string)
		hasTemplate = true
	}
	exitCode := 0
	if v, ok := cmdDef["handler_exit_code"]; ok {
		exitCode = int(v.(float64))
	}

	allFlags := collectAllFlagDefs(cmdDef, globalFlags)

	// Collect arg defs.
	var argDefs []map[string]interface{}
	if args, ok := cmdDef["args"]; ok {
		for _, a := range args.([]interface{}) {
			argDefs = append(argDefs, a.(map[string]interface{}))
		}
	}

	// Capture for closure.
	ec := exitCode

	return func(ctx *strictcli.Context, args map[string]interface{}) strictcli.Outcome {
		runHandlerEffects(ctx, handlerEffects)
		runHandlerClaim(ctx, cmdDef)
		runHandlerDiagnostics(ctx, handlerDiagnostics)
		if !hasTemplate {
			return strictcli.Exit(ec)
		}
		out := template

		// Delivered records first, so `{via.subject}` is resolved before the
		// selector's own `{via}` key is (§24.1's one-key-per-selector rule).
		for _, fd := range allFlags {
			if !isSelector(fd) {
				continue
			}
			name := fd["name"].(string)
			key := strings.ReplaceAll(name, "-", "_")
			e := strictcli.GetElected(args, key)
			for _, ref := range scopedRefs(out, "provided:", name) {
				v := strconv.FormatBool(selProvided(e, fd, strings.Split(ref, ".")))
				out = strings.ReplaceAll(out, "{provided:"+name+"."+ref+"}", v)
			}
			for _, ref := range scopedRefs(out, "", name) {
				v, d := selWalk(e, fd, strings.Split(ref, "."))
				out = strings.ReplaceAll(out, "{"+name+"."+ref+"}", renderScoped(v, d))
			}
			out = strings.ReplaceAll(out, "{"+name+"}", renderElected(e, fd))
		}

		// Substitute {source:name} and {provided:name} through the context's
		// per-parse store. The names come from the TEMPLATE rather than from
		// the declaration list, so a case can ask the store about a name it
		// does not hold -- which is how §24.9's "a scoped flag is not in the
		// store at all" is asserted rather than assumed.
		for _, name := range templateRefs(out, "source:") {
			out = strings.ReplaceAll(out, "{source:"+name+"}", ctx.Source(name))
		}
		for _, name := range templateRefs(out, "provided:") {
			out = strings.ReplaceAll(
				out, "{provided:"+name+"}", strconv.FormatBool(ctx.Provided(name)))
		}
		// {unset:name} is the clear vocabulary's own accessor (contract §27.6).
		// The minted --unset-<prop> delivers no kwarg, so this is the only way
		// a handler learns that a property was CLEARED rather than left
		// untouched: both deliver absence, and Provided() is true for the
		// clear alone.
		for _, name := range templateRefs(out, "unset:") {
			out = strings.ReplaceAll(
				out, "{unset:"+name+"}", strconv.FormatBool(ctx.Unset(name)))
		}

		// Substitute flags.
		for _, fd := range allFlags {
			if isSelector(fd) {
				continue
			}
			name := fd["name"].(string)
			key := strings.ReplaceAll(name, "-", "_")
			ftype := "str"
			if t, ok := fd["type"]; ok {
				ftype = t.(string)
			}

			if strings.HasPrefix(ftype, "list[") {
				raw := args[key]
				// An OPTIONAL compound flag that received nothing delivers
				// absence rather than an empty collection (contract §23.5),
				// and absence renders as the scalar branches render it.
				if raw == nil {
					out = strings.ReplaceAll(out, "{"+name+"}", "None")
				} else {
					var parts []string
					itemType := ftype[5 : len(ftype)-1] // extract "int" from "list[int]"
					for _, v := range raw.([]interface{}) {
						if itemType == "int" {
							parts = append(parts, fmt.Sprintf("%d", v.(int)))
						} else {
							parts = append(parts, renderElem(v))
						}
					}
					out = strings.ReplaceAll(out, "{"+name+"}", strings.Join(parts, ","))
				}
			} else if strings.HasPrefix(ftype, "dict[") {
				raw := args[key]
				if raw == nil {
					out = strings.ReplaceAll(out, "{"+name+"}", "None")
				} else {
					var parts []string
					m := raw.(map[string]interface{})
					keys := make([]string, 0, len(m))
					for k := range m {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					for _, k := range keys {
						parts = append(parts, k+"="+renderElem(m[k]))
					}
					out = strings.ReplaceAll(out, "{"+name+"}", strings.Join(parts, ","))
				}
			} else if rep, ok := fd["repeatable"]; ok && rep.(bool) {
				raw := args[key]
				if raw == nil {
					out = strings.ReplaceAll(out, "{"+name+"}", "None")
				} else {
					var parts []string
					for _, v := range raw.([]interface{}) {
						if ftype == "int" {
							parts = append(parts, fmt.Sprintf("%d", v.(int)))
						} else {
							parts = append(parts, renderElem(v))
						}
					}
					out = strings.ReplaceAll(out, "{"+name+"}", strings.Join(parts, ","))
				}
			} else if ftype == "bool" {
				if args[key] == nil {
					out = strings.ReplaceAll(out, "{"+name+"}", "None")
				} else if args[key].(bool) {
					out = strings.ReplaceAll(out, "{"+name+"}", "true")
				} else {
					out = strings.ReplaceAll(out, "{"+name+"}", "false")
				}
			} else if ftype == "int" {
				// An omitted optional int flag arrives as nil, like str/bool.
				if args[key] == nil {
					out = strings.ReplaceAll(out, "{"+name+"}", "None")
				} else {
					out = strings.ReplaceAll(out, "{"+name+"}", fmt.Sprintf("%d", args[key].(int)))
				}
			} else if ftype == "float" {
				// Same for an omitted optional float flag.
				if args[key] == nil {
					out = strings.ReplaceAll(out, "{"+name+"}", "None")
				} else {
					out = strings.ReplaceAll(out, "{"+name+"}", formatFloatSCF(args[key].(float64)))
				}
			} else {
				// str -- might be nil
				if args[key] != nil {
					out = strings.ReplaceAll(out, "{"+name+"}", fmt.Sprintf("%v", args[key]))
				} else {
					out = strings.ReplaceAll(out, "{"+name+"}", "None")
				}
			}
		}

		// Substitute args.
		for _, ad := range argDefs {
			name := ad["name"].(string)
			key := name // args use name as-is
			atype := "str"
			if t, ok := ad["type"]; ok {
				atype = t.(string)
			}

			if v, ok := ad["variadic"]; ok && v.(bool) {
				raw := args[key]
				var parts []string
				if raw != nil {
					for _, v := range raw.([]interface{}) {
						switch atype {
						case "int":
							parts = append(parts, fmt.Sprintf("%d", v.(int)))
						default:
							parts = append(parts, renderElem(v))
						}
					}
				}
				out = strings.ReplaceAll(out, "{"+name+"}", strings.Join(parts, ","))
			} else if atype == "bool" {
				if args[key] == nil {
					out = strings.ReplaceAll(out, "{"+name+"}", "None")
				} else if args[key].(bool) {
					out = strings.ReplaceAll(out, "{"+name+"}", "true")
				} else {
					out = strings.ReplaceAll(out, "{"+name+"}", "false")
				}
			} else if atype == "int" {
				// An omitted optional int arg arrives as nil, like str/bool.
				if args[key] == nil {
					out = strings.ReplaceAll(out, "{"+name+"}", "None")
				} else {
					out = strings.ReplaceAll(out, "{"+name+"}", fmt.Sprintf("%d", args[key].(int)))
				}
			} else if atype == "float" {
				// Same for an omitted optional float arg.
				if args[key] == nil {
					out = strings.ReplaceAll(out, "{"+name+"}", "None")
				} else {
					out = strings.ReplaceAll(out, "{"+name+"}", formatFloatSCF(args[key].(float64)))
				}
			} else {
				if args[key] != nil {
					out = strings.ReplaceAll(out, "{"+name+"}", fmt.Sprintf("%v", args[key]))
				} else {
					out = strings.ReplaceAll(out, "{"+name+"}", "None")
				}
			}
		}

		fmt.Println(out)
		return strictcli.Exit(ec)
	}
}

// makePassthroughHandler builds a passthrough command handler.
func makePassthroughHandler(cmdDef map[string]interface{}, globalFlags []map[string]interface{}) strictcli.PassthroughHandler {
	exitCode := 0
	if v, ok := cmdDef["handler_exit_code"]; ok {
		exitCode = int(v.(float64))
	}
	ec := exitCode

	// handler_aborts: the passthrough handler unwinds instead of printing and
	// returning, exactly as the normal-command form does.
	if v, ok := cmdDef["handler_aborts"]; ok && v.(bool) {
		return func(ctx *strictcli.Context, name string, args []string, globals map[string]interface{}) int {
			panic(handlerAbortMessage)
		}
	}

	return func(ctx *strictcli.Context, name string, args []string, globals map[string]interface{}) int {
		// Print global flag values.
		for _, gf := range globalFlags {
			gfName := gf["name"].(string)
			gfKey := strings.ReplaceAll(gfName, "-", "_")
			gfType := "str"
			if t, ok := gf["type"]; ok {
				gfType = t.(string)
			}

			switch gfType {
			case "bool":
				if globals[gfKey] == nil {
					fmt.Printf("%s=None\n", gfName)
				} else if globals[gfKey].(bool) {
					fmt.Printf("%s=true\n", gfName)
				} else {
					fmt.Printf("%s=false\n", gfName)
				}
			case "int":
				fmt.Printf("%s=%d\n", gfName, globals[gfKey].(int))
			default:
				fmt.Printf("%s=%v\n", gfName, globals[gfKey])
			}
		}

		// Print using passthrough_handler_prints template, or default format.
		if pt, ok := cmdDef["passthrough_handler_prints"]; ok {
			out := pt.(string)
			out = strings.ReplaceAll(out, "{name}", name)
			out = strings.ReplaceAll(out, "{args}", strings.Join(args, ","))
			fmt.Println(out)
		} else {
			fmt.Printf("%s:%s\n", name, strings.Join(args, ","))
		}

		return ec
	}
}

// registerCommand registers a single command (normal, passthrough, or deprecated) on a target.
func registerCommand(cmdDef map[string]interface{}, t target, globalFlags []map[string]interface{}, app *strictcli.App) {
	name := cmdDef["name"].(string)
	help := cmdDef["help"].(string)

	// Deprecated command. Deprecated entries are classification-EXEMPT
	// (effects contract §1.1), so `effect` is forwarded ONLY when the case
	// declares it -- which is a case asserting errDeprecatedCommandEffect.
	if v, ok := cmdDef["deprecated"]; ok && v.(bool) {
		message := ""
		if m, ok := cmdDef["deprecated_message"]; ok {
			message = m.(string)
		}
		var depOpts []strictcli.CmdOption
		if e, ok := cmdDef["effect"]; ok {
			depOpts = append(depOpts, strictcli.WithEffect(e.(string)))
		}
		t.Deprecated(name, message, depOpts...)
		return
	}

	// Passthrough command.
	if v, ok := cmdDef["passthrough"]; ok && v.(bool) {
		handler := makePassthroughHandler(cmdDef, globalFlags)
		opts := []strictcli.CmdOption{strictcli.WithPassthrough(handler)}
		opts = append(opts, buildCmdOptions(cmdDef)...)
		t.Command(name, help, nil, opts...)
		return
	}

	// Normal command.
	handler := makeHandler(cmdDef, globalFlags)
	_ = app
	opts := buildCmdOptions(cmdDef)
	t.Command(name, help, handler, opts...)
}

// buildGroup recursively registers a group and its contents on an App.
func buildGroup(groupDef map[string]interface{}, app *strictcli.App, globalFlags []map[string]interface{}) {
	name := groupDef["name"].(string)
	help := groupDef["help"].(string)
	var tags []string
	if t, ok := groupDef["tags"]; ok {
		for _, item := range t.([]interface{}) {
			tags = append(tags, item.(string))
		}
	}
	group := app.Group(name, help, tags...)
	if v, ok := groupDef["hidden"]; ok && v.(bool) {
		group.Hidden = true
	}
	populateGroup(groupDef, group, globalFlags, app)
}

// buildSubGroup recursively registers a sub-group and its contents on a parent Group.
func buildSubGroup(groupDef map[string]interface{}, parent *strictcli.Group, globalFlags []map[string]interface{}, app *strictcli.App) {
	name := groupDef["name"].(string)
	help := groupDef["help"].(string)
	var tags []string
	if t, ok := groupDef["tags"]; ok {
		for _, item := range t.([]interface{}) {
			tags = append(tags, item.(string))
		}
	}
	group := parent.Group(name, help, tags...)
	if v, ok := groupDef["hidden"]; ok && v.(bool) {
		group.Hidden = true
	}
	populateGroup(groupDef, group, globalFlags, app)
}

// populateGroup registers commands and sub-groups on a group.
func populateGroup(groupDef map[string]interface{}, group *strictcli.Group, globalFlags []map[string]interface{}, app *strictcli.App) {
	// Register commands.
	if cmds, ok := groupDef["commands"]; ok {
		for _, c := range cmds.([]interface{}) {
			registerCommand(c.(map[string]interface{}), groupTarget{group}, globalFlags, app)
		}
	}

	// Register sub-groups recursively.
	if groups, ok := groupDef["groups"]; ok {
		for _, g := range groups.([]interface{}) {
			buildSubGroup(g.(map[string]interface{}), group, globalFlags, app)
		}
	}
}
