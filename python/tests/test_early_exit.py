"""The early exit: strictcli.exit_now and strictcli.ExitError (contract §19.9, §3.5)."""

import asyncio
import io
import json

import pytest

import strictcli


def _app():
    return strictcli.App(name="app", version="1.0.0", help="app")


def _helper_that_gives_up():
    # Two frames below the handler, the way a real tool reaches its die().
    def deeper():
        strictcli.exit_now(3, "no manifest at ./m.toml")
    deeper()


def _exiting_app(**cmd_kwargs):
    app = _app()

    @app.command("cmd", effect="read_only", help="cmd", **cmd_kwargs)
    def _cmd(ctx):
        ctx.warn("cache is stale")
        _helper_that_gives_up()
        return 0

    return app


class TestHumanMode:
    def test_prints_the_message_with_the_error_prefix_and_exits_with_the_code(self):
        r = _exiting_app().test(["cmd"])
        assert r.exit_code == 3
        assert r.stdout == ""
        assert r.stderr == (
            "warning: cache is stale\nerror: no manifest at ./m.toml\n"
        )

    def test_quiet_never_hides_the_message(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="cmd")
        def _cmd(ctx):
            ctx.info("loading")
            strictcli.exit_now(3, "gone")

        r = app.test(["--quiet", "cmd"])
        assert (r.exit_code, r.stdout, r.stderr) == (3, "", "error: gone\n")

    def test_output_written_before_it_is_kept(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="cmd")
        def _cmd(ctx):
            ctx.out("checked 2 of 3")
            strictcli.exit_now(3, "gone")

        r = app.test(["cmd"])
        assert r.stdout == "checked 2 of 3\n"

    def test_finally_blocks_run_before_the_exit_step_prints(self):
        app = _app()
        order = []

        @app.command("cmd", effect="read_only", help="cmd")
        def _cmd(ctx):
            try:
                strictcli.exit_now(4, "gone")
            finally:
                order.append("finally")

        r = app.test(["cmd"])
        assert r.exit_code == 4
        assert order == ["finally"]

    def test_except_exception_does_not_swallow_it(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="cmd")
        def _cmd(ctx):
            try:
                strictcli.exit_now(5, "gone")
            except Exception:
                return 0
            return 0

        assert app.test(["cmd"]).exit_code == 5

    def test_a_passthrough_handler_ends_the_same_way(self):
        app = _app()

        def _forward(ctx, name, args, globals):
            strictcli.exit_now(6, "child refused")

        app.command(
            "fwd", effect="read_only", help="fwd",
            passthrough=strictcli.Passthrough(handler=_forward),
        )(lambda: None)
        r = app.test(["fwd", "xx"])
        assert (r.exit_code, r.stderr) == (6, "error: child refused\n")


class TestDryRun:
    def _app(self):
        app = _app()

        @app.command("build", effect="mutating", help="build")
        def _build(ctx):
            ctx.effects.write("out.txt", "hi")
            strictcli.exit_now(3, "no manifest")

        return app

    def test_the_would_do_log_renders_and_the_message_goes_to_stderr(self):
        r = self._app().test(["--dry-run", "build"])
        assert r.exit_code == 3
        assert r.stdout == (
            "DRY RUN — no changes were made. Would do:\n"
            "  1. write: out.txt (2 bytes)\n"
        )
        assert r.stderr == "error: no manifest\n"

    def test_json_preview_is_complete(self):
        r = self._app().test(["--json", "--dry-run", "build"])
        doc = json.loads(r.stdout)
        assert doc["exit_code"] == 3
        assert doc["dry_run"] is True
        assert [p["verb"] for p in doc["preview"]] == ["write"]
        assert doc["preview_error"] is None
        assert doc["diagnostics"] == [{"level": "error", "message": "no manifest"}]


class TestMachineMode:
    def test_the_document_carries_the_code_and_the_message_last(self):
        r = _exiting_app().test(["--json", "cmd"])
        assert r.exit_code == 3
        assert r.stderr == ""
        doc = json.loads(r.stdout)
        assert doc["exit_code"] == 3
        assert doc["diagnostics"] == [
            {"level": "warn", "message": "cache is stale"},
            {"level": "error", "message": "no manifest at ./m.toml"},
        ]

    def test_on_an_owns_stdout_command_the_document_goes_to_stderr(self):
        r = _exiting_app(owns_stdout=True).test(["--json", "cmd"])
        assert r.exit_code == 3
        assert r.stdout == ""
        assert json.loads(r.stderr)["exit_code"] == 3

    def test_a_payload_supplied_before_it_is_kept(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="cmd", payload_schema={})
        def _cmd(ctx):
            ctx.payload({"done": 2})
            strictcli.exit_now(3, "gone")

        r = app.test(["--json", "cmd"])
        assert json.loads(r.stdout)["payload"] == {"done": 2}
        assert r.data == {"done": 2}
        assert app.test(["cmd"]).data == {"done": 2}


