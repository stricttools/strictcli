package strictcli

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// invokeResult holds the outcome of an invoke call.
type invokeResult struct {
	exitCode int
	data     interface{} // the machine payload the handler supplied (nil otherwise)
	err      string      // non-empty if invocation failed
	// early is set when the handler ended through ExitNow (§19.9): the caller
	// receives an *ExitError rather than a value.
	early *exitNowSignal
}

// InvokeError is returned by App.Call() when invocation fails
// (unknown command, missing flags, mutex violations, etc.).
type InvokeError struct {
	Message string
}

// Error returns the error message describing the invocation failure.
func (e *InvokeError) Error() string {
	return e.Message
}

// callOptions carries the per-call state that is NOT a handler kwarg.
type callOptions struct {
	approveConsequential bool
}

// CallOption configures one App.Call. Go's Call takes its kwargs as a map, so
// consent cannot ride in them the way Python's keyword-only argument does --
// it is a variadic option instead, which is this package's existing shape for
// "a declaration that is not data".
type CallOption func(*callOptions)

// WithApproveConsequential is the caller's explicit consent on the
// programmatic path, the counterpart of the CLI's --approve-consequential. A
// command that declares itself consequential is refused without it; read-only
// and plain mutating commands are unaffected.
func WithApproveConsequential() CallOption {
	return func(o *callOptions) { o.approveConsequential = true }
}

