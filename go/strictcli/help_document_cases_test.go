package strictcli

// Assertions over whole help documents (help --json) of the apps declared in
// testdata/help_document_apps, run through the case harness, and the trace
// store's observational-only sweeps over named cases.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// orderedObject is a JSON object with its keys in document order, which a
// map loses and the fragment check reads.
type orderedObject struct {
	keys   []string
	values map[string]any
}

// decodeOrdered decodes a JSON document, objects as *orderedObject.
func decodeOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch delim := tok.(type) {
	case json.Delim:
		switch delim {
		case '{':
			obj := &orderedObject{values: map[string]any{}}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key := keyTok.(string)
				value, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				obj.keys = append(obj.keys, key)
				obj.values[key] = value
			}
			_, err := dec.Token()
			return obj, err
		case '[':
			var list []any
			for dec.More() {
				value, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				list = append(list, value)
			}
			_, err := dec.Token()
			return list, err
		}
	}
	return tok, nil
}

// plain converts an ordered value back into maps and slices.
func plain(v any) any {
	switch typed := v.(type) {
	case *orderedObject:
		out := map[string]any{}
		for _, k := range typed.keys {
			out[k] = plain(typed.values[k])
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = plain(item)
		}
		return out
	}
	return v
}

// helpDocumentOf runs `help --json` for an app definition file and returns
// the document decoded with its key order kept.
func helpDocumentOf(t *testing.T, harness, appFile string) *orderedObject {
	t.Helper()
	raw, err := os.ReadFile(appFile)
	if err != nil {
		t.Fatal(err)
	}
	var app map[string]any
	if err := json.Unmarshal(raw, &app); err != nil {
		t.Fatal(err)
	}
	c := behaviorCase{Name: filepath.Base(appFile), App: app, Argv: []string{"help", "--json"}}
	result, problems := runCase(t, harness, t.TempDir(), c, t.TempDir(), nil)
	// The expectation block is empty, so the only problem a successful run
	// reports is the exit code check against 0.
	if result == nil || result.exitCode != 0 {
		t.Fatalf("%s: help --json failed: %v", appFile, problems)
	}
	doc, err := decodeOrdered(json.NewDecoder(strings.NewReader(result.stdout)))
	if err != nil {
		t.Fatalf("%s: the help document is not JSON: %v", appFile, err)
	}
	return doc.(*orderedObject)
}

// fragmentKeywords is the closed subset a value_schema is written in, in the
// order its keys are emitted.
var fragmentKeywords = []string{"type", "items", "additionalProperties", "enum"}

var fragmentTypes = map[string]bool{
	"string": true, "boolean": true, "integer": true, "number": true, "array": true, "object": true,
}

// checkFragment asserts a value_schema validates under the payload-schema
// validator, uses only the closed subset's keywords in their declared order,
// names a type, and carries items, additionalProperties and enum only where
// they apply.
func checkFragment(fragment any, where string) []string {
	obj, ok := fragment.(*orderedObject)
	if !ok {
		return []string{fmt.Sprintf("%s: value_schema is %T, not an object", where, fragment)}
	}
	var problems []string
	if finding := validatePayloadSchemaLiteral(plain(obj).(map[string]any)); finding != nil {
		problems = append(problems, fmt.Sprintf("%s: the payload-schema validator refuses it: %v", where, finding))
	}
	var walk func(node any, path string)
	walk = func(node any, path string) {
		o, ok := node.(*orderedObject)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: expected an object, got %T", path, node))
			return
		}
		var seen []string
		for _, key := range o.keys {
			known := false
			for _, kw := range fragmentKeywords {
				known = known || key == kw
			}
			if !known {
				problems = append(problems, fmt.Sprintf("%s: keyword %q is outside the closed subset (%s)", path, key, strings.Join(fragmentKeywords, ", ")))
				continue
			}
			seen = append(seen, key)
		}
		var ordered []string
		for _, kw := range fragmentKeywords {
			for _, s := range seen {
				if s == kw {
					ordered = append(ordered, kw)
				}
			}
		}
		if strings.Join(seen, ",") != strings.Join(ordered, ",") {
			problems = append(problems, fmt.Sprintf("%s: keys are emitted as %v, not in the declared order %v", path, seen, ordered))
		}
		typ, hasType := o.values["type"]
		typeName, _ := typ.(string)
		switch {
		case !hasType:
			problems = append(problems, path+": no `type`; every fragment names one")
		case !fragmentTypes[typeName]:
			problems = append(problems, fmt.Sprintf("%s: type %v is not a JSON Schema type name", path, typ))
		}
		if items, ok := o.values["items"]; ok {
			if typeName != "array" {
				problems = append(problems, path+": `items` on a non-array fragment")
			}
			walk(items, path+".items")
		}
		if ap, ok := o.values["additionalProperties"]; ok {
			if typeName != "object" {
				problems = append(problems, path+": `additionalProperties` on a non-object fragment")
			}
			walk(ap, path+".additionalProperties")
		}
		if enum, ok := o.values["enum"]; ok {
			list, isList := enum.([]any)
			switch {
			case !isList || len(list) == 0:
				problems = append(problems, path+": `enum` must be a non-empty array")
			case typeName == "array":
				problems = append(problems, path+": `enum` sits at the root of an array fragment; it belongs inside `items`, describing the element")
			}
		}
	}
	walk(obj, where)
	return problems
}

