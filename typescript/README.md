# strictcli

A CLI framework for the Era of Agents: nothing is inferred, everything is declared. First-class support for Go, Python, and TypeScript (TypeScript implementation).

strictcli takes the opposite stance from convention-over-configuration CLI
libraries: every command, flag, argument, type, default, and help string is
declared explicitly, and anything left unstated is a hard error at registration
time. No implicit defaults, no guessed types, no silently ignored input. The
result is a CLI whose behavior is fully determined by its declaration.

This package is the **native TypeScript implementation**: pure ESM, Node >= 22,
with full static type inference from flag declarations all the way to handler
arguments — declare `flag("count", t.int, { help: "...", presence: "required" })`
and your handler's `args.count` is a `bigint`, checked by the compiler.

There are Python and Go implementations too, and this one is not a port of
either. The surface here is TypeScript's own — flag and arg options are a
discriminated union on `presence`, so a `default` written beside
`presence: "required"` does not compile and a `presence: "default"` without a
`default` does not either, and the twin command factories narrow the handler's
`ctx`, making a `.write()` inside a read-only command a compile error. That
share of the enforcement reaching the compiler is TypeScript's alone; the
runtime checks every implementation carries are still there underneath. What the
three hold identical is behavior: the same semantics, the same help bytes, the
same schema, and the same error sentence with TypeScript's spellings inside it.

## Install

```sh
npm install strictcli
```

## Usage

Types are carriers (`t.str`, `t.bool`, `t.int`, `t.float`, plus `t.list(...)`
and `t.dict(...)`) that bind the schema, the runtime parser, and the inferred
TypeScript type in one value, so they cannot drift apart:

```ts validate
import { arg, createApp, defineMutatingCommand, flag, t } from "strictcli";

const app = createApp({
	name: "myapp",
	version: "1.0.0",
	help: "my cool app",
});

app.command(
	defineMutatingCommand("build", {
		help: "Build the project",
		flags: {
			count: flag("count", t.int, {
				help: "How many times to build",
				presence: "required",
			}),
			label: flag("label", t.str, {
				help: "Build label; the handler uses dev when it is not supplied",
				presence: "optional",
			}),
		},
		args: [
			arg("values", t.float, {
				help: "Input values",
				variadic: true,
				presence: "required",
			}),
		],
		handler: (args, ctx) => {
			// Inferred: args.count is bigint, args.label is string | undefined,
			//           args.values is number[]
			ctx.info(`building ${args.count} time(s) as ${args.label ?? "dev"}`);
			if (ctx.dryRun) {
				ctx.info("(preview only)");
			}
			return 0;
		},
	}),
);

app.run(process.argv.slice(2));
```

Commands are registered through the **twin factories**: `defineReadOnlyCommand`
for commands that change nothing, `defineMutatingCommand` for commands that do.
Classification is mandatory and has no default, so the factory name *is* the
classification, and a read-only command's handler `ctx` is narrowed such that
calling a mutating effect on it is a compile error.

Every flag and every arg declares **exactly one** of three facts about itself:
`presence: "required"` (a value must be supplied), `presence: "optional"`
(absence is legal and arrives as `undefined`), or `presence: "default"` with a
`default` value the framework supplies when nothing else does. Declaring none
of the three, or two of them, is a registration-time hard error, and nothing
about presence is inferred from the shape of another declaration. An optional
declaration is what makes a key optional in the inferred handler-args type, and
`ctx.provided(name)` answers whether the invocation — CLI, env, config or an
implication — supplied the value, rather than the declaration.

**"Exactly one of these" is a choice flag**, not a constraint over independent
flags. `choiceFlag(name, choices, opts)` puts the choice map where a carrier
sits — for a choice flag, the choices *are* the type — and each choice declares
a scope: the flags that exist only while it is elected. Object-literal keys are
literal types, so the delivered value is an exact discriminated union with no
annotation anywhere, `switch (args.via.choice)` narrows, and `assertNever` in
the default branch is checked by the compiler:

```ts
via: choiceFlag("via", {
	email: choice({ help: "deliver as an email message", flags: {
		subject: flag("subject", t.str, { help: "subject line", presence: "required" }),
	}}),
	sms: choice({ help: "deliver as a text message", flags: {
		phone_number: flag("phone-number", t.str, { help: "destination number", presence: "required" }),
	}}),
}, { help: "Delivery channel", short: "v", presence: "required" }),
```

`myapp send --via email --subject hi` parses; `myapp send --via sms --subject hi`
says *`--subject` is only valid under `--via email`* — never "unknown flag".
`memberChoiceFlag(...)` is the member-spelled twin factory, where each choice is
its own flag (`--profile work` / `--all-profiles`) and election is command-line
only. A choice flag declares `presence: "required"` or a `default` typed
`keyof C & string`; `optional` has no union member at all, because an absent
selection is a choice nobody named. Scoped flags are never top-level `args`
keys, at any depth, and a choice flag may be declared inside a choice's scope
without limit.

### Constraints

Four declared rules, all passed via `constraints: [...]`, all taking one option
object with a **mandatory name** -- the name is what a violation prints and what
`--help` shows, and it is what lets one constraint be a member of another.

- `atLeastOne({ name, members })` -- at least one member is engaged. Members may
  co-occur; it has no upper bound and is never exclusivity
- `allOrNone({ name, members })` -- either every member is engaged or none is.
  Nothing engaged is vacuously satisfied
- `requires({ name, flag, dependsOn })` -- one-way dependency
- `implies({ name, flag, implies, value })` -- auto-set a bool flag when another
  is provided; explicit contradictions are parse errors

A **member** is a plain object literal `{ name, when? }` naming a command flag,
a positional arg, or another named at-least-one or all-or-none (nesting is a
cycle-checked DAG). `members` is typed
`readonly [ConstraintMember, ConstraintMember, ...ConstraintMember[]]`, so a
one-member constraint is a compile error, and `when` is the literal union
`"present" | "true" | "non_empty"`, so a typo is one too. `when` says when a
member counts as engaged -- `"present"` (the default; the value was provided by
the invocation, never by a declared default), `"true"` (bool only),
`"non_empty"` (strings and collections) -- and a **bool member must declare it
explicitly**, so `--no-all` can never engage a constraint while selecting
nothing.

