"""Tests for the framework-owned reserved flag quartet.

Covers the unconditional name ban (--dry-run/--approve-consequential/
--quiet/--verbose) plus the outright `yes` ban, the
position-aware pre-scan extraction, Context delivery, and the --quiet/--verbose
output gating table.
"""

import pytest

import strictcli

QUARTET = ["dry-run", "approve-consequential", "quiet", "verbose"]
BAN_MESSAGE = (
    "is reserved by the framework "
    "(dry-run, approve-consequential, quiet, verbose)"
)


class TestReservedNameBan:
    """The ban is unconditional and applies at every level."""

    @pytest.mark.parametrize("name", QUARTET)
    def test_banned_on_a_bare_flag(self, name):
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(name=name, type=bool, default=False, help="nope")
        assert str(exc.value) == (
            f"flag name '{name}' is reserved by the framework "
            f"(dry-run, approve-consequential, quiet, verbose)"
        )

    @pytest.mark.parametrize("name", QUARTET)
    def test_banned_as_a_global_flag(self, name):
        with pytest.raises(ValueError, match=BAN_MESSAGE.replace("(", r"\(").replace(")", r"\)")):
            strictcli.App(
                name="app", version="1.0.0", help="app",
                flags=[strictcli.Flag(name=name, type=bool, default=False, help="nope")],
            )

    @pytest.mark.parametrize("name", QUARTET)
    def test_banned_as_a_command_flag(self, name):
        app = strictcli.App(name="app", version="1.0.0", help="app")
        with pytest.raises(ValueError) as exc:
            @app.command("run", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="run")
            @strictcli.flag(name, type=bool, default=False, help="nope")
            def _run(ctx, **kw):
                return 0
        assert BAN_MESSAGE in str(exc.value)

    @pytest.mark.parametrize("name", QUARTET)
    def test_banned_in_a_flag_set(self, name):
        with pytest.raises(ValueError) as exc:
            strictcli.FlagSet(flags=[
                strictcli.Flag(name=name, type=bool, default=False, help="nope"),
            ])
        assert BAN_MESSAGE in str(exc.value)

    @pytest.mark.parametrize("name", QUARTET)
    def test_banned_inside_a_choice_scope_at_every_depth(self, name):
        """§24.7: every existing name ban re-runs at every depth.

        A ban enforced only against a flat root list is this construct's most
        likely correctness defect, so it is a requirement rather than a
        discovery. The scope is validated when the SELECTOR claims it, which is
        where a message can name both the choice and the selector.
        """
        field = name.replace("-", "_")
        email = strictcli.choice("email", help="deliver by email")(
            type("Email", (), {
                "__annotations__": {field: bool},
                field: strictcli.sub_flag(help="nope", default=False),
            }),
        )

        @strictcli.choice("sms", help="deliver by text")
        class Sms:
            pass

        with pytest.raises(ValueError) as exc:
            strictcli.choice_flag(
                "via", help="delivery channel", presence="required",
                elect_by="selector-token", choices=[email, Sms],
            )
        assert BAN_MESSAGE in str(exc.value)

    @pytest.mark.parametrize("name", QUARTET)
    def test_banned_as_a_member_choice_name(self, name):
        """Under member spelling a choice name IS a flag name (§24.7)."""
        member = strictcli.choice(name, help="a member")(type("M", (), {}))

        @strictcli.choice("other", help="the other member")
        class Other:
            pass

        with pytest.raises(ValueError) as exc:
            strictcli.choice_flag(
                "mode", help="the mode", presence="required",
                elect_by="member-flags", choices=[member, Other],
            )
        assert BAN_MESSAGE in str(exc.value)

    def test_yes_is_banned_outright(self):
        """`yes` owns no framework flag any more, but it stays banned.

        A private --yes would restate --approve-consequential in a spelling
        that IS muscle memory -- exactly what the rename removed.
        """
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(name="yes", type=bool, default=False, help="nope")
        assert str(exc.value) == (
            "flag name 'yes' is banned by the framework: "
            "the confirmation skip is --approve-consequential"
        )

    def test_yes_is_banned_as_a_global_flag(self):
        with pytest.raises(ValueError) as exc:
            strictcli.App(
                name="app", version="1.0.0", help="app",
                flags=[strictcli.Flag(name="yes", type=bool, default=False,
                                      help="nope")],
            )
        assert "banned by the framework" in str(exc.value)

    def test_yes_is_banned_in_a_flag_set(self):
        with pytest.raises(ValueError) as exc:
            strictcli.FlagSet(flags=[
                strictcli.Flag(name="yes", type=bool, default=False,
                               help="nope"),
            ])
        assert "banned by the framework" in str(exc.value)

    def test_yes_is_no_longer_a_recognized_token(self):
        r = _quartet_app().test(["run", "--yes"])
        assert r.exit_code == 1
        assert "unknown flag '--yes'" in r.stderr

    def test_short_names_are_unaffected(self):
        """The ban covers long flag names only."""
        f = strictcli.Flag(name="loud", short="q", type=bool, default=False, help="loud")
        assert f.short == "q"

    def test_arg_names_are_unaffected(self):
        """A positional arg has no `--` spelling, so the ban does not apply."""
        a = strictcli.Arg(name="verbose", help="a positional named verbose", presence="required")
        assert a.name == "verbose"


