# Framework-owned exits, output writers, and enforcement against bypasses

## Context

Under `--json`, strictcli promises that stdout carries one document (the
`envelope` struct in the Go port), written by one exit step on every way out of
a command (`finishDispatch` / `runSealed` in `go/strictcli/strictcli.go`), and
that messages written through the Context writers are recorded in that
document's `diagnostics`. A survey of the consumers found these guarantees
bypassed on a large scale:

- process exits from inside handler code, which skip the exit step, so no
  `--json` document is written (Go `os.Exit` behind a `die()` helper; TypeScript
  `process.exit`);
- direct stdout writes beside the `--json` document;
- direct stderr writes, which never reach `diagnostics`;
- argv edited or parsed outside the framework;
- raw environment reads that `--help` and the schema never show.

Framework gaps invite these bypasses:

- Go has no way to end a command from deep in its call stack through the exit
  step (`Outcome` carries only a code; "A handler that calls os.Exit is outside
  this guarantee").
- No port has a writer for a command's main output (`Info` is hidden by
  `--quiet`), so consumers built their own, mostly without machine-mode handling.
- `OwnsStdout` commands have no framework writer for their document.
- `Spawn` and `Run(Stream(true))` connect a child's stdout to the process stdout
  in machine mode (`go/strictcli/effects.go`, `cmd.Stdout = os.Stdout`), a leak
  inside the framework itself.
- The existing `effects-bypass` check is opt-in, follows calls within one file
  or package, and is never run by the release tool, and consumers arrange code
  to stay out of its sight.

This work partly resolves `todo/error-payload-channel.md` (its "no document at
all" shape); that todo is split when this work is done.

## Decisions (the owner's rulings)

Every ruling below was the recommended option of its question, so each is
weakly held: walk it back when evidence turns against it.

- **Early exit.** A package-level `strictcli.ExitNow(code, message)` in Go
  panics with a private typed value that the exit step recovers (the same path
  the dry-run truncation signal uses); the command ends with `code`, the message
  is recorded as an error diagnostic, and deferred functions run. Python gains
  `strictcli.exit_now(code, message)`, TypeScript `throw new ExitNow(code,
  message)`. Code 0 is refused at call time as a programming error.
- **Goroutines.** Go gains `strictcli.Go(ctx, func())`, which runs a function on
  a new goroutine and delivers an `ExitNow` or panic raised there to the
  handler; `ExitNow` inside a raw `go` func literal is a lint finding.
- **In-process invocation.** When a command invoked in-process (`Call` / `call`)
  ends through `ExitNow`, the caller receives a typed error carrying the code
  and the message: Go `*ExitError{Code, Message}`, Python raises
  `strictcli.ExitError`, TypeScript rejects with `ExitError`.
- **Human-mode prefixes.** `ctx.Error` and `ExitNow` print `error: <message>`;
  `ctx.Warn` prints `warning: <message>`. Under `--json` the levels already
  carry the meaning and the text is stored unprefixed.
- **Main-output writer.** `ctx.Out` writes a command's human-readable answer. It
  is not hidden by `--quiet`. Under `--json` its text goes into a new top-level
  member `output` of the `--json` document (null when nothing was written);
  `interface_version` becomes 3.
- **Declared payload rendering.** A command with a payload schema may declare a
  renderer; human mode prints the rendering (not hidden by `--quiet`), machine
  mode emits only the payload. `ctx.Out` is refused at call time on a command
  with a declared renderer.
- **Document writer.** `ctx.Document()` returns a byte writer to the real
  stdout, usable only on commands that declared `OwnsStdout` (a hard error
  otherwise), untouched by `--quiet` and `--json`. `ctx.Out` is refused on such
  commands.
- **Child stdout.** Under `--json`, the stdout of a child run through `Spawn` or
  `Run(Stream(true))` is captured into the `output` member; human mode streams
  as before.
- **Signals.** strictcli catches SIGINT and SIGTERM during the handler and
  cancels the handler's context (Go `ctx.Done()`, and the Python and TypeScript
  equivalents); when the handler returns, the command exits 128 + the signal
  number with an error diagnostic naming the signal. A second signal gets the
  default action.
- **Environment input.** Every environment variable an app reads is declared
  through an existing mechanism (a flag's environment binding, a handshake,
  a connection, a location root) and appears in `--help` and the schema. No new
  environment-only declaration is added.
- **Runtime guard.** In machine mode the framework redirects the process stdout
  while the handler runs (at the file-descriptor level in Go and Python; by
  patching `process.stdout.write` in TypeScript). Bytes not written through the
  framework fail the run: exit 1 unless the code is already nonzero, and an
  error diagnostic `stdout written outside the framework: <n> bytes: "<first 4 KB>"`.
  Python and TypeScript also trap `os._exit` and `process.exit` during the
  handler.
