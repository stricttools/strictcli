"""Tests for the check-provider hook: register_check_provider, CheckSpec,
error_check_spec/warn_check_spec, lazy per-cwd materialization, and reset."""

import json
import os
from dataclasses import dataclass
from pathlib import Path

import pytest

import strictcli
from strictcli import CheckSpec, error_check_spec, warn_check_spec
from conftest import drop_builtin_check_providers, pass_outcome, payload


TOML = """\
app = "testapp"

[checks.version-consistency]
description = "Checks version-consistency"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.changelog-coverage]
description = "Checks changelog-coverage"
subject = "quality"
tags = ["changelog"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
"""


@dataclass
class SimpleContext:
    project_root: Path


# An empty, dedicated project root. Checks that statically analyse the
# consumer's sources (effects-bypass) walk this, so it must not be a shared
# scratch directory.
EMPTY_ROOT = Path(__file__).parent / "_fixtures" / "empty_project"
EMPTY_ROOT.mkdir(parents=True, exist_ok=True)


def err_spec(name, tags=("provider",)):
    return error_check_spec(
        name=name, tags=list(tags), fast=True, pure=True,
        needs_network=False, depends_on=[],
        impl=lambda ctx, r: r.passed(f"{name} ok"),
    )


def make_app():
    app = strictcli.App(name="testapp", version="1.0.0", help="test app")
    app.set_check_context(lambda: SimpleContext(project_root=EMPTY_ROOT))
    return app


# --- TOML-less app + provider = working check command ---

def test_tomlless_list_dryrun_execution():
    app = make_app()
    app.register_check_provider(lambda: [err_spec("prov-a"), err_spec("prov-b")])
    assert app._checks_enabled  # registering a provider enables the check system

    r = app.test(["check", "--list"])
    assert r.exit_code == 0
    assert "prov-a" in r.stdout and "prov-b" in r.stdout

    r = app.test(["--dry-run", "check", "--all"])
    assert "prov-a" in r.stdout and "prov-b" in r.stdout

    r = app.test(["check", "--all"])
    assert r.exit_code == 0
    assert "prov-a ok" in r.stdout and "prov-b ok" in r.stdout


def test_programmatic_run_checks():
    app = make_app()
    app.register_check_provider(lambda: [err_spec("prov-a")])
    drop_builtin_check_providers(app)
    results, _, code = app.run_checks(
        SimpleContext(project_root=EMPTY_ROOT), run_all=True,
    )
    assert code == 0
    assert len(results) == 1 and results[0].name == "prov-a"


# --- TOML + provider coexist ---

def test_coexists_with_toml(tmp_path):
    toml_file = tmp_path / "checks.toml"
    toml_file.write_text(TOML)
    app = strictcli.App(
        name="testapp", version="1.0.0", help="test app",
        checks_path=str(toml_file),
    )
    for name in ("version-consistency", "changelog-coverage"):
        app._check_defs[name].impl = lambda ctx, n=name: pass_outcome(f"{n} ok")
    app.register_check_provider(lambda: [err_spec("prov-a")])

    r = app.test(["check", "--list"])
    for want in ("version-consistency", "changelog-coverage", "prov-a"):
        assert want in r.stdout


# --- collisions are hard errors ---

def test_collision_with_toml(tmp_path):
    toml_file = tmp_path / "checks.toml"
    toml_file.write_text(TOML)
    app = strictcli.App(
        name="testapp", version="1.0.0", help="test app",
        checks_path=str(toml_file),
    )
    for name in ("version-consistency", "changelog-coverage"):
        app._check_defs[name].impl = lambda ctx, n=name: pass_outcome(f"{n} ok")
    app.set_check_context(lambda: SimpleContext(project_root=EMPTY_ROOT))
    drop_builtin_check_providers(app)
    app.register_check_provider(lambda: [err_spec("version-consistency")])

    with pytest.raises(ValueError, match="duplicate check definition"):
        app.test(["check", "--list"])


def test_collision_between_providers():
    app = make_app()
    app.register_check_provider(lambda: [err_spec("dup")])
    app.register_check_provider(lambda: [err_spec("dup")])
    with pytest.raises(ValueError, match="duplicate check definition"):
        app.test(["check", "--list"])


# --- provider raise = hard error in every mode ---

@pytest.mark.parametrize("argv", [
    ["check", "--list"],
    ["--dry-run", "check", "--all"],
    ["check", "--all"],
])
def test_provider_raise_hard_error_cli(argv):
    app = make_app()

    def boom():
        raise RuntimeError("provider boom")

    app.register_check_provider(boom)
    with pytest.raises(RuntimeError, match="provider boom"):
        app.test(argv)


