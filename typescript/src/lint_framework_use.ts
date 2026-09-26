/**
 * The framework-use lint (effects contract §28), run through the reserved
 * `--lint-framework-use` flag every app carries. It reads the program's own
 * source and refuses the constructs that bypass the framework's ownership of
 * exits, output, argv, and environment input.
 *
 * WHAT IS SCANNED (§28.2). The scan root is the working directory and must
 * hold `package.json`; the running entry script (`process.argv[1]`) must be,
 * by real path, one of its `bin` targets. Only repository-owned files are read
 * -- what `git ls-files --cached --others --exclude-standard` lists -- and only
 * the modules reachable from the `bin` entries through relative specifiers.
 * Each `bin` target is mapped back to its source: a repository-owned source
 * file is its own source, and a built file under `compilerOptions.outDir` maps
 * to the same relative path under `compilerOptions.rootDir` with the `.ts`
 * family's extension. Test files are never scanned.
 *
 * HOW (§28.3). `typescript@7` ships no in-process parser, so the scan works
 * over the compiler's token stream, as the `effects-bypass` check does. Names
 * are resolved through each file's imports, not through scopes (§17):
 * `process` and `console` are globals, a default or namespace import of
 * `process` / `node:process` under any local name reads as `process`, a named
 * import of a listed member from that module is reported at its import line,
 * and so is a destructuring whose initializer is the bare `process`.
 */

import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, realpathSync } from "node:fs";
import { join, posix } from "node:path";
import { computeLineStarts, SyntaxKind } from "typescript/unstable/ast";
import type { Writer } from "./context.js";
import {
	errLintArgvAccess,
	errLintEnvironmentRead,
	errLintFrameworkUseBinUnresolved,
	errLintFrameworkUseManifestMismatch,
	errLintFrameworkUseNoManifest,
	errLintFrameworkUseNotWorkTree,
	errLintFrameworkUseUnparsable,
	errLintProcessExit,
	errLintStderrWrite,
	errLintStdoutWrite,
} from "./errors.js";
import { isNameToken, lineOf, type Tok, tokenizeChecked } from "./tokens.js";

/** The manifest a TypeScript program's scan root must hold. */
const MANIFEST = "package.json";

/** The rule identifiers (§28.3); TypeScript has no goroutine rule. */
type Rule =
	| "process-exit"
	| "stdout-write"
	| "stderr-write"
	| "argv-access"
	| "environment-read";

/** One finding: a construct at a file and line. */
export interface FrameworkUseFinding {
	readonly path: string;
	readonly line: number;
	readonly rule: Rule;
	readonly message: string;
}

/** What a scan produced: the sorted finding lines, or the refusal. */
export type FrameworkUseScan =
	| { readonly kind: "findings"; readonly findings: FrameworkUseFinding[] }
	| { readonly kind: "refused"; readonly message: string };

/** Each rule's message for a construct, with TypeScript's spellings (§12.17). */
function ruleMessage(rule: Rule, construct: string): string {
	switch (rule) {
		case "process-exit":
			return errLintProcessExit(construct, "throw new ExitNow(code, message)");
		case "stdout-write":
			return errLintStdoutWrite(
				construct,
				"ctx.out",
				"ctx.payload",
				"ctx.document()",
			);
		case "stderr-write":
			return errLintStderrWrite(construct, "ctx.warn", "ctx.error");
		case "argv-access":
			return errLintArgvAccess(construct);
		case "environment-read":
			return errLintEnvironmentRead(construct);
	}
}

/**
 * The members of `process` a reference, a named import, or a destructuring
 * reports, with their rules. `exitCode` is a finding only when assigned, so it
 * is matched separately and never through an import or a destructuring, both
 * of which only read it.
 */
const PROCESS_MEMBERS: ReadonlyMap<string, Rule> = new Map<string, Rule>([
	["exit", "process-exit"],
	["abort", "process-exit"],
	["stdout", "stdout-write"],
	["stderr", "stderr-write"],
	["argv", "argv-access"],
	["argv0", "argv-access"],
	["execArgv", "argv-access"],
	["env", "environment-read"],
]);

/** The console members that write to stderr; every other member is stdout. */
const CONSOLE_STDERR: ReadonlySet<string> = new Set([
	"error",
	"warn",
	"trace",
	"assert",
]);

const PROCESS_MODULES: ReadonlySet<string> = new Set([
	"process",
	"node:process",
]);
const UTIL_MODULES: ReadonlySet<string> = new Set(["util", "node:util"]);

