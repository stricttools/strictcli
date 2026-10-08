# Framework-level configuration file support

## Context

Multiple strictcli-based projects (rlsbl and a private project) need persistent configuration beyond CLI flags and env vars. Currently each project must implement its own TOML loading, precedence chain, and config management subcommands.

## Problem

Settings like host, port, model names, batch sizes are set-once-and-forget -- CLI flags are repetitive, env vars are invisible. A config file is the natural middle layer.

## Proposed feature

- JSON config file support with XDG-compliant paths (`~/.config/{app_name}/config.json`)
- Auto-generated config schema from registered flags (strictcli already knows every flag's name, type, default, and env var)
- Precedence chain: CLI flag > env var > config file > code default
- Auto-generated subcommands: `config show` (with source attribution like `git config --show-origin`), `config set key value`, `config edit` (opens $EDITOR), `config path`
- Config validation using flag types and choices
- Future migration to TOML once py-toml-edit exists (see config-toml-migration.md)

## Pros

- Solves a universal need across all strictcli projects
- strictcli has all the metadata needed to auto-generate config schema
- Users get config management for free just by using strictcli
- JSON uses only stdlib (no dependencies in Python or Go)

## Cons

- JSON is less human-friendly than TOML (no comments, stricter syntax)
- Introduces persistent state (currently stateless)
- Opinionated about file location, format, and precedence
- Consumes one nesting level for the `config` group

## Effort estimate

Medium -- the reading side is straightforward, writing/editing adds complexity, auto-generation from flag metadata requires careful design.
