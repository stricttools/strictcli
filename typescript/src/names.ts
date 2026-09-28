/**
 * The naming rule and the framework-command reservation: one pattern for every
 * identifier a caller types or references, a one-letter short form, and the
 * names of the framework's own commands, reserved at every level of the command
 * tree. Mirrors go/strictcli/check.go (isKebabName) and strictcli.go
 * (checkCommandTreeName, isShortForm).
 */

import {
	errCommandNameInvalid,
	errDeprecatedNameInvalid,
	errFrameworkCommandName,
	errGroupNameInvalid,
	RegistrationError,
} from "./errors.js";

const KEBAB_NAME_RE = /^[a-z][a-z0-9]*(-[a-z0-9]+)*$/;

/** Lowercase kebab-case of at least two characters. */
export function isKebabName(name: unknown): boolean {
	return (
		typeof name === "string" && name.length >= 2 && KEBAB_NAME_RE.test(name)
	);
}

/** One ASCII letter, lowercase or uppercase. */
export function isShortForm(short: unknown): boolean {
	return typeof short === "string" && /^[A-Za-z]$/.test(short);
}

/** The framework's own commands, whose names no app command may take. */
export const FRAMEWORK_COMMAND_NAMES: ReadonlySet<string> = new Set([
	"help",
	"version",
]);

/**
 * The reservation and the naming rule for a command, group or deprecated
 * command name. `kind` is "command", "group" or "deprecated command".
 */
export function validateCommandTreeName(
	kind: "command" | "group" | "deprecated command",
	name: string,
): void {
	if (FRAMEWORK_COMMAND_NAMES.has(name)) {
		throw new RegistrationError(errFrameworkCommandName(kind, name));
	}
	if (!isKebabName(name)) {
		if (kind === "group") {
			throw new RegistrationError(errGroupNameInvalid(name));
		}
		if (kind === "deprecated command") {
			throw new RegistrationError(errDeprecatedNameInvalid(name));
		}
		throw new RegistrationError(errCommandNameInvalid(name));
	}
}