const ASSIGNMENT_OPERATORS: ReadonlySet<string> = new Set([
	"=",
	"+=",
	"-=",
	"*=",
	"/=",
	"%=",
	"**=",
	"<<=",
	">>=",
	">>>=",
	"&=",
	"|=",
	"^=",
	"&&=",
	"||=",
	"??=",
]);

/** The extensions a module source may carry. */
const SOURCE_EXTS = [
	".ts",
	".tsx",
	".mts",
	".cts",
	".js",
	".jsx",
	".mjs",
	".cjs",
];

/** A built extension and the source extensions TypeScript substitutes for it. */
const EXTENSION_SUBSTITUTION: ReadonlyMap<string, readonly string[]> = new Map([
	[".js", [".ts", ".tsx", ".js", ".jsx"]],
	[".jsx", [".tsx", ".jsx"]],
	[".mjs", [".mts", ".mjs"]],
	[".cjs", [".cts", ".cjs"]],
]);

/** A built file's extension mapped to its source's, under outDir -> rootDir. */
const BUILT_TO_SOURCE: ReadonlyMap<string, string> = new Map([
	[".js", ".ts"],
	[".mjs", ".mts"],
	[".cjs", ".cts"],
]);

/**
 * Runs the lint for the running program from the working directory and writes
 * its report (§28.1): one line per finding on stdout, exit 1 on findings and 0
 * on none; a refusal is one `error: ` line on stderr and exit 1.
 */
export function runFrameworkUseLint(out: Writer, err: Writer): number {
	const scan = scanFrameworkUse(process.cwd(), process.argv[1]);
	if (scan.kind === "refused") {
		err.write(`error: ${scan.message}\n`);
		return 1;
	}
	for (const f of scan.findings) {
		out.write(`${f.path}:${f.line}: ${f.rule}: ${f.message}\n`);
	}
	return scan.findings.length === 0 ? 0 : 1;
}

/**
 * Scans the program whose manifest is in `root` and whose running entry
 * script is `entryScript`, returning its findings sorted by path (byte order),
 * line, rule, and message, or the refusal that stopped the scan.
 */
export function scanFrameworkUse(
	root: string,
	entryScript: string | undefined,
): FrameworkUseScan {
	const manifestPath = join(root, MANIFEST);
	if (!existsSync(manifestPath)) {
		return refused(errLintFrameworkUseNoManifest(MANIFEST, root));
	}
	const owned = repositoryOwnedFiles(root);
	if (owned === null) {
		return refused(errLintFrameworkUseNotWorkTree(root));
	}
	let manifest: Record<string, unknown>;
	try {
		manifest = JSON.parse(readFileSync(manifestPath, "utf8")) as Record<
			string,
			unknown
		>;
	} catch {
		return refused(errLintFrameworkUseManifestMismatch(MANIFEST, root));
	}
	const bins = binEntries(manifest);
	if (!declaresProgram(root, bins, entryScript)) {
		return refused(errLintFrameworkUseManifestMismatch(MANIFEST, root));
	}
	const roots: string[] = [];
	for (const [name, target] of bins) {
		const source = binSource(root, owned, target);
		if (source === null) {
			return refused(errLintFrameworkUseBinUnresolved(name));
		}
		roots.push(source);
	}

	const findings: FrameworkUseFinding[] = [];
	const seen = new Set<string>();
	const queue = [...roots];
	while (queue.length > 0) {
		const rel = queue.shift() as string;
		if (seen.has(rel) || isTestFile(rel)) {
			continue;
		}
		seen.add(rel);
		const text = readFileSync(join(root, rel), "utf8");
		const { toks, error } = tokenizeChecked(text);
		// A file the scan cannot read is refused rather than skipped: its
		// constructs would go unseen. JSX text is read only by a parser, so the
		// lexical check does not apply to .tsx and .jsx files.
		if (error !== null && !/\.[jt]sx$/.test(rel)) {
			return refused(
				errLintFrameworkUseUnparsable(
					rel,
					`${error.detail} at line ${error.line}`,
				),
			);
		}
		for (const spec of relativeSpecifiers(toks)) {
			const target = resolveSpecifier(owned, rel, spec);
			if (target !== null && !seen.has(target)) {
				queue.push(target);
			}
		}
		findings.push(...scanFile(toks, text, rel));
	}
	findings.sort(compareFindings);
	return { kind: "findings", findings };
}

function refused(message: string): FrameworkUseScan {
	return { kind: "refused", message };
}

