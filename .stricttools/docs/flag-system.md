+++
title = "Flag System"
description = "strictcli's flags and args: the three-way presence declaration, four types, bool tri-state, choices and retired choices, choice flags, named constraints, and update commands."
nav_group = "Guides"
nav_order = 3
+++

# Flag System

strictcli's flag system is strict by design. Every flag must have help text, every type must be explicit, and every default must be deliberate. This guide covers the full flag and argument system using Python examples. The Go and TypeScript implementations have identical semantics (see the quickstart guides for language-specific syntax).

## Flag types

strictcli supports exactly four scalar types: `str`, `bool`, `int`, and `float`.
There is no implicit type inference -- every flag declares its type explicitly at
registration time. Additionally, compound types `list[T]` and `dict[str, T]`
are available for repeatable and key-value flags. The type determines how values
are parsed from CLI tokens, environment variables, and config files.

```python
@app.command("report", help="summarize the last deployment", effect="read_only")
@strictcli.flag("target", type=str, presence="required", help="deployment target")
@strictcli.flag("cache", type=bool, default=True, help="include cache statistics")
@strictcli.flag("replicas", type=int, default=3, help="replicas to summarize")
@strictcli.flag("threshold", type=float, default=0.95, help="success threshold")
def report(ctx, target, cache, replicas, threshold):
    ...
```

