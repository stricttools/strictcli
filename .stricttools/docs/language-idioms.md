+++
title = "Language Idioms"
description = "Why strictcli's three declaration surfaces differ on purpose: presence, the choice flag, the constraint member, the update record, the retired choice, and what parity binds."
nav_group = "Guides"
nav_order = 5
+++

# Language Idioms

strictcli ships three first-class implementations, and the first thing most
readers assume about them is wrong. They assume the goal is one API rendered in
three syntaxes -- that a Python declaration and a Go declaration should look as
alike as the two languages will permit, and that every place they read
differently is a seam waiting to be closed.

That is not the design. The three declaration surfaces are deliberately
different, and each is different in the direction its own language pulls. What
is held identical is **behavior**: the semantics of every declaration, the exact
sentence of every error, the bytes of every help screen, the fields of the
dumped schema, the exit codes. Not the spelling you write to get there.

This is not a compromise the project tolerates. It is the point. You should be
able to build your CLI in whichever of the three languages you actually want to
work in, using that language's own idioms and its own ways of writing clean
code -- and get a framework that makes the best way the only way in *that*
language, rather than one that flattens all three into whatever shape they
happen to share.

## One semantic, three surfaces

The clearest small case is **the presence declaration**. Every flag
and every positional argument declares exactly one of three facts about itself
-- that a value must be supplied, that absence is legal and delivered as
absence, or a default value the framework supplies when nothing else does.
Declaring none of the three does not register. Declaring two does not register.
Nothing about presence is inferred from the shape of another declaration.

That is one semantic, pinned once. Here is what you write.

**Python** -- a keyword argument on the `flag()` decorator, beside the
`default=` it has always had:

```python
@app.command("report", help="Summarize the last deploy", effect="read_only")
@strictcli.flag("target", type=str, presence="required", help="Where to deploy")
@strictcli.flag("tag", type=str, presence="optional", help="Optional release tag")
@strictcli.flag("cache", type=bool, default=True, help="Include cache statistics")
def report(ctx, target, tag=None, cache=True):
    ...
```

**Go** -- three sibling functional options, exactly like every other option the
package takes:

```go
strictcli.WithFlags(
    strictcli.StringFlag("target", "Where to deploy", strictcli.Required()),
    strictcli.StringFlag("tag", "Optional release tag", strictcli.Optional()),
    strictcli.BoolFlag("cache", "Include cache statistics", strictcli.Default(true)),
)
```

Positional args take the same three facts through their own trio:
`ArgRequired()`, `ArgOptional()`, `ArgDefault(v)`.

**TypeScript** -- a discriminated union on the options object, mirroring the
three-shape union `ArgOpts` has carried since the port:

```ts
flags: {
    target: flag("target", t.str, { help: "Where to deploy", presence: "required" }),
    tag: flag("tag", t.str, { help: "Optional release tag", presence: "optional" }),
    cache: flag("cache", t.bool, {
        help: "Include cache statistics",
        presence: "default",
        default: true,
    }),
}
```

Three surfaces. No two of them would be recognizable as translations of each
other, and a shared spelling could not have been any of them: Python has no
functional options, Go has no keyword arguments, and a keyword-shaped Python
surface expressed in TypeScript would have thrown away the discriminated union
that makes the whole thing type-check.

## The same design, one construct further: choice flags

The **choice flag** is where the divergence stops being a matter of taste and
becomes the whole point. One semantic: a flag elects exactly one of its declared
choices, each choice owns the flags that exist only while it is elected, and the
handler receives one tagged record it must consume exhaustively. Each language
already had a shape for "a closed set of alternatives, each carrying different
fields" -- and the three shapes have nothing in common.

**Python** -- `@choice`-decorated frozen dataclasses. A scope is a set of named
typed slots, and Python has exactly one spelling for that:

```python
@strictcli.choice("email", help="deliver as an email message")
class Email:
    subject: str = strictcli.sub_flag(help="subject line", presence="required")


@app.command("send", help="Send one notification", effect="mutating")
@strictcli.choice_flag("via", help="Delivery channel", presence="required",
                       elect_by="selector-token", choices=[Email, Sms])
def send(ctx, via: Email | Sms):
    match via:
        case Email(subject=subject): ...
        case Sms(phone_number=number): ...
        case _: assert_never(via)
```

