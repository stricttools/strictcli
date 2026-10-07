package strictcli

import (
	"os"
	"sort"
)

// Three check-system hooks (do not confuse them):
//
//  1. Check provider (RegisterCheckProvider) -- REGISTRY POPULATION. A provider
//     is a function that returns a list of fully-formed check SPECS (metadata +
//     a ceiling-typed impl). Providers are the TOML-less way to add checks: they
//     run lazily at the first registry read (materialization) and their specs go
//     through the same single add-path as TOML-declared checks, so a name that
//     collides with a TOML check or another provider's check is the usual hard
//     error. Registering a provider ENABLES the check system (a TOML-less app
//     with a provider gets a working `check` command).
//
//  2. Check-context factory (SetCheckContext) -- PROJECT CONSTRUCTION. Called
//     once per run with the dispatching command's *Context (its effects handle
//     included) to build the CheckContext handed to every check impl. It answers "what project are we checking?", independent of
//     which checks exist.
//
//  3. Scope adapter (Python-only, set_scope_adapter) -- PER-CHECK CONTEXT
//     PROJECTION. Called per scoped check to project the context or skip the
//     check. Go has no scope adapter yet (see the asymmetry note in check.go).
//
// A provider decides WHICH checks exist; the context factory decides WHAT
// project they see; the scope adapter (Python) decides HOW an individual check
// sees that project.

// CheckSpecMeta carries the declarative metadata of a provider-sourced check,
// mirroring the fields of a [checks.<name>] table in checks.toml. Severity is
// cross-checked against the constructor form at materialization time.
type CheckSpecMeta struct {
	Name         string
	Tags         []string
	Severity     string // "error" or "warn"
	Fast         bool
	Pure         bool
	NeedsNetwork bool
	DependsOn    []string
	Scope        string
}

// CheckSpec is a fully-formed, ceiling-typed check produced by a provider. It
// is opaque: construct one ONLY via NewErrorCheckSpec / NewWarnCheckSpec, which
// bind the reporter form to the severity so the impl cannot mint a problem its
// declared severity forbids.
type CheckSpec struct {
	meta     CheckSpecMeta
	impl     checkImpl
	implForm string // "error" or "warn" -- bound by the constructor
}

// NewErrorCheckSpec builds an error-severity check spec. The impl receives an
// *ErrorReporter (which can mint both error- and warn-severity problems). The
// meta's Severity must be "error" -- a mismatch is a hard error at
// materialization (the provider analog of the TOML/register severity check).
func NewErrorCheckSpec(meta CheckSpecMeta, impl func(CheckContext, *ErrorReporter) CheckOutcome) CheckSpec {
	return CheckSpec{
		meta: meta,
		impl: func(ctx CheckContext, _ *effectLog) CheckOutcome {
			r := &ErrorReporter{}
			return impl(ctx, r)
		},
		implForm: "error",
	}
}

// NewWarnCheckSpec builds a warn-severity check spec. The impl receives a
// *WarnReporter, which structurally lacks error-minting: a warn check cannot
// cascade. The meta's Severity must be "warn".
func NewWarnCheckSpec(meta CheckSpecMeta, impl func(CheckContext, *WarnReporter) CheckOutcome) CheckSpec {
	return CheckSpec{
		meta: meta,
		impl: func(ctx CheckContext, _ *effectLog) CheckOutcome {
			r := &WarnReporter{}
			return impl(ctx, r)
		},
		implForm: "warn",
	}
}

// RegisterCheckProvider registers a provider function that supplies check specs
// at materialization time. Registering a provider enables the check system (so
// a TOML-less app gains a working `check` command). Multiple providers may be
// registered; their specs are materialized in registration order. A registered
// provider is re-run whenever the registry is materialized fresh (first read or
// after a cwd change / ResetCheckProviderCache).
//
// Reentrancy: a provider must not trigger check execution during
// materialization (e.g. by calling RunChecks or the check command). Doing so
// re-enters materialization while it is in progress -- behavior is undefined
// (unbounded recursion). A provider's job is to return specs, nothing else.
func (a *App) RegisterCheckProvider(provider func() []CheckSpec) {
	a.enableChecks()
	a.checkProviders = append(a.checkProviders, provider)
	// Registering a new provider invalidates any prior materialization.
	a.providerMaterialized = false
}

