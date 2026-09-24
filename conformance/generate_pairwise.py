#!/usr/bin/env python3
# /// script
# requires-python = ">=3.11"
# dependencies = ["allpairspy", "jsonschema"]
# ///
"""Generate pairwise combinatorial test cases for strictcli conformance.

Uses allpairspy to produce a covering array of all 2-way combinations, then
writes JSON test cases to conformance/cases/pairwise.json.

The presence matrix is
{required, defaulted, optional} x {str, int, float, bool, list, dict} x
{plain, choices, env, validate, scoped, co-required, implies}. It is
spelled here as independent axes rather than one three-column table, so the
2-way covering array also reaches the pairs the older matrix already covered
(short x type, negatable x scoped, choices x env, repeatable x anything):

  - type: str, bool, int, float, list, dict
  - presence: required, defaulted, optional     <- the round's new dimension
  - short: yes, no
  - env: present, absent
  - choices: present, absent
  - negatable: true, false, na (bool only)
  - selector: yes, no                           <- the scoped-selector axis
  - constraint: plain, validate, all_or_none, at_least_one, implies
  - repeatable: yes, no, na (scalar str/int/float only)

**A `selector=yes` row declares the subject flag inside a CHOICE'S SCOPE**
(contract §24.1), elected by a command-line token. A scope is not a presence
declaration and never supplies one, so the whole matrix survives one level
down: type, presence, short, env, choices, repeatability, negation and
`validate` all mean at depth exactly what they mean at root, which is the
property §24.7 states per level and this axis is what proves.

**Presence drives how the value is supplied**, which is what makes the axis
mean something at parse time rather than only at registration:

  - a flag declaring `env` is supplied through the environment,
    which is what §23.5's env row promises satisfies requiredness;
  - otherwise a `required` flag is supplied on the command line;
  - otherwise nothing supplies it, so a `default` flag delivers its declared
    value and an `optional` flag delivers **absence** -- the path that had zero
    conformance coverage in any language before this round.

Invalid combinations filtered:
  - bool + repeatable=yes, bool + choices=present
  - non-bool + negatable in (true, false)  [forced to "na"]
  - list/dict + repeatable in (yes, no)    [forced to "na"]
  - list/dict + choices=present            [choices are a scalar declaration]
  - selector=yes + constraint in (all_or_none, at_least_one, implies)  [§24.8: a
    constraint naming a scoped flag is a registration error -- the scope already
    IS the constraint]
  - presence=required + constraint in (all_or_none, at_least_one): §26.5 refuses
    a member that declares required -- in at-least-one because the invocation
    always supplies it, so the constraint could never fire, and in all-or-none
    because it silently means "every other member is required too". The refusal
    itself is asserted byte for byte, both families, every target, by
    cases/constraint_registration.json; here it would make the row a
    registration error rather than the green parse this generator emits.
  - constraint=validate + type in (bool, dict): the corpus's validator compares
    the value through each language's own default formatting, which agrees for
    strings, ints, floats and their list elements, and does NOT agree for a
    bool (Python's `True` vs `true`) or for a whole dict.
"""

from __future__ import annotations

import itertools
import json
from pathlib import Path

import jsonschema
from allpairspy import AllPairs

CONFORMANCE_DIR = Path(__file__).resolve().parent
SCHEMA_PATH = CONFORMANCE_DIR / "schema.json"

# Axis values
FLAG_TYPES = ["str", "bool", "int", "float", "list", "dict"]
PRESENCE_OPTS = ["required", "defaulted", "optional"]
SHORT_OPTS = ["yes", "no"]
ENV_OPTS = ["present", "absent"]
CHOICES_OPTS = ["present", "absent"]
NEGATABLE_OPTS = ["true", "false", "na"]
SELECTOR_OPTS = ["yes", "no"]
CONSTRAINT_OPTS = ["plain", "validate", "all_or_none", "at_least_one", "implies"]

# The two co-occurrence families (contract §26.1), which share every emitter
# rule that differs from the dependency families': a member record per operand,
# a mandatory constraint name, and §26.5's refusal of a required member.
CO_OCCURRENCE = ("all_or_none", "at_least_one")
REPEATABLE_OPTS = ["yes", "no", "na"]

