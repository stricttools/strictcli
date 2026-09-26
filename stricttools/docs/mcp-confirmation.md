+++
title = "Consequential Confirmation over MCP"
description = "How a strictcli MCP server asks a human before running a consequential tool: the input-required round-trip, the requestState blob, and the handshake era."
nav_group = "Guides"
nav_order = 25
+++

# Consequential Confirmation over MCP

Every strictcli command declares its effect on the world, and a command declared `consequential`
must be confirmed before it runs. At a terminal that confirmation is a prompt. Over the Model
Context Protocol it is this page: the server asks the question, the client puts it to a human, and
the answer comes back on the wire.

A client does not have to guess whether a server behaves this way. The server says so by name.

## What the server declares

`server/discover` advertises the feature as an extension identifier under the framework's own
vendor prefix:

```json
{"resultType":"complete",
 "supportedVersions":["2026-07-28"],
 "capabilities":{"tools":{},
                 "extensions":{"dev.smmh.strictcli/consequential-confirmation":{}}},
 "instructions":"<the app's help>",
 "ttlMs":3600000,"cacheScope":"public",
 "_meta":{"io.modelcontextprotocol/serverInfo":{"name":"myapp","version":"1.0.0"}}}
```

`dev.smmh.strictcli/consequential-confirmation` is a **name, not a version number**. It appears
whenever this server runs the dance described below, and a different name would appear only if the
dance changed in a way that breaks a client written against this one. Nothing about the
confirmation has to be inferred from a protocol revision date.

The other half of the declaration is per tool. `tools/list` publishes each tool's classification
beside its argument schema:

```json
{"name":"release","description":"cut a release",
 "effect":"mutating","consequential":true,
 "inputSchema":{"type":"object","properties":{},"additionalProperties":false}}
```

`effect` is always present; `consequential` appears only when it is true. A client can therefore
see which calls will be asked about before it makes one.

## What a client must declare

The server asks by sending an `elicitation/create` request in **form** mode, so the client must
declare it can render one. On the current revision (`2026-07-28`) capabilities travel on every
request:

```json
{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",
          "io.modelcontextprotocol/clientCapabilities":{"elicitation":{"form":{}}},
          "io.modelcontextprotocol/clientInfo":{"name":"my-client","version":"1.0.0"}}}
```

`{"elicitation":{}}` is equivalent to `{"elicitation":{"form":{}}}`, as the protocol specifies.
A client that declares only `{"elicitation":{"url":{}}}` cannot render this question and is
treated as not having declared it.

A consequential call from a client that did not declare form elicitation is answered with the
revision's own error rather than being run:

```json
{"jsonrpc":"2.0","id":1,
 "error":{"code":-32021,
          "message":"Server requires the elicitation capability for this request",
          "data":{"requiredCapabilities":{"elicitation":{"form":{}}}}}}
```

That is the whole answer: the error names what the client would have to be able to do, and never a
way to run the command without confirming it.

## The dialogue

Four messages, two of them the client's. The tool is `release`, declared `consequential`.

**1. The call, carrying no consent.**

```json
{"jsonrpc":"2.0","id":1,"method":"tools/call",
 "params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",
                    "io.modelcontextprotocol/clientCapabilities":{"elicitation":{"form":{}}},
                    "io.modelcontextprotocol/clientInfo":{"name":"my-client","version":"1.0.0"}},
           "name":"release","arguments":{}}}
```

**2. The question, and the state to bring back with the answer.**

```json
{"jsonrpc":"2.0","id":1,
 "result":{"resultType":"input_required",
           "inputRequests":{"consequential-confirmation":{
             "method":"elicitation/create",
             "params":{"mode":"form",
                       "message":"about to run consequential command 'release'. Proceed?",
                       "requestedSchema":{"type":"object",
                         "properties":{"proceed":{"type":"boolean","title":"Proceed",
                           "description":"Whether to run the consequential command."}},
                         "required":["proceed"]}}}},
           "requestState":"eyJ2IjoxLCJqdGkiOi...vZSJ9.Xb2K9r...",
           "_meta":{"io.modelcontextprotocol/serverInfo":{"name":"myapp","version":"1.0.0"}}}}
```

The message is the terminal prompt's own words minus its keystroke hint: one question, one
vocabulary, however it is delivered. `requestState` is opaque -- a client echoes it back verbatim
and never parses it.

**3. The retry, carrying the human's answer and the state.** It is an independent request, so it
carries a new JSON-RPC id.

```json
{"jsonrpc":"2.0","id":2,"method":"tools/call",
 "params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",
                    "io.modelcontextprotocol/clientCapabilities":{"elicitation":{"form":{}}},
                    "io.modelcontextprotocol/clientInfo":{"name":"my-client","version":"1.0.0"}},
           "name":"release","arguments":{},
           "requestState":"eyJ2IjoxLCJqdGkiOi...vZSJ9.Xb2K9r...",
           "inputResponses":{"consequential-confirmation":{
             "action":"accept","content":{"proceed":true}}}}}
```

**4. The result.** The command ran, once, with consent.

```json
{"jsonrpc":"2.0","id":2,
 "result":{"resultType":"complete",
           "content":[{"type":"text","text":"{\"released\":true}"}],
           "_meta":{"io.modelcontextprotocol/serverInfo":{"name":"myapp","version":"1.0.0"}}}}
```

### Every other answer

