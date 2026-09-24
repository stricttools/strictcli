package strictcli

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// --- Type system tests ---

func TestListOfPanicsOnBool(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for ListOf(TypeBool)")
		}
	}()
	ListOf(TypeBool)
}

func TestDictOfPanicsOnBool(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for DictOf(TypeBool)")
		}
	}()
	DictOf(TypeBool)
}

func TestTypePredicates(t *testing.T) {
	cases := []struct {
		t      FlagType
		scalar bool
		list   bool
		dict   bool
	}{
		{TypeStr, true, false, false},
		{TypeBool, true, false, false},
		{TypeInt, true, false, false},
		{TypeFloat, true, false, false},
		{TypeListStr, false, true, false},
		{TypeListInt, false, true, false},
		{TypeListFloat, false, true, false},
		{TypeDictStr, false, false, true},
		{TypeDictInt, false, false, true},
		{TypeDictFloat, false, false, true},
	}
	for _, c := range cases {
		if IsScalarType(c.t) != c.scalar {
			t.Errorf("IsScalarType(%d) = %v, want %v", c.t, !c.scalar, c.scalar)
		}
		if IsListType(c.t) != c.list {
			t.Errorf("IsListType(%d) = %v, want %v", c.t, !c.list, c.list)
		}
		if IsDictType(c.t) != c.dict {
			t.Errorf("IsDictType(%d) = %v, want %v", c.t, !c.dict, c.dict)
		}
	}
}

func TestItemType(t *testing.T) {
	cases := []struct {
		t    FlagType
		item FlagType
	}{
		{TypeListStr, TypeStr},
		{TypeListInt, TypeInt},
		{TypeListFloat, TypeFloat},
		{TypeDictStr, TypeStr},
		{TypeDictInt, TypeInt},
		{TypeDictFloat, TypeFloat},
	}
	for _, c := range cases {
		if ItemType(c.t) != c.item {
			t.Errorf("ItemType(%d) = %d, want %d", c.t, ItemType(c.t), c.item)
		}
	}
}

// --- ListFlag registration tests ---

func TestListFlagRequiresUnique(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic: ListFlag without Unique()")
		}
		if !strings.Contains(r.(string), "unique") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	ListFlag(TypeStr, "items", "list of items", Default([]interface{}{}))
}

func TestListFlagCreation(t *testing.T) {
	f := ListFlag(TypeStr, "tags", "tag list", Unique(false), Default([]interface{}{}))
	if !IsListType(f.Type) {
		t.Fatal("expected list type")
	}
	if !f.Repeatable {
		t.Fatal("expected repeatable")
	}
	if ItemType(f.Type) != TypeStr {
		t.Fatal("expected str item type")
	}
}

func TestListFlagIntCreation(t *testing.T) {
	f := ListFlag(TypeInt, "ids", "id list", Unique(true), Default([]interface{}{}))
	if !IsListType(f.Type) {
		t.Fatal("expected list type")
	}
	if ItemType(f.Type) != TypeInt {
		t.Fatal("expected int item type")
	}
	if !f.Unique {
		t.Fatal("expected unique")
	}
}

// --- DictFlag registration tests ---

func TestDictFlagCreation(t *testing.T) {
	f := DictFlag(TypeStr, "headers", "HTTP headers", Unique(false), Default(map[string]interface{}{}))
	if !IsDictType(f.Type) {
		t.Fatal("expected dict type")
	}
	if !f.Repeatable {
		t.Fatal("expected repeatable")
	}
	if ItemType(f.Type) != TypeStr {
		t.Fatal("expected str value type")
	}
}

func TestDictFlagNoChoices(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic: DictFlag with Choices")
		}
		if !strings.Contains(r.(string), "choices") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	DictFlag(TypeStr, "headers", "HTTP headers", Unique(false), Choices(Ch("a", ""), Ch("b", "")), Default(map[string]interface{}{}))
}

// --- CLI parsing: list flags ---

func TestListFlagStrParsing(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeStr, "tag", "tags to apply", Unique(false), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--tag", "alpha", "--tag", "beta"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	tags := captured["tag"].([]interface{})
	if len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Fatalf("unexpected tags: %v", tags)
	}
}

