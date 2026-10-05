package strictcli

// The Stdin and Timeout effect options: where they are accepted, how a
// non-positive timeout is refused, and how both render in the effect log
// without ever echoing stdin content.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const stdinSecret = "hunter2-secret-token"

func TestStdinAndTimeoutRenderInTheWouldDoLogWithoutContent(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		e := ctx.Effects()
		e.Run([]interface{}{"security", "unlock-keychain"}, Stdin([]byte(stdinSecret)), Timeout(30*time.Second))
		e.Spawn([]interface{}{"ssh", "mac", "sudo", "-S", "true"}, Stdin([]byte(stdinSecret)))
		e.Run([]interface{}{"make"}, Timeout(1500*time.Millisecond), Stdin(nil))
		return Exit(0)
	})
	r := app.Test([]string{"--dry-run", "go"})
	want := "DRY RUN — no changes were made. Would do:\n" +
		"  1. run: security unlock-keychain (stdin: 20 bytes, content withheld) (timeout: 30s)\n" +
		"  2. spawn: ssh mac sudo -S true (stdin: 20 bytes, content withheld)\n" +
		"  3. run: make (stdin: 0 bytes, content withheld) (timeout: 1.5s)\n"
	if r.Stdout != want {
		t.Fatalf("got:\n%q\nwant:\n%q", r.Stdout, want)
	}
	log := app.EffectLog()
	if len(log) != 3 {
		t.Fatalf("expected 3 records, got %#v", log)
	}
	if log[0]["stdin_bytes"] != 20 || log[0]["timeout"] != "30s" {
		t.Fatalf("unexpected run record: %#v", log[0])
	}
	if log[1]["stdin_bytes"] != 20 {
		t.Fatalf("unexpected spawn record: %#v", log[1])
	}
	if _, has := log[1]["timeout"]; has {
		t.Fatalf("a record without a timeout must not carry one: %#v", log[1])
	}
	if log[2]["stdin_bytes"] != 0 || log[2]["timeout"] != "1.5s" {
		t.Fatalf("unexpected record: %#v", log[2])
	}
	blob, err := json.Marshal(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), stdinSecret) {
		t.Fatalf("stdin content leaked into the record JSON: %s", blob)
	}
}

func TestRecordsWithoutStdinOrTimeoutCarryNeither(t *testing.T) {
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		ctx.Effects().Run([]interface{}{"make"})
		return Exit(0)
	})
	app.Test([]string{"--dry-run", "go"})
	log := app.EffectLog()
	if len(log) != 1 {
		t.Fatalf("expected 1 record, got %#v", log)
	}
	for _, key := range []string{"stdin_bytes", "timeout"} {
		if _, has := log[0][key]; has {
			t.Fatalf("record carries %q without the option: %#v", key, log[0])
		}
	}
}

func TestStdinContentNeverReachesAnyOutputStream(t *testing.T) {
	for _, argv := range [][]string{
		{"--dry-run", "go"},
		{"--dry-run", "--verbose", "go"},
		{"--json", "--dry-run", "go"},
		{"--json", "--dry-run", "--verbose", "go"},
	} {
		app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
			ctx.Effects().Run([]interface{}{"security", "unlock"}, Stdin([]byte(stdinSecret)), Timeout(time.Second))
			ctx.Effects().Spawn([]interface{}{"ssh", "mac"}, Stdin([]byte(stdinSecret)))
			return Exit(0)
		})
		r := app.Test(argv)
		all := r.Stdout + r.Stderr
		if !strings.Contains(all, "20 bytes") && !strings.Contains(all, `"stdin_bytes":20`) {
			t.Fatalf("%v: expected the byte count in the output, got stdout=%q stderr=%q", argv, r.Stdout, r.Stderr)
		}
		if strings.Contains(r.Stdout+r.Stderr, stdinSecret) {
			t.Fatalf("%v: stdin content leaked: stdout=%q stderr=%q", argv, r.Stdout, r.Stderr)
		}
	}
}

func TestStdinAndTimeoutAreRefusedWhereNotAccepted(t *testing.T) {
	cases := []struct {
		name string
		call func(*Effects) error
		want string
	}{
		{"write/stdin", func(e *Effects) error { _, err := e.Write("p", "c", Stdin([]byte("s"))); return err },
			`command "go": effects.write does not accept option 'stdin'`},
		{"http/stdin", func(e *Effects) error {
			_, err := e.HTTP("POST", "https://x.test", Stdin([]byte("s")))
			return err
		},
			`command "go": effects.http does not accept option 'stdin'`},
		{"mkdir/stdin", func(e *Effects) error { _, err := e.Mkdir("d", Stdin([]byte("s"))); return err },
			`command "go": effects.mkdir does not accept option 'stdin'`},
		{"spawn/timeout", func(e *Effects) error {
			_, err := e.Spawn([]interface{}{"x"}, Timeout(time.Second))
			return err
		},
			`command "go": effects.spawn does not accept option 'timeout'`},
		{"rename/timeout", func(e *Effects) error { _, err := e.Rename("a", "b", Timeout(time.Second)); return err },
			`command "go": effects.rename does not accept option 'timeout'`},
		{"remove/timeout", func(e *Effects) error { _, err := e.Remove("p", Timeout(time.Second)); return err },
			`command "go": effects.remove does not accept option 'timeout'`},
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
}

func TestWaitRefusesStdin(t *testing.T) {
	s := Spawned{settled: true, cmdPath: "go"}
	_, err := s.Wait(Stdin([]byte("s")))
	want := `command "go": effects.spawn does not accept option 'stdin'`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v want %q", err, want)
	}
}

func TestANonPositiveTimeoutIsRefused(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		var runErr error
		app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
			_, runErr = ctx.Effects().Run([]interface{}{"make"}, Timeout(d))
			return Exit(0)
		})
		r := app.Test([]string{"--dry-run", "go"})
		want := `command "go": effects.run option 'timeout' must be a positive duration, got ` + d.String()
		if runErr == nil || runErr.Error() != want {
			t.Fatalf("run %v: got %v want %q", d, runErr, want)
		}
		// A refused call records nothing.
		if strings.Contains(r.Stdout, "1. run") {
			t.Fatalf("a refused call must not be recorded, got %q", r.Stdout)
		}

		s := Spawned{settled: true, cmdPath: "go"}
		_, waitErr := s.Wait(Timeout(d))
		want = `command "go": effects.spawn option 'timeout' must be a positive duration, got ` + d.String()
		if waitErr == nil || waitErr.Error() != want {
			t.Fatalf("wait %v: got %v want %q", d, waitErr, want)
		}
	}
}
