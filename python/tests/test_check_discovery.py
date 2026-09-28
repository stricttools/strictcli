"""Tests for check discovery and double-entry registration."""

import pytest

import strictcli
from strictcli import _parse_checks_toml
from conftest import pass_outcome


VALID_TOML = """\
app = "testapp"

[checks.lint-code]
description = "Checks lint-code"
subject = "quality"
tags = ["code", "fast"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []

[checks.check-deps]
description = "Checks check-deps"
subject = "quality"
tags = ["deps"]
severity = "warn"
fast = false
pure = false
needs_network = true
depends_on = ["lint-code"]
"""

INVALID_TOML = """\
app = "testapp"

[checks.BadName]
description = "Checks BadName"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
"""


class TestDiscovery:
    def test_no_checks_path_ignores_cwd(self, tmp_path, monkeypatch):
        """Without checks_path, CWD checks.toml is NOT discovered."""
        (tmp_path / ".strictcli").mkdir()
        (tmp_path / ".strictcli" / "checks.toml").write_text(VALID_TOML)
        monkeypatch.chdir(tmp_path)

        app = strictcli.App(name="testapp", version="1.0.0", help="test app")
        assert app._checks_enabled is False
        assert app._check_defs == {}

    def test_no_toml_sets_checks_disabled(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)

        app = strictcli.App(name="testapp", version="1.0.0", help="test app")
        assert app._checks_enabled is False
        assert app._check_defs == {}

    def test_invalid_toml_raises_valueerror(self, tmp_path):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(INVALID_TOML)

        with pytest.raises(ValueError, match='invalid check name "BadName"'):
            strictcli.App(
                name="testapp", version="1.0.0", help="test app",
                checks_path=str(toml_file),
            )


class TestChecksPath:
    def test_explicit_path_discovers_checks(self, tmp_path, monkeypatch):
        """checks_path points to a valid TOML in a non-CWD directory."""
        # Put checks.toml somewhere that is NOT CWD/.strictcli/
        custom_dir = tmp_path / "custom"
        custom_dir.mkdir()
        toml_file = custom_dir / "checks.toml"
        toml_file.write_text(VALID_TOML)

        # chdir to a directory with NO .strictcli/
        empty_dir = tmp_path / "empty"
        empty_dir.mkdir()
        monkeypatch.chdir(empty_dir)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )
        assert app._checks_enabled is True
        assert "lint-code" in app._check_defs
        assert "check-deps" in app._check_defs

    def test_explicit_path_as_pathlib(self, tmp_path, monkeypatch):
        """checks_path accepts a Path object."""
        custom_dir = tmp_path / "custom"
        custom_dir.mkdir()
        toml_file = custom_dir / "checks.toml"
        toml_file.write_text(VALID_TOML)

        empty_dir = tmp_path / "empty"
        empty_dir.mkdir()
        monkeypatch.chdir(empty_dir)

        from pathlib import Path

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=Path(str(toml_file)),
        )
        assert app._checks_enabled is True
        assert "lint-code" in app._check_defs

    def test_nonexistent_path_raises(self, tmp_path, monkeypatch):
        """checks_path pointing to a missing file raises ValueError."""
        monkeypatch.chdir(tmp_path)
        bad_path = tmp_path / "nope" / "checks.toml"

        with pytest.raises(ValueError, match="checks_path does not exist"):
            strictcli.App(
                name="testapp", version="1.0.0", help="test app",
                checks_path=str(bad_path),
            )

    def test_none_means_disabled(self, tmp_path, monkeypatch):
        """checks_path=None (default) means checks are disabled."""
        (tmp_path / ".strictcli").mkdir()
        (tmp_path / ".strictcli" / "checks.toml").write_text(VALID_TOML)
        monkeypatch.chdir(tmp_path)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=None,
        )
        assert app._checks_enabled is False
        assert app._check_defs == {}


