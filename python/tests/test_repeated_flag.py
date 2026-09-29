"""A flag that is not repeatable takes one value.

Giving it more than once on the command line is refused, naming the flag and
its first two occurrences as typed, rather than silently keeping the last one.
Environment variables and config files are not occurrences: their precedence
is unchanged.
"""

import strictcli
from strictcli import UpdateOf, choice, choice_flag, member_value, sub_flag


def _app(**app_kwargs):
    app = strictcli.App(
        name="myapp", version="1.0.0", help="test app",
        flags=[
            strictcli.Flag(name="region", type=str, help="the region",
                           default="eu", short="r"),
            strictcli.Flag(name="zone", type=str, help="zones to use",
                           repeatable=True, unique=False, default=[]),
        ],
        **app_kwargs,
    )

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag("commits", type=str, help="the commit range",
                    presence="optional", short="c", env="MYAPP_COMMITS")
    @strictcli.flag("cache", type=bool, help="use the cache", default=True,
                    short="k")
    @strictcli.flag("tag", type=list[str], help="tags to apply", default=[])
    @strictcli.flag("labels", type=dict[str, str], help="labels", default={})
    def run(ctx, commits, cache, tag, labels, region, zone):
        print(f"commits={commits} cache={cache} tag={','.join(tag)} "
              f"labels={labels} region={region} zone={','.join(zone)}")

    return app


def _refused(app, argv, message):
    r = app.test(argv)
    assert r.exit_code == 1, (argv, r.stdout, r.stderr)
    assert f"error: {message}\n" in r.stderr, (argv, r.stderr)


def _runs(app, argv, fragment):
    r = app.test(argv)
    assert r.exit_code == 0, (argv, r.stderr)
    assert fragment in r.stdout, (argv, r.stdout)


def _msg(name, first, second):
    return (f"--{name}: given more than once, as '{first}' and '{second}'; "
            f"it takes one value")


def test_space_form():
    _refused(_app(), ["run", "--commits", "a", "--commits", "b"],
             _msg("commits", "--commits a", "--commits b"))


def test_equals_form():
    _refused(_app(), ["run", "--commits=a", "--commits=b"],
             _msg("commits", "--commits=a", "--commits=b"))


def test_short_alias_mixed_with_long_form():
    _refused(_app(), ["run", "-c", "a", "--commits", "b"],
             _msg("commits", "-c a", "--commits b"))


def test_the_same_value_twice_is_still_refused():
    _refused(_app(), ["run", "--commits", "a", "--commits", "a"],
             _msg("commits", "--commits a", "--commits a"))


def test_names_the_first_two_of_three():
    _refused(_app(), ["run", "--commits", "a", "-c", "b", "--commits=c"],
             _msg("commits", "--commits a", "-c b"))


def test_a_bool_given_twice():
    _refused(_app(), ["run", "--cache", "-k"], _msg("cache", "--cache", "-k"))


def test_a_bool_given_with_its_negation():
    _refused(_app(), ["run", "--cache", "--no-cache"],
             _msg("cache", "--cache", "--no-cache"))
    _refused(_app(), ["run", "--no-cache", "--no-cache"],
             _msg("cache", "--no-cache", "--no-cache"))


def test_the_refusal_outranks_a_value_that_would_not_parse():
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("run", effect="read_only", help="run it")
    @strictcli.flag("count", type=int, help="a count", presence="optional")
    def run(ctx, count):
        pass

    _refused(app, ["run", "--count", "x", "--count", "5"],
             _msg("count", "--count x", "--count 5"))


def test_passing_the_flag_once_runs():
    _runs(_app(), ["run", "--commits", "b"], "commits=b")
    _runs(_app(), ["run", "--no-cache"], "cache=False")


def test_repeatable_list_and_dict_flags_still_collect():
    _runs(_app(), ["run", "--tag", "a", "--tag", "b",
                   "--labels", "x=1", "--labels", "y=2"],
          "tag=a,b labels={'x': '1', 'y': '2'}")


def test_env_is_not_repetition(monkeypatch):
    monkeypatch.setenv("MYAPP_COMMITS", "from-env")
    _runs(_app(), ["run", "--commits", "cli"], "commits=cli")


def test_config_is_not_repetition(tmp_path):
    path = tmp_path / "config.json"
    path.write_text('{"commits": "from-config"}')
    _runs(_app(config=True, config_path=str(path)),
          ["run", "--commits", "cli"], "commits=cli")


def test_a_global_repeated_before_the_command():
    _refused(_app(), ["--region", "us", "-r", "ap", "run"],
             _msg("region", "--region us", "-r ap"))


def test_a_global_repeated_after_the_command():
    _refused(_app(), ["run", "--region=us", "--region=ap"],
             _msg("region", "--region=us", "--region=ap"))


