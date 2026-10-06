package strictcli

// The behavior cases: testdata/cases/*.json declares, in JSON, an app, the
// argv it runs with, and what its exit code, streams, effect log, written
// config file, and help document must be. testdata/caseharness is a program
// that builds the app a case declares from that JSON (read from the file
// CONFORMANCE_APP_DEF names) and runs it; TestCases builds it once and runs
// every case against it as a subprocess, so each case sees the process
// boundary a real program has: its exit status, its own stdout and stderr,
// its environment, and its stdin.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// caseTimeout bounds one case's run, and caseStepTimeout one scripted
// protocol step's wait for a reply line.
const (
	caseTimeout     = 10 * time.Second
	caseStepTimeout = 10 * time.Second
)

// anyValue is the per-field wildcard: an expected value of this string matches
// any actual value, of any type, at that position. It is for fields whose
// value is nondeterministic by construction -- a continuation signature, a
// timestamp -- where only the field's presence is asserted.
const anyValue = "$ANY"

// behaviorCase is one case as a file declares it. App is kept raw: it is the
// harness's input, handed over as written.
type behaviorCase struct {
	Name                   string            `json:"name"`
	SkipSchemaValidation   bool              `json:"skip_schema_validation"`
	ProjectRootNotWorkTree bool              `json:"project_root_not_a_work_tree"`
	App                    map[string]any    `json:"app"`
	Argv                   []string          `json:"argv"`
	Env                    map[string]string `json:"env"`
	Stdin                  *string           `json:"stdin"`
	ProtocolScript         []protocolStep    `json:"protocol_script"`
	Expect                 caseExpect        `json:"expect"`
	file                   string
	raw                    map[string]any
}

// protocolStep is one step of a line-scripted exchange: send a line, and/or
// read one reply line and assert on it.
type protocolStep struct {
	Send       *string           `json:"send"`
	Capture    map[string]string `json:"capture"`
	Stream     string            `json:"stream"`
	ExpectLine map[string]any    `json:"expect_line"`
}

// caseExpect is what a case asserts about its run.
type caseExpect struct {
	ExitCode                int                       `json:"exit_code"`
	StdoutContains          stringList                `json:"stdout_contains"`
	StdoutEquals            *string                   `json:"stdout_equals"`
	StdoutNotContains       stringList                `json:"stdout_not_contains"`
	StdoutMatches           stringList                `json:"stdout_matches"`
	StderrContains          stringList                `json:"stderr_contains"`
	StderrEquals            *string                   `json:"stderr_equals"`
	StderrNotContains       stringList                `json:"stderr_not_contains"`
	StderrMatches           stringList                `json:"stderr_matches"`
	ConfigFileContains      stringList                `json:"config_file_contains"`
	ConfigFileNotContains   stringList                `json:"config_file_not_contains"`
	ConfigFileMatches       stringList                `json:"config_file_matches"`
	EffectsEquals           *[]any                    `json:"effects_equals"`
	SchemaCommandKeys       map[string]map[string]any `json:"schema_command_keys"`
	SchemaCommandAbsentKeys map[string][]string       `json:"schema_command_absent_keys"`
	SchemaBytesEqual        *string                   `json:"schema_bytes_equal"`
}

// stringList is a field written either as one string or as a list of them.
type stringList []string

func (l *stringList) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*l = stringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

// caseResult is a finished run: its exit status and its two streams.
type caseResult struct {
	exitCode int
	stdout   string
	stderr   string
}

// The harness binary, built once per test binary.
var (
	harnessOnce sync.Once
	harnessPath string
	harnessErr  error
	harnessDir  string
)

// caseHarness builds testdata/caseharness and returns the binary's path.
func caseHarness(t *testing.T) string {
	t.Helper()
	harnessOnce.Do(func() {
		dir, err := os.MkdirTemp("", "strictcli-caseharness-")
		if err != nil {
			harnessErr = err
			return
		}
		harnessDir = dir
		harnessPath = filepath.Join(dir, "caseharness")
		build := exec.Command("go", "build", "-o", harnessPath, ".")
		build.Dir = filepath.Join("testdata", "caseharness")
		if out, err := build.CombinedOutput(); err != nil {
			harnessErr = fmt.Errorf("building the case harness: %v\n%s", err, out)
		}
	})
	if harnessErr != nil {
		t.Fatal(harnessErr)
	}
	return harnessPath
}

