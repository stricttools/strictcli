/**
 * MCP (Model Context Protocol) server: a line-delimited JSON-RPC 2.0 loop
 * over stdin/stdout handling server/discover, tools/list and tools/call under
 * protocol 2026-07-28, plus the retained initialize handshake of the era
 * before it.
 * Triggered by the reserved --mcp global flag (position-aware pre-scan in
 * parse.ts); test() rejects it with the interactive-stdin message instead.
 *
 * Parity: Go mcp.go supplies the canonical error strings ("Parse error",
 * "Method not found: <m>", the three -32602 messages); Python
 * _run_mcp_server is the ground truth for behavior where the siblings once
 * diverged -- a tool name is the dotted command path (Go's underscore
 * mangling and its guessing reverse lookup are deleted), and a non-object
 * JSON line is a -32700 parse error exactly like malformed JSON.
 */

import {
	createHash,
	createHmac,
	randomBytes,
	timingSafeEqual,
} from "node:crypto";
import { createInterface } from "node:readline";
import type { AppImpl } from "./app.js";
import type { Writer } from "./context.js";
import { errConfirmDeclined } from "./errors.js";
import { formatFloatCanonical } from "./float.js";
import { commandClassification } from "./invoke.js";
import { jsonCompact } from "./outcome.js";
import {
	buildJSONSchema,
	collectToolCommands,
	flatToCallKwargs,
	toolDescription,
} from "./tool.js";

/** Optional stream overrides for serveMcp (defaults: process stdin/stdout). */
export interface McpIO {
	readonly input?: NodeJS.ReadableStream;
	readonly output?: Writer;
}

// JSON-RPC and MCP error codes. -32020 (HeaderMismatch) belongs to the HTTP
// transport, which this server does not speak; -32021
// (MissingRequiredClientCapability) is what a consequential call from a client
// that cannot render the confirmation is answered with.
const MCP_ERR_PARSE = -32700;
const MCP_ERR_METHOD_NOT_FOUND = -32601;
const MCP_ERR_INVALID_PARAMS = -32602;
const MCP_ERR_MISSING_CLIENT_CAPABILITY = -32021;
const MCP_ERR_UNSUPPORTED_PROTOCOL_VERSION = -32022;

// The revision forbids a server sending an input request the client never said
// it could fulfil, and assigns -32021 for saying so. `data.requiredCapabilities`
// is a client-capabilities object, and this server names FORM mode: a client
// that declared only URL-mode elicitation cannot render this question either.
const MCP_MSG_MISSING_ELICITATION =
	"Server requires the elicitation capability for this request";

function requiredElicitationCapabilities(): JsonObject {
	return { elicitation: { form: {} } };
}

// The server speaks two eras (effects contract §22):
//
//   MODERN (2026-07-28) -- stateless. There is no handshake: every request
//   carries its protocol version and the client's capabilities in `_meta`,
//   every result carries a `resultType`, and `server/discover` advertises the
//   supported versions, the capabilities and the server identity that the
//   handshake used to carry.
//
//   LEGACY (2025-11-25) -- the `initialize` handshake, the newest of the
//   handshake-based revisions. It is selected by an `initialize` request and
//   scoped to this process, which is exactly the dual-era rule the modern
//   revision specifies for a server serving both. That era has no
//   input-required result, so the same confirmation is delivered as a
//   server-initiated `elicitation/create` request (contract §22.7).
//
// A request that carries neither the modern metadata nor a preceding
// `initialize` is malformed and is refused. Nothing is inferred.
const MCP_PROTOCOL_VERSION = "2026-07-28";
const MCP_LEGACY_PROTOCOL_VERSION = "2025-11-25";

// The reserved `_meta` keys of the modern revision.
const MCP_META_PROTOCOL_VERSION = "io.modelcontextprotocol/protocolVersion";
const MCP_META_CLIENT_CAPABILITIES =
	"io.modelcontextprotocol/clientCapabilities";
const MCP_META_CLIENT_INFO = "io.modelcontextprotocol/clientInfo";
const MCP_META_SERVER_INFO = "io.modelcontextprotocol/serverInfo";
const MCP_META_LOG_LEVEL = "io.modelcontextprotocol/logLevel";
const MCP_META_SUBSCRIPTION_ID = "io.modelcontextprotocol/subscriptionId";

// A key under a prefix the protocol reserves for itself is either one this
// revision defines or one this server does not speak; the second is refused
// rather than ignored.
const MCP_RECOGNIZED_RESERVED_META_KEYS = new Set([
	MCP_META_PROTOCOL_VERSION,
	MCP_META_CLIENT_CAPABILITIES,
	MCP_META_CLIENT_INFO,
	MCP_META_LOG_LEVEL,
	MCP_META_SUBSCRIPTION_ID,
]);

// The named feature the server declares. A NAME, never a
// version number: a new name appears only if the confirmation dance changes
// incompatibly.
const MCP_FEATURE_CONSEQUENTIAL_CONFIRMATION =
	"dev.smmh.strictcli/consequential-confirmation";

// Cacheability of the list surfaces. The tool list is derived from the app's
// static command registration, so it cannot vary per client (public) and cannot
// change while the process runs.
const MCP_CACHE_TTL_MS = 3600000;
const MCP_CACHE_SCOPE = "public";

