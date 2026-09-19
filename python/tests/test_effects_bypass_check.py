"""Tests for the built-in `effects-bypass` check provider."""

import json
import subprocess
from dataclasses import dataclass
from pathlib import Path

import pytest
from conftest import payload

import strictcli


@dataclass
class SimpleContext:
    project_root: Path


CHECKS_TOML = 'app = "testapp"\n'


def _git(root, *args):
    """Run one git command inside a fixture repository."""
    subprocess.run(
        ["git", *args], cwd=str(root), check=True, capture_output=True,
    )


def _app(tmp_path, project_root=None, git_init=True):
    """An app whose check context names a project root.

    The root is a git work tree by default: the `effects-bypass` check reads
    only repository-owned files, so a fixture that is not a repository is
    refused rather than scanned.
    """
    toml_file = tmp_path / "checks.toml"
    toml_file.write_text(CHECKS_TOML)
    app = strictcli.App(
        name="testapp", version="1.0.0", help="test app",
        checks_path=str(toml_file),
    )
    root = project_root if project_root is not None else tmp_path / "src"
    root.mkdir(parents=True, exist_ok=True)
    if git_init:
        _git(root, "init", "-q")
    app.set_check_context(lambda: SimpleContext(project_root=root))
    return app, root


class TestRegistration:
    def test_registered_when_checks_are_enabled(self, tmp_path):
        app, _ = _app(tmp_path)
        r = app.test(["check", "--list"])
        assert r.exit_code == 0
        assert "effects-bypass" in r.stdout

    def test_metadata(self, tmp_path):
        app, _ = _app(tmp_path)
        r = app.test(["check", "--list", "--json"])
        entry = next(
            e for e in payload(r) if e["name"] == "effects-bypass"
        )
        assert entry["severity"] == "error"
        assert sorted(entry["tags"]) == ["effects", "quality"]

    def test_the_dumped_checks_block_never_carries_a_provider_sourced_check(
        self, tmp_path,
    ):
        """The block is a function of the DECLARATION alone (§25.7).

        A provider materializes into the same registry lazily and per-cwd, so
        a dump taken after a check run used to differ from one taken before it.
        """
        app, _ = _app(tmp_path)
        before = app.dump_schema_dict().get("checks", {})
        app.test(["check", "--list"])  # materialize providers
        after = app.dump_schema_dict().get("checks", {})
        assert "effects-bypass" not in after
        assert after == before

    def test_not_registered_without_checks(self):
        app = strictcli.App(name="testapp", version="1.0.0", help="test app")
        assert "check" not in app._commands


