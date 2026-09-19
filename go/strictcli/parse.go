package strictcli

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Source represents where a flag value came from.
type Source int

const (
	SourceCLI     Source = iota // explicitly passed on the command line
	SourceEnv     Source = iota // from an environment variable
	SourceConfig  Source = iota // from a config file
	SourceDefault Source = iota // from the flag's default value
	SourceImplied Source = iota // injected by an Implies dependency
	SourceInfra   Source = iota // default resolved through a RelativeToRoot infra root
)

// sourceLabelString maps a Source to its provenance label string.
func sourceLabelString(src Source) string {
	switch src {
	case SourceCLI:
		return "cli"
	case SourceEnv:
		return "env"
	case SourceConfig:
		return "config"
	case SourceDefault:
		return "default"
	case SourceImplied:
		return "implied"
	case SourceInfra:
		return "infra"
	}
	return "default"
}

// sourcedEntry stores a value alongside its provenance.
type sourcedEntry struct {
	value  interface{}
	source Source
}

// sourcedStore wraps a map of flag-name to sourcedEntry, providing
// source-filtered presence queries for mutex and dependency evaluation.
type sourcedStore struct {
	entries map[string]sourcedEntry
}

func newSourcedStore() *sourcedStore {
	return &sourcedStore{entries: make(map[string]sourcedEntry)}
}

// set stores a value with its source.
func (s *sourcedStore) set(name string, value interface{}, src Source) {
	s.entries[name] = sourcedEntry{value: value, source: src}
}

// get returns the value and whether the key exists (ignoring source).
func (s *sourcedStore) get(name string) (interface{}, bool) {
	e, ok := s.entries[name]
	if !ok {
		return nil, false
	}
	return e.value, true
}

// getEntry returns the full sourcedEntry and whether the key exists.
func (s *sourcedStore) getEntry(name string) (sourcedEntry, bool) {
	e, ok := s.entries[name]
	return e, ok
}

// has returns true if the key exists regardless of source.
func (s *sourcedStore) has(name string) bool {
	_, ok := s.entries[name]
	return ok
}

// isCLI reports whether the value came from a command-line token. Mutex
// election is CLI-only (effects contract §21.3): env and config sources
// neither elect a member nor supply its value.
func (s *sourcedStore) isCLI(name string) bool {
	e, ok := s.entries[name]
	if !ok {
		return false
	}
	return e.source == SourceCLI
}

// isEnvOrConfig reports whether the value came from an env var or config file.
func (s *sourcedStore) isEnvOrConfig(name string) bool {
	e, ok := s.entries[name]
	if !ok {
		return false
	}
	return e.source == SourceEnv || e.source == SourceConfig
}

// delete drops an entry entirely, so defaults apply to it later.
func (s *sourcedStore) delete(name string) {
	delete(s.entries, name)
}

// isPresentForDeps reports whether the INVOCATION caused this flag's value.
// This is the single definition of "was this supplied" (contract §23.6):
// cli, env, config and implied count; default and infra do not, both being
// the declaration deciding rather than the invocation. Context.Provided
// answers the same question off the same set of source labels.
//
// It drives constraint evaluation -- a member's `present` election, the
// Requires operands and the Implies trigger (§26) -- and the custom-validator
// step, which runs on a supplied value only (contract §23.5's validate row).
func (s *sourcedStore) isPresentForDeps(name string) bool {
	e, ok := s.entries[name]
	if !ok {
		return false
	}
	switch e.source {
	case SourceCLI, SourceEnv, SourceConfig, SourceImplied:
		return true
	}
	return false
}

// toMap returns a plain map of name -> value (dropping source info).
func (s *sourcedStore) toMap() map[string]interface{} {
	m := make(map[string]interface{}, len(s.entries))
	for k, e := range s.entries {
		m[k] = e.value
	}
	return m
}

// sourceMap returns a map of flag name -> source label string.
func (s *sourcedStore) sourceMap() map[string]string {
	m := make(map[string]string, len(s.entries))
	for k, e := range s.entries {
		m[k] = sourceLabelString(e.source)
	}
	return m
}

const atPrefixMaxSize = 1024 * 1024 // 1 MB

// resolveAtPrefix resolves @-prefix for string flag values.
// @path reads from file, @- reads from stdin, @@literal strips leading @.
// Returns (resolved value, error message). Error message is "" on success.
// stdinConsumedBy is a pointer to a *string tracking which flag consumed stdin.
func resolveAtPrefix(flagName, raw string, stdinConsumedBy **string) (string, string) {
	if !strings.HasPrefix(raw, "@") {
		return raw, ""
	}
	if strings.HasPrefix(raw, "@@") {
		return raw[1:], "" // strip leading @
	}
	if raw == "@-" {
		if *stdinConsumedBy != nil {
			return "", errAtPrefixStdinOnce(flagName)
		}
		data, err := io.ReadAll(io.LimitReader(os.Stdin, int64(atPrefixMaxSize+1)))
		if err != nil {
			return "", errAtPrefixCannotReadStdin(flagName)
		}
		if len(data) > atPrefixMaxSize {
			return "", errAtPrefixFileTooLarge(flagName)
		}
		consumed := flagName
		*stdinConsumedBy = &consumed
		return strings.TrimRight(string(data), " \t\n\r"), ""
	}
	// @path
	path := raw[1:]
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errAtPrefixFileNotFound(flagName, path)
		}
		return "", errAtPrefixCannotReadFile(flagName, path)
	}
	if info.IsDir() {
		return "", errAtPrefixCannotReadFile(flagName, path)
	}
	if info.Size() > int64(atPrefixMaxSize) {
		return "", errAtPrefixFileTooLarge(flagName)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errAtPrefixCannotReadFile(flagName, path)
	}
	return strings.TrimRight(string(data), " \t\n\r"), ""
}

