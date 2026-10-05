package strictcli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Context provides structured output and provenance for command handlers.
// It is constructed unconditionally for every dispatch and passed to the handler.
// Each output method writes to the appropriate stream (stdout or stderr).
type Context struct {
	stdout  io.Writer
	stderr  io.Writer
	sources map[string]string // flag param name -> source label (cli/env/config/default/implied/infra)
	infra   *infraAccess      // resolved infra roots + declared handshake vars (nil if none)

	// The framework-owned reserved quartet plus --json, delivered here and
	// never as handler kwargs.
	reserved reservedFlags
	// effects is the per-dispatch effects handle (the runtime seal). nil for a
	// Context constructed outside a command dispatch.
	effects *Effects

	// The machine payload slot (contract §19.4): at most one value per
	// dispatch, settable only on a command that declared a payload schema.
	commandName   string
	payloadSchema map[string]interface{}
	payload       interface{}
	payloadSet    bool

	// The update construct's per-dispatch state (contract §27). writes is this
	// invocation's write set, nil on every command that declares no update; it
	// is what the envelope's `writes` member and the would-do log's write-set
	// line both render. unsets names the properties this invocation CLEARED,
	// which is what Unset answers off -- the minted `--unset-<prop>` delivers
	// no kwarg of its own (§27.6, §7.5's precedent).
	writes *updateState
	unsets map[string]bool

	// configData is the config file's data this dispatch's parse loaded, and
	// configParseErr the parse error it hit instead; the config commands read
	// both. Empty on the programmatic door, which loads no config.
	configData     map[string]interface{}
	configParseErr string

	// The diagnostics this dispatch emitted, in emission order (contract
	// §19.2). In machine mode the writers below record here instead of
	// writing: what they were asked to say rides the envelope. Outside machine
	// mode the slice stays empty and nothing changes.
	diagnostics []diagnosticRecord

	// The command's output declarations (contract §19.6's box, §19.10): Out is
	// refused on a command that owns stdout or declares a payload renderer,
	// and Document is usable only on a command that owns stdout.
	ownsStdout bool
	renderer   func(payload interface{}) string
	// The command's declared runtime requirements and the values they loaded
	// before the handler ran; Need reads them.
	declaredRequirements []AnyRequirement
	loadedRequirements   map[string]interface{}

	// output is the envelope's `output` member (§19.2's box): what Out wrote
	// in machine mode plus every captured child's stdout (§19.11), in arrival
	// order. Never nil.
	output *outputMember

	// done is closed when the handler's context is canceled (§19.13): by the
	// first SIGINT or SIGTERM, by a failure captured from a function started
	// through Go, and when the dispatch ends.
	done       chan struct{}
	cancelOnce sync.Once

	// The functions started through Go in this dispatch (§19.9), and the first
	// ExitNow or panic value captured from one of them.
	goroutines   sync.WaitGroup
	goMu         sync.Mutex
	goCaptured   interface{}
	goHasCapture bool
}

// diagnosticRecord is one entry of the envelope's diagnostics array (§19.2).
// The field order is the serialized key order.
type diagnosticRecord struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// reservedFlags carries the values of the framework-owned reserved quartet --
// plus --json, which is reserved beside them on the same unconditional tier
// (contract §7.1's 2026-08-13 amendment) without joining the set.
type reservedFlags struct {
	dryRun               bool
	approveConsequential bool
	quiet                bool
	verbose              bool
	json                 bool
}

// infraAccess carries a Context's view of infrastructure env vars: resolved root
// values (captured at construction), the set of declared handshake env vars
// (read live at access time), and the set of declared connection env vars (read
// live, but suppressed under --hermetic).
type infraAccess struct {
	roots       map[string]string // env var -> resolved path
	handshakes  map[string]bool   // env var -> declared
	connections map[string]bool   // env var -> declared (connection URL kind)
	hermetic    bool              // when true, connection env vars resolve as absent
}

// newContext creates a new Context with the given writers and provenance
// sources. It is the single arming point for the runtime seal: every dispatch
// site funnels through here and passes the reserved-flag state and the
// per-dispatch effects handle in.
func newContext(stdout, stderr io.Writer, sources map[string]string, infra *infraAccess, reserved reservedFlags, effects *Effects) *Context {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if sources == nil {
		sources = make(map[string]string)
	}
	return &Context{
		stdout:   stdout,
		stderr:   stderr,
		sources:  sources,
		infra:    infra,
		reserved: reserved,
		effects:  effects,
		output:   &outputMember{},
		done:     make(chan struct{}),
	}
}

