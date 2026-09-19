#!/usr/bin/env python3
"""The observational-only sweeps for the process trace store.

Effects contract §20.2 makes observational-only a ratified contract item and
names the enforcement: two sweeps that assert **byte-identical stdout, stderr
and exit code** against the same runs without them.

- The **forged-ID sweep** runs with `STRICTCLI_TRACE_PARENT` set to an
  identifier no store ever minted -- once syntactically valid under the strict
  profile, once outright garbage. A forged ancestry is a false attribution
  claim, never an input to behaviour, and this is what proves it.
- The **broken-store sweep** runs with the store unwritable -- once because the
  directory itself cannot be written to, once because the store path is a
  regular file, so directory creation fails at a different point.

A baseline run of the same case, with the variable unset and a healthy empty
store, is the comparand. Any difference on any of the three streams is a
failure: nothing the framework does may vary with the store's state or with
what the environment claims about ancestry.

The case set is declared below, by name, and every declared name must resolve
-- a renamed case fails the sweep loudly instead of silently covering nothing.
It is deliberately small and deliberately covers the seam from both sides: runs
and observes that really start children, previews that record instead, and
ordinary output paths that never touch the store at all.

Usage:
    python conformance/sweep_trace.py               # every registered target
    python conformance/sweep_trace.py --target go
"""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import run  # noqa: E402  (path setup must precede the import)

# A syntactically valid identifier under the strict profile that no store ever
# minted, and a value that is not an identifier at all.
FORGED_VALID = "01JZ8X4M6N7QK2WVBD3F5RTYAC"
FORGED_GARBAGE = "not a ulid at all -- ../../etc/passwd"

TRACE_PARENT_ENV = "STRICTCLI_TRACE_PARENT"
PARTITION_RE = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}\.jsonl$")

# Every declared name must exist. The set covers, in order: a live run that
# starts a real child; an allowlisted observe that starts one in a read_only
# command; an observe that starts one *in dry mode* (the only way an entry's
# dry_run can be true); a refused run that starts nothing; a recorded spawn that
# starts nothing; a would-do log; a machine-mode envelope; and two ordinary
# output paths that never come near the seam.
SWEEP_CASES = [
    "effects_call_errors: a nonzero exit fails the run call",
    "effects_call_errors: a nonzero exit fails an allowlisted observe too",
    "effects_read_only: an allowlisted run is an observe and executes",
    "effects_read_only: an observe executes in dry mode and is never logged",
    "effects_read_only: a non-allowlisted run is refused in a read-only command",
    "effects_log: a spawn is an effect and is recorded, not performed",
    "effects_log: a live run records the effect it performed",
    "effects_dry_run_log: every verb renders in the pinned line format",
    "effects_output_gating: quiet does not suppress the machine payload",
    "envelope: a plain exit emits the envelope as the sole stdout document",
    "help: app help shows version and commands",
]


@dataclass
class Condition:
    """One sweep condition: a name, an environment overlay, and a store state."""

    name: str
    trace_parent: str | None
    store: str  # "healthy", "unwritable-dir" or "path-is-a-file"


CONDITIONS = [
    Condition("forged-valid-id", FORGED_VALID, "healthy"),
    Condition("forged-garbage-id", FORGED_GARBAGE, "healthy"),
    Condition("broken-store-unwritable", None, "unwritable-dir"),
    Condition("broken-store-path-is-a-file", None, "path-is-a-file"),
    # Both at once: a forged claim about a store that cannot be written.
    Condition("forged-id-and-broken-store", FORGED_VALID, "unwritable-dir"),
]


def _store_dir(home: str) -> str:
    return os.path.join(home, ".local", "share", "strictcli", "trace")


def _prepare_home(condition: Condition) -> str:
    """Build the throwaway HOME this condition runs under."""
    home = run.make_trace_home(prefix=f"strictcli_sweep_{condition.name}_")
    store = _store_dir(home)
    if condition.store == "unwritable-dir":
        os.makedirs(store, mode=0o700)
        os.chmod(store, 0o500)
    elif condition.store == "path-is-a-file":
        os.makedirs(os.path.dirname(store), mode=0o700, exist_ok=True)
        with open(store, "w", encoding="utf-8") as fh:
            fh.write("not a directory\n")
    return home


