/**
 * The auto-registered `check` command plus the human-readable and JSON result
 * formatters, dispatching the list, help, no-match and run modes -- where a
 * run under --dry-run is the same run restricted to the purity partition,
 * followed by the would-run plan for what it did not execute.
 *
 * Parity sources: go/strictcli/check_cmd.go and check_public.go (formatters)
 * with Python _register_check_command / _check_list_mode /
 * _check_dry_run_mode / format_check_results as the divergence ground truth
 * for the branch order (list -> help-when-unfiltered -> no-match -> run) and
 * the inline output strings.
 *
 * Errors raised below the handler (tag DSL, cycles, provider
 * materialization) propagate out of run()/test(), mirroring Python where
 * they surface as ValueError to the caller.
 */

import {
	type AppImpl,
	defineFrameworkCommand,
	markFrameworkHandler,
} from "../app.js";
import { type Context, contextIsHermetic } from "../context.js";
import { type AnyFlag, flag } from "../factories.js";
import { formatCommandHelp } from "../help.js";
import { t } from "../types.js";
import { effectsBypassProvider } from "./effects_bypass.js";
import type {
	CheckContext,
	CheckDef,
	CheckRunResult,
	CheckStatus,
	ChecksState,
} from "./framework.js";
import { orderedProblems, sortedCheckNames } from "./framework.js";
import { materializeCheckProviders } from "./provider.js";
import {
	checkIsPure,
	filterChecks,
	resolveCheckOrder,
	runOrderedChecks,
} from "./runner.js";

/**
 * Turns on the check system exactly once: flips enabled and registers the
 * auto-generated `check` command a single time. Idempotent -- calling it
 * again is a no-op, which prevents double-registration. The command is
 * absent (hidden) entirely when checks are never enabled.
 */
export function enableChecks(app: AppImpl): void {
	if (app.checks.enabled) {
		return;
	}
	app.checks.enabled = true;
	registerCheckCommand(app);
	// The built-in effects-bypass lint rides the same provider hook the
	// built-in cli-test-coverage check uses. Pushed directly (not through
	// app.registerCheckProvider, which routes back here).
	app.checks.providers.push(effectsBypassProvider(app));
	app.checks.providerMaterializedCwd = undefined;
}

/**
 * The check command's machine payload contract (contract §19.5). Both of the
 * command's machine shapes -- the listing (--list) and the run results -- are
 * arrays of objects. Framework-owned literal, byte-identical across the three
 * implementations.
 */
const CHECK_PAYLOAD_SCHEMA: Readonly<Record<string, unknown>> = {
	type: "array",
	items: { type: "object" },
};

/** Registers the auto-generated `check` command (called from enableChecks). */
function registerCheckCommand(app: AppImpl): void {
	const candidates: AnyFlag[] = [
		flag("all", t.bool, {
			help: "Run every registered check regardless of tag or name filters",
			presence: "default",
			default: false,
		}),
		flag("tag", t.str, {
			help: "Tag DSL expression to select checks (e.g. 'changelog & !quality')",
			presence: "default",
			default: "",
		}),
		flag("name", t.str, {
			help: "Glob pattern to filter checks by name (e.g. 'hash-*', '*coverage*')",
			presence: "default",
			default: "",
		}),
		flag("list", t.bool, {
			help: "List all registered checks with their tags and exit without running",
			presence: "default",
			default: false,
		}),
		flag("ignore-warnings", t.bool, {
			help: "Treat warn-severity results as passing so they do not cause nonzero exit",
			presence: "default",
			default: false,
		}),
	];
	// `verbose`, `dry-run` and `json` are NOT in the candidate list: all three
	// names are reserved by the framework (two flags cannot share a spelling),
	// so the handler reads ctx.verbose, ctx.dryRun and ctx.json instead. The
	// machine output is this command's payload (contract §19.4, §7.5's
	// 2026-08-13 sweep box), not a locally-flagged print. `check --dry-run` runs
	// the checks declared pure and lists the impure remainder, and the whole run
	// is in dry mode, so the framework emits the would-do header after the
	// handler's own output.
	// Candidates colliding with global flags are dropped -- the handler
	// receives the global flag's value for that key instead (Python parity).
	const flags: Record<string, AnyFlag> = {};
	for (const f of candidates) {
		if (!app.globalFlagNames.has(f.name)) {
			flags[f.name.replaceAll("-", "_")] = f;
		}
	}

	// The handler identity is registered in the framework-handler WeakSet at
	// the moment it is created: the marker on the carrier is only honored for
	// handlers strictcli itself minted.
	const handler = markFrameworkHandler((args: unknown, ctx: Context) =>
		checkHandler(app, args as Record<string, unknown>, ctx),
	);
	// `check` classifies as read_only: its coverage-shard writes are
	// CACHE_WRITEs, which never trip read-only enforcement.
	app.command(
		defineFrameworkCommand("check", "read_only", {
			help: "Run project checks registered via the check framework and report results",
			flags,
			payloadSchema: CHECK_PAYLOAD_SCHEMA,
			handler: handler as never,
		}),
	);
}

