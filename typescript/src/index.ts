/**
 * strictcli public API surface: the TS-native strict CLI entry point
 * re-exporting createApp, the carrier namespace `t`, outcome, and the public
 * types.
 */

export type {
	App,
	AppSpec,
	Group,
	GroupSpec,
	Result,
	RunChecksOptions,
	RunChecksResult,
} from "./app.js";
export { createApp } from "./app.js";
export {
	formatCheckResults,
	formatCheckResultsJSON,
} from "./checks/cmd.js";
export type {
	CheckContext,
	CheckOutcome,
	CheckProblem,
	CheckSeverity,
	CheckStatus,
} from "./checks/framework.js";
export {
	CheckRunResult,
	ErrorReporter,
	WarnReporter,
} from "./checks/framework.js";
export type {
	ErrorCheckSpecInit,
	WarnCheckSpecInit,
} from "./checks/provider.js";
export {
	CheckSpec,
	errorCheckSpec,
	warnCheckSpec,
} from "./checks/provider.js";
export type { ConfigFieldSpec } from "./config.js";
export type {
	ConnectionEnvReader,
	InfraAccess,
	MutatingContext,
	ReadOnlyContext,
	Writer,
} from "./context.js";
export { Context } from "./context.js";
export type {
	Completed,
	Effect,
	EffectKind,
	Forwarding,
	Grant,
	MutatingEffects,
	ReadOnlyEffects,
	Response,
	Spawned,
} from "./effects.js";
export type { ElectedRecord } from "./elected.js";
export { assertNever, provided } from "./elected.js";
// ParseError and RegistrationError stay internal (sibling parity: Python's
// __all__ and Go both export only InvokeError -- registration failures are
// Go panics / Python ValueError, parse failures print to stderr and exit 1).
export { EffectFailed, InvokeError } from "./errors.js";
export { ExitError, ExitNow } from "./exits.js";
export type {
	AllOrNone,
	AnyArg,
	AnyChoice,
	AnyChoiceFlag,
	AnyCommand,
	AnyDecl,
	AnyFlag,
	AnyFlagSet,
	ArgDef,
	ArgOpts,
	AtLeastOne,
	ChoiceDef,
	ChoiceFlagDef,
	ChoiceFlagOpts,
	ChoiceMap,
	ChoiceRecord,
	CommandDef,
	ConflictMode,
	Constraint,
	ConstraintMember,
	ConstraintMembers,
	DeprecatedDef,
	ElectBy,
	ElementOf,
	FlagDef,
	FlagMap,
	FlagOpts,
	FlagSet,
	Handler,
	Implies,
	MutatingCommandSpec,
	PassthroughArgs,
	PassthroughDef,
	PassthroughHandler,
	ReadOnlyCommandSpec,
	Requires,
	UpdateOf,
	ValueChoiceDef,
	When,
	WriteMode,
} from "./factories.js";
export {
	allOrNone,
	arg,
	atLeastOne,
	choice,
	choiceFlag,
	defineMutatingCommand,
	defineReadOnlyCommand,
	deprecated,
	flag,
	flagSet,
	implies,
	memberChoiceFlag,
	mutatingPassthrough,
	readOnlyPassthrough,
	requires,
} from "./factories.js";
export type {
	ChoiceOf,
	Elected,
	ElectedOf,
	HandlerArgs,
	InferHandler,
	InferHandlerArgs,
	InferScopeArgs,
} from "./infer.js";
export type { InfraRootPath } from "./infra.js";
export { relativeToRoot } from "./infra.js";
export type { CallOptions } from "./invoke.js";
export type { McpIO } from "./mcp.js";
export type { Outcome } from "./outcome.js";
export { outcome } from "./outcome.js";
export type { SchemaObjectOpts } from "./payload_schema.js";
export {
	schemaArray,
	schemaConst,
	schemaEnum,
	schemaObject,
	schemaType,
} from "./payload_schema.js";
export type { Tool } from "./tool.js";
export type {
	Carrier,
	DictSchema,
	ElemSchema,
	HandlerResult,
	HandlerReturn,
	ListSchema,
	ScalarSchema,
	Schema,
} from "./types.js";
export { t } from "./types.js";
// VERSION is generated from package.json by scripts/gen-version.mjs on every
// build (the `prebuild` hook, inherited by `prepack`), so the published
// constant cannot drift from the published package.
export { VERSION } from "./version.js";
