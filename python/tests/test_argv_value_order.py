"""The argv path's own value order (contract §24.3, §18.28 item 262).

§24.3 pins command-line order *within* a phase, and the value phase is ONE
phase: a root flag's token and a scoped flag's token are read in the order they
were typed, never partitioned into root-first and scoped-second. The partition
would make which of two true refusals is printed depend on a declaration the
operator cannot see, which is the outcome the phase order exists to prevent.

This is deliberately NOT the order the two programmatic doors use
(`test_value_sweep_order.py`, §18.25 item 249): those sweep the command's own
declarations once, because an object the caller hands in has no order of its
own. The command line does, and it is the one that decides here. Go is the
reference implementation for this order, and every expectation below was probed
against its bytes.

The one thing that does not follow the tokens is `validate`: every coercion
failure anywhere on the command line outranks every callback refusal (§18.20
item 226), so the callbacks run in a pass of their own once nothing is left to
coerce.
"""

import pytest

import strictcli
from strictcli import choice, choice_flag, member_value, sub_flag


def _reject(value):
    raise ValueError("rejected")


@choice("email", help="deliver as an email message")
class Email:
    # Declared BEFORE `retries` on purpose: a callback refusal must never be
    # reported ahead of a coercion failure, whatever the declaration order.
    schecked: str = sub_flag(
        help="a checked scoped string", default="", validate=_reject,
    )
    retries: int = sub_flag(help="how many times to retry", default=1)


@choice("sms", help="deliver as a text message")
class Sms:
    phone_number: str = sub_flag(help="destination number", default="+1")


@choice("profile", help="one numbered profile")
class Profile:
    value: int = member_value(help="the profile number")


@choice("all-profiles", help="every profile")
class AllProfiles:
    pass


def _app():
    """One command carrying a root int, a global int, a token-spelled selector
    with a scoped int and a checked scoped string, and a member-spelled
    selector whose payload is an int."""
    app = strictcli.App(
        name="myapp", version="1.0.0", help="test app",
        flags=[strictcli.Flag(
            name="jobs", type=int, help="how many jobs", default=1,
        )],
    )

    @app.command("run", help="run it", effect="read_only")
    @choice_flag(
        "via", help="delivery channel", presence="required",
        elect_by="selector-token", choices=[Email, Sms],
    )
    @choice_flag(
        "target", help="which profiles", default=AllProfiles(),
        elect_by="member-flags", choices=[Profile, AllProfiles],
    )
    @strictcli.flag("before", type=int, help="a root int", default=0)
    @strictcli.flag(
        "rchecked", type=str, help="a checked root string", default="",
        validate=_reject,
    )
    def run(ctx, via: "Email | Sms", target: "Profile | AllProfiles",
            before, rchecked, jobs=None):
        return 0

    return app


def _refusal(argv):
    r = _app().test(argv)
    assert r.exit_code == 1, r.stdout
    return r.stderr.splitlines()[0]


# ---------------------------------------------------------------------------
# One sweep, in the order the tokens were typed
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "tokens,want",
    [
        # A scoped token first: the scoped refusal, though the root flag is
        # declared at the command's own surface and the scoped one is not.
        (
            ["--retries", "nope", "--before", "nope"],
            "error: --retries: expected integer, got 'nope'",
        ),
        # The same two mistakes the other way round: the other answer.
        (
            ["--before", "nope", "--retries", "nope"],
            "error: --before: expected integer, got 'nope'",
        ),
        # A member's payload is one more occurrence and takes its position.
        (
            ["--profile", "nope", "--before", "nope"],
            "error: --profile: expected integer, got 'nope'",
        ),
        (
            ["--before", "nope", "--profile", "nope"],
            "error: --before: expected integer, got 'nope'",
        ),
        # An app GLOBAL typed after the command name is in the same sweep,
        # and no command declaration positions it at all.
        (
            ["--retries", "nope", "--jobs", "nope"],
            "error: --retries: expected integer, got 'nope'",
        ),
        (
            ["--jobs", "nope", "--retries", "nope"],
            "error: --jobs: expected integer, got 'nope'",
        ),
    ],
)
def test_the_value_phase_reads_root_and_scoped_tokens_in_one_order(tokens, want):
    assert _refusal(["run", "--via", "email", *tokens]) == want


def test_a_repeated_scoped_flag_is_refused_before_either_occurrence_is_read():
    """A flag that is not repeatable takes one value: `--retries nope
    --retries 1` is refused as a repetition, which outranks the token that
    will not parse, exactly as it does at the root surface."""
    assert _refusal(
        ["run", "--via", "email", "--retries", "nope", "--retries", "1"],
    ) == (
        "error: --retries: given more than once, as '--retries nope' and "
        "'--retries 1'; it takes one value"
    )


def test_no_occurrence_silently_supplies_the_value():
    r = _app().test(["run", "--via", "email", "--retries", "2", "--retries", "3"])
    assert r.exit_code == 1
    assert r.stderr.startswith(
        "error: --retries: given more than once, as '--retries 2' and "
        "'--retries 3'; it takes one value\n"
    )


# ---------------------------------------------------------------------------
# Coercion before `validate`, whatever either one's position
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "tokens,want",
    [
        # A scoped callback refusal never outranks a coercion failure -- not a
        # root one, not a scoped one, and not one typed after it.
        (
            ["--schecked", "x", "--before", "nope"],
            "error: --before: expected integer, got 'nope'",
        ),
        (
            ["--before", "nope", "--schecked", "x"],
            "error: --before: expected integer, got 'nope'",
        ),
        (
            ["--schecked", "x", "--retries", "nope"],
            "error: --retries: expected integer, got 'nope'",
        ),
        (
            ["--retries", "nope", "--schecked", "x"],
            "error: --retries: expected integer, got 'nope'",
        ),
        # And a ROOT callback refusal does not outrank a scoped coercion
        # failure either: the exception is about the two kinds of refusal, not
        # about which scope either one sits in.
        (
            ["--rchecked", "x", "--retries", "nope"],
            "error: --retries: expected integer, got 'nope'",
        ),
        (
            ["--retries", "nope", "--rchecked", "x"],
            "error: --retries: expected integer, got 'nope'",
        ),
    ],
)
def test_a_coercion_failure_outranks_a_validate_refusal(tokens, want):
    assert _refusal(["run", "--via", "email", *tokens]) == want


@pytest.mark.parametrize(
    "tokens",
    [
        ["--rchecked", "x", "--schecked", "y"],
        ["--schecked", "y", "--rchecked", "x"],
    ],
)
def test_the_scoped_callback_still_runs_before_the_root_one(tokens):
    """Between two callbacks the scopes are decided first, as they always
    were: the deferral moves `validate` past every coercion, and nothing
    else."""
    assert _refusal(["run", "--via", "email", *tokens]) == (
        "error: --schecked: rejected"
    )