// ResetCheckProviderCache drops every provider-sourced definition and clears the
// materialization memo so the next registry read re-runs all providers. Intended
// for tests and long-lived singletons. It does NOT unregister the providers
// themselves.
func (a *App) ResetCheckProviderCache() {
	a.providerMu.Lock()
	defer a.providerMu.Unlock()
	a.dropProviderSourcedDefs()
	a.providerMaterialized = false
	a.providerMaterializedCwd = ""
}

// materializeCheckProviders runs all registered providers and inserts their
// specs into the registry, memoized on the cwd at materialization time. It is
// the single chokepoint called at the start of every registry read (the check
// command handler and the programmatic RunChecks). Calling it repeatedly in the
// same cwd is a cheap no-op; a cwd change re-runs the providers (dropping the
// previous provider-sourced defs first).
func (a *App) materializeCheckProviders() {
	if len(a.checkProviders) == 0 {
		return
	}
	a.providerMu.Lock()
	defer a.providerMu.Unlock()
	cwd, _ := os.Getwd()
	if a.providerMaterialized && a.providerMaterializedCwd == cwd {
		return
	}
	// First materialization, or cwd changed: drop stale provider defs and re-run.
	a.dropProviderSourcedDefs()
	if a.providerSourcedNames == nil {
		a.providerSourcedNames = make(map[string]bool)
	}
	for _, provider := range a.checkProviders {
		specs := provider() // a panicking provider is a hard error in every mode
		for _, spec := range specs {
			if spec.meta.Severity != spec.implForm {
				used := checkSpecCtorName(spec.implForm)
				want := checkSpecCtorName(spec.meta.Severity)
				panic(errCheckProviderSeverityMismatch(spec.meta.Name, spec.meta.Severity, used, want))
			}
			def := &checkDef{
				name:         spec.meta.Name,
				tags:         spec.meta.Tags,
				severity:     spec.meta.Severity,
				fast:         spec.meta.Fast,
				pure:         spec.meta.Pure,
				needsNetwork: spec.meta.NeedsNetwork,
				dependsOn:    spec.meta.DependsOn,
				scope:        spec.meta.Scope,
				impl:         spec.impl,
				implForm:     spec.implForm,
			}
			// Routes through the single add-path: a name colliding with a TOML
			// check or another provider's check is the usual hard error.
			if err := a.addCheckDef(def); err != nil {
				panic(err.Error())
			}
			a.providerSourcedNames[spec.meta.Name] = true
		}
	}
	a.providerMaterialized = true
	a.providerMaterializedCwd = cwd
}

// dropProviderSourcedDefs removes every provider-sourced definition from the
// registry and rebuilds checkOrder from the survivors, so re-materialization
// removes exactly the provider defs (leaving TOML defs untouched).
func (a *App) dropProviderSourcedDefs() {
	if len(a.providerSourcedNames) == 0 {
		return
	}
	for name := range a.providerSourcedNames {
		delete(a.checkDefs, name)
	}
	a.providerSourcedNames = make(map[string]bool)
	a.checkOrder = a.checkOrder[:0]
	for name := range a.checkDefs {
		a.checkOrder = append(a.checkOrder, name)
	}
	sort.Strings(a.checkOrder)
}

// checkSpecCtorName maps a severity form to the constructor an author should use
// (for the severity-mismatch hint message).
func checkSpecCtorName(form string) string {
	switch form {
	case "error":
		return "NewErrorCheckSpec"
	case "warn":
		return "NewWarnCheckSpec"
	default:
		return "New" + form + "CheckSpec"
	}
}