class TestFindings:
    def _run(self, tmp_path, source):
        app, root = _app(tmp_path)
        (root / "handlers.py").write_text(source)
        return app.test(["check", "--name", "effects-bypass"])

    def test_clean_effects_handler_passes(self, tmp_path):
        r = self._run(tmp_path, '''
def deploy(ctx):
    ctx.effects.run(["git", "push"])
    ctx.effects.write("a.txt", "hi")
    ctx.effects.remove("stale")
''')
        assert r.exit_code == 0
        assert "no direct effect calls bypass ctx.effects" in r.stdout

    @pytest.mark.parametrize("bad,target", [
        ('subprocess.run(["git", "push"])', "subprocess.run"),
        ('subprocess.Popen(["daemon"])', "subprocess.Popen"),
        ('os.system("rm -rf /")', "os.system"),
        ('os.remove("x")', "os.remove"),
        ('os.makedirs("x")', "os.makedirs"),
        ('os.chmod("x", 0o755)', "os.chmod"),
        ('shutil.rmtree("x")', "shutil.rmtree"),
        ('Path("x").write_text("y")', "write_text"),
        ('requests.post("https://x.test")', "requests.post"),
        ('urllib.request.urlopen("https://x.test")', "urllib.request.urlopen"),
        ('open("x", "w")', "open"),
    ])
    def test_direct_calls_are_findings(self, tmp_path, bad, target):
        r = self._run(tmp_path, f'''
def deploy(ctx):
    ctx.effects.run(["true"])
    {bad}
''')
        assert r.exit_code == 1
        assert "1 direct effect call(s) bypassing ctx.effects" in r.stdout
        assert f"deploy calls {target} directly; route it through ctx.effects" in r.stdout

    def test_read_only_open_is_not_a_finding(self, tmp_path):
        r = self._run(tmp_path, '''
def deploy(ctx):
    ctx.effects.run(["true"])
    open("x")
    open("y", "r")
''')
        assert r.exit_code == 0

    def test_functions_that_never_opt_in_are_not_analysed(self, tmp_path):
        r = self._run(tmp_path, '''
import subprocess

def helper():
    subprocess.run(["anything"])
''')
        assert r.exit_code == 0

    def test_finding_names_file_and_line(self, tmp_path):
        r = self._run(tmp_path, '''
def deploy(ctx):
    ctx.effects.run(["true"])
    os.remove("x")
''')
        assert "handlers.py:4: deploy calls os.remove directly" in r.stdout

    def test_multiple_findings_are_all_reported(self, tmp_path):
        r = self._run(tmp_path, '''
def deploy(ctx):
    ctx.effects.run(["true"])
    os.remove("a")
    os.remove("b")
''')
        assert "2 direct effect call(s) bypassing ctx.effects" in r.stdout

    def test_ordinary_get_is_not_a_network_finding(self, tmp_path):
        r = self._run(tmp_path, '''
def deploy(ctx, mapping):
    ctx.effects.run(["true"])
    return mapping.get("k")
''')
        assert r.exit_code == 0

    def test_unparseable_file_is_not_a_finding(self, tmp_path):
        app, root = _app(tmp_path)
        (root / "broken.py").write_text("def (:\n")
        r = app.test(["check", "--name", "effects-bypass"])
        assert r.exit_code == 0

    def test_skipped_directories_are_not_scanned(self, tmp_path):
        app, root = _app(tmp_path)
        vendored = root / "node_modules"
        vendored.mkdir()
        (vendored / "handlers.py").write_text('''
def deploy(ctx):
    ctx.effects.run(["true"])
    os.remove("x")
''')
        assert app.test(["check", "--name", "effects-bypass"]).exit_code == 0

    def test_a_handler_that_never_mentions_effects_is_analysed(self, tmp_path):
        """Escape shape 1: opting in cannot be the trigger.

        §11 scopes the lint to calls REACHABLE FROM A REGISTERED COMMAND
        HANDLER. A handler that mentions the handle nowhere is the easiest
        possible bypass, and a lint that only looked at effects-using functions
        would wave it straight through.
        """
        r = self._run(tmp_path, '''
import os
import shutil
import subprocess

import strictcli

app = strictcli.App(name="a", version="1.0.0", help="a")


@app.command("deploy", help="deploy", effect="mutating")
def deploy(ctx):
    subprocess.run(["git", "push"])
    os.makedirs("build")
    shutil.rmtree("stale")
    return 0
''')
        assert r.exit_code == 1
        assert "3 direct effect call(s) bypassing ctx.effects" in r.stdout
        assert "deploy calls subprocess.run directly" in r.stdout
        assert "deploy calls os.makedirs directly" in r.stdout
        assert "deploy calls shutil.rmtree directly" in r.stdout

    def test_a_bypass_one_helper_call_away_is_analysed(self, tmp_path):
        """Escape shape 2: reachability, not the immediate body."""
        r = self._run(tmp_path, '''
import subprocess

import strictcli

app = strictcli.App(name="a", version="1.0.0", help="a")


def _publish(path):
    subprocess.run(["rsync", path, "remote:/srv"])


@app.command("deploy", help="deploy", effect="mutating")
def deploy(ctx):
    ctx.effects.run(["make", "build"])
    _publish("build")
    return 0
''')
        assert r.exit_code == 1
        assert "1 direct effect call(s) bypassing ctx.effects" in r.stdout
        assert "_publish calls subprocess.run directly" in r.stdout

    def test_reachability_is_transitive(self, tmp_path):
        r = self._run(tmp_path, '''
import os

import strictcli

app = strictcli.App(name="a", version="1.0.0", help="a")


def _inner():
    os.remove("x")


def _outer():
    _inner()


@app.command("deploy", help="deploy", effect="mutating")
def deploy(ctx):
    _outer()
    return 0
''')
        assert r.exit_code == 1
        assert "_inner calls os.remove directly" in r.stdout

    def test_a_handler_named_through_the_handler_keyword_is_a_root(self, tmp_path):
        r = self._run(tmp_path, '''
import subprocess


def _pt(ctx, name, args, globals):
    subprocess.run(["docker"] + args)
    return 0


spec = Passthrough(handler=_pt)
''')
        assert r.exit_code == 1
        assert "_pt calls subprocess.run directly" in r.stdout

    def test_a_local_alias_of_the_handle_is_not_a_bypass(self, tmp_path):
        r = self._run(tmp_path, '''
import strictcli

app = strictcli.App(name="a", version="1.0.0", help="a")


@app.command("deploy", help="deploy", effect="mutating")
def deploy(ctx):
    e = ctx.effects
    e.mkdir("build")
    e.remove("stale")
    return 0
''')
        assert r.exit_code == 0

    def test_an_unreachable_helper_is_still_not_analysed(self, tmp_path):
        """The scope is reachability, not "every function in the tree"."""
        r = self._run(tmp_path, '''
import subprocess

import strictcli

app = strictcli.App(name="a", version="1.0.0", help="a")


def _never_called():
    subprocess.run(["anything"])


@app.command("deploy", help="deploy", effect="mutating")
def deploy(ctx):
    ctx.effects.run(["make"])
    return 0
''')
        assert r.exit_code == 0

    def test_a_bypass_is_reported_once_per_call_site(self, tmp_path):
        r = self._run(tmp_path, '''
import os

import strictcli

app = strictcli.App(name="a", version="1.0.0", help="a")


@app.command("deploy", help="deploy", effect="mutating")
def deploy(ctx):
    ctx.effects.run(["make"])

    def _nested():
        os.remove("x")

    _nested()
    return 0
''')
        assert r.exit_code == 1
        assert "1 direct effect call(s) bypassing ctx.effects" in r.stdout

    def test_async_handlers_are_analysed(self, tmp_path):
        r = self._run(tmp_path, '''
async def deploy(ctx):
    ctx.effects.run(["true"])
    os.remove("x")
''')
        assert r.exit_code == 1


