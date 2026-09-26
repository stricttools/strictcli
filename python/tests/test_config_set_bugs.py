"""Red tests for config set bugs.

These tests document bugs in the config set command and are expected to
FAIL with the current code. They will turn green when the bugs are fixed.

Bug 0.4: config set stores raw strings instead of typed values.
Bug 0.6: config set accepts unknown keys without validation.
"""

import json

import strictcli


def test_config_set_writes_typed_values(tmp_path):
    """config set should write typed values (int, bool, float), not strings.

    Bug: _config_set_handler stores the raw string from argv without
    coercing it to the flag's declared type. So 'config set count 42'
    writes {"count": "42"} instead of {"count": 42}.
    """
    config_file = tmp_path / "config.json"

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("count", type=int, help="how many", default=0)
    @strictcli.flag("loud", type=bool, help="be loud", default=False)
    @strictcli.flag("rate", type=float, help="the rate", default=0.0)
    def run(ctx, count, loud, rate):
        pass

    r = app.test(["config", "set", "count", "--value", "42"])
    assert r.exit_code == 0

    r = app.test(["config", "set", "loud", "--value", "true"])
    assert r.exit_code == 0

    r = app.test(["config", "set", "rate", "--value", "3.14"])
    assert r.exit_code == 0

    data = json.loads(config_file.read_text())
    assert isinstance(data["count"], int), f"expected int, got {type(data['count']).__name__}: {data['count']!r}"
    assert data["count"] == 42
    assert isinstance(data["loud"], bool), f"expected bool, got {type(data['loud']).__name__}: {data['loud']!r}"
    assert data["loud"] is True
    assert isinstance(data["rate"], float), f"expected float, got {type(data['rate']).__name__}: {data['rate']!r}"
    assert data["rate"] == 3.14


def test_config_set_rejects_unknown_key(tmp_path):
    """config set should reject keys that don't correspond to any registered flag.

    Bug: _config_set_handler accepts any key without checking if it
    matches a registered flag. 'config set nonexistent value' silently
    writes {"nonexistent": "value"} to the config file.
    """
    config_file = tmp_path / "config.json"

    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("name", type=str, help="the name", default="")
    def run(ctx, name):
        pass

    r = app.test(["config", "set", "nonexistent", "--value", "value"])
    assert r.exit_code != 0, f"expected nonzero exit code, got {r.exit_code}"
    assert "nonexistent" in r.stderr, f"expected error about unknown key in stderr, got: {r.stderr!r}"


def _make_app(tmp_path, **extra_flags):
    """Helper: create an app with common typed flags for config set tests."""
    config_file = tmp_path / "config.json"
    app = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
    )

    @app.command("run", effect="read_only", help="run something")
    @strictcli.flag("debug", type=bool, help="debug mode", default=False)
    @strictcli.flag("count", type=int, help="how many", default=0)
    @strictcli.flag("rate", type=float, help="the rate", default=0.0)
    @strictcli.flag("name", type=str, help="the name", default="")
    def run(ctx, debug, count, rate, name):
        pass

    return app, config_file


def test_config_set_typed_bool(tmp_path):
    """config set coerces boolean values and stores them as JSON booleans."""
    app, config_file = _make_app(tmp_path)

    for true_val in ("true", "yes", "1", "True", "YES"):
        r = app.test(["config", "set", "debug", "--value", true_val])
        assert r.exit_code == 0, f"exit_code={r.exit_code} for '{true_val}': {r.stderr}"
        data = json.loads(config_file.read_text())
        assert data["debug"] is True, f"expected True for '{true_val}', got {data['debug']!r}"

    for false_val in ("false", "no", "0", "False", "NO"):
        r = app.test(["config", "set", "debug", "--value", false_val])
        assert r.exit_code == 0, f"exit_code={r.exit_code} for '{false_val}': {r.stderr}"
        data = json.loads(config_file.read_text())
        assert data["debug"] is False, f"expected False for '{false_val}', got {data['debug']!r}"


def test_config_set_typed_int(tmp_path):
    """config set coerces integer values and stores them as JSON integers."""
    app, config_file = _make_app(tmp_path)

    r = app.test(["config", "set", "count", "--value", "42"])
    assert r.exit_code == 0
    data = json.loads(config_file.read_text())
    assert isinstance(data["count"], int)
    assert data["count"] == 42

    r = app.test(["config", "set", "count", "--value", "0"])
    assert r.exit_code == 0
    data = json.loads(config_file.read_text())
    assert isinstance(data["count"], int)
    assert data["count"] == 0


