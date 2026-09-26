# Deviations from `framework-owned-exits-output-and-bypass-enforcement.md`

Append-only. Each entry records what the plan says, what changed, and what is
built instead, at the moment it was ruled.

## strictcli does no source parsing

- **Plan:** a static lint in all three ports, run through the reserved flag
  `--lint-framework-use`, specified in the contract's §28, and run by rlsbl's
  release validation through each consumer's entry point.
- **Ruling:** strictcli must not parse source code at all. Every static rule
  moves to an external source-analysis tool, which rlsbl runs; this includes the
  existing `effects-bypass` check, not only the new lint.
- **Built instead:** strictcli keeps only the runtime pieces (the early exit, the
  goroutine helper, `ExitError`, the output writers, the prefixes, signal
  handling, child-stdout capture, and the runtime stdout guard). The lint
  scanners, `--lint-framework-use`, §28, its templates and conformance cases,
  the TypeScript token scanner, and `effects-bypass` are removed from strictcli.
  The lint implementations already built in the ports are deleted, not kept
  beside the external tool.

## Rulings added after the plan

- An unwaited child is a hard error: a handler that returns while a child it
  spawned is still running fails with exit 1 and a diagnostic naming the child,
  which is killed, in both modes. `Spawned` gains ways to signal and kill a
  child.
- `ExitError` carries the payload the command set before its early exit, in all
  three ports.
- The framework's own errors (`config set`, `config init`, and the check
  command's) go through the error writer in all three ports, and conformance
  asserts their exact stderr bytes.
- In-process invocation of a command that owns stdout returns everything written
  through `ctx.Document()` and an owns-stdout child's stdout to the caller.
- The Go runtime guard redirects stdout at the operating-system level on every
  platform (`dup2`/`dup3` from `golang.org/x/sys/unix` where the standard
  library lacks it, `SetStdHandle` on Windows).
- Confirmed as written in the contract: the signal exit code replaces the
  handler's; on an owns-stdout command a child's stdout is part of the document;
  `ExitNow` refuses code 0, codes outside 1..255, and an empty message.