_OFFENDING = '''
def deploy(ctx):
    ctx.effects.run(["true"])
    os.remove("x")
'''


class TestInputsAreRepositoryOwned:
    """The check enumerates its inputs through git.

    A release-blocking check reads only what the repository owns, so the file
    set is what `git ls-files --cached --others --exclude-standard` lists from
    the project root: tracked files plus untracked files `.gitignore` does not
    exclude. A gitignored scratch file is one machine's, not the repository's,
    and must never decide a verdict.
    """

    def test_tracked_and_unignored_files_are_read_and_ignored_ones_are_not(
        self, tmp_path,
    ):
        app, root = _app(tmp_path)
        (root / "tracked.py").write_text(_OFFENDING)
        _git(root, "add", "tracked.py")
        (root / "untracked.py").write_text(_OFFENDING)
        (root / ".gitignore").write_text("ignored.py\n")
        (root / "ignored.py").write_text(_OFFENDING)

        r = app.test(["check", "--name", "effects-bypass"])

        assert r.exit_code == 1
        assert "tracked.py:4: deploy calls os.remove directly" in r.stdout
        assert "untracked.py:4: deploy calls os.remove directly" in r.stdout
        assert "ignored.py" not in r.stdout
        assert "2 direct effect call(s) bypassing ctx.effects" in r.stdout

    def test_a_root_outside_a_git_work_tree_is_refused(self, tmp_path):
        root = tmp_path / "loose"
        app, _ = _app(tmp_path, project_root=root, git_init=False)
        (root / "handlers.py").write_text(_OFFENDING)

        r = app.test(["check", "--name", "effects-bypass"])

        assert r.exit_code == 1
        assert (
            f"effects-bypass: project root '{root}' is not a git work tree; "
            f"the check reads only repository-owned files"
        ) in r.stdout
        assert "calls os.remove directly" not in r.stdout


