#!/usr/bin/env python3
"""Unit tests for the N-way target registry in run.py.

These exercise the target-agnostic parity machinery WITHOUT shelling out to the
real python/go targets: a fake in-process target is registered into the registry
and `_run_case` is stubbed to return synthetic outcomes. This proves that adding
a target is purely a registry operation and that N-way odd-one-out reporting
works for more than two targets.

Runnable under pytest (auto-discovered) or standalone (`python3 test_run_registry.py`).
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from contextlib import contextmanager
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import run


def _proc(stdout: str = "", stderr: str = "", returncode: int = 0):
    """Build a synthetic CompletedProcess for comparison tests."""
    return subprocess.CompletedProcess(
        args=[], returncode=returncode, stdout=stdout, stderr=stderr
    )


@contextmanager
def _fake_target(name: str):
    """Register a throwaway target into the registry, then remove it."""
    def _prepare(app_def, case_argv):  # pragma: no cover - never called (stubbed)
        raise AssertionError("fake target prepare should be stubbed via _run_case")

    def _write(d, app_name):  # pragma: no cover
        pass

    run._register_target(run.Target(name, _prepare, _write))
    try:
        yield
    finally:
        run.TARGETS.pop(name, None)


@contextmanager
def _stub_run_case(outcomes: dict[str, tuple[bool, list, object]]):
    """Stub run._run_case to return per-target synthetic outcomes.

    `outcomes` maps target name -> (ok, errors, CompletedProcess|None).
    """
    original = run._run_case

    def _fake(case, target):
        return outcomes[target]

    run._run_case = _fake
    try:
        yield
    finally:
        run._run_case = original


# --- Registry basics ---------------------------------------------------------


def test_default_registry_has_python_and_go():
    assert set(run.TARGETS) >= {"python", "go"}
    assert isinstance(run.TARGETS["python"], run.Target)
    assert isinstance(run.TARGETS["go"], run.Target)


def test_register_and_unregister_target():
    assert "ts" not in run.TARGETS
    with _fake_target("ts"):
        assert "ts" in run.TARGETS
        assert list(run.TARGETS)[-1] == "ts"  # insertion order preserved
    assert "ts" not in run.TARGETS


# --- N-way output comparison -------------------------------------------------


def test_compare_all_agree_no_warnings():
    results = {
        "python": _proc(stdout="hello"),
        "go": _proc(stdout="hello"),
        "ts": _proc(stdout="hello"),
    }
    assert run._compare_outputs(results) == []


def test_compare_three_way_odd_one_out():
    # python & go agree; ts is the odd one out on stdout.
    results = {
        "python": _proc(stdout="hello"),
        "go": _proc(stdout="hello"),
        "ts": _proc(stdout="HELLO"),
    }
    warnings = run._compare_outputs(results)
    text = "\n".join(warnings)
    assert "stdout divergence (odd one out: ts):" in text
    assert "go,python: 'hello'" in text
    assert "ts: 'HELLO'" in text
    # stderr all empty -> no stderr divergence line
    assert "stderr divergence" not in text


def test_compare_two_way_no_majority_reports_both_labeled():
    results = {
        "python": _proc(stderr="py-error"),
        "go": _proc(stderr="go-error"),
    }
    warnings = run._compare_outputs(results)
    text = "\n".join(warnings)
    # No unique majority with two distinct targets -> no odd-one-out marker.
    assert "stderr divergence:" in text
    assert "odd one out" not in text
    assert "go: 'go-error'" in text
    assert "python: 'py-error'" in text


def test_compare_fewer_than_two_present_is_noop():
    assert run._compare_outputs({"python": _proc(stdout="x"), "go": None}) == []


# --- Acknowledged divergence --------------------------------------------------


def _ack(streams: dict[str, list[str]]):
    return {"reason": "test reason", "streams": streams}


def test_acknowledged_target_excluded_from_stream_comparison():
    # go's stderr genuinely differs but is acknowledged; python & ts still match.
    results = {
        "python": _proc(stderr="shared error"),
        "go": _proc(stderr="go-specific error"),
        "ts": _proc(stderr="shared error"),
    }
    assert run._compare_outputs(results, _ack({"stderr": ["go"]})) == []


def test_acknowledgment_is_per_stream_not_global():
    # go acknowledged on stderr only; its stdout divergence still warns.
    results = {
        "python": _proc(stdout="hello", stderr="shared"),
        "go": _proc(stdout="HELLO", stderr="go-specific"),
        "ts": _proc(stdout="hello", stderr="shared"),
    }
    warnings = run._compare_outputs(results, _ack({"stderr": ["go"]}))
    text = "\n".join(warnings)
    assert "stdout divergence (odd one out: go):" in text
    assert "stderr" not in text


def test_unacknowledged_targets_still_compared():
    # go acknowledged, but python & ts diverge from each other -> warn.
    results = {
        "python": _proc(stderr="py error"),
        "go": _proc(stderr="go error"),
        "ts": _proc(stderr="ts error"),
    }
    warnings = run._compare_outputs(results, _ack({"stderr": ["go"]}))
    text = "\n".join(warnings)
    assert "stderr divergence" in text
    assert "py error" in text and "ts error" in text
    assert "go error" not in text


def test_stale_acknowledgment_is_reported():
    # Acknowledged target's output matches every other target -> stale.
    results = {
        "python": _proc(stderr="same"),
        "go": _proc(stderr="same"),
        "ts": _proc(stderr="same"),
    }
    warnings = run._compare_outputs(results, _ack({"stderr": ["go"]}))
    text = "\n".join(warnings)
    assert "stale acknowledged divergence" in text
    assert "'go'" in text


def test_validate_acknowledgment_rejects_inapplicable_target():
    case = {
        "name": "x",
        "targets": ["python", "go"],
        "acknowledged_divergence": _ack({"stderr": ["ts"]}),
    }
    errors = run._validate_acknowledged_divergence(case, ["python", "go"])
    assert len(errors) == 1
    assert "not applicable" in errors[0]


def test_validate_acknowledgment_requires_a_baseline_target():
    case = {
        "name": "x",
        "acknowledged_divergence": _ack({"stderr": ["python", "go"]}),
    }
    errors = run._validate_acknowledged_divergence(case, ["python", "go"])
    assert len(errors) == 1
    assert "baseline" in errors[0]


def test_validate_acknowledgment_absent_is_noop():
    assert run._validate_acknowledged_divergence({"name": "x"}, ["python", "go"]) == []


# --- Target scoping ----------------------------------------------------------


def test_applicable_targets_intersection():
    names = ["python", "go", "ts"]
    assert run._applicable_targets({}, names) == ["python", "go", "ts"]
    assert run._applicable_targets({"targets": ["python", "go"]}, names) == [
        "python",
        "go",
    ]
    assert run._applicable_targets({"targets": ["python"]}, names) == ["python"]


# --- N-way parity mode with a fake registered target -------------------------


def test_parity_mode_reports_divergence_with_fake_target():
    case = {"name": "synthetic", "targets": ["python", "go", "ts"]}
    cases = [("fake.json", case)]
    outcomes = {
        "python": (True, [], _proc(stdout="ok")),
        "go": (True, [], _proc(stdout="ok")),
        "ts": (True, [], _proc(stdout="DIFFERENT")),  # passes own assertions, diverges
    }
    with _fake_target("ts"), _stub_run_case(outcomes):
        report = run._run_parity_mode(cases, ["python", "go", "ts"], verbose=False)

    assert report.passed == 1
    assert report.parity_failures == 0
    assert report.output_divergences == 1
    label, warnings = report.divergence_details[0]
    assert "synthetic" in label
    assert "odd one out: ts" in "\n".join(warnings)


def test_parity_mode_flags_odd_target_failure_as_parity_break():
    case = {"name": "synthetic", "targets": ["python", "go", "ts"]}
    cases = [("fake.json", case)]
    outcomes = {
        "python": (True, [], _proc(stdout="ok")),
        "go": (True, [], _proc(stdout="ok")),
        "ts": (False, ["  boom"], _proc(stdout="ok", returncode=1)),
    }
    with _fake_target("ts"), _stub_run_case(outcomes):
        report = run._run_parity_mode(cases, ["python", "go", "ts"], verbose=False)

    assert report.parity_failures == 1
    assert report.passed == 0
    label, detail = report.parity_failure_details[0]
    assert "ts=FAIL" in detail
    assert "python=PASS" in detail
    assert report.exit_code == 1


def test_parity_mode_all_fail_is_consistent_not_parity_break():
    case = {"name": "synthetic", "targets": ["python", "go", "ts"]}
    cases = [("fake.json", case)]
    outcomes = {
        "python": (False, ["x"], _proc(returncode=1)),
        "go": (False, ["x"], _proc(returncode=1)),
        "ts": (False, ["x"], _proc(returncode=1)),
    }
    with _fake_target("ts"), _stub_run_case(outcomes):
        report = run._run_parity_mode(cases, ["python", "go", "ts"], verbose=False)

    assert report.consistent_failures == 1
    assert report.parity_failures == 0
    assert report.exit_code == 0


def test_parity_mode_skips_single_target_case():
    # A case scoped to one registered target has nothing to compare -> skipped.
    case = {"name": "py-only", "targets": ["python"]}
    cases = [("fake.json", case)]
    outcomes = {"python": (True, [], _proc(stdout="ok"))}
    with _stub_run_case(outcomes):
        report = run._run_parity_mode(cases, ["python", "go"], verbose=False)

    assert report.total == 0
    assert report.exit_code == 0


# --- the shared structural comparator ---------------------------------------
#
# One comparator backs every structure-aware assertion (effects_equals,
# schema_command_keys, protocol_script line matching), so the wildcard and the
# key-set rule mean the same thing in all of them.


def test_structural_equal_ignores_key_order():
    assert run._structural_equal({"b": 1, "a": 2}, {"a": 2, "b": 1}) == []


def test_structural_equal_reports_key_set_difference():
    errors = run._structural_equal({"a": 1}, {"a": 1, "b": 2})
    assert len(errors) == 1
    assert "missing ['b']" in errors[0]
    errors = run._structural_equal({"a": 1, "b": 2}, {"a": 1})
    assert len(errors) == 1
    assert "unexpected ['b']" in errors[0]


def test_structural_equal_wildcard_matches_any_value():
    for actual in ("sig", 17, None, {"nested": True}, [1, 2]):
        assert run._structural_equal({"sig": actual}, {"sig": run.ANY_VALUE}) == []


def test_structural_equal_wildcard_still_requires_the_key():
    errors = run._structural_equal({}, {"sig": run.ANY_VALUE})
    assert len(errors) == 1
    assert "missing ['sig']" in errors[0]


def test_structural_equal_recurses_and_names_the_path():
    errors = run._structural_equal(
        {"a": {"b": [1, 2]}}, {"a": {"b": [1, 3]}}, "root"
    )
    assert len(errors) == 1
    assert "root.a.b[1]" in errors[0]


def test_structural_equal_array_length_mismatch():
    errors = run._structural_equal([1], [1, 2])
    assert len(errors) == 1
    assert "array length 1" in errors[0]


def test_structural_equal_type_mismatch():
    assert run._structural_equal("x", {"a": 1})
    assert run._structural_equal({"a": 1}, [1])


# --- the line-scripted protocol driver ---------------------------------------
#
# Driven against a tiny in-process echo child rather than a real target, so the
# driver's own contract (lock-step send/read, structural line assertions, a
# named failure instead of a hang) is pinned independently of any harness.

_ECHO_CHILD = (
    "import sys\n"
    "for line in sys.stdin:\n"
    "    sys.stdout.write(line)\n"
    "    sys.stdout.flush()\n"
)

_SILENT_CHILD = "import sys\nsys.stdin.read()\n"


def _script(child_src: str, steps: list[dict]):
    return run._run_protocol_script(
        [sys.executable, "-c", child_src], dict(os.environ), str(run.CONFORMANCE_DIR), steps
    )


def test_protocol_script_asserts_each_reply_line_in_order():
    result, errors = _script(
        _ECHO_CHILD,
        [
            {"send": '{"id":1}', "expect_line": {"json_equals": {"id": 1}}},
            {"send": '{"id":2}', "expect_line": {"equals": '{"id":2}'}},
            {"send": "plain", "expect_line": {"contains": "plai"}},
        ],
    )
    assert errors == [], errors
    assert result.returncode == 0
    assert result.stdout == '{"id":1}\n{"id":2}\nplain\n'


def test_protocol_script_json_equals_ignores_key_order_and_spacing():
    # The whole point: one expectation serves three different serializers.
    result, errors = _script(
        _ECHO_CHILD,
        [{"send": '{"b": 2, "a": 1}', "expect_line": {"json_equals": {"a": 1, "b": 2}}}],
    )
    assert errors == [], errors


def test_protocol_script_json_equals_supports_the_wildcard():
    _, errors = _script(
        _ECHO_CHILD,
        [{"send": '{"sig":"whatever"}', "expect_line": {"json_equals": {"sig": run.ANY_VALUE}}}],
    )
    assert errors == []


def test_protocol_script_reports_a_mismatched_line():
    _, errors = _script(
        _ECHO_CHILD,
        [{"send": '{"id":1}', "expect_line": {"json_equals": {"id": 2}}}],
    )
    assert len(errors) == 2
    assert "json_equals mismatch" in errors[0]


def test_protocol_script_reports_a_missing_line_instead_of_hanging():
    original = run._SCRIPT_STEP_TIMEOUT
    run._SCRIPT_STEP_TIMEOUT = 1.0
    try:
        _, errors = _script(
            _SILENT_CHILD,
            [{"send": "anything", "expect_line": {"contains": "never"}}],
        )
    finally:
        run._SCRIPT_STEP_TIMEOUT = original
    assert len(errors) == 1
    assert "no line within" in errors[0]


def test_protocol_script_reports_a_non_json_line_under_json_equals():
    _, errors = _script(
        _ECHO_CHILD,
        [{"send": "not json", "expect_line": {"json_equals": {"a": 1}}}],
    )
    assert len(errors) == 1
    assert "not valid JSON" in errors[0]


if __name__ == "__main__":
    failures = 0
    for _name, _fn in sorted(globals().items()):
        if _name.startswith("test_") and callable(_fn):
            try:
                _fn()
                print(f"PASS {_name}")
            except AssertionError as exc:
                failures += 1
                print(f"FAIL {_name}: {exc}")
    sys.exit(1 if failures else 0)


def test_protocol_script_captures_a_value_and_splices_it_into_a_later_line():
    """The round-trip shape: a later request quotes what the server minted."""
    _, errors = _script(
        _ECHO_CHILD,
        [
            {"send": '{"state":"abc"}', "capture": {"state": "state"}},
            {"send": '{"echo":"{{state}}"}', "expect_line": {"json_equals": {"echo": "abc"}}},
        ],
    )
    assert errors == [], errors


def test_protocol_script_tamper_changes_the_captured_values_first_character():
    """The FIRST character: a base64 blob's last character may carry no bits."""
    _, errors = _script(
        _ECHO_CHILD,
        [
            {"send": '{"state":"abc"}', "capture": {"state": "state"}},
            {
                "send": '{"echo":"{{state|tamper}}"}',
                "expect_line": {"json_equals": {"echo": "Abc"}},
            },
        ],
    )
    assert errors == [], errors


