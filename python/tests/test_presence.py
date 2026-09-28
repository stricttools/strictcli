"""The presence declaration (contract §23).

Every flag and every positional arg declares exactly one of
``presence="required"``, ``presence="optional"`` and ``default=<value>``.
Nothing about presence is inferred from the shape of another declaration, so
these tests cover the registration errors byte-exactly, what each declaration
delivers at parse time, how presence composes with every other declaration,
``ctx.provided``, the schema keys, the help markers and the MCP projection.
"""

import json

import pytest

import strictcli


def _app(**kwargs):
    return strictcli.App(name="test", version="1.0.0", help="test app", **kwargs)


# ---------------------------------------------------------------------------
# §23.1 / §12.12 -- the registration errors, byte-exact
# ---------------------------------------------------------------------------


class TestUndeclaredPresence:
    def test_flag_declaring_nothing_does_not_register(self):
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(name="target", type=str, help="the target")
        assert str(exc.value) == (
            'Flag "target": presence is undeclared: declare exactly one of '
            'presence="required", presence="optional", or default=<value>'
        )

    def test_flag_decorator_declaring_nothing_does_not_register(self):
        app = _app()
        with pytest.raises(ValueError) as exc:

            @app.command("cmd", effect="read_only", help="a command")
            @strictcli.flag("target", type=str, help="the target")
            def cmd(ctx, target):
                pass

        assert str(exc.value) == (
            'Flag "target": presence is undeclared: declare exactly one of '
            'presence="required", presence="optional", or default=<value>'
        )

    def test_arg_declaring_nothing_does_not_register(self):
        with pytest.raises(ValueError) as exc:
            strictcli.Arg(name="path", help="the path")
        assert str(exc.value) == (
            'Arg "path": presence is undeclared: declare exactly one of '
            'presence="required", presence="optional", or default=<value>'
        )

    def test_compound_flags_have_no_exemption(self):
        """The silent forced-[] / forced-{} was a derivation and is deleted."""
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(
                name="tags", type=list[str], help="tags", unique=False,
            )
        assert "presence is undeclared" in str(exc.value)
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(name="headers", type=dict[str, str], help="headers")
        assert "presence is undeclared" in str(exc.value)


class TestPresenceDeclaredTwice:
    def test_flag_required_plus_default(self):
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(
                name="port", type=int, help="the port",
                presence="required", default=8080,
            )
        assert str(exc.value) == (
            'Flag "port": presence is declared twice: presence="required" and '
            "default=8080 cannot be combined; declare exactly one"
        )

    def test_flag_optional_plus_default(self):
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(
                name="target", type=str, help="the target",
                presence="optional", default="prod",
            )
        assert str(exc.value) == (
            'Flag "target": presence is declared twice: presence="optional" and '
            "default=prod cannot be combined; declare exactly one"
        )

    def test_flag_default_value_uses_the_error_value_formatter(self):
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(
                name="loud", type=bool, help="be loud",
                presence="required", default=False,
            )
        assert str(exc.value) == (
            'Flag "loud": presence is declared twice: presence="required" and '
            "default=false cannot be combined; declare exactly one"
        )

    def test_arg_optional_plus_default(self):
        with pytest.raises(ValueError) as exc:
            strictcli.Arg(
                name="path", help="the path",
                presence="optional", default=".",
            )
        assert str(exc.value) == (
            'Arg "path": presence is declared twice: presence="optional" and '
            "default=. cannot be combined; declare exactly one"
        )

    def test_the_two_are_named_in_canonical_order_not_written_order(self):
        """Canonical order is required, optional, default -- always.

        Python spells presence with one keyword carrying one of two words, so
        `presence=` and `default=` are the only pair that can co-occur and all
        three at once is unwritable: the required/optional pair Go can be
        handed does not exist here. What remains to pin is that writing the
        default first does not put it first in the message.
        """
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(
                name="port", type=int, help="the port",
                default=8080, presence="required",
            )
        assert str(exc.value) == (
            'Flag "port": presence is declared twice: presence="required" and '
            "default=8080 cannot be combined; declare exactly one"
        )
        with pytest.raises(ValueError) as exc:
            strictcli.Arg(
                name="path", help="the path",
                default=".", presence="required",
            )
        assert str(exc.value) == (
            'Arg "path": presence is declared twice: presence="required" and '
            "default=. cannot be combined; declare exactly one"
        )


