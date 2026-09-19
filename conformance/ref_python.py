"""Generate a temporary Python script from a conformance test case's app definition.

The generated script imports strictcli, builds the app as described by the JSON
definition, registers handlers that print template-substituted output, and calls
app.run().
"""

from __future__ import annotations

import json
import keyword
import textwrap
import tomllib

# The message a `handler_aborts` handler carries, identical in all three
# harnesses so the abort's surfaced stderr line is byte-identical across
# targets. Every harness prints it as "error: <message>".
HANDLER_ABORT_MESSAGE = "conformance: handler aborted"

# The message an `aborts` check impl carries. The three harnesses raise/throw/
# panic a type spelled CheckAborted with this exact message, so the framework's
# containment line -- which names the type -- is byte-identical across targets.
CHECK_ABORT_MESSAGE = "conformance: check aborted"


def _check_severities(checks_toml: str) -> dict[str, str]:
    """Parse the embedded checks_toml and return a name->severity map.

    The registration form (error vs warn) is derived from severity -- there is
    no per-check registration field in the case fixture.
    """
    data = tomllib.loads(checks_toml)
    return {name: c["severity"] for name, c in data.get("checks", {}).items()}


def _emit_check_impl_body(check_def: dict, indent: str) -> list[str]:
    """Emit the reporter-minting body lines of a check impl.

    Shared by TOML-declared checks (``@app.error_check`` / ``@app.warn_check``)
    and provider-sourced specs (``error_check_spec`` / ``warn_check_spec``
    impls). Both receive ``(ctx, reporter)``; the body mints any problems then
    returns a terminal outcome via the reporter.

    With ``aborts`` the body mints nothing and unwinds instead, which is how a
    case reaches the runner's per-check containment.
    """
    if check_def.get("aborts", False):
        return [f"{indent}raise CheckAborted({CHECK_ABORT_MESSAGE!r})"]
    mint = check_def["mint"]
    message = check_def["message"]
    problems = check_def.get("problems", [])
    notes = check_def.get("notes", [])
    body = []
    for n in notes:
        body.append(f"{indent}reporter.note({n!r})")
    for p in problems:
        pmethod = "error" if p["severity"] == "error" else "warn"
        body.append(f"{indent}reporter.{pmethod}({p['text']!r})")
    body.append(f"{indent}return reporter.{mint}({message!r})")
    return body


def _flag_param(name: str) -> str:
    """Convert a flag name to a Python parameter name (e.g. dry-run -> dry_run).

    If the result is a Python keyword (e.g. 'global', 'class'), appends '_'
    per PEP 8 convention to match strictcli's _flag_param_name().
    """
    result = name.replace("-", "_")
    if keyword.iskeyword(result):
        result += "_"
    return result


def _presence_parts(decl: dict) -> list[str]:
    """Emit the `presence=` keyword a declaration carries, if any.

    The case encoding names all three facts through one `presence` key, because
    TypeScript's discriminated union needs the discriminant beside the value.
    Python spells the third fact with `default=<value>` itself (contract §23.2),
    and `presence="default"` is a value it refuses -- so the key is dropped here
    and the `default=` emission below carries the declaration.

    A case that omits `presence` entirely is asserting the undeclared-presence
    registration error, and a case carrying a value that is neither fact is
    asserting Python's own bad-value error; both are emitted verbatim.
    """
    if "presence" not in decl:
        return []
    presence = decl["presence"]
    if presence == "default":
        return []
    return [f"presence={presence!r}"]


def _emit_default_value(value: object, ftype: str) -> str:
    """Render a declared default as a Python literal of the flag's own type.

    JSON has one number type, so an int-valued element of a `list[float]` or
    `dict[str, float]` default must be widened here -- the framework's
    element-type check is exact.
    """
    elem = ftype[5:-1] if ftype.startswith("list[") else (
        ftype[9:-1] if ftype.startswith("dict[") else ftype
    )

    def _scalar(v: object) -> object:
        if isinstance(v, bool):
            return v
        if elem == "float" and isinstance(v, (int, float)):
            return float(v)
        if elem == "int" and isinstance(v, float) and v.is_integer():
            return int(v)
        return v

    if isinstance(value, list):
        return repr([_scalar(v) for v in value])
    if isinstance(value, dict):
        return repr({k: _scalar(v) for k, v in value.items()})
    if isinstance(value, bool):
        return repr(value)
    if ftype == "float" and isinstance(value, (int, float)):
        return repr(float(value))
    return repr(value)


def _arg_type_expr(atype: str) -> str:
    """The Python type expression a declared arg carrier renders as.

    A positional arg takes the four scalars and, since §25.4 unified the two
    variadic spellings, a list carrier over the non-bool scalars -- the same
    declaration TypeScript spells `t.list(t.str)` and Go spells
    `ArgType(TypeListStr)`.
    """
    return {
        "str": "str", "bool": "bool", "int": "int", "float": "float",
        "list[str]": "list[str]", "list[int]": "list[int]",
        "list[float]": "list[float]",
    }[atype]


def _validate_part(decl: dict) -> list[str]:
    """Emit the `validate=` keyword from a case's closed validator vocabulary."""
    if "validate" not in decl:
        return []
    spec = decl["validate"]
    rejects = [str(r) for r in spec["rejects"]]
    return [f"validate=_mk_validate({rejects!r}, {spec['message']!r})"]


# ---------------------------------------------------------------------------
# Choices: the record spelling, and the selector construct
# ---------------------------------------------------------------------------

def _choices_part(decl: dict) -> list[str]:
    """Emit `choices=` from the record-shaped typed keys (contract §24.2).

    A `choices=` entry is ALWAYS a record: `Choice(<value>, help=...)`, with the
    help optional. The typed split survives on the input side alone, because
    JSON cannot tell an integer choice from a float one.
    """
    for key, cast in (
        ("choices_str", lambda v: repr(v)),
        ("choices_int", lambda v: repr(int(v))),
        ("choices_float", lambda v: repr(float(v))),
    ):
        if key not in decl:
            continue
        entries = []
        for rec in decl[key]:
            # A BARE entry is spellable so the deleted-entry refusal has a
            # covering input (§12.13's errChoicesEntryNotRecord); it is emitted
            # as the bare value the framework refuses, never wrapped.
            if not isinstance(rec, dict):
                entries.append(cast(rec))
                continue
            parts = [cast(rec["value"])]
            if "help" in rec:
                parts.append(f"help={rec['help']!r}")
            entries.append(f"strictcli.Choice({', '.join(parts)})")
        return [f"choices=[{', '.join(entries)}]"]
    return []


def _retired_choices_part(decl: dict) -> list[str]:
    """Emit `retired_choices=` from the record-shaped typed keys.

    The value-level twin of the deprecated-command construct: a spelling the
    declaration used to accept, plus the message naming its replacement. The
    typed split mirrors `choices_<T>`'s, for the same input-side reason.
    """
    for key, cast in (
        ("retired_choices_str", lambda v: repr(v)),
        ("retired_choices_int", lambda v: repr(int(v))),
        ("retired_choices_float", lambda v: repr(float(v))),
    ):
        if key not in decl:
            continue
        entries = []
        for rec in decl[key]:
            # A BARE entry is spellable so Python's bare-entry refusal has a
            # covering input; the siblings refuse it at compile time.
            if not isinstance(rec, dict):
                entries.append(cast(rec))
                continue
            entries.append(
                f"strictcli.RetiredChoice({cast(rec['value'])}, "
                f"message={rec['message']!r})"
            )
        return [f"retired_choices=[{', '.join(entries)}]"]
    return []


def _is_selector(decl: dict) -> bool:
    """`elect_by` is the input-side discriminator (§13's item-207 box)."""
    return "elect_by" in decl


def _scalar_annotation(ftype: str) -> str:
    """The annotation a scoped flag's declared carrier renders as."""
    return {
        "str": "str", "bool": "bool", "int": "int", "float": "float",
        "list[str]": "list[str]", "list[int]": "list[int]",
        "list[float]": "list[float]",
        "dict[str,str]": "dict[str, str]", "dict[str,int]": "dict[str, int]",
        "dict[str,float]": "dict[str, float]",
    }[ftype]


