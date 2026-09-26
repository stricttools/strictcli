"""SIGINT and SIGTERM during a handler on the CLI path (contract §19.13)."""

import json
import signal

from conftest import run_app_script

import strictcli

# A handler that signals itself, waits for its Context to report the
# cancellation, and then runs TAIL -- the shape a real handler has when it
# notices ctx.canceled and winds down.
_BODY = """
    import os, signal, time
    app = strictcli.App(name="app", version="1.0.0", help="app")

    @app.command("cmd", effect="mutating", help="cmd")
    def _cmd(ctx):
        ctx.info("working")
        os.kill(os.getpid(), signal.SIGNAL)
        deadline = time.monotonic() + 10
        while not ctx.canceled:
            if time.monotonic() > deadline:
                raise RuntimeError("never canceled")
            time.sleep(0.01)
        TAIL
"""


def _run(tmp_path, argv, *, sig="SIGTERM", tail="ctx.out('stopping'); return 0"):
    return run_app_script(
        tmp_path, _BODY.replace("SIGNAL", sig).replace("TAIL", tail), argv,
    )


def test_sigterm_cancels_the_context_and_exits_143(tmp_path):
    p = _run(tmp_path, ["cmd"])
    assert p.returncode == 143
    assert p.stdout == b"working\nstopping\n"
    assert p.stderr == b"error: canceled by signal SIGTERM\n"


def test_sigint_exits_130(tmp_path):
    p = _run(tmp_path, ["cmd"], sig="SIGINT")
    assert p.returncode == 130
    assert p.stderr == b"error: canceled by signal SIGINT\n"


def test_the_signal_status_replaces_the_returned_one(tmp_path):
    p = _run(tmp_path, ["cmd"], tail="return 7")
    assert p.returncode == 143


def test_the_signal_status_replaces_a_sys_exit(tmp_path):
    p = _run(tmp_path, ["cmd"], tail="sys.exit(7)")
    assert p.returncode == 143
    assert p.stderr == b"error: canceled by signal SIGTERM\n"


def test_an_early_exits_message_is_kept_ahead_of_the_signal(tmp_path):
    p = _run(tmp_path, ["cmd"], tail="strictcli.exit_now(3, 'gave up')")
    assert p.returncode == 143
    assert p.stderr == (
        b"error: gave up\nerror: canceled by signal SIGTERM\n"
    )


def test_under_json_the_document_carries_143_and_the_diagnostic_last(tmp_path):
    p = _run(tmp_path, ["--json", "cmd"])
    assert p.returncode == 143
    doc = json.loads(p.stdout)
    assert doc["exit_code"] == 143
    assert doc["output"] == "stopping\n"
    assert doc["diagnostics"] == [
        {"level": "info", "message": "working"},
        {"level": "error", "message": "canceled by signal SIGTERM"},
    ]


def test_a_dry_run_still_renders_its_log(tmp_path):
    p = _run(
        tmp_path, ["--dry-run", "cmd"],
        tail="ctx.effects.write('out.txt', 'hi'); return 0",
    )
    assert p.returncode == 143
    assert p.stdout == (
        "working\n"
        "DRY RUN — no changes were made. Would do:\n"
        "  1. write: out.txt (2 bytes)\n"
    ).encode()


def test_a_second_signal_gets_the_default_action(tmp_path):
    p = _run(
        tmp_path, ["cmd"],
        tail="os.kill(os.getpid(), signal.SIGTERM); time.sleep(10); return 0",
    )
    assert p.returncode == -signal.SIGTERM


def test_the_previous_handlers_are_restored_after_the_handler(tmp_path):
    p = run_app_script(tmp_path, """
        import signal
        app = strictcli.App(name="app", version="1.0.0", help="app")
        seen = []

        @app.command("cmd", effect="read_only", help="cmd")
        def _cmd(ctx):
            seen.append(signal.getsignal(signal.SIGINT) is signal.default_int_handler)
            return 0
    """, ["cmd"], tail="""
        try:
            app.run()
        except SystemExit:
            pass
        print(seen[0], signal.getsignal(signal.SIGINT) is signal.default_int_handler,
              signal.getsignal(signal.SIGTERM) is signal.SIG_DFL)
    """)
    assert p.stdout == b"False True True\n"


def test_test_installs_nothing_and_the_context_is_canceled_when_the_dispatch_ends():
    app = strictcli.App(name="app", version="1.0.0", help="app")
    seen = {}

    @app.command("cmd", effect="read_only", help="cmd")
    def _cmd(ctx):
        seen["ctx"] = ctx
        seen["during"] = ctx.canceled
        seen["handler"] = signal.getsignal(signal.SIGTERM)
        return 0

    before = signal.getsignal(signal.SIGTERM)
    app.test(["cmd"])
    assert seen["during"] is False
    assert seen["handler"] is before
    assert seen["ctx"].canceled is True


def test_call_cancels_the_context_when_its_dispatch_ends():
    app = strictcli.App(name="app", version="1.0.0", help="app")
    seen = {}

    @app.command("cmd", effect="read_only", help="cmd")
    def _cmd(ctx):
        seen["ctx"] = ctx
        seen["during"] = ctx.canceled
        return 0

    app.call("cmd")
    assert seen["during"] is False
    assert seen["ctx"].canceled is True
