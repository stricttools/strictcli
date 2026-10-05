package strictcli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// expectPanic runs fn and verifies it panics with a message containing substr.
func expectPanic(t *testing.T, substr string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic containing %q, but no panic occurred", substr)
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected panic string, got %T: %v", r, r)
		}
		if !strings.Contains(msg, substr) {
			t.Fatalf("panic message %q does not contain %q", msg, substr)
		}
	}()
	fn()
}

// --- Basic registration ---

func TestConfigFieldBasicStr(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("name", ConfigFieldHelp("The user name"), ConfigFieldDefault("anonymous"))

	cf, ok := app.configFields["name"]
	if !ok {
		t.Fatal("config field 'name' not registered")
	}
	if cf.Type != TypeStr {
		t.Fatalf("expected TypeStr, got %d", cf.Type)
	}
	if cf.Help != "The user name" {
		t.Fatalf("expected help 'The user name', got %q", cf.Help)
	}
	if cf.Default != "anonymous" {
		t.Fatalf("expected default 'anonymous', got %v", cf.Default)
	}
	if !cf.HasDefault {
		t.Fatal("expected HasDefault to be true")
	}
	if cf.Required {
		t.Fatal("expected Required to be false (has default)")
	}
}

func TestConfigFieldBasicBool(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("verbose", ConfigFieldHelp("Enable verbose output"), ConfigFieldType(TypeBool), ConfigFieldDefault(false))

	cf := app.configFields["verbose"]
	if cf.Type != TypeBool {
		t.Fatalf("expected TypeBool, got %d", cf.Type)
	}
	if cf.Default != false {
		t.Fatalf("expected default false, got %v", cf.Default)
	}
}

func TestConfigFieldBasicInt(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("port", ConfigFieldHelp("Port number"), ConfigFieldType(TypeInt), ConfigFieldDefault(8080))

	cf := app.configFields["port"]
	if cf.Type != TypeInt {
		t.Fatalf("expected TypeInt, got %d", cf.Type)
	}
	if cf.Default != 8080 {
		t.Fatalf("expected default 8080, got %v", cf.Default)
	}
}

func TestConfigFieldBasicFloat(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("threshold", ConfigFieldHelp("Detection threshold"), ConfigFieldType(TypeFloat), ConfigFieldDefault(0.95))

	cf := app.configFields["threshold"]
	if cf.Type != TypeFloat {
		t.Fatalf("expected TypeFloat, got %d", cf.Type)
	}
	if cf.Default != 0.95 {
		t.Fatalf("expected default 0.95, got %v", cf.Default)
	}
}

// --- Dot names (TOML sections) ---

func TestConfigFieldDotName(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("server.host", ConfigFieldHelp("Server hostname"), ConfigFieldDefault("localhost"))
	app.ConfigField("server.port", ConfigFieldHelp("Server port"), ConfigFieldType(TypeInt), ConfigFieldDefault(443))
	app.ConfigField("database.primary.host", ConfigFieldHelp("Primary database host"))

	if _, ok := app.configFields["server.host"]; !ok {
		t.Fatal("config field 'server.host' not registered")
	}
	if _, ok := app.configFields["server.port"]; !ok {
		t.Fatal("config field 'server.port' not registered")
	}
	if _, ok := app.configFields["database.primary.host"]; !ok {
		t.Fatal("config field 'database.primary.host' not registered")
	}
}

// --- Required vs optional ---

func TestConfigFieldRequired(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("api_key", ConfigFieldHelp("The API key"))

	cf := app.configFields["api_key"]
	if !cf.Required {
		t.Fatal("expected Required to be true (no default)")
	}
	if cf.HasDefault {
		t.Fatal("expected HasDefault to be false")
	}
}

func TestConfigFieldOptionalNilDefault(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("label", ConfigFieldHelp("Optional label"), ConfigFieldDefault(nil))

	cf := app.configFields["label"]
	if cf.Required {
		t.Fatal("expected Required to be false (has nil default)")
	}
	if !cf.HasDefault {
		t.Fatal("expected HasDefault to be true")
	}
}

// --- Type mismatch in default (panic) ---

func TestConfigFieldDefaultTypeMismatchStrGotInt(t *testing.T) {
	expectPanic(t, "does not match type", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.ConfigField("name", ConfigFieldHelp("Name"), ConfigFieldDefault(42))
	})
}

func TestConfigFieldDefaultTypeMismatchIntGotStr(t *testing.T) {
	expectPanic(t, "does not match type", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.ConfigField("port", ConfigFieldHelp("Port"), ConfigFieldType(TypeInt), ConfigFieldDefault("8080"))
	})
}

func TestConfigFieldDefaultTypeMismatchBoolGotStr(t *testing.T) {
	expectPanic(t, "does not match type", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.ConfigField("verbose", ConfigFieldHelp("Verbose"), ConfigFieldType(TypeBool), ConfigFieldDefault("true"))
	})
}

func TestConfigFieldDefaultTypeMismatchFloatGotInt(t *testing.T) {
	expectPanic(t, "does not match type", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.ConfigField("rate", ConfigFieldHelp("Rate"), ConfigFieldType(TypeFloat), ConfigFieldDefault(42))
	})
}

// --- Duplicate name (panic) ---

func TestConfigFieldDuplicateName(t *testing.T) {
	expectPanic(t, "duplicate config field name", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.ConfigField("name", ConfigFieldHelp("First"))
		app.ConfigField("name", ConfigFieldHelp("Second"))
	})
}

// --- Underscore prefix reserved (panic) ---

func TestConfigFieldUnderscorePrefix(t *testing.T) {
	expectPanic(t, "is reserved: names starting with underscore", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.ConfigField("_internal", ConfigFieldHelp("Should fail"))
	})
}

func TestConfigFieldUnderscorePrefixWithDot(t *testing.T) {
	expectPanic(t, "is reserved: names starting with underscore", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.ConfigField("_schema.version", ConfigFieldHelp("Should fail"))
	})
}

// --- Invalid name format ---

func TestConfigFieldInvalidNameUppercase(t *testing.T) {
	expectPanic(t, "is invalid:", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.ConfigField("MyField", ConfigFieldHelp("Bad name"))
	})
}

