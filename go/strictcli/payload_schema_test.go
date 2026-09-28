package strictcli

// The declared payload schema's validator (effects contract §19.5).
//
// The bulk of the coverage is the committed cross-language vector file at
// conformance/payload_schema_vectors.json, replayed here and by the Python and
// TypeScript suites. Every vector pins both the verdict AND the exact error
// text, which is what makes the three validators byte-identical rather than
// merely similarly-strict.
//
// The tests that cannot be shared vectors -- values JSON has no way to carry,
// Go's typed-struct payloads, and the framework's own wiring -- are written
// natively below.

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type payloadVector struct {
	Name              string          `json:"name"`
	Schema            json.RawMessage `json:"schema"`
	Value             json.RawMessage `json:"value"`
	Valid             bool            `json:"valid"`
	Path              string          `json:"path"`
	Detail            string          `json:"detail"`
	UnrepresentableIn []string        `json:"unrepresentable_in"`
	UnrepReason       string          `json:"unrepresentable_reason"`
}

type payloadVectorDoc struct {
	SchemaVectorCount   int             `json:"schema_vector_count"`
	InstanceVectorCount int             `json:"instance_vector_count"`
	SchemaVectors       []payloadVector `json:"schema_vectors"`
	InstanceVectors     []payloadVector `json:"instance_vectors"`
}

func payloadVectorsPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(thisFile) // .../go/strictcli
	return filepath.Join(dir, "..", "..", "conformance", "payload_schema_vectors.json")
}

func loadPayloadVectors(t *testing.T) payloadVectorDoc {
	t.Helper()
	data, err := os.ReadFile(payloadVectorsPath(t))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var doc payloadVectorDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	if len(doc.SchemaVectors) != doc.SchemaVectorCount {
		t.Fatalf("schema count mismatch: header says %d, got %d",
			doc.SchemaVectorCount, len(doc.SchemaVectors))
	}
	if len(doc.InstanceVectors) != doc.InstanceVectorCount {
		t.Fatalf("instance count mismatch: header says %d, got %d",
			doc.InstanceVectorCount, len(doc.InstanceVectors))
	}
	return doc
}

func (v payloadVector) skipHere() bool {
	for _, impl := range v.UnrepresentableIn {
		if impl == "go" {
			return true
		}
	}
	return false
}

// decodeVectorJSON reads a raw vector member with UseNumber, so an integer of
// 2^53+1 survives as its exact literal rather than collapsing to a float64.
func decodeVectorJSON(t *testing.T, raw json.RawMessage) interface{} {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out interface{}
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode vector member: %v", err)
	}
	return out
}

func decodeVectorSchema(t *testing.T, raw json.RawMessage) map[string]interface{} {
	t.Helper()
	v := decodeVectorJSON(t, raw)
	m, ok := v.(map[string]interface{})
	if !ok {
		t.Fatalf("vector schema is not an object: %s", string(raw))
	}
	return m
}

func TestPayloadSchemaVectors(t *testing.T) {
	doc := loadPayloadVectors(t)
	if len(doc.SchemaVectors) < 50 {
		t.Fatalf("expected a substantial vector set, got %d", len(doc.SchemaVectors))
	}
	for _, v := range doc.SchemaVectors {
		if v.skipHere() {
			continue
		}
		t.Run(v.Name, func(t *testing.T) {
			schema := decodeVectorSchema(t, v.Schema)
			f := validatePayloadSchemaLiteral(schema)
			if v.Valid {
				if f != nil {
					t.Fatalf("unexpectedly rejected at %s: %s", f.Path, f.Detail)
				}
				return
			}
			if f == nil {
				t.Fatal("unexpectedly accepted")
			}
			if f.Path != v.Path {
				t.Errorf("path: got %q, want %q", f.Path, v.Path)
			}
			if f.Detail != v.Detail {
				t.Errorf("detail:\n got %q\nwant %q", f.Detail, v.Detail)
			}
		})
	}
}

