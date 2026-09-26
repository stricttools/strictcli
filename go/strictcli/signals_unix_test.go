//go:build unix

package strictcli

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// --- signals (§19.13), through Run in a child process ------------------------

// signalHelperEnv names the helper process mode: the test binary re-executes
// itself, and TestSignalHelperProcess runs a real App.Run there.
const signalHelperEnv = "STRICTCLI_SIGNAL_HELPER"

func TestSignalHelperProcess(t *testing.T) {
	mode := os.Getenv(signalHelperEnv)
	if mode == "" {
		t.Skip("helper process only")
	}
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Info("working")
		sig := syscall.SIGTERM
		if mode == "SIGINT" {
			sig = syscall.SIGINT
		}
		syscall.Kill(os.Getpid(), sig)
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			panic("the handler's context was not canceled")
		}
		ctx.Out("stopping")
		return Exit(7)
	}, WithEffect(EffectReadOnly))
	os.Args = append([]string{"myapp"}, strings.Fields(os.Getenv(signalHelperEnv+"_ARGS"))...)
	app.Run()
}

func runSignalHelper(t *testing.T, sig string, args string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalHelperProcess$")
	cmd.Env = append(os.Environ(), signalHelperEnv+"="+sig, signalHelperEnv+"_ARGS="+args)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

func TestSIGTERMCancelsTheContextAndExits143(t *testing.T) {
	code, stdout, stderr := runSignalHelper(t, "SIGTERM", "cmd")
	if code != 143 || stdout != "working\nstopping\n" || stderr != "error: canceled by signal SIGTERM\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestSIGINTExits130(t *testing.T) {
	code, _, stderr := runSignalHelper(t, "SIGINT", "cmd")
	if code != 130 || stderr != "error: canceled by signal SIGINT\n" {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
}

func TestASignalUnderJSONIsTheLastDiagnostic(t *testing.T) {
	code, stdout, _ := runSignalHelper(t, "SIGTERM", "--json cmd")
	want := envelopeV3("cmd", 143, "null", `"stopping\n"`, `[{"level":"info","message":"working"},{"level":"error","message":"canceled by signal SIGTERM"}]`)
	if code != 143 || stdout != want {
		t.Fatalf("exit=%d stdout=%q", code, stdout)
	}
}

// The file-descriptor redirect on Run: a raw write to fd 1 bypassing
// os.Stdout's buffering entirely is still caught.
const guardHelperEnv = "STRICTCLI_GUARD_HELPER"

func TestGuardHelperProcess(t *testing.T) {
	if os.Getenv(guardHelperEnv) == "" {
		t.Skip("helper process only")
	}
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("cmd", "a command", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		syscall.Write(1, []byte("raw"))
		child := exec.Command("echo", "child")
		child.Stdout = os.Stdout
		child.Run()
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	os.Args = []string{"myapp", "--json", "cmd"}
	app.Run()
}

func TestTheGuardRedirectsTheFileDescriptorOnRun(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestGuardHelperProcess$")
	cmd.Env = append(os.Environ(), guardHelperEnv+"=1")
	out, _ := cmd.Output()
	want := envelopeV3("cmd", 1, "null", "null", `[{"level":"error","message":"stdout written outside the framework: 9 bytes: \"rawchild\\n\""}]`)
	if string(out) != want {
		t.Fatalf("stdout=%q\nwant  %q", out, want)
	}
}
