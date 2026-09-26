package strictcli

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The output writers, the prefixes, the declared rendering, the runtime guard,
// a child's stdout in machine mode, and signals (effects contract §19.6's
// document-writer amendment, §19.10-§19.14).

func outApp(handler func(ctx *Context, kwargs map[string]interface{}) Outcome, opts ...CmdOption) *App {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", handler, append([]CmdOption{WithEffect(EffectReadOnly)}, opts...)...)
	return app
}

func envelopeV3(command string, exitCode int, payload, output string, diagnostics string) string {
	return fmt.Sprintf(`{"interface_version":3,"app":"myapp","app_version":"1.0.0","command":"%s","exit_code":%d,"payload":%s,"output":%s,"dry_run":false,"writes":null,"preview":[],"preview_error":null,"diagnostics":%s}`+"\n",
		command, exitCode, payload, output, diagnostics)
}

func mustPanicWith(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		t.Helper()
		if r := recover(); r != want {
			t.Fatalf("panicked with %v, want %q", r, want)
		}
	}()
	fn()
	t.Fatalf("no panic, want %q", want)
}

// --- prefixes (§19.14) ------------------------------------------------------

func TestWarnAndErrorArePrefixedInHumanMode(t *testing.T) {
	app := outApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Warn("disk almost full")
		ctx.Error("error: upload failed")
		ctx.Info("loading")
		return Exit(0)
	})
	r := app.Test([]string{"cmd"})
	if r.Stderr != "warning: disk almost full\nerror: error: upload failed\n" || r.Stdout != "loading\n" {
		t.Fatalf("stdout=%q stderr=%q", r.Stdout, r.Stderr)
	}
	r = app.Test([]string{"--json", "cmd"})
	want := envelopeV3("cmd", 0, "null", "null", `[{"level":"warn","message":"disk almost full"},{"level":"error","message":"error: upload failed"},{"level":"info","message":"loading"}]`)
	if r.Stdout != want {
		t.Fatalf("stdout=%q\nwant  %q", r.Stdout, want)
	}
}

// --- ctx.Out (§19.10) ---------------------------------------------------------

func TestOutWritesTheAnswerAndQuietNeverHidesIt(t *testing.T) {
	app := outApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Info("loading")
		ctx.Out("hello")
		ctx.Out("world")
		return Exit(0)
	})
	if r := app.Test([]string{"--quiet", "cmd"}); r.Stdout != "hello\nworld\n" {
		t.Fatalf("stdout=%q", r.Stdout)
	}
	r := app.Test([]string{"--json", "--quiet", "cmd"})
	if want := envelopeV3("cmd", 0, "null", `"hello\nworld\n"`, `[{"level":"info","message":"loading"}]`); r.Stdout != want {
		t.Fatalf("stdout=%q\nwant  %q", r.Stdout, want)
	}
}

func TestOutIsRefusedOnOwnsStdoutAndRendererCommands(t *testing.T) {
	ctx := newContext(nil, nil, nil, nil, reservedFlags{}, nil)
	ctx.commandName = "dump"
	ctx.ownsStdout = true
	mustPanicWith(t, errOutOnOwnsStdout("dump"), func() { ctx.Out("x") })
	ctx = newContext(nil, nil, nil, nil, reservedFlags{}, nil)
	ctx.commandName = "status"
	ctx.renderer = func(interface{}) string { return "" }
	mustPanicWith(t, errOutWithRenderer("status"), func() { ctx.Out("x") })
}

// --- ctx.Document (§19.6's box) ---------------------------------------------

func TestDocumentWritesTheArtifactInBothModes(t *testing.T) {
	app := outApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		fmt.Fprint(ctx.Document(), "CREATE TABLE t (id int);\n")
		return Exit(0)
	}, OwnsStdout())
	if r := app.Test([]string{"--quiet", "cmd"}); r.Stdout != "CREATE TABLE t (id int);\n" || r.Stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", r.Stdout, r.Stderr)
	}
	r := app.Test([]string{"--json", "cmd"})
	if r.Stdout != "CREATE TABLE t (id int);\n" || r.Stderr != envelopeV3("cmd", 0, "null", "null", "[]") {
		t.Fatalf("stdout=%q stderr=%q", r.Stdout, r.Stderr)
	}
}

