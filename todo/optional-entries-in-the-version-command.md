# Optional app-declared entries in the framework's `version` command

## Context

strictcli reserves the command name `version` for the framework: `app version`
prints the program's name and version, and `app version --json` prints
`{"name", "version"}`. An app can no longer register its own `version` command;
registration panics on the reserved name.

safegit had its own `version` command that also reported the versions of what
it runs on: the Go toolchain it was built with, the operating system, and the
`git` executable it drives. Moving safegit onto the reserved command drops
those extra lines, and the owner has ruled that they should come back as
optional entries of the framework's `version` command rather than as a safegit
command under another name.

## Problem

The framework's `version` output is fixed. An app has no way to add facts that
belong next to its version, such as:

- the version of an external program it requires (`git --version`);
- build information (Go toolchain, commit, build date where the app records
  them);
- the operating system and architecture.

Without a declared extension point, apps either drop that information or put it
in some other command, so an agent looking for "what is this program and what
does it run on" has to know a different command per app.

## Options

1. **Declared entries with a value function.** The app declares named entries,
   each with help text and a function returning its value, e.g. Go
   `WithVersionEntry("git", "version of the git executable", func() (string, error))`.
   Human mode prints them after the name and version; `--json` adds them as
   members of the version document. An entry whose function fails is reported
   as a hard error naming the entry, never silently omitted.
   - Pro: open to any fact an app needs; one place in every app.
   - Con: arbitrary code runs inside `version`; the document's key set becomes
     app-specific, so its schema must publish the declared entries.
2. **Built-in entries the app opts into.** The framework offers a closed set
   (Go toolchain, operating system and architecture, and the version of each
   declared runtime requirement such as an external program), and the app
   chooses which to show.
   - Pro: uniform across apps; no app code runs; ties into declared runtime
     requirements, which already know the external programs an app needs.
   - Con: cannot carry a fact the framework did not anticipate.
3. **Both:** the closed built-in set plus declared custom entries.

Requirements whatever the option: the entries appear in `--help` for
`version` and in the help document; identical behavior in the Go, Python, and
TypeScript ports with conformance cases; no entry is ever dropped silently.

## Affected files

- The `version` command's implementation in each port (`go/strictcli/`,
  `python/strictcli/__init__.py`, `typescript/src/`).
- The contract's section on the `help` and `version` framework commands and the
  help document.
- Conformance cases for the version document.

## Effort

About a day across the three ports, plus conformance.
