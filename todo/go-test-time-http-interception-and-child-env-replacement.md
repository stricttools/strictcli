# Go: a test-time HTTP interception hook and a replacing child environment

## Context

A runtime layered on the Go implementation runs commands under a per-test
world: a fresh directory the program's declared access is mapped into, a
hygiene environment for children, and a network that is answered by the test
or not reached at all. Building that against the current surface needed two
process-global workarounds inside the layered runtime, both documented there
and both windows held inside its serialized dispatch:

- it points `http.DefaultClient.Transport` at its own transport for the
  duration of a dispatch, because the effects handle's `HTTP` exposes no
  client, transport or hook a test could answer through;
- it empties the process environment (keeping `PATH`) around a `Run` or
  `Spawn`, because `EffectEnv` merges over the inherited environment and
  cannot drop a variable, so a credential the parent holds always reaches the
  child.

A third gap was met on the same path: `Response`'s fields are unexported and
there is no constructor, so a layered runtime cannot mint the unsettled
carrier a dry run would return in order to answer an unanswered request; the
one it can build carries a nil effect log and reading it segfaults through
the truncation seam.

## Problem

The effects handle has no test-time seam for the network and no way to give
a child a closed environment. Every consumer that wants a hermetic test of a
command that fetches or spawns has to reach around the framework at process
scope.

## Proposal

- **An HTTP seam declared on the app or on `Test`**: a `WithHTTPTransport(rt
  http.RoundTripper)` app option, or a `Test` option carrying a responder,
  honored by the effects handle's `HTTP` in live mode. The responder shape a
  layered runtime already uses: method, URL, headers and body in; status and
  body out.
- **A replacing environment option**: beside `EffectEnv` (merge), an
  `EffectEnvExact(map[string]string)` (or a `ClearEnv()` option composed with
  `EffectEnv`) that gives the child exactly the named variables and nothing
  inherited. The merging option's doc should say it cannot remove a variable.
- **A mint for an unsettled `Response`** usable by a layered runtime, or a
  documented statement that the carrier is framework-private and a request
  the framework does not perform must be refused at the call rather than
  answered with a carrier. Either closes the segfault path.

Each in all three implementations where the surface exists, with
conformance cases where behavior is observable through `test()`.

## Alternatives

- Leave the workarounds in consumers. They are process-global, invisible to
  the framework, and every consumer rediscovers the segfault.

## Affected files

- `go/strictcli/effects*.go` (the handle's `HTTP`, `Run`, `Spawn`, the
  options), `go/strictcli/app.go` (an app option) or the test entry point.
- Python and TypeScript counterparts of `EffectEnv` and `HTTP` for parity.
- `conformance/cases/` for the environment option and the responder.

## Effort

Medium: three small surface additions and their parity.
