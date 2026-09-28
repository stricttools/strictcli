"""The constraint system (contract §26): AtLeastOne, AllOrNone, Requires, Implies.

`CoRequired` is DELETED by rename (§26.1) -- all-or-none absorbs it, with no
alias and no deprecation period. Every constraint declares a mandatory name,
members are records naming flags, positional args or other named constraints,
and the election vocabulary is declared rather than dispatched on type.
"""

import pytest

import strictcli
from strictcli import AllOrNone, AtLeastOne, Implies, Member, Requires


# ---------------------------------------------------------------------------
# all-or-none: the predicate
# ---------------------------------------------------------------------------


def _all_or_none_app(**flag_kwargs):
    app = strictcli.App(name="test", version="1.0.0", help="test app")
    presence = flag_kwargs or {"presence": "optional"}

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AllOrNone("out-format", [Member("output"), Member("format")])],
    )
    @strictcli.flag("output", type=str, help="output path", **presence)
    @strictcli.flag("format", type=str, help="output format", **presence)
    def cmd(ctx, output, format):
        print(f"output={output} format={format}")

    return app


def test_all_or_none_both_provided_ok():
    r = _all_or_none_app().test(["cmd", "--output", "file.txt", "--format", "json"])
    assert r.exit_code == 0
    assert "output=file.txt" in r.stdout
    assert "format=json" in r.stdout


def test_all_or_none_neither_provided_is_vacuously_true():
    """Nothing engaged is the "none" half of its own name, not a loophole."""
    r = _all_or_none_app(default="").test(["cmd"])
    assert r.exit_code == 0


def test_all_or_none_one_provided_error():
    r = _all_or_none_app().test(["cmd", "--output", "file.txt"])
    assert r.exit_code == 1
    assert r.stderr.splitlines()[0] == (
        'error: constraint "out-format": --output, --format must be used together'
    )


def test_all_or_none_second_provided_without_first_error():
    r = _all_or_none_app().test(["cmd", "--format", "json"])
    assert r.exit_code == 1
    assert 'constraint "out-format": --output, --format must be used together' in r.stderr


def test_all_or_none_lists_every_member_engaged_or_not():
    """The shipped message's behaviour, carried over (§12.15)."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AllOrNone(
            "trio", [Member("aa"), Member("bb"), Member("cc")],
        )],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    @strictcli.flag("cc", type=str, help="cc", presence="optional")
    def cmd(ctx, aa, bb, cc):
        pass

    r = app.test(["cmd", "--bb", "xx"])
    assert 'constraint "trio": --aa, --bb, --cc must be used together' in r.stderr


def test_all_or_none_env_sets_one_cli_sets_another_ok(monkeypatch):
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AllOrNone("out-format", [Member("output"), Member("format")])],
    )
    @strictcli.flag("output", type=str, help="output path", presence="optional",
                    env="TEST_DEP_OUTPUT", prefixed=False)
    @strictcli.flag("format", type=str, help="output format", presence="optional",
                    env="TEST_DEP_FORMAT", prefixed=False)
    def cmd(ctx, output, format):
        print(f"output={output} format={format}")

    monkeypatch.setenv("TEST_DEP_OUTPUT", "env_file.txt")
    r = app.test(["cmd", "--format", "json"])
    assert r.exit_code == 0
    assert "output=env_file.txt" in r.stdout


def test_all_or_none_env_sets_one_not_other_error(monkeypatch):
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AllOrNone("out-format", [Member("output"), Member("format")])],
    )
    @strictcli.flag("output", type=str, help="output path", presence="optional",
                    env="TEST_DEP_OUTPUT2", prefixed=False)
    @strictcli.flag("format", type=str, help="output format", presence="optional",
                    env="TEST_DEP_FORMAT2", prefixed=False)
    def cmd(ctx, output, format):
        pass

    monkeypatch.setenv("TEST_DEP_OUTPUT2", "env_file.txt")
    r = app.test(["cmd"])
    assert r.exit_code == 1
    assert "must be used together" in r.stderr


def test_a_defaulted_member_never_engages_on_its_own():
    """A default is not provided (§23.6), so it never engages a constraint."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AllOrNone("pair", [Member("aa"), Member("bb")])],
    )
    @strictcli.flag("aa", type=str, help="aa", default="xx")
    @strictcli.flag("bb", type=str, help="bb", default="yy")
    def cmd(ctx, aa, bb):
        print("ran")

    assert app.test(["cmd"]).exit_code == 0
    assert app.test(["cmd", "--aa", "zz"]).exit_code == 1


# ---------------------------------------------------------------------------
# at-least-one: the predicate
# ---------------------------------------------------------------------------


def _at_least_one_app():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AtLeastOne("selection", [Member("aa"), Member("bb")])],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    def cmd(ctx, aa, bb):
        print(f"a={aa} b={bb}")

    return app


def test_at_least_one_satisfied_by_one():
    assert _at_least_one_app().test(["cmd", "--aa", "xx"]).exit_code == 0


def test_at_least_one_members_may_co_occur():
    """Engaging two satisfies it exactly as engaging one does -- it has no
    upper bound and is never exclusivity."""
    r = _at_least_one_app().test(["cmd", "--aa", "xx", "--bb", "yy"])
    assert r.exit_code == 0
    assert "a=xx b=yy" in r.stdout


def test_at_least_one_vacuous_is_violated():
    r = _at_least_one_app().test(["cmd"])
    assert r.exit_code == 1
    assert r.stderr.splitlines()[0] == (
        'error: constraint "selection": at least one of --aa, --bb is required'
    )


def test_at_least_one_decline_clause_names_the_first_declined_bool():
    """§21.4's clause verbatim, appended when a `when="true"` bool was
    provided false. It is about a NEGATED BOOL, not about exclusivity."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AtLeastOne("selection", [
            Member("aa", when="true"), Member("bb", when="true"),
        ])],
    )
    @strictcli.flag("aa", type=bool, help="aa", default=False)
    @strictcli.flag("bb", type=bool, help="bb", default=False)
    def cmd(ctx, aa, bb):
        pass

    r = app.test(["cmd", "--no-bb", "--no-aa"])
    assert r.stderr.splitlines()[0] == (
        'error: constraint "selection": at least one of --aa, --bb is required '
        "(--no-aa declines an option; it does not choose one)"
    )


def test_no_decline_clause_for_an_empty_non_empty_member():
    """A2 places empty-value legality on the flag's own validation, never on
    the layer above it, so there is deliberately no analogous clause."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AtLeastOne("selection", [
            Member("aa", when="non_empty"), Member("bb"),
        ])],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    def cmd(ctx, aa, bb):
        pass

    r = app.test(["cmd", "--aa", ""])
    assert r.stderr.splitlines()[0] == (
        'error: constraint "selection": at least one of --aa, --bb is required'
    )


# ---------------------------------------------------------------------------
# The `when` vocabulary (§26.3)
# ---------------------------------------------------------------------------


def test_when_true_counts_only_a_true_value():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AtLeastOne("selection", [
            Member("all", when="true"), Member("target"),
        ])],
    )
    @strictcli.flag("all", type=bool, help="all", default=False)
    @strictcli.flag("target", type=str, help="target", presence="optional")
    def cmd(ctx, all, target):
        print("ran")

    assert app.test(["cmd", "--all"]).exit_code == 0
    assert app.test(["cmd", "--no-all"]).exit_code == 1


def test_when_present_on_a_bool_counts_any_value():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AtLeastOne("selection", [
            Member("all", when="present"), Member("target"),
        ])],
    )
    @strictcli.flag("all", type=bool, help="all", default=False)
    @strictcli.flag("target", type=str, help="target", presence="optional")
    def cmd(ctx, all, target):
        print("ran")

    assert app.test(["cmd", "--no-all"]).exit_code == 0


