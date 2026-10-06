# A framework-owned `self` command group: update the binary and migrate its data

## Context

Tools built on strictcli keep data whose format changes between releases:
saferm's archive database and archive directory, selfdoc's documentation layout
in a repository, rlsbl's scaffolded files in a repository, and the
command-line schema and test-coverage files strictcli itself writes into every
repository. Each tool handles a format change on its own, in its own way:

- saferm migrates its database when a new binary first opens it (numbered
  migrations keyed on `PRAGMA user_version`);
- selfdoc has a dedicated `selfdoc layout migrate` command;
- rlsbl records the scaffold version in `.rlsbl/version` and merges against
  stored bases on the next `rlsbl scaffold`;
- a move of strictcli's own `.strictcli/` directory into `.strictmetadata/`
  needs yet another migration path.

Each of these is a separate surface an agent must discover, and none shares a
dry run, a verification step, or an error message with the others. While
everything is pre-stable, format changes delete the old surface and provide an
explicit migration path; after graduation, migrations become permanent. The
mechanism should be built and exercised now, while breaking changes are cheap.

## Proposal

### One framework-owned command group: `self`

strictcli reserves a `self` command group, the way it reserves `help`, so every
app gets the same surface and an agent learns it once. `self` follows an
established convention (`uv self update`, `rustup self update`) and avoids
colliding with apps' own commands. `update` and `upgrade` were rejected as
top-level names: strictcli's update construct (`WithUpdateOf`, contract §27)
exists so apps can declare their own `update` commands, so a reserved `update`
would collide with exactly the commands strictcli helps apps build.

- `<app> self update --to <version|latest>`: installs the requested version of
  the binary, then the newly installed binary runs every pending data
  migration step (only the new binary knows the new steps). `--to` is
  required, with no default. Supports `--dry-run` through the effects regime:
  steps written against the effects handle preview themselves.
- `<app> self status`: installed version, latest published version, how the
  binary was installed, each store's recorded data version, and the pending
  steps.
- A separate `detect` command is not needed: `status` reports the same facts.

Open: whether `self` is the final name.

### Stores

A tool registers the stores it owns. Each store declares whether it is
repository-owned (committed files shared by sessions and tools, such as a
documentation layout) or machine-owned (local state such as saferm's database).
The classification is per store, not per tool: one tool can own both.

- A repository-owned upgrade commits through safegit and takes a lock, so two
  sessions upgrading the same repository cannot collide.
- A machine-owned upgrade is an atomic write.
- A library owns no stores; its host app owns the data.

### The data version is the app version

Each store is stamped with the app version that last migrated it. No separate
data-version counter exists. A stamp is required because, after the binary is
updated, nothing else records which version last wrote the data, and guessing
the old version from the data's shape is refused (refuse over guess).

### Steps

A step is registered at the release version that introduced it ("steps for
1.4.0"). Upgrading data stamped 1.2.3 with binary 1.6.0 runs every step
registered for releases above 1.2.3 up to 1.6.0, in order. Each step has:

1. apply: the change, written against the effects handle;
2. verify: a postcondition proving the store is now in the new format and
   stable (for example an integrity check, or every record's type matching its
   archived entry); a failed verify stops the chain with a hard error naming
   the step, leaving earlier completed steps committed;
3. a fixture of the old format, used by tests that run the step and the whole
   chain.

For a step that cannot be fully atomic (files plus a database), the framework
offers the pattern saferm's archive migration uses: plan every change first,
write the new form, commit the database, delete the old form last.

### Refusals

Every ordinary command of an app refuses data older than the binary, with one
framework-generated sentence naming `<app> self update`. The sentence is
executed in a test, per the fix-instruction rule.

### How the binary was installed

`self update` must know the installation channel; it never guesses:

- Artifacts built by rlsbl in CI carry a link-time stamp (`-ldflags -X`) naming
  version, commit, and channel (GitHub release archive, Homebrew, npm package,
  PyPI package). See the companion rlsbl todo
  (`self-update-channel-stamps-and-step-rules.md`).
- A binary installed with `go install module@v0` cannot be stamped, but Go's
  embedded build info (`debug.ReadBuildInfo`) shows a real module version and
  no stamp, which identifies it unambiguously.
- A local development build shows `(devel)` or a pseudo-version, plus
  `vcs.modified` when dirty; `self update` refuses it with a hard error naming
  the checkout's own install step.
- Channels owned by another package manager (Homebrew, npm, PyPI) are not
  overwritten in place, since the manager's records would then disagree with
  the disk. Open: run the manager's own command (`brew upgrade <app>`,
  `npm install -g <app>@latest`, `uv tool upgrade <app>`) or refuse and name
  it.
- Looking up `latest` queries the registry only for published versions; never
  query a registry about an unpublished version of our own modules.

### Version rules

Enforced at release by rlsbl (companion todo): a patch release registering a
step is refused; after graduation a minor release may register only additive
steps; a major release (or any pre-stable minor) may register any step; a
breaking changelog entry that changes a stored format without a registered
step is refused. While pre-stable, old steps may be pruned; after graduation
they are kept permanently.

## Future consumers (not part of this todo)

- saferm's migration on open becomes registered steps on its database store.
- `selfdoc layout migrate` becomes steps on selfdoc's layout store, and the
  separate command goes away.
- rlsbl's scaffold version bump becomes steps on its scaffold store.
- The move of `.strictcli/` into `.strictmetadata/.cli-schema/` and
  `.strictmetadata/.cli-test-coverage/` could be the first step strictcli
  registers for its own repository-owned store.

## Open questions

- The final name of the group.
- Reverse steps for downgrades (`--to` an older version is refused unless
  every step in between has a reverse).
- Cross-tool steps: a format owned by strictcli but stored in repositories that
  rlsbl and selfdoc also read. Which tool runs the step, and when?
- Whether package-manager channels are handed off or refused.
- How stores are declared (API shape) and where each store's stamp lives.

## Scope and effort

Go strictcli only, per the Great Refinement. Large: a new reserved group, a
store and step registry, stamping, a chain runner with verify and dry run,
channel detection, and refusal generation, each with red-green tests. Best
designed through a ladder of solutions before building.
