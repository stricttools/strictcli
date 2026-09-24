"""Tests for provenance: env and config source attribution."""

import json
import os
import tempfile

from conftest import payload

import strictcli


# ---------------------------------------------------------------------------
# Env and config source attribution
# ---------------------------------------------------------------------------


def test_env_source_label(monkeypatch):
    """A flag set by an environment variable gets source 'env'."""
    monkeypatch.setenv("MYAPP_LEVEL", "42")

    app = strictcli.App(name="myapp", version="1.0.0", help="test app",
                        env_prefix="MYAPP")

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag("level", type=int, env="MYAPP_LEVEL", help="level", presence="required")
    def run(ctx, level):
        return 0

    r = app.test(["run"])
    assert r.exit_code == 0, r.stderr
    assert app._last_sources["level"] == "env"


def test_config_source_label():
    """A flag set by config file gets source 'config'."""
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump({"level": 7}, f)
        config_path = f.name

    try:
        app = strictcli.App(name="myapp", version="1.0.0", help="test app",
                            config=True, config_path=config_path)

        @app.command("run", effect="read_only", help="run it")
        @strictcli.flag("level", type=int, default=0, help="level")
        def run(ctx, level):
            return 0

        r = app.test(["run"])
        assert r.exit_code == 0, r.stderr
        assert app._last_sources["level"] == "config"
    finally:
        os.unlink(config_path)


def test_cli_overrides_config():
    """A flag set by CLI overriding config gets source 'cli'."""
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump({"level": 7}, f)
        config_path = f.name

    try:
        app = strictcli.App(name="myapp", version="1.0.0", help="test app",
                            config=True, config_path=config_path)

        @app.command("run", effect="read_only", help="run it")
        @strictcli.flag("level", type=int, default=0, help="level")
        def run(ctx, level):
            return 0

        r = app.test(["run", "--level", "99"])
        assert r.exit_code == 0, r.stderr
        assert app._last_sources["level"] == "cli"
    finally:
        os.unlink(config_path)


def test_cli_overrides_env(monkeypatch):
    """A flag set by CLI overriding env gets source 'cli'."""
    monkeypatch.setenv("MYAPP_LEVEL", "42")

    app = strictcli.App(name="myapp", version="1.0.0", help="test app",
                        env_prefix="MYAPP")

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag("level", type=int, env="MYAPP_LEVEL", help="level", presence="required")
    def run(ctx, level):
        return 0

    r = app.test(["run", "--level", "99"])
    assert r.exit_code == 0, r.stderr
    assert app._last_sources["level"] == "cli"


def test_env_overrides_config(monkeypatch):
    """A flag set by env overriding config gets source 'env'."""
    monkeypatch.setenv("MYAPP_LEVEL", "42")

    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump({"level": 7}, f)
        config_path = f.name

    try:
        app = strictcli.App(name="myapp", version="1.0.0", help="test app",
                            env_prefix="MYAPP", config=True,
                            config_path=config_path)

        @app.command("run", effect="read_only", help="run it")
        @strictcli.flag("level", type=int, env="MYAPP_LEVEL", default=0,
                        help="level")
        def run(ctx, level):
            return 0

        r = app.test(["run"])
        assert r.exit_code == 0, r.stderr
        assert app._last_sources["level"] == "env"
    finally:
        os.unlink(config_path)


def test_default_with_config_available_but_absent():
    """A defaulted flag with config available but key absent gets source 'default'."""
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump({}, f)
        config_path = f.name

    try:
        app = strictcli.App(name="myapp", version="1.0.0", help="test app",
                            config=True, config_path=config_path)

        @app.command("run", effect="read_only", help="run it")
        @strictcli.flag("level", type=int, default=0, help="level")
        def run(ctx, level):
            return 0

        r = app.test(["run"])
        assert r.exit_code == 0, r.stderr
        assert app._last_sources["level"] == "default"
    finally:
        os.unlink(config_path)


# ---------------------------------------------------------------------------
# Global flag source attribution
# ---------------------------------------------------------------------------


def test_global_flag_env_source(monkeypatch):
    """A global flag set by env gets source 'env'."""
    monkeypatch.setenv("MYAPP_VERBOSE", "true")

    app = strictcli.App(
        name="myapp", version="1.0.0", help="test app",
        env_prefix="MYAPP",
        flags=[
            strictcli.Flag(name="loud", type=bool, env="MYAPP_VERBOSE",
                           default=False, help="loud"),
        ],
    )

    @app.command("run", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="run it")
    def run(ctx, **kw):
        return 0

    r = app.test(["run"])
    assert r.exit_code == 0, r.stderr
    assert app._last_sources["loud"] == "env"


