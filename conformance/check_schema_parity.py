#!/usr/bin/env python3
"""Schema parity check for strictcli conformance.

Defines a rich app (covering all feature combinations), runs `help --json` on
every registered target (Python, Go, TypeScript), and compares the resulting
JSON schemas N-way. After schema v2 the comparison is BYTE equality (effects
contract §25.8): key order, escaping, number form, two-space indentation and
the single trailing newline are all part of the contract, so a serialization
change can no longer hide inside a structural comparison. All targets must
produce identical bytes; any difference is a parity gap, reported with the odd
one(s) out and the first differing line.

The one thing removed before comparing is the `project_id` LINE, which names
each target's own program (Go's main module, the Python project holding the
program's code, the TypeScript entry script's package). §25.9 places
`project_id` immediately after `defaults` precisely so that removing it leaves
the rest of the document byte-identical.

Exit 0 if all schemas are identical, exit 1 with a diff report otherwise.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------

CONFORMANCE_DIR = Path(__file__).resolve().parent
PROJECT_ROOT = CONFORMANCE_DIR.parent
GO_PKG_DIR = PROJECT_ROOT / "go"
HARNESS_DIR = CONFORMANCE_DIR / "harness"
TS_DIR = PROJECT_ROOT / "typescript"
HARNESS_TS_ENTRY = CONFORMANCE_DIR / "harness_ts" / "main.js"

# Registration order is also the reporting order for N-way comparison.
TARGET_NAMES = ["python", "go", "typescript"]

# ---------------------------------------------------------------------------
# App definitions -- each exercises a different feature surface
# ---------------------------------------------------------------------------

# A rich app definition covering: all flag types, args, groups, nested groups,
# deprecated commands, scoped selectors in both spellings and nested,
# record-shaped choices on flags AND args, compound (list/dict) flags,
# constraints (AllOrNone, Requires, Implies), repeatable flags, unique, env,
# env_separator, passthrough commands, config, tags, flag_sets, optional args,
# variadic args, negatable, prefixed, and short flags.
#
# Effects contract §14.5: every non-deprecated command entry carries `effect`
# (classification is mandatory, so the fixture cannot even be built without it),
# the deprecated entries deliberately do not (§1.1), and the app additionally
# exercises `grants`, `forwarding` and `proc_observe_allowlist` end-to-end so
# the three new schema keys (§13) are compared across implementations.
RICH_APP = {
    "name": "richapp",
    "version": "2.5.0",
    "help": "A comprehensive test app for schema parity",
    "env_prefix": "RICH",
    "config": True,
    "infra_root": {"RICH_HOME": "/var/lib/richapp"},
    "handshake_env": {"RICH_SESSION": "Session token from the invoking process"},
    "proc_observe_allowlist": [["git", "status"], ["git", "rev-parse"]],
    "global_flags": [
        # Named `trace`, not `verbose`: the reserved quartet (§7.1) bans
        # dry-run/yes/quiet/verbose as flag names at every level.
        {
            "name": "trace",
            "type": "bool",
            "help": "Enable trace output",
            "presence": "default",
            "short": "V",
            "default": False,
        },
        {
            "name": "log-level",
            "type": "str",
            "help": "Logging level",
            "presence": "default",
            "default": "info",
            "env": "RICH_LOG_LEVEL",
            "choices_str": [
                {"value": "debug", "help": "Everything, including internals"},
                {"value": "info"},
                {"value": "warn"},
                {"value": "error"},
            ],
        },
        # Global flag with a RelativeToRoot marker default. Locks in that markers
        # serialize identically (machine-stable) across both implementations.
        {
            "name": "state-file",
            "type": "str",
            "help": "State file relative to the infra root",
            "presence": "default",
            "default_relative_to_root": {
                "env_var": "RICH_HOME",
                "parts": ["state", "app.db"],
            },
        },
    ],
    "commands": [
        # 1. Simple command with all flag types
        {
            "name": "types",
            "help": "Test all flag types",
            "effect": "read_only",
            "handler_prints": "types",
            "flags": [
                {
                    "name": "name",
                    "type": "str",
                    "help": "A string flag",
                    "presence": "default",
                    "default": "world",
                },
                {
                    "name": "count",
                    "type": "int",
                    "help": "An integer flag",
                    "presence": "default",
                    "default": 42,
                },
                {
                    "name": "ratio",
                    "type": "float",
                    "help": "A float flag",
                    "presence": "default",
                    "default": 3.14,
                },
                # Named `simulate`, not `dry-run`: the reserved quartet (§7.1)
                # bans the four framework names on command flags too.
                {
                    "name": "simulate",
                    "type": "bool",
                    "help": "Simulate without applying",
                    "presence": "required",
                },
                # Command flag with a RelativeToRoot marker default.
                {
                    "name": "cache-file",
                    "type": "str",
                    "help": "Cache file relative to the infra root",
                    "presence": "default",
                    "default_relative_to_root": {
                        "env_var": "RICH_HOME",
                        "parts": ["cache.bin"],
                    },
                },
            ],
            "args": [
                {
                    "name": "target",
                    "help": "Target to process",
                    "presence": "required",
                },
            ],
        },
        # 2. Command with repeatable/unique flags and env_separator
        {
            "name": "multi",
            "help": "Test repeatable flags",
            "effect": "read_only",
            "handler_prints": "multi",
            "flags": [
                {
                    "name": "tag",
                    "type": "str",
                    "help": "Tags to apply",
                    "presence": "required",
                    "repeatable": True,
                    "unique": True,
                    "env": "RICH_TAGS",
                    "env_separator": ",",
                },
                {
                    "name": "port",
                    "type": "int",
                    "help": "Ports to open",
                    "presence": "default",
                    "repeatable": True,
                    "unique": False,
                    "default": [80, 443],
                },
            ],
        },
        # 3. Command with the scoped-selector construct in both spellings,
        #    nested one level, and with compound flags inside a scope. The
        #    selector encoding (§25.6) is the one shape v1 had no way to
        #    publish, so it is compared at every depth: choice objects, their
        #    scopes, `elect_by`, the flat-map default, and the member payload
        #    that sits first in its choice's `flags` array.
        {
            "name": "output",
            "help": "Test the selector construct",
            "effect": "read_only",
            "handler_prints": "output",
            "flags": [
                {
                    "name": "sink",
                    "help": "Where the output goes",
                    "short": "k",
                    "presence": "default",
                    "default": {"choice": "stdout"},
                    "elect_by": "selector-token",
                    "choices": [
                        {"name": "stdout", "help": "Write to standard output"},
                        {
                            "name": "file",
                            "help": "Write to a file on disk",
                            "flags": [
                                {
                                    "name": "path",
                                    "type": "str",
                                    "help": "Where to write",
                                    "presence": "required",
                                },
                                {
                                    "name": "rotate",
                                    "type": "int",
                                    "help": "How many files to keep",
                                    "presence": "default",
                                    "default": 5,
                                },
                                {
                                    "name": "headers",
                                    "type": "dict[str,str]",
                                    "help": "Extra headers to write",
                                    "presence": "default",
                                    "default": {},
                                },
                                {
                                    "name": "encoding",
                                    "help": "How the file is encoded",
                                    "presence": "required",
                                    "elect_by": "selector-token",
                                    "choices": [
                                        {"name": "utf8", "help": "UTF-8 text"},
                                        {
                                            "name": "binary",
                                            "help": "Raw bytes",
                                            "flags": [
                                                {
                                                    "name": "chunk",
                                                    "type": "int",
                                                    "help": "Bytes per write",
                                                    "presence": "default",
                                                    "default": 4096,
                                                },
                                            ],
                                        },
                                    ],
                                },
                            ],
                        },
                    ],
                },
                {
                    "name": "audience",
                    "help": "Who the output is for",
                    "presence": "required",
                    "elect_by": "member-flags",
                    "choices": [
                        {
                            "name": "operator",
                            "help": "One named operator",
                            # The payload's help deliberately REPEATS the
                            # choice's. TypeScript's choice({help, value,
                            # flags}) has no slot for a payload help and
                            # publishes the choice's, which is a live
                            # divergence recorded on its own by
                            # cases/schema_v2.json's member-payload case.
                            # Repeating it here keeps that one known
                            # difference from sitting ahead of every byte this
                            # fixture exists to compare, while the payload
                            # entry's name, position, fragment and presence are
                            # still compared in full.
                            # The member's short rides the payload, which is
                            # the electing flag's own declaration (§24.12), and
                            # publishes as the `value` entry's `short` key --
                            # an ordinary flag-entry key in §25.9's order.
                            "value": {
                                "type": "str",
                                "help": "One named operator",
                                "short": "o",
                            },
                            "flags": [
                                {
                                    "name": "notify",
                                    "type": "bool",
                                    "help": "Send a notification too",
                                    "presence": "default",
                                    "default": False,
                                },
                            ],
                        },
                        # A payload-less member's short is declared on the
                        # choice and publishes NOTHING: the choice has no
                        # `value` entry to carry it, in any implementation.
                        {
                            "name": "everyone",
                            "help": "Every subscriber",
                            "short": "e",
                        },
                    ],
                },
                {
                    "name": "labels",
                    "type": "list[str]",
                    "help": "Labels to attach",
                    "presence": "default",
                    "unique": False,
                    "default": [],
                },
                {
                    "name": "limits",
                    "type": "dict[str,int]",
                    "help": "Per-stream limits",
                    "presence": "optional",
                },
            ],
            "args": [
                {
                    "name": "level",
                    "type": "str",
                    "help": "How loud to be",
                    "presence": "default",
                    "default": "normal",
                    "choices_str": [
                        {"value": "quiet", "help": "Say as little as possible"},
                        {"value": "normal"},
                    ],
                },
            ],
        },
        # 4. Command with constraints (AllOrNone, Requires)
        {
            "name": "deploy",
            "help": "Test constraints",
            "effect": "mutating",
            # Consequential on its own (§8.1), with dry run left at the regime's
            # supported baseline: pairs with `notify` below so the schema
            # comparison sees `consequential` both with and without a dry-run
            # declaration alongside it.
            "consequential": True,
            # Two grants of different kinds, so the schema's `grants` array is
            # compared for both content and declaration order (§13).
            "grants": [
                {
                    "name": "push",
                    "reason": "release engine owns remote refs",
                    "kind": "proc_mutate",
                },
                {
                    "name": "write-manifest",
                    "reason": "the deploy manifest is regenerated in place",
                    "kind": "file_write",
                },
            ],
            "handler_prints": "deploy",
            "flags": [
                {
                    "name": "host",
                    "type": "str",
                    "help": "Deploy host",
                    "presence": "optional",
                },
                {
                    "name": "port-num",
                    "type": "int",
                    "help": "Deploy port",
                    "presence": "optional",
                },
                {
                    "name": "ssl",
                    "type": "bool",
                    "help": "Use SSL",
                    "presence": "required",
                },
                {
                    "name": "cert",
                    "type": "str",
                    "help": "SSL certificate path",
                    "presence": "optional",
                },
            ],
            "args": [
                {
                    "name": "regions",
                    "help": "Regions to deploy to",
                    "presence": "optional",
                    "variadic": True,
                },
            ],
            # Three of the four constraint kinds, each with the mandatory name
            # §26.1 gives it, and the co-occurrence family carrying the member
            # records §26.11 publishes as `{kind, name, when}`. The at-least-one
            # names all three member kinds in one constraint -- a positional arg
            # under an election selector, the all-or-none declared above it as a
            # NESTED member (published as `kind: "constraint"` with no `when`),
            # and a flag -- because §26.11 calls the nesting encoding the
            # difference between a complete encoding and an indicative one, and
            # a `kind: "flag"` pair alone leaves both of the others uncompared.
            "constraints": [
                {
                    "type": "all_or_none",
                    "name": "endpoint",
                    "members": [{"name": "host"}, {"name": "port-num"}],
                },
                {
                    "type": "at_least_one",
                    "name": "deploy-selection",
                    "members": [
                        {"name": "regions", "when": "non_empty"},
                        {"name": "endpoint"},
                        {"name": "cert"},
                    ],
                },
                {
                    "type": "requires",
                    "name": "cert-ssl",
                    "flag": "cert",
                    "depends_on": "ssl",
                },
            ],
        },
        # 5. Command with an Implies constraint
        {
            "name": "notify",
            "help": "Test the implies constraint",
            "effect": "mutating",
            # Both effects-regime declarations at once: `consequential` and the
            # `dry_run_supported=false` + reason pair. Every other mutating
            # command in this fixture omits the pair, so the comparison pins
            # emission (here) and omission (everywhere else) in one app -- an
            # implementation that emitted the default-valued key would diverge
            # on all of them.
            "consequential": True,
            "dry_run_supported": False,
            "dry_run_unsupported_reason": "the alert it raises decides who is paged next",
            "handler_prints": "notify",
            "flags": [
                {
                    "name": "email",
                    "type": "bool",
                    "help": "Send email notification",
                    "presence": "required",
                },
                {
                    "name": "alert",
                    "type": "bool",
                    "help": "Enable alerts",
                    "presence": "required",
                },
            ],
            "constraints": [
                {
                    "type": "implies",
                    "name": "email-alert",
                    "flag": "email",
                    "implies": "alert",
                    "value": True,
                },
            ],
        },
        # 6. Command with flag_sets
        {
            "name": "query",
            "help": "Test flag sets",
            "effect": "read_only",
            "handler_prints": "query",
            "flag_sets": [
                {
                    "name": "pagination",
                    "flags": [
                        {
                            "name": "page",
                            "type": "int",
                            "help": "Page number",
                            "presence": "default",
                            "default": 1,
                        },
                        {
                            "name": "per-page",
                            "type": "int",
                            "help": "Items per page",
                            "presence": "default",
                            "default": 20,
                        },
                    ],
                },
            ],
        },
        # 6. Command with optional/variadic args
        {
            "name": "files",
            "help": "Test args",
            "effect": "read_only",
            "handler_prints": "files",
            "args": [
                {
                    "name": "src",
                    "help": "Source directory",
                    "presence": "required",
                },
                {
                    "name": "extra",
                    "help": "Extra files",
                    "presence": "optional",
                    "variadic": True,
                },
            ],
        },
        # 7. Passthrough command
        {
            "name": "exec",
            "help": "Execute a command",
            "effect": "mutating",
            "passthrough": True,
            "handler_prints": "exec",
            "passthrough_handler_prints": "exec:{name}:{args}",
        },
        # 8. Command with tags
        {
            "name": "lint",
            "help": "Run linters",
            "effect": "read_only",
            # Declared forwarding (§10.2). Inert in Go and TypeScript and, here,
            # inert in Python too (the generated handler names its parameters) --
            # the fixture declares it so the schema's `forwarding` key is
            # compared across implementations.
            "forwarding": {"reason": "wraps the underlying linter's own flags"},
            "handler_prints": "lint",
            "tags": ["quality", "ci"],
        },
        # 9. Deprecated command
        {
            "name": "old-cmd",
            "help": "Deprecated command",
            "deprecated": True,
            "deprecated_message": "Use 'new-cmd' instead",
        },
        # 10. Command with choices (int and float)
        {
            "name": "level",
            "help": "Test int/float choices",
            "effect": "read_only",
            "handler_prints": "level",
            "flags": [
                {
                    "name": "priority",
                    "type": "int",
                    "help": "Priority level",
                    "presence": "default",
                    "choices_int": [
                        {"value": 1, "help": "The lowest level"},
                        {"value": 2},
                        {"value": 3},
                        {"value": 4},
                        {"value": 5},
                    ],
                    "default": 3,
                },
                {
                    "name": "threshold",
                    "type": "float",
                    "help": "Threshold value",
                    "presence": "default",
                    "choices_float": [
                        {"value": 0.1},
                        {"value": 0.5},
                        {"value": 0.9, "help": "Almost everything"},
                    ],
                    "default": 0.5,
                },
            ],
        },
        # 11. Command with short flag and prefixed env
        {
            "name": "info",
            "help": "Show info",
            "effect": "read_only",
            "handler_prints": "info",
            "flags": [
                {
                    "name": "format",
                    "type": "str",
                    "help": "Output format",
                    "presence": "default",
                    "short": "f",
                    "default": "table",
                    "prefixed": True,
                },
                {
                    "name": "color-off",
                    "type": "bool",
                    "help": "Disable colors",
                    "presence": "required",
                    "negatable": False,
                },
            ],
        },
    ],
    "groups": [
        {
            "name": "db",
            "help": "Database operations",
            "tags": ["infra"],
            "commands": [
                {
                    "name": "migrate",
                    "help": "Run migrations",
                    "effect": "mutating",
                    "handler_prints": "migrate",
                    "flags": [
                        {
                            "name": "steps",
                            "type": "int",
                            "help": "Migration steps",
                            "presence": "optional",
                        },
                    ],
                },
                {
                    "name": "seed",
                    "help": "Seed database",
                    "effect": "mutating",
                    "handler_prints": "seed",
                },
                # Deprecated command in group
                {
                    "name": "reset",
                    "help": "Reset database",
                    "deprecated": True,
                    "deprecated_message": "Use 'db migrate --steps -1' instead",
                },
            ],
            "groups": [
                # Nested group
                {
                    "name": "cache",
                    "help": "Cache operations",
                    "commands": [
                        {
                            "name": "clear",
                            "help": "Clear cache",
                            "effect": "mutating",
                            "handler_prints": "clear",
                        },
                        {
                            "name": "stats",
                            "help": "Show cache stats",
                            "effect": "read_only",
                            "handler_prints": "stats",
                            "flags": [
                                {
                                    "name": "detailed",
                                    "type": "bool",
                                    "help": "Show detailed stats",
                                    "presence": "required",
                                },
                            ],
                        },
                    ],
                },
            ],
        },
    ],
}

# Minimal app -- tests that empty/default fields are also handled identically
MINIMAL_APP = {
    "name": "minimal",
    "version": "0.1.0",
    "help": "A minimal app",
    "commands": [
        {
            "name": "hello",
            "help": "Say hello",
            "effect": "read_only",
            "handler_prints": "hello",
        },
    ],
}

# Config fields app -- tests that config_fields schema format matches
CONFIG_APP = {
    "name": "cfgapp",
    "version": "1.0.0",
    "help": "An app with config fields",
    "config": True,
    "config_fields_def": [
        {
            "name": "api.key",
            "type": "str",
            "help": "API key for the service",
        },
        {
            "name": "port",
            "type": "int",
            "help": "Port to listen on",
            "default": 8080,
        },
        {
            "name": "debug",
            "type": "bool",
            "help": "Enable debug mode",
            "default": False,
        },
    ],
    "commands": [
        {
            "name": "serve",
            "effect": "mutating",
            "help": "Start the server",
            "handler_prints": "serve",
            "config_fields": ["api.key", "port"],
        },
        {
            "name": "status",
            "effect": "read_only",
            "help": "Show status",
            "handler_prints": "status",
        },
    ],
}


# ---------------------------------------------------------------------------
# Helpers for running apps
# ---------------------------------------------------------------------------


def _generate_python_script(app_def: dict) -> str:
    """Generate a Python script from an app definition."""
    sys.path.insert(0, str(CONFORMANCE_DIR))
    from ref_python import generate
    script = generate(app_def)
    # Fix the sys.path to use an absolute path
    python_dir = str(PROJECT_ROOT / "python")
    script = script.replace(
        "sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'python'))",
        f"sys.path.insert(0, {python_dir!r})",
    )
    return script


def _build_harness() -> str:
    """Build the Go harness binary. Returns path to binary."""
    binary = str(HARNESS_DIR / "harness")
    result = subprocess.run(
        ["go", "build", "-o", binary, "."],
        cwd=str(HARNESS_DIR),
        capture_output=True,
        text=True,
        timeout=60,
    )
    if result.returncode != 0:
        raise RuntimeError(f"harness build failed:\n{result.stderr}")
    return binary


def _build_ts_harness() -> str:
    """Build typescript/dist (the TS harness's only prerequisite). Returns the
    harness entry path (conformance/harness_ts/main.js, plain Node ESM)."""
    result = subprocess.run(
        ["npm", "run", "build"],
        cwd=str(TS_DIR),
        capture_output=True,
        text=True,
        timeout=300,
    )
    if result.returncode != 0:
        raise RuntimeError(
            f"typescript dist build failed:\n{result.stdout}\n{result.stderr}"
        )
    return str(HARNESS_TS_ENTRY)


def _run_dump_schema(
    app_def: dict,
    target: str,
    harness_binary: str | None = None,
    ts_entry: str | None = None,
) -> str:
    """Run `help --json` for a given target and return the document's TEXT.

    The text, not the parsed document: after v2 the comparison is byte equality
    (§25.8), so parsing first would discard exactly what is being asserted.

    Raises RuntimeError on failure.
    """
    cleanup: list[str] = []
    env = os.environ.copy()
    try:
        if target == "python":
            # The script lives in this directory (ignored by `tmp*.py`): the
            # project holding the program's code names its project_id.
            with tempfile.NamedTemporaryFile(
                mode="w", suffix=".py", prefix="tmp_strictcli_schema_py_",
                delete=False, dir=CONFORMANCE_DIR,
            ) as f:
                f.write(_generate_python_script(app_def))
                cleanup.append(f.name)
            cmd = [sys.executable, f.name]
        elif target in ("go", "typescript"):
            with tempfile.NamedTemporaryFile(
                mode="w", suffix=".json", prefix="strictcli_schema_def_",
                delete=False,
            ) as f:
                json.dump(app_def, f, sort_keys=True)
                cleanup.append(f.name)
            env["CONFORMANCE_APP_DEF"] = f.name
            if target == "go":
                assert harness_binary is not None
                cmd = [harness_binary]
            else:
                assert ts_entry is not None
                cmd = ["node", ts_entry]
        else:
            raise ValueError(f"unsupported target: {target}")
        result = subprocess.run(
            cmd + ["help", "--json"],
            capture_output=True,
            text=True,
            env=env,
            timeout=10,
        )
        if result.returncode != 0:
            raise RuntimeError(
                f"{target} help --json exited {result.returncode}\n"
                f"stdout: {result.stdout}\n"
                f"stderr: {result.stderr}"
            )
        return result.stdout
    finally:
        for path in cleanup:
            if os.path.exists(path):
                os.unlink(path)


# ---------------------------------------------------------------------------
# Schema comparison
# ---------------------------------------------------------------------------


def _diff_schemas(
    schema_a: object,
    schema_b: object,
    label_a: str = "python",
    label_b: str = "go",
    path: str = "$",
) -> list[str]:
    """Recursively compare two JSON-like objects. Returns list of difference descriptions."""
    diffs: list[str] = []

    if type(schema_a) != type(schema_b):
        # Special case: both null
        if schema_a is None and schema_b is None:
            return diffs
        # Special case: int vs float (JSON numbers)
        if isinstance(schema_a, (int, float)) and isinstance(schema_b, (int, float)):
            if float(schema_a) != float(schema_b):
                diffs.append(
                    f"{path}: value mismatch: {label_a}={schema_a!r} {label_b}={schema_b!r}"
                )
            return diffs
        diffs.append(
            f"{path}: type mismatch: {label_a}={type(schema_a).__name__}({schema_a!r}) "
            f"{label_b}={type(schema_b).__name__}({schema_b!r})"
        )
        return diffs

    if isinstance(schema_a, dict):
        a_keys = set(schema_a.keys())
        b_keys = set(schema_b.keys())

        for k in sorted(a_keys - b_keys):
            diffs.append(
                f"{path}.{k}: present in {label_a} only (value={schema_a[k]!r})"
            )
        for k in sorted(b_keys - a_keys):
            diffs.append(
                f"{path}.{k}: present in {label_b} only (value={schema_b[k]!r})"
            )

        for k in sorted(a_keys & b_keys):
            diffs.extend(
                _diff_schemas(schema_a[k], schema_b[k], label_a, label_b, f"{path}.{k}")
            )

    elif isinstance(schema_a, list):
        if len(schema_a) != len(schema_b):
            diffs.append(
                f"{path}: list length mismatch: {label_a}={len(schema_a)} "
                f"{label_b}={len(schema_b)}"
            )
        for i in range(min(len(schema_a), len(schema_b))):
            diffs.extend(
                _diff_schemas(
                    schema_a[i], schema_b[i], label_a, label_b, f"{path}[{i}]"
                )
            )

    else:
        if schema_a != schema_b:
            diffs.append(
                f"{path}: value mismatch: {label_a}={schema_a!r} {label_b}={schema_b!r}"
            )

    return diffs


def _strip_project_id(text: str) -> str:
    """Drop the `project_id` LINE, which names each target's own program.

    §25.9 places `project_id` immediately after `defaults` precisely so that
    removing it leaves the CWD-free core document byte-identical -- so the
    normalization is one whole line and nothing else. The v1 layer that
    rewrote repeatable-scalar flags into their list-carrier spelling
    (`_canonicalize_repeatable`) is DELETED with the `repeatable` key itself
    (§25.3): every rule in a normalization layer is a place where a real
    divergence can be absorbed as serialization noise, and this round removes
    the machinery that could hide the next one.
    """
    return "\n".join(
        line for line in text.split("\n")
        if not line.lstrip().startswith('"project_id":')
    )


def _first_line_diff(a: str, b: str, label_a: str, label_b: str) -> list[str]:
    """The first line at which two emitted documents differ (§25.8)."""
    la, lb = a.split("\n"), b.split("\n")
    for i in range(max(len(la), len(lb))):
        x = la[i] if i < len(la) else "<missing>"
        y = lb[i] if i < len(lb) else "<missing>"
        if x != y:
            return [
                f"first byte difference at line {i + 1}",
                f"  {label_a}: {x!r}",
                f"  {label_b}: {y!r}",
            ]
    return ["documents differ but no line differs (trailing bytes)"]


def _compare_schemas_nway(schemas: dict[str, str]) -> list[str]:
    """All-identical BYTE assertion across N emitted schema documents (§25.8).

    Returns [] when every target's document is byte-identical. Otherwise groups
    the targets by identical bytes; when a unique largest group exists it is the
    majority and every other target is reported as the odd one out, with the
    first line at which it diverges. With no unique majority (an even split),
    every other target is compared against the first registered target.
    """
    keys = dict(schemas)
    groups: dict[str, list[str]] = {}
    for t in schemas:  # preserves registration order within groups
        groups.setdefault(keys[t], []).append(t)
    if len(groups) == 1:
        return []

    diffs: list[str] = []
    sizes = sorted((len(ts) for ts in groups.values()), reverse=True)
    majority_is_unique = len(sizes) == 1 or sizes[0] > sizes[1]

    if majority_is_unique:
        majority = max(groups.values(), key=len)
        odd = [t for t in schemas if t not in majority]
        diffs.append(
            f"odd one out: {', '.join(odd)} "
            f"(majority: {', '.join(majority)})"
        )
        base_label = f"majority({','.join(majority)})"
        base = schemas[majority[0]]
    else:
        ordered = list(schemas)
        base_label = ordered[0]
        base = schemas[base_label]
        odd = [t for t in ordered[1:] if keys[t] != keys[base_label]]
        diffs.append(
            f"no majority; diffing against {base_label}: {', '.join(odd)} differ"
        )

    for t in odd:
        diffs.extend(_first_line_diff(base, schemas[t], base_label, t))
    return diffs


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def _presence_violations(node: object, path: str = "$") -> list[str]:
    """Every flag or arg entry in a dumped schema that fails §23's promises.

    The erasure this asserts against is the point of the assertion rather than
    an afterthought (contract §13's presence-round amendment): before the round
    the dumped schema described a flag's presence NOWHERE, so a required flag
    and an explicitly-optional one serialized identically and three
    implementations certified agreement about a fact none of them emitted. A
    parity check that cannot fail on a missing field would let that happen
    again, so a missing key is a failure here, never a silently-equal pair of
    absences.

    Three facts are checked on every entry: `presence` is present and is one of
    the three declarable values; `default` is present exactly when `presence`
    is "default"; and an arg entry carries no `required` key, which the round
    deleted rather than keeping beside presence.
    """
    problems: list[str] = []
    if isinstance(node, dict):
        for kind, key in (("flag", "global_flags"), ("flag", "flags"), ("arg", "args")):
            entries = node.get(key)
            if not isinstance(entries, list):
                continue
            for i, entry in enumerate(entries):
                if not isinstance(entry, dict):
                    continue
                where = f"{path}.{key}[{i}]"
                name = entry.get("name", "?")
                presence = entry.get("presence")
                if presence is None:
                    problems.append(
                        f"{where} ({name!r}): no 'presence' key -- presence is "
                        "always emitted (§13)"
                    )
                elif presence not in ("required", "optional", "default"):
                    problems.append(
                        f"{where} ({name!r}): presence is {presence!r}, expected "
                        "one of required, optional, default"
                    )
                has_default = "default" in entry
                if presence == "default" and not has_default:
                    problems.append(
                        f"{where} ({name!r}): presence is 'default' with no "
                        "'default' key -- the value is emitted always, "
                        "including [], {}, '', false and 0 (§13)"
                    )
                if presence in ("required", "optional") and has_default:
                    problems.append(
                        f"{where} ({name!r}): presence is {presence!r} but a "
                        "'default' key is emitted -- default is emitted exactly "
                        "when presence is 'default' (§13)"
                    )
                if kind == "arg" and "required" in entry:
                    problems.append(
                        f"{where} ({name!r}): the arg entry's 'required' key is "
                        "deleted by §23.3, not kept beside presence"
                    )
        for key, value in node.items():
            if key in ("defaults", "global_flags", "flags", "args"):
                continue
            problems.extend(_presence_violations(value, f"{path}.{key}"))
    elif isinstance(node, list):
        for i, value in enumerate(node):
            problems.extend(_presence_violations(value, f"{path}[{i}]"))
    return problems


def main() -> int:
    print("Building Go harness...", flush=True)
    try:
        harness = _build_harness()
    except RuntimeError as e:
        print(f"FAILED: {e}", file=sys.stderr)
        return 1

    print("Building TypeScript dist...", flush=True)
    try:
        ts_entry = _build_ts_harness()
    except RuntimeError as e:
        print(f"FAILED: {e}", file=sys.stderr)
        return 1

    app_defs = [
        ("rich app", RICH_APP),
        ("minimal app", MINIMAL_APP),
        ("config fields app", CONFIG_APP),
    ]

    all_diffs: list[tuple[str, list[str]]] = []

    for label, app_def in app_defs:
        print(f"Testing {label}...", flush=True)

        schemas: dict[str, str] = {}
        failed = False
        for target in TARGET_NAMES:
            try:
                raw = _run_dump_schema(
                    app_def, target, harness_binary=harness, ts_entry=ts_entry
                )
            except RuntimeError as e:
                print(f"  {target} FAILED: {e}", file=sys.stderr)
                all_diffs.append((label, [f"{target} failed: {e}"]))
                failed = True
                break
            schemas[target] = _strip_project_id(raw)
        if failed:
            continue

        diffs: list[str] = []
        for target, text in schemas.items():
            parsed = json.loads(text)
            diffs.extend(
                f"{target}: {problem}" for problem in _presence_violations(parsed)
            )
        diffs.extend(_compare_schemas_nway(schemas))
        if diffs:
            all_diffs.append((label, diffs))
            print(f"  {len(diffs)} difference(s) found")
        else:
            print(f"  PASS (byte-identical across {', '.join(TARGET_NAMES)})")

    # Cleanup harness
    harness_path = HARNESS_DIR / "harness"
    if harness_path.exists():
        os.unlink(harness_path)

    if all_diffs:
        print()
        print(f"Schema parity check FAILED ({sum(len(d) for _, d in all_diffs)} difference(s)):")
        print("=" * 60)
        for label, diffs in all_diffs:
            print(f"\n{label}:")
            for d in diffs:
                print(f"  - {d}")
        return 1

    print()
    print("Schema parity check passed.")
    print(f"  Apps tested: {len(app_defs)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