// buildCallOptions folds the variadic options into a value.
func buildCallOptions(opts []CallOption) callOptions {
	var o callOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// invoke executes a command programmatically by path and pre-typed kwargs,
// bypassing CLI parsing, env var resolution, and config loading. The caller
// provides fully-typed values for all non-defaultable parameters.
//
// commandPath uses dot-separated segments: "deploy", "dns.zone.create".
// kwargs keys use underscored parameter names (e.g., "dry_run", not "--dry-run").
//
// For passthrough commands, the special key "_args" must contain a []string
// of raw arguments to forward to the handler.
//
// opts carries the caller's consent: a consequential command is refused
// without WithApproveConsequential().
func (a *App) invoke(commandPath string, kwargs map[string]interface{}, opts ...CallOption) invokeResult {
	co := buildCallOptions(opts)

	// Validate registrations
	if errMsg := a.validateCheckRegistrations(); errMsg != "" {
		return invokeResult{exitCode: 1, err: errMsg}
	}
	if errMsg := a.validateTagContracts(); errMsg != "" {
		return invokeResult{exitCode: 1, err: errMsg}
	}

	// Split command path into route segments
	segments := strings.Split(commandPath, ".")

	// Resolve the command
	route := a.resolveCommand(segments)
	if route.err != "" {
		return invokeResult{exitCode: 1, err: route.err}
	}
	if route.cmd == nil {
		return invokeResult{exitCode: 1, err: "no command resolved from path: " + commandPath}
	}

	cmd := route.cmd

	// The consent check (contract §8.5). There is no terminal here, so the
	// confirm protocol's PROMPT cannot fire -- the caller must have said so in
	// the call. Checked before anything is dispatched or recorded.
	if cmd.Consequential && !co.approveConsequential {
		return invokeResult{exitCode: 1, err: errCallConsequentialUnconsented(commandPath)}
	}

	// Programmatic dispatch: --dry-run is not reachable (argv parsing is
	// bypassed entirely) and the confirm protocol's prompt never fires -- these
	// paths have no TTY contract and a prompt there would hang the caller. The
	// requirement itself is honoured above, and the caller's consent is
	// delivered to the handler on the Context.
	a.beginDispatch()

	// Record test-coverage hit (command-level only).
	if a.coverageShardPath != "" {
		a.recordCoverage(commandPath)
	}

	// Handle passthrough commands
	if cmd.Passthrough {
		var args []string
		if rawArgs, ok := kwargs["_args"]; ok {
			if typedArgs, ok := rawArgs.([]string); ok {
				args = typedArgs
			} else {
				return invokeResult{exitCode: 1, err: errPassthroughArgsNotStringSlice}
			}
		}

		// Build set of known global flag param names
		globalParamNames := make(map[string]bool)
		for _, gf := range a.globalFlags {
			globalParamNames[flagParamName(gf.Name)] = true
		}

		// Validate that all kwargs keys are either "_args" or known global flags
		for key := range kwargs {
			if key == "_args" {
				continue
			}
			if !globalParamNames[key] {
				return invokeResult{exitCode: 1, err: errUnknownParameterForPassthroughCommand(key, commandPath)}
			}
		}

		// Build global kwargs from the remaining kwargs entries
		globalKwargs := make(map[string]interface{})
		for _, gf := range a.globalFlags {
			paramName := flagParamName(gf.Name)
			if v, ok := kwargs[paramName]; ok {
				globalKwargs[paramName] = v
			} else {
				val, _, errMsg := applyFlagDefault(&gf, "global ", a.infraRoots)
				if errMsg != "" {
					return invokeResult{exitCode: 1, err: errMsg}
				}
				globalKwargs[paramName] = val
			}
		}
		ctx := newContext(io.Discard, io.Discard, nil, a.infraAccess(false),
			reservedFlags{approveConsequential: co.approveConsequential},
			a.armEffects(cmd, commandPath, false, nil))
		ctx.bindCommand(cmd)
		code, truncErr, early := a.invokeSealed(ctx, func() int {
			return cmd.PassthroughHandler(ctx, cmd.Name, args, globalKwargs)
		})
		if truncErr != "" {
			return invokeResult{exitCode: 1, err: truncErr}
		}
		if early != nil {
			return invokeResult{exitCode: early.code, data: ctx.payload, early: early}
		}
		return invokeResult{exitCode: code}
	}

	// The properties this command publishes at the flat boundary, each under
	// the PARAMETER spelling a handler receives (§24.11).
	props := buildFlatProps(a, cmd)

	// SHAPE precedes every phase. A key naming nothing the command declares is
	// a fact about the object's shape, and it outranks every election, scope,
	// value and presence problem the same object also contains -- exactly as an
	// unknown flag outranks all four on the command line wherever it sits in
	// argv (§24.11, §18.23 item 238). It therefore runs BEFORE the declaration
	// walk below, whose own refusals (an unelectable selector value, a record
	// naming a foreign choice) are phase facts.
	for paramName := range kwargs {
		if props.declares(paramName) {
			continue
		}
		return invokeResult{
			exitCode: 1,
			err:      errUnknownParameterForCommand(paramName, commandPath),
		}
	}

	// The same fact one level down. An elected record's key namespace is the
	// ELECTED CHOICE'S OWN SCOPE, so a key outside it names nothing, at any
	// depth, and is refused with this same sentence plus the clause that says
	// where (§24.11 item 246).
	if errStr := checkRecordShape(cmd, kwargs, commandPath); errStr != "" {
		return invokeResult{exitCode: 1, err: errStr}
	}

	// Populate sourcedStore from kwargs, mapping param names back to flag names.
	// Provided kwargs are marked SourceCLI; absent flags will get SourceDefault
	// when validateAndBuildKwargs applies defaults.
	store := newSourcedStore()
	positionals := map[string]interface{}{}
	// The properties this call CLEARED, spelled `null` on the property's own
	// key at this door (§27.6).
	unsets := map[string]bool{}

	// A selector's value on the programmatic front door is the same record a
	// handler receives -- Elect(<choice>, Fields{...}) -- or, at the machine
	// boundary, the flat form: the choice name under the selector's own key,
	// with every scoped parameter a top-level key. Both are converted here into
	// the SAME election and value input the argv path produces, so the two front
	// doors agree by construction rather than by test (contract §24.11).
	sup := newSuppliedElections()
	cliByFlag := make(map[*Flag]interface{})
	var scopedBinds []pendingBind
	if cmd.index != nil && cmd.index.hasSelectors {
		binds, errStr := collectInvokeElections(cmd, kwargs, sup, props.scoped)
		if errStr != "" {
			return invokeResult{exitCode: 1, err: errStr}
		}
		scopedBinds = binds
	}

	// Every value this call supplied, held until the election phase has run
	// command-wide. Checking where the value is read would let an earlier
	// scope's value problem outrank a later selector's election refusal, which
	// inverts the pinned phase order (§24.3, §24.11).
	rootValues := make(map[*Flag]interface{})
	globalValues := make(map[*Flag]interface{})
	for paramName, value := range kwargs {
		if f, ok := props.flags[paramName]; ok {
			if f.Type == TypeChoice {
				continue // already folded into the election input
			}
			rootValues[f] = value
			continue
		}
		if gf, ok := props.globals[paramName]; ok {
			globalValues[gf] = value
			continue
		}
		// A positional arg, or a scoped parameter supplied at the top level --
		// the latter legal at this boundary, where the schema is flat (§24.11),
		// and refused by the SAME scope machinery the CLI parser uses when the
		// combination is wrong. Both are read after the election phase.
	}

	amb := ambientSource{hermetic: true}
	est, electErr := elect(cmd, sup, amb)
	if electErr != "" {
		return invokeResult{exitCode: 1, err: electErr}
	}
	var suppliedOrder []string
	for name := range sup.suppliedNames {
		suppliedOrder = append(suppliedOrder, name)
	}
	sort.Strings(suppliedOrder)
	if errStr := est.checkScope(suppliedOrder); errStr != "" {
		return invokeResult{exitCode: 1, err: errStr}
	}

	// Phase 4 begins here: election and scope are settled command-wide, so a
	// value may now be refused. A flat object and an elected record have no
	// order of their own, so the sweep is DECLARATION order -- which is what
	// §21.4 already uses wherever an order-free object has to be reported in
	// some order -- and it is ONE sweep over the command's declarations rather
	// than one pass per kind: the command's own flags and the values inside
	// each elected scope interleave exactly where the declarations sit, at
	// every depth, so a flag declared before a selector reports its value
	// problem ahead of anything inside that selector's scope and a nested
	// selector's values sit where the nested selector is declared. Sweeping
	// every scope first would let a value one level down outrank one on a flag
	// declared above the selector, which is the order neither the declarations
	// nor an argv written in their order produces. The app's globals follow the
	// command's own declarations, and the positionals are checked where every
	// positional is resolved, in the shared pipeline's own positional phase.
	for i := range cmd.flags {
		f := &cmd.flags[i]
		if f.Type == TypeChoice {
			for _, b := range scopedBinds {
				if b.sel != f {
					continue
				}
				if b.record {
					// The record door's own field check, and the record door's
					// own label on what it delivers (§18.26 items 252, 253).
					checked, absent, errStr := checkRecordFieldValue(b.flag, b.raw)
					if errStr != "" {
						return invokeResult{exitCode: 1, err: errStr}
					}
					if absent {
						// The nil a record writes on an optional field IS the
						// absent key, so it is delivered by the declaration's
						// own optional path -- the one an omitted key takes.
						continue
					}
					cliByFlag[b.flag] = recordSupplied{value: checked}
					continue
				}
				checked, errStr := checkPreTypedValue(b.flag, b.raw)
				if errStr != "" {
					return invokeResult{exitCode: 1, err: errStr}
				}
				cliByFlag[b.flag] = checked
			}
			continue
		}
		raw, ok := rootValues[f]
		if !ok {
			continue
		}
		// The one stated carve-out from "null is legal for nothing" (§24.11's
		// amendment, §27.6): a NULLABLE PROPERTY is the one declaration for
		// which null is a VALUE -- it means clear this property on the
		// resource, which is a write. It delivers absence, reports Provided()
		// true, and is what ctx.Unset answers true for. The carve-out is a
		// property of the DECLARATION and never of the door: a null anywhere
		// else is refused by the rule below, unchanged.
		if raw == nil && f.Nullable {
			unsets[f.Name] = true
			store.set(f.Name, nil, SourceCLI)
			continue
		}
		checked, errStr := checkPreTypedValue(f, raw)
		if errStr != "" {
			return invokeResult{exitCode: 1, err: errStr}
		}
		store.set(f.Name, checked, SourceCLI)
	}

	for i := range a.globalFlags {
		gf := &a.globalFlags[i]
		raw, ok := globalValues[gf]
		if !ok {
			continue
		}
		checked, errStr := checkPreTypedValue(gf, raw)
		if errStr != "" {
			return invokeResult{exitCode: 1, err: errStr}
		}
		store.set(gf.Name, checked, SourceCLI)
	}

	// The positionals this call supplied, under the arg names it supplied them
	// under. The values are handed on as supplied; the declaration checks them
	// where every positional is resolved, and binds each to the arg its key
	// names (§24.11 item 244).
	for i := range cmd.args {
		arg := &cmd.args[i]
		if val, ok := kwargs[arg.Name]; ok {
			positionals[arg.Name] = val
		}
	}

	// Run validation and build final kwargs
	var noStdin *string
	validatedKwargs, postGlobalValues, sources, writes, errStr := validateAndBuildKwargs(cmd, store, preTypedPositionals(positionals), props.globalNames, a.infraRoots, est, cliByFlag, amb, &noStdin, unsets)
	if errStr != "" {
		return invokeResult{exitCode: 1, err: errStr}
	}

	// Merge global values into kwargs (same as doParse)
	for k, v := range postGlobalValues {
		validatedKwargs[k] = v
	}

	// Apply global flag defaults for globals not provided in kwargs
	for i := range a.globalFlags {
		gf := &a.globalFlags[i]
		paramName := flagParamName(gf.Name)
		if _, ok := validatedKwargs[paramName]; ok {
			continue
		}
		// Use value from store if provided
		if v, ok := store.get(gf.Name); ok {
			validatedKwargs[paramName] = v
			continue
		}
		// Apply defaults
		val, src, errMsg := applyFlagDefault(gf, "global ", a.infraRoots)
		if errMsg != "" {
			return invokeResult{exitCode: 1, err: errMsg}
		}
		validatedKwargs[paramName] = val
		if sources != nil {
			sources[paramName] = sourceLabelString(src)
		}
	}

	// Context is constructed unconditionally. For invoke, stdout/stderr are
	// discarded -- the machine payload flows back through the Context, not
	// stdout.
	ctx := newContext(io.Discard, io.Discard, sources, a.infraAccess(false),
		reservedFlags{approveConsequential: co.approveConsequential},
		a.armEffects(cmd, commandPath, false, nil))
	ctx.bindCommand(cmd)
	ctx.writes = writes
	ctx.unsets = unsets

	// Call the handler under the runtime seal.
	code, truncErr, early := a.invokeSealed(ctx, func() int {
		return cmd.Handler(ctx, validatedKwargs).code
	})
	if truncErr != "" {
		return invokeResult{exitCode: 1, err: truncErr}
	}
	if early != nil {
		return invokeResult{exitCode: early.code, data: ctx.payload, early: early}
	}
	// The programmatic surface keeps its capture: it returns the payload the
	// handler supplied (contract §19.4).
	return invokeResult{exitCode: code, data: ctx.payload}
}

// invokeSealed runs a handler on the programmatic path under the runtime seal.
// A carrier extraction surfaces as an InvokeError carrying the pinned
// truncation text; any other panic is re-raised untouched.
//
// It waits for every function started through Go (§19.9), settles the
// children the handler spawned and left unwaited -- a child still running is
// killed and turns a zero status into 1, though this door has no stream to
// name it on (§19.11's box) -- recognizes the early exit, and cancels the
// handler's context when the handler is done.
func (a *App) invokeSealed(ctx *Context, fn func() int) (code int, truncErr string, early *exitNowSignal) {
	defer func() {
		r := handlerUnwind(ctx, recover())
		if killed := ctx.effects.settleChildren(); len(killed) > 0 && r == nil && code == 0 {
			code = 1
		}
		ctx.cancel()
		switch v := r.(type) {
		case nil:
		case exitNowSignal:
			early = &v
		case dryRunTruncation:
			code = 1
			truncErr = v.message
		default:
			panic(r)
		}
	}()
	return fn(), "", nil
}

// flatProps is every property a command publishes at the flat boundary, keyed
// by the PARAMETER spelling -- underscored, exactly as a handler receives it,
// and exactly as the schema publishes it (§24.11). A flag's own dashed spelling
// is therefore a key the command does not declare: the boundary reads the
// command line the flat object spells, but it is not the command line, and one
// property has one name.
type flatProps struct {
	// flags are the command's own root declarations, selectors included.
	flags map[string]*Flag
	// scoped is every declaration inside a scope, at every depth, including a
	// member's payload key -- each a property of the flat schema, so supplying
	// one is a scope question and never a shape one.
	scoped map[string]*Flag
	// globals are the app-level flags.
	globals map[string]*Flag
	// globalNames is the same set under the dashed spelling the parse pipeline
	// keys its store by.
	globalNames map[string]bool
	// args are the positional args, under their declared names.
	args map[string]bool
}

// declares reports whether one kwargs key names something this command has.
func (p *flatProps) declares(paramName string) bool {
	if _, ok := p.flags[paramName]; ok {
		return true
	}
	if _, ok := p.scoped[paramName]; ok {
		return true
	}
	if _, ok := p.globals[paramName]; ok {
		return true
	}
	return p.args[paramName]
}

// buildFlatProps indexes one command's flat properties.
func buildFlatProps(a *App, cmd *Command) *flatProps {
	p := &flatProps{
		flags:       make(map[string]*Flag, len(cmd.flags)),
		scoped:      map[string]*Flag{},
		globals:     make(map[string]*Flag, len(a.globalFlags)),
		globalNames: make(map[string]bool, len(a.globalFlags)),
		args:        make(map[string]bool, len(cmd.args)),
	}
	for i := range cmd.flags {
		p.flags[flagParamName(cmd.flags[i].Name)] = &cmd.flags[i]
	}
	if cmd.index != nil {
		for _, name := range cmd.index.order {
			for _, site := range cmd.index.sites[name] {
				if len(site.path) > 0 {
					p.scoped[flagParamName(name)] = site.flag
				}
			}
		}
	}
	for i := range a.globalFlags {
		p.globals[flagParamName(a.globalFlags[i].Name)] = &a.globalFlags[i]
		p.globalNames[a.globalFlags[i].Name] = true
	}
	for _, arg := range cmd.args {
		p.args[arg.Name] = true
	}
	return p
}

// checkRecordShape refuses a key inside an elected record that the elected
// scope does not declare (contract §24.11 item 246, §24.3).
//
// The record door's key namespace is the elected choice's OWN scope: the
// payload key where the choice carries one, and the parameters that scope
// declares at that level. A key outside that set names nothing -- a fact about
// the object's SHAPE, decided ahead of every election, scope, value and
// presence problem the same call contains, which is why this runs before the
// declaration walk that settles the elections.
//
// The sentence is the flat door's own with §12.13's scope suffix on it: the
// fact is the same fact one level down, and the suffix is the clause that says
// where. §12.13's out-of-scope template is deliberately NOT used -- it names a
// flag the command declares against the scope that owns it, and a key naming
// nothing anywhere has no other side to name.
//
// A record naming a choice this selector does not declare is skipped: without a
// scope there is no namespace to read the keys against, and the election phase
// refuses that record on its own.
func checkRecordShape(cmd *Command, kwargs map[string]interface{}, commandPath string) string {
	var walk func(flags []Flag, args map[string]interface{}, path []pathSeg) string
	walk = func(flags []Flag, args map[string]interface{}, path []pathSeg) string {
		for i := range flags {
			f := &flags[i]
			if f.Type != TypeChoice {
				continue
			}
			rec, isRecord := args[flagParamName(f.Name)].(*Elected)
			if !isRecord {
				// A flat election is descended THROUGH rather than checked: its
				// keys are the command's own top-level ones, which the shape
				// sweep above has already read, but a nested selector under it
				// may still carry a record.
				if ch := flatElectedChoice(f, args); ch != nil {
					if errStr := walk(ch.Flags, args, append(append([]pathSeg{}, path...), pathSeg{sel: f, ch: ch})); errStr != "" {
						return errStr
					}
				}
				continue
			}
			ch := findChoice(f, rec.decl.Name)
			if ch != rec.decl {
				continue
			}
			scope := append(append([]pathSeg{}, path...), pathSeg{sel: f, ch: ch})
			if errStr := unknownRecordKey(ch, rec.Fields, scope, commandPath); errStr != "" {
				return errStr
			}
			if errStr := walk(ch.Flags, rec.Fields, scope); errStr != "" {
				return errStr
			}
		}
		return ""
	}
	return walk(cmd.flags, kwargs, nil)
}

// flatElectedChoice reports the choice the flat spelling elects for one
// selector, or nil when nothing settles. The shape sweep reads an election only
// to reach the records nested under it; refusing one is the election phase's
// own duty, and an unsettled election simply ends this walk.
func flatElectedChoice(sel *Flag, args map[string]interface{}) *ChoiceDecl {
	if v, isName := args[flagParamName(sel.Name)].(string); isName {
		return findChoice(sel, v)
	}
	if !sel.memberSpelled {
		return nil
	}
	var only *ChoiceDecl
	for _, ch := range sel.choiceDecls {
		value, supplied := args[flagParamName(ch.Name)]
		if !supplied {
			continue
		}
		// A payload-less member DECLINES on the false its `--no-<name>` token
		// would have carried (§21.2).
		if !choiceCarriesPayload(ch) {
			if b, isBool := value.(bool); isBool && !b {
				continue
			}
		}
		if only != nil {
			return nil
		}
		only = ch
	}
	return only
}

// unknownRecordKey reports the refusal for one record's first undeclared key.
// A map has no order, so the keys are sorted: the refusal names one key, and
// which one it names is the declaration's business rather than the runtime's.
func unknownRecordKey(ch *ChoiceDecl, fields Fields, scope []pathSeg, commandPath string) string {
	declared := make(map[string]bool, len(ch.Flags)+1)
	if choiceCarriesPayload(ch) {
		declared[scopeReservedValueName] = true
	}
	for i := range ch.Flags {
		declared[flagParamName(ch.Flags[i].Name)] = true
	}
	var unknown []string
	for key := range fields {
		if !declared[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	sort.Strings(unknown)
	return errUnknownParameterForCommand(unknown[0], commandPath) +
		errScopeSuffix(renderScopePath(scope))
}

// checkPreTypedValue checks one PRE-TYPED value against the declaration it was
// supplied against, and returns it in the representation the parse pipeline
// carries (contract §24.11, §23.4, §18.23 item 240).
//
// The two programmatic doors hand the framework values that are already typed,
// so nothing parses them -- but the declaration still decides what they may be,
// exactly as it does for a token that has to be parsed first. *Pre-typed* means
// ALREADY OF THE DECLARED TYPE, never exempt from the declaration: the closed-set
// half of the declaration (Choices) is consulted on this path already, so the
// type half is consulted too. The check is the one the config reader runs over
// an already-typed document, because a flat object and a config document pose
// the identical question, and the sentences are therefore the ones that reader
// already prints.
//
// nil is a legal value for nothing. Optionality has ONE spelling (§23.4): a flag
// that may be absent declares Optional() and is delivered absent when its key is
// simply not there, so a null says nothing the declaration cannot already say --
// and on a required flag it would answer the presence rule with a value the
// declaration forbids.
func checkPreTypedValue(f *Flag, value interface{}) (interface{}, string) {
	if IsDictType(f.Type) {
		// A dict flag keeps its own SHAPE refusal, which names the Go type the
		// caller actually handed over; its entries are then checked like any
		// others.
		m, errStr := coerceInvokeDict(f, value)
		if errStr != "" {
			return nil, errStr
		}
		value = m
	}
	coerced, errStr := coerceConfigValue(normalizePreTyped(value), f)
	if errStr != "" {
		return nil, errFlagValueError(f.Name, errStr)
	}
	return coerced, ""
}

// checkRecordFieldValue checks one field of a supplied RECORD against the
// declaration it was supplied against (contract §24.11, §18.26 items 252, 254).
//
// It is checkPreTypedValue with ONE carve-out, and the carve-out is this door's
// alone: a scope class cannot omit a field the way a flat object omits a key,
// so an explicit nil on an OPTIONAL field IS that omission. Such a field is
// reported ABSENT rather than checked, and the caller delivers it the way an
// omitted key is delivered -- through the declaration's own optional path. The
// carve-out stops at optionality: a required field's presence rule would
// otherwise be answered by a value its declared type forbids, and a defaulted
// field's declaration says a value, not absence. At the flat door, where
// absence has its own spelling, a nil stays legal for nothing.
//
// The check itself is the TYPE half of the declaration and no other half: the
// closed set and the custom validation are deferred at this door, because it
// cannot tell a field the caller supplied from one the declaration filled in
// (§18.26 items 253, 254), and §23.4 forbids running validate on a value the
// declaration decided. Those two halves are skipped where the shared pipeline
// would run them, which is what the recordSupplied marker carries.
func checkRecordFieldValue(f *Flag, value interface{}) (interface{}, bool, string) {
	if value == nil && f.presence == presenceOptional {
		return nil, true, ""
	}
	checked, errStr := checkPreTypedValue(f, value)
	return checked, false, errStr
}

// checkPreTypedArgValue checks one PRE-TYPED positional value against its
// declaration, and delivers the value itself (contract §24.11 item 244).
//
// A positional is declared with the same presence spellings and the same four
// types a flag is (§23.3), so nothing about being a positional makes a supplied
// value exempt from its declaration: an int is not a string arg's value and a
// null is nothing's. Stringifying instead would not merely skip the check, it
// would MANUFACTURE an argv -- a token the caller never wrote, re-parsed into
// the right answer by luck where the types happen to line up, and into the text
// of a null where they do not. The wrapper is the arg side's own, never
// `--<name>`, because that is the prefix every arg-side value refusal uses.
//
// A variadic arg is a SEQUENCE of positionals rather than one value of a
// collection type, so an array spreads into one element per entry and each
// element is checked on its own -- and anything else is the single element it
// looks like, which is the one positional a command line would have typed.
func checkPreTypedArgValue(a *Arg, value interface{}) (interface{}, string) {
	if a.IsVariadic {
		items, ok := normalizePreTyped(value).([]interface{})
		if !ok {
			items = []interface{}{value}
		}
		vals := make([]interface{}, len(items))
		for i, item := range items {
			checked, errStr := checkPreTypedArgElement(a, item)
			if errStr != "" {
				return nil, errStr
			}
			vals[i] = checked
		}
		return vals, ""
	}
	return checkPreTypedArgElement(a, value)
}

// checkPreTypedArgElement checks one pre-typed value against the type its arg
// declares.
func checkPreTypedArgElement(a *Arg, value interface{}) (interface{}, string) {
	t := a.Type
	if IsListType(t) {
		t = ItemType(t)
	}
	coerced, errStr := coerceConfigScalar(normalizePreTyped(value), t, false)
	if errStr != "" {
		return nil, fmt.Sprintf("argument '%s': %s", a.Name, errStr)
	}
	return coerced, ""
}

// normalizePreTyped converts a Go-native value into the representation a decoded
// document has, which is what the config reader's check reads: int64 and float64
// for numbers, []interface{} and map[string]interface{} for the two compounds.
// It decides what the value IS -- the fact the refusal below it must name -- and
// never whether the declaration accepts it.
func normalizePreTyped(value interface{}) interface{} {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case float32:
		return float64(v)
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = normalizePreTyped(item)
		}
		return out
	case []string:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out
	case []int:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = int64(item)
		}
		return out
	case []float64:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = item
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for k, item := range v {
			out[k] = normalizePreTyped(item)
		}
		return out
	}
	return value
}

