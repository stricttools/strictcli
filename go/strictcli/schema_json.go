package strictcli

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The dumped schema's ordered document and its byte canon (effects contract
// §25.8, §25.9).
//
// A committed `.strictmetadata/.cli-schema/schema.json` must be DUMPER-INDEPENDENT: a repository
// whose file is written sometimes by a Go binary and sometimes by a Python one
// must see a diff exactly when something changed. Two things make that true and
// neither is `encoding/json`'s: keys are emitted in a DECLARED order (a Go map
// marshal sorts them, which is why a Go-written schema used to start with
// `commands` while a Python-written one started with `schema_version`), and the
// document is written by the canonical writer below (`json.MarshalIndent`
// escapes `<`, `>` and `&`, and renders floats through its own formatter rather
// than through the canonical float form this repo owns).

// schemaObject is an insertion-ordered JSON object. Its whole purpose is that
// serialization never sorts: §25.9's key order is the order each serializer
// sets its keys in, at every depth.
type schemaObject struct {
	keys []string
	vals map[string]interface{}
}

func newSchemaObject() *schemaObject {
	return &schemaObject{vals: map[string]interface{}{}}
}

// set appends a key, or overwrites one already present IN PLACE -- a rewrite
// keeps the position the first write gave it, which is what lets a member
// payload be serialized as an ordinary flag entry and then renamed without
// disturbing §25.9's order.
func (o *schemaObject) set(key string, value interface{}) *schemaObject {
	if _, seen := o.vals[key]; !seen {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = value
	return o
}

func (o *schemaObject) get(key string) interface{} {
	return o.vals[key]
}

// delete removes a key; removing an absent key does nothing. The help
// document's slices prune the command tree with it.
func (o *schemaObject) delete(key string) {
	if _, seen := o.vals[key]; !seen {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			return
		}
	}
}

// insertAfter places a new key immediately after an existing one. project_id is
// the only key that needs it: it is added by the file-writer path and sits
// immediately after `defaults`, so removing it leaves the CWD-free core
// byte-identical (§25.9).
func (o *schemaObject) insertAfter(existing, key string, value interface{}) *schemaObject {
	if _, seen := o.vals[key]; seen {
		o.vals[key] = value
		return o
	}
	o.vals[key] = value
	for i, k := range o.keys {
		if k == existing {
			o.keys = append(o.keys[:i+1], append([]string{key}, o.keys[i+1:]...)...)
			return o
		}
	}
	o.keys = append(o.keys, key)
	return o
}

// toPlain converts an ordered document into the plain Go maps DumpSchemaDict
// returns. Order is a property of the WRITTEN document; a Go map has none, and
// a caller reading the map reads fields by name.
func toPlain(value interface{}) interface{} {
	switch v := value.(type) {
	case *schemaObject:
		m := make(map[string]interface{}, len(v.keys))
		for _, k := range v.keys {
			m[k] = toPlain(v.vals[k])
		}
		return m
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, e := range v {
			out[i] = toPlain(e)
		}
		return out
	default:
		return value
	}
}

// canonicalJSONString escapes one string exactly as the canon mandates: `"` and
// `\` are escaped, control characters below U+0020 use JSON's short escapes
// where one exists and `\u00XX` otherwise -- and NOTHING else is escaped.
// Non-ASCII is raw UTF-8, the HTML-significant characters `<`, `>` and `&` are
// literal, and `/` is never escaped (§25.8).
func canonicalJSONString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			if r == utf8.RuneError {
				// A byte sequence that is not valid UTF-8 reaches here as the
				// replacement rune; emitting it literally is what keeps the
				// document valid UTF-8.
				b.WriteRune(r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// writeCanonicalJSON appends one value's canonical encoding to b.
func writeCanonicalJSON(value interface{}, depth int, b *strings.Builder) error {
	switch v := value.(type) {
	case nil:
		b.WriteString("null")
		return nil
	case *schemaObject:
		return writeCanonicalMembers(v.keys, func(k string) interface{} { return v.vals[k] }, depth, b)
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		return nil
	case string:
		b.WriteString(canonicalJSONString(v))
		return nil
	case float64:
		// Every float goes through the canonical float form this repo owns --
		// the same one the three implementations share byte-for-byte.
		b.WriteString(formatFloatCanonical(v))
		return nil
	case float32:
		b.WriteString(formatFloatCanonical(float64(v)))
		return nil
	case int:
		b.WriteString(strconv.Itoa(v))
		return nil
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
		return nil
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		if rv.Len() == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[\n")
		inner := strings.Repeat("  ", depth+1)
		for i := 0; i < rv.Len(); i++ {
			b.WriteString(inner)
			if err := writeCanonicalJSON(rv.Index(i).Interface(), depth+1, b); err != nil {
				return err
			}
			if i < rv.Len()-1 {
				b.WriteString(",\n")
			} else {
				b.WriteString("\n")
			}
		}
		b.WriteString(strings.Repeat("  ", depth) + "]")
		return nil
	case reflect.Map:
		// A plain Go map reaches the writer only from a value the framework did
		// not order itself: a registered payload-schema literal, a dict-typed
		// default, a RelativeToRoot marker. A Go map holds no order, so its keys
		// are sorted -- which is what §13 means by "verbatim" being a promise
		// about content rather than about bytes.
		if rv.Len() == 0 {
			b.WriteString("{}")
			return nil
		}
		keys := make([]string, 0, rv.Len())
		byKey := map[string]interface{}{}
		for _, mk := range rv.MapKeys() {
			if mk.Kind() != reflect.String {
				return errSchemaValueUnserializable(value)
			}
			keys = append(keys, mk.String())
			byKey[mk.String()] = rv.MapIndex(mk).Interface()
		}
		sort.Strings(keys)
		return writeCanonicalMembers(keys, func(k string) interface{} { return byKey[k] }, depth, b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b.WriteString(strconv.FormatInt(rv.Int(), 10))
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		b.WriteString(strconv.FormatUint(rv.Uint(), 10))
		return nil
	case reflect.Float32, reflect.Float64:
		b.WriteString(formatFloatCanonical(rv.Float()))
		return nil
	case reflect.Bool:
		if rv.Bool() {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		return nil
	case reflect.String:
		b.WriteString(canonicalJSONString(rv.String()))
		return nil
	}
	return errSchemaValueUnserializable(value)
}

// writeCanonicalMembers renders one object's members: two-space indent, one
// member per line, `": "` between a key and its value, and an empty object
// inline as `{}`.
func writeCanonicalMembers(keys []string, value func(string) interface{}, depth int, b *strings.Builder) error {
	if len(keys) == 0 {
		b.WriteString("{}")
		return nil
	}
	b.WriteString("{\n")
	inner := strings.Repeat("  ", depth+1)
	for i, k := range keys {
		b.WriteString(inner)
		b.WriteString(canonicalJSONString(k))
		b.WriteString(": ")
		if err := writeCanonicalJSON(value(k), depth+1, b); err != nil {
			return err
		}
		if i < len(keys)-1 {
			b.WriteString(",\n")
		} else {
			b.WriteString("\n")
		}
	}
	b.WriteString(strings.Repeat("  ", depth) + "}")
	return nil
}

// canonicalJSON is the dumper-independent encoding of a whole schema document
// (§25.8). The trailing newline is the writer's, not this function's.
func canonicalJSON(value interface{}) (string, error) {
	var b strings.Builder
	if err := writeCanonicalJSON(value, 0, &b); err != nil {
		return "", err
	}
	return b.String(), nil
}