const MCP_META_PREFIX_LABEL_RE = /^[A-Za-z](?:[A-Za-z0-9-]*[A-Za-z0-9])?$/;
const MCP_META_NAME_RE = /^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$/;

type JsonObject = Record<string, unknown>;

function isPlainObject(v: unknown): v is JsonObject {
	return typeof v === "object" && v !== null && !Array.isArray(v);
}

/**
 * True when `key` matches the protocol's `_meta` key-name grammar: an optional
 * dot-separated prefix, a slash, and a name. The name may be empty; when it is
 * not, it begins and ends alphanumeric and may carry hyphens, underscores and
 * dots in between.
 */
function metaKeyValid(key: string): boolean {
	const parts = key.split("/");
	let name: string;
	if (parts.length === 1) {
		name = parts[0] as string;
	} else if (parts.length === 2) {
		for (const label of (parts[0] as string).split(".")) {
			if (!MCP_META_PREFIX_LABEL_RE.test(label)) {
				return false;
			}
		}
		name = parts[1] as string;
	} else {
		return false;
	}
	return name === "" || MCP_META_NAME_RE.test(name);
}

/**
 * True when `key` sits under a prefix the protocol reserves for itself: any
 * prefix whose SECOND label is `modelcontextprotocol` or `mcp`. So
 * `io.modelcontextprotocol/` and `com.mcp.tools/` are reserved and
 * `com.example.mcp/` is not.
 */
function metaKeyReserved(key: string): boolean {
	const parts = key.split("/");
	if (parts.length !== 2) {
		return false;
	}
	const labels = (parts[0] as string).split(".");
	return (
		labels.length >= 2 &&
		(labels[1] === "modelcontextprotocol" || labels[1] === "mcp")
	);
}

/**
 * Validate one request's `_meta` block; return the refusal, or undefined.
 * Called only once the block is known to carry the protocol version, which is
 * what selects the modern era in the first place.
 */
/** Sorts strings by their UTF-8 bytes, the order the siblings sort in. */
function sortedByUtf8(keys: string[]): string[] {
	return [...keys].sort((left, right) =>
		Buffer.compare(Buffer.from(left, "utf8"), Buffer.from(right, "utf8")),
	);
}

function validateMeta(meta: JsonObject): string | undefined {
	// Sorted, not in the document's own order: a request carrying more than one
	// offending key must be refused by naming the SAME key in all three
	// implementations (§22.2), and Go has to sort because its map iteration is
	// randomized. The comparison is over UTF-8 bytes, which is what Go's
	// sort.Strings and Python's code-point sort both give -- JavaScript's
	// default sort compares UTF-16 code units, which differs above the BMP.
	for (const key of sortedByUtf8(Object.keys(meta))) {
		if (!metaKeyValid(key)) {
			return `invalid _meta key name: '${key}'`;
		}
		if (metaKeyReserved(key) && !MCP_RECOGNIZED_RESERVED_META_KEYS.has(key)) {
			return `unrecognized reserved _meta key: '${key}'`;
		}
	}
	if (typeof meta[MCP_META_PROTOCOL_VERSION] !== "string") {
		return `_meta['${MCP_META_PROTOCOL_VERSION}'] must be a string`;
	}
	if (!Object.hasOwn(meta, MCP_META_CLIENT_CAPABILITIES)) {
		return `missing required request metadata: _meta['${MCP_META_CLIENT_CAPABILITIES}']`;
	}
	if (!isPlainObject(meta[MCP_META_CLIENT_CAPABILITIES])) {
		return `_meta['${MCP_META_CLIENT_CAPABILITIES}'] must be an object`;
	}
	if (
		Object.hasOwn(meta, MCP_META_CLIENT_INFO) &&
		!isPlainObject(meta[MCP_META_CLIENT_INFO])
	) {
		return `_meta['${MCP_META_CLIENT_INFO}'] must be an object`;
	}
	return undefined;
}

function jsonrpcError(
	reqId: unknown,
	code: number,
	message: string,
	data?: unknown,
): JsonObject {
	const error: JsonObject = { code, message };
	if (data !== undefined) {
		error.data = data;
	}
	return { jsonrpc: "2.0", id: reqId ?? null, error };
}

function jsonrpcResult(reqId: unknown, result: unknown): JsonObject {
	return { jsonrpc: "2.0", id: reqId ?? null, result };
}

/** The identity every modern result carries in its own `_meta`. */
function serverInfoMeta(app: AppImpl): JsonObject {
	return {
		[MCP_META_SERVER_INFO]: { name: app.name, version: app.version },
	};
}

/** Wrap a modern result body: `resultType` in front, server identity behind. */
function completeResult(
	app: AppImpl,
	reqId: unknown,
	body: JsonObject,
): JsonObject {
	return jsonrpcResult(reqId, {
		resultType: "complete",
		...body,
		_meta: serverInfoMeta(app),
	});
}

