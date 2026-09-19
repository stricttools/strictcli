import { strict as assert } from "node:assert";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import {
	AT_PREFIX_MAX_SIZE,
	newStdinTracker,
	resolveAtPrefix,
} from "../src/atprefix.js";
import { tempDir } from "./helpers.js";

const dir = tempDir("strictcli-atprefix-");

function write(name: string, content: string | Buffer): string {
	const p = join(dir, name);
	writeFileSync(p, content);
	return p;
}

function parseError(message: string): { name: string; message: string } {
	return { name: "ParseError", message };
}

test("non-@ values pass through unchanged", () => {
	const tr = newStdinTracker();
	assert.equal(resolveAtPrefix("msg", "hello", tr), "hello");
	assert.equal(resolveAtPrefix("msg", "", tr), "");
	assert.equal(resolveAtPrefix("msg", "a@b", tr), "a@b");
});

test("@@ escape strips exactly one leading @", () => {
	const tr = newStdinTracker();
	assert.equal(resolveAtPrefix("msg", "@@literal", tr), "@literal");
	// conformance at_prefix.json: @@@ double escape
	assert.equal(resolveAtPrefix("msg", "@@@x", tr), "@@x");
	assert.equal(resolveAtPrefix("msg", "@@", tr), "@");
});

test("@file reads content and trims trailing space/tab/CR/LF only", () => {
	const tr = newStdinTracker();
	const p = write("basic.txt", "file content\n");
	assert.equal(resolveAtPrefix("msg", `@${p}`, tr), "file content");

	const multi = write("multi.txt", "line1\nline2\n\n");
	assert.equal(resolveAtPrefix("msg", `@${multi}`, tr), "line1\nline2");

	const cutset = write("cutset.txt", "payload \t\r\n \t");
	assert.equal(resolveAtPrefix("msg", `@${cutset}`, tr), "payload");

	// Vertical tab is NOT in the trim cutset (space/tab/CR/LF only).
	const vtab = write("vtab.txt", "payload\v\n");
	assert.equal(resolveAtPrefix("msg", `@${vtab}`, tr), "payload\v");

	// Leading whitespace is preserved.
	const lead = write("lead.txt", "  indented\n");
	assert.equal(resolveAtPrefix("msg", `@${lead}`, tr), "  indented");

	const empty = write("empty.txt", "");
	assert.equal(resolveAtPrefix("msg", `@${empty}`, tr), "");
});

test("@file errors: not found, directory, unreadable", () => {
	const tr = newStdinTracker();
	assert.throws(
		() => resolveAtPrefix("msg", "@nonexistent.txt", tr),
		parseError("--msg: file not found: nonexistent.txt"),
	);
	const sub = join(dir, "subdir");
	mkdirSync(sub, { recursive: true });
	assert.throws(
		() => resolveAtPrefix("msg", `@${sub}`, tr),
		parseError(`--msg: cannot read file: ${sub}`),
	);
});

test("@file enforces the 1 MB limit (boundary-exact)", () => {
	const tr = newStdinTracker();
	const atLimit = write("at-limit.bin", Buffer.alloc(AT_PREFIX_MAX_SIZE, 0x61));
	assert.equal(
		resolveAtPrefix("msg", `@${atLimit}`, tr).length,
		AT_PREFIX_MAX_SIZE,
	);
	const overLimit = write(
		"over-limit.bin",
		Buffer.alloc(AT_PREFIX_MAX_SIZE + 1, 0x61),
	);
	assert.throws(
		() => resolveAtPrefix("msg", `@${overLimit}`, tr),
		parseError("--msg: file exceeds 1 MB limit"),
	);
});

test("@- reads stdin once, trims, and records the consumer", () => {
	const tr = newStdinTracker();
	const readStdin = (): Buffer => Buffer.from("from stdin\n");
	assert.equal(resolveAtPrefix("msg", "@-", tr, readStdin), "from stdin");
	assert.equal(tr.consumedBy, "msg");
	assert.throws(
		() => resolveAtPrefix("other", "@-", tr, readStdin),
		parseError("--other: stdin (@-) can only be used once per invocation"),
	);
});

test("@- stdin errors: unreadable and over-limit", () => {
	const failing = (): Buffer => {
		throw new Error("boom");
	};
	assert.throws(
		() => resolveAtPrefix("msg", "@-", newStdinTracker(), failing),
		parseError("--msg: cannot read stdin"),
	);
	const huge = (): Buffer => Buffer.alloc(AT_PREFIX_MAX_SIZE + 1, 0x61);
	const tr = newStdinTracker();
	assert.throws(
		() => resolveAtPrefix("msg", "@-", tr, huge),
		parseError("--msg: file exceeds 1 MB limit"),
	);
	// A failed stdin read does not mark stdin as consumed.
	assert.equal(tr.consumedBy, null);
});

test("stdin-once applies even when the second value is a file", () => {
	// Only @- consumes stdin; files never do.
	const tr = newStdinTracker();
	const p = write("after-stdin.txt", "x\n");
	assert.equal(
		resolveAtPrefix("a", "@-", tr, () => Buffer.from("s")),
		"s",
	);
	assert.equal(resolveAtPrefix("b", `@${p}`, tr), "x");
});
