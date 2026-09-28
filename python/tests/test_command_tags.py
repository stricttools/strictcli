"""Tests for command tags: frozen storage, validation, group inheritance, tag contracts, and schema output."""

from __future__ import annotations

import json
import os
from dataclasses import FrozenInstanceError

import pytest
import strictcli


def _make_app(**kwargs):
    """Create a minimal app for testing."""
    defaults = dict(name="testapp", help="A test app", version="1.0.0")
    defaults.update(kwargs)
    return strictcli.App(**defaults)


# ---------------------------------------------------------------------------
# 1. Frozen Command tests
# ---------------------------------------------------------------------------


class TestFrozenCommand:
    """Command dataclass is frozen; flags/args stored as tuples."""

    def test_command_flags_is_tuple(self):
        app = _make_app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("loud", type=bool, default=False, help="be loud")
        def cmd(ctx, loud):
            pass

        c = app._commands["cmd"]
        assert isinstance(c.flags, tuple)

    def test_command_args_is_tuple(self):
        app = _make_app()

        @app.command("cmd", effect="read_only", help="a command", args=[strictcli.Arg(name="name", help="a name", presence="required")])
        def cmd(ctx, name):
            pass

        c = app._commands["cmd"]
        assert isinstance(c.args, tuple)

    def test_command_frozen_assignment(self):
        app = _make_app()

        @app.command("cmd", effect="read_only", help="a command")
        def cmd(ctx):
            pass

        c = app._commands["cmd"]
        with pytest.raises(FrozenInstanceError):
            c.flags = ()


# ---------------------------------------------------------------------------
# 2. Tag storage and validation
# ---------------------------------------------------------------------------


class TestTagStorageAndValidation:
    """Tags are stored as frozenset; invalid names are rejected."""

    def test_command_tags_frozenset(self):
        app = _make_app()

        @app.command("cmd", effect="read_only", help="a command", tags={"json"})
        def cmd(ctx):
            pass

        c = app._commands["cmd"]
        assert c.tags == frozenset({"json"})

    def test_invalid_tag_uppercase(self):
        app = _make_app()
        with pytest.raises(ValueError, match="invalid tag name"):

            @app.command("cmd", effect="read_only", help="a command", tags={"JSON"})
            def cmd(ctx):
                pass

    def test_invalid_tag_underscore(self):
        app = _make_app()
        with pytest.raises(ValueError, match="invalid tag name"):

            @app.command("cmd", effect="read_only", help="a command", tags={"my_tag"})
            def cmd(ctx):
                pass

    def test_invalid_tag_starts_with_digit(self):
        app = _make_app()
        with pytest.raises(ValueError, match="invalid tag name"):

            @app.command("cmd", effect="read_only", help="a command", tags={"1abc"})
            def cmd(ctx):
                pass

    def test_invalid_tag_trailing_newline(self):
        """Trailing newline rejected via fullmatch (Python re.$ would accept it)."""
        app = _make_app()
        with pytest.raises(ValueError, match="invalid tag name"):

            @app.command("cmd", effect="read_only", help="a command", tags={"json\n"})
            def cmd(ctx):
                pass

    def test_invalid_tag_empty(self):
        app = _make_app()
        with pytest.raises(ValueError, match="invalid tag name"):

            @app.command("cmd", effect="read_only", help="a command", tags={""})
            def cmd(ctx):
                pass

    def test_invalid_tag_starts_with_dash(self):
        app = _make_app()
        with pytest.raises(ValueError, match="invalid tag name"):

            @app.command("cmd", effect="read_only", help="a command", tags={"-abc"})
            def cmd(ctx):
                pass

    def test_valid_tag_names(self):
        """Several valid tag names should register without error."""
        app = _make_app()

        @app.command("c1", effect="read_only", help="hh", tags={"json"})
        def c1(ctx):
            pass

        @app.command("c2", effect="read_only", help="hh", tags={"aa"})
        def c2(ctx):
            pass

        @app.command("c3", effect="read_only", help="hh", tags={"my-tag"})
        def c3(ctx):
            pass

        @app.command("c4", effect="read_only", help="hh", tags={"a1"})
        def c4(ctx):
            pass

        assert app._commands["c1"].tags == frozenset({"json"})
        assert app._commands["c2"].tags == frozenset({"aa"})
        assert app._commands["c3"].tags == frozenset({"my-tag"})
        assert app._commands["c4"].tags == frozenset({"a1"})