PARAMETERS = [
    FLAG_TYPES,       # 0: type
    PRESENCE_OPTS,    # 1: presence
    SHORT_OPTS,       # 2: short
    ENV_OPTS,         # 3: env
    CHOICES_OPTS,     # 4: choices
    NEGATABLE_OPTS,   # 5: negatable
    SELECTOR_OPTS,    # 6: selector
    CONSTRAINT_OPTS,  # 7: constraint
    REPEATABLE_OPTS,  # 8: repeatable
]

SCALARS = ("str", "int", "float")
COMPOUNDS = ("list", "dict")

# The JSON type name each type axis value declares.
JSON_TYPE = {
    "str": "str",
    "bool": "bool",
    "int": "int",
    "float": "float",
    "list": "list[str]",
    "dict": "dict[str,str]",
}

# The value a validator refuses, per element type. Never supplied and never a
# declared default, so a `validate` row stays green: what the validator does to
# a value it does refuse is pinned by the hand-written validate family.
VALIDATE_REJECTS = {"str": "REJECTED_VALUE", "int": 1234567, "float": 9.75}


def is_valid_combination(row: list) -> bool:
    """Filter out invalid combinations during generation.

    allpairspy calls this incrementally with partial rows, so each constraint
    is checked only once the indices it reads are present.
    """
    flag_type = row[0]

    if len(row) > 4:
        choices = row[4]
        if flag_type in ("bool",) + COMPOUNDS and choices == "present":
            return False
    if len(row) > 5:
        negatable = row[5]
        if flag_type == "bool" and negatable == "na":
            return False
        if flag_type != "bool" and negatable != "na":
            return False
    if len(row) > 7:
        constraint = row[7]
        # A constraint naming a scoped flag is a registration error (§24.8):
        # the scope already IS the constraint, and expressing one fact in two
        # mechanisms is how the two disagree later.
        if row[6] == "yes" and constraint in CO_OCCURRENCE + ("implies",):
            return False
        # §26.5: no member of a co-occurrence constraint may declare required.
        if row[1] == "required" and constraint in CO_OCCURRENCE:
            return False
        if constraint == "validate" and flag_type in ("bool", "dict"):
            return False
    if len(row) > 8:
        repeatable = row[8]
        if flag_type in SCALARS and repeatable == "na":
            return False
        if flag_type not in SCALARS and repeatable != "na":
            return False
        if flag_type == "bool" and repeatable == "yes":
            return False
    return True


def _short_letter(flag_type: str) -> str:
    return {"str": "s", "bool": "b", "int": "n", "float": "f",
            "list": "l", "dict": "d"}[flag_type]


def _choices(flag_type: str) -> list:
    return {
        "str": ["test_val", "other_val", "third_val"],
        "int": [99, 50, 1],
        "float": [1.5, 2.5, 3.5],
    }[flag_type]


def _values(flag_type: str, has_choices: bool, kind: str) -> dict:
    """The three values one row uses: supplied on the CLI, supplied through the
    environment, and declared as the default.

    With `choices` every one of them is drawn from the declared choices, because
    the default-in-choices check applies to declared values (§23.5's choices
    row) and a supplied value is matched at parse time. A repeatable or list
    flag carries lists, and its env form is the separator-joined text.
    """
    if kind in ("repeat", "list"):
        elem = "str" if flag_type == "list" else flag_type
        if has_choices:
            ch = _choices(elem)
            cli, dflt = [ch[0], ch[1]], [ch[2]]
        elif elem == "str":
            cli, dflt = ["alpha", "beta"], ["dflt"]
        elif elem == "int":
            cli, dflt = [1, 2], [42]
        else:
            cli, dflt = [1.5, 2.25], [0.5]
        return {"cli": cli, "env": ",".join(str(v) for v in cli), "default": dflt}
    if kind == "dict":
        return {"cli": ["k=v"], "env": '{"k": "v"}', "default": {"d": "1"}}
    if flag_type == "str":
        if has_choices:
            return {"cli": "test_val", "env": "other_val", "default": "third_val"}
        return {"cli": "test_val", "env": "env_val", "default": "default_val"}
    if flag_type == "int":
        if has_choices:
            return {"cli": 99, "env": 50, "default": 1}
        return {"cli": 99, "env": 77, "default": 42}
    if flag_type == "float":
        if has_choices:
            return {"cli": 1.5, "env": 2.5, "default": 3.5}
        return {"cli": 1.5, "env": 2.25, "default": 0.5}
    # bool
    return {"cli": True, "env": True, "default": False}


def _render_scalar(flag_type: str, value) -> str:
    """Render one value the way all three harnesses render it in a template."""
    if flag_type == "bool":
        return "true" if value else "false"
    return str(value)


