// TypeScript conformance harness. Mirrors conformance/harness/main.go in
// contract: reads the app-definition JSON file path from the
// CONFORMANCE_APP_DEF env var, builds the app through the TS public API
// (typescript/dist), and runs app.run() against process.argv.slice(2).
// Registration errors print "error: <msg>" to stderr and exit 1 (the Go
// harness's recover semantics / the Python ref's `except ValueError` wrap).
//
// Import mechanism: direct relative import of the built dist. The harness is
// plain Node ESM (no tsconfig, no install, no build of its own); its only
// prerequisite is `cd typescript && npm run build`. Bare specifiers used by
// the dist itself (smol-toml) resolve through typescript/node_modules via
// Node's directory walk-up, because the imported files live under typescript/.
import { readFileSync, readSync, writeFileSync } from "node:fs";

import { setConfirmIO } from "../../typescript/dist/confirm.js";
import {
	errChoiceDuplicateName,
	errCommandDuplicateFlag,
	errCommandPassthroughCannotHave,
	errDuplicateGlobalFlag,
	errFlagRepeatableRequiresExplicitUnique,
} from "../../typescript/dist/errors.js";
import { formatFloatCanonical } from "../../typescript/dist/float.js";
import {
	allOrNone,
	arg,
	atLeastOne,
	choice,
	choiceFlag,
	createApp,
	defineMutatingCommand,
	defineReadOnlyCommand,
	deprecated,
	errorCheckSpec,
	flag,
	flagSet,
	implies,
	mutatingPassthrough,
	memberChoiceFlag,
	outcome,
	provided,
	readOnlyPassthrough,
	relativeToRoot,
	requires,
	t,
	warnCheckSpec,
} from "../../typescript/dist/index.js";

// The message a `handler_aborts` handler throws, identical in all three
// harnesses so an aborting case's stderr line is byte-identical across targets.
const HANDLER_ABORT_MESSAGE = "conformance: handler aborted";

/**
 * Reads one line from fd 0 synchronously, WITHOUT its trailing newline -- the
 * contract of the ConfirmIO.readLine member. The framework's own reader is
 * module-private in confirm.ts, so the harness carries its own copy; both stop
 * at the first newline and both tolerate a non-blocking descriptor.
 */
function readLineFromStdin() {
	const buf = Buffer.alloc(1);
	let line = "";
	for (;;) {
		let n;
		try {
			n = readSync(0, buf, 0, 1, null);
		} catch (e) {
			if (e.code === "EAGAIN") {
				continue;
			}
			break;
		}
		if (n === 0) {
			break;
		}
		const ch = buf.toString("utf8", 0, 1);
		if (ch === "\n") {
			break;
		}
		line += ch;
	}
	return line;
}

function underscore(name) {
	return name.replaceAll("-", "_");
}

/** Literal (non-regex, non-$-pattern) replace-all. */
function subst(text, needle, replacement) {
	return text.split(needle).join(replacement);
}

// ---------------------------------------------------------------------------
// Value rendering (the cross-target template vocabulary): bool -> true/false,
// nil/None -> None, int (bigint) -> decimal, float -> canonical form, lists/
// variadics comma-join, dicts (Maps) comma-join k=v with keys sorted.
// ---------------------------------------------------------------------------
function render(v) {
	if (v === undefined || v === null) {
		return "None";
	}
	switch (typeof v) {
		case "boolean":
			return v ? "true" : "false";
		case "bigint":
			return v.toString();
		case "number":
			return formatFloatCanonical(v);
		case "string":
			return v;
		default:
			break;
	}
	if (Array.isArray(v)) {
		return v.map(render).join(",");
	}
	if (v instanceof Map) {
		return [...v.keys()]
			.sort()
			.map((k) => `${k}=${render(v.get(k))}`)
			.join(",");
	}
	return String(v);
}

// ---------------------------------------------------------------------------
// Carriers
// ---------------------------------------------------------------------------
function scalarCarrier(typeName) {
	switch (typeName) {
		case "bool":
			return t.bool;
		case "int":
			return t.int;
		case "float":
			return t.float;
		default:
			return t.str;
	}
}

/**
 * The carrier a declared ARG type names. §25.4 unified the two variadic
 * spellings, so a list carrier is a legal arg declaration in every
 * implementation and the corpus must be able to spell it.
 */
function argCarrier(typeName) {
	switch (typeName) {
		case "list[str]":
			return t.list(t.str);
		case "list[int]":
			return t.list(t.int);
		case "list[float]":
			return t.list(t.float);
		default:
			return scalarCarrier(typeName);
	}
}

/** Scalar JSON value -> the TS runtime value for a strictcli type. */
function convertScalar(typeName, v) {
	if (v === null || v === undefined) {
		return v;
	}
	// Only JSON numbers become bigints; a mistyped value (e.g. a string
	// default on an int flag) carries over as-is so the framework mints its
	// own default-type registration error (the Go harness's `if float64`
	// pattern).
	if (typeName === "int" && typeof v === "number") {
		return BigInt(v);
	}
	return v; // bool, float, str carry over as-is
}

/** Element type of "list[int]" / "dict[str,int]" -> "int". */
function elemTypeOf(ftype) {
	if (ftype.startsWith("list[")) {
		return ftype.slice(5, -1);
	}
	if (ftype.startsWith("dict[")) {
		return ftype.slice(9, -1);
	}
	return ftype;
}

// ---------------------------------------------------------------------------
// Choices: the record spelling, and the scoped-selector construct
// ---------------------------------------------------------------------------

/**
 * Converts a declaration's record-shaped `choices_<T>` key into the record
 * literals the TS surface takes (contract §24.2). The typed split survives on
 * the input side alone: JSON cannot tell an integer choice from a float one.
 */
function choiceRecords(d) {
	if ("choices_str" in d) {
		return d.choices_str.map(choiceRecord((v) => v));
	}
	if ("choices_int" in d) {
		return d.choices_int.map(choiceRecord((v) => BigInt(v)));
	}
	if ("choices_float" in d) {
		return d.choices_float.map(choiceRecord((v) => v));
	}
	return undefined;
}

function choiceRecord(conv) {
	return (rec) => {
		// A BARE entry is spellable so the deleted-entry refusal has a covering
		// input (§12.13's errChoicesEntryNotRecord): it reaches the factory as
		// the bare value the public type would have rejected.
		if (rec === null || typeof rec !== "object") {
			return conv(rec);
		}
		return "help" in rec
			? { value: conv(rec.value), help: rec.help }
			: { value: conv(rec.value) };
	};
}

