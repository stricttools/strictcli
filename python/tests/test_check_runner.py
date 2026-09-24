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
        defs = {"a": _make_check_def("a")}
        order = _resolve_check_order(defs, {"a"})
        assert order == ["a"]

    def test_dependency_chain(self):
        defs = {
            "a": _make_check_def("a", depends_on=["b"]),
            "b": _make_check_def("b", depends_on=["c"]),
            "c": _make_check_def("c"),
        }
        order = _resolve_check_order(defs, {"a", "b", "c"})
        assert order.index("c") < order.index("b") < order.index("a")

    def test_dependency_pull_in(self):
        defs = {
            "a": _make_check_def("a", depends_on=["b"]),
            "b": _make_check_def("b"),
        }
        # Only select "a" -- "b" should be pulled in
        order = _resolve_check_order(defs, {"a"})
        assert "b" in order
        assert "a" in order
        assert order.index("b") < order.index("a")

    def test_cycle_detection(self):
        defs = {
            "a": _make_check_def("a", depends_on=["b"]),
            "b": _make_check_def("b", depends_on=["a"]),
        }
        with pytest.raises(ValueError, match="check dependency cycle"):
            _resolve_check_order(defs, {"a", "b"})

    def test_three_node_cycle(self):
        defs = {
            "a": _make_check_def("a", depends_on=["b"]),
            "b": _make_check_def("b", depends_on=["c"]),
            "c": _make_check_def("c", depends_on=["a"]),
        }
        with pytest.raises(ValueError, match="check dependency cycle"):
            _resolve_check_order(defs, {"a", "b", "c"})

    def test_independent_checks_all_returned(self):
        defs = {
            "a": _make_check_def("a"),
            "b": _make_check_def("b"),
            "c": _make_check_def("c"),
        }
        order = _resolve_check_order(defs, {"a", "b", "c"})
        assert set(order) == {"a", "b", "c"}

    def test_diamond_dependency(self):
        # d depends on b and c, both depend on a
        defs = {
            "a": _make_check_def("a"),
            "b": _make_check_def("b", depends_on=["a"]),
            "c": _make_check_def("c", depends_on=["a"]),
            "d": _make_check_def("d", depends_on=["b", "c"]),
        }
        order = _resolve_check_order(defs, {"d"})
        assert set(order) == {"a", "b", "c", "d"}
        assert order.index("a") < order.index("b")
        assert order.index("a") < order.index("c")
        assert order.index("b") < order.index("d")
        assert order.index("c") < order.index("d")


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
            "a": _make_check_def(
                "a",
                impl=lambda ctx: pass_outcome("All good"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = _run_checks(app._check_defs, ["a"], ctx, ignore_warnings=False)
        assert exit_code == 0
        assert len(results) == 1
        assert results[0][0] == "a"
        assert results[0][1].status == "pass"

    def test_single_failing_check(self, tmp_path, monkeypatch):
        defs = {
            "a": _make_check_def(
                "a",
                impl=lambda ctx: fail_outcome("Broken"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = _run_checks(app._check_defs, ["a"], ctx, ignore_warnings=False)
        assert exit_code == 1
        assert results[0][1].status == "fail"

    def test_dependency_chain_passes(self, tmp_path, monkeypatch):
        defs = {
            "b": _make_check_def(
                "b",
                impl=lambda ctx: pass_outcome("B OK"),
            ),
            "a": _make_check_def(
                "a",
                depends_on=["b"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"a", "b"})
        results, _, exit_code = _run_checks(app._check_defs, order, ctx, ignore_warnings=False)
        assert exit_code == 0
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["a"] == "pass"
        assert statuses["b"] == "pass"

    def test_dependency_failure_skips_dependent(self, tmp_path, monkeypatch):
        defs = {
            "b": _make_check_def(
                "b",
                impl=lambda ctx: fail_outcome("B failed"),
            ),
            "a": _make_check_def(
                "a",
                depends_on=["b"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"a", "b"})
        results, _, exit_code = _run_checks(app._check_defs, order, ctx, ignore_warnings=False)
        assert exit_code == 1
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["b"] == "fail"
        assert statuses["a"] == "skip"
        # Verify skip message references the failed dependency
        skip_result = {name: o for name, o, _ in results}["a"]
        assert 'dependency "b" failed' in skip_result.message

    def test_transitive_skip(self, tmp_path, monkeypatch):
        defs = {
            "c": _make_check_def(
                "c",
                impl=lambda ctx: fail_outcome("C failed"),
            ),
            "b": _make_check_def(
                "b",
                depends_on=["c"],
                impl=lambda ctx: pass_outcome("B OK"),
            ),
            "a": _make_check_def(
                "a",
                depends_on=["b"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"a", "b", "c"})
        results, _, exit_code = _run_checks(app._check_defs, order, ctx, ignore_warnings=False)
        assert exit_code == 1
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["c"] == "fail"
        assert statuses["b"] == "skip"
        assert statuses["a"] == "skip"

    def test_warn_with_ignore_warnings_true(self, tmp_path, monkeypatch):
        defs = {
            "a": _make_check_def(
                "a",
                severity="warn",
                impl=lambda ctx: warn_outcome("Watch out"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = _run_checks(app._check_defs, ["a"], ctx, ignore_warnings=True)
        assert exit_code == 0
        assert results[0][1].status == "warn"

    def test_warn_with_ignore_warnings_false(self, tmp_path, monkeypatch):
        defs = {
            "a": _make_check_def(
                "a",
                severity="warn",
                impl=lambda ctx: warn_outcome("Watch out"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        results, _, exit_code = _run_checks(app._check_defs, ["a"], ctx, ignore_warnings=False)
        assert exit_code == 1
        assert results[0][1].status == "warn"

    def test_warn_dependency_runs_dependent(self, tmp_path, monkeypatch):
        # A warn satisfies a dependency: only fail (or cascade-skip) skips
        # dependents. The warn still makes the run exit non-zero when
        # ignore_warnings=False, but the dependent must run.
        defs = {
            "b": _make_check_def(
                "b",
                impl=lambda ctx: warn_outcome("Warning"),
            ),
            "a": _make_check_def(
                "a",
                depends_on=["b"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"a", "b"})
        results, _, exit_code = _run_checks(app._check_defs, order, ctx, ignore_warnings=False)
        assert exit_code == 1
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["b"] == "warn"
        assert statuses["a"] == "pass"

    def test_warn_dependency_transitive_dependents_run(self, tmp_path, monkeypatch):
        # warn -> dependent -> transitive dependent: the whole chain runs.
        defs = {
            "c": _make_check_def(
                "c",
                impl=lambda ctx: warn_outcome("Warning"),
            ),
            "b": _make_check_def(
                "b",
                depends_on=["c"],
                impl=lambda ctx: pass_outcome("B OK"),
            ),
            "a": _make_check_def(
                "a",
                depends_on=["b"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"a", "b", "c"})
        results, _, exit_code = _run_checks(app._check_defs, order, ctx, ignore_warnings=False)
        assert exit_code == 1
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["c"] == "warn"
        assert statuses["b"] == "pass"
        assert statuses["a"] == "pass"

    def test_skip_from_scope_adapter_runs_dependent(self, tmp_path, monkeypatch):
        # When the scope adapter skips a check via SkipCheck, the skip must also
        # satisfy dependencies (no cascade-skip) -- an explicit skip is not a
        # failure. (The adapter can no longer mint a warn; that path is gone.)
        defs = {
            "b": _make_check_def(
                "b",
                impl=lambda ctx: pass_outcome("unused"),
            ),
            "a": _make_check_def(
                "a",
                depends_on=["b"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        # Give "b" a scope so the adapter is consulted for it.
        app._check_defs["b"].scope = "some-scope"

        def adapter(context, scope):
            return SkipCheck("adapter skipped b")

        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"a", "b"})
        results, _, exit_code = _run_checks(
            app._check_defs, order, ctx, ignore_warnings=False, scope_adapter=adapter
        )
        assert exit_code == 0
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["b"] == "skip"
        assert statuses["a"] == "pass"

    def test_warn_does_not_cascade_when_ignored(self, tmp_path, monkeypatch):
        defs = {
            "b": _make_check_def(
                "b",
                impl=lambda ctx: warn_outcome("Warning"),
            ),
            "a": _make_check_def(
                "a",
                depends_on=["b"],
                impl=lambda ctx: pass_outcome("A OK"),
            ),
        }
        app = self._make_app_with_checks(defs, tmp_path, monkeypatch)
        ctx = SimpleContext(project_root=tmp_path)
        order = _resolve_check_order(app._check_defs, {"a", "b"})
        results, _, exit_code = _run_checks(app._check_defs, order, ctx, ignore_warnings=True)
        assert exit_code == 0
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["b"] == "warn"
        assert statuses["a"] == "pass"


class TestNonMintedOutcome:
    def test_impl_returning_non_outcome_raises(self):
        # Belt-and-braces: an impl that returns something not minted by a
        # reporter is a hard error at the runner.
        defs = {
            "a": _make_check_def("a", impl=lambda ctx: "not an outcome"),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        with pytest.raises(TypeError, match="not an outcome minted by its reporter"):
            _run_checks(defs, ["a"], ctx, False)


class TestRaisingImplContained:
    """A raising impl is contained and reported as that check's own failure."""

    @staticmethod
    def _raiser(exc):
        def impl(ctx):
            raise exc
        return impl

    def test_raising_impl_fails_only_itself(self):
        defs = {
            "a": _make_check_def("a", impl=self._raiser(ValueError("boom"))),
            "b": _make_check_def("b", impl=lambda ctx: pass_outcome("b ok")),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["a", "b"], ctx, False)
        assert exit_code == 1
        statuses = {name: outcome.status for name, outcome, _ in results}
        assert statuses == {"a": "fail", "b": "pass"}
        messages = {name: outcome.message for name, outcome, _ in results}
        assert messages["a"] == 'check "a" aborted with ValueError: boom'
        assert messages["b"] == "b ok"

    def test_contained_failure_carries_an_error_problem(self):
        defs = {"a": _make_check_def("a", impl=self._raiser(RuntimeError("nope")))}
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, _ = _run_checks(defs, ["a"], ctx, False)
        _, outcome, _ = results[0]
        assert [(p.severity, p.text) for p in outcome.problems] == [
            ("error", 'check "a" aborted with RuntimeError: nope'),
        ]

    def test_empty_exception_message_drops_the_colon(self):
        defs = {"a": _make_check_def("a", impl=self._raiser(ValueError()))}
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, _ = _run_checks(defs, ["a"], ctx, False)
        assert results[0][1].message == 'check "a" aborted with ValueError'

    def test_contained_failure_cascade_skips_dependents(self):
        defs = {
            "a": _make_check_def("a", impl=self._raiser(ValueError("boom"))),
            "b": _make_check_def(
                "b", depends_on=["a"], impl=lambda ctx: pass_outcome("b ok"),
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["a", "b"], ctx, False)
        assert exit_code == 1
        statuses = {name: outcome.status for name, outcome, _ in results}
        assert statuses == {"a": "fail", "b": "skip"}

    def test_a_warn_check_that_raises_still_fails(self):
        # --ignore-warnings forgives warn RESULTS, never a broken check.
        defs = {
            "a": _make_check_def(
                "a", severity="warn", impl=self._raiser(ValueError("boom")),
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["a"], ctx, True)
        assert exit_code == 1
        assert results[0][1].status == "fail"

    def test_keyboard_interrupt_is_not_contained(self):
        # BaseException is the operator ending the process, not a broken check.
        defs = {"a": _make_check_def("a", impl=self._raiser(KeyboardInterrupt()))}
        ctx = SimpleContext(project_root=Path("/tmp"))
        with pytest.raises(KeyboardInterrupt):
            _run_checks(defs, ["a"], ctx, False)


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

        If "a" tagged "release" depends on "b" (not tagged "release"),
        filtering by "release" selects "a", and _resolve_check_order
        pulls "b" in.
        """
        defs = {
            "a": _make_check_def("a", tags=["release"], depends_on=["b"]),
            "b": _make_check_def("b", tags=["infra"]),
        }
        selected = _filter_checks(defs, tag_expr="release", name_glob=None, run_all=False)
        assert selected == {"a"}

        order = _resolve_check_order(defs, selected)
        assert "b" in order
        assert "a" in order
        assert order.index("b") < order.index("a")


class TestScopeAdapter:
    """Tests for scope adapter integration in _run_checks."""

    def test_no_scope_no_adapter_call(self):
        """When check has no scope, adapter is never called."""
        adapter_calls = []

        def adapter(ctx, scope):
            adapter_calls.append(scope)
            return ctx

        defs = {
            "a": _make_check_def(
                "a",
                impl=lambda ctx: pass_outcome("ok"),
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["a"], ctx, False, scope_adapter=adapter)
        assert exit_code == 0
        assert len(adapter_calls) == 0

    def test_scope_without_adapter_skips_adaptation(self):
        """When check has scope but no adapter is set, impl is called normally."""
        defs = {
            "a": _CheckDef(
                name="a", tags=["default"], severity="error",
                fast=True, pure=True, needs_network=False,
                depends_on=[], scope="changelog",
                impl=lambda ctx: pass_outcome("ok"),
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["a"], ctx, False, scope_adapter=None)
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
            "a": _CheckDef(
                name="a", tags=["default"], severity="error",
                fast=True, pure=True, needs_network=False,
                depends_on=[], scope="changelog",
                impl=impl,
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["a"], ctx, False, scope_adapter=adapter)
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
            "a": _CheckDef(
                name="a", tags=["default"], severity="error",
                fast=True, pure=True, needs_network=False,
                depends_on=[], scope="changelog",
                impl=impl,
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["a"], ctx, False, scope_adapter=adapter)
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
            "a": _CheckDef(
                name="a", tags=["default"], severity="error",
                fast=True, pure=True, needs_network=False,
                depends_on=[], scope="changelog",
                impl=lambda ctx: pass_outcome("should not run"),
            ),
            "b": _make_check_def(
                "b", depends_on=["a"],
                impl=lambda ctx: (b_ran.append(True), pass_outcome("b ok"))[1],
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["a", "b"], ctx, False, scope_adapter=adapter)
        assert exit_code == 0
        statuses = {name: r.status for name, r, _ in results}
        assert statuses["a"] == "skip"
        assert statuses["b"] == "pass"
        assert b_ran == [True]

    def test_scope_adapter_skip_reason_surfaced(self):
        """The SkipCheck reason appears in the derived skip outcome's message."""
        def adapter(ctx, scope):
            return SkipCheck("changelog not present")

        defs = {
            "a": _CheckDef(
                name="a", tags=["default"], severity="warn",
                fast=True, pure=True, needs_network=False,
                depends_on=[], scope="changelog",
                impl=lambda ctx: pass_outcome("should not run"),
            ),
        }
        ctx = SimpleContext(project_root=Path("/tmp"))
        results, _, exit_code = _run_checks(defs, ["a"], ctx, False, scope_adapter=adapter)
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
