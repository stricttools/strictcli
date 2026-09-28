"""Tests for the connection-env primitive: app-level declaration, lazy read,
hermetic suppression, check-side access, registration-time enforcement, and
precedence (cli > env)."""

import json
from dataclasses import dataclass
from pathlib import Path

import pytest

import strictcli
from strictcli import App, Context


# --- Declaration + help + schema surfacing ---


def test_connection_env_declaration():
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "Postgres connection string"})
    assert app._connection_envs["DATABASE_URL"] == "Postgres connection string"
    assert app._connection_order == ["DATABASE_URL"]


def test_connection_env_help_rendering():
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "Postgres connection string"})

    @app.command("run", effect="read_only", help="run it")
    def run(ctx):
        return 0

    r = app.test(["--help"])
    assert "Infrastructure:" in r.stdout
    assert "DATABASE_URL" in r.stdout
    assert "connection URL, suppressed by --hermetic (Postgres connection string)" in r.stdout


def test_connection_env_schema_dump():
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "Postgres connection string"})

    @app.command("run", effect="read_only", help="run it")
    def run(ctx):
        return 0

    from strictcli import _dump_schema_core
    schema = _dump_schema_core(app)
    conns = schema["infra"]["connections"]
    assert conns == [{"env_var": "DATABASE_URL", "help": "Postgres connection string"}]
    json.dumps(schema)  # must marshal


# --- Lazy read + precedence (cli > env) ---


def _make_conn_app():
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "conn"})
    captured = {}

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag("dsn", help="connection string", default="",
                    connection_url=True, connection_env="DATABASE_URL")
    def run(ctx, dsn):
        captured["dsn"] = dsn
        return 0

    return app, captured


def test_connection_env_lazy_read_from_env(monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "postgres://from-env/db")
    app, captured = _make_conn_app()
    r = app.test(["run"])
    assert r.exit_code == 0, r.stderr
    assert captured["dsn"] == "postgres://from-env/db"
    assert app._last_sources["dsn"] == "env"


def test_connection_env_cli_beats_env(monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "postgres://from-env/db")
    app, captured = _make_conn_app()
    r = app.test(["run", "--dsn", "postgres://from-cli/db"])
    assert r.exit_code == 0, r.stderr
    assert captured["dsn"] == "postgres://from-cli/db"
    assert app._last_sources["dsn"] == "cli"


def test_connection_env_hermetic_suppresses_flag(monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "postgres://from-env/db")
    app, captured = _make_conn_app()
    r = app.test(["--hermetic", "run"])
    assert r.exit_code == 0, r.stderr
    assert captured["dsn"] != "postgres://from-env/db"
    assert app._last_sources["dsn"] != "env"


# --- Presence: a REQUIRED connection-URL flag (contract §23.5's env row) ---


def _make_required_conn_app():
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "conn"})
    captured = {}

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag("dsn", help="connection string", presence="required",
                    connection_url=True, connection_env="DATABASE_URL")
    def run(ctx, dsn):
        captured["dsn"] = dsn
        captured["source"] = ctx.source("dsn")
        captured["provided"] = ctx.provided("dsn")
        return 0

    return app, captured


def test_required_connection_flag_satisfied_by_env(monkeypatch):
    """The bound connection env satisfies requiredness -- it is §23.5's env row,
    with no extra guard for the URL class."""
    monkeypatch.setenv("DATABASE_URL", "postgres://from-env/db")
    app, captured = _make_required_conn_app()
    r = app.test(["run"])
    assert r.exit_code == 0, r.stderr
    assert captured["dsn"] == "postgres://from-env/db"
    assert captured["source"] == "env"
    assert captured["provided"] is True


def test_required_connection_flag_env_unset_is_the_required_error(monkeypatch):
    monkeypatch.delenv("DATABASE_URL", raising=False)
    app, _ = _make_required_conn_app()
    r = app.test(["run"])
    assert r.exit_code == 1
    assert r.stderr == "error: flag '--dsn' is required\ntry 'myapp run --help'\n"


