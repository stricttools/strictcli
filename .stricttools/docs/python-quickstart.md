+++
title = "Python Quickstart"
description = "Build Python CLIs with strictcli: effect classification, required/optional/default presence, choices and retired choices, choice flags, consent, constraints, and update commands."
nav_group = "Guides"
nav_order = 2
+++

# Python Quickstart

This guide walks through building a CLI application with the Python implementation of strictcli.

Everything below is Python-shaped on purpose -- decorators, keyword arguments,
a `Flag` dataclass -- and none of it is a transliteration of the Go or
TypeScript surface. The three implementations are identical in behavior, not in
spelling; see [Language idioms](language-idioms.md).

## Install

```bash
pip install strictcli
```

Import the package:

```python
import strictcli
```

## Creating an App

Every CLI starts with `App`, which takes the application name, version string,
and help text as required arguments. Empty help text is a hard error -- strictcli
enforces self-documenting apps from the first line of code. Additional options
like `config=True`, `env_prefix=`, and `config_format=` are passed as keyword
arguments.

```python validate
import strictcli

app = strictcli.App(name="mytool", version="0.1.0", help="A tool that does useful things")

@app.command("hello", help="Print a greeting", effect="read_only")
def hello(ctx):
    ctx.info("Hello, world!")

app.run()
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

Every command must declare what it does to the world. The `effect` keyword is
mandatory on `@app.command()` and takes exactly one of two values -- there is no
default, and a command registered without it raises `ValueError` at registration
time:

| Value | Meaning |
|-------|---------|
| `effect="read_only"` | The command changes nothing. It never prompts, and calling a mutating member of the effects handle from it is a hard error at call time. |
| `effect="mutating"` | The command changes something. It participates in `--dry-run`, where its effects are recorded instead of performed. |

```python
@app.command("status", help="Show deployment status", effect="read_only")
def status(ctx):
    ctx.info("healthy")

@app.command("deploy", help="Deploy the app", effect="mutating")
def deploy(ctx):
    ctx.info("deploying")
```

Classification answers one question -- "should a dry run record this rather than
perform it?" It is deliberately **not** the same question as "is this dangerous
enough to interrupt someone for?", which is what
[`consequential`](#consequential-commands-and-the-confirm-protocol) answers. A
`mutating` command does not prompt unless it also declares itself
`consequential`.

Classification is a property of the command, so it is emitted in `--dump-schema`
output on every command entry and can be asserted against by check gates.
Deprecated commands are exempt: they have no handler, execute nothing, and
passing `effect=` to `app.deprecate()` is a registration-time error.

## Handler Signature

Every command handler receives `ctx` as its first argument, providing structured
output methods and provenance introspection. Flag and arg values arrive as
keyword arguments with dashes converted to underscores (`--log-file` becomes
`log_file`). The return value must be `int` (exit code), `None` (exit 0), or
`strictcli.outcome(exit_code)` -- any other return type is a hard error.
Structured output is a separate channel (see [Returning Structured
Data](#returning-structured-data)).

```python
@app.command("greet", help="Greet someone", effect="read_only")
@strictcli.flag("name", type=str, presence="required", help="Who to greet")
@strictcli.flag("loud", type=bool, default=False, help="Shout the greeting")
def greet(ctx, name, loud):
    msg = f"Hello, {name}!"
    if loud:
        msg = f"HELLO, {name}!!!"
    ctx.info(msg)
```

- `ctx` provides structured output (`ctx.info`, `ctx.warn`, `ctx.error`, `ctx.debug`), provenance (`ctx.source`), the four reserved-quartet values, and the effects handle (`ctx.effects`).
- Return `int` for an exit code, `None` for exit 0, or `strictcli.outcome(exit_code)`. Any other return type is a hard error. Structured output goes through `ctx.payload(...)` instead (see below).

### Context Methods

| Method | Stream | Purpose |
|--------|--------|---------|
| `ctx.info(msg)` | stdout | Informational messages (suppressed under `--quiet`) |
| `ctx.warn(msg)` | stderr | Warnings (never suppressed) |
| `ctx.error(msg)` | stderr | Errors (never suppressed) |
| `ctx.debug(msg)` | stdout | Debug output (shown only under `--verbose`) |
| `ctx.source(name)` | -- | Provenance of a flag value (`"cli"`, `"env"`, `"config"`, `"default"`, `"implied"`, `"infra"`) |
| `ctx.provided(name)` | -- | Whether the *invocation* caused the value: `True` for `cli`/`env`/`config`/`implied`, `False` for `default`/`infra` |

### Context Properties

The four reserved-quartet flags are never declared by you and never arrive as
handler kwargs -- the framework parses them and delivers them on `ctx`:

| Property | Set by |
|----------|--------|
| `ctx.dry_run` | `--dry-run` |
| `ctx.approve_consequential` | `--approve-consequential` |
| `ctx.quiet` | `--quiet` |
| `ctx.verbose` | `--verbose` |

`ctx.effects` is the recorded-effects handle. Under `--dry-run` its operations
are recorded and rendered as a would-do log instead of being performed, which is
what makes a preview honest.

### Returning Structured Data

Structured data is a separate channel from the return value. A command declares
its payload's JSON Schema with `payload_schema=`, and its handler supplies the
value through `ctx.payload(...)` -- at most once per dispatch, and only on a
command that declared a schema (calling it without one is a hard error at call
time). The payload is printed only under the framework-owned `--json`; `test()`
and `call()` capture it in either mode.

```python
@app.command("status", help="Show status", effect="read_only",
             payload_schema={"type": "object"})
def status(ctx):
    ctx.payload({"healthy": True, "uptime": 3600})
    return strictcli.outcome(exit_code=0)
```

```
$ mytool status --json
{"healthy":true,"uptime":3600}
```

## Flags

Flags are declared with the `@strictcli.flag()` decorator, which attaches flag
metadata to the handler function before command registration. The `name` and
`help` arguments are always required, and so is a **presence declaration** --
exactly one of `presence="required"`, `presence="optional"`, or
`default=<value>` (see [Presence](#presence-required-optional-or-a-default)).
strictcli supports four scalar types: `str` (default), `bool`, `int`, and
`float`, plus compound types `list[T]` and `dict[str, T]` for repeatable and
key-value flags.

### String Flags

```python
@app.command("build", help="Build the project", effect="mutating")
@strictcli.flag("output", type=str, presence="required", help="Output file path")
@strictcli.flag("format", type=str, presence="optional",
                help="Output format; the handler uses json when it is not supplied")
def build(ctx, output, format):
    ctx.info(f"Building to {output} as {format or 'json'}")
