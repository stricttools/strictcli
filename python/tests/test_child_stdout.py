"""A child's stdout under --json is captured into the output member (§19.11)."""

import json
import time

from conftest import run_app_script

import strictcli


def _app(body, **cmd_kwargs):
    app = strictcli.App(name="app", version="1.0.0", help="app")

    @app.command("build", effect="mutating", help="build", **cmd_kwargs)
    def _build(ctx):
        return body(ctx)

    return app


def _streamed_echo(ctx):
    ctx.effects.run(["echo", "performed"], stream=True)
    return 0


class TestMachineMode:
    def test_a_streamed_runs_stdout_goes_into_output(self):
        r = _app(_streamed_echo).test(["--json", "build"])
        assert r.exit_code == 0
        doc = json.loads(r.stdout)
        assert doc["output"] == "performed\n"
        assert doc["diagnostics"] == []

    def test_arrival_order_with_ctx_out_is_kept(self):
        def body(ctx):
            ctx.out("before")
            ctx.effects.run(["echo", "child"], stream=True)
            ctx.out("after")
            return 0

        doc = json.loads(_app(body).test(["--json", "build"]).stdout)
        assert doc["output"] == "before\nchild\nafter\n"

    def test_invalid_utf8_is_replaced_never_dropped(self):
        def body(ctx):
            ctx.effects.run(["printf", "a\\377b"], stream=True)
            return 0

        doc = json.loads(_app(body).test(["--json", "build"]).stdout)
        assert doc["output"] == "a�b"

    def test_a_captured_run_stays_in_its_completed(self):
        seen = []

        def body(ctx):
            seen.append(ctx.effects.run(["echo", "quiet"]).stdout)
            return 0

        doc = json.loads(_app(body).test(["--json", "build"]).stdout)
        assert doc["output"] is None
        assert seen == ["quiet"]

    def test_a_spawned_child_is_drained_by_its_wait(self):
        seen = []

        def body(ctx):
            ctx.effects.spawn(["echo", "spawned"]).wait()
            seen.append(ctx._output.value())
            return 0

        doc = json.loads(_app(body).test(["--json", "build"]).stdout)
        assert seen == ["spawned\n"]
        assert doc["output"] == "spawned\n"

    def test_a_spawned_child_that_exited_unwaited_is_drained_by_the_exit_step(self):
        def body(ctx):
            ctx.effects.spawn(["sh", "-c", "echo late"])
            time.sleep(0.5)
            return 0

        doc = json.loads(_app(body).test(["--json", "build"]).stdout)
        assert doc["output"] == "late\n"
        assert doc["diagnostics"] == []

    def test_an_owns_stdout_commands_child_writes_the_document(self):
        r = _app(_streamed_echo, owns_stdout=True).test(["--json", "build"])
        assert r.stdout == "performed\n"
        assert json.loads(r.stderr)["output"] is None
        assert r.exit_code == 0


class TestTheRealProcess:
    BODY = """
        app = strictcli.App(name="app", version="1.0.0", help="app")

        @app.command("build", effect="mutating", help="build", owns_stdout=OWNS)
        def _build(ctx):
            ctx.effects.run(["sh", "-c", "echo out; echo err >&2"], stream=True)
            return 0
    """

    def test_machine_mode_captures_stdout_and_leaves_stderr_inherited(self, tmp_path):
        p = run_app_script(tmp_path, self.BODY.replace("OWNS", "False"),
                           ["--json", "build"])
        assert p.returncode == 0
        assert p.stderr == b"err\n"
        assert json.loads(p.stdout)["output"] == "out\n"

    def test_human_mode_streams_as_before(self, tmp_path):
        p = run_app_script(tmp_path, self.BODY.replace("OWNS", "False"), ["build"])
        assert (p.returncode, p.stdout, p.stderr) == (0, b"out\n", b"err\n")

    def test_an_owns_stdout_child_writes_the_real_stdout_past_the_guard(self, tmp_path):
        p = run_app_script(tmp_path, self.BODY.replace("OWNS", "True"),
                           ["--json", "build"])
        assert p.returncode == 0
        assert p.stdout == b"out\n"
        doc = json.loads(p.stderr.decode().split("\n", 1)[1])
        assert doc["exit_code"] == 0 and doc["diagnostics"] == []