def _render(kind: str, flag_type: str, value) -> str:
    """Render a resolved flag value as the harnesses' template vocabulary does."""
    if value is None:
        return "None"
    if kind in ("repeat", "list"):
        elem = "str" if flag_type == "list" else flag_type
        return ",".join(_render_scalar(elem, v) for v in value)
    if kind == "dict":
        return ",".join(f"{k}={v}" for k, v in sorted(value.items()))
    return _render_scalar(flag_type, value)


def _kind(flag_type: str, is_repeatable: bool) -> str:
    if flag_type == "list":
        return "list"
    if flag_type == "dict":
        return "dict"
    return "repeat" if is_repeatable else "scalar"


def _cli_tokens(flag_name: str, short: str | None, kind: str, flag_type: str, values: dict) -> list:
    """The argv tokens that supply this flag on the command line."""
    spec = f"-{short}" if short else f"--{flag_name}"
    if flag_type == "bool":
        return [f"--{flag_name}"]
    if kind == "repeat":
        return [tok for v in values["cli"] for tok in (spec, str(v))]
    if kind == "list":
        return [tok for v in values["cli"] for tok in (spec, str(v))]
    if kind == "dict":
        return [tok for v in values["cli"] for tok in (spec, str(v))]
    return [spec, str(values["cli"])]


def _cli_result(kind: str, flag_type: str, values: dict):
    """The value a CLI supply resolves to."""
    if flag_type == "bool":
        return True
    if kind in ("repeat", "list"):
        return list(values["cli"])
    if kind == "dict":
        return dict(pair.split("=", 1) for pair in values["cli"])
    return values["cli"]


def _env_result(kind: str, flag_type: str, values: dict):
    """The value an env supply resolves to."""
    if flag_type == "bool":
        return True
    if kind in ("repeat", "list"):
        return list(values["cli"])
    if kind == "dict":
        return json.loads(values["env"])
    return values["env"]


def _env_text(kind: str, flag_type: str, values: dict) -> str:
    if flag_type == "bool":
        return "true"
    if kind in ("repeat", "list", "dict"):
        return values["env"]
    return str(values["env"])