class _ChoiceClassEmitter:
    """Emits the `@choice`-decorated frozen dataclasses a selector needs.

    Python's declaration surface is classes (§24.12), so a case's nested choice
    objects become module-level classes emitted BEFORE the command that
    declares them. The emitter hands back the class variable names, which the
    `choice_flag(...)` decorator and the handler's annotation both reference.
    """

    def __init__(self) -> None:
        self.lines: list[str] = []
        self._n = 0
        # Keyed by the flag def's identity rather than its name: two commands
        # may each declare a selector called "via", and a nested one may reuse
        # an outer name.
        self._var_for: dict[tuple[int, str], str] = {}

    def _fresh(self, sel: str, choice: str) -> str:
        self._n += 1
        stem = f"{sel}_{choice}".replace("-", "_").replace(".", "_")
        return f"_C{self._n}_{stem}"

    def emit_selector(self, flag_def: dict, indent: str) -> list[str]:
        """Emit every class one selector's declaration needs, innermost first.

        Returns the class variable names, in declaration order.
        """
        sel = flag_def["name"]
        names: list[str] = []
        for choice in flag_def.get("choices", []):
            var = self._emit_choice(sel, choice, indent)
            self._var_for[(id(flag_def), choice["name"])] = var
            names.append(var)
        return names

    def _emit_choice(self, sel: str, choice: dict, indent: str) -> str:
        body: list[str] = []
        field_order: list[str] = []
        # The member payload is rendered FIRST, which is where §25.6 places it
        # in the dumped scope and where Go's MemberChoice puts the electing
        # flag. The three harnesses render a record's fields in one order.
        if "value" in choice:
            payload = choice["value"]
            field_order.append("value")
            # The payload-carrying member's short goes on `member_value`: that
            # field IS the electing flag's declaration in this language
            # (§24.12).
            payload_parts = [f"help={payload['help']!r}"]
            if "short" in payload:
                payload_parts.append(f"short={payload['short']!r}")
            body.append(
                f"    value: {_scalar_annotation(payload['type'])} = "
                f"strictcli.member_value({', '.join(payload_parts)})"
            )
        # A nested selector's own classes must exist before the class whose
        # field annotates them, so they are emitted first (§24.1's recursion).
        for sub in choice.get("flags", []):
            field_name = _flag_param(sub["name"])
            field_order.append(field_name)
            if _is_selector(sub):
                sub_names = self.emit_selector(sub, indent)
                union = " | ".join(sub_names) if sub_names else "object"
                parts = [f"help={sub['help']!r}", f"choices=[{', '.join(sub_names)}]"]
                parts.append(f"elect_by={sub['elect_by']!r}")
                parts.extend(_presence_parts(sub))
                if "default" in sub:
                    parts.append(f"default={self.default_expr(sub)}")
                if "short" in sub:
                    parts.append(f"short={sub['short']!r}")
                if "env" in sub:
                    parts.append(f"env={sub['env']!r}")
                body.append(
                    f"    {field_name}: {union} = "
                    f"strictcli.sub_choice_flag({', '.join(parts)})"
                )
                continue
            ftype = sub.get("type", "str")
            parts = [f"help={sub['help']!r}"]
            parts.extend(_presence_parts(sub))
            # A scoped `RelativeToRoot` default means inside a scope exactly
            # what it means at root scope (§23.5, §18.23 item 237), so the
            # scoped emitter carries the keyword the command-level emitter
            # already spells (§18.27 item 259).
            if "default_relative_to_root" in sub:
                rtr = sub["default_relative_to_root"]
                rtr_args = ", ".join(
                    [repr(rtr["env_var"])] + [repr(p) for p in rtr.get("parts", [])]
                )
                parts.append(f"default=strictcli.RelativeToRoot({rtr_args})")
            elif "default" in sub:
                parts.append(f"default={_emit_default_value(sub['default'], ftype)}")
            if "short" in sub:
                parts.append(f"short={sub['short']!r}")
            if "env" in sub:
                parts.append(f"env={sub['env']!r}")
            if "prefixed" in sub:
                parts.append(f"prefixed={sub['prefixed']!r}")
            parts.extend(_choices_part(sub))
            parts.extend(_retired_choices_part(sub))
            if sub.get("repeatable", False):
                parts.append("repeatable=True")
            if "unique" in sub:
                parts.append(f"unique={sub['unique']}")
            if "conflict_mode" in sub:
                parts.append(f"conflict_mode={sub['conflict_mode']!r}")
            if "env_separator" in sub:
                parts.append(f"env_separator={sub['env_separator']!r}")
            if "negatable" in sub and not sub["negatable"]:
                parts.append("negatable=False")
            if sub.get("nullable", False):
                parts.append("nullable=True")
            parts.extend(_validate_part(sub))
            body.append(
                f"    {field_name}: {_scalar_annotation(ftype)} = "
                f"strictcli.sub_flag({', '.join(parts)})"
            )
        # A positional declared inside a scope is refused by name (§12.13); the
        # case spells it so the refusal has a covering input.
        for pos in choice.get("args", []):
            body.append(
                f"    {pos['name']}: str = strictcli.Arg("
                f"name={pos['name']!r}, help={pos['help']!r}, presence='required')"
            )
        if not body:
            body.append("    pass")

        var = self._fresh(sel, choice["name"])
        # The choice-level short: a payload-less member's own, and the input
        # the two short refusals are asserted against.
        decorator_parts = [f"{choice['name']!r}", f"help={choice['help']!r}"]
        if "short" in choice:
            decorator_parts.append(f"short={choice['short']!r}")
        self.lines.append(
            f"{indent}@strictcli.choice({', '.join(decorator_parts)})"
        )
        self.lines.append(f"{indent}class {var}:")
        for line in body:
            self.lines.append(f"{indent}{line}")
        self.lines.append(f"{indent}_CHOICE_NAMES[{var}] = {choice['name']!r}")
        self.lines.append(f"{indent}_REC_FIELDS[{var}] = {field_order!r}")
        self.lines.append(
            f"{indent}_CHOICE_CLASS[({sel!r}, {choice['name']!r})] = {var}"
        )
        # A name-only fallback, so a case can hand ONE selector a choice class
        # its sibling declared -- the input the record door's wrong-choice
        # refusal is asserted against (§18.25 item 251).
        self.lines.append(
            f"{indent}_CHOICE_CLASS[('*', {choice['name']!r})] = {var}"
        )
        self.lines.append("")
        return var

    def default_expr(self, flag_def: dict) -> str:
        """Render a selector's defaulted selection as a choice INSTANCE.

        Python's default IS a choice instance, which is what makes an
        incomplete defaulted selection unconstructable rather than refused
        (§24.5). The case spells the same flat map §25.6 publishes, and the
        harness materializes it through the declared class.
        """
        flat = dict(flag_def["default"])
        choice_name = flat.pop("choice")
        target = None
        for choice in flag_def.get("choices", []):
            if choice["name"] == choice_name:
                target = choice
                break
        var = self._var_for.get((id(flag_def), choice_name))
        if var is None:
            # A default naming no declared choice is a registration error the
            # case is asserting; hand the raw name through so the framework
            # produces its own message instead of the harness raising first.
            return repr(choice_name)
        field_types = {}
        if target is not None:
            for sub in target.get("flags", []):
                field_types[_flag_param(sub["name"])] = sub.get("type", "str")
            if "value" in target:
                field_types["value"] = target["value"]["type"]
        kwargs = ", ".join(
            f"{_flag_param(k)}={_emit_default_value(v, field_types.get(_flag_param(k), 'str'))}"
            for k, v in flat.items()
        )
        return f"{var}({kwargs})"


def _emit_selector_decorator(
    flag_def: dict, class_vars: list[str], emitter: _ChoiceClassEmitter,
    indent: str,
) -> str:
    """Emit the `@strictcli.choice_flag(...)` decorator for one selector."""
    parts = [f"{flag_def['name']!r}", f"help={flag_def['help']!r}"]
    parts.append(f"choices=[{', '.join(class_vars)}]")
    parts.append(f"elect_by={flag_def['elect_by']!r}")
    parts.extend(_presence_parts(flag_def))
    if "default" in flag_def:
        parts.append(f"default={emitter.default_expr(flag_def)}")
    if "short" in flag_def:
        parts.append(f"short={flag_def['short']!r}")
    if "env" in flag_def:
        parts.append(f"env={flag_def['env']!r}")
    return f"{indent}@strictcli.choice_flag({', '.join(parts)})"


def _selector_annotation(flag_def: dict, class_vars: list[str]) -> str:
    """The union a selector-bound handler parameter must be annotated with.

    The check is mandatory (§24.12): it is what makes `assert_never` sound, so
    the harness writes exactly the declared union and nothing wider.
    """
    return " | ".join(class_vars) if class_vars else "object"


