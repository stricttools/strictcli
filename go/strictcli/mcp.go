package strictcli

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// mcpRequest represents an incoming JSON-RPC 2.0 request or notification.
type mcpRequest struct {
	Jsonrpc string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id,omitempty"` // nil for notifications
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

// mcpResponse represents an outgoing JSON-RPC 2.0 response.
type mcpResponse struct {
	Jsonrpc string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *mcpError   `json:"error,omitempty"`
}

// mcpError represents a JSON-RPC 2.0 error object.
type mcpError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// JSON-RPC and MCP error codes. -32020 (HeaderMismatch) belongs to the HTTP
// transport, which this server does not speak; -32021
// (MissingRequiredClientCapability) is what a consequential call from a client
// that cannot render the confirmation is answered with.
const (
	mcpErrMethodNotFound             = -32601
	mcpErrInvalidParams              = -32602
	mcpErrInternal                   = -32603
	mcpErrMissingClientCapability    = -32021
	mcpErrUnsupportedProtocolVersion = -32022
)

// The revision forbids a server sending an input request the client never said
// it could fulfil, and assigns -32021 for saying so. `data.requiredCapabilities`
// is a client-capabilities object, and this server names FORM mode: a client
// that declared only URL-mode elicitation cannot render this question either.
const mcpMsgMissingElicitation = "Server requires the elicitation capability for this request"

func mcpRequiredElicitationCapabilities() map[string]interface{} {
	return map[string]interface{}{
		"elicitation": map[string]interface{}{
			"form": map[string]interface{}{},
		},
	}
}

// The server speaks two eras (effects contract §22):
//
//	MODERN (2026-07-28) -- stateless. There is no handshake: every request
//	carries its protocol version and the client's capabilities in `_meta`,
//	every result carries a `resultType`, and `server/discover` advertises the
//	supported versions, the capabilities and the server identity that the
//	handshake used to carry.
//
//	LEGACY (2025-11-25) -- the `initialize` handshake, the newest of the
//	handshake-based revisions. It is selected by an `initialize` request and
//	scoped to this process, which is exactly the dual-era rule the modern
//	revision specifies for a server serving both. That era has no
//	input-required result, so the same confirmation is delivered as a
//	server-initiated `elicitation/create` request (contract §22.7).
//
// A request that carries neither the modern metadata nor a preceding
// `initialize` is malformed and is refused. Nothing is inferred.
const (
	mcpProtocolVersion       = "2026-07-28"
	mcpLegacyProtocolVersion = "2025-11-25"
)

// The reserved `_meta` keys of the modern revision.
const (
	mcpMetaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	mcpMetaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	mcpMetaClientInfo         = "io.modelcontextprotocol/clientInfo"
	mcpMetaServerInfo         = "io.modelcontextprotocol/serverInfo"
	mcpMetaLogLevel           = "io.modelcontextprotocol/logLevel"
	mcpMetaSubscriptionID     = "io.modelcontextprotocol/subscriptionId"
)

// mcpRecognizedReservedMetaKeys are the keys this revision defines under a
// prefix the protocol reserves for itself. Any other key under a reserved
// prefix is refused rather than ignored.
var mcpRecognizedReservedMetaKeys = map[string]bool{
	mcpMetaProtocolVersion:    true,
	mcpMetaClientCapabilities: true,
	mcpMetaClientInfo:         true,
	mcpMetaLogLevel:           true,
	mcpMetaSubscriptionID:     true,
}

// mcpFeatureConsequentialConfirmation is the named feature the server declares.
// A NAME, never a version number: a new name appears
// only if the confirmation dance changes incompatibly.
const mcpFeatureConsequentialConfirmation = "dev.smmh.strictcli/consequential-confirmation"

// Cacheability of the list surfaces. The tool list is derived from the app's
// static command registration, so it cannot vary per client (public) and cannot
// change while the process runs.
const (
	mcpCacheTTLMs = 3600000
	mcpCacheScope = "public"
)

var (
	mcpMetaPrefixLabelRe = regexp.MustCompile(`^[A-Za-z](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
	mcpMetaNameRe        = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
)

// mcpMetaKeyValid reports whether key matches the protocol's `_meta` key-name
// grammar: an optional dot-separated prefix, a slash, and a name. The name may
// be empty; when it is not, it begins and ends alphanumeric and may carry
// hyphens, underscores and dots in between.
func mcpMetaKeyValid(key string) bool {
	parts := strings.Split(key, "/")
	var name string
	switch len(parts) {
	case 1:
		name = parts[0]
	case 2:
		for _, label := range strings.Split(parts[0], ".") {
			if !mcpMetaPrefixLabelRe.MatchString(label) {
				return false
			}
		}
		name = parts[1]
	default:
		return false
	}
	return name == "" || mcpMetaNameRe.MatchString(name)
}

// mcpMetaKeyReserved reports whether key sits under a prefix the protocol
// reserves for itself: any prefix whose SECOND label is `modelcontextprotocol`
// or `mcp`. So `io.modelcontextprotocol/` and `com.mcp.tools/` are reserved and
// `com.example.mcp/` is not.
func mcpMetaKeyReserved(key string) bool {
	parts := strings.Split(key, "/")
	if len(parts) != 2 {
		return false
	}
	labels := strings.Split(parts[0], ".")
	return len(labels) >= 2 && (labels[1] == "modelcontextprotocol" || labels[1] == "mcp")
}

// mcpValidateMeta validates one request's `_meta` block, returning the refusal
// text or "". It is called only once the block is known to carry the protocol
// version, which is what selects the modern era in the first place.
func mcpValidateMeta(meta map[string]interface{}) string {
	// Sorted so the refusal is deterministic when a request carries more than
	// one offending key: Go map iteration is randomized.
	keys := make([]string, 0, len(meta))
	for key := range meta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !mcpMetaKeyValid(key) {
			return fmt.Sprintf("invalid _meta key name: '%s'", key)
		}
		if mcpMetaKeyReserved(key) && !mcpRecognizedReservedMetaKeys[key] {
			return fmt.Sprintf("unrecognized reserved _meta key: '%s'", key)
		}
	}
	if _, ok := meta[mcpMetaProtocolVersion].(string); !ok {
		return fmt.Sprintf("_meta['%s'] must be a string", mcpMetaProtocolVersion)
	}
	caps, ok := meta[mcpMetaClientCapabilities]
	if !ok {
		return fmt.Sprintf("missing required request metadata: _meta['%s']", mcpMetaClientCapabilities)
	}
	if _, ok := caps.(map[string]interface{}); !ok {
		return fmt.Sprintf("_meta['%s'] must be an object", mcpMetaClientCapabilities)
	}
	if info, present := meta[mcpMetaClientInfo]; present {
		if _, ok := info.(map[string]interface{}); !ok {
			return fmt.Sprintf("_meta['%s'] must be an object", mcpMetaClientInfo)
		}
	}
	return ""
}

// mcpServerInfo is the identity every modern result carries in its own `_meta`.
func (a *App) mcpServerInfo() map[string]interface{} {
	return map[string]interface{}{
		mcpMetaServerInfo: map[string]interface{}{
			"name":    a.Name,
			"version": a.Version,
		},
	}
}

// mcpCompleteResult wraps a modern result body with its result type and the
// server identity.
func (a *App) mcpCompleteResult(reqID interface{}, body map[string]interface{}) mcpResponse {
	result := map[string]interface{}{"resultType": "complete"}
	for k, v := range body {
		result[k] = v
	}
	result["_meta"] = a.mcpServerInfo()
	return mcpResponse{Jsonrpc: "2.0", ID: reqID, Result: result}
}

// mcpErrorResponse builds a JSON-RPC error response, with optional data.
func mcpErrorResponse(reqID interface{}, code int, message string, data interface{}) mcpResponse {
	return mcpResponse{
		Jsonrpc: "2.0",
		ID:      reqID,
		Error:   &mcpError{Code: code, Message: message, Data: data},
	}
}

// ServeMCP starts a JSON-RPC 2.0 server on stdin/stdout implementing the
// Model Context Protocol. It reads one JSON object per line from stdin and
// writes one JSON object per line to stdout. The server handles
// server/discover, tools/list and tools/call under protocol 2026-07-28, plus
// the retained initialize handshake of the era before it.
func (a *App) ServeMCP() {
	a.serveMCPIO(os.Stdin, os.Stdout)
}

// mcpLegacySession is what the legacy handshake establishes, scoped to this
// process. In that era the session IS the client's declaration, exactly as the
// per-request `_meta` block is in the modern one: the capabilities and the
// identity arrive once, at `initialize`, and every later request is read
// against them.
type mcpLegacySession struct {
	active       bool
	capabilities map[string]interface{}
	clientInfo   map[string]interface{}
}

func (s *mcpLegacySession) open(params map[string]interface{}) {
	s.active = true
	s.capabilities, _ = params["capabilities"].(map[string]interface{})
	s.clientInfo, _ = params["clientInfo"].(map[string]interface{})
}

// mcpChannel is the line channel to the client, in both directions.
//
// The legacy confirmation is a request the SERVER sends, so the loop has to be
// able to write one and read its answer in the middle of serving a call.
// Anything else that arrives while an answer is awaited is held here and served
// afterwards -- the loop stays one request at a time, and no client line is
// dropped.
type mcpChannel struct {
	scanner *bufio.Scanner
	out     io.Writer
	held    []string
}

func newMCPChannel(in io.Reader, out io.Writer) *mcpChannel {
	scanner := bufio.NewScanner(in)
	// Increase buffer size for large JSON objects
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	return &mcpChannel{scanner: scanner, out: out}
}

// nextLine returns the next line to serve: what was held first, then the stream.
func (c *mcpChannel) nextLine() (string, bool) {
	if len(c.held) > 0 {
		line := c.held[0]
		c.held = c.held[1:]
		return line, true
	}
	if c.scanner.Scan() {
		return c.scanner.Text(), true
	}
	return "", false
}

// write emits one server-to-client message as a single line.
func (c *mcpChannel) write(message interface{}) {
	data, err := json.Marshal(message)
	if err != nil {
		fmt.Fprintf(c.out, `{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"marshal error"}}`+"\n")
		return
	}
	fmt.Fprintf(c.out, "%s\n", data)
}

// awaitResponse reads until the response to reqID arrives, or the stream ends.
// A response carrying another id answers nothing this server sent and is
// discarded; anything else is held for the main loop.
func (c *mcpChannel) awaitResponse(reqID string) map[string]interface{} {
	for c.scanner.Scan() {
		line := c.scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil || msg == nil {
			c.held = append(c.held, line)
			continue
		}
		_, hasResult := msg["result"]
		_, hasError := msg["error"]
		if hasResult || hasError {
			if id, ok := msg["id"].(string); ok && id == reqID {
				return msg
			}
			continue
		}
		c.held = append(c.held, line)
	}
	return nil
}

// serveMCPIO is the internal implementation of ServeMCP that accepts custom
// reader/writer for testability.
func (a *App) serveMCPIO(in io.Reader, out io.Writer) {
	channel := newMCPChannel(in, out)

	// The one piece of connection state a dual-era server is allowed: an
	// `initialize` request selects legacy semantics for this process and
	// carries that client's declaration. Modern requests carry everything they
	// need and never consult it.
	session := &mcpLegacySession{}
	// The continuation minting key and the spent-id set. Both are per process:
	// a blob is unforgeable outside this process and unusable twice inside it.
	continuation := newMCPContinuation()

	for {
		line, ok := channel.nextLine()
		if !ok {
			return
		}
		if strings.TrimSpace(line) == "" {
			continue
		}

		var raw map[string]interface{}
		var req mcpRequest
		if err := json.Unmarshal([]byte(line), &raw); err != nil || raw == nil {
			// Invalid JSON -- send parse error if we can extract an ID
			channel.write(mcpResponse{
				Jsonrpc: "2.0",
				ID:      nil,
				Error: &mcpError{
					Code:    -32700,
					Message: "Parse error",
				},
			})
			continue
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			channel.write(mcpResponse{
				Jsonrpc: "2.0",
				ID:      nil,
				Error:   &mcpError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		// Notifications have no ID and expect no response. Neither does a
		// response the client sent when this server asked for none.
		_, hasResult := raw["result"]
		_, hasError := raw["error"]
		if req.ID == nil || hasResult || hasError {
			// notifications/initialized and any other notification: silently ignore
			continue
		}

		channel.write(a.handleMCPRequest(req, session, continuation, channel))
	}
}

// handleMCPRequest routes one request to its era and dispatches it there.
func (a *App) handleMCPRequest(
	req mcpRequest,
	session *mcpLegacySession,
	continuation *mcpContinuation,
	channel *mcpChannel,
) mcpResponse {
	if req.Method == "initialize" {
		return a.handleMCPInitialize(req, session)
	}

	metaVal, present := req.Params["_meta"]
	meta, isObject := metaVal.(map[string]interface{})
	switch {
	case present && !isObject:
		return mcpErrorResponse(req.ID, mcpErrInvalidParams,
			"parameter '_meta' must be an object", nil)
	case isObject && hasKey(meta, mcpMetaProtocolVersion):
		return a.dispatchMCPModern(req, meta, continuation)
	case session.active:
		return a.dispatchMCPLegacy(req, session, continuation, channel)
	default:
		return mcpErrorResponse(req.ID, mcpErrInvalidParams,
			fmt.Sprintf("missing required request metadata: _meta['%s']", mcpMetaProtocolVersion), nil)
	}
}

// hasKey reports whether a decoded JSON object carries a key at all, including
// one whose value is null.
func hasKey(obj map[string]interface{}, key string) bool {
	_, ok := obj[key]
	return ok
}

// dispatchMCPModern dispatches one modern-era request: metadata, then version,
// then method.
func (a *App) dispatchMCPModern(req mcpRequest, meta map[string]interface{}, continuation *mcpContinuation) mcpResponse {
	if invalid := mcpValidateMeta(meta); invalid != "" {
		return mcpErrorResponse(req.ID, mcpErrInvalidParams, invalid, nil)
	}
	version, _ := meta[mcpMetaProtocolVersion].(string)
	if version != mcpProtocolVersion {
		return mcpErrorResponse(req.ID, mcpErrUnsupportedProtocolVersion,
			"Unsupported protocol version", map[string]interface{}{
				"supported": []interface{}{mcpProtocolVersion},
				"requested": version,
			})
	}
	switch req.Method {
	case "server/discover":
		return a.handleMCPDiscover(req)
	case "tools/list":
		return a.handleMCPToolsList(req, true)
	case "tools/call":
		return a.handleMCPToolsCall(req, true, meta, continuation, nil, nil)
	default:
		return mcpErrorResponse(req.ID, mcpErrMethodNotFound,
			fmt.Sprintf("Method not found: %s", req.Method), nil)
	}
}

// dispatchMCPLegacy dispatches one legacy-era request, in that revision's own
// shapes.
func (a *App) dispatchMCPLegacy(
	req mcpRequest,
	session *mcpLegacySession,
	continuation *mcpContinuation,
	channel *mcpChannel,
) mcpResponse {
	switch req.Method {
	case "tools/list":
		return a.handleMCPToolsList(req, false)
	case "tools/call":
		return a.handleMCPToolsCallLegacy(req, session, continuation, channel)
	default:
		return mcpErrorResponse(req.ID, mcpErrMethodNotFound,
			fmt.Sprintf("Method not found: %s", req.Method), nil)
	}
}

// handleMCPInitialize answers the legacy-era handshake, and selects that era.
//
// The modern revision has no handshake; this method is what a legacy client
// opens with, and answering it is what puts this process into legacy semantics
// for every later request that carries no modern metadata. This server speaks
// one legacy revision, so it always answers with that one -- the negotiation
// rule says to answer with the latest version supported.
//
// The declared feature is advertised here too, under the key that revision
// gives a non-standard server capability: one name, two advertisements.
func (a *App) handleMCPInitialize(req mcpRequest, session *mcpLegacySession) mcpResponse {
	session.open(req.Params)
	return mcpResponse{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"protocolVersion": mcpLegacyProtocolVersion,
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
				"experimental": map[string]interface{}{
					mcpFeatureConsequentialConfirmation: map[string]interface{}{},
				},
			},
			"serverInfo": map[string]interface{}{
				"name":    a.Name,
				"version": a.Version,
			},
		},
	}
}

// handleMCPDiscover answers `server/discover`, the modern era's mandatory
// discovery call. It replaces the handshake: supported versions, capabilities
// and identity in one request. The declared feature is a NAME rather than a
// version number, so a client learns that this server runs the confirmation
// dance without having to infer it from a revision date.
func (a *App) handleMCPDiscover(req mcpRequest) mcpResponse {
	return a.mcpCompleteResult(req.ID, map[string]interface{}{
		"supportedVersions": []interface{}{mcpProtocolVersion},
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
			"extensions": map[string]interface{}{
				mcpFeatureConsequentialConfirmation: map[string]interface{}{},
			},
		},
		"instructions": a.Help,
		"ttlMs":        mcpCacheTTLMs,
		"cacheScope":   mcpCacheScope,
	})
}

// handleMCPToolsList responds with tool definitions for all non-hidden,
// non-interactive commands.
func (a *App) handleMCPToolsList(req mcpRequest, modern bool) mcpResponse {
	// Initialized rather than declared nil for the same reason buildJSONSchema
	// initializes `required`: encoding/json renders a nil slice as null, and an
	// app with no exportable command must publish an empty tools list, as
	// Python and TypeScript do.
	toolDefs := []interface{}{}

	// Collect leaf commands from top-level in insertion order
	for _, name := range a.cmdOrder {
		cmd, ok := a.commands[name]
		if !ok || cmd.Hidden || cmd.Interactive {
			continue
		}
		toolDefs = append(toolDefs, buildMCPToolDef(name, cmd))
	}

	// Collect leaf commands from groups (recursive) in insertion order
	for _, groupName := range a.groupOrder {
		grp, ok := a.groups[groupName]
		if !ok {
			continue
		}
		collectMCPToolsFromGroup(grp, []string{groupName}, &toolDefs)
	}

	if !modern {
		return mcpResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": toolDefs,
			},
		}
	}
	return a.mcpCompleteResult(req.ID, map[string]interface{}{
		"tools":      toolDefs,
		"ttlMs":      mcpCacheTTLMs,
		"cacheScope": mcpCacheScope,
	})
}

// collectMCPToolsFromGroup recursively collects MCP tool definitions from
// a group and its subgroups.
func collectMCPToolsFromGroup(group *Group, path []string, toolDefs *[]interface{}) {
	if group.Hidden {
		return
	}

	for _, cmdName := range group.order {
		cmd, ok := group.Commands[cmdName]
		if !ok || cmd.Hidden || cmd.Interactive {
			continue
		}
		dotted := strings.Join(append(path, cmdName), ".")
		*toolDefs = append(*toolDefs, buildMCPToolDef(dotted, cmd))
	}

	for _, subName := range group.groupOrder {
		subGroup, ok := group.Groups[subName]
		if !ok {
			continue
		}
		collectMCPToolsFromGroup(subGroup, append(path, subName), toolDefs)
	}
}

// buildMCPToolDef builds an MCP tool definition for a single command.
// The tool name IS the dotted command path: dots are legal in MCP tool names,
// and the two sibling implementations have always published them unchanged.
//
// The classification sits BESIDE inputSchema, never inside it: it describes
// the tool, not an argument the caller passes. Same emission rule as the
// schema dump -- "effect" always, "consequential" only when true (absence
// means "not consequential").
func buildMCPToolDef(commandPath string, cmd *Command) map[string]interface{} {
	def := map[string]interface{}{
		"name":        commandPath,
		"description": toolDescription(cmd),
		"effect":      cmd.Effect,
		"inputSchema": buildJSONSchema(cmd),
	}
	if cmd.Consequential {
		def["consequential"] = true
	}
	return def
}

// mcpToolResult builds a tool result -- the one place the two eras' shapes
// differ.
func (a *App) mcpToolResult(reqID interface{}, text string, modern, isError bool) mcpResponse {
	body := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": text},
		},
	}
	if isError {
		body["isError"] = true
	}
	if !modern {
		return mcpResponse{Jsonrpc: "2.0", ID: reqID, Result: body}
	}
	return a.mcpCompleteResult(reqID, body)
}

// handleMCPToolsCallLegacy serves a legacy-era call, which may have to ask the
// client to confirm before it runs (§22.7).
func (a *App) handleMCPToolsCallLegacy(
	req mcpRequest,
	session *mcpLegacySession,
	continuation *mcpContinuation,
	channel *mcpChannel,
) mcpResponse {
	return a.handleMCPToolsCall(req, false, nil, continuation, session, channel)
}

// handleMCPToolsCall validates params, calls the command, and returns the result.
func (a *App) handleMCPToolsCall(
	req mcpRequest,
	modern bool,
	meta map[string]interface{},
	continuation *mcpContinuation,
	session *mcpLegacySession,
	channel *mcpChannel,
) mcpResponse {
	params := req.Params

	// Extract tool name
	nameVal, ok := params["name"]
	if !ok {
		return mcpResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error: &mcpError{
				Code:    mcpErrInvalidParams,
				Message: "missing required parameter: name",
			},
		}
	}
	toolName, ok := nameVal.(string)
	if !ok {
		return mcpResponse{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error: &mcpError{
				Code:    mcpErrInvalidParams,
				Message: "parameter 'name' must be a string",
			},
		}
	}

	// The tool name is the command path verbatim -- there is no name mangling
	// to undo, and therefore no ambiguity to guess at. An unresolvable name
	// reaches Call unchanged and surfaces as a tool-result error naming what
	// the caller actually sent.
	commandPath := toolName

	// Extract arguments (may be nil)
	var callArgs map[string]interface{}
	if argsVal, ok := params["arguments"]; ok {
		if argsMap, ok := argsVal.(map[string]interface{}); ok {
			callArgs = argsMap
		} else {
			return mcpResponse{
				Jsonrpc: "2.0",
				ID:      req.ID,
				Error: &mcpError{
					Code:    mcpErrInvalidParams,
					Message: "parameter 'arguments' must be an object",
				},
			}
		}
	}
	if callArgs == nil {
		callArgs = map[string]interface{}{}
	}

	// Consent is a top-level param, a sibling of "name" and "arguments" --
	// never a member of "arguments", which is the command's own argument
	// namespace and is published with additionalProperties: false. There is no
	// server-side default: absent means "not consented", and a consequential
	// tool is then refused.
	consented := false
	if consentVal, ok := params["approve_consequential"]; ok {
		consent, ok := consentVal.(bool)
		if !ok {
			return mcpErrorResponse(req.ID, mcpErrInvalidParams,
				"parameter 'approve_consequential' must be a boolean", nil)
		}
		consented = consent
	}

	if modern {
		resp, granted := a.mcpConfirmationExchange(
			req, commandPath, callArgs, meta, continuation, consented)
		if resp != nil {
			return *resp
		}
		consented = granted
	} else if !consented && session != nil && continuation != nil && channel != nil {
		decided, granted := a.mcpLegacyConfirmation(
			commandPath, callArgs, session, continuation, channel)
		if decided && !granted {
			return a.mcpToolResult(req.ID, errConfirmDeclined, false, true)
		}
		consented = granted
	}

	var callOpts []CallOption
	if consented {
		callOpts = append(callOpts, WithApproveConsequential())
	}

	// Call the command
	result, err := a.Call(commandPath, callArgs, callOpts...)
	if err != nil {
		// A command that ended early answers with its status as well as its
		// reason (§19.9).
		var ee *ExitError
		if errors.As(err, &ee) {
			return a.mcpToolResult(req.ID, errEarlyExitToolResult(ee.Code, ee.Message), modern, true)
		}
		return a.mcpToolResult(req.ID, err.Error(), modern, true)
	}

	// Marshal result to JSON text
	resultJSON, jsonErr := json.Marshal(result)
	if jsonErr != nil {
		return mcpErrorResponse(req.ID, mcpErrInternal,
			fmt.Sprintf("failed to marshal result: %s", jsonErr), nil)
	}

	return a.mcpToolResult(req.ID, string(resultJSON), modern, false)
}

// --- The continuation primitive (contract §22.4) ----------------------------
//
// The protocol is stateless: a server that needs more input answers with an
// input-required result and whatever it must remember, and the client echoes
// that back on a retry that is otherwise a fresh, independent request. The
// state therefore travels THROUGH the client, which makes it attacker-
// controlled input rather than server memory.

// mcpContinuationTTLSeconds is how long a minted continuation stays usable:
// long enough for a human to answer the confirmation the client renders, short
// enough that a captured blob is worth little.
const mcpContinuationTTLSeconds = 300

// mcpConfirmationKey is the key the confirmation elicitation is filed under, in
// both directions.
const mcpConfirmationKey = "consequential-confirmation"

// mcpContinuationPayload is what the blob carries, and what its MAC covers.
type mcpContinuationPayload struct {
	V    int    `json:"v"`
	JTI  string `json:"jti"`
	Prin string `json:"prin"`
	Exp  int64  `json:"exp"`
	Req  string `json:"req"`
}

// mcpContinuation mints and verifies the integrity-protected continuation state
// blob.
//
// The blob is `<payload>.<mac>`, both unpadded base64url, where the MAC is
// HMAC-SHA256 over the payload bytes under a key minted for this process and
// never emitted. A blob is therefore unforgeable without reading this process's
// memory, and worthless to any other process.
//
// The payload binds three things the protocol requires be checked on receipt --
// the principal it was issued to, an expiry, and a digest of the originating
// request -- plus a unique id, which is what makes single use enforceable:
// those three bound the replay window but do not close it.
type mcpContinuation struct {
	key      []byte
	consumed map[string]int64
}

func newMCPContinuation() *mcpContinuation {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Sprintf("strictcli: cannot mint a continuation key: %s", err))
	}
	return &mcpContinuation{key: key, consumed: map[string]int64{}}
}

// mcpB64URLAlphabet is the base64url alphabet, in value order -- the one
// spelling §22.4 defines.
const mcpB64URLAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

// mcpB64URLCanonical reports whether text is the ONE unpadded base64url
// spelling of some bytes.
//
// The three languages' decoders do not agree on their own -- Python's ignores
// stray characters and accepts padding, Node's ignores anything outside the
// alphabet, and Go's skips newlines -- and all three ignore the trailing bits
// of a final character that does not fill a byte, so a blob's last character
// can be altered without changing what it decodes to. The blob is
// attacker-controlled input, so the spelling is checked here, before any
// decoder sees it, identically in all three implementations.
func mcpB64URLCanonical(text string) bool {
	remainder := len(text) % 4
	if remainder == 1 {
		// No byte string encodes to a length one past a multiple of four.
		return false
	}
	last := 0
	for i := 0; i < len(text); i++ {
		value := strings.IndexByte(mcpB64URLAlphabet, text[i])
		if value < 0 {
			return false
		}
		last = value
	}
	if remainder == 0 {
		return true
	}
	// Two characters carry one byte and three carry two, so the final character
	// has 4 or 2 bits left over, and a canonical encoder leaves them zero.
	leftover := 0b11
	if remainder == 2 {
		leftover = 0b1111
	}
	return last&leftover == 0
}

// mcpB64URLDecode decodes canonical unpadded base64url; ok is false when the
// text is not one.
func mcpB64URLDecode(text string) (raw []byte, ok bool) {
	if !mcpB64URLCanonical(text) {
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(text)
	if err != nil {
		return nil, false
	}
	return decoded, true
}

func (c *mcpContinuation) mac(payload []byte) []byte {
	mac := hmac.New(sha256.New, c.key)
	mac.Write(payload)
	return mac.Sum(nil)
}

// mint issues a blob binding this principal, this request and a short window.
func (c *mcpContinuation) mint(principal, digest string, now int64) string {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		panic(fmt.Sprintf("strictcli: cannot mint a continuation id: %s", err))
	}
	payload := mcpContinuationPayload{
		V:    1,
		JTI:  base64.RawURLEncoding.EncodeToString(id),
		Prin: principal,
		Exp:  now + mcpContinuationTTLSeconds,
		Req:  digest,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("strictcli: cannot encode a continuation: %s", err))
	}
	return base64.RawURLEncoding.EncodeToString(raw) + "." +
		base64.RawURLEncoding.EncodeToString(c.mac(raw))
}

// verify verifies and CONSUMES a blob, returning "" when it is good and the
// refusal otherwise. A blob that passes every check is consumed here, so a
// second presentation is refused even though it is still perfectly well-formed,
// unexpired and correctly bound.
func (c *mcpContinuation) verify(blob, principal, digest string, now int64) string {
	parts := strings.Split(blob, ".")
	if len(parts) != 2 {
		return mcpErrStateVerification
	}
	raw, ok := mcpB64URLDecode(parts[0])
	if !ok {
		return mcpErrStateVerification
	}
	mac, ok := mcpB64URLDecode(parts[1])
	if !ok {
		return mcpErrStateVerification
	}
	if !hmac.Equal(mac, c.mac(raw)) {
		return mcpErrStateVerification
	}
	var payload mcpContinuationPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return mcpErrStateVerification
	}
	if payload.V != 1 || payload.JTI == "" {
		return mcpErrStateVerification
	}
	c.prune(now)
	if payload.Exp <= now {
		return mcpErrStateExpired
	}
	if payload.Prin != principal {
		return mcpErrStateWrongClient
	}
	if payload.Req != digest {
		return mcpErrStateWrongRequest
	}
	if _, spent := c.consumed[payload.JTI]; spent {
		return mcpErrStateReused
	}
	c.consumed[payload.JTI] = payload.Exp
	return ""
}

// prune forgets consumed ids that can no longer be replayed anyway.
func (c *mcpContinuation) prune(now int64) {
	for id, exp := range c.consumed {
		if exp <= now {
			delete(c.consumed, id)
		}
	}
}

// The continuation refusals, byte-identical in all three implementations.
const (
	mcpErrStateVerification = "requestState failed verification"
	mcpErrStateExpired      = "requestState has expired"
	mcpErrStateWrongClient  = "requestState was issued to a different client"
	mcpErrStateWrongRequest = "requestState does not match this request"
	mcpErrStateReused       = "requestState has already been used"
)

// mcpCanonicalJSON is a canonical encoding of a JSON value, for digesting: keys
// sorted, no insignificant whitespace, floats in the framework's canonical form
// -- so the digest depends on what the caller said, never on how their encoder
// spelled it.
func mcpCanonicalJSON(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		if v == math.Trunc(v) && math.Abs(v) < 1e15 {
			return strconv.FormatInt(int64(v), 10)
		}
		return formatFloatCanonical(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case string:
		return mcpCanonicalString(v)
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, mcpCanonicalJSON(item))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			parts = append(parts, mcpCanonicalString(key)+":"+mcpCanonicalJSON(v[key]))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		return mcpCanonicalString(fmt.Sprintf("%v", v))
	}
}

// mcpCanonicalString encodes one string the way the envelope does: plain UTF-8,
// escaping only what JSON mandates.
func mcpCanonicalString(s string) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(s); err != nil {
		return `""`
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// mcpRequestDigest digests the originating request: the method and its salient
// parameters.
func mcpRequestDigest(method, toolName string, arguments map[string]interface{}) string {
	material := method + "\n" + toolName + "\n" + mcpCanonicalJSON(arguments)
	sum := sha256.Sum256([]byte(material))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// mcpPrincipal is the client this state is minted for, as the client declares
// itself.
//
// On this transport there is no authenticated principal; the declaration is
// self-reported and the binding is a consistency check, not authentication.
// What actually contains a stolen blob is the per-process key.
func mcpPrincipal(meta map[string]interface{}) string {
	return mcpPrincipalOf(meta[mcpMetaClientInfo])
}

// mcpPrincipalOf reads one self-reported client identity. Both eras carry the
// same self-report -- per request, or once at the handshake.
func mcpPrincipalOf(value interface{}) string {
	info, ok := value.(map[string]interface{})
	if !ok {
		return ""
	}
	name, _ := info["name"].(string)
	version, _ := info["version"].(string)
	return name + "/" + version
}

// mcpClientDeclaresElicitation reports whether a modern request's client
// declared a form elicitation.
func mcpClientDeclaresElicitation(meta map[string]interface{}) bool {
	return mcpDeclaresFormElicitation(meta[mcpMetaClientCapabilities])
}

// mcpDeclaresFormElicitation reports whether a capabilities block says the
// client can render a form. An empty `elicitation` object means form mode, which
// the protocol states for compatibility with clients written before the modes
// existed. The two eras deliver their capabilities differently -- per request,
// or once at the handshake -- and read them the same way.
func mcpDeclaresFormElicitation(value interface{}) bool {
	caps, ok := value.(map[string]interface{})
	if !ok {
		return false
	}
	elicitation, ok := caps["elicitation"].(map[string]interface{})
	if !ok {
		return false
	}
	if len(elicitation) == 0 {
		return true
	}
	_, form := elicitation["form"].(map[string]interface{})
	return form
}

// mcpConfirmationRequest is the elicitation that asks a human, through the
// client, to confirm. Same words as the terminal prompt (§12.6) minus its
// keystroke hint: one vocabulary for one question, however it is delivered.
func mcpConfirmationRequest(cmdPath string) map[string]interface{} {
	return map[string]interface{}{
		"method": "elicitation/create",
		"params": map[string]interface{}{
			"mode":    "form",
			"message": fmt.Sprintf("about to run consequential command '%s'. Proceed?", cmdPath),
			"requestedSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"proceed": map[string]interface{}{
						"type":        "boolean",
						"title":       "Proceed",
						"description": "Whether to run the consequential command.",
					},
				},
				"required": []interface{}{"proceed"},
			},
		},
	}
}

// mcpInputRequired is the interim result: what is needed, and the state to echo
// back with it.
func (a *App) mcpInputRequired(reqID interface{}, cmdPath, requestState string) mcpResponse {
	return mcpResponse{
		Jsonrpc: "2.0",
		ID:      reqID,
		Result: map[string]interface{}{
			"resultType": "input_required",
			"inputRequests": map[string]interface{}{
				mcpConfirmationKey: mcpConfirmationRequest(cmdPath),
			},
			"requestState": requestState,
			"_meta":        a.mcpServerInfo(),
		},
	}
}

// mcpConfirmationVerdict reads one elicitation result: "accept", "reject",
// "absent" or "malformed".
//
// An `accept` carrying `proceed: false` is a refusal, not an approval: the
// action names what the client did with the dialogue, and the field is the
// answer to the question.
func mcpConfirmationVerdict(answer interface{}) string {
	if answer == nil {
		return "absent"
	}
	result, ok := answer.(map[string]interface{})
	if !ok {
		return "malformed"
	}
	action, _ := result["action"].(string)
	if action == "decline" || action == "cancel" {
		return "reject"
	}
	if action != "accept" {
		return "malformed"
	}
	content, ok := result["content"].(map[string]interface{})
	if !ok {
		return "malformed"
	}
	if proceed, ok := content["proceed"].(bool); ok && proceed {
		return "accept"
	}
	return "reject"
}

// mcpConfirmationExchange runs the confirmation round-trip for one call. It
// returns a response to send back (a refusal, or the interim result asking for
// confirmation) or, when there is none, the consent the call may proceed with.
func (a *App) mcpConfirmationExchange(
	req mcpRequest,
	toolName string,
	arguments map[string]interface{},
	meta map[string]interface{},
	continuation *mcpContinuation,
	alreadyConsented bool,
) (*mcpResponse, bool) {
	principal := mcpPrincipal(meta)
	digest := mcpRequestDigest("tools/call", toolName, arguments)
	consented := alreadyConsented
	now := time.Now().Unix()

	if stateVal, present := req.Params["requestState"]; present {
		state, ok := stateVal.(string)
		if !ok {
			resp := mcpErrorResponse(req.ID, mcpErrInvalidParams,
				"parameter 'requestState' must be a string", nil)
			return &resp, false
		}
		responses := map[string]interface{}{}
		if responsesVal, ok := req.Params["inputResponses"]; ok {
			responses, ok = responsesVal.(map[string]interface{})
			if !ok {
				resp := mcpErrorResponse(req.ID, mcpErrInvalidParams,
					"parameter 'inputResponses' must be an object", nil)
				return &resp, false
			}
		}
		if continuation == nil {
			resp := mcpErrorResponse(req.ID, mcpErrInvalidParams, mcpErrStateVerification, nil)
			return &resp, false
		}
		if refusal := continuation.verify(state, principal, digest, now); refusal != "" {
			resp := mcpErrorResponse(req.ID, mcpErrInvalidParams, refusal, nil)
			return &resp, false
		}
		switch mcpConfirmationVerdict(responses[mcpConfirmationKey]) {
		case "malformed":
			resp := mcpErrorResponse(req.ID, mcpErrInvalidParams,
				fmt.Sprintf("inputResponses['%s'] is not an elicitation result", mcpConfirmationKey), nil)
			return &resp, false
		case "reject":
			resp := a.mcpToolResult(req.ID, errConfirmDeclined, true, true)
			return &resp, false
		case "accept":
			consented = true
		default:
			// The state was good but the answer never came. The protocol says
			// to ask again rather than error -- with a fresh state, since the
			// one just presented is spent.
			resp := a.mcpInputRequired(req.ID, toolName, continuation.mint(principal, digest, now))
			return &resp, false
		}
	} else if _, present := req.Params["inputResponses"]; present {
		// An answer whose state is missing cannot be verified, and an
		// unverifiable answer is not an answer.
		resp := mcpErrorResponse(req.ID, mcpErrInvalidParams,
			"parameter 'inputResponses' requires the requestState it was issued with", nil)
		return &resp, false
	}

	if consented {
		return nil, true
	}
	cmd := a.lookupMCPCommand(toolName)
	if cmd == nil || !cmd.Consequential {
		return nil, consented
	}
	if continuation != nil && mcpClientDeclaresElicitation(meta) {
		resp := a.mcpInputRequired(req.ID, toolName, continuation.mint(principal, digest, now))
		return &resp, false
	}
	// A client that cannot render the confirmation is told what it would have
	// to declare, in the code the revision assigns -- never how to proceed
	// without confirming.
	resp := mcpErrorResponse(req.ID, mcpErrMissingClientCapability, mcpMsgMissingElicitation,
		map[string]interface{}{"requiredCapabilities": mcpRequiredElicitationCapabilities()})
	return &resp, false
}

// mcpLegacyConfirmation asks a legacy client to confirm, over a request the
// server sends. It reports whether there was anything to ask (decided) and, if
// so, whether the client accepted.
//
// decided is false when the command is not consequential, or when this client
// never declared it could render the form -- in which case the call reaches the
// consent seam unconsented and gets its refusal.
//
// The continuation blob rides as the JSON-RPC request id: JSON-RPC obliges the
// client to echo an id back verbatim, which is the same obligation the modern
// era puts on `requestState`, so the correlation needs no second mechanism. It
// is verified on return through the same path, and a matching id that fails any
// of those checks confirms nothing.
func (a *App) mcpLegacyConfirmation(
	toolName string,
	arguments map[string]interface{},
	session *mcpLegacySession,
	continuation *mcpContinuation,
	channel *mcpChannel,
) (decided, consented bool) {
	cmd := a.lookupMCPCommand(toolName)
	if cmd == nil || !cmd.Consequential {
		return false, false
	}
	if !mcpDeclaresFormElicitation(session.capabilities) {
		return false, false
	}
	principal := mcpPrincipalOf(session.clientInfo)
	digest := mcpRequestDigest("tools/call", toolName, arguments)
	now := time.Now().Unix()
	state := continuation.mint(principal, digest, now)

	ask := mcpConfirmationRequest(toolName)
	ask["jsonrpc"] = "2.0"
	ask["id"] = state
	channel.write(ask)

	answer := channel.awaitResponse(state)
	// Consumption is unconditional once the blob is on the wire: EVERY exit
	// below spends it, not just the one that reads a well-formed result. A blob
	// an aborted exchange left live is still bound to this principal and this
	// request digest for its whole five minutes, which is a `requestState` the
	// client can present on the modern path with an acceptance it wrote itself
	// -- for the very call the abort refused.
	verified := continuation.verify(state, principal, digest, time.Now().Unix()) == ""
	if answer == nil {
		// A stream that ended before an answer arrived. There is no re-ask in
		// this era: the server is holding the request open, so a non-answer is
		// a decision.
		return true, false
	}
	result, ok := answer["result"]
	if !ok {
		// A JSON-RPC error response answers the question with a failure.
		return true, false
	}
	if !verified {
		return true, false
	}
	return true, mcpConfirmationVerdict(result) == "accept"
}

// lookupMCPCommand resolves a dotted tool name to its command, or nil.
func (a *App) lookupMCPCommand(dotted string) *Command {
	parts := strings.Split(dotted, ".")
	if len(parts) == 1 {
		return a.commands[parts[0]]
	}
	group, ok := a.groups[parts[0]]
	if !ok {
		return nil
	}
	for _, name := range parts[1 : len(parts)-1] {
		group, ok = group.Groups[name]
		if !ok {
			return nil
		}
	}
	return group.Commands[parts[len(parts)-1]]
}
