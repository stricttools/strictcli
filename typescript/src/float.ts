/**
 * Strictcli canonical float form (SCF), the shortest-round-trip float
 * formatting shared byte-for-byte with the Python and Go implementations. It
 * is pinned by the committed go/strictcli/testdata/float_vectors.json and matches
 * go/strictcli/float.go formatFloatCanonical and Python
 * _format_float_canonical exactly.
 *
 * Rules:
 *  1. Digit source is the shortest decimal string that round-trips to the
 *     identical IEEE-754 double. ECMAScript Number::toString is specified to
 *     produce exactly that, so no digit post-processing is needed.
 *  2. Integer-valued floats in fixed notation always carry a trailing ".0".
 *  3. -0.0 is preserved as "-0.0".
 *  4. Fixed notation for |x| in [1e-6, 1e21); scientific outside. Zero is a
 *     carve-out: 0.0 and -0.0 are always fixed. ECMAScript's own notation
 *     switch matches this band exactly: toString uses exponential notation
 *     precisely when the decimal exponent is >= 21 or <= -7, and a shortest
 *     round-trip digit string never crosses the band boundary (it would have
 *     to round-trip to a double on the other side of 1e21 / 1e-6, which are
 *     exactly representable comparison points in both siblings).
 *  5. Scientific spelling: lowercase "e", explicit exponent sign, no exponent
 *     zero-padding ("1e+21", "1e-7", "1.5e+300") -- ECMAScript's native
 *     exponential spelling, verbatim.
 *  6. The trailing ".0" is only ever added in the fixed branch, never in the
 *     scientific branch.
 */

export function formatFloatCanonical(v: number): string {
	if (!Number.isFinite(v)) {
		// NaN/Inf are rejected at parse time everywhere; reaching here is a bug.
		throw new Error(`internal: formatFloatCanonical on non-finite ${v}`);
	}
	if (v === 0) {
		return Object.is(v, -0) ? "-0.0" : "0.0";
	}
	const s = String(v);
	if (s.includes("e")) {
		return s;
	}
	return s.includes(".") ? s : `${s}.0`;
}