func TestDocumentRequiresTheOwnsStdoutDeclaration(t *testing.T) {
	ctx := newContext(nil, nil, nil, nil, reservedFlags{}, nil)
	ctx.commandName = "dump"
	mustPanicWith(t, errDocumentWithoutOwnsStdout("dump"), func() { ctx.Document() })
}

// --- the declared rendering (§19.10) ------------------------------------------

func statusRenderer(p interface{}) string {
	m := p.(map[string]interface{})
	return fmt.Sprintf("%v: %v rows", m["table"], m["rows"])
}

func TestTheRendererPrintsThePayloadInHumanModeOnly(t *testing.T) {
	app := outApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"table": "users", "rows": 3})
		return Exit(0)
	}, PayloadSchema(map[string]interface{}{"type": "object"}), PayloadRenderer(statusRenderer))
	if r := app.Test([]string{"--quiet", "cmd"}); r.Stdout != "users: 3 rows\n" {
		t.Fatalf("stdout=%q", r.Stdout)
	}
	r := app.Test([]string{"--json", "cmd"})
	if want := envelopeV3("cmd", 0, `{"rows":3,"table":"users"}`, "null", "[]"); r.Stdout != want {
		t.Fatalf("stdout=%q", r.Stdout)
	}
}

func TestTheRendererRendersAPayloadKeptByAnEarlyExit(t *testing.T) {
	app := outApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Payload(map[string]interface{}{"table": "users", "rows": 3})
		ExitNow(2, "partial")
		return Exit(0)
	}, PayloadSchema(map[string]interface{}{"type": "object"}), PayloadRenderer(statusRenderer))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 2 || r.Stdout != "users: 3 rows\n" || r.Stderr != "error: partial\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestTheRendererRegistrationRefusals(t *testing.T) {
	h := func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) }
	mustPanicWith(t, errRendererWithoutPayloadSchema("status"), func() {
		NewApp("a", "1", "a").Command("status", "s", h, WithEffect(EffectReadOnly), PayloadRenderer(statusRenderer))
	})
	mustPanicWith(t, errRendererOnOwnsStdout("dump"), func() {
		NewApp("a", "1", "a").Command("dump", "d", h, WithEffect(EffectReadOnly), OwnsStdout(),
			PayloadSchema(map[string]interface{}{}), PayloadRenderer(statusRenderer))
	})
}

// --- the runtime guard (§19.12) ---------------------------------------------

func TestAStrayPrintlnUnderJSONFailsTheRun(t *testing.T) {
	app := outApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Warn("slow disk")
		ctx.Out("done")
		fmt.Println("stray")
		return Exit(0)
	})
	r := app.Test([]string{"--json", "cmd"})
	want := envelopeV3("cmd", 1, "null", `"done\n"`, `[{"level":"warn","message":"slow disk"},{"level":"error","message":"stdout written outside the framework: 6 bytes: \"stray\\n\""}]`)
	if r.ExitCode != 1 || r.Stdout != want || r.Stderr != "" {
		t.Fatalf("exit=%d stdout=%q\nwant       %q", r.ExitCode, r.Stdout, want)
	}
	// Human mode redirects nothing.
	if r := app.Test([]string{"cmd"}); r.ExitCode != 0 || r.Stdout != "done\nstray\n" {
		t.Fatalf("exit=%d stdout=%q", r.ExitCode, r.Stdout)
	}
}

