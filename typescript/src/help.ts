/**
 * Help rendering at app/group/command levels, byte-identical to the siblings
 * (Go help.go and Python's _format_app_help/_format_group_help family). The
 * output is pinned by conformance/cases/help.json plus the help expectations
 * scattered across the other case files.
 *
 * Sibling divergences resolved here:
 * - App-level "Global flags:" section: Python renders it (name/short + help,
 *   no meta), Go omits it entirely; no conformance case covers it. TS follows
 *   Python (the divergence ground truth).
 * - Every flag and every arg renders exactly one presence part, last on the
 *   line: "[required]", "[optional]" or "[default: <value>]" (contract §23.8).
 *   A declared empty collection renders "[default: []]" / "[default: {}]".
 * - List carriers render "[repeatable]" when declared with repeatable: true
 *   (the sibling scalar-repeatable shape) and "[list]" otherwise (the sibling
 *   compound-list shape).
 */

import type { AppImpl, GroupImpl, RegisteredCommand } from "./app.js";
import {
	constraintIndex,
	helpSentence,
	resolveConstraints,
} from "./constraints.js";
import {
	type AnyArg,
	type AnyChoice,
	type AnyChoiceFlag,
	type AnyCommand,
	type AnyDecl,
	type AnyFlag,
	anyChoiceHasHelp,
	type ChoiceRecordView,
	choiceValues,
	elemSchemaOf,
	flagOpts,
	memberShort,
	type Presence,
	schemaKind,
	unsetFlagName,
} from "./factories.js";
import { formatFloatCanonical } from "./float.js";
import {
	formatChoices,
	formatDictForDisplay,
	formatValueForError,
} from "./values.js";

export function formatVersion(app: AppImpl): string {
	return `${app.name} ${app.version}`;
}

/** Two-column section body: "  name<pad>text" with 4-space gutter. */
function twoColumn(rows: readonly (readonly [string, string])[]): string[] {
	const maxLen = Math.max(...rows.map(([left]) => left.length));
	return rows.map(
		([left, right]) =>
			`  ${left}${" ".repeat(maxLen - left.length + 4)}${right}`,
	);
}

function commandsSection(
	lines: string[],
	label: string,
	entries: readonly (readonly [string, string])[],
): void {
	if (entries.length > 0) {
		lines.push("", label, ...twoColumn(entries));
	}
}

/**
 * Lists the visible commands and groups within `depth` levels below one node,
 * depth-first in declaration order: the node's own commands, then each group
 * followed by what it holds. Level 1 is the node's own children, which is all
 * --help lists.
 */
function collectHelpTree(
	commands: ReadonlyMap<string, RegisteredCommand>,
	groups: ReadonlyMap<string, GroupImpl>,
	prefix: string,
	level: number,
	depth: number,
	commandsOut: [string, string][],
	groupsOut: [string, string][],
): void {
	for (const c of commands.values()) {
		if (!c.hidden) {
			commandsOut.push([prefix + c.name, c.help]);
		}
	}
	for (const g of groups.values()) {
		if (g.hidden) {
			continue;
		}
		groupsOut.push([prefix + g.name, g.help]);
		if (level < depth) {
			collectHelpTree(
				g.commands,
				g.groups,
				`${prefix}${g.name} `,
				level + 1,
				depth,
				commandsOut,
				groupsOut,
			);
		}
	}
}

/**
 * The app's help page listing `depth` levels of its command tree; depth 1 is
 * the page --help shows.
 */
