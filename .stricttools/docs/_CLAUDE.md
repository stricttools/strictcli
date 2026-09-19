+++
title = "CLAUDE.md"
+++
# strictcli

Strict CLI framework -- declare everything, infer nothing. Multiple first-class implementations kept in behavioral lockstep via a conformance test suite.

## Monorepo structure

This is an rlsbl monorepo (`.rlsbl-monorepo/workspace.toml`). Each sub-project has its own version, changelog, and release cycle.

| Directory | What | Version file | Targets | Tests |
|-----------|------|-------------|---------|-------|
| `python/` | Python implementation (PyPI) | `pyproject.toml` | pypi | `uv run pytest` in `python/` |
| `go/` | Go implementation | `VERSION` | go | `go test ./... -race` in `go/` |
| `typescript/` | TypeScript implementation (npm, releasable `ts-strictcli`) | `package.json` | npm | `npm test` in `typescript/` |
| `conformance/` | Cross-language conformance suite | n/a | plain | `python conformance/run.py --target python` / `--target go` / `--target typescript` |

**Note:** `conformance/` is a `dev_node` project. It has no changelog, no user-facing changes, and does not participate in the changelog system. It is not released independently -- releases happen only as part of monorepo batch releases (`rlsbl monorepo release`) if at all.

## Building and testing

```bash
# Python
cd python && uv sync && uv run pytest

# Go
cd go && go test ./strictcli/... -race

# TypeScript
cd typescript && npm ci && npm test

# Conformance (requires all implementations)
cd conformance && python run.py --target python && python run.py --target go && python run.py --target typescript
```

## Architecture

### Python (`python/strictcli/__init__.py`)

Single-file implementation (~7,900 lines, tomlkit dependency). Key internal stages:

1. **Registration** -- `@flag`/`@arg` decorators attach metadata to handlers; `@app.command()` triggers `_build_and_validate_command()` which merges tags, validates signatures, checks constraints.
2. **Global flag parsing** -- `_parse_global_flags()` extracts app-level flags before and after the command token.
3. **Command routing** -- first non-flag token selects the command or group.
4. **Command parsing** -- `_parse_command()` runs the four phases (tokenize, resolve elections, validate scope membership, resolve values and presence) and then env vars, config, defaults, choices and custom validation.
5. **Execution** -- handler called with ctx-first signature (`ctx, **kwargs`); the return value must be `int` (exit code), `None` (exit 0), or `strictcli.outcome(...)` -- anything else is a hard error.

### Go (`go/strictcli/`)

:-: list-modules path="go/strictcli/"

Handlers use ctx-first signatures: `func(ctx *Context, args map[string]interface{}) Outcome`. The `Context` provides structured output, provenance, and infra access; `Outcome` is the branded return type replacing raw exit codes.

### TypeScript (`typescript/src/`)

:-: list-modules path="typescript/src/"

### Conformance (`conformance/`)

JSON test cases in `cases/` define app structure + argv + expected output. `run.py` drives targets differently:

- **Python**: generates a reference script via `ref_python.py` and executes it with the case argv.
- **Go**: builds a single persistent harness binary (`conformance/harness/`, built once per run and left in place afterward -- it is gitignored, and deleting it would break any other conformance tool running against the same checkout) that interprets the app definition at runtime. `run.py` writes the app definition JSON to a temp file and passes its path via the `CONFORMANCE_APP_DEF` env var. There is NO per-app-hash Go binary cache.
- **TypeScript**: runs the `harness_ts` runtime harness (`conformance/harness_ts/main.js`) -- a plain Node ESM script (no install or build of its own) that imports the built `typescript/dist` by relative path. `run.py` builds the dist once per run (`npm run build` in `typescript/`) and passes the app definition via the same `CONFORMANCE_APP_DEF` env var as Go.
- `conformance/fuzz.py` (the differential argv fuzzer) drives all three implementations through the same runtime paths as `run.py`: Python via `ref_python.py` codegen, Go and TypeScript via the runtime harnesses above (each reading the app definition from `CONFORMANCE_APP_DEF`). It compares results N-way, identifying the odd one out by majority. The legacy `ref_go.py` Go codegen generator has been deleted.