func TestListFlagIntParsing(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeInt, "id", "ids to process", Unique(false), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--id", "10", "--id", "20", "--id", "30"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	ids := captured["id"].([]interface{})
	if len(ids) != 3 || ids[0] != 10 || ids[1] != 20 || ids[2] != 30 {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

func TestListFlagFloatParsing(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeFloat, "weight", "weights", Unique(false), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--weight", "1.5", "--weight", "2.7"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	ws := captured["weight"].([]interface{})
	if len(ws) != 2 || ws[0] != 1.5 || ws[1] != 2.7 {
		t.Fatalf("unexpected weights: %v", ws)
	}
}

func TestListFlagIntTypeError(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeInt, "id", "ids", Unique(false), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--id", "abc"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "expected integer") {
		t.Fatalf("expected integer error, got: %s", r.Stderr)
	}
}

func TestListFlagUnique(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeStr, "tag", "tags", Unique(true), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--tag", "a", "--tag", "a"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "duplicate") {
		t.Fatalf("expected duplicate error, got: %s", r.Stderr)
	}
}

func TestListFlagDefault(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeStr, "tag", "tags", Unique(false), Default([]interface{}{"default-tag"})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	tags := captured["tag"].([]interface{})
	if len(tags) != 1 || tags[0] != "default-tag" {
		t.Fatalf("unexpected default tags: %v", tags)
	}
}

func TestListFlagEmptyDefault(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeStr, "tag", "tags", Unique(false), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	tags := captured["tag"].([]interface{})
	if len(tags) != 0 {
		t.Fatalf("expected empty list, got: %v", tags)
	}
}

func TestListFlagEqualsSyntax(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeInt, "id", "ids", Unique(false), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--id=42", "--id=99"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	ids := captured["id"].([]interface{})
	if len(ids) != 2 || ids[0] != 42 || ids[1] != 99 {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

// --- CLI parsing: dict flags ---

func TestDictFlagStrParsing(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--header", "Content-Type=json", "--header", "Accept=text"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	headers := captured["header"].(map[string]interface{})
	if headers["Content-Type"] != "json" || headers["Accept"] != "text" {
		t.Fatalf("unexpected headers: %v", headers)
	}
}

func TestDictFlagIntParsing(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeInt, "port", "port mappings", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--port", "http=80", "--port", "https=443"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	ports := captured["port"].(map[string]interface{})
	if ports["http"] != 80 || ports["https"] != 443 {
		t.Fatalf("unexpected ports: %v", ports)
	}
}

func TestDictFlagFloatParsing(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeFloat, "rate", "rates", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--rate", "cpu=0.75", "--rate", "mem=1.5"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	rates := captured["rate"].(map[string]interface{})
	if rates["cpu"] != 0.75 || rates["mem"] != 1.5 {
		t.Fatalf("unexpected rates: %v", rates)
	}
}

func TestDictFlagJSONParsing(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--header", `{"Content-Type": "json", "Accept": "text"}`})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	headers := captured["header"].(map[string]interface{})
	if headers["Content-Type"] != "json" || headers["Accept"] != "text" {
		t.Fatalf("unexpected headers: %v", headers)
	}
}

func TestDictFlagJSONIntParsing(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeInt, "port", "port mappings", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--port", `{"http": 80, "https": 443}`})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	ports := captured["port"].(map[string]interface{})
	if ports["http"] != 80 || ports["https"] != 443 {
		t.Fatalf("unexpected ports: %v", ports)
	}
}

func TestDictFlagMissingEquals(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--header", "no-equals-here"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	// Exact wording is pinned for cross-language parity: Python and TypeScript
	// emit the same message (Go also accepts a JSON object for dict flags).
	want := "error: --header: expected key=value or JSON, got 'no-equals-here'"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("expected %q in stderr, got: %s", want, r.Stderr)
	}
}

func TestDictFlagEmptyKey(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--header", "=value"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "empty key") {
		t.Fatalf("expected empty key error, got: %s", r.Stderr)
	}
}

func TestDictFlagIntTypeError(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeInt, "port", "port mappings", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--port", "http=notanumber"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "expected integer") {
		t.Fatalf("expected integer error, got: %s", r.Stderr)
	}
}

func TestDictFlagDefault(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false),
			Default(map[string]interface{}{"X-Default": "yes"})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	headers := captured["header"].(map[string]interface{})
	if headers["X-Default"] != "yes" {
		t.Fatalf("unexpected default headers: %v", headers)
	}
}

