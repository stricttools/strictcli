#!/usr/bin/env python3
"""Fragment validity for the dumped schema (effects contract §25.12).

Every `value_schema` in every dump must be a valid document of the closed
four-keyword subset, and the check that proves it is **Python-side and
singular**: one check reading all three targets' dumps, not three
implementations each asserting about themselves.

What it asserts, over every `value_schema` in every dump -- flag entries, arg
entries, global flags, config fields, and every scoped entry at every depth
inside a selector's `choices`:

1. the fragment validates under the **in-house payload-schema validator**
   (`_validate_payload_schema`, the registration-time validator §19.5 already
   owns), which is sound only because the fragment subset is a strict subset of
   the payload subset;
2. it uses **only** the four keywords -- `type`, `items`,
   `additionalProperties`, `enum` -- which is narrower than the payload
   validator's own closure and is therefore this check's own assertion, along
   with the JSON Schema type names §25.2 pins;
3. every entry that must carry a fragment does, **and a selector entry carries
   none** -- the same shape of assertion the parity checker's `presence` walk
   added, for the same reason: an agreed-upon absence must never read as
   agreement.

The 2^53 registration rule is what makes strict validation sound. The payload
validator scans every `enum` member with the magnitude guard, so an int choice
above 2^53 would produce a fragment the framework's own validator REJECTS --
the framework emitting a document it refuses to accept. §12.14's registration
error is the closure of that gap, and this check is what would discover it if
the error were ever removed.

Exit 0 when every fragment in every dump is valid, exit 1 with a report
otherwise.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

CONFORMANCE_DIR = Path(__file__).resolve().parent
PROJECT_ROOT = CONFORMANCE_DIR.parent
sys.path.insert(0, str(CONFORMANCE_DIR))
sys.path.insert(0, str(PROJECT_ROOT / "python"))

# The dumps come from check_schema_parity's own runners, so the two checks read
# exactly the same documents -- a fragment this check blesses is a fragment
# that checker compared byte-for-byte.
import check_schema_parity as parity  # noqa: E402

import strictcli  # noqa: E402

# The registration-time validator §19.5 owns. Reaching for the private name is
# deliberate: the point of §25.12 is that the fragment subset is a STRICT
# SUBSET of the payload subset, which is only demonstrable by running the very
# validator the framework applies to a declared payload schema.
_validate_payload_schema = strictcli._validate_payload_schema

# ---------------------------------------------------------------------------
# The closed subset (§25.2)
# ---------------------------------------------------------------------------

FRAGMENT_KEYWORDS = ("type", "items", "additionalProperties", "enum")
FRAGMENT_TYPES = (
    "string", "boolean", "integer", "number", "array", "object",
)
# Keys inside a fragment are emitted in this order, which covers every row of
# §25.2's table without a second rule.
KEYWORD_ORDER = list(FRAGMENT_KEYWORDS)


def _check_fragment(fragment: object, where: str) -> list[str]:
    """Assertions 1 and 2, over one fragment and everything nested in it."""
    problems: list[str] = []
    if not isinstance(fragment, dict):
        return [f"{where}: value_schema is {type(fragment).__name__}, not an object"]

    try:
        _validate_payload_schema(fragment)
    except Exception as e:  # noqa: BLE001 - the validator's own refusal is the finding
        problems.append(f"{where}: the payload-schema validator refuses it: {e}")

    def walk(node: object, path: str) -> None:
        if not isinstance(node, dict):
            problems.append(f"{path}: expected an object, got {type(node).__name__}")
            return
        for key in node:
            if key not in FRAGMENT_KEYWORDS:
                problems.append(
                    f"{path}: keyword {key!r} is outside the closed subset "
                    f"({', '.join(FRAGMENT_KEYWORDS)})"
                )
        seen = [k for k in node if k in FRAGMENT_KEYWORDS]
        if seen != [k for k in KEYWORD_ORDER if k in seen]:
            problems.append(
                f"{path}: keys are emitted as {seen}, not in the declared order "
                f"{[k for k in KEYWORD_ORDER if k in seen]}"
            )
        t = node.get("type")
        if t is None:
            problems.append(f"{path}: no `type`; every fragment names one")
        elif not isinstance(t, str) or t not in FRAGMENT_TYPES:
            problems.append(
                f"{path}: type {t!r} is not a JSON Schema type name "
                f"({', '.join(FRAGMENT_TYPES)})"
            )
        if "items" in node:
            if t != "array":
                problems.append(f"{path}: `items` on a non-array fragment")
            walk(node["items"], f"{path}.items")
        if "additionalProperties" in node:
            if t != "object":
                problems.append(f"{path}: `additionalProperties` on a non-object fragment")
            walk(node["additionalProperties"], f"{path}.additionalProperties")
        if "enum" in node:
            values = node["enum"]
            if not isinstance(values, list) or not values:
                problems.append(f"{path}: `enum` must be a non-empty array")
            elif t == "array":
                problems.append(
                    f"{path}: `enum` sits at the root of an array fragment; "
                    f"it belongs inside `items`, describing the element (§25.13)"
                )

    walk(fragment, where)
    return problems


# ---------------------------------------------------------------------------
# The walk over a whole dumped document (§25.12's third assertion)
# ---------------------------------------------------------------------------

def _check_entry(entry: dict, where: str, *, kind: str) -> list[str]:
    """Assertion 3 for one flag/arg/config-field entry, plus its fragment.

    `elect_by` is the discriminator (§25.6): an entry carrying it is a selector,
    which has NO fragment and whose `choices` are choice objects; an entry
    without it is an ordinary declaration and must carry one.
    """
    problems: list[str] = []
    is_selector = "elect_by" in entry

    if is_selector:
        if kind != "flag":
            problems.append(f"{where}: `elect_by` on a {kind} entry; only a flag elects")
        if "value_schema" in entry:
            problems.append(
                f"{where}: a selector carries a value_schema; a variant is "
                f"inexpressible in the closed subset and its ABSENCE is the "
                f"declaration (§25.6)"
            )
        if entry["elect_by"] not in ("selector-token", "member-flags"):
            problems.append(
                f"{where}: elect_by is {entry['elect_by']!r}, not one of "
                f"'selector-token' / 'member-flags'"
            )
        for i, choice in enumerate(entry.get("choices", [])):
            cwhere = f"{where}.choices[{i}]"
            if not isinstance(choice, dict) or "name" not in choice:
                problems.append(f"{cwhere}: a selector's choices are choice objects")
                continue
            for j, scoped in enumerate(choice.get("flags", [])):
                problems.extend(
                    _check_entry(scoped, f"{cwhere}.flags[{j}]", kind="flag")
                )
        return problems

    if "value_schema" not in entry:
        problems.append(
            f"{where}: no value_schema; every {kind} entry that is not a "
            f"selector carries one (§25.2)"
        )
        return problems
    problems.extend(_check_fragment(entry["value_schema"], f"{where}.value_schema"))
    return problems


def _check_command(cmd: dict, where: str) -> list[str]:
    problems: list[str] = []
    for i, flag in enumerate(cmd.get("flags", [])):
        problems.extend(_check_entry(flag, f"{where}.flags[{i}]", kind="flag"))
    for i, arg in enumerate(cmd.get("args", [])):
        problems.extend(_check_entry(arg, f"{where}.args[{i}]", kind="arg"))
    return problems


def _check_group(group: dict, where: str) -> list[str]:
    problems: list[str] = []
    for name, cmd in (group.get("commands") or {}).items():
        problems.extend(_check_command(cmd, f"{where}.commands[{name!r}]"))
    for name, sub in (group.get("groups") or {}).items():
        problems.extend(_check_group(sub, f"{where}.groups[{name!r}]"))
    return problems


def check_document(schema: dict) -> list[str]:
    """Every fragment-bearing site in one dumped document."""
    problems: list[str] = []
    for i, flag in enumerate(schema.get("global_flags", [])):
        problems.extend(_check_entry(flag, f"$.global_flags[{i}]", kind="flag"))
    for name, cmd in (schema.get("commands") or {}).items():
        problems.extend(_check_command(cmd, f"$.commands[{name!r}]"))
    for name, group in (schema.get("groups") or {}).items():
        problems.extend(_check_group(group, f"$.groups[{name!r}]"))
    for name, field in (schema.get("config_fields") or {}).items():
        problems.extend(
            _check_entry(field, f"$.config_fields[{name!r}]", kind="config field")
        )
    return problems


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> int:
    print("Building Go harness...", flush=True)
    try:
        harness = parity._build_harness()
    except RuntimeError as e:
        print(f"FAILED: {e}", file=sys.stderr)
        return 1

    print("Building TypeScript dist...", flush=True)
    try:
        ts_entry = parity._build_ts_harness()
    except RuntimeError as e:
        print(f"FAILED: {e}", file=sys.stderr)
        return 1

    app_defs = [
        ("rich app", parity.RICH_APP),
        ("minimal app", parity.MINIMAL_APP),
        ("config fields app", parity.CONFIG_APP),
    ]

    all_problems: list[tuple[str, str, list[str]]] = []
    checked = 0

    for label, app_def in app_defs:
        print(f"Checking {label}...", flush=True)
        for target in parity.TARGET_NAMES:
            try:
                text = parity._run_dump_schema(
                    app_def, target, harness_binary=harness, ts_entry=ts_entry
                )
            except RuntimeError as e:
                all_problems.append((label, target, [f"help --json failed: {e}"]))
                continue
            problems = check_document(json.loads(text))
            checked += 1
            if problems:
                all_problems.append((label, target, problems))
        print(f"  {label}: {'PASS' if not all_problems else 'see report'}", flush=True)

    harness_path = parity.HARNESS_DIR / "harness"
    if harness_path.exists():
        harness_path.unlink()

    if all_problems:
        total = sum(len(p) for _, _, p in all_problems)
        print()
        print(f"Schema fragment check FAILED ({total} problem(s)):")
        print("=" * 60)
        for label, target, problems in all_problems:
            print(f"\n{label} / {target}:")
            for p in problems:
                print(f"  - {p}")
        return 1

    print()
    print("Schema fragment check passed.")
    print(f"  Documents checked: {checked}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
