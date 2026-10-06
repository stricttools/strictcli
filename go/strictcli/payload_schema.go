package strictcli

// The declared payload schema's validator (effects contract §19.5).
//
// Two duties over one deliberately closed subset:
//
//   - registration-time validation of the declared literal -- an unknown
//     keyword anywhere is a hard error, which is what keeps the subset closed;
//   - emission-time validation of the value a handler supplies through
//     Context.Payload -- a payload that deviates from its declaration fails
//     here rather than shipping a wrong shape.
//
// Every detail string in this file is byte-identical to the Python and
// TypeScript validators'. errors.go carries the two OUTER templates
// (errPayloadSchemaInvalid, errPayloadInvalid); the details are pinned by the
// vectors at testdata/payload_schema_vectors.json, which the Python and
// TypeScript suites replay too.
//
// Go's one structural asymmetry, deliberate: a Go handler's natural payload is
// a typed struct or a typed slice, and encoding/json's tags are the only thing
// that says what those emit as. So BOTH the value and the declared literal are
// normalized THROUGH encoding/json before validation -- the validator sees
// exactly the document that will be written, not the Go value producing it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// payloadSchemaKeywords is the closed subset, in the order the "unknown
// keyword" message lists it.
var payloadSchemaKeywords = []string{
	"additionalProperties", "const", "enum", "items", "properties",
	"required", "type",
}

// payloadJSONTypes is the set of JSON Schema type names the subset admits,
// sorted.
var payloadJSONTypes = []string{
	"array", "boolean", "integer", "null", "number", "object", "string",
}

// payloadMaxMagnitude is decision 16's guard. Every IEEE-754 double whose
// magnitude exceeds 2^53 is already an integer (the spacing between
// representable doubles is at least 1 from 2^52 upward), so "any integer above
// 2^53" and "any number above 2^53" are the same set -- which is why the guard
// is a plain magnitude test.
const payloadMaxMagnitude = float64(1 << 53)

const (
	pdetailNotJSON         = "the value is not representable in JSON"
	pdetailMagnitude       = "the number's magnitude exceeds 2^53 (declare a big identifier as a string)"
	pdetailTypeShape       = `"type" must be a string or an array of strings`
	pdetailTypeEmpty       = `"type" must not be an empty array`
	pdetailPropertiesShape = `"properties" must be an object`
	pdetailRequiredShape   = `"required" must be an array of strings`
	pdetailEnumShape       = `"enum" must be a non-empty array`
	pdetailAddPropsShape   = `"additionalProperties" must be a boolean or a schema object`
	pdetailEnumMismatch    = "the value is not one of the declared enum values"
	pdetailConstMismatch   = "the value does not equal the declared const"
)

// payloadFinding is one violation: where it is and what rule it broke. A nil
// *payloadFinding means "no violation".
type payloadFinding struct {
	Path   string
	Detail string
}

func newFinding(path, detail string) *payloadFinding {
	return &payloadFinding{Path: path, Detail: detail}
}

