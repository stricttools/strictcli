//go:build unix

package strictcli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A child still running when its handler ends is killed and fails the run
// (effects contract §19.11's box). Spawned can send a signal and kill.

var killedChildRe = regexp.MustCompile(`^child process (\d+) was still running when the handler ended and was killed: (.*)$`)

func spawnerApp(handler func(ctx *Context, kwargs map[string]interface{}) Outcome) *App {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("start", "start a worker", handler, WithEffect(EffectMutating))
	return app
}

func mustSpawn(ctx *Context, argv ...interface{}) Spawned {
	s, err := ctx.Effects().Spawn(argv)
	if err != nil {
		panic(err)
	}
	return s
}

func processGone(pid int) bool {
	return syscall.Kill(pid, 0) == syscall.ESRCH
}

func TestARunningChildIsKilledAndTheRunFails(t *testing.T) {
	var pid int
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		pid = mustSpawn(ctx, "sleep", "30").PID()
		return Exit(0)
	})
	r := app.Test([]string{"start"})
	want := "error: child process " + strconv.Itoa(pid) + " was still running when the handler ended and was killed: sleep 30\n"
	if r.ExitCode != 1 || r.Stdout != "" || r.Stderr != want {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
	if !processGone(pid) {
		t.Fatal("the child is still alive")
	}
}

func TestAKilledChildKeepsANonzeroStatus(t *testing.T) {
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		mustSpawn(ctx, "sleep", "30")
		return Exit(3)
	})
	if r := app.Test([]string{"start"}); r.ExitCode != 3 {
		t.Fatalf("exit=%d", r.ExitCode)
	}
}

func TestAnEarlyExitsMessageComesBeforeTheKilledChild(t *testing.T) {
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		mustSpawn(ctx, "sleep", "30")
		ExitNow(4, "worker config is missing")
		return Exit(0)
	})
	r := app.Test([]string{"start"})
	lines := strings.Split(strings.TrimSuffix(r.Stderr, "\n"), "\n")
	if r.ExitCode != 4 || len(lines) != 2 || lines[0] != "error: worker config is missing" ||
		!killedChildRe.MatchString(strings.TrimPrefix(lines[1], "error: ")) {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestKilledChildrenAreNamedInSpawnOrder(t *testing.T) {
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		mustSpawn(ctx, "sleep", "30")
		mustSpawn(ctx, "sleep", "31")
		return Exit(0)
	})
	r := app.Test([]string{"start"})
	var argvs []string
	for _, line := range strings.Split(strings.TrimSuffix(r.Stderr, "\n"), "\n") {
		m := killedChildRe.FindStringSubmatch(strings.TrimPrefix(line, "error: "))
		if m == nil {
			t.Fatalf("stderr=%q", r.Stderr)
		}
		argvs = append(argvs, m[2])
	}
	if strings.Join(argvs, "|") != "sleep 30|sleep 31" {
		t.Fatalf("argvs=%v", argvs)
	}
}

func TestAChildIgnoringSIGTERMIsKilledAfterOneSecond(t *testing.T) {
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		mustSpawn(ctx, "sh", "-c", "trap '' TERM; exec sleep 30")
		time.Sleep(200 * time.Millisecond)
		return Exit(0)
	})
	start := time.Now()
	r := app.Test([]string{"start"})
	elapsed := time.Since(start)
	if r.ExitCode != 1 || elapsed < time.Second || elapsed > 10*time.Second {
		t.Fatalf("exit=%d elapsed=%s stderr=%q", r.ExitCode, elapsed, r.Stderr)
	}
}

func TestAWaitedChildIsNotSettledAgain(t *testing.T) {
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		if _, err := mustSpawn(ctx, "true").Wait(); err != nil {
			panic(err)
		}
		return Exit(0)
	})
	if r := app.Test([]string{"start"}); r.ExitCode != 0 || r.Stderr != "" {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestAnExitedChildLeftUnwaitedIsDrainedNotAnError(t *testing.T) {
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		mustSpawn(ctx, "echo", "hi")
		time.Sleep(500 * time.Millisecond)
		return Exit(0)
	})
	r := app.Test([]string{"--json", "start"})
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		t.Fatalf("stdout=%q", r.Stdout)
	}
	if r.ExitCode != 0 || doc["output"] != "hi\n" || len(doc["diagnostics"].([]interface{})) != 0 {
		t.Fatalf("exit=%d stdout=%q", r.ExitCode, r.Stdout)
	}
}

