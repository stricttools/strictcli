/**
 * Declared runtime requirements: a requirement is declared once, commands
 * reference it, the framework loads it before the handler runs on every door,
 * and a missing one ends the command with one error naming it and how to
 * install it.
 */

import { strict as assert } from "node:assert";
import { test } from "node:test";
import { createApp } from "../src/app.js";
import {
	defineReadOnlyCommand,
	ExitError,
	type Requirement,
	requirement,
} from "../src/index.js";

interface GpuLoader {
	readonly version: string;
}

function vulkan(available: boolean): Requirement<GpuLoader> {
	return requirement({
		name: "vulkan-loader",
		help: "the Vulkan loader library",
		install: "sudo dnf install vulkan-loader",
		load: () => {
			if (!available) {
				throw new Error("libvulkan.so.1: cannot open shared object file");
			}
			return { version: "1.4" };
		},
	});
}

function requirementApp(req: Requirement<GpuLoader>) {
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("render", {
			help: "render a frame",
			requires: [req],
			handler: (_args, ctx) => {
				ctx.out(`vulkan ${ctx.need(req).version}`);
				return 0;
			},
		}),
	);
	app.command(
		defineReadOnlyCommand("plain", {
			help: "needs nothing",
			handler: (_args, ctx) => {
				ctx.out("plain");
				return 0;
			},
		}),
	);
	return app;
}

const MISSING =
	"command 'render' needs vulkan-loader (the Vulkan loader library), which is not available: " +
	"libvulkan.so.1: cannot open shared object file; install it: sudo dnf install vulkan-loader";

test("an available requirement runs the handler with the loaded value", async () => {
	const r = await requirementApp(vulkan(true)).test(["render"]);
	assert.equal(r.exitCode, 0, r.stderr);
	assert.equal(r.stdout, "vulkan 1.4\n");
});

test("a missing requirement ends the command naming the fix", async () => {
	const app = requirementApp(vulkan(false));
	const r = await app.test(["render"]);
	assert.deepEqual(
		[r.exitCode, r.stdout, r.stderr],
		[1, "", `error: ${MISSING}\n`],
	);
	assert.equal((await app.test(["plain"])).stdout, "plain\n");
	// The fix the error names -- the requirement becoming available -- clears it.
	assert.equal(
		(await requirementApp(vulkan(true)).test(["render"])).exitCode,
		0,
	);
});

test("checked in dry run and machine mode", async () => {
	const app = requirementApp(vulkan(false));
	const dry = await app.test(["--dry-run", "render"]);
	assert.equal(dry.exitCode, 1);
	assert.ok(dry.stderr.includes(MISSING));
	const r = await app.test(["--json", "render"]);
	assert.equal(r.exitCode, 1);
	const env = JSON.parse(r.stdout);
	assert.equal(env.command, "render");
	assert.deepEqual(env.diagnostics.at(-1), {
		level: "error",
		message: MISSING,
	});
});

test("checked on the programmatic door", async () => {
	await assert.rejects(
		() => requirementApp(vulkan(false)).call("render", {}),
		(e: unknown) => {
			assert.ok(e instanceof ExitError);
			assert.equal(e.code, 1);
			assert.equal(e.message, MISSING);
			return true;
		},
	);
	await requirementApp(vulkan(true)).call("render", {});
});

test("help never loads it", async () => {
	let loads = 0;
	const req = requirement({
		name: "vulkan-loader",
		help: "the Vulkan loader library",
		install: "sudo dnf install vulkan-loader",
		load: (): GpuLoader => {
			loads++;
			throw new Error("missing");
		},
	});
	const app = requirementApp(req);
	const want =
		"myapp render -- render a frame\n\nRequirements:\n" +
		"  vulkan-loader    the Vulkan loader library; install it: sudo dnf install vulkan-loader\n";
	assert.equal((await app.test(["render", "--help"])).stdout, want);
	assert.equal((await app.test(["help", "render"])).stdout, want);
	assert.equal(loads, 0);
});

test("published in the help document", async () => {
	const doc = JSON.parse(
		(await requirementApp(vulkan(true)).test(["help", "--json"])).stdout,
	);
	assert.deepEqual(doc.commands.render.requires, [
		{
			name: "vulkan-loader",
			help: "the Vulkan loader library",
			install: "sudo dnf install vulkan-loader",
		},
	]);
	assert.equal("requires" in doc.commands.plain, false);
});

const CLAUSE =
	"must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*";

function rejects(fn: () => unknown, message: string): void {
	assert.throws(fn, (e: unknown) => {
		assert.equal((e as Error).message, message);
		return true;
	});
}

test("declaration refusals and their fixes", () => {
	const ok = () => 1;
	const loose = (v: unknown) => v as never;
	rejects(
		() => requirement({ name: "Vulkan", help: "h", install: "i", load: ok }),
		`requirement name "Vulkan" ${CLAUSE}`,
	);
	rejects(
		() => requirement({ name: "vulkan", help: "", install: "i", load: ok }),
		'requirement "vulkan": help must be one non-empty line',
	);
	rejects(
		() =>
			requirement({
				name: "vulkan",
				help: "two\nlines",
				install: "i",
				load: ok,
			}),
		'requirement "vulkan": help must be one non-empty line',
	);
	rejects(
		() => requirement({ name: "vulkan", help: "h", install: " ", load: ok }),
		'requirement "vulkan": install must be one non-empty line saying how to install it',
	);
	rejects(
		() =>
			requirement({
				name: "vulkan",
				help: "h",
				install: "i",
				load: loose(undefined),
			}),
		'requirement "vulkan": load must be a function returning the loaded value or an error',
	);
	const req = requirement({
		name: "vulkan",
		help: "h",
		install: "i",
		load: ok,
	});
	rejects(
		() =>
			defineReadOnlyCommand("render", {
				help: "x",
				requires: [req, req],
				handler: () => 0,
			}),
		'command "render": requirement "vulkan" is referenced twice',
	);
	// Declared once: two commands referencing one value is the idiom ...
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("render", {
			help: "x",
			requires: [req],
			handler: () => 0,
		}),
	);
	app.group("gpu", { help: "g" }).command(
		defineReadOnlyCommand("probe", {
			help: "x",
			requires: [req],
			handler: () => 0,
		}),
	);
	// ... and a second value of the same name is refused.
	const other = requirement({
		name: "vulkan",
		help: "h",
		install: "i",
		load: ok,
	});
	rejects(
		() =>
			app.command(
				defineReadOnlyCommand("bench", {
					help: "x",
					requires: [other],
					handler: () => 0,
				}),
			),
		'requirement "vulkan" is declared by two different values; declare it once and reference that value from every command that needs it',
	);
});

test("need of an undeclared requirement is a hard error", async () => {
	const declared = vulkan(true);
	const undeclared = requirement({
		name: "cuda-runtime",
		help: "the CUDA runtime",
		install: "install the CUDA toolkit",
		load: () => 0,
	});
	const app = createApp({ name: "myapp", version: "1.0.0", help: "test app" });
	app.command(
		defineReadOnlyCommand("render", {
			help: "render a frame",
			requires: [declared],
			handler: (_args, ctx) => {
				ctx.need(undeclared);
				return 0;
			},
		}),
	);
	await assert.rejects(
		() => app.test(["render"]),
		/command 'render' did not declare requirement 'cuda-runtime'; add it to the command's requirements/,
	);
});
