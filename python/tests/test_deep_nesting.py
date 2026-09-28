"""Tests for recursive group nesting (arbitrary depth)."""

import pytest

import strictcli


def _make_3level_app():
    """Helper: app -> group -> subgroup -> command (3 levels)."""
    app = strictcli.App(name="nch", version="1.0.0", help="cloud tool")
    dns = app.group("dns", help="manage DNS")
    zone = dns.group("zone", help="manage DNS zones")

    @zone.command("list", effect="read_only", help="list all zones")
    def list_zones(ctx):
        print("listing zones")

    @zone.command("create", effect="read_only", help="create a zone")
    @strictcli.flag("name", type=str, help="zone name", presence="required")
    def create_zone(ctx, name):
        print(f"creating zone {name}")

    return app


def _make_4level_app():
    """Helper: app -> g1 -> g2 -> g3 -> command (4 levels)."""
    app = strictcli.App(name="deep", version="1.0.0", help="deeply nested app")
    g1 = app.group("level1", help="first level")
    g2 = g1.group("level2", help="second level")
    g3 = g2.group("level3", help="third level")

    @g3.command("action", effect="read_only", help="do the thing")
    def action(ctx):
        print("action executed")

    return app


# --- Dispatch tests ---


def test_3level_dispatch():
    """3-level nesting dispatches correctly."""
    app = _make_3level_app()
    r = app.test(["dns", "zone", "list"])
    assert r.exit_code == 0
    assert "listing zones" in r.stdout


def test_3level_dispatch_with_flags():
    """3-level nesting with command flags works."""
    app = _make_3level_app()
    r = app.test(["dns", "zone", "create", "--name", "example.com"])
    assert r.exit_code == 0
    assert "creating zone example.com" in r.stdout


def test_4level_dispatch():
    """4-level nesting dispatches correctly."""
    app = _make_4level_app()
    r = app.test(["level1", "level2", "level3", "action"])
    assert r.exit_code == 0
    assert "action executed" in r.stdout


# --- Help tests ---


def test_help_at_app_level():
    """App-level help shows top-level groups."""
    app = _make_3level_app()
    r = app.test(["--help"])
    assert r.exit_code == 0
    assert "Groups:" in r.stdout
    assert "dns" in r.stdout
    assert "manage DNS" in r.stdout


def test_help_at_group_level():
    """Group help shows subgroups."""
    app = _make_3level_app()
    r = app.test(["dns", "--help"])
    assert r.exit_code == 0
    assert "Groups:" in r.stdout
    assert "zone" in r.stdout
    assert "manage DNS zones" in r.stdout


def test_help_at_subgroup_level():
    """Subgroup help shows commands."""
    app = _make_3level_app()
    r = app.test(["dns", "zone", "--help"])
    assert r.exit_code == 0
    assert "Commands:" in r.stdout
    assert "list" in r.stdout
    assert "create" in r.stdout


def test_help_at_command_level():
    """Command help shows full path in header."""
    app = _make_3level_app()
    r = app.test(["dns", "zone", "create", "--help"])
    assert r.exit_code == 0
    assert "nch dns zone create" in r.stdout


def test_help_with_h_flag():
    """-h works at each level."""
    app = _make_3level_app()
    # Group level
    r = app.test(["dns", "-h"])
    assert r.exit_code == 0
    assert "nch dns" in r.stdout

    # Subgroup level
    r = app.test(["dns", "zone", "-h"])
    assert r.exit_code == 0
    assert "nch dns zone" in r.stdout


def test_group_help_shows_full_path():
    """Group help header and hint include the full group path."""
    app = _make_3level_app()
    r = app.test(["dns", "zone", "--help"])
    assert r.exit_code == 0
    assert "nch dns zone -- manage DNS zones" in r.stdout
    assert "Use 'nch help dns zone <command>'" in r.stdout


def test_4level_help_at_each_level():
    """Help works at every level of 4-deep nesting."""
    app = _make_4level_app()

    r = app.test(["level1", "--help"])
    assert r.exit_code == 0
    assert "level2" in r.stdout

    r = app.test(["level1", "level2", "--help"])
    assert r.exit_code == 0
    assert "level3" in r.stdout

    r = app.test(["level1", "level2", "level3", "--help"])
    assert r.exit_code == 0
    assert "action" in r.stdout

    r = app.test(["level1", "level2", "level3", "action", "--help"])
    assert r.exit_code == 0
    assert "deep level1 level2 level3 action" in r.stdout


# --- Error tests ---


def test_unknown_command_in_subgroup_shows_full_path():
    """Unknown command error includes the group path."""
    app = _make_3level_app()
    r = app.test(["dns", "zone", "delete"])
    assert r.exit_code == 1
    assert "unknown command 'delete' in 'dns zone'" in r.stderr


def test_unknown_command_in_4level_group():
    """Unknown command in deeply nested group shows full path."""
    app = _make_4level_app()
    r = app.test(["level1", "level2", "level3", "bogus"])
    assert r.exit_code == 1
    assert "unknown command 'bogus' in 'level1 level2 level3'" in r.stderr


def test_unknown_command_at_top_level():
    """Unknown command at top level has no path prefix."""
    app = _make_3level_app()
    r = app.test(["bogus"])
    assert r.exit_code == 1
    assert "unknown command 'bogus'" in r.stderr
    assert "in '" not in r.stderr


