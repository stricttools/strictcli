"""Tests for check runner: DAG resolution, execution, and filtering."""

from dataclasses import dataclass
from pathlib import Path

import pytest

import strictcli
from strictcli import (
    CheckContext,
    SkipCheck,
    _CheckDef,
    _filter_checks,
    _match_tag_expr,
    _resolve_check_order,
    _run_checks,
)
from conftest import pass_outcome, fail_outcome, warn_outcome, skip_outcome


@dataclass
class SimpleContext:
    project_root: Path


def _make_check_def(
    name: str,
    tags: list[str] | None = None,
    severity: str = "error",
    depends_on: list[str] | None = None,
    impl=None,
) -> _CheckDef:
    return _CheckDef(
        name=name,
        tags=tags or ["default"],
        severity=severity,
        fast=True,
        pure=True,
        needs_network=False,
        depends_on=depends_on or [],
        impl=impl,
    )


class TestResolveCheckOrder:
    def test_single_check_no_deps(self):
        defs = {"aa": _make_check_def("aa")}
        order = _resolve_check_order(defs, {"aa"})
        assert order == ["aa"]

    def test_dependency_chain(self):
        defs = {
            "aa": _make_check_def("aa", depends_on=["bb"]),
            "bb": _make_check_def("bb", depends_on=["cc"]),
            "cc": _make_check_def("cc"),
        }
        order = _resolve_check_order(defs, {"aa", "bb", "cc"})
        assert order.index("cc") < order.index("bb") < order.index("aa")

    def test_dependency_pull_in(self):
        defs = {
            "aa": _make_check_def("aa", depends_on=["bb"]),
            "bb": _make_check_def("bb"),
        }
        # Only select "aa" -- "bb" should be pulled in
        order = _resolve_check_order(defs, {"aa"})
        assert "bb" in order
        assert "aa" in order
        assert order.index("bb") < order.index("aa")

    def test_cycle_detection(self):
        defs = {
            "aa": _make_check_def("aa", depends_on=["bb"]),
            "bb": _make_check_def("bb", depends_on=["aa"]),
        }
        with pytest.raises(ValueError, match="check dependency cycle"):
            _resolve_check_order(defs, {"aa", "bb"})

    def test_three_node_cycle(self):
        defs = {
            "aa": _make_check_def("aa", depends_on=["bb"]),
            "bb": _make_check_def("bb", depends_on=["cc"]),
            "cc": _make_check_def("cc", depends_on=["aa"]),
        }
        with pytest.raises(ValueError, match="check dependency cycle"):
            _resolve_check_order(defs, {"aa", "bb", "cc"})

    def test_independent_checks_all_returned(self):
        defs = {
            "aa": _make_check_def("aa"),
            "bb": _make_check_def("bb"),
            "cc": _make_check_def("cc"),
        }
        order = _resolve_check_order(defs, {"aa", "bb", "cc"})
        assert set(order) == {"aa", "bb", "cc"}

    def test_diamond_dependency(self):
        # d depends on b and c, both depend on a
        defs = {
            "aa": _make_check_def("aa"),
            "bb": _make_check_def("bb", depends_on=["aa"]),
            "cc": _make_check_def("cc", depends_on=["aa"]),
            "dd": _make_check_def("dd", depends_on=["bb", "cc"]),
        }
        order = _resolve_check_order(defs, {"dd"})
        assert set(order) == {"aa", "bb", "cc", "dd"}
        assert order.index("aa") < order.index("bb")
        assert order.index("aa") < order.index("cc")
        assert order.index("bb") < order.index("dd")
        assert order.index("cc") < order.index("dd")