func TestTheGuardKeepsANonzeroCodeAndQuotesTheFirst4096Bytes(t *testing.T) {
	app := outApp(func(ctx *Context, kwargs map[string]interface{}) Outcome {
		os.Stdout.WriteString(strings.Repeat("a", 5000))
		return Exit(4)
	})
	r := app.Test([]string{"--json", "cmd"})
	diag := fmt.Sprintf(`stdout written outside the framework: 5000 bytes: \"%s\"`, strings.Repeat("a", 4096))
	if want := envelopeV3("cmd", 4, "null", "null", `[{"level":"error","message":"`+diag+`"}]`); r.ExitCode != 4 || r.Stdout != want {
		t.Fatalf("exit=%d stdout=%q", r.ExitCode, r.Stdout)
	}
}

func TestTheGuardExcerptDecodesInvalidUTF8ByTheReplacementRule(t *testing.T) {
	s := &strayStdout{total: 4, head: []byte{0xE2, 0x82, 'A', 0xFF}}
	if got, want := s.diagnostic(), "stdout written outside the framework: 4 bytes: \"�A�\""; got != want {
		t.Fatalf("diagnostic=%q want %q", got, want)
	}
}

func TestDecodeUTF8ReplacingFollowsTheMaximalSubpartRule(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte("héllo"), "héllo"},
		{[]byte{0xF0, 0x90, 0x80}, "�"},
		{[]byte{0xED, 0xA0, 0x80}, "���"},
		{[]byte{0xC0, 0xAF}, "��"},
		{[]byte{0xE0, 0x80, 'x'}, "��x"},
	}
	for _, c := range cases {
		if got, _ := decodeUTF8Replacing(c.in, true); got != c.want {
			t.Errorf("decode(% x) = %q, want %q", c.in, got, c.want)
		}
	}
	// A sequence split across two chunks is held back, not replaced.
	cc := &childCapture{out: &outputMember{}}
	cc.Write([]byte{'a', 0xE2, 0x82})
	cc.Write([]byte{0xAC, 'b'})
	cc.close()
	if v := cc.out.value(); v == nil || *v != "a€b" {
		t.Fatalf("captured %v", v)
	}
}

// --- a child's stdout in machine mode (§19.11) ------------------------------

func childApp(stream func(ctx *Context) error, opts ...CmdOption) *App {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		if err := stream(ctx); err != nil {
			panic(err)
		}
		return Exit(0)
	}, append([]CmdOption{WithEffect(EffectMutating)}, opts...)...)
	return app
}

func TestAStreamedRunsStdoutIsCapturedIntoOutputUnderJSON(t *testing.T) {
	app := childApp(func(ctx *Context) error {
		_, err := ctx.Effects().Run([]interface{}{"echo", "performed"}, Stream(true))
		return err
	})
	r := app.Test([]string{"--json", "cmd"})
	if r.ExitCode != 0 || !strings.HasPrefix(r.Stdout, "{") || !strings.Contains(r.Stdout, `"output":"performed\n"`) {
		t.Fatalf("exit=%d stdout=%q", r.ExitCode, r.Stdout)
	}
	if r := app.Test([]string{"cmd"}); r.Stdout != "performed\n" {
		t.Fatalf("human stdout=%q", r.Stdout)
	}
}

func TestASpawnedChildsStdoutIsCapturedEvenWhenNeverWaitedOn(t *testing.T) {
	app := childApp(func(ctx *Context) error {
		_, err := ctx.Effects().Spawn([]interface{}{"sh", "-c", "sleep 0.1; echo late"})
		return err
	})
	r := app.Test([]string{"--json", "cmd"})
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, `"output":"late\n"`) {
		t.Fatalf("exit=%d stdout=%q", r.ExitCode, r.Stdout)
	}
}

func TestAnOwnsStdoutCommandsChildWritesTheDocument(t *testing.T) {
	app := childApp(func(ctx *Context) error {
		s, err := ctx.Effects().Spawn([]interface{}{"echo", "dumped"})
		if err != nil {
			return err
		}
		_, err = s.Wait()
		return err
	}, OwnsStdout())
	r := app.Test([]string{"--json", "cmd"})
	if r.ExitCode != 0 || r.Stdout != "dumped\n" || !strings.Contains(r.Stderr, `"output":null`) {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
}