| The client sends | What happens |
|------------------|--------------|
| `{"action":"accept","content":{"proceed":true}}` | the command runs |
| `{"action":"accept","content":{"proceed":false}}` | aborted -- the action says what was done with the dialogue, the field is the answer to the question |
| `{"action":"decline"}` | aborted |
| `{"action":"cancel"}` | aborted |
| no `inputResponses` at all | the server asks again, with a **fresh** state |
| something that is not an elicitation result | `-32602 inputResponses['consequential-confirmation'] is not an elicitation result` |
| `inputResponses` without `requestState` | `-32602 parameter 'inputResponses' requires the requestState it was issued with` |

An abort is ordinary tool-result content -- `isError: true` with the text `aborted` -- because the
call was answered, not malformed.

## The state, and what it is protected against

`requestState` travels through the client, which makes it attacker-controlled input rather than
server memory, and the server treats it as such. The blob is `<payload>.<mac>` in unpadded
base64url, where the MAC is HMAC-SHA256 under a key minted for that server process and never
emitted. It carries a unique id, the client it was issued to, an expiry five minutes out, and a
digest of the originating request.

Verification is also consumption. In order: shape, MAC, payload version, expiry, principal,
request digest, and then the spent-id set. Each failure has its own message, all `-32602`:

```
requestState failed verification
requestState has expired
requestState was issued to a different client
requestState does not match this request
requestState has already been used
```

So a state cannot be forged, cannot be redeemed twice, cannot be moved to a different call, cannot
be moved to a different declared client, and is worthless to any other server process. The client
binding uses the client's self-reported `clientInfo`: on stdio there is no authenticated
principal, so it is a consistency check rather than authentication. What actually contains a
stolen blob is the per-process key, the five-minute window and the single use.

## What is not asked about

- A `read_only` or plain `mutating` command. Only `consequential` commands are confirmed.
- A call that states consent itself: `approve_consequential: true` as a **top-level** `tools/call`
  param, a sibling of `name` and `arguments`.

```json
{"jsonrpc":"2.0","id":1,"method":"tools/call",
 "params":{"_meta":{...},"name":"release","arguments":{},"approve_consequential":true}}
```

That is not human approval and is not meant to be: it is a caller stating, in the call, that it is
proceeding without a human. It is never a member of `arguments` -- that object is the command's own
argument namespace, published with `additionalProperties: false`, and a consent smuggled inside it
consents to nothing.

## The handshake era

The same server also answers the `initialize` handshake, and a client that opens with one is
served the last handshake-based revision, `2025-11-25`, for the life of the process:

```json
{"jsonrpc":"2.0","id":1,
 "result":{"protocolVersion":"2025-11-25",
           "capabilities":{"tools":{},
             "experimental":{"dev.smmh.strictcli/consequential-confirmation":{}}},
           "serverInfo":{"name":"myapp","version":"1.0.0"}}}
```

The feature is declared there too, under the key that revision gives a non-standard server
capability. What differs is the delivery. That revision has no input-required result: a server
that needs input sends a request of its own and waits. So an unconsented consequential
`tools/call` from a client whose handshake declared `capabilities.elicitation` produces, on the
same connection:

```json
{"jsonrpc":"2.0","id":"eyJ2IjoxLCJqdGkiOi...vZSJ9.Xb2K9r...",
 "method":"elicitation/create",
 "params":{"mode":"form",
           "message":"about to run consequential command 'release'. Proceed?",
           "requestedSchema":{"type":"object",
             "properties":{"proceed":{"type":"boolean","title":"Proceed",
               "description":"Whether to run the consequential command."}},
             "required":["proceed"]}}}
```

The client answers it like any server-to-client request, echoing the id:

```json
{"jsonrpc":"2.0","id":"eyJ2IjoxLCJqdGkiOi...vZSJ9.Xb2K9r...",
 "result":{"action":"accept","content":{"proceed":true}}}
```

and the original `tools/call` then completes. **The request id is the same continuation blob the
modern era puts in `requestState`**, verified on return through the same checks, so the two eras
share one mint-and-verify path and differ only in which field carries it.

Two differences a client should know about:

- **Everything that is not an explicit acceptance aborts** -- a decline, a cancel, an acceptance
  that says no, an unreadable result, an error response, or the connection ending. There is no
  re-ask in this era: the server is holding the request open, so a non-answer is a decision.
  Every one of those exits **spends the continuation**, exactly as an acceptance does: once the
  elicitation has been written, the blob is single-use whatever happens next. An aborted question
  therefore cannot be re-answered -- presenting its id as `requestState` on the modern path is
  refused with `requestState has already been used`.
- **A legacy client that declared no elicitation is refused, not asked**, with ordinary
  tool-result error content: `command 'release' is consequential: the call must carry
  confirmation`. It does not get `-32021`, which belongs to a revision that client is not
  speaking.

Anything a client sends while the server is waiting for the answer is held and served afterwards,
not dropped.

## One process, both eras

The two eras coexist in one server process, and nothing is inferred from a request's shape:

- A request carrying `_meta['io.modelcontextprotocol/protocolVersion']` is served statelessly under
  `2026-07-28`, whether or not a handshake happened earlier.
- An `initialize` request selects the legacy era for the process.
- A request that carries neither is refused with `-32602 missing required request metadata:
  _meta['io.modelcontextprotocol/protocolVersion']`.
- A version this server does not speak is refused with `-32022`, naming what it does speak:
  `{"supported":["2026-07-28"],"requested":"..."}`.
