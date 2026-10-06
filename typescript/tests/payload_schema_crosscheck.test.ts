/**
 * Dev-only third-party cross-check for the payload-schema validator
 * (effects contract §19.5).
 *
 * `@hyperjump/json-schema` is a devDependency and never a runtime one: it
 * exists to assert that the in-house validator's verdicts agree with an
 * independent implementation on every shared vector. A disagreement is a test
 * failure to investigate, never something the code resolves for itself.
 *
 * Two families are excluded by construction, because they are ours and not
 * JSON Schema's: decision 16's magnitude guard, and JSON representability
 * (which a JSON vector file cannot express in the first place).
 *
 * The declared literal carries no `$schema` -- the closed subset rejects that
 * keyword -- so the dialect is spliced onto a COPY here, purely to tell
 * hyperjump which dialect to compile against.
 */

import { strict as assert } from "node:assert";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { registerSchema, validate } from "@hyperjump/json-schema/draft-2020-12";
import { validatePayloadValue } from "../src/payload_schema.js";

const vectorsPath = join(
	dirname(fileURLToPath(import.meta.url)),
	"..",
	"..",
	"..",
	"go",
	"strictcli",
	"testdata",
	"payload_schema_vectors.json",
);

interface Vector {
	readonly name: string;
	readonly schema: Record<string, unknown>;
	readonly value?: unknown;
	readonly valid: boolean;
	readonly detail?: string;
	readonly unrepresentable_in?: readonly string[];
}

interface VectorDoc {
	readonly instance_vectors: readonly Vector[];
}

const doc = JSON.parse(readFileSync(vectorsPath, "utf8")) as VectorDoc;

/** The two details JSON Schema itself has no opinion about. */
const OURS_ONLY = new Set([
	"the number's magnitude exceeds 2^53 (declare a big identifier as a string)",
	"the value is not representable in JSON",
]);

function crossCheckable(v: Vector): boolean {
	if ((v.unrepresentable_in ?? []).includes("typescript")) {
		return false;
	}
	return v.valid || !OURS_ONLY.has(v.detail ?? "");
}

const checkable = doc.instance_vectors.filter(crossCheckable);

test("the cross-check covers most of the instance matrix", () => {
	assert.ok(checkable.length >= 130, `only ${checkable.length} vectors`);
});

let uriSeq = 0;

for (const v of checkable) {
	test(`cross-check: ${v.name}`, async () => {
		uriSeq += 1;
		const uri = `https://strictcli.test/payload-schema/${uriSeq}`;
		registerSchema(
			{
				$schema: "https://json-schema.org/draft/2020-12/schema",
				...v.schema,
			},
			uri,
		);
		const theirValidator = await validate(uri);
		// The instance type is spelled through the validator's own signature
		// rather than imported from a transitive package.
		type Instance = Parameters<typeof theirValidator>[0];
		const theirs = theirValidator(v.value as Instance).valid;
		const ours = validatePayloadValue(v.value, v.schema) === null;
		assert.equal(
			ours,
			theirs,
			`in-house says ${ours}, hyperjump says ${theirs}`,
		);
	});
}