// coerceInvokeDict converts various Go map types to map[string]interface{}.
func coerceInvokeDict(f *Flag, value interface{}) (interface{}, string) {
	switch v := value.(type) {
	case map[string]interface{}:
		return v, ""
	case map[string]string:
		result := make(map[string]interface{}, len(v))
		for k, s := range v {
			result[k] = s
		}
		return result, ""
	case map[string]int:
		result := make(map[string]interface{}, len(v))
		for k, n := range v {
			result[k] = n
		}
		return result, ""
	case map[string]float64:
		result := make(map[string]interface{}, len(v))
		for k, n := range v {
			result[k] = n
		}
		return result, ""
	default:
		return nil, errDictFlagExpectedMapType(f.Name, value)
	}
}

// Call invokes a command programmatically and returns its result.
//
// Unlike invoke(), this is the public API. It returns an InvokeError for
// parse/validation errors instead of os.Exit, making it safe for
// programmatic use.
//
// commandPath uses dot-separated segments: "deploy", "dns.zone.create".
// kwargs keys use underscored parameter names (e.g., "dry_run", not "--dry-run").
//
// For passthrough commands, the special key "_args" must contain a []string
// of raw arguments to forward to the handler.
//
// Returns:
//   - For handlers that supplied a payload through ctx.Payload: the payload
//   - For handlers that return Exit without a payload: the exit code (int)
//   - For passthrough handlers: the exit code (int)
//
// Returns an InvokeError if invocation fails (unknown command, missing
// required flags, mutex violations, dependency errors, etc.), and an
// *ExitError when the handler ran and ended through ExitNow.
func (a *App) Call(commandPath string, kwargs map[string]interface{}, opts ...CallOption) (interface{}, error) {
	ir := a.invoke(commandPath, kwargs, opts...)
	if ir.err != "" {
		return nil, &InvokeError{Message: ir.err}
	}
	if ir.early != nil {
		return nil, &ExitError{Code: ir.early.code, Message: ir.early.message, Payload: ir.data}
	}
	if ir.data != nil {
		return ir.data, nil
	}
	return ir.exitCode, nil
}

