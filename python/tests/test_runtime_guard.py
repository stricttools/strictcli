"""The runtime guard and the process-exit trap (contract §19.12)."""

import json
import os
import sys

import pytest
from conftest import run_app_script

import strictcli


def _app(body, **cmd_kwargs):
    app = strictcli.App(name="app", version="1.0.0", help="app")

    @app.command("cmd", effect="read_only", help="cmd", **cmd_kwargs)
    def _cmd(ctx):
        return body(ctx)

    return app


def _guard_message(n, excerpt):
    return (
        f"stdout written outside the framework: {n} bytes: "
        + json.dumps(excerpt, ensure_ascii=False)
    )


def _raw(text):
    def body(ctx):
        sys.stdout.write(text)
        sys.stdout.flush()
        return 0
    return body


class TestTheGuardInTest:
    def test_a_raw_write_fails_the_run_and_names_the_bytes(self):
        r = _app(_raw("stray\n")).test(["--json", "cmd"])
        assert r.exit_code == 1
        assert r.stderr == ""
        doc = json.loads(r.stdout)  # stdout still carries one document
        assert doc["exit_code"] == 1
        assert doc["output"] is None
        assert doc["diagnostics"] == [
            {"level": "error", "message": _guard_message(6, "stray\n")},
        ]

    def test_print_is_a_raw_write_too(self):
        def body(ctx):
            print("hi")
            return 0

        doc = json.loads(_app(body).test(["--json", "cmd"]).stdout)
        assert doc["diagnostics"][-1]["message"] == _guard_message(3, "hi\n")

    def test_a_descriptor_level_write_is_caught(self):
        def body(ctx):
            os.write(1, b"fd\n")
            return 0

        r = _app(body).test(["--json", "cmd"])
        assert r.exit_code == 1
        assert json.loads(r.stdout)["diagnostics"][-1]["message"] == (
            _guard_message(3, "fd\n")
        )

    def test_a_nonzero_exit_code_is_kept(self):
        def body(ctx):
            _raw("x")(ctx)
            return 4

        r = _app(body).test(["--json", "cmd"])
        assert r.exit_code == 4
        assert json.loads(r.stdout)["exit_code"] == 4

    def test_the_excerpt_is_the_first_4096_bytes_and_the_count_the_total(self):
        r = _app(_raw("a" * 5000)).test(["--json", "cmd"])
        message = json.loads(r.stdout)["diagnostics"][-1]["message"]
        assert message == _guard_message(5000, "a" * 4096)

    def test_invalid_utf8_is_replaced_in_the_excerpt(self):
        def body(ctx):
            sys.stdout.buffer.write(b"\xff\n")
            return 0

        message = json.loads(_app(body).test(["--json", "cmd"]).stdout)[
            "diagnostics"][-1]["message"]
        assert message == _guard_message(2, "�\n")

    def test_it_follows_the_handlers_diagnostics_and_the_output_is_kept(self):
        def body(ctx):
            ctx.warn("slow disk")
            ctx.out("done")
            return _raw("x")(ctx)

        doc = json.loads(_app(body).test(["--json", "cmd"]).stdout)
        assert doc["output"] == "done\n"
        assert doc["diagnostics"] == [
            {"level": "warn", "message": "slow disk"},
            {"level": "error", "message": _guard_message(1, "x")},
        ]

    def test_it_follows_an_early_exit_and_keeps_its_code(self):
        def body(ctx):
            _raw("x")(ctx)
            strictcli.exit_now(3, "gone")

        r = _app(body).test(["--json", "cmd"])
        assert r.exit_code == 3
        assert [d["message"] for d in json.loads(r.stdout)["diagnostics"]] == [
            "gone", _guard_message(1, "x"),
        ]

    def test_human_mode_redirects_nothing(self):
        r = _app(_raw("stray\n")).test(["cmd"])
        assert (r.exit_code, r.stdout) == (0, "stray\n")

    def test_an_owns_stdout_commands_raw_write_fails_while_its_document_is_kept(self):
        def body(ctx):
            ctx.document().write(b"DOC\n")
            return _raw("x")(ctx)

        r = _app(body, owns_stdout=True).test(["--json", "cmd"])
        assert r.exit_code == 1
        assert r.stdout == "DOC\n"
        assert json.loads(r.stderr)["diagnostics"][-1]["message"] == (
            _guard_message(1, "x")
        )

    def test_sys_stdout_is_restored_after_the_dispatch(self):
        before = sys.stdout
        _app(_raw("x")).test(["--json", "cmd"])
        assert sys.stdout is before

    def test_call_is_not_guarded(self, capsys):
        _app(_raw("plain\n")).call("cmd")
        assert capsys.readouterr().out == "plain\n"