# ---------------------------------------------------------------------------
# 3. Group inheritance
# ---------------------------------------------------------------------------


class TestGroupTagInheritance:
    """Tags cascade from groups to commands and accumulate through nesting."""

    def test_group_tag_inherited(self):
        app = _make_app()
        grp = app.group("grp", help="a group", tags={"json"})

        @grp.command("cmd", effect="read_only", help="a command")
        def cmd(ctx):
            pass

        c = grp.commands["cmd"]
        assert c.tags == frozenset({"json"})

    def test_nested_group_tag_cascade(self):
        app = _make_app()
        parent = app.group("parent", help="parent group", tags={"aa"})
        child = parent.group("child", help="child group", tags={"bb"})

        @child.command("cmd", effect="read_only", help="a command")
        def cmd(ctx):
            pass

        c = child.commands["cmd"]
        assert "aa" in c.tags
        assert "bb" in c.tags

    def test_command_merges_own_and_group_tags(self):
        app = _make_app()
        grp = app.group("grp", help="a group", tags={"json"})

        @grp.command("cmd", effect="read_only", help="a command", tags={"loud"})
        def cmd(ctx):
            pass

        c = grp.commands["cmd"]
        assert c.tags == frozenset({"json", "loud"})

    def test_command_no_tags_under_tagged_group(self):
        app = _make_app()
        grp = app.group("grp", help="a group", tags={"json"})

        @grp.command("cmd", effect="read_only", help="a command")
        def cmd(ctx):
            pass

        c = grp.commands["cmd"]
        assert c.tags == frozenset({"json"})

    def test_group_tags_shows_own_only(self):
        """A child group's .tags is only its own tags, not accumulated from parents."""
        app = _make_app()
        parent = app.group("parent", help="parent group", tags={"admin"})
        child = parent.group("child", help="child group", tags={"json"})

        assert child.tags == frozenset({"json"})

    def test_sibling_groups_different_tags(self):
        app = _make_app()
        grp_a = app.group("alpha", help="group a", tags={"fast"})
        grp_b = app.group("beta", help="group b", tags={"slow"})

        @grp_a.command("cmd", effect="read_only", help="a command")
        def cmd_a(ctx):
            pass

        @grp_b.command("cmd", effect="read_only", help="a command")
        def cmd_b(ctx):
            pass

        assert grp_a.commands["cmd"].tags == frozenset({"fast"})
        assert grp_b.commands["cmd"].tags == frozenset({"slow"})


# ---------------------------------------------------------------------------
# 4. Tag contracts
# ---------------------------------------------------------------------------