func TestPayloadInstanceVectors(t *testing.T) {
	doc := loadPayloadVectors(t)
	if len(doc.InstanceVectors) < 140 {
		t.Fatalf("expected a substantial vector set, got %d", len(doc.InstanceVectors))
	}
	for _, v := range doc.InstanceVectors {
		if v.skipHere() {
			continue
		}
		t.Run(v.Name, func(t *testing.T) {
			schema := decodeVectorSchema(t, v.Schema)
			if f := validatePayloadSchemaLiteral(schema); f != nil {
				t.Fatalf("the vector's own schema is illegal at %s: %s", f.Path, f.Detail)
			}
			value := decodeVectorJSON(t, v.Value)
			f := validatePayloadValue(value, schema)
			if v.Valid {
				if f != nil {
					t.Fatalf("unexpectedly rejected at %s: %s", f.Path, f.Detail)
				}
				return
			}
			if f == nil {
				t.Fatal("unexpectedly accepted")
			}
			if f.Path != v.Path {
				t.Errorf("path: got %q, want %q", f.Path, v.Path)
			}
			if f.Detail != v.Detail {
				t.Errorf("detail:\n got %q\nwant %q", f.Detail, v.Detail)
			}
		})
	}
}

func TestPayloadVectorExclusionsCarryReasons(t *testing.T) {
	doc := loadPayloadVectors(t)
	all := append(append([]payloadVector{}, doc.SchemaVectors...), doc.InstanceVectors...)
	for _, v := range all {
		if len(v.UnrepresentableIn) == 0 {
			continue
		}
		if strings.TrimSpace(v.UnrepReason) == "" {
			t.Errorf("vector %q excludes %v with no reason", v.Name, v.UnrepresentableIn)
		}
		if len(v.UnrepresentableIn) == 3 {
			t.Errorf("vector %q excludes every implementation", v.Name)
		}
	}
}

// ---------------------------------------------------------------------------
// Values JSON cannot carry
// ---------------------------------------------------------------------------

func TestPayloadRejectsNonRepresentableValues(t *testing.T) {
	cases := []struct {
		name  string
		value interface{}
	}{
		{"NaN", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"negative infinity", math.Inf(-1)},
		{"a channel", make(chan int)},
		{"a function", func() {}},
		{"a NaN nested in a map", map[string]interface{}{"a": math.NaN()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := validatePayloadValue(tc.value, map[string]interface{}{})
			if f == nil {
				t.Fatal("unexpectedly accepted")
			}
			if f.Detail != pdetailNotJSON {
				t.Errorf("detail: got %q, want %q", f.Detail, pdetailNotJSON)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Go's typed payloads: the value is validated as encoding/json will emit it
// ---------------------------------------------------------------------------

type payloadTestItem struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Note  string `json:"note,omitempty"`
}

func TestPayloadValidatesTheEmittedShapeOfAStruct(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name":  map[string]interface{}{"type": "string"},
			"count": map[string]interface{}{"type": "integer"},
		},
		"required":             []interface{}{"name", "count"},
		"additionalProperties": false,
	}
	if f := validatePayloadValue(payloadTestItem{Name: "a", Count: 2}, schema); f != nil {
		t.Fatalf("unexpectedly rejected at %s: %s", f.Path, f.Detail)
	}
}

func TestPayloadOmitEmptyIsHonouredByRequired(t *testing.T) {
	// `note` carries omitempty, so an empty value emits no key at all -- and
	// the validator sees exactly that, because it validates the emitted
	// document rather than the Go value.
	schema := map[string]interface{}{
		"type":     "object",
		"required": []interface{}{"note"},
	}
	f := validatePayloadValue(payloadTestItem{Name: "a"}, schema)
	if f == nil {
		t.Fatal("unexpectedly accepted")
	}
	if f.Detail != pdetailRequiredMissing("note") {
		t.Errorf("detail: got %q", f.Detail)
	}
}

func TestPayloadValidatesATypedSlice(t *testing.T) {
	schema := map[string]interface{}{
		"type":  "array",
		"items": map[string]interface{}{"type": "object"},
	}
	items := []payloadTestItem{{Name: "a", Count: 1}, {Name: "b", Count: 2}}
	if f := validatePayloadValue(items, schema); f != nil {
		t.Fatalf("unexpectedly rejected at %s: %s", f.Path, f.Detail)
	}
}

func TestPayloadTypeListWrittenAsAGoStringSlice(t *testing.T) {
	// []string{"string","null"} and []interface{}{"string","null"} emit the
	// same literal, so the validator must accept both spellings.
	schema := map[string]interface{}{"type": []string{"string", "null"}}
	if f := validatePayloadSchemaLiteral(schema); f != nil {
		t.Fatalf("unexpectedly rejected at %s: %s", f.Path, f.Detail)
	}
	if f := validatePayloadValue(nil, schema); f != nil {
		t.Fatalf("null unexpectedly rejected: %s", f.Detail)
	}
	f := validatePayloadValue(1, schema)
	if f == nil {
		t.Fatal("an integer was unexpectedly accepted")
	}
	want := pdetailExpectedTypes([]string{"string", "null"}, "integer")
	if f.Detail != want {
		t.Errorf("detail:\n got %q\nwant %q", f.Detail, want)
	}
}

func TestPayloadMagnitudeGuardOnAGoInt64(t *testing.T) {
	if f := validatePayloadValue(int64(1)<<53, map[string]interface{}{}); f != nil {
		t.Fatalf("2^53 unexpectedly rejected: %s", f.Detail)
	}
	f := validatePayloadValue(int64(1)<<53+1, map[string]interface{}{})
	if f == nil {
		t.Fatal("2^53+1 unexpectedly accepted")
	}
	if f.Detail != pdetailMagnitude {
		t.Errorf("detail: got %q", f.Detail)
	}
	f = validatePayloadValue(uint64(math.MaxUint64), map[string]interface{}{})
	if f == nil || f.Detail != pdetailMagnitude {
		t.Errorf("MaxUint64: got %v", f)
	}
	f = validatePayloadValue(int64(math.MinInt64), map[string]interface{}{})
	if f == nil || f.Detail != pdetailMagnitude {
		t.Errorf("MinInt64: got %v", f)
	}
}

// ---------------------------------------------------------------------------
// Registration and emission wiring
// ---------------------------------------------------------------------------

func TestPayloadSchemaRegistrationPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a panic")
		}
		want := `command "run": payload schema is invalid at payload_schema: ` +
			`unknown keyword "minProperties" (the closed subset is: ` +
			`additionalProperties, const, enum, items, properties, required, type)`
		if got, _ := r.(string); got != want {
			t.Errorf("panic:\n got %q\nwant %q", got, want)
		}
	}()
	app := NewApp("t", "1", "t")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly), PayloadSchema(map[string]interface{}{
		"type": "object", "minProperties": 1,
	}))
}

