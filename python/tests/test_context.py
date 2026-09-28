"""Tests for the Context class, always-injected ctx, and Outcome returns."""

import io
import json

import pytest
from conftest import payload

import strictcli
from strictcli import Context


def _build_app(**kwargs):
    return strictcli.App(name="myapp", version="1.0.0", help="test app", **kwargs)


class TestContextOutputRouting:
    """Context.info/warn/debug/error route to correct streams."""

    def test_info_writes_to_stdout(self):
        stdout = io.StringIO()
        stderr = io.StringIO()
        ctx = Context(stdout=stdout, stderr=stderr)
        ctx.info("hello")
        assert stdout.getvalue() == "hello\n"
        assert stderr.getvalue() == ""

    def test_warn_writes_to_stderr(self):
        stdout = io.StringIO()
        stderr = io.StringIO()
        ctx = Context(stdout=stdout, stderr=stderr)
        ctx.warn("careful")
        assert stdout.getvalue() == ""
        assert stderr.getvalue() == "warning: careful\n"

    def test_debug_writes_to_stdout(self):
        stdout = io.StringIO()
        stderr = io.StringIO()
        # debug is gated by --verbose (see the quartet gating table).
        ctx = Context(stdout=stdout, stderr=stderr, verbose=True)
        ctx.debug("trace")
        assert stdout.getvalue() == "trace\n"
        assert stderr.getvalue() == ""

    def test_error_writes_to_stderr(self):
        stdout = io.StringIO()
        stderr = io.StringIO()
        ctx = Context(stdout=stdout, stderr=stderr)
        ctx.error("fail")
        assert stdout.getvalue() == ""
        assert stderr.getvalue() == "error: fail\n"


class TestContextAlwaysInjected:
    """Every handler receives a Context as its first positional argument."""

    def test_handler_receives_context(self):
        app = _build_app()
        captured = {}

        @app.command("greet", effect="read_only", help="greet someone")
        @strictcli.flag("name", type=str, help="person to greet", presence="required")
        def greet(ctx, name):
            captured["ctx"] = ctx
            captured["name"] = name
            ctx.info(f"Hello, {name}!")

        result = app.test(["greet", "--name", "Alice"])
        assert result.exit_code == 0
        assert isinstance(captured["ctx"], Context)
        assert captured["name"] == "Alice"
        assert "Hello, Alice!" in result.stdout

    def test_context_slot_is_positional_regardless_of_name(self):
        """The first parameter is the ctx slot even when named something else."""
        app = _build_app()
        captured = {}

        @app.command("ping", effect="read_only", help="ping")
        def ping(c):
            captured["cc"] = c
            c.info("pong")

        result = app.test(["ping"])
        assert result.exit_code == 0
        assert isinstance(captured["cc"], Context)
        assert "pong" in result.stdout

    def test_context_handler_with_no_flags(self):
        app = _build_app()
        captured = {}

        @app.command("ping", effect="read_only", help="ping")
        def ping(ctx):
            captured["ctx"] = ctx
            ctx.info("pong")

        result = app.test(["ping"])
        assert result.exit_code == 0
        assert isinstance(captured["ctx"], Context)
        assert "pong" in result.stdout

    def test_no_param_handler_is_registration_error(self):
        """A flag with no handler parameter (first slot consumed by ctx) errors."""
        app = _build_app()
        with pytest.raises(ValueError, match='missing parameter "name"'):
            @app.command("greet", effect="read_only", help="greet")
            @strictcli.flag("name", type=str, help="name", presence="required")
            def greet(name):  # noqa: only ctx slot, flag 'name' unbound
                pass


class TestOutcomeConstruction:
    """Outcome is a branded frozen class, built only via the factory."""

    def test_direct_construction_forbidden(self):
        with pytest.raises(TypeError, match="cannot be constructed directly"):
            strictcli.Outcome(exit_code=0)

    def test_factory_builds_outcome(self):
        oc = strictcli.outcome(exit_code=2)
        assert isinstance(oc, strictcli.Outcome)
        assert oc.exit_code == 2

    def test_factory_defaults(self):
        oc = strictcli.outcome()
        assert oc.exit_code == 0

    def test_factory_rejects_a_data_argument(self):
        """The bare-JSON-print data channel is gone (contract §19.4)."""
        with pytest.raises(TypeError):
            strictcli.outcome(data={"kk": 1})