function compareFindings(
	a: FrameworkUseFinding,
	b: FrameworkUseFinding,
): number {
	const byPath = Buffer.compare(Buffer.from(a.path), Buffer.from(b.path));
	if (byPath !== 0) {
		return byPath;
	}
	if (a.line !== b.line) {
		return a.line - b.line;
	}
	if (a.rule !== b.rule) {
		return a.rule < b.rule ? -1 : 1;
	}
	return a.message < b.message ? -1 : a.message > b.message ? 1 : 0;
}

/**
 * The repository-owned files under `root`, relative to it with `/`
 * separators, or null when `root` is not inside a git work tree (§11.2's rule:
 * no filesystem-walk fallback).
 */
function repositoryOwnedFiles(root: string): ReadonlySet<string> | null {
	let listed: string;
	try {
		listed = execFileSync(
			"git",
			["ls-files", "--cached", "--others", "--exclude-standard", "-z"],
			{ cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] },
		);
	} catch {
		return null;
	}
	return new Set(listed.split("\0").filter((rel) => rel !== ""));
}

/** The manifest's `bin` entries as `[name, target]` pairs. */
function binEntries(manifest: Record<string, unknown>): [string, string][] {
	const bin = manifest.bin;
	if (typeof bin === "string") {
		const name = typeof manifest.name === "string" ? manifest.name : "";
		return [[name, bin]];
	}
	if (bin !== null && typeof bin === "object") {
		return Object.entries(bin as Record<string, unknown>)
			.filter((e): e is [string, string] => typeof e[1] === "string")
			.map(([name, target]) => [name, target]);
	}
	return [];
}

/** The identity check: the running entry script is one `bin` target. */
function declaresProgram(
	root: string,
	bins: readonly [string, string][],
	entryScript: string | undefined,
): boolean {
	if (entryScript === undefined) {
		return false;
	}
	const running = realPath(entryScript);
	if (running === null) {
		return false;
	}
	return bins.some(([, target]) => realPath(join(root, target)) === running);
}

function realPath(path: string): string | null {
	try {
		return realpathSync(path);
	} catch {
		return null;
	}
}

/** A path relative to the scan root, normalized, or null when it leaves it. */
function normalizeRel(path: string): string | null {
	const n = posix.normalize(path.replaceAll("\\", "/"));
	if (n === ".." || n.startsWith("../") || posix.isAbsolute(n)) {
		return null;
	}
	return n.replace(/^\.\//, "").replace(/\/$/, "");
}

/**
 * A `bin` target's source (§28.2): the target itself when it is a
 * repository-owned source file, else its path under tsconfig's `outDir` mapped
 * to the same relative path under `rootDir` with the source extension, when
 * that file is repository-owned. Null when neither rule resolves it.
 */
function binSource(
	root: string,
	owned: ReadonlySet<string>,
	target: string,
): string | null {
	const rel = normalizeRel(target);
	if (rel === null) {
		return null;
	}
	if (owned.has(rel) && SOURCE_EXTS.some((ext) => rel.endsWith(ext))) {
		return rel;
	}
	const options = tsconfigCompilerOptions(root);
	const outDir =
		typeof options?.outDir === "string" ? normalizeRel(options.outDir) : null;
	const rootDir =
		typeof options?.rootDir === "string" ? normalizeRel(options.rootDir) : null;
	if (outDir === null || rootDir === null) {
		return null;
	}
	const prefix = outDir === "" || outDir === "." ? "" : `${outDir}/`;
	if (!rel.startsWith(prefix)) {
		return null;
	}
	const inner = rel.slice(prefix.length);
	const ext = posix.extname(inner);
	const sourceExt = BUILT_TO_SOURCE.get(ext);
	if (sourceExt === undefined) {
		return null;
	}
	const base = `${inner.slice(0, inner.length - ext.length)}${sourceExt}`;
	const source =
		rootDir === "" || rootDir === "." ? base : `${rootDir}/${base}`;
	return owned.has(source) ? source : null;
}

/** `compilerOptions` of the scan root's tsconfig.json, or null. */
function tsconfigCompilerOptions(root: string): Record<string, unknown> | null {
	const path = join(root, "tsconfig.json");
	if (!existsSync(path)) {
		return null;
	}
	try {
		const parsed = JSON.parse(stripJsonComments(readFileSync(path, "utf8"))) as
			| Record<string, unknown>
			| undefined;
		const options = parsed?.compilerOptions;
		return options !== null && typeof options === "object"
			? (options as Record<string, unknown>)
			: null;
	} catch {
		return null;
	}
}

/** Removes the comments and trailing commas tsconfig.json may carry. */
function stripJsonComments(text: string): string {
	let out = "";
	let i = 0;
	while (i < text.length) {
		const c = text[i] as string;
		if (c === '"') {
			let j = i + 1;
			while (j < text.length && text[j] !== '"') {
				j += text[j] === "\\" ? 2 : 1;
			}
			out += text.slice(i, j + 1);
			i = j + 1;
		} else if (c === "/" && text[i + 1] === "/") {
			while (i < text.length && text[i] !== "\n") {
				i++;
			}
		} else if (c === "/" && text[i + 1] === "*") {
			const end = text.indexOf("*/", i + 2);
			i = end === -1 ? text.length : end + 2;
		} else {
			out += c;
			i++;
		}
	}
	return out.replace(/,(\s*[}\]])/g, "$1");
}

