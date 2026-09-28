"""The naming rule and the framework-command reservation.

Every identifier a caller types or references is lowercase kebab-case of at
least two characters, and a short form is one ASCII letter. `help` and
`version` are framework commands, reserved at every level of the command tree.
Each refusal names the rule, so each test also applies the fix it names (a
conforming spelling) and sees the declaration accepted.
"""

import functools
import operator

import pytest

import strictcli
from strictcli import (
    AtLeastOne, Member, UpdateOf, choice, choice_flag, sub_flag,
)
from strictcli import _parse_checks_toml_full

CLAUSE = (
    "must be lowercase kebab-case of at least two characters: "
    "[a-z][a-z0-9]*(-[a-z0-9]+)*"
)

NON_KEBAB = ["Compile", "compile_all", "COMPILE", "c", "fast-", "a--b", "-x", "9lives", "déploy"]
KEBAB = ["compile", "compile-all", "c0", "x86-64", "ab"]


def _app():
    return strictcli.App(name="myapp", version="1.0.0", help="test app")


def _command(target, name, **kw):
    @target.command(name, help="x", effect="read_only", **kw)
    def _h(ctx):
        return 0


@pytest.mark.parametrize("name", NON_KEBAB)
def test_command_names_are_refused(name):
    with pytest.raises(ValueError) as exc:
        _command(_app(), name)
    assert str(exc.value) == f'command name "{name}" {CLAUSE}'
    with pytest.raises(ValueError) as exc:
        _command(_app().group("grp", help="a group"), name)
    assert str(exc.value) == f'command name "{name}" {CLAUSE}'


@pytest.mark.parametrize("name", KEBAB)
def test_kebab_command_names_are_accepted(name):
    _command(_app(), name)
    _command(_app().group("grp", help="a group"), name)


@pytest.mark.parametrize("name", ["Db", "db_tools", "d", "db-"])
def test_group_and_deprecated_names_are_refused(name):
    for register in (
        lambda: _app().group(name, help="a group"),
        lambda: _app().group("grp", help="g").group(name, help="a group"),
    ):
        with pytest.raises(ValueError) as exc:
            register()
        assert str(exc.value) == f'group name "{name}" {CLAUSE}'
    for register in (
        lambda: _app().deprecate(name, message="gone"),
        lambda: _app().group("grp", help="g").deprecate(name, message="gone"),
    ):
        with pytest.raises(ValueError) as exc:
            register()
        assert str(exc.value) == f'deprecated command name "{name}" {CLAUSE}'
    app = _app()
    app.group("db-tools", help="a group").group("sub", help="a sub group")
    app.deprecate("old-cmd", message="gone")


@pytest.mark.parametrize("name", ["Device", "device_id", "DEVICE", "d", "m", "F", "device-"])
def test_flag_names_are_refused(name):
    with pytest.raises(ValueError) as exc:
        strictcli.Flag(name=name, type=str, help="help text", presence="required")
    assert str(exc.value) == f'flag name "{name}" {CLAUSE}'
    strictcli.Flag(name="device-id", type=str, help="help text", presence="required")


def test_a_scoped_flag_runs_the_same_rule():
    @choice("email", help="as mail")
    class Email:
        Subject: str = sub_flag(help="the subject", presence="required")

    @choice("sms", help="as text")
    class Sms:
        pass

    app = _app()
    with pytest.raises(ValueError) as exc:

        @app.command("send", help="send", effect="read_only")
        @choice_flag("via", help="pick", presence="required",
                     elect_by="selector-token", choices=[Email, Sms])
        def send(ctx, via: Email | Sms):
            return 0

    assert str(exc.value) == f'flag name "Subject" {CLAUSE}'

    @choice("email", help="as mail")
    class Email2:
        subject_line: str = sub_flag(help="the subject", presence="required")

    @app.command("send", help="send", effect="read_only")
    @choice_flag("via", help="pick", presence="required",
                 elect_by="selector-token", choices=[Email2, Sms])
    def send2(ctx, via: Email2 | Sms):
        return 0


