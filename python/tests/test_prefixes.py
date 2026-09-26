"""Human-mode prefixes on the never-suppressed diagnostic writers (§19.14)."""

import json

import strictcli


def _app(*diagnostics):
    app = strictcli.App(name="app", version="1.0.0", help="app")

    @app.command("run", effect="read_only", help="run")
    def _run(ctx):
        for level, message in diagnostics:
            getattr(ctx, level)(message)
        return 0

    return app


def test_warn_and_error_are_prefixed_on_stderr():
    r = _app(("warn", "disk almost full"), ("error", "upload failed")).test(["run"])
    assert r.stdout == ""
    assert r.stderr == "warning: disk almost full\nerror: upload failed\n"


def test_quiet_keeps_both_prefixed_lines():
    r = _app(("warn", "w"), ("error", "e")).test(["--quiet", "run"])
    assert r.stderr == "warning: w\nerror: e\n"


def test_a_message_already_carrying_the_prefix_is_prefixed_again():
    r = _app(("error", "error: upload failed")).test(["run"])
    assert r.stderr == "error: error: upload failed\n"


def test_a_multi_line_message_is_prefixed_once():
    r = _app(("warn", "first\nsecond")).test(["run"])
    assert r.stderr == "warning: first\nsecond\n"


def test_info_and_debug_stay_unprefixed():
    r = _app(("info", "loading"), ("debug", "cache hit")).test(["--verbose", "run"])
    assert r.stdout == "loading\ncache hit\n"
    assert r.stderr == ""


def test_machine_mode_stores_the_messages_unprefixed():
    r = _app(("warn", "w"), ("error", "e")).test(["--json", "run"])
    assert json.loads(r.stdout)["diagnostics"] == [
        {"level": "warn", "message": "w"},
        {"level": "error", "message": "e"},
    ]
    assert r.stderr == ""


def test_the_config_parse_error_is_prefixed_once(tmp_path):
    """A framework command's own error goes through ctx.error like any other,
    so it must not carry a prefix of its own."""
    cfg = tmp_path / "config.json"
    cfg.write_text("{not json")
    app = strictcli.App(
        name="app", version="1.0.0", help="app", config=True,
        config_path=str(cfg),
    )

    @app.command("run", effect="read_only", help="run")
    def _run(ctx):
        return 0

    r = app.test(["config", "show"])
    assert r.exit_code == 1
    assert r.stderr.startswith("error: ")
    assert not r.stderr.startswith("error: error: ")
    r = app.test(["--json", "config", "show"])
    diagnostics = json.loads(r.stdout)["diagnostics"]
    assert diagnostics and not diagnostics[-1]["message"].startswith("error: ")
