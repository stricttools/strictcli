//go:build unix

package strictcli

// The Stdin and Timeout effect options against live child processes.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStdinIsDeliveredToARunChild(t *testing.T) {
	var got Completed
	var runErr error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		got, runErr = ctx.Effects().Run([]interface{}{"cat"}, Stdin([]byte(stdinSecret+"\n")))
		return Exit(0)
	})
	r := app.Test([]string{"--verbose", "go"})
	if runErr != nil {
		t.Fatalf("run: %v (stderr=%q)", runErr, r.Stderr)
	}
	if got.Stdout() != stdinSecret {
		t.Fatalf("the child must read the stdin bytes, got %q", got.Stdout())
	}
	if strings.Contains(r.Stdout+r.Stderr, stdinSecret) {
		t.Fatalf("stdin content leaked into framework output: stdout=%q stderr=%q", r.Stdout, r.Stderr)
	}
	log := app.EffectLog()
	blob, _ := json.Marshal(log)
	if len(log) != 1 || log[0]["stdin_bytes"] != len(stdinSecret)+1 || strings.Contains(string(blob), stdinSecret) {
		t.Fatalf("unexpected live record: %s", blob)
	}
}

func TestStdinIsClosedAfterItsBytes(t *testing.T) {
	// wc -c reads to EOF: it returns only if the framework closes stdin.
	var got Completed
	var runErr error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		got, runErr = ctx.Effects().Run([]interface{}{"wc", "-c"}, Stdin([]byte("12345")), Timeout(10*time.Second))
		return Exit(0)
	})
	app.Test([]string{"go"})
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	if strings.TrimSpace(got.Stdout()) != "5" {
		t.Fatalf("got %q", got.Stdout())
	}
}

func TestStdinCombinesWithStream(t *testing.T) {
	var runErr error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		_, runErr = ctx.Effects().Run([]interface{}{"cat"}, Stdin([]byte("streamed-line\n")), Stream(true))
		return Exit(0)
	})
	r := app.Test([]string{"go"})
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	if !strings.Contains(r.Stdout, "streamed-line") {
		t.Fatalf("a streamed run must still receive its stdin, got stdout=%q", r.Stdout)
	}
}

func TestStdinIsDeliveredToASpawnedChild(t *testing.T) {
	var waitErr error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		s, err := ctx.Effects().Spawn([]interface{}{"cat"}, Stdin([]byte("spawned-line\n")))
		if err != nil {
			waitErr = err
			return Exit(1)
		}
		_, waitErr = s.Wait()
		return Exit(0)
	})
	r := app.Test([]string{"go"})
	if waitErr != nil {
		t.Fatalf("spawn/wait: %v", waitErr)
	}
	if !strings.Contains(r.Stdout, "spawned-line") {
		t.Fatalf("the spawned child must read its stdin, got stdout=%q", r.Stdout)
	}
}

func TestTimeoutKillsARunChildAndNamesIt(t *testing.T) {
	var runErr error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		_, runErr = ctx.Effects().Run([]interface{}{"sleep", "30"}, Timeout(200*time.Millisecond))
		return Exit(0)
	})
	start := time.Now()
	app.Test([]string{"go"})
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the timeout did not bound the run: took %s", elapsed)
	}
	want := `command "go": effects.run timed out: sleep 30 was killed after 200ms`
	if runErr == nil || runErr.Error() != want {
		t.Fatalf("got %v want %q", runErr, want)
	}
}

func TestTimeoutIsIgnoredByAChildThatFinishesInTime(t *testing.T) {
	var got Completed
	var runErr error
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		got, runErr = ctx.Effects().Run([]interface{}{"echo", "done"}, Timeout(10*time.Second))
		return Exit(0)
	})
	app.Test([]string{"go"})
	if runErr != nil || got.Stdout() != "done" {
		t.Fatalf("got %q, %v", got.Stdout(), runErr)
	}
}

func TestWaitTimeoutKillsASpawnedChildAndNamesIt(t *testing.T) {
	var waitErr error
	var pid int
	app := effectsApp(EffectMutating, func(ctx *Context) Outcome {
		s := mustSpawn(ctx, "sleep", "30")
		pid = s.PID()
		_, waitErr = s.Wait(Timeout(200 * time.Millisecond))
		return Exit(0)
	})
	start := time.Now()
	r := app.Test([]string{"go"})
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the timeout did not bound the wait: took %s", elapsed)
	}
	want := `command "go": effects.spawn timed out: sleep 30 was killed after 200ms`
	if waitErr == nil || waitErr.Error() != want {
		t.Fatalf("got %v want %q", waitErr, want)
	}
	if !processGone(pid) {
		t.Fatal("the child is still alive")
	}
	// A child killed by its Wait timeout counts as waited on: the exit step
	// does not report it again.
	if r.ExitCode != 0 || strings.Contains(r.Stderr, "still running") {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}