/** A tool result -- the one place the two eras' shapes differ. */
function toolResult(
	app: AppImpl,
	reqId: unknown,
	text: string,
	modern: boolean,
	isError = false,
): JsonObject {
	const body: JsonObject = { content: [{ type: "text", text }] };
	if (isError) {
		body.isError = true;
	}
	return modern ? completeResult(app, reqId, body) : jsonrpcResult(reqId, body);
}

/**
 * What the legacy handshake establishes, scoped to this process.
 *
 * In that era the session IS the client's declaration, exactly as the
 * per-request `_meta` block is in the modern one: the capabilities and the
 * identity arrive once, at `initialize`, and every later request is read
 * against them.
 */
class McpLegacySession {
	active = false;
	capabilities: JsonObject = {};
	clientInfo: JsonObject = {};

	open(params: JsonObject): void {
		this.active = true;
		this.capabilities = isPlainObject(params.capabilities)
			? params.capabilities
			: {};
		this.clientInfo = isPlainObject(params.clientInfo) ? params.clientInfo : {};
	}
}

/**
 * The line channel to the client, in both directions.
 *
 * The legacy confirmation is a request the SERVER sends, so the loop has to be
 * able to write one and read its answer in the middle of serving a call.
 * Anything else that arrives while an answer is awaited is held here and served
 * afterwards -- the loop stays one request at a time, and no client line is
 * dropped.
 */
class McpChannel {
	readonly #lines: AsyncIterator<string>;
	readonly #out: Writer;
	readonly #held: string[] = [];

	constructor(input: NodeJS.ReadableStream, out: Writer) {
		this.#lines = createInterface({ input, crlfDelay: Infinity })[
			Symbol.asyncIterator
		]();
		this.#out = out;
	}

	/** The next line to serve: what was held first, then the stream. */
	async nextLine(): Promise<string | undefined> {
		const held = this.#held.shift();
		if (held !== undefined) {
			return held;
		}
		const next = await this.#lines.next();
		return next.done === true ? undefined : next.value;
	}

	write(message: JsonObject): void {
		this.#out.write(`${jsonCompact(message)}\n`);
	}

	/**
	 * Read until the response to `reqId` arrives, or the stream ends. A response
	 * carrying another id answers nothing this server sent and is discarded;
	 * anything else is held for the main loop.
	 */
	async awaitResponse(reqId: string): Promise<JsonObject | undefined> {
		for (;;) {
			const next = await this.#lines.next();
			if (next.done === true) {
				return undefined;
			}
			const line = next.value;
			if (line.trim() === "") {
				continue;
			}
			let msg: unknown;
			try {
				msg = JSON.parse(line);
			} catch {
				this.#held.push(line);
				continue;
			}
			if (
				isPlainObject(msg) &&
				(Object.hasOwn(msg, "result") || Object.hasOwn(msg, "error"))
			) {
				if (msg.id === reqId) {
					return msg;
				}
				continue;
			}
			this.#held.push(line);
		}
	}
}

/**
 * The legacy-era `initialize` handshake, which also selects that era.
 *
 * The modern revision has no handshake; this method is what a legacy client
 * opens with, and answering it is what puts this process into legacy semantics
 * for every later request that carries no modern metadata. This server speaks
 * one legacy revision, so it always answers with that one -- the negotiation
 * rule says to answer with the latest version supported.
 *
 * The declared feature is advertised here too, under the key that revision
 * gives a non-standard server capability: one name, two advertisements.
 */
function handleInitialize(
	app: AppImpl,
	reqId: unknown,
	params: JsonObject,
	session: McpLegacySession,
): JsonObject {
	session.open(params);
	return jsonrpcResult(reqId, {
		protocolVersion: MCP_LEGACY_PROTOCOL_VERSION,
		capabilities: {
			tools: {},
			experimental: { [MCP_FEATURE_CONSEQUENTIAL_CONFIRMATION]: {} },
		},
		serverInfo: { name: app.name, version: app.version },
	});
}

/**
 * `server/discover` -- the modern era's mandatory discovery call. It replaces
 * the handshake: supported versions, capabilities and identity in one request.
 * The declared feature is a NAME rather than a version number, so a client
 * learns that this server runs the confirmation dance without having to infer
 * it from a revision date.
 */
function handleDiscover(app: AppImpl, reqId: unknown): JsonObject {
	return completeResult(app, reqId, {
		supportedVersions: [MCP_PROTOCOL_VERSION],
		capabilities: {
			tools: {},
			extensions: { [MCP_FEATURE_CONSEQUENTIAL_CONFIRMATION]: {} },
		},
		instructions: app.help,
		ttlMs: MCP_CACHE_TTL_MS,
		cacheScope: MCP_CACHE_SCOPE,
	});
}

function handleToolsList(
	app: AppImpl,
	reqId: unknown,
	modern: boolean,
): JsonObject {
	// The classification sits BESIDE inputSchema, never inside it: it describes
	// the tool, not an argument the caller passes. Same emission rule as the
	// schema dump -- `effect` always, `consequential` only when true (absence
	// means "not consequential").
	const tools = collectToolCommands(app).map(([dottedPath, cmd]) => {
		const { effect, consequential } = commandClassification(cmd);
		const def: JsonObject = {
			name: dottedPath,
			// The scope structure the flattened schema cannot carry rides the
			// description, exactly as it does on the Tool descriptor (§24.11).
			description: toolDescription(cmd),
			effect,
			inputSchema: buildJSONSchema(cmd),
		};
		if (consequential) {
			def.consequential = true;
		}
		return def;
	});
	if (!modern) {
		return jsonrpcResult(reqId, { tools });
	}
	return completeResult(app, reqId, {
		tools,
		ttlMs: MCP_CACHE_TTL_MS,
		cacheScope: MCP_CACHE_SCOPE,
	});
}