class TestOutcomeDataViaTest:
    """test() captures the machine payload and prints it in machine mode."""

    def test_outcome_data_captured_and_printed(self):
        app = _build_app()

        @app.command("data", effect="read_only", help="return data", payload_schema={})
        def data(ctx):
            ctx.payload({"result": 42})
            return strictcli.outcome()

        result = app.test(["data", "--json"])
        assert result.exit_code == 0
        assert result.data == {"result": 42}
        assert payload(result) == {"result": 42}

    def test_payload_is_captured_but_not_printed_outside_machine_mode(self):
        app = _build_app()

        @app.command("data", effect="read_only", help="return data",
                     payload_schema={})
        def data(ctx):
            ctx.payload({"result": 42})
            return strictcli.outcome()

        result = app.test(["data"])
        assert result.exit_code == 0
        assert result.data == {"result": 42}
        assert result.stdout == ""

    def test_outcome_exit_code_only(self):
        app = _build_app()

        @app.command("fail", effect="read_only", help="fail")
        def fail(ctx):
            return strictcli.outcome(exit_code=3)

        result = app.test(["fail"])
        assert result.exit_code == 3
        assert result.data is None
        assert result.stdout == ""

    def test_outcome_exit_code_and_data(self):
        app = _build_app()

        @app.command("both", effect="read_only", help="both", payload_schema={})
        def both(ctx):
            ctx.payload([1, 2, 3])
            return strictcli.outcome(exit_code=1)

        result = app.test(["both", "--json"])
        assert result.exit_code == 1
        assert result.data == [1, 2, 3]
        assert payload(result) == [1, 2, 3]

    def test_outcome_data_none_not_printed(self):
        app = _build_app()

        @app.command("empty", effect="read_only", help="empty outcome")
        def empty(ctx):
            return strictcli.outcome()

        result = app.test(["empty"])
        assert result.exit_code == 0
        assert result.data is None
        assert result.stdout == ""


class TestIntAndNoneReturns:
    """int and None returns keep their meaning."""

    def test_int_return_is_exit_code(self):
        app = _build_app()

        @app.command("run", effect="read_only", help="run")
        def run(ctx):
            return 7

        result = app.test(["run"])
        assert result.exit_code == 7
        assert result.data is None

    def test_none_return_is_exit_zero(self):
        app = _build_app()

        @app.command("run", effect="read_only", help="run")
        def run(ctx):
            ctx.info("done")

        result = app.test(["run"])
        assert result.exit_code == 0
        assert result.data is None


class TestBadReturnIsHardError:
    """Returning anything other than int/None/Outcome is a hard error."""

    def test_raw_dict_return_raises(self):
        app = _build_app()

        @app.command("bad", effect="read_only", help="bad")
        def bad(ctx):
            return {"nope": True}

        with pytest.raises(TypeError, match="int .exit code., None .exit 0., or"):
            app.test(["bad"])

    def test_raw_string_return_raises(self):
        app = _build_app()

        @app.command("bad", effect="read_only", help="bad")
        def bad(ctx):
            return "nope"

        with pytest.raises(TypeError, match="strictcli.outcome"):
            app.test(["bad"])

    def test_bad_return_names_type(self):
        app = _build_app()

        @app.command("bad", effect="read_only", help="bad")
        def bad(ctx):
            return 3.14

        with pytest.raises(TypeError, match="got float"):
            app.test(["bad"])


class TestOutcomeViaCall:
    """call() returns the Outcome's data payload."""

    def test_call_returns_outcome_data(self):
        app = _build_app()

        @app.command("compute", effect="read_only", help="compute", payload_schema={})
        @strictcli.flag("xx", type=int, help="value", presence="required")
        def compute(ctx, xx):
            ctx.payload({"squared": xx * xx})
            return strictcli.outcome()

        assert app.call("compute", xx=5) == {"squared": 25}

    def test_call_returns_int(self):
        app = _build_app()

        @app.command("compute", effect="read_only", help="compute")
        @strictcli.flag("xx", type=int, help="value", presence="required")
        def compute(ctx, xx):
            return xx * 2

        assert app.call("compute", xx=5) == 10

    def test_call_returns_none_for_exit_only_outcome(self):
        app = _build_app()

        @app.command("noop", effect="read_only", help="noop")
        def noop(ctx):
            return strictcli.outcome(exit_code=0)

        assert app.call("noop") is None

    def test_call_bad_return_raises(self):
        app = _build_app()

        @app.command("bad", effect="read_only", help="bad")
        def bad(ctx):
            return {"nope": True}

        with pytest.raises(TypeError, match="strictcli.outcome"):
            app.call("bad")


class TestContextWithGroups:
    """Context injection works with nested groups."""

    def test_context_in_grouped_command(self):
        app = _build_app()
        grp = app.group("svc", help="service commands")
        captured = {}

        @grp.command("status", effect="read_only", help="show status")
        def status(ctx):
            captured["ctx"] = ctx
            ctx.info("running")

        result = app.test(["svc", "status"])
        assert result.exit_code == 0
        assert isinstance(captured["ctx"], Context)
        assert "running" in result.stdout


class TestContextWithInvoke:
    """_invoke() injects Context and returns Outcome data."""

    def test_invoke_returns_outcome_data(self):
        app = _build_app()
        captured = {}

        @app.command("cmd", effect="read_only", help="test", payload_schema={})
        @strictcli.flag("xx", type=int, help="value", presence="required")
        def cmd(ctx, xx):
            captured["xx"] = xx
            ctx.payload({"doubled": xx * 2})
            return strictcli.outcome()

        result = app._invoke("cmd", {"xx": 3})
        assert result == {"doubled": 6}
        assert captured["xx"] == 3

    def test_invoke_returns_int(self):
        app = _build_app()

        @app.command("cmd", effect="read_only", help="test")
        @strictcli.flag("xx", type=int, help="value", presence="required")
        def cmd(ctx, xx):
            return xx + 1

        assert app._invoke("cmd", {"xx": 3}) == 4