func TestDictFlagEmptyDefault(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	headers := captured["header"].(map[string]interface{})
	if len(headers) != 0 {
		t.Fatalf("expected empty map, got: %v", headers)
	}
}

func TestDictFlagMergesMultipleEntries(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "meta", "metadata", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	// Two separate --meta flags, should merge into one map
	r := app.Test([]string{"run", "--meta", "a=1", "--meta", "b=2"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	meta := captured["meta"].(map[string]interface{})
	if meta["a"] != "1" || meta["b"] != "2" {
		t.Fatalf("unexpected meta: %v", meta)
	}
}

func TestDictFlagOverwriteKey(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "meta", "metadata", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	// Same key twice: last value wins
	r := app.Test([]string{"run", "--meta", "a=1", "--meta", "a=2"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	meta := captured["meta"].(map[string]interface{})
	if meta["a"] != "2" {
		t.Fatalf("expected overwritten value, got: %v", meta)
	}
}

func TestDictFlagEqualsSyntax(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "meta", "metadata", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--meta=key=value"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	meta := captured["meta"].(map[string]interface{})
	if meta["key"] != "value" {
		t.Fatalf("unexpected meta: %v", meta)
	}
}

func TestDictFlagValueWithEquals(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "meta", "metadata", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	// Value containing = sign
	r := app.Test([]string{"run", "--meta", "url=https://example.com?a=b"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	meta := captured["meta"].(map[string]interface{})
	if meta["url"] != "https://example.com?a=b" {
		t.Fatalf("unexpected meta: %v", meta)
	}
}

// --- Env var tests ---

func TestListFlagEnvVar(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeInt, "id", "ids", Unique(false),
			Env("TEST_IDS"), Prefixed(false), EnvSeparator(","), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	os.Setenv("TEST_IDS", "1,2,3")
	defer os.Unsetenv("TEST_IDS")

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	ids := captured["id"].([]interface{})
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("unexpected ids from env: %v", ids)
	}
}

func TestDictFlagEnvVar(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false),
			Env("TEST_HEADERS"), Prefixed(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	os.Setenv("TEST_HEADERS", `{"Content-Type": "json"}`)
	defer os.Unsetenv("TEST_HEADERS")

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	headers := captured["header"].(map[string]interface{})
	if headers["Content-Type"] != "json" {
		t.Fatalf("unexpected headers from env: %v", headers)
	}
}

func TestDictFlagEnvVarInvalidJSON(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false),
			Env("TEST_HEADERS_BAD"), Prefixed(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	os.Setenv("TEST_HEADERS_BAD", "not json")
	defer os.Unsetenv("TEST_HEADERS_BAD")

	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "invalid JSON") {
		t.Fatalf("expected JSON error, got: %s", r.Stderr)
	}
}

// --- Config tests ---

func TestListFlagConfigCoercion(t *testing.T) {
	var captured map[string]interface{}
	tmpDir := t.TempDir()
	configFile := tmpDir + "/config.json"
	os.WriteFile(configFile, []byte(`{"id": [10, 20, 30]}`), 0644)

	app := NewApp("test", "1.0.0", "test app",
		WithConfig(), WithConfigPath(configFile))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeInt, "id", "ids", Unique(false), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	ids := captured["id"].([]interface{})
	if len(ids) != 3 || ids[0] != 10 || ids[1] != 20 || ids[2] != 30 {
		t.Fatalf("unexpected ids from config: %v", ids)
	}
}

func TestDictFlagConfigCoercion(t *testing.T) {
	var captured map[string]interface{}
	tmpDir := t.TempDir()
	configFile := tmpDir + "/config.json"
	os.WriteFile(configFile, []byte(`{"header": {"X-Key": "val"}}`), 0644)

	app := NewApp("test", "1.0.0", "test app",
		WithConfig(), WithConfigPath(configFile))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	headers := captured["header"].(map[string]interface{})
	if headers["X-Key"] != "val" {
		t.Fatalf("unexpected headers from config: %v", headers)
	}
}

// --- Invoke/Call tests ---

func TestListFlagInvoke(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeInt, "id", "ids", Unique(false), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	result, err := app.Call("run", map[string]interface{}{
		"id": []interface{}{1, 2, 3},
	})
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if result.(int) != 0 {
		t.Fatalf("expected exit 0, got %v", result)
	}
	ids := captured["id"].([]interface{})
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

func TestListFlagInvokeTypedSlice(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeInt, "id", "ids", Unique(false), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	// Go caller passes []int instead of []interface{}
	result, err := app.Call("run", map[string]interface{}{
		"id": []int{10, 20},
	})
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if result.(int) != 0 {
		t.Fatalf("expected exit 0, got %v", result)
	}
	ids := captured["id"].([]interface{})
	if len(ids) != 2 || ids[0] != 10 || ids[1] != 20 {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

func TestDictFlagInvoke(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	result, err := app.Call("run", map[string]interface{}{
		"header": map[string]interface{}{"X-Key": "val"},
	})
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if result.(int) != 0 {
		t.Fatalf("expected exit 0, got %v", result)
	}
	headers := captured["header"].(map[string]interface{})
	if headers["X-Key"] != "val" {
		t.Fatalf("unexpected headers: %v", headers)
	}
}

func TestDictFlagInvokeTypedMap(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	// Go caller passes map[string]string instead of map[string]interface{}
	result, err := app.Call("run", map[string]interface{}{
		"header": map[string]string{"X-Key": "val"},
	})
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}
	if result.(int) != 0 {
		t.Fatalf("expected exit 0, got %v", result)
	}
	headers := captured["header"].(map[string]interface{})
	if headers["X-Key"] != "val" {
		t.Fatalf("unexpected headers: %v", headers)
	}
}

// --- Variadic arg with list type ---

func TestVariadicArgListInt(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("sum", "sum numbers", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithArgs(
		NewArg("numbers", "numbers to sum", Variadic(), ArgType(ListOf(TypeInt)), ArgRequired()),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"sum", "1", "2", "3"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	nums := captured["numbers"].([]interface{})
	if len(nums) != 3 || nums[0] != 1 || nums[1] != 2 || nums[2] != 3 {
		t.Fatalf("unexpected numbers: %v", nums)
	}
}

func TestVariadicArgListFloat(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("sum", "sum numbers", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithArgs(
		NewArg("weights", "weight values", Variadic(), ArgType(ListOf(TypeFloat)), ArgRequired()),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"sum", "1.5", "2.7"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	ws := captured["weights"].([]interface{})
	if len(ws) != 2 || ws[0] != 1.5 || ws[1] != 2.7 {
		t.Fatalf("unexpected weights: %v", ws)
	}
}

func TestVariadicArgListIntTypeError(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("sum", "sum numbers", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithArgs(
		NewArg("numbers", "numbers to sum", Variadic(), ArgType(ListOf(TypeInt)), ArgRequired()),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"sum", "1", "abc"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "expected integer") {
		t.Fatalf("expected integer error, got: %s", r.Stderr)
	}
}

func TestNonVariadicListArgPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic: list type on non-variadic arg")
		}
		if !strings.Contains(r.(string), "variadic") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	NewArg("items", "item list", ArgType(ListOf(TypeStr)), ArgRequired())
}

func TestDictArgPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic: dict type on arg")
		}
		if !strings.Contains(r.(string), "dict") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	NewArg("items", "item dict", ArgType(DictOf(TypeStr)), Variadic(), ArgRequired())
}

// --- Schema tests ---

func TestListFlagSchema(t *testing.T) {
	chdirTemp(t)
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeInt, "id", "ids to process", Unique(false), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("schema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	run := commands["run"].(map[string]interface{})
	flags := run["flags"].([]interface{})
	flag := flags[0].(map[string]interface{})
	// v2 publishes a real JSON Schema fragment, and the v1 carrier string is
	// gone with it (contract §25.2).
	frag := flag["value_schema"].(map[string]interface{})
	if frag["type"] != "array" {
		t.Fatalf("expected an array fragment, got %v", frag)
	}
	items := frag["items"].(map[string]interface{})
	if items["type"] != "integer" {
		t.Fatalf("expected integer items, got %v", items)
	}
}

func TestDictFlagSchema(t *testing.T) {
	chdirTemp(t)
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("schema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	run := commands["run"].(map[string]interface{})
	flags := run["flags"].([]interface{})
	flag := flags[0].(map[string]interface{})
	// A dict's keys are `string` structurally, so `additionalProperties`
	// carrying the value type is a complete description (contract §25.2).
	frag := flag["value_schema"].(map[string]interface{})
	if frag["type"] != "object" {
		t.Fatalf("expected an object fragment, got %v", frag)
	}
	addl := frag["additionalProperties"].(map[string]interface{})
	if addl["type"] != "string" {
		t.Fatalf("expected string values, got %v", addl)
	}
}

// --- Help text tests ---

func TestListFlagHelp(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		ListFlag(TypeInt, "id", "ids to process", Unique(false), Default([]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "<int>") {
		t.Fatalf("expected <int> in help, got: %s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "[list]") {
		t.Fatalf("expected [list] in help, got: %s", r.Stdout)
	}
}

func TestDictFlagHelp(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "<key=str>") {
		t.Fatalf("expected <key=str> in help, got: %s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "[dict]") {
		t.Fatalf("expected [dict] in help, got: %s", r.Stdout)
	}
}

// --- JSON round-trip test ---

func TestDictFlagJSONRoundTrip(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "data", "data map", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	// Pass JSON with special characters in values
	jsonInput := `{"key with spaces": "value with = sign", "simple": "ok"}`
	r := app.Test([]string{"run", "--data", jsonInput})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	data := captured["data"].(map[string]interface{})
	if data["key with spaces"] != "value with = sign" || data["simple"] != "ok" {
		t.Fatalf("unexpected data: %v", data)
	}
}

// --- Mixed dict and key=value test ---

func TestDictFlagMixedJSONAndKV(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "meta", "metadata", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	// First a key=value, then a JSON object -- both should merge
	r := app.Test([]string{"run", "--meta", "a=1", "--meta", `{"b": "2"}`})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	meta := captured["meta"].(map[string]interface{})
	if meta["a"] != "1" || meta["b"] != "2" {
		t.Fatalf("unexpected meta: %v", meta)
	}
}

// --- Test that invoke matches Test for compound types ---

func TestInvokeMatchesTestForListFlag(t *testing.T) {
	var invokeKwargs, testKwargs map[string]interface{}

	makeApp := func(captured *map[string]interface{}) *App {
		app := NewApp("test", "1.0.0", "test app")
		app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
			*captured = kwargs
			return Exit(0)
		}, WithFlags(
			ListFlag(TypeInt, "id", "ids", Unique(false), Default([]interface{}{})),
		), WithEffect(EffectReadOnly))
		return app
	}

	// Invoke
	app1 := makeApp(&invokeKwargs)
	_, err := app1.Call("run", map[string]interface{}{
		"id": []interface{}{10, 20},
	})
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}

	// Test (CLI)
	app2 := makeApp(&testKwargs)
	r := app2.Test([]string{"run", "--id", "10", "--id", "20"})
	if r.ExitCode != 0 {
		t.Fatalf("Test failed: %s", r.Stderr)
	}

	if !reflect.DeepEqual(invokeKwargs, testKwargs) {
		t.Fatalf("kwargs mismatch:\ninvoke: %v\nTest:   %v", invokeKwargs, testKwargs)
	}
}

func TestInvokeMatchesTestForDictFlag(t *testing.T) {
	var invokeKwargs, testKwargs map[string]interface{}

	makeApp := func(captured *map[string]interface{}) *App {
		app := NewApp("test", "1.0.0", "test app")
		app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
			*captured = kwargs
			return Exit(0)
		}, WithFlags(
			DictFlag(TypeStr, "header", "headers", Unique(false), Default(map[string]interface{}{})),
		), WithEffect(EffectReadOnly))
		return app
	}

	// Invoke
	app1 := makeApp(&invokeKwargs)
	_, err := app1.Call("run", map[string]interface{}{
		"header": map[string]interface{}{"X-Key": "val"},
	})
	if err != nil {
		t.Fatalf("invoke error: %v", err)
	}

	// Test (CLI) -- use JSON form to set the same value
	app2 := makeApp(&testKwargs)
	r := app2.Test([]string{"run", "--header", `{"X-Key": "val"}`})
	if r.ExitCode != 0 {
		t.Fatalf("Test failed: %s", r.Stderr)
	}

	if !reflect.DeepEqual(invokeKwargs, testKwargs) {
		invokeJSON, _ := json.Marshal(invokeKwargs)
		testJSON, _ := json.Marshal(testKwargs)
		t.Fatalf("kwargs mismatch:\ninvoke: %s\nTest:   %s", invokeJSON, testJSON)
	}
}

// --- Invalid JSON in dict flag ---

func TestDictFlagInvalidJSON(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "data", "data map", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--data", "{invalid json"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "invalid JSON") {
		t.Fatalf("expected JSON error, got: %s", r.Stderr)
	}
}

// --- Dict flag with JSON type mismatch ---

func TestDictFlagJSONTypeMismatch(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeInt, "data", "data map", Unique(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	// JSON values are strings, but dict expects int
	r := app.Test([]string{"run", "--data", `{"a": "notanint"}`})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "expected integer") {
		t.Fatalf("expected type error, got: %s", r.Stderr)
	}
}

// --- Dict flag default validation ---

func TestDictFlagDefaultTypeMismatch(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic: dict default with wrong value type")
		}
	}()
	DictFlag(TypeInt, "data", "data map", Unique(false),
		Default(map[string]interface{}{"a": "notanint"}))
}

func TestDictFlagDefaultMustBeMap(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic: dict default not a map")
		}
		if !strings.Contains(r.(string), "map[string]interface{}") {
			t.Fatalf("unexpected panic: %v", r)
		}
	}()
	DictFlag(TypeStr, "data", "data map", Unique(false),
		Default([]interface{}{"not", "a", "map"}))
}

func TestListFlagDefaultTypeMismatch(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic: list default with wrong element type")
		}
	}()
	ListFlag(TypeInt, "ids", "id list", Unique(false),
		Default([]interface{}{"notanint"}))
}

