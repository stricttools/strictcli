package strictcli

// Declared reads (Read on HTTP, Observe on Run), the settable HTTP client and
// the HTTP Timeout, Redact, the HTTP body's byte count, and Write's Mode.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const redactSecret = "sk-live-0123456789"

// countingTransport counts round trips, proving the declared client was used.
type countingTransport struct {
	trips atomic.Int64
	base  http.RoundTripper
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.trips.Add(1)
	return c.base.RoundTrip(r)
}

// httpFixture is an app with one command whose handler is fn, wired to a
// counting client.
func httpFixture(effect string, fn func(ctx *Context) Outcome, opts ...CmdOption) (*App, *countingTransport) {
	transport := &countingTransport{base: http.DefaultTransport}
	app := NewApp("app", "1.0.0", "effects fixture", WithHTTPClient(&http.Client{Transport: transport}))
	all := append([]CmdOption{WithEffect(effect)}, opts...)
	app.Command("go", "run the fixture handler",
		func(ctx *Context, kwargs map[string]interface{}) Outcome { return fn(ctx) }, all...)
	return app, transport
}

func okServer(t *testing.T, hits *atomic.Int64) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("pong"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// --- declared HTTP reads ---------------------------------------------------

func TestDeclaredHTTPReadInReadOnlyIsPerformedUnderDryRun(t *testing.T) {
	for _, argv := range [][]string{{"--dry-run", "go"}, {"go"}} {
		var hits atomic.Int64
		srv := okServer(t, &hits)
		var resp Response
		var err error
		app, transport := httpFixture(EffectReadOnly, func(ctx *Context) Outcome {
			resp, err = ctx.Effects().HTTP("GET", srv.URL+"/status", Read())
			return Exit(0)
		})
		r := app.Test(argv)
		if err != nil {
			t.Fatalf("%v: a declared read is legal in a read_only command, got %v", argv, err)
		}
		if string(resp.Body()) != "pong" || resp.Status() != 200 {
			t.Fatalf("%v: a declared read returns the real response, got %d %q", argv, resp.Status(), resp.Body())
		}
		if hits.Load() != 1 || transport.trips.Load() != 1 {
			t.Fatalf("%v: expected one request through the declared client, got hits=%d trips=%d", argv, hits.Load(), transport.trips.Load())
		}
		if len(app.EffectLog()) != 0 {
			t.Fatalf("%v: a declared read is never recorded, got %#v", argv, app.EffectLog())
		}
		if strings.Contains(r.Stdout, "net:") {
			t.Fatalf("%v: a declared read is not in the would-do log, got %q", argv, r.Stdout)
		}
	}
}

func TestHTTPWithoutReadIsStillRefusedInReadOnly(t *testing.T) {
	var hits atomic.Int64
	srv := okServer(t, &hits)
	var err error
	app, _ := httpFixture(EffectReadOnly, func(ctx *Context) Outcome {
		_, err = ctx.Effects().HTTP("GET", srv.URL)
		return Exit(0)
	})
	app.Test([]string{"go"})
	want := `command "go" is classified read_only; effects.http is a mutating operation`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
	if hits.Load() != 0 {
		t.Fatalf("a refused request must not be sent")
	}
}

func TestHTTPWithoutReadIsRecordedWhateverTheMethod(t *testing.T) {
	var hits atomic.Int64
	srv := okServer(t, &hits)
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().HTTP("GET", srv.URL)
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	if !strings.Contains(r.Stdout, "1. net: GET "+srv.URL) || hits.Load() != 0 {
		t.Fatalf("an undeclared GET is a recorded mutation, got %q (hits=%d)", r.Stdout, hits.Load())
	}
}

func TestDeclaredHTTPReadAfterARecordedMutationIsStale(t *testing.T) {
	var hits atomic.Int64
	srv := okServer(t, &hits)
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().Run([]interface{}{"make", "deploy"})
		stale, _ := ctx.Effects().HTTP("GET", srv.URL+"/status", Read())
		ctx.Effects().Write("REPORT", stale)
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	if !strings.Contains(r.Stdout, "2. write: REPORT («stale: GET "+srv.URL+"/status»)") {
		t.Fatalf("expected a stale brand, got %q", r.Stdout)
	}
	if hits.Load() != 0 {
		t.Fatalf("a post-mutation read is not performed under --dry-run")
	}
}

func TestReadWithAGrantIsRefused(t *testing.T) {
	var err error
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		_, err = ctx.Effects().HTTP("GET", "https://x.test", Read(), UseGrant("api"))
		return Exit(0)
	}, WithGrants(Grant{Name: "api", Reason: "calls the API", Kind: NetMutate}))
	app.Test([]string{"--dry-run", "go"})
	want := `command "go": grant 'api' cannot be used on effects.http declared with option 'read' (a declared read changes nothing)`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
}