- **Static lint.** A new check refuses, in every source file of a program built
  on strictcli: process exits (Go `os.Exit`, `log.Fatal*`; Python `sys.exit`,
  `raise SystemExit`, `os._exit`; TypeScript `process.exit`), direct stdout and
  stderr writes, argv access or rewriting, and environment reads that bypass the
  declared mechanisms, plus `ExitNow` in a raw `go` func literal.
  - Scope: in Go, every package linked into a binary whose main package imports
    strictcli; in Python, the package behind the project's console entry points
    that construct a strictcli app; in TypeScript, the modules reachable from
    the `bin` entries. Other programs in the repository (development scripts,
    generators, fake binaries used by tests) are not scanned. Test files are
    not scanned.
  - No allow-list, no skip flag, no severity downgrade.
  - It is run through a new framework-reserved flag, `--lint-framework-use`,
    present in every strictcli app without opt-in: it scans the program's
    source and exits nonzero listing each finding with file:line.
- **Release tool.** rlsbl's release validation runs `--lint-framework-use`
  through each strictcli consumer's own entry point, next to `--dump-schema`,
  and a finding blocks the release.

## Work in strictcli

### Contract and docs

Amend `.stricttools/docs/history/_effects-contract.md`:

- §3.5: the deliberate-exit row covers `ExitNow` / `exit_now` / the TypeScript
  throw in all three ports.
- §7.4: `ctx.Out` and declared renderings are not hidden by `--quiet`.
- §17: `os.Exit` and `process.exit` in command code are refused by the lint and
  trapped by the runtime guard where the language allows; the goroutine limit of
  `ExitNow` outside `strictcli.Go`.
- §19: `interface_version` 3, the `output` member, the prefixes, the runtime
  guard's failure, and the signal exit.
- §19.6: `ctx.Document()`.
- §18: a new entry recording these rulings.

Update `architecture.md`, the three quickstarts, `language-idioms.md`, and
`conformance.md`. The quickstarts' hello-world examples move from `ctx.Info` to
`ctx.Out`.

### Implementation, in all three ports

Every decision above, in Go, Python, and TypeScript. The `Spawn` /
`Run(Stream(true))` capture under `--json` in every port that has the
construct. The new check and `--lint-framework-use` in every port. The lint's
Go scanner resolves imports (an import alias of `os` must not escape it).

### Conformance

New cases pinning, in all three harnesses:

- `ExitNow`: human stderr, `--json` document, `--dry-run` log, `OwnsStdout`, and
  `--quiet`;
- `ctx.Out` in each mode;
- a declared renderer;
- `ctx.Document()`;
- a guard failure;
- a signal exit;
- the prefixes;
- `interface_version` 3.

Plus entries in `check_api_surface.py` and `check_error_parity.py`. The
goroutine helper, deferred-function execution, and the file-descriptor redirect
are covered by each port's unit tests.

### Red-green

Each guarantee gets a test that fails before the change and passes after it,
including:

- a Go handler calling `ExitNow` from a helper;
- a stray `fmt.Println` under `--json`;
- a `Spawn` child writing stdout under `--json`;
- a SIGTERM during a handler;
- a lint finding for each refused construct.

### Release

One monorepo release of the three releasables at the end, after a blind audit
against this file.

## Work outside strictcli, after its release

- **rlsbl:** run `--lint-framework-use` in release validation. Its own code is
  migrated too, including the argv rewrite in `rlsbl/__init__.py`, whose stated
  reason ("strictcli does not support variadic positional args") is out of date.
- **safegit:**
  - Its `die()` helper and every call site move to `strictcli.ExitNow`, and its
    output helpers move to `ctx.Out` and the Context writers.
  - Its hook-stop cap variable becomes the flag `--hook-kill-cap-s` on `push`
    and `hook run`, bound to `SAFEGIT_HOOK_KILL_CAP_S`, range 11-1800, default
    60.
  - `CLAUDE_CODE_SESSION_ID` becomes a declared handshake.
- **claudewheel:**
  - A bare invocation prints help; the launcher starts with `claudewheel
    launch`, and the `launch` rewrite in `claudewheel/cli.py` is deleted. The
    opt-in TUI layer in `todo/opt-in-tui-layer.md` stays a separate design.
  - Tokens after `--` become a declared variadic argument on `launch`.
- **howmuchleft:** its statusline mode becomes an explicit `statusline` command
  (with `--refresh-git-cache` declared on it), the pre-framework dispatch in
  `main.go` is deleted, and the statusline setting of every Claude Code profile
  calls the new command.
- **Every other strictcli consumer** is migrated until `--lint-framework-use`
  reports nothing and its `--json` runs pass the runtime guard, each followed by
  that consumer's own release.
