"""Tests for the auto-registered 'check' command."""

import json
from dataclasses import dataclass
from pathlib import Path

import pytest

import strictcli
from conftest import drop_builtin_check_providers, pass_outcome, fail_outcome, warn_outcome, payload


TWO_CHECKS_TOML = """\
app = "testapp"

[checks.version-check]
description = "Checks version-check"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.lint-check]
description = "Checks lint-check"
subject = "quality"
tags = ["code", "fast"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
"""

PURE_AND_IMPURE_TOML = """\
app = "testapp"

[checks.lint-check]
description = "Checks lint-check"
subject = "quality"
tags = ["code"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.deploy-check]
description = "Checks deploy-check"
subject = "quality"
tags = ["release"]
severity = "error"
fast = false
pure = false
needs_network = false
depends_on = []
"""

THREE_CHECKS_WITH_DEP_TOML = """\
app = "testapp"

[checks.base-check]
description = "Checks base-check"
subject = "quality"
tags = ["infra"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.version-check]
description = "Checks version-check"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = ["base-check"]

[checks.lint-check]
description = "Checks lint-check"
subject = "quality"
tags = ["code", "fast"]
severity = "warn"
fast = true
pure = true
needs_network = false
depends_on = []
"""


@dataclass
class SimpleContext:
    project_root: Path


def _setup_checks_app(tmp_path, monkeypatch, toml_content, register_impls=True,
                       pass_results=None):
    """Create a temp dir with checks.toml, build an App, and register impls.

    pass_results: dict mapping check name to a minted outcome. If None, all return pass.
    """
    toml_file = tmp_path / "checks.toml"
    toml_file.write_text(toml_content)

    app = strictcli.App(
        name="testapp", version="1.0.0", help="test app",
        checks_path=str(toml_file),
    )

    if register_impls:
        if pass_results is None:
            pass_results = {}
        for name in app._check_defs:
            if name in pass_results:
                result = pass_results[name]
                app._check_defs[name].impl = lambda ctx, r=result: r
            else:
                app._check_defs[name].impl = (
                    lambda ctx, n=name: pass_outcome(f"{n} OK")
                )

    app.set_check_context(lambda: SimpleContext(project_root=tmp_path))
    drop_builtin_check_providers(app)
    return app


class TestCheckCommandBasic:
    def test_no_flags_shows_help(self, tmp_path, monkeypatch):
        """check with no flags shows help and exits 0."""
        app = _setup_checks_app(tmp_path, monkeypatch, TWO_CHECKS_TOML)
        result = app.test(["check"])
        assert result.exit_code == 0
        assert "check" in result.stdout.lower()

    def test_check_not_in_help_without_toml(self, tmp_path, monkeypatch):
        """check command should not appear in help when no TOML exists."""
        monkeypatch.chdir(tmp_path)
        app = strictcli.App(name="testapp", version="1.0.0", help="test app")

        @app.command("hello", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="say hello")
        def hello(ctx, **kw):
            print("hello")

        result = app.test(["--help"])
        assert result.exit_code == 0
        # "check" should not appear as a command
        assert "check" not in result.stdout.lower() or "check" not in [
            line.strip().split()[0]
            for line in result.stdout.splitlines()
            if line.startswith("  ")
        ]

    def test_check_in_help_with_toml(self, tmp_path, monkeypatch):
        """check command appears in help when TOML exists."""
        app = _setup_checks_app(tmp_path, monkeypatch, TWO_CHECKS_TOML)

        @app.command("hello", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="say hello")
        def hello(ctx, **kw):
            print("hello")

        result = app.test(["--help"])
        assert result.exit_code == 0
        assert "check" in result.stdout