def test_when_non_empty_on_a_string():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AtLeastOne("selection", [
            Member("aa", when="non_empty"), Member("bb"),
        ])],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    def cmd(ctx, aa, bb):
        print("ran")

    assert app.test(["cmd", "--aa", "xx"]).exit_code == 0
    assert app.test(["cmd", "--aa", ""]).exit_code == 1


def test_when_non_empty_on_a_repeatable_flag():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AtLeastOne("selection", [
            Member("tag", when="non_empty"), Member("bb"),
        ])],
    )
    @strictcli.flag("tag", type=list[str], help="tags", default=[])
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    def cmd(ctx, tag, bb):
        print("ran")

    assert app.test(["cmd", "--tag", "xx"]).exit_code == 0
    assert app.test(["cmd"]).exit_code == 1


def test_member_when_default_is_present():
    assert Member("xx").resolved_when == "present"
    assert Member("xx").when is None


def test_member_rejects_an_unknown_when():
    with pytest.raises(ValueError) as exc:
        Member("xx", when="ture")
    assert str(exc.value) == (
        'Member "xx": when must be "present", "true" or "non_empty", got '
        "'ture'"
    )


# ---------------------------------------------------------------------------
# Positional args as members, and the arg-side provided predicate (§26.3)
# ---------------------------------------------------------------------------


def _purge_app():
    app = strictcli.App(name="saferm", version="1.0.0", help="test app")

    @app.command(
        "purge", effect="read_only", help="destroy archived items",
        args=[strictcli.Arg(
            "targets", help="record ids", variadic=True, presence="optional",
        )],
        constraints=[AtLeastOne("purge-selection", [
            Member("targets", when="non_empty"),
            Member("older-than"),
            Member("all", when="true"),
        ])],
    )
    @strictcli.flag("older-than", type=str, help="age", presence="optional")
    @strictcli.flag("all", type=bool, help="all", default=False)
    def purge(ctx, targets, older_than, all):
        print(f"targets={targets} all={all}")

    return app


def test_a_variadic_arg_engages_when_a_token_was_supplied():
    app = _purge_app()
    assert app.test(["purge", "abc"]).exit_code == 0
    r = app.test(["purge"])
    assert r.exit_code == 1
    assert r.stderr.splitlines()[0] == (
        'error: constraint "purge-selection": at least one of targets, '
        "--older-than, --all is required"
    )


def test_an_arg_member_renders_bare_in_the_violation():
    r = _purge_app().test(["purge"])
    assert "targets," in r.stderr
    assert "--targets" not in r.stderr


def test_a_fixed_arg_engages_only_when_a_token_was_supplied():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        args=[strictcli.Arg("target", help="target", presence="optional")],
        constraints=[AtLeastOne("selection", [Member("target"), Member("bb")])],
    )
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    def cmd(ctx, target, bb):
        print("ran")

    assert app.test(["cmd", "xx"]).exit_code == 0
    assert app.test(["cmd"]).exit_code == 1


def test_an_arg_default_does_not_engage():
    """The declaration's default filled it, never the invocation."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        args=[strictcli.Arg("target", help="target", default="xx")],
        constraints=[AtLeastOne("selection", [Member("target"), Member("bb")])],
    )
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    def cmd(ctx, target, bb):
        print("ran")

    assert app.test(["cmd"]).exit_code == 1
    assert app.test(["cmd", "yy"]).exit_code == 0


def test_the_arg_predicate_answers_the_same_at_the_programmatic_door():
    """The same answer at the argv door and at the machine doors."""
    app = _purge_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        app.call("purge")
    assert 'constraint "purge-selection"' in str(exc.value)
    app.call("purge", targets=["aa"])


def test_an_explicitly_empty_array_is_not_a_provision():
    app = _purge_app()
    with pytest.raises(strictcli.InvokeError):
        app.call("purge", targets=[])


# ---------------------------------------------------------------------------
# Nesting: engaged, vacuous, satisfied (§26.4)
# ---------------------------------------------------------------------------


def _safegit_app():
    app = strictcli.App(name="safegit", version="1.0.0", help="test app")

    @app.command(
        "rewrite", effect="read_only", help="rewrite author identity",
        constraints=[
            AllOrNone("author-name", [Member("old-name"), Member("new-name")]),
            AllOrNone("author-email", [Member("old-email"), Member("new-email")]),
            AtLeastOne("author-change", [
                Member("author-name"), Member("author-email"),
            ]),
        ],
    )
    @strictcli.flag("old-name", type=str, help="old name", presence="optional")
    @strictcli.flag("new-name", type=str, help="new name", presence="optional")
    @strictcli.flag("old-email", type=str, help="old email", presence="optional")
    @strictcli.flag("new-email", type=str, help="new email", presence="optional")
    def rewrite(ctx, old_name, new_name, old_email, new_email):
        print("ran")

    return app


def test_two_vacuous_pairs_leave_the_parent_unsatisfied():
    """A nested member counts toward its parent ONLY when engaged -- which is
    precisely safegit's shipped hand guard, expressed."""
    r = _safegit_app().test(["rewrite"])
    assert r.exit_code == 1
    assert r.stderr.splitlines()[0] == (
        'error: constraint "author-change": at least one of '
        "(--old-name with --new-name), (--old-email with --new-email) is required"
    )


def test_children_are_evaluated_before_parents():
    """An operator who typed one half of a pair is told the pair is
    incomplete, not that the whole selection is missing."""
    r = _safegit_app().test(["rewrite", "--old-name", "aa"])
    assert r.stderr.splitlines()[0] == (
        'error: constraint "author-name": --old-name, --new-name must be used together'
    )


def test_a_complete_nested_pair_engages_the_parent():
    r = _safegit_app().test(["rewrite", "--old-email", "aa", "--new-email", "bb"])
    assert r.exit_code == 0


def test_engagement_propagates_upward_satisfaction_does_not():
    """An at-least-one nested inside another engages its parent as soon as one
    of its own members engages."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[
            AtLeastOne("inner", [Member("aa"), Member("bb")]),
            AtLeastOne("outer", [Member("inner"), Member("cc")]),
        ],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    @strictcli.flag("cc", type=str, help="cc", presence="optional")
    def cmd(ctx, aa, bb, cc):
        print("ran")

    assert app.test(["cmd", "--aa", "xx"]).exit_code == 0
    assert app.test(["cmd", "--cc", "xx"]).exit_code == 1  # inner is violated


# ---------------------------------------------------------------------------
# `Implies` runs before engagement (§26.4's pipeline position)
# ---------------------------------------------------------------------------


def test_an_implied_value_can_engage_a_member():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[
            Implies("fast-implies", flag="fast", implies="silent", value=True),
            AtLeastOne("selection", [Member("silent", when="true"), Member("bb")]),
        ],
    )
    @strictcli.flag("fast", type=bool, help="fast", default=False)
    @strictcli.flag("silent", type=bool, help="silent", default=False)
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    def cmd(ctx, fast, silent, bb):
        print("ran")

    assert app.test(["cmd", "--fast"]).exit_code == 0
    assert app.test(["cmd"]).exit_code == 1


# ---------------------------------------------------------------------------
# Registration: name legality (§26.8 step 1)
# ---------------------------------------------------------------------------


def _register(constraints, flags=(("aa", str), ("bb", str)), args=None):
    """Build a command with the given constraint set, deferred to a call."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    def build():
        def handler(ctx, **kwargs):
            pass

        for fname, ftype in reversed(flags):
            handler = strictcli.flag(
                fname, type=ftype, help=fname, presence="optional",
            )(handler)
        return app.command(
            "cmd", effect="read_only", help="a command",
            constraints=constraints, args=args,
            forwarding=strictcli.Forwarding(reason="test helper"),
        )(handler)

    return build


