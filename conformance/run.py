#!/usr/bin/env python3
"""Conformance test runner for strictcli implementations.

Reads JSON test cases from conformance/cases/, generates reference apps,
invokes them as subprocesses, and compares the results against expectations.

Usage:
    python conformance/run.py --target python
"""

from __future__ import annotations

import argparse
import atexit
import json
import os
import queue
import re
import shutil
import subprocess
import sys
import tempfile
import textwrap
import threading
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable

import jsonschema

# Resolve paths relative to this file
CONFORMANCE_DIR = Path(__file__).resolve().parent
CASES_DIR = CONFORMANCE_DIR / "cases"
SCHEMA_PATH = CONFORMANCE_DIR / "schema.json"
PROJECT_ROOT = CONFORMANCE_DIR.parent

# Go harness: single pre-built binary for all test cases
HARNESS_DIR = CONFORMANCE_DIR / "harness"
HARNESS_BINARY: str | None = None

# TypeScript harness: plain Node ESM script (conformance/harness_ts/main.js)
# that interprets the app definition at runtime. Its only build prerequisite is
# the typescript package's dist (built once per run, cached like the Go binary).
HARNESS_TS_DIR = CONFORMANCE_DIR / "harness_ts"
HARNESS_TS_ENTRY: str | None = None

# Every case runs with HOME pointing at a throwaway directory rather than at the
# operator's home. The process trace store (docs/process-trace-store.md) derives
# its location literally from HOME, and a case that starts a real child writes an
# entry -- which must land in a throwaway store, never in the operator's. The
# trace sweeps (sweep_trace.py) reassign TRACE_HOME per condition, which is also
# how the broken-store sweep points a run at an unwritable store. Toolchain
# builds (go build, npm run build) happen before any case runs and use the real
# environment, so no build cache is redirected here.
#
# `~/.local/lib` is symlinked back to the operator's, because the Python target
# runs under the ambient interpreter and resolves its dependencies through the
# per-user site directory that lives there. Only `~/.local/share` -- where the
# store sits -- is genuinely fresh.


def remove_trace_home(home: str) -> None:
    """Remove a throwaway HOME, deliberately unwritable directories included.

    A sweep condition chmods the store to 0o500 to make it unwritable, so the
    tree has to be made removable before it can be removed. os.walk descends
    top-down and does not follow symlinks, so each directory is widened before
    it is read and the ~/.local/lib symlink is unlinked rather than followed.
    """
    for root, dirs, _files in os.walk(home):
        for name in dirs:
            path = os.path.join(root, name)
            if os.path.islink(path):
                continue
            try:
                os.chmod(path, 0o700)
            except OSError:
                pass
    shutil.rmtree(home, ignore_errors=True)


def make_trace_home(prefix: str = "strictcli_conf_home_") -> str:
    """Create a throwaway HOME whose ~/.local/share is empty.

    Removal is registered here rather than at the call sites: the module-level
    TRACE_HOME below has no call site to put it in, and a caller that releases
    its own home early (sweep_trace) removes an already-removed path without
    error.
    """
    home = tempfile.mkdtemp(prefix=prefix)
    os.makedirs(os.path.join(home, ".local"), exist_ok=True)
    real_lib = os.path.join(os.path.expanduser("~"), ".local", "lib")
    if os.path.isdir(real_lib):
        os.symlink(real_lib, os.path.join(home, ".local", "lib"))
    atexit.register(remove_trace_home, home)
    return home


TRACE_HOME: str = make_trace_home()


def _ensure_harness() -> str:
    """Build the Go harness binary if not already built. Returns path to binary.

    The binary is left on disk when the run ends. It is gitignored, every
    process rebuilds it once before using it, and deleting it at the end used
    to break any other conformance tool (fuzz.py, sweep_trace.py, a second
    run.py) that was mid-run against the same shared path in this checkout.
    """
    global HARNESS_BINARY
    if HARNESS_BINARY is not None:
        return HARNESS_BINARY

    binary = str(HARNESS_DIR / "harness")

    # Build the harness
    result = subprocess.run(
        ["go", "build", "-o", binary, "."],
        cwd=str(HARNESS_DIR),
        capture_output=True,
        text=True,
        timeout=60,
    )
    if result.returncode != 0:
        raise RuntimeError(f"harness build failed:\n{result.stderr}")

    HARNESS_BINARY = binary
    return binary


def _ensure_ts_harness() -> str:
    """Build typescript/dist if not already built this run. Returns the harness entry path.

    The harness itself (main.js) has no build step; the prerequisite is a fresh
    typescript/dist, rebuilt once per run so engine changes are always picked up.

    SINGLE-CHECKOUT ASSUMPTION: the rebuild happens in place, in the one shared
    typescript/dist of this checkout. Two conformance tools running at the same
    time in the same checkout (run.py, fuzz.py, sweep_trace.py) will rebuild it
    under each other and read half-written output. Run them one at a time, or
    give each a checkout of its own.
    """
    global HARNESS_TS_ENTRY
    if HARNESS_TS_ENTRY is not None:
        return HARNESS_TS_ENTRY

    result = subprocess.run(
        ["npm", "run", "build"],
        cwd=str(PROJECT_ROOT / "typescript"),
        capture_output=True,
        text=True,
        timeout=300,
    )
    if result.returncode != 0:
        raise RuntimeError(
            f"typescript dist build failed:\n{result.stdout}\n{result.stderr}"
        )

    HARNESS_TS_ENTRY = str(HARNESS_TS_DIR / "main.js")
    return HARNESS_TS_ENTRY


def _load_schema() -> tuple[dict, dict]:
    """Load the conformance schema and return (full_schema, test_case_schema).

    The test_case_schema is the $defs/test_case definition with a local $defs
    copy so $ref pointers resolve correctly when validating individual cases.
    """
    with open(SCHEMA_PATH) as f:
        full_schema = json.load(f)
    # Build a standalone schema for a single test_case that carries all $defs
    test_case_schema = dict(full_schema["$defs"]["test_case"])
    test_case_schema["$defs"] = full_schema["$defs"]
    return full_schema, test_case_schema


