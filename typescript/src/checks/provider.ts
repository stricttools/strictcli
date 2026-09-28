/**
 * Check providers: the TOML-less way to add checks, where a provider returns
 * a list of fully-formed check specs (metadata plus a ceiling-typed impl).
 * Providers run lazily at the first registry read
 * (materialization), memoized per cwd, and their specs go through the same
 * single add-path as TOML-declared checks.
 *
 * Three check-system hooks (do not confuse them):
 *
 *  1. Check provider (app.registerCheckProvider) -- REGISTRY POPULATION:
 *     decides WHICH checks exist. Registering a provider ENABLES the check
 *     system (a TOML-less app with a provider gets a working `check`
 *     command).
 *  2. Check-context factory (app.setCheckContext) -- PROJECT CONSTRUCTION:
 *     called once per run with no arguments to build the CheckContext handed
 *     to every check impl. Decides WHAT project the checks see.
 *  3. Scope adapter -- Python-only per-check context projection. TS matches
 *     Go: no scope adapter, the scope field is parse-only.
 *
 * Parity sources: go/strictcli/check_provider.go with Python
 * register_check_provider / _materialize_check_providers (~3238-3342) as the
 * divergence ground truth for the runtime guards.
 */

import {
	errCheckProviderMustReturnList,
	errCheckProviderNonCheckSpec,
	errCheckProviderSeverityMismatch,
} from "../errors.js";
import { pyRepr, pyTypeName } from "../factories.js";
import {
	addCheckDef,
	type CheckContext,
	type CheckImpl,
	type CheckOutcome,
	type CheckSeverity,
	type ChecksState,
	ErrorReporter,
	WarnReporter,
} from "./framework.js";

// Module-private construction token: a CheckSpec is built ONLY via
// errorCheckSpec / warnCheckSpec, which bind the reporter form to the
// declared severity so the impl cannot mint a problem its severity forbids.
const SPEC_TOKEN = Symbol("strictcli.checks.spec");

/**
 * A fully-formed, ceiling-typed check produced by a check provider. Opaque
 * by construction: obtain one only from the errorCheckSpec / warnCheckSpec
 * builders. Providers return arrays of these.
 */
export class CheckSpec {
	readonly name: string;
	readonly tags: readonly string[];
	readonly severity: CheckSeverity;
	readonly fast: boolean;
	readonly pure: boolean;
	readonly needsNetwork: boolean;
	readonly dependsOn: readonly string[];
	readonly scope: string;
	/** Wrapped runner with the reporter already bound. */
	readonly impl: CheckImpl;
	/** "error" or "warn" -- bound by the builder, for the severity cross-check. */
	readonly implForm: CheckSeverity;

	constructor(
		token: symbol,
		meta: {
			name: string;
			tags: readonly string[];
			severity: CheckSeverity;
			fast: boolean;
			pure: boolean;
			needsNetwork: boolean;
			dependsOn: readonly string[];
			scope: string;
		},
		impl: CheckImpl,
		implForm: CheckSeverity,
	) {
		if (token !== SPEC_TOKEN) {
			throw new Error(
				"CheckSpec cannot be constructed directly; use errorCheckSpec or warnCheckSpec",
			);
		}
		this.name = meta.name;
		this.tags = [...meta.tags];
		this.severity = meta.severity;
		this.fast = meta.fast;
		this.pure = meta.pure;
		this.needsNetwork = meta.needsNetwork;
		this.dependsOn = [...meta.dependsOn];
		this.scope = meta.scope;
		this.impl = impl;
		this.implForm = implForm;
	}
}

/** Declarative metadata shared by both spec builders (8 meta fields). */
interface CheckSpecInitBase {
	readonly name: string;
	readonly tags: readonly string[];
	readonly fast: boolean;
	readonly pure: boolean;
	readonly needsNetwork: boolean;
	readonly dependsOn: readonly string[];
	/**
	 * Defaults to the builder's form. An explicit mismatching severity is
	 * accepted here and rejected at materialization (the provider analog of
	 * the TOML/register severity cross-check).
	 */
	readonly severity?: CheckSeverity;
	/** Parse-only scope string; defaults to "". */
	readonly scope?: string;
}

/** Initialization options for an error-severity check spec. The impl receives an ErrorReporter. */
export interface ErrorCheckSpecInit extends CheckSpecInitBase {
	readonly impl: (
		ctx: CheckContext,
		reporter: ErrorReporter,
	) => CheckOutcome | Promise<CheckOutcome>;
}

