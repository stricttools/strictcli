"""The update-command construct (contract §27, §12.16).

The fixture is the contract's own worked example -- a sparse DNS-record update
with two identity members and three properties, one of them nullable and one of
them a bool -- so every rendering below is checked against the declaration the
contract renders in its own text.
"""

import json

import pytest

import strictcli
from strictcli import UpdateOf, choice, choice_flag, sub_flag


def _update_app(handler=None, **cmd_kwargs):
    app = strictcli.App(name="dnsapp", version="1.0.0", help="manage DNS")

    def _default_handler(ctx, zone, record_id, content, ttl, proxied):
        return 0

    kwargs = {
        "help": "change one DNS record in place",
        "effect": "mutating",
        "update_of": UpdateOf(
            "dns-record", write_mode="sparse",
            identity=["zone", "record-id"],
            properties=["content", "ttl", "proxied"],
        ),
    }
    kwargs.update(cmd_kwargs)

    @app.command("update-record", **kwargs)
    @strictcli.flag("zone", type=str, help="zone the record belongs to",
                    presence="required")
    @strictcli.flag("record-id", type=str,
                    help="identifier of the record to change",
                    presence="required")
    @strictcli.flag("content", type=str, help="record content",
                    presence="optional")
    @strictcli.flag("ttl", type=int, help="time to live in seconds",
                    presence="optional", nullable=True)
    @strictcli.flag("proxied", type=bool, help="whether the record is proxied",
                    presence="optional")
    def update_record(ctx, zone, record_id, content, ttl, proxied):
        return (handler or _default_handler)(
            ctx, zone, record_id, content, ttl, proxied,
        )

    return app


# ---------------------------------------------------------------------------
# §27.1 -- the mutating-default ban
# ---------------------------------------------------------------------------


def test_the_ban_refuses_a_flags_value_default():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating")
        @strictcli.flag("ttl", type=int, help="time to live", default=300)
        def _u(ctx, ttl):
            return 0

    assert str(exc.value) == (
        'command "uu": flag \'--ttl\' declares default=300 on a mutating '
        "command: absence would write a value the invocation never stated "
        '(declare presence="required" or presence="optional", or apply the '
        "fallback in the handler and say so in its help)"
    )


def test_the_ban_refuses_a_positional_args_value_default():
    """The presence spellings inside the sentence take the FLAG spelling even
    when the subject is an arg (§12.16): the prefix names the command rather
    than a surface."""
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating")
        @strictcli.arg("target", help="where", default="prod")
        def _u(ctx, target):
            return 0

    assert str(exc.value).startswith(
        'command "uu": argument \'target\' declares default=prod on a '
        "mutating command"
    )


@pytest.mark.parametrize(
    ("kwargs", "want"),
    [
        ({"type": str, "default": ""}, "flag '--xx' declares default= on a"),
        ({"type": int, "default": 0}, "flag '--xx' declares default=0 on a"),
        ({"type": bool, "default": False},
         "flag '--xx' declares default=false on a"),
        ({"type": bool, "default": True},
         "flag '--xx' declares default=true on a"),
        ({"type": float, "default": 1.5},
         "flag '--xx' declares default=1.5 on a"),
    ],
)
def test_the_ban_reaches_every_scalar_including_the_empty_ones(kwargs, want):
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating")
        @strictcli.flag("xx", help="a value", **kwargs)
        def _u(ctx, xx):
            return 0

    assert want in str(exc.value)


def test_the_ban_reaches_a_non_empty_compound():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating")
        @strictcli.flag("tag", type=list[str], help="tags", default=["aa"])
        def _u(ctx, tag):
            return 0

    assert "flag '--tag' declares default=['aa'] on a mutating command" in str(
        exc.value,
    )


def test_an_empty_collection_declares_no_elements():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update", effect="mutating")
    @strictcli.flag("tag", type=list[str], help="tags", default=[])
    @strictcli.flag("header", type=dict[str, str], help="headers", default={})
    def _u(ctx, tag, header):
        return 0


def test_a_relative_to_root_default_decides_where_never_what():
    app = strictcli.App(
        name="tt", version="1.0.0", help="tt", infra_root={"T_HOME": "~/.t"},
    )

    @app.command("uu", help="update", effect="mutating")
    @strictcli.flag("path", type=str, help="a path",
                    default=strictcli.RelativeToRoot("T_HOME", "store"))
    def _u(ctx, path):
        return 0


def test_a_read_only_command_writes_no_value_invented_or_otherwise():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("rr", help="read", effect="read_only")
    @strictcli.flag("ttl", type=int, help="ttl", default=300)
    def _r(ctx, ttl):
        return 0


def test_an_app_level_global_is_not_reached():
    app = strictcli.App(
        name="tt", version="1.0.0", help="tt",
        flags=[strictcli.Flag(name="depth", type=int, help="depth", default=3)],
    )

    @app.command("uu", help="update", effect="mutating")
    def _u(ctx, depth):
        return 0


