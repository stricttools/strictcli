"""Machine mode: the reserved --json flag and the payload API.

Contract §19.1 (the flag and its delivery), §19.4 (the payload API) and
§7.1's 2026-08-13 amendment (the unconditional every-level name ban).
"""

import io
import json

import pytest
from conftest import envelope, payload

import strictcli


def _app():
    return strictcli.App(name="app", version="1.0.0", help="app")


# ---------------------------------------------------------------------------
# The name ban -- unconditional, at every level
# ---------------------------------------------------------------------------

_RESERVED = "flag name 'json' is reserved by the framework"


class TestJsonNameIsReserved:
    def test_command_flag(self):
        with pytest.raises(ValueError, match=_RESERVED):
            strictcli.Flag(name="json", type=bool, default=False, help="h")

    def test_flag_decorator_at_command_registration(self):
        # The @flag decorator defers construction to command build time, which
        # is where the quartet's ban fires too.
        app = _app()
        with pytest.raises(ValueError, match=_RESERVED):
            @app.command("run", effect="read_only", help="run")
            @strictcli.flag("json", type=bool, default=False, help="h")
            def _run(ctx, json):
                return 0

    def test_app_global_flag(self):
        with pytest.raises(ValueError, match=_RESERVED):
            strictcli.App(
                name="app", version="1.0.0", help="app",
                flags=[strictcli.Flag(name="json", type=bool, default=False,
                                      help="h")],
            )

    def test_flag_set_flag(self):
        with pytest.raises(ValueError, match=_RESERVED):
            strictcli.FlagSet(flags=[
                strictcli.Flag(name="json", type=bool, default=False, help="h"),
            ])

    def test_scoped_flag_at_any_depth(self):
        """The ban re-runs at every depth: a choice's scope is a level (§24.7).

        A scope is validated when the SELECTOR claims it, because every message
        a scope raises names both the choice and its selector (§12.13).
        """

        @strictcli.choice("email", help="deliver by email")
        class Email:
            json: bool = strictcli.sub_flag(help="h", default=False)

        @strictcli.choice("sms", help="deliver by text")
        class Sms:
            pass

        with pytest.raises(ValueError, match=_RESERVED):
            strictcli.choice_flag(
                "via", help="delivery channel", presence="required",
                elect_by="selector-token", choices=[Email, Sms],
            )

    def test_a_member_choice_named_json(self):
        """Under member spelling a choice name IS a flag name (§24.7)."""

        @strictcli.choice("json", help="JSON output")
        class AsJson:
            pass

        @strictcli.choice("text", help="Text output")
        class Text:
            pass

        with pytest.raises(ValueError, match=_RESERVED):
            strictcli.choice_flag(
                "format", help="the output format", presence="required",
                elect_by="member-flags", choices=[AsJson, Text],
            )

    def test_positional_arg_named_json_is_unaffected(self):
        # The ban covers the flag surface only: an arg has no `--` spelling.
        strictcli.Arg(name="json", help="a file named json", presence="required")


# ---------------------------------------------------------------------------
# Delivery: recognized anywhere in argv, stripped, on the Context
# ---------------------------------------------------------------------------

def _flag_reader_app():
    app = _app()

    @app.command("run", effect="read_only", help="run", payload_schema={})
    def _run(ctx):
        ctx.payload({"json": ctx.json})
        return strictcli.outcome()

    return app