def test_constraint_name_charset():
    with pytest.raises(ValueError) as exc:
        _register([AllOrNone("Author_Name", [Member("aa"), Member("bb")])])()
    assert str(exc.value) == (
        'command "cmd": constraint name "Author_Name" must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*'
    )


def test_duplicate_constraint_name():
    with pytest.raises(ValueError) as exc:
        _register([
            AllOrNone("pair", [Member("aa"), Member("bb")]),
            AtLeastOne("pair", [Member("aa"), Member("bb")]),
        ])()
    assert str(exc.value) == 'command "cmd": duplicate constraint name "pair"'


def test_constraint_name_collides_with_a_flag_name():
    with pytest.raises(ValueError) as exc:
        _register([AllOrNone("aa", [Member("aa"), Member("bb")])])()
    assert str(exc.value) == (
        'command "cmd": constraint name "aa" is already a flag or arg name: a '
        "member reference resolves by name and would be ambiguous"
    )


def test_constraint_name_collides_with_an_arg_name():
    with pytest.raises(ValueError) as exc:
        _register(
            [AllOrNone("target", [Member("aa"), Member("bb")])],
            args=[strictcli.Arg("target", help="tt", presence="optional")],
        )()
    assert "is already a flag or arg name" in str(exc.value)


# ---------------------------------------------------------------------------
# Registration: member arity, records, resolution (§26.8 steps 2-3)
# ---------------------------------------------------------------------------


def test_min_members():
    with pytest.raises(ValueError) as exc:
        _register([AllOrNone("pair", [Member("aa")])])()
    assert str(exc.value) == (
        'command "cmd": constraint "pair" must declare at least two members, got 1'
    )


def test_a_bare_string_member_is_refused():
    """§24.2's rule for `choices=` entries, applied for its reason."""
    with pytest.raises(ValueError) as exc:
        _register([AllOrNone("pair", [Member("aa"), "bb"])])()
    assert str(exc.value) == (
        'command "cmd": constraint "pair" member 1 is a bare name: declare it '
        'as Member("<x>")'
    )


def test_the_bare_string_refusal_is_a_set_wide_step():
    """The member-record shape is checked across the WHOLE set between steps 1
    and 2, so a bare name in a LATER constraint is refused ahead of an unknown
    member in an earlier one: the fault is in a declaration's shape rather than
    in what it names."""
    with pytest.raises(ValueError) as exc:
        _register([
            AllOrNone("one", [Member("aa"), Member("nope")]),
            AllOrNone("two", [Member("aa"), "bb"]),
        ])()
    assert str(exc.value) == (
        'command "cmd": constraint "two" member 1 is a bare name: declare it '
        'as Member("<x>")'
    )


def test_the_bare_string_refusal_precedes_the_arity_step():
    """Between steps 1 and 2 means before arity too, wherever it is declared."""
    with pytest.raises(ValueError) as exc:
        _register([
            AllOrNone("one", [Member("aa")]),
            AllOrNone("two", [Member("aa"), "bb"]),
        ])()
    assert str(exc.value) == (
        'command "cmd": constraint "two" member 1 is a bare name: declare it '
        'as Member("<x>")'
    )


def test_unknown_member():
    with pytest.raises(ValueError) as exc:
        _register([AllOrNone("pair", [Member("aa"), Member("nope")])])()
    assert str(exc.value) == (
        'command "cmd": constraint "pair" references unknown member "nope"'
    )


def test_duplicate_member():
    with pytest.raises(ValueError) as exc:
        _register([AllOrNone("pair", [Member("aa"), Member("aa")])])()
    assert str(exc.value) == (
        'command "cmd": constraint "pair" declares member "aa" twice'
    )


def test_a_member_naming_both_a_flag_and_an_arg_is_ambiguous():
    """The implementations check duplicate flag and arg names separately, so a
    command may declare both; this round refuses to GUESS inside that state."""
    with pytest.raises(ValueError) as exc:
        _register(
            [AllOrNone("pair", [Member("aa"), Member("bb")])],
            args=[strictcli.Arg("aa", help="aa", presence="optional")],
        )()
    assert str(exc.value) == (
        'command "cmd": constraint "pair" references "aa", which names both a '
        "flag and a positional arg"
    )


def test_requires_unknown_flag_keeps_the_flag_noun():
    with pytest.raises(ValueError) as exc:
        _register([Requires("dep", flag="aa", depends_on="nope")])()
    assert str(exc.value) == (
        'command "cmd": constraint "dep" references unknown flag "nope"'
    )


def test_implies_unknown_flag_keeps_the_flag_noun():
    with pytest.raises(ValueError) as exc:
        _register(
            [Implies("imp", flag="nope", implies="aa", value=True)],
            flags=(("aa", bool), ("bb", bool)),
        )()
    assert str(exc.value) == (
        'command "cmd": constraint "imp" references unknown flag "nope"'
    )


# ---------------------------------------------------------------------------
# Registration: scope (§26.8 step 4, §24.8)
# ---------------------------------------------------------------------------


def test_a_constraint_cannot_reference_a_scoped_flag():
    """§24.8: the scope already IS the constraint, so naming one is refused.

    The refusal names the CONSTRAINT rather than the family (§12.13's
    amendment), and the trailing clause drops the word `dependency`.
    """

    @strictcli.choice("email", help="deliver by email")
    class Email:
        subject: str = strictcli.sub_flag(help="the subject", presence="required")

    @strictcli.choice("sms", help="deliver by text")
    class Sms:
        phone: str = strictcli.sub_flag(help="the number", presence="required")

    app = strictcli.App(name="test", version="1.0.0", help="test app")

    with pytest.raises(ValueError) as exc:

        @app.command(
            "cmd", effect="read_only", help="a command",
            constraints=[Requires("dep", flag="output", depends_on="subject")],
        )
        @strictcli.choice_flag(
            "via", help="delivery channel", presence="required",
            elect_by="selector-token", choices=[Email, Sms],
        )
        @strictcli.flag("output", type=str, help="output path", default="")
        def cmd(ctx, output, via: Email | Sms):
            pass

    assert str(exc.value) == (
        'command "cmd": constraint "dep" references \'subject\', which is '
        "declared under '--via email': constraints operate at root scope only"
    )


def test_a_co_occurrence_member_cannot_be_a_scoped_flag():
    @strictcli.choice("email", help="deliver by email")
    class Email:
        subject: str = strictcli.sub_flag(help="the subject", presence="required")

    @strictcli.choice("sms", help="deliver by text")
    class Sms:
        phone: str = strictcli.sub_flag(help="the number", presence="required")

    app = strictcli.App(name="test", version="1.0.0", help="test app")

    with pytest.raises(ValueError) as exc:

        @app.command(
            "cmd", effect="read_only", help="a command",
            constraints=[AllOrNone("pair", [Member("output"), Member("subject")])],
        )
        @strictcli.choice_flag(
            "via", help="delivery channel", presence="required",
            elect_by="selector-token", choices=[Email, Sms],
        )
        @strictcli.flag("output", type=str, help="output path", presence="optional")
        def cmd(ctx, output, via: Email | Sms):
            pass

    assert str(exc.value) == (
        'command "cmd": constraint "pair" references \'subject\', which is '
        "declared under '--via email': constraints operate at root scope only"
    )