/** Initialization options for a warn-severity check spec. The impl receives a WarnReporter. */
export interface WarnCheckSpecInit extends CheckSpecInitBase {
	readonly impl: (
		ctx: CheckContext,
		reporter: WarnReporter,
	) => CheckOutcome | Promise<CheckOutcome>;
}

/**
 * Builds an error-severity check spec for a provider. The impl receives an
 * ErrorReporter (which can mint both error- and warn-severity problems).
 */
export function errorCheckSpec(init: ErrorCheckSpecInit): CheckSpec {
	const { impl } = init;
	return new CheckSpec(
		SPEC_TOKEN,
		{
			name: init.name,
			tags: init.tags,
			severity: init.severity ?? "error",
			fast: init.fast,
			pure: init.pure,
			needsNetwork: init.needsNetwork,
			dependsOn: init.dependsOn,
			scope: init.scope ?? "",
		},
		(ctx) => impl(ctx, new ErrorReporter()),
		"error",
	);
}

/**
 * Builds a warn-severity check spec for a provider. The impl receives a
 * WarnReporter, which structurally lacks error-minting: a warn check cannot
 * cascade.
 */
export function warnCheckSpec(init: WarnCheckSpecInit): CheckSpec {
	const { impl } = init;
	return new CheckSpec(
		SPEC_TOKEN,
		{
			name: init.name,
			tags: init.tags,
			severity: init.severity ?? "warn",
			fast: init.fast,
			pure: init.pure,
			needsNetwork: init.needsNetwork,
			dependsOn: init.dependsOn,
			scope: init.scope ?? "",
		},
		(ctx) => impl(ctx, new WarnReporter()),
		"warn",
	);
}

/** Maps a severity form to the builder an author should use (mismatch hint). */
function checkSpecCtorName(form: string): string {
	return `${form}CheckSpec`;
}

/**
 * Runs all registered providers and inserts their specs into the registry,
 * memoized on the cwd at materialization time. Single chokepoint called at
 * the start of every registry read (the check command handler and the
 * programmatic app.runChecks). A repeat call in the same cwd is a cheap
 * no-op; a cwd change re-runs the providers (dropping the previous
 * provider-sourced defs first). A throwing provider is a hard error in every
 * mode; a provider returning undefined or an empty list is honest-empty.
 *
 * Reentrancy: a provider must not trigger check execution during
 * materialization (behavior is undefined -- unbounded recursion). A
 * provider's job is to return specs, nothing else.
 */
export function materializeCheckProviders(state: ChecksState): void {
	if (state.providers.length === 0) {
		return;
	}
	const cwd = process.cwd();
	if (state.providerMaterializedCwd === cwd) {
		return;
	}
	// First materialization, or cwd changed: drop stale provider defs, re-run.
	dropProviderSourcedDefs(state);
	for (const provider of state.providers) {
		const result = provider() ?? [];
		if (!Array.isArray(result)) {
			throw new Error(errCheckProviderMustReturnList(pyTypeName(result)));
		}
		for (const spec of result) {
			if (!(spec instanceof CheckSpec)) {
				throw new Error(errCheckProviderNonCheckSpec(pyRepr(spec)));
			}
			if (spec.severity !== spec.implForm) {
				throw new Error(
					errCheckProviderSeverityMismatch(
						spec.name,
						spec.severity,
						checkSpecCtorName(spec.implForm),
						checkSpecCtorName(spec.severity),
					),
				);
			}
			// Routes through the single add-path: a name colliding with a TOML
			// check or another provider's check is the usual hard error.
			addCheckDef(state, {
				name: spec.name,
				tags: spec.tags,
				severity: spec.severity,
				fast: spec.fast,
				pure: spec.pure,
				needsNetwork: spec.needsNetwork,
				dependsOn: spec.dependsOn,
				scope: spec.scope,
				// Provider-sourced checks have no TOML declaration, so no
				// description or subject.
				description: "",
				subject: "",
				impl: spec.impl,
				implForm: spec.implForm,
			});
			state.providerSourcedNames.add(spec.name);
		}
	}
	state.providerMaterializedCwd = cwd;
}

/**
 * Drops every provider-sourced definition and clears the materialization
 * memo so the next registry read re-runs all providers. Backs
 * app.resetCheckProviderCache; does NOT unregister the providers themselves.
 */
export function resetCheckProviderCache(state: ChecksState): void {
	dropProviderSourcedDefs(state);
	state.providerMaterializedCwd = undefined;
}

/** Removes provider-sourced definitions, leaving TOML defs untouched. */
function dropProviderSourcedDefs(state: ChecksState): void {
	for (const name of state.providerSourcedNames) {
		state.defs.delete(name);
	}
	state.providerSourcedNames.clear();
}
