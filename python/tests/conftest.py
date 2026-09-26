"""Shared test helpers for the check-outcome tests.

The minter helpers exercise the real reporter minting path (the only way to
obtain a ``_CheckOutcome``) so behavioral tests can express expected outcomes
concisely. ``fail_outcome``/``warn_outcome`` mint one problem per extra arg (or
a single problem from the message when none are given).
"""

from strictcli import ErrorReporter, _CheckOutcome


def pass_outcome(message: str) -> _CheckOutcome:
    return ErrorReporter().passed(message)


def skip_outcome(reason: str) -> _CheckOutcome:
    return ErrorReporter().skipped(reason)


def fail_outcome(message: str, *problems: str) -> _CheckOutcome:
    r = ErrorReporter()
    if not problems:
        r.error(message)
    for p in problems:
        r.error(p)
    return r.found(message)


def warn_outcome(message: str, *problems: str) -> _CheckOutcome:
    r = ErrorReporter()
    if not problems:
        r.warn(message)
    for p in problems:
        r.warn(p)
    return r.found(message)


def drop_builtin_check_providers(app):
    """Strip strictcli's own built-in check providers from an app.

    Enabling the check system also registers the built-in ``effects-bypass``
    lint. Tests that assert on a specific check inventory (counts, --list
    output, result ordering) drop it so they keep testing the runner rather
    than the framework's own checks; dedicated tests cover the built-in itself.
    """
    app._check_providers = [
        p for p in app._check_providers
        if getattr(p, "__name__", "") != "_effects_bypass_provider"
    ]
    app._provider_materialized_cwd = None
    return app


def envelope(result):
    """Parse a machine-mode run's stdout as the envelope (contract §19.2).

    In machine mode stdout carries exactly one document, so every test that
    used to parse a bare payload document reads the envelope through this
    helper and takes the member it means.
    """
    import json
    return json.loads(result.stdout)


def payload(result):
    """The ``payload`` member of a machine-mode run's envelope (§19.4)."""
    return envelope(result)["payload"]


def run_app_script(tmp_path, body, argv, *, timeout=60):
    """Run an app through ``app.run()`` in a real child process.

    ``body`` is Python source that builds ``app`` (``strictcli`` and ``sys`` are
    already imported). The child is what reaches the process-level mechanisms
    ``test()`` never touches -- file descriptor 1, signal handlers, and the
    process exit status. Returns the CompletedProcess with bytes stdout and
    stderr.
    """
    import subprocess
    import sys
    import textwrap

    script = tmp_path / "app_script.py"
    script.write_text(
        "import sys\nimport strictcli\n"
        + textwrap.dedent(body)
        + "\napp.run()\n"
    )
    return subprocess.run(
        [sys.executable, str(script), *argv],
        capture_output=True, timeout=timeout, cwd=tmp_path,
    )