func TestConfigFieldInvalidNameStartsWithDigit(t *testing.T) {
	expectPanic(t, "is invalid:", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.ConfigField("1field", ConfigFieldHelp("Bad name"))
	})
}

func TestConfigFieldInvalidNameEmpty(t *testing.T) {
	expectPanic(t, "is invalid:", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.ConfigField("", ConfigFieldHelp("Bad name"))
	})
}

// --- Missing help text ---

func TestConfigFieldMissingHelp(t *testing.T) {
	expectPanic(t, "help text is required", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.ConfigField("name")
	})
}

func TestConfigFieldEmptyHelp(t *testing.T) {
	expectPanic(t, "help text is required", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.ConfigField("name", ConfigFieldHelp("   "))
	})
}

// --- Framework field registration ---

func TestFrameworkFieldRegistration(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.registerFrameworkField("_schema_version", TypeStr, "Schema version for migration")

	cf, ok := app.frameworkFields["_schema_version"]
	if !ok {
		t.Fatal("framework field '_schema_version' not registered")
	}
	if cf.Type != TypeStr {
		t.Fatalf("expected TypeStr, got %d", cf.Type)
	}
	if cf.Help != "Schema version for migration" {
		t.Fatalf("expected help text, got %q", cf.Help)
	}
	if !cf.Required {
		t.Fatal("expected Required to be true (framework fields have no default)")
	}
}

func TestFrameworkFieldAllTypes(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.registerFrameworkField("_version", TypeStr, "Version")
	app.registerFrameworkField("_enabled", TypeBool, "Enabled")
	app.registerFrameworkField("_count", TypeInt, "Count")
	app.registerFrameworkField("_rate", TypeFloat, "Rate")

	if app.frameworkFields["_version"].Type != TypeStr {
		t.Fatal("wrong type for _version")
	}
	if app.frameworkFields["_enabled"].Type != TypeBool {
		t.Fatal("wrong type for _enabled")
	}
	if app.frameworkFields["_count"].Type != TypeInt {
		t.Fatal("wrong type for _count")
	}
	if app.frameworkFields["_rate"].Type != TypeFloat {
		t.Fatal("wrong type for _rate")
	}
}

func TestFrameworkFieldNoUnderscorePrefix(t *testing.T) {
	expectPanic(t, "must start with underscore", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.registerFrameworkField("schema_version", TypeStr, "Should fail")
	})
}

func TestFrameworkFieldDuplicate(t *testing.T) {
	expectPanic(t, "duplicate framework field name", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.registerFrameworkField("_ver", TypeStr, "First")
		app.registerFrameworkField("_ver", TypeStr, "Second")
	})
}

func TestFrameworkFieldMissingHelp(t *testing.T) {
	expectPanic(t, "help text is required", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.registerFrameworkField("_ver", TypeStr, "")
	})
}

// --- Cross-namespace duplicate detection ---

func TestConfigFieldConflictsWithFrameworkField(t *testing.T) {
	// User field with _ prefix is blocked by the reservation check,
	// so we test the other direction: framework field conflicting with user field.
	// Framework fields require _ prefix while user fields forbid it, so in practice
	// the namespaces don't overlap. This test verifies the underscore reservation fires.
	expectPanic(t, "is reserved: names starting with underscore", func() {
		app := NewApp("test", "1.0.0", "test app")
		app.registerFrameworkField("_ver", TypeStr, "Version")
		app.ConfigField("_ver", ConfigFieldHelp("Conflict"))
	})
}

// --- Order preservation ---

func TestConfigFieldOrderPreserved(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("zebra", ConfigFieldHelp("Z field"))
	app.ConfigField("alpha", ConfigFieldHelp("A field"))
	app.ConfigField("middle", ConfigFieldHelp("M field"))

	expected := []string{"zebra", "alpha", "middle"}
	if len(app.configFieldOrder) != len(expected) {
		t.Fatalf("expected %d fields, got %d", len(expected), len(app.configFieldOrder))
	}
	for i, name := range expected {
		if app.configFieldOrder[i] != name {
			t.Fatalf("order[%d]: expected %q, got %q", i, name, app.configFieldOrder[i])
		}
	}
}

func TestFrameworkFieldOrderPreserved(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.registerFrameworkField("_z", TypeStr, "Z")
	app.registerFrameworkField("_a", TypeStr, "A")
	app.registerFrameworkField("_m", TypeStr, "M")

	expected := []string{"_z", "_a", "_m"}
	if len(app.frameworkFieldOrder) != len(expected) {
		t.Fatalf("expected %d fields, got %d", len(expected), len(app.frameworkFieldOrder))
	}
	for i, name := range expected {
		if app.frameworkFieldOrder[i] != name {
			t.Fatalf("order[%d]: expected %q, got %q", i, name, app.frameworkFieldOrder[i])
		}
	}
}

// --- Name with hyphens and underscores ---

func TestConfigFieldNameWithHyphens(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("api_key", ConfigFieldHelp("API key"))
	if _, ok := app.configFields["api_key"]; !ok {
		t.Fatal("config field 'api_key' not registered")
	}
}

func TestConfigFieldNameWithUnderscoresNonPrefix(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("api_key", ConfigFieldHelp("API key"))
	if _, ok := app.configFields["api_key"]; !ok {
		t.Fatal("config field 'api_key' not registered")
	}
}

// === 5c: Per-command config field binding ===

func TestWithConfigFieldsStoresOnCommand(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("api_key", ConfigFieldHelp("API key"))
	app.ConfigField("region", ConfigFieldHelp("Region"), ConfigFieldDefault("us-east-1"))

	app.Command("deploy", "Deploy the app", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("api_key", "region"), WithEffect(EffectReadOnly))

	cmd := app.commands["deploy"]
	if len(cmd.configFields) != 2 {
		t.Fatalf("expected 2 config fields, got %d", len(cmd.configFields))
	}
	if cmd.configFields[0] != "api_key" || cmd.configFields[1] != "region" {
		t.Fatalf("unexpected config fields: %v", cmd.configFields)
	}
}