// --- declared process reads -------------------------------------------------

func TestObserveRunInReadOnlyIsPerformedUnderDryRun(t *testing.T) {
	var got Completed
	var err error
	app := effectsApp(EffectReadOnly, func(ctx *Context) Outcome {
		got, err = ctx.Effects().Run(echoArgv(), echoEnv("observed"), Observe())
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	if err != nil {
		t.Fatalf("an Observe() run is legal in a read_only command, got %v (stderr=%q)", err, r.Stderr)
	}
	if got.Stdout() != "observed" {
		t.Fatalf("an Observe() run is performed under --dry-run, got %q", got.Stdout())
	}
	if len(app.EffectLog()) != 0 || strings.Contains(r.Stdout, "run:") {
		t.Fatalf("an Observe() run is never recorded, got %#v / %q", app.EffectLog(), r.Stdout)
	}
}

func TestObserveRunAfterARecordedMutationIsStale(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().Run([]interface{}{"git", "tag", "v1"})
		stale, _ := ctx.Effects().Run([]interface{}{"git", "status"}, Observe())
		ctx.Effects().Write("REPORT", stale)
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	if !strings.Contains(r.Stdout, "2. write: REPORT («stale: git status»)") {
		t.Fatalf("expected a stale brand, got %q", r.Stdout)
	}
}

func TestObserveWithAGrantIsRefused(t *testing.T) {
	var err error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		_, err = ctx.Effects().Run([]interface{}{"git", "status"}, Observe(), UseGrant("push"))
		return Exit(0)
	}, WithGrants(Grant{Name: "push", Reason: "pushes", Kind: ProcMutate}))
	app.Test([]string{"--dry-run", "go"})
	want := `command "go": grant 'push' cannot be used on effects.run declared with option 'observe' (a declared read changes nothing)`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
}

// --- the HTTP client and Timeout ---------------------------------------------

func TestTheDefaultHTTPClientIsNotTheStdlibDefaultAndHasATimeout(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome { return Exit(0) })
	e := app.armEffects(app.commands["go"], "go", false, nil)
	if e.httpClient == nil || e.httpClient == http.DefaultClient || e.httpClient.Timeout <= 0 {
		t.Fatalf("expected the framework's own client with a finite timeout, got %#v", e.httpClient)
	}
}

func TestWithHTTPClientRefusesNil(t *testing.T) {
	got := mustPanic(t, func() { NewApp("app", "1.0.0", "h", WithHTTPClient(nil)) })
	if got != "WithHTTPClient: the client must not be nil" {
		t.Fatalf("got %q", got)
	}
}

func TestHTTPTimeoutAbortsASlowRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	var err error
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		_, err = ctx.Effects().HTTP("GET", srv.URL+"/slow", Timeout(50*time.Millisecond))
		return Exit(0)
	})
	start := time.Now()
	app.Test([]string{"go"})
	want := `command "go": effects.http timed out: GET ` + srv.URL + `/slow did not complete within 50ms`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the timeout did not bound the request")
	}
}