def generate_test_case(row_idx: int, row: list) -> dict:
    """Generate a single test case from a pairwise row."""
    flag_type = row[0]
    presence = row[1]
    has_short = row[2] == "yes"
    has_env = row[3] == "present"
    has_choices = row[4] == "present"
    is_negatable = row[5] == "true"  # only meaningful when type=bool
    is_scoped = row[6] == "yes"
    constraint = row[7]
    is_repeatable = row[8] == "yes"

    kind = _kind(flag_type, is_repeatable)
    values = _values(flag_type, has_choices, kind)

    flag_name = f"flag{row_idx}"
    env_var = f"MYAPP_{flag_name.upper()}"
    short = _short_letter(flag_type) if has_short else None

    # The subject flag is the implication TARGET when it is a bool: the
    # injected value is what satisfies its presence, whatever that presence is
    # (§23.5's Implies-target row). For every other type the implication runs
    # beside it, because an Implies trigger must be a bool flag.
    subject_is_implied = constraint == "implies" and flag_type == "bool"
    sel_name = f"sel{row_idx}"

    features = [f"type={flag_type}", f"presence={presence}"]
    if has_short:
        features.append("short")
    if has_env:
        features.append("env")
    if has_choices:
        features.append("choices")
    if is_repeatable:
        features.append("repeatable")
    if flag_type == "bool":
        features.append(f"negatable={row[5]}")
    if is_scoped:
        features.append("scoped")
    if constraint != "plain":
        features.append(constraint)
    name = f"pairwise: {', '.join(features)}"

    flag_def: dict = {
        "name": flag_name,
        "type": JSON_TYPE[flag_type],
        "help": f"test flag {row_idx}",
        "presence": "default" if presence == "defaulted" else presence,
    }
    if presence == "defaulted":
        flag_def["default"] = values["default"]

    if has_short:
        flag_def["short"] = short
    if has_env:
        flag_def["env"] = env_var
    if is_repeatable:
        flag_def["repeatable"] = True
    if kind in ("repeat", "list"):
        # Go requires an explicit `unique` on a compound flag, and a compound
        # fed from an env var needs its separator declared.
        flag_def["unique"] = False
        if has_env:
            flag_def["env_separator"] = ","
    if has_choices:
        flag_def[f"choices_{flag_type}"] = [
            {"value": v} for v in _choices(flag_type)
        ]
    if flag_type == "bool":
        flag_def["negatable"] = is_negatable
    if constraint == "validate":
        elem = "str" if flag_type == "list" else flag_type
        flag_def["validate"] = {
            "rejects": [VALIDATE_REJECTS[elem]],
            "message": f"{flag_name} refuses that value",
        }

    # --- how the value is supplied -----------------------------------------
    #
    # A scoped row supplies its value exactly as a root-scope row does: the
    # scope is elected first, and everything inside it then resolves by the
    # ordinary rules (§24.3's fourth phase). An env var bound to a scoped flag
    # is a CONDITIONAL BINDING (§24.6), consulted because its scope IS elected
    # here -- which is the property this axis pairs against every other one.
    argv = ["cmd"]
    env_dict: dict[str, str] = {}
    test_negation = False

    if is_scoped:
        argv.extend([f"--{sel_name}", "on"])

    if subject_is_implied:
        supply = "implied"
    elif has_env:
        supply = "env"
    elif presence == "required":
        supply = "cli"
    else:
        supply = "none"

    if supply == "cli":
        test_negation = flag_type == "bool" and is_negatable
        if test_negation:
            argv.append(f"--no-{flag_name}")
        else:
            argv.extend(_cli_tokens(flag_name, short, kind, flag_type, values))
    elif supply == "env":
        env_dict[env_var] = _env_text(kind, flag_type, values)

    if supply == "cli":
        resolved = False if test_negation else _cli_result(kind, flag_type, values)
    elif supply == "env":
        resolved = _env_result(kind, flag_type, values)
    elif supply == "implied":
        resolved = True
    elif presence == "defaulted":
        resolved = flag_def["default"]
    else:
        resolved = None

    expected_parts = [f"{flag_name}={_render(kind, flag_type, resolved)}"]
    handler_parts = [f"{flag_name}={{{flag_name}}}"]

    command: dict = {"name": "cmd", "help": "a command", "effect": "read_only"}
    flags: list[dict] = []
    constraints: list[dict] = []

    if is_scoped:
        # The subject flag lives in the scope of the elected choice; the sibling
        # scope declares nothing, which is the degenerate choice §24.2 says the
        # construct subsumes. The selector's own key is what the handler reads,
        # and the field is reached THROUGH it -- sub-flags are never top-level
        # handler arguments at any depth (§24.1).
        flags.append({
            "name": sel_name,
            "help": f"scope selector {row_idx}",
            "presence": "required",
            "elect_by": "selector-token",
            "choices": [
                {"name": "on", "help": "the scope that owns the subject flag",
                 "flags": [flag_def]},
                {"name": "off", "help": "the sibling scope, which owns nothing"},
            ],
        })
        handler_parts[0] = f"{flag_name}={{{sel_name}.{flag_name}}}"
    else:
        flags.append(flag_def)

    if constraint in CO_OCCURRENCE:
        partner_name = f"part{row_idx}"
        # The subject is ENGAGED exactly when the invocation provided it
        # (§26.3's `present`, §23.6's predicate): a declared default never
        # engages a member, and constraints run before defaults are applied.
        subject_engaged = supply in ("cli", "env")
        # A bool member must declare its election (§26.3): omitting `when`
        # there is the registration error that keeps `--no-x` from engaging a
        # constraint while selecting nothing. `present` is legal spelled OUT --
        # only the omission is refused -- and it keeps this row's engagement
        # rule the same one every other type takes.
        subject_member: dict = {"name": flag_name}
        if flag_type == "bool":
            subject_member["when"] = "present"
        flags.append({
            "name": partner_name,
            "help": f"co-occurrence partner {row_idx}",
            "presence": "optional",
        })
        constraints.append({
            "type": constraint,
            "name": f"pair{row_idx}",
            "members": [subject_member, {"name": partner_name}],
        })
        if constraint == "all_or_none":
            # Either every member is engaged or none is, so the partner is
            # supplied exactly when the subject is. The not-supplied half is
            # the VACUOUS satisfaction, which is the "none" of its own name.
            partner_provided = subject_engaged
        else:
            # at-least-one needs one engaged member and refuses no second one.
            # Supplying the partner only when the subject is absent exercises
            # both halves across the matrix: satisfied by the subject alone,
            # and satisfied by the partner alone.
            partner_provided = not subject_engaged
        if partner_provided:
            argv.extend([f"--{partner_name}", "together"])
            expected_parts.append(f"{partner_name}=together")
        else:
            expected_parts.append(f"{partner_name}=None")
        handler_parts.append(f"{partner_name}={{{partner_name}}}")

    if constraint == "implies":
        trigger_name = f"trig{row_idx}"
        flags.append({
            "name": trigger_name,
            "type": "bool",
            "help": f"implication trigger {row_idx}",
            "presence": "default",
            "default": False,
        })
        argv.append(f"--{trigger_name}")
        handler_parts.append(f"{trigger_name}={{{trigger_name}}}")
        expected_parts.append(f"{trigger_name}=true")
        if subject_is_implied:
            constraints.append({
                "type": "implies",
                "name": f"link{row_idx}",
                "flag": trigger_name,
                "implies": flag_name,
                "value": True,
            })
        else:
            target_name = f"imp{row_idx}"
            flags.append({
                "name": target_name,
                "type": "bool",
                "help": f"implication target {row_idx}",
                "presence": "default",
                "default": False,
            })
            constraints.append({
                "type": "implies",
                "name": f"link{row_idx}",
                "flag": trigger_name,
                "implies": target_name,
                "value": True,
            })
            handler_parts.append(f"{target_name}={{{target_name}}}")
            expected_parts.append(f"{target_name}=true")

    if flags:
        command["flags"] = flags
    if constraints:
        command["constraints"] = constraints
    command["handler_prints"] = " ".join(handler_parts)

    app: dict = {
        "name": "myapp",
        "version": "1.0.0",
        "help": "test",
        "commands": [command],
    }
    if has_env:
        app["env_prefix"] = "MYAPP"

    case: dict = {
        "name": name,
        "app": app,
        "argv": argv,
        "expect": {"exit_code": 0, "stdout_equals": " ".join(expected_parts)},
    }
    if env_dict:
        case["env"] = env_dict
    return case


