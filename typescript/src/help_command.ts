/**
 * The framework's help and version commands: `help` shows the help of the
 * app, a group, a command, or one flag, as text or, under --json, as the help
 * document (the app's schema, version 2); `version` shows the app's name and
 * version. Both are recognized as the first command word; their names are
 * reserved at every level of the command tree. Mirrors go/strictcli
 * help_command.go.
 */

import { readFileSync, realpathSync } from "node:fs";
import { dirname, join } from "node:path";
import type { AppImpl, GroupImpl, RegisteredCommand } from "./app.js";
import {
	errCommandDeprecated,
	errHelpAfterFlag,
	errHelpDepthMissing,
	errHelpDepthOnCommand,
	errHelpDepthValue,
	errHelpFlagOnGroup,
	errHelpFlagWithValue,
	errHelpNegatedFlag,
	errHelpOptionAfterAddress,
	errHelpShortAddress,
	errHelpTextOnly,
	errHelpUnknownFlag,
	errHelpUnknownFlagNoFlags,
	errHelpUnknownOption,
	errHelpWordAfterCommand,
	errProjectIDUndetermined,
	errUnknownCommand,
	errUnknownCommandInGroup,
	errVersionArgs,
	ParseError,
} from "./errors.js";
import {
	type AnyDecl,
	flagOpts,
	memberShort,
	unsetFlagName,
} from "./factories.js";
import {
	formatAppHelp,
	formatCommandHelp,
	formatFlagHelp,
	formatGroupHelp,
	formatVersion,
} from "./help.js";
import { dumpSchema, schemaJson } from "./schema.js";

/** A parsed `help` invocation: help's own options, then the address. */
export interface HelpRequest {
	own: boolean;
	depth: number;
	groupPath: string[];
	group: GroupImpl | undefined;
	cmd: RegisteredCommand | undefined;
	flag: string;
	isGlobal: boolean;
	address: string[];
}

/** What the help or version command produced. */
export type FrameworkCommandResult =
	| { readonly kind: "page"; readonly text: string }
	| {
			readonly kind: "document";
			readonly document: string;
			readonly command: string;
	  };

function helpLine(app: AppImpl, ...words: string[]): string {
	return [app.name, "help", ...words].join(" ");
}

function parseHelpDepth(value: string): number | undefined {
	if (!/^[1-9][0-9]*$/.test(value)) {
		return undefined;
	}
	return Number(value);
}

/** The declarations of a command's flag map, a passthrough having none. */
function commandDecls(cmd: RegisteredCommand): readonly AnyDecl[] {
	return cmd.def.kind === "passthrough" ? [] : cmd.def.allDecls;
}

/**
 * The long names a command's flags put on the command line, depth-first in
 * declaration order: ordinary flags, token-spelled selectors, member flags,
 * and every choice's scoped flags.
 */
function typedFlagNames(decls: readonly AnyDecl[]): string[] {
	const out: string[] = [];
	for (const d of decls) {
		if (d.kind !== "choice-flag") {
			out.push(d.name);
			continue;
		}
		const member = d.electBy === "member-flags";
		if (!member) {
			out.push(d.name);
		}
		for (const [choiceName, c] of Object.entries(d.choices)) {
			if (member) {
				out.push(choiceName);
			}
			out.push(...typedFlagNames(Object.values(c.flags)));
		}
	}
	return out;
}

/** The long name of the flag declaring a short form, or "". */
function shortOwner(
	decls: readonly AnyDecl[],
	globals: readonly AnyDecl[],
	short: string,
): string {
	const walk = (ds: readonly AnyDecl[]): string => {
		for (const d of ds) {
			if (d.kind !== "choice-flag") {
				if (flagOpts(d).short === short && short !== "") {
					return d.name;
				}
				continue;
			}
			const member = d.electBy === "member-flags";
			if (!member && d.opts.short === short && short !== "") {
				return d.name;
			}
			for (const [choiceName, c] of Object.entries(d.choices)) {
				if (member && memberShort(c) === short) {
					return choiceName;
				}
				const found = walk(Object.values(c.flags));
				if (found !== "") {
					return found;
				}
			}
		}
		return "";
	};
	const found = walk(decls);
	return found !== "" ? found : walk(globals);
}

