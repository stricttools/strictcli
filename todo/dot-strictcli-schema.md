# .strictcli/ directory: config.json spec and consumer rollout

## Context

`--dump-schema` is implemented (generates `.strictcli/schema.json`). What remains is the static metadata file and the rollout to consumers.

## Remaining work

### .strictcli/config.json

Static metadata file created manually in each consumer project:
- language (python/go)
- strictcli version constraint
- entry point (CLI command name)
- app name

Design the format and document it.

### Consumer rollout

Roll out `.strictcli/` to all consumers:
- Python: rlsbl, claudewheel, predraw, selfdoc, claudestream, and a private project
- Go: safegit, howmuchleft, migrable, saferm

For each: run `--dump-schema`, create `config.json`, commit both.

### Auto-generation during release

Filed separately in rlsbl as `todo/strictcli-schema-auto-dump.md` -- rlsbl will auto-detect strictcli projects and run `--dump-schema` during release.

## Depends on

- rlsbl auto-detection feature (for automated rollout)
- Or manual per-project setup (immediate, no dependency)
