// Package strictcli: A CLI framework for the Era of Agents: nothing is inferred, everything is declared. First-class support for Go, Python, and TypeScript.
//
// This is the Go implementation of strictcli. The Python and TypeScript
// implementations are held to identical behavior -- same semantics, same help
// bytes, same schema, same error sentences -- by one shared conformance suite.
package strictcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// FlagType represents the type of a flag value.
// Scalar types: TypeStr, TypeBool, TypeInt, TypeFloat.
// Compound types encode the item/value type in the upper bits:
// list types = 0x100 | scalar, dict types = 0x200 | scalar.
type FlagType int

const (
	TypeStr   FlagType = iota
	TypeBool  FlagType = iota
	TypeInt   FlagType = iota
	TypeFloat FlagType = iota

	// TypeChoice is a SELECTOR: a flag whose value elects exactly one of its
	// declared choices, each of which owns a scope of flags legal only while
	// that choice is elected (contract §24). It is not a scalar and carries no
	// value shape of its own.
	TypeChoice FlagType = iota

	// Compound type bit flags
	listBit FlagType = 0x100
	dictBit FlagType = 0x200

	// List types: a repeatable flag whose items are coerced to the element type.
	TypeListStr   FlagType = listBit | TypeStr
	TypeListInt   FlagType = listBit | TypeInt
	TypeListFloat FlagType = listBit | TypeFloat

	// Dict types: a repeatable key=value flag whose values are coerced to the value type.
	TypeDictStr   FlagType = listBit | dictBit | TypeStr
	TypeDictInt   FlagType = listBit | dictBit | TypeInt
	TypeDictFloat FlagType = listBit | dictBit | TypeFloat
)

// IsScalarType returns true for the four primitive types.
func IsScalarType(t FlagType) bool {
	return t == TypeStr || t == TypeBool || t == TypeInt || t == TypeFloat
}

// IsListType returns true for list compound types.
func IsListType(t FlagType) bool {
	return t&listBit != 0 && t&dictBit == 0
}

// IsDictType returns true for dict compound types.
func IsDictType(t FlagType) bool {
	return t&dictBit != 0
}

// IsCompoundType returns true for any compound type (list or dict).
func IsCompoundType(t FlagType) bool {
	return IsListType(t) || IsDictType(t)
}

// ItemType returns the scalar element type for a compound type.
// For scalar types, returns the type itself.
func ItemType(t FlagType) FlagType {
	return t & 0x0F
}

// ListOf creates a list type from a scalar item type.
// Panics if the item type is not one of TypeStr, TypeInt, TypeFloat.
func ListOf(itemType FlagType) FlagType {
	switch itemType {
	case TypeStr, TypeInt, TypeFloat:
		return listBit | itemType
	default:
		panic(errListOfBadItemType(itemType))
	}
}

// DictOf creates a dict type from a scalar value type.
// Panics if the value type is not one of TypeStr, TypeInt, TypeFloat.
func DictOf(valueType FlagType) FlagType {
	switch valueType {
	case TypeStr, TypeInt, TypeFloat:
		return listBit | dictBit | valueType
	default:
		panic(errDictOfBadValueType(valueType))
	}
}

// presenceKind is the resolved presence declaration of a flag or a positional
// arg (contract §23). Every flag and every arg declares EXACTLY ONE of the
// three, at registration, through the sibling options Required()/Optional()/
// Default(v) (ArgRequired()/ArgOptional()/ArgDefault(v) for args). Nothing is
// inferred from the shape of another declaration.
type presenceKind uint8

const (
	presenceUndeclared presenceKind = iota
	presenceRequired
	presenceOptional
	presenceDefault
)

// Presence declaration bits. They are accumulated by the three sibling options
// so that declaring two can be named in the error, and so that a struct literal
// -- which passes through no option at all -- declares nothing and does not
// register.
const (
	presenceBitRequired uint8 = 1 << iota
	presenceBitOptional
	presenceBitDefault
)

// Flag represents a --flag declaration.
type Flag struct {
	Name         string
	Type         FlagType
	Help         string
	Short        string
	Default      interface{} // the declared default; meaningful only with Default(v)
	Env          string
	Prefixed     bool
	Negatable    bool
	Choices      []interface{}
	Validate     func(interface{}) error
	Repeatable   bool
	Unique       bool
	EnvSeparator string

	// Nullable declares that this property of an update command can be CLEARED
	// (contract §27.6), which mints the framework-owned `--unset-<name>`. It is
	// legal only on a property of an update: nullable on anything else is a
	// registration-time hard error. An unset property delivers absence and
	// reports Provided() true; ctx.Unset(name) is what tells the two apart.
	Nullable bool

	// ConnectionURL marks this flag as a connection-URL (URL-class) flag: a flag
	// whose value is a connection URL (e.g. a database DSN). A URL-class flag MUST
	// bind to a declared connection env (ConnectionEnv), enforced at registration.
	ConnectionURL bool
	// ConnectionEnv is the name of the app-level connection env (declared via
	// WithConnectionEnv) that this flag binds to. Binding is hermetic-suppressed,
	// lazily read, and has no default; the CLI token still wins over the env
	// (source "cli" vs "env"). Only valid on URL-class flags; the referenced
	// connection env must be declared.
	ConnectionEnv string

	// ConflictMode is the per-flag override of the app config conflict mode.
	// Empty string (with hasConflictMode false) means "inherit the app default".
	// When set, must be "cli-wins" or "error". Applies to flags only:
	// standalone ConfigFields have no CLI/env conflict surface, and a
	// flag-colliding ConfigField inherits the flag's handling.
	ConflictMode string

	// choiceRecords carries the per-entry help of a value flag's Choices, in
	// declaration order and index-aligned with Choices. Help rendering reads it;
	// the parser reads Choices (contract §24.2).
	choiceRecords []ChoiceValue

	// retiredChoices are the spellings this flag used to accept, in
	// declaration order. A retired value is refused at parse time with its own
	// message; it is not a choice, so help and the published value_schema
	// never name it. Unexported: RetiredChoices is the only mint, which keeps
	// a Flag struct literal from declaring one half of the construct.
	retiredChoices []RetiredChoiceValue

	// choiceDecls is a SELECTOR's declared choices, in declaration order. It is
	// unexported on purpose: a Flag struct literal cannot be a selector at all,
	// which is §23.2's presenceBits guarantee extended to the new construct
	// (contract §24.12).
	choiceDecls []*ChoiceDecl
	// memberSpelled records that the selector was declared with
	// MemberChoiceFlag: its choices are elected by their own flags and no
	// selector token is ever typed (contract §24.4).
	memberSpelled bool

	// hasDefault records that Default(v) was applied. It is internal
	// bookkeeping for the declared value only: requiredness comes from
	// `presence`, never from this field (contract §23.4).
	hasDefault bool
	// presence is the resolved declaration; presenceBits records which of the
	// three sibling options were applied, so zero and two-or-more are both
	// nameable registration errors (§12.12).
	presence        presenceKind
	presenceBits    uint8
	hasUnique       bool
	hasConflictMode bool
}

// Arg represents a positional argument.
type Arg struct {
	Name       string
	Help       string
	Default    interface{}
	IsVariadic bool
	Type       FlagType
	Choices    []interface{}

	choiceRecords []ChoiceValue
	// retiredChoices: the arg twin of Flag.retiredChoices.
	retiredChoices []RetiredChoiceValue
	hasDefault     bool
	presence       presenceKind
	presenceBits   uint8
}

// FlagSet is a reusable bundle of flags.
type FlagSet struct {
	Name  string
	Flags []Flag
}

// The constraint declarations -- AtLeastOne, AllOrNone, Requires and Implies --
// are constructors over an unexported type and live in constraints.go
// (contract §26.6). `CoRequired` is deleted: all-or-none absorbs it by rename,
// with no alias and no deprecation period.

// InfraRootPath is an opaque marker produced by RelativeToRoot. It represents a
// filesystem path built from a declared infrastructure root (identified by its
// env var name) joined with zero or more path parts. Config-path markers resolve
// eagerly at construction; flag-default markers resolve when defaults are applied
// at parse time. A marker referencing an undeclared root is a registration-time
// hard error.
type InfraRootPath struct {
	envVar string
	parts  []string
}

// RelativeToRoot returns a marker representing a path relative to a declared
// infrastructure root. envVar names the root (declared via WithInfraRoot); parts
// are joined onto the resolved root path. Accepted by flag Default(...) and
// WithConfigPathRelativeToRoot.
func RelativeToRoot(envVar string, parts ...string) InfraRootPath {
	return InfraRootPath{envVar: envVar, parts: append([]string{}, parts...)}
}

// String renders the marker as the declaration that produced it, which is the
// form Python (repr) and TypeScript (toString) both print -- including the
// separator after the env var when there are no parts at all. Without it, fmt
// rendered the struct ("{MYAPP_ROOT [store]}") and Go's help output leaked its
// internal shape where the siblings printed the declaration (contract §23.8).
func (p InfraRootPath) String() string {
	quoted := make([]string, len(p.parts))
	for i, part := range p.parts {
		quoted[i] = pyStrRepr(part)
	}
	return "RelativeToRoot(" + pyStrRepr(p.envVar) + ", " + strings.Join(quoted, ", ") + ")"
}

// pyStrRepr renders a string the way Python's repr() does for the printable
// values a declaration carries: single quotes, switching to double quotes only
// when the value contains a single quote and no double quote, with backslashes
// (and single quotes inside single quotes) escaped.
func pyStrRepr(s string) string {
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
	}
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, "'", `\'`)
	return "'" + escaped + "'"
}

// infraRootDecl is a raw WithInfraRoot declaration, collected during the options
// loop and resolved eagerly after the loop completes in NewApp.
type infraRootDecl struct {
	envVar      string
	defaultPath string
}

// PassthroughHandler is the handler type for passthrough commands.
type PassthroughHandler func(ctx *Context, name string, args []string, globals map[string]interface{}) int

// Command is a leaf command with a handler.
type Command struct {
	Name    string
	Help    string
	Handler func(ctx *Context, kwargs map[string]interface{}) Outcome
	// Effect is the mandatory classification: EffectReadOnly or EffectMutating.
	// There is no default -- a command registered without WithEffect is a
	// registration-time hard error.
	Effect string
	// Consequential is declared per-command (contract §8.1) and is NOT
	// mandatory -- absence means "not consequential". It is a property of the
	// COMMAND, deliberately not named after the framework's reaction to it, so
	// other behaviours can hang off it later. Today the framework prompts for
	// exactly these commands.
	Consequential bool
	// DryRunSupported is declared per-command and defaults to true (the
	// regime's baseline: a mutating command records rather than executes). A
	// command that sets it false is saying a preview of it would LIE -- its
	// effects escape the effects handle, or its later steps read state the
	// recorded ones would have written -- so the framework refuses --dry-run
	// for it at parse time instead of rendering a preview nobody can trust.
	// Set only through WithDryRunUnsupported, which carries the mandatory
	// reason shown in help and in the refusal.
	DryRunSupported         bool
	DryRunUnsupportedReason string
	// PayloadSchema is the command's machine payload contract (contract
	// §19.5): an inline JSON Schema literal, registered as written. A nil
	// schema means the command cannot produce a payload -- ctx.Payload is
	// then a call-time hard error. The literal is validated at registration
	// over the closed subset, and the value ctx.Payload supplies is
	// validated against it at emission.
	PayloadSchema map[string]interface{}
	// OwnsStdout declares that this command's stdout IS the artifact
	// (contract §19.6) -- a SQL dump, an SVG, a hash-verified JSON document.
	// In machine mode the envelope moves to stderr so the artifact's bytes are
	// untouched. Outside machine mode the declaration changes nothing at all.
	OwnsStdout bool
	Grants     []Grant
	Forwarding *Forwarding
	// updateOf is the command's update declaration (contract §27.2): the
	// resource it changes, the write mode, and the names of the declarations
	// that identify the instance and the ones that carry what changes. nil on
	// every command that declares no update. Declared through WithUpdateOf,
	// which is the only spelling -- a half-declared record (a write mode
	// without an update, or the reverse) is unrepresentable rather than
	// guarded.
	updateOf    *updateDecl
	flags       []Flag
	args        []Arg
	flagSets    []FlagSet
	constraints []constraintDecl
	// index is the command's whole declaration tree flattened by name, built
	// once at registration (contract §24). It is what makes tokenization,
	// election, scope validation and help rendering see every depth.
	index              *flagIndex
	tags               []string
	configFields       []string // bound config field names
	Passthrough        bool
	PassthroughHandler PassthroughHandler
	Hidden             bool
	Interactive        bool
	// effectSet records that WithEffect was applied, so a deliberate
	// WithEffect("") is told apart from an absent declaration.
	effectSet bool
	// frameworkInternal is a private marker, set ONLY by strictcli's own
	// registration paths. It is not reachable from any public option, and is
	// not emitted in the schema.
	frameworkInternal bool
}

// deprecatedCmd is a declaration-only command that prints a message and exits 1.
type deprecatedCmd struct {
	Name    string
	Message string
}

// Group is a container for nested commands and subgroups (arbitrary depth).
type Group struct {
	Name            string
	Help            string
	Commands        map[string]*Command
	Groups          map[string]*Group
	tags            []string
	accumulatedTags []string // own tags union all ancestor tags; passed as inheritedTags to children
	envPrefix       string

	// app is a reference to the root App (needed for collision/infra validation)
	app *App

	// globalFlags is a reference to the app's global flags for collision checking
	globalFlags []Flag

	// order preserves insertion order for help display
	order      []string
	groupOrder []string

	deprecated    []deprecatedCmd
	deprecatedMap map[string]string

	Hidden bool
}

// Result is returned by App.Test().
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Data     interface{}
}

// App is the root CLI application.
type App struct {
	Name      string
	Version   string
	Help      string
	EnvPrefix string

	commands    map[string]*Command
	groups      map[string]*Group
	globalFlags []Flag

	// order preserves insertion order for help display
	cmdOrder   []string
	groupOrder []string

	deprecated    []deprecatedCmd
	deprecatedMap map[string]string

	configEnabled       bool
	configPathOverride  string
	configFormat        string
	configData          map[string]interface{}
	configFields        map[string]*ConfigField
	configFieldOrder    []string
	frameworkFields     map[string]*ConfigField
	frameworkFieldOrder []string
	noDefaultConfigPath bool

	checksEnabled       bool
	checksPath          string
	checksEmbed         []byte
	checkDefs           map[string]*checkDef
	checkOrder          []string // sorted check names for deterministic listing
	checkContextFactory func() CheckContext

	// Check-provider hook state. Providers populate the registry lazily at the
	// first registry read (materialization), memoized per cwd. See
	// check_provider.go for the mechanics.
	checkProviders          []func() []CheckSpec
	providerMaterialized    bool            // true once providers ran for providerMaterializedCwd
	providerMaterializedCwd string          // os.Getwd() at last materialization
	providerSourcedNames    map[string]bool // def names added by providers (dropped on re-materialization)

	stdinConsumedBy *string           // tracks which flag consumed stdin via @-
	tagContracts    map[string]string // tag name -> required flag name

	// configParseErr stores a config parse error for config show to pick up.
	// Set when a config subcommand is routed and the config file was malformed.
	configParseErr string

	// configConflictMode controls whether config+cli and config+env overlaps
	// are hard errors. Valid values: "cli-wins" (default), "error".
	configConflictMode string

	// Infrastructure env vars (location roots + handshake signals).
	// infraRootDecls holds raw WithInfraRoot declarations; they are resolved
	// eagerly in NewApp (post-options) into infraRoots. Resolution never
	// consults the hermetic flag: infra vars have no argv dependency, which is
	// WHY eager construction-time resolution is sound (and hermetic-immune).
	infraRootDecls   []infraRootDecl
	infraRoots       map[string]string // env var -> resolved absolute path
	infraRootOrder   []string          // env var names in declaration order
	infraRootFromEnv map[string]bool   // env var -> value came from the env var (vs default)
	configPathRef    *InfraRootPath    // set by WithConfigPathRelativeToRoot
	schemaPath       string            // set by WithSchemaPath
	schemaPathRef    *InfraRootPath    // set by WithSchemaPathRelativeToRoot
	schemaOutPath    string            // resolved absolute --dump-schema target

	// Handshake env vars: cross-tool protocol signals. No default, no eager
	// resolution -- read live via os.LookupEnv at access time.
	handshakeEnvs  map[string]string // env var -> help
	handshakeOrder []string          // env var names in declaration order

	// Connection env vars: behavioral "reach outside the process" signals such
	// as a database/service URL. Declared once at app level (name + help), read
	// lazily, with NO default. Unlike roots and handshakes, connection envs are
	// hermetic-SUPPRESSED: under --hermetic they resolve as absent so that
	// connection-dependent behavior (including checks) skips visibly instead of
	// connecting. Flags bind to a declared connection env by reference.
	connectionEnvs  map[string]string // env var -> help
	connectionOrder []string          // env var names in declaration order

	// Test-coverage instrumentation. When the declared directory exists, every
	// Test() and Call() invocation records the resolved command path to
	// per-process shard files so a check can verify that every command in the
	// surface has been exercised. The three derived paths stay empty when the
	// declared directory is absent, which is what turns the whole mechanism off.
	testCoverageDir      string // the declared directory, as given
	coverageShardPath    string // "<declared>/coverage/<pid>.jsonl"
	coverageDir          string // "<declared>/coverage"
	coverageManifestPath string // "<declared>/test-coverage.json"

	// The effects regime. procObserveAllowlist is app-level observe
	// authorization: a list of argv PREFIXES, matched element-wise by string
	// equality. A ctx.Effects().Run whose argv matches one is an observe: it
	// executes even in dry mode, returns a real value, and is never written to
	// the would-do log.
	procObserveAllowlist [][]string

	// effects is the structured effect log for the most recent dispatch.
	// Populated in BOTH modes: recorded entries in dry mode, executed entries
	// (with recorded: false) in live mode, plus framework-blessed CACHE_WRITEs.
	effects *effectLog

	// The framework-owned reserved quartet, extracted by the position-aware
	// pre-scan and delivered on the Context (never as handler kwargs).
	lastDryRun               bool
	lastApproveConsequential bool
	lastQuiet                bool
	lastVerbose              bool
	// Machine mode (contract §19.1), delivered on the Context like the
	// quartet: extracted by the pre-scan, never a handler kwarg.
	lastJSON bool

	// exitHook runs immediately before Run's terminal os.Exit. Test-only
	// surface; see SetExitHook.
	exitHook func()

	// confirmIO overrides the stdin side of the confirm protocol. Test-only
	// surface; see SetConfirmIO. nil means the real stdin.
	confirmIO *ConfirmIO
}

