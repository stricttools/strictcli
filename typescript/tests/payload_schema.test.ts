/**
 * The declared payload schema's validator (effects contract §19.5).
 *
 * The bulk of the coverage is the committed cross-language vector file at
 * go/strictcli/testdata/payload_schema_vectors.json, replayed here and by the Python and
 * Go suites. Every vector pins both the verdict AND the exact error text, which
 * is what makes the three validators byte-identical rather than merely
 * similarly-strict.
 *
 * The vectors are read with JSON.parse, which creates OWN data properties for
 * every key including `__proto__` -- an object literal would set the prototype
 * instead, and the prototype-chain vectors would test nothing.
 *
 * The tests that cannot be shared vectors -- values JSON has no way to carry,
 * and the framework's own wiring -- are written natively below.
 */

import { strict as assert } from "node:assert";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { createApp, defineReadOnlyCommand } from "../src/index.js";
import {
	PAYLOAD_SCHEMA_KEYWORDS,
	schemaArray,
	schemaConst,
	schemaEnum,
	schemaObject,
	schemaType,
	validatePayloadSchema,
	validatePayloadSchemaLiteral,
	validatePayloadValue,
} from "../src/payload_schema.js";

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
	"payload_schema_vectors.json",
);

interface Vector {
	readonly name: string;
	readonly schema: Record<string, unknown>;
	readonly value?: unknown;
	readonly valid: boolean;
	readonly path?: string;
	readonly detail?: string;
	readonly unrepresentable_in?: readonly string[];
	readonly unrepresentable_reason?: string;
}

interface VectorDoc {
	readonly schema_vector_count: number;
	readonly instance_vector_count: number;
	readonly schema_vectors: readonly Vector[];
	readonly instance_vectors: readonly Vector[];
}

const doc = JSON.parse(readFileSync(vectorsPath, "utf8")) as VectorDoc;

function applicable(vectors: readonly Vector[]): Vector[] {
	return vectors.filter(
		(v) => !(v.unrepresentable_in ?? []).includes("typescript"),
	);
}

test("the vector file's counts match its header", () => {
	assert.equal(doc.schema_vector_count, doc.schema_vectors.length);
	assert.equal(doc.instance_vector_count, doc.instance_vectors.length);
	assert.ok(doc.schema_vectors.length >= 50);
	assert.ok(doc.instance_vectors.length >= 140);
});

test("every vector exclusion carries a reason and spares at least one impl", () => {
	for (const v of [...doc.schema_vectors, ...doc.instance_vectors]) {
		const excluded = v.unrepresentable_in ?? [];
		if (excluded.length === 0) {
			continue;
		}
		assert.ok(
			(v.unrepresentable_reason ?? "").trim() !== "",
			`${v.name} excludes without a reason`,
		);
		assert.ok(excluded.length < 3, `${v.name} excludes every implementation`);
	}
});

for (const v of applicable(doc.schema_vectors)) {
	test(`schema vector: ${v.name}`, () => {
		const found = validatePayloadSchema(v.schema);
		if (v.valid) {
			assert.equal(
				found,
				null,
				`unexpectedly rejected: ${JSON.stringify(found)}`,
			);
			return;
		}
		assert.notEqual(found, null, "unexpectedly accepted");
		assert.equal(found?.path, v.path);
		assert.equal(found?.detail, v.detail);
	});
}

for (const v of applicable(doc.instance_vectors)) {
	test(`instance vector: ${v.name}`, () => {
		assert.equal(
			validatePayloadSchema(v.schema),
			null,
			"the vector's own schema is illegal",
		);
		const found = validatePayloadValue(v.value, v.schema);
		if (v.valid) {
			assert.equal(
				found,
				null,
				`unexpectedly rejected: ${JSON.stringify(found)}`,
			);
			return;
		}
		assert.notEqual(found, null, "unexpectedly accepted");
		assert.equal(found?.path, v.path);
		assert.equal(found?.detail, v.detail);
	});
}

