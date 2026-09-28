/**
 * Declared runtime requirements: what a command needs at run time -- a system
 * library, an executable, a device -- declared ONCE as a requirement value and
 * referenced by every command that needs it (`requires: [...]`). Before the
 * handler runs, on every door (`run()`, `test()`, `call()` and the MCP server)
 * and in dry mode too, the framework loads each of the command's requirements;
 * a missing one ends the command with exit 1 and one error naming what is
 * missing and how to install it. The handler reads a loaded value through
 * `ctx.need(requirement)`. Mirrors go/strictcli/requirements.go.
 */

import {
	errNeedUndeclared,
	errRequirementDeclaredTwice,
	errRequirementHelpInvalid,
	errRequirementInstallInvalid,
	errRequirementLoadMissing,
	errRequirementNameInvalid,
	errRequirementNameReused,
	errRequirementUnavailable,
	RegistrationError,
} from "./errors.js";
import { ExitNow } from "./exits.js";
import { isKebabName } from "./names.js";

/**
 * One runtime requirement: a name (the naming rule), one line saying what it
 * is, one line saying how to install it, and `load`, which returns the loaded
 * value or throws an Error saying why it is not available.
 */
export interface Requirement<T> {
	readonly kind: "requirement";
	readonly name: string;
	readonly help: string;
	readonly install: string;
	readonly load: () => T;
}

/** Any requirement, whatever it loads. */
// biome-ignore lint/suspicious/noExplicitAny: the loaded type is erased where requirements are stored together
export type AnyRequirement = Requirement<any>;

function isOneLine(value: unknown): boolean {
	return (
		typeof value === "string" &&
		value.trim() !== "" &&
		!value.includes("\n") &&
		!value.includes("\r")
	);
}

/** Declares a runtime requirement. */
export function requirement<T>(spec: {
	readonly name: string;
	readonly help: string;
	readonly install: string;
	readonly load: () => T;
}): Requirement<T> {
	if (!isKebabName(spec.name)) {
		throw new RegistrationError(errRequirementNameInvalid(spec.name));
	}
	if (!isOneLine(spec.help)) {
		throw new RegistrationError(errRequirementHelpInvalid(spec.name));
	}
	if (!isOneLine(spec.install)) {
		throw new RegistrationError(errRequirementInstallInvalid(spec.name));
	}
	if (typeof spec.load !== "function") {
		throw new RegistrationError(errRequirementLoadMissing(spec.name));
	}
	return {
		kind: "requirement",
		name: spec.name,
		help: spec.help,
		install: spec.install,
		load: spec.load,
	};
}

/** A command's requirements, refusing one referenced twice. */
export function validateRequires(
	cmdName: string,
	requires: readonly AnyRequirement[] | undefined,
): readonly AnyRequirement[] {
	const seen = new Set<string>();
	for (const r of requires ?? []) {
		if (seen.has(r.name)) {
			throw new RegistrationError(errRequirementDeclaredTwice(cmdName, r.name));
		}
		seen.add(r.name);
	}
	return [...(requires ?? [])];
}

/**
 * Refuses a second requirement value under a name the app already knows: a
 * requirement is declared once and referenced everywhere.
 */
export function registerRequirements(
	known: Map<string, AnyRequirement>,
	requires: readonly AnyRequirement[],
): void {
	for (const r of requires) {
		const prior = known.get(r.name);
		if (prior !== undefined && prior !== r) {
			throw new RegistrationError(errRequirementNameReused(r.name));
		}
		known.set(r.name, r);
	}
}

/**
 * The requirements each dispatch's context declared and the values they
 * loaded, keyed by the context, so `ctx.need` reads them without widening the
 * Context's own surface.
 */
const loadedByContext = new WeakMap<
	object,
	{
		readonly declared: readonly AnyRequirement[];
		readonly loaded: ReadonlyMap<string, unknown>;
	}
>();

/**
 * The value a requirement the command declared loaded before the handler ran;
 * asking for one the command did not declare is a hard error.
 */
export function neededValue<T>(
	ctx: object,
	requirement: Requirement<T>,
	commandName: string,
): T {
	const entry = loadedByContext.get(ctx);
	if (entry === undefined || !entry.declared.includes(requirement)) {
		throw new Error(errNeedUndeclared(commandName, requirement.name));
	}
	return entry.loaded.get(requirement.name) as T;
}

/**
 * Loads a command's requirements in declaration order before its handler runs,
 * ending the command early at the first one that is not available.
 */
export function loadRequirements(
	ctx: object,
	requires: readonly AnyRequirement[] | undefined,
	cmdPath: string,
): void {
	const declared = requires ?? [];
	const loaded = new Map<string, unknown>();
	for (const r of declared) {
		try {
			loaded.set(r.name, r.load());
		} catch (e) {
			throw new ExitNow(
				1,
				errRequirementUnavailable(
					cmdPath,
					r.name,
					r.help,
					e instanceof Error ? e.message : String(e),
					r.install,
				),
			);
		}
	}
	loadedByContext.set(ctx, { declared, loaded });
}

/** The help document's `requires` entry of a command. */
export function serializeRequires(
	requires: readonly AnyRequirement[],
): Record<string, unknown>[] {
	return requires.map((r) => ({
		name: r.name,
		help: r.help,
		install: r.install,
	}));
}

/** The `Requirements:` section of command help, or nothing. */
export function formatRequirementsSection(
	requires: readonly AnyRequirement[] | undefined,
): string[] {
	if (requires === undefined || requires.length === 0) {
		return [];
	}
	const width = Math.max(...requires.map((r) => r.name.length));
	return [
		"",
		"Requirements:",
		...requires.map(
			(r) =>
				`  ${r.name}${" ".repeat(width - r.name.length + 4)}${r.help}; install it: ${r.install}`,
		),
	];
}