func TestPayloadSchemaNestedRegistrationPanicNamesItsPath(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a panic")
		}
		got, _ := r.(string)
		if !strings.Contains(got, `payload_schema.properties["a"]`) {
			t.Errorf("panic does not name the nested path: %q", got)
		}
	}()
	app := NewApp("t", "1", "t")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly), PayloadSchema(map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"a": map[string]interface{}{"type": "string", "maxLength": 3},
		},
	}))
}

func TestPayloadEmissionPanicsOnADeviatingValue(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a panic")
		}
		want := `command "run": payload does not satisfy the declared schema ` +
			`at payload["a"]: expected type "integer", got string`
		if got, _ := r.(string); got != want {
			t.Errorf("panic:\n got %q\nwant %q", got, want)
		}
	}()
	app := NewApp("t", "1", "t")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"a": "x"})
		return Exit(0)
	}, WithEffect(EffectReadOnly), PayloadSchema(map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"a": map[string]interface{}{"type": "integer"}},
	}))
	app.Test([]string{"run", "--json"})
}

func TestPayloadHumanModeNeverValidatesTheValue(t *testing.T) {
	// §19.4's call-unconditionally rule: a handler builds its payload the same
	// way in both modes, so a value machine mode could not carry must not fail
	// a run that was never going to emit an envelope.
	app := NewApp("t", "1", "t")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"a": "x"})
		return Exit(0)
	}, WithEffect(EffectReadOnly), PayloadSchema(map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"a": map[string]interface{}{"type": "integer"}},
	}))
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("exit code %d, stderr %q", r.ExitCode, r.Stderr)
	}
}

