"""Tests for JSON config file support."""

import json
import os
import tomllib

import pytest
from conftest import payload

import strictcli


def _make_config_app(config=True, flags=None):
    """Helper: app with config support and a simple command."""
    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=config,
        flags=flags or [],
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("target", type=str, help="the target", default="default-val")
    @strictcli.flag("count", type=int, help="how many", default=1)
    @strictcli.flag("loud", type=bool, default=False, help="be loud")
    def run(ctx, target, count, loud):
        print(f"target={target} count={count} loud={loud}")

    return app


def _write_config(tmp_path, app_name, data):
    """Write a config JSON file and return its directory."""
    config_dir = tmp_path / app_name
    config_dir.mkdir(parents=True, exist_ok=True)
    config_file = config_dir / "config.json"
    config_file.write_text(json.dumps(data, indent=2) + "\n")
    return str(tmp_path)


# --- Test 1: config=False has no config subcommand ---

def test_no_config_group_when_disabled():
    """App with config=False has no config subcommand."""
    app = _make_config_app(config=False)
    r = app.test(["config", "path"])
    assert r.exit_code == 1
    assert "unknown command" in r.stderr


# --- Test 2: config=True has config group with subcommands ---

def test_config_group_exists():
    """App with config=True has config group with show/set/path/edit subcommands."""
    app = _make_config_app(config=True)
    r = app.test(["config", "--help"])
    assert r.exit_code == 0
    assert "show" in r.stdout
    assert "set" in r.stdout
    assert "path" in r.stdout
    assert "edit" in r.stdout


# --- Test 3: config values used when no CLI or env ---

def test_config_value_used(tmp_path, monkeypatch):
    """Config file values are used when no CLI or env value is provided."""
    config_home = _write_config(tmp_path, "testapp", {"target": "from-config"})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    # Must create the app AFTER setting XDG_CONFIG_HOME so it loads the config
    app = _make_config_app(config=True)
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "target=from-config" in r.stdout


# --- Test 4: CLI flag overrides config value ---

def test_cli_overrides_config(tmp_path, monkeypatch):
    """CLI flag overrides config value."""
    config_home = _write_config(tmp_path, "testapp", {"target": "from-config"})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["run", "--target", "from-cli"])
    assert r.exit_code == 0
    assert "target=from-cli" in r.stdout


# --- Test 5: env var overrides config value ---

def test_env_overrides_config(tmp_path, monkeypatch):
    """Env var overrides config value."""
    config_home = _write_config(tmp_path, "testapp", {"target": "from-config"})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        env_prefix="TESTAPP",
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("target", type=str, help="the target", default="default-val",
                     env="TESTAPP_TARGET")
    def run(ctx, target):
        print(f"target={target}")

    monkeypatch.setenv("TESTAPP_TARGET", "from-env")
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "target=from-env" in r.stdout


# --- Test 6: config value overrides default ---

def test_config_overrides_default(tmp_path, monkeypatch):
    """Config value overrides code default."""
    config_home = _write_config(tmp_path, "testapp", {"count": 42})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "count=42" in r.stdout
    # Also verify default was NOT used
    assert "count=1" not in r.stdout


# --- Test 7: invalid JSON is a hard error with position ---

def test_invalid_json_hard_error(tmp_path, monkeypatch):
    """Config with invalid JSON is a hard error with position information."""
    config_dir = tmp_path / "testapp"
    config_dir.mkdir(parents=True, exist_ok=True)
    config_file = config_dir / "config.json"
    config_file.write_text("{invalid json!!")
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_app(config=True)
    r = app.test(["run"])
    assert r.exit_code == 1
    assert "config file" in r.stderr
    assert "line" in r.stderr
    assert "column" in r.stderr


# --- Test 8: config set creates file and directory ---

def test_config_set_creates_file(tmp_path, monkeypatch):
    """Config set creates file and directory if needed."""
    config_home = str(tmp_path)
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["config", "set", "target", "--value", "new-value"])
    assert r.exit_code == 0

    # Verify the file was created
    config_file = tmp_path / "testapp" / "config.json"
    assert config_file.exists()
    data = json.loads(config_file.read_text())
    assert data["target"] == "new-value"


# --- Test 9: config path prints the correct path ---

def test_config_path(tmp_path, monkeypatch):
    """Config path prints the correct path."""
    config_home = str(tmp_path)
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["config", "path"])
    assert r.exit_code == 0
    expected = os.path.join(config_home, "testapp", "config.json")
    assert expected in r.stdout


# --- Test 10: config show displays values with source attribution ---

def test_config_show(tmp_path, monkeypatch):
    """Config show displays values with source attribution."""
    config_home = _write_config(tmp_path, "testapp", {"target": "from-config"})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["config", "show", "--plain"])
    assert r.exit_code == 0
    assert "target = from-config  (source: config)" in r.stdout
    assert "count = 1  (source: default)" in r.stdout
    assert "loud = false  (source: default)" in r.stdout


# --- Test 10b: config show formats bools as lowercase (Go parity) ---

def test_config_show_lowercase_bools(tmp_path, monkeypatch):
    """Config show formats bools as lowercase true/false, matching Go output."""
    config_home = _write_config(tmp_path, "testapp", {"loud": True})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["config", "show", "--plain"])
    assert r.exit_code == 0
    # Bool from config must be lowercase
    assert "loud = true  (source: config)" in r.stdout
    # None (no default for bool) must show as <nil>
    assert "loud = True" not in r.stdout
    assert "loud = False" not in r.stdout


def test_config_show_bool_false_default_lowercase(tmp_path, monkeypatch):
    """Config show formats default False bool as lowercase false."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_app(config=True)
    r = app.test(["config", "show", "--plain"])
    assert r.exit_code == 0
    # loud has no explicit default, defaults to False -- must be lowercase
    assert "loud = false  (source: default)" in r.stdout
    assert "loud = False" not in r.stdout


# --- Test 11: config respects XDG_CONFIG_HOME ---

def test_xdg_config_home(tmp_path, monkeypatch):
    """Config respects XDG_CONFIG_HOME env var."""
    custom_dir = tmp_path / "custom-config"
    config_home = _write_config(custom_dir, "testapp", {"target": "xdg-value"})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "target=xdg-value" in r.stdout


# --- Additional: config with bool values ---

def test_config_bool_value(tmp_path, monkeypatch):
    """Config file with bool values works correctly."""
    config_home = _write_config(tmp_path, "testapp", {"loud": True})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "loud=True" in r.stdout


# --- Additional: config with int values ---

def test_config_int_value(tmp_path, monkeypatch):
    """Config file with int values works correctly."""
    config_home = _write_config(tmp_path, "testapp", {"count": 99})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "count=99" in r.stdout


# --- Additional: config file does not exist returns empty dict ---

def test_config_missing_file(tmp_path, monkeypatch):
    """Missing config file results in defaults being used."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_app(config=True)
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "target=default-val" in r.stdout
    assert "count=1" in r.stdout


# --- Additional: config with global flags ---

def test_config_with_global_flags(tmp_path, monkeypatch):
    """Config works with global flags too."""
    config_home = _write_config(tmp_path, "testapp", {"output": "json"})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        flags=[strictcli.Flag(name="output", type=str, help="output format", default="text")],
    )

    @app.command("run", effect="read_only", help="run something")
    def run(ctx, output):
        print(f"output={output}")

    r = app.test(["run"])
    assert r.exit_code == 0
    assert "output=json" in r.stdout


# --- Additional: config set updates existing file ---

def test_config_set_updates_existing(tmp_path, monkeypatch):
    """Config set updates an existing config file without losing other keys."""
    config_home = _write_config(tmp_path, "testapp", {"target": "old", "count": 5})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["config", "set", "target", "--value", "new"])
    assert r.exit_code == 0

    config_file = tmp_path / "testapp" / "config.json"
    data = json.loads(config_file.read_text())
    assert data["target"] == "new"
    # Other keys should be preserved
    assert data["count"] == 5


