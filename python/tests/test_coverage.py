"""Tests for the cli-test-coverage mechanism.

Verifies that:
- source_tree_root=<root holding .strictmetadata/.cli-test-coverage/> enables
  recording of command hits
- test() and call() both record to per-process shard files under that directory
- The cli-test-coverage check merges shards, compares against the command
  surface, and FAILs listing uncovered commands
- Full coverage produces a PASS
- Empty/stale manifest is a hard error
- A coverage directory that does not exist leaves the instrumentation off
- The retired options are refused by name
"""

import json
import os
from dataclasses import dataclass
from pathlib import Path

import pytest

import strictcli


@dataclass
class SimpleCtx:
    project_root: Path


# The coverage directory relative to the declared source-tree root, spelled out
# here rather than read from the implementation so the tests pin the layout.
COVERAGE_REL = ".strictmetadata/.cli-test-coverage"


def _source_tree_root(tmp_path):
    """The declared root, with its coverage directory created so coverage is on."""
    (tmp_path / COVERAGE_REL).mkdir(parents=True, exist_ok=True)
    return tmp_path


def _make_app(tmp_path):
    """Build a 3-command app whose source-tree root is tmp_path."""
    app = strictcli.App(
        name="coverapp", version="1.0.0", help="coverage test app",
        source_tree_root=str(_source_tree_root(tmp_path)),
    )

    @app.command(name="deploy", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="deploy the app")
    def cmd_deploy(ctx, **_kw):
        pass

    @app.command(name="status", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="show status")
    def cmd_status(ctx, **_kw):
        pass

    @app.command(name="build", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="build the app")
    def cmd_build(ctx, **_kw):
        pass

    app.set_check_context(lambda: SimpleCtx(project_root=tmp_path))
    return app


def _make_grouped_app(tmp_path):
    """Build an app with grouped commands for dotted-path coverage."""
    app = strictcli.App(
        name="grpapp", version="1.0.0", help="grouped coverage test",
        source_tree_root=str(_source_tree_root(tmp_path)),
    )

    grp = app.group("infra", help="infrastructure commands")

    @grp.command(name="deploy", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="deploy infra")
    def cmd_infra_deploy(ctx, **_kw):
        pass

    @grp.command(name="teardown", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="tear down infra")
    def cmd_infra_teardown(ctx, **_kw):
        pass

    @app.command(name="status", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="show status")
    def cmd_status(ctx, **_kw):
        pass

    app.set_check_context(lambda: SimpleCtx(project_root=tmp_path))
    return app


class TestCoverageRecording:
    def test_test_creates_shard_file(self, tmp_path):
        app = _make_app(tmp_path)
        app.test(["deploy"])

        coverage_dir = tmp_path / COVERAGE_REL / "shards"
        assert coverage_dir.is_dir()
        shards = list(coverage_dir.glob("*.jsonl"))
        assert len(shards) >= 1

        # One shard per process, named "<pid>.jsonl" (no shard counter suffix).
        assert shards[0].name == f"{os.getpid()}.jsonl"

        entries = []
        for shard in shards:
            for line in shard.read_text().strip().splitlines():
                entries.append(json.loads(line))

        commands = {e["command"] for e in entries}
        assert "deploy" in commands

    def test_call_records_coverage(self, tmp_path):
        app = _make_app(tmp_path)
        app.call("status")

        coverage_dir = tmp_path / COVERAGE_REL / "shards"
        shards = list(coverage_dir.glob("*.jsonl"))
        assert len(shards) >= 1

        entries = []
        for shard in shards:
            for line in shard.read_text().strip().splitlines():
                entries.append(json.loads(line))

        commands = {e["command"] for e in entries}
        assert "status" in commands

    def test_multiple_calls_accumulate(self, tmp_path):
        app = _make_app(tmp_path)
        app.test(["deploy"])
        app.test(["status"])
        app.call("build")

        coverage_dir = tmp_path / COVERAGE_REL / "shards"
        entries = []
        for shard in coverage_dir.glob("*.jsonl"):
            for line in shard.read_text().strip().splitlines():
                entries.append(json.loads(line))

        commands = {e["command"] for e in entries}
        assert commands == {"deploy", "status", "build"}

    def test_grouped_command_dotted_path(self, tmp_path):
        app = _make_grouped_app(tmp_path)
        app.test(["infra", "deploy"])

        coverage_dir = tmp_path / COVERAGE_REL / "shards"
        entries = []
        for shard in coverage_dir.glob("*.jsonl"):
            for line in shard.read_text().strip().splitlines():
                entries.append(json.loads(line))

        commands = {e["command"] for e in entries}
        assert "infra.deploy" in commands