// SetExitHook registers a function to run immediately before Run's terminal
// os.Exit, after the handler has returned and the would-do log has been
// written.
//
// Test-only surface, beside Test() and EffectLog(). Go has no stdlib at-exit
// facility and Run ends in os.Exit, so a caller that needs to read a
// post-dispatch diagnostic -- EffectLog() above all -- has no other seam. It
// gives Go what Python's atexit and Node's process.on("exit") give their
// harnesses for free. It changes no behavior: the hook runs after every
// observable output the run produces, and its own errors are the caller's.
func (a *App) SetExitHook(fn func()) {
	a.exitHook = fn
}

// runExitHook invokes the registered exit hook, if any. Called immediately
// before Run's terminal os.Exit calls.
func (a *App) runExitHook() {
	if a.exitHook != nil {
		a.exitHook()
	}
}

// --- Option types ---

// AppOption configures an App.
type AppOption func(*App)

// WithEnvPrefix sets the environment variable prefix for the app.
func WithEnvPrefix(prefix string) AppOption {
	return func(a *App) {
		a.EnvPrefix = prefix
	}
}

// WithConfig enables config file support.
func WithConfig() AppOption {
	return func(a *App) {
		a.configEnabled = true
	}
}

// WithConfigPath overrides the default config file path.
func WithConfigPath(path string) AppOption {
	return func(a *App) {
		a.configPathOverride = path
	}
}

// WithConfigFormat sets the config file format ("json" or "toml").
func WithConfigFormat(format string) AppOption {
	return func(a *App) {
		a.configFormat = format
	}
}

// WithNoDefaultConfigPath makes the app load NO config file unless
// --config is explicitly passed on the command line. Without this
// option (the default), the app loads from the XDG default path.
func WithNoDefaultConfigPath() AppOption {
	return func(a *App) {
		a.noDefaultConfigPath = true
	}
}

// WithConfigConflictMode sets the conflict resolution mode for config values.
// Valid values: "cli-wins" (default) and "error".
// In "error" mode, a flag set by both config AND cli (or config AND env)
// is a hard error. Implied sources are excluded from conflict checks.
func WithConfigConflictMode(mode string) AppOption {
	return func(a *App) {
		if mode != "cli-wins" && mode != "error" {
			panic(errWithConfigConflictModeBadMode(mode))
		}
		a.configConflictMode = mode
	}
}

// WithInfraRoot declares an infrastructure location root: an env var that, when
// set, overrides defaultPath as the base directory for the tool's data. Multiple
// roots are allowed (keyed by env var name). Roots are resolved eagerly at
// construction and are immune to --hermetic (hermetic suppresses config and
// behavioral env, never location). A leading ~ in the value or default is
// expanded to the user's home directory.
func WithInfraRoot(envVar, defaultPath string) AppOption {
	return func(a *App) {
		a.infraRootDecls = append(a.infraRootDecls, infraRootDecl{envVar: envVar, defaultPath: defaultPath})
	}
}

// WithHandshakeEnv declares a handshake env var: a cross-tool protocol signal set
// by the invoking process. It has no default and no resolution semantics beyond
// "read live at access time" via ctx.InfraValue.
func WithHandshakeEnv(envVar, help string) AppOption {
	return func(a *App) {
		if strings.TrimSpace(help) == "" {
			panic(errHandshakeEnvVarEmptyHelp(envVar))
		}
		if a.handshakeEnvs == nil {
			a.handshakeEnvs = make(map[string]string)
		}
		if _, dup := a.handshakeEnvs[envVar]; dup {
			panic(errDuplicateHandshakeEnvVar(envVar))
		}
		a.handshakeEnvs[envVar] = help
		a.handshakeOrder = append(a.handshakeOrder, envVar)
	}
}

// WithConnectionEnv declares a connection env var: a behavioral "reach outside
// the process" signal such as a database or service connection URL. It is
// declared once at app level (name + help) and surfaced in --help and the
// schema dump alongside the other infrastructure env vars. Unlike roots and
// handshakes it is hermetic-SUPPRESSED: under --hermetic it resolves as absent.
// It has no default and is read lazily. Flags bind to it by reference via
// ConnectionURLFlag. A connection env must not collide with a declared root or
// handshake var, and duplicate declarations are a hard error.
func WithConnectionEnv(envVar, help string) AppOption {
	return func(a *App) {
		if strings.TrimSpace(help) == "" {
			panic(errConnectionEnvVarEmptyHelp(envVar))
		}
		if a.connectionEnvs == nil {
			a.connectionEnvs = make(map[string]string)
		}
		if _, dup := a.connectionEnvs[envVar]; dup {
			panic(errDuplicateConnectionEnvVar(envVar))
		}
		a.connectionEnvs[envVar] = help
		a.connectionOrder = append(a.connectionOrder, envVar)
	}
}

// WithConfigPathRelativeToRoot overrides the config file path with a location
// relative to a declared infrastructure root. The marker is resolved eagerly at
// construction into the absolute config path override.
func WithConfigPathRelativeToRoot(envVar string, parts ...string) AppOption {
	return func(a *App) {
		ref := RelativeToRoot(envVar, parts...)
		a.configPathRef = &ref
	}
}

// WithSchemaPath declares where --dump-schema writes. The path may be absolute
// or relative to the App's construction-time working directory. Undeclared, the
// framework's own location applies: ".strictcli/schema.json" ANCHORED at the
// construction-time working directory, so a chdir between construction and
// dispatch cannot redirect the write into the caller's cwd.
func WithSchemaPath(path string) AppOption {
	return func(a *App) {
		a.schemaPath = path
	}
}

// WithSchemaPathRelativeToRoot declares the --dump-schema target as a location
// relative to a declared infrastructure root. The marker is resolved eagerly at
// construction, exactly as WithConfigPathRelativeToRoot is.
func WithSchemaPathRelativeToRoot(envVar string, parts ...string) AppOption {
	return func(a *App) {
		ref := RelativeToRoot(envVar, parts...)
		a.schemaPathRef = &ref
	}
}

// WithChecks enables the check system with an explicit path to checks.toml.
func WithChecks(path string) AppOption {
	return func(a *App) {
		a.checksPath = path
	}
}

// WithChecksEmbed enables the check system with inline TOML data (e.g., from //go:embed).
func WithChecksEmbed(data []byte) AppOption {
	return func(a *App) {
		a.checksEmbed = data
	}
}

// WithProcObserveAllowlist declares app-level observe authorization: a list of
// argv PREFIXES, matched element-wise against the leading elements of an
// effect's argv by string equality. A ctx.Effects().Run whose argv matches any
// listed prefix is an observe -- it executes even in dry mode, returns a real
// value, and is never written to the would-do log.
func WithProcObserveAllowlist(prefixes [][]string) AppOption {
	return func(a *App) {
		for _, prefix := range prefixes {
			if len(prefix) == 0 {
				panic(errProcObserveAllowlistEmptyPrefix)
			}
			a.procObserveAllowlist = append(a.procObserveAllowlist, append([]string{}, prefix...))
		}
	}
}

// WithTestCoverageDir declares the directory holding this app's coverage
// state: coverage/ (per-process shard files) and test-coverage.json (the
// committed manifest). Every Test() and Call() invocation records the resolved
// command path to the process's shard file (<dir>/coverage/<pid>.jsonl), whose
// directory is created on the first such record and never at construction -- a
// plain CLI run writes no shard and leaves no directory. A built-in
// cli-test-coverage check (auto-registered via the provider mechanism) merges
// shards and hard-FAILs listing every command with zero coverage.
//
// The directory decides everything. Undeclared means coverage is off. A
// declared directory that does not exist at construction also means off -- no
// check registered, no paths computed, nothing created. That is the installed
// distribution, whose declared path names a source checkout that is not there.
func WithTestCoverageDir(path string) AppOption {
	return func(a *App) {
		a.testCoverageDir = path
	}
}

// WithTestCoverage is the retired boolean option. It has no accepted spelling:
// applying it is a registration-time panic naming the option that replaced it.
func WithTestCoverage() AppOption {
	return func(a *App) {
		panic(errTestCoverageBooleanRetired)
	}
}

// FlagOption configures a Flag.
//
// It is an INTERFACE rather than a func type (contract §24.12, ruling S12): a
// func cannot also carry a choice's name, help, scope and IDENTITY, and identity
// is what lets a handler switch on a choice by reference instead of by string.
// Every constructor's signature is unchanged -- Required() still returns
// FlagOption and still compiles at every call site. The one caller shape that
// breaks is a hand-written strictcli.FlagOption func literal, which can reach
// only Flag's exported fields, cannot declare presence, and therefore already
// fails registration today (§23.2).
type FlagOption interface {
	applyFlag(*Flag)
}

// flagOptFunc adapts a plain func(*Flag) to the FlagOption interface. It is the
// carrier every option constructor below returns.
type flagOptFunc func(*Flag)

func (fn flagOptFunc) applyFlag(f *Flag) { fn(f) }

// Short sets the single-character short form for a flag.
func Short(s string) FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.Short = s
	})
}

// Default declares the flag's presence as "a default value the framework
// supplies when nothing else does" (contract §23.1). It is one of the three
// sibling presence options -- Required(), Optional(), Default(v) -- of which
// EXACTLY ONE must be applied to every flag.
//
// Default(nil) is a registration error: a null-valued default is not a spelling
// of optionality. Use Optional(), which delivers nil when the flag is absent.
func Default(v interface{}) FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.Default = v
		f.hasDefault = true
		f.presenceBits |= presenceBitDefault
	})
}

// Required declares that a value must be supplied for this flag, from any
// source (CLI, env, config, or an implication). One of the three sibling
// presence options; see Default.
func Required() FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.presenceBits |= presenceBitRequired
	})
}

// Optional declares that absence is legal and is delivered AS absence: the
// handler receives nil. One of the three sibling presence options; see Default.
func Optional() FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.presenceBits |= presenceBitOptional
	})
}

// Env sets the environment variable name for a flag.
func Env(varName string) FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.Env = varName
	})
}

// Prefixed controls whether env var prefix validation is applied.
func Prefixed(b bool) FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.Prefixed = b
	})
}

// ChoiceValue is one entry of a value flag's (or positional arg's) choices list:
// a value plus its OPTIONAL help. It is minted only by Ch -- a
// ChoiceValue struct literal declares a bare value and is refused at
// registration (contract §24.2: an entry that may carry help and an entry that
// carries none would otherwise be two spellings of one fact).
type ChoiceValue struct {
	// Value is the allowed value, of the flag's or arg's declared type.
	Value interface{}
	// Help is the entry's help text. Empty means "no help", which is the one
	// place in the framework an empty help string is legal: Go has no optional
	// parameters, and an empty help string is refused everywhere it is
	// mandatory, so the spelling has no second meaning to destroy (§24.12).
	Help string

	// viaCh records that this record came from Ch(). A struct literal reaches
	// the constructors with it false and is refused, the same guarantee
	// presenceBits gives the presence declaration (§23.2).
	viaCh bool
}

// Ch declares one choices entry: a value and its help. Ch(v, "") is the
// no-help spelling.
func Ch(value interface{}, help string) ChoiceValue {
	return ChoiceValue{Value: value, Help: help, viaCh: true}
}

// choiceValuesToRecords validates a choices list and splits it into the plain
// value list the parser and schema use and the record list help rendering uses.
// name is the flag or arg name for the error text, and notRecord is the
// surface's own bare-value template: the two surfaces have twin messages, so an
// arg reports itself as an arg (§12.13, §18.19 item 219).
func choiceValuesToRecords(name string, vals []ChoiceValue, notRecord func(string, int) string) ([]interface{}, []ChoiceValue) {
	values := make([]interface{}, len(vals))
	records := make([]ChoiceValue, len(vals))
	for i, cv := range vals {
		if !cv.viaCh {
			panic(notRecord(name, i))
		}
		values[i] = cv.Value
		records[i] = cv
	}
	if vals == nil {
		values = []interface{}{}
		records = []ChoiceValue{}
	}
	return values, records
}

// checkChoiceMagnitudes runs §12.14's guard over one declaration's resolved
// choice values. surface is "Flag" or "Arg", which is each surface's own
// existing message prefix.
func checkChoiceMagnitudes(surface, name string, values []interface{}) {
	for _, v := range values {
		n, ok := v.(int)
		if !ok {
			continue
		}
		if n > choiceMaxMagnitude || n < -choiceMaxMagnitude {
			if surface == "Arg" {
				panic(errArgChoiceMagnitude(name, n))
			}
			panic(errFlagChoiceMagnitude(name, n))
		}
	}
}

// choiceMaxMagnitude is the largest integer a JSON reader parsing numbers as
// IEEE-754 doubles recovers exactly. It is the payload regime's own guard at a
// second boundary: there a value being written into the envelope, here one
// being written into the schema file.
const choiceMaxMagnitude = 1 << 53

// Choices sets the allowed values for a flag. Every entry is a record built by
// Ch(value, help); the bare-value entry is deleted (contract §24.2).
func Choices(vals ...ChoiceValue) FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.Choices, f.choiceRecords = choiceValuesToRecords(f.Name, vals, errChoicesEntryNotRecord)
	})
}

// RetiredChoiceValue is one entry of a declaration's retired choices: a
// spelling the flag or arg used to accept, plus the message naming what
// replaced it. It is the value-level twin of app.Deprecate's command entry.
//
// Unlike ChoiceValue there is no via-constructor bit: both fields are
// mandatory in the sentence a retired choice produces, so a struct literal has
// no degenerate form to refuse. A literal that omits the message is caught by
// the empty-message guard, which is the same refusal under the name that
// describes it.
type RetiredChoiceValue struct {
	// Value is the retired spelling, of the flag's or arg's declared type.
	Value interface{}
	// Message names the replacement. It is mandatory and non-empty, like every
	// other message the framework prints on a declaration's behalf.
	Message string
}

// RetiredChoice declares one retired spelling and the message that names its
// replacement.
func RetiredChoice(value interface{}, message string) RetiredChoiceValue {
	return RetiredChoiceValue{Value: value, Message: message}
}

// RetiredChoices declares the spellings a flag used to accept. A value that
// matches one is refused at parse time with its own message; it is never a
// choice, so it appears in neither help nor the published value_schema.
func RetiredChoices(vals ...RetiredChoiceValue) FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.retiredChoices = append([]RetiredChoiceValue{}, vals...)
	})
}

// validateRetiredChoices runs the registration-time guards shared by the flag
// and arg surfaces. The templates are twinned per surface, so each caller
// passes its own; the rules themselves are one set.
func validateRetiredChoices(
	name string,
	retired []RetiredChoiceValue,
	choices []interface{},
	itemType FlagType,
	hasDefault bool,
	dflt interface{},
	tpl retiredChoiceTemplates,
) {
	if retired == nil {
		return
	}
	// The bool refusal comes first so the declaration is named by what it got
	// wrong: choices are already incompatible with bool, and reporting the
	// missing choices instead would send a reader to add a declaration the
	// framework would then refuse for the same reason.
	if itemType == TypeBool {
		panic(tpl.incompatibleBool(name))
	}
	if choices == nil {
		panic(tpl.requireChoices(name))
	}
	seen := make(map[interface{}]bool, len(retired))
	for _, rc := range retired {
		formatted := formatValueForError(rc.Value)
		// The type check comes first: a value of the wrong type can never
		// match at parse time, so the declaration is dead however it compares
		// against the live set or against its siblings.
		switch itemType {
		case TypeStr:
			if _, ok := rc.Value.(string); !ok {
				panic(tpl.typeMismatch(name, formatted, "str"))
			}
		case TypeInt:
			if _, ok := rc.Value.(int); !ok {
				panic(tpl.typeMismatch(name, formatted, "int"))
			}
		case TypeFloat:
			if _, ok := rc.Value.(float64); !ok {
				panic(tpl.typeMismatch(name, formatted, "float"))
			}
		}
		if strings.TrimSpace(rc.Message) == "" {
			panic(tpl.messageEmpty(name, formatted))
		}
		if inChoices(rc.Value, choices) {
			panic(tpl.isLive(name, formatted))
		}
		if seen[rc.Value] {
			panic(tpl.duplicate(name, formatted))
		}
		seen[rc.Value] = true
	}
	if hasDefault && dflt != nil {
		for _, rc := range retired {
			if rc.Value == dflt {
				panic(tpl.defaultIsRetired(name, formatValueForError(dflt)))
			}
		}
	}
}

// retiredChoiceTemplates carries one surface's half of the twinned message set,
// so validateRetiredChoices states each rule once.
type retiredChoiceTemplates struct {
	isLive           func(string, string) string
	duplicate        func(string, string) string
	messageEmpty     func(string, string) string
	incompatibleBool func(string) string
	defaultIsRetired func(string, string) string
	requireChoices   func(string) string
	typeMismatch     func(string, string, string) string
}

var flagRetiredChoiceTemplates = retiredChoiceTemplates{
	isLive:           errFlagRetiredChoiceIsLive,
	duplicate:        errFlagRetiredChoiceDuplicate,
	messageEmpty:     errFlagRetiredChoiceMessageEmpty,
	incompatibleBool: errFlagRetiredChoicesIncompatibleBool,
	defaultIsRetired: errFlagDefaultIsRetiredChoice,
	requireChoices:   errFlagRetiredChoicesRequireChoices,
	typeMismatch:     errFlagRetiredChoiceTypeMismatch,
}

var argRetiredChoiceTemplates = retiredChoiceTemplates{
	isLive:           errArgRetiredChoiceIsLive,
	duplicate:        errArgRetiredChoiceDuplicate,
	messageEmpty:     errArgRetiredChoiceMessageEmpty,
	incompatibleBool: errArgRetiredChoicesIncompatibleBool,
	defaultIsRetired: errArgDefaultIsRetiredChoice,
	requireChoices:   errArgRetiredChoicesRequireChoices,
	typeMismatch:     errArgRetiredChoiceTypeMismatch,
}

// retiredChoiceMessage returns the message declared for a retired spelling, and
// whether the value is retired at all. Retired lists are short and ordered, so
// the scan mirrors inChoices rather than building a map.
func retiredChoiceMessage(val interface{}, retired []RetiredChoiceValue) (string, bool) {
	for _, rc := range retired {
		if val == rc.Value {
			return rc.Message, true
		}
	}
	return "", false
}

// Repeatable marks a flag as accepting multiple occurrences.
func Repeatable() FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.Repeatable = true
	})
}

// Unique controls whether a repeatable flag rejects duplicate values.
func Unique(b bool) FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.Unique = b
		f.hasUnique = true
	})
}

// ConflictMode sets the per-flag config conflict mode, overriding the app-level
// default (WithConfigConflictMode). Must be "cli-wins" or "error". This applies
// only to flags; standalone ConfigFields have no CLI/env conflict surface, and a
// flag-colliding ConfigField inherits the flag's handling.
func ConflictMode(mode string) FlagOption {
	return flagOptFunc(func(f *Flag) {
		if mode != "cli-wins" && mode != "error" {
			panic(errConflictModeBadMode(mode))
		}
		f.ConflictMode = mode
		f.hasConflictMode = true
	})
}