export function formatAppHelp(app: AppImpl, depth = 1): string {
	const lines: string[] = [`${app.name} v${app.version} -- ${app.help}`];

	const commandRows: [string, string][] = [];
	const groupRows: [string, string][] = [];
	collectHelpTree(
		app.commands,
		app.groups,
		"",
		1,
		depth,
		commandRows,
		groupRows,
	);
	commandsSection(lines, "Commands:", commandRows);
	commandsSection(lines, "Groups:", groupRows);
	commandsSection(
		lines,
		"Deprecated:",
		[...app.deprecated.entries()].map(([n, msg]) => [n, msg] as const),
	);

	if (app.globalFlags.length > 0) {
		// App-level global flags render name + short + help only (no meta),
		// mirroring Python's app help (Go has no app-level section at all).
		const rows = app.globalFlags.map((f) => {
			const parts = [`--${f.name}`];
			const short = flagOpts(f).short;
			if (short !== undefined && short !== "") {
				parts.push(`-${short}`);
			}
			return [parts.join(", "), f.opts.help] as const;
		});
		lines.push("", "Global flags:", ...twoColumn(rows));
	}

	if (
		app.infraRoots.size > 0 ||
		app.handshakeEnvs.size > 0 ||
		app.connectionEnvs.size > 0
	) {
		lines.push(
			"",
			"Infrastructure:",
			"  (location/handshake env vars; not suppressed by --hermetic)",
		);
		const rows: (readonly [string, string])[] = [
			...[...app.infraRoots.keys()].map(
				(ev) =>
					[
						ev,
						`root (default: ${app.infraRootDefaults.get(ev) ?? ""})`,
					] as const,
			),
			...[...app.handshakeEnvs.entries()].map(
				([ev, helpText]) => [ev, helpText] as const,
			),
			...[...app.connectionEnvs.entries()].map(
				([ev, helpText]) =>
					[
						ev,
						`connection URL, suppressed by --hermetic (${helpText})`,
					] as const,
			),
		];
		lines.push(...twoColumn(rows));
	}

	lines.push("", `Use '${app.name} help <command>' for more information.`);
	return lines.join("\n");
}

/** A group's help page listing `depth` levels below it. */
export function formatGroupHelp(
	app: AppImpl,
	group: GroupImpl,
	path: readonly string[],
	depth = 1,
): string {
	const fullPath = path.join(" ");
	const lines: string[] = [`${app.name} ${fullPath} -- ${group.help}`];

	const commandRows: [string, string][] = [];
	const groupRows: [string, string][] = [];
	collectHelpTree(
		group.commands,
		group.groups,
		"",
		1,
		depth,
		commandRows,
		groupRows,
	);
	commandsSection(lines, "Commands:", commandRows);
	commandsSection(lines, "Groups:", groupRows);
	commandsSection(
		lines,
		"Deprecated:",
		[...group.deprecated.entries()].map(([n, msg]) => [n, msg] as const),
	);

	lines.push(
		"",
		`Use '${app.name} help ${fullPath} <command>' for more information.`,
	);
	return lines.join("\n");
}

/** Formats a default value for help text (floats via SCF, dicts sorted). */
function formatDefaultForHelp(value: unknown): string {
	if (typeof value === "number") {
		return formatFloatCanonical(value);
	}
	if (value instanceof Map) {
		return formatDictForDisplay(value as ReadonlyMap<string, unknown>);
	}
	return String(value);
}

/**
 * The single presence part every flag and arg renders, LAST on its line
 * (contract §23.8). A declared empty collection renders `[default: []]` /
 * `[default: {}]`: it is a declaration now, and a declaration that rendered as
 * blank would leave that one line with no presence part at all.
 */
function presenceMeta(
	presence: Presence,
	dflt: unknown,
	kind: "scalar" | "list" | "dict",
	schema: string,
): string {
	if (presence === "required") {
		return "required";
	}
	if (presence === "optional") {
		return "optional";
	}
	if (kind === "dict") {
		const m = dflt as ReadonlyMap<string, unknown>;
		return `default: ${m.size === 0 ? "{}" : formatDefaultForHelp(m)}`;
	}
	if (kind === "list") {
		const items = dflt as readonly unknown[];
		return `default: ${
			items.length === 0 ? "[]" : items.map(formatValueForError).join(", ")
		}`;
	}
	if (schema === "bool") {
		return `default: ${dflt === true ? "true" : "false"}`;
	}
	return `default: ${formatDefaultForHelp(dflt)}`;
}

