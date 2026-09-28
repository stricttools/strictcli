"""Tests for the `dry_run_supported=False` command declaration.

Covers the three registration-time guards (read_only prohibition, mandatory
reason, orphan reason), the parse-time refusal on every argv path, `--help`
precedence over the refusal, the `Dry run:` help section, and the
emit-when-declared schema pair.
"""

import json

import pytest

import strictcli


REASON = "the engine re-reads what its earlier steps wrote, so a preview lies"


def _app_with_refusing_command(*, group=False, passthrough=False):
    app = strictcli.App(name="app", version="1.0.0", help="app")
    target = app.group("rel", help="release group") if group else app

    if passthrough:
        @target.command(
            "run", effect="mutating", help="run the release",
            dry_run_supported=False, dry_run_unsupported_reason=REASON,
            passthrough=strictcli.Passthrough(
                handler=lambda ctx, name, args, globals_: 0,
            ),
        )
        def _run_pt(ctx, name, args, globals_):
            return 0
        return app

    @target.command(
        "run", effect="mutating", help="run the release",
        dry_run_supported=False, dry_run_unsupported_reason=REASON,
    )
    def _run(ctx):
        print("ran")
        return 0

    @target.command("plan", effect="mutating", help="plan the release")
    def _plan(ctx):
        print("planned")
        return 0

    return app


class TestRegistrationValidation:
    """The three declaration guards, on both registration surfaces."""

    def test_read_only_prohibition(self):
        app = strictcli.App(name="app", version="1.0.0", help="app")
        with pytest.raises(ValueError) as exc:
            @app.command(
                "show", effect="read_only", help="show",
                dry_run_supported=False, dry_run_unsupported_reason=REASON,
            )
            def _show(ctx):
                return 0
        assert str(exc.value) == (
            'command "show": a read_only command cannot declare '
            'dry_run_supported=false (a command that changes nothing has no '
            'effects a preview could misrepresent)'
        )

    def test_read_only_prohibition_in_a_group(self):
        app = strictcli.App(name="app", version="1.0.0", help="app")
        grp = app.group("gg", help="gg")
        with pytest.raises(ValueError) as exc:
            @grp.command(
                "show", effect="read_only", help="show",
                dry_run_supported=False, dry_run_unsupported_reason=REASON,
            )
            def _show(ctx):
                return 0
        assert "a read_only command cannot declare dry_run_supported=false" in str(exc.value)

    def test_read_only_prohibition_on_the_carrier(self):
        with pytest.raises(ValueError) as exc:
            strictcli.Command(
                name="show", help="show", handler=None, effect="read_only",
                dry_run_supported=False, dry_run_unsupported_reason=REASON,
            )
        assert "a read_only command cannot declare dry_run_supported=false" in str(exc.value)

    def test_missing_reason(self):
        app = strictcli.App(name="app", version="1.0.0", help="app")
        with pytest.raises(ValueError) as exc:
            @app.command("run", effect="mutating", help="run", dry_run_supported=False)
            def _run(ctx):
                return 0
        assert str(exc.value) == (
            'command "run": dry_run_supported=false requires a non-empty '
            'dry_run_unsupported_reason (say what a preview cannot honestly show)'
        )

    def test_blank_reason_is_missing(self):
        app = strictcli.App(name="app", version="1.0.0", help="app")
        with pytest.raises(ValueError) as exc:
            @app.command(
                "run", effect="mutating", help="run",
                dry_run_supported=False, dry_run_unsupported_reason="   ",
            )
            def _run(ctx):
                return 0
        assert "requires a non-empty dry_run_unsupported_reason" in str(exc.value)

    def test_reason_without_declaration(self):
        app = strictcli.App(name="app", version="1.0.0", help="app")
        with pytest.raises(ValueError) as exc:
            @app.command(
                "run", effect="mutating", help="run",
                dry_run_unsupported_reason=REASON,
            )
            def _run(ctx):
                return 0
        assert str(exc.value) == (
            'command "run": dry_run_unsupported_reason requires '
            'dry_run_supported=false (there is nothing to explain while dry '
            'run is supported)'
        )

    def test_the_baseline_needs_no_declaration(self):
        app = _app_with_refusing_command()
        assert app._commands["plan"].dry_run_supported is True
        assert app._commands["plan"].dry_run_unsupported_reason is None


