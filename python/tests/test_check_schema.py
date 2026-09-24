"""Tests for check metadata in --dump-schema output."""

import json

import pytest
import strictcli
from conftest import pass_outcome


@pytest.fixture(autouse=True)
def _pyproject_in_tmp(tmp_path):
    """Ensure every test that uses tmp_path has a pyproject.toml for project_id."""
    (tmp_path / "pyproject.toml").write_text('[project]\nname = "testproject"\n')


CHECKS_TOML = """\
app = "testapp"

[checks.lint-code]
tags = ["code", "fast"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.check-deps]
tags = ["deps"]
severity = "warn"
fast = false
pure = false
needs_network = true
depends_on = ["lint-code"]
"""


class TestSchemaWithChecks:
    """--dump-schema includes checks key when checks are enabled."""

    def test_checks_key_present(self, tmp_path, monkeypatch):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(CHECKS_TOML)
        monkeypatch.chdir(tmp_path)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

        @app.error_check("lint-code")
        def lint_impl(ctx, reporter):
            return pass_outcome("ok")

        @app.warn_check("check-deps")
        def deps_impl(ctx, reporter):
            return pass_outcome("ok")

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        result = app.test(["--dump-schema"])
        assert result.exit_code == 0
        data = json.loads((tmp_path / ".strictcli" / "schema.json").read_text())
        assert "checks" in data
        assert isinstance(data["checks"], dict)
        assert len(data["checks"]) == 2

    def test_check_metadata_correct(self, tmp_path, monkeypatch):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(CHECKS_TOML)
        monkeypatch.chdir(tmp_path)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

        @app.error_check("lint-code")
        def lint_impl(ctx, reporter):
            return pass_outcome("ok")

        @app.warn_check("check-deps")
        def deps_impl(ctx, reporter):
            return pass_outcome("ok")

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        app.test(["--dump-schema"])
        data = json.loads((tmp_path / ".strictcli" / "schema.json").read_text())

        lint = data["checks"]["lint-code"]
        assert lint["tags"] == ["code", "fast"]
        assert lint["severity"] == "error"
        assert lint["fast"] is True
        assert lint["pure"] is True
        assert lint["needs_network"] is False
        assert lint["depends_on"] == []

        deps = data["checks"]["check-deps"]
        assert deps["tags"] == ["deps"]
        assert deps["severity"] == "warn"
        assert deps["fast"] is False
        assert deps["pure"] is False
        assert deps["needs_network"] is True
        assert deps["depends_on"] == ["lint-code"]


class TestSchemaWithoutChecks:
    """--dump-schema omits checks key when checks are not enabled."""

    def test_no_checks_key(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)

        app = strictcli.App(name="testapp", version="1.0.0", help="test app")

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        result = app.test(["--dump-schema"])
        assert result.exit_code == 0
        data = json.loads((tmp_path / ".strictcli" / "schema.json").read_text())
        assert "checks" not in data


SCOPED_CHECKS_TOML = """\
app = "testapp"

[checks.scoped-check]
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
scope = "changelog"

[checks.unscoped-check]
tags = ["code"]
severity = "warn"
fast = true
pure = true
needs_network = false
depends_on = []
"""


class TestSchemaWithScope:
    """--dump-schema includes scope for scoped checks and omits it for unscoped."""

    def test_scope_present_in_schema(self, tmp_path, monkeypatch):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(SCOPED_CHECKS_TOML)
        monkeypatch.chdir(tmp_path)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

        @app.error_check("scoped-check")
        def scoped_impl(ctx, reporter):
            return pass_outcome("ok")

        @app.warn_check("unscoped-check")
        def unscoped_impl(ctx, reporter):
            return pass_outcome("ok")

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        app.test(["--dump-schema"])
        data = json.loads((tmp_path / ".strictcli" / "schema.json").read_text())

        scoped = data["checks"]["scoped-check"]
        assert scoped["scope"] == "changelog"

        unscoped = data["checks"]["unscoped-check"]
        assert "scope" not in unscoped


class TestCheckBlockCanon:
    """The block is sorted by key and is a function of the declaration alone
    (contract §25.7, §25.9)."""

    def test_checks_are_sorted_ascending_by_name(self, tmp_path, monkeypatch):
        """Go retains no declaration order for the block, and a canon no
        implementation can produce is not a canon."""
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(CHECKS_TOML)
        monkeypatch.chdir(tmp_path)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

        @app.error_check("lint-code")
        def lint_impl(ctx, reporter):
            return pass_outcome("ok")

        @app.warn_check("check-deps")
        def deps_impl(ctx, reporter):
            return pass_outcome("ok")

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        app.test(["--dump-schema"])
        data = json.loads((tmp_path / ".strictcli" / "schema.json").read_text())
        # Declared in the TOML as lint-code then check-deps.
        assert list(data["checks"]) == ["check-deps", "lint-code"]

    def test_a_block_with_only_provider_sourced_checks_is_omitted(
        self, tmp_path, monkeypatch,
    ):
        """`checks: {}` is the app baseline, so an empty block is no block."""
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text('app = "testapp"\n')
        monkeypatch.chdir(tmp_path)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        app.test(["--dump-schema"])
        data = json.loads((tmp_path / ".strictcli" / "schema.json").read_text())
        assert "checks" not in data