class TestCheckDecorator:
    def test_registers_implementation(self, tmp_path):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(VALID_TOML)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

        @app.error_check("lint-code")
        def lint_impl(ctx, reporter):
            return reporter.passed("OK")

        # The registered impl is the wrapper that constructs a reporter and
        # invokes the decorated function; the decorated function is returned
        # unchanged, and the registration form is recorded.
        assert app._check_defs["lint-code"].impl is not None
        assert app._check_defs["lint-code"].impl_form == "error"
        assert lint_impl.__name__ == "lint_impl"

    def test_undeclared_name_raises(self, tmp_path):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(VALID_TOML)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

        with pytest.raises(ValueError, match='cannot register check "nonexistent"'):
            @app.error_check("nonexistent")
            def bad_impl(ctx, reporter):
                pass

    def test_duplicate_registration_raises(self, tmp_path):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(VALID_TOML)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

        @app.error_check("lint-code")
        def first(ctx, reporter):
            pass

        with pytest.raises(ValueError, match='check "lint-code": duplicate registration'):
            @app.error_check("lint-code")
            def second(ctx, reporter):
                pass

    def test_no_toml_raises(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)

        app = strictcli.App(name="testapp", version="1.0.0", help="test app")

        with pytest.raises(ValueError, match="checks not enabled"):
            @app.error_check("anything")
            def bad(ctx, reporter):
                pass

    def test_register_check_not_enabled_message(self, tmp_path):
        """Verify exact error message for registering check when not enabled."""
        app = strictcli.App(name="testapp", version="1.0.0", help="test app")

        with pytest.raises(
            ValueError,
            match=r'cannot register check "my-check": checks not enabled',
        ):
            @app.error_check("my-check")
            def impl(ctx, reporter):
                pass


class TestDoubleEntryValidation:
    def test_missing_impl_error_in_test(self, tmp_path):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(VALID_TOML)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

        # Register a dummy command so the app has something to run
        @app.command("hello", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="say hello")
        def hello(ctx, **kw):
            print("hello")

        # Only register one of the two checks
        @app.error_check("lint-code")
        def lint_impl(ctx, reporter):
            return pass_outcome("OK")

        # check-deps is not registered -- should fail validation
        result = app.test(["hello"])
        assert result.exit_code == 1
        assert "checks declared in checks.toml but not registered" in result.stderr
        assert "check-deps" in result.stderr

    def test_all_registered_passes_validation(self, tmp_path):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(VALID_TOML)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

        @app.command("hello", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="say hello")
        def hello(ctx, **kw):
            print("hello")

        @app.error_check("lint-code")
        def lint_impl(ctx, reporter):
            return pass_outcome("OK")

        @app.warn_check("check-deps")
        def deps_impl(ctx, reporter):
            return pass_outcome("OK")

        result = app.test(["hello"])
        assert result.exit_code == 0
        assert "hello" in result.stdout

    def test_no_checks_skips_validation(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)

        app = strictcli.App(name="testapp", version="1.0.0", help="test app")

        @app.command("hello", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="say hello")
        def hello(ctx, **kw):
            print("hello")

        result = app.test(["hello"])
        assert result.exit_code == 0
        assert "hello" in result.stdout

    def test_app_mismatch_raises(self, tmp_path):
        """checks_path with app='wrong' but App name='testapp' raises ValueError."""
        toml = """\
app = "wrong"

[checks.my-check]
description = "Checks my-check"
subject = "quality"
tags = ["a"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
"""
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(toml)

        with pytest.raises(
            ValueError,
            match=r'checks.toml: app "wrong" does not match app name "testapp"',
        ):
            strictcli.App(
                name="testapp", version="1.0.0", help="test app",
                checks_path=str(toml_file),
            )