// parseCommand parses tokens against a resolved command's flags and args.
// globalFlags are also recognized in post-command tokens and returned separately
// in postGlobalValues so the caller can merge them with pre-command globals.
// configData is an optional map of config values (may be nil).
// conflictMode is "cli-wins" (default) or "error" (config+cli/env overlap is an error).
// When hermetic is true, env var and config resolution are skipped entirely.
// Returns (kwargs, postGlobalValues, sources, errorString).
func parseCommand(cmd *Command, tokens []string, globalFlags []Flag, configData map[string]interface{}, stdinConsumedBy **string, conflictMode string, hermetic bool, infraRoots map[string]string) (map[string]interface{}, map[string]interface{}, map[string]string, *updateState, map[string]bool, string, []string) {
	// Build flag lookup maps over the command's WHOLE scope tree (contract
	// §24.3): whether `--target` consumes the next argv element is decided
	// before any choice is elected, which is why sibling scopes may reuse a name
	// only with an identical value shape -- any site answers the arity question.
	longLookup := make(map[string]*Flag)     // --flag-name -> Flag
	shortLookup := make(map[string]*Flag)    // -x -> Flag
	negationLookup := make(map[string]*Flag) // --no-flag-name -> Flag

	unsetLookup := make(map[string]*Flag) // --unset-flag-name -> Flag

	for _, name := range cmd.index.order {
		f := cmd.index.sites[name][0].flag
		longLookup["--"+f.Name] = f
		if f.Type == TypeBool && f.Negatable {
			negationLookup["--no-"+f.Name] = f
		}
		// The clear vocabulary's minted spelling (§27.6). It is framework-owned
		// and reaches the handler on the Context; it is NOT negatable, so
		// `--no-unset-<x>` names nothing and is refused by the ordinary
		// unknown-flag path.
		if f.Nullable {
			unsetLookup["--"+unsetFlagName(f.Name)] = f
		}
	}
	for _, short := range cmd.index.shortOrder {
		shortLookup["-"+short] = cmd.index.shorts[short][0].flag
	}

	// Also include global flags in the lookup tables so they are recognized
	// when placed after the command name (matching Python's _parse_command)
	globalFlagNames := make(map[string]bool)
	globalByName := make(map[string]*Flag)
	for i := range globalFlags {
		f := &globalFlags[i]
		longLookup["--"+f.Name] = f
		if f.Short != "" {
			shortLookup["-"+f.Short] = f
		}
		if f.Type == TypeBool && f.Negatable {
			negationLookup["--no-"+f.Name] = f
		}
		globalFlagNames[f.Name] = true
		globalByName[f.Name] = f
	}

	// --- Phase 1: TOKENIZE every occurrence, without interpreting any of it.
	//
	// Coercion is deferred to phase 4 so that a scope violation is reported
	// before a coercion failure (§24.3's election -> scope -> value -> presence
	// precedence). Occurrences keep command-line order, so the value phase
	// reports the same coercion error, in the same order, as it always did.
	var occs []occurrence
	var suppliedOrder []string
	suppliedSeen := make(map[string]bool)
	var positionals []string

	record := func(name string, raw string, kind occKind) {
		occs = append(occs, occurrence{name: name, raw: raw, kind: kind})
	}
	// recordShort is record's short-form twin. A short may be claimed by two
	// MUTUALLY EXCLUSIVE scopes (§24.7), so which flag it names is not knowable
	// until the election is resolved; the occurrence carries the short and the
	// value phase resolves it against the live declaration.
	recordShort := func(short string, f *Flag, raw string, kind occKind) {
		occs = append(occs, occurrence{name: f.Name, short: short, raw: raw, kind: kind})
	}

	i := 0
	stopFlags := false

	for i < len(tokens) {
		tok := tokens[i]

		if stopFlags || !strings.HasPrefix(tok, "-") || tok == "-" {
			positionals = append(positionals, tok)
			i++
			continue
		}

		if tok == "--" {
			stopFlags = true
			i++
			continue
		}

		// --flag=value form
		if strings.HasPrefix(tok, "--") && strings.Contains(tok, "=") {
			eqPos := strings.Index(tok, "=")
			flagPart := tok[:eqPos]
			valuePart := tok[eqPos+1:]

			if f, ok := longLookup[flagPart]; ok {
				if f.Type == TypeBool {
					return nil, nil, nil, nil, nil, errBoolFlagNoValue(flagPart), nil
				}
				record(f.Name, valuePart, occValue)
			} else if _, ok := negationLookup[flagPart]; ok {
				return nil, nil, nil, nil, nil, errBoolNegationNoValue(flagPart), nil
			} else {
				return nil, nil, nil, nil, nil, errUnknownFlag(flagPart), nil
			}
			i++
			continue
		}

		// --no-flag negation
		if f, ok := negationLookup[tok]; ok {
			record(f.Name, "", occNegated)
			i++
			continue
		}

		// --unset-flag: the clear vocabulary's minted spelling. It carries no
		// value of its own -- clearing is one act, not a value -- and it is
		// checked against the property's own occurrences in phase 4a.
		if f, ok := unsetLookup[tok]; ok {
			record(f.Name, "", occUnset)
			i++
			continue
		}

		// --flag (long form without =)
		if strings.HasPrefix(tok, "--") {
			f, ok := longLookup[tok]
			if !ok {
				return nil, nil, nil, nil, nil, errUnknownFlag(tok), nil
			}
			if f.Type == TypeBool {
				record(f.Name, "", occBool)
				i++
			} else {
				if i+1 >= len(tokens) {
					return nil, nil, nil, nil, nil, errFlagRequiresValue(tok), nil
				}
				record(f.Name, tokens[i+1], occValue)
				i += 2
			}
			continue
		}

		// -x (short form)
		if strings.HasPrefix(tok, "-") && len(tok) == 2 {
			if f, ok := shortLookup[tok]; ok {
				if f.Type == TypeBool {
					recordShort(tok[1:], f, "", occBool)
					i++
				} else {
					if i+1 >= len(tokens) {
						return nil, nil, nil, nil, nil, errFlagRequiresValue(tok), nil
					}
					recordShort(tok[1:], f, tokens[i+1], occValue)
					i += 2
				}
				continue
			}
		}

		// Token starts with "-" but doesn't match any known flag;
		// treat as a positional arg (e.g. negative numbers like -7, -3.14)
		positionals = append(positionals, tok)
		i++
	}

	// --- Phase 2: ELECT, outermost first, then recursively inside each elected
	// choice. A token-spelled selector elects from any source; a member-spelled
	// one elects from the command line only (§24.6).
	sup := newSuppliedElections()
	for _, o := range occs {
		sup.suppliedNames[o.name] = true
		if !suppliedSeen[o.name] {
			suppliedSeen[o.name] = true
			suppliedOrder = append(suppliedOrder, o.name)
		}
		if _, isGlobal := globalFlagNames[o.name]; isGlobal {
			continue
		}
		sites, ok := cmd.index.sites[o.name]
		if !ok {
			continue
		}
		f := sites[0].flag
		if f.Type == TypeChoice && !f.memberSpelled {
			sup.tokenValues[o.name] = append(sup.tokenValues[o.name], o.raw)
			continue
		}
		if isMemberFlagName(cmd.index, o.name) {
			// A bool member is elected by `--<name>` and only when the value it
			// resolves to is true; `--no-<name>` DECLINES (§21.2).
			sup.memberElected[o.name] = o.kind != occNegated
		}
	}
	amb := ambientSource{hermetic: hermetic, configData: configData}
	est, electErr := elect(cmd, sup, amb)
	if electErr != "" {
		return nil, nil, nil, nil, nil, electErr, nil
	}

	// A short claimed by two mutually exclusive scopes names its LIVE
	// declaration; the provisional name the tokenizer recorded is replaced now
	// that the election is known.
	for i := range occs {
		if occs[i].short == "" {
			continue
		}
		if f := est.liveFlagForShort(cmd, occs[i].short); f != nil {
			occs[i].name = f.Name
		}
	}
	suppliedOrder = suppliedOrder[:0]
	suppliedSeen = map[string]bool{}
	for _, o := range occs {
		if !suppliedSeen[o.name] {
			suppliedSeen[o.name] = true
			suppliedOrder = append(suppliedOrder, o.name)
		}
	}

	// --- Phase 3: validate SCOPE membership of every supplied flag.
	if errStr := est.checkScope(suppliedOrder); errStr != "" {
		return nil, nil, nil, nil, nil, errStr, est.skipped
	}

	// --- Phase 4a: coerce the collected occurrences, in command-line order,
	// against the LIVE declaration of each name.
	//
	// A property written AND cleared in one invocation is refused before either
	// is read: the two tokens state opposite things about one property, and no
	// order of application makes one of them true (§27.6).
	unsets := map[string]bool{}
	for _, o := range occs {
		if o.kind == occUnset {
			unsets[o.name] = true
		}
	}
	if len(unsets) > 0 {
		for _, o := range occs {
			if o.kind != occUnset && unsets[o.name] {
				return nil, nil, nil, nil, nil, errUpdateValueAndUnset(o.name), est.skipped
			}
		}
	}
	cliByFlag := make(map[*Flag]interface{})
	for _, o := range occs {
		if o.kind == occUnset {
			continue
		}
		f := est.liveFlagFor(cmd, globalByName, o.name)
		if f == nil {
			continue // unreachable: scope validation passed
		}
		if f.Type == TypeChoice {
			continue // the election is the selector's value
		}
		if o.kind == occNegated {
			cliByFlag[f] = false
			continue
		}
		if o.kind == occBool {
			cliByFlag[f] = true
			continue
		}
		if errStr := parseFlagRawValue(f, o.raw, cliByFlag, stdinConsumedBy); errStr != "" {
			return nil, nil, nil, nil, nil, errStr, est.skipped
		}
	}

	// Root-scope and global CLI values keep their name-keyed store, which is
	// what the dependency, choices and validate machinery reads unchanged.
	cliSet := make(map[string]interface{})
	for i := range cmd.flags {
		if v, ok := cliByFlag[&cmd.flags[i]]; ok {
			cliSet[cmd.flags[i].Name] = v
		}
	}
	for i := range globalFlags {
		if v, ok := cliByFlag[&globalFlags[i]]; ok {
			cliSet[globalFlags[i].Name] = v
		}
	}
	// A cleared property is SUPPLIED: the value it delivers is absence, the
	// same nil an untouched property delivers, and the two are told apart by
	// Provided() -- true here, because the invocation caused the write -- and
	// by ctx.Unset (§27.6). Entering it in the store also stops env, config and
	// the declared-default step from filling the gap the clear opened.
	for name := range unsets {
		cliSet[name] = nil
	}

	// Track which flag names are set by env vs config (for source attribution).
	envNames := make(map[string]bool)
	configNames := make(map[string]bool)

	// Resolve env vars for flags not set by CLI (skipped under --hermetic)
	if !hermetic {
		for i := range cmd.flags {
			f := &cmd.flags[i]
			// A selector's own env binding was already consulted by the election
			// phase; consulting it again here would be a second application site.
			if f.Type == TypeChoice {
				continue
			}
			if _, ok := cliSet[f.Name]; ok {
				continue
			}
			if f.Env == "" {
				continue
			}
			envVal, ok := os.LookupEnv(f.Env)
			if !ok {
				continue
			}
			val, errStr := resolveFlagEnvValue(f, envVal, stdinConsumedBy)
			if errStr != "" {
				return nil, nil, nil, nil, nil, errStr, est.skipped
			}
			cliSet[f.Name] = val
			envNames[f.Name] = true
		}

		// Resolve config values for flags not set by CLI or env.
		// In conflict mode "error", detect when config would set a flag
		// already set by CLI or env.
		if configData != nil {
			for i := range cmd.flags {
				f := &cmd.flags[i]
				// A selector's own config binding was already consulted by the
				// election phase (§24.6).
				if f.Type == TypeChoice {
					continue
				}
				param := flagParamName(f.Name)
				configVal, hasConfig := configData[param]
				if !hasConfig {
					continue
				}
				// Effective mode: per-flag override if set, else the app default.
				effectiveMode := conflictMode
				if f.hasConflictMode {
					effectiveMode = f.ConflictMode
				}
				if existing, alreadySet := cliSet[f.Name]; alreadySet {
					// Flag set by CLI or env, config also has a value. This is a
					// conflict ONLY when the values diverge; identical values agree.
					if effectiveMode == "error" {
						coerced, errStr := coerceConfigValue(configVal, f)
						if errStr != "" {
							return nil, nil, nil, nil, nil, errConfigValueError(f.Name, errStr), est.skipped
						}
						if !valuesEqualForConflict(existing, coerced, f) {
							existingSource := "cli"
							if envNames[f.Name] {
								existingSource = "env"
							}
							return nil, nil, nil, nil, nil, errFlagSetInBothAndConfig(f.Name, existingSource), est.skipped
						}
					}
					continue // cli-wins, or error mode with matching values
				}
				coerced, errStr := coerceConfigValue(configVal, f)
				if errStr != "" {
					return nil, nil, nil, nil, nil, errConfigValueError(f.Name, errStr), est.skipped
				}
				if f.Unique {
					if arr, ok := coerced.([]interface{}); ok {
						if dup := findDuplicate(arr); dup != nil {
							return nil, nil, nil, nil, nil, errConfigValueDuplicate(f.Name, formatValueForError(dup)), est.skipped
						}
					}
				}
				cliSet[f.Name] = coerced
				configNames[f.Name] = true
			}

			// Config-conflict detection for GLOBAL flags parsed AFTER the command
			// name (`tool cmd --global X`). This is CONFLICT-DETECTION ONLY: config
			// values for globals were already APPLIED during the pre-command
			// global-flag pass (extractGlobalFlags), so applying them again here
			// would be a second application site -- wrong even if idempotent. We
			// must never write a config value into cliSet for a global here.
			// Globals that reach cliSet at this point are purely CLI-parsed
			// (post-command env for globals is never resolved here), so the
			// divergence source is always "cli".
			for i := range globalFlags {
				f := &globalFlags[i]
				existing, alreadySet := cliSet[f.Name]
				if !alreadySet {
					continue
				}
				param := flagParamName(f.Name)
				configVal, hasConfig := configData[param]
				if !hasConfig {
					continue
				}
				effectiveMode := conflictMode
				if f.hasConflictMode {
					effectiveMode = f.ConflictMode
				}
				if effectiveMode != "error" {
					continue
				}
				coerced, errStr := coerceConfigValue(configVal, f)
				if errStr != "" {
					return nil, nil, nil, nil, nil, errConfigValueError(f.Name, errStr), est.skipped
				}
				if !valuesEqualForConflict(existing, coerced, f) {
					return nil, nil, nil, nil, nil, errFlagSetInBothCliAndConfig(f.Name), est.skipped
				}
			}
		}
	} // end if !hermetic

	// Wrap cliSet into a sourcedStore with proper source attribution.
	// CLI-parsed values are SourceCLI, env-resolved values are SourceEnv,
	// and config-resolved values are SourceConfig.
	store := newSourcedStore()
	for k, v := range cliSet {
		if envNames[k] {
			store.set(k, v, SourceEnv)
		} else if configNames[k] {
			store.set(k, v, SourceConfig)
		} else {
			store.set(k, v, SourceCLI)
		}
	}

	kwargs, postGlobals, sources, writes, errStr := validateAndBuildKwargs(cmd, store, argvPositionals(positionals), globalFlagNames, infraRoots, est, cliByFlag, amb, stdinConsumedBy, unsets)
	return kwargs, postGlobals, sources, writes, unsets, errStr, est.skipped
}