func TestUnderJSONTheKillIsADiagnosticAndWhatTheChildWroteIsKept(t *testing.T) {
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		mustSpawn(ctx, "sh", "-c", "echo early; exec sleep 30")
		time.Sleep(300 * time.Millisecond)
		return Exit(0)
	})
	r := app.Test([]string{"--json", "start"})
	var doc struct {
		ExitCode    int     `json:"exit_code"`
		Output      *string `json:"output"`
		Diagnostics []diagnosticRecord
	}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		t.Fatalf("stdout=%q", r.Stdout)
	}
	if r.ExitCode != 1 || r.Stderr != "" || doc.ExitCode != 1 || doc.Output == nil || *doc.Output != "early\n" ||
		len(doc.Diagnostics) != 1 || doc.Diagnostics[0].Level != "error" ||
		!killedChildRe.MatchString(doc.Diagnostics[0].Message) {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestKillEndsTheChildAndCountsAsAWait(t *testing.T) {
	var pid int
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		child := mustSpawn(ctx, "sleep", "30")
		pid = child.PID()
		if err := child.Kill(); err != nil {
			panic(err)
		}
		if !processGone(pid) {
			panic("the child is still alive after Kill")
		}
		c, err := child.Wait(Check(false))
		if err != nil || c.ExitCode() == 0 {
			panic("a killed child's wait reports a nonzero status")
		}
		return Exit(0)
	})
	if r := app.Test([]string{"start"}); r.ExitCode != 0 || r.Stderr != "" {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestSignalDeliversAndLeavesTheWaitToTheHandler(t *testing.T) {
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		child := mustSpawn(ctx, "sleep", "30")
		if err := child.Signal(syscall.SIGTERM); err != nil {
			panic(err)
		}
		if _, err := child.Wait(Check(false)); err != nil {
			panic(err)
		}
		return Exit(0)
	})
	if r := app.Test([]string{"start"}); r.ExitCode != 0 || r.Stderr != "" {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestSignalAndKillLeaveAnExitedChildAlone(t *testing.T) {
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		child := mustSpawn(ctx, "true")
		if _, err := child.Wait(); err != nil {
			panic(err)
		}
		if err := child.Signal(syscall.SIGTERM); err != nil {
			panic(err)
		}
		if err := child.Kill(); err != nil {
			panic(err)
		}
		return Exit(0)
	})
	if r := app.Test([]string{"start"}); r.ExitCode != 0 || r.Stderr != "" {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestSignalAndKillTruncateOnAnUnsettledHandle(t *testing.T) {
	for _, use := range []func(Spawned){
		func(s Spawned) { s.Kill() },
		func(s Spawned) { s.Signal(syscall.SIGTERM) },
	} {
		app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
			use(mustSpawn(ctx, "sleep", "30"))
			return Exit(0)
		})
		r := app.Test([]string{"--dry-run", "start"})
		if r.ExitCode != 1 || !strings.Contains(r.Stderr, "dry-run preview ends at step") {
			t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
		}
	}
}

func TestCallKillsTheChildAndReportsTheFailedStatus(t *testing.T) {
	var pid int
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		pid = mustSpawn(ctx, "sleep", "30").PID()
		return Exit(0)
	})
	got, err := app.Call("start", nil)
	if err != nil || got != 1 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if !processGone(pid) {
		t.Fatal("the child is still alive")
	}
}

// Signal handling lasts until the children are settled (§19.13's box): the
// child ignores SIGTERM, so the exit step waits a second before its SIGKILL,
// and a SIGINT sent during that wait is the run's first signal.
const childSignalHelperEnv = "STRICTCLI_CHILD_SIGNAL_HELPER"

func TestChildSignalHelperProcess(t *testing.T) {
	if os.Getenv(childSignalHelperEnv) == "" {
		t.Skip("helper process only")
	}
	app := spawnerApp(func(ctx *Context, _ map[string]interface{}) Outcome {
		mustSpawn(ctx, "sh", "-c", "trap '' TERM; exec sleep 30")
		time.Sleep(200 * time.Millisecond)
		go func() {
			time.Sleep(400 * time.Millisecond)
			syscall.Kill(os.Getpid(), syscall.SIGINT)
		}()
		return Exit(0)
	})
	os.Args = []string{"myapp", "start"}
	app.Run()
}

func TestSignalHandlingLastsUntilTheChildrenAreSettled(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestChildSignalHelperProcess$")
	cmd.Env = append(os.Environ(), childSignalHelperEnv+"=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 130 {
		t.Fatalf("err=%v stderr=%q", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	if len(lines) != 2 || !killedChildRe.MatchString(strings.TrimPrefix(lines[0], "error: ")) ||
		lines[1] != "error: canceled by signal SIGINT" {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
