"""Tests for the config field declaration API (ConfigField, app.config_field)."""

import json
import os

import pytest
from conftest import payload

import strictcli


def _build_app(**kwargs):
    return strictcli.App(name="myapp", version="1.0.0", help="test app", **kwargs)


class TestConfigFieldRegistration:
    """Basic registration of config fields with all four types."""

    def test_register_str_field(self):
        app = _build_app()
        cf = app.config_field("name", type=str, help="user name")
        assert cf.name == "name"
        assert cf.type is str
        assert cf.help == "user name"
        assert cf.required is True

    def test_register_int_field(self):
        app = _build_app()
        cf = app.config_field("port", type=int, help="listen port")
        assert cf.type is int
        assert cf.required is True

    def test_register_float_field(self):
        app = _build_app()
        cf = app.config_field("rate", type=float, help="rate limit")
        assert cf.type is float
        assert cf.required is True

    def test_register_bool_field(self):
        app = _build_app()
        cf = app.config_field("loud", type=bool, help="enable loud")
        assert cf.type is bool
        assert cf.required is True

    def test_field_stored_in_config_fields(self):
        app = _build_app()
        app.config_field("name", type=str, help="user name")
        assert "name" in app._config_fields
        assert app._config_fields["name"].type is str


class TestConfigFieldDotNames:
    """Dot-separated names for TOML section nesting."""

    def test_single_dot(self):
        app = _build_app()
        cf = app.config_field("serve.port", type=int, help="server port")
        assert cf.name == "serve.port"

    def test_multiple_dots(self):
        app = _build_app()
        cf = app.config_field("db.pool.max_size", type=int, help="pool size")
        assert cf.name == "db.pool.max_size"

    def test_underscore_in_segment(self):
        app = _build_app()
        cf = app.config_field("log_level", type=str, help="log level")
        assert cf.name == "log_level"


class TestConfigFieldDefaults:
    """Required vs optional fields based on default presence."""

    def test_no_default_is_required(self):
        app = _build_app()
        cf = app.config_field("name", type=str, help="user name")
        assert cf.required is True

    def test_with_default_is_optional(self):
        app = _build_app()
        cf = app.config_field("port", type=int, help="port", default=8080)
        assert cf.required is False
        assert cf.default == 8080

    def test_str_default(self):
        app = _build_app()
        cf = app.config_field("host", type=str, help="host", default="localhost")
        assert cf.required is False
        assert cf.default == "localhost"

    def test_bool_default(self):
        app = _build_app()
        cf = app.config_field("debug", type=bool, help="debug", default=False)
        assert cf.required is False
        assert cf.default is False

    def test_float_default(self):
        app = _build_app()
        cf = app.config_field("rate", type=float, help="rate", default=1.5)
        assert cf.required is False
        assert cf.default == 1.5


class TestConfigFieldTypeValidation:
    """Type mismatch between default and declared type."""

    def test_str_field_with_int_default(self):
        app = _build_app()
        with pytest.raises(ValueError, match="does not match type str"):
            app.config_field("name", type=str, help="name", default=42)

    def test_int_field_with_str_default(self):
        app = _build_app()
        with pytest.raises(ValueError, match="does not match type int"):
            app.config_field("port", type=int, help="port", default="8080")

    def test_bool_field_with_int_default(self):
        app = _build_app()
        with pytest.raises(ValueError, match="does not match type bool"):
            app.config_field("debug", type=bool, help="debug", default=1)

    def test_float_field_with_str_default(self):
        app = _build_app()
        with pytest.raises(ValueError, match="does not match type float"):
            app.config_field("rate", type=float, help="rate", default="1.5")

    def test_invalid_type(self):
        app = _build_app()
        with pytest.raises(ValueError, match="must be str, bool, int, or float"):
            app.config_field("data", type=list, help="data")