class TestCoverageCheck:
    def test_partial_coverage_fails_naming_uncovered(self, tmp_path):
        """One command tested out of 3 -> check FAILs naming the untested two."""
        app = _make_app(tmp_path)
        # Only test one command
        app.test(["deploy"])

        results, _, code = app.run_checks(
            SimpleCtx(project_root=tmp_path),
            run_all=True,
        )
        cov_result = next(r for r in results if r.name == "cli-test-coverage")
        assert cov_result.status == "fail"
        # Should list the two uncovered commands
        problem_texts = [p.text for p in cov_result.problems]
        uncovered_cmds = set()
        for text in problem_texts:
            if "no test coverage for command:" in text:
                cmd = text.split("no test coverage for command: ")[1]
                uncovered_cmds.add(cmd)
        assert "status" in uncovered_cmds
        assert "build" in uncovered_cmds
        assert "deploy" not in uncovered_cmds

    def test_full_coverage_passes(self, tmp_path):
        """All 3 commands tested -> check PASSes."""
        app = _make_app(tmp_path)
        app.test(["deploy"])
        app.test(["status"])
        app.test(["build"])

        results, _, code = app.run_checks(
            SimpleCtx(project_root=tmp_path),
            run_all=True,
        )
        cov_result = next(r for r in results if r.name == "cli-test-coverage")
        assert cov_result.status == "pass"

    def test_zero_coverage_state_skips(self, tmp_path):
        """No shards and no manifest -> SKIP with a subject-matter reason.

        Subject-matter gating: when the anchored coverage root holds NEITHER a
        committed manifest NOR any shard files, this is not the app's own
        development tree (e.g. an installed app running its checks from a foreign
        project's cwd). The check reports a visible SKIP naming the anchored path
        rather than failing with the whole command surface listed as uncovered.
        """
        app = _make_app(tmp_path)
        # Don't run any test() or call() -- no shards, no manifest

        results, _, code = app.run_checks(
            SimpleCtx(project_root=tmp_path),
            run_all=True,
        )
        cov_result = next(r for r in results if r.name == "cli-test-coverage")
        assert cov_result.status == "skip"
        assert "development tree" in cov_result.message
        # Reason names the anchored coverage directory, not the foreign cwd.
        assert str(tmp_path / COVERAGE_REL) in cov_result.message

    def test_empty_manifest_file_present_fails_listing_all(self, tmp_path):
        """An empty-manifest file present means "coverage configured but empty"
        -> FAIL listing every command, NOT a skip. The skip class only triggers
        when NEITHER a manifest NOR any shards exist."""
        app = _make_app(tmp_path)
        manifest_path = tmp_path / COVERAGE_REL / "manifest.json"
        manifest_path.parent.mkdir(parents=True, exist_ok=True)
        manifest_path.write_text("[]\n")

        results, _, code = app.run_checks(
            SimpleCtx(project_root=tmp_path),
            run_all=True,
        )
        cov_result = next(r for r in results if r.name == "cli-test-coverage")
        assert cov_result.status == "fail"
        problem_texts = [p.text for p in cov_result.problems]
        uncovered = {
            t.split("no test coverage for command: ")[1]
            for t in problem_texts
            if "no test coverage for command:" in t
        }
        assert uncovered == {"build", "deploy", "status"}

    def test_manifest_written_on_check(self, tmp_path):
        """The check writes the coverage manifest with covered commands."""
        app = _make_app(tmp_path)
        app.test(["deploy"])
        app.test(["status"])
        app.test(["build"])

        app.run_checks(
            SimpleCtx(project_root=tmp_path),
            run_all=True,
        )

        manifest_path = tmp_path / COVERAGE_REL / "manifest.json"
        assert manifest_path.is_file()
        manifest = json.loads(manifest_path.read_text())
        assert sorted(manifest) == ["build", "deploy", "status"]

    def test_grouped_commands_coverage(self, tmp_path):
        """Grouped commands use dotted paths in coverage tracking."""
        app = _make_grouped_app(tmp_path)
        # Test all commands
        app.test(["infra", "deploy"])
        app.test(["infra", "teardown"])
        app.test(["status"])

        results, _, code = app.run_checks(
            SimpleCtx(project_root=tmp_path),
            run_all=True,
        )
        cov_result = next(r for r in results if r.name == "cli-test-coverage")
        assert cov_result.status == "pass"

    def test_grouped_partial_coverage_fails(self, tmp_path):
        """Missing a grouped command -> check FAILs naming it."""
        app = _make_grouped_app(tmp_path)
        app.test(["infra", "deploy"])
        app.test(["status"])
        # infra.teardown is not tested

        results, _, code = app.run_checks(
            SimpleCtx(project_root=tmp_path),
            run_all=True,
        )
        cov_result = next(r for r in results if r.name == "cli-test-coverage")
        assert cov_result.status == "fail"
        problem_texts = [p.text for p in cov_result.problems]
        assert any("infra.teardown" in t for t in problem_texts)


