+++
title = "Behavior Cases"
description = "How the Go implementation's JSON behavior cases work: the case format, the case harness, the help-document and trace-store checks, and how to add a case."
nav_group = "Guides"
nav_order = 10
+++

# Behavior Cases

The Go implementation's suite holds a body of behavior cases written as JSON
rather than as Go: each case declares an app, the argv it runs with, and what
the run must produce. They live in `go/strictcli/testdata/cases/` and run as
part of `go test` (`TestCases` in `go/strictcli/cases_test.go`).

`go/strictcli/testdata/caseharness/` is a small program that reads a case's app
definition from the file `CONFORMANCE_APP_DEF` names, builds that app with the
Go API, and runs it. The test builds it once per run and starts it as a
subprocess for every case, so each case sees what a real program sees: its own
exit status, its own stdout and stderr, its environment, and its stdin.

## The case format

Each case file is a JSON array of cases. A case is an object with:

- `name`: unique within its file; the subtest is named `<file>: <name>`.
- `app`: a declarative app definition (commands, flags, args, groups, config, checks).
- `argv`: the command-line arguments to pass.
- `env`: optional environment variables to set.
- `stdin`: optional text piped to the program's stdin. Absent means `/dev/null`, so no case ever depends on a terminal (a pipe carrying this text is not a TTY either). The `--mcp` cases deliver their JSON-RPC lines this way.
- `protocol_script`: an alternative to `stdin` for exchanges whose next request depends on the previous reply (see below).
- `expect`: assertions on the exit code, stdout, and stderr, plus structural assertions on the effect log and on a printed help document.

Every case is validated against `go/strictcli/testdata/case_schema.json`, a JSON
Schema that defines the vocabulary of app definitions and expectations. A case
that deliberately crafts an invalid app definition sets
`skip_schema_validation: true`.

Every case runs with `HOME` pointing at a throwaway directory, so the process
trace store a case writes into is never the operator's.

### Expectations

| Field | Purpose |
|-------|---------|
| `exit_code` | Required. The expected process exit code. |
| `stdout_equals` / `stderr_equals` | Exact match, after trailing whitespace is stripped from every line and trailing newlines from the whole. |
| `stdout_contains` / `stderr_contains` | Substring(s) that must appear. |
| `stdout_not_contains` / `stderr_not_contains` | Substring(s) that must not appear. |
| `stdout_matches` / `stderr_matches` | Go regular expression(s) that must match somewhere in the stream. |
| `config_file_contains` / `config_file_not_contains` / `config_file_matches` | The same assertions against the config file a case seeded with `config_content` or `config_content_late`, read after the run. |
| `effects_equals` | Deep equality against the structured effect log the run produced. Absent optional keys and explicit-null keys are equivalent, and `recorded` is required on every record. |
| `schema_command_keys` | Maps a dotted command path (groups then command, e.g. `release.run`) to keys the help document's entry must carry, with their values. Requires `help ... --json` in the argv. |
| `schema_command_absent_keys` | Maps a dotted command path to keys that entry must not carry. Requires `help ... --json` in the argv. |
| `schema_bytes_equal` | The whole help document, compared as text apart from its `project_id` line, which must be present. Requires `help ... --json` in the argv. |

Structural assertions (`effects_equals`, `schema_command_keys`, and `json_equals`
in a protocol step) compare parsed JSON, so key order and whitespace are never
part of them. Within those comparisons the string `"$ANY"` matches any value of
any type: the field must be present, and nothing is asserted about what it
holds. It is for values that are nondeterministic by construction, such as a
continuation signature or a timestamp.

### Line-scripted protocol cases

A `protocol_script` case drives a request/response exchange step by step. Each
step writes one line to the program's stdin (`send`), and/or reads one reply
line from `stdout` or `stderr` (`stream`) and asserts on it (`expect_line`, with
`equals`, `contains`, `not_contains`, `matches`, or `json_equals`). A step may
`capture` values out of its reply by dotted path, which a later step splices
into what it sends as `{{name}}`, or as `{{name|tamper}}` with the first
character changed. A missing reply line within the step timeout is a failure,
never a hang. The program's full streams are still checked by the ordinary
`expect` block.

### Handler keys

Commands declare what their handler does instead of carrying code:

- `handler_prints` is a template; `{name}` is replaced with the value of the flag or arg named `name`, and `{source:name}` with its provenance source label.
- `handler_effects` and `handler_diagnostics` run effects and diagnostics through the context.
- `handler_aborts: true` makes the handler panic with `conformance: handler aborted` after its effects and diagnostics, which is how a case reaches the framework's unwinding paths.
- `handler_signals_self`, `handler_out`, `handler_document`, `handler_raw_stdout`, and `handler_exit_now` drive the framework's exit step and output writers, in that order; a command-level `payload_renderer: {"template": ...}` declares a payload renderer.

## Checks over whole help documents

`TestHelpDocumentFragmentsAndPresence` runs `help --json` for each app in
`go/strictcli/testdata/help_document_apps/` and holds every document to two
promises:

- every `value_schema` validates under the payload-schema validator, uses only the closed subset's keywords (`type`, `items`, `additionalProperties`, `enum`) in that order, names a type, and carries `items`, `additionalProperties`, and `enum` only where they apply; a selector entry (one carrying `elect_by`) carries no `value_schema`;
- every flag and arg entry states its `presence`, carries a `default` exactly when presence is `default`, and an arg entry carries no `required` key.

## The trace store's sweeps

`TestTraceStoreIsObservationalOnly` runs a named set of cases with no trace
parent and a healthy, empty store, then again with a forged trace parent
(valid and garbage), with an unwritable store, and with the store's path taken
by a file. Stdout, stderr, and the exit code must match the baseline byte for
byte, and the baseline store must have received entries, so the sweep cannot
pass by never reaching the code that writes the store.

## Running the cases

```bash
cd go
go test -run 'TestCases' ./strictcli
go test -run 'TestCases/flags.json' ./strictcli   # one file
```

## Adding a case

1. Pick the file in `go/strictcli/testdata/cases/` that matches the feature area, or create one.
2. Write the case as an object in the file's array:

```json
{
  "name": "feature-area: descriptive name of what is being tested",
  "app": {
    "name": "myapp",
    "version": "1.0.0",
    "help": "test app",
    "commands": [
      {
        "name": "greet",
        "help": "say hello",
        "effect": "read_only",
        "flags": [
          {"name": "loud", "help": "shout", "type": "bool", "presence": "default", "default": false}
        ],
        "handler_prints": "hello loud={loud}"
      }
    ]
  },
  "argv": ["greet", "--loud"],
  "expect": {"exit_code": 0, "stdout_equals": "hello loud=true"}
}
```

3. Run `go test -run 'TestCases' ./strictcli`. A case using an app-definition
   key the harness does not know needs the harness and `case_schema.json`
   taught it first.
