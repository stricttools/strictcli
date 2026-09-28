"""Conformance validation tool for strictcli -- dogfoods the check system."""

import json
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

from strictcli import App, ErrorReporter

TOOL_DIR = Path(__file__).resolve().parent
CONFORMANCE_DIR = TOOL_DIR.parent
PROJECT_ROOT = CONFORMANCE_DIR.parent

# Canonical committed schema location. `conformance help --json` prints the
# help document, whose project_id is the project holding this tool's code
# ("strictcli-conformance"); the committed copy is that document redirected
# into this file.
SCHEMA_PATH = CONFORMANCE_DIR / ".strictcli" / "schema.json"


@dataclass
class ConformanceContext:
    project_root: Path


app = App(
    name="conformance",
    version=(CONFORMANCE_DIR / "VERSION").read_text().strip(),
    help="Conformance validation tool for strictcli",
    checks_path=TOOL_DIR / ".strictcli" / "checks.toml",
)

app.set_check_context(lambda: ConformanceContext(project_root=PROJECT_ROOT))


def _run_argv(reporter: ErrorReporter, label: str, argv: list[str]):
    """Run a subprocess from CONFORMANCE_DIR and mint an outcome via the reporter.

    Exit 0 mints a terminal pass; a non-zero exit mints error-severity problems
    (the process's stdout/stderr) and a terminal ``found`` outcome, which derives
    to FAIL. All conformance checks are error-severity, so a failure gates.
    """
    result = subprocess.run(
        argv,
        capture_output=True,
        text=True,
        cwd=str(CONFORMANCE_DIR),
    )
    if result.returncode == 0:
        return reporter.passed(f"{label} passed")
    problems = []
    if result.stdout.strip():
        problems.append(result.stdout.strip())
    if result.stderr.strip():
        problems.append(result.stderr.strip())
    if not problems:
        problems.append(f"exited with code {result.returncode} and no output")
    for text in problems:
        reporter.error(text)
    return reporter.found(f"{label} failed (exit code {result.returncode})")


def _run_script(reporter: ErrorReporter, script: str, *args: str):
    """Run a conformance script under this interpreter."""
    script_path = CONFORMANCE_DIR / script
    return _run_argv(
        reporter, script, [sys.executable, str(script_path), *args]
    )


# The meta-tests that guard the checkers themselves. They pin invariants of the
# extraction surfaces (which templates the parity extractor can see, which TS
# names the api-surface registry points at) and of the run.py case registry --
# the failure modes that produce a false PASS rather than a visible error.
# `--frozen` keeps the gate from ever rewriting uv.lock mid-release.
META_TEST_FILES = [
    "test_error_parity_extraction.py",
    "test_run_registry.py",
    "test_api_surface_registry.py",
    "test_lock_pin.py",
]


@app.error_check("api-surface")
def check_api_surface(ctx, reporter):
    return _run_script(reporter, "check_api_surface.py")


@app.error_check("error-parity")
def check_error_parity(ctx, reporter):
    return _run_script(reporter, "check_error_parity.py")


@app.error_check("conformance-meta")
def check_conformance_meta(ctx, reporter):
    return _run_argv(
        reporter,
        "conformance meta-tests",
        ["uv", "run", "--frozen", "pytest", "-q", *META_TEST_FILES],
    )


@app.error_check("conformance-python")
def check_conformance_python(ctx, reporter):
    return _run_script(reporter, "run.py", "--target", "python")


@app.error_check("conformance-go")
def check_conformance_go(ctx, reporter):
    return _run_script(reporter, "run.py", "--target", "go")


@app.error_check("conformance-typescript")
def check_conformance_typescript(ctx, reporter):
    return _run_script(reporter, "run.py", "--target", "typescript")


@app.error_check("conformance-parity")
def check_conformance_parity(ctx, reporter):
    return _run_script(reporter, "run.py", "--both")


@app.error_check("schema-parity")
def check_schema_parity(ctx, reporter):
    return _run_script(reporter, "check_schema_parity.py")


@app.error_check("schema-fragments")
def check_schema_fragments(ctx, reporter):
    """Every value_schema in every dump is a valid closed-subset fragment.

    Effects contract §25.12: the check is Python-side and SINGULAR -- one check
    reading all three targets' dumps, not three implementations each asserting
    about themselves. It also asserts the absence a selector entry declares: an
    agreed-upon absence must never read as agreement.
    """
    return _run_script(reporter, "check_schema_fragments.py")


@app.error_check("float-fuzz")
def check_float_fuzz(ctx, reporter):
    return _run_script(reporter, "check_float_fuzz.py")


@app.error_check("trace-sweeps")
def check_trace_sweeps(ctx, reporter):
    """The trace store's observational-only sweeps (effects contract §20.2).

    A forged ancestry identifier and an unwritable store must both leave
    stdout, stderr and the exit code byte-identical to the same runs without
    them. This is the entire spoofing defense, and it is what makes the store
    observational rather than an input to behaviour.
    """
    return _run_script(reporter, "sweep_trace.py")


@app.error_check("schema-freshness")
def check_schema_freshness(ctx, reporter):
    """Fail when the committed schema no longer matches the tool's own schema.

    This tool is a dev_node: it is never released, so rlsbl's release-time
    schema auto-regen never fires for it. The check gate (run by CI on every
    relevant push) is therefore the only freshness trigger. Comparison uses the
    registration-time snapshot (id-free) against the parsed committed file with
    ``project_id`` excluded, so key order is irrelevant but list order (which is
    semantic) still counts.

    The snapshot, not a live ``dump_schema_dict()`` call, is what makes the
    comparison like-for-like. By the time this check body runs, the check
    command has materialized the registered check providers, which inserts
    their specs (strictcli's own built-in ``effects-bypass``, among any others)
    into the registry the schema's ``checks`` section serializes. A
    ``help --json`` run never materializes providers -- deliberately, because
    a provider's result depends on the cwd and the schema must not -- so a live
    call here would compare a post-materialization registry against a
    pre-materialization file and report every committed schema as stale.
    """
    current = _REGISTRATION_TIME_SCHEMA
    remediation = (
        f"Regenerate with `conformance help --json > .strictcli/schema.json` "
        f"run from {CONFORMANCE_DIR}."
    )
    if not SCHEMA_PATH.exists():
        reporter.error(f"Committed schema {SCHEMA_PATH} is missing. {remediation}")
        return reporter.found("schema-freshness failed (schema file missing)")
    try:
        committed = json.loads(SCHEMA_PATH.read_text())
    except (OSError, ValueError) as exc:
        reporter.error(
            f"Committed schema {SCHEMA_PATH} is unreadable ({exc}). {remediation}"
        )
        return reporter.found("schema-freshness failed (schema file unreadable)")
    committed.pop("project_id", None)
    if committed != current:
        reporter.error(
            f"Committed schema {SCHEMA_PATH} is stale (does not match the tool's "
            f"current in-memory schema). {remediation}"
        )
        return reporter.found("schema-freshness failed (committed schema is stale)")
    return reporter.passed("committed schema matches the current in-memory schema")


# Captured after every check is registered and before anything can run, so it is
# exactly what `conformance help --json` prints: no project_id (the accessor
# is id-free) and no provider-sourced checks (nothing has materialized them
# yet). See check_schema_freshness for why a live call would not do.
_REGISTRATION_TIME_SCHEMA = app.dump_schema_dict()


def main():
    app.run()
