package strictcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	tomledit "github.com/stricttools/go-toml-edit"
)

// nestedGet looks up a dot-separated key in a nested map.
// Returns (value, true) if found, (nil, false) if any intermediate
// segment is missing or not a map.
func nestedGet(data map[string]interface{}, dotPath string) (interface{}, bool) {
	parts := strings.Split(dotPath, ".")
	var current interface{} = data
	for _, part := range parts[:len(parts)-1] {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	m, ok := current.(map[string]interface{})
	if !ok {
		return nil, false
	}
	val, ok := m[parts[len(parts)-1]]
	return val, ok
}

// nestedSet sets a dot-separated key in a nested map, creating
// intermediate maps as needed.
func nestedSet(data map[string]interface{}, dotPath string, value interface{}) {
	parts := strings.Split(dotPath, ".")
	current := data
	for _, part := range parts[:len(parts)-1] {
		if sub, ok := current[part]; ok {
			if subMap, ok := sub.(map[string]interface{}); ok {
				current = subMap
			} else {
				subMap := make(map[string]interface{})
				current[part] = subMap
				current = subMap
			}
		} else {
			subMap := make(map[string]interface{})
			current[part] = subMap
			current = subMap
		}
	}
	current[parts[len(parts)-1]] = value
}

// nestedDelete deletes a dot-separated key from a nested map.
// Returns true if the key was found and deleted, false otherwise.
// Cleans up empty intermediate maps.
func nestedDelete(data map[string]interface{}, dotPath string) bool {
	parts := strings.Split(dotPath, ".")
	type parentEntry struct {
		m   map[string]interface{}
		key string
	}
	var parents []parentEntry
	current := data
	for _, part := range parts[:len(parts)-1] {
		sub, ok := current[part]
		if !ok {
			return false
		}
		subMap, ok := sub.(map[string]interface{})
		if !ok {
			return false
		}
		parents = append(parents, parentEntry{m: current, key: part})
		current = subMap
	}
	lastKey := parts[len(parts)-1]
	if _, ok := current[lastKey]; !ok {
		return false
	}
	delete(current, lastKey)
	// Clean up empty intermediate maps
	for i := len(parents) - 1; i >= 0; i-- {
		p := parents[i]
		child := p.m[p.key].(map[string]interface{})
		if len(child) == 0 {
			delete(p.m, p.key)
		}
	}
	return true
}

// collectNestedKeys flattens a nested map to dot-separated leaf key paths.
// Non-map values are leaves; map values are recursed into.
func collectNestedKeys(data map[string]interface{}, prefix string) []string {
	var keys []string
	for k, v := range data {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}
		if subMap, ok := v.(map[string]interface{}); ok {
			keys = append(keys, collectNestedKeys(subMap, fullKey)...)
		} else {
			keys = append(keys, fullKey)
		}
	}
	return keys
}

// ConfigField describes a declared config file field.
type ConfigField struct {
	Name       string
	Type       FlagType
	Help       string
	Default    interface{}
	HasDefault bool
	Required   bool // computed: !HasDefault
}

// ConfigFieldOption configures a ConfigField.
type ConfigFieldOption func(*ConfigField)

// ConfigFieldType sets the type for a config field (default: TypeStr).
func ConfigFieldType(t FlagType) ConfigFieldOption {
	return func(cf *ConfigField) {
		cf.Type = t
	}
}

// ConfigFieldHelp sets the help text for a config field (required).
func ConfigFieldHelp(help string) ConfigFieldOption {
	return func(cf *ConfigField) {
		cf.Help = help
	}
}

// ConfigFieldDefault sets the default value for a config field.
func ConfigFieldDefault(v interface{}) ConfigFieldOption {
	return func(cf *ConfigField) {
		cf.Default = v
		cf.HasDefault = true
	}
}