// removeCaseHarness removes the built harness; TestMain calls it once every
// test has run.
func removeCaseHarness() {
	if harnessDir != "" {
		os.RemoveAll(harnessDir)
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	removeCaseHarness()
	os.Exit(code)
}

// loadCases reads every case file, validating each case that does not opt
// out against testdata/case_schema.json.
func loadCases(t *testing.T) []behaviorCase {
	t.Helper()
	schema := compileCaseSchema(t)
	files, err := filepath.Glob(filepath.Join("testdata", "cases", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no case files under testdata/cases (%v)", err)
	}
	sort.Strings(files)
	var cases []behaviorCase
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var generic []any
		if err := json.Unmarshal(raw, &generic); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		forSchema, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var typed []behaviorCase
		if err := json.Unmarshal(raw, &typed); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for i := range typed {
			typed[i].file = filepath.Base(file)
			typed[i].raw = generic[i].(map[string]any)
			if !typed[i].SkipSchemaValidation {
				if err := schema.Validate(forSchema.([]any)[i]); err != nil {
					t.Fatalf("%s: case %q does not validate against case_schema.json: %v", file, typed[i].Name, err)
				}
			}
		}
		cases = append(cases, typed...)
	}
	return cases
}

// compileCaseSchema compiles the single-case definition of
// testdata/case_schema.json.
func compileCaseSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "case_schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("case_schema.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile("case_schema.json#/$defs/test_case")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestCases(t *testing.T) {
	cases := loadCases(t)
	harness := caseHarness(t)
	testdata, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range cases {
		key := c.file + ": " + c.Name
		if seen[key] {
			t.Fatalf("two cases are named %q", key)
		}
		seen[key] = true
		c := c
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			_, problems := runCase(t, harness, testdata, c, home, nil)
			for _, problem := range problems {
				t.Error(problem)
			}
		})
	}
}

