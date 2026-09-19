/**
 * The built-in `effects-bypass` check provider.
 *
 * It statically analyses the consumer's own sources and fails on any direct
 * process, filesystem-mutation or network call REACHABLE FROM A REGISTERED
 * COMMAND HANDLER (§11).
 *
 * Additionally -- and this part is TypeScript-specific -- it flags the two
 * accepted Proxy ceilings: a bare truthiness test and an identity comparison
 * against a value the analyser can trace to an effects-handle return. Those two
 * are the only things the runtime seal cannot catch, so lint is the sole line
 * of defence and the check names them explicitly.
 *
 * ANALYSER: the TypeScript compiler API, a REGULAR dependency -- `typescript`
 * sits in `dependencies`, not `devDependencies`, and there is no optional
 * import and no soft degradation. Concretely it is the compiler's own scanner
 * (`createScanner` from `typescript/unstable/ast`). The compiler package at
 * version 7 is the native port: it ships the scanner, the SyntaxKind enum and
 * the AST node predicates, but NO in-process parser -- building a syntax tree
 * there means spawning the native language server against a resolved tsconfig,
 * which a check declared `fast` and `pure` must not do.
 *
 * WHAT THE SCANNER DELIVERS, and what it cannot. Brace depth gives real
 * containment, and token adjacency gives a serviceable function table
 * (`function f(...) {}`, `const f = (...) => {}`) plus handler roots (the
 * `handler:` property of a factory options object, inline or naming a declared
 * function). On top of those the check builds an intra-FILE call graph and
 * follows it transitively from every root, which is what closes the two shapes
 * that escape the narrower "a block that mentions `.effects`" reading: a
 * handler that never mentions the handle, and a bypass one helper-call away.
 *
 * The residual gap is recorded as an accepted ceiling in §17 rather than
 * papered over: without a parser there is no import resolution (a helper in
 * ANOTHER FILE is not followed), no scope or shadowing resolution (a name is
 * matched as a name), no method-call resolution (`this.helper()` and
 * `obj.helper()` are not followed), and handler roots are recognized only
 * through the literal `handler:` spelling or an `.effects` mention. Python and
 * Go, which have real in-process parsers, deliver intra-module reachability;
 * TypeScript delivers intra-file.
 */

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
	computeLineStarts,
	createScanner,
	SyntaxKind,
} from "typescript/unstable/ast";
import type { AppImpl, GroupImpl, RegisteredCommand } from "../app.js";
import type { Grant } from "../effects.js";
import { errEffectsBypassNotAWorkTree } from "../errors.js";
import { type CheckSpec, errorCheckSpec, warnCheckSpec } from "./provider.js";

/** Process starts. Matched on the called name, bare or through a receiver. */
const BYPASS_PROCESS: ReadonlySet<string> = new Set([
	"spawn",
	"spawnSync",
	"exec",
	"execSync",
	"execFile",
	"execFileSync",
	"fork",
]);

/**
 * Process leaves that are a process start only bare or through a
 * child_process receiver. `exec` is the whole set: it is child_process'
 * shell-runner AND `RegExp.prototype.exec`, a pure in-process string match
 * that starts nothing. A finding on the latter cannot be acted on -- the
 * check's remediation is "route it through ctx.effects" and the handle's
 * closed method set has no in-process-observe method -- so the leaf is
 * receiver-gated exactly as the network leaves already are. An UNKNOWN
 * receiver is exempt: `re.exec(s)` is not evidence a process starts. A BARE
 * `exec(...)` stays a finding, because this analyser has no import resolution
 * (§17) and an unqualified `exec(` in a reachable scope is the child_process
 * import in every realistic reading.
 */
const BYPASS_PROCESS_RECEIVER_SCOPED: ReadonlySet<string> = new Set(["exec"]);

/** Receivers that make a receiver-scoped process leaf a finding. */
const BYPASS_PROCESS_RECEIVERS: ReadonlySet<string> = new Set([
	"child_process",
	"childProcess",
	"cp",
	"execa",
	"shelljs",
]);

