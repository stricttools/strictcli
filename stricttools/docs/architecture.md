+++
title = "Architecture and Internals"
description = "strictcli internals: the parse pipeline's four phases, presence, constraint and update resolution, registration checks, the v2 schema and the envelope, and the declared test-coverage root."
nav_group = "Guides"
nav_order = 10
+++

# Architecture and Internals

strictcli is a strict CLI framework implemented in Python, Go, and TypeScript,
kept in behavioral lockstep by a shared conformance test suite. This document
explains how the framework works internally, covering the parse pipeline, the
registration-time validation model, the schema format, the config system, flag
negation, and the error handling philosophy.

## Implementation layout

Each implementation has its own internal structure, but all follow the same
logical architecture. The Python implementation is a single-file module, the Go
implementation is split across roughly 20 source files, and the TypeScript
implementation uses roughly 30 source files compiled to ESM. Despite these
structural differences, the three codebases produce identical error messages,
identical help output, and identical schema JSON.

- **Python** (`python/strictcli/__init__.py`): single-file implementation, one
  external dependency (tomlkit for TOML editing).
- **Go** (`go/strictcli/`): split across ~20 source files, one external
  dependency (go-toml-edit for TOML editing).
- **TypeScript** (`typescript/src/`): split across ~30 source files (pure ESM,
  Node >= 22), two external dependencies (smol-toml for parsing,
  toml-eslint-parser for comment-preserving edits).

All three produce identical error messages, identical help output, identical
schema JSON, and pass the same conformance test cases.

## The parse pipeline

Every invocation flows through the same five-stage pipeline: reserved flag
pre-scan, global flag parsing, command routing, command parsing, and dispatch.
The stages are named identically in all implementations and execute in the same
order. The Go function names below appear alongside the Python and TypeScript
equivalents where they differ; the behavior at each stage is enforced to be
identical by the conformance test suite.

### Stage 1: reserved flag pre-scan

Before any global flag or command parsing begins, a pre-scan examines argv for
the framework-reserved flags and removes the ones it consumes. The scan splits
argv into two regions with different rulesets: the pre-command region recognizes
every reserved flag, while the command region recognizes only the effects-regime
quartet plus `--json`. Both regions stop at a bare `--`, and the command region
also stops at a passthrough command's name.

**The pre-command region** -- everything before the first non-flag token or a
`--` separator -- recognizes every reserved flag:

- `--dump-schema`: refused with an error naming `help --json`, which prints the
  help document (see [The schema format](#the-schema-format-strictclischemajson)).
- `--lint-framework-use`: scans the program's own source for constructs that
  bypass the framework and exits (see [The framework-use lint](#the-framework-use-lint)).
  It must be the only argument.
- `--mcp`: starts the MCP JSON-RPC server on stdin/stdout.
- `--hermetic`: enables hermetic mode (suppresses env vars and config file
  loading for the rest of the parse).
- `--config <path>`: overrides the config file path.
- The effects-regime quartet `--dry-run`, `--approve-consequential`, `--quiet`,
  `--verbose`.
- `--json`: selects machine mode. It is not a fifth member of the quartet --
  the four are the effects regime's own flags -- but it is delivered by the
  same rules, in both regions.

The pre-scan does not consume these tokens from argv for `--hermetic`,
`--config`, the quartet and `--json`; instead it records their presence and
builds a "cleaned argv" with them stripped out. `--dump-schema` (refused),
`--lint-framework-use` and `--mcp` cause an immediate return (no further parsing
occurs). `--help`, `-h`, `--version` and `-v` are stepped over, so a `--json`
after them is still seen, and refused: both flags are text only.

The pre-scan also skips over known global flags (long, short, and negation
forms) so a global-flag value that happens to look like a command name does not
end the region early.

**The command region** -- from the command token onward -- recognizes the
**quartet and `--json` only**, anywhere, exactly like `--help` / `-h`. `myapp deploy --dry-run`
and `myapp --dry-run deploy` are equivalent; `myapp dns zone create --dry-run`
works at any nesting depth. `--hermetic`, `--config`, `--dump-schema`,
`--lint-framework-use` and `--mcp` are *not* recognized here and become unknown-flag errors after the command token.

The command-region scan stops for good at two boundaries:

- **A bare `--`.** Every token after it is positional data, never a reserved
  flag: `myapp cmd -- --dry-run` passes the literal string `--dry-run` to the
  command.
- **A passthrough command's name.** A passthrough's args belong to the child
  process and are forwarded byte-for-byte, so `myapp exec --verbose child` gives
  the handler `["--verbose", "child"]` and leaves `ctx.verbose` false. The
  pre-command position is the escape hatch: `myapp --verbose exec --verbose child`
  sets `ctx.verbose` *and* still forwards the child's own `--verbose` untouched.

Anywhere-recognition costs exactly what `--help` already costs: a flag *value*
spelled like one of those tokens is eaten. Write `--message=--dry-run` or use
`--` to pass one literally.

**Go**: `App.preScanReservedFlags()` in `strictcli.go`.
**Python**: `App._pre_scan_reserved_flags()` in `__init__.py`.
**TypeScript**: `preScanReservedFlags()` in `parse.ts`.

### Stage 2: global flag parsing

The cleaned argv is scanned left-to-right for global flags. Global flags can
appear before the command name. The first token that is not a recognized global
flag (and is not `--`) is treated as the start of the command region; it and all
subsequent tokens become "remaining tokens."

Token forms recognized:

- `--flag-name value` (two tokens, value consumed)
- `--flag-name=value` (single token, `=`-split)
- `-x value` (short form, two tokens)
- `--no-flag-name` (boolean negation, single token)
- `--flag-name` alone for bool flags (sets to true)
- `--` stops global flag scanning; included in remaining tokens

After CLI tokens are consumed, env vars are resolved for any global flags that
were not set on the command line (skipped entirely under `--hermetic`). Then
config values are resolved for any global flags not set by CLI or env (again
skipped under `--hermetic`). Finally, defaults are applied for any global flags
still missing a value.

The result is a triple: `(global_values, global_sources, remaining_tokens)`.

**Go**: `App.extractGlobalFlags()` in `strictcli.go`.
**Python**: `App._parse_global_flags()` in `__init__.py`.
**TypeScript**: `extractGlobalFlags()` in `parse.ts`.

### Stage 3: command routing

The first token of the remaining argv selects a command or group. If it matches
a group, the router descends into that group and repeats the lookup with the
next token. This continues to arbitrary depth: App > Group > Group > ... >
Command.

At each level the router checks, in order:

1. Groups (descend if matched).
2. Commands (return the resolved command).
3. Deprecated commands (produce an error message with the deprecation notice).
4. Unknown (produce an error).

If a group is reached but no further tokens remain (or only `--help`), the
group's help text is displayed.

Before routing, a first word of `help` or `version` selects the framework's own
command: `help` parses its own options (`--depth`) and then an address -- groups,
a command, and one of the command's flags as typed -- and renders that page, or
the help document under `--json`; `version` prints the name and version. Inside
a group, `help` and `version` are refused naming the root form.

**Go**: `App.resolveCommand()` in `routing.go`.
**Python**: `App._resolve_command()` in `__init__.py`.
**TypeScript**: `resolveCommand()` in `routing.ts`.

### Stage 4: command parsing

The remaining tokens (after the command name was consumed by routing) are parsed
against the resolved command's declared flags and positional arguments. The
parser builds three lookup tables from the command's flags (long form, short
form, and negation form), then consumes tokens left-to-right. After CLI tokens
are processed, the value resolution cascade applies env vars and config values,
then runs constraint validation and an update command's at-least-one-property
rule, and only then applies defaults.

**Parsing is phased**, and the phase order is what makes order independence, the
distinct out-of-scope error and that error's priority over a missing required
flag fall out instead of being special-cased:

1. **Tokenize** every occurrence, without interpreting any of it.
2. **Resolve elections** for [choice flags](flag-system.md#choice-flags-a-choice-is-a-declaration-scope),
   outermost first, then recursively inside each elected choice.
3. **Validate scope membership** of every supplied flag.
4. **Resolve values and presence** within the live scopes only.

Error precedence follows that order -- **election, then scope, then value, then
presence** -- so a command line with several problems reports the same error
every time, never one that depends on declaration order. `--via sms --subject hi`
says *`--subject` belongs to `email`*, never *`--phone-number` is required*: the
spelling mistake is reported before its consequence.

**The precedence is normative for every command, choice-flag-free ones
included.** The phase order is a property of the parser, not of the declaration:
a structural problem -- an unknown flag, an unknown choice, a double election, a
scope violation -- is reported before a value problem -- a coercion failure, a
`validate` refusal, an at-prefix failure -- whether or not the command declares
a choice flag. Within a phase, ordering is command-line order, except in the
value phase, where every coercion failure is reported before any `validate`
refusal: a value is coerced as its token is consumed, and `validate` callbacks
run in a later pass over the command's declared flags.

Tokenization cannot wait for an election -- whether `--target` consumes the next
argv element is decided before any choice is elected -- which is why sibling
scopes may reuse a name only with an identical type and arity.

- **Long lookup**: `--flag-name` to flag definition.
- **Short lookup**: `-x` to flag definition.
- **Negation lookup**: `--no-flag-name` to flag definition (for negatable bool
  flags only).

Global flags are also added to these tables so they can be specified after the
command name.

Tokens are consumed left-to-right:

- If a token matches a long, short, or negation form, the corresponding value
  is consumed and stored.
- `--` stops flag parsing; all remaining tokens become positional arguments.
- Tokens starting with `-` that do not match any known flag are treated as
  positional arguments (allowing negative numbers like `-7`).
- All other tokens become positional arguments.

For non-bool flags, value coercion happens immediately at parse time:

- **str**: stored as-is after `@`-prefix resolution (`@file` reads from file,
  `@-` reads from stdin, `@@literal` strips the leading `@`).
- **bool**: `--flag` sets true, `--no-flag` sets false, no raw value accepted
  (`--flag=value` is an error).
- **int**: strict integer parsing -- no leading/trailing whitespace, no leading
  zeros (Go), 64-bit signed range. TypeScript uses `bigint` as its int type.
- **float**: strict float parsing in strictcli canonical form (SCF) -- NaN and
  Inf are rejected, the shortest round-trip representation is used.

After CLI tokens are consumed, the value resolution cascade runs (the env and
config steps are the two skipped under `--hermetic`):

1. **Env vars**: for each flag not set by CLI, check its declared env var. Bool
   env vars accept `1|true|yes` / `0|false|no` (case-insensitive). Repeatable
   flags split the env value by the declared env_separator. Dict flags parse the
   env value as JSON.
2. **Config file**: for each flag not set by CLI or env, check the loaded config
   data. Config values are coerced to the flag's type.
3. **Constraint validation**: `implies` injection first, then every declared
   constraint, children before parents. It runs here -- over the values CLI, env,
   config and `implies` supplied, and before any default exists -- because the
   engagement predicate only distinguishes anything while `default` and `infra`
   are still absent from the store.
4. **The at-least-one-property rule** of an update command, evaluated after every
   declared constraint has had its say and, like them, before defaults, over the
   same provided-ness predicate. The same pass computes the write set.
5. **Presence resolution**: for each flag still unset, act on its declared
   presence. A `default` declaration supplies its value (source `default`); an
   `optional` declaration delivers absence -- `None` / `nil` / `undefined` as a
   present key, also labelled `default`, since the declaration is what decided;
   a `required` declaration with no value from any source produces a "missing
   required flag" error. There is no silent empty-collection default: a
   repeatable or dict flag that wants `[]` / `{}` declares it.
6. **InfraRootPath resolution**: if a flag's default is a `RelativeToRoot`
   marker, it is resolved against the declared infrastructure roots at this
   point, and its source is labeled "infra" instead of "default."

Step 3's constraint validation covers four kinds. Exactly-one selection
is **not** among them: it is a choice flag, resolved in the election
phase above, and a constraint naming a scoped flag is a
registration-time error -- the scope already is the constraint.

- **at-least-one**: at least one member is engaged. Members may co-occur; the
  family has no upper bound and is never exclusivity.
- **all-or-none**: either every member is engaged or none is. With nothing
  engaged it is vacuously satisfied.
- **requires**: if flag A is present, flag B must also be present.
- **implies**: if flag A is present, flag B is automatically set to the implied
  value. If the user explicitly provided a contradicting value for B, it is a
  parse error.

A member of the two co-occurrence families names a flag, a positional arg, or
another named co-occurrence constraint, and carries an election selector --
`present`, `true` or `non_empty` -- deciding when it counts as **engaged**. A
nested constraint is engaged when at least one of its own members is: engagement
propagates upward, satisfaction does not. **Children are evaluated before
parents, siblings in declaration order**, so a violated nested constraint reports
its own sentence and its parent is never evaluated. `implies` injection runs
first, so an implied value can engage a member; defaults are applied after, so a
declared default cannot. See
[the constraint system](flag-system.md#constraints) for the declaration surface.

**An update command's at-least-one-property rule runs next**: after every
declared constraint has been evaluated, and before defaults are applied. Two
facts fix that position, each sufficient on its own. *After the constraints*,
because this is the framework's own rule about a declared property set rather
than a rule the command wrote, so a command with both faults reports the
constraint it declared -- including an `AtLeastOne` over optional identity
members. *Before defaults*, because it reads the **same** provided-ness predicate
the constraint system reads, and that predicate only distinguishes anything while
`default` and `infra` are still absent from the store; running it after would
make every defaulted flag look supplied. There is one predicate, one call site
and no source filter, so an env- or config-supplied property satisfies the rule
and renders in the write set beside a typed one. The same pass computes the write
set the run will publish. See
[update commands](flag-system.md#update-commands) for the declaration surface.

Finally, choices validation runs for any flag or arg with a declared set of
allowed values.

**Go**: `parseCommand()` in `parse.go`.
**Python**: `_parse_command()` in `__init__.py`.
**TypeScript**: `parseCommand()` in `parse.ts`.

### Stage 5: dispatch

The handler is called with the resolved context and the kwargs map once all flag
values have been resolved and constraints validated. First, inside the same span
that runs the handler, the framework loads the command's declared runtime
requirements in declaration order; the first that fails ends the command through
the early exit with exit 1 and `command '<path>' needs <name> (<help>), which is
not available: <reason>; install it: <install>`. This happens on every door --
the CLI, `test()`, `call()`, and MCP -- and in dry mode too, and the handler
reads each loaded value through `ctx.need` / `strictcli.Need`. Each implementation has its
own handler signature, but all share the same strict contract: the return value
must be an exit code, nothing (implicit exit 0), or a branded outcome carrying
the exit code. Any other return type is a hard error.

- **Go**: `func(ctx *Context, kwargs map[string]interface{}) Outcome`
- **Python**: `def handler(ctx, **kwargs)` returning `int`, `None`, or
  `outcome(code)`
- **TypeScript**: `(args, ctx) => number | undefined | outcome(code)`

The return value is interpreted strictly. In Go, the handler must return an
`Outcome` (created via `Exit(code)`). In Python, only `int`, `None`, or a
branded `outcome(...)` are accepted; any other return type is a hard error. In
TypeScript, the same contract applies with `number`, `undefined`, or a branded
`outcome(...)`.

Structured output does not ride the return value. A command declares its
payload's JSON Schema at registration time and its handler supplies the value
through `ctx.payload(value)` / `ctx.Payload(value)` -- at most once per
dispatch, and only on a command that declared a schema. The payload is printed
only under the framework-owned `--json`; `Test()` / `test()` and `Call()` /
`call()` capture it in either mode.

#### Output writers

The Context carries one writer for the command's answer and four for diagnostics:

| Writer | Human mode | Under `--quiet` | Under `--json` |
|--------|-----------|-----------------|----------------|
| `ctx.out` / `ctx.Out` | stdout, text plus `\n` | shown | appended to the `output` member |
| `ctx.debug` / `ctx.Debug` | stdout, only with `--verbose` | hidden | a `debug` diagnostic |
| `ctx.info` / `ctx.Info` | stdout | hidden | an `info` diagnostic |
| `ctx.warn` / `ctx.Warn` | stderr, as `warning: <message>` | shown | a `warn` diagnostic, unprefixed |
| `ctx.error` / `ctx.Error` | stderr, as `error: <message>` | shown | an `error` diagnostic, unprefixed |

`ctx.out` is refused at call time on a command that declares a payload renderer
and on a command that owns stdout. A command with a payload schema may declare a
renderer (`payload_renderer=` / `PayloadRenderer(fn)` / `payloadRenderer:`):
in human mode the exit step prints the rendering of the supplied payload, never
hidden by `--quiet`; under `--json` only the payload is emitted. A command that
declares stdout ownership writes its document through `ctx.document()` /
`ctx.Document()`, a byte writer to the real stdout in both modes, usable only on
such a command.

Under `--json`, a child started through `spawn`, or through `run` with `stream`
true, does not write to stdout: the framework captures its stdout into the
`output` member. On a command that owns stdout the child's stdout is part of
the document and goes to the real stdout in both modes.

#### Ending a command

A handler ends a command by returning, or early, from anywhere in its call
stack, through the same exit step: `strictcli.ExitNow(code, message)` (Go),
`strictcli.exit_now(code, message)` (Python), `throw new ExitNow(code, message)`
(TypeScript). The code is 1 to 255 -- a successful run returns -- and the
message is non-empty; either refusal is a programming error at the call. The
handler's deferred functions and `finally` blocks run, the message becomes an
`error` diagnostic (`error: <message>` on stderr in human mode), the would-do
log of a dry run renders as for a return, and the command exits with `code`.
Through `call()` / `Call()` the caller receives an `ExitError` carrying the code
and the message instead of a value. In Go, work started with a plain `go`
statement cannot end the command this way; `strictcli.Go(ctx, fn)` starts `fn`
on a goroutine whose `ExitNow` or panic is delivered to the command when the
handler returns.

On the CLI path, SIGINT and SIGTERM received while the handler runs cancel the
handler's context (`ctx.Done()` in Go, `ctx.canceled` in Python, `ctx.signal`
in TypeScript). When the handler returns, the command exits with 128 plus the
signal number and an `error` diagnostic `canceled by signal <SIGNAL>`. A second
signal gets the default action.

Process exits from handler code (`os.Exit`, `sys.exit`, `os._exit`,
`process.exit`) skip the exit step. They are refused statically by
`--lint-framework-use`, and in machine mode Python's `os._exit` and TypeScript's
`process.exit` are trapped while the handler runs and turned into a failed run
through the exit step.

### Machine mode and the envelope

Under `--json` the framework's stdout carries one document, serialized as JSON
and terminated by a single `\n`. Its `interface_version` is the document
contract's own version: **3**.

| Key | Type | Meaning |
|-----|------|---------|
| `interface_version` | integer | The document contract's own version -- `3`. |
| `app` / `app_version` | string | The app's declared name and version. |
| `command` | string \| null | The dotted command path, `null` when the run ended before a command resolved. |
| `exit_code` | integer | The process's exit status. |
| `payload` | any \| null | The machine payload the handler supplied, validated against the declared schema. |
| `output` | string \| null | The bytes `ctx.out` would have printed in human mode, plus the stdout of any child the framework captured, in arrival order. `null` when nothing was written. **Never absent.** |
| `dry_run` | boolean | Whether the run was in dry mode. |
| `writes` | object \| null | The write set of a command declaring an update; `null` on every command that declares none. **Never absent**, and populated in **both** modes. |
| `preview` | array | The structured effects; `[]` when nothing was recorded. |
| `preview_error` | object \| null | The terminal condition of a preview that did not finish. |
| `diagnostics` | array | Every diagnostic the run emitted, in emission order, with unprefixed messages. |

The `writes` member carries the resource, the write mode, and four arrays of
**underscored parameter names** in declaration order that partition the declared
property set exactly:

```json
{"interface_version":3,"app":"mytool","app_version":"0.1.0","command":"update-record",
 "exit_code":0,"payload":null,"output":null,"dry_run":true,
 "writes":{"resource":"dns-record","write_mode":"sparse","written":["content"],
           "cleared":["ttl"],"resent":[],"untouched":["proxied"]},
 "preview":[],"preview_error":null,"diagnostics":[]}
```

`resent` and `untouched` are the two readings of "the rest", and exactly one of
them is ever non-empty -- a consumer reads the write mode off the member it is
describing rather than by subtracting arrays from a schema it would have to load.
See [update commands](flag-system.md#update-commands).

**No timing fields.** No duration, no timestamps, no clock-derived counters: this
is a document three implementations must produce identically and the conformance
suite compares structurally.

**The runtime guard.** While the handler runs under `--json`, the framework
redirects the process stdout (at the file-descriptor level in Go and Python, by
replacing `process.stdout.write` in TypeScript). Bytes that reach it outside the
framework are discarded and fail the run: the exit status becomes 1 unless it is
already nonzero, and an `error` diagnostic names them --
`stdout written outside the framework: <n> bytes: "<the first 4096 bytes>"`, the
excerpt as a JSON string. The declared routes to stdout in machine mode are the
`payload` and `output` members, and `ctx.document()` on a command that owns
stdout.

## Source provenance

Every resolved flag value carries a source label tracking where its value came
from. Source provenance enables intelligent constraint evaluation: a
member-spelled election considers only the cli source, while constraint
engagement considers everything except defaults and infra. Handlers can inspect provenance at runtime
to alter behavior based on whether a value was explicitly provided or fell
through to its default. The six source labels are:

| Label | Meaning |
|-------|---------|
| `cli` | Explicitly passed on the command line |
| `env` | From an environment variable |
| `config` | From a config file |
| `default` | From the flag's declared default value -- and the label an `optional` declaration carries when nothing supplied a value |
| `implied` | Injected by an `Implies` constraint |
| `infra` | Default resolved through a `RelativeToRoot` infrastructure root |

No seventh label is minted for "declared optional, received nothing": `default`
already means "the declaration decided", and an optional declaration deciding on
absence is that.

Provenance is tracked internally by a `SourcedStore` (Go/TypeScript) or
`_SourcedStore` (Python) that pairs each value with its source label.
Provenance matters for constraint evaluation:

- **Member-spelled election** considers only the `cli` source. Env and config
  are not consulted for a member flag at all: the spelling exists to make the
  operator choose in the invocation. A **token-spelled** choice flag is an
  ordinary value flag whose value happens to name a choice, so it elects from
  any source, and the election's origin is named in every message it causes
  (` from env var '<VAR>'`, ` from config key '<key>'`, ` by default`).
- **Ambient values for flags in non-elected scopes** are conditional bindings by
  declaration: an env var or config key bound to a scoped flag is consulted when
  its scope is elected and otherwise never consulted. Every skipped binding that
  actually carried a value is named on the debug channel (`not consulted: env
  var '<VAR>' binds flag '--<x>' under '<scope path>', which was not elected`),
  hidden by default and shown by `--verbose`.
- **Constraint engagement** (a co-occurrence member's `present` selector, the
  `requires` predicate, and the `implies` trigger) considers `cli`, `env`,
  `config` and `implied`, and excludes both `default` and `infra`. A flag that
  got its value from `implied` counts as provided; a flag carrying only a
  declared default -- including a `RelativeToRoot` default with the `infra`
  label -- does not. The `true` and `non_empty` selectors are that same
  predicate plus a test on the resolved value.

Handlers access provenance via `ctx.Source(name)` (Go) / `ctx.source(name)`
(Python) / `ctx.source(name)` (TypeScript). The name can be dashed
(`dry-run`) or underscored (`dry_run`); both forms are accepted.

For the yes/no question -- *did the invocation cause this value?* -- handlers use
`ctx.Provided(name)` (Go) / `ctx.provided(name)` (Python and TypeScript), which
reads the same predicate constraint engagement does, so the framework has one
definition of "was this supplied" rather than two. An unknown name behaves
exactly as it does on `ctx.source`, with the same message.

## Registration-time validation

strictcli enforces over 30 invariants at the moment flags, args, commands, and
groups are constructed. This is a deliberate design: if a declaration is
invalid, the program panics (Go), raises `ValueError` (Python), or throws a
`RegistrationError` (TypeScript) before any parsing occurs. The user never
sees a confusing runtime error caused by a misconfigured CLI. These checks
span 4 categories: flag naming, type checking, arg validation, and command
validation.

### Flag naming rules

Flag names are validated at registration time by `validateFlagConfig` (Go),
`Flag.__post_init__` (Python), and `validateFlagConfig` (TypeScript). These
rules prevent ambiguous flag names that would conflict with the framework's
automatic negation system, and ensure that every flag is self-documenting by
requiring help text. The bare name `force` is banned to prevent agents from
taking shortcuts with a generic override flag.

- **Help text is mandatory.** Every flag must have a non-empty help string.
- **Presence is mandatory.** Every flag declares exactly one of required,
  optional, or a default value. Declaring none names all three choices;
  declaring two names the two that were supplied; a null-valued default
  (`default=None` / `Default(nil)` / `default: null`) is refused with a
  redirect to the optional spelling.
- **`force` is banned.** The bare name `force` is reserved. Use a qualified
  name like `force-overwrite` or `force-delete`.
- **`no-` prefix is reserved.** Flag names cannot start with `no-`. This prefix
  is auto-generated by the negation system for negatable boolean flags.
- **The naming rule.** After the bans, a name must be lowercase kebab-case of at
  least two characters, and a short form one ASCII letter (see
  [The naming rule and the framework commands](#the-naming-rule-and-the-framework-commands)).

### Type checking

The four scalar types (`str`, `bool`, `int`, `float`) and compound types
(`list[T]`, `dict[str,T]`) are strictly validated at registration time to catch
type mismatches, invalid defaults, and incompatible flag option combinations
before any user input is parsed. Every type constraint is enforced identically
across Python, Go, and TypeScript:

- Default values must match the declared type. A `float` flag with an `int`
  default is a hard error. A `str` flag with a `bool` default is a hard error.
- Choices must all be of the declared type.
- Compound types (`list[T]`, `dict[str,T]`) validate their element type at
  registration: `T` must be `str`, `int`, or `float` (never `bool`).
- Repeatable flags cannot be `bool`.
- Repeatable flags require explicit `unique` (true or false) -- no implicit
  default.
- A repeatable or dict flag declares presence like any other flag. An explicit
  `default=[]` / `default={}` is legal, and so is `optional` or `required`.
- `unique` requires `repeatable`.
- Dict flags cannot be combined with `repeatable`, `unique`, `choices`, or
  `env_separator`.
- `env_separator` requires `repeatable`, requires `env`, must be a single
  character, and cannot be a backslash.

### Arg validation

- Help text is mandatory.
- Presence is mandatory, with the same three facts and the same one-spelling
  rule flags use. There is no `required=` field on an arg.
- A variadic arg cannot declare a default: it always delivers a list, so the
  empty case is `optional`.
- Only scalar types and `list[T]` are allowed. `dict` types are not supported
  on positional args.
- `list[T]` requires `variadic=true`.
- Variadic args must be last.
- At most one variadic arg per command.
- Duplicate arg names within a command are a hard error.
- Choices type must match the arg's declared type.
- Default values must match the declared type and be in the choices set (if
  declared).

### Command validation

Command-level validation is enforced by `buildAndValidateCommand` (Go),
`_build_and_validate_command` (Python), and the equivalent validation in
`app.ts` (TypeScript). These checks verify that each command is internally
consistent: no duplicate flag names, no collisions with global flags, valid
choice-flag and constraint declarations, and mandatory help text on every
command.

- Help text is mandatory.
- Duplicate flag names within a command are a hard error.
- Flags cannot collide with global flags.
- `Requires` must reference declared flags and cannot be self-referential.
- `Implies` trigger and target must be bool flags; target must be different
  from trigger.
- A constraint may not name a scoped flag: constraints operate at root scope
  only.
- Passthrough commands cannot have flags, args, or flag sets, so they declare
  nothing a choice flag could scope.

### Constraint validation

A command's constraint set is resolved in a pinned order, so a declaration with
two faults reports the same first error in every implementation. The order runs
from the constraint's own identity outward to the declarations it names, so a
message never blames a member for a fault in the constraint that names it:

1. **name legality** -- the naming rule (lowercase kebab-case of at least two characters), duplicates, and collision
   with a root flag or arg name (a member reference resolves by name and would
   otherwise be ambiguous);
2. **member arity** -- at least two members. Go and TypeScript reach this state
   only through a widened caller: their two-member floors are compile errors;
3. **member resolution** -- each name resolves to exactly one flag, arg or
   constraint. Unknown names refuse here, and so does a name that resolves to
   **both** a flag and an arg;
4. **scope** -- a resolved flag declared inside a choice scope refuses;
5. **nesting legality** -- a nested member is an at-least-one or an all-or-none
   (naming a `Requires` or an `Implies` refuses, "engaged" having no meaning for
   them), carries no election selector of its own, and the reference graph is
   acyclic;
6. **election legality** -- the selector against the member's declared type,
   including the mandatory-election refusal on a bool member;
7. **presence legality** -- no member of a co-occurrence constraint may declare
   `required`.

### Choice-flag validation

Every rule below runs at **every depth**, because a choice flag may be declared
inside a choice's scope without limit and a ban enforced only against a flat
root list is the construct's most likely correctness defect:

- A choice flag must declare at least two choices, each with a unique name and
  non-empty help.
- A choice flag declares `required` or a `default`; `optional` is refused with a
  redirect that names the remedy.
- A default must name a declared choice, and its selection must be **complete**:
  a choice whose scope declares a required sub-flag cannot be a default (Go and
  TypeScript check it; in Python the default is a choice instance, which cannot
  be constructed without its required fields).
- A member flag must declare `required`, read as *required once this member is
  elected*. A member-spelled choice flag cannot carry a short. A defaulted
  member-spelled choice flag may only default to a payload-less member.
- A token-spelled choice cannot carry a payload.
- `choice` and `value` are reserved flag names inside every scope; every other
  name rule (the reserved quartet, `json`, `yes`, bare `force`, the `no-`
  prefix, `approve_consequential`, the charset, mandatory help) re-runs there
  too.
- A scoped flag may not reuse a command-level flag's name, nor its own choice
  flag's name. Sibling scopes may reuse a name only with an identical type and
  arity; simultaneously electable scopes may not reuse one at all. Shorts are
  claimed across every simultaneously live scope.
- Positional args cannot be declared inside a scope.
- Every `choices` entry is a value-plus-help record; a bare value is refused.

### Update validation

An [update command](flag-system.md#update-commands) is resolved and validated in
a pinned order, because three implementations must report the same first error
for a declaration with two faults. Each step runs across the whole declaration
before the next begins, so a message never blames a name for a fault in the
record that names it:

1. **The mutating-default ban**, over every flag and arg the command carries, in
   declaration order. It runs first because it is a fact about the command's
   **classification** and is independent of whether an update is declared at all
   -- it is the only step in this family that fires on a command declaring no
   update.
2. **Classification legality** -- an update on a `read_only` command.
3. **Record legality** -- the resource name's charset, the write mode's
   vocabulary, at least one property.
4. **Name resolution** -- each name in either list resolves to exactly one flag
   or arg; unknown, ambiguous, duplicated and both-roles names refuse here.
5. **Scope** -- a resolved flag declared inside a choice scope refuses.
6. **Role legality** -- a property that is a positional arg, then a property that
   is a choice flag.
7. **Presence legality** -- a property declaring `required`.
8. **The clear vocabulary** -- `nullable` off a property, and the `unset-<x>` name
   reservation, which runs last because it is the only step that reads the flag
   namespace back after the property set is known.

The order runs from the command's own classification, through the record's
identity, outward to the declarations it names. The construct's one **parse-time**
rule -- the at-least-one-property refusal and the write set it computes -- sits in
[stage 4](#stage-4-command-parsing), after the constraints and before defaults.

### Global flag validation

- Global flag names cannot collide with reserved framework names: `help`, `h`,
  `version`, `v`, `dump-schema`, `mcp`, `config`, `hermetic`.

### The naming rule and the framework commands

- Command, group, deprecated-command, long-flag (at every level), token-elected
  choice, constraint, tag, check, hook, grant, update-resource, and requirement
  names are lowercase kebab-case of at least two characters,
  `[a-z][a-z0-9]*(-[a-z0-9]+)*`. A short form is one ASCII letter, uppercase
  allowed. Positional argument names, the App's own name, plain `choices`
  values, and config field names are exempt.
- `help` and `version` are the framework's own commands. No command, group, or
  deprecated command at any depth may take either name.
- Duplicate global flag names are a hard error.

### Tag validation

Tag names follow the naming rule. Tag contracts (`TagContract` in Go,
`tag_contract` in Python) declare that any command tagged with a given tag must
have a specific flag; this is validated at `Run`/`Test` time across the entire
command tree.

## The schema format (`.strictcli/schema.json`)

`help --json` prints the help document on every strictcli app: a JSON document
describing the entire CLI surface -- every command, group, flag, positional
argument, constraint, and config field -- on stdout, with the `--json`
document on stderr, the rule for a command that owns stdout. It writes no file:
a committed `.strictcli/schema.json` is `<app> help --json` redirected into it,
and external tools like rlsbl compare that file against the program during
release to verify that no flags or commands were silently removed.

An address prunes the document -- `help db --json` keeps only the `db` group,
`help deploy --target --json` only the flag entries declaring `--target` -- and
records the address in an `address` key after `project_id`; `--depth <int>`
keeps that many levels of the command tree and records `depth`. `project_id`
names the program's own project from its build information: Go's main module
path, the Python project holding the module that constructed the App, the
`package.json` above the TypeScript entry script. `DumpSchemaDict()` /
`dump_schema_dict()` / `dumpSchemaDict()` return the whole-app document without
it, in-process.

### Schema version 2

The dump is at **`schema_version: 2`**, emitted at both sites -- the top-level
key and the copy inside the `defaults` block. There is no v1 compatibility path:
no dual-reader, no shim, no negotiated fallback. A reader sees `schema_version`
and knows which format it holds, and a v1 file stays readable as exactly what it
is.

What v2 changed, relative to v1:

| Change | What it replaces |
|---|---|
| `value_schema` -- a real JSON Schema fragment on every flag and arg entry | the `type` key, which had three different spellings across the implementations |
| arity is part of the value's shape | the `repeatable` key on the flag entry, deleted |
| a native encoding for choice flags (`choices` + `elect_by`) | nothing -- v1 could not describe one |
| value-plus-help records under `choices` | the bare value list |
| one declared key order and one byte canon | per-implementation serializer behavior |
| the rewritten `defaults` block, plus `config_format`, `config_path`, `config_conflict_mode`, `prefixed` and `flag_sets` | a block with a phantom key and three stale baselines |
| the `at_least_one` / `all_or_none` / `requires` / `implies` catalogue, every entry carrying a mandatory `name` | the four-entry catalogue that included `mutex`, and the flat `co_required` flag list |

### Top-level fields

| Field | Type | Description |
|-------|------|-------------|
| `schema_version` | `int` | Always `2` |
| `defaults` | `object` | What an omitted key means (see below) |
| `project_id` | `string` | Language-specific project identifier (Go module path, Python package name, npm package name) |
| `name` | `string` | App name |
| `version` | `string` | App version |
| `help` | `string` | App help text |
| `env_prefix` | `string?` | Env var prefix (omitted when unset) |
| `config` | `bool` | Whether config file support is enabled |
| `config_format` | `string` | `"json"` or `"toml"`; omitted when `"json"` |
| `config_path` | `string \| object?` | The **declared** config path, never the resolution; a `RelativeToRoot` declaration is emitted in its marker shape |
| `config_conflict_mode` | `string` | Omitted when `"cli-wins"` |
| `proc_observe_allowlist` | `string[]` | Declared process-observation prefixes |
| `global_flags` | `Flag[]` | Global flag definitions |
| `commands` | `{name: Command}` | Top-level commands, in declaration order |
| `groups` | `{name: Group}` | Top-level groups, in declaration order |
| `deprecated` | `{name: message}` | Deprecated command names and messages, sorted by key |
| `tag_contracts` | `{tag: flag_name}` | Tag contract declarations, sorted by key |
| `checks` | `{name: CheckSchema}` | Check declarations; provider-sourced checks are excluded, so the block is a function of the declaration alone |
| `config_fields` | `{name: ConfigFieldSchema}` | Config field declarations, in declaration order |
| `infra` | `InfraSchema` | Infrastructure env var declarations |

### `value_schema`: a real fragment from a closed subset

Every flag entry and every arg entry carries a `value_schema` -- a JSON Schema
fragment describing the shape of the value the declaration delivers, using JSON
Schema's own type names (`string`, `boolean`, `integer`, `number`, `array`,
`object`). **The subset is closed at four keywords**: `type`, `items`,
`additionalProperties`, `enum`. Nothing else may appear in a fragment, and one
conformance check (`schema-fragments`) validates every fragment in all three
targets' dumps against exactly that closure.

| Carrier | `value_schema` |
|---|---|
| `str` scalar | `{"type": "string"}` |
| `bool` scalar | `{"type": "boolean"}` |
| `int` scalar | `{"type": "integer"}` |
| `float` scalar | `{"type": "number"}` |
| `list[T]` flag, and a repeatable scalar `T` flag | `{"type": "array", "items": {"type": "<T>"}}` |
| `dict[str, T]` flag | `{"type": "object", "additionalProperties": {"type": "<T>"}}` |
| variadic arg of element type `T`, in either spelling | `{"type": "array", "items": {"type": "<T>"}}` |
| any scalar carrier with `choices` | `{"type": "<T>", "enum": [<values>]}` |
| any array-shaped carrier with `choices` | `{"type": "array", "items": {"type": "<T>", "enum": [<values>]}}` |
| a config field (always scalar) | the matching scalar row |
| a **choice flag** | **none** -- the key is absent |

Keys inside a fragment are emitted in the order `type`, `items`,
`additionalProperties`, `enum`.

**Arity is value shape.** A repeatable scalar flag delivers a list, so it
publishes the identical array fragment a `list[T]` carrier does; the `repeatable`
key is gone. `variadic` survives on the arg entry, because it names a
token-consumption rule (this arg takes every remaining positional token) rather
than restating a value shape.

**An optional flag emits the plain type. There is no `null` in any fragment**,
and no type list. Presence is the sole authority on absence; a fragment that
added `null` would be a second statement about the same fact. The fragment
describes the shape of a value when there is one, and `presence` answers whether
there is one.

### The choice-flag encoding

**A choice flag has no `value_schema`, and its absence is the declaration.** Its
value is a *variant* -- one tagged record chosen from several, each with a
different set of fields -- which the closed four-keyword subset cannot express.
Publishing a wrong fragment would be worse than publishing none: a reader would
validate against it and be told a record is invalid.

So the entry carries a framework-native encoding beside the fragments its scopes'
entries carry, under two keys:

- **`choices`** -- an array of choice objects in declaration order, each
  `name`, `help` (mandatory on a choice, so always emitted), `flags` (omitted
  when the scope is empty). **Each scoped entry is a full flag entry**, with its
  own `value_schema`, `presence` and `default`, which is what makes recursion
  free: a nested choice flag is an entry inside a `flags` array carrying its own
  `choices` and `elect_by`, to any depth.
- **`elect_by`** -- `"selector-token"` or `"member-flags"`, marking the spelling.

**The presence of `elect_by` is the discriminator.** An entry with it is a choice
flag: it has no `value_schema`, and its `choices` entries are choice objects. An
entry without it is an ordinary flag: it has a `value_schema`, and its `choices`
entries (if any) are value records. A reader never has to guess which shape it
is holding.

A member-spelled choice's payload appears as the **first** entry of that choice's
`flags` array, under the reserved name `value`, with `"presence": "required"`. A
choice flag's `default` is published in the delivery record's own flat map form:
`{"choice": "<name>", "<field>": <value>, ...}`.

### Choices: the enum in the fragment, the records beside it

A value flag's `choices` declaration produces **two** keys, each carrying the
half it is good at. `value_schema` carries the values as an `enum` -- inside
`items` for an array-shaped carrier, at the fragment root for a scalar one --
which a validator can use as-is. `choices` carries the value-plus-help records,
for which JSON Schema has no vocabulary:

```json
"choices": [
  {"value": "head", "help": "the current commit only"},
  {"value": "branches"}
]
```

Entries are in declaration order, `value` is emitted with its own type -- a
string, an integer or a float token, never wrapped in quotes when it is not a
string -- and `help` is omitted when the entry declares none.

### A dump

A real dump of a one-command app declaring one token-spelled choice flag, one
list flag, one `choices` flag and one positional arg. Only the `defaults` block
is elided, and it is reproduced verbatim further down:

```json
{
  "schema_version": 2,
  "defaults": { "...": "elided; see below" },
  "project_id": "notify",
  "name": "notify",
  "version": "0.1.0",
  "help": "Send notifications",
  "commands": {
    "send": {
      "name": "send",
      "help": "Send one notification",
      "effect": "mutating",
      "flags": [
        {
          "name": "via",
          "help": "Delivery channel",
          "short": "v",
          "presence": "required",
          "choices": [
            {
              "name": "email",
              "help": "deliver the notification as an email message",
              "flags": [
                {
                  "name": "subject",
                  "help": "subject line of the message",
                  "value_schema": {
                    "type": "string"
                  },
                  "presence": "required"
                }
              ]
            },
            {
              "name": "webhook",
              "help": "post the notification to a URL",
              "flags": [
                {
                  "name": "url",
                  "help": "endpoint to post to",
                  "value_schema": {
                    "type": "string"
                  },
                  "presence": "required"
                },
                {
                  "name": "retries",
                  "help": "delivery attempts before giving up",
                  "value_schema": {
                    "type": "integer"
                  },
                  "presence": "default",
                  "default": 3
                }
              ]
            }
          ],
          "elect_by": "selector-token"
        },
        {
          "name": "tag",
          "help": "Tags to attach",
          "value_schema": {
            "type": "array",
            "items": {
              "type": "string"
            }
          },
          "presence": "default",
          "default": []
        },
        {
          "name": "format",
          "help": "Output format",
          "value_schema": {
            "type": "string",
            "enum": [
              "json",
              "csv"
            ]
          },
          "presence": "default",
          "default": "json",
          "choices": [
            {
              "value": "json",
              "help": "one JSON document"
            },
            {
              "value": "csv"
            }
          ]
        }
      ],
      "args": [
        {
          "name": "recipient",
          "help": "Who to notify",
          "value_schema": {
            "type": "string"
          },
          "presence": "required"
        }
      ]
    }
  }
}
```

### Canonical key order

Object keys are emitted in a **declared order**, identical in all three
implementations. No implementation sorts them at serialization time.

| Entity | Key order |
|---|---|
| top level | `schema_version`, `defaults`, `project_id`, `name`, `version`, `help`, `env_prefix`, `config`, `config_format`, `config_path`, `config_conflict_mode`, `proc_observe_allowlist`, `global_flags`, `commands`, `groups`, `deprecated`, `tag_contracts`, `checks`, `config_fields`, `infra` |
| flag entry | `name`, `help`, `value_schema`, `short`, `presence`, `default`, `env`, `env_separator`, `prefixed`, `choices`, `elect_by`, `unique`, `conflict_mode`, `negatable`, `nullable` |
| arg entry | `name`, `help`, `value_schema`, `presence`, `default`, `variadic`, `choices` |
| choice object | `name`, `help`, `flags` |
| choice record | `value`, `help` |
| command entry | `name`, `help`, `effect`, `consequential`, `dry_run_supported`, `dry_run_unsupported_reason`, `update_of`, `write_mode`, `payload_schema`, `owns_stdout`, `passthrough`, `flags`, `flag_sets`, `args`, `tags`, `constraints`, `hidden`, `interactive`, `config_fields`, `grants`, `forwarding` |
| group entry | `name`, `help`, `commands`, `groups`, `deprecated`, `tags`, `hidden` |
| config-field entry | `value_schema`, `help`, `required`, `default`, `bound_commands` |
| check entry | `tags`, `severity`, `fast`, `pure`, `needs_network`, `depends_on`, `description`, `subject`, `scope` |
| grant entry | `name`, `reason`, `kind` |
| infra block | `roots`, `handshakes`, `connections` |

`commands`, `groups` and `config_fields` are emitted in **declaration order**,
which all three implementations retain. `checks`, `deprecated` and
`tag_contracts` are emitted **sorted ascending by key**, because no
implementation retains a declaration order for them -- a canon that cannot be
produced from what an implementation holds is not a canon. Every key in those
positions is ASCII by registration rule, so byte order, code-point order and
UTF-16 order coincide.

Array order is always declaration order: flags, args, choices, grants,
constraints, `bound_commands`, `proc_observe_allowlist`. `tags` remain sorted.
The one pinned position is a member-spelled choice's payload, which sits first
in that choice's `flags` array.

### The byte canon

A committed `.strictcli/schema.json` must be **dumper-independent**: a repository
whose file is written sometimes by a Go binary and sometimes by a Python one must
see a diff exactly when something changed. The `schema-parity` conformance check
therefore compares **bytes**, with no normalization layer.

- **Numbers.** Every float is written in the strictcli canonical float form
  (SCF), the same one-form-three-implementations canon the float vectors already
  enforce. Integers are bare integer tokens -- no decimal point, no exponent, no
  separators.
- **Escaping.** Escape exactly what JSON mandates and emit everything else
  literally. `"` and `\` are escaped; control characters below U+0020 use JSON's
  short escapes where one exists and `\u00XX` otherwise. **Non-ASCII is never
  escaped** (raw UTF-8, no `\uXXXX`); **HTML-significant characters are never
  escaped** (`<`, `>` and `&` are literal); `/` is never escaped. A lone
  surrogate is escaped as `\uDXXX` -- the one escape not mandated by the
  character itself, and the alternative is emitting invalid UTF-8.
- **Layout.** Two-space indent; one member or element per line; `": "` between a
  key and its value; `,` then a newline between siblings; empty containers as
  `{}` and `[]` on a single line; **exactly one trailing newline** at end of
  file.

### Defaults object

The `defaults` key is the machine-readable map of **what an omitted key means**.
A consumer reconstructs omitted fields by reading from it: a flag with no `short`
key means `defaults.flag.short`, which is `null`.

Keys with **no** baseline are absent from the block on purpose, and the list is
exactly the set of always-emitted facts: `name`, `help`, `version`,
`schema_version`, `project_id`, `effect`, `presence`, `value_schema` on every
entry that has one, a choice object's `name` and `help`, a choice record's
`value`, a config field's `help` and `required`, and a check's mandatory
fields (every field of its checks.toml declaration except `scope`).

`presence` is always emitted on every flag and arg entry, so there is no baseline
to omit against; its value is `"required"`, `"optional"` or `"default"`, and a
`default` key accompanies it exactly when `presence` is `"default"` -- then
always, including for `[]`, `{}`, `""`, `false` and `0`. `default` therefore has
no baseline either.

`value_schema` is the one always-emitted key with a stated exception, and the
exception is not an omission at a baseline: a **choice flag** carries no fragment
at all, and its absence *is* the declaration. A baseline would have to say what
an absent fragment means, and every answer it could give is false for the one
entry that omits the key.

```json
{
  "schema_version": 2,
  "app": {
    "env_prefix": null, "config": false, "config_format": "json", "config_path": null,
    "config_conflict_mode": "cli-wins", "proc_observe_allowlist": [], "global_flags": [],
    "commands": {}, "groups": {}, "deprecated": {}, "tag_contracts": {}, "checks": {},
    "config_fields": {}, "infra": {}
  },
  "flag": {
    "short": null, "env": null, "env_separator": null, "prefixed": true, "choices": null,
    "elect_by": null, "unique": false, "conflict_mode": null, "negatable": null,
    "nullable": false
  },
  "arg": { "variadic": false, "choices": null },
  "choice": { "flags": [] },
  "choice_record": { "help": null },
  "command": {
    "consequential": false, "dry_run_supported": true, "dry_run_unsupported_reason": null,
    "update_of": null, "write_mode": null,
    "payload_schema": null, "owns_stdout": false, "passthrough": false, "flags": [],
    "flag_sets": [], "args": [], "tags": [], "constraints": [], "hidden": false,
    "interactive": false, "config_fields": [], "grants": [], "forwarding": null
  },
  "group": { "commands": {}, "groups": {}, "deprecated": {}, "tags": [], "hidden": false },
  "config_field": { "default": null, "bound_commands": [] },
  "check": { "scope": null },
  "infra": { "roots": [], "handshakes": [], "connections": [] }
}
```

The two choice entities carry the block's last two omission rules: a choice
object's `flags` is omitted when the scope is empty, and a choice record's `help`
is omitted when the entry declares none. The unqualified name goes to the choice
flag's choice object, because a choice flag's entry *is* a choice while a value
flag's entry is a value that may carry help.

### Constraint serialization

Constraints are serialized in the `constraints` array of each command, in
declaration order. The catalogue is closed at four types, and there is no
`mutex` entry -- exactly one selection is a choice flag, which is published on
the flag entry itself. Every entry carries a mandatory `name`:

| `type` | Keys, in order |
|---|---|
| `at_least_one` | `type`, `name`, `members` |
| `all_or_none` | `type`, `name`, `members` |
| `requires` | `type`, `name`, `flag`, `depends_on` |
| `implies` | `type`, `name`, `flag`, `implies`, `value` |

A **member object** emits its keys in the order `kind`, `name`, `when`. `kind`
is one of `flag`, `arg`, `constraint` -- the **resolved** kind, so a consumer
never has to search the flag and arg lists to learn what a name refers to, which
is the same reason a flag entry publishes a `value_schema` rather than a type
word. `when` is one of `present`, `true`, `non_empty`; it is **always** emitted
on a `flag` or `arg` member and **never** on a `constraint` member, a nested
member having no election of its own. Because it is never omitted it takes no
`defaults` block entry -- that block is the map of what an omitted key means.

The encoding is complete rather than indicative: a consumer reconstructs the
rule without re-reading the declaration, so nesting is published as
constraint-kind members rather than flattened into leaves, and a partially
encoded constraint is not a legal intermediate state.

```json
[
  {"type": "all_or_none", "name": "author-name", "members": [
    {"kind": "flag", "name": "old-name", "when": "present"},
    {"kind": "flag", "name": "new-name", "when": "present"}]},
  {"type": "at_least_one", "name": "purge-selection", "members": [
    {"kind": "arg", "name": "targets", "when": "non_empty"},
    {"kind": "flag", "name": "older-than", "when": "present"},
    {"kind": "flag", "name": "all", "when": "true"}]},
  {"type": "at_least_one", "name": "author-change", "members": [
    {"kind": "constraint", "name": "author-name"},
    {"kind": "constraint", "name": "author-email"}]},
  {"type": "requires", "name": "port-needs-host", "flag": "port", "depends_on": "host"},
  {"type": "implies", "name": "trace-debugs", "flag": "trace", "implies": "debug", "value": true}
]
```

### Update serialization

An [update command](flag-system.md#update-commands) publishes **two command-entry
keys**, emitted exactly together and never alone: `update_of`, an object with
`resource`, `identity` and `properties`; and `write_mode`, the string `"sparse"`
or `"full_replace"`. Both baseline to `null`, so a command declaring no update
emits neither.

```json
{
  "name": "update-record",
  "help": "change one DNS record in place",
  "effect": "mutating",
  "update_of": {
    "resource": "dns-record",
    "identity": ["zone", "record-id"],
    "properties": ["content", "ttl", "proxied"]
  },
  "write_mode": "sparse"
}
```

They are two keys in the dump where they are one nested record on every
declaration surface, and that is the declaration-versus-published-fact
distinction: a consumer reading a dump asks "what write mode does this command
have", and a key it can read without descending is the answer; a consumer
*writing* a declaration must not be able to spell half of one.

Names are published in the **declared spelling** -- `record-id`, matching the
flag entry's own `name` and the constraint catalogue's member names. The
underscored spelling belongs to the machine doors, where a caller writes keys.
`identity` is `[]` when the resource has none, and the encoding is complete
rather than indicative, for the third time and the same reason: a partially
encoded update is not a legal intermediate state.

One state is carried rather than checked: a **passthrough** command's update
declaration is stored and published, and none of registration's update checks run
on it -- the passthrough early-return precedes them. It is unusable rather than
guarded, because a passthrough can declare no flags, so it can name no property,
and a passthrough bypasses parsing entirely.

### Config fields, check entries and behavioral completeness

Config-field entries carry a `value_schema` from the scalar rows of the fragment
table. Their `required` key **stays**: it is not the presence declaration wearing
another name -- a config field has no CLI surface, no three-way declaration and
no `presence` key, and `required` there means "the config file must contain it".

Check entries carry no value shape, so nothing converts. What v2 does reach in
the `checks` block is its purity: **the dumped block is a function of the
declaration alone**, so provider-sourced check names are filtered out by every
serializer rather than by comment.

Five keys close v1's blind spots -- declarations that change what a user's
installation does but were invisible in the dump. Each is omitted at its
baseline, so a departure from the framework's behavior is exactly what makes a
key appear:

| Key | Where | Emission |
|---|---|---|
| `config_format` | app | omitted when `"json"` |
| `config_path` | app | omitted when the app declares none |
| `config_conflict_mode` | app | omitted when `"cli-wins"` |
| `prefixed` | flag | omitted when `true` |
| `flag_sets` | command | omitted when empty |

`config_path` publishes the **declaration**, never the resolution: a declared
literal path is emitted as declared, a `RelativeToRoot` declaration in its marker
shape, and the resolved absolute path never. And with `config_conflict_mode`
emitted at the app level, a per-flag `conflict_mode: null` becomes resolvable --
its absence has always meant "inherit the app default", and until v2 the app
default was not published at all.

### InfraRootPath serialization

Flag defaults that are `RelativeToRoot` markers are serialized in a
machine-stable form that never contains resolved, machine-specific paths. The
serialized shape includes the environment variable name and the relative path
parts, allowing any consumer to reconstruct the absolute path on their own
machine.

```json
{
  "default": {
    "relative_to_root": {
      "env_var": "MY_ROOT",
      "parts": ["data", "cache"]
    }
  }
}
```

This shape is identical across all three implementations so schemas byte-compare
across languages.
### Schema freshness

The committed schema file is used by external tools (rlsbl uses it during
release to verify the CLI surface is up-to-date). Its `project_id` names the
project the program was built from, so a reader can refuse a file that belongs
to another project.

## The test-coverage root

The built-in `cli-test-coverage` check reads and writes two things: per-process
shard files under `coverage/`, appended by every `test()` and `call()` dispatch,
and `test-coverage.json`, the committed manifest its verdict is derived from.
Both live under one directory, and **that directory is declared, never
discovered**:

| Declaration | Result |
|-------------|--------|
| `test_coverage_dir="..."` (Python) / `WithTestCoverageDir(...)` (Go) / `testCoverageDir: "..."` (TypeScript), naming a directory that exists at construction | the check registers, and `coverage/` plus `test-coverage.json` resolve inside that directory |
| the same declaration, naming a directory that does not exist at construction | coverage is off: no check registered, no paths computed, nothing created |
| undeclared | coverage is off |

Nothing here resolves against the process's working directory, which has two
consequences. A test that changes directory between construction and dispatch
still records into the declared directory. And an installed CLI started in a
consumer's project registers no coverage check and writes nothing into that
project -- its declared directory names a source checkout that is not there,
which is exactly the second row.

The `coverage/` subdirectory itself is created lazily, immediately before the
first shard write, so a plain CLI run leaves no empty directory behind.

## How WithConfig works internally

Config file support is enabled via `WithConfig()` (Go), `App(config=True)`
(Python), or `createApp({config: true})` (TypeScript). When config support is
enabled, the framework auto-registers a `config` command group with five
subcommands for managing the configuration file, resolves the config file path
from XDG defaults or explicit overrides, and integrates config values into the
flag resolution cascade between env vars and defaults. Enabling it triggers the
following at construction time:

1. **Auto-registers the `config` group** with five subcommands: `config show`,
   `config set`, `config path`, `config edit`, `config init`.
2. **Resolves the config file path.** Default location is the XDG config
   directory: `~/.config/{app_name}/config.{json|toml}`. This can be overridden
   by `WithConfigPath(path)`, `WithConfigPathRelativeToRoot(envVar, parts...)`,
   or the runtime `--config <path>` flag.

### Config loading flow

Config data is loaded once per parse, at the start of the parse pipeline (after
the pre-scan, before global flag parsing). The loading process determines which
file to read based on a priority chain of runtime override, app-level override,
and XDG default path, then parses the file as JSON or TOML depending on the
configured format. Missing files are handled differently depending on whether
the path was explicitly provided or is the default:

1. Determine the path: runtime `--config` override > app-level override > XDG
   default. If `no_default_config_path` is set and no runtime override was
   given, no config file is loaded.
2. Read and parse the file. JSON files use the standard library parser; TOML
   files use the TOML library. Malformed files produce a parse error with line
   and column information.
3. If the file is missing: with a runtime `--config` flag, this is a hard
   error. Without one (default XDG path), it is silently ignored (empty config
   data).
4. The parsed config data is a flat or nested key-value map. Nested keys use
   dot-separated paths in TOML (e.g., `[database]` / `host = ...` becomes
   `database.host`).

### Config field declarations

`App.ConfigField(name, type, help, default)` (Go) / `app.config_field(name,
type, help, default)` (Python) declares a typed config-file-only field. Config
fields are validated at run time when their bound commands are dispatched:
required fields must be present with the correct type.

A config field whose name matches a flag's parameter name (the underscored
form) is a "colliding" field -- a validation-only annotation that ties the
flag to a config-file key. Their defaults must agree. The flag's config value
is resolved through the normal CLI > env > config > default cascade; the
config field does not create a separate config key.

### Config conflict modes

The `config_conflict_mode` (app-level) and `conflict_mode` (per-flag) control
what happens when a flag's value comes from both the config file and the CLI or
env var. The default mode is `cli-wins`, where command-line and environment
values take precedence over config file values. The alternative `error` mode
treats dual-source values as a hard error, enforcing that each flag has exactly
one explicit source of truth:

- `cli-wins` (default): the CLI or env value takes precedence. The config
  value is silently ignored.
- `error`: having a value from both sources is a hard error, unless the values
  are identical (in which case no conflict exists).

### Hermetic mode interaction

`--hermetic` skips config file loading entirely. Config subcommands cannot be
used with `--hermetic` (hard error). This is enforced in the pre-scan: if both
`--hermetic` and `--config` are present, parsing fails immediately.

## Flag negation system

Every boolean flag in strictcli automatically generates a `--no-{name}`
negation form, allowing callers to explicitly set a boolean flag to false on the
command line. This is a core part of strictcli's design philosophy: when a
boolean flag declares itself required, the caller must pass either `--flag` or
`--no-flag` explicitly, eliminating implicit assumptions about flag state.
Negation behavior is controlled by the `negatable` property:

- For `bool` flags, `negatable` defaults to `true`. The framework automatically
  populates the negation lookup table with `--no-{name}` pointing to the same
  flag.
- For non-bool flags (`str`, `int`, `float`, and compound types), `negatable`
  is forced to `false` regardless of what the user declares.

### How negation works at parse time

The negation lookup is a separate table built alongside the long and short
lookup tables during command parsing. For each negatable boolean flag, the
parser registers `--no-{name}` in the negation table pointing back to the
original flag definition. When a token like `--no-verbose` is encountered during
left-to-right token consumption, the parser checks the negation table first:

1. Look it up in the negation table. If found, set the flag's value to `false`
   with source `cli`.
2. If `--no-verbose=value` is encountered, it is an error: boolean negations
   do not take a value.

### The `no-` prefix ban

Flag names starting with `no-` are banned at registration time. This prevents
ambiguity: if a flag named `no-cache` existed, `--no-no-cache` would be its
negation form, which is confusing. The ban ensures the `--no-` prefix is
exclusively the framework's negation mechanism.

### Schema representation

In the schema JSON, bool flags always include the `negatable` key with an
explicit true or false value, making the negation behavior machine-readable for
external tools that consume the schema. Non-bool flags omit the key entirely
because negation is not applicable to them; the schema defaults section
specifies `null` as the default for `negatable`, which consumers interpret as
"not applicable to this flag type."

The `nullable` key follows the same shape one construct over: it is emitted on a
flag entry only when the flag is a nullable property of an
[update](flag-system.md#update-commands), it baselines to `false`, and the
`--unset-<prop>` spelling the framework mints from it gets **no entry of its
own** -- exactly as `negatable` publishes `--no-<x>`. The dump publishes
declarations, and a spelling minted from one is not a second declaration. The
`value_schema` fragment is unchanged by `nullable`: the fragment describes the
shape of a value the declaration types, and whether the property can also be
cleared is a different fact with its own key.

### Bool flags and the presence declaration

A bool flag declaring `required` forces the user to pass either `--flag` or
`--no-flag`, which is how a caller is made to state intent on a binary decision.
A bool declaring `Default(true)` or `Default(false)` falls through to that value.
A bool declaring `optional` is a genuine three-valued flag: `--flag` is true,
`--no-flag` is false, and absence is delivered as absence -- which is what
retires the "use a string and treat the empty string as unset" idiom.

## The framework-use lint

`--lint-framework-use` is reserved in every strictcli app, with no opt-in. Run
from the program's project root -- the directory holding its `go.mod`,
`pyproject.toml` or `package.json` -- it reads the repository-owned source files
linked into the program (test files and the repository's other programs
excluded) and reports every construct that bypasses the framework, one per line
on stdout:

```
<path>:<line>: <rule>: <message>
```

It exits 1 when there is a finding and 0, printing nothing, when there is none.
The rules are `process-exit`, `stdout-write`, `stderr-write`, `argv-access`,
`environment-read` and, in Go, `exit-now-in-goroutine`. Names are resolved
through each file's imports, so an alias of `os` does not hide `os.Exit`. There
is no allow-list, no skip flag, and no severity to lower: a finding is fixed in
the code. Environment variables an app needs are declared through a flag's
environment binding, a handshake, a connection, or a location root, and read
through the framework, never read raw.

| Rule | Go | Python | TypeScript |
|------|----|--------|------------|
| `process-exit` | `os.Exit`, `syscall.Exit`, `log.Fatal*` | `sys.exit`, `raise SystemExit`, `os._exit`, `exit()`, `quit()` | `process.exit`, `process.abort`, assigning `process.exitCode` |
| `stdout-write` | `fmt.Print*`, `os.Stdout` | `print(...)` without `file=`, `sys.stdout`, `os.write(1, ...)` | `console.log` and every `console` member not listed for stderr, `process.stdout` |
| `stderr-write` | `os.Stderr`, `print`, `println`, `log.Print*`, `log.Panic*` | `sys.stderr`, `os.write(2, ...)` | `console.error`, `console.warn`, `console.trace`, `console.assert`, `process.stderr` |
| `argv-access` | `os.Args`, the `flag` package | `sys.argv`, `sys.orig_argv`, `argparse`, `optparse`, `getopt` | `process.argv`, `process.argv0`, `process.execArgv`, `parseArgs` |
| `environment-read` | `os.Getenv`, `os.LookupEnv`, `os.Environ`, `os.ExpandEnv`, `syscall.Getenv`, `syscall.Environ` | `os.environ`, `os.environb`, `os.getenv`, `os.getenvb` | `process.env` |
| `exit-now-in-goroutine` | `strictcli.ExitNow` inside a function literal started by `go` | -- | -- |

What is scanned: in Go, every `main` package of the module that imports
strictcli and every package of the module it imports; in Python, the top-level
package behind each `[project.scripts]` entry that imports strictcli; in
TypeScript, the modules reachable from the `bin` entries through relative
imports. Only files git reports as belonging to the repository are read.

## Error handling philosophy

strictcli follows a strict, no-silent-defaults error philosophy. Every error
condition produces a specific, actionable message with enough context to fix the
problem. There are no warnings that continue execution -- if something is wrong,
the framework fails immediately. Error messages are byte-identical across all
three implementations, enforced by the conformance suite's error parity check
that compares every error template one-to-one.

### Two error categories

1. **Registration-time errors** (programmer errors): these indicate a bug in
   the CLI definition. They produce a panic (Go), `ValueError` (Python), or
   `RegistrationError` (TypeScript). The program cannot start.

2. **Parse-time errors** (user errors): these indicate incorrect input. They
   print an error message to stderr with a `try '...' --help` hint and exit
   with code 1.

### Error message parity

All three implementations produce byte-identical error messages for identical
inputs. This is enforced by the `check_error_parity.py` conformance check,
which extracts every error template from all implementations and verifies
they match one-to-one.

Error templates are centralized in a single file per implementation:

- **Go**: `errors.go` -- functions returning format strings.
- **Python**: inline in `__init__.py` (using f-strings with the same
  placeholders as the Go templates).
- **TypeScript**: `errors.ts` -- functions returning format strings, mirroring
  `errors.go` one-to-one.

### No implicit defaults

strictcli does not silently fill in default values for configuration that
affects behavior, and it does not infer presence either: a flag or arg that
declares none of required / optional / default does not register at all. These
rules exist to prevent a common class of bugs where a CLI tool silently uses a
default that the caller did not know about.

A `required` bool must be explicitly passed as `--flag` or `--no-flag`. A
repeatable or dict flag gets no silent `[]` / `{}` -- it declares one, or
declares `optional`, or declares `required`. Repeatable flags require explicit
`unique` declaration. Env separator is mandatory for repeatable flags with env
var support.

### No unknown flags

Any `--flag` token that does not match a declared flag is a hard error. There
is no "ignore unknown flags" mode. This prevents silent typo bugs where
`--quite` (typo for `--quiet`) is silently ignored.

### Strict type parsing

Integer parsing rejects leading whitespace, trailing whitespace, and (in Go)
leading zeros. Float parsing rejects NaN and Inf. Bool env vars accept only
the exact set `1|true|yes` / `0|false|no` (case-insensitive); anything else is
an error.

## Conformance testing

The three implementations are kept in lockstep by a conformance test suite
(`conformance/`). JSON test cases define an app structure, argv, and expected
output. A runner generates the app in each language and executes it, comparing
results.

Conformance checks run as part of CI and include:

- **conformance-python/go/typescript**: all JSON test cases pass in each
  implementation.
- **conformance-parity**: outputs are byte-identical across implementations
  (with acknowledged divergences for language-specific output).
- **error-parity**: every error template exists in all implementations with
  identical format strings.
- **api-surface**: the public API surface matches across implementations.
- **schema-parity**: the schema JSON produced by each implementation is
  byte-identical for the same app definition.
- **float-fuzz**: exhaustive bit-pattern verification of the canonical float
  format (SCF) across all implementations.