async function handleToolsCall(
	app: AppImpl,
	reqId: unknown,
	params: JsonObject,
	modern: boolean,
	meta: JsonObject,
	continuation: McpContinuation | undefined,
	legacy?: { session: McpLegacySession; channel: McpChannel },
): Promise<JsonObject> {
	if (!Object.hasOwn(params, "name")) {
		return jsonrpcError(
			reqId,
			MCP_ERR_INVALID_PARAMS,
			"missing required parameter: name",
		);
	}
	const toolName = params.name;
	if (typeof toolName !== "string") {
		return jsonrpcError(
			reqId,
			MCP_ERR_INVALID_PARAMS,
			"parameter 'name' must be a string",
		);
	}

	// Unknown tools are NOT a -32602 protocol error: the name is passed to
	// app.call(), whose invocation error surfaces as isError content below.
	let callArgs: JsonObject = {};
	if (Object.hasOwn(params, "arguments")) {
		const argsVal = params.arguments;
		if (!isPlainObject(argsVal)) {
			return jsonrpcError(
				reqId,
				MCP_ERR_INVALID_PARAMS,
				"parameter 'arguments' must be an object",
			);
		}
		callArgs = argsVal;
	}

	// Consent is a top-level param, a sibling of `name` and `arguments` --
	// never a member of `arguments`, which is the command's own argument
	// namespace and is published with additionalProperties: false. There is no
	// server-side default: absent means "not consented", and a consequential
	// tool is then refused.
	const consentVal = Object.hasOwn(params, "approve_consequential")
		? params.approve_consequential
		: false;
	if (typeof consentVal !== "boolean") {
		return jsonrpcError(
			reqId,
			MCP_ERR_INVALID_PARAMS,
			"parameter 'approve_consequential' must be a boolean",
		);
	}

	let consented = consentVal;
	if (modern && continuation !== undefined) {
		const exchange = confirmationExchange(
			app,
			reqId,
			params,
			toolName,
			callArgs,
			meta,
			continuation,
			consented,
		);
		if (exchange.response !== undefined) {
			return exchange.response;
		}
		consented = exchange.consented;
	} else if (!modern && !consented && continuation !== undefined && legacy) {
		const answered = await legacyConfirmation(
			app,
			toolName,
			callArgs,
			legacy.session,
			continuation,
			legacy.channel,
		);
		if (answered === false) {
			return toolResult(app, reqId, errConfirmDeclined(), false, true);
		}
		consented = answered === true;
	}

	let result: unknown;
	try {
		// The flat machine form is converted into the elected records at the
		// protocol boundary, through the same election machinery the argv path
		// uses (§24.11); a wrong combination is refused with the CLI's own
		// sentence, surfaced through the same isError content an invocation
		// error takes.
		const entry = collectToolCommands(app).find(([path]) => path === toolName);
		const callable =
			entry === undefined
				? callArgs
				: flatToCallKwargs(app, toolName, entry[1], callArgs);
		result = await app.call(toolName, callable, {
			approveConsequential: consented,
		});
	} catch (e) {
		return toolResult(
			app,
			reqId,
			e instanceof Error ? e.message : String(e),
			modern,
			true,
		);
	}

	// jsonCompact serializes undefined as "null" (Python json.dumps(None))
	// and BigInt values as bare integer tokens.
	return toolResult(app, reqId, jsonCompact(result), modern);
}

/** Dispatch one modern-era request: metadata, then version, then method. */
async function dispatchModern(
	app: AppImpl,
	reqId: unknown,
	method: string,
	params: JsonObject,
	meta: JsonObject,
	continuation: McpContinuation,
): Promise<JsonObject> {
	const invalid = validateMeta(meta);
	if (invalid !== undefined) {
		return jsonrpcError(reqId, MCP_ERR_INVALID_PARAMS, invalid);
	}
	const version = meta[MCP_META_PROTOCOL_VERSION];
	if (version !== MCP_PROTOCOL_VERSION) {
		return jsonrpcError(
			reqId,
			MCP_ERR_UNSUPPORTED_PROTOCOL_VERSION,
			"Unsupported protocol version",
			{ supported: [MCP_PROTOCOL_VERSION], requested: version },
		);
	}
	if (method === "server/discover") {
		return handleDiscover(app, reqId);
	}
	if (method === "tools/list") {
		return handleToolsList(app, reqId, true);
	}
	if (method === "tools/call") {
		return await handleToolsCall(app, reqId, params, true, meta, continuation);
	}
	return jsonrpcError(
		reqId,
		MCP_ERR_METHOD_NOT_FOUND,
		`Method not found: ${method}`,
	);
}