/**
 * Converts a declaration's record-shaped `retired_choices_<T>` key into the
 * record literals the TS surface takes. The typed split mirrors
 * choiceRecords's, for the same input-side reason.
 */
function retiredChoiceRecords(d) {
	if ("retired_choices_str" in d) {
		return d.retired_choices_str.map(retiredChoiceRecord((v) => v));
	}
	if ("retired_choices_int" in d) {
		return d.retired_choices_int.map(retiredChoiceRecord((v) => BigInt(v)));
	}
	if ("retired_choices_float" in d) {
		return d.retired_choices_float.map(retiredChoiceRecord((v) => v));
	}
	return undefined;
}

function retiredChoiceRecord(conv) {
	return (rec) => {
		// A bare entry is only refusable in Python, whose keyword takes a list
		// of anything; the TS record type refuses it at compile time, so the
		// bare spelling reaches the factory as a value with no message and is
		// answered by the empty-message guard.
		if (rec === null || typeof rec !== "object") {
			return { value: conv(rec), message: "" };
		}
		return { value: conv(rec.value), message: rec.message };
	};
}

/** `elect_by` is the input-side discriminator (§13's item-207 box, §25.6). */
function isSelector(fd) {
	return "elect_by" in fd;
}

/**
 * Builds one choice of a selector. `value` declares a member-spelled choice's
 * own payload, and stays spellable on a token-spelled choice because that is
 * the input errTokenChoiceCarriesPayload is asserted against.
 *
 * `presence` is the WIDENED call §12.13 names: the declared choice type has no
 * per-member presence slot, so a case reaching errMemberFlagPresence hands the
 * factory a value the public type would have rejected.
 */
function buildChoice(cd) {
	const spec = { help: cd.help };
	if ("flags" in cd) {
		spec.flags = flagMapOf(cd.flags, (fn) => errCommandDuplicateFlag(cd.name, fn));
	}
	if ("value" in cd) {
		// The payload is a carrier plus its OWN help, which is what §25.6's
		// `value` entry publishes and what the member's flattened MCP property
		// is described by -- the choice's help documents electing it instead.
		// `short` rides the payload because that declaration IS the electing
		// flag's (§24.12).
		spec.value = { carrier: scalarCarrier(cd.value.type), help: cd.value.help };
		if ("short" in cd.value) {
			spec.value.short = cd.value.short;
		}
	}
	// The choice-level short: a payload-less member's own. Spellable beside a
	// `value` and on a token-spelled choice too -- those are the inputs the two
	// short refusals are asserted against.
	if ("short" in cd) {
		spec.short = cd.short;
	}
	if ("args" in cd) {
		// A positional inside a scope is inexpressible through the declared
		// choice type; the widened flags map IS the covering input the
		// scoped-positional refusal is asserted against (§12.13).
		spec.flags = { ...(spec.flags ?? {}) };
		for (const ad of cd.args) {
			spec.flags[underscore(ad.name)] = buildArg(ad);
		}
	}
	const built = choice(spec);
	return "presence" in cd ? { ...built, presence: cd.presence } : built;
}

/** Builds a selector from a flag definition carrying `elect_by` (§24.12). */
function buildSelectorFlag(fd) {
	// A JS object collapses a same-name duplicate silently, so the framework's
	// duplicate-choice check is replayed here first with its own catalog
	// message -- the same convention flagMapOf already follows for flags.
	const choices = {};
	for (const cd of fd.choices ?? []) {
		if (cd.name in choices) {
			throw new Error(errChoiceDuplicateName(fd.name, cd.name));
		}
		choices[cd.name] = buildChoice(cd);
	}
	const opts = { help: fd.help };
	if ("short" in fd) {
		opts.short = fd.short;
	}
	if ("presence" in fd) {
		opts.presence = fd.presence;
	}
	if ("default" in fd) {
		// TypeScript's default NAMES a choice (§24.5); the case spells the same
		// flat map §25.6 publishes, and errSelectorDefaultIncomplete is what
		// guarantees the named choice's scope is complete.
		opts.default =
			fd.default !== null && typeof fd.default === "object"
				? fd.default.choice
				: fd.default;
	}
	if ("env" in fd) {
		opts.env = fd.env;
	}
	if ("prefixed" in fd) {
		opts.prefixed = fd.prefixed;
	}
	if ("conflict_mode" in fd) {
		opts.conflictMode = fd.conflict_mode;
	}
	return fd.elect_by === "member-flags"
		? memberChoiceFlag(fd.name, choices, opts)
		: choiceFlag(fd.name, choices, opts);
}

// ---------------------------------------------------------------------------
// Delivered records in handler templates (contract §24.1, §24.9)
//
// One rendering, three harnesses: `<choice>` when the scope is empty and
// `<choice>(<field>=<value>, ...)` otherwise, fields in declaration order with
// a member payload first (where §25.6 places it).
// ---------------------------------------------------------------------------

function findChoiceDef(fd, name) {
	return (fd?.choices ?? []).find((cd) => cd.name === name);
}

function recordFieldOrder(cd) {
	const out = [];
	if (cd === undefined) {
		return out;
	}
	if ("value" in cd) {
		out.push("value");
	}
	for (const f of cd.flags ?? []) {
		out.push(underscore(f.name));
	}
	return out;
}

function findFieldDef(cd, key) {
	return (cd?.flags ?? []).find((f) => underscore(f.name) === key);
}

function isRecord(v) {
	return typeof v === "object" && v !== null && typeof v.choice === "string";
}

function renderScoped(v, def) {
	return isRecord(v) ? renderElected(v, def) : render(v);
}

function renderElected(rec, fd) {
	const cd = findChoiceDef(fd, rec.choice);
	const order = recordFieldOrder(cd);
	if (order.length === 0) {
		return rec.choice;
	}
	return `${rec.choice}(${order
		.map((k) => `${k}=${renderScoped(rec[k], findFieldDef(cd, k))}`)
		.join(", ")})`;
}

function selWalk(v, def, parts) {
	let cur = v;
	let d = def;
	for (const p of parts) {
		if (p === "choice") {
			return [cur.choice, undefined];
		}
		const key = underscore(p);
		const cd = findChoiceDef(d, cur.choice);
		d = findFieldDef(cd, key);
		cur = cur[key];
	}
	return [cur, d];
}

/**
 * The `{<prefix><name>}` references a template makes, in first-appearance
 * order. A dotted name is a scoped reference and belongs to the record walk,
 * not to the context's store.
 */
function templateRefs(template, prefix) {
	const out = [];
	const needle = `{${prefix}`;
	let rest = template;
	for (;;) {
		const i = rest.indexOf(needle);
		if (i < 0) {
			return out;
		}
		rest = rest.slice(i + needle.length);
		const j = rest.indexOf("}");
		if (j < 0) {
			return out;
		}
		const name = rest.slice(0, j);
		if (!name.includes(".")) {
			out.push(name);
		}
		rest = rest.slice(j);
	}
}