/** Test files are never scanned (§28.2). */
function isTestFile(rel: string): boolean {
	const parts = rel.split("/");
	const base = parts[parts.length - 1] as string;
	return (
		/\.(test|spec)\./.test(base) ||
		parts.slice(0, -1).some((part) => part === "__tests__")
	);
}

/**
 * Resolves a relative specifier from `fromRel` to a repository-owned module,
 * with TypeScript's extension substitution and a directory's `index` file.
 */
function resolveSpecifier(
	owned: ReadonlySet<string>,
	fromRel: string,
	spec: string,
): string | null {
	const joined = normalizeRel(posix.join(posix.dirname(fromRel), spec));
	if (joined === null) {
		return null;
	}
	const candidates: string[] = [];
	const ext = posix.extname(joined);
	const substituted = EXTENSION_SUBSTITUTION.get(ext);
	if (substituted !== undefined) {
		const stem = joined.slice(0, joined.length - ext.length);
		candidates.push(...substituted.map((e) => `${stem}${e}`));
	} else if (SOURCE_EXTS.includes(ext)) {
		candidates.push(joined);
	} else {
		candidates.push(...SOURCE_EXTS.map((e) => `${joined}${e}`));
		candidates.push(...SOURCE_EXTS.map((e) => `${joined}/index${e}`));
	}
	return candidates.find((c) => owned.has(c) && !c.endsWith(".d.ts")) ?? null;
}

function isString(t: Tok | undefined): boolean {
	return (
		t !== undefined &&
		(t.kind === SyntaxKind.StringLiteral ||
			t.kind === SyntaxKind.NoSubstitutionTemplateLiteral)
	);
}

function stringValue(t: Tok): string {
	return t.text.slice(1, -1);
}

function isRelative(spec: string): boolean {
	return spec.startsWith("./") || spec.startsWith("../");
}

/** One import declaration's clause, as the rules read it. */
interface ImportDecl {
	/** Index of the `import` keyword. */
	readonly at: number;
	readonly module: string;
	readonly typeOnly: boolean;
	/** Default and namespace bindings' local names. */
	readonly wholeModule: readonly string[];
	/** Named bindings: [imported, local]. */
	readonly named: readonly (readonly [string, string])[];
}

/**
 * Parses the static import declaration whose `import` keyword is at `i`, or
 * returns null when the keyword starts something else (a dynamic import,
 * `import.meta`, an import-equals).
 */
