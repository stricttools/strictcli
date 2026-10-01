# Scalar parsing: stale "no leading zeros" doc claim

Two defects in the strict scalar-parsing layer, found by reading the parsers and
the conformance corpus side by side. Both verified against the working tree.

## Problem 1: docs claim "no leading zeros" but conformance asserts acceptance

`conformance/cases/boundary.json` contains a case passing `--port 007` to an
`int` flag and expecting exit 0 with `port=7` — acceptance, in all three
implementations. Multiple docs claim the opposite (search anchor: the phrase
"leading zeros"):

- `docs/_CLAUDE.md` — "no leading zeros in Go"
- `docs/go-quickstart.md` — unqualified "no leading zeros"
- `docs/python-quickstart.md` — unqualified
- `docs/typescript-quickstart.md` — two occurrences, unqualified
- `docs/flag-system.md` — "no leading zeros (in Go)"
- `docs/architecture.md` — unqualified
- generated `docs/_build/` outputs derived from the above

The docs and the committed conformance expectation contradict each other; one of
them is wrong, and the choice should be deliberate.

### Solutions

- **A. Fix the docs** — delete the claim; behavior stays "leading zeros
  accepted, parsed base-10". Pros: no behavior change; conformance already pins
  acceptance in all three languages. Cons: `007` silently meaning 7 can
  surprise octal-minded callers, and the strict-parsing philosophy arguably
  wants the stricter reading.
- **B. Make the docs true** — reject leading zeros in all three parsers, add an
  error template (plus error-parity entry) and a conformance case asserting
  rejection. Pros: stricter; removes the octal-ambiguity class entirely, which
  fits "declare everything, infer nothing". Cons: breaking change for callers
  passing zero-padded integers (dates, IDs); needs a changelog entry and a
  migration note.

Either way the current contradictory state is the defect.

## Affected files

- `conformance/cases/` (new vector(s): hex float; leading zeros already covered
  by `boundary.json` — moves or changes only if Problem 1 resolves toward
  rejection)
- `docs/_CLAUDE.md`, `docs/go-quickstart.md`, `docs/python-quickstart.md`,
  `docs/typescript-quickstart.md`, `docs/flag-system.md`,
  `docs/architecture.md` (+ regenerate `docs/_build/`)
- error template files (`go/strictcli/errors.go`, Python inline templates,
  `typescript/src/errors.ts`) only if a new error message is introduced

## Effort

Small. Problem 2 (guard + vector): roughly an hour. Problem 1 is a decision
plus either doc edits (minutes) or a three-implementation behavior change with
error templates and parity updates (half a day).
