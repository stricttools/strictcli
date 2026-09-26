"""A child still running when its handler ends is killed and fails the run.

Contract §19.11's box: the exit step settles every child the handler started
through ``spawn`` and did not wait on -- an exited one is reaped and drained, a
running one gets SIGTERM, then SIGKILL a second later, and names itself in an
error diagnostic. ``Spawned`` can send a signal and kill.
"""

import json
import re
import signal
import time

import pytest
from conftest import run_app_script

import strictcli

_KILLED = re.compile(
    r"child process (\d+) was still running when the handler ended and was "
    r"killed: (.*)"
)


def _app(body, **cmd_kwargs):
    app = strictcli.App(name="app", version="1.0.0", help="app")

    @app.command("start", effect="mutating", help="start", **cmd_kwargs)
    def _start(ctx):
        return body(ctx)

    return app


def _spawn_sleeper(ctx):
    ctx.effects.spawn(["sleep", "30"])
    return 0


class TestHumanMode:
    def test_a_running_child_is_killed_and_the_run_fails(self):
        pids = []

        def body(ctx):
            pids.append(ctx.effects.spawn(["sleep", "30"]).pid)
            return 0

        r = _app(body).test(["start"])
        assert r.exit_code == 1
        assert r.stdout == ""
        assert r.stderr == (
            f"error: child process {pids[0]} was still running when the "
            "handler ended and was killed: sleep 30\n"
        )

    def test_the_child_is_really_gone(self):
        pids = []

        def body(ctx):
            pids.append(ctx.effects.spawn(["sleep", "30"]).pid)
            return 0

        _app(body).test(["start"])
        with pytest.raises(ProcessLookupError):
            import os
            os.kill(pids[0], 0)

    def test_a_nonzero_status_is_kept(self):
        def body(ctx):
            ctx.effects.spawn(["sleep", "30"])
            return 3

        assert _app(body).test(["start"]).exit_code == 3

    def test_an_early_exit_keeps_its_code_and_its_message_comes_first(self):
        def body(ctx):
            ctx.effects.spawn(["sleep", "30"])
            strictcli.exit_now(4, "worker config is missing")

        r = _app(body).test(["start"])
        assert r.exit_code == 4
        lines = r.stderr.splitlines()
        assert lines[0] == "error: worker config is missing"
        assert _KILLED.fullmatch(lines[1].removeprefix("error: "))

    def test_children_are_named_in_spawn_order(self):
        def body(ctx):
            ctx.effects.spawn(["sleep", "30"])
            ctx.effects.spawn(["sleep", "31"])
            return 0

        lines = _app(body).test(["start"]).stderr.splitlines()
        argvs = [_KILLED.fullmatch(ln.removeprefix("error: ")).group(2)
                 for ln in lines]
        assert argvs == ["sleep 30", "sleep 31"]

    def test_a_child_ignoring_sigterm_is_killed_after_one_second(self):
        def body(ctx):
            ctx.effects.spawn(["sh", "-c", "trap '' TERM; exec sleep 30"])
            time.sleep(0.2)  # let the shell install its trap
            return 0

        started = time.monotonic()
        r = _app(body).test(["start"])
        elapsed = time.monotonic() - started
        assert r.exit_code == 1
        assert 1.0 <= elapsed < 10
        assert "was killed: sh -c trap '' TERM; exec sleep 30" in r.stderr

    def test_a_waited_child_is_not_settled_again(self):
        def body(ctx):
            ctx.effects.spawn(["true"]).wait()
            return 0

        r = _app(body).test(["start"])
        assert (r.exit_code, r.stderr) == (0, "")

    def test_an_exited_child_left_unwaited_is_not_an_error(self):
        def body(ctx):
            ctx.effects.spawn(["true"])
            time.sleep(0.5)
            return 0

        r = _app(body).test(["start"])
        assert (r.exit_code, r.stderr) == (0, "")