# --- Mixed groups and commands in same group ---


def test_mixed_groups_and_commands():
    """A group can have both commands and subgroups."""
    app = strictcli.App(name="mix", version="1.0.0", help="mixed app")
    grp = app.group("infra", help="infrastructure")

    @grp.command("status", effect="read_only", help="show status")
    def status(ctx):
        print("status ok")

    sub = grp.group("network", help="network management")

    @sub.command("list", effect="read_only", help="list networks")
    def list_nets(ctx):
        print("networks listed")

    # Command in group works
    r = app.test(["infra", "status"])
    assert r.exit_code == 0
    assert "status ok" in r.stdout

    # Subgroup command works
    r = app.test(["infra", "network", "list"])
    assert r.exit_code == 0
    assert "networks listed" in r.stdout

    # Help shows both
    r = app.test(["infra", "--help"])
    assert r.exit_code == 0
    assert "Commands:" in r.stdout
    assert "status" in r.stdout
    assert "Groups:" in r.stdout
    assert "network" in r.stdout


# --- Deprecated command in subgroup ---


def test_deprecated_command_in_subgroup():
    """Deprecated command in a nested subgroup shows deprecation message."""
    app = strictcli.App(name="nch", version="1.0.0", help="cloud tool")
    dns = app.group("dns", help="manage DNS")
    zone = dns.group("zone", help="manage zones")

    @zone.command("list", effect="read_only", help="list zones")
    def list_zones(ctx):
        print("listing")

    zone.deprecate("dump", message="use 'list' instead")

    r = app.test(["dns", "zone", "dump"])
    assert r.exit_code == 1
    assert "command 'dump' is deprecated: use 'list' instead" in r.stderr


def test_deprecated_shown_in_subgroup_help():
    """Deprecated section appears in subgroup help."""
    app = strictcli.App(name="nch", version="1.0.0", help="cloud tool")
    dns = app.group("dns", help="manage DNS")
    zone = dns.group("zone", help="manage zones")

    @zone.command("list", effect="read_only", help="list zones")
    def list_zones(ctx):
        print("listing")

    zone.deprecate("dump", message="use 'list' instead")

    r = app.test(["dns", "zone", "--help"])
    assert r.exit_code == 0
    assert "Deprecated:" in r.stdout
    assert "dump" in r.stdout


# --- Global flags with deep nesting ---


def test_global_flags_with_deep_nesting():
    """Global flags are parsed and passed through deep nesting."""
    app = strictcli.App(
        name="nch", version="1.0.0", help="cloud tool",
        flags=[strictcli.Flag(name="loud", type=bool, default=False, help="enable loud output")],
    )
    dns = app.group("dns", help="manage DNS")
    zone = dns.group("zone", help="manage zones")

    @zone.command("list", effect="read_only", help="list zones")
    def list_zones(ctx, loud):
        if loud:
            print("loud listing")
        else:
            print("normal listing")

    r = app.test(["--loud", "dns", "zone", "list"])
    assert r.exit_code == 0
    assert "loud listing" in r.stdout

    r = app.test(["dns", "zone", "list"])
    assert r.exit_code == 0
    assert "normal listing" in r.stdout


def test_global_flags_after_command_deep_nesting():
    """Global flags placed after the command work with deep nesting."""
    app = strictcli.App(
        name="nch", version="1.0.0", help="cloud tool",
        flags=[strictcli.Flag(name="loud", type=bool, default=False, help="enable loud output")],
    )
    dns = app.group("dns", help="manage DNS")
    zone = dns.group("zone", help="manage zones")

    @zone.command("list", effect="read_only", help="list zones")
    def list_zones(ctx, loud):
        if loud:
            print("loud listing")
        else:
            print("normal listing")

    r = app.test(["dns", "zone", "list", "--loud"])
    assert r.exit_code == 0
    assert "loud listing" in r.stdout


# --- Name collision validation ---


def test_name_collision_command_and_subgroup_raises():
    """Registering a command with the same name as a subgroup raises ValueError."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")
    grp = app.group("infra", help="infra group")
    grp.group("network", help="network subgroup")

    with pytest.raises(ValueError, match='command "network" collides with an existing group'):
        @grp.command("network", effect="read_only", help="this conflicts")
        def network(ctx):
            pass


def test_name_collision_subgroup_and_command_raises():
    """Registering a subgroup with the same name as a command raises ValueError."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")
    grp = app.group("infra", help="infra group")

    @grp.command("network", effect="read_only", help="a command")
    def network(ctx):
        pass

    with pytest.raises(ValueError, match='group "network" collides with an existing command'):
        grp.group("network", help="this conflicts")


def test_name_collision_deprecated_and_subgroup_raises():
    """Registering a deprecated command with the same name as a subgroup raises ValueError."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")
    grp = app.group("infra", help="infra group")
    grp.group("network", help="network subgroup")

    with pytest.raises(ValueError, match='collides with an existing group'):
        grp.deprecate("network", message="removed")


def test_duplicate_subgroup_raises():
    """Registering the same subgroup name twice raises ValueError."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")
    grp = app.group("infra", help="infra group")
    grp.group("network", help="first")

    with pytest.raises(ValueError, match='group "network" is already registered'):
        grp.group("network", help="second")