func TestHTTPTimeoutRendersInDryRun(t *testing.T) {
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().HTTP("POST", "https://api.test/x", Timeout(30*time.Second))
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	if !strings.Contains(r.Stdout, "1. net: POST https://api.test/x (timeout: 30s)") || app.EffectLog()[0]["timeout"] != "30s" {
		t.Fatalf("got %q / %#v", r.Stdout, app.EffectLog())
	}
}

// --- the HTTP body ----------------------------------------------------------

func TestHTTPBodyRendersOnlyItsByteCount(t *testing.T) {
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().HTTP("POST", "https://api.test/x", Body([]byte(redactSecret)))
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	want := "  1. net: POST https://api.test/x (body: 18 bytes, content withheld)\n"
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("got %q want %q", r.Stdout, want)
	}
	log := app.EffectLog()
	blob, _ := json.Marshal(log)
	if log[0]["body_bytes"] != 18 || strings.Contains(string(blob), redactSecret) {
		t.Fatalf("unexpected record: %s", blob)
	}
}

// --- Redact -------------------------------------------------------------------

func TestRedactHidesAQueryStringKeyEverywhere(t *testing.T) {
	url := "https://api.test/v1/zones?api_key=" + redactSecret
	for _, argv := range [][]string{{"--dry-run", "go"}, {"--json", "--dry-run", "go"}} {
		app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
			ctx.Effects().HTTP("POST", url, Redact(redactSecret), Resource("zone-"+redactSecret))
			ctx.Effects().Run([]interface{}{"deploy", "--token", redactSecret}, Redact(redactSecret))
			ctx.Effects().Write("/srv/"+redactSecret+"/key", "x", Redact(redactSecret))
			return Exit(0)
		})
		r := app.Test(argv)
		if strings.Contains(r.Stdout+r.Stderr, redactSecret) {
			t.Fatalf("%v: the redacted value leaked: stdout=%q stderr=%q", argv, r.Stdout, r.Stderr)
		}
		blob, _ := json.Marshal(app.EffectLog())
		if strings.Contains(string(blob), redactSecret) {
			t.Fatalf("%v: the redacted value leaked into the records: %s", argv, blob)
		}
		if !strings.Contains(r.Stdout+r.Stderr, "api_key=«redacted»") {
			t.Fatalf("%v: expected the redaction marker, got stdout=%q stderr=%q", argv, r.Stdout, r.Stderr)
		}
	}
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().HTTP("POST", url, Redact(redactSecret))
		ctx.Effects().Run([]interface{}{"deploy", "--token", redactSecret}, Redact(redactSecret))
		ctx.Effects().Write("/srv/"+redactSecret+"/key", "x", Redact(redactSecret))
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	want := "DRY RUN — no changes were made. Would do:\n" +
		"  1. net: POST https://api.test/v1/zones?api_key=«redacted»\n" +
		"  2. run: deploy --token «redacted»\n" +
		"  3. write: /srv/«redacted»/key (1 bytes)\n"
	if r.Stdout != want {
		t.Fatalf("got:\n%q\nwant:\n%q", r.Stdout, want)
	}
}

func TestRedactHidesTheValueInLiveModeErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	t.Cleanup(srv.Close)
	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	var statusErr, transportErr, writeErr error
	dir := t.TempDir()
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		_, statusErr = ctx.Effects().HTTP("GET", srv.URL+"/?key="+redactSecret, Redact(redactSecret))
		_, transportErr = ctx.Effects().HTTP("GET", closedURL+"/?key="+redactSecret, Redact(redactSecret))
		_, writeErr = ctx.Effects().Write(filepath.Join(dir, redactSecret, "missing", "f"), "x", Redact(redactSecret))
		return Exit(0)
	})
	app.Test([]string{"go"})
	wantStatus := `command "go": effects.http failed: GET ` + srv.URL + `/?key=«redacted» returned 500`
	if statusErr == nil || statusErr.Error() != wantStatus {
		t.Fatalf("got %v want %q", statusErr, wantStatus)
	}
	for name, err := range map[string]error{"transport": transportErr, "write": writeErr} {
		if err == nil || strings.Contains(err.Error(), redactSecret) || !strings.Contains(err.Error(), "«redacted»") {
			t.Fatalf("%s: expected a redacted error, got %v", name, err)
		}
	}
	if !errors.Is(writeErr, os.ErrNotExist) {
		t.Fatalf("a redacted error still answers errors.Is, got %v", writeErr)
	}
	blob, _ := json.Marshal(app.EffectLog())
	if strings.Contains(string(blob), redactSecret) {
		t.Fatalf("the redacted value leaked into the live records: %s", blob)
	}
}

