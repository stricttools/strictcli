+++
title = "Cross-Language Conformance"
description = "How twelve conformance checks keep the Python, Go and TypeScript implementations behaviorally identical, from JSON cases to byte-identical schema dumps."
nav_group = "Guides"
nav_order = 10
+++

# Cross-Language Conformance

strictcli ships three independent implementations -- Python, Go, and TypeScript -- that must behave identically for the same inputs. The conformance suite is the mechanism that enforces this. It lives in `conformance/` at the repository root and is structured as a `dev_node` project: never released independently, existing solely as test infrastructure.

## What the suite covers

The conformance suite enforces behavioral parity through twelve checks, all gated
at error severity so that any failure blocks a release. These checks cover API
surface consistency, error message parity, the suite's own extraction and
registry surfaces, per-target test case execution, cross-target output
comparison, schema structure identity, canonical float formatting, and the
process trace store's observational-only sweeps across all three strictcli
implementations:

| Check | What it verifies |
|-------|-----------------|
| `api-surface` | Every public API field (flags, args, app options, etc.) exists in all three implementations and in the conformance schema, accounting for language-idiomatic name mappings |
| `error-parity` | Error message templates in `python/strictcli/__init__.py`, `go/strictcli/errors.go`, and `typescript/src/errors.ts` produce identical user-facing messages for identical inputs |
| `conformance-meta` | The suite's own meta-tests (`test_error_parity_extraction.py`, `test_run_registry.py`, `test_api_surface_registry.py`, `test_lock_pin.py`), which pin the extraction and registry surfaces whose silent drift produces a false PASS rather than a visible error, plus the lockfile's editable-sibling pin |
| `conformance-python` | All JSON test cases pass against the Python implementation |
| `conformance-go` | All JSON test cases pass against the Go implementation |
| `conformance-typescript` | All JSON test cases pass against the TypeScript implementation |
| `conformance-parity` | N-way output comparison: for every case that runs on multiple targets, stdout and stderr must be byte-identical (after normalization) |
| `schema-parity` | A rich app definition exercising all features produces **byte-identical** `--dump-schema` output from all three implementations |
| `schema-fragments` | Every `value_schema` in every target's dump is a valid document of the closed four-keyword subset, every entry that must carry one does, and a choice flag carries none |
| `float-fuzz` | The strictcli canonical float format (SCF) produces byte-identical strings for a fixed set of double-precision bit patterns across all three implementations |
| `schema-freshness` | The committed `.strictcli/schema.json` for the conformance tool itself matches its current in-memory schema |
| `trace-sweeps` | The process trace store stays observational: a forged ancestry identifier and an unwritable store each leave stdout, stderr and the exit code byte-identical to the same run without them |

## Guaranteed-identical behaviors

The following behaviors are tested to be byte-identical across Python, Go, and
TypeScript. Every entry in this list is enforced by at least one conformance
check, and any divergence in these areas blocks a release:

- **Error messages.** Every parse-time and registration-time error produces the same text. Error templates are centralized in each implementation's `errors` module and cross-checked by `check_error_parity.py`.
- **Help text.** App-level, group-level, and command-level help output is formatted identically, including column alignment, section ordering (`Commands:`, `Deprecated:`, `Infrastructure:`), and the `Use '<app> <command> --help'` footer.
- **Exit codes.** Every case asserts a specific exit code, and parity mode verifies all targets agree.
- **Flag parsing.** Type coercion (str, bool, int, float), default resolution, env var resolution (including `1|true|yes` / `0|false|no` for booleans), config file loading, choice-flag election and scope enforcement, constraint enforcement (at-least-one, all-or-none, requires, implies, including nesting and the `present` / `true` / `non_empty` election selectors), and negatable booleans (`--no-flag`).
- **Float formatting.** The SCF canonical form is byte-shared: a fixed seed of double-precision bit patterns is formatted identically by all three implementations, verified by `check_float_fuzz.py` and the committed vectors in `conformance/float_vectors.json`.
- **Schema output.** `--dump-schema` produces **byte-identical** JSON at `schema_version: 2` describing the full CLI structure. Since v2 the comparison is byte equality rather than a structural one -- the normalization layer that used to reconcile three serializers is deleted, and one canonical encoding (declared key order at every depth, SCF floats, JSON-mandated escaping only, exactly one trailing newline) is what makes that possible. `check_schema_fragments.py` additionally validates every `value_schema` in every dump against the closed four-keyword subset.
- **Provenance labels.** Source labels (`cli`, `env`, `config`, `default`, `implied`, `infra`) are identical strings across implementations.
- **Config subsystem.** `config show`, `config set`, `config path`, `config edit`, `config init` produce identical output and behavior.
- **Check system.** Tag DSL evaluation, DAG-ordered execution, dependency pull-in, cascade skips, and result formatting all behave identically.
- **Hermetic mode.** `--hermetic` suppresses env and config identically; mutual exclusion with `--config` and config subcommands produces identical errors.
- **Machine mode.** The framework-owned `--json` is refused identically at every declaration level (command flag, app global, flag-set, and a choice's scope at any depth), is recognized in the same argv positions with the same two boundaries (a bare `--`, a passthrough command's name), and prints the same payload bytes -- plain UTF-8 with no HTML escaping, and structurally exempt from `--quiet`. The document is `interface_version` 3 and carries the `output` member in every run; a stray direct write to stdout fails the run with the same diagnostic in all three.
- **Exits and output.** The early exit (`ExitNow` / `exit_now` / `throw new ExitNow`) ends a command with the same stderr line, `--json` document, dry-run log, and exit code in all three; `ctx.out` prints the same bytes in every mode and is untouched by `--quiet`; a declared payload renderer and the owns-stdout document writer behave identically; `ctx.error` and `ctx.warn` print the same `error: ` and `warning: ` prefixes; and a SIGINT or SIGTERM during a handler ends the command with the same exit status and diagnostic.

## How testing works

### JSON test cases

The core of the suite is the JSON files in `conformance/cases/`, holding the
individual test cases organized by feature area (flags, config,
checks, choice flags, groups, etc.). Each case is a self-contained JSON object specifying an app definition,
argv input, optional environment variables, and expected output assertions
including exit code, stdout content, and stderr content:

