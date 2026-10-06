/**
 * Replays ALL committed cross-language SCF vectors (generated from the Python
 * reference formatter) and asserts the TS formatter reproduces every recorded
 * string byte-for-byte, proving cross-language parity. Also runs a seeded
 * round-trip property test: Number(format(x)) must have bit-identical value.
 */

import { strict as assert } from "node:assert";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { formatFloatCanonical } from "../src/float.js";

// Compiled test lives at typescript/dist-test/tests/, so the vectors are
// three directories up and across into conformance/.
const vectorsPath = join(
	dirname(fileURLToPath(import.meta.url)),
	"..",
	"..",
	"..",
	"go",
	"strictcli",
	"testdata",
	"float_vectors.json",
);

interface FloatVectorDoc {
	readonly count: number;
	readonly vectors: readonly { readonly bits: string; readonly scf: string }[];
}

function floatFromBits(bits: bigint): number {
	const dv = new DataView(new ArrayBuffer(8));
	dv.setBigUint64(0, bits);
	return dv.getFloat64(0);
}

function bitsFromFloat(x: number): bigint {
	const dv = new DataView(new ArrayBuffer(8));
	dv.setFloat64(0, x);
	return dv.getBigUint64(0);
}

test("all committed SCF vectors reproduce byte-for-byte", () => {
	const doc = JSON.parse(readFileSync(vectorsPath, "utf8")) as FloatVectorDoc;
	assert.ok(doc.vectors.length > 0, "no vectors loaded");
	assert.equal(
		doc.vectors.length,
		doc.count,
		`count mismatch: header says ${doc.count}, got ${doc.vectors.length}`,
	);
	for (const v of doc.vectors) {
		const x = floatFromBits(BigInt(`0x${v.bits}`));
		assert.equal(formatFloatCanonical(x), v.scf, `bits=${v.bits}`);
	}
});

// splitmix64: deterministic 64-bit PRNG over BigInt.
const MASK64 = 0xffffffffffffffffn;
function splitmix64(state: bigint): [bigint, bigint] {
	const next = (state + 0x9e3779b97f4a7c15n) & MASK64;
	let z = next;
	z = ((z ^ (z >> 30n)) * 0xbf58476d1ce4e5b9n) & MASK64;
	z = ((z ^ (z >> 27n)) * 0x94d049bb133111ebn) & MASK64;
	z = z ^ (z >> 31n);
	return [next, z];
}

test("round-trip property: Number(format(x)) is bit-identical", () => {
	let state = 0x5cff10a7n; // same seed the vector generator recorded
	let checked = 0;
	for (let i = 0; i < 5000; i++) {
		let bits: bigint;
		[state, bits] = splitmix64(state);
		const x = floatFromBits(bits);
		if (!Number.isFinite(x)) {
			continue; // NaN/Inf are outside the formatter's contract
		}
		const s = formatFloatCanonical(x);
		assert.equal(
			bitsFromFloat(Number(s)),
			bits,
			`round-trip failed for bits=${bits.toString(16)} via "${s}"`,
		);
		checked++;
	}
	assert.ok(checked > 4000, `too few finite samples: ${checked}`);
});
