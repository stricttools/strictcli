# strictcli

A CLI framework for the Era of Agents: nothing is inferred, everything is declared. First-class support for Go, Python, and TypeScript (Python implementation).

strictcli makes you declare everything -- every command, flag, argument, and environment variable must have help text or the framework errors at registration time. Four types only: `str`, `bool`, `int`, `float`. No magic type inference, no implicit defaults.

There are Go and TypeScript implementations too, and this one is not a port of
either. The surface here is Python's own -- decorators, keyword arguments, a
`Flag` dataclass, `ValueError` at registration -- and some of the enforcement
exists only in Python, because only Python can see it (a handler parameter
bound to an optional flag must default to `None`, since anything else
re-introduces the sentinel the declaration removed). What the three
implementations hold identical is behavior: the same semantics, the same help
bytes, the same schema, and the same error sentence with Python's spellings
inside it.

## Installation

```
pip install strictcli
```

Or with uv:

```
uv add strictcli
```

Requires Python 3.11+. One runtime dependency: [tomlkit](https://pypi.org/project/tomlkit/), for comment-preserving TOML config support.

## Quickstart

```python validate
import strictcli

app = strictcli.App("greet", version="1.0.0", help="A greeting app")

@app.command("hello", help="Say hello", effect="read_only")
@strictcli.flag("name", type=str, help="Who to greet", presence="required")
@strictcli.flag("loud", type=bool, default=False, help="Shout it")
def hello(ctx, name, loud):
    msg = f"Hello, {name}!"
    ctx.info(msg.upper() if loud else msg)

app.run()
```

Every handler is **ctx-first**: the framework injects a `Context` as the first
positional argument, and flag and arg values follow as keyword arguments. Every
command declares its `effect` -- `"read_only"` or `"mutating"` -- and the
declaration is mandatory.

```
$ python greet.py hello --name World
Hello, World!

$ python greet.py hello --name World --loud
HELLO, WORLD!

$ python greet.py hello --help
greet hello -- Say hello

Flags:
  --name <str>         Who to greet [required]
  --loud, --no-loud    Shout it [default: false]
```

## Features

### Commands and groups

Top-level commands with `@app.command`, nested groups with `app.group`. Groups nest recursively to arbitrary depth via `group.group`.

```python
db = app.group("db", help="Database operations")
schema = db.group("schema", help="Schema management")

@schema.command("migrate", help="Run migrations", effect="mutating")
def migrate(ctx):
    ctx.info("migrating")
```

Invoked as `myapp db schema migrate`.

### The presence declaration

Every flag and every positional argument declares **exactly one** of three facts about itself. Declaring none of them does not register; declaring two does not register.

| Fact | Spelling | Delivered when nothing supplies a value |
|------|----------|------------------------------------------|
| required | `presence="required"` | nothing -- the parse fails with `flag '--x' is required` |
| optional | `presence="optional"` | `None` |
| default | `default=<value>` | the declared value |

```python
@strictcli.flag("target", type=str, help="Deploy target", presence="required")
@strictcli.flag("note", type=str, help="A note", presence="optional")
@strictcli.flag("retries", type=int, help="Retry count", default=3)
```

Nothing about presence is inferred. A bool with no declaration is not "false by default", a `list[T]` with no declaration is not "empty by default" -- an empty collection is declared with `default=[]` or `default={}` -- and `default=None` is not a spelling of optionality: it is refused at registration with a redirect to `presence="optional"`, which is what delivers `None`.

Requiredness is satisfied by any source that supplies a value: a command-line token, an environment variable, a config file, or an `Implies` injection. An optional flag makes real tri-state bools possible (`--x` true, `--no-x` false, absent absent), and it makes `""` a value again instead of an absence sentinel.

`ctx.provided(name)` answers whether the **invocation** caused a value: true for `cli`, `env`, `config` and `implied`, false for `default` and `infra`. `ctx.source(name)` still answers the narrower question of which origin it was.

A handler parameter bound to an optional flag or argument must default to `None` if it defaults to anything, so the sentinel the declaration removed cannot come back one line later. A choice flag declares `required` or a default and never `optional` -- an absent selection is a choice nobody named, so it is named as a choice of its own.

### Four flag types

`str`, `bool`, `int`, and `float`. No magic coercion -- parse errors are clear and immediate.

```python
@strictcli.flag("port", type=int, help="Port number", presence="required")
@strictcli.flag("threshold", type=float, help="Score threshold", presence="optional")
@strictcli.flag("cache", type=bool, default=True, help="Reuse the build cache")
@strictcli.flag("output", type=str, help="Output path", default="out.txt")
```

Bool flags support `--flag` / `--no-flag` negation (disable with `negatable=False`). A bool declared `presence="required"` must be passed as `--flag` or `--no-flag`; one declared `presence="optional"` is real tri-state. Float parsing rejects NaN and Inf.

### Compound types

`list[T]` and `dict[str, T]` for collecting multiple values.

```python
@strictcli.flag("tags", type=list[str], help="Tags to apply", unique=True, default=[])
@strictcli.flag("env", type=dict[str, str], help="Environment variables", default={})
```

List flags accept `--tags a --tags b`. Dict flags accept `--env KEY=VALUE` pairs or JSON objects.

### Positional arguments

Two equivalent declaration forms. Every argument declares its presence exactly as a flag does: `presence="required"`, `presence="optional"`, or a `default=`. A variadic argument always delivers a list, so it declares `required` (at least one value) or `optional` (possibly none) and never a default.

```python
# Decorator form
@app.command("show", help="Show a file", effect="read_only")
@strictcli.arg("path", help="File to show", presence="required")
def show(ctx, path): ...

# Inline form
@app.command("copy", help="Copy files", effect="mutating", args=[
    strictcli.Arg(name="src", help="Source", presence="required"),
    strictcli.Arg(name="dst", help="Destination", presence="required"),
])
def copy(ctx, src, dst): ...
```

### Short flag aliases

Single-character shortcuts for any flag.

```python
@strictcli.flag("recursive", short="r", type=bool, default=False, help="Recurse into subdirectories")
@strictcli.flag("output", short="o", type=str, help="Output path", default=".")
```

### Environment variable binding

Flags can be backed by environment variables. Prefix enforcement keeps your config namespace clean.

```python
app = strictcli.App("myapp", version="1.0.0", help="My app", env_prefix="MYAPP")

@strictcli.flag("region", type=str, help="Cloud region", env="MYAPP_REGION", default="us-east-1")
```

All env vars must start with the declared prefix. Use `prefixed=False` for external env vars like `GITHUB_TOKEN`. Precedence: CLI > env > config > default.

Bool env vars accept `1|true|yes` / `0|false|no` (case-insensitive).

### FlagSets

Reusable bundles of flags shared across commands.

```python
auth_flags = strictcli.FlagSet(
    name="auth",
    flags=[
        strictcli.Flag(name="token", type=str, help="Auth token", presence="required"),
        strictcli.Flag(name="insecure", type=bool, presence="optional", help="Skip TLS verification"),
    ],
)

@app.command("deploy", help="Deploy", effect="mutating", flag_sets=[auth_flags])
def deploy(ctx, token, insecure): ...
```

### Choice flags: a choice is a declaration scope

A **choice flag** elects exactly one of its declared choices, and each choice
owns the flags that exist only while it is elected. A flag supplied outside its
elected scope is a distinct parse error naming both sides -- never "unknown
flag" -- and the elected value reaches the handler as one tagged record that
`match` consumes exhaustively.

```python
@strictcli.choice("email", help="deliver the notification as an email message")
class Email:
    subject: str = strictcli.sub_flag(help="subject line", presence="required")
    recipient: str = strictcli.sub_flag(help="destination address", presence="required")


@strictcli.choice("sms", help="deliver the notification as a text message")
class Sms:
    phone_number: str = strictcli.sub_flag(help="destination number", presence="required")


@app.command("send", help="Send one notification", effect="mutating")
@strictcli.choice_flag("via", help="Delivery channel", short="v", presence="required",
                       elect_by="selector-token", choices=[Email, Sms])
def send(ctx, via: Email | Sms):
    match via:
        case Email(subject=subject, recipient=recipient): ...
        case Sms(phone_number=number): ...
        case _: assert_never(via)
```

`notify send --via email --subject hi` parses; `notify send --via sms --subject hi`
says *`--subject` is only valid under `--via email`*. Order is irrelevant --
nothing is interpreted until every token is collected -- and a choice flag may be
declared inside a choice's scope to any depth (`strictcli.sub_choice_flag`).

`elect_by` is mandatory and has no default. `elect_by="member-flags"` spells each
choice as its own flag instead (`--profile work` / `--all-profiles`), with no
selector token ever typed; a member's payload is declared
`value: str = strictcli.member_value(help=...)`.

Scoped flags are never top-level handler arguments, at any depth, so every
declared top-level key is still always present. Inside the record,
`strictcli.provided(via, "subject")` answers whether the invocation caused a
field's value.

### Constraints

Four relationship types, all passed via `constraints=[...]`, and every one of
them declares a **mandatory name** -- the name is what a violation prints and
what `--help` shows, and it is what lets one constraint be a member of another.

- `AtLeastOne(name, members)` -- at least one member is engaged. Members may
  co-occur; it has no upper bound and is never exclusivity
- `AllOrNone(name, members)` -- either every member is engaged or none is.
  Nothing engaged is vacuously satisfied
- `Requires(name, flag=..., depends_on=...)` -- one-way dependency
- `Implies(name, flag=..., implies=..., value=...)` -- auto-set a bool flag when
  another is provided; explicit contradictions are parse errors

A **member** is a `Member(name, when=...)` record naming a command flag, a
positional arg, or another named at-least-one or all-or-none (nesting is a
cycle-checked DAG). A bare string is refused. `when` is the closed election
vocabulary -- `"present"` (the default; the value was provided by the
invocation, never by a declared default), `"true"` (bool only), `"non_empty"`
(strings and collections) -- and a **bool member must declare it explicitly**,
so `--no-all` can never engage a constraint while selecting nothing.

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
@strictcli.flag("old-name", type=str, presence="optional", help="Current name")
@strictcli.flag("new-name", type=str, presence="optional", help="New name")
@strictcli.flag("old-email", type=str, presence="optional", help="Current email")
@strictcli.flag("new-email", type=str, presence="optional", help="New email")
def rewrite(ctx, old_name, new_name, old_email, new_email): ...
```

```
Constraints:
  author-name      all or none of --old-name, --new-name
  author-email     all or none of --old-email, --new-email
  author-change    at least one of (--old-name with --new-name), (--old-email with --new-email)
```

Children are evaluated before parents, so an operator who typed one half of a
pair is told the pair is incomplete rather than that the whole selection is
missing. No member of a co-occurrence constraint may declare
`presence="required"` -- a member the invocation must always supply leaves the
constraint nothing to decide.

Constraints can only reference flags and args you declared, and they operate at
root scope only, so the reserved quartet (`dry-run`, `approve-consequential`,
`quiet`, `verbose`) can never appear in one and a scoped flag is a registration
error.

### Update commands

A command that changes some properties of one resource and leaves the rest alone
declares what it updates with `update_of=`. `UpdateOf` is a frozen, keyword-only
record whose first field is the resource name:

```python
@app.command("update-record", help="Change one DNS record in place", effect="mutating",
             update_of=strictcli.UpdateOf("dns-record", write_mode="sparse",
                                          identity=["zone", "record-id"],
                                          properties=["content", "ttl", "proxied"]))
@strictcli.flag("zone", type=str, presence="required", help="Zone the record belongs to")
@strictcli.flag("record-id", type=str, presence="required", help="Identifier of the record")
@strictcli.flag("content", type=str, presence="optional", help="Record content")
@strictcli.flag("ttl", type=int, presence="optional", nullable=True, help="Time to live in seconds")
@strictcli.flag("proxied", type=bool, presence="optional", help="Whether the record is proxied")
def update_record(ctx, zone, record_id, content, ttl, proxied): ...
```

`write_mode` is `"sparse"` or `"full_replace"` and carries no default. A property
declares `presence="optional"` and nothing else -- absence *is* untouched -- and
the framework refuses an invocation that supplies none of them:

```
error: update "dns-record": at least one property is required: --content, --ttl, --proxied
```

Because absence must never resolve to a value nobody stated, **no flag or arg on
a `mutating` command may declare a value default** (`default=[]` and `default={}`
stay legal, as does every default on a `read_only` command). Inside an update
`--no-proxied` writes `False`; a `nullable` property mints `--unset-<prop>`,
answered by `ctx.unset(name)`. Every run that reports what it does renders the
write set -- one unnumbered line in the would-do log, and a `writes` member on
the machine envelope:

```
$ mytool --dry-run update-record --zone z1 --record-id r7 --content hi --unset-ttl
DRY RUN — no changes were made. Would do:
  writes: content; clears: ttl (other properties unchanged)
  1. net: PATCH https://api.example.com/zones/z1/dns_records/r7
```

### Global flags

App-level flags available to all commands, parsed before and after the command token.

```python
app = strictcli.App("myapp", version="1.0.0", help="My app", flags=[
    strictcli.Flag(name="color", type=bool, default=True, help="Colorize output"),
])
```

Global flag names cannot collide with the framework's reserved names (`help`,
`h`, `version`, `v`, `dump-schema`, `mcp`, `config`, `hermetic`) or with the
reserved quartet.

### Passthrough commands

Bypass all parsing -- handler gets raw args plus global flag values.

```python
@app.command("run", help="Run a script", effect="mutating", passthrough=True)
def run(ctx, args, color):
    ctx.effects.run(args)
```

The reserved quartet is not scanned after a passthrough command's name: its args
are forwarded to the child byte-for-byte, so `myapp run deploy --dry-run` passes
`--dry-run` to the child.

### Repeatable flags

Flags that accumulate values across multiple occurrences. Requires explicit `unique=True` or `unique=False`.

```python
@strictcli.flag("tag", type=str, help="Add a tag", repeatable=True, unique=True, default=[])
```

### Choices

Restrict flag values to an allowed set. Every entry is a record, and its help is
optional:

```python
@strictcli.flag("format", type=str, help="Output format", presence="required",
                choices=[strictcli.Choice("json", help="one JSON document"),
                         strictcli.Choice("csv"),
                         strictcli.Choice("xml")])
```

Help renders on one line (`[choices: json, csv, xml]`) until an entry carries
help, at which point the whole flag renders as an indented block.

The boundary against a choice flag is structural, not a matter of taste: **need a
scope or member spelling -> choice flag; a plain constrained value -> choices.**

### Custom validation

Per-flag validation functions.

```python
@strictcli.flag("port", type=int, help="Port number", validate=lambda v: 1 <= v <= 65535, presence="required")
```

### Deprecated commands

Register retired commands that print a message to stderr and exit 1.

```python
app.deprecate("init", message="Use 'setup' instead")
db.deprecate("reset", message="Use 'db wipe' instead")
```

Deprecated commands appear in help output under a `Deprecated:` section.

### Hidden commands and groups

Commands and groups can be hidden from help output while remaining functional.

```python
@app.command("internal-debug", help="Debug internals", effect="read_only", hidden=True)
def internal_debug(ctx): ...
```

### JSON config file support

Reads `~/.config/{name}/config.json` (or TOML). Auto-registers `config show/set/path/edit` subcommands, where `config set <key> --value <v>` writes under a required selector over a value, a clear (`--clear`) and a reset to the declared default (`--default`).

```python
app = strictcli.App("myapp", version="1.0.0", help="My app", config=True)
```

Precedence: CLI > env > config > default. Config fields can be declared with typed validation:

```python
app.config_field("serve.port", type=int, help="Server port", default=8080)
```

### The effects regime

Every command declares `effect="read_only"` or `effect="mutating"` -- there is no
default and no inference. A read-only command changes nothing and calling a
mutating member of `ctx.effects` from one is a hard error at call time. A
mutating command participates in `--dry-run`, where the eight recorded
operations (`run`, `spawn`, `write`, `mkdir`, `remove`, `rename`, `chmod`,
`http`) are recorded rather than performed and rendered as a would-do log.

Four flag names are owned by the framework and cannot be declared at any level
(app flags, command flags, flag sets, and flags declared inside a choice's
scope at any depth). They arrive on the context,
never as handler kwargs:

| Flag | Context property |
|------|-----------------|
| `--dry-run` | `ctx.dry_run` |
| `--approve-consequential` | `ctx.approve_consequential` |
| `--quiet` | `ctx.quiet` |
| `--verbose` | `ctx.verbose` |

A flag named `yes` is banned outright -- the confirmation skip is
`--approve-consequential`.

A command whose preview would lie declares the refusal instead of rendering one:

```python
@app.command("migrate", help="Run migrations", effect="mutating",
             dry_run_supported=False,
             dry_run_unsupported_reason="each migration reads the schema the previous one wrote")
def migrate(ctx): ...
```

`--dry-run` is then refused at parse time with the reason, which also appears in
the command's help under a `Dry run:` section and in the schema.

### Consequential commands

`consequential=True` is the only thing that makes the framework prompt -- a plain
mutating command never does. Classification answers "should a dry run record
this?"; `consequential` answers "are these effects worth interrupting someone
for?"

```python
@app.command("destroy", help="Destroy the cluster", effect="mutating", consequential=True)
def destroy(ctx): ...
```

Before dispatch the framework prints `about to run consequential command
'destroy'. Proceed? [y/N] ` to stderr and reads one line from stdin; only `y` or
`Y` proceeds. `--approve-consequential` answers in advance, and `--dry-run`
skips the prompt because nothing is being performed. A non-interactive stdin
without either flag is a hard error rather than a hang. Declaring
`consequential=True` on a read-only command raises `ValueError`.

### Schema dump

`--dump-schema` is auto-injected on every app. Writes `.strictcli/schema.json` at `schema_version: 2` describing the full CLI structure (commands, flags, args, groups, checks). Every command entry carries its `effect`; `consequential`, `dry_run_supported` and `dry_run_unsupported_reason` are emitted only when declared.

Every flag and arg entry carries a `value_schema`: a real JSON Schema fragment from a closed subset of `type`, `items`, `additionalProperties` and `enum`, using JSON Schema's own type names. Arity is part of the value's shape, so a repeatable scalar flag and a `list[T]` flag publish the identical array fragment. A choice flag carries no fragment -- its value is a variant the subset cannot express -- and publishes its nested `choices` and scopes instead, each scoped entry a full flag entry, with `elect_by` marking the spelling. A value flag's `choices=` splits in two: the values as an `enum` inside the fragment, and the value-plus-help records beside it under `choices`. Keys are emitted in a declared order at every depth and the document is written in one canonical encoding, so a schema file written by this implementation and one written by the Go or TypeScript implementation for the same declaration are byte-identical.

### Check system

First-class check/validation framework with double-entry security. Enabled via `checks_path=` pointing to a TOML file.

```python
app = strictcli.App("myapp", version="1.0.0", help="My app", checks_path="checks.toml")

@app.error_check("lint")
def lint(ctx, reporter: strictcli.ErrorReporter):
    if problems := find_problems():
        for p in problems:
            reporter.error(p)
        return reporter.found("lint problems found")
    return reporter.passed("All good")
```

Checks are declared in TOML and registered in code -- both must agree. The
registration form must match the declared severity: `@app.error_check` for
`severity = "error"` (its reporter has `error` and `warn`), `@app.warn_check`
for `severity = "warn"` (its reporter structurally lacks `error`, so a warn
check cannot cascade). An outcome is minted only through a reporter method --
`passed(message)`, `skipped(reason)`, or `found(message)` after accumulating
problems; `reporter.note(text)` records verdict-inert informational notes.
Auto-registers a `check` command with tag DSL filtering
(`--tag "release & !slow"`), JSON output, and dependency resolution.

### Auto-version

`App(name="x", help="...")` without an explicit `version` auto-detects from `importlib.metadata`.

### Tool export

`app.as_tools()` exports non-hidden, non-interactive commands as `Tool` descriptors for LLM agents.

```python
tools = app.as_tools()
# Each Tool has: name, description, parameters (JSON Schema), effect,
# consequential, execute
```

`effect` and `consequential` publish the effects-regime classification beside
the argument schema, so a caller can see before it calls that a tool changes
things and that invoking it requires stating consent:

```python
release = next(t for t in tools if t.name == "release")
release.consequential            # True
await release.execute()          # InvokeError: ... the call must carry confirmation
await release.execute(approve_consequential=True)   # proceeds
```

### MCP server

`app.serve_mcp()` runs a JSON-RPC 2.0 MCP server on stdin/stdout, exposing commands as tools for AI clients. Triggered via `--mcp` flag.

### Help and version

- `--help` / `-h` recognized anywhere in argv, at app, group, and command levels
- `--version` / `-v` prints app version
- Help is auto-generated with flag types, defaults, env var names, and choices

## Testing

`app.test(argv)` runs the CLI in-process and returns a `Result`:

```python
result = app.test(["hello", "--name", "World", "--loud"])

assert result.exit_code == 0
assert "HELLO, WORLD!" in result.stdout
assert result.stderr == ""
```

The confirm protocol never fires on `test()` or `call()` -- the programmatic
paths have no TTY contract, so a consequential command is dispatched directly.

## API reference

### Core types

| Type | Description |
|------|-------------|
| `App` | Root CLI application |
| `Flag` | Flag declaration |
| `Arg` | Positional argument |
| `FlagSet` | Reusable flag bundle |
| `Choice` | One entry of a `choices=` value flag: a value with optional help |
| `AtLeastOne` | At least one member must be engaged |
| `AllOrNone` | Every member is engaged, or none is |
| `Requires` | One flag depends on another |
| `Implies` | Auto-set a bool flag from another |
| `Member` | One operand of a co-occurrence constraint |
| `Result` | Return type of `app.test()` |
| `Tool` | LLM tool descriptor |
| `CheckRunResult` | Check execution result with wall-clock timing |
| `CheckContext` | Protocol for check context |
| `ErrorReporter` / `WarnReporter` | Problem accumulators passed to check handlers |
| `ConfigField` | Typed config file field |

### Decorators

| Decorator | Description |
|-----------|-------------|
| `@app.command(name, help=..., effect=...)` | Register a command (`effect` is mandatory) |
| `@strictcli.flag(name, type=, help=..., presence=/default=)` | Declare a flag (presence is mandatory) |
| `@strictcli.arg(name, help=..., presence=/default=)` | Declare a positional argument (presence is mandatory) |
| `@strictcli.choice_flag(name, help=..., choices=, elect_by=, presence=/default=)` | Declare a choice flag (`elect_by` is mandatory) |
| `@strictcli.choice(name, help=...)` | Declare one choice of a choice flag, and its scope |
| `strictcli.sub_flag(help=..., presence=/default=, ...)` | Declare one flag of a choice's scope, inside the choice class body (the field name is the flag name) |
| `strictcli.sub_choice_flag(help=..., choices=, elect_by=, ...)` | Declare a nested choice flag inside a choice's scope |
| `strictcli.member_value(help=...)` | Declare a member-spelled choice's own payload, delivered under the reserved name `value` |
| `strictcli.provided(record, name)` | Whether the invocation caused a scoped field's value, asked of the delivered record |
| `@app.error_check(name)` / `@app.warn_check(name)` | Register a check handler |

### App methods

| Method | Description |
|--------|-------------|
| `app.command(name, help=..., effect=...)` | Register a command (decorator; `effect` is mandatory) |
| `app.group(name, help=...)` | Create a command group |
| `app.deprecate(name, message=...)` | Register a deprecated command |
| `app.run()` | Parse `sys.argv` and execute |
| `app.test(argv)` | Run in-process, return `Result` |
| `app.as_tools()` | Export commands as `Tool` descriptors |
| `app.serve_mcp()` | Run MCP server on stdin/stdout |
| `app.config_field(name, type=, help=...)` | Declare a typed config field |
| `app.error_check(name)` / `app.warn_check(name)` | Register a check handler (decorator) |
| `app.set_check_context(factory)` | Set the check context factory |

## Design principles

- **Help is mandatory.** Every command, flag, and argument must have help text. Missing help raises `ValueError` at registration time.
- **Four types only.** `str`, `bool`, `int`, `float` -- plus compound `list[T]` and `dict[str, T]`. No magic type coercion.
- **Handler signatures are validated.** Parameter names must match declared flags and args exactly. Extra or missing parameters raise `ValueError`.
- **Effect classification is mandatory.** Every command declares `read_only` or `mutating`. There is no default and no inference.
- **Registration-time errors.** Misconfigurations fail loud and early, not at parse time.
- **Minimal dependencies.** The standard library plus [tomlkit](https://pypi.org/project/tomlkit/) for TOML config support.

## See also

- [strictcli monorepo](https://github.com/stricttools/strictcli) -- conformance tests, Go implementation, and project documentation
- [Go implementation](https://github.com/stricttools/strictcli/tree/main/go) -- same semantics, functional options API

## License

MIT