def _load_cases() -> list[tuple[str, dict]]:
    """Load all test cases from JSON files. Returns (filename, case) pairs.

    Validates each non-exempt case against schema.json. Exits on first failure.
    """
    _, test_case_schema = _load_schema()
    cases = []
    for json_file in sorted(CASES_DIR.glob("*.json")):
        with open(json_file) as f:
            data = json.load(f)
        for case in data:
            if not case.get("skip_schema_validation", False):
                try:
                    jsonschema.validate(instance=case, schema=test_case_schema)
                except jsonschema.ValidationError as e:
                    print(
                        f"Schema validation failed for case "
                        f"{case.get('name', '<unnamed>')!r} in {json_file.name}:",
                        file=sys.stderr,
                    )
                    print(f"  {e.message}", file=sys.stderr)
                    sys.exit(1)
            cases.append((json_file.name, case))
    return cases


def _generate_python_script(app_def: dict) -> str:
    """Generate a Python script from an app definition."""
    from ref_python import generate
    return generate(app_def)


def _normalize(s: str) -> str:
    """Normalize a string for comparison (strip trailing whitespace per line, strip trailing newline)."""
    return "\n".join(line.rstrip() for line in s.rstrip("\n").split("\n"))


def _check_contains(actual: str, expected, stream_name: str) -> list[str]:
    """Check that actual contains expected substring(s). Returns list of error messages."""
    errors = []
    if isinstance(expected, str):
        expected = [expected]
    for s in expected:
        if s not in actual:
            errors.append(f"  {stream_name} does not contain: {s!r}")
            errors.append(f"  actual {stream_name}: {actual!r}")
    return errors


def _check_not_contains(actual: str, expected, stream_name: str) -> list[str]:
    """Check that actual does NOT contain the specified substring(s)."""
    errors = []
    if isinstance(expected, str):
        expected = [expected]
    for s in expected:
        if s in actual:
            errors.append(f"  {stream_name} should NOT contain: {s!r}")
            errors.append(f"  actual {stream_name}: {actual!r}")
    return errors


def _check_matches(actual: str, expected, stream_name: str) -> list[str]:
    """Check that actual matches expected regex pattern(s) via re.search."""
    errors = []
    if isinstance(expected, str):
        expected = [expected]
    for pat in expected:
        if not re.search(pat, actual):
            errors.append(f"  {stream_name} does not match pattern: {pat!r}")
            errors.append(f"  actual {stream_name}: {actual!r}")
    return errors


def _check_equals(actual: str, expected: str, stream_name: str) -> list[str]:
    """Check exact match. Returns list of error messages."""
    errors = []
    actual_norm = _normalize(actual)
    expected_norm = _normalize(expected)
    if actual_norm != expected_norm:
        errors.append(f"  {stream_name} mismatch:")
        errors.append(f"    expected: {expected_norm!r}")
        errors.append(f"    actual:   {actual_norm!r}")
    return errors


# The per-field wildcard: an expected value of exactly this string matches any
# actual value, of any type, at that position. It exists for the fields whose
# value is nondeterministic by construction -- a continuation signature, a
# timestamp -- where the CONTRACT is that the field is present and the
# comparison of its content is meaningless.
ANY_VALUE = "$ANY"


def _structural_equal(actual, expected, path: str = "") -> list[str]:
    """Compare two parsed JSON structures structurally. Returns mismatch lines.

    Key order is never part of the comparison, so nothing is sorted and nothing
    is canonicalized: two objects agree when their key SETS agree (checked as
    sets, failing fast on the first difference and naming which side held the
    extra keys) and every shared key's value agrees, recursively. An expected
    value of ANY_VALUE matches anything.

    This is the one comparison the structure-aware assertions share --
    effects_equals, schema_command_keys and the scripted-protocol line matcher
    all route through it, so the wildcard means the same thing everywhere.
    """
    where = path or "<root>"
    if expected == ANY_VALUE:
        return []
    if isinstance(expected, dict):
        if not isinstance(actual, dict):
            return [f"    {where}: expected an object, got {type(actual).__name__}"]
        missing = sorted(set(expected) - set(actual))
        extra = sorted(set(actual) - set(expected))
        if missing or extra:
            parts = []
            if missing:
                parts.append(f"missing {missing}")
            if extra:
                parts.append(f"unexpected {extra}")
            return [f"    {where}: key sets differ ({'; '.join(parts)})"]
        errors: list[str] = []
        for key in expected:
            errors.extend(
                _structural_equal(actual[key], expected[key], f"{where}.{key}")
            )
        return errors
    if isinstance(expected, list):
        if not isinstance(actual, list):
            return [f"    {where}: expected an array, got {type(actual).__name__}"]
        if len(actual) != len(expected):
            return [
                f"    {where}: array length {len(actual)}, expected {len(expected)}"
            ]
        errors = []
        for i, (a, e) in enumerate(zip(actual, expected)):
            errors.extend(_structural_equal(a, e, f"{where}[{i}]"))
        return errors
    if actual != expected:
        return [f"    {where}: {actual!r}, expected {expected!r}"]
    return []


# The optional keys of $defs/effect_record. Effects contract §14.1: absent
# optional keys and explicit-null keys are equivalent, so both sides drop them
# before comparison. `recorded` is deliberately NOT in this set -- it is a
# required key precisely so that "absent" never has to mean both "live" and
# "unstated" at once.
_EFFECT_RECORD_OPTIONAL_KEYS = ("bytes", "resource", "skip_if_current", "grant")


def _normalize_effect_record(rec: dict) -> dict:
    """Drop optional keys whose value is absent or null."""
    return {
        k: v
        for k, v in rec.items()
        if not (k in _EFFECT_RECORD_OPTIONAL_KEYS and v is None)
    }


def _check_effects_equals(log_path: str | None, expected: list) -> list[str]:
    """Deep-equality assertion against the structured effect log (§14.1)."""
    if log_path is None or not os.path.exists(log_path):
        return [
            "  effects_equals: the harness wrote no effect log "
            f"(expected it at {log_path!r})"
        ]
    with open(log_path, encoding="utf-8") as fh:
        raw = fh.read()
    try:
        actual = json.loads(raw)
    except json.JSONDecodeError as e:
        return [f"  effects_equals: effect log is not valid JSON: {e}"]

    actual_norm = [_normalize_effect_record(r) for r in actual]
    expected_norm = [_normalize_effect_record(r) for r in expected]
    mismatches = _structural_equal(actual_norm, expected_norm, "effects")
    if not mismatches:
        return []
    return [
        "  effects_equals mismatch:",
        *mismatches,
        f"    expected: {json.dumps(expected_norm, sort_keys=True)}",
        f"    actual:   {json.dumps(actual_norm, sort_keys=True)}",
    ]