// runCase runs one case and returns its result and every assertion that
// failed. home is the HOME the run sees; extraEnv overlays the environment
// last.
func runCase(t *testing.T, harness, testdata string, c behaviorCase, home string, extraEnv map[string]string) (*caseResult, []string) {
	t.Helper()
	work := t.TempDir()
	app := map[string]any{}
	for k, v := range c.App {
		app[k] = v
	}
	argv := append([]string{}, c.Argv...)

	// config_content is written to a file the app reads: through $CONFIG_PATH
	// in argv when the case names it there, through config_path otherwise.
	// config_content_late gets a path only; the harness writes the content
	// after constructing the app and before running it.
	var configPath string
	if content, ok := app["config_content"].(string); ok {
		configPath = filepath.Join(work, "strictcli_cfg_config"+configExt(app))
		if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		named := false
		for i, arg := range argv {
			if strings.Contains(arg, "$CONFIG_PATH") {
				argv[i] = strings.ReplaceAll(arg, "$CONFIG_PATH", configPath)
				named = true
			}
		}
		if !named {
			app["config_path"] = configPath
		}
	}
	if _, ok := app["config_content_late"]; ok {
		configPath = filepath.Join(work, "strictcli_lcfg_config"+configExt(app))
		app["config_path"] = configPath
	}

	appDef := filepath.Join(work, "app.json")
	encoded, err := json.Marshal(app)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appDef, encoded, 0o644); err != nil {
		t.Fatal(err)
	}

	// A case never inherits a trace parent from whatever runs the tests: the
	// trace sweeps set one deliberately.
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "STRICTCLI_TRACE_PARENT=") {
			env = append(env, kv)
		}
	}
	env = append(env, "HOME="+home)
	for k, v := range c.Env {
		env = append(env, k+"="+v)
	}
	env = append(env, "CONFORMANCE_APP_DEF="+appDef)
	effectLog := ""
	if c.Expect.EffectsEquals != nil {
		effectLog = filepath.Join(work, "effects.json")
		env = append(env, "CONFORMANCE_EFFECT_LOG="+effectLog)
	}
	for k, v := range extraEnv {
		env = append(env, k+"="+v)
	}

	// The working directory: testdata for an ordinary case (where its
	// fixtures/ resolve), a git work tree for a case declaring a source-tree
	// root, and a bare directory for the one case pinning a root that is not
	// a work tree.
	cwd := testdata
	switch {
	case c.ProjectRootNotWorkTree:
		cwd = filepath.Join(work, "project")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
	case app["source_tree_root"] != nil:
		cwd = filepath.Join(work, "project")
		if out, err := exec.Command("git", "init", "-q", cwd).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		if err := os.MkdirAll(filepath.Join(cwd, ".strictmetadata", ".cli-test-coverage"), 0o755); err != nil {
			t.Fatal(err)
		}
		if seed, ok := app["coverage_manifest"]; ok {
			dir := filepath.Join(cwd, app["source_tree_root"].(string), ".strictmetadata", ".cli-test-coverage")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			body, _ := json.MarshalIndent(seed, "", "  ")
			if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(body, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	var result *caseResult
	var problems []string
	if c.ProtocolScript != nil {
		result, problems = runProtocolScript(harness, argv, env, cwd, c.ProtocolScript)
	} else {
		result, err = runOnce(harness, argv, env, cwd, c.Stdin)
		if err != nil {
			return nil, []string{err.Error()}
		}
	}
	problems = append(problems, checkExpect(c, argv, result, configPath, effectLog)...)
	return result, problems
}

// configExt is the config file's extension for the app's declared format.
func configExt(app map[string]any) string {
	if app["config_format"] == "toml" {
		return ".toml"
	}
	return ".json"
}

// runOnce runs the harness with the whole of stdin handed over up front, or
// with /dev/null -- never a terminal -- when the case declares none.
func runOnce(harness string, argv, env []string, cwd string, stdin *string) (*caseResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), caseTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, harness, argv...)
	cmd.Env = env
	cmd.Dir = cwd
	if stdin != nil {
		cmd.Stdin = strings.NewReader(*stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("timed out after %s", caseTimeout)
	}
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		return nil, err
	}
	return &caseResult{exitCode: code, stdout: stdout.String(), stderr: stderr.String()}, nil
}

// captureRef is a `{{name}}` or `{{name|tamper}}` placeholder in a line to
// send.
var captureRef = regexp.MustCompile(`\{\{([A-Za-z_][A-Za-z0-9_]*)(\|tamper)?\}\}`)

// streamLines reads a stream line by line onto a channel, keeping the whole
// text too, and closes the channel when the stream ends.
func streamLines(r *bufio.Reader, all *strings.Builder, mu *sync.Mutex, lines chan<- string, done *sync.WaitGroup) {
	defer done.Done()
	defer close(lines)
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			mu.Lock()
			all.WriteString(line)
			mu.Unlock()
			lines <- line
		}
		if err != nil {
			return
		}
	}
}

// runProtocolScript drives the harness through a scripted exchange: each
// step sends a line, reads one reply line, or both, and a step may capture a
// value out of its reply for a later step to splice into what it sends.
func runProtocolScript(harness string, argv, env []string, cwd string, script []protocolStep) (*caseResult, []string) {
	cmd := exec.Command(harness, argv...)
	cmd.Env = env
	cmd.Dir = cwd
	stdin, _ := cmd.StdinPipe()
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, []string{err.Error()}
	}
	var mu sync.Mutex
	var outAll, errAll strings.Builder
	outLines := make(chan string, 1024)
	errLines := make(chan string, 1024)
	var readers sync.WaitGroup
	readers.Add(2)
	go streamLines(bufio.NewReader(stdoutPipe), &outAll, &mu, outLines, &readers)
	go streamLines(bufio.NewReader(stderrPipe), &errAll, &mu, errLines, &readers)

	var problems []string
	captured := map[string]any{}
