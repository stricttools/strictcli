"""The document writer of an owns-stdout command (contract §19.6's amendment)."""

import json

import pytest
from conftest import run_app_script

import strictcli

SQL = b"CREATE TABLE t (id int);\n"


def _app(owns_stdout=True, body=None):
    app = strictcli.App(name="app", version="1.0.0", help="app")

    @app.command("dump", effect="read_only", help="dump", owns_stdout=owns_stdout)
    def _dump(ctx):
        if body is not None:
            return body(ctx)
        ctx.document().write(SQL)
        return 0

    return app


class TestTheWriter:
    def test_human_mode_writes_the_bytes_to_stdout(self):
        r = _app().test(["dump"])
        assert (r.exit_code, r.stdout, r.stderr) == (0, SQL.decode(), "")

    def test_quiet_leaves_the_document_alone(self):
        assert _app().test(["--quiet", "dump"]).stdout == SQL.decode()

    def test_under_json_the_document_keeps_stdout(self):
        r = _app().test(["--json", "dump"])
        assert r.stdout == SQL.decode()
        doc = json.loads(r.stderr)
        assert doc["output"] is None
        assert doc["exit_code"] == 0

    def test_write_returns_the_byte_count_and_flush_exists(self):
        seen = []

        def body(ctx):
            w = ctx.document()
            seen.append(w.write(b"abc"))
            w.flush()
            return 0

        _app(body=body).test(["dump"])
        assert seen == [3]

    def test_a_str_is_not_bytes(self):
        def body(ctx):
            ctx.document().write("text")
            return 0

        with pytest.raises(TypeError):
            _app(body=body).test(["dump"])

    def test_call_order_with_context_writes_is_kept(self):
        def body(ctx):
            ctx.info("-- header")
            ctx.document().write(SQL)
            return 0

        assert _app(body=body).test(["dump"]).stdout == "-- header\n" + SQL.decode()

    def test_refused_on_a_command_that_does_not_own_stdout(self):
        with pytest.raises(
            ValueError,
            match='^command "dump": ctx.document requires the owns-stdout declaration$',
        ):
            _app(owns_stdout=False).test(["dump"])

    def test_call_writes_to_the_process_stdout(self, capsysbinary):
        _app().call("dump")
        assert capsysbinary.readouterr().out == SQL


class TestTheRealProcess:
    BODY = """
        app = strictcli.App(name="app", version="1.0.0", help="app")

        @app.command("dump", effect="read_only", help="dump", owns_stdout=True)
        def _dump(ctx):
            ctx.document().write(b"\\xff\\xfe raw\\n")
            return 0
    """

    def test_bytes_reach_stdout_unchanged_in_human_mode(self, tmp_path):
        p = run_app_script(tmp_path, self.BODY, ["dump"])
        assert (p.returncode, p.stdout, p.stderr) == (0, b"\xff\xfe raw\n", b"")

    def test_bytes_reach_stdout_unchanged_under_json(self, tmp_path):
        p = run_app_script(tmp_path, self.BODY, ["--json", "dump"])
        assert p.returncode == 0
        assert p.stdout == b"\xff\xfe raw\n"
        assert json.loads(p.stderr)["exit_code"] == 0
