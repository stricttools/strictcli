/**
 * Check runner: filtering (tag DSL + name glob), DAG-ordered execution with
 * dependency pull-in, cycle detection, and dependency-failure cascade-skips.
 * It also handles the purity partition and integer-millisecond wall-clock
 * timing.
 *
 * Parity sources: go/strictcli/check_runner.go with Python _filter_checks /
 * _resolve_check_order / _find_cycle / _run_checks (~6841-7085) as the
 * divergence ground truth (FIFO Kahn queue, DFS cycle path, fnmatch globs).
 */

import {
	errCheckDependencyCycle,
	errCheckOutcomeNotMinted,
	errCheckValueAboveSeverity,
	errCheckValueInvalid,
	errCheckValueSourceEmpty,
} from "../errors.js";
import {
	type CheckContext,
	type CheckDef,
	CheckOutcome,
	CheckRunResult,
	type CheckValue,
	mintAsWarnings,
	mintCheckAbort,
	mintOff,
	mintSkip,
} from "./framework.js";
import { matchTagExpr } from "./tagdsl.js";

/**
 * Translates an fnmatch-style glob (*, ?, [seq], [!seq]) into an anchored
 * RegExp, mirroring Python fnmatch.translate: an unterminated "[" is a
 * literal, "]" directly after "[" or "[!" is a literal member, and there are
 * no error cases (Python globs never fail to compile).
 */
export function globToRegExp(pattern: string): RegExp {
	let re = "";
	let i = 0;
	while (i < pattern.length) {
		const ch = pattern[i] as string;
		i++;
		if (ch === "*") {
			re += "[\\s\\S]*";
		} else if (ch === "?") {
			re += "[\\s\\S]";
		} else if (ch === "[") {
			let j = i;
			if (j < pattern.length && pattern[j] === "!") {
				j++;
			}
			if (j < pattern.length && pattern[j] === "]") {
				j++;
			}
			while (j < pattern.length && pattern[j] !== "]") {
				j++;
			}
			if (j >= pattern.length) {
				re += "\\[";
			} else {
				let inner = pattern.slice(i, j).replaceAll("\\", "\\\\");
				if (inner.startsWith("!")) {
					inner = `^${inner.slice(1)}`;
				} else if (inner.startsWith("^")) {
					inner = `\\${inner}`;
				}
				re += `[${inner}]`;
				i = j + 1;
			}
		} else {
			re += ch.replace(/[.*+?^${}()|[\]\\]/, "\\$&");
		}
	}
	return new RegExp(`^(?:${re})$`);
}

/**
 * Selects checks based on tag expression, name glob, or runAll. When both
 * tagExpr and nameGlob are provided, the result is their intersection.
 * Neither filter selects nothing.
 */
export function filterChecks(
	defs: ReadonlyMap<string, CheckDef>,
	tagExpr: string | undefined,
	nameGlob: string | undefined,
	runAll: boolean,
): Set<string> {
	if (runAll) {
		return new Set(defs.keys());
	}

	let byTag: Set<string> | undefined;
	if (tagExpr !== undefined) {
		byTag = new Set();
		for (const [name, def] of defs) {
			if (matchTagExpr(tagExpr, new Set(def.tags))) {
				byTag.add(name);
			}
		}
	}

	let byName: Set<string> | undefined;
	if (nameGlob !== undefined) {
		const re = globToRegExp(nameGlob);
		byName = new Set();
		for (const name of defs.keys()) {
			if (re.test(name)) {
				byName.add(name);
			}
		}
	}

	if (byTag !== undefined && byName !== undefined) {
		const both = byName;
		return new Set([...byTag].filter((name) => both.has(name)));
	}
	return byTag ?? byName ?? new Set();
}

/**
 * Resolves execution order via topological sort (Kahn's algorithm with a
 * sorted seed and FIFO queue -- Python parity), pulling unselected
 * dependencies into the execution set. Throws on cycles with a formatted
 * cycle path.
 */