def test_protocol_script_reports_a_capture_path_that_is_not_there():
    _, errors = _script(
        _ECHO_CHILD,
        [{"send": '{"state":"abc"}', "capture": {"state": "result.requestState"}}],
    )
    assert any("found no 'result.requestState'" in e for e in errors), errors


def test_protocol_script_reports_a_substitution_with_nothing_captured():
    _, errors = _script(_ECHO_CHILD, [{"send": '{"echo":"{{missing}}"}'}])
    assert any("nothing captured under 'missing'" in e for e in errors), errors


# --- Temp-directory hygiene --------------------------------------------------
#
# The suite's throwaway paths live in the operating system's temp directory and
# nothing sweeps that directory afterwards, so a path a run leaves behind is
# permanent. Both tests below run the real thing rather than inspecting the
# code that makes the paths.


def _case_named(file_name: str, name: str) -> dict:
    """One case, read straight from its file.

    run._load_cases() validates every case in the suite against the schema,
    which costs far more than the one case these tests run.
    """
    with open(run.CASES_DIR / file_name, encoding="utf-8") as fh:
        for case in json.load(fh):
            if case["name"] == name:
                return case
    raise AssertionError(f"no case named {name!r} in {file_name}")


def test_running_a_checks_case_leaves_nothing_in_the_temp_directory():
    """Every temp path one case run makes is gone when the run returns.

    The checks cases are the sharp ones: the generated Python reference script
    has to get its checks.toml to the app somehow, and a file written into the
    temp directory is a file nobody deletes.
    """
    case = _case_named("checks.json", "checks: all passing exits 0")
    prior_tempdir = tempfile.tempdir
    prior_env = os.environ.get("TMPDIR")
    with tempfile.TemporaryDirectory(prefix="strictcli_leakprobe_") as probe:
        tempfile.tempdir = probe
        os.environ["TMPDIR"] = probe
        try:
            before = set(os.listdir(probe))
            _ok, errors, result = run._run_case(case, "python")
            leaked = sorted(set(os.listdir(probe)) - before)
        finally:
            tempfile.tempdir = prior_tempdir
            if prior_env is None:
                os.environ.pop("TMPDIR", None)
            else:
                os.environ["TMPDIR"] = prior_env
    # Not `assert ok`: whether this case passes is other tests' business, and
    # a checkout whose own scratch directory trips a built-in check would hide
    # the leak behind an unrelated red. That the reference script ran at all is
    # what makes the assertion below meaningful.
    assert result is not None, errors
    assert leaked == [], f"the case run left these behind in the temp dir: {leaked}"


def test_importing_run_leaves_no_throwaway_home_behind():
    """run.py's module-level throwaway HOME is removed when the process ends.

    Importing run.py creates it, and every process that imports run.py -- the
    runner, the fuzzer, the trace sweeps, this test file -- creates one more.
    """
    probe = subprocess.run(
        [sys.executable, "-c", "import run; print(run.TRACE_HOME)"],
        cwd=str(Path(__file__).resolve().parent),
        capture_output=True,
        text=True,
        check=True,
    )
    home = probe.stdout.strip()
    assert home, probe.stderr
    assert not os.path.exists(home), (
        f"importing run.py left its throwaway HOME behind at {home}"
    )
