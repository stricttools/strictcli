"""Tests for the --hermetic reserved flag."""

import json
import os
import tempfile

import pytest

import strictcli


# ---------------------------------------------------------------------------
# Reserve and intercept
# ---------------------------------------------------------------------------


def test_hermetic_reserved_name():
    """Verify that 'hermetic' cannot be used as a user-defined global flag."""
    with pytest.raises(ValueError, match="reserved"):
        strictcli.App(
            name="myapp", version="1.0.0", help="test app",
            flags=[strictcli.Flag(name="hermetic", type=bool, help="should be rejected", default=False)],
        )


def test_hermetic_skips_env(monkeypatch):
    """Under --hermetic, env vars are ignored for command flags."""
    monkeypatch.setenv("MYAPP_LEVEL", "42")

    app = strictcli.App(name="myapp", version="1.0.0", help="test app", env_prefix="MYAPP")

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag(name="level", type=int, help="verbosity", env="MYAPP_LEVEL", default=0)
    def run(ctx, level):
        print(f"level={level}")
        return 0

    r = app.test(["--hermetic", "run"])
    assert r.exit_code == 0
    assert "level=0" in r.stdout


def test_hermetic_skips_env_global_flags(monkeypatch):
    """Under --hermetic, env vars are ignored for global flags."""
    monkeypatch.setenv("MYAPP_VERBOSE", "true")

    app = strictcli.App(
        name="myapp", version="1.0.0", help="test app", env_prefix="MYAPP",
        flags=[strictcli.Flag(name="loud", type=bool, help="loud", env="MYAPP_VERBOSE", default=False)],
    )

    @app.command("run", effect="read_only", help="run it")
    def run(ctx, loud):
        print(f"loud={'true' if loud else 'false'}")
        return 0

    r = app.test(["--hermetic", "run"])
    assert r.exit_code == 0
    assert "loud=false" in r.stdout


def test_hermetic_cli_flag_still_works(monkeypatch):
    """Under --hermetic, CLI-passed flags still work."""
    monkeypatch.setenv("MYAPP_LEVEL", "42")

    app = strictcli.App(name="myapp", version="1.0.0", help="test app", env_prefix="MYAPP")

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag(name="level", type=int, help="verbosity", env="MYAPP_LEVEL", default=0)
    def run(ctx, level):
        print(f"level={level}")
        return 0

    r = app.test(["--hermetic", "run", "--level", "99"])
    assert r.exit_code == 0
    assert "level=99" in r.stdout


def test_hermetic_skips_config():
    """Under --hermetic, config files are ignored."""
    with tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False) as f:
        json.dump({"level": 7}, f)
        config_path = f.name

    try:
        app = strictcli.App(
            name="myapp", version="1.0.0", help="test app",
            config=True, config_path=config_path,
        )

        @app.command("run", effect="read_only", help="run it")
        @strictcli.flag(name="level", type=int, help="verbosity", default=0)
        def run(ctx, level):
            print(f"level={level}")
            return 0

        r = app.test(["--hermetic", "run"])
        assert r.exit_code == 0
        assert "level=0" in r.stdout
    finally:
        os.unlink(config_path)


def test_hermetic_config_mutual_exclusion():
    """--hermetic + --config together is a hard error."""
    app = strictcli.App(name="myapp", version="1.0.0", help="test app", config=True)

    @app.command("run", effect="read_only", help="run it")
    def run(ctx):
        return 0

    r = app.test(["--hermetic", "--config", "/tmp/foo.json", "run"])
    assert r.exit_code == 1
    assert "--hermetic and --config are mutually exclusive" in r.stderr


def test_hermetic_config_subcommand_error():
    """--hermetic + config subcommand is a hard error."""
    app = strictcli.App(name="myapp", version="1.0.0", help="test app", config=True)

    @app.command("run", effect="read_only", help="run it")
    def run(ctx):
        return 0

    r = app.test(["--hermetic", "config", "show"])
    assert r.exit_code == 1
    assert "--hermetic cannot be used with config commands" in r.stderr


def test_hermetic_required_flag_missing_env_set(monkeypatch):
    """Under --hermetic, a required flag that env would satisfy produces an error."""
    monkeypatch.setenv("MYAPP_NAME", "test-value")

    app = strictcli.App(name="myapp", version="1.0.0", help="test app", env_prefix="MYAPP")

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag(name="name", type=str, help="name", env="MYAPP_NAME", presence="required")
    def run(ctx, name):
        return 0

    r = app.test(["--hermetic", "run"])
    assert r.exit_code == 1
    assert "required" in r.stderr


def test_hermetic_on_app_without_config(monkeypatch):
    """--hermetic on an app without config is fine (still disables env)."""
    monkeypatch.setenv("MYAPP_LEVEL", "42")

    app = strictcli.App(name="myapp", version="1.0.0", help="test app", env_prefix="MYAPP")

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag(name="level", type=int, help="verbosity", env="MYAPP_LEVEL", default=0)
    def run(ctx, level):
        print(f"level={level}")
        return 0

    r = app.test(["--hermetic", "run"])
    assert r.exit_code == 0
    assert "level=0" in r.stdout


def test_hermetic_required_bool_missing():
    """Under --hermetic, a required bool with no CLI value is an error."""
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag(name="loud", type=bool, help="loud mode", presence="required")
    def run(ctx, loud):
        return 0

    r = app.test(["--hermetic", "run"])
    assert r.exit_code == 1
    assert "must be passed" in r.stderr


def test_hermetic_source_is_default_when_env_set(monkeypatch):
    """Under --hermetic, a flag with env set gets source 'default', not 'env'."""
    monkeypatch.setenv("MYAPP_LEVEL", "42")

    app = strictcli.App(name="myapp", version="1.0.0", help="test app", env_prefix="MYAPP")

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag(name="level", type=int, help="verbosity", env="MYAPP_LEVEL", default=0)
    def run(ctx, level):
        return 0

    r = app.test(["--hermetic", "run"])
    assert r.exit_code == 0
    assert app._last_sources["level"] == "default"


def test_hermetic_source_is_cli_for_passed_flag():
    """Under --hermetic, a flag passed on CLI gets source 'cli'."""
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag(name="level", type=int, help="verbosity", default=0)
    def run(ctx, level):
        return 0

    r = app.test(["--hermetic", "run", "--level", "5"])
    assert r.exit_code == 0
    assert app._last_sources["level"] == "cli"