class TestRefusals:
    @pytest.mark.parametrize("code", [0, 256, -1, True, "3"])
    def test_a_code_outside_1_to_255_is_refused_at_the_call(self, code):
        with pytest.raises(ValueError) as e:
            strictcli.exit_now(code, "xx")
        assert str(e.value) == (
            f"early exit requires an exit code between 1 and 255, got {code}: "
            "a successful run ends with a return from the handler"
        )

    @pytest.mark.parametrize("message", ["", None])
    def test_an_empty_message_is_refused_at_the_call(self, message):
        with pytest.raises(ValueError, match="^early exit requires a non-empty message$"):
            strictcli.exit_now(2, message)

    def test_a_refusal_unwinds_as_an_error_not_as_an_early_exit(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="cmd")
        def _cmd(ctx):
            strictcli.exit_now(0, "done")

        with pytest.raises(ValueError, match="got 0"):
            app.test(["cmd"])

    @pytest.mark.parametrize("code", [1, 255])
    def test_the_bounds_are_inclusive(self, code):
        app = _app()

        @app.command("cmd", effect="read_only", help="cmd")
        def _cmd(ctx):
            strictcli.exit_now(code, "bye")

        assert app.test(["cmd"]).exit_code == code


class TestProgrammaticDoors:
    def test_call_raises_exit_error(self):
        with pytest.raises(strictcli.ExitError) as e:
            _exiting_app().call("cmd")
        assert e.value.code == 3
        assert e.value.message == "no manifest at ./m.toml"
        assert str(e.value) == "no manifest at ./m.toml"

    def test_exit_error_carries_the_payload_supplied_before_the_early_exit(self):
        app = _app()

        @app.command("cmd", effect="read_only", help="cmd", payload_schema={})
        def _cmd(ctx):
            ctx.payload({"done": 2})
            strictcli.exit_now(3, "gone")

        with pytest.raises(strictcli.ExitError) as e:
            app.call("cmd")
        assert e.value.payload == {"done": 2}
        assert e.value.payload == app.test(["cmd"]).data

    def test_exit_error_payload_is_none_when_none_was_supplied(self):
        with pytest.raises(strictcli.ExitError) as e:
            _exiting_app().call("cmd")
        assert e.value.payload is None

    def test_exit_error_is_not_an_invoke_error(self):
        assert not issubclass(strictcli.ExitError, strictcli.InvokeError)

    def test_call_on_a_passthrough_raises_exit_error(self):
        app = _app()

        def _forward(ctx, name, args, globals):
            strictcli.exit_now(6, "child refused")

        app.command(
            "fwd", effect="read_only", help="fwd",
            passthrough=strictcli.Passthrough(handler=_forward),
        )(lambda: None)
        with pytest.raises(strictcli.ExitError) as e:
            app.call("fwd", _args=[])
        assert (e.value.code, e.value.message) == (6, "child refused")

    def test_acall_raises_the_same_error(self):
        with pytest.raises(strictcli.ExitError):
            asyncio.run(_exiting_app().acall("cmd"))

    def test_mcp_answers_is_error_with_the_code_and_the_message(self):
        app = _exiting_app()
        req = {
            "jsonrpc": "2.0", "id": 1, "method": "tools/call",
            "params": {
                "name": "cmd", "arguments": {},
                "_meta": {
                    "io.modelcontextprotocol/protocolVersion": "2026-07-28",
                    "io.modelcontextprotocol/clientCapabilities": {},
                },
            },
        }
        out = io.StringIO()
        app.serve_mcp(input=io.StringIO(json.dumps(req) + "\n"), output=out)
        resp = json.loads(out.getvalue().strip().splitlines()[-1])
        assert resp["result"]["isError"] is True
        assert resp["result"]["content"][0]["text"] == (
            "exit code 3: no manifest at ./m.toml"
        )


class TestOutsideAHandler:
    def test_a_check_implementation_is_not_a_handler(self):
        """Inside a check it unwinds as any other error: the runner contains
        it as that check's abort, and the check command is not ended early."""
        from pathlib import Path

        from strictcli import _CheckDef, _run_checks

        def impl(ctx):
            strictcli.exit_now(4, "gave up")

        defs = {"aa": _CheckDef(
            name="aa", tags=["tt"], severity="error", fast=True, pure=True,
            needs_network=False, depends_on=[], impl=impl,
        )}

        class _Ctx:
            project_root = Path(".")

        results, _, exit_code = _run_checks(defs, ["aa"], _Ctx(), False)
        assert exit_code == 1
        (_, outcome, _), = results
        assert outcome.status == "fail"
        assert outcome.message.endswith(": gave up")

    def test_a_validate_callback_is_not_a_handler(self):
        app = _app()

        def refuse(value):
            strictcli.exit_now(4, "gave up")

        @app.command("cmd", effect="read_only", help="cmd")
        @strictcli.flag("xx", type=str, presence="required", help="xx",
                        validate=refuse)
        def _cmd(ctx, xx):
            return 0

        with pytest.raises(BaseException) as e:
            app.test(["cmd", "--xx", "vv"])
        assert not isinstance(e.value, Exception)
        assert str(e.value) == "gave up"