```

`presence="required"` means some source -- a CLI token, a bound env var, a config
entry, or an `Implies` injection -- must supply a value. `format` would read more
naturally as `default="json"`, and on a `read_only` command it would be written
that way; `build` is `mutating`, where
[no declaration may carry a value default](flag-system.md#a-mutating-command-may-not-default-a-value),
so the fallback moves into the handler and the flag's help says so.

### Bool Flags

```python
@app.command("report", help="Summarize the last build", effect="read_only")
@strictcli.flag("cache", type=bool, default=True, help="Include cache statistics")
@strictcli.flag("watch", type=bool, presence="required", help="Watch for changes")
@strictcli.flag("color", type=bool, presence="optional", help="Colorize output")
def report(ctx, cache, watch, color):
    if not cache:
        ctx.info("Cache statistics omitted")
    if color is None:
        ctx.info("Color decision inherited from the environment")
```

Bool flags are negatable by default: `--cache` sets `True`, `--no-cache` sets
`False`. A `presence="required"` bool must be answered -- the user passes either
`--flag` or `--no-flag`. A `presence="optional"` bool is a real tri-state:
`--flag` is `True`, `--no-flag` is `False`, and absence arrives as `None`. The
command above is `read_only`, which is what makes `default=True` legal on it;
`default=True` and `default=False` are both refused on a `mutating` command, so
a mutating command's bools are `required` or `optional`.

Note that `verbose` and `quiet` are **not** available as flag names: they belong
to the [reserved quartet](#the-reserved-flag-quartet) and arrive on `ctx`
instead.

### Int Flags

```python
@strictcli.flag("port", type=int, default=8080, help="Server port")
@strictcli.flag("retries", type=int, presence="required", help="Number of retries")
```

Integers are parsed strictly: no leading/trailing whitespace, 64-bit signed bounds, no leading zeros.

### Float Flags

```python
@strictcli.flag("threshold", type=float, default=0.5, help="Score threshold")
@strictcli.flag("rate", type=float, presence="required", help="Rate limit")
```

Float parsing rejects NaN and Inf.

### Flag Options

All available `@strictcli.flag()` parameters are listed below. The `name` and
`help` parameters are always required and must be non-empty strings, and exactly
one of `presence=` / `default=` must be supplied. The remaining parameters
control type, choices, environment variable binding, short aliases, custom
validation callbacks, and repeat semantics including uniqueness enforcement and
env var splitting for repeatable flags:

| Parameter | Description |
|-----------|-------------|
| `name` | Flag name (required). Becomes `--name` on the CLI. |
| `help` | Help text (required). Must be non-empty. |
| `type` | Value type: `str`, `bool`, `int`, or `float` (default: `str`). |
| `presence` | `"required"` or `"optional"`. Mutually exclusive with `default`; exactly one of the two must be declared. |
| `default` | The declared default value -- the third presence spelling. `default=None` is a registration error redirecting to `presence="optional"`. |
| `short` | Single-character short form (e.g., `short="o"` for `-o`). |
| `env` | Environment variable name. Precedence: CLI > env > config > default. |
| `choices` | List of allowed values. Not available on bool flags. |
| `retired_choices` | List of `RetiredChoice(<value>, message="...")` records: spellings this flag used to accept, each naming its replacement. Requires `choices`. |
| `validate` | Custom validation function. |
| `repeatable` | If `True`, the flag can appear multiple times, collecting values into a list. |
| `unique` | Requires explicit `True` or `False` when `repeatable=True`. Rejects duplicate values when `True`. |
| `env_separator` | Single character to split env var values for repeatable flags. Required when both `repeatable` and `env` are set. |

### Retired choices

A flag or arg that declares `choices` may also declare the spellings it **used
to** accept, each carrying the message that names its replacement. A value
matching one is refused at parse time, ahead of the invalid-value check:

```python
@strictcli.flag("format", type=str, presence="required", help="Output format",
                choices=[strictcli.Choice("text"), strictcli.Choice("json")],
                retired_choices=[
                    strictcli.RetiredChoice(
                        "xml", message="use 'json' (XML output was dropped)"),
                ])
```

```
error: --format: value 'xml' retired: use 'json' (XML output was dropped)
```

A retired value is not a choice: help never lists it, and the `value_schema`
enum `--dump-schema` publishes -- together with the MCP tool schema derived
from it -- carries the live values only. `--dump-schema` publishes the
declaration separately, as a `retired_choices` map from spelling to message,
sorted ascending by key and omitted when nothing is retired.

A retired spelling may not also be a live choice, may not repeat, may not carry
an empty message, may not be what a `default` names, and may not be declared
without `choices`; each is a registration-time hard error. Retired choices are
incompatible with bool, like `choices` itself. Full rules:
[Retired choices](flag-system.md#retired-choices).

### Short Flags

```python
@strictcli.flag("output", short="o", type=str, presence="required", help="Output file")
@strictcli.flag("recursive", short="r", type=bool, default=False, help="Recurse into subdirectories")
```

Usage: `-o myfile.txt`, `-r`.

### Choices

Restrict a flag to specific values using the `choices` parameter. Values not in
the choices list produce a parse error listing all allowed values. All choice
values must match the declared flag type, and bool flags cannot have choices.

**Every entry is a `strictcli.Choice` record: a value, and optional help.** A
bare value is refused at registration -- an entry that may carry help and an
entry that carries none would be two spellings of one fact:

```python
@strictcli.flag("format", type=str, default="json", help="Output format",
                choices=[strictcli.Choice("json", help="one JSON document"),
                         strictcli.Choice("yaml"),
                         strictcli.Choice("csv")])
@strictcli.flag("level", type=int, presence="required", help="Compression level",
                choices=[strictcli.Choice(1), strictcli.Choice(2), strictcli.Choice(3)])
```

```
Flag "format": choices entry 0 is a bare value: declare it as Choice(<value>, help=...)
```

Help on an entry decides how the flag renders. Until one entry carries help the
flag keeps its one-line `[choices: ...]` form; from the first entry that has
help, the whole flag renders as an indented block:

```
  --format <str>    Output format [default: json]
    json            one JSON document
    yaml
    csv
  --level <int>     Compression level [choices: 1, 2, 3] [required]