def test_the_ban_reaches_a_flag_sets_flag():
    """A shared flag set carrying a default is legal; ATTACHING it to a
    mutating command is not -- the ban is evaluated per command (§18.33 item
    302)."""
    shared = strictcli.FlagSet(
        name="shared",
        flags=[strictcli.Flag(name="ttl", type=int, help="time to live",
                              default=300)],
    )
    read_only = strictcli.App(name="tt", version="1.0.0", help="tt")

    @read_only.command("rr", help="read", effect="read_only", flag_sets=[shared])
    def _r(ctx, ttl):
        return 0

    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating", flag_sets=[shared])
        def _u(ctx, ttl):
            return 0

    assert "flag '--ttl' declares default=300 on a mutating command" in str(
        exc.value,
    )


@choice("webhook", help="post to a URL")
class _Webhook:
    retries: int = sub_flag(help="attempts", default=3)


@choice("email", help="send an email")
class _Email:
    subject: str = sub_flag(help="subject", presence="optional")


@choice("webhook-ok", help="post to a URL")
class _WebhookOk:
    retries: int = sub_flag(help="attempts", presence="optional")


def test_the_ban_spares_the_selector_and_reaches_its_scope():
    """A choice name is not a value written to anything: it names which scope
    is live. The flags INSIDE the scope are ordinary flags of a mutating
    command, reached at every depth."""
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating")
        @choice_flag("via", help="channel", elect_by="selector-token",
                     choices=[_Webhook, _Email], default=_Webhook())
        def _u(ctx, via: _Webhook | _Email):
            return 0

    assert "flag '--retries' declares default=3 on a mutating command" in str(
        exc.value,
    )

    ok = strictcli.App(name="tt", version="1.0.0", help="tt")

    @ok.command("uu", help="update", effect="mutating")
    @choice_flag("via", help="channel", elect_by="selector-token",
                 choices=[_WebhookOk, _Email],
                 default=_WebhookOk(retries=None))
    def _ok(ctx, via: _WebhookOk | _Email):
        return 0


def test_a_defaulted_selections_instance_passes_no_field_values():
    """Python's instance-shaped default is the one surface that can write a
    field value into a default election, and that is a value default under
    another spelling (§18.33 item 303)."""
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating")
        @choice_flag("via", help="channel", elect_by="selector-token",
                     choices=[_WebhookOk, _Email],
                     default=_WebhookOk(retries=5))
        def _u(ctx, via: _WebhookOk | _Email):
            return 0

    assert "flag '--retries' declares default=5 on a mutating command" in str(
        exc.value,
    )


# ---------------------------------------------------------------------------
# §27.2, §27.3, §27.11 -- the registration guards, in the pinned order
# ---------------------------------------------------------------------------


def test_update_of_on_a_read_only_command_is_refused():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="read_only",
                     update_of=UpdateOf("thing", write_mode="sparse",
                                        properties=["content"]))
        @strictcli.flag("content", type=str, help="content", presence="optional")
        def _u(ctx, content):
            return 0

    assert str(exc.value) == (
        'command "uu": a read_only command cannot declare update_of '
        "(a command that changes nothing writes no properties)"
    )


def test_the_write_mode_vocabulary():
    """Python's reachable input is a string outside the vocabulary (§12.16)."""
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("thing", write_mode="patch",
                                        properties=["content"]))
        @strictcli.flag("content", type=str, help="content", presence="optional")
        def _u(ctx, content):
            return 0

    assert str(exc.value) == (
        'command "uu": invalid write_mode "patch": must be "sparse" or '
        '"full_replace"'
    )


def test_the_write_mode_has_no_default_at_the_declaration_site():
    """It carries no default, so omitting it is Python's own TypeError."""
    with pytest.raises(TypeError):
        UpdateOf("thing", properties=["content"])  # type: ignore[call-arg]


def test_the_resource_name_charset():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("DNS_Record", write_mode="sparse",
                                        properties=["content"]))
        @strictcli.flag("content", type=str, help="content", presence="optional")
        def _u(ctx, content):
            return 0

    assert str(exc.value) == (
        'command "uu": update resource "DNS_Record" must be lowercase kebab-case of at least two characters: [a-z][a-z0-9]*(-[a-z0-9]+)*'
    )


def test_an_update_with_no_properties_is_refused():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("thing", write_mode="sparse",
                                        identity=["id"]))
        @strictcli.flag("id", type=str, help="id", presence="required")
        def _u(ctx, id):
            return 0

    assert str(exc.value) == (
        'command "uu": update of "thing" declares no properties: an update '
        "with nothing to write is not an update"
    )


def test_an_unknown_name_is_refused():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("thing", write_mode="sparse",
                                        properties=["nope"]))
        @strictcli.flag("content", type=str, help="content", presence="optional")
        def _u(ctx, content):
            return 0

    assert str(exc.value) == (
        'command "uu": update of "thing" references unknown name "nope"'
    )