/** Filesystem mutations (sync and promise forms). */
const BYPASS_FILESYSTEM: ReadonlySet<string> = new Set([
	"writeFileSync",
	"appendFileSync",
	"mkdirSync",
	"mkdtempSync",
	"rmSync",
	"rmdirSync",
	"unlinkSync",
	"renameSync",
	"chmodSync",
	"chownSync",
	"symlinkSync",
	"linkSync",
	"truncateSync",
	"copyFileSync",
	"cpSync",
	"createWriteStream",
	"writeFile",
	"appendFile",
	"mkdir",
	"mkdtemp",
	"rm",
	"rmdir",
	"unlink",
	"rename",
	"chmod",
	"chown",
	"symlink",
	"link",
	"truncate",
	"copyFile",
	"cp",
]);

/** Network calls, banned only through the receivers below. */
const BYPASS_NETWORK: ReadonlySet<string> = new Set([
	"request",
	"get",
	"post",
	"put",
	"patch",
	"delete",
	"head",
	"createConnection",
	"connect",
]);

/**
 * Network members are banned only through these receivers, so an ordinary
 * `map.get(...)` inside a handler is not a finding.
 */
const BYPASS_NETWORK_RECEIVERS: ReadonlySet<string> = new Set([
	"http",
	"https",
	"axios",
	"got",
	"undici",
	"net",
	"tls",
	"request",
	"client",
	"session",
	"agent",
]);

/**
 * Directories the analyser never reads. git lists tracked files wherever they
 * are, including a vendored tree, a committed `dist` or `build` directory, and
 * a dot-directory's contents, so the list applies as a filter over git's
 * answer.
 */
const SKIP_DIRS: ReadonlySet<string> = new Set([
	"node_modules",
	"dist",
	"dist-test",
	"build",
	"out",
	"coverage",
	"vendor",
]);

const SOURCE_EXTS = [".ts", ".tsx", ".mts", ".cts", ".js", ".mjs", ".cjs"];

/** One reported bypass. */
export interface BypassFinding {
	readonly file: string;
	readonly line: number;
	readonly text: string;
	readonly kind: "call" | "ceiling";
}

/**
 * Thrown when the project root the check was handed is not inside a git work
 * tree. The check refuses the root rather than reading it another way.
 */
export class BypassRootNotAWorkTree extends Error {}

/** True when a repository-owned path sits under a skipped directory. */
function pathIsSkipped(rel: string): boolean {
	const parts = rel.split("/");
	return parts
		.slice(0, -1)
		.some((part) => SKIP_DIRS.has(part) || part.startsWith("."));
}

/**
 * The repository-owned source files under `root`, relative to it, sorted.
 *
 * The input set is what git reports: tracked files plus untracked files
 * `.gitignore` does not exclude. A release-blocking check reads only inputs the
 * repository owns, so a gitignored scratch file -- present on one machine and
 * absent from a fresh clone -- can never decide the verdict. There is no
 * filesystem-walk fallback: a root outside a work tree is refused.
 */
function repoSourceFiles(root: string): string[] {
	let out: string;
	try {
		out = execFileSync(
			"git",
			["ls-files", "--cached", "--others", "--exclude-standard", "-z"],
			{ cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] },
		);
	} catch {
		throw new BypassRootNotAWorkTree(errEffectsBypassNotAWorkTree(root));
	}
	const rels = new Set<string>();
	for (const rel of out.split("\0")) {
		if (rel === "" || !SOURCE_EXTS.some((ext) => rel.endsWith(ext))) {
			continue;
		}
		if (pathIsSkipped(rel)) {
			continue;
		}
		rels.add(rel);
	}
	return [...rels].sort();
}

interface Tok {
	readonly kind: SyntaxKind;
	readonly text: string;
	readonly start: number;
}

