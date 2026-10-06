"""The declared payload schema's validator (effects contract §19.5).

Two duties, one closed subset: registration-time validation of the declared
literal and emission-time validation of the value a handler supplies through
``ctx.payload``.

The bulk of the coverage is the committed cross-language vector file at
``go/strictcli/testdata/payload_schema_vectors.json``, replayed here and by the Go and
TypeScript suites. Every vector pins both the verdict AND the exact error text,
which is what makes the three validators byte-identical rather than merely
similarly-strict.

The tests that cannot be shared vectors -- values JSON has no way to carry, and
the framework's own wiring -- are written natively below.
"""

from __future__ import annotations

import json
import math
from pathlib import Path

import pytest

import strictcli

VECTORS_PATH = (
    Path(__file__).resolve().parents[2]
    / "go" / "strictcli" / "testdata"
    / "payload_schema_vectors.json"
)

_DOC = json.loads(VECTORS_PATH.read_text(encoding="utf-8"))
SCHEMA_VECTORS = _DOC["schema_vectors"]
INSTANCE_VECTORS = _DOC["instance_vectors"]


def _applicable(vectors: list[dict]) -> list[dict]:
    """Drop the vectors this implementation structurally cannot present."""
    return [
        v for v in vectors if "python" not in v.get("unrepresentable_in", [])
    ]


# ---------------------------------------------------------------------------
# The vector file itself
# ---------------------------------------------------------------------------


class TestVectorFile:
    def test_counts_match_the_header(self):
        assert _DOC["schema_vector_count"] == len(SCHEMA_VECTORS)
        assert _DOC["instance_vector_count"] == len(INSTANCE_VECTORS)

    def test_substantial(self):
        assert len(SCHEMA_VECTORS) >= 50
        assert len(INSTANCE_VECTORS) >= 140

    def test_every_exclusion_carries_a_reason(self):
        for v in SCHEMA_VECTORS + INSTANCE_VECTORS:
            if "unrepresentable_in" in v:
                assert v["unrepresentable_in"], v["name"]
                assert v.get("unrepresentable_reason", "").strip(), v["name"]

    def test_no_vector_excludes_every_implementation(self):
        for v in SCHEMA_VECTORS + INSTANCE_VECTORS:
            excluded = set(v.get("unrepresentable_in", []))
            assert excluded != {"python", "go", "typescript"}, v["name"]


# ---------------------------------------------------------------------------
# Registration-time vectors
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "vec", _applicable(SCHEMA_VECTORS), ids=lambda v: v["name"]
)
def test_schema_vector(vec):
    found = strictcli._validate_payload_schema(vec["schema"])
    if vec["valid"]:
        assert found is None, f"unexpectedly rejected: {found}"
    else:
        assert found is not None, "unexpectedly accepted"
        assert found[0] == vec["path"]
        assert found[1] == vec["detail"]


# ---------------------------------------------------------------------------
# Emission-time vectors
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "vec", _applicable(INSTANCE_VECTORS), ids=lambda v: v["name"]
)
def test_instance_vector(vec):
    # Every instance vector's schema must itself be a legal declaration.
    assert strictcli._validate_payload_schema(vec["schema"]) is None
    found = strictcli._validate_payload_value(vec["value"], vec["schema"])
    if vec["valid"]:
        assert found is None, f"unexpectedly rejected: {found}"
    else:
        assert found is not None, "unexpectedly accepted"
        assert found[0] == vec["path"]
        assert found[1] == vec["detail"]


# ---------------------------------------------------------------------------
# Values JSON cannot carry, so they cannot be shared vectors
# ---------------------------------------------------------------------------