func TestWithConfigFieldsValidationAtRunTime(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	// Bind a config field that doesn't exist — should fail at Test() time
	app.Command("deploy", "Deploy the app", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("nonexistent"), WithEffect(EffectReadOnly))

	r := app.Test([]string{"deploy"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config_fields references unknown config field \"nonexistent\"") {
		t.Fatalf("expected undeclared config field error, got: %s", r.Stderr)
	}
}

func TestWithConfigFieldsValidBinding(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("region", ConfigFieldHelp("Region"), ConfigFieldDefault("us-east-1"))
	app.Command("deploy", "Deploy the app", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("region"), WithEffect(EffectReadOnly))

	// Should succeed since "region" is declared and has a default
	r := app.Test([]string{"deploy"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}
}

func TestWithConfigFieldsInGroup(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("api_key", ConfigFieldHelp("API key"), ConfigFieldDefault("default-key"))

	grp := app.Group("infra", "Infrastructure commands")
	grp.Command("deploy", "Deploy the app", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("api_key"), WithEffect(EffectReadOnly))

	// Should validate correctly
	r := app.Test([]string{"infra", "deploy"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}
}

func TestWithConfigFieldsInGroupUnknownField(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app")

	grp := app.Group("infra", "Infrastructure commands")
	grp.Command("deploy", "Deploy the app", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("ghost-field"), WithEffect(EffectReadOnly))

	r := app.Test([]string{"infra", "deploy"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config_fields references unknown config field \"ghost-field\"") {
		t.Fatalf("expected undeclared config field error, got: %s", r.Stderr)
	}
}

// === 5d: Config field validation at startup ===

func TestConfigFieldValidationRequiredFieldMissing(t *testing.T) {
	// Create a temp config file with no api_key
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("api_key", ConfigFieldHelp("API key"))
	app.Command("deploy", "Deploy the app", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("api_key"), WithEffect(EffectReadOnly))

	r := app.Test([]string{"deploy"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "required config field") && !strings.Contains(r.Stderr, "api_key") {
		t.Fatalf("expected required field error, got: %s", r.Stderr)
	}
}

func TestConfigFieldValidationRequiredFieldPresent(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"api_key": "my-secret"}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("api_key", ConfigFieldHelp("API key"))
	app.Command("deploy", "Deploy the app", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("api_key"), WithEffect(EffectReadOnly))

	r := app.Test([]string{"deploy"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}
}

func TestConfigFieldValidationTypeMismatch(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"port": "not-a-number"}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("port", ConfigFieldHelp("Port number"), ConfigFieldType(TypeInt))
	app.Command("serve", "Start server", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("port"), WithEffect(EffectReadOnly))

	r := app.Test([]string{"serve"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config field") || !strings.Contains(r.Stderr, "port") {
		t.Fatalf("expected type mismatch error, got: %s", r.Stderr)
	}
}

func TestConfigFieldValidationOptionalFieldMissing(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("region", ConfigFieldHelp("Region"), ConfigFieldDefault("us-east-1"))
	app.Command("deploy", "Deploy the app", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("region"), WithEffect(EffectReadOnly))

	r := app.Test([]string{"deploy"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0 (optional field), got %d. stderr: %s", r.ExitCode, r.Stderr)
	}
}

func TestConfigFieldValidationUnknownKeyInConfig(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"api_key": "secret", "bogus_key": "value"}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("api_key", ConfigFieldHelp("API key"))
	app.Command("deploy", "Deploy the app", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("api_key"), WithEffect(EffectReadOnly))

	r := app.Test([]string{"deploy"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown key") || !strings.Contains(r.Stderr, "bogus_key") {
		t.Fatalf("expected unknown key error, got: %s", r.Stderr)
	}
}

func TestConfigFieldValidationConfigSubcommandExempt(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	// No config file exists — config subcommands should still work
	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("api_key", ConfigFieldHelp("API key")) // required, not set

	r := app.Test([]string{"config", "path"})
	if r.ExitCode != 0 {
		t.Fatalf("config subcommand should be exempt from validation, got exit %d. stderr: %s", r.ExitCode, r.Stderr)
	}
}

func TestConfigFieldValidationNoConfigFieldsSkipsBoundValidation(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	// api_key is missing from config — but since the command doesn't bind it,
	// bound-field validation is skipped. No unknown keys either.
	os.WriteFile(configFile, []byte(`{"api_key": "present"}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("api_key", ConfigFieldHelp("API key"))
	// Command has no config fields bound — bound validation should not run
	app.Command("status", "Show status", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"status"})
	if r.ExitCode != 0 {
		t.Fatalf("no config fields bound — should skip bound validation, got exit %d. stderr: %s", r.ExitCode, r.Stderr)
	}
}

func TestConfigFieldValidationUnknownKeyStillCheckedWhenNoBinding(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"random_key": true}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("api_key", ConfigFieldHelp("API key"))
	// Command has no config fields bound, but app-wide unknown-key check still runs
	app.Command("status", "Show status", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	r := app.Test([]string{"status"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1 for unknown key, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown key") || !strings.Contains(r.Stderr, "random_key") {
		t.Fatalf("expected unknown key error, got: %s", r.Stderr)
	}
}

// === 5e: Config show and config set with config fields ===

func TestConfigShowIncludesConfigFields(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"region": "eu-west-1"}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("region", ConfigFieldHelp("Region"), ConfigFieldDefault("us-east-1"))
	app.ConfigField("api_key", ConfigFieldHelp("API key"))

	r := app.Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "Config fields:") {
		t.Fatalf("expected Config fields: header in output, got: %s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "region (str, optional) = eu-west-1  (source: config)  -- Region") {
		t.Fatalf("expected region in output, got: %s", r.Stdout)
	}
	// api_key should show as not set since it's required and not in the file
	if !strings.Contains(r.Stdout, "api_key (str, required)") {
		t.Fatalf("expected api_key in output, got: %s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "(source: not set)") {
		t.Fatalf("expected source: not set for required field, got: %s", r.Stdout)
	}
}

func TestConfigShowJSONIncludesConfigFields(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"region": "eu-west-1"}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("region", ConfigFieldHelp("Region"), ConfigFieldDefault("us-east-1"))
	app.ConfigField("api_key", ConfigFieldHelp("API key"))

	r := app.Test([]string{"config", "show", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(envelopePayload(t, r.Stdout), &result); err != nil {
		t.Fatalf("failed to parse JSON: %v\noutput: %s", err, r.Stdout)
	}
	// Config field with value from config file
	regionEntry, ok := result["region"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected region entry, got: %v", result["region"])
	}
	if regionEntry["value"] != "eu-west-1" {
		t.Fatalf("expected region value eu-west-1, got: %v", regionEntry["value"])
	}
	if regionEntry["source"] != "config" {
		t.Fatalf("expected region source config, got: %v", regionEntry["source"])
	}
	if regionEntry["type"] != "str" {
		t.Fatalf("expected region type str, got: %v", regionEntry["type"])
	}
	if regionEntry["required"] != false {
		t.Fatalf("expected region required false, got: %v", regionEntry["required"])
	}
	if regionEntry["help"] != "Region" {
		t.Fatalf("expected region help Region, got: %v", regionEntry["help"])
	}
	if regionEntry["default"] != "us-east-1" {
		t.Fatalf("expected region default us-east-1, got: %v", regionEntry["default"])
	}
	// Required config field with no value
	apiEntry, ok := result["api_key"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected api_key entry, got: %v", result["api_key"])
	}
	if apiEntry["source"] != "not set" {
		t.Fatalf("expected api_key source 'not set', got: %v", apiEntry["source"])
	}
	if apiEntry["required"] != true {
		t.Fatalf("expected api_key required true, got: %v", apiEntry["required"])
	}
	if _, hasDefault := apiEntry["default"]; hasDefault {
		t.Fatalf("expected no default for required field, got: %v", apiEntry["default"])
	}
}

func TestConfigSetConfigField(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("region", ConfigFieldHelp("Region"), ConfigFieldDefault("us-east-1"))

	r := app.Test([]string{"config", "set", "region", "--value", "eu-west-1"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}

	// Verify it was written
	data, _ := os.ReadFile(configFile)
	if !strings.Contains(string(data), `"eu-west-1"`) {
		t.Fatalf("expected eu-west-1 in config file, got: %s", string(data))
	}
}

func TestConfigSetConfigFieldTypeBool(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("verbose", ConfigFieldHelp("Verbose output"), ConfigFieldType(TypeBool), ConfigFieldDefault(false))

	r := app.Test([]string{"config", "set", "verbose", "--value", "true"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}

	data, _ := os.ReadFile(configFile)
	if !strings.Contains(string(data), "true") {
		t.Fatalf("expected true in config file, got: %s", string(data))
	}
}

func TestConfigSetConfigFieldTypeInt(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("port", ConfigFieldHelp("Port number"), ConfigFieldType(TypeInt), ConfigFieldDefault(8080))

	r := app.Test([]string{"config", "set", "port", "--value", "9090"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}

	data, _ := os.ReadFile(configFile)
	if !strings.Contains(string(data), "9090") {
		t.Fatalf("expected 9090 in config file, got: %s", string(data))
	}
}

func TestConfigSetConfigFieldTypeFloat(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("threshold", ConfigFieldHelp("Detection threshold"), ConfigFieldType(TypeFloat), ConfigFieldDefault(0.5))

	r := app.Test([]string{"config", "set", "threshold", "--value", "0.95"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}

	data, _ := os.ReadFile(configFile)
	if !strings.Contains(string(data), "0.95") {
		t.Fatalf("expected 0.95 in config file, got: %s", string(data))
	}
}

func TestConfigSetConfigFieldInvalidType(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("port", ConfigFieldHelp("Port number"), ConfigFieldType(TypeInt))

	r := app.Test([]string{"config", "set", "port", "--value", "not-a-number"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "expected integer") {
		t.Fatalf("expected type error, got: %s", r.Stderr)
	}
}

func TestConfigSetUnknownKey(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))

	r := app.Test([]string{"config", "set", "nonexistent", "--value", "value"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown key") {
		t.Fatalf("expected unknown key error, got: %s", r.Stderr)
	}
}

func TestConfigSetConfigFieldClearNotAllowed(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("region", ConfigFieldHelp("Region"), ConfigFieldDefault("us-east-1"))

	r := app.Test([]string{"config", "set", "region", "--clear"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--clear is only for repeatable flags") {
		t.Fatalf("expected clear error, got: %s", r.Stderr)
	}
}

func TestConfigSetConfigFieldDefault(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"region": "eu-west-1"}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("region", ConfigFieldHelp("Region"), ConfigFieldDefault("us-east-1"))

	r := app.Test([]string{"config", "set", "region", "--default"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}

	data, _ := os.ReadFile(configFile)
	if strings.Contains(string(data), "region") {
		t.Fatalf("expected region to be removed from config, got: %s", string(data))
	}
}

// === 5f: config init ===

func TestConfigInitJSON(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("region", ConfigFieldHelp("Region"), ConfigFieldDefault("us-east-1"))
	app.ConfigField("api_key", ConfigFieldHelp("API key"))
	app.ConfigField("port", ConfigFieldHelp("Port number"), ConfigFieldType(TypeInt), ConfigFieldDefault(8080))
	app.Command("deploy", "Deploy", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "init"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "us-east-1") {
		t.Fatalf("expected default region in template, got: %s", content)
	}
	if !strings.Contains(content, "8080") {
		t.Fatalf("expected default port in template, got: %s", content)
	}
}

func TestConfigInitTOML(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.toml"

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile), WithConfigFormat("toml"))
	app.ConfigField("region", ConfigFieldHelp("Region"), ConfigFieldDefault("us-east-1"))
	app.ConfigField("server.host", ConfigFieldHelp("Server hostname"), ConfigFieldDefault("localhost"))
	app.ConfigField("server.port", ConfigFieldHelp("Server port"), ConfigFieldType(TypeInt), ConfigFieldDefault(443))
	app.ConfigField("api_key", ConfigFieldHelp("API key"))
	app.Command("deploy", "Deploy", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "init"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}

	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("config file not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[server]") {
		t.Fatalf("expected [server] section in TOML template, got: %s", content)
	}
	if !strings.Contains(content, "us-east-1") {
		t.Fatalf("expected default region in TOML template, got: %s", content)
	}
	if !strings.Contains(content, "REQUIRED") {
		t.Fatalf("expected REQUIRED marker for api_key, got: %s", content)
	}
}

func TestConfigInitErrorIfExists(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.Command("deploy", "Deploy", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "init"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1 (file exists), got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "already exists") {
		t.Fatalf("expected already exists error, got: %s", r.Stderr)
	}
}

func TestConfigInitJSONNested(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("db.host", ConfigFieldHelp("Database host"), ConfigFieldDefault("localhost"))
	app.ConfigField("db.port", ConfigFieldHelp("Database port"), ConfigFieldType(TypeInt), ConfigFieldDefault(5432))
	app.Command("serve", "Serve", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "init"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}

	data, _ := os.ReadFile(configFile)
	content := string(data)
	// Should have nested structure
	if !strings.Contains(content, "localhost") {
		t.Fatalf("expected localhost in nested JSON, got: %s", content)
	}
	if !strings.Contains(content, "5432") {
		t.Fatalf("expected 5432 in nested JSON, got: %s", content)
	}
}

// === 5g: Schema serialization ===

func TestSchemaIncludesConfigFields(t *testing.T) {
	chdirTemp(t)
	app := NewApp("test", "1.0.0", "test app")
	app.ConfigField("region", ConfigFieldHelp("AWS region"), ConfigFieldDefault("us-east-1"))
	app.ConfigField("port", ConfigFieldHelp("Port number"), ConfigFieldType(TypeInt))

	app.Command("deploy", "Deploy the app", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("region", "port"), WithEffect(EffectReadOnly))

	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}

	cfFields, ok := schema["config_fields"]
	if !ok {
		t.Fatal("expected config_fields in schema")
	}
	fieldMap, ok := cfFields.(map[string]interface{})
	if !ok {
		t.Fatalf("expected config_fields to be a map, got %T", cfFields)
	}
	if len(fieldMap) != 2 {
		t.Fatalf("expected 2 config fields in schema, got %d", len(fieldMap))
	}

	// Check region field
	regionRaw, ok := fieldMap["region"]
	if !ok {
		t.Fatal("expected region key in config_fields")
	}
	region := regionRaw.(map[string]interface{})
	// A config-field entry publishes a fragment too, from the scalar rows only
	// (contract §25.7). `required` stays beside it: a config field has no CLI
	// surface and no presence declaration.
	if got := region["value_schema"].(map[string]interface{})["type"]; got != "string" {
		t.Fatalf("expected region fragment type 'string', got %v", got)
	}
	if region["help"] != "AWS region" {
		t.Fatalf("expected region help 'AWS region', got %v", region["help"])
	}
	if region["required"] != false {
		t.Fatalf("expected region required false, got %v", region["required"])
	}
	if region["default"] != "us-east-1" {
		t.Fatalf("expected region default 'us-east-1', got %v", region["default"])
	}
	// Check bound_commands
	cmds0, ok := region["bound_commands"]
	if !ok {
		t.Fatal("expected bound_commands key for region")
	}
	cmdList0 := cmds0.([]interface{})
	if len(cmdList0) != 1 || cmdList0[0] != "deploy" {
		t.Fatalf("expected bound_commands ['deploy'], got %v", cmdList0)
	}

	// Check port field
	portRaw, ok := fieldMap["port"]
	if !ok {
		t.Fatal("expected port key in config_fields")
	}
	port := portRaw.(map[string]interface{})
	if got := port["value_schema"].(map[string]interface{})["type"]; got != "integer" {
		t.Fatalf("expected port fragment type 'integer', got %v", got)
	}
	if port["required"] != true {
		t.Fatalf("expected port required true, got %v", port["required"])
	}

	// Check per-command config_fields list
	commands := schema["commands"].(map[string]interface{})
	deployCmd := commands["deploy"].(map[string]interface{})
	cmdCfFields, ok := deployCmd["config_fields"]
	if !ok {
		t.Fatal("expected config_fields on deploy command in schema")
	}
	cmdCfList := cmdCfFields.([]interface{})
	if len(cmdCfList) != 2 {
		t.Fatalf("expected 2 config_fields on deploy command, got %d", len(cmdCfList))
	}
	if cmdCfList[0] != "region" || cmdCfList[1] != "port" {
		t.Fatalf("expected config_fields ['region', 'port'], got %v", cmdCfList)
	}
}

func TestSchemaNoConfigFieldsWhenNoneDeclared(t *testing.T) {
	chdirTemp(t)
	app := NewApp("test", "1.0.0", "test app")
	app.Command("hello", "Say hello", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithEffect(EffectReadOnly))

	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema error: %v", err)
	}

	if _, ok := schema["config_fields"]; ok {
		t.Fatal("expected no config_fields key when none are declared")
	}
}

// === Nested (dot-name) config field tests ===

func TestConfigFieldValidationNestedJSONRequired(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"server": {"host": "localhost"}}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("server.host", ConfigFieldHelp("Server hostname"))
	app.Command("serve", "Start server", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("server.host"), WithEffect(EffectReadOnly))

	r := app.Test([]string{"serve"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0 (nested field present), got %d. stderr: %s", r.ExitCode, r.Stderr)
	}
}

func TestConfigFieldValidationNestedJSONMissing(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("server.host", ConfigFieldHelp("Server hostname"))
	app.Command("serve", "Start server", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("server.host"), WithEffect(EffectReadOnly))

	r := app.Test([]string{"serve"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1 (required nested field missing), got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "required config field") || !strings.Contains(r.Stderr, "server.host") {
		t.Fatalf("expected missing field error for server.host, got: %s", r.Stderr)
	}
}

func TestConfigFieldValidationNestedJSONTypeMismatch(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"server": {"port": "not-a-number"}}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("server.port", ConfigFieldHelp("Server port"), ConfigFieldType(TypeInt))
	app.Command("serve", "Start server", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("server.port"), WithEffect(EffectReadOnly))

	r := app.Test([]string{"serve"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "config field") || !strings.Contains(r.Stderr, "server.port") {
		t.Fatalf("expected type mismatch error for server.port, got: %s", r.Stderr)
	}
}

func TestConfigFieldValidationNestedJSONUnknownKey(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"server": {"host": "localhost", "bogus": "value"}}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("server.host", ConfigFieldHelp("Server hostname"))
	app.Command("serve", "Start server", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("server.host"), WithEffect(EffectReadOnly))

	r := app.Test([]string{"serve"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit code 1 for unknown nested key, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown key") || !strings.Contains(r.Stderr, "server.bogus") {
		t.Fatalf("expected unknown key error for server.bogus, got: %s", r.Stderr)
	}
}

func TestConfigSetNestedField(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("server.host", ConfigFieldHelp("Server hostname"), ConfigFieldDefault("localhost"))

	r := app.Test([]string{"config", "set", "server.host", "--value", "example.com"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}

	data, _ := os.ReadFile(configFile)
	content := string(data)
	// Should have nested structure: {"server": {"host": "example.com"}}
	if !strings.Contains(content, "example.com") {
		t.Fatalf("expected example.com in config file, got: %s", content)
	}
	if !strings.Contains(content, "server") {
		t.Fatalf("expected nested 'server' key in config file, got: %s", content)
	}
}

func TestConfigSetDefaultNestedField(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"server": {"host": "example.com", "port": 9090}}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("server.host", ConfigFieldHelp("Server hostname"), ConfigFieldDefault("localhost"))
	app.ConfigField("server.port", ConfigFieldHelp("Server port"), ConfigFieldType(TypeInt), ConfigFieldDefault(8080))

	r := app.Test([]string{"config", "set", "server.host", "--default"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}

	data, _ := os.ReadFile(configFile)
	content := string(data)
	// server.host should be removed; server.port should remain
	if strings.Contains(content, "example.com") {
		t.Fatalf("expected server.host to be removed, got: %s", content)
	}
	if !strings.Contains(content, "9090") {
		t.Fatalf("expected server.port to remain, got: %s", content)
	}
}

func TestConfigSetDefaultNestedFieldCleansEmptyParent(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"server": {"host": "example.com"}}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("server.host", ConfigFieldHelp("Server hostname"), ConfigFieldDefault("localhost"))

	r := app.Test([]string{"config", "set", "server.host", "--default"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}

	data, _ := os.ReadFile(configFile)
	content := string(data)
	// Both server.host and the empty server parent should be gone
	if strings.Contains(content, "server") {
		t.Fatalf("expected empty parent 'server' to be cleaned up, got: %s", content)
	}
}

func TestConfigShowNestedFieldPlain(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"server": {"host": "example.com"}}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("server.host", ConfigFieldHelp("Server hostname"), ConfigFieldDefault("localhost"))

	r := app.Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "server.host (str, optional) = example.com  (source: config)  -- Server hostname") {
		t.Fatalf("expected nested field value in plain output, got: %s", r.Stdout)
	}
}

func TestConfigShowNestedFieldJSON(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"server": {"host": "example.com"}}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("server.host", ConfigFieldHelp("Server hostname"), ConfigFieldDefault("localhost"))

	r := app.Test([]string{"config", "show", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, `"server.host"`) {
		t.Fatalf("expected server.host key in JSON output, got: %s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, `"example.com"`) {
		t.Fatalf("expected example.com in JSON output, got: %s", r.Stdout)
	}
}

func TestConfigShowNestedFieldDefaultFallback(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("server.host", ConfigFieldHelp("Server hostname"), ConfigFieldDefault("localhost"))

	r := app.Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "server.host (str, optional) = localhost  (source: default)  -- Server hostname") {
		t.Fatalf("expected default value in output, got: %s", r.Stdout)
	}
}

func TestConfigFieldValidationDeepNestedJSON(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"database": {"primary": {"host": "db.example.com"}}}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("database.primary.host", ConfigFieldHelp("Primary database host"))
	app.Command("serve", "Start server", func(ctx *Context, args map[string]interface{}) Outcome {
		return Exit(0)
	}, WithConfigFields("database.primary.host"), WithEffect(EffectReadOnly))

	r := app.Test([]string{"serve"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0 (deep nested field present), got %d. stderr: %s", r.ExitCode, r.Stderr)
	}
}

func TestConfigSetDeepNestedField(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.ConfigField("database.primary.host", ConfigFieldHelp("Primary database host"), ConfigFieldDefault("localhost"))

	r := app.Test([]string{"config", "set", "database.primary.host", "--value", "db.example.com"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d. stderr: %s", r.ExitCode, r.Stderr)
	}

	data, _ := os.ReadFile(configFile)
	content := string(data)
	if !strings.Contains(content, "db.example.com") {
		t.Fatalf("expected db.example.com in config file, got: %s", content)
	}
	if !strings.Contains(content, "database") {
		t.Fatalf("expected nested 'database' key in config file, got: %s", content)
	}
}

// ---- --config flag tests ----

func TestConfigFlagSelectsFile(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	// Write a config file at a custom path
	customPath := filepath.Join(tmpDir, "custom.json")
	os.WriteFile(customPath, []byte(`{"port": 9999}`), 0o644)

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("port=%d", args["port"]))
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"--config", customPath, "serve"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=9999") {
		t.Fatalf("expected port=9999 from --config, got %q", r.Stdout)
	}
}

func TestConfigFlagEqualsForm(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	customPath := filepath.Join(tmpDir, "custom.json")
	os.WriteFile(customPath, []byte(`{"port": 7777}`), 0o644)

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("port=%d", args["port"]))
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"--config=" + customPath, "serve"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=7777") {
		t.Fatalf("expected port=7777 from --config=, got %q", r.Stdout)
	}
}

func TestConfigFlagOverridesConstructedPath(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	// Set up constructed config path with one value
	constructedPath := filepath.Join(tmpDir, "constructed.json")
	os.WriteFile(constructedPath, []byte(`{"port": 1111}`), 0o644)

	// Set up override config path with a different value
	overridePath := filepath.Join(tmpDir, "override.json")
	os.WriteFile(overridePath, []byte(`{"port": 2222}`), 0o644)

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithConfigPath(constructedPath))
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("port=%d", args["port"]))
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
	), WithEffect(EffectReadOnly))

	r := app.Test([]string{"--config", overridePath, "serve"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=2222") {
		t.Fatalf("expected port=2222 from --config override, got %q", r.Stdout)
	}
}

func TestConfigFlagOnDisabledAppIsError(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"--config", "/tmp/fake.json", "run"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "--config is not available") {
		t.Fatalf("expected --config error, got: %s", r.Stderr)
	}
}

func TestConfigFlagAfterCommandIsUnknown(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--config", "/tmp/fake.json"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown flag") {
		t.Fatalf("expected unknown flag error, got: %s", r.Stderr)
	}
}

func TestConfigFlagAfterDoubleDash(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"--", "--config", "/tmp/fake.json"})
	// After --, --config is treated as a command name (unknown command error)
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
}

func TestConfigFlagNotInSchema(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	// Create a go.mod so writeSchema works
	goModPath := filepath.Join(tmpDir, "go.mod")
	os.WriteFile(goModPath, []byte("module testapp\n"), 0o644)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	app := NewApp("testapp", "1.0.0", "test app", WithConfig())
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	schema, err := dumpSchema(app)
	if err != nil {
		t.Fatalf("dumpSchema failed: %v", err)
	}
	data, _ := json.Marshal(schema)
	schemaStr := string(data)

	// --config should NOT appear in the schema
	if strings.Contains(schemaStr, `"config"`) {
		// config might appear as a group name but NOT as a flag name
		// Check that no flag object has name "config"
		if gf, ok := schema["global_flags"]; ok {
			for _, f := range gf.([]interface{}) {
				fm := f.(map[string]interface{})
				if fm["name"] == "config" {
					t.Fatal("--config should NOT appear in schema output as a global flag")
				}
			}
		}
	}
}

func TestDumpSchemaAfterCommandIsNotIntercepted(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"run", "--dump-schema"})
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Stderr, "unknown flag") {
		t.Fatalf("expected unknown flag error, got: %s", r.Stderr)
	}
}

func TestDumpSchemaAfterDoubleDash(t *testing.T) {
	app := NewApp("testapp", "1.0.0", "test app")
	app.Command("run", "run", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"--", "--dump-schema"})
	// After --, --dump-schema is treated as a command name (unknown command error)
	if r.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %d", r.ExitCode)
	}
}

// ---- no-default-config-path tests ----

func TestNoDefaultConfigPath(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	// Write a config file at the XDG default location
	dir := filepath.Join(tmpDir, "testapp")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"port": 6666}`), 0o644)

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithNoDefaultConfigPath())
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("port=%d", args["port"]))
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
	), WithEffect(EffectReadOnly))

	// Without --config, the XDG default is NOT loaded
	r := app.Test([]string{"serve"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=8080") {
		t.Fatalf("expected default port=8080 with no-default-config-path, got %q", r.Stdout)
	}
}

func TestNoDefaultConfigPathWithConfigFlag(t *testing.T) {
	tmpDir, cleanup := configTestSetup(t)
	defer cleanup()

	// Write a config file at a custom path
	customPath := filepath.Join(tmpDir, "explicit.json")
	os.WriteFile(customPath, []byte(`{"port": 3333}`), 0o644)

	app := NewApp("testapp", "1.0.0", "test app", WithConfig(), WithNoDefaultConfigPath())
	app.Command("serve", "start server", func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Out(fmt.Sprintf("port=%d", args["port"]))
		return Exit(0)
	}, WithFlags(
		IntFlag("port", "port number", Default(8080)),
	), WithEffect(EffectReadOnly))

	// With --config, the explicit path IS loaded
	r := app.Test([]string{"--config", customPath, "serve"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "port=3333") {
		t.Fatalf("expected port=3333 from --config with no-default-config-path, got %q", r.Stdout)
	}
}

// === ConfigField / Flag coexistence ===

func TestFieldFlagShowPlainRendersOnceWithAnnotation(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"target": "prod"}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("target", "deploy target", Required())), WithEffect(EffectReadOnly))
	app.ConfigField("target", ConfigFieldHelp("the deploy target"))

	r := app.Test([]string{"config", "show", "--plain"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	// One line for target, on the flag line, with the annotation.
	count := strings.Count(r.Stdout, "target =")
	if count != 1 {
		t.Fatalf("expected exactly one 'target =' line, got %d:\n%s", count, r.Stdout)
	}
	if !strings.Contains(r.Stdout, "-- the deploy target") {
		t.Fatalf("expected annotation on flag line, got: %s", r.Stdout)
	}
	if strings.Contains(r.Stdout, "Config fields:") {
		t.Fatalf("colliding field must not appear in Config fields section: %s", r.Stdout)
	}
}

func TestFieldFlagShowJSONRendersOnce(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	os.WriteFile(configFile, []byte(`{"target": "prod"}`), 0o644)

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("target", "deploy target", Required())), WithEffect(EffectReadOnly))
	app.ConfigField("target", ConfigFieldHelp("the deploy target"))

	r := app.Test([]string{"config", "show", "--json"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(envelopePayload(t, r.Stdout), &result); err != nil {
		t.Fatalf("bad json: %v\n%s", err, r.Stdout)
	}
	entry, ok := result["target"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected target entry, got: %v", result["target"])
	}
	if entry["value"] != "prod" || entry["source"] != "config" {
		t.Fatalf("expected flag entry value/source, got: %v", entry)
	}
	// The config-field-only keys must be absent (rendered as flag entry, once).
	if _, has := entry["type"]; has {
		t.Fatalf("colliding key rendered as config-field entry, not flag: %v", entry)
	}
}

func TestFieldFlagInitTomlRendersOnce(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.toml"
	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile), WithConfigFormat("toml"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("target", "deploy target", Default("prod"))), WithEffect(EffectReadOnly))
	app.ConfigField("target", ConfigFieldHelp("the deploy target"), ConfigFieldDefault("prod"))

	r := app.Test([]string{"config", "init"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	content, _ := os.ReadFile(configFile)
	if c := strings.Count(string(content), "target ="); c != 1 {
		t.Fatalf("expected exactly one 'target =' entry, got %d:\n%s", c, content)
	}
	if !strings.Contains(string(content), "-- the deploy target") {
		t.Fatalf("expected annotation in template, got:\n%s", content)
	}
}

func TestFieldFlagInitJSONRendersOnce(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.json"
	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("target", "deploy target", Default("prod"))), WithEffect(EffectReadOnly))
	app.ConfigField("target", ConfigFieldHelp("the deploy target"), ConfigFieldDefault("prod"))

	r := app.Test([]string{"config", "init"})
	if r.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", r.ExitCode, r.Stderr)
	}
	content, _ := os.ReadFile(configFile)
	var data map[string]interface{}
	if err := json.Unmarshal(content, &data); err != nil {
		t.Fatalf("bad json: %v\n%s", err, content)
	}
	if data["target"] != "prod" {
		t.Fatalf("expected target=prod once, got: %v", data)
	}
	if strings.Count(string(content), "\"target\"") != 1 {
		t.Fatalf("expected target key once, got:\n%s", content)
	}
}