class TestConfigFieldDuplicateError:
    """Duplicate field name is a registration-time error."""

    def test_duplicate_name_error(self):
        app = _build_app()
        app.config_field("port", type=int, help="port")
        with pytest.raises(ValueError, match='duplicate config field name "port"'):
            app.config_field("port", type=int, help="port again")

    def test_different_types_still_duplicate(self):
        app = _build_app()
        app.config_field("port", type=int, help="port")
        with pytest.raises(ValueError, match='duplicate config field name "port"'):
            app.config_field("port", type=str, help="port as string")


class TestConfigFieldUnderscoreReserved:
    """Names starting with underscore are reserved for framework fields."""

    def test_underscore_prefix_rejected(self):
        app = _build_app()
        with pytest.raises(ValueError, match="reserved"):
            app.config_field("_internal", type=str, help="internal field")

    def test_underscore_schema_version_rejected(self):
        app = _build_app()
        with pytest.raises(ValueError, match="reserved"):
            app.config_field("_schema_version", type=int, help="schema version")


class TestConfigFieldNameValidation:
    """Name format validation."""

    def test_empty_name_rejected(self):
        app = _build_app()
        with pytest.raises(ValueError, match="invalid"):
            app.config_field("", type=str, help="empty name")

    def test_uppercase_rejected(self):
        app = _build_app()
        with pytest.raises(ValueError, match="invalid"):
            app.config_field("Port", type=int, help="uppercase")

    def test_starts_with_digit_rejected(self):
        app = _build_app()
        with pytest.raises(ValueError, match="invalid"):
            app.config_field("3port", type=int, help="starts with digit")

    def test_hyphen_rejected(self):
        """Config field names use underscores, not hyphens."""
        app = _build_app()
        with pytest.raises(ValueError, match="invalid"):
            app.config_field("log-level", type=str, help="hyphenated")

    def test_trailing_dot_rejected(self):
        app = _build_app()
        with pytest.raises(ValueError, match="invalid"):
            app.config_field("serve.", type=str, help="trailing dot")

    def test_leading_dot_rejected(self):
        app = _build_app()
        with pytest.raises(ValueError, match="invalid"):
            app.config_field(".serve", type=str, help="leading dot")

    def test_double_dot_rejected(self):
        app = _build_app()
        with pytest.raises(ValueError, match="invalid"):
            app.config_field("serve..port", type=str, help="double dot")

    def test_trailing_newline_rejected(self):
        """A trailing newline must be rejected (fullmatch, not $-anchored match).

        Python's re.$ matches before a trailing newline, so ``match`` would
        wrongly accept ``db.url\\n``. Go uses a full-string match; parity
        requires ``fullmatch`` here.
        """
        app = _build_app()
        with pytest.raises(ValueError, match="invalid"):
            app.config_field("db.url\n", type=str, help="trailing newline")


class TestConfigFieldHelpRequired:
    """Help text is mandatory."""

    def test_empty_help_rejected(self):
        app = _build_app()
        with pytest.raises(ValueError, match="help"):
            app.config_field("name", type=str, help="")