def test_a_member_naming_a_member_flag_is_the_scope_refusal():
    """A member-spelled choice's own token is declared BY the election it
    carries, so it lives in that choice's scope: naming it is the scoped-operand
    refusal, whose path renders as the member token itself (§12.13)."""

    @strictcli.choice("file", help="read from a file")
    class FromFile:
        value: str = strictcli.member_value(help="the path")

    @strictcli.choice("url", help="read from a URL")
    class FromUrl:
        value: str = strictcli.member_value(help="the URL")

    app = strictcli.App(name="test", version="1.0.0", help="test app")

    with pytest.raises(ValueError) as exc:

        @app.command(
            "cmd", effect="read_only", help="a command",
            constraints=[AtLeastOne("pick", [Member("file"), Member("host")])],
        )
        @strictcli.choice_flag(
            "src", help="where to read from", presence="required",
            elect_by="member-flags", choices=[FromFile, FromUrl],
        )
        @strictcli.flag("host", type=str, help="the host", presence="optional")
        def cmd(ctx, host, src: FromFile | FromUrl):
            pass

    assert str(exc.value) == (
        'command "cmd": constraint "pick" references \'file\', which is '
        "declared under '--file': constraints operate at root scope only"
    )


# ---------------------------------------------------------------------------
# Registration: nesting legality (§26.8 step 5)
# ---------------------------------------------------------------------------


def test_a_nested_member_cannot_declare_an_election():
    with pytest.raises(ValueError) as exc:
        _register([
            AllOrNone("pair", [Member("aa"), Member("bb")]),
            AtLeastOne("outer", [Member("pair", when="present"), Member("aa")]),
        ])()
    assert str(exc.value) == (
        'command "cmd": constraint "outer" member "pair" is a constraint and '
        "cannot declare an election: a nested constraint is engaged when its "
        "own members are"
    )


def test_a_dependency_family_cannot_be_nested():
    with pytest.raises(ValueError) as exc:
        _register([
            Requires("dep", flag="aa", depends_on="bb"),
            AtLeastOne("outer", [Member("dep"), Member("aa")]),
        ])()
    assert str(exc.value) == (
        'command "cmd": constraint "outer" references constraint "dep", which '
        "declares a one-way dependency rather than a co-occurrence rule: only "
        "at-least-one and all-or-none can be members of another constraint"
    )


def test_a_constraint_cycle_is_refused():
    with pytest.raises(ValueError) as exc:
        _register([
            AtLeastOne("outer", [Member("inner"), Member("aa")]),
            AtLeastOne("inner", [Member("outer"), Member("bb")]),
        ])()
    assert str(exc.value) == (
        'command "cmd": constraints form a cycle: outer -> inner -> outer'
    )


def test_the_cycle_path_opens_on_the_earliest_declared_participant():
    """The walk starts at "xx", enters the cycle at "cc" and closes it at "bb" --
    and the path still opens on "bb", which is declared first (§12.15)."""
    with pytest.raises(ValueError) as exc:
        _register([
            AtLeastOne("xx", [Member("cc"), Member("aa")]),
            AtLeastOne("bb", [Member("cc"), Member("aa")]),
            AtLeastOne("cc", [Member("bb"), Member("aa")]),
        ], flags=(("aa", str),))()
    assert str(exc.value) == (
        'command "cmd": constraints form a cycle: bb -> cc -> bb'
    )


def test_a_self_naming_constraint_is_the_degenerate_cycle():
    with pytest.raises(ValueError) as exc:
        _register([AtLeastOne("outer", [Member("outer"), Member("aa")])])()
    assert str(exc.value) == (
        'command "cmd": constraints form a cycle: outer -> outer'
    )


def test_deep_nesting_is_legal():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[
            AllOrNone("l1", [Member("aa"), Member("bb")]),
            AtLeastOne("l2", [Member("l1"), Member("cc")]),
            AtLeastOne("l3", [Member("l2"), Member("dd")]),
        ],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    @strictcli.flag("cc", type=str, help="cc", presence="optional")
    @strictcli.flag("dd", type=str, help="dd", presence="optional")
    def cmd(ctx, aa, bb, cc, dd):
        print("ran")

    assert app.test(["cmd", "--dd", "xx"]).exit_code == 1  # l2 fires first
    assert app.test(["cmd", "--cc", "xx"]).exit_code == 0


# ---------------------------------------------------------------------------
# Registration: election legality (§26.8 step 6)
# ---------------------------------------------------------------------------


def test_a_bool_member_must_declare_its_election():
    """Without this refusal, `present` on a bool means `--no-all` engages a
    constraint while selecting nothing (A1, by omission)."""
    with pytest.raises(ValueError) as exc:
        _register(
            [AtLeastOne("selection", [Member("aa"), Member("bb")])],
            flags=(("aa", bool), ("bb", str)),
        )()
    assert str(exc.value) == (
        'command "cmd": constraint "selection" member \'--aa\' is a bool and '
        'must declare its election: when="true" counts only a true value, '
        'when="present" counts any'
    )


def test_when_true_on_a_non_bool_is_refused():
    with pytest.raises(ValueError) as exc:
        _register([AtLeastOne("selection", [
            Member("aa", when="true"), Member("bb"),
        ])])()
    assert str(exc.value) == (
        'command "cmd": constraint "selection" member \'--aa\' declares '
        'when="true", which needs a bool; \'--aa\' is a str'
    )


def test_when_non_empty_on_an_int_is_refused():
    with pytest.raises(ValueError) as exc:
        _register(
            [AtLeastOne("selection", [
                Member("aa", when="non_empty"), Member("bb"),
            ])],
            flags=(("aa", int), ("bb", str)),
        )()
    assert str(exc.value) == (
        'command "cmd": constraint "selection" member \'--aa\' declares '
        'when="non_empty", which needs a string or a collection; '
        "'--aa' is a int"
    )


def test_when_non_empty_on_a_bool_is_refused():
    with pytest.raises(ValueError) as exc:
        _register(
            [AtLeastOne("selection", [
                Member("aa", when="non_empty"), Member("bb"),
            ])],
            flags=(("aa", bool), ("bb", str)),
        )()
    assert "'--aa' is a bool" in str(exc.value)


def test_when_true_on_a_repeatable_names_the_compound_type_word():
    with pytest.raises(ValueError) as exc:
        _register(
            [AtLeastOne("selection", [
                Member("aa", when="true"), Member("bb"),
            ])],
            flags=(("aa", list[str]), ("bb", str)),
        )()
    assert "'--aa' is a list[str]" in str(exc.value)


def test_a_bool_arg_member_renders_bare_in_the_election_guard():
    with pytest.raises(ValueError) as exc:
        _register(
            [AtLeastOne("selection", [Member("target"), Member("aa")])],
            args=[strictcli.Arg(
                "target", type=bool, help="tt", presence="optional",
            )],
        )()
    assert str(exc.value) == (
        'command "cmd": constraint "selection" member \'target\' is a bool and '
        'must declare its election: when="true" counts only a true value, '
        'when="present" counts any'
    )


# ---------------------------------------------------------------------------
# Registration: presence legality (§26.8 step 7, §26.5)
# ---------------------------------------------------------------------------


def test_a_required_member_is_refused_in_all_or_none():
    """§23.5's shipped "legal, and stated because it is a surprising shape"
    cell becomes a registration error: the surprise was the whole objection."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    with pytest.raises(ValueError) as exc:

        @app.command(
            "cmd", effect="read_only", help="a command",
            constraints=[AllOrNone("pair", [Member("aa"), Member("bb")])],
        )
        @strictcli.flag("aa", type=str, help="aa", presence="required")
        @strictcli.flag("bb", type=str, help="bb", presence="optional")
        def cmd(ctx, aa, bb):
            pass

    assert str(exc.value) == (
        'command "cmd": constraint "pair" member \'--aa\' declares '
        'presence="required": a member the invocation must always supply '
        "leaves the constraint nothing to decide"
    )


def test_a_required_member_is_refused_in_at_least_one():
    """The invocation always supplies it, so the constraint is satisfied in
    every invocation and can never fire."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    with pytest.raises(ValueError) as exc:

        @app.command(
            "cmd", effect="read_only", help="a command",
            constraints=[AtLeastOne("selection", [Member("aa"), Member("bb")])],
        )
        @strictcli.flag("aa", type=str, help="aa", presence="optional")
        @strictcli.flag("bb", type=str, help="bb", presence="required")
        def cmd(ctx, aa, bb):
            pass

    assert 'member \'--bb\' declares presence="required"' in str(exc.value)


