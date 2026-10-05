package strictcli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Eager root resolution ---

func TestInfraRoot_EnvSet(t *testing.T) {
	os.Setenv("MYAPP_HOME", "/opt/data")
	defer os.Unsetenv("MYAPP_HOME")

	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"))
	if got := app.infraRoots["MYAPP_HOME"]; got != "/opt/data" {
		t.Fatalf("root = %q, want /opt/data", got)
	}
	if !app.infraRootFromEnv["MYAPP_HOME"] {
		t.Fatalf("expected fromEnv true")
	}
}

func TestInfraRoot_Unset(t *testing.T) {
	os.Unsetenv("MYAPP_HOME")
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"))
	if got := app.infraRoots["MYAPP_HOME"]; got != "/var/lib/myapp" {
		t.Fatalf("root = %q, want /var/lib/myapp", got)
	}
	if app.infraRootFromEnv["MYAPP_HOME"] {
		t.Fatalf("expected fromEnv false")
	}
}

func TestInfraRoot_TildeExpansion(t *testing.T) {
	os.Unsetenv("MYAPP_HOME")
	home, _ := os.UserHomeDir()
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "~/.myapp"))
	want := filepath.Join(home, ".myapp")
	if got := app.infraRoots["MYAPP_HOME"]; got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}

func TestInfraRoot_TildeExpansionFromEnv(t *testing.T) {
	os.Setenv("MYAPP_HOME", "~/data")
	defer os.Unsetenv("MYAPP_HOME")
	home, _ := os.UserHomeDir()
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"))
	want := filepath.Join(home, "data")
	if got := app.infraRoots["MYAPP_HOME"]; got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}

// --- Flag-default marker + infra provenance ---

func newInfraFlagApp(t *testing.T) (*App, *map[string]string, *map[string]interface{}) {
	sources := map[string]string{}
	kw := map[string]interface{}{}
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		sources = map[string]string{"db": ctx.Source("db")}
		kw = kwargs
		return Exit(0)
	}, WithFlags(
		StringFlag("db", "db path", Default(RelativeToRoot("MYAPP_HOME", "db.sqlite"))),
	), WithEffect(EffectReadOnly))
	return app, &sources, &kw
}

func TestFlagDefaultMarker_InfraProvenance(t *testing.T) {
	os.Unsetenv("MYAPP_HOME")
	app, sourcesP, kwP := newInfraFlagApp(t)
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s", r.ExitCode, r.Stderr)
	}
	if got := (*kwP)["db"]; got != "/var/lib/myapp/db.sqlite" {
		t.Fatalf("db = %v, want /var/lib/myapp/db.sqlite", got)
	}
	if (*sourcesP)["db"] != "infra" {
		t.Fatalf("source = %q, want infra", (*sourcesP)["db"])
	}
}

func TestFlagDefaultMarker_HermeticImmune(t *testing.T) {
	os.Setenv("MYAPP_HOME", "/opt/data")
	defer os.Unsetenv("MYAPP_HOME")
	app, sourcesP, kwP := newInfraFlagApp(t)
	// Even under --hermetic, the root resolves (it has no argv dependency).
	r := app.Test([]string{"--hermetic", "run"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s", r.ExitCode, r.Stderr)
	}
	if got := (*kwP)["db"]; got != "/opt/data/db.sqlite" {
		t.Fatalf("db = %v, want /opt/data/db.sqlite", got)
	}
	if (*sourcesP)["db"] != "infra" {
		t.Fatalf("source = %q, want infra", (*sourcesP)["db"])
	}
}

func TestCliOverride_NotInfra(t *testing.T) {
	os.Unsetenv("MYAPP_HOME")
	app, sourcesP, kwP := newInfraFlagApp(t)
	r := app.Test([]string{"run", "--db", "/tmp/custom.db"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s", r.ExitCode, r.Stderr)
	}
	if got := (*kwP)["db"]; got != "/tmp/custom.db" {
		t.Fatalf("db = %v, want /tmp/custom.db", got)
	}
	if (*sourcesP)["db"] != "cli" {
		t.Fatalf("source = %q, want cli", (*sourcesP)["db"])
	}
}

// --- Infra defaults and the dependency presence predicate ---

// An infra-resolved default is still a DECLARED default: it does not make the
// flag provided, so it never engages an all-or-none member and never satisfies
// a Requires (contract §23.5's amended rows, §23.6's source table, §26.4).

func newInfraDepApp(cs ...Constraint) *App {
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("db=%v cache=%v", kwargs["db"], kwargs["cache"]))
		return Exit(0)
	}, WithFlags(
		StringFlag("db", "db path", Default(RelativeToRoot("MYAPP_HOME", "db.sqlite"))),
		StringFlag("cache", "cache path", Optional()),
	), WithConstraints(cs...), WithEffect(EffectReadOnly))
	return app
}