class TestFrameworkFieldRegistration:
    """Framework-internal field registration via _register_framework_field."""

    def test_register_framework_field(self):
        app = _build_app()
        cf = app._register_framework_field(
            "_schema_version", type=int, help="config schema version"
        )
        assert cf.name == "_schema_version"
        assert cf.type is int
        assert "_schema_version" in app._framework_fields

    def test_framework_field_requires_underscore_prefix(self):
        app = _build_app()
        with pytest.raises(ValueError, match="must start with underscore"):
            app._register_framework_field(
                "schema_version", type=int, help="no underscore"
            )

    def test_duplicate_framework_field(self):
        app = _build_app()
        app._register_framework_field(
            "_schema_version", type=int, help="schema version"
        )
        with pytest.raises(ValueError, match="duplicate framework field"):
            app._register_framework_field(
                "_schema_version", type=int, help="schema version again"
            )

    def test_framework_field_stored_separately(self):
        """Framework fields go in _framework_fields, not _config_fields."""
        app = _build_app()
        app._register_framework_field(
            "_schema_version", type=int, help="schema version"
        )
        assert "_schema_version" not in app._config_fields
        assert "_schema_version" in app._framework_fields

    def test_user_cannot_register_framework_name(self):
        """User config_field rejects underscore-prefixed names even if
        no framework field with that name exists."""
        app = _build_app()
        with pytest.raises(ValueError, match="reserved"):
            app.config_field("_internal", type=str, help="reserved name")

    def test_framework_field_conflicts_with_user_field(self):
        """Framework field cannot use a name already taken by a user field.

        This is a safety check for the (unlikely) case where the name
        validation somehow allows overlap. In practice, user fields
        cannot start with underscore, so the namespaces are disjoint.
        """
        app = _build_app()
        # This would only conflict if the namespaces could overlap.
        # Since user fields reject underscore and framework fields require it,
        # the namespaces are disjoint by construction.
        app.config_field("port", type=int, help="port")
        # Framework field with different name is fine
        app._register_framework_field(
            "_schema_version", type=int, help="schema version"
        )
        assert "port" in app._config_fields
        assert "_schema_version" in app._framework_fields


class TestConfigFieldDataclass:
    """Direct ConfigField construction and validation."""

    def test_construct_directly(self):
        cf = strictcli.ConfigField(name="port", type=int, help="listen port")
        assert cf.name == "port"
        assert cf.required is True

    def test_construct_with_default(self):
        cf = strictcli.ConfigField(
            name="port", type=int, help="listen port", default=8080
        )
        assert cf.required is False
        assert cf.default == 8080

    def test_construct_invalid_type(self):
        with pytest.raises(ValueError, match="must be str, bool, int, or float"):
            strictcli.ConfigField(name="data", type=dict, help="bad type")

    def test_construct_type_mismatch(self):
        with pytest.raises(ValueError, match="does not match type"):
            strictcli.ConfigField(
                name="port", type=int, help="port", default="not_an_int"
            )


# ---- Task 5c: Per-command config field binding ----


class TestCommandConfigFieldBinding:
    """Config fields can be bound to commands via config_fields parameter."""

    def test_valid_binding(self):
        app = _build_app()
        app.config_field("db.url", type=str, help="Database URL")
        app.config_field("cache.ttl", type=int, help="Cache TTL", default=3600)

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start server",
                     config_fields=["db.url", "cache.ttl"])
        def serve(ctx, **kw):
            pass

        cmd = app._commands["serve"]
        assert cmd.config_fields == ("db.url", "cache.ttl")

    def test_unknown_field_reference_rejected(self):
        app = _build_app()
        app.config_field("db.url", type=str, help="Database URL")
        with pytest.raises(ValueError, match='unknown config field "nonexistent"'):
            @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start server",
                         config_fields=["nonexistent"])
            def serve(ctx, **kw):
                pass

    def test_no_config_fields_default(self):
        app = _build_app()

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start server")
        def serve(ctx, **kw):
            pass

        cmd = app._commands["serve"]
        assert cmd.config_fields == ()

    def test_empty_config_fields(self):
        app = _build_app()

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start server", config_fields=[])
        def serve(ctx, **kw):
            pass

        cmd = app._commands["serve"]
        assert cmd.config_fields == ()

    def test_group_command_binding(self):
        app = _build_app()
        app.config_field("db.url", type=str, help="Database URL")
        grp = app.group(name="server", help="server commands")

        @grp.command(name="start", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start server",
                     config_fields=["db.url"])
        def start(ctx, **kw):
            pass

        cmd = grp.commands["start"]
        assert cmd.config_fields == ("db.url",)

    def test_group_command_unknown_field_rejected(self):
        app = _build_app()
        grp = app.group(name="server", help="server commands")
        with pytest.raises(ValueError, match='unknown config field "missing"'):
            @grp.command(name="start", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start server",
                         config_fields=["missing"])
            def start(ctx, **kw):
                pass

    def test_nested_group_command_binding(self):
        app = _build_app()
        app.config_field("log_level", type=str, help="Log level")
        grp = app.group(name="server", help="server commands")
        sub = grp.group(name="db", help="database commands")

        @sub.command(name="migrate", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="run migrations",
                     config_fields=["log_level"])
        def migrate(ctx, **kw):
            pass

        cmd = sub.commands["migrate"]
        assert cmd.config_fields == ("log_level",)