// ConnectionURLFlag marks a flag as a connection-URL (URL-class) flag bound to
// the declared connection env named envVar (see WithConnectionEnv). The value
// resolves from the CLI token if present, else from the connection env (lazily,
// hermetic-suppressed), with no default. Binding to an env that was not declared
// via WithConnectionEnv is a registration-time hard error, as is marking a flag
// URL-class without a binding.
func ConnectionURLFlag(envVar string) FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.ConnectionURL = true
		f.ConnectionEnv = envVar
	})
}

// EnvSeparator sets the character used to split an env var value into multiple
// values for a repeatable flag (e.g., "," to split "a,b,c" into ["a","b","c"]).
func EnvSeparator(sep string) FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.EnvSeparator = sep
	})
}

// ValidateFn sets a validation function for a flag.
func ValidateFn(fn func(interface{}) error) FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.Validate = fn
	})
}

// Negatable controls whether a bool flag supports --no-X negation.
func NegatableOpt(b bool) FlagOption {
	return flagOptFunc(func(f *Flag) {
		f.Negatable = b
	})
}

// ArgOption configures an Arg.
type ArgOption func(*Arg)

// ArgRequired declares that a value must be supplied for this positional arg.
// One of the three sibling presence options -- ArgRequired(), ArgOptional(),
// ArgDefault(v) -- of which EXACTLY ONE must be applied to every arg.
func ArgRequired() ArgOption {
	return func(a *Arg) {
		a.presenceBits |= presenceBitRequired
	}
}

// ArgOptional declares that absence is legal and is delivered AS absence: the
// arg's kwargs entry is present and holds nil. One of the three sibling
// presence options; see ArgRequired.
func ArgOptional() ArgOption {
	return func(a *Arg) {
		a.presenceBits |= presenceBitOptional
	}
}

// ArgDefault declares a default value the framework supplies when the arg is
// absent. One of the three sibling presence options; see ArgRequired.
//
// ArgDefault(nil) is a registration error (use ArgOptional()), and so is
// ArgDefault on a variadic arg: a variadic always delivers a list, so its empty
// case is ArgOptional().
func ArgDefault(v interface{}) ArgOption {
	return func(a *Arg) {
		a.Default = v
		a.hasDefault = true
		a.presenceBits |= presenceBitDefault
	}
}

// Variadic marks a positional argument as variadic (collects remaining values).
func Variadic() ArgOption {
	return func(a *Arg) {
		a.IsVariadic = true
	}
}

// ArgType sets the type for a positional argument.
func ArgType(t FlagType) ArgOption {
	return func(a *Arg) {
		a.Type = t
	}
}

// ArgChoices sets the allowed values for a positional argument. Entries are the
// same Ch(value, help) records a flag's Choices takes (contract §24.2, §24.7:
// positionals stay command-level, with choices in the record spelling).
func ArgChoices(vals ...ChoiceValue) ArgOption {
	return func(a *Arg) {
		a.Choices, a.choiceRecords = choiceValuesToRecords(a.Name, vals, errArgChoicesEntryNotRecord)
	}
}

// ArgRetiredChoices is the positional-arg twin of RetiredChoices: the
// spellings this arg used to accept, each with the message naming its
// replacement.
func ArgRetiredChoices(vals ...RetiredChoiceValue) ArgOption {
	return func(a *Arg) {
		a.retiredChoices = append([]RetiredChoiceValue{}, vals...)
	}
}

// CmdOption configures a Command during registration.
type CmdOption func(*Command)

// WithArgs adds positional arguments to a command.
func WithArgs(args ...Arg) CmdOption {
	return func(c *Command) {
		c.args = append(c.args, args...)
	}
}

// WithFlags adds flags to a command.
func WithFlags(flags ...Flag) CmdOption {
	return func(c *Command) {
		c.flags = append(c.flags, flags...)
	}
}

// WithFlagSets adds flag sets (reusable flag bundles) to a command.
func WithFlagSets(flagSets ...FlagSet) CmdOption {
	return func(c *Command) {
		c.flagSets = append(c.flagSets, flagSets...)
	}
}

// WithConstraints adds declared constraints to a command: the two co-occurrence
// families (AtLeastOne, AllOrNone) and the two one-way dependency rules
// (Requires, Implies). The container is named for what it holds -- the schema
// key has been `constraints` since v1 (contract §26.1).
func WithConstraints(cs ...Constraint) CmdOption {
	return func(c *Command) {
		for _, decl := range cs {
			c.constraints = append(c.constraints, decl.(constraintDecl))
		}
	}
}

// WithPassthrough marks a command as passthrough (skips parsing, forwards raw args).
func WithPassthrough(handler PassthroughHandler) CmdOption {
	return func(c *Command) {
		c.Passthrough = true
		c.PassthroughHandler = handler
	}
}

// WithHidden marks a command as hidden (excluded from help but still routable).
func WithHidden() CmdOption {
	return func(c *Command) {
		c.Hidden = true
	}
}

// WithInteractive marks a command as interactive (visible in help but excluded from tool export).
func WithInteractive() CmdOption {
	return func(c *Command) {
		c.Interactive = true
	}
}

// WithEffect declares a command's mandatory effect classification: either
// EffectReadOnly or EffectMutating. There is no default and nothing is
// inferred -- a command registered without it is a registration-time hard
// error, and so is a value that is neither constant.
//
// A mutating command participates in dry mode and may call the mutating
// members of ctx.Effects(). A read_only command may not, and calling any
// mutating member is a hard error at call time. Classification does NOT decide
// whether a command prompts -- WithConsequential does (§8).
func WithEffect(effect string) CmdOption {
	return func(c *Command) {
		c.Effect = effect
		c.effectSet = true
	}
}

// WithConsequential declares that a command's effects are worth interrupting
// someone for. It is the ONLY thing that makes the framework prompt (§8.1): a
// plain mutating command never does.
//
// It is deliberately not mandatory. Classification answers "should a dry run
// record rather than execute?", which almost everything that touches anything
// answers yes to; consequentiality is a separate, much rarer judgement, and
// making it mandatory would push every registration to answer it reflexively.
//
// Declaring it on a read_only command is a registration-time hard error: a
// command that changes nothing has nothing to confirm.
func WithConsequential() CmdOption {
	return func(c *Command) {
		c.Consequential = true
	}
}

// WithDryRunUnsupported declares that --dry-run is refused for this command,
// with a mandatory reason. It is the opt-out from the regime's baseline, where
// a mutating command's effects are recorded rather than executed under
// --dry-run.
//
// Declare it when a preview would LIE: when the command's effects escape the
// effects handle, or when its later steps read state its earlier (recorded,
// therefore un-performed) steps would have written. A refusal that names the
// reason is honest; a preview that silently diverges from the real run is not.
//
// The reason is mandatory and non-empty, and is shown both in the command's
// help and in the parse-time refusal. Declaring this on a read_only command is
// a registration-time hard error: a command that changes nothing has no
// effects a preview could misrepresent.
func WithDryRunUnsupported(reason string) CmdOption {
	return func(c *Command) {
		c.DryRunSupported = false
		c.DryRunUnsupportedReason = reason
	}
}

// PayloadSchema declares the command's machine payload contract (contract
// §19.5): the inline JSON Schema literal a payload supplied through
// Context.Payload is registered against. A command that declares none cannot
// produce a payload, and calling Context.Payload on it is a call-time hard
// error.
//
// The name is the contract's pinned spelling (§18.9 item 111), which is why it
// carries no With- prefix.
func PayloadSchema(schema map[string]interface{}) CmdOption {
	return func(c *Command) {
		c.PayloadSchema = schema
	}
}

// OwnsStdout declares that this command's stdout is its own document
// (contract §19.6): in machine mode stdout carries that document byte-exactly
// and the envelope moves to stderr, together with every framework diagnostic
// it carries. Outside machine mode the declaration changes nothing at all --
// the command prints its document as it always did.
//
// The name is the contract's pinned spelling (§18.9 item 111), which is why it
// carries no With- prefix.
func OwnsStdout() CmdOption {
	return func(c *Command) {
		c.OwnsStdout = true
	}
}

// WithGrants declares per-effect-kind authorizations for a command. A grant is
// not permission to do something otherwise forbidden; it is a labelled reason
// that surfaces in the preview so a reviewer reading a dry run sees why a
// dangerous step is there.
func WithGrants(grants ...Grant) CmdOption {
	return func(c *Command) {
		c.Grants = append(c.Grants, grants...)
	}
}

// WithForwarding declares that a handler deliberately accepts and forwards the
// app's global flag values. The reason is mandatory and non-empty, and is
// emitted in the schema so a consumer's audit gate can review every forwarding
// site. In Go the declaration is inert beyond the schema emission -- guard v2's
// enforcement is Python-only.
func WithForwarding(reason string) CmdOption {
	return func(c *Command) {
		c.Forwarding = &Forwarding{Reason: reason}
	}
}

// WithConfigFields binds config fields to a command. At startup, bound required
// config fields are validated to be present with correct types in the config file.
// Each field name must exist in app.configFields (validated at Run/Test time).
func WithConfigFields(fields ...string) CmdOption {
	return func(c *Command) {
		c.configFields = append(c.configFields, fields...)
	}
}

// WithTags adds tags to a command.
func WithTags(tags ...string) CmdOption {
	return func(c *Command) {
		seen := make(map[string]bool)
		for _, t := range tags {
			if !identifierRe.MatchString(t) {
				panic(errInvalidTagName(t))
			}
			if !seen[t] {
				c.tags = append(c.tags, t)
				seen[t] = true
			}
		}
	}
}

// validateAndDedup validates tag names and removes duplicates, preserving order.
func validateAndDedup(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	result := make([]string, 0, len(tags))
	for _, t := range tags {
		if !identifierRe.MatchString(t) {
			panic(errInvalidTagName(t))
		}
		if !seen[t] {
			result = append(result, t)
			seen[t] = true
		}
	}
	return result
}

// mergeTags merges two tag slices, deduplicates, and sorts.
func mergeTags(a, b []string) []string {
	seen := make(map[string]bool)
	for _, t := range a {
		seen[t] = true
	}
	for _, t := range b {
		seen[t] = true
	}
	if len(seen) == 0 {
		return nil
	}
	result := make([]string, 0, len(seen))
	for t := range seen {
		result = append(result, t)
	}
	sort.Strings(result)
	return result
}

// --- Flag constructors ---

// StringFlag creates a string-typed flag.
func StringFlag(name, help string, opts ...FlagOption) Flag {
	f := Flag{
		Name:     name,
		Type:     TypeStr,
		Help:     help,
		Prefixed: true,
	}
	for _, opt := range opts {
		opt.applyFlag(&f)
	}
	validateFlagConfig(&f)
	return f
}

// BoolFlag creates a boolean-typed flag.
func BoolFlag(name, help string, opts ...FlagOption) Flag {
	f := Flag{
		Name:      name,
		Type:      TypeBool,
		Help:      help,
		Prefixed:  true,
		Negatable: true,
	}
	for _, opt := range opts {
		opt.applyFlag(&f)
	}
	validateFlagConfig(&f)
	return f
}

// IntFlag creates an integer-typed flag.
func IntFlag(name, help string, opts ...FlagOption) Flag {
	f := Flag{
		Name:     name,
		Type:     TypeInt,
		Help:     help,
		Prefixed: true,
	}
	for _, opt := range opts {
		opt.applyFlag(&f)
	}
	validateFlagConfig(&f)
	return f
}

// FloatFlag creates a float-typed flag.
func FloatFlag(name, help string, opts ...FlagOption) Flag {
	f := Flag{
		Name:     name,
		Type:     TypeFloat,
		Help:     help,
		Prefixed: true,
	}
	for _, opt := range opts {
		opt.applyFlag(&f)
	}
	validateFlagConfig(&f)
	return f
}

// ListFlag creates a list-typed flag. itemType must be TypeStr, TypeInt, or TypeFloat.
// List flags are automatically repeatable. The Unique option is supported.
// CLI usage: --flag val1 --flag val2 (each value coerced to itemType).
func ListFlag(itemType FlagType, name, help string, opts ...FlagOption) Flag {
	lt := ListOf(itemType) // panics on bad itemType
	f := Flag{
		Name:       name,
		Type:       lt,
		Help:       help,
		Prefixed:   true,
		Repeatable: true,
	}
	for _, opt := range opts {
		opt.applyFlag(&f)
	}
	// List flags are always repeatable; override any explicit Repeatable(false)
	f.Repeatable = true
	validateFlagConfig(&f)
	return f
}

// DictFlag creates a dict-typed flag. valueType must be TypeStr, TypeInt, or TypeFloat.
// Dict flags are automatically repeatable (multiple key=value pairs).
// CLI usage: --flag key=value --flag key2=value2
// Also accepts JSON: --flag '{"key": "value"}'
func DictFlag(valueType FlagType, name, help string, opts ...FlagOption) Flag {
	dt := DictOf(valueType) // panics on bad valueType
	f := Flag{
		Name:       name,
		Type:       dt,
		Help:       help,
		Prefixed:   true,
		Repeatable: true,
	}
	for _, opt := range opts {
		opt.applyFlag(&f)
	}
	// Dict flags are always repeatable
	f.Repeatable = true
	validateFlagConfig(&f)
	return f
}

// NewArg creates a positional argument.
func NewArg(name, help string, opts ...ArgOption) Arg {
	if strings.TrimSpace(help) == "" {
		panic(errArgHelpEmpty)
	}
	// The consent parameter name is the one reserved name that reaches the
	// positional-arg surface.
	if name == reservedConsentParamName {
		panic(errArgNameConsentReserved)
	}
	a := Arg{
		Name: name,
		Help: help,
		Type: TypeStr,
	}
	for _, opt := range opts {
		opt(&a)
	}
	// Presence is mandatory and is never inferred (contract §23.1, §23.3).
	resolveArgPresence(&a)
	// Validate type: scalar types always allowed; list types only on variadic args
	if IsListType(a.Type) {
		if !a.IsVariadic {
			panic(errArgListTypeRequiresVariadic(a.Name))
		}
		// Item type must be scalar
		item := ItemType(a.Type)
		switch item {
		case TypeStr, TypeInt, TypeFloat:
			// ok
		default:
			panic(errArgListItemTypeBad(a.Name))
		}
	} else if IsDictType(a.Type) {
		panic(errArgDictTypeNotSupported(a.Name))
	} else {
		switch a.Type {
		case TypeStr, TypeBool, TypeInt, TypeFloat:
			// ok
		default:
			panic(errArgTypeBad(a.Type))
		}
	}
	// Validate choices. The list-typed refusal is DELETED (contract §25.4): a
	// variadic arg declared with a scalar element type and one declared with the
	// list carrier are one declaration with one published shape, so a ban that
	// fired on one spelling and not the other was two rules for one fact. The
	// entries are checked against the ELEMENT type in both spellings.
	if a.Choices != nil {
		item := ItemType(a.Type)
		if item == TypeBool {
			panic(errArgChoicesIncompatibleBool(a.Name))
		}
		if len(a.Choices) == 0 {
			panic(errArgChoicesEmpty(a.Name))
		}
		for _, c := range a.Choices {
			switch item {
			case TypeStr:
				if _, ok := c.(string); !ok {
					panic(errArgChoiceTypeMismatch(a.Name, c, "str"))
				}
			case TypeInt:
				if _, ok := c.(int); !ok {
					panic(errArgChoiceTypeMismatch(a.Name, c, "int"))
				}
			case TypeFloat:
				if _, ok := c.(float64); !ok {
					panic(errArgChoiceTypeMismatch(a.Name, c, "float"))
				}
			}
		}
		// §12.14's guard: an int choice beyond ±2^53 cannot survive the
		// fragment that publishes it.
		checkChoiceMagnitudes("Arg", a.Name, a.Choices)
	}
	// Validate default type matches declared type. There is no list branch:
	// a list-typed arg must be variadic (refused just above otherwise), and a
	// variadic arg cannot declare a default at all (resolveArgPresence), so an
	// arg default is always a scalar.
	if a.hasDefault && a.Default != nil {
		switch a.Type {
		case TypeStr:
			if _, ok := a.Default.(string); !ok {
				var gotType string
				switch a.Default.(type) {
				case bool:
					gotType = "bool"
				case int:
					gotType = "int"
				default:
					gotType = fmt.Sprintf("%T", a.Default)
				}
				panic(errArgStrDefaultTypeMismatch(a.Name, gotType))
			}
		case TypeInt:
			if _, ok := a.Default.(int); !ok {
				var gotType string
				switch a.Default.(type) {
				case string:
					gotType = "str"
				case bool:
					gotType = "bool"
				default:
					gotType = fmt.Sprintf("%T", a.Default)
				}
				panic(errArgIntDefaultTypeMismatch(a.Name, gotType))
			}
		case TypeFloat:
			if _, ok := a.Default.(float64); !ok {
				var gotType string
				switch a.Default.(type) {
				case string:
					gotType = "str"
				case bool:
					gotType = "bool"
				case int:
					gotType = "int"
				default:
					gotType = fmt.Sprintf("%T", a.Default)
				}
				panic(errArgFloatDefaultTypeMismatch(a.Name, gotType))
			}
		case TypeBool:
			if _, ok := a.Default.(bool); !ok {
				var gotType string
				switch a.Default.(type) {
				case string:
					gotType = "str"
				case int:
					gotType = "int"
				default:
					gotType = fmt.Sprintf("%T", a.Default)
				}
				panic(errArgBoolDefaultTypeMismatch(a.Name, gotType))
			}
		}
	}
	// Retired choices, ahead of the default-in-choices check for the reason
	// stated at the flag surface.
	validateRetiredChoices(
		a.Name, a.retiredChoices, a.Choices, ItemType(a.Type),
		a.hasDefault, a.Default, argRetiredChoiceTemplates,
	)
	// Validate default is in choices
	if a.Choices != nil && a.hasDefault && a.Default != nil {
		found := false
		for _, c := range a.Choices {
			if a.Default == c {
				found = true
				break
			}
		}
		if !found {
			choiceParts := make([]string, len(a.Choices))
			for i, c := range a.Choices {
				choiceParts[i] = fmt.Sprintf("'%v'", c)
			}
			panic(errArgDefaultNotInChoices(a.Name, a.Default, strings.Join(choiceParts, ", ")))
		}
	}
	return a
}

// --- The presence declaration (contract §23) ---

// presenceBitOrder is the canonical rendering order of the three declarations:
// required, optional, default. A "declared twice" message names the two that
// were supplied in this order regardless of the order they were written in, so
// the line is deterministic.
var presenceBitOrder = []uint8{presenceBitRequired, presenceBitOptional, presenceBitDefault}

// flagPresenceSpelling renders Go's spelling of one presence declaration for a
// flag. A default spelling carries its value, formatted by the same formatter
// the other declaration guards use.
func flagPresenceSpelling(bit uint8, dflt interface{}) string {
	switch bit {
	case presenceBitRequired:
		return "Required()"
	case presenceBitOptional:
		return "Optional()"
	default:
		if dflt == nil {
			return "Default(nil)"
		}
		return fmt.Sprintf("Default(%s)", formatValueForError(dflt))
	}
}