class TestRunChecks:
    def _make_app_with_checks(self, check_defs, tmp_path, monkeypatch):
        """Build an App with pre-populated check definitions."""
        # Write a minimal TOML so the app discovers checks
        toml_lines = ['app = "testapp"', ""]
        for name, cdef in check_defs.items():
            tags_str = ", ".join(f'"{t}"' for t in cdef.tags)
            deps_str = ", ".join(f'"{d}"' for d in cdef.depends_on)
            toml_lines.append(f"[checks.{name}]")
            toml_lines.append(f"tags = [{tags_str}]")
            toml_lines.append(f'severity = "{cdef.severity}"')
            toml_lines.append(f"fast = {'true' if cdef.fast else 'false'}")
            toml_lines.append(f"pure = {'true' if cdef.pure else 'false'}")
            toml_lines.append(f"needs_network = {'true' if cdef.needs_network else 'false'}")
            toml_lines.append(f"depends_on = [{deps_str}]")
            toml_lines.append(f'description = "Checks {name}"')
            toml_lines.append('subject = "quality"')
            toml_lines.append("")

        toml_file = tmp_path / "checks.toml"
        toml_file.write_text("\n".join(toml_lines))

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )
        # Inject the impl functions from our check_defs
        for name, cdef in check_defs.items():
            if cdef.impl is not None:
                app._check_defs[name].impl = cdef.impl
        return app

    def test_single_passing_check(self, tmp_path, monkeypatch):
        defs = {
            "aa": _make_check_def(
                "aa",
                impl=lambda ctx: pass_outcome("All good"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = _run_checks(app._check_defs, ["aa"], ctx)
        assert exit_code == 0
        assert len(results) == 1
        assert results[0][0] == "aa"
        assert results[0][1].status == "pass"

    def test_single_failing_check(self, tmp_path, monkeypatch):
        defs = {
            "aa": _make_check_def(
                "aa",
                impl=lambda ctx: fail_outcome("Broken"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = _run_checks(app._check_defs, ["aa"], ctx)
        assert exit_code == 1
        assert results[0][1].status == "fail"

    def test_dependency_chain_passes(self, tmp_path, monkeypatch):
        defs = {
            "bb": _make_check_def(
                "bb",
                impl=lambda ctx: pass_outcome("B OK"),
            ),
            "aa": _make_check_def(
                "aa",
                depends_on=["bb"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"aa", "bb"})
        results, _, exit_code = _run_checks(app._check_defs, order, ctx)
        assert exit_code == 0
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["aa"] == "pass"
        assert statuses["bb"] == "pass"

    def test_dependency_failure_skips_dependent(self, tmp_path, monkeypatch):
        defs = {
            "bb": _make_check_def(
                "bb",
                impl=lambda ctx: fail_outcome("B failed"),
            ),
            "aa": _make_check_def(
                "aa",
                depends_on=["bb"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"aa", "bb"})
        results, _, exit_code = _run_checks(app._check_defs, order, ctx)
        assert exit_code == 1
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["bb"] == "fail"
        assert statuses["aa"] == "skip"
        # Verify skip message references the failed dependency
        skip_result = {name: o for name, o, _ in results}["aa"]
        assert 'dependency "bb" failed' in skip_result.message

    def test_transitive_skip(self, tmp_path, monkeypatch):
        defs = {
            "cc": _make_check_def(
                "cc",
                impl=lambda ctx: fail_outcome("C failed"),
            ),
            "bb": _make_check_def(
                "bb",
                depends_on=["cc"],
                impl=lambda ctx: pass_outcome("B OK"),
            ),
            "aa": _make_check_def(
                "aa",
                depends_on=["bb"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"aa", "bb", "cc"})
        results, _, exit_code = _run_checks(app._check_defs, order, ctx)
        assert exit_code == 1
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["cc"] == "fail"
        assert statuses["bb"] == "skip"
        assert statuses["aa"] == "skip"

    def test_warn_exits_nonzero(self, tmp_path, monkeypatch):
        defs = {
            "aa": _make_check_def(
                "aa",
                severity="warn",
                impl=lambda ctx: warn_outcome("Watch out"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = _run_checks(app._check_defs, ["aa"], ctx)
        assert exit_code == 1
        assert results[0][1].status == "warn"

    def test_warn_dependency_runs_dependent(self, tmp_path, monkeypatch):
        # A warn satisfies a dependency: only fail (or cascade-skip) skips
        # dependents. The warning makes the run exit nonzero, but the
        # dependent must run.
        defs = {
            "bb": _make_check_def(
                "bb",
                impl=lambda ctx: warn_outcome("Warning"),
            ),
            "aa": _make_check_def(
                "aa",
                depends_on=["bb"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"aa", "bb"})
        results, _, exit_code = _run_checks(app._check_defs, order, ctx)
        assert exit_code == 1
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["bb"] == "warn"
        assert statuses["aa"] == "pass"

    def test_warn_dependency_transitive_dependents_run(self, tmp_path, monkeypatch):
        # warn -> dependent -> transitive dependent: the whole chain runs.
        defs = {
            "cc": _make_check_def(
                "cc",
                impl=lambda ctx: warn_outcome("Warning"),
            ),
            "bb": _make_check_def(
                "bb",
                depends_on=["cc"],
                impl=lambda ctx: pass_outcome("B OK"),
            ),
            "aa": _make_check_def(
                "aa",
                depends_on=["bb"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"aa", "bb", "cc"})
        results, _, exit_code = _run_checks(app._check_defs, order, ctx)
        assert exit_code == 1
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["cc"] == "warn"
        assert statuses["bb"] == "pass"
        assert statuses["aa"] == "pass"

    def test_skip_from_scope_adapter_runs_dependent(self, tmp_path, monkeypatch):
        # When the scope adapter skips a check via SkipCheck, the skip must also
        # satisfy dependencies (no cascade-skip) -- an explicit skip is not a
        # failure. (The adapter can no longer mint a warn; that path is gone.)
        defs = {
            "bb": _make_check_def(
                "bb",
                impl=lambda ctx: pass_outcome("unused"),
            ),
            "aa": _make_check_def(
                "aa",
                depends_on=["bb"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        # Give "bb" a scope so the adapter is consulted for it.
        app._check_defs["bb"].scope = "some-scope"

        def adapter(context, scope):
            return SkipCheck("adapter skipped b")

        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"aa", "bb"})
        results, _, exit_code = _run_checks(
            app._check_defs, order, ctx, scope_adapter=adapter
        )
        assert exit_code == 0
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["bb"] == "skip"
        assert statuses["aa"] == "pass"

    def test_warn_does_not_cascade(self, tmp_path, monkeypatch):
        defs = {
            "bb": _make_check_def(
                "bb",
                impl=lambda ctx: warn_outcome("Warning"),
            ),
            "aa": _make_check_def(
                "aa",
                depends_on=["bb"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"aa", "bb"})
        results, _, exit_code = _run_checks(app._check_defs, order, ctx)
        assert exit_code == 1
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["bb"] == "warn"
        assert statuses["aa"] == "pass"


class TestNonMintedOutcome:
    def test_impl_returning_non_outcome_raises(self):
        # Belt-and-braces: an impl that returns something not minted by a
        # reporter is a hard error at the runner.
        defs = {
            "aa": _make_check_def("aa", impl=lambda ctx: "not an outcome"),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        with pytest.raises(TypeError, match="not an outcome minted by its reporter"):
            _run_checks(defs, ["aa"], ctx)


class TestRaisingImplContained:
    """A raising impl is contained and reported as that check's own failure."""

    @staticmethod
    def _raiser(exc):
        def impl(ctx):
            raise exc
        return impl

    def test_raising_impl_fails_only_itself(self):
        defs = {
            "aa": _make_check_def("aa", impl=self._raiser(ValueError("boom"))),
            "bb": _make_check_def("bb", impl=lambda ctx: pass_outcome("b ok")),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["aa", "bb"], ctx)
        assert exit_code == 1
        statuses = {name: outcome.status for name, outcome, _ in results}
        assert statuses == {"aa": "fail", "bb": "pass"}
        messages = {name: outcome.message for name, outcome, _ in results}
        assert messages["aa"] == 'check "aa" aborted with ValueError: boom'
        assert messages["bb"] == "b ok"

    def test_contained_failure_carries_an_error_problem(self):
        defs = {"aa": _make_check_def("aa", impl=self._raiser(RuntimeError("nope")))}
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, _ = _run_checks(defs, ["aa"], ctx)
        _, outcome, _ = results[0]
        assert [(p.severity, p.text) for p in outcome.problems] == [
            ("error", 'check "aa" aborted with RuntimeError: nope'),
        ]

    def test_empty_exception_message_drops_the_colon(self):
        defs = {"aa": _make_check_def("aa", impl=self._raiser(ValueError()))}
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, _ = _run_checks(defs, ["aa"], ctx)
        assert results[0][1].message == 'check "aa" aborted with ValueError'

    def test_contained_failure_cascade_skips_dependents(self):
        defs = {
            "aa": _make_check_def("aa", impl=self._raiser(ValueError("boom"))),
            "bb": _make_check_def(
                "bb", depends_on=["aa"], impl=lambda ctx: pass_outcome("b ok"),
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["aa", "bb"], ctx)
        assert exit_code == 1
        statuses = {name: outcome.status for name, outcome, _ in results}
        assert statuses == {"aa": "fail", "bb": "skip"}

    def test_a_warn_check_that_raises_still_fails(self):
        # A warn check's findings are warnings; a broken check still fails.
        defs = {
            "aa": _make_check_def(
                "aa", severity="warn", impl=self._raiser(ValueError("boom")),
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["aa"], ctx)
        assert exit_code == 1
        assert results[0][1].status == "fail"

    def test_keyboard_interrupt_is_not_contained(self):
        # BaseException is the operator ending the process, not a broken check.
        defs = {"aa": _make_check_def("aa", impl=self._raiser(KeyboardInterrupt()))}
        ctx = SimpleContext(project_root=Path("/tmp"))
        with pytest.raises(KeyboardInterrupt):
            _run_checks(defs, ["aa"], ctx)


class TestFilterChecks:
    def setup_method(self):
        self.defs = {
            "lint-code": _make_check_def("lint-code", tags=["code", "fast"]),
            "lint-docs": _make_check_def("lint-docs", tags=["docs", "fast"]),
            "check-deps": _make_check_def("check-deps", tags=["deps", "release"]),
            "check-changelog": _make_check_def("check-changelog", tags=["release"]),
        }

    def test_run_all(self):
        result = _filter_checks(self.defs, tag_expr=None, name_glob=None, run_all=True)
        assert result == set(self.defs.keys())

    def test_filter_by_tag(self):
        result = _filter_checks(self.defs, tag_expr="release", name_glob=None, run_all=False)
        assert result == {"check-deps", "check-changelog"}

    def test_filter_by_tag_complex(self):
        result = _filter_checks(self.defs, tag_expr="fast & code", name_glob=None, run_all=False)
        assert result == {"lint-code"}

    def test_filter_by_name_glob(self):
        result = _filter_checks(self.defs, tag_expr=None, name_glob="lint-*", run_all=False)
        assert result == {"lint-code", "lint-docs"}

    def test_filter_by_name_glob_exact(self):
        result = _filter_checks(self.defs, tag_expr=None, name_glob="lint-code", run_all=False)
        assert result == {"lint-code"}

    def test_filter_combined_intersection(self):
        # "fast" matches lint-code, lint-docs; "lint-*" matches lint-code, lint-docs
        # Intersection: lint-code, lint-docs
        result = _filter_checks(self.defs, tag_expr="fast", name_glob="lint-*", run_all=False)
        assert result == {"lint-code", "lint-docs"}

    def test_filter_combined_narrow(self):
        # "code" matches lint-code; "lint-*" matches lint-code, lint-docs
        # Intersection: lint-code only
        result = _filter_checks(self.defs, tag_expr="code", name_glob="lint-*", run_all=False)
        assert result == {"lint-code"}

    def test_no_filters_returns_empty(self):
        result = _filter_checks(self.defs, tag_expr=None, name_glob=None, run_all=False)
        assert result == set()

    def test_filter_no_matches(self):
        result = _filter_checks(self.defs, tag_expr="nonexistent", name_glob=None, run_all=False)
        assert result == set()

    def test_dependency_pull_in_with_filter(self):
        """Test that dependency pull-in works with filtered checks.

        If "aa" tagged "release" depends on "bb" (not tagged "release"),
        filtering by "release" selects "aa", and _resolve_check_order
        pulls "bb" in.
        """
        defs = {
            "aa": _make_check_def("aa", tags=["release"], depends_on=["bb"]),
            "bb": _make_check_def("bb", tags=["infra"]),
        }
        selected = _filter_checks(defs, tag_expr="release", name_glob=None, run_all=False)
        assert selected == {"aa"}

        order = _resolve_check_order(defs, selected)
        assert "bb" in order
        assert "aa" in order
        assert order.index("bb") < order.index("aa")


class TestScopeAdapter:
    """Tests for scope adapter integration in _run_checks."""

    def test_no_scope_no_adapter_call(self):
        """When check has no scope, adapter is never called."""
        adapter_calls = []

        def adapter(ctx, scope):
            adapter_calls.append(scope)
            return ctx

        defs = {
            "aa": _make_check_def(
                "aa",
                impl=lambda ctx: pass_outcome("ok"),
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["aa"], ctx, False, scope_adapter=adapter)
        assert exit_code == 0
        assert len(adapter_calls) == 0

    def test_scope_without_adapter_skips_adaptation(self):
        """When check has scope but no adapter is set, impl is called normally."""
        defs = {
            "aa": _CheckDef(
                name="aa", tags=["default"], severity="error",
                fast=True, pure=True, needs_network=False,
                depends_on=[], scope="changelog",
                impl=lambda ctx: pass_outcome("ok"),
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["aa"], ctx, False, scope_adapter=None)
        assert exit_code == 0
        assert results[0][1].status == "pass"

    def test_scope_adapter_transforms_context(self):
        """Adapter returning a context replaces the context for impl."""
        @dataclass
        class ScopedContext:
            project_root: Path
            scope_value: str

        def adapter(ctx, scope):
            return ScopedContext(project_root=ctx.project_root, scope_value=scope)

        def impl(ctx):
            assert hasattr(ctx, "scope_value")
            assert ctx.scope_value == "changelog"
            return pass_outcome("scoped ok")

        defs = {
            "aa": _CheckDef(
                name="aa", tags=["default"], severity="error",
                fast=True, pure=True, needs_network=False,
                depends_on=[], scope="changelog",
                impl=impl,
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["aa"], ctx, False, scope_adapter=adapter)
        assert exit_code == 0
        assert results[0][1].status == "pass"
        assert results[0][1].message == "scoped ok"

    def test_scope_adapter_returns_skip_check(self):
        """Adapter returning SkipCheck skips the check; impl is not called."""
        impl_called = []

        def adapter(ctx, scope):
            return SkipCheck("scope not applicable")

        def impl(ctx):
            impl_called.append(True)
            return pass_outcome("should not run")

        defs = {
            "aa": _CheckDef(
                name="aa", tags=["default"], severity="error",
                fast=True, pure=True, needs_network=False,
                depends_on=[], scope="changelog",
                impl=impl,
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["aa"], ctx, False, scope_adapter=adapter)
        assert exit_code == 0
        assert results[0][1].status == "skip"
        assert "scope not applicable" in results[0][1].message
        assert len(impl_called) == 0

    def test_scope_adapter_skip_does_not_cascade(self):
        """A SkipCheck from the adapter is not a failure: dependents still run
        (an adapter can no longer fail a check -- that path is gone)."""
        b_ran = []

        def adapter(ctx, scope):
            return SkipCheck("skipping a")

        defs = {
            "aa": _CheckDef(
                name="aa", tags=["default"], severity="error",
                fast=True, pure=True, needs_network=False,
                depends_on=[], scope="changelog",
                impl=lambda ctx: pass_outcome("should not run"),
            ),
            "bb": _make_check_def(
                "bb", depends_on=["aa"],
                impl=lambda ctx: (b_ran.append(True), pass_outcome("b ok"))[1],
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["aa", "bb"], ctx, False, scope_adapter=adapter)
        assert exit_code == 0
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["aa"] == "skip"
        assert statuses["bb"] == "pass"
        assert b_ran == [True]

    def test_scope_adapter_skip_reason_surfaced(self):
        """The SkipCheck reason appears in the derived skip outcome's message."""
        def adapter(ctx, scope):
            return SkipCheck("changelog not present")

        defs = {
            "aa": _CheckDef(
                name="aa", tags=["default"], severity="warn",
                fast=True, pure=True, needs_network=False,
                depends_on=[], scope="changelog",
                impl=lambda ctx: pass_outcome("should not run"),
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["aa"], ctx, False, scope_adapter=adapter)
        assert exit_code == 0
        assert results[0][1].status == "skip"
        assert "changelog not present" in results[0][1].message

    def test_mixed_scoped_and_unscoped_checks(self):
        """Adapter is only called for checks with non-empty scope."""
        adapter_calls = []

        def adapter(ctx, scope):
            adapter_calls.append(scope)
            return ctx

        defs = {
            "scoped": _CheckDef(
                name="scoped", tags=["default"], severity="error",
                fast=True, pure=True, needs_network=False,
                depends_on=[], scope="changelog",
                impl=lambda ctx: pass_outcome("scoped ok"),
            ),
            "unscoped": _make_check_def(
                "unscoped",
                impl=lambda ctx: pass_outcome("unscoped ok"),
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(
            defs, ["scoped", "unscoped"], ctx, False, scope_adapter=adapter,
        )
        assert exit_code == 0
        assert len(adapter_calls) == 1
        assert adapter_calls[0] == "changelog"