// pendingBind is one value an elected scope was given, held until the election
// phase has run command-wide. Collecting rather than coercing is what keeps a
// value refusal behind every selector's election refusal (§24.3, §24.11).
//
// sel is the command-level selector the value's scope descends from, at any
// depth. The value sweep is one declaration-ordered pass over the command's own
// flags, and sel is what lets a scope's values be swept AT the selector that
// owns them rather than before every flag the command declares.
type pendingBind struct {
	flag *Flag
	raw  interface{}
	sel  *Flag
	// record reports that the value came out of a RECORD's fields rather than
	// out of the flat object. The two doors answer three questions differently
	// -- an explicit nil on an optional field, the source a supplied field
	// earns, and which halves of the declaration are consulted -- so which door
	// supplied a value has to survive the collection pass (§18.26 items 252,
	// 253, 254).
	record bool
}

// recordSupplied wraps a value the RECORD door supplied, so the shared
// resolution path can tell it from a value the flat door or the command line
// supplied (contract §18.26 items 253, 254).
//
// Every field a caller's record delivers reports source `default`, and this
// door consults the TYPE half of a declaration and no other half. Both facts
// are properties of the door rather than of the value -- and the values a
// record collects re-enter the same pipeline the argv path uses, so the door
// has to travel with the value.
type recordSupplied struct {
	value interface{}
}