/**
 * Tokenizes with the compiler's scanner, dropping trivia.
 *
 * The scanner is a pure lexer: it cannot leave a template-substitution state on
 * its own, because in a real compile the PARSER decides when a `}` closes a
 * `${` and calls back into `reScanTemplateToken`. Without that cooperation the
 * scanner stalls at the closing brace, returning a zero-width token forever.
 * Progress is therefore asserted explicitly: on a stall, re-scan as a template
 * continuation, and if even that does not advance, step one character and carry
 * on. Both recoveries are lossless for this analyser, which reads names,
 * punctuation and brace depth -- never template contents.
 */
function tokenize(text: string): Tok[] {
	const scanner = createScanner(/* skipTrivia */ true);
	scanner.setText(text);
	const toks: Tok[] = [];
	let lastEnd = -1;
	for (;;) {
		let kind = scanner.scan();
		if (kind === SyntaxKind.EndOfFile) {
			break;
		}
		if (scanner.getTokenEnd() <= lastEnd) {
			kind = scanner.reScanTemplateToken(/* isTaggedTemplate */ false);
			if (scanner.getTokenEnd() <= lastEnd) {
				if (lastEnd + 1 >= text.length) {
					break;
				}
				scanner.resetTokenState(lastEnd + 1);
				lastEnd += 1;
				continue;
			}
		}
		lastEnd = scanner.getTokenEnd();
		toks.push({
			kind,
			text: scanner.getTokenText(),
			start: scanner.getTokenStart(),
		});
	}
	return toks;
}

function lineOf(lineStarts: readonly number[], pos: number): number {
	let lo = 0;
	let hi = lineStarts.length - 1;
	while (lo < hi) {
		const mid = (lo + hi + 1) >> 1;
		if ((lineStarts[mid] as number) <= pos) {
			lo = mid;
		} else {
			hi = mid - 1;
		}
	}
	return lo + 1;
}

/** True when the token is an identifier or a keyword usable as a member name. */
function isNameToken(t: Tok | undefined): boolean {
	return (
		t !== undefined &&
		/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(t.text) &&
		t.kind !== SyntaxKind.StringLiteral
	);
}

/**
 * Scans one tokenized file. A call is a finding when it sits in a scope
 * reachable from a registered command handler (or from an `.effects` mention).
 */