class TestSystemIsBannedOnlyThroughOs:
    """`system` starts a shell only as `os.system`.

    `platform.system()` is a pure in-process string read: no process, no
    effect, and NOTHING the effects handle could carry it -- the handle's
    closed method set has no in-process-observe method. A finding on it is
    unfixable by its own remediation ("route it through ctx.effects"), which
    is the one thing a lint must never emit. So the leaf is receiver-scoped,
    exactly as the network leaves already are.
    """

    def _run(self, tmp_path, source):
        app, root = _app(tmp_path)
        (root / "handlers.py").write_text(source)
        return app.test(["check", "--name", "effects-bypass"])

    def test_platform_system_is_not_a_finding(self, tmp_path):
        r = self._run(tmp_path, '''
import platform

def deploy(ctx):
    ctx.effects.run(["true"])
    return platform.system()
''')
        assert r.exit_code == 0, r.stdout
        assert "no direct effect calls bypass ctx.effects" in r.stdout

    def test_system_imported_from_platform_is_not_a_finding(self, tmp_path):
        r = self._run(tmp_path, '''
from platform import system

def deploy(ctx):
    ctx.effects.run(["true"])
    return system()
''')
        assert r.exit_code == 0, r.stdout

    def test_an_unknown_receiver_named_system_is_not_a_finding(self, tmp_path):
        """No resolvable binding to `os` is no evidence a process starts.

        `foo.system()` is some object's method. Receiver-awareness means the
        lint reports what it can show, and an unknown receiver shows nothing.
        """
        r = self._run(tmp_path, '''
def deploy(ctx, foo):
    ctx.effects.run(["true"])
    return foo.system()
''')
        assert r.exit_code == 0, r.stdout

    def test_os_system_is_still_a_finding(self, tmp_path):
        r = self._run(tmp_path, '''
import os

def deploy(ctx):
    ctx.effects.run(["true"])
    os.system("rm -rf /")
''')
        assert r.exit_code == 1
        assert "deploy calls os.system directly" in r.stdout

    def test_system_imported_from_os_is_a_finding(self, tmp_path):
        r = self._run(tmp_path, '''
from os import system

def deploy(ctx):
    ctx.effects.run(["true"])
    system("rm -rf /")
''')
        assert r.exit_code == 1, r.stdout
        assert "deploy calls system directly" in r.stdout

    def test_system_imported_from_os_under_an_alias_is_a_finding(self, tmp_path):
        r = self._run(tmp_path, '''
from os import system as shell_out

def deploy(ctx):
    ctx.effects.run(["true"])
    shell_out("rm -rf /")
''')
        assert r.exit_code == 1, r.stdout
        assert "deploy calls shell_out directly" in r.stdout

    def test_an_aliased_os_module_receiver_is_a_finding(self, tmp_path):
        r = self._run(tmp_path, '''
import os as o

def deploy(ctx):
    ctx.effects.run(["true"])
    o.system("rm -rf /")
''')
        assert r.exit_code == 1, r.stdout
        assert "deploy calls o.system directly" in r.stdout


