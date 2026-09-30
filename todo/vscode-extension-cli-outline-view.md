# A VS Code extension showing an interactive outline of a strictcli app

## Context

Every strictcli app, in all three implementations, publishes its whole
declared surface as the help document: `<app> help --json` prints it at
`schema_version: 2`, and many consumers commit it as
`.strictcli/schema.json`. The document is fully declared and byte-canonical
across Python, Go, and TypeScript (see `stricttools/docs/architecture.md`,
"The schema format"). Per command it carries the mandatory `effect`
(`read_only` or `mutating`), `consequential`, `dry_run_supported` with its
reason, `update_of` and `write_mode`, `payload_schema`, `owns_stdout`,
`passthrough`, `tags`, `constraints`, `grants`, `forwarding`, `requires`,
`hidden`, `interactive`, and `config_fields`; per flag and arg it carries
`presence`, `value_schema`, and, for choice flags, nested `choices` with
`elect_by`. At app level it carries `global_flags`, `groups` (recursive),
`deprecated`, `tag_contracts`, `checks`, `config_fields`, and `infra`.

That is the complete, declared shape of a CLI: what it can do, which parts
write, which need human approval, and which constraints bind its flags. A
developer or agent editing a strictcli app sees none of it in the editor.
They read the declaration code (spread over files and, in Go, over
functional options), or run `help` at several addresses, or read a
long JSON file.

A VS Code extension can render that document as a navigable tree and keep it
in sync with the code, which fits the framework's premise (declare
everything, infer nothing): the declarations are already machine-readable,
so an editor view costs no inference.

## Problem

- No view shows an app's command tree with its effects at a glance, for
  example "which commands under this group are mutating, which are
  consequential, and which refuse dry run".
- Reviewing a change to declarations means diffing `.strictcli/schema.json`
  as text; the structural meaning of the diff (a command became mutating, a
  flag lost its default, a constraint was added) is not surfaced.
- Nothing links a tree node back to the declaration in source, in any of
  the three languages.

## Proposal

A VS Code extension (TypeScript, as every VS Code extension is) that reads a
help document and presents it.

### Features

1. **Outline tree view** (`vscode.TreeDataProvider`, contributed to the
   Explorer or to its own view container): App, then groups (recursive),
   then commands, then flags, args, choice scopes (nested to any depth), and
   constraints. Each node shows its help text as a tooltip and badges for
   the facts that matter to a reader: effect, `consequential`, dry run
   refused, update command with its write mode, passthrough, hidden,
   deprecated, presence of each flag (`[required]`, `[optional]`,
   `[default: v]`), and declared requirements.
2. **Filters and grouping**: show only mutating commands, only consequential
   ones, only commands carrying a tag (the check system's tag DSL could be
   reused for the filter expression), or only commands with a payload
   schema.
3. **Detail panel** (a `WebviewPanel` or the tree's own description and
   tooltip): the full entry for the selected node, including the rendered
   `value_schema`, constraints as the `Constraints:` section of `--help`
   renders them, and the payload schema.
4. **Structural diff**: compare the working-tree `.strictcli/schema.json`
   against its committed version (through the built-in Git extension's API)
   or against a fresh `help --json`, and mark added, removed, and changed
   nodes in the tree, with a summary of meaning-level changes (effect
   changed, presence changed, flag removed).
5. **Stale-schema diagnostic**: when the committed `.strictcli/schema.json`
   differs from what `help --json` prints now, show a diagnostic on the file
   (`DiagnosticCollection`) naming the first differing path. This mirrors
   what release tooling checks, but at edit time.
6. **Go to declaration**: from a node to its declaration in source. See the
   solutions below; this is the hardest feature.
7. **Framework-use lint as diagnostics** (optional, later): run the app's
   reserved `--lint-framework-use` and map its `<path>:<line>: <rule>:
   <message>` lines to editor diagnostics.

### Where the document comes from

- **From the committed `.strictcli/schema.json`**: no execution, instant,
  but only as fresh as the last dump.
- **From running `<app> help --json`**: always fresh, but executes the
  consumer's program, which needs a command line to be configured per
  workspace (the extension must refuse, not guess, when a workspace holds
  more than one strictcli app and none is selected) and needs VS Code's
  workspace trust (`capabilities.untrustedWorkspaces`), since it runs code.

Both sources should be available, chosen explicitly per workspace, with the
chosen source shown in the view's title. No silent switch from one to the
other when one fails.

## Solutions for "go to declaration"

1. **Search source text for the declared name** (the command name string
   literal, the flag name).
   - Pro: no framework change.
   - Con: guessing; ambiguous for shared names (a flag declared on several
     commands, flag sets), which contradicts the framework's "refuse over
     guess" stance. At best a fallback the user triggers knowingly, listing
     every match.
2. **Record declaration source locations in the framework and publish them
   in a separate dev-only document** (for example a `help --json` variant
   or an in-process dump), never in the canonical help document, since
   locations differ per language and would break the three-way byte
   comparison.
   - Pro: exact; one mechanism for all three languages.
   - Con: framework work in three implementations with conformance
     coverage; capturing call sites differs per language (Python
     `inspect` frames, Go `runtime.Caller`, TypeScript stack traces or a
     compile-time transform), and a new surface needs a design decision.
3. **Language-specific static analysis** in the extension (Python AST, Go
   `go/ast`, TypeScript compiler API) that finds declaration calls.
   - Pro: no runtime execution.
   - Con: three analyzers to maintain against three declaration surfaces
     that deliberately differ in idiom; a large ongoing cost.

Solution 2 is the most correct; solution 1 is acceptable only as an
explicit, list-every-match command.

## Where the extension lives

1. **A new sub-project in this monorepo** (for example a directory next to
   `typescript/`, dev-only or its own releasable).
   - Pro: the document format and the extension change in one commit; a
     conformance or freshness check can validate the extension's parser
     against dumps from all three targets.
   - Con: a fourth kind of artifact in the workspace, with a publishing
     path rlsbl does not have.
2. **A separate repository in the family.**
   - Pro: independent release cadence.
   - Con: the extension can drift from `schema_version`; it must refuse any
     `schema_version` it does not know rather than render a guess.

Either way, the extension reads `schema_version` and refuses unknown
versions with a message naming the version it found and the versions it
supports.

## Distribution

- Publish to both the **VS Code Marketplace** (`vsce publish`) and
  **Open VSX** (`ovsx publish`), so VS Code and its derivatives (VSCodium,
  Cursor, and others that install from Open VSX) all get it.
- rlsbl has no VS Code extension publishing target yet; the release either
  needs one added to rlsbl or a separate publishing step, to be decided.
- The extension name, publisher ID, and package name are to be chosen.

## Affected files and new components

- New: the extension (package manifest, tree data provider, document
  loader and validator for schema version 2, webview for details, diff
  logic), with tests against help documents dumped by the conformance
  harnesses of all three implementations.
- If solution 2 for go to declaration is chosen: declaration-site capture
  in `python/strictcli/`, `go/strictcli/`, and `typescript/src/`, a
  published contract section, and conformance cases.
- `stricttools/docs/`: a page describing the extension, once it exists.

## Effort estimate

- Tree view, badges, filters, and detail panel from a committed schema
  file: two to three days.
- Running `help --json` with workspace trust and explicit app selection,
  plus the stale-schema diagnostic: one to two days.
- Structural diff: two days.
- Go to declaration through framework-recorded locations: three to five
  days across the three implementations with conformance.
- Publishing to both registries and wiring a release path: one day, plus
  any rlsbl work for a publishing target.