/** The dotted references a template makes into one selector's record. */
function scopedRefs(template, prefix, sel) {
	const out = [];
	const needle = `{${prefix}${sel}.`;
	let rest = template;
	for (;;) {
		const i = rest.indexOf(needle);
		if (i < 0) {
			return out;
		}
		rest = rest.slice(i + needle.length);
		const j = rest.indexOf("}");
		if (j < 0) {
			return out;
		}
		out.push(rest.slice(0, j));
		rest = rest.slice(j);
	}
}

// ---------------------------------------------------------------------------
// Flag construction
// ---------------------------------------------------------------------------
function buildFlag(fd) {
	if (isSelector(fd)) {
		return buildSelectorFlag(fd);
	}
	const name = fd.name;
	const ftype = fd.type ?? "str";
	const isList = ftype.startsWith("list[");
	const isDict = ftype.startsWith("dict[");
	const repeatable = fd.repeatable === true;
	const elemType = elemTypeOf(ftype);

	// Scalar repeatable-without-unique is inexpressible through the TS
	// factory API (the list carrier that a repeatable scalar maps to defaults
	// unique like Python's list[T] does), so the framework's guard is
	// replayed here with its own catalog message. bool + repeatable is
	// excluded: the framework's bool-incompatibility error fires first,
	// matching the sibling validation order.
	if (
		repeatable &&
		!isList &&
		!isDict &&
		ftype !== "bool" &&
		!("unique" in fd)
	) {
		throw new Error(errFlagRepeatableRequiresExplicitUnique(name));
	}

	// Carrier: list/dict types map directly; a repeatable scalar becomes a
	// list carrier (in TS, list carriers ARE the repeatable flags -- scalar
	// repeatable does not exist). bool + repeatable keeps the scalar carrier
	// so the framework mints the repeatable-incompatible-with-bool error.
	let carrier;
	if (isList || (repeatable && ftype !== "bool")) {
		carrier = t.list(scalarCarrier(elemType));
	} else if (isDict) {
		carrier = t.dict(scalarCarrier(elemType));
	} else {
		carrier = scalarCarrier(ftype);
	}

	const opts = { help: fd.help };
	if ("short" in fd) {
		opts.short = fd.short;
	}
	// presence is a mandatory case-schema key (effects contract §23.1); a case
	// that omits it is meant to reach the framework's undeclared-presence
	// error, so it is passed through exactly as written.
	if ("presence" in fd) {
		opts.presence = fd.presence;
	}
	if ("default_relative_to_root" in fd) {
		const rtr = fd.default_relative_to_root;
		opts.default = relativeToRoot(rtr.env_var, ...(rtr.parts ?? []));
	}
	if ("default" in fd) {
		const dv = fd.default;
		if (dv === null) {
			// Still expressible on the case side, as the input the null-default
			// redirect error (§12.12) is asserted against.
			opts.default = null;
		} else if (Array.isArray(dv)) {
			opts.default = dv.map((el) => convertScalar(elemType, el));
		} else if (isDict && typeof dv === "object") {
			// Dict default (contract §23.5's dict row): the TS dict carrier is
			// Map-backed, so the case's JSON object becomes a Map here.
			opts.default = new Map(
				Object.entries(dv).map(([k, val]) => [k, convertScalar(elemType, val)]),
			);
		} else {
			opts.default = convertScalar(ftype, dv);
		}
	}
	if ("env" in fd) {
		opts.env = fd.env;
	}
	if ("prefixed" in fd) {
		opts.prefixed = fd.prefixed;
	}
	const flagChoices = choiceRecords(fd);
	if (flagChoices !== undefined) {
		opts.choices = flagChoices;
	}
	const flagRetired = retiredChoiceRecords(fd);
	if (flagRetired !== undefined) {
		opts.retiredChoices = flagRetired;
	}
	if (repeatable) {
		opts.repeatable = true;
	}
	if ("unique" in fd) {
		opts.unique = fd.unique;
	}
	if ("conflict_mode" in fd) {
		opts.conflictMode = fd.conflict_mode;
	}
	if ("env_separator" in fd) {
		opts.envSeparator = fd.env_separator;
	}
	if ("negatable" in fd && fd.negatable === false) {
		opts.negatable = false;
	}
	// The clear vocabulary's declaration (contract §27.6): legal only on a
	// property of an update, and what mints `--unset-<prop>`.
	if (fd.nullable === true) {
		opts.nullable = true;
	}
	// The corpus's one expressible validator shape (case-schema `validate`):
	// a callable refusing named values with a fixed message. Comparison goes
	// through each language's own default formatting of the value, so one
	// reject list means the same set in all three.
	if ("validate" in fd) {
		const rejects = new Set(fd.validate.rejects.map((r) => String(r)));
		const message = fd.validate.message;
		opts.validate = (v) => {
			if (rejects.has(String(v))) {
				throw new Error(message);
			}
		};
	}
	return flag(name, carrier, opts);
}

/**
 * Builds a FlagMap keyed by the underscore form of each flag name. A JS
 * object would silently collapse a same-name duplicate, so the framework's
 * duplicate check is replayed here first with the framework's own catalog
 * message (dupMessage receives the colliding flag name).
 */
function flagMapOf(flagDefs, dupMessage) {
	const fm = {};
	for (const fd of flagDefs) {
		const key = underscore(fd.name);
		if (key in fm) {
			throw new Error(dupMessage(fd.name));
		}
		fm[key] = buildFlag(fd);
	}
	return fm;
}

// ---------------------------------------------------------------------------
// Arg construction
// ---------------------------------------------------------------------------
function buildArg(ad) {
	const atype = ad.type ?? "str";
	const opts = { help: ad.help };
	// The arg surface takes the same three-way declaration; `required` is
	// deleted from the case schema rather than retained beside it (§23.3).
	if ("presence" in ad) {
		opts.presence = ad.presence;
	}
	if ("default" in ad) {
		opts.default = ad.default === null ? null : convertScalar(atype, ad.default);
	}
	if (ad.variadic === true) {
		opts.variadic = true;
	}
	const argChoices = choiceRecords(ad);
	if (argChoices !== undefined) {
		opts.choices = argChoices;
	}
	const argRetired = retiredChoiceRecords(ad);
	if (argRetired !== undefined) {
		opts.retiredChoices = argRetired;
	}
	return arg(ad.name, argCarrier(atype), opts);
}

