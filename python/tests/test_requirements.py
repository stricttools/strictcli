"""Declared runtime requirements.

A requirement is declared once, commands reference it, the framework loads it
before the handler runs on every door, and a missing one ends the command with
one error naming it and how to install it.
"""

import json
from dataclasses import dataclass

import pytest

import strictcli
from strictcli import ExitError, Requirement


@dataclass
class GpuLoader:
    version: str


def _vulkan(available: bool) -> Requirement:
    def load():
        if not available:
            raise OSError("libvulkan.so.1: cannot open shared object file")
        return GpuLoader(version="1.4")

    return Requirement(
        name="vulkan-loader", help="the Vulkan loader library",
        install="sudo dnf install vulkan-loader", load=load,
    )


def _app(req: Requirement):
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("render", help="render a frame", effect="read_only", requires=[req])
    def render(ctx):
        ctx.out(f"vulkan {ctx.need(req).version}")

    @app.command("plain", help="needs nothing", effect="read_only")
    def plain(ctx):
        ctx.out("plain")

    return app


MISSING = (
    "command 'render' needs vulkan-loader (the Vulkan loader library), which is "
    "not available: libvulkan.so.1: cannot open shared object file; install it: "
    "sudo dnf install vulkan-loader"
)


def test_available_runs_the_handler_with_the_loaded_value():
    r = _app(_vulkan(True)).test(["render"])
    assert (r.exit_code, r.stdout) == (0, "vulkan 1.4\n"), r.stderr


def test_missing_ends_the_command_naming_the_fix():
    app = _app(_vulkan(False))
    r = app.test(["render"])
    assert (r.exit_code, r.stdout, r.stderr) == (1, "", f"error: {MISSING}\n")
    assert app.test(["plain"]).stdout == "plain\n"
    # The fix the error names -- the requirement becoming available -- clears it.
    assert _app(_vulkan(True)).test(["render"]).exit_code == 0


def test_checked_in_dry_run_and_machine_mode():
    app = _app(_vulkan(False))
    r = app.test(["--dry-run", "render"])
    assert r.exit_code == 1 and MISSING in r.stderr
    r = app.test(["--json", "render"])
    assert r.exit_code == 1
    env = json.loads(r.stdout)
    assert env["command"] == "render"
    assert env["diagnostics"][-1] == {"level": "error", "message": MISSING}


def test_checked_on_the_programmatic_door():
    with pytest.raises(ExitError) as exc:
        _app(_vulkan(False)).call("render")
    assert (exc.value.code, exc.value.message) == (1, MISSING)
    _app(_vulkan(True)).call("render")


def test_help_never_loads_it():
    loads = []

    def load():
        loads.append(1)
        raise OSError("missing")

    req = Requirement(
        name="vulkan-loader", help="the Vulkan loader library",
        install="sudo dnf install vulkan-loader", load=load,
    )
    app = _app(req)
    want = (
        "myapp render -- render a frame\n\nRequirements:\n"
        "  vulkan-loader    the Vulkan loader library; install it: "
        "sudo dnf install vulkan-loader\n"
    )
    assert app.test(["render", "--help"]).stdout == want
    assert app.test(["help", "render"]).stdout == want
    assert loads == []


def test_published_in_the_help_document():
    doc = json.loads(_app(_vulkan(True)).test(["help", "--json"]).stdout)
    assert doc["commands"]["render"]["requires"] == [{
        "name": "vulkan-loader", "help": "the Vulkan loader library",
        "install": "sudo dnf install vulkan-loader",
    }]
    assert "requires" not in doc["commands"]["plain"]


CLAUSE = (
    "must be lowercase kebab-case of at least two characters: "
    "[a-z][a-z0-9]*(-[a-z0-9]+)*"
)


def test_declaration_refusals_and_their_fixes():
    def ok():
        return 1

    for kwargs, message in [
        (dict(name="Vulkan", help="h", install="i", load=ok),
         f'requirement name "Vulkan" {CLAUSE}'),
        (dict(name="vulkan", help="", install="i", load=ok),
         'requirement "vulkan": help must be one non-empty line'),
        (dict(name="vulkan", help="two\nlines", install="i", load=ok),
         'requirement "vulkan": help must be one non-empty line'),
        (dict(name="vulkan", help="h", install=" ", load=ok),
         'requirement "vulkan": install must be one non-empty line saying how to install it'),
        (dict(name="vulkan", help="h", install="i", load=None),
         'requirement "vulkan": load must be a function returning the loaded value or an error'),
    ]:
        with pytest.raises(ValueError) as exc:
            Requirement(**kwargs)
        assert str(exc.value) == message
    req = Requirement(name="vulkan", help="h", install="i", load=ok)

    app = strictcli.App(name="myapp", version="1.0.0", help="test app")
    with pytest.raises(ValueError) as exc:

        @app.command("render", help="x", effect="read_only", requires=[req, req])
        def render(ctx):
            pass

    assert str(exc.value) == 'command "render": requirement "vulkan" is referenced twice'

    # Declared once: two commands referencing one value is the idiom ...
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("render", help="x", effect="read_only", requires=[req])
    def render2(ctx):
        pass

    @app.group("gpu", help="g").command("probe", help="x", effect="read_only", requires=[req])
    def probe(ctx):
        pass

    # ... and a second value of the same name is refused.
    other = Requirement(name="vulkan", help="h", install="i", load=ok)
    with pytest.raises(ValueError) as exc:

        @app.command("bench", help="x", effect="read_only", requires=[other])
        def bench(ctx):
            pass

    assert str(exc.value) == (
        'requirement "vulkan" is declared by two different values; declare it once '
        "and reference that value from every command that needs it"
    )


def test_need_of_an_undeclared_requirement_is_a_hard_error():
    declared = _vulkan(True)
    undeclared = Requirement(
        name="cuda-runtime", help="the CUDA runtime",
        install="install the CUDA toolkit", load=lambda: 0,
    )
    app = strictcli.App(name="myapp", version="1.0.0", help="test app")

    @app.command("render", help="render a frame", effect="read_only", requires=[declared])
    def render(ctx):
        ctx.need(undeclared)

    with pytest.raises(RuntimeError) as exc:
        app.test(["render"])
    assert str(exc.value) == (
        "command 'render' did not declare requirement 'cuda-runtime'; add it to "
        "the command's requirements"
    )