# ---- Task 5d: Config field validation at startup ----


def _build_config_app(tmp_path, config_data=None, config_format="json"):
    """Build an app with config enabled, writing config_data to a temp file."""
    if config_format == "json":
        config_file = str(tmp_path / "config.json")
        if config_data is not None:
            with open(config_file, "w") as f:
                f.write(json.dumps(config_data, indent=2))
    else:
        config_file = str(tmp_path / "config.toml")
        if config_data is not None:
            # Write a simple TOML manually
            import tomllib
            with open(config_file, "w") as f:
                for k, v in config_data.items():
                    if isinstance(v, str):
                        f.write(f'{k} = "{v}"\n')
                    elif isinstance(v, bool):
                        f.write(f"{k} = {str(v).lower()}\n")
                    else:
                        f.write(f"{k} = {v}\n")

    app = strictcli.App(
        name="testapp", version="1.0.0", help="test app",
        config=True, config_path=config_file, config_format=config_format,
    )
    return app


class TestConfigFieldValidation:
    """Config field validation at startup (after command routing)."""

    def test_missing_required_field_errors(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["db.url"])
        def serve(ctx, **kw):
            pass

        result = app.test(["serve"])
        assert result.exit_code == 1
        assert 'required config field "db.url" is missing' in result.stderr

    def test_optional_missing_field_ok(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("cache.ttl", type=int, help="Cache TTL", default=3600)

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["cache.ttl"])
        def serve(ctx, **kw):
            return 0

        result = app.test(["serve"])
        assert result.exit_code == 0

    def test_type_mismatch_errors(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={"db": {"port": "not_int"}})
        app.config_field("db.port", type=int, help="DB port")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["db.port"])
        def serve(ctx, **kw):
            pass

        result = app.test(["serve"])
        assert result.exit_code == 1
        assert 'config field "db.port": expected int' in result.stderr

    def test_unknown_key_in_config_errors(self, tmp_path):
        app = _build_config_app(tmp_path,
                                config_data={"port": 8080, "totally_unknown": "value"})
        app.config_field("port", type=int, help="Port")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["port"])
        def serve(ctx, **kw):
            pass

        result = app.test(["serve"])
        assert result.exit_code == 1
        assert 'unknown key "totally_unknown" in config file' in result.stderr

    def test_valid_config_passes(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={"db": {"url": "postgres://..."}})
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["db.url"])
        def serve(ctx, **kw):
            return 0

        result = app.test(["serve"])
        assert result.exit_code == 0

    def test_flag_key_is_known(self, tmp_path):
        """Flag-backed keys are recognized and don't trigger unknown key error."""
        app = _build_config_app(tmp_path, config_data={"loud": True})
        app.config_field("port", type=int, help="Port", default=8080)

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["port"])
        @strictcli.flag("loud", type=bool, default=False, help="loud output")
        def serve(ctx, loud, **kw):
            return 0

        result = app.test(["serve"])
        assert result.exit_code == 0

    def test_framework_field_is_known(self, tmp_path):
        """Framework fields (underscore-prefixed) are recognized."""
        app = _build_config_app(tmp_path, config_data={"_schema_version": 1})
        app._register_framework_field(
            "_schema_version", type=int, help="config schema version"
        )

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            return 0

        result = app.test(["serve"])
        assert result.exit_code == 0

    def test_unbound_required_field_not_validated(self, tmp_path):
        """Required config fields NOT bound to the target command are not checked."""
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("db.url", type=str, help="Database URL")

        # This command does NOT bind db.url, so missing db.url is fine
        @app.command(name="ping", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="ping")
        def ping(ctx, **kw):
            return 0

        result = app.test(["ping"])
        assert result.exit_code == 0

    def test_bool_type_mismatch(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={"debug": 1})
        app.config_field("debug", type=bool, help="Debug mode")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["debug"])
        def serve(ctx, **kw):
            pass

        result = app.test(["serve"])
        assert result.exit_code == 1
        assert 'expected bool' in result.stderr

    def test_float_type_accepts_int(self, tmp_path):
        """Int values should be accepted for float fields."""
        app = _build_config_app(tmp_path, config_data={"rate": 5})
        app.config_field("rate", type=float, help="Rate limit")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["rate"])
        def serve(ctx, **kw):
            return 0

        result = app.test(["serve"])
        assert result.exit_code == 0