def _release_home(home: str) -> None:
    """Remove a condition's home now, rather than at interpreter exit.

    run.make_trace_home already registers the removal, so this is only about
    when it happens: a later condition must not see the previous one's store.
    """
    run.remove_trace_home(home)


def _entry_count(home: str) -> int:
    """Lines across every partition file. Non-partition files are ignored."""
    store = _store_dir(home)
    if not os.path.isdir(store):
        return 0
    total = 0
    for name in os.listdir(store):
        if not PARTITION_RE.match(name):
            continue
        with open(os.path.join(store, name), encoding="utf-8") as fh:
            total += sum(1 for line in fh if line.strip())
    return total


def _capture(case: dict, target: str) -> subprocess.CompletedProcess | None:
    """Run one case and return the raw process result."""
    _ok, _errors, result = run._run_case(case, target)
    return result


def _streams(result: subprocess.CompletedProcess | None) -> tuple:
    if result is None:
        return ("<no result>", "<no result>", None)
    return (result.stdout, result.stderr, result.returncode)


def _select_cases() -> list[dict]:
    by_name = {case["name"]: case for _file, case in run._load_cases()}
    missing = [name for name in SWEEP_CASES if name not in by_name]
    if missing:
        print("sweep case names that no longer resolve:", file=sys.stderr)
        for name in missing:
            print(f"  {name}", file=sys.stderr)
        sys.exit(1)
    return [by_name[name] for name in SWEEP_CASES]


def _run_target(target: str, cases: list[dict], verbose: bool) -> list[str]:
    failures: list[str] = []

    # Baseline: the variable unset, the store healthy and empty.
    baseline_home = run.make_trace_home(prefix="strictcli_sweep_baseline_")
    prior_parent = os.environ.pop(TRACE_PARENT_ENV, None)
    run.TRACE_HOME = baseline_home
    try:
        baselines = {case["name"]: _streams(_capture(case, target)) for case in cases}
        # A sweep over a seam that never fired proves nothing, so the baseline
        # store must actually have received entries.
        written = _entry_count(baseline_home)
    finally:
        _release_home(baseline_home)
    if written == 0:
        failures.append(
            f"{target}: the baseline store received no entries at all -- the "
            f"sweep case set no longer reaches the spawn seam"
        )

    for condition in CONDITIONS:
        home = _prepare_home(condition)
        run.TRACE_HOME = home
        if condition.trace_parent is None:
            os.environ.pop(TRACE_PARENT_ENV, None)
        else:
            os.environ[TRACE_PARENT_ENV] = condition.trace_parent
        try:
            for case in cases:
                got = _streams(_capture(case, target))
                want = baselines[case["name"]]
                if got == want:
                    if verbose:
                        print(f"  {target} / {condition.name} / {case['name']}: same")
                    continue
                labels = ("stdout", "stderr", "exit_code")
                for label, a, b in zip(labels, got, want):
                    if a != b:
                        failures.append(
                            f"{target} / {condition.name} / {case['name']}: "
                            f"{label} differs from the baseline\n"
                            f"    baseline: {b!r}\n"
                            f"    swept:    {a!r}"
                        )
        finally:
            _release_home(home)
            os.environ.pop(TRACE_PARENT_ENV, None)

    if prior_parent is not None:
        os.environ[TRACE_PARENT_ENV] = prior_parent
    return failures


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Trace-store observational-only sweeps"
    )
    parser.add_argument(
        "--target", default=None, choices=list(run.TARGETS),
        help="Sweep one implementation (default: every registered target)",
    )
    parser.add_argument("--verbose", "-v", action="store_true")
    args = parser.parse_args()

    cases = _select_cases()
    targets = [args.target] if args.target else list(run.TARGETS)

    failures: list[str] = []
    for target in targets:
        applicable = [
            case for case in cases
            if case.get("targets") is None or target in case["targets"]
        ]
        failures.extend(_run_target(target, applicable, args.verbose))

    runs = len(targets) * len(cases) * (len(CONDITIONS) + 1)
    if failures:
        print("SWEEP FAILURES:")
        print("=" * 60)
        for failure in failures:
            print(failure)
        print()
        print(f"{len(failures)} difference(s) across {runs} runs")
        sys.exit(1)
    print(
        f"{runs} runs across {len(targets)} target(s), "
        f"{len(CONDITIONS)} condition(s) and {len(cases)} case(s): "
        f"byte-identical to the baseline"
    )


if __name__ == "__main__":
    main()
