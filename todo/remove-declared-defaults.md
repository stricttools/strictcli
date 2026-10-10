# Remove declared defaults (`Default(v)`) from the Go API

## Context

The owner's rule against implicit defaults now covers every value: a value the program chooses that the caller did
not state is banned, including a default written into a flag's declaration and shown in help (`[default: 20]`),
because the caller never stated it. The owner ruled that strictcli removes declared defaults: a flag or argument
declares required or optional only, and optional means the handler receives nothing when it is absent and must not
substitute a value.

A read-only tally of the family's 22 Go programs (in this repository's
`experiments/defaults-and-compounds/out/`, scripts in `../scripts/run-all.sh`) found 132 declared defaults and 795
optional flags or arguments whose absence handler code turns into a value, through about 35 hand-written absence
helpers across 13 programs (the framework gap this repository's CLAUDE.md already describes). strictcli's own text
currently tells authors to "apply the fallback in the handler" when the mutating-default ban refuses a default.

## Required change

- Delete `Default(v)` (and `ArgDefault(v)`) and every code path that applies a declared default; `Required()` and
  `Optional()` remain. Delete the empty-collection exception to the mutating-default ban, choice flags' default
  choice, and `RelativeToRoot` defaults on flags; delete `[default: ...]` from help and `presence: "default"` from the
  help document (a help-document schema version change).
- Rewrite every refusal text that names a default or a handler fallback as the fix; the fix becomes stating the
  value, or a file the user writes.
- The config subsystem's own implicit behavior must change with it (from the same tally): its path falls back
  through `$XDG_CONFIG_HOME`, `~/.config`, and `$HOME`; a missing file is silently empty; format and conflict mode
  default; config fields can declare defaults; `config set --default` and `config init`'s default comments depend on
  defaults; `config edit` falls back to vi; keys are global by flag name. Open: where a config file's path comes from
  when naming it on every call is unbearable and an environment variable does not count as stated.
- Open: a way to state "explicitly empty" for list flags (72 entries need it), the framework-reserved flags
  (`--dry-run`, `--json`, `--quiet`, `--verbose`, `--hermetic`, which are absent-means-off on every call), and
  update-command properties (absence means "leave unchanged", which is not a default).
- Docs and quickstarts that teach defaults; `~/Projects/CONTEXT/building-a-cli.md` has been updated already.

## Migration

Every consumer's declared defaults and handler fallbacks move to a mandatory flag, app config, or a TOML input file
the user writes (the tally assigns each entry a destination with a reason); busy commands such as safegit's
`commit`, rlsbl's `changelog add`, and saferm's `delete` need redesigns rather than more mandatory flags. A separate
effort is removing the easy cases in consumers ahead of this change.

## Effort estimate

strictcli: about 2 to 4 days including the config subsystem; consumers: per the tally.