def test_a_global_given_on_both_sides_of_the_command():
    _refused(_app(), ["--region", "us", "run", "-r", "ap"],
             _msg("region", "--region us", "-r ap"))


def test_a_repeatable_global_still_collects():
    _runs(_app(), ["--zone", "a", "--zone", "b", "run"], "zone=a,b")


def test_reports_the_first_second_occurrence():
    _refused(_app(), ["run", "--cache", "--commits", "a", "--commits", "b",
                      "--no-cache"],
             _msg("commits", "--commits a", "--commits b"))


# --- scopes, members, selectors ---


@choice("email", help="an email message")
class Email:
    subject: str = sub_flag(help="the subject", presence="required", short="s")
    urgent: bool = sub_flag(help="mark urgent", default=False)


@choice("sms", help="a text message")
class Sms:
    phone: str = sub_flag(help="destination", presence="required")


@choice("profile", help="one named profile")
class Profile:
    value: str = member_value(help="a profile")


@choice("all-profiles", help="every profile")
class AllProfiles:
    pass


def _scoped_app():
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("send", effect="read_only", help="send it")
    @choice_flag("via", help="delivery channel", presence="required",
                 elect_by="selector-token", choices=[Email, Sms])
    @choice_flag("mode", help="which profiles", default=AllProfiles(),
                 elect_by="member-flags", choices=[Profile, AllProfiles])
    def send(ctx, via: Email | Sms, mode: Profile | AllProfiles):
        print(f"via={via!r} mode={mode!r}")

    return app


def test_a_repeated_scoped_flag():
    _refused(_scoped_app(),
             ["send", "--via", "email", "-s", "hi", "--subject", "yo"],
             _msg("subject", "-s hi", "--subject yo"))


def test_a_scoped_bool_with_its_negation():
    _refused(_scoped_app(),
             ["send", "--via", "email", "-s", "hi", "--urgent", "--no-urgent"],
             _msg("urgent", "--urgent", "--no-urgent"))


def test_a_repeated_member_flag():
    _refused(_scoped_app(),
             ["send", "--via", "sms", "--phone", "1",
              "--profile", "a", "--profile", "b"],
             _msg("profile", "--profile a", "--profile b"))
    _refused(_scoped_app(),
             ["send", "--via", "sms", "--phone", "1",
              "--all-profiles", "--no-all-profiles"],
             _msg("all-profiles", "--all-profiles", "--no-all-profiles"))


def test_a_selector_elected_twice_keeps_its_own_sentence():
    _refused(_scoped_app(), ["send", "--via", "email", "--via", "sms"],
             "--via: elected more than once, as 'email' and 'sms'")


def test_an_out_of_scope_flag_outranks_the_repetition():
    _refused(_scoped_app(),
             ["send", "--via", "sms", "--subject", "a", "--subject", "b"],
             "flag '--subject' is only valid under '--via email', "
             "but '--via sms' was elected")


def test_the_repetition_outranks_a_missing_required_flag():
    _refused(_scoped_app(),
             ["send", "--via", "email", "--subject", "a", "--subject", "b",
              "--all-profiles"],
             _msg("subject", "--subject a", "--subject b"))


# --- update commands and the reserved --config ---


def _update_app():
    app = strictcli.App(name="dnsapp", version="1.0.0", help="manage DNS")

    @app.command(
        "update-record", help="change one DNS record in place",
        effect="mutating",
        update_of=UpdateOf("dns-record", write_mode="sparse",
                           identity=["zone"], properties=["ttl", "proxied"]),
    )
    @strictcli.flag("zone", type=str, help="the zone", presence="required")
    @strictcli.flag("ttl", type=int, help="time to live", presence="optional",
                    nullable=True)
    @strictcli.flag("proxied", type=bool, help="proxied", presence="optional")
    def update_record(ctx, zone, ttl, proxied):
        return 0

    return app


def test_a_clear_given_twice():
    _refused(_update_app(),
             ["update-record", "--zone", "z", "--unset-ttl", "--unset-ttl"],
             _msg("unset-ttl", "--unset-ttl", "--unset-ttl"))


def test_a_value_beside_a_clear_keeps_its_own_sentence():
    _refused(_update_app(),
             ["update-record", "--zone", "z", "--ttl", "1", "--ttl", "2",
              "--unset-ttl"],
             "--ttl and --unset-ttl are mutually exclusive: a property is "
             "either written or cleared")


def test_an_update_bool_with_its_negation():
    _refused(_update_app(),
             ["update-record", "--zone", "z", "--proxied", "--no-proxied"],
             _msg("proxied", "--proxied", "--no-proxied"))


def test_config_given_twice(tmp_path):
    path = tmp_path / "config.json"
    path.write_text("{}")
    app = _app(config=True)
    _refused(app, ["--config", str(path), f"--config={path}", "run"],
             _msg("config", f"--config {path}", f"--config={path}"))
    _runs(app, ["--config", str(path), "run"], "commits=None")
