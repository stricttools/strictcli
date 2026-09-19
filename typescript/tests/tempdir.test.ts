/**
 * The suite's own throwaway directories.
 *
 * A directory made with mkdtempSync lives in the operating system's temp
 * directory, and nothing in the project sweeps that directory later: the run
 * that made it is the only moment at which it can be removed. Every throwaway
 * directory the suite makes therefore goes through tempDir() in helpers.ts,
 * which records what it created and removes all of it before the test process
 * exits -- including when the test that asked for it threw, was skipped
 * part-way, or chdir'd into it and never came back.
 */

import { strict as assert } from "node:assert";
import { execFileSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const TESTS_SRC = fileURLToPath(new URL("../../tests/", import.meta.url));
const COMPILED_HELPERS = fileURLToPath(
	new URL("./helpers.js", import.meta.url),
);

/**
 * helpers.ts owns the one call the ban is stated against, and this file spells
 * the banned call out in the pattern it scans for.
 */
const EXEMPT = new Set(["helpers.ts", "tempdir.test.ts"]);

test("every throwaway directory in the suite is made through tempDir", () => {
	const offenders: string[] = [];
	const names = readdirSync(TESTS_SRC, {
		recursive: true,
	}) as unknown as string[];
	for (const name of names) {
		if (!name.endsWith(".ts") || EXEMPT.has(name)) {
			continue;
		}
		const lines = readFileSync(join(TESTS_SRC, name), "utf8").split("\n");
		lines.forEach((line, index) => {
			if (/\bmkdtemp(Sync)?\s*\(/.test(line)) {
				offenders.push(`${name}:${index + 1}: ${line.trim()}`);
			}
		});
	}
	assert.deepEqual(
		offenders,
		[],
		`these lines make a temp directory the suite never removes; ` +
			`use tempDir() from tests/helpers.ts instead:\n${offenders.join("\n")}`,
	);
});

test("tempDir removes every directory it handed out before the process exits", () => {
	const snippet = [
		`import { tempDir } from ${JSON.stringify(COMPILED_HELPERS)};`,
		`process.stdout.write(`,
		`  [tempDir("sc-tempdir-probe-"), tempDir("sc-tempdir-probe-")].join("\\n"),`,
		`);`,
	].join("\n");
	const out = execFileSync(
		process.execPath,
		["--input-type=module", "-e", snippet],
		{ encoding: "utf8" },
	);
	const dirs = out.trim().split("\n");
	assert.equal(dirs.length, 2);
	assert.notEqual(dirs[0], dirs[1]);
	for (const dir of dirs) {
		assert.ok(dir.length > 0);
		assert.equal(
			existsSync(dir),
			false,
			`tempDir left ${dir} behind after its process exited`,
		);
	}
});