// collectInvokeElections converts a programmatic call's selector arguments into
// the same election-and-value input the argv path produces (contract §24.11).
//
// Two spellings reach this boundary, and they are two FRONT DOORS rather than
// two spellings of one fact: App.Call takes the elected record pre-typed
// (Elect(<choice>, Fields{...})), while the machine boundary's flat object
// carries the choice name under the selector's own key and every scoped
// parameter as a top-level key.
//
// It returns the values the elected scopes were given, in declaration order,
// for the caller to coerce once every election is settled. Nothing here refuses
// a value: a walk over declarations is not a licence to refuse mid-walk.
//
// The two duties run as two passes, and the split is what makes the second one
// declaration-ordered: the ELECTION walk settles every selector at every depth
// first, and the BIND pass then descends the declarations of each elected scope
// in the order they are written, recursing into a nested selector where the
// nested selector is declared rather than after the scope that holds it.
func collectInvokeElections(cmd *Command, kwargs map[string]interface{}, sup *suppliedElections, scoped map[string]*Flag) ([]pendingBind, string) {
	// Every scoped parameter this call named is marked supplied, so a parameter
	// belonging to a scope that was NOT elected reaches scope validation and is
	// refused with the CLI's own sentence rather than silently ignored.
	for key := range kwargs {
		if f, ok := scoped[key]; ok {
			sup.suppliedNames[f.Name] = true
		}
	}

	// fromRecord says whether the object THIS level reads is a record's fields.
	// The door a value came through decides how its declaration is read
	// (§18.26 item 252), and an election read out of a record's fields is a
	// FIELD of that record, so it earns the label every other field of a record
	// earns (§18.26 item 253) -- while the top-level object, at either door, is
	// the call's own kwargs.
	var walk func(flags []Flag, args map[string]interface{}, fromRecord bool) string
	walk = func(flags []Flag, args map[string]interface{}, fromRecord bool) string {
		for i := range flags {
			f := &flags[i]
			if f.Type != TypeChoice {
				continue
			}
			if fromRecord {
				sup.recordElected[f] = true
			}
			raw, named := args[flagParamName(f.Name)]
			if rec, isRecord := raw.(*Elected); named && isRecord {
				// The record front door, at either spelling: the caller handed
				// over the election already made.
				ch := findChoice(f, rec.decl.Name)
				if ch != rec.decl {
					return errElectNotAChoice(f.Name, rec.decl.Name)
				}
				sup.suppliedNames[f.Name] = true
				sup.preElected[f] = ch
				if errStr := walk(ch.Flags, rec.Fields, true); errStr != "" {
					return errStr
				}
				continue
			}
			if f.memberSpelled {
				if errStr := collectFlatMemberElections(f, args, named, raw, sup, walk, fromRecord); errStr != "" {
					return errStr
				}
				continue
			}
			if !named {
				continue
			}
			v, isName := raw.(string)
			if !isName {
				return errSelectorValueNotElected(f.Name, raw)
			}
			ch := findChoice(f, v)
			if ch == nil {
				return errFlagInvalidChoice(f.Name, v, strings.Join(choiceNames(f), ", "))
			}
			sup.suppliedNames[f.Name] = true
			sup.preElected[f] = ch
			// The flat form's scoped parameters sit beside the selector, so
			// the same object supplies the next level too -- a record's fields
			// where this level is a record's, the top-level object otherwise.
			if errStr := walk(ch.Flags, args, fromRecord); errStr != "" {
				return errStr
			}
		}
		return ""
	}
	if errStr := walk(cmd.flags, kwargs, false); errStr != "" {
		return nil, errStr
	}

	var binds []pendingBind
	for i := range cmd.flags {
		f := &cmd.flags[i]
		if f.Type != TypeChoice {
			continue
		}
		bindSelectorValues(f, kwargs, sup, &binds, f)
	}
	return binds, ""
}