/**
 * Augments a tool-supplied check context with connection-env access
 * (ConnectionEnvReader), backed by the framework Context which carries the
 * app's declared connection envs and the invocation's hermetic state. When no
 * connection envs are declared, the base context is returned unchanged so the
 * common case is unaffected. Mirrors Go wrapCheckContext / Python
 * _wrap_check_context.
 */
function wrapCheckContext(
	app: AppImpl,
	base: CheckContext,
	ctx: Context,
): CheckContext {
	if (app.connectionEnvs.size === 0) {
		return base;
	}
	const reader = (
		envVar: string,
	): [value: string | undefined, present: boolean] =>
		ctx.connectionEnvValue(envVar);
	// Capture the invocation's hermetic state so a check can tell "--hermetic
	// suppressed the connection env" from "env var simply unset". Mirrors Go's
	// wrapper reading frameworkCtx.infra and Python's _last_hermetic.
	const hermetic = contextIsHermetic(ctx);
	return new Proxy(base as CheckContext & object, {
		get(target, prop, receiver): unknown {
			if (prop === "connectionEnvValue") {
				return reader;
			}
			if (prop === "isHermetic") {
				return (): boolean => hermetic;
			}
			return Reflect.get(target, prop, receiver);
		},
	});
}

async function checkHandler(
	app: AppImpl,
	kwargs: Record<string, unknown>,
	ctx: Context,
): Promise<number> {
	// Materialize provider-sourced checks before any registry read (covers
	// the list and execution branches below).
	materializeCheckProviders(app.checks);

	const runAll = kwargs.all === true;
	const listMode = kwargs.list === true;
	const ignoreWarnings = kwargs.ignore_warnings === true;
	// Framework-delivered, not command flags (all three names are reserved).
	const verbose = ctx.verbose;
	const dryRun = ctx.dryRun;
	// Treat empty strings as "not provided".
	const tagRaw = typeof kwargs.tag === "string" ? kwargs.tag : "";
	const nameRaw = typeof kwargs.name === "string" ? kwargs.name : "";
	const tagExpr = tagRaw !== "" ? tagRaw : undefined;
	const nameGlob = nameRaw !== "" ? nameRaw : undefined;

	if (listMode) {
		checkListMode(app.checks, ctx);
		return 0;
	}

	const hasFilter = runAll || tagExpr !== undefined || nameGlob !== undefined;
	if (!hasFilter) {
		// No flags: show help for the check command.
		const cmd = app.commands.get("check");
		if (cmd !== undefined) {
			ctx.info(formatCommandHelp(app, cmd, ""));
		}
		return 0;
	}

	// A tag expression that does not parse is this command's own error,
	// reported through the error writer (§19.14's box) rather than unwinding
	// the handler. Parsing the expression is the only thing here that throws.
	let selected: Set<string>;
	try {
		selected = filterChecks(app.checks.defs, tagExpr, nameGlob, runAll);
	} catch (e) {
		ctx.error((e as Error).message);
		return 1;
	}
	if (selected.size === 0) {
		ctx.info("No checks matched the given filters.");
		return 0;
	}
	const order = resolveCheckOrder(app.checks.defs, selected);

	// Both a full run and a dry run execute checks, so both need a context.
	// --dry-run is not a separate branch: it selects the purity partition, so
	// the checks declared pure really run and only the impure remainder is
	// rendered as the would-run plan.
	if (app.checks.contextFactory === undefined) {
		ctx.error(
			"no check context configured. " +
				"Call app.setCheckContext(factory) before running.",
		);
		return 1;
	}
	const context = wrapCheckContext(app, app.checks.contextFactory(), ctx);
	const { results, impureListed, exitCode } = await runOrderedChecks(
		app.checks.defs,
		order,
		context,
		ignoreWarnings,
		dryRun,
	);

	// The payload is supplied unconditionally and is NOT routed through
	// ctx.info: the call is mode-independent (contract §19.4) and machine
	// output is structurally exempt from --quiet (§19.2). The human rendering
	// is unconditional too, and rides the envelope's diagnostics in machine
	// mode (§19.1).
	ctx.payload(checkResultItems(results));
	const output = formatCheckResults(results, verbose);
	if (output !== "") {
		ctx.info(output);
	}
	if (dryRun) {
		checkDryRunPlan(app.checks.defs, impureListed, order, ctx);
	}
	return exitCode;
}

/**
 * The --list mode. The payload is supplied unconditionally (contract §19.4)
 * and the human table goes through the context writer, so machine mode
 * carries it as one diagnostic instead of a second stdout document (§19.1).
 */
