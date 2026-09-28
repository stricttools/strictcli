"""Tests for the public check runner API: run_checks(), CheckRunResult, formatters."""

import json
from dataclasses import FrozenInstanceError, dataclass
from pathlib import Path

import pytest

import strictcli
from strictcli import (
    CheckContext,
    CheckRunResult,
    SkipCheck,
    format_check_results,
    format_check_results_json,
)
from conftest import drop_builtin_check_providers, pass_outcome, fail_outcome, warn_outcome, skip_outcome


TWO_CHECKS_TOML = """\
app = "testapp"

[checks.alpha]
description = "Checks alpha"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.beta]
description = "Checks beta"
subject = "quality"
tags = ["code"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
"""

DEP_CHAIN_TOML = """\
app = "testapp"

[checks.base]
description = "Checks base"
subject = "quality"
tags = ["infra"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.mid]
description = "Checks mid"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = ["base"]

[checks.top]
description = "Checks top"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = ["mid"]
"""

WARN_CHECK_TOML = """\
app = "testapp"

[checks.warn-check]
description = "Checks warn-check"
subject = "quality"
tags = ["all"]
severity = "warn"
fast = true
pure = true
needs_network = false
depends_on = []
"""

SINGLE_CHECK_TOML = """\
app = "testapp"

[checks.only]
description = "Checks only"
subject = "quality"
tags = ["default"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
"""


@dataclass
class SimpleContext:
    project_root: Path


def _make_app(tmp_path, toml_content, impls=None, register_all=True):
    """Build an app with checks, optionally registering implementations.

    impls: dict of check name -> callable(ctx) returning a minted outcome.
    register_all: if True and a check has no entry in impls, register a
    default passing impl.
    """
    toml_file = tmp_path / "checks.toml"
    toml_file.write_text(toml_content)

    app = strictcli.App(
        name="testapp", version="1.0.0", help="test app",
        checks_path=str(toml_file),
    )
    drop_builtin_check_providers(app)

    if impls is None:
        impls = {}

    if register_all:
        for name in app._check_defs:
            if name in impls:
                app._check_defs[name].impl = impls[name]
            else:
                app._check_defs[name].impl = (
                    lambda ctx, n=name: pass_outcome(f"{n} OK")
                )
    else:
        for name, fn in impls.items():
            app._check_defs[name].impl = fn

    return app


# ---------------------------------------------------------------------------
# CheckRunResult dataclass
# ---------------------------------------------------------------------------


class TestCheckRunResult:
    def test_fields_accessible(self):
        cr = pass_outcome("OK")
        r = CheckRunResult(name="my-check", outcome=cr)
        assert r.name == "my-check"
        assert r.outcome is cr
        assert r.status == "pass"
        assert r.message == "OK"

    def test_frozen(self):
        cr = pass_outcome("OK")
        r = CheckRunResult(name="my-check", outcome=cr)
        with pytest.raises(FrozenInstanceError):
            r.name = "other"  # type: ignore[misc]

    def test_frozen_result_field(self):
        cr = pass_outcome("OK")
        r = CheckRunResult(name="my-check", outcome=cr)
        with pytest.raises(FrozenInstanceError):
            r.outcome = fail_outcome("bad")  # type: ignore[misc]


# ---------------------------------------------------------------------------
# App.run_checks()
# ---------------------------------------------------------------------------


