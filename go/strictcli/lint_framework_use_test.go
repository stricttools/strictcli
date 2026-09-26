package strictcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The framework-use lint (effects contract §28): the Go scanner.

const lintModule = "example.com/tool"

func writeFixture(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func lintFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := gitTempDir(t)
	all := map[string]string{"go.mod": "module " + lintModule + " // the tool\n\ngo 1.22\n"}
	for k, v := range files {
		all[k] = v
	}
	writeFixture(t, root, all)
	return root
}

func lintLines(t *testing.T, root string) []string {
	t.Helper()
	findings, refusal := lintGoProgram(root, lintModule)
	if refusal != "" {
		t.Fatalf("refused: %s", refusal)
	}
	return findings
}

const lintMain = `package main

import (
	"fmt"
	o "os"

	"example.com/tool/internal/report"
	"github.com/stricttools/strictcli/go/strictcli"
)

func main() {
	app := strictcli.NewApp("tool", "1.0.0", "a tool")
	report.Print()
	if len(o.Args) > 1 { fmt.Println("x"); o.Exit(2) }
	go func() {
		strictcli.ExitNow(1, "in a raw goroutine")
	}()
	app.Run()
}
`

const lintReport = `package report

import (
	"flag"
	"log"
	. "os"
	"syscall"
)

var _ = flag.Parse

func Print() {
	println("dbg")
	log.Fatalf("x %d", 1)
	Getenv("HOME")
	_ = syscall.Environ()
	_ = Stderr
}
`

func TestLintFindsEveryRefusedConstructInTheLinkedPackages(t *testing.T) {
	root := lintFixture(t, map[string]string{
		"cmd/tool/main.go":          lintMain,
		"internal/report/print.go":  lintReport,
		"internal/report/x_test.go": "package report\n\nimport \"os\"\n\nfunc init() { os.Exit(3) }\n",
		// Another program: it does not import strictcli, so it is not scanned.
		"scripts/gen/main.go": "package main\n\nimport \"os\"\n\nfunc main() { os.Exit(1) }\n",
		// An unlinked package of the module: not scanned.
		"internal/unused/u.go": "package unused\n\nimport \"os\"\n\nfunc U() { os.Exit(1) }\n",
		// testdata and a nested module: never scanned.
		"cmd/tool/testdata/t.go": "package main\n\nimport \"os\"\n\nfunc main() { os.Exit(1) }\n",
		"sub/go.mod":             "module example.com/sub\n",
		"sub/main.go":            "package main\n\nimport (\n\t\"os\"\n\t\"github.com/stricttools/strictcli/go/strictcli\"\n)\n\nvar _ = strictcli.NewApp\n\nfunc main() { os.Exit(1) }\n",
	})
	got := lintLines(t, root)
	stdoutMsg := func(c string) string { return errLintStdoutWrite(c, "ctx.Out", "ctx.Payload", "ctx.Document()") }
	stderrMsg := func(c string) string { return errLintStderrWrite(c, "ctx.Warn", "ctx.Error") }
	want := []string{
		"cmd/tool/main.go:14: argv-access: " + errLintArgvAccess("os.Args"),
		"cmd/tool/main.go:14: process-exit: " + errLintProcessExit("os.Exit", "strictcli.ExitNow(code, message)"),
		"cmd/tool/main.go:14: stdout-write: " + stdoutMsg("fmt.Println"),
		"cmd/tool/main.go:16: exit-now-in-goroutine: " + errLintExitNowInGoroutine,
		"internal/report/print.go:4: argv-access: " + errLintArgvAccess("flag"),
		"internal/report/print.go:13: stderr-write: " + stderrMsg("println"),
		"internal/report/print.go:14: process-exit: " + errLintProcessExit("log.Fatalf", "strictcli.ExitNow(code, message)"),
		"internal/report/print.go:15: environment-read: " + errLintEnvironmentRead("os.Getenv"),
		"internal/report/print.go:16: environment-read: " + errLintEnvironmentRead("syscall.Environ"),
		"internal/report/print.go:17: stderr-write: " + stderrMsg("os.Stderr"),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLintIsCleanOnAProgramThatUsesTheFramework(t *testing.T) {
	root := lintFixture(t, map[string]string{
		"main.go": "package main\n\nimport \"github.com/stricttools/strictcli/go/strictcli\"\n\nfunc main() { strictcli.NewApp(\"t\", \"1\", \"t\").Run() }\n",
	})
	if got := lintLines(t, root); len(got) != 0 {
		t.Fatalf("findings: %v", got)
	}
}

func TestLintRefusals(t *testing.T) {
	empty := t.TempDir()
	if _, r := lintGoProgram(empty, lintModule); r != errLintFrameworkUseNoManifest("go.mod", empty) {
		t.Fatalf("refusal = %q", r)
	}
	root := lintFixture(t, nil)
	if _, r := lintGoProgram(root, "example.com/other"); r != errLintFrameworkUseManifestMismatch("go.mod", root) {
		t.Fatalf("refusal = %q", r)
	}
	notRepo := t.TempDir()
	writeFixture(t, notRepo, map[string]string{"go.mod": "module " + lintModule + "\n"})
	if _, r := lintGoProgram(notRepo, lintModule); r != errLintFrameworkUseNotWorkTree(notRepo) {
		t.Fatalf("refusal = %q", r)
	}
}

func TestLintFlagMustBeTheOnlyArgument(t *testing.T) {
	app := outApp(func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) })
	r := app.Test([]string{"--lint-framework-use", "cmd"})
	if r.ExitCode != 1 || r.Stdout != "" || !strings.HasPrefix(r.Stderr, "error: "+errLintFrameworkUseArgs+"\n") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
	r = app.Test([]string{"cmd", "--lint-framework-use"})
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "error: unknown flag '--lint-framework-use'") {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestLintFlagNameIsReservedForGlobals(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), `global flag name "lint-framework-use" is reserved`) {
			t.Fatalf("recovered %v", r)
		}
	}()
	NewApp("a", "1", "a").GlobalFlag(BoolFlag("lint-framework-use", "x", Required()))
}

func TestLintFlagRunsTheScanOnTheWorkingDirectory(t *testing.T) {
	root := lintFixture(t, map[string]string{
		"main.go": "package main\n\nimport (\n\t\"os\"\n\t\"github.com/stricttools/strictcli/go/strictcli\"\n)\n\nfunc main() { strictcli.NewApp(\"t\", \"1\", \"t\"); os.Exit(0) }\n",
	})
	t.Chdir(root)
	app := outApp(func(ctx *Context, kwargs map[string]interface{}) Outcome { return Exit(0) })
	app.lintMainModule = func() (string, bool) { return lintModule, true }
	r := app.Test([]string{"--lint-framework-use"})
	want := "main.go:8: process-exit: " + errLintProcessExit("os.Exit", "strictcli.ExitNow(code, message)") + "\n"
	if r.ExitCode != 1 || r.Stdout != want || r.Stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
	app.lintMainModule = func() (string, bool) { return "example.com/else", true }
	r = app.Test([]string{"--lint-framework-use"})
	if r.ExitCode != 1 || r.Stdout != "" || r.Stderr != "error: "+errLintFrameworkUseManifestMismatch("go.mod", root)+"\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
}
