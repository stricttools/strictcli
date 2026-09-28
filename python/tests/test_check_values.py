"""Check metadata, per-check values, named hook selections, and the
failing-checks command."""

import json
import re
from dataclasses import dataclass
from pathlib import Path

import pytest

import strictcli
from conftest import (
    drop_builtin_check_providers, fail_outcome, pass_outcome, payload,
    warn_outcome,
)


@dataclass
class SimpleContext:
    project_root: Path


def _check(name, *, tags, severity="error", depends_on=(), extra=""):
    deps = ", ".join(f'"{d}"' for d in depends_on)
    tag_list = ", ".join(f'"{t}"' for t in tags)
    return (
        f"[checks.{name}]\n"
        f'description = "Checks {name}"\n'
        f'subject = "quality"\n'
        f"tags = [{tag_list}]\n"
        f'severity = "{severity}"\n'
        f"fast = true\npure = true\nneeds_network = false\n"
        f"depends_on = [{deps}]\n{extra}\n"
    )


HOOKS = '''
[hooks.pre-push]
tag = "prepush"

[hooks.pre-release]
tag = "preflight & !slow"
'''

TOML = (
    'app = "testapp"\n\n'
    + _check("lint", tags=["prepush"])
    + _check("fmt", tags=["prepush"], severity="warn")
    + _check("docs", tags=["preflight"])
    + _check("bench", tags=["preflight", "slow"])
    + HOOKS
)


def _app(tmp_path, toml=TOML, outcomes=None, resolver=None):
    toml_file = tmp_path / "checks.toml"
    toml_file.write_text(toml)
    app = strictcli.App(
        name="testapp", version="1.0.0", help="test app",
        checks_path=str(toml_file),
    )
    outcomes = outcomes or {}
    for name in app._check_defs:
        out = outcomes.get(name)
        app._check_defs[name].impl = (
            (lambda ctx, o=out: o) if out is not None
            else (lambda ctx, n=name: pass_outcome(f"{n} ok"))
        )
    app.set_check_context(lambda: SimpleContext(project_root=tmp_path))
    drop_builtin_check_providers(app)
    if resolver is not None:
        app.set_check_value_resolver(resolver)
    return app


def _values(mapping):
    def resolve(name):
        entry = mapping.get(name)
        if entry is None:
            return None
        return strictcli.CheckValue(value=entry[0], source=entry[1])
    return resolve


def _toml_error(tmp_path, toml):
    f = tmp_path / "checks.toml"
    f.write_text(toml)
    with pytest.raises(ValueError) as exc:
        strictcli.App(name="testapp", version="1.0.0", help="t", checks_path=str(f))
    return str(exc.value)


def _toml_ok(tmp_path, toml):
    f = tmp_path / "checks.toml"
    f.write_text(toml)
    return strictcli.App(name="testapp", version="1.0.0", help="t", checks_path=str(f))


# ---------------------------------------------------------------------------
# Check metadata: description and subject
# ---------------------------------------------------------------------------