class TestConfigSubcommandExemption:
    """Config subcommands are exempt from config field validation."""

    def test_config_show_works_with_missing_required(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["db.url"])
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "show", "--plain"])
        assert result.exit_code == 0

    def test_config_path_works_with_invalid_config(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={"unknown_key": 42})
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["db.url"])
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "path"])
        assert result.exit_code == 0

    def test_config_set_works_with_missing_required(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["db.url"])
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "set", "db.url", "--value", "postgres://localhost/db"])
        assert result.exit_code == 0

    def test_config_init_works_with_missing_required(self, tmp_path):
        # Use a path that doesn't exist yet for init
        init_path = str(tmp_path / "new_config.json")
        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            config=True, config_path=init_path,
        )
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["db.url"])
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "init"])
        assert result.exit_code == 0
        assert os.path.isfile(init_path)


# ---- Task 5e: Updated config show and config set ----


class TestConfigShowWithFields:
    """config show displays config fields alongside flag-backed values."""

    def test_plain_output_includes_config_fields(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={"db": {"url": "pg://x"}})
        app.config_field("db.url", type=str, help="Database URL")
        app.config_field("cache.ttl", type=int, help="Cache TTL", default=3600)

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "show", "--plain"])
        assert result.exit_code == 0
        assert "Config fields:" in result.stdout
        assert "db.url" in result.stdout
        assert "pg://x" in result.stdout
        assert "config" in result.stdout  # source: config
        assert "cache.ttl" in result.stdout
        assert "3600" in result.stdout
        assert "Database URL" in result.stdout
        assert "required" in result.stdout
        assert "optional" in result.stdout

    def test_json_output_includes_config_fields(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={"db": {"url": "pg://x"}})
        app.config_field("db.url", type=str, help="Database URL")
        app.config_field("cache.ttl", type=int, help="Cache TTL", default=3600)

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "show", "--json"])
        assert result.exit_code == 0
        data = payload(result)
        assert "db.url" in data
        assert data["db.url"]["value"] == "pg://x"
        assert data["db.url"]["source"] == "config"
        assert data["db.url"]["type"] == "str"
        assert data["db.url"]["required"] is True
        assert "cache.ttl" in data
        assert data["cache.ttl"]["value"] == 3600
        assert data["cache.ttl"]["source"] == "default"
        assert data["cache.ttl"]["required"] is False

    def test_not_set_field_in_json(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "show", "--json"])
        data = payload(result)
        assert data["db.url"]["source"] == "not set"
        assert data["db.url"]["value"] is None