def test_a_required_arg_member_renders_bare():
    """One template with one substitution, not a Flag/Arg twin pair."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    with pytest.raises(ValueError) as exc:

        @app.command(
            "cmd", effect="read_only", help="a command",
            args=[strictcli.Arg("targets", help="tt", variadic=True,
                                presence="required")],
            constraints=[AtLeastOne("selection", [
                Member("targets"), Member("aa"),
            ])],
        )
        @strictcli.flag("aa", type=str, help="aa", presence="optional")
        def cmd(ctx, targets, aa):
            pass

    assert str(exc.value) == (
        'command "cmd": constraint "selection" member \'targets\' declares '
        'presence="required": a member the invocation must always supply '
        "leaves the constraint nothing to decide"
    )


def test_membership_neither_makes_a_flag_required_nor_exempts_it():
    """An at-least-one over two optional flags leaves both optional."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AtLeastOne("selection", [Member("aa"), Member("bb")])],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    def cmd(ctx, aa, bb):
        print(f"a={aa!r} b={bb!r}")

    r = app.test(["cmd", "--aa", "xx"])
    assert r.exit_code == 0
    assert "b=None" in r.stdout


def test_a_member_flag_of_a_member_spelled_selector_is_closed_by_two_rules():
    """A member flag must declare requiredness (§12.13) and §26.5 refuses a
    required member -- two existing rules meeting, no third rule needed."""

    @strictcli.choice("work", help="the work profile")
    class Work:
        pass

    @strictcli.choice("home", help="the home profile")
    class Home:
        pass

    app = strictcli.App(name="test", version="1.0.0", help="test app")

    with pytest.raises(ValueError) as exc:

        @app.command(
            "cmd", effect="read_only", help="a command",
            constraints=[AtLeastOne("selection", [Member("work"), Member("aa")])],
        )
        @strictcli.choice_flag(
            "profile", help="which profile", presence="required",
            elect_by="member-flags", choices=[Work, Home],
        )
        @strictcli.flag("aa", type=str, help="aa", presence="optional")
        def cmd(ctx, aa, profile: Work | Home):
            pass

    assert "constraint" in str(exc.value)


# ---------------------------------------------------------------------------
# `Requires` -- semantics untouched (§26.13)
# ---------------------------------------------------------------------------


def _requires_app(**presence):
    app = strictcli.App(name="test", version="1.0.0", help="test app")
    p = presence or {"presence": "optional"}

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[Requires("fmt-needs-out", flag="format", depends_on="output")],
    )
    @strictcli.flag("output", type=str, help="output path", **p)
    @strictcli.flag("format", type=str, help="output format", **p)
    def cmd(ctx, output, format):
        print(f"output={output} format={format}")

    return app


def test_requires_both_provided_ok():
    r = _requires_app().test(["cmd", "--output", "ff", "--format", "json"])
    assert r.exit_code == 0


def test_requires_flag_not_provided_ok():
    assert _requires_app(default="").test(["cmd"]).exit_code == 0


def test_requires_depends_on_alone_ok():
    """Unidirectional."""
    assert _requires_app(default="").test(["cmd", "--output", "ff"]).exit_code == 0


def test_requires_violation_carries_the_prefix_and_nothing_else():
    """The sentence is untouched byte for byte AFTER the prefix (§26.13)."""
    r = _requires_app().test(["cmd", "--format", "json"])
    assert r.exit_code == 1
    assert r.stderr.splitlines()[0] == (
        'error: constraint "fmt-needs-out": flag \'--format\' requires \'--output\''
    )


def test_requires_same_flag_error():
    with pytest.raises(ValueError, match="cannot be the same"):
        _register([Requires("dep", flag="aa", depends_on="aa")])()


# ---------------------------------------------------------------------------
# `Implies` -- semantics untouched (§26.13)
# ---------------------------------------------------------------------------


def _implies_app(target_default=False):
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[Implies("fast-implies", flag="fast", implies="embeddings",
                             value=False)],
    )
    @strictcli.flag("fast", type=bool, default=False, help="fast mode")
    @strictcli.flag("embeddings", type=bool, default=target_default,
                    help="use embeddings")
    def cmd(ctx, fast, embeddings):
        print(f"fast={fast} embeddings={embeddings}")

    return app


def test_implies_trigger_set_target_auto_set():
    r = _implies_app().test(["cmd", "--fast"])
    assert r.exit_code == 0
    assert "fast=True" in r.stdout
    assert "embeddings=False" in r.stdout


def test_implies_trigger_not_set_target_gets_default():
    r = _implies_app(target_default=True).test(["cmd"])
    assert r.exit_code == 0
    assert "embeddings=True" in r.stdout


def test_implies_conflict_carries_the_prefix():
    r = _implies_app().test(["cmd", "--fast", "--embeddings"])
    assert r.exit_code == 1
    assert r.stderr.splitlines()[0] == (
        'error: constraint "fast-implies": flag \'--fast\' implies '
        "'--no-embeddings', but '--embeddings' was explicitly provided"
    )


def test_implies_explicit_agreement_ok():
    r = _implies_app().test(["cmd", "--fast", "--no-embeddings"])
    assert r.exit_code == 0


def test_implies_self_implication_error():
    with pytest.raises(ValueError, match="cannot be the same"):
        _register(
            [Implies("imp", flag="aa", implies="aa", value=True)],
            flags=(("aa", bool), ("bb", bool)),
        )()


def test_implies_trigger_not_bool_error():
    with pytest.raises(ValueError, match='trigger flag "aa" must be a bool flag'):
        _register(
            [Implies("imp", flag="aa", implies="bb", value=True)],
            flags=(("aa", str), ("bb", bool)),
        )()


def test_implies_target_not_bool_error():
    with pytest.raises(ValueError, match='target flag "bb" must be a bool flag'):
        _register(
            [Implies("imp", flag="aa", implies="bb", value=True)],
            flags=(("aa", bool), ("bb", str)),
        )()


def test_implies_env_var_trigger(monkeypatch):
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[Implies("imp", flag="fast", implies="embeddings",
                             value=False)],
    )
    @strictcli.flag("fast", type=bool, default=False, help="fast mode",
                    env="TEST_IMPLIES_FAST", prefixed=False)
    @strictcli.flag("embeddings", type=bool, default=False, help="use embeddings")
    def cmd(ctx, fast, embeddings):
        print(f"fast={fast} embeddings={embeddings}")

    monkeypatch.setenv("TEST_IMPLIES_FAST", "true")
    r = app.test(["cmd"])
    assert r.exit_code == 0
    assert "embeddings=False" in r.stdout


def test_implies_with_requires_interaction():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[
            Implies("imp", flag="fast", implies="embeddings", value=False),
            Requires("out-needs-fast", flag="output", depends_on="fast"),
        ],
    )
    @strictcli.flag("fast", type=bool, default=False, help="fast mode")
    @strictcli.flag("embeddings", type=bool, default=False, help="use embeddings")
    @strictcli.flag("output", type=str, help="output path", presence="optional")
    def cmd(ctx, fast, embeddings, output):
        print(f"fast={fast} embeddings={embeddings} output={output}")

    r = app.test(["cmd", "--fast", "--output", "file.txt"])
    assert r.exit_code == 0
    assert "embeddings=False" in r.stdout

    r = app.test(["cmd", "--output", "file.txt"])
    assert r.exit_code == 1
    assert 'constraint "out-needs-fast"' in r.stderr