class TestImportBindingsResolveReceivers:
    """An effect-module import is a receiver the analyser can resolve.

    The same binding table that keeps `platform.system` clean also closes two
    escapes it would otherwise open: an aliased effect module and a bare name
    imported from one.
    """

    def _run(self, tmp_path, source):
        app, root = _app(tmp_path)
        (root / "handlers.py").write_text(source)
        return app.test(["check", "--name", "effects-bypass"])

    def test_an_aliased_requests_module_is_a_network_finding(self, tmp_path):
        r = self._run(tmp_path, '''
import requests as rq

def deploy(ctx):
    ctx.effects.run(["true"])
    rq.post("https://x.test")
''')
        assert r.exit_code == 1, r.stdout
        assert "deploy calls rq.post directly" in r.stdout

    def test_a_bare_name_imported_from_subprocess_is_a_finding(self, tmp_path):
        r = self._run(tmp_path, '''
from subprocess import run

def deploy(ctx):
    ctx.effects.run(["true"])
    run(["git", "push"])
''')
        assert r.exit_code == 1, r.stdout
        assert "deploy calls run directly" in r.stdout

    def test_a_bare_name_from_an_unrelated_module_is_not_a_finding(self, tmp_path):
        """Only the closed list of effect modules binds a receiver.

        `from mylib import get` must stay silent -- widening the binding table
        to every module would re-create the `mapping.get(...)` noise the
        network receiver list exists to remove.
        """
        r = self._run(tmp_path, '''
from mylib import get

def deploy(ctx):
    ctx.effects.run(["true"])
    return get("k")
''')
        assert r.exit_code == 0, r.stdout


class TestObserveAllowlistBreadth:
    """§6.2's hazard, surfaced as a WARNING and never as an error.

    `proc_observe_allowlist=[["git"]]` makes EVERY git invocation an observe:
    it really executes under --dry-run, is never logged, and is legal in a
    read_only command. That may be exactly what the app wants -- the allowlist
    is a declared, source-visible choice that authorizes real execution in dry
    mode -- so the framework says so out loud instead of inventing a
    specificity rule.
    """

    def _app(self, tmp_path, allowlist):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(CHECKS_TOML)
        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
            proc_observe_allowlist=allowlist,
        )
        root = tmp_path / "src"
        root.mkdir(parents=True, exist_ok=True)
        app.set_check_context(lambda: SimpleContext(project_root=root))
        return app

    def test_metadata_is_warn_severity(self, tmp_path):
        app = self._app(tmp_path, [])
        r = app.test(["check", "--list", "--json"])
        entry = next(
            e for e in payload(r)
            if e["name"] == "observe-allowlist-breadth"
        )
        assert entry["severity"] == "warn"
        assert sorted(entry["tags"]) == ["effects", "quality"]

    def test_a_single_token_prefix_warns(self, tmp_path):
        app = self._app(tmp_path, [["git"]])
        r = app.test(["check", "--name", "observe-allowlist-breadth"])
        assert "WARN" in r.stdout
        assert "EVERY 'git' invocation becomes an observe" in r.stdout
        assert "really executes under --dry-run" in r.stdout

    def test_a_warning_is_not_an_error(self, tmp_path):
        """--ignore-warnings clears it; an error-severity check could not."""
        app = self._app(tmp_path, [["git"]])
        assert app.test([
            "check", "--name", "observe-allowlist-breadth", "--ignore-warnings",
        ]).exit_code == 0

    def test_multi_token_prefixes_pass(self, tmp_path):
        app = self._app(tmp_path, [["git", "status"], ["gh", "release", "view"]])
        r = app.test(["check", "--name", "observe-allowlist-breadth"])
        assert r.exit_code == 0
        assert "no single-token proc_observe_allowlist prefixes" in r.stdout

    def test_an_empty_allowlist_passes(self, tmp_path):
        app = self._app(tmp_path, [])
        assert app.test(["check", "--name", "observe-allowlist-breadth"]).exit_code == 0


