#!/usr/bin/env python3
"""Rewrite case expectations for the --json document's interface_version 3.

Effects contract §19.2's amendment for §18.37 item 338: the document gains an
`output` member, never absent, after `payload`, and `interface_version`
becomes 3. Every case expectation that pins a version-2 document is rewritten:

- a `stdout_equals` / `stderr_equals` line that is a whole version-2 document
  gets `interface_version` 3 and `"output": null` after `payload`. The line is
  re-serialized only when re-serializing it unmodified reproduces its bytes, so
  the rewrite cannot change anything else about the document;
- a `stdout_matches` pattern pinning the version-2 prefix gets the version-3
  prefix.

Anything else mentioning `"interface_version":2` is reported and left for a
hand edit. With --dry-run nothing is written and every change is printed.

Usage: scripts/envelope_interface_version_3.py [--dry-run] [cases/*.json ...]
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

CASES = Path(__file__).resolve().parent.parent / "cases"
OLD_PREFIX = '{"interface_version":2,'
OLD_PATTERN = '^\\{"interface_version":2,'
NEW_PATTERN = '^\\{"interface_version":3,'


def compact(obj: object) -> str:
    return json.dumps(obj, separators=(",", ":"), ensure_ascii=False)


def rewrite_document_line(line: str) -> str | None:
    """The version-3 form of one whole version-2 document line, or None."""
    if not line.startswith(OLD_PREFIX):
        return None
    doc = json.loads(line)
    if compact(doc) != line:
        raise ValueError(f"line does not round-trip, edit by hand: {line[:120]}")
    out: dict = {}
    for key, value in doc.items():
        out[key] = 3 if key == "interface_version" else value
        if key == "payload":
            out["output"] = None
    if "output" not in out:
        raise ValueError(f"document has no payload member: {line[:120]}")
    return compact(out)


def rewrite_text(text: str) -> str:
    lines = text.split("\n")
    changed = False
    for i, line in enumerate(lines):
        new = rewrite_document_line(line)
        if new is not None:
            lines[i] = new
            changed = True
    return "\n".join(lines) if changed else text


def rewrite_case(case: dict) -> list[str]:
    """Rewrite one case in place; return a description of each change."""
    changes: list[str] = []
    expect = case.get("expect", {})
    for key in ("stdout_equals", "stderr_equals"):
        value = expect.get(key)
        if isinstance(value, str):
            new = rewrite_text(value)
            if new != value:
                expect[key] = new
                changes.append(f"{key}: version 3, output null")
    value = expect.get("stdout_matches")
    if isinstance(value, str) and value.startswith(OLD_PATTERN):
        expect["stdout_matches"] = NEW_PATTERN + value[len(OLD_PATTERN):]
        changes.append("stdout_matches: version-3 prefix")
    leftover = compact(expect)
    if '\\"interface_version\\":2' in leftover or '"interface_version":2' in leftover:
        changes.append("LEFTOVER: a version-2 mention this script does not rewrite")
    return changes


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("files", nargs="*", type=Path)
    args = ap.parse_args()
    files = args.files or sorted(CASES.glob("*.json"))
    total = 0
    leftovers = 0
    for path in files:
        text = path.read_text(encoding="utf-8")
        cases = json.loads(text)
        # A case file is written with or without ASCII escaping; the rewrite
        # keeps whichever layout the file already has.
        ascii_layout = None
        for candidate in (False, True):
            if json.dumps(cases, indent=2, ensure_ascii=candidate) + "\n" == text:
                ascii_layout = candidate
                break
        if ascii_layout is None:
            if 'interface_version\\":2' in text:
                print(f"{path.name}: LEFTOVER: not in a known layout, edit by hand")
                leftovers += 1
            continue
        file_changes = 0
        for case in cases:
            for change in rewrite_case(case):
                print(f"{path.name}: {case['name']}: {change}")
                if change.startswith("LEFTOVER"):
                    leftovers += 1
                else:
                    file_changes += 1
        total += file_changes
        if file_changes and not args.dry_run:
            path.write_text(json.dumps(cases, indent=2, ensure_ascii=ascii_layout) + "\n", encoding="utf-8")
    print(f"{total} change(s){' (dry run)' if args.dry_run else ''}, {leftovers} leftover(s)")
    return 1 if leftovers else 0


if __name__ == "__main__":
    sys.exit(main())
