package strictcli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Declared runtime requirements: a requirement is declared once, commands
// reference it, the framework loads it before the handler runs on every door,
// and a missing one ends the command with one error naming it and how to
// install it.

type gpuLoader struct{ version string }

func vulkanRequirement(available bool) *Requirement[gpuLoader] {
	return NewRequirement("vulkan-loader", "the Vulkan loader library",
		"sudo dnf install vulkan-loader",
		func() (gpuLoader, error) {
			if !available {
				return gpuLoader{}, errors.New("libvulkan.so.1: cannot open shared object file")
			}
			return gpuLoader{version: "1.4"}, nil
		})
}

func requirementApp(req *Requirement[gpuLoader]) *App {
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("render", "render a frame", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Out("vulkan " + Need(ctx, req).version)
		return Exit(0)
	}, WithEffect(EffectReadOnly), WithRequires(req))
	app.Command("plain", "needs nothing", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		ctx.Out("plain")
		return Exit(0)
	}, WithEffect(EffectReadOnly))
	return app
}

const missingVulkan = "command 'render' needs vulkan-loader (the Vulkan loader library), which is not available: " +
	"libvulkan.so.1: cannot open shared object file; install it: sudo dnf install vulkan-loader"

func TestRequirement_AvailableRunsTheHandlerWithTheLoadedValue(t *testing.T) {
	r := requirementApp(vulkanRequirement(true)).Test([]string{"render"})
	if r.ExitCode != 0 || r.Stdout != "vulkan 1.4\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestRequirement_MissingEndsTheCommandNamingTheFix(t *testing.T) {
	app := requirementApp(vulkanRequirement(false))
	r := app.Test([]string{"render"})
	if r.ExitCode != 1 || r.Stdout != "" || r.Stderr != "error: "+missingVulkan+"\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", r.ExitCode, r.Stdout, r.Stderr)
	}
	// A command that does not need it runs.
	if r := app.Test([]string{"plain"}); r.ExitCode != 0 || r.Stdout != "plain\n" {
		t.Fatalf("plain: exit=%d stdout=%q", r.ExitCode, r.Stdout)
	}
	// The fix the error names -- the requirement becoming available -- clears it.
	if r := requirementApp(vulkanRequirement(true)).Test([]string{"render"}); r.ExitCode != 0 {
		t.Fatalf("after the fix: exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
}

func TestRequirement_CheckedInDryRunAndMachineMode(t *testing.T) {
	app := requirementApp(vulkanRequirement(false))
	if r := app.Test([]string{"--dry-run", "render"}); r.ExitCode != 1 || !strings.Contains(r.Stderr, missingVulkan) {
		t.Fatalf("dry run: exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	r := app.Test([]string{"--json", "render"})
	if r.ExitCode != 1 {
		t.Fatalf("json: exit=%d", r.ExitCode)
	}
	var env map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil {
		t.Fatalf("json: %v: %q", err, r.Stdout)
	}
	diags := env["diagnostics"].([]interface{})
	last := diags[len(diags)-1].(map[string]interface{})
	if last["level"] != "error" || last["message"] != missingVulkan || env["command"] != "render" {
		t.Fatalf("json envelope: %v", env)
	}
}

func TestRequirement_CheckedOnTheProgrammaticDoor(t *testing.T) {
	_, err := requirementApp(vulkanRequirement(false)).Call("render", nil)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 1 || ee.Message != missingVulkan {
		t.Fatalf("call: %v", err)
	}
	if _, err := requirementApp(vulkanRequirement(true)).Call("render", nil); err != nil {
		t.Fatalf("call after the fix: %v", err)
	}
}

func TestRequirement_HelpNeverLoadsIt(t *testing.T) {
	loads := 0
	req := NewRequirement("vulkan-loader", "the Vulkan loader library", "sudo dnf install vulkan-loader",
		func() (gpuLoader, error) { loads++; return gpuLoader{}, errors.New("missing") })
	app := requirementApp(req)
	r := app.Test([]string{"render", "--help"})
	want := "myapp render -- render a frame\n\nRequirements:\n" +
		"  vulkan-loader    the Vulkan loader library; install it: sudo dnf install vulkan-loader\n"
	if r.ExitCode != 0 || r.Stdout != want {
		t.Fatalf("help: exit=%d stdout=%q", r.ExitCode, r.Stdout)
	}
	if r := app.Test([]string{"help", "render"}); r.Stdout != want {
		t.Fatalf("help command: %q", r.Stdout)
	}
	if loads != 0 {
		t.Fatalf("help loaded the requirement %d times", loads)
	}
}

func TestRequirement_PublishedInTheHelpDocument(t *testing.T) {
	r := requirementApp(vulkanRequirement(true)).Test([]string{"help", "--json"})
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &doc); err != nil {
		t.Fatal(err)
	}
	entry := doc["commands"].(map[string]interface{})["render"].(map[string]interface{})
	reqs := entry["requires"].([]interface{})
	want := map[string]interface{}{"name": "vulkan-loader", "help": "the Vulkan loader library", "install": "sudo dnf install vulkan-loader"}
	if len(reqs) != 1 || !mapsEqual(reqs[0].(map[string]interface{}), want) {
		t.Fatalf("requires: %v", reqs)
	}
	plain := doc["commands"].(map[string]interface{})["plain"].(map[string]interface{})
	if _, ok := plain["requires"]; ok {
		t.Fatal("a command needing nothing publishes requires")
	}
}

func mapsEqual(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

func TestRequirement_DeclarationRefusalsAndTheirFixes(t *testing.T) {
	ok := func() (int, error) { return 1, nil }
	mustPanicWith(t, errRequirementNameInvalid("Vulkan"), func() { NewRequirement("Vulkan", "h", "i", ok) })
	mustPanicWith(t, errRequirementHelpInvalid("vulkan"), func() { NewRequirement("vulkan", "", "i", ok) })
	mustPanicWith(t, errRequirementHelpInvalid("vulkan"), func() { NewRequirement("vulkan", "two\nlines", "i", ok) })
	mustPanicWith(t, errRequirementInstallInvalid("vulkan"), func() { NewRequirement("vulkan", "h", " ", ok) })
	mustPanicWith(t, errRequirementLoadMissing("vulkan"), func() { NewRequirement[int]("vulkan", "h", "i", nil) })
	NewRequirement("vulkan", "h", "i", ok)

	req := NewRequirement("vulkan", "h", "i", ok)
	mustPanicWith(t, errRequirementDeclaredTwice("render", "vulkan"), func() {
		namingApp().Command("render", "x", roHandler, WithEffect(EffectReadOnly), WithRequires(req, req))
	})
	// Declared once: two commands referencing one value is the idiom ...
	app := namingApp()
	app.Command("render", "x", roHandler, WithEffect(EffectReadOnly), WithRequires(req))
	app.Group("gpu", "g").Command("probe", "x", roHandler, WithEffect(EffectReadOnly), WithRequires(req))
	// ... and a second value of the same name is refused.
	other := NewRequirement("vulkan", "h", "i", ok)
	mustPanicWith(t, errRequirementNameReused("vulkan"), func() {
		app.Command("bench", "x", roHandler, WithEffect(EffectReadOnly), WithRequires(other))
	})
}

func TestRequirement_NeedOfAnUndeclaredRequirementIsAHardError(t *testing.T) {
	declared := vulkanRequirement(true)
	undeclared := NewRequirement("cuda-runtime", "the CUDA runtime", "install the CUDA toolkit",
		func() (int, error) { return 0, nil })
	app := NewApp("myapp", "1.0.0", "test app")
	app.Command("render", "render a frame", func(ctx *Context, kwargs map[string]interface{}) Outcome {
		Need(ctx, undeclared)
		return Exit(0)
	}, WithEffect(EffectReadOnly), WithRequires(declared))
	mustPanicWith(t, errNeedUndeclared("render", "cuda-runtime"), func() { app.Test([]string{"render"}) })
}