class TestNonRepresentableValues:
    @pytest.mark.parametrize(
        "value",
        [
            float("nan"),
            float("inf"),
            float("-inf"),
            object(),
            {1: "a"},
            {"a": object()},
            [object()],
            b"bytes",
            {"a", "b"},
        ],
    )
    def test_rejected(self, value):
        found = strictcli._validate_payload_value(value, {})
        assert found is not None
        assert found[1] == "the value is not representable in JSON"

    def test_the_path_names_the_offending_node(self):
        found = strictcli._validate_payload_value(
            {"a": [0, float("nan")]}, {}
        )
        assert found == (
            'payload["a"][1]', "the value is not representable in JSON"
        )

    def test_a_tuple_is_an_array(self):
        assert strictcli._validate_payload_value(
            (1, 2), {"type": "array", "items": {"type": "integer"}}
        ) is None

    def test_a_registration_time_const_must_be_representable(self):
        found = strictcli._validate_payload_schema({"const": float("nan")})
        assert found == (
            "payload_schema.const", "the value is not representable in JSON"
        )

    def test_a_registration_time_enum_entry_must_be_representable(self):
        found = strictcli._validate_payload_schema({"enum": ["a", object()]})
        assert found == (
            "payload_schema.enum[1]",
            "the value is not representable in JSON",
        )

    def test_a_schema_that_is_not_an_object_names_unsupported(self):
        found = strictcli._validate_payload_schema(
            {"properties": {"a": object()}}
        )
        assert found == (
            'payload_schema.properties["a"]',
            "a schema must be an object, got unsupported",
        )


# ---------------------------------------------------------------------------
# The magnitude guard's exact boundary in Python's unbounded integers
# ---------------------------------------------------------------------------


class TestMagnitudeGuard:
    def test_two_to_the_53_plus_one_is_rejected_exactly(self):
        found = strictcli._validate_payload_value(2 ** 53 + 1, {})
        assert found is not None
        assert found[1].startswith("the number's magnitude exceeds 2^53")

    def test_a_huge_integer_never_reaches_a_float_conversion(self):
        found = strictcli._validate_payload_value(10 ** 400, {})
        assert found is not None
        assert found[1].startswith("the number's magnitude exceeds 2^53")

    def test_the_largest_finite_double_is_rejected(self):
        found = strictcli._validate_payload_value(
            math.ldexp(1, 1023), {"type": "number"}
        )
        assert found is not None
        assert found[1].startswith("the number's magnitude exceeds 2^53")


# ---------------------------------------------------------------------------
# Registration wiring
# ---------------------------------------------------------------------------


class TestRegistrationWiring:
    def test_an_unknown_keyword_is_a_registration_error(self):
        app = strictcli.App(name="t", help="t", version="1")
        with pytest.raises(ValueError) as e:
            @app.command(
                "run", effect="read_only", help="run",
                payload_schema={"type": "object", "minProperties": 1},
            )
            def _run(ctx):
                return 0
        assert str(e.value) == (
            'command "run": payload schema is invalid at payload_schema: '
            'unknown keyword "minProperties" (the closed subset is: '
            "additionalProperties, const, enum, items, properties, required, "
            "type)"
        )

    def test_a_nested_unknown_keyword_names_its_path(self):
        app = strictcli.App(name="t", help="t", version="1")
        with pytest.raises(ValueError, match=r'payload_schema\.properties\["a"\]'):
            @app.command(
                "run", effect="read_only", help="run",
                payload_schema={
                    "type": "object",
                    "properties": {"a": {"type": "string", "maxLength": 3}},
                },
            )
            def _run(ctx):
                return 0

    def test_an_empty_schema_is_legal(self):
        app = strictcli.App(name="t", help="t", version="1")

        @app.command("run", effect="read_only", help="run", payload_schema={})
        def _run(ctx):
            ctx.payload({"anything": [1, 2]})
            return 0

        assert app.test(["run"]).exit_code == 0

    def test_a_group_command_is_validated_too(self):
        app = strictcli.App(name="t", help="t", version="1")
        grp = app.group("grp", help="g")
        with pytest.raises(ValueError, match="unknown keyword"):
            @grp.command(
                "run", effect="read_only", help="run",
                payload_schema={"pattern": "^a"},
            )
            def _run(ctx):
                return 0