// applyFlagDefault resolves the default value for a flag that was not provided
// on the command line. Returns (value, errorMsg). If errorMsg is non-empty, the
// flag is required and was not provided. The prefix is prepended to error
// messages (e.g. "global " for global flags, "" for command flags).
func applyFlagDefault(f *Flag, prefix string, roots map[string]string) (interface{}, Source, string) {
	// Delivery follows the DECLARED presence and nothing else (contract §23):
	// there is no empty-collection default a compound flag never asked for, and
	// no mutex-member exemption -- a member declares Optional() like anything
	// else, and the group enforces cardinality on top of that.
	switch f.presence {
	case presenceDefault:
		if IsDictType(f.Type) {
			src := f.Default.(map[string]interface{})
			m := make(map[string]interface{}, len(src))
			for k, v := range src {
				m[k] = v
			}
			return m, SourceDefault, ""
		}
		if f.Repeatable {
			src := f.Default.([]interface{})
			return append([]interface{}{}, src...), SourceDefault, ""
		}
		// A RelativeToRoot marker resolves through the declared infra roots and
		// reports source "infra" (distinguishable from a plain default).
		if ref, ok := f.Default.(InfraRootPath); ok {
			resolved, err := resolveInfraRootPath(ref, roots)
			if err != nil {
				// Should be unreachable: markers are validated at registration.
				return nil, SourceDefault, fmt.Sprintf("%s%s", prefix, err.Error())
			}
			return resolved, SourceInfra, ""
		}
		return f.Default, SourceDefault, ""
	case presenceOptional:
		// Absence delivered AS absence. The source label stays "default" --
		// the declaration decided, which is what that label means (§23.6).
		return nil, SourceDefault, ""
	}
	if f.Type == TypeBool && f.Negatable {
		return nil, SourceDefault, fmt.Sprintf("%sflag '--%s' must be passed as --%s or --no-%s", prefix, f.Name, f.Name, f.Name)
	}
	if f.Type == TypeBool && !f.Negatable {
		return nil, SourceDefault, fmt.Sprintf("%sflag '--%s' must be passed as --%s", prefix, f.Name, f.Name)
	}
	return nil, SourceDefault, fmt.Sprintf("%sflag '--%s' is required", prefix, f.Name)
}