// argPresenceSpelling is flagPresenceSpelling's positional-arg twin.
func argPresenceSpelling(bit uint8, dflt interface{}) string {
	switch bit {
	case presenceBitRequired:
		return "ArgRequired()"
	case presenceBitOptional:
		return "ArgOptional()"
	default:
		if dflt == nil {
			return "ArgDefault(nil)"
		}
		return fmt.Sprintf("ArgDefault(%s)", formatValueForError(dflt))
	}
}

// declaredPresenceBits returns the declared bits in canonical order.
func declaredPresenceBits(bits uint8) []uint8 {
	var out []uint8
	for _, bit := range presenceBitOrder {
		if bits&bit != 0 {
			out = append(out, bit)
		}
	}
	return out
}

// resolveFlagPresence enforces §23.1 on a flag and stores the resolved
// declaration. It is idempotent, so it can run both in the constructors (where
// most flags are built) and again at command registration (which is the only
// place a struct literal is seen).
func resolveFlagPresence(f *Flag) {
	declared := declaredPresenceBits(f.presenceBits)
	switch len(declared) {
	case 0:
		panic(errFlagPresenceUndeclared(f.Name))
	case 1:
		// resolved below
	default:
		panic(errFlagPresenceDeclaredTwice(f.Name,
			flagPresenceSpelling(declared[0], f.Default),
			flagPresenceSpelling(declared[1], f.Default)))
	}
	switch declared[0] {
	case presenceBitRequired:
		f.presence = presenceRequired
	case presenceBitOptional:
		f.presence = presenceOptional
	default:
		// A null-valued default is not a spelling of optionality: one spelling
		// per fact, so the value-shaped one is refused and redirected.
		if f.Default == nil {
			panic(errFlagDefaultNullNotOptional(f.Name))
		}
		f.presence = presenceDefault
	}
}

// resolveArgPresence is resolveFlagPresence's positional-arg twin.
func resolveArgPresence(a *Arg) {
	declared := declaredPresenceBits(a.presenceBits)
	switch len(declared) {
	case 0:
		panic(errArgPresenceUndeclared(a.Name))
	case 1:
		// resolved below
	default:
		panic(errArgPresenceDeclaredTwice(a.Name,
			argPresenceSpelling(declared[0], a.Default),
			argPresenceSpelling(declared[1], a.Default)))
	}
	switch declared[0] {
	case presenceBitRequired:
		a.presence = presenceRequired
	case presenceBitOptional:
		a.presence = presenceOptional
	default:
		if a.Default == nil {
			panic(errArgDefaultNullNotOptional(a.Name))
		}
		// A variadic arg always delivers a list, so its empty case is
		// ArgOptional() and a default has nothing to mean.
		if a.IsVariadic {
			panic(errArgVariadicDefault(a.Name))
		}
		a.presence = presenceDefault
	}
}

// validateFlagConfig panics on invalid flag configuration (programmer error).
func validateFlagConfig(f *Flag) {
	if strings.TrimSpace(f.Help) == "" {
		panic(errFlagHelpEmpty)
	}
	if f.Name == "force" {
		panic(errFlagForceReserved)
	}
	// The reserved quartet. The ban is UNCONDITIONAL and applies at every level
	// -- command flags, flag-set flags, mutex-group flags and app global flags.
	// Short names and positional arg names are unaffected.
	if reservedFrameworkFlagNames[f.Name] {
		panic(errFlagNameReservedByFramework(f.Name))
	}
	// The machine-mode flag, on the same unconditional tier (§7.1's amendment).
	if f.Name == reservedMachineFlagName {
		panic(errFlagNameJSONReserved)
	}
	// The consent parameter name, reserved on the flag surface too.
	if f.Name == reservedConsentParamName {
		panic(errFlagNameConsentReserved)
	}
	if bannedFlagNames[f.Name] {
		panic(errFlagNameYesBanned)
	}
	if strings.HasPrefix(f.Name, "no-") {
		panic(errFlagNoPrefixReserved(f.Name))
	}
	// Presence is mandatory and is never inferred from another declaration
	// (contract §23.1).
	resolveFlagPresence(f)
	if f.Repeatable && f.Type == TypeBool {
		panic(errFlagRepeatableIncompatibleBool(f.Name))
	}
	// A dict is the one carrier choices cannot describe: its keys are
	// structurally strings and its values are not a closed set. A LIST carrier
	// takes choices and constrains each element, which is what the published
	// fragment's `items` enum says (contract §25.5).
	if IsDictType(f.Type) && f.Choices != nil {
		panic(errFlagDictCannotCombineChoices(f.Name))
	}
	// Unique requires repeatable; repeatable requires explicit unique
	if f.Repeatable && !f.hasUnique {
		panic(errFlagRepeatableRequiresExplicitUnique(f.Name))
	}
	if f.hasUnique && !f.Repeatable {
		panic(errFlagUniqueRequiresRepeatable(f.Name))
	}
	// Dict flags: env_separator for dicts means JSON parse from env (env_separator not used
	// for splitting). For list types, env_separator works as before.
	// EnvSeparator validations
	if f.EnvSeparator != "" && !f.Repeatable {
		panic(errFlagEnvSeparatorRequiresRepeatable(f.Name))
	}
	if f.EnvSeparator != "" && f.Env == "" {
		panic(errFlagEnvSeparatorRequiresEnv(f.Name))
	}
	if f.Repeatable && f.Env != "" && f.EnvSeparator == "" && !IsDictType(f.Type) {
		panic(errFlagRepeatableEnvRequiresSeparator(f.Name))
	}
	if f.EnvSeparator != "" && len(f.EnvSeparator) != 1 {
		panic(errFlagEnvSeparatorSingleChar(f.Name))
	}
	if f.EnvSeparator == "\\" {
		panic(errFlagEnvSeparatorBackslash(f.Name))
	}
	if f.Choices != nil {
		if f.Type == TypeBool {
			panic(errFlagChoicesIncompatibleBool(f.Name))
		}
		if len(f.Choices) == 0 {
			panic(errFlagChoicesEmpty(f.Name))
		}
		// Validate each choice matches the flag type
		for _, c := range f.Choices {
			switch f.Type {
			case TypeStr:
				if _, ok := c.(string); !ok {
					panic(errFlagChoiceTypeMismatch(f.Name, c, "str"))
				}
			case TypeInt:
				if _, ok := c.(int); !ok {
					panic(errFlagChoiceTypeMismatch(f.Name, c, "int"))
				}
			case TypeFloat:
				if _, ok := c.(float64); !ok {
					panic(errFlagChoiceTypeMismatch(f.Name, c, "float"))
				}
			}
		}
		// §12.14's guard: an int choice beyond ±2^53 cannot survive the
		// fragment that publishes it.
		checkChoiceMagnitudes("Flag", f.Name, f.Choices)
	}
	// Validate int default type
	if f.Type == TypeInt && f.hasDefault && f.Default != nil {
		if !f.Repeatable {
			if _, ok := f.Default.(int); !ok {
				var gotType string
				switch f.Default.(type) {
				case string:
					gotType = "str"
				case bool:
					gotType = "bool"
				default:
					gotType = fmt.Sprintf("%T", f.Default)
				}
				panic(errFlagIntDefaultTypeMismatch(f.Name, gotType))
			}
		}
	}
	// Validate float default type
	if f.Type == TypeFloat && f.hasDefault && f.Default != nil {
		if !f.Repeatable {
			if _, ok := f.Default.(float64); !ok {
				var gotType string
				switch f.Default.(type) {
				case string:
					gotType = "str"
				case bool:
					gotType = "bool"
				case int:
					gotType = "int"
				default:
					gotType = fmt.Sprintf("%T", f.Default)
				}
				panic(errFlagFloatDefaultTypeMismatch(f.Name, gotType))
			}
		}
	}
	// Validate dict flag defaults: must be map[string]interface{} with correct value types
	if IsDictType(f.Type) && f.hasDefault && f.Default != nil {
		m, ok := f.Default.(map[string]interface{})
		if !ok {
			panic(errFlagDictDefaultMustBeMap(f.Name))
		}
		valType := ItemType(f.Type)
		for k, v := range m {
			if errStr := validateScalarType(v, valType); errStr != "" {
				panic(errFlagDefaultValueForKey(f.Name, k, errStr))
			}
		}
	} else if IsListType(f.Type) && f.hasDefault && f.Default != nil {
		// List flag defaults: must be []interface{} with correct item types
		slice, ok := f.Default.([]interface{})
		if !ok {
			panic(errFlagListDefaultMustBeSlice(f.Name))
		}
		elemType := ItemType(f.Type)
		for i, elem := range slice {
			if errStr := validateScalarType(elem, elemType); errStr != "" {
				panic(errFlagDefaultElementError(f.Name, i, errStr))
			}
			// Auto-coerce int to float64 for float list defaults
			if elemType == TypeFloat {
				if intVal, ok := elem.(int); ok {
					slice[i] = float64(intVal)
				}
			}
		}
	} else if f.Repeatable && !IsCompoundType(f.Type) && f.hasDefault && f.Default != nil {
		// Validate repeatable scalar flag defaults
		slice, ok := f.Default.([]interface{})
		if !ok {
			panic(errFlagRepeatableDefaultMustBeList(f.Name))
		}
		for i, elem := range slice {
			switch f.Type {
			case TypeStr:
				if _, ok := elem.(string); !ok {
					panic(errFlagDefaultElementTypeMismatch(f.Name, i, "str"))
				}
			case TypeInt:
				if _, ok := elem.(int); !ok {
					panic(errFlagDefaultElementTypeMismatch(f.Name, i, "int"))
				}
			case TypeFloat:
				if intVal, ok := elem.(int); ok {
					slice[i] = float64(intVal)
				} else if _, ok := elem.(float64); !ok {
					panic(errFlagDefaultElementTypeMismatch(f.Name, i, "float"))
				}
			}
		}
	}
	// For non-bool, non-repeatable: negatable is forced off
	if f.Type != TypeBool {
		f.Negatable = false
	}
	// Retired choices. The guards run after the live choices are validated --
	// a declaration that got its choices wrong is told that first -- and
	// BEFORE the default-in-choices check, so a default naming a retired
	// spelling is answered by the sentence that names the reason rather than
	// by the list the value is missing from.
	validateRetiredChoices(
		f.Name, f.retiredChoices, f.Choices, ItemType(f.Type),
		f.hasDefault, f.Default, flagRetiredChoiceTemplates,
	)
	// Validate default is in choices. The check applies to declared VALUES
	// only: an optional flag has no value, and absence is never matched
	// against choices (contract §23.5).
	if f.Choices != nil && f.hasDefault && f.Default != nil && !f.Repeatable {
		found := false
		for _, c := range f.Choices {
			if f.Default == c {
				found = true
				break
			}
		}
		if !found {
			choiceParts := make([]string, len(f.Choices))
			for i, c := range f.Choices {
				choiceParts[i] = fmt.Sprintf("'%v'", c)
			}
			panic(errFlagDefaultNotInChoices(f.Name, f.Default, strings.Join(choiceParts, ", ")))
		}
	}
}

// validateScalarType checks if a value matches a scalar FlagType.
// Returns an error message or empty string on success.
func validateScalarType(v interface{}, t FlagType) string {
	switch t {
	case TypeStr:
		if _, ok := v.(string); !ok {
			return errExpectedStrGot(describeGoType(v))
		}
	case TypeInt:
		if _, ok := v.(int); !ok {
			return errExpectedIntGot(describeGoType(v))
		}
	case TypeFloat:
		if _, ok := v.(float64); ok {
			return ""
		}
		if _, ok := v.(int); ok {
			return "" // int is acceptable for float
		}
		return errExpectedFloatGot(describeGoType(v))
	case TypeBool:
		if _, ok := v.(bool); !ok {
			return errExpectedBoolGot(describeGoType(v))
		}
	}
	return ""
}

// --- App ---

// NewApp creates a new CLI application.
func NewApp(name, version, help string, opts ...AppOption) *App {
	if strings.TrimSpace(help) == "" {
		panic(errAppHelpEmpty)
	}
	a := &App{
		Name:          name,
		Version:       version,
		Help:          help,
		commands:      make(map[string]*Command),
		groups:        make(map[string]*Group),
		deprecatedMap: make(map[string]string),
	}
	for _, opt := range opts {
		opt(a)
	}

	// Resolve infrastructure roots eagerly, immediately after options are
	// applied. Infra vars have no argv dependency, so their resolution is sound
	// at construction time -- and this is precisely WHY it is hermetic-immune:
	// there is no argv yet to consult, so --hermetic (which only suppresses
	// argv-derived config/env behavior) can never affect location roots.
	a.infraRoots = make(map[string]string)
	a.infraRootFromEnv = make(map[string]bool)
	for _, decl := range a.infraRootDecls {
		if _, dup := a.infraRoots[decl.envVar]; dup {
			panic(errDuplicateInfraRootEnvVar(decl.envVar))
		}
		if val, ok := os.LookupEnv(decl.envVar); ok {
			a.infraRoots[decl.envVar] = expandTilde(val)
			a.infraRootFromEnv[decl.envVar] = true
		} else {
			a.infraRoots[decl.envVar] = expandTilde(decl.defaultPath)
			a.infraRootFromEnv[decl.envVar] = false
		}
		a.infraRootOrder = append(a.infraRootOrder, decl.envVar)
	}
	// Handshake env vars must not collide with declared roots.
	for _, ev := range a.handshakeOrder {
		if _, isRoot := a.infraRoots[ev]; isRoot {
			panic(errHandshakeIsAlreadyInfraRoot(ev))
		}
	}
	// Connection env vars must not collide with declared roots or handshakes.
	for _, ev := range a.connectionOrder {
		if _, isRoot := a.infraRoots[ev]; isRoot {
			panic(errConnectionEnvIsAlreadyInfraRoot(ev))
		}
		if _, isHandshake := a.handshakeEnvs[ev]; isHandshake {
			panic(errConnectionEnvIsAlreadyHandshake(ev))
		}
	}
	// Resolve the config-path marker (if any) now that roots exist.
	if a.configPathRef != nil {
		resolved, err := a.resolveInfraPath(*a.configPathRef)
		if err != nil {
			panic(err.Error())
		}
		a.configPathOverride = resolved
	}
	// Resolve the --dump-schema target once, at construction: a declared
	// marker through its root, a declared relative path and the framework's own
	// default against the construction-time cwd.
	if a.schemaPathRef != nil {
		resolved, err := a.resolveInfraPath(*a.schemaPathRef)
		if err != nil {
			panic(err.Error())
		}
		a.schemaPath = resolved
	}
	schemaTarget := a.schemaPath
	if schemaTarget == "" {
		schemaTarget = filepath.Join(".strictcli", "schema.json")
	}
	if abs, err := filepath.Abs(schemaTarget); err == nil {
		a.schemaOutPath = abs
	} else {
		a.schemaOutPath = schemaTarget
	}

	// Default config format to "json" if not set
	if a.configFormat == "" {
		a.configFormat = "json"
	}
	if a.configFormat != "json" && a.configFormat != "toml" {
		fmt.Fprintf(os.Stderr, "App.config_format must be \"json\" or \"toml\", got %q\n", a.configFormat)
		os.Exit(1)
	}
	if a.configEnabled {
		a.registerConfigGroup()
	}
	// Enable check system when WithChecks(path) or WithChecksEmbed(data) was provided
	if a.checksPath != "" && len(a.checksEmbed) > 0 {
		panic(errCannotUseBothChecksAndEmbed)
	}
	if a.checksPath != "" {
		if _, err := os.Stat(a.checksPath); err != nil {
			panic(errChecksPathNotExist(a.checksPath))
		}
		appName, defs, order, err := loadChecksToml(a.checksPath)
		if err != nil {
			panic(err.Error())
		}
		if appName != a.Name {
			panic(errChecksTomlAppMismatch(appName, a.Name))
		}
		a.enableChecks()
		for _, name := range order {
			if err := a.addCheckDef(defs[name]); err != nil {
				panic(err.Error())
			}
		}
	} else if len(a.checksEmbed) > 0 {
		appName, defs, order, err := parseChecksToml(a.checksEmbed)
		if err != nil {
			panic(err.Error())
		}
		if appName != a.Name {
			panic(errChecksTomlAppMismatch(appName, a.Name))
		}
		a.enableChecks()
		for _, name := range order {
			if err := a.addCheckDef(defs[name]); err != nil {
				panic(err.Error())
			}
		}
	}
	// Test-coverage instrumentation: register the built-in provider, but only
	// when the DECLARED directory exists. An app installed elsewhere finds it
	// absent and stays uninstrumented: no provider, no paths, no writes
	// anywhere -- in particular not into whatever directory the consumer
	// happened to start the CLI from.
	if a.testCoverageDir != "" {
		root, err := filepath.Abs(a.testCoverageDir)
		if err == nil {
			if info, statErr := os.Stat(root); statErr == nil && info.IsDir() {
				// Only the PATHS are computed here. The coverage directory
				// itself is created lazily by recordCoverage, immediately
				// before the first shard write: shards are written only on the
				// test-harness paths (Test and Call), so a plain CLI invocation
				// leaves no coverage/ behind.
				a.coverageDir = filepath.Join(root, "coverage")
				a.coverageManifestPath = filepath.Join(root, "test-coverage.json")
				a.coverageShardPath = filepath.Join(a.coverageDir, fmt.Sprintf("%d.jsonl", os.Getpid()))
				a.RegisterCheckProvider(a.testCoverageProvider)
			}
		}
	}
	return a
}

// RegisterErrorCheck registers an error-severity check implementation for a
// check declared with severity = "error" in checks.toml. The impl receives an
// *ErrorReporter (which can mint both error- and warn-severity problems) and
// must return a CheckOutcome obtained from that reporter.
//
// Panics if checks are not enabled, the name is not declared, it is already
// registered, or the declared severity is not "error" (see registerCheckImpl).
func (a *App) RegisterErrorCheck(name string, fn func(CheckContext, *ErrorReporter) CheckOutcome) {
	a.registerCheckImpl(name, "error", func(ctx CheckContext) CheckOutcome {
		r := &ErrorReporter{}
		return fn(ctx, r)
	})
}

// RegisterWarnCheck registers a warn-severity check implementation for a check
// declared with severity = "warn" in checks.toml. The impl receives a
// *WarnReporter, which structurally lacks error-minting: a warn check cannot
// produce an error-severity problem, so it can never cascade.
//
// Panics under the same conditions as RegisterErrorCheck, with the severity
// cross-check requiring the declared severity to be "warn".
func (a *App) RegisterWarnCheck(name string, fn func(CheckContext, *WarnReporter) CheckOutcome) {
	a.registerCheckImpl(name, "warn", func(ctx CheckContext) CheckOutcome {
		r := &WarnReporter{}
		return fn(ctx, r)
	})
}