steps:
	for i, step := range script {
		n := i + 1
		if step.Send != nil {
			line := captureRef.ReplaceAllStringFunc(*step.Send, func(ref string) string {
				m := captureRef.FindStringSubmatch(ref)
				value, ok := captured[m[1]]
				if !ok {
					problems = append(problems, fmt.Sprintf("protocol_script: nothing captured under %q", m[1]))
					return ref
				}
				text := fmt.Sprint(value)
				if m[2] != "" && text != "" {
					first := "A"
					if text[0] == 'A' {
						first = "B"
					}
					return first + text[1:]
				}
				return text
			})
			if _, err := stdin.Write([]byte(line + "\n")); err != nil {
				problems = append(problems, fmt.Sprintf("protocol_script step %d: the child closed its stdin before the line could be sent", n))
				break
			}
		}
		if step.ExpectLine == nil && len(step.Capture) == 0 {
			continue
		}
		source := outLines
		if step.Stream == "stderr" {
			source = errLines
		}
		var line string
		var open bool
		select {
		case line, open = <-source:
		case <-time.After(caseStepTimeout):
			problems = append(problems, fmt.Sprintf("protocol_script step %d: no line within %s", n, caseStepTimeout))
			break steps
		}
		if step.ExpectLine != nil {
			if !open {
				problems = append(problems, fmt.Sprintf("protocol_script step %d: the stream closed before a line arrived", n))
			} else {
				problems = append(problems, checkProtocolLine(strings.TrimSuffix(line, "\n"), step.ExpectLine, n)...)
			}
		}
		if len(step.Capture) > 0 {
			if !open {
				problems = append(problems, fmt.Sprintf("protocol_script step %d: the stream closed before a line could be captured", n))
				break
			}
			problems = append(problems, captureFromReply(line, step.Capture, captured)...)
		}
	}
	stdin.Close()
	exited := make(chan error, 1)
	go func() {
		readers.Wait()
		exited <- cmd.Wait()
	}()
	var waitErr error
	select {
	case waitErr = <-exited:
	case <-time.After(caseStepTimeout):
		cmd.Process.Kill()
		waitErr = <-exited
		problems = append(problems, "protocol_script: the child did not exit after the script")
	}
	code := 0
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		code = exitErr.ExitCode()
	}
	mu.Lock()
	defer mu.Unlock()
	return &caseResult{exitCode: code, stdout: outAll.String(), stderr: errAll.String()}, problems
}

// captureFromReply pulls declared values out of one reply line, each by a
// dotted path into the parsed line.
func captureFromReply(line string, paths map[string]string, captured map[string]any) []string {
	var parsed any
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		return []string{fmt.Sprintf("protocol_script: cannot capture from a non-JSON line: %q", line)}
	}
	var problems []string
	for name, path := range paths {
		cursor := parsed
		found := true
		for _, part := range strings.Split(path, ".") {
			object, ok := cursor.(map[string]any)
			if !ok {
				found = false
				break
			}
			if cursor, ok = object[part]; !ok {
				found = false
				break
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf("protocol_script: capture %q found no %q in %q", name, path, line))
			continue
		}
		captured[name] = cursor
	}
	return problems
}

// checkProtocolLine asserts one reply line against a step's expectations.
func checkProtocolLine(text string, expect map[string]any, step int) []string {
	where := fmt.Sprintf("protocol_script step %d", step)
	var problems []string
	if want, ok := expect["equals"].(string); ok && text != want {
		problems = append(problems, fmt.Sprintf("%s: line %q, expected %q", where, text, want))
	}
	if want, ok := expect["contains"]; ok {
		problems = append(problems, checkContains(text, asStrings(want), where+" line")...)
	}
	if want, ok := expect["not_contains"]; ok {
		problems = append(problems, checkNotContains(text, asStrings(want), where+" line")...)
	}
	if want, ok := expect["matches"]; ok {
		problems = append(problems, checkMatches(text, asStrings(want), where+" line")...)
	}
	if want, ok := expect["json_equals"]; ok {
		parsed, err := decodeJSONNumbers(text)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: line is not valid JSON (%v): %q", where, err, text))
		} else if mismatches := structuralEqual(parsed, want, fmt.Sprintf("step %d", step)); len(mismatches) > 0 {
			problems = append(problems, where+": json_equals mismatch:")
			problems = append(problems, mismatches...)
		}
	}
	return problems
}