def test_an_ambiguous_name_is_refused():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("thing", write_mode="sparse",
                                        identity=["target"],
                                        properties=["content"]))
        @strictcli.flag("target", type=str, help="a flag", presence="optional")
        @strictcli.flag("content", type=str, help="content", presence="optional")
        @strictcli.arg("target", help="an arg", presence="optional")
        def _u(ctx, target, content):
            return 0

    assert str(exc.value) == (
        'command "uu": update of "thing" references "target", which names both '
        "a flag and a positional arg"
    )


def test_a_duplicated_name_is_refused():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("thing", write_mode="sparse",
                                        properties=["content", "content"]))
        @strictcli.flag("content", type=str, help="content", presence="optional")
        def _u(ctx, content):
            return 0

    assert str(exc.value) == (
        'command "uu": update of "thing" declares "content" twice'
    )


def test_a_name_in_both_roles_is_refused():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("thing", write_mode="sparse",
                                        identity=["content"],
                                        properties=["content"]))
        @strictcli.flag("content", type=str, help="content", presence="optional")
        def _u(ctx, content):
            return 0

    assert str(exc.value) == (
        'command "uu": update of "thing" declares "content" as both identity '
        "and property"
    )


@choice("email", help="by email")
class _ByEmail:
    subject: str = sub_flag(help="subject", presence="optional")


@choice("sms", help="by sms")
class _BySms:
    number: str = sub_flag(help="number", presence="optional")


def test_a_scoped_name_names_its_actual_fault():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("thing", write_mode="sparse",
                                        properties=["subject"]))
        @choice_flag("via", help="channel", presence="required",
                     elect_by="selector-token", choices=[_ByEmail, _BySms])
        def _u(ctx, via: _ByEmail | _BySms):
            return 0

    assert str(exc.value) == (
        'command "uu": update of "thing" references \'subject\', which is '
        "declared under '--via email': an update's identity and properties "
        "are declared at root scope only"
    )


def test_a_property_may_not_be_a_positional_arg():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("thing", write_mode="sparse",
                                        properties=["content"]))
        @strictcli.arg("content", help="content", presence="optional")
        def _u(ctx, content):
            return 0

    assert str(exc.value) == (
        'command "uu": update of "thing" property "content" is a positional '
        "arg: a property must be individually omissible and clearable, and "
        "only a flag is"
    )


def test_a_property_may_not_be_a_choice_flag():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("thing", write_mode="sparse",
                                        properties=["via"]))
        @choice_flag("via", help="channel", presence="required",
                     elect_by="selector-token", choices=[_ByEmail, _BySms])
        def _u(ctx, via: _ByEmail | _BySms):
            return 0

    assert str(exc.value) == (
        'command "uu": update of "thing" property \'--via\' is a choice flag: '
        "an elected record is a selection, not a property value"
    )


def test_a_property_declares_optional_and_nothing_else():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("thing", write_mode="sparse",
                                        properties=["content"]))
        @strictcli.flag("content", type=str, help="content", presence="required")
        def _u(ctx, content):
            return 0

    assert str(exc.value) == (
        'command "uu": update of "thing" property flag \'--content\' declares '
        'presence="required": a property is absent exactly when it is not '
        "being written, and the presence declaration for that is "
        'presence="optional"'
    )


@choice("by-id", help="address it by id")
class _ById:
    pass


@choice("by-name", help="address it by name")
class _ByName:
    pass


def test_identity_may_be_an_arg_or_a_choice_flag_and_may_be_optional():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update", effect="mutating",
                 update_of=UpdateOf(
                     "thing", write_mode="sparse",
                     identity=["id", "addressing", "name"],
                     properties=["content"],
                 ))
    @choice_flag("addressing", help="how the resource is addressed",
                 presence="required", elect_by="selector-token",
                 choices=[_ById, _ByName])
    @strictcli.flag("name", type=str, help="the name", presence="optional")
    @strictcli.flag("content", type=str, help="content", presence="optional")
    @strictcli.arg("id", help="the id", presence="required")
    def _u(ctx, addressing: _ById | _ByName, name, content, id):
        return 0


def test_nullable_is_refused_off_a_property():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating")
        @strictcli.flag("content", type=str, help="content",
                        presence="optional", nullable=True)
        def _u(ctx, content):
            return 0

    assert str(exc.value) == (
        'command "uu": flag \'--content\' declares nullable=True but is not a '
        "property of an update: only a property can be cleared"
    )

    app2 = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc2:

        @app2.command("uu", help="update", effect="mutating",
                      update_of=UpdateOf("thing", write_mode="sparse",
                                         identity=["zone"],
                                         properties=["content"]))
        @strictcli.flag("zone", type=str, help="zone", presence="optional",
                        nullable=True)
        @strictcli.flag("content", type=str, help="content", presence="optional")
        def _u2(ctx, zone, content):
            return 0

    assert "flag '--zone' declares nullable=True but is not a property" in str(
        exc2.value,
    )