// bindCommand carries a command's per-dispatch declarations onto the Context:
// its name (for call-time refusals), its payload schema, and its output
// declarations.
func (c *Context) bindCommand(cmd *Command) {
	c.commandName = cmd.Name
	c.payloadSchema = cmd.PayloadSchema
	c.ownsStdout = cmd.OwnsStdout
	c.renderer = cmd.PayloadRenderer
}

// DryRun reports whether the framework-owned --dry-run flag was passed.
func (c *Context) DryRun() bool { return c.reserved.dryRun }

// ApproveConsequential reports whether the framework-owned
// --approve-consequential flag was passed.
func (c *Context) ApproveConsequential() bool {
	return c.reserved.approveConsequential
}

// Quiet reports whether the framework-owned --quiet flag was passed.
func (c *Context) Quiet() bool { return c.reserved.quiet }

// Verbose reports whether the framework-owned --verbose flag was passed.
func (c *Context) Verbose() bool { return c.reserved.verbose }

// JSON reports whether the framework-owned --json flag was passed, which is
// what selects machine mode (contract §19.1).
//
// Handlers do not branch on it to decide whether to build a payload --
// Context.Payload is mode-independent and the framework decides what to do
// with the value -- but it is exposed for symmetry with the quartet and for
// apps that propagate it to a child process.
func (c *Context) JSON() bool { return c.reserved.json }

// Payload supplies this dispatch's machine payload (contract §19.4).
//
// The call is mode-independent: a handler calls it identically in both modes
// and never branches on JSON(). In machine mode the value is emitted; outside
// machine mode it is not printed at all. Test and Call capture it either way.
//
// It PANICS at call time on §19.4's own two rules: when the command declared no
// payload schema (there is nothing to validate the value against) and when a
// payload was already supplied in this dispatch (one slot, one answer).
//
// The value itself is validated against the declared schema at the EMISSION
// seam (§19.4, §19.5) -- only where machine mode actually writes the envelope.
// Validating here instead would make a payload that is legal in human mode fail
// a run that was never going to emit it, which §19.4's call-unconditionally rule
// forbids.
func (c *Context) Payload(value interface{}) {
	if c.payloadSchema == nil {
		panic(errPayloadNoSchema(c.commandName))
	}
	if c.payloadSet {
		panic(errPayloadAlreadySet(c.commandName))
	}
	c.payload = value
	c.payloadSet = true
}

// Effects returns the effects handle for this run. Panics when the Context was
// constructed outside a command dispatch.
func (c *Context) Effects() *Effects {
	if c.effects == nil {
		panic(errEffectsUnavailable)
	}
	return c.effects
}

// InfraValue returns the value of a declared infrastructure env var.
//
// For a declared location root (WithInfraRoot), it returns the value resolved
// eagerly at construction (env var if set, else the declared default) and true.
// The resolved value is always available, so the boolean is always true for
// roots.
//
// For a declared handshake var (WithHandshakeEnv), it reads the environment LIVE
// at call time (handshakes are set by the invoking process and carry no
// construction-time value), returning (value, isSet).
//
// For a declared connection env (WithConnectionEnv), it reads the environment
// LIVE at call time and returns (value, isSet) -- EXCEPT under --hermetic, where
// it resolves as absent ("", false) so connection-dependent behavior skips
// visibly instead of connecting.
//
// Panics if envVar is not a declared root, handshake, or connection var --
// declare everything.
func (c *Context) InfraValue(envVar string) (string, bool) {
	if c.infra != nil {
		if v, ok := c.infra.roots[envVar]; ok {
			return v, true
		}
		if c.infra.handshakes[envVar] {
			return os.LookupEnv(envVar)
		}
		if c.infra.connections[envVar] {
			if c.infra.hermetic {
				return "", false
			}
			return os.LookupEnv(envVar)
		}
	}
	panic(errInfraValueUndeclared(envVar))
}

// ConnectionEnvValue returns the value of a declared connection env
// (WithConnectionEnv), read LIVE at call time -- EXCEPT under --hermetic, where
// it resolves as absent ("", false). Panics if envVar is not a declared
// connection env. This is the check-side and handler-side accessor for the
// connection-URL kind; see also InfraValue, which resolves all three kinds.
func (c *Context) ConnectionEnvValue(envVar string) (string, bool) {
	if c.infra != nil && c.infra.connections[envVar] {
		if c.infra.hermetic {
			return "", false
		}
		return os.LookupEnv(envVar)
	}
	panic(errConnectionValueUndeclared(envVar))
}