// checkFragmentEntry asserts one flag, arg, or config-field entry: a selector
// (an entry carrying elect_by) has no value_schema and choice objects whose
// scoped flags are entries too; every other entry carries a valid fragment.
func checkFragmentEntry(entry *orderedObject, where, kind string) []string {
	var problems []string
	if electBy, isSelector := entry.values["elect_by"]; isSelector {
		if kind != "flag" {
			problems = append(problems, fmt.Sprintf("%s: `elect_by` on a %s entry; only a flag elects", where, kind))
		}
		if _, ok := entry.values["value_schema"]; ok {
			problems = append(problems, where+": a selector carries a value_schema; its absence is the declaration")
		}
		if electBy != "selector-token" && electBy != "member-flags" {
			problems = append(problems, fmt.Sprintf("%s: elect_by is %v, not one of 'selector-token' / 'member-flags'", where, electBy))
		}
		choices, _ := entry.values["choices"].([]any)
		for i, c := range choices {
			choice, ok := c.(*orderedObject)
			cwhere := fmt.Sprintf("%s.choices[%d]", where, i)
			if !ok || choice.values["name"] == nil {
				problems = append(problems, cwhere+": a selector's choices are choice objects")
				continue
			}
			flags, _ := choice.values["flags"].([]any)
			for j, f := range flags {
				problems = append(problems, checkFragmentEntry(f.(*orderedObject), fmt.Sprintf("%s.flags[%d]", cwhere, j), "flag")...)
			}
		}
		return problems
	}
	fragment, ok := entry.values["value_schema"]
	if !ok {
		return []string{fmt.Sprintf("%s: no value_schema; every %s entry that is not a selector carries one", where, kind)}
	}
	return checkFragment(fragment, where+".value_schema")
}

// fragmentProblems walks every fragment-bearing site of a help document.
func fragmentProblems(doc *orderedObject) []string {
	var problems []string
	entries := func(node *orderedObject, key, where, kind string) {
		list, _ := node.values[key].([]any)
		for i, e := range list {
			problems = append(problems, checkFragmentEntry(e.(*orderedObject), fmt.Sprintf("%s.%s[%d]", where, key, i), kind)...)
		}
	}
	var command func(cmd *orderedObject, where string)
	command = func(cmd *orderedObject, where string) {
		entries(cmd, "flags", where, "flag")
		entries(cmd, "args", where, "arg")
	}
	var group func(g *orderedObject, where string)
	group = func(g *orderedObject, where string) {
		if cmds, ok := g.values["commands"].(*orderedObject); ok {
			for _, name := range cmds.keys {
				command(cmds.values[name].(*orderedObject), fmt.Sprintf("%s.commands[%q]", where, name))
			}
		}
		if groups, ok := g.values["groups"].(*orderedObject); ok {
			for _, name := range groups.keys {
				group(groups.values[name].(*orderedObject), fmt.Sprintf("%s.groups[%q]", where, name))
			}
		}
	}
	entries(doc, "global_flags", "$", "flag")
	group(doc, "$")
	if fields, ok := doc.values["config_fields"].(*orderedObject); ok {
		for _, name := range fields.keys {
			problems = append(problems, checkFragmentEntry(fields.values[name].(*orderedObject), fmt.Sprintf("$.config_fields[%q]", name), "config field")...)
		}
	}
	return problems
}

