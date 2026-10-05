package strictcli

import (
	"strings"
	"testing"
)

// A flag registered with Optional() + Choices(Ch(..., "")) means "optional; if
// passed, must be one of the choices". When the flag is NOT passed, its
// resolved value is nil and choices validation must be skipped -- nil only
// arises from Optional()/ArgOptional()/unset mutex members; a CLI-supplied
// value is never nil. The default-in-choices check applies to declared VALUES
// only, never to absence (contract §23.5).

func TestFlagOptionalChoicesNotPassed(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format", Optional(), Choices(Ch("text", ""), Ch("json", "")))))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "format=None\n" {
		t.Fatalf("expected 'format=None', got %q", r.Stdout)
	}
}

func TestFlagOptionalChoicesPassedValid(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format", Optional(), Choices(Ch("text", ""), Ch("json", "")))))
	r := app.Test([]string{"cmd", "--format", "json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "format=json\n" {
		t.Fatalf("expected 'format=json', got %q", r.Stdout)
	}
}

func TestFlagOptionalChoicesPassedInvalid(t *testing.T) {
	app := simpleApp("cmd", "a command", "format={format}",
		WithFlags(StringFlag("format", "output format", Optional(), Choices(Ch("text", ""), Ch("json", "")))))
	r := app.Test([]string{"cmd", "--format", "xml"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--format: invalid value 'xml', must be one of: text, json") {
		t.Fatalf("expected choices error, got %q", r.Stderr)
	}
}

func TestArgOptionalChoicesNotPassed(t *testing.T) {
	app := simpleApp("cmd", "a command", "env={env}",
		WithArgs(NewArg("env", "target env", ArgOptional(),
			ArgChoices(Ch("dev", ""), Ch("staging", ""), Ch("prod", "")))))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "env=None\n" {
		t.Fatalf("expected 'env=None', got %q", r.Stdout)
	}
}

func TestArgOptionalChoicesPassedValid(t *testing.T) {
	app := simpleApp("cmd", "a command", "env={env}",
		WithArgs(NewArg("env", "target env", ArgOptional(),
			ArgChoices(Ch("dev", ""), Ch("staging", ""), Ch("prod", "")))))
	r := app.Test([]string{"cmd", "prod"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "env=prod\n" {
		t.Fatalf("expected 'env=prod', got %q", r.Stdout)
	}
}

func TestArgOptionalChoicesPassedInvalid(t *testing.T) {
	app := simpleApp("cmd", "a command", "env={env}",
		WithArgs(NewArg("env", "target env", ArgOptional(),
			ArgChoices(Ch("dev", ""), Ch("staging", ""), Ch("prod", "")))))
	r := app.Test([]string{"cmd", "local"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "argument 'env': invalid value 'local', must be one of: dev, staging, prod") {
		t.Fatalf("expected choices error, got %q", r.Stderr)
	}
}

func TestGlobalFlagOptionalChoicesNotPassed(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok")
	app.GlobalFlag(StringFlag("format", "output format", Optional(), Choices(Ch("text", ""), Ch("json", ""))))
	r := app.Test([]string{"cmd"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestGlobalFlagOptionalChoicesPassedInvalid(t *testing.T) {
	app := simpleApp("cmd", "a command", "ok")
	app.GlobalFlag(StringFlag("format", "output format", Optional(), Choices(Ch("text", ""), Ch("json", ""))))
	r := app.Test([]string{"--format", "xml", "cmd"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--format: invalid value 'xml', must be one of: text, json") {
		t.Fatalf("expected choices error, got %q", r.Stderr)
	}
}

func TestUnelectedScopeChoicesNotValidated(t *testing.T) {
	// A choices flag inside a scope that was not elected is not resolved at
	// all, so its choices cannot fire (contract §24.1: an unelected choice's
	// flags are not resolved). This is what the deleted mutex version asserted,
	// restated over the construct that replaced the group.
	app := simpleApp("cmd", "a command", "mode={mode}",
		WithFlags(MemberChoiceFlag("mode", "how to write output", Required(),
			MemberChoice(StringFlag("format", "output format", Required(),
				Choices(Ch("text", ""), Ch("json", ""))), "write a formatted document"),
			MemberChoice(StringFlag("output", "output path", Required()), "write to a path"),
		)))
	r := app.Test([]string{"cmd", "--output", "out.txt"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.ExitCode, r.Stderr)
	}
	if r.Stdout != "mode=output[value:out.txt]\n" {
		t.Fatalf("expected 'mode=output[value:out.txt]', got %q", r.Stdout)
	}
}

// The validator family lives in validate_test.go; the "never runs on absence"
// case moved there as TestValidateNeverRunsOnAbsence.