// Info writes an informational message to stdout (hidden under --quiet).
func (c *Context) Info(msg string) {
	if c.diagnostic("info", msg) {
		return
	}
	if c.reserved.quiet {
		return
	}
	fmt.Fprintln(c.stdout, msg)
}

// Out writes the command's human-readable answer (contract §19.10): text and
// one newline to stdout, never hidden by --quiet. In machine mode nothing is
// written and the same bytes are appended to the envelope's `output` member.
//
// It panics at call time on a command that declares a payload renderer (the
// rendering is its human output) and on a command that owns stdout (write the
// document through Document).
func (c *Context) Out(text string) {
	if c.renderer != nil {
		panic(errOutWithRenderer(c.commandName))
	}
	if c.ownsStdout {
		panic(errOutOnOwnsStdout(c.commandName))
	}
	if c.reserved.json {
		c.output.appendText(text + "\n")
		return
	}
	fmt.Fprintln(c.stdout, text)
}

// Document returns the writer for an owns-stdout command's document (contract
// §19.6's document-writer amendment): bytes go to the real stdout, unchanged,
// in both modes, untouched by --quiet and --json. Writes are synchronous.
//
// It panics at call time on a command that did not declare OwnsStdout.
func (c *Context) Document() io.Writer {
	if !c.ownsStdout {
		panic(errDocumentWithoutOwnsStdout(c.commandName))
	}
	return c.stdout
}

// Done returns a channel that is closed when the handler's context is canceled
// (contract §19.13): by the first SIGINT or SIGTERM the CLI path receives while
// the handler runs, by an ExitNow or panic captured from a function started
// through Go, and when the dispatch ends.
func (c *Context) Done() <-chan struct{} {
	return c.done
}

// cancel closes Done, once.
func (c *Context) cancel() {
	c.cancelOnce.Do(func() { close(c.done) })
}

// Warn writes a warning message to stderr as "warning: <message>" (never
// suppressed). In machine mode the diagnostic carries the message unprefixed
// (contract §19.14).
func (c *Context) Warn(msg string) {
	if c.diagnostic("warn", msg) {
		return
	}
	fmt.Fprintln(c.stderr, warnPrefix+msg)
}

// Debug writes a debug message to stdout (shown only under --verbose).
// --quiet DOMINATES --verbose: passing both hides debug output.
func (c *Context) Debug(msg string) {
	if c.diagnostic("debug", msg) {
		return
	}
	if c.reserved.quiet || !c.reserved.verbose {
		return
	}
	fmt.Fprintln(c.stdout, msg)
}

// Error writes an error message to stderr as "error: <message>" (never
// suppressed). In machine mode the diagnostic carries the message unprefixed
// (contract §19.14).
func (c *Context) Error(msg string) {
	if c.diagnostic("error", msg) {
		return
	}
	fmt.Fprintln(c.stderr, errorPrefix+msg)
}

// The human-mode prefixes of the never-suppressed diagnostic writers (contract
// §19.14): added once, before the whole message, whatever the message holds.
const (
	errorPrefix = "error: "
	warnPrefix  = "warning: "
)

// diagnostic records a diagnostic in machine mode, reporting whether it was
// recorded. In machine mode the writers above write nothing and what they were
// asked to say rides the envelope's diagnostics instead (§19.1). The recording
// is NOT filtered by --quiet or --verbose: the envelope's content is a function
// of what the run produced, never of how a terminal was configured (§19.2).
func (c *Context) diagnostic(level, msg string) bool {
	if !c.reserved.json {
		return false
	}
	c.diagnostics = append(c.diagnostics, diagnosticRecord{Level: level, Message: msg})
	return true
}

// Source returns the provenance source label for a flag.
// Returns one of: "cli", "env", "config", "default", "implied", "infra".
// ("infra" indicates the value came from a RelativeToRoot default resolved
// through a declared infrastructure root.)
// Panics if the flag name is not found.
func (c *Context) Source(name string) string {
	// Try param name (underscores)
	key := strings.ReplaceAll(name, "-", "_")
	if s, ok := c.sources[key]; ok {
		return s
	}
	// Try original name (dashes)
	if s, ok := c.sources[name]; ok {
		return s
	}
	panic(errNoSourceInfo(name))
}