def test_the_unset_name_is_reserved():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("thing", write_mode="sparse",
                                        properties=["content"]))
        @strictcli.flag("content", type=str, help="content",
                        presence="optional", nullable=True)
        @strictcli.flag("unset-content", type=str, help="a flag of that name",
                        presence="optional")
        def _u(ctx, content, unset_content):
            return 0

    assert str(exc.value) == (
        'command "uu": flag name "unset-content" is reserved: property '
        "'--content' declares nullable=True, which mints '--unset-content'"
    )


def test_the_unset_name_reservation_reaches_the_apps_globals():
    """A global is recognized after the command name too, so a global of the
    minted name would be unreachable behind the clear spelling."""
    app = strictcli.App(
        name="tt", version="1.0.0", help="tt",
        flags=[strictcli.Flag(name="unset-content", type=str,
                              help="a global of that name",
                              presence="optional")],
    )
    with pytest.raises(ValueError) as exc:

        @app.command("uu", help="update", effect="mutating",
                     update_of=UpdateOf("thing", write_mode="sparse",
                                        properties=["content"]))
        @strictcli.flag("content", type=str, help="content",
                        presence="optional", nullable=True)
        def _u(ctx, content, unset_content):
            return 0

    assert str(exc.value) == (
        'command "uu": flag name "unset-content" is reserved: property '
        "'--content' declares nullable=True, which mints '--unset-content'"
    )


class TestTheRegistrationOrder:
    """The pinned order matters only where one declaration carries two
    faults (§27.11)."""

    def test_the_ban_runs_ahead_of_every_update_step(self):
        app = strictcli.App(name="tt", version="1.0.0", help="tt")
        with pytest.raises(ValueError) as exc:

            @app.command("uu", help="update", effect="mutating",
                         update_of=UpdateOf("BAD-NAME", write_mode="sparse",
                                            properties=["nope"]))
            @strictcli.flag("ttl", type=int, help="ttl", default=300)
            def _u(ctx, ttl):
                return 0

        assert "flag '--ttl' declares default=300 on a mutating command" in str(
            exc.value,
        )

    def test_classification_runs_ahead_of_record_legality(self):
        app = strictcli.App(name="tt", version="1.0.0", help="tt")
        with pytest.raises(ValueError) as exc:

            @app.command("uu", help="update", effect="read_only",
                         update_of=UpdateOf("BAD-NAME", write_mode="sparse",
                                            properties=["nope"]))
            def _u(ctx):
                return 0

        assert "a read_only command cannot declare update_of" in str(exc.value)

    def test_the_records_legality_runs_ahead_of_the_names_it_carries(self):
        app = strictcli.App(name="tt", version="1.0.0", help="tt")
        with pytest.raises(ValueError) as exc:

            @app.command("uu", help="update", effect="mutating",
                         update_of=UpdateOf("BAD-NAME", write_mode="sparse",
                                            properties=["nope"]))
            def _u(ctx):
                return 0

        assert 'update resource "BAD-NAME" must be lowercase kebab-case' in str(exc.value)

    def test_role_legality_runs_ahead_of_presence_legality(self):
        app = strictcli.App(name="tt", version="1.0.0", help="tt")
        with pytest.raises(ValueError) as exc:

            @app.command("uu", help="update", effect="mutating",
                         update_of=UpdateOf("thing", write_mode="sparse",
                                            properties=["content"]))
            @strictcli.arg("content", help="content", presence="required")
            def _u(ctx, content):
                return 0

        assert 'property "content" is a positional arg' in str(exc.value)

    def test_the_name_reservation_runs_last(self):
        """The nullable-off-a-property refusal is step 8's first half and the
        reservation its second, so a declaration carrying both reports the
        first."""
        app = strictcli.App(name="tt", version="1.0.0", help="tt")
        with pytest.raises(ValueError) as exc:

            @app.command("uu", help="update", effect="mutating",
                         update_of=UpdateOf("thing", write_mode="sparse",
                                            properties=["content"]))
            @strictcli.flag("zone", type=str, help="zone", presence="optional",
                            nullable=True)
            @strictcli.flag("content", type=str, help="content",
                            presence="optional", nullable=True)
            @strictcli.flag("unset-content", type=str, help="collides",
                            presence="optional")
            def _u(ctx, zone, content, unset_content):
                return 0

        assert "flag '--zone' declares nullable=True but is not a property" in (
            str(exc.value)
        )


# ---------------------------------------------------------------------------
# §27.4 -- the at-least-one-property rule
# ---------------------------------------------------------------------------


def test_at_least_one_property_is_required():
    r = _update_app().test(
        ["update-record", "--zone", "z1", "--record-id", "r7"],
    )
    assert r.exit_code == 1
    assert r.stderr.startswith(
        'error: update "dns-record": at least one property is required: '
        "--content, --ttl, --proxied\n"
    )


def test_a_negated_bool_property_is_a_provision_and_not_a_decline():
    """Inside an update command `--no-proxied` WRITES false, so it satisfies
    the rule rather than declining it -- which is why §12.16 pins that the
    sentence carries no decline clause."""
    seen = {}

    def _handler(ctx, zone, record_id, content, ttl, proxied):
        seen["proxied"] = proxied
        seen["provided"] = ctx.provided("proxied")
        return 0

    r = _update_app(_handler).test(
        ["update-record", "--zone", "z1", "--record-id", "r7", "--no-proxied"],
    )
    assert r.exit_code == 0
    assert seen == {"proxied": False, "provided": True}