func TestInfraDefaultIsNotPresentForAllOrNone(t *testing.T) {
	os.Unsetenv("MYAPP_HOME")
	app := newInfraDepApp(AllOrNone("paths", Member("db"), Member("cache")))
	// Neither member supplied: the infra default is not a supplied value, so
	// the group is not half-filled and there is no violation.
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "db=/var/lib/myapp/db.sqlite cache=<nil>\n" {
		t.Fatalf("unexpected stdout %q", r.Stdout)
	}
}

func TestInfraDefaultDoesNotSatisfyAllOrNone(t *testing.T) {
	os.Unsetenv("MYAPP_HOME")
	app := newInfraDepApp(AllOrNone("paths", Member("db"), Member("cache")))
	// The other member supplied alone: the infra default does not stand in for
	// the missing one, so the group IS half-filled.
	r := app.Test([]string{"run", "--cache", "/tmp/c"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, `constraint "paths": --db, --cache must be used together`) {
		t.Fatalf("expected all-or-none error, got %q", r.Stderr)
	}
}

func TestInfraDefaultDoesNotSatisfyRequires(t *testing.T) {
	os.Unsetenv("MYAPP_HOME")
	app := newInfraDepApp(Requires("cache-needs-db", "cache", "db"))
	r := app.Test([]string{"run", "--cache", "/tmp/c"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d: stdout=%q", r.ExitCode, r.Stdout)
	}
	if !strings.Contains(r.Stderr, `constraint "cache-needs-db": flag '--cache' requires '--db'`) {
		t.Fatalf("expected requires error, got %q", r.Stderr)
	}
}

// --- Config-path marker rewrite ---

func TestConfigPathMarkerRewrite(t *testing.T) {
	os.Setenv("MYAPP_HOME", "/opt/data")
	defer os.Unsetenv("MYAPP_HOME")
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"),
		WithConfig(),
		WithConfigPathRelativeToRoot("MYAPP_HOME", "config.json"))
	if app.configPathOverride != "/opt/data/config.json" {
		t.Fatalf("configPathOverride = %q, want /opt/data/config.json", app.configPathOverride)
	}
	r := app.Test([]string{"config", "path"})
	if !strings.Contains(r.Stdout, "/opt/data/config.json") {
		t.Fatalf("config path output = %q", r.Stdout)
	}
}

// --- Undeclared root marker: registration hard error ---

func TestUndeclaredRootMarker_Panics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic for undeclared root marker")
		}
	}()
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("db", "db path", Default(RelativeToRoot("NOPE", "x")))), WithEffect(EffectReadOnly))
}

func TestConfigPathMarker_UndeclaredPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic for undeclared config-path root marker")
		}
	}()
	NewApp("myapp", "1.0.0", "test app",
		WithConfig(),
		WithConfigPathRelativeToRoot("NOPE", "config.json"))
}

// --- Handshake + accessor ---

type infraHandler struct {
	rootVal   string
	rootOK    bool
	hsVal     string
	hsOK      bool
	panicked  bool
	testUndef bool
}

func (h *infraHandler) command() func(ctx *Context, kwargs map[string]interface{}) Outcome {
	return func(ctx *Context, kwargs map[string]interface{}) Outcome {
		if h.testUndef {
			defer func() {
				if recover() != nil {
					h.panicked = true
				}
			}()
			ctx.InfraValue("UNDECLARED_VAR")
			return Exit(0)
		}
		h.rootVal, h.rootOK = ctx.InfraValue("MYAPP_HOME")
		h.hsVal, h.hsOK = ctx.InfraValue("CI_TOKEN")
		return Exit(0)
	}
}