// ---------------------------------------------------------------------------
// Values JSON cannot carry
// ---------------------------------------------------------------------------

test("non-representable values are refused", () => {
	const notJSON = "the value is not representable in JSON";
	for (const value of [
		undefined,
		Number.NaN,
		Number.POSITIVE_INFINITY,
		Number.NEGATIVE_INFINITY,
		() => 1,
		Symbol("s"),
	]) {
		const found = validatePayloadValue(value, {});
		assert.notEqual(found, null, `unexpectedly accepted ${String(value)}`);
		assert.equal(found?.detail, notJSON);
	}
});

test("a non-representable value nested in an object names its path", () => {
	const found = validatePayloadValue({ a: [0, Number.NaN] }, {});
	assert.deepEqual(found, {
		path: 'payload["a"][1]',
		detail: "the value is not representable in JSON",
	});
});

test("an undefined member is validated as the absent key the emitter writes", () => {
	// The machine-mode serializer omits an undefined-valued property, so the
	// validator sees the object it will actually write -- and `required` is
	// what reports the loss, by name.
	assert.equal(validatePayloadValue({ a: undefined }, {}), null);
	assert.deepEqual(
		validatePayloadValue({ a: undefined }, { required: ["a"] }),
		{
			path: "payload",
			detail: 'required property "a" is missing',
		},
	);
});

test("a bigint is an integer, and the magnitude guard sees its exact value", () => {
	// `int` values are bigint end-to-end in this implementation, which is the
	// one place TypeScript CAN represent 2^53+1 exactly.
	assert.equal(validatePayloadValue(1n, { type: "integer" }), null);
	assert.equal(validatePayloadValue(2n ** 53n, {}), null);
	assert.deepEqual(validatePayloadValue(2n ** 53n + 1n, {}), {
		path: "payload",
		detail:
			"the number's magnitude exceeds 2^53 (declare a big identifier as a string)",
	});
	assert.deepEqual(validatePayloadValue(-(2n ** 53n) - 1n, {}), {
		path: "payload",
		detail:
			"the number's magnitude exceeds 2^53 (declare a big identifier as a string)",
	});
	assert.equal(validatePayloadValue(1n, { const: 1 }), null);
	assert.equal(
		validatePayloadValue(2n, { type: "boolean" })?.detail,
		'expected type "boolean", got integer',
	);
});

test("a Map is validated as the object the emitter writes", () => {
	const value = new Map<string, unknown>([
		["b", 2],
		["a", 1],
	]);
	assert.equal(
		validatePayloadValue(value, {
			type: "object",
			required: ["a", "b"],
			additionalProperties: { type: "integer" },
		}),
		null,
	);
});

test("a toJSON method decides the shape that is validated", () => {
	const value = { at: new Date(Date.UTC(2025, 0, 15)) };
	assert.equal(
		validatePayloadValue(value, {
			type: "object",
			properties: { at: { type: "string" } },
		}),
		null,
	);
});

test("a registration-time const must be representable", () => {
	const found = validatePayloadSchema({ const: Number.NaN });
	assert.deepEqual(found, {
		path: "payload_schema.const",
		detail: "the value is not representable in JSON",
	});
});

// ---------------------------------------------------------------------------
// JavaScript's prototype chain, tested through real prototype exposure
// ---------------------------------------------------------------------------

test("an inherited property does not satisfy required", () => {
	const proto = { inherited: 1 };
	const value = Object.create(proto) as Record<string, unknown>;
	value.own = 2;
	const found = validatePayloadValue(value, {
		type: "object",
		required: ["inherited"],
	});
	assert.deepEqual(found, {
		path: "payload",
		detail: 'required property "inherited" is missing',
	});
});

test("an inherited property is not an additional property either", () => {
	const proto = { inherited: 1 };
	const value = Object.create(proto) as Record<string, unknown>;
	const found = validatePayloadValue(value, {
		type: "object",
		additionalProperties: false,
	});
	assert.equal(found, null);
});