func TestPayloadSlotIsTakenByTheFirstCallEvenWhenItDeviates(t *testing.T) {
	// The one-slot rule (§19.4) is a call-time rule and is independent of the
	// value: the second call is refused whatever the first one carried.
	app := NewApp("t", "1", "t")
	var second string
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"a": 1})
		func() {
			defer func() {
				if r := recover(); r != nil {
					second, _ = r.(string)
				}
			}()
			ctx.Payload([]interface{}{1, 2})
		}()
		return Exit(0)
	}, WithEffect(EffectReadOnly), PayloadSchema(map[string]interface{}{"type": "array"}))
	func() {
		defer func() { _ = recover() }()
		app.Test([]string{"run"})
	}()
	if second != errPayloadAlreadySet("run") {
		t.Fatalf("second call panic: got %q, want %q", second, errPayloadAlreadySet("run"))
	}
}

func TestPayloadEmissionAcceptsAMatchingValue(t *testing.T) {
	app := NewApp("t", "1", "t")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"a": 1})
		return Exit(0)
	}, WithEffect(EffectReadOnly), PayloadSchema(map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"a": map[string]interface{}{"type": "integer"}},
	}))
	r := app.Test([]string{"run", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("exit code: got %d, stderr %q", r.ExitCode, r.Stderr)
	}
}

// ---------------------------------------------------------------------------
// The framework's own commands declare schemas their payloads satisfy
// ---------------------------------------------------------------------------

func TestCheckPayloadSatisfiesItsDeclaration(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "checks.toml")
	body := "app = \"t\"\n\n[checks.one]\ndescription = \"Checks one\"\nsubject = \"quality\"\ntags = [\"a\"]\nseverity = \"error\"\n" +
		"fast = true\npure = true\nneeds_network = false\ndepends_on = []\n"
	if err := os.WriteFile(tomlPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	app := NewApp("t", "1", "t", WithChecks(tomlPath))
	app.RegisterErrorCheck("one", func(ctx CheckContext, r *ErrorReporter) CheckOutcome {
		return r.Passed("ok")
	})
	r := app.Test([]string{"check", "--list", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("exit code %d, stderr %q", r.ExitCode, r.Stderr)
	}
	var env map[string]interface{}
	dec := json.NewDecoder(strings.NewReader(r.Stdout))
	dec.UseNumber()
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if f := validatePayloadValue(env["payload"], checkPayloadSchema); f != nil {
		t.Fatalf("check payload violates its declaration at %s: %s", f.Path, f.Detail)
	}
}

// ---------------------------------------------------------------------------
// Builder sugar (contract §19.5, decision 14)
// ---------------------------------------------------------------------------

type builderConstruct struct {
	Name    string          `json:"name"`
	Literal json.RawMessage `json:"literal"`
}

type builderDoc struct {
	ConstructCount int                `json:"construct_count"`
	Constructs     []builderConstruct `json:"constructs"`
}

func buildersPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..",
		"conformance", "payload_schema_builders.json")
}

// buildConstruct constructs one fixture entry through the builders.
func buildConstruct(t *testing.T, name string) map[string]interface{} {
	t.Helper()
	switch name {
	case "type: one name":
		return SchemaType("string")
	case "type: a list for nullability":
		return SchemaType("string", "null")
	case "type: every json type":
		return SchemaType("array", "boolean", "integer", "null", "number", "object", "string")
	case "array: items":
		return SchemaArray(SchemaType("integer"))
	case "array: items is itself a built object":
		return SchemaArray(SchemaObject(map[string]interface{}{
			"a": SchemaType("string"),
		}, nil, nil))
	case "object: bare":
		return SchemaObject(nil, nil, nil)
	case "object: properties only":
		return SchemaObject(map[string]interface{}{
			"a": SchemaType("string"),
			"b": SchemaType("integer"),
		}, nil, nil)
	case "object: properties and required":
		return SchemaObject(map[string]interface{}{
			"a": SchemaType("string"),
		}, []string{"a"}, nil)
	case "object: closed":
		return SchemaObject(map[string]interface{}{
			"a": SchemaType("string"),
		}, []string{"a"}, false)
	case "object: open by declaration":
		return SchemaObject(nil, nil, true)
	case "object: a dynamic-key map":
		return SchemaObject(nil, nil, SchemaType("number"))
	case "object: empty required":
		return SchemaObject(nil, []string{}, nil)
	case "enum: strings":
		return SchemaEnum("pass", "fail", "warn")
	case "enum: mixed json values":
		return SchemaEnum("a", 1, nil, true)
	case "const: a scalar":
		return SchemaConst("fixed")
	case "const: a composite":
		return SchemaConst(map[string]interface{}{"a": []interface{}{1, 2}})
	}
	t.Fatalf("no builder mapping for fixture %q", name)
	return nil
}