class TestRunChecks:
    def test_all_pass(self, tmp_path):
        app = _make_app(tmp_path, TWO_CHECKS_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = app.run_checks(ctx, run_all=True)
        assert exit_code == 0
        assert len(results) == 2
        for r in results:
            assert isinstance(r, CheckRunResult)
            assert r.status == "pass"

    def test_one_fails(self, tmp_path):
        impls = {
            "alpha": lambda ctx: fail_outcome("broken"),
        }
        app = _make_app(tmp_path, TWO_CHECKS_TOML, impls=impls)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = app.run_checks(ctx, run_all=True)
        assert exit_code == 1
        statuses = {r.name: r.status for r in results}
        assert statuses["alpha"] == "fail"
        assert statuses["beta"] == "pass"

    def test_tag_filtering(self, tmp_path):
        app = _make_app(tmp_path, TWO_CHECKS_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = app.run_checks(ctx, tag_expr="release")
        assert exit_code == 0
        assert len(results) == 1
        assert results[0].name == "alpha"

    def test_name_glob(self, tmp_path):
        app = _make_app(tmp_path, TWO_CHECKS_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = app.run_checks(ctx, name_glob="bet*")
        assert exit_code == 0
        assert len(results) == 1
        assert results[0].name == "beta"

    def test_run_all(self, tmp_path):
        app = _make_app(tmp_path, TWO_CHECKS_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        names = {r.name for r in results}
        assert names == {"alpha", "beta"}

    def test_dependency_ordering(self, tmp_path):
        app = _make_app(tmp_path, DEP_CHAIN_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = app.run_checks(ctx, run_all=True)
        assert exit_code == 0
        names = [r.name for r in results]
        assert names.index("base") < names.index("mid") < names.index("top")

    def test_dependency_failure_cascade(self, tmp_path):
        impls = {
            "base": lambda ctx: fail_outcome("base broken"),
        }
        app = _make_app(tmp_path, DEP_CHAIN_TOML, impls=impls)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = app.run_checks(ctx, run_all=True)
        assert exit_code == 1
        statuses = {r.name: r.status for r in results}
        assert statuses["base"] == "fail"
        assert statuses["mid"] == "skip"
        assert statuses["top"] == "skip"

    def test_warn_exits_nonzero(self, tmp_path):
        impls = {
            "warn-check": lambda ctx: warn_outcome("caution"),
        }
        app = _make_app(tmp_path, WARN_CHECK_TOML, impls=impls)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = app.run_checks(ctx, run_all=True)
        assert exit_code == 1
        assert results[0].status == "warn"
        assert not results[0].gated()

    def test_ignore_warnings_is_gone(self, tmp_path):
        app = _make_app(tmp_path, WARN_CHECK_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        with pytest.raises(TypeError, match="ignore_warnings"):
            app.run_checks(ctx, run_all=True, ignore_warnings=True)

    def test_no_matches_empty(self, tmp_path):
        app = _make_app(tmp_path, TWO_CHECKS_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = app.run_checks(ctx, tag_expr="nonexistent")
        assert results == []
        assert exit_code == 0

    def test_error_checks_not_enabled(self, tmp_path):
        app = strictcli.App(name="testapp", version="1.0.0", help="test app")
        ctx = SimpleContext(project_root=tmp_path)
        with pytest.raises(ValueError, match="checks are not enabled"):
            app.run_checks(ctx, run_all=True)

    def test_error_incomplete_registrations(self, tmp_path):
        # Build app but don't register any impls
        app = _make_app(
            tmp_path, SINGLE_CHECK_TOML, register_all=False,
        )
        ctx = SimpleContext(project_root=tmp_path)
        with pytest.raises(ValueError, match="not registered"):
            app.run_checks(ctx, run_all=True)


# ---------------------------------------------------------------------------
# format_check_results
# ---------------------------------------------------------------------------


class TestFormatCheckResults:
    def test_returns_string(self, tmp_path):
        app = _make_app(tmp_path, SINGLE_CHECK_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        output = format_check_results(results)
        assert isinstance(output, str)
        assert len(output) > 0

    def test_format_pass(self, tmp_path):
        app = _make_app(tmp_path, SINGLE_CHECK_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        output = format_check_results(results)
        assert "PASS" in output
        assert "only" in output
        assert "only OK" in output

    def test_format_fail(self, tmp_path):
        impls = {
            "only": lambda ctx: fail_outcome("broken", "line 1 bad", "line 2 bad"),
        }
        app = _make_app(tmp_path, SINGLE_CHECK_TOML, impls=impls)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        output = format_check_results(results)
        assert "FAIL" in output
        assert "broken" in output
        assert "line 1 bad" in output
        assert "line 2 bad" in output

    def test_aligned_columns(self, tmp_path):
        app = _make_app(tmp_path, TWO_CHECKS_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        output = format_check_results(results)
        lines = output.split("\n")
        # Both check names should be padded to same width
        assert len(lines) == 2
        # "alpha" and "beta" both padded to 5 chars
        for line in lines:
            assert "PASS" in line

    def test_verbose_pass_has_no_problems(self, tmp_path):
        # A passing outcome carries no problems, so neither the default nor the
        # verbose formatting emits problem lines for it.
        impls = {
            "only": lambda ctx: pass_outcome("all good"),
        }
        app = _make_app(tmp_path, SINGLE_CHECK_TOML, impls=impls)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        output_normal = format_check_results(results, verbose=False)
        assert "[error]" not in output_normal and "[warn]" not in output_normal
        output_verbose = format_check_results(results, verbose=True)
        assert "[error]" not in output_verbose and "[warn]" not in output_verbose

    def test_fail_details_shown_without_verbose(self, tmp_path):
        impls = {
            "only": lambda ctx: fail_outcome("broken", "detail line"),
        }
        app = _make_app(tmp_path, SINGLE_CHECK_TOML, impls=impls)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        output = format_check_results(results, verbose=False)
        assert "detail line" in output

    def test_empty_results(self):
        output = format_check_results([])
        assert output == ""

    def test_no_trailing_newline(self, tmp_path):
        app = _make_app(tmp_path, SINGLE_CHECK_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        output = format_check_results(results)
        assert not output.endswith("\n")


# ---------------------------------------------------------------------------
# format_check_results_json
# ---------------------------------------------------------------------------


class TestFormatCheckResultsJson:
    def test_returns_valid_json(self, tmp_path):
        app = _make_app(tmp_path, TWO_CHECKS_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        output = format_check_results_json(results)
        parsed = json.loads(output)
        assert isinstance(parsed, list)
        assert len(parsed) == 2

    def test_json_structure(self, tmp_path):
        app = _make_app(tmp_path, SINGLE_CHECK_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        output = format_check_results_json(results)
        parsed = json.loads(output)
        item = parsed[0]
        assert item["name"] == "only"
        assert item["status"] == "pass"
        assert item["message"] == "only OK"
        assert item["problems"] == []

    def test_empty_problems_is_list(self, tmp_path):
        app = _make_app(tmp_path, SINGLE_CHECK_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        output = format_check_results_json(results)
        parsed = json.loads(output)
        assert parsed[0]["problems"] == []
        assert parsed[0]["problems"] is not None

    def test_problems_with_content(self, tmp_path):
        impls = {
            "only": lambda ctx: fail_outcome("broken", "issue 1", "issue 2"),
        }
        app = _make_app(tmp_path, SINGLE_CHECK_TOML, impls=impls)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        output = format_check_results_json(results)
        parsed = json.loads(output)
        assert parsed[0]["problems"] == [
            {"severity": "error", "text": "issue 1"},
            {"severity": "error", "text": "issue 2"},
        ]

    def test_empty_results(self):
        output = format_check_results_json([])
        parsed = json.loads(output)
        assert parsed == []

    def test_no_trailing_newline(self, tmp_path):
        app = _make_app(tmp_path, SINGLE_CHECK_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        output = format_check_results_json(results)
        assert not output.endswith("\n")


SCOPED_CHECK_TOML = """\
app = "testapp"

[checks.scoped-check]
description = "Checks scoped-check"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
scope = "changelog"
"""


class TestSetScopeAdapter:
    """Tests for App.set_scope_adapter."""

    def test_stores_adapter(self, tmp_path):
        app = _make_app(tmp_path, SCOPED_CHECK_TOML)
        assert app._scope_adapter is None

        def my_adapter(ctx, scope):
            return ctx

        app.set_scope_adapter(my_adapter)
        assert app._scope_adapter is my_adapter

    def test_adapter_called_during_run_checks(self, tmp_path):
        app = _make_app(tmp_path, SCOPED_CHECK_TOML)
        adapter_calls = []

        def adapter(ctx, scope):
            adapter_calls.append(scope)
            return ctx

        app.set_scope_adapter(adapter)

        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = app.run_checks(ctx, run_all=True)
        assert exit_code == 0
        assert len(adapter_calls) == 1
        assert adapter_calls[0] == "changelog"

    def test_adapter_returning_skip_check(self, tmp_path):
        impl_called = []

        def impl(ctx):
            impl_called.append(True)
            return pass_outcome("should not run")

        app = _make_app(tmp_path, SCOPED_CHECK_TOML, impls={"scoped-check": impl})

        def adapter(ctx, scope):
            return SkipCheck("adapter skipped")

        app.set_scope_adapter(adapter)

        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = app.run_checks(ctx, run_all=True)
        assert exit_code == 0
        assert results[0].status == "skip"
        assert "adapter skipped" in results[0].message
        assert len(impl_called) == 0

    def test_adapter_returning_invalid_context_raises(self, tmp_path):
        """A non-SkipCheck return that is not a valid CheckContext is a hard error.

        The adapter contract is: return a SkipCheck OR an object satisfying the
        CheckContext protocol (i.e. exposing a project_root attribute). Anything
        else -- here an int -- must be rejected loudly instead of being silently
        passed to the check impl as a bogus context.
        """
        impl_called = []

        def impl(ctx):
            impl_called.append(True)
            return pass_outcome("should not run")

        app = _make_app(tmp_path, SCOPED_CHECK_TOML, impls={"scoped-check": impl})

        def adapter(ctx, scope):
            return 42  # neither a SkipCheck nor a CheckContext

        app.set_scope_adapter(adapter)

        ctx = SimpleContext(project_root=tmp_path)
        with pytest.raises(TypeError, match="scope adapter"):
            app.run_checks(ctx, run_all=True)
        assert len(impl_called) == 0

    def test_adapter_returning_object_without_project_root_raises(self, tmp_path):
        """An object lacking project_root does not satisfy the CheckContext protocol."""

        class NotAContext:
            pass

        app = _make_app(tmp_path, SCOPED_CHECK_TOML)

        def adapter(ctx, scope):
            return NotAContext()

        app.set_scope_adapter(adapter)

        ctx = SimpleContext(project_root=tmp_path)
        with pytest.raises(TypeError, match="project_root"):
            app.run_checks(ctx, run_all=True)


PARTITION_TOML = """\
app = "testapp"

[checks.pure-a]
description = "Checks pure-a"
subject = "quality"
tags = ["p"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.net-b]
description = "Checks net-b"
subject = "quality"
tags = ["p"]
severity = "error"
fast = true
pure = true
needs_network = true
depends_on = []

[checks.impure-c]
description = "Checks impure-c"
subject = "quality"
tags = ["p"]
severity = "error"
fast = true
pure = false
needs_network = false
depends_on = []

[checks.dep-on-impure]
description = "Checks dep-on-impure"
subject = "quality"
tags = ["p"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = ["impure-c"]

[checks.dep-on-pure]
description = "Checks dep-on-pure"
subject = "quality"
tags = ["p"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = ["pure-a"]
"""


class TestRunChecksPurityPartition:
    """The purity partition: pure_only executes pure/non-network checks and
    lists the rest without executing them or touching the exit code."""

    def test_executes_pure_lists_impure(self, tmp_path):
        app = _make_app(tmp_path, PARTITION_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, impure_listed, exit_code = app.run_checks(
            ctx, run_all=True, pure_only=True,
        )
        assert exit_code == 0  # listed checks contribute no exit code
        executed = {r.name for r in results}
        assert executed == {"pure-a", "dep-on-pure"}
        for r in results:
            assert r.status == "pass"
        assert set(impure_listed) == {"net-b", "impure-c", "dep-on-impure"}
        # Listed checks must not leak into results (outcome vocabulary stays pure)
        assert not (executed & set(impure_listed))

    def test_pure_depending_on_impure_is_listed(self, tmp_path):
        ran = []

        impls = {}
        for name in ("pure-a", "net-b", "impure-c", "dep-on-impure", "dep-on-pure"):
            def make(n):
                def impl(ctx):
                    ran.append(n)
                    return pass_outcome(f"{n} OK")
                return impl
            impls[name] = make(name)

        app = _make_app(tmp_path, PARTITION_TOML, impls=impls)
        ctx = SimpleContext(project_root=tmp_path)
        _, impure_listed, _ = app.run_checks(ctx, run_all=True, pure_only=True)
        # dep-on-impure is pure but depends on the listed impure-c, so it cannot
        # verify its precondition and joins the listing instead of executing.
        assert "dep-on-impure" not in ran
        assert "dep-on-impure" in impure_listed

    def test_partition_off_is_unchanged(self, tmp_path):
        app = _make_app(tmp_path, PARTITION_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, impure_listed, exit_code = app.run_checks(ctx, run_all=True)
        assert exit_code == 0
        assert len(results) == 5  # every check executes
        assert impure_listed == []

    def test_dry_run_runs_the_pure_partition_and_plans_the_rest(self, tmp_path):
        """The check command's --dry-run IS the purity partition: the pure
        checks execute and the remainder is rendered as the would-run plan,
        each entry keeping its purity annotation."""
        ran = []

        impls = {}
        for name in ("pure-a", "net-b", "impure-c", "dep-on-impure", "dep-on-pure"):
            def make(n):
                def impl(ctx):
                    ran.append(n)
                    return pass_outcome(f"{n} OK")
                return impl
            impls[name] = make(name)

        app = _make_app(tmp_path, PARTITION_TOML, impls=impls)
        app.set_check_context(lambda: SimpleContext(project_root=tmp_path))
        result = app.test(["--dry-run", "check", "--all"])
        assert result.exit_code == 0
        assert sorted(ran) == ["dep-on-pure", "pure-a"]
        assert "PASS  pure-a" in result.stdout
        assert "Would run 3 checks:" in result.stdout
        # dep-on-impure is pure but joins the listing (its dependency did not
        # run), so the annotation still distinguishes the two reasons.
        assert "dep-on-impure (depends on: impure-c) [pure]" in result.stdout
        assert "[impure]" in result.stdout

    def test_failed_pure_dependency_cascades_over_listing(self, tmp_path):
        """A genuinely FAILED pure dependency cascade-skips its pure dependent
        even under pure_only -- the failed-dependency cascade takes precedence
        over the purity listing (the dependent is skipped, not listed)."""
        impls = {"pure-a": lambda ctx: fail_outcome("pure-a failed", "boom")}
        app = _make_app(tmp_path, PARTITION_TOML, impls=impls)
        ctx = SimpleContext(project_root=tmp_path)
        results, impure_listed, exit_code = app.run_checks(
            ctx, run_all=True, pure_only=True,
        )
        by_name = {r.name: r for r in results}
        assert by_name["pure-a"].status == "fail"
        # dep-on-pure is pure and its dep FAILED -> cascade-skip wins over listing.
        assert by_name["dep-on-pure"].status == "skip"
        assert "dep-on-pure" not in impure_listed
        assert exit_code == 1

    def test_listed_check_contributes_no_exit_code(self, tmp_path):
        """A check listed (not executed) under pure_only contributes NOTHING to
        the exit code -- even an impl that would fail never runs, so it cannot
        gate the run."""
        ran: list[str] = []

        def impure_fail(ctx):
            ran.append("impure-c")
            return fail_outcome("would fail", "nope")

        app = _make_app(tmp_path, PARTITION_TOML, impls={"impure-c": impure_fail})
        ctx = SimpleContext(project_root=tmp_path)
        results, impure_listed, exit_code = app.run_checks(
            ctx, run_all=True, pure_only=True,
        )
        assert "impure-c" in impure_listed
        assert "impure-c" not in ran  # listed checks never execute
        assert exit_code == 0  # a listed (unexecuted) check cannot gate


# ---------------------------------------------------------------------------
# Verdict-inert notes channel + honest --verbose (per-check notes, duration,
# trailing count summary). Notes are recorded unconditionally on EVERY outcome
# (including pass), surface only under --verbose and in JSON, and never affect
# status, gating, or exit codes.
# ---------------------------------------------------------------------------

import re

from strictcli import ErrorReporter, WarnReporter


def _pass_with_notes(message: str, *notes: str):
    r = ErrorReporter()
    for n in notes:
        r.note(n)
    return r.passed(message)


def _found_with_notes(message: str, problem: str, *notes: str):
    r = ErrorReporter()
    for n in notes:
        r.note(n)
    r.error(problem)
    return r.found(message)


class TestReporterNote:
    def test_note_allowed_on_pass_and_carried(self):
        outcome = _pass_with_notes("all good", "cached result", "took a shortcut")
        assert outcome.status == "pass"
        assert outcome.notes == ("cached result", "took a shortcut")

    def test_note_allowed_on_found(self):
        outcome = _found_with_notes("bad", "the problem", "an aside")
        assert outcome.status == "fail"
        assert outcome.notes == ("an aside",)

    def test_note_allowed_on_skip(self):
        r = ErrorReporter()
        r.note("context note")
        outcome = r.skipped("nothing to do")
        assert outcome.status == "skip"
        assert outcome.notes == ("context note",)

    def test_note_available_on_warn_reporter(self):
        r = WarnReporter()
        r.note("warn-reporter note")
        outcome = r.passed("fine")
        assert outcome.notes == ("warn-reporter note",)

    def test_empty_note_rejected(self):
        r = ErrorReporter()
        with pytest.raises(ValueError, match="note text must be a non-empty string"):
            r.note("   ")

    def test_notes_do_not_defeat_passed_problem_seal(self):
        # A note must NOT let a check with problems claim it passed -- the sealed
        # invariant (problems + passed => hard error) stays exact.
        r = ErrorReporter()
        r.note("informational")
        r.warn("a real problem")
        with pytest.raises(ValueError, match="cannot pass"):
            r.passed("nope")

    def test_notes_do_not_defeat_skipped_problem_seal(self):
        r = ErrorReporter()
        r.note("informational")
        r.error("a real problem")
        with pytest.raises(ValueError, match="cannot skip"):
            r.skipped("nope")


class TestNotesFormatting:
    def test_notes_hidden_in_normal_mode(self):
        results = [CheckRunResult(name="a", outcome=_pass_with_notes("ok", "hidden note"))]
        out = format_check_results(results, verbose=False)
        assert "[note]" not in out
        assert "hidden note" not in out

    def test_notes_shown_in_verbose_including_on_pass(self):
        results = [CheckRunResult(name="a", outcome=_pass_with_notes("ok", "visible note"))]
        out = format_check_results(results, verbose=True)
        assert "        [note] visible note" in out

    def test_normal_mode_output_unchanged(self):
        # Pin the exact normal-mode output. Notes/duration/summary must not leak in.
        results = [
            CheckRunResult(name="alpha", outcome=_pass_with_notes("alpha OK", "note")),
            CheckRunResult(name="beta", outcome=pass_outcome("beta OK")),
        ]
        out = format_check_results(results, verbose=False)
        assert out == "PASS  alpha    alpha OK\nPASS  beta     beta OK"

    def test_verbose_renders_duration_in_stable_shape(self):
        results = [CheckRunResult(name="a", outcome=pass_outcome("ok"), duration_ms=12)]
        out = format_check_results(results, verbose=True)
        assert "(12ms)" in out
        assert re.search(r"\(\d+ms\)", out)

    def test_verbose_trailing_count_summary(self):
        results = [
            CheckRunResult(name="a", outcome=pass_outcome("ok")),
            CheckRunResult(name="b", outcome=fail_outcome("bad", "x")),
            CheckRunResult(name="c", outcome=warn_outcome("hmm", "y")),
            CheckRunResult(name="d", outcome=skip_outcome("skip")),
        ]
        out = format_check_results(results, verbose=True)
        assert out.endswith("1 passed / 1 failed / 1 warned / 1 skipped")
        # Summary is preceded by a blank line.
        assert "\n\n1 passed / 1 failed / 1 warned / 1 skipped" in out

    def test_json_always_includes_notes_and_duration(self):
        results = [
            CheckRunResult(name="a", outcome=_pass_with_notes("ok", "n1"), duration_ms=7),
            CheckRunResult(name="b", outcome=pass_outcome("ok2")),
        ]
        parsed = json.loads(format_check_results_json(results))
        assert parsed[0]["notes"] == ["n1"]
        assert parsed[0]["duration_ms"] == 7
        # Additive keys are ALWAYS present, even with no notes.
        assert parsed[1]["notes"] == []
        assert "duration_ms" in parsed[1]


class TestNotesEndToEnd:
    def test_notes_do_not_affect_exit_code_or_status(self, tmp_path):
        def noted_pass(ctx):
            return _pass_with_notes("all good", "a note that must not gate")

        app = _make_app(tmp_path, TWO_CHECKS_TOML, impls={"alpha": noted_pass})
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = app.run_checks(ctx, run_all=True)
        assert exit_code == 0
        by_name = {r.name: r for r in results}
        assert by_name["alpha"].status == "pass"
        assert by_name["alpha"].notes == ("a note that must not gate",)

    def test_duration_field_present_after_run(self, tmp_path):
        app = _make_app(tmp_path, TWO_CHECKS_TOML)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, _ = app.run_checks(ctx, run_all=True)
        for r in results:
            assert isinstance(r.duration_ms, int)
            assert r.duration_ms >= 0
