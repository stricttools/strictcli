"""Tests for command/group visibility: hidden, interactive, help filtering, schema."""

from __future__ import annotations

import json
import os

import pytest
import strictcli


def _make_app(**kwargs):
    """Create a minimal app for testing."""
    defaults = dict(name="testapp", help="A test app", version="1.0.0")
    defaults.update(kwargs)
    return strictcli.App(**defaults)


# ---------------------------------------------------------------------------
# Hidden command: not in help, still routable
# ---------------------------------------------------------------------------


class TestHiddenCommand:
    """Hidden commands are excluded from help text but remain callable."""

    def test_hidden_command_not_in_help(self):
        app = _make_app()

        @app.command("visible", effect="read_only", help="A visible command")
        def visible(ctx):
            pass

        @app.command("secret", effect="read_only", help="A secret command", hidden=True)
        def secret(ctx):
            pass

        result = app.test(["--help"])
        assert "visible" in result.stdout
        assert "secret" not in result.stdout

    def test_hidden_command_still_routable(self):
        app = _make_app()

        @app.command("secret", effect="read_only", help="A secret command", hidden=True)
        def secret(ctx):
            print("secret output")

        result = app.test(["secret"])
        assert result.stdout.strip() == "secret output"

    def test_hidden_command_help_still_works(self):
        """Requesting --help on a hidden command should still show its help."""
        app = _make_app()

        @app.command("secret", effect="read_only", help="A secret command", hidden=True)
        @strictcli.flag("loud", type=bool, default=False, help="be loud")
        def secret(ctx, loud):
            pass

        result = app.test(["secret", "--help"])
        assert "A secret command" in result.stdout

    def test_hidden_false_is_default(self):
        app = _make_app()

        @app.command("normal", effect="read_only", help="A normal command")
        def normal(ctx):
            pass

        cmd = app._commands["normal"]
        assert cmd.hidden is False

    def test_hidden_true_stored(self):
        app = _make_app()

        @app.command("secret", effect="read_only", help="A secret command", hidden=True)
        def secret(ctx):
            pass

        cmd = app._commands["secret"]
        assert cmd.hidden is True

    def test_all_hidden_commands_no_commands_section(self):
        """If all commands are hidden, the Commands section should not appear."""
        app = _make_app()

        @app.command("aa", effect="read_only", help="cmd a", hidden=True)
        def a(ctx):
            pass

        @app.command("bb", effect="read_only", help="cmd b", hidden=True)
        def b(ctx):
            pass

        result = app.test(["--help"])
        assert "Commands:" not in result.stdout


# ---------------------------------------------------------------------------
# Hidden group: not in help, commands within still routable
# ---------------------------------------------------------------------------