// payloadQuote applies §19.5's escaping regime to one string: escape exactly
// what JSON mandates and emit everything else literally. Python reaches the
// same rule through json.dumps(ensure_ascii=False); TypeScript hand-rolls it
// the same way this does. encoding/json is deliberately NOT used here -- it
// escapes U+2028/U+2029 and (without SetEscapeHTML(false)) HTML specials,
// neither of which JSON mandates.
func payloadQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				b.WriteString(fmt.Sprintf(`\u%04x`, r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func pdetailUnknownKeyword(kw string) string {
	return fmt.Sprintf("unknown keyword %s (the closed subset is: %s)",
		payloadQuote(kw), strings.Join(payloadSchemaKeywords, ", "))
}

func pdetailUnknownType(t string) string {
	return fmt.Sprintf("unknown type %s (the JSON Schema types are: %s)",
		payloadQuote(t), strings.Join(payloadJSONTypes, ", "))
}

func pdetailSchemaNotObject(got string) string {
	return "a schema must be an object, got " + got
}

func pdetailTypeDuplicate(t string) string {
	return fmt.Sprintf(`"type" has a duplicate entry %s`, payloadQuote(t))
}

func pdetailRequiredDuplicate(k string) string {
	return fmt.Sprintf(`"required" has a duplicate entry %s`, payloadQuote(k))
}

func pdetailExpectedType(declared string, got string) string {
	return fmt.Sprintf("expected type %s, got %s", payloadQuote(declared), got)
}

func pdetailExpectedTypes(declared []string, got string) string {
	quoted := make([]string, len(declared))
	for i, t := range declared {
		quoted[i] = payloadQuote(t)
	}
	return fmt.Sprintf("expected type [%s], got %s", strings.Join(quoted, ", "), got)
}

func pdetailRequiredMissing(k string) string {
	return fmt.Sprintf("required property %s is missing", payloadQuote(k))
}

func pdetailNotPermitted(k string) string {
	return fmt.Sprintf("property %s is not permitted (additionalProperties is false)",
		payloadQuote(k))
}

func payloadPathKey(path, key string) string {
	return path + "[" + payloadQuote(key) + "]"
}

func payloadPathIndex(path string, index int) string {
	return path + "[" + strconv.Itoa(index) + "]"
}

// payloadSortedKeys returns a map's keys in code-point order, which is what
// makes the traversal deterministic. Go map iteration is deliberately
// randomized, so without this the reported violation would differ run to run --
// and from the two siblings, whose maps preserve insertion order.
func payloadSortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// normalizePayload renders a Go value as the JSON document it will be emitted
// as, then reads it back as a generic tree. Numbers come back as json.Number,
// so the exact literal survives for the magnitude guard.
func normalizePayload(v interface{}) (interface{}, bool) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, false
	}
	dec := json.NewDecoder(&buf)
	dec.UseNumber()
	var out interface{}
	if err := dec.Decode(&out); err != nil {
		return nil, false
	}
	return out, true
}

// payloadKind reports the JSON kind of a normalized value, or "" when it is
// not representable.
//
// "integer" is reported for any number with a zero fractional part, which is
// JSON Schema's own reading of the type and the only one three languages can
// agree on -- TypeScript has no separate integer type at all.
func payloadKind(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		f, err := strconv.ParseFloat(v.String(), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return ""
		}
		if f == math.Trunc(f) {
			return "integer"
		}
		return "number"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	}
	return ""
}