// --- Dict env requires no env_separator ---

func TestDictFlagEnvWithoutSeparator(t *testing.T) {
	var captured map[string]interface{}
	app := NewApp("test", "1.0.0", "test app")
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		captured = kwargs
		return Exit(0)
	}, WithFlags(
		DictFlag(TypeStr, "header", "HTTP headers", Unique(false),
			Env("TEST_DICT_NO_SEP"), Prefixed(false), Default(map[string]interface{}{})),
	), WithEffect(EffectReadOnly))

	os.Setenv("TEST_DICT_NO_SEP", `{"a": "b"}`)
	defer os.Unsetenv("TEST_DICT_NO_SEP")

	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	headers := captured["header"].(map[string]interface{})
	if headers["a"] != "b" {
		t.Fatalf("unexpected headers: %v", headers)
	}
}

// --- Compound type flag names ---

func TestFlagTypeNames(t *testing.T) {
	expected := map[FlagType]string{
		TypeListStr:   "list[str]",
		TypeListInt:   "list[int]",
		TypeListFloat: "list[float]",
		TypeDictStr:   "dict[str]",
		TypeDictInt:   "dict[int]",
		TypeDictFloat: "dict[float]",
	}
	for ft, name := range expected {
		if flagTypeName[ft] != name {
			t.Errorf("flagTypeName[%d] = %q, want %q", ft, flagTypeName[ft], name)
		}
	}
}