// validateAndBuildKwargs performs pure validation and kwargs assembly on the
// already-parsed sourced values. It enforces mutex constraints (using
// source-filtered presence), resolves implies dependencies, checks
// co-required/requires dependencies, applies defaults, validates choices,
// runs custom validation, resolves positional args, and builds the final
// kwargs map.
// Returns (kwargs, postGlobalValues, errorString).
func validateAndBuildKwargs(cmd *Command, store *sourcedStore, positionals positionalInput, globalFlagNames map[string]bool, infraRoots map[string]string, est *electionState, cliByFlag map[*Flag]interface{}, amb ambientSource, stdinConsumedBy **string, unsets map[string]bool) (map[string]interface{}, map[string]interface{}, map[string]string, *updateState, string) {
	// Election ran in phase 2 and scope validation in phase 3 (scope_parse.go).
	// MutexGroup is deleted: "exactly one of these" is a member-spelled selector
	// now, and §21.4's three errors survive verbatim inside the election phase
	// (contract §21's supersession box, §24.4).

	// Resolve `Implies` first, so an implied value can engage a constraint
	// member. Implied values are stored with SourceImplied.
	for i := range cmd.constraints {
		d := &cmd.constraints[i]
		if d.family != familyImplies {
			continue
		}
		if !store.isPresentForDeps(d.flag) {
			continue
		}
		if targetVal, targetSet := store.get(d.implies); targetSet {
			// Target was explicitly set -- check for conflict
			if targetVal.(bool) != d.value {
				neg := ""
				if !d.value {
					neg = "no-"
				}
				explicitNeg := ""
				if d.value {
					explicitNeg = "no-"
				}
				return nil, nil, nil, nil, errImpliesConflict(d.name, d.flag, neg, d.implies, explicitNeg)
			}
			continue
		}
		// Target not set -- inject the implied value
		store.set(d.implies, d.value, SourceImplied)
	}

	// Enforce the declared constraints, children before parents, siblings in
	// declaration order (contract §26.4). It runs after `Implies` injection and
	// BEFORE defaults are applied, so a declared default cannot engage a member:
	// isPresentForDeps counts cli, env, config and implied, and never default.
	if errMsg := evaluateConstraints(cmd, store, positionals, est); errMsg != "" {
		return nil, nil, nil, nil, errMsg
	}

	// The at-least-one-property rule, and the write set it guarantees is never
	// empty (contract §27.4, §27.5). It is the framework's own rule about a
	// declared property set rather than a constraint the command wrote, so it
	// is evaluated after every declared constraint has had its say -- and, like
	// them, BEFORE defaults are applied, over the one provided-ness predicate
	// with no source filter.
	writes, errMsg := evaluateUpdate(cmd, store, unsets)
	if errMsg != "" {
		return nil, nil, nil, nil, errMsg
	}

	// Apply defaults (SourceDefault), and resolve every selector's elected
	// record. Presence is the LAST phase: a required selector that elected
	// nothing is refused here, after scope and value errors, in declaration
	// order beside the ordinary required-flag errors (contract §24.3).
	for i := range cmd.flags {
		f := &cmd.flags[i]
		if f.Type == TypeChoice {
			ls := est.bySel[f]
			if ls.unsatisfied {
				return nil, nil, nil, nil, est.unsatisfiedSelectorError(ls)
			}
			elected, errMsg := est.resolveElected(ls, cliByFlag, amb, infraRoots, stdinConsumedBy)
			if errMsg != "" {
				return nil, nil, nil, nil, errMsg
			}
			store.set(f.Name, elected, electionSource(ls.origin))
			continue
		}
		if store.has(f.Name) {
			continue
		}
		val, src, errMsg := applyFlagDefault(f, "", infraRoots)
		if errMsg != "" {
			return nil, nil, nil, nil, errMsg
		}
		store.set(f.Name, val, src)
	}

	// Validate choices
	for i := range cmd.flags {
		f := &cmd.flags[i]
		val, ok := store.get(f.Name)
		if !ok {
			continue
		}
		if errMsg := validateChoices(f.Name, val, f.Repeatable, f.Choices, f.retiredChoices, false); errMsg != "" {
			return nil, nil, nil, nil, errMsg
		}
	}

	// Custom validation. It runs on a SUPPLIED value only: never on absence,
	// and never on a declared default -- including a RelativeToRoot default,
	// whose "infra" label is still the declaration deciding (contract §23.5's
	// validate row, §23.6).
	for i := range cmd.flags {
		f := &cmd.flags[i]
		if f.Validate == nil || !store.isPresentForDeps(f.Name) {
			continue
		}
		val, ok := store.get(f.Name)
		if !ok || val == nil {
			continue
		}
		if f.Repeatable {
			vals, ok := val.([]interface{})
			if !ok {
				continue
			}
			for _, v := range vals {
				if err := f.Validate(v); err != nil {
					return nil, nil, nil, nil, errFlagValueError(f.Name, err.Error())
				}
			}
		} else {
			if err := f.Validate(val); err != nil {
				return nil, nil, nil, nil, errFlagValueError(f.Name, err.Error())
			}
		}
	}

	// Resolve positional args
	argValues := make(map[string]interface{})
	if positionals.preTyped {
		if errStr := resolvePreTypedArgs(cmd, positionals.byName, argValues); errStr != "" {
			return nil, nil, nil, nil, errStr
		}
	} else if errStr := resolveArgTokens(cmd, positionals.tokens, argValues); errStr != "" {
		return nil, nil, nil, nil, errStr
	}

	// Validate arg choices (after type coercion)
	for i := range cmd.args {
		a := &cmd.args[i]
		val, ok := argValues[a.Name]
		if !ok {
			continue
		}
		if errMsg := validateChoices(a.Name, val, a.IsVariadic, a.Choices, a.retiredChoices, true); errMsg != "" {
			return nil, nil, nil, nil, errMsg
		}
	}

	// Build kwargs dict (command flags only)
	kwargs := make(map[string]interface{})
	for i := range cmd.flags {
		f := &cmd.flags[i]
		if val, ok := store.get(f.Name); ok {
			kwargs[flagParamName(f.Name)] = val
		}
	}
	for _, a := range cmd.args {
		if v, ok := argValues[a.Name]; ok {
			kwargs[a.Name] = v
		}
	}

	// Separate out global flag values parsed from post-command tokens
	postGlobalValues := make(map[string]interface{})
	for name := range globalFlagNames {
		if val, ok := store.get(name); ok {
			postGlobalValues[flagParamName(name)] = val
		}
	}

	// Build source map: param-name -> source label (for Context.Source())
	rawSources := store.sourceMap()
	sources := make(map[string]string)
	for i := range cmd.flags {
		f := &cmd.flags[i]
		if s, ok := rawSources[f.Name]; ok {
			sources[flagParamName(f.Name)] = s
		}
	}
	// Global flags parsed post-command emit their source label too (always
	// "cli" here; env/config for globals resolve in the pre-command pass).
	// Without this, `tool cmd --global X` reports source "default" for it.
	for name := range globalFlagNames {
		if s, ok := rawSources[name]; ok {
			sources[flagParamName(name)] = s
		}
	}

	return kwargs, postGlobalValues, sources, writes, ""
}

