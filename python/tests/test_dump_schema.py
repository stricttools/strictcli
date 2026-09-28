"""Tests for the help document (`help --json`) and schema serialization."""

from __future__ import annotations

import json

import pytest
import strictcli


@pytest.fixture(autouse=True)
def _pyproject_in_tmp(tmp_path):
    """Ensure every test that uses tmp_path has a pyproject.toml for project_id."""


def _make_app(**kwargs):
    """Create a minimal app for testing."""
    defaults = dict(name="testapp", help="A test app", version="1.0.0")
    defaults.update(kwargs)
    return strictcli.App(**defaults)


class TestSchemaContent:
    """Schema contains correct app name, version, help."""

    def test_app_metadata(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app(name="myapp", version="2.3.4", help="My great app")

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert data["name"] == "myapp"
        assert data["version"] == "2.3.4"
        assert data["help"] == "My great app"

    def test_env_prefix_omitted_when_none(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert "env_prefix" not in data

    def test_env_prefix_when_set(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app(env_prefix="MYAPP")

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert data["env_prefix"] == "MYAPP"

    def test_config_omitted_when_false(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app_no_config = _make_app(config=False)

        @app_no_config.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app_no_config.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert "config" not in data


class TestSchemaCommands:
    """Schema contains commands with their flags and args."""

    def test_command_with_flags(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("deploy", effect="read_only", help="Deploy the app")
        @strictcli.flag("target", type=str, help="Deploy target", short="t",
                        choices=[strictcli.Choice("prod"), strictcli.Choice("staging")], presence="required")
        @strictcli.flag("force-deploy", type=bool, default=False, help="Force deploy")
        def deploy(ctx, target, force_deploy):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert "deploy" in data["commands"]
        cmd = data["commands"]["deploy"]
        assert cmd["name"] == "deploy"
        assert cmd["help"] == "Deploy the app"
        assert len(cmd["flags"]) == 2

        # Check flag serialization
        target_flag = cmd["flags"][0]
        assert target_flag["name"] == "target"
        assert target_flag["value_schema"] == {
            "type": "string", "enum": ["prod", "staging"],
        }
        assert target_flag["short"] == "t"
        assert target_flag["choices"] == [
            {"value": "prod"}, {"value": "staging"},
        ]
        assert "hidden" not in target_flag  # hidden=False is the default, omitted

        force_flag = cmd["flags"][1]
        assert force_flag["name"] == "force-deploy"
        assert force_flag["value_schema"] == {"type": "boolean"}
        assert force_flag["negatable"] is True
        assert force_flag["default"] is False

    def test_command_with_args(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("greet", effect="read_only", help="Greet someone",
                     args=[strictcli.Arg(name="name", help="Who to greet", presence="required")])
        def greet(ctx, name):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        cmd = data["commands"]["greet"]
        assert len(cmd["args"]) == 1
        arg = cmd["args"][0]
        assert arg["name"] == "name"
        assert arg["help"] == "Who to greet"
        assert "required" not in arg  # required=True is the default, omitted
        assert "variadic" not in arg  # variadic=False is the default, omitted

    def test_passthrough_command(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("run", effect="read_only", help="Run a command",
                     passthrough=strictcli.Passthrough(
                         handler=lambda ctx, name, args, globals: 0))
        def run():
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        cmd = data["commands"]["run"]
        assert cmd["passthrough"] is True

    def test_non_passthrough_command(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("greet", effect="read_only", help="Say hello")
        def greet(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        cmd = data["commands"]["greet"]
        assert "passthrough" not in cmd  # passthrough=False is the default, omitted


class TestSchemaGroups:
    """Schema contains groups (including nested groups)."""

    def test_group_with_commands(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()
        dns = app.group("dns", help="DNS management")

        @dns.command("list", effect="read_only", help="List DNS records")
        def dns_list(ctx):
            pass

        @dns.command("add", effect="read_only", help="Add a DNS record")
        @strictcli.flag("type", type=str, help="Record type", presence="required")
        def dns_add(ctx, type):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert "dns" in data["groups"]
        grp = data["groups"]["dns"]
        assert grp["name"] == "dns"
        assert grp["help"] == "DNS management"
        assert "list" in grp["commands"]
        assert "add" in grp["commands"]
        assert grp["commands"]["add"]["flags"][0]["name"] == "type"

    def test_nested_groups(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()
        dns = app.group("dns", help="DNS management")
        zone = dns.group("zone", help="Zone management")

        @zone.command("list", effect="read_only", help="List zones")
        def zone_list(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert "dns" in data["groups"]
        assert "zone" in data["groups"]["dns"]["groups"]
        nested = data["groups"]["dns"]["groups"]["zone"]
        assert nested["name"] == "zone"
        assert nested["help"] == "Zone management"
        assert "list" in nested["commands"]

    def test_group_deprecated_commands(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()
        dns = app.group("dns", help="DNS management")

        @dns.command("list", effect="read_only", help="List DNS records")
        def dns_list(ctx):
            pass

        dns.deprecate("old-cmd", message="Use 'list' instead")

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        grp = data["groups"]["dns"]
        assert "old-cmd" in grp["deprecated"]
        assert grp["deprecated"]["old-cmd"] == "Use 'list' instead"


class TestSchemaGlobalFlags:
    """Schema contains global flags."""

    def test_global_flags_serialized(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app(
            flags=[
                strictcli.Flag(name="loud", type=bool, default=False, help="Verbose output", short="V"),
                strictcli.Flag(name="output", type=str, help="Output format",
                               default="text", choices=[strictcli.Choice("text"), strictcli.Choice("json")]),
            ]
        )

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx, loud, output):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert len(data["global_flags"]) == 2
        loud = data["global_flags"][0]
        assert loud["name"] == "loud"
        assert loud["value_schema"] == {"type": "boolean"}
        assert loud["short"] == "V"
        assert loud["negatable"] is True

        output = data["global_flags"][1]
        assert output["name"] == "output"
        assert output["value_schema"] == {
            "type": "string", "enum": ["text", "json"],
        }
        assert output["default"] == "text"
        assert output["choices"] == [{"value": "text"}, {"value": "json"}]
        assert "negatable" not in output  # non-bool flag, null is the default, omitted


class TestSchemaDeprecated:
    """Schema contains deprecated commands."""

    def test_deprecated_commands(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("new-cmd", effect="read_only", help="The new command")
        def new_cmd(ctx):
            pass

        app.deprecate("old-cmd", message="Use 'new-cmd' instead")

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert "old-cmd" in data["deprecated"]
        assert data["deprecated"]["old-cmd"] == "Use 'new-cmd' instead"


class TestSchemaEmptyApp:
    """App without commands still produces valid schema."""

    def test_empty_app(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()
        result = app.test(["help", "--json"])
        assert result.exit_code == 0
        data = json.loads(result.stdout)
        assert data["name"] == "testapp"
        assert "commands" not in data  # empty dict is the default, omitted
        assert "groups" not in data  # empty dict is the default, omitted
        assert "global_flags" not in data  # empty list is the default, omitted
        assert "deprecated" not in data  # empty dict is the default, omitted


class TestSchemaFlagTypes:
    """Schema correctly serializes all flag types."""

    def test_int_flag(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="A command")
        @strictcli.flag("count", type=int, help="How many", default=5)
        def cmd(ctx, count):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        flag = data["commands"]["cmd"]["flags"][0]
        assert flag["value_schema"] == {"type": "integer"}
        assert flag["default"] == 5

    def test_float_flag(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="A command")
        @strictcli.flag("ratio", type=float, help="The ratio", default=0.5)
        def cmd(ctx, ratio):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        flag = data["commands"]["cmd"]["flags"][0]
        assert flag["value_schema"] == {"type": "number"}
        assert flag["default"] == 0.5

    def test_repeatable_flag(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="A command")
        @strictcli.flag("tag", type=str, help="A tag", repeatable=True, unique=False, default=[])
        def cmd(ctx, tag):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        flag = data["commands"]["cmd"]["flags"][0]
        # `repeatable` is deleted: the fragment carries the arity (§25.3).
        assert "repeatable" not in flag
        assert flag["value_schema"] == {
            "type": "array", "items": {"type": "string"},
        }
        assert flag["default"] == []

    def test_env_on_flag(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app(env_prefix="MYAPP")

        @app.command("cmd", effect="read_only", help="A command")
        @strictcli.flag("token", type=str, help="Auth token", env="MYAPP_TOKEN", presence="required")
        def cmd(ctx, token):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        flag = data["commands"]["cmd"]["flags"][0]
        assert flag["env"] == "MYAPP_TOKEN"

    def test_bool_non_negatable(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="A command")
        @strictcli.flag("force-it", type=bool, default=False, help="Force it", negatable=False)
        def cmd(ctx, force_it):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        flag = data["commands"]["cmd"]["flags"][0]
        assert flag["negatable"] is False


class TestDumpSchemaWithOtherArgs:
    """--dump-schema is only detected in the pre-command region."""

    def test_dump_schema_after_command_is_unknown_flag(self, tmp_path, monkeypatch):
        """--dump-schema after a command name is NOT intercepted."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("greet", effect="read_only", help="Say hello")
        def greet(ctx):
            pass

        result = app.test(["greet", "--dump-schema"])
        assert result.exit_code == 1
        assert "unknown flag" in result.stderr

    def test_dump_schema_after_double_dash_is_not_intercepted(self, tmp_path, monkeypatch):
        """--dump-schema after -- is NOT intercepted."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("greet", effect="read_only", help="Say hello")
        def greet(ctx):
            pass

        result = app.test(["--", "--dump-schema"])
        # After --, --dump-schema is treated as a command name (unknown command error)
        assert result.exit_code == 1


class TestSchemaDefaults:
    """Schema includes a top-level 'defaults' key documenting what missing fields mean."""

    def test_defaults_key_present(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert "defaults" in data
        assert isinstance(data["defaults"], dict)

    def test_defaults_structure(self, tmp_path, monkeypatch):
        """The whole block, verbatim (contract §25.10).

        It is the machine-readable map of what an OMITTED key means, so it is
        pinned as one document rather than key by key: a phantom entry, or a
        baseline for a key whose emission is governed by another key, is the
        exact defect the v2 rewrite removed.
        """
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)

        assert data["defaults"] == {
            "schema_version": 2,
            "app": {
                "env_prefix": None, "config": False, "config_format": "json",
                "config_path": None, "config_conflict_mode": "cli-wins",
                "proc_observe_allowlist": [], "global_flags": [],
                "commands": {}, "groups": {}, "deprecated": {},
                "tag_contracts": {}, "checks": {}, "config_fields": {},
                "infra": {},
            },
            "flag": {
                "short": None, "env": None, "env_separator": None,
                "prefixed": True, "choices": None, "elect_by": None,
                "unique": False, "conflict_mode": None, "negatable": None,
                "nullable": False,
            },
            "arg": {"variadic": False, "choices": None},
            "choice": {"flags": []},
            "choice_record": {"help": None},
            "command": {
                "consequential": False, "dry_run_supported": True,
                "dry_run_unsupported_reason": None,
                "update_of": None, "write_mode": None, "payload_schema": None,
                "owns_stdout": False, "passthrough": False, "flags": [],
                "flag_sets": [], "args": [], "tags": [], "constraints": [],
                "hidden": False, "interactive": False, "config_fields": [],
                "grants": [], "forwarding": None,
            },
            "group": {
                "commands": {}, "groups": {}, "deprecated": {}, "tags": [],
                "hidden": False,
            },
            "config_field": {"default": None, "bound_commands": []},
            "check": {"scope": None},
            "infra": {"roots": [], "handshakes": [], "connections": []},
        }

    def test_the_deleted_baselines_are_gone(self, tmp_path, monkeypatch):
        """`flag.hidden` was a phantom (no implementation has a flag-level
        `hidden`); `flag.default` and `arg.default` state something false now
        that presence governs `default`'s emission; `flag.repeatable` and
        `arg.type` name keys that no longer exist."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        defaults = data["defaults"]
        assert "hidden" not in defaults["flag"]
        assert "default" not in defaults["flag"]
        assert "repeatable" not in defaults["flag"]
        assert "default" not in defaults["arg"]
        assert "type" not in defaults["arg"]
        assert "required" not in defaults["arg"]
        # `presence` and `value_schema` are always emitted, so neither has a
        # baseline to omit against.
        assert "presence" not in defaults["flag"]
        assert "presence" not in defaults["arg"]
        assert "value_schema" not in defaults["flag"]
        assert "value_schema" not in defaults["arg"]


class TestSchemaOmitsDefaults:
    """Fields matching their defaults are omitted from the schema output."""

    def test_flag_null_fields_omitted(self, tmp_path, monkeypatch):
        """A flag with all-default optional fields carries only its identity
        keys: name, help, value_schema and presence."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="A command")
        @strictcli.flag("name", type=str, help="A name", presence="required")
        def cmd(ctx, name):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        flag = data["commands"]["cmd"]["flags"][0]
        assert flag == {
            "name": "name",
            "help": "A name",
            "value_schema": {"type": "string"},
            "presence": "required",
        }

    def test_command_empty_flags_and_args_omitted(self, tmp_path, monkeypatch):
        """A command with no flags/args should omit those lists."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        cmd = data["commands"]["noop"]
        assert "flags" not in cmd
        assert "args" not in cmd
        assert "passthrough" not in cmd

    def test_group_empty_subgroups_and_deprecated_omitted(self, tmp_path, monkeypatch):
        """A group with no subgroups or deprecated commands omits those."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()
        grp = app.group("stuff", help="Stuff management")

        @grp.command("do", effect="read_only", help="Do stuff")
        def do_stuff(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        group_data = data["groups"]["stuff"]
        assert "commands" in group_data  # has commands, so present
        assert "groups" not in group_data  # empty, omitted
        assert "deprecated" not in group_data  # empty, omitted

    def test_arg_defaults_omitted(self, tmp_path, monkeypatch):
        """An arg with required=True and variadic=False omits both."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="A command",
                     args=[strictcli.Arg(name="target", help="The target", presence="required")])
        def cmd(ctx, target):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        arg = data["commands"]["cmd"]["args"][0]
        assert arg["name"] == "target"
        assert arg["help"] == "The target"
        assert "required" not in arg
        assert "variadic" not in arg


class TestSchemaNonDefaultValues:
    """Non-default values are present in the schema output."""

    def test_arg_optional_presence_present(self, tmp_path, monkeypatch):
        """The arg entry carries `presence`; the old `required` key is gone."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="A command",
                     args=[strictcli.Arg(name="target", help="The target",
                                         presence="optional")])
        def cmd(ctx, target=None):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        arg = data["commands"]["cmd"]["args"][0]
        assert arg["presence"] == "optional"
        assert "required" not in arg
        assert "default" not in arg

    def test_arg_variadic_true_present(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="A command",
                     args=[strictcli.Arg(name="files", help="Files to process",
                                         variadic=True, presence="required")])
        def cmd(ctx, files):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        arg = data["commands"]["cmd"]["args"][0]
        assert arg["variadic"] is True

    def test_passthrough_true_present(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("run", effect="read_only", help="Run a command",
                     passthrough=strictcli.Passthrough(
                         handler=lambda ctx, name, args, globals: 0))
        def run():
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        cmd = data["commands"]["run"]
        assert cmd["passthrough"] is True

    def test_flag_with_all_non_default_values(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="A command")
        @strictcli.flag("level", type=int, help="Level", short="l",
                        default=3, env="MY_LEVEL", choices=[strictcli.Choice(1), strictcli.Choice(2), strictcli.Choice(3)])
        def cmd(ctx, level):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        flag = data["commands"]["cmd"]["flags"][0]
        assert flag["short"] == "l"
        assert flag["default"] == 3
        assert flag["env"] == "MY_LEVEL"
        assert flag["value_schema"] == {"type": "integer", "enum": [1, 2, 3]}
        assert flag["choices"] == [{"value": 1}, {"value": 2}, {"value": 3}]

    def test_config_true_present(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app(config=True)

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert data["config"] is True


class TestSchemaVersion:
    """Schema includes schema_version field."""

    def test_schema_version_present(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert data["schema_version"] == 2

    def test_schema_version_is_first_key(self, tmp_path, monkeypatch):
        """schema_version should appear before other keys in the JSON."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        keys = list(data.keys())
        assert keys[0] == "schema_version"


class TestSchemaConstraints:
    """Schema serializes command constraints (§25.7's four `type` values:
    at_least_one, all_or_none, requires, implies)."""

    def test_no_constraints_omitted(self, tmp_path, monkeypatch):
        """Commands without constraints omit the constraints field."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert "constraints" not in data["commands"]["noop"]

    def test_the_mutex_constraint_entry_is_gone(self, tmp_path, monkeypatch):
        """`MutexGroup` is deleted, and so is its schema entry (§21's box)."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @strictcli.choice("as-json", help="JSON output")
        class AsJson:
            pass

        @strictcli.choice("text", help="Text output")
        class Text:
            pass

        @app.command("show", effect="read_only", help="Show data")
        @strictcli.choice_flag(
            "format", help="the output format", presence="required",
            elect_by="member-flags", choices=[AsJson, Text],
        )
        def show(ctx, format: AsJson | Text):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        cmd = data["commands"]["show"]
        assert "constraints" not in cmd
        # The selector is published NESTED, never flattened away, and it lives
        # in the command's ONE flags array: a selector IS a flag, and the
        # presence of `elect_by` is what tells a reader which shape it is
        # holding (§25.6).
        assert "selectors" not in cmd
        assert cmd["flags"] == [{
            "name": "format",
            "help": "the output format",
            "presence": "required",
            "choices": [
                {"name": "as-json", "help": "JSON output"},
                {"name": "text", "help": "Text output"},
            ],
            "elect_by": "member-flags",
        }]
        # A selector carries NO value_schema: its value is a variant, which
        # the closed four-keyword subset cannot express, and publishing a
        # wrong fragment would be worse than publishing none (§25.6).
        assert "value_schema" not in cmd["flags"][0]

    def test_all_or_none(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("deploy", effect="read_only", help="Deploy",
                     constraints=[strictcli.AllOrNone("host-port", [
                         strictcli.Member("host"), strictcli.Member("port"),
                     ])])
        @strictcli.flag("host", type=str, help="Hostname", presence="optional")
        @strictcli.flag("port", type=int, help="Port number", presence="optional")
        def deploy(ctx, host, port):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        cmd = data["commands"]["deploy"]
        assert cmd["constraints"] == [{
            "type": "all_or_none",
            "name": "host-port",
            "members": [
                {"kind": "flag", "name": "host", "when": "present"},
                {"kind": "flag", "name": "port", "when": "present"},
            ],
        }]

    def test_at_least_one_with_every_member_kind(self, tmp_path, monkeypatch):
        """The resolved `kind` is published, so a consumer never has to search
        the flag and arg lists; `when` is ALWAYS emitted on a flag or arg
        member and NEVER on a constraint member."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command(
            "purge", effect="read_only", help="Purge",
            args=[strictcli.Arg("targets", help="ids", variadic=True,
                                presence="optional")],
            constraints=[
                strictcli.AllOrNone("host-port", [
                    strictcli.Member("host"), strictcli.Member("port"),
                ]),
                strictcli.AtLeastOne("selection", [
                    strictcli.Member("targets", when="non_empty"),
                    strictcli.Member("all", when="true"),
                    strictcli.Member("host-port"),
                ]),
            ],
        )
        @strictcli.flag("host", type=str, help="Hostname", presence="optional")
        @strictcli.flag("port", type=int, help="Port number", presence="optional")
        @strictcli.flag("all", type=bool, default=False, help="Everything")
        def purge(ctx, targets, host, port, all):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        cmd = data["commands"]["purge"]
        assert cmd["constraints"][1] == {
            "type": "at_least_one",
            "name": "selection",
            "members": [
                {"kind": "arg", "name": "targets", "when": "non_empty"},
                {"kind": "flag", "name": "all", "when": "true"},
                {"kind": "constraint", "name": "host-port"},
            ],
        }

    def test_requires(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("deploy", effect="read_only", help="Deploy",
                     constraints=[strictcli.Requires(
                         "port-needs-host", flag="port", depends_on="host")])
        @strictcli.flag("host", type=str, help="Hostname", presence="required")
        @strictcli.flag("port", type=int, help="Port number", presence="required")
        def deploy(ctx, host, port):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        cmd = data["commands"]["deploy"]
        assert cmd["constraints"] == [{
            "type": "requires",
            "name": "port-needs-host",
            "flag": "port",
            "depends_on": "host",
        }]

    def test_implies(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("deploy", effect="read_only", help="Deploy",
                     constraints=[strictcli.Implies(
                         "force-implies-agree",
                         flag="force-deploy", implies="agree", value=True)])
        @strictcli.flag("force-deploy", type=bool, default=False, help="Force deploy")
        @strictcli.flag("agree", type=bool, default=False, help="Skip confirmation")
        def deploy(ctx, force_deploy, agree):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        cmd = data["commands"]["deploy"]
        assert cmd["constraints"] == [{
            "type": "implies",
            "name": "force-implies-agree",
            "flag": "force-deploy",
            "implies": "agree",
            "value": True,
        }]

    def test_multiple_constraints(self, tmp_path, monkeypatch):
        """Multiple constraint types on the same command, declaration order."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("deploy", effect="read_only", help="Deploy",
                     constraints=[
                         strictcli.AllOrNone("host-port", [
                             strictcli.Member("host"), strictcli.Member("port"),
                         ]),
                         strictcli.Requires(
                             "port-needs-host", flag="port", depends_on="host"),
                     ])
        @strictcli.flag("host", type=str, help="Hostname", presence="optional")
        @strictcli.flag("port", type=int, help="Port number", presence="optional")
        def deploy(ctx, host, port):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        cmd = data["commands"]["deploy"]
        types = [c["type"] for c in cmd["constraints"]]
        assert types == ["all_or_none", "requires"]

    def test_constraint_member_names_use_dashes(self, tmp_path, monkeypatch):
        """Member names are flag names (dashes), never param names."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("deploy", effect="read_only", help="Deploy",
                     constraints=[strictcli.AllOrNone("pair", [
                         strictcli.Member("sim-run", when="true"),
                         strictcli.Member("skip-confirm", when="true"),
                     ])])
        @strictcli.flag("sim-run", type=bool, default=False, help="Dry run")
        @strictcli.flag("skip-confirm", type=bool, default=False, help="Skip confirmation")
        def deploy(ctx, sim_run, skip_confirm):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        c = data["commands"]["deploy"]["constraints"][0]
        assert [m["name"] for m in c["members"]] == ["sim-run", "skip-confirm"]


class TestSchemaTagContracts:
    """Schema serializes tag contracts at app level."""

    def test_no_tag_contracts_omitted(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert "tag_contracts" not in data

    def test_tag_contracts_serialized(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()
        app.tag_contract("dangerous", requires_flag="force-deploy")

        @app.command("deploy", effect="read_only", help="Deploy", tags=["dangerous"])
        @strictcli.flag("force-deploy", type=bool, default=False, help="Force it")
        def deploy(ctx, force_deploy):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert "tag_contracts" in data
        assert data["tag_contracts"] == {"dangerous": "force-deploy"}

    def test_multiple_tag_contracts(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()
        app.tag_contract("dangerous", requires_flag="force-deploy")
        app.tag_contract("slow", requires_flag="timeout")

        @app.command("deploy", effect="read_only", help="Deploy", tags=["dangerous", "slow"])
        @strictcli.flag("force-deploy", type=bool, default=False, help="Force it")
        @strictcli.flag("timeout", type=int, help="Timeout", default=30)
        def deploy(ctx, force_deploy, timeout):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        assert data["tag_contracts"] == {
            "dangerous": "force-deploy",
            "slow": "timeout",
        }


class TestSchemaArgDefaults:
    """Schema serializes arg defaults when present."""

    def test_arg_default_omitted_when_missing(self, tmp_path, monkeypatch):
        """Required args with no default omit the default field."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="A command",
                     args=[strictcli.Arg(name="target", help="The target", presence="required")])
        def cmd(ctx, target):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        arg = data["commands"]["cmd"]["args"][0]
        assert "default" not in arg

    def test_arg_default_string(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="A command",
                     args=[strictcli.Arg(name="target", help="The target",
                                         default="localhost")])
        def cmd(ctx, target):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        arg = data["commands"]["cmd"]["args"][0]
        assert arg["default"] == "localhost"

    def test_arg_empty_string_default_is_emitted(self, tmp_path, monkeypatch):
        """`default` is emitted whenever presence is "default", "" included."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("cmd", effect="read_only", help="A command",
                     args=[strictcli.Arg(name="target", help="The target",
                                         default="")])
        def cmd(ctx, target):
            pass

        _dump_r = app.test(["help", "--json"])
        data = json.loads(_dump_r.stdout)
        arg = data["commands"]["cmd"]["args"][0]
        assert arg["presence"] == "default"
        assert arg["default"] == ""


class TestDumpSchemaDict:
    """App.dump_schema_dict() returns the CWD-free schema core dict."""

    def test_returns_version_without_project_id(self, tmp_path, monkeypatch):
        # Run from a directory with NO pyproject.toml to prove no CWD access.
        empty = tmp_path / "empty"
        empty.mkdir()
        monkeypatch.chdir(empty)
        app = _make_app()

        @app.command("greet", effect="read_only", help="Say hello")
        def greet(ctx):
            pass

        d = app.dump_schema_dict()
        assert d["schema_version"] == 2
        assert d["version"] == "1.0.0"
        assert d["name"] == "testapp"
        assert "project_id" not in d
        assert "commands" in d

    def test_no_pyproject_does_not_raise(self, tmp_path, monkeypatch):
        empty = tmp_path / "empty2"
        empty.mkdir()
        monkeypatch.chdir(empty)
        assert not (empty / "pyproject.toml").exists()
        app = _make_app()
        # Must not raise even though _read_project_id() would.
        d = app.dump_schema_dict()
        assert isinstance(d, dict)

    def test_equals_file_writer_minus_project_id(self, tmp_path, monkeypatch):
        # tmp_path has a pyproject.toml (autouse fixture), so the file writer works.
        monkeypatch.chdir(tmp_path)
        app = _make_app(env_prefix="TESTAPP")

        @app.command("greet", effect="read_only", help="Say hello")
        @strictcli.flag("loud", type=bool, help="be loud", default=False)
        def greet(ctx, loud):
            pass

        result = app.test(["help", "--json"])
        assert result.exit_code == 0
        written = json.loads(result.stdout)
        method = app.dump_schema_dict()

        # File-writer output minus project_id must equal the method output.
        written_minus = {k: v for k, v in written.items() if k != "project_id"}
        assert written_minus == method

        # Byte-identical by construction: serializing the method output equals
        # serializing the file output with the project_id key removed.
        assert json.dumps(method, indent=2) == json.dumps(written_minus, indent=2)


class TestSchemaMarkerDefault:
    """A RelativeToRoot marker default serializes machine-stably.

    Regression: previously the raw marker was assigned to d["default"] and
    json.dumps crashed with TypeError. The marker must serialize as
    {"relative_to_root": {"env_var": ..., "parts": [...]}} -- only the declared
    env var and path parts, never a resolved machine-specific path -- and
    identically across the Python and Go implementations.
    """

    def test_command_and_global_flag_round_trip(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        monkeypatch.delenv("MYAPP_HOME", raising=False)
        app = _make_app(
            infra_root={"MYAPP_HOME": "/var/lib/myapp"},
            flags=[
                strictcli.Flag(
                    name="global-db",
                    type=str,
                    help="global db path",
                    default=strictcli.RelativeToRoot("MYAPP_HOME", "global.sqlite"),
                )
            ],
        )

        @app.command("run", effect="read_only", help="run it")
        @strictcli.flag(
            "db",
            help="db path",
            default=strictcli.RelativeToRoot("MYAPP_HOME", "sub", "db.sqlite"),
        )
        def run(ctx, db):
            return 0

        # The full --dump-schema round-trip must not crash and must write the
        # machine-stable marker shape.
        result = app.test(["help", "--json"])
        assert result.exit_code == 0
        data = json.loads(result.stdout)

        want_global = {
            "relative_to_root": {"env_var": "MYAPP_HOME", "parts": ["global.sqlite"]}
        }
        assert data["global_flags"][0]["default"] == want_global

        cmd_flag = data["commands"]["run"]["flags"][0]
        want_cmd = {
            "relative_to_root": {
                "env_var": "MYAPP_HOME",
                "parts": ["sub", "db.sqlite"],
            }
        }
        assert cmd_flag["default"] == want_cmd

        # No resolved/machine-specific joined path leaks into the schema. The
        # infra roots section legitimately carries the declared default root,
        # but the marker must never emit the root joined with its parts.
        dumped = json.dumps(data)
        assert "/var/lib/myapp/global.sqlite" not in dumped
        assert "/var/lib/myapp/sub/db.sqlite" not in dumped


class TestByteCanon:
    """The dumped document is dumper-independent (contract §25.8).

    A repository whose schema file is written sometimes by one implementation
    and sometimes by another must see a diff exactly when something changed,
    so the encoding itself is pinned: escaping, layout, numbers and the
    trailing newline.
    """

    def _dump(self, tmp_path, app):
        return app.test(["help", "--json"]).stdout

    def test_the_whole_document_byte_for_byte(self, tmp_path, monkeypatch):
        """One small app, pinned as bytes: layout, key order and escaping in
        one assertion, because they are one encoding."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command(
            "greet", effect="read_only",
            help="Greet — with ünicode & <html> and a/slash",
        )
        @strictcli.flag("ratio", type=float, help="The ratio", default=1e-7)
        def greet(ctx, ratio):
            pass

        text = self._dump(tmp_path, app)
        tail = text[text.index('  "project_id"'):]
        assert tail == (
            '  "project_id": "strictcli",\n'
            '  "name": "testapp",\n'
            '  "version": "1.0.0",\n'
            '  "help": "A test app",\n'
            '  "commands": {\n'
            '    "greet": {\n'
            '      "name": "greet",\n'
            '      "help": "Greet — with ünicode & <html> and a/slash",\n'
            '      "effect": "read_only",\n'
            '      "flags": [\n'
            '        {\n'
            '          "name": "ratio",\n'
            '          "help": "The ratio",\n'
            '          "value_schema": {\n'
            '            "type": "number"\n'
            '          },\n'
            '          "presence": "default",\n'
            '          "default": 1e-7\n'
            '        }\n'
            '      ]\n'
            '    }\n'
            '  }\n'
            '}\n'
        )

    def test_non_ascii_is_raw_and_html_significant_characters_are_literal(
        self, tmp_path, monkeypatch,
    ):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="café <b> & </b> a/b")
        def noop(ctx):
            pass

        text = self._dump(tmp_path, app)
        assert "café <b> & </b> a/b" in text
        assert "\\u" not in text
        assert "\\/" not in text

    def test_a_control_character_uses_json_s_own_escape(
        self, tmp_path, monkeypatch,
    ):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="two\nlines\tapart")
        def noop(ctx):
            pass

        text = self._dump(tmp_path, app)
        assert '"help": "two\\nlines\\tapart"' in text

    def test_every_float_goes_through_the_canonical_float_form(
        self, tmp_path, monkeypatch,
    ):
        """Python's `repr` -- which `json.dumps` would use -- writes `1e-07`
        where SCF writes `1e-7`."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        @strictcli.flag("small", type=float, help="A small one", default=1e-7)
        @strictcli.flag("big", type=float, help="A big one", default=1e21)
        @strictcli.flag("whole", type=float, help="A whole one", default=2.0)
        def noop(ctx, small, big, whole):
            pass

        text = self._dump(tmp_path, app)
        assert '"default": 1e-7' in text
        assert '"default": 1e+21' in text
        assert '"default": 2.0' in text
        assert "1e-07" not in text

    def test_integers_are_bare_tokens(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        @strictcli.flag("count", type=int, help="How many", default=5)
        def noop(ctx, count):
            pass

        text = self._dump(tmp_path, app)
        assert '"default": 5\n' in text
        assert '"default": 5.0' not in text

    def test_the_layout_is_two_space_indent_one_member_per_line(
        self, tmp_path, monkeypatch,
    ):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        text = self._dump(tmp_path, app)
        assert text.startswith('{\n  "schema_version": 2,\n  "defaults": {\n')
        # Empty containers are inline, never split across lines.
        assert '"proc_observe_allowlist": [],' in text
        assert '"commands": {},' in text

    def test_exactly_one_trailing_newline(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        text = self._dump(tmp_path, app)
        assert text.endswith("}\n")
        assert not text.endswith("}\n\n")


class TestCanonicalKeyOrder:
    """Keys are emitted in a DECLARED order, never sorted at serialization
    time; keyed objects follow the two rules §25.9 pins."""

    def _dump(self, tmp_path, app):
        _dump_r = app.test(["help", "--json"])
        return json.loads(_dump_r.stdout)

    def test_the_top_level_order(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app(env_prefix="MYAPP", config=True, config_format="toml")
        app.config_field("db.url", type=str, help="Database URL")

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        app.deprecate("old", message="gone")
        data = self._dump(tmp_path, app)
        assert list(data) == [
            "schema_version", "defaults", "project_id", "name", "version",
            "help", "env_prefix", "config", "config_format", "commands",
            "groups", "deprecated", "config_fields",
        ]

    def test_the_flag_entry_order(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        @strictcli.flag(
            "level", type=int, help="Level", short="l", default=3,
            env="MY_LEVEL", prefixed=False, conflict_mode="error",
            choices=[strictcli.Choice(1), strictcli.Choice(3)],
        )
        def noop(ctx, level):
            pass

        data = self._dump(tmp_path, app)
        assert list(data["commands"]["noop"]["flags"][0]) == [
            "name", "help", "value_schema", "short", "presence", "default",
            "env", "prefixed", "choices", "conflict_mode",
        ]

    def test_the_arg_entry_order(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        @strictcli.arg(
            "scope", help="the scope", default="head",
            choices=[strictcli.Choice("head"), strictcli.Choice("tags")],
        )
        def noop(ctx, scope):
            pass

        data = self._dump(tmp_path, app)
        assert list(data["commands"]["noop"]["args"][0]) == [
            "name", "help", "value_schema", "presence", "default", "choices",
        ]

    def test_commands_keep_declaration_order(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        for name in ("zebra", "alpha", "middle"):
            @app.command(name, effect="read_only", help="Does nothing")
            def noop(ctx):
                pass

        data = self._dump(tmp_path, app)
        assert list(data["commands"]) == ["zebra", "alpha", "middle"]

    def test_deprecated_and_tag_contracts_are_sorted_by_key(
        self, tmp_path, monkeypatch,
    ):
        """Go retains no declaration order for either, and a canon no
        implementation can produce is not a canon."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command(
            "noop", effect="read_only", help="Does nothing", tags=["zz", "aa"],
        )
        @strictcli.flag("dry", type=bool, help="dry", default=False)
        def noop(ctx, dry):
            pass

        app.deprecate("zulu", message="gone")
        app.deprecate("alpha", message="gone")
        app.tag_contract("zz", requires_flag="dry")
        app.tag_contract("aa", requires_flag="dry")
        data = self._dump(tmp_path, app)
        assert list(data["deprecated"]) == ["alpha", "zulu"]
        assert list(data["tag_contracts"]) == ["aa", "zz"]


class TestBehavioralCompleteness:
    """The keys v1 was blind to (contract §25.11).

    Each is omitted at its baseline, so a departure from the framework's own
    behavior is exactly what makes a key appear.
    """

    def _dump(self, tmp_path, app):
        _dump_r = app.test(["help", "--json"])
        return json.loads(_dump_r.stdout)

    def test_the_config_keys_are_absent_at_their_baselines(
        self, tmp_path, monkeypatch,
    ):
        monkeypatch.chdir(tmp_path)
        app = _make_app(config=True)

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        data = self._dump(tmp_path, app)
        assert "config_format" not in data
        assert "config_path" not in data
        assert "config_conflict_mode" not in data

    def test_a_non_default_config_format_and_conflict_mode_appear(
        self, tmp_path, monkeypatch,
    ):
        monkeypatch.chdir(tmp_path)
        app = _make_app(
            config=True, config_format="toml", config_conflict_mode="error",
        )

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        data = self._dump(tmp_path, app)
        assert data["config_format"] == "toml"
        assert data["config_conflict_mode"] == "error"

    def test_a_literal_config_path_is_published_as_declared(
        self, tmp_path, monkeypatch,
    ):
        monkeypatch.chdir(tmp_path)
        app = _make_app(config=True, config_path="conf/app.json")

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        assert self._dump(tmp_path, app)["config_path"] == "conf/app.json"

    def test_a_relative_to_root_config_path_publishes_the_declaration(
        self, tmp_path, monkeypatch,
    ):
        """Never the resolution: a resolved absolute path is a property of the
        dumping machine, and a committed schema file must not carry one."""
        monkeypatch.chdir(tmp_path)
        monkeypatch.setenv("MYAPP_HOME", str(tmp_path / "home"))
        app = _make_app(
            config=True,
            infra_root={"MYAPP_HOME": "~/.myapp"},
            config_path=strictcli.RelativeToRoot("MYAPP_HOME", "conf", "app.json"),
        )

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        published = self._dump(tmp_path, app)["config_path"]
        assert published == {
            "relative_to_root": {
                "env_var": "MYAPP_HOME",
                "parts": ["conf", "app.json"],
            },
        }
        # The eagerly resolved path is still available to the runtime, and is
        # exactly what must NOT be published.
        assert app.config_path == str(tmp_path / "home" / "conf" / "app.json")

    def test_prefixed_is_omitted_when_true_and_emitted_when_false(
        self, tmp_path, monkeypatch,
    ):
        monkeypatch.chdir(tmp_path)
        app = _make_app(env_prefix="MYAPP")

        @app.command("noop", effect="read_only", help="Does nothing")
        @strictcli.flag("plain", type=str, help="Plain", presence="optional")
        @strictcli.flag(
            "bare", type=str, help="Bare", presence="optional", prefixed=False,
        )
        def noop(ctx, plain, bare):
            pass

        flags = self._dump(tmp_path, app)["commands"]["noop"]["flags"]
        assert "prefixed" not in flags[0]
        assert flags[1]["prefixed"] is False

    def test_flag_sets_record_the_grouping_v1_discarded(
        self, tmp_path, monkeypatch,
    ):
        monkeypatch.chdir(tmp_path)
        app = _make_app()
        common = strictcli.FlagSet(
            name="common",
            flags=[
                strictcli.Flag(
                    name="host", type=str, help="Hostname", presence="optional",
                ),
                strictcli.Flag(
                    name="port", type=int, help="Port", presence="optional",
                ),
            ],
        )

        @app.command(
            "serve", effect="read_only", help="Serve", flag_sets=[common],
        )
        def serve(ctx, host, port):
            pass

        cmd = self._dump(tmp_path, app)["commands"]["serve"]
        assert cmd["flag_sets"] == [{"name": "common", "flags": ["host", "port"]}]
        # The members keep their ordinary entries, so the key adds a grouping
        # without duplicating a declaration.
        assert [f["name"] for f in cmd["flags"]] == ["host", "port"]

    def test_flag_sets_is_absent_when_the_command_declares_none(
        self, tmp_path, monkeypatch,
    ):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        def noop(ctx):
            pass

        assert "flag_sets" not in self._dump(tmp_path, app)["commands"]["noop"]


class TestChoicesSiblingKey:
    """§25.5: the enum lives in the fragment, the records live beside it."""

    def _dump(self, tmp_path, app):
        _dump_r = app.test(["help", "--json"])
        return json.loads(_dump_r.stdout)

    def test_help_is_omitted_when_the_entry_declares_none(
        self, tmp_path, monkeypatch,
    ):
        """An absent help and Go's empty-string spelling of the same fact must
        not produce different bytes."""
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        @strictcli.flag(
            "scope", type=str, help="The scope", presence="required",
            choices=[
                strictcli.Choice("head", help="the current commit only"),
                strictcli.Choice("branches"),
            ],
        )
        def noop(ctx, scope):
            pass

        flag = self._dump(tmp_path, app)["commands"]["noop"]["flags"][0]
        assert flag["choices"] == [
            {"value": "head", "help": "the current commit only"},
            {"value": "branches"},
        ]
        assert flag["value_schema"] == {
            "type": "string", "enum": ["head", "branches"],
        }

    def test_a_value_keeps_its_own_type_and_is_never_stringified(
        self, tmp_path, monkeypatch,
    ):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        @strictcli.flag(
            "port", type=int, help="The port", presence="required",
            choices=[strictcli.Choice(80), strictcli.Choice(443)],
        )
        def noop(ctx, port):
            pass

        flag = self._dump(tmp_path, app)["commands"]["noop"]["flags"][0]
        assert flag["choices"] == [{"value": 80}, {"value": 443}]
        assert flag["value_schema"] == {"type": "integer", "enum": [80, 443]}

    def test_an_array_shaped_carriers_enum_lives_inside_items(
        self, tmp_path, monkeypatch,
    ):
        monkeypatch.chdir(tmp_path)
        app = _make_app()

        @app.command("noop", effect="read_only", help="Does nothing")
        @strictcli.flag(
            "tag", type=list[str], help="The tags", default=[], unique=False,
            choices=[strictcli.Choice("a"), strictcli.Choice("b")],
        )
        def noop(ctx, tag):
            pass

        flag = self._dump(tmp_path, app)["commands"]["noop"]["flags"][0]
        assert flag["value_schema"] == {
            "type": "array",
            "items": {"type": "string", "enum": ["a", "b"]},
        }