class TestJsonDelivery:
    def test_absent_by_default(self):
        r = _flag_reader_app().test(["run"])
        assert r.data == {"json": False}
        assert r.stdout == ""

    def test_before_the_command(self):
        r = _flag_reader_app().test(["--json", "run"])
        assert r.data == {"json": True}
        assert payload(r) == {"json": True}

    def test_after_the_command(self):
        r = _flag_reader_app().test(["run", "--json"])
        assert r.data == {"json": True}
        assert payload(r) == {"json": True}

    def test_never_a_handler_kwarg(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            return 0

        # A handler declaring no parameters still dispatches: the token is
        # stripped from argv before command parsing.
        assert app.test(["run", "--json"]).exit_code == 0

    def test_after_a_bare_double_dash_is_data(self):
        app = _app()

        @app.command("run", effect="read_only", help="run", payload_schema={})
        @strictcli.arg("rest", help="rest", variadic=True, presence="required")
        def _run(ctx, rest):
            ctx.payload({"json": ctx.json, "rest": rest})
            return strictcli.outcome()

        r = app.test(["run", "--", "--json"])
        assert r.data == {"json": False, "rest": ["--json"]}


# ---------------------------------------------------------------------------
# The payload API
# ---------------------------------------------------------------------------

class TestPayloadApi:
    def test_requires_a_declared_schema(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            ctx.payload({"k": 1})
            return strictcli.outcome()

        with pytest.raises(RuntimeError, match="requires a declared payload schema"):
            app.test(["run"])

    def test_at_most_once_per_dispatch(self):
        app = _app()

        @app.command("run", effect="read_only", help="run", payload_schema={})
        def _run(ctx):
            ctx.payload({"k": 1})
            ctx.payload({"k": 2})
            return strictcli.outcome()

        with pytest.raises(RuntimeError, match="was already called"):
            app.test(["run"])

    def test_call_returns_the_payload(self):
        app = _app()

        @app.command("run", effect="read_only", help="run", payload_schema={})
        def _run(ctx):
            ctx.payload(["a", "b"])
            return strictcli.outcome()

        assert app.call("run") == ["a", "b"]

    def test_escaping_is_plain_utf8(self):
        """Contract §19.5: escape only what JSON mandates."""
        app = _app()

        @app.command("run", effect="read_only", help="run", payload_schema={})
        def _run(ctx):
            ctx.payload({"text": "héllo <b>&</b> 日本語"})
            return strictcli.outcome()

        r = app.test(["run", "--json"])
        assert r.stdout == (
            '{"interface_version":3,"app":"app","app_version":"1.0.0",'
            '"command":"run","exit_code":0,'
            '"payload":{"text":"héllo <b>&</b> 日本語"},"output":null,"dry_run":false,'
            '"writes":null,"preview":[],"preview_error":null,'
            '"diagnostics":[]}\n'
        )


# ---------------------------------------------------------------------------
# The envelope (contract §19.2) and its preview member (§19.3)
# ---------------------------------------------------------------------------

ENVELOPE_KEYS = [
    "interface_version", "app", "app_version", "command", "exit_code",
    # `writes` sits after `dry_run` and before `preview` -- what the run
    # writes, beside the preview of how (§19.2's update-round amendment).
    "payload", "output", "dry_run", "writes", "preview", "preview_error",
    "diagnostics",
]


class TestEnvelope:
    def test_a_plain_exit_emits_the_whole_envelope(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            return 0

        r = app.test(["--json", "run"])
        assert list(envelope(r)) == ENVELOPE_KEYS
        assert envelope(r) == {
            "interface_version": 3,
            "app": "app",
            "app_version": "1.0.0",
            "command": "run",
            "exit_code": 0,
            "payload": None,
            "output": None,
            "dry_run": False,
            "writes": None,
            "preview": [],
            "preview_error": None,
            "diagnostics": [],
        }

    def test_it_is_the_sole_stdout_document(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            ctx.info("chatter")
            return 0

        r = app.test(["--json", "run"])
        assert len(r.stdout.splitlines()) == 1
        assert r.stderr == ""

    def test_the_exit_code_rides_the_envelope(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            return 3

        r = app.test(["--json", "run"])
        assert r.exit_code == 3
        assert envelope(r)["exit_code"] == 3

    def test_a_group_subcommand_carries_the_dotted_path(self):
        app = _app()
        grp = app.group("grp", help="grp")

        @grp.command("run", effect="read_only", help="run")
        def _run(ctx):
            return 0

        assert envelope(app.test(["--json", "grp", "run"]))["command"] == "grp.run"

    def test_the_dry_run_flag_rides_the_envelope(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            return 0

        assert envelope(app.test(["--json", "--dry-run", "run"]))["dry_run"] is True


class TestEnvelopeDiagnostics:
    def _app_with_diagnostics(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            ctx.info("starting")
            ctx.warn("careful")
            ctx.debug("detail")
            ctx.error("bad")
            return 0

        return app

    def test_every_level_rides_in_emission_order(self):
        r = self._app_with_diagnostics().test(["--json", "run"])
        assert envelope(r)["diagnostics"] == [
            {"level": "info", "message": "starting"},
            {"level": "warn", "message": "careful"},
            {"level": "debug", "message": "detail"},
            {"level": "error", "message": "bad"},
        ]
        assert r.stderr == ""

    def test_quiet_cannot_reach_the_envelope(self):
        """Contract §19.2: structurally exempt -- quiet governs the human stream."""
        r = self._app_with_diagnostics().test(["--quiet", "--json", "run"])
        assert len(envelope(r)["diagnostics"]) == 4
        assert envelope(r)["interface_version"] == 3

    def test_debug_rides_without_verbose(self):
        r = self._app_with_diagnostics().test(["--json", "run"])
        levels = [d["level"] for d in envelope(r)["diagnostics"]]
        assert "debug" in levels

    def test_human_mode_is_unchanged(self):
        r = self._app_with_diagnostics().test(["run"])
        assert r.stdout == "starting\n"
        assert r.stderr == "warning: careful\nerror: bad\n"


class TestEnvelopePreDispatch:
    def test_an_unknown_command_emits_a_null_command_envelope(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            return 0

        r = app.test(["--json", "nope"])
        assert r.exit_code == 1
        assert envelope(r) == {
            "interface_version": 3, "app": "app", "app_version": "1.0.0",
            "command": None, "exit_code": 1, "payload": None, "output": None,
            "dry_run": False, "writes": None, "preview": [],
            "preview_error": None, "diagnostics": [],
        }
        assert "unknown command 'nope'" in r.stderr

    def test_a_parse_error_after_the_flag_emits_an_envelope(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            return 0

        r = app.test(["run", "--json", "--nope"])
        assert r.exit_code == 1
        assert envelope(r)["command"] is None
        assert "unknown flag '--nope'" in r.stderr

    def test_the_help_flag_under_machine_mode_is_refused(self):
        """--help is text only; the refusal names the help command that
        prints the machine form, and that command works."""
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            return 0

        r = app.test(["--json", "run", "--help"])
        assert r.exit_code == 1
        assert r.stderr.startswith(
            "error: help pages are text; for the machine form use "
            "'app help run --json'\n"
        )
        assert app.test(["help", "run", "--json"]).exit_code == 0

    def test_a_stale_flag_never_decides_the_next_run(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            return 0

        app.test(["--json", "run"])
        assert app.test(["run"]).stdout == ""


class TestEnvelopePreview:
    def test_a_dry_run_preview_agrees_with_the_effect_log(self):
        app = _app()

        @app.command("rel", effect="mutating", help="rel")
        def _rel(ctx):
            ctx.effects.mkdir("build")
            ctx.effects.write("VERSION", "1.2.3")
            return 0

        r = app.test(["--json", "--dry-run", "rel"])
        assert envelope(r)["preview"] == app.effect_log()
        assert [rec["recorded"] for rec in envelope(r)["preview"]] == [True, True]

    def test_a_live_run_preview_agrees_with_the_effect_log(self, tmp_path, monkeypatch):
        monkeypatch.chdir(tmp_path)
        app = _app()

        @app.command("rel", effect="mutating", help="rel")
        def _rel(ctx):
            ctx.effects.mkdir("build")
            return 0

        r = app.test(["--json", "rel"])
        assert envelope(r)["preview"] == app.effect_log()
        assert envelope(r)["preview"][0]["recorded"] is False

    def test_a_read_only_run_previews_an_empty_list(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            return 0

        assert envelope(app.test(["--json", "--dry-run", "run"]))["preview"] == []

    def test_a_truncated_preview_carries_preview_error(self):
        app = _app()

        @app.command("rel", effect="mutating", help="rel")
        def _rel(ctx):
            out = ctx.effects.run(["git", "tag", "v1"])
            if out:
                pass
            return 0

        r = app.test(["--json", "--dry-run", "rel"])
        assert r.exit_code == 1
        assert envelope(r)["preview_error"] == {
            "kind": "truncated",
            "step": 2,
            "command": "rel",
            "brand": "«step 1 output»",
            "message": (
                "error: dry-run preview ends at step 2: rel branched on "
                "unsettled value «step 1 output» — cannot preview past this point"
            ),
        }
        assert r.stderr == ""

    def _abort_app(self):
        app = _app()

        @app.command("rel", effect="mutating", help="rel")
        def _rel(ctx):
            ctx.effects.mkdir("build")
            raise ValueError("boom")

        return app

    def _dispatch_capturing(self, app, argv):
        """Run through the dispatch seam directly: test() loses its buffers
        when the handler unwinds, and the envelope is written before the
        unwind continues (§19.3)."""
        out, err = io.StringIO(), io.StringIO()
        with pytest.raises(ValueError):
            app._dispatch(argv, out, err, "test")
        return json.loads(out.getvalue()), err.getvalue()

    def test_an_aborted_dry_run_carries_preview_error(self):
        env, stderr = self._dispatch_capturing(
            self._abort_app(), ["--json", "--dry-run", "rel"],
        )
        assert env["preview_error"] == {
            "kind": "aborted",
            "step": 2,
            "command": "rel",
            "brand": None,
            "message": (
                "error: dry-run preview ends at step 2: rel aborted — "
                "the preview above may be incomplete"
            ),
        }
        assert env["exit_code"] == 1
        assert len(env["preview"]) == 1
        assert stderr == ""

    def test_an_aborted_live_run_carries_no_preview_error(self, tmp_path, monkeypatch):
        """The marker's text names a dry-run preview, so it is dry-mode-only --
        exactly as the human stream's marker is."""
        monkeypatch.chdir(tmp_path)
        env, _ = self._dispatch_capturing(self._abort_app(), ["--json", "rel"])
        assert env["preview_error"] is None
        assert env["dry_run"] is False

    def test_the_effect_log_accessor_is_public(self):
        app = _app()

        @app.command("rel", effect="mutating", help="rel")
        def _rel(ctx):
            ctx.effects.mkdir("build")
            return 0

        app.test(["--dry-run", "rel"])
        assert app.effect_log() == [{
            "seq": 1, "kind": "file_write", "verb": "mkdir",
            "detail": "build", "recorded": True,
        }]
