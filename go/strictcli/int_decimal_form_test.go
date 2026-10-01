package strictcli

// An integer value is plain decimal digits with an optional leading minus sign
// (contract §30): every other spelling strconv.Atoi would take is refused, on
// the command line, from the environment, and in `config set`.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseIntStrictAcceptsPlainDecimal(t *testing.T) {
	for text, want := range map[string]int{"30": 30, "-5": -5, "0": 0, "-0": 0} {
		got, err := parseIntStrict(text)
		if err != nil {
			t.Fatalf("parseIntStrict(%q): unexpected error %v", text, err)
		}
		if got != want {
			t.Fatalf("parseIntStrict(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestParseIntStrictRefusesOtherForms(t *testing.T) {
	refused := []string{
		"+30", "+0", "030", "-030", "00", "-00", " 30", "30 ", "3_0",
		"٣٠", // Arabic-Indic digits
		"３０", // fullwidth digits
		"1e3", "0x1e", "-", "+", "", "9223372036854775808",
	}
	for _, text := range refused {
		_, err := parseIntStrict(text)
		if err == nil {
			t.Fatalf("parseIntStrict(%q): expected a refusal", text)
		}
		if want := "expected integer, got '" + text + "'"; err.Error() != want {
			t.Fatalf("parseIntStrict(%q): error %q, want %q", text, err.Error(), want)
		}
	}
}

func TestIntFlagRefusesNonDecimalFormOnCommandLine(t *testing.T) {
	for _, text := range []string{"+30", "030", "-030"} {
		app := simpleApp("cmd", "a command", "port={port}",
			WithFlags(IntFlag("port", "the port", Required())))
		r := app.Test([]string{"cmd", "--port", text})
		if r.ExitCode != 1 {
			t.Fatalf("%q: expected exit 1, got %d (stdout=%q)", text, r.ExitCode, r.Stdout)
		}
		if want := "error: --port: expected integer, got '" + text + "'"; !strings.Contains(r.Stderr, want) {
			t.Fatalf("%q: stderr %q does not contain %q", text, r.Stderr, want)
		}
	}
}

func TestIntFlagRefusesNonDecimalFormFromEnv(t *testing.T) {
	for _, text := range []string{"+30", "030"} {
		t.Setenv("MYAPP_PORT", text)
		app := simpleApp("cmd", "a command", "port={port}",
			WithFlags(IntFlag("port", "the port", Default(80), Env("MYAPP_PORT"))))
		r := app.Test([]string{"cmd"})
		if r.ExitCode != 1 {
			t.Fatalf("%q: expected exit 1, got %d (stdout=%q)", text, r.ExitCode, r.Stdout)
		}
		want := "error: --port: expected integer, got '" + text + "' (from env var 'MYAPP_PORT')"
		if !strings.Contains(r.Stderr, want) {
			t.Fatalf("%q: stderr %q does not contain %q", text, r.Stderr, want)
		}
	}
}

func TestConfigSetRefusesNonDecimalInt(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()
	for _, text := range []string{"+30", "030"} {
		app := NewApp("intform", "1.0.0", "test app", WithConfig())
		app.Command("run", "run something", func(ctx *Context, args map[string]interface{}) Outcome {
			return Exit(0)
		}, WithFlags(IntFlag("count", "how many", Default(0))), WithEffect(EffectReadOnly))
		r := app.Test([]string{"config", "set", "count", "--value", text})
		if r.ExitCode != 1 {
			t.Fatalf("%q: expected exit 1, got %d", text, r.ExitCode)
		}
		if want := "error: config set: key 'count': expected integer, got '" + text + "'"; !strings.Contains(r.Stderr, want) {
			t.Fatalf("%q: stderr %q does not contain %q", text, r.Stderr, want)
		}
		if _, err := os.Stat(filepath.Join(tmpDir, "intform", "config.json")); err == nil {
			t.Fatalf("%q: config file was written", text)
		}
	}
}
