"""The framework's help and version commands.

Help pages by address, per-flag help, --depth, the help document under --json
(which replaced --dump-schema), and the refusals that point at the right
spelling. Every refusal that names a spelling is followed by that spelling.
"""

import json

import pytest

import strictcli
from strictcli import _canonical_json, _dump_schema, choice, choice_flag, sub_flag


@choice("email", help="as an email")
class Email:
    subject: str = sub_flag(help="the subject line", presence="required")


@choice("sms", help="as a text")
class Sms:
    pass


def _app():
    app = strictcli.App(
        name="myapp", version="1.0.0", help="test app",
        flags=[strictcli.Flag(name="trace", type=bool, help="trace every step", default=False)],
    )

    @app.command("run", help="run something", effect="read_only")
    @strictcli.flag("device", type=str, help="which GPU to target", presence="required", short="d")
    @strictcli.flag("fast", type=bool, help="skip the slow checks", default=False)
    @choice_flag("via", help="delivery channel", presence="required",
                 elect_by="selector-token", choices=[Email, Sms])
    def run(ctx, device, fast, via: Email | Sms, trace):
        return 0

    db = app.group("db", help="database commands")

    @db.command("migrate", help="run migrations", effect="read_only")
    def migrate(ctx, trace):
        return 0

    backup = db.group("backup", help="backup commands")

    @backup.command("take", help="take a backup", effect="read_only")
    def take(ctx, trace):
        return 0

    return app


def _ok(app, *argv):
    r = app.test(list(argv))
    assert r.exit_code == 0, (argv, r.stderr)
    return r


def _refused(app, message, *argv):
    r = app.test(list(argv))
    assert r.exit_code == 1, (argv, r.stdout)
    assert r.stderr.startswith(f"error: {message}\n"), (argv, r.stderr)
    return r


ENVELOPE = {
    "interface_version": 3, "app": "myapp", "app_version": "1.0.0",
    "exit_code": 0, "payload": None, "output": None, "dry_run": False,
    "writes": None, "preview": [], "preview_error": None, "diagnostics": [],
}


@pytest.mark.parametrize("command, flag", [
    (["help"], ["--help"]),
    (["help", "db"], ["db", "--help"]),
    (["help", "db", "backup"], ["db", "backup", "--help"]),
    (["help", "run"], ["run", "--help"]),
    (["help", "db", "migrate"], ["db", "migrate", "--help"]),
])
def test_the_help_command_matches_the_help_flag(command, flag):
    app = _app()
    assert _ok(app, *command).stdout == _ok(app, *flag).stdout


def test_root_and_group_pages_name_the_help_command():
    app = _app()
    assert _ok(app, "help").stdout.endswith(
        "\nUse 'myapp help <command>' for more information.\n"
    )
    assert _ok(app, "help", "db").stdout.endswith(
        "\nUse 'myapp help db <command>' for more information.\n"
    )


def test_one_flag():
    app = _app()
    assert _ok(app, "help", "run", "--device").stdout == (
        "myapp run -- run something\n\nFlags:\n"
        "  --device, -d <str>    which GPU to target [required]\n"
    )
    assert _ok(app, "help", "run", "--subject").stdout == (
        "myapp run -- run something\n\nFlags:\n"
        "  --via <choice>         delivery channel [required]\n"
        "    email                as an email\n"
        "      --subject <str>    the subject line [required]\n"
    )
    assert _ok(app, "help", "run", "--trace").stdout == (
        "myapp run -- run something\n\nGlobal flags:\n"
        "  --trace, --no-trace    trace every step [default: false]\n"
    )


def test_depth():
    app = _app()
    assert _ok(app, "help", "--depth", "2").stdout == (
        "myapp v1.0.0 -- test app\n\n"
        "Commands:\n  run           run something\n  db migrate    run migrations\n\n"
        "Groups:\n  db           database commands\n  db backup    backup commands\n\n"
        "Global flags:\n  --trace    trace every step\n\n"
        "Use 'myapp help <command>' for more information.\n"
    )
    assert _ok(app, "help", "--depth=3", "db").stdout == (
        "myapp db -- database commands\n\n"
        "Commands:\n  migrate        run migrations\n  backup take    take a backup\n\n"
        "Groups:\n  backup    backup commands\n\n"
        "Use 'myapp help db <command>' for more information.\n"
    )
    assert _ok(app, "help", "--depth", "1").stdout == _ok(app, "--help").stdout


