/**
 * describe.ts -- dev-only self-dump of the TS public API surface, shape-
 * aligned with the conformance/describe_go/main.go reference dumper's output.
 * Sections cover schema_version, package, structs, option_constructors,
 * functions, methods, and constants.
 * TypeScript has no runtime type info, so instead of reflection the surface
 * is a hand-maintained registry (SURFACE below) whose accuracy is enforced
 * by tests/describe.test.ts in both directions: every listed name must exist
 * on the real exports (runtime typeof checks plus compile-time keyof
 * equality assertions), and every src/index.ts export must be listed.
 *
 * TS analogs of the Go dumper's sections:
 * - structs: member lists of exported interfaces/classes that carry data
 *   (spec/option object types owned by a factory live under that factory's
 *   option_keys instead of here).
 * - option_constructors: factories that take an options/spec object, with
 *   the object's keys; flag/arg additionally record per-carrier
 *   applicability (never-typed keys are inexpressible for that carrier).
 * - functions: exported value functions that take only positional args.
 * - methods: receiver + name for interface/class method surfaces.
 * - constants: exported non-function values.
 * - classes / types / check_system: TS-only sections. classes are exported
 *   class values, types are the type-only index.ts exports (no Go analog --
 *   Go types are all values of the AST), check_system is the flat list of
 *   check-system public names.
 *
 * This module is intentionally NOT exported through index.ts: it is dev
 * tooling, not public API. Bin-style usage: node dist/describe.js
 */

import { pathToFileURL } from "node:url";

export const SCHEMA_VERSION = 1;

/**
 * The single source of truth for the TS public API surface. Set-like name
 * lists (option_keys, per_carrier, functions, classes, types, check_system)
 * are kept alphabetically sorted; struct member lists are in declaration
 * order (mirroring the Go dumper, which sorts every list except struct
 * fields). Phantom type-only members that never exist at runtime (_out) are
 * listed because keyof sees them; tests filter them for runtime key checks.
 */