// registerCheckImpl is the single registration chokepoint shared by
// RegisterErrorCheck and RegisterWarnCheck. It enforces the double-entry
// contract (declared vs registered) and cross-checks the registration FORM
// against the TOML-declared severity so that, e.g., calling RegisterErrorCheck
// on a severity="warn" definition is a hard error.
func (a *App) registerCheckImpl(name, form string, run func(CheckContext) CheckOutcome) {
	if !a.checksEnabled {
		panic(errCannotRegisterCheckNotEnabled(name))
	}
	def, ok := a.checkDefs[name]
	if !ok {
		panic(errCannotRegisterCheckNotDeclared(name))
	}
	if def.impl != nil {
		panic(errCheckDuplicateRegistration(name))
	}
	if def.severity != form {
		used, want := "RegisterErrorCheck", "RegisterWarnCheck"
		if form == "warn" {
			used, want = "RegisterWarnCheck", "RegisterErrorCheck"
		}
		panic(errCheckSeverityMismatch(name, def.severity, used, want))
	}
	def.impl = run
	def.implForm = form
}

// SetCheckContext sets the factory function that provides CheckContext to check implementations.
func (a *App) SetCheckContext(factory func() CheckContext) {
	a.checkContextFactory = factory
}

// TagContract declares that any command tagged with the given tag must have a flag
// with the given name. Validated at Run/Test time.
func (a *App) TagContract(tag, requiresFlag string) {
	if !identifierRe.MatchString(tag) {
		panic(errInvalidTagName(tag))
	}
	if a.tagContracts == nil {
		a.tagContracts = make(map[string]string)
	}
	a.tagContracts[tag] = requiresFlag
}

// validateCheckRegistrations checks that all declared checks have been registered.
// Returns an error message listing unregistered checks, or empty string if all are registered.
func (a *App) validateCheckRegistrations() string {
	if !a.checksEnabled {
		return ""
	}
	var missing []string
	for name, def := range a.checkDefs {
		if def.impl == nil {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	sort.Strings(missing)
	return fmt.Sprintf("checks declared in checks.toml but not registered: %s", strings.Join(missing, ", "))
}

// validateConfigFieldBindings checks that all WithConfigFields references point to
// declared config fields. Returns an error message listing violations, or empty
// string if all bindings are valid.
func (a *App) validateConfigFieldBindings() string {
	var violations []string
	// Check top-level commands
	for _, name := range a.cmdOrder {
		cmd := a.commands[name]
		for _, field := range cmd.configFields {
			if a.configFields == nil || a.configFields[field] == nil {
				violations = append(violations, errCommandConfigFieldsUnknownField(cmd.Name, field))
			}
		}
	}
	// Check commands in groups recursively
	var checkGroup func(g *Group, path string)
	checkGroup = func(g *Group, path string) {
		for _, name := range g.order {
			cmd := g.Commands[name]
			for _, field := range cmd.configFields {
				if a.configFields == nil || a.configFields[field] == nil {
					violations = append(violations, errCommandConfigFieldsUnknownField(cmd.Name, field))
				}
			}
		}
		for _, name := range g.groupOrder {
			checkGroup(g.Groups[name], path+name+" ")
		}
	}
	for _, name := range a.groupOrder {
		checkGroup(a.groups[name], name+" ")
	}
	if len(violations) == 0 {
		return ""
	}
	sort.Strings(violations)
	return strings.Join(violations, "; ")
}

// validateTagContracts checks that commands with a given tag have the required flag.
// Returns an error message listing violations, or empty string if all contracts are satisfied.
func (a *App) validateTagContracts() string {
	if len(a.tagContracts) == 0 {
		return ""
	}
	var violations []string
	// Check top-level commands
	for _, cmd := range a.commands {
		if v := checkCommandTagContract(cmd, a.tagContracts, a.globalFlags); v != "" {
			violations = append(violations, v)
		}
	}
	// Check commands in groups recursively
	for _, g := range a.groups {
		violations = append(violations, checkGroupTagContracts(g, a.tagContracts, a.globalFlags)...)
	}
	if len(violations) == 0 {
		return ""
	}
	sort.Strings(violations)
	return strings.Join(violations, "; ")
}

func checkCommandTagContract(cmd *Command, contracts map[string]string, globalFlags []Flag) string {
	if cmd.Passthrough {
		return ""
	}
	for _, tag := range cmd.tags {
		requiredFlag, ok := contracts[tag]
		if !ok {
			continue
		}
		found := false
		for _, f := range cmd.flags {
			if f.Name == requiredFlag {
				found = true
				break
			}
		}
		if !found {
			for _, f := range globalFlags {
				if f.Name == requiredFlag {
					found = true
					break
				}
			}
		}
		if !found {
			return fmt.Sprintf("command %q: tag %q requires flag \"--%s\"", cmd.Name, tag, requiredFlag)
		}
	}
	return ""
}

func checkGroupTagContracts(g *Group, contracts map[string]string, globalFlags []Flag) []string {
	var violations []string
	for _, cmd := range g.Commands {
		if v := checkCommandTagContract(cmd, contracts, globalFlags); v != "" {
			violations = append(violations, v)
		}
	}
	for _, sub := range g.Groups {
		violations = append(violations, checkGroupTagContracts(sub, contracts, globalFlags)...)
	}
	return violations
}

// Command registers a top-level command.
// checkFlagConfigFieldDefault panics when a colliding flag and config field have
// conflicting explicit defaults.
//
// A ConfigField whose name equals a flag's param name is a validation-only
// declaration that annotates the flag. Their defaults must agree. The matrix:
// both absent OK; equal OK; both present unequal = error; one absent OK (the
// flag's default wins for rendering). A flag has a default exactly when its
// DECLARED presence is "default" (contract §23.1) -- never when its default
// value happens to be non-nil, which would stand the value's shape in for the
// declaration.
func checkFlagConfigFieldDefault(flagName string, flagPresence presenceKind, flagDefault interface{}, cf *ConfigField) {
	flagHasDefault := flagPresence == presenceDefault
	cfHasDefault := cf.HasDefault && cf.Default != nil
	if flagHasDefault && cfHasDefault && !reflect.DeepEqual(flagDefault, cf.Default) {
		panic(errConfigFieldFlagDefaultDisagree(cf.Name, flagName, cf.Default, flagDefault))
	}
}

// checkCmdFieldCollisions panics if any of the command's flags collides with a
// registered config field whose default disagrees. Config fields registered
// after this command are checked from the App.ConfigField() side instead.
func (a *App) checkCmdFieldCollisions(cmd *Command) {
	if len(a.configFields) == 0 {
		return
	}
	for i := range cmd.flags {
		f := &cmd.flags[i]
		if cf, ok := a.configFields[flagParamName(f.Name)]; ok {
			checkFlagConfigFieldDefault(f.Name, f.presence, f.Default, cf)
		}
	}
}

// expandTilde expands a leading ~ (as ~ or ~/...) to the user's home directory.
func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		if p == "~" {
			return home
		}
		return filepath.Join(home, p[2:])
	}
	return p
}

// resolveInfraRootPath resolves an InfraRootPath marker against a roots map.
// Returns an error if the marker references an undeclared root.
func resolveInfraRootPath(ref InfraRootPath, roots map[string]string) (string, error) {
	root, ok := roots[ref.envVar]
	if !ok {
		return "", errRelativeToRootUndeclared(ref.envVar)
	}
	return filepath.Join(append([]string{root}, ref.parts...)...), nil
}

// resolveInfraPath resolves an InfraRootPath marker against the declared roots.
// Returns an error if the marker references an undeclared root.
func (a *App) resolveInfraPath(ref InfraRootPath) (string, error) {
	return resolveInfraRootPath(ref, a.infraRoots)
}

// validateFlagInfraMarker panics if a flag's Default is an InfraRootPath marker
// that references an undeclared root. Called at registration time so that a
// dangling marker is a construction-time hard error.
func (a *App) validateFlagInfraMarker(f *Flag) {
	if ref, ok := f.Default.(InfraRootPath); ok {
		if _, declared := a.infraRoots[ref.envVar]; !declared {
			panic(errFlagRelativeToRootUndeclared(f.Name, ref.envVar))
		}
	}
}

// validateFlagConnection enforces the connection-URL binding rules at
// registration time (mechanical enforcement, not review). A URL-class flag must
// bind to a declared connection env; the binding drives env resolution by
// reusing the per-flag Env channel. Called at registration so a misbound flag is
// a construction-time hard error.
func (a *App) validateFlagConnection(f *Flag) {
	if !f.ConnectionURL && f.ConnectionEnv == "" {
		return
	}
	// A connection binding may not be combined with a per-flag Env: the
	// connection env IS the flag's env source.
	if f.ConnectionEnv != "" && f.Env != "" && f.Env != f.ConnectionEnv {
		panic(errConnectionEnvWithPerFlagEnv(f.Name))
	}
	// URL-class flag with no binding: the exact bug class the framework refuses.
	if f.ConnectionURL && f.ConnectionEnv == "" {
		panic(errConnectionURLFlagUnbound(f.Name))
	}
	// A binding without the URL-class marker is inconsistent; require both.
	if f.ConnectionEnv != "" && !f.ConnectionURL {
		panic(errConnectionEnvWithoutURLFlag(f.Name))
	}
	// The referenced connection env must be declared at app level.
	if _, declared := a.connectionEnvs[f.ConnectionEnv]; !declared {
		panic(errFlagConnectionEnvUndeclared(f.Name, f.ConnectionEnv))
	}
	// Bind: the connection env is the flag's env source, so all downstream env
	// resolution (cli>env precedence, --hermetic suppression, source labeling)
	// works unchanged.
	f.Env = f.ConnectionEnv
}

// validateCmdInfraMarkers validates every flag on a command at registration.
// cmd.flags already contains flag-set and mutex flags (merged at build time).
func (a *App) validateCmdInfraMarkers(cmd *Command) {
	for i := range cmd.flags {
		a.validateFlagInfraMarker(&cmd.flags[i])
		a.validateFlagConnection(&cmd.flags[i])
	}
}

// infraAccess snapshots the app's infra data for a Context: resolved roots
// (captured value), the set of declared handshake env vars (read live), and the
// set of declared connection env vars (read live, but suppressed when hermetic
// is true so connection-dependent behavior skips visibly).
func (a *App) infraAccess(hermetic bool) *infraAccess {
	if len(a.infraRoots) == 0 && len(a.handshakeEnvs) == 0 && len(a.connectionEnvs) == 0 {
		return nil
	}
	roots := make(map[string]string, len(a.infraRoots))
	for k, v := range a.infraRoots {
		roots[k] = v
	}
	handshakes := make(map[string]bool, len(a.handshakeEnvs))
	for k := range a.handshakeEnvs {
		handshakes[k] = true
	}
	connections := make(map[string]bool, len(a.connectionEnvs))
	for k := range a.connectionEnvs {
		connections[k] = true
	}
	return &infraAccess{roots: roots, handshakes: handshakes, connections: connections, hermetic: hermetic}
}

// Command registers a top-level command with the given name, help text, and handler.
func (a *App) Command(name, help string, handler func(ctx *Context, kwargs map[string]interface{}) Outcome, opts ...CmdOption) {
	cmd := buildAndValidateCommand(name, help, handler, a.EnvPrefix, a.globalFlags, nil, opts)
	a.checkCmdFieldCollisions(cmd)
	a.validateCmdInfraMarkers(cmd)
	a.commands[name] = cmd
	a.cmdOrder = append(a.cmdOrder, name)
}

// Passthrough registers a passthrough command (raw args, no parsing).
// Accepts CmdOptions for validation purposes (e.g., to detect invalid passthrough+flags).
//
// It routes through the same single validated registration path as every other
// command -- there is no direct-Command-construction bypass left -- so a
// passthrough is classified with WithEffect like everything else.
func (a *App) Passthrough(name, help string, handler PassthroughHandler, opts ...CmdOption) {
	cmd := buildAndValidateCommand(name, help, nil, a.EnvPrefix, a.globalFlags, nil,
		append([]CmdOption{WithPassthrough(handler)}, opts...))
	a.commands[name] = cmd
	a.cmdOrder = append(a.cmdOrder, name)
}

// GlobalFlag registers a global flag on the app.
func (a *App) GlobalFlag(f Flag) {
	// The reserved quartet carries its own message on the global-flag path too.
	// Unreachable through the flag constructors (validateFlagConfig bans the
	// quartet first); kept so any other construction route says the same thing.
	if reservedFrameworkFlagNames[f.Name] {
		panic(errFlagNameReservedByFramework(f.Name))
	}
	if f.Name == reservedMachineFlagName {
		panic(errFlagNameJSONReserved)
	}
	if bannedFlagNames[f.Name] {
		panic(errFlagNameYesBanned)
	}
	// Check reserved names
	if reservedGlobalFlagNames[f.Name] {
		panic(errGlobalFlagNameReserved(f.Name))
	}
	// Short names are checked against the pre-existing reserved set only: the
	// framework quartet bans long names, and the four have no short forms.
	if f.Short != "" && reservedGlobalShortNames[f.Short] {
		panic(errGlobalShortFlagReserved(f.Short))
	}
	// Check for collisions with existing global flags
	for _, gf := range a.globalFlags {
		if gf.Name == f.Name {
			panic(errDuplicateGlobalFlag(f.Name))
		}
	}
	// Presence is mandatory at every level, global flags included (§23.1).
	// The constructors already resolved it; this catches a struct literal.
	resolveFlagPresence(&f)
	a.validateFlagInfraMarker(&f)
	a.validateFlagConnection(&f)
	a.globalFlags = append(a.globalFlags, f)
}

// Group creates and registers a command group.
func (a *App) Group(name, help string, tags ...string) *Group {
	if strings.TrimSpace(help) == "" {
		panic(errGroupHelpEmpty)
	}
	validTags := validateAndDedup(tags)
	grp := &Group{
		Name:            name,
		Help:            help,
		Commands:        make(map[string]*Command),
		Groups:          make(map[string]*Group),
		tags:            validTags,
		accumulatedTags: validTags,
		envPrefix:       a.EnvPrefix,
		app:             a,
		globalFlags:     a.globalFlags,
		deprecatedMap:   make(map[string]string),
	}
	a.groups[name] = grp
	a.groupOrder = append(a.groupOrder, name)
	return grp
}

// Group creates and registers a child subgroup.
func (g *Group) Group(name, help string, tags ...string) *Group {
	if strings.TrimSpace(help) == "" {
		panic(errGroupHelpEmpty)
	}
	if _, ok := g.Commands[name]; ok {
		panic(errGroupCollidesWithCommand(name))
	}
	if _, ok := g.Groups[name]; ok {
		panic(errGroupAlreadyRegistered(name))
	}
	validTags := validateAndDedup(tags)
	accumulated := mergeTags(g.accumulatedTags, validTags)
	sub := &Group{
		Name:            name,
		Help:            help,
		Commands:        make(map[string]*Command),
		Groups:          make(map[string]*Group),
		tags:            validTags,
		accumulatedTags: accumulated,
		envPrefix:       g.envPrefix,
		app:             g.app,
		globalFlags:     g.globalFlags,
		deprecatedMap:   make(map[string]string),
	}
	g.Groups[name] = sub
	g.groupOrder = append(g.groupOrder, name)
	return sub
}

// Command registers a command within a group.
func (g *Group) Command(name, help string, handler func(ctx *Context, kwargs map[string]interface{}) Outcome, opts ...CmdOption) {
	if _, ok := g.Groups[name]; ok {
		panic(errCommandCollidesWithGroup(name))
	}
	cmd := buildAndValidateCommand(name, help, handler, g.envPrefix, g.globalFlags, g.accumulatedTags, opts)
	if g.app != nil {
		g.app.checkCmdFieldCollisions(cmd)
		g.app.validateCmdInfraMarkers(cmd)
	}
	g.Commands[name] = cmd
	g.order = append(g.order, name)
}

// Deprecated registers a deprecated command on the app.
// Invoking a deprecated command prints the message to stderr and exits 1.
func (a *App) Deprecated(name, message string, opts ...CmdOption) {
	rejectDeprecatedEffect(name, opts)
	if strings.TrimSpace(name) == "" {
		panic(errDeprecatedNameEmpty)
	}
	if strings.TrimSpace(message) == "" {
		panic(errDeprecatedMessageEmpty(name))
	}
	if _, ok := a.commands[name]; ok {
		panic(errDeprecatedCollidesCommand(name))
	}
	if _, ok := a.groups[name]; ok {
		panic(errDeprecatedCollidesGroup(name))
	}
	if _, ok := a.deprecatedMap[name]; ok {
		panic(errDeprecatedAlreadyRegistered(name))
	}
	a.deprecated = append(a.deprecated, deprecatedCmd{Name: name, Message: message})
	a.deprecatedMap[name] = message
}

// Deprecated registers a deprecated subcommand on the group.
// Invoking a deprecated subcommand prints the message to stderr and exits 1.
func (g *Group) Deprecated(name, message string, opts ...CmdOption) {
	rejectDeprecatedEffect(name, opts)
	if strings.TrimSpace(name) == "" {
		panic(errDeprecatedNameEmpty)
	}
	if strings.TrimSpace(message) == "" {
		panic(errDeprecatedMessageEmpty(name))
	}
	if _, ok := g.Commands[name]; ok {
		panic(errDeprecatedCollidesCommand(name))
	}
	if _, ok := g.Groups[name]; ok {
		panic(errDeprecatedCollidesGroup(name))
	}
	if _, ok := g.deprecatedMap[name]; ok {
		panic(errDeprecatedAlreadyRegistered(name))
	}
	g.deprecated = append(g.deprecated, deprecatedCmd{Name: name, Message: message})
	g.deprecatedMap[name] = message
}

// Commands returns the registered top-level commands.
func (a *App) Commands() map[string]*Command {
	return a.commands
}

// Groups returns the registered command groups.
func (a *App) Groups() map[string]*Group {
	return a.groups
}

// GlobalFlags returns the registered global flags.
func (a *App) GlobalFlags() []Flag {
	return a.globalFlags
}

// DeprecatedCommands returns the deprecated command map (name -> message).
func (a *App) DeprecatedCommands() map[string]string {
	return a.deprecatedMap
}

// DeprecatedCommands returns the deprecated subcommand map (name -> message).
func (g *Group) DeprecatedCommands() map[string]string {
	return g.deprecatedMap
}