```

A declared `default` value must be in the choices list, checked at registration.
`presence="optional"` declares no value, so nothing is checked at registration
and absence is never matched against `choices` at parse time.

`strictcli.Choice` and the `@strictcli.choice` decorator are case twins naming
**different** constructs: `Choice` is one entry of a `choices=` value flag, and
`@choice` declares one choice of a [choice flag](#choice-flags). A choice class
that reaches `choices=` is refused by name rather than misread.

### Environment Variables

Read flag values from the environment using the `env` parameter. Environment
variables sit between CLI tokens and config file values in the resolution
cascade (CLI > env > config > default), and are skipped entirely under
`--hermetic` mode:

```python
@strictcli.flag("token", type=str, presence="required", env="MYTOOL_TOKEN", help="API token")
```

Precedence: CLI > env > config > default. Boolean env values accept `1|true|yes`
/ `0|false|no` (case-insensitive). An env- or config-supplied value satisfies a
`presence="required"` declaration and makes the flag *provided* --
`ctx.provided("token")` is `True` with source `"env"` or `"config"`.

### Presence: required, optional, or a default

Every flag and every positional argument declares **exactly one** of three facts
about itself. Nothing is inferred from the shape of another declaration:

| Fact | Spelling | The handler receives |
|------|----------|----------------------|
| **required** | `presence="required"` | the supplied value; the parse fails if nothing supplies one |
| **optional** | `presence="optional"` | the supplied value, or `None` when nothing supplied one |
| **default** | `default=<value>` | the supplied value, or the declared default |

```python
@strictcli.flag("target", type=str, presence="required", help="Deploy target")
@strictcli.flag("region", type=str, default="us-east", help="AWS region")
@strictcli.flag("tag", type=str, presence="optional", help="Release tag")
```

Declaring none of the three, or more than one, is a registration-time error:

```
Flag "target": presence is undeclared: declare exactly one of presence="required", presence="optional", or default=<value>
Flag "target": presence is declared twice: presence="required" and default=x cannot be combined; declare exactly one
```

`default=None` is **not** a spelling of optionality. It is refused, with a
redirect to the one spelling optionality has:

```
Flag "tag": default=None does not declare optionality: use presence="optional" (it delivers None when the flag is absent)
```

A handler parameter bound to an optional flag or arg must either declare no
default at all or default to `None` -- anything else re-introduces at the
handler boundary the sentinel the declaration just removed:

```python
# Both are fine
def publish(ctx, tag): ...
def publish(ctx, tag=None): ...

# Registration error:
#   command "publish": handler parameter 'tag' is bound to optional flag '--tag' and must default to None
def publish(ctx, tag=""): ...
```

To ask whether the invocation supplied a value rather than the declaration, use
`ctx.provided(name)` -- `True` for `cli`, `env`, `config` and `implied`, `False`
for `default` and `infra`. Inside a choice flag's scope the answer depends on the
door a value arrived through: a scoped field the caller supplied answers `True`
from the command line and from the flat machine door, and `False` from the record
door, where a constructed scope has already filled its declared defaults and the
framework refuses to guess which fields the caller wrote (see
[the door table](flag-system.md#was-this-supplied-ctxprovided)).

In help output, every flag and arg renders exactly one presence part:
`[required]`, `[optional]`, or `[default: <value>]`.

### Repeatable Flags

A repeatable flag can appear multiple times on the command line, collecting
values into a list. It declares presence like every other flag -- there is no
silent empty-list default. The `unique` parameter is mandatory: set
`unique=True` to reject duplicate values, or `unique=False` to allow them:

```python
@app.command("process", help="Process records", effect="read_only")
@strictcli.flag("record", type=str, default=[], help="A record to process", repeatable=True, unique=False)
def process(ctx, record):
    for r in record:
        ctx.info(f"Processing: {r}")
```

```
$ mytool process --record alpha --record beta --record gamma
Processing: alpha
Processing: beta
Processing: gamma
```

`default=[]` declares the empty list explicitly and renders `[default: []]` in
help. The alternatives are `presence="optional"`, where zero occurrences deliver
`None` rather than `[]`, and `presence="required"`, where at least one
occurrence must arrive from some source.

You can also use `list[T]` as the type, which is equivalent to `repeatable=True` with the appropriate item type:

```python
@strictcli.flag("port", type=list[int], default=[], help="Ports to listen on", unique=False)
```

A dict flag declares presence the same way, with `default={}` as the explicit
empty declaration:

```python
@strictcli.flag("label", type=dict[str, str], default={}, help="key=value labels")
```

## Positional Arguments

Use `@strictcli.arg()` to declare positional arguments that are consumed in
order after all flags have been parsed. An argument declares presence with the
same three spellings a flag uses -- `presence="required"`, `presence="optional"`,
or `default=<value>`. There is no `required=` parameter.

Pass `args=[...]` on `@app.command()` when a command has more than one
positional, so the order is the list's order:

```python
@app.command(
    "show",
    help="Show what is deployed to an environment",
    effect="read_only",
    args=[
        strictcli.Arg(name="environment", help="Target environment", presence="required"),
        strictcli.Arg(name="version", help="Version to inspect", default="latest"),
    ],
)
def show(ctx, environment, version):
    ctx.info(f"Showing {version} in {environment}")
```

A positional arg is reached by the
[mutating-default ban](flag-system.md#a-mutating-command-may-not-default-a-value)
exactly as a flag is, which is why the command above is `read_only`: on a
`mutating` one, `default="latest"` is a registration error and the arg declares
`presence="required"` or `presence="optional"` instead.

An optional arg delivers absence as a present keyword argument (`None`), exactly as an
optional flag does:

```python
@app.command("greet", help="Greet someone", effect="read_only")
@strictcli.arg("title", help="An honorific", presence="optional")
def greet(ctx, title):
    ctx.info("Hello!" if title is None else f"Hello, {title}!")
```

### Arg Parameters

| Parameter | Description |
|-----------|-------------|
| `name` | Argument name (required). |
| `help` | Help text (required). |
| `presence` | `"required"` or `"optional"`. Mutually exclusive with `default`; exactly one of the two must be declared. |
| `default` | The declared default value -- the third presence spelling. `default=None` is a registration error redirecting to `presence="optional"`. |
| `type` | Type: `str`, `bool`, `int`, or `float` (default: `str`). |
| `variadic` | If `True`, collects all remaining positional values. Must be the last arg. |
| `choices` | List of allowed values. |
| `retired_choices` | List of `RetiredChoice(<value>, message="...")` records: spellings this arg used to accept, each naming its replacement. Requires `choices`. |

### Variadic Arguments

A variadic argument collects all remaining positional values into a list. It
must be the last positional argument in the command's declaration, and only one
variadic argument is allowed per command. Because it always delivers a list,
`presence="required"` means *at least one value* and `presence="optional"` means
*possibly none*:

```python
@app.command("process", help="Process files", effect="read_only")
@strictcli.arg("files", help="Files to process", variadic=True, presence="required")
def process(ctx, files):
    for f in files:
        ctx.info(f"Processing: {f}")
