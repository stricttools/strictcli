# New types for codehome migration

## Context

codehome has a plugin system that builds CLI commands dynamically from TOML manifests at runtime. It currently uses argparse as the backend. To migrate to strictcli, codehome needs to construct the command tree programmatically (not via decorators) and requires several features strictcli doesn't yet support.

## Missing features

### 1. Programmatic (non-decorator) command registration

RESOLVED: Already works. `app.command("name", help="...")(handler_func)` is valid Python. Tested in test_exit_codes.py. Go is natively imperative. No changes needed.

### 2. type=float

4 occurrences in a private consumer (--sample-rate), also needed by codehome manifests. Trivial to add alongside type=int.

### 3. type=Path (pathlib.Path)

codehome manifests declare `type = "path"` which maps to `pathlib.Path`. strictcli could support this as `type=Path` (Python) or `type=string` with path validation (Go).

### 4. Hidden flags

DEFERRED: Only codehome's manifest schema mentions hidden flags. No actual plugin uses them. codehome can filter hidden args from help output itself if needed.

### 5. nargs variants

DEFERRED: No actual codehome plugin manifest uses nargs. The feature exists in codehome's builder code but is unused. codehome can drop nargs from its manifest schema or handle it outside strictcli.

## Consumer

codehome (primary). The individual features (float, Path, hidden, programmatic registration) are useful to other consumers too.

## Effort

- type=float: Trivial (mirror type=int implementation)
- Conformance tests
- Total: ~1-2 days across Python, Go, and conformance

## Depends on

Recursive nesting (filed separately) -- codehome plugins can declare arbitrary subcommand depth.