Every flag also declares its **presence** -- one of `required`, `optional`, or a
`default` value. `presence="required"` above is that declaration; `default=`
on the other three is the same declaration in its value-carrying form. See
[The presence declaration](#the-presence-declaration) below.

Every command declares its `effect` -- `"read_only"` or `"mutating"` -- and the
declaration is mandatory. See the [Python quickstart](python-quickstart.md#command-classification)
for what classification buys. The command above is `read_only`, which is why the
three `default=` declarations are legal: on a `mutating` command **none** of them
would be, because a value the framework picked is a value the framework writes.
See [A mutating command may not default a value](#a-mutating-command-may-not-default-a-value).

### String flags

String flags (`type=str`, the default) take a value from the next token or via
`--flag=value` syntax. They support the `@-prefix` resolution system, which
allows reading values from files, stdin, or literal at-signs, making it easy to
pass large values or secrets without exposing them on the command line.

String flags support `@-prefix` resolution: `@path` reads the value from a file, `@-` reads from stdin (once per invocation), and `@@` is a literal `@` escape.

```
mytool deploy --target @config/target.txt
echo "production" | mytool deploy --target @-
mytool deploy --target @@literal-at-sign
```

### Boolean flags

Boolean flags (`type=bool`) do not take a value argument -- they are pure
presence/absence flags. `--flag` sets the value to `True`, and the
auto-generated `--no-flag` negation form sets it to `False`. The `--flag=value`
syntax is rejected as a parse error because boolean flags should never accept
an ambiguous string value.

```
mytool deploy --cache          # cache=True
mytool deploy --no-cache       # cache=False
```

### Integer flags

Integer flags (`type=int`) use strict parsing with no leading or trailing
whitespace, no leading zeros (in Go), and 64-bit signed bounds. The value comes
from the next token or `--flag=value` syntax. Negative integers like `-7` are
supported as positional arguments because tokens starting with `-` that do not
match any declared flag are treated as positional values rather than unknown
flags.

### Float flags

Float flags (`type=float`) also use strict parsing and reject NaN and Inf at
parse time to prevent invalid numeric states from reaching handlers. All three
implementations use the strictcli canonical float form (SCF), a
shortest-round-trip representation that produces identical output byte-for-byte
across Python, Go, and TypeScript. The canonical form is verified by exhaustive
bit-pattern tests committed in the conformance suite.

## The presence declaration

Every flag and every positional argument declares **exactly one** of three facts
about itself, and declares it explicitly. Nothing about presence is inferred
from the shape of another declaration:

| Fact | What it means | Python | Go | TypeScript |
|------|---------------|--------|-----|-----------|
| **required** | a value must be supplied | `presence="required"` | `Required()` | `{ presence: "required" }` |
| **optional** | absence is legal and is delivered *as* absence | `presence="optional"` | `Optional()` | `{ presence: "optional" }` |
| **default** | the framework supplies the declared value when nothing else does | `default=<value>` | `Default(<value>)` | `{ presence: "default", default: <value> }` |

```python
# Required: some source must supply it on every invocation
@strictcli.flag("target", type=str, presence="required", help="deployment target")

# Default: 3 when nothing supplies a value
@strictcli.flag("replicas", type=int, default=3, help="number of replicas")

# Optional: the handler receives None when nothing supplies a value
@strictcli.flag("tag", type=str, presence="optional", help="release tag")
```

Declaring **none** of the three is a registration-time hard error naming all
three choices:

```
Flag "y": presence is undeclared: declare exactly one of presence="required", presence="optional", or default=<value>
```

Declaring **two** is a registration-time hard error naming the two that were
supplied:

```
Flag "z": presence is declared twice: presence="required" and default=a cannot be combined; declare exactly one
```

### A null default is not optionality

`default=None` (Python), `Default(nil)` (Go) and `default: null` (TypeScript)
are registration errors that redirect to the optional spelling. Optionality has
exactly one way to be written, and the value-shaped way is refused rather than
accepted as a synonym:

```
Flag "x": default=None does not declare optionality: use presence="optional" (it delivers None when the flag is absent)
```

`presence="optional"` is what delivers `None` / `nil` / `undefined`.

### What "required" requires

A `required` declaration is satisfied by **any source that supplies a value** -- a CLI
token, a bound environment variable, a config file entry, or an `Implies`
injection. It is not a "must be typed on the command line" rule. The one
exception belongs to [member-spelled choice flags](#member-spelling), where
election is command-line-only and env and config are not consulted for a member
at all.

### What "optional" delivers

An optional flag delivers absence as a **present key**: `None` in Python, `nil`
in Go, `undefined` in TypeScript. The keyword argument is always passed -- it is never
omitted -- so a handler reads it like any other value:

```python
@app.command("publish", help="publish a build", effect="mutating")
@strictcli.flag("tag", type=str, presence="optional", help="release tag")
def publish(ctx, tag):
    if tag is None:
        ctx.info("publishing untagged")
```

In Python a handler parameter bound to an optional flag or arg must either
declare no default at all (as above) or default to `None`. Anything else --
`def publish(ctx, tag="")` -- re-introduces at the handler boundary the sentinel
the declaration just removed, and is a registration-time hard error:

```
command "publish": handler parameter 'tag' is bound to optional flag '--tag' and must default to None
```

Go and TypeScript handlers receive one kwargs map / one args object, so they
have no per-parameter default and no such check.

### Was this supplied? `ctx.provided`

`ctx.provided(name)` (Python and TypeScript) / `ctx.Provided(name)` (Go) answers
whether the **invocation** caused a value, so no handler reconstructs that
boolean out of a sentinel:

| Source | `provided` |
|--------|-----------|
| `cli`, `env`, `config`, `implied` | **true** -- the invocation caused the value |
| `default`, `infra` | **false** -- the declaration caused it |

An optional flag that received nothing carries source `default` and reports
`false`. Unknown names raise exactly as `ctx.source` does.

**Inside a choice flag's scope the answer depends on the door**, because the
three doors do not carry the same information about their own input:

| Door | A scoped field the caller supplied |
|------|------------------------------------|
| the command line | `provided` **true**, source `cli` |
| the flat machine door (an MCP `tools/call`, a flat kwargs map) | `provided` **true**, source `cli` |
| the record door (a constructed scope handed to `call()`) | `provided` **false**, source `default` |

The record door's answer is an acknowledged limitation rather than a different
rule: a scope class fills its declared defaults at construction, so a field
holding its declared default cannot be told from one the caller wrote by hand,
and the framework refuses to guess by comparing the value against the
declaration. The flat door has no such problem -- it reads the caller's own
keys, so a key it read is a value the caller wrote. Everywhere, a
`RelativeToRoot` default resolves with source `infra` and reports `false`.

### Presence in help output

Every flag and every arg renders **exactly one** presence part, and it is the
last bracketed part of the line:

| Declared | Rendered |
|----------|----------|
| required | `[required]` |
| optional | `[optional]` |
| default | `[default: <value>]` |

A declared empty collection renders `[default: []]` or `[default: {}]`, and a
required positional argument renders `[required]` like a required flag.

### Presence composed with other declarations

| Declared with | Behavior |
|---------------|----------|
| `choices` | a **declared default value** must be in `choices` at registration; absence is never matched against `choices`, so `optional` + `choices` checks nothing at registration and validates only supplied values |
| a scoped flag | declared exactly as a command-level flag is, and resolved when its scope is elected; a scope is not a presence declaration and never supplies one |
| `env` / `config` | an env- or config-supplied value satisfies a `required` declaration and makes the flag *provided*; precedence stays CLI > env > config > default |
| `validate` | runs on a supplied value only -- **never** on a declared default, and never on absence |
| `Implies` target | the injected value satisfies a `required` declaration (implication resolves before defaults) |
| `Implies` trigger | fires when the flag is *provided*; a defaulted trigger never fires from its own default |
| a constraint member | engagement reads *provided*, so a declared default never engages a member on its own; a member may not declare `required` at all (see [Constraints](#constraints)) |
| a choice flag | `required` or a `default` only; `optional` is a registration error, because an absent selection is a choice nobody named |
| a member flag | **must** declare `required`, read as *required once this member is elected*; anything else is a registration error |
| `RelativeToRoot` | the marker **is** a `default=` declaration; its value resolves at parse time with source label `infra` |

## Boolean flag semantics

Boolean flags have special behavior compared to other types, including automatic
negation form generation, required boolean semantics where callers must
explicitly pass `--flag` or `--no-flag`, a genuine tri-state under
`presence="optional"`, and strict env var parsing that accepts only a fixed set
of truthy and falsy strings.

### Automatic negation (--flag / --no-flag)

By default, every bool flag is negatable: strictcli auto-generates a
`--no-flag` counterpart at registration time. Both forms appear in help output
and both are accepted on the command line. This ensures that callers can always
explicitly set a boolean to either true or false, which is especially important
for flags with `Default(true)` where the caller needs a way to opt out.

```python
@strictcli.flag("auto-commit", type=bool, default=True, help="commit after changes")
```

This creates both `--auto-commit` (sets True) and `--no-auto-commit` (sets False). The help output displays both forms:

```
  --auto-commit, --no-auto-commit    commit after changes [default: true]
```

### Required booleans

A bool flag declaring `presence="required"` forces the caller to pass either
`--flag` or `--no-flag` explicitly, which is the mechanism for forcing explicit
intent on binary decisions (e.g., `--watch` / `--no-watch` during a release).
The error message reflects both accepted forms:

```python
@strictcli.flag("watch", type=bool, presence="required", help="watch CI after release")
```

```
flag '--watch' must be passed as --watch or --no-watch
```

### Tri-state booleans

A bool flag declaring `presence="optional"` is a genuine three-valued flag:
`--flag` is true, `--no-flag` is false, and absence is absence. This is what
retires the "use a string and treat the empty string as unset" idiom.

```python
@app.command("build", help="build the project", effect="mutating")
@strictcli.flag("cache", type=bool, presence="optional", help="reuse the build cache")
def build(ctx, cache):
    if cache is None:
        ctx.info("cache decision inherited from the profile")
```

### Non-negatable booleans

Setting `negatable=False` disables the `--no-flag` form, turning the flag into
a pure presence flag that is `True` when passed and otherwise takes whatever its
presence declaration says. This is useful for flags like `--debug` where
negation is not meaningful. A non-negatable bool declaring `presence="required"`
produces a simpler error message that only shows the positive form:

```
flag '--debug' must be passed as --debug
```

For non-bool types (`str`, `int`, `float`), the `negatable` parameter is silently ignored.

### Env var parsing for booleans

Boolean flags accept a fixed set of env var strings for parsing, validated
case-insensitively. Any string not in this set produces a parse error with a
message listing the accepted values. This strict parsing prevents ambiguous
truthy/falsy interpretations that differ across languages:

- True: `1`, `true`, `yes` (3 accepted truthy strings)
- False: `0`, `false`, `no` (3 accepted falsy strings)

## Short flags

Flags can declare a single-character short form that serves as an alias for the
long flag name. Short flags follow the same parsing rules as their long
counterparts, including type coercion, env var fallback, and config file
resolution. Short flags are shown alongside long flags in help output.

```python
@strictcli.flag("recursive", short="r", type=bool, default=False, help="recurse into subdirectories")
```

This allows `-r` as an alias for `--recursive`. Short flags follow the same parsing rules as their long counterparts. The reserved quartet has no short forms, and the quartet's ban applies to long names only -- a short flag named `v` is legal.

## Repeatable flags

A flag with `repeatable=True` can be passed multiple times on the command line,
with each occurrence appending to a list. Like every other flag, a repeatable
flag declares its own presence -- there is no silent empty-list default. They
support uniqueness enforcement via the `unique` parameter and can split env var
values using a declared separator character.

```python
# An empty list when nothing is passed -- declared, not assumed
@strictcli.flag("tag", type=str, default=[], help="tags to apply", repeatable=True, unique=False)

# Absent when nothing is passed: the handler receives None, not []
@strictcli.flag("only", type=str, presence="optional", help="restrict to these", repeatable=True, unique=False)

# At least one occurrence must arrive from some source
@strictcli.flag("host", type=str, presence="required", help="hosts to contact", repeatable=True, unique=True)
```

```
mytool cmd --tag alpha --tag beta --tag gamma
# handler receives tag=["alpha", "beta", "gamma"]
```

### Rules for repeatable flags

- A repeatable flag declares presence like any other flag. `default=[]` is an explicit, legal declaration; `presence="optional"` delivers absence rather than `[]`; `presence="required"` demands at least one occurrence from some source.
- `type=bool` is incompatible with `repeatable=True`.
- `unique` must be set explicitly to `True` or `False`. When `unique=True`, duplicate values are rejected.
- If the flag has an `env` binding, `env_separator` is required (a single character used to split the env var value into list elements).
- A non-empty default must be a list of the correct element type.

### Compound types: list[T] and dict[str, T]

Repeatable flags can also be declared via compound types, which provide a more
concise syntax. The `list[T]` form is equivalent to `type=T, repeatable=True`,
and `dict[str, T]` creates a key-value flag where each occurrence adds a pair.
The element type `T` must be `str`, `int`, or `float` -- boolean elements are
not supported in compound types.

```python
@strictcli.flag("port", type=list[int], default=[], help="ports to bind")
```

This is equivalent to `type=int, repeatable=True` (with `unique` defaulting to `False`).

Dict flags use `dict[str, T]`:

```python
@strictcli.flag("label", type=dict[str, str], default={}, help="key=value labels")
```

Each occurrence adds a key-value pair. Duplicate keys are rejected. Dict flags cannot be combined with `repeatable=True`, `unique`, `choices`, or `env_separator`.

Compound flags declare presence honestly like every other flag: `default=[]` /
`default={}` for an empty collection, `presence="optional"` to receive absence
instead, `presence="required"` to demand at least one occurrence. A declared
empty collection renders `[default: []]` / `[default: {}]` in help.

## Flag naming rules

strictcli enforces naming rules at registration time (in `Flag.__post_init__`
for Python, `validateFlagConfig` for Go and TypeScript). Violations raise
`ValueError` in Python, panic in Go, or throw a `RegistrationError` in
TypeScript, preventing the app from starting. These rules prevent ambiguous flag
names and ensure the negation namespace remains uncontaminated.

### Bare --force is banned

The flag name `force` is rejected outright because a generic force flag lets
agents and automation bypass guardrails without specifying what they are forcing.
Qualified names like `force-overwrite` or `force-delete` make the intent
explicit and auditable. Use a qualified name that describes what is being
forced:

```python
# Rejected: ValueError
@strictcli.flag("force", type=bool, default=False, help="force it")

# Accepted: qualified name
@strictcli.flag("force-overwrite", type=bool, default=False, help="overwrite existing files")
@strictcli.flag("force-delete", type=bool, default=False, help="delete without confirmation")
```

The error message: `flag 'force' is a reserved name; use a qualified name like 'force-overwrite' or 'force-delete'`.

### --no-* prefix is reserved

Flag names starting with `no-` are rejected because the `--no-` prefix is
auto-generated by the negation system for boolean flags. Allowing user-defined
flags in this namespace would create ambiguity: a flag named `no-cache` would
generate a `--no-no-cache` negation form, which is confusing. The positive name
should be used instead, and the framework generates the negation automatically.

```python
# Rejected: ValueError
@strictcli.flag("no-cache", type=bool, default=False, help="disable cache")

# Accepted: positive name, negation is auto-generated
@strictcli.flag("cache", type=bool, default=True, help="use cache")
# User passes --cache or --no-cache
```

The error message: `flag 'no-cache': names starting with 'no-' are reserved for the negation system; use a positive name instead`.

### Every boolean states its presence, like every other flag

There is no implicit default for boolean flags, and none of the three
declarations is assumed. A bool silently defaulting to `False` has never been a
possibility, and after the presence declaration neither is a bool whose author
did not say which of the three it is:

```python
# Required: user must choose explicitly, with --watch or --no-watch
@strictcli.flag("watch", type=bool, presence="required", help="watch CI after release")

# Default True: user can override with --no-auto-commit
@strictcli.flag("auto-commit", type=bool, default=True, help="commit automatically")

# Default False: user can override with --recursive
@strictcli.flag("recursive", type=bool, default=False, help="recurse into subdirectories")

# Optional: True, False, or absent -- a real tri-state
@strictcli.flag("color", type=bool, presence="optional", help="colorize output")
```

### Dash-to-underscore conversion

Flag names use dashes (`--log-file`), but handler parameters use underscores (`log_file`). The conversion is automatic. If the resulting name is a Python keyword (e.g., `global`, `class`), an underscore is appended per PEP 8 convention (`global_`, `class_`).

## Choices

Flags (and args) can restrict values to a fixed set using the `choices`
parameter. Values not in the choices list produce a parse error listing all
allowed values. Choices are validated at registration time to ensure they match
the flag's declared type.

**Every entry is a record: a value, and optional help.** The bare-value entry
(`choices=["json", "csv"]`) is refused -- an entry that may carry help and an
entry that carries none would be two spellings of one fact:

```python
@strictcli.flag("format", type=str, presence="required", help="output format",
                choices=[strictcli.Choice("json", help="one JSON document"),
                         strictcli.Choice("csv"),
                         strictcli.Choice("xml")])
```

```
Flag "format": choices entry 0 is a bare value: declare it as Choice(<value>, help=...)
```

Go spells the record `Ch("json", "one JSON document")` inside
`Choices(...)` -- `Ch("csv", "")` is how it says "no help", since it has no
optional parameters -- and TypeScript spells it
`{ value: "json", help: "one JSON document" }` / `{ value: "csv" }`.

The help an entry carries decides how the flag renders. Until one entry has
help, the flag keeps its one-line form; from the first entry that has help, the
whole flag renders as an indented block:

```
  --format <str>              output format [required]
    json                      one JSON document
    csv
    xml
  --style <str>               rendering style [choices: plain, rich] [default: plain]
```

Rules:
- Every entry is a record; a bare value is a registration error.
- An entry's help is optional, and non-empty when supplied.
- `choices` is incompatible with `type=bool`.
- All choice values must match the declared type.
- If a `default` value is declared, it must be in the choices list. This is a check on declared *values*: `presence="optional"` declares no value, so it is checked against nothing at registration and absence is never matched against `choices` at parse time.
- For repeatable flags, each individual value is validated against the choices.
- Dict flags cannot have choices.

A `choices` flag restricts one **value**. When one of the alternatives needs
flags of its own, or needs to be spelled as its own flag, the declaration is a
[choice flag](#choice-flags-a-choice-is-a-declaration-scope) instead.

## Retired choices

A declaration that carries `choices` may also declare the spellings it **used
to** accept. Each retired spelling carries the message that names its
replacement, and a value matching one is refused at parse time:

```
$ myapp convert --format xml
error: --format: value 'xml' retired: use 'json' (XML output was dropped)
```

```
$ myapp convert turbo
error: argument 'mode': value 'turbo' retired: use 'fast' (turbo was renamed)
```

This is the value-level twin of `app.deprecate(name, message=...)`, which
answers a retired **command** with `command 'old-cmd' is deprecated: <message>`
and the same exit code 1. The reason both exist is the same: a caller that
reaches for a spelling which used to work should be told what replaced it, not
handed the list the spelling is missing from. An agent reading
`invalid value 'xml', must be one of: text, json` has to guess which of the two
was meant; an agent reading `use 'json'` does not.

The refusal is checked **before** the invalid-value check, and it reaches every
source the choices check already reaches -- a command-line token, an env var, a
config file value, and the programmatic doors. Nothing new is resolved for it.

Here is one declaration, in each language's own shape.

**Python** -- a keyword on `@flag` / `@arg`, taking records beside the
`Choice` records `choices` takes:

```python
@strictcli.flag("format", type=str, presence="required", help="output format",
                choices=[strictcli.Choice("text"), strictcli.Choice("json")],
                retired_choices=[
                    strictcli.RetiredChoice(
                        "xml", message="use 'json' (XML output was dropped)"),
                ])
```

A bare entry is refused the way a bare choice entry is:

```
Flag "format": retired_choices entry 0 is a bare value: declare it as RetiredChoice(<value>, message="<message>")
```

**Go** -- a functional option beside `Choices`, over records beside `Ch`:

```go
strictcli.StringFlag("format", "output format",
    strictcli.Choices(strictcli.Ch("text", ""), strictcli.Ch("json", "")),
    strictcli.RetiredChoices(
        strictcli.RetiredChoice("xml", "use 'json' (XML output was dropped)"),
    ),
    strictcli.Required(),
)
```

Positional args take the same records through `ArgRetiredChoices(...)`.

**TypeScript** -- a member of the options object, typed as the readonly
non-empty tuple `choices` already uses:

```ts
format: flag("format", t.str, {
    help: "output format",
    choices: [{ value: "text" }, { value: "json" }],
    retiredChoices: [
        { value: "xml", message: "use 'json' (XML output was dropped)" },
    ],
    presence: "required",
}),
```

**A retired value is not a choice.** Help output is unchanged -- a retired
spelling appears in no help line and no choices block -- and the published
`value_schema` enum, together with the MCP tool schema derived from it, carries
the live values only. What a retired spelling means is "this used to be
accepted", and a surface that lists what a caller may pass would be saying the
opposite.

**No exhaustiveness story is needed, in any language.** This is not a delivery
API: a handler never receives a retired value, because the invocation that
named one never reaches the handler. There is no closed set arriving at a
consumption site, so there is nothing for `match` / `Match` / `switch` to be
exhaustive over.

`--dump-schema` publishes the declaration as a `retired_choices` object on the
flag or arg entry, mapping each retired spelling to its message, sorted
ascending by key (the treatment a group's `deprecated` map gets) and omitted
entirely when nothing is retired:

```json
"retired_choices": {
  "ascii": "use 'text'",
  "xml": "use 'json'"
}
```

Rules, each a registration-time hard error:
- A retired spelling may not also be a live choice: a value is live or retired, never both.
- A retired spelling may not be declared twice.
- A retired spelling's message is mandatory and non-empty.
- Retired choices are incompatible with `type=bool`, like `choices` itself.
- A declared `default` may not name a retired spelling.
- Retired choices require `choices`. A declaration with no live values to redirect to has nothing to say.

## Custom validation

A `validate` callback runs after type coercion and choices validation, giving
the flag author a way to enforce arbitrary constraints on the parsed value. The
callback receives the coerced value and should raise `ValueError` with a
descriptive message on failure. For repeatable flags, the callback is called
once per element.

```python
def positive_int(val):
    if val <= 0:
        raise ValueError("must be positive")

@strictcli.flag("port", type=int, presence="required", help="port number", validate=positive_int)
```

The callback receives the coerced value and should raise `ValueError` with a
message on failure. For repeatable flags, the callback is called once per
element. It runs on **supplied** values only: a declared default is the
author's to get right at registration, so `validate` never runs against it, and
it never runs on the absence an optional declaration delivers.

## Env var binding

Flags can be bound to environment variables via the `env` parameter, providing a
fallback source for values not passed on the command line. Environment variables
sit between CLI tokens and config file values in the resolution cascade (CLI >
env > config > default) and are skipped entirely under `--hermetic` mode.

```python
@strictcli.flag("token", type=str, presence="required", help="API token", env="MYAPP_TOKEN")
```

Precedence: CLI > env > config > default. An env- or config-supplied value
satisfies a `presence="required"` declaration -- what a required declaration
demands is a value arriving, not a token being typed -- and it makes the flag
*provided*, so
`ctx.provided("token")` is true with source `env` or `config`.

When the app declares an `env_prefix`, flag env vars must start with that prefix (enforced at registration). The `prefixed=False` option exempts a flag from this check.

## Positional arguments

Positional arguments are declared via `Arg` objects, passed to `@app.command()`
or attached via the `@strictcli.arg` decorator. Positional arguments are
consumed in declaration order after all flags have been parsed, and support the
same four scalar types as flags plus variadic collection into lists.

```python
@app.command("greet", help="say hello", effect="read_only")
@strictcli.arg("name", help="who to greet", presence="required")
def greet(ctx, name):
    ctx.info(f"Hello, {name}!")
```

### Arg presence

Positional args take **the same three facts and the same one-spelling rule** as
flags. There is no `required=` parameter on an arg: it was an implicit default
(an arg that declared nothing was required by omission), and paired with
`default=` it spelled one fact across two fields.

| Fact | Python | Go | TypeScript |
|------|--------|-----|-----------|
| required | `presence="required"` | `ArgRequired()` | `{ presence: "required" }` |
| optional | `presence="optional"` | `ArgOptional()` | `{ presence: "optional" }` |
| default | `default=<value>` | `ArgDefault(<value>)` | `{ presence: "default", default: <value> }` |

```python
# A default when the token is absent
@strictcli.arg("output", help="output file", default="out.txt")

# Absence delivered as absence: the handler receives None
@strictcli.arg("note", help="an optional note", presence="optional")
```

An optional arg delivers absence as a **present key** (`None` / `nil` /
`undefined`) exactly as an optional flag does -- it does not omit the keyword
argument.

### Variadic args

A variadic arg collects all remaining positional tokens into a list, and must be
the last positional argument in the command's declaration. At most one variadic
arg is allowed per command. Because a variadic always delivers a list,
`presence="required"` means *at least one value* and `presence="optional"` means
*possibly none*; a `default` on a variadic arg is a registration error.

```python
@app.command(
    "process",
    help="process files",
    effect="read_only",
    args=[strictcli.Arg(name="files", help="input files", variadic=True, presence="required")],
)
def process(ctx, files):
    for f in files:
        ctx.info(f"Processing {f}")
```

```
mytool process a.txt b.txt c.txt
# files=["a.txt", "b.txt", "c.txt"]
```

The empty case is spelled once, as `presence="optional"`. Declaring a default
instead is refused:

```
Arg "files": a variadic arg cannot declare default=: it always delivers a list, so declare presence="required" for at least one value or presence="optional" for possibly none
```

Variadic args support typed collection via `type=list[int]` (which also requires `variadic=True`).

### Arg types

Args support the same four scalar types as flags: `str`, `bool`, `int`, and
`float`. Type coercion uses the same strict parsing rules, and variadic args
additionally support the `list[T]` compound type for typed collection. Dict
types are not supported on positional args.

## The context object

Every command handler receives a `Context` as its first argument (in Go and
Python; TypeScript uses args-first, ctx-second). The context provides structured
output methods for writing to stdout and stderr, provenance introspection via
`ctx.source()`, and infrastructure value access via `ctx.infra_value()`. The
context is the primary interface between the handler and the framework.

```python
@app.command("deploy", help="deploy the app", effect="mutating")
@strictcli.flag("target", type=str, presence="required", help="deployment target")
def deploy(ctx, target):
    ctx.info(f"Deploying to {target}")      # stdout
    ctx.warn("Deployment is slow today")     # stderr
    ctx.debug("Connecting...")               # stdout, only under --verbose
    ctx.error("Connection failed")           # stderr
```

### Output methods

| Method | Stream | Purpose |
|--------|--------|---------|
| `ctx.info(msg)` | stdout | Informational messages (suppressed under `--quiet`) |
| `ctx.warn(msg)` | stderr | Warnings (never suppressed) |
| `ctx.debug(msg)` | stdout | Debug output (shown only under `--verbose`) |
| `ctx.error(msg)` | stderr | Error messages (never suppressed) |

### The reserved quartet on the context

`--dry-run`, `--approve-consequential`, `--quiet` and `--verbose` are framework
flag names that cannot be declared at any level. They never arrive as handler
parameters -- their values are read from the context as `ctx.dry_run`,
`ctx.approve_consequential`, `ctx.quiet` and `ctx.verbose`. Declaring a flag
with any of those four names raises `ValueError` at registration time.

### The consent parameter name

`approve_consequential` -- the underscore spelling, the one the *parameter*
surface uses -- is reserved separately, and it is the only reserved name that
reaches positional args as well as flags. It is how a caller states consent
programmatically (`app.call(..., approve_consequential=True)`,
`WithApproveConsequential()`, `{ approveConsequential: true }`) and over MCP
(a top-level `tools/call` param). Declaring a flag or an arg of that name is a
registration-time hard error in every implementation:

```
flag name 'approve_consequential' is reserved by the framework: it names the programmatic consent parameter
arg name 'approve_consequential' is reserved by the framework: it names the programmatic consent parameter
```

Without the reservation the same command would mean different things on
different channels: in Python a positional arg of that name is swallowed by
`call()`'s keyword-only consent parameter while MCP still reaches it. Go and
TypeScript pass kwargs as a map and an options object, so they are structurally
immune -- but the name is framework vocabulary everywhere, so all three refuse
it identically. The hyphenated `approve-consequential` is already banned as a
flag name by the quartet; as an *arg* name it stays legal, because an arg has
no `--` spelling and never becomes that parameter.

### Provenance: ctx.source()

`ctx.source(name)` returns where a flag's value came from as a string label,
enabling handlers to alter behavior based on whether a value was explicitly
provided by the user or fell through to its default. The method accepts both
dashed names (`log-file`) and underscored names (`log_file`).

```python
@app.command("cmd", help="a command", effect="read_only")
@strictcli.flag("target", type=str, default="local", help="target", env="MYAPP_TARGET")
def cmd(ctx, target):
    source = ctx.source("target")  # "cli", "env", "config", "default", "implied", or "infra"
    ctx.info(f"target={target} (from {source})")
```

The 6 source labels:

| Label | Meaning |
|-------|---------|
| `cli` | Explicitly passed on the command line |
| `env` | From an environment variable |
| `config` | From a config file |
| `default` | From the flag's declared default value -- and the label an optional flag carries when nothing supplied it |
| `implied` | Injected by an `Implies` constraint |
| `infra` | Default resolved through a `RelativeToRoot` infrastructure root |

For the yes/no question -- *did the invocation cause this value?* -- use
[`ctx.provided(name)`](#was-this-supplied-ctxprovided) rather than comparing
labels by hand. `ctx.source` is not superseded: it answers the narrower question
of *which* origin, and is still the accessor for a handler that must distinguish
env from config.

### Handler return values

Handlers must return one of three strictly validated types, and any other return
type is a hard error that immediately terminates the program. This strict
contract prevents silent bugs where a handler accidentally returns a string,
list, or other value that the framework would not know how to interpret:
- `int` -- exit code (0 = success)
- `None` -- exit 0
- `strictcli.outcome(exit_code)` -- a branded exit-code result (structured output goes through `ctx.payload(...)`)

Any other return type is a hard error. Structured output is a separate channel: a command declares its payload's JSON Schema with `payload_schema=` and its handler supplies the value through `ctx.payload(...)`, which is printed only under the framework-owned `--json` and captured by `app.test()` and `app.call()` in either mode.

## Global flags

Flags can be declared at the app level, making them available to all commands
in the application. Global flags are parsed before the command token during the
global flag parsing stage, and their values are passed to every handler alongside
the command's own flags. Global flag names cannot collide with reserved framework
names like `help`, `version`, `dump-schema`, `mcp`, `config`, or `hermetic`, nor
with the reserved quartet `dry-run`, `approve-consequential`, `quiet`, `verbose`,
nor with `json`, which selects machine mode.

```python
app = strictcli.App(
    name="mytool",
    version="1.0.0",
    help="my tool",
    flags=[
        strictcli.Flag(name="color", type=bool, default=True, help="colorize output"),
        strictcli.Flag(name="output", type=str, default="text", help="output format"),
    ],
)
```

Global flags are parsed before the command token and are passed to every handler.

## Flag sets

Reusable flag bundles avoid repetition across commands by grouping related flags
into a named `FlagSet` that can be attached to multiple commands. Each command
that uses a flag set receives all its flags as if they were declared directly on
the command, including type checking, env var binding, and constraint
validation.

```python
auth_flags = strictcli.FlagSet(
    name="auth",
    flags=[
        strictcli.Flag(name="token", type=str, presence="required", help="API token", env="MYAPP_TOKEN"),
        strictcli.Flag(name="region", type=str, presence="optional", help="AWS region"),
    ],
)

@app.command("deploy", help="deploy", effect="mutating", flag_sets=[auth_flags])
def deploy(ctx, token, region):
    ...

@app.command("status", help="check status", effect="read_only", flag_sets=[auth_flags])
def status(ctx, token, region):
    ...
```

The [mutating-default ban](#a-mutating-command-may-not-default-a-value) is
evaluated over the flags a command actually carries, its flag sets' included, so
a flag set is legal on its own and attaching it decides the verdict: a set
carrying `default="us-east-1"` attaches to `status` and is refused on `deploy`.

## Choice flags: a choice is a declaration scope

A **choice flag** elects **exactly one** of its declared choices per invocation,
and each choice declares a **scope**: the flags that exist only while that
choice is elected. Scoping by nesting replaces scoping by a separate constraint
object -- *`--subject` belongs to `email`* is expressed by where the declaration
sits, not by a rule written beside it.

```python
@strictcli.choice("email", help="deliver the notification as an email message")
class Email:
    subject: str = strictcli.sub_flag(help="subject line of the message", presence="required")
    recipient: str = strictcli.sub_flag(help="destination email address", presence="required")


@strictcli.choice("sms", help="deliver the notification as a text message")
class Sms:
    phone_number: str = strictcli.sub_flag(help="destination number in E.164 form", presence="required")


@app.command("send", help="send one notification", effect="mutating")
@strictcli.choice_flag("via", help="delivery channel", short="v", presence="required",
                       elect_by="selector-token", choices=[Email, Sms])
def send(ctx, via: Email | Sms):
    ...
```

`notify send --via email --subject hi` parses. `notify send --via sms --subject hi`
is a parse error the declaration produces on its own:

```
error: flag '--subject' is only valid under '--via email', but '--via sms' was elected
```

Never "unknown flag": the flag is declared, it is simply not in the elected
scope, and the sentence names both sides.

There is **no at-most-one construct** anywhere in the framework. An absent
selection is a choice nobody named, so the answer is to name it as a choice of
its own.

### The two spellings

`elect_by` chooses how a choice is elected on the command line. It is mandatory
in Python and has no default; Go and TypeScript spell the same decision as twin
constructors, so there is no option to forget.

| | token spelling | member spelling |
|---|---|---|
| declared by | `elect_by="selector-token"` / `ChoiceFlag(...)` / `choiceFlag(...)` | `elect_by="member-flags"` / `MemberChoiceFlag(...)` / `memberChoiceFlag(...)` |
| typed as | `--via email` | `--profile work`, `--all-profiles` |
| the flag's own name | is typed | is **never** typed -- it is the handler key and the noun help and errors use |
| a choice may carry a payload | no (the token names the choice) | yes: exactly one value, delivered under the reserved name `value` |
| elects from | any source: CLI > env > config > default | the **command line only** |

Both spellings deliver the same kind of value; only tokenization differs.

### Member spelling

A member-spelled choice is its own flag, carrying its own payload:

```python
@strictcli.choice("profile", help="use the named profile")
class NamedProfile:
    value: str = strictcli.member_value(help="profile name")
    create_missing: bool = strictcli.sub_flag(help="create the profile if it does not exist",
                                              presence="optional")


@strictcli.choice("all-profiles", help="apply to every profile")
class AllProfiles:
    pass


@app.command("sync", help="synchronize profiles", effect="mutating")
@strictcli.choice_flag("scope", help="what to synchronize", presence="required",
                       elect_by="member-flags", choices=[NamedProfile, AllProfiles])
def sync(ctx, scope: NamedProfile | AllProfiles):
    ...
```

What a member elects, and what it does not: a bool member is elected by
`--<name>` and only when it resolves to **true**. `--no-<name>` *declines* --
it says "not this one" and elects nothing. Every other type elects on presence
with any value, including the empty string (`--profile ""` is an explicit act;
whether `""` is legal for that flag is the flag's own value validation).

| Situation | Error |
|-----------|-------|
| More than one member elected | `--profile and --all-profiles are mutually exclusive` |
| One elected, another declined | `--no-all-profiles cannot be combined with --profile (--no-all-profiles declines an option; it does not choose one)` |
| Nothing elected | `one of --profile, --all-profiles is required`, plus ` (--no-all-profiles declines an option; it does not choose one)` when a member was declined |

**Election is command-line-only**, and that is the framework's one deliberate
exception to the ordinary CLI > env > config > default precedence. The spelling
exists to make the operator choose *in the invocation*, and an inherited
environment is not that choice.

A member owns a scope like any other choice, so `--profile work --create-missing`
parses and `--all-profiles --create-missing` is a scope error. A member carries
its payload in the alternative that owns it, so there are no sentinel defaults
on flags that mean nothing unless elected. And a member-spelled choice flag
**cannot carry a short** -- it is never typed; a short declared on a member is
an ordinary flag short.

### Presence

A choice flag declares `required` or a `default`. **`optional` is refused**:

```
Flag "via": a choice flag cannot declare presence="optional": an absent selection is a choice nobody named, so name it as a choice of its own
```

A **member flag must declare `required`**, read as *required once this member is
elected*. This is the inverse of the rule the retired mutex group carried, where
a member was forbidden from declaring `required`:

```
Choice "profile" of "scope": a member flag must declare Required(), read as required once this member is elected
```

That error belongs to Go and TypeScript, each with its own spelling inside the
sentence. Python has no input that could produce it: `member_value(help=...)`
takes no presence keyword, and a frozen dataclass's field is required by
construction.

A choice flag's **`default` is a complete elected value** -- a choice plus every
field its scope needs -- so a defaulted selection with an unsatisfied required
sub-flag cannot exist. Python spells it as a choice *instance*
(`default=Sms(phone_number="+15550100")`), which a frozen dataclass cannot
construct without its required fields; Go and TypeScript name the choice and a
registration check refuses one whose scope declares a required sub-flag.
Electing a choice on the command line never borrows the default's values.

Scoped flags declare presence exactly as command-level flags do -- `required`,
`optional`, or a `default`, resolved when their scope is elected. A scope is not
a presence declaration and never supplies one.

### Delivery: one tagged record per choice flag

The handler receives, under the choice flag's own key, the elected choice plus
that choice's fields, in each language's own exhaustively-checkable shape:

| | delivered as | consumed by |
|---|---|---|
| Python | a frozen dataclass instance of the elected choice class | `match` / `case`, with `assert_never` in the last branch |
| Go | `*Elected` -- the elected `*ChoiceDecl` plus `Fields` | `sc.Match` with `sc.When(<choice value>, ...)`, exhaustive against the declaration |
| TypeScript | a member of a derived discriminated union, tagged `choice` | `switch (args.via.choice)`, with `assertNever` in the default branch |

See the [Python](python-quickstart.md#choice-flags), [Go](go-quickstart.md#choice-flags)
and [TypeScript](typescript-quickstart.md#choice-flags) quickstart guides for each.

**Scoped flags are never top-level handler arguments, at any depth.** The only
key a choice flag adds is its own, so every declared top-level key is still
always present. One level down the ordinary presence rule applies again
unchanged: an optional sub-flag delivers absence as a present **field** of the
record, never a missing one.

Inside the record, provided-ness is answered by the record itself --
`strictcli.provided(via, "subject")` (Python), `e.Provided("subject")` (Go),
`provided(args.via, "subject")` (TypeScript). The context-level accessors do not
see scope interiors: a scoped name is not unique command-wide, so
`ctx.provided("subject")` has no single answer and raises the ordinary
unknown-name error rather than inventing one. The choice flag's own key **is** in
the store and answers as any flag does.

### Order, recursion, and depth

Nothing is interpreted until every token is collected, so `--subject hi --via email`
parses exactly as `--via email --subject hi` does. Parsing is phased --
**tokenize, resolve elections, validate scope membership, resolve values and
presence** -- and error precedence follows that order: **election, then scope,
then value, then presence**. `--via sms --subject hi` reports that `--subject`
belongs to `email`, never that `--phone-number` is required: the spelling
mistake is reported before its consequence.

A choice flag is a flag, so a choice flag may be declared inside a choice's
scope, to **unlimited depth** (`sub_choice_flag` in Python). "Required exactly
when user-facing" stops being a rule a handler enforces and becomes where the
declaration sits.

Every name rule re-runs at every depth: the reserved quartet and `json`, the
banned `yes`, bare `force`, the `no-` prefix, `approve_consequential`, the
charset and the mandatory help. Two further names are reserved inside every
scope -- `choice` (the delivered record's tag) and `value` (a member-spelled
choice's own payload). A scoped flag may not reuse a command-level flag's name
nor its own choice flag's name; sibling scopes may reuse a name only with an
identical type and arity, because tokenization cannot wait for an election;
simultaneously electable scopes may not reuse one at all. **Positional args
cannot be declared inside a scope**, and keep `choices` at command level
unchanged.

### Ambient values in a scope that was not elected

An env var or a config key bound to a scoped flag is consulted **when its scope
is elected**, and otherwise never consulted. It is not an error and it is not a
value -- the binding's condition is written in the declaration, and the
framework evaluates it the same way every run.

Every skipped binding that actually carried a value is named on the debug
channel, one line per binding, in declaration order -- hidden by default, shown
by `--verbose`, and carried in machine mode's `diagnostics` at level `debug`:

```
not consulted: env var 'MYAPP_SUBJECT' binds flag '--subject' under '--via email', which was not elected
not consulted: config key 'subject' binds flag '--subject' under '--via email', which was not elected
```

An election from a non-CLI source names itself in every message it causes, so a
refusal never blames a command line that does not contain the cause. The clause
is `(elected from env var 'NOTIFY_VIA')`, `(elected from config key 'via')` or
`(elected by default)`, appended after the scope suffix; an election the command
line made renders no clause at all, because the cause is already on the line the
reader typed.

```
error: flag '--phone-number' is required under '--via sms' (elected from env var 'NOTIFY_VIA')
```

The one template that already ends in the election carries the same clause
without the parentheses, because there is nothing left to wrap:

```
error: flag '--subject' is only valid under '--via email', but '--via sms' was elected from env var 'NOTIFY_VIA'
```

### Constraints operate at root scope only

Every [constraint](#constraints) references its members **by name**, and a
scoped name has no single namespace to resolve in. A constraint naming a scoped
flag is a registration error, not a resolution -- because the
scope already **is** the constraint. A choice's scope says "these flags exist
together, exactly when this choice is elected", which is a co-requirement plus
an exclusivity plus a conditional requirement in one declaration.

### On the machine boundaries: schema, MCP and `call()`

**The dumped schema** carries the construct natively. A choice flag has no
`value_schema` -- a variant is inexpressible in the closed JSON Schema subset,
and its absence *is* the declaration -- and it publishes nested `choices` plus
`elect_by` instead, each scoped entry a full flag entry with its own
`value_schema`, `presence` and `default`. See
[the schema format](architecture.md#the-choice-flag-encoding).

**The MCP tool schema is flatten plus a description block.** One object schema
per command: the choice flag contributes one property named after itself,
`{"type": "string", "enum": [<choice names>]}`, in `required` iff it declares
`required`; every scoped flag contributes a top-level property and **never**
appears in `required`, because whether it is required depends on the election
and the schema has no vocabulary for that. A member-spelled choice flag projects identically to
a token-spelled one -- tokenization is a command-line fact and there are no
tokens at this boundary.

The scope structure survives in the tool **description**, appended as a
deterministic block so an agent can read the constraint the schema cannot carry:

```
Scoped parameters (enforced at call time):
  via=email: subject (required), recipient (required)
  via=sms: phone_number (required)
  via=webhook: url (required), retries (default: 3)
```

One line per scope, at every depth, in declaration order; the key is the scope's
path rendered as `<property>=<choice>` segments joined by a single space, and an
empty scope renders `(no parameters)`. Wrong combinations are refused at call
time with **the same sentence the CLI parser gives**, carried on the framework's
ordinary tool-result error channel. The cost is stated rather than hidden: an
agent cannot see the scope rule before it calls, and learns by being refused.

**`call()` takes the elected record, pre-typed.** The programmatic front door's
contract is unchanged -- pre-typed values, no parsing -- so a choice flag's value
is the same record a handler receives: a choice instance (Python), an
`Elect(<choice>, Fields{...})` value (Go), the union member object (TypeScript).
The flat machine form is converted into that record at the protocol boundary,
through the same election, scope and presence machinery the argv path uses.

### Choosing between a choice flag and a `choices` flag

The boundary is structural, not a matter of taste:

> **Need a scope or member spelling -> choice flag. A plain constrained value -> `choices` flag.**

| | `choices` flag | choice flag |
|---|---|---|
| what an entry is | a **value**, with **optional** help | a **choice**: a name, **mandatory** help, and a scope |
| delivery | the bare scalar, unchanged | one tagged record |
| presence | all three | `required` or a `default` only |
| sources | all sources | token spelling: all sources; member spelling: command line only |
| help | one line, or a block once any entry carries help | always a block |

Moving a declaration from a `choices` flag to a choice flag changes the handler
contract from a **scalar** to a **record**: every read of that value changes
shape, and the command's tests, its `call()` sites and its MCP arguments change
with it. That cost is why both constructs exist -- forcing every four-value enum
through a choice flag would make every simple flag pay it -- and it is why the
boundary is drawn at what the choices *carry* rather than at how many there are.

(`json` is not usable as a flag name anywhere: it is reserved for machine mode.)

## Constraints

A **constraint** is a declared rule over a command's own flags, args and other
constraints. There are four kinds, in two pairs:

| Kind | Meaning |
|---|---|
| **at-least-one** | at least one member is **engaged**. Members **may co-occur** -- engaging two, or all, satisfies it exactly as engaging one does |
| **all-or-none** | either **every** member is engaged or **none** is. With nothing engaged it is vacuously satisfied -- that is the "none" half of its own name |
| **requires** | if one flag is provided, another must also be provided |
| **implies** | when one bool flag is provided, another is automatically set to a declared value; an explicit contradiction is a parse error |

All four are declared through one container -- `constraints=[...]` in Python,
`WithConstraints(...)` in Go, `constraints: [...]` in TypeScript -- and every
one of them carries a **mandatory name**. The name is what a violation prints,
what `--help` shows, and what lets one constraint be a member of another. It is
mandatory rather than optional-with-a-fallback because a family plus a member
list identifies a rule only until a command has two rules over overlapping
members, which real commands do.

**at-least-one is not exclusivity.** It has no upper bound and never refuses a
second member. Exactly-one selection is a
[choice flag](#choice-flags-a-choice-is-a-declaration-scope), not a constraint,
and there is no at-most-one construct anywhere in strictcli -- no constructor,
no cardinality parameter, no `min`/`max` pair.

```python
@app.command(
    "cmd",
    help="a command",
    effect="mutating",
    constraints=[
        # --host and --port must both appear, or neither
        strictcli.AllOrNone("endpoint", [
            strictcli.Member("host"), strictcli.Member("port"),
        ]),

        # One-way: --trace requires --log-file
        strictcli.Requires("trace-needs-log", flag="trace", depends_on="log-file"),

        # Auto-set: when --fast is passed, set --no-embeddings
        strictcli.Implies("fast-skips-embeddings", flag="fast",
                          implies="embeddings", value=False),
    ],
)
```

### Members

A member is a **reference by name**, never an owned declaration: flags and args
are declared once, in the normal places, and a constraint names them. A member
carries no presence and no help of its own -- the declaration it points at
carries every fact about the value. Registration resolves every name and refuses
an unknown one.

A member of an at-least-one or an all-or-none names one of three things:

| Member kind | What it names |
|---|---|
| `flag` | a command flag, at **root scope** |
| `arg` | a positional arg of the same command |
| `constraint` | another **named** at-least-one or all-or-none of the same command |

Names resolve in **one namespace** -- the command's flags, its args and its
constraints. A constraint name that collides with a flag or arg name is a
registration error, and so is a member name that resolves to both a flag and an
arg: the framework refuses to guess rather than picking one.

`requires` and `implies` are the exception, and deliberately so: their operand
vocabulary stays **flags only**, by name, at root scope. Neither takes an arg,
a nested constraint or an election selector.

A co-occurrence constraint must declare **at least two** members. In Go and
TypeScript that floor is a compile error rather than a registration one -- Go's
constructors take two named members before the variadic tail, and TypeScript's
`members` is typed `[ConstraintMember, ConstraintMember, ...ConstraintMember[]]`.

### The election vocabulary: `when`

Each flag or arg member declares **when it counts as engaged**, from a closed
three-value vocabulary. What counts as "chosen" is a declaration, never a rule
the parser applies on its own:

| `when` | Engaged when | Legal on |
|---|---|---|
| `present` | the value was **provided** -- `cli`, `env`, `config` or `implied`, never `default` and never `infra` | every type |
| `true` | provided **and** the resolved value is `true` | `bool` only |
| `non_empty` | provided **and** the resolved value is a non-empty string, list or map | `str`, repeatable/list and dict flags, and variadic args |

The default is `present`, **and omitting `when` on a bool member is a
registration error**. Both halves are deliberate. A uniform default keeps the
vocabulary from becoming type dispatch wearing a keyword, and the bool refusal
closes the hole the default would otherwise open: `present` on a bool means
`--no-all` engages a constraint while *selecting nothing*. The framework refuses
to guess exactly where guessing is known to be wrong, so a bool member says
which it means:

```python
strictcli.AtLeastOne("purge-selection", [
    strictcli.Member("targets", when="non_empty"),
    strictcli.Member("older-than"),
    strictcli.Member("larger-than"),
    strictcli.Member("all", when="true"),
])
```

Declaring `when="true"` on a non-bool, or `when="non_empty"` on a bool, an int
or a float, is a registration error: a selector that cannot be evaluated against
the declared type is a mis-declaration, not a no-op. (A **variadic bool arg** is
sized rather than bool -- its value is a sequence whatever its element type is --
so `non_empty` is legal on it, `true` is not, and omitting `when` is legal.)

There is no source filter in the vocabulary. There is exactly one definition of
"was this supplied" in the framework, and it is the one `ctx.provided` answers.

### Engaged, vacuous, satisfied

Three words, defined once, because every surface below depends on them:

- a **flag or arg member is engaged** when its `when` selector fires;
- a **nested constraint member is engaged** when at least one of *its* members
  is engaged. Engagement propagates upward; satisfaction does not;
- **at-least-one is satisfied** when at least one member is engaged;
- **all-or-none is satisfied** when every member is engaged or none is;
- a constraint is **vacuous** when no member of it is engaged. A vacuous
  all-or-none is satisfied; a vacuous at-least-one is violated, which is the
  whole of what it says.

**Children are evaluated before parents, siblings in declaration order.** A
violated nested constraint reports its own sentence and its parent is never
evaluated. That is not a tie-break convention -- it is the only order that
reports the fixable fact: an operator who typed one half of a pair is told the
pair is incomplete, not that the whole selection is missing.

Constraints run at one fixed point in the parse pipeline: **after** `Implies`
injection, so an implied value can engage a member, and **before** defaults are
applied, so a declared default cannot.

### Nesting

A named at-least-one or all-or-none may be a member of another one. Nesting is a
cycle-checked DAG at unlimited depth, and only the two co-occurrence families
may be nested -- naming a `Requires` or an `Implies` as a member is a
registration error, because those two are rules rather than co-occurrence
predicates and "engaged" has no meaning for them.

The shape that motivates it: *change the author name, or the email, or both --
but never half of either.*

```python
@app.command("rewrite", help="Rewrite author identity across history",
             effect="mutating",
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

A vacuous all-or-none nested inside an at-least-one contributes nothing, so two
vacuous pairs leave the parent unsatisfied -- which is exactly the rule above.
Bare, that reads:

```
error: constraint "author-change": at least one of (--old-name with --new-name), (--old-email with --new-email) is required
```

Type one half of a pair and the child fires instead, by the children-before-parents
order:

```
error: constraint "author-name": --old-name, --new-name must be used together
```

A member list is rendered **structurally, never by name**: a nested member
renders its own operands, joined by ` with ` for all-or-none and ` or ` for
at-least-one. The constraint's name identifies the rule that failed and appears
once, in the prefix; the member list names tokens the reader can type.

### A member never declares `required`

Membership does not subtract from a declaration and does not add to one. **No
member of a co-occurrence constraint may declare `required`** -- it is a
registration error in both families:

- in an **at-least-one**, a required member means the invocation always supplies
  it, so the constraint is satisfied in every invocation and can never fire;
- in an **all-or-none**, a required member turns the rule into "every other
  member is required too", which already has a spelling: declare them required.

A **default** is legal and is the ordinary bool shape on a `read_only` command --
a default is not *provided*, so it never engages the constraint by itself, and
`default=False` with `when="true"` engages exactly when someone types `--all`. On
a `mutating` command that same declaration is refused by the
[mutating-default ban](#a-mutating-command-may-not-default-a-value), and the
member declares `optional` instead: an optional bool is a real tri-state, so
`when="true"` still engages exactly when the flag resolves true. `optional` is
legal everywhere and is the ordinary case.

The consequence runs the other way too: **membership never makes a flag
required, and never exempts it from being required.** An at-least-one over three
optional flags leaves all three optional. What is required is that one of them
engages, which is the constraint's own sentence and never a per-flag presence
part.

Constraints can only reference flags and args you declared. The reserved quartet
is not declarable, so `dry-run`, `approve-consequential`, `quiet` and `verbose`
can never appear in one, and a constraint naming a
[scoped flag](#constraints-operate-at-root-scope-only) is a registration error.

### Where constraints show up

**In help**, as a `Constraints:` section after the last of the `Arguments:` /
`Flags:` blocks. The declared name renders in the position a flag name occupies,
so the identifier a violation prints is discoverable in the help the operator
already read:

```
Flags:
  --old-name <str>     Current display name [optional]
  --new-name <str>     New display name [optional]
  --old-email <str>    Current email address [optional]
  --new-email <str>    New email address [optional]

Constraints:
  author-name      all or none of --old-name, --new-name
  author-email     all or none of --old-email, --new-email
  author-change    at least one of (--old-name with --new-name), (--old-email with --new-email)
```

One line per constraint in declaration order, including nested ones -- a nested
constraint has its own line *and* appears inside its parent's line, because it
is both a rule of its own and an operand. The block computes its own alignment
column and never shares the flag block's. The per-family sentences are
`at least one of <members>`, `all or none of <members>`,
`--<flag> requires --<depends_on>` and `--<flag> implies --<implies>` (rendering
`--no-<implies>` when the declared value is false). A member's presence part is
never repeated here: every flag line already carries exactly one.

**In the schema**, in each command's `constraints` array, in declaration order.
The encoding is complete rather than indicative -- a consumer reconstructs the
rule without re-reading the declaration:

```json
[
  {"type": "all_or_none", "name": "author-name", "members": [
    {"kind": "flag", "name": "old-name", "when": "present"},
    {"kind": "flag", "name": "new-name", "when": "present"}]},
  {"type": "at_least_one", "name": "author-change", "members": [
    {"kind": "constraint", "name": "author-name"},
    {"kind": "constraint", "name": "author-email"}]}
]
```

The `kind` is the **resolved** kind, so no name lookup is needed; `when` is
always emitted on a flag or arg member and never on a constraint member; nesting
is published as constraint-kind members rather than flattened into leaves. See
[constraint serialization](architecture.md#constraint-serialization) for the
full catalogue.

**In MCP tool schemas**, with a declared fidelity policy: a constraint is
**never silently dropped**. Every kind is either *exact* -- a JSON Schema keyword
expresses the rule completely -- or *partial*, in which case what can be emitted
is emitted and the **remainder is stated in the tool description**. There is no
third verdict in which a rule reaches the boundary unstated.

| Kind and shape | Emitted | Fidelity |
|---|---|---|
| at-least-one, no election selector at any depth | `anyOf`, one branch per member (a nested all-or-none becomes one branch listing its leaves; a nested at-least-one is inlined) | exact |
| at-least-one with a `true` or `non_empty` member at any depth | the same `anyOf` | partial -- `required` says a key is present, not what it holds |
| all-or-none, every member a flag or arg with `when: present` | `dependentRequired`, mapping each member to all the others | exact |
| all-or-none with a nested constraint member, or any non-`present` selector | nothing | partial -- the keyword cannot carry a group as an operand |
| requires | `dependentRequired: {<flag>: [<depends_on>]}` | exact |
| implies | nothing | partial -- it injects a value rather than constraining the input |

A command declaring exactly one at-least-one emits its branches as the object's
own `anyOf`; two or more emit `allOf: [{anyOf: ...}, {anyOf: ...}]`, one element
per constraint, because two at-least-one rules must **both** hold and merging
their branches would say "satisfy either".

The description block names every constraint either way, in property names
(underscored) rather than CLI tokens -- the caller writes keys, not argv -- and a
partial projection appends its reason:

```
Constraints (enforced at call time):
  at least one of: targets, older_than, larger_than, all -- not expressed in the schema: the "true" and "non_empty" selectors
  all or none of: old_name, new_name
```

Enforcement at call time is unchanged and total: every constraint is evaluated at
the machine doors exactly as at the argv door, and a violation returns the same
sentence the CLI parser gives, on the framework's ordinary tool-result error
channel. The runtime refusal is the authority; the schema is advisory.

## Update commands

An **update command** changes some properties of one resource instance and
leaves the rest alone. strictcli makes that a declaration rather than a
convention: the command names the resource, states which flags and args identify
the instance, which flags carry the changes, and whether the write is sparse or a
full replace. The framework then refuses an invocation that supplies no property,
renders the resulting **write set** on every surface a run reports through, and
publishes the declaration in `--dump-schema` and in MCP tool schemas.

### A mutating command may not default a value

**On a command declaring `effect="mutating"`, no flag and no positional arg may
declare a value default.** It is a registration-time hard error in all three
implementations:

```
command "update-record": flag '--ttl' declares default=300 on a mutating command: absence would write a value the invocation never stated (declare presence="required" or presence="optional", or apply the fallback in the handler and say so in its help)
```

The rule follows from one sentence, and every cell below is derived from it:
**absence must never resolve to a value the invocation did not state, because on
a mutating command a value the framework picked is a value the framework
writes.** A record-updating command that declares `default=300` for a TTL resets
a record's TTL to 300 whenever the operator changes something else and does not
restate it -- a number nobody typed replacing a number nobody read.

| Declaration, on a `mutating` command | Verdict |
|---|---|
| `default=<str>` / `<int>` / `<float>`, any value, `""` and `0` included | **registration error** -- `""` is a value like any other, and a declaration that means *absent* is `optional` |
| `default=True` / `default=False` on a bool | **registration error** -- the same class with two values |
| a **non-empty** `list` or `dict` default | **registration error** -- `default=["a"]` is as tool-picked as `default=300` |
| `default=[]` / `default={}` | **legal** -- an empty collection declares *no elements*, so no framework-chosen value reaches a write through it |
| a `RelativeToRoot` default | **legal** -- it resolves a *location* under a declared infrastructure root, deciding where a command writes and never what it writes |
| any default on a `read_only` command | **legal, untouched** -- the ban keys on classification exactly as dry-mode participation does |
| a **choice flag's** own default | **legal** -- electing a choice names which scope is live; it is not a value written to anything |
| the flags **inside** a choice's scope | **reached, at every depth** -- they are ordinary flags of a mutating command, so on a mutating command every scoped flag is `required` or `optional` |
| a **flag set's** flag | **reached per attaching command** -- a shared flag set carrying a default is legal, and attaching it to a mutating command is not |
| an **app-level global** flag's default | **not reached** -- a global has no classification of its own and reaches read-only and mutating commands alike |
| an `Implies` injection | **not a default** -- it exists only because the invocation contained the trigger (`provided()` is true, source `implied`) |
| an `env` or `config` value | **not a default** -- the operator supplied it |

A site the ban refuses has three remedies, and the error names all three: make
the declaration `required`, make it `optional`, or apply the fallback in the
handler **and say so in the flag's help**. The third is legal only where the
fallback is not itself a write -- a handler that substitutes `300` and then sends
it has moved the defect one layer down, where no registration guard can see it.
For an update command that door is closed by construction: a property has no
default and absence means untouched.

### Declaring the update

An update command carries one record, in the same registration-level family
`effect`, `consequential` and `dry_run_supported` belong to.

| Fact | What it says |
|---|---|
| `resource` | the **name** of the thing being updated -- mandatory, matching `[a-z][a-z0-9-]*` |
| `write_mode` | `"sparse"` or `"full_replace"` -- mandatory, no default |
| `identity` | the flags and args that name **which** instance -- possibly empty |
| `properties` | the flags that name **what changes** -- at least one |

**Python** -- a frozen, keyword-only record joining the `AtLeastOne` / `AllOrNone`
/ `Requires` / `Implies` family of declarations that name a rule:

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
    ...
```

**Go** -- a constructor plus functional options, with the mode as a positional
parameter because that is Go's spelling of mandatory:

```go
sc.WithUpdateOf("dns-record", sc.WriteSparse,
    sc.Identity("zone", "record-id"),
    sc.Properties("content", "ttl", "proxied"),
)
```

`Properties(first string, rest ...string)` puts a **compile-time floor of one**
on the property list, and `Nullable()` is an ordinary `FlagOption` beside
`Required()` / `Optional()` / `Default(v)`.

**TypeScript** -- one option object on the command spec, the shape
`requires({...})` and `implies({...})` already have:

```ts
updateOf: {
    resource: "dns-record",
    writeMode: "sparse",
    identity: ["zone", "record-id"],
    properties: ["content", "ttl", "proxied"],
},
```

`writeMode` is the literal union `"sparse" | "full_replace"`, so a typo is a
compile error. `properties` is typed `readonly [K, ...K[]]` where `K` is the key
union of the command's own declarations, so the floor of one *and* every name are
checked by the compiler. `nullable: true` joins the flag's option object.

**`update_of` on a `read_only` command is a registration error** -- a command that
changes nothing writes no properties -- so an update command is always
`mutating`, which is what makes the ban above apply to every one of its
declarations without a second rule. `dry_run_supported=False` composes with an
update declaration and is legal: the human write-set line then never renders,
there being no dry run to render it in, and the machine envelope's member still
does.

### Identity and properties

Both are references by name, resolved at registration, **at root scope only**. A
name is looked up among the command's flags and its positional args; unknown,
ambiguous, duplicated and both-roles names are registration errors, and a name
that resolves to a flag declared inside a choice scope is refused with the
sentence that names the actual fault. Root scope is what makes the write set
decidable at every door, including the programmatic one where a constructed scope
record cannot tell a field the caller wrote from one the declaration filled.

| | `required` | a value `default` | `optional` |
|---|---|---|---|
| **property** | **registration error** -- a property the invocation must always supply is written in every invocation, which leaves the at-least-one rule nothing to fire on | **registration error**, already, by the ban -- an update command is mutating | **the only legal declaration**; absence *is* untouched |
| **identity** | **legal**, and the ordinary case | **registration error** by the ban | **legal**, for alternative addressing: two optional identity members plus an `AtLeastOne` over them is how *by name or by id* is declared |

**A property is a flag; an identity member may be a flag or a positional arg.** A
sparse update needs every property to be individually omissible, and positional
syntax cannot deliver that past the last arg; the clear vocabulary has no
positional spelling either. Identity has neither problem, so
`myapp update-record <record-id>` stays the ordinary CLI shape.

**A property may not be a choice flag** -- an elected record is a selection rather
than a property value. **An identity member may be one**, token- or
member-spelled, which is how a resource with two addressing modes names itself.

**A flag named in neither list is neither**, and that is ordinary. `--format`,
`--wait`, `--timeout`: a flag that is not part of the resource is not a fact
about the resource. What is refused is a name in **both** lists.

### At least one property

**The framework enforces that at least one property is provided.** All properties
absent is a parse-time hard error naming every declared property -- never a silent
no-op and never a request that writes nothing:

```
error: update "dns-record": at least one property is required: --content, --ttl, --proxied
try 'mytool update-record --help'
```

A property is provided exactly when [`ctx.provided`](#was-this-supplied-ctxprovided)
says so, **with no source filter**: a value from `env`, from `config`, or injected
by an `Implies` is a provision, and a configured value cannot join a write
invisibly because it renders in the write set beside a typed one. The rule is
evaluated **after every declared constraint and before defaults are applied**, so
a command with both faults reports the constraint it declared.

**A negated bool property is a provision, not a decline.** Inside an update
command `--no-proxied` **writes `false`**: the write set is what the invocation
states, and stating `false` is stating a value. This inverts the member-election
reading on purpose -- there `--no-x` chooses nothing, because a false member is
not a selection; here false is a value with the same standing as any other. The
at-least-one refusal carries no decline clause for exactly that reason.

**An unset is a provision too**: clearing a property is writing it.

### The write set, and its two renderings

The write set of an invocation is the ordered pair of the properties it **writes**
and the properties it **clears**, in declaration order.

**The human rendering is one unnumbered line in the would-do log**, sitting
immediately after the header and before line `1.`, taking no sequence number and
rendering in dry mode only:

```
DRY RUN — no changes were made. Would do:
  writes: content; clears: ttl (other properties unchanged)
  1. net: PATCH https://api.example.com/zones/z1/dns_records/r7
```

Two segments, `writes:` first, separated by `; `. An empty segment is omitted
entirely and the at-least-one rule guarantees one survives, so the line is never
empty:

```
  writes: content (other properties unchanged)
  writes: content, ttl (other properties unchanged)
  writes: content; clears: ttl (other properties unchanged)
  clears: ttl (other properties unchanged)
  writes: content (other properties are re-sent as read)
```

Names are the properties' declared names **without** the `--` prefix and without
underscoring -- `phone-number`, never `--phone-number` and never `phone_number` --
because the log is the human surface and the write set is data. The trailing
parenthetical is a function of `write_mode` alone and is always present:
`sparse` sends only the provided properties, so the rest is unchanged;
`full_replace` sends the whole resource, so the rest is re-sent as read. A
preview that said "other properties unchanged" over a full-replace API would be a
false statement about the most destructive thing the command does, which is why
the mode has no default.

**The machine rendering is the envelope's `writes` member**, present in both
modes because it is a function of the declaration and the invocation, not of the
mode:

```json
"writes": {
  "resource": "dns-record",
  "write_mode": "sparse",
  "written": ["content"],
  "cleared": ["ttl"],
  "resent": [],
  "untouched": ["proxied"]
}
```

The four arrays hold **underscored parameter names** in declaration order and
partition the declared property set exactly: every property appears in exactly
one of them. `written` and `cleared` are the two halves of the write set and are
disjoint. `resent` and `untouched` are the two readings of "the rest", and
exactly one of them is ever non-empty -- under `sparse` the rest is untouched and
`resent` is `[]`; under `full_replace` the rest is re-sent and `untouched` is
`[]`. The member is `null` on every command that declares no update, and the
envelope's `interface_version` is `2`.

### Clearing a property

**A property declaring `nullable` mints `--unset-<prop>`.** The vocabulary is
modeled on `config set`'s value / `--clear` / `--default`, with the third case
deliberately absent: a property has no default, so there is nothing to reset it
to, and the vocabulary is complete at two.

- **`""` is an ordinary value.** `--content ""` writes an empty string. Separating
  the sentinel from the value is the whole point.
- **The minted flag is framework-owned and reaches the handler on the Context.**
  It delivers no keyword argument of its own: `ctx.unset(name)` / `ctx.Unset(name)` answers
  it. An unset property delivers absence -- `None` / `nil` / `undefined`, the same
  value an untouched property delivers -- and reports `provided()` true; `ctx.unset`
  is what saves a handler from reconstructing the boolean out of two facts.
- **Value and unset together is a parse error**, command line only, because the
  machine doors have one key per property:

  ```
  error: --ttl and --unset-ttl are mutually exclusive: a property is either written or cleared
  ```

- **The minted flag is not negatable**, and it takes no value. `--no-unset-content`
  names nothing, and `--unset-ttl=5` takes the ordinary unknown-flag path
  (`error: unknown flag '--unset-ttl'`): once the `=` is written the token names
  no flag at all. `--unset-ttl 5` leaves `5` as an extra positional.
- **The machine doors spell the clear as `null` on the property's own key.** One
  key per property at every door, and no minted parameter name to collide with a
  declared flag.
- **`nullable` on anything that is not a property is a registration error**, and
  `unset-<x>` is a **reserved flag name** on a command whose `<x>` is a nullable
  property.
- **Every type may be nullable**, the four scalars and the compounds alike:
  clearing is a fact about the resource's field, not about the value's shape.

Help rendering follows negation's precedent exactly -- one line, one help text,
one presence part, because the minted spelling is a second way to write to one
declaration rather than a second declaration:

```
myapp update-record -- change one DNS record in place

Flags:
  --zone <str>                                zone the record belongs to [required]
  --record-id <str>                           identifier of the record to change [required]
  --content <str>                             record content [optional]
  --ttl <int>, --unset-ttl                    time to live in seconds [optional]
  --proxied, --no-proxied, --unset-proxied    whether the record is proxied [optional]
```

### Bool properties, and the sub-verb pair convention

**A bool property inside an update command is a real tri-state**: `--proxied`
writes true, `--no-proxied` writes false, absent leaves it alone. That is the
optional-bool row read over a property, and it removes the class of commands that
force an operator to restate `--enable` / `--disable` on every edit, so that
changing a mail route's destination re-decides whether the route is live.

**A standalone single-property toggle keeps the sub-verb pair convention.**
`enable` and `disable` as two commands remain the right shape when the toggle
*is* the operation. Both rules are stated here so neither reads as a violation of
the other: a sub-verb pair is a **command** whose whole purpose is one property, a
tri-state bool is a **property** of a command that updates several. The test is
whether the command has anything else to write.

### On the machine boundaries

**In the schema**, the command entry carries two keys -- `update_of` (an object
with `resource`, `identity` and `properties`, names in the declared spelling) and
`write_mode` -- emitted exactly together and never alone. `nullable` is a key on
the property's own flag entry, and the minted `--unset-<prop>` gets no entry of
its own, exactly as `negatable` publishes `--no-<x>`. The encoding is complete
rather than indicative: a consumer reconstructs the rule without re-reading the
declaration.

**In MCP tool schemas**, properties are ordinary optional properties and never
appear in `required` -- what makes them required *is* the at-least-one rule, which
`required` cannot express. The rule projects instead as `anyOf`, one branch per
property, at **exact** fidelity: a supplied key is a provided property at that
door, a `null` is a supplied key and a clear, a `false` is a supplied key and a
write, so `required` states the whole rule with nothing left over. A command with
both an update and an at-least-one constraint emits
`allOf: [{anyOf: <update>}, {anyOf: <constraint>}]`, the update's branch first. A
nullable property's schema is a type list including `"null"`, because a caller
that cannot see the null cannot clear anything.

```
Update of "dns-record" (write mode: sparse):
  identifies: zone, record_id
  writes: content, ttl, proxied -- at least one is required
  a property that is not supplied is left unchanged; null clears ttl
```

Members render in property names (underscored), like every other member in the
description block: the caller writes keys, not argv. The `identifies:` line is
omitted when the resource declares no identity members; the last line's first
clause is `left unchanged` under `sparse` and `re-sent as read` under
`full_replace`; the `; null clears <list>` clause appears only when at least one
property is nullable. Enforcement at call time is unchanged and total -- every
rule is evaluated at the machine doors exactly as at the argv door.

## Help text is mandatory

Every `Flag`, `Arg`, `Command`, `Group`, and `App` must have non-empty help
text. Missing or empty help is a registration-time error with no opt-out and no
way to silence the check. This is a deliberate design choice: self-documenting
CLIs are non-negotiable, and the framework enforces this at the earliest
possible point rather than waiting for a user to encounter an undocumented flag
at runtime.