/** Left-column spec string for a flag (e.g. "--target, -t <str>"). */
export function buildFlagSpec(f: AnyFlag): string {
	const o = flagOpts(f);
	const parts: string[] = [];
	if (f.schema === "bool" && o.negatable !== false) {
		parts.push(`--${f.name}, --no-${f.name}`);
	} else {
		parts.push(`--${f.name}`);
	}
	if (o.short !== undefined && o.short !== "") {
		parts.push(`-${o.short}`);
	}
	let spec = parts.join(", ");
	const kind = schemaKind(f.schema);
	if (kind === "list") {
		spec += ` <${elemSchemaOf(f.carrier)}>`;
	} else if (kind === "dict") {
		spec += ` <key=${elemSchemaOf(f.carrier)}>`;
	} else if (f.schema !== "bool") {
		spec += ` <${f.schema}>`;
	}
	// A nullable property renders its minted clear spelling on the SAME line,
	// exactly as a negatable bool renders `--x, --no-x` (contract §27.6): one
	// line, one help text, and one presence part -- §23.8's invariant is
	// untouched, because the minted spelling is a second way to write to one
	// declaration rather than a second declaration.
	if (o.nullable === true) {
		spec += `, --${unsetFlagName(f.name)}`;
	}
	return spec;
}

/** Bracketed metadata suffix for a flag (" [x] [y]" form). */
export function buildFlagMeta(f: AnyFlag): string {
	const o = flagOpts(f);
	const kind = schemaKind(f.schema);
	const metaParts: string[] = [];
	if (kind === "dict") {
		metaParts.push("dict");
	} else if (kind === "list") {
		metaParts.push(o.repeatable === true ? "repeatable" : "list");
	}
	if (o.unique === true) {
		metaParts.push("unique");
	}
	// The one-line `[choices: a, b, c]` form survives for a value flag whose
	// entries carry no help and no scope; anything richer renders as the
	// indented block below (§24.10).
	if (o.choices !== undefined && !anyChoiceHasHelp(o.choices)) {
		metaParts.push(`choices: ${formatChoices(choiceValues(o.choices))}`);
	}
	if (o.env !== undefined) {
		metaParts.push(
			o.envSeparator !== undefined
				? `env: ${o.env} (sep: ${o.envSeparator})`
				: `env: ${o.env}`,
		);
	}
	metaParts.push(presenceMeta(o.presence, o.default, kind, f.schema));
	return ` [${metaParts.join("] [")}]`;
}

function flagRows(flags: readonly AnyFlag[]): (readonly [string, string])[] {
	return flags.map(
		(f) => [buildFlagSpec(f), `${f.opts.help}${buildFlagMeta(f)}`] as const,
	);
}

/**
 * One rendered line of the command's flag block: its left column already
 * carries its indentation, so the alignment column below is computed across
 * the WHOLE block, deepest entry included (contract §24.10).
 */
interface FlagBlockRow {
	readonly left: string;
	readonly right: string;
}

/** Two columns of indent per level, exactly as §24.10's layout pins. */
const SCOPE_INDENT = "  ";

/**
 * A choice-carrying flag renders as an INDENTED BLOCK iff any of its choices
 * carries help OR a scope; otherwise it keeps the one-line
 * `[choices: a, b, c]` form (§24.10).
 *
 * A selector is therefore always a block -- its choices carry mandatory help
 * -- and a value flag is a block exactly when its entries were given help.
 */
function declBlockRows(decl: AnyDecl, depth: number): FlagBlockRow[] {
	const pad = SCOPE_INDENT.repeat(depth);
	if (decl.kind === "choice-flag") {
		return selectorBlockRows(decl, depth);
	}
	const rows: FlagBlockRow[] = [
		{
			left: `${pad}${buildFlagSpec(decl)}`,
			right: `${decl.opts.help}${buildFlagMeta(decl)}`,
		},
	];
	const choices = flagOpts(decl).choices;
	if (choices !== undefined && anyChoiceHasHelp(choices)) {
		// A value flag's entries in block form render the VALUE where a choice
		// name renders, followed by its help; an entry with no help renders the
		// value alone.
		for (const c of choices) {
			rows.push({
				left: `${pad}${SCOPE_INDENT}${formatValueForError(c.value)}`,
				right: c.help ?? "",
			});
		}
	}
	return rows;
}