class TestCoverageChdirSafety:
    def test_record_anchored_to_declared_directory(self, tmp_path):
        """test() records into the declared coverage dir, not the cwd that a
        test happened to chdir into."""
        app = _make_app(tmp_path)  # declares tmp_path as the source-tree root
        other = tmp_path / "elsewhere"
        other.mkdir()
        os.chdir(other)

        app.test(["deploy"])

        declared_shards = list(
            (tmp_path / COVERAGE_REL / "shards").glob("*.jsonl")
        )
        assert declared_shards, "shard must be written under the declared dir"
        foreign = other / COVERAGE_REL / "shards"
        assert not foreign.exists(), "must not record into the chdir'd cwd"


class TestManifestUnionVerdict:
    def test_manifest_present_shards_absent_passes(self, tmp_path):
        """Committed manifest covering every command -> deterministic PASS even
        with no shard files (the machine never ran the suite)."""
        app = _make_app(tmp_path)
        manifest_path = tmp_path / COVERAGE_REL / "manifest.json"
        manifest_path.parent.mkdir(parents=True, exist_ok=True)
        manifest_path.write_text(
            json.dumps(["build", "deploy", "status"], indent=2) + "\n"
        )

        results, _, code = app.run_checks(
            SimpleCtx(project_root=tmp_path),
            run_all=True,
        )
        cov_result = next(r for r in results if r.name == "cli-test-coverage")
        assert cov_result.status == "pass"

    def test_check_from_foreign_cwd_reads_app_state(self, tmp_path):
        """The check evaluated from a foreign cwd reads the app's own repo state
        (the declared manifest), not the foreign directory."""
        app = _make_app(tmp_path)
        manifest_path = tmp_path / COVERAGE_REL / "manifest.json"
        manifest_path.parent.mkdir(parents=True, exist_ok=True)
        manifest_path.write_text(
            json.dumps(["build", "deploy", "status"], indent=2) + "\n"
        )
        other = tmp_path / "foreign"
        other.mkdir()
        os.chdir(other)

        results, _, code = app.run_checks(
            SimpleCtx(project_root=tmp_path),
            run_all=True,
        )
        cov_result = next(r for r in results if r.name == "cli-test-coverage")
        assert cov_result.status == "pass"

    def test_manifest_union_is_monotonic(self, tmp_path):
        """A run recording only a subset keeps prior commands covered (union)."""
        app = _make_app(tmp_path)
        manifest_path = tmp_path / COVERAGE_REL / "manifest.json"
        manifest_path.parent.mkdir(parents=True, exist_ok=True)
        manifest_path.write_text(
            json.dumps(["build", "deploy", "status"], indent=2) + "\n"
        )
        app.test(["deploy"])  # only one recorded this run

        app.run_checks(SimpleCtx(project_root=tmp_path), run_all=True)

        manifest = json.loads(manifest_path.read_text())
        assert sorted(manifest) == ["build", "deploy", "status"]

    def test_manifest_not_rewritten_when_unchanged(self, tmp_path):
        """A pure check must not dirty a byte-identical manifest."""
        app = _make_app(tmp_path)
        app.test(["deploy"])
        app.test(["status"])
        app.test(["build"])
        app.run_checks(SimpleCtx(project_root=tmp_path), run_all=True)

        manifest_path = tmp_path / COVERAGE_REL / "manifest.json"
        content1 = manifest_path.read_text()
        mtime1 = manifest_path.stat().st_mtime_ns

        app.run_checks(SimpleCtx(project_root=tmp_path), run_all=True)

        assert manifest_path.read_text() == content1
        assert manifest_path.stat().st_mtime_ns == mtime1