// presenceProblems reports every flag or arg entry that does not carry
// presence as one of the three declarable values, carries a default other
// than exactly when presence is "default", or is an arg entry with a
// `required` key.
func presenceProblems(node any, path string) []string {
	var problems []string
	switch typed := node.(type) {
	case *orderedObject:
		for _, spec := range []struct{ kind, key string }{{"flag", "global_flags"}, {"flag", "flags"}, {"arg", "args"}} {
			list, _ := typed.values[spec.key].([]any)
			for i, e := range list {
				entry, ok := e.(*orderedObject)
				if !ok {
					continue
				}
				where := fmt.Sprintf("%s.%s[%d] (%v)", path, spec.key, i, entry.values["name"])
				presence, hasPresence := entry.values["presence"]
				_, hasDefault := entry.values["default"]
				switch {
				case !hasPresence:
					problems = append(problems, where+": no 'presence' key; presence is always emitted")
				case presence != "required" && presence != "optional" && presence != "default":
					problems = append(problems, fmt.Sprintf("%s: presence is %v, expected one of required, optional, default", where, presence))
				}
				if presence == "default" && !hasDefault {
					problems = append(problems, where+": presence is 'default' with no 'default' key")
				}
				if (presence == "required" || presence == "optional") && hasDefault {
					problems = append(problems, fmt.Sprintf("%s: presence is %v but a 'default' key is emitted", where, presence))
				}
				if _, ok := entry.values["required"]; ok && spec.kind == "arg" {
					problems = append(problems, where+": an arg entry carries a 'required' key beside presence")
				}
			}
		}
		for _, key := range typed.keys {
			switch key {
			case "defaults", "global_flags", "flags", "args":
				continue
			}
			problems = append(problems, presenceProblems(typed.values[key], path+"."+key)...)
		}
	case []any:
		for i, item := range typed {
			problems = append(problems, presenceProblems(item, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return problems
}

// TestHelpDocumentFragmentsAndPresence holds every help document of the apps
// in testdata/help_document_apps to two promises: every value_schema is a
// valid document of the closed four-keyword subset, and every flag and arg
// entry states its presence, with a default exactly when presence is
// "default".
func TestHelpDocumentFragmentsAndPresence(t *testing.T) {
	harness := caseHarness(t)
	apps, err := filepath.Glob(filepath.Join("testdata", "help_document_apps", "*.json"))
	if err != nil || len(apps) == 0 {
		t.Fatalf("no apps under testdata/help_document_apps (%v)", err)
	}
	for _, app := range apps {
		app := app
		t.Run(filepath.Base(app), func(t *testing.T) {
			doc := helpDocumentOf(t, harness, app)
			for _, problem := range fragmentProblems(doc) {
				t.Error(problem)
			}
			for _, problem := range presenceProblems(doc, "$") {
				t.Error(problem)
			}
		})
	}
}

// The fragment and presence checks find what they are for.
func TestHelpDocumentChecksFindViolations(t *testing.T) {
	parse := func(text string) *orderedObject {
		v, err := decodeOrdered(json.NewDecoder(strings.NewReader(text)))
		if err != nil {
			t.Fatal(err)
		}
		return v.(*orderedObject)
	}
	bad := parse(`{"global_flags": [
	  {"name": "a", "value_schema": {"enum": ["x"], "type": "string"}, "presence": "default"},
	  {"name": "b", "value_schema": {"type": "array", "enum": [["x"]]}, "presence": "optional", "default": []},
	  {"name": "c", "presence": "required"}
	]}`)
	fragments := strings.Join(fragmentProblems(bad), "\n")
	for _, want := range []string{"not in the declared order", "`enum` sits at the root of an array fragment", "no value_schema"} {
		if !strings.Contains(fragments, want) {
			t.Errorf("the fragment check missed %q:\n%s", want, fragments)
		}
	}
	presence := strings.Join(presenceProblems(bad, "$"), "\n")
	for _, want := range []string{"presence is 'default' with no 'default' key", "presence is optional but a 'default' key is emitted"} {
		if !strings.Contains(presence, want) {
			t.Errorf("the presence check missed %q:\n%s", want, presence)
		}
	}
}

// The case runner reports a case whose run does not match its expectations.
func TestCaseRunnerReportsAMismatch(t *testing.T) {
	cases := loadCases(t)
	harness := caseHarness(t)
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Expect.StdoutEquals == nil || c.ProtocolScript != nil {
			continue
		}
		wrong := "this is not what the program prints"
		c.Expect.StdoutEquals = &wrong
		c.Expect.ExitCode += 7
		_, problems := runCase(t, harness, testdata, c, t.TempDir(), nil)
		joined := strings.Join(problems, "\n")
		if !strings.Contains(joined, "exit_code: expected") || !strings.Contains(joined, "stdout mismatch") {
			t.Fatalf("the runner did not report both mismatches of %q:\n%s", c.Name, joined)
		}
		return
	}
	t.Fatal("no case asserts stdout_equals")
}

// The trace store's observational-only sweeps: the named cases run once with
// no trace parent and a healthy, empty store, and again under each condition
// below; stdout, stderr, and the exit code must match the baseline byte for
// byte. A forged ancestry is a false attribution claim, never an input to
// behavior, and a store that cannot be written changes nothing either.
var traceSweepCases = []string{
	"effects_call_errors: a nonzero exit fails the run call",
	"effects_call_errors: a nonzero exit fails an allowlisted observe too",
	"effects_read_only: an allowlisted run is an observe and executes",
	"effects_read_only: an observe executes in dry mode and is never logged",
	"effects_read_only: a non-allowlisted run is refused in a read-only command",
	"effects_log: a spawn is an effect and is recorded, not performed",
	"effects_log: a live run records the effect it performed",
	"effects_dry_run_log: every verb renders in the pinned line format",
	"effects_output_gating: quiet does not suppress the machine payload",
	"envelope: a plain exit emits the envelope as the sole stdout document",
	"help: app help shows version and commands",
}

// traceStore is where the store sits under a HOME.
func traceStore(home string) string {
	return filepath.Join(home, ".local", "share", "strictcli", "trace")
}

var tracePartition = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}\.jsonl$`)

// traceEntries counts the entries across every partition file of a store.
func traceEntries(home string) int {
	entries, err := os.ReadDir(traceStore(home))
	if err != nil {
		return 0
	}
	total := 0
	for _, e := range entries {
		if !tracePartition.MatchString(e.Name()) {
			continue
		}
		raw, _ := os.ReadFile(filepath.Join(traceStore(home), e.Name()))
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) != "" {
				total++
			}
		}
	}
	return total
}

func TestTraceStoreIsObservationalOnly(t *testing.T) {
	const (
		forgedValid   = "01JZ8X4M6N7QK2WVBD3F5RTYAC"
		forgedGarbage = "not a ulid at all -- ../../etc/passwd"
		parentEnv     = "STRICTCLI_TRACE_PARENT"
	)
	byName := map[string]behaviorCase{}
	for _, c := range loadCases(t) {
		byName[c.Name] = c
	}
	var cases []behaviorCase
	for _, name := range traceSweepCases {
		c, ok := byName[name]
		if !ok {
			t.Fatalf("the sweep names a case that does not exist: %q", name)
		}
		cases = append(cases, c)
	}
	harness := caseHarness(t)
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	// The baseline runs with no trace parent at all: runCase never passes an
	// inherited one on.
	run := func(c behaviorCase, home, parent string) caseResult {
		var extra map[string]string
		if parent != "" {
			extra = map[string]string{parentEnv: parent}
		}
		r, _ := runCase(t, harness, testdata, c, home, extra)
		if r == nil {
			t.Fatalf("%q produced no result", c.Name)
		}
		return *r
	}
	baselineHome := t.TempDir()
	baseline := map[string]caseResult{}
	for _, c := range cases {
		baseline[c.Name] = run(c, baselineHome, "")
	}
	if traceEntries(baselineHome) == 0 {
		t.Fatal("the baseline store received no entries: the sweep's cases no longer reach the spawn seam")
	}

	conditions := []struct {
		name, parent, store string
	}{
		{"forged valid id", forgedValid, "healthy"},
		{"forged garbage id", forgedGarbage, "healthy"},
		{"unwritable store", "", "unwritable-dir"},
		{"store path is a file", "", "path-is-a-file"},
		{"forged id and unwritable store", forgedValid, "unwritable-dir"},
	}
	for _, cond := range conditions {
		home := t.TempDir()
		store := traceStore(home)
		switch cond.store {
		case "unwritable-dir":
			if err := os.MkdirAll(store, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(store, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(store, 0o700) })
		case "path-is-a-file":
			if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(store, []byte("not a directory\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for _, c := range cases {
			got, want := run(c, home, cond.parent), baseline[c.Name]
			if got != want {
				t.Errorf("%s / %s: differs from the baseline\n  baseline: %+v\n  swept:    %+v", cond.name, c.Name, want, got)
			}
		}
	}
}