/** Dispatch one legacy-era request, in that revision's own shapes. */
async function dispatchLegacy(
	app: AppImpl,
	reqId: unknown,
	method: string,
	params: JsonObject,
	session: McpLegacySession,
	continuation: McpContinuation,
	channel: McpChannel,
): Promise<JsonObject> {
	if (method === "tools/list") {
		return handleToolsList(app, reqId, false);
	}
	if (method === "tools/call") {
		return await handleToolsCall(app, reqId, params, false, {}, continuation, {
			session,
			channel,
		});
	}
	return jsonrpcError(
		reqId,
		MCP_ERR_METHOD_NOT_FOUND,
		`Method not found: ${method}`,
	);
}

/**
 * Runs the MCP JSON-RPC 2.0 server loop: one JSON object per line in, one
 * JSON object per line out, until input is exhausted (EOF). Notifications
 * (no "id" key) get no response; blank lines are skipped.
 */
export async function serveMcp(app: AppImpl, io: McpIO = {}): Promise<void> {
	const input = io.input ?? process.stdin;
	const output = io.output ?? process.stdout;
	const channel = new McpChannel(input, output);
	const write = (resp: JsonObject): void => {
		channel.write(resp);
	};

	// The one piece of connection state a dual-era server is allowed: an
	// `initialize` request selects legacy semantics for this process and carries
	// that client's declaration. Modern requests carry everything they need and
	// never consult it.
	const session = new McpLegacySession();
	// The continuation minting key and the spent-id set. Both are per process:
	// a blob is unforgeable outside this process and unusable twice inside it.
	const continuation = new McpContinuation();

	for (;;) {
		const line = await channel.nextLine();
		if (line === undefined) {
			break;
		}
		if (line.trim() === "") {
			continue;
		}

		let msg: unknown;
		try {
			msg = JSON.parse(line);
		} catch {
			write(jsonrpcError(null, MCP_ERR_PARSE, "Parse error"));
			continue;
		}
		// A non-object JSON value is a parse error, matching Go (which
		// unmarshals directly into a struct).
		if (!isPlainObject(msg)) {
			write(jsonrpcError(null, MCP_ERR_PARSE, "Parse error"));
			continue;
		}

		// Notifications have no "id" key -- consume silently, no response.
		// Neither does a response the client sent when this server asked for
		// none.
		if (
			!Object.hasOwn(msg, "id") ||
			Object.hasOwn(msg, "result") ||
			Object.hasOwn(msg, "error")
		) {
			continue;
		}
		const reqId = msg.id;
		const method = Object.hasOwn(msg, "method") ? msg.method : "";
		const params = isPlainObject(msg.params) ? msg.params : {};

		let resp: JsonObject;
		if (method === "initialize") {
			resp = handleInitialize(app, reqId, params, session);
		} else {
			const metaVal = params._meta;
			const present = Object.hasOwn(params, "_meta");
			if (present && !isPlainObject(metaVal)) {
				resp = jsonrpcError(
					reqId,
					MCP_ERR_INVALID_PARAMS,
					"parameter '_meta' must be an object",
				);
			} else if (
				isPlainObject(metaVal) &&
				Object.hasOwn(metaVal, MCP_META_PROTOCOL_VERSION)
			) {
				resp = await dispatchModern(
					app,
					reqId,
					String(method),
					params,
					metaVal,
					continuation,
				);
			} else if (session.active) {
				resp = await dispatchLegacy(
					app,
					reqId,
					String(method),
					params,
					session,
					continuation,
					channel,
				);
			} else {
				resp = jsonrpcError(
					reqId,
					MCP_ERR_INVALID_PARAMS,
					`missing required request metadata: _meta['${MCP_META_PROTOCOL_VERSION}']`,
				);
			}
		}
		write(resp);
	}
}

// --- The continuation primitive (contract §22.4) ----------------------------
//
// The protocol is stateless: a server that needs more input answers with an
// input-required result and whatever it must remember, and the client echoes
// that back on a retry that is otherwise a fresh, independent request. The
// state therefore travels THROUGH the client, which makes it attacker-
// controlled input rather than server memory.

/**
 * How long a minted continuation stays usable: long enough for a human to
 * answer the confirmation the client renders, short enough that a captured blob
 * is worth little.
 */
export const MCP_CONTINUATION_TTL_SECONDS = 300;

/** The key the confirmation elicitation is filed under, in both directions. */
const MCP_CONFIRMATION_KEY = "consequential-confirmation";

/** The continuation refusals, byte-identical in all three implementations. */
const MCP_ERR_STATE_VERIFICATION = "requestState failed verification";
const MCP_ERR_STATE_EXPIRED = "requestState has expired";
const MCP_ERR_STATE_WRONG_CLIENT =
	"requestState was issued to a different client";
const MCP_ERR_STATE_WRONG_REQUEST = "requestState does not match this request";
const MCP_ERR_STATE_REUSED = "requestState has already been used";

/** The base64url alphabet, in value order -- the one spelling §22.4 defines. */
const B64URL_ALPHABET =
	"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