test("toString on the prototype does not satisfy required", () => {
	const found = validatePayloadValue({}, { required: ["toString"] });
	assert.deepEqual(found, {
		path: "payload",
		detail: 'required property "toString" is missing',
	});
});

// ---------------------------------------------------------------------------
// Registration and emission wiring
// ---------------------------------------------------------------------------

test("an unknown keyword is a registration error", () => {
	assert.throws(
		() =>
			defineReadOnlyCommand("run", {
				help: "run",
				payloadSchema: { type: "object", minProperties: 1 },
				handler: () => 0,
			}),
		(err: Error) =>
			err.message ===
			'command "run": payload schema is invalid at payload_schema: ' +
				'unknown keyword "minProperties" (the closed subset is: ' +
				"additionalProperties, const, enum, items, properties, required, type)",
	);
});

test("a nested unknown keyword names its path", () => {
	assert.throws(
		() =>
			defineReadOnlyCommand("run", {
				help: "run",
				payloadSchema: {
					type: "object",
					properties: { a: { type: "string", maxLength: 3 } },
				},
				handler: () => 0,
			}),
		/payload_schema\.properties\["a"\]/,
	);
});

test("a deviating payload fails at the emission seam", async () => {
	const app = createApp({ name: "t", version: "1", help: "t" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			payloadSchema: {
				type: "object",
				properties: { a: { type: "integer" } },
			},
			handler: (_args, ctx) => {
				ctx.payload({ a: "x" });
				return 0;
			},
		}),
	);
	await assert.rejects(
		app.test(["run", "--json"]),
		(err: Error) =>
			err.message ===
			'command "run": payload does not satisfy the declared schema at ' +
				'payload["a"]: expected type "integer", got string',
	);
});

test("human mode never validates the value", async () => {
	// §19.4's call-unconditionally rule: a handler builds its payload the same
	// way in both modes, so a value machine mode could not carry must not fail a
	// run that was never going to emit an envelope.
	const app = createApp({ name: "t", version: "1", help: "t" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			payloadSchema: {
				type: "object",
				properties: { a: { type: "integer" } },
			},
			handler: (_args, ctx) => {
				ctx.payload({ a: "x" });
				return 0;
			},
		}),
	);
	const r = await app.test(["run"]);
	assert.equal(r.exitCode, 0);
});

test("a matching payload rides the envelope", async () => {
	const app = createApp({ name: "t", version: "1", help: "t" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			payloadSchema: {
				type: "object",
				properties: { a: { type: "integer" } },
			},
			handler: (_args, ctx) => {
				ctx.payload({ a: 1 });
				return 0;
			},
		}),
	);
	const r = await app.test(["run", "--json"]);
	assert.equal(r.exitCode, 0);
	assert.deepEqual(JSON.parse(r.stdout).payload, { a: 1 });
});

test("the slot is taken by the first call even when it deviates", async () => {
	// The one-slot rule (§19.4) is a call-time rule and is independent of the
	// value: the second call is refused whatever the first one carried, and the
	// deviating first value is what the emission seam then refuses.
	const app = createApp({ name: "t", version: "1", help: "t" });
	let second = "";
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			payloadSchema: { type: "array" },
			handler: (_args, ctx) => {
				ctx.payload({ a: 1 });
				try {
					ctx.payload([1, 2]);
				} catch (err) {
					second = (err as Error).message;
				}
				return 0;
			},
		}),
	);
	await assert.rejects(app.test(["run", "--json"]), (err: Error) =>
		err.message.includes("does not satisfy"),
	);
	assert.equal(
		second,
		'command "run": ctx.payload was already called ' +
			"(a dispatch carries at most one payload)",
	);
});

// ---------------------------------------------------------------------------
// Builder sugar (contract §19.5, decision 14)
// ---------------------------------------------------------------------------