class TestCoverageDisabled:
    def test_no_recording_when_disabled(self, tmp_path):
        """An undeclared source_tree_root (the default) produces no shards."""
        os.chdir(tmp_path)
        app = strictcli.App(
            name="nocover", version="1.0.0", help="no coverage",
        )

        @app.command(name="greet", effect="read_only", forwarding=strictcli.Forwarding(reason="test handler absorbs global flag values"), help="say hello")
        def cmd_greet(ctx, **_kw):
            pass

        app.test(["greet"])

        coverage_dir = tmp_path / COVERAGE_REL / "shards"
        assert not coverage_dir.exists()


class TestCoverageDirectoryIsLazy:
    def test_construction_leaves_no_coverage_subdirectory(self, tmp_path):
        """Constructing an app with a declared coverage directory must not
        create coverage/ inside it -- a plain CLI invocation never records
        coverage, so it must not plant an empty directory."""
        app = _make_app(tmp_path)
        assert app is not None
        assert not (tmp_path / COVERAGE_REL / "shards").exists()

    def test_recording_creates_the_directory(self, tmp_path):
        """The recorder creates the coverage directory immediately before the
        first shard write."""
        app = _make_app(tmp_path)
        app.test(["deploy"])

        shard = tmp_path / COVERAGE_REL / "shards" / f"{os.getpid()}.jsonl"
        assert shard.is_file()

    def test_check_skips_when_coverage_state_absent(self, tmp_path):
        """The provider reads a declared root holding no coverage state without
        raising -- it reports the subject-matter SKIP."""
        app = _make_app(tmp_path)
        assert not (tmp_path / COVERAGE_REL / "shards").exists()

        results, _, _code = app.run_checks(
            SimpleCtx(project_root=tmp_path),
            run_all=True,
        )
        cov_result = next(r for r in results if r.name == "cli-test-coverage")
        assert cov_result.status == "skip"


class TestDeclaredDirectoryAbsent:
    """A coverage directory that does not exist leaves coverage off.

    This is the installed-wheel case: the declared root is not a source
    checkout once the CLI is installed elsewhere, so it holds no
    ``.strictmetadata/.cli-test-coverage/`` and the app registers no check,
    computes no paths and creates nothing.
    """

    def test_missing_directory_registers_no_check(self, tmp_path):
        missing = tmp_path / "nowhere"
        app = strictcli.App(
            name="coverapp", version="1.0.0", help="coverage test app",
            source_tree_root=str(missing),
        )

        @app.command(name="deploy", effect="read_only", help="deploy the app")
        def cmd_deploy(ctx):
            pass

        # No provider, so the check system never turns on: there is no `check`
        # command to route to, and no cli-test-coverage to list.
        result = app.test(["check", "--all"])
        assert result.exit_code == 1
        assert "cli-test-coverage" not in result.stdout
        with pytest.raises(ValueError, match="checks are not enabled"):
            app.run_checks(SimpleCtx(project_root=tmp_path), run_all=True)

    def test_missing_directory_creates_nothing(self, tmp_path):
        missing = tmp_path / "nowhere"
        app = strictcli.App(
            name="coverapp", version="1.0.0", help="coverage test app",
            source_tree_root=str(missing),
        )

        @app.command(name="deploy", effect="read_only", help="deploy the app")
        def cmd_deploy(ctx):
            pass

        app.test(["deploy"])
        app.call("deploy")

        assert not (tmp_path / "nowhere").exists()


