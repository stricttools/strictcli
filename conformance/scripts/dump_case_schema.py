#!/usr/bin/env python3
"""Dump one case's app definition through every target and compare the bytes.

`schema_bytes_equal` cases pin the WHOLE help document `help --json` prints
(effects contract §25.8), apart from the project_id line, which names each
target's own program. The only honest way to author one is to run the three
targets and see that they agree. This does exactly that:

    python scripts/dump_case_schema.py cases/schema_v2.json "<case name>"

It prints the shared bytes (without project_id) when all three agree, and the
per-target diff when they do not. With `--write` it splices the shared bytes
back into the case's `expect.schema_bytes_equal`.
"""
from __future__ import annotations

import argparse
import json
import os
import pathlib
import subprocess
import sys
import tempfile

CONFORMANCE = pathlib.Path(__file__).resolve().parent.parent
sys.path.insert(0, str(CONFORMANCE))

import ref_python  # noqa: E402
import run as conformance_run  # noqa: E402


def dump(target: str, app_def: dict, workdir: pathlib.Path) -> str:
    """Run one target with `help --json` and return the document it printed."""
    env = dict(os.environ)
    if target == "python":
        # The script lives in the conformance directory (ignored by
        # `tmp*.py`), so the help document's project_id is the conformance
        # project's, as run.py arranges for every Python case.
        script = CONFORMANCE / f"tmp_dump_case_schema_{os.getpid()}.py"
        script.write_text(ref_python.generate(app_def).replace(
            "sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'python'))",
            f"sys.path.insert(0, {str(CONFORMANCE.parent / 'python')!r})",
        ))
        cmd = [sys.executable, str(script), "help", "--json"]
    else:
        script = None
        defpath = workdir / "appdef.json"
        defpath.write_text(json.dumps(app_def))
        env["CONFORMANCE_APP_DEF"] = str(defpath)
        if target == "go":
            cmd = [str(CONFORMANCE / "harness" / "harness"), "help", "--json"]
        else:
            cmd = ["node", str(CONFORMANCE / "harness_ts" / "main.js"), "help", "--json"]
    try:
        proc = subprocess.run(
            cmd, cwd=workdir, env=env, capture_output=True, text=True, timeout=60,
        )
    finally:
        if script is not None:
            script.unlink()
    if proc.returncode != 0:
        raise SystemExit(
            f"{target}: help --json failed\nstdout: {proc.stdout}\nstderr: {proc.stderr}"
        )
    return conformance_run._without_project_id(proc.stdout)[0]


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("case_file")
    ap.add_argument("case_name")
    ap.add_argument("--write", action="store_true",
                    help="splice the shared bytes into expect.schema_bytes_equal")
    args = ap.parse_args()

    path = pathlib.Path(args.case_file)
    cases = json.loads(path.read_text())
    case = next((c for c in cases if c["name"] == args.case_name), None)
    if case is None:
        raise SystemExit(f"no case named {args.case_name!r} in {path}")

    dumps = {}
    with tempfile.TemporaryDirectory(dir=CONFORMANCE / "_scratch") as tmp:
        for target in ("python", "go", "typescript"):
            dumps[target] = dump(target, case["app"], pathlib.Path(tmp))

    texts = set(dumps.values())
    if len(texts) != 1:
        print("targets DISAGREE:")
        base = dumps["python"].split("\n")
        for target in ("go", "typescript"):
            other = dumps[target].split("\n")
            for i in range(max(len(base), len(other))):
                b = base[i] if i < len(base) else "<missing>"
                o = other[i] if i < len(other) else "<missing>"
                if b != o:
                    print(f"  python vs {target}: line {i + 1}")
                    print(f"    python: {b!r}")
                    print(f"    {target}: {o!r}")
                    break
            else:
                print(f"  python vs {target}: identical")
        return 1

    shared = texts.pop()
    if args.write:
        case["expect"]["schema_bytes_equal"] = shared
        path.write_text(json.dumps(cases, indent=2, ensure_ascii=False) + "\n")
        print(f"wrote {len(shared)} bytes into {path.name} :: {args.case_name}")
    else:
        print(shared)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