// TestFormatDictForDisplaySortedKeys verifies that dict values render with
// keys sorted ascending in canonical "key=value" form. Regression: dict
// display must be deterministic (sorted), not dependent on Go
// map iteration order or the fmt "map[...]" representation.
func TestFormatDictForDisplaySortedKeys(t *testing.T) {
	m := map[string]interface{}{
		"zebra": "z",
		"alpha": int64(1),
		"mike":  true,
	}
	got := formatDictForDisplay(m)
	want := "alpha=1, mike=true, zebra=z"
	if got != want {
		t.Fatalf("dict display not canonical/sorted.\nwant: %q\ngot:  %q", want, got)
	}

	// formatValueForError must route maps through the same canonical form.
	if fv := formatValueForError(m); fv != want {
		t.Fatalf("formatValueForError map case: want %q, got %q", want, fv)
	}
}

// --- Choices on a LIST carrier (contract §25.5) ---
//
// A list carrier's choices constrain each ELEMENT, which is exactly what the
// published fragment says: an array whose `items` carries the enum. The blanket
// compound refusal was one rule too wide -- a dict's keys are structurally
// strings and its values are not a closed set, so the ban survives there and
// only there.

func listChoicesApp() *App {
	return simpleApp("run", "run it", "tags={tags}", WithFlags(
		ListFlag(TypeStr, "tags", "tags to apply", Optional(), Unique(false),
			Choices(Ch("a", "the a tag"), Ch("b", "the b tag"))),
	))
}