// bindSelectorValues collects the values one selector's elected scope was given,
// from whichever of the two doors supplied them.
func bindSelectorValues(sel *Flag, args map[string]interface{}, sup *suppliedElections, binds *[]pendingBind, root *Flag) {
	if rec, isRecord := args[flagParamName(sel.Name)].(*Elected); isRecord {
		// The record door: the scope's values are the record's own fields.
		if findChoice(sel, rec.decl.Name) == rec.decl {
			bindScopeValues(rec.decl, rec.Fields, sup, binds, root, true)
		}
		return
	}
	// The flat door: every scoped parameter sits beside the selector in the one
	// object that carries the selector itself.
	if ch := electedScope(sel, sup); ch != nil {
		bindScopeValues(ch, Fields(args), sup, binds, root, false)
	}
}

// electedScope reports the choice one selector elected at the flat door, or nil
// when the election did not settle -- which the election phase has already
// refused, and which is not this pass's question.
func electedScope(sel *Flag, sup *suppliedElections) *ChoiceDecl {
	if ch, ok := sup.preElected[sel]; ok {
		return ch
	}
	var only *ChoiceDecl
	for _, ch := range sel.choiceDecls {
		if !sup.memberElected[ch.Name] {
			continue
		}
		if only != nil {
			return nil
		}
		only = ch
	}
	return only
}