class TestChecksEmbed:
    def test_checks_embed_enables_checks(self, tmp_path, monkeypatch):
        """checks_embed with valid TOML bytes enables checks."""
        monkeypatch.chdir(tmp_path)

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_embed=VALID_TOML.encode(),
        )
        assert app._checks_enabled is True
        assert "lint-code" in app._check_defs
        assert "check-deps" in app._check_defs

    def test_checks_embed_and_checks_path_raises(self, tmp_path, monkeypatch):
        """Using both checks_path and checks_embed raises ValueError."""
        monkeypatch.chdir(tmp_path)
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(VALID_TOML)

        with pytest.raises(ValueError, match="cannot use both checks_path and checks_embed"):
            strictcli.App(
                name="testapp", version="1.0.0", help="test app",
                checks_path=str(toml_file),
                checks_embed=VALID_TOML.encode(),
            )

    def test_checks_embed_invalid_toml(self, tmp_path, monkeypatch):
        """checks_embed with garbage bytes raises ValueError."""
        monkeypatch.chdir(tmp_path)

        with pytest.raises(ValueError, match="checks.toml:"):
            strictcli.App(
                name="testapp", version="1.0.0", help="test app",
                checks_embed=b"\x80\x81 not valid toml {{{{",
            )

    def test_checks_embed_wrong_app_name(self, tmp_path, monkeypatch):
        """checks_embed with mismatched app name raises ValueError."""
        monkeypatch.chdir(tmp_path)
        wrong_toml = 'app = "wrong"\n'

        with pytest.raises(
            ValueError,
            match=r'checks.toml: app "wrong" does not match app name "testapp"',
        ):
            strictcli.App(
                name="testapp", version="1.0.0", help="test app",
                checks_embed=wrong_toml.encode(),
            )


class TestScopeFieldParsing:
    """Tests for the optional scope field in checks.toml."""

    def test_scope_absent_defaults_to_empty(self):
        """When scope is not present, it defaults to empty string."""
        toml = b"""\
app = "testapp"

[checks.my-check]
description = "Checks my-check"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
"""
        _, defs = _parse_checks_toml(toml)
        assert defs["my-check"].scope == ""

    def test_scope_present_parsed_correctly(self):
        """When scope is present, it is parsed as a string."""
        toml = b"""\
app = "testapp"

[checks.my-check]
description = "Checks my-check"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
scope = "changelog"
"""
        _, defs = _parse_checks_toml(toml)
        assert defs["my-check"].scope == "changelog"

    def test_scope_empty_string_accepted(self):
        """An explicit empty string for scope is valid."""
        toml = b"""\
app = "testapp"

[checks.my-check]
description = "Checks my-check"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
scope = ""
"""
        _, defs = _parse_checks_toml(toml)
        assert defs["my-check"].scope == ""

    def test_scope_wrong_type_raises(self):
        """Non-string scope raises ValueError."""
        toml = b"""\
app = "testapp"

[checks.my-check]
description = "Checks my-check"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
scope = 42
"""
        with pytest.raises(ValueError, match='"scope" must be a string'):
            _parse_checks_toml(toml)

    def test_unknown_fields_still_rejected_with_scope(self):
        """Unknown fields are rejected even when scope is present."""
        toml = b"""\
app = "testapp"

[checks.my-check]
description = "Checks my-check"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
scope = "changelog"
bogus = true
"""
        with pytest.raises(ValueError, match='unknown field "bogus"'):
            _parse_checks_toml(toml)

    def test_scope_via_checks_embed(self, tmp_path, monkeypatch):
        """scope is parsed correctly when using checks_embed."""
        monkeypatch.chdir(tmp_path)
        toml = """\
app = "testapp"

[checks.my-check]
description = "Checks my-check"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
scope = "workspace"
"""
        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_embed=toml.encode(),
        )
        assert app._check_defs["my-check"].scope == "workspace"

    def test_scope_via_checks_path(self, tmp_path):
        """scope is parsed correctly when using checks_path."""
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text("""\
app = "testapp"

[checks.my-check]
description = "Checks my-check"
subject = "quality"
tags = ["release"]
severity = "error"
fast = true
pure = true
needs_network = false
depends_on = []
scope = "project"
""")
        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )
        assert app._check_defs["my-check"].scope == "project"