/**
 * The one presence part a selector's line carries (§23.8, §24.10).
 *
 * A DEFAULTED selector renders its complete elected value, because that is
 * what a default is (§24.5): `[default: <choice> (<field>=<value>, ...)]`, the
 * elected choice's own scalar fields in declaration order, joined by `, `. A
 * choice whose scope is empty -- or whose fields are all nested selectors --
 * renders `[default: <choice>]` with no parenthesized part at all, never an
 * empty `()`. A nested selector is not expanded inline: it opens its own line
 * in the block and states its own default there (§18.19 item 215).
 */
function selectorPresenceMeta(sel: AnyChoiceFlag): string {
	if (sel.opts.presence === "required") {
		return "required";
	}
	const elected = sel.opts.default as string;
	const parts: string[] = [];
	for (const sub of Object.values(sel.choices[elected]?.flags ?? {})) {
		// A nested selector has its own line; a field with no declared value has
		// nothing to render, and `null` is not a declarable default anywhere in
		// the framework, so an omission is unambiguous.
		if (sub.kind !== "flag") {
			continue;
		}
		const o = flagOpts(sub);
		if (o.presence !== "default") {
			continue;
		}
		parts.push(`${sub.name}=${formatDefaultForHelp(o.default)}`);
	}
	return `default: ${elected}${parts.length === 0 ? "" : ` (${parts.join(", ")})`}`;
}

/** The left-column spec of one member flag under member spelling. */
function buildMemberSpec(choiceName: string, c: AnyChoice): string {
	// A payload-less member and a bool-payload member are both typed as a bare
	// flag; only a value-carrying member renders the value it takes.
	const payload = c.value;
	const short = memberShort(c);
	const spec =
		short === undefined ? `--${choiceName}` : `--${choiceName}, -${short}`;
	return payload === undefined || payload.carrier.schema === "bool"
		? spec
		: `${spec} <${payload.carrier.schema}>`;
}

/** The selector block: its own line, then one line per choice, then each scope. */
function selectorBlockRows(sel: AnyChoiceFlag, depth: number): FlagBlockRow[] {
	const pad = SCOPE_INDENT.repeat(depth);
	const rows: FlagBlockRow[] = [];
	const presence = selectorPresenceMeta(sel);
	if (sel.electBy === "member-flags") {
		// A member-spelled selector has NO token to render, so its left column
		// carries its bare name -- the handler's key and the noun errors use,
		// never something a user types -- and its right column carries its help,
		// the clause `(exactly one of the following)` and its presence part, in
		// that order (§24.10).
		rows.push({
			left: `${pad}${sel.name}`,
			right: `${sel.opts.help} (exactly one of the following) [${presence}]`,
		});
	} else {
		const parts = [`--${sel.name}`];
		if (sel.opts.short !== undefined && sel.opts.short !== "") {
			parts.push(`-${sel.opts.short}`);
		}
		rows.push({
			left: `${pad}${parts.join(", ")} <choice>`,
			right: `${sel.opts.help} [${presence}]`,
		});
	}
	for (const [choiceName, c] of Object.entries(sel.choices)) {
		// A member flag is an ordinary flag line, so §23.8's presence invariant
		// holds on it too: it ends with exactly one presence part, and a member
		// is required once elected (item 161).
		rows.push(
			sel.electBy === "member-flags"
				? {
						left: `${pad}${SCOPE_INDENT}${buildMemberSpec(choiceName, c)}`,
						right: `${c.help} [required]`,
					}
				: { left: `${pad}${SCOPE_INDENT}${choiceName}`, right: c.help },
		);
		for (const sub of Object.values(c.flags)) {
			rows.push(...declBlockRows(sub, depth + 2));
		}
	}
	return rows;
}

/**
 * The flag block's rows a one-flag help page shows: the declarations of that
 * name, with the selector and choice lines above a scoped one, each rendered
 * exactly as the whole page renders it.
 */
