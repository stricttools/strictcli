"""The framework's own commands report through the error writer (§19.14's box).

`config set`, `config init`, `config edit` and `check` print `error: <message>`
once in human mode and carry the message unprefixed as an `error` diagnostic
under --json, with nothing on stderr.
"""

import json

import strictcli


def _config_app(tmp_path, *, exists=False):
    path = tmp_path / "config.json"
    if exists:
        path.write_text("{}\n")
    app = strictcli.App(
        name="app", version="1.0.0", help="app", config=True,
        config_path=str(path),
    )

    @app.command("run", effect="read_only", help="run")
    @strictcli.flag("count", type=int, help="how many", default=0)
    def _run(ctx, count):
        return 0

    return app, path


def test_config_set_unknown_key_prints_once_with_the_prefix(tmp_path):
    app, _ = _config_app(tmp_path)
    r = app.test(["config", "set", "nope", "--value", "1"])
    assert (r.exit_code, r.stdout) == (1, "")
    assert r.stderr == "error: config set: unknown key 'nope'\n"


def test_config_set_bad_value_prints_once_with_the_prefix(tmp_path):
    app, _ = _config_app(tmp_path)
    r = app.test(["config", "set", "count", "--value", "abc"])
    assert r.stderr == (
        "error: config set: key 'count': expected integer, got 'abc'\n"
    )


def test_config_set_error_under_json_is_a_diagnostic(tmp_path):
    app, _ = _config_app(tmp_path)
    r = app.test(["--json", "config", "set", "nope", "--value", "1"])
    assert (r.exit_code, r.stderr) == (1, "")
    assert json.loads(r.stdout)["diagnostics"] == [
        {"level": "error", "message": "config set: unknown key 'nope'"},
    ]


def test_config_init_refusal_goes_through_the_writer(tmp_path):
    app, path = _config_app(tmp_path, exists=True)
    r = app.test(["config", "init"])
    assert r.exit_code == 1
    assert r.stderr == f"error: config init: config file already exists: {path}\n"
    r = app.test(["--json", "config", "init"])
    assert r.stderr == ""
    assert json.loads(r.stdout)["diagnostics"] == [
        {"level": "error",
         "message": f"config init: config file already exists: {path}"},
    ]


def test_config_edit_failure_goes_through_the_writer(tmp_path, monkeypatch):
    app, _ = _config_app(tmp_path, exists=True)
    monkeypatch.setenv("EDITOR", "false")
    r = app.test(["config", "edit"])
    assert r.exit_code == 1
    assert r.stderr.startswith("error: editor failed: ")
    assert not r.stderr.startswith("error: error: ")
    assert r.stderr.count("\n") == 1


def _check_app(tmp_path):
    toml = tmp_path / "checks.toml"
    toml.write_text(
        'app = "app"\n[checks.lint]\ndescription = \"Checks lint\"\nsubject = \"quality\"\ntags = ["release"]\nseverity = "error"\n'
        "fast = true\npure = true\nneeds_network = false\ndepends_on = []\n"
    )
    app = strictcli.App(
        name="app", version="1.0.0", help="app", checks_path=str(toml),
    )

    @app.error_check("lint")
    def _lint(ctx, rep):
        return rep.passed("ok")

    app.set_check_context(lambda: None)
    return app


def test_check_tag_expression_error_goes_through_the_writer(tmp_path):
    app = _check_app(tmp_path)
    r = app.test(["check", "--tag", "(("])
    assert (r.exit_code, r.stdout) == (1, "")
    assert r.stderr == (
        "error: tag expression: unexpected end of expression at position 2\n"
    )
    r = app.test(["--json", "check", "--tag", "(("])
    assert r.stderr == ""
    assert json.loads(r.stdout)["diagnostics"] == [
        {"level": "error",
         "message": "tag expression: unexpected end of expression at position 2"},
    ]