class TestCheckList:
    def test_list_shows_checks(self, tmp_path, monkeypatch):
        """--list shows check names and tags."""
        app = _setup_checks_app(tmp_path, monkeypatch, TWO_CHECKS_TOML)
        result = app.test(["check", "--list"])
        assert result.exit_code == 0
        assert "version-check" in result.stdout
        assert "lint-check" in result.stdout
        assert "release" in result.stdout
        assert "NAME" in result.stdout

    def test_list_json(self, tmp_path, monkeypatch):
        """--list --json produces valid JSON."""
        app = _setup_checks_app(tmp_path, monkeypatch, TWO_CHECKS_TOML)
        result = app.test(["check", "--list", "--json"])
        assert result.exit_code == 0
        data = payload(result)
        assert isinstance(data, list)
        assert len(data) == 2
        names = {item["name"] for item in data}
        assert "version-check" in names
        assert "lint-check" in names
        for item in data:
            assert "tags" in item
            assert "severity" in item


class TestCheckExecution:
    def test_all_passing(self, tmp_path, monkeypatch):
        """--all runs all checks; all passing gives exit 0."""
        app = _setup_checks_app(tmp_path, monkeypatch, TWO_CHECKS_TOML)
        result = app.test(["check", "--all"])
        assert result.exit_code == 0
        assert "PASS" in result.stdout

    def test_all_with_failure(self, tmp_path, monkeypatch):
        """--all with a failing check gives exit 1."""
        app = _setup_checks_app(
            tmp_path, monkeypatch, TWO_CHECKS_TOML,
            pass_results={
                "version-check": fail_outcome("version mismatch"),
            },
        )
        result = app.test(["check", "--all"])
        assert result.exit_code == 1
        assert "FAIL" in result.stdout
        assert "version mismatch" in result.stdout

    def test_filter_by_tag(self, tmp_path, monkeypatch):
        """--tag filters to checks with matching tags."""
        # Track which checks actually ran
        ran = []

        app = _setup_checks_app(
            tmp_path, monkeypatch, TWO_CHECKS_TOML, register_impls=False,
        )
        for name in app._check_defs:
            def make_impl(n):
                def impl(ctx):
                    ran.append(n)
                    return pass_outcome(f"{n} OK")
                return impl
            app._check_defs[name].impl = make_impl(name)

        result = app.test(["check", "--tag", "release"])
        assert result.exit_code == 0
        assert "version-check" in ran
        assert "lint-check" not in ran

    def test_filter_by_name_glob(self, tmp_path, monkeypatch):
        """--name filters by glob pattern."""
        ran = []

        app = _setup_checks_app(
            tmp_path, monkeypatch, TWO_CHECKS_TOML, register_impls=False,
        )
        for name in app._check_defs:
            def make_impl(n):
                def impl(ctx):
                    ran.append(n)
                    return pass_outcome(f"{n} OK")
                return impl
            app._check_defs[name].impl = make_impl(name)

        result = app.test(["check", "--name", "version-*"])
        assert result.exit_code == 0
        assert "version-check" in ran
        assert "lint-check" not in ran


class TestCheckDryRun:
    @staticmethod
    def _recording_impls(app, ran):
        for name in app._check_defs:
            def make_impl(n):
                def impl(ctx):
                    ran.append(n)
                    return pass_outcome(f"{n} OK")
                return impl
            app._check_defs[name].impl = make_impl(name)

    def test_dry_run_executes_the_pure_checks(self, tmp_path, monkeypatch):
        """--dry-run runs the checks declared pure; the plan is what is left."""
        ran = []
        app = _setup_checks_app(
            tmp_path, monkeypatch, THREE_CHECKS_WITH_DEP_TOML,
            register_impls=False,
        )
        self._recording_impls(app, ran)

        result = app.test(["--dry-run", "check", "--all"])
        assert result.exit_code == 0
        # Every check in this TOML is pure, so every one really ran.
        assert sorted(ran) == ["base-check", "lint-check", "version-check"]
        assert "PASS  version-check" in result.stdout
        assert "Would run 0 checks:" in result.stdout

    def test_dry_run_lists_the_impure_checks_without_running_them(
        self, tmp_path, monkeypatch,
    ):
        ran = []
        app = _setup_checks_app(
            tmp_path, monkeypatch, PURE_AND_IMPURE_TOML, register_impls=False,
        )
        self._recording_impls(app, ran)

        result = app.test(["--dry-run", "check", "--all"])
        assert result.exit_code == 0
        assert ran == ["lint-check"]  # the impure one never ran
        assert "PASS  lint-check" in result.stdout
        assert "Would run 1 check:\n  1. deploy-check [impure]" in result.stdout

    def test_dry_run_reports_a_failing_pure_check(self, tmp_path, monkeypatch):
        """A rehearsal that finds a real failure fails: exit code 1."""
        app = _setup_checks_app(
            tmp_path, monkeypatch, PURE_AND_IMPURE_TOML,
            pass_results={"lint-check": fail_outcome("lint failed", "boom")},
        )
        result = app.test(["--dry-run", "check", "--all"])
        assert result.exit_code == 1
        assert "FAIL  lint-check" in result.stdout