func TestInfraValue_RootAndHandshakeLiveRead(t *testing.T) {
	os.Setenv("MYAPP_HOME", "/opt/data")
	defer os.Unsetenv("MYAPP_HOME")
	os.Setenv("CI_TOKEN", "abc123")
	defer os.Unsetenv("CI_TOKEN")

	h := &infraHandler{}
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"),
		WithHandshakeEnv("CI_TOKEN", "CI auth token"))
	app.Command("run", "run it", h.command(), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s", r.ExitCode, r.Stderr)
	}
	if h.rootVal != "/opt/data" || !h.rootOK {
		t.Fatalf("root = (%q,%v), want (/opt/data,true)", h.rootVal, h.rootOK)
	}
	if h.hsVal != "abc123" || !h.hsOK {
		t.Fatalf("handshake = (%q,%v), want (abc123,true)", h.hsVal, h.hsOK)
	}
}

func TestInfraValue_HandshakeUnsetLiveRead(t *testing.T) {
	os.Unsetenv("CI_TOKEN")
	os.Unsetenv("MYAPP_HOME")
	h := &infraHandler{}
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"),
		WithHandshakeEnv("CI_TOKEN", "CI auth token"))
	app.Command("run", "run it", h.command(), WithEffect(EffectReadOnly))
	r := app.Test([]string{"run"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s", r.ExitCode, r.Stderr)
	}
	if h.hsOK {
		t.Fatalf("expected handshake unset (ok=false), got val=%q", h.hsVal)
	}
}

func TestInfraValue_UndeclaredPanics(t *testing.T) {
	h := &infraHandler{testUndef: true}
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"))
	app.Command("run", "run it", h.command(), WithEffect(EffectReadOnly))
	app.Test([]string{"run"})
	if !h.panicked {
		t.Fatalf("expected InfraValue on undeclared var to panic")
	}
}

func TestHandshake_DuplicateRootPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic for handshake colliding with root")
		}
	}()
	NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("SHARED", "/x"),
		WithHandshakeEnv("SHARED", "collides"))
}

// --- Surfaces: schema, help, config show ---

func TestInfraSchemaSurface(t *testing.T) {
	os.Setenv("MYAPP_HOME", "/opt/data")
	defer os.Unsetenv("MYAPP_HOME")
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"),
		WithHandshakeEnv("CI_TOKEN", "CI auth token"))
	schema := app.DumpSchemaDict()
	infra, ok := schema["infra"].(map[string]interface{})
	if !ok {
		t.Fatalf("no infra section in schema: %v", schema["infra"])
	}
	roots := infra["roots"].([]interface{})
	root0 := roots[0].(map[string]interface{})
	if root0["env_var"] != "MYAPP_HOME" || root0["default"] != "/var/lib/myapp" {
		t.Fatalf("root0 = %v", root0)
	}
	// Machine-stable: resolved value must NOT be present.
	if _, present := root0["resolved"]; present {
		t.Fatalf("schema must not include resolved root value")
	}
	hs := infra["handshakes"].([]interface{})
	hs0 := hs[0].(map[string]interface{})
	if hs0["env_var"] != "CI_TOKEN" || hs0["help"] != "CI auth token" {
		t.Fatalf("hs0 = %v", hs0)
	}
}

func TestInfraSchemaAbsentWhenUndeclared(t *testing.T) {
	app := NewApp("myapp", "1.0.0", "test app")
	if _, present := app.DumpSchemaDict()["infra"]; present {
		t.Fatalf("infra section must be absent when nothing declared")
	}
}

func TestInfraHelpSurface(t *testing.T) {
	os.Unsetenv("MYAPP_HOME")
	app := NewApp("myapp", "1.0.0", "test app",
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"),
		WithHandshakeEnv("CI_TOKEN", "CI auth token"))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))
	r := app.Test([]string{"--help"})
	if !strings.Contains(r.Stdout, "Infrastructure:") {
		t.Fatalf("help missing Infrastructure section: %s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "MYAPP_HOME") || !strings.Contains(r.Stdout, "CI_TOKEN") {
		t.Fatalf("help missing infra vars: %s", r.Stdout)
	}
}