# ---------------------------------------------------------------------------
# Emission wiring
# ---------------------------------------------------------------------------


class TestEmissionWiring:
    def _app(self, schema, value):
        app = strictcli.App(name="t", help="t", version="1")

        @app.command(
            "run", effect="read_only", help="run", payload_schema=schema
        )
        def _run(ctx):
            ctx.payload(value)
            return 0

        return app

    def test_a_matching_payload_passes(self):
        app = self._app(
            {"type": "object", "properties": {"a": {"type": "integer"}}},
            {"a": 1},
        )
        assert app.test(["run", "--json"]).exit_code == 0

    def test_a_deviating_payload_fails_at_the_emission_seam(self):
        app = self._app(
            {"type": "object", "properties": {"a": {"type": "integer"}}},
            {"a": "x"},
        )
        with pytest.raises(RuntimeError) as e:
            app.test(["run", "--json"])
        assert str(e.value) == (
            'command "run": payload does not satisfy the declared schema at '
            'payload["a"]: expected type "integer", got string'
        )

    def test_human_mode_never_validates_the_value(self):
        # §19.4's call-unconditionally rule: a handler builds its payload the
        # same way in both modes, so a value machine mode could not carry must
        # not fail a run that was never going to emit an envelope.
        app = self._app({"type": "array"}, {"a": 1})
        assert app.test(["run"]).exit_code == 0

    def test_the_slot_is_taken_by_the_first_call_even_when_it_deviates(self):
        # The one-slot rule (§19.4) is a call-time rule and is independent of
        # the value: the second call is refused, and the deviating first value
        # is what the emission seam then refuses.
        app = strictcli.App(name="t", help="t", version="1")
        seen = {}

        @app.command(
            "run", effect="read_only", help="run",
            payload_schema={"type": "array"},
        )
        def _run(ctx):
            ctx.payload({"a": 1})
            try:
                ctx.payload([1, 2])
            except RuntimeError as exc:
                seen["second"] = str(exc)
            return 0

        with pytest.raises(RuntimeError, match="does not satisfy"):
            app.test(["run", "--json"])
        assert seen["second"] == (
            'command "run": ctx.payload was already called '
            "(a dispatch carries at most one payload)"
        )

    def test_call_captures_without_validating(self):
        # call() writes no envelope, so there is no emission seam to validate
        # at: it returns the value the handler supplied.
        app = self._app({"type": "string"}, 1)
        assert app.call("run") == 1


# ---------------------------------------------------------------------------
# The framework's own commands declare schemas their payloads satisfy
# ---------------------------------------------------------------------------


class TestFrameworkOwnedSchemas:
    def test_check_list_payload_satisfies_its_declaration(self, tmp_path):
        toml = tmp_path / "checks.toml"
        toml.write_text(
            'app = "t"\n\n[checks.one]\ndescription = \"Checks one\"\nsubject = \"quality\"\ntags = ["a"]\nseverity = "error"\n'
            "fast = true\npure = true\nneeds_network = false\ndepends_on = []\n"
        )
        app = strictcli.App(
            name="t", help="t", version="1", checks_path=str(toml)
        )

        @app.error_check("one")
        def _one(ctx, reporter):
            return reporter.passed("ok")

        r = app.test(["check", "--list", "--json"])
        assert r.exit_code == 0
        payload = json.loads(r.stdout)["payload"]
        assert strictcli._validate_payload_value(
            payload, strictcli._CHECK_PAYLOAD_SCHEMA
        ) is None

    def test_config_show_payload_satisfies_its_declaration(self, tmp_path):
        app = strictcli.App(
            name="t", help="t", version="1", config=True,
            config_path=str(tmp_path / "config.json"),
        )

        @app.command("run", effect="read_only", help="run")
        @strictcli.flag("count", type=int, help="how many", default=0)
        def _run(ctx, count):
            return 0

        r = app.test(["config", "show", "--json"])
        assert r.exit_code == 0
        payload = json.loads(r.stdout)["payload"]
        assert strictcli._validate_payload_value(
            payload, strictcli._CONFIG_SHOW_PAYLOAD_SCHEMA
        ) is None