def test_provider_raise_hard_error_programmatic():
    app = make_app()

    def boom():
        raise RuntimeError("provider boom")

    app.register_check_provider(boom)
    with pytest.raises(RuntimeError, match="provider boom"):
        app.run_checks(SimpleContext(project_root=EMPTY_ROOT), run_all=True)


# --- honest-empty is a clean no-op ---

def test_honest_empty():
    app = make_app()
    app.register_check_provider(lambda: [])
    drop_builtin_check_providers(app)
    r = app.test(["check", "--list"])
    assert r.exit_code == 0
    results, _, code = app.run_checks(
        SimpleContext(project_root=EMPTY_ROOT), run_all=True,
    )
    assert code == 0 and results == []


# --- severity-form mismatch ---

def test_severity_form_mismatch():
    app = make_app()
    # Declares "warn" but built via the error-form constructor.
    app.register_check_provider(lambda: [error_check_spec(
        name="mismatch", tags=["x"], severity="warn", fast=True, pure=True,
        needs_network=False, depends_on=[], impl=lambda ctx, r: r.passed("ok"),
    )])
    with pytest.raises(ValueError, match="declared severity"):
        app.test(["check", "--list"])


# --- memoization: provider called once across repeated reads ---

def test_memoized_once():
    app = make_app()
    calls = {"n": 0}

    def provider():
        calls["n"] += 1
        return [err_spec("prov-a")]

    app.register_check_provider(provider)
    app.test(["check", "--list"])
    app.test(["--dry-run", "check", "--all"])
    app.test(["check", "--all"])
    app.run_checks(SimpleContext(project_root=EMPTY_ROOT), run_all=True)
    assert calls["n"] == 1


# --- cwd-change re-materialization ---

def test_cwd_change_rematerializes(tmp_path):
    dir_a = tmp_path / "a"
    dir_b = tmp_path / "b"
    dir_a.mkdir()
    dir_b.mkdir()
    orig = os.getcwd()

    app = make_app()
    calls = {"n": 0}

    def provider():
        calls["n"] += 1
        cwd = os.getcwd()
        name = "in-b" if cwd == str(dir_b) else "in-a"
        return [err_spec(name)]

    app.register_check_provider(provider)
    try:
        os.chdir(dir_a)
        r = app.test(["check", "--list"])
        assert "in-a" in r.stdout
        app.test(["check", "--list"])  # same cwd, memoized
        assert calls["n"] == 1

        os.chdir(dir_b)
        r = app.test(["check", "--list"])
        assert calls["n"] == 2
        assert "in-b" in r.stdout and "in-a" not in r.stdout
    finally:
        os.chdir(orig)


# --- reset ---

def test_reset_cache():
    app = make_app()
    calls = {"n": 0}

    def provider():
        calls["n"] += 1
        return [err_spec("prov-a")]

    app.register_check_provider(provider)
    app.test(["check", "--list"])
    assert calls["n"] == 1
    app.reset_check_provider_cache()
    assert "prov-a" not in app._check_defs  # provider def dropped
    app.test(["check", "--list"])
    assert calls["n"] == 2


# --- list JSON carries provider severity ---

def test_list_json_severity():
    app = make_app()
    app.register_check_provider(lambda: [warn_check_spec(
        name="warn-prov", tags=["w"], fast=True, pure=True,
        needs_network=False, depends_on=[], impl=lambda ctx, r: r.passed("ok"),
    )])
    drop_builtin_check_providers(app)
    r = app.test(["check", "--list", "--json"])
    entries = payload(r)
    assert len(entries) == 1
    assert entries[0]["name"] == "warn-prov"
    assert entries[0]["severity"] == "warn"


# --- register validation ---

def test_register_non_callable_raises():
    app = make_app()
    with pytest.raises(ValueError, match="must be callable"):
        app.register_check_provider(42)


def test_non_checkspec_element_raises():
    app = make_app()
    app.register_check_provider(lambda: ["not a spec"])
    with pytest.raises(ValueError, match="non-CheckSpec"):
        app.test(["check", "--list"])


def test_bad_return_type_raises():
    app = make_app()
    app.register_check_provider(lambda: "not a list")
    with pytest.raises(ValueError, match="must return a list"):
        app.test(["check", "--list"])


def test_checkspec_is_exported():
    assert CheckSpec.__name__ == "CheckSpec"
