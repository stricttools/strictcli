"""The framework-use lint and its reserved flag (contract §28, §7.1's box)."""

import os
import subprocess
import sys
import textwrap

import pytest

import strictcli

FLAG = "--lint-framework-use"


def _app():
    app = strictcli.App(name="app", version="1.0.0", help="app")

    @app.command("cmd", effect="read_only", help="cmd")
    def _cmd(ctx):
        return 0

    return app


# ---------------------------------------------------------------------------
# The flag
# ---------------------------------------------------------------------------


class TestTheFlag:
    def test_an_app_global_of_that_name_is_refused(self):
        with pytest.raises(
            ValueError, match='^global flag name "lint-framework-use" is reserved$',
        ):
            strictcli.App(
                name="app", version="1.0.0", help="app",
                flags=[strictcli.Flag(
                    name="lint-framework-use", type=bool, presence="required",
                    help="h",
                )],
            )

    @pytest.mark.parametrize("argv", [
        [FLAG, "cmd"], [FLAG, "--verbose"], ["--verbose", FLAG],
        ["--json", FLAG], [FLAG, FLAG], [FLAG, "--help"],
    ])
    def test_it_must_be_the_only_argument(self, argv):
        r = _app().test(argv)
        assert r.exit_code == 1
        assert "error: --lint-framework-use takes no other arguments" in r.stderr
        if "--json" not in argv:
            assert r.stdout == ""

    def test_after_the_command_word_it_is_an_unknown_flag(self):
        r = _app().test(["cmd", FLAG])
        assert r.exit_code == 1
        assert "error: unknown flag '--lint-framework-use'" in r.stderr


# ---------------------------------------------------------------------------
# The scan, through a real program in a throwaway repository
# ---------------------------------------------------------------------------

_CLI = """\
import strictcli

app = strictcli.App(name="tool", version="1.0.0", help="tool")


@app.command("cmd", effect="read_only", help="cmd")
def _cmd(ctx):
    return 0


def main():
    app.run()


if __name__ == "__main__":
    main()
"""

_PYPROJECT = """\
[project]
name = "tool"
version = "1.0.0"

[project.scripts]
tool = "tool.cli:main"
"""


def _project(root, files, *, pyproject=_PYPROJECT, git=True, package="tool"):
    if git:
        subprocess.run(["git", "init", "-q", str(root)], check=True)
    if pyproject is not None:
        (root / "pyproject.toml").write_text(pyproject)
    all_files = {f"{package}/__init__.py": "", f"{package}/cli.py": _CLI}
    all_files.update(files)
    for rel, text in all_files.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(textwrap.dedent(text))
    return root


def _lint(root, module="tool.cli", cwd=None, extra_path=None):
    env = dict(os.environ)
    env["PYTHONPATH"] = os.pathsep.join(
        p for p in (str(extra_path or root), env.get("PYTHONPATH", "")) if p
    )
    return subprocess.run(
        [sys.executable, "-m", module, FLAG],
        capture_output=True, text=True, cwd=cwd or root, env=env, timeout=60,
    )