function checkListMode(state: ChecksState, ctx: Context): void {
	const names = sortedCheckNames(state);
	const sortedDefs = names.map((n) => state.defs.get(n) as CheckDef);

	const items = sortedDefs.map((def) => ({
		name: def.name,
		tags: def.tags,
		severity: def.severity,
		// Scope is emitted only when non-empty (omitempty parity).
		...(def.scope !== "" ? { scope: def.scope } : {}),
	}));
	ctx.payload(items);

	if (sortedDefs.length === 0) {
		ctx.info("No checks defined.");
		return;
	}

	let nameWidth = "NAME".length;
	let tagsWidth = "TAGS".length;
	for (const def of sortedDefs) {
		nameWidth = Math.max(nameWidth, def.name.length);
		tagsWidth = Math.max(tagsWidth, def.tags.join(", ").length);
	}
	const lines = [
		`${"NAME".padEnd(nameWidth)}   ${"TAGS".padEnd(tagsWidth)}   SEVERITY`,
	];
	for (const def of sortedDefs) {
		const tagsStr = def.tags.join(", ");
		lines.push(
			`${def.name.padEnd(nameWidth)}   ${tagsStr.padEnd(tagsWidth)}   ${def.severity}`,
		);
	}
	ctx.info(lines.join("\n"));
}

/**
 * Prints the would-run plan for the checks a dry run did NOT execute -- the
 * purity partition's remainder (the impure checks and any check whose
 * dependency was listed). `order` is the full selected order, used only to
 * decide which dependencies are worth naming. The header is printed even when
 * nothing was left over: an empty plan is a statement ("everything selected
 * ran"), the same way the framework's own would-do log prints its header with
 * an empty body.
 */
function checkDryRunPlan(
	defs: ReadonlyMap<string, CheckDef>,
	listed: readonly string[],
	order: readonly string[],
	ctx: Context,
): void {
	const noun = listed.length === 1 ? "check" : "checks";
	const lines = [`Would run ${listed.length} ${noun}:`];
	const inOrder = new Set(order);
	listed.forEach((name, i) => {
		const def = defs.get(name) as CheckDef;
		const purity = checkIsPure(def) ? "pure" : "impure";
		const deps = def.dependsOn.filter((d) => inOrder.has(d));
		if (deps.length > 0) {
			lines.push(
				`  ${i + 1}. ${name} (depends on: ${deps.join(", ")}) [${purity}]`,
			);
		} else {
			lines.push(`  ${i + 1}. ${name} [${purity}]`);
		}
	});
	ctx.info(lines.join("\n"));
}

const STATUS_LABELS: Readonly<Record<CheckStatus, string>> = {
	pass: "PASS",
	fail: "FAIL",
	warn: "WARN",
	skip: "SKIP",
};

/**
 * Formats check results as a human-readable aligned string. Shows the
 * derived status label, name, and message, with minted problems listed under
 * the check row grouped by severity (error problems first, then warns), each
 * tagged with its severity. Problems appear for fail/warn/skip outcomes or
 * when verbose. Notes are verdict-inert and surface ONLY under verbose, on
 * every outcome including a pass; verbose also appends per-check durations
 * ("(<n>ms)") and a trailing count summary. No trailing newline.
 */
export function formatCheckResults(
	results: readonly CheckRunResult[],
	verbose = false,
): string {
	if (results.length === 0) {
		return "";
	}

	let nameWidth = 0;
	for (const r of results) {
		nameWidth = Math.max(nameWidth, r.name.length);
	}

	const lines: string[] = [];
	const counts: Record<CheckStatus, number> = {
		pass: 0,
		fail: 0,
		warn: 0,
		skip: 0,
	};

	for (const r of results) {
		const status = r.status;
		counts[status]++;
		let row = `${STATUS_LABELS[status]}  ${r.name.padEnd(nameWidth)}    ${r.message}`;
		if (verbose) {
			row += ` (${r.durationMs}ms)`;
		}
		lines.push(row);

		const showProblems =
			verbose || status === "fail" || status === "warn" || status === "skip";
		if (showProblems) {
			for (const p of orderedProblems(r.outcome)) {
				lines.push(`        [${p.severity}] ${p.text}`);
			}
		}
		if (verbose) {
			for (const n of r.notes) {
				lines.push(`        [note] ${n}`);
			}
		}
	}

	if (verbose) {
		lines.push("");
		lines.push(
			`${counts.pass} passed / ${counts.fail} failed / ` +
				`${counts.warn} warned / ${counts.skip} skipped`,
		);
	}

	return lines.join("\n");
}

/**
 * Formats check results as a compact JSON array string. Each entry carries
 * the derived status plus the minted problems (each with its severity and
 * text); problems and notes serialize as [] when empty, and duration_ms is
 * always present. No trailing newline.
 */
export function formatCheckResultsJSON(
	results: readonly CheckRunResult[],
): string {
	return JSON.stringify(checkResultItems(results));
}

/**
 * Check results as machine data (the check command's run payload, contract
 * §19.4): the same records formatCheckResultsJSON serializes, handed to the
 * framework as data instead of as a string.
 */
function checkResultItems(
	results: readonly CheckRunResult[],
): readonly Record<string, unknown>[] {
	return results.map((r) => ({
		name: r.name,
		status: r.status,
		message: r.message,
		problems: r.problems.map((p) => ({ severity: p.severity, text: p.text })),
		notes: [...r.notes],
		duration_ms: r.durationMs,
	}));
}