SCHEMA_ASSERT_KEYS = (
    "schema_command_keys", "schema_command_absent_keys", "schema_bytes_equal",
)


def _check_schema_bytes(proj_dir: str | None, expected: str) -> list[str]:
    """Assert the emitted schema file's WHOLE BYTES (effects contract §25.8).

    The committed `.strictcli/schema.json` must be dumper-independent: a
    repository whose file is written sometimes by a Go binary and sometimes by
    a Python one must see a diff exactly when something changed. Key order,
    escaping, number form, indentation and the single trailing newline are all
    part of the contract after v2, so this assertion reads the file as text and
    compares it whole rather than parsing it first.
    """
    if proj_dir is None:
        return ["  schema_bytes_equal requires --dump-schema in the case argv"]
    path = os.path.join(proj_dir, ".strictcli", "schema.json")
    if not os.path.exists(path):
        return [f"  schema_bytes_equal: no schema was emitted at {path}"]
    with open(path, encoding="utf-8") as fh:
        actual = fh.read()
    if actual == expected:
        return []
    exp_lines = expected.split("\n")
    act_lines = actual.split("\n")
    for i in range(max(len(exp_lines), len(act_lines))):
        e = exp_lines[i] if i < len(exp_lines) else "<missing>"
        a = act_lines[i] if i < len(act_lines) else "<missing>"
        if e != a:
            return [
                f"  schema_bytes_equal: first difference at line {i + 1}",
                f"    expected: {e!r}",
                f"    actual:   {a!r}",
            ]
    return ["  schema_bytes_equal: files differ but no line differs"]


def _resolve_schema_command(schema: dict, dotted: str) -> dict | None:
    """Walk an emitted schema's group tree to the command at a dotted path.

    `groups` and `commands` are name-keyed objects in every implementation's
    dump, so the walk is a plain key lookup per segment.
    """
    node: dict = schema
    parts = dotted.split(".")
    for part in parts[:-1]:
        node = (node.get("groups") or {}).get(part)
        if not isinstance(node, dict):
            return None
    entry = (node.get("commands") or {}).get(parts[-1])
    return entry if isinstance(entry, dict) else None


def _check_schema_commands(proj_dir: str | None, expect: dict) -> list[str]:
    """Assert per-command key presence/absence in the emitted schema file.

    Structural, not textual: key order and indentation are not part of the
    contract, but WHICH keys a command entry carries is. The emit-when-declared
    pairs (`dry_run_supported`/`dry_run_unsupported_reason`, `consequential`)
    only mean anything if their absence is pinned too -- an implementation that
    emitted a default-valued key where its siblings omit it would otherwise be
    a silent schema divergence.
    """
    if proj_dir is None:
        return [
            "  schema_command_* assertion requires --dump-schema in the case argv"
        ]
    path = os.path.join(proj_dir, ".strictcli", "schema.json")
    if not os.path.exists(path):
        return [f"  schema_command_*: no schema was emitted at {path}"]
    with open(path, encoding="utf-8") as fh:
        try:
            schema = json.load(fh)
        except json.JSONDecodeError as e:
            return [f"  schema_command_*: emitted schema is not valid JSON: {e}"]

    errors: list[str] = []
    for dotted, fields in expect.get("schema_command_keys", {}).items():
        entry = _resolve_schema_command(schema, dotted)
        if entry is None:
            errors.append(
                f"  schema_command_keys: no command {dotted!r} in the emitted schema"
            )
            continue
        for key, want in fields.items():
            if key not in entry:
                errors.append(
                    f"  schema_command_keys: {dotted}.{key} is absent, "
                    f"expected {want!r}"
                )
                continue
            # Structural, and wildcard-aware: a value declared ANY_VALUE
            # asserts the key's presence and nothing about its content.
            mismatches = _structural_equal(
                entry[key], want, f"{dotted}.{key}"
            )
            for line in mismatches:
                errors.append(f"  schema_command_keys:{line}")
    for dotted, keys in expect.get("schema_command_absent_keys", {}).items():
        entry = _resolve_schema_command(schema, dotted)
        if entry is None:
            errors.append(
                "  schema_command_absent_keys: no command "
                f"{dotted!r} in the emitted schema"
            )
            continue
        for key in keys:
            if key in entry:
                errors.append(
                    f"  schema_command_absent_keys: {dotted}.{key} is present "
                    f"({entry[key]!r}), expected absent"
                )
    return errors


# --- the line-scripted protocol driver ---------------------------------------
#
# A `stdin` case hands the child its whole input up front, which is enough for a
# protocol whose requests never depend on an earlier response. A scripted case
# is the interactive form: send one line, read the reply, decide, send the next.
# That is what a request/response protocol -- the MCP loop above all, and its
# confirmation round-trip -- actually is, and it cannot be expressed by a static
# blob.
#
# The child's streams are drained by reader threads so a step that waits for a
# line can never deadlock against a full pipe buffer, and every read carries its
# own timeout so a missing reply is a named failure rather than a hang.

_SCRIPT_STEP_TIMEOUT = 10.0

# A step may CAPTURE a value out of its reply and a later step may splice it
# into what it sends. That is the only way to script a protocol whose later
# requests quote something the server minted -- a continuation state blob, say,
# which is by construction unguessable and different every run.
#
#   {"send": "...", "capture": {"state": "result.requestState"}}
#   {"send": "... \"requestState\":\"{{state}}\" ..."}
#   {"send": "... \"requestState\":\"{{state|tamper}}\" ..."}
#
# `capture` maps a name to a dotted path into the parsed reply. The
# substitution vocabulary is closed: a bare `{{name}}` splices the captured
# text, and `{{name|tamper}}` splices it with its FIRST character changed,
# which is how a case asserts that an integrity-protected value is actually
# checked. The first character and not the last: base64 decoders ignore the
# trailing bits of a final character that does not fill a byte, so changing the
# last character of a base64url blob can decode to the identical bytes -- which
# is exactly what a Go run of the tamper case hit, passing verification and
# running the command the case was asserting it would not.

