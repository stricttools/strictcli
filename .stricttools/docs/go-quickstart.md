+++
title = "Go Quickstart"
description = "Build Go CLIs with strictcli: WithEffect classification, Required/Optional/Default presence, Choices and RetiredChoices, choice flags with Match, constraints, and update commands."
nav_group = "Guides"
nav_order = 1
+++

# Go Quickstart

This guide walks through building a CLI application with the Go implementation of strictcli.

Everything below is Go-shaped on purpose -- functional options, typed
constants, package-private fields that make a half-written declaration fail to
register -- and none of it is a transliteration of the Python or TypeScript
surface. The three implementations are identical in behavior, not in spelling;
see [Language idioms](language-idioms.md).

## Install

```bash
go get github.com/stricttools/strictcli/go/strictcli@latest
```

Import the package:

```go
import "github.com/stricttools/strictcli/go/strictcli"
```

## Creating an App

Every CLI starts with `NewApp`, which takes the application name, version
string, and help text as its three required arguments. Empty help text is a hard
error -- strictcli enforces self-documenting apps from the first line of code.
Additional options like `WithConfig()`, `WithEnvPrefix()`, and `WithConfigFormat()`
are passed as functional options after the help text.

```go validate
package main

import "github.com/stricttools/strictcli/go/strictcli"

func main() {
    app := strictcli.NewApp("mytool", "0.1.0", "A tool that does useful things")
    app.Command("hello", "Print a greeting", func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
        ctx.Info("Hello, world!")
        return strictcli.Exit(0)
    }, strictcli.WithEffect(strictcli.EffectReadOnly))
    app.Run()
}
```

Running this:

```
$ mytool hello
Hello, world!

$ mytool --help
mytool v0.1.0 -- A tool that does useful things

Commands:
  hello    Print a greeting

$ mytool --version
mytool 0.1.0
```

## Command Classification

Every command must declare what it does to the world. `WithEffect(...)` is a
mandatory `CmdOption` taking one of two exported constants -- there is no
default, and a command registered without it panics at registration time:

| Constant | Meaning |
|----------|---------|
| `strictcli.EffectReadOnly` | The command changes nothing. It never prompts, and calling a mutating member of the effects handle from it is a hard error at call time. |
| `strictcli.EffectMutating` | The command changes something. It participates in `--dry-run`, where its effects are recorded instead of performed. |

```go
app.Command("status", "Show deployment status", statusHandler,
    strictcli.WithEffect(strictcli.EffectReadOnly))

app.Command("deploy", "Deploy the app", deployHandler,
    strictcli.WithEffect(strictcli.EffectMutating))
```

