# strictcli (Python)

Python implementation of strictcli. Published to PyPI. Part of the strictcli
rlsbl monorepo as releasable `py-strictcli`.

## Development

- `uv sync` then `uv run pytest` from this directory. Always use `uv`, never pip.
- Run the conformance suite after changes: `cd ../conformance && python run.py --target python`.
- CI (`ci-router.yml` at the repo root) runs the full pytest suite on every push touching `python/**`.

## Release workflow

Releases go through [rlsbl](https://github.com/stricttools/rlsbl) monorepo releases.

- CHANGELOG.md is generated from JSONL changelog entries — never edit it by hand.
  Add entries with `rlsbl changelog add` from inside `python/` after each commit
  (they land in `.rlsbl-monorepo/releasables/py-strictcli/changes/unreleased.jsonl`).
- To release: from the repo root, run `rlsbl monorepo release init`, edit the
  scaffolded release file (bump type, description, and context live in the file),
  then run `rlsbl monorepo release run --watch --approve-consequential`.
- Never publish or push manually — CI publishes to PyPI via Trusted Publishing.

## Conventions

- No tokens or secrets in command-line arguments (use env vars or config files).
