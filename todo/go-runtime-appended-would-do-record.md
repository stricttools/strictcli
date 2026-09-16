# Go: let a layered runtime append its own record to the would-do log

## Context

A runtime layered on the Go implementation adds doors the framework does not
have (a database scope whose `Exec` is a write). Under `--dry-run` such a
write should appear in the would-do log beside the framework's own records,
numbered with them, so a preview reads as one list. The effects handle
publishes its eight effect methods and no way to append a record of a
mutation the framework did not perform, so the layered runtime prints its
would-do lines on the diagnostics channel with a `would:` prefix, outside the
numbered log, and its previews read as two lists.

## Problem

The would-do log is closed to the runtime that wraps the handle, although
that runtime performs mutations the framework never sees.

## Proposal

An effects-handle method that records a mutation the caller describes, in
the same record shape the built-in effects produce (a kind, a resource, a
one-line rendering), numbered in the log and printed by `RenderLog`, and
carried into the machine-mode `effects` array under a kind the caller names.
Live mode records nothing (the caller performs the mutation itself). One
method per implementation where a handle exists, with a conformance case for
the rendered line and the machine array.

## Alternatives

- Keep printing on the diagnostics channel. Two lists per preview, and the
  machine-mode `effects` array under-reports what a dry run would do.

## Affected files

- `go/strictcli/effects.go` (the handle and its log), the machine-mode
  envelope writer, and the Python and TypeScript counterparts.
- `conformance/cases/` for the rendered line and the array.

## Effort

Small to medium.