class TestTheTrapInTest:
    def _exiting(self, code):
        def body(ctx):
            ctx.out("partial")
            os._exit(code)
        return _app(body)

    def test_a_trapped_exit_ends_the_run_with_the_requested_code(self):
        r = self._exiting(5).test(["--json", "cmd"])
        assert r.exit_code == 5
        doc = json.loads(r.stdout)
        assert doc["exit_code"] == 5
        assert doc["output"] == "partial\n"
        assert doc["diagnostics"] == [{
            "level": "error",
            "message": "process exit called outside the framework with code 5",
        }]

    def test_code_zero_fails_the_run_with_1(self):
        r = self._exiting(0).test(["--json", "cmd"])
        assert r.exit_code == 1
        assert json.loads(r.stdout)["diagnostics"][-1]["message"] == (
            "process exit called outside the framework with code 0"
        )

    def test_os_exit_is_restored_after_the_dispatch(self):
        original = os._exit
        self._exiting(5).test(["--json", "cmd"])
        assert os._exit is original


class TestTheRealProcess:
    def test_every_route_to_descriptor_1_is_caught(self, tmp_path):
        p = run_app_script(tmp_path, """
            import os, subprocess
            app = strictcli.App(name="app", version="1.0.0", help="app")

            @app.command("cmd", effect="read_only", help="cmd")
            def _cmd(ctx):
                print("a")
                sys.stdout.flush()
                os.write(1, b"b\\n")
                subprocess.run(["echo", "c"], check=True)
                return 0
        """, ["--json", "cmd"])
        assert p.returncode == 1
        doc = json.loads(p.stdout)
        assert doc["exit_code"] == 1
        assert doc["diagnostics"] == [
            {"level": "error", "message": _guard_message(6, "a\nb\nc\n")},
        ]

    def test_an_unwinding_handler_still_writes_the_document_first(self, tmp_path):
        p = run_app_script(tmp_path, """
            app = strictcli.App(name="app", version="1.0.0", help="app")

            @app.command("cmd", effect="read_only", help="cmd")
            def _cmd(ctx):
                print("a")
                raise RuntimeError("boom")
        """, ["--json", "cmd"])
        assert p.returncode == 1
        doc = json.loads(p.stdout)
        assert doc["diagnostics"][-1]["message"] == _guard_message(2, "a\n")
        assert b"RuntimeError: boom" in p.stderr

    def test_the_trap_in_a_real_process(self, tmp_path):
        p = run_app_script(tmp_path, """
            import os
            app = strictcli.App(name="app", version="1.0.0", help="app")

            @app.command("cmd", effect="read_only", help="cmd")
            def _cmd(ctx):
                os._exit(5)
        """, ["--json", "cmd"])
        assert p.returncode == 5
        assert json.loads(p.stdout)["exit_code"] == 5

    def test_human_mode_traps_nothing(self, tmp_path):
        p = run_app_script(tmp_path, """
            import os
            app = strictcli.App(name="app", version="1.0.0", help="app")

            @app.command("cmd", effect="read_only", help="cmd")
            def _cmd(ctx):
                os._exit(7)
        """, ["cmd"])
        assert (p.returncode, p.stdout, p.stderr) == (7, b"", b"")
