# strictcli

A CLI framework for the Era of Agents: nothing is inferred, everything is declared. First-class support for Go, Python, and TypeScript (Go implementation).

strictcli makes you declare everything -- every command, flag, argument, and environment variable must have help text or the framework panics at registration time. Four types only: `str`, `bool`, `int`, `float`. No magic type inference, no implicit defaults.

There are Python and TypeScript implementations too, and this one is not a port
of either. The surface here is Go's own -- functional options, typed constants,
generics for typed kwargs access, and a panic at registration -- and some of the
enforcement exists only in Go, because only Go's shape provides it: the presence
options write an unexported field, so a `Flag` struct literal that bypasses them
declares nothing and fails to register rather than silently dropping its value
at parse time. What the three implementations hold identical is behavior: the
same semantics, the same help bytes, the same schema, and the same error
sentence with Go's spellings inside it.

## Installation

```
go get github.com/stricttools/strictcli/go/strictcli
```

Requires Go 1.25+. One dependency: [go-toml-edit](https://github.com/smm-h/go-toml-edit) for TOML config/checks support.

## Quickstart

```go validate
package main

import (
    "fmt"
    "strings"

    "github.com/stricttools/strictcli/go/strictcli"
)

func main() {
    app := strictcli.NewApp("greet", "1.0.0", "A greeting app")

    app.Command("hello", "Say hello",
        func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
            name := strictcli.Get[string](kwargs, "name")
            loud := strictcli.Get[bool](kwargs, "loud")
            msg := fmt.Sprintf("Hello, %s!", name)
            if loud {
                msg = strings.ToUpper(msg)
            }
            ctx.Info(msg)
            return strictcli.Exit(0)
        },
        strictcli.WithEffect(strictcli.EffectReadOnly),
        strictcli.WithFlags(
            strictcli.StringFlag("name", "Who to greet", strictcli.Required()),
            strictcli.BoolFlag("loud", "Shout it", strictcli.Default(false)),
        ),
    )

    app.Run()
}
```

Name, version, and help are all required `NewApp` arguments, and every command
must declare its effect: `WithEffect(EffectReadOnly)` or
`WithEffect(EffectMutating)`. There is no default -- omitting it panics at
registration time.

```
$ greet hello --name World
Hello, World!

$ greet hello --name World --loud
HELLO, WORLD!

$ greet hello --help
greet hello -- Say hello

Flags:
  --name <str>         Who to greet [required]
  --loud, --no-loud    Shout it [default: false]
```

## Features

### Commands and groups

Top-level commands with `app.Command`, nested groups with `app.Group`. Groups nest recursively to arbitrary depth via `group.Group`.

```go
db := app.Group("db", "Database operations")
schema := db.Group("schema", "Schema management")

schema.Command("migrate", "Run migrations",
    func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
        ctx.Info("migrating")
        return strictcli.Exit(0)
    },
    strictcli.WithEffect(strictcli.EffectMutating),
)
```

Invoked as `myapp db schema migrate`.

### The presence declaration

Every flag declares EXACTLY ONE of three facts about itself, and every
positional arg does the same. Nothing is inferred from the shape of another
declaration.

| Fact | Flag | Arg |
|------|------|-----|
| a value must be supplied | `Required()` | `ArgRequired()` |
| absence is legal, and is delivered as absence | `Optional()` | `ArgOptional()` |
| the framework supplies this value when nothing else does | `Default(v)` | `ArgDefault(v)` |

Declaring none is a registration-time hard error, and so is declaring two.
`Default(nil)` is refused with a message redirecting to `Optional()`: a
null-valued default is not a spelling of optionality, and optionality has
exactly one spelling.

```go
strictcli.StringFlag("target", "Where to deploy", strictcli.Required()),
strictcli.StringFlag("note", "An optional note", strictcli.Optional()),
strictcli.IntFlag("retries", "How many retries", strictcli.Default(3)),
```

Requiredness is satisfied by ANY source that provides a value -- a command-line
token, an env var, a config file, or an `Implies` injection -- not by a typed
token specifically. A `Flag` STRUCT LITERAL passes through no option, so it
declares no presence and does not register; build flags through the
constructors.

`ctx.Provided(name)` answers the question the declaration makes askable: true
when the invocation caused the value (`cli`, `env`, `config`, `implied`), false
when the declaration did (`default`, `infra`).

### Four flag types

`StringFlag`, `BoolFlag`, `IntFlag`, `FloatFlag`. No magic coercion -- parse errors are clear and immediate.

```go
strictcli.StringFlag("output", "Output path", strictcli.Default("out.txt")),
strictcli.BoolFlag("cache", "Reuse the build cache", strictcli.Default(true)),
strictcli.IntFlag("port", "Port number", strictcli.Required()),
strictcli.FloatFlag("threshold", "Score threshold", strictcli.Optional()),
```

Bool flags support `--flag` / `--no-flag` negation (disable with `NegatableOpt(false)`). A required bool must be passed explicitly as `--flag` or `--no-flag`; an optional one is a real tri-state -- `--flag` is true, `--no-flag` is false, absent is nil. Float parsing rejects NaN and Inf.

### Compound types

`ListFlag` and `DictFlag` for collecting multiple values.

```go
strictcli.ListFlag(strictcli.TypeStr, "tags", "Tags to apply", strictcli.Unique(true), strictcli.Optional()),
strictcli.DictFlag(strictcli.TypeStr, "env", "Environment variables", strictcli.Unique(false), strictcli.Default(map[string]interface{}{})),
```

List flags accept `--tags a --tags b`. Dict flags accept `--env KEY=VALUE` pairs or JSON objects. Both are always repeatable and therefore require an explicit `Unique(true)` or `Unique(false)`.

Compound flags declare presence like everything else: `Optional()` delivers nil when nothing arrives, `Default([]interface{}{})` / `Default(map[string]interface{}{})` declares an empty collection, and `Required()` demands at least one value from some source. There is no silent empty default.

### Positional arguments

Every arg declares exactly one of `ArgRequired()`, `ArgOptional()` or `ArgDefault(v)`, and can be variadic (`Variadic()`). An optional arg delivers a present key holding nil, never key-absence. A variadic arg always delivers a list, so `ArgRequired()` means at least one value, `ArgOptional()` means possibly none, and `ArgDefault(v)` on one is a registration error.

```go
app.Command("copy", "Copy files",
    handler,
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithArgs(
        strictcli.NewArg("src", "Source path", strictcli.ArgRequired()),
        strictcli.NewArg("dst", "Destination path", strictcli.ArgRequired()),
    ),
)
```

### Short flag aliases

Single-character shortcuts for any flag.

```go
strictcli.BoolFlag("recursive", "Recurse into subdirectories", strictcli.Short("r"), strictcli.Default(false)),
strictcli.StringFlag("output", "Output path", strictcli.Short("o"), strictcli.Default(".")),
```

### Environment variable binding

Flags can be backed by environment variables. Prefix enforcement keeps your config namespace clean.

```go
app := strictcli.NewApp("myapp", "1.0.0", "My app", strictcli.WithEnvPrefix("MYAPP"))

strictcli.StringFlag("region", "Cloud region",
    strictcli.Env("MYAPP_REGION"), strictcli.Default("us-east-1")),
```

All env vars must start with the declared prefix. Use `Prefixed(false)` for external env vars. Precedence: CLI > env > config > default.

Bool env vars accept `1|true|yes` / `0|false|no` (case-insensitive).

### FlagSets

Reusable bundles of flags shared across commands.

```go
authFlags := strictcli.FlagSet{
    Name: "auth",
    Flags: []strictcli.Flag{
        strictcli.StringFlag("token", "Auth token", strictcli.Required()),
        strictcli.BoolFlag("insecure", "Skip TLS verification", strictcli.Optional()),
    },
}

app.Command("deploy", "Deploy", handler,
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithFlagSets(authFlags),
)
```

### Choice flags: a choice is a declaration scope

A **choice flag** elects exactly one of its declared choices, and each choice
owns the flags that exist only while it is elected. A flag supplied outside its
elected scope is a distinct parse error naming both sides -- never "unknown
flag". `Choice(...)` returns a value with identity, so `When(ViaEmail, ...)` is a
compile-checked reference and a typo does not compile.

```go
var (
    ViaEmail = strictcli.Choice("email", "deliver the notification as an email message",
        strictcli.StringFlag("subject", "subject line", strictcli.Required()),
    )
    ViaSMS = strictcli.Choice("sms", "deliver the notification as a text message",
        strictcli.StringFlag("phone-number", "destination number", strictcli.Required()),
    )
)

app.Command("send", "Send one notification", handler,
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithFlags(strictcli.ChoiceFlag("via", "Delivery channel",
        strictcli.Required(), strictcli.Short("v"), ViaEmail, ViaSMS)),
)

// in the handler:
via := strictcli.GetElected(kwargs, "via")
line := strictcli.Match(via,
    strictcli.When(ViaEmail, func(f strictcli.Fields) string { return strictcli.Get[string](f, "subject") }),
    strictcli.When(ViaSMS, func(f strictcli.Fields) string { return strictcli.Get[string](f, "phone_number") }),
)
```

`notify send --via email --subject hi` parses; `notify send --via sms --subject hi`
says *`--subject` is only valid under `--via email`*. `Match` is exhaustive
against the declaration and panics naming what is missing, and
`e.Provided("subject")` answers whether the invocation caused a field's value.

`MemberChoiceFlag(...)` with `MemberChoice(memberFlag, help, scope...)` is the
member-spelled twin: each choice is its own flag (`--profile work` /
`--all-profiles`), no choice-flag token is ever typed, and election is
command-line only. A member's payload is delivered under the reserved name
`value`, and a member flag must declare `Required()` -- read as *required once
this member is elected*.

A choice flag declares `Required()` or `Default(<choice name>)`; `Optional()` is
refused, because an absent selection is a choice nobody named. A choice flag is
a flag, so one may be declared inside a choice's scope, to unlimited depth.

### Constraints

Four declared rules, all passed via `WithConstraints(...)`, each carrying a
mandatory name that identifies it in help and in every violation message:

- `AtLeastOne(name, a, b, rest...)` -- at least one member is engaged. Members
  MAY co-occur: it has no upper bound and never refuses a second member.
- `AllOrNone(name, a, b, rest...)` -- either every member is engaged or none is.
  With nothing engaged it is vacuously satisfied.
- `Requires(name, flag, dependsOn)` -- one-way dependency.
- `Implies(name, flag, implies, value)` -- auto-set a bool flag when another is
  provided; explicit contradictions are parse errors.

A member is a flag, a positional arg, or another named at-least-one or
all-or-none, referenced by name and resolved at registration. Nesting is a
cycle-checked DAG of unlimited depth. Two named members precede the variadic
tail, so a one-member constraint is a compile error.

Each flag or arg member declares WHEN it engages, from a closed vocabulary:
`WhenPresent()` (the default -- the value was provided from cli, env, config or
an implication, never a default), `WhenTrue()` (bool only), `WhenNonEmpty()`
(strings and collections only). A **bool member must declare its election**:
without that rule `--no-all` would engage a constraint while selecting nothing.

```go
app.Command("purge", "Purge archived items", handler,
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithFlags(
        strictcli.StringFlag("older-than", "Purge items older than duration", strictcli.Optional()),
        strictcli.StringFlag("larger-than", "Only purge items larger than this size", strictcli.Optional()),
        strictcli.BoolFlag("all", "Select all archived items", strictcli.Optional()),
    ),
    strictcli.WithArgs(
        strictcli.NewArg("targets", "Record UUIDs or numeric database IDs",
            strictcli.Variadic(), strictcli.ArgOptional()),
    ),
    strictcli.WithConstraints(
        strictcli.AtLeastOne("purge-selection",
            strictcli.Member("targets", strictcli.WhenNonEmpty()),
            strictcli.Member("older-than"),
            strictcli.Member("larger-than"),
            strictcli.Member("all", strictcli.WhenTrue()),
        ),
    ),
)
```

```
constraint "purge-selection": at least one of targets, --older-than, --larger-than, --all is required
```

Nesting expresses a rule over pairs -- an at-least-one over two all-or-none
groups, where a half-typed pair reports its own incompleteness first:

```go
strictcli.WithConstraints(
    strictcli.AllOrNone("author-name", strictcli.Member("old-name"), strictcli.Member("new-name")),
    strictcli.AllOrNone("author-email", strictcli.Member("old-email"), strictcli.Member("new-email")),
    strictcli.AtLeastOne("author-change", strictcli.Member("author-name"), strictcli.Member("author-email")),
)
```

No member may declare `Required()`: a member the invocation must always supply
leaves the constraint nothing to decide. Constraints reference root-scope
declarations only -- naming a flag inside a choice scope is a registration
error, because the scope already IS the constraint. Every constraint renders in
`--help` under a `Constraints:` section, is published in `--dump-schema`, and is
projected into MCP tool schemas (`anyOf` / `dependentRequired`) with anything a
keyword cannot carry stated in the tool description.

Constraints can only reference flags and args you declared, so the reserved
quartet (`dry-run`, `approve-consequential`, `quiet`, `verbose`) can never
appear in one.

### Update commands

A command that changes some properties of one resource and leaves the rest alone
declares what it updates with `WithUpdateOf(...)`. The write mode is a positional
parameter, which is Go's spelling of mandatory:

```go
app.Command("update-record", "Change one DNS record in place", handler,
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithUpdateOf("dns-record", strictcli.WriteSparse,
        strictcli.Identity("zone", "record-id"),
        strictcli.Properties("content", "ttl", "proxied"),
    ),
    strictcli.WithFlags(
        strictcli.StringFlag("zone", "Zone the record belongs to", strictcli.Required()),
        strictcli.StringFlag("record-id", "Identifier of the record", strictcli.Required()),
        strictcli.StringFlag("content", "Record content", strictcli.Optional()),
        strictcli.IntFlag("ttl", "Time to live in seconds", strictcli.Optional(), strictcli.Nullable()),
        strictcli.BoolFlag("proxied", "Whether the record is proxied", strictcli.Optional()),
    ),
)
```

`Properties(first string, rest ...string)` puts a compile-time floor of one on
the property list. A property applies `Optional()` and nothing else -- absence
*is* untouched -- and the framework refuses an invocation that supplies none:

```
error: update "dns-record": at least one property is required: --content, --ttl, --proxied
```

Because absence must never resolve to a value nobody stated, **no flag or arg on
a mutating command may apply `Default(v)`** (empty collection defaults and every
default on a read-only command stay legal). Inside an update `--no-proxied`
writes false; a `Nullable()` property mints `--unset-<prop>`, answered by
`ctx.Unset(name)`. Every run that reports what it does renders the write set --
one unnumbered line in the would-do log, and a `writes` member on the machine
envelope:

```
$ mytool --dry-run update-record --zone z1 --record-id r7 --content hi --unset-ttl
DRY RUN — no changes were made. Would do:
  writes: content; clears: ttl (other properties unchanged)
  1. net: PATCH https://api.example.com/zones/z1/dns_records/r7
```

### Global flags

App-level flags available to all commands, parsed before and after the command token.

```go
app.GlobalFlag(strictcli.BoolFlag("color", "Colorize output", strictcli.Default(true)))
```

Global flag names cannot collide with the framework's reserved names (`help`,
`h`, `version`, `v`, `dump-schema`, `mcp`, `config`, `hermetic`) or with the
reserved quartet.