_CAPTURE_RE = re.compile(r"\{\{([A-Za-z_][A-Za-z0-9_]*)(\|tamper)?\}\}")


def _capture_from_reply(line: str, paths: dict, captured: dict) -> list[str]:
    """Pull declared values out of one reply line into the capture table."""
    errors: list[str] = []
    try:
        parsed = json.loads(line)
    except json.JSONDecodeError:
        return [f"  protocol_script: cannot capture from a non-JSON line: {line!r}"]
    for name, path in paths.items():
        cursor = parsed
        for part in str(path).split("."):
            if not isinstance(cursor, dict) or part not in cursor:
                errors.append(
                    f"  protocol_script: capture {name!r} found no {path!r} in {line!r}"
                )
                cursor = None
                break
            cursor = cursor[part]
        if cursor is not None:
            captured[name] = cursor
    return errors


def _splice_captures(text: str, captured: dict) -> tuple[str, list[str]]:
    """Substitute `{{name}}` / `{{name|tamper}}` in a step's line to send."""
    errors: list[str] = []

    def replace(match: "re.Match[str]") -> str:
        name = match.group(1)
        if name not in captured:
            errors.append(f"  protocol_script: nothing captured under {name!r}")
            return match.group(0)
        value = str(captured[name])
        if match.group(2) and value:
            first = value[0]
            return ("A" if first != "A" else "B") + value[1:]
        return value

    return _CAPTURE_RE.sub(replace, text), errors


def _drain(stream, sink: list[str], lines: "queue.Queue[str | None]" = None):
    """Read a child stream line by line into `sink` (and optionally a queue)."""
    for line in stream:
        sink.append(line)
        if lines is not None:
            lines.put(line)
    if lines is not None:
        lines.put(None)  # sentinel: the stream closed
    stream.close()


def _check_protocol_line(line: str | None, expect: dict, step_no: int) -> list[str]:
    """Assert one response line against a step's expectation block."""
    where = f"  protocol_script step {step_no}"
    if line is None:
        return [f"{where}: the stream closed before a line arrived"]
    text = line.rstrip("\n")
    errors: list[str] = []
    if "equals" in expect and text != expect["equals"]:
        errors.append(f"{where}: line {text!r}, expected {expect['equals']!r}")
    if "contains" in expect:
        errors.extend(_check_contains(text, expect["contains"], f"{where} line"))
    if "matches" in expect:
        errors.extend(_check_matches(text, expect["matches"], f"{where} line"))
    if "json_equals" in expect:
        try:
            parsed = json.loads(text)
        except json.JSONDecodeError as e:
            errors.append(f"{where}: line is not valid JSON ({e}): {text!r}")
        else:
            mismatches = _structural_equal(
                parsed, expect["json_equals"], f"step {step_no}"
            )
            if mismatches:
                errors.append(f"{where}: json_equals mismatch:")
                errors.extend(mismatches)
    return errors


def _run_protocol_script(
    argv: list[str], env: dict, run_cwd: str, script: list[dict]
) -> tuple[subprocess.CompletedProcess, list[str]]:
    """Drive a child through a scripted request/response line exchange.

    Returns a CompletedProcess carrying the child's full streams (so the case's
    ordinary expect block and the N-way comparison work unchanged) plus the
    per-step assertion failures.
    """
    errors: list[str] = []
    proc = subprocess.Popen(
        argv,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        env=env,
        cwd=run_cwd,
        bufsize=1,
    )
    out_lines: "queue.Queue[str | None]" = queue.Queue()
    err_lines: "queue.Queue[str | None]" = queue.Queue()
    out_sink: list[str] = []
    err_sink: list[str] = []
    threads = [
        threading.Thread(
            target=_drain, args=(proc.stdout, out_sink, out_lines), daemon=True
        ),
        threading.Thread(
            target=_drain, args=(proc.stderr, err_sink, err_lines), daemon=True
        ),
    ]
    for t in threads:
        t.start()

    captured: dict = {}
    try:
        for i, step in enumerate(script, start=1):
            if "send" in step:
                line_to_send, splice_errors = _splice_captures(step["send"], captured)
                errors.extend(splice_errors)
                try:
                    proc.stdin.write(line_to_send + "\n")
                    proc.stdin.flush()
                except (BrokenPipeError, ValueError):
                    errors.append(
                        f"  protocol_script step {i}: the child closed its stdin "
                        f"before the line could be sent"
                    )
                    break
            expect_line = step.get("expect_line")
            capture = step.get("capture")
            if expect_line is None and not capture:
                continue
            source = err_lines if step.get("stream") == "stderr" else out_lines
            try:
                line = source.get(timeout=_SCRIPT_STEP_TIMEOUT)
            except queue.Empty:
                errors.append(
                    f"  protocol_script step {i}: no line within "
                    f"{_SCRIPT_STEP_TIMEOUT:g}s"
                )
                break
            if expect_line is not None:
                errors.extend(_check_protocol_line(line, expect_line, i))
            if capture:
                if line is None:
                    errors.append(
                        f"  protocol_script step {i}: the stream closed before a "
                        f"line could be captured"
                    )
                    break
                errors.extend(_capture_from_reply(line, capture, captured))
    finally:
        try:
            proc.stdin.close()
        except (BrokenPipeError, ValueError):
            pass
        try:
            proc.wait(timeout=_SCRIPT_STEP_TIMEOUT)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()
            errors.append("  protocol_script: the child did not exit after the script")
        for t in threads:
            t.join(timeout=_SCRIPT_STEP_TIMEOUT)

    return (
        subprocess.CompletedProcess(
            args=argv,
            returncode=proc.returncode,
            stdout="".join(out_sink),
            stderr="".join(err_sink),
        ),
        errors,
    )


# --- N-way target registry ---------------------------------------------------
#
# Each registered target is a self-contained descriptor that knows how to
# prepare a case (turn an app definition + argv into an executable command) and
# how to write the project marker file needed for --dump-schema. All target-
# specific code lives in these descriptors; the comparison and orchestration
# logic below is fully target-agnostic. Adding a future target (e.g. TypeScript)
# is one _register_target(...) call and zero changes anywhere else.


