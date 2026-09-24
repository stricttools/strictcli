"""Tests for source-filtered presence semantics (source provenance)."""

import strictcli


# ---------------------------------------------------------------------------
# Test 1: Mutex group where one flag is default -- NOT trigger violation
# ---------------------------------------------------------------------------

def _selector_source_app():
    @strictcli.choice("email", help="deliver by email")
    class Email:
        subject: str = strictcli.sub_flag(help="the subject", presence="required")
        cc: str = strictcli.sub_flag(help="a cc address", presence="optional")

    @strictcli.choice("sms", help="deliver by text")
    class Sms:
        phone: str = strictcli.sub_flag(help="the number", presence="required")

    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("out", effect="read_only", help="output command")
    @strictcli.choice_flag(
        "via", help="delivery channel", default=Sms(phone="+15550100"),
        elect_by="selector-token", choices=[Email, Sms],
    )
    def out(ctx, via: Email | Sms):
        print(
            f"provided={ctx.provided('via')} source={ctx.source('via')} "
            f"value={via!r}"
        )

    return app


def test_a_selectors_own_key_answers_provided_and_source():
    """§24.5: `ctx.provided` is true when the invocation elected, false when
    the declaration's default did, and `ctx.source` reports which."""
    app = _selector_source_app()

    r = app.test(["out"])
    assert r.exit_code == 0
    assert "provided=False source=default" in r.stdout

    r = app.test(["out", "--via", "email", "--subject", "hi"])
    assert r.exit_code == 0
    assert "provided=True source=cli" in r.stdout


def test_a_scoped_name_is_not_in_the_per_parse_store():
    """§24.9: a scoped name is not unique command-wide, so asking for one
    raises the existing unknown-name error rather than inventing an answer."""
    @strictcli.choice("email", help="deliver by email")
    class Email:
        subject: str = strictcli.sub_flag(help="the subject", presence="required")

    @strictcli.choice("sms", help="deliver by text")
    class Sms:
        phone: str = strictcli.sub_flag(help="the number", presence="required")

    app = strictcli.App(name="test", version="1.0.0", help="test app")
    seen = {}

    @app.command("out", effect="read_only", help="output command")
    @strictcli.choice_flag(
        "via", help="delivery channel", presence="required",
        elect_by="selector-token", choices=[Email, Sms],
    )
    def out(ctx, via: Email | Sms):
        try:
            ctx.provided("subject")
        except KeyError as exc:
            seen["err"] = str(exc)

    app.test(["out", "--via", "email", "--subject", "hi"])
    assert seen["err"] == "\"no source info for flag 'subject'\""


def test_the_delivered_record_answers_provided_ness_for_its_own_fields():
    """§24.9: the record answers for its own fields, and only for those."""
    @strictcli.choice("email", help="deliver by email")
    class Email:
        subject: str = strictcli.sub_flag(help="the subject", presence="required")
        cc: str = strictcli.sub_flag(help="a cc address", presence="optional")
        tone: str = strictcli.sub_flag(help="the tone", default="plain")

    @strictcli.choice("sms", help="deliver by text")
    class Sms:
        phone: str = strictcli.sub_flag(help="the number", presence="required")

    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("out", effect="read_only", help="output command")
    @strictcli.choice_flag(
        "via", help="delivery channel", presence="required",
        elect_by="selector-token", choices=[Email, Sms],
    )
    def out(ctx, via: Email | Sms):
        print(
            f"subject={strictcli.provided(via, 'subject')} "
            f"cc={strictcli.provided(via, 'cc')} "
            f"tone={strictcli.provided(via, 'tone')}"
        )

    r = app.test(["out", "--via", "email", "--subject", "hi"])
    assert r.exit_code == 0
    assert "subject=True cc=False tone=False" in r.stdout


# ---------------------------------------------------------------------------
# Test 4: Requires where required flag is implied -- should PASS
# ---------------------------------------------------------------------------

def test_requires_implied_source_counts_as_present():
    """Implied values count as 'present' for Requires dependency checks."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "deploy", effect="read_only", help="deploy",
        constraints=[
            # --all implies --loud=true
            strictcli.Implies("all-implies-loud", flag="all", implies="loud", value=True),
            # --target requires --loud
            strictcli.Requires("target-needs-loud", flag="target", depends_on="loud"),
        ],
    )
    @strictcli.flag("all", type=bool, default=False, help="deploy all")
    @strictcli.flag("loud", type=bool, default=False, help="loud mode")
    @strictcli.flag("target", type=str, help="deploy target", presence="required")
    def deploy(ctx, all, loud, target):
        print(f"all={all} loud={loud} target={target}")

    # Provide --all and --target. --all implies --loud (source=implied).
    # --target requires --loud. Implied counts for deps, so should succeed.
    r = app.test(["deploy", "--all", "--target", "prod"])
    assert r.exit_code == 0, f"expected success, got: {r.stderr}"


# ---------------------------------------------------------------------------
# Test 5: Requires where required flag is default -- should FAIL
# ---------------------------------------------------------------------------

def test_requires_default_source_not_present():
    """Default values do NOT count as 'present' for Requires dependency checks."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "deploy", effect="read_only", help="deploy",
        constraints=[
            # --target requires --loud
            strictcli.Requires("target-needs-loud", flag="target", depends_on="loud"),
        ],
    )
    @strictcli.flag("target", type=str, help="deploy target", presence="required")
    @strictcli.flag("loud", type=bool, default=False, help="loud mode")
    def deploy(ctx, target, loud):
        print(f"target={target} loud={loud}")

    # Provide --target but NOT --loud. --loud has default=False,
    # so it gets source=default. Default does NOT count for deps.
    r = app.test(["deploy", "--target", "prod"])
    assert r.exit_code == 1
    assert "requires" in r.stderr


# ---------------------------------------------------------------------------
# Invoke path tests
# ---------------------------------------------------------------------------

def test_invoke_selector_record_is_cli_source():
    """A record handed to call() carries the `cli` source, as any value does."""
    @strictcli.choice("email", help="deliver by email")
    class Email:
        subject: str = strictcli.sub_flag(help="the subject", presence="required")

    @strictcli.choice("sms", help="deliver by text")
    class Sms:
        phone: str = strictcli.sub_flag(help="the number", presence="required")

    app = strictcli.App(name="test", version="1.0.0", help="test app")
    seen = {}

    @app.command("out", effect="read_only", help="output command")
    @strictcli.choice_flag(
        "via", help="delivery channel", presence="required",
        elect_by="selector-token", choices=[Email, Sms],
    )
    def out(ctx, via: Email | Sms):
        seen["source"] = ctx.source("via")
        seen["provided"] = ctx.provided("via")

    app.call("out", via=Email(subject="hi"))
    assert seen == {"source": "cli", "provided": True}


def test_invoke_defaulted_not_present_for_requires():
    """Invoke: absent kwarg gets source=default, not present for Requires."""
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command(
        "deploy", effect="read_only", help="deploy",
        constraints=[
            strictcli.Requires("target-needs-loud", flag="target", depends_on="loud"),
        ],
    )
    @strictcli.flag("target", type=str, help="deploy target", presence="required")
    @strictcli.flag("loud", type=bool, default=False, help="loud mode")
    def deploy(ctx, target, loud):
        print(f"target={target} loud={loud}")

    # Provide target but not loud. loud will be defaulted.
    try:
        app.call("deploy", target="prod")
        assert False, "expected InvokeError for requires violation"
    except strictcli.InvokeError as e:
        assert "requires" in str(e)