// validateChoices checks a resolved flag or arg value against its choices
// list, returning an error message or "" if valid. isArg selects the message
// prefix ("argument 'name':" instead of "--name:"); the two message templates
// live in errors.go so conformance/check_error_parity.py can extract them.
// A nil val is exempt from validation: nil only arises from an Optional() /
// ArgOptional() declaration or an unelected mutex member, all meaning "not
// passed" -- a CLI-supplied value is never nil. Absence is never matched
// against choices (contract §23.5).
//
// A RETIRED spelling is checked first, so a reader who typed a value that used
// to work is told what replaced it instead of being handed the list it is
// missing from. Every source that reaches this funnel today -- command line,
// env var, config file, and the programmatic doors -- takes the retired
// refusal for free; nothing new is resolved here.
func validateChoices(name string, val interface{}, repeatable bool, choices []interface{}, retired []RetiredChoiceValue, isArg bool) string {
	if (choices == nil && retired == nil) || val == nil {
		return ""
	}
	check := func(v interface{}) string {
		if msg, ok := retiredChoiceMessage(v, retired); ok {
			if isArg {
				return errArgRetiredChoice(name, formatValueForError(v), msg)
			}
			return errFlagRetiredChoice(name, formatValueForError(v), msg)
		}
		if choices == nil || inChoices(v, choices) {
			return ""
		}
		if isArg {
			return errArgInvalidChoice(name, formatValueForError(v), formatChoices(choices))
		}
		return errFlagInvalidChoice(name, formatValueForError(v), formatChoices(choices))
	}
	if repeatable {
		vals, ok := val.([]interface{})
		if !ok {
			return ""
		}
		for _, v := range vals {
			if errMsg := check(v); errMsg != "" {
				return errMsg
			}
		}
		return ""
	}
	return check(val)
}

func inChoices(val interface{}, choices []interface{}) bool {
	for _, c := range choices {
		if val == c {
			return true
		}
	}
	return false
}

func formatChoices(choices []interface{}) string {
	parts := make([]string, len(choices))
	for i, c := range choices {
		parts[i] = formatValueForError(c)
	}
	return strings.Join(parts, ", ")
}

// parseBoolStrict parses a string as a boolean with strict validation.
// Accepts: 1, true, yes (case-insensitive) -> true
// Accepts: 0, false, no (case-insensitive) -> false
// Everything else returns an error.
func parseBoolStrict(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no":
		return false, nil
	default:
		return false, errExpectedBoolean(s)
	}
}

// parseIntStrict parses a string as an integer with strict validation.
// Uses strconv.Atoi which rejects leading/trailing whitespace.
func parseIntStrict(s string) (int, error) {
	intVal, err := strconv.Atoi(s)
	if err != nil {
		return 0, errExpectedInteger(s)
	}
	return intVal, nil
}

// parseFloatStrictValue parses a string as float64 with strict validation:
// rejects leading/trailing whitespace, NaN, and +/-Inf.
func parseFloatStrictValue(s string) (float64, error) {
	if s != strings.TrimSpace(s) {
		return 0, errExpectedFloat(s)
	}
	floatVal, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, errExpectedFloat(s)
	}
	if math.IsNaN(floatVal) {
		return 0, errNaNNotAllowed()
	}
	if math.IsInf(floatVal, 0) {
		return 0, errInfNotAllowed()
	}
	return floatVal, nil
}