func TestRedactHidesTheValueInAStaleBrand(t *testing.T) {
	app, _ := httpFixture(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().Run([]interface{}{"make"})
		stale, _ := ctx.Effects().HTTP("GET", "https://api.test/?key="+redactSecret, Read(), Redact(redactSecret))
		ctx.Effects().Write("REPORT", stale)
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	if !strings.Contains(r.Stdout, "2. write: REPORT («stale: GET https://api.test/?key=«redacted»»)") {
		t.Fatalf("got %q", r.Stdout)
	}
}

func TestRedactRefusesEmptyValues(t *testing.T) {
	cases := []struct {
		opt  EffectOption
		want string
	}{
		{Redact(""), `command "go": effects.run option 'redact' was given an empty value`},
		{Redact("a", ""), `command "go": effects.run option 'redact' was given an empty value`},
		{Redact(), `command "go": effects.run option 'redact' was given no values`},
	}
	for _, c := range cases {
		var err error
		app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
			_, err = ctx.Effects().Run([]interface{}{"make"}, c.opt)
			return Exit(0)
		})
		app.Test([]string{"--dry-run", "go"})
		if err == nil || err.Error() != c.want {
			t.Fatalf("got %v want %q", err, c.want)
		}
	}
}

func TestRedactIsAcceptedByEveryMethod(t *testing.T) {
	calls := map[string]func(*Effects) error{
		"run":    func(e *Effects) error { _, err := e.Run([]interface{}{"x"}, Redact("s")); return err },
		"spawn":  func(e *Effects) error { _, err := e.Spawn([]interface{}{"x"}, Redact("s")); return err },
		"write":  func(e *Effects) error { _, err := e.Write("p", "c", Redact("s")); return err },
		"mkdir":  func(e *Effects) error { _, err := e.Mkdir("p", Redact("s")); return err },
		"remove": func(e *Effects) error { _, err := e.Remove("p", Redact("s")); return err },
		"rename": func(e *Effects) error { _, err := e.Rename("a", "b", Redact("s")); return err },
		"chmod":  func(e *Effects) error { _, err := e.Chmod("p", 0o600, Redact("s")); return err },
		"http":   func(e *Effects) error { _, err := e.HTTP("GET", "https://x.test", Redact("s")); return err },
	}
	for name, call := range calls {
		var err error
		app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
			err = call(ctx.Effects())
			return Exit(0)
		})
		app.Test([]string{"--dry-run", "go"})
		if err != nil {
			t.Fatalf("%s: Redact must be accepted, got %v", name, err)
		}
	}
	s := Spawned{settled: true, cmdPath: "go"}
	if _, err := s.Wait(Redact("")); err == nil || !strings.Contains(err.Error(), "option 'redact' was given an empty value") {
		t.Fatalf("Wait accepts and validates Redact, got %v", err)
	}
}

// --- Mode on Write ------------------------------------------------------------