func TestListCarrierAcceptsChoicesPerElement(t *testing.T) {
	r := listChoicesApp().Test([]string{"run", "--tags", "a", "--tags", "b"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "tags=a,b") {
		t.Fatalf("stdout = %q", r.Stdout)
	}
}

func TestListCarrierChoicesRefuseTheOffendingElement(t *testing.T) {
	r := listChoicesApp().Test([]string{"run", "--tags", "a", "--tags", "zz"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: --tags: invalid value 'zz', must be one of: a, b\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// An element is one occurrence: a list carrier never splits a token, so a
// comma-carrying token is one (invalid) element rather than two valid ones.
func TestListCarrierChoicesDoNotSplitAToken(t *testing.T) {
	r := listChoicesApp().Test([]string{"run", "--tags", "a,b"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: --tags: invalid value 'a,b', must be one of: a, b\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// The env path resolves the same list and validates the same elements.
func TestListCarrierChoicesValidateEnvElements(t *testing.T) {
	app := simpleApp("run", "run it", "tags={tags}", WithFlags(
		ListFlag(TypeStr, "tags", "tags to apply", Optional(), Unique(false),
			Env("APP_TAGS"), EnvSeparator(","),
			Choices(Ch("a", "the a tag"), Ch("b", "the b tag"))),
	))
	t.Setenv("APP_TAGS", "a,zz")
	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: --tags: invalid value 'zz', must be one of: a, b\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// A declared default is a declaration, not an invocation: an element outside
// the choices is refused where every other value is, at parse time.
func TestListCarrierChoicesValidateADeclaredDefault(t *testing.T) {
	app := simpleApp("run", "run it", "tags={tags}", WithFlags(
		ListFlag(TypeStr, "tags", "tags to apply", Default([]interface{}{"zz"}), Unique(false),
			Choices(Ch("a", "the a tag"), Ch("b", "the b tag"))),
	))
	r := app.Test([]string{"run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	want := "error: --tags: invalid value 'zz', must be one of: a, b\n"
	if !strings.Contains(r.Stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", r.Stderr, want)
	}
}

// The ban survives for a dict, in the siblings' own words.
func TestDictCarrierStillRefusesChoices(t *testing.T) {
	expectPanic(t, `Flag "header": dict type cannot be combined with choices`, func() {
		DictFlag(TypeStr, "header", "HTTP headers", Optional(),
			Choices(Ch("a", "the a header"), Ch("b", "the b header")))
	})
}

// §25.5's fragment: the enum lives inside `items`, where the elements are.
func TestListCarrierChoicesSchemaFragment(t *testing.T) {
	chdirTemp(t)
	schema, err := dumpSchema(listChoicesApp())
	if err != nil {
		t.Fatalf("schema error: %v", err)
	}
	commands := schema["commands"].(map[string]interface{})
	run := commands["run"].(map[string]interface{})
	flags := run["flags"].([]interface{})
	frag := flags[0].(map[string]interface{})["value_schema"].(map[string]interface{})
	if frag["type"] != "array" {
		t.Fatalf("expected an array fragment, got %v", frag)
	}
	if _, atTop := frag["enum"]; atTop {
		t.Fatalf("the enum belongs inside items, got %v", frag)
	}
	items := frag["items"].(map[string]interface{})
	if items["type"] != "string" {
		t.Fatalf("expected string items, got %v", items)
	}
	if !reflect.DeepEqual(items["enum"], []interface{}{"a", "b"}) {
		t.Fatalf("items enum = %#v", items["enum"])
	}
}

// The tool schema publishes the same fragment, because it IS the same fragment.
func TestListCarrierChoicesToolSchema(t *testing.T) {
	props := listChoicesApp().JsonSchema("run")["properties"].(map[string]interface{})
	tags := props["tags"].(map[string]interface{})
	if tags["type"] != "array" {
		t.Fatalf("expected an array property, got %v", tags)
	}
	items := tags["items"].(map[string]interface{})
	if !reflect.DeepEqual(items["enum"], []interface{}{"a", "b"}) {
		t.Fatalf("items enum = %#v", items["enum"])
	}
}

// The help block renders like any other choices block: one line per entry.
func TestListCarrierChoicesHelpBlock(t *testing.T) {
	r := listChoicesApp().Test([]string{"run", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	want := "  --tags <str>    tags to apply [list] [optional]\n" +
		"    a             the a tag\n" +
		"    b             the b tag\n"
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("stdout = %q, want it to contain %q", r.Stdout, want)
	}
}