// bindScopeValues records the values one elected scope's flags were given,
// keyed by the declaration and IN DECLARATION ORDER, and marks every named flag
// as supplied so scope validation sees it. It refuses nothing: the values it
// collects are coerced once every election is settled (§24.3's phase order).
//
// A nested selector is descended WHERE IT IS DECLARED, so the values one level
// down sit between the scoped flags declared before and after it, exactly as an
// argv written in declaration order would type them. fields is the object that
// carries this scope's values -- a record's own fields at the record door, and
// the one top-level object at the flat door, where every depth reads the same
// one. The two spellings may be mixed, so each level answers for itself, and
// fromRecord says which object THIS level is reading: the door a value came
// through decides three of its answers (§18.26 items 252, 253, 254).
func bindScopeValues(ch *ChoiceDecl, fields Fields, sup *suppliedElections, binds *[]pendingBind, root *Flag, fromRecord bool) {
	for i := range ch.Flags {
		sub := &ch.Flags[i]
		key := flagParamName(sub.Name)
		isMember := ch.member && i == 0
		if isMember {
			if !choiceCarriesPayload(ch) {
				continue
			}
			// A member-spelled choice's payload arrives under the reserved name
			// "value" in a record, and under the member's OWN flag name in the
			// flat machine form -- which is the property name §24.11 publishes.
			if v, ok := fields["value"]; ok {
				*binds = append(*binds, pendingBind{flag: sub, raw: v, sel: root, record: fromRecord})
				sup.suppliedNames[sub.Name] = true
				continue
			}
		}
		if sub.Type == TypeChoice {
			if rec, isRecord := fields[key].(*Elected); isRecord {
				if findChoice(sub, rec.decl.Name) == rec.decl {
					bindScopeValues(rec.decl, rec.Fields, sup, binds, root, true)
				}
				continue
			}
			// The flat spelling at this level: the nested scope's values sit in
			// the same object the nested selector's own election was read from,
			// which is a record's fields when this level is a record's.
			if nested := electedScope(sub, sup); nested != nil {
				bindScopeValues(nested, fields, sup, binds, root, fromRecord)
			}
			continue
		}
		if v, ok := fields[key]; ok {
			*binds = append(*binds, pendingBind{flag: sub, raw: v, sel: root, record: fromRecord})
			sup.suppliedNames[sub.Name] = true
		}
	}
}