class TestNullValuedDefault:
    def test_flag_default_none_redirects_to_optional(self):
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(name="target", type=str, help="the target", default=None)
        assert str(exc.value) == (
            'Flag "target": default=None does not declare optionality: use '
            'presence="optional" (it delivers None when the flag is absent)'
        )

    def test_arg_default_none_redirects_to_optional(self):
        with pytest.raises(ValueError) as exc:
            strictcli.Arg(name="path", help="the path", default=None)
        assert str(exc.value) == (
            'Arg "path": default=None does not declare optionality: use '
            'presence="optional" (it delivers None when the arg is absent)'
        )

    def test_the_two_declared_error_wins_over_the_redirect(self):
        """The count check runs first (§12.12's implementation-sweep box,
        ledger item 154).

        A null default written BESIDE a presence declaration is a combination
        error, and the message names both spellings -- including the one that
        was actually written, `default=None`. The redirect teaches the old
        idiom of writing `default=None` alone; an author who wrote `presence=`
        already knows the keyword and gets the neutral combination error.
        """
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(
                name="target", type=str, help="the target",
                presence="optional", default=None,
            )
        assert str(exc.value) == (
            'Flag "target": presence is declared twice: presence="optional" '
            "and default=None cannot be combined; declare exactly one"
        )
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(
                name="target", type=str, help="the target",
                presence="required", default=None,
            )
        assert str(exc.value) == (
            'Flag "target": presence is declared twice: presence="required" '
            "and default=None cannot be combined; declare exactly one"
        )

    def test_the_two_declared_error_wins_over_the_redirect_on_an_arg(self):
        """The arg twin of the rule above: `Arg`, the arg spellings, and the
        same canonical order (ledger item 154)."""
        with pytest.raises(ValueError) as exc:
            strictcli.Arg(
                name="path", help="the path",
                presence="optional", default=None,
            )
        assert str(exc.value) == (
            'Arg "path": presence is declared twice: presence="optional" '
            "and default=None cannot be combined; declare exactly one"
        )
        with pytest.raises(ValueError) as exc:
            strictcli.Arg(
                name="path", help="the path",
                presence="required", default=None,
            )
        assert str(exc.value) == (
            'Arg "path": presence is declared twice: presence="required" '
            "and default=None cannot be combined; declare exactly one"
        )


class TestPresenceValueInvalid:
    def test_presence_default_is_not_a_python_spelling(self):
        with pytest.raises(ValueError) as exc:
            strictcli.Flag(
                name="target", type=str, help="the target", presence="default",
            )
        assert str(exc.value) == (
            'Flag "target": presence must be "required" or "optional", got '
            "'default'; a default value is declared with default=<value>"
        )


class TestMemberFlagPresenceInverts:
    """§12.12's `errFlagMutexMemberRequired` is DELETED and its rule inverts.

    A mutex member may not declare requiredness; a member flag now MUST -- read
    as *required once this member is elected* (§21's box, §12.13). In Python
    the member's presence is not declarable at all: `member_value(...)` takes no
    presence keyword, and a frozen dataclass field with no default is required
    by construction, so the non-required state is unconstructable. The template
    is therefore Python-EXCLUDED, exactly as the defaulted-selector
    completeness check is (§24.5).
    """

    def test_the_old_template_is_gone(self):
        assert not hasattr(strictcli, "MutexGroup")
        assert not hasattr(strictcli, "_raise_flag_mutex_member_required")

    def test_a_member_payload_is_required_once_elected(self):
        app = _app()

        @strictcli.choice("profile", help="one named profile")
        class Profile:
            value: str = strictcli.member_value(help="the profile name")

        @strictcli.choice("all-profiles", help="every profile")
        class AllProfiles:
            pass

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.choice_flag(
            "mode", help="which profiles", presence="required",
            elect_by="member-flags", choices=[Profile, AllProfiles],
        )
        def cmd(ctx, mode: Profile | AllProfiles):
            print(repr(mode))

        r = app.test(["cmd", "--profile"])
        assert r.exit_code == 1
        assert "error: flag '--profile' requires a value\n" in r.stderr
        assert app.test(["cmd", "--profile", "work"]).exit_code == 0