class TestHiddenGroup:
    """Hidden groups are excluded from help text but their commands remain callable."""

    def test_hidden_group_not_in_help(self):
        app = _make_app()
        visible_grp = app.group("public", help="Public group")
        hidden_grp = app.group("internal", help="Internal group", hidden=True)

        @visible_grp.command("cmd", effect="read_only", help="Public command")
        def pub_cmd(ctx):
            pass

        @hidden_grp.command("cmd", effect="read_only", help="Internal command")
        def int_cmd(ctx):
            pass

        result = app.test(["--help"])
        assert "public" in result.stdout
        assert "internal" not in result.stdout

    def test_hidden_group_commands_still_routable(self):
        app = _make_app()
        hidden_grp = app.group("internal", help="Internal group", hidden=True)

        @hidden_grp.command("run", effect="read_only", help="Run internal")
        def run(ctx):
            print("internal run")

        result = app.test(["internal", "run"])
        assert result.stdout.strip() == "internal run"

    def test_hidden_group_help_still_works(self):
        """Requesting --help on a hidden group should still show its help."""
        app = _make_app()
        hidden_grp = app.group("internal", help="Internal group", hidden=True)

        @hidden_grp.command("run", effect="read_only", help="Run internal")
        def run(ctx):
            pass

        result = app.test(["internal", "--help"])
        assert "Internal group" in result.stdout

    def test_hidden_subgroup_in_group_help(self):
        """Hidden subgroups within a group should not appear in the parent group's help."""
        app = _make_app()
        grp = app.group("parent", help="Parent group")
        visible_sub = grp.group("visible", help="Visible subgroup")
        hidden_sub = grp.group("hidden", help="Hidden subgroup", hidden=True)

        @visible_sub.command("aa", effect="read_only", help="cmd a")
        def a(ctx):
            pass

        @hidden_sub.command("bb", effect="read_only", help="cmd b")
        def b(ctx):
            pass

        result = app.test(["parent", "--help"])
        assert "visible" in result.stdout
        assert "hidden" not in result.stdout

    def test_hidden_command_in_group_help(self):
        """Hidden commands within a group should not appear in the group's help."""
        app = _make_app()
        grp = app.group("mygroup", help="My group")

        @grp.command("visible", effect="read_only", help="Visible command")
        def visible(ctx):
            pass

        @grp.command("secret", effect="read_only", help="Secret command", hidden=True)
        def secret(ctx):
            pass

        result = app.test(["mygroup", "--help"])
        assert "visible" in result.stdout
        assert "secret" not in result.stdout

    def test_all_hidden_groups_no_groups_section(self):
        """If all groups are hidden, the Groups section should not appear."""
        app = _make_app()
        app.group("aa", help="group a", hidden=True)
        app.group("bb", help="group b", hidden=True)

        result = app.test(["--help"])
        assert "Groups:" not in result.stdout


# ---------------------------------------------------------------------------
# Interactive command: appears in help, stored on Command
# ---------------------------------------------------------------------------


class TestInteractiveCommand:
    """Interactive commands appear in help text (only hidden from tool export)."""

    def test_interactive_command_in_help(self):
        app = _make_app()

        @app.command("edit", effect="read_only", help="Edit interactively", interactive=True)
        def edit(ctx):
            pass

        result = app.test(["--help"])
        assert "edit" in result.stdout

    def test_interactive_false_is_default(self):
        app = _make_app()

        @app.command("normal", effect="read_only", help="A normal command")
        def normal(ctx):
            pass

        cmd = app._commands["normal"]
        assert cmd.interactive is False

    def test_interactive_true_stored(self):
        app = _make_app()

        @app.command("edit", effect="read_only", help="Edit interactively", interactive=True)
        def edit(ctx):
            pass

        cmd = app._commands["edit"]
        assert cmd.interactive is True

    def test_interactive_and_hidden_combined(self):
        """A command can be both hidden and interactive."""
        app = _make_app()

        @app.command("secret-edit", effect="read_only", help="Secret edit", hidden=True, interactive=True)
        def secret_edit(ctx):
            print("secret edit output")

        cmd = app._commands["secret-edit"]
        assert cmd.hidden is True
        assert cmd.interactive is True

        # Hidden means not in help
        result = app.test(["--help"])
        assert "secret-edit" not in result.stdout

        # But still routable
        result = app.test(["secret-edit"])
        assert result.stdout.strip() == "secret edit output"


# ---------------------------------------------------------------------------
# config edit has interactive=True automatically
# ---------------------------------------------------------------------------


class TestConfigEditInteractive:
    """The auto-generated 'config edit' command has interactive=True."""

    def test_config_edit_is_interactive(self):
        app = _make_app(config=True)
        config_grp = app._groups["config"]
        edit_cmd = config_grp.commands["edit"]
        assert edit_cmd.interactive is True

    def test_config_other_commands_not_interactive(self):
        """Other config subcommands should not be interactive."""
        app = _make_app(config=True)
        config_grp = app._groups["config"]
        assert config_grp.commands["path"].interactive is False
        assert config_grp.commands["show"].interactive is False
        assert config_grp.commands["set"].interactive is False


# ---------------------------------------------------------------------------
# Schema includes hidden/interactive
# ---------------------------------------------------------------------------


