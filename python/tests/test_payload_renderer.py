"""The declared payload rendering (contract §19.10)."""

import json

import pytest

import strictcli


def _render(payload):
    return f"{payload['table']}: {payload['rows']} rows"


def _app(handler_body, **cmd_kwargs):
    app = strictcli.App(name="app", version="1.0.0", help="app")
    kwargs = {"payload_schema": {"type": "object"}, "payload_renderer": _render}
    kwargs.update(cmd_kwargs)

    @app.command("status", effect="mutating", help="status", **kwargs)
    def _status(ctx):
        return handler_body(ctx)

    return app


def _supplies(ctx):
    ctx.payload({"table": "users", "rows": 3})
    return 0


class TestHumanMode:
    def test_prints_the_rendering_of_the_payload(self):
        r = _app(_supplies).test(["status"])
        assert (r.exit_code, r.stdout, r.stderr) == (0, "users: 3 rows\n", "")

    def test_quiet_does_not_hide_it(self):
        assert _app(_supplies).test(["--quiet", "status"]).stdout == "users: 3 rows\n"

    def test_it_follows_the_handlers_output_and_precedes_the_would_do_log(self):
        def body(ctx):
            ctx.effects.write("out.txt", "hi")
            ctx.info("syncing")
            return _supplies(ctx)

        r = _app(body).test(["--dry-run", "status"])
        assert r.stdout == (
            "syncing\n"
            "users: 3 rows\n"
            "DRY RUN — no changes were made. Would do:\n"
            "  1. write: out.txt (2 bytes)\n"
        )

    def test_no_payload_renders_nothing(self):
        r = _app(lambda ctx: 0).test(["status"])
        assert (r.stdout, r.stderr) == ("", "")

    def test_an_early_exit_still_renders_the_payload(self):
        def body(ctx):
            ctx.payload({"table": "users", "rows": 2})
            strictcli.exit_now(3, "stopped")

        r = _app(body).test(["status"])
        assert (r.exit_code, r.stdout, r.stderr) == (
            3, "users: 2 rows\n", "error: stopped\n",
        )

    def test_a_deliberate_sys_exit_renders_as_a_return_does(self):
        import sys

        def body(ctx):
            ctx.payload({"table": "users", "rows": 1})
            sys.exit(2)

        r = _app(body).test(["status"])
        assert (r.exit_code, r.stdout) == (2, "users: 1 rows\n")

    def test_an_unwinding_handler_renders_nothing(self):
        def body(ctx):
            ctx.payload({"table": "users", "rows": 1})
            raise RuntimeError("boom")

        app = _app(body)
        with pytest.raises(RuntimeError):
            app.test(["status"])

    def test_a_truncated_preview_renders_nothing(self):
        def body(ctx):
            ctx.payload({"table": "users", "rows": 1})
            done = ctx.effects.run(["true"])
            bool(done)
            return 0

        r = _app(body).test(["--dry-run", "status"])
        assert r.exit_code == 1
        assert "users" not in r.stdout

    def test_the_payload_is_not_validated_first(self):
        # §19.4 validates at emission, and human mode emits no --json document.
        def body(ctx):
            ctx.payload({"table": "users", "rows": 2 ** 60})
            return 0

        r = _app(body).test(["status"])
        assert r.stdout == f"users: {2 ** 60} rows\n"

    def test_the_renderer_receives_the_payload_as_supplied(self):
        seen = []
        supplied = {"table": "tt", "rows": 1}

        def renderer(payload):
            seen.append(payload)
            return "xx"

        def body(ctx):
            ctx.payload(supplied)
            return 0

        _app(body, payload_renderer=renderer).test(["status"])
        assert seen == [supplied] and seen[0] is supplied


class TestMachineMode:
    def test_only_the_payload_is_emitted_and_output_stays_null(self):
        calls = []

        def renderer(payload):
            calls.append(payload)
            return "never"

        r = _app(_supplies, payload_renderer=renderer).test(["--json", "status"])
        doc = json.loads(r.stdout)
        assert doc["payload"] == {"table": "users", "rows": 3}
        assert doc["output"] is None
        assert calls == []

    def test_call_does_not_render(self, capsys):
        app = _app(_supplies)
        assert app.call("status") == {"table": "users", "rows": 3}
        assert capsys.readouterr().out == ""


class TestDeclaration:
    def test_a_renderer_without_a_payload_schema_is_refused(self):
        with pytest.raises(
            ValueError,
            match='^command "status": a payload renderer requires a declared payload schema$',
        ):
            _app(_supplies, payload_schema=None)

    def test_a_renderer_on_an_owns_stdout_command_is_refused(self):
        with pytest.raises(
            ValueError,
            match=(
                '^command "status": a payload renderer cannot be declared on a '
                "command that owns stdout$"
            ),
        ):
            _app(_supplies, owns_stdout=True)

    def test_ctx_out_is_refused_on_a_command_with_a_renderer(self):
        def body(ctx):
            ctx.out("xx")
            return 0

        with pytest.raises(
            ValueError,
            match=(
                'command "status": ctx.out is refused on a command that declares '
                "a payload renderer: the rendering is its human output"
            ),
        ):
            _app(body).test(["status"])

    def test_a_group_command_declares_it_the_same_way(self):
        app = strictcli.App(name="app", version="1.0.0", help="app")
        grp = app.group("gg", help="gg")

        @grp.command(
            "status", effect="read_only", help="status",
            payload_schema={"type": "object"}, payload_renderer=_render,
        )
        def _status(ctx):
            return _supplies(ctx)

        assert app.test(["gg", "status"]).stdout == "users: 3 rows\n"

    def test_dump_schema_does_not_publish_it(self):
        app = _app(_supplies)
        assert "payload_renderer" not in json.dumps(app.dump_schema_dict())