// asStrings reads a field written as one string or a list of them.
func asStrings(v any) []string {
	switch typed := v.(type) {
	case string:
		return []string{typed}
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		return out
	}
	return nil
}

// checkExpect asserts every expectation of a case against its run.
func checkExpect(c behaviorCase, argv []string, r *caseResult, configPath, effectLog string) []string {
	e := c.Expect
	var problems []string
	if r.exitCode != e.ExitCode {
		problems = append(problems, fmt.Sprintf("exit_code: expected %d, got %d\nstderr: %q\nstdout: %q", e.ExitCode, r.exitCode, r.stderr, r.stdout))
	}
	problems = append(problems, checkContains(r.stdout, e.StdoutContains, "stdout")...)
	problems = append(problems, checkNotContains(r.stdout, e.StdoutNotContains, "stdout")...)
	problems = append(problems, checkMatches(r.stdout, e.StdoutMatches, "stdout")...)
	if e.StdoutEquals != nil {
		problems = append(problems, checkEquals(r.stdout, *e.StdoutEquals, "stdout")...)
	}
	problems = append(problems, checkContains(r.stderr, e.StderrContains, "stderr")...)
	problems = append(problems, checkNotContains(r.stderr, e.StderrNotContains, "stderr")...)
	problems = append(problems, checkMatches(r.stderr, e.StderrMatches, "stderr")...)
	if e.StderrEquals != nil {
		problems = append(problems, checkEquals(r.stderr, *e.StderrEquals, "stderr")...)
	}
	if e.ConfigFileContains != nil || e.ConfigFileNotContains != nil || e.ConfigFileMatches != nil {
		content, err := os.ReadFile(configPath)
		if configPath == "" || err != nil {
			problems = append(problems, "config_file_* assertion requires a seeded config file (config_content / config_content_late), but none was found")
		} else {
			text := string(content)
			problems = append(problems, checkContains(text, e.ConfigFileContains, "config_file")...)
			problems = append(problems, checkNotContains(text, e.ConfigFileNotContains, "config_file")...)
			problems = append(problems, checkMatches(text, e.ConfigFileMatches, "config_file")...)
		}
	}
	if e.EffectsEquals != nil {
		problems = append(problems, checkEffects(effectLog, c.raw["expect"].(map[string]any)["effects_equals"])...)
	}
	if e.SchemaCommandKeys != nil || e.SchemaCommandAbsentKeys != nil {
		problems = append(problems, checkSchemaCommands(argv, r.stdout, c.raw["expect"].(map[string]any))...)
	}
	if e.SchemaBytesEqual != nil {
		problems = append(problems, checkSchemaBytes(argv, r.stdout, *e.SchemaBytesEqual)...)
	}
	return problems
}

// normalizeStream strips trailing whitespace from every line and trailing
// newlines from the whole, for the _equals comparisons.
func normalizeStream(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r\f\v")
	}
	return strings.Join(lines, "\n")
}

func checkContains(actual string, want []string, stream string) []string {
	var problems []string
	for _, s := range want {
		if !strings.Contains(actual, s) {
			problems = append(problems, fmt.Sprintf("%s does not contain: %q\nactual %s: %q", stream, s, stream, actual))
		}
	}
	return problems
}

func checkNotContains(actual string, want []string, stream string) []string {
	var problems []string
	for _, s := range want {
		if strings.Contains(actual, s) {
			problems = append(problems, fmt.Sprintf("%s should NOT contain: %q\nactual %s: %q", stream, s, stream, actual))
		}
	}
	return problems
}

func checkMatches(actual string, want []string, stream string) []string {
	var problems []string
	for _, pattern := range want {
		re, err := regexp.Compile(pattern)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s pattern %q does not compile: %v", stream, pattern, err))
			continue
		}
		if !re.MatchString(actual) {
			problems = append(problems, fmt.Sprintf("%s does not match pattern: %q\nactual %s: %q", stream, pattern, stream, actual))
		}
	}
	return problems
}

func checkEquals(actual, want, stream string) []string {
	a, w := normalizeStream(actual), normalizeStream(want)
	if a == w {
		return nil
	}
	return []string{fmt.Sprintf("%s mismatch:\n  expected: %q\n  actual:   %q", stream, w, a)}
}