def _emit_flag(flag_def: dict, indent: str = "") -> str:
    """Emit a strictcli.Flag(...) expression from a flag JSON definition."""
    parts = [
        f"name={flag_def['name']!r}",
    ]
    ftype = flag_def.get("type", "str")
    scalar_type_map = {"str": "str", "bool": "bool", "int": "int", "float": "float"}
    compound_type_map = {
        "list[str]": "list[str]", "list[int]": "list[int]", "list[float]": "list[float]",
        "dict[str,str]": "dict[str, str]", "dict[str,int]": "dict[str, int]",
        "dict[str,float]": "dict[str, float]",
    }
    if ftype in compound_type_map:
        parts.append(f"type={compound_type_map[ftype]}")
    else:
        parts.append(f"type={scalar_type_map[ftype]}")
    parts.append(f"help={flag_def['help']!r}")

    if "short" in flag_def:
        parts.append(f"short={flag_def['short']!r}")

    parts.extend(_presence_parts(flag_def))

    if "default_relative_to_root" in flag_def:
        rtr = flag_def["default_relative_to_root"]
        rtr_args = ", ".join([repr(rtr["env_var"])] + [repr(p) for p in rtr.get("parts", [])])
        parts.append(f"default=strictcli.RelativeToRoot({rtr_args})")
    elif "default" in flag_def:
        parts.append(f"default={_emit_default_value(flag_def['default'], ftype)}")

    if "env" in flag_def:
        parts.append(f"env={flag_def['env']!r}")

    if "prefixed" in flag_def:
        parts.append(f"prefixed={flag_def['prefixed']!r}")

    parts.extend(_choices_part(flag_def))
    parts.extend(_retired_choices_part(flag_def))

    if flag_def.get("repeatable", False):
        parts.append("repeatable=True")

    if "unique" in flag_def:
        parts.append(f"unique={flag_def['unique']}")

    if "conflict_mode" in flag_def:
        parts.append(f"conflict_mode={flag_def['conflict_mode']!r}")

    if "env_separator" in flag_def:
        parts.append(f"env_separator={flag_def['env_separator']!r}")

    if "negatable" in flag_def and not flag_def["negatable"]:
        parts.append("negatable=False")

    # The clear vocabulary's declaration (contract §27.6): legal only on a
    # property of an update, and what mints `--unset-<prop>`.
    if flag_def.get("nullable", False):
        parts.append("nullable=True")

    parts.extend(_validate_part(flag_def))

    return f"{indent}strictcli.Flag({', '.join(parts)})"


def _emit_flag_set(fs_def: dict, indent: str = "") -> str:
    """Emit a strictcli.FlagSet(...) expression."""
    flag_lines = [_emit_flag(f, indent + "        ") for f in fs_def["flags"]]
    flags_str = ",\n".join(flag_lines)
    return (
        f"{indent}strictcli.FlagSet(\n"
        f"{indent}    name={fs_def['name']!r},\n"
        f"{indent}    flags=[\n"
        f"{flags_str},\n"
        f"{indent}    ],\n"
        f"{indent})"
    )


def _emit_constraint_member(m: object) -> str:
    """One `Member(...)` record, or a bare name passed straight through.

    A string entry is emitted as a bare Python string on purpose: it is the
    covering input for `errConstraintMemberNotRecord`, which Python and
    TypeScript can reach and Go cannot.
    """
    if isinstance(m, str):
        return repr(m)
    parts = [repr(m["name"])]
    if m.get("when") is not None:
        parts.append(f"when={m['when']!r}")
    return f"strictcli.Member({', '.join(parts)})"


def _emit_constraint(c: dict) -> str:
    """One constraint expression from the case encoding (contract §26).

    The case encoding mirrors the SCHEMA encoding of §25.7's rewritten
    catalogue -- `type`, `name`, and either `members` or the dependency
    families' own operands -- minus the member `kind`, which registration
    resolves rather than the declaration carrying.
    """
    t = c["type"]
    if t in ("at_least_one", "all_or_none"):
        ctor = "AtLeastOne" if t == "at_least_one" else "AllOrNone"
        members = ", ".join(_emit_constraint_member(m) for m in c["members"])
        return f"strictcli.{ctor}({c['name']!r}, [{members}])"
    if t == "requires":
        return (
            f"strictcli.Requires({c['name']!r}, flag={c['flag']!r}, "
            f"depends_on={c['depends_on']!r})"
        )
    if t == "implies":
        val = "True" if c["value"] else "False"
        return (
            f"strictcli.Implies({c['name']!r}, flag={c['flag']!r}, "
            f"implies={c['implies']!r}, value={val})"
        )
    raise ValueError(f"unknown constraint type {t!r}")


def _constraint_exprs(cmd_def: dict) -> list[str]:
    """The `constraints` case key, in lockstep with the Go and TS harnesses.

    The pre-§26 `dependencies` key is gone with the construct it carried; a
    corpus case still using it declares no constraint at all.
    """
    if not cmd_def.get("constraints"):
        return []
    return [_emit_constraint(c) for c in cmd_def["constraints"]]


def _collect_params(cmd_def: dict, global_flags: list[dict] | None = None) -> list[str]:
    """Collect all parameter names for a command handler."""
    params = []
    # Global flags (passed as kwargs to all handlers)
    for f in (global_flags or []):
        params.append(_flag_param(f["name"]))
    # Flags from direct flags and flag sets
    # A selector contributes ONE key -- its own. Sub-flags are never top-level
    # handler arguments at any depth (contract §24.1).
    for f in cmd_def.get("flags", []):
        params.append(_flag_param(f["name"]))
    for fs in cmd_def.get("flag_sets", []):
        for f in fs["flags"]:
            params.append(_flag_param(f["name"]))
    # Args
    for a in cmd_def.get("args", []):
        params.append(a["name"])
    return params


def _collect_all_flag_defs(cmd_def: dict, global_flags: list[dict] | None = None) -> list[dict]:
    """Collect all flag definitions (global, direct, from flag sets)."""
    flags = list(global_flags or [])
    flags.extend(cmd_def.get("flags", []))
    for fs in cmd_def.get("flag_sets", []):
        flags.extend(fs["flags"])
    return flags


def _emit_handler_body(cmd_def: dict, global_flags: list[dict] | None = None) -> str:
    """Emit the handler body that prints the template-substituted output.

    Handlers are ctx-first: ``{source:name}`` references resolve via
    ``ctx.source(name)``; ``{name}`` references resolve to the flag/arg value
    from kwargs, type-formatted.
    """
    import re
    template = cmd_def["handler_prints"]
    all_flags = _collect_all_flag_defs(cmd_def, global_flags)
    flag_types = {}
    for f in all_flags:
        flag_types[f["name"]] = f.get("type", "str")

    # Provenance references: {source:name} -> ctx.source(name), and
    # {provided:name} -> ctx.provided(name), the dedicated "was this supplied?"
    # accessor (contract §23.6).
    source_refs = sorted(set(re.findall(r"\{source:([^}]+)\}", template)))
    provided_refs = sorted(set(re.findall(r"\{provided:([^}]+)\}", template)))
    # {unset:name} -> ctx.unset(name), the clear vocabulary's own accessor
    # (contract §27.6). The minted --unset-<prop> delivers no kwarg, so this is
    # the only way a handler learns that a property was CLEARED rather than
    # left untouched: both deliver absence, and provided() is true for the
    # clear alone.
    unset_refs = sorted(set(re.findall(r"\{unset:([^}]+)\}", template)))
    # Scoped references reach INTO a delivered record (contract §24.1, §24.9):
    # `{via.subject}` is a field, `{via.choice}` is the tag, and
    # `{provided:via.subject}` is the record's own provided-ness accessor --
    # the context-level one deliberately does not see scope interiors.
    selector_params = {
        _flag_param(f["name"]) for f in all_flags if _is_selector(f)
    }
    scoped_refs = sorted({
        r for r in re.findall(r"\{([A-Za-z0-9_.-]+)\}", template)
        if "." in r and _flag_param(r.split(".")[0]) in selector_params
    })
    scoped_provided = [r for r in provided_refs if "." in r]
    provided_refs = [r for r in provided_refs if "." not in r]

    # Build a format expression
    params = _collect_params(cmd_def, global_flags)
    if (not params and not source_refs and not provided_refs
            and not unset_refs and not scoped_refs and not scoped_provided):
        return f"    print({template!r})"

    # We build the output using string concatenation to handle type formatting
    lines = []
    lines.append("    _parts = {}")
    for name in source_refs:
        lines.append(f"    _parts[{('source:' + name)!r}] = ctx.source({name!r})")
    for name in provided_refs:
        lines.append(
            f"    _parts[{('provided:' + name)!r}] = "
            f"'true' if ctx.provided({name!r}) else 'false'"
        )
    for name in unset_refs:
        lines.append(
            f"    _parts[{('unset:' + name)!r}] = "
            f"'true' if ctx.unset({name!r}) else 'false'"
        )
    for ref in scoped_provided:
        head, *rest = ref.split(".")
        lines.append(
            f"    _parts[{('provided:' + ref)!r}] = 'true' if strictcli.provided("
            f"_sel_walk({_flag_param(head)}, {rest[:-1]!r}), {rest[-1]!r}) else 'false'"
        )
    for ref in scoped_refs:
        head, *rest = ref.split(".")
        lines.append(
            f"    _parts[{ref!r}] = _sel_fmt("
            f"_sel_walk({_flag_param(head)}, {rest!r}))"
        )
    for f in all_flags:
        pname = _flag_param(f["name"])
        if _is_selector(f):
            # A selector delivers ONE tagged record under its own key, so
            # `{via}` renders the record and `{via.<field>}` reaches into it.
            lines.append(f"    _parts[{f['name']!r}] = _sel_fmt({pname})")
            continue
        ftype = f.get("type", "str")
        if ftype.startswith("list["):
            # List compound type: print comma-separated
            lines.append(
                f"    _parts[{f['name']!r}] = 'None' if {pname} is None "
                f"else ','.join(str(x) for x in {pname})"
            )
        elif ftype.startswith("dict["):
            # Dict compound type: print key=value pairs comma-separated, keys
            # sorted for deterministic (cross-target) output.
            lines.append(
                f"    _parts[{f['name']!r}] = 'None' if {pname} is None "
                f"else ','.join(f'{{k}}={{v}}' for k, v in sorted({pname}.items()))"
            )
        elif f.get("repeatable", False):
            # For repeatable, print comma-separated values. An OPTIONAL
            # compound flag that received nothing delivers absence rather than
            # an empty collection (contract §23.5), and absence renders as the
            # scalar branches render it.
            lines.append(
                f"    _parts[{f['name']!r}] = 'None' if {pname} is None "
                f"else ','.join(str(x) for x in {pname})"
            )
        elif ftype == "bool":
            lines.append(
                f"    _parts[{f['name']!r}] = 'None' if {pname} is None else ('true' if {pname} else 'false')"
            )
        else:
            lines.append(f"    _parts[{f['name']!r}] = str({pname})")

    for a in cmd_def.get("args", []):
        atype = a.get("type", "str")
        if a.get("variadic", False):
            # Variadic: value is a list, print comma-separated
            lines.append(f"    _parts[{a['name']!r}] = ','.join(str(x) for x in {a['name']})")
        elif atype == "bool":
            lines.append(
                f"    _parts[{a['name']!r}] = 'true' if {a['name']} else 'false'"
            )
        else:
            lines.append(f"    _parts[{a['name']!r}] = str({a['name']})")

    lines.append(f"    _template = {template!r}")
    lines.append("    _out = _template")
    lines.append("    for _k, _v in _parts.items():")
    lines.append("        _out = _out.replace('{' + _k + '}', _v)")
    lines.append("    print(_out)")

    return "\n".join(lines)