class TestConfigSetWithFields:
    """config set accepts config field names."""

    def test_set_config_field_str(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "set", "db.url", "--value", "postgres://localhost/db"])
        assert result.exit_code == 0

        # Verify the value was written
        config_file = str(tmp_path / "config.json")
        with open(config_file) as f:
            data = json.load(f)
        assert data["db"]["url"] == "postgres://localhost/db"

    def test_set_config_field_int(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("cache.ttl", type=int, help="Cache TTL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "set", "cache.ttl", "--value", "300"])
        assert result.exit_code == 0

        config_file = str(tmp_path / "config.json")
        with open(config_file) as f:
            data = json.load(f)
        assert data["cache"]["ttl"] == 300

    def test_set_config_field_bool(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("debug", type=bool, help="Debug mode")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "set", "debug", "--value", "true"])
        assert result.exit_code == 0

        config_file = str(tmp_path / "config.json")
        with open(config_file) as f:
            data = json.load(f)
        assert data["debug"] is True

    def test_set_config_field_float(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("rate", type=float, help="Rate limit")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "set", "rate", "--value", "1.5"])
        assert result.exit_code == 0

        config_file = str(tmp_path / "config.json")
        with open(config_file) as f:
            data = json.load(f)
        assert data["rate"] == 1.5

    def test_set_unknown_key_rejected(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "set", "unknown_key", "--value", "value"])
        assert result.exit_code == 1
        assert "unknown key" in result.stderr

    def test_set_config_field_type_error(self, tmp_path):
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("cache.ttl", type=int, help="Cache TTL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "set", "cache.ttl", "--value", "not_a_number"])
        assert result.exit_code == 1
        assert "cache.ttl" in result.stderr

    def test_set_config_field_default_removes_key(self, tmp_path):
        app = _build_config_app(tmp_path,
                                config_data={"db": {"url": "old_value"}})
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "set", "db.url", "--default"])
        assert result.exit_code == 0

        config_file = str(tmp_path / "config.json")
        with open(config_file) as f:
            data = json.load(f)
        # The db section should be cleaned up too
        assert "db" not in data or "url" not in data.get("db", {})

    def test_set_config_field_clear_rejected(self, tmp_path):
        """--clear is only for repeatable flags, not config fields."""
        app = _build_config_app(tmp_path, config_data={})
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "set", "db.url", "--clear"])
        assert result.exit_code == 1
        assert "--clear is only for repeatable flags" in result.stderr


# ---- Task 5f: config init ----


class TestConfigInit:
    """config init generates a template config file."""

    def test_json_template_generated(self, tmp_path):
        config_file = str(tmp_path / "config.json")
        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            config=True, config_path=config_file,
        )
        app.config_field("db.url", type=str, help="Database URL")
        app.config_field("cache.ttl", type=int, help="Cache TTL", default=3600)

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "init"])
        assert result.exit_code == 0
        assert os.path.isfile(config_file)

        with open(config_file) as f:
            data = json.load(f)
        # Required field should have None placeholder
        assert data["db"]["url"] is None
        # Optional field should have default
        assert data["cache"]["ttl"] == 3600

    def test_toml_template_generated(self, tmp_path):
        config_file = str(tmp_path / "config.toml")
        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            config=True, config_path=config_file, config_format="toml",
        )
        app.config_field("db.url", type=str, help="Database URL")
        app.config_field("cache.ttl", type=int, help="Cache TTL", default=3600)

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "init"])
        assert result.exit_code == 0
        assert os.path.isfile(config_file)

        content = open(config_file).read()
        assert "[db]" in content
        assert "Database URL" in content
        assert "[cache]" in content
        assert "ttl = 3600" in content

    def test_refuses_overwrite_existing(self, tmp_path):
        config_file = str(tmp_path / "config.json")
        with open(config_file, "w") as f:
            f.write("{}")

        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            config=True, config_path=config_file,
        )

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "init"])
        assert result.exit_code == 1
        assert "already exists" in result.stderr

    def test_prints_path_on_success(self, tmp_path):
        config_file = str(tmp_path / "config.json")
        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            config=True, config_path=config_file,
        )

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        result = app.test(["config", "init"])
        assert result.exit_code == 0
        assert config_file in result.stdout

    def test_json_template_with_flag_defaults(self, tmp_path):
        config_file = str(tmp_path / "config.json")
        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            config=True, config_path=config_file,
        )

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        @strictcli.flag("loud", type=bool, default=False, help="loud output")
        def serve(ctx, loud, **kw):
            pass

        result = app.test(["config", "init"])
        assert result.exit_code == 0

        with open(config_file) as f:
            data = json.load(f)
        # Bool flag defaults to False
        assert "loud" in data