// parseFloatStrict parses a string as float64 with strict validation,
// returning flag-contextualized error messages.
func parseFloatStrict(flagName, raw string) (interface{}, string) {
	floatVal, err := parseFloatStrictValue(raw)
	if err != nil {
		msg := err.Error()
		if msg == "NaN is not allowed" || msg == "Inf is not allowed" {
			return nil, fmt.Sprintf("--%s: %s", flagName, msg)
		}
		return nil, fmt.Sprintf("--%s: expected float, got '%s'", flagName, raw)
	}
	return floatVal, ""
}

// positionalInput is the positional input one door supplied, in the form that
// door speaks (contract §24.11 item 244).
//
// The argv door supplies TOKENS in the order they were typed, which are parsed
// against the declaration and bound by position. A programmatic door supplies
// VALUES KEYED BY ARG NAME: they are already of the declared type, so they are
// checked against it rather than parsed, handed on as supplied rather than
// stringified into a token the caller did not write, and bound to the arg the
// key names rather than to whatever position the supplied subset happens to
// leave them in. Which of the two a call carries is the DOOR's statement, made
// once where the input is built; nothing below infers it from the values.
type positionalInput struct {
	tokens   []string
	byName   map[string]interface{}
	preTyped bool
}

// argvPositionals is the argv door's input: tokens to parse, bound by position.
func argvPositionals(tokens []string) positionalInput {
	return positionalInput{tokens: tokens}
}

// preTypedPositionals is a programmatic door's input: values to check, bound by
// the arg name each was supplied under.
func preTypedPositionals(byName map[string]interface{}) positionalInput {
	return positionalInput{byName: byName, preTyped: true}
}

// resolveArgTokens binds the argv door's tokens to the declared args BY
// POSITION, parsing each against the arg it lands on.
func resolveArgTokens(cmd *Command, tokens []string, argValues map[string]interface{}) string {
	posIdx := 0
	for i := range cmd.args {
		a := &cmd.args[i]
		if a.IsVariadic {
			remaining := tokens[posIdx:]
			if len(remaining) == 0 {
				if a.presence == presenceRequired {
					return errMissingRequiredArgument(a.Name)
				}
				// A variadic arg always delivers a list, so its optional case
				// is the empty one (contract §23.3).
				argValues[a.Name] = []interface{}{}
			} else {
				vals := make([]interface{}, len(remaining))
				for j, tok := range remaining {
					coerced, errStr := coerceArgValue(a, tok)
					if errStr != "" {
						return errStr
					}
					vals[j] = coerced
				}
				argValues[a.Name] = vals
			}
			posIdx = len(tokens)
		} else if posIdx < len(tokens) {
			coerced, errStr := coerceArgValue(a, tokens[posIdx])
			if errStr != "" {
				return errStr
			}
			argValues[a.Name] = coerced
			posIdx++
		} else if a.presence == presenceRequired {
			return errMissingRequiredArgument(a.Name)
		} else if a.presence == presenceDefault {
			argValues[a.Name] = a.Default
		} else {
			// An optional arg delivers absence as a PRESENT key holding nil
			// -- never key-absence (contract §23.3).
			argValues[a.Name] = nil
		}
	}
	if posIdx < len(tokens) {
		return errUnexpectedArgument(tokens[posIdx])
	}
	return ""
}

// resolvePreTypedArgs binds a programmatic door's values to the args their KEYS
// name, checking each against the declaration it was supplied against
// (contract §24.11 item 244).
//
// Presence is answered by the key's absence, exactly as the argv path answers it
// with a token that was never typed -- so an omitted optional arg is delivered
// as a present key holding absence, and an omitted required one keeps the argv
// path's own sentence.
func resolvePreTypedArgs(cmd *Command, byName map[string]interface{}, argValues map[string]interface{}) string {
	for i := range cmd.args {
		a := &cmd.args[i]
		raw, supplied := byName[a.Name]
		if supplied {
			checked, errStr := checkPreTypedArgValue(a, raw)
			if errStr != "" {
				return errStr
			}
			if a.IsVariadic && a.presence == presenceRequired {
				if vals, ok := checked.([]interface{}); ok && len(vals) == 0 {
					// An empty array is the flat spelling of no tokens at all.
					return errMissingRequiredArgument(a.Name)
				}
			}
			argValues[a.Name] = checked
			continue
		}
		switch {
		case a.IsVariadic:
			if a.presence == presenceRequired {
				return errMissingRequiredArgument(a.Name)
			}
			argValues[a.Name] = []interface{}{}
		case a.presence == presenceRequired:
			return errMissingRequiredArgument(a.Name)
		case a.presence == presenceDefault:
			argValues[a.Name] = a.Default
		default:
			argValues[a.Name] = nil
		}
	}
	return ""
}

// coerceArgValue coerces a raw positional arg string to the declared type.
// Uses the same strict parsing functions as flags. Error messages use
// "argument '<name>': ..." prefix for parity with Python.
// For list types, coerces using the item type.
func coerceArgValue(a *Arg, raw string) (interface{}, string) {
	t := a.Type
	// For list-typed variadic args, coerce each element to the item type
	if IsListType(t) {
		t = ItemType(t)
	}
	switch t {
	case TypeStr:
		return raw, ""
	case TypeInt:
		intVal, err := parseIntStrict(raw)
		if err != nil {
			return nil, fmt.Sprintf("argument '%s': %s", a.Name, err.Error())
		}
		return intVal, ""
	case TypeFloat:
		floatVal, err := parseFloatStrictValue(raw)
		if err != nil {
			msg := err.Error()
			if msg == "NaN is not allowed" || msg == "Inf is not allowed" {
				return nil, fmt.Sprintf("argument '%s': %s", a.Name, msg)
			}
			return nil, fmt.Sprintf("argument '%s': expected float, got '%s'", a.Name, raw)
		}
		return floatVal, ""
	case TypeBool:
		boolVal, err := parseBoolStrict(raw)
		if err != nil {
			return nil, fmt.Sprintf("argument '%s': %s", a.Name, err.Error())
		}
		return boolVal, ""
	default:
		return raw, ""
	}
}

// splitEscaped splits value on sep, treating backslash as escape character.
// Escaped sep becomes literal sep. Escaped backslash becomes literal backslash.
// Trailing backslash with nothing to escape becomes literal backslash.
func splitEscaped(value string, sep byte) []string {
	var parts []string
	var current []byte
	i := 0
	for i < len(value) {
		if value[i] == '\\' {
			if i+1 < len(value) {
				next := value[i+1]
				if next == sep {
					current = append(current, sep)
					i += 2
				} else if next == '\\' {
					current = append(current, '\\', '\\')
					i += 2
				} else {
					current = append(current, '\\', next)
					i += 2
				}
			} else {
				// Trailing backslash
				current = append(current, '\\', '\\')
				i++
			}
		} else if value[i] == sep {
			parts = append(parts, string(current))
			current = current[:0]
			i++
		} else {
			current = append(current, value[i])
			i++
		}
	}
	parts = append(parts, string(current))
	return parts
}