// decodeJSONNumbers decodes JSON keeping numbers exact, so 1 and 1.0 compare
// equal and an integer above 2^53 keeps its digits.
func decodeJSONNumbers(text string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var v any
	if err := decoder.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// numberValue reads a decoded JSON number of either decoding as an exact
// rational.
func numberValue(v any) (*big.Rat, bool) {
	switch typed := v.(type) {
	case json.Number:
		r, ok := new(big.Rat).SetString(string(typed))
		return r, ok
	case float64:
		r := new(big.Rat)
		if r.SetFloat64(typed) == nil {
			return nil, false
		}
		return r, true
	}
	return nil, false
}

// structuralEqual compares two decoded JSON values. Key order is never part
// of the comparison; an expected anyValue matches anything.
func structuralEqual(actual, expected any, path string) []string {
	where := path
	if where == "" {
		where = "<root>"
	}
	if s, ok := expected.(string); ok && s == anyValue {
		return nil
	}
	switch exp := expected.(type) {
	case map[string]any:
		act, ok := actual.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s: expected an object, got %T", where, actual)}
		}
		var missing, extra []string
		for k := range exp {
			if _, ok := act[k]; !ok {
				missing = append(missing, k)
			}
		}
		for k := range act {
			if _, ok := exp[k]; !ok {
				extra = append(extra, k)
			}
		}
		if len(missing) > 0 || len(extra) > 0 {
			sort.Strings(missing)
			sort.Strings(extra)
			return []string{fmt.Sprintf("%s: key sets differ (missing %v; unexpected %v)", where, missing, extra)}
		}
		keys := make([]string, 0, len(exp))
		for k := range exp {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var problems []string
		for _, k := range keys {
			problems = append(problems, structuralEqual(act[k], exp[k], where+"."+k)...)
		}
		return problems
	case []any:
		act, ok := actual.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s: expected an array, got %T", where, actual)}
		}
		if len(act) != len(exp) {
			return []string{fmt.Sprintf("%s: array length %d, expected %d", where, len(act), len(exp))}
		}
		var problems []string
		for i := range exp {
			problems = append(problems, structuralEqual(act[i], exp[i], fmt.Sprintf("%s[%d]", where, i))...)
		}
		return problems
	}
	if a, ok := numberValue(actual); ok {
		if e, ok := numberValue(expected); ok {
			if a.Cmp(e) == 0 {
				return nil
			}
			return []string{fmt.Sprintf("%s: %v, expected %v", where, actual, expected)}
		}
	}
	if fmt.Sprintf("%T:%v", actual, actual) != fmt.Sprintf("%T:%v", expected, expected) {
		return []string{fmt.Sprintf("%s: %#v, expected %#v", where, actual, expected)}
	}
	return nil
}

// effectRecordOptionalKeys are the effect record keys that are absent and
// null alike; both sides drop them before comparing.
var effectRecordOptionalKeys = []string{"bytes", "resource", "skip_if_current", "grant"}

func normalizeEffectRecords(records []any) []any {
	out := make([]any, 0, len(records))
	for _, r := range records {
		record, ok := r.(map[string]any)
		if !ok {
			out = append(out, r)
			continue
		}
		kept := map[string]any{}
		for k, v := range record {
			drop := false
			for _, optional := range effectRecordOptionalKeys {
				if k == optional && v == nil {
					drop = true
				}
			}
			if !drop {
				kept[k] = v
			}
		}
		out = append(out, kept)
	}
	return out
}

// checkEffects compares the structured effect log the run wrote.
func checkEffects(logPath string, expected any) []string {
	raw, err := os.ReadFile(logPath)
	if err != nil {
		return []string{fmt.Sprintf("effects_equals: the harness wrote no effect log (expected it at %q)", logPath)}
	}
	actual, err := decodeJSONNumbers(string(raw))
	if err != nil {
		return []string{fmt.Sprintf("effects_equals: effect log is not valid JSON: %v", err)}
	}
	actualList, _ := actual.([]any)
	expectedList, _ := expected.([]any)
	mismatches := structuralEqual(normalizeEffectRecords(actualList), normalizeEffectRecords(expectedList), "effects")
	if len(mismatches) == 0 {
		return nil
	}
	return append([]string{"effects_equals mismatch:"}, append(mismatches, "actual: "+string(raw))...)
}