class TestInternalAddPath:
    """Tests for the internal _add_check_def / _enable_checks helpers."""

    def _enabled_app(self):
        return strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_embed=VALID_TOML.encode(),
        )

    def test_add_check_def_rejects_duplicate_name(self):
        """Adding a def whose name already exists is a hard error."""
        app = self._enabled_app()
        from strictcli import _CheckDef

        dup = _CheckDef(
            name="lint-code",
            tags=["x"],
            severity="error",
            fast=True,
            pure=True,
            needs_network=False,
            depends_on=[],
            scope="",
        )
        with pytest.raises(ValueError, match='duplicate check definition "lint-code"'):
            app._add_check_def(dup)

    def test_add_check_def_inserts_new(self):
        """A fresh name is inserted into the registry."""
        app = self._enabled_app()
        from strictcli import _CheckDef

        new = _CheckDef(
            name="brand-new",
            tags=["x"],
            severity="error",
            fast=True,
            pure=True,
            needs_network=False,
            depends_on=[],
            scope="",
        )
        app._add_check_def(new)
        assert app._check_defs["brand-new"] is new

    def test_enable_checks_idempotent_command_registration(self):
        """Calling _enable_checks again must not double-register the command."""
        app = self._enabled_app()
        assert app._checks_enabled is True
        cmd_before = app._commands["check"]
        app._enable_checks()
        app._enable_checks()
        assert app._commands["check"] is cmd_before
        # Registry and flag stay intact.
        assert app._checks_enabled is True
        assert "lint-code" in app._check_defs


class TestSeverityCrossCheck:
    """The registration form must match the TOML-declared severity."""

    def _app(self, tmp_path):
        # VALID_TOML: lint-code severity="error", check-deps severity="warn".
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(VALID_TOML)
        return strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

    def test_error_check_on_warn_severity_raises(self, tmp_path):
        app = self._app(tmp_path)
        with pytest.raises(
            ValueError,
            match=r'check "check-deps": declared severity "warn" in checks.toml '
            r"but registered via @app.error_check; use @app.warn_check",
        ):
            @app.error_check("check-deps")
            def deps(ctx, reporter):
                return reporter.passed("ok")

    def test_warn_check_on_error_severity_raises(self, tmp_path):
        app = self._app(tmp_path)
        with pytest.raises(
            ValueError,
            match=r'check "lint-code": declared severity "error" in checks.toml '
            r"but registered via @app.warn_check; use @app.error_check",
        ):
            @app.warn_check("lint-code")
            def lint(ctx, reporter):
                return reporter.passed("ok")

    def test_correct_forms_register(self, tmp_path):
        app = self._app(tmp_path)

        @app.error_check("lint-code")
        def lint(ctx, reporter):
            return reporter.passed("ok")

        @app.warn_check("check-deps")
        def deps(ctx, reporter):
            return reporter.passed("ok")

        assert app._check_defs["lint-code"].impl_form == "error"
        assert app._check_defs["check-deps"].impl_form == "warn"

    def test_real_reporter_binding_error_check_can_report_error(self, tmp_path):
        # The error_check impl receives an ErrorReporter and can mint an error
        # problem (deriving FAIL); the warn_check impl receives a WarnReporter.
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(VALID_TOML)
        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

        @app.error_check("lint-code")
        def lint(ctx, reporter):
            reporter.error("boom")
            return reporter.found("failed")

        @app.warn_check("check-deps")
        def deps(ctx, reporter):
            assert not hasattr(reporter, "error")
            return reporter.passed("ok")

        app.set_check_context(
            lambda: type("C", (), {"project_root": tmp_path})()
        )
        results, _, exit_code = app.run_checks(
            type("C", (), {"project_root": tmp_path})(),
            name_glob="lint-code",
        )
        assert exit_code == 1
        assert results[0].status == "fail"