/**
 * Whether `text` is the ONE unpadded base64url spelling of some bytes.
 *
 * The three languages' decoders do not agree on their own -- Python's ignores
 * stray characters and accepts padding, Node's ignores anything outside the
 * alphabet, and Go's skips newlines -- and all three ignore the trailing bits
 * of a final character that does not fill a byte, so a blob's last character
 * can be altered without changing what it decodes to. The blob is
 * attacker-controlled input, so the spelling is checked here, before any
 * decoder sees it, identically in all three implementations.
 */
function b64urlCanonical(text: string): boolean {
	const remainder = text.length % 4;
	if (remainder === 1) {
		// No byte string encodes to a length one past a multiple of four.
		return false;
	}
	let last = 0;
	for (const char of text) {
		last = B64URL_ALPHABET.indexOf(char);
		if (last < 0) {
			return false;
		}
	}
	if (remainder === 0) {
		return true;
	}
	// Two characters carry one byte and three carry two, so the final character
	// has 4 or 2 bits left over, and a canonical encoder leaves them zero.
	return (last & (remainder === 2 ? 0b1111 : 0b11)) === 0;
}

/** Decodes canonical unpadded base64url, or undefined when it is not one. */
function b64urlDecode(text: string): Buffer | undefined {
	return b64urlCanonical(text) ? Buffer.from(text, "base64url") : undefined;
}

/**
 * Mints and verifies the integrity-protected continuation state blob.
 *
 * The blob is `<payload>.<mac>`, both unpadded base64url, where the MAC is
 * HMAC-SHA256 over the payload bytes under a key minted for this process and
 * never emitted. A blob is therefore unforgeable without reading this process's
 * memory, and worthless to any other process.
 *
 * The payload binds three things the protocol requires be checked on receipt --
 * the principal it was issued to, an expiry, and a digest of the originating
 * request -- plus a unique id, which is what makes single use enforceable:
 * those three bound the replay window but do not close it.
 */
export class McpContinuation {
	readonly #key = randomBytes(32);
	readonly #consumed = new Map<string, number>();

	#mac(payload: Buffer): Buffer {
		return createHmac("sha256", this.#key).update(payload).digest();
	}

	/** Issues a blob binding this principal, this request and a short window. */
	mint(principal: string, digest: string, now?: number): string {
		const moment = now ?? Math.floor(Date.now() / 1000);
		const payload = Buffer.from(
			JSON.stringify({
				v: 1,
				jti: randomBytes(16).toString("base64url"),
				prin: principal,
				exp: Math.floor(moment) + MCP_CONTINUATION_TTL_SECONDS,
				req: digest,
			}),
			"utf8",
		);
		return `${payload.toString("base64url")}.${this.#mac(payload).toString("base64url")}`;
	}

	/**
	 * Verifies and CONSUMES a blob; returns undefined when it is good and the
	 * refusal otherwise. A blob that passes every check is consumed here, so a
	 * second presentation is refused even though it is still perfectly
	 * well-formed, unexpired and correctly bound.
	 */
	verify(
		blob: string,
		principal: string,
		digest: string,
		now?: number,
	): string | undefined {
		const moment = Math.floor(now ?? Date.now() / 1000);
		const parts = blob.split(".");
		if (parts.length !== 2) {
			return MCP_ERR_STATE_VERIFICATION;
		}
		const raw = b64urlDecode(parts[0] as string);
		const mac = b64urlDecode(parts[1] as string);
		if (raw === undefined || mac === undefined) {
			return MCP_ERR_STATE_VERIFICATION;
		}
		const expected = this.#mac(raw);
		if (
			mac.length !== expected.length ||
			!timingSafeEqual(mac, expected) ||
			raw.length === 0
		) {
			return MCP_ERR_STATE_VERIFICATION;
		}
		let payload: unknown;
		try {
			payload = JSON.parse(raw.toString("utf8"));
		} catch {
			return MCP_ERR_STATE_VERIFICATION;
		}
		if (!isPlainObject(payload) || payload.v !== 1) {
			return MCP_ERR_STATE_VERIFICATION;
		}
		const jti = payload.jti;
		const expiry = payload.exp;
		if (
			typeof jti !== "string" ||
			typeof expiry !== "number" ||
			!Number.isInteger(expiry)
		) {
			return MCP_ERR_STATE_VERIFICATION;
		}
		this.#prune(moment);
		if (expiry <= moment) {
			return MCP_ERR_STATE_EXPIRED;
		}
		if (payload.prin !== principal) {
			return MCP_ERR_STATE_WRONG_CLIENT;
		}
		if (payload.req !== digest) {
			return MCP_ERR_STATE_WRONG_REQUEST;
		}
		if (this.#consumed.has(jti)) {
			return MCP_ERR_STATE_REUSED;
		}
		this.#consumed.set(jti, expiry);
		return undefined;
	}

	/** Forgets consumed ids that can no longer be replayed anyway. */
	#prune(moment: number): void {
		for (const [jti, expiry] of this.#consumed) {
			if (expiry <= moment) {
				this.#consumed.delete(jti);
			}
		}
	}
}

/**
 * A canonical encoding of a JSON value, for digesting: keys sorted, no
 * insignificant whitespace, floats in the framework's canonical form -- so the
 * digest depends on what the caller said, never on how their encoder spelled
 * it.
 */