export function scanTokens(
	toks: readonly Tok[],
	text: string,
	rel: string,
): BypassFinding[] {
	const lineStarts = computeLineStarts(text);
	const findings: BypassFinding[] = [];

	// Brace blocks: blockOf[i] is the index of the innermost block containing
	// token i; blocks are (open, close) index pairs.
	const openStack: number[] = [];
	const blockParent: number[] = []; // per token: enclosing block id, or -1
	const blockOpenIdx: number[] = [];
	const blockEnclosing: number[] = [];
	const blockOfToken: number[] = new Array(toks.length).fill(-1);
	for (let i = 0; i < toks.length; i++) {
		const t = toks[i] as Tok;
		if (t.kind === SyntaxKind.CloseBraceToken) {
			openStack.pop();
		}
		const current =
			openStack.length > 0 ? (openStack[openStack.length - 1] as number) : -1;
		blockOfToken[i] = current;
		if (t.kind === SyntaxKind.OpenBraceToken) {
			const id = blockOpenIdx.length;
			blockOpenIdx.push(i);
			blockEnclosing.push(current);
			blockParent.push(current);
			openStack.push(id);
		}
	}

	// The block a given open-brace token opens.
	const blockIdByOpenTok = new Map<number, number>();
	for (let b = 0; b < blockOpenIdx.length; b++) {
		blockIdByOpenTok.set(blockOpenIdx[b] as number, b);
	}

	/**
	 * The body block of the function whose declaration starts at `from`, or -1.
	 *
	 * Scans forward at the DECLARATION's own bracket depth: an `=>` or a
	 * `function` keyword found there introduces the body, and the next `{` opens
	 * it. Depth matters -- in `const cmd = defineCommand("x", { handler: (a) =>
	 * {...} })` the arrow belongs to the handler, not to `cmd`, and lives one
	 * paren deeper.
	 */
	const bodyBlockAfter = (
		from: number,
		stopAtComma: boolean,
		introduced = false,
	): number => {
		let depth = 0;
		let sawIntroducer = introduced;
		for (let j = from; j < toks.length; j++) {
			const tj = toks[j] as Tok;
			switch (tj.kind) {
				case SyntaxKind.OpenParenToken:
				case SyntaxKind.OpenBracketToken:
					depth += 1;
					continue;
				case SyntaxKind.CloseParenToken:
				case SyntaxKind.CloseBracketToken:
					depth -= 1;
					if (depth < 0) {
						return -1;
					}
					continue;
				case SyntaxKind.OpenBraceToken:
					if (depth === 0 && sawIntroducer) {
						return blockIdByOpenTok.get(j) ?? -1;
					}
					depth += 1;
					continue;
				case SyntaxKind.CloseBraceToken:
					depth -= 1;
					if (depth < 0) {
						return -1;
					}
					continue;
				case SyntaxKind.EqualsGreaterThanToken:
				case SyntaxKind.FunctionKeyword:
					if (depth === 0) {
						sawIntroducer = true;
					}
					continue;
				case SyntaxKind.SemicolonToken:
					if (depth === 0) {
						return -1;
					}
					continue;
				case SyntaxKind.CommaToken:
					if (depth === 0 && stopAtComma) {
						return -1;
					}
					continue;
				default:
					continue;
			}
		}
		return -1;
	};

	// The token-level function table: declared name -> body block. This is what
	// makes an intra-file call graph possible at all; there is no symbol table
	// and no type information, so a name is matched as a name.
	const funcBlocks = new Map<string, number>();
	for (let i = 0; i < toks.length; i++) {
		const t = toks[i] as Tok;
		if (t.kind === SyntaxKind.FunctionKeyword && isNameToken(toks[i + 1])) {
			// The `function` keyword at `i` IS the introducer.
			const block = bodyBlockAfter(
				i + 2,
				/* stopAtComma */ false,
				/* introduced */ true,
			);
			if (block !== -1) {
				funcBlocks.set((toks[i + 1] as Tok).text, block);
			}
			continue;
		}
		if (
			(t.kind === SyntaxKind.ConstKeyword ||
				t.kind === SyntaxKind.LetKeyword ||
				t.kind === SyntaxKind.VarKeyword) &&
			isNameToken(toks[i + 1]) &&
			(toks[i + 2] as Tok | undefined)?.kind === SyntaxKind.EqualsToken
		) {
			const block = bodyBlockAfter(i + 3, /* stopAtComma */ true);
			if (block !== -1) {
				funcBlocks.set((toks[i + 1] as Tok).text, block);
			}
		}
	}

	// Roots. TWO conditions, and the first is what §11 actually asks for:
	//   1. a registered command handler -- token-level, the `handler:` property
	//      of a factory options object, either an inline function or a named one;
	//   2. as before, any scope that reaches for `.effects` at all.
	// Condition 2 alone let two shapes escape completely: a handler that never
	// mentions the handle, and a bypass one helper-call away.
	const reachable = new Set<number>();
	for (let i = 1; i < toks.length; i++) {
		if (
			(toks[i] as Tok).text === "effects" &&
			(toks[i - 1] as Tok).kind === SyntaxKind.DotToken
		) {
			let b = blockOfToken[i] as number;
			while (b !== -1) {
				reachable.add(b);
				b = blockEnclosing[b] as number;
			}
		}
	}
	for (let i = 0; i + 1 < toks.length; i++) {
		const t = toks[i] as Tok;
		if (
			t.text !== "handler" ||
			(toks[i + 1] as Tok).kind !== SyntaxKind.ColonToken
		) {
			continue;
		}
		const named = toks[i + 2] as Tok | undefined;
		if (named !== undefined && funcBlocks.has(named.text)) {
			reachable.add(funcBlocks.get(named.text) as number);
		}
		// An inline handler: the first `{` in the property's value opens it,
		// whether it is written bare (`handler: (a, c) => {}`) or through a
		// wrapper (`handler: mark((a, c) => {})`).
		for (let j = i + 2, depth = 0; j < toks.length; j++) {
			const tj = toks[j] as Tok;
			if (tj.kind === SyntaxKind.OpenBraceToken) {
				reachable.add(blockIdByOpenTok.get(j) as number);
				break;
			}
			if (
				tj.kind === SyntaxKind.OpenParenToken ||
				tj.kind === SyntaxKind.OpenBracketToken
			) {
				depth += 1;
			} else if (
				tj.kind === SyntaxKind.CloseParenToken ||
				tj.kind === SyntaxKind.CloseBracketToken
			) {
				depth -= 1;
			} else if (
				depth <= 0 &&
				(tj.kind === SyntaxKind.CommaToken ||
					tj.kind === SyntaxKind.SemicolonToken ||
					tj.kind === SyntaxKind.CloseBraceToken)
			) {
				break;
			}
		}
	}

	const inReachableScope = (i: number): boolean => {
		let b = blockOfToken[i] as number;
		while (b !== -1) {
			if (reachable.has(b)) {
				return true;
			}
			b = blockEnclosing[b] as number;
		}
		return false;
	};

	// Reachability closure: a bare `name(` inside a reachable scope pulls that
	// function's body in, transitively.
	for (let changed = true; changed; ) {
		changed = false;
		for (let i = 0; i < toks.length; i++) {
			const t = toks[i] as Tok;
			if (
				!isNameToken(t) ||
				(toks[i + 1] as Tok | undefined)?.kind !== SyntaxKind.OpenParenToken ||
				(toks[i - 1] as Tok | undefined)?.kind === SyntaxKind.DotToken
			) {
				continue;
			}
			const block = funcBlocks.get(t.text);
			if (block === undefined || reachable.has(block)) {
				continue;
			}
			if (inReachableScope(i)) {
				reachable.add(block);
				changed = true;
			}
		}
	}

	if (reachable.size === 0) {
		return findings;
	}

	// Identifiers bound to the effects handle (`const e = ctx.effects;`), so the
	// handle itself never reads as a bypass.
	const handleAliases = new Set<string>();
	for (let i = 0; i + 4 < toks.length; i++) {
		const t = toks[i] as Tok;
		if (
			(t.kind !== SyntaxKind.ConstKeyword &&
				t.kind !== SyntaxKind.LetKeyword &&
				t.kind !== SyntaxKind.VarKeyword) ||
			!isNameToken(toks[i + 1]) ||
			(toks[i + 2] as Tok).kind !== SyntaxKind.EqualsToken
		) {
			continue;
		}
		for (let j = i + 3; j < toks.length; j++) {
			const tj = toks[j] as Tok;
			if (
				tj.kind === SyntaxKind.SemicolonToken ||
				tj.kind === SyntaxKind.CommaToken
			) {
				break;
			}
			if (
				tj.text === "effects" &&
				(toks[j - 1] as Tok).kind === SyntaxKind.DotToken &&
				(toks[j + 1] as Tok | undefined)?.kind !== SyntaxKind.DotToken
			) {
				handleAliases.add((toks[i + 1] as Tok).text);
				break;
			}
		}
	}

	// Identifiers bound to an effects-handle return, so the Proxy-ceiling
	// checks have something concrete to trace.
	const carriers = new Set<string>();
	for (let i = 0; i + 2 < toks.length; i++) {
		const t = toks[i] as Tok;
		if (
			t.kind !== SyntaxKind.ConstKeyword &&
			t.kind !== SyntaxKind.LetKeyword &&
			t.kind !== SyntaxKind.VarKeyword
		) {
			continue;
		}
		const nameTok = toks[i + 1] as Tok;
		if (!isNameToken(nameTok)) {
			continue;
		}
		// Walk the initializer to the end of the statement looking for
		// `.effects.<method>(`.
		let j = i + 2;
		let sawEquals = false;
		let sawEffectsCall = false;
		for (; j < toks.length; j++) {
			const tj = toks[j] as Tok;
			if (tj.kind === SyntaxKind.SemicolonToken) {
				break;
			}
			if (tj.kind === SyntaxKind.EqualsToken) {
				sawEquals = true;
				continue;
			}
			if (
				sawEquals &&
				tj.text === "effects" &&
				(toks[j - 1] as Tok).kind === SyntaxKind.DotToken &&
				(toks[j + 1] as Tok | undefined)?.kind === SyntaxKind.DotToken &&
				(toks[j + 3] as Tok | undefined)?.kind === SyntaxKind.OpenParenToken
			) {
				sawEffectsCall = true;
			}
		}
		if (sawEquals && sawEffectsCall) {
			carriers.add(nameTok.text);
		}
	}

	const isCarrierAt = (i: number): boolean =>
		i >= 0 && i < toks.length && carriers.has((toks[i] as Tok).text);

	const push = (i: number, kind: "call" | "ceiling", text: string): void => {
		findings.push({
			file: rel,
			line: lineOf(lineStarts, (toks[i] as Tok).start),
			kind,
			text,
		});
	};

	for (let i = 0; i < toks.length; i++) {
		const t = toks[i] as Tok;
		if (!inReachableScope(i)) {
			continue;
		}

		// --- direct effect calls ---
		if (
			isNameToken(t) &&
			(toks[i + 1] as Tok | undefined)?.kind === SyntaxKind.OpenParenToken
		) {
			const prevDot =
				(toks[i - 1] as Tok | undefined)?.kind === SyntaxKind.DotToken;
			const receiver = prevDot
				? (toks[i - 2] as Tok | undefined)?.text
				: undefined;
			// Anything reached through `.effects.` is exactly what we want.
			const throughEffects =
				prevDot &&
				receiver !== undefined &&
				(receiver === "effects" || handleAliases.has(receiver));
			if (!throughEffects) {
				const leaf = t.text;
				const target = receiver !== undefined ? `${receiver}.${leaf}` : leaf;
				const banned =
					(BYPASS_PROCESS.has(leaf) &&
						(!BYPASS_PROCESS_RECEIVER_SCOPED.has(leaf) ||
							receiver === undefined ||
							BYPASS_PROCESS_RECEIVERS.has(receiver))) ||
					BYPASS_FILESYSTEM.has(leaf) ||
					(leaf === "fetch" && !prevDot) ||
					(BYPASS_NETWORK.has(leaf) &&
						receiver !== undefined &&
						BYPASS_NETWORK_RECEIVERS.has(receiver));
				if (banned) {
					push(
						i,
						"call",
						`calls ${target} directly; route it through ctx.effects`,
					);
					continue;
				}
			}
		}

		// --- Proxy ceiling 1: truthiness ---
		const truthyHere =
			// if (C) / while (C)
			((t.kind === SyntaxKind.IfKeyword ||
				t.kind === SyntaxKind.WhileKeyword) &&
				(toks[i + 1] as Tok | undefined)?.kind === SyntaxKind.OpenParenToken &&
				isCarrierAt(i + 2) &&
				(toks[i + 3] as Tok | undefined)?.kind ===
					SyntaxKind.CloseParenToken) ||
			// !C
			(t.kind === SyntaxKind.ExclamationToken && isCarrierAt(i + 1)) ||
			// C ? / C && / C ||
			(isCarrierAt(i) &&
				[
					SyntaxKind.QuestionToken,
					SyntaxKind.AmpersandAmpersandToken,
					SyntaxKind.BarBarToken,
				].includes((toks[i + 1] as Tok | undefined)?.kind as SyntaxKind)) ||
			// && C / || C
			([SyntaxKind.AmpersandAmpersandToken, SyntaxKind.BarBarToken].includes(
				t.kind,
			) &&
				isCarrierAt(i + 1));
		if (truthyHere) {
			push(
				i,
				"ceiling",
				"branches on the truthiness of an effects result; a Proxy cannot trap truthiness -- read ctx.dryRun, or forward the value instead",
			);
			continue;
		}

		// --- Proxy ceiling 2: identity comparison ---
		const identityKinds = [
			SyntaxKind.EqualsEqualsEqualsToken,
			SyntaxKind.ExclamationEqualsEqualsToken,
			SyntaxKind.EqualsEqualsToken,
			SyntaxKind.ExclamationEqualsToken,
		];
		if (
			identityKinds.includes(t.kind) &&
			(isCarrierAt(i - 1) || isCarrierAt(i + 1))
		) {
			push(
				i,
				"ceiling",
				"compares an effects result with ===; a Proxy cannot trap === -- forward the value instead",
			);
		}
	}
	return findings;
}