```

```
$ mytool process a.txt b.txt c.txt
Processing: a.txt
Processing: b.txt
Processing: c.txt
```

A `default` on a variadic arg is a registration error -- the empty case is
spelled once, as `presence="optional"`:

```
Arg "files": a variadic arg cannot declare default=: it always delivers a list, so declare presence="required" for at least one value or presence="optional" for possibly none
```

Only one variadic argument is allowed, and it must be the last. You can also use `list[T]` as the type for typed variadic args (e.g., `type=list[int], variadic=True`).

## Global Flags

Global flags are available to all commands and can appear before or after the
command name in argv. Pass them via the `flags` parameter on `App`. Global flag
names cannot collide with reserved framework names like `help`, `version`,
`dump-schema`, `mcp`, `config`, or `hermetic`, nor with the reserved quartet:

```python
app = strictcli.App(
    name="mytool",
    version="0.1.0",
    help="A useful tool",
    flags=[
        strictcli.Flag(name="color", type=bool, default=True, help="Colorize output"),
        strictcli.Flag(name="log-level", type=str, default="info", help="Log level",
                       choices=[strictcli.Choice("debug"), strictcli.Choice("info"),
                                strictcli.Choice("warn"), strictcli.Choice("error")]),
    ],
)

@app.command("deploy", help="Deploy the app", effect="mutating")
def deploy(ctx, color, log_level):
    if not color:
        ctx.info("Color disabled")
    ctx.info(f"Log level: {log_level}")
```

Usage: `mytool --no-color deploy` or `mytool deploy --no-color` (global flags can appear before or after the command).

Reserved global flag names that cannot be used: `help`, `h`, `version`, `v`, `dump-schema`, `mcp`, `config`, `hermetic`, plus the reserved quartet `dry-run`, `approve-consequential`, `quiet`, `verbose`, plus `json`, which selects machine mode. The name `yes` is banned outright -- the confirmation skip is `--approve-consequential`.

## Command Groups

Groups organize commands into namespaces, creating a hierarchical command
structure like `mytool dns zone list`. Groups can nest to arbitrary depth, and
each group requires a name and help text. When a group is reached without a
subcommand, the group's help text is displayed.

```python
app = strictcli.App(name="mytool", version="0.1.0", help="Infrastructure tool")

dns = app.group("dns", help="DNS management")

@dns.command("list", help="List DNS records", effect="read_only")
def dns_list(ctx):
    ctx.info("Listing records...")

@dns.command("create", help="Create a DNS record", effect="mutating")
@strictcli.flag("name", type=str, presence="required", help="Record name")
def dns_create(ctx, name):
    ctx.info(f"Creating record: {name}")

zone = dns.group("zone", help="Zone management")

@zone.command("list", help="List zones", effect="read_only")
def zone_list(ctx):
    ctx.info("Listing zones...")

@zone.command("delete", help="Delete a zone", effect="mutating", consequential=True)
@strictcli.flag("name", type=str, presence="required", help="Zone name")
def zone_delete(ctx, name):
    ctx.info(f"Deleting zone: {name}")
```

Usage:

```
$ mytool dns list
$ mytool dns create --name example.com
$ mytool dns zone list
$ mytool dns zone delete --name example.com
```

## Flag Naming Conventions

strictcli enforces strict flag naming rules at registration time to prevent
ambiguous flag names and protect the negation namespace. Violations raise
`ValueError` with a descriptive message explaining what is wrong and how to fix
it. These rules are identical across all three implementations.

### Bare `--force` is banned

The flag name cannot be exactly `"force"` because a generic force flag lets
automation bypass guardrails without specifying what is being forced. Use a
qualified name that describes the specific action being forced, making the
intent explicit and auditable:

```python
# This raises ValueError:
strictcli.flag("force", type=bool, default=False, help="Force the operation")

# Use a qualified name instead:
strictcli.flag("force-overwrite", type=bool, default=False, help="Overwrite existing files")
strictcli.flag("force-delete", type=bool, default=False, help="Delete without confirmation")
```

### `--no-*` prefix is reserved

Flag names cannot start with `no-` because the `--no-` prefix is auto-generated
by the negation system for boolean flags. Allowing user-defined flags in this
namespace would create double-negation ambiguity where `--no-no-cache` becomes
the negation form of a flag named `no-cache`:

```python
# This raises ValueError:
strictcli.flag("no-cache", type=bool, default=False, help="Disable caching")

# Use a positive name instead:
strictcli.flag("cache", type=bool, default=True, help="Enable caching")
# Users pass --no-cache to disable
```

### The reserved flag quartet

Four flag names are owned by the framework and cannot be declared at any level --
not as app global flags, not as command flags, not inside a flag set, and not
inside a choice's scope at any depth:

| Flag | Delivered as | Meaning |
|------|-------------|---------|
| `--dry-run` | `ctx.dry_run` | Record effects instead of performing them, then print the would-do log |
| `--approve-consequential` | `ctx.approve_consequential` | Answer the confirm prompt in advance |
| `--quiet` | `ctx.quiet` | Suppress `ctx.info` output; warnings and errors still print |
| `--verbose` | `ctx.verbose` | Enable `ctx.debug` output |

```python
# Every one of these raises ValueError:
strictcli.flag("dry-run", type=bool, default=False, help="Simulate the run")
strictcli.flag("verbose", type=bool, default=False, help="Be verbose")
strictcli.flag("quiet", type=bool, default=False, help="Be quiet")
```

The error message is `flag name 'dry-run' is reserved by the framework
(dry-run, approve-consequential, quiet, verbose)`. The name `yes` is banned
outright with its own message pointing at `--approve-consequential`, so that a
private `--yes` cannot restate the confirmation skip in a different spelling.

All four are recognized anywhere in argv: `mytool deploy --dry-run` and
`mytool --dry-run deploy` are equivalent. Two boundaries stop the scan -- a bare
`--` (everything after it is data) and a passthrough command's name (its args
are forwarded to the child byte-for-byte).

### Refusing `--dry-run` with `dry_run_supported`

`--dry-run` works on every `mutating` command by default: its effects are
recorded rather than performed. Some commands cannot honor that honestly --
their effects escape the effects handle, or their later steps read state that
their earlier (recorded, therefore un-performed) steps would have written. Such
a command declares `dry_run_supported=False` with a mandatory
`dry_run_unsupported_reason`:

```python
@app.command(
    "migrate",
    help="Run pending database migrations",
    effect="mutating",
    dry_run_supported=False,
    dry_run_unsupported_reason=(
        "each migration reads the schema the previous one wrote, "
        "so a recorded run would report the wrong pending set"
    ),
)
def migrate(ctx):
    ...
