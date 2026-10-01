# The framework should own channel management: what goes to stdin/stdout/stderr, and how

Filed from consumer-adoption experience. Related: item 1 (stdin) of
`todo/effects-run-gaps-stdin-settledness-tee-observe-dryrun.md`.

## Problem

The framework rigorously owns ONE channel contract — machine mode's
stdout carries exactly the envelope — but everything else about channel
discipline is left to each consumer's discretion, and consumers get it
wrong in ways the framework could make impossible:

- **Interactive prompts.** A consumer's hand-rolled consent prompt
  (the per-condition confirm pattern the framework's consequential
  protocol does not cover) naturally gets written with fmt.Printf — to
  STDOUT. A piped stdout then swallows the question and the operator
  stares at a hang. Prompts belong on stderr (or the controlling tty);
  nothing in the framework says so or provides the primitive.
- **Notices vs results.** Consumers invent per-case decisions about
  which informational lines go where, whether --quiet suppresses them,
  and whether machine mode re-routes them. Observed consumer drift:
  advisory notes printed via a stdout helper suppressed by --quiet in
  one command and deliberately unconditional on stderr in a sibling —
  same class of fact, opposite treatment, discovered only by audit.
- **Subprocess output.** A consumer re-emitting a captured child's
  stdout must remember to re-route it to stderr under machine mode or
  the envelope is corruptible. Each consumer rediscovers this.
- **Consent-vs-quiet interaction.** A consent prompt must never be
  suppressed by --quiet (silence must never manufacture consent); the
  correct non-interactive behavior is refusal, which the framework's
  own confirm protocol already implements — but the hand-rolled
  per-condition prompts each re-derive the rule.

## Proposal to consider

An authoritative channel policy, framework-owned, with primitives:

- `Prompt(...)`: writes to stderr (or /dev/tty), never suppressed by
  --quiet, refuses under machine mode and non-TTY stdin per the confirm
  protocol's existing rules — one implementation for the framework's
  own consequential prompt AND consumers' per-condition prompts.
- `Notice(...)` / `Result(...)` (names illustrative): informational
  lines with declared quiet/machine behavior vs primary results, so the
  suppression decision is made once by kind, not per call site.
- A documented contract: stdout carries results (and, in machine mode,
  only the envelope); stderr carries prompts, notices, progress, and
  re-emitted child stderr; machine mode re-routes any child stdout the
  consumer re-emits.
- Registration-time or vet-style discouragement of raw fmt.Printf in
  handlers, if cheaply expressible — the same philosophy as the
  reserved quartet: the framework owns the seams consumers would
  otherwise each hand-roll divergently.

## Why the framework

Every consumer CLI faces identical questions and the failure modes are
silent (hung pipes, corrupted envelopes, inconsistent quiet behavior).
Channel discipline is exactly the kind of cross-cutting contract the
effects regime already owns for mutations; output is the missing half.

## Effort

Design-bearing but contained: the primitives are small; the migration
story for existing consumers is per-command and incremental.

## Added later: which stream Info and Debug use

This file predates the `Out` writer and does not name the Info and Debug
writers. The owner ruled that the question of their stream is recorded here.

- **What the code does.** In plain-text mode Info and Debug write to stdout
  in all three ports (Go `go/strictcli/context.go`, the `Info` and `Debug`
  methods; Python `python/strictcli/__init__.py`, `info` and `debug`;
  TypeScript `typescript/src/context.ts`, `info` and `debug`). `--quiet`
  hides both; under `--json` both become `diagnostics` entries and nothing
  reaches stderr. Only Warn and Error write to stderr.
- **Nobody decided it.** Info was the handler's main output writer before
  `Out` existed. When `Out` was added (contract §19.10), Info was reclassified
  as a diagnostic, "what a command reports about itself", but its stream was
  not revisited. Debug moved from stderr to stdout in §24.6 to match the code,
  not by a ruling on streams.
- **The consequence.** A plain-text caller capturing stdout gets progress
  mixed with results. safegit, after moving all its output onto the writers,
  shows it: `x=$(safegit --verbose doctor --action diagnose)` captures the
  `checked: <name> (<duration>)` progress lines before the `[OK]` result lines.
  git itself keeps progress and advice on stderr. A program cannot route
  progress to stderr on its own: the only stderr writers prefix `warning: ` or
  `error: `.
- **Consumers use Info for both kinds of text.** Some print results through
  Info (strictexpr prints evaluated values; safegit printed commit results),
  others progress (cgofree's waiting lines). Moving Info or Debug to stderr
  would mean sorting every Info call into a result (`Out`) or a report (Info).
- **What a change would touch.** Each port's `info` and `debug`, the
  conformance cases that pin their stdout bytes, contract §7.4, §19.10, and
  §24.6, the docs pages describing the writers, the tests that assert Info on
  stdout, and the consumers' result-type Info calls.

The proposal above (stderr carries notices and progress) is one answer; the
alternative is keeping Info and Debug on stdout and stating why in the
contract.
