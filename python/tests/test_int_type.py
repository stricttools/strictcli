"""Tests for type=int flag support."""

import pytest

import strictcli


def _make_app_with_int_flag(**flag_kwargs):
    """Helper: app with a single command that has one int flag."""
    # Presence is mandatory (contract §23); these helpers declare the plain
    # required case unless the caller states its own.
    if "default" not in flag_kwargs and "presence" not in flag_kwargs:
        flag_kwargs["presence"] = "required"
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag("port", type=int, help="the port", **flag_kwargs)
    def cmd(ctx, port):
        print(f"port={port}")

    return app


def test_int_flag_parses():
    """Int flag with --port 8080 -> handler receives 8080 as int."""
    app = _make_app_with_int_flag()
    r = app.test(["cmd", "--port", "8080"])
    assert r.exit_code == 0
    assert "port=8080" in r.stdout


def test_int_flag_value_is_int():
    """Int flag value is actually an int, not a string."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag("port", type=int, help="the port", presence="required")
    def cmd(ctx, port):
        print(f"type={type(port).__name__}")

    r = app.test(["cmd", "--port", "8080"])
    assert r.exit_code == 0
    assert "type=int" in r.stdout


def test_int_flag_with_default():
    """Int flag omitted uses default value."""
    app = _make_app_with_int_flag(default=8000)
    r = app.test(["cmd"])
    assert r.exit_code == 0
    assert "port=8000" in r.stdout


def test_int_flag_from_env(monkeypatch):
    """Int flag value from env var is coerced to int."""
    app = strictcli.App(name="test", version="1.0.0", help="test app", env_prefix="MYAPP")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag("port", type=int, help="the port", default=80, env="MYAPP_PORT")
    def cmd(ctx, port):
        print(f"port={port} type={type(port).__name__}")

    monkeypatch.setenv("MYAPP_PORT", "9090")
    r = app.test(["cmd"])
    assert r.exit_code == 0
    assert "port=9090" in r.stdout
    assert "type=int" in r.stdout


def test_int_flag_bad_value():
    """Non-integer value for int flag -> exit 1, stderr says expected integer."""
    app = _make_app_with_int_flag()
    r = app.test(["cmd", "--port", "abc"])
    assert r.exit_code == 1
    assert "expected integer" in r.stderr
    assert "'abc'" in r.stderr


def test_int_flag_bad_env_value(monkeypatch):
    """Non-integer env var for int flag -> exit 1, stderr says expected integer."""
    app = strictcli.App(name="test", version="1.0.0", help="test app", env_prefix="MYAPP")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag("port", type=int, help="the port", default=80, env="MYAPP_PORT")
    def cmd(ctx, port):
        print(f"port={port}")

    monkeypatch.setenv("MYAPP_PORT", "abc")
    r = app.test(["cmd"])
    assert r.exit_code == 1
    assert "expected integer" in r.stderr
    assert "'abc'" in r.stderr


def test_int_flag_reject_underscore_separators():
    """'1_000' (PEP 515 digit separators) is rejected to match Go/TypeScript."""
    app = _make_app_with_int_flag()
    r = app.test(["cmd", "--port", "1_000"])
    assert r.exit_code == 1
    assert "expected integer" in r.stderr
    assert "'1_000'" in r.stderr


def test_int_flag_reject_underscore_env_value(monkeypatch):
    """Underscore-separated int env var is rejected."""
    app = strictcli.App(name="test", version="1.0.0", help="test app", env_prefix="MYAPP")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag("port", type=int, help="the port", default=80, env="MYAPP_PORT")
    def cmd(ctx, port):
        print(f"port={port}")

    monkeypatch.setenv("MYAPP_PORT", "1_000")
    r = app.test(["cmd"])
    assert r.exit_code == 1
    assert "expected integer" in r.stderr
    assert "'1_000'" in r.stderr


def test_int_flag_required():
    """Int flag with no default and omitted -> exit 1, stderr says required."""
    app = _make_app_with_int_flag()
    r = app.test(["cmd"])
    assert r.exit_code == 1
    assert "required" in r.stderr


def test_int_flag_equals_syntax():
    """Int flag with --port=8080 -> receives 8080."""
    app = _make_app_with_int_flag()
    r = app.test(["cmd", "--port=8080"])
    assert r.exit_code == 0
    assert "port=8080" in r.stdout


def test_int_flag_negative_value():
    """Int flag with negative value --offset -5 -> receives -5."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag("offset", type=int, help="the offset", presence="required")
    def cmd(ctx, offset):
        print(f"offset={offset}")

    r = app.test(["cmd", "--offset", "-5"])
    assert r.exit_code == 0
    assert "offset=-5" in r.stdout


