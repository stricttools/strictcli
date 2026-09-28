/**
 * CLI test-coverage instrumentation: per-process shard files recording which
 * commands app.test() exercised, plus the built-in cli-test-coverage
 * provider. Both live under the directory the app declares through
 * `testCoverageDir`, never under the process's working directory, so a
 * consumer running an installed CLI from anywhere touches nothing. The
 * provider merges the committed manifest and shard files into the covered set
 * and compares it against the app's full command surface.
 *
 * Parity sources: go/strictcli/coverage.go with Python _record_coverage /
 * _collect_all_command_paths / _test_coverage_provider as the divergence
 * ground truth.
 */

import {
	appendFileSync,
	mkdirSync,
	readdirSync,
	readFileSync,
	statSync,
	writeFileSync,
} from "node:fs";
import { dirname, join, resolve } from "node:path";
import type { AppImpl, GroupImpl } from "../app.js";
import type { CheckSpec } from "./provider.js";
import { errorCheckSpec } from "./provider.js";

/**
 * Enables test-coverage instrumentation on an app, from the DECLARED
 * directory: `coverage/` and `test-coverage.json` sit inside it, and both the
 * recorder and the check provider use the absolute paths derived from it, so a
 * chdir between construction and dispatch cannot move either.
 *
 * A declared directory that does not exist leaves the app uninstrumented: no
 * paths, no provider, no writes anywhere. That is the installed distribution,
 * whose declared path names a source checkout that is not there.
 *
 * Only the PATHS are computed here. The coverage/ subdirectory is created
 * lazily by recordCoverage, immediately before the first shard write: shards
 * are written only on the test-harness paths (test() and call()), so a plain
 * CLI invocation leaves no coverage/ behind.
 */
export function initTestCoverage(app: AppImpl, declaredDir: string): void {
	const root = resolve(declaredDir);
	if (!isDir(root)) {
		return;
	}
	app.coverageDir = join(root, "coverage");
	app.coverageManifestPath = join(root, "test-coverage.json");
	// One shard per process (append semantics); uniqueness across concurrent
	// writers comes from the PID, so there is no per-write shard counter.
	app.coverageShardPath = join(app.coverageDir, `${process.pid}.jsonl`);
	app.registerCheckProvider(testCoverageProvider(app));
}

/**
 * Appends a coverage record for the resolved command path. Each test() or
 * call() invocation appends one JSONL line to the per-process shard file.
 */
export function recordCoverage(app: AppImpl, cmdPath: string): void {
	if (app.coverageShardPath === undefined) {
		return;
	}
	mkdirSync(dirname(app.coverageShardPath), { recursive: true });
	appendFileSync(
		app.coverageShardPath,
		`${JSON.stringify({ command: cmdPath })}\n`,
	);
	// A framework-blessed CACHE_WRITE: it executes even in dry mode, is never
	// written to the would-do log, and never trips read-only enforcement.
	app.recordCacheWrite(app.coverageShardPath);
}

/**
 * Enumerates all non-deprecated leaf command paths as dot-separated strings
 * (e.g. "deploy", "infra.deploy").
 */
export function collectAllCommandPaths(app: AppImpl): Set<string> {
	const paths = new Set<string>();
	for (const name of app.commands.keys()) {
		paths.add(name);
	}
	const walkGroup = (grp: GroupImpl, prefix: readonly string[]): void => {
		for (const cmdName of grp.commands.keys()) {
			paths.add([...prefix, cmdName].join("."));
		}
		for (const [subName, subGrp] of grp.groups) {
			walkGroup(subGrp, [...prefix, subName]);
		}
	};
	for (const [groupName, grp] of app.groups) {
		walkGroup(grp, [groupName]);
	}
	return paths;
}

/** True when the path exists and is a regular file. */
function isFile(path: string): boolean {
	try {
		return statSync(path).isFile();
	} catch {
		return false;
	}
}

/** True when the path exists and is a directory. */
function isDir(path: string): boolean {
	try {
		return statSync(path).isDirectory();
	} catch {
		return false;
	}
}

/** Names of shard files (*.jsonl) in the coverage dir, or [] if unreadable. */
function shardNames(coverageDir: string): string[] {
	try {
		return readdirSync(coverageDir).filter((f) => f.endsWith(".jsonl"));
	} catch {
		return [];
	}
}