function canonicalJson(value: unknown): string {
	if (value === null || value === undefined) {
		return "null";
	}
	if (typeof value === "boolean") {
		return value ? "true" : "false";
	}
	if (typeof value === "bigint") {
		return value.toString();
	}
	if (typeof value === "number") {
		return Number.isInteger(value) && Math.abs(value) < 1e15
			? String(value)
			: formatFloatCanonical(value);
	}
	if (typeof value === "string") {
		return JSON.stringify(value);
	}
	if (Array.isArray(value)) {
		return `[${value.map(canonicalJson).join(",")}]`;
	}
	if (isPlainObject(value)) {
		const keys = Object.keys(value).sort();
		const parts = keys.map(
			(key) => `${JSON.stringify(key)}:${canonicalJson(value[key])}`,
		);
		return `{${parts.join(",")}}`;
	}
	return JSON.stringify(String(value));
}

/** Digests the originating request: the method and its salient parameters. */
function requestDigest(
	method: string,
	toolName: string,
	args: JsonObject,
): string {
	const material = [method, toolName, canonicalJson(args)].join("\n");
	return createHash("sha256").update(material, "utf8").digest("base64url");
}

/**
 * The client this state is minted for, as the client declares itself.
 *
 * On this transport there is no authenticated principal; the declaration is
 * self-reported and the binding is a consistency check, not authentication.
 * What actually contains a stolen blob is the per-process key.
 */
function principalOf(meta: JsonObject): string {
	return principalOfInfo(meta[MCP_META_CLIENT_INFO]);
}

/**
 * One self-reported client identity. Both eras carry the same self-report --
 * per request, or once at the handshake.
 */
function principalOfInfo(info: unknown): string {
	if (!isPlainObject(info)) {
		return "";
	}
	const name = typeof info.name === "string" ? info.name : "";
	const version = typeof info.version === "string" ? info.version : "";
	return `${name}/${version}`;
}

/** True when a modern request's client declared a form elicitation. */
function clientDeclaresElicitation(meta: JsonObject): boolean {
	return declaresFormElicitation(meta[MCP_META_CLIENT_CAPABILITIES]);
}

/**
 * True when a capabilities block says the client can render a form. An empty
 * `elicitation` object means form mode, which the protocol states for
 * compatibility with clients written before the modes existed. The two eras
 * deliver their capabilities differently -- per request, or once at the
 * handshake -- and read them the same way.
 */
function declaresFormElicitation(caps: unknown): boolean {
	if (!isPlainObject(caps)) {
		return false;
	}
	const elicitation = caps.elicitation;
	if (!isPlainObject(elicitation)) {
		return false;
	}
	if (Object.keys(elicitation).length === 0) {
		return true;
	}
	return isPlainObject(elicitation.form);
}

/**
 * The elicitation that asks a human, through the client, to confirm. Same words
 * as the terminal prompt (§12.6) minus its keystroke hint: one vocabulary for
 * one question, however it is delivered.
 */
function confirmationRequest(cmdPath: string): JsonObject {
	return {
		method: "elicitation/create",
		params: {
			mode: "form",
			message: `about to run consequential command '${cmdPath}'. Proceed?`,
			requestedSchema: {
				type: "object",
				properties: {
					proceed: {
						type: "boolean",
						title: "Proceed",
						description: "Whether to run the consequential command.",
					},
				},
				required: ["proceed"],
			},
		},
	};
}

/** The interim result: what is needed, and the state to echo back with it. */
function inputRequired(
	app: AppImpl,
	reqId: unknown,
	cmdPath: string,
	requestState: string,
): JsonObject {
	return jsonrpcResult(reqId, {
		resultType: "input_required",
		inputRequests: { [MCP_CONFIRMATION_KEY]: confirmationRequest(cmdPath) },
		requestState,
		_meta: serverInfoMeta(app),
	});
}

/**
 * Reads one elicitation result: "accept", "reject", "absent" or "malformed".
 *
 * An `accept` carrying `proceed: false` is a refusal, not an approval: the
 * action names what the client did with the dialogue, and the field is the
 * answer to the question.
 */
function confirmationVerdict(answer: unknown): string {
	if (answer === undefined || answer === null) {
		return "absent";
	}
	if (!isPlainObject(answer)) {
		return "malformed";
	}
	const action = answer.action;
	if (action === "decline" || action === "cancel") {
		return "reject";
	}
	if (action !== "accept") {
		return "malformed";
	}
	const content = answer.content;
	if (!isPlainObject(content)) {
		return "malformed";
	}
	return content.proceed === true ? "accept" : "reject";
}

/**
 * Runs the confirmation round-trip for one call: either a response to send back
 * (a refusal, or the interim result asking for confirmation) or the consent the
 * call may proceed with.
 */