```

`--dry-run` is then refused at parse time rather than rendering a preview that
would lie:

```
$ mytool migrate --dry-run
error: --dry-run is not supported by command 'migrate': each migration reads the schema the previous one wrote, so a recorded run would report the wrong pending set
```

Three guardrails apply at registration time:

- `dry_run_supported=False` on a `read_only` command is an error -- a command that changes nothing has no effects a preview could misrepresent.
- `dry_run_supported=False` without a non-empty reason is an error -- say what a preview cannot honestly show.
- A `dry_run_unsupported_reason` without `dry_run_supported=False` is an error -- there is nothing to explain while dry run is supported.

The reason also appears in the command's help under a `Dry run:` section, and in
`--dump-schema` output as the pair `dry_run_supported` / `dry_run_unsupported_reason`.
Both keys are emitted only when declared, so a schema entry without them means
dry run is supported. `--help` always beats the refusal: asking what a command
does is never answered with a refusal to preview it.

## Consequential Commands and the Confirm Protocol

Classification says whether a dry run should record rather than perform.
`consequential` says something different: that these effects are worth
interrupting a human for. It is the **only** thing that makes the framework
prompt -- a plain `mutating` command never does.

```python
@app.command("destroy", help="Destroy the cluster", effect="mutating", consequential=True)
@strictcli.arg("cluster", help="Cluster to destroy", presence="required")
def destroy(ctx, cluster):
    ctx.info(f"Destroying {cluster}")
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
`app.test()` behaves as if `--approve-consequential` were passed; `app.call()`
(and `acall()`, and the MCP server) take the consent from the call instead and
refuse a consequential command without it:

```python
# Raises InvokeError:
#   command 'destroy' is consequential: the call must carry confirmation
app.call("destroy", env="prod")

# Proceeds
app.call("destroy", approve_consequential=True, env="prod")
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
`tools/list` publish `effect` and `consequential` beside the argument schema so
a caller can see the requirement before it calls. There is no bypass flag:
`--approve-consequential` answers the prompt and does nothing else. A `read_only` command cannot be
declared consequential -- a command that changes nothing has nothing to confirm --
and trying raises `ValueError` at registration time.

A consequential passthrough command is not exempt. The framework knows *less*
about what is about to happen there, not more.

### Help text is mandatory

Every flag, arg, command, group, and app must have non-empty help text. Missing
or empty help raises `ValueError` at registration time with no opt-out. This
ensures that every strictcli application is self-documenting and users always
have access to meaningful help for every flag and command.

## Choice Flags

A **choice flag** elects exactly one of its declared choices per invocation, and
each choice declares a **scope**: the flags that exist only while that choice is
elected. It is the framework's one construct for "exactly one of these", and
there is no at-most-one construct anywhere -- an absent selection is a choice
nobody named, so the answer is to name it.

A choice is a frozen, keyword-only dataclass declared with `@strictcli.choice`,
and its fields are the scope's flags. `sub_flag(...)` takes no `name=`: the
field name **is** the flag name (`phone_number` becomes `--phone-number`), which
is the mapping the framework already uses in the other direction for handler
parameters.

```python
from typing import assert_never

@strictcli.choice("email", help="deliver the notification as an email message")
class Email:
    subject: str = strictcli.sub_flag(help="subject line of the message", presence="required")
    recipient: str = strictcli.sub_flag(help="destination email address", presence="required")


@strictcli.choice("sms", help="deliver the notification as a text message")
class Sms:
    phone_number: str = strictcli.sub_flag(help="destination number in E.164 form", presence="required")


@strictcli.choice("webhook", help="post the notification to a URL")
class Webhook:
    url: str = strictcli.sub_flag(help="endpoint to post to", presence="required")
    retries: int = strictcli.sub_flag(help="delivery attempts before giving up; 3 when omitted",
                                      presence="optional")


@app.command("send", help="Send one notification through exactly one channel", effect="mutating")
@strictcli.choice_flag("via", help="Delivery channel", short="v", presence="required",
                       elect_by="selector-token", choices=[Email, Sms, Webhook])
def send(ctx, via: Email | Sms | Webhook) -> int:
    match via:
        case Email(subject=subject, recipient=recipient):
            ctx.info(f"emailing {recipient}: {subject}")
        case Sms(phone_number=number):
            ctx.info(f"texting {number}")
        case Webhook(url=url, retries=retries):
            ctx.info(f"posting to {url} ({retries} retries)")
        case _:
            assert_never(via)
    return 0
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

### The handler annotation is mandatory

The parameter bound to a choice flag must be annotated with exactly the declared
union, and `**kwargs` handlers are banned on a command that declares one:

```
command "send": handler parameter 'via' is bound to choice flag '--via' and must be annotated Email | Sms | Webhook, got nothing
```

That check is what makes `assert_never` sound: without it a handler could
annotate `via: Email` and silently skip two branches with the type checker's
blessing. Annotations are resolved at registration through
`typing.get_type_hints`, so a name importable only under `TYPE_CHECKING` is a
registration error naming it rather than a `NameError` at import time.

### Member spelling

`elect_by` is mandatory and has no default. `elect_by="member-flags"` spells
each choice as its own flag instead, and the choice flag's own name is never
typed -- it is the handler key and the noun help and errors use. A member
carries its own payload in a field named `value`, declared with
`member_value(help=...)`, which takes no presence keyword because electing the
member supplies it.

```python
@strictcli.choice("profile", help="use the named profile")
class NamedProfile:
    value: str = strictcli.member_value(help="profile name")
    create_missing: bool = strictcli.sub_flag(help="create the profile if it does not exist",
                                              presence="optional")


@strictcli.choice("all-profiles", help="apply to every profile")
class AllProfiles:
    pass


@app.command("sync", help="Synchronize profiles", effect="mutating")
@strictcli.choice_flag("scope", help="What to synchronize", presence="required",
                       elect_by="member-flags", choices=[NamedProfile, AllProfiles])
def sync(ctx, scope: NamedProfile | AllProfiles) -> int:
    match scope:
        case NamedProfile(value=name, create_missing=create):
            ctx.info(f"syncing {name} (create={create})")
        case AllProfiles():
            ctx.info("syncing every profile")
        case _:
            assert_never(scope)
    return 0
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
syncing work (create=True)

$ myapp sync --all-profiles --create-missing
error: flag '--create-missing' is only valid under '--profile', but '--all-profiles' was elected

$ myapp sync --profile work --all-profiles
error: --profile and --all-profiles are mutually exclusive

$ myapp sync
error: one of --profile, --all-profiles is required
```

A bool member is elected by `--<name>` and only when it resolves to **true**;
`--no-<name>` *declines* -- it says "not this one" and elects nothing, and
combining a decline with a real election is a parse error. Member election is
**command-line only**: the spelling exists to make the operator choose in the
invocation, so env and config are not consulted for a member at all. A
member-spelled choice flag cannot carry a short, since it is never typed.