def test_an_env_provided_property_satisfies_the_rule(monkeypatch):
    """There is no source filter: the framework has exactly one definition of
    "was this supplied" (§23.6)."""
    monkeypatch.setenv("T_CONTENT", "from-env")
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update", effect="mutating",
                 update_of=UpdateOf("thing", write_mode="sparse",
                                    properties=["content", "ttl"]))
    @strictcli.flag("content", type=str, help="content", presence="optional",
                    env="T_CONTENT")
    @strictcli.flag("ttl", type=int, help="ttl", presence="optional")
    def _u(ctx, content, ttl):
        return 0

    r = app.test(["--json", "uu"])
    assert r.exit_code == 0
    assert json.loads(r.stdout)["writes"]["written"] == ["content"]


def test_an_implied_property_is_a_provision():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update", effect="mutating",
                 update_of=UpdateOf("thing", write_mode="sparse",
                                    properties=["proxied"]),
                 constraints=[strictcli.Implies(
                     "secure-proxies", "secure", "proxied", True)])
    @strictcli.flag("secure", type=bool, help="turn on the secure mode",
                    presence="optional")
    @strictcli.flag("proxied", type=bool, help="whether the record is proxied",
                    presence="optional")
    def _u(ctx, secure, proxied):
        return 0

    r = app.test(["--json", "uu", "--secure"])
    assert json.loads(r.stdout)["writes"]["written"] == ["proxied"]


# ---------------------------------------------------------------------------
# §27.6 -- the clear vocabulary
# ---------------------------------------------------------------------------


def test_an_unset_delivers_absence_and_reports_provided():
    seen = {}

    def _handler(ctx, zone, record_id, content, ttl, proxied):
        seen["ttl"] = ttl
        seen["provided"] = ctx.provided("ttl")
        seen["unset"] = ctx.unset("ttl")
        seen["untouched_unset"] = ctx.unset("content")
        return 0

    r = _update_app(_handler).test(
        ["update-record", "--zone", "z1", "--record-id", "r7", "--unset-ttl"],
    )
    assert r.exit_code == 0
    assert seen == {
        "ttl": None, "provided": True, "unset": True, "untouched_unset": False,
    }


def test_unset_accepts_dashed_and_underscored_names():
    seen = {}
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update", effect="mutating",
                 update_of=UpdateOf("thing", write_mode="sparse",
                                    properties=["phone-number"]))
    @strictcli.flag("phone-number", type=str, help="the number",
                    presence="optional", nullable=True)
    def _u(ctx, phone_number):
        seen["dashed"] = ctx.unset("phone-number")
        seen["underscored"] = ctx.unset("phone_number")
        return 0

    r = app.test(["uu", "--unset-phone-number"])
    assert r.exit_code == 0
    assert seen == {"dashed": True, "underscored": True}


def test_unset_on_an_unknown_name_raises_like_provided():
    def _handler(ctx, zone, record_id, content, ttl, proxied):
        ctx.unset("nope")
        return 0

    with pytest.raises(KeyError) as exc:
        _update_app(_handler).test(
            ["update-record", "--zone", "z1", "--record-id", "r7",
             "--content", "xx"],
        )
    assert "nope" in str(exc.value)


def test_value_and_unset_together_is_a_parse_error():
    r = _update_app().test(
        ["update-record", "--zone", "z1", "--record-id", "r7",
         "--ttl", "300", "--unset-ttl"],
    )
    assert r.exit_code == 1
    assert r.stderr.startswith(
        "error: --ttl and --unset-ttl are mutually exclusive: a property is "
        "either written or cleared\n"
    )


def test_the_minted_unset_flag_is_not_negatable():
    r = _update_app().test(
        ["update-record", "--zone", "z1", "--record-id", "r7",
         "--no-unset-ttl"],
    )
    assert r.exit_code == 1
    assert "unknown flag '--no-unset-ttl'" in r.stderr


def test_an_unset_on_a_non_nullable_property_names_nothing():
    r = _update_app().test(
        ["update-record", "--zone", "z1", "--record-id", "r7",
         "--unset-content"],
    )
    assert r.exit_code == 1
    assert "unknown flag '--unset-content'" in r.stderr


def test_the_empty_string_is_an_ordinary_value():
    seen = {}

    def _handler(ctx, zone, record_id, content, ttl, proxied):
        seen["content"] = content
        return 0

    r = _update_app(_handler).test(
        ["update-record", "--zone", "z1", "--record-id", "r7", "--content", ""],
    )
    assert r.exit_code == 0
    assert seen == {"content": ""}