// Run executes the CLI, reading from os.Args.
func (a *App) Run() {
	if errMsg := a.validateCheckRegistrations(); errMsg != "" {
		fmt.Fprintln(os.Stderr, "error: "+errMsg)
		os.Exit(1)
	}
	if errMsg := a.validateTagContracts(); errMsg != "" {
		fmt.Fprintln(os.Stderr, "error: "+errMsg)
		os.Exit(1)
	}
	if errMsg := a.validateConfigFieldBindings(); errMsg != "" {
		fmt.Fprintln(os.Stderr, "error: "+errMsg)
		os.Exit(1)
	}
	argv := os.Args[1:]
	pr := a.doParse(argv)

	if pr.helpText != "" {
		fmt.Println(pr.helpText)
		os.Exit(0)
	}
	if pr.versionText != "" {
		fmt.Println(pr.versionText)
		os.Exit(0)
	}
	if pr.dumpSchema {
		path, err := writeSchema(a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
			os.Exit(1)
		}
		fmt.Println(path)
		os.Exit(0)
	}
	if pr.serveMCP {
		a.ServeMCP()
		os.Exit(0)
	}
	if pr.parseErr != "" {
		fmt.Fprintln(os.Stderr, "error: "+pr.parseErr)
		prefix := pr.commandPrefix
		if prefix == "" {
			prefix = a.Name
		}
		fmt.Fprintf(os.Stderr, "try '%s --help'\n", prefix)
		a.emitPreDispatchEnvelope(os.Stdout)
		os.Exit(1)
	}

	a.beginDispatch()
	reserved := a.reservedFlagState()

	ctx := a.newDispatchContext(os.Stdout, os.Stderr, pr, reserved)
	// The confirm protocol fires only on the real CLI path -- and a mutating
	// PASSTHROUGH is not exempt.
	a.confirmConsequential(pr.cmd, pr.cmdPath)
	code := a.runSealed(os.Stdout, os.Stderr, reserved.dryRun, pr.cmdPath, ctx, pr.cmd.OwnsStdout, func() int {
		if pr.cmd.Passthrough {
			return pr.cmd.PassthroughHandler(ctx, pr.cmd.Name, pr.passthroughArgs, pr.globalKwargs)
		}
		return pr.cmd.Handler(ctx, pr.kwargs).code
	})
	a.runExitHook()
	os.Exit(code)
}

// newDispatchContext builds the one Context a dispatch runs on, arming the
// runtime seal and carrying the command's payload declaration.
func (a *App) newDispatchContext(stdout, stderr io.Writer, pr parseResult, reserved reservedFlags) *Context {
	ctx := newContext(stdout, stderr, pr.sources, a.infraAccess(pr.hermetic),
		reserved, a.armEffects(pr.cmd, pr.cmdPath, reserved.dryRun, stdout))
	ctx.commandName = pr.cmd.Name
	ctx.payloadSchema = pr.cmd.PayloadSchema
	ctx.writes = pr.writes
	ctx.unsets = pr.unsets
	// The write set's human rendering: ONE unnumbered line between the log's
	// header and its first effect, in dry mode only (contract §3.2's amendment,
	// §27.5). It takes no sequence number -- the counter is contiguous over
	// rendered EFFECTS, and a write set is not one -- and a live run's write set
	// rides the envelope instead.
	if pr.writes != nil && reserved.dryRun && a.effects != nil {
		a.effects.writeSetLine = pr.writes.logLine()
	}
	// Every conditional binding this run did not consult is NAMED, one line per
	// binding, in declaration order, on the debug channel: hidden by default,
	// shown by --verbose, and carried in machine mode's diagnostics at level
	// "debug" whatever the human stream did (contract §24.6). Surfacing is what
	// keeps the rule inside the no-silent-degradation rule rather than beside
	// it.
	for _, line := range pr.skippedBindings {
		ctx.Debug(line)
	}
	return ctx
}

// reservedFlagState snapshots the framework-owned quartet for one dispatch.
func (a *App) reservedFlagState() reservedFlags {
	return reservedFlags{
		dryRun:               a.lastDryRun,
		approveConsequential: a.lastApproveConsequential,
		quiet:                a.lastQuiet,
		verbose:              a.lastVerbose,
		json:                 a.lastJSON,
	}
}

// runSealed runs a handler under the runtime seal AND owns the would-do log's
// rendering, so the log reaches stdout on every exit path out of the handler
// rather than only on the normal return:
//
//   - normal return -- the log, after whatever the handler emitted;
//   - a carrier extraction -- the truncation path (dryRunTruncation) prints the
//     already-recorded log to stdout and its own pinned error to stderr, and
//     exits 1;
//   - any other panic -- the log, then the aborted-preview marker on stderr,
//     and then the panic continues untouched.
//
// A handler that calls os.Exit is outside this guarantee and outside Go: the
// process is gone before any deferred function runs.
func (a *App) runSealed(stdout, stderr io.Writer, dryRun bool, cmdPath string, ctx *Context, ownsStdout bool, fn func() int) (code int) {
	defer func() {
		r := recover()
		if r == nil {
			a.finishDispatch(ctx, stdout, stderr, dryRun, cmdPath, code, nil, false, ownsStdout)
			return
		}
		if t, ok := r.(dryRunTruncation); ok {
			code = 1
			a.finishDispatch(ctx, stdout, stderr, dryRun, cmdPath, 1, &t, false, ownsStdout)
			return
		}
		// An unexpected unwind. The recorded effects are still owed to whoever
		// asked for the preview; the marker says the list may not be all of it.
		//
		// The envelope's exit_code is "the process's exit status" (§19.2), and
		// §3.5 pins what that status is on this path: the panic is not
		// swallowed, so it is "whatever the language would have produced
		// anyway". In Go an unrecovered panic exits 2, not 1 -- reporting 1
		// here would make the envelope contradict the process it describes.
		// The two siblings report 1 on the same path for the same reason:
		// an uncaught Python exception and an uncaught Node throw both exit 1.
		a.finishDispatch(ctx, stdout, stderr, dryRun, cmdPath, goPanicExitStatus, nil, true, ownsStdout)
		panic(r)
	}()
	return fn()
}

// emitPreDispatchEnvelope writes the envelope a run that ended before a command
// resolved still owes machine mode, with a null command (§19.2). A no-op
// outside machine mode.
//
// The parse error's own text stays on stderr: it does not go through the
// context writers, so it is not one of the diagnostics the envelope carries.
func (a *App) emitPreDispatchEnvelope(stdout io.Writer) {
	if !a.lastJSON {
		return
	}
	a.emitEnvelope(nil, stdout, nil, 1, a.lastDryRun, nil, nil)
}

// interfaceVersion is the envelope contract's own version (§19.2). Changed only
// by a later amendment to that section -- and §18.33 item 313 is one: the key
// set grew a `writes` member that is never absent, so a consumer validating the
// envelope's key set against version 1 must be able to tell which document it
// holds.
const interfaceVersion = 2

// goPanicExitStatus is the exit status the Go runtime gives a process whose
// panic was never recovered. It is the status §3.5 promises an aborted
// dispatch keeps ("whatever the language would have produced anyway"), and
// therefore the exit_code the envelope reports on that path (§19.2).
const goPanicExitStatus = 2

// envelope is machine mode's sole stdout document (§19.2). The field order is
// the table's order in that section: optional and for readability only, since
// correctness is decided by structural comparison.
type envelope struct {
	InterfaceVersion int         `json:"interface_version"`
	App              string      `json:"app"`
	AppVersion       string      `json:"app_version"`
	Command          *string     `json:"command"`
	ExitCode         int         `json:"exit_code"`
	Payload          interface{} `json:"payload"`
	DryRun           bool        `json:"dry_run"`
	// Writes is the write set of a command declaring update_of (§27.5), and
	// null on every command that declares none. NEVER absent, and populated in
	// BOTH modes, for preview's reason: it is a function of the declaration and
	// the invocation, not of the mode.
	Writes       *writesEnvelope          `json:"writes"`
	Preview      []map[string]interface{} `json:"preview"`
	PreviewError *previewError            `json:"preview_error"`
	Diagnostics  []diagnosticRecord       `json:"diagnostics"`
}

// previewError is the terminal condition of a preview that did not finish
// (§19.3). Brand is nil for an abort: §12.11's marker deliberately names no
// value.
type previewError struct {
	Kind    string  `json:"kind"`
	Step    int     `json:"step"`
	Command string  `json:"command"`
	Brand   *string `json:"brand"`
	Message string  `json:"message"`
}

// finishDispatch is the ONE ordered exit step. Reachable from every way out of
// a dispatch (a normal return, a truncated preview and an unwinding abort), so
// there is exactly one place that decides what the framework emits at the end
// of a run and in what order.
//
// In machine mode it emits the envelope INSTEAD of the human stream's would-do
// log, truncation error and abort marker: those texts become the envelope's
// preview and preview_error members (§19.1, §19.3), and stdout carries exactly
// one document.
func (a *App) finishDispatch(ctx *Context, stdout, stderr io.Writer, dryRun bool, cmdPath string, exitCode int, trunc *dryRunTruncation, aborted bool, ownsStdout bool) {
	if ctx != nil && ctx.reserved.json {
		// The emission seam owns instance validation (§19.4, §19.5): the value
		// is checked here, where the envelope is about to carry it, and nowhere
		// else. A human-mode run never reaches this line, so a payload the
		// envelope could not represent costs it nothing.
		validateEmittedPayload(ctx)
		// A command that declared stdout ownership keeps stdout for its own
		// document, and the envelope moves to stderr with the diagnostics it
		// carries (contract §19.6). Leaving it on stdout would re-create the
		// two-documents-on-one-stream collision §19.1 exists to remove.
		dest := stdout
		if ownsStdout {
			dest = stderr
		}
		a.emitEnvelope(ctx, dest, &cmdPath, exitCode, dryRun, a.EffectLog(),
			a.buildPreviewError(cmdPath, dryRun, trunc, aborted))
		return
	}
	if trunc != nil {
		// The truncation path ends the preview for its own pinned reason: it
		// renders the log it already has and its own error, and never goes
		// through the generic would-do rendering.
		if !trunc.log.seamSuppressed() {
			fmt.Fprintln(stdout, trunc.log.render())
		}
		fmt.Fprintln(stderr, trunc.message)
		return
	}
	if !dryRun {
		return
	}
	// A handler that claimed the render AND produced the bytes already has the
	// log in the stream; re-emitting it here would duplicate it. A claim that
	// never rendered falls through and is rendered (§19.7).
	if !a.effects.seamSuppressed() {
		fmt.Fprintln(stdout, a.renderWouldDoLog())
	}
	if aborted {
		fmt.Fprintln(stderr, errDryRunAborted(a.wouldDoSeq(), cmdPath))
	}
}

// validateEmittedPayload validates the payload the envelope is about to carry
// (§19.5). The schema check, JSON representability and the 2^53 magnitude guard
// all live here, at the one seam where the value becomes a document. A deviation
// fails the run rather than shipping a wrong shape.
func validateEmittedPayload(ctx *Context) {
	if !ctx.payloadSet {
		return
	}
	if f := validatePayloadValue(ctx.payload, ctx.payloadSchema); f != nil {
		panic(errPayloadInvalid(ctx.commandName, f.Path, f.Detail))
	}
}

// previewError builds the envelope's preview_error member (§19.3).
//
// The two terminal conditions are mutually exclusive by §3.5's table. Each
// carries the §12.5 / §12.11 text byte-identically rather than restating it, so
// there is one text per condition.
//
// The abort branch is dry-mode-only, exactly as the human stream's marker is:
// the message says "dry-run preview ends at step N", which is not a true
// sentence about a live run.
func (a *App) buildPreviewError(cmdPath string, dryRun bool, trunc *dryRunTruncation, aborted bool) *previewError {
	if trunc != nil {
		brand := trunc.brand
		return &previewError{
			Kind:    "truncated",
			Step:    trunc.step,
			Command: trunc.cmdPath,
			Brand:   &brand,
			Message: trunc.message,
		}
	}
	if aborted && dryRun {
		step := a.wouldDoSeq()
		return &previewError{
			Kind:    "aborted",
			Step:    step,
			Command: cmdPath,
			Brand:   nil,
			Message: errDryRunAborted(step, cmdPath),
		}
	}
	return nil
}

// emitEnvelope writes the envelope, machine mode's sole stdout document
// (§19.2).
//
// Field order follows §19.2's table (the struct's field order): optional and
// for readability only, since conformance compares parsed structures. The
// encoder disables HTML escaping so the bytes follow §19.5's regime -- plain
// UTF-8, escaping only what JSON mandates -- and the write does not go through
// the quiet-suppressible writers, so --quiet has no mechanism by which to
// reach it.
func (a *App) emitEnvelope(ctx *Context, stdout io.Writer, command *string, exitCode int, dryRun bool, preview []map[string]interface{}, prevErr *previewError) {
	if preview == nil {
		preview = []map[string]interface{}{}
	}
	env := envelope{
		InterfaceVersion: interfaceVersion,
		App:              a.Name,
		AppVersion:       a.Version,
		Command:          command,
		ExitCode:         exitCode,
		DryRun:           dryRun,
		Preview:          preview,
		PreviewError:     prevErr,
		Diagnostics:      []diagnosticRecord{},
	}
	if ctx != nil {
		if ctx.payloadSet {
			env.Payload = ctx.payload
		}
		if ctx.writes != nil {
			env.Writes = ctx.writes.envelopeMember()
		}
		if len(ctx.diagnostics) > 0 {
			env.Diagnostics = ctx.diagnostics
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to marshal the envelope: %s\n", err)
		return
	}
	// Encode already terminates with a newline.
	fmt.Fprint(stdout, buf.String())
}

// Test runs the CLI with the given argv, capturing output and exit code.
func (a *App) Test(argv []string) Result {
	if errMsg := a.validateCheckRegistrations(); errMsg != "" {
		return Result{Stderr: "error: " + errMsg + "\n", ExitCode: 1}
	}
	if errMsg := a.validateTagContracts(); errMsg != "" {
		return Result{Stderr: "error: " + errMsg + "\n", ExitCode: 1}
	}
	if errMsg := a.validateConfigFieldBindings(); errMsg != "" {
		return Result{Stderr: "error: " + errMsg + "\n", ExitCode: 1}
	}
	pr := a.doParse(argv)

	if pr.helpText != "" {
		return Result{Stdout: pr.helpText + "\n", ExitCode: 0}
	}
	if pr.versionText != "" {
		return Result{Stdout: pr.versionText + "\n", ExitCode: 0}
	}
	if pr.dumpSchema {
		path, err := writeSchema(a)
		if err != nil {
			return Result{Stderr: fmt.Sprintf("error: %s\n", err), ExitCode: 1}
		}
		return Result{Stdout: path + "\n", ExitCode: 0}
	}
	if pr.serveMCP {
		// In Test mode, MCP mode cannot be exercised (it requires stdin/stdout).
		// Use serveMCPIO directly for testing.
		return Result{Stderr: "error: --mcp cannot be used with Test(); use serveMCPIO directly\n", ExitCode: 1}
	}
	if pr.parseErr != "" {
		prefix := pr.commandPrefix
		if prefix == "" {
			prefix = a.Name
		}
		stderr := fmt.Sprintf("error: %s\ntry '%s --help'\n", pr.parseErr, prefix)
		var stdout bytes.Buffer
		a.emitPreDispatchEnvelope(&stdout)
		return Result{Stdout: stdout.String(), Stderr: stderr, ExitCode: 1}
	}

	a.beginDispatch()

	// Record test-coverage hit (command-level only).
	if a.coverageShardPath != "" && pr.cmdPath != "" {
		a.recordCoverage(pr.cmdPath)
	}

	// Capture stdout/stderr from handler
	oldStdout := os.Stdout
	oldStderr := os.Stderr

	stdoutR, stdoutW, _ := os.Pipe()
	stderrR, stderrW, _ := os.Pipe()
	os.Stdout = stdoutW
	os.Stderr = stderrW

	// Drain both pipes concurrently while the handler runs. A handler that
	// emits more than the OS pipe buffer (~64KB) would otherwise block on write
	// (nothing reading) or have its output truncated by a fixed-size read. Using
	// unbounded io.Copy into bytes.Buffer captures arbitrarily large output.
	var stdoutBuf, stderrBuf bytes.Buffer
	var drainWG sync.WaitGroup
	drainWG.Add(2)
	go func() { defer drainWG.Done(); io.Copy(&stdoutBuf, stdoutR) }()
	go func() { defer drainWG.Done(); io.Copy(&stderrBuf, stderrR) }()

	// Context is constructed unconditionally for every dispatch, writing to the
	// capture pipes. Test() behaves as if --approve-consequential were passed:
	// it never prompts.
	reserved := a.reservedFlagState()
	ctx := a.newDispatchContext(stdoutW, stderrW, pr, reserved)

	exitCode := a.runSealed(stdoutW, stderrW, reserved.dryRun, pr.cmdPath, ctx, pr.cmd.OwnsStdout, func() int {
		if pr.cmd.Passthrough {
			return pr.cmd.PassthroughHandler(ctx, pr.cmd.Name, pr.passthroughArgs, pr.globalKwargs)
		}
		return pr.cmd.Handler(ctx, pr.kwargs).code
	})
	resultData := ctx.payload

	stdoutW.Close()
	stderrW.Close()

	// Wait for both drain goroutines to finish consuming the pipes, then close
	// the read ends.
	drainWG.Wait()
	stdoutR.Close()
	stderrR.Close()

	os.Stdout = oldStdout
	os.Stderr = oldStderr

	return Result{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: exitCode,
		Data:     resultData,
	}
}

// parseResult holds the output of doParse.
type parseResult struct {
	cmd             *Command
	cmdPath         string // dot-separated command path (e.g. "infra.deploy")
	kwargs          map[string]interface{}
	globalKwargs    map[string]interface{}
	sources         map[string]string // flag param name -> source label
	passthroughArgs []string
	helpText        string
	versionText     string
	parseErr        string
	commandPrefix   string
	dumpSchema      bool
	serveMCP        bool
	hermetic        bool // --hermetic active for this invocation
	// writes is this invocation's write set (contract §27.5), nil on every
	// command that declares no update. unsets names the properties this
	// invocation CLEARED, which is what ctx.Unset answers off.
	writes *updateState
	unsets map[string]bool
	// skippedBindings names every conditional env/config binding this run did
	// NOT consult, because its scope was not elected (contract §24.6). They are
	// DIAGNOSTICS, emitted on the debug channel once the dispatch context
	// exists, so they are hidden by default, shown by --verbose, and carried in
	// machine mode's diagnostics at level "debug".
	skippedBindings []string
}

// preScanResult holds the results of the position-aware pre-scan for
// reserved flags (--dump-schema, --mcp, --config, --hermetic).
type preScanResult struct {
	dumpSchema  bool
	serveMCP    bool
	hermetic    bool          // --hermetic: skip config loading and env var resolution
	reserved    reservedFlags // the framework-owned quartet
	configPath  string        // value from --config <path> or --config=<path>
	err         string        // non-empty on error (e.g. missing value, config on disabled app)
	cleanedArgv []string      // argv with the reserved tokens stripped out
}

// reservedQuartetTokens maps an argv token to the preScanResult field it sets.
// The quartet -- plus --json, which reads exactly as the quartet does in both
// argv regions (§7.1's amendment, §7.2) -- is recognized ANYWHERE in argv,
// exactly like --help/-h: both `app --dry-run cmd` and `app cmd --dry-run`
// work. Contrast --hermetic, --config, --dump-schema and --mcp, which stay
// pre-command-only.
var reservedQuartetTokens = map[string]func(*reservedFlags){
	"--dry-run":               func(r *reservedFlags) { r.dryRun = true },
	"--approve-consequential": func(r *reservedFlags) { r.approveConsequential = true },
	"--quiet":                 func(r *reservedFlags) { r.quiet = true },
	"--verbose":               func(r *reservedFlags) { r.verbose = true },
	"--json":                  func(r *reservedFlags) { r.json = true },
}

// preScanReservedFlags scans argv for the framework-owned reserved flags.
// Two regions, two rulesets (contract §7.2, amended):
//
//   - The pre-command region -- before the first non-flag token, before a "--"
//     terminator -- recognizes every reserved flag. Known global flags and their
//     values are skipped so that a global flag value that happens to look like a
//     command name is not treated as one.
//   - The command region recognizes ONLY the quartet, anywhere, exactly like
//     --help/-h. --hermetic, --config, --dump-schema and --mcp stay
//     pre-command-only and become unknown-flag errors after the command token.
//     See scanCommandRegionQuartet.
func (a *App) preScanReservedFlags(argv []string) preScanResult {
	// Build a set of known global flag long-names and short-names,
	// along with whether they take a value (non-bool).
	type flagInfo struct {
		takesValue bool
	}
	knownFlags := make(map[string]*flagInfo)
	for i := range a.globalFlags {
		f := &a.globalFlags[i]
		knownFlags["--"+f.Name] = &flagInfo{takesValue: f.Type != TypeBool}
		if f.Short != "" {
			knownFlags["-"+f.Short] = &flagInfo{takesValue: f.Type != TypeBool}
		}
		if f.Type == TypeBool && f.Negatable {
			knownFlags["--no-"+f.Name] = &flagInfo{takesValue: false}
		}
	}

	var result preScanResult
	// Track indices to exclude from cleanedArgv (--config tokens)
	excludeIndices := make(map[int]bool)
	// Index where the command region begins; -1 means "never reached one"
	// (a bare -- or an unknown flag-like token ended the scan for good).
	commandRegionFrom := -1
	i := 0
	for i < len(argv) {
		tok := argv[i]

		// -- terminates the whole scan: everything after it is data
		if tok == "--" {
			break
		}

		// Non-flag token = the command token: the command region starts here
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			commandRegionFrom = i
			break
		}

		// --dump-schema
		if tok == "--dump-schema" {
			result.dumpSchema = true
			return result
		}

		// --mcp
		if tok == "--mcp" {
			result.serveMCP = true
			return result
		}

		// --hermetic (boolean, no value)
		if tok == "--hermetic" {
			result.hermetic = true
			excludeIndices[i] = true
			i++
			continue
		}

		// The reserved quartet: booleans, no values, stripped from argv and
		// delivered on the Context (never as handler kwargs).
		if set, ok := reservedQuartetTokens[tok]; ok {
			set(&result.reserved)
			excludeIndices[i] = true
			i++
			continue
		}

		// --config=<value>
		if strings.HasPrefix(tok, "--config=") {
			if !a.configEnabled {
				result.err = "--config is not available: this app does not use config files"
				return result
			}
			val := tok[len("--config="):]
			if val == "" {
				result.err = errFlagRequiresValue("--config")
				return result
			}
			result.configPath = val
			excludeIndices[i] = true
			i++
			continue
		}

		// --config <value>
		if tok == "--config" {
			if !a.configEnabled {
				result.err = "--config is not available: this app does not use config files"
				return result
			}
			if i+1 >= len(argv) {
				result.err = errFlagRequiresValue("--config")
				return result
			}
			result.configPath = argv[i+1]
			excludeIndices[i] = true
			excludeIndices[i+1] = true
			i += 2
			continue
		}

		// Known global flag with --flag=value form: skip
		if strings.HasPrefix(tok, "--") && strings.Contains(tok, "=") {
			eqPos := strings.Index(tok, "=")
			flagPart := tok[:eqPos]
			if _, ok := knownFlags[flagPart]; ok {
				i++
				continue
			}
			// Unknown flag-like token before command name: stop
			break
		}

		// Known global flag: skip it (and its value if non-bool)
		if info, ok := knownFlags[tok]; ok {
			if info.takesValue {
				i += 2
			} else {
				i++
			}
			continue
		}

		// Unknown flag-like token before command name: stop
		break
	}

	if commandRegionFrom >= 0 {
		a.scanCommandRegionQuartet(argv, commandRegionFrom, &result.reserved, excludeIndices)
	}

	// Build cleaned argv with --config tokens stripped
	if len(excludeIndices) > 0 {
		cleaned := make([]string, 0, len(argv)-len(excludeIndices))
		for j, tok := range argv {
			if !excludeIndices[j] {
				cleaned = append(cleaned, tok)
			}
		}
		result.cleanedArgv = cleaned
	} else {
		result.cleanedArgv = argv
	}

	return result
}