def test_a_member_choice_name_is_a_flag_name():
    @choice("w", help="the work profile")
    class W:
        pass

    @choice("home", help="the home profile")
    class Home:
        pass

    app = _app()
    with pytest.raises(ValueError) as exc:

        @app.command("run", help="run", effect="read_only")
        @choice_flag("profile", help="which profile", presence="required",
                     elect_by="member-flags", choices=[W, Home])
        def run(ctx, profile: W | Home):
            return 0

    assert str(exc.value) == f'flag name "w" {CLAUSE}'

    @choice("work", help="the work profile")
    class Work:
        pass

    @app.command("run", help="run", effect="read_only")
    @choice_flag("profile", help="which profile", presence="required",
                 elect_by="member-flags", choices=[Work, Home])
    def run2(ctx, profile: Work | Home):
        return 0


@pytest.mark.parametrize("short", ["1", "ab", "é", "-", "_"])
def test_short_forms_are_one_ascii_letter(short):
    with pytest.raises(ValueError) as exc:
        strictcli.Flag(name="device", type=str, help="h", presence="required", short=short)
    assert str(exc.value) == (
        f'Flag "device": short form "{short}" must be one ASCII letter (a-z or A-Z)'
    )


@pytest.mark.parametrize("short", ["d", "D", "F", "m"])
def test_letter_short_forms_are_accepted(short):
    strictcli.Flag(name="device", type=str, help="h", presence="required", short=short)


def test_global_flag_names():
    with pytest.raises(ValueError) as exc:
        strictcli.App(name="myapp", version="1.0.0", help="t", flags=[
            strictcli.Flag(name="Verbose_out", type=bool, help="more", default=False),
        ])
    assert str(exc.value) == f'flag name "Verbose_out" {CLAUSE}'
    strictcli.App(name="myapp", version="1.0.0", help="t", flags=[
        strictcli.Flag(name="verbose-out", type=bool, help="more", default=False),
    ])


@pytest.mark.parametrize("name", ["a", "Email", "e_mail", "email-"])
def test_choice_names_are_refused(name):
    with pytest.raises(ValueError) as exc:
        _selector_app([name, "sms"])
    assert str(exc.value) == f'Flag "via": choice name "{name}" {CLAUSE}'
    _selector_app(["e-mail", "sms"])


def _selector_app(names):
    classes = [choice(n, help="c")(type(f"C{i}", (), {})) for i, n in enumerate(names)]
    app = _app()

    def send(ctx, via):
        return 0

    send.__annotations__["via"] = functools.reduce(operator.or_, classes)
    decorate = choice_flag("via", help="pick", presence="required",
                           elect_by="selector-token", choices=classes)
    app.command("send", help="send", effect="read_only")(decorate(send))


@pytest.mark.parametrize("tag", ["fast-", "a--b", "x", "Fast"])
def test_tag_names_are_refused(tag):
    msg = f'invalid tag name "{tag}": {CLAUSE}'
    with pytest.raises(ValueError) as exc:
        _command(_app(), "cmd", tags={tag})
    assert str(exc.value) == msg
    with pytest.raises(ValueError) as exc:
        _app().group("grp", help="g", tags={tag})
    assert str(exc.value) == msg
    with pytest.raises(ValueError) as exc:
        _app().tag_contract(tag, requires_flag="force-overwrite")
    assert str(exc.value) == msg
    _command(_app(), "cmd", tags={"fast-lane"})


def test_constraint_grant_and_resource_names():
    def constrained(name):
        app = _app()

        @app.command("cmd", help="x", effect="read_only",
                     constraints=[AtLeastOne(name, [Member("file"), Member("host")])])
        @strictcli.flag("file", type=str, help="a file", presence="optional")
        @strictcli.flag("host", type=str, help="a host", presence="optional")
        def cmd(ctx, file, host):
            return 0

    with pytest.raises(ValueError) as exc:
        constrained("p")
    assert str(exc.value) == f'command "cmd": constraint name "p" {CLAUSE}'
    constrained("pick")

    def granted(name):
        app = _app()

        @app.command("cmd", help="x", effect="mutating",
                     grants=[strictcli.Grant(name, "r", strictcli.PROC_MUTATE)])
        def cmd(ctx):
            return 0

    with pytest.raises(ValueError) as exc:
        granted("push-")
    assert str(exc.value) == f"command \"cmd\": invalid grant name 'push-': {CLAUSE}"
    granted("push-it")

    def updated(resource):
        app = _app()

        @app.command("cmd", help="x", effect="mutating",
                     update_of=UpdateOf(resource, write_mode="sparse", properties=["content"]))
        @strictcli.flag("content", type=str, help="the content", presence="optional")
        def cmd(ctx, content):
            return 0

    with pytest.raises(ValueError) as exc:
        updated("r")
    assert str(exc.value) == f'command "cmd": update resource "r" {CLAUSE}'
    updated("dns-record")