class TestConfigShowWithAnUnrepresentableConfigValue:
    """The case that moved instance validation to the emission seam.

    A config file may legitimately hold a float above 2^53, which §19.5's
    magnitude guard refuses. The human rendering must be unaffected -- and it
    is, because the guard runs only where the envelope is written.
    """

    def _app(self, tmp_path):
        cfg = tmp_path / "config.json"
        cfg.write_text('{"size": 1e17}', encoding="utf-8")
        app = strictcli.App(
            name="t", help="t", version="1", config=True, config_path=str(cfg),
        )

        @app.command("run", effect="read_only", help="run")
        @strictcli.flag("size", type=float, help="how big", default=0.0)
        def _run(ctx, size):
            return 0

        return app

    def test_the_human_rendering_is_unaffected(self, tmp_path):
        r = self._app(tmp_path).test(["config", "show", "--plain"])
        assert r.exit_code == 0
        assert "size = 100000000000000000.0  (source: config)" in r.stdout
        assert r.stderr == ""

    def test_machine_mode_refuses_it_with_the_magnitude_message(self, tmp_path):
        app = self._app(tmp_path)
        with pytest.raises(RuntimeError) as e:
            app.test(["config", "show", "--json"])
        assert str(e.value) == (
            'command "show": payload does not satisfy the declared schema at '
            'payload["size"]["value"]: the number\'s magnitude exceeds 2^53 '
            "(declare a big identifier as a string)"
        )


# ---------------------------------------------------------------------------
# Builder sugar (contract §19.5, decision 14)
# ---------------------------------------------------------------------------

BUILDERS_PATH = (
    Path(__file__).resolve().parents[2]
    / "go" / "strictcli" / "testdata"
    / "payload_schema_builders.json"
)
_BUILDERS = json.loads(BUILDERS_PATH.read_text(encoding="utf-8"))


def _build(name: str) -> dict:
    """Construct one fixture entry through the builders."""
    s = strictcli
    if name == "type: one name":
        return s.schema_type("string")
    if name == "type: a list for nullability":
        return s.schema_type("string", "null")
    if name == "type: every json type":
        return s.schema_type(
            "array", "boolean", "integer", "null", "number", "object", "string"
        )
    if name == "array: items":
        return s.schema_array(s.schema_type("integer"))
    if name == "array: items is itself a built object":
        return s.schema_array(
            s.schema_object(properties={"a": s.schema_type("string")})
        )
    if name == "object: bare":
        return s.schema_object()
    if name == "object: properties only":
        return s.schema_object(properties={
            "a": s.schema_type("string"), "b": s.schema_type("integer"),
        })
    if name == "object: properties and required":
        return s.schema_object(
            properties={"a": s.schema_type("string")}, required=["a"],
        )
    if name == "object: closed":
        return s.schema_object(
            properties={"a": s.schema_type("string")}, required=["a"],
            additional_properties=False,
        )
    if name == "object: open by declaration":
        return s.schema_object(additional_properties=True)
    if name == "object: a dynamic-key map":
        return s.schema_object(additional_properties=s.schema_type("number"))
    if name == "object: empty required":
        return s.schema_object(required=[])
    if name == "enum: strings":
        return s.schema_enum("pass", "fail", "warn")
    if name == "enum: mixed json values":
        return s.schema_enum("a", 1, None, True)
    if name == "const: a scalar":
        return s.schema_const("fixed")
    if name == "const: a composite":
        return s.schema_const({"a": [1, 2]})
    raise AssertionError(f"no builder mapping for fixture {name!r}")


