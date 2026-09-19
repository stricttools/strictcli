#!/usr/bin/env python3
"""API surface check for strictcli conformance.

Introspects Python classes, parses Go API surface via the describe_go AST
dumper, runs the TypeScript describe self-dump, reads the conformance schema,
and verifies that every real API field exists in all places (with known
exclusions for runtime-only or non-serializable features).

The Go side uses the describe_go program (conformance/describe_go/) which
parses Go source with go/ast and dumps the full API surface as JSON. This
replaces the fragile regex extraction that previously scanned Go source text.

The TypeScript side uses typescript/src/describe.ts (a hand-maintained
registry whose accuracy is enforced by typescript/tests/describe.test.ts in
both directions), run via `node dist/describe.js` after rebuilding dist so
the dump always reflects the current working tree.

Each entity (Flag, Arg, App, etc.) is described by an EntityDescriptor that
bundles the schema def name, per-language source, name maps, and exclusions.
Adding a new target is a data-entry task: provide a new descriptor with the
target's fields, name mappings, and exclusions.

Exit 0 if all checks pass, exit 1 with a diff report otherwise.
"""

from __future__ import annotations

import dataclasses
import inspect
import json
import re
import subprocess
import sys
from pathlib import Path

# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------

CONFORMANCE_DIR = Path(__file__).resolve().parent
PROJECT_ROOT = CONFORMANCE_DIR.parent
SCHEMA_PATH = CONFORMANCE_DIR / "schema.json"
DESCRIBE_GO_DIR = CONFORMANCE_DIR / "describe_go"
TYPESCRIPT_DIR = PROJECT_ROOT / "typescript"

# ---------------------------------------------------------------------------
# Target sources: per-language field extraction
# ---------------------------------------------------------------------------