def test_required_connection_flag_cli_beats_env(monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "postgres://from-env/db")
    app, captured = _make_required_conn_app()
    r = app.test(["run", "--dsn", "postgres://from-cli/db"])
    assert r.exit_code == 0, r.stderr
    assert captured["dsn"] == "postgres://from-cli/db"
    assert captured["source"] == "cli"
    assert captured["provided"] is True


def test_required_connection_flag_hermetic_suppresses_env(monkeypatch):
    """--hermetic suppresses the connection env, so requiredness is unsatisfied
    and the required error fires even though the variable is set."""
    monkeypatch.setenv("DATABASE_URL", "postgres://from-env/db")
    app, _ = _make_required_conn_app()
    r = app.test(["--hermetic", "run"])
    assert r.exit_code == 1
    assert r.stderr == "error: flag '--dsn' is required\ntry 'myapp run --help'\n"


# --- Handler-side infra_value / connection_env_value ---


def test_connection_env_infra_value_live(monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "postgres://live/db")
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "conn"})
    captured = {}

    @app.command("run", effect="read_only", help="run it")
    def run(ctx):
        captured["conn"] = ctx.connection_env_value("DATABASE_URL")
        captured["infra"] = ctx.infra_value("DATABASE_URL")
        return 0

    app.test(["run"])
    assert captured["conn"] == ("postgres://live/db", True)
    assert captured["infra"] == ("postgres://live/db", True)


def test_connection_env_hermetic_suppresses_infra_value(monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "postgres://live/db")
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "conn"})
    captured = {}

    @app.command("run", effect="read_only", help="run it")
    def run(ctx):
        captured["conn"] = ctx.connection_env_value("DATABASE_URL")
        return 0

    app.test(["--hermetic", "run"])
    assert captured["conn"] == (None, False)


def test_connection_env_undeclared_value_raises(monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "postgres://live/db")
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "conn"})
    captured = {}

    @app.command("run", effect="read_only", help="run it")
    def run(ctx):
        try:
            ctx.connection_env_value("NOPE")
        except KeyError as e:
            captured["err"] = str(e)
        return 0

    app.test(["run"])
    assert "NOPE" in captured["err"]


# --- Check-side access via ConnectionEnvReader ---

CONN_CHECKS_TOML = """
app = "myapp"

[checks.db-reachable]
description = "Checks db-reachable"
subject = "quality"
tags = ["db"]
severity = "error"
fast = true
pure = false
needs_network = true
depends_on = []
"""


@dataclass
class SimpleContext:
    project_root: Path


def _make_conn_check_app(tmp_path):
    toml_file = tmp_path / "checks.toml"
    toml_file.write_text(CONN_CHECKS_TOML)
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "conn"},
              checks_path=str(toml_file))

    @app.error_check("db-reachable")
    def db_reachable(ctx, reporter):
        reporter.note(f"hermetic={ctx.is_hermetic()}")
        dsn, present = ctx.connection_env_value("DATABASE_URL")
        if not present:
            # Distinguish hermetic suppression from a plainly-unset env: a
            # consumer layering config fallbacks below the env must honor
            # hermetic (skip) rather than fall through and connect.
            if ctx.is_hermetic():
                return reporter.skipped("DATABASE_URL suppressed by --hermetic")
            return reporter.skipped("DATABASE_URL unset")
        reporter.note(f"dsn={dsn}")
        return reporter.passed("connection env visible")

    app.set_check_context(lambda: SimpleContext(project_root=tmp_path))
    return app


def test_connection_env_check_side_access(tmp_path, monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "postgres://check/db")
    app = _make_conn_check_app(tmp_path)
    r = app.test(["--verbose", "check", "--tag", "db"])
    assert "dsn=postgres://check/db" in r.stdout
    assert "PASS" in r.stdout
    # A non-hermetic invocation reports is_hermetic()==False to the check.
    assert "hermetic=False" in r.stdout