export const SURFACE = {
	schema_version: SCHEMA_VERSION,
	package: "strictcli",

	structs: [
		{ name: "App", members: ["name", "version", "help"] },
		{ name: "Group", members: ["name", "help", "tags", "hidden"] },
		{ name: "GroupSpec", members: ["help", "tags", "hidden"] },
		{ name: "Result", members: ["stdout", "stderr", "exitCode", "data"] },
		{
			name: "RunChecksOptions",
			members: ["tagExpr", "nameGlob", "runAll", "ignoreWarnings", "pureOnly"],
		},
		{
			name: "RunChecksResult",
			members: ["results", "impureListed", "exitCode"],
		},
		{
			// The command carrier built by the twin factories.
			name: "CommandDef",
			members: [
				"kind",
				"name",
				"help",
				"effect",
				"consequential",
				"dryRunSupported",
				"dryRunUnsupportedReason",
				"updateOf",
				"payloadSchema",
				"ownsStdout",
				"flags",
				"args",
				"flagSets",
				"constraints",
				"allDecls",
				"allFlags",
				"handler",
				"tags",
				"hidden",
				"interactive",
				"configFields",
				"grants",
				"forwarding",
			],
		},
		{
			name: "FlagDef",
			members: ["kind", "name", "schema", "carrier", "opts", "_out"],
		},
		{
			name: "ArgDef",
			members: ["kind", "name", "schema", "carrier", "opts", "_out"],
		},
		{ name: "FlagSet", members: ["kind", "name", "flags"] },
		{
			// The scoped-selector construct (§24): a flag whose type is its set
			// of choices, each of which owns a scope.
			name: "ChoiceFlagDef",
			members: ["kind", "electBy", "name", "choices", "opts"],
		},
		{ name: "ChoiceDef", members: ["kind", "help", "flags", "short"] },
		{ name: "ValueChoiceDef", members: ["kind", "help", "flags", "value"] },
		{ name: "AtLeastOne", members: ["kind", "name", "members"] },
		{ name: "AllOrNone", members: ["kind", "name", "members"] },
		{ name: "Requires", members: ["kind", "name", "flag", "dependsOn"] },
		{
			name: "Implies",
			members: ["kind", "name", "flag", "implies", "value"],
		},
		{
			name: "PassthroughDef",
			members: [
				"kind",
				"name",
				"help",
				"effect",
				"consequential",
				"dryRunSupported",
				"dryRunUnsupportedReason",
				"updateOf",
				"payloadSchema",
				"ownsStdout",
				"handler",
				"tags",
				"hidden",
				"grants",
			],
		},
		// The effects regime: the three result shapes, the two declaration
		// records, the two classification-narrowed contexts and their handles.
		{ name: "Completed", members: ["exitCode", "stdout", "stderr"] },
		{ name: "Response", members: ["status", "body", "headers"] },
		{ name: "Spawned", members: ["pid", "wait"] },
		{ name: "Grant", members: ["name", "reason", "kind"] },
		{ name: "Forwarding", members: ["reason"] },
		{ name: "ReadOnlyEffects", members: ["run", "recorded", "renderLog"] },
		{
			name: "MutatingEffects",
			members: [
				"run",
				"spawn",
				"write",
				"mkdir",
				"remove",
				"rename",
				"chmod",
				"http",
				"recorded",
				"renderLog",
			],
		},
		{
			name: "ReadOnlyContext",
			members: [
				"dryRun",
				"approveConsequential",
				"quiet",
				"verbose",
				"info",
				"warn",
				"debug",
				"error",
				"source",
				"infraValue",
				"connectionEnvValue",
				"effects",
			],
		},
		{
			name: "MutatingContext",
			members: [
				"dryRun",
				"approveConsequential",
				"quiet",
				"verbose",
				"info",
				"warn",
				"debug",
				"error",
				"source",
				"infraValue",
				"connectionEnvValue",
				"effects",
			],
		},
		{ name: "PassthroughArgs", members: ["name", "args", "globals"] },
		{ name: "DeprecatedDef", members: ["kind", "name", "message"] },
		{ name: "Outcome", members: ["exitCode"] },
		{ name: "Carrier", members: ["_out", "schema", "parse", "elem"] },
		{
			name: "Tool",
			members: [
				"name",
				"description",
				"parameters",
				"effect",
				"consequential",
				"execute",
			],
		},
		{ name: "ConfigFieldSpec", members: ["type", "help", "default"] },
		{
			name: "InfraAccess",
			members: ["roots", "handshakes", "connections", "hermetic"],
		},
		{ name: "Writer", members: ["write"] },
		{ name: "InfraRootPath", members: ["envVar", "parts"] },
		{ name: "McpIO", members: ["input", "output"] },
		{ name: "CallOptions", members: ["approveConsequential"] },
		{ name: "CheckContext", members: ["projectRoot"] },
		{
			name: "ConnectionEnvReader",
			members: ["connectionEnvValue", "isHermetic"],
		},
		{ name: "CheckProblem", members: ["severity", "text"] },
		{
			name: "CheckOutcome",
			members: ["kind", "message", "problems", "notes"],
		},
		{
			// gated()/warned() are under methods; these are fields and getters.
			name: "CheckRunResult",
			members: [
				"name",
				"outcome",
				"durationMs",
				"status",
				"message",
				"problems",
				"notes",
			],
		},
		{
			name: "CheckSpec",
			members: [
				"name",
				"tags",
				"severity",
				"fast",
				"pure",
				"needsNetwork",
				"dependsOn",
				"scope",
				"impl",
				"implForm",
			],
		},
	],

	option_constructors: [
		{
			name: "flag",
			options_type: "FlagOpts",
			option_keys: [
				"choices",
				"conflictMode",
				"connectionEnv",
				"connectionUrl",
				"default",
				"env",
				"envSeparator",
				"help",
				"negatable",
				"nullable",
				"prefixed",
				"presence",
				"repeatable",
				"retiredChoices",
				"short",
				"unique",
				"validate",
			],
			// Keys expressible per carrier kind (never-typed keys excluded).
			// scalar = str/int/float.
			per_carrier: {
				bool: [
					"conflictMode",
					"connectionEnv",
					"connectionUrl",
					"default",
					"env",
					"help",
					"negatable",
					"nullable",
					"prefixed",
					"presence",
					"short",
					"validate",
				],
				scalar: [
					"choices",
					"conflictMode",
					"connectionEnv",
					"connectionUrl",
					"default",
					"env",
					"help",
					"nullable",
					"prefixed",
					"presence",
					"retiredChoices",
					"short",
					"validate",
				],
				list: [
					"choices",
					"conflictMode",
					"connectionEnv",
					"connectionUrl",
					"default",
					"env",
					"envSeparator",
					"help",
					"nullable",
					"prefixed",
					"presence",
					"repeatable",
					"retiredChoices",
					"short",
					"unique",
					"validate",
				],
				dict: [
					"conflictMode",
					"connectionEnv",
					"connectionUrl",
					"default",
					"env",
					"help",
					"nullable",
					"prefixed",
					"presence",
					"short",
					"validate",
				],
			},
		},
		{
			name: "arg",
			options_type: "ArgOpts",
			option_keys: [
				"choices",
				"default",
				"help",
				"presence",
				"retiredChoices",
				"variadic",
			],
			// scalar = str/int/float; bool args cannot take choices, and a
			// retired choice is a value of the same set.
			per_carrier: {
				bool: ["default", "help", "presence", "variadic"],
				scalar: [
					"choices",
					"default",
					"help",
					"presence",
					"retiredChoices",
					"variadic",
				],
			},
		},
		{
			name: "defineReadOnlyCommand",
			options_type: "ReadOnlyCommandSpec",
			option_keys: [
				"args",
				"configFields",
				"consequential",
				"constraints",
				"dryRunSupported",
				"dryRunUnsupportedReason",
				"flagSets",
				"flags",
				"forwarding",
				"grants",
				"handler",
				"help",
				"hidden",
				"interactive",
				"ownsStdout",
				"payloadSchema",
				"tags",
				"updateOf",
			],
		},
		{
			name: "defineMutatingCommand",
			options_type: "MutatingCommandSpec",
			option_keys: [
				"args",
				"configFields",
				"consequential",
				"constraints",
				"dryRunSupported",
				"dryRunUnsupportedReason",
				"flagSets",
				"flags",
				"forwarding",
				"grants",
				"handler",
				"help",
				"hidden",
				"interactive",
				"ownsStdout",
				"payloadSchema",
				"tags",
				"updateOf",
			],
		},
		{
			name: "createApp",
			options_type: "AppSpec",
			option_keys: [
				"checksEmbed",
				"checksPath",
				"config",
				"configConflictMode",
				"configFormat",
				"configPath",
				"connectionEnv",
				"envPrefix",
				"flags",
				"handshakeEnv",
				"help",
				"infraRoot",
				"name",
				"noDefaultConfigPath",
				"procObserveAllowlist",
				"schemaPath",
				"testCoverageDir",
				"version",
			],
		},
		{
			name: "readOnlyPassthrough",
			options_type: "(inline spec)",
			option_keys: [
				"consequential",
				"dryRunSupported",
				"dryRunUnsupportedReason",
				"grants",
				"handler",
				"help",
				"hidden",
				"ownsStdout",
				"payloadSchema",
				"tags",
				"updateOf",
			],
		},
		{
			name: "mutatingPassthrough",
			options_type: "(inline spec)",
			option_keys: [
				"consequential",
				"dryRunSupported",
				"dryRunUnsupportedReason",
				"grants",
				"handler",
				"help",
				"hidden",
				"ownsStdout",
				"payloadSchema",
				"tags",
				"updateOf",
			],
		},
		{
			name: "atLeastOne",
			options_type: "(inline spec)",
			option_keys: ["members", "name"],
		},
		{
			name: "allOrNone",
			options_type: "(inline spec)",
			option_keys: ["members", "name"],
		},
		{
			name: "requires",
			options_type: "(inline spec)",
			option_keys: ["dependsOn", "flag", "name"],
		},
		{
			name: "implies",
			options_type: "(inline spec)",
			option_keys: ["flag", "implies", "name", "value"],
		},
		{
			name: "errorCheckSpec",
			options_type: "ErrorCheckSpecInit",
			option_keys: [
				"dependsOn",
				"fast",
				"impl",
				"name",
				"needsNetwork",
				"pure",
				"scope",
				"severity",
				"tags",
			],
		},
		{
			name: "warnCheckSpec",
			options_type: "WarnCheckSpecInit",
			option_keys: [
				"dependsOn",
				"fast",
				"impl",
				"name",
				"needsNetwork",
				"pure",
				"scope",
				"severity",
				"tags",
			],
		},
	],

	functions: [
		"assertNever",
		"choice",
		"choiceFlag",
		"deprecated",
		"flagSet",
		"formatCheckResults",
		"formatCheckResultsJSON",
		"memberChoiceFlag",
		"outcome",
		"provided",
		"relativeToRoot",
		// The payload-schema builder sugar (§19.5, decision 14): pure
		// constructors of literals, one per subset keyword shape.
		"schemaArray",
		"schemaConst",
		"schemaEnum",
		"schemaObject",
		"schemaType",
	],

	methods: [
		{ receiver: "App", name: "command" },
		{ receiver: "App", name: "group" },
		{ receiver: "App", name: "deprecate" },
		{ receiver: "App", name: "configField" },
		{ receiver: "App", name: "tagContract" },
		{ receiver: "App", name: "errorCheck" },
		{ receiver: "App", name: "warnCheck" },
		{ receiver: "App", name: "setCheckContext" },
		{ receiver: "App", name: "registerCheckProvider" },
		{ receiver: "App", name: "resetCheckProviderCache" },
		{ receiver: "App", name: "runChecks" },
		{ receiver: "App", name: "dumpSchemaDict" },
		{ receiver: "App", name: "call" },
		{ receiver: "App", name: "jsonSchema" },
		{ receiver: "App", name: "asTools" },
		{ receiver: "App", name: "serveMcp" },
		{ receiver: "App", name: "run" },
		{ receiver: "App", name: "test" },
		{ receiver: "App", name: "effectLog" },
		{ receiver: "Group", name: "command" },
		{ receiver: "Group", name: "group" },
		{ receiver: "Group", name: "deprecate" },
		// The reserved quartet and the effects handle are prototype accessors
		// (Go spells them as methods: ctx.DryRun(), ctx.Effects()).
		{ receiver: "Context", name: "dryRun" },
		{ receiver: "Context", name: "approveConsequential" },
		{ receiver: "Context", name: "quiet" },
		{ receiver: "Context", name: "verbose" },
		{ receiver: "Context", name: "json" },
		{ receiver: "Context", name: "payload" },
		{ receiver: "Context", name: "effects" },
		{ receiver: "Context", name: "info" },
		{ receiver: "Context", name: "warn" },
		{ receiver: "Context", name: "debug" },
		{ receiver: "Context", name: "error" },
		{ receiver: "Context", name: "source" },
		{ receiver: "Context", name: "provided" },
		{ receiver: "Context", name: "unset" },
		{ receiver: "Context", name: "infraValue" },
		{ receiver: "Context", name: "connectionEnvValue" },
		{ receiver: "ErrorReporter", name: "note" },
		{ receiver: "ErrorReporter", name: "warn" },
		{ receiver: "ErrorReporter", name: "error" },
		{ receiver: "ErrorReporter", name: "passed" },
		{ receiver: "ErrorReporter", name: "skipped" },
		{ receiver: "ErrorReporter", name: "found" },
		{ receiver: "WarnReporter", name: "note" },
		{ receiver: "WarnReporter", name: "warn" },
		{ receiver: "WarnReporter", name: "passed" },
		{ receiver: "WarnReporter", name: "skipped" },
		{ receiver: "WarnReporter", name: "found" },
		{ receiver: "CheckRunResult", name: "gated" },
		{ receiver: "CheckRunResult", name: "warned" },
	],

	constants: [
		{ name: "VERSION", type: "string" },
		{ name: "t", type: "object" },
	],

	classes: [
		"CheckRunResult",
		"CheckSpec",
		"Context",
		"EffectFailed",
		"ErrorReporter",
		"InvokeError",
		"WarnReporter",
	],

	types: [
		"AllOrNone",
		"AnyArg",
		"AnyCommand",
		"AnyFlag",
		"AnyChoice",
		"AnyChoiceFlag",
		"AnyDecl",
		"AnyFlagSet",
		"App",
		"AppSpec",
		"ArgDef",
		"ArgOpts",
		"AtLeastOne",
		"CallOptions",
		"Carrier",
		"CheckContext",
		"CheckOutcome",
		"CheckProblem",
		"CheckSeverity",
		"CheckStatus",
		"ChoiceDef",
		"ChoiceFlagDef",
		"ChoiceFlagOpts",
		"ChoiceMap",
		"ChoiceOf",
		"ChoiceRecord",
		"Completed",
		"CommandDef",
		"ConfigFieldSpec",
		"ConflictMode",
		"ConnectionEnvReader",
		"Constraint",
		"ConstraintMember",
		"ConstraintMembers",
		"DeprecatedDef",
		"DictSchema",
		"ElemSchema",
		"Effect",
		"EffectKind",
		"ElectBy",
		"Elected",
		"ElectedOf",
		"ElectedRecord",
		"ElementOf",
		"ErrorCheckSpecInit",
		"FlagDef",
		"FlagMap",
		"FlagOpts",
		"FlagSet",
		"Forwarding",
		"Grant",
		"Group",
		"GroupSpec",
		"Handler",
		"HandlerArgs",
		"HandlerResult",
		"HandlerReturn",
		"Implies",
		"InferHandler",
		"InferHandlerArgs",
		"InferScopeArgs",
		"InfraAccess",
		"InfraRootPath",
		"ListSchema",
		"McpIO",
		"MutatingCommandSpec",
		"MutatingContext",
		"MutatingEffects",
		"Outcome",
		"PassthroughArgs",
		"PassthroughDef",
		"PassthroughHandler",
		"ReadOnlyCommandSpec",
		"ReadOnlyContext",
		"ReadOnlyEffects",
		"Requires",
		"UpdateOf",
		"ValueChoiceDef",
		"When",
		"WriteMode",
		"Response",
		"Result",
		"RunChecksOptions",
		"RunChecksResult",
		"ScalarSchema",
		"Schema",
		"SchemaObjectOpts",
		"Spawned",
		"Tool",
		"WarnCheckSpecInit",
		"Writer",
	],

	check_system: [
		"CheckContext",
		"CheckOutcome",
		"CheckProblem",
		"CheckRunResult",
		"CheckSeverity",
		"CheckSpec",
		"CheckStatus",
		"ErrorCheckSpecInit",
		"ErrorReporter",
		"RunChecksOptions",
		"RunChecksResult",
		"WarnCheckSpecInit",
		"WarnReporter",
		"errorCheckSpec",
		"formatCheckResults",
		"formatCheckResultsJSON",
		"warnCheckSpec",
	],
} as const;