function parseImport(toks: readonly Tok[], i: number): ImportDecl | null {
	let j = i + 1;
	const first = toks[j];
	if (first === undefined) {
		return null;
	}
	if (isString(first)) {
		return {
			at: i,
			module: stringValue(first),
			typeOnly: false,
			wholeModule: [],
			named: [],
		};
	}
	let typeOnly = false;
	if (
		first.text === "type" &&
		toks[j + 1]?.text !== "from" &&
		toks[j + 1]?.text !== ","
	) {
		typeOnly = true;
		j++;
	}
	const wholeModule: string[] = [];
	const named: [string, string][] = [];
	for (;;) {
		const t = toks[j];
		if (t === undefined) {
			return null;
		}
		if (t.text === "from" && isString(toks[j + 1])) {
			return {
				at: i,
				module: stringValue(toks[j + 1] as Tok),
				typeOnly,
				wholeModule,
				named,
			};
		}
		if (t.kind === SyntaxKind.AsteriskToken) {
			if (toks[j + 1]?.text === "as" && isNameToken(toks[j + 2])) {
				wholeModule.push((toks[j + 2] as Tok).text);
				j += 3;
				continue;
			}
			return null;
		}
		if (t.kind === SyntaxKind.OpenBraceToken) {
			j++;
			while (
				toks[j] !== undefined &&
				toks[j]?.kind !== SyntaxKind.CloseBraceToken
			) {
				let entry = toks[j] as Tok;
				if (
					entry.text === "type" &&
					isNameToken(toks[j + 1]) &&
					toks[j + 1]?.text !== "as"
				) {
					j++;
					entry = toks[j] as Tok;
				}
				if (isNameToken(entry) || isString(entry)) {
					const imported = isString(entry) ? stringValue(entry) : entry.text;
					let local = imported;
					if (toks[j + 1]?.text === "as" && isNameToken(toks[j + 2])) {
						local = (toks[j + 2] as Tok).text;
						j += 2;
					}
					named.push([imported, local]);
				}
				j++;
			}
			j++;
			continue;
		}
		if (t.kind === SyntaxKind.CommaToken) {
			j++;
			continue;
		}
		if (isNameToken(t) && t.text !== "from") {
			wholeModule.push(t.text);
			j++;
			continue;
		}
		return null;
	}
}

/**
 * The relative module specifiers a file links (§28.2): static `import` and
 * `export ... from` declarations, and `import("...")` and `require("...")`
 * with a string literal. Bare specifiers (packages) are not followed.
 */
function relativeSpecifiers(toks: readonly Tok[]): string[] {
	const specs: string[] = [];
	for (let i = 0; i < toks.length; i++) {
		const t = toks[i] as Tok;
		const prev = toks[i - 1];
		const afterDot =
			prev !== undefined &&
			(prev.kind === SyntaxKind.DotToken ||
				prev.kind === SyntaxKind.QuestionDotToken);
		if (t.kind === SyntaxKind.ImportKeyword && !afterDot) {
			if (
				toks[i + 1]?.kind === SyntaxKind.OpenParenToken &&
				isString(toks[i + 2])
			) {
				specs.push(stringValue(toks[i + 2] as Tok));
				continue;
			}
			const decl = parseImport(toks, i);
			if (decl !== null) {
				specs.push(decl.module);
			}
			continue;
		}
		if (t.kind === SyntaxKind.ExportKeyword) {
			// export * from "m", export * as ns from "m", export { a } from "m",
			// export type { a } from "m": the clause never holds a string, so
			// the first string after `from` within the statement is the module.
			for (let j = i + 1; j < toks.length; j++) {
				const tj = toks[j] as Tok;
				if (
					tj.kind === SyntaxKind.SemicolonToken ||
					(tj.kind !== SyntaxKind.AsteriskToken &&
						tj.kind !== SyntaxKind.OpenBraceToken &&
						tj.kind !== SyntaxKind.CloseBraceToken &&
						tj.kind !== SyntaxKind.CommaToken &&
						!isNameToken(tj))
				) {
					break;
				}
				if (tj.text === "from" && isString(toks[j + 1])) {
					specs.push(stringValue(toks[j + 1] as Tok));
					break;
				}
				if (
					j === i + 1 &&
					tj.kind !== SyntaxKind.AsteriskToken &&
					tj.kind !== SyntaxKind.OpenBraceToken &&
					tj.text !== "type"
				) {
					break;
				}
			}
			continue;
		}
		if (
			t.text === "require" &&
			!afterDot &&
			toks[i + 1]?.kind === SyntaxKind.OpenParenToken &&
			isString(toks[i + 2])
		) {
			specs.push(stringValue(toks[i + 2] as Tok));
		}
	}
	return specs.filter(isRelative);
}