export function resolveCheckOrder(
	defs: ReadonlyMap<string, CheckDef>,
	selected: ReadonlySet<string>,
): string[] {
	// Expand selected to include all transitive dependencies.
	const expanded = new Set<string>();
	const stack = [...selected];
	while (stack.length > 0) {
		const name = stack.pop() as string;
		if (expanded.has(name)) {
			continue;
		}
		expanded.add(name);
		const def = defs.get(name) as CheckDef;
		for (const dep of def.dependsOn) {
			if (!expanded.has(dep)) {
				stack.push(dep);
			}
		}
	}

	// Build in-degrees and dependents within the expanded set.
	const inDegree = new Map<string, number>();
	const dependents = new Map<string, string[]>();
	for (const name of expanded) {
		inDegree.set(name, 0);
		dependents.set(name, []);
	}
	for (const name of expanded) {
		const def = defs.get(name) as CheckDef;
		for (const dep of def.dependsOn) {
			if (expanded.has(dep)) {
				(dependents.get(dep) as string[]).push(name);
				inDegree.set(name, (inDegree.get(name) as number) + 1);
			}
		}
	}

	// Kahn's algorithm: sorted initial queue, FIFO thereafter, sorted
	// dependents appended as they become ready (Python deque parity).
	const queue = [...expanded].sort().filter((n) => inDegree.get(n) === 0);
	const order: string[] = [];
	while (queue.length > 0) {
		const node = queue.shift() as string;
		order.push(node);
		for (const child of [...(dependents.get(node) as string[])].sort()) {
			const deg = (inDegree.get(child) as number) - 1;
			inDegree.set(child, deg);
			if (deg === 0) {
				queue.push(child);
			}
		}
	}

	if (order.length !== expanded.size) {
		const remaining = new Set([...expanded].filter((n) => !order.includes(n)));
		throw new Error(errCheckDependencyCycle(findCycle(defs, remaining)));
	}
	return order;
}

/** Finds and formats a cycle path among the given nodes (Python _find_cycle). */
function findCycle(
	defs: ReadonlyMap<string, CheckDef>,
	nodes: ReadonlySet<string>,
): string {
	const visited = new Set<string>();
	const path: string[] = [];
	const pathSet = new Set<string>();

	const dfs = (node: string): string | undefined => {
		visited.add(node);
		path.push(node);
		pathSet.add(node);
		const def = defs.get(node) as CheckDef;
		for (const dep of def.dependsOn) {
			if (!nodes.has(dep)) {
				continue;
			}
			if (pathSet.has(dep)) {
				const cycleStart = path.indexOf(dep);
				return [...path.slice(cycleStart), dep].join(" -> ");
			}
			if (!visited.has(dep)) {
				const result = dfs(dep);
				if (result !== undefined) {
					return result;
				}
			}
		}
		path.pop();
		pathSet.delete(node);
		return undefined;
	};

	for (const node of [...nodes].sort()) {
		if (!visited.has(node)) {
			const result = dfs(node);
			if (result !== undefined) {
				return result;
			}
		}
	}
	return [...nodes].sort().join(" -> ");
}

/**
 * Whether a check is executable under the purity partition: declared pure
 * AND not requiring network access. Everything else is "impure".
 */
export function checkIsPure(def: CheckDef): boolean {
	return def.pure && !def.needsNetwork;
}

/** One check's effective value and where it came from. */
export interface ResolvedCheckValue {
	readonly value: "error" | "warn" | "off";
	readonly source: string;
}

/**
 * The source shown for a check the resolver assigns no value: it runs at its
 * registered severity.
 */
const CHECK_VALUE_DEFAULT_SOURCE = "default";

/**
 * Resolves each named check's effective value and source. A check the
 * resolver assigns no value (undefined), and every check when no resolver is
 * set, runs at its registered severity with the source "default". Throws
 * naming the first check whose resolved value is not a check value, carries no
 * source, or would raise the check above its registered severity.
 */
export function resolveCheckValues(
	defs: ReadonlyMap<string, CheckDef>,
	names: Iterable<string>,
	resolver: ((name: string) => CheckValue | undefined) | undefined,
): Map<string, ResolvedCheckValue> {
	const values = new Map<string, ResolvedCheckValue>();
	for (const name of names) {
		const severity = (defs.get(name) as CheckDef).severity;
		const resolved = resolver === undefined ? undefined : resolver(name);
		if (resolved === undefined) {
			values.set(name, { value: severity, source: CHECK_VALUE_DEFAULT_SOURCE });
			continue;
		}
		const value = resolved.value as string;
		if (value !== "error" && value !== "warn" && value !== "off") {
			throw new Error(errCheckValueInvalid(name, String(value)));
		}
		if (typeof resolved.source !== "string" || resolved.source.trim() === "") {
			throw new Error(errCheckValueSourceEmpty(name, value));
		}
		if (value === "error" && severity === "warn") {
			throw new Error(
				errCheckValueAboveSeverity(name, value, resolved.source, severity),
			);
		}
		values.set(name, { value, source: resolved.source });
	}
	return values;
}