### Presence, defaults, and recursion

A choice flag declares `presence="required"` or a `default`; `presence="optional"`
is a registration error:

```
Flag "via": a choice flag cannot declare presence="optional": an absent selection is a choice nobody named, so name it as a choice of its own
```

A default **is a choice instance**, so a defaulted selection is complete by
construction -- a frozen dataclass cannot be built without its required fields,
so there is nothing to check and no error to raise:

```python
@strictcli.choice_flag("via", help="Delivery channel", elect_by="selector-token",
                       choices=[Email, Sms], default=Sms(phone_number="+15550100"))
```

```
  --via <choice>              Delivery channel [default: sms (phone-number=+15550100)]
```

Electing a choice on the command line never borrows the default's values.
`ctx.provided("via")` is true when the invocation elected and false when the
declaration's default did, and `ctx.source("via")` reports which.

A choice flag is a flag, so `strictcli.sub_choice_flag(...)` declares a nested
one inside a choice's scope, to unlimited depth. "Required exactly when
user-facing" stops being a rule a handler enforces and becomes where the
declaration sits.

### What the handler sees

Scoped flags are **never** top-level handler arguments, at any depth: the only
key a choice flag adds is its own, so every declared top-level key is still
always present. Inside the record, `strictcli.provided(via, "subject")` answers
whether the invocation caused a field's value; `ctx.provided` deliberately does
not see scope interiors, because a scoped name is not unique command-wide.

Name every parameter explicitly. A handler that accepts `**kwargs` without the
command declaring `forwarding=strictcli.Forwarding(reason=...)` is a
registration-time error -- an unnamed parameter bag hides which flags a handler
actually consumes, and on a choice-flag command it is banned outright.
## Constraints

Declare rules over a command's flags and args using the `constraints` parameter.
Four kinds are available, all frozen dataclasses whose **first field is `name`**:

| Constraint | Behavior |
|------------|----------|
| `AtLeastOne(name, members)` | At least one member is engaged. Members may co-occur -- it has no upper bound and is never exclusivity. |
| `AllOrNone(name, members)` | Either every member is engaged or none is. Nothing engaged is vacuously satisfied. |
| `Requires(name, flag, depends_on)` | If `flag` is provided, `depends_on` must also be provided. |
| `Implies(name, flag, implies, value)` | When `flag` is provided, automatically set `implies` to `value`. |

The name is mandatory: it is what a violation prints, what `--help` shows, and
what lets one constraint be a member of another.

```python
@app.command("deploy", help="Deploy the app", effect="mutating", constraints=[
    # --target and --region must both appear or neither
    strictcli.AllOrNone("deploy-destination", [
        strictcli.Member("target"), strictcli.Member("region"),
    ]),
    # at least one rollout control has to be chosen
    strictcli.AtLeastOne("rollout-mode", [
        strictcli.Member("staged", when="true"),
        strictcli.Member("batch-size"),
    ]),
    # --staged implies --wait=True
    strictcli.Implies("staged-waits", flag="staged", implies="wait", value=True),
])
@strictcli.flag("target", type=str, presence="optional", help="Deploy target")
@strictcli.flag("region", type=str, presence="optional", help="Target region")
@strictcli.flag("staged", type=bool, presence="optional", help="Roll out in stages")
@strictcli.flag("batch-size", type=int, presence="optional", help="Instances per batch")
@strictcli.flag("wait", type=bool, presence="optional", help="Block until the rollout settles")
def deploy(ctx, target, region, staged, batch_size, wait):
    ctx.info(f"Deploying to {target} in {region}")
```

### Members

A member is a `Member(name, when=...)` **record** -- a bare string is refused,
for the same reason a bare `choices=` entry is. It references, by name, a
command flag, a positional arg, or another named `AtLeastOne` / `AllOrNone` of
the same command; nesting is a cycle-checked DAG at unlimited depth. A member
carries no presence and no help of its own, and a co-occurrence constraint needs
at least two of them.

`Requires` and `Implies` are narrower on purpose: their operands are **flags
only**, by name, and they take no `when`.

`when` is the closed election vocabulary that decides when a member counts as
engaged:

| `when` | Engaged when | Legal on |
|---|---|---|
| `"present"` (the default) | the value was provided -- `cli`, `env`, `config` or `implied` | every type |
| `"true"` | provided **and** the value is `True` | `bool` only |
| `"non_empty"` | provided **and** the value is a non-empty string, list or map | `str`, list and dict flags, and variadic args |

**A bool member must declare its election explicitly** -- omitting `when` on a
bool is a registration error. Without that rule `--no-staged` would engage a
constraint while selecting nothing. Declaring `when="true"` on a non-bool, or
`when="non_empty"` on a bool, int or float, is a registration error too.

Nesting is what expresses "either identity pair, but never half of one":

```python
@app.command("rewrite", help="Rewrite author identity", effect="mutating",
             constraints=[
                 strictcli.AllOrNone("author-name", [
                     strictcli.Member("old-name"), strictcli.Member("new-name"),
                 ]),
                 strictcli.AllOrNone("author-email", [
                     strictcli.Member("old-email"), strictcli.Member("new-email"),
                 ]),
                 strictcli.AtLeastOne("author-change", [
                     strictcli.Member("author-name"),
                     strictcli.Member("author-email"),
                 ]),
             ])
@strictcli.flag("old-name", type=str, presence="optional", help="Current display name")
@strictcli.flag("new-name", type=str, presence="optional", help="New display name")
@strictcli.flag("old-email", type=str, presence="optional", help="Current email address")
@strictcli.flag("new-email", type=str, presence="optional", help="New email address")
def rewrite(ctx, old_name, new_name, old_email, new_email):
    ...
```

```
Constraints:
  author-name      all or none of --old-name, --new-name
  author-email     all or none of --old-email, --new-email
  author-change    at least one of (--old-name with --new-name), (--old-email with --new-email)
```

Children are evaluated before parents, so an operator who typed one half of a
pair reads `constraint "author-name": --old-name, --new-name must be used
together` rather than a complaint about the whole selection.

No member of a co-occurrence constraint may declare `presence="required"` -- a
member the invocation must always supply leaves the constraint nothing to
decide. A `default` is legal and never engages the constraint on its own;
`optional` is the ordinary case, and membership neither makes a flag required
nor exempts it from being required.

Constraints cannot reference the reserved quartet: `dry-run` is not a flag you
declare, so it can never be a member, a `Requires` target or an `Implies`
subject. They operate at root scope only -- naming a flag declared inside a
choice's scope is a registration error, because the scope already *is* the
constraint.