function declBlockRowsFor(
	decl: AnyDecl,
	depth: number,
	name: string,
): FlagBlockRow[] {
	if (decl.kind !== "choice-flag") {
		return decl.name === name ? declBlockRows(decl, depth) : [];
	}
	const member = decl.electBy === "member-flags";
	if (!member && decl.name === name) {
		return selectorBlockRows(decl, depth);
	}
	const pad = SCOPE_INDENT.repeat(depth);
	const sub: FlagBlockRow[] = [];
	for (const [choiceName, c] of Object.entries(decl.choices)) {
		const choiceRow: FlagBlockRow = member
			? {
					left: `${pad}${SCOPE_INDENT}${buildMemberSpec(choiceName, c)}`,
					right: `${c.help} [required]`,
				}
			: { left: `${pad}${SCOPE_INDENT}${choiceName}`, right: c.help };
		if (member && choiceName === name) {
			sub.push(choiceRow);
			for (const inner of Object.values(c.flags)) {
				sub.push(...declBlockRows(inner, depth + 2));
			}
			continue;
		}
		const inner = Object.values(c.flags).flatMap((d) =>
			declBlockRowsFor(d, depth + 2, name),
		);
		if (inner.length > 0) {
			sub.push(choiceRow, ...inner);
		}
	}
	if (sub.length === 0) {
		return [];
	}
	return [selectorBlockRows(decl, depth)[0] as FlagBlockRow, ...sub];
}

/**
 * The one-flag help page: the command's header line, then the section holding
 * the flag, as the command's page renders it.
 */
export function formatFlagHelp(
	app: AppImpl,
	cmd: RegisteredCommand,
	prefix: string,
	name: string,
	isGlobal: boolean,
): string {
	const lines: string[] = [`${app.name} ${prefix}${cmd.name} -- ${cmd.help}`];
	if (isGlobal) {
		const f = app.globalFlags.find((g) => g.name === name);
		if (f !== undefined) {
			lines.push("", "Global flags:", ...twoColumn(flagRows([f])));
		}
		return lines.join("\n");
	}
	const decls = cmd.def.kind === "passthrough" ? [] : cmd.def.allDecls;
	lines.push(
		"",
		"Flags:",
		...renderBlock(decls.flatMap((d) => declBlockRowsFor(d, 0, name))),
	);
	return lines.join("\n");
}

/**
 * Renders the command's whole flag block with ONE alignment column computed
 * across every entry, deepest included, so help text starts in the same
 * column everywhere on the page (§24.10).
 */
function flagBlock(decls: readonly AnyDecl[]): string[] {
	return renderBlock(decls.flatMap((d) => declBlockRows(d, 0)));
}

/**
 * Renders block rows against ONE alignment column, deepest entry included. A
 * row whose right column is empty (a choice entry declaring no help) keeps no
 * trailing padding.
 */
function renderBlock(rows: readonly FlagBlockRow[]): string[] {
	const maxLen = Math.max(...rows.map((r) => r.left.length));
	return rows.map((r) =>
		`  ${r.left}${" ".repeat(maxLen - r.left.length + 4)}${r.right}`.trimEnd(),
	);
}

function argDisplayName(a: AnyArg): string {
	return a.opts.variadic === true ? `${a.name}...` : a.name;
}

/** An arg's declared `choices` entries, or undefined when it declares none. */
function argChoices(a: AnyArg): readonly ChoiceRecordView[] | undefined {
	return (a.opts as { readonly choices?: readonly ChoiceRecordView[] }).choices;
}

/**
 * Every line the `Arguments:` section renders, as block rows: the arg's own
 * line, and -- when any of its `choices` entries carries help -- one indented
 * line per entry, value first and help second (§24.10, §18.19 item 218).
 *
 * The block rule is content-keyed, never surface-keyed: an arg cannot own a
 * scope (§24.7), so help on an entry is the only thing that can promote it,
 * and an arg whose entries carry no help keeps the one-line form unchanged.
 */
function argBlockRows(args: readonly AnyArg[]): FlagBlockRow[] {
	const rows: FlagBlockRow[] = [];
	for (const a of args) {
		rows.push({
			left: argDisplayName(a),
			right: `${a.opts.help}${argMeta(a)}`,
		});
		const choices = argChoices(a);
		if (choices === undefined || !anyChoiceHasHelp(choices)) {
			continue;
		}
		for (const c of choices) {
			rows.push({
				left: `${SCOPE_INDENT}${formatValueForError(c.value)}`,
				right: c.help ?? "",
			});
		}
	}
	return rows;
}