export interface RunChecksOutput {
	readonly results: CheckRunResult[];
	/**
	 * Ordered names of checks NOT executed because of the purity partition
	 * (empty unless pureOnly). Listed checks contribute nothing to the exit
	 * code -- a consumer renders them as e.g. "would run: <name> (impure)".
	 */
	readonly impureListed: string[];
	/** 0 if every executed check passes or skips, else 1 (a warning counts). */
	readonly exitCode: number;
}

/**
 * Executes checks in order, cascade-skipping dependents of gated (FAIL)
 * checks. Cascade keys ONLY on a derived FAIL (an error-severity problem
 * present) or a prior cascade-skip: a WARN outcome satisfies the dependency
 * (dependents still run) and only affects the exit code -- warn-severity
 * checks physically cannot cascade because WarnReporter lacks error-minting.
 * An explicit SKIP from an impl is NOT a failure -- dependents still run.
 *
 * `values` maps a check name to its resolved value (see resolveCheckValues);
 * a check absent from it runs as registered. A check resolved "off" does not
 * run: it gets an OFF row, cascades nothing, and leaves the exit code alone.
 * A check resolved "warn" runs and has its failures reported as warnings, so
 * it never blocks its dependents.
 *
 * Purity partition (pureOnly): only pure, non-network checks execute; every
 * other selected check is listed (not run, no exit-code contribution). A
 * check also joins the listing when any dependency was listed -- an
 * unexecuted dependency means its precondition cannot be verified. The
 * failed-dependency cascade takes precedence over the listing.
 */
export async function runOrderedChecks(
	defs: ReadonlyMap<string, CheckDef>,
	order: readonly string[],
	ctx: CheckContext,
	values: ReadonlyMap<string, ResolvedCheckValue>,
	pureOnly: boolean,
): Promise<RunChecksOutput> {
	const results: CheckRunResult[] = [];
	const failedChecks = new Set<string>();
	const listedChecks = new Set<string>();
	const impureListed: string[] = [];
	let exitCode = 0;

	for (const name of order) {
		const def = defs.get(name) as CheckDef;
		const resolved = values.get(name);
		const value = resolved?.value ?? def.severity;

		// A check its resolved value turned off never runs, whatever its
		// dependencies did: it is shown as off, with where that came from.
		if (resolved !== undefined && resolved.value === "off") {
			results.push(new CheckRunResult(name, mintOff(resolved.source), 0));
			continue;
		}

		// Cascade: skip when any dependency failed.
		const failedDep = def.dependsOn.find((dep) => failedChecks.has(dep));
		if (failedDep !== undefined) {
			const outcome = mintSkip(`skipped: dependency "${failedDep}" failed`);
			results.push(new CheckRunResult(name, outcome, 0));
			failedChecks.add(name);
			exitCode = 1;
			continue;
		}

		if (pureOnly) {
			const listed =
				!checkIsPure(def) || def.dependsOn.some((dep) => listedChecks.has(dep));
			if (listed) {
				listedChecks.add(name);
				impureListed.push(name);
				continue;
			}
		}

		// Capture wall-clock duration around the impl call only.
		const start = performance.now();
		let outcome: CheckOutcome;
		try {
			outcome = await (def.impl as NonNullable<CheckDef["impl"]>)(ctx);
		} catch (err) {
			// A throwing impl is contained here and reported as that check's own
			// failure: one broken check must not abort the whole run, and every
			// other selected check still executes.
			const durationMs = Math.trunc(performance.now() - start);
			const aborted = mintCheckAbort(name, err);
			results.push(new CheckRunResult(name, aborted, durationMs));
			failedChecks.add(name);
			exitCode = 1;
			continue;
		}
		const durationMs = Math.trunc(performance.now() - start);
		// Belt-and-braces: an impl must return a reporter-minted outcome.
		if (!(outcome instanceof CheckOutcome)) {
			throw new Error(errCheckOutcomeNotMinted(name));
		}
		// A check resolved to warn reports its failures as warnings. (A broken
		// check, contained above, fails whatever its value: resolving it to
		// warn lowers what its findings mean, not what an abort means.)
		if (value === "warn") {
			outcome = mintAsWarnings(outcome);
		}
		const result = new CheckRunResult(name, outcome, durationMs);
		results.push(result);

		if (result.gated()) {
			failedChecks.add(name);
			exitCode = 1;
		} else if (result.warned()) {
			// Warn satisfies the dependency (no cascade), but still makes the
			// run exit non-zero.
			exitCode = 1;
		}
		// pass / skip: not a failure, no cascade, no exit code change.
	}

	return { results, impureListed, exitCode };
}