# ---- Task 5g: Schema serialization ----


class TestSchemaWithConfigFields:
    """Schema output includes config_fields."""

    def test_schema_includes_config_fields(self):
        app = _build_app()
        app.config_field("db.url", type=str, help="Database URL")
        app.config_field("cache.ttl", type=int, help="Cache TTL", default=3600)

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["db.url", "cache.ttl"])
        def serve(ctx, **kw):
            pass

        from strictcli import _dump_schema
        schema = _dump_schema(app)

        assert "config_fields" in schema
        assert "db.url" in schema["config_fields"]
        # A config field is scalar-only in every implementation, so its
        # fragment is always a scalar row (§25.7). `required` stays beside it:
        # it means "the config file must contain it", not §23's presence.
        assert schema["config_fields"]["db.url"]["value_schema"] == {
            "type": "string",
        }
        assert "type" not in schema["config_fields"]["db.url"]
        assert schema["config_fields"]["db.url"]["help"] == "Database URL"
        assert schema["config_fields"]["db.url"]["required"] is True
        assert "default" not in schema["config_fields"]["db.url"]

        assert "cache.ttl" in schema["config_fields"]
        assert schema["config_fields"]["cache.ttl"]["value_schema"] == {
            "type": "integer",
        }
        assert schema["config_fields"]["cache.ttl"]["required"] is False
        assert schema["config_fields"]["cache.ttl"]["default"] == 3600

    def test_schema_includes_bound_commands(self):
        app = _build_app()
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["db.url"])
        def serve(ctx, **kw):
            pass

        @app.command(name="migrate", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="run migrations",
                     config_fields=["db.url"])
        def migrate(ctx, **kw):
            pass

        from strictcli import _dump_schema
        schema = _dump_schema(app)

        bound = schema["config_fields"]["db.url"]["bound_commands"]
        assert "serve" in bound
        assert "migrate" in bound

    def test_schema_no_config_fields_when_empty(self):
        app = _build_app()

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        from strictcli import _dump_schema
        schema = _dump_schema(app)

        assert "config_fields" not in schema

    def test_schema_command_includes_config_fields(self):
        app = _build_app()
        app.config_field("db.url", type=str, help="Database URL")

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start", config_fields=["db.url"])
        def serve(ctx, **kw):
            pass

        from strictcli import _dump_schema
        schema = _dump_schema(app)

        assert "config_fields" in schema["commands"]["serve"]
        assert schema["commands"]["serve"]["config_fields"] == ["db.url"]

    def test_schema_command_no_config_fields_when_empty(self):
        app = _build_app()

        @app.command(name="serve", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start")
        def serve(ctx, **kw):
            pass

        from strictcli import _dump_schema
        schema = _dump_schema(app)

        assert "config_fields" not in schema["commands"]["serve"]

    def test_schema_group_command_bindings(self):
        app = _build_app()
        app.config_field("db.url", type=str, help="Database URL")
        grp = app.group(name="server", help="server commands")

        @grp.command(name="start", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="start server",
                     config_fields=["db.url"])
        def start(ctx, **kw):
            pass

        from strictcli import _dump_schema
        schema = _dump_schema(app)

        bound = schema["config_fields"]["db.url"]["bound_commands"]
        assert "server start" in bound


# ---- ConfigField / Flag coexistence ----


class TestConfigFieldFlagCoexistence:
    """A config field colliding with a flag's param name is validation-only:
    it annotates the flag and renders once."""

    def _app_field_after_flag(self, tmp_path, config_data=None,
                              flag_default=strictcli._MISSING,
                              field_default=strictcli._MISSING):
        app = _build_config_app(tmp_path, config_data=config_data or {})
        # A flag declares a default or it declares requiredness; there is no
        # third spelling for "no default" (contract §23.1).
        flag_kwargs = (
            {"presence": "required"}
            if flag_default is strictcli._MISSING
            else {"default": flag_default}
        )

        @app.command(name="run", effect="read_only", help="run")
        @strictcli.flag("target", type=str, help="deploy target", **flag_kwargs)
        def run(ctx, target):
            pass

        app.config_field("target", type=str, help="the deploy target",
                         default=field_default)
        return app

    def test_show_plain_renders_key_once_with_annotation(self, tmp_path):
        app = self._app_field_after_flag(tmp_path, config_data={"target": "prod"})
        result = app.test(["config", "show", "--plain"])
        assert result.exit_code == 0
        # The flag line carries the config field help as a trailing annotation.
        target_lines = [ln for ln in result.stdout.splitlines() if ln.startswith("target ")]
        assert len(target_lines) == 1, result.stdout
        assert "-- the deploy target" in target_lines[0]
        # Not duplicated under a "Config fields:" section.
        assert "Config fields:" not in result.stdout

    def test_show_json_renders_key_once(self, tmp_path):
        app = self._app_field_after_flag(tmp_path, config_data={"target": "prod"})
        result = app.test(["config", "show", "--json"])
        assert result.exit_code == 0
        data = payload(result)
        # Single entry: the flag entry (value + source), not the config-field entry.
        assert data["target"]["value"] == "prod"
        assert data["target"]["source"] == "config"
        # The config-field-only keys must be absent (rendered as flag, once).
        assert "type" not in data["target"]
        assert "required" not in data["target"]

    def test_init_toml_renders_key_once_with_annotation(self, tmp_path):
        init_path = str(tmp_path / "cfg.toml")
        app = strictcli.App(name="testapp", version="1.0.0", help="t",
                            config=True, config_path=init_path, config_format="toml")

        @app.command(name="run", effect="read_only", help="run")
        @strictcli.flag("target", type=str, help="deploy target", default="prod")
        def run(ctx, target):
            pass

        app.config_field("target", type=str, help="the deploy target", default="prod")
        result = app.test(["config", "init"])
        assert result.exit_code == 0
        content = open(init_path).read()
        assert content.count("target =") == 1, content
        assert "-- the deploy target" in content

    def test_init_json_renders_key_once(self, tmp_path):
        init_path = str(tmp_path / "cfg.json")
        app = strictcli.App(name="testapp", version="1.0.0", help="t",
                            config=True, config_path=init_path)

        @app.command(name="run", effect="read_only", help="run")
        @strictcli.flag("target", type=str, help="deploy target", default="prod")
        def run(ctx, target):
            pass

        app.config_field("target", type=str, help="the deploy target", default="prod")
        result = app.test(["config", "init"])
        assert result.exit_code == 0
        data = json.loads(open(init_path).read())
        assert list(data.keys()).count("target") == 1
        assert data["target"] == "prod"

    def test_unequal_defaults_field_after_flag_raises(self, tmp_path):
        with pytest.raises(ValueError, match="defaults disagree"):
            self._app_field_after_flag(tmp_path, flag_default="prod",
                                       field_default="staging")

    def test_unequal_defaults_flag_after_field_raises(self, tmp_path):
        app = _build_config_app(tmp_path)
        app.config_field("target", type=str, help="the deploy target",
                         default="staging")
        with pytest.raises(ValueError, match="defaults disagree"):
            @app.command(name="run", effect="read_only", help="run")
            @strictcli.flag("target", type=str, help="deploy target", default="prod")
            def run(ctx, target):
                pass

    def test_equal_defaults_ok(self, tmp_path):
        # Should not raise.
        self._app_field_after_flag(tmp_path, flag_default="prod",
                                   field_default="prod")

    def test_one_absent_default_ok(self, tmp_path):
        # Flag has default, config field has none -> OK (flag wins).
        self._app_field_after_flag(tmp_path, flag_default="prod")
        # Field has default, flag has none -> OK.
        self._app_field_after_flag(tmp_path, field_default="prod")
