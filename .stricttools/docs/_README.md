+++
title = "README.md"
+++
# strictcli

A CLI framework for the Era of Agents: nothing is inferred, everything is declared. First-class support for Go, Python, and TypeScript

It is for developers who would rather see a mistake when the CLI is declared than when someone runs it. The three implementations are first-class rather than ports of one another, and one shared conformance test suite holds them to identical behavior:

| Implementation | Install | Docs |
|---------------|---------|------|
| **Python** | `pip install strictcli` | [python/README.md](python/README.md) |
| **Go** | `go get github.com/stricttools/strictcli/go/strictcli` | [go/](go/) |
| **TypeScript** | `npm install strictcli` | [typescript/README.md](typescript/README.md) |

## Build in your language, not in a shared subset

Pick whichever of the three you want to work in and write it the way that
language is written. Python gets decorators, keyword arguments and dataclasses.
Go gets functional options and compile-time option shapes. TypeScript gets
discriminated unions and full type inference from the flag declaration through to
the handler's argument types. The three surfaces are deliberately different, and
none of them is a transliteration of another:

```python
@strictcli.flag("target", type=str, presence="required", help="Where to deploy")
```
```go
strictcli.StringFlag("target", "Where to deploy", strictcli.Required())
```
```ts
flag("target", t.str, { help: "Where to deploy", presence: "required" })
```

What the three hold identical is **behavior** -- the same semantics for every
declaration, the same help bytes, the same schema, and the same error sentence
with your language's own spellings inside it. You are not handed a
lowest-common-denominator API so that three implementations can stay in step.
You are handed your language's best form, enforced strictly: what strictcli
makes mandatory, it makes mandatory in the idiom you already write. See
[.stricttools/docs/language-idioms.md](.stricttools/docs/language-idioms.md).

## Philosophy

Most CLI frameworks infer behavior from type hints, function signatures, or naming conventions. strictcli does the opposite: every flag, argument, command, and help string is declared explicitly. If something is missing, you get an error at registration time, not a confusing runtime surprise.

- **Four types only.** `str`, `bool`, `int`, `float` -- no magic type coercion. NaN and Inf are rejected.
- **Mandatory help text.** Every flag, arg, command, and group must have help text.
- **Mandatory effect classification.** Every command declares `read_only` or `mutating`. There is no default and no inference from names, tags or handler bodies.
- **Mandatory presence declaration.** Every flag and every positional argument declares exactly one of required, optional, or a default value. Declaring none or two is a registration-time error, and nothing about presence is derived from the shape of another declaration.
- **A choice is a declaration scope.** "Exactly one of these" is a choice flag, not a constraint over independent flags: each choice owns the flags that exist only while it is elected, and a flag supplied outside its elected scope is a parse error naming both sides. There is no at-most-one construct anywhere.
- **Handler signature validation.** Parameter names must match declared flags and args exactly.
- **Registration-time errors.** Misconfigurations fail loud and early, not at parse time.
- **Minimal dependencies.** Each implementation uses its language's standard library plus TOML support: Python depends on [tomlkit](https://pypi.org/project/tomlkit/), Go depends on [go-toml-edit](https://github.com/smm-h/go-toml-edit), TypeScript depends on [smol-toml](https://www.npmjs.com/package/smol-toml) and [toml-eslint-parser](https://www.npmjs.com/package/toml-eslint-parser).

## Quick taste

### Python

```python validate
import strictcli

app = strictcli.App("greet", version="1.0.0", help="A greeting app")

@app.command("hello", help="Say hello", effect="read_only")
@strictcli.flag("name", type=str, presence="required", help="Who to greet")
@strictcli.flag("loud", type=bool, default=False, help="Shout it")
def hello(ctx, name, loud):
    msg = f"Hello, {name}!"
    ctx.info(msg.upper() if loud else msg)

app.run()
```

### Go

```go validate
package main

import (
    "strings"

    "github.com/stricttools/strictcli/go/strictcli"
)

func main() {
    app := strictcli.NewApp("greet", "1.0.0", "A greeting app")

    app.Command("hello", "Say hello",
        func(ctx *strictcli.Context, args map[string]interface{}) strictcli.Outcome {
            name := strictcli.Get[string](args, "name")
            loud := strictcli.Get[bool](args, "loud")
            msg := "Hello, " + name + "!"
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

### TypeScript

```ts validate
import { createApp, defineReadOnlyCommand, flag, t } from "strictcli";

const app = createApp({
    name: "greet",
    version: "1.0.0",
    help: "A greeting app",
});

app.command(
    defineReadOnlyCommand("hello", {
        help: "Say hello",
        flags: {
            name: flag("name", t.str, { help: "Who to greet", presence: "required" }),
            loud: flag("loud", t.bool, {
                help: "Shout it",
                presence: "default",
                default: false,
            }),
        },
        handler: (args, ctx) => {
            // Inferred: args.name is string, args.loud is boolean
            const msg = `Hello, ${args.name}!`;
            ctx.info(args.loud ? msg.toUpperCase() : msg);
            return 0;
        },
    }),
);