class TestForeignDirectoryRun:
    """A consumer-style run from a foreign directory.

    The installed CLI is started in some unrelated project. Its declared
    coverage directory does not exist there, so `check` must neither list
    cli-test-coverage nor write a `.strictmetadata/` into the foreign directory.
    """

    def test_foreign_run_lists_no_check_and_touches_nothing(self, tmp_path):
        foreign = tmp_path / "some-other-project"
        foreign.mkdir()
        os.chdir(foreign)

        app = strictcli.App(
            name="coverapp", version="1.0.0", help="coverage test app",
            source_tree_root=str(tmp_path / "gone"),
        )

        @app.command(name="deploy", effect="read_only", help="deploy the app")
        def cmd_deploy(ctx):
            pass

        result = app.test(["check", "--all"])
        assert "cli-test-coverage" not in result.stdout

        assert not (foreign / ".strictmetadata").exists()
        assert list(foreign.iterdir()) == []


class TestRootWithoutCoverageDirectory:
    def test_existing_root_without_coverage_directory_is_off(self, tmp_path):
        """An installed wheel's declared root exists (the package's parent in
        site-packages) but holds no coverage directory: coverage stays off and
        nothing is created under the root."""
        app = strictcli.App(
            name="coverapp", version="1.0.0", help="coverage test app",
            source_tree_root=str(tmp_path),
        )

        @app.command(name="deploy", effect="read_only", help="deploy the app")
        def cmd_deploy(ctx):
            pass

        app.test(["deploy"])
        assert "cli-test-coverage" not in app.test(["check", "--all"]).stdout
        assert list(tmp_path.iterdir()) == []


class TestLayout:
    def test_shards_and_manifest_sit_under_the_root(self, tmp_path):
        app = _make_app(tmp_path)
        app.test(["deploy"])
        shard = (tmp_path / ".strictmetadata" / ".cli-test-coverage" / "shards"
                 / f"{os.getpid()}.jsonl")
        assert shard.is_file()
        app.run_checks(SimpleCtx(project_root=tmp_path), run_all=True)
        manifest = tmp_path / ".strictmetadata" / ".cli-test-coverage" / "manifest.json"
        assert "deploy" in json.loads(manifest.read_text())


class TestRetiredBooleanRefused:
    def test_boolean_true_is_refused_naming_the_root_option(self):
        with pytest.raises(ValueError) as exc:
            strictcli.App(
                name="coverapp", version="1.0.0", help="coverage test app",
                test_coverage=True,
            )
        assert str(exc.value) == (
            "test_coverage is not accepted; declare the source-tree root with "
            "source_tree_root, which keeps test coverage in "
            ".strictmetadata/.cli-test-coverage/ under it"
        )

    def test_directory_option_is_refused_naming_the_root_option(self, tmp_path):
        with pytest.raises(ValueError) as exc:
            strictcli.App(
                name="coverapp", version="1.0.0", help="coverage test app",
                test_coverage_dir=str(tmp_path),
            )
        assert str(exc.value) == (
            "test_coverage_dir is not accepted; declare the source-tree root "
            "with source_tree_root, which keeps test coverage in "
            ".strictmetadata/.cli-test-coverage/ under it"
        )

    def test_boolean_false_is_refused_too(self):
        """There is no accepted spelling of the retired option, not even the
        one that used to mean "off" -- absence is how coverage is declared off."""
        with pytest.raises(ValueError) as exc:
            strictcli.App(
                name="coverapp", version="1.0.0", help="coverage test app",
                test_coverage=False,
            )
        assert "source_tree_root" in str(exc.value)