class TestTheScan:
    def test_a_clean_program_exits_0_with_empty_output(self, tmp_path):
        p = _lint(_project(tmp_path, {}))
        assert (p.returncode, p.stdout, p.stderr) == (0, "", "")

    def test_every_rule_resolved_through_the_files_imports(self, tmp_path):
        _project(tmp_path, {"tool/bad.py": """\
            import os, sys
            import os as o
            from os import environ as e
            from sys import exit
            import argparse
            from getopt import getopt

            def f(fh, fd):
                sys.exit(1)
                raise SystemExit(2)
                raise SystemExit
                o._exit(1)
                exit(3)
                quit()
                print("x")
                print("x", file=sys.stderr)
                print("x", file=fh)
                sys.stdout.write("x")
                sys.__stderr__.write("x")
                os.write(1, b"x")
                os.write(2, b"x")
                os.write(fd, b"x")
                sys.argv[1:]
                e.get("X")
                os.getenv("X")
                o.environ.copy(); sys.orig_argv
        """})
        p = _lint(tmp_path)
        assert p.returncode == 1
        assert p.stderr == ""
        early = "strictcli.exit_now(code, message)"

        def exit_(c):
            return f"process-exit: {strictcli._msg_lint_process_exit(c, early)}"

        def out(c):
            return "stdout-write: " + strictcli._msg_lint_stdout_write(
                c, "ctx.out", "ctx.payload", "ctx.document()")

        def err(c):
            return "stderr-write: " + strictcli._msg_lint_stderr_write(
                c, "ctx.warn", "ctx.error")

        def argv(c):
            return f"argv-access: {strictcli._msg_lint_argv_access(c)}"

        def env(c):
            return f"environment-read: {strictcli._msg_lint_environment_read(c)}"

        expected = [
            (5, argv("argparse")),
            (6, argv("getopt")),
            (9, exit_("sys.exit")),
            (10, exit_("raise SystemExit")),
            (11, exit_("raise SystemExit")),
            (12, exit_("os._exit")),
            (13, exit_("sys.exit")),
            (14, exit_("quit")),
            (15, out("print")),
            (16, err("sys.stderr")),
            (18, out("sys.stdout")),
            (19, err("sys.__stderr__")),
            (20, out("os.write")),
            (21, err("os.write")),
            (23, argv("sys.argv")),
            (24, env("os.environ")),
            (25, env("os.getenv")),
            (26, argv("sys.orig_argv")),
            (26, env("os.environ")),
        ]
        lines = sorted(
            ((line, msg) for line, msg in expected),
            key=lambda x: (x[0], x[1].split(":")[0], x[1]),
        )
        assert p.stdout == "".join(
            f"tool/bad.py:{line}: {msg}\n" for line, msg in lines
        )

    def test_two_constructs_on_one_line_are_two_findings(self, tmp_path):
        _project(tmp_path, {"tool/two.py": "import sys\nsys.exit(sys.argv)\n"})
        lines = _lint(tmp_path).stdout.splitlines()
        assert [ln.split(": ")[1] for ln in lines] == ["argv-access", "process-exit"]

    def test_lines_sort_by_path_then_line(self, tmp_path):
        _project(tmp_path, {
            "tool/b.py": "import sys\n\nsys.exit(1)\n",
            "tool/a.py": "import sys\nsys.exit(1)\n\n\n\n\n\n\n\n\nsys.exit(2)\n",
        })
        lines = _lint(tmp_path).stdout.splitlines()
        assert [ln.split(":", 2)[:2] for ln in lines] == [
            ["tool/a.py", "2"], ["tool/a.py", "11"], ["tool/b.py", "3"],
        ]

    def test_test_files_are_not_scanned(self, tmp_path):
        bad = "import sys\nsys.exit(1)\n"
        _project(tmp_path, {
            "tool/test_a.py": bad, "tool/a_test.py": bad,
            "tool/conftest.py": bad, "tool/tests/helper.py": bad,
            "tool/test/helper.py": bad,
        })
        assert _lint(tmp_path).returncode == 0

    def test_other_programs_are_not_scanned(self, tmp_path):
        _project(tmp_path, {"scripts/dev.py": "import sys\nsys.exit(1)\n"})
        assert _lint(tmp_path).returncode == 0

    def test_a_script_package_that_never_imports_strictcli_is_not_scanned(self, tmp_path):
        _project(tmp_path, {
            "helper/__init__.py": "import sys\nsys.exit(1)\n",
        }, pyproject=_PYPROJECT + 'helper = "helper:main"\n')
        assert _lint(tmp_path).returncode == 0

    def test_files_git_ignores_are_not_read(self, tmp_path):
        _project(tmp_path, {
            ".gitignore": "tool/generated.py\n",
            "tool/generated.py": "import sys\nsys.exit(1)\n",
        })
        assert _lint(tmp_path).returncode == 0

    def test_a_src_layout_is_found(self, tmp_path):
        _project(tmp_path, {"src/tool/bad.py": "import sys\nsys.exit(1)\n"},
                 package="src/tool")
        p = _lint(tmp_path, extra_path=tmp_path / "src")
        assert p.returncode == 1
        assert p.stdout.startswith("src/tool/bad.py:2: process-exit: ")


class TestScanRefusals:
    def _refusal(self, p, text):
        assert (p.returncode, p.stdout, p.stderr) == (1, "", f"error: {text}\n")

    def test_no_manifest_in_the_working_directory(self, tmp_path):
        _project(tmp_path, {}, pyproject=None)
        self._refusal(_lint(tmp_path), strictcli._msg_lint_framework_use_no_manifest(
            "pyproject.toml", str(tmp_path)))

    def test_not_a_git_work_tree(self, tmp_path):
        _project(tmp_path, {}, git=False)
        self._refusal(_lint(tmp_path), strictcli._msg_lint_framework_use_not_work_tree(
            str(tmp_path)))

    def test_a_manifest_that_does_not_declare_this_program(self, tmp_path):
        _project(tmp_path, {}, pyproject=_PYPROJECT.replace("tool.cli", "other.cli"))
        self._refusal(_lint(tmp_path), strictcli._msg_lint_framework_use_manifest_mismatch(
            "pyproject.toml", str(tmp_path)))

    def test_a_package_found_in_both_layouts_is_a_mismatch(self, tmp_path):
        _project(tmp_path, {"src/tool/__init__.py": ""})
        self._refusal(_lint(tmp_path), strictcli._msg_lint_framework_use_manifest_mismatch(
            "pyproject.toml", str(tmp_path)))

    def test_a_scan_from_another_directory_is_refused(self, tmp_path):
        _project(tmp_path, {})
        sub = tmp_path / "tool"
        p = _lint(tmp_path, cwd=sub)
        self._refusal(p, strictcli._msg_lint_framework_use_no_manifest(
            "pyproject.toml", str(sub)))
