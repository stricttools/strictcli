package strictcli

import (
	"os"
	"strings"
	"testing"
)

// overlap coordinates the two commands the overlap tests run: "first"
// starts, waits until "second" has started, and returns; "second" waits until
// first's Test call has returned, then streams a child's output. The two calls
// therefore overlap without nesting, which is the order a process-wide stream
// swap cannot survive.
type overlap struct {
	firstStarted, secondStarted, firstDone chan struct{}
}

func newOverlap() *overlap {
	return &overlap{
		firstStarted:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		firstDone:     make(chan struct{}),
	}
}

func (o *overlap) registerFirst(app *App) {
	app.Command("first", "the call that starts first", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Out("first-out " + Get[string](kwargs, "label"))
		ctx.Warn("first-err")
		if _, err := ctx.Effects().Write("first.txt", "x"); err != nil {
			panic(err)
		}
		close(o.firstStarted)
		<-o.secondStarted
		return Exit(3)
	}, WithEffect(EffectMutating), WithFlags(StringFlag("label", "a label echoed back", Required())))
}

func (o *overlap) registerSecond(app *App) {
	app.Command("second", "the call that starts second and ends last", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		close(o.secondStarted)
		<-o.firstDone
		if _, err := ctx.Effects().Run([]interface{}{"sh", "-c", "echo second-child; echo second-child-err >&2"}, Stream(true)); err != nil {
			panic(err)
		}
		ctx.Out("second-out " + Get[string](kwargs, "label"))
		return Exit(5)
	}, WithEffect(EffectMutating), WithFlags(StringFlag("label", "a label echoed back", Required())))
}

// runOverlap runs first on firstApp and second on secondApp, overlapping as
// described above, and returns both results.
func runOverlap(t *testing.T, o *overlap, firstApp, secondApp *App) (Result, Result) {
	t.Helper()
	firstResult := make(chan Result, 1)
	go func() {
		firstResult <- firstApp.Test([]string{"--dry-run", "first", "--label", "one"})
	}()
	<-o.firstStarted
	secondResult := make(chan Result, 1)
	go func() {
		secondResult <- secondApp.Test([]string{"second", "--label", "two"})
	}()
	first := <-firstResult
	close(o.firstDone)
	second := <-secondResult
	return first, second
}

func assertOverlapResults(t *testing.T, first, second Result) {
	t.Helper()
	if first.ExitCode != 3 {
		t.Errorf("first exit = %d, want 3; stdout=%q stderr=%q", first.ExitCode, first.Stdout, first.Stderr)
	}
	if !strings.Contains(first.Stdout, "first-out one") || !strings.Contains(first.Stdout, "first.txt") {
		t.Errorf("first stdout lacks its own output or its would-do log: %q", first.Stdout)
	}
	if !strings.Contains(first.Stderr, "first-err") {
		t.Errorf("first stderr lacks its own diagnostic: %q", first.Stderr)
	}
	if strings.Contains(first.Stdout+first.Stderr, "second") {
		t.Errorf("first sees second's output: stdout=%q stderr=%q", first.Stdout, first.Stderr)
	}
	if second.ExitCode != 5 {
		t.Errorf("second exit = %d, want 5; stdout=%q stderr=%q", second.ExitCode, second.Stdout, second.Stderr)
	}
	if !strings.Contains(second.Stdout, "second-child\n") || !strings.Contains(second.Stdout, "second-out two") {
		t.Errorf("second stdout lacks its child's or its own output: %q", second.Stdout)
	}
	if !strings.Contains(second.Stderr, "second-child-err") {
		t.Errorf("second stderr lacks its child's stderr: %q", second.Stderr)
	}
	if strings.Contains(second.Stdout+second.Stderr, "first") {
		t.Errorf("second sees first's output: stdout=%q stderr=%q", second.Stdout, second.Stderr)
	}
}

func TestOverlappingTestCallsOnOneAppShareNothing(t *testing.T) {
	savedStdout, savedStderr := os.Stdout, os.Stderr
	o := newOverlap()
	app := NewApp("myapp", "1.0.0", "test app")
	o.registerFirst(app)
	o.registerSecond(app)
	first, second := runOverlap(t, o, app, app)
	assertOverlapResults(t, first, second)
	if os.Stdout != savedStdout || os.Stderr != savedStderr {
		t.Errorf("Test left the process streams replaced")
	}
	// EffectLog is the most recently finished dispatch's: second's.
	log := app.EffectLog()
	if len(log) != 1 || !strings.Contains(log[0]["detail"].(string), "second-child") {
		t.Errorf("EffectLog() = %v, want second's one run", log)
	}
}

func TestOverlappingTestCallsOnTwoAppsShareNothing(t *testing.T) {
	savedStdout, savedStderr := os.Stdout, os.Stderr
	o := newOverlap()
	firstApp := NewApp("myapp", "1.0.0", "test app")
	o.registerFirst(firstApp)
	secondApp := NewApp("myapp", "1.0.0", "test app")
	o.registerSecond(secondApp)
	first, second := runOverlap(t, o, firstApp, secondApp)
	assertOverlapResults(t, first, second)
	if os.Stdout != savedStdout || os.Stderr != savedStderr {
		t.Errorf("Test left the process streams replaced")
	}
}