class TestParseTimeRefusal:
    """--dry-run on a declared-unsupported command is refused at parse time."""

    @pytest.mark.parametrize("argv", [
        ["--dry-run", "run"],
        ["run", "--dry-run"],
    ])
    def test_refusal_on_both_flag_positions(self, argv):
        result = _app_with_refusing_command().test(argv)
        assert result.exit_code == 1
        assert result.stderr == (
            f"error: --dry-run is not supported by command 'run': {REASON}\n"
            f"try 'app --help'\n"
        )
        assert result.stdout == ""

    def test_refusal_names_the_dotted_path_in_a_group(self):
        result = _app_with_refusing_command(group=True).test(["rel", "run", "--dry-run"])
        assert result.exit_code == 1
        assert (
            f"error: --dry-run is not supported by command 'rel.run': {REASON}"
            in result.stderr
        )

    def test_refusal_applies_to_a_passthrough_command(self):
        # Pre-command position only: after a passthrough command's name the
        # quartet belongs to the child process and is never scanned.
        result = _app_with_refusing_command(passthrough=True).test(["--dry-run", "run"])
        assert result.exit_code == 1
        assert "--dry-run is not supported by command 'run'" in result.stderr

    def test_passthrough_asymmetry_leaves_a_trailing_dry_run_to_the_child(self):
        result = _app_with_refusing_command(passthrough=True).test(["run", "--dry-run"])
        assert result.exit_code == 0

    def test_a_bare_double_dash_terminates_the_scan(self):
        # After `--` the token is positional data, not the quartet: the
        # command takes no positionals, so it fails as an unexpected argument
        # rather than as a dry-run refusal.
        result = _app_with_refusing_command().test(["run", "--", "--dry-run"])
        assert result.stderr.startswith("error: unexpected argument '--dry-run'")
        assert "is not supported by command" not in result.stderr

    def test_a_supported_command_still_previews(self):
        result = _app_with_refusing_command().test(["plan", "--dry-run"])
        assert result.exit_code == 0
        assert result.stdout.startswith("planned\n")
        assert "is not supported by command" not in result.stderr

    def test_without_dry_run_the_command_runs(self):
        result = _app_with_refusing_command().test(["run"])
        assert result.exit_code == 0
        assert result.stdout == "ran\n"

    def test_refusal_on_the_real_argv_path(self, monkeypatch, capsys):
        app = _app_with_refusing_command()
        monkeypatch.setattr("sys.argv", ["app", "run", "--dry-run"])
        with pytest.raises(SystemExit) as exc:
            app.run()
        assert exc.value.code == 1
        captured = capsys.readouterr()
        assert (
            f"error: --dry-run is not supported by command 'run': {REASON}"
            in captured.err
        )


class TestHelpPrecedence:
    """--help beats the refusal: asking what a command does always answers."""

    @pytest.mark.parametrize("argv", [
        ["run", "--dry-run", "--help"],
        ["run", "--help", "--dry-run"],
        ["--dry-run", "run", "-h"],
    ])
    def test_help_wins(self, argv):
        result = _app_with_refusing_command().test(argv)
        assert result.exit_code == 0
        assert result.stdout.startswith("app run -- run the release")
        assert "is not supported by command" not in result.stderr


class TestHelpRendering:
    """The `Dry run:` section renders only for a declaring command."""

    def test_section_renders_with_the_reason(self):
        result = _app_with_refusing_command().test(["run", "--help"])
        assert result.stdout == (
            "app run -- run the release\n"
            "\n"
            "Dry run:\n"
            f"  --dry-run is not supported: {REASON}\n"
        )

    def test_section_renders_for_a_passthrough_command(self):
        result = _app_with_refusing_command(passthrough=True).test(["run", "--help"])
        assert result.stdout == (
            "app run -- run the release\n"
            "\n"
            "Dry run:\n"
            f"  --dry-run is not supported: {REASON}\n"
        )

    def test_no_section_on_a_supporting_command(self):
        result = _app_with_refusing_command().test(["plan", "--help"])
        assert "Dry run:" not in result.stdout


class TestSchemaEmission:
    """The pair is emitted only when declared."""

    def _schema(self, build_app, tmp_path, monkeypatch):
        return json.loads(build_app().test(["help", "--json"]).stdout)

    def test_emitted_when_declared(self, tmp_path, monkeypatch):
        schema = self._schema(_app_with_refusing_command, tmp_path, monkeypatch)
        run = schema["commands"]["run"]
        assert run["dry_run_supported"] is False
        assert run["dry_run_unsupported_reason"] == REASON

    def test_omitted_when_not_declared(self, tmp_path, monkeypatch):
        schema = self._schema(_app_with_refusing_command, tmp_path, monkeypatch)
        plan = schema["commands"]["plan"]
        assert "dry_run_supported" not in plan
        assert "dry_run_unsupported_reason" not in plan