function argMeta(a: AnyArg): string {
	const metaParts: string[] = [];
	// The ELEMENT type, so a variadic arg reads the same in either spelling:
	// the element carrier plus `variadic: true`, or the list carrier (§25.4).
	const elem = elemSchemaOf(a.carrier);
	if (elem !== "str") {
		metaParts.push(`type: ${elem}`);
	}
	const choices = argChoices(a);
	// The one-line `[choices: a, b, c]` form survives only while the arg
	// renders as one line: once any entry carries help, the entries render as
	// the block above instead.
	if (choices !== undefined && !anyChoiceHasHelp(choices)) {
		metaParts.push(`choices: ${formatChoices(choiceValues(choices))}`);
	}
	// Args carry the same single presence part flags do -- a required
	// positional renders `[required]` where nothing was rendered before, since
	// this framework's help has no usage line to show requiredness any other
	// way (contract §23.8).
	metaParts.push(presenceMeta(a.opts.presence, a.opts.default, "scalar", elem));
	return ` [${metaParts.join("] [")}]`;
}

/**
 * Renders the `Dry run:` section of command help, or nothing. It appears only
 * for a command that declares `dryRunSupported: false`: the baseline (dry run
 * works) needs no announcement, and a section on every command would be noise.
 * Byte-identical across implementations.
 */
function formatDryRunSection(cmd: RegisteredCommand): readonly string[] {
	const def = cmd.def as {
		readonly dryRunSupported?: boolean;
		readonly dryRunUnsupportedReason?: string;
	};
	if (def.dryRunSupported !== false) {
		return [];
	}
	return [
		"",
		"Dry run:",
		`  --dry-run is not supported: ${def.dryRunUnsupportedReason ?? ""}`,
	];
}

export function formatCommandHelp(
	app: AppImpl,
	cmd: RegisteredCommand,
	prefix: string,
): string {
	const lines: string[] = [`${app.name} ${prefix}${cmd.name} -- ${cmd.help}`];

	// Rendered before the passthrough early-return: a passthrough command can
	// declare the refusal too, and its help is the only place the reason would
	// otherwise be visible.
	lines.push(...formatDryRunSection(cmd));

	// Passthrough commands show only the header line.
	if (cmd.def.kind === "passthrough") {
		return lines.join("\n");
	}
	const def = cmd.def;

	if (def.args.length > 0) {
		// The `Arguments:` section computes its own alignment column, deepest
		// entry included; it never shares the flag block's (§24.10).
		lines.push("", "Arguments:", ...renderBlock(argBlockRows(def.args)));
	}

	// One `Flags:` section for every declaration, in declaration order. The
	// separate `Flags (mutually exclusive):` section is gone with the construct
	// that produced it: an exactly-one group is now a member-spelled selector,
	// which renders as a block inside this one section (§21's box, §24.10).
	if (def.allDecls.length > 0) {
		lines.push("", "Flags:", ...flagBlock(def.allDecls));
	}
	// The `Constraints:` section, after the last of the Arguments/Flags blocks
	// (§26.10). This is the constraint system's FIRST rendering in any
	// implementation: a declared rule that decides whether an invocation is
	// accepted and that --help cannot show is the same erasure the presence
	// round ended for requiredness.
	if (def.constraints.length > 0) {
		lines.push("", "Constraints:", ...constraintBlock(def));
	}
	if (app.globalFlags.length > 0) {
		lines.push("", "Global flags:", ...twoColumn(flagRows(app.globalFlags)));
	}

	return lines.join("\n");
}

/**
 * One line per constraint, INCLUDING nested ones -- a nested constraint has
 * its own line and also appears inside its parent's line, because it is both
 * a rule of its own and an operand.
 *
 * The declared NAME renders in the position a flag name occupies, so the
 * identifier a violation prints is discoverable in the help the operator
 * already read. The alignment column is computed across this block alone and
 * never shares the flag block's, which is §24.10's rule for `Arguments:`
 * applied to a third section. A member's presence part is deliberately not
 * repeated: every flag line already carries exactly one (§23.8), and a
 * constraint states a rule over members rather than a property of one.
 */
function constraintBlock(def: AnyCommand): string[] {
	const resolved = resolveConstraints(def);
	const index = constraintIndex(resolved);
	return renderBlock(
		resolved.map((c) => ({
			left: c.name,
			right: helpSentence(c, index),
		})),
	);
}