```ts
constraints: [
	allOrNone({ name: "author-name", members: [{ name: "old-name" }, { name: "new-name" }] }),
	allOrNone({ name: "author-email", members: [{ name: "old-email" }, { name: "new-email" }] }),
	atLeastOne({ name: "author-change", members: [
		{ name: "author-name" }, { name: "author-email" },
	]}),
],
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
`presence: "required"` -- a member the invocation must always supply leaves the
constraint nothing to decide. Constraints reference root-scope flags and args
only: naming a flag declared inside a choice's scope is a registration error,
because the scope already IS the constraint, and the reserved quartet is not
declarable so it can never appear in one. Every constraint renders in `--help`
under a `Constraints:` section, is published in `--dump-schema`, and is
projected into MCP tool schemas (`anyOf` / `dependentRequired`) with anything a
JSON Schema keyword cannot carry stated in the tool description instead.

Note that `--dry-run`, `--approve-consequential`, `--quiet` and `--verbose` are
framework-owned names. You never declare them; their values arrive on the
context as `ctx.dryRun`, `ctx.approveConsequential`, `ctx.quiet` and
`ctx.verbose`.

## Features

- **Strict four-type system** — `str`, `bool`, `int`, `float`, with `int` backed
  by `bigint` for full 64-bit signed integer range and strict parsing (no
  whitespace, no overflow wraparound).
- **Static handler-arg inference** — dash-named flags (`--dry-run`) arrive as
  underscore keys (`dry_run`) with exact types and true optional-key modifiers,
  derived entirely from the declarations.
- **Mandatory help everywhere** — missing help text on any app, group, command,
  flag, or argument is a registration-time error.
- **Mandatory effect classification** — every command is registered through
  `defineReadOnlyCommand` or `defineMutatingCommand` (and passthroughs through
  `readOnlyPassthrough` / `mutatingPassthrough`). There is no default and no
  inference from names, tags, or handler bodies.
- **Honest dry runs** — `ctx.effects` mints eight recorded operations (`run`,
  `spawn`, `write`, `mkdir`, `remove`, `rename`, `chmod`, `http`). Under
  `--dry-run` they are recorded rather than performed and rendered as a would-do
  log. A command whose preview would lie declares `dryRunSupported: false` with
  a mandatory reason, and `--dry-run` is refused at parse time instead.
- **A confirm protocol you opt into** — `consequential: true` is the only thing
  that makes the framework prompt before dispatch; `--approve-consequential`
  answers it in advance, and a non-interactive stdin without it is a hard error
  rather than a hang.
- **Env var and JSON config file resolution** — explicit precedence:
  CLI > env > config > default, with auto-registered `config show/set/path/edit`
  subcommands, where `config set <key> --value <v>` writes under a required
  selector over a value, a clear (`--clear`) and a reset to the declared default
  (`--default`).
- **First-class check system** — TOML-declared checks with double-entry
  registration, tag DSL selection, DAG-ordered execution.
- **MCP server integration** — expose commands as Model Context Protocol tools.
- **Schema dump** — every app answers `--dump-schema` with a machine-readable
  JSON description of its full structure, at `schema_version: 2`. Every flag and
  arg entry carries a `value_schema`: a real JSON Schema fragment from a closed
  subset of `type`, `items`, `additionalProperties` and `enum`, using JSON
  Schema's own type names. Arity is part of the value's shape, so a `t.list(...)`
  flag and a variadic arg publish the identical array fragment. A choice flag
  carries no fragment — its value is a variant the subset cannot express — and
  publishes its nested `choices` and scopes instead, each scoped entry a full
  flag entry, with `elect_by` marking the spelling. Keys are emitted in a
  declared order at every depth and the document is written in one canonical
  encoding, so a schema file written by this implementation and one written by
  the Python or Go implementation for the same declaration are byte-identical.
- **Choice flags** — elect exactly one of a flag's declared choices, each choice
  owning a scope of flags legal only while it is elected, in either spelling
  (`choiceFlag` for `--via email`, `memberChoiceFlag` for `--profile work` /
  `--all-profiles`). Delivery is a derived discriminated union, `provided(record,
  name)` answers per-field provided-ness, and recursion is unlimited.
- **Named constraints** — at-least-one, all-or-none, requires and implies, all
  declared through `constraints: [...]` with a mandatory name. A member is a
  reference by name to a flag, a positional arg or another named constraint,
  nested to unlimited depth, with a declared election selector saying when it
  counts. Constraints render in `--help`, publish their members in
  `--dump-schema`, and project into MCP tool schemas with any remainder stated
  in the tool description.
- **Update commands** — `updateOf: { resource, writeMode, identity, properties }`
  declares what a command changes: which flags and args name the instance, which
  flags carry the changes, and whether the write is sparse or a full replace.
  `properties` is typed over the command's own declared names with a floor of
  one, so a property naming a flag the command does not declare — and an empty
  list — are compile errors. Because absence must never resolve to a value the
  invocation did not state, no flag or arg on a mutating command may declare a
  value default. The framework enforces at least one property per invocation,
  renders the write set on every surface a run reports through (one line in the
  would-do log, a `writes` member on the machine envelope), and publishes the
  declaration in `--dump-schema` and in MCP tool schemas. A property declaring
  `nullable: true` mints `--unset-<prop>`, answered by `ctx.unset(name)`.
- **Groups, passthrough commands, deprecation notices** — the complete strictcli
  surface.

## Sibling implementations

strictcli is developed in the [stricttools/strictcli](https://github.com/stricttools/strictcli)
monorepo alongside first-class **Python** (PyPI: `strictcli`) and **Go**
implementations. All implementations are kept byte-identical in behavior — same
error messages, same help output, same parsing rules — enforced by a shared
cross-language conformance suite.

## License

MIT
