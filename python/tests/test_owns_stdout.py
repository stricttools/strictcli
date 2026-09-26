"""Stdout ownership: the owns_stdout declaration (effects contract §19.6).

A command whose stdout IS the artifact declares it, and in machine mode the
envelope moves to stderr so the artifact's bytes are untouched. Outside machine
mode the declaration changes nothing at all.
"""

import json

import strictcli as sc


def _app():
    return sc.App(name="app", version="1.0.0", help="app")


def _dump(app):
    """A command that writes its own document through the document writer."""

    @app.command("dump", help="dump", effect="read_only", owns_stdout=True)
    def _dump_handler(ctx):
        ctx.document().write(b'{"artifact":"v1"}\n')
        return 0

    return app


class TestOutsideMachineMode:
    def test_the_declaration_changes_nothing(self):
        r = _dump(_app()).test(["dump"])
        assert r.exit_code == 0
        assert r.stdout == '{"artifact":"v1"}\n'
        assert r.stderr == ""

    def test_an_undeclared_command_is_unaffected(self):
        app = _app()

        @app.command("plain", help="plain", effect="read_only")
        def _plain(ctx):
            print("ok")
            return 0

        r = app.test(["plain"])
        assert r.stdout == "ok\n"
        assert r.stderr == ""


class TestMachineMode:
    def test_the_document_keeps_stdout_and_the_envelope_moves_to_stderr(self):
        r = _dump(_app()).test(["--json", "dump"])
        assert r.exit_code == 0
        assert r.stdout == '{"artifact":"v1"}\n'
        env = json.loads(r.stderr)
        assert env["command"] == "dump"
        assert env["exit_code"] == 0

    def test_the_diagnostics_move_with_the_envelope(self):
        app = _app()

        @app.command("dump", help="dump", effect="read_only", owns_stdout=True)
        def _dump_handler(ctx):
            ctx.info("wrote 1 row")
            ctx.warn("provisional")
            ctx.document().write(b'{"artifact":"v1"}\n')
            return 0

        r = app.test(["--json", "dump"])
        assert r.stdout == '{"artifact":"v1"}\n'
        env = json.loads(r.stderr)
        assert env["diagnostics"] == [
            {"level": "info", "message": "wrote 1 row"},
            {"level": "warn", "message": "provisional"},
        ]

    def test_an_undeclared_command_keeps_the_envelope_on_stdout(self):
        app = _app()

        @app.command("plain", help="plain", effect="read_only")
        def _plain(ctx):
            return 0

        r = app.test(["--json", "plain"])
        assert json.loads(r.stdout)["command"] == "plain"
        assert r.stderr == ""

    def test_a_preview_envelope_moves_too(self):
        app = _app()

        @app.command("dump", help="dump", effect="mutating", owns_stdout=True)
        def _dump_handler(ctx):
            ctx.effects.write("out.sql", "x")
            ctx.document().write(b"-- sql\n")
            return 0

        r = app.test(["--json", "--dry-run", "dump"])
        assert r.stdout == "-- sql\n"
        env = json.loads(r.stderr)
        assert env["dry_run"] is True
        assert [rec["verb"] for rec in env["preview"]] == ["write"]

    def test_a_declared_payload_still_rides_the_envelope(self):
        app = _app()

        @app.command(
            "dump", help="dump", effect="read_only",
            owns_stdout=True, payload_schema={"type": "object"},
        )
        def _dump_handler(ctx):
            ctx.payload({"rows": 3})
            ctx.document().write(b"-- sql\n")
            return 0

        r = app.test(["--json", "dump"])
        assert r.stdout == "-- sql\n"
        assert json.loads(r.stderr)["payload"] == {"rows": 3}


class TestSchemaDump:
    def test_the_key_is_emitted_only_when_declared(self):
        app = _app()

        @app.command("dump", help="dump", effect="read_only", owns_stdout=True)
        def _dump_handler(ctx):
            return 0

        @app.command("plain", help="plain", effect="read_only")
        def _plain(ctx):
            return 0

        commands = sc._dump_schema_core(app)["commands"]
        assert commands["dump"]["owns_stdout"] is True
        assert "owns_stdout" not in commands["plain"]