// configFieldNameRe validates config field names: optional underscore prefix
// (reserved for framework), then a letter followed by lowercase letters,
// digits, and underscores. Dots separate segments, each starting with a letter.
// Matches Python's _CONFIG_FIELD_NAME_RE.
var configFieldNameRe = regexp.MustCompile(`^_?[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// ConfigField declares a config field on the app.
// Panics on invalid configuration (programmer error).
func (a *App) ConfigField(name string, opts ...ConfigFieldOption) {
	cf := &ConfigField{
		Name: name,
		Type: TypeStr, // default type
	}
	for _, opt := range opts {
		opt(cf)
	}
	cf.Required = !cf.HasDefault

	// Validate name format
	if !configFieldNameRe.MatchString(name) {
		panic(errConfigFieldNameInvalid(name))
	}

	// Names starting with _ are reserved for framework fields
	if strings.HasPrefix(name, "_") {
		panic(errConfigFieldNameReserved(name))
	}

	// Validate help is non-empty
	if strings.TrimSpace(cf.Help) == "" {
		panic(errConfigFieldHelpRequired(name))
	}

	// Validate type
	switch cf.Type {
	case TypeStr, TypeBool, TypeInt, TypeFloat:
		// ok
	default:
		panic(errConfigFieldTypeBad(cf.Type))
	}

	// Validate default matches type
	if cf.HasDefault && cf.Default != nil {
		validateConfigFieldDefault(name, cf.Type, cf.Default)
	}

	// Check for duplicate names (user fields and framework fields)
	if a.configFields == nil {
		a.configFields = make(map[string]*ConfigField)
	}
	if _, ok := a.configFields[name]; ok {
		panic(errDuplicateConfigField(name))
	}
	if a.frameworkFields != nil {
		if _, ok := a.frameworkFields[name]; ok {
			panic(errConfigFieldConflictsFramework(name))
		}
	}

	// A config field colliding with an existing flag's param name is a
	// validation-only declaration that annotates the flag; their defaults must
	// agree. Flags registered after this field are checked from the command
	// registration side instead.
	for _, f := range a.collectAllFlags() {
		if flagParamName(f.Name) == name {
			checkFlagConfigFieldDefault(f.Name, f.presence, f.Default, cf)
		}
	}

	a.configFields[name] = cf
	a.configFieldOrder = append(a.configFieldOrder, name)
}

// collidingConfigFields returns config fields whose name equals a flag's param
// name, keyed by that param name. Such fields are validation-only: they
// annotate the colliding flag and render once (on the flag), not as a separate
// config key.
func (a *App) collidingConfigFields() map[string]*ConfigField {
	result := make(map[string]*ConfigField)
	if len(a.configFields) == 0 {
		return result
	}
	flagParams := make(map[string]bool)
	for _, f := range a.collectAllFlags() {
		flagParams[flagParamName(f.Name)] = true
	}
	for name, cf := range a.configFields {
		if flagParams[name] {
			result[name] = cf
		}
	}
	return result
}

// registerFrameworkField declares an internal framework config field.
// Framework fields use underscore-prefixed names and are not exposed to users.
func (a *App) registerFrameworkField(name string, fieldType FlagType, help string) {
	if !strings.HasPrefix(name, "_") {
		panic(errFrameworkFieldMustStartUnderscore(name))
	}

	if !configFieldNameRe.MatchString(name) {
		panic(errFrameworkFieldNameInvalid(name))
	}

	if strings.TrimSpace(help) == "" {
		panic(errFrameworkFieldHelpRequired(name))
	}

	switch fieldType {
	case TypeStr, TypeBool, TypeInt, TypeFloat:
		// ok
	default:
		panic(errConfigFieldTypeBad(fieldType))
	}

	if a.frameworkFields == nil {
		a.frameworkFields = make(map[string]*ConfigField)
	}
	if _, ok := a.frameworkFields[name]; ok {
		panic(errDuplicateFrameworkField(name))
	}
	if a.configFields != nil {
		if _, ok := a.configFields[name]; ok {
			panic(errFrameworkFieldConflictsUser(name))
		}
	}

	cf := &ConfigField{
		Name:     name,
		Type:     fieldType,
		Help:     help,
		Required: true, // framework fields have no default
	}

	a.frameworkFields[name] = cf
	a.frameworkFieldOrder = append(a.frameworkFieldOrder, name)
}

// validateConfigFieldDefault panics if the default value doesn't match the declared type.
func validateConfigFieldDefault(name string, fieldType FlagType, value interface{}) {
	switch fieldType {
	case TypeStr:
		if _, ok := value.(string); !ok {
			panic(errConfigFieldDefaultMismatch(name, value, "str"))
		}
	case TypeBool:
		if _, ok := value.(bool); !ok {
			panic(errConfigFieldDefaultMismatch(name, value, "bool"))
		}
	case TypeInt:
		if _, ok := value.(int); !ok {
			panic(errConfigFieldDefaultMismatch(name, value, "int"))
		}
	case TypeFloat:
		if _, ok := value.(float64); !ok {
			panic(errConfigFieldDefaultMismatch(name, value, "float"))
		}
	}
}

// describeGoType returns a human-readable type name for a Go value,
// using strictcli type names (str, bool, int, float).
func describeGoType(v interface{}) string {
	switch v.(type) {
	case string:
		return "str"
	case bool:
		return "bool"
	case int:
		return "int"
	case float64:
		return "float"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// configPath returns the full path to the config file for an app.
// If override is non-empty, it is returned as-is.
// format should be "json" or "toml" and determines the file extension.
func configPath(appName string, override string, format string) string {
	if override != "" {
		return override
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		configHome = filepath.Join(home, ".config")
	}
	ext := "json"
	if format == "toml" {
		ext = "toml"
	}
	return filepath.Join(configHome, appName, "config."+ext)
}

// configLoadResult holds the result of loading a config file.
// If parseErr is non-empty, the file existed but was malformed.
type configLoadResult struct {
	data     map[string]interface{}
	parseErr string // non-empty if file was malformed (includes position info)
}

// loadConfig reads the config file for an app.
// Missing file with isRuntimeFlag=true is a hard error (user explicitly passed --config).
// Missing file with isRuntimeFlag=false is soft (returns empty map, no error).
// Malformed file is always a hard error with position information.
func loadConfig(appName string, pathOverride string, format string, isRuntimeFlag bool) configLoadResult {
	path := configPath(appName, pathOverride, format)
	data, err := os.ReadFile(path)
	if err != nil {
		if isRuntimeFlag {
			return configLoadResult{parseErr: fmt.Sprintf("config file not found: %s", path)}
		}
		return configLoadResult{data: map[string]interface{}{}}
	}
	var result map[string]interface{}
	switch format {
	case "toml":
		parsed, err := tomledit.Unmarshal[map[string]interface{}](data)
		if err != nil {
			var pe *tomledit.Error
			if errors.As(err, &pe) {
				return configLoadResult{
					parseErr: fmt.Sprintf("config file %s: %s (line %d, column %d)", path, pe.Message, pe.Pos.Line, pe.Pos.Column),
				}
			}
			return configLoadResult{
				parseErr: fmt.Sprintf("config file %s: %s", path, err.Error()),
			}
		}
		result = *parsed
	default:
		if err := json.Unmarshal(data, &result); err != nil {
			if se, ok := err.(*json.SyntaxError); ok {
				line, col := computeJSONPosition(data, se.Offset)
				return configLoadResult{
					parseErr: fmt.Sprintf("config file %s: %s (line %d, column %d)", path, se.Error(), line, col),
				}
			}
			return configLoadResult{
				parseErr: fmt.Sprintf("config file %s: %s", path, err.Error()),
			}
		}
	}
	return configLoadResult{data: result}
}

// computeJSONPosition converts a byte offset to 1-based line and column.
func computeJSONPosition(data []byte, offset int64) (int, int) {
	line := 1
	col := 1
	for i := int64(0); i < offset && i < int64(len(data)); i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// resolveConfigData loads config data for the app. This is the single
// entry point for all config loading.
// isRuntimeFlag indicates the path came from --config (hard error on missing).
func (a *App) resolveConfigData(runtimePathOverride string, hermetic bool, isRuntimeFlag bool) configLoadResult {
	if hermetic {
		return configLoadResult{data: map[string]interface{}{}}
	}
	override := a.configPathOverride
	if runtimePathOverride != "" {
		override = runtimePathOverride
	}
	return loadConfig(a.Name, override, a.configFormat, isRuntimeFlag)
}

// coerceConfigScalar coerces a single JSON-decoded value to the given flag type.
// Returns the coerced value and an error string (empty on success).
// When shortNames is true (config field validation path), uses short type names
// ("bool", "int", "str", "float") to match Python's _check_config_field_type.
// When shortNames is false (flag coercion path), uses long type names
// ("boolean", "integer", "string", "float") to match Python's _coerce_config_scalar.
func coerceConfigScalar(value interface{}, flagType FlagType, shortNames bool) (interface{}, string) {
	if shortNames {
		return coerceConfigScalarShort(value, flagType)
	}
	return coerceConfigScalarLong(value, flagType)
}

// coerceConfigScalarLong uses long type names for the flag coercion path.
func coerceConfigScalarLong(value interface{}, flagType FlagType) (interface{}, string) {
	switch flagType {
	case TypeBool:
		if b, ok := value.(bool); ok {
			return b, ""
		}
		return nil, errConfigExpectedBooleanGot(typeName(value))
	case TypeInt:
		// TOML integers decode as int64; JSON numbers decode as float64
		if val, ok := value.(int64); ok {
			return int(val), ""
		}
		if fv, ok := value.(float64); ok {
			intVal := int(fv)
			if float64(intVal) == fv {
				return intVal, ""
			}
			return nil, errConfigExpectedIntegerGotFloat
		}
		return nil, errConfigExpectedIntegerGot(typeName(value))
	case TypeFloat:
		// TOML integers decode as int64; JSON numbers decode as float64
		if val, ok := value.(int64); ok {
			return float64(val), ""
		}
		if fv, ok := value.(float64); ok {
			return fv, ""
		}
		return nil, errExpectedFloatGot(typeName(value))
	case TypeStr:
		if s, ok := value.(string); ok {
			return s, ""
		}
		return nil, errConfigExpectedStringGot(typeName(value))
	}
	return nil, errConfigUnsupportedFlagType(flagType)
}

// coerceConfigScalarShort uses short type names for the config field validation path.
func coerceConfigScalarShort(value interface{}, flagType FlagType) (interface{}, string) {
	switch flagType {
	case TypeBool:
		if b, ok := value.(bool); ok {
			return b, ""
		}
		return nil, errExpectedBoolGot(typeName(value))
	case TypeInt:
		// TOML integers decode as int64; JSON numbers decode as float64
		if val, ok := value.(int64); ok {
			return int(val), ""
		}
		if fv, ok := value.(float64); ok {
			intVal := int(fv)
			if float64(intVal) == fv {
				return intVal, ""
			}
			return nil, errConfigExpectedIntGotFloat
		}
		return nil, errExpectedIntGot(typeName(value))
	case TypeFloat:
		// TOML integers decode as int64; JSON numbers decode as float64
		if val, ok := value.(int64); ok {
			return float64(val), ""
		}
		if fv, ok := value.(float64); ok {
			return fv, ""
		}
		return nil, errExpectedFloatGot(typeName(value))
	case TypeStr:
		if s, ok := value.(string); ok {
			return s, ""
		}
		return nil, errExpectedStrGot(typeName(value))
	}
	return nil, errConfigUnsupportedFlagType(flagType)
}

// coerceConfigValue coerces a JSON-decoded value to the flag's type.
// Handles scalar values, arrays (for repeatable/list flags), and objects (for dict flags).
// Returns the coerced value and an error string (empty on success).
func coerceConfigValue(value interface{}, f *Flag) (interface{}, string) {
	// Dict flags: expect a JSON object in config
	if IsDictType(f.Type) {
		m, ok := value.(map[string]interface{})
		if !ok {
			return nil, errConfigExpectedObjectForDictFlag(typeName(value))
		}
		valType := ItemType(f.Type)
		result := make(map[string]interface{}, len(m))
		for k, v := range m {
			coerced, errStr := coerceConfigScalar(v, valType, false)
			if errStr != "" {
				return nil, errConfigDictKeyTypeMismatch(k, flagTypeName[valType], typeName(v))
			}
			result[k] = coerced
		}
		return result, ""
	}
	// List flags: expect a JSON array in config
	if IsListType(f.Type) {
		arr, ok := value.([]interface{})
		if !ok {
			return nil, errConfigExpectedArrayForListFlag(typeName(value))
		}
		elemType := ItemType(f.Type)
		result := make([]interface{}, len(arr))
		for i, elem := range arr {
			coerced, errStr := coerceConfigScalar(elem, elemType, false)
			if errStr != "" {
				return nil, errConfigElementTypeMismatch(i, flagTypeName[elemType], typeName(elem))
			}
			result[i] = coerced
		}
		return result, ""
	}
	if arr, ok := value.([]interface{}); ok {
		if !f.Repeatable {
			return nil, errConfigExpectedScalarGotArray
		}
		result := make([]interface{}, len(arr))
		for i, elem := range arr {
			coerced, errStr := coerceConfigScalar(elem, f.Type, false)
			if errStr != "" {
				return nil, errConfigElementTypeMismatch(i, flagTypeName[f.Type], typeName(elem))
			}
			result[i] = coerced
		}
		return result, ""
	}
	if f.Repeatable {
		return nil, errConfigExpectedArrayForRepeatableFlag(typeName(value))
	}
	return coerceConfigScalar(value, f.Type, false)
}

// typeName returns a human-readable type name for a config-decoded value.
func typeName(v interface{}) string {
	switch v.(type) {
	case bool:
		return "bool"
	case int64:
		return "int"
	case float64:
		fv := v.(float64)
		if math.Floor(fv) == fv && !math.IsInf(fv, 0) && !math.IsNaN(fv) {
			return "int"
		}
		return "float"
	case string:
		return "str"
	case nil:
		return "null"
	case []interface{}:
		return "array"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// collectAllFlags collects all flags (global + all commands in all groups) for config show.
func (a *App) collectAllFlags() []Flag {
	var flags []Flag
	seen := make(map[string]bool)
	for _, f := range a.globalFlags {
		flags = append(flags, f)
		seen[f.Name] = true
	}
	for _, name := range a.cmdOrder {
		cmd := a.commands[name]
		for _, f := range cmd.flags {
			if !seen[f.Name] {
				flags = append(flags, f)
				seen[f.Name] = true
			}
		}
	}
	var collectFromGroup func(grp *Group)
	collectFromGroup = func(grp *Group) {
		for _, name := range grp.order {
			cmd := grp.Commands[name]
			for _, f := range cmd.flags {
				if !seen[f.Name] {
					flags = append(flags, f)
					seen[f.Name] = true
				}
			}
		}
		for _, name := range grp.groupOrder {
			collectFromGroup(grp.Groups[name])
		}
	}
	for _, name := range a.groupOrder {
		if name == "config" {
			continue // skip auto-generated config group
		}
		collectFromGroup(a.groups[name])
	}
	return flags
}

// configChange describes a single config mutation applied to the file.
// For TOML it is applied to the parsed document (preserving comments and
// key order); for JSON the whole map is re-marshaled and the change is
// already reflected in it.
type configChange struct {
	key    string      // dot-separated key path
	value  interface{} // value to set; ignored when remove is true
	remove bool        // true = delete the key
}

// writeConfigFile persists a single config mutation THROUGH ctx.Effects().
//
// The JSON branch re-marshals the full data map (the caller has already
// applied the change to it). The TOML branch parses the existing file,
// applies the change via go-toml-edit's document API, and re-serializes —
// preserving comments, formatting, and key order for all untouched keys.
//
// The write is a FILE_WRITE on the effects handle, not a bare os.WriteFile:
// config set is classified mutating, so under --dry-run the write must be
// RECORDED, never performed. A framework command that printed
// "DRY RUN — no changes were made." while rewriting the user's config file
// would be the loudest possible counterexample to its own regime.
func writeConfigFile(ctx *Context, data map[string]interface{}, path string, format string, change configChange) int {
	e := ctx.Effects()
	switch format {
	case "toml":
		return writeConfigFileTOML(ctx, path, change)
	default:
		raw, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			ctx.Error(fmt.Sprintf("cannot marshal config: %s", err))
			return 1
		}
		raw = append(raw, '\n')
		if _, err := e.Write(path, raw); err != nil {
			ctx.Error(fmt.Sprintf("cannot write config file: %s", err))
			return 1
		}
		return 0
	}
}

// ensureConfigDir records/performs the config file's parent directory creation.
//
// The existence probe is an ordinary filesystem READ (never an effect), and
// branching on it is branching on a real value, so the preview walks straight
// through it in both modes. Probing keeps the preview honest: a mkdir line
// appears only when a directory would really be created.
func ensureConfigDir(ctx *Context, path string) int {
	e := ctx.Effects()
	dirPath := filepath.Dir(path)
	if dirPath == "" {
		return 0
	}
	if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
		return 0
	}
	if _, err := e.Mkdir(dirPath); err != nil {
		ctx.Error(fmt.Sprintf("cannot create config directory: %s", err))
		return 1
	}
	return 0
}

// writeConfigFileTOML applies a single mutation to a TOML config file while
// preserving comments and the existing key order. It parses the file into
// go-toml-edit's lossless document AST, applies the change, and writes the
// bytes back. Bytes() (round-trip fidelity) is used rather than Format() so
// that unrelated formatting — blank lines, alignment, comment placement —
// survives byte-for-byte; only the changed key is touched.
func writeConfigFileTOML(ctx *Context, path string, change configChange) int {
	e := ctx.Effects()
	var doc *tomledit.Document
	existingBytes, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			ctx.Error(fmt.Sprintf("cannot read config file: %s", err))
			return 1
		}
		// New file: start from an empty document.
		doc, err = tomledit.Parse(nil)
		if err != nil {
			ctx.Error(fmt.Sprintf("cannot initialize config document: %s", err))
			return 1
		}
	} else {
		doc, err = tomledit.Parse(existingBytes)
		if err != nil {
			ctx.Error(fmt.Sprintf("cannot parse config file: %s", err))
			return 1
		}
	}

	if change.remove {
		if err := doc.Delete(change.key); err != nil {
			ctx.Error(fmt.Sprintf("cannot update config: %s", err))
			return 1
		}
	} else {
		if err := doc.SetCreate(change.key, change.value); err != nil {
			ctx.Error(fmt.Sprintf("cannot update config: %s", err))
			return 1
		}
	}

	if _, err := e.Write(path, doc.Bytes()); err != nil {
		ctx.Error(fmt.Sprintf("cannot write config file: %s", err))
		return 1
	}
	return 0
}

// resolveFlagShowSource resolves the effective value and source for a flag
// in the config show context. Precedence: env > config > default.
// "cli" is structurally impossible in config show because the app's own
// flags were never passed on the command line.
func resolveFlagShowSource(f *Flag, configData map[string]interface{}) (interface{}, string) {
	// Check env first (highest precedence after CLI)
	if f.Env != "" {
		if envVal, ok := os.LookupEnv(f.Env); ok {
			// Coerce the env value to the flag's type.
			// For show purposes, we display the raw string if coercion
			// fails (the parse-time error path handles actual errors).
			switch f.Type {
			case TypeBool:
				if boolVal, err := parseBoolStrict(envVal); err == nil {
					return boolVal, "env"
				}
			case TypeInt:
				if intVal, err := parseIntStrict(envVal); err == nil {
					return intVal, "env"
				}
			case TypeFloat:
				if floatVal, err := parseFloatStrictValue(envVal); err == nil {
					return floatVal, "env"
				}
			default:
				return envVal, "env"
			}
			// If coercion failed, still report env source with the raw string
			return envVal, "env"
		}
	}
	// Check config
	param := flagParamName(f.Name)
	if v, ok := configData[param]; ok {
		// Coerce to the flag's declared type so display matches runtime
		// behavior (encoding/json yields float64 for every number; an int
		// flag's value must display as "42", not "42.0" -- Python and
		// TypeScript both display the integer form). If coercion fails,
		// display the raw value (the parse-time error path reports it).
		if coerced, errStr := coerceConfigValue(v, f); errStr == "" {
			return coerced, "config"
		}
		return v, "config"
	}
	// Default
	if f.presence == presenceDefault {
		return f.Default, "default"
	}
	return nil, "default"
}

// registerConfigGroup registers the auto-generated 'config' command group.
func (a *App) registerConfigGroup() {
	grp := a.Group("config", "Manage persistent configuration values stored in the config file")

	// config path
	registerFrameworkSubcommand(grp, "path", "Print the absolute path to this application's config file and nothing else, so the value can be piped straight into another command. The path is $XDG_CONFIG_HOME/<app>/config.<toml|json> (falling back to ~/.config), or the explicit override the application was built with. Printing it does not create the file, and reports the same path whether or not one exists yet.", EffectReadOnly, func(ctx *Context, args map[string]interface{}) Outcome {
		ctx.Info(configPath(a.Name, a.configPathOverride, a.configFormat))
		return Exit(0)
	})

	// config show
	//
	// Source resolution uses the shared precedence chain: env > config > default.
	// "cli" is structurally impossible here -- config show is a subcommand,
	// so the app's own flags were never passed on the command line.
	// If the config file is malformed, shows the parse error instead of values.
	registerFrameworkSubcommand(grp, "show", "Show every flag and config field with its effective value and where that value came from, resolved through the precedence chain environment variable, then config file, then declared default. Declared infrastructure roots, handshake and connection environment variables are listed too. Choose --plain for an aligned human-readable table; the framework-owned --json yields the same information as a machine-readable object carrying each entry's type, default and help text.", EffectReadOnly, func(ctx *Context, args map[string]interface{}) Outcome {
		// If there was a config parse error, show it instead of values
		if ctx.configParseErr != "" {
			ctx.Error(ctx.configParseErr)
			return Exit(1)
		}
		// --json is framework-owned (contract §19.1): the object below is this
		// command's payload, not a locally-flagged print, and it is supplied
		// UNCONDITIONALLY (§19.4). Instance validation lives at the emission
		// seam, so a config value machine mode could not carry -- a float above
		// 2^53 -- costs the human rendering nothing.
		configData := ctx.configData
		allFlags := a.collectAllFlags()
		colliding := a.collidingConfigFields()

		result := make(map[string]interface{})
		for _, f := range allFlags {
			param := flagParamName(f.Name)
			value, source := resolveFlagShowSource(&f, configData)
			result[param] = map[string]interface{}{
				// A RelativeToRoot default reaches the payload as the marker
				// itself (the human form below prints the declaration, not the
				// path a run would deliver). Its fields are unexported, so
				// handing it to encoding/json publishes "{}" -- an empty object
				// where the document pins one marker shape (§13). It rides the
				// same serializer the dumped schema uses, flattened to plain
				// maps because a payload is written by encoding/json rather
				// than by the schema canon's writer. Every other value passes
				// through both calls unchanged.
				"value":  toPlain(serializeDefault(value)),
				"source": source,
			}
		}
		// Include config fields (skip those colliding with a flag: they are
		// validation-only and render once, on the flag entry).
		for _, name := range a.configFieldOrder {
			if _, isColliding := colliding[name]; isColliding {
				continue
			}
			cf := a.configFields[name]
			var value interface{}
			var source string
			if v, ok := nestedGet(configData, name); ok {
				value = v
				source = "config"
			} else if cf.HasDefault {
				value = cf.Default
				source = "default"
			} else {
				value = nil
				source = "not set"
			}
			cfEntry := map[string]interface{}{
				"value":    value,
				"source":   source,
				"type":     flagTypeName[cf.Type],
				"required": cf.Required,
				"help":     cf.Help,
			}
			if cf.HasDefault {
				cfEntry["default"] = cf.Default
			}
			result[name] = cfEntry
		}
		// Infrastructure section (roots + handshakes + connections)
		if len(a.infraRootOrder) > 0 || len(a.handshakeOrder) > 0 || len(a.connectionOrder) > 0 {
			infra := make(map[string]interface{})
			for _, ev := range a.infraRootOrder {
				src := "default"
				if a.infraRootFromEnv[ev] {
					src = "env"
				}
				infra[ev] = map[string]interface{}{
					"kind":     "root",
					"source":   src,
					"resolved": a.infraRoots[ev],
				}
			}
			for _, ev := range a.handshakeOrder {
				val, isSet := os.LookupEnv(ev)
				entry := map[string]interface{}{
					"kind": "handshake",
					"set":  isSet,
					"help": a.handshakeEnvs[ev],
				}
				if isSet {
					entry["value"] = val
				}
				infra[ev] = entry
			}
			for _, ev := range a.connectionOrder {
				val, isSet := os.LookupEnv(ev)
				entry := map[string]interface{}{
					"kind": "connection",
					"set":  isSet,
					"help": a.connectionEnvs[ev],
				}
				if isSet {
					entry["value"] = val
				}
				infra[ev] = entry
			}
			result["__infrastructure__"] = infra
		}
		ctx.Payload(result)

		// The human rendering is unconditional too, and goes through the
		// context writers, so in machine mode the same lines ride the
		// envelope's diagnostics (§19.1) exactly as the check command's table
		// does.
		for _, f := range allFlags {
			param := flagParamName(f.Name)
			value, source := resolveFlagShowSource(&f, configData)
			line := fmt.Sprintf("%s = %v  (source: %s)", param, formatConfigValue(value), source)
			// A colliding config field annotates the flag line (rendered once).
			if cf, isColliding := colliding[param]; isColliding {
				line += fmt.Sprintf("  -- %s", cf.Help)
			}
			ctx.Info(line)
		}
		// Include config fields (skip colliding ones: rendered as an annotation
		// on the flag line above).
		var nonColliding []string
		for _, name := range a.configFieldOrder {
			if _, isColliding := colliding[name]; !isColliding {
				nonColliding = append(nonColliding, name)
			}
		}
		if len(nonColliding) > 0 {
			ctx.Info("")
			ctx.Info("Config fields:")
			for _, name := range nonColliding {
				cf := a.configFields[name]
				var value interface{}
				var source string
				if v, ok := nestedGet(configData, name); ok {
					value = v
					source = "config"
				} else if cf.HasDefault {
					value = cf.Default
					source = "default"
				} else {
					value = nil
					source = "not set"
				}
				reqStr := "required"
				if !cf.Required {
					reqStr = "optional"
				}
				ctx.Info(fmt.Sprintf("  %s (%s, %s) = %v  (source: %s)  -- %s",
					name, flagTypeName[cf.Type], reqStr, formatConfigValue(value), source, cf.Help))
			}
		}
		// Infrastructure section (roots + handshakes + connections)
		if len(a.infraRootOrder) > 0 || len(a.handshakeOrder) > 0 || len(a.connectionOrder) > 0 {
			ctx.Info("")
			ctx.Info("Infrastructure:")
			for _, ev := range a.infraRootOrder {
				src := "default"
				if a.infraRootFromEnv[ev] {
					src = "env-set"
				}
				ctx.Info(fmt.Sprintf("  %s (root) = %s  (source: %s)", ev, a.infraRoots[ev], src))
			}
			for _, ev := range a.handshakeOrder {
				val, isSet := os.LookupEnv(ev)
				if isSet {
					ctx.Info(fmt.Sprintf("  %s (handshake) = %s  (set)  -- %s", ev, val, a.handshakeEnvs[ev]))
				} else {
					ctx.Info(fmt.Sprintf("  %s (handshake) = <unset>  -- %s", ev, a.handshakeEnvs[ev]))
				}
			}
			for _, ev := range a.connectionOrder {
				val, isSet := os.LookupEnv(ev)
				if isSet {
					ctx.Info(fmt.Sprintf("  %s (connection) = %s  (set)  -- %s", ev, val, a.connectionEnvs[ev]))
				} else {
					ctx.Info(fmt.Sprintf("  %s (connection) = <unset>  -- %s", ev, a.connectionEnvs[ev]))
				}
			}
		}
		return Exit(0)
	},
		// --plain is the only local flag left: the machine form moved to the
		// framework-owned --json (contract §7.5's sweep box), which cannot be
		// declared here, so the two-flag mutex group went with it.
		WithFlags(BoolFlag("plain", "Display config values in a human-readable table format", Default(false))),
		PayloadSchema(configShowPayloadSchema),
	)

	// config set
	//
	// The write is an EXACTLY-ONE SELECTION over a value, a clear and a reset to
	// default -- a member-spelled selector (contract §27.1, §18.33 item 304).
	// The shape it replaces was two bools declaring Default(false) plus an
	// optional positional, with three hand-rolled guards holding its illegal
	// corners shut, and §27.1's mutating-default ban refuses exactly that: a
	// framework cannot ship a registration guard its own command does not pass,
	// and an exemption for framework-owned commands would be the escape hatch
	// this regime refuses everywhere else.
	//
	// The selection is what the guards used to say. "--clear and --default are
	// mutually exclusive", "cannot provide a value with --clear" and "provide a
	// value, --clear, or --default" are all unrepresentable now: exactly one
	// member is elected, and electing none is the framework's own unsatisfied-
	// selector refusal.
	setWriteValue := MemberChoice(
		StringFlag("value", "Write this value at the key, coerced to the key's own type (comma-separated for a repeatable flag, backslash-escaping a literal comma; a JSON object for a dict flag)", Required()),
		"Write a value at the key")
	setWriteClear := MemberChoice(
		BoolFlag("clear", "Clear a repeatable flag by setting its value to an empty list", Required()),
		"Clear a repeatable flag")
	setWriteDefault := MemberChoice(
		BoolFlag("default", "Reset a key to its default value by removing it from the config file", Required()),
		"Reset the key to its declared default")
	registerFrameworkSubcommand(grp, "set", "Write a persistent value into the config file so it overrides a flag's declared default on every later run. The value is coerced to the flag's own type and rejected if it does not fit: repeatable flags take a comma-separated list (backslash-escape a literal comma) and are checked for duplicates, dict flags take a JSON object. Use --default to drop a key back to its default, and --clear to empty a repeatable flag.", EffectMutating, func(ctx *Context, args map[string]interface{}) Outcome {
		key := Get[string](args, "key")
		path := configPath(a.Name, a.configPathOverride, a.configFormat)
		// Every mutation this handler performs rides ctx.Effects() (through
		// the helpers below): the command is classified mutating, so a dry run
		// must RECORD them and change nothing.
		if code := ensureConfigDir(ctx, path); code != 0 {
			return Exit(code)
		}
		// Read existing config (use the already-loaded data from parse time)
		existing := ctx.configData

		// Look up the key against registered flags and config fields
		allFlags := a.collectAllFlags()
		var matchedFlag *Flag
		var matchedConfigField *ConfigField
		for i := range allFlags {
			if flagParamName(allFlags[i].Name) == key {
				matchedFlag = &allFlags[i]
				break
			}
		}
		if matchedFlag == nil && a.configFields != nil {
			matchedConfigField = a.configFields[key]
		}
		if matchedFlag == nil && matchedConfigField == nil {
			ctx.Error(fmt.Sprintf("config set: unknown key '%s'", key))
			return Exit(1)
		}

		// The elected member says what to write. Match is exhaustive against the
		// declaration, so a fourth member could not be added without every
		// dispatch site naming it.
		write := GetElected(args, "write")

		// --clear: repeatable flags only, writes []
		if write.Is(setWriteClear) {
			if matchedConfigField != nil || !matchedFlag.Repeatable {
				ctx.Error("config set: --clear is only for repeatable flags")
				return Exit(1)
			}
			existing[key] = []interface{}{}
			return Exit(writeConfigFile(ctx, existing, path, a.configFormat, configChange{key: key, value: []interface{}{}}))
		}

		// --default: remove the key from config
		if write.Is(setWriteDefault) {
			if _, ok := nestedGet(existing, key); !ok {
				ctx.Error(fmt.Sprintf("config set: key '%s' not in config", key))
				return Exit(1)
			}
			nestedDelete(existing, key)
			return Exit(writeConfigFile(ctx, existing, path, a.configFormat, configChange{key: key, remove: true}))
		}

		// The value member carries its payload under the reserved field name.
		value := Get[string](write.Fields, scopeReservedValueName)

		// Config field: coerce to config field type
		if matchedConfigField != nil {
			var typedValue interface{}
			switch matchedConfigField.Type {
			case TypeBool:
				v, err := parseBoolStrict(value)
				if err != nil {
					ctx.Error(fmt.Sprintf("config set: key '%s': %s", key, err.Error()))
					return Exit(1)
				}
				typedValue = v
			case TypeInt:
				v, err := parseIntStrict(value)
				if err != nil {
					ctx.Error(fmt.Sprintf("config set: key '%s': %s", key, err.Error()))
					return Exit(1)
				}
				typedValue = v
			case TypeFloat:
				v, err := parseFloatStrictValue(value)
				if err != nil {
					ctx.Error(fmt.Sprintf("config set: key '%s': %s", key, err.Error()))
					return Exit(1)
				}
				typedValue = v
			case TypeStr:
				typedValue = value
			}
			nestedSet(existing, key, typedValue)
			return Exit(writeConfigFile(ctx, existing, path, a.configFormat, configChange{key: key, value: typedValue}))
		}

		// Flag: coerce the string value to the flag's type
		var typedValue interface{}
		if matchedFlag.Repeatable {
			// Split on comma, coerce each element
			parts := splitEscaped(value, ',')
			coerced := make([]interface{}, len(parts))
			switch matchedFlag.Type {
			case TypeInt:
				for i, p := range parts {
					v, err := parseIntStrict(p)
					if err != nil {
						ctx.Error(fmt.Sprintf("config set: key '%s': %s", key, err.Error()))
						return Exit(1)
					}
					coerced[i] = v
				}
			case TypeFloat:
				for i, p := range parts {
					v, err := parseFloatStrictValue(p)
					if err != nil {
						ctx.Error(fmt.Sprintf("config set: key '%s': %s", key, err.Error()))
						return Exit(1)
					}
					coerced[i] = v
				}
			case TypeStr:
				for i, p := range parts {
					coerced[i] = p
				}
			}
			// Unique enforcement
			if matchedFlag.Unique {
				if dup := findDuplicate(coerced); dup != nil {
					ctx.Error(fmt.Sprintf("config set: key '%s': duplicate value '%s'", key, formatValueForError(dup)))
					return Exit(1)
				}
			}
			typedValue = coerced
		} else {
			switch matchedFlag.Type {
			case TypeBool:
				v, err := parseBoolStrict(value)
				if err != nil {
					ctx.Error(fmt.Sprintf("config set: key '%s': %s", key, err.Error()))
					return Exit(1)
				}
				typedValue = v
			case TypeInt:
				v, err := parseIntStrict(value)
				if err != nil {
					ctx.Error(fmt.Sprintf("config set: key '%s': %s", key, err.Error()))
					return Exit(1)
				}
				typedValue = v
			case TypeFloat:
				v, err := parseFloatStrictValue(value)
				if err != nil {
					ctx.Error(fmt.Sprintf("config set: key '%s': %s", key, err.Error()))
					return Exit(1)
				}
				typedValue = v
			case TypeStr:
				typedValue = value
			}
		}

		existing[key] = typedValue
		return Exit(writeConfigFile(ctx, existing, path, a.configFormat, configChange{key: key, value: typedValue}))
	}, WithArgs(
		NewArg("key", "The config key to set, matching a registered flag name", ArgRequired()),
	), WithFlags(
		MemberChoiceFlag("write", "What to write at the key: a value, a clear, or a reset to the declared default", Required(),
			setWriteValue, setWriteClear, setWriteDefault),
	))

	// config edit
	registerFrameworkSubcommand(grp, "edit", "Open this application's config file in the editor named by $EDITOR, falling back to vi. The parent directory and an empty config file are created first if they do not exist, so the editor always opens something. Launching the editor counts as a mutation: under --dry-run the command records the editor invocation and opens nothing.", EffectMutating, func(ctx *Context, args map[string]interface{}) Outcome {
		path := configPath(a.Name, a.configPathOverride, a.configFormat)
		e := ctx.Effects()
		if code := ensureConfigDir(ctx, path); code != 0 {
			return Exit(code)
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			emptyContent := "{}\n"
			if a.configFormat == "toml" {
				emptyContent = ""
			}
			if _, err := e.Write(path, []byte(emptyContent)); err != nil {
				ctx.Error(fmt.Sprintf("cannot create config file: %s", err))
				return Exit(1)
			}
		}
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		// LAUNCHING AN EDITOR IS A MUTATION. Routed through the handle, a dry
		// run records `run: <editor> <path>` and never opens anything; a bare
		// exec.Command here would open the user's editor during a run that
		// announced it would change nothing.
		//
		// Check(true) is the default and is what keeps the preview walking: a
		// failed operation is an error, not a value (§2.5.4), so nothing here
		// ever reads an exit code off a carrier.
		if _, err := e.Run([]interface{}{editor, path}, Stream(true)); err != nil {
			ctx.Error(fmt.Sprintf("editor failed: %s", err))
			return Exit(1)
		}
		return Exit(0)
	}, WithInteractive())

	// config init
	registerFrameworkSubcommand(grp, "init", "Create a starter config file listing every flag and config field the application declares, each commented with its help text, type and default value, so the file documents itself. The format follows whichever of TOML or JSON the application was built for. Refuses with an error if a config file already exists rather than overwriting it; the created path is printed on success.", EffectMutating, func(ctx *Context, args map[string]interface{}) Outcome {
		path := configPath(a.Name, a.configPathOverride, a.configFormat)
		if _, err := os.Stat(path); err == nil {
			ctx.Error(fmt.Sprintf("config init: config file already exists: %s", path))
			return Exit(1)
		}
		e := ctx.Effects()
		if code := ensureConfigDir(ctx, path); code != 0 {
			return Exit(code)
		}

		allFlags := a.collectAllFlags()

		content := a.generateJSONTemplate(allFlags)
		if a.configFormat == "toml" {
			content = a.generateTomlTemplate(allFlags)
		}
		if _, err := e.Write(path, []byte(content)); err != nil {
			ctx.Error(fmt.Sprintf("cannot write config file: %s", err))
			return Exit(1)
		}
		ctx.Info(path)
		return Exit(0)
	})
}

// validateBoundConfigFields validates that bound config fields for a command
// are present and have correct types in the config data.
// Returns an error message or empty string on success.
func (a *App) validateBoundConfigFields(cmd *Command, configData map[string]interface{}) string {
	for _, fieldName := range cmd.configFields {
		cf, ok := a.configFields[fieldName]
		if !ok {
			// Should not happen — validated by validateConfigFieldBindings
			continue
		}
		val, exists := nestedGet(configData, fieldName)
		if !exists {
			if cf.Required {
				return fmt.Sprintf("required config field \"%s\" is missing from config file", fieldName)
			}
			continue
		}
		// Validate type
		if _, errStr := coerceConfigScalar(val, cf.Type, true); errStr != "" {
			return fmt.Sprintf("config field \"%s\": %s", fieldName, errStr)
		}
	}
	return ""
}

// validateUnknownConfigKeys validates that all keys in the config file are known
// (match a flag, config field, or framework field).
// Returns an error message or empty string on success.
func (a *App) validateUnknownConfigKeys(configData map[string]interface{}) string {
	if len(configData) == 0 {
		return ""
	}
	// Build set of all known keys: flags (using param names), config fields, framework fields
	knownKeys := make(map[string]bool)
	allFlags := a.collectAllFlags()
	for _, f := range allFlags {
		knownKeys[flagParamName(f.Name)] = true
	}
	for name := range a.configFields {
		knownKeys[name] = true
	}
	if a.frameworkFields != nil {
		for name := range a.frameworkFields {
			knownKeys[name] = true
		}
	}
	for _, key := range collectNestedKeys(configData, "") {
		if !knownKeys[key] {
			return fmt.Sprintf("unknown key \"%s\" in config file", key)
		}
	}
	return ""
}

// generateTomlTemplate generates a TOML template config file with comments.
// Config fields with dot-names are organized into TOML sections.
// Required fields are left empty, optional fields get their defaults.
func (a *App) generateTomlTemplate(allFlags []Flag) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Configuration for %s\n\n", a.Name))

	// A config field colliding with a flag's param name is validation-only: it
	// annotates the flag and the key renders once (on the flag).
	colliding := a.collidingConfigFields()

	// Write flags as top-level keys
	for _, f := range allFlags {
		param := flagParamName(f.Name)
		comment := fmt.Sprintf("# %s (type: %s)", f.Help, flagTypeName[f.Type])
		if cf, isColliding := colliding[param]; isColliding {
			comment += fmt.Sprintf(" -- %s", cf.Help)
		}
		sb.WriteString(comment + "\n")
		if f.presence == presenceDefault {
			sb.WriteString(fmt.Sprintf("%s = %s\n", param, formatTomlValue(f.Default)))
		} else {
			sb.WriteString(fmt.Sprintf("# %s = \n", param))
		}
		sb.WriteString("\n")
	}

	// Write config fields, grouping dot-names into TOML sections
	type sectionEntry struct {
		key string
		cf  *ConfigField
	}
	sections := make(map[string][]sectionEntry) // section -> entries
	var topLevel []*ConfigField                 // non-dotted fields
	var sectionOrder []string

	for _, name := range a.configFieldOrder {
		if _, isColliding := colliding[name]; isColliding {
			continue // rendered once on the flag line above
		}
		cf := a.configFields[name]
		if idx := strings.LastIndex(name, "."); idx != -1 {
			section := name[:idx]
			key := name[idx+1:]
			if _, ok := sections[section]; !ok {
				sectionOrder = append(sectionOrder, section)
			}
			sections[section] = append(sections[section], sectionEntry{key: key, cf: cf})
		} else {
			topLevel = append(topLevel, cf)
		}
	}

	// Write non-dotted config fields
	for _, cf := range topLevel {
		sb.WriteString(fmt.Sprintf("# %s (type: %s)\n", cf.Help, flagTypeName[cf.Type]))
		if cf.HasDefault && cf.Default != nil {
			sb.WriteString(fmt.Sprintf("%s = %s\n", cf.Name, formatTomlValue(cf.Default)))
		} else if cf.Required {
			sb.WriteString(fmt.Sprintf("# %s =  # REQUIRED\n", cf.Name))
		} else {
			sb.WriteString(fmt.Sprintf("# %s = \n", cf.Name))
		}
		sb.WriteString("\n")
	}

	// Write sectioned config fields
	for _, section := range sectionOrder {
		entries := sections[section]
		sb.WriteString(fmt.Sprintf("[%s]\n", section))
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("# %s (type: %s)\n", e.cf.Help, flagTypeName[e.cf.Type]))
			if e.cf.HasDefault && e.cf.Default != nil {
				sb.WriteString(fmt.Sprintf("%s = %s\n", e.key, formatTomlValue(e.cf.Default)))
			} else if e.cf.Required {
				sb.WriteString(fmt.Sprintf("# %s =  # REQUIRED\n", e.key))
			} else {
				sb.WriteString(fmt.Sprintf("# %s = \n", e.key))
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// generateJSONTemplate generates a JSON template config file.
// Config fields with dot-names are nested into objects.
// Required fields are left empty (null), optional fields get their defaults.
func (a *App) generateJSONTemplate(allFlags []Flag) string {
	result := make(map[string]interface{})

	// A config field colliding with a flag's param name is validation-only; the
	// flag owns the rendered value, so the key appears once.
	colliding := a.collidingConfigFields()

	// Add flags
	for _, f := range allFlags {
		param := flagParamName(f.Name)
		if f.presence == presenceDefault {
			result[param] = f.Default
		} else {
			result[param] = nil
		}
	}

	// Add config fields, nesting dot-names into objects via nestedSet. Skip
	// colliding fields (rendered once via the flag above).
	for _, name := range a.configFieldOrder {
		if _, isColliding := colliding[name]; isColliding {
			continue
		}
		cf := a.configFields[name]
		if cf.HasDefault && cf.Default != nil {
			nestedSet(result, name, cf.Default)
		} else {
			nestedSet(result, name, nil)
		}
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(data) + "\n"
}

// formatTomlValue formats a Go value as a TOML value string.
func formatTomlValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", val)
	case float64:
		return formatFloatCanonical(val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// formatConfigValue formats a value for config show output.
func formatConfigValue(v interface{}) string {
	if v == nil {
		return "<nil>"
	}
	switch val := v.(type) {
	case bool:
		if val {
			return "true"
		}
		return "false"
	case []interface{}:
		parts := make([]string, len(val))
		for i, v := range val {
			b, _ := json.Marshal(v)
			parts[i] = string(b)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case string:
		return val
	case float64:
		return formatFloatCanonical(val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// configShowPayloadSchema is config show's machine payload contract (contract
// §19.5): one object keyed by flag/config-field name, plus the
// "__infrastructure__" entry. The keys are dynamic, so the declaration names
// the container only. Framework-owned literal, byte-identical across the three
// implementations.
var configShowPayloadSchema = map[string]interface{}{"type": "object"}