class TestHandlerParameterCheck:
    def test_optional_flag_bound_to_a_sentinel_default_is_refused(self):
        app = _app()
        with pytest.raises(ValueError) as exc:

            @app.command("cmd", effect="read_only", help="a command")
            @strictcli.flag("target", type=str, help="the target", presence="optional")
            def cmd(ctx, target=""):
                pass

        assert str(exc.value) == (
            "command \"cmd\": handler parameter 'target' is bound to optional "
            "flag '--target' and must default to None"
        )

    def test_optional_arg_bound_to_a_sentinel_default_is_refused(self):
        app = _app()
        with pytest.raises(ValueError) as exc:

            @app.command("cmd", effect="read_only", help="a command")
            @strictcli.arg("path", help="the path", presence="optional")
            def cmd(ctx, path=""):
                pass

        assert str(exc.value) == (
            "command \"cmd\": handler parameter 'path' is bound to optional "
            "arg 'path' and must default to None"
        )

    def test_a_none_default_is_accepted(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("target", type=str, help="the target", presence="optional")
        def cmd(ctx, target=None):
            print(f"target={target}")

        assert app.test(["cmd"]).exit_code == 0

    def test_a_parameter_with_no_default_is_accepted(self):
        """There is no per-parameter default to re-sentinelize with."""
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("target", type=str, help="the target", presence="optional")
        def cmd(ctx, target):
            print(f"target={target}")

        r = app.test(["cmd"])
        assert r.exit_code == 0
        assert "target=None" in r.stdout

    def test_a_defaulted_flag_may_bind_any_parameter_default(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("target", type=str, help="the target", default="prod")
        def cmd(ctx, target=""):
            print(f"target={target}")

        assert app.test(["cmd"]).exit_code == 0


# ---------------------------------------------------------------------------
# §23.1 / §23.5 -- what each declaration delivers
# ---------------------------------------------------------------------------


class TestOptionalDelivery:
    def test_optional_scalar_delivers_none(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("target", type=str, help="the target", presence="optional")
        def cmd(ctx, target=None):
            print(f"target={target!r}")

        r = app.test(["cmd"])
        assert r.exit_code == 0
        assert "target=None" in r.stdout

    def test_empty_string_is_a_value_again(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("target", type=str, help="the target", presence="optional")
        def cmd(ctx, target=None):
            print(f"target={target!r}")

        r = app.test(["cmd", "--target", ""])
        assert r.exit_code == 0
        assert "target=''" in r.stdout

    def test_optional_bool_is_real_tri_state(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("loud", type=bool, help="be loud", presence="optional")
        def cmd(ctx, loud=None):
            print(f"loud={loud!r}")

        assert "loud=True" in app.test(["cmd", "--loud"]).stdout
        assert "loud=False" in app.test(["cmd", "--no-loud"]).stdout
        assert "loud=None" in app.test(["cmd"]).stdout

    def test_optional_repeatable_delivers_absence_not_an_empty_list(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "tag", type=list[str], help="a tag", presence="optional", unique=False,
        )
        def cmd(ctx, tag=None):
            print(f"tag={tag!r}")

        assert "tag=None" in app.test(["cmd"]).stdout
        assert "tag=['aa']" in app.test(["cmd", "--tag", "aa"]).stdout

    def test_optional_dict_delivers_absence_not_an_empty_dict(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "header", type=dict[str, str], help="a header", presence="optional",
        )
        def cmd(ctx, header=None):
            print(f"header={header!r}")

        assert "header=None" in app.test(["cmd"]).stdout

    def test_declared_empty_collections_deliver_themselves(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "tag", type=list[str], help="a tag", default=[], unique=False,
        )
        @strictcli.flag(
            "header", type=dict[str, str], help="a header", default={},
        )
        def cmd(ctx, tag, header):
            print(f"tag={tag!r} header={header!r}")

        r = app.test(["cmd"])
        assert r.exit_code == 0
        assert "tag=[] header={}" in r.stdout


class TestRequiredDelivery:
    def test_required_repeatable_needs_at_least_one_occurrence(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "tag", type=list[str], help="a tag", presence="required", unique=False,
        )
        def cmd(ctx, tag):
            print(f"tag={tag!r}")

        r = app.test(["cmd"])
        assert r.exit_code == 1
        assert "flag '--tag' is required" in r.stderr
        assert "tag=['aa']" in app.test(["cmd", "--tag", "aa"]).stdout

    def test_required_dict_needs_at_least_one_key(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "header", type=dict[str, str], help="a header", presence="required",
        )
        def cmd(ctx, header):
            pass

        r = app.test(["cmd"])
        assert r.exit_code == 1
        assert "flag '--header' is required" in r.stderr

    def test_required_bool_must_be_passed(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("loud", type=bool, help="be loud", presence="required")
        def cmd(ctx, loud):
            print(f"loud={loud}")

        r = app.test(["cmd"])
        assert r.exit_code == 1
        assert "flag '--loud' must be passed as --loud or --no-loud" in r.stderr
        assert "loud=False" in app.test(["cmd", "--no-loud"]).stdout


class TestArgDelivery:
    def test_optional_arg_delivers_a_present_key_holding_none(self):
        """Absence arrives as a present kwarg, never as a missing one."""
        seen = {}
        app = _app()

        @app.command(
            "cmd", effect="read_only", help="a command",
            forwarding=strictcli.Forwarding(reason="captures the kwargs verbatim"),
        )
        @strictcli.arg("path", help="the path", presence="optional")
        def cmd(ctx, **kw):
            seen.update(kw)

        assert app.test(["cmd"]).exit_code == 0
        assert "path" in seen
        assert seen["path"] is None

    def test_required_variadic_needs_at_least_one_value(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.arg(
            "files", help="the files", variadic=True, presence="required",
        )
        def cmd(ctx, files):
            print(f"files={files!r}")

        assert app.test(["cmd"]).exit_code == 1
        assert "files=['aa']" in app.test(["cmd", "aa"]).stdout

    def test_optional_variadic_delivers_an_empty_list(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.arg(
            "files", help="the files", variadic=True, presence="optional",
        )
        def cmd(ctx, files=None):
            print(f"files={files!r}")

        r = app.test(["cmd"])
        assert r.exit_code == 0
        assert "files=[]" in r.stdout


# ---------------------------------------------------------------------------
# §23.5 -- composition
# ---------------------------------------------------------------------------


class TestComposition:
    def test_env_satisfies_requiredness(self, monkeypatch):
        app = _app(env_prefix="MYAPP")

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "target", type=str, help="the target", presence="required",
            env="MYAPP_TARGET",
        )
        def cmd(ctx, target):
            print(f"target={target}")

        monkeypatch.setenv("MYAPP_TARGET", "prod")
        r = app.test(["cmd"])
        assert r.exit_code == 0
        assert "target=prod" in r.stdout

    def test_an_implication_satisfies_requiredness(self):
        app = _app()

        @app.command(
            "cmd", effect="read_only", help="a command",
            constraints=[strictcli.Implies(
                "release-implies-signed",
                flag="release", implies="signed", value=True,
            )],
        )
        @strictcli.flag("release", type=bool, help="release", default=False)
        @strictcli.flag("signed", type=bool, help="signed", presence="required")
        def cmd(ctx, release, signed):
            print(f"signed={signed}")

        r = app.test(["cmd", "--release"])
        assert r.exit_code == 0
        assert "signed=True" in r.stdout

    def test_an_implies_trigger_never_fires_from_its_own_default(self):
        app = _app()

        @app.command(
            "cmd", effect="read_only", help="a command",
            constraints=[strictcli.Implies(
                "release-implies-signed",
                flag="release", implies="signed", value=True,
            )],
        )
        @strictcli.flag("release", type=bool, help="release", default=True)
        @strictcli.flag("signed", type=bool, help="signed", presence="optional")
        def cmd(ctx, release, signed=None):
            print(f"signed={signed!r}")

        r = app.test(["cmd"])
        assert r.exit_code == 0
        assert "signed=None" in r.stdout

    def test_a_default_does_not_satisfy_a_dependency(self):
        app = _app()

        @app.command(
            "cmd", effect="read_only", help="a command",
            constraints=[strictcli.Requires(
                "sign-needs-key", flag="sign", depends_on="key",
            )],
        )
        @strictcli.flag("sign", type=bool, help="sign", default=False)
        @strictcli.flag("key", type=str, help="the key", default="builtin")
        def cmd(ctx, sign, key):
            pass

        r = app.test(["cmd", "--sign"])
        assert r.exit_code == 1
        assert (
            'constraint "sign-needs-key": flag \'--sign\' requires \'--key\''
            in r.stderr
        )

    def test_an_all_or_none_member_may_not_declare_required(self):
        # §23.5's `CoRequired`/`required` cell is AMENDED by §26.5: the shape
        # it called "surprising" is the whole objection, and it is now a
        # registration error. A member the invocation must always supply turns
        # all-or-none into "every other member is required too", which already
        # has a spelling -- declare them required.
        a = _app()

        with pytest.raises(ValueError) as exc:

            @a.command(
                "cmd", effect="read_only", help="a command",
                constraints=[strictcli.AllOrNone("tls", [
                    strictcli.Member("cert"), strictcli.Member("key"),
                ])],
            )
            @strictcli.flag("cert", type=str, help="the certificate", presence="required")
            @strictcli.flag("key", type=str, help="the private key", presence="optional")
            def cmd(ctx, cert, key=None):
                pass

        assert str(exc.value) == (
            'command "cmd": constraint "tls" member \'--cert\' declares '
            'presence="required": a member the invocation must always supply '
            "leaves the constraint nothing to decide"
        )

    def test_choices_compose_with_optional_in_both_directions(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "format", type=str, help="format", presence="optional",
            choices=[strictcli.Choice("text"), strictcli.Choice("json")],
        )
        def cmd(ctx, format=None):
            print(f"format={format!r}")

        assert "format=None" in app.test(["cmd"]).stdout
        assert "format='json'" in app.test(["cmd", "--format", "json"]).stdout
        bad = app.test(["cmd", "--format", "xml"])
        assert bad.exit_code == 1
        assert "--format: invalid value 'xml'" in bad.stderr

    def test_an_optional_sub_flag_delivers_absence_as_a_present_field(self):
        """§23 applies again one level down, unchanged (§24.1)."""
        app = _app()

        @strictcli.choice("write", help="write the output")
        class Write:
            output: str = strictcli.sub_flag(help="output", presence="optional")
            target: str = strictcli.sub_flag(help="target", default="prod")

        @strictcli.choice("discard", help="discard the output")
        class Discard:
            pass

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.choice_flag(
            "mode", help="what to do", presence="required",
            elect_by="selector-token", choices=[Write, Discard],
        )
        def cmd(ctx, mode: Write | Discard):
            print(f"output={mode.output!r} target={mode.target!r}")

        r = app.test(["cmd", "--mode", "write", "--output", "out.txt"])
        assert r.exit_code == 0
        assert "output='out.txt' target='prod'" in r.stdout


# ---------------------------------------------------------------------------
# §23.6 -- ctx.provided
# ---------------------------------------------------------------------------


class TestProvided:
    def _probe_app(self, **flag_kwargs):
        app = _app(env_prefix="MYAPP")

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("target", type=str, help="the target", **flag_kwargs)
        def cmd(ctx, target=None):
            print(f"provided={ctx.provided('target')} source={ctx.source('target')}")

        return app

    def test_a_cli_value_is_provided(self):
        app = self._probe_app(presence="optional")
        r = app.test(["cmd", "--target", "prod"])
        assert "provided=True source=cli" in r.stdout

    def test_an_env_value_is_provided(self, monkeypatch):
        app = self._probe_app(presence="optional", env="MYAPP_TARGET")
        monkeypatch.setenv("MYAPP_TARGET", "prod")
        r = app.test(["cmd"])
        assert "provided=True source=env" in r.stdout

    def test_a_declared_default_is_not_provided(self):
        app = self._probe_app(default="prod")
        r = app.test(["cmd"])
        assert "provided=False source=default" in r.stdout

    def test_an_optional_flag_that_received_nothing_is_not_provided(self):
        """It carries source `default`: an optional declaration deciding on
        absence IS the declaration deciding. No seventh label is minted."""
        app = self._probe_app(presence="optional")
        r = app.test(["cmd"])
        assert "provided=False source=default" in r.stdout

    def test_an_implied_value_is_provided(self):
        app = _app()

        @app.command(
            "cmd", effect="read_only", help="a command",
            constraints=[strictcli.Implies(
                "release-implies-signed",
                flag="release", implies="signed", value=True,
            )],
        )
        @strictcli.flag("release", type=bool, help="release", default=False)
        @strictcli.flag("signed", type=bool, help="signed", presence="optional")
        def cmd(ctx, release, signed=None):
            print(f"provided={ctx.provided('signed')} source={ctx.source('signed')}")

        r = app.test(["cmd", "--release"])
        assert "provided=True source=implied" in r.stdout

    def test_an_infra_default_is_not_provided(self, monkeypatch):
        app = _app(infra_root={"MYAPP_HOME": "~/.myapp"})

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "state", type=str, help="the state dir",
            default=strictcli.RelativeToRoot("MYAPP_HOME", "state"),
        )
        def cmd(ctx, state):
            print(f"provided={ctx.provided('state')} source={ctx.source('state')}")

        r = app.test(["cmd"])
        assert "provided=False source=infra" in r.stdout

    def test_a_dashed_name_is_accepted(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("dry-mode", type=bool, help="dry", presence="optional")
        def cmd(ctx, dry_mode=None):
            print(f"provided={ctx.provided('dry-mode')}")

        assert "provided=True" in app.test(["cmd", "--dry-mode"]).stdout

    def test_an_unknown_name_raises_the_source_error(self):
        seen = {}
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("target", type=str, help="the target", presence="optional")
        def cmd(ctx, target=None):
            try:
                ctx.provided("nope")
            except KeyError as e:
                seen["msg"] = e.args[0]

        app.test(["cmd"])
        assert seen["msg"] == "no source info for flag 'nope'"''


# ---------------------------------------------------------------------------
# §13's amendment -- the dumped schema
# ---------------------------------------------------------------------------


class TestSchemaPresence:
    def _dump(self, build, tmp_path, monkeypatch):
        """The help document of a freshly built app."""
        app = build()
        r = app.test(["help", "--json"])
        assert r.exit_code == 0, r.stderr
        return json.loads(r.stdout)

    def test_every_flag_and_arg_entry_carries_presence(self, tmp_path, monkeypatch):
        def build():
            app = _app()

            @app.command("cmd", effect="read_only", help="a command")
            @strictcli.flag("aa", type=str, help="aa", presence="required")
            @strictcli.flag("bb", type=str, help="bb", presence="optional")
            @strictcli.flag("cc", type=str, help="cc", default="xx")
            @strictcli.arg("pp", help="pp", presence="required")
            def cmd(ctx, aa, bb, cc, pp):
                pass

            return app

        data = self._dump(build, tmp_path, monkeypatch)
        flags = {f["name"]: f for f in data["commands"]["cmd"]["flags"]}
        assert flags["aa"]["presence"] == "required"
        assert "default" not in flags["aa"]
        assert flags["bb"]["presence"] == "optional"
        assert "default" not in flags["bb"]
        assert flags["cc"]["presence"] == "default"
        assert flags["cc"]["default"] == "xx"
        arg = data["commands"]["cmd"]["args"][0]
        assert arg["presence"] == "required"
        assert "required" not in arg

    def test_empty_and_falsey_defaults_are_emitted(self, tmp_path, monkeypatch):
        def build():
            app = _app()

            @app.command("cmd", effect="read_only", help="a command")
            @strictcli.flag("tag", type=list[str], help="tags", default=[], unique=False)
            @strictcli.flag("header", type=dict[str, str], help="headers", default={})
            @strictcli.flag("name", type=str, help="name", default="")
            @strictcli.flag("count", type=int, help="count", default=0)
            @strictcli.flag("loud", type=bool, help="loud", default=False)
            def cmd(ctx, tag, header, name, count, loud):
                pass

            return app

        data = self._dump(build, tmp_path, monkeypatch)
        flags = {f["name"]: f for f in data["commands"]["cmd"]["flags"]}
        assert flags["tag"]["default"] == []
        assert flags["header"]["default"] == {}
        assert flags["name"]["default"] == ""
        assert flags["count"]["default"] == 0
        assert flags["loud"]["default"] is False
        for f in flags.values():
            assert f["presence"] == "default"


# ---------------------------------------------------------------------------
# §23.8 -- help rendering
# ---------------------------------------------------------------------------


class TestHelpMarkers:
    def _help(self, app):
        r = app.test(["cmd", "--help"])
        assert r.exit_code == 0
        return r.stdout

    def test_one_marker_per_flag_line_and_it_is_last(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "format", type=str, help="the format", presence="optional",
            choices=[strictcli.Choice("text"), strictcli.Choice("json")], env="FMT", prefixed=False,
        )
        def cmd(ctx, format=None):
            pass

        line = next(
            ln for ln in self._help(app).splitlines() if "--format" in ln
        )
        assert line.endswith("[optional]")
        assert line.count("[optional]") == 1
        assert "[required]" not in line
        assert "[default" not in line

    def test_python_gains_the_optional_marker(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("target", type=str, help="the target", presence="optional")
        def cmd(ctx, target=None):
            pass

        assert "[optional]" in self._help(app)

    def test_declared_empty_collections_render_their_literal(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("tag", type=list[str], help="tags", default=[], unique=False)
        @strictcli.flag("header", type=dict[str, str], help="headers", default={})
        def cmd(ctx, tag, header):
            pass

        out = self._help(app)
        assert "[default: []]" in out
        assert "[default: {}]" in out

    def test_a_required_positional_renders_required(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.arg("path", help="the path", presence="required")
        def cmd(ctx, path):
            pass

        line = next(ln for ln in self._help(app).splitlines() if "path" in ln)
        assert line.endswith("[required]")

    def test_an_optional_positional_renders_optional(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.arg("path", help="the path", presence="optional")
        def cmd(ctx, path=None):
            pass

        assert "[optional]" in self._help(app)

    @pytest.mark.parametrize(
        "declared_type,value,rendered",
        [
            (bool, True, "true"),
            (bool, False, "false"),
            (int, 7, "7"),
            (float, 1.5, "1.5"),
            (float, 1e-7, "1e-7"),
            (float, 2.0, "2.0"),
            (str, "prod", "prod"),
        ],
    )
    def test_the_same_default_renders_the_same_on_an_arg_and_a_flag(
        self, declared_type, value, rendered,
    ):
        """One value, one rendering, whichever surface declared it (§23.8).

        The positional side used to render a bool default through ``str()``,
        so `[default: True]` faced a flag's `[default: true]` on the same
        help page.
        """
        app = _app()

        @app.command(
            "cmd", effect="read_only", help="a command",
            args=[
                strictcli.Arg(
                    name="pos", help="the positional",
                    type=declared_type, default=value,
                ),
            ],
        )
        @strictcli.flag(
            "opt", type=declared_type, help="the flag", default=value,
        )
        def cmd(ctx, pos, opt):
            pass

        lines = self._help(app).splitlines()
        arg_line = next(ln for ln in lines if ln.strip().startswith("pos "))
        flag_line = next(ln for ln in lines if "--opt" in ln)
        assert arg_line.endswith(f"[default: {rendered}]")
        assert flag_line.endswith(f"[default: {rendered}]")

    def test_a_dict_default_renders_sorted_pairs_inside_the_marker(self):
        """The whole bracketed part, not just the pairs (§23.8)."""
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "header", type=dict[str, int], help="headers",
            default={"zebra": 3, "apple": 1, "mango": 2},
        )
        def cmd(ctx, header):
            pass

        line = next(
            ln for ln in self._help(app).splitlines() if "--header" in ln
        )
        assert line.endswith("[default: apple=1, mango=2, zebra=3]")

    def test_a_list_default_renders_its_elements_inside_the_marker(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "tag", type=list[str], help="tags", default=["xx", "yy"], unique=False,
        )
        def cmd(ctx, tag):
            pass

        line = next(ln for ln in self._help(app).splitlines() if "--tag" in ln)
        assert line.endswith("[default: xx, yy]")

    def test_declared_empty_collections_render_the_whole_marker(self):
        """`[default: []]` / `[default: {}]`, brackets included (§23.8)."""
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("tag", type=list[str], help="tags", default=[], unique=False)
        @strictcli.flag("header", type=dict[str, str], help="headers", default={})
        def cmd(ctx, tag, header):
            pass

        lines = self._help(app).splitlines()
        assert next(
            ln for ln in lines if "--tag" in ln
        ).endswith("[default: []]")
        assert next(
            ln for ln in lines if "--header" in ln
        ).endswith("[default: {}]")


# ---------------------------------------------------------------------------
# §23.7 -- the MCP / tool projection
# ---------------------------------------------------------------------------


class TestToolSchemaRequiredness:
    def test_requiredness_is_read_off_the_declaration(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("aa", type=str, help="aa", presence="required")
        @strictcli.flag("bb", type=str, help="bb", presence="optional")
        @strictcli.flag("cc", type=str, help="cc", default="xx")
        @strictcli.arg("pp", help="pp", presence="required")
        @strictcli.arg("qq", help="qq", presence="optional")
        def cmd(ctx, aa, bb, cc, pp, qq=None):
            pass

        schema = app.json_schema("cmd")
        assert schema["required"] == ["aa", "pp"]

    def test_a_required_bool_is_in_the_required_array(self):
        """Bools were excluded on the reasoning that they always have a
        default, which the presence declaration makes false."""
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag("loud", type=bool, help="be loud", presence="required")
        @strictcli.flag("quiet-mode", type=bool, help="quiet", default=False)
        def cmd(ctx, loud, quiet_mode):
            pass

        schema = app.json_schema("cmd")
        assert schema["required"] == ["loud"]

    def test_a_required_compound_flag_is_in_the_required_array(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "tag", type=list[str], help="tags", presence="required", unique=False,
        )
        def cmd(ctx, tag):
            pass

        schema = app.json_schema("cmd")
        assert schema["required"] == ["tag"]