// collectFlatMemberElections reads a member-spelled selector's elections out of
// the flat machine object (contract §24.11, §21.4).
//
// Supplying a member's payload key IS electing that member, exactly as typing
// its token is: the flat form is the command line with the tokens removed, not
// a second election vocabulary. So an object naming one member under the
// selector's own key AND carrying another member's payload key has elected
// TWICE, and the refusal is the election phase's own -- §21.4's mutual-exclusion
// sentence, reached by handing both elections to the same machinery the argv
// path hands its tokens to, rather than by a second rule written here.
func collectFlatMemberElections(
	sel *Flag,
	args map[string]interface{},
	named bool,
	raw interface{},
	sup *suppliedElections,
	walk func([]Flag, map[string]interface{}, bool) string,
	fromRecord bool,
) string {
	elected := map[string]bool{}
	if named {
		v, isName := raw.(string)
		if !isName {
			return errSelectorValueNotElected(sel.Name, raw)
		}
		ch := findChoice(sel, v)
		if ch == nil {
			return errFlagInvalidChoice(sel.Name, v, strings.Join(choiceNames(sel), ", "))
		}
		sup.suppliedNames[sel.Name] = true
		elected[ch.Name] = true
	}
	for _, ch := range sel.choiceDecls {
		value, supplied := args[flagParamName(ch.Name)]
		if !supplied {
			continue
		}
		sup.suppliedNames[ch.Name] = true
		// A payload-less member elects on presence and DECLINES on the false its
		// `--no-<name>` token would have carried (§21.2), which is what keeps the
		// flat form and the command line one rule rather than two.
		if !choiceCarriesPayload(ch) {
			if b, isBool := value.(bool); isBool && !b {
				sup.memberElected[ch.Name] = false
				continue
			}
		}
		elected[ch.Name] = true
	}
	for name := range elected {
		sup.memberElected[name] = true
	}
	// Nothing elected, or several: the election phase is what says so, with the
	// sentence the command line would have produced.
	if len(elected) != 1 {
		return ""
	}
	var only *ChoiceDecl
	for _, ch := range sel.choiceDecls {
		if elected[ch.Name] {
			only = ch
		}
	}
	return walk(only.Flags, args, fromRecord)
}