// Provided reports whether the INVOCATION caused this flag's value, as opposed
// to the declaration (contract §23.6). Sources "cli", "env", "config" and
// "implied" are provided; "default" and "infra" are not -- an implied value
// exists only because the invocation contained the trigger, while a
// RelativeToRoot default is still a declared default with a distinct label.
//
// It accepts dashed or underscored names, underscore form first, exactly as
// Source does, and panics on an unknown name with the same message: it reads
// the same per-parse store, so a name with no source has no provision either.
func (c *Context) Provided(name string) bool {
	switch c.Source(name) {
	case "cli", "env", "config", "implied":
		return true
	}
	return false
}

// Unset reports whether this invocation CLEARED the named property of an update
// command (contract §27.6): `--unset-<prop>` on the command line, or `null` on
// the property's own key at a machine door.
//
// An unset property delivers absence -- the same nil an untouched property
// delivers -- and reports Provided() true, the invocation having caused the
// write. This is what saves a handler from reconstructing that boolean out of
// two facts, which is §23.6's own reason for existing.
//
// It accepts dashed or underscored names and panics on an unknown name with the
// same message Source and Provided use: it reads the same per-parse store, so a
// name with no source has no clear either.
func (c *Context) Unset(name string) bool {
	key := strings.ReplaceAll(name, "-", "_")
	if _, ok := c.sources[key]; !ok {
		if _, ok := c.sources[name]; !ok {
			panic(errNoSourceInfo(name))
		}
	}
	dashed := strings.ReplaceAll(name, "_", "-")
	return c.unsets[dashed]
}

// reservedFrameworkFlagNames is the effects-regime quartet. The ban on these
// four LONG flag names is unconditional and applies at every level -- command
// flags, flag-set flags, mutex-group flags and app global flags. The four have
// no short forms, and short-flag names are unaffected by this ban.
var reservedFrameworkFlagNames = map[string]bool{
	"dry-run":               true,
	"approve-consequential": true,
	"quiet":                 true,
	"verbose":               true,
}

// reservedMachineFlagName is the machine-mode flag name, reserved on the SAME
// unconditional every-level tier as the quartet (contract §7.1's 2026-08-13
// amendment). It is NOT a fifth member of the quartet -- the four are the
// effects regime's own flags and are named as a set throughout the contract --
// so it carries its own reserved-name message and its own token entry.
const reservedMachineFlagName = "json"

// bannedFlagNames are names the framework refuses outright without owning a
// flag of that name. `yes` is here because --approve-consequential replaced
// --yes (contract §7.1) and a private --yes would restate it in a spelling
// that IS muscle memory -- exactly what the rename removed.
var bannedFlagNames = map[string]bool{
	"yes": true,
}

// reservedConsentParamName is the programmatic consent PARAMETER name,
// reserved on both the flag surface and the arg surface at every level.
// Go's kwargs are a map, so a parameter of this name cannot shadow the
// WithApproveConsequential() option the way Python's keyword-only consent
// parameter would -- but the name is framework vocabulary in every
// implementation (App.Call, Tool.Execute, the MCP tools/call param), and a
// command must mean the same thing on every channel and in every language.
// The quartet ban above covers the FLAG spelling `approve-consequential`;
// this covers the underscore spelling the parameter surface uses.
const reservedConsentParamName = "approve_consequential"

// reservedGlobalShortNames are the pre-existing reserved names; they are also
// what a SHORT flag name is checked against.
var reservedGlobalShortNames = map[string]bool{
	"help":        true,
	"h":           true,
	"version":     true,
	"v":           true,
	"dump-schema": true,
	"mcp":         true,
	"config":      true,
	"hermetic":    true,
	// The framework-use lint's flag (§28.1, §7.1's box): a whole-program
	// action on the global-only tier, beside dump-schema.
	"lint-framework-use": true,
}

// reservedGlobalFlagNames are names that cannot be used for user-defined global flags
// because they are reserved by the framework.
var reservedGlobalFlagNames = func() map[string]bool {
	m := make(map[string]bool, len(reservedGlobalShortNames)+len(reservedFrameworkFlagNames)+1)
	for k := range reservedGlobalShortNames {
		m[k] = true
	}
	for k := range reservedFrameworkFlagNames {
		m[k] = true
	}
	m[reservedMachineFlagName] = true
	return m
}()