class TestCheckJsonOutput:
    def test_json_output(self, tmp_path, monkeypatch):
        """--all --json produces valid JSON with results."""
        app = _setup_checks_app(tmp_path, monkeypatch, TWO_CHECKS_TOML)
        result = app.test(["check", "--all", "--json"])
        assert result.exit_code == 0
        data = payload(result)
        assert isinstance(data, list)
        assert len(data) == 2
        for item in data:
            assert "name" in item
            assert "status" in item
            assert "message" in item
            assert "problems" in item
            assert isinstance(item["problems"], list)


class TestCheckVerbose:
    def test_verbose_pass_has_no_problem_lines(self, tmp_path, monkeypatch):
        """A passing check carries no problems, so --verbose reveals nothing
        extra: the run shows PASS but emits no problem lines."""
        app = _setup_checks_app(
            tmp_path, monkeypatch, TWO_CHECKS_TOML,
            pass_results={
                "version-check": pass_outcome("All good"),
            },
        )
        result = app.test(["--verbose", "check", "--all"])
        assert result.exit_code == 0
        assert "PASS" in result.stdout
        assert "[error]" not in result.stdout
        assert "[warn]" not in result.stdout

    def test_non_verbose_pass_has_no_problem_lines(self, tmp_path, monkeypatch):
        """Without --verbose, a passing check shows no problem lines either."""
        app = _setup_checks_app(
            tmp_path, monkeypatch, TWO_CHECKS_TOML,
            pass_results={
                "version-check": pass_outcome("All good"),
            },
        )
        result = app.test(["check", "--all"])
        assert result.exit_code == 0
        assert "[error]" not in result.stdout
        assert "[warn]" not in result.stdout


class TestCheckWarnings:
    def test_ignore_warnings_is_gone(self, tmp_path, monkeypatch):
        """The --ignore-warnings escape hatch no longer exists: the flag is
        refused like any undeclared flag."""
        app = _setup_checks_app(
            tmp_path, monkeypatch, THREE_CHECKS_WITH_DEP_TOML,
            pass_results={
                "lint-check": warn_outcome("minor issue"),
            },
        )
        result = app.test(["check", "--all", "--ignore-warnings"])
        assert result.exit_code == 1
        assert "--ignore-warnings" in result.stderr

    def test_warn_without_ignore_causes_exit_1(self, tmp_path, monkeypatch):
        """A warning makes check exit 1: check is the full report."""
        app = _setup_checks_app(
            tmp_path, monkeypatch, THREE_CHECKS_WITH_DEP_TOML,
            pass_results={
                "lint-check": warn_outcome("minor issue"),
            },
        )
        result = app.test(["check", "--all"])
        assert result.exit_code == 1


class TestCheckNoContextFactory:
    def test_no_context_factory_error(self, tmp_path):
        """Running checks without set_check_context produces error."""
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(TWO_CHECKS_TOML)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )
        for name in app._check_defs:
            app._check_defs[name].impl = (
                lambda ctx, n=name: pass_outcome(f"{n} OK")
            )
        # Do NOT call set_check_context

        result = app.test(["check", "--all"])
        assert result.exit_code == 1
        assert "no check context configured" in result.stderr