**Go** -- choices as package-level identity values, the token idiom Go already
uses, extended to something that carries a payload:

```go
var ViaEmail = strictcli.Choice("email", "deliver as an email message",
    strictcli.StringFlag("subject", "subject line", strictcli.Required()),
)

via := strictcli.GetElected(kwargs, "via")
line := strictcli.Match(via,
    strictcli.When(ViaEmail, func(f strictcli.Fields) string { return strictcli.Get[string](f, "subject") }),
    strictcli.When(ViaSMS, func(f strictcli.Fields) string { return strictcli.Get[string](f, "phone_number") }),
)
```

**TypeScript** -- a keyed map where a carrier sits, producing a derived
discriminated union:

```ts
via: choiceFlag("via", {
    email: choice({ help: "deliver as an email message", flags: {
        subject: flag("subject", t.str, { help: "subject line", presence: "required" }),
    }}),
    sms: choice({ help: "deliver as a text message", flags: { /* ... */ } }),
}, { help: "Delivery channel", presence: "required" }),
```

Same semantic, same pinned sentences, same help bytes. Three declaration
surfaces that are not translations of each other, and each is the shape its
language's existing strictcli idiom already pointed at.

## A third case: the constraint member

The [constraint system](flag-system.md#constraints) is the same story once more,
and it is worth reading because two of the three surfaces move part of the rule
into the compiler.

One semantic: a co-occurrence constraint carries a **mandatory name** and **at
least two members**; a member is a *reference by name* to a flag, a positional
arg or another named constraint, plus an election selector from the closed
vocabulary `present` / `true` / `non_empty` saying when it counts as engaged.

**Python** -- frozen dataclasses whose first field is `name`, and a keyword
taking a closed string vocabulary, which is how Python already spells `effect=`,
`presence=` and `elect_by=`:

```python
constraints=[
    strictcli.AllOrNone("author-name", [
        strictcli.Member("old-name"), strictcli.Member("new-name"),
    ]),
    strictcli.AtLeastOne("purge-selection", [
        strictcli.Member("targets", when="non_empty"),
        strictcli.Member("all", when="true"),
    ]),
]
```

**Go** -- constructors returning a closed interface, with functional options for
the selector because Go has no keyword arguments:

```go
sc.WithConstraints(
    sc.AllOrNone("author-name", sc.Member("old-name"), sc.Member("new-name")),
    sc.AtLeastOne("purge-selection",
        sc.Member("targets", sc.WhenNonEmpty()),
        sc.Member("all", sc.WhenTrue()),
    ),
)
```

**TypeScript** -- one option object per factory, matching the shape `requires`
and `implies` already had, with a member as a plain object literal:

```ts
constraints: [
    allOrNone({ name: "author-name", members: [{ name: "old-name" }, { name: "new-name" }] }),
    atLeastOne({ name: "purge-selection", members: [
        { name: "targets", when: "non_empty" },
        { name: "all", when: "true" },
    ]}),
]
```

Two of those bought an enforcement the third cannot express, and in both cases
the same one. Go's constructors take **two named members before the variadic
tail**, so a one-member constraint does not compile; a caller holding a slice
writes `AllOrNone(n, ms[0], ms[1], ms[2:]...)`, which fails at the caller rather
than inside the framework. TypeScript types `members` as
`readonly [ConstraintMember, ConstraintMember, ...ConstraintMember[]]`, so the
same floor is a compile error there too, and `when` is the literal union
`"present" | "true" | "non_empty"`, so a typo does not compile either.

Python's list takes any length and its keyword takes any string, so Python is
where `errConstraintMinMembers` is reachable at all -- and the other two record
the covering input they can only reach through a widened or JSON-shaped caller.
Nothing was weakened to keep the three in step: the runtime refusal exists in
all three, and two of them simply refuse earlier.

## A fourth case: the update declaration

The [update-command construct](flag-system.md#update-commands) is the pattern's
newest instance, and its three surfaces sit further apart than any of the three
above.

One semantic: a command that changes some properties of one resource declares
**one record** naming the resource, a mandatory write mode, the flags and args
that identify the instance, and the flags that carry the changes -- at least one
of them. The write mode is declared **inside** the record in all three, because a
write mode without an update has nothing to describe, and nesting it makes the
half-declared state unrepresentable rather than guarded.

**Python** -- a frozen, keyword-only dataclass whose first field is `resource`,
joining the CapWords family `AtLeastOne` / `AllOrNone` / `Requires` / `Implies`
established for declarations that name a rule:

```python
update_of=strictcli.UpdateOf("dns-record", write_mode="sparse",
                             identity=["zone", "record-id"],
                             properties=["content", "ttl", "proxied"])
```

**Go** -- a constructor plus functional options, with the mode as a **positional
parameter**, which is Go's spelling of mandatory: there is no option to forget
and no zero-valued struct field to fill in silently:

```go
sc.WithUpdateOf("dns-record", sc.WriteSparse,
    sc.Identity("zone", "record-id"),
    sc.Properties("content", "ttl", "proxied"),
)
```

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

Each surface bought something the other two cannot express, and once again the
payoffs are unequal. Go's `Properties(first string, rest ...string)` puts a
**compile-time floor of one** on the property list -- the same two-named-plus-
variadic idiom its constraint constructors use, at the arity this construct
needs -- so an update with nothing to write does not compile; the registration
guard survives for the caller that omits the option entirely. Its `WriteMode` is
string-based rather than an integer enum, so the zero value renders `""` in the
invalid-mode message and the sentence stays byte-identical with its siblings'.

TypeScript's is the round's biggest, and it is a **type over the command's own
declarations**: `properties` is `readonly [K, ...K[]]` where `K` is the key union
of the names that command declares. The floor of one *and* every property name
are therefore checked by the compiler -- a property naming a flag the command
does not declare does not compile, and `writeMode` is a literal union, so a typo
does not either:

```
Type '"contnet"' is not assignable to type '"content"'.
Type '[]' is not assignable to type 'readonly ["content", ..."content"[]]'.
Type '"patch"' is not assignable to type 'WriteMode'.
```

Python's record has neither floor -- a list takes any length and a keyword takes
any string -- so Python is where the empty-property and unknown-name refusals are
reachable at all, and the other two record the covering input they can only reach
through a widened or JSON-shaped caller. Its own payoff is elsewhere: `write_mode=`
carries **no default**, so omitting it is Python's own `TypeError` at the
declaration site, before strictcli sees anything, for the same reason `effect=`
has no default.

Nothing was weakened to keep the three in step. The framework's refusals exist in
all three; two of them simply refuse earlier, in different places, for reasons
their own languages supply.

## What each surface bought

The divergence is not merely tolerated for style. In every case the language's
own form bought an enforcement the other two cannot express, and every one of
those is a gain that reaches exactly one language.

### Go: a package-private field closes a trap the exported one left open

`Required()`, `Optional()` and `Default(v)` all write the same **package-private**
`presenceBits` field on the `Flag` struct (`go/strictcli/strictcli.go`). Those
three options are the only things that write it, and no caller outside the
package can. A
`Flag` **struct literal** -- written directly, passing through none of the
option constructors -- therefore declares no presence, and does not register:

```go
app.GlobalFlag(strictcli.Flag{Name: "level", Type: strictcli.TypeStr, Help: "verbosity"})
// panics: Flag "level": presence is undeclared: declare exactly one of
// Required(), Optional(), or Default(<value>)
```

That refusal closed a pre-existing trap as a side effect. `Flag` has an
exported `Default` field, and setting it on a literal left the package-private
`hasDefault` bookkeeping false, so the value was accepted at registration and
then **silently ignored at parse time**. After the presence round that flag does
not register at all, so the value can no longer be quietly dropped. The
registration-error tests for it are in `go/strictcli/presence_test.go`
(`TestFlagStructLiteralWithDefaultFieldDoesNotRegister`, and its flag-set, arg
and choice-scope twins -- the last one is why the rule holds at every depth: a
struct literal written inside a choice's scope declares no presence and does not
register either).

Nothing in Python or TypeScript corresponds to this, because neither has a
struct literal that can bypass a constructor. The trap only ever existed in Go,
and only Go's idiomatic surface could close it.

The same field carries the choice flag's guarantee: `choices` is **unexported**
on `Flag`, so a struct literal cannot be a choice flag at all. And because a Go
choice is an identity *value* rather than a string, `e.Is(ViaEmail)` and
`When(ViaEmail, ...)` are compile-checked references -- a typo does not compile,
and renaming a choice breaks every site that names it. `Match` is exhaustive
against the declaration at dispatch: Go has no sealed union, so the check cannot
be at compile time, but it cannot be defeated by a typo and it cannot go stale,
because adding a choice breaks every `Match` that omits it on the first call.

### TypeScript: the type system carries the declaration

`FlagOpts` is a union of three members, and the two value-less members declare
`default?: never`:

```ts
export type FlagOpts<Out, S extends Schema> =
    | (FlagCommonOpts<Out, S> & { readonly presence: "required"; readonly default?: never })
    | (FlagCommonOpts<Out, S> & { readonly presence: "optional"; readonly default?: never })
    | (FlagCommonOpts<Out, S> & { readonly presence: "default"; readonly default: Out | InfraRootPath });
```

A `default` written beside `presence: "required"` does not compile. A
`presence: "default"` without a `default` does not compile. The `flag()` factory
takes **const type parameters** (`const N extends string`, `const O extends
FlagOpts<Out, S>`), so the literal options object keeps its literal type all the
way through, and `infer.ts` reads the declaration back out of it:

```ts
/** A flag key is optional iff the flag declares `presence: "optional"`. */
```

That single line fixed a real unsoundness. `FlagKeyIsOptional` used to test
`opts extends { default: null }`, which typed a flag declared without a default
as an always-present, non-nullable key while the parser handed the handler
`undefined`. The handler-args type now follows the declaration by construction.

The **choice flag** takes that further, from "the type follows the declaration"
to "the wrong shape is inexpressible". A choice flag's `choices` argument is an
object literal whose keys are literal types, so the delivered value is an exact
discriminated union with no annotation anywhere, and a scope's flags are
unreachable except through the tag that proves the scope was elected:

```ts
switch (args.via.choice) {
    case "email": return send(args.via.subject);   // `subject` exists only here
    default: return assertNever(args.via);          // a missing case fails to compile
}
```

Because silence is the failure mode, a **computed** choice key -- which would
degrade the tag to `string` and make `assertNever` accept anything -- is a
compile error naming itself. And a choice flag's `default` is typed
`keyof C & string`, so a default naming a choice that does not exist is a
compile error before it is a registration error.

The other two languages get the same guarantee at registration time, as a hard
error. TypeScript gets it at registration time **and** in the editor, before the
program is run. That is not a divergence to be apologized for.

### Python: the framework can see the handler's parameters

Python handlers name their parameters, so Python -- alone of the three -- can
check the boundary where a declaration is received:

```python
@app.command("c", help="h", effect="read_only")
@strictcli.flag("target", type=str, presence="optional", help="h")
def c(ctx, target=""):
    ...
# ValueError: command "c": handler parameter 'target' is bound to optional flag
# '--target' and must default to None
```

A written `target=""` re-introduces at the handler boundary exactly the sentinel
the presence declaration just removed -- an empty string standing in for "not
supplied", which destroys `""` as a real value. The check reads narrowly: it
fires only when the parameter **has** a default and that default is not `None`.
A bare `def c(ctx, target)` is legal, because the framework passes every declared
value as a keyword argument on every dispatch, so the parameter receives the
framework's `None` and no second value competes with it.

Go and TypeScript have no such check, and its absence is not a gap. Their
handlers receive one `map[string]interface{}` / one args object; there is no
per-parameter default with which a handler author could stand a sentinel back
up. There is no site to check -- not a check that was skipped.

The choice flag adds a second Python-only check at the same boundary, and it is
what makes `assert_never` **sound**. The parameter bound to a choice flag must be
annotated with exactly the declared union, resolved at registration through
`typing.get_type_hints`:

```
command "send": handler parameter 'via' is bound to choice flag '--via' and must be annotated Email | Sms | Webhook, got nothing
```

Without it a developer could annotate `via: Email` and silently skip two
branches with the type checker's blessing. The companion refusal fires exactly
when **the elected value would fall into `**kwargs`** -- when the selector's
parameter name is absent from the handler's named parameters -- rather than on
every `**kwargs` handler of a command declaring a choice flag. The narrowing is
forced: the framework's own internal commands absorb the app's globals through
declared forwarding, so they all carry `**kwargs`, and the reshaped `config set`
is one of them. What the guard protects is unchanged -- the annotation that makes
`assert_never` exhaustive, which a named parameter carries and `**kwargs` cannot.
Go and TypeScript reach the
same exhaustiveness by other routes -- `Match` against the declaration, and a
`switch` the compiler narrows -- so neither has a parameter annotation to check.

## What parity actually binds

Parity is not API sameness. It binds four things, and the presence round
exercises all four.

**Semantics.** An optional declaration delivers absence as a *present key* in
every implementation -- `None` / `nil` / `undefined` -- rather than omitting the
key. Optional bools are a real tri-state in all three. A compound flag gets no
silent `[]` or `{}` in any of them. A variadic arg refuses a default everywhere.

**Rendered bytes.** Help renders exactly one presence part per line, and the
three implementations emit the same characters:

```
greet hello -- Say hello

Flags:
  --name <str>         Who to greet [required]
  --tag <str>          Optional tag [optional]
  --loud, --no-loud    Shout it [default: false]
```

**Schema fields.** `--dump-schema` emits `presence` on every flag and arg entry
in every implementation, which is what stopped schema parity from passing by
erasure -- the dumped schema previously said nothing at all about which flags
had to be supplied.

**Pinned sentences.** This is the subtle one, and it is where the whole design
becomes legible.

## One sentence, each language's spellings inside

An error message names things the user wrote. When the thing it names is a
*spelling*, and the three languages spell it differently, a byte-identical
message across all three is impossible -- and faking one would mean telling a Go
developer to write `presence="required"`.

So the contract pins the **sentence**, and substitutes each language's own
spellings inside it:

| Implementation | Text |
|---|---|
| Python | `Flag "x": presence is undeclared: declare exactly one of presence="required", presence="optional", or default=<value>` |
| Go | `Flag "target": presence is undeclared: declare exactly one of Required(), Optional(), or Default(<value>)` |
| TypeScript | `Flag "x": presence is undeclared: declare exactly one of presence: "required", presence: "optional", or presence: "default" with default: <value>` |

Same sentence, word for word, with three spellings substituted into it. The same
rule governs the whole family -- the declared-twice error, the variadic-default
error, the null-default redirect (whose parenthetical also substitutes its noun:
`Flag` messages say "when the flag is absent", `Arg` messages say "when the arg
is absent"), and every choice-flag guard:

| Implementation | Text |
|---|---|
| Python | `Flag "via": a choice flag cannot declare presence="optional": an absent selection is a choice nobody named, so name it as a choice of its own` |
| Go | `Flag "via": a choice flag cannot declare Optional(): an absent selection is a choice nobody named, so name it as a choice of its own` |
| TypeScript | `Flag "via": a choice flag cannot declare presence: "optional": an absent selection is a choice nobody named, so name it as a choice of its own` |

This has a consequence for how conformance is run. The cross-language
error-parity check compares templates across implementations, so a template that
carries a per-language spelling cannot be compared that way: each of the three
carries only its own, and the other two record it as an `excluded:` entry in
`conformance/check_error_parity.py` with the rationale written out. The
assertion still happens -- it just happens **per target**, in
`conformance/cases/presence_registration.json`, where every implementation is
required to produce its own exact line.

## Errors only one language can produce

Two members of the presence family exist in exactly one implementation each, and
not because two implementations skipped them.

**Python only.** Python spells the declaration as a keyword taking a string, so
`presence="defualt"` and `presence=3` are writable:

```
Flag "x": presence must be "required" or "optional", got 'defualt'; a default value is declared with default=<value>
```

Go's three sibling options and TypeScript's discriminated union have no input
that could carry a bad presence value. There is nothing to mistype.

The constraint system produces the same asymmetry for the same reason. A
member's election selector is a Python keyword taking a string, so
`Member("all", when="tru")` is writable, and it is refused where it was written
-- in the record's own constructor, before registration order ever runs:

```
Member "all": when must be "present", "true" or "non_empty", got 'tru'
```

Go declares that selector with `WhenTrue()` / `WhenPresent()` / `WhenNonEmpty()`
and TypeScript with a literal union, so a typo is a compile error in both and
neither has an input that could produce the sentence. Note what this template is
*not*: the cross-language guards all name a declared selector that cannot apply
to the member's **type** -- `when: "true"` on a string, `when: "non_empty"` on an
int -- and every implementation raises those. This one names a typo in the
keyword's own vocabulary, which only one spelling can carry.

**TypeScript only.** TypeScript is the only language whose default spelling has
two parts, so a widened options object can reach the factory carrying the
discriminant and not the value:

```
Flag "x": presence: "default" requires a default value: declare default: <value>, or presence: "optional" for no value
```

Python's `default=<value>` and Go's `Default(v)` *are* the value; a half-written
default declaration is inexpressible there.

Each of these names a state only one language's spelling can reach. A sibling
has no input that could produce the message, and asserting parity over it would
be asserting that two implementations carry text no code path can print. Their
absence elsewhere is a consequence of the spelling, not a parity defect -- and
the conformance suite records it as such.

## A fifth case: the retired choice

The **retired choice** is the smallest of these cases, and the one that shows
what the pattern costs when the semantic is simple. A value flag or positional
arg that declares `choices` may also declare the spellings it used to accept,
each carrying the message that names its replacement; a value matching one is
refused ahead of the invalid-value check, with the replacement named. One
semantic, three records.

**Python** -- a keyword taking records, beside the `Choice` records `choices`
takes:

```python
@strictcli.flag("format", type=str, presence="required", help="Output format",
                choices=[strictcli.Choice("text"), strictcli.Choice("json")],
                retired_choices=[
                    strictcli.RetiredChoice("xml", message="use 'json'"),
                ])
def convert(ctx, format):
    ...
```

**Go** -- a functional option beside `Choices`, over records beside `Ch`:

```go
strictcli.StringFlag("format", "Output format",
    strictcli.Choices(strictcli.Ch("text", ""), strictcli.Ch("json", "")),
    strictcli.RetiredChoices(strictcli.RetiredChoice("xml", "use 'json'")),
    strictcli.Required(),
)
```

**TypeScript** -- a member of the options object, typed as the readonly
non-empty tuple `choices` already uses:

```ts
format: flag("format", t.str, {
    help: "Output format",
    choices: [{ value: "text" }, { value: "json" }],
    retiredChoices: [{ value: "xml", message: "use 'json'" }],
    presence: "required",
}),
```

Two things are worth reading off this one.

**The entry-shape refusal is Python-only, and that is the spelling talking.**
Python's keyword takes a list of anything, so `retired_choices=["xml"]` is
writable and is refused at registration:

```
Flag "format": retired_choices entry 0 is a bare value: declare it as RetiredChoice(<value>, message="<message>")
```

Go's variadic `...RetiredChoiceValue` parameter and TypeScript's
`RetiredChoiceRecord` tuple make the same mis-declaration a compile error, so
neither sibling has an input that could produce the sentence. It joins the
family in [Errors only one language can produce](#errors-only-one-language-can-produce).
The six *rules* -- a live spelling, a duplicate, an empty message, a bool
carrier, a retired default, retired choices with no choices -- name the concept
rather than any language's spelling of the declaration, so all three carry one
sentence per rule and the parity manifest records nothing for them.

**No exhaustiveness story is needed, and saying so is part of the design.**
New surface ships with its exhaustiveness story stated per language, and here
the story is that there is none to tell: a retired value never reaches a
handler, because the invocation naming one never gets that far. Nothing
delivers a closed set to a consumption site, so there is no `match`, no
`Match`/`When`, and no `switch` with `assertNever` to write. The requirement is
to state the answer, not to manufacture a construct that has nothing to be
exhaustive over.

## The same shape, elsewhere in the framework

Presence and the choice flag are the richest examples, not the only ones.

**Effect classification** is mandatory on every command, and each language
declares it its own way. Python takes a closed-enum keyword,
`effect="read_only"` or `effect="mutating"`. Go takes a functional option with a
typed constant, `WithEffect(EffectReadOnly)`. TypeScript takes neither -- it has
**twin factories**, `defineReadOnlyCommand` and `defineMutatingCommand`, which
is what lets the classification reach the type system: `defineReadOnlyCommand`
narrows the handler's `ctx` to a `ReadOnlyContext` whose `effects` member
exposes only `run`, so a `.write()` inside a read-only command is a **compile**
error. The fixtures that pin it live in `typescript/tests/negative/` and are
compiled through `tsc` by `typescript/tests/negative_types.test.ts`.

Every implementation still carries the runtime seal regardless -- a plain-JS
consumer bypasses the type system entirely -- so the semantics are identical and
TypeScript simply catches it earlier. One language got a compile-time
enforcement out of its own idiomatic surface. That is a pro.

**Go got the mirror image of that trade.** The four `Unsettled`-family effect
carriers each begin with a `_ [0]func()` field whose only purpose is to make the
struct non-comparable, so `a == b` on a carrier is a compile error rather than a
silently wrong answer. No runtime test can observe that field -- deleting it
changes no behavior -- so the pin is a compile-FAIL package,
`go/strictcli/testdata/noncomparable/`, which `carrier_noncomparable_test.go`
compiles on purpose and asserts one diagnostic per fixture, naming the field
verbatim:

```
invalid operation: a == b (struct containing [0]func() cannot be compared)
```

The comment on the Go test says where the idea came from: it is modelled on the
TypeScript sibling. Techniques travel between the implementations. Spellings do
not have to.

## The best way, the only way

None of this is an argument for looseness. It is the opposite. Each
implementation is strict in its own language's terms, and the strictness is what
makes the idiom worth having.

- **No implicit defaults.** Presence is declared three ways and derived zero
  ways. Before the round, the shape of a `default=` declaration was silently
  read as a statement about whether a value had to be supplied -- in three
  mutually incompatible ways; now the declaration says it, in every language, or
  the program does not register.
- **Help text is mandatory** on every app, group, command, flag and arg. Empty
  help is a registration-time error, in all three.
- **Four types only** -- `str`, `bool`, `int`, `float` -- parsed strictly, with
  NaN and Inf rejected.
- **Registration-time hard errors**, in each language's own failure idiom:
  `ValueError` in Python, a panic in Go, a throw in TypeScript. The failure
  happens where the mistake was written, not on the day a user types the flag.
- **Named-and-banned flag names.** `--yes` is banned outright, with a message
  pointing at `--approve-consequential`, so a private confirmation skip cannot
  be reintroduced under a different spelling. A bare `--force` is refused with a
  redirect to a qualified name (`--force-overwrite`, `--force-delete`), because
  "force" alone never says what is being forced.
- **No escape hatches.** The consent flag `--approve-consequential` answers the
  confirmation in advance and does nothing else; there is no flag that turns a
  registration error into a warning, and none should ever be added.

A framework that made these rules optional in order to keep three languages
easy to synchronize would be worse in all three. Making the best way the only
way is what each of the three surfaces is *for*.

## If you are extending strictcli

The rule for new surface follows from everything above: design **three
idiomatic forms** for one pinned semantic. Ask what a Python developer, a Go
developer and a TypeScript developer would each expect to write, and write those
three. Do not pick one language's shape and transliterate it into the other two,
and do not reach for a lowest-common-denominator spelling because it is easier
to keep in step.

Parity work then binds the semantics, the rendered bytes, the schema fields, and
the sentence of every message -- with each language's own spellings substituted
inside it. When a mis-declaration is expressible in only one language, that
language gets an error template the others do not have, recorded as an
`excluded:` entry with its rationale and asserted per target in a conformance
case.

A difference in what you type is not a defect to be filed. A difference in what
happens is.