@dataclass
class Preparation:
    """The result of preparing a case for one target: a runnable command."""

    argv: list[str]
    extra_env: dict[str, str]
    cleanup_paths: list[str] = field(default_factory=list)


@dataclass
class Target:
    """A conformance target descriptor.

    prepare(app_def, case_argv) -> Preparation
        Builds the argv/env for running the case and lists temp files to unlink.
        May raise RuntimeError if the target's toolchain fails to build; callers
        translate that into a per-case failure.

    write_project_file(dir, app_name) -> None
        Writes the project marker file (e.g. go.mod / pyproject.toml) that
        --dump-schema needs in the working directory to determine project_id.
    """

    name: str
    prepare: Callable[[dict, list[str]], Preparation]
    write_project_file: Callable[[str, str], None]


TARGETS: dict[str, Target] = {}


def _register_target(target: Target) -> None:
    """Register a target descriptor. The insertion order is the reporting order."""
    TARGETS[target.name] = target


def _prepare_python(app_def: dict, case_argv: list[str]) -> Preparation:
    script = _generate_python_script(app_def)
    # Fix the sys.path to use an absolute path so the script works from any
    # directory (ref_python.py emits a __file__-relative path that only works
    # inside the conformance dir).
    python_dir = str(PROJECT_ROOT / "python")
    script = script.replace(
        "sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'python'))",
        f"sys.path.insert(0, {python_dir!r})",
    )
    with tempfile.NamedTemporaryFile(
        mode="w", suffix=".py", prefix="strictcli_py_", delete=False
    ) as f:
        f.write(script)
        script_path = f.name
    return Preparation(
        argv=[sys.executable, script_path] + case_argv,
        extra_env={},
        cleanup_paths=[script_path],
    )


def _write_python_project_file(d: str, app_name: str) -> None:
    with open(os.path.join(d, "pyproject.toml"), "w") as f:
        f.write(f'[project]\nname = "{app_name}"\n')


def _prepare_go(app_def: dict, case_argv: list[str]) -> Preparation:
    binary = _ensure_harness()  # may raise RuntimeError; caller translates it
    # Write the app definition to a temp file for the harness to read.
    app_def_file = tempfile.NamedTemporaryFile(
        mode="w", suffix=".json", prefix="strictcli_def_", delete=False
    )
    json.dump(app_def, app_def_file, sort_keys=True)
    app_def_file.close()
    return Preparation(
        argv=[binary] + case_argv,
        extra_env={"CONFORMANCE_APP_DEF": app_def_file.name},
        cleanup_paths=[app_def_file.name],
    )


def _write_go_project_file(d: str, app_name: str) -> None:
    with open(os.path.join(d, "go.mod"), "w") as f:
        f.write(f"module {app_name}\n\ngo 1.21\n")


def _prepare_typescript(app_def: dict, case_argv: list[str]) -> Preparation:
    entry = _ensure_ts_harness()  # may raise RuntimeError; caller translates it
    # Write the app definition to a temp file for the harness to read.
    app_def_file = tempfile.NamedTemporaryFile(
        mode="w", suffix=".json", prefix="strictcli_def_", delete=False
    )
    json.dump(app_def, app_def_file, sort_keys=True)
    app_def_file.close()
    return Preparation(
        argv=["node", entry] + case_argv,
        extra_env={"CONFORMANCE_APP_DEF": app_def_file.name},
        cleanup_paths=[app_def_file.name],
    )


def _write_typescript_project_file(d: str, app_name: str) -> None:
    with open(os.path.join(d, "package.json"), "w") as f:
        json.dump({"name": app_name}, f)
        f.write("\n")


_register_target(Target("python", _prepare_python, _write_python_project_file))
_register_target(Target("go", _prepare_go, _write_go_project_file))
_register_target(
    Target("typescript", _prepare_typescript, _write_typescript_project_file)
)