def _quartet_app():
    app = strictcli.App(name="app", version="1.0.0", help="app")

    @app.command("run", effect="read_only", help="run", payload_schema={})
    def _run(ctx):
        ctx.payload({
            "dry_run": ctx.dry_run,
            "approve_consequential": ctx.approve_consequential,
            "quiet": ctx.quiet,
            "verbose": ctx.verbose,
        })
        return strictcli.outcome()

    return app


class TestDelivery:
    """The quartet is delivered on the Context, never as handler kwargs."""

    def test_defaults_all_false(self):
        r = _quartet_app().test(["run"])
        assert r.exit_code == 0
        assert r.data == {"dry_run": False, "approve_consequential": False,
                          "quiet": False, "verbose": False}

    @pytest.mark.parametrize("token,key", [
        ("--dry-run", "dry_run"),
        ("--approve-consequential", "approve_consequential"),
        ("--quiet", "quiet"),
        ("--verbose", "verbose"),
    ])
    def test_each_flag_reaches_the_context(self, token, key):
        r = _quartet_app().test([token, "run"])
        assert r.exit_code == 0
        assert r.data[key] is True
        for other in ("dry_run", "approve_consequential", "quiet", "verbose"):
            if other != key:
                assert r.data[other] is False

    def test_all_four_together(self):
        r = _quartet_app().test(["--dry-run", "--approve-consequential", "--quiet",
                                 "--verbose", "run"])
        assert r.exit_code == 0
        assert r.data == {"dry_run": True, "approve_consequential": True,
                          "quiet": True, "verbose": True}

    def test_not_passed_as_handler_kwargs(self):
        """A handler that names no quartet parameter still dispatches."""
        app = strictcli.App(name="app", version="1.0.0", help="app")
        seen = {}

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            seen["ok"] = True
            return 0

        r = app.test(["--dry-run", "--quiet", "run"])
        assert r.exit_code == 0
        assert seen == {"ok": True}

    @pytest.mark.parametrize("token,key", [
        ("--dry-run", "dry_run"),
        ("--approve-consequential", "approve_consequential"),
        ("--quiet", "quiet"),
        ("--verbose", "verbose"),
    ])
    def test_each_flag_is_recognized_after_the_command(self, token, key):
        """The quartet is recognized anywhere in argv, exactly like --help."""
        r = _quartet_app().test(["run", token])
        assert r.exit_code == 0
        assert r.data[key] is True

    def test_all_four_together_after_the_command(self):
        r = _quartet_app().test(["run", "--dry-run", "--approve-consequential",
                                 "--quiet", "--verbose"])
        assert r.exit_code == 0
        assert r.data == {"dry_run": True, "approve_consequential": True,
                          "quiet": True, "verbose": True}

    def test_mixed_positions_are_unioned(self):
        r = _quartet_app().test(["--dry-run", "run", "--verbose"])
        assert r.exit_code == 0
        assert r.data["dry_run"] is True
        assert r.data["verbose"] is True

    def test_recognized_after_a_nested_group_subcommand(self):
        app = strictcli.App(name="app", version="1.0.0", help="app")
        outer = app.group("outer", help="outer")
        inner = outer.group("inner", help="inner")

        @inner.command("run", effect="read_only", help="run", payload_schema={})
        def _run(ctx):
            ctx.payload({"dry_run": ctx.dry_run})
            return strictcli.outcome()

        r = app.test(["outer", "inner", "run", "--dry-run"])
        assert r.exit_code == 0
        assert r.data == {"dry_run": True}

    def test_recognized_between_a_group_and_its_subcommand(self):
        app = strictcli.App(name="app", version="1.0.0", help="app")
        grp = app.group("grp", help="grp")

        @grp.command("run", effect="read_only", help="run", payload_schema={})
        def _run(ctx):
            ctx.payload({"dry_run": ctx.dry_run})
            return strictcli.outcome()

        r = app.test(["grp", "--dry-run", "run"])
        assert r.exit_code == 0
        assert r.data == {"dry_run": True}

    def test_stripped_from_argv_after_the_command(self):
        """The quartet never reaches the command parser as an argument."""
        app = strictcli.App(name="app", version="1.0.0", help="app")

        @app.command("run", effect="read_only", help="run",
                     args=[strictcli.Arg(name="name", help="a positional", presence="required")], payload_schema={})
        def _run(ctx, name):
            ctx.payload({"name": name, "quiet": ctx.quiet})
            return strictcli.outcome()

        r = app.test(["run", "--quiet", "value"])
        assert r.exit_code == 0
        assert r.data == {"name": "value", "quiet": True}

    def test_a_token_after_double_dash_is_data(self):
        """A bare -- ends the scan: what follows is positional data."""
        app = strictcli.App(name="app", version="1.0.0", help="app")

        @app.command("run", effect="read_only", help="run",
                     args=[strictcli.Arg(name="rest", help="trailing args",
                                         variadic=True, presence="required")], payload_schema={})
        def _run(ctx, rest):
            ctx.payload({"rest": rest, "dry_run": ctx.dry_run})
            return strictcli.outcome()

        r = app.test(["run", "--", "--dry-run"])
        assert r.exit_code == 0
        assert r.data == {"rest": ["--dry-run"], "dry_run": False}

    def test_hermetic_stays_pre_command_only(self):
        """Only the quartet moved; --hermetic is still pre-command-only."""
        app = _quartet_app()
        r = app.test(["run", "--hermetic"])
        assert r.exit_code == 1
        assert "unknown flag '--hermetic'" in r.stderr

    def test_read_only_accepts_a_post_command_dry_run(self):
        app = _quartet_app()
        r = app.test(["run", "--dry-run"])
        assert r.exit_code == 0
        assert "DRY RUN — no changes were made. Would do:" in r.stdout

    def test_read_only_still_rejects_a_mutating_effect_post_command(self):
        """Per-command applicability is unchanged, wherever --dry-run appeared."""
        app = strictcli.App(name="app", version="1.0.0", help="app")

        @app.command("look", effect="read_only", help="look")
        def _look(ctx):
            ctx.effects.mkdir("d")
            return 0

        with pytest.raises(ValueError) as exc:
            app.test(["look", "--dry-run"])
        assert ('command "look" is classified read_only; '
                "effects.mkdir is a mutating operation") in str(exc.value)

    def test_passthrough_args_stay_opaque(self):
        app = strictcli.App(name="app", version="1.0.0", help="app")
        seen = {}

        def _pt(ctx, name, args, globals):
            seen["args"] = args
            return 0

        @app.command("exec", effect="read_only", help="exec",
                     passthrough=strictcli.Passthrough(handler=_pt))
        def _exec(ctx, **kw):
            return 0

        r = app.test(["exec", "--quiet", "--verbose"])
        assert r.exit_code == 0
        assert seen["args"] == ["--quiet", "--verbose"]

    def test_passthrough_under_a_group_stays_opaque(self):
        app = strictcli.App(name="app", version="1.0.0", help="app")
        seen = {}

        def _pt(ctx, name, args, globals):
            seen["args"] = args
            seen["verbose"] = ctx.verbose
            return 0

        grp = app.group("grp", help="grp")

        @grp.command("exec", effect="read_only", help="exec",
                     passthrough=strictcli.Passthrough(handler=_pt))
        def _exec(ctx, **kw):
            return 0

        r = app.test(["grp", "exec", "--verbose", "child"])
        assert r.exit_code == 0
        assert seen == {"args": ["--verbose", "child"], "verbose": False}

    def test_pre_command_position_is_the_passthrough_escape_hatch(self):
        """A pre-command quartet token reaches the Context AND leaves the
        child's identically-spelled argument untouched."""
        app = strictcli.App(name="app", version="1.0.0", help="app")
        seen = {}

        def _pt(ctx, name, args, globals):
            seen["args"] = args
            seen["verbose"] = ctx.verbose
            return 0

        @app.command("exec", effect="read_only", help="exec",
                     passthrough=strictcli.Passthrough(handler=_pt))
        def _exec(ctx, **kw):
            return 0

        r = app.test(["--verbose", "exec", "--verbose", "child"])
        assert r.exit_code == 0
        assert seen == {"args": ["--verbose", "child"], "verbose": True}