// ---------------------------------------------------------------------------
// Constraints (contract §26)
// ---------------------------------------------------------------------------

/**
 * Members are passed through as the plain `{name, when?}` records the TS
 * declaration surface takes. A case may declare fewer than two of them: the
 * two-member floor is a COMPILE-time rule here (`readonly [M, M, ...M[]]`),
 * and this harness is plain JS, so it is exactly the widened caller
 * errConstraintMinMembers stays reachable through (§26.6).
 */
function buildConstraintMembers(cd) {
	return cd.members.map((m) =>
		typeof m === "string"
			? m
			: "when" in m
				? { name: m.name, when: m.when }
				: { name: m.name },
	);
}

function buildConstraint(cd) {
	switch (cd.type) {
		case "at_least_one":
			return atLeastOne({ name: cd.name, members: buildConstraintMembers(cd) });
		case "all_or_none":
			return allOrNone({ name: cd.name, members: buildConstraintMembers(cd) });
		case "requires":
			return requires({
				name: cd.name,
				flag: cd.flag,
				dependsOn: cd.depends_on,
			});
		case "implies":
			return implies({
				name: cd.name,
				flag: cd.flag,
				implies: cd.implies,
				value: cd.value,
			});
		default:
			throw new Error(`unknown constraint type: ${cd.type}`);
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

/** All flag defs visible to a command's handler: global + direct + flag sets. */
function collectAllFlagDefs(cmdDef, globalFlags) {
	const all = [...globalFlags];
	all.push(...(cmdDef.flags ?? []));
	for (const fs of cmdDef.flag_sets ?? []) {
		all.push(...fs.flags);
	}
	return all;
}

// ---------------------------------------------------------------------------
// The effects vocabulary (effects contract §14.4)
//
// `handler_effects` is materialized identically by all three harnesses: iterate
// the array in order, call the named method with EXACTLY the keys the entry
// declares (no per-method filtering -- a case declaring a key the method does
// not accept is asserting the error), and keep the returned carrier in a
// per-run map indexed by position so `forward_from` / `extract_from` can
// reference it.
// ---------------------------------------------------------------------------

/** The option keys the vocabulary carries, in the harnesses' shared order. */
function effectOptions(e) {
	const opts = {};
	if ("stream" in e) {
		opts.stream = e.stream;
	}
	if ("resource" in e) {
		opts.resource = e.resource;
	}
	if ("skip_if_current" in e) {
		opts.skipIfCurrent = e.skip_if_current;
	}
	if ("grant" in e) {
		opts.grant = e.grant;
	}
	return opts;
}

/**
 * Reads a concrete value out of a carrier -- the illegal use that trips the
 * runtime seal and truncates the preview (§4.4). `String(...)` reaches the
 * Proxy's get trap through Symbol.toPrimitive / toString, neither of which is
 * exempt; a bare truthiness test would not, and is the accepted TS ceiling
 * (§17) that lint, not the seal, is responsible for.
 */
function extractCarrier(c) {
	return String(c);
}

/**
 * Issues the declared Context diagnostic calls, in order. The four levels are
 * gated by --quiet / --verbose (effects contract §7.4); the harness does no
 * gating of its own, it just calls the named method.
 */
/**
 * The claimed-rendering calls (effects contract §19.7). They run AFTER
 * handler_effects and BEFORE handler_diagnostics / handler_prints, so a
 * rendered log lands ahead of the handler's own output -- which is exactly the
 * ordering the feature exists to make possible.
 */
function runHandlerClaim(ctx, cmdDef) {
	if (cmdDef.handler_claims_log === true) {
		ctx.effects.recorded();
	}
	if (cmdDef.handler_payloads_recorded === true) {
		ctx.payload(ctx.effects.recorded().map((rec) => rec.verb));
	}
	if (cmdDef.handler_renders_log === true) {
		ctx.effects.renderLog();
	}
}

function runHandlerDiagnostics(ctx, entries) {
	for (const d of entries) {
		switch (d.level) {
			case "debug":
				ctx.debug(d.message);
				break;
			case "info":
				ctx.info(d.message);
				break;
			case "warn":
				ctx.warn(d.message);
				break;
			case "error":
				ctx.error(d.message);
				break;
			default:
				throw new Error(`unknown handler_diagnostics level: ${d.level}`);
		}
	}
}

function runHandlerEffects(ctx, entries) {
	const eff = {};
	for (let i = 0; i < entries.length; i++) {
		const e = entries[i];
		const method = e.method;

		if ("extract_from" in e) {
			// Terminal by construction: the extraction truncates the run.
			extractCarrier(eff[e.extract_from]);
			return;
		}

		const hasFwd = "forward_from" in e;
		const fwd = hasFwd ? eff[e.forward_from] : undefined;
		const opts = effectOptions(e);

		let carrier;
		switch (method) {
			case "run":
			case "spawn": {
				const argv = [...(e.argv ?? [])];
				if (hasFwd) {
					argv.push(fwd);
				}
				carrier =
					method === "run"
						? ctx.effects.run(argv, opts)
						: ctx.effects.spawn(argv, opts);
				break;
			}
			case "write":
				carrier = ctx.effects.write(
					e.path,
					hasFwd ? fwd : e.content,
					opts,
				);
				break;
			case "mkdir":
				carrier = ctx.effects.mkdir(hasFwd ? fwd : e.path, opts);
				break;
			case "remove":
				carrier = ctx.effects.remove(hasFwd ? fwd : e.path, opts);
				break;
			case "rename":
				carrier = ctx.effects.rename(e.path, hasFwd ? fwd : e.to, opts);
				break;
			case "chmod":
				carrier = ctx.effects.chmod(
					hasFwd ? fwd : e.path,
					Number.parseInt(e.mode, 8),
					opts,
				);
				break;
			case "http":
				carrier = ctx.effects.http(
					e.http_method,
					hasFwd ? fwd : e.url,
					opts,
				);
				break;
			default:
				throw new Error(`unknown handler_effects method: ${method}`);
		}
		eff[i + 1] = carrier;
	}
}

function makeHandler(cmdDef, globalFlags) {
	// handler_effects runs BEFORE the handler_prints / handler_returns path and
	// does not replace it (§14.4). handler_diagnostics follows it, still before
	// that path.
	const handlerEffects = cmdDef.handler_effects ?? [];
	const handlerDiagnostics = cmdDef.handler_diagnostics ?? [];

	// handler_aborts: the handler unwinds instead of returning, after its
	// effects and diagnostics have run. The sibling harnesses raise/panic with
	// the identical message and surface it identically, which is what makes an
	// aborting case comparable across all three targets.
	if (cmdDef.handler_aborts === true) {
		return (_args, ctx) => {
			runHandlerEffects(ctx, handlerEffects);
			runHandlerClaim(ctx, cmdDef);
			runHandlerDiagnostics(ctx, handlerDiagnostics);
			throw new Error(HANDLER_ABORT_MESSAGE);
		};
	}

	// handler_returns pins an explicit return (survivor-contract cases): the
	// template-printing path is skipped entirely. Kinds mirror ref_python's
	// _emit_handler_return; "bad" returns a non-outcome to trigger the
	// framework's hard error (expressible in TS, unlike Go).
	if ("handler_returns" in cmdDef) {
		const hr = cmdDef.handler_returns;
		const code = hr.code ?? 0;
		// An unrecognized kind is a HARD ERROR at registration, never a silent
		// mapping to something else. Quietly returning a non-outcome for every
		// unknown kind would turn a case this harness cannot express into a
		// false pass, which is the defect the Go harness had removed.
		if (!["exit", "data", "exit_data", "none", "bad"].includes(hr.kind)) {
			throw new Error(
				`conformance harness: handler_returns kind ${JSON.stringify(hr.kind)} ` +
					"is unknown to the TypeScript harness; teach the harness the kind " +
					"or the case must restrict its targets",
			);
		}
		return (_args, ctx) => {
			runHandlerEffects(ctx, handlerEffects);
			runHandlerClaim(ctx, cmdDef);
			runHandlerDiagnostics(ctx, handlerDiagnostics);
			switch (hr.kind) {
				case "data":
					ctx.payload(hr.data);
					return outcome(0);
				case "exit_data":
					ctx.payload(hr.data);
					return outcome(code);
				case "exit":
					return code;
				case "none":
					return undefined;
				default:
					// "bad": a return that is not a number, undefined, or an
					// outcome -- the framework's hard error.
					return ["not-an-outcome"];
			}
		};
	}

	const template = cmdDef.handler_prints;
	const exitCode = cmdDef.handler_exit_code ?? 0;
	const allFlags = collectAllFlagDefs(cmdDef, globalFlags);
	const argDefs = cmdDef.args ?? [];

	return (args, ctx) => {
		runHandlerEffects(ctx, handlerEffects);
		runHandlerClaim(ctx, cmdDef);
		runHandlerDiagnostics(ctx, handlerDiagnostics);
		// A handler_effects-only command declares no template and prints
		// nothing; the effect calls above are its whole body.
		if (template === undefined) {
			return exitCode;
		}
		let out = template;

		// Delivered records first: `{via.subject}` reaches into the record, and
		// `{via}` renders the whole of it (§24.1's one-key-per-selector rule).
		for (const fd of allFlags) {
			if (!isSelector(fd)) {
				continue;
			}
			const rec = args[underscore(fd.name)];
			for (const ref of scopedRefs(out, "provided:", fd.name)) {
				const parts = ref.split(".");
				const [parent] = selWalk(rec, fd, parts.slice(0, -1));
				out = subst(
					out,
					`{provided:${fd.name}.${ref}}`,
					provided(parent, parts[parts.length - 1]) ? "true" : "false",
				);
			}
			for (const ref of scopedRefs(out, "", fd.name)) {
				const [v, d] = selWalk(rec, fd, ref.split("."));
				out = subst(out, `{${fd.name}.${ref}}`, renderScoped(v, d));
			}
			out = subst(out, `{${fd.name}}`, renderElected(rec, fd));
		}

		// {source:name} and {provided:name} resolve through the context's
		// per-parse store. The names come from the TEMPLATE rather than from
		// the declaration list, so a case can ask the store about a name it
		// does not hold -- which is how §24.9's "a scoped flag is not in the
		// store at all" is asserted rather than assumed.
		for (const name of templateRefs(out, "source:")) {
			out = subst(out, `{source:${name}}`, ctx.source(name));
		}
		for (const name of templateRefs(out, "provided:")) {
			out = subst(out, `{provided:${name}}`, ctx.provided(name) ? "true" : "false");
		}
		// {unset:name} is the clear vocabulary's own accessor (contract §27.6).
		// The minted --unset-<prop> delivers no key of its own, so this is the
		// only way a handler learns that a property was CLEARED rather than
		// left untouched: both deliver absence, and provided() is true for the
		// clear alone.
		for (const name of templateRefs(out, "unset:")) {
			out = subst(out, `{unset:${name}}`, ctx.unset(name) ? "true" : "false");
		}

		// Flags: values arrive under the underscore key (globals included).
		for (const fd of allFlags) {
			if (isSelector(fd)) {
				continue;
			}
			out = subst(out, `{${fd.name}}`, render(args[underscore(fd.name)]));
		}

		// Args: keyed by name as-is.
		for (const ad of argDefs) {
			out = subst(out, `{${ad.name}}`, render(args[ad.name]));
		}

		// console.log, NOT ctx.info: `handler_prints` means the handler writes
		// to stdout unconditionally, which is what Python's `print` and Go's
		// fmt.Println do. ctx.info is gated by --quiet (effects contract §7.4),
		// so routing through it would make every handler_prints case diverge
		// under --quiet in TypeScript alone.
		console.log(out);
		return exitCode;
	};
}

function makePassthroughHandler(cmdDef, globalFlags) {
	const exitCode = cmdDef.handler_exit_code ?? 0;
	// handler_aborts: the passthrough handler unwinds instead of printing and
	// returning, exactly as the normal-command form does.
	if (cmdDef.handler_aborts === true) {
		return () => {
			throw new Error(HANDLER_ABORT_MESSAGE);
		};
	}
	return (pt, ctx) => {
		// Print global flag values (name=value lines) first. console.log, not
		// ctx.info -- see makeHandler above.
		for (const gf of globalFlags) {
			console.log(`${gf.name}=${render(pt.globals[underscore(gf.name)])}`);
		}
		// Then the passthrough_handler_prints template, or the default format.
		const template = cmdDef.passthrough_handler_prints;
		if (template !== undefined) {
			let out = subst(template, "{name}", pt.name);
			out = subst(out, "{args}", pt.args.join(","));
			console.log(out);
		} else {
			console.log(`${pt.name}:${pt.args.join(",")}`);
		}
		return exitCode;
	};
}

// ---------------------------------------------------------------------------
// Command / group registration
// ---------------------------------------------------------------------------
function registerCommand(cmdDef, target, globalFlags) {
	const name = cmdDef.name;

	// Deprecated command. Deprecated entries are classification-EXEMPT
	// (effects contract §1.1), so `effect` is spliced onto the DeprecatedDef
	// ONLY when the case declares it -- which is a case asserting
	// errDeprecatedCommandEffect. The `deprecated()` factory never mints the
	// field, so the splice is the only way to reach that guard.
	if (cmdDef.deprecated === true) {
		const def = deprecated(name, cmdDef.deprecated_message ?? "");
		target.deprecate(
			"effect" in cmdDef ? { ...def, effect: cmdDef.effect } : def,
		);
		return;
	}

	// Passthrough command. The TS factory API makes passthrough-with-parsing
	// declarations inexpressible (passthrough() takes no flags/args/flag
	// sets), so the framework's registration guard is replayed here
	// with its own catalog message, in the sibling part order.
	if (cmdDef.passthrough === true) {
		const parts = [];
		if ((cmdDef.flags ?? []).length > 0) {
			parts.push("flags");
		}
		if ((cmdDef.args ?? []).length > 0) {
			parts.push("args");
		}
		if ((cmdDef.flag_sets ?? []).length > 0) {
			parts.push("flag sets");
		}
		if (parts.length > 0) {
			throw new Error(errCommandPassthroughCannotHave(name, parts.join(", ")));
		}
		const spec = {
			help: cmdDef.help,
			handler: makePassthroughHandler(cmdDef, globalFlags),
		};
		if ("tags" in cmdDef) {
			spec.tags = cmdDef.tags;
		}
		if (cmdDef.hidden === true) {
			spec.hidden = true;
		}
		if ("grants" in cmdDef) {
			spec.grants = cmdDef.grants;
		}
		// `consequential` is NOT mandatory (§8.1): absence means "not
		// consequential", so it is spliced only when the case declares it.
		if (cmdDef.consequential === true) {
			spec.consequential = true;
		}
		spliceUpdateOf(spec, cmdDef);
		spliceDryRun(spec, cmdDef);
		// Classification is spliced into the factory name (§1.2). The twins are
		// the sole mint, so a missing or invalid classification is unreachable
		// through them -- spliceEffect puts the case's declared (or absent)
		// value back onto the minted carrier, exactly as the deprecated branch
		// above does, so app.command()'s own guard is what rejects it.
		target.command(
			spliceEffect(
				factoryClassification(cmdDef) === "mutating"
					? mutatingPassthrough(name, spec)
					: readOnlyPassthrough(name, spec),
				cmdDef,
			),
		);
		return;
	}

	// Normal command.
	const spec = {
		help: cmdDef.help,
		handler: makeHandler(cmdDef, globalFlags),
	};
	if ("args" in cmdDef) {
		spec.args = cmdDef.args.map(buildArg);
	}
	if ("flags" in cmdDef) {
		spec.flags = flagMapOf(cmdDef.flags, (fn) =>
			errCommandDuplicateFlag(name, fn),
		);
	}
	if ("flag_sets" in cmdDef) {
		spec.flagSets = cmdDef.flag_sets.map((fs) =>
			flagSet(
				fs.name,
				flagMapOf(fs.flags, (fn) => errCommandDuplicateFlag(name, fn)),
			),
		);
	}
	if ("constraints" in cmdDef) {
		spec.constraints = cmdDef.constraints.map(buildConstraint);
	}
	if ("tags" in cmdDef) {
		spec.tags = cmdDef.tags;
	}
	if ("config_fields" in cmdDef) {
		spec.configFields = cmdDef.config_fields;
	}
	if (cmdDef.hidden === true) {
		spec.hidden = true;
	}
	if (cmdDef.interactive === true) {
		spec.interactive = true;
	}
	if ("grants" in cmdDef) {
		spec.grants = cmdDef.grants;
	}
	if (cmdDef.consequential === true) {
		spec.consequential = true;
	}
	spliceUpdateOf(spec, cmdDef);
	spliceDryRun(spec, cmdDef);
	if ("forwarding" in cmdDef) {
		spec.forwarding = cmdDef.forwarding;
	}
	target.command(
		spliceEffect(
			factoryClassification(cmdDef) === "mutating"
				? defineMutatingCommand(name, spec)
				: defineReadOnlyCommand(name, spec),
			cmdDef,
		),
	);
}

/**
 * Splices the update declaration onto a command spec (contract §27). The case
 * schema follows the DECLARATION side -- ONE record carrying its write mode
 * (§13's amendment) -- which is TypeScript's own shape, so the only conversion
 * is the key spelling. An absent write_mode reaches the option object as
 * `undefined` and is the covering input for errUpdateWriteModeInvalid, which
 * renders it as the empty string exactly as its siblings do; an absent
 * properties list is the covering input for errUpdatePropertiesEmpty, the
 * declared tuple type putting a compile-time floor of one on every ordinary
 * caller. A PASSTHROUGH carries the declaration rather than dropping it, as
 * its siblings do.
 */
function spliceUpdateOf(spec, cmdDef) {
	if (!("update_of" in cmdDef)) {
		return;
	}
	const ud = cmdDef.update_of;
	spec.updateOf = {
		resource: ud.resource,
		writeMode: ud.write_mode,
		identity: ud.identity ?? [],
		properties: ud.properties ?? [],
	};
}

/**
 * Splices the dry-run declaration onto a command spec. `dry_run_supported` is
 * NOT mandatory: absence means supported, only false is declarable, and it
 * carries a mandatory reason. Both keys are spliced independently so a case can
 * assert the orphan-reason registration error too.
 */
function spliceDryRun(spec, cmdDef) {
	if ("dry_run_supported" in cmdDef) {
		spec.dryRunSupported = cmdDef.dry_run_supported;
	}
	if ("dry_run_unsupported_reason" in cmdDef) {
		spec.dryRunUnsupportedReason = cmdDef.dry_run_unsupported_reason;
	}
	splicePayloadSchema(spec, cmdDef);
	spliceOwnsStdout(spec, cmdDef);
}

/**
 * Declares the machine payload's schema (§19.5) for the commands that supply
 * one. A handler_returns of kind "data"/"exit_data" calls ctx.payload, which
 * refuses to run on a command that declares no schema -- so the harness
 * declares the permissive literal for exactly those commands. The literal is
 * identical in all three harnesses, which is what keeps the schema dump in
 * parity.
 */
function splicePayloadSchema(spec, cmdDef) {
	// A case may declare its own literal with "payload_schema", which is how
	// the closed subset's enforcement becomes observable through the CLI: the
	// schema dump publishes it verbatim and a payload is validated against it.
	if (cmdDef.payload_schema !== undefined) {
		spec.payloadSchema = cmdDef.payload_schema;
		return;
	}
	const kind = cmdDef.handler_returns?.kind;
	if (kind === "data" || kind === "exit_data") {
		spec.payloadSchema = {};
		return;
	}
	if (cmdDef.handler_payloads_recorded === true) {
		spec.payloadSchema = {};
	}
}

/**
 * Stdout ownership (§19.6): the command's own document keeps stdout and the
 * envelope moves to stderr in machine mode.
 */
function spliceOwnsStdout(spec, cmdDef) {
	if (cmdDef.owns_stdout === true) {
		spec.ownsStdout = true;
	}
}

/**
 * Which twin factory mints the carrier. TS bakes the classification into the
 * factory name, so a case declaring a missing or invalid `effect` has no twin
 * to call: `mutating` is used as the carrier-building placeholder (it carries
 * the fewest classification-specific factory guards, so the effect guard in
 * app.command() is what such a case reaches), and spliceEffect then replaces
 * the placeholder with what the case actually declared.
 */
function factoryClassification(cmdDef) {
	return cmdDef.effect === "read_only" ? "read_only" : "mutating";
}

/**
 * Puts the case's declared classification back onto a twin-minted carrier.
 * Absent `effect` => the key is removed; an invalid string => spliced verbatim.
 * Same technique as the deprecated branch's `effect` splice: the factories are
 * the sole mint and cannot express these states, but app.command() re-validates
 * classification precisely so hand-forged carriers cannot bypass it, and that
 * guard is what these cases assert.
 */
function spliceEffect(def, cmdDef) {
	if (!("effect" in cmdDef)) {
		const { effect: _dropped, ...rest } = def;
		return rest;
	}
	if (cmdDef.effect !== "read_only" && cmdDef.effect !== "mutating") {
		return { ...def, effect: cmdDef.effect };
	}
	return def;
}

function buildGroup(groupDef, parent, globalFlags) {
	const spec = { help: groupDef.help };
	if ("tags" in groupDef) {
		spec.tags = groupDef.tags;
	}
	if (groupDef.hidden === true) {
		spec.hidden = true;
	}
	const group = parent.group(groupDef.name, spec);
	for (const c of groupDef.commands ?? []) {
		registerCommand(c, group, globalFlags);
	}
	for (const g of groupDef.groups ?? []) {
		buildGroup(g, group, globalFlags);
	}
}

// ---------------------------------------------------------------------------
// Checks
// ---------------------------------------------------------------------------

// The message an `aborts` check impl carries. The sibling harnesses raise and
// panic the identical text carried by a type spelled the same, so the
// framework's containment line (which names the type) is byte-identical.
const CHECK_ABORT_MESSAGE = "conformance: check aborted";

/**
 * The thrown value an `aborts` check impl carries. Its NAME is part of the
 * contract: Python raises a CheckAborted exception and Go panics with a
 * CheckAborted value, and the framework prints the constructor name.
 */
class CheckAborted extends Error {}

/**
 * Replays the case's notes and problems onto the reporter and mints the
 * requested terminal outcome. A warn-form reporter replays every problem as
 * a warn (it structurally lacks error-minting), mirroring mintWarnOutcome.
 * An `aborts` impl mints nothing and throws instead, which is how a case
 * reaches the runner's per-check containment.
 */
function mintOutcome(reporter, warnForm, cd) {
	if (cd.aborts === true) {
		throw new CheckAborted(CHECK_ABORT_MESSAGE);
	}
	for (const n of cd.notes ?? []) {
		reporter.note(n);
	}
	for (const p of cd.problems ?? []) {
		if (!warnForm && p.severity === "error") {
			reporter.error(p.text);
		} else {
			reporter.warn(p.text);
		}
	}
	switch (cd.mint) {
		case "passed":
			return reporter.passed(cd.message);
		case "skipped":
			return reporter.skipped(cd.message);
		default:
			return reporter.found(cd.message);
	}
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
async function main() {
	const defPath = process.env.CONFORMANCE_APP_DEF;
	if (!defPath) {
		process.stderr.write("CONFORMANCE_APP_DEF environment variable not set\n");
		process.exit(2);
	}

	let raw;
	try {
		raw = readFileSync(defPath, "utf8");
	} catch (e) {
		process.stderr.write(`failed to read app def: ${e.message}\n`);
		process.exit(2);
	}

	let appDef;
	try {
		appDef = JSON.parse(raw);
	} catch (e) {
		process.stderr.write(`failed to parse app def: ${e.message}\n`);
		process.exit(2);
	}

	// Build app spec.
	const spec = {
		name: appDef.name,
		version: appDef.version,
		help: appDef.help,
	};
	if ("env_prefix" in appDef) {
		spec.envPrefix = appDef.env_prefix;
	}
	if (appDef.config === true) {
		spec.config = true;
	}
	if ("config_path" in appDef && appDef.config_path !== null) {
		spec.configPath = appDef.config_path;
	}
	// The config path declared as a marker relative to an infra root: the same
	// declaration the flag side spells with default_relative_to_root, resolved
	// eagerly at construction.
	if ("config_path_relative_to_root" in appDef) {
		const cprtr = appDef.config_path_relative_to_root;
		spec.configPath = relativeToRoot(cprtr.env_var, ...(cprtr.parts ?? []));
	}
	if ("schema_path" in appDef && appDef.schema_path !== null) {
		spec.schemaPath = appDef.schema_path;
	}
	if ("config_format" in appDef && appDef.config_format !== "json") {
		spec.configFormat = appDef.config_format;
	}
	if (
		"config_conflict_mode" in appDef &&
		appDef.config_conflict_mode !== "cli-wins"
	) {
		spec.configConflictMode = appDef.config_conflict_mode;
	}
	if (appDef.no_default_config_path === true) {
		spec.noDefaultConfigPath = true;
	}
	if ("infra_root" in appDef) {
		spec.infraRoot = appDef.infra_root;
	}
	if ("handshake_env" in appDef) {
		spec.handshakeEnv = appDef.handshake_env;
	}
	if ("checks_toml" in appDef) {
		spec.checksEmbed = appDef.checks_toml;
	}
	if ("test_coverage_dir" in appDef) {
		spec.testCoverageDir = appDef.test_coverage_dir;
	}
	if ("proc_observe_allowlist" in appDef) {
		spec.procObserveAllowlist = appDef.proc_observe_allowlist;
	}

	// Global flags go into the createApp spec (TS has no post-construction
	// global-flag registration; the framework replays the same validations).
	const globalFlags = appDef.global_flags ?? [];
	if (globalFlags.length > 0) {
		spec.flags = flagMapOf(globalFlags, (fn) => errDuplicateGlobalFlag(fn));
	}

	const app = createApp(spec);

	// Register config fields (before commands, since commands may bind to them).
	for (const cfDef of appDef.config_fields_def ?? []) {
		const cfType = cfDef.type ?? "str";
		const cfSpec = { type: scalarCarrier(cfType), help: cfDef.help };
		if ("default" in cfDef) {
			cfSpec.default = convertScalar(cfType, cfDef.default);
		}
		app.configField(cfDef.name, cfSpec);
	}

	// Register groups (recursive), then top-level commands (main.go order).
	for (const g of appDef.groups ?? []) {
		buildGroup(g, app, globalFlags);
	}
	for (const c of appDef.commands ?? []) {
		registerCommand(c, app, globalFlags);
	}

	// Register tag contracts.
	for (const [tag, contract] of Object.entries(appDef.tag_contracts ?? {})) {
		app.tagContract(tag, contract.requires_flag);
	}

	// Register checks. The registration FORM (error vs warn) is derived from
	// the check's declared severity in the embedded checks_toml -- read back
	// from the app's parsed defs (createApp already parsed the TOML), with
	// the Go harness's fallback to error-form for undeclared names so the
	// framework's double-entry cross-check surfaces genuine mismatches.
	if ("checks_toml" in appDef) {
		for (const cd of appDef.checks ?? []) {
			const severity = app.checks?.defs?.get(cd.name)?.severity ?? "error";
			if (severity === "warn") {
				app.warnCheck(cd.name, (_ctx, r) => mintOutcome(r, true, cd));
			} else {
				app.errorCheck(cd.name, (_ctx, r) => mintOutcome(r, false, cd));
			}
		}
	}

	// Register check providers. Each provider is a list of specs carrying the
	// 8 meta fields inline; the builder (errorCheckSpec vs warnCheckSpec) is
	// the spec's impl_form (defaults to its meta severity). Specs are built
	// lazily inside the provider, mirroring main.go.
	for (const specDefs of appDef.providers ?? []) {
		app.registerCheckProvider(() =>
			specDefs.map((sd) => {
				const implForm = sd.impl_form ?? sd.severity;
				const init = {
					name: sd.name,
					tags: sd.tags ?? [],
					severity: sd.severity,
					fast: sd.fast,
					pure: sd.pure,
					needsNetwork: sd.needs_network,
					dependsOn: sd.depends_on ?? [],
					scope: sd.scope ?? "",
				};
				if (implForm === "warn") {
					return warnCheckSpec({
						...init,
						impl: (_ctx, r) => mintOutcome(r, true, sd),
					});
				}
				return errorCheckSpec({
					...init,
					impl: (_ctx, r) => mintOutcome(r, false, sd),
				});
			}),
		);
	}

	if (
		"checks_toml" in appDef ||
		"providers" in appDef ||
		"test_coverage_dir" in appDef
	) {
		app.setCheckContext(() => ({ projectRoot: "." }));
	}

	// The confirm protocol's interactive branch is otherwise unreachable from a
	// subprocess: a case's stdin is a pipe, and a pipe is not a TTY in any of
	// the three implementations, so every consequential case would take the
	// non-interactive error branch. The framework's test-only confirm seam says
	// the answer channel IS interactive and leaves the answer itself coming from
	// the case's real stdin -- WHERE the answer comes from, never WHETHER the
	// protocol runs.
	if (appDef.confirm_stdin_interactive === true) {
		setConfirmIO({
			isInteractive: () => true,
			readLine: readLineFromStdin,
		});
	}

	// Write config_content_late AFTER construction but BEFORE run.
	if ("config_content_late" in appDef) {
		const configPath = appDef.config_path ?? "";
		if (configPath !== "") {
			writeFileSync(configPath, appDef.config_content_late);
		}
	}

	// Pre-test argv lists: run app.test() for each before the main app.run().
	for (const argv of appDef.pre_test ?? []) {
		await app.test(argv);
	}

	// Tool descriptor dump: the exported classification, one line per tool.
	if (appDef.dump_tools === true) {
		for (const tl of app.asTools()) {
			process.stdout.write(
				`tool: ${tl.name} effect=${tl.effect} consequential=${tl.consequential}\n`,
			);
		}
	}

	// Programmatic calls: the app.call() channel, which argv cannot reach.
	for (const spec of appDef.pre_call ?? []) {
		try {
			await app.call(spec.command, undefinedSentinels(spec.kwargs ?? {}), {
				approveConsequential: spec.approve_consequential === true,
			});
			process.stdout.write(`call ok: ${spec.command}\n`);
		} catch (e) {
			const msg = e instanceof Error ? e.message : String(e);
			process.stderr.write(`call error: ${msg}\n`);
		}
	}

	// The structured effect-log side channel (§14.3): the same env-var file
	// handoff as CONFORMANCE_APP_DEF. app.run() ends in process.exit, so the
	// write rides process.on("exit") -- the TS counterpart of the Python ref's
	// atexit and the Go harness's SetExitHook.
	const effectLogPath = process.env.CONFORMANCE_EFFECT_LOG;
	if (effectLogPath !== undefined && effectLogPath !== "") {
		process.on("exit", () => {
			writeFileSync(effectLogPath, stableJson(app.effectLog()));
		});
	}

	await app.run();
}

/**
 * Rewrites the corpus's `"$undefined"` sentinel to a real `undefined` VALUE at
 * a present key, at every depth of a pre_call kwargs object.
 *
 * JSON has no `undefined`, and TypeScript is the only implementation that has
 * the value at all -- so this is the corpus's only way to spell the state
 * §27.6 rules on: `undefined` is NOT a second spelling of the clear. Absence
 * has its own spelling at this door (an absent key), and a value the
 * declaration names is not absence, so `{ttl: undefined}` earns the ordinary
 * value refusal where `{ttl: null}` clears. The sentinel reaches no sibling
 * harness, which is why the case that uses it declares targets: ["typescript"].
 */
function undefinedSentinels(value) {
	if (value === "$undefined") {
		return undefined;
	}
	if (Array.isArray(value)) {
		return value.map(undefinedSentinels);
	}
	if (value !== null && typeof value === "object") {
		const out = {};
		for (const [k, v] of Object.entries(value)) {
			out[k] = undefinedSentinels(v);
		}
		return out;
	}
	return value;
}

/** Compact JSON with sorted object keys, matching the sibling harnesses. */
function stableJson(value) {
	return JSON.stringify(value, (_k, v) => {
		if (v === null || typeof v !== "object" || Array.isArray(v)) {
			return v;
		}
		return Object.fromEntries(
			Object.keys(v)
				.sort()
				.map((k) => [k, v[k]]),
		);
	});
}

main().catch((e) => {
	process.stderr.write(`error: ${e instanceof Error ? e.message : e}\n`);
	process.exit(1);
});