def test_global_flag_config_source():
    """A global flag set by config gets source 'config'."""
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump({"loud": True}, f)
        config_path = f.name

    try:
        app = strictcli.App(
            name="myapp", version="1.0.0", help="test app",
            config=True, config_path=config_path,
            flags=[
                strictcli.Flag(name="loud", type=bool, default=False,
                               help="loud"),
            ],
        )

        @app.command("run", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="run it")
        def run(ctx, **kw):
            return 0

        r = app.test(["run"])
        assert r.exit_code == 0, r.stderr
        assert app._last_sources["loud"] == "config"
    finally:
        os.unlink(config_path)


def test_global_flag_cli_source():
    """A global flag passed on CLI gets source 'cli'."""
    app = strictcli.App(
        name="myapp", version="1.0.0", help="test app",
        flags=[
            strictcli.Flag(name="loud", type=bool, default=False,
                           help="loud"),
        ],
    )

    @app.command("run", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="run it")
    def run(ctx, **kw):
        return 0

    r = app.test(["--loud", "run"])
    assert r.exit_code == 0, r.stderr
    assert app._last_sources["loud"] == "cli"


def test_global_flag_default_source():
    """A global flag with only a default gets source 'default'."""
    app = strictcli.App(
        name="myapp", version="1.0.0", help="test app",
        flags=[
            strictcli.Flag(name="loud", type=bool, default=False,
                           help="loud"),
        ],
    )

    @app.command("run", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="run it")
    def run(ctx, **kw):
        return 0

    r = app.test(["run"])
    assert r.exit_code == 0, r.stderr
    assert app._last_sources["loud"] == "default"


# ---------------------------------------------------------------------------
# config show reports env source
# ---------------------------------------------------------------------------


def test_config_show_reports_env(monkeypatch):
    """Config show reports 'env' for a flag whose value comes from an env var."""
    monkeypatch.setenv("MYAPP_LEVEL", "42")

    app = strictcli.App(name="myapp", version="1.0.0", help="test app",
                        env_prefix="MYAPP", config=True)

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag("level", type=int, env="MYAPP_LEVEL", default=0,
                    help="level")
    def run(ctx, level):
        return 0

    r = app.test(["config", "show", "--json"])
    assert r.exit_code == 0, r.stderr
    result = payload(r)
    assert result["level"]["source"] == "env"


def test_config_show_reports_config():
    """Config show reports 'config' for a flag set in the config file."""
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump({"level": 7}, f)
        config_path = f.name

    try:
        app = strictcli.App(name="myapp", version="1.0.0", help="test app",
                            config=True, config_path=config_path)

        @app.command("run", effect="read_only", help="run it")
        @strictcli.flag("level", type=int, default=0, help="level")
        def run(ctx, level):
            return 0

        r = app.test(["config", "show", "--json"])
        assert r.exit_code == 0, r.stderr
        result = payload(r)
        assert result["level"]["source"] == "config"
    finally:
        os.unlink(config_path)


def test_config_show_reports_default():
    """Config show reports 'default' for a flag with no config or env value."""
    app = strictcli.App(name="myapp", version="1.0.0", help="test app",
                        config=True)

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag("level", type=int, default=0, help="level")
    def run(ctx, level):
        return 0

    r = app.test(["config", "show", "--json"])
    assert r.exit_code == 0, r.stderr
    result = payload(r)
    assert result["level"]["source"] == "default"


# ---------------------------------------------------------------------------
# Invoke/call-path provenance
# ---------------------------------------------------------------------------


def test_invoke_provided_kwarg_source_is_cli():
    """A kwarg provided via invoke/call gets source 'cli'."""
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag("level", type=int, default=0, help="level")
    def run(ctx, level):
        return 0

    result = app.call("run", level=42)
    assert result == 0
    assert app._last_sources["level"] == "cli"


def test_invoke_absent_kwarg_source_is_default():
    """A kwarg not provided via invoke (and thus defaulted) gets source 'default'."""
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag("level", type=int, default=0, help="level")
    def run(ctx, level):
        return 0

    result = app.call("run")
    assert result == 0
    assert app._last_sources["level"] == "default"


# ---------------------------------------------------------------------------
# Byte-parity: config show output format
# ---------------------------------------------------------------------------


def test_config_show_plain_format():
    """Config show plain output contains 'level = 0  (source: default)'."""
    app = strictcli.App(name="myapp", version="1.0.0", help="test app",
                        config=True)

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag("level", type=int, default=0, help="level")
    def run(ctx, level):
        return 0

    r = app.test(["config", "show", "--plain"])
    assert r.exit_code == 0, r.stderr
    assert "level = 0  (source: default)" in r.stdout
