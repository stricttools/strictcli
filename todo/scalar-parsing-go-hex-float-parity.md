# Scalar parsing: Go-only hex-float acceptance

Two defects in the strict scalar-parsing layer, found by reading the parsers and
the conformance corpus side by side. Both verified against the working tree.

## Problem 2: Go accepts hex float literals; Python and TypeScript reject them

`typescript/src/values.ts` carries the comment: `Go-only hex floats ("0x10p2")
are rejected, matching Python.` — i.e. TS and Python deliberately reject hex
float syntax. But Go's `parseFloatStrictValue` (`go/strictcli/parse.go`) only
checks surrounding whitespace and then calls `strconv.ParseFloat`, which
accepts hex float literals (`"0x10p2"` parses to 64). So `--rate 0x10p2`
succeeds under the Go implementation and errors under Python/TS — a
cross-language parity break.

It survives because no conformance vector covers hex-float input: if one
existed, conformance-parity would already be red.

### Solutions

- **A. Reject hex floats in Go** (match the direction Python/TS already chose
  deliberately): guard before `strconv.ParseFloat` (e.g. refuse inputs
  containing `0x`/`0X`), and add a conformance case asserting rejection in all
  three implementations. Pros: restores parity toward the stricter behavior;
  matches the TS comment's stated intent. Cons: none apparent — no doc promises
  hex floats.
- **B. Accept hex floats everywhere.** Pros: none apparent. Cons: widens the
  accepted grammar in all three languages against the strict-parsing
  philosophy.

Whichever direction: add the hex-float conformance vector — the coverage gap is
what let the divergence survive, and per red-green the vector should exist (and
fail) before the fix.

## Affected files

- `go/strictcli/parse.go` (`parseFloatStrictValue`)
- `conformance/cases/` (new vector(s): hex float; leading zeros already covered
  by `boundary.json` — moves or changes only if Problem 1 resolves toward
  rejection)

## Effort

Small. Problem 2 (guard + vector): roughly an hour. Problem 1 is a decision
plus either doc edits (minutes) or a three-implementation behavior change with
error templates and parity updates (half a day).