function confirmationExchange(
	app: AppImpl,
	reqId: unknown,
	params: JsonObject,
	toolName: string,
	args: JsonObject,
	meta: JsonObject,
	continuation: McpContinuation,
	alreadyConsented: boolean,
): { response?: JsonObject; consented: boolean } {
	const principal = principalOf(meta);
	const digest = requestDigest("tools/call", toolName, args);
	let consented = alreadyConsented;

	if (Object.hasOwn(params, "requestState")) {
		const state = params.requestState;
		if (typeof state !== "string") {
			return {
				response: jsonrpcError(
					reqId,
					MCP_ERR_INVALID_PARAMS,
					"parameter 'requestState' must be a string",
				),
				consented: false,
			};
		}
		let responses: JsonObject = {};
		if (Object.hasOwn(params, "inputResponses")) {
			const given = params.inputResponses;
			if (!isPlainObject(given)) {
				return {
					response: jsonrpcError(
						reqId,
						MCP_ERR_INVALID_PARAMS,
						"parameter 'inputResponses' must be an object",
					),
					consented: false,
				};
			}
			responses = given;
		}
		const refusal = continuation.verify(state, principal, digest);
		if (refusal !== undefined) {
			return {
				response: jsonrpcError(reqId, MCP_ERR_INVALID_PARAMS, refusal),
				consented: false,
			};
		}
		const verdict = confirmationVerdict(responses[MCP_CONFIRMATION_KEY]);
		if (verdict === "malformed") {
			return {
				response: jsonrpcError(
					reqId,
					MCP_ERR_INVALID_PARAMS,
					`inputResponses['${MCP_CONFIRMATION_KEY}'] is not an elicitation result`,
				),
				consented: false,
			};
		}
		if (verdict === "reject") {
			return {
				response: toolResult(app, reqId, errConfirmDeclined(), true, true),
				consented: false,
			};
		}
		if (verdict === "accept") {
			consented = true;
		} else {
			// The state was good but the answer never came. The protocol says to
			// ask again rather than error -- with a fresh state, since the one
			// just presented is spent.
			return {
				response: inputRequired(
					app,
					reqId,
					toolName,
					continuation.mint(principal, digest),
				),
				consented: false,
			};
		}
	} else if (Object.hasOwn(params, "inputResponses")) {
		// An answer whose state is missing cannot be verified, and an
		// unverifiable answer is not an answer.
		return {
			response: jsonrpcError(
				reqId,
				MCP_ERR_INVALID_PARAMS,
				"parameter 'inputResponses' requires the requestState it was issued with",
			),
			consented: false,
		};
	}

	if (consented) {
		return { consented: true };
	}
	const entry = collectToolCommands(app).find(([path]) => path === toolName);
	if (entry === undefined || !commandClassification(entry[1]).consequential) {
		return { consented };
	}
	if (clientDeclaresElicitation(meta)) {
		return {
			response: inputRequired(
				app,
				reqId,
				toolName,
				continuation.mint(principal, digest),
			),
			consented: false,
		};
	}
	// A client that cannot render the confirmation is told what it would have to
	// declare, in the code the revision assigns -- never how to proceed without
	// confirming.
	return {
		response: jsonrpcError(
			reqId,
			MCP_ERR_MISSING_CLIENT_CAPABILITY,
			MCP_MSG_MISSING_ELICITATION,
			{ requiredCapabilities: requiredElicitationCapabilities() },
		),
		consented: false,
	};
}

/**
 * Ask a legacy client to confirm, over a request the server sends.
 *
 * Returns true when the client accepted, false when the exchange aborted, and
 * undefined when there was nothing to ask -- either the command is not
 * consequential, or this client never declared it could render the form, in
 * which case the call reaches the consent seam unconsented and gets its
 * refusal.
 *
 * The continuation blob rides as the JSON-RPC request id: JSON-RPC obliges the
 * client to echo an id back verbatim, which is the same obligation the modern
 * era puts on `requestState`, so the correlation needs no second mechanism. It
 * is verified on return through the same path, and a matching id that fails any
 * of those checks confirms nothing.
 */
async function legacyConfirmation(
	app: AppImpl,
	toolName: string,
	args: JsonObject,
	session: McpLegacySession,
	continuation: McpContinuation,
	channel: McpChannel,
): Promise<boolean | undefined> {
	const entry = collectToolCommands(app).find(([path]) => path === toolName);
	if (entry === undefined || !commandClassification(entry[1]).consequential) {
		return undefined;
	}
	if (!declaresFormElicitation(session.capabilities)) {
		return undefined;
	}
	const principal = principalOfInfo(session.clientInfo);
	const digest = requestDigest("tools/call", toolName, args);
	const state = continuation.mint(principal, digest);
	channel.write({
		jsonrpc: "2.0",
		id: state,
		...confirmationRequest(toolName),
	});
	const answer = await channel.awaitResponse(state);
	// Consumption is unconditional once the blob is on the wire: EVERY exit
	// below spends it, not just the one that reads a well-formed result. A blob
	// an aborted exchange left live is still bound to this principal and this
	// request digest for its whole five minutes, which is a `requestState` the
	// client can present on the modern path with an acceptance it wrote itself
	// -- for the very call the abort refused.
	const verified = continuation.verify(state, principal, digest) === undefined;
	if (answer === undefined || !Object.hasOwn(answer, "result")) {
		// A JSON-RPC error, or a stream that ended before an answer arrived.
		// There is no re-ask in this era: the server is holding the request open,
		// so a non-answer is a decision.
		return false;
	}
	if (!verified) {
		return false;
	}
	return confirmationVerdict(answer.result) === "accept";
}
