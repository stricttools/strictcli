"""Member spelling: §21's election semantics, carried over by §24.4.

`MutexGroup` is subsumed and deleted (§21's amendment box, §24.14). What
survives it is member spelling: each choice is spelled as its own flag, no
selector token is ever typed, and the three §21.4 sentences are reused
BYTE-FOR-BYTE -- a migrated group changes no user-visible text. Every
assertion below that quotes a message is quoting the pre-round text.
"""

import pytest

import strictcli
from strictcli import choice, choice_flag, member_value, sub_flag


# ---------------------------------------------------------------------------
# The mixed group of §21.2: a payload-carrying member plus two payload-less
# ones (the old str + negatable bool + negatable bool group).
# ---------------------------------------------------------------------------


@choice("profile", help="a profile")
class Profile:
    value: str = member_value(help="the profile name")


@choice("all-profiles", help="every profile")
class AllProfiles:
    pass


@choice("current-profile", help="the current profile")
class CurrentProfile:
    pass


Mode = Profile | AllProfiles | CurrentProfile


def _election_app():
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("run", effect="read_only", help="run it")
    @choice_flag(
        "mode", help="which profiles", presence="required",
        elect_by="member-flags",
        choices=[Profile, AllProfiles, CurrentProfile],
    )
    def run(ctx, mode: Mode):
        match mode:
            case Profile(value=v):
                print(f"profile={v}")
            case AllProfiles():
                print("all")
            case CurrentProfile():
                print("current")

    return app


def test_a1_declined_member_elects_nothing():
    """A1: `--no-<x>` on a payload-less member elects nothing (§21.2)."""
    r = _election_app().test(["run", "--no-all-profiles"])
    assert r.exit_code == 1
    assert (
        "error: one of --profile, --all-profiles, --current-profile is required "
        "(--no-all-profiles declines an option; it does not choose one)\n"
    ) in r.stderr


def test_a1_payload_less_member_elects_on_presence():
    r = _election_app().test(["run", "--all-profiles"])
    assert r.exit_code == 0
    assert "all" in r.stdout


def test_a1_all_members_declined_is_unsatisfied():
    """A1/A3: the clause names the FIRST declined member, in declaration order."""
    r = _election_app().test(["run", "--no-current-profile", "--no-all-profiles"])
    assert r.exit_code == 1
    assert (
        "error: one of --profile, --all-profiles, --current-profile is required "
        "(--no-all-profiles declines an option; it does not choose one)\n"
    ) in r.stderr


def test_a2_payload_member_elects_on_empty_string():
    """A2: every other type elects on presence with any value, including ''."""
    r = _election_app().test(["run", "--profile", ""])
    assert r.exit_code == 0
    assert "profile=\n" in r.stdout


def test_a3_clause_absent_when_nothing_was_declined():
    r = _election_app().test(["run"])
    assert r.exit_code == 1
    assert (
        "error: one of --profile, --all-profiles, --current-profile is required\n"
    ) in r.stderr


def test_a4_redundant_negation_beside_an_election_is_an_error():
    r = _election_app().test(["run", "--profile", "work", "--no-all-profiles"])
    assert r.exit_code == 1
    assert (
        "error: --no-all-profiles cannot be combined with --profile "
        "(--no-all-profiles declines an option; it does not choose one)\n"
    ) in r.stderr


def test_a4_multiple_declined_members_are_all_named():
    r = _election_app().test(
        ["run", "--profile", "work", "--no-current-profile", "--no-all-profiles"],
    )
    assert r.exit_code == 1
    assert (
        "error: --no-all-profiles and --no-current-profile cannot be combined "
        "with --profile "
        "(--no-all-profiles declines an option; it does not choose one)\n"
    ) in r.stderr


def test_two_elections_are_still_mutually_exclusive():
    r = _election_app().test(["run", "--profile", "work", "--all-profiles"])
    assert r.exit_code == 1
    assert (
        "error: --profile and --all-profiles are mutually exclusive\n"
    ) in r.stderr


# ---------------------------------------------------------------------------
# CLI-only election (§21.3, carried over by §24.6)
# ---------------------------------------------------------------------------