def test_connection_env_check_side_hermetic_skips(tmp_path, monkeypatch):
    monkeypatch.setenv("DATABASE_URL", "postgres://check/db")
    app = _make_conn_check_app(tmp_path)
    r = app.test(["--hermetic", "--verbose", "check", "--tag", "db"])
    assert "SKIP" in r.stdout
    assert "dsn=" not in r.stdout
    # The check SEES hermetic (is_hermetic()==True) and skips for that reason.
    assert "hermetic=True" in r.stdout
    assert "suppressed by --hermetic" in r.stdout


def test_connection_env_check_side_hermetic_conflation(tmp_path, monkeypatch):
    """Documents the exact gap is_hermetic() closes: under --hermetic with the
    env var UNSET, connection_env_value returns present=False (indistinguishable
    from a plain unset), yet is_hermetic() returns True -- so a check layering
    config fallbacks can honor hermetic instead of connecting via a config URL."""
    monkeypatch.delenv("DATABASE_URL", raising=False)  # env UNSET
    app = _make_conn_check_app(tmp_path)
    r = app.test(["--hermetic", "--verbose", "check", "--tag", "db"])
    assert "hermetic=True" in r.stdout
    assert "suppressed by --hermetic" in r.stdout
    assert "DATABASE_URL unset" not in r.stdout


def test_connection_env_check_side_unset_not_hermetic(tmp_path, monkeypatch):
    """Counterpart: env unset and NOT hermetic -> is_hermetic()==False, so a
    consumer is free to consult a config fallback (here reported as plain unset)."""
    monkeypatch.delenv("DATABASE_URL", raising=False)  # env UNSET, no --hermetic
    app = _make_conn_check_app(tmp_path)
    r = app.test(["--verbose", "check", "--tag", "db"])
    assert "hermetic=False" in r.stdout
    assert "DATABASE_URL unset" in r.stdout


# --- Registration-time enforcement ---


def test_connection_url_flag_unbound_raises():
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "conn"})
    with pytest.raises(ValueError, match="must bind to a declared connection env"):
        @app.command("run", effect="read_only", help="run it")
        @strictcli.flag("dsn", help="dsn", presence="optional", connection_url=True)
        def run(ctx, dsn):
            return 0


def test_connection_url_flag_undeclared_binding_raises():
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "conn"})
    with pytest.raises(ValueError, match="undeclared connection env"):
        @app.command("run", effect="read_only", help="run it")
        @strictcli.flag("dsn", help="dsn", presence="optional",
                        connection_url=True, connection_env="OTHER_URL")
        def run(ctx, dsn):
            return 0


def test_connection_env_binding_without_url_marker_raises():
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "conn"})
    with pytest.raises(ValueError, match="requires the flag to be marked as a connection-URL flag"):
        @app.command("run", effect="read_only", help="run it")
        @strictcli.flag("dsn", help="dsn", presence="optional", connection_env="DATABASE_URL")
        def run(ctx, dsn):
            return 0


def test_connection_env_binding_plus_per_flag_env_raises():
    app = App(name="myapp", version="1.0.0", help="t",
              connection_env={"DATABASE_URL": "conn"})
    with pytest.raises(ValueError, match="cannot be combined with a per-flag env var"):
        @app.command("run", effect="read_only", help="run it")
        @strictcli.flag("dsn", help="dsn", presence="optional", env="SOMETHING_ELSE",
                        connection_url=True, connection_env="DATABASE_URL")
        def run(ctx, dsn):
            return 0


def test_connection_env_empty_help_raises():
    with pytest.raises(ValueError, match="help must be a non-empty string"):
        App(name="myapp", version="1.0.0", help="t",
            connection_env={"DATABASE_URL": ""})


def test_connection_env_collides_with_root_raises():
    with pytest.raises(ValueError, match="already declared as an infra root"):
        App(name="myapp", version="1.0.0", help="t",
            infra_root={"SHARED": "/var/lib"},
            connection_env={"SHARED": "conn"})


def test_connection_env_collides_with_handshake_raises():
    with pytest.raises(ValueError, match="already declared as a handshake env var"):
        App(name="myapp", version="1.0.0", help="t",
            handshake_env={"SHARED": "handshake"},
            connection_env={"SHARED": "conn"})