/** Parses the words after `help`; throws ParseError on a refusal. */
export function parseHelpCommand(
	app: AppImpl,
	args: readonly string[],
): HelpRequest {
	const req: HelpRequest = {
		own: false,
		depth: 0,
		groupPath: [],
		group: undefined,
		cmd: undefined,
		flag: "",
		isGlobal: false,
		address: [],
	};
	for (const tok of args) {
		if (tok === "--") {
			break;
		}
		if (tok === "--help" || tok === "-h") {
			return { ...req, own: true };
		}
	}
	let i = 0;
	// Help's own options come first, before the address.
	while (i < args.length && (args[i] as string).startsWith("-")) {
		const tok = args[i] as string;
		if (tok === "--depth") {
			const value = args[i + 1];
			if (value === undefined) {
				throw new ParseError(errHelpDepthMissing());
			}
			const n = parseHelpDepth(value);
			if (n === undefined) {
				throw new ParseError(errHelpDepthValue(value));
			}
			req.depth = n;
			i += 2;
		} else if (tok.startsWith("--depth=")) {
			const value = tok.slice("--depth=".length);
			const n = parseHelpDepth(value);
			if (n === undefined) {
				throw new ParseError(errHelpDepthValue(value));
			}
			req.depth = n;
			i += 1;
		} else if (app.globalFlags.some((g) => tok === `--${g.name}`)) {
			throw new ParseError(
				errHelpFlagOnGroup(tok, helpLine(app, "<command>", tok)),
			);
		} else {
			throw new ParseError(errHelpUnknownOption(tok, app.name));
		}
	}
	let groups: ReadonlyMap<string, GroupImpl> = app.groups;
	let commands: ReadonlyMap<string, RegisteredCommand> = app.commands;
	let deprecated: ReadonlyMap<string, string> = app.deprecated;
	for (; i < args.length; i++) {
		const tok = args[i] as string;
		if (req.cmd !== undefined) {
			if (req.flag !== "") {
				throw new ParseError(errHelpAfterFlag(tok));
			}
			if (!tok.startsWith("-")) {
				throw new ParseError(
					errHelpWordAfterCommand(tok, req.address.join(" ")),
				);
			}
			resolveHelpFlag(app, req, tok);
			continue;
		}
		if (tok.startsWith("-")) {
			if (tok === "--depth" || tok.startsWith("--depth=")) {
				throw new ParseError(
					errHelpOptionAfterAddress(
						"--depth",
						helpLine(app, "--depth", "<int>", ...req.address),
					),
				);
			}
			throw new ParseError(
				errHelpFlagOnGroup(
					tok,
					helpLine(app, ...req.address, "<command>", tok),
				),
			);
		}
		const grp = groups.get(tok);
		if (grp !== undefined) {
			req.groupPath.push(tok);
			req.address.push(tok);
			req.group = grp;
			groups = grp.groups;
			commands = grp.commands;
			deprecated = grp.deprecated;
			continue;
		}
		const cmd = commands.get(tok);
		if (cmd !== undefined) {
			req.cmd = cmd;
			req.group = undefined;
			req.address.push(tok);
			continue;
		}
		const depMsg = deprecated.get(tok);
		if (depMsg !== undefined) {
			throw new ParseError(errCommandDeprecated(tok, depMsg));
		}
		if (req.groupPath.length > 0) {
			throw new ParseError(
				errUnknownCommandInGroup(tok, req.groupPath.join(" ")),
			);
		}
		throw new ParseError(errUnknownCommand(tok));
	}
	if (req.cmd !== undefined && req.depth !== 0) {
		throw new ParseError(errHelpDepthOnCommand(req.address.join(" ")));
	}
	return req;
}

/**
 * Resolves one flag address on the addressed command: its own flags at every
 * scope, then the app's globals. Other spellings of a declared flag (a short
 * form, a value, a negation, a minted clear) are refused naming the long form.
 */