def _run_case(case: dict, target: str) -> tuple[bool, list[str], subprocess.CompletedProcess | None]:
    """Run a single test case. Returns (passed, error_messages, raw_result)."""
    errors = []
    raw_result = None

    # Handle config_content: write to a temp file and override config_path.
    # If argv contains "$CONFIG_PATH", substitute the temp path into argv
    # instead of setting config_path on the app def (for --config flag tests).
    config_tmp_path = None
    late_config_tmp_path = None
    app_def = case["app"]
    case_argv = case["argv"]
    if "config_content" in app_def:
        config_format = app_def.get("config_format", "json")
        ext = ".toml" if config_format == "toml" else ".json"
        with tempfile.NamedTemporaryFile(
            mode="w", suffix=ext, prefix="strictcli_cfg_", delete=False
        ) as cfg_f:
            cfg_f.write(app_def["config_content"])
            config_tmp_path = cfg_f.name
        # Shallow copy so we don't mutate the original case
        app_def = dict(app_def)
        if any("$CONFIG_PATH" in arg for arg in case_argv):
            # Substitute $CONFIG_PATH in argv; don't set config_path on app
            case_argv = [
                arg.replace("$CONFIG_PATH", config_tmp_path)
                for arg in case_argv
            ]
        else:
            app_def["config_path"] = config_tmp_path

    # Handle config_content_late: create a temp path for the config file,
    # set config_path to it, but let the generated code write the content
    # AFTER app construction (between construction and app.run()).
    if "config_content_late" in app_def:
        config_format = app_def.get("config_format", "json")
        ext = ".toml" if config_format == "toml" else ".json"
        late_config_tmp_path = tempfile.mktemp(
            suffix=ext, prefix="strictcli_lcfg_",
        )
        # Shallow copy so we don't mutate the original case
        if app_def is case["app"]:
            app_def = dict(app_def)
        app_def["config_path"] = late_config_tmp_path
        # Keep config_content_late in app_def so generators can emit the write code

    descriptor = TARGETS.get(target)
    if descriptor is None:
        return False, [f"  unsupported target: {target}"], None
    try:
        prep = descriptor.prepare(app_def, case_argv)
    except RuntimeError as e:
        return False, [f"  harness build error: {e}"], None
    argv = prep.argv
    extra_env = prep.extra_env
    cleanup_paths = prep.cleanup_paths

    # The structured effect-log side channel (effects contract §14.3): the same
    # env-var file handoff as CONFORMANCE_APP_DEF, set ONLY for a case that
    # declares effects_equals. It selects a diagnostic destination and changes
    # no behavior -- it is not the deleted A9 mode token.
    effect_log_path = None
    if "effects_equals" in case["expect"]:
        effect_log_path = tempfile.mktemp(
            suffix=".json", prefix="strictcli_efflog_",
        )
        extra_env = dict(extra_env)
        extra_env["CONFORMANCE_EFFECT_LOG"] = effect_log_path
        cleanup_paths = list(cleanup_paths) + [effect_log_path]

    # --dump-schema needs the target's project marker file (go.mod / pyproject.toml)
    # in the CWD to determine project_id. Create a temp dir with the right file.
    # A test_coverage_dir case needs a writable temp dir: the declared directory
    # is a path relative to it, and the shard files are written underneath.
    proj_dir = None
    if "--dump-schema" in case_argv:
        proj_dir = tempfile.mkdtemp(prefix="strictcli_proj_")
        descriptor.write_project_file(proj_dir, app_def["name"])
        run_cwd = proj_dir
    elif "test_coverage_dir" in app_def:
        proj_dir = tempfile.mkdtemp(prefix="strictcli_cov_")
        run_cwd = proj_dir
        # The runner creates the fixture root and NOTHING below it, so the
        # declared path itself carries the existence fact a case is pinning:
        # "." names the fixture root (present, so the app instruments), and any
        # other value names a directory that is absent (so it does not).
        #
        # Seed a committed coverage manifest so the check can be exercised on
        # the empty-shard path. Target-agnostic: the same seeded file is read by
        # all three implementations.
        seed_manifest = app_def.get("coverage_manifest")
        if seed_manifest is not None:
            declared_dir = os.path.join(proj_dir, app_def["test_coverage_dir"])
            os.makedirs(declared_dir, exist_ok=True)
            with open(
                os.path.join(declared_dir, "test-coverage.json"),
                "w",
                encoding="utf-8",
            ) as mf:
                json.dump(seed_manifest, mf, indent=2)
                mf.write("\n")
    else:
        run_cwd = str(CONFORMANCE_DIR)

    try:
        # Build environment: inherit current env, overlay the throwaway HOME,
        # then the test env and the target extras.
        env = os.environ.copy()
        env["HOME"] = TRACE_HOME
        test_env = case.get("env", {})
        env.update(test_env)
        env.update(extra_env)

        # A case must never depend on the operator's terminal. /dev/null is
        # definitively not a TTY, which is what makes the confirm protocol's
        # non-interactive branch (effects contract §8.3) a pinnable,
        # deterministic outcome instead of a hang. A case that declares stdin
        # gets a pipe carrying exactly that text -- also not a TTY, so the
        # property holds; it is how the --mcp cases deliver their JSON-RPC
        # lines.
        case_stdin = case.get("stdin")
        script = case.get("protocol_script")
        if script is not None:
            result, script_errors = _run_protocol_script(
                argv, env, run_cwd, script
            )
            errors.extend(script_errors)
        else:
            result = subprocess.run(
                argv,
                capture_output=True,
                text=True,
                env=env,
                cwd=run_cwd,
                timeout=10,
                input=case_stdin,
                stdin=None if case_stdin is not None else subprocess.DEVNULL,
            )
        raw_result = result

        # Check exit code. `exit_code_by_target` overrides the baseline for a
        # target whose exit status is intrinsically its own -- an unrecovered Go
        # panic exits 2 where an uncaught Python exception and an uncaught Node
        # throw both exit 1 (effects contract §3.5: the framework does not
        # handle the crash, so the status is the language's). It overrides ONLY
        # the status, so the case stays one case and its stdout and stderr are
        # still compared across every target.
        expect = case["expect"]
        want_code = expect.get("exit_code_by_target", {}).get(
            target, expect["exit_code"]
        )
        if result.returncode != want_code:
            errors.append(
                f"  exit_code: expected {want_code}, got {result.returncode}"
            )
            if result.stderr:
                errors.append(f"  stderr: {result.stderr.rstrip()!r}")
            if result.stdout:
                errors.append(f"  stdout: {result.stdout.rstrip()!r}")

        # Check stdout
        if "stdout_contains" in expect:
            errors.extend(
                _check_contains(result.stdout, expect["stdout_contains"], "stdout")
            )
        if "stdout_equals" in expect:
            errors.extend(
                _check_equals(result.stdout, expect["stdout_equals"], "stdout")
            )
        if "stdout_not_contains" in expect:
            errors.extend(
                _check_not_contains(result.stdout, expect["stdout_not_contains"], "stdout")
            )
        if "stdout_matches" in expect:
            errors.extend(
                _check_matches(result.stdout, expect["stdout_matches"], "stdout")
            )

        # Check stderr
        if "stderr_contains" in expect:
            errors.extend(
                _check_contains(result.stderr, expect["stderr_contains"], "stderr")
            )
        if "stderr_equals" in expect:
            errors.extend(
                _check_equals(result.stderr, expect["stderr_equals"], "stderr")
            )
        if "stderr_not_contains" in expect:
            errors.extend(
                _check_not_contains(result.stderr, expect["stderr_not_contains"], "stderr")
            )
        if "stderr_matches" in expect:
            errors.extend(
                _check_matches(result.stderr, expect["stderr_matches"], "stderr")
            )

        # Check the seeded config file's content (after the run mutated it).
        cfg_assert_keys = (
            "config_file_contains",
            "config_file_not_contains",
            "config_file_matches",
        )
        if any(k in expect for k in cfg_assert_keys):
            cfg_path = config_tmp_path or late_config_tmp_path
            if cfg_path is None or not os.path.exists(cfg_path):
                errors.append(
                    "  config_file_* assertion requires a seeded config file "
                    "(config_content / config_content_late), but none was found"
                )
            else:
                with open(cfg_path, encoding="utf-8") as cf:
                    cfg_text = cf.read()
                if "config_file_contains" in expect:
                    errors.extend(
                        _check_contains(
                            cfg_text, expect["config_file_contains"], "config_file"
                        )
                    )
                if "config_file_not_contains" in expect:
                    errors.extend(
                        _check_not_contains(
                            cfg_text, expect["config_file_not_contains"], "config_file"
                        )
                    )
                if "config_file_matches" in expect:
                    errors.extend(
                        _check_matches(
                            cfg_text, expect["config_file_matches"], "config_file"
                        )
                    )

        # Check the structured effect log the run produced (§14.1).
        if "effects_equals" in expect:
            errors.extend(
                _check_effects_equals(effect_log_path, expect["effects_equals"])
            )

        # Check the schema file a --dump-schema run emitted into proj_dir.
        if any(k in expect for k in SCHEMA_ASSERT_KEYS):
            errors.extend(_check_schema_commands(proj_dir, expect))
        if "schema_bytes_equal" in expect:
            errors.extend(
                _check_schema_bytes(proj_dir, expect["schema_bytes_equal"])
            )

    except subprocess.TimeoutExpired:
        errors.append("  timed out after 10 seconds")
    except Exception as e:
        errors.append(f"  exception: {e}")
    finally:
        for cleanup_path in cleanup_paths:
            if cleanup_path is not None and os.path.exists(cleanup_path):
                os.unlink(cleanup_path)
        if config_tmp_path is not None:
            os.unlink(config_tmp_path)
        if late_config_tmp_path is not None and os.path.exists(late_config_tmp_path):
            os.unlink(late_config_tmp_path)
        if proj_dir is not None:
            shutil.rmtree(proj_dir, ignore_errors=True)

    return len(errors) == 0, errors, raw_result


