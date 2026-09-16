# Go: per-dispatch effect state, so two Call dispatches can run at once

## Context

A runtime layered on the Go implementation serves HTTP requests by turning
each request into a programmatic dispatch through `App.Call`. It wanted two
concurrent requests to be two dispatches with nothing shared but the app.
That is not possible today: `App.Call` runs `beginDispatch`, which replaces
the app-level effect log (`App.effects`) at the start of every call, so two
calls at once share one log and a handler's effects land in whichever log the
other installed. The race is visible under the race detector. The layered
runtime therefore serializes every dispatch under one lock, and a slow
handler holds up every other request of the server it serves.

## Problem

The effect log, the payload record and the exit record are per-app state
that a dispatch installs and tears down, so `App.Call` is not reentrant and
not safe for concurrent use, while its signature and docs suggest an ordinary
in-process call.

## Proposal

Move the per-dispatch state (the effect log, the emitted payload, the
outcome) from the `App` onto the `Context` the dispatch creates, so that
`Call` builds a fresh context with its own log and two calls never touch each
other's. `App.Test` keeps its stream capture serialized (stdout and stderr
are process-global), documented as such; `Call` becomes safe for concurrent
use and its doc says so. A test dispatches two handlers concurrently under
the race detector and asserts each sees only its own effects.

## Alternatives

- Document `Call` as not concurrency-safe and leave the caller to lock. That
  is the state today; it makes every server built on the framework
  single-threaded at the dispatch.

## Affected files

- `go/strictcli/app.go` (`beginDispatch`, `endDispatch`, `Call`), the effects
  log's owner, `context.go`, and the tests of `Call`.
- Python and TypeScript: check whether `call` has the same shape and file the
  parity change if it does.

## Effort

Medium: a state move inside one implementation plus a concurrency test.