def _checks(name, hook=None):
    text = (
        f'app = "myapp"\n\n[checks.{name}]\ndescription = "d"\nsubject = "quality"\n'
        'tags = ["fast-lane"]\nseverity = "error"\nfast = true\npure = true\n'
        "needs_network = false\ndepends_on = []\n"
    )
    if hook is not None:
        text += f'\n[hooks."{hook}"]\ntag = "fast-lane"\n'
    return text.encode()


@pytest.mark.parametrize("name", ["x", "lint-", "lint--code"])
def test_check_names_are_refused(name):
    with pytest.raises(ValueError) as exc:
        _parse_checks_toml_full(_checks(f'"{name}"'))
    assert str(exc.value) == f'checks.toml: invalid check name "{name}" ({CLAUSE})'
    _parse_checks_toml_full(_checks("lint-code"))


@pytest.mark.parametrize("hook", ["p", "pre-", "Pre-push"])
def test_hook_names_are_refused(hook):
    with pytest.raises(ValueError) as exc:
        _parse_checks_toml_full(_checks("lint-code", hook))
    assert str(exc.value) == f'checks.toml: invalid hook name "{hook}" ({CLAUSE})'
    _parse_checks_toml_full(_checks("lint-code", "pre-push"))


@pytest.mark.parametrize("name", ["help", "version"])
def test_framework_command_names_are_reserved_at_every_level(name):
    reserved = (
        "is reserved: help and version are framework commands at every level "
        "of the command tree"
    )
    with pytest.raises(ValueError) as exc:
        _command(_app(), name)
    assert str(exc.value) == f'command name "{name}" {reserved}'
    with pytest.raises(ValueError) as exc:
        _command(_app().group("grp", help="g").group("sub", help="s"), name)
    assert str(exc.value) == f'command name "{name}" {reserved}'
    with pytest.raises(ValueError) as exc:
        _app().group(name, help="g")
    assert str(exc.value) == f'group name "{name}" {reserved}'
    with pytest.raises(ValueError) as exc:
        _app().group("grp", help="g").group(name, help="g")
    assert str(exc.value) == f'group name "{name}" {reserved}'
    with pytest.raises(ValueError) as exc:
        _app().deprecate(name, message="gone")
    assert str(exc.value) == f'deprecated command name "{name}" {reserved}'
    with pytest.raises(ValueError) as exc:
        _app().group("grp", help="g").deprecate(name, message="gone")
    assert str(exc.value) == f'deprecated command name "{name}" {reserved}'
    # The fix the refusal names: any other name.
    app = _app()
    _command(app, "show-help")
    _command(app.group("versions", help="g"), "list")


@pytest.mark.parametrize("short", ["1", "ab", "é"])
def test_a_member_choice_short_is_one_ascii_letter(short):
    @choice("work", help="the work profile", short=short)
    class Work:
        pass

    @choice("home", help="the home profile")
    class Home:
        pass

    app = _app()
    with pytest.raises(ValueError) as exc:

        @app.command("run", help="run", effect="read_only")
        @choice_flag("profile", help="which profile", presence="required",
                     elect_by="member-flags", choices=[Work, Home])
        def run(ctx, profile: Work | Home):
            return 0

    assert str(exc.value) == (
        f'Flag "work": short form "{short}" must be one ASCII letter (a-z or A-Z)'
    )


def test_an_empty_short_is_no_short():
    strictcli.Flag(name="device", type=str, help="h", presence="required", short="")