function resolveHelpFlag(app: AppImpl, req: HelpRequest, tok: string): void {
	const cmd = req.cmd as RegisteredCommand;
	const path = req.address.join(" ");
	const fix = (name: string) => helpLine(app, ...req.address, `--${name}`);
	const own = typedFlagNames(commandDecls(cmd));
	const typed = [...own, ...app.globalFlags.map((g) => g.name)];
	const unknown = () =>
		new ParseError(
			typed.length === 0
				? errHelpUnknownFlagNoFlags(path, tok)
				: errHelpUnknownFlag(path, tok, typed.map((n) => `--${n}`).join(", ")),
		);
	if (!tok.startsWith("--")) {
		const long = shortOwner(commandDecls(cmd), app.globalFlags, tok.slice(1));
		if (long !== "") {
			throw new ParseError(errHelpShortAddress(tok, fix(long)));
		}
		throw unknown();
	}
	const name = tok.slice(2);
	const eq = name.indexOf("=");
	if (eq >= 0) {
		const base = name.slice(0, eq);
		if (typed.includes(base)) {
			throw new ParseError(errHelpFlagWithValue(tok, fix(base)));
		}
		throw unknown();
	}
	if (typed.includes(name)) {
		req.flag = name;
		req.address.push(tok);
		req.isGlobal = !own.includes(name);
		return;
	}
	for (const prefix of ["no-", unsetFlagName("")]) {
		if (name.startsWith(prefix) && typed.includes(name.slice(prefix.length))) {
			throw new ParseError(
				errHelpNegatedFlag(tok, fix(name.slice(prefix.length))),
			);
		}
	}
	throw unknown();
}

/** The help command's own page. */
export function formatHelpOwnPage(app: AppImpl): string {
	return [
		`${app.name} help -- show the help of the app, a group, a command, or one of its flags`,
		"",
		"Arguments:",
		`  address...    groups, then a command, then one of its flags, as typed after '${app.name}' [optional]`,
		"",
		"Flags:",
		"  --depth <int>    levels of groups and commands to list below the address, written before it [optional]",
		"",
		"With --json, the help document is printed as JSON.",
	].join("\n");
}

/** The version command's own page. */
export function formatVersionOwnPage(app: AppImpl): string {
	return `${app.name} version -- show the app's name and version\n\nWith --json, they are printed as JSON.`;
}

/** Renders a parsed help request as text. */
export function helpText(app: AppImpl, req: HelpRequest): string {
	if (req.own) {
		return formatHelpOwnPage(app);
	}
	if (req.flag !== "") {
		const prefix =
			req.address.length > 2 ? `${req.address.slice(0, -2).join(" ")} ` : "";
		return formatFlagHelp(
			app,
			req.cmd as RegisteredCommand,
			prefix,
			req.flag,
			req.isGlobal,
		);
	}
	if (req.cmd !== undefined) {
		const prefix =
			req.address.length > 1 ? `${req.address.slice(0, -1).join(" ")} ` : "";
		return formatCommandHelp(app, req.cmd, prefix);
	}
	if (req.group !== undefined) {
		return formatGroupHelp(
			app,
			req.group,
			req.groupPath,
			Math.max(req.depth, 1),
		);
	}
	return formatAppHelp(app, Math.max(req.depth, 1));
}

type JsonObject = Record<string, unknown>;

/**
 * Keeps the flag entries that declare `name` somewhere in their subtree,
 * pruning selectors to the choices that do.
 */
function filterFlagEntries(
	entries: readonly unknown[],
	name: string,
): JsonObject[] {
	const kept: JsonObject[] = [];
	for (const item of entries) {
		const entry = item as JsonObject;
		const electBy = entry.elect_by;
		if (electBy === undefined) {
			if (entry.name === name) {
				kept.push(entry);
			}
			continue;
		}
		if (electBy === "selector-token" && entry.name === name) {
			kept.push(entry);
			continue;
		}
		const keptChoices: JsonObject[] = [];
		for (const c of (entry.choices as readonly JsonObject[] | undefined) ??
			[]) {
			if (electBy === "member-flags" && c.name === name) {
				keptChoices.push(c);
				continue;
			}
			if (Array.isArray(c.flags)) {
				const sub = filterFlagEntries(c.flags, name);
				if (sub.length > 0) {
					c.flags = sub;
					keptChoices.push(c);
				}
			}
		}
		if (keptChoices.length > 0) {
			entry.choices = keptChoices;
			kept.push(entry);
		}
	}
	return kept;
}

/** Keeps `depth` levels of the command tree below one node. */
function pruneGroupDepth(node: JsonObject, depth: number): void {
	const groups = node.groups as Record<string, JsonObject> | undefined;
	for (const g of Object.values(groups ?? {})) {
		if (depth <= 1) {
			delete g.commands;
			delete g.groups;
			delete g.deprecated;
			continue;
		}
		pruneGroupDepth(g, depth - 1);
	}
}

/**
 * The project_id of the help document: the "name" of the nearest package.json
 * above the real path of the program's entry script, so an installed program
 * names its project wherever it runs.
 */