func TestPayloadSchemaBuilders(t *testing.T) {
	data, err := os.ReadFile(buildersPath(t))
	if err != nil {
		t.Fatalf("read builders fixture: %v", err)
	}
	var doc builderDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse builders fixture: %v", err)
	}
	if len(doc.Constructs) != doc.ConstructCount {
		t.Fatalf("count mismatch: header says %d, got %d",
			doc.ConstructCount, len(doc.Constructs))
	}
	for _, c := range doc.Constructs {
		t.Run(c.Name, func(t *testing.T) {
			built := buildConstruct(t, c.Name)
			// Compare the emitted documents: the literal is the canonical
			// artifact, and what the builder produces must BE that literal.
			normalized, ok := normalizePayload(built)
			if !ok {
				t.Fatal("the built literal is not representable")
			}
			want := decodeVectorJSON(t, c.Literal)
			if !payloadDeepEqual(normalized, want) {
				gotJSON, _ := json.Marshal(built)
				t.Fatalf("built literal:\n got %s\nwant %s", gotJSON, string(c.Literal))
			}
			// One-to-one onto the closed subset: nothing a builder emits is
			// outside the vocabulary, and the result is a legal declaration.
			for key := range built {
				if !isPayloadKeyword(key) {
					t.Errorf("builder emitted a keyword outside the subset: %q", key)
				}
			}
			if f := validatePayloadSchemaLiteral(built); f != nil {
				t.Errorf("built literal is not a legal declaration at %s: %s", f.Path, f.Detail)
			}
		})
	}
}

// --- config show with an unrepresentable config value ----------------------
//
// The case that moved instance validation to the emission seam. A config file
// may legitimately hold a float above 2^53, which §19.5's magnitude guard
// refuses. The human rendering must be unaffected -- and it is, because the
// guard runs only where the envelope is written.

func configShowBigFloatApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{"size": 1e17}`), 0o644); err != nil {
		t.Fatal(err)
	}
	app := NewApp("t", "1", "t", WithConfig(), WithConfigPath(configFile))
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly),
		WithFlags(FloatFlag("size", "how big", Default(0.0))))
	return app
}

func TestConfigShowHumanRenderingSurvivesAnUnrepresentableValue(t *testing.T) {
	r := configShowBigFloatApp(t).Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("exit code %d, stderr %q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "size = 100000000000000000.0  (source: config)") {
		t.Fatalf("stdout: %q", r.Stdout)
	}
	if r.Stderr != "" {
		t.Fatalf("stderr: %q", r.Stderr)
	}
}

func TestConfigShowMachineModeRefusesAnUnrepresentableValue(t *testing.T) {
	app := configShowBigFloatApp(t)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a panic")
		}
		want := `command "show": payload does not satisfy the declared schema ` +
			`at payload["size"]["value"]: the number's magnitude exceeds 2^53 ` +
			`(declare a big identifier as a string)`
		if got, _ := r.(string); got != want {
			t.Errorf("panic:\n got %q\nwant %q", got, want)
		}
	}()
	app.Test([]string{"config", "show", "--json"})
}

func TestABuilderDoesNotValidateOnItsOwn(t *testing.T) {
	// A builder is a constructor, not a check: an illegal type name is a legal
	// literal to build and a registration-time hard error to declare.
	built := SchemaType("strng")
	f := validatePayloadSchemaLiteral(built)
	if f == nil {
		t.Fatal("unexpectedly accepted")
	}
	if !strings.HasPrefix(f.Detail, `unknown type "strng"`) {
		t.Errorf("detail: got %q", f.Detail)
	}
}

func TestBuiltSchemasAreDeclarable(t *testing.T) {
	app := NewApp("t", "1", "t")
	app.Command("run", "run", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"a": 1})
		return Exit(0)
	}, WithEffect(EffectReadOnly), PayloadSchema(SchemaObject(
		map[string]interface{}{"a": SchemaType("integer")},
		[]string{"a"},
		false,
	)))
	r := app.Test([]string{"run", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("exit code %d, stderr %q", r.ExitCode, r.Stderr)
	}
}
