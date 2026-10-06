/**
 * Replays the committed strict-ULID vectors against the TypeScript
 * implementation.
 *
 * The vectors live at go/strictcli/testdata/ulid_vectors.json and are authored in
 * conformance/gen_ulid_vectors.py -- not derived from any implementation. The
 * Python and Go suites replay the same file, which is what pins the profile
 * (docs/process-trace-store.md, "Identifiers") across three independent
 * minters.
 */

import { strict as assert } from "node:assert";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import {
	ulidEncode,
	ulidMint,
	ulidTimestamp,
	ulidValid,
} from "../src/trace.js";

// Compiled test lives at typescript/dist-test/tests/, so the vectors are three
// directories up and across into conformance/.
const vectorsPath = join(
	dirname(fileURLToPath(import.meta.url)),
	"..",
	"..",
	"..",
	"go",
	"strictcli",
	"testdata",
	"ulid_vectors.json",
);

interface EncodeVector {
	readonly name: string;
	readonly ms: number;
	readonly random_hex: string;
	readonly ulid: string;
}

interface ParseVector {
	readonly name: string;
	readonly text: string;
	readonly valid: boolean;
	readonly ms?: number;
}

interface VectorDoc {
	readonly encode_vector_count: number;
	readonly parse_vector_count: number;
	readonly encode_vectors: readonly EncodeVector[];
	readonly parse_vectors: readonly ParseVector[];
}

const doc = JSON.parse(readFileSync(vectorsPath, "utf8")) as VectorDoc;

test("ulid vectors: the document's own counts hold", () => {
	assert.equal(doc.encode_vectors.length, doc.encode_vector_count);
	assert.equal(doc.parse_vectors.length, doc.parse_vector_count);
	assert.ok(doc.encode_vector_count > 0);
	assert.ok(doc.parse_vector_count > 0);
});

test("ulid vectors: every encode vector reproduces byte-for-byte", () => {
	for (const vec of doc.encode_vectors) {
		const randomness = Uint8Array.from(Buffer.from(vec.random_hex, "hex"));
		assert.equal(randomness.length, 10, vec.name);
		const encoded = ulidEncode(vec.ms, randomness);
		assert.equal(encoded, vec.ulid, vec.name);
		assert.equal(encoded.length, 26, vec.name);
		// Every encoding round-trips to the millisecond it carries.
		assert.equal(ulidTimestamp(encoded), vec.ms, vec.name);
	}
});

test("ulid vectors: every parse vector yields the pinned verdict", () => {
	for (const vec of doc.parse_vectors) {
		const ms = ulidTimestamp(vec.text);
		if (vec.valid) {
			assert.equal(ms, vec.ms, vec.name);
			assert.equal(ulidValid(vec.text), true, vec.name);
		} else {
			assert.equal(ms, null, vec.name);
			assert.equal(ulidValid(vec.text), false, vec.name);
		}
	}
});

test("ulid: a minted identifier carries the clock and is canonical", () => {
	const minted = ulidMint(1786594672913);
	assert.equal(minted.length, 26);
	assert.equal(ulidTimestamp(minted), 1786594672913);
	for (const ch of minted) {
		assert.ok("0123456789ABCDEFGHJKMNPQRSTVWXYZ".includes(ch));
	}
});

test("ulid: 80 crypto-random bits differ across mints", () => {
	const seen = new Set<string>();
	for (let i = 0; i < 64; i++) {
		seen.add(ulidMint(1786594672913));
	}
	assert.equal(seen.size, 64);
});

test("ulid: a non-string is never a valid identifier", () => {
	assert.equal(ulidTimestamp(null), null);
	assert.equal(ulidTimestamp(12345), null);
	assert.equal(ulidValid(undefined), false);
});