Cases may carry an `acknowledged_divergence` block for intrinsically language-specific output (per-stream target lists with a mandatory reason); acknowledged targets are excluded from byte-identity comparison while the case's own expect block still runs everywhere, and stale acknowledgments are reported. The `check` gate (`uv run conformance check --tag pre-release` from `conformance/`) runs 12 checks: api-surface, error-parity, conformance-meta, conformance-python, conformance-go, conformance-typescript, conformance-parity, schema-parity, schema-fragments, schema-freshness, float-fuzz, trace-sweeps. `conformance-meta` runs the suite's own meta-tests (`test_error_parity_extraction.py`, `test_run_registry.py`, `test_api_surface_registry.py`, `test_lock_pin.py`) -- they pin the extraction and registry surfaces whose silent drift produces a false PASS rather than a visible error, plus the lockfile's editable-sibling pin, which drifts one release behind `python/pyproject.toml` every time it is not refreshed by hand.

## TypeScript port -- durable facts

The TypeScript implementation shipped as npm `strictcli` 0.31.0. These are the agent-facing constants that must not drift; the full historical design record and decision ledger live in `.stricttools/docs/history/_ts-port-spec.md` (the underscore prefix keeps it out of the published docs site -- selfdoc's `resolve_all_docs` walks `.stricttools/docs/` recursively and treats every non-underscore `.md` as a page).

- **Naming registry.** Conformance target `typescript`; conformance check `conformance-typescript`; rlsbl releasable and workspace project `ts-strictcli`; npm package `strictcli`; directory `typescript/`.
- **TOML acceptance gate.** The TS parse layer MUST reject the six TOML-1.1-only constructs (parity with the stricter Python/Go TOML): backslash-`e` escapes and backslash-`x` hex escapes in basic strings; newlines and trailing commas inside inline tables; times without seconds and datetimes without seconds.
- **TOML stack.** `smol-toml` (with `integersAsBigInt` so TOML integers round-trip as `bigint`) for parsing; a `toml-eslint-parser`-based single-key splicer for comment-preserving, byte-exact `config set` edits.
- **SCF float canon.** One canonical decimal form for floats, byte-identical across Python/Go/TS; the exhaustive bit-pattern to expected-string vectors are committed at `conformance/float_vectors.json` and enforced by the `float-fuzz` check.

## Idiomatic divergence is the design, not a defect

**Read this before proposing that two implementations be made to look alike.**
The three declaration surfaces are deliberately different, each in the direction
its own language pulls. strictcli exists so a developer can build in whichever of
the three languages they prefer, using that language's full idiom -- not so that
three languages can be deformed into one shape. Convergence of declaration
surfaces is never a goal in itself, and a lowest-common-denominator API is a
regression in all three languages at once.

- **Parity binds semantics and pinned sentences. It does not bind spellings.**
  Identical across implementations: what a declaration means, the bytes of help
  output, the fields of `--dump-schema`, exit codes, parse-time behavior, and the
  **sentence** of every error template (§12), with each language's own spellings
  substituted inside it. Free to differ: keyword argument vs functional option vs
  discriminated union, constructor shape, naming style, and how much of the rule
  the type system carries.
- **When adding new surface, design THREE idiomatic forms for one pinned
  semantic.** Ask what a Python, a Go, and a TypeScript developer would each
  expect to write, and write those three. Never pick one language's shape and
  transliterate it into the other two. The presence declaration is the reference
  case: Python `presence="required"` / `default=<value>` keywords, Go
  `Required()` / `Optional()` / `Default(v)` functional options, TypeScript a
  discriminated union on `presence` mirroring the shape `ArgOpts` already had.
- **A payoff that reaches only one language is a PRO, not a con.** Go's
  unexported `presenceBits` makes a `Flag` struct literal fail to register, which
  closed the pre-existing trap where an exported `Default` field on a literal was
  silently ignored at parse time. TypeScript's `default?: never` union members,
  const type parameters and `infer.ts` reading `presence` move the declaration
  into the type system and fixed `FlagKeyIsOptional`'s unsoundness. Python's
  named handler parameters make the handler-parameter default rule checkable at
  all (`def h(ctx, target="")` bound to an optional flag is a registration
  error). Never propose deleting or weakening one of these for symmetry.
- **A language-specific error template is EXPECTED when a mis-declaration is only
  expressible in one language.** Python alone can write `presence="defualt"`
  (a keyword taking a string); TypeScript alone can write `presence: "default"`
  with no `default` (the only two-part spelling). The siblings have no input that
  could produce those messages. Record them in `conformance/check_error_parity.py`
  as `excluded:` entries with the rationale, and assert them **per target** in a
  conformance case (`conformance/cases/presence_registration.json` is the model),
  never by forcing one shared spelling.
- **Exhaustiveness is a first-class goal of every delivery API.** Wherever a
  handler consumes a closed set (an elected choice, a tagged record, an
  enum-like verdict), the blessed idiom is the language's
  completeness-enforcing construct: TypeScript `switch` over the discriminated
  union with `assertNever`, Python `match` with `assert_never` (made sound by
  the mandatory handler annotation), Go `Match`/`When` (which names unhandled
  choices at dispatch -- the closest Go offers to sealed exhaustiveness). This
  holds for ALL features and ALL closed sets, not just selectors, and at every
  set size -- a two-member set consumed by an if/else is never the documented
  idiom, because the third member's arrival should be a compile error or a
  named dispatch refusal, not a silent fall-through. New surface must ship with
  its exhaustiveness story stated per language.
- **Before proposing "make X the same in all three", name the SEMANTIC that
  differs.** If none differs, the difference is idiom and it stays. "The Go
  version doesn't look like the Python version" is not a finding.
- **Strictness is what makes the idiom worth having.** Mandatory presence with no
  implicit default, mandatory help, mandatory effect classification, four types
  only, closed enums, registration-time hard errors, the banned `--yes` and bare
  `--force` names, and no escape hatches -- each implementation enforces the best
  way as the only way in its own language's terms. Never relax a rule to make the
  three easier to keep in step.

The published narrative version of this is `.stricttools/docs/language-idioms.md`; keep the two
consistent when the design moves.

## Consumer hand-rolling is a framework gap

When consumers of strictcli start hand-rolling logic that belongs in the
framework -- absence-to-fallback helpers, per-repo wrappers around one recurring
idiom, guards the framework could enforce at registration -- the framework is
doing something wrong. The N copies across consumers ARE the finding: design the
framework mechanism that reduces N to 1, never bless the copies or document the
workaround as the idiom. (Live example: the mutating-default ban made every
fleet consumer grow its own `optStr`/`_opt_default`/`_absent` helper for
absence-to-fallback resolution -- ten hand copies of one idiom is a framework
gap, and the remedy is a framework-owned declared-fallback mechanism, not an
eleventh helper.)

## Cross-language parity rules

All implementations must:

- Support exactly four types: `str`, `bool`, `int`, `float`.
- Use strict integer parsing (no leading/trailing whitespace, 64-bit signed bounds, no leading zeros in Go). Float parsing rejects NaN and Inf.
- Accept the same boolean env var strings: `1|true|yes` / `0|false|no` (case-insensitive).
- Produce identical error messages for identical inputs (checked by `check_error_parity.py`) -- one sentence per rule, byte-identical, with each language's own spellings substituted inside it where the message names a spelling (§12.10, §12.12).
- Export the same API surface (checked by `check_api_surface.py`) -- the same capabilities under each language's own declaration shape, not the same literal spellings.
- Produce identical error messages for constraint violations (checked by `check_error_parity.py`).
- Pass all conformance cases for every target before release.

When adding a feature to one implementation, add it to all implementations and add conformance cases.

## Key conventions

- Flags with dashes (`--dry-run`) become underscore parameters (`dry_run`) in handlers.
- `app.test(argv)` / `app.Test(argv)` runs the CLI in-process for unit tests -- never shell out.
- Help text is mandatory on every Flag, Arg, Command, Group, and App. Missing help is a registration-time error.
- Recursive group nesting: `group.group(name, help=...)` (Python) / `group.Group(name, help)` (Go). Arbitrary depth: App > Group > Group > ... > Command.
- Passthrough commands bypass all parsing -- handler gets raw args plus global flag values.
- **Constraints** -- four kinds, all carrying a **mandatory name**, all declared through one container (`constraints=[...]` / `WithConstraints(...)` / `constraints: [...]`; the `dependencies` spelling is deleted). `AtLeastOne(name, members)` -- at least one member engaged, members MAY co-occur, no upper bound, never exclusivity. `AllOrNone(name, members)` -- every member engaged or none; vacuously satisfied when nothing is engaged; it absorbed the deleted `CoRequired`. `Requires(name, flag, depends_on)` and `Implies(name, flag, implies, value)` keep their semantics and take flags only, by name. A **member** is a record (`Member(name, when=...)` / `Member(name, opts...)` / `{ name, when? }`; a bare string is refused) naming a flag, a positional arg, or another named at-least-one/all-or-none -- nesting is a cycle-checked DAG at unlimited depth, and `Requires`/`Implies` may never be nested. `when` is the closed election vocabulary `present` (default) / `true` (bool only) / `non_empty` (sized only), and a **bool member MUST declare its election** -- otherwise `--no-x` would engage a constraint while selecting nothing. Co-occurrence constraints take at least two members (a compile error in Go's two-named-plus-variadic constructors and TypeScript's `[M, M, ...M[]]` tuple). **No member may declare required.** Children are evaluated before parents, siblings in declaration order. Constraints operate at root scope only -- naming a scoped flag is a registration error, because the scope already is the constraint. They render in `--help` under a `Constraints:` section, publish `{kind, name, when}` members in `--dump-schema`, and project into MCP tool schemas as `anyOf` / `dependentRequired` with a declared exact/partial fidelity per kind and every remainder stated in the tool description.
- **Choice flags** -- exactly-one selection is a choice flag, and there is no `MutexGroup` and no at-most-one construct anywhere. A choice flag elects exactly one of its choices, each choice declares a scope of flags legal only while it is elected, and the handler receives one tagged record (`match` in Python, `Match`/`When` over `*Elected` in Go, a derived discriminated union in TypeScript). Two spellings: token (`--via email`, `elect_by="selector-token"` / `ChoiceFlag` / `choiceFlag`) and member (`--profile work` / `--all-profiles`, `elect_by="member-flags"` / `MemberChoiceFlag` / `memberChoiceFlag`); member election is command-line only. Recursion to unlimited depth, every name rule re-running at each level, `choice` and `value` reserved inside every scope, positionals never scoped. `choices=` survives for plain constrained values, and every entry is a value-plus-help record (`Choice(<value>, help=...)` / `Ch(<value>, "<help>")` / `{value, help}`) -- a bare value is refused.
- `app.deprecate(name, message=...)` / `group.deprecate(name, message=...)` registers a retired command that prints the message to stderr and exits 1. Shown in help under a `Deprecated:` section.
- Validation errors at registration time use panics (Go) / ValueError (Python) / a thrown error (TypeScript; the `RegistrationError` class stays internal). Parse-time errors print to stderr and exit 1 in every implementation.
- `type=float` / `FloatFlag(...)` -- float type support. NaN and Inf are rejected at parse time.
- Config file support -- `App(config=True)` (Python) / `WithConfig()` (Go). Format is JSON (default) or TOML (`config_format="toml"` / `WithConfigFormat("toml")`). Reads `~/.config/{name}/config.json` (or `.toml`). Precedence: CLI > env > config > default. Auto-registers `config show/set/path/edit/init` subcommands. `config set` takes its write under a required member-spelled selector named `write` over exactly three choices -- `config set <key> --value <v>`, `--clear`, `--default` -- so the old trailing positional value is gone and supplying none or two of the three is the framework's refusal, not the command's.
- `--dump-schema` -- auto-injected flag on every app. Writes `.strictcli/schema.json` at `schema_version: 2` describing the full CLI structure (commands, flags, args, groups). Every flag and arg entry carries a `value_schema` -- a real JSON Schema fragment from a closed subset of `type`, `items`, `additionalProperties` and `enum` -- and there is no `type` or `repeatable` key. A choice flag carries no fragment (its value is a variant the subset cannot express) and publishes nested `choices` plus `elect_by` instead. Keys are emitted in a declared order and the document is written in one canonical encoding, so dumps from the three implementations byte-compare.
- `--help` / `-h` is recognized anywhere in argv, not just at token boundaries.
- The reserved quartet (`--dry-run`, `--approve-consequential`, `--quiet`, `--verbose`) is likewise recognized anywhere in argv -- `myapp cmd --dry-run` and `myapp --dry-run cmd` are equivalent -- with two boundaries: a bare `--` (everything after it is data) and a passthrough command's name (its args are forwarded to the child byte-for-byte). `--json` is delivered by those same rules without joining the quartet. `--hermetic`, `--config`, `--dump-schema` and `--mcp` stay pre-command-only.
- **Presence is declared, never derived.** Every flag and every positional arg declares exactly one of required / optional / default: `presence="required"` | `presence="optional"` | `default=<value>` (Python), `Required()` | `Optional()` | `Default(v)` (Go; args use `ArgRequired()` | `ArgOptional()` | `ArgDefault(v)`), `{presence:"required"}` | `{presence:"optional"}` | `{presence:"default", default: v}` (TypeScript, on flags AND args). Declaring none or two is a registration-time hard error, and `default=None` / `Default(nil)` / `default: null` is refused with a redirect to the optional spelling -- optionality has one spelling. An optional declaration delivers `None` / `nil` / `undefined` as a present key (optional bools are a real tri-state); compound flags get no silent `[]` / `{}` and must declare `default=[]` / `default={}` if they want one; a variadic arg refuses any default; a choice flag declares `required` or a default and may never declare optional, and a member flag MUST declare required (read as required once this member is elected). `ctx.provided(name)` / `ctx.Provided(name)` answers "did the invocation cause this value" (true for `cli`/`env`/`config`/`implied`, false for `default`/`infra`), a `validate` callback never runs on a declared default, help renders exactly one presence part per line (`[required]` / `[optional]` / `[default: v]`), and `--dump-schema` emits `presence` on every flag and arg entry. **On a command declaring `effect="mutating"` no flag and no positional arg may declare a value default** (`errMutatingDefault`, a registration-time hard error): absence must never resolve to a value the invocation did not state, because on a mutating command a value the framework picked is a value the framework writes. Empty collection defaults (`default=[]` / `default={}`), a `RelativeToRoot` default, a choice flag's own selector default, an app-level global's default and every declaration on a `read_only` command stay legal; a flag set's default is evaluated per attaching command, and a choice's scoped flags are reached at every depth. An **update command's property** declares `optional` and nothing else -- `required` is a registration error and a value default is refused twice over -- while an identity member declares `required` or `optional`.
- **Update commands** -- a command declares what it updates through one record (`update_of=UpdateOf(resource, write_mode=, identity=, properties=)` / `WithUpdateOf(resource, mode, Identity(...), Properties(...))` / `updateOf: {resource, writeMode, identity, properties}`). `write_mode` is `"sparse"` or `"full_replace"`, mandatory with no default; `properties` carries a floor of one (a compile error in Go's two-parameter `Properties` and TypeScript's `readonly [K, ...K[]]` over the command's own declared names); `update_of` on a `read_only` command is a registration error, so an update command is always mutating. Both roles resolve at **root scope only**; a property is a flag and may not be a choice flag, an identity member may be either and may be a positional arg. The framework enforces **at least one property per invocation** at parse time -- after every declared constraint, before defaults, over the same provided-ness predicate, with no source filter -- and inside an update `--no-x` WRITES false rather than declining. A property declaring `nullable` mints framework-owned `--unset-<prop>`, answered by `ctx.unset(name)` / `ctx.Unset(name)`, delivering absence with `provided()` true; value-plus-unset is a parse error; the machine doors spell the clear as `null` on the property's own key. The write set renders as one unnumbered line in the would-do log (`writes: content; clears: ttl (other properties unchanged)`) and as the envelope's `writes` member in both modes, and publishes as the `update_of` + `write_mode` command keys plus a `nullable` flag key in `--dump-schema`, and as `anyOf` plus a description block over MCP.
- Check system -- first-class check/validation framework with double-entry security. See below.

### Handler result contract

Every handler receives a context (Go and Python are ctx-first; TypeScript is args-first); there is no legacy no-ctx signature and no `ctx.emit`. The return value carries the exit code and nothing else.

- **Go**: `func(ctx *Context, kwargs map[string]interface{}) Outcome`. Return `Exit(code)`.
- **Python**: `def handler(ctx, **kwargs)` returning `int` (exit code), `None` (exit 0), or `strictcli.outcome(exit_code)`. Any other return type is a hard error. `Outcome` is branded -- it cannot be constructed directly, only via the `outcome()` factory.
- **TypeScript**: `handler: (args, ctx) => ...` returning a `number` (exit code), `undefined`/no return (exit 0), or `outcome(exitCode)`. Any other return is a hard error. `outcome()` is the only mint -- hand-forged objects are rejected.

Structured output is a separate channel: a command declares its payload's JSON Schema (`payload_schema=` / `PayloadSchema(...)` / `payloadSchema:`) and its handler supplies the value through `ctx.payload(value)` / `ctx.Payload(value)`. At most one payload per dispatch, and calling it without a declared schema is a hard error at call time. The payload is printed only under the framework-owned `--json`, and `test()`/`call()` capture it in either mode.

### Declared payload schemas

The declaration is an inline JSON Schema literal over a **closed subset**: `type` (including type lists for nullability), `properties`, `required`, `items`, `enum`, `const`, and `additionalProperties` (boolean **or** schema — the schema form is how a dynamic-key map is declared). Both duties are hard errors:

- **Registration time** — the literal is validated as written. An unknown keyword anywhere in it is rejected, including near-miss typos, which is what keeps the subset closed by construction.
- **Emission time** — the framework validates the value against the declaration where machine mode writes the envelope, and refuses a deviation, naming the path into the value (`payload["a"][1]`) and the violated constraint. Not at the `ctx.payload(...)` call: a handler supplies its payload unconditionally in both modes, so a value the envelope could not carry never fails a human-mode run.

Numbers are IEEE-754 doubles and any number whose magnitude exceeds 2^53 is refused at emission: a big identifier (a nanosecond timestamp, a 64-bit id) is a **string by declaration**. Every implementation validates the document it will actually write, so Go sees a struct through its `json` tags (`omitempty` included) and TypeScript sees BigInt as an integer token and a `Map` as an object.

Optional builder sugar constructs the same literal, one constructor per subset keyword shape: `schema_type` / `schema_array` / `schema_object` / `schema_enum` / `schema_const` (Python), `SchemaType` / `SchemaArray` / `SchemaObject` / `SchemaEnum` / `SchemaConst` (Go), `schemaType` / `schemaArray` / `schemaObject` / `schemaEnum` / `schemaConst` (TypeScript). Builders add no vocabulary and validate nothing themselves — their output is the canonical literal and passes the identical registration-time validation.

The cross-language vectors live at `conformance/payload_schema_vectors.json` (verdicts and exact error texts) and `conformance/payload_schema_builders.json` (each builder construct's literal); all three unit suites replay both. python-jsonschema, santhosh-tekuri/jsonschema v6 and hyperjump are wired as dev-only cross-checks asserting verdict agreement, and are never runtime dependencies.

### Provenance

Every resolved flag value carries a source label: `cli`, `env`, `config`, `default`, `implied` (injected by an `Implies` constraint), or `infra` (a `RelativeToRoot` default resolved through a declared infrastructure root).

- Handler access: `ctx.Source(name)` (Go, panics if the flag is unknown) / `ctx.source(name)` (Python, raises KeyError). Both accept dashed or underscored names. Python additionally exposes `ctx.source_map()`.
- `config show` displays each value's source.

### Infrastructure env vars (infra roots + handshake)

There are two kinds of declared infrastructure env vars; each is shown in help under an `Infrastructure:` section (annotated as not suppressed by `--hermetic`):

- **Infra roots** -- `WithInfraRoot(envVar, defaultPath)` (Go) / `App(infra_root={env_var: default_path})` (Python). A location root: env var value if set, else the declared default (`~` expanded), resolved EAGERLY at construction time. Resolution has no argv dependency, which is why it is hermetic-immune.
- **Handshake env vars** -- `WithHandshakeEnv(envVar, help)` (Go) / `App(handshake_env={env_var: help})` (Python). Cross-tool protocol signals set by the invoking process: no default, no eager capture, read LIVE at call time. A handshake var must not collide with a declared root.
- **`RelativeToRoot(envVar, parts...)`** -- opaque path marker relative to a declared root. Accepted as a flag `default=` (resolved when defaults are applied at parse time; source label `infra`) and as the config path (`WithConfigPathRelativeToRoot(envVar, parts...)` in Go / `App(config_path=RelativeToRoot(...))` in Python; resolved eagerly at construction). Referencing an undeclared root is a registration-time hard error.
- **Handler access**: `ctx.InfraValue(envVar)` (Go) / `ctx.infra_value(env_var)` (Python) returns `(value, ok)`. For roots the value is the construction-time resolution and the boolean is always true; for handshakes it is a live `os.environ` lookup and the boolean means "is set". Undeclared vars panic (Go) / raise KeyError (Python) -- declare everything.

### Hermetic mode

`--hermetic` is a reserved global flag on every app, intercepted by a position-aware pre-scan (alongside `--dump-schema`, `--mcp`, `--config`). Semantics:

- Skips config file loading entirely (even the default XDG path) and skips env var resolution for flags. Values come only from CLI tokens, declared defaults, and infra roots.
- Mutually exclusive with `--config` (parse error: `--hermetic and --config are mutually exclusive`).
- Cannot be combined with the `config` subcommands (parse error: `--hermetic cannot be used with config commands`).
- Does NOT suppress infra roots or handshake env vars -- those are resolved at construction / read live and are explicitly hermetic-immune.

### Programmatic invocation

`app.Call(commandPath, kwargs, opts...)` (Go) / `app.call(command_path, **kwargs)` (Python; async variant `app.acall(...)` runs the handler in a thread) / `app.call(commandPath, kwargs, opts)` (TypeScript). Runs a command in-process with pre-typed values, bypassing CLI parsing, env var resolution, config loading, and stdin handling.

- `commandPath` is dot-separated (`"deploy"`, `"dns.zone.create"`). Kwargs use underscored parameter names. Passthrough commands take a single `_args` key with the raw argument list.
- Returns the handler's structured data when present, else the exit code (Go) / the handler's return value (Python).
- Failures (unknown command, missing required flags, election and scope violations, constraint violations) produce `InvokeError` -- returned as the error in Go, raised as an exception in Python. A choice flag's value is the same record a handler receives: a choice instance (Python), `Elect(<choice>, Fields{...})` (Go), the union member object (TypeScript).
- **Consent is explicit.** A command declared `consequential` is refused unless the call states consent: `approve_consequential=True` (Python keyword-only), `strictcli.WithApproveConsequential()` (Go `CallOption`), `{ approveConsequential: true }` (TypeScript `CallOptions`). The refusal is `command '<path>' is consequential: the call must carry confirmation`. Read-only and plain mutating commands are unaffected, and `test()` is unaffected -- it takes argv, so it can carry `--approve-consequential` itself. Over MCP the same consent is a top-level `tools/call` param (`approve_consequential`), never a member of `arguments` -- inside `arguments` it is an unknown parameter of the command's own namespace, and a non-boolean value is a `-32602` protocol error. `approve_consequential` is a reserved name in every implementation: declaring a flag OR a positional arg of that name is a registration-time hard error. Tool descriptors and MCP `tools/list` publish `effect` and `consequential` beside the argument schema so a caller can see the requirement before it calls. Over the current protocol revision (`2026-07-28`) the server ASKS instead of taking the caller's word for it: an unconsented consequential `tools/call` from a client that declared elicitation support is answered with a confirmation request plus an opaque `requestState`, and the retry echoing that state back with an acceptance is what consents. The state is HMAC-protected under a per-process key, bound to the declared client and to a digest of that exact request, expires in five minutes and is single-use. A client whose declaration does not cover a form elicitation is answered `-32021` with `data.requiredCapabilities`, never run. The retained `initialize` handshake now answers `2025-11-25`, the newest handshake-based revision, and asks that era's way -- a server-initiated `elicitation/create` request whose JSON-RPC id IS the same continuation blob, so one mint-and-verify path serves both eras; there, anything but an explicit acceptance aborts, and a legacy client that declared no elicitation gets the seam's refusal instead. The server declares the feature by name (`dev.smmh.strictcli/consequential-confirmation`) in `server/discover` and under `capabilities.experimental` in the handshake. The published page is `.stricttools/docs/mcp-confirmation.md`.

### Check system

Enabled via `WithChecks(path)` (Go) / `checks_path=` (Python), pointing to a TOML file (source of truth, committed to repo). Checks are registered in code via `@app.error_check("name")` / `@app.warn_check("name")` (Python), `app.RegisterErrorCheck("name", fn)` / `app.RegisterWarnCheck("name", fn)` (Go), and `app.errorCheck("name", fn)` / `app.warnCheck("name", fn)` (TypeScript). The registration FORM must match the TOML-declared severity -- registering a `severity = "warn"` check through the error form is a hard error. Declaration and registration must agree -- declared but unregistered or registered but undeclared are errors (double-entry security). The TOML file requires a top-level `app` field that must match the app name.

**TOML schema**: Required top-level `app` field (must match app name). `[checks.<name>]` sections with required fields: `tags` (list of strings), `severity` ("error"/"warn"), `fast` (bool), `pure` (bool), `needs_network` (bool), `depends_on` (list of check names). Check names: `[a-z][a-z0-9-]*`. Every field must be explicit -- no defaults section. The `[checks]` section is optional -- an `app` field with no checks is a valid TOML file.

**Check command**: auto-registered when checks are enabled via `WithChecks(path)` (Go) or `checks_path=` (Python). 5 own flags: `--all`, `--tag <dsl>`, `--name <glob>`, `--list`, `--ignore-warnings`. `--verbose`, `--dry-run` and `--json` are NOT among them -- all three names are framework-reserved and their values reach the handler on the Context, so the check command subsumes them rather than declaring them. Its machine output is the command's payload, emitted under the framework-owned `--json`. A candidate flag is also dropped when the app already declares a global flag of that name. No flags = show help. Hidden from help when no TOML exists.

**Tag DSL**: `--tag` accepts a set-operation expression. Operators by precedence (tightest first): `!` (NOT), `&` (AND), `^` (XOR), `|` (OR), `-` (DIFF). Parentheses for grouping. Example: `--tag "(release | changelog) & !slow"`.

**CheckOutcome**: `status` (pass/fail/warn/skip), `message` (str), `details` (list of str), `notes` (informational messages recorded via `Note()`, verdict-inert). It is minted ONLY through a reporter method -- `Passed(message)`, `Skipped(reason)`, or `Found(message)` after accumulating problems via `Error(text)` / `Warn(text)`; a `WarnReporter` structurally lacks `Error`, so a warn check cannot cascade. Warn causes nonzero exit unless `--ignore-warnings`. `CheckRunResult` wraps `CheckOutcome` with `DurationMs` (wall-clock timing in integer milliseconds).

**CheckContext**: protocol/interface with single required field `ProjectRoot() string` / `project_root: Path`. Tool sets a factory via `app.set_check_context(factory)` / `app.SetCheckContext(factory)`.

**depends_on**: DAG resolution with cycle detection. Dependency failure skips dependents. Filtered-out dependencies are pulled back in when a selected check depends on them.

**Schema integration**: `--dump-schema` includes a `checks` top-level key when checks are enabled.

**`cli-test-coverage` (built-in provider check)**: enabled by declaring the directory that holds the app's coverage state -- `test_coverage_dir=` (Python) / `WithTestCoverageDir(path)` (Go) / `testCoverageDir:` (TypeScript). That directory is the anchor for everything the mechanism reads and writes: `coverage/<pid>.jsonl` shard files, appended by every `test()` and `call()` dispatch, and `test-coverage.json`, the committed manifest the verdict is derived from. Nothing is resolved against the process's working directory, so a `chdir` between construction and dispatch moves neither, and an installed CLI started in a consumer's project writes nothing there. The declaration is the whole switch: undeclared means off, and a declared directory that does not exist at construction also means off -- no check registered, no paths computed, nothing created, which is the state an installed distribution is in. The retired boolean (`test_coverage=True` / `WithTestCoverage()` / `testCoverage: true`) is refused at registration with an error naming the directory option.

**Hooks**: strictcli does NOT manage `.git/hooks/`. External tools call `myapp check --tag pre-push` from their own hook scripts.

## Release workflow

`python/` and `go/` release independently via `rlsbl release` from their own directories. The TypeScript implementation is the releasable `ts-strictcli` and releases via `rlsbl monorepo release` from the repo root (changelog entries are added from inside `typescript/` and land in `.rlsbl-monorepo/releasables/ts-strictcli/changes/`). See sub-project CLAUDE.md files and the parent `~/Projects/CLAUDE.md` for rlsbl details.

## Useful commands

```bash
# Check rlsbl status across sub-projects
cd python && rlsbl status
cd go && rlsbl status
cd typescript && rlsbl status
cd conformance && rlsbl status

# API surface check
cd conformance && python check_api_surface.py

# Error message parity check
cd conformance && python check_error_parity.py
```