def test_member_election_is_command_line_only_env(monkeypatch):
    """Env neither elects nor supplies a member: it is not consulted at all."""
    monkeypatch.setenv("MYAPP_PROFILE", "work")
    r = _election_app().test(["run"])
    assert r.exit_code == 1
    assert (
        "error: one of --profile, --all-profiles, --current-profile is required\n"
    ) in r.stderr


def test_member_election_is_command_line_only_config(tmp_path):
    cfg = tmp_path / "config.json"
    cfg.write_text('{"profile": "from-config"}\n')
    app = strictcli.App(
        name="myapp", version="1.0.0", help="test app", config=True,
    )

    @app.command("run", effect="read_only", help="run it")
    @choice_flag(
        "mode", help="which profiles", presence="required",
        elect_by="member-flags", choices=[Profile, AllProfiles],
    )
    def run(ctx, mode: Profile | AllProfiles):
        print(repr(mode))

    r = app.test(["--config", str(cfg), "run"])
    assert r.exit_code == 1
    assert "error: one of --profile, --all-profiles is required\n" in r.stderr


# ---------------------------------------------------------------------------
# What member spelling adds, and a mutex group could not express (§24.4)
# ---------------------------------------------------------------------------


@choice("work", help="the work profile")
class Work:
    value: str = member_value(help="the profile name")
    create_missing: bool = sub_flag(
        help="create the profile if it is absent", default=False,
    )


@choice("all", help="every profile")
class All:
    pass


def _scoped_member_app():
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("run", effect="read_only", help="run it")
    @choice_flag(
        "mode", help="which profiles", presence="required",
        elect_by="member-flags", choices=[Work, All],
    )
    def run(ctx, mode: Work | All):
        print(repr(mode))

    return app


def test_a_member_may_own_a_scope():
    r = _scoped_member_app().test(["run", "--work", "w", "--create-missing"])
    assert r.exit_code == 0
    assert "Work(value='w', create_missing=True)" in r.stdout


def test_a_scoped_flag_under_the_wrong_member_is_a_scope_error():
    """Where a mutex group could only leave the flag silently ignored."""
    r = _scoped_member_app().test(["run", "--all", "--create-missing"])
    assert r.exit_code == 1
    assert (
        "error: flag '--create-missing' is only valid under '--work', but "
        "'--all' was elected\n"
    ) in r.stderr


def test_the_member_spelled_scope_path_names_the_member_flag():
    """A member-spelled segment is `--<choice>` -- the only token typed."""
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @choice("advanced", help="the advanced mode")
    class Advanced:
        tuning: str = sub_flag(help="tuning profile", presence="required")

    @choice("simple", help="the simple mode")
    class Simple:
        pass

    @app.command("run", effect="read_only", help="run it")
    @choice_flag(
        "mode", help="the mode", presence="required",
        elect_by="member-flags", choices=[Advanced, Simple],
    )
    def run(ctx, mode: Advanced | Simple):
        return 0

    r = app.test(["run", "--advanced"])
    assert r.exit_code == 1
    assert "error: flag '--tuning' is required under '--advanced'\n" in r.stderr


# ---------------------------------------------------------------------------
# The programmatic front door (§24.11)
# ---------------------------------------------------------------------------


def test_call_takes_the_elected_record():
    app = _election_app()
    app.call("run", mode=AllProfiles())


def test_call_without_the_selector_is_the_required_error():
    app = _election_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        app.call("run")
    assert "one of --profile, --all-profiles, --current-profile is required" in str(
        exc.value
    )


# ---------------------------------------------------------------------------
# Registration (§12.13)
# ---------------------------------------------------------------------------


def test_a_member_spelled_selector_cannot_carry_a_short():
    with pytest.raises(ValueError) as exc:
        choice_flag(
            "mode", help="the mode", presence="required", short="m",
            elect_by="member-flags", choices=[AllProfiles, CurrentProfile],
        )
    assert str(exc.value) == (
        'Flag "mode": a member-spelled choice flag is never typed, so it '
        "cannot carry a short: declare the short on a member"
    )