def _get_go_api() -> dict:
    """Run the describe_go AST dumper and return its JSON output.

    The dumper re-parses Go source on every invocation, so the check
    always reflects the current working-tree state.
    """
    proc = subprocess.run(
        ["go", "run", "."],
        cwd=DESCRIBE_GO_DIR,
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        print(f"describe_go failed (exit {proc.returncode}):", file=sys.stderr)
        print(proc.stderr, file=sys.stderr)
        sys.exit(2)
    return json.loads(proc.stdout)


def get_go_fields_from_api(api: dict) -> dict[str, set[str]]:
    """Extract {struct_name: {exported_field_names}} from describe_go JSON.

    Also includes '_option_funcs' key with the set of option constructor names.
    """
    result: dict[str, set[str]] = {}
    for s in api["structs"]:
        exported = {f["name"] for f in s["fields"] if f["exported"]}
        if exported:
            result[s["name"]] = exported
    result["_option_funcs"] = {f["name"] for f in api["option_constructors"]}
    return result


def get_go_all_fields_from_api(api: dict) -> dict[str, dict[str, bool]]:
    """Extract {struct_name: {field_name: exported}} from describe_go JSON.

    Includes both exported and unexported fields for schema-to-Go validation,
    and the UNEXPORTED struct carriers alongside the public ones: the
    constraint system declares its four kinds through constructors over one
    unexported `constraintDecl` (contract §26.6), so the schema -> Go arm has
    no exported struct to read. `structs` stays the public surface -- only
    this map, which no surface list is built from, sees the internal ones.
    """
    result: dict[str, dict[str, bool]] = {}
    for s in api["structs"] + api["internal_structs"]:
        result[s["name"]] = {f["name"]: f["exported"] for f in s["fields"]}
    return result


def get_python_fields() -> dict[str, set[str]]:
    """Return {class_name: {field_names}} for Python dataclasses."""
    sys.path.insert(0, str(PROJECT_ROOT / "python"))
    import strictcli

    # Command is internal but part of the conformance surface. _ChoiceDecl is
    # the same shape one construct over: a consumer writes a @choice-decorated
    # class, and this is what the decorator attaches to it (contract §24.12).
    from strictcli import Command, _ChoiceDecl

    result: dict[str, set[str]] = {}
    for cls in [
        strictcli.Flag, strictcli.Arg, strictcli.FlagSet,
        strictcli.AtLeastOne, strictcli.AllOrNone, strictcli.Member,
        strictcli.Requires, strictcli.Implies,
        strictcli.App, strictcli.Group,
        Command, _ChoiceDecl,
    ]:
        fields = {f.name for f in dataclasses.fields(cls)}
        result[cls.__name__] = fields

    # Also capture decorator factory signatures
    for func_name in ("flag", "arg"):
        func = getattr(strictcli, func_name)
        sig = inspect.signature(func)
        result[f"{func_name}()"] = set(sig.parameters.keys())

    return result


def get_schema_fields() -> dict[str, set[str]]:
    """Return {def_name: {field_names}} from the conformance schema."""
    schema = json.loads(SCHEMA_PATH.read_text())
    defs = schema.get("$defs", {})
    result: dict[str, set[str]] = {}
    for def_name, def_body in defs.items():
        props = def_body.get("properties", {})
        if props:
            result[def_name] = set(props.keys())
    return result


def _get_ts_api() -> dict:
    """Rebuild typescript/dist and run the describe.ts self-dump via node.

    Like describe_go, the dump must reflect the current working-tree state,
    so dist is rebuilt first (same discipline as run.py's _ensure_ts_harness).
    """
    build = subprocess.run(
        ["npm", "run", "build"],
        cwd=TYPESCRIPT_DIR,
        capture_output=True,
        text=True,
        timeout=300,
    )
    if build.returncode != 0:
        print(f"typescript build failed (exit {build.returncode}):", file=sys.stderr)
        print(build.stdout, file=sys.stderr)
        print(build.stderr, file=sys.stderr)
        sys.exit(2)
    proc = subprocess.run(
        ["node", "dist/describe.js"],
        cwd=TYPESCRIPT_DIR,
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        print(f"describe.js failed (exit {proc.returncode}):", file=sys.stderr)
        print(proc.stderr, file=sys.stderr)
        sys.exit(2)
    return json.loads(proc.stdout)


def get_ts_struct_fields_from_api(api: dict) -> dict[str, set[str]]:
    """Extract {struct_name: {member_names}} from the TS describe JSON."""
    return {s["name"]: set(s["members"]) for s in api["structs"]}


def get_ts_ctor_keys_from_api(api: dict) -> dict[str, set[str]]:
    """Extract {option_constructor_name: {option_keys}} from the TS describe JSON."""
    return {c["name"]: set(c["option_keys"]) for c in api["option_constructors"]}


def get_ts_function_names(api: dict) -> set[str]:
    """Callable public TS names: positional-arg functions + option constructors."""
    return set(api["functions"]) | {c["name"] for c in api["option_constructors"]}


def get_ts_type_names(api: dict) -> set[str]:
    """Public TS type names: type-only exports, classes, and struct interfaces."""
    return set(api["types"]) | set(api["classes"]) | {s["name"] for s in api["structs"]}


def get_ts_index_exports() -> set[str]:
    """Parse typescript/src/index.ts and return every exported name.

    Covers `export {...}`, `export type {...}`, and `export const NAME`
    forms (the only forms index.ts uses).
    """
    src = (TYPESCRIPT_DIR / "src" / "index.ts").read_text()
    names: set[str] = set()
    for m in re.finditer(r"export\s+(?:type\s+)?\{([^}]*)\}", src):
        for name in m.group(1).split(","):
            name = name.strip()
            if name:
                names.add(name)
    for m in re.finditer(r"export\s+const\s+(\w+)", src):
        names.add(m.group(1))
    return names


# ---------------------------------------------------------------------------
# Entity descriptors
#
# Each descriptor bundles everything needed to check one entity across all
# targets.  Adding a new target is data entry: provide field names, name
# maps, and exclusions in the descriptor.
# ---------------------------------------------------------------------------

@dataclasses.dataclass(frozen=True)
class EntityDescriptor:
    """Describes one API entity for cross-target surface checking."""

    # Schema def name (e.g., "flag"), Python class name, Go struct name
    schema_def: str
    python_cls: str
    go_struct: str

    # Python field -> schema field(s).  Qualified key "ClassName.field" for
    # per-entity overrides; plain "field" for global mappings applied to
    # this entity.
    python_to_schema: dict[str, list[str]] = dataclasses.field(default_factory=dict)

    # Go exported field -> schema field (unqualified).
    go_to_schema: dict[str, str] = dataclasses.field(default_factory=dict)

    # Schema field -> Python field.  Qualified key "schema_def.field" or
    # plain "field".
    schema_to_python: dict[str, str] = dataclasses.field(default_factory=dict)

    # Schema field -> Go field (exported or unexported).  Qualified key
    # "schema_def.field" or plain "field".
    schema_to_go: dict[str, str] = dataclasses.field(default_factory=dict)

    # Implementation fields excluded from the schema (global, keyed by field
    # name with rationale string).
    impl_exclusions: dict[str, str] = dataclasses.field(default_factory=dict)

    # Per-language entity-specific field exclusions.
    python_entity_exclusions: set[str] = dataclasses.field(default_factory=set)

    # Schema fields that are test-harness-only (not real API fields).
    schema_test_only: set[str] = dataclasses.field(default_factory=set)

    # Schema per-entity exclusions (e.g., JSON discriminator "type" field).
    schema_entity_exclusions: set[str] = dataclasses.field(default_factory=set)

    # Schema fields that map to Python runtime attributes (set in
    # __post_init__, not dataclass fields).  Rationale string.
    schema_python_runtime: dict[str, str] = dataclasses.field(default_factory=dict)

    # TypeScript struct name in the describe.ts dump.  The entity's TS name
    # universe is the struct's members plus the option_keys of its owning
    # factory (see TS_STRUCT_OPTION_CTOR).
    ts_struct: str = ""

    # TS name -> schema field.  Qualified key "TsStruct.name" or plain
    # "name".  Unmapped names fall back to camelCase -> snake_case.
    ts_to_schema: dict[str, str] = dataclasses.field(default_factory=dict)

    # Schema field -> TS name.  Qualified key "schema_def.field" or plain
    # "field".  Unmapped fields fall back to snake_case -> camelCase.
    schema_to_ts: dict[str, str] = dataclasses.field(default_factory=dict)

    # TS-specific exclusions with rationale.  Keys are checked against BOTH
    # namespaces: TS member/option names (TS -> Schema arm) and schema field
    # names (Schema -> TS arm).
    ts_entity_exclusions: dict[str, str] = dataclasses.field(default_factory=dict)


# ---------------------------------------------------------------------------
# Shared exclusions (apply to multiple entities)
# ---------------------------------------------------------------------------

# Implementation fields excluded from the schema across all entities.
_GLOBAL_IMPL_EXCLUSIONS: dict[str, str] = {
    "validate": "callable, not serializable to JSON",
    "Validate": "callable, not serializable to JSON (Go struct field)",
    "ValidateFn": "callable, not serializable to JSON (Go option func)",
    "handler": "runtime-only (Python)",
    "Handler": "runtime-only (Go)",
    "PassthroughHandler": "runtime-only (Go)",
    "hasDefault": "private implementation detail (Go)",
    "hasConflictMode": "private implementation detail (Go)",
    "type": "Python Flag.type uses native types; schema uses 'type' string enum",
    "checks_embed": "runtime-only (bytes data, not serializable to JSON schema)",
    "checksEmbed": "runtime-only (Go field for WithChecksEmbed, not serializable to JSON schema)",
    # The scoped-selector construct's derived indices. Each is computed from
    # the declaration at registration and published nowhere: the dump carries
    # the declaration itself (contract §25.6), not the tables a parser builds
    # from it.
    "choice_records": "the record list §24.2 pairs with `choices`; the case "
    "schema spells both halves in one `choices_<T>` key and the dump splits "
    "them into `value_schema`'s enum and the `choices` sibling (§25.5)",
    "members": "derived index: the flags a command's scopes declare, flattened for lookup",
    "selectors": "derived index: the command's selector declarations, which the flag list already carries",
    "shorts": "derived index: the short-claim table §12.13's two short guards are built from",
    "sites": "derived index: every declaration site of a scoped name, for the collision guards",
    "allDecls": "derived index (TS): every declaration of a command in one list, for the scope walk",
}

# Schema fields that exist only for the test harness (not real API fields).
# Shared across all entities.
_GLOBAL_SCHEMA_TEST_ONLY: set[str] = {
    "handler_prints",
    "handler_exit_code",
    "passthrough_handler_prints",
    "deprecated",
    "deprecated_message",
    "checks",
    "checks_toml",
    "providers",
    "config_content",
    "config_content_late",
    "config_fields_def",
    "handler_returns",
    "handler_aborts",
    # The effects-regime handler vocabulary (effects contract §14.4): the
    # effect calls a *generated* handler issues. Like the handler_prints
    # family it describes the harness's synthetic handler body, not a field
    # any implementation's Command carries.
    "handler_effects",
    "handler_diagnostics",
    # Claimed rendering (effects contract §19.7), same class: the claim and
    # render calls a *generated* handler issues, not a field any
    # implementation's Command carries.
    "handler_claims_log",
    "handler_renders_log",
    "handler_payloads_recorded",
    "default_relative_to_root",
    "pre_test",
    "coverage_manifest",
    # The non-CLI channel drivers (effects contract §8.5, §14): pre_call runs
    # app.call() before the argv run and dump_tools prints the exported
    # descriptors' classification. Both describe what the harness does with an
    # app, not a field any implementation's App carries.
    "pre_call",
    "dump_tools",
    # The generated Python handler's per-parameter defaults: the only way a
    # case can spell the re-sentinelization the handler-parameter check refuses
    # (contract §12.12, §23.3). It describes the harness's synthetic handler
    # signature, not a field any implementation's Command carries.
    "handler_param_defaults",
    # Installs the framework's test-only confirm seam (SetConfirmIO /
    # _set_confirm_io / setConfirmIO) so the confirm protocol treats the case's
    # piped stdin as interactive. It describes what the harness does to an app,
    # not a field any implementation's App carries.
    "confirm_stdin_interactive",
}

# Shared name mappings (applied to any entity that uses them).
_SHARED_PYTHON_TO_SCHEMA: dict[str, list[str]] = {
    "choices": ["choices_str", "choices_int", "choices_float"],
    "retired_choices": [
        "retired_choices_str", "retired_choices_int", "retired_choices_float",
    ],
    "env_prefix": ["env_prefix"],
    "variadic": ["variadic"],
    "negatable": ["negatable"],
}

_SHARED_GO_TO_SCHEMA: dict[str, str] = {
    "IsVariadic": "variadic",
    "Negatable": "negatable",
    "EnvPrefix": "env_prefix",
    "EnvSeparator": "env_separator",
    "Choices": "choices_str",
    "retiredChoices": "retired_choices_str",
    "Type": "type",
    "ConflictMode": "conflict_mode",
    # ConnectionEnv maps by default snake conversion; ConnectionURL needs an
    # explicit entry (the initialism would snake to "connection_u_r_l").
    "ConnectionURL": "connection_url",
}

_SHARED_SCHEMA_TO_PYTHON: dict[str, str] = {
    "choices_str": "choices",
    "choices_int": "choices",
    "choices_float": "choices",
    "retired_choices_str": "retired_choices",
    "retired_choices_int": "retired_choices",
    "retired_choices_float": "retired_choices",
    "env_prefix": "env_prefix",
    "variadic": "variadic",
    "negatable": "negatable",
}

_SHARED_SCHEMA_TO_GO: dict[str, str] = {
    "choices_str": "Choices",
    "choices_int": "Choices",
    "choices_float": "Choices",
    "retired_choices_str": "retiredChoices",
    "retired_choices_int": "retiredChoices",
    "retired_choices_float": "retiredChoices",
    "variadic": "IsVariadic",
    "negatable": "Negatable",
    "env_prefix": "EnvPrefix",
    "env_separator": "EnvSeparator",
    "type": "Type",
    "depends_on": "DependsOn",
    # connection_env -> ConnectionEnv by default PascalCase; connection_url needs
    # the initialism-preserving ConnectionURL.
    "connection_url": "ConnectionURL",
    # The presence declaration (contract §23.2) is stored on an UNEXPORTED Go
    # field on purpose: presence is declared through the three sibling
    # FlagOptions (Required/Optional/Default), and a Flag struct literal that
    # never passes through them declares no presence and does not register.
    # describe_go dumps unexported fields too, so the mapping is the lowercase
    # name rather than an exclusion.
    "presence": "presence",
}

# TS struct -> owning factories whose option_keys extend the entity's TS name
# universe (TS spec/option object types live under the factory in describe.ts,
# not as separate structs).  The value is a LIST because a carrier can be built
# by more than one factory: CommandDef comes from the effect-classified twins
# (contract §1.2), whose specs are the only place command spec-only keys live.
# Every name here is verified against the live describe dump by
# check_ts_registry_targets() -- a factory rename must never silently empty a
# universe (that is how `defineCommand` outlived its own deletion).
TS_STRUCT_OPTION_CTOR: dict[str, list[str]] = {
    "FlagDef": ["flag"],
    "ArgDef": ["arg"],
    "CommandDef": ["defineReadOnlyCommand", "defineMutatingCommand"],
    "App": ["createApp"],
}

_SHARED_TS_TO_SCHEMA: dict[str, str] = {
    "choices": "choices_str",
    "retiredChoices": "retired_choices_str",
    "schema": "type",
}

_SHARED_SCHEMA_TO_TS: dict[str, str] = {
    "choices_str": "choices",
    "choices_int": "choices",
    "choices_float": "choices",
    "retired_choices_str": "retiredChoices",
    "retired_choices_int": "retiredChoices",
    "retired_choices_float": "retiredChoices",
    "type": "schema",
}

# Structural TS members present on every def-union carrier; excluded from the
# TS -> Schema arm with rationale (analogous to _GLOBAL_IMPL_EXCLUSIONS).
_SHARED_TS_EXCLUSIONS: dict[str, str] = {
    "kind": "TS discriminant tag on def-union carriers (FlagDef/ArgDef/FlagSet/...), no schema analog",
    "carrier": "runtime type carrier (t.str/t.int/...); the schema 'type' string corresponds to the 'schema' member",
    "opts": "raw options object retained by the factory; its keys are checked individually via option_keys",
    "_out": "phantom type-only member for handler-arg inference, never exists at runtime",
    "allFlags": "derived merged flag map (flags + flagSets), not independent API",
}


def _build_descriptors() -> list[EntityDescriptor]:
    """Build the list of entity descriptors."""
    return [
        EntityDescriptor(
            schema_def="flag",
            python_cls="Flag",
            go_struct="Flag",
            python_to_schema=_SHARED_PYTHON_TO_SCHEMA,
            go_to_schema=_SHARED_GO_TO_SCHEMA,
            schema_to_python=_SHARED_SCHEMA_TO_PYTHON,
            schema_to_go=_SHARED_SCHEMA_TO_GO,
            impl_exclusions=_GLOBAL_IMPL_EXCLUSIONS,
            python_entity_exclusions={"compound", "item_type", "value_type"},
            schema_test_only=_GLOBAL_SCHEMA_TEST_ONLY,
            # `elect_by` is what makes a case's flag entry a SELECTOR (§13's
            # item-207 box), and a selector is its own declaration entity in
            # every implementation: Python's _Selector, Go's unexported
            # memberSpelled on a TypeChoice flag, TypeScript's ChoiceFlagDef.
            # One case-schema entry serves both constructs; three declaration
            # surfaces do not, which is B9 rather than a gap.
            schema_entity_exclusions={"elect_by"},
            ts_struct="FlagDef",
            ts_to_schema=_SHARED_TS_TO_SCHEMA,
            schema_to_ts=_SHARED_SCHEMA_TO_TS,
            ts_entity_exclusions=_SHARED_TS_EXCLUSIONS,
        ),
        EntityDescriptor(
            schema_def="arg",
            python_cls="Arg",
            go_struct="Arg",
            python_to_schema=_SHARED_PYTHON_TO_SCHEMA,
            go_to_schema=_SHARED_GO_TO_SCHEMA,
            schema_to_python=_SHARED_SCHEMA_TO_PYTHON,
            schema_to_go=_SHARED_SCHEMA_TO_GO,
            impl_exclusions=_GLOBAL_IMPL_EXCLUSIONS,
            python_entity_exclusions={"compound", "item_type"},
            schema_test_only=_GLOBAL_SCHEMA_TEST_ONLY,
            ts_struct="ArgDef",
            ts_to_schema=_SHARED_TS_TO_SCHEMA,
            schema_to_ts=_SHARED_SCHEMA_TO_TS,
            ts_entity_exclusions=_SHARED_TS_EXCLUSIONS,
        ),
        EntityDescriptor(
            schema_def="flag_set",
            python_cls="FlagSet",
            go_struct="FlagSet",
            impl_exclusions=_GLOBAL_IMPL_EXCLUSIONS,
            schema_test_only=_GLOBAL_SCHEMA_TEST_ONLY,
            ts_struct="FlagSet",
            ts_entity_exclusions=_SHARED_TS_EXCLUSIONS,
        ),
        # The scoped-selector construct's choice object (contract §24.1,
        # §13's item-207 box). It replaces $defs/mutex_group, which is deleted
        # with MutexGroup itself (§24.14): the exactly-one family leaves the
        # constraint system entirely, and its declaration is a selector.
        #
        # The three declaration surfaces diverge on purpose (B9), so only the
        # SHAPE is compared here: Python's is a @choice-decorated frozen
        # dataclass whose fields are the scope (no dataclass fields of its
        # own), Go's is a *ChoiceDecl value with identity, and TypeScript's is
        # a ChoiceDef record in a keyed map. The case schema's `presence` and
        # `args` keys are input-only -- they exist so the two refused
        # declarations have a covering input (§12.13) and no implementation
        # carries a field for either.
        EntityDescriptor(
            schema_def="choice",
            python_cls="_ChoiceDecl",
            go_struct="ChoiceDecl",
            impl_exclusions=_GLOBAL_IMPL_EXCLUSIONS,
            # `presence` and `args` are input-only: they exist so the two
            # refused declarations have a covering input (§12.13), and no
            # implementation carries a field for either. `value` is the member
            # payload, which Go carries as the electing Flag itself (Flags[0])
            # and TypeScript as the choice record's `value` carrier -- neither
            # is a named field of the choice entity.
            schema_test_only=_GLOBAL_SCHEMA_TEST_ONLY | {
                "presence", "args", "value",
            },
            # Python's choice IS a decorated class: `cls` is the class the
            # scope's fields live on, and `localns` is the frame the
            # annotations resolve against (§24.12). Neither is a published
            # fact, and `flags` has no field at all because the scope is the
            # class body.
            python_entity_exclusions={"cls", "localns"},
            # `short` is the electing flag's short, and which declaration
            # holds it is the same three-surface divergence `elect_by` is:
            # Go's MemberChoice takes the member FLAG, so the short is a
            # field of that Flag rather than of ChoiceDecl, while Python's
            # @choice(short=...) and TypeScript's choice({short}) carry a
            # payload-less member's on the choice itself (and a
            # payload-carrying member's on its payload, which is `value` and
            # already schema-test-only). One case-schema key, three
            # declaration surfaces -- B9 rather than a gap.
            schema_entity_exclusions={"short"},
            schema_python_runtime={
                "choice.flags": "Python's scope is the decorated class's own "
                "dataclass fields, read off `cls` rather than held in a list",
            },
            ts_struct="ChoiceDef",
            ts_entity_exclusions={
                **_SHARED_TS_EXCLUSIONS,
                "name": "a TypeScript choice is named by its KEY in the "
                "selector's choice map, which is what makes the delivered tag "
                "an exact literal type (§24.12)",
                "kind": "the discriminant TypeScript brands every declaration "
                "descriptor with; not a declared fact",
            },
        ),
        # `members` is a GLOBAL impl exclusion -- it names the derived index a
        # command builds from its scopes -- but on a co-occurrence constraint
        # it is the declaration itself, so the two families drop the exclusion
        # and let both arms compare it.
        # The constraint system (contract §26). All four kinds share one Go
        # representation -- `constraintDecl`, unexported behind the four
        # constructors, which is why a struct literal cannot declare a
        # half-formed constraint (§26.6) -- so every descriptor below names it
        # and maps each schema key onto its own lowercase field. The Go ->
        # schema arm reads EXPORTED fields only and therefore stays silent
        # here; the schema -> Go arm reads describe_go's full field list and
        # is what checks these.
        EntityDescriptor(
            schema_def="constraint_member_record",
            python_cls="Member",
            go_struct="ConstraintMember",
            impl_exclusions=_GLOBAL_IMPL_EXCLUSIONS,
            schema_test_only=_GLOBAL_SCHEMA_TEST_ONLY,
            schema_to_go={"constraint_member_record.name": "name",
                          "constraint_member_record.when": "when"},
            # No ts_struct: TypeScript's member is a plain object literal
            # `{ name, when? }` (§26.6), which describe.ts treats the way it
            # treats the value-flag choice record it matches -- the dump's
            # `structs` list carries the def-union carriers, not every option
            # object. The interface's existence is pinned by
            # KNOWN_TS_PUBLIC_NAMES instead.
            ts_entity_exclusions=_SHARED_TS_EXCLUSIONS,
        ),
        EntityDescriptor(
            schema_def="at_least_one",
            python_cls="AtLeastOne",
            go_struct="constraintDecl",
            impl_exclusions={k: v for k, v in _GLOBAL_IMPL_EXCLUSIONS.items()
                             if k != "members"},
            schema_to_go={"at_least_one.name": "name",
                          "at_least_one.members": "members"},
            schema_test_only=_GLOBAL_SCHEMA_TEST_ONLY,
            schema_entity_exclusions={"type"},
            ts_struct="AtLeastOne",
            ts_entity_exclusions=_SHARED_TS_EXCLUSIONS,
        ),
        EntityDescriptor(
            schema_def="all_or_none",
            python_cls="AllOrNone",
            go_struct="constraintDecl",
            impl_exclusions={k: v for k, v in _GLOBAL_IMPL_EXCLUSIONS.items()
                             if k != "members"},
            schema_to_go={"all_or_none.name": "name",
                          "all_or_none.members": "members"},
            schema_test_only=_GLOBAL_SCHEMA_TEST_ONLY,
            schema_entity_exclusions={"type"},
            ts_struct="AllOrNone",
            ts_entity_exclusions=_SHARED_TS_EXCLUSIONS,
        ),
        EntityDescriptor(
            schema_def="requires",
            python_cls="Requires",
            go_struct="constraintDecl",
            impl_exclusions=_GLOBAL_IMPL_EXCLUSIONS,
            schema_to_go={"requires.name": "name", "requires.flag": "flag",
                          "requires.depends_on": "dependsOn"},
            schema_test_only=_GLOBAL_SCHEMA_TEST_ONLY,
            schema_entity_exclusions={"type"},
            ts_struct="Requires",
            ts_entity_exclusions=_SHARED_TS_EXCLUSIONS,
        ),
        EntityDescriptor(
            schema_def="implies",
            python_cls="Implies",
            go_struct="constraintDecl",
            impl_exclusions=_GLOBAL_IMPL_EXCLUSIONS,
            schema_to_go={"implies.name": "name", "implies.flag": "flag",
                          "implies.implies": "implies",
                          "implies.value": "value"},
            schema_test_only=_GLOBAL_SCHEMA_TEST_ONLY,
            schema_entity_exclusions={"type"},
            ts_struct="Implies",
            ts_entity_exclusions=_SHARED_TS_EXCLUSIONS,
        ),
        EntityDescriptor(
            schema_def="command",
            python_cls="Command",
            go_struct="Command",
            impl_exclusions=_GLOBAL_IMPL_EXCLUSIONS,
            python_entity_exclusions={"needs_context"},
            schema_test_only=_GLOBAL_SCHEMA_TEST_ONLY,
            schema_to_python={
                "command.flags": "flags",
                "command.args": "args",
            },
            schema_to_go={
                "command.flags": "flags",
                "command.args": "args",
                "command.flag_sets": "flagSets",
                "command.constraints": "constraints",
                "command.tags": "tags",
                "command.config_fields": "configFields",
                # The update declaration (contract §27.2) is stored on an
                # UNEXPORTED Go field for `presence`'s reason: WithUpdateOf is
                # the only spelling, so a Command struct literal cannot declare
                # half a record. describe_go dumps unexported fields too, which
                # is why this maps to the lowercase name rather than excluding.
                "command.update_of": "updateOf",
            },
            ts_struct="CommandDef",
            ts_to_schema=_SHARED_TS_TO_SCHEMA,
            schema_to_ts=_SHARED_SCHEMA_TO_TS,
            ts_entity_exclusions={
                **_SHARED_TS_EXCLUSIONS,
                "passthrough": "TS models passthrough commands as a separate "
                "PassthroughDef carrier (own passthrough() factory), not a "
                "bool field on CommandDef",
            },
        ),
        EntityDescriptor(
            schema_def="app",
            python_cls="App",
            go_struct="App",
            python_to_schema={
                **_SHARED_PYTHON_TO_SCHEMA,
                "App.flags": ["global_flags"],
            },
            go_to_schema=_SHARED_GO_TO_SCHEMA,
            impl_exclusions=_GLOBAL_IMPL_EXCLUSIONS,
            schema_test_only=_GLOBAL_SCHEMA_TEST_ONLY,
            schema_to_python={
                **_SHARED_SCHEMA_TO_PYTHON,
                "app.global_flags": "flags",
                "app.commands": "_commands",
                "app.groups": "_groups",
                "app.tag_contracts": "_tag_contracts",
                # One declaration, two input spellings: `config_path` holds the
                # RESOLVED path once a marker is read, and the declaration
                # itself is kept beside it.
                "app.config_path_relative_to_root": "_config_path_declared",
            },
            schema_to_go={
                **_SHARED_SCHEMA_TO_GO,
                "app.commands": "commands",
                "app.global_flags": "globalFlags",
                "app.groups": "groups",
                "app.config": "configEnabled",
                "app.config_path": "configPathOverride",
                # One declaration, two input spellings: a config path is a
                # string or a marker relative to a declared root, and Go keeps
                # the marker in its own field beside the resolved override.
                "app.config_path_relative_to_root": "configPathRef",
                # Go resolves the declared schema location eagerly into one
                # absolute field, as it does the config path override.
                "app.schema_path": "schemaOutPath",
                "app.config_format": "configFormat",
                "app.no_default_config_path": "noDefaultConfigPath",
                "app.config_conflict_mode": "configConflictMode",
                "app.checks_path": "checksPath",
                "app.infra_root": "infraRootDecls",
                "app.handshake_env": "handshakeEnvs",
                "app.connection_env": "connectionEnvs",
                "app.tag_contracts": "tagContracts",
                "app.test_coverage_dir": "testCoverageDir",
                # Effects contract §13. Go stores the allowlist unexported,
                # as it does every other App-level declaration above.
                "app.proc_observe_allowlist": "procObserveAllowlist",
            },
            python_entity_exclusions={"test_coverage"},
            schema_python_runtime={
                "app.tag_contracts": "_tag_contracts (set in __post_init__, not a dataclass field)",
                "app.config_path_relative_to_root": "_config_path_declared (set in __post_init__; config_path itself holds the RESOLVED path once the marker is read)",
            },
            ts_struct="App",
            ts_to_schema={
                **_SHARED_TS_TO_SCHEMA,
                "App.flags": "global_flags",
            },
            schema_to_ts={
                **_SHARED_SCHEMA_TO_TS,
                "app.global_flags": "flags",
                # One declaration, two input spellings: AppSpec.configPath is
                # `string | InfraRootPath`, so the marker form is the same key.
                "app.config_path_relative_to_root": "configPath",
            },
            ts_entity_exclusions={
                **_SHARED_TS_EXCLUSIONS,
                "commands": "registered via app.command(); held in internal "
                "closure state, not exposed as an App member or AppSpec key",
                "groups": "registered via app.group(); held in internal "
                "closure state, not exposed as an App member or AppSpec key",
                "tag_contracts": "registered via the app.tagContract() method "
                "(covered by the app-method parity table), not an AppSpec key",
            },
        ),
        EntityDescriptor(
            schema_def="group",
            python_cls="Group",
            go_struct="Group",
            python_to_schema=_SHARED_PYTHON_TO_SCHEMA,
            go_to_schema=_SHARED_GO_TO_SCHEMA,
            impl_exclusions=_GLOBAL_IMPL_EXCLUSIONS,
            python_entity_exclusions={"env_prefix", "deprecated"},
            schema_test_only=_GLOBAL_SCHEMA_TEST_ONLY,
            schema_to_python=_SHARED_SCHEMA_TO_PYTHON,
            schema_to_go={
                **_SHARED_SCHEMA_TO_GO,
                "group.tags": "tags",
                "group.commands": "Commands",
            },
            ts_struct="Group",
            ts_entity_exclusions={
                **_SHARED_TS_EXCLUSIONS,
                "commands": "registered via group.command(); held in internal "
                "closure state, not exposed as a Group member",
                "groups": "registered via group.group(); held in internal "
                "closure state, not exposed as a Group member",
            },
        ),
    ]


# ---------------------------------------------------------------------------
# Entity checking (unified, descriptor-driven)
# ---------------------------------------------------------------------------

def _resolve_python_to_schema(desc: EntityDescriptor, field: str) -> list[str] | None:
    """Resolve a Python field to schema field(s) using the descriptor's name map."""
    qualified = f"{desc.python_cls}.{field}"
    if qualified in desc.python_to_schema:
        return desc.python_to_schema[qualified]
    if field in desc.python_to_schema:
        return desc.python_to_schema[field]
    return None


def _resolve_go_to_schema(desc: EntityDescriptor, field: str) -> str | None:
    """Resolve a Go field to its schema name using the descriptor's name map."""
    return desc.go_to_schema.get(field)


def _resolve_schema_to_python(desc: EntityDescriptor, field: str) -> str:
    """Resolve a schema field to its Python field name."""
    qualified = f"{desc.schema_def}.{field}"
    if qualified in desc.schema_to_python:
        return desc.schema_to_python[qualified]
    if field in desc.schema_to_python:
        return desc.schema_to_python[field]
    return field


def _resolve_schema_to_go(desc: EntityDescriptor, field: str) -> str:
    """Resolve a schema field to its Go field name."""
    qualified = f"{desc.schema_def}.{field}"
    if qualified in desc.schema_to_go:
        return desc.schema_to_go[qualified]
    if field in desc.schema_to_go:
        return desc.schema_to_go[field]
    # Convert snake_case to PascalCase
    return "".join(part.capitalize() for part in field.split("_"))


def _resolve_ts_to_schema(desc: EntityDescriptor, field: str) -> str:
    """Resolve a TS name to its schema field name."""
    qualified = f"{desc.ts_struct}.{field}"
    if qualified in desc.ts_to_schema:
        return desc.ts_to_schema[qualified]
    if field in desc.ts_to_schema:
        return desc.ts_to_schema[field]
    # Convert camelCase to snake_case
    return re.sub(r"(?<!^)(?=[A-Z])", "_", field).lower()


def _resolve_schema_to_ts(desc: EntityDescriptor, field: str) -> str:
    """Resolve a schema field to its TS name."""
    qualified = f"{desc.schema_def}.{field}"
    if qualified in desc.schema_to_ts:
        return desc.schema_to_ts[qualified]
    if field in desc.schema_to_ts:
        return desc.schema_to_ts[field]
    # Convert snake_case to camelCase
    parts = field.split("_")
    return parts[0] + "".join(part.capitalize() for part in parts[1:])


def _ts_entity_universe(
    desc: EntityDescriptor,
    ts_structs: dict[str, set[str]],
    ts_ctor_keys: dict[str, set[str]],
) -> set[str]:
    """TS names belonging to an entity: struct members + owning factory keys."""
    if not desc.ts_struct:
        return set()
    universe = set(ts_structs.get(desc.ts_struct, set()))
    for ctor in TS_STRUCT_OPTION_CTOR.get(desc.ts_struct, []):
        universe |= ts_ctor_keys.get(ctor, set())
    return universe


def check_ts_registry_targets(
    descriptors: list[EntityDescriptor],
    ts_structs: dict[str, set[str]],
    ts_ctor_keys: dict[str, set[str]],
) -> list[str]:
    """Every TS name the registry points at must exist in the describe dump.

    Without this, a renamed or deleted TS struct/factory degrades silently: the
    entity's name universe shrinks (or empties) and the TS arms of check_entity
    stop checking anything, reporting a vacuous PASS.
    """
    errors: list[str] = []
    for desc in descriptors:
        if desc.ts_struct and desc.ts_struct not in ts_structs:
            errors.append(
                f"registry: ts_struct '{desc.ts_struct}' (entity "
                f"{desc.schema_def}) is not in the TS describe dump"
            )
    for struct, ctors in sorted(TS_STRUCT_OPTION_CTOR.items()):
        if struct not in ts_structs:
            errors.append(
                f"registry: TS_STRUCT_OPTION_CTOR key '{struct}' is not a "
                f"struct in the TS describe dump"
            )
        for ctor in ctors:
            if ctor not in ts_ctor_keys:
                errors.append(
                    f"registry: TS_STRUCT_OPTION_CTOR['{struct}'] names "
                    f"'{ctor}', which is not an option constructor in the TS "
                    f"describe dump"
                )
    return errors


def check_entity(
    desc: EntityDescriptor,
    py_fields: dict[str, set[str]],
    go_fields: dict[str, set[str]],
    go_all_fields: dict[str, dict[str, bool]],
    schema_fields: dict[str, set[str]],
    ts_structs: dict[str, set[str]],
    ts_ctor_keys: dict[str, set[str]],
) -> list[str]:
    """Check one entity across Python, Go, TypeScript, and schema."""
    errors: list[str] = []
    s_fields = schema_fields.get(desc.schema_def, set())
    py_set = py_fields.get(desc.python_cls, set())
    go_exported = go_fields.get(desc.go_struct, set())
    go_all = go_all_fields.get(desc.go_struct, {})
    ts_universe = _ts_entity_universe(desc, ts_structs, ts_ctor_keys)

    # --- Python -> Schema ---
    if py_set and s_fields:
        for field in sorted(py_set):
            if field.startswith("_"):
                continue
            if field in desc.impl_exclusions:
                continue
            if field in desc.python_entity_exclusions:
                continue

            mapped = _resolve_python_to_schema(desc, field)
            if mapped is not None:
                if not any(m in s_fields for m in mapped):
                    errors.append(
                        f"Python {desc.python_cls}.{field} -> schema {desc.schema_def}: "
                        f"expected one of {mapped}, found none"
                    )
            elif field not in s_fields:
                errors.append(
                    f"Python {desc.python_cls}.{field} not found in schema {desc.schema_def}"
                )

    # --- Go -> Schema ---
    if go_exported and s_fields:
        for field in sorted(go_exported):
            if field in desc.impl_exclusions:
                continue

            mapped = _resolve_go_to_schema(desc, field)
            if mapped is not None:
                if mapped not in s_fields:
                    errors.append(
                        f"Go {desc.go_struct}.{field} -> schema {desc.schema_def}: "
                        f"expected '{mapped}', not found"
                    )
            else:
                # Go PascalCase -> snake_case
                snake = re.sub(r"(?<!^)(?=[A-Z])", "_", field).lower()
                if snake not in s_fields:
                    errors.append(
                        f"Go {desc.go_struct}.{field} (as '{snake}') "
                        f"not found in schema {desc.schema_def}"
                    )

    # --- TypeScript -> Schema ---
    if ts_universe and s_fields:
        for field in sorted(ts_universe):
            if field in desc.impl_exclusions:
                continue
            if field in desc.ts_entity_exclusions:
                continue

            mapped = _resolve_ts_to_schema(desc, field)
            if mapped not in s_fields:
                errors.append(
                    f"TS {desc.ts_struct}.{field} (as '{mapped}') "
                    f"not found in schema {desc.schema_def}"
                )

    # --- Schema -> all implementations ---
    if s_fields:
        for field in sorted(s_fields):
            if field in desc.schema_test_only:
                continue
            if field in desc.schema_entity_exclusions:
                continue

            # Check Python
            py_name = _resolve_schema_to_python(desc, field)
            py_check = py_name in py_set or f"_{py_name}" in py_set
            qualified_py = f"{desc.schema_def}.{field}"
            if not py_check and qualified_py in desc.schema_python_runtime:
                py_check = True
            if not py_check:
                errors.append(
                    f"Schema {desc.schema_def}.{field} (as '{py_name}') "
                    f"not found in Python {desc.python_cls}"
                )

            # Check Go -- use describe_go's full field list (exported + unexported)
            go_name = _resolve_schema_to_go(desc, field)
            go_check = go_name in go_all
            if not go_check:
                errors.append(
                    f"Schema {desc.schema_def}.{field} (as '{go_name}') "
                    f"not found in Go {desc.go_struct}"
                )

            # Check TypeScript -- struct members + owning factory option_keys
            if ts_universe and field not in desc.ts_entity_exclusions:
                ts_name = _resolve_schema_to_ts(desc, field)
                if ts_name not in ts_universe:
                    errors.append(
                        f"Schema {desc.schema_def}.{field} (as '{ts_name}') "
                        f"not found in TS {desc.ts_struct}"
                    )

    return errors


# ---------------------------------------------------------------------------
# Option function coverage check
# ---------------------------------------------------------------------------

# Known Go option constructors (must be updated when new ones are added).
KNOWN_OPTION_FUNCS: set[str] = {
    "Short", "Default", "Env", "Prefixed", "Choices", "Repeatable",
    "ValidateFn", "NegatableOpt",
    "ArgRequired", "ArgDefault", "Variadic", "ArgType", "ArgChoices",
    # Retired choices: the spellings a declaration used to accept, each with
    # the message naming its replacement. One option per surface, beside
    # Choices / ArgChoices, whose declaration they require.
    "RetiredChoices", "ArgRetiredChoices",
    # The presence declaration (contract §23.2, §23.3): three sibling
    # FlagOptions and their ArgOption twins, where Default(v) / ArgDefault(v)
    # are the third spelling and were already catalogued above.
    "Required", "Optional", "ArgOptional",
    "WithArgs", "WithFlags", "WithFlagSets",
    # The constraint system (contract §26.6). `WithMutex` and
    # `WithDependencies` are DELETED with the constructs they carried -- the
    # exactly-one family left for the selector (§24.14) and the container is
    # named for what it holds (§26.1) -- and the three election selectors are
    # MemberOption constructors, which describe_go classifies here because
    # their result type is a func over a named type.
    "WithConstraints", "WhenPresent", "WhenTrue", "WhenNonEmpty",
    "WithPassthrough", "WithEnvPrefix", "WithConfig",
    "WithConfigPath", "WithConfigFormat",
    "WithChecks", "WithChecksEmbed",
    "WithTags",
    "WithHidden", "WithInteractive", "WithConfigFields",
    "Unique", "EnvSeparator", "ConflictMode",
    "WithNoDefaultConfigPath",
    "WithConfigConflictMode",
    "WithInfraRoot", "WithHandshakeEnv", "WithConfigPathRelativeToRoot",
    "WithSchemaPath", "WithSchemaPathRelativeToRoot",
    "WithConnectionEnv", "ConnectionURLFlag",
    "RelativeToRoot",
    # ConfigFieldOption constructors (from describe_go, not matched by old regex)
    "ConfigFieldDefault", "ConfigFieldHelp", "ConfigFieldType",
    # The declared coverage directory, plus the retired boolean it replaced.
    # WithTestCoverage is API surface only in the sense that calling it is a
    # registration-time refusal naming WithTestCoverageDir; it declares nothing.
    "WithTestCoverageDir", "WithTestCoverage",
    # Effects regime (contract §1.2, §6.1, §6.2, §10.2). The per-effect-call
    # EffectOption constructors (Resource, SkipIfCurrent, UseGrant, Cwd,
    # EffectEnv, Check, Stream, Body, Header) are not CmdOption/AppOption
    # constructors and so are not part of this catalog.
    "WithEffect", "WithConsequential", "WithGrants", "WithForwarding",
    "WithProcObserveAllowlist", "WithDryRunUnsupported",
    # The programmatic channel's consent (contract §8.5). It is a CallOption,
    # not a CmdOption/AppOption -- Go's Call takes its kwargs as a map, so
    # consent rides a variadic option where Python uses a keyword-only argument
    # and TS a trailing options object. The describe_go dumper classifies it as
    # an option constructor because its result type is a func(*T) named type.
    "WithApproveConsequential",
    # The machine payload's declared schema (contract §19.5) and the
    # stdout-ownership declaration (§19.6). The contract pins both spellings
    # WITHOUT a With- prefix (§18.9 item 111), which is why they read unlike
    # their neighbours here.
    "PayloadSchema", "OwnsStdout",
    # The update-command construct (contract §27.2, §27.8). WithUpdateOf is
    # the ONE spelling: the write mode is a positional parameter of it, which
    # is Go's spelling of mandatory, and the two name lists arrive as
    # UpdateOptions (Identity, Properties) -- constructors over a named type
    # of their own, which describe_go catalogues separately. `Nullable()`
    # (contract §27.6) is an ordinary FlagOption beside Required/Optional.
    "WithUpdateOf",
}


def check_option_funcs_coverage(go_fields: dict[str, set[str]]) -> list[str]:
    """Verify that Go option functions map to known features."""
    actual = go_fields.get("_option_funcs", set())
    unknown = actual - KNOWN_OPTION_FUNCS
    errors: list[str] = []
    for func_name in sorted(unknown):
        errors.append(
            f"Go option function '{func_name}' is not in the known list -- "
            f"add it to the surface check or to the exclusion list"
        )
    return errors


# Known TS public names (TS column of the KNOWN_OPTION_FUNCS discipline):
# every name exported from typescript/src/index.ts, values and types alike.
# Must be updated when the TS public surface changes.
KNOWN_TS_PUBLIC_NAMES: set[str] = {
    # Values: factories, functions, classes, constants
    "arg", "createApp", "deprecated",
    "errorCheckSpec", "flag", "flagSet", "implies",
    "outcome", "relativeToRoot", "requires", "warnCheckSpec",
    # The constraint system's two co-occurrence factories (contract §26.6).
    # `coRequired` is DELETED by rename: all-or-none absorbed it, with no
    # alias and no deprecation period (§26.1).
    "allOrNone", "atLeastOne",
    # The scoped-selector construct (contract §24.12): the choice factory, the
    # two selector twins, the record's provided-ness accessor and the
    # exhaustiveness helper the derived union makes sound.
    "choice", "choiceFlag", "memberChoiceFlag", "provided", "assertNever",
    "formatCheckResults", "formatCheckResultsJSON",
    "CheckRunResult", "CheckSpec", "Context", "ErrorReporter",
    "InvokeError", "WarnReporter",
    "VERSION", "t",
    # Effects regime: the twin command and passthrough factories that replace
    # `defineCommand` / `passthrough` (contract §1.2), plus the failure class.
    "defineReadOnlyCommand", "defineMutatingCommand",
    "readOnlyPassthrough", "mutatingPassthrough",
    "EffectFailed",
    # Type-only exports
    "AnyArg", "AnyCommand", "AnyFlag", "AnyFlagSet",
    # Selector type-only exports. MutexGroup / AnyMutexGroup / mutexGroup are
    # DELETED with the construct (contract §24.14): the exactly-one family
    # leaves the constraint system entirely.
    "AnyChoice", "AnyChoiceFlag", "AnyDecl", "ChoiceDef", "ChoiceFlagDef",
    "ChoiceFlagOpts", "ChoiceMap", "ChoiceOf", "ChoiceRecord", "ElectBy",
    "Elected", "ElectedOf", "ElectedRecord", "InferScopeArgs",
    "ValueChoiceDef",
    "App", "AppSpec", "ArgDef", "ArgOpts", "CallOptions", "Carrier",
    "CheckContext", "CheckOutcome", "CheckProblem", "CheckSeverity",
    "ConnectionEnvReader",
    "CheckStatus", "CommandDef",
    # The constraint system's type-only exports (contract §26.6). `CoRequired`
    # and `Dependency` are DELETED with the noun they carried: the union is
    # named `Constraint` for the container it fills, `ConstraintMembers` is the
    # `[M, M, ...M[]]` tuple that makes the two-member floor a COMPILE error,
    # and `When` is the literal union that makes a selector typo one too.
    "AllOrNone", "AtLeastOne", "Constraint", "ConstraintMember",
    "ConstraintMembers", "When",
    "ConfigFieldSpec", "ConflictMode", "DeprecatedDef",
    "DictSchema", "ElemSchema", "ElementOf", "ErrorCheckSpecInit",
    "FlagDef", "FlagMap", "FlagOpts", "FlagSet", "Group", "GroupSpec",
    "Handler", "HandlerArgs", "HandlerResult", "HandlerReturn",
    "Implies", "InferHandler", "InferHandlerArgs", "InfraAccess",
    "InfraRootPath", "ListSchema", "McpIO", "Outcome",
    "PassthroughArgs", "PassthroughDef", "PassthroughHandler",
    "Requires", "Result", "RunChecksOptions", "RunChecksResult",
    "ScalarSchema", "Schema", "Tool", "WarnCheckSpecInit", "Writer",
    # Effects regime type-only exports (contract §1.2, §2.4, §2.5.1, §6.1,
    # §10.2). `CommandSpec` is replaced by the twins' two spec types, whose
    # only difference is the Context their handler's ctx narrows to. The
    # `Unsettled` carrier is deliberately absent: neither the declared returns
    # nor the carrier-accepting parameter unions ever name it (§2.5.3).
    "ReadOnlyCommandSpec", "MutatingCommandSpec",
    "ReadOnlyContext", "MutatingContext",
    "ReadOnlyEffects", "MutatingEffects",
    "Completed", "Spawned", "Response",
    "Effect", "EffectKind", "Grant", "Forwarding",
    # The payload-schema builder sugar (contract §19.5, decision 14): pure
    # constructors of literals, one per subset keyword shape, plus the option
    # bag `schemaObject` takes. Python spells them schema_type/schema_array/
    # schema_object/schema_enum/schema_const; Go SchemaType/SchemaArray/
    # SchemaObject/SchemaEnum/SchemaConst. Only TS carries an option-bag type,
    # because only TS takes an options object -- Python uses keyword-only
    # arguments and Go positional ones.
    "schemaType", "schemaArray", "schemaObject", "schemaEnum", "schemaConst",
    "SchemaObjectOpts",
    # The update-command construct's type-only exports (contract §27.2,
    # §27.8). `UpdateOf` is the one option object a command spec carries --
    # the write mode nests inside it, so a half-declared record is
    # unrepresentable -- and `WriteMode` is the literal union
    # "sparse" | "full_replace" that makes a typo a compile error. There is no
    # exported type for the minted `--unset-<prop>`: it is derived from the
    # flag's own `nullable: true` and reaches the handler on the Context.
    "UpdateOf", "WriteMode",
}

# The payload-schema builders (contract §19.5, decision 14), one row per
# construct: Python module function, Go package function, TS module export.
# Checked in all three so a builder added to one implementation and forgotten
# in the others fails here rather than in a consumer.
PAYLOAD_SCHEMA_BUILDERS: list[tuple[str, str, str]] = [
    ("schema_type", "SchemaType", "schemaType"),
    ("schema_array", "SchemaArray", "schemaArray"),
    ("schema_object", "SchemaObject", "schemaObject"),
    ("schema_enum", "SchemaEnum", "schemaEnum"),
    ("schema_const", "SchemaConst", "schemaConst"),
]


def check_payload_schema_builders(go_api: dict, ts_api: dict) -> list[str]:
    """Every payload-schema builder exists in all three implementations."""
    errors: list[str] = []
    py_funcs = get_python_module_functions()
    go_funcs = {f["name"] for f in go_api["functions"]}
    ts_funcs = get_ts_function_names(ts_api)
    for py_name, go_name, ts_name in PAYLOAD_SCHEMA_BUILDERS:
        if py_name not in py_funcs:
            errors.append(f"Python builder '{py_name}' not found")
        if go_name not in go_funcs:
            errors.append(f"Go builder '{go_name}' not found")
        if ts_name not in ts_funcs:
            errors.append(f"TS builder '{ts_name}' not found")
    return errors


def check_ts_public_names(ts_api: dict) -> list[str]:
    """Verify TS public names (describe dump + index.ts) match the known list.

    Both sources are compared against KNOWN_TS_PUBLIC_NAMES in both
    directions, so a name added to (or dropped from) either the describe
    registry or index.ts without updating the other -- or this list -- fails.
    """
    errors: list[str] = []
    describe_names = (
        get_ts_function_names(ts_api)
        | set(ts_api["classes"])
        | {c["name"] for c in ts_api["constants"]}
        | set(ts_api["types"])
    )
    index_names = get_ts_index_exports()

    for name in sorted(describe_names - KNOWN_TS_PUBLIC_NAMES):
        errors.append(
            f"TS public name '{name}' (from describe output) is not in "
            f"KNOWN_TS_PUBLIC_NAMES -- add it to the surface check"
        )
    for name in sorted(KNOWN_TS_PUBLIC_NAMES - describe_names):
        errors.append(
            f"Known TS public name '{name}' missing from describe output"
        )
    for name in sorted(index_names - KNOWN_TS_PUBLIC_NAMES):
        errors.append(
            f"TS public name '{name}' (exported from index.ts) is not in "
            f"KNOWN_TS_PUBLIC_NAMES -- add it to the surface check"
        )
    for name in sorted(KNOWN_TS_PUBLIC_NAMES - index_names):
        errors.append(
            f"Known TS public name '{name}' not exported from index.ts"
        )
    return errors


# ---------------------------------------------------------------------------
# Cross-implementation parity for public check runner API
# ---------------------------------------------------------------------------

# Types that exist in all implementations but NOT in the conformance schema.
# Columns: Python class, Go struct, TS class/interface,
# {py_field: (go_field, ts_member)}.
CHECK_RUNNER_TYPES: list[tuple[str, str, str, dict[str, tuple[str, str]]]] = [
    ("CheckRunResult", "CheckRunResult", "CheckRunResult", {
        "name": ("Name", "name"),
        "outcome": ("Outcome", "outcome"),
    }),
    ("RunChecksOptions", "RunChecksOptions", "RunChecksOptions", {
        "tag_expr": ("TagExpr", "tagExpr"),
        "name_glob": ("NameGlob", "nameGlob"),
        "run_all": ("RunAll", "runAll"),
        "ignore_warnings": ("IgnoreWarnings", "ignoreWarnings"),
    }),
]

# Methods on App that must exist in all implementations (Python, Go, TS).
CHECK_RUNNER_APP_METHODS: list[tuple[str, str, str]] = [
    ("run_checks", "RunChecks", "runChecks"),
    ("tag_contract", "TagContract", "tagContract"),
    ("register_check_provider", "RegisterCheckProvider", "registerCheckProvider"),
    ("reset_check_provider_cache", "ResetCheckProviderCache", "resetCheckProviderCache"),
]

# Methods on App that must exist in all implementations, beyond the check
# runner's. The structured effect log's accessor is public API since the
# machine-interface round (effects contract §14.3's amendment): it is the
# envelope's source (§19.3), so it belongs in this catalog rather than in the
# test-only enclosure it used to sit in.
PUBLIC_APP_METHODS: list[tuple[str, str, str]] = [
    ("effect_log", "EffectLog", "effectLog"),
]

# Module-level (Python) / package-level (Go) / module-export (TS) functions.
CHECK_RUNNER_FUNCTIONS: list[tuple[str, str, str]] = [
    ("format_check_results", "FormatCheckResults", "formatCheckResults"),
    ("format_check_results_json", "FormatCheckResultsJSON", "formatCheckResultsJSON"),
    ("error_check_spec", "NewErrorCheckSpec", "errorCheckSpec"),
    ("warn_check_spec", "NewWarnCheckSpec", "warnCheckSpec"),
]

# Public check-outcome types that must exist in ALL implementations
# (same name in Python, Go, and TS).
CHECK_RUNNER_SHARED_TYPES: list[str] = [
    "ErrorReporter",
    "WarnReporter",
    "CheckSpec",
]

# Python-only check symbols.
PYTHON_ONLY_CHECK_SYMBOLS: list[str] = [
    "SkipCheck",
]

# Go-only typed kwargs accessors (Python and TS handlers receive
# natively-typed kwargs/args, so this bug class cannot occur there).
OUTCOME_GO_ONLY_ACCESSORS: list[str] = ["Get", "GetOpt"]


def get_python_module_functions() -> set[str]:
    """Return the set of public function names in the strictcli module."""
    sys.path.insert(0, str(PROJECT_ROOT / "python"))
    import strictcli
    return {
        name for name, obj in inspect.getmembers(strictcli, inspect.isfunction)
        if not name.startswith("_")
    }


def get_python_app_methods() -> set[str]:
    """Return the set of public method names on the App class."""
    sys.path.insert(0, str(PROJECT_ROOT / "python"))
    import strictcli
    return {
        name for name, obj in inspect.getmembers(strictcli.App, predicate=inspect.isfunction)
        if not name.startswith("_")
    }


def get_python_check_runner_types() -> dict[str, set[str]]:
    """Return {class_name: {field_names}} for Python check runner dataclasses."""
    sys.path.insert(0, str(PROJECT_ROOT / "python"))
    import strictcli
    result: dict[str, set[str]] = {}
    for cls in [strictcli.CheckRunResult]:
        fields = {f.name for f in dataclasses.fields(cls)}
        result[cls.__name__] = fields
    return result


def check_outcome_api(go_api: dict, ts_api: dict) -> list[str]:
    """Check the Outcome return-contract surface exists in all implementations."""
    errors: list[str] = []
    sys.path.insert(0, str(PROJECT_ROOT / "python"))
    import strictcli

    # Shared branded type: Outcome exists everywhere.
    if not hasattr(strictcli, "Outcome"):
        errors.append("Python type 'Outcome' not found in strictcli module")

    go_struct_names = {s["name"] for s in go_api["structs"]}
    if "Outcome" not in go_struct_names:
        errors.append("Go type 'Outcome' not found")

    if "Outcome" not in get_ts_type_names(ts_api):
        errors.append("TS type 'Outcome' not found")

    # Python factory.
    py_funcs = get_python_module_functions()
    if "outcome" not in py_funcs:
        errors.append("Python module function 'outcome' not found")

    # TS factory.
    if "outcome" not in get_ts_function_names(ts_api):
        errors.append("TS function 'outcome' not found")

    # Go constructors (package-level funcs).
    go_func_names = {f["name"] for f in go_api["functions"]}
    # ExitData is deleted (contract §19.4): the machine payload is supplied
    # through ctx.Payload, not attached to the outcome.
    for gofn in ("Exit",):
        if gofn not in go_func_names:
            errors.append(f"Go constructor '{gofn}' not found")

    # Go-only generic typed accessors.
    go_generic_names = {f["name"] for f in go_api["generic_functions"]}
    for gofn in OUTCOME_GO_ONLY_ACCESSORS:
        if gofn not in go_generic_names:
            errors.append(f"Go generic accessor '{gofn}' not found")

    return errors


def check_check_runner_types(
    go_api: dict,
    go_fields: dict[str, set[str]],
    ts_structs: dict[str, set[str]],
) -> list[str]:
    """Check that check runner types have matching fields in Python, Go, and TS."""
    errors: list[str] = []
    py_types = get_python_check_runner_types()

    for py_cls, go_struct, ts_struct, field_map in CHECK_RUNNER_TYPES:
        # Check Python fields exist
        py_set = py_types.get(py_cls, set())
        if not py_set and py_cls == "RunChecksOptions":
            py_app_methods = get_python_app_methods()
            if "run_checks" not in py_app_methods:
                errors.append(
                    f"Python App.run_checks() not found (needed for RunChecksOptions parity)"
                )
            else:
                import strictcli
                sig = inspect.signature(strictcli.App.run_checks)
                py_params = set(sig.parameters.keys()) - {"self"}
                for py_field in field_map:
                    if py_field not in py_params:
                        errors.append(
                            f"RunChecksOptions field '{py_field}' not in Python "
                            f"App.run_checks() parameters: {sorted(py_params)}"
                        )
        else:
            for py_field in field_map:
                if py_field not in py_set:
                    errors.append(
                        f"Python {py_cls}.{py_field} not found "
                        f"(expected fields: {sorted(py_set)})"
                    )

        # Check Go fields exist
        go_set = go_fields.get(go_struct, set())
        if not go_set:
            errors.append(f"Go struct {go_struct} not found in source")
        else:
            for go_field, _ in field_map.values():
                if go_field not in go_set:
                    errors.append(
                        f"Go {go_struct}.{go_field} not found "
                        f"(expected fields: {sorted(go_set)})"
                    )

        # Check TS members exist
        ts_set = ts_structs.get(ts_struct, set())
        if not ts_set:
            errors.append(f"TS struct {ts_struct} not found in describe output")
        else:
            for _, ts_member in field_map.values():
                if ts_member not in ts_set:
                    errors.append(
                        f"TS {ts_struct}.{ts_member} not found "
                        f"(expected members: {sorted(ts_set)})"
                    )

    return errors


def check_check_runner_methods(go_api: dict, ts_api: dict) -> list[str]:
    """Check that the catalogued App methods exist in all implementations."""
    errors: list[str] = []
    py_methods = get_python_app_methods()
    go_methods = {
        m["name"] for m in go_api["methods"]
        if m["receiver"] in ("*App", "App")
    }
    ts_methods = {
        m["name"] for m in ts_api["methods"]
        if m["receiver"] == "App"
    }

    for py_method, go_method, ts_method in (
        CHECK_RUNNER_APP_METHODS + PUBLIC_APP_METHODS
    ):
        if py_method not in py_methods:
            errors.append(f"Python App.{py_method}() not found")
        if go_method not in go_methods:
            errors.append(f"Go App.{go_method}() not found")
        if ts_method not in ts_methods:
            errors.append(f"TS App.{ts_method}() not found")

    return errors


def check_check_runner_functions(go_api: dict, ts_api: dict) -> list[str]:
    """Check that package/module-level check runner functions exist everywhere."""
    errors: list[str] = []
    py_funcs = get_python_module_functions()
    go_funcs = {f["name"] for f in go_api["functions"]}
    ts_funcs = get_ts_function_names(ts_api)

    for py_func, go_func, ts_func in CHECK_RUNNER_FUNCTIONS:
        if py_func not in py_funcs:
            errors.append(f"Python module function '{py_func}' not found")
        if go_func not in go_funcs:
            errors.append(f"Go package function '{go_func}' not found")
        if ts_func not in ts_funcs:
            errors.append(f"TS function '{ts_func}' not found")

    return errors


def check_check_runner_shared_types(go_api: dict, ts_api: dict) -> list[str]:
    """Check that the shared check-outcome types exist in all implementations."""
    errors: list[str] = []
    sys.path.insert(0, str(PROJECT_ROOT / "python"))
    import strictcli

    go_struct_names = {s["name"] for s in go_api["structs"]}
    ts_type_names = get_ts_type_names(ts_api)

    for name in CHECK_RUNNER_SHARED_TYPES:
        if not hasattr(strictcli, name):
            errors.append(f"Python type '{name}' not found in strictcli module")
        if name not in go_struct_names:
            errors.append(f"Go type '{name}' not found")
        if name not in ts_type_names:
            errors.append(f"TS type '{name}' not found")

    for name in PYTHON_ONLY_CHECK_SYMBOLS:
        if not hasattr(strictcli, name):
            errors.append(f"Python-only symbol '{name}' not found in strictcli module")

    return errors


# ---------------------------------------------------------------------------
# New-target stub generator
# ---------------------------------------------------------------------------

def generate_target_stub(target_name: str) -> EntityDescriptor:
    """Generate a descriptor stub for a hypothetical new target.

    Every field is empty -- the caller must fill in field names, name maps,
    and exclusions for the new target.  This demonstrates that adding a
    target is purely data entry.
    """
    return EntityDescriptor(
        schema_def="example",
        python_cls="Example",
        go_struct="Example",
    )


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> int:
    py_fields = get_python_fields()
    go_api = _get_go_api()
    go_fields = get_go_fields_from_api(go_api)
    go_all_fields = get_go_all_fields_from_api(go_api)
    schema_fields = get_schema_fields()
    ts_api = _get_ts_api()
    ts_structs = get_ts_struct_fields_from_api(ts_api)
    ts_ctor_keys = get_ts_ctor_keys_from_api(ts_api)

    descriptors = _build_descriptors()

    all_errors: list[str] = []

    # Registry integrity: the TS names the descriptors point at must exist.
    all_errors.extend(
        check_ts_registry_targets(descriptors, ts_structs, ts_ctor_keys)
    )

    # Entity checks (descriptor-driven)
    for desc in descriptors:
        all_errors.extend(
            check_entity(
                desc, py_fields, go_fields, go_all_fields, schema_fields,
                ts_structs, ts_ctor_keys,
            )
        )

    # Option function coverage
    all_errors.extend(check_option_funcs_coverage(go_fields))

    # TS public-name coverage (describe dump + index.ts vs known list)
    all_errors.extend(check_ts_public_names(ts_api))

    # Check runner parity
    all_errors.extend(check_check_runner_types(go_api, go_fields, ts_structs))
    all_errors.extend(check_check_runner_methods(go_api, ts_api))
    all_errors.extend(check_check_runner_functions(go_api, ts_api))
    all_errors.extend(check_payload_schema_builders(go_api, ts_api))
    all_errors.extend(check_check_runner_shared_types(go_api, ts_api))
    all_errors.extend(check_outcome_api(go_api, ts_api))

    if all_errors:
        print(f"API surface check FAILED ({len(all_errors)} issue(s)):\n")
        for err in all_errors:
            print(f"  - {err}")
        return 1

    print("API surface check passed.")
    # Print summary
    for desc in descriptors:
        s_count = len(schema_fields.get(desc.schema_def, set()) - desc.schema_test_only)
        py_count = len({
            f for f in py_fields.get(desc.python_cls, set())
            if not f.startswith("_") and f not in desc.impl_exclusions
        })
        go_count = len({
            f for f in go_fields.get(desc.go_struct, set())
            if f not in desc.impl_exclusions
        })
        ts_count = len({
            f for f in _ts_entity_universe(desc, ts_structs, ts_ctor_keys)
            if f not in desc.impl_exclusions and f not in desc.ts_entity_exclusions
        })
        print(
            f"  {desc.schema_def}: schema={s_count} python={py_count} "
            f"go={go_count} ts={ts_count}"
        )
    option_count = len(go_fields.get("_option_funcs", set()))
    print(f"  Go option functions: {option_count}")
    print(f"  TS public names: {len(KNOWN_TS_PUBLIC_NAMES)}")
    # Print check runner parity summary
    for py_cls, go_struct, ts_struct, field_map in CHECK_RUNNER_TYPES:
        print(
            f"  {py_cls}/{go_struct}/{ts_struct}: {len(field_map)} fields "
            f"(cross-impl parity)"
        )
    print(
        f"  App methods (check runner + public): "
        f"{len(CHECK_RUNNER_APP_METHODS) + len(PUBLIC_APP_METHODS)}"
    )
    print(f"  Package functions (check runner): {len(CHECK_RUNNER_FUNCTIONS)}")
    print(f"  Shared check types (cross-impl): {len(CHECK_RUNNER_SHARED_TYPES)}")
    print(f"  Python-only check symbols: {len(PYTHON_ONLY_CHECK_SYMBOLS)}")
    print(f"  Outcome API: Outcome type + outcome()/Exit + Go accessors {OUTCOME_GO_ONLY_ACCESSORS}")
    # New-target diagnostic
    stub = generate_target_stub("rust")
    print(f"  New-target stub: {len(dataclasses.fields(stub))} fields to fill per entity")
    return 0


if __name__ == "__main__":
    sys.exit(main())