const buildersPath = join(
	dirname(fileURLToPath(import.meta.url)),
	"..",
	"..",
	"..",
	"go",
	"strictcli",
	"testdata",
	"payload_schema_builders.json",
);

interface BuilderDoc {
	readonly construct_count: number;
	readonly constructs: readonly {
		readonly name: string;
		readonly literal: Record<string, unknown>;
	}[];
}

const builders = JSON.parse(readFileSync(buildersPath, "utf8")) as BuilderDoc;

/** Constructs one fixture entry through the builders. */
function buildConstruct(name: string): Record<string, unknown> {
	switch (name) {
		case "type: one name":
			return schemaType("string");
		case "type: a list for nullability":
			return schemaType("string", "null");
		case "type: every json type":
			return schemaType(
				"array",
				"boolean",
				"integer",
				"null",
				"number",
				"object",
				"string",
			);
		case "array: items":
			return schemaArray(schemaType("integer"));
		case "array: items is itself a built object":
			return schemaArray(
				schemaObject({ properties: { a: schemaType("string") } }),
			);
		case "object: bare":
			return schemaObject();
		case "object: properties only":
			return schemaObject({
				properties: { a: schemaType("string"), b: schemaType("integer") },
			});
		case "object: properties and required":
			return schemaObject({
				properties: { a: schemaType("string") },
				required: ["a"],
			});
		case "object: closed":
			return schemaObject({
				properties: { a: schemaType("string") },
				required: ["a"],
				additionalProperties: false,
			});
		case "object: open by declaration":
			return schemaObject({ additionalProperties: true });
		case "object: a dynamic-key map":
			return schemaObject({ additionalProperties: schemaType("number") });
		case "object: empty required":
			return schemaObject({ required: [] });
		case "enum: strings":
			return schemaEnum("pass", "fail", "warn");
		case "enum: mixed json values":
			return schemaEnum("a", 1, null, true);
		case "const: a scalar":
			return schemaConst("fixed");
		case "const: a composite":
			return schemaConst({ a: [1, 2] });
		default:
			throw new Error(`no builder mapping for fixture ${name}`);
	}
}

test("the builder fixture's count matches its header", () => {
	assert.equal(builders.construct_count, builders.constructs.length);
});

for (const c of builders.constructs) {
	test(`builder construct: ${c.name}`, () => {
		const built = buildConstruct(c.name);
		assert.deepEqual(built, c.literal);
		// One-to-one onto the closed subset: nothing a builder emits is outside
		// the vocabulary, and the result is a legal declaration.
		for (const key of Object.keys(built)) {
			assert.ok(
				(PAYLOAD_SCHEMA_KEYWORDS as readonly string[]).includes(key),
				`builder emitted a keyword outside the subset: ${key}`,
			);
		}
		assert.equal(validatePayloadSchemaLiteral(built), null);
	});
}

test("a builder does not validate on its own", () => {
	// A builder is a constructor, not a check: an illegal type name is a legal
	// literal to build and a registration-time hard error to declare.
	const built = schemaType("strng");
	assert.deepEqual(built, { type: "strng" });
	const found = validatePayloadSchemaLiteral(built);
	assert.ok(found?.detail.startsWith('unknown type "strng"'));
});

test("built schemas are declarable", async () => {
	const app = createApp({ name: "t", version: "1", help: "t" });
	app.command(
		defineReadOnlyCommand("run", {
			help: "run",
			payloadSchema: schemaObject({
				properties: { a: schemaType("integer") },
				required: ["a"],
				additionalProperties: false,
			}),
			handler: (_args, ctx) => {
				ctx.payload({ a: 1 });
				return 0;
			},
		}),
	);
	const r = await app.test(["run", "--json"]);
	assert.equal(r.exitCode, 0);
	assert.deepEqual(JSON.parse(r.stdout).payload, { a: 1 });
});