func TestFieldFlagUnequalDefaultsFieldAfterFlagPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on unequal defaults")
		}
	}()
	app := NewApp("test", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("target", "deploy target", Default("prod"))), WithEffect(EffectReadOnly))
	app.ConfigField("target", ConfigFieldHelp("the deploy target"), ConfigFieldDefault("staging"))
}

func TestFieldFlagUnequalDefaultsFlagAfterFieldPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on unequal defaults")
		}
	}()
	app := NewApp("test", "1.0.0", "test app", WithConfig())
	app.ConfigField("target", ConfigFieldHelp("the deploy target"), ConfigFieldDefault("staging"))
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("target", "deploy target", Default("prod"))), WithEffect(EffectReadOnly))
}

func TestFieldFlagEqualDefaultsOK(t *testing.T) {
	app := NewApp("test", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("target", "deploy target", Default("prod"))), WithEffect(EffectReadOnly))
	app.ConfigField("target", ConfigFieldHelp("the deploy target"), ConfigFieldDefault("prod"))
}

func TestFieldFlagOneAbsentDefaultOK(t *testing.T) {
	// Flag has default, field has none.
	app := NewApp("test", "1.0.0", "test app", WithConfig())
	app.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("target", "deploy target", Default("prod"))), WithEffect(EffectReadOnly))
	app.ConfigField("target", ConfigFieldHelp("the deploy target"))

	// Field has default, flag has none.
	app2 := NewApp("test", "1.0.0", "test app", WithConfig())
	app2.Command("run", "run it", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) },
		WithFlags(StringFlag("target", "deploy target", Required())), WithEffect(EffectReadOnly))
	app2.ConfigField("target", ConfigFieldHelp("the deploy target"), ConfigFieldDefault("prod"))
}