# ---------------------------------------------------------------------------
# The effects vocabulary (effects contract §14.4)
#
# `handler_effects` is materialized identically by all three harnesses: iterate
# the array in order, call the named method with EXACTLY the keys the entry
# declares (no per-method filtering -- a case declaring a key the method does
# not accept is asserting the error), and keep the returned carrier in a per-run
# map indexed by position so `forward_from` / `extract_from` can reference it.
# ---------------------------------------------------------------------------

# The carrier-accepting positional each method's `forward_from` supplies. It is
# the method's LAST carrier-accepting argument (§2.5.5); `run` and `spawn` have
# only `argv`, so the carrier is appended as its final element.
_FORWARD_TARGET = {
    "run": "argv",
    "spawn": "argv",
    "write": "content",
    "mkdir": "path",
    "remove": "path",
    "rename": "to",
    "chmod": "path",
    "http": "url",
}

# The option keys the vocabulary carries, in the order the harnesses pass them.
_EFFECT_OPTION_KEYS = ("stream", "resource", "skip_if_current", "grant")


def _deprecated_effect_arg(cmd_def: dict) -> str:
    """The `effect=` argument for a deprecated registration, if the case declares one.

    Deprecated commands are classification-EXEMPT (§1.1), so a case that
    declares `effect` on a deprecated entry is asserting the registration hard
    error `errDeprecatedCommandEffect`. Forwarding it is the only way to reach
    that guard.
    """
    if "effect" not in cmd_def:
        return ""
    return f", effect={cmd_def['effect']!r}"


def _emit_classification(cmd_def: dict, indent: str) -> list[str]:
    """Emit the effects-regime keywords: effect, consequential, grants, forwarding.

    `effect` is mandatory on every non-deprecated command (§1.1), so it is
    emitted whenever the case declares it and omitted when it does not -- a case
    that omits it is asserting the registration hard error.
    """
    lines: list[str] = []
    if "effect" in cmd_def:
        lines.append(f"{indent}effect={cmd_def['effect']!r},")
    # `consequential` is NOT mandatory (§8.1): absence means "not
    # consequential", so it is emitted only when the case declares it.
    if "consequential" in cmd_def:
        lines.append(f"{indent}consequential={cmd_def['consequential']!r},")
    # `dry_run_supported` is likewise NOT mandatory: absence means supported.
    # Only false is declarable, and it carries a mandatory reason.
    if "dry_run_supported" in cmd_def:
        lines.append(
            f"{indent}dry_run_supported={cmd_def['dry_run_supported']!r},"
        )
    if "dry_run_unsupported_reason" in cmd_def:
        lines.append(
            f"{indent}dry_run_unsupported_reason="
            f"{cmd_def['dry_run_unsupported_reason']!r},"
        )
    # The update-command construct (contract §27). The case schema follows the
    # DECLARATION side -- ONE record carrying its write mode (§13's amendment)
    # -- and Python's spelling is that record: a frozen keyword-only dataclass
    # whose `write_mode` has no default, so an absent key reaches the
    # constructor as the empty string and is the covering input for
    # errUpdateWriteModeInvalid, which is why the case schema does not require
    # the key.
    if "update_of" in cmd_def:
        _ud = cmd_def["update_of"]
        _upd = [repr(_ud.get("resource", ""))]
        _upd.append(f"write_mode={_ud.get('write_mode', '')!r}")
        if "identity" in _ud:
            _upd.append(f"identity={list(_ud['identity'])!r}")
        if "properties" in _ud:
            _upd.append(f"properties={list(_ud['properties'])!r}")
        lines.append(f"{indent}update_of=strictcli.UpdateOf({', '.join(_upd)}),")
    # The machine payload's declared schema (§19.5). A handler_returns of kind
    # "data"/"exit_data" supplies a payload, and ctx.payload refuses to run on a
    # command that declares no schema -- so the harness declares the permissive
    # literal for exactly those commands. The literal is identical in all three
    # harnesses, which is what keeps the schema dump in parity.
    # A case may declare its own literal with "payload_schema", which is how
    # the closed subset's enforcement becomes observable through the CLI: the
    # schema dump publishes it verbatim and a payload is validated against it.
    _hr_kind = (cmd_def.get("handler_returns") or {}).get("kind")
    if "payload_schema" in cmd_def:
        lines.append(
            f"{indent}payload_schema={cmd_def['payload_schema']!r},"
        )
    elif _hr_kind in ("data", "exit_data") or cmd_def.get(
        "handler_payloads_recorded", False
    ):
        lines.append(f"{indent}payload_schema={{}},")
    # Stdout ownership (§19.6): the command's own document keeps stdout and the
    # envelope moves to stderr in machine mode.
    if cmd_def.get("owns_stdout", False):
        lines.append(f"{indent}owns_stdout=True,")
    if cmd_def.get("grants"):
        exprs = [
            f"strictcli.Grant(name={g['name']!r}, reason={g['reason']!r}, "
            f"kind={g['kind']!r})"
            for g in cmd_def["grants"]
        ]
        lines.append(f"{indent}grants=[{', '.join(exprs)}],")
    if "forwarding" in cmd_def:
        reason = cmd_def["forwarding"]["reason"]
        lines.append(
            f"{indent}forwarding=strictcli.Forwarding(reason={reason!r}),"
        )
    return lines


def _emit_handler_effects(cmd_def: dict, indent: str) -> list[str]:
    """Emit the effect calls a generated handler issues, in order."""
    entries = cmd_def.get("handler_effects")
    if not entries:
        return []

    lines = [f"{indent}_eff = {{}}"]
    for i, e in enumerate(entries, start=1):
        method = e["method"]

        extract_from = e.get("extract_from")
        if extract_from is not None:
            # Extraction is terminal by construction: it truncates the preview,
            # so nothing after this entry runs.
            lines.append(f"{indent}bool(_eff[{extract_from}])")
            break

        forward_from = e.get("forward_from")
        fwd = f"_eff[{forward_from}]" if forward_from is not None else None
        target = _FORWARD_TARGET[method]

        pos: list[str] = []
        if method in ("run", "spawn"):
            argv = [repr(a) for a in e.get("argv", [])]
            if fwd is not None and target == "argv":
                argv.append(fwd)
            pos.append(f"[{', '.join(argv)}]")
        elif method == "write":
            pos.append(fwd if (fwd and target == "path") else repr(e["path"]))
            pos.append(fwd if (fwd and target == "content") else repr(e["content"]))
        elif method in ("mkdir", "remove"):
            pos.append(fwd if fwd is not None else repr(e["path"]))
        elif method == "rename":
            pos.append(repr(e["path"]))
            pos.append(fwd if fwd is not None else repr(e["to"]))
        elif method == "chmod":
            pos.append(fwd if fwd is not None else repr(e["path"]))
            pos.append(str(int(e["mode"], 8)))
        elif method == "http":
            pos.append(repr(e["http_method"]))
            pos.append(fwd if fwd is not None else repr(e["url"]))

        for key in _EFFECT_OPTION_KEYS:
            if key in e:
                pos.append(f"{key}={e[key]!r}")

        lines.append(f"{indent}_eff[{i}] = ctx.effects.{method}({', '.join(pos)})")
    return lines