// scanCommandRegionQuartet recognizes the reserved quartet in the command
// region of argv.
//
// Contract §7.2 (amended 2026-08-04): --dry-run/--yes/--quiet/--verbose are
// recognized ANYWHERE in argv, exactly like --help/-h, because their
// applicability is per-command -- requiring them before the command name was
// backwards. Only the quartet is recognized here; --hermetic, --config,
// --dump-schema and --mcp remain pre-command-only.
//
// The scan stops for good at two boundaries:
//
//   - a bare "--", after which every token is positional data;
//   - a passthrough command's name, after which every token belongs to the
//     child process and is forwarded byte-for-byte. Eating a child's own
//     --verbose would silently change what the child does.
//
// Routing tokens are walked through the group/command tree so a quartet token
// may sit anywhere among them. Nothing here errors: routing failures are the
// real parse's job.
//
// Both boundaries are visible in the dry_run_supported=false refusal, which
// reads the flag this scan resolved. `app cmd -- --dry-run` and
// `app passthrough --dry-run` are NOT refused, because in neither case did the
// operator ask this app for a dry run: after `--` the token is the command's
// own data, and after a passthrough's name it is the child process's flag.
// `app --dry-run passthrough` IS refused -- there the token is unambiguously
// addressed to this app.
func (a *App) scanCommandRegionQuartet(
	argv []string,
	start int,
	reserved *reservedFlags,
	excludeIndices map[int]bool,
) {
	groups := a.groups
	commands := a.commands
	routingDone := false
	for i := start; i < len(argv); i++ {
		tok := argv[i]

		if tok == "--" {
			return
		}

		if strings.HasPrefix(tok, "-") && tok != "-" {
			if set, ok := reservedQuartetTokens[tok]; ok {
				set(reserved)
				excludeIndices[i] = true
			}
			continue
		}

		// A non-flag token: a routing token until routing resolves.
		if !routingDone {
			if grp, ok := groups[tok]; ok {
				groups = grp.Groups
				commands = grp.Commands
				continue
			}
			if cmd, ok := commands[tok]; ok && cmd.Passthrough {
				return
			}
			// Resolved a normal command, or hit an unknown/deprecated token the
			// real parse will report: routing is over either way.
			routingDone = true
		}
	}
}

// doParse parses argv and returns a parseResult.
// Exactly one of: (cmd+kwargs), helpText, versionText, or parseErr will be non-zero.
func (a *App) doParse(argv []string) parseResult {
	// Reset stdin tracking for each parse invocation
	a.stdinConsumedBy = nil

	// Machine mode is not known until the pre-scan below runs, so the flag
	// starts false on every parse: a stale value from an earlier run must
	// never decide what this one emits.
	a.lastJSON = false

	// App-level --help/-h and --version/-v (no global flags present)
	if len(argv) == 0 || (len(argv) == 1 && (argv[0] == "--help" || argv[0] == "-h")) {
		return parseResult{helpText: formatAppHelp(a)}
	}
	if len(argv) == 1 && (argv[0] == "--version" || argv[0] == "-v") {
		return parseResult{versionText: formatVersion(a)}
	}

	// Position-aware pre-scan: intercept --dump-schema, --mcp, --config, --hermetic
	// in the pre-command region only (before the first non-flag token, before --).
	// This replaces the old naive scans that checked ALL of argv.
	preScan := a.preScanReservedFlags(argv)

	// Record the reserved quartet -- and --json beside it -- for the dispatch
	// ctx. This runs BEFORE the pre-scan's own exits so every parse error from
	// here on knows whether the run is in machine mode and can emit the
	// envelope the mode owes it (§19.2).
	a.lastDryRun = preScan.reserved.dryRun
	a.lastApproveConsequential = preScan.reserved.approveConsequential
	a.lastQuiet = preScan.reserved.quiet
	a.lastVerbose = preScan.reserved.verbose
	a.lastJSON = preScan.reserved.json

	if preScan.dumpSchema {
		return parseResult{dumpSchema: true}
	}
	if preScan.serveMCP {
		return parseResult{serveMCP: true}
	}
	if preScan.err != "" {
		return parseResult{parseErr: preScan.err}
	}

	// --hermetic + --config mutual exclusion
	if preScan.hermetic && preScan.configPath != "" {
		return parseResult{parseErr: errHermeticConfigMutuallyExclusive}
	}

	// Load config data once at parse time.
	// When hermetic is active, skip config loading entirely (even XDG defaults).
	// Capture any parse error to handle config subcommand exemption later.
	var configLoadErr string
	if a.configEnabled && !preScan.hermetic {
		runtimeOverride := preScan.configPath
		hermetic := a.noDefaultConfigPath && runtimeOverride == ""
		isRuntimeFlag := runtimeOverride != ""
		result := a.resolveConfigData(runtimeOverride, hermetic, isRuntimeFlag)
		if result.parseErr != "" {
			configLoadErr = result.parseErr
			a.configData = map[string]interface{}{}
		} else {
			a.configData = result.data
		}
	} else if preScan.hermetic {
		// Hermetic mode: no config data at all
		a.configData = nil
	}

	// Extract global flags from cleaned argv (--config/--hermetic stripped), leaving
	// the rest for command routing. Pass hermetic flag to skip env resolution.
	globalValues, globalSourceMap, rest, globalErr := a.extractGlobalFlags(preScan.cleanedArgv, preScan.hermetic)
	if globalErr != "" {
		return parseResult{parseErr: globalErr}
	}

	// If global flag parsing stopped at --, strip it before routing
	if len(rest) > 0 && rest[0] == "--" {
		rest = rest[1:]
	}

	// After extracting globals, check for help/version again
	if len(rest) == 0 || (len(rest) == 1 && (rest[0] == "--help" || rest[0] == "-h")) {
		return parseResult{helpText: formatAppHelp(a)}
	}
	if len(rest) == 1 && (rest[0] == "--version" || rest[0] == "-v") {
		return parseResult{versionText: formatVersion(a)}
	}

	// Route through the group/command tree
	route := a.resolveCommand(rest)

	// Handle routing errors (deprecated, unknown, no command)
	if route.err != "" {
		return parseResult{parseErr: route.err, commandPrefix: route.commandPrefix}
	}

	// Handle help at group level
	if route.helpAtGroup {
		return parseResult{helpText: formatGroupHelp(a, route.lastGroup, route.path)}
	}

	// Command was resolved — handle help, passthrough, and parsing
	cmd := route.cmd
	cmdRest := route.rest
	path := route.path

	// Build dotted command path for coverage and other instrumentation
	resolvedCmdPath := strings.Join(append(path, cmd.Name), ".")

	// Check command-level --help anywhere in remaining tokens
	if tokensContainHelp(cmdRest) {
		prefix := ""
		if len(path) > 0 {
			prefix = strings.Join(path, " ") + " "
		}
		return parseResult{helpText: formatCommandHelp(a, cmd, prefix)}
	}

	// A command that declares dry_run_supported=false refuses --dry-run here,
	// on every argv path (Run/Test/harness) at once, and AFTER the
	// command-help check above so --help always beats the refusal: asking what
	// a command does must never be answered with a refusal to preview it.
	// preScan.reserved.dryRun covers both `app --dry-run cmd` and
	// `app cmd --dry-run`; see scanCommandRegionQuartet for the two boundaries
	// that make a trailing --dry-run invisible here (a bare `--`, and a
	// passthrough command's name).
	if preScan.reserved.dryRun && !cmd.DryRunSupported {
		return parseResult{
			parseErr: errDryRunNotSupported(resolvedCmdPath, cmd.DryRunUnsupportedReason),
		}
	}

	// Passthrough: skip parsing, forward raw args
	if cmd.Passthrough {
		return parseResult{cmd: cmd, cmdPath: resolvedCmdPath, passthroughArgs: cmdRest, globalKwargs: globalValues, hermetic: preScan.hermetic}
	}

	// Config subcommand exemption: config edit, config path, config set
	// are exempt from config load errors (self-lock prevention).
	// config show handles the error specially (shows it as output).
	isConfigSubcommand := a.configEnabled && len(path) > 0 && path[0] == "config"

	// --hermetic + config subcommand = hard error
	if preScan.hermetic && isConfigSubcommand {
		return parseResult{parseErr: errHermeticWithConfigCommands}
	}

	if configLoadErr != "" {
		if !isConfigSubcommand {
			// Non-config command: hard error
			return parseResult{parseErr: configLoadErr}
		}
		// Config subcommand: only config show needs special handling.
		// edit, path, set, init are exempt (they work on broken configs).
		// config show is handled by the config show handler itself,
		// which calls resolveConfigData independently. We store the
		// error on the app for config show to pick up.
		a.configParseErr = configLoadErr
	}

	// Validate config fields for non-config subcommands
	if a.configEnabled && !isConfigSubcommand {
		if len(cmd.configFields) > 0 {
			if errMsg := a.validateBoundConfigFields(cmd, a.configData); errMsg != "" {
				return parseResult{parseErr: errMsg}
			}
		}
		if len(a.configFields) > 0 {
			if errMsg := a.validateUnknownConfigKeys(a.configData); errMsg != "" {
				return parseResult{parseErr: errMsg}
			}
		}
	}

	kwargs, postGlobalValues, cmdSources, writes, unsets, err, skipped := parseCommand(cmd, cmdRest, a.globalFlags, a.configData, &a.stdinConsumedBy, a.configConflictMode, preScan.hermetic, a.infraRoots)
	if err != "" {
		parts := append([]string{a.Name}, path...)
		parts = append(parts, cmd.Name)
		return parseResult{parseErr: err, commandPrefix: strings.Join(parts, " ")}
	}
	// Merge global values: post-command globals override pre-command ones
	for k, v := range postGlobalValues {
		globalValues[k] = v
	}
	for k, v := range globalValues {
		kwargs[k] = v
	}
	// Merge global sources into command sources. This mirrors the VALUE merge
	// above: for a global set post-command, parseCommand already placed the
	// correct (cli) source into cmdSources, so the pre-command source label
	// (typically "default") must NOT overwrite it.
	for k, v := range globalSourceMap {
		if _, isPost := postGlobalValues[k]; isPost {
			continue // post-command position wins
		}
		cmdSources[k] = v
	}
	return parseResult{cmd: cmd, cmdPath: resolvedCmdPath, kwargs: kwargs, globalKwargs: globalValues, sources: cmdSources, hermetic: preScan.hermetic, skippedBindings: skipped, writes: writes, unsets: unsets}
}

// tokensContainHelp checks if --help or -h appears in tokens before any "--"
// separator. Tokens after "--" are literal arguments and should not trigger help.
func tokensContainHelp(tokens []string) bool {
	for _, tok := range tokens {
		if tok == "--" {
			return false
		}
		if tok == "--help" || tok == "-h" {
			return true
		}
	}
	return false
}