def test_a_defaulted_member_selector_may_only_elect_a_payload_less_member():
    with pytest.raises(ValueError) as exc:
        choice_flag(
            "mode", help="the mode", default=Profile(value="work"),
            elect_by="member-flags",
            choices=[Profile, AllProfiles],
        )
    assert str(exc.value) == (
        'Flag "mode": default= elects choice "profile", whose flag carries a '
        "value nothing supplies: only a payload-less member can be a default"
    )


def test_a_defaulted_member_selector_electing_a_payload_less_member_is_legal():
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("run", effect="read_only", help="run it")
    @choice_flag(
        "mode", help="which profiles", default=AllProfiles(),
        elect_by="member-flags", choices=[Profile, AllProfiles],
    )
    def run(ctx, mode: Profile | AllProfiles):
        print(f"{mode!r} source={ctx.source('mode')}")

    r = app.test(["run"])
    assert r.exit_code == 0
    assert "AllProfiles() source=default" in r.stdout
    r = app.test(["run", "--profile", "work"])
    assert r.exit_code == 0
    assert "Profile(value='work') source=cli" in r.stdout


def test_a_token_spelled_choice_cannot_carry_a_payload():
    with pytest.raises(ValueError) as exc:
        choice_flag(
            "mode", help="the mode", presence="required",
            elect_by="selector-token", choices=[Profile, AllProfiles],
        )
    assert str(exc.value) == (
        'Choice "profile" of "mode": a token-spelled choice cannot carry a '
        "payload: the token names the choice, and a choice that carries its "
        "own value belongs to a member-spelled choice flag, declared with "
        'choice_flag(..., elect_by="member-flags")'
    )


def test_a_member_choice_name_inherits_every_flag_name_ban():
    """Under member spelling a choice name IS a flag name (§24.7)."""

    @choice("quiet", help="be quiet")
    class Quiet:
        pass

    with pytest.raises(ValueError) as exc:
        choice_flag(
            "mode", help="the mode", presence="required",
            elect_by="member-flags", choices=[Quiet, AllProfiles],
        )
    assert str(exc.value) == (
        "flag name 'quiet' is reserved by the framework "
        "(dry-run, approve-consequential, quiet, verbose)"
    )


# ---------------------------------------------------------------------------
# Help (§24.10)
# ---------------------------------------------------------------------------


def test_a_member_spelled_selector_renders_as_a_heading():
    """It has no token of its own, so its line carries the clause instead."""
    r = _scoped_member_app().test(["run", "--help"])
    assert r.exit_code == 0
    assert r.stdout == (
        "myapp run -- run it\n"
        "\n"
        "Flags:\n"
        "  mode                                         which profiles "
        "(exactly one of the following) [required]\n"
        "    --work <str>                               the work profile "
        "[required]\n"
        "      --create-missing, --no-create-missing    create the profile if "
        "it is absent [default: false]\n"
        "    --all                                      every profile "
        "[required]\n"
    )


# ---------------------------------------------------------------------------
# The flat machine form (§24.11, §21.4): a member's payload key IS an election
# ---------------------------------------------------------------------------


def _flat(app, **arguments):
    """Call `run` through the flat machine form the MCP boundary uses."""
    return app._call_with_kwargs(
        "run", dict(arguments), approve_consequential=False, flat=True,
    )


def test_a_payload_key_beside_another_members_election_is_a_double_election():
    """Supplying a member's payload key elects that member, so the pair is
    the SAME double election `--profile work --all-profiles` is, refused with
    §21.4's sentence in the parser's own bytes -- never silently discarded."""
    app = _election_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, mode="all-profiles", profile="work")
    assert str(exc.value) == "--profile and --all-profiles are mutually exclusive"


def test_the_double_election_sentence_is_the_clis_own_bytes():
    """The renderer is shared: the command line and the flat form produce one
    sentence, in declaration order, whatever order the keys arrived in."""
    app = _election_app()
    r = app.test(["run", "--profile", "work", "--all-profiles"])
    cli = r.stderr.split("\n")[0].removeprefix("error: ")
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, profile="work", mode="all-profiles")
    assert str(exc.value) == cli