func TestInfraConfigShowSurface(t *testing.T) {
	os.Setenv("MYAPP_HOME", "/opt/data")
	defer os.Unsetenv("MYAPP_HOME")
	os.Unsetenv("CI_TOKEN")
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	os.WriteFile(configFile, []byte(`{}`), 0o644)
	app := NewApp("myapp", "1.0.0", "test app",
		WithConfig(), WithConfigPath(configFile),
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"),
		WithHandshakeEnv("CI_TOKEN", "CI auth token"))
	r := app.Test([]string{"config", "show", "--plain"})
	if !strings.Contains(r.Stdout, "Infrastructure:") {
		t.Fatalf("config show missing Infrastructure: %s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "MYAPP_HOME (root) = /opt/data") {
		t.Fatalf("config show root wrong: %s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "source: env-set") {
		t.Fatalf("config show root source wrong: %s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "CI_TOKEN (handshake) = <unset>") {
		t.Fatalf("config show handshake wrong: %s", r.Stdout)
	}

	// JSON mode
	rj := app.Test([]string{"config", "show", "--json"})
	var result map[string]interface{}
	if err := json.Unmarshal(envelopePayload(t, rj.Stdout), &result); err != nil {
		t.Fatalf("json parse: %s\n%s", err, rj.Stdout)
	}
	infra, ok := result["__infrastructure__"].(map[string]interface{})
	if !ok {
		t.Fatalf("no __infrastructure__ in json: %v", result)
	}
	root := infra["MYAPP_HOME"].(map[string]interface{})
	if root["resolved"] != "/opt/data" || root["source"] != "env" {
		t.Fatalf("json root = %v", root)
	}
}

// --- Marker display form ---

// A RelativeToRoot marker renders as the Python repr of the declaration, which
// is what Python and TypeScript both print. Go used to fall through to fmt's
// struct rendering ("{MYAPP_ROOT [store]}"), leaking its own internal shape
// into help output.
func TestMarkerStringForm(t *testing.T) {
	cases := []struct {
		marker InfraRootPath
		want   string
	}{
		{RelativeToRoot("MYAPP_ROOT", "store"), "RelativeToRoot('MYAPP_ROOT', 'store')"},
		{RelativeToRoot("MYAPP_ROOT", "sub", "db.sqlite"), "RelativeToRoot('MYAPP_ROOT', 'sub', 'db.sqlite')"},
		// Python's repr keeps the separator after the env var even with no
		// parts at all: repr(RelativeToRoot('E')) == "RelativeToRoot('E', )".
		{RelativeToRoot("E"), "RelativeToRoot('E', )"},
		{RelativeToRoot("E", ""), "RelativeToRoot('E', '')"},
		// Quote selection follows Python's repr: double quotes only when the
		// value contains a single quote and no double quote.
		{RelativeToRoot("it's", "x"), `RelativeToRoot("it's", 'x')`},
		{RelativeToRoot("E", `q"z`), `RelativeToRoot('E', 'q"z')`},
		{RelativeToRoot("E", `a\b`), `RelativeToRoot('E', 'a\\b')`},
	}
	for _, tc := range cases {
		if got := tc.marker.String(); got != tc.want {
			t.Fatalf("String() = %q, want %q", got, tc.want)
		}
		if got := fmt.Sprintf("%v", tc.marker); got != tc.want {
			t.Fatalf("%%v = %q, want %q", got, tc.want)
		}
	}
}

func TestMarkerDefaultInHelp(t *testing.T) {
	os.Unsetenv("MYAPP_ROOT")
	app := NewApp("myapp", "1.0.0", "test app", WithInfraRoot("MYAPP_ROOT", "/opt/myapp"))
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(StringFlag("store", "the store", Default(RelativeToRoot("MYAPP_ROOT", "store")))),
		WithEffect(EffectReadOnly))
	r := app.Test([]string{"cmd", "--help"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	want := "  --store <str>    the store [default: RelativeToRoot('MYAPP_ROOT', 'store')]"
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("help line missing:\nwant %q\ngot  %q", want, r.Stdout)
	}
}

// --- config show and a marker default ---

// newMarkerConfigShowApp is an app whose one flag carries a RelativeToRoot
// default, with a config file that leaves it alone: the flag resolves to its
// declared default, which is the marker itself.
func newMarkerConfigShowApp(t *testing.T) *App {
	t.Helper()
	os.Unsetenv("MYAPP_HOME")
	configFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configFile, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	app := NewApp("myapp", "1.0.0", "test app",
		WithConfig(), WithConfigPath(configFile),
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		StringFlag("db", "db path", Default(RelativeToRoot("MYAPP_HOME", "db.sqlite"))),
	), WithEffect(EffectReadOnly))
	return app
}

// config show prints a marker-defaulted flag as the DECLARATION it was written
// as, labelled `default` -- not the path a run would deliver (contract §13,
// §25.10, §18.26 item 261). This line is byte-identical in Python and is the
// pin the machine form below must not disturb.
func TestConfigShowMarkerDefaultHumanForm(t *testing.T) {
	app := newMarkerConfigShowApp(t)
	r := app.Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	want := "db = RelativeToRoot('MYAPP_HOME', 'db.sqlite')  (source: default)"
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("config show line missing:\nwant %q\ngot  %q", want, r.Stdout)
	}
}

// The machine form publishes §13's marker shape -- the same
// {"relative_to_root": {"env_var": ..., "parts": [...]}} the dumped schema
// publishes for the same declaration. Go used to hand the marker straight to
// encoding/json, whose unexported fields marshal to `{}`: a read-only command
// published an empty object where the document pins one shape.
func TestConfigShowMarkerDefaultMachineForm(t *testing.T) {
	app := newMarkerConfigShowApp(t)
	r := app.Test([]string{"config", "show", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: stderr=%q stdout=%q", r.ExitCode, r.Stderr, r.Stdout)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(envelopePayload(t, r.Stdout), &payload); err != nil {
		t.Fatalf("json parse: %s\n%s", err, r.Stdout)
	}
	entry, ok := payload["db"].(map[string]interface{})
	if !ok {
		t.Fatalf("no db entry in payload: %v", payload)
	}
	if entry["source"] != "default" {
		t.Fatalf("source = %v, want default", entry["source"])
	}
	got, err := json.Marshal(entry["value"])
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	want := `{"relative_to_root":{"env_var":"MYAPP_HOME","parts":["db.sqlite"]}}`
	if string(got) != want {
		t.Fatalf("value = %s, want %s", got, want)
	}
	// The WRITTEN bytes carry the same shape in the same key order: the payload
	// is one object per entry, so what the envelope emits is what a sibling
	// implementation's payload is byte-compared against.
	if !strings.Contains(r.Stdout, `"value":`+want) {
		t.Fatalf("emitted payload does not carry %s: %s", want, r.Stdout)
	}
	// The human rendering rides the envelope's diagnostics unchanged (§19.1).
	if !strings.Contains(r.Stderr+r.Stdout, "db = RelativeToRoot('MYAPP_HOME', 'db.sqlite')  (source: default)") {
		t.Fatalf("machine-mode diagnostics lost the human line: stdout=%q stderr=%q", r.Stdout, r.Stderr)
	}
}

// A marker with no parts publishes an empty parts array, not a missing key or
// a null -- the same shape the schema dump publishes for the same declaration.
func TestConfigShowMarkerDefaultNoPartsMachineForm(t *testing.T) {
	os.Unsetenv("MYAPP_HOME")
	configFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configFile, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	app := NewApp("myapp", "1.0.0", "test app",
		WithConfig(), WithConfigPath(configFile),
		WithInfraRoot("MYAPP_HOME", "/var/lib/myapp"))
	app.Command("run", "run it", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		return Exit(0)
	}, WithFlags(
		StringFlag("home", "the root itself", Default(RelativeToRoot("MYAPP_HOME"))),
	), WithEffect(EffectReadOnly))
	r := app.Test([]string{"config", "show", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(envelopePayload(t, r.Stdout), &payload); err != nil {
		t.Fatalf("json parse: %s\n%s", err, r.Stdout)
	}
	entry := payload["home"].(map[string]interface{})
	got, err := json.Marshal(entry["value"])
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	want := `{"relative_to_root":{"env_var":"MYAPP_HOME","parts":[]}}`
	if string(got) != want {
		t.Fatalf("value = %s, want %s", got, want)
	}
}
