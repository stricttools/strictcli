"""Retired choices: the spellings a value flag or arg used to accept.

The value-level twin of the deprecated-command construct. A retired spelling
carries the message that names its replacement, is refused at parse time ahead
of the invalid-value check, and is never a choice -- so help, the published
`value_schema` enum, and the MCP projection derived from it name the live set
only.
"""

import pytest

import strictcli


@pytest.fixture
def in_project_dir(tmp_path, monkeypatch):
    """A project directory the dump can resolve, entered BEFORE the app is built.

    App construction captures the project root, so the chdir has to precede it
    -- which is why every schema test below takes this fixture rather than
    changing directory for itself.
    """
    (tmp_path / "pyproject.toml").write_text('[project]\nname = "testproject"\n')
    monkeypatch.chdir(tmp_path)
    return tmp_path


def _flag_app(**flag_kwargs):
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag("format", help="output format", **flag_kwargs)
    def cmd(ctx, format):
        print(f"format={format}")

    return app


def _arg_app(**arg_kwargs):
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.arg("mode", help="the mode", **arg_kwargs)
    def cmd(ctx, mode):
        print(f"mode={mode}")

    return app


LIVE = [strictcli.Choice("text"), strictcli.Choice("json")]
RETIRED_XML = [strictcli.RetiredChoice("xml", message="use 'json' (XML output was dropped)")]


# --- Parse time: the refusal ---


def test_a_retired_flag_value_is_refused():
    app = _flag_app(choices=LIVE, retired_choices=RETIRED_XML, presence="required")
    r = app.test(["cmd", "--format", "xml"])
    assert r.exit_code == 1
    assert (
        "--format: value 'xml' retired: use 'json' (XML output was dropped)"
        in r.stderr
    )


def test_a_retired_arg_value_is_refused():
    app = _arg_app(
        choices=[strictcli.Choice("fast"), strictcli.Choice("slow")],
        retired_choices=[
            strictcli.RetiredChoice("turbo", message="use 'fast' (turbo was renamed)")
        ],
        presence="required",
    )
    r = app.test(["cmd", "turbo"])
    assert r.exit_code == 1
    assert (
        "argument 'mode': value 'turbo' retired: use 'fast' (turbo was renamed)"
        in r.stderr
    )


def test_retired_beats_invalid_value():
    """The retired sentence wins: it names the replacement, which the list cannot."""
    app = _flag_app(choices=LIVE, retired_choices=RETIRED_XML, presence="required")
    r = app.test(["cmd", "--format", "xml"])
    assert "must be one of" not in r.stderr


def test_an_unrelated_bad_value_still_takes_the_invalid_value_sentence():
    app = _flag_app(choices=LIVE, retired_choices=RETIRED_XML, presence="required")
    r = app.test(["cmd", "--format", "yaml"])
    assert r.exit_code == 1
    assert "invalid value 'yaml', must be one of: text, json" in r.stderr


def test_a_live_value_is_still_accepted():
    app = _flag_app(choices=LIVE, retired_choices=RETIRED_XML, presence="required")
    r = app.test(["cmd", "--format", "json"])
    assert r.exit_code == 0
    assert "format=json" in r.stdout


def test_a_retired_value_from_an_env_var_is_refused(monkeypatch):
    """Every source that resolves through the choices check takes the refusal."""
    monkeypatch.setenv("TEST_FORMAT", "xml")
    app = _flag_app(
        choices=LIVE, retired_choices=RETIRED_XML, env="TEST_FORMAT", presence="required"
    )
    r = app.test(["cmd"])
    assert r.exit_code == 1
    assert "value 'xml' retired: use 'json'" in r.stderr


def test_a_retired_int_spelling_formats_through_the_error_value_formatter():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag(
        "port",
        type=int,
        help="the port",
        choices=[strictcli.Choice(80), strictcli.Choice(443)],
        retired_choices=[
            strictcli.RetiredChoice(
                8080, message="use 443 (the plaintext port was dropped)"
            )
        ],
        presence="required",
    )
    def cmd(ctx, port):
        print(f"port={port}")

    r = app.test(["cmd", "--port", "8080"])
    assert r.exit_code == 1
    assert (
        "--port: value '8080' retired: use 443 (the plaintext port was dropped)"
        in r.stderr
    )