### Passthrough commands

Bypass all parsing -- handler gets raw args plus global flag values.

```go
app.Passthrough("run", "Run a script",
    func(ctx *strictcli.Context, name string, args []string, globals map[string]interface{}) int {
        // args contains everything after the command name
        return 0
    },
    strictcli.WithEffect(strictcli.EffectMutating),
)
```

### Repeatable flags

Flags that accumulate values across multiple occurrences. Requires explicit `Unique(true)` or `Unique(false)`.

```go
strictcli.StringFlag("tag", "Add a tag", strictcli.Repeatable(), strictcli.Unique(true), strictcli.Optional()),
```

### Choices

Restrict flag values to an allowed set. Every entry is a `Ch(<value>, "<help>")`
record, and its help is optional -- Go spells "no help" as `""`, for lack of
optional parameters. A bare value is refused at registration.

```go
strictcli.StringFlag("format", "Output format", strictcli.Required(),
    strictcli.Choices(
        strictcli.Ch("json", "one JSON document"),
        strictcli.Ch("csv", ""),
        strictcli.Ch("xml", ""),
    )),
```

Help renders on one line (`[choices: json, csv, xml]`) until an entry carries
help, at which point the whole flag renders as an indented block. The boundary
against a choice flag is structural, not a matter of taste: **need a scope or
member spelling -> choice flag; a plain constrained value -> choices.**