def test_clearing_a_bool_property():
    """Every type may be nullable: clearing is a fact about the resource's
    field, not about the value's shape (§27.6)."""
    seen = {}
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update", effect="mutating",
                 update_of=UpdateOf("thing", write_mode="sparse",
                                    properties=["proxied"]))
    @strictcli.flag("proxied", type=bool, help="whether the record is proxied",
                    presence="optional", nullable=True)
    def _u(ctx, proxied):
        seen["proxied"] = proxied
        seen["unset"] = ctx.unset("proxied")
        return 0

    r = app.test(["uu", "--unset-proxied"])
    assert r.exit_code == 0
    assert seen == {"proxied": None, "unset": True}


# ---------------------------------------------------------------------------
# §27.5 -- the write set and its two renderings
# ---------------------------------------------------------------------------


_DRY_HEADER = "DRY RUN — no changes were made. Would do:\n"


def test_the_write_set_line_takes_no_sequence_number():
    def _handler(ctx, zone, record_id, content, ttl, proxied):
        ctx.effects.http(
            "PATCH", "https://api.example.com/zones/z1/dns_records/r7",
        )
        return 0

    r = _update_app(_handler).test(
        ["--dry-run", "update-record", "--zone", "z1", "--record-id", "r7",
         "--content", "1.2.3.4", "--unset-ttl"],
    )
    assert r.stdout == (
        _DRY_HEADER
        + "  writes: content; clears: ttl (other properties unchanged)\n"
        + "  1. net: PATCH https://api.example.com/zones/z1/dns_records/r7\n"
    )


@pytest.mark.parametrize(
    ("argv", "mode", "want"),
    [
        (["--content", "xx"], "sparse",
         "  writes: content (other properties unchanged)"),
        (["--ttl", "5", "--content", "xx"], "sparse",
         "  writes: content, ttl (other properties unchanged)"),
        (["--content", "xx", "--unset-ttl"], "sparse",
         "  writes: content; clears: ttl (other properties unchanged)"),
        (["--unset-ttl"], "sparse",
         "  clears: ttl (other properties unchanged)"),
        (["--content", "xx"], "full_replace",
         "  writes: content (other properties are re-sent as read)"),
    ],
)
def test_the_write_set_lines_pinned_forms(argv, mode, want):
    app = strictcli.App(name="dnsapp", version="1.0.0", help="manage DNS")

    @app.command("update-record", help="change one DNS record in place",
                 effect="mutating",
                 update_of=UpdateOf("dns-record", write_mode=mode,
                                    properties=["content", "ttl", "proxied"]))
    @strictcli.flag("content", type=str, help="record content",
                    presence="optional")
    @strictcli.flag("ttl", type=int, help="time to live in seconds",
                    presence="optional", nullable=True)
    @strictcli.flag("proxied", type=bool, help="whether the record is proxied",
                    presence="optional")
    def _u(ctx, content, ttl, proxied):
        return 0

    r = app.test(["--dry-run", "update-record", *argv])
    assert r.stdout == _DRY_HEADER + want + "\n"


def test_the_write_set_line_renders_in_dry_mode_only():
    r = _update_app().test(
        ["update-record", "--zone", "z1", "--record-id", "r7", "--content", "xx"],
    )
    assert "writes:" not in r.stdout


@pytest.mark.parametrize("dry", [False, True])
def test_the_envelope_carries_the_write_set_in_both_modes(dry):
    argv = ["--json", "update-record", "--zone", "z1", "--record-id", "r7",
            "--content", "1.2.3.4", "--unset-ttl"]
    if dry:
        argv.insert(0, "--dry-run")
    r = _update_app().test(argv)
    env = json.loads(r.stdout)
    assert env["interface_version"] == 3
    assert env["writes"] == {
        "resource": "dns-record",
        "write_mode": "sparse",
        "written": ["content"],
        "cleared": ["ttl"],
        "resent": [],
        "untouched": ["proxied"],
    }


def test_the_envelopes_write_set_key_order_is_pinned():
    r = _update_app().test(
        ["--json", "update-record", "--zone", "z1", "--record-id", "r7",
         "--content", "xx"],
    )
    assert (
        '"writes":{"resource":"dns-record","write_mode":"sparse",'
        '"written":["content"],"cleared":[],"resent":[],'
        '"untouched":["ttl","proxied"]}'
    ) in r.stdout


def test_full_replace_swaps_resent_and_untouched():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update", effect="mutating",
                 update_of=UpdateOf("thing", write_mode="full_replace",
                                    properties=["content", "ttl"]))
    @strictcli.flag("content", type=str, help="content", presence="optional")
    @strictcli.flag("ttl", type=int, help="ttl", presence="optional")
    def _u(ctx, content, ttl):
        return 0

    r = app.test(["--json", "uu", "--content", "xx"])
    assert (
        '"writes":{"resource":"thing","write_mode":"full_replace",'
        '"written":["content"],"cleared":[],"resent":["ttl"],"untouched":[]}'
    ) in r.stdout


def test_a_command_with_no_update_carries_a_null_writes_member():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("rr", help="read", effect="read_only")
    def _r(ctx):
        return 0

    r = app.test(["--json", "rr"])
    assert '"writes":null' in r.stdout