def test_a_retired_element_of_a_repeatable_flag():
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag(
        "tag",
        help="a tag",
        repeatable=True,
        unique=False,
        choices=[strictcli.Choice("alpha"), strictcli.Choice("beta")],
        retired_choices=[strictcli.RetiredChoice("gamma", message="use 'beta'")],
        default=[],
    )
    def cmd(ctx, tag):
        print(f"tag={tag}")

    r = app.test(["cmd", "--tag", "alpha", "--tag", "gamma"])
    assert r.exit_code == 1
    assert "--tag: value 'gamma' retired: use 'beta'" in r.stderr


# --- Help output is unchanged ---


def test_help_never_lists_a_retired_value():
    app = _flag_app(choices=LIVE, retired_choices=RETIRED_XML, presence="required")
    r = app.test(["cmd", "--help"])
    assert r.exit_code == 0
    assert "choices: text, json" in r.stdout
    assert "xml" not in r.stdout


# --- Registration-time hard errors ---


def test_a_retired_spelling_equal_to_a_live_choice_is_refused():
    with pytest.raises(ValueError) as exc:
        _flag_app(
            choices=LIVE,
            retired_choices=[strictcli.RetiredChoice("json", message="use 'text'")],
            presence="required",
        )
    assert (
        'Flag "format": retired choice \'json\' is also a live choice: '
        "a value is live or retired, never both" in str(exc.value)
    )


def test_an_arg_retired_spelling_equal_to_a_live_choice_is_refused():
    with pytest.raises(ValueError) as exc:
        _arg_app(
            choices=[strictcli.Choice("fast"), strictcli.Choice("slow")],
            retired_choices=[strictcli.RetiredChoice("fast", message="use 'slow'")],
            presence="required",
        )
    assert (
        'Arg "mode": retired choice \'fast\' is also a live choice: '
        "a value is live or retired, never both" in str(exc.value)
    )


def test_a_duplicate_retired_spelling_is_refused():
    with pytest.raises(ValueError) as exc:
        _flag_app(
            choices=LIVE,
            retired_choices=[
                strictcli.RetiredChoice("xml", message="use 'json'"),
                strictcli.RetiredChoice("xml", message="use 'text'"),
            ],
            presence="required",
        )
    assert 'Flag "format": retired choice \'xml\' is declared twice' in str(exc.value)


def test_an_empty_retired_message_is_refused():
    with pytest.raises(ValueError) as exc:
        _flag_app(
            choices=LIVE,
            retired_choices=[strictcli.RetiredChoice("xml", message="")],
            presence="required",
        )
    assert (
        'Flag "format": retired choice \'xml\': message must be a non-empty string'
        in str(exc.value)
    )


def test_retired_choices_on_a_bool_are_refused():
    app = strictcli.App(name="test", version="1.0.0", help="test app")
    with pytest.raises(ValueError) as exc:

        @app.command("cmd", effect="read_only", help="a command")
        @strictcli.flag(
            "cache",
            type=bool,
            help="use the cache",
            retired_choices=[strictcli.RetiredChoice("on", message="use --cache")],
            default=False,
        )
        def cmd(ctx, cache):
            pass

    assert 'Flag "cache": retired choices are incompatible with type=bool' in str(
        exc.value
    )


def test_a_default_naming_a_retired_spelling_is_refused():
    with pytest.raises(ValueError) as exc:
        _flag_app(choices=LIVE, retired_choices=RETIRED_XML, default="xml")
    assert 'Flag "format": default \'xml\' is a retired choice' in str(exc.value)


def test_an_arg_default_naming_a_retired_spelling_is_refused():
    with pytest.raises(ValueError) as exc:
        _arg_app(
            choices=[strictcli.Choice("fast"), strictcli.Choice("slow")],
            retired_choices=[strictcli.RetiredChoice("turbo", message="use 'fast'")],
            default="turbo",
        )
    assert 'Arg "mode": default \'turbo\' is a retired choice' in str(exc.value)