def test_two_payload_keys_are_a_double_election_too():
    @choice("profile", help="one named profile")
    class OneProfile:
        value: str = member_value(help="the profile name")

    @choice("group", help="one named group")
    class OneGroup:
        value: str = member_value(help="the group name")

    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("run", effect="read_only", help="run it")
    @choice_flag(
        "mode", help="which profiles", presence="required",
        elect_by="member-flags", choices=[OneProfile, OneGroup],
    )
    def run(ctx, mode: OneProfile | OneGroup):
        print(repr(mode))

    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, profile="work", group="team")
    assert str(exc.value) == "--profile and --group are mutually exclusive"


def test_a_payload_key_alone_elects_its_member(capsys):
    """The flat form maps onto the command line: `--profile work` needs no
    second token naming the selector, and neither does `{profile: "work"}`."""
    _flat(_election_app(), profile="work")
    assert capsys.readouterr().out == "profile=work\n"


def test_a_payload_key_alone_opens_its_scope(capsys):
    """Electing by payload key opens that member's scope, so its own flags
    are in scope -- the flat reading of `--work w --create-missing`."""
    _flat(_scoped_member_app(), work="w", create_missing=True)
    assert capsys.readouterr().out == "Work(value='w', create_missing=True)\n"


def test_the_selector_key_and_its_own_payload_key_still_elect_once(capsys):
    """The canonical pair names one member twice: one election, not two."""
    _flat(_election_app(), mode="profile", profile="work")
    assert capsys.readouterr().out == "profile=work\n"


def test_a_payload_less_member_still_elects_through_the_selector_key(capsys):
    _flat(_election_app(), mode="all-profiles")
    assert capsys.readouterr().out == "all\n"


def test_a_selector_key_naming_no_declared_member_is_still_refused():
    app = _election_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, mode="nope")
    assert str(exc.value) == (
        "--mode: invalid value 'nope', must be one of: profile, "
        "all-profiles, current-profile"
    )


# ---------------------------------------------------------------------------
# A payload-less member's OWN key at the flat boundary (§24.11)
#
# The flat form maps onto the command line, so a payload-less member's property
# is the member's own token: true elects it exactly as `--<name>` does, and an
# explicit false DECLINES it exactly as `--no-<name>` does. Ignoring the key
# would make the flat form a second election vocabulary rather than the command
# line with its tokens removed.
# ---------------------------------------------------------------------------


def test_a_payload_less_members_own_key_elects_it(capsys):
    _flat(_election_app(), all_profiles=True)
    assert capsys.readouterr().out == "all\n"


def test_a_payload_less_members_own_key_elects_beside_the_selector_key(capsys):
    """One member named twice and consistently is ONE election."""
    _flat(_election_app(), mode="all-profiles", all_profiles=True)
    assert capsys.readouterr().out == "all\n"


def test_an_explicit_false_on_a_payload_less_member_declines_it():
    """`--no-<name>` declines: it names the member and states it is not the
    choice, so the selector is left unsatisfied and carries the clause."""
    app = _election_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, all_profiles=False)
    assert str(exc.value) == (
        "one of --profile, --all-profiles, --current-profile is required "
        "(--no-all-profiles declines an option; it does not choose one)"
    )


def test_the_decline_sentence_is_the_clis_own_bytes():
    r = _election_app().test(["run", "--no-all-profiles"])
    cli = r.stderr.split("\n")[0].removeprefix("error: ")
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(_election_app(), all_profiles=False)
    assert str(exc.value) == cli


def test_a_payload_less_members_key_beside_a_payload_key_is_a_double_election():
    app = _election_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, profile="work", all_profiles=True)
    assert str(exc.value) == "--profile and --all-profiles are mutually exclusive"


def test_a_decline_beside_an_election_is_the_redundant_negation_refusal():
    """§21.4's third error, reached through the flat form's own keys."""
    app = _election_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, profile="work", all_profiles=False)
    assert str(exc.value) == (
        "--no-all-profiles cannot be combined with --profile "
        "(--no-all-profiles declines an option; it does not choose one)"
    )


def test_the_selector_key_outranks_a_false_on_the_member_it_elects(capsys):
    """§18.23 item 236: no command line spells this contradiction -- a
    member-spelled selector has no token of its own -- so it exists only
    because the flat object has two ways to name one member and no order
    between them. The election stands and the decline is dropped, because a
    payload-less member publishes no property of its own (§18.21 item 231):
    the selector's property is the one thing the schema tells a caller to send
    for electing it, and what the schema publishes outranks what it does
    not."""
    _flat(_election_app(), mode="all-profiles", all_profiles=False)
    assert capsys.readouterr().out == "all\n"


