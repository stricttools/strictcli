"""Global-flag config-conflict position + post-command provenance.

Global flags may appear before the command (``tool --g X cmd``) or after it
(``tool cmd --g X``). Config-conflict detection (error mode) and source
provenance must behave identically regardless of position.
"""

import json
import os
import tempfile

import strictcli


def _write_config(data):
    f = tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False)
    json.dump(data, f)
    f.close()
    return f.name


def _app(conflict_mode="error", flag_conflict_mode=strictcli._MISSING,
         config=None, env=None, default="default-val"):
    """App with a global str flag ``settings`` and a bare ``run`` command."""
    config_path = _write_config(config) if config is not None else None
    kwargs = dict(name="myapp", version="1.0.0", help="test app")
    if config_path is not None:
        kwargs.update(config=True, config_path=config_path,
                      config_conflict_mode=conflict_mode)
    app = strictcli.App(
        flags=[strictcli.Flag(name="settings", type=str, default=default,
                              help="settings path", env=env,
                              conflict_mode=flag_conflict_mode)],
        **kwargs,
    )

    @app.command("run", effect="read_only", help="run something")
    def run(ctx, settings):
        print(f"settings={settings}")

    app._config_path_for_cleanup = config_path
    return app


# ---------------------------------------------------------------------------
# Conflict detection: post-command position
# ---------------------------------------------------------------------------

def test_post_command_global_conflict_fires_app_error_mode():
    """`run --settings X` with divergent config in error mode is a hard error."""
    app = _app(conflict_mode="error", config={"settings": "from-config"})
    try:
        r = app.test(["run", "--settings", "from-cli"])
        assert r.exit_code == 1, r.stdout
        assert "flag 'settings' set in both cli and config; remove one" in r.stderr
    finally:
        os.unlink(app._config_path_for_cleanup)


def test_post_command_global_conflict_fires_per_flag_error_mode():
    """Per-flag error mode beats app-level cli-wins for post-command globals."""
    app = _app(conflict_mode="cli-wins", flag_conflict_mode="error",
               config={"settings": "from-config"})
    try:
        r = app.test(["run", "--settings", "from-cli"])
        assert r.exit_code == 1, r.stdout
        assert "flag 'settings' set in both cli and config; remove one" in r.stderr
    finally:
        os.unlink(app._config_path_for_cleanup)


def test_pre_command_global_conflict_still_fires():
    """Regression: pre-command position still detects the conflict."""
    app = _app(conflict_mode="error", config={"settings": "from-config"})
    try:
        r = app.test(["--settings", "from-cli", "run"])
        assert r.exit_code == 1, r.stdout
        assert "flag 'settings' set in both cli and config; remove one" in r.stderr
    finally:
        os.unlink(app._config_path_for_cleanup)


def test_post_command_global_matching_value_passes():
    """Identical config + post-command CLI value does not conflict."""
    app = _app(conflict_mode="error", config={"settings": "same"})
    try:
        r = app.test(["run", "--settings", "same"])
        assert r.exit_code == 0, r.stderr
        assert "settings=same" in r.stdout
    finally:
        os.unlink(app._config_path_for_cleanup)


def test_post_command_global_allow_mode_silent_wins():
    """cli-wins mode: post-command global silently overrides config."""
    app = _app(conflict_mode="cli-wins", config={"settings": "from-config"})
    try:
        r = app.test(["run", "--settings", "from-cli"])
        assert r.exit_code == 0, r.stderr
        assert "settings=from-cli" in r.stdout
    finally:
        os.unlink(app._config_path_for_cleanup)


def test_env_matched_then_post_cli_diverges_names_cli_and_config(monkeypatch):
    """Env matches config (no pre-command conflict); post-command CLI diverges.

    The error names 'cli and config' (simple form) -- env dropped out because
    the post-command CLI value is what conflicts with config.
    """
    monkeypatch.setenv("MYAPP_SETTINGS", "shared")
    app = _app(conflict_mode="error", env="MYAPP_SETTINGS",
               config={"settings": "shared"})
    try:
        r = app.test(["run", "--settings", "from-cli"])
        assert r.exit_code == 1, r.stdout
        assert "flag 'settings' set in both cli and config; remove one" in r.stderr
    finally:
        os.unlink(app._config_path_for_cleanup)


# ---------------------------------------------------------------------------
# Provenance: post-command position
# ---------------------------------------------------------------------------

def test_post_command_global_provenance_is_cli():
    """A global flag set post-command reports source 'cli', not 'default'."""
    app = _app(conflict_mode="cli-wins")
    r = app.test(["run", "--settings", "from-cli"])
    assert r.exit_code == 0, r.stderr
    assert app._last_sources["settings"] == "cli"


def test_pre_command_global_provenance_is_cli():
    """Regression: a global flag set pre-command still reports source 'cli'."""
    app = _app(conflict_mode="cli-wins")
    r = app.test(["--settings", "from-cli", "run"])
    assert r.exit_code == 0, r.stderr
    assert app._last_sources["settings"] == "cli"


def test_post_command_global_provenance_config_when_not_on_cli():
    """When the global is only in config, provenance stays 'config'."""
    app = _app(conflict_mode="cli-wins", config={"settings": "from-config"})
    try:
        r = app.test(["run"])
        assert r.exit_code == 0, r.stderr
        assert app._last_sources["settings"] == "config"
    finally:
        os.unlink(app._config_path_for_cleanup)