def _emit_handler_diagnostics(cmd_def: dict, indent: str) -> list[str]:
    """Emit the Context diagnostic calls a generated handler issues, in order.

    The four levels are gated by --quiet / --verbose (effects contract §7.4);
    the harness itself does no gating, it just calls the named method.
    """
    lines: list[str] = []
    for d in cmd_def.get("handler_diagnostics", []):
        lines.append(f"{indent}ctx.{d['level']}({d['message']!r})")
    return lines


def _emit_command_registration(
    cmd_def: dict, target: str, indent: str = "",
    global_flags: list[dict] | None = None,
) -> str:
    """Emit the code to register a command on a target (app or group variable name).

    Returns multi-line code string.
    """
    lines = []
    is_passthrough = cmd_def.get("passthrough", False)
    exit_code = cmd_def.get("handler_exit_code", 0)

    # --- Passthrough command ---
    if is_passthrough:
        handler_name = cmd_def['name'].replace('-', '_') + '_passthrough_handler'
        # Define the passthrough handler function first
        # Signature: func(ctx, name: str, args: list[str], globals: dict) -> int
        lines.append(f"{indent}def {handler_name}(ctx, name, args, globals):")
        pt_aborts = cmd_def.get("handler_aborts", False)
        if pt_aborts:
            # The handler unwinds instead of printing and returning.
            lines.append(f"{indent}    raise ValueError({HANDLER_ABORT_MESSAGE!r})")
        if global_flags and not pt_aborts:
            # Print global flag values first
            for gf in global_flags:
                gf_name = gf["name"]
                ftype = gf.get("type", "str")
                if ftype == "bool":
                    # An OPTIONAL global that received nothing arrives as None,
                    # which renders as None here exactly as it does on the
                    # ordinary handler path -- truthiness would spell it
                    # "false" and hide the difference from a real false.
                    lines.append(
                        f'{indent}    _gv = globals[{gf_name!r}]'
                    )
                    lines.append(
                        f'{indent}    print({gf_name!r} + "=" + '
                        f'("None" if _gv is None else ("true" if _gv else "false")))'
                    )
                else:
                    lines.append(
                        f'{indent}    print({gf_name!r} + "=" + str(globals[{gf_name!r}]))'
                    )
        # Print using passthrough_handler_prints template, or default format
        pt_template = cmd_def.get("passthrough_handler_prints")
        if pt_aborts:
            pass
        elif pt_template:
            # Build the output by substituting {name} and {args} in the template
            lines.append(f'{indent}    _pt_out = {pt_template!r}')
            lines.append(f'{indent}    _pt_out = _pt_out.replace("{{name}}", name)')
            lines.append(f'{indent}    _pt_out = _pt_out.replace("{{args}}", ",".join(args))')
            lines.append(f'{indent}    print(_pt_out)')
        else:
            lines.append(f'{indent}    print(name + ":" + ",".join(args))')
        if not pt_aborts:
            lines.append(f"{indent}    return {exit_code}")
        lines.append("")

        # Register the command with passthrough=Passthrough(handler=...)
        lines.append(f"{indent}@{target}.command(")
        lines.append(f"{indent}    {cmd_def['name']!r},")
        lines.append(f"{indent}    help={cmd_def['help']!r},")

        # If the test case also specifies flags/args/flag_sets (registration error tests),
        # include them so the error is triggered
        if cmd_def.get("args"):
            arg_exprs = []
            for a in cmd_def["args"]:
                aparts = [f"name={a['name']!r}", f"help={a['help']!r}"]
                atype = a.get("type", "str")
                if atype != "str":
                    aparts.append(f"type={_arg_type_expr(atype)}")
                aparts.extend(_presence_parts(a))
                if "default" in a:
                    aparts.append(f"default={_emit_default_value(a['default'], atype)}")
                if a.get("variadic", False):
                    aparts.append("variadic=True")
                aparts.extend(_choices_part(a))
                aparts.extend(_retired_choices_part(a))
                arg_exprs.append(f"strictcli.Arg({', '.join(aparts)})")
            lines.append(
                f"{indent}    args=[{', '.join(arg_exprs)}],"
            )

        if cmd_def.get("flag_sets"):
            fs_exprs = [_emit_flag_set(t, indent + "        ") for t in cmd_def["flag_sets"]]
            lines.append(f"{indent}    flag_sets=[")
            for te in fs_exprs:
                lines.append(f"{te},")
            lines.append(f"{indent}    ],")

        constraint_exprs = _constraint_exprs(cmd_def)
        if constraint_exprs:
            lines.append(f"{indent}    constraints=[{', '.join(constraint_exprs)}],")

        if cmd_def.get("tags"):
            tag_set = ", ".join(repr(t) for t in cmd_def["tags"])
            lines.append(f"{indent}    tags={{{tag_set}}},")
        lines.extend(_emit_classification(cmd_def, indent + "    "))
        lines.append(f"{indent}    passthrough=strictcli.Passthrough(handler={handler_name}),")
        lines.append(f"{indent})")

        # Emit flag decorators if present (for registration error tests)
        flag_decorators = []
        for f in cmd_def.get("flags", []):
            fd_parts = [f"{f['name']!r}"]
            ftype = f.get("type", "str")
            if ftype != "str":
                fd_parts.append(f"type={ftype}")
            fd_parts.append(f"help={f['help']!r}")
            # Presence is mandatory on every flag (§23.1), including one
            # declared on a passthrough command purely to reach the
            # passthrough-cannot-have-flags error: without it the presence
            # error fires first and the case asserts the wrong line.
            fd_parts.extend(_presence_parts(f))
            if "default" in f:
                fd_parts.append(f"default={_emit_default_value(f['default'], ftype)}")
            flag_decorators.append(
                f"{indent}@strictcli.flag({', '.join(fd_parts)})"
            )
        for fd in flag_decorators:
            lines.append(fd)

        # The decorated function is a dummy (ignored for passthrough commands)
        dummy_name = cmd_def['name'].replace('-', '_') + '_cmd'
        lines.append(f"{indent}def {dummy_name}():")
        lines.append(f"{indent}    pass")
        lines.append("")
        return "\n".join(lines)

    # --- Normal command ---
    params = _collect_params(cmd_def, global_flags)

    # Build decorator kwargs
    decorator_parts = [f"{indent}@{target}.command("]
    decorator_parts.append(f"{indent}    {cmd_def['name']!r},")
    decorator_parts.append(f"{indent}    help={cmd_def['help']!r},")

    # args
    if cmd_def.get("args"):
        arg_exprs = []
        for a in cmd_def["args"]:
            aparts = [f"name={a['name']!r}", f"help={a['help']!r}"]
            atype = a.get("type", "str")
            if atype != "str":
                aparts.append(f"type={_arg_type_expr(atype)}")
            aparts.extend(_presence_parts(a))
            if "default" in a:
                aparts.append(f"default={_emit_default_value(a['default'], atype)}")
            if a.get("variadic", False):
                aparts.append("variadic=True")
            aparts.extend(_choices_part(a))
            aparts.extend(_retired_choices_part(a))
            arg_exprs.append(f"strictcli.Arg({', '.join(aparts)})")
        decorator_parts.append(
            f"{indent}    args=[{', '.join(arg_exprs)}],"
        )

    # flag sets
    if cmd_def.get("flag_sets"):
        fs_exprs = [_emit_flag_set(t, indent + "        ") for t in cmd_def["flag_sets"]]
        decorator_parts.append(f"{indent}    flag_sets=[")
        for te in fs_exprs:
            decorator_parts.append(f"{te},")
        decorator_parts.append(f"{indent}    ],")

    # constraints (contract §26)
    constraint_exprs = _constraint_exprs(cmd_def)
    if constraint_exprs:
        decorator_parts.append(
            f"{indent}    constraints=[{', '.join(constraint_exprs)}],"
        )

    # tags
    if cmd_def.get("tags"):
        tag_set = ", ".join(repr(t) for t in cmd_def["tags"])
        decorator_parts.append(f"{indent}    tags={{{tag_set}}},")

    # config_fields
    if cmd_def.get("config_fields"):
        cf_list = repr(cmd_def["config_fields"])
        decorator_parts.append(f"{indent}    config_fields={cf_list},")

    # hidden
    if cmd_def.get("hidden", False):
        decorator_parts.append(f"{indent}    hidden=True,")

    # interactive
    if cmd_def.get("interactive", False):
        decorator_parts.append(f"{indent}    interactive=True,")

    # effect / grants / forwarding (the effects regime, §1.1, §6.1, §10.2)
    decorator_parts.extend(_emit_classification(cmd_def, indent + "    "))

    decorator_parts.append(f"{indent})")

    # Flag decorators (for direct flags)
    compound_type_map = {
        "list[str]": "list[str]", "list[int]": "list[int]", "list[float]": "list[float]",
        "dict[str,str]": "dict[str, str]", "dict[str,int]": "dict[str, int]",
        "dict[str,float]": "dict[str, float]",
    }
    # Selectors are a declaration surface of their own (§24.12): the choice
    # classes are emitted BEFORE the command, and the selector itself is a
    # `choice_flag(...)` decorator rather than a `flag(...)` one.
    emitter = _ChoiceClassEmitter()
    selector_vars: dict[str, list[str]] = {}
    for f in cmd_def.get("flags", []):
        if not _is_selector(f):
            continue
        selector_vars[f["name"]] = emitter.emit_selector(f, indent)

    # Selectors and plain flags share ONE decorator list, in the case's own
    # declaration order: Python reverses the list it collects (decorators run
    # bottom-to-top), and the two constructs' relative order is what §24.10's
    # help block renders.
    flag_decorators = []
    for f in cmd_def.get("flags", []):
        if _is_selector(f):
            flag_decorators.append(_emit_selector_decorator(
                f, selector_vars[f["name"]], emitter, indent,
            ))
            continue
        fd_parts = [f"{f['name']!r}"]
        ftype = f.get("type", "str")
        if ftype in compound_type_map:
            fd_parts.append(f"type={compound_type_map[ftype]}")
        elif ftype != "str":
            fd_parts.append(f"type={ftype}")
        fd_parts.append(f"help={f['help']!r}")
        if "short" in f:
            fd_parts.append(f"short={f['short']!r}")
        fd_parts.extend(_presence_parts(f))
        if "default_relative_to_root" in f:
            rtr = f["default_relative_to_root"]
            rtr_args = ", ".join([repr(rtr["env_var"])] + [repr(p) for p in rtr.get("parts", [])])
            fd_parts.append(f"default=strictcli.RelativeToRoot({rtr_args})")
        elif "default" in f:
            fd_parts.append(f"default={_emit_default_value(f['default'], ftype)}")
        if "env" in f:
            fd_parts.append(f"env={f['env']!r}")
        if "prefixed" in f:
            fd_parts.append(f"prefixed={f['prefixed']!r}")
        fd_parts.extend(_choices_part(f))
        fd_parts.extend(_retired_choices_part(f))
        if f.get("repeatable", False):
            fd_parts.append("repeatable=True")
        if "unique" in f:
            fd_parts.append(f"unique={f['unique']}")
        if "conflict_mode" in f:
            fd_parts.append(f"conflict_mode={f['conflict_mode']!r}")
        if "env_separator" in f:
            fd_parts.append(f"env_separator={f['env_separator']!r}")
        if "negatable" in f and not f["negatable"]:
            fd_parts.append("negatable=False")
        # The clear vocabulary's declaration (contract §27.6): legal only on a
        # property of an update, and what mints `--unset-<prop>`.
        if f.get("nullable", False):
            fd_parts.append("nullable=True")
        fd_parts.extend(_validate_part(f))
        flag_decorators.append(
            f"{indent}@strictcli.flag({', '.join(fd_parts)})"
        )

    # Handler function
    # For optional args with no default, set handler param default to None
    # For variadic optional args, default to empty list
    # De-duplicate params to avoid SyntaxError (library validation catches duplicates at registration)
    seen_params = set()
    unique_params = []
    for p in params:
        if p not in seen_params:
            seen_params.add(p)
            unique_params.append(p)
    params = unique_params

    # A handler parameter bound to an OPTIONAL flag or arg must default to None
    # (contract §23.3): the framework refuses any other default at registration.
    optional_names = {
        d["name"]
        for d in list(cmd_def.get("args", [])) + list(cmd_def.get("flags", []))
        if d.get("presence") == "optional"
    }
    # The handler-annotation check is MANDATORY (§24.12): the parameter bound
    # to a selector must carry exactly the declared union, which is what makes
    # `assert_never` sound in a consumer's own handler.
    selector_annotations = {
        _flag_param(name): _selector_annotation(
            next(f for f in cmd_def["flags"] if f["name"] == name), class_vars,
        )
        for name, class_vars in selector_vars.items()
    }
    optional_names |= {
        f["name"].replace("-", "_")
        for f in cmd_def.get("flags", [])
        if f.get("presence") == "optional"
    }
    # Python forbids a parameter without a default after one that has it, so the
    # bare parameters are emitted first and the `=None` ones after. Declaration
    # order carries no meaning here: the framework passes every declared value
    # as a keyword argument on every dispatch (§23.3's delivery rule).
    # A case may override a parameter's default outright, which is the only
    # way to spell the re-sentinelization the handler-parameter check refuses
    # (§23.3): `def h(ctx, target="")` bound to an optional `--target`.
    overrides = cmd_def.get("handler_param_defaults", {})
    def _annotated(p: str) -> str:
        ann = selector_annotations.get(p)
        return f"{p}: {ann}" if ann else p

    param_strs = [
        _annotated(p) for p in params
        if p not in optional_names and p not in overrides
    ]
    param_strs += [
        f"{p}={overrides[p]!r}" if p in overrides else f"{p}=None"
        for p in params
        if p in optional_names or p in overrides
    ]
    # Handlers are ctx-first under the unified contract.
    sig_params = ", ".join(["ctx"] + param_strs)
    fn_name = f"{cmd_def['name'].replace('-', '_')}_handler"

    lines.extend(emitter.lines)
    lines.extend(decorator_parts)
    for fd in flag_decorators:
        lines.append(fd)
    lines.append(f"{indent}def {fn_name}({sig_params}):")

    # handler_effects runs BEFORE the handler_prints / handler_returns path and
    # does not replace it (§14.4). handler_diagnostics follows it, still before
    # that path.
    effect_lines = _emit_handler_effects(cmd_def, indent + "    ")
    lines.extend(effect_lines)
    claim_lines = _emit_handler_claim(cmd_def, indent + "    ")
    lines.extend(claim_lines)
    diag_lines = _emit_handler_diagnostics(cmd_def, indent + "    ")
    lines.extend(diag_lines)

    handler_returns = cmd_def.get("handler_returns")
    if cmd_def.get("handler_aborts", False):
        # The handler ABORTS rather than returning (§12.3's unwinding path).
        # ValueError, not a bespoke type: the script's top-level catch prints
        # it as "error: <msg>", which is byte-for-byte what the Go harness's
        # recover and the TS harness's catch print for the same abort.
        lines.append(f"{indent}    raise ValueError({HANDLER_ABORT_MESSAGE!r})")
    elif handler_returns is not None:
        # Survivor-contract cases pin an explicit return value.
        lines.extend(_emit_handler_return(handler_returns, indent + "    "))
    else:
        # A handler_effects-only command prints nothing; the effect calls above
        # are its whole body.
        if "handler_prints" in cmd_def:
            lines.append(_emit_handler_body(cmd_def, global_flags))
        elif not effect_lines and not diag_lines and not claim_lines:
            lines.append(f"{indent}    pass")
        # Unified return: build an Outcome carrying the exit code.
        lines.append(f"{indent}    return strictcli.outcome(exit_code={exit_code})")
    lines.append("")

    return "\n".join(lines)