func TestWriteModeIsAppliedAndRendered(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh")
	existing := filepath.Join(dir, "existing")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errs []error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		for _, p := range []string{fresh, existing} {
			_, err := ctx.Effects().Write(p, "secret", Mode(0o600))
			errs = append(errs, err)
		}
		return Exit(0)
	})
	app.Test([]string{"go"})
	for i, p := range []string{fresh, existing} {
		if errs[i] != nil {
			t.Fatalf("%s: %v", p, errs[i])
		}
		data, _ := os.ReadFile(p)
		if string(data) != "secret" {
			t.Fatalf("%s: content %q", p, data)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("%s: mode %o, want 0600", p, info.Mode().Perm())
			}
		}
	}

	app = effectsApp(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().Write("KEY", "secret", Mode(0o600))
		ctx.Effects().Write("PLAIN", "x")
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	if !strings.Contains(r.Stdout, "1. write: KEY (6 bytes) (mode: 0600)\n") || !strings.Contains(r.Stdout, "2. write: PLAIN (1 bytes)\n") {
		t.Fatalf("got %q", r.Stdout)
	}
	log := app.EffectLog()
	if log[0]["mode"] != "0600" {
		t.Fatalf("expected mode in the record, got %#v", log[0])
	}
	if _, has := log[1]["mode"]; has {
		t.Fatalf("a write without Mode carries no mode: %#v", log[1])
	}
}

func TestWriteModeRefusesNonPermissionBits(t *testing.T) {
	var err error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		_, err = ctx.Effects().Write("p", "c", Mode(os.ModeDir|0o600))
		return Exit(0)
	})
	app.Test([]string{"--dry-run", "go"})
	want := `command "go": effects.write option 'mode' must hold permission bits only, got drw-------`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
}

// --- where each option is refused ---------------------------------------------

func TestReadObserveModeAreRefusedWhereNotAccepted(t *testing.T) {
	cases := []struct {
		name string
		call func(*Effects) error
		want string
	}{
		{"run/read", func(e *Effects) error { _, err := e.Run([]interface{}{"x"}, Read()); return err },
			`command "go": effects.run does not accept option 'read'`},
		{"spawn/read", func(e *Effects) error { _, err := e.Spawn([]interface{}{"x"}, Read()); return err },
			`command "go": effects.spawn does not accept option 'read'`},
		{"write/read", func(e *Effects) error { _, err := e.Write("p", "c", Read()); return err },
			`command "go": effects.write does not accept option 'read'`},
		{"http/observe", func(e *Effects) error { _, err := e.HTTP("GET", "https://x.test", Observe()); return err },
			`command "go": effects.http does not accept option 'observe'`},
		{"spawn/observe", func(e *Effects) error { _, err := e.Spawn([]interface{}{"x"}, Observe()); return err },
			`command "go": effects.spawn does not accept option 'observe'`},
		{"mkdir/observe", func(e *Effects) error { _, err := e.Mkdir("d", Observe()); return err },
			`command "go": effects.mkdir does not accept option 'observe'`},
		{"run/mode", func(e *Effects) error { _, err := e.Run([]interface{}{"x"}, Mode(0o600)); return err },
			`command "go": effects.run does not accept option 'mode'`},
		{"http/mode", func(e *Effects) error { _, err := e.HTTP("GET", "https://x.test", Mode(0o600)); return err },
			`command "go": effects.http does not accept option 'mode'`},
		{"mkdir/mode", func(e *Effects) error { _, err := e.Mkdir("d", Mode(0o700)); return err },
			`command "go": effects.mkdir does not accept option 'mode'`},
		{"chmod/mode", func(e *Effects) error { _, err := e.Chmod("p", 0o600, Mode(0o600)); return err },
			`command "go": effects.chmod does not accept option 'mode'`},
	}
	for _, c := range cases {
		var err error
		app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
			err = c.call(ctx.Effects())
			return Exit(0)
		})
		app.Test([]string{"--dry-run", "go"})
		if err == nil || err.Error() != c.want {
			t.Fatalf("%s: got %v want %q", c.name, err, c.want)
		}
	}
	s := Spawned{settled: true, cmdPath: "go"}
	for opt, want := range map[string]EffectOption{"read": Read(), "observe": Observe(), "mode": Mode(0o600)} {
		_, err := s.Wait(want)
		if err == nil || err.Error() != `command "go": effects.spawn does not accept option '`+opt+`'` {
			t.Fatalf("wait/%s: got %v", opt, err)
		}
	}
}