export function projectIdForEntry(entry: string | undefined): string {
	if (entry === undefined || entry === "") {
		throw new Error(
			errProjectIDUndetermined("the program has no entry script"),
		);
	}
	let path: string;
	try {
		path = realpathSync(entry);
	} catch {
		path = entry;
	}
	let dir = dirname(path);
	for (;;) {
		const candidate = join(dir, "package.json");
		let raw: string | undefined;
		try {
			raw = readFileSync(candidate, "utf8");
		} catch {
			raw = undefined;
		}
		if (raw !== undefined) {
			let parsed: unknown;
			try {
				parsed = JSON.parse(raw);
			} catch (e) {
				throw new Error(
					errProjectIDUndetermined(
						`'${candidate}' is not valid JSON: ${(e as Error).message}`,
					),
				);
			}
			const name =
				typeof parsed === "object" && parsed !== null
					? (parsed as { name?: unknown }).name
					: undefined;
			if (typeof name !== "string" || name === "") {
				throw new Error(
					errProjectIDUndetermined(`'${candidate}' declares no name`),
				);
			}
			return name;
		}
		const parent = dirname(dir);
		if (parent === dir) {
			throw new Error(
				errProjectIDUndetermined(`no package.json above '${path}'`),
			);
		}
		dir = parent;
	}
}

/**
 * The help document: the whole app's schema, or the same document pruned to
 * the address, with `address` (and `depth`) recording the selection.
 */
export function helpDocument(app: AppImpl, req: HelpRequest): string {
	let doc: JsonObject = dumpSchema(app, projectIdForEntry(process.argv[1]));
	if (req.address.length > 0 || req.depth > 0) {
		const rebuilt: JsonObject = {};
		for (const [key, value] of Object.entries(doc)) {
			rebuilt[key] = value;
			if (key === "project_id") {
				if (req.address.length > 0) {
					rebuilt.address = [...req.address];
				}
				if (req.depth > 0) {
					rebuilt.depth = BigInt(req.depth);
				}
			}
		}
		doc = rebuilt;
	}
	let node = doc;
	for (const g of req.groupPath) {
		delete node.commands;
		delete node.deprecated;
		const child = (node.groups as Record<string, JsonObject>)[g] as JsonObject;
		node.groups = { [g]: child };
		node = child;
	}
	if (req.cmd !== undefined) {
		delete node.groups;
		delete node.deprecated;
		const name = req.cmd.name;
		const entry = (node.commands as Record<string, JsonObject>)[
			name
		] as JsonObject;
		node.commands = { [name]: entry };
		if (req.flag !== "") {
			for (const [obj, key] of [
				[entry, "flags"],
				[doc, "global_flags"],
			] as const) {
				const list = obj[key];
				if (Array.isArray(list)) {
					const kept = filterFlagEntries(list, req.flag);
					if (kept.length > 0) {
						obj[key] = kept;
					} else {
						delete obj[key];
					}
				}
			}
		}
	} else if (req.depth > 0) {
		pruneGroupDepth(node, req.depth);
	}
	return `${schemaJson(doc)}\n`;
}

/**
 * Handles `help` and `version` as the first command word. Returns undefined
 * when `rest` names neither; throws ParseError (carrying the command prefix)
 * on a refusal.
 */
export function dispatchFrameworkCommand(
	app: AppImpl,
	rest: readonly string[],
	json: boolean,
): FrameworkCommandResult | undefined {
	const first = rest[0];
	if (first === "help") {
		const req = parseHelpCommand(app, rest.slice(1));
		if (req.own || !json) {
			if (json) {
				throw new ParseError(errHelpTextOnly(`${app.name} help --json`));
			}
			return { kind: "page", text: helpText(app, req) };
		}
		let document: string;
		try {
			document = helpDocument(app, req);
		} catch (e) {
			throw new ParseError((e as Error).message);
		}
		return { kind: "document", document, command: "help" };
	}
	if (first === "version") {
		if (rest.slice(1).some((t) => t === "--help" || t === "-h")) {
			if (json) {
				throw new ParseError(errHelpTextOnly(`${app.name} help --json`));
			}
			return { kind: "page", text: formatVersionOwnPage(app) };
		}
		if (rest.length > 1) {
			throw new ParseError(errVersionArgs(rest[1] as string));
		}
		if (!json) {
			return { kind: "page", text: formatVersion(app) };
		}
		return {
			kind: "document",
			document: `${schemaJson({ name: app.name, version: app.version })}\n`,
			command: "version",
		};
	}
	return undefined;
}