# ---------------------------------------------------------------------------
# `CoRequired` is deleted (§26.1) -- no alias, no shim
# ---------------------------------------------------------------------------


def test_corequired_is_gone():
    assert not hasattr(strictcli, "CoRequired")
    assert "CoRequired" not in strictcli.__all__


def test_dependencies_container_is_gone():
    app = strictcli.App(name="test", version="1.0.0", help="test app")
    with pytest.raises(TypeError):

        @app.command(
            "cmd", effect="read_only", help="a command",
            dependencies=[],
        )
        def cmd(ctx):
            pass


def test_the_new_names_are_exported():
    for n in ("AtLeastOne", "AllOrNone", "Requires", "Implies", "Member"):
        assert n in strictcli.__all__
        assert hasattr(strictcli, n)


def test_the_families_are_frozen():
    c = AllOrNone("pair", [Member("aa"), Member("bb")])
    with pytest.raises(Exception):
        c.name = "other"
    assert isinstance(c.members, tuple)


# ---------------------------------------------------------------------------
# Help rendering (§26.10) -- the first rendering this system has ever had
# ---------------------------------------------------------------------------


def test_the_constraints_block_renders_after_the_flag_block():
    r = _safegit_app().test(["rewrite", "--help"])
    assert r.stdout == (
        "safegit rewrite -- rewrite author identity\n"
        "\n"
        "Flags:\n"
        "  --old-name <str>     old name [optional]\n"
        "  --new-name <str>     new name [optional]\n"
        "  --old-email <str>    old email [optional]\n"
        "  --new-email <str>    new email [optional]\n"
        "\n"
        "Constraints:\n"
        "  author-name      all or none of --old-name, --new-name\n"
        "  author-email     all or none of --old-email, --new-email\n"
        "  author-change    at least one of (--old-name with --new-name), "
        "(--old-email with --new-email)\n"
    )


def test_the_constraint_block_has_its_own_alignment_column():
    """It never shares the flag block's column (§24.10's rule for a third
    section)."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AllOrNone("ab", [Member("aa"), Member("bb")])],
    )
    @strictcli.flag("aa", type=str, help="a flag with a very long name", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    def cmd(ctx, aa, bb):
        pass

    out = app.test(["cmd", "--help"]).stdout
    assert "  ab    all or none of --aa, --bb" in out


def test_the_constraints_block_renders_args_bare_and_every_family():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        args=[strictcli.Arg("targets", help="ids", variadic=True,
                            presence="optional")],
        constraints=[
            AtLeastOne("selection", [
                Member("targets", when="non_empty"), Member("aa"),
            ]),
            Requires("b-needs-a", flag="bb", depends_on="aa"),
            Implies("c-implies-d", flag="cc", implies="dd", value=False),
        ],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    @strictcli.flag("cc", type=bool, help="cc", default=False)
    @strictcli.flag("dd", type=bool, help="dd", default=False)
    def cmd(ctx, targets, aa, bb, cc, dd):
        pass

    out = app.test(["cmd", "--help"]).stdout
    assert "  selection      at least one of targets, --aa\n" in out
    assert "  b-needs-a      --bb requires --aa\n" in out
    assert "  c-implies-d    --cc implies --no-dd" in out


def test_implies_renders_the_positive_target_when_the_value_is_true():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[Implies("c-implies-d", flag="cc", implies="dd", value=True)],
    )
    @strictcli.flag("cc", type=bool, help="cc", default=False)
    @strictcli.flag("dd", type=bool, help="dd", default=False)
    def cmd(ctx, cc, dd):
        pass

    assert "  c-implies-d    --cc implies --dd" in app.test(["cmd", "--help"]).stdout


def test_no_constraints_no_block():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    def cmd(ctx, aa):
        pass

    assert "Constraints:" not in app.test(["cmd", "--help"]).stdout


def test_a_member_carries_no_presence_part_in_the_constraint_block():
    """Every flag line already carries exactly one presence part (§23.8), and
    a constraint states a rule over members rather than a property of one."""
    out = _safegit_app().test(["rewrite", "--help"]).stdout
    block = out.split("Constraints:\n")[1]
    assert "[optional]" not in block
    assert "[required]" not in block


# ---------------------------------------------------------------------------
# The MCP projection and the declared lossiness policy (§26.12)
# ---------------------------------------------------------------------------


def test_at_least_one_over_all_or_none_projects_exactly():
    """safegit's site projects with no loss at all."""
    schema = _safegit_app().json_schema("rewrite")
    assert schema["anyOf"] == [
        {"required": ["old_name", "new_name"]},
        {"required": ["old_email", "new_email"]},
    ]
    assert schema["dependentRequired"] == {
        "old_name": ["new_name"],
        "new_name": ["old_name"],
        "old_email": ["new_email"],
        "new_email": ["old_email"],
    }


def test_an_exact_projection_appends_no_clause():
    tool = next(t for t in _safegit_app().as_tools() if t.name == "rewrite")
    assert tool.description == (
        "rewrite author identity\n"
        "\n"
        "Constraints (enforced at call time):\n"
        "  all or none of: old_name, new_name\n"
        "  all or none of: old_email, new_email\n"
        "  at least one of: (old_name with new_name), (old_email with new_email)"
    )


def test_a_partial_projection_states_the_remainder():
    """`required` says a key is present, not that it is true or non-empty."""
    app = _purge_app()
    schema = app.json_schema("purge")
    assert schema["anyOf"] == [
        {"required": ["targets"]},
        {"required": ["older_than"]},
        {"required": ["all"]},
    ]
    tool = next(t for t in app.as_tools() if t.name == "purge")
    assert tool.description == (
        "destroy archived items\n"
        "\n"
        "Constraints (enforced at call time):\n"
        "  at least one of: targets, older_than, all -- not expressed in the "
        'schema: the "true" and "non_empty" selectors'
    )


def test_an_all_or_none_with_a_nested_member_emits_nothing_and_says_why():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[
            AtLeastOne("inner", [Member("aa"), Member("bb")]),
            AllOrNone("outer", [Member("inner"), Member("cc")]),
        ],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    @strictcli.flag("cc", type=str, help="cc", presence="optional")
    def cmd(ctx, aa, bb, cc):
        pass

    schema = app.json_schema("cmd")
    assert "dependentRequired" not in schema
    tool = next(t for t in app.as_tools() if t.name == "cmd")
    assert (
        "  all or none of: (aa or bb), cc -- not expressed in the schema: "
        "the nested grouping" in tool.description
    )


def test_a_nested_at_least_one_inlines_its_branches():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[
            AtLeastOne("inner", [Member("aa"), Member("bb")]),
            AtLeastOne("outer", [Member("inner"), Member("cc")]),
        ],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    @strictcli.flag("cc", type=str, help="cc", presence="optional")
    def cmd(ctx, aa, bb, cc):
        pass

    # Two at-least-one constraints are two independent rules, so they are
    # conjoined rather than merged (§18.31 item 284) -- and `inner`'s branches
    # are inlined into `outer`'s own `anyOf`.
    assert app.json_schema("cmd")["allOf"] == [
        {"anyOf": [{"required": ["aa"]}, {"required": ["bb"]}]},
        {"anyOf": [
            {"required": ["aa"]},
            {"required": ["bb"]},
            {"required": ["cc"]},
        ]},
    ]