def test_config_set_typed_float(tmp_path):
    """config set coerces float values and stores them as JSON floats."""
    app, config_file = _make_app(tmp_path)

    r = app.test(["config", "set", "rate", "--value", "3.14"])
    assert r.exit_code == 0
    data = json.loads(config_file.read_text())
    assert isinstance(data["rate"], float)
    assert data["rate"] == 3.14

    # Integer-like value stored as float when flag type is float
    r = app.test(["config", "set", "rate", "--value", "3"])
    assert r.exit_code == 0
    data = json.loads(config_file.read_text())
    assert isinstance(data["rate"], float)
    assert data["rate"] == 3.0


def test_config_set_bad_value(tmp_path):
    """config set rejects values that don't parse as the flag's type."""
    app, _ = _make_app(tmp_path)

    # Bad int
    r = app.test(["config", "set", "count", "--value", "abc"])
    assert r.exit_code != 0
    assert "expected integer, got 'abc'" in r.stderr

    # Bad bool
    r = app.test(["config", "set", "debug", "--value", "maybe"])
    assert r.exit_code != 0
    assert "expected boolean, got 'maybe'" in r.stderr

    # Bad float
    r = app.test(["config", "set", "rate", "--value", "xyz"])
    assert r.exit_code != 0
    assert "expected float, got 'xyz'" in r.stderr

    # NaN rejected for float
    r = app.test(["config", "set", "rate", "--value", "nan"])
    assert r.exit_code != 0
    assert "NaN is not allowed" in r.stderr

    # Inf rejected for float
    r = app.test(["config", "set", "rate", "--value", "inf"])
    assert r.exit_code != 0
    assert "Inf is not allowed" in r.stderr


def test_config_set_unknown_key_error(tmp_path):
    """config set error format for unknown keys matches Go exactly."""
    app, _ = _make_app(tmp_path)

    r = app.test(["config", "set", "xyz", "--value", "value"])
    assert r.exit_code == 1
    assert r.stderr == "error: config set: unknown key 'xyz'\n"

    r = app.test(["config", "set", "nonexistent_flag", "--value", "42"])
    assert r.exit_code == 1
    assert r.stderr == "error: config set: unknown key 'nonexistent_flag'\n"


def test_config_set_negative_int(tmp_path):
    """config set accepts negative integers like -7 as positional values."""
    app, config_file = _make_app(tmp_path)

    r = app.test(["config", "set", "count", "--value", "-7"])
    assert r.exit_code == 0, f"exit_code={r.exit_code}, stderr={r.stderr!r}"

    data = json.loads(config_file.read_text())
    assert isinstance(data["count"], int), f"expected int, got {type(data['count']).__name__}"
    assert data["count"] == -7


def test_config_set_negative_float(tmp_path):
    """config set accepts negative floats like -3.14 as positional values."""
    app, config_file = _make_app(tmp_path)

    r = app.test(["config", "set", "rate", "--value", "-3.14"])
    assert r.exit_code == 0, f"exit_code={r.exit_code}, stderr={r.stderr!r}"

    data = json.loads(config_file.read_text())
    assert isinstance(data["rate"], float), f"expected float, got {type(data['rate']).__name__}"
    assert data["rate"] == -3.14


def test_config_set_round_trip_typed(tmp_path):
    """Values set via config set are read back with correct types by a new app."""
    app, config_file = _make_app(tmp_path)

    app.test(["config", "set", "debug", "--value", "true"])
    app.test(["config", "set", "count", "--value", "42"])
    app.test(["config", "set", "rate", "--value", "3.14"])
    app.test(["config", "set", "name", "--value", "hello"])

    # Create a fresh app pointing at the same config file
    app2 = strictcli.App(
        name="testapp",
        version="1.0.0",
        help="test app",
        config=True,
        config_path=str(config_file),
    )

    received = {}

    @app2.command("run", effect="read_only", help="run something")
    @strictcli.flag("debug", type=bool, help="debug mode", default=False)
    @strictcli.flag("count", type=int, help="how many", default=0)
    @strictcli.flag("rate", type=float, help="the rate", default=0.0)
    @strictcli.flag("name", type=str, help="the name", default="")
    def run(ctx, debug, count, rate, name):
        received["debug"] = debug
        received["count"] = count
        received["rate"] = rate
        received["name"] = name

    r = app2.test(["run"])
    assert r.exit_code == 0

    assert received["debug"] is True
    assert isinstance(received["debug"], bool)
    assert received["count"] == 42
    assert isinstance(received["count"], int)
    assert received["rate"] == 3.14
    assert isinstance(received["rate"], float)
    assert received["name"] == "hello"
    assert isinstance(received["name"], str)