def _pairs_of(row: list) -> set:
    """Every 2-way (axis, axis, value, value) pair one row covers."""
    return {
        (i, j, row[i], row[j])
        for i in range(len(row))
        for j in range(i + 1, len(row))
    }


def covering_rows() -> list[list]:
    """A 2-way covering array over the axes, completed to full coverage.

    allpairspy produces the covering array, but under this matrix's filters it
    stops short: three legal (type, presence) pairs went uncovered, and a
    presence axis that does not reach every type is the one thing this
    generator exists to prevent. So its rows are the seed, and the remainder is
    completed by greedily adding valid rows until every 2-way pair that ANY
    valid row can reach is covered. What is achievable is computed from the
    filtered combination space itself, so an impossible pair is never chased.
    """
    valid = [
        list(combo)
        for combo in itertools.product(*PARAMETERS)
        if is_valid_combination(list(combo))
    ]
    achievable: set = set()
    for row in valid:
        achievable |= _pairs_of(row)

    rows = [list(r) for r in AllPairs(PARAMETERS, filter_func=is_valid_combination)]
    covered: set = set()
    for row in rows:
        covered |= _pairs_of(row)

    while True:
        missing = achievable - covered
        if not missing:
            break
        best = max(valid, key=lambda r: len(_pairs_of(r) & missing))
        gain = len(_pairs_of(best) & missing)
        if gain == 0:
            raise RuntimeError(f"{len(missing)} achievable pairs left uncoverable")
        rows.append(best)
        covered |= _pairs_of(best)
    return rows


def main() -> None:
    rows = covering_rows()

    print(f"Generated {len(rows)} pairwise combinations")

    cases = [generate_test_case(idx, list(row)) for idx, row in enumerate(rows)]

    with open(SCHEMA_PATH) as f:
        full_schema = json.load(f)
    test_case_schema = dict(full_schema["$defs"]["test_case"])
    test_case_schema["$defs"] = full_schema["$defs"]
    for case in cases:
        try:
            jsonschema.validate(instance=case, schema=test_case_schema)
        except jsonschema.ValidationError as e:
            raise RuntimeError(
                f"Generated case {case['name']!r} fails schema validation: {e.message}"
            ) from e

    out_path = CONFORMANCE_DIR / "cases" / "pairwise.json"
    with open(out_path, "w") as f:
        json.dump(cases, f, indent=2)
        f.write("\n")

    print(f"Wrote {len(cases)} test cases to {out_path}")


if __name__ == "__main__":
    main()