### Custom validation

Per-flag validation functions.

```go
strictcli.IntFlag("port", "Port number",
    strictcli.ValidateFn(func(v interface{}) error {
        if n := v.(int); n < 1 || n > 65535 {
            return fmt.Errorf("port must be 1-65535, got %d", n)
        }
        return nil
    }),
    strictcli.Required(),
),
```

### Deprecated commands

Register retired commands that print a message to stderr and exit 1.

```go
app.Deprecated("old-cmd", "Use 'new-cmd' instead")
group.Deprecated("legacy-lint", "Use 'lint' instead")
```

Deprecated commands appear in help output under a `Deprecated:` section.

### Hidden commands and groups

Commands and groups can be hidden from help output while remaining functional.

```go
app.Command("internal-debug", "Debug internals", handler,
    strictcli.WithEffect(strictcli.EffectReadOnly),
    strictcli.WithHidden(),
)
```

### JSON config file support

Reads `~/.config/{name}/config.json` (or TOML). Auto-registers `config show/set/path/edit` subcommands, where `config set <key> --value <v>` writes under a required selector over a value, a clear (`--clear`) and a reset to the declared default (`--default`).

```go
app := strictcli.NewApp("myapp", "1.0.0", "My app", strictcli.WithConfig())
```

Precedence: CLI > env > config > default. Config fields can be declared with typed validation:

```go
app.ConfigField("serve.port",
    strictcli.ConfigFieldType(strictcli.TypeInt),
    strictcli.ConfigFieldHelp("Server port"),
    strictcli.ConfigFieldDefault(8080),
)
```

### The effects regime

Every command declares `WithEffect(EffectReadOnly)` or
`WithEffect(EffectMutating)` -- there is no default and no inference. A read-only
command changes nothing and calling a mutating member of `ctx.Effects()` from one
is a hard error at call time. A mutating command participates in `--dry-run`,
where the eight recorded operations (`Run`, `Spawn`, `Write`, `Mkdir`, `Remove`,
`Rename`, `Chmod`, `HTTP`) are recorded rather than performed and rendered as a
would-do log.

Four flag names are owned by the framework and cannot be declared at any level
(global flags, command flags, flag sets, and a choice's scope at any depth).
They arrive on the context, never in `kwargs`:

| Flag | Context accessor |
|------|-----------------|
| `--dry-run` | `ctx.DryRun()` |
| `--approve-consequential` | `ctx.ApproveConsequential()` |
| `--quiet` | `ctx.Quiet()` |
| `--verbose` | `ctx.Verbose()` |

A flag named `yes` is banned outright -- the confirmation skip is
`--approve-consequential`.

A command whose preview would lie declares the refusal instead of rendering one:

```go
app.Command("migrate", "Run migrations", handler,
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithDryRunUnsupported("each migration reads the schema the previous one wrote"),
)
```

`--dry-run` is then refused at parse time with the reason, which also appears in
the command's help under a `Dry run:` section and in the schema.

### Consequential commands

`WithConsequential()` is the only thing that makes the framework prompt -- a
plain mutating command never does. Classification answers "should a dry run
record this?"; consequential answers "are these effects worth interrupting
someone for?"

```go
app.Command("destroy", "Destroy the cluster", handler,
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithConsequential(),
)
```

Before dispatch the framework prints `about to run consequential command
'destroy'. Proceed? [y/N] ` to stderr and reads one line from stdin; only `y` or
`Y` proceeds. `--approve-consequential` answers in advance, and `--dry-run`
skips the prompt because nothing is being performed. A non-interactive stdin
without either flag is a hard error rather than a hang. Declaring
`WithConsequential()` on a read-only command panics.

### Schema dump

`--dump-schema` is auto-injected on every app. Writes `.strictcli/schema.json` describing the full CLI structure (commands, flags, args, groups, checks) at `schema_version: 2`. Every command entry carries its `effect`; `consequential`, `dry_run_supported` and `dry_run_unsupported_reason` are emitted only when declared.

Every flag and arg entry carries a `value_schema`: a real JSON Schema fragment from a closed subset of `type`, `items`, `additionalProperties` and `enum`, using JSON Schema's own type names. Arity is part of the value's shape, so a repeatable scalar flag and a `ListFlag` publish the identical array fragment. A choice flag carries no fragment -- its value is a variant the subset cannot express -- and publishes its nested `choices` and scopes instead, each scoped entry a full flag entry, with `elect_by` marking the spelling. A value flag's `Choices(...)` splits in two: the values as an `enum` inside the fragment, and the value-plus-help records beside it under `choices`.

Keys are emitted in a declared order at every depth, and the document is written by a canonical writer rather than `encoding/json`: canonical floats, raw UTF-8, no HTML escaping, two-space indent, exactly one trailing newline. A schema file written by this implementation and one written by the Python or TypeScript implementation for the same declaration are byte-identical. The `defaults` block is the complete map of what an omitted key means, and `config_format`, `config_path`, `config_conflict_mode`, per-flag `prefixed` and per-command `flag_sets` appear exactly when a declaration departs from the framework's own behavior.

### Check system

First-class check/validation framework with double-entry security. Enabled via `WithChecks(path)` pointing to a TOML file.

```go
app := strictcli.NewApp("myapp", "1.0.0", "My app", strictcli.WithChecks("checks.toml"))

app.RegisterErrorCheck("lint", func(ctx strictcli.CheckContext, r *strictcli.ErrorReporter) strictcli.CheckOutcome {
    return r.Passed("All good")
})
```

Checks are declared in TOML and registered in code -- both must agree. Registration form matches the declared severity: `RegisterErrorCheck` for `severity = "error"` checks (reporter has `Error` and `Warn`), `RegisterWarnCheck` for `severity = "warn"` checks (reporter structurally lacks `Error`). A `CheckOutcome` is minted only via reporter methods: `Passed(message)`, `Skipped(reason)`, or `Found(message)` after accumulating problems with `r.Error(text)` / `r.Warn(text)`; `r.Note(text)` records verdict-inert informational notes. Auto-registers a `check` command with tag DSL filtering (`--tag "release & !slow"`), JSON output, and dependency resolution.

### Context

`Context` is constructed by the framework for every dispatch and passed as the first argument to every handler. It provides structured output methods -- `Info(msg)` (stdout, suppressed under `--quiet`), `Warn(msg)` (stderr), `Debug(msg)` (stdout, shown only under `--verbose`), `Error(msg)` (stderr) -- plus provenance: `Source(name)` returns where a flag's value came from (`"cli"`, `"env"`, `"config"`, `"default"`, `"implied"`, or `"infra"`), `Provided(name)` reports whether the invocation caused the value rather than the declaration, and `InfraValue(envVar)` reads a declared infrastructure env var.

It also carries the reserved quartet and the effects handle: `DryRun()`,
`ApproveConsequential()`, `Quiet()`, `Verbose()`, and `Effects()`.

### Tool export

`app.AsTools()` exports non-hidden commands as `Tool` descriptors for LLM agents.

```go
tools := app.AsTools()
// Each Tool has: Name, Description, Parameters (JSON Schema), Effect,
// Consequential, Execute
```

`Effect` and `Consequential` publish the effects-regime classification beside
the argument schema, so a caller can see before it calls that a tool changes
things and that invoking it requires stating consent:

```go
_, err := release.Execute(nil)
// err: command 'release' is consequential: the call must carry confirmation
_, err = release.Execute(nil, strictcli.WithApproveConsequential()) // proceeds
```

### MCP server

`app.ServeMCP()` runs a JSON-RPC 2.0 MCP server on stdin/stdout, exposing commands as tools for AI clients. Triggered via `--mcp` flag.

### Help and version

- `--help` / `-h` recognized anywhere in argv, at app, group, and command levels
- `--version` / `-v` prints app version
- Help is auto-generated with flag types, defaults, env var names, and choices

## Handlers

Every command handler has the ctx-first signature:

```go
func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome
```

`kwargs` holds the parsed flag and arg values, keyed by parameter name (hyphens converted to underscores: `--log-file` becomes `log_file`). The reserved quartet is never in `kwargs` -- it arrives on `ctx`. `ctx` provides structured output and provenance (see Context above).

### Outcome

`Outcome` is an opaque, branded return type. It carries the exit code and nothing else, and it is constructed ONLY via one function:

```go
return strictcli.Exit(0)  // exit code
```

Structured output is a separate channel: the command declares its payload's JSON Schema with `PayloadSchema(...)` and the handler supplies the value with `ctx.Payload(value)` (at most once per dispatch; calling it without a declared schema is a hard error). The payload is printed only under the framework-owned `--json`, as the envelope's `payload` member, and is available programmatically via `Test` (in `Result.Data`) and `Call` in either mode.

### Typed kwargs accessors

`Get[T]` and `GetOpt[T]` replace raw type assertions on `kwargs`:

```go
name := strictcli.Get[string](kwargs, "name")      // panics if absent, nil, or wrong type
port, ok := strictcli.GetOpt[int](kwargs, "port")  // (zero, false) when the value is nil (not provided)
```

`Get` treats a nil value as an error (nil means "not provided"); use `GetOpt` for optional flags without defaults.

### Passthrough handlers

Passthrough commands bypass parsing and use a distinct signature returning a plain exit code:

```go
func(ctx *strictcli.Context, name string, args []string, globals map[string]interface{}) int
```

## Testing

`app.Test(argv)` runs the CLI in-process and returns a `Result`:

```go
result := app.Test([]string{"hello", "--name", "World", "--loud"})

if result.ExitCode != 0 {
    t.Fatalf("expected exit 0, got %d: %s", result.ExitCode, result.Stderr)
}
if !strings.Contains(result.Stdout, "HELLO, WORLD!") {
    t.Fatalf("unexpected output: %q", result.Stdout)
}
```

`Result` carries `Stdout`, `Stderr`, `ExitCode`, and `Data` (the payload the handler supplied through `ctx.Payload`, `nil` otherwise).

## API reference

### Constructors

```go
app  := strictcli.NewApp(name, version, help, opts ...AppOption)
flag := strictcli.StringFlag(name, help, opts ...FlagOption)
flag := strictcli.BoolFlag(name, help, opts ...FlagOption)
flag := strictcli.IntFlag(name, help, opts ...FlagOption)
flag := strictcli.FloatFlag(name, help, opts ...FlagOption)
flag := strictcli.ListFlag(itemType, name, help, opts ...FlagOption)
flag := strictcli.DictFlag(valueType, name, help, opts ...FlagOption)
flag := strictcli.ChoiceFlag(name, help, opts ...FlagOption)
flag := strictcli.MemberChoiceFlag(name, help, opts ...FlagOption)
ch   := strictcli.Choice(name, help, flags ...Flag)
ch   := strictcli.MemberChoice(memberFlag Flag, help string, scope ...Flag)
cv   := strictcli.Ch(value interface{}, help string)
arg  := strictcli.NewArg(name, help, opts ...ArgOption)
```

`Choice` and `MemberChoice` produce `*ChoiceDecl` values, which are passed to
`ChoiceFlag` / `MemberChoiceFlag` as `FlagOption`s. In a handler,
`GetElected(kwargs, name)` returns the `*Elected` record, and
`Match(e, When(ch, fn)...)` dispatches on it exhaustively.

### Flag options

| Function | Description |
|----------|-------------|
| `Short(s)` | Single-character alias |
| `Required()` | A value must be supplied, from any source |
| `Optional()` | Absence is legal and is delivered as nil |
| `Default(v)` | The value the framework supplies when nothing else does |
| `Env(varName)` | Environment variable name |
| `Prefixed(b)` | Control env prefix validation |
| `Choices(vals...)` | Restrict to allowed values, one `Ch(<value>, "<help>")` record each |
| `Repeatable()` | Accept multiple occurrences |
| `Unique(b)` | Deduplicate repeatable values |
| `ValidateFn(fn)` | Custom validation function |
| `NegatableOpt(b)` | Control `--no-X` form for bool flags |

### Arg options

| Function | Description |
|----------|-------------|
| `ArgRequired()` | A value must be supplied |
| `ArgOptional()` | Absence is legal and is delivered as nil |
| `ArgDefault(v)` | The value supplied when the arg is absent |
| `Variadic()` | Collect remaining positional values |

### Command options

| Function | Description |
|----------|-------------|
| `WithFlags(flags...)` | Add flags to a command |
| `WithArgs(args...)` | Add positional arguments |
| `WithFlagSets(flagSets...)` | Attach flag set bundles |
| `WithConstraints(cs...)` | Add AtLeastOne/AllOrNone/Requires/Implies constraints |
| `WithPassthrough(handler)` | Mark as passthrough command |
| `WithHidden()` | Hide from help output |
| `WithEffect(effect)` | **Mandatory.** `EffectReadOnly` or `EffectMutating` |
| `WithConsequential()` | Prompt for confirmation before dispatch |
| `WithDryRunUnsupported(reason)` | Refuse `--dry-run` with a mandatory reason |
| `WithGrants(grants...)` | Declare why a dangerous step is authorized |
| `WithForwarding(reason)` | Declare that the handler forwards its arguments |

### App options

| Function | Description |
|----------|-------------|
| `WithEnvPrefix(prefix)` | Set env var prefix |
| `WithConfig()` | Enable config file support |
| `WithConfigPath(path)` | Override config file path |
| `WithConfigFormat(fmt)` | Set config format ("json" or "toml") |
| `WithChecks(path)` | Enable check system |
| `WithChecksEmbed(data)` | Enable check system with embedded TOML |

### App methods

| Method | Description |
|--------|-------------|
| `app.Command(name, help, handler, opts...)` | Register a command |
| `app.Passthrough(name, help, handler, opts...)` | Register a passthrough command |
| `app.Group(name, help)` | Create a command group |
| `app.GlobalFlag(flag)` | Register a global flag |
| `app.Deprecated(name, message)` | Register a deprecated command |
| `app.Run()` | Parse `os.Args` and execute |
| `app.Test(argv)` | Run in-process, return `Result` |
| `app.Call(commandPath, kwargs, opts...)` | Invoke a command programmatically; returns the handler's payload (`ctx.Payload`) or the exit code. `WithApproveConsequential()` is the consent a consequential command requires |
| `app.AsTools()` | Export commands as `Tool` descriptors |
| `app.ServeMCP()` | Run MCP server on stdin/stdout |
| `app.ConfigField(name, opts...)` | Declare a typed config field |
| `app.RegisterErrorCheck(name, fn)` | Register an error-severity check handler |
| `app.RegisterWarnCheck(name, fn)` | Register a warn-severity check handler |
| `app.SetCheckContext(factory)` | Set the check context factory |

### Core types

| Type | Description |
|------|-------------|
| `App` | Root CLI application |
| `Command` | Leaf command with handler |
| `Group` | Container for nested commands |
| `Flag` | Flag declaration |
| `Arg` | Positional argument |
| `FlagSet` | Reusable flag bundle |
| `ChoiceDecl` | One choice of a choice flag: a name, help, and a scope (minted by `Choice` / `MemberChoice`) |
| `Elected` | The delivered tagged record: the elected `*ChoiceDecl` plus its `Fields` |
| `ChoiceValue` | One entry of a `Choices(...)` value flag: a value with optional help (minted by `Ch`) |
| `Constraint` | A declared rule, minted by `AtLeastOne` / `AllOrNone` / `Requires` / `Implies` |
| `ConstraintMember` | One operand of a co-occurrence constraint (minted by `Member`) |
| `Result` | Return type of `app.Test()` (Stdout, Stderr, ExitCode, Data) |
| `Tool` | LLM tool descriptor |
| `Outcome` | Branded handler return type (via `Exit` only) |
| `Context` | Structured output and provenance (Info/Warn/Debug/Error/Source/InfraValue) |
| `CheckOutcome` | Check result, minted only via reporter methods |
| `ErrorReporter` / `WarnReporter` | Problem accumulators passed to check handlers |
| `CheckContext` | Interface for check context |
| `ConfigField` | Typed config file field |

## Design principles

- **Help is mandatory.** Every command, flag, and argument must have help text. Missing help panics at registration time.
- **Four types only.** `str`, `bool`, `int`, `float` -- plus compound `list` and `dict`. No magic type coercion.
- **One handler contract.** `func(ctx *Context, kwargs map[string]interface{}) Outcome`, with kwargs keyed by parameter name (hyphens become underscores), exit codes flowing only through `Exit`, and structured output only through `ctx.Payload` against a declared `PayloadSchema`.
- **Effect classification is mandatory.** Every command declares `EffectReadOnly` or `EffectMutating`. There is no default and no inference.
- **Presence is mandatory.** Every flag and arg declares required, optional, or a value default. Zero declarations and two declarations are both registration-time errors, and requiredness is never derived from whether a default happens to exist.
- **Registration-time errors.** Misconfigurations panic loud and early, not at parse time.
- **Minimal dependencies.** One dependency ([go-toml-edit](https://github.com/smm-h/go-toml-edit)) for TOML support.

## See also

- [strictcli monorepo](https://github.com/stricttools/strictcli) -- conformance tests, Python implementation, and project documentation
- [Python implementation](https://github.com/stricttools/strictcli/tree/main/python) -- same semantics, decorator-based API

## License

MIT