class TestTagContracts:
    """tag_contract enforces that tagged commands have required flags."""

    def test_contract_satisfied(self):
        app = _make_app()
        app.tag_contract("json", requires_flag="as-json")

        @app.command("cmd", effect="read_only", help="a command", tags={"json"})
        @strictcli.flag("as-json", type=bool, default=False, help="output json")
        def cmd(ctx, as_json):
            pass

        result = app.test(["cmd"])
        assert result.exit_code == 0

    def test_contract_violated(self):
        app = _make_app()
        app.tag_contract("json", requires_flag="as-json")

        @app.command("foo", effect="read_only", help="a command", tags={"json"})
        def foo(ctx):
            pass

        result = app.test(["foo"])
        assert result.exit_code == 1
        assert "requires flag" in result.stderr

    def test_contract_error_message_exact(self):
        app = _make_app()
        app.tag_contract("json", requires_flag="as-json")

        @app.command("foo", effect="read_only", help="a command", tags={"json"})
        def foo(ctx):
            pass

        result = app.test(["foo"])
        assert result.exit_code == 1
        assert 'command "foo": tag "json" requires flag "--as-json"' in result.stderr

    def test_contract_on_inherited_tag(self):
        app = _make_app()
        app.tag_contract("json", requires_flag="as-json")
        grp = app.group("grp", help="a group", tags={"json"})

        @grp.command("cmd", effect="read_only", help="a command")
        def cmd(ctx):
            pass

        result = app.test(["grp", "cmd"])
        assert result.exit_code == 1
        assert 'requires flag "--as-json"' in result.stderr

    def test_contract_untagged_not_checked(self):
        app = _make_app()
        app.tag_contract("json", requires_flag="as-json")

        @app.command("cmd", effect="read_only", help="a command")
        def cmd(ctx):
            pass

        result = app.test(["cmd"])
        assert result.exit_code == 0

    def test_contract_passthrough_exempt(self):
        app = _make_app()
        app.tag_contract("json", requires_flag="as-json")
        grp = app.group("grp", help="a group", tags={"json"})

        @grp.command("run", effect="read_only", help="run something",
                     passthrough=strictcli.Passthrough(
                         handler=lambda ctx, name, args, globals: 0))
        def run():
            pass

        result = app.test(["grp", "run"])
        assert result.exit_code == 0

    def test_contract_ordering_independent(self):
        """Registering commands before calling tag_contract still catches violations."""
        app = _make_app()

        @app.command("foo", effect="read_only", help="a command", tags={"json"})
        def foo(ctx):
            pass

        app.tag_contract("json", requires_flag="as-json")

        result = app.test(["foo"])
        assert result.exit_code == 1
        assert 'requires flag "--as-json"' in result.stderr

    def test_contract_satisfied_by_global_flag(self):
        app = _make_app(
            flags=[strictcli.Flag(name="as-json", type=bool, default=False, help="output json")]
        )
        app.tag_contract("json", requires_flag="as-json")

        @app.command("cmd", effect="read_only", help="a command", tags={"json"})
        def cmd(ctx, as_json):
            pass

        result = app.test(["cmd"])
        assert result.exit_code == 0

    def test_contract_multiple(self):
        # Both contracts satisfied: should pass
        app_good = _make_app()
        app_good.tag_contract("json", requires_flag="as-json")
        app_good.tag_contract("loud", requires_flag="loud")

        @app_good.command("good", effect="read_only", help="has both flags", tags={"json", "loud"})
        @strictcli.flag("as-json", type=bool, default=False, help="output json")
        @strictcli.flag("loud", type=bool, default=False, help="be loud")
        def good(ctx, as_json, loud):
            pass

        result = app_good.test(["good"])
        assert result.exit_code == 0

        # One contract violated: should fail
        app_bad = _make_app()
        app_bad.tag_contract("json", requires_flag="as-json")
        app_bad.tag_contract("loud", requires_flag="loud")

        @app_bad.command("bad", effect="read_only", help="missing loud flag", tags={"json", "loud"})
        @strictcli.flag("as-json", type=bool, default=False, help="output json")
        def bad(ctx, as_json):
            pass

        result = app_bad.test(["bad"])
        assert result.exit_code == 1
        assert "requires flag" in result.stderr


# ---------------------------------------------------------------------------
# 5. Schema output
# ---------------------------------------------------------------------------


@pytest.fixture(autouse=True)
def _pyproject_in_tmp(tmp_path):
    """Ensure every test that uses tmp_path has a pyproject.toml for project_id."""


class TestSchemaTagOutput:
    """Tags appear correctly in --dump-schema output."""

    def test_schema_tagged_command(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="a command", tags={"beta", "admin"})
        def cmd(ctx):
            pass

        result = app.test(["help", "--json"])
        assert result.exit_code == 0
        data = json.loads(result.stdout)
        assert data["commands"]["cmd"]["tags"] == ["admin", "beta"]

    def test_schema_untagged_command(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="a command")
        def cmd(ctx):
            pass

        result = app.test(["help", "--json"])
        assert result.exit_code == 0
        data = json.loads(result.stdout)
        assert "tags" not in data["commands"]["cmd"]

    def test_schema_group_own_tags(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()
        grp = app.group("admin", help="admin group", tags={"admin"})

        @grp.command("cmd", effect="read_only", help="a command")
        def cmd(ctx):
            pass

        result = app.test(["help", "--json"])
        assert result.exit_code == 0
        data = json.loads(result.stdout)
        assert data["groups"]["admin"]["tags"] == ["admin"]

    def test_schema_defaults_include_tags(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="a command")
        def cmd(ctx):
            pass

        result = app.test(["help", "--json"])
        assert result.exit_code == 0
        data = json.loads(result.stdout)
        defaults = data["defaults"]
        assert defaults["command"]["tags"] == []
        assert defaults["group"]["tags"] == []