class TestVisibilitySchema:
    """Schema serialization includes hidden and interactive fields."""

    def _load_schema(self, tmp_path, app):
        return json.loads(app.test(["help", "--json"]).stdout)

    def test_schema_hidden_command_emitted(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)

        app = _make_app()

        @app.command("secret", effect="read_only", help="A secret command", hidden=True)
        def secret(ctx):
            pass

        schema = self._load_schema(tmp_path, app)
        cmd_schema = schema["commands"]["secret"]
        assert cmd_schema["hidden"] is True
        assert "interactive" not in cmd_schema  # default omitted

    def test_schema_interactive_command_emitted(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)

        app = _make_app()

        @app.command("edit", effect="read_only", help="Edit stuff", interactive=True)
        def edit(ctx):
            pass

        schema = self._load_schema(tmp_path, app)
        cmd_schema = schema["commands"]["edit"]
        assert cmd_schema["interactive"] is True
        assert "hidden" not in cmd_schema  # default omitted

    def test_schema_normal_command_no_hidden_or_interactive(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)

        app = _make_app()

        @app.command("normal", effect="read_only", help="Normal command")
        def normal(ctx):
            pass

        schema = self._load_schema(tmp_path, app)
        cmd_schema = schema["commands"]["normal"]
        assert "hidden" not in cmd_schema
        assert "interactive" not in cmd_schema

    def test_schema_hidden_group_emitted(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)

        app = _make_app()
        grp = app.group("internal", help="Internal group", hidden=True)

        @grp.command("cmd", effect="read_only", help="A command")
        def cmd(ctx):
            pass

        schema = self._load_schema(tmp_path, app)
        grp_schema = schema["groups"]["internal"]
        assert grp_schema["hidden"] is True

    def test_schema_normal_group_no_hidden(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)

        app = _make_app()
        grp = app.group("public", help="Public group")

        @grp.command("cmd", effect="read_only", help="A command")
        def cmd(ctx):
            pass

        schema = self._load_schema(tmp_path, app)
        grp_schema = schema["groups"]["public"]
        assert "hidden" not in grp_schema

    def test_schema_defaults_include_visibility(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)

        app = _make_app()

        @app.command("xx", effect="read_only", help="x cmd")
        def x(ctx):
            pass

        schema = self._load_schema(tmp_path, app)
        defaults = schema["defaults"]
        assert defaults["command"]["hidden"] is False
        assert defaults["command"]["interactive"] is False
        assert defaults["group"]["hidden"] is False

    def test_schema_config_edit_interactive(self, tmp_path, monkeypatch):
        """config edit should appear as interactive in the schema."""
        monkeypatch.chdir(tmp_path)

        app = _make_app(config=True)

        schema = self._load_schema(tmp_path, app)
        config_grp = schema["groups"]["config"]
        edit_cmd = config_grp["commands"]["edit"]
        assert edit_cmd["interactive"] is True


# ---------------------------------------------------------------------------
# Group.command() with hidden/interactive
# ---------------------------------------------------------------------------


class TestGroupCommandVisibility:
    """Visibility flags work on group-level commands too."""

    def test_group_hidden_command_not_in_group_help(self):
        app = _make_app()
        grp = app.group("mygroup", help="My group")

        @grp.command("visible", effect="read_only", help="Visible command")
        def visible(ctx):
            pass

        @grp.command("hidden", effect="read_only", help="Hidden command", hidden=True)
        def hidden(ctx):
            pass

        result = app.test(["mygroup", "--help"])
        assert "visible" in result.stdout
        assert "hidden" not in result.stdout

    def test_group_hidden_command_still_routable(self):
        app = _make_app()
        grp = app.group("mygroup", help="My group")

        @grp.command("hidden", effect="read_only", help="Hidden command", hidden=True)
        def hidden(ctx):
            print("hidden output")

        result = app.test(["mygroup", "hidden"])
        assert result.stdout.strip() == "hidden output"

    def test_group_interactive_command_stored(self):
        app = _make_app()
        grp = app.group("mygroup", help="My group")

        @grp.command("edit", effect="read_only", help="Edit command", interactive=True)
        def edit(ctx):
            pass

        cmd = grp.commands["edit"]
        assert cmd.interactive is True