/**
 * Finds direct effect calls and untrappable carrier uses in the
 * repository-owned sources under `root` (see `repoSourceFiles`). Returns
 * findings in file-then-line order, and throws `BypassRootNotAWorkTree` when
 * the root is not inside a git work tree.
 */
export function scanEffectsBypasses(root: string): BypassFinding[] {
	const findings: BypassFinding[] = [];
	for (const rel of repoSourceFiles(root)) {
		let text: string;
		try {
			text = readFileSync(join(root, rel), "utf8");
		} catch {
			// A file the analyser cannot read is not evidence of a bypass.
			continue;
		}
		let toks: Tok[];
		try {
			toks = tokenize(text);
		} catch {
			continue;
		}
		findings.push(...scanTokens(toks, text, rel));
	}
	return findings;
}

/**
 * The `observe-allowlist-breadth` warning (contract §6.2).
 *
 * A one-token prefix is a near-blanket exemption for that binary: EVERY
 * invocation of it becomes an observe, which means it really executes under
 * `--dry-run`, is never written to the would-do log, and is legal inside a
 * `read_only` command. That may be exactly what the app wants -- the allowlist
 * is a declared, source-visible choice and it authorizes real execution in dry
 * mode -- so this is a warning, not an error.
 */
export function observeAllowlistBreadthWarning(binary: string): string {
	return (
		`proc_observe_allowlist prefix ['${binary}'] is a single token: EVERY ` +
		`'${binary}' invocation becomes an observe, so it really executes under ` +
		"--dry-run, is never logged, and is legal in a read_only command. " +
		"Narrow it to the subcommands you actually observe."
	);
}