def test_the_envelope_uses_underscored_parameter_names():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update", effect="mutating",
                 update_of=UpdateOf(
                     "thing", write_mode="sparse",
                     properties=["phone-number", "display-name"],
                 ))
    @strictcli.flag("phone-number", type=str, help="the number",
                    presence="optional")
    @strictcli.flag("display-name", type=str, help="the name",
                    presence="optional")
    def _u(ctx, phone_number, display_name):
        return 0

    r = app.test(["--json", "uu", "--phone-number", "555"])
    assert (
        '"written":["phone_number"],"cleared":[],"resent":[],'
        '"untouched":["display_name"]'
    ) in r.stdout


def test_the_human_line_uses_declared_names_without_the_prefix():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update", effect="mutating",
                 update_of=UpdateOf("thing", write_mode="sparse",
                                    properties=["phone-number"]))
    @strictcli.flag("phone-number", type=str, help="the number",
                    presence="optional")
    def _u(ctx, phone_number):
        return 0

    r = app.test(["--dry-run", "uu", "--phone-number", "555"])
    assert "  writes: phone-number (other properties unchanged)\n" in r.stdout


# ---------------------------------------------------------------------------
# §27.6 -- help rendering
# ---------------------------------------------------------------------------


def test_nullable_renders_its_minted_spelling_on_one_line():
    r = _update_app().test(["update-record", "--help"])
    for want in ("--content <str>", "--ttl <int>, --unset-ttl",
                 "--proxied, --no-proxied"):
        assert want in r.stdout
    # One presence part per line, and no second line for the minted spelling.
    assert r.stdout.count("--unset-ttl") == 1
    assert r.stdout.count("[optional]") == 3


def test_a_nullable_bool_renders_all_three_spellings():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update", effect="mutating",
                 update_of=UpdateOf("thing", write_mode="sparse",
                                    properties=["proxied"]))
    @strictcli.flag("proxied", type=bool, help="whether the record is proxied",
                    presence="optional", nullable=True)
    def _u(ctx, proxied):
        return 0

    r = app.test(["uu", "--help"])
    assert "--proxied, --no-proxied, --unset-proxied" in r.stdout


# ---------------------------------------------------------------------------
# §27.9 -- the schema encoding
# ---------------------------------------------------------------------------


def _dump(app, tmp_path):
    return app.test(["help", "--json"]).stdout


def test_the_dump_publishes_the_update_pair_and_nullable(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    app = strictcli.App(name="dnsapp", version="1.0.0", help="manage DNS")

    @app.command("update-record", help="change one DNS record in place",
                 effect="mutating",
                 update_of=UpdateOf("dns-record", write_mode="sparse",
                                    identity=["zone", "record-id"],
                                    properties=["content", "ttl"]))
    @strictcli.flag("zone", type=str, help="zone the record belongs to",
                    presence="required")
    @strictcli.flag("record-id", type=str, help="identifier of the record",
                    presence="required")
    @strictcli.flag("content", type=str, help="record content",
                    presence="optional")
    @strictcli.flag("ttl", type=int, help="time to live", presence="optional",
                    nullable=True)
    def _u(ctx, zone, record_id, content, ttl):
        return 0

    text = _dump(app, tmp_path)
    # The pair sits immediately after the dry-run pair's position and ahead of
    # the payload keys (§25.9), and the names are the DECLARED spelling.
    assert '''      "effect": "mutating",
      "update_of": {
        "resource": "dns-record",
        "identity": [
          "zone",
          "record-id"
        ],
        "properties": [
          "content",
          "ttl"
        ]
      },
      "write_mode": "sparse",''' in text
    assert '''          "presence": "optional",
          "nullable": true''' in text
    # No second entry for the minted spelling: the dump publishes declarations.
    assert "unset-ttl" not in text


def test_a_command_with_no_update_omits_the_pair(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("rr", help="read", effect="read_only")
    def _r(ctx):
        return 0

    text = _dump(app, tmp_path)
    entries = text[text.index('\n  "commands"'):]
    assert "update_of" not in entries
    assert "write_mode" not in entries


def test_an_update_with_no_identity_publishes_an_empty_array(
    tmp_path, monkeypatch,
):
    monkeypatch.chdir(tmp_path)
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update", effect="mutating",
                 update_of=UpdateOf("thing", write_mode="full_replace",
                                    properties=["content"]))
    @strictcli.flag("content", type=str, help="content", presence="optional")
    def _u(ctx, content):
        return 0

    text = _dump(app, tmp_path)
    assert '''      "update_of": {
        "resource": "thing",
        "identity": [],
        "properties": [
          "content"
        ]
      },
      "write_mode": "full_replace",''' in text


# ---------------------------------------------------------------------------
# §27.10 -- the MCP projection
# ---------------------------------------------------------------------------


def _tool_schema(app, name):
    for tool in app.as_tools():
        if tool.name == name:
            return tool.parameters
    raise AssertionError(f"no tool named {name}")


def _tool_description(app, name):
    for tool in app.as_tools():
        if tool.name == name:
            return tool.description
    raise AssertionError(f"no tool named {name}")


def test_the_at_least_one_property_rule_projects_as_a_bare_any_of():
    schema = _tool_schema(_update_app(), "update-record")
    assert schema["anyOf"] == [
        {"required": ["content"]},
        {"required": ["ttl"]},
        {"required": ["proxied"]},
    ]
    # A property is never in `required`: its requiredness IS the rule.
    assert schema["required"] == ["zone", "record_id"]


def test_the_update_branch_comes_first_inside_the_all_of():
    app = _update_app(
        constraints=[strictcli.AtLeastOne(
            "addressing",
            [strictcli.Member("content"), strictcli.Member("ttl")],
        )],
    )
    schema = _tool_schema(app, "update-record")
    assert "anyOf" not in schema
    assert len(schema["allOf"]) == 2
    assert schema["allOf"][0] == {
        "anyOf": [
            {"required": ["content"]},
            {"required": ["ttl"]},
            {"required": ["proxied"]},
        ],
    }


def test_a_nullable_property_publishes_a_type_list():
    schema = _tool_schema(_update_app(), "update-record")
    assert schema["properties"]["ttl"]["type"] == ["integer", "null"]
    assert schema["properties"]["content"]["type"] == "string"


def test_the_update_description_block():
    got = _tool_description(_update_app(), "update-record")
    assert got == (
        "change one DNS record in place\n"
        "\n"
        'Update of "dns-record" (write mode: sparse):\n'
        "  identifies: zone, record_id\n"
        "  writes: content, ttl, proxied -- at least one is required\n"
        "  a property that is not supplied is left unchanged; null clears ttl"
    )


def test_the_block_omits_identifies_and_the_null_clause():
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update it", effect="mutating",
                 update_of=UpdateOf("thing", write_mode="full_replace",
                                    properties=["content"]))
    @strictcli.flag("content", type=str, help="content", presence="optional")
    def _u(ctx, content):
        return 0

    assert _tool_description(app, "uu") == (
        "update it\n"
        "\n"
        'Update of "thing" (write mode: full_replace):\n'
        "  writes: content -- at least one is required\n"
        "  a property that is not supplied is re-sent as read"
    )