Engagement reads presence through the same predicate `ctx.provided` uses -- a
flag counts as provided when the invocation caused its value. A declared default
never engages a member, never satisfies a `Requires`, and never fires an
`Implies` trigger. Every constraint renders in `--help` under a `Constraints:`
section, publishes its members in `--dump-schema`, and projects into MCP tool
schemas (`anyOf` / `dependentRequired`) with anything a JSON Schema keyword
cannot carry stated in the tool description instead.

## Update Commands

A command that changes some properties of one resource and leaves the rest alone
declares what it updates with `update_of=`. `UpdateOf` is a frozen, keyword-only
record whose first field is the resource name, joining the `AtLeastOne` /
`AllOrNone` / `Requires` / `Implies` family of declarations that name a rule:

```python
@app.command("update-record", help="change one DNS record in place", effect="mutating",
             update_of=strictcli.UpdateOf("dns-record", write_mode="sparse",
                                          identity=["zone", "record-id"],
                                          properties=["content", "ttl", "proxied"]))
@strictcli.flag("zone", type=str, help="zone the record belongs to", presence="required")
@strictcli.flag("record-id", type=str, help="identifier of the record to change", presence="required")
@strictcli.flag("content", type=str, help="record content", presence="optional")
@strictcli.flag("ttl", type=int, help="time to live in seconds", presence="optional", nullable=True)
@strictcli.flag("proxied", type=bool, help="whether the record is proxied", presence="optional")
def update_record(ctx, zone, record_id, content, ttl, proxied):
    body = {}
    if ctx.provided("content"):
        body["content"] = content
    if ctx.unset("ttl"):
        body["ttl"] = None
    elif ctx.provided("ttl"):
        body["ttl"] = ttl
    if ctx.provided("proxied"):
        body["proxied"] = proxied
    ctx.effects.http("PATCH", f"https://api.example.com/zones/{zone}/dns_records/{record_id}")
    return 0
```

`write_mode=` is a keyword taking a closed string vocabulary, which is how Python
spells `effect=`, `presence=` and `elect_by=`, and it carries **no default** --
omitting it is Python's own `TypeError` at the declaration site. `identity=` and
`properties=` are lists of **declared names**, dashed exactly as
`Member("old-name")` takes them; there is no alternate underscored spelling on
the declaration surface. `nullable=True` sits on the flag, beside `presence=`.

A **property declares `presence="optional"` and nothing else**: absence *is*
untouched. `presence="required"` is a registration error, and a value default is
refused twice over -- an update command is always `mutating`, so
[the ban](flag-system.md#a-mutating-command-may-not-default-a-value) reaches it
first. An **identity member** declares `required` or `optional`, and may be a
positional arg where a property may not.

The framework refuses an invocation that supplies no property, naming every
declared one:

```
$ mytool update-record --zone z1 --record-id r7
error: update "dns-record": at least one property is required: --content, --ttl, --proxied
try 'mytool update-record --help'
```

A nullable property mints `--unset-<prop>`, which shares its property's help line
the way `--no-<x>` shares a bool's, and is answered by `ctx.unset(name)`:

```
$ mytool update-record --help
mytool update-record -- change one DNS record in place

Flags:
  --zone <str>                zone the record belongs to [required]
  --record-id <str>           identifier of the record to change [required]
  --content <str>             record content [optional]
  --ttl <int>, --unset-ttl    time to live in seconds [optional]
  --proxied, --no-proxied     whether the record is proxied [optional]

$ mytool update-record --zone z1 --record-id r7 --ttl 60 --unset-ttl
error: --ttl and --unset-ttl are mutually exclusive: a property is either written or cleared
```

Inside an update command `--no-proxied` **writes `False`** -- stating false is
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

## Flag Sets

Reuse the same set of flags across multiple commands by grouping them into a
named `FlagSet`. Each command that uses a flag set receives all its flags as if
they were declared directly on the command, including type checking, env var
binding, and constraint validation:

```python
auth_flags = strictcli.FlagSet(name="auth", flags=[
    strictcli.Flag(name="token", type=str, presence="required", env="MYTOOL_TOKEN", help="API token"),
    strictcli.Flag(name="region", type=str, presence="optional", help="API region"),
])

@app.command("list", help="List resources", effect="read_only", flag_sets=[auth_flags])
def list_cmd(ctx, token, region):
    ctx.info(f"Listing in {region}")

@app.command("delete", help="Delete a resource", effect="mutating",
             consequential=True, flag_sets=[auth_flags])
@strictcli.flag("resource-id", type=str, presence="required", help="Resource to delete")
def delete_cmd(ctx, token, region, resource_id):
    ctx.info(f"Deleting {resource_id}")
```

## Config File Support

Pass `config=True` to `App` to enable automatic config file loading from the
XDG config directory and register five `config` subcommands (`show`, `set`,
`path`, `edit`, `init`) for managing the configuration file. Config values
participate in the flag resolution cascade between env vars and defaults.

```python
app = strictcli.App(
    name="mytool",
    version="0.1.0",
    help="A configurable tool",
    config=True,
    env_prefix="MYTOOL",
)

@app.command("run", help="Run the tool", effect="read_only")
@strictcli.flag("port", type=int, default=8080, env="MYTOOL_PORT", help="Server port")
def run(ctx, port):
    ctx.info(f"Listening on port {port}")
```

Config files live at `~/.config/mytool/config.json` by default. Value precedence: CLI > env > config > default.

### Config format

Default format is JSON, which uses the standard library parser. Use TOML with
`config_format="toml"` for human-editable configuration files with comments and
section headers. TOML parsing is strict and rejects 6 TOML-1.1-only constructs
(including backslash-e escapes and trailing commas in inline tables) to maintain
byte-level parity across the Python, Go, and TypeScript implementations:

```python
app = strictcli.App(
    name="mytool",
    version="0.1.0",
    help="A configurable tool",
    config=True,
    config_format="toml",
)
```

### Auto-registered config commands

When config is enabled, these five subcommands are registered automatically
under a `config` group. They provide a complete config management interface
without any additional code, covering display of current values with their
provenance sources, in-place modification, path inspection, editor integration,
and initialization of the config file with default values:

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
global flag), or at construction time with `config_path=`. The CLI override
takes precedence over the construction-time path, which takes precedence over
the default XDG location. Using `--config` with a missing file is a hard error:

```python
app = strictcli.App(
    name="mytool",
    version="0.1.0",
    help="A configurable tool",
    config=True,
    config_path="~/.mytool/config.json",
)
```

### Config fields

Declare typed config-only fields (not backed by CLI flags) with
`config_field()`. Config fields are validated at runtime when their bound
commands are dispatched: required fields must be present in the config file with
the correct type:

```python
app.config_field("serve.port", type=int, help="Default server port", default=8080)
app.config_field("serve.host", type=str, help="Bind address", default="localhost")
app.config_field("api_key", type=str, help="API key")  # required -- no default
```

A config field is **not** a CLI declaration and does not take a `presence=`
argument: it has no presence on a command line, no help marker, and no
`ctx.provided` question, so it is still required exactly when it declares no
default. A field that collides with a flag inherits the flag's
handling, which is the declared one.

### Hermetic mode

`--hermetic` is a reserved global flag on every app. It skips config file loading and env var resolution entirely. Values come only from CLI tokens, declared defaults, and infrastructure roots.

## Schema Dump

Every strictcli app automatically supports `--dump-schema`, a reserved flag
that writes a JSON file describing the full CLI structure to
`.strictcli/schema.json` and prints the absolute path to stdout. The schema
includes all commands, flags, args, groups, constraints, and config field
declarations:

```
$ mytool --dump-schema
.strictcli/schema.json
```

The schema includes all commands, flags, args, groups, and their metadata. It is used by tools like rlsbl to keep documentation in sync with the CLI surface.

The location is declared, never discovered: `App(schema_path="build/cli-schema.json")`,
or `App(schema_path=strictcli.RelativeToRoot("MYTOOL_HOME", "schema.json"))` for a
location under a declared infrastructure root. With neither, the framework writes
`.strictcli/schema.json` anchored at the working directory the app was
CONSTRUCTED in, so a later `chdir` cannot move the file.

## Testing

Use `app.test(argv)` to run the CLI in-process and capture output without
shelling out. The `Result` object contains `stdout`, `stderr`, `exit_code`, and
`data` (the machine payload the handler supplied through `ctx.payload()`). This
is the standard way to test strictcli apps:

```python
def test_greet():
    app = strictcli.App(name="mytool", version="0.1.0", help="test app")

    @app.command("greet", help="Say hello", effect="read_only")
    @strictcli.flag("name", type=str, presence="required", help="Who to greet")
    def greet(ctx, name):
        ctx.info(f"Hello, {name}!")

    r = app.test(["greet", "--name", "Alice"])
    assert r.exit_code == 0
    assert "Hello, Alice!" in r.stdout
```

The `Result` object contains `stdout`, `stderr`, `exit_code`, and `data` (the machine payload the handler supplied through `ctx.payload()`).

### Programmatic Invocation

Use `app.call(command_path, **kwargs)` to invoke a command in-process with
pre-typed values, bypassing CLI parsing, env var resolution, and config file
loading. This is useful for testing, automation, and composing commands
programmatically without constructing argv strings:

```python
result = app.call("deploy", target="staging", region="us-west")
```

The `command_path` is dot-separated for nested commands: `"dns.zone.create"`. Failures raise `InvokeError`.

## Deprecated Commands

Register retired commands that print a deprecation message to stderr and exit
with code 1. Deprecated commands appear in help output under a `Deprecated:`
section, giving users visibility into the migration path:

```python
app.deprecate("old-deploy", message="Use 'deploy' instead. See https://example.com/migration")
```

Deprecated commands appear in help under a `Deprecated:` section.

## Passthrough Commands

Passthrough commands bypass all flag and argument parsing and forward raw args
directly to the handler. They are useful for wrapping external tools where the
argument format is not known in advance. Passthrough commands cannot have flags,
args, or flag sets, and declare nothing a choice flag could scope:

```python
@app.command("exec", help="Execute a command", effect="mutating",
             passthrough=strictcli.Passthrough(
    handler=lambda ctx, name, args, globals: (
        ctx.info(f"Running: {name} {args}") or 0
    ),
))
def exec_placeholder():
    pass  # handler is in the Passthrough object
```

The passthrough handler receives `(ctx, name, args, globals)` where `args` is the raw list of tokens and `globals` is a dict of global flag values.

A passthrough command is classified like any other command, and may declare
itself `consequential`. Because its args are forwarded to the child
byte-for-byte, the reserved quartet is not scanned after the passthrough
command's name: `mytool exec deploy --dry-run` passes `--dry-run` to the child.

## Error Handling

strictcli distinguishes between two kinds of errors, each handled differently.
Registration-time errors are programmer mistakes caught at startup, while
parse-time errors are user input mistakes caught during command-line parsing.
Both produce specific, actionable messages:

- **Registration-time errors** (`ValueError`): raised when declaring apps, commands, flags, or args with invalid configuration (missing help text, banned flag names, type mismatches). These are programmer errors caught at startup.
- **Parse-time errors**: printed to stderr and exit 1. Include unknown flags, missing required values, type coercion failures, election and scope violations on a choice flag, and constraint violations.

```
$ mytool deploy --unknown-flag
error: unknown flag '--unknown-flag'
try 'mytool deploy --help'

$ mytool deploy
error: flag '--target' is required
try 'mytool deploy --help'
```

## Full Example

```python validate
import strictcli

app = strictcli.App(
    name="deploy-tool",
    version="0.1.0",
    help="Deployment management tool",
    config=True,
    env_prefix="DEPLOY",
    flags=[
        strictcli.Flag(name="color", type=bool, default=True, help="Colorize output"),
    ],
)

@app.command("status", help="Show deployment status", effect="read_only",
             payload_schema={"type": "object"})
@strictcli.flag("environment", short="e", type=str, default="production",
                choices=[strictcli.Choice("production"), strictcli.Choice("staging"),
                         strictcli.Choice("dev")],
                help="Target environment")
def status(ctx, color, environment):
    ctx.debug(f"Checking status for environment: {environment}")
    ctx.payload({
        "environment": environment,
        "status": "healthy",
    })
    return strictcli.outcome(exit_code=0)

svc = app.group("service", help="Service management")

@svc.command("restart", help="Restart a service", effect="mutating", consequential=True)
@strictcli.flag("name", type=str, presence="required", help="Service name")
@strictcli.flag("timeout", type=int, presence="optional",
                help="Shutdown timeout in seconds; the handler uses 30 when it is not supplied")
def restart(ctx, color, name, timeout):
    seconds = 30 if timeout is None else timeout
    ctx.info(f"Restarting {name} (timeout: {seconds}s)")

app.run()
```

`restart` is `mutating`, so `--timeout` cannot declare `default=30`: it declares
`presence="optional"` and the handler applies the fallback, which the flag's help
states. `status` is `read_only`, which is why its `--environment` keeps
`default="production"`, and `--color` is an app-level global, which the ban does
not reach. See
[A mutating command may not default a value](flag-system.md#a-mutating-command-may-not-default-a-value).

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