class TestMachineMode:
    def test_the_kill_is_an_error_diagnostic_and_stderr_stays_empty(self):
        r = _app(_spawn_sleeper).test(["--json", "start"])
        assert r.exit_code == 1
        assert r.stderr == ""
        doc = json.loads(r.stdout)
        assert doc["exit_code"] == 1
        [diag] = doc["diagnostics"]
        assert diag["level"] == "error"
        assert _KILLED.fullmatch(diag["message"]).group(2) == "sleep 30"

    def test_what_a_killed_child_wrote_is_kept_in_output(self):
        def body(ctx):
            ctx.effects.spawn(["sh", "-c", "echo early; exec sleep 30"])
            time.sleep(0.3)
            return 0

        doc = json.loads(_app(body).test(["--json", "start"]).stdout)
        assert doc["output"] == "early\n"
        assert doc["exit_code"] == 1

    def test_an_exited_unwaited_child_is_drained_into_output(self):
        def body(ctx):
            ctx.effects.spawn(["echo", "hi"])
            time.sleep(0.5)
            return 0

        doc = json.loads(_app(body).test(["--json", "start"]).stdout)
        assert doc["output"] == "hi\n"
        assert doc["diagnostics"] == []


class TestSignalAndKill:
    def test_kill_ends_the_child_and_counts_as_a_wait(self):
        seen = []

        def body(ctx):
            child = ctx.effects.spawn(["sleep", "30"])
            child.kill()
            seen.append(child._proc.returncode)
            return 0

        r = _app(body).test(["start"])
        assert (r.exit_code, r.stderr) == (0, "")
        assert seen == [-signal.SIGKILL]

    def test_a_killed_childs_wait_reports_its_status(self):
        def body(ctx):
            child = ctx.effects.spawn(["sleep", "30"])
            child.kill()
            return child.wait(check=False).exit_code

        assert _app(body).test(["start"]).exit_code != 0

    def test_send_signal_delivers_and_leaves_the_wait_to_the_handler(self):
        seen = []

        def body(ctx):
            child = ctx.effects.spawn(["sleep", "30"])
            child.send_signal(signal.SIGTERM)
            seen.append(child.wait(check=False).exit_code)
            return 0

        r = _app(body).test(["start"])
        assert (r.exit_code, r.stderr) == (0, "")
        assert seen == [-signal.SIGTERM]

    def test_an_exited_child_is_left_alone(self):
        def body(ctx):
            child = ctx.effects.spawn(["true"])
            child.wait()
            child.send_signal(signal.SIGTERM)
            child.kill()
            return 0

        r = _app(body).test(["start"])
        assert (r.exit_code, r.stderr) == (0, "")

    def test_both_truncate_on_an_unsettled_handle(self):
        for method in ("kill", "send_signal"):
            def body(ctx, method=method):
                child = ctx.effects.spawn(["sleep", "30"])
                getattr(child, method)
                return 0

            r = _app(body).test(["--dry-run", "start"])
            assert r.exit_code == 1, method
            assert "dry-run preview ends at step" in r.stderr, method


class TestCallDoor:
    def test_call_kills_the_child_and_reports_the_failed_status(self):
        pids = []

        def body(ctx):
            pids.append(ctx.effects.spawn(["sleep", "30"]).pid)
            return 0

        assert _app(body).call("start") == 1
        with pytest.raises(ProcessLookupError):
            import os
            os.kill(pids[0], 0)


def test_signal_handling_lasts_until_the_children_are_settled(tmp_path):
    # The child ignores SIGTERM, so the exit step waits a second before its
    # SIGKILL; a SIGINT sent during that wait must be the run's first signal,
    # not the default action that kills the process mid-settle.
    body = """
        import os, signal, threading, time
        app = strictcli.App(name="app", version="1.0.0", help="app")

        @app.command("start", effect="mutating", help="start")
        def _start(ctx):
            ctx.effects.spawn(["sh", "-c", "trap '' TERM; exec sleep 30"])
            time.sleep(0.2)
            def later():
                time.sleep(0.4)
                os.kill(os.getpid(), signal.SIGINT)
            threading.Thread(target=later, daemon=True).start()
            return 0
    """
    p = run_app_script(tmp_path, body, ["start"])
    assert p.returncode == 130
    lines = p.stderr.decode().splitlines()
    assert _KILLED.fullmatch(lines[0].removeprefix("error: "))
    assert lines[1:] == ["error: canceled by signal SIGINT"]