class TestCheckFailDetails:
    def test_fail_details_shown(self, tmp_path, monkeypatch):
        """Failing check details are shown without --verbose."""
        app = _setup_checks_app(
            tmp_path, monkeypatch, TWO_CHECKS_TOML,
            pass_results={
                "version-check": fail_outcome("3 commits not covered", "a1b2c3d: fix typo", "e4f5g6h: add feature"),
            },
        )
        result = app.test(["check", "--all"])
        assert result.exit_code == 1
        assert "a1b2c3d: fix typo" in result.stdout
        assert "e4f5g6h: add feature" in result.stdout


class TestCheckNoMatchFilters:
    def test_no_matches(self, tmp_path, monkeypatch):
        """When filters match nothing, print message and exit 0."""
        app = _setup_checks_app(tmp_path, monkeypatch, TWO_CHECKS_TOML)
        result = app.test(["check", "--tag", "nonexistent"])
        assert result.exit_code == 0
        assert "No checks matched" in result.stdout


class TestCheckCommandVerboseNotes:
    """End-to-end: --verbose surfaces per-check notes (including on passing
    checks), per-check durations, and a trailing count summary. Notes never
    affect the exit code."""

    def _noted_pass(self):
        r = strictcli.ErrorReporter()
        r.note("a verbose-only note")
        return r.passed("version OK")

    def test_verbose_shows_notes_duration_and_summary(self, tmp_path, monkeypatch):
        app = _setup_checks_app(
            tmp_path, monkeypatch, TWO_CHECKS_TOML,
            pass_results={"version-check": self._noted_pass()},
        )
        result = app.test(["--verbose", "check", "--all"])
        assert result.exit_code == 0
        assert "[note] a verbose-only note" in result.stdout
        import re
        assert re.search(r"\(\d+ms\)", result.stdout)
        assert "2 passed / 0 failed / 0 warned / 0 skipped" in result.stdout

    def test_normal_mode_hides_notes(self, tmp_path, monkeypatch):
        app = _setup_checks_app(
            tmp_path, monkeypatch, TWO_CHECKS_TOML,
            pass_results={"version-check": self._noted_pass()},
        )
        result = app.test(["check", "--all"])
        assert result.exit_code == 0
        assert "[note]" not in result.stdout
        assert "passed /" not in result.stdout
        assert "ms)" not in result.stdout

    def test_dry_run_emits_the_framework_would_do_header(self, tmp_path, monkeypatch):
        """--dry-run is now the framework flag, so it also puts the whole run
        in dry mode. check is read_only, so the body is empty."""
        app = _setup_checks_app(tmp_path, monkeypatch, TWO_CHECKS_TOML)
        result = app.test(["--dry-run", "check", "--all"])
        assert result.exit_code == 0
        assert result.stdout.endswith(
            "DRY RUN — no changes were made. Would do:\n"
        )
        # Both checks are pure, so both ran and nothing is left to plan.
        assert "Would run 0 checks:" in result.stdout

    def test_check_no_longer_declares_verbose_dry_run_or_json_flags(self, tmp_path, monkeypatch):
        """--verbose, --dry-run and --json are framework-owned reserved names,
        so the check command's own three flags are dropped and the values are
        read off the Context (subsumption, contract §7.5)."""
        app = _setup_checks_app(tmp_path, monkeypatch, TWO_CHECKS_TOML)
        check_cmd = app._commands["check"]
        names = {f.name for f in check_cmd.flags}
        assert names == {"all", "tag", "name", "hook", "list"}
        result = app.test(["check", "--help"])
        assert result.exit_code == 0
        assert "--verbose" not in result.stdout
        assert "--dry-run" not in result.stdout
        assert "--json" not in result.stdout

    def test_json_includes_notes_and_duration(self, tmp_path, monkeypatch):
        app = _setup_checks_app(
            tmp_path, monkeypatch, TWO_CHECKS_TOML,
            pass_results={"version-check": self._noted_pass()},
        )
        result = app.test(["check", "--all", "--json"])
        assert result.exit_code == 0
        data = payload(result)
        by_name = {item["name"]: item for item in data}
        assert by_name["version-check"]["notes"] == ["a verbose-only note"]
        for item in data:
            assert "notes" in item
            assert "duration_ms" in item