def _normalize_temp_paths(s: str) -> str:
    """Replace temp directory paths with a placeholder so cross-target comparison ignores them."""
    tmpdir = re.escape(tempfile.gettempdir())
    return re.sub(
        tmpdir + r"/strictcli_[a-z]+_[a-zA-Z0-9_]+",
        "<TMPDIR>",
        s,
    )


def _stream_divergence(stream_name: str, values: dict[str, str]) -> list[str]:
    """Report N-way divergence for one stream.

    `values` maps target name -> normalized stream text. If all targets agree,
    returns []. Otherwise groups targets by identical output, identifies the odd
    one(s) out by majority (a unique largest group is the majority; every other
    target is odd), and emits a labeled diff. With no majority (e.g. two targets,
    or an even split) every distinct group is reported without an odd-one-out
    marker.
    """
    groups: dict[str, list[str]] = {}
    for tgt, val in values.items():
        groups.setdefault(val, []).append(tgt)
    if len(groups) == 1:
        return []

    sized = sorted(groups.items(), key=lambda kv: len(kv[1]), reverse=True)
    top_size = len(sized[0][1])
    majority_is_unique = sum(1 for _, tgts in sized if len(tgts) == top_size) == 1

    odd: list[str] = []
    if majority_is_unique:
        majority_targets = set(sized[0][1])
        odd = sorted(t for t in values if t not in majority_targets)

    header = f"  {stream_name} divergence"
    if odd:
        header += f" (odd one out: {', '.join(odd)})"
    header += ":"
    lines = [header]
    # Deterministic order: sort groups by their sorted target list.
    for val, tgts in sorted(groups.items(), key=lambda kv: sorted(kv[1])):
        label = ",".join(sorted(tgts))
        lines.append(f"    {label}: {val!r}")
    return lines


def _validate_acknowledged_divergence(
    case: dict, applicable: list[str]
) -> list[str]:
    """Validate a case's acknowledged_divergence block against its applicable targets.

    Rules (beyond the JSON schema): every acknowledged target must be
    applicable to the case, and at least one applicable target per stream must
    remain unacknowledged to serve as the comparison baseline.
    """
    ack = case.get("acknowledged_divergence")
    if ack is None:
        return []
    errors: list[str] = []
    for stream, targets in ack["streams"].items():
        unknown = sorted(set(targets) - set(applicable))
        if unknown:
            errors.append(
                f"acknowledged_divergence.streams.{stream}: target(s) "
                f"{', '.join(unknown)} not applicable to this case"
            )
        remaining = [t for t in applicable if t not in targets]
        if not remaining:
            errors.append(
                f"acknowledged_divergence.streams.{stream}: every applicable "
                f"target is acknowledged; at least one must remain as the "
                f"comparison baseline"
            )
    return errors


def _compare_outputs(
    results: dict[str, subprocess.CompletedProcess | None],
    acknowledged: dict | None = None,
) -> list[str]:
    """N-way comparison of normalized stdout/stderr across all targets.

    `results` maps target name -> CompletedProcess (or None for a target that
    produced no result). Targets with no result are excluded. If fewer than two
    targets produced comparable output, returns [] (nothing to compare). On
    divergence, returns a labeled diff identifying the odd one(s) out.

    `acknowledged` is the case's optional acknowledged_divergence block: targets
    listed under a stream are excluded from that stream's byte-identity
    comparison because their output is intentionally language-specific (parser
    prose, tracebacks, idiomatic API names). Double-entry: an acknowledged
    target whose output does NOT actually differ from every other target is a
    stale acknowledgment and is reported as a divergence warning.
    """
    warnings: list[str] = []
    present = {t: r for t, r in results.items() if r is not None}
    if len(present) < 2:
        return warnings

    ack_streams = acknowledged["streams"] if acknowledged else {}
    for stream_name, attr in (("stdout", "stdout"), ("stderr", "stderr")):
        vals = {
            t: _normalize_temp_paths(_normalize(getattr(r, attr)))
            for t, r in present.items()
        }
        ack = [t for t in ack_streams.get(stream_name, []) if t in vals]
        base_vals = {t: v for t, v in vals.items() if t not in ack}
        warnings.extend(_stream_divergence(stream_name, base_vals))
        for t in ack:
            others = [v for ot, v in vals.items() if ot != t]
            if others and all(vals[t] == v for v in others):
                warnings.append(
                    f"  {stream_name}: stale acknowledged divergence: {t!r} "
                    f"output is identical to every other target"
                )

    return warnings


@dataclass
class ParityReport:
    """Aggregate outcome of an N-way parity run."""

    passed: int = 0
    parity_failures: int = 0
    output_divergences: int = 0
    consistent_failures: int = 0
    parity_failure_details: list[tuple[str, str]] = field(default_factory=list)
    divergence_details: list[tuple[str, list[str]]] = field(default_factory=list)

    @property
    def total(self) -> int:
        return self.passed + self.parity_failures + self.consistent_failures

    @property
    def exit_code(self) -> int:
        return 0 if self.parity_failures == 0 else 1