def test_a_retired_spelling_of_the_wrong_type_is_refused():
    """The seventh guard: a declaration that could never match.

    `RetiredChoice(8080, ...)` on a str flag is a dead declaration -- the
    parse-time comparison is type-aware, so no invocation could ever reach it.
    """
    with pytest.raises(ValueError) as exc:
        _flag_app(
            choices=LIVE,
            retired_choices=[strictcli.RetiredChoice(8080, message="use 'json'")],
            presence="required",
        )
    assert (
        'Flag "format": retired choice \'8080\' is not of type str' in str(exc.value)
    )


def test_an_arg_retired_spelling_of_the_wrong_type_is_refused():
    with pytest.raises(ValueError) as exc:
        _arg_app(
            choices=[strictcli.Choice("fast"), strictcli.Choice("slow")],
            retired_choices=[strictcli.RetiredChoice(8080, message="use 'fast'")],
            presence="required",
        )
    assert (
        'Arg "mode": retired choice \'8080\' is not of type str' in str(exc.value)
    )


def test_retired_choices_without_choices_are_refused():
    with pytest.raises(ValueError) as exc:
        _flag_app(retired_choices=RETIRED_XML, presence="required")
    assert 'Flag "format": retired choices require choices' in str(exc.value)


def test_arg_retired_choices_without_choices_are_refused():
    with pytest.raises(ValueError) as exc:
        _arg_app(
            retired_choices=[strictcli.RetiredChoice("turbo", message="use 'fast'")],
            presence="required",
        )
    assert 'Arg "mode": retired choices require choices' in str(exc.value)


# Python alone can write a bare entry: the keyword takes a list of anything,
# where Go's variadic parameter and TypeScript's record type refuse it at
# compile time. The refusal mirrors the bare-choice one.
@pytest.mark.parametrize("entry", ["xml", ("xml", "use 'json'")])
def test_a_bare_retired_entry_is_refused(entry):
    with pytest.raises(ValueError) as exc:
        _flag_app(choices=LIVE, retired_choices=[entry], presence="required")
    assert (
        'Flag "format": retired_choices entry 0 is a bare value: declare it as '
        'RetiredChoice(<value>, message="<message>")' in str(exc.value)
    )


# --- The schema dump ---


def _dump(app, project_dir):
    result = app.test(["--dump-schema"])
    assert result.exit_code == 0, result.stderr
    return (project_dir / ".strictcli" / "schema.json").read_text()


def test_the_schema_publishes_retired_choices_sorted_by_key(in_project_dir):
    app = strictcli.App(name="test", version="1.0.0", help="test app")

    @app.command("cmd", effect="read_only", help="a command")
    @strictcli.flag(
        "format",
        help="output format",
        choices=LIVE,
        retired_choices=[
            strictcli.RetiredChoice("xml", message="use 'json'"),
            strictcli.RetiredChoice("ascii", message="use 'text'"),
        ],
        presence="required",
    )
    @strictcli.arg(
        "mode",
        help="the mode",
        choices=[strictcli.Choice("fast")],
        retired_choices=[strictcli.RetiredChoice("turbo", message="use 'fast'")],
        presence="required",
    )
    def cmd(ctx, format, mode):
        pass

    text = _dump(app, in_project_dir)
    assert (
        """          "retired_choices": {
            "ascii": "use 'text'",
            "xml": "use 'json'"
          }"""
        in text
    )
    assert (
        """"retired_choices": {
            "turbo": "use 'fast'"
          }"""
        in text
    )


def test_the_schema_omits_retired_choices_when_none_are_declared(in_project_dir):
    app = _flag_app(choices=LIVE, presence="required")
    assert "retired_choices" not in _dump(app, in_project_dir)


def test_the_value_schema_fragment_excludes_retired_spellings(in_project_dir):
    import json

    app = _flag_app(choices=LIVE, retired_choices=RETIRED_XML, presence="required")
    doc = json.loads(_dump(app, in_project_dir))
    fragment = doc["commands"]["cmd"]["flags"][0]["value_schema"]
    assert fragment["enum"] == ["text", "json"]


def test_the_tool_projection_excludes_retired_spellings():
    app = _flag_app(choices=LIVE, retired_choices=RETIRED_XML, presence="required")
    tools = app.as_tools()
    assert "xml" not in repr(tools[0])