def test_refusals_and_their_fixes():
    app = _app()
    _refused(app, "unknown command 'nope'", "help", "nope")
    _refused(app, "unknown command 'nope' in 'db'", "help", "db", "nope")
    for bad in ("0", "two", "01"):
        _refused(app, f"help: --depth: invalid value '{bad}': must be an integer of at least 1",
                 "help", "--depth", bad, "db")
    _refused(app, "help: --depth requires a value", "help", "--depth")
    _refused(app, "help: --depth lists the command tree below the app or a group; 'run' is a command",
             "help", "--depth", "2", "run")
    _refused(app, "help: unknown option '--all': help's only option is --depth <int>, and a flag "
                  "is addressed after its command: 'myapp help <command> --all'", "help", "--all")
    _refused(app, "command 'run' has no flag '--devise'; its flags: --device, --fast, --via, "
                  "--subject, --trace", "help", "run", "--devise")
    _refused(app, "help: 'extra' follows the command 'run'; only one of its flags may follow a "
                  "command (--<flag>)", "help", "run", "extra")
    _refused(app, "help: '--fast' follows the flag address; an address ends at one flag",
             "help", "run", "--device", "--fast")
    _refused(app, "help: '-d' is a short form; address the flag by its long name: "
                  "'myapp help run --device'", "help", "run", "-d")
    _ok(app, "help", "run", "--device")
    _refused(app, "help: '--device=x' carries a value; a flag address is the flag alone: "
                  "'myapp help run --device'", "help", "run", "--device=x")
    _refused(app, "help: '--no-fast' is another spelling of a declared flag; address the "
                  "declaration: 'myapp help run --fast'", "help", "run", "--no-fast")
    _ok(app, "help", "run", "--fast")
    _refused(app, "help: --depth is help's own option and goes before the address: "
                  "'myapp help --depth <int> db'", "help", "db", "--depth", "2")
    _ok(app, "help", "--depth", "2", "db")
    _refused(app, "help: '--fast' names a flag, and a flag is addressed after its command: "
                  "'myapp help db <command> --fast'", "help", "db", "--fast")
    _refused(app, "help: '--trace' names a flag, and a flag is addressed after its command: "
                  "'myapp help <command> --trace'", "help", "--trace")
    _ok(app, "help", "run", "--trace")
    _refused(app, "'help' is a framework command at the root: use 'myapp help db'", "db", "help")
    _refused(app, "'version' is a framework command at the root: use 'myapp version'", "db", "version")
    _ok(app, "help", "db")
    _ok(app, "version")


def test_own_pages():
    app = _app()
    page = _ok(app, "help", "--help").stdout
    assert page.startswith(
        "myapp help -- show the help of the app, a group, a command, or one of its flags\n"
    )
    assert _ok(app, "help", "run", "-h").stdout == page
    assert _ok(app, "version", "--help").stdout == (
        "myapp version -- show the app's name and version\n\nWith --json, they are printed as JSON.\n"
    )


def test_version_command():
    app = _app()
    assert _ok(app, "version").stdout == "myapp 1.0.0\n"
    assert _ok(app, "version").stdout == _ok(app, "--version").stdout
    r = _ok(app, "version", "--json")
    assert r.stdout == '{\n  "name": "myapp",\n  "version": "1.0.0"\n}\n'
    assert json.loads(r.stderr) == {**ENVELOPE, "command": "version"}
    _refused(app, "version takes no arguments, got 'extra'", "version", "extra")
    _refused(app, "the version line is text; for the machine form use 'myapp version --json'",
             "--version", "--json")
    _ok(app, "version", "--json")


@pytest.mark.parametrize("argv, fix", [
    (["--help", "--json"], "myapp help --json"),
    (["--json"], "myapp help --json"),
    (["db", "--help", "--json"], "myapp help db --json"),
    (["run", "--help", "--json"], "myapp help run --json"),
    (["db", "migrate", "--json", "-h"], "myapp help db migrate --json"),
])
def test_the_help_flag_is_text_only(argv, fix):
    app = _app()
    _refused(app, f"help pages are text; for the machine form use '{fix}'", *argv)
    _ok(app, *fix.split()[1:])


def test_the_whole_app_document_is_the_schema():
    app = _app()
    r = _ok(app, "help", "--json")
    assert r.stdout == _canonical_json(_dump_schema(app)) + "\n"
    doc = json.loads(r.stdout)
    # The tests' own module lies in the strictcli project, whose name the
    # project_id is (the nearest pyproject.toml above it).
    assert doc["project_id"] == "strictcli"
    assert "address" not in doc
    assert json.loads(r.stderr) == {**ENVELOPE, "command": "help"}


def test_slices():
    app = _app()
    doc = json.loads(_ok(app, "help", "db", "migrate", "--json").stdout)
    assert doc["address"] == ["db", "migrate"]
    assert "commands" not in doc
    db = doc["groups"]["db"]
    assert list(db["commands"]) == ["migrate"]
    assert "groups" not in db

    doc = json.loads(_ok(app, "help", "run", "--subject", "--json").stdout)
    flags = doc["commands"]["run"]["flags"]
    assert [f["name"] for f in flags] == ["via"]
    assert [c["name"] for c in flags[0]["choices"]] == ["email"]
    assert "global_flags" not in doc

    doc = json.loads(_ok(app, "help", "--depth", "1", "--json").stdout)
    assert doc["depth"] == 1
    assert "commands" not in doc["groups"]["db"]
    assert "groups" not in doc["groups"]["db"]


def test_dump_schema_is_refused_naming_the_help_document():
    app = _app()
    _refused(app, "--dump-schema is not supported; the app's help document is printed by "
                  "'myapp help --json'", "--dump-schema")
    _ok(app, "help", "--json")


def test_help_json_writes_no_file(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    _ok(_app(), "help", "--json")
    assert list(tmp_path.iterdir()) == []


def test_a_project_is_named_by_the_nearest_pyproject(tmp_path):
    (tmp_path / "pyproject.toml").write_text('[project]\nname = "example-tool"\n')
    (tmp_path / "pkg").mkdir()
    module = tmp_path / "pkg" / "cli.py"
    module.write_text("")
    assert strictcli._project_id_for_file(str(module)) == "example-tool"
    with pytest.raises(RuntimeError) as exc:
        strictcli._project_id_for_file(str(tmp_path.parent / "loose.py"))
    assert str(exc.value).startswith("cannot determine project_id: ")