def _emit_handler_claim(cmd_def: dict, indent: str) -> list[str]:
    """Emit the claimed-rendering calls (effects contract §19.7).

    Runs AFTER handler_effects and BEFORE handler_diagnostics /
    handler_prints, so a rendered log lands ahead of the handler's own output
    -- which is exactly the ordering the feature exists to make possible.
    """
    lines: list[str] = []
    if cmd_def.get("handler_claims_log", False):
        lines.append(f"{indent}ctx.effects.recorded()")
    if cmd_def.get("handler_payloads_recorded", False):
        lines.append(
            f"{indent}ctx.payload("
            f"[r['verb'] for r in ctx.effects.recorded()])"
        )
    if cmd_def.get("handler_renders_log", False):
        lines.append(f"{indent}ctx.effects.render_log()")
    return lines


def _emit_handler_return(hr: dict, indent: str) -> list[str]:
    """Emit the return statement for a handler_returns spec.

    Kinds: 'exit' (outcome(exit_code)), 'data' (ctx.payload + outcome()),
    'exit_data' (ctx.payload + outcome(exit_code)), 'none' (return None ->
    exit 0), and 'bad' (return an invalid value -> the framework's TypeError
    hard error).
    """
    kind = hr["kind"]
    code = hr.get("code", 0)
    if kind == "exit":
        return [f"{indent}return strictcli.outcome(exit_code={code})"]
    if kind == "data":
        return [
            f"{indent}ctx.payload({hr['data']!r})",
            f"{indent}return strictcli.outcome()",
        ]
    if kind == "exit_data":
        return [
            f"{indent}ctx.payload({hr['data']!r})",
            f"{indent}return strictcli.outcome(exit_code={code})",
        ]
    if kind == "none":
        return [f"{indent}return None"]
    if kind == "bad":
        # A return that is not int, None, or Outcome -- triggers the hard error.
        return [f"{indent}return ['not-an-outcome']"]
    raise ValueError(f"unknown handler_returns kind: {kind!r}")


