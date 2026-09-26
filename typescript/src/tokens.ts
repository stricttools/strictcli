/**
 * The compiler's token scanner, shared by the two static checks that read a
 * consumer's source without a syntax tree: the `effects-bypass` check (§11) and
 * the framework-use lint (§28). `typescript@7` ships the scanner but no
 * in-process parser, so both work over the token stream this module produces.
 */

import {
	computeLineStarts,
	createScanner,
	SyntaxKind,
} from "typescript/unstable/ast";

export interface Tok {
	readonly kind: SyntaxKind;
	readonly text: string;
	readonly start: number;
}

/**
 * Tokens after which a `/` is division; after any other token (or at the start
 * of the file) it begins a regular expression literal. This is the choice the
 * parser makes when it calls back into `reScanSlashToken`, and making it here
 * keeps a regular expression's contents from reading as names or as invalid
 * characters.
 */
const DIVISION_AFTER: ReadonlySet<SyntaxKind> = new Set([
	SyntaxKind.Identifier,
	SyntaxKind.PrivateIdentifier,
	SyntaxKind.NumericLiteral,
	SyntaxKind.BigIntLiteral,
	SyntaxKind.StringLiteral,
	SyntaxKind.NoSubstitutionTemplateLiteral,
	SyntaxKind.TemplateTail,
	SyntaxKind.RegularExpressionLiteral,
	SyntaxKind.CloseParenToken,
	SyntaxKind.CloseBracketToken,
	SyntaxKind.CloseBraceToken,
	SyntaxKind.PlusPlusToken,
	SyntaxKind.MinusMinusToken,
	SyntaxKind.ThisKeyword,
	SyntaxKind.SuperKeyword,
	SyntaxKind.TrueKeyword,
	SyntaxKind.FalseKeyword,
	SyntaxKind.NullKeyword,
]);

/** What the scanner could not read in a file, with its 1-based line. */
export interface LexicalError {
	readonly line: number;
	readonly detail: string;
}

/**
 * Tokenizes with the compiler's scanner, dropping trivia.
 *
 * The scanner is a pure lexer: it cannot leave a template-substitution state on
 * its own, because in a real compile the PARSER decides when a `}` closes a
 * `${` and calls back into `reScanTemplateToken`, and when a `/` begins a
 * regular expression and calls back into `reScanSlashToken`. This tokenizer
 * makes both calls itself: it tracks brace depth per open substitution, and
 * reads a `/` as a regular expression wherever an expression may begin. As a
 * last resort, progress is asserted explicitly: on a zero-width token, re-scan
 * as a template continuation, and if even that does not advance, step one
 * character and carry on.
 */
export function tokenize(text: string): Tok[] {
	return tokenizeChecked(text).toks;
}

/**
 * Tokenizes as `tokenize` does and also reports the first thing the scanner
 * could not read: an unterminated string, template, or regular expression
 * literal, or a character that begins no token. It is lexical only --
 * `typescript@7` ships no in-process parser, so a file whose tokens are all
 * well formed but whose grammar is not is not detected here.
 */
export function tokenizeChecked(text: string): {
	readonly toks: Tok[];
	readonly error: LexicalError | null;
} {
	const scanner = createScanner(/* skipTrivia */ true);
	scanner.setText(text);
	const toks: Tok[] = [];
	let error: LexicalError | null = null;
	let lineStarts: number[] | undefined;
	const noteError = (pos: number, detail: string): void => {
		if (error === null) {
			lineStarts ??= computeLineStarts(text);
			error = { line: lineOf(lineStarts, pos), detail };
		}
	};
	// Brace depth, and the depth at which each open template substitution
	// began: the `}` that returns to that depth closes the substitution, and is
	// re-scanned as the template's continuation, as the parser would.
	let braceDepth = 0;
	const substitutions: number[] = [];
	let lastEnd = -1;
	for (;;) {
		let kind = scanner.scan();
		if (kind === SyntaxKind.EndOfFile) {
			break;
		}
		if (kind === SyntaxKind.OpenBraceToken) {
			braceDepth++;
		} else if (kind === SyntaxKind.CloseBraceToken) {
			if (
				substitutions.length > 0 &&
				substitutions[substitutions.length - 1] === braceDepth
			) {
				kind = scanner.reScanTemplateToken(/* isTaggedTemplate */ false);
				if (kind === SyntaxKind.TemplateTail) {
					substitutions.pop();
				}
			} else {
				braceDepth--;
			}
		} else if (kind === SyntaxKind.TemplateHead) {
			substitutions.push(braceDepth);
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
		if (
			(kind === SyntaxKind.SlashToken ||
				kind === SyntaxKind.SlashEqualsToken) &&
			!DIVISION_AFTER.has(
				(toks[toks.length - 1] as Tok | undefined)?.kind as SyntaxKind,
			)
		) {
			kind = scanner.reScanSlashToken();
		}
		if (scanner.isUnterminated()) {
			noteError(scanner.getTokenStart(), "unterminated literal");
		} else if (kind === SyntaxKind.Unknown) {
			noteError(
				scanner.getTokenStart(),
				`invalid character '${scanner.getTokenText()}'`,
			);
		}
		lastEnd = scanner.getTokenEnd();
		toks.push({
			kind,
			text: scanner.getTokenText(),
			start: scanner.getTokenStart(),
		});
	}
	return { toks, error };
}

export function lineOf(lineStarts: readonly number[], pos: number): number {
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
export function isNameToken(t: Tok | undefined): boolean {
	return (
		t !== undefined &&
		/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(t.text) &&
		t.kind !== SyntaxKind.StringLiteral
	);
}