# ---------------------------------------------------------------------------
# §24.11's carve-out -- the machine doors
# ---------------------------------------------------------------------------


def test_null_on_a_nullable_propertys_key_is_the_clear():
    seen = {}

    def _handler(ctx, zone, record_id, content, ttl, proxied):
        seen["ttl"] = ttl
        seen["provided"] = ctx.provided("ttl")
        seen["unset"] = ctx.unset("ttl")
        return 0

    app = _update_app(_handler)
    app.call("update-record", zone="z1", record_id="r7", ttl=None)
    assert seen == {"ttl": None, "provided": True, "unset": True}


def test_null_on_a_non_nullable_property_is_still_refused():
    app = _update_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        app.call("update-record", zone="z1", record_id="r7", content=None)
    assert "content" in str(exc.value)


def test_the_at_least_one_rule_is_enforced_at_the_machine_door():
    app = _update_app()
    with pytest.raises(strictcli.InvokeError) as exc:
        app.call("update-record", zone="z1", record_id="r7")
    assert str(exc.value) == (
        'update "dns-record": at least one property is required: --content, '
        "--ttl, --proxied"
    )


# ---------------------------------------------------------------------------
# §27.12 -- composition
# ---------------------------------------------------------------------------


def test_dry_run_unsupported_composes_with_an_update():
    """The human write-set line then never renders, there being no dry run to
    render it in; the envelope's member still does, in a live run."""
    app = _update_app(
        dry_run_supported=False,
        dry_run_unsupported_reason="the API has no preview endpoint",
    )
    r = app.test(["--dry-run", "update-record", "--zone", "z1",
                  "--record-id", "r7", "--content", "xx"])
    assert r.exit_code == 1
    assert "the API has no preview endpoint" in r.stderr

    live = app.test(["--json", "update-record", "--zone", "z1",
                     "--record-id", "r7", "--content", "xx"])
    assert '"written":["content"]' in live.stdout


def test_an_update_command_declares_constraints_like_any_command():
    """Alternative addressing over two optional identity members IS an
    AtLeastOne, which is the intended composition (§27.12)."""
    app = strictcli.App(name="tt", version="1.0.0", help="tt")

    @app.command("uu", help="update", effect="mutating",
                 update_of=UpdateOf("thing", write_mode="sparse",
                                    identity=["id", "name"],
                                    properties=["content"]),
                 constraints=[strictcli.AtLeastOne(
                     "addressing",
                     [strictcli.Member("id"), strictcli.Member("name")],
                 )])
    @strictcli.flag("id", type=str, help="by id", presence="optional")
    @strictcli.flag("name", type=str, help="by name", presence="optional")
    @strictcli.flag("content", type=str, help="content", presence="optional")
    def _u(ctx, id, name, content):
        return 0

    r = app.test(["uu", "--content", "xx"])
    assert r.exit_code == 1
    assert 'constraint "addressing"' in r.stderr