# --- Additional: config with choices validation ---

def test_config_choices_validation(tmp_path, monkeypatch):
    """Config values are validated against choices."""
    config_home = _write_config(tmp_path, "testapp", {"format": "invalid"})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("format", type=str, help="output format",
                     choices=[strictcli.Choice("json"), strictcli.Choice("yaml"), strictcli.Choice("text")], default="text")
    def run(ctx, format):
        print(f"format={format}")

    r = app.test(["run"])
    assert r.exit_code == 1
    assert "invalid value" in r.stderr


# --- Additional: config with validate function ---

def test_config_validate_function(tmp_path, monkeypatch):
    """Config values are validated with custom validate function."""
    config_home = _write_config(tmp_path, "testapp", {"port": -1})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)

    def validate_port(v):
        if v < 1 or v > 65535:
            raise ValueError("port must be between 1 and 65535")

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("port", type=int, help="port number", default=8080,
                     validate=validate_port)
    def run(ctx, port):
        print(f"port={port}")

    r = app.test(["run"])
    assert r.exit_code == 1
    assert "port must be between 1 and 65535" in r.stderr


# --- Additional: float config value ---

def test_config_float_value(tmp_path, monkeypatch):
    """Config file with float values works correctly."""
    config_home = _write_config(tmp_path, "testapp", {"ratio": 0.75})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("ratio", type=float, help="ratio value", default=1.0)
    def run(ctx, ratio):
        print(f"ratio={ratio}")

    r = app.test(["run"])
    assert r.exit_code == 0
    assert "ratio=0.75" in r.stdout


# --- Additional: int promoted to float in config ---

def test_config_int_as_float(tmp_path, monkeypatch):
    """JSON integer in config is accepted for float flags."""
    config_home = _write_config(tmp_path, "testapp", {"ratio": 2})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("ratio", type=float, help="ratio value", default=1.0)
    def run(ctx, ratio):
        print(f"ratio={ratio}")

    r = app.test(["run"])
    assert r.exit_code == 0
    assert "ratio=2.0" in r.stdout


# --- Custom config_path tests ---

def test_custom_config_path(tmp_path, monkeypatch):
    """Custom config_path is used instead of XDG-computed path."""
    config_file = tmp_path / "my-custom-config.json"
    config_file.write_text(json.dumps({"target": "custom-path-val"}) + "\n")

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("target", type=str, help="the target", default="default-val")
    def run(ctx, target):
        print(f"target={target}")

    r = app.test(["run"])
    assert r.exit_code == 0
    assert "target=custom-path-val" in r.stdout


def test_custom_config_path_tilde_expansion(tmp_path, monkeypatch):
    """Custom config_path expands ~ correctly."""
    monkeypatch.setenv("HOME", str(tmp_path))
    config_dir = tmp_path / ".myapp"
    config_dir.mkdir()
    config_file = config_dir / "settings.json"
    config_file.write_text(json.dumps({"target": "tilde-val"}) + "\n")

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path="~/.myapp/settings.json",
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("target", type=str, help="the target", default="default-val")
    def run(ctx, target):
        print(f"target={target}")

    r = app.test(["run"])
    assert r.exit_code == 0
    assert "target=tilde-val" in r.stdout


def test_custom_config_path_config_path_command(tmp_path):
    """config path command prints the custom path."""
    config_file = tmp_path / "custom.json"
    config_file.write_text("{}")

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
    )

    @app.command("run", effect="read_only", help="run something")
    def run(ctx):
        pass

    r = app.test(["config", "path"])
    assert r.exit_code == 0
    assert str(config_file) in r.stdout


def test_custom_config_path_config_set(tmp_path):
    """config set writes to the custom path."""
    config_file = tmp_path / "custom.json"

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("target", type=str, help="the target", default="default-val")
    def run(ctx, target):
        print(f"target={target}")

    r = app.test(["config", "set", "target", "--value", "written"])
    assert r.exit_code == 0
    assert config_file.exists()
    data = json.loads(config_file.read_text())
    assert data["target"] == "written"


# --- TOML config format tests ---

def test_toml_format_reads_correctly(tmp_path):
    """TOML format config reads values correctly."""
    config_file = tmp_path / "config.toml"
    config_file.write_text('target = "toml-value"\ncount = 42\nloud = true\n')

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
        config_format="toml",
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("target", type=str, help="the target", default="default-val")
    @strictcli.flag("count", type=int, help="how many", default=1)
    @strictcli.flag("loud", type=bool, default=False, help="be loud")
    def run(ctx, target, count, loud):
        print(f"target={target} count={count} loud={loud}")

    r = app.test(["run"])
    assert r.exit_code == 0
    assert "target=toml-value" in r.stdout
    assert "count=42" in r.stdout
    assert "loud=True" in r.stdout


def test_toml_format_set_writes_correctly(tmp_path):
    """TOML format config set writes valid TOML."""
    config_file = tmp_path / "config.toml"

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
        config_format="toml",
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("target", type=str, help="the target", default="default-val")
    def run(ctx, target):
        print(f"target={target}")

    r = app.test(["config", "set", "target", "--value", "toml-written"])
    assert r.exit_code == 0
    assert config_file.exists()

    import tomllib
    with open(config_file, "rb") as f:
        data = tomllib.load(f)
    assert data["target"] == "toml-written"


def test_toml_format_set_preserves_existing(tmp_path):
    """TOML format config set preserves existing keys."""
    config_file = tmp_path / "config.toml"
    config_file.write_text('existing = "keep-me"\n')

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
        config_format="toml",
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("target", type=str, help="the target", default="default-val")
    def run(ctx, target):
        print(f"target={target}")

    r = app.test(["config", "set", "target", "--value", "new-val"])
    assert r.exit_code == 0

    import tomllib
    with open(config_file, "rb") as f:
        data = tomllib.load(f)
    assert data["target"] == "new-val"
    assert data["existing"] == "keep-me"


def test_toml_format_config_path_command(tmp_path):
    """config path prints the custom path for TOML format."""
    config_file = tmp_path / "my-config.toml"
    config_file.write_text("")

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
        config_format="toml",
    )

    @app.command("run", effect="read_only", help="run something")
    def run(ctx):
        pass

    r = app.test(["config", "path"])
    assert r.exit_code == 0
    assert str(config_file) in r.stdout


