package strictcli

// invocation is one dispatch of an App through one of its doors (Run, Test,
// Call): the state its parse and its handler call write. That state lives here
// and never on the App, so dispatches running at the same time on one App
// share nothing they change. The App is embedded for its declarations, which a
// dispatch only reads.
type invocation struct {
	*App

	// reserved is the framework-owned quartet plus --json (contract §19.1),
	// extracted by the position-aware pre-scan and delivered on the Context,
	// never as handler kwargs.
	reserved reservedFlags

	// stdinConsumedBy names the flag that consumed stdin through @-.
	stdinConsumedBy *string

	// configData is the config file's data, loaded once at parse time.
	configData map[string]interface{}
	// configParseErr is a config parse error for config show to report. Set
	// when a config subcommand is routed and the config file was malformed.
	configParseErr string

	// effects is this dispatch's structured effect log. Populated in BOTH
	// modes: recorded entries in dry mode, executed entries (with recorded:
	// false) in live mode, plus framework-blessed CACHE_WRITEs.
	effects *effectLog
}

// newInvocation starts one dispatch of the App.
func (a *App) newInvocation() *invocation {
	return &invocation{App: a, effects: &effectLog{}}
}

// publishEffects makes this dispatch's effect log the one EffectLog returns:
// the log of the most recently finished dispatch.
func (a *invocation) publishEffects() {
	a.lastEffectsMu.Lock()
	defer a.lastEffectsMu.Unlock()
	a.lastEffects = a.effects
}