def test_int_flag_help_shows_int():
    """Int flag is shown as <int> in help output."""
    app = _make_app_with_int_flag(default=8000)
    r = app.test(["cmd", "--help"])
    assert r.exit_code == 0
    assert "<int>" in r.stdout


def test_int_default_must_be_int():
    """Flag(type=int, default="8000") raises ValueError at registration."""
    with pytest.raises(ValueError, match="int default"):
        strictcli.Flag(name="port", type=int, help="the port", default="8000")


def test_int_flag_short_form():
    """Int flag via short flag -p 8080 works."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag("port", short="p", type=int, help="the port", presence="required")
    def cmd(ctx, port):
        print(f"port={port}")

    r = app.test(["cmd", "-p", "8080"])
    assert r.exit_code == 0
    assert "port=8080" in r.stdout


def test_int_flag_bad_value_equals_syntax():
    """Non-integer value via equals syntax -> error."""
    app = _make_app_with_int_flag()
    r = app.test(["cmd", "--port=abc"])
    assert r.exit_code == 1
    assert "expected integer" in r.stderr


# An integer value is plain decimal digits with an optional leading minus sign
# (contract §30): every other spelling int() would take is refused, on the
# command line, from the environment, and in `config set`.

_DECIMAL_ACCEPTED = [("30", 30), ("-5", -5), ("0", 0), ("-0", 0)]
_DECIMAL_REFUSED = [
    "+30", "+0", "030", "-030", "00", "-00", " 30", "30 ", "3_0",
    "٣٠",  # Arabic-Indic digits
    "３０",  # fullwidth digits
    "1e3", "0x1e", "-", "+", "",
]


def test_strict_int_accepts_plain_decimal():
    for text, value in _DECIMAL_ACCEPTED:
        assert strictcli._strict_int(text) == value


@pytest.mark.parametrize("text", _DECIMAL_REFUSED)
def test_strict_int_refuses_other_forms(text):
    with pytest.raises(ValueError) as exc:
        strictcli._strict_int(text)
    assert str(exc.value) == f"expected integer, got '{text}'"


@pytest.mark.parametrize("text", ["+30", "030", "-030", "٣٠"])
def test_int_flag_refuses_non_decimal_form_on_command_line(text):
    app = _make_app_with_int_flag()
    r = app.test(["cmd", "--port", text])
    assert r.exit_code == 1
    assert f"error: --port: expected integer, got '{text}'" in r.stderr


@pytest.mark.parametrize("text", ["+30", "030"])
def test_int_flag_refuses_non_decimal_form_from_env(monkeypatch, text):
    app = strictcli.App(name="test", version="1.0.0", help="test app", env_prefix="MYAPP")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag("port", type=int, help="the port", default=80, env="MYAPP_PORT")
    def cmd(ctx, port):
        print(f"port={port}")

    monkeypatch.setenv("MYAPP_PORT", text)
    r = app.test(["cmd"])
    assert r.exit_code == 1
    assert f"error: --port: expected integer, got '{text}' (from env var 'MYAPP_PORT')" in r.stderr


@pytest.mark.parametrize("text", ["+30", "030"])
def test_config_set_refuses_non_decimal_int(tmp_path, text):
    config_file = tmp_path / "config.json"
    app = strictcli.App(name="testapp", version="1.0.0", help="test app", config=True, config_path=str(config_file))

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("count", type=int, help="how many", default=0)
    def run(ctx, count):
        pass

    r = app.test(["config", "set", "count", "--value", text])
    assert r.exit_code == 1
    assert f"error: config set: key 'count': expected integer, got '{text}'" in r.stderr
    assert not config_file.exists()
