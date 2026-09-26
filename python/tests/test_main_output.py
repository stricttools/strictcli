"""The main-output writer and the --json document's output member.

Contract §19.10 (ctx.out), §19.2's framework-owned-exits amendment (the
``output`` member and ``interface_version`` 3), and §7.4's amendment (--quiet
never hides the command's answer).
"""

import json

import pytest

import strictcli


def _app():
    return strictcli.App(name="app", version="1.0.0", help="app")


def _answering_app(*texts, **cmd_kwargs):
    app = _app()

    @app.command("run", effect="read_only", help="run", **cmd_kwargs)
    def _run(ctx):
        for t in texts:
            ctx.out(t)
        return 0

    return app


class TestHumanMode:
    def test_each_call_prints_its_text_and_a_newline(self):
        r = _answering_app("hello", "world").test(["run"])
        assert r.exit_code == 0
        assert r.stdout == "hello\nworld\n"
        assert r.stderr == ""

    def test_quiet_does_not_hide_it(self):
        r = _answering_app("hello").test(["--quiet", "run"])
        assert r.stdout == "hello\n"

    def test_verbose_changes_nothing(self):
        r = _answering_app("hello").test(["--verbose", "run"])
        assert r.stdout == "hello\n"

    def test_info_is_hidden_by_quiet_while_the_answer_stays(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            ctx.info("loading")
            ctx.out("answer")
            return 0

        assert app.test(["--quiet", "run"]).stdout == "answer\n"
        assert app.test(["run"]).stdout == "loading\nanswer\n"

    def test_the_answer_precedes_the_would_do_log(self):
        app = _app()

        @app.command("build", effect="mutating", help="build")
        def _build(ctx):
            ctx.effects.write("out.txt", "hi")
            ctx.out("planned")
            return 0

        r = app.test(["--dry-run", "build"])
        assert r.stdout == (
            "planned\n"
            "DRY RUN — no changes were made. Would do:\n"
            "  1. write: out.txt (2 bytes)\n"
        )

    def test_a_non_string_is_refused_in_both_modes_alike(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            ctx.out(5)
            return 0

        with pytest.raises(TypeError):
            app.test(["run"])
        with pytest.raises(TypeError):
            app.test(["--json", "run"])


class TestMachineMode:
    def test_the_text_goes_into_output_byte_for_byte(self):
        r = _answering_app("hello", "world").test(["--json", "run"])
        doc = json.loads(r.stdout)
        assert doc["output"] == "hello\nworld\n"
        assert r.stderr == ""

    def test_quiet_cannot_reach_the_output_member(self):
        r = _answering_app("hello").test(["--json", "--quiet", "run"])
        assert json.loads(r.stdout)["output"] == "hello\n"

    def test_info_stays_a_diagnostic_beside_it(self):
        app = _app()

        @app.command("run", effect="read_only", help="run")
        def _run(ctx):
            ctx.info("loading")
            ctx.out("answer")
            return 0

        doc = json.loads(app.test(["--json", "run"]).stdout)
        assert doc["output"] == "answer\n"
        assert doc["diagnostics"] == [{"level": "info", "message": "loading"}]

    def test_an_empty_string_still_writes_its_newline(self):
        doc = json.loads(_answering_app("").test(["--json", "run"]).stdout)
        assert doc["output"] == "\n"


class TestTheDocumentShape:
    def test_version_3_and_output_sits_after_payload(self):
        r = _answering_app().test(["--json", "run"])
        assert r.stdout == (
            '{"interface_version":3,"app":"app","app_version":"1.0.0",'
            '"command":"run","exit_code":0,"payload":null,"output":null,'
            '"dry_run":false,"writes":null,"preview":[],'
            '"preview_error":null,"diagnostics":[]}\n'
        )

    def test_a_run_that_never_resolved_a_command_carries_it_too(self):
        r = _answering_app().test(["--json", "nosuch"])
        doc = json.loads(r.stdout)
        assert doc["interface_version"] == 3
        assert doc["command"] is None
        assert "output" in doc and doc["output"] is None


class TestRefusals:
    def test_refused_on_a_command_that_owns_stdout(self):
        app = _answering_app("x", owns_stdout=True)
        with pytest.raises(
            ValueError,
            match=(
                'command "run": ctx.out is refused on a command that owns '
                "stdout: write the document through ctx.document"
            ),
        ):
            app.test(["run"])

    def test_refused_under_json_too(self):
        app = _answering_app("x", owns_stdout=True)
        with pytest.raises(ValueError, match="ctx.out is refused"):
            app.test(["--json", "run"])


class TestProgrammaticDoor:
    def test_call_writes_the_answer_to_stdout(self, capsys):
        app = _answering_app("hello")
        app.call("run")
        assert capsys.readouterr().out == "hello\n"