app.run(process.argv.slice(2));
```

## Features

- Commands and command groups (recursive nesting to arbitrary depth)
- Deprecated commands -- register retired commands that print a message and exit 1, shown in help under a `Deprecated:` section
- Flags: string, boolean (with `--no-` negation), integer, float (NaN/Inf rejected)
- Short flag aliases (`-o` for `--output`)
- Positional arguments -- the same three-way presence declaration as flags (required, optional, or a default), plus variadic collection
- The presence declaration -- `presence="required"` / `presence="optional"` / `default=<value>` (Python), `Required()` / `Optional()` / `Default(v)` (Go), `{presence:"required"}` / `{presence:"optional"}` / `{presence:"default", default: v}` (TypeScript); an optional declaration delivers `None`/`nil`/`undefined` as a present key, and optional bools are a real tri-state
- `ctx.provided(name)` / `ctx.Provided(name)` -- whether the invocation caused a value, rather than the declaration
- Environment variable binding with prefix enforcement
- Flag tags -- reusable bundles of flags shared across commands
- Choice flags -- elect exactly one of a flag's declared choices, each choice owning a scope of flags legal only while it is elected, spelled as a token (`--via email`) or as the choices' own flags (`--profile work` / `--all-profiles`); the handler receives one tagged record consumed exhaustively, and recursion is unlimited
- Constraints -- four named rules over a command's own flags and args: at-least-one, all-or-none, requires, and implies (auto-set a bool flag when another is provided; explicit contradictions are parse errors). A member is a reference by name to a flag, a positional arg, or another named constraint, nested to unlimited depth, carrying a declared election selector (`present` / `true` / `non_empty`) that says when it counts; every constraint renders in `--help`, publishes its members in `--dump-schema`, and projects into MCP tool schemas with any remainder stated in the tool description
- Update commands -- a command declares what it changes (`update_of=UpdateOf(...)` / `WithUpdateOf(...)` / `updateOf: {...}`): the resource, a mandatory sparse-or-full-replace write mode, the flags and args that identify the instance, and the properties that carry the changes. Absence means untouched, so no flag or arg on a `mutating` command may declare a value default; the framework enforces at least one property per invocation, renders the write set on every surface a run reports through (one line in the would-do log, a `writes` member on the machine envelope), and a `nullable` property mints `--unset-<prop>` answered by `ctx.unset(name)`
- Global flags (parsed before and after the command token)
- Passthrough commands -- delegate unparsed args to another tool
- Repeatable flags (accumulate values into a list)
- Choices -- restrict flag values to an allowed set, one value-plus-optional-help record per entry, rendered as a block once any entry carries help
- Custom validation functions per flag
- Auto-generated help at every level (app, group, command)
- Built-in `--version` / `-v` support
- Auto-version detection from package metadata (Python only)
- Config file support (JSON or TOML) -- reads `~/.config/{name}/config.json` (or `.toml`), auto-registers `config show/set/path/edit/init` subcommands, where `config set <key> --value <v>` writes under a required selector over a value, a clear and a reset to the declared default. Precedence: CLI > env > config > default.
- Mandatory effect classification -- every command declares `read_only` or `mutating` (`effect=` in Python, `WithEffect(...)` in Go, the twin factories `defineReadOnlyCommand` / `defineMutatingCommand` in TypeScript)
- The effects regime -- `ctx.effects` mints recorded operations (`run`, `spawn`, `write`, `mkdir`, `remove`, `rename`, `chmod`, `http`); under `--dry-run` they are recorded rather than performed and rendered as a would-do log
- The reserved flag quartet -- `--dry-run`, `--approve-consequential`, `--quiet` and `--verbose` are framework-owned names, banned at every declaration level and delivered on the handler context
- `dry_run_supported` -- a command whose preview would lie declares the refusal with a mandatory reason, and `--dry-run` is rejected at parse time instead
- `consequential` -- the per-command declaration that makes the framework prompt before dispatch; `--approve-consequential` answers it in advance, and a non-interactive stdin without it is a hard error
- `--hermetic` -- reserved global flag that skips config loading and env var resolution entirely, so values come only from the CLI and declared defaults
- Infrastructure env vars -- declared location roots (resolved at construction, usable in defaults via `RelativeToRoot`), handshake vars (cross-tool protocol signals, read live), and connection vars (behavioral URLs like a database DSN: read live, no default, and hermetic-suppressed so `--hermetic` resolves them absent; flags bind to a declared connection env, and checks can read it via the check context)
- Value provenance -- every resolved flag reports its source (`cli`/`env`/`config`/`default`/`implied`/`infra`) via the handler context
- Programmatic invocation -- `app.call()` / `app.Call()` runs a command in-process with typed kwargs, bypassing CLI parsing; failures surface as `InvokeError`
- Check system -- first-class check/validation framework with a TOML manifest, tag DSL, and DAG-ordered execution
- MCP server mode -- expose commands as tools over the Model Context Protocol (protocol `2026-07-28`, with the handshake era retained), where a consequential tool asks for confirmation before it runs
- `--dump-schema` -- auto-injected flag that writes `.strictcli/schema.json` at `schema_version: 2` describing the full CLI structure, with a real JSON Schema fragment on every flag and arg entry and one canonical encoding so the three implementations' dumps byte-compare
- `--help` / `-h` recognized anywhere in argv
- In-process testing via `app.test()` / `app.Test()`

## Conformance

The `conformance/` directory contains a cross-language test suite that verifies all implementations (Python, Go, TypeScript) produce identical output for identical inputs. It includes:

- A shared JSON case suite covering every feature, run against each target via `run.py --target python` / `--target go` / `--target typescript`
- API surface verification (`check_api_surface.py`)
- Error message parity checks (`check_error_parity.py`)
- Byte-identical schema dump parity (`check_schema_parity.py`), fragment validity (`check_schema_fragments.py`) and float formatting fuzzing (`check_float_fuzz.py`)
- Pairwise combination testing and fuzzing

All implementations must pass all conformance tests before release.

## Project structure

```
strictcli/
  python/          Python implementation (PyPI)
  go/              Go implementation
  typescript/      TypeScript implementation (npm)
  conformance/     Cross-language conformance tests
```

Each sub-project has its own version, changelog, and release cycle, managed by [rlsbl](https://github.com/smm-h/rlsbl).

## License

MIT
