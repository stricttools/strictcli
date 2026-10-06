# strictcli (Go)

Go implementation of strictcli. Part of the strictcli rlsbl monorepo as
releasable `go-strictcli`.

## Development

- `go test ./strictcli/... -race` from this directory.
- The JSON behavior cases in `strictcli/testdata/cases/` run as part of that suite (`TestCases`).
- CI (`ci-router.yml` at the repo root) runs the full Go test suite on every push touching `go/**`.

## Release workflow

Releases go through [rlsbl](https://github.com/stricttools/rlsbl) monorepo releases.

- CHANGELOG.md is generated from JSONL changelog entries — never edit it by hand.
  Add entries with `rlsbl changelog add` from inside `go/` after each commit
  (they land in `.rlsbl-monorepo/releasables/go-strictcli/changes/unreleased.jsonl`).
- To release: from the repo root, run `rlsbl monorepo release init`, edit the
  scaffolded release file (bump type, description, and context live in the file),
  then run `rlsbl monorepo release run --watch --approve-consequential`.
- Go library — no publish step. Tagged releases are available via `go get`.
- Never push tags or publish manually.

## Conventions

- No tokens or secrets in command-line arguments (use env vars or config files).