# ---------------------------------------------------------------------------
# A member elected by the selector key must still carry its payload (§24.11)
#
# The member flag's own presence is `required`, read as required once this
# member is elected (§24.4). Electing `profile` and supplying no `profile`
# property is the flat reading of `--profile` with nothing after it.
# ---------------------------------------------------------------------------


def test_a_member_elected_by_the_selector_key_must_carry_its_payload():
    app = _election_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, mode="profile")
    assert str(exc.value) == "flag '--profile' requires a value"


def test_the_missing_payload_sentence_is_the_clis_own_bytes():
    r = _election_app().test(["run", "--profile"])
    cli = r.stderr.split("\n")[0].removeprefix("error: ")
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(_election_app(), mode="profile")
    assert str(exc.value) == cli


def test_a_missing_payload_is_refused_though_its_scope_is_complete():
    """The payload is the member's own value, not one of its scope's flags:
    a complete scope beside a missing payload is still a missing payload."""
    app = _scoped_member_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, mode="work", create_missing=True)
    assert str(exc.value) == "flag '--work' requires a value"


# ---------------------------------------------------------------------------
# Cross-selector staging (§24.3's `election -> scope -> value -> presence`)
#
# The phase order is a property of the parser, not of one selector: EVERY
# selector's election is resolved before any scope, value or presence problem
# is reported, so a double election on a later selector outranks an earlier
# selector's unsatisfied requirement and its missing payload alike.
# ---------------------------------------------------------------------------


@choice("one-tag", help="one named tag")
class OneTag:
    value: str = member_value(help="the tag name")
    strict: bool = sub_flag(help="fail on an unknown tag", default=False)


@choice("all-tags", help="every tag")
class AllTags:
    pass


def _two_selector_app():
    """`mode` is declared FIRST, `scope` second."""
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("run", effect="read_only", help="run it")
    @choice_flag(
        "mode", help="which profiles", presence="required",
        elect_by="member-flags", choices=[Profile, AllProfiles],
    )
    @choice_flag(
        "scope", help="which tags", presence="required",
        elect_by="member-flags", choices=[OneTag, AllTags],
    )
    def run(ctx, mode: Profile | AllProfiles, scope: OneTag | AllTags):
        print(f"{mode!r} {scope!r}")

    return app


def test_the_declaration_order_of_the_two_selector_app_is_mode_then_scope():
    assert [s.name for s in _two_selector_app()._commands["run"].selectors] == [
        "mode", "scope",
    ]


def test_a_later_double_election_outranks_an_earlier_missing_election():
    app = _two_selector_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, scope="all-tags", one_tag="t")
    assert str(exc.value) == "--one-tag and --all-tags are mutually exclusive"


def test_a_later_double_election_by_member_keys_outranks_an_earlier_one():
    """The double election is spelled with the members' own keys, which is
    the pair rule 3 makes reachable at this boundary at all."""
    app = _two_selector_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, one_tag="t", all_tags=True)
    assert str(exc.value) == "--one-tag and --all-tags are mutually exclusive"


def test_a_later_double_election_outranks_an_earlier_missing_payload():
    """The missing payload is a VALUE problem, so it waits for every
    selector's election -- including the one that turns out to be double."""
    app = _two_selector_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, mode="profile", scope="all-tags", one_tag="t")
    assert str(exc.value) == "--one-tag and --all-tags are mutually exclusive"


def _valued_scope_app():
    """`mode` is declared first and its `--work` member owns a checked flag."""

    @choice("work", help="the work profile")
    class WorkChecked:
        value: str = member_value(help="the profile name")
        level: str = sub_flag(
            help="the level", presence="required",
            choices=[strictcli.Choice("low"), strictcli.Choice("high")],
        )

    @choice("all", help="every profile")
    class AllChecked:
        pass

    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("run", effect="read_only", help="run it")
    @choice_flag(
        "mode", help="which profiles", presence="required",
        elect_by="member-flags", choices=[WorkChecked, AllChecked],
    )
    @choice_flag(
        "scope", help="which tags", presence="required",
        elect_by="member-flags", choices=[OneTag, AllTags],
    )
    def run(ctx, mode: WorkChecked | AllChecked, scope: OneTag | AllTags):
        print(f"{mode!r} {scope!r}")

    return app