/** Scans one file's tokens for the §28.3 constructs. */
function scanFile(
	toks: readonly Tok[],
	text: string,
	rel: string,
): FrameworkUseFinding[] {
	const lineStarts = computeLineStarts(text);
	const findings: FrameworkUseFinding[] = [];
	const add = (at: number, rule: Rule, construct: string): void => {
		findings.push({
			path: rel,
			line: lineOf(lineStarts, (toks[at] as Tok).start),
			rule,
			message: ruleMessage(rule, construct),
		});
	};

	// The local names that read as `process`: the global, plus every default
	// or namespace import of the process module. Named imports of a listed
	// member, and of `parseArgs` from util, are findings at the import line.
	const processNames = new Set<string>(["process"]);
	for (let i = 0; i < toks.length; i++) {
		const t = toks[i] as Tok;
		const prev = toks[i - 1];
		if (
			t.kind !== SyntaxKind.ImportKeyword ||
			(prev !== undefined &&
				(prev.kind === SyntaxKind.DotToken ||
					prev.kind === SyntaxKind.QuestionDotToken))
		) {
			continue;
		}
		const decl = parseImport(toks, i);
		if (decl === null || decl.typeOnly) {
			continue;
		}
		if (PROCESS_MODULES.has(decl.module)) {
			for (const local of decl.wholeModule) {
				processNames.add(local);
			}
			for (const [imported] of decl.named) {
				const rule = PROCESS_MEMBERS.get(imported);
				if (rule !== undefined) {
					add(decl.at, rule, `process.${imported}`);
				}
			}
		}
		if (UTIL_MODULES.has(decl.module)) {
			for (const [imported] of decl.named) {
				if (imported === "parseArgs") {
					add(decl.at, "argv-access", "parseArgs");
				}
			}
		}
	}

	for (let i = 0; i < toks.length; i++) {
		const t = toks[i] as Tok;
		if (!isNameToken(t)) {
			continue;
		}
		const isProcess = processNames.has(t.text);
		const isConsole = t.text === "console";
		if (!isProcess && !isConsole) {
			continue;
		}
		const prev = toks[i - 1];
		if (
			prev !== undefined &&
			(prev.kind === SyntaxKind.DotToken ||
				prev.kind === SyntaxKind.QuestionDotToken)
		) {
			continue;
		}
		const next = toks[i + 1];
		const access =
			next !== undefined &&
			(next.kind === SyntaxKind.DotToken ||
				next.kind === SyntaxKind.QuestionDotToken);
		if (access && isNameToken(toks[i + 2])) {
			const member = (toks[i + 2] as Tok).text;
			if (isConsole) {
				add(
					i,
					CONSOLE_STDERR.has(member) ? "stderr-write" : "stdout-write",
					`console.${member}`,
				);
				continue;
			}
			if (member === "exitCode") {
				if (ASSIGNMENT_OPERATORS.has(toks[i + 3]?.text ?? "")) {
					add(i, "process-exit", "process.exitCode");
				}
				continue;
			}
			const rule = PROCESS_MEMBERS.get(member);
			if (rule !== undefined) {
				add(i, rule, `process.${member}`);
			}
			continue;
		}
		if (isProcess && prev?.kind === SyntaxKind.EqualsToken && !access) {
			destructuredMembers(toks, i - 2, add);
		}
	}
	return findings;
}

/**
 * Reports each listed member a destructuring pattern ending at `close` (the
 * `}` before `= process`) takes, at the line of the pattern's opening brace.
 */
function destructuredMembers(
	toks: readonly Tok[],
	close: number,
	add: (at: number, rule: Rule, construct: string) => void,
): void {
	if (toks[close]?.kind !== SyntaxKind.CloseBraceToken) {
		return;
	}
	let depth = 0;
	let open = -1;
	for (let j = close; j >= 0; j--) {
		const k = (toks[j] as Tok).kind;
		if (
			k === SyntaxKind.CloseBraceToken ||
			k === SyntaxKind.CloseBracketToken
		) {
			depth++;
		} else if (
			k === SyntaxKind.OpenBraceToken ||
			k === SyntaxKind.OpenBracketToken
		) {
			depth--;
			if (depth === 0) {
				open = j;
				break;
			}
		}
	}
	if (open < 0 || toks[open]?.kind !== SyntaxKind.OpenBraceToken) {
		return;
	}
	depth = 0;
	for (let j = open + 1; j < close; j++) {
		const tj = toks[j] as Tok;
		if (
			tj.kind === SyntaxKind.OpenBraceToken ||
			tj.kind === SyntaxKind.OpenBracketToken
		) {
			depth++;
			continue;
		}
		if (
			tj.kind === SyntaxKind.CloseBraceToken ||
			tj.kind === SyntaxKind.CloseBracketToken
		) {
			depth--;
			continue;
		}
		if (depth !== 0 || !isNameToken(tj)) {
			continue;
		}
		const before = toks[j - 1] as Tok;
		const isKey =
			before.kind === SyntaxKind.OpenBraceToken ||
			before.kind === SyntaxKind.CommaToken;
		const rule = PROCESS_MEMBERS.get(tj.text);
		if (isKey && rule !== undefined) {
			add(open, rule, `process.${tj.text}`);
		}
	}
}