// isHelpDocumentRun reports whether argv prints the help document.
func isHelpDocumentRun(argv []string) bool {
	if len(argv) == 0 || argv[0] != "help" {
		return false
	}
	for _, arg := range argv {
		if arg == "--json" {
			return true
		}
	}
	return false
}

// withoutProjectID removes the help document's project_id line, which names
// the program that printed it.
func withoutProjectID(text string) (string, bool) {
	lines := strings.Split(text, "\n")
	kept := lines[:0:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, `  "project_id": `) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n"), len(kept) != len(lines)
}

// checkSchemaBytes compares the whole help document as text, apart from its
// project_id line, which must be present.
func checkSchemaBytes(argv []string, stdout, expected string) []string {
	if !isHelpDocumentRun(argv) {
		return []string{"schema_bytes_equal requires `help --json` in the case argv"}
	}
	actual, hadID := withoutProjectID(stdout)
	want, _ := withoutProjectID(expected)
	if !hadID {
		return []string{"schema_bytes_equal: the help document carries no project_id"}
	}
	if actual == want {
		return nil
	}
	a, w := strings.Split(actual, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(a) || i < len(w); i++ {
		al, wl := "<missing>", "<missing>"
		if i < len(a) {
			al = a[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if al != wl {
			return []string{fmt.Sprintf("schema_bytes_equal: first difference at line %d\n  expected: %q\n  actual:   %q", i+1, wl, al)}
		}
	}
	return []string{"schema_bytes_equal: files differ but no line differs"}
}

// resolveSchemaCommand walks the help document's group tree to the command at
// a dotted path.
func resolveSchemaCommand(schema map[string]any, dotted string) map[string]any {
	node := schema
	parts := strings.Split(dotted, ".")
	for _, part := range parts[:len(parts)-1] {
		groups, _ := node["groups"].(map[string]any)
		next, ok := groups[part].(map[string]any)
		if !ok {
			return nil
		}
		node = next
	}
	commands, _ := node["commands"].(map[string]any)
	entry, _ := commands[parts[len(parts)-1]].(map[string]any)
	return entry
}

// checkSchemaCommands asserts which keys a command entry of the help document
// carries, and with what values.
func checkSchemaCommands(argv []string, stdout string, expect map[string]any) []string {
	if !isHelpDocumentRun(argv) {
		return []string{"schema_command_* assertion requires `help --json` in the case argv"}
	}
	parsed, err := decodeJSONNumbers(stdout)
	if err != nil {
		return []string{fmt.Sprintf("schema_command_*: the help document is not valid JSON: %v", err)}
	}
	schema, _ := parsed.(map[string]any)
	var problems []string
	keys, _ := expect["schema_command_keys"].(map[string]any)
	for dotted, fields := range keys {
		entry := resolveSchemaCommand(schema, dotted)
		if entry == nil {
			problems = append(problems, fmt.Sprintf("schema_command_keys: no command %q in the emitted schema", dotted))
			continue
		}
		for key, want := range fields.(map[string]any) {
			got, ok := entry[key]
			if !ok {
				problems = append(problems, fmt.Sprintf("schema_command_keys: %s.%s is absent, expected %v", dotted, key, want))
				continue
			}
			for _, line := range structuralEqual(got, want, dotted+"."+key) {
				problems = append(problems, "schema_command_keys: "+line)
			}
		}
	}
	absent, _ := expect["schema_command_absent_keys"].(map[string]any)
	for dotted, list := range absent {
		entry := resolveSchemaCommand(schema, dotted)
		if entry == nil {
			problems = append(problems, fmt.Sprintf("schema_command_absent_keys: no command %q in the emitted schema", dotted))
			continue
		}
		for _, key := range asStrings(list) {
			if v, ok := entry[key]; ok {
				problems = append(problems, fmt.Sprintf("schema_command_absent_keys: %s.%s is present (%v), expected absent", dotted, key, v))
			}
		}
	}
	return problems
}