// extractGlobalFlags scans argv for global flag tokens that appear before the
// command name.  It stops at the first non-flag token (the command name) or at
// "--", returning everything from that point onward as remaining tokens.
// This matches Python's _parse_global_flags behavior.  Global flags appearing
// after the command name are handled by parseCommand instead.
// When hermetic is true, env var and config resolution are skipped entirely.
// Returns (globalValues map, globalSources map, remaining argv, error string).
func (a *App) extractGlobalFlags(argv []string, hermetic bool) (map[string]interface{}, map[string]string, []string, string) {
	globalValues := make(map[string]interface{})
	globalSources := make(map[string]string)
	if len(a.globalFlags) == 0 {
		return globalValues, globalSources, argv, ""
	}

	// Build lookup maps for global flags
	longLookup := make(map[string]*Flag)
	shortLookup := make(map[string]*Flag)
	negationLookup := make(map[string]*Flag)
	for i := range a.globalFlags {
		f := &a.globalFlags[i]
		longLookup["--"+f.Name] = f
		if f.Short != "" {
			shortLookup["-"+f.Short] = f
		}
		if f.Type == TypeBool && f.Negatable {
			negationLookup["--no-"+f.Name] = f
		}
	}

	// Global flags are unconditional, so the per-flag CLI map the command path
	// uses is folded back onto names as soon as parsing finishes.
	globalByFlag := make(map[*Flag]interface{})
	flushGlobals := func() {
		for i := range a.globalFlags {
			if v, ok := globalByFlag[&a.globalFlags[i]]; ok {
				globalValues[a.globalFlags[i].Name] = v
			}
		}
	}

	i := 0
	for i < len(argv) {
		tok := argv[i]

		// -- stops global flag parsing; include it and the rest in remaining
		if tok == "--" {
			break
		}

		// Non-flag token (command name): stop and return the rest
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			break
		}

		// --flag=value form for global flags
		if strings.HasPrefix(tok, "--") && strings.Contains(tok, "=") {
			eqPos := strings.Index(tok, "=")
			flagPart := tok[:eqPos]
			valuePart := tok[eqPos+1:]
			if f, ok := longLookup[flagPart]; ok {
				if f.Type == TypeBool {
					return nil, nil, nil, errBoolFlagNoValue(flagPart)
				}
				if errStr := parseFlagRawValue(f, valuePart, globalByFlag, &a.stdinConsumedBy); errStr != "" {
					return nil, nil, nil, errStr
				}
				i++
				continue
			}
			// Not a global flag -- stop (command region)
			break
		}

		// --no-flag negation for global bool flags
		if f, ok := negationLookup[tok]; ok {
			globalValues[f.Name] = false
			i++
			continue
		}

		// --flag (long form)
		if f, ok := longLookup[tok]; ok {
			if f.Type == TypeBool {
				globalValues[f.Name] = true
				i++
			} else {
				if i+1 >= len(argv) {
					return nil, nil, nil, errFlagRequiresValue(tok)
				}
				if errStr := parseFlagRawValue(f, argv[i+1], globalByFlag, &a.stdinConsumedBy); errStr != "" {
					return nil, nil, nil, errStr
				}
				i += 2
			}
			continue
		}

		// -x (short form)
		if f, ok := shortLookup[tok]; ok {
			if f.Type == TypeBool {
				globalValues[f.Name] = true
				i++
			} else {
				if i+1 >= len(argv) {
					return nil, nil, nil, errFlagRequiresValue(tok)
				}
				if errStr := parseFlagRawValue(f, argv[i+1], globalByFlag, &a.stdinConsumedBy); errStr != "" {
					return nil, nil, nil, errStr
				}
				i += 2
			}
			continue
		}

		// Unknown flag-like token before command name: stop (let command parser handle it)
		break
	}

	remaining := argv[i:]
	flushGlobals()

	// All values set in the CLI loop above are SourceCLI.
	// Mark them now before env/config/default layers add more.
	for k := range globalValues {
		globalSources[flagParamName(k)] = "cli"
	}

	// Resolve env vars for global flags not set by CLI (skipped under --hermetic)
	if !hermetic {
		for i := range a.globalFlags {
			f := &a.globalFlags[i]
			if _, ok := globalValues[f.Name]; ok {
				continue
			}
			if f.Env != "" {
				envVal, ok := os.LookupEnv(f.Env)
				if ok {
					// Compound types: dict parses JSON from env, list uses env_separator
					if IsDictType(f.Type) {
						entries, errStr := parseDictEnvValue(f.Name, envVal, ItemType(f.Type))
						if errStr != "" {
							return nil, nil, nil, errWrappedFromEnvVar(errStr, f.Env)
						}
						globalValues[f.Name] = entries
						globalSources[flagParamName(f.Name)] = "env"
						continue
					}
					if IsListType(f.Type) {
						if f.EnvSeparator == "" {
							return nil, nil, nil, errListFlagEnvRequiresSeparator(f.Name)
						}
						parts := splitEscaped(envVal, f.EnvSeparator[0])
						elemType := ItemType(f.Type)
						coercedList := make([]interface{}, 0, len(parts))
						for _, element := range parts {
							val, errStr := coerceToScalar(f.Name, element, elemType, nil)
							if errStr != "" {
								return nil, nil, nil, errWrappedFromEnvVar(errStr, f.Env)
							}
							coercedList = append(coercedList, val)
						}
						if f.Unique {
							if dup := findDuplicate(coercedList); dup != nil {
								return nil, nil, nil, errFlagDuplicateValueFromEnv(f.Name, formatValueForError(dup), f.Env)
							}
						}
						globalValues[f.Name] = coercedList
						globalSources[flagParamName(f.Name)] = "env"
						continue
					}
					switch f.Type {
					case TypeBool:
						boolVal, err := parseBoolStrict(envVal)
						if err != nil {
							return nil, nil, nil, errInvalidBoolEnvValue(envVal, f.Env, f.Name)
						}
						globalValues[f.Name] = boolVal
					case TypeInt:
						if f.Repeatable && f.EnvSeparator != "" {
							parts := splitEscaped(envVal, f.EnvSeparator[0])
							coercedList := make([]interface{}, 0, len(parts))
							for _, element := range parts {
								intVal, err := parseIntStrict(element)
								if err != nil {
									return nil, nil, nil, errFlagErrFromEnvVar(f.Name, err.Error(), f.Env)
								}
								coercedList = append(coercedList, intVal)
							}
							if f.Unique {
								if dup := findDuplicate(coercedList); dup != nil {
									return nil, nil, nil, errFlagDuplicateValueFromEnv(f.Name, formatValueForError(dup), f.Env)
								}
							}
							globalValues[f.Name] = coercedList
						} else {
							intVal, err := parseIntStrict(envVal)
							if err != nil {
								return nil, nil, nil, errFlagErrFromEnvVar(f.Name, err.Error(), f.Env)
							}
							if f.Repeatable {
								globalValues[f.Name] = []interface{}{intVal}
							} else {
								globalValues[f.Name] = intVal
							}
						}
					case TypeFloat:
						if f.Repeatable && f.EnvSeparator != "" {
							parts := splitEscaped(envVal, f.EnvSeparator[0])
							coercedList := make([]interface{}, 0, len(parts))
							for _, element := range parts {
								floatVal, errStr := parseFloatStrict(f.Name, element)
								if errStr != "" {
									return nil, nil, nil, errWrappedFromEnvVar(errStr, f.Env)
								}
								coercedList = append(coercedList, floatVal)
							}
							if f.Unique {
								if dup := findDuplicate(coercedList); dup != nil {
									return nil, nil, nil, errFlagDuplicateValueFromEnv(f.Name, formatValueForError(dup), f.Env)
								}
							}
							globalValues[f.Name] = coercedList
						} else {
							floatVal, errStr := parseFloatStrict(f.Name, envVal)
							if errStr != "" {
								return nil, nil, nil, errWrappedFromEnvVar(errStr, f.Env)
							}
							if f.Repeatable {
								globalValues[f.Name] = []interface{}{floatVal}
							} else {
								globalValues[f.Name] = floatVal
							}
						}
					default:
						if f.Repeatable && f.EnvSeparator != "" {
							parts := splitEscaped(envVal, f.EnvSeparator[0])
							coercedList := make([]interface{}, 0, len(parts))
							for _, element := range parts {
								resolved, errStr := resolveAtPrefix(f.Name, element, &a.stdinConsumedBy)
								if errStr != "" {
									return nil, nil, nil, errStr
								}
								coercedList = append(coercedList, resolved)
							}
							if f.Unique {
								if dup := findDuplicate(coercedList); dup != nil {
									return nil, nil, nil, errFlagDuplicateValueFromEnv(f.Name, formatValueForError(dup), f.Env)
								}
							}
							globalValues[f.Name] = coercedList
						} else {
							resolved, errStr := resolveAtPrefix(f.Name, envVal, &a.stdinConsumedBy)
							if errStr != "" {
								return nil, nil, nil, errStr
							}
							if f.Repeatable {
								globalValues[f.Name] = []interface{}{resolved}
							} else {
								globalValues[f.Name] = resolved
							}
						}
					}
					globalSources[flagParamName(f.Name)] = "env"
					continue
				}
			}
		}

		// Resolve config values for global flags not set by CLI or env.
		// In conflict mode "error", detect when config would set a flag
		// already set by CLI or env.
		if a.configData != nil {
			for i := range a.globalFlags {
				f := &a.globalFlags[i]
				param := flagParamName(f.Name)
				configVal, hasConfig := a.configData[param]
				if !hasConfig {
					continue
				}
				// Effective mode: per-flag override if set, else the app default.
				effectiveMode := a.configConflictMode
				if f.hasConflictMode {
					effectiveMode = f.ConflictMode
				}
				if existing, alreadySet := globalValues[f.Name]; alreadySet {
					// Conflict ONLY when config diverges from the CLI/env value.
					if effectiveMode == "error" {
						coerced, errStr := coerceConfigValue(configVal, f)
						if errStr != "" {
							return nil, nil, nil, errConfigValueError(f.Name, errStr)
						}
						if !valuesEqualForConflict(existing, coerced, f) {
							existingSource := globalSources[param]
							return nil, nil, nil, errFlagSetInBothAndConfig(f.Name, existingSource)
						}
					}
					continue // cli-wins, or error mode with matching values
				}
				coerced, errStr := coerceConfigValue(configVal, f)
				if errStr != "" {
					return nil, nil, nil, errConfigValueError(f.Name, errStr)
				}
				if f.Unique {
					if arr, ok := coerced.([]interface{}); ok {
						if dup := findDuplicate(arr); dup != nil {
							return nil, nil, nil, errConfigValueDuplicate(f.Name, formatValueForError(dup))
						}
					}
				}
				globalValues[f.Name] = coerced
				globalSources[flagParamName(f.Name)] = "config"
			}
		}
	} // end if !hermetic

	// Apply defaults for global flags not set
	for i := range a.globalFlags {
		f := &a.globalFlags[i]
		if _, ok := globalValues[f.Name]; ok {
			continue
		}
		val, src, errMsg := applyFlagDefault(f, "global ", a.infraRoots)
		if errMsg != "" {
			return nil, nil, nil, errMsg
		}
		globalValues[f.Name] = val
		globalSources[flagParamName(f.Name)] = sourceLabelString(src)
	}

	// Validate choices for global flags
	for i := range a.globalFlags {
		f := &a.globalFlags[i]
		val, ok := globalValues[f.Name]
		if !ok {
			continue
		}
		if errMsg := validateChoices(f.Name, val, f.Repeatable, f.Choices, f.retiredChoices, false); errMsg != "" {
			return nil, nil, nil, errMsg
		}
	}

	// Convert to param-name keys
	result := make(map[string]interface{})
	for k, v := range globalValues {
		result[flagParamName(k)] = v
	}

	return result, globalSources, remaining, ""
}

// buildAndValidateCommand creates and validates a Command.
func buildAndValidateCommand(name, help string, handler func(ctx *Context, kwargs map[string]interface{}) Outcome, envPrefix string, globalFlags []Flag, inheritedTags []string, opts []CmdOption) *Command {
	if strings.TrimSpace(help) == "" {
		panic(errCommandMissingHelp(name))
	}

	cmd := &Command{
		Name:    name,
		Help:    help,
		Handler: handler,
		// The regime's baseline; WithDryRunUnsupported is the only opt-out.
		DryRunSupported: true,
	}
	for _, opt := range opts {
		opt(cmd)
	}

	// Classification is MANDATORY and nothing is inferred. Passthrough commands
	// are classified the same way, through the same scheme, so this runs before
	// the passthrough early-return below.
	if !cmd.effectSet {
		panic(errCommandEffectMissing(name))
	}
	if cmd.Effect != EffectReadOnly && cmd.Effect != EffectMutating {
		panic(errCommandEffectInvalid(name, cmd.Effect))
	}
	// A read_only command cannot be consequential: it changes nothing, so
	// there is nothing to interrupt anyone for (contract §8.1).
	if cmd.Consequential && cmd.Effect == EffectReadOnly {
		panic(errCommandReadOnlyConsequential(name))
	}
	// The dry-run declaration mirrors the guard above: illegal on read_only,
	// the reason it exists to carry is mandatory, and a reason without the
	// declaration is rejected. WithDryRunUnsupported always sets both fields,
	// but Command's fields are exported and CmdOption is a plain
	// func(*Command), so the orphan state is reachable -- and silently
	// ignoring it would tell an author --dry-run is refused when it is not.
	if !cmd.DryRunSupported {
		if cmd.Effect == EffectReadOnly {
			panic(errCommandReadOnlyDryRunUnsupported(name))
		}
		if strings.TrimSpace(cmd.DryRunUnsupportedReason) == "" {
			panic(errCommandDryRunReasonMissing(name))
		}
	} else if cmd.DryRunUnsupportedReason != "" {
		panic(errCommandDryRunReasonWithoutDeclaration(name))
	}
	// The declared payload schema is validated as written, over the closed
	// subset (§19.5). An unknown keyword anywhere in the literal is a hard
	// error here, which is what keeps the subset closed by construction.
	if cmd.PayloadSchema != nil {
		if f := validatePayloadSchemaLiteral(cmd.PayloadSchema); f != nil {
			panic(errPayloadSchemaInvalid(name, f.Path, f.Detail))
		}
	}
	cmd.Grants = validateGrants(name, cmd.Grants)
	if cmd.Forwarding != nil && strings.TrimSpace(cmd.Forwarding.Reason) == "" {
		panic(errForwardingReasonEmpty(name))
	}
	// The framework-internal marker is unreachable from any public option. When
	// it is set, verify the handler really is defined in this package, so a
	// consumer that reaches the marker by any route fails loudly here rather
	// than silently inheriting a framework exemption.
	if cmd.frameworkInternal {
		var h interface{} = handler
		if cmd.Passthrough {
			h = cmd.PassthroughHandler
		}
		if !handlerIsFrameworkDefined(h) {
			panic(errFrameworkInternalHandlerForeign(name))
		}
	}

	// Passthrough commands cannot have flags, args or flag sets
	if cmd.Passthrough {
		if len(cmd.flags) > 0 || len(cmd.args) > 0 || len(cmd.flagSets) > 0 {
			var parts []string
			if len(cmd.flags) > 0 {
				parts = append(parts, "flags")
			}
			if len(cmd.args) > 0 {
				parts = append(parts, "args")
			}
			if len(cmd.flagSets) > 0 {
				parts = append(parts, "flag sets")
			}
			panic(errCommandPassthroughCannotHave(name, strings.Join(parts, ", ")))
		}
		cmd.tags = mergeTags(inheritedTags, cmd.tags)
		return cmd
	}

	// Presence is mandatory on every flag and every arg (contract §23.1). The
	// constructors resolve it, so this pass is what catches a Flag/Arg STRUCT
	// LITERAL: it passes through no option, declares no presence, and does not
	// register -- which is also what closes the trap where an exported Default
	// field set on a literal left hasDefault false and was silently ignored.
	for i := range cmd.flags {
		resolveFlagPresence(&cmd.flags[i])
	}
	for i := range cmd.flagSets {
		for j := range cmd.flagSets[i].Flags {
			resolveFlagPresence(&cmd.flagSets[i].Flags[j])
		}
	}
	for i := range cmd.args {
		resolveArgPresence(&cmd.args[i])
	}

	// Merge flag set flags into a unified all-flags list for validation
	allFlags := make([]Flag, 0, len(cmd.flags))
	allFlags = append(allFlags, cmd.flags...)
	for _, fs := range cmd.flagSets {
		allFlags = append(allFlags, fs.Flags...)
	}

	// The scope tree is indexed here, over the FINAL flag slice: every site
	// holds a pointer into it, and cmd.flags is assigned the same backing array
	// below. Indexing it is also what makes the whole-command rules checkable --
	// root-versus-scoped collisions, simultaneously-electable name and short
	// reuse, and the sibling shape rule at any depth (§24.7).
	cmd.index = buildFlagIndex(allFlags)
	validateCommandScopes(name, cmd.index)

	// Check duplicate flag names and collisions with global flags
	globalFlagSet := make(map[string]bool)
	for _, gf := range globalFlags {
		globalFlagSet[gf.Name] = true
	}
	seenFlags := make(map[string]bool)
	for _, f := range allFlags {
		if globalFlagSet[f.Name] {
			panic(errCommandFlagCollidesGlobal(name, f.Name))
		}
		if seenFlags[f.Name] {
			panic(errCommandDuplicateFlag(name, f.Name))
		}
		seenFlags[f.Name] = true
	}

	// Check duplicate arg names
	seenArgs := make(map[string]bool)
	for _, a := range cmd.args {
		if seenArgs[a.Name] {
			panic(errCommandDuplicateArg(name, a.Name))
		}
		seenArgs[a.Name] = true
	}

	// Validate variadic args: first check count, then check position
	variadicCount := 0
	for _, a := range cmd.args {
		if a.IsVariadic {
			variadicCount++
		}
	}
	if variadicCount > 1 {
		panic(errCommandAtMostOneVariadic(name))
	}
	for i, a := range cmd.args {
		if a.IsVariadic && i != len(cmd.args)-1 {
			panic(errCommandVariadicMustBeLast(name, a.Name))
		}
	}

	// Validate flag help
	for _, f := range allFlags {
		if strings.TrimSpace(f.Help) == "" {
			panic(errCommandFlagMissingHelp(name, f.Name))
		}
	}

	// Validate env prefix
	if envPrefix != "" {
		expectedPrefix := envPrefix + "_"
		for _, f := range allFlags {
			if f.Env != "" && f.Prefixed {
				if !strings.HasPrefix(f.Env, expectedPrefix) {
					panic(errCommandEnvVarPrefix(name, f.Env, f.Name, expectedPrefix))
				}
			}
		}
	}

	// Store the resolved allFlags on the command for parsing. The constraint
	// passes below resolve member names against it and against cmd.args.
	cmd.flags = allFlags

	// The update declaration, in §27.11's pinned eight-step order. It runs
	// BEFORE the constraint passes because its first step is the
	// mutating-default ban, which is a fact about the command's own
	// classification and is independent of every rule declared on top of it.
	validateUpdate(name, cmd, globalFlags)

	// Validate the constraint set, in §26.8's pinned pass order -- including the
	// trailing phase carrying the two dependency families' own guards.
	validateConstraints(name, cmd)

	cmd.tags = mergeTags(inheritedTags, cmd.tags)

	return cmd
}

// withFrameworkInternal sets the private framework-internal marker. It is
// UNEXPORTED on purpose: there is no public factory, option or spec that can
// reach it, so only strictcli's own registration paths can claim the
// declared-forwarding exemption -- and even they are verified (§10.4).
func withFrameworkInternal() CmdOption {
	return func(c *Command) {
		c.frameworkInternal = true
	}
}

// strictcliPkgPrefix is this package's runtime function-name prefix, derived
// from a function that is by construction defined here.
var strictcliPkgPrefix = func() string {
	name := runtime.FuncForPC(reflect.ValueOf(newEffects).Pointer()).Name()
	slash := strings.LastIndex(name, "/")
	dot := strings.Index(name[slash+1:], ".")
	if dot < 0 {
		return ""
	}
	return name[:slash+1+dot+1]
}()

// handlerIsFrameworkDefined reports whether a handler's function pointer
// resolves into the strictcli package.
func handlerIsFrameworkDefined(handler interface{}) bool {
	if handler == nil {
		return false
	}
	v := reflect.ValueOf(handler)
	if v.Kind() != reflect.Func || v.IsNil() {
		return false
	}
	fn := runtime.FuncForPC(v.Pointer())
	if fn == nil {
		return false
	}
	return strictcliPkgPrefix != "" && strings.HasPrefix(fn.Name(), strictcliPkgPrefix)
}

// rejectDeprecatedEffect enforces §1.1's classification exemption in the one
// direction that can be enforced: a caller passing WithEffect to Deprecated is
// wrong, because a deprecated entry has no handler and executes nothing.
func rejectDeprecatedEffect(name string, opts []CmdOption) {
	if len(opts) == 0 {
		return
	}
	probe := &Command{}
	for _, opt := range opts {
		opt(probe)
	}
	if probe.effectSet {
		panic(errDeprecatedCommandEffect(name))
	}
}

// flagParamName converts a flag name like "dry-run" to a parameter key "dry_run".
func flagParamName(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

// findCommandPrefix finds the group prefix for a command.
// Traverses the group tree recursively to find the full path.
func (a *App) findCommandPrefix(cmd *Command) string {
	result := searchGroupsForCommand(a.groups, cmd, nil)
	if result != "" {
		return result
	}
	return ""
}

// searchGroupsForCommand recursively searches groups for a command and returns
// the full path as a prefix string (e.g. "dns zone ").
func searchGroupsForCommand(groups map[string]*Group, cmd *Command, path []string) string {
	for _, grp := range groups {
		for _, c := range grp.Commands {
			if c == cmd {
				return strings.Join(append(path, grp.Name), " ") + " "
			}
		}
		result := searchGroupsForCommand(grp.Groups, cmd, append(path, grp.Name))
		if result != "" {
			return result
		}
	}
	return ""
}