class TestGating:
    """The --quiet/--verbose gating table (quiet dominates verbose)."""

    def _run(self, argv):
        app = strictcli.App(name="app", version="1.0.0", help="app")

        @app.command("run", effect="read_only", help="run")
        def _r(ctx):
            ctx.debug("D")
            ctx.info("I")
            ctx.warn("W")
            ctx.error("E")
            return 0

        return app.test(argv)

    def test_default_hides_debug_only(self):
        r = self._run(["run"])
        assert r.stdout == "I\n"
        assert r.stderr == "warning: W\nerror: E\n"

    def test_verbose_shows_debug(self):
        r = self._run(["--verbose", "run"])
        assert r.stdout == "D\nI\n"
        assert r.stderr == "warning: W\nerror: E\n"

    def test_quiet_hides_info_and_debug(self):
        r = self._run(["--quiet", "run"])
        assert r.stdout == ""
        assert r.stderr == "warning: W\nerror: E\n"

    def test_quiet_dominates_verbose(self):
        r = self._run(["--quiet", "--verbose", "run"])
        assert r.stdout == ""
        assert r.stderr == "warning: W\nerror: E\n"

    def test_quiet_never_suppresses_the_machine_payload(self):
        """--quiet cannot reach the payload: it is not written through the
        writers quiet suppresses (contract §19.2, §7.4's amendment)."""
        app = strictcli.App(name="app", version="1.0.0", help="app")

        @app.command("run", effect="read_only", help="run", payload_schema={})
        def _r(ctx):
            ctx.payload({"k": 1})
            return strictcli.outcome()

        r = app.test(["--quiet", "--json", "run"])
        assert r.data == {"k": 1}
        assert '{"k":1}' in r.stdout

    def test_quiet_never_suppresses_parse_errors(self):
        app = _quartet_app()
        r = app.test(["--quiet", "nope"])
        assert r.exit_code == 1
        assert "unknown command" in r.stderr