def test_toml_format_xdg_default_path(tmp_path, monkeypatch):
    """Without custom config_path, TOML format uses .toml extension in XDG path."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_format="toml",
    )

    @app.command("run", effect="read_only", help="run something")
    def run(ctx):
        pass

    r = app.test(["config", "path"])
    assert r.exit_code == 0
    expected = os.path.join(str(tmp_path), "testapp", "config.toml")
    assert expected in r.stdout


def test_invalid_toml_hard_error(tmp_path):
    """Invalid TOML file is a hard error with position information."""
    config_file = tmp_path / "config.toml"
    config_file.write_text("this is = not [ valid toml")

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
        config_format="toml",
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("target", type=str, help="the target", default="default-val")
    def run(ctx, target):
        print(f"target={target}")

    r = app.test(["run"])
    assert r.exit_code == 1
    assert "config file" in r.stderr


def test_invalid_config_format():
    """Invalid config_format raises ValueError."""
    with pytest.raises(ValueError, match='config_format must be'):
        strictcli.App(
            name="testapp",
            version="1.0.0",
            help="test app",
            config=True,
            config_format="yaml",
        )


def test_default_json_unchanged(tmp_path, monkeypatch):
    """Default behavior (JSON, XDG path) is unchanged."""
    config_home = _write_config(tmp_path, "testapp", {"target": "json-default"})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "target=json-default" in r.stdout


def test_toml_config_show(tmp_path):
    """config show works with TOML format."""
    config_file = tmp_path / "config.toml"
    config_file.write_text('target = "toml-show-val"\n')

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
        config_format="toml",
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("target", type=str, help="the target", default="default-val")
    @strictcli.flag("count", type=int, help="how many", default=1)
    def run(ctx, target, count):
        pass

    r = app.test(["config", "show", "--plain"])
    assert r.exit_code == 0
    assert "target = toml-show-val  (source: config)" in r.stdout
    assert "count = 1  (source: default)" in r.stdout


def test_toml_float_value(tmp_path):
    """TOML config with float values works correctly."""
    config_file = tmp_path / "config.toml"
    config_file.write_text('ratio = 0.75\n')

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
        config_format="toml",
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("ratio", type=float, help="ratio value", default=1.0)
    def run(ctx, ratio):
        print(f"ratio={ratio}")

    r = app.test(["run"])
    assert r.exit_code == 0
    assert "ratio=0.75" in r.stdout


# --- config show --json output ---

def test_config_show_json(tmp_path, monkeypatch):
    """config show --json produces JSON output with sorted keys."""
    config_home = _write_config(tmp_path, "testapp", {"target": "from-config"})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["config", "show", "--json"])
    assert r.exit_code == 0
    data = payload(r)
    assert data["target"] == {"value": "from-config", "source": "config"}
    assert data["count"] == {"value": 1, "source": "default"}
    assert data["loud"] == {"value": False, "source": "default"}
    # Keys must be sorted
    keys = list(data.keys())
    assert keys == sorted(keys)


def test_config_show_json_bool_values(tmp_path, monkeypatch):
    """config show --json preserves typed values (bools, ints)."""
    config_home = _write_config(tmp_path, "testapp", {"loud": True, "count": 42})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_app(config=True)
    r = app.test(["config", "show", "--json"])
    assert r.exit_code == 0
    data = payload(r)
    assert data["loud"]["value"] is True
    assert data["loud"]["source"] == "config"
    assert data["count"]["value"] == 42
    assert data["count"]["source"] == "config"


# --- config show without flags ---

def test_config_show_no_flags_prints_the_table(tmp_path, monkeypatch):
    """config show prints the human table when machine mode is off.

    The old "one of --plain, --json is required" mutex is gone with the local
    --json flag: --json is framework-owned now (contract §7.5's sweep box), so
    it cannot take part in a command-local mutex group.
    """
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_app(config=True)
    r = app.test(["config", "show"])
    assert r.exit_code == 0
    assert "target = " in r.stdout


def test_config_show_plain_with_machine_mode_yields_the_payload(tmp_path, monkeypatch):
    """--plain no longer competes with --json: machine mode wins."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_app(config=True)
    r = app.test(["config", "show", "--plain", "--json"])
    assert r.exit_code == 0
    data = payload(r)
    assert data["target"]["source"] == "default"


# --- config array coercion for repeatable flags ---


def _make_repeatable_config_app(tmp_path, monkeypatch, config_data,
                                flag_type=str, flag_name="tags"):
    """Helper: app with config and a repeatable flag."""
    config_home = _write_config(tmp_path, "repapp", config_data)
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = strictcli.App(
        name="repapp",
        version="1.0.0",
        help="test app",
        config=True,
    )

    @app.command("run", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="run something")
    @strictcli.flag(flag_name, type=flag_type, help="the flag",
                     repeatable=True, unique=False, default=[])
    def run(ctx, **kwargs):
        val = kwargs[flag_name.replace("-", "_")]
        print(f"val={val}")

    return app


def test_config_array_for_repeatable_string(tmp_path, monkeypatch):
    """Config array for repeatable str flag is coerced correctly."""
    app = _make_repeatable_config_app(tmp_path, monkeypatch,
                                       {"tags": ["a", "b", "c"]})
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "val=['a', 'b', 'c']" in r.stdout


def test_config_array_for_repeatable_int(tmp_path, monkeypatch):
    """Config array for repeatable int flag is coerced correctly."""
    app = _make_repeatable_config_app(tmp_path, monkeypatch,
                                       {"nums": [1, 2, 3]},
                                       flag_type=int, flag_name="nums")
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "val=[1, 2, 3]" in r.stdout


def test_config_array_for_repeatable_float(tmp_path, monkeypatch):
    """Config array for repeatable float flag is coerced correctly."""
    app = _make_repeatable_config_app(tmp_path, monkeypatch,
                                       {"rates": [1.5, 2.5]},
                                       flag_type=float, flag_name="rates")
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "val=[1.5, 2.5]" in r.stdout


def test_config_array_for_non_repeatable_error(tmp_path, monkeypatch):
    """Array value for non-repeatable flag errors."""
    config_home = _write_config(tmp_path, "scalarapp", {"target": ["a", "b"]})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = strictcli.App(
        name="scalarapp",
        version="1.0.0",
        help="test app",
        config=True,
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("target", type=str, help="the target", default="x")
    def run(ctx, target):
        print(f"target={target}")

    r = app.test(["run"])
    assert r.exit_code == 1
    assert "expected scalar, got array" in r.stderr


def test_config_scalar_for_repeatable_error(tmp_path, monkeypatch):
    """Scalar value for repeatable flag errors."""
    app = _make_repeatable_config_app(tmp_path, monkeypatch,
                                       {"tags": "single"})
    r = app.test(["run"])
    assert r.exit_code == 1
    assert "expected array for repeatable flag" in r.stderr


def test_config_array_bad_element_type(tmp_path, monkeypatch):
    """Array with wrong element type errors with element index."""
    app = _make_repeatable_config_app(tmp_path, monkeypatch,
                                       {"tags": ["a", 123]})
    r = app.test(["run"])
    assert r.exit_code == 1
    assert "element 1: expected str, got int" in r.stderr


def test_config_empty_array(tmp_path, monkeypatch):
    """Empty array for repeatable flag gives empty list."""
    app = _make_repeatable_config_app(tmp_path, monkeypatch,
                                       {"tags": []})
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "val=[]" in r.stdout


def test_config_single_element_array(tmp_path, monkeypatch):
    """Single-element array for repeatable flag works."""
    app = _make_repeatable_config_app(tmp_path, monkeypatch,
                                       {"tags": ["a"]})
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "val=['a']" in r.stdout


def test_config_array_precedence_cli_wins(tmp_path, monkeypatch):
    """CLI values override config array entirely."""
    app = _make_repeatable_config_app(tmp_path, monkeypatch,
                                       {"tags": ["a", "b", "c"]})
    r = app.test(["run", "--tags", "x", "--tags", "y"])
    assert r.exit_code == 0
    assert "val=['x', 'y']" in r.stdout


# --- config array unique enforcement ---


def _make_unique_config_app(tmp_path, monkeypatch, config_data,
                            flag_type=str, flag_name="tags",
                            unique=True):
    """Helper: app with config and a unique repeatable flag."""
    config_home = _write_config(tmp_path, "uniqcfgapp", config_data)
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = strictcli.App(
        name="uniqcfgapp",
        version="1.0.0",
        help="test app",
        config=True,
    )

    @app.command("run", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="run something")
    @strictcli.flag(flag_name, type=flag_type, help="the flag",
                     repeatable=True, unique=unique, default=[])
    def run(ctx, **kwargs):
        val = kwargs[flag_name.replace("-", "_")]
        print(f"val={val}")

    return app


def test_config_unique_enforcement(tmp_path, monkeypatch):
    """Config array with duplicates for unique flag errors."""
    app = _make_unique_config_app(tmp_path, monkeypatch,
                                  {"tags": ["a", "b", "a"]}, unique=True)
    r = app.test(["run"])
    assert r.exit_code == 1
    assert "duplicate value 'a'" in r.stderr
    assert "config value error" in r.stderr


def test_config_unique_no_duplicates(tmp_path, monkeypatch):
    """Config array without duplicates for unique flag succeeds."""
    app = _make_unique_config_app(tmp_path, monkeypatch,
                                  {"tags": ["a", "b", "c"]}, unique=True)
    r = app.test(["run"])
    assert r.exit_code == 0
    assert "val=['a', 'b', 'c']" in r.stdout


def test_config_show_plain_array(tmp_path, monkeypatch):
    """Config show --plain displays arrays as JSON array syntax."""
    config_home = _write_config(tmp_path, "showcfgapp", {"tags": ["a", "b", "c"]})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = strictcli.App(
        name="showcfgapp",
        version="1.0.0",
        help="test app",
        config=True,
    )

    @app.command("run", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="run something")
    @strictcli.flag("tags", type=str, help="the tags",
                     repeatable=True, unique=False, default=[])
    def run(ctx, **kwargs):
        pass

    r = app.test(["config", "show", "--plain"])
    assert r.exit_code == 0
    assert 'tags = ["a", "b", "c"]  (source: config)' in r.stdout


def test_config_show_json_array(tmp_path, monkeypatch):
    """Config show --json includes array values correctly."""
    config_home = _write_config(tmp_path, "jsonshowapp",
                                {"tags": ["x", "y"]})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = strictcli.App(
        name="jsonshowapp",
        version="1.0.0",
        help="test app",
        config=True,
    )

    @app.command("run", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="run something")
    @strictcli.flag("tags", type=str, help="the tags",
                     repeatable=True, unique=False, default=[])
    def run(ctx, **kwargs):
        pass

    r = app.test(["config", "show", "--json"])
    assert r.exit_code == 0
    data = payload(r)
    assert data["tags"]["value"] == ["x", "y"]
    assert data["tags"]["source"] == "config"


def test_config_unique_enforcement_global_flag(tmp_path, monkeypatch):
    """Global flag unique enforcement from config works."""
    config_home = _write_config(tmp_path, "globuniqapp",
                                {"tags": ["a", "b", "a"]})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = strictcli.App(
        name="globuniqapp",
        version="1.0.0",
        help="test app",
        config=True,
        flags=[
            strictcli.Flag(
                name="tags", type=str, help="the tags",
                repeatable=True, unique=True, default=[],
            ),
        ],
    )

    @app.command("run", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="run something")
    def run(ctx, **kwargs):
        print(f"tags={kwargs['tags']}")

    r = app.test(["run"])
    assert r.exit_code == 1
    assert "duplicate value 'a'" in r.stderr
    assert "config value error" in r.stderr


# --- TOML array write round-trip tests ---


def _toml_write_roundtrip(tmp_path, values):
    """Write each key via _write_config_set (toml) and read back via tomllib.

    The write rides ``ctx.effects`` like every other config mutation, so the
    round-trip runs inside a real (live-mode) dispatch to get a handle.
    """
    path = str(tmp_path / "config.toml")
    data = {}
    app = strictcli.App(name="writer", version="1.0.0", help="writer")

    @app.command("write", help="write", effect="mutating")
    def _write(ctx):
        for key, value in values.items():
            strictcli._write_config_set(ctx.effects, data, path, "toml", key, value)
        return 0

    assert app.test(["write"]).exit_code == 0
    with open(path, "rb") as f:
        return tomllib.load(f)


def test_toml_write_string_array(tmp_path):
    """config-set TOML write handles string arrays and they round-trip."""
    data = _toml_write_roundtrip(tmp_path, {"tags": ["a", "b"]})
    assert data["tags"] == ["a", "b"]


def test_toml_write_int_array(tmp_path):
    """config-set TOML write handles int arrays and they round-trip."""
    data = _toml_write_roundtrip(tmp_path, {"nums": [1, 2, 3]})
    assert data["nums"] == [1, 2, 3]


def test_toml_write_float_array(tmp_path):
    """config-set TOML write handles float arrays and they round-trip."""
    data = _toml_write_roundtrip(tmp_path, {"rates": [1.5, 2.5]})
    assert data["rates"] == [1.5, 2.5]


def test_toml_write_empty_array(tmp_path):
    """config-set TOML write handles empty arrays and they round-trip."""
    data = _toml_write_roundtrip(tmp_path, {"items": []})
    assert data["items"] == []


def test_toml_write_mixed_scalars_and_arrays(tmp_path):
    """config-set TOML write handles mixed scalar and array values correctly."""
    data = _toml_write_roundtrip(tmp_path, {
        "name": "test",
        "count": 42,
        "rate": 3.14,
        "debug": True,
        "tags": ["a", "b", "c"],
        "nums": [1, 2],
    })
    assert data["name"] == "test"
    assert data["count"] == 42
    assert data["rate"] == 3.14
    assert data["debug"] is True
    assert data["tags"] == ["a", "b", "c"]
    assert data["nums"] == [1, 2]


def test_toml_write_array_with_special_chars(tmp_path):
    """config-set TOML write handles string arrays with special chars correctly."""
    data = _toml_write_roundtrip(tmp_path, {
        "paths": ['C:\\Users\\me', 'say "hello"', "back\\slash"],
    })
    assert data["paths"] == ['C:\\Users\\me', 'say "hello"', "back\\slash"]


# --- Config set: repeatable flag support ---


def _make_config_set_app(config_path=None, config_format="json"):
    """Helper: app with config support and repeatable flags for config set tests."""
    kwargs = {"config": True}
    if config_path is not None:
        kwargs["config_path"] = config_path
    if config_format != "json":
        kwargs["config_format"] = config_format
    app = strictcli.App(
        name="repapp",
        version="1.0.0",
        help="test app with repeatable flags",
        **kwargs,
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("tags", type=str, help="tags", repeatable=True, unique=False, default=[])
    @strictcli.flag("counts", type=int, help="counts", repeatable=True,
                    unique=False, default=[])
    @strictcli.flag("rates", type=float, help="rates", repeatable=True,
                    unique=False, default=[])
    @strictcli.flag("ids", type=int, help="unique ids", repeatable=True,
                    unique=True, default=[])
    @strictcli.flag("name", type=str, help="name", default="default")
    def run(ctx, tags, counts, rates, ids, name):
        print(f"tags={tags} counts={counts} rates={rates} ids={ids} name={name}")

    return app


def test_config_set_repeatable_string(tmp_path, monkeypatch):
    """Config set writes a string array for a repeatable flag."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "tags", "--value", "a,b,c"])
    assert r.exit_code == 0

    config_file = tmp_path / "repapp" / "config.json"
    data = json.loads(config_file.read_text())
    assert data["tags"] == ["a", "b", "c"]


def test_config_set_repeatable_int(tmp_path, monkeypatch):
    """Config set writes an int array for a repeatable flag."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "counts", "--value", "1,2,3"])
    assert r.exit_code == 0

    config_file = tmp_path / "repapp" / "config.json"
    data = json.loads(config_file.read_text())
    assert data["counts"] == [1, 2, 3]


def test_config_set_repeatable_float(tmp_path, monkeypatch):
    """Config set writes a float array for a repeatable flag."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "rates", "--value", "1.5,2.5,3.0"])
    assert r.exit_code == 0

    config_file = tmp_path / "repapp" / "config.json"
    data = json.loads(config_file.read_text())
    assert data["rates"] == [1.5, 2.5, 3.0]


def test_config_set_escaped_comma(tmp_path, monkeypatch):
    r"""Config set handles escaped commas: a\,b,c -> ["a,b", "c"]."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "tags", "--value", "a\\,b,c"])
    assert r.exit_code == 0

    config_file = tmp_path / "repapp" / "config.json"
    data = json.loads(config_file.read_text())
    assert data["tags"] == ["a,b", "c"]


def test_config_set_repeatable_unique_valid(tmp_path, monkeypatch):
    """Config set accepts distinct values for a unique repeatable flag."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "ids", "--value", "1,2,3"])
    assert r.exit_code == 0

    config_file = tmp_path / "repapp" / "config.json"
    data = json.loads(config_file.read_text())
    assert data["ids"] == [1, 2, 3]


def test_config_set_repeatable_unique_duplicate(tmp_path, monkeypatch):
    """Config set rejects duplicate values for a unique repeatable flag."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "ids", "--value", "1,2,1"])
    assert r.exit_code == 1
    assert "config set: key 'ids': duplicate value '1'" in r.stderr


def test_config_set_round_trip_json(tmp_path, monkeypatch):
    """Config set then show --json round-trips an array correctly."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "tags", "--value", "x,y"])
    assert r.exit_code == 0

    r = app.test(["config", "show", "--json"])
    assert r.exit_code == 0
    data = payload(r)
    assert data["tags"]["value"] == ["x", "y"]
    assert data["tags"]["source"] == "config"


def test_config_set_round_trip_toml(tmp_path):
    """Config set then show --plain round-trips an array with TOML format."""
    config_file = tmp_path / "config.toml"
    app = _make_config_set_app(
        config_path=str(config_file), config_format="toml",
    )
    r = app.test(["config", "set", "tags", "--value", "a,b"])
    assert r.exit_code == 0
    assert config_file.exists()

    r = app.test(["config", "show", "--plain"])
    assert r.exit_code == 0
    assert 'tags = ["a", "b"]  (source: config)' in r.stdout


# --- Config set: --clear and --default ---


def test_config_set_clear_repeatable(tmp_path, monkeypatch):
    """--clear writes [] for a repeatable flag."""
    config_home = _write_config(tmp_path, "repapp", {"tags": ["a", "b"]})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_set_app()
    r = app.test(["config", "set", "tags", "--clear"])
    assert r.exit_code == 0

    config_file = tmp_path / "repapp" / "config.json"
    data = json.loads(config_file.read_text())
    assert data["tags"] == []


def test_config_set_clear_scalar_error(tmp_path, monkeypatch):
    """--clear errors on a scalar (non-repeatable) flag."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "name", "--clear"])
    assert r.exit_code == 1
    assert "config set: --clear is only for repeatable flags" in r.stderr


def test_config_set_default_removes_key(tmp_path, monkeypatch):
    """--default removes the key from config."""
    config_home = _write_config(tmp_path, "repapp", {"name": "alice", "tags": ["x"]})
    monkeypatch.setenv("XDG_CONFIG_HOME", config_home)
    app = _make_config_set_app()
    r = app.test(["config", "set", "name", "--default"])
    assert r.exit_code == 0

    config_file = tmp_path / "repapp" / "config.json"
    data = json.loads(config_file.read_text())
    assert "name" not in data
    # Other keys preserved
    assert data["tags"] == ["x"]


def test_config_set_default_nonexistent_key_error(tmp_path, monkeypatch):
    """--default errors when the key is not in config."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "name", "--default"])
    assert r.exit_code == 1
    assert "config set: key 'name' not in config" in r.stderr


def test_config_set_electing_nothing_is_refused(tmp_path, monkeypatch):
    """Electing no member is the framework's own unsatisfied-selector refusal.

    The hand-rolled "provide a value, --clear, or --default" guard is gone with
    the three bools that made its state expressible (contract §27.1, §18.33
    item 304).
    """
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "name"])
    assert r.exit_code == 1
    assert "one of --value, --clear, --default is required" in r.stderr


def test_config_set_value_with_clear_error(tmp_path, monkeypatch):
    """Electing two members is the framework's own refusal (§27.1)."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "tags", "--value", "a,b", "--clear"])
    assert r.exit_code == 1
    assert "--value and --clear are mutually exclusive" in r.stderr


def test_config_set_value_with_default_error(tmp_path, monkeypatch):
    """Electing two members is the framework's own refusal (§27.1)."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "name", "--value", "alice", "--default"])
    assert r.exit_code == 1
    assert "--value and --default are mutually exclusive" in r.stderr


def test_config_set_clear_and_default_error(tmp_path, monkeypatch):
    """Electing two members is the framework's own refusal (§27.1)."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_set_app()
    r = app.test(["config", "set", "tags", "--clear", "--default"])
    assert r.exit_code == 1
    assert "--clear and --default are mutually exclusive" in r.stderr


# ---- --config flag tests ----


def test_config_flag_selects_file(tmp_path, monkeypatch):
    """--config <path> selects that file for config loading."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    custom_path = str(tmp_path / "custom.json")
    with open(custom_path, "w") as f:
        json.dump({"port": 9999}, f)

    app = strictcli.App(name="testapp", version="1.0.0", help="test app", config=True)

    @app.command("serve", effect="read_only", help="start server")
    @strictcli.flag("port", type=int, help="port number", default=8080)
    def serve(ctx, port):
        print(f"port={port}")

    r = app.test(["--config", custom_path, "serve"])
    assert r.exit_code == 0
    assert "port=9999" in r.stdout


def test_config_flag_equals_form(tmp_path, monkeypatch):
    """--config=<path> form works."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    custom_path = str(tmp_path / "custom.json")
    with open(custom_path, "w") as f:
        json.dump({"port": 7777}, f)

    app = strictcli.App(name="testapp", version="1.0.0", help="test app", config=True)

    @app.command("serve", effect="read_only", help="start server")
    @strictcli.flag("port", type=int, help="port number", default=8080)
    def serve(ctx, port):
        print(f"port={port}")

    r = app.test([f"--config={custom_path}", "serve"])
    assert r.exit_code == 0
    assert "port=7777" in r.stdout


def test_config_flag_overrides_constructed_path(tmp_path, monkeypatch):
    """--config overrides a constructed config_path."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    constructed_path = str(tmp_path / "constructed.json")
    override_path = str(tmp_path / "override.json")
    with open(constructed_path, "w") as f:
        json.dump({"port": 1111}, f)
    with open(override_path, "w") as f:
        json.dump({"port": 2222}, f)

    app = strictcli.App(
        name="testapp", version="1.0.0", help="test app",
        config=True, config_path=constructed_path,
    )

    @app.command("serve", effect="read_only", help="start server")
    @strictcli.flag("port", type=int, help="port number", default=8080)
    def serve(ctx, port):
        print(f"port={port}")

    r = app.test(["--config", override_path, "serve"])
    assert r.exit_code == 0
    assert "port=2222" in r.stdout


def test_config_flag_on_disabled_app_is_error():
    """--config on an app with config disabled is a hard error."""
    app = strictcli.App(name="testapp", version="1.0.0", help="test app")

    @app.command("run", effect="read_only", help="run")
    def run(ctx):
        pass

    r = app.test(["--config", "/tmp/fake.json", "run"])
    assert r.exit_code == 1
    assert "--config is not available" in r.stderr


def test_config_flag_after_command_is_unknown():
    """--config after the command name is an unknown flag error."""
    app = strictcli.App(name="testapp", version="1.0.0", help="test app", config=True)

    @app.command("run", effect="read_only", help="run")
    def run(ctx):
        pass

    r = app.test(["run", "--config", "/tmp/fake.json"])
    assert r.exit_code == 1
    assert "unknown flag" in r.stderr


def test_config_flag_after_double_dash():
    """--config after -- is not intercepted."""
    app = strictcli.App(name="testapp", version="1.0.0", help="test app", config=True)

    @app.command("run", effect="read_only", help="run")
    def run(ctx):
        pass

    r = app.test(["--", "--config", "/tmp/fake.json"])
    assert r.exit_code == 1


def test_config_flag_not_in_schema(tmp_path, monkeypatch):
    """--config should NOT appear in --dump-schema output."""
    monkeypatch.chdir(tmp_path)
    # Create pyproject.toml for project_id detection
    (tmp_path / "pyproject.toml").write_text('[project]\nname = "testapp"\n')

    app = strictcli.App(name="testapp", version="1.0.0", help="test app", config=True)

    @app.command("run", effect="read_only", help="run")
    def run(ctx):
        pass

    r = app.test(["--dump-schema"])
    assert r.exit_code == 0
    schema = json.loads((tmp_path / ".strictcli" / "schema.json").read_text())
    # Check that no global flag is named "config"
    for gf in schema.get("global_flags", []):
        assert gf["name"] != "config", "--config should not appear in schema"


# ---- no-default-config-path tests ----


def test_no_default_config_path(tmp_path, monkeypatch):
    """With no_default_config_path, XDG config is NOT loaded without --config."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    # Write config at the XDG default location
    cfg_dir = tmp_path / "testapp"
    cfg_dir.mkdir()
    (cfg_dir / "config.json").write_text(json.dumps({"port": 6666}))

    app = strictcli.App(
        name="testapp", version="1.0.0", help="test app",
        config=True, no_default_config_path=True,
    )

    @app.command("serve", effect="read_only", help="start server")
    @strictcli.flag("port", type=int, help="port number", default=8080)
    def serve(ctx, port):
        print(f"port={port}")

    r = app.test(["serve"])
    assert r.exit_code == 0
    assert "port=8080" in r.stdout


def test_no_default_config_path_with_config_flag(tmp_path, monkeypatch):
    """With no_default_config_path + --config, the explicit path IS loaded."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    custom_path = str(tmp_path / "explicit.json")
    with open(custom_path, "w") as f:
        json.dump({"port": 3333}, f)

    app = strictcli.App(
        name="testapp", version="1.0.0", help="test app",
        config=True, no_default_config_path=True,
    )

    @app.command("serve", effect="read_only", help="start server")
    @strictcli.flag("port", type=int, help="port number", default=8080)
    def serve(ctx, port):
        print(f"port={port}")

    r = app.test(["--config", custom_path, "serve"])
    assert r.exit_code == 0
    assert "port=3333" in r.stdout


# --- hard-error config loading tests ---

def test_malformed_toml_hard_error(tmp_path):
    """Malformed TOML config file is a hard error with position info."""
    config_file = tmp_path / "config.toml"
    config_file.write_text("key = [unclosed")
    app = strictcli.App(
        name="testapp", version="1.0.0", help="test",
        config=True, config_path=str(config_file), config_format="toml",
    )

    @app.command("run", effect="read_only", help="run")
    @strictcli.flag("name", type=str, help="a name", default="")
    def run(ctx, name):
        print(f"name={name}")

    r = app.test(["run"])
    assert r.exit_code == 1
    assert "config file" in r.stderr


def test_malformed_json_hard_error(tmp_path, monkeypatch):
    """Malformed JSON config file is a hard error with position info."""
    config_dir = tmp_path / "testapp"
    config_dir.mkdir(parents=True)
    (config_dir / "config.json").write_text('{"key": bad}')
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_app(config=True)
    r = app.test(["run"])
    assert r.exit_code == 1
    assert "config file" in r.stderr
    assert "line" in r.stderr
    assert "column" in r.stderr


def test_missing_via_runtime_flag():
    """--config pointing at missing file is a hard error."""
    app = strictcli.App(name="testapp", version="1.0.0", help="test", config=True)

    @app.command("run", effect="read_only", help="run")
    def run(ctx):
        pass

    r = app.test(["--config", "/nonexistent/path/config.json", "run"])
    assert r.exit_code == 1
    assert "config file not found" in r.stderr


def test_missing_via_config_path_is_soft():
    """config_path pointing at missing file is soft (empty map)."""
    app = strictcli.App(
        name="testapp", version="1.0.0", help="test",
        config=True, config_path="/nonexistent/path/config.json",
    )

    @app.command("run", effect="read_only", help="run")
    @strictcli.flag("name", type=str, help="a name", default="default")
    def run(ctx, name):
        print(f"name={name}")

    r = app.test(["run"])
    assert r.exit_code == 0
    assert "name=default" in r.stdout


def test_missing_xdg_is_soft(tmp_path, monkeypatch):
    """Missing XDG config is soft (no error)."""
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_app(config=True)
    r = app.test(["run"])
    assert r.exit_code == 0


def test_config_show_on_broken_config(tmp_path, monkeypatch):
    """config show on broken config shows the error."""
    config_dir = tmp_path / "testapp"
    config_dir.mkdir(parents=True)
    (config_dir / "config.json").write_text("{broken")
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_app(config=True)
    r = app.test(["config", "show", "--plain"])
    assert r.exit_code == 1
    assert "config file" in r.stderr


def test_config_path_on_broken_config(tmp_path, monkeypatch):
    """config path still works on broken config."""
    config_dir = tmp_path / "testapp"
    config_dir.mkdir(parents=True)
    (config_dir / "config.json").write_text("{broken")
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_app(config=True)
    r = app.test(["config", "path"])
    assert r.exit_code == 0


def test_duplicate_key_toml_hard_error(tmp_path):
    """Duplicate key in TOML config is a hard error."""
    config_file = tmp_path / "config.toml"
    config_file.write_text('name = "a"\nname = "b"\n')
    app = strictcli.App(
        name="testapp", version="1.0.0", help="test",
        config=True, config_path=str(config_file), config_format="toml",
    )

    @app.command("run", effect="read_only", help="run")
    @strictcli.flag("name", type=str, help="a name", default="")
    def run(ctx, name):
        print(f"name={name}")

    r = app.test(["run"])
    assert r.exit_code == 1
    assert "config file" in r.stderr


# --- conflict mode tests ---

def test_conflict_mode_default(tmp_path, monkeypatch):
    """Default mode (cli-wins): CLI overrides config silently."""
    config_dir = tmp_path / "testapp"
    config_dir.mkdir(parents=True)
    (config_dir / "config.json").write_text('{"target": "from-config"}')
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = _make_config_app(config=True)
    r = app.test(["run", "--target", "from-cli"])
    assert r.exit_code == 0
    assert "target=from-cli" in r.stdout


def test_conflict_mode_error_cli(tmp_path, monkeypatch):
    """Conflict mode error: config + CLI is a conflict."""
    config_dir = tmp_path / "testapp"
    config_dir.mkdir(parents=True)
    (config_dir / "config.json").write_text('{"target": "from-config"}')
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = strictcli.App(
        name="testapp", version="1.0.0", help="test",
        config=True, config_conflict_mode="error",
    )

    @app.command("run", effect="read_only", help="run")
    @strictcli.flag("target", type=str, help="target", default="default-val")
    def run(ctx, target):
        print(f"target={target}")

    r = app.test(["run", "--target", "from-cli"])
    assert r.exit_code == 1
    assert "target" in r.stderr
    assert "cli" in r.stderr
    assert "config" in r.stderr


def test_conflict_mode_error_env(tmp_path, monkeypatch):
    """Conflict mode error: config + env is a conflict."""
    config_dir = tmp_path / "testapp"
    config_dir.mkdir(parents=True)
    (config_dir / "config.json").write_text('{"target": "from-config"}')
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    monkeypatch.setenv("MY_TARGET", "from-env")
    app = strictcli.App(
        name="testapp", version="1.0.0", help="test",
        config=True, config_conflict_mode="error",
    )

    @app.command("run", effect="read_only", help="run")
    @strictcli.flag("target", type=str, help="target", default="default-val",
                    env="MY_TARGET")
    def run(ctx, target):
        print(f"target={target}")

    r = app.test(["run"])
    assert r.exit_code == 1
    assert "target" in r.stderr


def test_conflict_mode_implied_excluded(tmp_path, monkeypatch):
    """Implied source does NOT trigger conflict with config."""
    config_dir = tmp_path / "testapp"
    config_dir.mkdir(parents=True)
    (config_dir / "config.json").write_text('{"loud": true}')
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = strictcli.App(
        name="testapp", version="1.0.0", help="test",
        config=True, config_conflict_mode="error",
    )

    @app.command("run", effect="read_only", help="run",
                 constraints=[strictcli.Implies("debug-implies-loud", flag="debug", implies="loud", value=True)])
    @strictcli.flag("debug", type=bool, help="enable debug", default=False)
    @strictcli.flag("loud", type=bool, help="be loud", default=False)
    def run(ctx, debug, loud):
        print(f"loud={loud}")

    r = app.test(["run", "--debug"])
    assert r.exit_code == 0


def test_conflict_mode_fires_on_an_elected_member(tmp_path, monkeypatch):
    """§21.3's carve-out survives member spelling untouched (§21's box, item 119).

    "Not consulted at all" describes value DELIVERY and election. It does not
    describe the both-sources conflict check, which is a value-hygiene rule
    about the operator's own configuration: an app configured with
    config_conflict_mode="error" that has a config value for the member the
    command line then elects with a diverging value reports the conflict.
    """
    config_dir = tmp_path / "testapp"
    config_dir.mkdir(parents=True)
    (config_dir / "config.json").write_text('{"profile": "from-config"}')
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    app = strictcli.App(
        name="testapp", version="1.0.0", help="test",
        config=True, config_conflict_mode="error",
    )

    @strictcli.choice("profile", help="one named profile")
    class NamedProfile:
        value: str = strictcli.member_value(help="the profile name")

    @strictcli.choice("all-profiles", help="every profile")
    class AllProfiles:
        pass

    @app.command("run", effect="read_only", help="run")
    @strictcli.choice_flag(
        "mode", help="which profiles", presence="required",
        elect_by="member-flags", choices=[NamedProfile, AllProfiles],
    )
    def run(ctx, mode: NamedProfile | AllProfiles):
        pass

    r = app.test(["run", "--profile", "typed"])
    assert r.exit_code == 1
    assert "set in both" in r.stderr

    # An UNELECTED member has no conflict: the command line supplied nothing
    # to diverge from.
    r = app.test(["run", "--all-profiles"])
    assert r.exit_code == 0


# --- divergence-aware conflict mode + per-flag override ---

def _conflict_app(conflict_mode="error", flag_conflict_mode=strictcli._MISSING,
                  flag_type=str, default="default-val", unique=strictcli._MISSING,
                  repeatable=False):
    """Build an app with a single 'run' command flag 'target'."""
    app = strictcli.App(
        name="testapp", version="1.0.0", help="test",
        config=True, config_conflict_mode=conflict_mode,
    )

    @app.command("run", effect="read_only", help="run")
    @strictcli.flag("target", type=flag_type, help="target", default=default,
                    repeatable=repeatable, unique=unique,
                    conflict_mode=flag_conflict_mode)
    def run(ctx, target):
        print(f"target={target}")

    return app


def _write_cfg(tmp_path, monkeypatch, payload):
    config_dir = tmp_path / "testapp"
    config_dir.mkdir(parents=True, exist_ok=True)
    (config_dir / "config.json").write_text(json.dumps(payload))
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))


def test_conflict_error_identical_scalar_passes(tmp_path, monkeypatch):
    """Error mode: identical config+CLI value is NOT a conflict."""
    _write_cfg(tmp_path, monkeypatch, {"target": "same"})
    app = _conflict_app(conflict_mode="error")
    r = app.test(["run", "--target", "same"])
    assert r.exit_code == 0, r.stderr
    assert "target=same" in r.stdout


def test_conflict_error_divergent_scalar_errors(tmp_path, monkeypatch):
    """Error mode: divergent config+CLI value IS a conflict."""
    _write_cfg(tmp_path, monkeypatch, {"target": "from-config"})
    app = _conflict_app(conflict_mode="error")
    r = app.test(["run", "--target", "from-cli"])
    assert r.exit_code == 1
    assert "set in both cli and config; remove one" in r.stderr


def test_per_flag_error_beats_app_cli_wins(tmp_path, monkeypatch):
    """Per-flag conflict_mode='error' overrides app default cli-wins."""
    _write_cfg(tmp_path, monkeypatch, {"target": "from-config"})
    app = _conflict_app(conflict_mode="cli-wins", flag_conflict_mode="error")
    r = app.test(["run", "--target", "from-cli"])
    assert r.exit_code == 1
    assert "set in both cli and config; remove one" in r.stderr


def test_per_flag_cli_wins_beats_app_error(tmp_path, monkeypatch):
    """Per-flag conflict_mode='cli-wins' overrides app default error."""
    _write_cfg(tmp_path, monkeypatch, {"target": "from-config"})
    app = _conflict_app(conflict_mode="error", flag_conflict_mode="cli-wins")
    r = app.test(["run", "--target", "from-cli"])
    assert r.exit_code == 0, r.stderr
    assert "target=from-cli" in r.stdout


def test_conflict_repeatable_order_sensitive(tmp_path, monkeypatch):
    """Plain repeatable: same elements different order diverge -> error."""
    _write_cfg(tmp_path, monkeypatch, {"target": ["a", "b"]})
    app = _conflict_app(conflict_mode="error", default=[],
                        repeatable=True, unique=False)
    # Same order -> equal -> pass
    r = app.test(["run", "--target", "a", "--target", "b"])
    assert r.exit_code == 0, r.stderr
    # Different order -> divergent -> error
    app2 = _conflict_app(conflict_mode="error", default=[],
                         repeatable=True, unique=False)
    r2 = app2.test(["run", "--target", "b", "--target", "a"])
    assert r2.exit_code == 1
    assert "set in both" in r2.stderr


def test_conflict_unique_order_insensitive(tmp_path, monkeypatch):
    """Unique: same elements different order are equal (multiset) -> pass."""
    _write_cfg(tmp_path, monkeypatch, {"target": ["a", "b"]})
    app = _conflict_app(conflict_mode="error", default=[],
                        repeatable=True, unique=True)
    r = app.test(["run", "--target", "b", "--target", "a"])
    assert r.exit_code == 0, r.stderr


def test_conflict_malformed_config_value_errors_cleanly(tmp_path, monkeypatch):
    """Error mode co-presence: a malformed config value errors cleanly."""
    _write_cfg(tmp_path, monkeypatch, {"target": "not-an-int"})
    app = _conflict_app(conflict_mode="error", flag_type=int, default=0)
    r = app.test(["run", "--target", "5"])
    assert r.exit_code == 1
    assert "config value error" in r.stderr


def test_flag_conflict_mode_invalid_value_raises():
    """Registration: invalid per-flag conflict_mode is a ValueError."""
    with pytest.raises(ValueError, match="conflict_mode"):
        strictcli.Flag(name="x", type=str, help="h", conflict_mode="bogus", presence="required")


# --- Config set: TOML comment/order preservation ---


def test_config_set_toml_preserves_comments_and_order(tmp_path):
    """config set on a commented, custom-ordered TOML config keeps comments and
    ordering intact, changing only the targeted key (comment-preserving edit)."""
    config_file = tmp_path / "config.toml"
    config_file.write_text(
        "# strictcli config for myapp\n"
        "zeta = \"keep-me\"  # trailing comment on zeta\n"
        "\n"
        "# a comment above alpha\n"
        "alpha = 1\n"
        "beta = true\n"
    )

    app = strictcli.App(
        name="myapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_format="toml",
        config_path=str(config_file),
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("zeta", type=str, help="a string", default="")
    @strictcli.flag("alpha", type=int, help="a number", default=0)
    @strictcli.flag("beta", type=bool, help="a bool", default=False)
    def run(ctx, zeta, alpha, beta):
        pass

    r = app.test(["config", "set", "alpha", "--value", "42"])
    assert r.exit_code == 0

    text = config_file.read_text()
    # Comments preserved
    assert "# strictcli config for myapp" in text
    assert "# trailing comment on zeta" in text
    assert "# a comment above alpha" in text
    # Only alpha changed
    assert "alpha = 42" in text
    assert "alpha = 1" not in text
    # Untouched keys retained
    assert 'zeta = "keep-me"' in text
    assert "beta = true" in text
    # Original ordering (zeta before alpha before beta) preserved
    assert text.index("zeta") < text.index("alpha") < text.index("beta")


def test_config_set_toml_unset_preserves_comments(tmp_path):
    """config set --default (unset) removes only the target key, keeping the
    surrounding comments and other keys intact."""
    config_file = tmp_path / "config.toml"
    config_file.write_text(
        "# header comment\n"
        "alpha = 1\n"
        "beta = 2  # keep this comment\n"
    )

    app = strictcli.App(
        name="myapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_format="toml",
        config_path=str(config_file),
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("alpha", type=int, help="a number", default=0)
    @strictcli.flag("beta", type=int, help="another number", default=0)
    def run(ctx, alpha, beta):
        pass

    r = app.test(["config", "set", "alpha", "--default"])
    assert r.exit_code == 0

    text = config_file.read_text()
    assert "# header comment" in text
    assert "alpha" not in text
    assert "beta = 2" in text
    assert "# keep this comment" in text


# --- Config edit: editor failure is a hard error ---


def _make_config_edit_app(config_file):
    app = strictcli.App(
        name="editapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("opt", type=str, help="an option", default="")
    def run(ctx, opt):
        pass

    return app


def test_config_edit_editor_failure_is_hard_error(tmp_path, monkeypatch):
    """A non-zero editor exit is a hard error: exit 1, 'error: editor failed'."""
    config_file = tmp_path / "config.json"
    app = _make_config_edit_app(config_file)
    monkeypatch.setenv("EDITOR", "false")  # /bin/false always exits 1
    r = app.test(["config", "edit"])
    assert r.exit_code == 1
    assert "error: editor failed" in r.stderr


def test_config_edit_editor_success(tmp_path, monkeypatch):
    """A zero editor exit succeeds (exit 0, no error)."""
    config_file = tmp_path / "config.json"
    app = _make_config_edit_app(config_file)
    monkeypatch.setenv("EDITOR", "true")  # /bin/true always exits 0
    r = app.test(["config", "edit"])
    assert r.exit_code == 0
    assert "error: editor failed" not in r.stderr


def test_config_edit_editor_not_found_is_hard_error(tmp_path, monkeypatch):
    """A missing editor binary is a hard error, not a silent success."""
    config_file = tmp_path / "config.json"
    app = _make_config_edit_app(config_file)
    monkeypatch.setenv("EDITOR", "definitely-not-a-real-editor-xyz")
    r = app.test(["config", "edit"])
    assert r.exit_code == 1
    assert "error: editor failed" in r.stderr


# --- The framework's own mutating commands honour --dry-run (§9.2, §3.1) ---
#
# `config set`, `config init` and `config edit` are classified `mutating`, so
# every mutation they make must ride ctx.effects: under --dry-run they are
# RECORDED and rendered, never performed. A framework command that printed
# "DRY RUN — no changes were made." while rewriting the user's config file (or
# launching their editor) would be the loudest possible counterexample to the
# regime it ships.

_DRY_HEADER = "DRY RUN — no changes were made. Would do:\n"


def _dry_app(config_file, config_format="json"):
    app = strictcli.App(
        name="myapp", version="1.0.0", help="test app",
        config=True, config_path=str(config_file), config_format=config_format,
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("opt", type=str, help="an option", default="")
    def _run(ctx, opt):
        return 0

    return app


def test_config_set_dry_run_changes_nothing(tmp_path):
    config_file = tmp_path / "config.json"
    config_file.write_text('{"opt": "before"}\n')
    app = _dry_app(config_file)
    r = app.test(["--dry-run", "config", "set", "opt", "--value", "after"])
    assert r.exit_code == 0
    assert r.stdout == (
        f"{_DRY_HEADER}  1. write: {config_file} (21 bytes)\n"
    )
    assert config_file.read_text() == '{"opt": "before"}\n'


def test_config_set_default_dry_run_changes_nothing(tmp_path):
    config_file = tmp_path / "config.json"
    config_file.write_text('{"opt": "before"}\n')
    app = _dry_app(config_file)
    r = app.test(["--dry-run", "config", "set", "opt", "--default"])
    assert r.exit_code == 0
    assert f"1. write: {config_file}" in r.stdout
    assert config_file.read_text() == '{"opt": "before"}\n'


def test_config_set_dry_run_previews_the_missing_directory(tmp_path):
    config_file = tmp_path / "nested" / "deeper" / "config.json"
    app = _dry_app(config_file)
    r = app.test(["--dry-run", "config", "set", "opt", "--value", "v"])
    assert r.exit_code == 0
    assert r.stdout.startswith(
        f"{_DRY_HEADER}  1. mkdir: {config_file.parent}\n  2. write: {config_file} ("
    )
    assert not config_file.parent.exists()


def test_config_set_toml_dry_run_changes_nothing(tmp_path):
    config_file = tmp_path / "config.toml"
    config_file.write_text('# a comment\nopt = "before"\n')
    app = _dry_app(config_file, config_format="toml")
    r = app.test(["--dry-run", "config", "set", "opt", "--value", "after"])
    assert r.exit_code == 0
    assert f"1. write: {config_file}" in r.stdout
    assert config_file.read_text() == '# a comment\nopt = "before"\n'


def test_config_init_dry_run_writes_nothing(tmp_path):
    config_file = tmp_path / "config.json"
    app = _dry_app(config_file)
    r = app.test(["--dry-run", "config", "init"])
    assert r.exit_code == 0
    assert f"{config_file}\n" in r.stdout
    assert f"1. write: {config_file} (" in r.stdout
    assert not config_file.exists()


def test_config_edit_dry_run_does_not_launch_the_editor(tmp_path, monkeypatch):
    """The sharpest form of the bug: a dry run must not open $EDITOR."""
    config_file = tmp_path / "config.json"
    marker = tmp_path / "editor-ran"
    editor = tmp_path / "fake-editor"
    editor.write_text(f"#!/bin/sh\ntouch {marker}\n")
    editor.chmod(0o755)
    monkeypatch.setenv("EDITOR", str(editor))
    app = _dry_app(config_file)
    r = app.test(["--dry-run", "config", "edit"])
    assert r.exit_code == 0
    assert r.stdout == (
        f"{_DRY_HEADER}"
        f"  1. write: {config_file} (3 bytes)\n"
        f"  2. run: {editor} {config_file}\n"
    )
    assert not marker.exists()
    assert not config_file.exists()


def test_config_commands_still_mutate_in_live_mode(tmp_path):
    config_file = tmp_path / "sub" / "config.json"
    app = _dry_app(config_file)
    assert app.test(["config", "init"]).exit_code == 0
    assert config_file.is_file()
    assert app.test(["config", "set", "opt", "--value", "v"]).exit_code == 0
    assert json.loads(config_file.read_text())["opt"] == "v"


def test_config_set_records_its_write_in_the_live_effect_log(tmp_path):
    config_file = tmp_path / "config.json"
    config_file.write_text("{}\n")
    app = _dry_app(config_file)
    app.test(["config", "set", "opt", "--value", "v"])
    log = [r for r in app.effect_log() if r["kind"] != "cache_write"]
    assert [(r["verb"], r["recorded"]) for r in log] == [("write", False)]