Classification answers one question -- "should a dry run record this rather than
perform it?" It is deliberately **not** the same question as "is this dangerous
enough to interrupt someone for?", which is what
[`WithConsequential()`](#consequential-commands-and-the-confirm-protocol)
answers. A mutating command does not prompt unless it also declares itself
consequential.

Classification is a property of the command, so it is emitted in `--dump-schema`
output on every command entry and can be asserted against by check gates.
Deprecated commands are exempt: they have no handler, execute nothing, and
passing an effect to `app.Deprecated(...)` is a registration-time error.

## Handler Signature

Every command handler has a fixed signature that receives a context for
structured output and provenance, a map of parsed flag and arg values, and
returns a branded `Outcome` type that wraps the exit code:

```go
func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome
```

- `ctx` provides structured output (`ctx.Info`, `ctx.Warn`, `ctx.Error`, `ctx.Debug`), provenance (`ctx.Source`), the four reserved-quartet values (`ctx.DryRun()`, `ctx.ApproveConsequential()`, `ctx.Quiet()`, `ctx.Verbose()`), and the effects handle (`ctx.Effects()`).
- `kwargs` is a map of flag and arg values, keyed by parameter name (dashes converted to underscores: `--log-file` becomes `log_file`). The reserved quartet is never in `kwargs` -- it arrives on `ctx`.
- Return `Exit(code)`. Structured output goes through `ctx.Payload(value)` on a command that declares `PayloadSchema(...)`, and is emitted under the framework-owned `--json`.

Use the typed helpers `Get` and `GetOpt` to extract values from kwargs:

```go
app.Command("greet", "Greet someone", func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
    name := strictcli.Get[string](kwargs, "name")
    loud, provided := strictcli.GetOpt[bool](kwargs, "loud")

    msg := "Hello, " + name + "!"
    if provided && loud {
        msg = "HELLO, " + name + "!!!"
    }
    ctx.Info(msg)
    return strictcli.Exit(0)
}, strictcli.WithEffect(strictcli.EffectReadOnly), strictcli.WithFlags(
    strictcli.StringFlag("name", "Who to greet", strictcli.Required()),
    strictcli.BoolFlag("loud", "Shout the greeting", strictcli.Optional()),
))
```

`Get[T]` panics if the key is absent, nil, or the wrong type. `GetOpt[T]` returns
`(zero, false)` when the value is nil, which is what an `Optional()` declaration
delivers when nothing supplied a value. To ask whether the *invocation* caused a
value rather than the declaration, use `ctx.Provided("loud")`.

## Flags

strictcli provides 4 scalar flag constructors: `StringFlag`, `BoolFlag`,
`IntFlag`, and `FloatFlag`. The first 2 arguments (name and help) are always
required, and additional options like `Short()`, `Env()`, and `Choices()` are
passed as functional options.

Every flag must also apply **exactly one** of the three sibling presence
options -- `Required()`, `Optional()`, or `Default(v)` (see
[Presence](#presence-required-optional-or-a-default)). A flag that applies none
of them does not register, and neither does one that applies two.

### StringFlag

```go
strictcli.StringFlag("output", "Output file path", strictcli.Required())
strictcli.StringFlag("format", "Output format", strictcli.Default("json"))
strictcli.StringFlag("tag", "Release tag", strictcli.Optional())
strictcli.StringFlag("mode", "Run mode", strictcli.Required(),
    strictcli.Choices(strictcli.Ch("json", ""), strictcli.Ch("yaml", ""), strictcli.Ch("csv", "")))
```

`Required()` means some source -- a CLI token, a bound env var, a config entry,
or an `Implies` injection -- must supply a value. `Optional()` delivers `nil`
when nothing does.

### BoolFlag

```go
strictcli.BoolFlag("cache", "Reuse the build cache", strictcli.Default(true))
strictcli.BoolFlag("watch", "Watch for changes", strictcli.Required())
strictcli.BoolFlag("color", "Colorize output", strictcli.Optional())
```

Bool flags are negatable by default: `--cache` sets true, `--no-cache` sets
false. A `Required()` bool must be answered -- the user passes either `--flag` or
`--no-flag`. An `Optional()` bool is a real tri-state: `--flag` is true,
`--no-flag` is false, and absence arrives as `nil`.

Note that `verbose` and `quiet` are not available as flag names: they belong to
the [reserved quartet](#the-reserved-flag-quartet) and arrive on `ctx` instead.

### IntFlag

```go
strictcli.IntFlag("port", "Server port", strictcli.Default(8080))
strictcli.IntFlag("retries", "Number of retries", strictcli.Required())
```

Integers are parsed strictly: no leading/trailing whitespace, 64-bit signed bounds, no leading zeros.

### FloatFlag

```go
strictcli.FloatFlag("threshold", "Score threshold", strictcli.Default(0.5))
strictcli.FloatFlag("rate", "Rate limit", strictcli.Required())
```

Float parsing rejects NaN and Inf.

### Flag Options

Options are passed as functional option arguments after the name and help text.
Multiple options can be combined on a single flag to set defaults, short aliases,
environment variable bindings, and value restrictions. The table below lists all
available options:

```go
strictcli.StringFlag("output", "Output file path",
    strictcli.Default("out.json"),              // Presence: a declared default value
    strictcli.Short("o"),                       // -o shorthand
    strictcli.Env("MYTOOL_OUTPUT"),             // Read from env var
    strictcli.Choices(                          // Restrict to choices
        strictcli.Ch("out.json", "one JSON document"),
        strictcli.Ch("out.yaml", ""),
    ),
)
```

Available options:

| Option | Description |
|--------|-------------|
| `Required()` | Presence: a value must be supplied, from any source. One of the three; exactly one is mandatory. |
| `Optional()` | Presence: absence is legal and delivers `nil`. |
| `Default(v)` | Presence: the framework supplies `v` when nothing else does. `Default(nil)` is a registration error -- use `Optional()`. |
| `Short(s)` | Single-character short form (e.g., `Short("o")` for `-o`). |
| `Env(name)` | Environment variable to read from. Precedence: CLI > env > config > default. |
| `Choices(vals...)` | Restrict to specific values, one `Ch(<value>, "<help>")` record each. Not available on bool flags. |
| `RetiredChoices(vals...)` | The spellings this flag used to accept, one `RetiredChoice(<value>, "<message>")` record each, the message naming the replacement. Requires `Choices`. |
| `Prefixed(b)` | Whether env var prefix validation is applied (default: true). |
| `NegatableOpt(b)` | Override negation for bool flags (default: true for bool). |
| `ValidateFn(fn)` | Custom validation function. Runs on supplied values only -- never on a declared `Default(v)`. |
| `Repeatable()` | Accept multiple occurrences, collecting into a list. |
| `Unique(b)` | Reject (or allow) duplicate values on a repeatable flag. Mandatory when `Repeatable()` is applied. |
| `EnvSeparator(s)` | Character splitting an env var value into elements of a repeatable flag. |

### Retired choices

A flag or arg that declares `Choices` may also declare the spellings it **used
to** accept, each carrying the message that names its replacement. A value
matching one is refused at parse time, ahead of the invalid-value check:

```go
strictcli.StringFlag("format", "Output format",
    strictcli.Choices(strictcli.Ch("text", ""), strictcli.Ch("json", "")),
    strictcli.RetiredChoices(
        strictcli.RetiredChoice("xml", "use 'json' (XML output was dropped)"),
    ),
    strictcli.Required(),
)
```

```
error: --format: value 'xml' retired: use 'json' (XML output was dropped)
```

Positional args take the same records through `ArgRetiredChoices(...)`.

A retired value is not a choice: help never lists it, and the `value_schema`
enum `--dump-schema` publishes -- together with the MCP tool schema derived
from it -- carries the live values only. `--dump-schema` publishes the
declaration separately, as a `retired_choices` map from spelling to message,
sorted ascending by key and omitted when nothing is retired.

A retired spelling may not also be a live choice, may not repeat, may not carry
an empty message, may not be what a `Default(v)` names, and may not be declared
without `Choices`; each is a registration-time hard error. Retired choices are
incompatible with bool, like `Choices` itself. Full rules:
[Retired choices](flag-system.md#retired-choices).

### Presence: required, optional, or a default

Every flag and every positional argument declares **exactly one** of three facts
about itself. Nothing is inferred from the shape of another declaration:

| Fact | Flag option | Arg option | The handler receives |
|------|-------------|-----------|----------------------|
| **required** | `Required()` | `ArgRequired()` | the supplied value; the parse fails if nothing supplies one |
| **optional** | `Optional()` | `ArgOptional()` | the supplied value, or `nil` when nothing supplied one |
| **default** | `Default(v)` | `ArgDefault(v)` | the supplied value, or the declared default |

Declaring none of the three, or more than one, panics at registration:

```
Flag "output": presence is undeclared: declare exactly one of Required(), Optional(), or Default(<value>)
Flag "output": presence is declared twice: Required() and Default(out.json) cannot be combined; declare exactly one
```

A `Flag` **struct literal** that never passes through these option constructors
declares no presence and therefore does not register. That closes a trap the
struct literal used to carry: an exported `Default` field set directly on a
literal left the flag's internal `hasDefault` false and was silently ignored at
parse time. Now the flag does not register at all, so the value cannot be
dropped silently.

### `Default(nil)` is a registration error

`Default(nil)` used to be how a Go flag said "optional with no value". It is now
refused, because optionality has exactly one spelling and the value-shaped one
is not it:

```
Flag "config-path": Default(nil) does not declare optionality: use Optional() (it delivers nil when the flag is absent)
```

Write `Optional()` instead. It delivers exactly the `nil` the old spelling did:

```go
strictcli.StringFlag("config-path", "Override config location", strictcli.Optional())
```

In help output this renders `[optional]`. The same redirect applies to args, as
`ArgDefault(nil)` pointing at `ArgOptional()`.

### Presence in help output

Every flag and every arg renders exactly one presence part, and it is the last
bracketed part of the line:

| Declared | Rendered |
|----------|----------|
| `Required()` | `[required]` |
| `Optional()` | `[optional]` |
| `Default(v)` | `[default: v]` |

A declared empty collection renders `[default: []]` / `[default: {}]`, a
required positional renders `[required]`, and a `RelativeToRoot` default renders
as the declaration that produced it -- `[default: RelativeToRoot('MYTOOL_HOME', 'store')]` --
never as the resolved machine-specific path.

### Repeatable flags declare presence too

There is no silent empty-list default. A repeatable flag declares which of the
three it is, like anything else:

```go
// An empty list when nothing is passed -- declared, not assumed
strictcli.StringFlag("tag", "Tags to apply",
    strictcli.Repeatable(), strictcli.Unique(false), strictcli.Default([]interface{}{}))

// Absent when nothing is passed: the handler receives nil, not an empty slice
strictcli.StringFlag("only", "Restrict to these",
    strictcli.Repeatable(), strictcli.Unique(false), strictcli.Optional())

// At least one occurrence must arrive from some source
strictcli.StringFlag("host", "Hosts to contact",
    strictcli.Repeatable(), strictcli.Unique(true), strictcli.Required())
```

### `ctx.Provided` -- was this supplied?

`ctx.Provided(name)` answers whether the **invocation** caused a value, so no
handler reconstructs that boolean out of a sentinel:

| Source | `Provided` |
|--------|-----------|
| `cli`, `env`, `config`, `implied` | **true** -- the invocation caused the value |
| `default`, `infra` | **false** -- the declaration caused it |

An `Optional()` flag that received nothing carries source `default` and reports
false. An unknown name panics exactly as `ctx.Source` does. The same predicate
decides engagement for every [constraint](#constraints), so a declared
default never engages a member. Inside a choice flag's scope the answer
depends on the door a value arrived through: a scoped field the caller supplied
answers true from the command line and from the flat machine door, and false from
the record door, where a constructed `Elect(...)` has already filled its declared
defaults and the framework refuses to guess which fields the caller wrote (see
[the door table](flag-system.md#was-this-supplied-ctxprovided)).

## Positional Arguments

Use `NewArg` to declare positional arguments passed via `WithArgs()`. Arguments
are consumed in declaration order after all flags have been parsed, and each one
applies **exactly one** presence option -- `ArgRequired()`, `ArgOptional()`, or
`ArgDefault(v)`. There is no `ArgRequired(bool)`: it took a boolean, which made
a required arg the implicit default, and spelled one fact across two options.

```go
app.Command("show", "Show what is deployed to an environment", handler,
    strictcli.WithEffect(strictcli.EffectReadOnly),
    strictcli.WithArgs(
        strictcli.NewArg("environment", "Target environment", strictcli.ArgRequired()),
        strictcli.NewArg("version", "Version to inspect", strictcli.ArgDefault("latest")),
        strictcli.NewArg("note", "An optional note", strictcli.ArgOptional()),
    ),
)
```

An optional arg delivers absence as a **present kwargs key** holding `nil`,
exactly as an optional flag does -- the key is never omitted.

A positional arg is reached by the
[mutating-default ban](flag-system.md#a-mutating-command-may-not-default-a-value)
exactly as a flag is, which is why the command above is `EffectReadOnly`: on a
mutating command, `ArgDefault("latest")` is a registration-time panic and the arg
declares `ArgRequired()` or `ArgOptional()` instead.

Arg options:

| Option | Description |
|--------|-------------|
| `ArgRequired()` | Presence: a value must be supplied. One of the three; exactly one is mandatory. |
| `ArgOptional()` | Presence: absence is legal and delivers `nil`. |
| `ArgDefault(v)` | Presence: the framework supplies `v` when the arg is absent. `ArgDefault(nil)` is a registration error -- use `ArgOptional()`. |
| `ArgType(t)` | Type (default: `TypeStr`). Accepts `TypeStr`, `TypeBool`, `TypeInt`, `TypeFloat`. |
| `ArgChoices(vals...)` | Restrict to specific values, one `Ch(<value>, "<help>")` record each. |
| `ArgRetiredChoices(vals...)` | The spellings this arg used to accept, one `RetiredChoice(<value>, "<message>")` record each, the message naming the replacement. Requires `ArgChoices`. |
| `Variadic()` | Collect all remaining positional values (must be the last arg). |

### Variadic Arguments

A variadic argument collects all remaining positional values into a slice. It
must be the last argument in the command's declaration, and only one variadic
argument is allowed per command. Because it always delivers a list,
`ArgRequired()` means *at least one value* and `ArgOptional()` means *possibly
none*.

```go
app.Command("process", "Process files", handler,
    strictcli.WithEffect(strictcli.EffectReadOnly),
    strictcli.WithArgs(
        strictcli.NewArg("files", "Files to process", strictcli.Variadic(), strictcli.ArgRequired()),
    ),
)
```

A default on a variadic arg panics at registration -- the empty case is spelled
once, as `ArgOptional()`:

```
Arg "files": a variadic arg cannot declare ArgDefault(): it always delivers a list, so declare ArgRequired() for at least one value or ArgOptional() for possibly none
```

Only one variadic argument is allowed, and it must be the last.

## Global Flags

Global flags are available to all commands and can appear before or after the
command name in argv. They are parsed during the global flag parsing stage,
before the command is resolved. Global flag names cannot collide with reserved
framework names like `help`, `version`, `dump-schema`, `mcp`, `config`, or
`hermetic`, nor with the reserved quartet.

```go
app := strictcli.NewApp("mytool", "0.1.0", "A useful tool")
app.GlobalFlag(strictcli.BoolFlag("color", "Colorize output", strictcli.Default(true)))
app.GlobalFlag(strictcli.StringFlag("log-level", "Log level", strictcli.Default("info"),
    strictcli.Choices(strictcli.Ch("debug", ""), strictcli.Ch("info", ""),
        strictcli.Ch("warn", ""), strictcli.Ch("error", ""))))

app.Command("deploy", "Deploy the app", func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
    color := strictcli.Get[bool](kwargs, "color")
    if !color {
        ctx.Info("Color disabled")
    }
    return strictcli.Exit(0)
}, strictcli.WithEffect(strictcli.EffectMutating))
```

Usage: `mytool --no-color deploy` or `mytool deploy --no-color` (global flags can appear before or after the command).

Reserved global flag names that cannot be used: `help`, `h`, `version`, `v`, `dump-schema`, `mcp`, `config`, `hermetic`, plus the reserved quartet `dry-run`, `approve-consequential`, `quiet`, `verbose`, plus `json`, which selects machine mode. The name `yes` is banned outright -- the confirmation skip is `--approve-consequential`.

## Command Groups

Groups organize commands into namespaces, creating a hierarchical command
structure like `mytool dns zone list`. Groups can nest to arbitrary depth, and
each group requires a name and help text. When a group is reached without a
subcommand, the group's help text is displayed.

```go
app := strictcli.NewApp("mytool", "0.1.0", "Infrastructure tool")

dns := app.Group("dns", "DNS management")
dns.Command("list", "List DNS records", listHandler,
    strictcli.WithEffect(strictcli.EffectReadOnly))
dns.Command("create", "Create a DNS record", createHandler,
    strictcli.WithEffect(strictcli.EffectMutating))

zone := dns.Group("zone", "Zone management")
zone.Command("list", "List zones", zoneListHandler,
    strictcli.WithEffect(strictcli.EffectReadOnly))
zone.Command("delete", "Delete a zone", zoneDeleteHandler,
    strictcli.WithEffect(strictcli.EffectMutating), strictcli.WithConsequential())
```

Usage:

```
$ mytool dns list
$ mytool dns create --name example.com
$ mytool dns zone list
$ mytool dns zone delete --name example.com
```

## Flag Naming Conventions

strictcli enforces strict flag naming rules at registration time. Violations
cause panics because they are programmer errors that should be caught during
development, not runtime errors that users encounter. These rules prevent
ambiguous flag names and ensure the `--no-` negation namespace remains
uncontaminated.

### Bare `--force` is banned

The flag name cannot be exactly `"force"` because a generic force flag lets
automation bypass guardrails without specifying what is being forced. Use a
qualified name that describes the specific action being forced, making the
intent explicit and auditable:

```go
// This panics:
strictcli.BoolFlag("force", "Force the operation", strictcli.Default(false))

// Use a qualified name instead:
strictcli.BoolFlag("force-overwrite", "Overwrite existing files", strictcli.Default(false))
strictcli.BoolFlag("force-delete", "Delete without confirmation", strictcli.Default(false))
```

### `--no-*` prefix is reserved

Flag names cannot start with `no-` because the `--no-` prefix is auto-generated
by the negation system for boolean flags. Allowing user-defined flags in this
namespace would create double-negation ambiguity where `--no-no-cache` becomes
the negation form of a flag named `no-cache`:

```go
// This panics:
strictcli.BoolFlag("no-cache", "Disable caching", strictcli.Default(false))

// Use a positive name instead:
strictcli.BoolFlag("cache", "Enable caching", strictcli.Default(true))
// Users pass --no-cache to disable
```

### The reserved flag quartet

Four flag names are owned by the framework and cannot be declared at any level --
not as app global flags, not as command flags, not inside a flag set, and not
inside a choice's scope at any depth:

| Flag | Delivered as | Meaning |
|------|-------------|---------|
| `--dry-run` | `ctx.DryRun()` | Record effects instead of performing them, then print the would-do log |
| `--approve-consequential` | `ctx.ApproveConsequential()` | Answer the confirm prompt in advance |
| `--quiet` | `ctx.Quiet()` | Suppress `ctx.Info` output; warnings and errors still print |
| `--verbose` | `ctx.Verbose()` | Enable `ctx.Debug` output |

```go
// Every one of these panics:
strictcli.BoolFlag("dry-run", "Simulate the run", strictcli.Default(false))
strictcli.BoolFlag("verbose", "Be verbose", strictcli.Default(false))
strictcli.BoolFlag("quiet", "Be quiet", strictcli.Default(false))
```

The panic message is `flag name 'dry-run' is reserved by the framework
(dry-run, approve-consequential, quiet, verbose)`. The name `yes` is banned
outright with its own message pointing at `--approve-consequential`, so that a
private `--yes` cannot restate the confirmation skip in a different spelling.

All four are recognized anywhere in argv: `mytool deploy --dry-run` and
`mytool --dry-run deploy` are equivalent. Two boundaries stop the scan -- a bare
`--` (everything after it is data) and a passthrough command's name (its args
are forwarded to the child byte-for-byte).

### Refusing `--dry-run` with `WithDryRunUnsupported`

`--dry-run` works on every mutating command by default: its effects are recorded
rather than performed. Some commands cannot honor that honestly -- their effects
escape the effects handle, or their later steps read state that their earlier
(recorded, therefore un-performed) steps would have written. Such a command
declares the refusal with a mandatory reason:

```go
app.Command("migrate", "Run pending database migrations", migrateHandler,
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithDryRunUnsupported(
        "each migration reads the schema the previous one wrote, "+
            "so a recorded run would report the wrong pending set"),
)
```

`--dry-run` is then refused at parse time rather than rendering a preview that
would lie:

```
$ mytool migrate --dry-run
error: --dry-run is not supported by command 'migrate': each migration reads the schema the previous one wrote, so a recorded run would report the wrong pending set
```

Two guardrails apply at registration time:

- `WithDryRunUnsupported` on a `read_only` command panics -- a command that changes nothing has no effects a preview could misrepresent.
- An empty reason panics -- say what a preview cannot honestly show.

The reason also appears in the command's help under a `Dry run:` section, and in
`--dump-schema` output as the pair `dry_run_supported` / `dry_run_unsupported_reason`.
Both keys are emitted only when declared, so a schema entry without them means
dry run is supported. `--help` always beats the refusal: asking what a command
does is never answered with a refusal to preview it.

## Consequential Commands and the Confirm Protocol

Classification says whether a dry run should record rather than perform.
`WithConsequential()` says something different: that these effects are worth
interrupting a human for. It is the **only** thing that makes the framework
prompt -- a plain mutating command never does.

```go
app.Command("destroy", "Destroy the cluster", destroyHandler,
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithConsequential(),
    strictcli.WithArgs(strictcli.NewArg("cluster", "Cluster to destroy", strictcli.ArgRequired())),
)
```

Before dispatching, the framework prints the prompt to stderr and reads one line
from stdin:

```
$ mytool destroy prod
about to run consequential command 'destroy'. Proceed? [y/N]
```

Only `y` or `Y` proceeds. Anything else prints `aborted` to stderr and exits 1.

Two things skip the prompt, and neither disables anything else:

- `--approve-consequential` -- the operator answered in advance. This is what automation and CI pass.
- `--dry-run` -- nothing is being performed, so there is nothing to confirm.

When stdin is not a TTY and neither flag was passed, the framework refuses
rather than hanging or silently proceeding:

```
$ mytool destroy prod < /dev/null
error: stdin is not interactive; a consequential command must be confirmed at a terminal
```

The prompt never fires on the programmatic paths, which have no TTY contract.
`app.Test()` behaves as if `--approve-consequential` were passed; `app.Call()`
and the MCP server take the consent from the call instead and refuse a
consequential command without it:

```go
// Refused: command 'destroy' is consequential: the call must carry confirmation
_, err := app.Call("destroy", map[string]interface{}{"env": "prod"})

// Proceeds
_, err = app.Call("destroy", map[string]interface{}{"env": "prod"},
    strictcli.WithApproveConsequential())
```

Over MCP the same consent is a top-level `tools/call` param, a sibling of
`name` and `arguments`, never a member of `arguments`:

```json
{"jsonrpc":"2.0","id":1,"method":"tools/call",
 "params":{"name":"destroy","arguments":{"env":"prod"},"approve_consequential":true}}
```

This is not human approval and is not meant to be: it makes the caller state,
in the call, that it is proceeding without a human.
Over the current protocol revision the server does not have to take the
caller's word for it. A `tools/call` on a consequential command from a client
that declared elicitation support is answered with a confirmation request and
an opaque `requestState`; the client puts the question to a human, and the
retry that echoes the state back with an acceptance is what consents. The state
is integrity-protected, bound to that client and that exact request, expires in
five minutes and cannot be redeemed twice. The server declares the feature by
name (`dev.smmh.strictcli/consequential-confirmation`) in its `server/discover`
result. A client that did not declare elicitation is answered `-32021` naming the
capability it would need, and a client that opened with the `initialize`
handshake is asked over that era's server-initiated `elicitation/create` instead.
[Consequential confirmation over MCP](mcp-confirmation.md) has the full dialogue.

Tool descriptors and MCP
`tools/list` publish `Effect` and `Consequential` beside the argument schema so
a caller can see the requirement before it calls. There is no bypass flag:
`--approve-consequential` answers the prompt and does nothing else. A read-only
command cannot be declared consequential -- a command that changes nothing has
nothing to confirm -- and trying panics at registration time.

A consequential passthrough command is not exempt. The framework knows *less*
about what is about to happen there, not more.

### Help text is mandatory

Every flag, arg, command, group, and app must have non-empty help text. Missing
or empty help is a registration-time panic with no opt-out. This ensures that
every strictcli application is self-documenting from the first line of code, and
users always have access to meaningful help for every flag and command.

## Choice Flags

A **choice flag** elects exactly one of its declared choices per invocation, and
each choice declares a **scope**: the flags that exist only while that choice is
elected. It is the framework's one construct for "exactly one of these", and
there is no at-most-one construct anywhere -- an absent selection is a choice
nobody named, so the answer is to name it.

`Choice(name, help, flags...)` returns a value with **identity**, referenced by
both the declaration and every handler that switches on it. That is the
package-level-token idiom Go already uses, extended to something that carries a
payload: a typo does not compile, and renaming a choice breaks every site that
names it.

```go
var (
    ViaEmail = strictcli.Choice("email", "deliver the notification as an email message",
        strictcli.StringFlag("subject", "subject line of the message", strictcli.Required()),
        strictcli.StringFlag("recipient", "destination email address", strictcli.Required()),
    )
    ViaSMS = strictcli.Choice("sms", "deliver the notification as a text message",
        strictcli.StringFlag("phone-number", "destination number in E.164 form", strictcli.Required()),
    )
    ViaWebhook = strictcli.Choice("webhook", "post the notification to a URL",
        strictcli.StringFlag("url", "endpoint to post to", strictcli.Required()),
        strictcli.IntFlag("retries", "delivery attempts before giving up; 3 when omitted",
            strictcli.Optional()),
    )
)

app.Command("send", "Send one notification through exactly one channel",
    func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
        via := strictcli.GetElected(kwargs, "via")
        line := strictcli.Match(via,
            strictcli.When(ViaEmail, func(f strictcli.Fields) string {
                return "emailing " + strictcli.Get[string](f, "recipient") +
                    ": " + strictcli.Get[string](f, "subject")
            }),
            strictcli.When(ViaSMS, func(f strictcli.Fields) string {
                return "texting " + strictcli.Get[string](f, "phone_number")
            }),
            strictcli.When(ViaWebhook, func(f strictcli.Fields) string {
                retries, provided := strictcli.GetOpt[int](f, "retries")
                if !provided {
                    retries = 3
                }
                return fmt.Sprintf("posting to %s (%d retries)",
                    strictcli.Get[string](f, "url"), retries)
            }),
        )
        ctx.Info(line)
        return strictcli.Exit(0)
    },
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithFlags(strictcli.ChoiceFlag("via", "Delivery channel",
        strictcli.Required(), strictcli.Short("v"), ViaEmail, ViaSMS, ViaWebhook)),
)
```

The command's help renders the scope tree, and every line -- scoped or not --
ends with exactly one presence part:

```
$ notify send --help
notify send -- Send one notification through exactly one channel

Flags:
  --via, -v <choice>          Delivery channel [required]
    email                     deliver the notification as an email message
      --subject <str>         subject line of the message [required]
      --recipient <str>       destination email address [required]
    sms                       deliver the notification as a text message
      --phone-number <str>    destination number in E.164 form [required]
    webhook                   post the notification to a URL
      --url <str>             endpoint to post to [required]
      --retries <int>         delivery attempts before giving up [default: 3]
```

A flag supplied outside its elected scope is a distinct parse error naming both
sides -- never "unknown flag":

```
$ notify send --via sms --subject hi
error: flag '--subject' is only valid under '--via email', but '--via sms' was elected
try 'notify send --help'

$ notify send --subject hi
error: flag '--subject' is only valid under '--via email', but '--via' was not provided
try 'notify send --help'

$ notify send --via email
error: flag '--subject' is required under '--via email'
try 'notify send --help'
```

Order is irrelevant -- nothing is interpreted until every token is collected --
and errors are reported in a fixed order: **election, then scope, then value,
then presence**. `--via sms --subject hi` reports the spelling mistake, never
its consequence.

### Delivery: `GetElected`, `Match` and `When`

The handler receives an `*Elected` -- the elected `*ChoiceDecl` plus its
`Fields` -- under the choice flag's own key. `Match` is **exhaustive against the
declaration**: it compares the cases to the choice flag's own choice list and
panics naming what is missing.

```
strictcli.Match: choice flag "via" has no case for webhook
```

Go has no sealed union, so the check runs at dispatch rather than at compile
time. It cannot be defeated by a typo -- the cases are references, not strings --
and it cannot go stale, because adding a choice breaks every `Match` that omits
it on the first call. `e.Is(ViaEmail)` is the single-case form, and
`e.Provided("subject")` answers whether the invocation caused a field's value.

Scoped flags are **never** top-level `kwargs` entries, at any depth: the only key
a choice flag adds is its own, so every declared top-level key is still always
present. Inside the scope, field names are the underscored parameter names --
`--phone-number` reads back as `Get[string](f, "phone_number")`.

### Member spelling

`MemberChoiceFlag(name, help, opts...)` with `MemberChoice(memberFlag, help, scope...)`
is the member-spelled twin: each choice is its own flag, and the choice flag's
own name is never typed -- it is the `kwargs` key and the noun help and errors
use. They are twin **constructors** rather than an option, so the spelling is one
declaration instead of two that must agree.

```go
var (
    OneProfile = strictcli.MemberChoice(
        strictcli.StringFlag("profile", "use the named profile", strictcli.Required()),
        "use the named profile",
        strictcli.BoolFlag("create-missing", "create the profile if it does not exist",
            strictcli.Optional()),
    )
    EveryProfile = strictcli.MemberChoice(
        strictcli.BoolFlag("all-profiles", "apply to every profile", strictcli.Required()),
        "apply to every profile",
    )
)

app.Command("sync", "Synchronize profiles",
    func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
        scope := strictcli.GetElected(kwargs, "scope")
        ctx.Info(strictcli.Match(scope,
            strictcli.When(OneProfile, func(f strictcli.Fields) string {
                create, _ := strictcli.GetOpt[bool](f, "create_missing")
                return fmt.Sprintf("syncing %s (create=%v)",
                    strictcli.Get[string](f, "value"), create)
            }),
            strictcli.When(EveryProfile, func(f strictcli.Fields) string {
                return "syncing every profile"
            }),
        ))
        return strictcli.Exit(0)
    },
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithFlags(strictcli.MemberChoiceFlag("scope", "What to synchronize",
        strictcli.Required(), OneProfile, EveryProfile)),
)
```

```
$ myapp sync --help
myapp sync -- Synchronize profiles

Flags:
  scope                                        What to synchronize (exactly one of the following) [required]
    --profile <str>                            use the named profile [required]
      --create-missing, --no-create-missing    create the profile if it does not exist [optional]
    --all-profiles                             apply to every profile [required]

$ myapp sync --profile work --create-missing
syncing work (create=true)

$ myapp sync --all-profiles --create-missing
error: flag '--create-missing' is only valid under '--profile', but '--all-profiles' was elected

$ myapp sync --profile work --all-profiles
error: --profile and --all-profiles are mutually exclusive

$ myapp sync
error: one of --profile, --all-profiles is required
```

A member's payload is delivered under the reserved name `value`. A bool member
is elected by `--<name>` and only when it resolves to **true**; `--no-<name>`
*declines* -- it says "not this one" and elects nothing, and combining a decline
with a real election is a parse error. Member election is **command-line only**:
env and config are not consulted for a member at all. A member-spelled choice
flag cannot carry a short, since it is never typed, and a short declared on a
member is an ordinary flag short.

Because the two constructors are twins over one `FlagOption` interface, mixing
them is refused by name:

```
Choice "email" of "via": a member-spelled choice flag declares its choices with MemberChoice(...), which names the flag that elects the choice
```

### Presence, defaults, and recursion

A choice flag declares `Required()` or `Default(<choice name>)`; `Optional()` is
a registration error:

```
Flag "via": a choice flag cannot declare Optional(): an absent selection is a choice nobody named, so name it as a choice of its own
```

A member flag **must** declare `Required()`, read as *required once this member
is elected* -- the inverse of the rule the retired mutex group carried:

```
Choice "profile" of "scope": a member flag must declare Required(), read as required once this member is elected
```

A default names a choice, and a registration check refuses one whose scope
declares a required sub-flag, because a defaulted selection is a **complete**
elected value:

```
Flag "via": Default("email") elects choice "email", whose scope declares the required flag '--subject': a defaulted selection must be complete with nothing typed
```

A defaulted choice flag renders its complete elected value:

```
  --via <choice>              Delivery channel [default: sms (phone-number=+15550100)]
```

Electing a choice on the command line never borrows the default's values. A
choice flag is a flag, so a choice flag may be declared inside a choice's scope,
to unlimited depth.
## Constraints

Declare rules over a command's flags and args using `WithConstraints`. Four
kinds are available, each a constructor returning a `Constraint` and each taking
a **mandatory name** as its first parameter:

| Constructor | Behavior |
|-------------|----------|
| `AtLeastOne(name, a, b, rest...)` | At least one member is engaged. Members may co-occur -- it has no upper bound and is never exclusivity. |
| `AllOrNone(name, a, b, rest...)` | Either every member is engaged or none is. Nothing engaged is vacuously satisfied. |
| `Requires(name, flag, dependsOn)` | If `flag` is provided, `dependsOn` must also be provided. |
| `Implies(name, flag, implies, value)` | When `flag` is provided, automatically set `implies` to `value`. |

The name is what a violation prints, what `--help` shows, and what lets one
constraint be a member of another. The four constructors are the only things
that mint a `Constraint`: the interface is closed over a type the package does
not export, so a struct literal cannot declare a constraint and therefore cannot
declare a half-formed one.

```go
app.Command("deploy", "Deploy the app", handler,
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithFlags(
        strictcli.StringFlag("target", "Deploy target", strictcli.Optional()),
        strictcli.StringFlag("region", "Target region", strictcli.Optional()),
        strictcli.BoolFlag("staged", "Roll out in stages", strictcli.Optional()),
        strictcli.IntFlag("batch-size", "Instances per batch", strictcli.Optional()),
        strictcli.BoolFlag("wait", "Block until the rollout settles", strictcli.Optional()),
    ),
    strictcli.WithConstraints(
        // --target and --region must both appear or neither
        strictcli.AllOrNone("deploy-destination",
            strictcli.Member("target"), strictcli.Member("region")),
        // at least one rollout control has to be chosen
        strictcli.AtLeastOne("rollout-mode",
            strictcli.Member("staged", strictcli.WhenTrue()),
            strictcli.Member("batch-size")),
        // --staged implies --wait=true
        strictcli.Implies("staged-waits", "staged", "wait", true),
    ),
)
```

### Members

`Member(name, opts...)` references, by name, a command flag, a positional arg,
or another named `AtLeastOne` / `AllOrNone` of the same command; nesting is a
cycle-checked DAG at unlimited depth. A member carries no presence and no help
of its own -- the declaration it names carries every fact about the value.

The two co-occurrence constructors take **two named members before the variadic
tail**, so a one-member constraint is a *compile* error. A caller holding a
slice writes `AllOrNone(n, ms[0], ms[1], ms[2:]...)`, which fails at the caller
rather than inside the framework.

`Requires` and `Implies` are narrower on purpose: their operands are **flags
only**, by name, and they take no election selector.

Election selectors are functional options, matching every other option surface
in the package:

| Option | Engaged when | Legal on |
|---|---|---|
| `WhenPresent()` (the default) | the value was provided -- `cli`, `env`, `config` or `implied` | every type |
| `WhenTrue()` | provided **and** the value is `true` | `bool` only |
| `WhenNonEmpty()` | provided **and** the value is a non-empty string, list or map | `str`, list and dict flags, and variadic args |

**A bool member must declare its election explicitly** -- omitting it is a
registration error. Without that rule `--no-staged` would engage a constraint
while selecting nothing. `WhenTrue()` on a non-bool, or `WhenNonEmpty()` on a
bool, an int or a float, is a registration error too.

Nesting is what expresses "either identity pair, but never half of one":

```go
strictcli.WithFlags(
    strictcli.StringFlag("old-name", "Current display name", strictcli.Optional()),
    strictcli.StringFlag("new-name", "New display name", strictcli.Optional()),
    strictcli.StringFlag("old-email", "Current email address", strictcli.Optional()),
    strictcli.StringFlag("new-email", "New email address", strictcli.Optional()),
),
strictcli.WithConstraints(
    strictcli.AllOrNone("author-name",
        strictcli.Member("old-name"), strictcli.Member("new-name")),
    strictcli.AllOrNone("author-email",
        strictcli.Member("old-email"), strictcli.Member("new-email")),
    strictcli.AtLeastOne("author-change",
        strictcli.Member("author-name"), strictcli.Member("author-email")),
),
```

```
Constraints:
  author-name      all or none of --old-name, --new-name
  author-email     all or none of --old-email, --new-email
  author-change    at least one of (--old-name with --new-name), (--old-email with --new-email)
```

Children are evaluated before parents, so an operator who typed one half of a
pair reads `constraint "author-name": --old-name, --new-name must be used
together` rather than a complaint about the whole selection. A member list
mixing kinds renders each by its own spelling -- `--older-than` for a flag,
bare `targets` for a positional arg:

```
constraint "purge-selection": at least one of targets, --older-than, --larger-than, --all is required
```

No member of a co-occurrence constraint may declare `Required()` -- a member the
invocation must always supply leaves the constraint nothing to decide. A
`Default(v)` is legal and never engages the constraint on its own; `Optional()`
is the ordinary case, and membership neither makes a flag required nor exempts
it from being required.

Constraints cannot reference the reserved quartet: `dry-run` is not a flag you
declare, so it can never be a member, a `Requires` target or an `Implies`
subject. They operate at root scope only -- naming a flag declared inside a
choice's scope is a registration error, because the scope already *is* the
constraint.

Engagement reads presence through the same predicate `ctx.Provided` uses -- a
flag counts as provided when the invocation caused its value. A declared default
never engages a member, never satisfies a `Requires`, and never fires an
`Implies` trigger. That includes a `RelativeToRoot` default with the `infra`
source label: it is still the declaration deciding. Every constraint renders in
`--help` under a `Constraints:` section, publishes its members in
`--dump-schema`, and projects into MCP tool schemas (`anyOf` /
`dependentRequired`) with anything a JSON Schema keyword cannot carry stated in
the tool description instead.

## Update Commands

A command that changes some properties of one resource and leaves the rest alone
declares what it updates with `WithUpdateOf(...)`. The write mode is a
**positional parameter**, which is Go's spelling of mandatory: there is no option
to forget and no zero-valued struct field to fill in silently.

```go
app.Command("update-record", "change one DNS record in place",
    func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
        zone := strictcli.Get[string](kwargs, "zone")
        recordID := strictcli.Get[string](kwargs, "record_id")
        body := map[string]interface{}{}
        if ctx.Provided("content") {
            body["content"] = kwargs["content"]
        }
        if ctx.Unset("ttl") {
            body["ttl"] = nil
        } else if ctx.Provided("ttl") {
            body["ttl"] = kwargs["ttl"]
        }
        _, _ = ctx.Effects().HTTP("PATCH",
            fmt.Sprintf("https://api.example.com/zones/%s/dns_records/%s", zone, recordID))
        return strictcli.Exit(0)
    },
    strictcli.WithEffect(strictcli.EffectMutating),
    strictcli.WithUpdateOf("dns-record", strictcli.WriteSparse,
        strictcli.Identity("zone", "record-id"),
        strictcli.Properties("content", "ttl", "proxied"),
    ),
    strictcli.WithFlags(
        strictcli.StringFlag("zone", "zone the record belongs to", strictcli.Required()),
        strictcli.StringFlag("record-id", "identifier of the record to change", strictcli.Required()),
        strictcli.StringFlag("content", "record content", strictcli.Optional()),
        strictcli.IntFlag("ttl", "time to live in seconds", strictcli.Optional(), strictcli.Nullable()),
        strictcli.BoolFlag("proxied", "whether the record is proxied", strictcli.Optional()),
    ),
)
```

The surface:

```go
type WriteMode string

const (
    WriteSparse      WriteMode = "sparse"
    WriteFullReplace WriteMode = "full_replace"
)

func WithUpdateOf(resource string, mode WriteMode, opts ...UpdateOption) CmdOption
func Identity(names ...string) UpdateOption
func Properties(first string, rest ...string) UpdateOption
func Nullable() FlagOption
```

`Properties(first string, rest ...string)` puts a **compile-time floor of one** on
the property list -- the two-named-plus-variadic idiom `AllOrNone` and
`AtLeastOne` already use, at the arity this construct needs. `WriteMode` is
string-based so its zero value renders `""` in the invalid-mode panic and the
sentence stays byte-identical with its siblings'. `Nullable()` is an ordinary
`FlagOption` beside `Required()` / `Optional()` / `Default(v)`.

A **property applies `Optional()` and nothing else**: absence *is* untouched.
`Required()` on a property is a registration panic, and `Default(v)` is refused
twice over -- an update command is always mutating, so
[the ban](flag-system.md#a-mutating-command-may-not-default-a-value) reaches it
first. An **identity member** may be `Required()` or `Optional()`, and may be a
positional arg where a property may not.

The framework refuses an invocation that supplies no property, naming every
declared one, and a nullable property mints `--unset-<prop>`, answered by
`ctx.Unset(name)`:

```
$ mytool update-record --zone z1 --record-id r7
error: update "dns-record": at least one property is required: --content, --ttl, --proxied
try 'mytool update-record --help'

$ mytool update-record --help
mytool update-record -- change one DNS record in place

Flags:
  --zone <str>                zone the record belongs to [required]
  --record-id <str>           identifier of the record to change [required]
  --content <str>             record content [optional]
  --ttl <int>, --unset-ttl    time to live in seconds [optional]
  --proxied, --no-proxied     whether the record is proxied [optional]
```

Inside an update command `--no-proxied` **writes false** -- stating false is
stating a value -- and every run that reports what it does renders the write set.
In dry mode it is one unnumbered line before the first effect; under `--json` it
is the envelope's `writes` member, in both modes:

```
$ mytool --dry-run update-record --zone z1 --record-id r7 --content hi --unset-ttl
DRY RUN — no changes were made. Would do:
  writes: content; clears: ttl (other properties unchanged)
  1. net: PATCH https://api.example.com/zones/z1/dns_records/r7
```

See [Update commands](flag-system.md#update-commands) for the full rules: the
write set's two renderings, the clear vocabulary at every door, and the schema
and MCP projections.

## WithConfig -- Config File Support

`WithConfig()` enables automatic config file loading from the XDG config
directory and registers five `config` subcommands (`show`, `set`, `path`,
`edit`, `init`) for managing the configuration file. Config values participate
in the flag resolution cascade between env vars and defaults, with precedence
CLI > env > config > default.

```go
app := strictcli.NewApp("mytool", "0.1.0", "A configurable tool",
    strictcli.WithConfig(),
    strictcli.WithEnvPrefix("MYTOOL"),
)

app.Command("run", "Run the tool", func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
    port := strictcli.Get[int](kwargs, "port")
    ctx.Info(fmt.Sprintf("Listening on port %d", port))
    return strictcli.Exit(0)
}, strictcli.WithEffect(strictcli.EffectReadOnly), strictcli.WithFlags(
    strictcli.IntFlag("port", "Server port", strictcli.Default(8080), strictcli.Env("MYTOOL_PORT")),
))
```

Config files live at `~/.config/mytool/config.json` by default. Value precedence: CLI > env > config > default.

### Config format

Default format is JSON. Use TOML with `WithConfigFormat("toml")` for
human-editable configuration files with comments. TOML parsing is strict and
rejects TOML-1.1-only constructs to maintain parity with the Python and Go
parsers:

```go
app := strictcli.NewApp("mytool", "0.1.0", "A configurable tool",
    strictcli.WithConfig(),
    strictcli.WithConfigFormat("toml"),
)
```

### Auto-registered config commands

When config is enabled, these five subcommands are registered automatically
under a `config` group. They provide a complete config management interface
without writing any additional code, covering display, modification, path
inspection, editor integration, and initialization of the config file with
default values:

- `mytool config show` -- display current config with value sources
- `mytool config set <key> --value <v>` -- write a value at a key
- `mytool config path` -- print the config file path
- `mytool config edit` -- open the config file in `$EDITOR`
- `mytool config init` -- create the config file with defaults

`config set` takes its write under a **required, member-spelled selector** named
`write`, over exactly three choices -- a value, a clear, and a reset to the
declared default:

```
Flags:
  write              What to write at the key: a value, a clear, or a reset to the declared default (exactly one of the following) [required]
    --value <str>    Write a value at the key [required]
    --clear          Clear a repeatable flag [required]
    --default        Reset the key to its declared default [required]
```

Supplying none or two of the three is refused by the framework itself rather than
by the command, so all three sentences are the ordinary selector vocabulary:

```
error: one of --value, --clear, --default is required
error: --value and --clear are mutually exclusive
error: --clear and --default are mutually exclusive
```

There is no trailing positional value: `config set <key> <v>` now reaches the
first of those refusals rather than a value, because the unsatisfied-selector
refusal outranks the extra-positional error.

### Config path override

Override the config path at the CLI level with `--config <path>` (a reserved
global flag), or at construction time with `WithConfigPath(path)`. The CLI
override takes precedence over the construction-time path, which takes
precedence over the default XDG location. Using `--config` with a missing file
is a hard error:

```go
strictcli.WithConfigPath("/etc/mytool/config.json")
```

### Hermetic mode

`--hermetic` is a reserved global flag on every app. It skips config file loading and env var resolution entirely. Values come only from CLI tokens, declared defaults, and infrastructure roots.

## Schema Dump

Every strictcli app automatically supports `--dump-schema`, a reserved flag
that writes a JSON file describing the full CLI structure to
`.strictcli/schema.json` and prints the absolute path to stdout. The schema
includes all commands, flags, args, groups, constraints, and config field
declarations, and is used by external tools like rlsbl to verify that the CLI
surface stays in sync with documentation:

```
$ mytool --dump-schema
.strictcli/schema.json
```

The schema includes all commands, flags, args, groups, and their metadata. It is used by tools like rlsbl to keep documentation in sync with the CLI surface.
The location is declared, never discovered: `WithSchemaPath("build/cli-schema.json")`
or `WithSchemaPathRelativeToRoot("MYTOOL_HOME", "schema.json")` on the app. With
neither, the framework writes `.strictcli/schema.json` anchored at the working
directory the app was CONSTRUCTED in, so a later `chdir` cannot move the file.

## Testing

Use `app.Test(argv)` to run the CLI in-process and capture output without
shelling out. The `Result` struct contains `Stdout`, `Stderr`, `ExitCode`, and
`Data` (the machine payload the handler supplied through `ctx.Payload`). This is the standard way to test
strictcli apps in Go unit tests:

```go
func TestGreet(t *testing.T) {
    app := strictcli.NewApp("mytool", "0.1.0", "test app")
    app.Command("greet", "Say hello", func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
        name := strictcli.Get[string](kwargs, "name")
        ctx.Info("Hello, " + name + "!")
        return strictcli.Exit(0)
    }, strictcli.WithEffect(strictcli.EffectReadOnly), strictcli.WithFlags(
        strictcli.StringFlag("name", "Who to greet", strictcli.Required()),
    ))

    r := app.Test([]string{"greet", "--name", "Alice"})
    if r.ExitCode != 0 {
        t.Fatalf("expected exit 0, got %d: stderr=%q", r.ExitCode, r.Stderr)
    }
    if !strings.Contains(r.Stdout, "Hello, Alice!") {
        t.Fatalf("unexpected stdout: %q", r.Stdout)
    }
}
```

The `Result` struct contains `Stdout`, `Stderr`, `ExitCode`, and `Data` (the machine payload the handler supplied through `ctx.Payload`).

## Deprecated Commands

Register retired commands that print a deprecation message to stderr and exit
with code 1. Deprecated commands appear in help output under a `Deprecated:`
section, giving users visibility into the migration path:

```go
app.Deprecated("old-deploy", "Use 'deploy' instead. See https://example.com/migration")
```

## Passthrough Commands

Passthrough commands bypass all flag and argument parsing and forward raw args
directly to the handler. They are useful for wrapping external tools where the
argument format is not known in advance. Passthrough commands cannot have flags,
args, or flag sets, and declare nothing a choice flag could scope:

```go
app.Passthrough("exec", "Execute a command", func(ctx *strictcli.Context, name string, args []string, globals map[string]interface{}) int {
    ctx.Info(fmt.Sprintf("Running: %s %v", name, args))
    return 0
}, strictcli.WithEffect(strictcli.EffectMutating))
```

A passthrough is an ordinary command registration in Go, so it takes
`WithEffect(...)` like everything else, and may add `WithConsequential()`.
Because its args are forwarded to the child byte-for-byte, the reserved quartet
is not scanned after the passthrough command's name: `mytool exec deploy --dry-run`
passes `--dry-run` to the child.

## Full Example

```go validate
package main

import (
    "fmt"

    "github.com/stricttools/strictcli/go/strictcli"
)

func main() {
    app := strictcli.NewApp("deploy-tool", "0.1.0", "Deployment management tool",
        strictcli.WithConfig(),
        strictcli.WithEnvPrefix("DEPLOY"),
    )

    app.GlobalFlag(strictcli.BoolFlag("color", "Colorize output", strictcli.Default(true)))

    app.Command("status", "Show deployment status", func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
        env := strictcli.Get[string](kwargs, "environment")
        ctx.Debug(fmt.Sprintf("Checking status for environment: %s", env))
        ctx.Payload(map[string]interface{}{
            "environment": env,
            "status":      "healthy",
        })
        return strictcli.Exit(0)
    }, strictcli.WithEffect(strictcli.EffectReadOnly),
        strictcli.PayloadSchema(map[string]interface{}{"type": "object"}),
        strictcli.WithFlags(
        strictcli.StringFlag("environment", "Target environment",
            strictcli.Default("production"),
            strictcli.Short("e"),
            strictcli.Choices(
                strictcli.Ch("production", ""),
                strictcli.Ch("staging", ""),
                strictcli.Ch("dev", ""),
            ),
        ),
    ))

    svc := app.Group("service", "Service management")
    svc.Command("restart", "Restart a service", func(ctx *strictcli.Context, kwargs map[string]interface{}) strictcli.Outcome {
        name := strictcli.Get[string](kwargs, "name")
        timeout, provided := strictcli.GetOpt[int](kwargs, "timeout")
        if !provided {
            timeout = 30
        }
        ctx.Info(fmt.Sprintf("Restarting %s (timeout: %ds)", name, timeout))
        return strictcli.Exit(0)
    }, strictcli.WithEffect(strictcli.EffectMutating), strictcli.WithConsequential(),
        strictcli.WithFlags(
            strictcli.StringFlag("name", "Service name", strictcli.Required()),
            strictcli.IntFlag("timeout",
                "Shutdown timeout in seconds; the handler uses 30 when it is not supplied",
                strictcli.Optional()),
        ))

    app.Run()
}
```

Usage:

```
$ deploy-tool status -e staging
{"environment":"staging","status":"healthy"}

$ deploy-tool --verbose status -e staging
Checking status for environment: staging
{"environment":"staging","status":"healthy"}

$ deploy-tool service restart --name api --timeout 60
about to run consequential command 'service restart'. Proceed? [y/N] y
Restarting api (timeout: 60s)

$ deploy-tool service restart --name api --approve-consequential
Restarting api (timeout: 30s)

$ deploy-tool config show
$ deploy-tool --dump-schema
```