// payloadNumber returns a normalized value's numeric value, and whether it is
// a number at all.
func payloadNumber(value interface{}) (float64, bool) {
	if n, ok := value.(json.Number); ok {
		f, err := strconv.ParseFloat(n.String(), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// payloadOverMagnitude reports whether a number exceeds decision 16's guard.
//
// The exact decimal literal is consulted first: 2^53+1 parses to exactly 2^53
// as a float64, so a float-only test would let the one integer the guard
// exists to catch slip through.
func payloadOverMagnitude(value interface{}) bool {
	n, ok := value.(json.Number)
	if !ok {
		return false
	}
	if i, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
		return i > (1<<53) || i < -(1<<53)
	}
	f, ok := payloadNumber(value)
	if !ok {
		return false
	}
	return math.Abs(f) > payloadMaxMagnitude
}

// payloadScanValue is the document check: representability and the magnitude
// guard, recursively, over the WHOLE value before any keyword is consulted.
// A payload that could not be emitted at all is reported as that rather than
// as a type mismatch. Arrays traverse in index order, objects in sorted-key
// order.
func payloadScanValue(value interface{}, path string) *payloadFinding {
	kind := payloadKind(value)
	if kind == "" {
		return newFinding(path, pdetailNotJSON)
	}
	if payloadOverMagnitude(value) {
		return newFinding(path, pdetailMagnitude)
	}
	switch kind {
	case "array":
		for i, item := range value.([]interface{}) {
			if f := payloadScanValue(item, payloadPathIndex(path, i)); f != nil {
				return f
			}
		}
	case "object":
		obj := value.(map[string]interface{})
		for _, key := range payloadSortedKeys(obj) {
			if f := payloadScanValue(obj[key], payloadPathKey(path, key)); f != nil {
				return f
			}
		}
	}
	return nil
}

// payloadDeepEqual is JSON-value equality, used by enum and const.
//
// Type-aware on purpose: a boolean is never equal to a number, and two numbers
// are equal when their values are, so 1 matches a declared 1.0.
func payloadDeepEqual(a, b interface{}) bool {
	ka, kb := payloadKind(a), payloadKind(b)
	if ka == "" || kb == "" {
		return false
	}
	if (ka == "integer" || ka == "number") && (kb == "integer" || kb == "number") {
		fa, _ := payloadNumber(a)
		fb, _ := payloadNumber(b)
		return fa == fb
	}
	if ka != kb {
		return false
	}
	switch ka {
	case "null":
		return true
	case "boolean":
		return a.(bool) == b.(bool)
	case "string":
		return a.(string) == b.(string)
	case "array":
		xs, ys := a.([]interface{}), b.([]interface{})
		if len(xs) != len(ys) {
			return false
		}
		for i := range xs {
			if !payloadDeepEqual(xs[i], ys[i]) {
				return false
			}
		}
		return true
	}
	xs, ys := a.(map[string]interface{}), b.(map[string]interface{})
	if len(xs) != len(ys) {
		return false
	}
	for k, xv := range xs {
		yv, ok := ys[k]
		if !ok || !payloadDeepEqual(xv, yv) {
			return false
		}
	}
	return true
}

func payloadTypeMatches(declared, kind string) bool {
	switch declared {
	case "integer":
		return kind == "integer"
	case "number":
		return kind == "integer" || kind == "number"
	}
	return declared == kind
}

func isPayloadKeyword(kw string) bool {
	for _, k := range payloadSchemaKeywords {
		if k == kw {
			return true
		}
	}
	return false
}

func isPayloadJSONType(t string) bool {
	for _, k := range payloadJSONTypes {
		if k == t {
			return true
		}
	}
	return false
}

// payloadStringList narrows a normalized array-of-strings.
func payloadStringList(v interface{}) ([]string, bool) {
	xs, ok := v.([]interface{})
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// validatePayloadSchemaLiteral is the registration-time duty (§19.5): one
// declared schema literal, normalized through encoding/json and validated over
// the closed subset. The keyword scan is sorted, so which of several
// violations is reported never depends on a map's iteration order.
func validatePayloadSchemaLiteral(schema map[string]interface{}) *payloadFinding {
	normalized, ok := normalizePayload(schema)
	if !ok {
		return newFinding("payload_schema", pdetailNotJSON)
	}
	return validatePayloadSchema(normalized, "payload_schema")
}

func validatePayloadSchema(schema interface{}, path string) *payloadFinding {
	obj, ok := schema.(map[string]interface{})
	if !ok {
		got := payloadKind(schema)
		if got == "" {
			got = "unsupported"
		}
		return newFinding(path, pdetailSchemaNotObject(got))
	}

	for _, kw := range payloadSortedKeys(obj) {
		if !isPayloadKeyword(kw) {
			return newFinding(path, pdetailUnknownKeyword(kw))
		}
	}

	if t, present := obj["type"]; present {
		if s, isStr := t.(string); isStr {
			if !isPayloadJSONType(s) {
				return newFinding(path, pdetailUnknownType(s))
			}
		} else if list, isList := t.([]interface{}); isList {
			if len(list) == 0 {
				return newFinding(path, pdetailTypeEmpty)
			}
			names, allStrings := payloadStringList(t)
			if !allStrings {
				return newFinding(path, pdetailTypeShape)
			}
			seen := make([]string, 0, len(names))
			for _, n := range names {
				for _, prev := range seen {
					if prev == n {
						return newFinding(path, pdetailTypeDuplicate(n))
					}
				}
				seen = append(seen, n)
			}
			for _, n := range names {
				if !isPayloadJSONType(n) {
					return newFinding(path, pdetailUnknownType(n))
				}
			}
		} else {
			return newFinding(path, pdetailTypeShape)
		}
	}

	if req, present := obj["required"]; present {
		names, ok := payloadStringList(req)
		if !ok {
			return newFinding(path, pdetailRequiredShape)
		}
		seen := make([]string, 0, len(names))
		for _, n := range names {
			for _, prev := range seen {
				if prev == n {
					return newFinding(path, pdetailRequiredDuplicate(n))
				}
			}
			seen = append(seen, n)
		}
	}

	if e, present := obj["enum"]; present {
		values, ok := e.([]interface{})
		if !ok || len(values) == 0 {
			return newFinding(path, pdetailEnumShape)
		}
		for i, entry := range values {
			if f := payloadScanValue(entry, payloadPathIndex(path+".enum", i)); f != nil {
				return f
			}
		}
	}

	if c, present := obj["const"]; present {
		if f := payloadScanValue(c, path+".const"); f != nil {
			return f
		}
	}

	if p, present := obj["properties"]; present {
		props, ok := p.(map[string]interface{})
		if !ok {
			return newFinding(path, pdetailPropertiesShape)
		}
		for _, key := range payloadSortedKeys(props) {
			if f := validatePayloadSchema(props[key], payloadPathKey(path+".properties", key)); f != nil {
				return f
			}
		}
	}

	if it, present := obj["items"]; present {
		if f := validatePayloadSchema(it, path+".items"); f != nil {
			return f
		}
	}

	if ap, present := obj["additionalProperties"]; present {
		if _, isBool := ap.(bool); !isBool {
			sub, isObj := ap.(map[string]interface{})
			if !isObj {
				return newFinding(path, pdetailAddPropsShape)
			}
			if f := validatePayloadSchema(sub, path+".additionalProperties"); f != nil {
				return f
			}
		}
	}

	return nil
}

// validatePayloadInstance is the keyword half of the emission-time duty.
//
// Check order is pinned so that a value violating several constraints always
// reports the same one: type, then const, then enum, then (for an object)
// required, declared properties in sorted key order, and finally the
// additional properties in sorted key order; then (for an array) the items.
func validatePayloadInstance(value interface{}, schema map[string]interface{}, path string) *payloadFinding {
	kind := payloadKind(value)
	if kind == "" {
		return newFinding(path, pdetailNotJSON)
	}

	if t, present := schema["type"]; present {
		if s, isStr := t.(string); isStr {
			if !payloadTypeMatches(s, kind) {
				return newFinding(path, pdetailExpectedType(s, kind))
			}
		} else {
			names, _ := payloadStringList(t)
			matched := false
			for _, n := range names {
				if payloadTypeMatches(n, kind) {
					matched = true
					break
				}
			}
			if !matched {
				return newFinding(path, pdetailExpectedTypes(names, kind))
			}
		}
	}

	if c, present := schema["const"]; present {
		if !payloadDeepEqual(value, c) {
			return newFinding(path, pdetailConstMismatch)
		}
	}

	if e, present := schema["enum"]; present {
		values, _ := e.([]interface{})
		matched := false
		for _, entry := range values {
			if payloadDeepEqual(value, entry) {
				matched = true
				break
			}
		}
		if !matched {
			return newFinding(path, pdetailEnumMismatch)
		}
	}

	if kind == "object" {
		obj := value.(map[string]interface{})
		props, hasProps := schema["properties"].(map[string]interface{})
		if names, ok := payloadStringList(schema["required"]); ok {
			for _, name := range names {
				if _, present := obj[name]; !present {
					return newFinding(path, pdetailRequiredMissing(name))
				}
			}
		}
		if hasProps {
			for _, key := range payloadSortedKeys(props) {
				child, present := obj[key]
				if !present {
					continue
				}
				sub, ok := props[key].(map[string]interface{})
				if !ok {
					continue
				}
				if f := validatePayloadInstance(child, sub, payloadPathKey(path, key)); f != nil {
					return f
				}
			}
		}
		if ap, declared := schema["additionalProperties"]; declared {
			allow, isBool := ap.(bool)
			if !isBool || !allow {
				sub, isObj := ap.(map[string]interface{})
				for _, key := range payloadSortedKeys(obj) {
					if hasProps {
						if _, isDeclared := props[key]; isDeclared {
							continue
						}
					}
					if isBool {
						return newFinding(path, pdetailNotPermitted(key))
					}
					if !isObj {
						continue
					}
					if f := validatePayloadInstance(obj[key], sub, payloadPathKey(path, key)); f != nil {
						return f
					}
				}
			}
		}
	}

	if kind == "array" {
		if it, present := schema["items"]; present {
			if sub, isObj := it.(map[string]interface{}); isObj {
				for i, item := range value.([]interface{}) {
					if f := validatePayloadInstance(item, sub, payloadPathIndex(path, i)); f != nil {
						return f
					}
				}
			}
		}
	}

	return nil
}

// validatePayloadValue is the whole emission-time duty: normalize through
// encoding/json, run the document check, then the keywords.
func validatePayloadValue(value interface{}, schema map[string]interface{}) *payloadFinding {
	normalized, ok := normalizePayload(value)
	if !ok {
		// encoding/json refused the value outright (a NaN, a channel, a
		// cycle). The offending node sits inside a marshaler this package
		// cannot see into, so the whole payload is named.
		return newFinding("payload", pdetailNotJSON)
	}
	if f := payloadScanValue(normalized, "payload"); f != nil {
		return f
	}
	normalizedSchema, ok := normalizePayload(schema)
	if !ok {
		return newFinding("payload", pdetailNotJSON)
	}
	schemaObj, _ := normalizedSchema.(map[string]interface{})
	return validatePayloadInstance(normalized, schemaObj, "payload")
}

// ---------------------------------------------------------------------------
// Builder sugar for the declared payload schema (contract §19.5, decision 14)
//
// Pure constructors of literals, and nothing more. They add no vocabulary and
// no semantics: each one produces exactly the map an author could have
// written, that map is the canonical artifact, and it passes the identical
// registration-time validation. Go has no literal ergonomics to speak of --
// map[string]interface{} nested four deep is unreadable -- so this is where the
// compile-time help comes back.
//
// None of them validates anything: an unknown type name written through
// SchemaType is rejected at registration exactly as a hand-written literal
// would be. The one-to-one mapping onto the closed subset is pinned by
// testdata/payload_schema_builders.json.
// ---------------------------------------------------------------------------

// SchemaType builds {"type": ...}: one name, or a list of them for
// nullability.
func SchemaType(names ...string) map[string]interface{} {
	if len(names) == 1 {
		return map[string]interface{}{"type": names[0]}
	}
	entries := make([]interface{}, len(names))
	for i, n := range names {
		entries[i] = n
	}
	return map[string]interface{}{"type": entries}
}

// SchemaArray builds {"type": "array", "items": ...}.
func SchemaArray(items map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"type": "array", "items": items}
}

// SchemaObject builds {"type": "object", ...}.
//
// Each keyword is emitted only when supplied: nil properties, nil required and
// nil additionalProperties each omit their keyword, so SchemaObject(nil, nil,
// nil) is the bare {"type": "object"}. An omitted additionalProperties means
// the keyword is absent rather than true -- the same behaviour, not the same
// declaration.
//
// additionalProperties takes the boolean-or-schema union the subset admits:
// pass false, true, or a map.
func SchemaObject(
	properties map[string]interface{},
	required []string,
	additionalProperties interface{},
) map[string]interface{} {
	out := map[string]interface{}{"type": "object"}
	if properties != nil {
		out["properties"] = properties
	}
	if required != nil {
		entries := make([]interface{}, len(required))
		for i, n := range required {
			entries[i] = n
		}
		out["required"] = entries
	}
	if additionalProperties != nil {
		out["additionalProperties"] = additionalProperties
	}
	return out
}

// SchemaEnum builds {"enum": [...]}.
func SchemaEnum(values ...interface{}) map[string]interface{} {
	entries := make([]interface{}, len(values))
	copy(entries, values)
	return map[string]interface{}{"enum": entries}
}

// SchemaConst builds {"const": ...}.
func SchemaConst(value interface{}) map[string]interface{} {
	return map[string]interface{}{"const": value}
}