/** JSON dump shape (the plain-object mirror of SURFACE, deterministically sorted). */
export interface SurfaceDump {
	schema_version: number;
	package: string;
	structs: { name: string; members: string[] }[];
	option_constructors: {
		name: string;
		options_type: string;
		option_keys: string[];
		per_carrier?: Record<string, string[]>;
	}[];
	functions: string[];
	methods: { receiver: string; name: string }[];
	constants: { name: string; type: string }[];
	classes: string[];
	types: string[];
	check_system: string[];
}

function cmp(a: string, b: string): number {
	return a < b ? -1 : a > b ? 1 : 0;
}

/**
 * Returns the surface as a deterministically-sorted plain object, applying
 * the Go dumper's sort discipline: every list is sorted by name before
 * emission EXCEPT struct member lists, which retain declaration order.
 */
export function describeSurface(): SurfaceDump {
	return {
		schema_version: SURFACE.schema_version,
		package: SURFACE.package,
		structs: SURFACE.structs
			.map((s) => ({ name: s.name, members: [...s.members] }))
			.sort((a, b) => cmp(a.name, b.name)),
		option_constructors: SURFACE.option_constructors
			.map((c) => {
				const entry: SurfaceDump["option_constructors"][number] = {
					name: c.name,
					options_type: c.options_type,
					option_keys: [...c.option_keys].sort(cmp),
				};
				if ("per_carrier" in c) {
					const pc: Record<string, string[]> = {};
					for (const [kind, keys] of Object.entries(c.per_carrier)) {
						pc[kind] = [...keys].sort(cmp);
					}
					entry.per_carrier = pc;
				}
				return entry;
			})
			.sort((a, b) => cmp(a.name, b.name)),
		functions: [...SURFACE.functions].sort(cmp),
		methods: SURFACE.methods
			.map((m) => ({ receiver: m.receiver, name: m.name }))
			.sort((a, b) => cmp(a.receiver, b.receiver) || cmp(a.name, b.name)),
		constants: SURFACE.constants
			.map((c) => ({ name: c.name, type: c.type }))
			.sort((a, b) => cmp(a.name, b.name)),
		classes: [...SURFACE.classes].sort(cmp),
		types: [...SURFACE.types].sort(cmp),
		check_system: [...SURFACE.check_system].sort(cmp),
	};
}

/** The dump as pretty-printed JSON with a trailing newline (bin output). */
export function describeSurfaceJson(): string {
	return `${JSON.stringify(describeSurface(), null, 2)}\n`;
}

// Bin-style entry: `node dist/describe.js` prints the surface JSON. The
// guard keeps imports silent (test runner, library consumers).
if (
	process.argv[1] !== undefined &&
	import.meta.url === pathToFileURL(process.argv[1]).href
) {
	process.stdout.write(describeSurfaceJson());
}