// valuesEqualForConflict compares a CLI/env value and a config value for
// conflict-mode equality. Equality semantics (pinned):
//   - scalars: exact equality.
//   - plain repeatable lists: order-sensitive exact equality.
//   - Unique flags: order-insensitive multiset equality.
//
// When the two values are equal, config+CLI/env co-presence is NOT a conflict.
func valuesEqualForConflict(cliVal, configVal interface{}, f *Flag) bool {
	if f.Unique {
		cliArr, ok1 := cliVal.([]interface{})
		cfgArr, ok2 := configVal.([]interface{})
		if ok1 && ok2 {
			return multisetEqual(cliArr, cfgArr)
		}
	}
	return reflect.DeepEqual(cliVal, configVal)
}

// multisetEqual reports whether two slices contain the same elements regardless
// of order (order-insensitive multiset comparison).
func multisetEqual(a, b []interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		counts[fmt.Sprintf("%T:%v", v, v)]++
	}
	for _, v := range b {
		counts[fmt.Sprintf("%T:%v", v, v)]--
	}
	for _, c := range counts {
		if c != 0 {
			return false
		}
	}
	return true
}

// findDuplicate returns the first duplicate value in the slice, or nil if all unique.
func findDuplicate(values []interface{}) interface{} {
	seen := make(map[interface{}]bool, len(values))
	for _, v := range values {
		if seen[v] {
			return v
		}
		seen[v] = true
	}
	return nil
}

// formatValueForError formats a value for inclusion in error messages (without quotes).
// Floats always include a decimal point. Bools are lowercase.
func formatValueForError(value interface{}) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return formatFloatCanonical(v)
	case int:
		return strconv.Itoa(v)
	case string:
		return v
	case map[string]interface{}:
		return formatDictForDisplay(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// formatDictForDisplay renders a dict flag value as canonical, deterministic
// text for errors and help: keys sorted ascending, each rendered as
// "key=value" (mirroring the CLI input syntax), values formatted via
// formatValueForError, joined by ", ". Go's fmt "%v" also sorts map keys, but
// its "map[k:v]" form is a Go-ism that does not match the input syntax; this
// makes the canonical form explicit and guaranteed stable.
func formatDictForDisplay(m map[string]interface{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + formatValueForError(m[k])
	}
	return strings.Join(parts, ", ")
}

// coerceToScalar coerces a raw string to a scalar FlagType.
// For TypeStr, resolves @-prefix. For TypeInt/TypeFloat, does strict parsing.
// Returns (coerced value, error string).
func coerceToScalar(flagName, raw string, scalarType FlagType, stdinConsumedBy **string) (interface{}, string) {
	switch scalarType {
	case TypeInt:
		intVal, err := parseIntStrict(raw)
		if err != nil {
			return nil, fmt.Sprintf("--%s: %s", flagName, err.Error())
		}
		return intVal, ""
	case TypeFloat:
		return parseFloatStrict(flagName, raw)
	case TypeStr:
		if stdinConsumedBy != nil {
			return resolveAtPrefix(flagName, raw, stdinConsumedBy)
		}
		return raw, ""
	default:
		return raw, ""
	}
}

// parseDictValue parses a single dict flag value from the CLI.
// Accepts either "key=value" format or a JSON string starting with '{'.
// Returns (parsed map entries, error string).
func parseDictValue(flagName, raw string, valueType FlagType) (map[string]interface{}, string) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "{") {
		// JSON object input
		var jsonMap map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &jsonMap); err != nil {
			return nil, fmt.Sprintf("--%s: invalid JSON: %s", flagName, err.Error())
		}
		// Coerce all values to the declared value type
		result := make(map[string]interface{}, len(jsonMap))
		for k, v := range jsonMap {
			coerced, errStr := coerceJSONValueToScalar(v, valueType)
			if errStr != "" {
				return nil, fmt.Sprintf("--%s: JSON key %q: %s", flagName, k, errStr)
			}
			result[k] = coerced
		}
		return result, ""
	}
	// key=value format: split on first '='
	eqIdx := strings.Index(raw, "=")
	if eqIdx < 0 {
		return nil, fmt.Sprintf("--%s: expected key=value or JSON, got '%s'", flagName, raw)
	}
	key := raw[:eqIdx]
	valStr := raw[eqIdx+1:]
	if key == "" {
		return nil, fmt.Sprintf("--%s: empty key in key=value pair", flagName)
	}
	// Coerce the value
	var coerced interface{}
	switch valueType {
	case TypeInt:
		intVal, err := parseIntStrict(valStr)
		if err != nil {
			return nil, fmt.Sprintf("--%s: value for key %q: %s", flagName, key, err.Error())
		}
		coerced = intVal
	case TypeFloat:
		floatVal, err := parseFloatStrictValue(valStr)
		if err != nil {
			return nil, fmt.Sprintf("--%s: value for key %q: %s", flagName, key, err.Error())
		}
		coerced = floatVal
	default:
		coerced = valStr
	}
	return map[string]interface{}{key: coerced}, ""
}

// parseDictEnvValue parses a JSON string from an env var for a dict flag.
// Returns (parsed map, error string).
func parseDictEnvValue(flagName, envVal string, valueType FlagType) (map[string]interface{}, string) {
	var jsonMap map[string]interface{}
	if err := json.Unmarshal([]byte(envVal), &jsonMap); err != nil {
		return nil, fmt.Sprintf("--%s: expected JSON object in env var, got invalid JSON", flagName)
	}
	result := make(map[string]interface{}, len(jsonMap))
	for k, v := range jsonMap {
		coerced, errStr := coerceJSONValueToScalar(v, valueType)
		if errStr != "" {
			return nil, fmt.Sprintf("--%s: env var JSON key %q: %s", flagName, k, errStr)
		}
		result[k] = coerced
	}
	return result, ""
}

// coerceJSONValueToScalar coerces a JSON-decoded value to a scalar FlagType.
// JSON numbers are float64 by default; this handles int coercion.
func coerceJSONValueToScalar(value interface{}, scalarType FlagType) (interface{}, string) {
	switch scalarType {
	case TypeStr:
		if s, ok := value.(string); ok {
			return s, ""
		}
		return nil, fmt.Sprintf("expected string, got %s", typeName(value))
	case TypeInt:
		if fv, ok := value.(float64); ok {
			intVal := int(fv)
			if float64(intVal) == fv {
				return intVal, ""
			}
			return nil, "expected integer, got float"
		}
		return nil, fmt.Sprintf("expected integer, got %s", typeName(value))
	case TypeFloat:
		if fv, ok := value.(float64); ok {
			return fv, ""
		}
		return nil, fmt.Sprintf("expected float, got %s", typeName(value))
	}
	return value, ""
}