def test_requires_projects_dependent_required_exactly():
    app = _requires_app()
    assert app.json_schema("cmd")["dependentRequired"] == {"format": ["output"]}
    tool = next(t for t in app.as_tools() if t.name == "cmd")
    assert "  format requires output" in tool.description
    assert "not expressed in the schema" not in tool.description


def test_implies_projects_nothing_and_takes_a_description_line():
    """It injects a value rather than constraining the input, so there is
    nothing for a schema to say."""
    app = _implies_app()
    schema = app.json_schema("cmd")
    assert "anyOf" not in schema
    assert "dependentRequired" not in schema
    tool = next(t for t in app.as_tools() if t.name == "cmd")
    assert (
        "  fast implies embeddings = false -- not expressed in the schema: "
        "the injection" in tool.description
    )


def test_a_command_with_no_constraints_gets_no_description_block():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    def cmd(ctx, aa):
        pass

    tool = next(t for t in app.as_tools() if t.name == "cmd")
    assert tool.description == "a command"


def test_the_scope_block_and_the_constraint_block_coexist():
    """Appended after the scope block and separated from it by a blank line."""

    @strictcli.choice("email", help="deliver by email")
    class Email:
        subject: str = strictcli.sub_flag(help="the subject", presence="required")

    @strictcli.choice("sms", help="deliver by text")
    class Sms:
        phone: str = strictcli.sub_flag(help="the number", presence="required")

    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "send", effect="read_only", help="send it",
        constraints=[AllOrNone("pair", [Member("aa"), Member("bb")])],
    )
    @strictcli.choice_flag(
        "via", help="delivery channel", presence="required",
        elect_by="selector-token", choices=[Email, Sms],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    def send(ctx, aa, bb, via: Email | Sms):
        pass

    tool = next(t for t in app.as_tools() if t.name == "send")
    assert tool.description == (
        "send it\n"
        "\n"
        "Scoped parameters (enforced at call time):\n"
        "  via=email: subject (required)\n"
        "  via=sms: phone (required)\n"
        "\n"
        "Constraints (enforced at call time):\n"
        "  all or none of: aa, bb"
    )


def test_a_violation_at_the_machine_door_uses_the_parsers_own_sentence():
    """Enforcement at call time is unchanged and total (§26.12)."""
    app = _safegit_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        app.call("rewrite", old_name="aa")
    assert str(exc.value) == (
        'constraint "author-name": --old-name, --new-name must be used together'
    )


# ---------------------------------------------------------------------------
# The reconciliation round (§18.31): the pins this implementation had to move
# to, and the ones it already met.
# ---------------------------------------------------------------------------


def _two_at_least_one_app():
    """Two independent at-least-one rules over disjoint members."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[
            AtLeastOne("first", [Member("aa"), Member("bb")]),
            AtLeastOne("second", [Member("cc"), Member("dd")]),
        ],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    @strictcli.flag("cc", type=str, help="cc", presence="optional")
    @strictcli.flag("dd", type=str, help="dd", presence="optional")
    def cmd(ctx, aa, bb, cc, dd):
        pass

    return app


def test_two_at_least_one_constraints_conjoin_in_all_of():
    """§18.31 item 284: merging their branches would publish a schema
    satisfied by EITHER rule where the command declares that both must hold."""
    schema = _two_at_least_one_app().json_schema("cmd")
    assert "anyOf" not in schema
    assert schema["allOf"] == [
        {"anyOf": [{"required": ["aa"]}, {"required": ["bb"]}]},
        {"anyOf": [{"required": ["cc"]}, {"required": ["dd"]}]},
    ]


def test_exactly_one_at_least_one_stays_a_bare_any_of():
    schema = _purge_app().json_schema("purge")
    assert "allOf" not in schema
    assert schema["anyOf"] == [
        {"required": ["targets"]},
        {"required": ["older_than"]},
        {"required": ["all"]},
    ]


def test_the_all_of_elements_are_in_declaration_order():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[
            AtLeastOne("second", [Member("cc"), Member("dd")]),
            AtLeastOne("first", [Member("aa"), Member("bb")]),
        ],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    @strictcli.flag("cc", type=str, help="cc", presence="optional")
    @strictcli.flag("dd", type=str, help="dd", presence="optional")
    def cmd(ctx, aa, bb, cc, dd):
        pass

    assert app.json_schema("cmd")["allOf"] == [
        {"anyOf": [{"required": ["cc"]}, {"required": ["dd"]}]},
        {"anyOf": [{"required": ["aa"]}, {"required": ["bb"]}]},
    ]


def test_the_keywords_sit_after_required_and_before_additional_properties():
    """§18.31 item 286: the rule-carrying keywords sit beside the two keys
    they qualify, ahead of the key that closes the object."""
    schema = _safegit_app().json_schema("rewrite")
    assert list(schema) == [
        "type", "properties", "required", "anyOf", "dependentRequired",
        "additionalProperties",
    ]


def test_the_all_of_form_takes_the_same_position():
    schema = _two_at_least_one_app().json_schema("cmd")
    assert list(schema) == [
        "type", "properties", "required", "allOf", "additionalProperties",
    ]


def test_a_command_with_no_constraints_keeps_the_bare_key_order():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    def cmd(ctx, aa):
        pass

    assert list(app.json_schema("cmd")) == [
        "type", "properties", "required", "additionalProperties",
    ]


def test_implies_states_its_injected_value_in_the_description_block():
    """§18.31 item 283: a false value is a VALUE, not a name -- `no_<target>`
    names a key the schema does not carry and the caller can never send."""
    tool = next(t for t in _implies_app().as_tools() if t.name == "cmd")
    assert (
        "  fast implies embeddings = false -- not expressed in the schema: "
        "the injection" in tool.description
    )
    assert "no_embeddings" not in tool.description


def test_implies_states_a_true_value_the_same_way():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[Implies("c-implies-d", flag="cc", implies="dd", value=True)],
    )
    @strictcli.flag("cc", type=bool, help="cc", default=False)
    @strictcli.flag("dd", type=bool, help="dd", default=False)
    def cmd(ctx, cc, dd):
        pass

    tool = next(t for t in app.as_tools() if t.name == "cmd")
    assert (
        "  cc implies dd = true -- not expressed in the schema: the injection"
        in tool.description
    )


def test_the_implies_line_carries_no_in_sentence_commentary():
    """The clause comes from the block's closed set like every other partial,
    and the line says nothing else."""
    tool = next(t for t in _implies_app().as_tools() if t.name == "cmd")
    line = [
        ln for ln in tool.description.splitlines() if "implies" in ln
    ][0]
    assert line == (
        "  fast implies embeddings = false -- not expressed in the schema: "
        "the injection"
    )


def test_the_requires_line_is_two_property_names_and_no_clause():
    tool = next(t for t in _requires_app().as_tools() if t.name == "cmd")
    line = [
        ln for ln in tool.description.splitlines() if "requires" in ln
    ][0]
    assert line == "  format requires output"


def test_a_dict_member_renders_its_value_type_in_one_argument():
    """§18.31 item 289: the key type is `str` by construction, so a
    two-argument word states a fact no declaration can vary."""
    with pytest.raises(ValueError) as exc:
        _register(
            [AtLeastOne("selection", [
                Member("aa", when="true"), Member("bb"),
            ])],
            flags=(("aa", dict[str, int]), ("bb", str)),
        )()
    assert "'--aa' is a dict[int]" in str(exc.value)


def test_a_variadic_arg_member_renders_its_collection_spelling():
    with pytest.raises(ValueError) as exc:
        _register(
            [AtLeastOne("selection", [
                Member("targets", when="true"), Member("aa"),
            ])],
            args=[strictcli.Arg(
                "targets", type=str, help="tt", variadic=True,
                presence="optional",
            )],
        )()
    assert "'targets' is a list[str]" in str(exc.value)


def test_a_variadic_bool_arg_is_sized_and_never_bool():
    """§18.31 item 290: its value is a sequence whatever its element type is,
    and it has no `--no-` spelling to decline with."""
    build = _register(
        [AtLeastOne("selection", [Member("targets"), Member("aa")])],
        args=[strictcli.Arg(
            "targets", type=bool, help="tt", variadic=True, presence="optional",
        )],
    )
    build()  # omitting `when` is legal: the bool refusal does not apply

    with pytest.raises(ValueError) as exc:
        _register(
            [AtLeastOne("selection", [
                Member("targets", when="true"), Member("aa"),
            ])],
            args=[strictcli.Arg(
                "targets", type=bool, help="tt", variadic=True,
                presence="optional",
            )],
        )()
    assert "'targets' is a list[bool]" in str(exc.value)

    _register(
        [AtLeastOne("selection", [
            Member("targets", when="non_empty"), Member("aa"),
        ])],
        args=[strictcli.Arg(
            "targets", type=bool, help="tt", variadic=True,
            presence="optional",
        )],
    )()  # `non_empty` is legal on it


@strictcli.choice("email", help="deliver by email")
class _Email:
    pass


@strictcli.choice("sms", help="deliver by text")
class _Sms:
    pass


_Via = _Email | _Sms


def _selector_member_app(when=None, **selector_kwargs):
    kwargs = {
        "help": "delivery channel",
        "elect_by": "selector-token",
        "default": _Email(),
        "choices": [_Email, _Sms],
    }
    kwargs.update(selector_kwargs)
    if kwargs.get("presence") == "required":
        # A selector declares required OR a default, never both (§24.5).
        kwargs.pop("default", None)
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AtLeastOne("sel", [
            Member("via", when=when), Member("note"),
        ])],
    )
    @strictcli.choice_flag("via", **kwargs)
    @strictcli.flag("note", type=str, help="a note", presence="optional")
    def cmd(ctx, via: _Via, note):
        print("ran")

    return app


def test_a_token_spelled_selector_may_be_a_member():
    """§26.2: a token-spelled choice flag is an ordinary root-scope flag."""
    app = _selector_member_app()
    assert app.test(["cmd", "--via", "sms"]).exit_code == 0
    assert app.test(["cmd", "--note", "xx"]).exit_code == 0


def test_a_selector_member_engages_only_when_the_invocation_elected_it():
    """A DEFAULT election is the declaration deciding, not the invocation."""
    r = _selector_member_app().test(["cmd"])
    assert r.exit_code == 1
    assert r.stderr.splitlines()[0] == (
        'error: constraint "sel": at least one of --via, --note is required'
    )


def test_when_true_on_a_selector_member_renders_choice_flag():
    """§18.31 item 289: its value is a record neither selector can test, so
    the word names the CONSTRUCT."""
    with pytest.raises(ValueError) as exc:
        _selector_member_app(when="true")
    assert str(exc.value) == (
        'command "cmd": constraint "sel" member \'--via\' declares '
        'when="true", which needs a bool; \'--via\' is a choice flag'
    )


def test_when_non_empty_on_a_selector_member_renders_choice_flag():
    with pytest.raises(ValueError) as exc:
        _selector_member_app(when="non_empty")
    assert str(exc.value) == (
        'command "cmd": constraint "sel" member \'--via\' declares '
        'when="non_empty", which needs a string or a collection; '
        "'--via' is a choice flag"
    )


def test_a_required_selector_member_is_refused():
    """§26.5's inversion reaches a selector like any other declaration."""
    with pytest.raises(ValueError) as exc:
        _selector_member_app(presence="required")
    assert str(exc.value) == (
        'command "cmd": constraint "sel" member \'--via\' declares '
        'presence="required": a member the invocation must always supply '
        "leaves the constraint nothing to decide"
    )


def test_a_constraint_name_may_not_collide_with_a_selector_name():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    with pytest.raises(ValueError) as exc:

        @app.command(
            "cmd", effect="read_only", help="a command",
            constraints=[AtLeastOne("via", [Member("aa"), Member("note")])],
        )
        @strictcli.choice_flag(
            "via", help="channel", elect_by="selector-token",
            default=_Email(), choices=[_Email, _Sms],
        )
        @strictcli.flag("aa", type=str, help="aa", presence="optional")
        @strictcli.flag("note", type=str, help="nn", presence="optional")
        def cmd(ctx, via: _Via, aa, note):
            pass

    assert str(exc.value) == (
        'command "cmd": constraint name "via" is already a flag or arg name: '
        "a member reference resolves by name and would be ambiguous"
    )


def test_a_selector_member_projects_as_its_own_property():
    schema = _selector_member_app().json_schema("cmd")
    assert schema["anyOf"] == [{"required": ["via"]}, {"required": ["note"]}]
    tool = next(t for t in _selector_member_app().as_tools() if t.name == "cmd")
    assert "  at least one of: via, note" in tool.description


def test_the_decline_clause_searches_direct_members_only():
    """§18.31 item 290: a `--no-<x>` two levels down names a token the reader
    is not looking at, and the nested constraint has its own sentence."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[
            AllOrNone("inner", [Member("aa", when="true"), Member("bb")]),
            AtLeastOne("outer", [Member("inner"), Member("cc")]),
        ],
    )
    @strictcli.flag("aa", type=bool, help="aa", default=False)
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    @strictcli.flag("cc", type=str, help="cc", presence="optional")
    def cmd(ctx, aa, bb, cc):
        pass

    r = app.test(["cmd", "--no-aa"])
    assert r.exit_code == 1
    assert r.stderr.splitlines()[0] == (
        'error: constraint "outer": at least one of (--aa with --bb), --cc '
        "is required"
    )


def test_when_non_empty_on_a_scalar_arg_renders_the_member_bare():
    """The fourth member-naming guard takes §12.15's rendering too, and its
    trailing clause takes it a second time (§18.31 item 287)."""
    with pytest.raises(ValueError) as exc:
        _register(
            [AtLeastOne("selection", [
                Member("count", when="non_empty"), Member("aa"),
            ])],
            args=[strictcli.Arg(
                "count", type=int, help="how many", presence="optional",
            )],
        )()
    assert str(exc.value) == (
        'command "cmd": constraint "selection" member \'count\' declares '
        'when="non_empty", which needs a string or a collection; '
        "'count' is a int"
    )


def test_a_constraint_name_may_collide_with_a_scoped_flag_name():
    """§18.31 item 290: step 1's collision check reads ROOT flags and args
    only -- a member naming that scoped flag is refused one step later, with
    the message that names the actual fault."""

    @strictcli.choice("email", help="deliver by email")
    class Email:
        subject: str = strictcli.sub_flag(help="the subject", presence="required")

    @strictcli.choice("sms", help="deliver by text")
    class Sms:
        phone: str = strictcli.sub_flag(help="the number", presence="required")

    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "cmd", effect="read_only", help="a command",
        constraints=[AllOrNone("subject", [Member("aa"), Member("bb")])],
    )
    @strictcli.choice_flag(
        "via", help="delivery channel", presence="required",
        elect_by="selector-token", choices=[Email, Sms],
    )
    @strictcli.flag("aa", type=str, help="aa", presence="optional")
    @strictcli.flag("bb", type=str, help="bb", presence="optional")
    def cmd(ctx, aa, bb, via: Email | Sms):
        print("ran")

    assert app.test(
        ["cmd", "--via", "sms", "--phone", "1", "--aa", "xx", "--bb", "yy"],
    ).exit_code == 0