/**
 * The built-in check provider for cli-test-coverage, auto-registered when the
 * app declares a coverage directory that exists. The verdict is derived from
 * committed state: the covered set is the union of the committed manifest
 * (test-coverage.json in that directory) and any per-process shard files
 * merged from its coverage/ subdirectory. Every live registered command path
 * (minus the injected check command) must be present in that union to pass;
 * otherwise the check fails naming each uncovered command.
 *
 * Because the verdict reads the committed manifest, it is deterministic on
 * every machine -- a machine that never ran the suite (no local shards) still
 * gets a stable verdict from the committed manifest alone.
 *
 * The manifest is rewritten as the monotonic union of its prior contents and
 * the freshly merged shards, but ONLY when that content actually changes -- a
 * pure check must not dirty a byte-identical file. Accepted staleness:
 * deleting a test leaves its command covered in the manifest until the
 * manifest is deliberately regenerated, because the union never removes a
 * command.
 */
export function testCoverageProvider(app: AppImpl): () => CheckSpec[] {
	return () => [
		errorCheckSpec({
			name: "cli-test-coverage",
			tags: ["test"],
			fast: true,
			pure: true,
			needsNetwork: false,
			dependsOn: [],
			impl: (_ctx, reporter) => {
				const coverageDir = app.coverageDir ?? "";
				const manifestPath = app.coverageManifestPath ?? "";

				// Subject-matter gating (the sanctioned skip class, mirroring
				// project-type gating): when the declared coverage root holds
				// NEITHER a committed manifest NOR any shard files, the suite has
				// never run against it. Report a visible SKIP instead of failing
				// with the app's entire command surface listed as uncovered. When
				// EITHER exists, behavior is unchanged: a partial manifest still
				// fails honestly, and an empty-manifest file present still means
				// "coverage configured but empty" = fail listing all.
				const manifestExists = manifestPath !== "" && isFile(manifestPath);
				const shards = coverageDir !== "" ? shardNames(coverageDir) : [];
				if (!manifestExists && shards.length === 0) {
					const anchor =
						manifestPath !== "" ? dirname(manifestPath) : coverageDir;
					return reporter.skipped(
						`no coverage state at ${anchor} -- cli-test-coverage applies to the app's own development tree`,
					);
				}

				const covered = new Set<string>();

				// Seed from the committed manifest -- this is what makes the
				// verdict deterministic on machines that never ran the suite.
				if (manifestExists) {
					try {
						const data: unknown = JSON.parse(
							readFileSync(manifestPath, "utf8"),
						);
						if (Array.isArray(data)) {
							for (const c of data) {
								if (typeof c === "string") {
									covered.add(c);
								}
							}
						}
					} catch {
						// Unreadable/invalid manifest contributes nothing.
					}
				}

				// Merge shards (optional freshness input).
				for (const fname of shards) {
					const content = readFileSync(join(coverageDir, fname), "utf8");
					for (const rawLine of content.split("\n")) {
						const line = rawLine.trim();
						if (line === "") {
							continue;
						}
						const entry = JSON.parse(line) as Record<string, unknown>;
						if (typeof entry.command === "string") {
							covered.add(entry.command);
						}
					}
				}

				// Rewrite the manifest as the monotonic union, but only when the
				// content actually changes (keeps a pure check from dirtying a
				// byte-identical file).
				if (manifestPath !== "" && covered.size > 0) {
					const manifest = [...covered].sort();
					const newContent = `${JSON.stringify(manifest, null, 2)}\n`;
					let existing: string | undefined;
					try {
						existing = readFileSync(manifestPath, "utf8");
					} catch {
						existing = undefined;
					}
					if (existing !== newContent) {
						mkdirSync(dirname(manifestPath), { recursive: true });
						writeFileSync(manifestPath, newContent);
						app.recordCacheWrite(manifestPath);
					}
				}

				// Compare against the command surface (exclude the
				// framework-injected check commands -- they are not user commands).
				const allCommands = collectAllCommandPaths(app);
				allCommands.delete("check");
				allCommands.delete("failing-checks");
				const uncovered = [...allCommands]
					.filter((cmd) => !covered.has(cmd))
					.sort();

				if (uncovered.length > 0) {
					for (const cmd of uncovered) {
						reporter.error(`no test coverage for command: ${cmd}`);
					}
					return reporter.found(
						`${uncovered.length} command(s) with zero test coverage`,
					);
				}
				return reporter.passed(
					`all ${allCommands.size} commands have test coverage`,
				);
			},
		}),
	];
}