// TestConfigSetTOMLPreservesComments verifies that `config set` on a TOML
// config file preserves comments and custom key ordering byte-for-byte,
// changing only the targeted key's value. Regression: a previous
// implementation re-marshaled a plain map, destroying comments and
// alphabetizing keys.
func TestConfigSetTOMLPreservesComments(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.toml"

	// Untouched keys (zulu, port) keep alignment padding to prove it survives.
	// The changed key (region) uses a single space before its inline comment:
	// go-toml-edit normalizes pre-comment whitespace to one space when it
	// rewrites a value node, so this keeps the expected output deterministic
	// while the value itself stays the same length.
	original := `# Top-of-file comment must survive
# second line of banner

zulu = "z"            # trailing comment on an unregistered key
region = "us-east-1" # inline note on region

# --- server block ---
[server]
port = 8080           # the listen port
host = "localhost"
`
	if err := os.WriteFile(configFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write seed config: %v", err)
	}

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile), WithConfigFormat("toml"))
	app.ConfigField("region", ConfigFieldHelp("Region"), ConfigFieldDefault("us-east-1"))
	app.ConfigField("server.host", ConfigFieldHelp("Server host"), ConfigFieldDefault("localhost"))
	app.ConfigField("server.port", ConfigFieldHelp("Server port"), ConfigFieldType(TypeInt), ConfigFieldDefault(8080))
	app.Command("deploy", "Deploy", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "set", "region", "--value", "us-west-2"})
	if r.ExitCode != 0 {
		t.Fatalf("config set exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}

	got, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	want := strings.Replace(original, `region = "us-east-1"`, `region = "us-west-2"`, 1)
	if string(got) != want {
		t.Fatalf("TOML config not preserved byte-for-byte.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

// TestConfigSetTOMLDefaultRemovesKeyPreservingRest verifies `config set --default`
// deletes only the targeted key from a TOML config while preserving comments
// and ordering of the remaining keys.
func TestConfigSetTOMLDefaultRemovesKeyPreservingRest(t *testing.T) {
	dir := t.TempDir()
	configFile := dir + "/config.toml"

	original := `# banner comment
region = "us-east-1"

# server section comment
[server]
host = "example.com"  # keep me
port = 9090
`
	if err := os.WriteFile(configFile, []byte(original), 0o644); err != nil {
		t.Fatalf("write seed config: %v", err)
	}

	app := NewApp("test", "1.0.0", "test app", WithConfig(), WithConfigPath(configFile), WithConfigFormat("toml"))
	app.ConfigField("region", ConfigFieldHelp("Region"), ConfigFieldDefault("us-east-1"))
	app.ConfigField("server.host", ConfigFieldHelp("Server host"), ConfigFieldDefault("localhost"))
	app.ConfigField("server.port", ConfigFieldHelp("Server port"), ConfigFieldType(TypeInt), ConfigFieldDefault(8080))
	app.Command("deploy", "Deploy", func(ctx *Context, args map[string]interface{}) Outcome { return Exit(0) }, WithEffect(EffectReadOnly))

	r := app.Test([]string{"config", "set", "server.port", "--default"})
	if r.ExitCode != 0 {
		t.Fatalf("config set --default exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}

	got, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	gotStr := string(got)
	if strings.Contains(gotStr, "port") {
		t.Fatalf("expected server.port removed, got:\n%s", gotStr)
	}
	// Untouched comments and keys must remain.
	for _, must := range []string{"# banner comment", `region = "us-east-1"`, "# server section comment", `host = "example.com"  # keep me`} {
		if !strings.Contains(gotStr, must) {
			t.Fatalf("expected %q preserved, got:\n%s", must, gotStr)
		}
	}
}