def test_an_earlier_scopes_value_problem_is_reported_when_nothing_else_is():
    app = _valued_scope_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, work="w", level="sideways", scope="all-tags")
    assert str(exc.value) == (
        "--level: invalid value 'sideways', must be one of: low, high"
    )


def test_a_later_double_election_outranks_an_earlier_scopes_value_problem():
    app = _valued_scope_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, work="w", level="sideways", one_tag="t", all_tags=True)
    assert str(exc.value) == "--one-tag and --all-tags are mutually exclusive"


def test_a_later_scope_violation_outranks_an_earlier_missing_payload():
    """Scope precedes value in the same way, across selectors."""
    app = _two_selector_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        _flat(app, mode="profile", scope="all-tags", strict=True)
    assert str(exc.value) == (
        "flag '--strict' is only valid under '--one-tag', but "
        "'--all-tags' was elected"
    )


# ---------------------------------------------------------------------------
# A member's short (§24.4, §24.12)
#
# The member flag is the only token member spelling puts on the command line,
# so its short is an ordinary flag short: claimed across every simultaneously
# live scope, typed as `-x`, and rendered beside the member on its help line.
# Which declaration carries it follows the member's shape -- a payload-carrying
# member's electing flag IS its `member_value(...)`, and a payload-less one has
# no such field, so it declares the short on `@choice(...)`.
# ---------------------------------------------------------------------------


def _short_app():
    @choice("role", help="one role")
    class Role:
        value: str = member_value(help="the role name", short="r")

    @choice("cont", help="continue the previous session", short="c")
    class Cont:
        pass

    @choice("plain", help="a plain session", short="p")
    class Plain:
        pass

    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("launch", effect="read_only", help="launch it")
    @choice_flag(
        "start", help="how to start", presence="required",
        elect_by="member-flags", choices=[Role, Cont, Plain],
    )
    def launch(ctx, start: Role | Cont | Plain):
        match start:
            case Role(value=v):
                print(f"role={v}")
            case Cont():
                print("cont")
            case Plain():
                print("plain")

    return app


def test_a_payload_carrying_member_elects_by_its_short():
    r = _short_app().test(["launch", "-r", "admin"])
    assert r.exit_code == 0
    assert r.stdout == "role=admin\n"


def test_a_payload_carrying_members_short_consumes_the_next_token():
    """`-r X` is the member's own value, exactly as `--role X` is."""
    r = _short_app().test(["launch", "-r", "--plain"])
    assert r.exit_code == 0
    assert r.stdout == "role=--plain\n"


def test_a_payload_less_member_elects_by_its_short():
    r = _short_app().test(["launch", "-c"])
    assert r.exit_code == 0
    assert r.stdout == "cont\n"


def test_a_member_short_is_refused_a_value():
    r = _short_app().test(["launch", "-c", "extra"])
    assert r.exit_code == 1
    assert "unexpected argument 'extra'" in r.stderr


def test_a_member_with_a_short_is_still_declined_by_the_long_negation():
    """The short elects; the decline keeps its one spelling (§21.2)."""
    r = _short_app().test(["launch", "--no-cont"])
    assert r.exit_code == 1
    assert (
        "error: one of --role, --cont, --plain is required "
        "(--no-cont declines an option; it does not choose one)\n"
    ) in r.stderr


def test_a_decline_beside_a_short_election_names_the_members_long_form():
    """§21's A4 error, reached through a short: the message never says `-p`."""
    r = _short_app().test(["launch", "--no-cont", "-p"])
    assert r.exit_code == 1
    assert (
        "error: --no-cont cannot be combined with --plain "
        "(--no-cont declines an option; it does not choose one)\n"
    ) in r.stderr


def test_two_members_elected_by_short_are_mutually_exclusive():
    r = _short_app().test(["launch", "-c", "-p"])
    assert r.exit_code == 1
    assert "error: --cont and --plain are mutually exclusive\n" in r.stderr