- `app`: a declarative app definition (commands, flags, args, groups, config, checks)
- `argv`: the command-line arguments to pass
- `env`: optional environment variables to set
- `stdin`: optional text piped to the app's stdin. Absent means `/dev/null`, which is what keeps every other case independent of the operator's terminal (a pipe carrying this text is not a TTY either). Used by the `--mcp` cases, whose JSON-RPC lines arrive on stdin.
- `protocol_script`: an alternative to `stdin` for exchanges whose next request depends on the previous reply (see below). The two are mutually exclusive.
- `expect`: assertions on exit code, stdout, and stderr (exact match, substring, regex, negation), plus structural assertions on the effect log (`effects_equals`) and on an emitted `--dump-schema` document (`schema_command_keys`, `schema_command_absent_keys`)
- `targets`: restricts which implementations run the case (see [Target restrictions](#target-restrictions))
- `acknowledged_divergence`: declares intentionally language-specific output (see [Acknowledged divergence](#acknowledged-divergence))

Every case is validated against `conformance/schema.json`, a JSON Schema that defines the full vocabulary of app definitions and expectations.

#### Line-scripted protocol cases

A `protocol_script` case drives a request/response exchange step by step instead
of handing the whole input over up front. Each step writes one line to the
child's stdin (`send`), and/or reads exactly one reply line from `stdout` or
`stderr` (`stream`) and asserts on it (`expect_line`, with `equals`, `contains`,
`matches`, or `json_equals`). A missing reply line within the step timeout is a
failure, never a hang. The child's full streams are still available to the
ordinary `expect` block and to the N-way comparison. This is what the MCP loop
and its confirmation round-trip need, since their next request depends on the
previous reply.

#### Aborting handlers

`handler_aborts: true` on a command makes the generated handler unwind instead
of returning: it raises (Python), throws (TypeScript), or panics (Go) with the
identical message `conformance: handler aborted`, after running its
`handler_effects` and `handler_diagnostics`. This is the language-neutral way to
reach the framework's unwinding paths, such as the aborted-preview marker --
unlike `handler_returns` kind `bad`, which Go's `Outcome` type makes
unrepresentable. Only `true` is declarable, and it excludes `handler_returns`:
an aborting handler has no return.

#### Exits, output writers, and signals

A family of command keys drives the framework's exit step and output writers
from a case, in a fixed order after `handler_effects` and `handler_diagnostics`:
`handler_signals_self` (the handler sends itself `SIGINT` or `SIGTERM` and waits
for its context to be canceled), `handler_out` (one `ctx.out` call per string),
`handler_document` (bytes written through `ctx.document()`), and
`handler_raw_stdout` (bytes written to the process stdout directly, outside the
framework, which is how a case reaches the runtime guard). The last step is one
of `handler_exit_now` (`{code, message}`, made from a helper function in Go so
the early exit starts below the handler's own frame), `handler_process_exit`
(the language's own process exit, Python and TypeScript only), `handler_aborts`,
`handler_returns`, or the `handler_prints` path. A command-level
`payload_renderer: {"template": ...}` declares a payload renderer that replaces
each `{key}` with the payload's top-level member of that name.

#### The `$ANY` wildcard

Structural assertions (`effects_equals`, `schema_command_keys`, and
`json_equals` inside a protocol step) compare parsed JSON, so key order and
encoder whitespace are never part of the assertion -- which is what lets one
expectation serve three different serializers. Within those comparisons the
string `"$ANY"` is the per-field wildcard: it matches any actual value of any
type, asserting that the field is present and nothing about what it holds. It
exists for values that are nondeterministic by construction, such as a
continuation signature or a timestamp.

### Per-target execution

The test runner (`run.py`) drives each target differently, using language-specific
harnesses that construct the app definition at runtime from the JSON
specification. Each harness reads the app definition from a temp file whose path
is passed via the `CONFORMANCE_APP_DEF` environment variable:

- **Python**: `ref_python.py` generates a standalone Python script from the app definition. The script imports the Python `strictcli` package and constructs the app programmatically.
- **Go**: a persistent harness binary (`conformance/harness/`) is compiled once per run. It reads the app definition from a temp file (path passed via the `CONFORMANCE_APP_DEF` env var) and interprets it at runtime, constructing the Go app dynamically.
- **TypeScript**: the TypeScript package is built once per run (`npm run build` in `typescript/`). The harness (`conformance/harness_ts/main.js`) is a plain Node ESM script that imports the built dist and interprets the app definition from `CONFORMANCE_APP_DEF`, the same way as Go.

### Parity mode

`run.py --both` runs every case against all applicable targets and compares
outputs N-way, verifying that the three implementations produce byte-identical
stdout and stderr for every test case after normalizing temporary paths. This is
the primary cross-language consistency gate. For each case:

1. All applicable targets run the case independently.
2. If all targets pass their own assertions, stdout and stderr are compared byte-for-byte (after normalizing temp paths). Any divergence is reported with the odd-one-out identified by majority vote.
3. If some targets pass and some fail, that is a parity failure (fatal).
4. If all targets fail, that is a consistent failure (not a parity break).

### Acknowledged divergence

Some outputs are intentionally language-specific (parser prose, type names in error messages, idiomatic API names). Cases can declare an `acknowledged_divergence` block that excludes specific targets from byte-identity comparison on specific streams while requiring a mandatory reason:

```json
{
  "acknowledged_divergence": {
    "reason": "Python names int/None and reports the offending type as 'list'; TypeScript names number/undefined and reports 'Array'",
    "streams": {
      "stderr": ["python", "typescript"]
    }
  }
}
```

Each target's own `expect` block still runs -- acknowledgment only exempts the cross-target byte comparison. Stale acknowledgments (where the acknowledged target's output is actually identical to every other target) are reported as warnings.

### Target restrictions

Cases that are only expressible in some languages use the `targets` field to
restrict which implementations run the test. This is needed when a behavior is
unrepresentable in a language's type system, such as returning an invalid type
from a handler (impossible in Go's static type system but testable in Python and
TypeScript's dynamic runtime):

```json
{
  "targets": ["python", "typescript"]
}
```

Restrictions exist wherever a mis-declaration is expressible in one language and
not another -- the bad-return hard error, which Go's `Outcome` type makes
unrepresentable; the per-language registration guards whose sentence carries that
language's own spelling; an aborted-dispatch envelope case written twice, once
for Go and once for the other two. Of the 1437 cases, Go runs 1312, Python runs
1319 and TypeScript runs 1321.

### Differential argv fuzzing

`fuzz.py` generates random argv sequences and runs them against all three implementations of the same app definition, comparing results N-way. It reuses the same target harnesses as `run.py`. Divergences are identified by majority vote and minimized for debugging:

```bash
python conformance/fuzz.py --iterations 1000
python conformance/fuzz.py --iterations 100 --seed 42
```

### Supplementary checks

Beyond the JSON test cases, several Python scripts perform deeper structural
analysis across the three implementations, checking API surface completeness,
error message template parity, schema output identity, and float formatting
consistency:

- `check_api_surface.py` introspects Python classes, parses Go source via an AST dumper (`conformance/describe_go/`), and runs the TypeScript `describe` self-dump to verify every API field exists in all implementations and in the conformance schema.
- `check_error_parity.py` extracts error message patterns from all three implementations, normalizes them to a common signature form, and verifies symmetric coverage.
- `check_schema_parity.py` runs `--dump-schema` against all targets with a rich app definition and byte-compares the resulting files.
- `check_schema_fragments.py` reads all three dumps and validates every `value_schema` at every depth -- flag entries, arg entries, global flags, config fields, and every scoped entry inside a choice flag -- against the closed four-keyword subset, asserting the choice flag's absent fragment rather than tolerating it.
- `check_float_fuzz.py` formats a fixed set of double-precision bit patterns through all three formatters and asserts byte-for-byte agreement.
- `generate_pairwise.py` uses allpairspy to generate combinatorial test cases covering all 2-way flag feature combinations.

## Running conformance tests

All commands are run from the repository root unless otherwise noted. The test
runner supports filtering by case name, verbose output for debugging, and parity
mode for cross-target comparison. The full check gate runs all twelve checks in
dependency order.

### Single target

```bash
python conformance/run.py --target python
python conformance/run.py --target go
python conformance/run.py --target typescript
```

### Parity mode (all targets, cross-comparison)

```bash
python conformance/run.py --both
```

### With filtering and verbose output

```bash
python conformance/run.py --target python --filter "config" -v
python conformance/run.py --both --filter "hermetic" -v
```

### Full check gate (all twelve checks)

The full check gate runs all twelve conformance checks in dependency order from the
`conformance/` directory. It starts with the fast pure checks (api-surface,
error-parity and conformance-meta), then runs per-target conformance suites, then
parity and schema checks. All twelve checks must pass for a release to proceed:

```bash
uv run conformance check --tag pre-release
```

This runs all twelve checks in dependency order: `api-surface`, `error-parity` and `conformance-meta` first (fast, pure), then the per-target conformance runs, then parity and schema checks.

### Individual supplementary checks

```bash
cd conformance && python check_api_surface.py
cd conformance && python check_error_parity.py
cd conformance && python check_schema_parity.py
cd conformance && python check_float_fuzz.py
```

### Differential fuzzing

```bash
python conformance/fuzz.py --iterations 1000
```

## Adding a new conformance test case

### 1. Choose or create a case file

Cases are organized by feature area in `conformance/cases/`. Pick the file that matches your feature (e.g., `flags.json` for flag parsing, `config.json` for config subsystem, `checks.json` for the check system). Create a new file if the feature area does not exist yet.

### 2. Write the case

Each case is a JSON object in the file's top-level array. The app definition
is declarative -- commands specify `handler_prints` templates instead of actual
code, and the harness generates deterministic output by substituting flag and
arg values into the template at runtime:

```json
{
  "name": "feature-area: descriptive name of what is being tested",
  "app": {
    "name": "myapp",
    "version": "1.0.0",
    "help": "test app",
    "commands": [
      {
        "name": "greet",
        "help": "say hello",
        "flags": [
          {
            "name": "loud",
            "help": "shout",
            "type": "bool",
            "default": false
          }
        ],
        "handler_prints": "hello loud={loud}"
      }
    ]
  },
  "argv": ["greet", "--loud"],
  "expect": {
    "exit_code": 0,
    "stdout_equals": "hello loud=true"
  }
}
```

Key fields in `expect`:

| Field | Purpose |
|-------|---------|
| `exit_code` | Required. The expected process exit code. |
| `stdout_equals` / `stderr_equals` | Exact match (after whitespace normalization). |
| `stdout_contains` / `stderr_contains` | Substring(s) that must appear. |
| `stdout_not_contains` / `stderr_not_contains` | Substring(s) that must not appear. |
| `stdout_matches` / `stderr_matches` | Regex pattern(s) matched via `re.search`. |
| `config_file_contains` / `config_file_not_contains` | Substring(s) that must (or must not) appear in the seeded config file after the run. Reads the `config_content` / `config_content_late` temp file. |
| `config_file_matches` | Regex pattern(s) matched via `re.search` against the seeded config file after the run. Useful for asserting key ordering. |
| `effects_equals` | Deep-equality assertion against the structured effect log the run produced (effects contract §14.1). Compared in order over the parsed JSON arrays; absent optional keys and explicit-null keys are equivalent, and `recorded` is required on every record. |
| `schema_command_keys` | Per-command key assertions against the `.strictcli/schema.json` a `--dump-schema` run emitted. Maps a dotted command path (groups then command, e.g. `release.run`) to the keys that entry must carry, with their exact values. Requires `--dump-schema` in the case argv. |
| `schema_command_absent_keys` | The mirror of `schema_command_keys`: maps a dotted command path to keys that must NOT appear on that entry. Pins the emit-when-declared contract, where a key omitted by one implementation and emitted with a default by another is a silent divergence. Requires `--dump-schema` in the case argv. |

### 3. Use handler_prints for output

The `handler_prints` field on a command is a template string. `{name}` is replaced with the value of the flag or arg named `name`. `{source:name}` is replaced with the provenance source label. This is how conformance tests produce deterministic, comparable output without writing actual handler code.

### 4. Validate against the schema

Every case is validated against `conformance/schema.json` when loaded. If your case uses a novel app definition structure, the schema may need updating first. Cases that intentionally craft invalid app definitions can set `skip_schema_validation: true`.

### 5. Run and verify

```bash
# Run against all targets individually
python conformance/run.py --target python --filter "your case name" -v
python conformance/run.py --target go --filter "your case name" -v
python conformance/run.py --target typescript --filter "your case name" -v

# Run parity mode to verify cross-target byte identity
python conformance/run.py --both --filter "your case name" -v
```

### 6. Handle intentional divergence

If the new case has output that is legitimately language-specific, add an `acknowledged_divergence` block with a reason. At least one target per stream must remain unacknowledged to serve as the comparison baseline.

## Architecture notes

- The conformance suite is a `dev_node` in the monorepo's `workspace.toml`. It has no changelog, no JSONL entries, and cannot be released independently. It covers 3 target implementations with 12 automated checks.
- CI (`ci-router.yml`) runs the conformance checks on every push touching `conformance/**`, `python/**`, `go/**`, or `typescript/**`. A full conformance run exercises all 1437 test cases across all 3 targets (1312 on Go, 1319 on Python and 1321 on TypeScript, the difference being the target-restricted cases).
- The conformance tool itself is built with strictcli (dogfooding the check system). Its checks are declared in `conformance/conformance_tool/.strictcli/checks.toml`.
- Adding a new target to the suite is a data-entry task: register a new `Target` descriptor in `run.py` (one `_register_target(...)` call) and add corresponding entries in `check_api_surface.py`, `check_error_parity.py`, `check_schema_parity.py`, and `check_schema_fragments.py`. The orchestration, comparison, and reporting logic is fully target-agnostic.