def _applicable_targets(case: dict, target_names: list[str]) -> list[str]:
    """Targets a case runs on: intersection of registered and declared targets.

    A case's `targets` key (if present) restricts it to those implementations;
    absent means all. Registration order is preserved.
    """
    declared = case.get("targets")
    if declared is None:
        return list(target_names)
    declared_set = set(declared)
    return [t for t in target_names if t in declared_set]


def _run_parity_mode(
    cases: list[tuple[str, dict]], target_names: list[str], verbose: bool
) -> ParityReport:
    """Run all cases against every applicable registered target and assert parity.

    For each case, runs the intersection of registered and case-declared targets
    and asserts their outputs are byte-identical. A case applicable to fewer than
    two targets is skipped (nothing to compare). Classification per case:

    - all targets pass their own assertions -> counted as passed; outputs are then
      compared N-way and any divergence is reported as a (non-fatal) warning.
    - all targets fail -> a consistent failure (not a parity break).
    - some pass and some fail -> a parity failure (fatal: sets a nonzero exit).
    """
    report = ParityReport()

    for filename, case in cases:
        name = case["name"]
        label = f"{filename}: {name}"

        applicable = _applicable_targets(case, target_names)
        if len(applicable) < 2:
            # Fewer than two targets run this case; parity is undefined.
            continue

        ack_errors = _validate_acknowledged_divergence(case, applicable)
        if ack_errors:
            print(f"invalid acknowledged_divergence in {label}:", file=sys.stderr)
            for e in ack_errors:
                print(f"  {e}", file=sys.stderr)
            sys.exit(1)

        if verbose:
            print(f"  running: {label} ...", end=" ", flush=True)

        outcomes = {t: _run_case(case, t) for t in applicable}
        oks = {t: outcomes[t][0] for t in applicable}
        results = {t: outcomes[t][2] for t in applicable}

        if all(oks.values()):
            # All targets pass -- check output divergence.
            report.passed += 1
            div_warnings = _compare_outputs(
                results, case.get("acknowledged_divergence")
            )
            if div_warnings:
                report.output_divergences += 1
                report.divergence_details.append((label, div_warnings))
                if verbose:
                    print("PASS (output divergence)")
            else:
                if verbose:
                    print("PASS")
        elif not any(oks.values()):
            # All targets fail -- consistent.
            report.consistent_failures += 1
            extra = ""
            codes = {
                t: (results[t].returncode if results[t] is not None else None)
                for t in applicable
            }
            if len(set(codes.values())) > 1:
                joined = ", ".join(f"{t}={codes[t]}" for t in applicable)
                extra = f" (exit codes differ: {joined})"
            if verbose:
                print(f"CONSISTENT FAIL{extra}")
        else:
            # Parity failure -- some targets pass, some fail.
            report.parity_failures += 1
            detail = ", ".join(
                f"{t}={'PASS' if oks[t] else 'FAIL'}" for t in applicable
            )
            report.parity_failure_details.append((label, detail))
            if verbose:
                print(f"PARITY FAIL ({detail})")

    # Print parity failures
    if report.parity_failure_details:
        print()
        print("PARITY FAILURES:")
        print("=" * 60)
        for label, detail in report.parity_failure_details:
            print(f"\n{label}")
            print(f"  {detail}")
        print()

    # Print output divergence warnings
    if report.divergence_details:
        print()
        print("OUTPUT DIVERGENCE WARNINGS:")
        print("=" * 60)
        for label, warnings in report.divergence_details:
            print(f"\n{label}")
            for w in warnings:
                print(w)
        print()

    # Summary
    print(
        f"{report.passed}/{report.total} passed, {report.parity_failures} parity failures,"
        f" {report.output_divergences} output divergence warnings"
    )

    return report


def _run_single_mode(cases: list[tuple[str, dict]], target: str, verbose: bool) -> int:
    """Run all cases against a single target. Returns exit code."""
    passed = 0
    failed = 0
    failures = []

    for filename, case in cases:
        name = case["name"]
        label = f"{filename}: {name}"

        # Skip cases that declare a target restriction excluding this target.
        targets = case.get("targets")
        if targets is not None and target not in targets:
            continue

        if verbose:
            print(f"  running: {label} ...", end=" ", flush=True)

        ok, errors, _result = _run_case(case, target)

        if ok:
            passed += 1
            if verbose:
                print("PASS")
        else:
            failed += 1
            failures.append((label, errors))
            if verbose:
                print("FAIL")

    # Print failures
    if failures:
        print()
        print("FAILURES:")
        print("=" * 60)
        for label, errors in failures:
            print(f"\n{label}")
            for e in errors:
                print(e)
        print()

    # Summary
    total = passed + failed
    print(f"{passed}/{total} passed, {failed} failed")

    return 0 if failed == 0 else 1


def main() -> None:
    parser = argparse.ArgumentParser(description="Run strictcli conformance tests")
    parser.add_argument(
        "--target",
        default=None,
        choices=list(TARGETS),
        help="Which implementation to test",
    )
    parser.add_argument(
        "--both",
        action="store_true",
        help="Test all registered targets, comparing results for parity",
    )
    parser.add_argument(
        "--filter",
        default=None,
        help="Only run cases whose name contains this substring",
    )
    parser.add_argument(
        "--verbose", "-v",
        action="store_true",
        help="Print each test case name as it runs",
    )
    args = parser.parse_args()

    # Validate: exactly one of --target or --both
    if args.both and args.target is not None:
        print("error: --both and --target are mutually exclusive", file=sys.stderr)
        sys.exit(2)
    if not args.both and args.target is None:
        print("error: one of --target or --both is required", file=sys.stderr)
        sys.exit(2)

    cases = _load_cases()
    if not cases:
        print("No test cases found!")
        sys.exit(1)

    if args.filter:
        cases = [(f, c) for f, c in cases if args.filter in c["name"]]
        if not cases:
            print(f"No test cases match filter: {args.filter!r}")
            sys.exit(1)

    if args.both:
        report = _run_parity_mode(cases, list(TARGETS), args.verbose)
        exit_code = report.exit_code
    else:
        exit_code = _run_single_mode(cases, args.target, args.verbose)

    sys.exit(exit_code)


if __name__ == "__main__":
    main()
