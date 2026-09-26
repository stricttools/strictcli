# strictcli (TypeScript)

TypeScript implementation of strictcli. Publishes to npm as `strictcli`. Part of
the strictcli rlsbl monorepo as releasable `ts-strictcli`.

## Development

- `npm ci` then `npm run build`, `npm test`, and `npm run lint` from this directory.
- `package.json` has no `"private"` flag — the package publishes to npm as
  `strictcli`. Publishing is gated by CI (the publish gate in `ci-router.yml`),
  not by a package.json guard; never run `npm publish` manually.
- Run the conformance suite after changes: `cd ../conformance && python run.py --target typescript`.
- CI (`ci-router.yml` at the repo root) runs build, lint, and tests on Node 22 and 24
  on every push touching `typescript/**`.

## Release workflow

Releases go through [rlsbl](https://github.com/stricttools/rlsbl) monorepo releases.

- CHANGELOG.md is generated from JSONL changelog entries — never edit it by hand.
  Add entries with `rlsbl changelog add` from inside `typescript/` after each commit
  (they land in `.rlsbl-monorepo/releasables/ts-strictcli/changes/unreleased.jsonl`).
- To release: from the repo root, run `rlsbl monorepo release init`, edit the
  scaffolded release file (bump type, description, and context live in the file),
  then run `rlsbl monorepo release run --no-allow-dirty --watch --approve-consequential`.
- Never publish or push manually — CI publishes to npm (with provenance) after the
  publish gate confirms CI passed on the release commit.

## Conventions

- No tokens or secrets in command-line arguments (use env vars or config files).