class TestBuilders:
    def test_the_fixture_count_matches_its_header(self):
        assert _BUILDERS["construct_count"] == len(_BUILDERS["constructs"])

    @pytest.mark.parametrize(
        "entry", _BUILDERS["constructs"], ids=lambda e: e["name"]
    )
    def test_construct_maps_onto_the_literal(self, entry):
        built = _build(entry["name"])
        assert built == entry["literal"]
        # One-to-one onto the closed subset: nothing a builder emits is
        # outside the vocabulary, and the result is a legal declaration.
        assert strictcli._validate_payload_schema(built) is None

    def test_builders_add_no_vocabulary(self):
        for entry in _BUILDERS["constructs"]:
            for key in _build(entry["name"]):
                assert key in strictcli._PAYLOAD_SCHEMA_KEYWORDS

    def test_a_builder_does_not_validate_on_its_own(self):
        # A builder is a constructor, not a check: an illegal type name is a
        # legal literal to build and a registration-time hard error to declare.
        built = strictcli.schema_type("strng")
        assert built == {"type": "strng"}
        found = strictcli._validate_payload_schema(built)
        assert found is not None
        assert found[1].startswith('unknown type "strng"')

    def test_built_schemas_are_declarable(self):
        app = strictcli.App(name="t", help="t", version="1")

        @app.command(
            "run", effect="read_only", help="run",
            payload_schema=strictcli.schema_object(
                properties={"a": strictcli.schema_type("integer")},
                required=["a"], additional_properties=False,
            ),
        )
        def _run(ctx):
            ctx.payload({"a": 1})
            return 0

        r = app.test(["run", "--json"])
        assert r.exit_code == 0
        assert json.loads(r.stdout)["payload"] == {"a": 1}


# ---------------------------------------------------------------------------
# Dev-only third-party cross-check (contract §19.5)
#
# python-jsonschema is a DEV dependency and never a runtime one: it exists to
# assert that the in-house validator's verdicts agree with an independent
# implementation on every shared vector. A disagreement is a test failure to
# investigate, never something the code resolves for itself.
#
# Two families are excluded by construction, because they are ours and not
# JSON Schema's: decision 16's magnitude guard, and JSON representability
# (which a JSON vector file cannot express in the first place).
# ---------------------------------------------------------------------------

import jsonschema  # noqa: E402  (dev-only, imported beside its own tests)

_OURS_ONLY_DETAILS = {
    "the number's magnitude exceeds 2^53 "
    "(declare a big identifier as a string)",
    "the value is not representable in JSON",
}


def _cross_checkable(vectors: list[dict]) -> list[dict]:
    return [
        v for v in _applicable(vectors)
        if v["valid"] or v["detail"] not in _OURS_ONLY_DETAILS
    ]


class TestThirdPartyCrossCheck:
    @pytest.mark.parametrize(
        "vec", _cross_checkable(INSTANCE_VECTORS), ids=lambda v: v["name"]
    )
    def test_verdicts_agree(self, vec):
        theirs = jsonschema.Draft202012Validator(vec["schema"]).is_valid(
            vec["value"]
        )
        ours = strictcli._validate_payload_value(
            vec["value"], vec["schema"]
        ) is None
        assert ours == theirs, (
            f"in-house says {ours}, python-jsonschema says {theirs}"
        )

    @pytest.mark.parametrize(
        "vec",
        [v for v in _applicable(SCHEMA_VECTORS) if v["valid"]],
        ids=lambda v: v["name"],
    )
    def test_accepted_schemas_are_valid_json_schema(self, vec):
        # Anything the subset admits must be a legal JSON Schema document: the
        # subset is a restriction of JSON Schema, never a dialect of its own.
        jsonschema.Draft202012Validator.check_schema(vec["schema"])

    def test_the_cross_check_covers_most_of_the_matrix(self):
        assert len(_cross_checkable(INSTANCE_VECTORS)) >= 130