def generate(app_def: dict) -> str:
    """Generate a complete Python script from an app definition.

    Returns the script source as a string.
    """
    has_toml = bool(app_def.get("checks_toml"))
    has_providers = bool(app_def.get("providers"))
    has_test_coverage = "test_coverage_dir" in app_def
    has_checks = has_toml or has_providers or has_test_coverage

    lines = []
    lines.append("import sys")
    lines.append("import os")
    if has_checks:
        lines.append("import pathlib")
    lines.append("")
    lines.append("# Add strictcli to path")
    lines.append("sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'python'))")
    lines.append("import strictcli")
    lines.append("")
    # The conformance corpus's one expressible validator shape (case-schema
    # `validate`): a callable refusing named values with a fixed message. All
    # three harnesses build the identical callable, so the resulting
    # `--<flag>: <message>` parse error is byte-identical.
    # The delivered-record vocabulary the three harnesses share: a class ->
    # choice-name map, a class -> declared field order, and one renderer. A
    # record renders as `<choice>` when its scope is empty and
    # `<choice>(<field>=<value>, ...)` otherwise, fields in declaration order.
    lines.append("_CHOICE_NAMES = {}")
    lines.append("_REC_FIELDS = {}")
    # The record front door's own vocabulary (contract §24.11's `call()` block):
    # a (selector name, choice name) -> choice class map, so a case can spell an
    # elected record as the flat map §25.6 publishes and each harness
    # materializes it in its own language's shape -- a choice instance here, an
    # Elect(<choice>, Fields{...}) in Go, the tagged object itself in
    # TypeScript.
    lines.append("_CHOICE_CLASS = {}")
    lines.append("")
    lines.append("def _sel_key(key, choice):")
    lines.append("    for cand in (key, key.replace('_', '-'), '*'):")
    lines.append("        if (cand, choice) in _CHOICE_CLASS:")
    lines.append("            return cand")
    lines.append("    return None")
    lines.append("")
    lines.append("def _mk_rec(sel, obj):")
    lines.append("    cls = _CHOICE_CLASS[(sel, obj['choice'])]")
    lines.append("    kwargs = {}")
    lines.append("    for k, v in obj.items():")
    lines.append("        if k == 'choice':")
    lines.append("            continue")
    lines.append("        key = k.replace('-', '_')")
    lines.append("        if isinstance(v, dict) and 'choice' in v:")
    lines.append("            nested = _sel_key(k, v['choice'])")
    lines.append("            if nested is not None:")
    lines.append("                v = _mk_rec(nested, v)")
    lines.append("        kwargs[key] = v")
    lines.append("    return cls(**kwargs)")
    lines.append("")
    lines.append("def _rec_kwargs(d):")
    lines.append("    out = {}")
    lines.append("    for k, v in d.items():")
    lines.append("        if isinstance(v, dict) and 'choice' in v:")
    lines.append("            sel = _sel_key(k, v['choice'])")
    lines.append("            if sel is not None:")
    lines.append("                v = _mk_rec(sel, v)")
    lines.append("        out[k] = v")
    lines.append("    return out")
    lines.append("")
    lines.append("def _sel_fmt(v):")
    lines.append("    if type(v) in _CHOICE_NAMES:")
    lines.append("        name = _CHOICE_NAMES[type(v)]")
    lines.append("        fields = _REC_FIELDS[type(v)]")
    lines.append("        if not fields:")
    lines.append("            return name")
    lines.append("        inner = ', '.join(")
    lines.append("            f + '=' + _sel_fmt(getattr(v, f)) for f in fields")
    lines.append("        )")
    lines.append("        return name + '(' + inner + ')'")
    lines.append("    if v is None:")
    lines.append("        return 'None'")
    lines.append("    if isinstance(v, bool):")
    lines.append("        return 'true' if v else 'false'")
    lines.append("    if isinstance(v, list):")
    lines.append("        return ','.join(str(x) for x in v)")
    lines.append("    if isinstance(v, dict):")
    lines.append("        return ','.join(str(k) + '=' + str(x) for k, x in sorted(v.items()))")
    lines.append("    return str(v)")
    lines.append("")
    lines.append("def _sel_walk(rec, parts):")
    lines.append("    cur = rec")
    lines.append("    for p in parts:")
    lines.append("        if p == 'choice':")
    lines.append("            cur = _CHOICE_NAMES[type(cur)]")
    lines.append("        else:")
    lines.append("            cur = getattr(cur, p.replace('-', '_'))")
    lines.append("    return cur")
    lines.append("")
    lines.append("def _mk_validate(rejects, message):")
    lines.append("    def _validate(value):")
    lines.append("        if str(value) in rejects:")
    lines.append("            raise ValueError(message)")
    lines.append("    return _validate")
    lines.append("")

    # The aborting-check exception type. Spelled identically in all three
    # harnesses so the framework's containment line names the same type on
    # every target.
    if any(c.get("aborts", False) for c in app_def.get("checks", [])):
        lines.append("class CheckAborted(Exception):")
        lines.append("    pass")
        lines.append("")

    # Hand checks.toml to the app in memory, through checks_embed=, the way the
    # Go harness (WithChecksEmbed) and the TypeScript harness (checksEmbed) do.
    # The earlier spelling wrote the document to a path under the system temp
    # directory and passed checks_path=; nothing ever deleted those files.
    if has_toml:
        checks_toml = app_def["checks_toml"]
        lines.append("# The checks document, embedded rather than written to disk")
        lines.append(f"_checks_embed = {checks_toml.encode()!r}")
        lines.append("")

    # Build app
    global_flags = app_def.get("global_flags", [])
    app_parts = [
        f"name={app_def['name']!r}",
        f"version={app_def['version']!r}",
        f"help={app_def['help']!r}",
    ]
    if "env_prefix" in app_def:
        app_parts.append(f"env_prefix={app_def['env_prefix']!r}")
    if app_def.get("config", False):
        app_parts.append("config=True")
    if "config_path_relative_to_root" in app_def:
        _cprtr = app_def["config_path_relative_to_root"]
        _cp_args = ", ".join(
            [repr(_cprtr["env_var"])] + [repr(p) for p in _cprtr.get("parts", [])]
        )
        app_parts.append(f"config_path=strictcli.RelativeToRoot({_cp_args})")
    if "config_path" in app_def and app_def["config_path"] is not None:
        app_parts.append(f"config_path={app_def['config_path']!r}")
    if "schema_path" in app_def and app_def["schema_path"] is not None:
        app_parts.append(f"schema_path={app_def['schema_path']!r}")
    if "config_format" in app_def and app_def["config_format"] != "json":
        app_parts.append(f"config_format={app_def['config_format']!r}")
    if app_def.get("no_default_config_path", False):
        app_parts.append("no_default_config_path=True")
    if "config_conflict_mode" in app_def and app_def["config_conflict_mode"] != "cli-wins":
        app_parts.append(f"config_conflict_mode={app_def['config_conflict_mode']!r}")
    if "infra_root" in app_def:
        app_parts.append(f"infra_root={app_def['infra_root']!r}")
    if "handshake_env" in app_def:
        app_parts.append(f"handshake_env={app_def['handshake_env']!r}")
    if has_toml:
        app_parts.append("checks_embed=_checks_embed")
    if "test_coverage_dir" in app_def:
        app_parts.append(f"test_coverage_dir={app_def['test_coverage_dir']!r}")
    if app_def.get("proc_observe_allowlist"):
        app_parts.append(
            f"proc_observe_allowlist={app_def['proc_observe_allowlist']!r}"
        )
    if global_flags:
        gf_exprs = [_emit_flag(gf) for gf in global_flags]
        app_parts.append(f"flags=[{', '.join(gf_exprs)}]")

    lines.append("try:")
    lines.append(f"    app = strictcli.App({', '.join(app_parts)})")
    lines.append("")

    # Register config fields (before commands, since commands may bind to them)
    for cf_def in app_def.get("config_fields_def", []):
        cf_parts = [f"{cf_def['name']!r}", f"type={cf_def['type']!s}"]
        cf_parts.append(f"help={cf_def['help']!r}")
        if "default" in cf_def:
            cf_parts.append(f"default={cf_def['default']!r}")
        lines.append(f"    app.config_field({', '.join(cf_parts)})")
    if app_def.get("config_fields_def"):
        lines.append("")

    # Register groups first (recursive helper for nested groups)
    def _emit_group(group_def: dict, parent_var: str, indent: str) -> None:
        gvar = f"group_{group_def['name'].replace('-', '_')}"
        tags_arg = ""
        if group_def.get("tags"):
            tag_set = ", ".join(repr(t) for t in group_def["tags"])
            tags_arg = f", tags={{{tag_set}}}"
        hidden_arg = ""
        if group_def.get("hidden", False):
            hidden_arg = ", hidden=True"
        lines.append(
            f"{indent}{gvar} = {parent_var}.group({group_def['name']!r}, help={group_def['help']!r}{tags_arg}{hidden_arg})"
        )
        lines.append("")
        for cmd_def in group_def.get("commands", []):
            if cmd_def.get("deprecated"):
                lines.append(
                    f"{indent}{gvar}.deprecate({cmd_def['name']!r}, "
                    f"message={cmd_def.get('deprecated_message', '')!r}"
                    f"{_deprecated_effect_arg(cmd_def)})"
                )
                lines.append("")
            else:
                lines.append(textwrap.indent(_emit_command_registration(
                    cmd_def, gvar, global_flags=global_flags,
                ), indent))
        for sub_group_def in group_def.get("groups", []):
            _emit_group(sub_group_def, gvar, indent)

    for group_def in app_def.get("groups", []):
        _emit_group(group_def, "app", "    ")

    # Register top-level commands
    for cmd_def in app_def.get("commands", []):
        if cmd_def.get("deprecated"):
            lines.append(
                f"    app.deprecate({cmd_def['name']!r}, "
                f"message={cmd_def.get('deprecated_message', '')!r}"
                f"{_deprecated_effect_arg(cmd_def)})"
            )
            lines.append("")
        else:
            lines.append(textwrap.indent(_emit_command_registration(
                cmd_def, "app", global_flags=global_flags,
            ), "    "))

    # Register tag contracts
    for tag, contract in app_def.get("tag_contracts", {}).items():
        lines.append(f"    app.tag_contract({tag!r}, requires_flag={contract['requires_flag']!r})")
    if app_def.get("tag_contracts"):
        lines.append("")

    # Register checks if defined. The registration form (error_check vs
    # warn_check) is derived from the check's severity in the embedded
    # checks_toml -- the case only describes what the impl mints via its reporter.
    if has_toml:
        severities = _check_severities(app_def["checks_toml"])
        for check_def in app_def.get("checks", []):
            cname = check_def["name"]
            decorator = "warn_check" if severities.get(cname) == "warn" else "error_check"
            fn_name = f"check_{cname.replace('-', '_')}"
            lines.append(f"    @app.{decorator}({cname!r})")
            lines.append(f"    def {fn_name}(ctx, reporter):")
            lines.extend(_emit_check_impl_body(check_def, "        "))
            lines.append("")

    # Register check providers. Each provider is a list of specs it returns;
    # every spec carries its 8 meta fields inline (providers have no TOML). The
    # registration form (error_check_spec vs warn_check_spec) is the spec's
    # impl_form (defaults to its severity); a spec whose impl_form differs from
    # its severity pins the materialization-time severity-mismatch hard error.
    if has_providers:
        for pi, provider_specs in enumerate(app_def["providers"]):
            lines.append(f"    def _provider_{pi}():")
            spec_meta = []
            for si, spec in enumerate(provider_specs):
                impl_name = f"_impl_{pi}_{si}"
                lines.append(f"        def {impl_name}(ctx, reporter):")
                lines.extend(_emit_check_impl_body(spec, "            "))
                spec_meta.append((impl_name, spec))
            lines.append("        return [")
            for impl_name, spec in spec_meta:
                impl_form = spec.get("impl_form", spec["severity"])
                ctor = "warn_check_spec" if impl_form == "warn" else "error_check_spec"
                lines.append(f"            strictcli.{ctor}(")
                lines.append(f"                name={spec['name']!r},")
                lines.append(f"                tags={spec['tags']!r},")
                lines.append(f"                severity={spec['severity']!r},")
                lines.append(f"                fast={bool(spec['fast'])},")
                lines.append(f"                pure={bool(spec['pure'])},")
                lines.append(f"                needs_network={bool(spec['needs_network'])},")
                lines.append(f"                depends_on={spec['depends_on']!r},")
                lines.append(f"                scope={spec.get('scope', '')!r},")
                lines.append(f"                impl={impl_name},")
                lines.append(f"            ),")
            lines.append("        ]")
            lines.append(f"    app.register_check_provider(_provider_{pi})")
            lines.append("")

    if has_checks:
        lines.append("    class _CheckCtx:")
        lines.append("        project_root = pathlib.Path('.')")
        lines.append("")
        lines.append("    app.set_check_context(lambda: _CheckCtx())")
        lines.append("")

    # The confirm protocol's interactive branch is otherwise unreachable from a
    # subprocess: a case's stdin is a pipe, and a pipe is not a TTY in any of
    # the three implementations. The framework's test-only confirm seam says the
    # answer channel IS interactive and leaves the answer itself coming from the
    # case's real stdin -- WHERE the answer comes from, never WHETHER the
    # protocol runs.
    if app_def.get("confirm_stdin_interactive", False):
        lines.append("    class _ConfirmStdin:")
        lines.append("        def is_interactive(self):")
        lines.append("            return True")
        lines.append("")
        lines.append("        def read_line(self):")
        lines.append("            return sys.stdin.readline()")
        lines.append("")
        lines.append("    app._set_confirm_io(_ConfirmStdin())")
        lines.append("")

    # Write config_content_late AFTER construction but BEFORE run
    if "config_content_late" in app_def:
        late_content = app_def["config_content_late"]
        config_path_expr = f"app._config_path_override" if "config_path" in app_def else "None"
        lines.append(f"    # Write late config content")
        lines.append(f"    with open({app_def['config_path']!r}, 'w') as _lcf:")
        lines.append(f"        _lcf.write({late_content!r})")
        lines.append("")

    # Pre-test argv lists: run app.test() for each before app.run().
    # Used by test_coverage_dir conformance cases to generate shard files.
    for pre_argv in app_def.get("pre_test", []):
        lines.append(f"    app.test({pre_argv!r})")
    if app_def.get("pre_test"):
        lines.append("")

    # Tool descriptor dump: the exported classification, one line per tool.
    if app_def.get("dump_tools"):
        lines.append("    for _tool in app.as_tools():")
        lines.append("        print(f'tool: {_tool.name} effect={_tool.effect} "
                     "consequential={str(_tool.consequential).lower()}')")
        lines.append("")

    # Programmatic calls: the app.call() channel, which argv cannot reach.
    for pre_call in app_def.get("pre_call", []):
        cmd = pre_call["command"]
        kwargs = pre_call.get("kwargs", {})
        approve = bool(pre_call.get("approve_consequential", False))
        lines.append("    try:")
        lines.append(
            f"        app.call({cmd!r}, approve_consequential={approve!r}, "
            f"**_rec_kwargs({kwargs!r}))"
        )
        lines.append(f"        print('call ok: {cmd}')")
        lines.append("    except strictcli.InvokeError as _ce:")
        lines.append("        print(f'call error: {_ce}', file=sys.stderr)")
    if app_def.get("pre_call"):
        lines.append("")

    # The structured effect-log side channel (§14.3): the same env-var file
    # handoff as CONFORMANCE_APP_DEF. app.run() ends in sys.exit, so the write
    # rides atexit -- the Python counterpart of the Go harness's SetExitHook and
    # the TS harness's process.on("exit").
    lines.append("    _effect_log_path = os.environ.get('CONFORMANCE_EFFECT_LOG')")
    lines.append("    if _effect_log_path:")
    lines.append("        import atexit, json as _json")
    lines.append("")
    lines.append("        def _write_effect_log():")
    lines.append("            with open(_effect_log_path, 'w') as _elf:")
    lines.append("                _json.dump(app.effect_log(), _elf, sort_keys=True,")
    lines.append("                           separators=(',', ':'))")
    lines.append("")
    lines.append("        atexit.register(_write_effect_log)")
    lines.append("")
    lines.append("    app.run()")
    # The sibling harnesses both wrap the whole run: Go recovers any panic and
    # TypeScript catches any throw, each printing "error: <msg>" and exiting 1.
    # Python catches every Exception for the same reach. (SystemExit and the
    # framework's _DryRunTruncated derive from BaseException and stay uncaught,
    # exactly as before.)
    #
    # The framework's own error vocabulary prints bare, because the siblings
    # mirror those messages verbatim: ValueError, and EffectFailed -- the
    # effect-failure type whose Go and TypeScript equivalents also print bare.
    # Everything else is tagged with its type name, so a genuine harness bug
    # (a codegen TypeError, an AttributeError) stays distinguishable from a
    # framework error instead of masquerading as one.
    lines.append("except (ValueError, strictcli.EffectFailed) as e:")
    lines.append("    print(f'error: {e}', file=sys.stderr)")
    lines.append("    sys.exit(1)")
    lines.append("except Exception as e:")
    lines.append("    print(f'error: {type(e).__name__}: {e}', file=sys.stderr)")
    lines.append("    sys.exit(1)")
    lines.append("")

    return "\n".join(lines)