class TestConsequentialGrantAgreement:
    """§8.1's declaration and §6.1's grants should almost always agree.

    A grant exists so a reviewer reading a preview sees WHY a dangerous step is
    there -- the same judgement `consequential` makes. The check fires only for
    the two kinds that leave this process (`proc_mutate` runs another program,
    `net_mutate` changes remote state); a `file_write` or a `proc_spawn` is
    local and ordinarily recoverable, and flagging those would re-create the
    noise the `consequential` declaration exists to remove.

    It is a WARNING, not an error, for the same reason: an error would push
    consumers to declare `consequential` reflexively to clear a gate, which is
    the exact reflex the redesign removed.
    """

    def _app(self, tmp_path, *, kind, consequential):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(CHECKS_TOML)
        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )

        @app.command("release", help="release", effect="mutating",
                     consequential=consequential,
                     grants=[strictcli.Grant(
                         name="push",
                         reason="the release engine owns remote refs",
                         kind=kind,
                     )])
        def _release(ctx):
            return 0

        root = tmp_path / "src"
        root.mkdir(parents=True, exist_ok=True)
        app.set_check_context(lambda: SimpleContext(project_root=root))
        return app

    def test_metadata_is_warn_severity(self, tmp_path):
        app = self._app(tmp_path, kind=strictcli.PROC_MUTATE,
                        consequential=True)
        r = app.test(["check", "--list", "--json"])
        entry = next(
            e for e in payload(r)
            if e["name"] == "consequential-grant-agreement"
        )
        assert entry["severity"] == "warn"
        assert sorted(entry["tags"]) == ["effects", "quality"]

    def test_a_proc_mutate_grant_without_consequential_warns(self, tmp_path):
        app = self._app(tmp_path, kind=strictcli.PROC_MUTATE,
                        consequential=False)
        r = app.test(["check", "--name", "consequential-grant-agreement"])
        assert "WARN" in r.stdout
        assert (
            "command 'release' declares grant 'push' (kind proc_mutate) but "
            "is not consequential"
        ) in r.stdout

    def test_a_net_mutate_grant_without_consequential_warns(self, tmp_path):
        app = self._app(tmp_path, kind=strictcli.NET_MUTATE,
                        consequential=False)
        r = app.test(["check", "--name", "consequential-grant-agreement"])
        assert "WARN" in r.stdout
        assert "kind net_mutate" in r.stdout

    def test_a_warning_is_not_an_error(self, tmp_path):
        app = self._app(tmp_path, kind=strictcli.PROC_MUTATE,
                        consequential=False)
        assert app.test([
            "check", "--name", "consequential-grant-agreement",
            "--ignore-warnings",
        ]).exit_code == 0

    def test_a_consequential_command_passes(self, tmp_path):
        app = self._app(tmp_path, kind=strictcli.PROC_MUTATE,
                        consequential=True)
        r = app.test(["check", "--name", "consequential-grant-agreement"])
        assert r.exit_code == 0
        assert "every escaping grant sits on a consequential command" in r.stdout

    def test_a_file_write_grant_is_not_flagged(self, tmp_path):
        app = self._app(tmp_path, kind=strictcli.FILE_WRITE,
                        consequential=False)
        assert app.test([
            "check", "--name", "consequential-grant-agreement",
        ]).exit_code == 0

    def test_a_proc_spawn_grant_is_not_flagged(self, tmp_path):
        app = self._app(tmp_path, kind=strictcli.PROC_SPAWN,
                        consequential=False)
        assert app.test([
            "check", "--name", "consequential-grant-agreement",
        ]).exit_code == 0

    def test_a_grouped_command_is_named_by_its_dotted_path(self, tmp_path):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(CHECKS_TOML)
        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )
        grp = app.group("release", help="release")

        @grp.command("run", help="run", effect="mutating",
                     grants=[strictcli.Grant(
                         name="push", reason="owns remote refs",
                         kind=strictcli.PROC_MUTATE)])
        def _run(ctx):
            return 0

        root = tmp_path / "src"
        root.mkdir(parents=True, exist_ok=True)
        app.set_check_context(lambda: SimpleContext(project_root=root))
        r = app.test(["check", "--name", "consequential-grant-agreement"])
        assert "command 'release.run' declares grant 'push'" in r.stdout

    def test_an_app_with_no_grants_passes(self, tmp_path):
        toml_file = tmp_path / "checks.toml"
        toml_file.write_text(CHECKS_TOML)
        app = strictcli.App(
            name="testapp", version="1.0.0", help="test app",
            checks_path=str(toml_file),
        )
        root = tmp_path / "src"
        root.mkdir(parents=True, exist_ok=True)
        app.set_check_context(lambda: SimpleContext(project_root=root))
        assert app.test([
            "check", "--name", "consequential-grant-agreement",
        ]).exit_code == 0
