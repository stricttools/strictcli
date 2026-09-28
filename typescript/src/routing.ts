/**
 * Command routing: first non-flag token selects a command or group, and
 * groups are traversed to arbitrary depth, mirroring Go resolveCommand in
 * routing.go. Python _resolve_command is the divergence ground truth: after a group
 * token is consumed, a leading --help/-h in the remaining tokens requests
 * group help regardless of what follows (Python checks segments[0]; Go
 * additionally requires it to be the only remaining token).
 */

import type { AppImpl, GroupImpl, RegisteredCommand } from "./app.js";
import {
	errCommandDeprecated,
	errHelpInGroup,
	errNoCommandSpecified,
	errUnknownCommand,
	errUnknownCommandInGroup,
	errVersionInGroup,
} from "./errors.js";

export interface RouteResult {
	/** Resolved command; undefined if routing stopped at a group or errored. */
	readonly cmd?: RegisteredCommand;
	/** Deepest group reached (set even when cmd is found under a group). */
	readonly lastGroup?: GroupImpl;
	/** Group names traversed to reach the result. */
	readonly path: readonly string[];
	/** Remaining tokens after routing consumed path + command name. */
	readonly rest: readonly string[];
	/** Non-empty if routing failed (deprecated, unknown, no command). */
	readonly err?: string;
	/** Full "app group..." prefix for error messages that need path context. */
	readonly commandPrefix?: string;
	/** True when the user requested help at a group level. */
	readonly helpAtGroup: boolean;
}

export function resolveCommand(
	app: AppImpl,
	segments: readonly string[],
): RouteResult {
	let currentGroups: ReadonlyMap<string, GroupImpl> = app.groups;
	let currentCommands: ReadonlyMap<string, RegisteredCommand> = app.commands;
	let currentDeprecated: ReadonlyMap<string, string> = app.deprecated;
	const path: string[] = [];
	let lastGroup: GroupImpl | undefined;
	let rest = segments;

	while (rest.length > 0) {
		const token = rest[0] as string;

		const grp = currentGroups.get(token);
		if (grp !== undefined) {
			path.push(token);
			lastGroup = grp;
			rest = rest.slice(1);

			if (rest.length === 0 || rest[0] === "--help" || rest[0] === "-h") {
				return { lastGroup, path, rest, helpAtGroup: true };
			}

			currentGroups = grp.groups;
			currentCommands = grp.commands;
			currentDeprecated = grp.deprecated;
			continue;
		}

		const cmd = currentCommands.get(token);
		if (cmd !== undefined) {
			return {
				cmd,
				...(lastGroup !== undefined ? { lastGroup } : {}),
				path,
				rest: rest.slice(1),
				helpAtGroup: false,
			};
		}

		const depMsg = currentDeprecated.get(token);
		if (depMsg !== undefined) {
			return {
				err: errCommandDeprecated(token, depMsg),
				path,
				rest,
				helpAtGroup: false,
			};
		}

		// help and version are framework commands at the root only; their names
		// are reserved at every level, so inside a group they point at the root.
		if (path.length > 0 && token === "help") {
			return {
				err: errHelpInGroup([app.name, "help", ...path].join(" ")),
				path,
				rest,
				helpAtGroup: false,
			};
		}
		if (path.length > 0 && token === "version") {
			return {
				err: errVersionInGroup(`${app.name} version`),
				path,
				rest,
				helpAtGroup: false,
			};
		}
		if (path.length > 0) {
			return {
				err: errUnknownCommandInGroup(token, path.join(" ")),
				commandPrefix: [app.name, ...path].join(" "),
				path,
				rest,
				helpAtGroup: false,
			};
		}
		return { err: errUnknownCommand(token), path, rest, helpAtGroup: false };
	}

	// Loop ended without finding a command. Callers never pass empty segments
	// and group exhaustion is caught inside the loop, so this is defensive.
	if (lastGroup !== undefined) {
		return { lastGroup, path, rest, helpAtGroup: true };
	}
	return { err: errNoCommandSpecified(), path, rest, helpAtGroup: false };
}