// storeDictValue merges dict entries into the per-flag CLI value map.
// Returns an error string (empty on success).
func storeDictValue(cliByFlag map[*Flag]interface{}, f *Flag, entries map[string]interface{}) string {
	if existing, ok := cliByFlag[f]; ok {
		m := existing.(map[string]interface{})
		for k, v := range entries {
			m[k] = v
		}
	} else {
		m := make(map[string]interface{}, len(entries))
		for k, v := range entries {
			m[k] = v
		}
		cliByFlag[f] = m
	}
	return ""
}

// storeCLIValue records one coerced occurrence, appending for a repeatable flag
// and overwriting for a scalar one. The map is keyed by the DECLARATION rather
// than by name, because a name may be declared in several mutually exclusive
// scopes (contract §24.7).
func storeCLIValue(cliByFlag map[*Flag]interface{}, f *Flag, value interface{}) string {
	if !f.Repeatable {
		cliByFlag[f] = value
		return ""
	}
	if existing, ok := cliByFlag[f]; ok {
		cliByFlag[f] = append(existing.([]interface{}), value)
	} else {
		cliByFlag[f] = []interface{}{value}
	}
	if f.Unique {
		if dup := findDuplicate(cliByFlag[f].([]interface{})); dup != nil {
			return errFlagDuplicateValue(f.Name, formatValueForError(dup))
		}
	}
	return ""
}

// parseFlagRawValue parses a raw string value for a flag, handling scalar,
// list, and dict types, and records it against the flag's declaration.
// Returns error string (empty on success).
func parseFlagRawValue(f *Flag, raw string, cliByFlag map[*Flag]interface{}, stdinConsumedBy **string) string {
	if IsDictType(f.Type) {
		entries, errStr := parseDictValue(f.Name, raw, ItemType(f.Type))
		if errStr != "" {
			return errStr
		}
		return storeDictValue(cliByFlag, f, entries)
	}
	// For list flags, coerce using the item type
	scalarType := f.Type
	if IsListType(f.Type) {
		scalarType = ItemType(f.Type)
	}
	val, errStr := coerceToScalar(f.Name, raw, scalarType, stdinConsumedBy)
	if errStr != "" {
		return errStr
	}
	return storeCLIValue(cliByFlag, f, val)
}

// resolveFlagEnvValue coerces one env var value for a flag, applying the same
// compound, repeatable and @-prefix rules the CLI path applies. It is shared by
// the root-scope env pass and the scoped-flag pass, so a conditional binding
// resolves exactly as an unconditional one does when its scope is elected
// (contract §24.6).
func resolveFlagEnvValue(f *Flag, envVal string, stdinConsumedBy **string) (interface{}, string) {
	// Compound types: dict parses JSON from env, list uses env_separator
	if IsDictType(f.Type) {
		entries, errStr := parseDictEnvValue(f.Name, envVal, ItemType(f.Type))
		if errStr != "" {
			return nil, errWrappedFromEnvVar(errStr, f.Env)
		}
		return entries, ""
	}
	if IsListType(f.Type) {
		if f.EnvSeparator == "" {
			return nil, errListFlagEnvRequiresSeparator(f.Name)
		}
		parts := splitEscaped(envVal, f.EnvSeparator[0])
		elemType := ItemType(f.Type)
		coercedList := make([]interface{}, 0, len(parts))
		for _, element := range parts {
			val, errStr := coerceToScalar(f.Name, element, elemType, nil)
			if errStr != "" {
				return nil, errWrappedFromEnvVar(errStr, f.Env)
			}
			coercedList = append(coercedList, val)
		}
		if f.Unique {
			if dup := findDuplicate(coercedList); dup != nil {
				return nil, errFlagDuplicateValueFromEnv(f.Name, formatValueForError(dup), f.Env)
			}
		}
		return coercedList, ""
	}
	switch f.Type {
	case TypeBool:
		boolVal, err := parseBoolStrict(envVal)
		if err != nil {
			return nil, errInvalidBoolEnvValue(envVal, f.Env, f.Name)
		}
		return boolVal, ""
	case TypeInt:
		if f.Repeatable && f.EnvSeparator != "" {
			parts := splitEscaped(envVal, f.EnvSeparator[0])
			coercedList := make([]interface{}, 0, len(parts))
			for _, element := range parts {
				intVal, err := parseIntStrict(element)
				if err != nil {
					return nil, errFlagErrFromEnvVar(f.Name, err.Error(), f.Env)
				}
				coercedList = append(coercedList, intVal)
			}
			if f.Unique {
				if dup := findDuplicate(coercedList); dup != nil {
					return nil, errFlagDuplicateValueFromEnv(f.Name, formatValueForError(dup), f.Env)
				}
			}
			return coercedList, ""
		} else {
			intVal, err := parseIntStrict(envVal)
			if err != nil {
				return nil, errFlagErrFromEnvVar(f.Name, err.Error(), f.Env)
			}
			if f.Repeatable {
				return []interface{}{intVal}, ""
			} else {
				return intVal, ""
			}
		}
	case TypeFloat:
		if f.Repeatable && f.EnvSeparator != "" {
			parts := splitEscaped(envVal, f.EnvSeparator[0])
			coercedList := make([]interface{}, 0, len(parts))
			for _, element := range parts {
				floatVal, errStr := parseFloatStrict(f.Name, element)
				if errStr != "" {
					return nil, errWrappedFromEnvVar(errStr, f.Env)
				}
				coercedList = append(coercedList, floatVal)
			}
			if f.Unique {
				if dup := findDuplicate(coercedList); dup != nil {
					return nil, errFlagDuplicateValueFromEnv(f.Name, formatValueForError(dup), f.Env)
				}
			}
			return coercedList, ""
		} else {
			floatVal, errStr := parseFloatStrict(f.Name, envVal)
			if errStr != "" {
				return nil, errWrappedFromEnvVar(errStr, f.Env)
			}
			if f.Repeatable {
				return []interface{}{floatVal}, ""
			} else {
				return floatVal, ""
			}
		}
	default: // TypeStr
		if f.Repeatable && f.EnvSeparator != "" {
			parts := splitEscaped(envVal, f.EnvSeparator[0])
			coercedList := make([]interface{}, 0, len(parts))
			for _, element := range parts {
				resolved, errStr := resolveAtPrefix(f.Name, element, stdinConsumedBy)
				if errStr != "" {
					return nil, errStr
				}
				coercedList = append(coercedList, resolved)
			}
			if f.Unique {
				if dup := findDuplicate(coercedList); dup != nil {
					return nil, errFlagDuplicateValueFromEnv(f.Name, formatValueForError(dup), f.Env)
				}
			}
			return coercedList, ""
		} else {
			resolved, errStr := resolveAtPrefix(f.Name, envVal, stdinConsumedBy)
			if errStr != "" {
				return nil, errStr
			}
			if f.Repeatable {
				return []interface{}{resolved}, ""
			} else {
				return resolved, ""
			}
		}
	}
}