/**
 * The `consequential-grant-agreement` warning (contract §8.1, §11).
 *
 * A grant exists so a reviewer reading a preview sees WHY a dangerous step is
 * there (§6.1) -- the same judgement `consequential` makes. When the grant's
 * kind is one that leaves this process (`proc_mutate` runs another program,
 * `net_mutate` changes remote state), the two declarations should almost
 * always agree. They can legitimately disagree, so this is a warning: making it
 * an error would push consumers to declare `consequential` reflexively to clear
 * a gate, which is exactly the reflex the declaration exists to end.
 */
export function consequentialGrantWarning(
	cmdPath: string,
	grant: string,
	kind: string,
): string {
	return (
		`command '${cmdPath}' declares grant '${grant}' (kind ${kind}) but is ` +
		`not consequential: a ${kind} effect leaves this process and the ` +
		"framework cannot walk it back, and the grant already says the step " +
		"is worth explaining. Declare the command consequential, or drop the " +
		"grant if the step is routine."
	);
}

/**
 * Enumerates every non-deprecated leaf command with its dotted path, sorted by
 * path so check output is deterministic.
 */
function collectAllCommands(
	app: AppImpl,
): readonly (readonly [string, RegisteredCommand])[] {
	const out: [string, RegisteredCommand][] = [];
	for (const [name, cmd] of app.commands) {
		out.push([name, cmd]);
	}
	const walkGroup = (grp: GroupImpl, prefix: readonly string[]): void => {
		for (const [cmdName, cmd] of grp.commands) {
			out.push([[...prefix, cmdName].join("."), cmd]);
		}
		for (const [subName, subGrp] of grp.groups) {
			walkGroup(subGrp, [...prefix, subName]);
		}
	};
	for (const [groupName, grp] of app.groups) {
		walkGroup(grp, [groupName]);
	}
	out.sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
	return out;
}