class TestCheckMetadata:
    def _one(self, description='"Runs the linter"', subject='"quality"'):
        lines = ['app = "testapp"', "", "[checks.lint]"]
        if description is not None:
            lines.append(f"description = {description}")
        if subject is not None:
            lines.append(f"subject = {subject}")
        lines += [
            'tags = ["a"]', 'severity = "error"', "fast = true", "pure = true",
            "needs_network = false", "depends_on = []", "",
        ]
        return "\n".join(lines)

    def test_missing_description_is_refused_and_adding_it_clears(self, tmp_path):
        msg = _toml_error(tmp_path, self._one(description=None))
        assert msg == 'checks.toml: check "lint": missing required field "description"'
        _toml_ok(tmp_path, self._one())

    def test_missing_subject_is_refused_and_adding_it_clears(self, tmp_path):
        msg = _toml_error(tmp_path, self._one(subject=None))
        assert msg == 'checks.toml: check "lint": missing required field "subject"'
        _toml_ok(tmp_path, self._one())

    def test_both_missing_names_description_first(self, tmp_path):
        msg = _toml_error(tmp_path, self._one(description=None, subject=None))
        assert msg.endswith('missing required field "description"')

    @pytest.mark.parametrize("bad", ['""', '"   "', '"two\\nlines"', "3"])
    def test_description_must_be_one_nonempty_line(self, tmp_path, bad):
        msg = _toml_error(tmp_path, self._one(description=bad))
        assert msg == (
            'checks.toml: check "lint": "description" must be a non-empty '
            "single-line string"
        )
        _toml_ok(tmp_path, self._one(description='"one line"'))

    @pytest.mark.parametrize(
        "bad", ['""', '"Quality"', '"code quality"', '"a_b"', '"manifest"', "1"],
    )
    def test_subject_grammar_is_enforced(self, tmp_path, bad):
        msg = _toml_error(tmp_path, self._one(subject=bad))
        assert msg == (
            'checks.toml: check "lint": "subject" must be lowercase letters, '
            'digits, and hyphens, and not "manifest"'
        )
        _toml_ok(tmp_path, self._one(subject='"code-quality-2"'))

    def test_metadata_reaches_the_schema_dump(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        (tmp_path / "pyproject.toml").write_text('[project]\nname = "testapp"\n')
        app = _app(tmp_path)
        r = app.test(["--dump-schema"])
        assert r.exit_code == 0
        schema = json.loads((tmp_path / ".strictcli" / "schema.json").read_text())
        entry = schema["checks"]["lint"]
        assert entry["description"] == "Checks lint"
        assert entry["subject"] == "quality"
        assert list(entry)[-2:] == ["description", "subject"]


# ---------------------------------------------------------------------------
# Named hook selections
# ---------------------------------------------------------------------------


class TestHooks:
    def test_hook_runs_its_selection(self, tmp_path):
        r = _app(tmp_path).test(["check", "--hook", "pre-push"])
        assert r.exit_code == 0
        assert "lint ok" in r.stdout and "fmt ok" in r.stdout
        assert "docs" not in r.stdout

    def test_hook_expression_keeps_negation(self, tmp_path):
        r = _app(tmp_path).test(["check", "--hook", "pre-release"])
        assert "docs ok" in r.stdout
        assert "bench" not in r.stdout

    def test_manual_tag_keeps_negation(self, tmp_path):
        r = _app(tmp_path).test(["check", "--tag", "!prepush & !slow"])
        assert "docs ok" in r.stdout
        assert "lint" not in r.stdout and "bench" not in r.stdout

    def test_failing_checks_takes_a_hook(self, tmp_path):
        app = _app(tmp_path, outcomes={"lint": fail_outcome("broken", "bad line")})
        r = app.test(["failing-checks", "--hook", "pre-push"])
        assert r.exit_code == 1
        assert "FAIL  lint" in r.stdout

    def test_unknown_hook_lists_the_declared_ones(self, tmp_path):
        app = _app(tmp_path)
        r = app.test(["check", "--hook", "pre-pusj"])
        assert r.exit_code == 1
        assert r.stderr.startswith(
            'error: unknown hook "pre-pusj"; declared hooks: pre-push, pre-release\n'
        )
        assert app.test(["check", "--hook", "pre-push"]).exit_code == 0

    def test_unknown_hook_with_none_declared(self, tmp_path):
        toml = 'app = "testapp"\n\n' + _check("lint", tags=["prepush"])
        r = _app(tmp_path, toml=toml).test(["failing-checks", "--hook", "pre-push"])
        assert r.exit_code == 1
        assert r.stderr.startswith(
            'error: unknown hook "pre-push"; checks.toml declares no hooks\n'
        )
        # The fix: declare the hook.
        fixed = toml + '\n[hooks.pre-push]\ntag = "prepush"\n'
        assert _app(tmp_path, toml=fixed).test(
            ["failing-checks", "--hook", "pre-push"],
        ).exit_code == 0

    @pytest.mark.parametrize("other", [
        ["--tag", "prepush"], ["--name", "lint"], ["--all"],
    ])
    def test_hook_combines_with_no_other_selection(self, tmp_path, other):
        app = _app(tmp_path)
        r = app.test(["check", "--hook", "pre-push", *other])
        assert r.exit_code == 1
        assert r.stderr.startswith(
            "error: --hook cannot be combined with --all, --tag, or --name\n"
        )
        assert app.test(["check", "--hook", "pre-push"]).exit_code == 0

    def test_declared_hooks_appear_in_help(self, tmp_path):
        for command in ("check", "failing-checks"):
            r = _app(tmp_path).test([command, "--help"])
            assert (
                "Run the checks a hook declared in checks.toml selects: "
                "pre-push (tag 'prepush'), pre-release (tag 'preflight & !slow')"
            ) in r.stdout

    def test_help_says_when_no_hooks_are_declared(self, tmp_path):
        toml = 'app = "testapp"\n\n' + _check("lint", tags=["x"])
        r = _app(tmp_path, toml=toml).test(["check", "--help"])
        assert "(no hooks are declared)" in r.stdout


class TestHookDeclarationErrors:
    BASE = 'app = "testapp"\n\n' + _check("lint", tags=["prepush"])

    @pytest.mark.parametrize("hooks,message", [
        ("[hooks.Pre-Push]\ntag = \"prepush\"\n",
         'checks.toml: invalid hook name "Pre-Push" (must match [a-z][a-z0-9-]*)'),
        ("[hooks]\npre-push = \"prepush\"\n",
         'checks.toml: hook "pre-push" must be a table'),
        ("[hooks.pre-push]\ntag = \"prepush\"\nname = \"lint\"\n",
         'checks.toml: hook "pre-push": unknown field "name"'),
        ("[hooks.pre-push]\n",
         'checks.toml: hook "pre-push": missing required field "tag"'),
        ("[hooks.pre-push]\ntag = \"\"\n",
         'checks.toml: hook "pre-push": "tag" must be a non-empty string'),
        ("[hooks.pre-push]\ntag = \"prepush &\"\n",
         'checks.toml: hook "pre-push": tag expression: unexpected end of '
         "expression at position 9"),
        ("hooks = 3\n",
         "checks.toml: [hooks] must be a table"),
    ])
    def test_refusal_and_its_fix(self, tmp_path, hooks, message):
        if hooks.startswith("hooks ="):
            toml = 'app = "testapp"\n' + hooks + "\n" + _check("lint", tags=["prepush"])
        else:
            toml = self.BASE + "\n" + hooks
        assert _toml_error(tmp_path, toml) == message
        fixed = self.BASE + '\n[hooks.pre-push]\ntag = "prepush"\n'
        app = _toml_ok(tmp_path, fixed)
        assert app._check_hooks == {"pre-push": "prepush"}


# ---------------------------------------------------------------------------
# Per-check values
# ---------------------------------------------------------------------------


class TestCheckValues:
    def test_off_does_not_run_and_is_shown_with_its_source(self, tmp_path):
        called = []
        app = _app(tmp_path, resolver=_values({
            "lint": ("off", "demo:lint in options/quality.toml"),
        }))
        app._check_defs["lint"].impl = lambda ctx: called.append(1)
        r = app.test(["check", "--hook", "pre-push"])
        assert called == []
        assert r.exit_code == 0
        assert "OFF   lint    off: demo:lint in options/quality.toml" in r.stdout
        j = payload(app.test(["check", "--hook", "pre-push", "--json"]))
        entry = next(e for e in j if e["name"] == "lint")
        assert entry["status"] == "off"
        assert entry["message"] == "off: demo:lint in options/quality.toml"

    def test_warn_reports_failures_as_warnings(self, tmp_path):
        app = _app(
            tmp_path,
            outcomes={"lint": fail_outcome("2 problems", "bad a", "bad b")},
            resolver=_values({"lint": ("warn", "demo:lint in q.toml")}),
        )
        r = app.test(["check", "--name", "lint"])
        assert r.exit_code == 1  # check keeps exiting nonzero on warnings
        assert "WARN  lint" in r.stdout
        assert "[warn] bad a" in r.stdout and "[error]" not in r.stdout
        f = app.test(["failing-checks", "--name", "lint"])
        assert f.exit_code == 0
        assert f.stdout == ""

    def test_error_runs_as_registered(self, tmp_path):
        app = _app(
            tmp_path,
            outcomes={"lint": fail_outcome("broken", "bad")},
            resolver=_values({"lint": ("error", "demo:lint in q.toml")}),
        )
        r = app.test(["failing-checks", "--name", "lint"])
        assert r.exit_code == 1
        assert "FAIL  lint" in r.stdout

    def test_no_value_means_registered_severity_from_default(self, tmp_path):
        app = _app(tmp_path, resolver=lambda name: None)
        items = payload(app.test(["check", "--list", "--json"]))
        by = {i["name"]: i for i in items}
        assert (by["lint"]["value"], by["lint"]["source"]) == ("error", "default")
        assert (by["fmt"]["value"], by["fmt"]["source"]) == ("warn", "default")

    def test_raising_above_registered_severity_is_refused(self, tmp_path):
        app = _app(tmp_path, resolver=_values({"fmt": ("error", "demo:fmt in q.toml")}))
        r = app.test(["check", "--name", "fmt"])
        assert r.exit_code == 1
        assert r.stderr.startswith(
            'error: check "fmt": the check value resolver returned "error" '
            '(from demo:fmt in q.toml) for a check registered as "warn"; a check '
            "value may lower a check's severity, never raise it\n"
        )
        # The fix: resolve it at or below its registered severity.
        fixed = _app(tmp_path, resolver=_values({"fmt": ("warn", "demo:fmt in q.toml")}))
        assert fixed.test(["check", "--name", "fmt"]).exit_code == 0

    def test_a_value_outside_the_three_is_refused(self, tmp_path):
        app = _app(tmp_path, resolver=_values({"lint": ("on", "demo:lint in q.toml")}))
        r = app.test(["check", "--list"])
        assert r.exit_code == 1
        assert r.stderr.startswith(
            'error: check "lint": the check value resolver returned "on"; '
            "a check value is one of error, warn, off\n"
        )
        fixed = _app(tmp_path, resolver=_values({"lint": ("off", "demo:lint in q.toml")}))
        assert fixed.test(["check", "--list"]).exit_code == 0

    def test_an_empty_source_is_refused(self, tmp_path):
        app = _app(tmp_path, resolver=_values({"lint": ("off", "")}))
        r = app.test(["failing-checks", "--all"])
        assert r.exit_code == 1
        assert r.stderr.startswith(
            'error: check "lint": the check value resolver returned "off" with '
            "an empty source; name where the value came from\n"
        )
        fixed = _app(tmp_path, resolver=_values({"lint": ("off", "demo:lint")}))
        assert fixed.test(["failing-checks", "--all"]).exit_code == 0

    def test_resolver_must_return_a_check_value(self, tmp_path):
        app = _app(tmp_path, resolver=lambda name: "off")
        r = app.test(["check", "--all"])
        assert r.exit_code == 1
        assert "not a CheckValue or None" in r.stderr

    def test_resolver_must_be_callable(self, tmp_path):
        app = _app(tmp_path)
        with pytest.raises(ValueError, match="check value resolver must be callable"):
            app.set_check_value_resolver("off")

    def test_a_warn_dependency_does_not_block_its_dependent(self, tmp_path):
        toml = (
            'app = "testapp"\n\n'
            + _check("base", tags=["t"])
            + _check("top", tags=["t"], depends_on=["base"])
        )
        app = _app(
            tmp_path, toml=toml,
            outcomes={"base": fail_outcome("base broken", "x")},
            resolver=_values({"base": ("warn", "demo:base")}),
        )
        items = payload(app.test(["check", "--all", "--json"]))
        assert {i["name"]: i["status"] for i in items} == {"base": "warn", "top": "pass"}

    def test_an_error_dependency_still_blocks(self, tmp_path):
        toml = (
            'app = "testapp"\n\n'
            + _check("base", tags=["t"])
            + _check("top", tags=["t"], depends_on=["base"])
        )
        app = _app(tmp_path, toml=toml, outcomes={"base": fail_outcome("broken", "x")})
        items = payload(app.test(["check", "--name", "top", "--json"]))
        assert {i["name"]: i["status"] for i in items} == {"base": "fail", "top": "skip"}

    def test_an_off_dependency_does_not_block_its_dependent(self, tmp_path):
        toml = (
            'app = "testapp"\n\n'
            + _check("base", tags=["t"])
            + _check("top", tags=["t"], depends_on=["base"])
        )
        app = _app(
            tmp_path, toml=toml,
            outcomes={"base": fail_outcome("base broken", "x")},
            resolver=_values({"base": ("off", "demo:base")}),
        )
        r = app.test(["check", "--name", "top"])
        assert r.exit_code == 0
        assert "OFF   base    off: demo:base" in r.stdout
        assert "PASS  top " in r.stdout

    def test_a_broken_check_resolved_to_warn_still_fails(self, tmp_path):
        app = _app(tmp_path, resolver=_values({"lint": ("warn", "demo:lint")}))

        def boom(ctx):
            raise RuntimeError("kaboom")
        app._check_defs["lint"].impl = boom
        r = app.test(["failing-checks", "--name", "lint"])
        assert r.exit_code == 1
        assert 'check "lint" aborted with RuntimeError: kaboom' in r.stdout

    def test_verbose_summary_counts_off_checks(self, tmp_path):
        app = _app(tmp_path, resolver=_values({"lint": ("off", "demo:lint")}))
        r = app.test(["--verbose", "check", "--hook", "pre-push"])
        assert "1 passed / 0 failed / 0 warned / 0 skipped / 1 off" in r.stdout

    def test_run_checks_applies_the_resolver(self, tmp_path):
        app = _app(
            tmp_path,
            outcomes={"lint": fail_outcome("broken", "x")},
            resolver=_values({"lint": ("warn", "demo:lint"), "docs": ("off", "demo:docs")}),
        )
        results, _, exit_code = app.run_checks(
            SimpleContext(project_root=tmp_path), run_all=True,
        )
        by = {r.name: r.status for r in results}
        assert by["lint"] == "warn" and by["docs"] == "off"
        assert exit_code == 1
        assert not any(r.gated() for r in results)

    def test_run_checks_refuses_a_bad_value(self, tmp_path):
        app = _app(tmp_path, resolver=_values({"fmt": ("error", "demo:fmt")}))
        with pytest.raises(ValueError, match="never raise it"):
            app.run_checks(SimpleContext(project_root=tmp_path), run_all=True)


class TestCheckList:
    def test_list_shows_value_and_source(self, tmp_path):
        app = _app(tmp_path, resolver=_values({
            "lint": ("off", "demo:lint in options/quality.toml"),
            "docs": ("warn", "demo:docs in options/docs.toml"),
        }))
        r = app.test(["check", "--list"])
        assert r.exit_code == 0
        assert r.stdout == (
            "NAME    TAGS              SEVERITY   VALUE   SOURCE\n"
            "bench   preflight, slow   error      error   default\n"
            "docs    preflight         error      warn    demo:docs in options/docs.toml\n"
            "fmt     prepush           warn       warn    default\n"
            "lint    prepush           error      off     demo:lint in options/quality.toml\n"
        )
        items = payload(app.test(["check", "--list", "--json"]))
        by = {i["name"]: i for i in items}
        assert by["lint"]["value"] == "off"
        assert by["lint"]["source"] == "demo:lint in options/quality.toml"

    def test_failing_checks_lists_too(self, tmp_path):
        r = _app(tmp_path).test(["failing-checks", "--list"])
        assert r.exit_code == 0
        assert r.stdout.startswith("NAME ")


# ---------------------------------------------------------------------------
# failing-checks
# ---------------------------------------------------------------------------


class TestFailingChecks:
    def test_prints_only_error_level_failures(self, tmp_path):
        app = _app(tmp_path, outcomes={
            "lint": fail_outcome("lint broken", "bad"),
            "fmt": warn_outcome("fmt drift", "drift"),
        })
        r = app.test(["failing-checks", "--all"])
        assert r.exit_code == 1
        assert r.stdout == "FAIL  lint    lint broken\n        [error] bad\n"
        items = payload(app.test(["failing-checks", "--all", "--json"]))
        assert [i["name"] for i in items] == ["lint"]

    def test_warnings_alone_exit_zero_and_print_nothing(self, tmp_path):
        app = _app(tmp_path, outcomes={"fmt": warn_outcome("fmt drift", "drift")})
        r = app.test(["failing-checks", "--all"])
        assert r.exit_code == 0
        assert r.stdout == ""
        # check stays the full report and keeps exiting nonzero on warnings.
        c = app.test(["check", "--all"])
        assert c.exit_code == 1
        assert "WARN  fmt" in c.stdout

    def test_no_flags_shows_its_help(self, tmp_path):
        r = _app(tmp_path).test(["failing-checks"])
        assert r.exit_code == 0
        assert r.stdout.startswith("testapp failing-checks -- ")

    def test_takes_tag_and_name(self, tmp_path):
        app = _app(tmp_path, outcomes={"lint": fail_outcome("broken", "x")})
        assert app.test(["failing-checks", "--tag", "prepush"]).exit_code == 1
        assert app.test(["failing-checks", "--name", "docs"]).exit_code == 0

    def test_appears_in_app_help(self, tmp_path):
        r = _app(tmp_path).test(["--help"])
        assert re.search(r"^  failing-checks\s+Run project checks and report only "
                         r"error-level failures, exiting nonzero when any exist$",
                         r.stdout, re.MULTILINE)

    def test_declares_the_same_flags_as_check(self, tmp_path):
        app = _app(tmp_path)
        names = {f.name for f in app._commands["failing-checks"].flags}
        assert names == {"all", "tag", "name", "hook", "list"}
        r = app.test(["failing-checks", "--all", "--ignore-warnings"])
        assert r.exit_code == 1
        assert "--ignore-warnings" in r.stderr