def test_a_member_short_renders_beside_the_member_in_help():
    r = _short_app().test(["launch", "--help"])
    assert r.exit_code == 0
    assert "--role, -r <str>" in r.stdout
    assert "--cont, -c" in r.stdout
    assert "--plain, -p" in r.stdout


def test_a_member_short_is_claimed_against_a_command_flag():
    @choice("role", help="one role")
    class Role:
        value: str = member_value(help="the role name", short="r")

    @choice("plain", help="a plain session")
    class Plain:
        pass

    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    with pytest.raises(ValueError) as exc:

        @app.command("launch", effect="read_only", help="launch it")
        @strictcli.flag("repo", help="the repo", short="r", presence="optional")
        @choice_flag(
            "start", help="how to start", presence="required",
            elect_by="member-flags", choices=[Role, Plain],
        )
        def launch(ctx, start: Role | Plain, repo: str):
            pass

    assert str(exc.value) == (
        'command "launch": short \'-r\' is claimed by \'--role\' and '
        "'--repo', which can be elected at the same time"
    )


def test_two_sibling_members_may_not_share_a_short():
    """An election token is read before any election has happened (§24.7)."""

    @choice("cont", help="continue", short="c")
    class Cont:
        pass

    @choice("clean", help="start clean", short="c")
    class Clean:
        pass

    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    with pytest.raises(ValueError) as exc:

        @app.command("launch", effect="read_only", help="launch it")
        @choice_flag(
            "start", help="how to start", presence="required",
            elect_by="member-flags", choices=[Cont, Clean],
        )
        def launch(ctx, start: Cont | Clean):
            pass

    assert str(exc.value) == (
        'command "launch": short \'-c\' is reused by sibling scopes and also '
        "claimed by '--cont', which elects: an election token is read before "
        "any election has happened, so its short cannot be shared"
    )


def test_a_token_spelled_choice_cannot_carry_a_short():
    @choice("email", help="by email", short="e")
    class Email:
        pass

    @choice("sms", help="by sms")
    class Sms:
        pass

    with pytest.raises(ValueError) as exc:
        choice_flag(
            "via", help="delivery channel", presence="required",
            elect_by="selector-token", choices=[Email, Sms],
        )
    assert str(exc.value) == (
        'Choice "email" of "via": a token-spelled choice cannot carry a '
        "short: the token names the choice, and only a member-spelled choice "
        "has a flag of its own to carry one"
    )


def test_a_payload_carrying_member_declares_its_short_on_the_payload():
    @choice("role", help="one role", short="r")
    class Role:
        value: str = member_value(help="the role name")

    @choice("plain", help="a plain session")
    class Plain:
        pass

    with pytest.raises(ValueError) as exc:
        choice_flag(
            "start", help="how to start", presence="required",
            elect_by="member-flags", choices=[Role, Plain],
        )
    assert str(exc.value) == (
        'Choice "role" of "start": a payload-carrying member declares its '
        "short on its payload: member_value(short=...)"
    )


def test_a_member_short_is_published_on_the_payload_entry(tmp_path, monkeypatch):
    """§25.6's `value` entry is an ordinary flag entry, short included."""
    import json

    r = _short_app().test(["help", "--json"])
    assert r.exit_code == 0
    dumped = json.loads(r.stdout)
    sel = dumped["commands"]["launch"]["flags"][0]
    role = sel["choices"][0]
    assert role["name"] == "role"
    assert role["flags"][0]["name"] == "value"
    assert role["flags"][0]["short"] == "r"


def test_a_short_with_nothing_after_it_is_named_as_it_was_typed():
    """The refusal quotes the TOKEN, not the long form it resolved to.

    The root-scope path has always reported the token as typed; the scoped one
    reported the long name, which made a `-r` at the end of argv produce a
    message about `--role` in this implementation alone.
    """
    r = _short_app().test(["launch", "-r"])
    assert r.exit_code == 1
    assert "error: flag '-r' requires a value\n" in r.stderr


def test_a_long_member_token_with_nothing_after_it_is_named_in_full():
    r = _short_app().test(["launch", "--role"])
    assert r.exit_code == 1
    assert "error: flag '--role' requires a value\n" in r.stderr