/**
 * The built-in provider. Registered whenever the check system turns on, so a
 * consumer that adopts checks at all gets all three lints without a TOML
 * declaration: `effects-bypass` (error), `observe-allowlist-breadth` (warn) and
 * `consequential-grant-agreement` (warn).
 */
export function effectsBypassProvider(_app: AppImpl): () => CheckSpec[] {
	// Named so tests can identify and drop the framework's own provider
	// (tests/helpers.ts dropBuiltinCheckProviders), mirroring Python's
	// conftest helper which filters on the provider function's __name__.
	return function effectsBypassCheckProvider(): CheckSpec[] {
		return [
			errorCheckSpec({
				name: "effects-bypass",
				tags: ["effects", "quality"],
				fast: true,
				pure: true,
				needsNetwork: false,
				dependsOn: [],
				impl: (ctx, reporter) => {
					let findings: BypassFinding[];
					try {
						findings = scanEffectsBypasses(ctx.projectRoot);
					} catch (e) {
						if (!(e instanceof BypassRootNotAWorkTree)) {
							throw e;
						}
						// The input rule is part of the verdict: a root the
						// repository does not own is refused here, never
						// scanned some other way.
						reporter.error(e.message);
						return reporter.found(e.message);
					}
					for (const f of findings) {
						reporter.error(`${f.file}:${f.line}: ${f.text}`);
					}
					if (findings.length === 0) {
						return reporter.passed("no direct effect calls bypass ctx.effects");
					}
					const direct = findings.filter((f) => f.kind === "call").length;
					const ceilings = findings.length - direct;
					if (ceilings === 0) {
						return reporter.found(
							`${direct} direct effect call(s) bypassing ctx.effects`,
						);
					}
					return reporter.found(
						`${direct} direct effect call(s) bypassing ctx.effects and ` +
							`${ceilings} untrappable carrier use(s)`,
					);
				},
			}),
			warnCheckSpec({
				name: "observe-allowlist-breadth",
				tags: ["effects", "quality"],
				fast: true,
				pure: true,
				needsNetwork: false,
				dependsOn: [],
				impl: (_ctx, reporter) => {
					const broad = _app.procObserveAllowlist.filter(
						(prefix) => prefix.length === 1,
					);
					for (const prefix of broad) {
						reporter.warn(observeAllowlistBreadthWarning(prefix[0] as string));
					}
					if (broad.length > 0) {
						return reporter.found(
							`${broad.length} single-token proc_observe_allowlist prefix(es)`,
						);
					}
					return reporter.passed(
						"no single-token proc_observe_allowlist prefixes",
					);
				},
			}),
			warnCheckSpec({
				name: "consequential-grant-agreement",
				tags: ["effects", "quality"],
				fast: true,
				pure: true,
				needsNetwork: false,
				dependsOn: [],
				impl: (_ctx, reporter) => {
					// Only the kinds that leave this process. A file_write or a
					// proc_spawn is local and ordinarily recoverable; a proc_mutate
					// runs another program and a net_mutate changes remote state,
					// and neither can be walked back by the framework. Widening
					// this to every grant kind would re-create the noise the
					// consequential declaration exists to remove.
					let found = 0;
					for (const [path, rc] of collectAllCommands(_app)) {
						const def = rc.def as {
							readonly consequential?: boolean;
							readonly grants?: readonly Grant[];
						};
						if (def.consequential === true) {
							continue;
						}
						for (const g of def.grants ?? []) {
							if (g.kind !== "proc_mutate" && g.kind !== "net_mutate") {
								continue;
							}
							found++;
							reporter.warn(consequentialGrantWarning(path, g.name, g.kind));
						}
					}
					if (found > 0) {
						return reporter.found(
							`${found} grant(s) on non-consequential command(s)`,
						);
					}
					return reporter.passed(
						"every escaping grant sits on a consequential command",
					);
				},
			}),
		];
	};
}
