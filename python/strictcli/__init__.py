"""A strict CLI framework for Python with mandatory help text, type-safe flags, groups, and schema export."""

from __future__ import annotations

__version__ = "0.42.0"

__all__ = [
    "App", "Flag", "Arg", "FlagSet",
    # The constraint system (contract §26)
    "AtLeastOne", "AllOrNone", "Requires", "Implies", "Member",
    # The update-command construct (contract §27)
    "UpdateOf",
    "Passthrough", "Forwarding", "DeprecatedCommand", "Result",
    "InvokeError",
    # The scoped-selector construct (contract §24)
    "Choice", "choice", "choice_flag", "sub_flag", "sub_choice_flag",
    "member_value", "provided",
    # Retired choices: the value-level twin of the deprecated-command construct
    "RetiredChoice",
    "Grant", "EffectFailed", "Unsettled", "Completed", "Spawned", "Response",
    "PROC_MUTATE", "PROC_SPAWN", "FILE_WRITE", "NET_MUTATE",
    "flag", "arg",
    "CheckContext", "ConnectionEnvReader", "CheckRunResult",
    "ErrorReporter", "WarnReporter", "SkipCheck",
    "CheckSpec", "error_check_spec", "warn_check_spec",
    "format_check_results", "format_check_results_json",
    "ConfigField",
    "Context",
    "Outcome", "outcome",
    "Tool",
    "RelativeToRoot",
]

import ast
import base64
import binascii
import calendar
import contextlib
import dataclasses
import decimal
import keyword
import fnmatch
import hashlib
import hmac
import inspect
import io
import json
import math
import os
import re
import subprocess
import sys
import time
import tomllib
import types as _pytypes
import typing

import tomlkit
from tomlkit.items import InlineTable, Table
from collections import deque
from collections.abc import Iterable, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import (
    Any, Callable, NamedTuple, Protocol, TypeVar,
    get_args, get_origin, get_type_hints, runtime_checkable,
)

# TypeVar for decorator return types — preserves the decorated function's type
F = TypeVar("F", bound=Callable[..., Any])


# Sentinel for distinguishing "not provided" from actual values
class _MissingSentinel:
    def __repr__(self) -> str:
        return "_MISSING"


_MISSING = _MissingSentinel()

# The envelope contract's own version (effects contract §19.2). Changed only by
# a later amendment to that section.
_INTERFACE_VERSION = 2


class RelativeToRoot:
    """Opaque marker: a filesystem path relative to a declared infrastructure root.

    Produced as ``RelativeToRoot(env_var, *parts)`` and accepted by a flag's
    ``default=`` and by ``App(config_path=...)``. env_var names the root
    (declared via ``App(infra_root={env_var: default})``); parts are joined onto
    the resolved root path. Config-path markers resolve eagerly at construction;
    flag-default markers resolve when defaults are applied at parse time. A marker
    referencing an undeclared root is a registration-time hard error.
    """

    __slots__ = ("env_var", "parts")

    def __init__(self, env_var: str, *parts: str) -> None:
        self.env_var = env_var
        self.parts = list(parts)

    def __repr__(self) -> str:
        return f"RelativeToRoot({self.env_var!r}, {', '.join(map(repr, self.parts))})"


def _serialize_marker(ref: RelativeToRoot) -> dict:
    """Serialize a RelativeToRoot marker to a machine-stable JSON shape.

    Emits only the declared env var and path parts -- never the resolved,
    machine-specific path. The shape is identical across the Python and Go
    implementations so the schema round-trips and cross-language byte-compares.
    """
    return {"relative_to_root": {"env_var": ref.env_var, "parts": list(ref.parts)}}


def _resolve_infra_root_path(ref: RelativeToRoot, roots: dict[str, str]) -> str:
    """Resolve a RelativeToRoot marker against a roots map (env var -> path).

    Raises ValueError if the marker references an undeclared root.
    """
    root = roots.get(ref.env_var)
    if root is None:
        raise ValueError(
            f'RelativeToRoot references undeclared infra root "{ref.env_var}"; '
            f"declare it as an infra root"
        )
    return os.path.join(root, *ref.parts)


def _validate_connection_binding(f: "Flag", connection_env_names) -> None:
    """Enforce the connection-URL binding rules at registration time (mechanical
    enforcement, not review). A URL-class flag must bind to a declared connection
    env; the binding drives env resolution by reusing the per-flag env channel
    (connection_env is folded into env)."""
    if not f.connection_url and f.connection_env is None:
        return
    if f.connection_env is not None and f.env is not None and f.env != f.connection_env:
        raise ValueError(
            f'flag "{f.name}": a connection-URL binding cannot be combined with a per-flag env var'
        )
    if f.connection_url and f.connection_env is None:
        raise ValueError(
            f'flag "{f.name}": connection-URL flag must bind to a declared connection env'
        )
    if f.connection_env is not None and not f.connection_url:
        raise ValueError(
            f'flag "{f.name}": connection env binding requires the flag to be marked as a connection-URL flag'
        )
    if f.connection_env not in connection_env_names:
        raise ValueError(
            f'flag "{f.name}": connection-URL flag binds to undeclared connection env '
            f'"{f.connection_env}"; declare it as a connection env'
        )
    f.env = f.connection_env


# ---------------------------------------------------------------------------
# Source provenance (Phase 0c)
# ---------------------------------------------------------------------------

class _Source:
    """Where a flag value came from."""
    CLI = "cli"          # explicitly passed on the command line
    ENV = "env"          # from an environment variable
    CONFIG = "config"    # from a config file
    DEFAULT = "default"  # from the flag's default value
    IMPLIED = "implied"  # injected by an Implies dependency
    INFRA = "infra"      # default resolved through a RelativeToRoot infra root


class _SourcedEntry:
    """A value paired with its provenance source."""
    __slots__ = ("value", "source")

    def __init__(self, value: object, source: str) -> None:
        self.value = value
        self.source = source


class _SourcedStore:
    """Map of flag-name to _SourcedEntry with source-filtered presence queries.

    Replaces the plain ``cli_set: dict[str, object]`` in the validation
    pipeline, adding provenance tracking for each value.
    """

    def __init__(self) -> None:
        self._entries: dict[str, _SourcedEntry] = {}

    def set(self, name: str, value: object, source: str) -> None:
        self._entries[name] = _SourcedEntry(value, source)

    def get(self, name: str) -> tuple[object, bool]:
        """Return (value, True) or (None, False)."""
        e = self._entries.get(name)
        if e is None:
            return None, False
        return e.value, True

    def has(self, name: str) -> bool:
        return name in self._entries

    def get_value(self, name: str) -> object:
        """Return the value or raise KeyError."""
        return self._entries[name].value

    def set_value(self, name: str, value: object) -> None:
        """Update the value of an existing entry, keeping its source."""
        self._entries[name].value = value

    def is_cli(self, name: str) -> bool:
        """True when the value came from a command-line token.

        Mutex election is CLI-only (contract §21.3): env and config sources
        neither elect a member nor supply its value.
        """
        e = self._entries.get(name)
        if e is None:
            return False
        return e.source == _Source.CLI

    def is_env_or_config(self, name: str) -> bool:
        """True when the value came from an env var or the config file."""
        e = self._entries.get(name)
        if e is None:
            return False
        return e.source in (_Source.ENV, _Source.CONFIG)

    def delete(self, name: str) -> None:
        """Drop an entry entirely, so defaults apply to it later."""
        self._entries.pop(name, None)

    def is_present_for_deps(self, name: str) -> bool:
        """Present for the constraint system (§26): the invocation caused the
        value.

        This is the single definition of "was this supplied" (contract §23.6):
        `cli`, `env`, `config` and `implied` count; `default` and `infra` do
        not, both being the declaration deciding rather than the invocation.
        ``ctx.provided`` answers off the same predicate.
        """
        e = self._entries.get(name)
        if e is None:
            return False
        return e.source not in (_Source.DEFAULT, _Source.INFRA)

    def __contains__(self, name: str) -> bool:
        return name in self._entries

    def __setitem__(self, name: str, value: object) -> None:
        # Convenience for migration: stores with SourceCLI by default.
        # Only used in parsing contexts where source is CLI.
        self._entries[name] = _SourcedEntry(value, _Source.CLI)

    def __getitem__(self, name: str) -> object:
        return self._entries[name].value

    def source_map(self) -> dict[str, str]:
        """Return a dict mapping flag names to source labels."""
        return {k: e.source for k, e in self._entries.items()}

    @classmethod
    def from_dict(cls, d: dict[str, object], source: str) -> "_SourcedStore":
        """Build a store from a plain dict, marking all entries with source."""
        store = cls()
        for k, v in d.items():
            store.set(k, v, source)
        return store


class _InfraAccess:
    """A Context's view of infrastructure env vars: resolved root values
    (captured at construction), declared handshake env vars (read live), and
    declared connection env vars (read live, but suppressed under --hermetic)."""

    __slots__ = ("roots", "handshakes", "connections", "hermetic")

    def __init__(self, roots: dict[str, str], handshakes: set[str],
                 connections: set[str] | None = None, hermetic: bool = False) -> None:
        self.roots = roots
        self.handshakes = handshakes
        self.connections = connections or set()
        self.hermetic = hermetic


class Context:
    """Structured output context for command handlers.

    Always injected as the first positional argument to every handler.
    Provides info/warn/debug/error methods that route to the correct stream,
    plus source/infra_value provenance accessors. To return structured data,
    a handler calls ``ctx.payload(value)`` against the command's declared
    ``payload_schema=`` (contract §19.4).
    """

    def __init__(self, stdout=None, stderr=None, sources=None, infra=None,
                 *, dry_run: bool = False,
                 approve_consequential: bool = False,
                 quiet: bool = False, verbose: bool = False,
                 json: bool = False,
                 effects: "_Effects | None" = None,
                 command_name: str = "",
                 payload_schema: object | None = None,
                 unsets: set | None = None):
        self._stdout = stdout or sys.stdout
        self._stderr = stderr or sys.stderr
        self._sources = sources or {}  # flag-name -> source label (cli/env/config/default/implied/infra)
        # The properties this invocation CLEARED (contract §27.6), keyed by
        # DECLARED (dashed) name. `ctx.unset` answers off this set.
        self._unsets = unsets or set()
        self._infra = infra  # _InfraAccess | None
        self._dry_run = dry_run
        self._approve_consequential = approve_consequential
        self._quiet = quiet
        self._verbose = verbose
        self._json = json
        self._effects = effects
        # The payload slot (contract §19.4): at most one value per dispatch,
        # settable only on a command that declared a payload schema.
        self._command_name = command_name
        self._payload_schema = payload_schema
        self._payload_value: object = _MISSING
        # The diagnostics this dispatch emitted, in emission order (contract
        # §19.2). In machine mode the context writers record here instead of
        # writing: what they were asked to say rides the envelope. Outside
        # machine mode the list stays empty and nothing changes.
        self._diagnostics: list[dict] = []

    @property
    def dry_run(self) -> bool:
        """True when the framework-owned ``--dry-run`` flag was passed."""
        return self._dry_run

    @property
    def approve_consequential(self) -> bool:
        """True when the framework-owned ``--approve-consequential`` flag was passed."""
        return self._approve_consequential

    @property
    def quiet(self) -> bool:
        """True when the framework-owned ``--quiet`` flag was passed."""
        return self._quiet

    @property
    def verbose(self) -> bool:
        """True when the framework-owned ``--verbose`` flag was passed."""
        return self._verbose

    @property
    def json(self) -> bool:
        """True when the framework-owned ``--json`` flag was passed.

        ``--json`` selects machine mode (contract §19.1). Handlers do not
        branch on it to decide whether to build a payload -- ``ctx.payload``
        is mode-independent and the framework decides what to do with the
        value -- but the flag is exposed for symmetry with the quartet and for
        apps that propagate it to a child process.
        """
        return self._json

    def payload(self, value: object) -> None:
        """Supply this dispatch's machine payload (contract §19.4).

        The call is mode-independent: a handler calls it identically in both
        modes and never branches on ``ctx.json``. In machine mode the value is
        emitted; outside machine mode it is not printed at all. ``test()`` and
        ``call()`` capture it either way.

        Two hard errors, both at call time, and both §19.4's own rules:

        - the command declared no ``payload_schema=``, so there is nothing to
          validate the value against;
        - a payload was already supplied in this dispatch (one slot, one
          answer).

        The value itself is validated against the declared schema at the
        EMISSION seam (§19.4, §19.5) -- only where machine mode actually writes
        the envelope. Validating here instead would make a payload that is
        legal in human mode fail a run that was never going to emit it, which
        §19.4's call-unconditionally rule forbids.
        """
        if self._payload_schema is None:
            raise RuntimeError(_msg_payload_no_schema(self._command_name))
        if self._payload_value is not _MISSING:
            raise RuntimeError(_msg_payload_already_set(self._command_name))
        self._payload_value = value

    @property
    def effects(self) -> "_Effects":
        """The effects handle for this run (see the effects-regime contract)."""
        if self._effects is None:
            raise RuntimeError(_msg_effects_unavailable())
        return self._effects

    def _diagnostic(self, level: str, msg: str) -> bool:
        """Record a diagnostic in machine mode. True when it was recorded.

        In machine mode the writers below write nothing and what they were
        asked to say rides the envelope's ``diagnostics`` instead (§19.1).
        The recording is NOT filtered by ``--quiet`` or ``--verbose``: the
        envelope's content is a function of what the run produced, never of how
        a terminal was configured (§19.2).
        """
        if not self._json:
            return False
        self._diagnostics.append({"level": level, "message": msg})
        return True

    def info(self, msg: str) -> None:
        """Write an informational message to stdout (hidden under --quiet)."""
        if self._diagnostic("info", msg):
            return
        if self._quiet:
            return
        print(msg, file=self._stdout)

    def warn(self, msg: str) -> None:
        """Write a warning message to stderr (never suppressed)."""
        if self._diagnostic("warn", msg):
            return
        print(msg, file=self._stderr)

    def debug(self, msg: str) -> None:
        """Write a debug message to stdout (shown only under --verbose).

        ``--quiet`` dominates ``--verbose``: passing both hides debug output.
        """
        if self._diagnostic("debug", msg):
            return
        if self._quiet or not self._verbose:
            return
        print(msg, file=self._stdout)

    def error(self, msg: str) -> None:
        """Write an error message to stderr (never suppressed)."""
        if self._diagnostic("error", msg):
            return
        print(msg, file=self._stderr)

    def source(self, name: str) -> str:
        """Return the provenance source label for a flag.

        Returns one of: "cli", "env", "config", "default", "implied", "infra".
        ("infra" indicates the value came from a RelativeToRoot default resolved
        through a declared infrastructure root.)
        Raises KeyError if the flag name is not found.
        """
        key = name.replace("-", "_")
        if key in self._sources:
            return self._sources[key]
        # Try original name (with dashes)
        if name in self._sources:
            return self._sources[name]
        raise KeyError(f"no source info for flag {name!r}")

    def provided(self, name: str) -> bool:
        """Return True when the INVOCATION caused this flag's value.

        `cli`, `env`, `config` and `implied` are provided; `default` and `infra`
        are not -- those are the declaration deciding (contract §23.6). An
        optional flag that received nothing carries source `default`, so it
        reports False.

        Accepts dashed or underscored names and raises the same ``KeyError`` as
        :meth:`source` for an unknown name: one condition, one message.
        """
        return self.source(name) in _PROVIDED_SOURCES

    def unset(self, name: str) -> bool:
        """Return True when this invocation CLEARED the named property (§27.6).

        ``--unset-<prop>`` on the command line, or ``null`` on the property's
        own key at a machine door. An unset property delivers absence -- the
        same ``None`` an untouched property delivers -- and reports
        ``provided()`` true, the invocation having caused the write. This is
        what saves a handler from reconstructing that boolean out of two facts,
        which is §23.6's own reason for existing.

        Accepts dashed or underscored names and raises the same ``KeyError`` as
        :meth:`source` and :meth:`provided` for an unknown name: it reads the
        same per-parse store, so a name with no source has no clear either.
        """
        key = name.replace("-", "_")
        if key not in self._sources and name not in self._sources:
            raise KeyError(f"no source info for flag {name!r}")
        return name.replace("_", "-") in self._unsets

    def infra_value(self, env_var: str) -> tuple[str | None, bool]:
        """Return the value of a declared infrastructure env var.

        For a declared location root (``infra_root``), returns the value
        resolved eagerly at construction (env var if set, else the declared
        default) and ``True`` -- the resolved value is always available.

        For a declared handshake var (``handshake_env``), reads the environment
        LIVE at call time (handshakes are set by the invoking process and carry
        no construction-time value), returning ``(value, is_set)``.

        For a declared connection env (``connection_env``), reads the environment
        LIVE at call time and returns ``(value, is_set)`` -- EXCEPT under
        --hermetic, where it resolves as absent ``(None, False)`` so
        connection-dependent behavior skips visibly instead of connecting.

        Raises KeyError if env_var is not a declared root, handshake, or
        connection var.
        """
        if self._infra is not None:
            if env_var in self._infra.roots:
                return self._infra.roots[env_var], True
            if env_var in self._infra.handshakes:
                if env_var in os.environ:
                    return os.environ[env_var], True
                return None, False
            if env_var in self._infra.connections:
                if self._infra.hermetic:
                    return None, False
                if env_var in os.environ:
                    return os.environ[env_var], True
                return None, False
        raise KeyError(
            f'"{env_var}" is not a declared infra root, handshake, or connection env var'
        )

    def connection_env_value(self, env_var: str) -> tuple[str | None, bool]:
        """Return the value of a declared connection env (``connection_env``),
        read LIVE at call time -- EXCEPT under --hermetic, where it resolves as
        absent ``(None, False)``. Raises KeyError if env_var is not a declared
        connection env. This is the check-side and handler-side accessor for the
        connection-URL kind; see also ``infra_value``, which resolves all three
        kinds.
        """
        if self._infra is not None and env_var in self._infra.connections:
            if self._infra.hermetic:
                return None, False
            if env_var in os.environ:
                return os.environ[env_var], True
            return None, False
        raise KeyError(
            f'"{env_var}" is not a declared connection env var'
        )


# ---------------------------------------------------------------------------
# The effects regime
#
# Command classification (read_only / mutating), the ctx.effects handle, dry
# mode's would-do log, and the Unsettled carriers that make a data-flow preview
# complete without letting the framework invent a value it cannot know.
#
# Two rules govern the whole regime: FAIL CLOSED (when the framework cannot
# prove an operation is safe to preview, it stops with a precise error instead
# of guessing) and ZERO INFERENCE (nothing is inferred -- not classification,
# not whether an argument is a path, not whether a resource is current).
# ---------------------------------------------------------------------------

# Effect kinds. CACHE_WRITE has NO public method: it is minted only by
# framework-internal code (schema dump, test-coverage shards and manifest) and
# is unreachable from application code.
PROC_MUTATE = "proc_mutate"
PROC_SPAWN = "proc_spawn"
FILE_WRITE = "file_write"
NET_MUTATE = "net_mutate"
CACHE_WRITE = "cache_write"

# The kinds a Grant may be declared for (CACHE_WRITE is excluded: it is not
# reachable from application code, so nothing could ever use such a grant).
_GRANTABLE_KINDS = (PROC_MUTATE, PROC_SPAWN, FILE_WRITE, NET_MUTATE)
_GRANT_NAME_RE = re.compile(r"^[a-z][a-z0-9-]*$")


class EffectFailed(Exception):
    """A failed effect operation.

    A failed operation is an error, not a value: a ``run`` whose child exits
    nonzero and an ``http`` whose status is outside 200-299 raise this, as does
    invalid UTF-8 on a captured stream. ``check=False`` opts a single call out.
    """


class _DryRunTruncated(BaseException):
    """Raised when handler code extracts from or branches on an Unsettled value.

    Derives from BaseException deliberately: a handler's ``except Exception``
    must not be able to swallow the truncation and let the preview continue
    with a value the framework refuses to invent.
    """

    def __init__(self, message: str, log: "_EffectLog", *,
                 step: int, cmd_path: str, brand: str) -> None:
        super().__init__(message)
        self.message = message
        self.log = log
        # The three values §12.5's text is built from, kept apart from it so
        # the envelope's preview_error can carry them as members (§19.3)
        # without re-parsing the rendered message.
        self.step = step
        self.cmd_path = cmd_path
        self.brand = brand


@dataclass(frozen=True)
class Grant:
    """A per-command, per-effect-kind authorization with a mandatory reason.

    A grant is not permission to do something otherwise forbidden; it is a
    labelled reason that surfaces in the preview so a reviewer reading a dry
    run sees why a dangerous step is there.
    """

    name: str
    reason: str
    kind: str


@dataclass(frozen=True)
class Completed:
    """The result of a subprocess that ran to completion.

    ``stdout``/``stderr`` are the child's output decoded as UTF-8 strictly,
    with a single trailing newline removed if present -- the form that can be
    forwarded straight into a later effect's argv.
    """

    exit_code: int
    stdout: str
    stderr: str


@dataclass(frozen=True)
class Response:
    """The result of an HTTP request. Header names are lower-cased."""

    status: int
    body: bytes
    headers: dict


@dataclass(frozen=True)
class Spawned:
    """A handle for a started-but-not-awaited child process."""

    pid: int
    _proc: object = field(default=None, repr=False, compare=False)
    _cmd_path: str = field(default="", repr=False, compare=False)

    def wait(self, *, check: bool = True) -> Completed:
        """Wait for the child and return its Completed result.

        ``check`` mirrors ``run``'s opt-out: with the default ``True`` a
        nonzero exit raises :class:`EffectFailed`.
        """
        code = self._proc.wait()
        argv = " ".join(str(a) for a in self._proc.args)
        if check and code != 0:
            # One template covers run and spawn (the method name is the
            # parameter), so the parity catalogs carry one signature, not two.
            _raise_effect_run_failed(self._cmd_path, "spawn", argv, code)
        # spawn always streams (the child inherits stdio), so there is nothing
        # captured to report.
        return Completed(exit_code=code, stdout="", stderr="")


# The dunders Unsettled poisons. Every one of them is an EXTRACTION or a
# BRANCH: reading a concrete value out of a carrier, or deciding something from
# it. `__repr__` is the single non-poisoned dunder (so debuggers, tracebacks and
# logging never themselves detonate) and `__class__` is untouched (isinstance
# must work -- the effects API uses it at the forwarding boundary).
_UNSETTLED_POISONED_DUNDERS = (
    "__bool__", "__eq__", "__ne__", "__lt__", "__le__", "__gt__", "__ge__",
    "__hash__", "__len__", "__iter__", "__contains__", "__getitem__",
    "__getattr__", "__int__", "__float__", "__index__", "__str__",
    "__format__", "__bytes__", "__add__", "__radd__", "__mod__", "__rmod__",
    "__call__", "__setattr__",
)


class Unsettled:
    """A value standing in for a result that cannot exist because nothing ran.

    Produced by every mutating effect recorded in dry mode and by every
    post-mutation observe. FORWARDING one into a later ``ctx.effects`` call is
    legal and renders its brand inline; EXTRACTING from it or BRANCHING on it
    truncates the preview with a precise error.
    """

    __slots__ = ("_brand", "_log", "_cmd_path", "_forwardable")

    def __init__(self, brand: str, log: "_EffectLog", cmd_path: str,
                 forwardable: bool) -> None:
        # ``__setattr__`` is poisoned like every other extraction dunder, so the
        # constructor writes its own slots through ``object`` -- otherwise a
        # carrier could not be built at all. Poisoning the write side is what
        # stops ``u._brand = "«forged»"`` from minting a fake preview line and
        # ``u._forwardable = True`` from making a void carrier forwardable,
        # which is the same seal Go gets from unexported fields and TypeScript
        # from the Proxy's `set` trap.
        _set = object.__setattr__
        _set(self, "_brand", brand)
        _set(self, "_log", log)
        _set(self, "_cmd_path", cmd_path)
        # Void results (write/mkdir/remove/rename/chmod) and spawn results have
        # no scalar projection, so they are never forwardable -- in either mode.
        _set(self, "_forwardable", forwardable)

    def __repr__(self) -> str:
        return f"Unsettled({self._brand})"

    def _truncate(self) -> "_DryRunTruncated":
        step = self._log.next_seq()
        return _DryRunTruncated(
            _msg_dry_run_truncated(step, self._cmd_path, self._brand),
            self._log,
            step=step, cmd_path=self._cmd_path, brand=self._brand,
        )


def _make_poisoned_dunder(dunder_name: str):
    def _poisoned(self, *args, **kwargs):
        raise self._truncate()

    _poisoned.__name__ = dunder_name
    _poisoned.__qualname__ = f"Unsettled.{dunder_name}"
    _poisoned.__doc__ = (
        "Poisoned: extracting from or branching on an unsettled value "
        "truncates the dry-run preview."
    )
    return _poisoned


for _dunder in _UNSETTLED_POISONED_DUNDERS:
    setattr(Unsettled, _dunder, _make_poisoned_dunder(_dunder))
del _dunder


@dataclass
class _EffectRecord:
    """One entry in the structured effect log (see the conformance surface)."""

    seq: int
    kind: str
    verb: str
    detail: str
    bytes: int | None = None
    resource: str | None = None
    skip_if_current: str | None = None
    grant: str | None = None
    grant_reason: str | None = None
    recorded: bool = False

    def to_dict(self) -> dict:
        d: dict = {
            "seq": self.seq,
            "kind": self.kind,
            "verb": self.verb,
            "detail": self.detail,
            "recorded": self.recorded,
        }
        if self.bytes is not None:
            d["bytes"] = self.bytes
        if self.resource is not None:
            d["resource"] = self.resource
        if self.skip_if_current is not None:
            d["skip_if_current"] = self.skip_if_current
        if self.grant is not None:
            d["grant"] = self.grant
        return d

    def render(self) -> str:
        """Render this record as a would-do log line (without the indent)."""
        line = f"{self.seq}. {self.verb}: {self.detail}"
        if self.grant is not None:
            line += f" (granted: {self.grant} — {self.grant_reason})"
        if self.skip_if_current is not None:
            line += f" [unless resource '{self.skip_if_current}' already current]"
        return line


_DRY_RUN_HEADER = "DRY RUN — no changes were made. Would do:"


class _EffectLog:
    """The ordered effect records produced by one dispatch.

    TWO counters, deliberately. Would-do numbering is the numbering of the
    RENDERED lines: it feeds the log's ``<N>.`` prefix, the ``«step N output»``
    brand and the truncation error's "ends at step N". CACHE_WRITEs are never
    rendered, so they must never consume one of those numbers -- otherwise a
    coverage-instrumented run would silently start its preview at ``2.``. They
    get their own sequence instead, so every record still carries a ``seq``.
    """

    __slots__ = (
        "records", "_rendered", "_cached", "claimed", "handler_rendered",
        "write_set_line",
    )

    def __init__(self) -> None:
        self.records: list[_EffectRecord] = []
        self._rendered = 0
        self._cached = 0
        # Claimed rendering (contract §19.7). ``claimed`` is set by
        # ``ctx.effects.recorded()``; ``handler_rendered`` by
        # ``ctx.effects.render_log()``. The seam skips its own emission only
        # when the handler both claimed AND rendered -- a claim that never
        # rendered is re-rendered there, so §3.5's guarantee survives the
        # claim intact.
        self.claimed = False
        self.handler_rendered = False
        # An update command's write-set line (contract §27.5), rendered between
        # the header and the first effect and taking NO sequence number: the
        # counter is contiguous over rendered EFFECTS, and a write set is not
        # one. Empty on every command that declares no update, and set only for
        # a dry run.
        self.write_set_line = ""

    def append(self, rec: _EffectRecord) -> None:
        self.records.append(rec)
        if rec.kind == CACHE_WRITE:
            self._cached += 1
        else:
            self._rendered += 1

    def next_seq(self) -> int:
        """The next would-do number. Pure: callers may ask without appending."""
        return self._rendered + 1

    def next_cache_seq(self) -> int:
        """The next CACHE_WRITE number, on its own counter."""
        return self._cached + 1

    def render(self) -> str:
        """Render the would-do log. CACHE_WRITEs are never written to it."""
        lines = [_DRY_RUN_HEADER]
        if self.write_set_line:
            lines.append("  " + self.write_set_line)
        for rec in self.records:
            if rec.kind == CACHE_WRITE:
                continue
            lines.append("  " + rec.render())
        return "\n".join(lines)

    def to_list(self) -> list[dict]:
        return [rec.to_dict() for rec in self.records]

    def seam_suppressed(self) -> bool:
        """True when the handler already produced the log's bytes (§19.7)."""
        return self.claimed and self.handler_rendered


# Message templates that are NOT raised -- printed to stderr, or carried on a
# non-ValueError exception. Every one is a `_msg_*` function returning the
# finished string, which is the shape conformance/check_error_parity.py extracts
# (mirroring the Go `err*`/`prompt*` functions in errors.go and their TypeScript
# twins). A template that is only ever inlined at its use site is invisible to
# the extractor and silently drops out of the cross-language catalog.

def _msg_dry_run_truncated(step: int, cmd: str, brand: str) -> str:
    """The truncation error. Carries its own `error: ` prefix (it goes to
    stderr directly, not through the parse-error formatter)."""
    return (
        f"error: dry-run preview ends at step {step}: {cmd} branched on "
        f"unsettled value {brand} — cannot preview past this point"
    )


def _msg_dry_run_aborted(step: int, cmd: str) -> str:
    """The aborted-preview marker. Same shape and prefix as the truncation
    error above: both say the preview ended before the handler finished, and
    they differ only in why and in what the reader may conclude."""
    return (
        f"error: dry-run preview ends at step {step}: {cmd} aborted — "
        f"the preview above may be incomplete"
    )


def _msg_confirm_prompt(cmd_path: str) -> str:
    """The confirm prompt. A prompt, not an error, but parity is still checked."""
    return f"about to run consequential command '{cmd_path}'. Proceed? [y/N] "


def _strip_confirm_line(answer: str) -> str:
    """Strip the confirm answer's line terminator: one ``\\n``, then one ``\\r``.

    Exactly one of each, never more. The carriage return matters because a human
    at a Windows console types the same ``y`` as everyone else and their terminal
    terminates the line CRLF; a stdin stream that does not translate newlines
    hands us ``"y\\r\\n"``, and declining there would refuse an answer that was
    plainly given. Stripping only the terminator (rather than whitespace) keeps
    ``"  y"`` a decline, which §8.2 requires.
    """
    if answer.endswith("\n"):
        answer = answer[:-1]
    if answer.endswith("\r"):
        answer = answer[:-1]
    return answer


def _msg_confirm_non_interactive() -> str:
    """The non-interactive refusal (contract §8.3).

    It names what is required -- confirmation, at a terminal -- and never the
    token that lifts the requirement. A refusal that prints its own override
    is not a seam: the reflex it teaches is to append the override and re-run,
    which is the opposite of the judgement the declaration asks for.
    """
    return (
        "error: stdin is not interactive; a consequential command must be "
        "confirmed at a terminal"
    )


def _msg_confirm_declined() -> str:
    return "aborted"


def _msg_mutex_decline_clause(name: str) -> str:
    """The teaching clause both mutex-decline errors carry (contract §21.4).

    Appended to the unsatisfied-group error and repeated inside the
    redundant-negation error, so the two teach with one sentence. Spelled as a
    ``_msg_*`` function because it is a message template shared by two raise
    sites, which is how the Go and TypeScript catalogs carry it too.
    """
    return f" (--no-{name} declines an option; it does not choose one)"


class _ConfirmIO:
    """The stdin side of the confirm protocol, isolated so tests can drive it.

    The TypeScript twin is the ``ConfirmIO`` interface in ``confirm.ts`` and the
    Go twin is the ``ConfirmIO`` struct; the two members mean the same thing in
    all three. Swapping it changes WHERE the answer comes from, never WHETHER
    the protocol runs -- there is no bypass here and never will be.
    """

    def is_interactive(self) -> bool:
        """True when stdin is a TTY."""
        return sys.stdin.isatty()

    def read_line(self) -> str:
        """Read one line from stdin, terminator included."""
        return sys.stdin.readline()


_REAL_CONFIRM_IO = _ConfirmIO()


def _msg_call_consequential_unconsented(cmd_path: str) -> str:
    """The programmatic-path refusal (contract §8.5).

    Requiring confirmation is a property of the COMMAND, so every channel has
    to honour it -- but a programmatic caller has no terminal to prompt. The
    refusal makes the caller state, in the call, that it is proceeding without
    a human, instead of the framework deciding that silently on its behalf.
    """
    return f"command '{cmd_path}' is consequential: the call must carry confirmation"


def _consequential_grant_warning(cmd_path: str, grant: str, kind: str) -> str:
    """The `consequential-grant-agreement` warning (contract §8.1, §11).

    A grant exists so a reviewer reading a preview sees WHY a dangerous step is
    there (§6.1) -- the same judgement ``consequential`` makes. When the grant's
    kind is one that leaves this process (``proc_mutate`` runs another program,
    ``net_mutate`` changes remote state), the two declarations should almost
    always agree. They can legitimately disagree, so this is a warning: making
    it an error would push consumers to declare ``consequential`` reflexively
    to clear a gate, which is exactly the reflex the declaration exists to end.
    """
    return (
        f"command '{cmd_path}' declares grant '{grant}' (kind {kind}) but is "
        f"not consequential: a {kind} effect leaves this process and the "
        f"framework cannot walk it back, and the grant already says the step "
        f"is worth explaining. Declare the command consequential, or drop the "
        f"grant if the step is routine."
    )


def _observe_allowlist_breadth_warning(binary: str) -> str:
    """The `observe-allowlist-breadth` warning (contract §6.2).

    A one-token prefix is a near-blanket exemption for that binary: EVERY
    invocation of it becomes an observe, which means it really executes under
    ``--dry-run``, is never written to the would-do log, and is legal inside a
    ``read_only`` command. That may be exactly what the app wants -- the
    allowlist is a declared, source-visible choice and it authorizes real
    execution in dry mode -- so this is a warning, not an error.
    """
    return (
        f"proc_observe_allowlist prefix ['{binary}'] is a single token: EVERY "
        f"'{binary}' invocation becomes an observe, so it really executes under "
        f"--dry-run, is never logged, and is legal in a read_only command. "
        f"Narrow it to the subcommands you actually observe."
    )


def _msg_effects_unavailable() -> str:
    return (
        "ctx.effects is unavailable: this Context was constructed "
        "outside a command dispatch"
    )


def _msg_payload_no_schema(name: str) -> str:
    """Message template: ctx.payload on a command declaring no schema (§19.4).

    Registration cannot see that a handler intends to call ctx.payload, so
    call time is the earliest honest point at which the missing declaration
    can be named.
    """
    return (
        f'command "{name}": ctx.payload requires a declared payload schema'
    )


def _msg_payload_already_set(name: str) -> str:
    """Message template: a second ctx.payload call in one dispatch (§19.4).

    Two payloads are two answers to a question with one slot; picking either
    silently is the kind of guess this regime does not make.
    """
    return (
        f'command "{name}": ctx.payload was already called '
        f"(a dispatch carries at most one payload)"
    )


def _msg_payload_schema_invalid(name: str, path: str, detail: str) -> str:
    """Message template: a declared payload schema is outside the subset (§19.5).

    Registration time. ``path`` names the position inside the declared literal
    (rooted at ``payload_schema``) and ``detail`` names the violated rule. Both
    are byte-identical across the three implementations, pinned by
    ``conformance/payload_schema_vectors.json``.
    """
    return (
        f'command "{name}": payload schema is invalid at {path}: {detail}'
    )


def _msg_payload_invalid(name: str, path: str, detail: str) -> str:
    """Message template: a payload deviates from its declared schema (§19.5).

    Emission time. ``path`` names the position inside the value (rooted at
    ``payload``) and ``detail`` names the violated constraint, so a wrong shape
    fails here instead of shipping.
    """
    return (
        f"command \"{name}\": payload does not satisfy the declared schema "
        f"at {path}: {detail}"
    )


# ---------------------------------------------------------------------------
# The declared payload schema's validator (contract §19.5)
#
# Two duties over one deliberately closed subset:
#
#   * registration-time validation of the declared literal -- an unknown
#     keyword anywhere is a hard error, which is what keeps the subset closed;
#   * emission-time validation of the value a handler supplies through
#     ctx.payload -- a payload that deviates from its declaration fails here
#     rather than shipping a wrong shape.
#
# Every detail string below is byte-identical to the Go and TypeScript
# validators'. They are deliberately NOT named ``_msg_*``: the error-parity
# extractor reads only the two outer templates above, and the details are
# pinned across implementations by the shared vectors instead.
# ---------------------------------------------------------------------------

# The closed subset, in the order the "unknown keyword" message lists it.
_PAYLOAD_SCHEMA_KEYWORDS = (
    "additionalProperties", "const", "enum", "items", "properties",
    "required", "type",
)

# The JSON Schema type names the subset admits, sorted.
_PAYLOAD_JSON_TYPES = (
    "array", "boolean", "integer", "null", "number", "object", "string",
)

# Decision 16's guard. Every IEEE-754 double whose magnitude exceeds 2^53 is
# already an integer (the spacing between representable doubles is at least 1
# from 2^52 upward), so "any integer above 2^53" and "any number above 2^53"
# are the same set -- which is why the guard is a plain magnitude test.
_PAYLOAD_MAX_MAGNITUDE = 2 ** 53

_PDETAIL_NOT_JSON = "the value is not representable in JSON"
_PDETAIL_MAGNITUDE = (
    "the number's magnitude exceeds 2^53 (declare a big identifier as a string)"
)
_PDETAIL_TYPE_SHAPE = '"type" must be a string or an array of strings'
_PDETAIL_TYPE_EMPTY = '"type" must not be an empty array'
_PDETAIL_PROPERTIES_SHAPE = '"properties" must be an object'
_PDETAIL_REQUIRED_SHAPE = '"required" must be an array of strings'
_PDETAIL_ENUM_SHAPE = '"enum" must be a non-empty array'
_PDETAIL_ADDPROPS_SHAPE = (
    '"additionalProperties" must be a boolean or a schema object'
)
_PDETAIL_ENUM_MISMATCH = "the value is not one of the declared enum values"
_PDETAIL_CONST_MISMATCH = "the value does not equal the declared const"


def _payload_quote(s: str) -> str:
    """Quote a string for a message or a path segment.

    §19.5's escaping regime, applied to one string: escape exactly what JSON
    mandates and emit everything else literally. ``json.dumps`` with
    ``ensure_ascii=False`` is precisely that; the Go and TypeScript validators
    hand-roll the same rule.
    """
    return json.dumps(s, ensure_ascii=False)


def _pdetail_unknown_keyword(kw: str) -> str:
    subset = ", ".join(_PAYLOAD_SCHEMA_KEYWORDS)
    return (
        f"unknown keyword {_payload_quote(kw)} "
        f"(the closed subset is: {subset})"
    )


def _pdetail_unknown_type(t: str) -> str:
    types = ", ".join(_PAYLOAD_JSON_TYPES)
    return (
        f"unknown type {_payload_quote(t)} "
        f"(the JSON Schema types are: {types})"
    )


def _pdetail_schema_not_object(got: str) -> str:
    return f"a schema must be an object, got {got}"


def _pdetail_type_duplicate(t: str) -> str:
    return f'"type" has a duplicate entry {_payload_quote(t)}'


def _pdetail_required_duplicate(k: str) -> str:
    return f'"required" has a duplicate entry {_payload_quote(k)}'


def _pdetail_expected_type(declared, got: str) -> str:
    """The type-mismatch detail, in its single and its list form."""
    if isinstance(declared, str):
        return f"expected type {_payload_quote(declared)}, got {got}"
    inner = ", ".join(_payload_quote(t) for t in declared)
    return f"expected type [{inner}], got {got}"


def _pdetail_required_missing(k: str) -> str:
    return f"required property {_payload_quote(k)} is missing"


def _pdetail_not_permitted(k: str) -> str:
    return (
        f"property {_payload_quote(k)} is not permitted "
        f"(additionalProperties is false)"
    )


def _payload_path_key(path: str, key: str) -> str:
    return f"{path}[{_payload_quote(key)}]"


def _payload_path_index(path: str, index: int) -> str:
    return f"{path}[{index}]"


def _payload_kind(value: object) -> str | None:
    """The JSON kind of a value, or None when it is not representable.

    ``integer`` is reported for any number with a zero fractional part, which
    is JSON Schema's own reading of the type and the only one three languages
    can agree on -- TypeScript has no separate integer type at all.
    ``bool`` is a subclass of ``int`` in Python and is deliberately classified
    first, so a boolean is never a number.
    """
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, int):
        return "integer"
    if isinstance(value, float):
        if value != value or value in (float("inf"), float("-inf")):
            return None
        return "integer" if value.is_integer() else "number"
    if isinstance(value, str):
        return "string"
    if isinstance(value, (list, tuple)):
        return "array"
    if isinstance(value, dict):
        for k in value:
            if not isinstance(k, str):
                return None
        return "object"
    return None


def _payload_over_magnitude(value: object) -> bool:
    """True when a number exceeds decision 16's magnitude guard."""
    if isinstance(value, bool):
        return False
    if isinstance(value, int):
        return abs(value) > _PAYLOAD_MAX_MAGNITUDE
    if isinstance(value, float):
        return abs(value) > float(_PAYLOAD_MAX_MAGNITUDE)
    return False


def _payload_scan_value(value: object, path: str):
    """Document check: representability and the magnitude guard, recursively.

    Runs over the WHOLE value before any keyword is consulted, so a payload
    that could not be emitted at all is reported as that rather than as a type
    mismatch. Traversal is deterministic in every implementation: arrays in
    index order, objects in sorted-key order.

    Returns ``(path, detail)`` for the first violation, or None.
    """
    kind = _payload_kind(value)
    if kind is None:
        return (path, _PDETAIL_NOT_JSON)
    if _payload_over_magnitude(value):
        return (path, _PDETAIL_MAGNITUDE)
    if kind == "array":
        for i, item in enumerate(value):
            found = _payload_scan_value(item, _payload_path_index(path, i))
            if found is not None:
                return found
    elif kind == "object":
        for key in sorted(value):
            found = _payload_scan_value(
                value[key], _payload_path_key(path, key)
            )
            if found is not None:
                return found
    return None


def _payload_deep_equal(a: object, b: object) -> bool:
    """JSON-value equality, used by ``enum`` and ``const``.

    Type-aware on purpose: a boolean is never equal to a number (Python's
    ``True == 1`` is exactly the trap this closes), and two numbers are equal
    when their values are, so ``1`` matches a declared ``1.0``.
    """
    ka = _payload_kind(a)
    kb = _payload_kind(b)
    if ka is None or kb is None:
        return False
    if ka in ("integer", "number") and kb in ("integer", "number"):
        return float(a) == float(b)
    if ka != kb:
        return False
    if ka == "null":
        return True
    if ka == "boolean":
        return bool(a) is bool(b)
    if ka == "string":
        return a == b
    if ka == "array":
        if len(a) != len(b):
            return False
        return all(_payload_deep_equal(x, y) for x, y in zip(a, b))
    # object
    if set(a) != set(b):
        return False
    return all(_payload_deep_equal(a[k], b[k]) for k in a)


def _payload_type_matches(declared: str, kind: str) -> bool:
    if declared == "integer":
        return kind == "integer"
    if declared == "number":
        return kind in ("integer", "number")
    return declared == kind


def _validate_payload_schema(schema: object, path: str = "payload_schema"):
    """Registration-time validation of one declared schema literal (§19.5).

    Returns ``(path, detail)`` for the first violation, or None. The keyword
    scan is sorted, so which of several violations is reported never depends on
    a dict's iteration order.
    """
    kind = _payload_kind(schema)
    if kind != "object":
        return (path, _pdetail_schema_not_object(kind or "unsupported"))

    for kw in sorted(schema):
        if kw not in _PAYLOAD_SCHEMA_KEYWORDS:
            return (path, _pdetail_unknown_keyword(kw))

    if "type" in schema:
        t = schema["type"]
        if isinstance(t, str):
            if t not in _PAYLOAD_JSON_TYPES:
                return (path, _pdetail_unknown_type(t))
        elif isinstance(t, list):
            if not t:
                return (path, _PDETAIL_TYPE_EMPTY)
            seen: list[str] = []
            for entry in t:
                if not isinstance(entry, str) or isinstance(entry, bool):
                    return (path, _PDETAIL_TYPE_SHAPE)
                if entry in seen:
                    return (path, _pdetail_type_duplicate(entry))
                seen.append(entry)
            for entry in t:
                if entry not in _PAYLOAD_JSON_TYPES:
                    return (path, _pdetail_unknown_type(entry))
        else:
            return (path, _PDETAIL_TYPE_SHAPE)

    if "required" in schema:
        req = schema["required"]
        if not isinstance(req, list):
            return (path, _PDETAIL_REQUIRED_SHAPE)
        seen_req: list[str] = []
        for entry in req:
            if not isinstance(entry, str) or isinstance(entry, bool):
                return (path, _PDETAIL_REQUIRED_SHAPE)
            if entry in seen_req:
                return (path, _pdetail_required_duplicate(entry))
            seen_req.append(entry)

    if "enum" in schema:
        values = schema["enum"]
        if not isinstance(values, list) or not values:
            return (path, _PDETAIL_ENUM_SHAPE)
        for i, entry in enumerate(values):
            found = _payload_scan_value(
                entry, _payload_path_index(f"{path}.enum", i)
            )
            if found is not None:
                return found

    if "const" in schema:
        found = _payload_scan_value(schema["const"], f"{path}.const")
        if found is not None:
            return found

    if "properties" in schema:
        props = schema["properties"]
        if _payload_kind(props) != "object":
            return (path, _PDETAIL_PROPERTIES_SHAPE)
        for key in sorted(props):
            found = _validate_payload_schema(
                props[key], _payload_path_key(f"{path}.properties", key)
            )
            if found is not None:
                return found

    if "items" in schema:
        found = _validate_payload_schema(schema["items"], f"{path}.items")
        if found is not None:
            return found

    if "additionalProperties" in schema:
        ap = schema["additionalProperties"]
        if not isinstance(ap, bool):
            if _payload_kind(ap) != "object":
                return (path, _PDETAIL_ADDPROPS_SHAPE)
            found = _validate_payload_schema(
                ap, f"{path}.additionalProperties"
            )
            if found is not None:
                return found

    return None


def _validate_payload_instance(value: object, schema: dict, path: str):
    """Emission-time validation of one value against one declared schema.

    Check order is pinned so that a value violating several constraints always
    reports the same one: type, then const, then enum, then (for an object)
    required, declared properties in sorted key order, and finally the
    additional properties in sorted key order; then (for an array) the items.

    Returns ``(path, detail)`` for the first violation, or None.
    """
    kind = _payload_kind(value)
    if kind is None:
        return (path, _PDETAIL_NOT_JSON)

    if "type" in schema:
        declared = schema["type"]
        if isinstance(declared, str):
            if not _payload_type_matches(declared, kind):
                return (path, _pdetail_expected_type(declared, kind))
        else:
            if not any(_payload_type_matches(t, kind) for t in declared):
                return (path, _pdetail_expected_type(list(declared), kind))

    if "const" in schema:
        if not _payload_deep_equal(value, schema["const"]):
            return (path, _PDETAIL_CONST_MISMATCH)

    if "enum" in schema:
        if not any(_payload_deep_equal(value, e) for e in schema["enum"]):
            return (path, _PDETAIL_ENUM_MISMATCH)

    if kind == "object":
        props = schema.get("properties")
        declared_names = set(props) if isinstance(props, dict) else set()
        for key in schema.get("required", ()):
            if key not in value:
                return (path, _pdetail_required_missing(key))
        if isinstance(props, dict):
            for key in sorted(props):
                if key in value:
                    found = _validate_payload_instance(
                        value[key], props[key], _payload_path_key(path, key)
                    )
                    if found is not None:
                        return found
        if "additionalProperties" in schema:
            ap = schema["additionalProperties"]
            if ap is not True:
                for key in sorted(value):
                    if key in declared_names:
                        continue
                    if ap is False:
                        return (path, _pdetail_not_permitted(key))
                    found = _validate_payload_instance(
                        value[key], ap, _payload_path_key(path, key)
                    )
                    if found is not None:
                        return found

    if kind == "array" and "items" in schema:
        for i, item in enumerate(value):
            found = _validate_payload_instance(
                item, schema["items"], _payload_path_index(path, i)
            )
            if found is not None:
                return found

    return None


def _validate_payload_value(value: object, schema: dict):
    """The whole emission-time duty: the document check, then the keywords."""
    found = _payload_scan_value(value, "payload")
    if found is not None:
        return found
    return _validate_payload_instance(value, schema, "payload")


# ---------------------------------------------------------------------------
# Builder sugar for the declared payload schema (contract §19.5, decision 14)
#
# Pure constructors of literals, and nothing more. They add no vocabulary and
# no semantics: each one produces exactly the dict an author could have
# written, that dict is the canonical artifact, and it passes the identical
# registration-time validation. A builder is a convenience for writing the
# canonical artifact, never an alternative to it -- which is why none of them
# validates anything: an unknown type name written through ``schema_type`` is
# rejected at registration exactly as the hand-written literal would be.
#
# The one-to-one mapping onto the closed subset is pinned across the three
# implementations by conformance/payload_schema_builders.json.
# ---------------------------------------------------------------------------


def schema_type(*names: str) -> dict:
    """``{"type": ...}`` -- one name, or a list of them for nullability."""
    if len(names) == 1:
        return {"type": names[0]}
    return {"type": list(names)}


def schema_array(items: dict) -> dict:
    """``{"type": "array", "items": ...}``."""
    return {"type": "array", "items": items}


def schema_object(
    *,
    properties: dict | None = None,
    required: list[str] | None = None,
    additional_properties: bool | dict | None = None,
) -> dict:
    """``{"type": "object", ...}``.

    Each keyword is emitted only when supplied, so ``schema_object()`` is the
    bare ``{"type": "object"}`` and an omitted ``additional_properties`` means
    the keyword is absent rather than ``true`` -- absence and ``true`` are the
    same behaviour but not the same declaration.
    """
    out: dict = {"type": "object"}
    if properties is not None:
        out["properties"] = properties
    if required is not None:
        out["required"] = required
    if additional_properties is not None:
        out["additionalProperties"] = additional_properties
    return out


def schema_enum(*values: object) -> dict:
    """``{"enum": [...]}``."""
    return {"enum": list(values)}


def schema_const(value: object) -> dict:
    """``{"const": ...}``."""
    return {"const": value}


def _msg_effect_argv_not_sequence(name: str, method: str, got: str) -> str:
    return (
        f'command "{name}": effects.{method} argv must be a '
        f"sequence of strings, not {got}"
    )


def _msg_effect_param_not_stringish(name: str, method: str, param: str,
                                    got: str) -> str:
    return (
        f'command "{name}": effects.{method} parameter '
        f"'{param}' must be a string, a path, or a forwarded effect result; "
        f"got {got}"
    )


def _msg_effect_mode_not_int(name: str, got: str) -> str:
    return (
        f'command "{name}": effects.chmod parameter \'mode\' '
        f"must be an int, got {got}"
    )


def _msg_effect_http_method_not_str(name: str, got: str) -> str:
    return (
        f'command "{name}": effects.http parameter \'method\' '
        f"must be a string, got {got}"
    )


def _msg_effect_option_not_accepted(name: str, method: str, opt: str) -> str:
    """An option the receiving method does not accept (contract §12.8).

    Python reaches this through each method's `**_options` catch-all rather than
    through CPython's native `unexpected keyword argument` TypeError, so the
    rendered text is byte-identical to Go's and TypeScript's.  `<opt>` is the
    canonical snake_case option name, which is what makes that identity hold.
    """
    return f'command "{name}": effects.{method} does not accept option \'{opt}\''


def _reject_unaccepted_options(name: str, method: str, options: dict) -> None:
    """Raise on the first unaccepted option, in the caller's declaration order.

    A `TypeError`, matching every other call-time argument guard on the handle
    (and what CPython itself raises for an unexpected keyword).
    """
    for opt in options:
        raise TypeError(_msg_effect_option_not_accepted(name, method, opt))


def _raise_effect_mutating_in_read_only(name: str, method: str):
    raise ValueError(
        f'command "{name}" is classified read_only; effects.{method} is a '
        f"mutating operation"
    )


def _raise_effect_run_not_allowlisted(name: str, argv: str):
    raise ValueError(
        f'command "{name}" is classified read_only; effects.run argv {argv} '
        f"is not on the app's proc_observe_allowlist"
    )


def _raise_effect_grant_undeclared(name: str, grant: str):
    raise ValueError(f'command "{name}": grant \'{grant}\' is not declared on this command')


def _raise_effect_grant_kind_mismatch(name: str, grant: str, k1: str, k2: str):
    raise ValueError(
        f'command "{name}": grant \'{grant}\' is declared for kind {k1} but '
        f"was used for a {k2} effect"
    )


def _raise_effect_grant_on_observe(name: str, grant: str):
    raise ValueError(
        f'command "{name}": grant \'{grant}\' cannot be used on an observe '
        f"(an allowlisted effects.run changes nothing)"
    )


def _raise_effect_run_failed(name: str, method: str, argv: str, code: int):
    raise EffectFailed(f'command "{name}": effects.{method} failed: {argv} exited {code}')


def _raise_effect_http_failed(name: str, http_method: str, url: str, status: int):
    raise EffectFailed(
        f'command "{name}": effects.http failed: {http_method} {url} '
        f"returned {status}"
    )


def _raise_effect_output_not_utf8(name: str, method: str, *, cause=None):
    raise EffectFailed(
        f'command "{name}": effects.{method} produced output that is not valid UTF-8'
    ) from cause


def _raise_effect_param_rejects_carrier(name: str, method: str, param: str):
    raise ValueError(
        f'command "{name}": effects.{method} parameter \'{param}\' does not '
        f"accept an unsettled value"
    )


def _raise_grant_reason_empty(name: str, grant: str):
    raise ValueError(f'command "{name}": grant \'{grant}\' reason must be a non-empty string')


def _raise_grant_duplicate(name: str, grant: str):
    raise ValueError(f'command "{name}": duplicate grant \'{grant}\'')


def _raise_grant_name_invalid(name: str, grant: str):
    raise ValueError(
        f'command "{name}": invalid grant name \'{grant}\': '
        f"must match [a-z][a-z0-9-]*"
    )


def _raise_grant_kind_invalid(name: str, grant: str, kind: object):
    raise ValueError(
        f'command "{name}": grant \'{grant}\' has invalid kind \'{kind}\': '
        f"must be one of proc_mutate, proc_spawn, file_write, net_mutate"
    )


_CARRIER_TYPES = (Unsettled, Completed, Response, Spawned)


# --- the process trace store ----------------------------------------------
#
# The normative specification is docs/process-trace-store.md; the effects
# contract's §20 carries the two contract items (observational-only, and the
# best-effort failure carve-out). Nothing below is ever read back into a
# decision: the framework mints an identifier, appends one line, and composes
# the identifier into the CHILD's environment. There is no accessor.

_TRACE_PARENT_ENV = "STRICTCLI_TRACE_PARENT"

# Crockford base32, the exact alphabet: no I, L, O or U.
_CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
_CROCKFORD_INDEX = {c: i for i, c in enumerate(_CROCKFORD)}
_ULID_LEN = 26

_TRACE_PARTITION_RE = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}\.jsonl$")
_TRACE_ROLL_BYTES = 8 * 1024 * 1024
_TRACE_MARKER_NAME = "write-failure.marker"
_TRACE_FILE_MODE = 0o600
_TRACE_DIR_MODE = 0o700
_MS_PER_HOUR = 3_600_000


def _ulid_encode(ms: int, randomness: bytes) -> str:
    """Encode 48 timestamp bits + 80 random bits as 26 Crockford characters.

    26 characters carry 130 bits, so the 128-bit value is left-padded with two
    zero bits -- which is exactly why a canonical identifier's first character
    never exceeds ``7``.
    """
    value = (ms << 80) | int.from_bytes(randomness, "big")
    return "".join(
        _CROCKFORD[(value >> shift) & 0x1F] for shift in range(125, -1, -5)
    )


def _ulid_mint(ms: int) -> str:
    """Mint an identifier from this writer's clock plus 80 CSPRNG bits."""
    return _ulid_encode(ms, os.urandom(10))


def _ulid_timestamp(text: object) -> int | None:
    """Parse under the strict profile; return the millisecond, or None.

    Rejected, never repaired: any length but 26, any character outside the
    canonical uppercase alphabet (lowercase included -- one identifier must
    have exactly one spelling), and a 130-bit value that overflows 128 bits.
    """
    if not isinstance(text, str) or len(text) != _ULID_LEN:
        return None
    value = 0
    for ch in text:
        index = _CROCKFORD_INDEX.get(ch)
        if index is None:
            return None
        value = (value << 5) | index
    if value >> 128:
        return None
    return value >> 80


def _ulid_valid(text: object) -> bool:
    return _ulid_timestamp(text) is not None


def _trace_store_dir() -> str:
    """The literal store path. ``~`` is expanded and nothing else is consulted.

    Deliberately NOT derived from XDG_DATA_HOME: two conforming writers that
    disagreed about the location would produce two stores on one machine, and
    a chain crossing them would dangle at both ends.
    """
    return os.path.join(
        os.path.expanduser("~"), ".local", "share", "strictcli", "trace"
    )


def _trace_label(ms: int) -> str:
    """The UTC-hour label for an instant: the partition's range start."""
    return time.strftime("%Y-%m-%dT%H", time.gmtime((ms // _MS_PER_HOUR) * 3600))


def _trace_label_start_ms(label: str) -> int:
    """The inverse: a label's range start in epoch milliseconds."""
    return calendar.timegm(time.strptime(label, "%Y-%m-%dT%H")) * 1000


def _trace_timestamp(ms: int) -> str:
    """RFC 3339 in UTC with exactly three fractional digits and a Z suffix."""
    return "%s.%03dZ" % (
        time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime(ms // 1000)),
        ms % 1000,
    )


def _trace_active_label(store: str, now_ms: int) -> str:
    """Select the partition to append to, rolling when both conditions hold.

    The greatest-named file is the active partition; a new one is created with
    O_EXCL when the active file is at least 8 MB AND the current UTC hour is
    later than its label. Losing the creation race is not an error -- the loser
    appends to the winner's file.
    """
    now_label = _trace_label(now_ms)
    names = [n for n in os.listdir(store) if _TRACE_PARTITION_RE.match(n)]
    if not names:
        _trace_create_partition(store, now_label)
        return now_label
    active = max(names)
    label = active[: -len(".jsonl")]
    if now_label > label:
        try:
            size = os.path.getsize(os.path.join(store, active))
        except OSError:
            size = 0
        if size >= _TRACE_ROLL_BYTES:
            _trace_create_partition(store, now_label)
            return now_label
    return label


def _trace_create_partition(store: str, label: str) -> None:
    try:
        os.close(
            os.open(
                os.path.join(store, label + ".jsonl"),
                os.O_WRONLY | os.O_CREAT | os.O_EXCL,
                _TRACE_FILE_MODE,
            )
        )
    except FileExistsError:
        pass  # another writer won the race; append to its file


class _TraceIdentity(NamedTuple):
    """What an entry says about the invocation doing the spawning."""

    app: str
    version: str
    command: str | None
    dry_run: bool
    machine_mode: bool
    quiet: bool
    verbose: bool
    approve_consequential: bool
    effect: str


def _trace_write_entry(identity: _TraceIdentity) -> str | None:
    """Append one entry for a real child-process start; return its identifier.

    Returns None when anything at all went wrong: tracing is best-effort by
    declared design (contract §20.3), so a failure never fails the run, never
    prints, and is never retried. The first failure leaves a write-once marker.
    """
    try:
        store = _trace_store_dir()
        os.makedirs(store, mode=_TRACE_DIR_MODE, exist_ok=True)
        now_ms = int(time.time() * 1000)
        label = _trace_active_label(store, now_ms)
        # The clamp invariant, one-sided: an entry is never older than its
        # file's label. It may be NEWER than the next file's label, because a
        # file that has not reached the roll threshold keeps taking entries
        # after a newer partition exists -- which is why a reader binary-
        # searches to the label and then walks backward on a miss (see the
        # spec page's lookup rule, amended at the 2026-08-13 lookup-rule audit).
        ms = max(now_ms, _trace_label_start_ms(label))
        entry_id = _ulid_mint(ms)
        inherited = os.environ.get(_TRACE_PARENT_ENV)
        entry = {
            "id": entry_id,
            "parent_id": inherited if _ulid_valid(inherited) else None,
            "app": identity.app,
            "version": identity.version,
            "command": identity.command,
            "dry_run": identity.dry_run,
            "machine_mode": identity.machine_mode,
            "quiet": identity.quiet,
            "verbose": identity.verbose,
            "approve_consequential": identity.approve_consequential,
            "effect": identity.effect,
            "pid": os.getpid(),
            "spawned_at": _trace_timestamp(ms),
        }
        line = json.dumps(entry, separators=(",", ":"), ensure_ascii=False) + "\n"
        fd = os.open(
            os.path.join(store, label + ".jsonl"),
            os.O_WRONLY | os.O_APPEND | os.O_CREAT,
            _TRACE_FILE_MODE,
        )
        try:
            os.write(fd, line.encode("utf-8"))
        finally:
            os.close(fd)
        return entry_id
    except Exception:
        _trace_mark_failure()
        return None


def _trace_mark_failure() -> None:
    """Create the write-once failure marker. No counter, no retry, no noise."""
    try:
        fd = os.open(
            os.path.join(_trace_store_dir(), _TRACE_MARKER_NAME),
            os.O_WRONLY | os.O_CREAT | os.O_EXCL,
            _TRACE_FILE_MODE,
        )
        try:
            os.write(
                fd,
                (_trace_timestamp(int(time.time() * 1000)) + "\n").encode("utf-8"),
            )
        finally:
            os.close(fd)
    except Exception:
        pass  # a disk-full condition blinds the marker too; that is accepted


class _Effects:
    """The effects handle reached as ``ctx.effects``.

    Exactly eight methods, and the set is CLOSED: there is no escape hatch that
    mints an unlisted effect, and CACHE_WRITE has no public method at all.
    """

    __slots__ = ("_cmd", "_cmd_path", "_dry_run", "_log", "_allowlist",
                 "_grants", "_mutation_recorded", "_trace", "_out", "_json")

    def __init__(self, *, cmd: "Command", cmd_path: str, dry_run: bool,
                 log: _EffectLog, allowlist: tuple,
                 trace: "_TraceIdentity", out=None, json: bool = False) -> None:
        self._cmd = cmd
        self._cmd_path = cmd_path
        self._dry_run = dry_run
        self._log = log
        self._allowlist = allowlist
        self._grants = {g.name: g for g in cmd.grants}
        self._mutation_recorded = False
        self._trace = trace
        # The human stream render_log() writes to, and the mode that makes it a
        # no-op (contract §19.7).
        self._out = out
        self._json = json

    # -- claimed rendering (contract §19.7) ------------------------------

    def recorded(self) -> list[dict]:
        """Return the records recorded so far in this dispatch (§19.7).

        The shape is §14.2's, the same one §14.3's accessor returns for the
        whole run. Calling this CLAIMS the render: the framework's own
        end-of-dispatch emission is suppressed for the rest of the run, so a
        handler can put the preview where it wants it. A claim that never
        renders is re-rendered at the seam -- claiming moves the render, it can
        never remove it (§3.5).

        In machine mode claiming changes nothing: there is no human stream to
        order and the envelope's ``preview`` is unconditional either way.
        """
        self._log.claimed = True
        return self._log.to_list()

    def render_log(self) -> None:
        """Render the would-do log in §3.2's exact form, here (§19.7).

        Byte-identical to what the framework would have emitted at the end of
        the dispatch -- one renderer, one record list -- so this moves the
        preview in the stream and never changes its content. Calling it also
        claims the render, which is what keeps the log from appearing twice.

        A no-op in machine mode (§19.7) and outside dry mode, in both cases
        for the same reason: those are exactly the runs where the framework's
        own end-of-dispatch emission produces nothing.
        """
        self._log.claimed = True
        if self._json or not self._dry_run:
            return
        self._log.handler_rendered = True
        print(self._log.render(), file=self._out or sys.stdout)

    # -- helpers ---------------------------------------------------------

    def _reject_carrier_params(self, method: str, params: dict) -> None:
        """Hard-error when a carrier reaches a parameter that cannot take one."""
        for param, value in params.items():
            if isinstance(value, _CARRIER_TYPES):
                _raise_effect_param_rejects_carrier(self._cmd_path, method, param)
            if isinstance(value, dict):
                for k, v in value.items():
                    if isinstance(k, _CARRIER_TYPES) or isinstance(v, _CARRIER_TYPES):
                        _raise_effect_param_rejects_carrier(
                            self._cmd_path, method, param,
                        )

    def _operand(self, value: object, method: str, param: str) -> tuple:
        """Resolve a carrier-accepting parameter.

        Returns ``(runtime_value, rendered)``. ``runtime_value`` is ``None``
        when the value is unsettled (nothing ran, so there is nothing to use);
        ``rendered`` is what the log line shows.
        """
        if isinstance(value, Unsettled):
            if not value._forwardable:
                _raise_effect_param_rejects_carrier(self._cmd_path, method, param)
            return None, value._brand
        if isinstance(value, Spawned):
            # A Spawned has no scalar projection.
            _raise_effect_param_rejects_carrier(self._cmd_path, method, param)
        if isinstance(value, Completed):
            return value.stdout, value.stdout
        if isinstance(value, Response):
            text = _decode_effect_output(
                value.body, self._cmd_path, "http",
            )
            return text, text
        if isinstance(value, str):
            return value, value
        if isinstance(value, os.PathLike):
            text = os.fspath(value)
            if isinstance(text, bytes):
                text = text.decode()
            return text, text
        raise TypeError(_msg_effect_param_not_stringish(
            self._cmd_path, method, param, type(value).__name__,
        ))

    def _content_operand(self, value: object) -> tuple:
        """Resolve ``write``'s content. Returns ``(bytes_or_None, rendered)``.

        The rendered form is the encoded byte count for a settled value, and the
        forwarded carrier's brand when the content is unsettled (there is no
        byte count to report -- nothing produced the bytes).
        """
        if isinstance(value, bytes):
            return value, f"{len(value)} bytes"
        if isinstance(value, str):
            data = value.encode("utf-8")
            return data, f"{len(data)} bytes"
        runtime, rendered = self._operand(value, "write", "content")
        if runtime is None:
            return None, rendered
        data = runtime.encode("utf-8")
        return data, f"{len(data)} bytes"

    def _authorize(self, method: str, kind: str, grant: str | None) -> Grant | None:
        """Read-only enforcement plus grant validation, at call time."""
        if self._cmd.effect == EFFECT_READ_ONLY:
            _raise_effect_mutating_in_read_only(self._cmd_path, method)
        return self._check_grant(kind, grant)

    def _check_grant(self, kind: str, grant: str | None) -> Grant | None:
        if grant is None:
            return None
        declared = self._grants.get(grant)
        if declared is None:
            _raise_effect_grant_undeclared(self._cmd_path, grant)
        if declared.kind != kind:
            _raise_effect_grant_kind_mismatch(
                self._cmd_path, grant, declared.kind, kind,
            )
        return declared

    def _record(self, *, kind: str, verb: str, detail: str,
                resource: str | None, skip_if_current: str | None,
                grant: Grant | None, nbytes: int | None = None,
                recorded: bool) -> _EffectRecord:
        rec = _EffectRecord(
            seq=self._log.next_seq(),
            kind=kind,
            verb=verb,
            detail=detail,
            bytes=nbytes,
            resource=resource,
            skip_if_current=skip_if_current,
            grant=grant.name if grant is not None else None,
            grant_reason=grant.reason if grant is not None else None,
            recorded=recorded,
        )
        self._log.append(rec)
        return rec

    def _carrier(self, seq: int, *, forwardable: bool) -> Unsettled:
        self._mutation_recorded = True
        return Unsettled(f"«step {seq} output»", self._log, self._cmd_path,
                         forwardable)

    def _stale(self, descr: str) -> Unsettled:
        return Unsettled(f"«stale: {descr}»", self._log, self._cmd_path, True)

    def _is_observe(self, argv: list) -> bool:
        """Element-wise argv-prefix matching by string equality. Nothing else."""
        for prefix in self._allowlist:
            if len(prefix) > len(argv):
                continue
            if all(
                isinstance(argv[i], str) and argv[i] == prefix[i]
                for i in range(len(prefix))
            ):
                return True
        return False

    def _resolve_argv(self, argv: object, method: str) -> tuple:
        if isinstance(argv, (str, bytes)) or not isinstance(argv, (list, tuple)):
            raise TypeError(_msg_effect_argv_not_sequence(
                self._cmd_path, method, type(argv).__name__,
            ))
        if not argv:
            raise ValueError(
                f'command "{self._cmd_path}": effects.{method} argv must not be empty'
            )
        runtime: list = []
        rendered: list[str] = []
        for i, element in enumerate(argv):
            r, text = self._operand(element, method, f"argv[{i}]")
            runtime.append(r)
            rendered.append(text)
        return runtime, rendered

    # -- the eight methods -------------------------------------------------
    #
    # Every method DECLARES its settled return type and nothing else --
    # `Completed`, `Spawned`, `Response`, `None`. There is no `| Unsettled`
    # union in the surface and no is_unsettled() predicate, because branching
    # on unsettledness IS mode-branching: a declared union would oblige every
    # handler to narrow before touching `.stdout`, which is exactly the silent
    # mode-branch the truncation mechanism exists to prevent. In dry mode the
    # runtime value sitting at these positions is the `Unsettled` carrier,
    # which the static type deliberately does not mention -- a handler that
    # only forwards it never notices, and a handler that extracts from it
    # truncates the preview at runtime, where it is honest. A handler that
    # legitimately needs the mode reads `ctx.dry_run`.

    def run(self, argv: Sequence[str | Completed | Response], *, cwd=None,
            env=None, check=True, stream=False, resource=None,
            skip_if_current=None, grant=None, **_options) -> Completed:
        """Run a subprocess to completion (PROC_MUTATE, or an observe)."""
        _reject_unaccepted_options(self._cmd_path, "run", _options)
        self._reject_carrier_params("run", {
            "cwd": cwd, "env": env, "check": check, "stream": stream,
            "resource": resource,
            "skip_if_current": skip_if_current, "grant": grant,
        })
        runtime, rendered = self._resolve_argv(argv, "run")
        joined = " ".join(rendered)

        if self._is_observe(runtime):
            # An observe changes nothing: it is legal in a read_only command,
            # never written to the would-do log, and never carries a grant.
            if grant is not None:
                _raise_effect_grant_on_observe(self._cmd_path, grant)
            if self._dry_run and self._mutation_recorded:
                return self._stale(joined)
            return self._exec_run(runtime, joined, cwd, env, check, stream, "run")

        if self._cmd.effect == EFFECT_READ_ONLY:
            _raise_effect_run_not_allowlisted(self._cmd_path, joined)
        declared = self._check_grant(PROC_MUTATE, grant)

        if self._dry_run:
            rec = self._record(
                kind=PROC_MUTATE, verb="run", detail=joined, resource=resource,
                skip_if_current=skip_if_current, grant=declared, recorded=True,
            )
            return self._carrier(rec.seq, forwardable=True)

        self._record(
            kind=PROC_MUTATE, verb="run", detail=joined, resource=resource,
            skip_if_current=skip_if_current, grant=declared, recorded=False,
        )
        return self._exec_run(runtime, joined, cwd, env, check, stream, "run")

    def spawn(self, argv: Sequence[str | Completed | Response], *, cwd=None,
              env=None, resource=None, skip_if_current=None,
              grant=None, **_options) -> Spawned:
        """Start a subprocess without waiting (PROC_SPAWN).

        Spawning is itself an effect: a dry run RECORDS the spawn instead of
        performing it, which is why no cross-process mode token exists.
        """
        _reject_unaccepted_options(self._cmd_path, "spawn", _options)
        self._reject_carrier_params("spawn", {
            "cwd": cwd, "env": env, "resource": resource,
            "skip_if_current": skip_if_current, "grant": grant,
        })
        runtime, rendered = self._resolve_argv(argv, "spawn")
        joined = " ".join(rendered)
        declared = self._authorize("spawn", PROC_SPAWN, grant)

        if self._dry_run:
            rec = self._record(
                kind=PROC_SPAWN, verb="spawn", detail=joined, resource=resource,
                skip_if_current=skip_if_current, grant=declared, recorded=True,
            )
            return self._carrier(rec.seq, forwardable=False)

        self._record(
            kind=PROC_SPAWN, verb="spawn", detail=joined, resource=resource,
            skip_if_current=skip_if_current, grant=declared, recorded=False,
        )
        argv_settled = self._settled_argv(runtime, joined, "spawn")
        proc = subprocess.Popen(
            argv_settled, cwd=cwd, env=self._child_env(env),
        )
        return Spawned(pid=proc.pid, _proc=proc, _cmd_path=self._cmd_path)

    def write(self, path: str | os.PathLike[str] | Completed | Response,
              content: str | bytes | Completed | Response, *, resource=None,
              skip_if_current=None, grant=None, **_options) -> None:
        """Write bytes to a path (FILE_WRITE)."""
        _reject_unaccepted_options(self._cmd_path, "write", _options)
        self._reject_carrier_params("write", {
            "resource": resource, "skip_if_current": skip_if_current,
            "grant": grant,
        })
        rt_path, rendered_path = self._operand(path, "write", "path")
        data, rendered_content = self._content_operand(content)
        detail = f"{rendered_path} ({rendered_content})"
        declared = self._authorize("write", FILE_WRITE, grant)
        nbytes = len(data) if data is not None else None

        if self._dry_run:
            rec = self._record(
                kind=FILE_WRITE, verb="write", detail=detail, resource=resource,
                skip_if_current=skip_if_current, grant=declared, nbytes=nbytes,
                recorded=True,
            )
            return self._carrier(rec.seq, forwardable=False)

        self._record(
            kind=FILE_WRITE, verb="write", detail=detail, resource=resource,
            skip_if_current=skip_if_current, grant=declared, nbytes=nbytes,
            recorded=False,
        )
        with open(self._settled(rt_path, "write", "path"), "wb") as fh:
            fh.write(data)
        return None

    def mkdir(self, path: str | os.PathLike[str] | Completed | Response, *,
              resource=None, skip_if_current=None, grant=None,
              **_options) -> None:
        """Create a directory, parents included; an existing one is not an error."""
        _reject_unaccepted_options(self._cmd_path, "mkdir", _options)
        return self._path_effect(
            "mkdir", path, resource, skip_if_current, grant,
            lambda p: os.makedirs(p, exist_ok=True),
        )

    def remove(self, path: str | os.PathLike[str] | Completed | Response, *,
               resource=None, skip_if_current=None, grant=None,
               **_options) -> None:
        """Remove a file, symlink or directory tree; a missing path is not an error."""
        _reject_unaccepted_options(self._cmd_path, "remove", _options)
        return self._path_effect(
            "remove", path, resource, skip_if_current, grant, _remove_path,
        )

    def rename(self, src: str | os.PathLike[str] | Completed | Response,
               dst: str | os.PathLike[str] | Completed | Response, *,
               resource=None, skip_if_current=None, grant=None,
               **_options) -> None:
        """Move/rename a path (FILE_WRITE)."""
        _reject_unaccepted_options(self._cmd_path, "rename", _options)
        self._reject_carrier_params("rename", {
            "resource": resource, "skip_if_current": skip_if_current,
            "grant": grant,
        })
        rt_src, r_src = self._operand(src, "rename", "src")
        rt_dst, r_dst = self._operand(dst, "rename", "dst")
        detail = f"{r_src} -> {r_dst}"
        declared = self._authorize("rename", FILE_WRITE, grant)

        if self._dry_run:
            rec = self._record(
                kind=FILE_WRITE, verb="rename", detail=detail, resource=resource,
                skip_if_current=skip_if_current, grant=declared, recorded=True,
            )
            return self._carrier(rec.seq, forwardable=False)

        self._record(
            kind=FILE_WRITE, verb="rename", detail=detail, resource=resource,
            skip_if_current=skip_if_current, grant=declared, recorded=False,
        )
        os.replace(
            self._settled(rt_src, "rename", "src"),
            self._settled(rt_dst, "rename", "dst"),
        )
        return None

    def chmod(self, path: str | os.PathLike[str] | Completed | Response, mode,
              *, resource=None, skip_if_current=None, grant=None,
              **_options) -> None:
        """Change a path's mode (FILE_WRITE)."""
        _reject_unaccepted_options(self._cmd_path, "chmod", _options)
        self._reject_carrier_params("chmod", {
            "mode": mode, "resource": resource,
            "skip_if_current": skip_if_current, "grant": grant,
        })
        if not isinstance(mode, int) or isinstance(mode, bool):
            raise TypeError(_msg_effect_mode_not_int(
                self._cmd_path, type(mode).__name__,
            ))
        rt_path, r_path = self._operand(path, "chmod", "path")
        detail = f"{r_path} 0{mode:o}"
        declared = self._authorize("chmod", FILE_WRITE, grant)

        if self._dry_run:
            rec = self._record(
                kind=FILE_WRITE, verb="chmod", detail=detail, resource=resource,
                skip_if_current=skip_if_current, grant=declared, recorded=True,
            )
            return self._carrier(rec.seq, forwardable=False)

        self._record(
            kind=FILE_WRITE, verb="chmod", detail=detail, resource=resource,
            skip_if_current=skip_if_current, grant=declared, recorded=False,
        )
        os.chmod(self._settled(rt_path, "chmod", "path"), mode)
        return None

    def http(self, method, url: str | os.PathLike[str] | Completed | Response,
             *, body=None, headers=None, check=True, resource=None,
             skip_if_current=None, grant=None, **_options) -> Response:
        """Perform a network request (NET_MUTATE)."""
        _reject_unaccepted_options(self._cmd_path, "http", _options)
        self._reject_carrier_params("http", {
            "method": method, "body": body, "headers": headers,
            "check": check, "resource": resource,
            "skip_if_current": skip_if_current, "grant": grant,
        })
        if not isinstance(method, str):
            raise TypeError(_msg_effect_http_method_not_str(
                self._cmd_path, type(method).__name__,
            ))
        rt_url, r_url = self._operand(url, "http", "url")
        detail = f"{method} {r_url}"
        declared = self._authorize("http", NET_MUTATE, grant)

        if self._dry_run:
            rec = self._record(
                kind=NET_MUTATE, verb="net", detail=detail, resource=resource,
                skip_if_current=skip_if_current, grant=declared, recorded=True,
            )
            return self._carrier(rec.seq, forwardable=True)

        self._record(
            kind=NET_MUTATE, verb="net", detail=detail, resource=resource,
            skip_if_current=skip_if_current, grant=declared, recorded=False,
        )
        return self._exec_http(
            method, self._settled(rt_url, "http", "url"), body, headers, check,
        )

    # -- shared execution paths ------------------------------------------

    def _path_effect(self, verb, path, resource, skip_if_current, grant, perform):
        self._reject_carrier_params(verb, {
            "resource": resource, "skip_if_current": skip_if_current,
            "grant": grant,
        })
        rt_path, r_path = self._operand(path, verb, "path")
        declared = self._authorize(verb, FILE_WRITE, grant)

        if self._dry_run:
            rec = self._record(
                kind=FILE_WRITE, verb=verb, detail=r_path, resource=resource,
                skip_if_current=skip_if_current, grant=declared, recorded=True,
            )
            return self._carrier(rec.seq, forwardable=False)

        self._record(
            kind=FILE_WRITE, verb=verb, detail=r_path, resource=resource,
            skip_if_current=skip_if_current, grant=declared, recorded=False,
        )
        perform(self._settled(rt_path, verb, "path"))
        return None

    def _settled(self, value, method, param):
        if value is None:
            # Unreachable: an unsettled operand only survives in dry mode, where
            # nothing executes. Kept as a fail-closed backstop.
            _raise_effect_param_rejects_carrier(self._cmd_path, method, param)
        return value

    def _settled_argv(self, runtime, joined, method):
        for i, element in enumerate(runtime):
            if element is None:
                _raise_effect_param_rejects_carrier(
                    self._cmd_path, method, f"argv[{i}]",
                )
        return list(runtime)

    def _merged_env(self, env):
        """``env`` merges OVER the inherited environment, never replacing it."""
        if env is None:
            return None
        merged = dict(os.environ)
        merged.update({str(k): str(v) for k, v in env.items()})
        return merged

    def _child_env(self, env):
        """Compose the child's environment at a real child-process start.

        The handler's ``env`` merge happens first; the framework's ancestry
        composition is applied AFTER it and wins (contract §2.5), so a handler
        can neither sever the chain by clearing the variable nor forge a
        different ancestor by setting it. When the entry could not be written
        the variable is REMOVED rather than left inherited: a lost record must
        not silently re-attribute the child to its grandparent.
        """
        merged = self._merged_env(env)
        if merged is None:
            merged = dict(os.environ)
        entry_id = _trace_write_entry(self._trace)
        if entry_id is None:
            merged.pop(_TRACE_PARENT_ENV, None)
        else:
            merged[_TRACE_PARENT_ENV] = entry_id
        return merged

    def _exec_run(self, runtime, joined, cwd, env, check, stream, method):
        argv = self._settled_argv(runtime, joined, method)
        proc = subprocess.run(
            argv, cwd=cwd, env=self._child_env(env),
            capture_output=not stream,
        )
        if stream:
            out = err = ""
        else:
            out = _decode_effect_output(proc.stdout, self._cmd_path, method)
            err = _decode_effect_output(proc.stderr, self._cmd_path, method)
        if check and proc.returncode != 0:
            _raise_effect_run_failed(
                self._cmd_path, method, joined, proc.returncode,
            )
        return Completed(exit_code=proc.returncode, stdout=out, stderr=err)

    def _exec_http(self, method, url, body, headers, check):
        import urllib.error
        import urllib.request

        req = urllib.request.Request(url, data=body, method=method)
        for key, value in (headers or {}).items():
            req.add_header(str(key), str(value))
        try:
            with urllib.request.urlopen(req) as resp:
                status = resp.status
                payload = resp.read()
                hdrs = {k.lower(): v for k, v in resp.headers.items()}
        except urllib.error.HTTPError as e:
            status = e.code
            payload = e.read()
            hdrs = {k.lower(): v for k, v in e.headers.items()}
        if check and not (200 <= status <= 299):
            _raise_effect_http_failed(self._cmd_path, method, url, status)
        return Response(status=status, body=payload, headers=hdrs)


def _decode_effect_output(data: bytes, cmd_path: str, method: str) -> str:
    """Decode captured output as UTF-8 strictly, dropping one trailing newline."""
    if data is None:
        return ""
    try:
        text = data.decode("utf-8")
    except UnicodeDecodeError as e:
        _raise_effect_output_not_utf8(cmd_path, method, cause=e)
    if text.endswith("\n"):
        text = text[:-1]
    return text


def _remove_path(path: str) -> None:
    """Remove a file, a symlink or a directory tree. A missing path is fine."""
    import shutil

    if os.path.islink(path) or os.path.isfile(path):
        os.unlink(path)
    elif os.path.isdir(path):
        shutil.rmtree(path)


def _validate_grants(cmd_name: str, grants) -> tuple:
    """Validate a command's grant declarations at registration time."""
    resolved: list[Grant] = []
    seen: set[str] = set()
    for g in grants or ():
        if not isinstance(g, Grant):
            raise ValueError(
                f'command "{cmd_name}": grants must be Grant instances, '
                f"got {type(g).__name__}"
            )
        if not isinstance(g.name, str) or not _GRANT_NAME_RE.fullmatch(g.name):
            _raise_grant_name_invalid(cmd_name, g.name)
        if g.name in seen:
            _raise_grant_duplicate(cmd_name, g.name)
        if not isinstance(g.reason, str) or not g.reason.strip():
            _raise_grant_reason_empty(cmd_name, g.name)
        if g.kind not in _GRANTABLE_KINDS:
            _raise_grant_kind_invalid(cmd_name, g.name, g.kind)
        seen.add(g.name)
        resolved.append(g)
    return tuple(resolved)


# ---------------------------------------------------------------------------
# The effects-bypass lint (the `effects-bypass` check provider, see below)
#
# Closed lists, matched on the called attribute/function name: process starts,
# filesystem mutations, and network calls. The analyser is stdlib `ast` -- a
# regular dependency, no optional import and no soft degradation.
#
# Several leaves are RECEIVER-SCOPED: a name alone is not evidence of an
# effect, and a finding a consumer cannot act on is worse than no finding.
# `mapping.get(...)` is not a network call and `platform.system()` is not a
# process start, so those leaves are banned only through a receiver the
# module's own imports let the analyser resolve.
# ---------------------------------------------------------------------------

_BYPASS_PROCESS = frozenset({
    "run", "Popen", "call", "check_call", "check_output", "getoutput",
    "getstatusoutput", "popen", "execv", "execvp", "execve",
    "spawnv", "spawnl", "fork",
})
# Process leaves that start a process only through `os`. `system` is the whole
# set: `os.system` runs a shell, but `platform.system()` is a pure in-process
# string read -- no process, no effect, and nothing the effects handle could
# carry, since its closed method set has no in-process-observe method. Banning
# the leaf on any receiver produced a finding whose own remediation ("route it
# through ctx.effects") could not be followed, which is the one thing a lint
# must never emit. An UNKNOWN receiver (`foo.system()`) is exempt for the same
# reason the network leaves are receiver-scoped: without a resolvable binding
# to `os` there is no evidence a process starts, and a name is not evidence.
_BYPASS_PROCESS_OS_ONLY = frozenset({"system"})
_BYPASS_OS_RECEIVERS = frozenset({"os"})
_BYPASS_FILESYSTEM = frozenset({
    "remove", "unlink", "rmdir", "removedirs", "mkdir", "makedirs",
    "rename", "renames", "replace", "chmod", "chown", "symlink", "link",
    "truncate", "rmtree", "move", "copy", "copy2", "copyfile", "copytree",
    "write_text", "write_bytes", "touch", "symlink_to", "hardlink_to",
    "mkstemp", "mkdtemp",
})
_BYPASS_NETWORK = frozenset({
    "urlopen", "urlretrieve", "request", "get", "post", "put", "patch",
    "delete", "head", "HTTPConnection", "HTTPSConnection", "socket",
    "create_connection",
})
# Network members are banned only when reached through one of these receivers,
# so an ordinary `mapping.get(...)` inside a handler is not a finding.
_BYPASS_NETWORK_RECEIVERS = frozenset({
    "requests", "httpx", "urllib", "request", "http", "client", "socket",
    "aiohttp", "urllib3", "session", "Session",
})
_BYPASS_SKIP_DIRS = frozenset({
    ".git", ".venv", "venv", "env", "node_modules", "__pycache__",
    "site-packages", "build", "dist", ".tox", ".mypy_cache", ".ruff_cache",
    ".pytest_cache", ".eggs",
})
# The modules whose imports bind a receiver the analyser trusts. Closed, like
# every other list here: `import requests as rq` must resolve to `requests` and
# `from os import system` to `os`, while `from mylib import get` must bind
# NOTHING -- widening this to every module would re-create exactly the ordinary
# `mapping.get(...)` noise the network receiver list exists to remove.
_BYPASS_EFFECT_MODULES = frozenset({
    "os", "os.path", "subprocess", "shutil", "pathlib", "socket", "tempfile",
    "requests", "httpx", "urllib", "urllib.request", "http", "http.client",
    "aiohttp", "urllib3",
})


class _BypassImports(NamedTuple):
    """What a module's imports say about the names it calls.

    ``receivers`` maps a bound module name to the effect module it denotes
    (``import os as o`` -> ``{"o": "os"}``); ``calls`` maps a bound member name
    to the ``(module, member)`` pair it came from (``from os import system as
    sh`` -> ``{"sh": ("os", "system")}``). Both are used to normalize a call
    before the ban lists see it, so the lists stay written in terms of real
    module and member names rather than whatever the consumer spelled.
    """

    receivers: dict
    calls: dict


_BYPASS_NO_IMPORTS = _BypassImports({}, {})


def _bypass_import_bindings(tree) -> _BypassImports:
    """Names bound to effect modules and to their members, in one module.

    Relative imports are skipped: ``from .os import system`` is the consumer's
    own module, not the stdlib one, and the analyser cannot resolve it.
    """
    receivers: dict = {}
    calls: dict = {}
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            for alias in node.names:
                if alias.name not in _BYPASS_EFFECT_MODULES:
                    continue
                if alias.asname is None:
                    # `import os.path` binds `os`, and `os.system` still works.
                    bound = module = alias.name.split(".")[0]
                else:
                    bound = alias.asname
                    module = alias.name.rsplit(".", 1)[-1]
                receivers[bound] = module
        elif isinstance(node, ast.ImportFrom):
            if node.level or node.module not in _BYPASS_EFFECT_MODULES:
                continue
            module = node.module.rsplit(".", 1)[-1]
            for alias in node.names:
                calls[alias.asname or alias.name] = (module, alias.name)
    return _BypassImports(receivers, calls)


def _call_target_name(node) -> tuple:
    """Return ``(dotted_target, receiver)`` for a call's callee."""
    if isinstance(node, ast.Name):
        return node.id, None
    if isinstance(node, ast.Attribute):
        parts: list[str] = [node.attr]
        cur = node.value
        while isinstance(cur, ast.Attribute):
            parts.append(cur.attr)
            cur = cur.value
        if isinstance(cur, ast.Name):
            parts.append(cur.id)
        parts.reverse()
        receiver = parts[-2] if len(parts) >= 2 else None
        return ".".join(parts), receiver
    return None, None


def _reaches_effects_handle(node, aliases=frozenset()) -> bool:
    """True when a callee's receiver chain goes through ``.effects``.

    ``aliases`` are local names bound to the handle (``e = ctx.effects``), which
    is an ordinary way to write a handler and must not read as a bypass.
    """
    cur = node
    while isinstance(cur, ast.Attribute):
        if cur.attr == "effects":
            return True
        cur = cur.value
    return isinstance(cur, ast.Name) and cur.id in aliases


def _bypass_effects_aliases(tree) -> frozenset:
    """Names bound to the effects handle anywhere in the module.

    ``e = ctx.effects`` then ``e.write(...)`` is the same call as
    ``ctx.effects.write(...)``; without this the lint would report the handle
    itself as a bypass.
    """
    names: set = set()
    for node in ast.walk(tree):
        targets = []
        if isinstance(node, ast.Assign):
            targets = node.targets
        elif isinstance(node, ast.AnnAssign) and node.value is not None:
            targets = [node.target]
        else:
            continue
        if node.value is None or not _reaches_effects_handle(node.value):
            continue
        for target in targets:
            if isinstance(target, ast.Name):
                names.add(target.id)
    return frozenset(names)


def _function_opts_into_effects(fn) -> bool:
    """True when a function body reaches for an ``.effects`` handle at all.

    One of the two root conditions: a function that uses the effects handle must
    route ALL of its effects through it, or the preview it promises is a lie.
    """
    for node in ast.walk(fn):
        if isinstance(node, ast.Attribute) and node.attr == "effects":
            return True
    return False


# Decorator leaf names that register a command handler. `@app.command(...)` and
# `@group.command(...)` are the whole registration surface; `passthrough` is
# listed because a consumer may spell a passthrough wrapper the same way.
_BYPASS_HANDLER_DECORATORS = frozenset({"command", "passthrough"})


def _decorator_leaf(node) -> str | None:
    """The last dotted component of a decorator expression, call or not."""
    if isinstance(node, ast.Call):
        node = node.func
    if isinstance(node, ast.Attribute):
        return node.attr
    if isinstance(node, ast.Name):
        return node.id
    return None


def _bypass_handler_names(tree) -> set:
    """Function names passed as ``handler=`` anywhere in the module.

    The second way a handler is registered: `Passthrough(handler=_pt)`,
    `app.command(..., handler=deploy)`. Name-based, because that is all a
    single-module AST can honestly resolve.
    """
    names: set = set()
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        for kw in node.keywords:
            if kw.arg == "handler" and isinstance(kw.value, ast.Name):
                names.add(kw.value.id)
    return names


def _is_registered_handler(fn, handler_names: set) -> bool:
    """True when this function is a registered command handler."""
    for dec in fn.decorator_list:
        if _decorator_leaf(dec) in _BYPASS_HANDLER_DECORATORS:
            return True
    return fn.name in handler_names


def _bypass_direct_call_names(fn) -> set:
    """Bare ``name(...)`` callees inside a function's subtree."""
    names: set = set()
    for node in ast.walk(fn):
        if isinstance(node, ast.Call) and isinstance(node.func, ast.Name):
            names.add(node.func.id)
    return names


def _bypass_reachable_functions(tree) -> set:
    """The ids of every function REACHABLE FROM A REGISTERED COMMAND HANDLER.

    §11's scope is reachability, not "a function whose own body mentions
    ``.effects``" -- a handler that never touches the handle, and a bypass one
    helper-call away, are both trivial escapes from the narrower reading, and
    this lint is the sole stated mitigation for the accepted no-sandbox ceiling.

    Roots are registered handlers (a `.command` / `.passthrough` decorator, or a
    name passed as `handler=`) plus, as before, any function that reaches for
    `.effects` itself. From each root the closure follows DIRECT calls to
    MODULE-LEVEL functions, transitively, within this one module -- the most a
    single-file AST can resolve without a symbol table.
    """
    module_funcs: dict = {}
    for node in tree.body:
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            module_funcs.setdefault(node.name, node)

    handler_names = _bypass_handler_names(tree)
    queue = [
        fn for fn in ast.walk(tree)
        if isinstance(fn, (ast.FunctionDef, ast.AsyncFunctionDef))
        and (_is_registered_handler(fn, handler_names)
             or _function_opts_into_effects(fn))
    ]
    reachable: set = set()
    while queue:
        fn = queue.pop()
        if id(fn) in reachable:
            continue
        reachable.add(id(fn))
        for name in _bypass_direct_call_names(fn):
            target = module_funcs.get(name)
            if target is not None and id(target) not in reachable:
                queue.append(target)
    return reachable


def _bypass_resolve_call(leaf: str, receiver,
                         imports: _BypassImports) -> tuple:
    """The ``(leaf, receiver)`` a call really goes through, per its imports.

    A bare name imported from an effect module answers with that module and the
    member's REAL name (``from os import system as sh`` -> ``("system",
    "os")``), and an aliased module receiver answers with the module it denotes
    (``import os as o`` -> ``os``). Anything else is returned untouched, so an
    unresolvable receiver stays unresolvable rather than being guessed at.
    """
    if receiver is None:
        bound = imports.calls.get(leaf)
        if bound is None:
            return leaf, None
        module, member = bound
        return member, module
    return leaf, imports.receivers.get(receiver, receiver)


def _bypass_call_is_banned(node, target: str, receiver,
                           imports: _BypassImports = _BYPASS_NO_IMPORTS) -> bool:
    """True when one call is a direct effect the handle should have carried.

    Two leaves are deliberately narrower than the rest: builtin ``open`` is a
    finding only in a writing mode, and ``system`` only through ``os`` --
    ``platform.system()`` observes this process and starts nothing, and the
    effects handle has no method that could carry it.

    Builtin ``open`` is answered BEFORE import resolution, because it is the
    one leaf whose meaning comes from being unqualified. Everything after it is
    resolved through the module's imports (see :func:`_bypass_resolve_call`),
    so the lists below are written in terms of real module and member names.
    """
    leaf = target.rsplit(".", 1)[-1]
    if leaf == "open" and receiver is None:
        return _open_is_write_mode(node)
    leaf, receiver = _bypass_resolve_call(leaf, receiver, imports)
    if leaf in _BYPASS_PROCESS_OS_ONLY:
        return receiver in _BYPASS_OS_RECEIVERS
    return (
        (leaf in _BYPASS_PROCESS and receiver is not None)
        or leaf in _BYPASS_FILESYSTEM
        or (leaf in _BYPASS_NETWORK and receiver in _BYPASS_NETWORK_RECEIVERS)
    )


def _bypass_walk(node, stack: list, reachable: set, findings: list, rel: str,
                 aliases: frozenset, imports: _BypassImports) -> None:
    """Walk one subtree, carrying the enclosing-function stack.

    A banned call is reported once, at the INNERMOST enclosing function, when
    any enclosing function is reachable -- so a bypass inside a nested closure
    is one finding, not one per enclosing scope.
    """
    if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
        stack = stack + [node]
    elif isinstance(node, ast.Call):
        if stack and any(id(fn) in reachable for fn in stack):
            if not _reaches_effects_handle(node.func, aliases):
                target, receiver = _call_target_name(node.func)
                if target is not None and _bypass_call_is_banned(
                        node, target, receiver, imports):
                    findings.append((rel, node.lineno, stack[-1].name, target))
    for child in ast.iter_child_nodes(node):
        _bypass_walk(child, stack, reachable, findings, rel, aliases, imports)


def _open_is_write_mode(node) -> bool:
    """True when a bare ``open(...)`` call requests a writing mode."""
    mode = None
    if len(node.args) >= 2 and isinstance(node.args[1], ast.Constant):
        mode = node.args[1].value
    for kw in node.keywords:
        if kw.arg == "mode" and isinstance(kw.value, ast.Constant):
            mode = kw.value.value
    if not isinstance(mode, str):
        return False
    return any(ch in mode for ch in ("w", "a", "x", "+"))


def _scan_effects_bypasses(root: Path) -> list[tuple]:
    """Find direct effect calls REACHABLE FROM a registered command handler.

    Returns ``(relative_path, lineno, function_name, target)`` tuples, in file
    then line order. See :func:`_bypass_reachable_functions` for the scope rule.
    """
    findings: list[tuple] = []
    if not root.is_dir():
        return findings
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(
            d for d in dirnames
            if d not in _BYPASS_SKIP_DIRS and not d.startswith(".")
        )
        for fname in sorted(filenames):
            if not fname.endswith(".py"):
                continue
            path = os.path.join(dirpath, fname)
            try:
                tree = ast.parse(Path(path).read_text(encoding="utf-8"), filename=path)
            except (OSError, SyntaxError, UnicodeDecodeError, ValueError):
                # A file the analyser cannot read is not evidence of a bypass.
                continue
            rel = os.path.relpath(path, root)
            reachable = _bypass_reachable_functions(tree)
            if not reachable:
                continue
            _bypass_walk(tree, [], reachable, findings, rel,
                         _bypass_effects_aliases(tree),
                         _bypass_import_bindings(tree))
    return findings



# Module-private brand token: an Outcome can be constructed only through the
# ``outcome()`` factory (which holds this token). Direct construction raises,
# so a return value is an Outcome only when the framework minted it -- no
# structural shape detection.
_OUTCOME_TOKEN = object()


@dataclass(frozen=True)
class Outcome:
    """A structured result returned by a command handler.

    Built exclusively via the :func:`outcome` factory. Carries an exit code and
    nothing else: the bare-JSON-print data channel was deleted (contract
    §19.4) and machine payloads are supplied through ``ctx.payload``.
    """

    exit_code: int
    _token: object = None

    def __post_init__(self) -> None:
        if self._token is not _OUTCOME_TOKEN:
            raise TypeError(
                "Outcome cannot be constructed directly; "
                "build one with strictcli.outcome(...)"
            )


def outcome(exit_code: int = 0) -> Outcome:
    """Build an :class:`Outcome` for a command handler to return.

    Args:
        exit_code: process exit code (default 0).
    """
    return Outcome(exit_code=exit_code, _token=_OUTCOME_TOKEN)


def _interpret_handler_return(result: object) -> int:
    """Map a command handler's return value to an exit code.

    The only permitted returns are ``int`` (exit code), ``None`` (exit 0),
    or an :class:`Outcome` built via :func:`outcome`. Anything else is a
    hard error.
    """
    if result is None:
        return 0
    if isinstance(result, Outcome):
        return result.exit_code
    if isinstance(result, int):
        return result
    raise TypeError(
        "command handler must return int (exit code), None (exit 0), or "
        f"strictcli.outcome(...); got {type(result).__name__}"
    )


def _config_path(app_name: str, *, override: str | None = None, config_format: str = "json") -> str:
    """Compute the config file path for an app.

    If override is provided, expand ~ and return it directly.
    Otherwise compute from XDG_CONFIG_HOME + app_name.
    """
    if override is not None:
        return os.path.expanduser(override)
    config_home = os.environ.get("XDG_CONFIG_HOME", os.path.expanduser("~/.config"))
    ext = "toml" if config_format == "toml" else "json"
    return os.path.join(config_home, app_name, f"config.{ext}")


import re as _re


# Regex to extract position from tomllib error messages:
# "... (at line X, column Y)" pattern (Python 3.11-3.13)
_TOML_POSITION_RE = _re.compile(r"\(at line (\d+), column (\d+)\)")


class _ConfigLoadResult:
    """Result of loading a config file."""
    __slots__ = ("data", "parse_err")

    def __init__(self, data: dict | None = None, parse_err: str | None = None):
        self.data = data if data is not None else {}
        self.parse_err = parse_err


def _compute_json_position(text: str, offset: int) -> tuple[int, int]:
    """Convert a byte offset to 1-based (line, column)."""
    line = 1
    col = 1
    for i in range(min(offset, len(text))):
        if text[i] == "\n":
            line += 1
            col = 1
        else:
            col += 1
    return line, col


def _load_config(
    app_name: str,
    *,
    config_path_override: str | None = None,
    config_format: str = "json",
    is_runtime_flag: bool = False,
) -> _ConfigLoadResult:
    """Load the config file for an app.

    Missing file with is_runtime_flag=True is a hard error (user explicitly
    passed --config). Missing file otherwise is soft (returns empty dict).
    Malformed file is always a hard error with position information.
    """
    path = _config_path(app_name, override=config_path_override, config_format=config_format)
    if not os.path.isfile(path):
        if is_runtime_flag:
            return _ConfigLoadResult(parse_err=f"config file not found: {path}")
        return _ConfigLoadResult()
    if config_format == "toml":
        try:
            with open(path, "rb") as f:
                return _ConfigLoadResult(data=tomllib.load(f))
        except (tomllib.TOMLDecodeError, UnicodeDecodeError) as e:
            msg = str(e)
            m = _TOML_POSITION_RE.search(msg)
            if m:
                line, col = int(m.group(1)), int(m.group(2))
                return _ConfigLoadResult(
                    parse_err=f"config file {path}: {msg} (line {line}, column {col})",
                )
            return _ConfigLoadResult(parse_err=f"config file {path}: {msg}")
    try:
        with open(path) as f:
            text = f.read()
            return _ConfigLoadResult(data=json.loads(text))
    except json.JSONDecodeError as e:
        line, col = _compute_json_position(text, e.pos) if e.pos is not None else (0, 0)
        if line > 0:
            return _ConfigLoadResult(
                parse_err=f"config file {path}: {e.msg} (line {line}, column {col})",
            )
        return _ConfigLoadResult(parse_err=f"config file {path}: {e.msg}")
    except ValueError as e:
        return _ConfigLoadResult(parse_err=f"config file {path}: {e}")


def _toml_format_scalar(value: object) -> str:
    """Format a scalar value as a TOML literal."""
    if isinstance(value, bool):
        return str(value).lower()
    if isinstance(value, str):
        escaped = value.replace("\\", "\\\\").replace('"', '\\"')
        return f'"{escaped}"'
    if isinstance(value, float):
        return _format_float_canonical(value)
    if isinstance(value, int):
        return str(value)
    escaped = str(value).replace("\\", "\\\\").replace('"', '\\"')
    return f'"{escaped}"'


def _load_toml_doc(path: str) -> "tomlkit.TOMLDocument":
    """Load a TOML file as a comment/order-preserving tomlkit document.

    Returns an empty document if the file does not exist yet.
    """
    if os.path.exists(path):
        with open(path, encoding="utf-8") as fh:
            return tomlkit.parse(fh.read())
    return tomlkit.document()


def _toml_value_item(value: object) -> object:
    """Build a tomlkit item for ``value`` using canonical scalar formatting.

    Scalars and lists are rendered through ``_toml_format_scalar`` (so floats
    keep their canonical spelling) and re-parsed into properly formatted
    tomlkit items. Dicts become section tables with keys sorted for
    deterministic output.
    """
    if isinstance(value, dict):
        tbl = tomlkit.table()
        for k in sorted(value):
            tbl[k] = _toml_value_item(value[k])
        return tbl
    if isinstance(value, list):
        rendered = "[" + ", ".join(_toml_format_scalar(e) for e in value) + "]"
        return tomlkit.parse(f"_ = {rendered}")["_"]
    rendered = _toml_format_scalar(value)
    return tomlkit.parse(f"_ = {rendered}")["_"]


def _toml_set_nested(doc: "tomlkit.TOMLDocument", dotted_key: str, value: object) -> None:
    """Set a dot-separated key on a tomlkit document, preserving comments/order.

    Only the target key is (re)written; intermediate tables are created as
    needed. Comments and ordering of all other keys are untouched.
    """
    parts = dotted_key.split(".")
    current: object = doc
    for part in parts[:-1]:
        nxt = current.get(part) if hasattr(current, "get") else None
        if not isinstance(nxt, (Table, InlineTable)):
            tbl = tomlkit.table()
            current[part] = tbl
            current = current[part]
        else:
            current = nxt
    current[parts[-1]] = _toml_value_item(value)


def _toml_del_nested(doc: "tomlkit.TOMLDocument", dotted_key: str) -> bool:
    """Delete a dot-separated key from a tomlkit document.

    Returns True if the key was found and removed. Prunes now-empty
    intermediate tables. Comments/order of untouched keys are preserved.
    """
    parts = dotted_key.split(".")
    parents: list[tuple[object, str]] = []
    current: object = doc
    for part in parts[:-1]:
        nxt = current.get(part) if hasattr(current, "get") else None
        if not isinstance(nxt, (Table, InlineTable)):
            return False
        parents.append((current, part))
        current = nxt
    if parts[-1] not in current:
        return False
    del current[parts[-1]]
    for parent, key in reversed(parents):
        if len(parent[key]) == 0:
            del parent[key]
    return True


def _coerce_config_scalar(value: object, flag_type: type) -> object:
    """Coerce a single JSON config value to the given type.

    Returns the coerced value, or raises ValueError if coercion fails.
    """
    if flag_type is bool:
        if isinstance(value, bool):
            return value
        raise ValueError(f"expected boolean, got {_config_typename(value)}")
    if flag_type is int:
        if isinstance(value, int) and not isinstance(value, bool):
            return value
        raise ValueError(f"expected integer, got {_config_typename(value)}")
    if flag_type is float:
        if isinstance(value, (int, float)) and not isinstance(value, bool):
            return float(value)
        raise ValueError(f"expected float, got {_config_typename(value)}")
    if flag_type is str:
        if isinstance(value, str):
            return value
        raise ValueError(f"expected string, got {_config_typename(value)}")
    raise ValueError(f"unsupported flag type {flag_type}")


def _coerce_config_value(value: object, flag: "Flag") -> object:
    """Coerce a JSON config value to the flag's type.

    Returns the coerced value, or raises ValueError if coercion fails.
    Handles scalar, array (repeatable), and object (dict) values.
    """
    # Dict flags expect a JSON object
    if flag.compound == "dict":
        if not isinstance(value, dict):
            raise ValueError(
                f"expected object for dict flag, got {_config_typename(value)}"
            )
        result = {}
        for k, v in value.items():
            try:
                result[k] = _coerce_config_scalar(v, flag.value_type)
            except ValueError:
                raise ValueError(
                    f"key '{k}': expected {flag.value_type.__name__}, "
                    f"got {_config_typename(v)}"
                )
        return result
    if isinstance(value, list):
        if not flag.repeatable:
            raise ValueError("expected scalar, got array")
        result_list = []
        for i, elem in enumerate(value):
            try:
                result_list.append(_coerce_config_scalar(elem, flag.type))
            except ValueError:
                raise ValueError(
                    f"element {i}: expected {flag.type.__name__}, "
                    f"got {_config_typename(elem)}"
                )
        return result_list
    if flag.repeatable:
        raise ValueError(
            f"expected array for repeatable flag, got {_config_typename(value)}"
        )
    return _coerce_config_scalar(value, flag.type)


def _resolve_flag_show_source(f: "Flag", config_data: dict) -> tuple[object, str]:
    """Resolve the effective value and source for a flag in config show context.

    Precedence: env > config > default.
    "cli" is structurally impossible in config show because the app's own
    flags were never passed on the command line.
    """
    # Check env first (highest precedence after CLI)
    if f.env is not None:
        env_val = os.environ.get(f.env)
        if env_val is not None:
            # Coerce the env value to the flag's type for display
            if f.type is bool:
                try:
                    return _strict_bool(env_val), "env"
                except ValueError:
                    return env_val, "env"
            elif f.type is int:
                try:
                    return _strict_int(env_val), "env"
                except ValueError:
                    return env_val, "env"
            elif f.type is float:
                try:
                    return _strict_float(env_val), "env"
                except ValueError:
                    return env_val, "env"
            else:
                return env_val, "env"
    # Check config
    param = _flag_param_name(f.name)
    if param in config_data:
        return config_data[param], "config"
    # Default
    if f.presence == _PRESENCE_DEFAULT:
        return f.default, "default"
    return None, "default"


def _format_config_value(value: object) -> str:
    """Format a config value for display, matching Go's formatConfigValue."""
    if value is None:
        return "<nil>"
    if isinstance(value, dict):
        return json.dumps(value)
    if isinstance(value, list):
        return json.dumps(value)
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, float):
        return _format_float_canonical(value)
    return str(value)


def _nested_get(data: dict, dotted_key: str) -> tuple[bool, object]:
    """Look up a dot-separated key in a nested dict.

    Returns (found, value). If any intermediate segment is missing or
    not a dict, returns (False, None).
    """
    parts = dotted_key.split(".")
    current = data
    for part in parts[:-1]:
        if not isinstance(current, dict) or part not in current:
            return False, None
        current = current[part]
    if not isinstance(current, dict) or parts[-1] not in current:
        return False, None
    return True, current[parts[-1]]


def _nested_set(data: dict, dotted_key: str, value: object) -> None:
    """Set a dot-separated key in a nested dict, creating intermediate dicts."""
    parts = dotted_key.split(".")
    current = data
    for part in parts[:-1]:
        if part not in current or not isinstance(current[part], dict):
            current[part] = {}
        current = current[part]
    current[parts[-1]] = value


def _nested_delete(data: dict, dotted_key: str) -> bool:
    """Delete a dot-separated key from a nested dict.

    Returns True if the key was found and deleted, False otherwise.
    Cleans up empty intermediate dicts.
    """
    parts = dotted_key.split(".")
    # Walk to the parent, tracking the path for cleanup
    parents: list[tuple[dict, str]] = []
    current = data
    for part in parts[:-1]:
        if not isinstance(current, dict) or part not in current:
            return False
        parents.append((current, part))
        current = current[part]
    if not isinstance(current, dict) or parts[-1] not in current:
        return False
    del current[parts[-1]]
    # Clean up empty intermediate dicts
    for parent, key in reversed(parents):
        if not parent[key]:
            del parent[key]
    return True


def _collect_nested_keys(data: dict, prefix: str = "") -> list[str]:
    """Collect all leaf keys from a nested dict as dot-separated paths.

    Non-dict values are leaves. Dict values are recursed into.
    """
    keys: list[str] = []
    for k, v in data.items():
        full_key = f"{prefix}.{k}" if prefix else k
        if isinstance(v, dict):
            keys.extend(_collect_nested_keys(v, full_key))
        else:
            keys.append(full_key)
    return keys


def _check_config_field_type(cf: "ConfigField", value: object) -> str | None:
    """Validate that a config file value matches the config field's declared type.

    Returns an error message, or None if the type matches.
    """
    type_name = cf.type.__name__
    if cf.type is bool:
        if not isinstance(value, bool):
            return (
                f'config field "{cf.name}": expected {type_name}, '
                f"got {_config_typename(value)}"
            )
    elif cf.type is int:
        if not isinstance(value, int) or isinstance(value, bool):
            return (
                f'config field "{cf.name}": expected {type_name}, '
                f"got {_config_typename(value)}"
            )
    elif cf.type is float:
        if not isinstance(value, (int, float)) or isinstance(value, bool):
            return (
                f'config field "{cf.name}": expected {type_name}, '
                f"got {_config_typename(value)}"
            )
    elif cf.type is str:
        if not isinstance(value, str):
            return (
                f'config field "{cf.name}": expected {type_name}, '
                f"got {_config_typename(value)}"
            )
    return None


def _config_set_field(
    effects: "_Effects",
    key: str,
    write: object,
    cf: "ConfigField",
    existing: dict,
    path: str,
    config_format: str,
) -> int:
    """Handle 'config set' for a config field (not a flag).

    ``write`` is the elected member of the command's write selection: a value,
    a clear, or a reset to the declared default (contract §27.1, §18.33 item
    304). The hand-rolled "exactly one of the three" guards are gone with the
    three bools that made them expressible -- electing none is the framework's
    own unsatisfied-selector refusal, and electing two is unrepresentable.

    Returns an exit code (0 = success, 1 = error).
    """
    if isinstance(write, _ConfigSetClear):
        print("config set: --clear is only for repeatable flags", file=sys.stderr)
        return 1

    if isinstance(write, _ConfigSetDefault):
        if not _write_config_unset(effects, existing, path, config_format, key):
            print(f"config set: key '{key}' not in config", file=sys.stderr)
            return 1
        return 0

    # Coerce string value to the config field's type
    value = write.value
    try:
        if cf.type is bool:
            typed_value = _strict_bool(value)
        elif cf.type is int:
            typed_value = _strict_int(value)
        elif cf.type is float:
            try:
                typed_value = _strict_float(value)
            except ValueError as fe:
                msg = str(fe)
                if msg in ("NaN is not allowed", "Inf is not allowed"):
                    raise
                raise ValueError(f"expected float, got '{value}'") from fe
        else:
            typed_value = value
    except ValueError as e:
        print(f"config set: key '{key}': {e}", file=sys.stderr)
        return 1

    _write_config_set(effects, existing, path, config_format, key, typed_value)
    return 0


def _write_config_set(effects: "_Effects", data: dict, path: str,
                      config_format: str, key: str, value: object) -> None:
    """Set ``key`` = ``value`` in config and persist THROUGH ``ctx.effects``.

    For TOML, edits are comment/order-preserving: the existing file is loaded
    into a tomlkit document, only the changed key is written, and the document
    is dumped back. JSON is serialized from the in-memory ``data`` dict.

    The write is a `FILE_WRITE` on the effects handle, not a bare ``open``:
    ``config set`` is classified `mutating`, so under ``--dry-run`` the write
    must be RECORDED, never performed. A framework command that printed
    "DRY RUN -- no changes were made." while rewriting the user's config file
    would be the loudest possible counterexample to its own regime.
    """
    _nested_set(data, key, value)
    if config_format == "toml":
        doc = _load_toml_doc(path)
        _toml_set_nested(doc, key, value)
        text = tomlkit.dumps(doc)
    else:
        text = json.dumps(data, indent=2) + "\n"
    effects.write(path, text)


def _write_config_unset(effects: "_Effects", data: dict, path: str,
                        config_format: str, key: str) -> bool:
    """Remove ``key`` from config and persist. Returns False if key was absent.

    For TOML, the removal is comment/order-preserving (only the target key is
    dropped). JSON is serialized from the in-memory ``data`` dict. The write
    goes through ``ctx.effects`` for the reason spelled out above.
    """
    if not _nested_delete(data, key):
        return False
    if config_format == "toml":
        doc = _load_toml_doc(path)
        _toml_del_nested(doc, key)
        text = tomlkit.dumps(doc)
    else:
        text = json.dumps(data, indent=2) + "\n"
    effects.write(path, text)
    return True


def _ensure_config_dir(effects: "_Effects", path: str) -> None:
    """Record/perform the config file's parent directory creation.

    The existence probe is an ordinary filesystem READ (never an effect), and
    branching on it is branching on a real value, so the preview walks straight
    through it in both modes -- the §5.2 idiom. Probing keeps the preview honest:
    a `mkdir` line appears only when a directory would really be created.
    """
    dir_path = os.path.dirname(path)
    if dir_path and not os.path.isdir(dir_path):
        effects.mkdir(dir_path)


def _generate_config_template_toml(
    flags: list["Flag"],
    config_fields: dict[str, "ConfigField"],
) -> str:
    """Generate a TOML config template with comments."""
    lines: list[str] = []

    # A config field whose name equals a flag's param name is validation-only:
    # it annotates the flag and the key is rendered once (on the flag).
    flag_params = {_flag_param_name(f.name) for f in flags}
    colliding = {n: cf for n, cf in config_fields.items() if n in flag_params}

    # Flag-backed keys (flat)
    for f in flags:
        param = _flag_param_name(f.name)
        comment = f"# {f.help}"
        cf_collide = colliding.get(param)
        if cf_collide is not None:
            comment += f" -- {cf_collide.help}"
        lines.append(comment)
        if f.presence == _PRESENCE_DEFAULT:
            lines.append(f"{param} = {_toml_format_scalar(f.default)}")
        else:
            lines.append(f"# {param} =")
        lines.append("")

    # Config field keys (possibly nested via dot names). Skip colliding fields
    # (already rendered on the flag line above).
    # Group by first segment for TOML sections
    top_level: list[tuple[str, "ConfigField"]] = []
    sections: dict[str, list[tuple[str, "ConfigField"]]] = {}
    for name, cf in config_fields.items():
        if name in colliding:
            continue
        parts = name.split(".")
        if len(parts) == 1:
            top_level.append((name, cf))
        else:
            section = parts[0]
            if section not in sections:
                sections[section] = []
            sections[section].append((name, cf))

    for name, cf in top_level:
        req = " (required)" if cf.required else ""
        lines.append(f"# {cf.help}{req}")
        if not cf.required:
            lines.append(f"{name} = {_toml_format_scalar(cf.default)}")
        else:
            lines.append(f"# {name} =")
        lines.append("")

    for section, fields in sections.items():
        lines.append(f"[{section}]")
        for name, cf in fields:
            # The key within the section is everything after the first dot
            sub_parts = name.split(".", 1)
            sub_key = sub_parts[1] if len(sub_parts) > 1 else sub_parts[0]
            # Handle deeper nesting
            deeper_parts = sub_key.split(".")
            if len(deeper_parts) > 1:
                # Need a sub-section
                sub_section = f"{section}.{deeper_parts[0]}"
                leaf_key = ".".join(deeper_parts[1:])
                lines.append("")
                lines.append(f"[{sub_section}]")
                req = " (required)" if cf.required else ""
                lines.append(f"# {cf.help}{req}")
                if not cf.required:
                    lines.append(f"{leaf_key} = {_toml_format_scalar(cf.default)}")
                else:
                    lines.append(f"# {leaf_key} =")
            else:
                req = " (required)" if cf.required else ""
                lines.append(f"# {cf.help}{req}")
                if not cf.required:
                    lines.append(f"{sub_key} = {_toml_format_scalar(cf.default)}")
                else:
                    lines.append(f"# {sub_key} =")
        lines.append("")

    return "\n".join(lines) + "\n" if lines else ""


def _generate_config_template_json(
    flags: list["Flag"],
    config_fields: dict[str, "ConfigField"],
) -> str:
    """Generate a JSON config template."""
    data: dict = {}
    # A config field colliding with a flag's param name is validation-only; the
    # flag owns the rendered value, so the key appears once.
    flag_params = {_flag_param_name(f.name) for f in flags}
    # Flag-backed keys
    for f in flags:
        param = _flag_param_name(f.name)
        if f.presence == _PRESENCE_DEFAULT:
            data[param] = f.default
        else:
            data[param] = None

    # Config field keys (nested via dot names). Skip colliding fields (rendered
    # once via the flag above).
    for name, cf in config_fields.items():
        if name in flag_params:
            continue
        if not cf.required:
            _nested_set(data, name, cf.default)
        else:
            _nested_set(data, name, None)

    return json.dumps(data, indent=2) + "\n"


def _split_escaped(value: str, sep: str) -> list[str]:
    """Split value on sep, treating backslash as escape character.

    Escaped sep becomes literal sep. Escaped backslash becomes literal backslash.
    Trailing backslash with nothing to escape becomes literal backslash.
    """
    parts: list[str] = []
    current: list[str] = []
    i = 0
    while i < len(value):
        if value[i] == "\\":
            if i + 1 < len(value):
                next_ch = value[i + 1]
                if next_ch == sep:
                    current.append(sep)
                    i += 2
                elif next_ch == "\\":
                    current.append("\\\\")
                    i += 2
                else:
                    current.append("\\")
                    current.append(next_ch)
                    i += 2
            else:
                # Trailing backslash
                current.append("\\\\")
                i += 1
        elif value[i] == sep:
            parts.append("".join(current))
            current = []
            i += 1
        else:
            current.append(value[i])
            i += 1
    parts.append("".join(current))
    return parts


def _values_equal_for_conflict(cli_val: object, config_val: object, flag: "Flag") -> bool:
    """Compare a CLI/env value and a config value for conflict-mode equality.

    Equality semantics (pinned):
    - scalars: exact equality.
    - plain repeatable lists: order-sensitive exact equality.
    - Unique flags: order-insensitive multiset equality.

    When the two values are equal, config+CLI/env co-presence is NOT a conflict
    (they agree), so error mode does not fire.
    """
    if flag.unique is True and isinstance(cli_val, list) and isinstance(config_val, list):
        # Order-insensitive multiset comparison.
        return sorted(cli_val, key=repr) == sorted(config_val, key=repr)
    return cli_val == config_val


def _check_flag_configfield_default(
    flag_name: str, flag_presence: str, flag_default: object, cf: "ConfigField"
) -> None:
    """Raise ValueError when a colliding flag and config field have conflicting
    explicit defaults.

    A ConfigField whose name equals a flag's param name is a validation-only
    declaration -- it annotates the flag. Their defaults must agree. The matrix:
    both absent OK; equal OK; both present unequal = error; one absent OK (the
    flag's default wins for rendering). A flag has a default exactly when its
    declared presence is "default"; a ConfigField default of _MISSING is absent.
    """
    flag_has_default = flag_presence == _PRESENCE_DEFAULT
    cf_has_default = not isinstance(cf.default, _MissingSentinel)
    if flag_has_default and cf_has_default and flag_default != cf.default:
        raise ValueError(
            f'config field "{cf.name}" collides with flag "{flag_name}" but their defaults disagree ({cf.default!r} vs {flag_default!r}); remove one default or make them equal'
        )


def _find_duplicate(values: list) -> object | None:
    """Return the first duplicate value in the list, or None if all unique."""
    seen: set = set()
    for v in values:
        if v in seen:
            return v
        seen.add(v)
    return None


def _format_float_canonical(value: float) -> str:
    """Format a float in strictcli canonical form (SCF).

    Rules (must match the Go implementation for cross-language parity):
    1. Shortest decimal string that round-trips to the identical IEEE-754 double
       (Python's ``repr`` already yields shortest round-trip digits).
    2. Integer-valued floats in fixed notation always carry a trailing ``.0``.
    3. ``-0.0`` is preserved as ``-0.0``.
    4. Fixed notation for ``|x|`` in ``[1e-6, 1e21)``; scientific outside. Zero
       (``0.0`` / ``-0.0``) is always rendered fixed.
    5. Scientific spelling: lowercase ``e``, explicit sign, no zero-padding on
       the exponent (e.g. ``1e+21``, ``1e-7``, ``1.5e+300``).
    6. The ``.0`` rule applies only in the fixed branch, never scientific.
    """
    # Zero carve-out (covers both 0.0 and -0.0).
    if value == 0.0:
        return "-0.0" if math.copysign(1.0, value) < 0 else "0.0"
    absval = -value if value < 0 else value
    sign = "-" if value < 0 else ""
    if 1e-6 <= absval < 1e21:
        # Fixed notation, expanded from the shortest round-trip digits.
        s = format(decimal.Decimal(repr(absval)), "f")
        if "." not in s:
            s += ".0"
        return sign + s
    # Scientific notation: one digit before the point, shortest mantissa.
    r = repr(absval)
    if "e" in r or "E" in r:
        mant, exp_part = re.split("[eE]", r)
        exp = int(exp_part)
    else:
        mant, exp = r, 0
    if "." in mant:
        int_part, frac_part = mant.split(".")
    else:
        int_part, frac_part = mant, ""
    digits = (int_part + frac_part).lstrip("0")
    point_exp = exp - len(frac_part)
    stripped = digits.rstrip("0")
    if stripped == "":
        stripped = "0"
    point_exp += len(digits) - len(stripped)
    digits = stripped
    sci_exp = point_exp + len(digits) - 1
    mantissa = digits if len(digits) == 1 else digits[0] + "." + digits[1:]
    exp_sign = "+" if sci_exp >= 0 else "-"
    return f"{sign}{mantissa}e{exp_sign}{abs(sci_exp)}"


def _format_dict_for_display(value: dict) -> str:
    """Render a dict flag value as canonical ``key=value`` pairs.

    Keys are sorted for deterministic output, matching Go's
    ``formatDictForDisplay``. Values are rendered via ``_format_value_for_error``.
    """
    parts = [f"{k}={_format_value_for_error(value[k])}" for k in sorted(value)]
    return ", ".join(parts)


def _format_default_for_help(value: object) -> str:
    """Format a default value for help text.

    Floats use the canonical form (SCF); dict values render as sorted
    ``key=value`` pairs (matching Go). Every other type is rendered as ``str``.
    """
    if isinstance(value, float):
        return _format_float_canonical(value)
    if isinstance(value, dict):
        return _format_dict_for_display(value)
    return str(value)


def _format_value_for_error(value: object) -> str:
    """Format a value for inclusion in error messages (without quotes).

    Floats use the canonical form (SCF). Bools are lowercase.
    Dict values render as sorted ``key=value`` pairs (matching Go).
    Strings are returned as-is.
    """
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, float):
        return _format_float_canonical(value)
    if isinstance(value, dict):
        return _format_dict_for_display(value)
    return str(value)


def _config_typename(value: object) -> str:
    """Return a type name for config values, matching Go's typeName."""
    if isinstance(value, bool):
        return "bool"
    if isinstance(value, int):
        return "int"
    if isinstance(value, float):
        return "float"
    if isinstance(value, str):
        return "str"
    if value is None:
        return "null"
    if isinstance(value, list):
        return "array"
    if isinstance(value, dict):
        return "object"
    return type(value).__name__


_IDENTIFIER_RE = re.compile(r"^[a-z][a-z0-9-]*$")
_CHECK_REQUIRED_FIELDS = {"tags", "severity", "fast", "pure", "needs_network", "depends_on"}
_CHECK_OPTIONAL_FIELDS = {"scope"}
_CHECK_VALID_SEVERITIES = {"error", "warn"}


def _parse_checks_toml(data: bytes) -> tuple[str, dict[str, _CheckDef]]:
    """Parse and validate checks TOML data, returning (app_name, check_defs).

    Raises ValueError on any schema violation or invalid TOML.
    """
    try:
        parsed = tomllib.loads(data.decode())
    except (tomllib.TOMLDecodeError, UnicodeDecodeError) as exc:
        raise ValueError(f"checks.toml: {exc}") from exc

    # Only "app" and [checks] are allowed at the top level
    for key in parsed:
        if key not in ("app", "checks"):
            raise ValueError(f'checks.toml: unknown top-level key "{key}"')

    # Validate required "app" field
    if "app" not in parsed:
        raise ValueError('checks.toml: missing required top-level key "app"')
    if not isinstance(parsed["app"], str) or not parsed["app"]:
        raise ValueError('checks.toml: "app" must be a non-empty string')
    app_name = parsed["app"]

    if "checks" not in parsed:
        return (app_name, {})

    checks_section = parsed["checks"]
    if not isinstance(checks_section, dict):
        raise ValueError("checks.toml: [checks] must be a table")

    result: dict[str, _CheckDef] = {}

    for name, fields in checks_section.items():
        # Validate check name
        if not _IDENTIFIER_RE.fullmatch(name):
            raise ValueError(
                f'checks.toml: invalid check name "{name}" '
                f"(must match [a-z][a-z0-9-]*)"
            )
        if not isinstance(fields, dict):
            raise ValueError(f'checks.toml: check "{name}" must be a table')

        # No unknown fields
        unknown = set(fields.keys()) - _CHECK_REQUIRED_FIELDS - _CHECK_OPTIONAL_FIELDS
        if unknown:
            raise ValueError(
                f'checks.toml: check "{name}": unknown field "{sorted(unknown)[0]}"'
            )

        # Required fields
        for req in sorted(_CHECK_REQUIRED_FIELDS):
            if req not in fields:
                raise ValueError(
                    f'checks.toml: check "{name}": missing required field "{req}"'
                )

        # Validate tags
        tags = fields["tags"]
        if not isinstance(tags, list):
            raise ValueError(
                f'checks.toml: check "{name}": "tags" must be a list of strings'
            )
        for tag in tags:
            if not isinstance(tag, str) or not tag.strip():
                raise ValueError(
                    f'checks.toml: check "{name}": "tags" entries must be non-empty strings'
                )

        # Validate severity
        severity = fields["severity"]
        if not isinstance(severity, str) or severity not in _CHECK_VALID_SEVERITIES:
            raise ValueError(
                f'checks.toml: check "{name}": "severity" must be "error" or "warn", '
                f"got {severity!r}"
            )

        # Validate booleans
        for bool_field in ("fast", "pure", "needs_network"):
            val = fields[bool_field]
            if not isinstance(val, bool):
                raise ValueError(
                    f'checks.toml: check "{name}": "{bool_field}" must be a boolean, '
                    f"got {type(val).__name__}"
                )

        # Validate depends_on
        depends_on = fields["depends_on"]
        if not isinstance(depends_on, list):
            raise ValueError(
                f'checks.toml: check "{name}": "depends_on" must be a list of strings'
            )
        for dep in depends_on:
            if not isinstance(dep, str):
                raise ValueError(
                    f'checks.toml: check "{name}": "depends_on" entries must be strings'
                )

        # Validate optional scope field
        scope = fields.get("scope", "")
        if not isinstance(scope, str):
            raise ValueError(
                f'checks.toml: check "{name}": "scope" must be a string, '
                f"got {type(scope).__name__}"
            )

        result[name] = _CheckDef(
            name=name,
            tags=tags,
            severity=severity,
            fast=fields["fast"],
            pure=fields["pure"],
            needs_network=fields["needs_network"],
            depends_on=depends_on,
            scope=scope,
        )

    # Cross-validate depends_on references
    for name, check_def in result.items():
        for dep in check_def.depends_on:
            if dep not in result:
                raise ValueError(
                    f'checks.toml: check "{name}": depends_on references '
                    f'unknown check "{dep}"'
                )

    return (app_name, result)


def _load_checks_toml(path: str | Path) -> tuple[str, dict[str, _CheckDef]]:
    """Read and parse a checks.toml file, returning (app_name, check_defs).

    Raises ValueError on any file error, schema violation, or invalid TOML.
    """
    path = Path(path)
    try:
        raw = path.read_bytes()
    except OSError as exc:
        raise ValueError(f"checks.toml: {exc}") from exc
    return _parse_checks_toml(raw)


class _HelpRequested(Exception):
    """Raised when --help or -h is encountered."""

    def __init__(self, target: object) -> None:
        self.target = target
        super().__init__()


class _VersionRequested(Exception):
    """Raised when --version or -v is encountered."""


class _DumpSchemaRequested(Exception):
    """Raised when --dump-schema is encountered."""


class _McpRequested(Exception):
    """Raised when --mcp is encountered."""


class _ParseError(Exception):
    """Raised for user-facing parse errors."""

    def __init__(self, message: str, command_prefix: str | None = None):
        super().__init__(message)
        self.command_prefix = command_prefix


class InvokeError(Exception):
    """Raised by app.call() for invocation errors (unknown command, missing flags, etc.)."""


def _strict_bool(s: str) -> bool:
    """Parse a boolean string strictly.

    Accepts: 1, true, yes (case-insensitive) -> True
    Accepts: 0, false, no (case-insensitive) -> False
    Everything else raises ValueError.
    """
    lower = s.lower()
    if lower in ("1", "true", "yes"):
        return True
    if lower in ("0", "false", "no"):
        return False
    raise ValueError(f"expected boolean, got '{s}'")


def _strict_int(s: str) -> int:
    """Parse an integer string strictly -- no leading/trailing whitespace allowed.

    Python's int() silently strips whitespace; Go's strconv.Atoi does not.
    This matches Go's stricter behavior. Additionally, the result is
    range-checked to fit in a signed 64-bit integer, matching Go's int/int64.

    All errors raise ValueError with the same message format as Go's
    parseIntStrict: "expected integer, got '<value>'".
    """
    if s != s.strip():
        raise ValueError(f"expected integer, got '{s}'")
    # Python's int() accepts PEP 515 underscore digit separators ('1_000');
    # Go's strconv and the TypeScript parser reject them. Reject to match canon.
    if "_" in s:
        raise ValueError(f"expected integer, got '{s}'")
    try:
        n = int(s)
    except ValueError:
        raise ValueError(f"expected integer, got '{s}'") from None
    if n < -(2**63) or n > 2**63 - 1:
        raise ValueError(f"expected integer, got '{s}'")
    return n


def _strict_float(s: str) -> float:
    """Parse a float string strictly -- no leading/trailing whitespace allowed.

    Rejects nan, inf, and -inf (case-insensitive) since these are valid Python
    floats but not useful CLI values.
    """
    if s != s.strip():
        raise ValueError(f"invalid literal for float(): {s!r}")
    low = s.lower()
    if low == "nan":
        raise ValueError("NaN is not allowed")
    if low in ("inf", "-inf", "+inf", "infinity", "-infinity", "+infinity"):
        raise ValueError("Inf is not allowed")
    result = float(s)
    # A finite literal that overflows to +/-inf ('1e999') is a parse failure,
    # not an Inf literal. Go and TypeScript reject it as an invalid float; use
    # the generic "expected float" path (not "Inf is not allowed") to match.
    if math.isinf(result):
        raise ValueError(f"invalid literal for float(): {s!r}")
    return result


def _float_parse_error(
    flag_name: str, raw: str, exc: ValueError, *, env: str | None = None,
) -> "_ParseError":
    """Build a _ParseError for a failed float parse.

    If the ValueError is a NaN/Inf rejection, use its message directly.
    Otherwise, produce the generic "expected float, got ..." message.
    """
    msg = str(exc)
    suffix = f" (from env var '{env}')" if env else ""
    if msg in ("NaN is not allowed", "Inf is not allowed"):
        return _ParseError(f"--{flag_name}: {msg}{suffix}")
    return _ParseError(f"--{flag_name}: expected float, got {raw!r}{suffix}")


def _coerce_arg_value(a: "Arg", raw: str) -> object:
    """Coerce a raw positional arg string to the declared type.

    Uses the same strict parsing functions as flags: _strict_int, _strict_float,
    _strict_bool. Error messages follow the same pattern as flag type errors,
    with "argument '<name>'" instead of "--<name>".
    """
    if a.type is str:
        return raw
    if a.type is int:
        try:
            return _strict_int(raw)
        except ValueError as e:
            raise _ParseError(f"argument '{a.name}': {e}")
    if a.type is float:
        try:
            return _strict_float(raw)
        except ValueError as e:
            msg = str(e)
            if msg in ("NaN is not allowed", "Inf is not allowed"):
                raise _ParseError(f"argument '{a.name}': {msg}")
            raise _ParseError(f"argument '{a.name}': expected float, got {raw!r}")
    if a.type is bool:
        try:
            return _strict_bool(raw)
        except ValueError as e:
            raise _ParseError(f"argument '{a.name}': {e}")
    # Unreachable (validated at registration), but defensive
    return raw  # pragma: no cover


_AT_PREFIX_MAX_SIZE = 1024 * 1024  # 1 MB


def _resolve_at_prefix(
    flag_name: str, raw: str, stdin_consumed_by: str | None,
) -> tuple[str, str | None]:
    """Resolve @-prefix for string flag values.

    Returns (resolved_value, updated_stdin_consumed_by).
    """
    if not raw.startswith("@"):
        return raw, stdin_consumed_by
    if raw.startswith("@@"):
        return raw[1:], stdin_consumed_by
    if raw == "@-":
        if stdin_consumed_by is not None:
            raise _ParseError(
                f"--{flag_name}: stdin (@-) can only be used once per invocation"
            )
        try:
            data = sys.stdin.read(_AT_PREFIX_MAX_SIZE + 1)
            if len(data) > _AT_PREFIX_MAX_SIZE:
                raise _ParseError(f"--{flag_name}: file exceeds 1 MB limit")
            return data.rstrip(" \t\n\r"), flag_name
        except _ParseError:
            raise
        except Exception:
            raise _ParseError(f"--{flag_name}: cannot read stdin")
    # @path -- read file
    path = raw[1:]
    if not os.path.exists(path):
        raise _ParseError(f"--{flag_name}: file not found: {path}")
    try:
        with open(path, "r") as f:
            data = f.read(_AT_PREFIX_MAX_SIZE + 1)
        if len(data) > _AT_PREFIX_MAX_SIZE:
            raise _ParseError(f"--{flag_name}: file exceeds 1 MB limit")
        return data.rstrip(" \t\n\r"), stdin_consumed_by
    except _ParseError:
        raise
    except Exception:
        raise _ParseError(f"--{flag_name}: cannot read file: {path}")


def _require_non_empty_str(value: str, field_name: str, class_name: str) -> None:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{class_name}.{field_name} must be a non-empty string")


def _parse_dict_value(
    flag_name: str, raw: str, value_type: type,
) -> tuple[str, object] | dict[str, object]:
    """Parse a dict flag value from CLI.

    Two formats:
    - key=value: splits on first '=', coerces value to value_type
    - JSON string starting with '{': parsed as JSON dict

    For key=value format, returns a (key, coerced_value) tuple.
    For JSON format, returns a dict of {key: coerced_value}.
    """
    # JSON format: detected by leading '{'
    if raw.startswith("{"):
        try:
            parsed = json.loads(raw)
        except json.JSONDecodeError as e:
            raise _ParseError(f"--{flag_name}: invalid JSON: {e}")
        if not isinstance(parsed, dict):
            raise _ParseError(
                f"--{flag_name}: JSON value must be an object, "
                f"got {type(parsed).__name__}"
            )
        result = {}
        for k, v in parsed.items():
            if not isinstance(k, str):
                raise _ParseError(
                    f"--{flag_name}: JSON key must be a string, got {k!r}"
                )
            result[k] = _coerce_dict_json_value(flag_name, k, v, value_type)
        return result

    # key=value format: split on first '='
    if "=" not in raw:
        raise _ParseError(
            f"--{flag_name}: expected key=value or JSON, got '{raw}'"
        )
    eq_pos = raw.index("=")
    key = raw[:eq_pos]
    val_str = raw[eq_pos + 1:]

    if not key:
        raise _ParseError(f"--{flag_name}: empty key in '{raw}'")

    if value_type is int:
        try:
            return (key, _strict_int(val_str))
        except ValueError as e:
            raise _ParseError(f"--{flag_name}: value for key '{key}': {e}")
    elif value_type is float:
        try:
            return (key, _strict_float(val_str))
        except ValueError as e:
            raise _float_parse_error(flag_name, val_str, e)
    else:  # str
        return (key, val_str)


def _coerce_dict_json_value(
    flag_name: str, key: str, value: object, value_type: type,
) -> object:
    """Coerce a JSON-parsed value to the dict's value type."""
    if value_type is str:
        if not isinstance(value, str):
            raise _ParseError(
                f"--{flag_name}: JSON value for key '{key}' must be a string, "
                f"got {_config_typename(value)}"
            )
        return value
    if value_type is int:
        if not isinstance(value, int) or isinstance(value, bool):
            raise _ParseError(
                f"--{flag_name}: JSON value for key '{key}' must be an integer, "
                f"got {_config_typename(value)}"
            )
        return value
    if value_type is float:
        if not isinstance(value, (int, float)) or isinstance(value, bool):
            raise _ParseError(
                f"--{flag_name}: JSON value for key '{key}' must be a number, "
                f"got {_config_typename(value)}"
            )
        return float(value)
    raise _ParseError(f"--{flag_name}: unsupported value type {value_type}")


def _store_dict_flag(f: "Flag", raw: str, cli_set: dict) -> None:
    """Parse and store a dict flag value from a raw CLI string.

    Handles both key=value and JSON formats. For JSON, may add multiple
    entries at once. For key=value, adds one entry.
    """
    parsed = _parse_dict_value(f.name, raw, f.value_type)
    if isinstance(parsed, dict):
        # JSON format returned a full dict
        if f.name not in cli_set:
            cli_set[f.name] = {}
        for k, v in parsed.items():
            if k in cli_set[f.name]:
                raise _ParseError(f"--{f.name}: duplicate key '{k}'")
            cli_set[f.name][k] = v
    else:
        # key=value format returned a tuple
        k, v = parsed
        if f.name not in cli_set:
            cli_set[f.name] = {}
        if k in cli_set[f.name]:
            raise _ParseError(f"--{f.name}: duplicate key '{k}'")
        cli_set[f.name][k] = v


def _resolve_flag_env_value(
    f: "Flag", env_val: str, stdin_consumed_by: list,
) -> object:
    """Coerce one env var value for one flag, whatever scope declared it.

    The compound, repeatable, ``env_separator`` and @-prefix rules are the
    flag's own, so an env binding inside an elected scope resolves exactly as
    an unconditional one does (§24.3's "unaffected" list, §24.6). One function
    holds them, because three copies of this block are how the scoped surface
    came to silently ignore ``env_separator``.
    """
    if f.compound == "dict":
        try:
            parsed = json.loads(env_val)
        except json.JSONDecodeError as e:
            raise _ParseError(
                f"--{f.name}: invalid JSON in env var '{f.env}': {e}"
            )
        if not isinstance(parsed, dict):
            raise _ParseError(
                f"--{f.name}: env var '{f.env}' must be a JSON object, "
                f"got {type(parsed).__name__}"
            )
        return {
            k: _coerce_dict_json_value(f.name, k, v, f.value_type)
            for k, v in parsed.items()
        }
    if f.type is bool:
        try:
            return _strict_bool(env_val)
        except ValueError:
            raise _ParseError(
                f"invalid boolean value {env_val!r} for env var "
                f"'{f.env}' (flag '--{f.name}')"
            )
    split = f.repeatable and f.env_separator is not None
    elements = _split_escaped(env_val, f.env_separator) if split else [env_val]
    coerced_list: list = []
    for element in elements:
        if f.type is int:
            try:
                coerced_list.append(_strict_int(element))
            except ValueError as e:
                raise _ParseError(f"--{f.name}: {e} (from env var '{f.env}')")
        elif f.type is float:
            try:
                coerced_list.append(_strict_float(element))
            except ValueError as e:
                raise _float_parse_error(f.name, element, e, env=f.env)
        else:
            resolved, stdin_consumed_by[0] = _resolve_at_prefix(
                f.name, element, stdin_consumed_by[0],
            )
            coerced_list.append(resolved)
    if split:
        if f.unique:
            dup = _find_duplicate(coerced_list)
            if dup is not None:
                raise _ParseError(
                    f"--{f.name}: duplicate value "
                    f"'{_format_value_for_error(dup)}' "
                    f"(from env var '{f.env}')"
                )
        return coerced_list
    return coerced_list if f.repeatable else coerced_list[0]


_SCALAR_TYPES = (str, bool, int, float)
_NON_BOOL_SCALAR_TYPES = (str, int, float)

# The reserved flag quartet owned by the effects regime. Banned unconditionally
# at EVERY level (app global flags, command flags, flag-set flags, mutex-group
# flags) -- not just at the global level. Short-flag names and positional arg
# names are unaffected by this ban, and the four flags themselves have no short
# forms.
_RESERVED_FRAMEWORK_FLAG_NAMES = frozenset({
    "dry-run", "approve-consequential", "quiet", "verbose",
})

# The machine-mode flag name, reserved on the SAME unconditional every-level
# tier as the quartet (contract §7.1's 2026-08-13 amendment). It is NOT a
# fifth member of the quartet -- the four are the effects regime's own flags
# and are named as a set throughout the contract -- so it carries its own
# reserved-name message and its own token entry.
_RESERVED_MACHINE_FLAG_NAME = "json"

# `yes` is NOT a framework flag any more -- it was replaced by
# --approve-consequential (contract §7.1) -- but it stays banned so nobody
# reintroduces a private --yes meaning the same thing. Its ban message points
# at the replacement.
_BANNED_FLAG_NAMES = frozenset({"yes"})

# The programmatic consent PARAMETER name, reserved on both the flag surface
# and the arg surface at every level. `call(..., approve_consequential=...)`
# is keyword-only and framework-owned, so a command declaring a parameter of
# this name would be unreachable over that channel while staying reachable
# over MCP -- two channels disagreeing about the same command. The quartet ban
# above covers the FLAG spelling `approve-consequential`; this covers the
# underscore spelling the parameter surface actually uses, and it is the one
# reserved name that reaches positional args too.
_RESERVED_CONSENT_PARAM_NAME = "approve_consequential"

# Names reserved by the framework for global flags. The pre-existing set is
# also what a SHORT flag name is checked against (the framework quartet bans
# long names only).
_RESERVED_GLOBAL_SHORT_NAMES = frozenset({
    "help", "h", "version", "v", "dump-schema", "mcp", "config", "hermetic",
})
_RESERVED_GLOBAL_FLAG_NAMES = (
    _RESERVED_GLOBAL_SHORT_NAMES
    | _RESERVED_FRAMEWORK_FLAG_NAMES
    | frozenset({_RESERVED_MACHINE_FLAG_NAME})
)


# argv token -> pre-scan result key for the reserved quartet.
_RESERVED_QUARTET_TOKENS = {
    "--dry-run": "dry_run",
    "--approve-consequential": "approve_consequential",
    "--quiet": "quiet",
    "--verbose": "verbose",
}

# What the pre-scan actually recognizes: the quartet plus --json, which reads
# exactly as the quartet does in BOTH argv regions (contract §7.1's amendment,
# §7.2). The quartet stays a quartet; the machine flag rides the same delivery
# rules without joining the set.
_RESERVED_PRESCAN_TOKENS = {
    **_RESERVED_QUARTET_TOKENS,
    "--json": "json",
}


def _raise_flag_name_reserved_by_framework(name: str):
    """Message template: a flag name collides with the reserved quartet."""
    raise ValueError(
        f"flag name '{name}' is reserved by the framework "
        f"(dry-run, approve-consequential, quiet, verbose)"
    )


def _raise_flag_name_json_reserved():
    """Message template: a flag name collides with the machine-mode flag.

    `--json` is framework-owned (contract §19.1): it selects machine mode and
    is delivered on the Context, never as a handler kwarg. The ban is the
    unconditional every-level one, exactly as the quartet's is.
    """
    raise ValueError(
        "flag name 'json' is reserved by the framework: "
        "--json selects machine mode"
    )


def _raise_flag_name_yes_banned():
    """Message template: a flag named `yes` is banned outright.

    `yes` owns no framework flag any more, but a private --yes would restate
    --approve-consequential in a spelling that IS muscle memory -- which is
    exactly what the rename removed.
    """
    raise ValueError(
        "flag name 'yes' is banned by the framework: "
        "the confirmation skip is --approve-consequential"
    )


def _raise_flag_name_consent_reserved():
    """Message template: a flag name collides with the consent parameter."""
    raise ValueError(
        "flag name 'approve_consequential' is reserved by the framework: "
        "it names the programmatic consent parameter"
    )


def _raise_arg_name_consent_reserved():
    """Message template: an arg name collides with the consent parameter."""
    raise ValueError(
        "arg name 'approve_consequential' is reserved by the framework: "
        "it names the programmatic consent parameter"
    )


# ---------------------------------------------------------------------------
# The presence declaration (contract §23)
# ---------------------------------------------------------------------------

# The three facts every flag and every positional arg declares about itself.
# These are the RESOLVED values carried on Flag.presence / Arg.presence and
# published by --dump-schema; the declaration surface is `presence="required"`,
# `presence="optional"` and `default=<value>` (contract §23.2, §23.3).
_PRESENCE_REQUIRED = "required"
_PRESENCE_OPTIONAL = "optional"
_PRESENCE_DEFAULT = "default"

# The two facts spellable through the `presence=` keyword. The third is spelled
# by `default=` itself: one spelling per fact, which is why `presence="default"`
# is not accepted and `default=None` is refused (contract §23.1).
_PRESENCE_DECLARABLE = (_PRESENCE_REQUIRED, _PRESENCE_OPTIONAL)

# The per-language noun phrases §12.12 pins for Python. The sentences that carry
# them are byte-identical across the three implementations; only these vary.
_PRESENCE_SPELLING = {
    _PRESENCE_REQUIRED: 'presence="required"',
    _PRESENCE_OPTIONAL: 'presence="optional"',
}

def _default_spelling(value: object) -> str:
    """The `default=<value>` spelling with a concrete value, rendered by the
    same formatter every other declaration guard uses (§12.12)."""
    return f"default={_format_value_for_error(value)}"


def _raise_flag_presence_undeclared(name: str):
    """Message template: a flag declared none of the three presence facts."""
    raise ValueError(
        f'Flag "{name}": presence is undeclared: declare exactly one of '
        f'presence="required", presence="optional", or default=<value>'
    )


def _raise_arg_presence_undeclared(name: str):
    """Message template: an arg declared none of the three presence facts."""
    raise ValueError(
        f'Arg "{name}": presence is undeclared: declare exactly one of '
        f'presence="required", presence="optional", or default=<value>'
    )


def _raise_flag_presence_declared_twice(name: str, first: str, second: str):
    """Message template: a flag declared two of the three presence facts."""
    raise ValueError(
        f'Flag "{name}": presence is declared twice: {first} and {second} '
        f"cannot be combined; declare exactly one"
    )


def _raise_arg_presence_declared_twice(name: str, first: str, second: str):
    """Message template: an arg declared two of the three presence facts."""
    raise ValueError(
        f'Arg "{name}": presence is declared twice: {first} and {second} '
        f"cannot be combined; declare exactly one"
    )


def _raise_flag_default_null_not_optional(name: str):
    """Message template: `default=None` is not a spelling of optionality.

    The parenthetical is not decoration: the value the reader wanted is exactly
    what the redirected spelling delivers (contract §12.12).
    """
    raise ValueError(
        f'Flag "{name}": default=None does not declare optionality: use '
        f'presence="optional" (it delivers None when the flag is absent)'
    )


def _raise_arg_default_null_not_optional(name: str):
    """Message template: the arg twin of the null-default redirect."""
    raise ValueError(
        f'Arg "{name}": default=None does not declare optionality: use '
        f'presence="optional" (it delivers None when the arg is absent)'
    )


def _raise_arg_variadic_default(name: str):
    """Message template: a variadic arg cannot declare a default (§23.3)."""
    raise ValueError(
        f'Arg "{name}": a variadic arg cannot declare default=: it always '
        f'delivers a list, so declare presence="required" for at least one '
        f'value or presence="optional" for possibly none'
    )


def _raise_presence_value_invalid(surface: str, name: str, value: object):
    """Message template: `presence=` carried something other than the two facts
    it can spell.

    Authored (not pinned by §12.12): the pinned family covers zero, two and the
    null-valued default, none of which describes `presence="default"` or a
    typo. The redirect names the third spelling for the same reason the
    null-default redirect does.
    """
    raise ValueError(
        f'{surface} "{name}": presence must be "required" or "optional", got '
        f"{value!r}; a default value is declared with default=<value>"
    )


def _resolve_presence(
    surface: str, name: str, presence: object, default: object,
) -> str:
    """Resolve the three-way presence declaration, or raise (contract §23.1).

    Exactly one of `presence="required"`, `presence="optional"` and
    `default=<value>` must be supplied. Zero and two are registration-time hard
    errors, and `default=None` is refused with a redirect to the optional
    spelling rather than accepted as a second spelling of the same fact.

    The count check runs FIRST (§12.12's implementation-sweep amendment, ledger
    item 154). A null default written BESIDE a presence declaration is a
    combination error and reads as one, naming both spellings; the redirect is
    reserved for the null default written as the sole declaration, which is the
    old idiom it exists to teach.

    ``surface`` is ``"Flag"`` or ``"Arg"`` and selects the message family.
    """
    is_flag = surface == "Flag"
    has_presence = not isinstance(presence, _MissingSentinel)
    has_default = not isinstance(default, _MissingSentinel)
    if has_presence and presence not in _PRESENCE_DECLARABLE:
        _raise_presence_value_invalid(surface, name, presence)
    if has_presence and has_default:
        # Canonical order (required, optional, default) regardless of the order
        # they were written in, so the line is deterministic. A null default
        # reaches here too, and names the spelling that was actually written.
        first = _PRESENCE_SPELLING[presence]
        second = _default_spelling(default)
        if is_flag:
            _raise_flag_presence_declared_twice(name, first, second)
        _raise_arg_presence_declared_twice(name, first, second)
    if has_default and default is None:
        if is_flag:
            _raise_flag_default_null_not_optional(name)
        _raise_arg_default_null_not_optional(name)
    if has_presence:
        return presence
    if has_default:
        return _PRESENCE_DEFAULT
    if is_flag:
        _raise_flag_presence_undeclared(name)
    _raise_arg_presence_undeclared(name)


# Sources that mean "the invocation caused this value" (contract §23.6). The
# other two -- `default` and `infra` -- mean the declaration caused it.
_PROVIDED_SOURCES = frozenset({"cli", "env", "config", "implied"})


def _parse_compound_type(
    raw_type: type, context: str,
) -> tuple[str, type | None, type | None]:
    """Parse a type annotation into (kind, item_type, value_type).

    Returns:
        ("scalar", None, None) for str/bool/int/float
        ("list", item_type, None) for list[T]
        ("dict", None, value_type) for dict[str, T]

    Raises ValueError for invalid compound types.
    """
    # Plain scalar types
    if raw_type in _SCALAR_TYPES:
        return ("scalar", None, None)

    # Bare list/dict without type args
    if raw_type is list:
        raise ValueError(
            f'{context}: list type requires an item type '
            f'(e.g., list[int]), got bare list'
        )
    if raw_type is dict:
        raise ValueError(
            f'{context}: dict type requires type arguments '
            f'(e.g., dict[str, int]), got bare dict'
        )

    origin = get_origin(raw_type)

    # list[T]
    if origin is list:
        args = get_args(raw_type)
        if not args:
            raise ValueError(
                f'{context}: list type requires an item type '
                f'(e.g., list[int]), got bare list'
            )
        if len(args) != 1:
            raise ValueError(
                f'{context}: list type takes exactly one type argument, '
                f'got {len(args)}'
            )
        item_type = args[0]
        if item_type not in _NON_BOOL_SCALAR_TYPES:
            raise ValueError(
                f'{context}: list item type must be str, int, or float, '
                f'got {item_type!r}'
            )
        return ("list", item_type, None)

    # dict[str, T]
    if origin is dict:
        args = get_args(raw_type)
        if not args:
            raise ValueError(
                f'{context}: dict type requires type arguments '
                f'(e.g., dict[str, int]), got bare dict'
            )
        if len(args) != 2:
            raise ValueError(
                f'{context}: dict type takes exactly two type arguments, '
                f'got {len(args)}'
            )
        key_type, val_type = args
        if key_type is not str:
            raise ValueError(
                f'{context}: dict key type must be str, got {key_type!r}'
            )
        if val_type not in _NON_BOOL_SCALAR_TYPES:
            raise ValueError(
                f'{context}: dict value type must be str, int, or float, '
                f'got {val_type!r}'
            )
        return ("dict", None, val_type)

    raise ValueError(
        f'{context}: type must be str, bool, int, float, '
        f'list[T], or dict[str, T], got {raw_type!r}'
    )


def _validate_element_type(
    flag_name: str, expected_type: type, value: object, context: str,
) -> None:
    """Validate that a value matches the expected scalar type."""
    if expected_type is str:
        if not isinstance(value, str):
            raise ValueError(
                f'Flag "{flag_name}": {context} is not of type str'
            )
    elif expected_type is int:
        if not isinstance(value, int) or isinstance(value, bool):
            raise ValueError(
                f'Flag "{flag_name}": {context} is not of type int'
            )
    elif expected_type is float:
        if not isinstance(value, (int, float)) or isinstance(value, bool):
            raise ValueError(
                f'Flag "{flag_name}": {context} is not of type float'
            )


# ---------------------------------------------------------------------------
# The value-flag choice record (contract §24.2)
# ---------------------------------------------------------------------------

# The per-language noun phrases §12.13 pins for Python, used inside sentences
# that are byte-identical across the three implementations.
# Attribute a `@choice`-decorated class carries, holding its raw declaration.
_CHOICE_SPEC_ATTR = "__strictcli_choice__"
# Metadata key on a dataclass field declared by sub_flag/sub_choice_flag/
# member_value.
_SCOPE_FIELD_KEY = "strictcli_scope"
# Attribute holding a delivered record's per-field source labels (§24.9).
_RECORD_SOURCES_ATTR = "__strictcli_sources__"

_RECORD_SPELLING = "Choice(<value>, help=...)"
_RETIRED_RECORD_SPELLING = 'RetiredChoice(<value>, message="<message>")'
_SELECTOR_SPELLING = "choice_flag(...)"
_MEMBER_SELECTOR_SPELLING = 'choice_flag(..., elect_by="member-flags")'
# The payload-carrying member's own declaration, which is where its short goes:
# `member_value(...)` IS the electing flag's declaration in this language, the
# way `MemberChoice`'s first argument is in Go (§24.12).
_MEMBER_PAYLOAD_SHORT_SPELLING = "member_value(short=...)"


@dataclass(frozen=True)
class Choice:
    """One entry of a `choices=` value flag: a value, with optional help.

    A `choices=` entry is ALWAYS a record (contract §24.2): the bare-value entry
    is deleted, because an entry that may carry help and an entry that carries
    none would be two spellings of one fact. The help is optional -- that is
    what keeps §24.10's one-line rendering reachable -- and non-empty when
    supplied, like every other help string in the framework.

    Distinct from ``@choice``, which declares a SELECTOR's choice: a name,
    mandatory help, and a scope. The case twins name different constructs, and
    the confusion is named outright by a registration error rather than by
    inventing a third noun (§24.12).
    """

    value: object
    help: str | None = field(default=None, kw_only=True)

    def __post_init__(self) -> None:
        if self.help is not None:
            _require_non_empty_str(self.help, "help", "Choice")


@dataclass(frozen=True)
class RetiredChoice:
    """One retired spelling of a value flag or positional arg.

    The value-level twin of ``app.deprecate``: a value the declaration used to
    accept, plus the message that names its replacement. A retired spelling is
    refused at parse time ahead of the invalid-value check, and it is NOT a
    choice -- help never lists it, and the published ``value_schema`` enum and
    the MCP projection derived from it carry the live set only.

    The message is mandatory and non-empty, like every other message the
    framework prints on a declaration's behalf. There is no exhaustiveness
    story to tell: a handler never receives a retired value, so no closed set
    reaches a delivery site.
    """

    value: object
    message: str = field(kw_only=True)


def _raise_retired_choices_entry_not_record(surface: str, name: str, index: int):
    """Message template: a bare ``retired_choices=`` entry.

    Python-only. Go's variadic ``RetiredChoices(...RetiredChoiceValue)`` and
    TypeScript's record type refuse a bare value at compile time, so neither
    sibling has an input that could produce this line. It mirrors the
    bare-choice refusal, which is the same mis-declaration one keyword over.
    """
    raise ValueError(
        f'{surface} "{name}": retired_choices entry {index} is a bare value: '
        f"declare it as {_RETIRED_RECORD_SPELLING}"
    )


def _resolve_retired_choices(
    surface: str, name: str, entries: object,
) -> tuple["RetiredChoice", ...]:
    """Validate a ``retired_choices=`` list's entry SHAPE.

    The per-entry RULES (a live spelling, a duplicate, an empty message) are
    checked by ``_validate_retired_choices`` once the live choices are known.
    """
    if not isinstance(entries, list):
        _raise_retired_choices_entry_not_record(surface, name, 0)
    records: list[RetiredChoice] = []
    for i, entry in enumerate(entries):
        if not isinstance(entry, RetiredChoice):
            _raise_retired_choices_entry_not_record(surface, name, i)
        records.append(entry)
    return tuple(records)


# The registration-time templates, twinned per surface the way Go's err* and
# TypeScript's err* functions are. Python could parameterize the Flag/Arg
# prefix into one template, and does elsewhere; here it inlines both so the
# three implementations share one signature per rule and the parity manifest
# carries no entry for the family at all.


def _raise_flag_retired_choice_is_live(name: str, value: str):
    raise ValueError(
        f'Flag "{name}": retired choice \'{value}\' is also a live choice: '
        f"a value is live or retired, never both"
    )


def _raise_arg_retired_choice_is_live(name: str, value: str):
    raise ValueError(
        f'Arg "{name}": retired choice \'{value}\' is also a live choice: '
        f"a value is live or retired, never both"
    )


def _raise_flag_retired_choice_duplicate(name: str, value: str):
    raise ValueError(f'Flag "{name}": retired choice \'{value}\' is declared twice')


def _raise_arg_retired_choice_duplicate(name: str, value: str):
    raise ValueError(f'Arg "{name}": retired choice \'{value}\' is declared twice')


def _raise_flag_retired_choice_message_empty(name: str, value: str):
    raise ValueError(
        f'Flag "{name}": retired choice \'{value}\': message must be a non-empty string'
    )


def _raise_arg_retired_choice_message_empty(name: str, value: str):
    raise ValueError(
        f'Arg "{name}": retired choice \'{value}\': message must be a non-empty string'
    )


def _raise_flag_retired_choices_incompatible_bool(name: str):
    raise ValueError(f'Flag "{name}": retired choices are incompatible with type=bool')


def _raise_arg_retired_choices_incompatible_bool(name: str):
    raise ValueError(f'Arg "{name}": retired choices are incompatible with type=bool')


def _raise_flag_default_is_retired_choice(name: str, value: str):
    raise ValueError(f'Flag "{name}": default \'{value}\' is a retired choice')


def _raise_arg_default_is_retired_choice(name: str, value: str):
    raise ValueError(f'Arg "{name}": default \'{value}\' is a retired choice')


def _raise_flag_retired_choices_require_choices(name: str):
    raise ValueError(f'Flag "{name}": retired choices require choices')


def _raise_arg_retired_choices_require_choices(name: str):
    raise ValueError(f'Arg "{name}": retired choices require choices')


_RETIRED_CHOICE_TEMPLATES = {
    "Flag": (
        _raise_flag_retired_choice_is_live,
        _raise_flag_retired_choice_duplicate,
        _raise_flag_retired_choice_message_empty,
        _raise_flag_retired_choices_incompatible_bool,
        _raise_flag_default_is_retired_choice,
        _raise_flag_retired_choices_require_choices,
    ),
    "Arg": (
        _raise_arg_retired_choice_is_live,
        _raise_arg_retired_choice_duplicate,
        _raise_arg_retired_choice_message_empty,
        _raise_arg_retired_choices_incompatible_bool,
        _raise_arg_default_is_retired_choice,
        _raise_arg_retired_choices_require_choices,
    ),
}


def _validate_retired_choices(
    surface: str,
    name: str,
    retired: tuple["RetiredChoice", ...] | None,
    choices: list | None,
    item_type: type,
    has_default: bool,
    default: object,
) -> None:
    """The registration-time guards, one set over both surfaces.

    Every sentence names the CONCEPT ("retired choice") rather than this
    language's spelling of the declaration, so all three implementations share
    one signature per rule.
    """
    if retired is None:
        return
    (
        is_live, duplicate, message_empty,
        incompatible_bool, default_is_retired, require_choices,
    ) = _RETIRED_CHOICE_TEMPLATES[surface]
    # The bool refusal comes first so the declaration is named by what it got
    # wrong: choices are already incompatible with bool, and reporting the
    # missing choices instead would send a reader to add a declaration the
    # framework would then refuse for the same reason.
    if item_type is bool:
        incompatible_bool(name)
    if choices is None:
        require_choices(name)
    seen: list = []
    for rc in retired:
        formatted = _format_value_for_error(rc.value)
        if not isinstance(rc.message, str) or not rc.message.strip():
            message_empty(name, formatted)
        if rc.value in choices:
            is_live(name, formatted)
        if rc.value in seen:
            duplicate(name, formatted)
        seen.append(rc.value)
    if has_default and default is not None:
        for rc in retired:
            if rc.value == default:
                default_is_retired(name, _format_value_for_error(default))


def _retired_choice_message(
    value: object, retired: tuple["RetiredChoice", ...] | None,
) -> tuple[str, bool]:
    """The message declared for a retired spelling, and whether it is retired.

    Retired lists are short and ordered, so the scan mirrors the choices one
    rather than building a map.
    """
    if retired is None:
        return "", False
    for rc in retired:
        if value == rc.value and type(value) is type(rc.value):
            return rc.message, True
    return "", False


def _raise_choices_entry_not_record(surface: str, name: str, index: int):
    """Message template: a bare `choices=` entry (contract §12.13)."""
    raise ValueError(
        f'{surface} "{name}": choices entry {index} is a bare value: '
        f"declare it as {_RECORD_SPELLING}"
    )


def _raise_choices_entry_is_choice_class(
    surface: str, name: str, index: int, cls_name: str,
):
    """Message template: a `@choice` class reached `choices=` (§12.13).

    Python-only: Go's `Ch` and TypeScript's record literal are distinct types
    from a choice, so the sibling mis-declaration is a compile error.
    """
    raise ValueError(
        f'{surface} "{name}": choices entry {index} is the choice class '
        f"'{cls_name}', which declares a scope: a choice with a scope belongs "
        f"to a choice flag, declared with {_SELECTOR_SPELLING}"
    )


def _raise_choice_magnitude(surface: str, name: str, value: int):
    """Message template: an int choice beyond ±2^53 (contract §12.14).

    The published `value_schema` fragment carries a flag's or arg's choices as
    a JSON Schema `enum`, and a reader that parses JSON numbers as IEEE-754
    doubles -- which every reader of a dumped schema is entitled to be -- reads
    a DIFFERENT integer back. The framework refuses the declaration rather than
    publishing a fragment it already knows will be misread.

    Float choices are deliberately exempt: the canonical float form is by
    construction the shortest string that round-trips to the identical double,
    so nothing is lost there.

    The clause after the colon is reused byte-for-byte from the payload
    regime's own magnitude guard -- the same condition at a second boundary,
    and a second wording for one fact is what the reuse rule prevents.
    """
    raise ValueError(f'{surface} "{name}": choice {value}: {_PDETAIL_MAGNITUDE}')


def _check_choice_magnitudes(surface: str, name: str, values: list) -> None:
    """Run §12.14's guard over one declaration's resolved choice values."""
    for value in values:
        if isinstance(value, bool) or not isinstance(value, int):
            continue
        if abs(value) > _PAYLOAD_MAX_MAGNITUDE:
            _raise_choice_magnitude(surface, name, value)


def _resolve_choice_records(
    surface: str, name: str, entries: object,
) -> tuple[list["Choice"], list]:
    """Validate a `choices=` list of records and split it into (records, values).

    The declaration surface accepts records only; the resolved VALUE list is
    what every downstream value rule (coercion, validation, help, schema, MCP)
    continues to read, unchanged.
    """
    if not isinstance(entries, list) or len(entries) == 0:
        raise ValueError(f'{surface} "{name}": choices must be a non-empty list')
    records: list[Choice] = []
    for i, entry in enumerate(entries):
        if isinstance(entry, Choice):
            records.append(entry)
            continue
        spec = getattr(entry, _CHOICE_SPEC_ATTR, None)
        if spec is not None and isinstance(entry, type):
            _raise_choices_entry_is_choice_class(
                surface, name, i, entry.__name__,
            )
        _raise_choices_entry_not_record(surface, name, i)
    return records, [r.value for r in records]


@dataclass
class Flag:
    """Represents a --flag declaration."""

    name: str
    type: type
    help: str
    short: str | None = None
    # The presence declaration (contract §23). Exactly one of presence= and
    # default= is supplied; __post_init__ resolves the pair into one of
    # "required" / "optional" / "default" and leaves it here.
    presence: object = _MISSING
    default: object = _MISSING
    env: str | None = None
    env_separator: str | None = None
    prefixed: bool = True
    negatable: bool = True
    choices: list | None = None
    validate: Callable | None = None
    repeatable: bool = False
    unique: object = _MISSING
    # Connection-URL binding. connection_url marks this flag as a connection-URL
    # (URL-class) flag; connection_env names the app-level connection env
    # (declared via App(connection_env=...)) it binds to. A URL-class flag MUST
    # bind to a declared connection env (enforced at registration). The binding
    # is hermetic-suppressed, lazily read, no default; the CLI token wins over
    # the env (source "cli" vs "env").
    connection_url: bool = False
    connection_env: str | None = None
    # Per-flag config conflict mode. _MISSING means "inherit the app default".
    # When set explicitly, must be "cli-wins" or "error". Applies to flags only:
    # standalone ConfigFields have no CLI/env conflict surface, and a
    # flag-colliding ConfigField inherits the flag's handling.
    conflict_mode: object = _MISSING
    # Compound type fields (set by __post_init__, not by caller)
    compound: str = "scalar"  # "scalar", "list", or "dict"
    item_type: type | None = None  # for list[T]: the T
    value_type: type | None = None  # for dict[str, T]: the T
    # The clear vocabulary's declaration (contract §27.6): this property of an
    # update command can be CLEARED, which mints `--unset-<prop>`. Legal only
    # on a property of an update (`errNullableNotProperty`). The minted flag
    # delivers no kwarg of its own -- an unset property delivers absence,
    # reports `provided()` true, and is answered by `ctx.unset(name)`.
    nullable: bool = False
    # The declared `choices=` RECORDS (contract §24.2), kept beside the resolved
    # value list so per-entry help survives to help rendering. Set by
    # __post_init__ from `choices`, never by the caller.
    choice_records: tuple["Choice", ...] | None = None
    # The spellings this flag USED to accept, each carrying the message that
    # names its replacement. A retired value is refused at parse time; it is
    # not a choice, so help and the published value_schema never name it.
    retired_choices: list | None = None

    def __post_init__(self) -> None:
        _require_non_empty_str(self.help, "help", "Flag")
        # One function holds every flag-name ban, so the scoped declaration
        # surfaces raise the identical messages at every depth (§24.7).
        _check_flag_name_bans(self.name)

        # The presence declaration, resolved before anything reads `default`.
        # Nothing downstream infers presence from the shape of another
        # declaration any more (contract §23.1, §23.4).
        self.presence = _resolve_presence(
            "Flag", self.name, self.presence, self.default,
        )

        # Parse compound types (list[T], dict[str, T])
        kind, item_t, val_t = _parse_compound_type(
            self.type, f'Flag "{self.name}"',
        )
        self.compound = kind
        self.item_type = item_t
        self.value_type = val_t

        if kind == "list":
            # list[T] normalizes to: type=item_type, repeatable=True
            self.type = self.item_type
            if not self.repeatable:
                self.repeatable = True
            # unique defaults to False for list types if not specified
            if isinstance(self.unique, _MissingSentinel):
                self.unique = False
        elif kind == "dict":
            # dict[str, T] normalizes to: type stays as the original
            # dict[str, T] annotation. The value_type tracks the T.
            # Dict flags are implicitly repeatable (each --flag key=val
            # adds to the dict), but don't use the list-based repeatable
            # machinery. We store self.type as the value_type for coercion
            # dispatch, but keep compound="dict" to distinguish behavior.
            self.type = self.value_type
            # Dict flags cannot be combined with repeatable=True by the user
            if self.repeatable:
                raise ValueError(
                    f'Flag "{self.name}": dict type cannot be combined '
                    f'with repeatable=True'
                )
            # Dict flags cannot have unique
            if not isinstance(self.unique, _MissingSentinel):
                raise ValueError(
                    f'Flag "{self.name}": dict type cannot be combined with unique'
                )
            self.unique = False
            # Dict flags cannot have choices
            if self.choices is not None:
                raise ValueError(
                    f'Flag "{self.name}": dict type cannot be combined with choices'
                )

        # Validate scalar type
        if kind == "scalar" and self.type not in (str, bool, int, float):
            raise ValueError(
                f"Flag.type must be str, bool, int, float, "
                f"list[T], or dict[str, T], got {self.type!r}"
            )
        # Validate repeatable
        if self.repeatable and self.type is bool:
            raise ValueError(f'Flag "{self.name}": repeatable is incompatible with type=bool')
        # Validate unique
        if self.compound != "dict":
            if self.repeatable and isinstance(self.unique, _MissingSentinel):
                raise ValueError(
                    f'Flag "{self.name}": repeatable requires explicit unique '
                    f"(unique=True or unique=False)"
                )
            if not isinstance(self.unique, _MissingSentinel) and self.unique is not True and self.unique is not False:
                raise ValueError(f'Flag "{self.name}": unique must be True or False')
            if (self.unique is True or self.unique is False) and not self.repeatable:
                raise ValueError(f'Flag "{self.name}": unique requires repeatable=True')
            if isinstance(self.unique, _MissingSentinel) and not self.repeatable:
                self.unique = False
        # Validate conflict_mode (per-flag override of the app config conflict mode)
        if not isinstance(self.conflict_mode, _MissingSentinel):
            if self.conflict_mode not in ("cli-wins", "error"):
                raise ValueError(
                    f'Flag "{self.name}": conflict_mode must be "cli-wins" or '
                    f'"error", got {self.conflict_mode!r}'
                )
        # Validate env_separator
        if self.compound == "dict":
            # Dict flags use JSON for env vars, not env_separator
            if self.env_separator is not None:
                raise ValueError(
                    f'Flag "{self.name}": dict type cannot use env_separator '
                    f'(env vars are parsed as JSON)'
                )
        else:
            if self.env_separator is not None and not self.repeatable:
                raise ValueError(f'Flag "{self.name}": env_separator requires repeatable=True')
            if self.env_separator is not None and self.env is None:
                raise ValueError(f'Flag "{self.name}": env_separator requires env')
            if self.repeatable and self.env is not None and self.env_separator is None:
                raise ValueError(
                    f'Flag "{self.name}": repeatable flag with env requires env_separator'
                )
        if self.env_separator is not None and len(self.env_separator) != 1:
            raise ValueError(f'Flag "{self.name}": env_separator must be a single character')
        if self.env_separator == "\\":
            raise ValueError(f'Flag "{self.name}": env_separator cannot be a backslash')
        # Validate choices. Every entry is a record (contract §24.2); the
        # resolved value list is what the rest of the framework reads.
        if self.choices is not None:
            if self.type is bool:
                raise ValueError(f'Flag "{self.name}": choices is incompatible with type=bool')
            records, values = _resolve_choice_records(
                "Flag", self.name, self.choices,
            )
            self.choice_records = tuple(records)
            self.choices = values
            for c in self.choices:
                if not isinstance(c, self.type):
                    raise ValueError(
                        f'Flag "{self.name}": choice {c!r} is not of type {self.type.__name__}'
                    )
            _check_choice_magnitudes("Flag", self.name, self.choices)
        # Validate defaults for dict flags. An explicit `default={}` is a
        # declaration like any other (§23.5's compound row): the "explicit empty
        # default is redundant" refusals died with the silent forced-{} it used
        # to be redundant against.
        if self.compound == "dict":
            if self.presence == _PRESENCE_DEFAULT:
                if not isinstance(self.default, dict):
                    raise ValueError(
                        f'Flag "{self.name}": dict flag default must be a dict'
                    )
                for k, v in self.default.items():
                    if not isinstance(k, str):
                        raise ValueError(
                            f'Flag "{self.name}": dict default key {k!r} '
                            f'must be a string'
                        )
                    _validate_element_type(
                        self.name, self.type, v,
                        f"dict default value for key {k!r}",
                    )
        # Validate repeatable flag defaults (`default=[]` likewise legal)
        elif self.repeatable and self.presence == _PRESENCE_DEFAULT:
            if not isinstance(self.default, list):
                raise ValueError(
                    f'Flag "{self.name}": repeatable flag default must be a list'
                )
            # Validate element types
            type_name = {str: "str", int: "int", float: "float"}[self.type]
            for i, elem in enumerate(self.default):
                if self.type is str:
                    if not isinstance(elem, str):
                        raise ValueError(
                            f'Flag "{self.name}": default element {i} is not of type {type_name}'
                        )
                elif self.type is int:
                    if not isinstance(elem, int) or isinstance(elem, bool):
                        raise ValueError(
                            f'Flag "{self.name}": default element {i} is not of type {type_name}'
                        )
                elif self.type is float:
                    if not isinstance(elem, (int, float)) or isinstance(elem, bool):
                        raise ValueError(
                            f'Flag "{self.name}": default element {i} is not of type {type_name}'
                        )
                    if isinstance(elem, int):
                        self.default[i] = float(elem)
        # Validate default type for int flags
        if self.type is int and self.presence == _PRESENCE_DEFAULT:
            if not self.repeatable and self.compound != "dict" and not isinstance(self.default, int):
                raise ValueError(
                    f'Flag "{self.name}": type=int requires an int default, '
                    f"got {type(self.default).__name__!r}"
                )
        # Validate default type for float flags
        if self.type is float and self.presence == _PRESENCE_DEFAULT:
            if not self.repeatable and self.compound != "dict" and not isinstance(self.default, (int, float)):
                raise ValueError(
                    f'Flag "{self.name}": type=float requires a float default, '
                    f"got {type(self.default).__name__!r}"
                )
        # Retired choices. The entry SHAPE is resolved first, then the rules --
        # after the live choices are validated, so a declaration that got its
        # choices wrong is told that first, and BEFORE the default-in-choices
        # check, so a default naming a retired spelling is answered by the
        # sentence that names the reason.
        if self.retired_choices is not None:
            self.retired_choices = _resolve_retired_choices(
                "Flag", self.name, self.retired_choices,
            )
        _validate_retired_choices(
            "Flag", self.name, self.retired_choices, self.choices,
            self.item_type if self.compound == "list" else self.type,
            self.presence == _PRESENCE_DEFAULT, self.default,
        )
        # Validate default is in choices. The check applies to declared VALUES
        # only: a required or optional flag has no value to check, and absence
        # is never matched against choices (§23.5).
        if self.choices is not None and self.presence == _PRESENCE_DEFAULT:
            if not self.repeatable and self.default not in self.choices:
                raise ValueError(
                    f'Flag "{self.name}": default {self.default!r} is not in choices '
                    f"{self.choices!r}"
                )
        if isinstance(self.negatable, _MissingSentinel):
            self.negatable = self.type is bool
        elif self.type in (str, int, float):
            # negatable is only meaningful for bool flags
            self.negatable = False


@dataclass
class Arg:
    """Represents a positional argument."""

    name: str
    help: str
    # The presence declaration (contract §23.3), same three facts and same
    # one-spelling rule as a flag's. The old `required: bool = True` field is
    # deleted: it was an implicit default, and it spelled one fact across two
    # fields with a guard holding the illegal corner shut.
    presence: object = _MISSING
    default: object = _MISSING
    variadic: bool = False
    type: type = str
    choices: list | None = None
    # Compound type fields (set by __post_init__, not by caller)
    compound: str = "scalar"
    item_type: type | None = None
    # The declared `choices=` records (contract §24.2), same as a flag's.
    choice_records: tuple["Choice", ...] | None = None
    # The arg twin of Flag.retired_choices.
    retired_choices: list | None = None

    def __post_init__(self) -> None:
        _require_non_empty_str(self.help, "help", "Arg")
        if self.name == _RESERVED_CONSENT_PARAM_NAME:
            _raise_arg_name_consent_reserved()
        self.presence = _resolve_presence(
            "Arg", self.name, self.presence, self.default,
        )
        # A variadic arg always delivers a list, so the empty case is spelled
        # `optional` and a default has nothing to mean (§23.3).
        if self.variadic and self.presence == _PRESENCE_DEFAULT:
            _raise_arg_variadic_default(self.name)

        # Parse compound types for args (only list[T] is supported)
        origin = get_origin(self.type)
        if origin is list:
            args = get_args(self.type)
            if not args:
                raise ValueError(
                    f'Arg "{self.name}": list type requires an item type '
                    f'(e.g., list[int]), got bare list'
                )
            if len(args) != 1:
                raise ValueError(
                    f'Arg "{self.name}": list type takes exactly one type '
                    f'argument, got {len(args)}'
                )
            item_t = args[0]
            if item_t not in _NON_BOOL_SCALAR_TYPES:
                raise ValueError(
                    f'Arg "{self.name}": list item type must be str, int, '
                    f'or float, got {item_t!r}'
                )
            if not self.variadic:
                raise ValueError(
                    f'Arg "{self.name}": list type on args requires '
                    f'variadic=True'
                )
            self.compound = "list"
            self.item_type = item_t
            self.type = item_t
        elif origin is dict:
            raise ValueError(
                f'Arg "{self.name}": dict type is not supported on args'
            )
        # Validate type
        elif self.type not in (str, bool, int, float):
            raise ValueError(
                f"Arg.type must be str, bool, int, or float, got {self.type!r}"
            )
        # Validate choices -- record entries, same rule as a flag's (§24.2).
        if self.choices is not None:
            if self.type is bool:
                raise ValueError(
                    f'Arg "{self.name}": choices is incompatible with type=bool'
                )
            records, values = _resolve_choice_records(
                "Arg", self.name, self.choices,
            )
            self.choice_records = tuple(records)
            self.choices = values
            for c in self.choices:
                if not isinstance(c, self.type):
                    raise ValueError(
                        f'Arg "{self.name}": choice {c!r} is not of type '
                        f"{self.type.__name__}"
                    )
            _check_choice_magnitudes("Arg", self.name, self.choices)
        # Validate default type matches declared type. A list arg is always
        # variadic, and a variadic arg cannot declare a default at all, so no
        # list branch exists here any more (§23.3).
        if self.presence == _PRESENCE_DEFAULT:
            if self.type is int:
                if not isinstance(self.default, int) or isinstance(self.default, bool):
                    raise ValueError(
                        f'Arg "{self.name}": type=int requires an int default, '
                        f"got {type(self.default).__name__!r}"
                    )
            elif self.type is float:
                if not isinstance(self.default, (int, float)) or isinstance(self.default, bool):
                    raise ValueError(
                        f'Arg "{self.name}": type=float requires a float default, '
                        f"got {type(self.default).__name__!r}"
                    )
            elif self.type is bool:
                if not isinstance(self.default, bool):
                    raise ValueError(
                        f'Arg "{self.name}": type=bool requires a bool default, '
                        f"got {type(self.default).__name__!r}"
                    )
            elif self.type is str:
                if not isinstance(self.default, str):
                    raise ValueError(
                        f'Arg "{self.name}": type=str requires a str default, '
                        f"got {type(self.default).__name__!r}"
                    )
        # Retired choices, ahead of the default-in-choices check for the reason
        # stated at the flag surface.
        if self.retired_choices is not None:
            self.retired_choices = _resolve_retired_choices(
                "Arg", self.name, self.retired_choices,
            )
        _validate_retired_choices(
            "Arg", self.name, self.retired_choices, self.choices,
            self.item_type if self.compound == "list" else self.type,
            self.presence == _PRESENCE_DEFAULT, self.default,
        )
        # Validate default is in choices -- declared VALUES only (§23.5)
        if self.choices is not None and self.presence == _PRESENCE_DEFAULT:
            if self.default not in self.choices:
                raise ValueError(
                    f'Arg "{self.name}": default {self.default!r} is not in choices '
                    f"{self.choices!r}"
                )


@dataclass
class FlagSet:
    """A reusable bundle of flags."""

    name: str
    flags: list[Flag] = field(default_factory=list)


# ---------------------------------------------------------------------------
# The scoped-selector construct (contract §24)
#
# A choice is a declaration scope. A SELECTOR is a flag that elects exactly one
# of its declared choices, and each choice owns the flags that exist only while
# it is elected. The command is the ROOT scope, which is what makes every rule
# below uniform at every depth.
#
# `MutexGroup` is subsumed by this construct and deleted (§21's box, §24.4):
# "exactly one of these" is member spelling, where each choice is spelled as
# its own flag and no selector token is ever typed.
# ---------------------------------------------------------------------------

#: How a selector is spelled on the command line. Mandatory on every selector,
#: with no default: Python declares closed vocabularies as keyword strings, and
#: the spelling is a decision, never an inference (§24.12).
_ELECT_SELECTOR_TOKEN = "selector-token"
_ELECT_MEMBER_FLAGS = "member-flags"
_ELECT_MODES = (_ELECT_SELECTOR_TOKEN, _ELECT_MEMBER_FLAGS)

# The two names reserved inside every scope, because the delivered record uses
# them: `choice` tags the record and `value` carries a member's own payload
# (§24.7). The record's object form is flat, and that flatness is what makes
# the pair reserved.
_SCOPE_RESERVED_CHOICE = "choice"
_SCOPE_RESERVED_VALUE = "value"


@dataclass(frozen=True)
class _ChoiceDecl:
    """What `@choice` records: the raw declaration, before a selector claims it.

    Field validation is deferred to the selector build, because every message a
    scope raises is prefixed `Choice "<c>" of "<sel>": ` -- a choice name is
    unique only within its selector, so the prefix names both (§12.13).
    """

    name: str
    help: str
    cls: type
    localns: dict
    # The short of a PAYLOAD-LESS member's electing flag (§24.4, §24.12). A
    # payload-carrying member declares its short on `member_value(short=...)`
    # instead -- that field IS its electing flag's declaration -- and a
    # token-spelled choice has no flag of its own to carry one at all.
    short: str | None = None


@dataclass(frozen=True)
class _ChoiceSpec:
    """One choice of one selector: a name, mandatory help, and a scope."""

    name: str
    help: str
    cls: type
    members: tuple[object, ...]  # Flag | _Selector, in declaration order
    payload: Flag | None = None  # member spelling's `value` field, if any
    # The member flag's short, whichever spelling declared it: the one place
    # every reader of a member's short looks (the cross-scope claim table, the
    # `-x` token scan, the help line). None for a token-spelled choice, which
    # is never a token of its own.
    short: str | None = None


@dataclass(frozen=True)
class _Selector:
    """A choice flag: the selector plus the scopes its choices own."""

    name: str
    help: str
    presence: str
    choices: tuple[_ChoiceSpec, ...]
    elect_by: str
    default: object = _MISSING  # an INSTANCE of one of the choice classes
    short: str | None = None
    env: str | None = None

    @property
    def is_member_spelled(self) -> bool:
        return self.elect_by == _ELECT_MEMBER_FLAGS

    def choice_by_name(self, name: str) -> _ChoiceSpec | None:
        for c in self.choices:
            if c.name == name:
                return c
        return None

    def choice_by_class(self, cls: type) -> _ChoiceSpec | None:
        for c in self.choices:
            if c.cls is cls:
                return c
        return None


# --- registration message templates (contract §12.13) ----------------------


def _raise_selector_optional(name: str):
    raise ValueError(
        f'Flag "{name}": a choice flag cannot declare '
        f'{_PRESENCE_SPELLING[_PRESENCE_OPTIONAL]}: an absent selection is a '
        f"choice nobody named, so name it as a choice of its own"
    )


def _raise_selector_no_choices(name: str):
    raise ValueError(
        f'Flag "{name}": a choice flag must declare at least two choices'
    )


def _raise_choice_duplicate_name(sel: str, c: str):
    raise ValueError(f'Flag "{sel}": choice "{c}" is declared twice')


def _raise_choice_help_empty(sel: str, c: str):
    raise ValueError(f'Choice "{c}" of "{sel}": help text is required')


def _raise_selector_default_unknown_choice(sel: str, value: object, names: str):
    raise ValueError(
        f'Flag "{sel}": {_default_spelling(value)} names no declared choice: '
        f"must be one of: {names}"
    )


def _raise_member_selector_short(sel: str):
    raise ValueError(
        f'Flag "{sel}": a member-spelled choice flag is never typed, so it '
        f"cannot carry a short: declare the short on a member"
    )


def _raise_member_default_carries_value(sel: str, c: str):
    raise ValueError(
        f'Flag "{sel}": {_default_spelling_bare()} elects choice "{c}", whose '
        f"flag carries a value nothing supplies: only a payload-less member "
        f"can be a default"
    )


def _raise_token_choice_carries_payload(sel: str, c: str):
    raise ValueError(
        f'Choice "{c}" of "{sel}": a token-spelled choice cannot carry a '
        f"payload: the token names the choice, and a choice that carries its "
        f"own value belongs to a member-spelled choice flag, declared with "
        f"{_MEMBER_SELECTOR_SPELLING}"
    )


def _raise_token_choice_carries_short(sel: str, c: str):
    """Authored: a short on a token-spelled choice has no token to name.

    Under token spelling the choice is named by the selector's own value, and
    a value has no short form -- only a member-spelled choice puts a flag of
    its own on the command line (§24.4).
    """
    raise ValueError(
        f'Choice "{c}" of "{sel}": a token-spelled choice cannot carry a '
        f"short: the token names the choice, and only a member-spelled choice "
        f"has a flag of its own to carry one"
    )


def _raise_member_short_on_payload_choice(sel: str, c: str):
    """Authored: one member, one place to declare its short (§24.4, §24.12).

    A payload-carrying member's electing flag IS its payload declaration, so
    the short belongs there; carrying it on the choice as well would be two
    declarations that must agree.
    """
    raise ValueError(
        f'Choice "{c}" of "{sel}": a payload-carrying member declares its '
        f"short on its payload: {_MEMBER_PAYLOAD_SHORT_SPELLING}"
    )


def _raise_scoped_name_choice_reserved(c: str, sel: str):
    raise ValueError(
        f'Choice "{c}" of "{sel}": flag name \'choice\' is reserved by the '
        f"framework: it tags the delivered record"
    )


def _raise_scoped_name_value_reserved(c: str, sel: str):
    raise ValueError(
        f'Choice "{c}" of "{sel}": flag name \'value\' is reserved by the '
        f"framework: it carries a member-spelled choice's own payload"
    )


def _raise_scoped_name_collides_root(c: str, sel: str, x: str):
    raise ValueError(
        f'Choice "{c}" of "{sel}": flag \'--{x}\' collides with a '
        f"command-level flag of the same name: the scoped one could never be "
        f"reached"
    )


def _raise_scoped_name_collides_selector(c: str, sel: str, x: str):
    raise ValueError(
        f'Choice "{c}" of "{sel}": flag \'--{x}\' collides with the choice '
        f"flag's own name"
    )


def _raise_sibling_scope_shape_mismatch(sel: str, x: str, a: str, b: str):
    raise ValueError(
        f'Flag "{sel}": flag \'--{x}\' is declared by choices "{a}" and "{b}" '
        f"with different value shapes: sibling scopes may reuse a name only "
        f"with an identical type and arity, because tokenizing '--{x}' cannot "
        f"wait for an election"
    )


def _raise_co_electable_name_reuse(name: str, x: str, p1: str, p2: str):
    raise ValueError(
        f'command "{name}": flag \'--{x}\' is declared under \'{p1}\' and '
        f"under '{p2}', which can be elected at the same time: simultaneously "
        f"electable scopes may not reuse a flag name"
    )


def _raise_short_collides_across_scopes(name: str, s: str, a: str, b: str):
    raise ValueError(
        f'command "{name}": short \'-{s}\' is claimed by \'--{a}\' and '
        f"'--{b}', which can be elected at the same time"
    )


def _raise_short_on_ambiguous_election(cmd_name: str, short: str, token: str):
    """Authored: a short reused across sibling scopes cannot name an election.

    Which name a short binds to is decided AFTER the election (§24.7 lets
    sibling scopes reuse a short), and an election token has to be read before
    one has happened.
    """
    raise ValueError(
        f'command "{cmd_name}": short \'-{short}\' is reused by sibling '
        f"scopes and also claimed by '--{token}', which elects: an election "
        f"token is read before any election has happened, so its short cannot "
        f"be shared"
    )


def _raise_short_shape_mismatch(cmd_name: str, short: str, a: str, b: str):
    """Authored: sibling scopes reusing a short must tokenize identically."""
    raise ValueError(
        f'command "{cmd_name}": short \'-{short}\' is claimed by \'--{a}\' and '
        f"'--{b}' with different value shapes: sibling scopes may reuse a "
        f"short only with an identical type and arity, because tokenizing "
        f"'-{short}' cannot wait for an election"
    )


def _raise_scoped_positional(c: str, sel: str):
    raise ValueError(
        f'Choice "{c}" of "{sel}": positional args cannot be declared inside a '
        f"choice scope: a positional's meaning would depend on an election "
        f"that may be typed after it"
    )


def _raise_constraint_references_scoped_flag(
    name: str, c: str, x: str, path: str,
):
    """§12.13, amended by §18.30 item 270: the sentence names the CONSTRAINT.

    `<Family>` had both a value that no longer exists (`CoRequired`) and a
    better replacement, and the trailing clause loses the word `dependency`
    because after §26 the noun for all four kinds is `constraint`.
    """
    raise ValueError(
        f'command "{name}": constraint "{c}" references \'{x}\', which is '
        f"declared under '{path}': constraints operate at root scope only"
    )


# ---------------------------------------------------------------------------
# Constraint registration guards (contract §12.15)
#
# Every one carries the `command "<name>": ` prefix family, and the constraint
# name is in DOUBLE quotes: the catalog quotes by kind of thing rather than by
# category of message, and a constraint name is a declared identifier that no
# invocation ever contains.
# ---------------------------------------------------------------------------

def _raise_constraint_name_charset(name: str, c: str):
    raise ValueError(
        f'command "{name}": constraint name "{c}" must match [a-z][a-z0-9-]*'
    )


def _raise_constraint_name_duplicate(name: str, c: str):
    raise ValueError(f'command "{name}": duplicate constraint name "{c}"')


def _raise_constraint_name_collides(name: str, c: str):
    raise ValueError(
        f'command "{name}": constraint name "{c}" is already a flag or arg '
        f"name: a member reference resolves by name and would be ambiguous"
    )


def _raise_constraint_min_members(name: str, c: str, n: int):
    raise ValueError(
        f'command "{name}": constraint "{c}" must declare at least two '
        f"members, got {n}"
    )


def _raise_constraint_member_not_record(name: str, c: str, i: int):
    """Python and TypeScript only: Go's members are a typed value.

    `errChoicesEntryNotRecord`'s exact discipline one construct over -- a
    spelling that lets one member carry an election and another not carry the
    WORD for it is two spellings for one fact.
    """
    raise ValueError(
        f'command "{name}": constraint "{c}" member {i} is a bare name: '
        f'declare it as Member("<x>")'
    )


def _raise_constraint_member_unknown(name: str, c: str, x: str):
    raise ValueError(
        f'command "{name}": constraint "{c}" references unknown member "{x}"'
    )


def _raise_constraint_member_ambiguous(name: str, c: str, x: str):
    raise ValueError(
        f'command "{name}": constraint "{c}" references "{x}", which names '
        f"both a flag and a positional arg"
    )


def _raise_constraint_member_duplicate(name: str, c: str, x: str):
    raise ValueError(
        f'command "{name}": constraint "{c}" declares member "{x}" twice'
    )


def _raise_constraint_member_required(name: str, c: str, token: str):
    """§26.5's inversion: a member may not declare requiredness.

    One template with one substitution, not a Flag/Arg twin pair -- §12.12's
    twinning rule applies to messages whose PREFIX names a surface, and this
    one's prefix names the constraint. ``token`` is §12.15's member rendering,
    so an arg member reads `member 'targets'`.
    """
    raise ValueError(
        f'command "{name}": constraint "{c}" member \'{token}\' declares '
        f'presence="required": a member the invocation must always supply '
        f"leaves the constraint nothing to decide"
    )


def _raise_constraint_member_bool_when(name: str, c: str, token: str):
    raise ValueError(
        f'command "{name}": constraint "{c}" member \'{token}\' is a bool and '
        f'must declare its election: when="true" counts only a true value, '
        f'when="present" counts any'
    )


def _raise_constraint_when_true_not_bool(
    name: str, c: str, token: str, t: str,
):
    raise ValueError(
        f'command "{name}": constraint "{c}" member \'{token}\' declares '
        f'when="true", which needs a bool; \'{token}\' is a {t}'
    )


def _raise_constraint_when_non_empty_not_sized(
    name: str, c: str, token: str, t: str,
):
    raise ValueError(
        f'command "{name}": constraint "{c}" member \'{token}\' declares '
        f'when="non_empty", which needs a string or a collection; '
        f"'{token}' is a {t}"
    )


def _raise_constraint_nested_when(name: str, c: str, x: str):
    raise ValueError(
        f'command "{name}": constraint "{c}" member "{x}" is a constraint and '
        f"cannot declare an election: a nested constraint is engaged when its "
        f"own members are"
    )


def _raise_constraint_nested_family(name: str, c: str, x: str):
    raise ValueError(
        f'command "{name}": constraint "{c}" references constraint "{x}", '
        f"which declares a one-way dependency rather than a co-occurrence "
        f"rule: only at-least-one and all-or-none can be members of another "
        f"constraint"
    )


def _raise_constraint_cycle(name: str, path: str):
    raise ValueError(f'command "{name}": constraints form a cycle: {path}')


def _raise_constraint_unknown_flag(name: str, c: str, x: str):
    """`Requires` / `Implies` keep the flag noun: their operand vocabulary is
    flags only (§26.13), so an unknown name there is an unknown FLAG."""
    raise ValueError(
        f'command "{name}": constraint "{c}" references unknown flag "{x}"'
    )


def _raise_handler_selector_annotation(
    name: str, param: str, sel: str, union: str, written: str,
):
    """Python-only (§12.13): the handler parameter must annotate the union.

    Without this a developer can annotate one choice class and `assert_never`
    then passes the type checker while silently skipping branches -- the check
    is what makes exhaustiveness SOUND rather than hoped for.
    """
    raise ValueError(
        f'command "{name}": handler parameter \'{param}\' is bound to choice '
        f"flag '--{sel}' and must be annotated {union}, got {written}"
    )


def _raise_handler_kwargs_with_selector(name: str):
    """Python-only (§12.13): a **kwargs handler cannot carry a selector."""
    raise ValueError(
        f'command "{name}": a command declaring a choice flag cannot use a '
        f"**kwargs handler: the elected value must reach a named, annotated "
        f"parameter"
    )


def _raise_handler_annotation_unresolved(name: str, param: str, written: str):
    """Python-only (§12.13): the annotation did not resolve at registration."""
    raise ValueError(
        f'command "{name}": handler parameter \'{param}\' annotation {written} '
        f"cannot be resolved at registration: a choice class must be "
        f"importable at run time, not only under TYPE_CHECKING"
    )


def _default_spelling_bare() -> str:
    """`default=` with no value clause, for messages about the spelling."""
    return "default="


# --- authored Python-only registration guards ------------------------------
# The pinned catalogue covers the conditions all three languages can reach.
# These name mis-declarations only Python's spelling can produce.


def _raise_selector_elect_by_undeclared(name: str):
    raise ValueError(
        f'Flag "{name}": elect_by is undeclared: declare '
        f'elect_by="{_ELECT_SELECTOR_TOKEN}" or '
        f'elect_by="{_ELECT_MEMBER_FLAGS}"'
    )


def _raise_selector_elect_by_invalid(name: str, value: object):
    raise ValueError(
        f'Flag "{name}": elect_by must be "{_ELECT_SELECTOR_TOKEN}" or '
        f'"{_ELECT_MEMBER_FLAGS}", got {value!r}'
    )


def _raise_choices_entry_not_choice_class(name: str, index: int, got: str):
    raise ValueError(
        f'Flag "{name}": choices entry {index} is {got}, not a choice class: '
        f"declare it with @choice(...)"
    )


def _raise_choice_name_charset(sel: str, c: str):
    raise ValueError(
        f'Flag "{sel}": choice name "{c}" must match [a-z][a-z0-9-]*'
    )


def _raise_scope_field_undeclared(c: str, sel: str, fieldname: str):
    raise ValueError(
        f'Choice "{c}" of "{sel}": field \'{fieldname}\' declares no flag: '
        f"declare it with sub_flag(...), sub_choice_flag(...) or "
        f"member_value(...)"
    )


def _raise_member_value_field_name(c: str, sel: str, fieldname: str):
    raise ValueError(
        f'Choice "{c}" of "{sel}": member_value(...) declares the payload on '
        f"field '{fieldname}': a member-spelled choice's payload is delivered "
        f"under the reserved name 'value'"
    )


def _raise_scope_annotation_unresolved(c: str, sel: str, fieldname: str):
    raise ValueError(
        f'Choice "{c}" of "{sel}": the annotation of field \'{fieldname}\' '
        f"cannot be resolved at registration: a choice class must be "
        f"importable at run time, not only under TYPE_CHECKING"
    )


def _raise_scope_selector_annotation(
    c: str, sel: str, fieldname: str, nested: str, union: str, written: str,
):
    raise ValueError(
        f'Choice "{c}" of "{sel}": field \'{fieldname}\' is bound to choice '
        f"flag '--{nested}' and must be annotated {union}, got {written}"
    )


def _raise_selector_default_not_instance(sel: str, got: str):
    raise ValueError(
        f'Flag "{sel}": {_default_spelling_bare()} must be an instance of a '
        f"declared choice class, got {got}"
    )


# --- the declaration surface -----------------------------------------------


def choice(  # noqa: A002 - mirrors the framework keyword
    name: str, *, help: str, short: str | None = None,
):
    """Class decorator: declare one choice of a selector, and its scope.

    The decorated class becomes a **frozen, keyword-only dataclass** -- it is
    both the declaration and the delivered type, with structural equality, a
    useful repr, no mutable-default hazard and no field-ordering rule leaking
    out of ``dataclasses`` (§24.12). Its fields ARE the scope's flags, declared
    with :func:`sub_flag`, :func:`sub_choice_flag` or :func:`member_value`.

    ``short`` declares the short form of a **payload-less member's** electing
    flag: such a choice has no ``member_value`` field, so the decorator is the
    only place its one token is declared. A payload-carrying member declares
    its short on :func:`member_value` instead, and a token-spelled choice
    cannot carry one at all -- both are registration errors (§24.4).

    Validation of the scope is deferred to the selector that claims this class,
    because every message a scope raises names both the choice and the selector.
    """

    def decorator(cls: type) -> type:
        localns = dict(sys._getframe(1).f_locals)
        # A positional declared inside a scope is refused by name, with the
        # message that names both the choice and its selector (§12.13). The
        # marker keeps the dataclass constructible so that message is what the
        # reader sees, rather than `dataclasses`' mutable-default complaint.
        for attr, value in list(vars(cls).items()):
            if isinstance(value, Arg):
                setattr(cls, attr, field(
                    metadata={_SCOPE_FIELD_KEY: {"kind": "positional"}},
                ))
        dc = dataclasses.dataclass(frozen=True, kw_only=True)(cls)
        setattr(dc, _CHOICE_SPEC_ATTR, _ChoiceDecl(
            name=name, help=help, cls=dc, localns=localns, short=short,
        ))
        return dc

    return decorator


def sub_flag(
    *,
    help: str,  # noqa: A002
    presence: object = _MISSING,
    default: object = _MISSING,
    short: str | None = None,
    choices: list | None = None,
    env: str | None = None,
    env_separator: str | None = None,
    prefixed: bool = True,
    repeatable: bool = False,
    unique: object = _MISSING,
    negatable: object = _MISSING,
    conflict_mode: object = _MISSING,
    validate: Callable | None = None,
    nullable: bool = False,
):
    """Declare one flag of a choice's scope, inside the choice's class body.

    Takes **no** ``name=``: the field name IS the flag name (``phone_number``
    -> ``--phone-number``), which is the mapping the framework already uses in
    the other direction for handler parameters (§24.12). The type comes from
    the field's annotation.

    Presence is declared here exactly as it is on a command-level flag
    (§24.1): a scope is not a presence declaration and never supplies one.

    Every composition a command-level flag accepts is accepted here, because
    §24.3 says a scoped flag's env, config and compound behaviour is unaffected
    by the construct: ``env_separator`` is what makes a repeatable or list
    scoped flag with an env binding declarable at all, and ``conflict_mode`` is
    that flag's per-flag override of the app's config conflict mode.
    """
    payload = {
        "kind": "flag",
        "help": help,
        "presence": presence,
        "default": default,
        "short": short,
        "choices": choices,
        "env": env,
        "env_separator": env_separator,
        "prefixed": prefixed,
        "repeatable": repeatable,
        "unique": unique,
        "negatable": negatable,
        "conflict_mode": conflict_mode,
        "validate": validate,
        # Accepted here so the refusal it earns is the one that names the
        # fault: a scoped flag can never be a property of an update (root
        # scope only, §27.3), so `nullable` inside a scope is always
        # `errNullableNotProperty`.
        "nullable": nullable,
    }
    return _scope_field(payload, default)


def sub_choice_flag(
    *,
    help: str,  # noqa: A002
    choices: list[type],
    elect_by: object = _MISSING,
    presence: object = _MISSING,
    default: object = _MISSING,
    short: str | None = None,
    env: str | None = None,
):
    """Declare a NESTED selector inside a choice's scope (§24.1's recursion).

    A selector is a flag, so a selector may be declared inside a choice's scope
    to unlimited depth. "Required exactly when user-facing" stops being a rule a
    handler enforces and becomes where the declaration sits.
    """
    payload = {
        "kind": "selector",
        "help": help,
        "choices": choices,
        "elect_by": elect_by,
        "presence": presence,
        "default": default,
        "short": short,
        "env": env,
    }
    return _scope_field(payload, default)


def _scope_field(payload: dict, default: object):
    """The dataclass field a scope declaration produces.

    A declared collection default is passed through `default_factory`, because
    `dataclasses` refuses an unhashable class attribute -- and a shared mutable
    default is the hazard `@choice`'s frozen dataclass exists to remove.
    """
    meta = {_SCOPE_FIELD_KEY: payload}
    if isinstance(default, _MissingSentinel):
        return field(metadata=meta)
    if isinstance(default, (list, dict, set)):
        snapshot = type(default)(default)
        return field(
            default_factory=lambda: type(snapshot)(snapshot), metadata=meta,
        )
    return field(default=default, metadata=meta)


def member_value(*, help: str, short: str | None = None):  # noqa: A002
    """Declare a member-spelled choice's own payload (§24.4, §24.12).

    A payload is exactly one value, delivered under the reserved name ``value``,
    and only under member spelling. It takes no presence keyword, because
    electing the member supplies it.

    ``short`` is the electing flag's short form (``-r X`` for ``--role X``):
    this field IS that flag's declaration, exactly as ``MemberChoice``'s first
    argument is in Go. It is claimed across every simultaneously live scope
    like any other short, and it renders beside the member on its help line.
    """
    return field(metadata={_SCOPE_FIELD_KEY: {
        "kind": "payload", "help": help, "short": short,
    }})


def choice_flag(
    name: str,
    *,
    help: str,  # noqa: A002
    choices: list[type],
    elect_by: object = _MISSING,
    presence: object = _MISSING,
    default: object = _MISSING,
    short: str | None = None,
    env: str | None = None,
) -> Callable[[F], F]:
    """Attach a selector (a choice flag) to a command handler (§24.12).

    ``elect_by="selector-token"`` spells the selector as a token of its own
    (``--via email``); ``elect_by="member-flags"`` spells each choice as its own
    flag (``--profile work`` / ``--all-profiles``) and no selector token is ever
    typed. The keyword is mandatory and has no default.
    """
    localns = dict(sys._getframe(1).f_locals)
    decl = _make_selector(
        name, help=help, choices=choices, elect_by=elect_by,
        presence=presence, default=default, short=short, env=env,
        localns=localns,
    )

    def decorator(func: F) -> F:
        if not hasattr(func, "_strictcli_flags"):
            func._strictcli_flags = []  # type: ignore[attr-defined]
        func._strictcli_flags.append(decl)  # type: ignore[attr-defined]
        return func

    return decorator


def provided(record: object, name: str) -> bool:
    """Return True when the INVOCATION caused this scoped field's value (§24.9).

    The delivered record answers provided-ness for its OWN fields; the
    context-level accessors deliberately do not see scope interiors, because a
    scoped name is not unique command-wide. Spelled as a function over the
    record rather than as a method, because the record's fields are user-named
    and a method would occupy a name a scope might want.
    """
    sources = getattr(record, _RECORD_SOURCES_ATTR, None)
    if sources is None:
        raise KeyError(f"no source info for flag {name!r}")
    key = name.replace("-", "_")
    if key in sources:
        return sources[key] in _PROVIDED_SOURCES
    if name in sources:
        return sources[name] in _PROVIDED_SOURCES
    raise KeyError(f"no source info for flag {name!r}")


# The `config set` write selection (contract §27.1, §18.33 item 304).
#
# The write is an EXACTLY-ONE SELECTION over a value, a clear and a reset to
# default -- a member-spelled selector (§24.4). The shape it replaces was two
# bools declaring `default=False` plus an optional positional, with three
# hand-rolled guards holding its illegal corners shut, and §27.1's
# mutating-default ban refuses exactly that: a framework cannot ship a
# registration guard its own command does not pass, and an exemption for
# framework-owned commands would be the escape hatch this regime refuses
# everywhere else.
#
# The selection is what the guards used to say. "--clear and --default are
# mutually exclusive", "cannot provide a value with --clear" and "provide a
# value, --clear, or --default" are all unrepresentable now: exactly one member
# is elected, and electing none is the framework's own unsatisfied-selector
# refusal.


@choice("value", help="Write a value at the key")
class _ConfigSetValue:
    value: str = member_value(
        help="Write this value at the key, coerced to the key's own type "
             "(comma-separated for a repeatable flag, backslash-escaping a "
             "literal comma; a JSON object for a dict flag)",
    )


@choice("clear", help="Clear a repeatable flag")
class _ConfigSetClear:
    pass


@choice("default", help="Reset the key to its declared default")
class _ConfigSetDefault:
    pass


_ConfigSetWrite = "_ConfigSetValue | _ConfigSetClear | _ConfigSetDefault"


# --- building the declaration ----------------------------------------------


def _scope_field_name(fieldname: str) -> str:
    """The flag name a scope field declares: the field name, dashed."""
    return fieldname.replace("_", "-")


def _render_union(sel: _Selector) -> str:
    """The declared union, in declaration order, joined by ` | `.

    Nothing else joins it: a selector is never optional (§24.5), so there is no
    `None` member to render.
    """
    return " | ".join(c.cls.__name__ for c in sel.choices)


def _render_annotation(annotation: object) -> str:
    """An annotation as written, for the annotation-mismatch messages."""
    members = _union_members(annotation)
    if members is None:
        return getattr(annotation, "__name__", None) or str(annotation)
    return " | ".join(
        "None" if m is type(None)
        else (getattr(m, "__name__", None) or str(m))
        for m in members
    )


def _union_members(annotation: object) -> list[object] | None:
    origin = get_origin(annotation)
    if origin is typing.Union or origin is _pytypes.UnionType:
        return list(get_args(annotation))
    return None


def _annotation_matches_selector(annotation: object, sel: _Selector) -> bool:
    expected = [c.cls for c in sel.choices]
    members = _union_members(annotation)
    got = members if members is not None else [annotation]
    if len(got) != len(expected):
        return False
    return all(a is b for a, b in zip(got, expected))


def _make_selector(
    name: str,
    *,
    help: str,  # noqa: A002
    choices: object,
    elect_by: object,
    presence: object,
    default: object,
    short: str | None,
    env: str | None,
    localns: dict,
) -> _Selector:
    """Build and fully validate one selector declaration."""
    _require_non_empty_str(help, "help", "Flag")
    # Every existing flag-name ban re-runs on a selector's own name: a selector
    # IS a flag (§24.7). A member-spelled selector's name is never typed, but it
    # is the handler key and the noun help and errors use.
    _check_flag_name_bans(name)

    if isinstance(elect_by, _MissingSentinel):
        _raise_selector_elect_by_undeclared(name)
    if elect_by not in _ELECT_MODES:
        _raise_selector_elect_by_invalid(name, elect_by)

    if not isinstance(choices, list) or len(choices) < 2:
        _raise_selector_no_choices(name)

    resolved_presence = _resolve_presence("Flag", name, presence, default)
    if resolved_presence == _PRESENCE_OPTIONAL:
        _raise_selector_optional(name)

    if elect_by == _ELECT_MEMBER_FLAGS and short is not None:
        _raise_member_selector_short(name)

    specs: list[_ChoiceSpec] = []
    seen: set[str] = set()
    for i, cls in enumerate(choices):
        decl = getattr(cls, _CHOICE_SPEC_ATTR, None) if isinstance(cls, type) else None
        if not isinstance(decl, _ChoiceDecl) or decl.cls is not cls:
            if isinstance(cls, Choice):
                got = "a value record"
            elif isinstance(cls, type):
                got = f"the class '{cls.__name__}'"
            else:
                got = f"the value {_format_value_for_error(cls)!r}"
            _raise_choices_entry_not_choice_class(name, i, got)
        if decl.name in seen:
            _raise_choice_duplicate_name(name, decl.name)
        seen.add(decl.name)
        specs.append(_build_choice_spec(name, decl, elect_by, localns))

    sel = _Selector(
        name=name, help=help, presence=resolved_presence,
        choices=tuple(specs), elect_by=elect_by, default=default,
        short=short, env=env,
    )

    # A scoped flag may not reuse the name of the selector that owns it: it
    # could never be distinguished from the selector's own token (§24.7).
    for spec in specs:
        for m in spec.members:
            mname = m.name
            if mname == name:
                _raise_scoped_name_collides_selector(spec.name, name, mname)

    # Sibling scopes may reuse a name only with an identical VALUE SHAPE: two
    # choices of one selector can never be elected together, so the name is
    # unambiguous at delivery -- but tokenization precedes election, so the
    # token's arity may not depend on the outcome (§24.7, §12.13).
    shapes: dict[str, tuple[str, tuple]] = {}
    for spec in specs:
        for m in spec.members:
            shape = _value_shape(m)
            prev = shapes.get(m.name)
            if prev is not None and prev[1] != shape:
                _raise_sibling_scope_shape_mismatch(
                    name, m.name, prev[0], spec.name,
                )
            if prev is None:
                shapes[m.name] = (spec.name, shape)

    # The default is a complete elected value: a choice INSTANCE. A frozen
    # dataclass cannot be constructed without its required fields, so the
    # incomplete state is unconstructable and there is nothing to check
    # (§24.5 -- this is why Go's and TypeScript's completeness template is
    # Python-excluded).
    if resolved_presence == _PRESENCE_DEFAULT:
        owner = None
        for spec in specs:
            if type(default) is spec.cls:
                owner = spec
                break
        if owner is None:
            if isinstance(default, (str, int, float, bool)):
                names = ", ".join(c.name for c in specs)
                _raise_selector_default_unknown_choice(name, default, names)
            _raise_selector_default_not_instance(
                name, type(default).__name__,
            )
        if elect_by == _ELECT_MEMBER_FLAGS and owner.payload is not None:
            _raise_member_default_carries_value(name, owner.name)

    return sel


def _value_shape(member: object) -> tuple:
    """A declaration's value shape: its type AND arity together (§25.3's noun).

    Two sibling declarations of one name must tokenize identically, which is
    exactly what this tuple captures.
    """
    if isinstance(member, _Selector):
        return ("selector", member.elect_by)
    f: Flag = member  # type: ignore[assignment]
    return (f.compound, f.type, bool(f.repeatable))


def _check_flag_name_bans(name: str) -> None:
    """Every existing flag-name ban, applied at every depth (§24.7, §12.13).

    A ban enforced only against a flat root list is this construct's most
    likely correctness defect, so the bans live in one function that the root
    surface (``Flag.__post_init__``) and every scoped surface both call.
    """
    if name == "force":
        raise ValueError(
            "flag 'force' is a reserved name; use a qualified name "
            "like 'force-overwrite' or 'force-delete'"
        )
    if name in _RESERVED_FRAMEWORK_FLAG_NAMES:
        _raise_flag_name_reserved_by_framework(name)
    if name == _RESERVED_MACHINE_FLAG_NAME:
        _raise_flag_name_json_reserved()
    if name == _RESERVED_CONSENT_PARAM_NAME:
        _raise_flag_name_consent_reserved()
    if name in _BANNED_FLAG_NAMES:
        _raise_flag_name_yes_banned()
    if name.startswith("no-"):
        raise ValueError(
            f"flag '{name}': names starting with 'no-' are "
            f"reserved for the negation system; use a positive "
            f"name instead"
        )


def _build_choice_spec(
    sel_name: str, decl: _ChoiceDecl, elect_by: str, sel_localns: dict,
) -> _ChoiceSpec:
    """Build one choice's scope, validating every rule at this depth."""
    if not isinstance(decl.help, str) or not decl.help.strip():
        _raise_choice_help_empty(sel_name, decl.name)
    if not _IDENTIFIER_RE.fullmatch(decl.name):
        _raise_choice_name_charset(sel_name, decl.name)
    if elect_by == _ELECT_MEMBER_FLAGS:
        # Under member spelling a choice name IS a flag name and inherits every
        # flag-name rule, including the bans (§24.7).
        _check_flag_name_bans(decl.name)

    cls = decl.cls
    ns = dict(sel_localns)
    ns.update(decl.localns)
    try:
        hints = get_type_hints(cls, localns=ns)
    except NameError:
        hints = {}

    members: list[object] = []
    payload: Flag | None = None
    for f in dataclasses.fields(cls):
        meta = f.metadata.get(_SCOPE_FIELD_KEY)
        if meta is None:
            if isinstance(f.default, Arg):
                _raise_scoped_positional(decl.name, sel_name)
            _raise_scope_field_undeclared(decl.name, sel_name, f.name)
        flag_name = _scope_field_name(f.name)
        if meta["kind"] == "positional":
            _raise_scoped_positional(decl.name, sel_name)
        if meta["kind"] == "payload":
            if f.name != _SCOPE_RESERVED_VALUE:
                _raise_member_value_field_name(decl.name, sel_name, f.name)
            if elect_by != _ELECT_MEMBER_FLAGS:
                _raise_token_choice_carries_payload(sel_name, decl.name)
            if f.name not in hints:
                _raise_scope_annotation_unresolved(
                    decl.name, sel_name, f.name,
                )
            payload = Flag(
                name=decl.name, type=hints[f.name], help=meta["help"],
                presence=_PRESENCE_REQUIRED, short=meta["short"],
            )
            continue
        # The two reserved names, checked before anything else looks at the
        # declaration: the delivered record uses them (§24.7).
        if f.name == _SCOPE_RESERVED_CHOICE:
            _raise_scoped_name_choice_reserved(decl.name, sel_name)
        if f.name == _SCOPE_RESERVED_VALUE:
            _raise_scoped_name_value_reserved(decl.name, sel_name)
        if f.name not in hints:
            _raise_scope_annotation_unresolved(decl.name, sel_name, f.name)
        annotation = hints[f.name]
        if meta["kind"] == "flag":
            members.append(Flag(
                name=flag_name,
                type=annotation,
                help=meta["help"],
                presence=meta["presence"],
                default=meta["default"],
                short=meta["short"],
                choices=meta["choices"],
                env=meta["env"],
                env_separator=meta["env_separator"],
                prefixed=meta["prefixed"],
                repeatable=meta["repeatable"],
                unique=meta["unique"],
                negatable=meta["negatable"],
                conflict_mode=meta["conflict_mode"],
                validate=meta["validate"],
                nullable=meta["nullable"],
            ))
            continue
        nested = _make_selector(
            flag_name,
            help=meta["help"], choices=meta["choices"],
            elect_by=meta["elect_by"], presence=meta["presence"],
            default=meta["default"], short=meta["short"], env=meta["env"],
            localns=ns,
        )
        if not _annotation_matches_selector(annotation, nested):
            _raise_scope_selector_annotation(
                decl.name, sel_name, f.name, nested.name,
                _render_union(nested), _render_annotation(annotation),
            )
        members.append(nested)

    # The member's short, resolved to the ONE slot every reader looks at. Which
    # declaration carries it follows the member's shape: a payload-carrying
    # member's electing flag is its `member_value(...)`, a payload-less one has
    # no such field and declares it on `@choice(...)` (§24.4, §24.12).
    if decl.short is not None:
        if elect_by != _ELECT_MEMBER_FLAGS:
            _raise_token_choice_carries_short(sel_name, decl.name)
        if payload is not None:
            _raise_member_short_on_payload_choice(sel_name, decl.name)
    short = decl.short if payload is None else payload.short

    return _ChoiceSpec(
        name=decl.name, help=decl.help, cls=cls,
        members=tuple(members), payload=payload, short=short,
    )


def _check_handler_selector_annotations(
    cmd_name: str,
    handler: Callable,
    selectors: list[_Selector],
    localns: dict,
) -> None:
    """The Python-only mandatory annotation check (§12.13, §24.12)."""
    raw = getattr(handler, "__annotations__", {}) or {}
    try:
        hints = get_type_hints(handler, localns=localns)
    except NameError:
        hints = None
    for sel in selectors:
        param = _flag_param_name(sel.name)
        written = raw.get(param)
        if hints is None or param not in hints:
            if written is None:
                _raise_handler_selector_annotation(
                    cmd_name, param, sel.name, _render_union(sel), "nothing",
                )
            _raise_handler_annotation_unresolved(
                cmd_name, param,
                written if isinstance(written, str)
                else _render_annotation(written),
            )
        if not _annotation_matches_selector(hints[param], sel):
            _raise_handler_selector_annotation(
                cmd_name, param, sel.name, _render_union(sel),
                _render_annotation(hints[param]),
            )


# --- the site table (every token a scoped declaration can accept) ----------


@dataclass(frozen=True)
class _Site:
    """One declared token, plus the election chain that makes it exist.

    ``path`` is the scope path: one ``(selector name, choice name)`` segment per
    election, outermost first. An empty path is the ROOT scope.
    """

    name: str
    kind: str  # "flag" | "selector" | "member"
    path: tuple[tuple[str, str], ...]
    decl: object  # Flag | _Selector
    choice: _ChoiceSpec | None = None  # for kind == "member"

    @property
    def takes_value(self) -> bool:
        if self.kind == "selector":
            return True
        if self.kind == "member":
            return self.choice.payload is not None
        f: Flag = self.decl  # type: ignore[assignment]
        return not (f.type is bool and f.compound == "scalar")

    @property
    def negatable(self) -> bool:
        """Whether `--no-<name>` is a legal token for this site.

        A payload-less member is declined by `--no-<name>` (§21.2, carried over
        into member spelling by §24.4).
        """
        if self.kind == "member":
            return self.choice.payload is None
        if self.kind == "selector":
            return False
        f: Flag = self.decl  # type: ignore[assignment]
        return f.type is bool and f.negatable and f.compound == "scalar"

    @property
    def value_flag(self) -> Flag:
        """The Flag whose value shape this token carries."""
        if self.kind == "member":
            return self.choice.payload
        return self.decl  # type: ignore[return-value]


def _walk_sites(
    members: tuple[object, ...], path: tuple[tuple[str, str], ...],
) -> list[_Site]:
    """Every token reachable below ``members``, in declaration order."""
    sites: list[_Site] = []
    for m in members:
        if isinstance(m, Flag):
            sites.append(_Site(m.name, "flag", path, m))
            continue
        sel: _Selector = m
        if sel.is_member_spelled:
            for c in sel.choices:
                sites.append(_Site(c.name, "member", path, sel, c))
        else:
            sites.append(_Site(sel.name, "selector", path, sel))
        for c in sel.choices:
            sites.extend(_walk_sites(c.members, path + ((sel.name, c.name),)))
    return sites


def _render_scope_path(
    path: tuple[tuple[str, str], ...], member_spelled: dict[str, bool],
) -> str:
    """The pinned scope-path format (§12.13).

    One segment per election on the path, outermost first, joined by a single
    space; a token-spelled segment is ``--<selector> <choice>`` and a
    member-spelled segment is ``--<choice>`` -- the member's own flag, which is
    the only token a reader ever types.
    """
    parts: list[str] = []
    for sel_name, choice_name in path:
        if member_spelled.get(sel_name):
            parts.append(f"--{choice_name}")
        else:
            parts.append(f"--{sel_name} {choice_name}")
    return " ".join(parts)


def _member_spelling_map(selectors: tuple[_Selector, ...]) -> dict[str, bool]:
    """selector name -> whether it is member-spelled, at every depth."""
    out: dict[str, bool] = {}

    def walk(sels: tuple[_Selector, ...]) -> None:
        for s in sels:
            out[s.name] = s.is_member_spelled
            for c in s.choices:
                walk(tuple(m for m in c.members if isinstance(m, _Selector)))

    walk(selectors)
    return out


def _short_claim_path(site: _Site) -> tuple[tuple[str, str], ...]:
    """The scope a site's short is claimed IN.

    For every ordinary declaration that is the site's own path. A member site
    is recorded at the path its selector sits on -- its NAME is a flag name
    command-wide (§24.7) -- but the token it puts on the command line exists
    only while that member is elected, so its short is claimed one segment
    deeper. Two sibling members therefore never collide; what refuses them is
    the election-token guard (§18.19 item 221).
    """
    if site.kind != "member":
        return site.path
    sel: _Selector = site.decl  # type: ignore[assignment]
    return site.path + ((sel.name, site.choice.name),)


def _paths_mutually_exclusive(
    a: tuple[tuple[str, str], ...], b: tuple[tuple[str, str], ...],
) -> bool:
    """True when two scopes can never be live at the same time.

    Stated against SIMULTANEOUSLY ELECTABLE scopes rather than against siblings
    deliberately (§24.7): it is the formulation that still holds if multi-elect
    is ever adopted.
    """
    for (sa, ca), (sb, cb) in zip(a, b):
        if sa != sb:
            return False
        if ca != cb:
            return True
    return False


def _divergent_selector(
    a: tuple[tuple[str, str], ...], b: tuple[tuple[str, str], ...],
) -> tuple[str, str, str] | None:
    """The (selector, choice a, choice b) where two sibling paths diverge."""
    for (sa, ca), (sb, cb) in zip(a, b):
        if sa != sb:
            return None
        if ca != cb:
            return (sa, ca, cb)
    return None


def _validate_scoped_names(
    cmd_name: str,
    root_flags: list[Flag],
    selectors: list[_Selector],
    global_flags: list[Flag] | None,
    sites: list[_Site],
) -> None:
    """The name and short collision rules (§24.7, §12.13)."""
    member_spelled = _member_spelling_map(tuple(selectors))
    # Every token the ROOT scope owns: command flags, global flags, a
    # token-spelled selector's own token, and a member-spelled selector's
    # member flags.
    root_names = {f.name for f in root_flags}
    if global_flags:
        root_names |= {gf.name for gf in global_flags}
    root_tokens: list[str] = [f.name for f in root_flags]
    for site in sites:
        if not site.path:
            root_tokens.append(site.name)
    seen_root: set[str] = set()
    for token in root_tokens:
        if token in seen_root:
            raise ValueError(
                f'command "{cmd_name}": duplicate flag name "{token}"'
            )
        seen_root.add(token)
    root_names |= seen_root

    scoped = [s for s in sites if s.path]
    for site in scoped:
        owner_sel, owner_choice = site.path[-1]
        if site.name in root_names:
            _raise_scoped_name_collides_root(owner_choice, owner_sel, site.name)
        # A scoped flag may not reuse the name of any selector on its own path:
        # that token is always live wherever this one is.
        for sel_name, _choice in site.path:
            if site.name == sel_name:
                _raise_scoped_name_collides_selector(
                    owner_choice, owner_sel, site.name,
                )

    by_name: dict[str, list[_Site]] = {}
    for site in scoped:
        by_name.setdefault(site.name, []).append(site)
    for token, group in by_name.items():
        for i in range(len(group)):
            for j in range(i + 1, len(group)):
                a, b = group[i], group[j]
                if _paths_mutually_exclusive(a.path, b.path):
                    continue
                _raise_co_electable_name_reuse(
                    cmd_name, token,
                    _render_scope_path(a.path, member_spelled),
                    _render_scope_path(b.path, member_spelled),
                )

    # Shorts are claimed across every simultaneously live scope; sibling scopes
    # may reuse one (§24.7).
    shorts: list[tuple[str, _Site]] = []
    for site in sites:
        if site.kind == "member":
            short = site.choice.short
        else:
            short = getattr(site.decl, "short", None)
        if short:
            shorts.append((short, site))
    scoped_short_count = len(shorts)  # noqa: F841 -- read below
    for f in root_flags:
        if f.short:
            shorts.append((f.short, _Site(f.name, "flag", (), f)))
    for i in range(len(shorts)):
        for j in range(i + 1, len(shorts)):
            if i >= scoped_short_count:
                # Two root flags: pre-existing territory this round does not
                # start policing.
                continue
            (s1, a), (s2, b) = shorts[i], shorts[j]
            if s1 != s2 or a.name == b.name:
                continue
            if _paths_mutually_exclusive(
                _short_claim_path(a), _short_claim_path(b),
            ):
                continue
            _raise_short_collides_across_scopes(cmd_name, s1, a.name, b.name)


# ---------------------------------------------------------------------------
# The constraint system (contract §26)
#
# Four kinds, one container. The two CO-OCCURRENCE families are predicates over
# a member list -- at-least-one and all-or-none -- and the two dependency rules
# keep the semantics they always had. `CoRequired` is DELETED by rename
# (§26.1): all-or-none absorbs it, there is no alias and no deprecation period.
#
# at-least-one is NOT exclusivity. It has no upper bound and never refuses a
# second member; nothing in this file may describe it with §21.4's vocabulary.
# ---------------------------------------------------------------------------

# The closed election vocabulary a member declares (§26.3). `present` is the
# default for every type; a BOOL member must declare one of the two explicitly,
# because `present` on a bool would let `--no-x` engage a constraint while
# selecting nothing -- the shipped-dangerous class A1, arriving by omission.
_WHEN_PRESENT = "present"
_WHEN_TRUE = "true"
_WHEN_NON_EMPTY = "non_empty"
_WHEN_VALUES = (_WHEN_PRESENT, _WHEN_TRUE, _WHEN_NON_EMPTY)

# The resolved member kinds published in the schema (§25.7's amendment).
_MEMBER_KIND_FLAG = "flag"
_MEMBER_KIND_ARG = "arg"
_MEMBER_KIND_CONSTRAINT = "constraint"


@dataclass(frozen=True)
class Member:
    """One operand of a co-occurrence constraint, referenced BY NAME (§26.2).

    The name resolves in one namespace -- the command's flags, its positional
    args and its other named constraints -- and a member declares no presence
    and no help of its own: the declaration it refers to carries every fact
    about the value.

    ``when`` is the election selector (§26.3). Left unwritten it means
    ``"present"`` for every type except ``bool``, where omitting it is a
    registration error. The field keeps the declaration AS WRITTEN (``None``
    for "not declared") so the bool refusal can tell an omission from an
    explicit ``when="present"``; registration resolves it.
    """

    name: str
    when: str | None = None

    def __post_init__(self) -> None:
        if not isinstance(self.name, str) or not self.name:
            raise ValueError("Member name must be a non-empty string")
        if self.when is not None and self.when not in _WHEN_VALUES:
            # Authored, not pinned by §12.15: the pinned `when` guards all name
            # a declared value that cannot apply to the member's TYPE, and none
            # of them describes a typo in the keyword's own vocabulary. Python
            # alone can write one, `when=` being a keyword taking a string.
            raise ValueError(
                f'Member "{self.name}": when must be "present", "true" or '
                f'"non_empty", got {self.when!r}'
            )

    @property
    def resolved_when(self) -> str:
        """The declared selector, or the uniform default (§26.3)."""
        return self.when if self.when is not None else _WHEN_PRESENT


def _normalize_members(constraint: str, members: object) -> tuple:
    """Freeze a declared member sequence, leaving non-records intact.

    A bare string is NOT rejected here: `errConstraintMemberNotRecord` names
    the command and the member's index, and neither is known at construction.
    """
    if isinstance(members, (str, bytes)) or not isinstance(members, Iterable):
        raise ValueError(
            f'constraint "{constraint}": members must be a list of '
            f"{Member.__name__} records"
        )
    return tuple(members)


@dataclass(frozen=True)
class AtLeastOne:
    """At least one member must be ENGAGED (§26.1).

    Members may co-occur: engaging two, or all, satisfies it exactly as
    engaging one does. With nothing engaged it is violated, which is the whole
    of what it says.
    """

    name: str
    members: tuple

    def __post_init__(self) -> None:
        object.__setattr__(
            self, "members", _normalize_members(self.name, self.members),
        )


@dataclass(frozen=True)
class AllOrNone:
    """Either every member is engaged or none is (§26.1).

    With nothing engaged it is VACUOUSLY TRUE -- that is the "none" half of its
    own name, not a loophole. This family absorbs the deleted ``CoRequired``.
    """

    name: str
    members: tuple

    def __post_init__(self) -> None:
        object.__setattr__(
            self, "members", _normalize_members(self.name, self.members),
        )


@dataclass(frozen=True)
class Requires:
    """Flag that depends on another flag being present.

    Semantics are untouched by §26 (§26.13): the predicate is §23.6's
    *provided*, the operand vocabulary is flags only, by name, at root scope --
    no args, no nesting, no ``when``. What it joins is the mandatory name, the
    ``constraints=`` container, the ``constraint "<c>": `` prefix, the help
    block and the schema encoding.
    """

    name: str
    flag: str
    depends_on: str


@dataclass(frozen=True)
class Implies:
    """When a trigger flag is provided, automatically set a target flag to a value.

    Semantics untouched by §26 (§26.13), including the injection order: the
    implication resolves BEFORE the co-occurrence families, so an implied value
    can engage a member.
    """

    name: str
    flag: str       # trigger flag name
    implies: str    # target flag name
    value: bool     # value to set on target when trigger is present


# The two families whose members are a list, and which may be nested.
_CO_OCCURRENCE_FAMILIES = (AtLeastOne, AllOrNone)
_CONSTRAINT_FAMILIES = (AtLeastOne, AllOrNone, Requires, Implies)


_CONSTRAINT_NAME_RE = re.compile(r"^[a-z][a-z0-9-]*$")


def _declared_type_word(decl: object) -> str:
    """strictcli's own type word for a flag or arg (§12.15's `<t>`).

    Never a Python type name: the compound spellings are the ones the existing
    type errors already use, so the sentence reads the same in all three
    implementations. The vocabulary is closed (§18.31 item 289): the four
    scalar words, `list[<elem>]`, the ONE-argument `dict[<value>]` (a dict's
    key type is `str` by construction, so a two-argument word would state a
    fact no declaration can vary), and `choice flag`.
    """
    if isinstance(decl, _Selector):
        # Its value is a record that neither `true` nor `non_empty` can test,
        # so the word names the CONSTRUCT: `choice` alone is the dump's enum
        # spelling and would read here as a value word for a value that does
        # not exist.
        return "choice flag"
    compound = getattr(decl, "compound", "scalar")
    if compound == "dict":
        value_type = getattr(decl, "value_type", None) or str
        return f"dict[{value_type.__name__}]"
    if compound == "list" or getattr(decl, "repeatable", False):
        item = getattr(decl, "item_type", None) or decl.type
        return f"list[{item.__name__}]"
    if getattr(decl, "variadic", False):
        return f"list[{decl.type.__name__}]"
    return decl.type.__name__


def _member_is_sized(decl: object) -> bool:
    """Can `when="non_empty"` be evaluated against this declaration (§26.3)?

    A string or a collection can; a bool, an int or a float cannot -- a
    selector that cannot be evaluated against the declared type is a
    mis-declaration, not a no-op. A choice flag's value is a record, so
    neither election selector has anything to test on it (§26.2).
    """
    if isinstance(decl, _Selector):
        return False
    if getattr(decl, "compound", "scalar") != "scalar":
        return True
    if getattr(decl, "repeatable", False) or getattr(decl, "variadic", False):
        return True
    return decl.type is str


def _validate_constraint_set(
    name: str,
    constraints: list,
    all_flags: list,
    all_args: list,
    scoped_paths: dict[str, str],
    selectors: list | None = None,
) -> None:
    """§26.8's resolution order, pinned because three implementations must
    report the same first error for a declaration with two faults.

    The order runs from the constraint's own identity outward to the
    declarations it names, so a message never blames a member for a fault in
    the constraint that names it.

    A token-spelled root selector is an ordinary root-scope flag here (§26.2):
    it resolves as a member, collides with a constraint name like any other
    flag name, and `present` is the only election legal on it -- its value is
    a record that neither `true` nor `non_empty` can test.
    """
    if not constraints:
        return
    flags_by_name = {f.name: f for f in all_flags}
    for sel in selectors or ():
        if not sel.is_member_spelled:
            flags_by_name[sel.name] = sel
    args_by_name = {a.name: a for a in all_args}

    # 1. Name legality -- charset, duplicates, collision with a flag or arg.
    seen_names: set[str] = set()
    for c in constraints:
        if not isinstance(c, _CONSTRAINT_FAMILIES):
            raise ValueError(
                f'command "{name}": constraints must be AtLeastOne, '
                f"AllOrNone, Requires or Implies declarations, got "
                f"{type(c).__name__!r}"
            )
        if not isinstance(c.name, str) or not _CONSTRAINT_NAME_RE.match(c.name):
            _raise_constraint_name_charset(name, c.name)
        if c.name in seen_names:
            _raise_constraint_name_duplicate(name, c.name)
        seen_names.add(c.name)
        if c.name in flags_by_name or c.name in args_by_name:
            _raise_constraint_name_collides(name, c.name)

    co_occurrence = [c for c in constraints if isinstance(c, _CO_OCCURRENCE_FAMILIES)]
    by_name = {c.name: c for c in constraints}

    # Member record shape -- a SET-WIDE step between steps 1 and 2. A bare name
    # is a fault in how a member is WRITTEN rather than in what it names, so it
    # is refused across the whole set before any constraint's members are
    # counted or resolved: a spelling that lets one member carry an election and
    # another not carry the word for it is two spellings for one fact.
    for c in co_occurrence:
        for i, m in enumerate(c.members):
            if not isinstance(m, Member):
                _raise_constraint_member_not_record(name, c.name, i)

    # 2. Member arity -- at least two members.
    for c in co_occurrence:
        if len(c.members) < 2:
            _raise_constraint_min_members(name, c.name, len(c.members))

    # 3. Member resolution -- each name resolves to exactly one flag, arg or
    #    constraint. Every member is a record by now: the shape step above ran
    #    over the whole set.
    for c in co_occurrence:
        seen_members: set[str] = set()
        for m in c.members:
            is_flag = m.name in flags_by_name
            is_arg = m.name in args_by_name
            if is_flag and is_arg:
                _raise_constraint_member_ambiguous(name, c.name, m.name)
            if not (is_flag or is_arg or m.name in by_name
                    or m.name in scoped_paths):
                _raise_constraint_member_unknown(name, c.name, m.name)
            if m.name in seen_members:
                _raise_constraint_member_duplicate(name, c.name, m.name)
            seen_members.add(m.name)
    for c in constraints:
        if isinstance(c, Requires):
            operands = (c.flag, c.depends_on)
        elif isinstance(c, Implies):
            operands = (c.flag, c.implies)
        else:
            continue
        for x in operands:
            if x not in flags_by_name and x not in scoped_paths:
                _raise_constraint_unknown_flag(name, c.name, x)

    # 4. Scope -- a resolved flag declared inside a choice scope refuses
    #    (§24.8): the scope already IS the constraint, and expressing one fact
    #    in two mechanisms is how the two disagree later.
    for c in constraints:
        if isinstance(c, _CO_OCCURRENCE_FAMILIES):
            operand_names = [m.name for m in c.members]
        elif isinstance(c, Requires):
            operand_names = [c.flag, c.depends_on]
        else:
            operand_names = [c.flag, c.implies]
        for x in operand_names:
            path = scoped_paths.get(x)
            if path is not None:
                _raise_constraint_references_scoped_flag(name, c.name, x, path)

    # 5. Nesting legality -- a nested member is a co-occurrence constraint,
    #    carries no `when`, and the reference graph is acyclic.
    for c in co_occurrence:
        for m in c.members:
            if m.name not in by_name:
                continue
            nested = by_name[m.name]
            if not isinstance(nested, _CO_OCCURRENCE_FAMILIES):
                _raise_constraint_nested_family(name, c.name, m.name)
            if m.when is not None:
                _raise_constraint_nested_when(name, c.name, m.name)
    _check_constraint_cycles(name, co_occurrence, by_name)

    # 6. Election legality -- `when` against the member's declared type,
    #    including the bool refusal. `present` on a bool would let `--no-x`
    #    engage a constraint while selecting nothing (A1, by omission).
    for c in co_occurrence:
        for m in c.members:
            if m.name in by_name:
                continue
            decl = flags_by_name.get(m.name) or args_by_name[m.name]
            token = (
                f"--{m.name}" if m.name in flags_by_name else m.name
            )
            is_bool = (
                getattr(decl, "type", None) is bool
                and getattr(decl, "compound", "scalar") == "scalar"
                and not getattr(decl, "repeatable", False)
                and not getattr(decl, "variadic", False)
            )
            if is_bool and m.when is None:
                _raise_constraint_member_bool_when(name, c.name, token)
            if m.when == _WHEN_TRUE and not is_bool:
                _raise_constraint_when_true_not_bool(
                    name, c.name, token, _declared_type_word(decl),
                )
            if m.when == _WHEN_NON_EMPTY and not _member_is_sized(decl):
                _raise_constraint_when_non_empty_not_sized(
                    name, c.name, token, _declared_type_word(decl),
                )

    # 7. Presence legality -- §26.5's inversion. A constraint never subtracts
    #    from a declaration; it adds a rule on top of one, and a member the
    #    invocation must always supply leaves it nothing to decide.
    for c in co_occurrence:
        for m in c.members:
            if m.name in by_name:
                continue
            decl = flags_by_name.get(m.name) or args_by_name[m.name]
            if decl.presence == _PRESENCE_REQUIRED:
                token = f"--{m.name}" if m.name in flags_by_name else m.name
                _raise_constraint_member_required(name, c.name, token)

    # The dependency families' own guards, whose sentences §26 leaves alone.
    for c in constraints:
        if isinstance(c, Requires):
            if c.flag == c.depends_on:
                raise ValueError(
                    f'command "{name}": Requires flag and depends_on cannot be '
                    f'the same ("{c.flag}")'
                )
        elif isinstance(c, Implies):
            if c.flag == c.implies:
                raise ValueError(
                    f'command "{name}": Implies flag and implies cannot be '
                    f'the same ("{c.flag}")'
                )
            if getattr(flags_by_name[c.flag], "type", None) is not bool:
                raise ValueError(
                    f'command "{name}": Implies trigger flag "{c.flag}" '
                    f"must be a bool flag"
                )
            if getattr(flags_by_name[c.implies], "type", None) is not bool:
                raise ValueError(
                    f'command "{name}": Implies target flag "{c.implies}" '
                    f"must be a bool flag"
                )
            if not isinstance(c.value, bool):
                raise ValueError(
                    f'command "{name}": Implies value must be a bool, '
                    f"got {type(c.value).__name__!r}"
                )


def _check_constraint_cycles(name: str, co_occurrence: list, by_name: dict) -> None:
    """Nesting is a DAG, cycle-checked at registration (§26.2).

    `<path>` renders the participating names joined by ` -> `, starting AND
    ending at the same name, beginning at the first participant in declaration
    order. A constraint naming itself is the degenerate case and takes the same
    template, never a second one.
    """
    WHITE, GREY, BLACK = 0, 1, 2
    colour: dict[str, int] = {}
    stack: list[str] = []
    order = {cname: i for i, cname in enumerate(by_name)}

    def walk(c) -> None:
        colour[c.name] = GREY
        stack.append(c.name)
        for m in c.members:
            child = by_name.get(m.name)
            if child is None or not isinstance(child, _CO_OCCURRENCE_FAMILIES):
                continue
            state = colour.get(child.name, WHITE)
            if state == GREY:
                # The participant the walk ENTERED the cycle through is not
                # necessarily the one declared first, so the cycle is rotated
                # onto the earliest-declared one before it renders.
                cycle = stack[stack.index(child.name):]
                open_at = min(range(len(cycle)), key=lambda i: order[cycle[i]])
                rotated = cycle[open_at:] + cycle[:open_at]
                path = " -> ".join(rotated + [rotated[0]])
                _raise_constraint_cycle(name, path)
            if state == WHITE:
                walk(child)
        stack.pop()
        colour[c.name] = BLACK

    for c in co_occurrence:
        if colour.get(c.name, WHITE) == WHITE:
            walk(c)


def _constraint_index(cmd: "Command") -> tuple[dict, dict]:
    """(kind by name, constraint by name) for a validated command.

    Registration guarantees the namespace is total and unambiguous -- a name
    resolves to exactly one flag, arg or constraint -- so one flat map answers
    every member lookup every surface below needs.
    """
    kinds: dict[str, str] = {}
    for f in cmd.flags:
        kinds[f.name] = _MEMBER_KIND_FLAG
    # A token-spelled root selector is an ordinary root-scope flag as a member
    # (§26.2), so it renders and projects as one: `--via` where a reader types
    # tokens, `via` where a caller writes keys.
    for sel in cmd.selectors:
        if not sel.is_member_spelled:
            kinds[sel.name] = _MEMBER_KIND_FLAG
    for a in cmd.args:
        kinds[a.name] = _MEMBER_KIND_ARG
    by_name: dict[str, object] = {}
    for c in cmd.constraints:
        kinds[c.name] = _MEMBER_KIND_CONSTRAINT
        by_name[c.name] = c
    return kinds, by_name


def _render_member_token(
    member_name: str, kinds: dict, by_name: dict, cli: bool,
) -> str:
    """One member, rendered by §12.15's pinned rule.

    A flag renders `--<name>`, an arg renders its bare `<name>`, and a nested
    constraint renders its OWN operands in parentheses, joined by its family's
    connector. The rendering is structural, never nominal: a nested member
    never renders its name, because the name identifies the rule that failed
    and appears once, in the prefix.

    ``cli`` picks the token vocabulary: command-line tokens for help and for
    the parse-time sentences, property names for the MCP description block
    (§26.12 -- the caller writes keys, not argv).
    """
    kind = kinds.get(member_name)
    if kind == _MEMBER_KIND_FLAG:
        return f"--{member_name}" if cli else _flag_param_name(member_name)
    if kind == _MEMBER_KIND_ARG:
        return member_name
    nested = by_name[member_name]
    connector = " with " if isinstance(nested, AllOrNone) else " or "
    inner = connector.join(
        _render_member_token(m.name, kinds, by_name, cli)
        for m in nested.members
    )
    return f"({inner})"


def _render_member_list(
    constraint: object, kinds: dict, by_name: dict, cli: bool,
) -> str:
    """The whole member list, unquoted and joined by `, ` in declaration order."""
    return ", ".join(
        _render_member_token(m.name, kinds, by_name, cli)
        for m in constraint.members
    )


def _msg_at_least_one_required(c: str, members: str, clause: str) -> str:
    return f'constraint "{c}": at least one of {members} is required{clause}'


def _msg_all_or_none_together(c: str, members: str) -> str:
    return f'constraint "{c}": {members} must be used together'


def _msg_constraint_prefix(c: str) -> str:
    """The prefix every constraint sentence carries (§12.15).

    `Requires` and `Implies` keep their sentences byte for byte AFTER it.
    """
    return f'constraint "{c}": '


def _is_non_empty_value(value: object) -> bool:
    """`non_empty` fires on a non-empty string, list or map (§26.3)."""
    if value is None:
        return False
    if isinstance(value, (str, list, tuple, dict)):
        return len(value) > 0
    return True


def _arg_engagement_inputs(
    cmd: "Command",
    positionals: list[str],
    pre_typed_args: dict[str, object] | None,
) -> dict[str, tuple[bool, object]]:
    """Provided-ness and the raw value of every positional arg (§26.3).

    The flag-side sourced store holds flags only, so this is the arg side of
    the one `provided` predicate, and it must give the same answer at the argv
    door and at both machine doors. An arg is provided when the invocation
    supplied a positional token for it, or a key for it at a machine door, and
    never when the declaration's default or an optional absence filled it. For
    a VARIADIC arg, provided means at least one element -- which is why an
    explicitly supplied empty array is not a provision.

    It runs BEFORE step 6, so the values here are still raw argv tokens on the
    argv path; nothing is coerced, because a coercion failure is step 6's
    error to raise, in step 6's place.
    """
    out: dict[str, tuple[bool, object]] = {}
    if not cmd.args:
        return out
    if pre_typed_args is not None:
        for a in cmd.args:
            if a.name not in pre_typed_args:
                out[a.name] = (False, None)
                continue
            value = pre_typed_args[a.name]
            if a.variadic:
                supplied = isinstance(value, (list, tuple)) and len(value) > 0
                out[a.name] = (supplied, value)
            else:
                out[a.name] = (True, value)
        return out
    has_variadic = bool(cmd.args) and cmd.args[-1].variadic
    fixed_args = cmd.args[:-1] if has_variadic else cmd.args
    for idx, a in enumerate(fixed_args):
        if idx < len(positionals):
            out[a.name] = (True, positionals[idx])
        else:
            out[a.name] = (False, None)
    if has_variadic:
        remaining = positionals[len(fixed_args):]
        out[cmd.args[-1].name] = (len(remaining) > 0, remaining)
    return out


def _member_engaged(
    member: Member,
    kinds: dict,
    by_name: dict,
    store: "_SourcedStore",
    arg_inputs: dict[str, tuple[bool, object]],
    selector_engaged: dict[str, bool],
) -> bool:
    """Is this member engaged in this invocation (§26.4)?

    A flag or arg member is engaged iff its `when` selector fires; a nested
    constraint member is engaged iff at least one of ITS members is. Engagement
    propagates upward; satisfaction does not.

    A selector member carries no value in the flag store: it is engaged when
    the INVOCATION elected it and not when a default election did (§26.2),
    which is the same predicate every other member takes -- the declaration
    deciding is never a provision.
    """
    kind = kinds.get(member.name)
    if kind == _MEMBER_KIND_CONSTRAINT:
        nested = by_name[member.name]
        return any(
            _member_engaged(
                m, kinds, by_name, store, arg_inputs, selector_engaged,
            )
            for m in nested.members
        )
    when = member.resolved_when
    if member.name in selector_engaged:
        return selector_engaged[member.name]
    if kind == _MEMBER_KIND_FLAG:
        if not store.is_present_for_deps(member.name):
            return False
        if when == _WHEN_PRESENT:
            return True
        value = store[member.name]
        if when == _WHEN_TRUE:
            return value is True
        return _is_non_empty_value(value)
    provided, value = arg_inputs.get(member.name, (False, None))
    if not provided:
        return False
    if when == _WHEN_PRESENT:
        return True
    if when == _WHEN_TRUE:
        # The argv door has a raw token here; the machine doors have the
        # caller's own bool. A token that does not parse is not engaged, and
        # step 6 raises the real coercion error in its own place.
        if isinstance(value, bool):
            return value is True
        if isinstance(value, str):
            try:
                return _strict_bool(value) is True
            except ValueError:
                return False
        return False
    return _is_non_empty_value(value)


def _constraint_decline_clause(
    constraint: object,
    kinds: dict,
    store: "_SourcedStore",
) -> str:
    """§21.4's decline clause verbatim, when a `when="true"` bool declined.

    Reusing it is not a claim that at-least-one is exclusivity: the clause is
    about a NEGATED BOOL, which is the same fact under both constructs. There
    is deliberately no analogous clause for a `non_empty` member supplied
    empty -- A2 places empty-value legality on the flag's own validation.
    """
    for m in constraint.members:
        if kinds.get(m.name) != _MEMBER_KIND_FLAG:
            continue
        if m.resolved_when != _WHEN_TRUE:
            continue
        if not store.is_present_for_deps(m.name):
            continue
        if store[m.name] is False:
            return _msg_mutex_decline_clause(m.name)
    return ""


def _enforce_constraints(
    cmd: "Command",
    store: "_SourcedStore",
    arg_inputs: dict[str, tuple[bool, object]],
    selector_engaged: dict[str, bool] | None = None,
) -> None:
    """Evaluate every constraint, children before parents (§26.4, §26.9).

    Siblings run in declaration order and a violated nested constraint reports
    its own sentence, its parent never being evaluated. That is not a tie-break
    convention: an operator who typed one half of a pair must be told the pair
    is incomplete, not that the whole selection is missing.

    ``selector_engaged`` carries the elections a selector member reads, keyed
    by the selector's own name: True when the invocation elected it, False
    when the declaration's default did (§26.2).
    """
    if not cmd.constraints:
        return
    elections = selector_engaged or {}
    kinds, by_name = _constraint_index(cmd)
    evaluated: set[str] = set()

    def provided(x: str) -> bool:
        """The one `provided` predicate, over a flag or a selector."""
        if x in elections:
            return elections[x]
        return store.is_present_for_deps(x)

    def visit(c: object) -> None:
        if c.name in evaluated:
            return
        evaluated.add(c.name)
        for m in c.members:
            if kinds.get(m.name) == _MEMBER_KIND_CONSTRAINT:
                visit(by_name[m.name])
        engaged = [
            _member_engaged(m, kinds, by_name, store, arg_inputs, elections)
            for m in c.members
        ]
        if isinstance(c, AtLeastOne):
            if not any(engaged):
                raise _ParseError(_msg_at_least_one_required(
                    c.name,
                    _render_member_list(c, kinds, by_name, cli=True),
                    _constraint_decline_clause(c, kinds, store),
                ))
            return
        # all-or-none: every member engaged or none. Nothing engaged is
        # VACUOUSLY TRUE -- the "none" half of its own name.
        if any(engaged) and not all(engaged):
            raise _ParseError(_msg_all_or_none_together(
                c.name, _render_member_list(c, kinds, by_name, cli=True),
            ))

    for c in cmd.constraints:
        if isinstance(c, _CO_OCCURRENCE_FAMILIES):
            visit(c)
        elif isinstance(c, Requires):
            if provided(c.flag) and not provided(c.depends_on):
                raise _ParseError(
                    _msg_constraint_prefix(c.name)
                    + f"flag '--{c.flag}' requires '--{c.depends_on}'"
                )


# ---------------------------------------------------------------------------
# The update-command construct (contract §27)
#
# An update command declares ONE record -- the resource it changes, the write
# mode it changes it under, the declarations that name WHICH instance, and the
# declarations that name WHAT changes -- and the framework derives from it the
# mutating-default ban (§27.1), the at-least-one-property rule (§27.4), the
# write set and its two renderings (§27.5), and the clear vocabulary (§27.6).
#
# Absence resolving to a VALUE is banned; absence BOUNDING SCOPE is what a
# sparse update is (§27.13). The three properties that keep the second half
# legitimate are enforced here rather than promised: the write set is derived
# from ONE predicate (§23.6's provided, no source filter), it is never empty,
# and it is never invisible.
# ---------------------------------------------------------------------------

_WRITE_SPARSE = "sparse"
_WRITE_FULL_REPLACE = "full_replace"
_WRITE_MODES = (_WRITE_SPARSE, _WRITE_FULL_REPLACE)

# The two parentheticals the write set's human line carries, a function of the
# write mode alone and always present (§27.5).
_WRITE_MODE_PAREN = {
    _WRITE_SPARSE: "(other properties unchanged)",
    _WRITE_FULL_REPLACE: "(other properties are re-sent as read)",
}

# The two clauses the MCP description block's last line opens with -- the human
# log's two parentheticals in the same words (§27.10).
_WRITE_MODE_CLAUSE = {
    _WRITE_SPARSE: "left unchanged",
    _WRITE_FULL_REPLACE: "re-sent as read",
}

# The per-language noun phrase §12.16 pins for Python's clear declaration.
_NULLABLE_SPELLING = "nullable=True"


@dataclass(frozen=True)
class UpdateOf:
    """What a command updates: one resource, one write mode, two name lists.

    A frozen, keyword-only record whose first field is ``resource``, joining
    the CapWords family ``AtLeastOne`` / ``AllOrNone`` / ``Requires`` /
    ``Implies`` established for declarations that name a rule (§27.8).

    ``write_mode`` carries NO default, so omitting it is Python's own
    ``TypeError`` at the declaration site: the framework refuses to guess which
    of two writes a command performs, for the reason ``effect`` has no default
    either (§27.2).

    ``identity`` and ``properties`` are lists of DECLARED names, dashed exactly
    as ``Member("old-name")`` takes them. There is no alternate underscored
    spelling on the declaration surface.
    """

    resource: str
    _: dataclasses.KW_ONLY
    write_mode: str
    identity: tuple = ()
    properties: tuple = ()

    def __post_init__(self) -> None:
        object.__setattr__(self, "identity", tuple(self.identity))
        object.__setattr__(self, "properties", tuple(self.properties))


class _UnsetOccurrence:
    """The token-scan marker for a `--unset-<prop>` occurrence (§27.6).

    It is a marker rather than a value: the minted flag carries no value of its
    own, so the value pass skips it and the clear is delivered as absence.
    """

    __slots__ = ()

    def __repr__(self) -> str:  # pragma: no cover - debugging aid
        return "<unset>"


_UNSET_OCC = _UnsetOccurrence()


def _unset_flag_name(prop: str) -> str:
    """The flag the framework mints for a nullable property (§27.6)."""
    return f"unset-{prop}"


def _render_decl_flag(name: str) -> str:
    """§12.16's ``<decl>`` for a flag."""
    return f"flag '--{name}'"


def _render_decl_arg(name: str) -> str:
    """§12.16's ``<decl>`` for a positional arg."""
    return f"argument '{name}'"


# --- the registration guards (§12.16, §27.11) ------------------------------


def _raise_mutating_default(name: str, decl: str, value: object):
    """The ONLY guard in this family that fires on a command declaring no
    update at all: the ban keys on ``effect="mutating"`` (§27.1), where the
    rest keys on ``update_of``."""
    raise ValueError(
        f'command "{name}": {decl} declares {_default_spelling(value)} on a '
        f"mutating command: absence would write a value the invocation never "
        f'stated (declare presence="required" or presence="optional", or '
        f"apply the fallback in the handler and say so in its help)"
    )


def _raise_update_on_read_only(name: str):
    raise ValueError(
        f'command "{name}": a read_only command cannot declare update_of '
        f"(a command that changes nothing writes no properties)"
    )


def _raise_update_write_mode_invalid(name: str, value: object):
    raise ValueError(
        f'command "{name}": invalid write_mode "{value}": must be "sparse" '
        f'or "full_replace"'
    )


def _raise_update_resource_charset(name: str, resource: object):
    raise ValueError(
        f'command "{name}": update resource "{resource}" must match '
        f"[a-z][a-z0-9-]*"
    )


def _raise_update_properties_empty(name: str, resource: str):
    raise ValueError(
        f'command "{name}": update of "{resource}" declares no properties: '
        f"an update with nothing to write is not an update"
    )


def _raise_update_name_unknown(name: str, resource: str, x: str):
    raise ValueError(
        f'command "{name}": update of "{resource}" references unknown '
        f'name "{x}"'
    )


def _raise_update_name_ambiguous(name: str, resource: str, x: str):
    raise ValueError(
        f'command "{name}": update of "{resource}" references "{x}", which '
        f"names both a flag and a positional arg"
    )


def _raise_update_name_duplicate(name: str, resource: str, x: str):
    raise ValueError(
        f'command "{name}": update of "{resource}" declares "{x}" twice'
    )


def _raise_update_name_both_roles(name: str, resource: str, x: str):
    raise ValueError(
        f'command "{name}": update of "{resource}" declares "{x}" as both '
        f"identity and property"
    )


def _raise_update_references_scoped_flag(
    name: str, resource: str, x: str, path: str,
):
    raise ValueError(
        f'command "{name}": update of "{resource}" references \'{x}\', which '
        f"is declared under '{path}': an update's identity and properties are "
        f"declared at root scope only"
    )


def _raise_update_property_presence(name: str, resource: str, decl: str):
    """Covers ``required`` ONLY: a property declaring a default is refused by
    the ban four steps earlier (§27.11's order), an update command being
    mutating by §27.2's own guard, so the two never compete."""
    raise ValueError(
        f'command "{name}": update of "{resource}" property {decl} declares '
        f'presence="required": a property is absent exactly when it is not '
        f"being written, and the presence declaration for that is "
        f'presence="optional"'
    )


def _raise_update_property_is_arg(name: str, resource: str, x: str):
    raise ValueError(
        f'command "{name}": update of "{resource}" property "{x}" is a '
        f"positional arg: a property must be individually omissible and "
        f"clearable, and only a flag is"
    )


def _raise_update_property_is_choice_flag(name: str, resource: str, x: str):
    raise ValueError(
        f'command "{name}": update of "{resource}" property \'--{x}\' is a '
        f"choice flag: an elected record is a selection, not a property value"
    )


def _raise_nullable_not_property(name: str, decl: str):
    raise ValueError(
        f'command "{name}": {decl} declares {_NULLABLE_SPELLING} but is not '
        f"a property of an update: only a property can be cleared"
    )


def _raise_unset_name_reserved(name: str, x: str):
    raise ValueError(
        f'command "{name}": flag name "unset-{x}" is reserved: property '
        f"'--{x}' declares {_NULLABLE_SPELLING}, which mints '--unset-{x}'"
    )


# --- the two violations (parse-time, §12.16) -------------------------------


def _msg_update_no_property(resource: str, properties: str) -> str:
    """Names EVERY declared property, whether or not it is nullable: naming
    only the ones a reader has not used would require the framework to guess
    which one was meant.

    There is deliberately NO decline clause. §12.15 appends §21.4's clause when
    a bool member was provided false; the analogous input here is the opposite
    fact, because inside an update command ``--no-proxied`` PROVIDES the
    property with the value false (§27.7), so it satisfies this rule rather
    than declining it.
    """
    return f'update "{resource}": at least one property is required: {properties}'


def _msg_update_value_and_unset(x: str) -> str:
    """COMMAND LINE only: it is a collision between two tokens, and the machine
    doors have one key per property (§27.6), so no door can reach the state."""
    return (
        f"--{x} and --unset-{x} are mutually exclusive: a property is either "
        f"written or cleared"
    )


def _banned_mutating_default(value: object) -> bool:
    """Whether a declared default is a TOOL-PICKED VALUE (§27.1).

    Every scalar is one, ``""`` and ``0`` included, and so is a NON-EMPTY list
    or dict. Two carve-outs, both derived from the ban's own reason (§18.33
    item 301): an EMPTY collection declares no elements, so no value the
    framework chose can reach a write through it; and a ``RelativeToRoot``
    default resolves a LOCATION under a declared infrastructure root, deciding
    where a command writes and never what.
    """
    if value is None:
        # default=None has its own refusal (§23.1) and never reaches a write.
        return False
    if isinstance(value, RelativeToRoot):
        return False
    if isinstance(value, (list, dict, set, tuple)):
        return len(value) > 0
    return True


def _validate_mutating_defaults(
    name: str, effect: str, members: list, args: list,
) -> None:
    """§27.1's ban: on a command declaring ``effect="mutating"``, a flag or a
    positional arg may not declare a value default.

    Absence must never resolve to a value the invocation did not state, because
    on a mutating command a value the framework picked is a value the framework
    WRITES.

    It is evaluated PER COMMAND, over the flags and args that command carries
    -- its own, its flag sets' and its selectors' scoped flags at every depth
    -- so a shared flag set carrying a default is legal and attaching it to a
    mutating command is not. App-level global flags are NOT reached: a global
    has no classification of its own, and there is no command at the point of
    its declaration to key on (§27.1's stated hole).
    """
    if effect != EFFECT_MUTATING:
        return

    def walk(decls: Sequence) -> None:
        for m in decls:
            if isinstance(m, _Selector):
                # A SELECTOR's default names which scope is live rather than a
                # value written to anything (§18.33 item 303). The scope
                # beneath it is another matter: those are ordinary flags of a
                # mutating command, reached at every depth.
                for c in m.choices:
                    is_default_scope = (
                        m.presence == _PRESENCE_DEFAULT
                        and isinstance(m.default, c.cls)
                    )
                    for entry in c.members:
                        if isinstance(entry, Flag):
                            _check_mutating_default_flag(name, entry)
                            if is_default_scope:
                                # Python's instance-shaped default passes NO
                                # field values: a field value written into the
                                # default instance is a value default under
                                # another spelling, and Python is the only
                                # surface that can express it.
                                _check_mutating_default_instance_field(
                                    name, m.default, entry,
                                )
                    walk(c.members)
                continue
            _check_mutating_default_flag(name, m)

    walk(members)
    for a in args:
        if a.presence != _PRESENCE_DEFAULT:
            continue
        if not _banned_mutating_default(a.default):
            continue
        _raise_mutating_default(name, _render_decl_arg(a.name), a.default)


def _check_mutating_default_flag(name: str, f: "Flag") -> None:
    if f.presence != _PRESENCE_DEFAULT:
        return
    if not _banned_mutating_default(f.default):
        return
    _raise_mutating_default(name, _render_decl_flag(f.name), f.default)


def _check_mutating_default_instance_field(
    name: str, record: object, f: "Flag",
) -> None:
    value = getattr(record, _flag_param_name(f.name), None)
    if not _banned_mutating_default(value):
        return
    _raise_mutating_default(name, _render_decl_flag(f.name), value)


def _validate_update(
    name: str,
    effect: str,
    update_of: "UpdateOf | None",
    members: list,
    all_flags: list,
    selectors: list,
    all_args: list,
    scoped_paths: dict,
    scoped_names: set,
    global_flags: list | None,
) -> None:
    """Resolve and validate one command's update declaration (§27.11).

    The eight steps run in the pinned order over the whole declaration, so
    three implementations report the same FIRST error for a declaration with
    two faults. The order runs from the command's own classification, through
    the record's identity, outward to the declarations it names -- §26.8's
    direction -- and each step crosses the whole declaration before the next
    begins, so a message never blames a name for a fault in the record that
    names it.
    """
    # Step 1: the mutating-default ban. It runs first because it is a fact
    # about the command's CLASSIFICATION and is independent of whether an
    # update is declared at all.
    _validate_mutating_defaults(name, effect, members, all_args)

    d = update_of

    # Step 2: classification legality.
    if d is not None and effect == EFFECT_READ_ONLY:
        _raise_update_on_read_only(name)

    flags_by_name = {f.name: f for f in all_flags}
    selectors_by_name = {s.name: s for s in selectors}
    args_by_name = {a.name: a for a in all_args}
    roles: dict[str, bool] = {}

    if d is not None:
        # Step 3: record legality -- the resource name's charset, the write
        # mode's vocabulary, at least one property.
        if not isinstance(d.resource, str) or not _IDENTIFIER_RE.fullmatch(
            d.resource,
        ):
            _raise_update_resource_charset(name, d.resource)
        if d.write_mode not in _WRITE_MODES:
            _raise_update_write_mode_invalid(name, d.write_mode)
        if not d.properties:
            _raise_update_properties_empty(name, d.resource)

        # Step 4: name resolution. Every name in either list resolves to
        # exactly one flag or arg; unknown, ambiguous, duplicated and
        # both-roles names refuse here.
        scoped: list[str] = []

        def resolve(x: str, is_property: bool) -> None:
            is_flag = x in flags_by_name or x in selectors_by_name
            is_arg = x in args_by_name
            if is_flag and is_arg:
                _raise_update_name_ambiguous(name, d.resource, x)
            if not is_flag and not is_arg:
                # A SCOPED flag resolves as a flag and is refused by the scope
                # step below; reporting it as unknown would name the wrong
                # fault (§24.8, §27.3).
                if x not in scoped_names:
                    _raise_update_name_unknown(name, d.resource, x)
                scoped.append(x)
            if x in roles:
                if roles[x] == is_property:
                    _raise_update_name_duplicate(name, d.resource, x)
                _raise_update_name_both_roles(name, d.resource, x)
            roles[x] = is_property

        for x in d.identity:
            resolve(x, False)
        for x in d.properties:
            resolve(x, True)

        # Step 5: scope. A property inside a scope would have a write-set
        # membership the argv and flat doors could answer and the record door
        # could not -- one rule with three answers (§27.3).
        for x in scoped:
            _raise_update_references_scoped_flag(
                name, d.resource, x, scoped_paths.get(x, ""),
            )

        # Step 6: role legality -- a property that is a positional arg, then a
        # property that is a choice flag.
        for x in d.properties:
            if x in args_by_name:
                _raise_update_property_is_arg(name, d.resource, x)
        for x in d.properties:
            if x in selectors_by_name:
                _raise_update_property_is_choice_flag(name, d.resource, x)

        # Step 7: presence legality. A property the invocation must always
        # supply is written in every invocation, which makes the at-least-one
        # rule unfireable and turns a sparse update into a partial full replace
        # under a name that denies it.
        for x in d.properties:
            if flags_by_name[x].presence == _PRESENCE_REQUIRED:
                _raise_update_property_presence(
                    name, d.resource, _render_decl_flag(x),
                )

    # Step 8: the clear vocabulary. It runs LAST because the name reservation
    # is the only step that reads the flag namespace back after the property
    # set is known.
    properties = set(d.properties) if d is not None else set()
    for f in all_flags:
        if f.nullable and f.name not in properties:
            _raise_nullable_not_property(name, _render_decl_flag(f.name))
    if d is None:
        return
    for x in d.properties:
        if not flags_by_name[x].nullable:
            continue
        minted = _unset_flag_name(x)
        # The whole flag namespace this command's tokenizer reads, which
        # includes the app's globals: they are recognized after the command
        # name too, so a global of the minted name would be unreachable.
        if (
            minted in flags_by_name
            or minted in selectors_by_name
            or minted in scoped_names
            or any(gf.name == minted for gf in (global_flags or ()))
        ):
            _raise_unset_name_reserved(name, x)


# --- the write set (§27.5) -------------------------------------------------


class _UpdateState:
    """One invocation's answer to an update declaration: the ordered pair of
    the properties it writes and the properties it clears, plus the two
    readings of "the rest".

    Computed at parse time from §27.4's predicate and rendered wherever a run
    reports what it does.
    """

    __slots__ = ("decl", "written", "cleared", "resent", "untouched")

    def __init__(self, decl: UpdateOf) -> None:
        self.decl = decl
        self.written: list[str] = []
        self.cleared: list[str] = []
        self.resent: list[str] = []
        self.untouched: list[str] = []

    def paren(self) -> str:
        """The trailing parenthetical: a function of the write mode alone, and
        always present in both segment shapes."""
        return _WRITE_MODE_PAREN[self.decl.write_mode]

    def log_line(self) -> str:
        """The would-do log's unnumbered write-set line (§27.5), without the
        two-space indent the log adds.

        Two segments, ``writes:`` first, separated by ``"; "``, with an empty
        segment omitted entirely -- §27.4's rule guarantees at least one
        survives, so the line is never empty and never has to say that it is.
        Names are the properties' DECLARED names without the ``--`` prefix: the
        log is the human surface, where the reader knows a declaration by the
        name they type, and the write set is data.
        """
        segments: list[str] = []
        if self.written:
            segments.append("writes: " + ", ".join(self.written))
        if self.cleared:
            segments.append("clears: " + ", ".join(self.cleared))
        return "; ".join(segments) + " " + self.paren()

    def envelope_member(self) -> dict:
        """The machine rendering (§19.2's amendment, §27.5).

        The four arrays hold UNDERSCORED parameter names in declaration order
        and partition the declared property set exactly: every property appears
        in exactly one of them. ``resent`` and ``untouched`` are the two
        readings of "the rest", and exactly one of them is ever non-empty.
        """
        return {
            "resource": self.decl.resource,
            "write_mode": self.decl.write_mode,
            "written": [_flag_param_name(n) for n in self.written],
            "cleared": [_flag_param_name(n) for n in self.cleared],
            "resent": [_flag_param_name(n) for n in self.resent],
            "untouched": [_flag_param_name(n) for n in self.untouched],
        }


def _render_property_tokens(names: Sequence) -> str:
    """Every declared property as a CLI token, unquoted and joined by ", " in
    declaration order (§12.16's list rule)."""
    return ", ".join(f"--{n}" for n in names)


def _evaluate_update(
    cmd: "Command", store: "_SourcedStore", unsets: set,
) -> "_UpdateState | None":
    """Enforce the at-least-one-property rule and compute the write set.

    A property is provided exactly when §23.6's predicate says so, and there is
    no source filter: a value from env, from config or injected by an
    ``Implies`` is a provision. A negated bool property is a provision too --
    inside an update command ``--no-proxied`` WRITES false -- and so is an
    unset, clearing being writing (§27.4).
    """
    d = cmd.update_of
    if d is None:
        return None
    st = _UpdateState(d)
    for x in d.properties:
        if x in unsets:
            st.cleared.append(x)
        elif store.is_present_for_deps(x):
            st.written.append(x)
        elif d.write_mode == _WRITE_FULL_REPLACE:
            # A full-replace write touches every property, so nothing is
            # untouched: the rest is read back and re-sent.
            st.resent.append(x)
        else:
            st.untouched.append(x)
    if not st.written and not st.cleared:
        raise _ParseError(_msg_update_no_property(
            d.resource, _render_property_tokens(d.properties),
        ))
    return st


# --- the MCP projection (§27.10) -------------------------------------------


def _update_any_of_branches(cmd: "Command") -> list[dict]:
    """The at-least-one-property rule as one ``required`` branch per property,
    in declaration order.

    Its fidelity is EXACT: the rule IS provision at this door -- a supplied key
    is a provided property, a null is a supplied key and a clear, a false is a
    supplied key and a write -- so ``required`` states the whole rule with
    nothing left over.
    """
    if cmd.update_of is None:
        return []
    return [
        {"required": [_flag_param_name(x)]}
        for x in cmd.update_of.properties
    ]


def _update_description_lines(cmd: "Command") -> list[str]:
    """The tool description's update block (§27.10), in the shape §24.11's
    scope block and §26.12's constraint block already established.

    Members render in PROPERTY names, like every other member in this block:
    the caller writes keys, not argv.
    """
    d = cmd.update_of
    if d is None:
        return []
    lines = [
        f'Update of "{d.resource}" (write mode: {d.write_mode}):'
    ]
    # The `identifies:` line is omitted when the resource declares no identity
    # members.
    if d.identity:
        lines.append(
            "  identifies: "
            + ", ".join(_flag_param_name(x) for x in d.identity)
        )
    lines.append(
        "  writes: "
        + ", ".join(_flag_param_name(x) for x in d.properties)
        + " -- at least one is required"
    )
    last = (
        "  a property that is not supplied is "
        + _WRITE_MODE_CLAUSE[d.write_mode]
    )
    # The `; null clears <list>` clause appears only when at least one property
    # is nullable, naming them in declaration order.
    by_name = {f.name: f for f in cmd.flags}
    nullable = [
        _flag_param_name(x) for x in d.properties
        if by_name[x].nullable
    ]
    if nullable:
        last += "; null clears " + ", ".join(nullable)
    lines.append(last)
    return lines


# --- the schema encoding (§27.9) -------------------------------------------


def _serialize_update_of(d: UpdateOf) -> dict:
    """Publish the declaration COMPLETELY rather than indicatively: a consumer
    reconstructs the rule without re-reading the declaration.

    Names are published in the DECLARED spelling, matching the flag entry's own
    ``name``; the underscored spelling belongs to the machine doors.
    """
    return {
        "resource": d.resource,
        "identity": list(d.identity),
        "properties": list(d.properties),
    }


@dataclass
class Passthrough:
    """Marks a command as passthrough -- all tokens after the command name are
    forwarded to the handler as a raw list, bypassing flag/arg parsing."""

    handler: Callable  # func(ctx: Context, name: str, args: list[str], globals: dict) -> int | None | Outcome


@dataclass
class Forwarding:
    """Declares that a handler deliberately accepts and forwards ``**kwargs``.

    Guard v2 refuses a var-keyword handler unless the command declares
    forwarding. The ``reason`` is mandatory, non-empty, and emitted in the
    schema so a consumer's audit gate can review every forwarding site.
    """

    reason: str


# The one reason string strictcli's own auto-registered commands use. Their
# handlers must absorb the app's app-defined global flag values, which a
# framework-authored handler cannot name.
_FRAMEWORK_INTERNAL_FORWARDING_REASON = (
    "framework-internal: absorbs app-defined global flag values"
)

# Payload schemas for strictcli's own auto-registered commands (contract
# §19.5). Inline literals, byte-identical across the three implementations.
# The check command's payload is an array in both of its machine shapes -- the
# listing (--list) and the run results.
_CHECK_PAYLOAD_SCHEMA = {"type": "array", "items": {"type": "object"}}
# config show's payload is one object keyed by flag/config-field name, plus the
# "__infrastructure__" entry; the keys are dynamic, so the declaration names
# the container only. Every value it carries is a JSON document, which is what
# admits a `RelativeToRoot` default in §13's machine-stable marker shape.
_CONFIG_SHOW_PAYLOAD_SCHEMA = {"type": "object"}


def _raise_handler_var_keyword_undeclared(name: str):
    raise ValueError(
        f'command "{name}": handler accepts **kwargs but the command does not '
        f'declare forwarding; add forwarding=Forwarding(reason=...) or name '
        f'every parameter explicitly'
    )


def _raise_handler_param_optional_flag_default(
    name: str, param: str, flag_name: str,
):
    """Message template: a handler parameter bound to an optional flag carries
    a default that is not None (contract §12.12, §23.3).

    Python-only: Go and TypeScript handlers receive one kwargs map / one args
    object, so no per-parameter default exists there to re-sentinelize with.
    """
    raise ValueError(
        f'command "{name}": handler parameter \'{param}\' is bound to optional '
        f"flag '--{flag_name}' and must default to None"
    )


def _raise_handler_param_optional_arg_default(
    name: str, param: str, arg_name: str,
):
    """Message template: the positional-arg twin of the check above."""
    raise ValueError(
        f'command "{name}": handler parameter \'{param}\' is bound to optional '
        f"arg '{arg_name}' and must default to None"
    )


def _raise_forwarding_reason_empty(name: str):
    raise ValueError(f'command "{name}": forwarding reason must be a non-empty string')


def _raise_framework_internal_handler_foreign(name: str):
    raise ValueError(
        f'command "{name}": handler is marked framework-internal but is not '
        f'defined in the strictcli module'
    )


@dataclass
class DeprecatedCommand:
    """A declaration-only deprecated command: prints message to stderr and exits 1."""

    name: str
    message: str


# The two legal command classifications. There is no default: every command
# declares one, and a command registered without it is a registration-time
# hard error. Deprecated commands are exempt (they have no handler).
EFFECT_READ_ONLY = "read_only"
EFFECT_MUTATING = "mutating"
_EFFECT_VALUES = (EFFECT_READ_ONLY, EFFECT_MUTATING)


def _raise_command_effect_missing(name: str):
    raise ValueError(
        f'command "{name}": effect classification is required '
        f'(effect="read_only" or effect="mutating")'
    )


def _raise_command_effect_invalid(name: str, value: object):
    raise ValueError(
        f'command "{name}": invalid effect "{value}": '
        f'must be "read_only" or "mutating"'
    )


def _raise_deprecated_command_effect(name: str):
    raise ValueError(
        f'deprecated command "{name}": effect classification does not apply '
        f'(a deprecated command has no handler)'
    )


def _raise_command_read_only_consequential(name: str):
    """A read_only command cannot be consequential (contract §8.1).

    Classification answers "should a dry run record rather than execute?";
    ``consequential`` answers "are these effects worth interrupting someone
    for?". A command that changes nothing has no effects to weigh, so the two
    declarations cannot both hold.
    """
    raise ValueError(
        f'command "{name}": a read_only command cannot be consequential '
        f'(a command that changes nothing has nothing to confirm)'
    )


def _raise_command_read_only_dry_run_unsupported(name: str):
    """A read_only command cannot declare ``dry_run_supported=False``.

    Mirrors the read_only + consequential prohibition: a command that changes
    nothing records nothing, so a preview of it can never be dishonest and
    there is no reason to refuse one.
    """
    raise ValueError(
        f'command "{name}": a read_only command cannot declare '
        f'dry_run_supported=false (a command that changes nothing has no '
        f'effects a preview could misrepresent)'
    )


def _raise_command_dry_run_reason_missing(name: str):
    raise ValueError(
        f'command "{name}": dry_run_supported=false requires a non-empty '
        f'dry_run_unsupported_reason (say what a preview cannot honestly show)'
    )


def _raise_command_dry_run_reason_without_declaration(name: str):
    raise ValueError(
        f'command "{name}": dry_run_unsupported_reason requires '
        f'dry_run_supported=false (there is nothing to explain while dry run '
        f'is supported)'
    )


def _validate_dry_run_declaration(
    name: str, effect: str, dry_run_supported: bool,
    dry_run_unsupported_reason: str | None,
) -> None:
    """The three registration-time guards on the dry-run declaration.

    Shared by :class:`Command.__post_init__` and
    :func:`_build_and_validate_command` so both registration surfaces reject
    the same shapes with the same messages.
    """
    has_reason = (
        isinstance(dry_run_unsupported_reason, str)
        and bool(dry_run_unsupported_reason.strip())
    )
    if not dry_run_supported:
        if effect == EFFECT_READ_ONLY:
            _raise_command_read_only_dry_run_unsupported(name)
        if not has_reason:
            _raise_command_dry_run_reason_missing(name)
    elif dry_run_unsupported_reason is not None:
        _raise_command_dry_run_reason_without_declaration(name)


@dataclass(frozen=True)
class Command:
    """A leaf command with a handler."""

    name: str
    help: str
    handler: Callable | None
    effect: str
    # Declared per-command (contract §8.1). NOT mandatory -- absence means
    # "not consequential". It is a property of the COMMAND, deliberately not
    # named after the framework's reaction to it, so other behaviours can hang
    # off it later. Today the framework prompts for exactly these commands.
    consequential: bool = False
    # Declared per-command. Absence means "dry run is supported", which is the
    # regime's baseline: a mutating command records rather than executes. A
    # command that declares it false is saying a preview of it would LIE --
    # its effects escape the effects handle, or its later steps read state the
    # recorded ones would have written -- so the framework refuses --dry-run
    # for it at parse time rather than rendering a preview nobody can trust.
    # The reason is mandatory and is shown in help and in the refusal.
    dry_run_supported: bool = True
    dry_run_unsupported_reason: str | None = None
    # The update declaration (contract §27.2): the resource this command
    # changes, the write mode it changes it under, and the two name lists that
    # say which instance and what changes. Absence means the command is not an
    # update. An update command is ALWAYS mutating -- `update_of` on a
    # read_only command is a registration error -- which is what makes §27.1's
    # ban apply to every one of its declarations without a second rule.
    update_of: "UpdateOf | None" = None
    # The command's machine payload contract (contract §19.5): an inline JSON
    # Schema literal, registered as written. Absence means the command cannot
    # produce a payload -- ctx.payload is then a call-time hard error. The
    # literal is validated at registration over the closed subset, and the
    # value ctx.payload supplies is validated against it at emission.
    payload_schema: dict | None = None
    # Stdout ownership (contract §19.6). A command whose stdout IS the artifact
    # -- a SQL dump, an SVG, a hash-verified JSON document -- declares it, and
    # in machine mode the envelope moves to stderr so the artifact's bytes are
    # untouched. Outside machine mode the declaration changes nothing at all.
    owns_stdout: bool = False
    flags: tuple[Flag, ...] = ()
    args: tuple[Arg, ...] = ()
    flag_sets: tuple[FlagSet, ...] = ()
    # Root-scope selectors, in declaration order (contract §24).
    selectors: tuple[_Selector, ...] = ()
    # Root flags and selectors interleaved in declaration order,
    # which is the order help renders them in.
    members: tuple[object, ...] = ()
    # Every token a scoped declaration can accept, keyed by token name.
    sites: dict = field(default_factory=dict)
    # Short -> scoped token name, for the shorts scoped declarations claim.
    shorts: dict = field(default_factory=dict)
    constraints: tuple[AtLeastOne | AllOrNone | Requires | Implies, ...] = ()
    passthrough: Passthrough | None = None
    tags: frozenset[str] = frozenset()
    hidden: bool = False
    interactive: bool = False
    config_fields: tuple[str, ...] = ()
    grants: tuple[Grant, ...] = ()
    forwarding: Forwarding | None = None
    # Private marker, set ONLY by strictcli's own registration paths. It is not
    # reachable from any public factory, option or keyword, and is not emitted
    # in the schema.
    _framework_internal: bool = False

    def __post_init__(self) -> None:
        _require_non_empty_str(self.help, "help", "Command")
        if self.effect not in _EFFECT_VALUES:
            _raise_command_effect_invalid(self.name, self.effect)
        if self.consequential and self.effect == EFFECT_READ_ONLY:
            _raise_command_read_only_consequential(self.name)
        _validate_dry_run_declaration(
            self.name, self.effect, self.dry_run_supported,
            self.dry_run_unsupported_reason,
        )
        # The declared payload schema is validated as written, over the closed
        # subset (§19.5). An unknown keyword anywhere in the literal is a hard
        # error here, which is what keeps the subset closed by construction.
        if self.payload_schema is not None:
            found = _validate_payload_schema(self.payload_schema)
            if found is not None:
                raise ValueError(
                    _msg_payload_schema_invalid(self.name, found[0], found[1])
                )
        for tag in self.tags:
            if not _IDENTIFIER_RE.fullmatch(tag):
                raise ValueError(f'invalid tag name "{tag}": must match [a-z][a-z0-9-]*')


@dataclass
class Group:
    """A container for nested commands and subgroups (arbitrary depth)."""

    name: str
    help: str
    commands: dict[str, Command] = field(default_factory=dict)
    _groups: dict[str, Group] = field(default_factory=dict)
    deprecated: dict[str, DeprecatedCommand] = field(default_factory=dict)
    env_prefix: str | None = None
    _global_flags: list[Flag] = field(default_factory=list)
    tags: frozenset[str] = frozenset()
    _accumulated_tags: frozenset[str] = frozenset()
    hidden: bool = False
    _config_fields_ref: dict[str, ConfigField] = field(default_factory=dict)
    _infra_root_names: frozenset[str] = frozenset()
    _connection_env_names: frozenset[str] = frozenset()

    def __post_init__(self) -> None:
        _require_non_empty_str(self.help, "help", "Group")
        for tag in self.tags:
            if not _IDENTIFIER_RE.fullmatch(tag):
                raise ValueError(f'invalid tag name "{tag}": must match [a-z][a-z0-9-]*')

    def group(self, name: str, *, help: str, tags: set[str] | None = None,
              hidden: bool = False) -> Group:
        """Create and register a child subgroup."""
        if name in self.commands:
            raise ValueError(
                f'group "{name}" collides with an existing command'
            )
        if name in self._groups:
            raise ValueError(
                f'group "{name}" is already registered'
            )
        own_tags = frozenset(tags or set())
        grp = Group(name=name, help=help, env_prefix=self.env_prefix,
                     _global_flags=self._global_flags,
                     tags=own_tags,
                     _accumulated_tags=self._accumulated_tags | own_tags,
                     hidden=hidden,
                     _config_fields_ref=self._config_fields_ref,
                     _infra_root_names=self._infra_root_names,
                     _connection_env_names=self._connection_env_names)
        self._groups[name] = grp
        return grp

    def deprecate(self, name: str, *, message: str,
                  effect: str | None = None) -> None:
        """Register a deprecated subcommand in this group.

        Deprecated entries are classification-EXEMPT: they have no handler and
        execute nothing, so passing ``effect=`` is a registration-time error.
        """
        if effect is not None:
            _raise_deprecated_command_effect(name)
        if not name or not name.strip():
            raise ValueError("deprecated command name must be a non-empty string")
        if not message or not message.strip():
            raise ValueError(f'deprecated command "{name}": message must not be empty')
        if name in self.commands:
            raise ValueError(
                f'deprecated command "{name}" collides with an existing command'
            )
        if name in self._groups:
            raise ValueError(
                f'deprecated command "{name}" collides with an existing group'
            )
        if name in self.deprecated:
            raise ValueError(
                f'deprecated command "{name}" is already registered'
            )
        self.deprecated[name] = DeprecatedCommand(name=name, message=message)

    def command(
        self,
        name: str,
        *,
        help: str,
        effect: str | None = None,
        consequential: bool = False,
        dry_run_supported: bool = True,
        dry_run_unsupported_reason: str | None = None,
        update_of: "UpdateOf | None" = None,
        payload_schema: dict | None = None,
        owns_stdout: bool = False,
        args: list[Arg] | None = None,
        flag_sets: list[FlagSet] | None = None,
        constraints: list[AtLeastOne | AllOrNone | Requires | Implies] | None = None,
        passthrough: Passthrough | None = None,
        grants: list[Grant] | None = None,
        forwarding: Forwarding | None = None,
        tags: set[str] | None = None,
        hidden: bool = False,
        interactive: bool = False,
        config_fields: list[str] | None = None,
    ) -> Callable[[F], F]:
        """Decorator to register a command within this group."""

        def decorator(func: F) -> F:
            if name in self._groups:
                raise ValueError(
                    f'command "{name}" collides with an existing group'
                )
            handler_localns = dict(sys._getframe(1).f_locals)
            cmd = _build_and_validate_command(
                name, help=help, effect=effect,
                consequential=consequential,
                dry_run_supported=dry_run_supported,
                dry_run_unsupported_reason=dry_run_unsupported_reason,
                update_of=update_of,
                payload_schema=payload_schema,
                owns_stdout=owns_stdout,
                handler=func, args=args, flag_sets=flag_sets,
                constraints=constraints,
                env_prefix=self.env_prefix,
                global_flags=self._global_flags,
                passthrough=passthrough,
                grants=grants,
                forwarding=forwarding,
                tags=tags,
                inherited_tags=self._accumulated_tags,
                hidden=hidden,
                interactive=interactive,
                config_fields=config_fields,
                config_fields_ref=self._config_fields_ref,
                infra_root_names=self._infra_root_names,
                connection_env_names=self._connection_env_names,
                handler_localns=handler_localns,
            )
            self.commands[name] = cmd
            return func

        return decorator


_CONFIG_FIELD_NAME_RE = re.compile(r"^_?[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$")


@dataclass
class ConfigField:
    """Declares a typed config file field.

    Fields with no default are required — the config system will error if
    they are missing from the config file. Fields with a default are optional.
    """

    name: str
    type: type
    help: str
    default: object = _MISSING
    required: bool = field(init=False)

    def __post_init__(self) -> None:
        _require_non_empty_str(self.help, "help", "ConfigField")
        if self.type not in (str, bool, int, float):
            raise ValueError(
                f"ConfigField.type must be str, bool, int, or float, got {self.type!r}"
            )
        if not _CONFIG_FIELD_NAME_RE.fullmatch(self.name):
            raise ValueError(
                f'ConfigField name "{self.name}" is invalid: '
                f"must match [a-z][a-z0-9_]*(.[a-z][a-z0-9_]*)* "
                f"(lowercase, dots for sections)"
            )
        self.required = isinstance(self.default, _MissingSentinel)
        if not self.required and not isinstance(self.default, self.type):
            raise ValueError(
                f'ConfigField "{self.name}": default value {self.default!r} '
                f"does not match type {self.type.__name__}"
            )


@dataclass
class Result:
    """Returned by app.test()."""

    stdout: str
    stderr: str
    exit_code: int
    data: object = None


@dataclass(frozen=True)
class _DispatchResult:
    """What the one dispatch seam hands back to ``run()`` / ``test()``.

    ``payload`` is ``_MISSING`` when the handler supplied none (a dispatch
    that never reached a handler always has none).
    """

    exit_code: int
    payload: object = _MISSING


@dataclass
class Tool:
    """A tool descriptor for exposing CLI commands to tool-using LLM agents."""

    name: str
    description: str
    parameters: dict
    # The effects-regime classification, published BESIDE the argument schema
    # (never inside it): a consumer rendering this tool must be able to see
    # that the command changes things and that calling it requires stating
    # consent. Same vocabulary as the schema dump: `effect` is mandatory,
    # `consequential` defaults to False.
    effect: str
    consequential: bool
    execute: Callable


# Module-private mint token: a _CheckOutcome can be constructed only by code
# that holds this token (the reporters and the runner's internal skip mint).
# This is the seal that makes forging an outcome directly impossible.
_MINT_TOKEN = object()


@dataclass(frozen=True)
class _CheckProblem:
    """A single minted finding: text plus severity ("error" or "warn").

    Module-private -- problems are minted only via reporter methods.
    """

    text: str
    severity: str


@dataclass(frozen=True)
class _CheckOutcome:
    """The ceiling-typed result of a check implementation.

    Module-private with a construction guard: a valid outcome is obtained ONLY
    through reporter methods (passed/skipped/found) or the runner's internal
    skip mint, both of which pass ``_MINT_TOKEN``. Direct construction raises.
    """

    kind: str  # "passed", "skipped", "found"
    message: str
    problems: tuple[_CheckProblem, ...] = ()
    # notes is an informational, verdict-inert channel: notes are recorded
    # unconditionally on ANY outcome (including a pass) via reporter.note. They
    # are PROVABLY inert -- excluded from status derivation, gating, problem
    # ordering, and exit codes. They surface only under --verbose and in JSON.
    notes: tuple[str, ...] = ()
    _token: object = None

    def __post_init__(self) -> None:
        if self._token is not _MINT_TOKEN:
            raise TypeError(
                "_CheckOutcome cannot be constructed directly; "
                "obtain one from a reporter (passed/skipped/found)"
            )

    @property
    def status(self) -> str:
        """Derived verdict label ("pass"/"fail"/"warn"/"skip")."""
        return _derive_status(self)

    def _ordered_problems(self) -> tuple[_CheckProblem, ...]:
        """Problems grouped by severity: all error problems, then all warns."""
        errs = tuple(p for p in self.problems if p.severity == "error")
        warns = tuple(p for p in self.problems if p.severity == "warn")
        return errs + warns


def _mint_skip(message: str) -> _CheckOutcome:
    """Runner-internal mint for cascade/scope skip outcomes."""
    return _CheckOutcome(kind="skipped", message=message, _token=_MINT_TOKEN)


def _check_abort_text(name: str, type_name: str, message: str) -> str:
    """Build the attribution line for a check whose impl aborted.

    Names the check, the exception type (so a framework or harness bug stays
    identifiable) and the exception's own message. An empty message drops the
    colon rather than emitting a dangling one.
    """
    if message:
        return f'check "{name}" aborted with {type_name}: {message}'
    return f'check "{name}" aborted with {type_name}'


def _mint_check_abort(name: str, exc: BaseException) -> _CheckOutcome:
    """Runner-internal mint for a check whose impl raised.

    The abort is reported as THAT check's own failure: a found outcome carrying
    a single error-severity problem, so it derives FAIL, fails the run, and
    cascade-skips its dependents exactly like any other failing check. Every
    rendering surface (the result row, the problem line, both JSON fields)
    carries the full attribution, so no reader loses it.
    """
    text = _check_abort_text(name, type(exc).__name__, str(exc))
    return _CheckOutcome(
        kind="found",
        message=text,
        problems=(_CheckProblem(text=text, severity="error"),),
        _token=_MINT_TOKEN,
    )


def _derive_status(outcome: _CheckOutcome) -> str:
    """Map a minted outcome to its verdict label.

    passed => pass; skipped => skip; found with an error problem => fail;
    found with only warns => warn.
    """
    if outcome.kind == "passed":
        return "pass"
    if outcome.kind == "skipped":
        return "skip"
    if outcome.kind == "found":
        if any(p.severity == "error" for p in outcome.problems):
            return "fail"
        return "warn"
    # Defensive: outcomes are only ever minted with one of the three kinds
    # above. Mirrors the Go implementation's panic so the two stay in parity.
    raise ValueError(f"unknown check outcome kind {outcome.kind!r}")


class _ReporterCore:
    """Shared problem accumulator and minting surface for both reporters.

    Holds warn()/passed()/skipped()/found(). Error-minting lives ONLY on
    ErrorReporter, so WarnReporter structurally lacks it (accessing ``.error``
    on a WarnReporter is an AttributeError at runtime and a type error under
    mypy).
    """

    def __init__(self) -> None:
        self._problems: list[_CheckProblem] = []
        # Notes accumulate informational messages recorded via note(). They are
        # carried onto the minted outcome but never influence status, gating, or
        # exit codes -- a verdict-inert reporting channel.
        self._notes: list[str] = []

    def note(self, text: str) -> None:
        """Record an informational note. Non-empty text required.

        Notes are allowed on EVERY outcome, including a pass -- they never
        trigger the problems-present errors that passed()/skipped() enforce.
        Notes are verdict-inert: they surface only under --verbose and in JSON.
        """
        if not isinstance(text, str) or not text.strip():
            raise ValueError("note text must be a non-empty string")
        self._notes.append(text)

    # Reporter validation messages are worded identically to the Go
    # implementation (method-agnostic phrasing, no "warn(text)"/"Warn:" prefix)
    # so the two implementations are byte-for-byte in parity -- see
    # conformance/check_error_parity.py.
    def warn(self, text: str) -> None:
        """Mint a warn-severity problem. Non-empty text required."""
        if not isinstance(text, str) or not text.strip():
            raise ValueError("problem text must be a non-empty string")
        self._problems.append(_CheckProblem(text=text, severity="warn"))

    def passed(self, message: str) -> _CheckOutcome:
        """Finalize a terminal PASS. Errors if any problems were reported."""
        if not isinstance(message, str) or not message.strip():
            raise ValueError("outcome message must be a non-empty string")
        if self._problems:
            raise ValueError(
                "problems were reported; a check that found problems "
                "cannot pass -- use found instead"
            )
        return _CheckOutcome(
            kind="passed", message=message,
            notes=tuple(self._notes), _token=_MINT_TOKEN,
        )

    def skipped(self, reason: str) -> _CheckOutcome:
        """Finalize a terminal SKIP. Errors if any problems were reported."""
        if not isinstance(reason, str) or not reason.strip():
            raise ValueError("skip reason must be a non-empty string")
        if self._problems:
            raise ValueError(
                "problems were reported; a check that found problems "
                "cannot skip"
            )
        return _CheckOutcome(
            kind="skipped", message=reason,
            notes=tuple(self._notes), _token=_MINT_TOKEN,
        )

    def found(self, message: str) -> _CheckOutcome:
        """Finalize an outcome carrying the accumulated problems.

        Errors when nothing was reported -- nothing found means pass, so say so
        explicitly with passed().
        """
        if not isinstance(message, str) or not message.strip():
            raise ValueError("outcome message must be a non-empty string")
        if not self._problems:
            raise ValueError(
                "no problems were reported; nothing found means pass "
                "-- use passed instead"
            )
        return _CheckOutcome(
            kind="found",
            message=message,
            problems=tuple(self._problems),
            notes=tuple(self._notes),
            _token=_MINT_TOKEN,
        )


class WarnReporter(_ReporterCore):
    """Reporter handed to warn-severity check impls.

    Can mint warn-severity problems and terminal outcomes but structurally
    LACKS error-minting: there is no ``error`` method, so a warn check cannot
    produce an error-severity problem and can never cascade.
    """


class ErrorReporter(_ReporterCore):
    """Reporter handed to error-severity check impls.

    Everything WarnReporter has PLUS ``error`` (mints an error-severity problem).
    """

    def error(self, text: str) -> None:
        """Mint an error-severity problem. Non-empty text required."""
        if not isinstance(text, str) or not text.strip():
            raise ValueError("problem text must be a non-empty string")
        self._problems.append(_CheckProblem(text=text, severity="error"))


@dataclass(frozen=True)
class SkipCheck:
    """Directive a scope adapter returns to skip a check with a reason.

    The adapter can no longer mint arbitrary outcomes -- it either returns a
    replacement context (context projection) or this skip directive.
    """

    reason: str

    def __post_init__(self) -> None:
        if not isinstance(self.reason, str) or not self.reason.strip():
            raise ValueError("SkipCheck.reason must be a non-empty string")


@dataclass(frozen=True)
class CheckRunResult:
    """A named check outcome returned by App.run_checks().

    The verdict is derived from the minted outcome; the runner's exit/cascade
    logic and the formatters all consume these same accessors (one source of
    truth).
    """

    name: str
    outcome: _CheckOutcome
    # Wall-clock time in integer milliseconds spent inside the check impl.
    # Captured around the impl call only; checks that never execute
    # (cascade-skipped) carry 0. Purely informational -- never affects status
    # or exit codes.
    duration_ms: int = 0

    @property
    def status(self) -> str:
        """Derived label: "pass", "fail", "warn", or "skip"."""
        return _derive_status(self.outcome)

    @property
    def message(self) -> str:
        """The outcome's human-readable message."""
        return self.outcome.message

    @property
    def problems(self) -> tuple[_CheckProblem, ...]:
        """The minted problems (error and warn severity) from this check run."""
        return self.outcome.problems

    @property
    def notes(self) -> tuple[str, ...]:
        """Informational notes recorded during the check run (verdict-inert)."""
        return self.outcome.notes

    def gated(self) -> bool:
        """Whether the outcome carries an error-severity problem (derived FAIL)."""
        return self.status == "fail"

    def warned(self) -> bool:
        """Whether the outcome carries only warn-severity problems (derived WARN)."""
        return self.status == "warn"


@runtime_checkable
class CheckContext(Protocol):
    """Minimal interface that tool-specific check contexts must satisfy."""

    project_root: Path


class ConnectionEnvReader(Protocol):
    """OPTIONAL capability a check context may expose: the value of a declared
    connection env (``connection_env``), read live -- EXCEPT under --hermetic,
    where it resolves as absent ``(None, False)`` so a check can skip visibly
    instead of connecting. The check command wraps the tool-supplied check
    context in a value that satisfies this protocol, backed by the app's declared
    connection envs and the invocation's hermetic state. Checks that need a
    connection URL call ``ctx.connection_env_value("DATABASE_URL")``.

    ``is_hermetic()`` reports whether the invocation ran under --hermetic. It
    exists so a check can DISTINGUISH the two cases that
    ``connection_env_value``'s ``present=False`` otherwise conflates:
    "--hermetic suppressed the connection env" vs "the env var is simply unset".
    A check that layers config fallbacks below the env must honor hermetic even
    when the env is unset -- otherwise it falls through to a config URL and
    connects, violating the hermetic guarantee::

        dsn, present = ctx.connection_env_value("DATABASE_URL")
        if not present:
            if ctx.is_hermetic():
                return reporter.skipped("hermetic: connection suppressed")
            # env unset but not hermetic -- config fallback is allowed here
    """

    def connection_env_value(self, env_var: str) -> "tuple[str | None, bool]": ...

    def is_hermetic(self) -> bool: ...


class _CheckContextWithConn:
    """Wraps a tool-supplied check context, delegating attribute access while
    adding connection-env access (hermetic-suppressed) so check functions can
    read declared connection envs without the tool implementing anything beyond
    ``project_root``."""

    def __init__(self, base, connections: frozenset[str], hermetic: bool) -> None:
        self._base = base
        self._connections = connections
        self._hermetic = hermetic

    def __getattr__(self, name):
        return getattr(self._base, name)

    def connection_env_value(self, env_var: str) -> "tuple[str | None, bool]":
        if env_var in self._connections:
            if self._hermetic:
                return None, False
            if env_var in os.environ:
                return os.environ[env_var], True
            return None, False
        raise KeyError(f'"{env_var}" is not a declared connection env var')

    def is_hermetic(self) -> bool:
        """Report whether the invocation ran under --hermetic. Mirrors the
        hermetic flag captured when the wrapper was built."""
        return self._hermetic


@dataclass
class _CheckDef:
    """Internal definition of a single check loaded from TOML."""

    name: str
    tags: list[str]
    severity: str
    fast: bool
    pure: bool
    needs_network: bool
    depends_on: list[str]
    scope: str = ""
    impl: object | None = None
    impl_form: str = ""  # "error" or "warn" -- registration form, for the severity cross-check


@dataclass(frozen=True)
class CheckSpec:
    """A fully-formed, ceiling-typed check produced by a check provider.

    Opaque by construction: build one only via :func:`error_check_spec` or
    :func:`warn_check_spec`, which bind the reporter form to the declared
    severity so the impl cannot mint a problem its severity forbids. Providers
    return lists of these (see :meth:`App.register_check_provider`).
    """

    name: str
    tags: list[str]
    severity: str
    fast: bool
    pure: bool
    needs_network: bool
    depends_on: list[str]
    scope: str
    _impl: Callable  # (ctx) -> _CheckOutcome, reporter already bound
    _impl_form: str  # "error" or "warn" -- bound by the constructor


def error_check_spec(
    *,
    name: str,
    tags: list[str],
    fast: bool,
    pure: bool,
    needs_network: bool,
    depends_on: list[str],
    impl: Callable,
    severity: str = "error",
    scope: str = "",
) -> CheckSpec:
    """Build an error-severity check spec for a provider.

    ``impl`` receives ``(ctx, reporter)`` where ``reporter`` is an
    :class:`ErrorReporter` (can mint both error- and warn-severity problems).
    ``severity`` must be ``"error"`` -- a mismatch is a hard error at
    materialization (the provider analog of the TOML/register severity check).
    """
    def run(ctx: CheckContext) -> _CheckOutcome:
        return impl(ctx, ErrorReporter())

    return CheckSpec(
        name=name, tags=list(tags), severity=severity, fast=fast, pure=pure,
        needs_network=needs_network, depends_on=list(depends_on), scope=scope,
        _impl=run, _impl_form="error",
    )


def warn_check_spec(
    *,
    name: str,
    tags: list[str],
    fast: bool,
    pure: bool,
    needs_network: bool,
    depends_on: list[str],
    impl: Callable,
    severity: str = "warn",
    scope: str = "",
) -> CheckSpec:
    """Build a warn-severity check spec for a provider.

    ``impl`` receives ``(ctx, reporter)`` where ``reporter`` is a
    :class:`WarnReporter`, which structurally lacks error-minting: a warn check
    cannot cascade. ``severity`` must be ``"warn"``.
    """
    def run(ctx: CheckContext) -> _CheckOutcome:
        return impl(ctx, WarnReporter())

    return CheckSpec(
        name=name, tags=list(tags), severity=severity, fast=fast, pure=pure,
        needs_network=needs_network, depends_on=list(depends_on), scope=scope,
        _impl=run, _impl_form="warn",
    )


def _msg_test_coverage_boolean_retired() -> str:
    """The retired boolean's refusal (contract §12.12: one sentence, each
    language's own spellings inside it -- `test_coverage` / `test_coverage_dir`
    here, `WithTestCoverage` / `WithTestCoverageDir` in Go, `testCoverage` /
    `testCoverageDir` in TypeScript)."""
    return (
        "test_coverage is not accepted; declare the directory holding "
        "coverage/ and test-coverage.json with test_coverage_dir"
    )


@dataclass
class App:
    """The root CLI application."""

    name: str
    help: str
    version: str | None = None
    env_prefix: str | None = None
    config: bool = False
    config_path: str | None = None
    # Where --dump-schema writes. A plain path (absolute, or relative to the
    # App's construction-time working directory) or a RelativeToRoot marker
    # resolved through a declared infra root. When undeclared, the framework's
    # own location applies: ".strictcli/schema.json" ANCHORED at the
    # construction-time working directory, so a chdir between construction and
    # dispatch can no longer redirect the write into the caller's cwd.
    schema_path: str | None = None
    config_format: str = "json"
    config_conflict_mode: str = "cli-wins"
    no_default_config_path: bool = False
    # Infrastructure env vars. infra_root maps a location env var -> its default
    # path (dict preserves declaration order). handshake_env maps a cross-tool
    # protocol env var -> its help string.
    infra_root: dict[str, str] | None = None
    handshake_env: dict[str, str] | None = None
    # connection_env maps a behavioral "reach outside the process" env var
    # (e.g. a database/service URL) -> its help string. Unlike roots and
    # handshakes it is hermetic-SUPPRESSED: under --hermetic it resolves as
    # absent. No default, read lazily. Flags bind to it via connection_url=.
    connection_env: dict[str, str] | None = None
    # App-level observe authorization: a list of argv PREFIXES. A
    # ctx.effects.run whose argv matches one element-wise (string equality only)
    # is an observe: it executes even in dry mode, returns a real value, and is
    # never written to the would-do log.
    proc_observe_allowlist: list[list[str]] | None = None
    checks_path: str | Path | None = None
    checks_embed: bytes | None = None
    # The directory holding this app's coverage state: `coverage/` (per-process
    # shard files) and `test-coverage.json` (the committed manifest). Declared,
    # never discovered. Absent means coverage is off -- no provider registered,
    # no paths computed, nothing touched. A declared directory that does not
    # exist at construction likewise leaves coverage off: that is the installed
    # distribution, where the path naming the source checkout is simply gone.
    test_coverage_dir: str | os.PathLike | None = None
    # Refusal-only. The retired boolean has no accepted spelling; this field
    # exists so that passing it names the option that replaced it instead of
    # raising CPython's bare "unexpected keyword argument" TypeError.
    test_coverage: bool | None = None
    flags: list[Flag] = field(default_factory=list)
    _commands: dict[str, Command] = field(default_factory=dict)
    _groups: dict[str, Group] = field(default_factory=dict)
    _deprecated: dict[str, DeprecatedCommand] = field(default_factory=dict)

    def __post_init__(self) -> None:
        if self.test_coverage is not None:
            raise ValueError(_msg_test_coverage_boolean_retired())
        _require_non_empty_str(self.version, "version", "App")
        _require_non_empty_str(self.help, "help", "App")
        # Check for duplicate and reserved global flag names
        seen: set[str] = set()
        for f in self.flags:
            if f.name in seen:
                raise ValueError(f'duplicate global flag name "{f.name}"')
            if f.name in _RESERVED_FRAMEWORK_FLAG_NAMES:
                # Unreachable through Flag() construction (Flag.__post_init__
                # bans the quartet first); kept so the global-flag validation
                # path carries the same message for any other construction route.
                _raise_flag_name_reserved_by_framework(f.name)
            if f.name in _BANNED_FLAG_NAMES:
                # Likewise unreachable through Flag(); kept for parity with the
                # quartet's own belt-and-braces check on this path.
                _raise_flag_name_yes_banned()
            if f.name in _RESERVED_GLOBAL_FLAG_NAMES:
                raise ValueError(
                    f'global flag name "{f.name}" is reserved'
                )
            if f.short and f.short in _RESERVED_GLOBAL_SHORT_NAMES:
                raise ValueError(
                    f'global short flag "{f.short}" is reserved'
                )
            seen.add(f.name)
        self._global_flags: list[Flag] = list(self.flags)
        self._last_global_values: dict[str, object] = {}
        self._last_sources: dict[str, str] = {}
        # The update construct's per-dispatch state (contract §27). `writes` is
        # this invocation's write set, None on every command that declares no
        # update; `unsets` names the properties it CLEARED.
        self._last_writes: "_UpdateState | None" = None
        self._last_unsets: set = set()
        self._last_hermetic: bool = False
        # Framework-owned reserved quartet, extracted by the pre-scan and
        # delivered on the Context (never as handler kwargs).
        self._last_dry_run: bool = False
        self._last_approve_consequential: bool = False
        self._last_quiet: bool = False
        # Machine mode (contract §19.1), delivered on the Context like the
        # quartet: extracted by the pre-scan, never a handler kwarg.
        self._last_json: bool = False
        self._last_verbose: bool = False

        # Observe allowlist: plain argv prefixes, compared by string equality.
        prefixes: list[tuple[str, ...]] = []
        for prefix in self.proc_observe_allowlist or ():
            if isinstance(prefix, str) or not isinstance(prefix, (list, tuple)):
                raise ValueError(
                    "proc_observe_allowlist entries must be lists of strings, "
                    f"got {type(prefix).__name__}"
                )
            if not prefix:
                raise ValueError(
                    "proc_observe_allowlist entries must not be empty"
                )
            for element in prefix:
                if not isinstance(element, str):
                    raise ValueError(
                        "proc_observe_allowlist entries must be lists of "
                        f"strings, got {type(element).__name__}"
                    )
            prefixes.append(tuple(prefix))
        self._proc_observe_allowlist: tuple[tuple[str, ...], ...] = tuple(prefixes)

        # The structured effect log for the most recent dispatch. Populated in
        # BOTH modes: recorded entries in dry mode, executed entries (with
        # recorded=False) in live mode, plus framework-blessed CACHE_WRITEs.
        self._effect_log = _EffectLog()

        # The stdin side of the confirm protocol. Swappable through the
        # test-only ``_set_confirm_io`` seam; the real reader by default.
        self._confirm_io: _ConfirmIO = _REAL_CONFIRM_IO

        # Resolve infrastructure roots eagerly, at construction. Infra vars have
        # no argv dependency, so resolution is sound here -- and this is WHY it
        # is hermetic-immune: there is no argv yet to consult, so --hermetic
        # (which only suppresses argv-derived config/env behavior) can never
        # affect location roots.
        self._infra_roots: dict[str, str] = {}
        self._infra_root_order: list[str] = []
        self._infra_root_defaults: dict[str, str] = {}
        self._infra_root_from_env: dict[str, bool] = {}
        if self.infra_root:
            for env_var, default_path in self.infra_root.items():
                if env_var in os.environ:
                    self._infra_roots[env_var] = os.path.expanduser(os.environ[env_var])
                    self._infra_root_from_env[env_var] = True
                else:
                    self._infra_roots[env_var] = os.path.expanduser(default_path)
                    self._infra_root_from_env[env_var] = False
                self._infra_root_order.append(env_var)
                self._infra_root_defaults[env_var] = default_path
        self._handshake_envs: dict[str, str] = dict(self.handshake_env) if self.handshake_env else {}
        self._handshake_order: list[str] = list(self.handshake_env.keys()) if self.handshake_env else []
        for ev in self._handshake_order:
            if not self._handshake_envs[ev] or not self._handshake_envs[ev].strip():
                raise ValueError(f'handshake env var "{ev}": help must be a non-empty string')
            if ev in self._infra_roots:
                raise ValueError(f'handshake env var "{ev}" is already declared as an infra root')
        # Connection env vars: behavioral, hermetic-suppressed, no default.
        self._connection_envs: dict[str, str] = dict(self.connection_env) if self.connection_env else {}
        self._connection_order: list[str] = list(self.connection_env.keys()) if self.connection_env else []
        for ev in self._connection_order:
            if not self._connection_envs[ev] or not self._connection_envs[ev].strip():
                raise ValueError(f'connection env var "{ev}": help must be a non-empty string')
            if ev in self._infra_roots:
                raise ValueError(f'connection env var "{ev}" is already declared as an infra root')
            if ev in self._handshake_envs:
                raise ValueError(f'connection env var "{ev}" is already declared as a handshake env var')
        self._connection_env_names: frozenset[str] = frozenset(self._connection_envs)
        # A shared frozenset of declared root names, threaded to commands/groups
        # so flag-default markers can be validated at registration time.
        self._infra_root_names: frozenset[str] = frozenset(self._infra_roots)
        # Resolve the config-path marker (if any) now that roots exist. The
        # DECLARATION is retained beside the resolution, because that is what
        # the dumped schema publishes: a resolved absolute path is a property
        # of the dumping machine, and a committed schema file must not carry
        # one (contract §25.11).
        self._config_path_declared: object = self.config_path
        if isinstance(self.config_path, RelativeToRoot):
            self.config_path = _resolve_infra_root_path(self.config_path, self._infra_roots)
        # Resolve the schema-dump location once, at construction: a declared
        # marker through its root, a declared relative path and the framework's
        # own default against the construction-time cwd.
        if isinstance(self.schema_path, RelativeToRoot):
            self.schema_path = _resolve_infra_root_path(
                self.schema_path, self._infra_roots,
            )
        self._schema_out_path: str = os.path.abspath(
            self.schema_path
            if self.schema_path is not None
            else os.path.join(".strictcli", "schema.json")
        )
        # Validate global flag default markers against declared roots and
        # connection-URL bindings against declared connection envs.
        for f in self._global_flags:
            self._validate_flag_infra_marker(f)
            _validate_connection_binding(f, self._connection_env_names)

        # Validate config_format
        if self.config_format not in ("json", "toml"):
            raise ValueError(
                f'App.config_format must be "json" or "toml", got {self.config_format!r}'
            )
        # Validate config_conflict_mode
        if self.config_conflict_mode not in ("cli-wins", "error"):
            raise ValueError(
                f'App.config_conflict_mode must be "cli-wins" or "error", got {self.config_conflict_mode!r}'
            )
        # Register config subcommands if enabled (config data loaded at parse time)
        self._config_data: dict = {}
        if self.config:
            self._register_config_group()
        # Discover checks TOML
        self._check_context_factory: Callable | None = None
        self._scope_adapter: Callable | None = None
        # Check-provider hook state. Providers populate the registry lazily at
        # the first registry read (materialization), memoized per cwd.
        self._check_providers: list[Callable] = []
        self._provider_sourced_names: set[str] = set()
        self._provider_materialized_cwd: str | None = None
        if self.checks_path is not None and self.checks_embed is not None:
            raise ValueError("cannot use both checks_path and checks_embed")
        if self.checks_path is not None:
            checks_toml_path = Path(self.checks_path).resolve()
            if not checks_toml_path.is_file():
                raise ValueError(f"checks_path does not exist: {self.checks_path}")
            app_name, parsed_defs = _load_checks_toml(checks_toml_path)
            if app_name != self.name:
                raise ValueError(
                    f'checks.toml: app "{app_name}" does not match app name "{self.name}"'
                )
            self._enable_checks()
            for cdef in parsed_defs.values():
                self._add_check_def(cdef)
        elif self.checks_embed is not None:
            app_name, parsed_defs = _parse_checks_toml(self.checks_embed)
            if app_name != self.name:
                raise ValueError(
                    f'checks.toml: app "{app_name}" does not match app name "{self.name}"'
                )
            self._enable_checks()
            for cdef in parsed_defs.values():
                self._add_check_def(cdef)
        else:
            self._check_defs: dict[str, _CheckDef] = {}
            self._checks_enabled = False

        self._tag_contracts: dict[str, str] = {}

        # Config field declarations
        self._config_fields: dict[str, ConfigField] = {}
        self._framework_fields: dict[str, ConfigField] = {}

        # Config parse error (for config show to pick up)
        self._config_parse_err: str | None = None

        # Test-coverage instrumentation. When enabled, every test() and call()
        # invocation records which command was dispatched so a check can verify
        # that every command in the app's surface has been exercised.
        self._coverage_shard_path: str | None = None
        self._coverage_dir: str | None = None
        self._coverage_manifest_path: str | None = None
        self._last_resolved_path: list[str] = []
        if self.test_coverage_dir is not None:
            # The declared directory decides everything. An app installed
            # elsewhere finds it absent and stays uninstrumented: no provider,
            # no paths, no writes anywhere -- in particular not into whatever
            # directory the consumer happened to start the CLI from.
            root = os.path.abspath(os.fspath(self.test_coverage_dir))
            if os.path.isdir(root):
                # Only the PATHS are computed here. The coverage directory
                # itself is created lazily by _record_coverage, immediately
                # before the first shard write: shards are written only on the
                # test-harness paths (test() and call()), so a plain CLI
                # invocation leaves no coverage/ behind.
                self._coverage_dir = os.path.join(root, "coverage")
                self._coverage_manifest_path = os.path.join(
                    root, "test-coverage.json"
                )
                self._coverage_shard_path = os.path.join(
                    self._coverage_dir,
                    f"{os.getpid()}.jsonl",
                )
                self.register_check_provider(self._test_coverage_provider)

    def _validate_flag_infra_marker(self, f: Flag) -> None:
        """Panic if a flag's default is a RelativeToRoot marker referencing an
        undeclared root. Called at registration for construction-time errors."""
        if isinstance(f.default, RelativeToRoot):
            if f.default.env_var not in self._infra_roots:
                raise ValueError(
                    f'flag "{f.name}": RelativeToRoot references undeclared infra '
                    f'root "{f.default.env_var}"; declare it as an infra root'
                )

    def _infra_access(self, hermetic: bool = False) -> "_InfraAccess | None":
        """Snapshot infra data for a Context: resolved roots + declared handshake
        env var names + declared connection env var names. Connection envs are
        suppressed when hermetic is True. Returns None when nothing is declared."""
        if not self._infra_roots and not self._handshake_envs and not self._connection_envs:
            return None
        return _InfraAccess(
            roots=dict(self._infra_roots),
            handshakes=set(self._handshake_envs),
            connections=set(self._connection_envs),
            hermetic=hermetic,
        )

    def _record_coverage(self, cmd_path: str) -> None:
        """Append a coverage record for the resolved command path.

        Each test() or call() invocation appends one JSONL line to the
        process's shard file (named "<pid>.jsonl"). Uniqueness across concurrent
        writers comes from the PID and O_APPEND; one shard per process is
        sufficient, so there is no per-write shard counter.
        """
        if self._coverage_shard_path is None:
            return
        path = self._coverage_shard_path
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "a", encoding="utf-8") as f:
            f.write(json.dumps({"command": cmd_path}) + "\n")
        self._record_cache_write(path)

    def _collect_all_command_paths(self) -> set[str]:
        """Enumerate all non-deprecated leaf command paths as dotted strings."""
        paths: set[str] = set()

        for name in self._commands:
            paths.add(name)

        def _walk_group(group: Group, prefix: list[str]) -> None:
            for cmd_name in group.commands:
                paths.add(".".join(prefix + [cmd_name]))
            for sub_name, sub_group in group._groups.items():
                _walk_group(sub_group, prefix + [sub_name])

        for group_name, group in self._groups.items():
            _walk_group(group, [group_name])

        return paths

    def _collect_all_commands(self) -> list[tuple[str, "Command"]]:
        """Enumerate (dotted path, Command) pairs in registration order."""
        out: list[tuple[str, Command]] = []

        for name, cmd in self._commands.items():
            out.append((name, cmd))

        def _walk_group(group: Group, prefix: list[str]) -> None:
            for cmd_name, cmd in group.commands.items():
                out.append((".".join(prefix + [cmd_name]), cmd))
            for sub_name, sub_group in group._groups.items():
                _walk_group(sub_group, prefix + [sub_name])

        for group_name, group in self._groups.items():
            _walk_group(group, [group_name])

        return out

    def _test_coverage_provider(self) -> list[CheckSpec]:
        """Built-in check provider for cli-test-coverage.

        Registered automatically when test_coverage_dir names a directory that
        exists. The verdict is derived from committed state: the covered set is
        the union of the committed manifest (test-coverage.json in that
        directory) and any per-process shard files merged from its coverage/
        subdirectory. Every live registered command path (minus the injected
        check command) must be present in that union to pass; otherwise the
        check fails naming each uncovered command.

        Because the verdict reads the committed manifest, it is deterministic on
        every machine -- a machine that never ran the suite (no local shards)
        still gets a stable verdict from the committed manifest alone. Both the
        coverage dir and the manifest path sit under the DECLARED directory, so
        the check evaluated from any cwd reads the app's own repo state.

        The manifest is rewritten as the monotonic union of its prior contents
        and the freshly merged shards, but ONLY when that content actually
        changes -- a pure check must not dirty a byte-identical file. Accepted
        staleness: deleting a test leaves its command covered in the manifest
        until the manifest is deliberately regenerated (e.g. by removing it and
        re-running the suite), because the union never removes a command.
        """
        def impl(ctx: CheckContext, reporter: "ErrorReporter") -> "_CheckOutcome":
            coverage_dir = self._coverage_dir
            manifest_path = self._coverage_manifest_path

            # Subject-matter gating (the sanctioned skip class, mirroring
            # project-type gating): when the anchored coverage root holds NEITHER
            # a committed manifest NOR any shard files, this is not the app's own
            # development tree -- e.g. an installed app running its checks from a
            # foreign project's cwd, where the construction-anchored root points
            # at a directory with no coverage state. Report a visible SKIP instead
            # of failing with the app's entire command surface listed as
            # uncovered. When EITHER exists, behavior is unchanged: a partial
            # manifest still fails honestly, and an empty-manifest file present
            # still means "coverage configured but empty" = fail listing all.
            manifest_exists = bool(manifest_path) and os.path.isfile(manifest_path)
            shards_exist = bool(coverage_dir) and os.path.isdir(coverage_dir) and any(
                fname.endswith(".jsonl") for fname in os.listdir(coverage_dir)
            )
            if not manifest_exists and not shards_exist:
                anchor = os.path.dirname(manifest_path) if manifest_path else coverage_dir
                return reporter.skipped(
                    f"no coverage state at {anchor} -- cli-test-coverage applies "
                    "to the app's own development tree"
                )

            covered: set[str] = set()

            # Seed from the committed manifest -- this is what makes the verdict
            # deterministic on machines that never ran the suite.
            if manifest_path and os.path.isfile(manifest_path):
                try:
                    with open(manifest_path, encoding="utf-8") as f:
                        data = json.load(f)
                    if isinstance(data, list):
                        covered.update(c for c in data if isinstance(c, str))
                except (json.JSONDecodeError, OSError):
                    pass

            # Merge shards (optional freshness input)
            if coverage_dir and os.path.isdir(coverage_dir):
                for fname in os.listdir(coverage_dir):
                    if not fname.endswith(".jsonl"):
                        continue
                    fpath = os.path.join(coverage_dir, fname)
                    with open(fpath, encoding="utf-8") as f:
                        for line in f:
                            line = line.strip()
                            if not line:
                                continue
                            entry = json.loads(line)
                            if "command" in entry:
                                covered.add(entry["command"])

            # Rewrite the manifest as the monotonic union, but only when the
            # content actually changes (keeps a pure check from dirtying a
            # byte-identical file).
            if manifest_path and covered:
                new_content = json.dumps(sorted(covered), indent=2) + "\n"
                existing: str | None = None
                if os.path.isfile(manifest_path):
                    try:
                        with open(manifest_path, encoding="utf-8") as f:
                            existing = f.read()
                    except OSError:
                        existing = None
                if existing != new_content:
                    os.makedirs(os.path.dirname(manifest_path), exist_ok=True)
                    with open(manifest_path, "w", encoding="utf-8") as f:
                        f.write(new_content)
                    self._record_cache_write(manifest_path)

            # Compare against command surface (exclude the framework-injected
            # check command -- it is not a user command)
            all_commands = self._collect_all_command_paths()
            all_commands.discard("check")
            uncovered = sorted(all_commands - covered)

            if uncovered:
                for cmd in uncovered:
                    reporter.error(f"no test coverage for command: {cmd}")
                return reporter.found(
                    f"{len(uncovered)} command(s) with zero test coverage"
                )
            return reporter.passed(
                f"all {len(all_commands)} commands have test coverage"
            )

        return [
            error_check_spec(
                name="cli-test-coverage",
                tags=["test"],
                fast=True,
                pure=True,
                needs_network=False,
                depends_on=[],
                impl=impl,
            ),
        ]

    def _effects_bypass_provider(self) -> list[CheckSpec]:
        """Built-in check provider for the three effects-regime lints.

        Registered whenever the check system turns on, so a consumer that
        adopts checks at all gets all three without a TOML declaration:

        - ``effects-bypass`` (error) fails on any direct process,
          filesystem-mutation or network call REACHABLE FROM A REGISTERED
          COMMAND HANDLER. Its remediation is always "route it through
          ctx.effects", so a leaf the handle could not carry must never be a
          finding: the handle's closed method set has no in-process-observe
          method, which is why ``platform.system()`` is exempt while
          ``os.system(...)`` is not (see :data:`_BYPASS_PROCESS_OS_ONLY`);
        - ``observe-allowlist-breadth`` (warn) surfaces short
          ``proc_observe_allowlist`` prefixes, which authorize real execution
          under ``--dry-run``;
        - ``consequential-grant-agreement`` (warn) surfaces commands that
          declare a process- or network-mutating grant but do not declare
          themselves consequential.
        """
        def impl(ctx: CheckContext, reporter: "ErrorReporter") -> "_CheckOutcome":
            findings = _scan_effects_bypasses(Path(ctx.project_root))
            for rel, lineno, func_name, target in findings:
                reporter.error(
                    f"{rel}:{lineno}: {func_name} calls {target} directly; "
                    f"route it through ctx.effects"
                )
            if findings:
                return reporter.found(
                    f"{len(findings)} direct effect call(s) bypassing ctx.effects"
                )
            return reporter.passed("no direct effect calls bypass ctx.effects")

        def breadth_impl(ctx: CheckContext,
                         reporter: "WarnReporter") -> "_CheckOutcome":
            broad = [
                prefix for prefix in self._proc_observe_allowlist
                if len(prefix) == 1
            ]
            for prefix in broad:
                reporter.warn(_observe_allowlist_breadth_warning(prefix[0]))
            if broad:
                return reporter.found(
                    f"{len(broad)} single-token proc_observe_allowlist prefix(es)"
                )
            return reporter.passed(
                "no single-token proc_observe_allowlist prefixes"
            )

        def grant_agreement_impl(ctx: CheckContext,
                                 reporter: "WarnReporter") -> "_CheckOutcome":
            # Only the kinds that leave this process. A file_write or a
            # proc_spawn is local and ordinarily recoverable; a proc_mutate
            # runs another program and a net_mutate changes remote state, and
            # neither can be walked back by the framework. Widening this to
            # every grant kind would re-create the noise the consequential
            # declaration exists to remove.
            escaping = (PROC_MUTATE, NET_MUTATE)
            found = 0
            for cmd_path, cmd in self._collect_all_commands():
                if cmd.consequential:
                    continue
                for grant in cmd.grants:
                    if grant.kind not in escaping:
                        continue
                    found += 1
                    reporter.warn(_consequential_grant_warning(
                        cmd_path, grant.name, grant.kind,
                    ))
            if found:
                return reporter.found(
                    f"{found} grant(s) on non-consequential command(s)"
                )
            return reporter.passed(
                "every escaping grant sits on a consequential command"
            )

        return [
            error_check_spec(
                name="effects-bypass",
                tags=["effects", "quality"],
                fast=True,
                pure=True,
                needs_network=False,
                depends_on=[],
                impl=impl,
            ),
            warn_check_spec(
                name="observe-allowlist-breadth",
                tags=["effects", "quality"],
                fast=True,
                pure=True,
                needs_network=False,
                depends_on=[],
                impl=breadth_impl,
            ),
            warn_check_spec(
                name="consequential-grant-agreement",
                tags=["effects", "quality"],
                fast=True,
                pure=True,
                needs_network=False,
                depends_on=[],
                impl=grant_agreement_impl,
            ),
        ]

    @property
    def config_file_path(self) -> str:
        """Return the resolved config file path for this app."""
        return _config_path(self.name, override=self.config_path, config_format=self.config_format)

    def dump_schema_dict(self) -> dict:
        """Return the app's full schema as a dict, excluding ``project_id``.

        This is the public, CWD-free accessor for the schema. Unlike the
        ``--dump-schema`` flag (which writes ``.strictcli/schema.json`` and
        derives ``project_id`` from ``pyproject.toml`` in the current working
        directory), this method reads only the in-memory ``App`` and performs
        no filesystem or CWD access. The returned dict is byte-identical to the
        written schema file with the ``project_id`` field removed.
        """
        return _dump_schema_core(self)

    def config_field(
        self,
        name: str,
        type: type,
        help: str,
        default: object = _MISSING,
    ) -> ConfigField:
        """Declare a typed config file field.

        Args:
            name: Field name. Dots allowed for TOML sections (e.g. "serve.port").
                  Names starting with underscore are reserved for framework fields.
            type: Field type — str, bool, int, or float.
            help: Help text describing the field.
            default: Default value. If omitted, the field is required.

        Returns:
            The registered ConfigField.

        Raises:
            ValueError: If the name is invalid, duplicated, reserved, or
                        the default doesn't match the declared type.
        """
        if name.startswith("_"):
            raise ValueError(
                f'config field name "{name}" is reserved: '
                f"names starting with underscore are reserved for framework fields"
            )
        if name in self._config_fields:
            raise ValueError(f'duplicate config field name "{name}"')
        if name in self._framework_fields:
            raise ValueError(
                f'config field name "{name}" conflicts with framework field'
            )
        cf = ConfigField(name=name, type=type, help=help, default=default)
        # A config field colliding with an existing flag's param name is a
        # validation-only declaration that annotates the flag; their defaults
        # must agree. Flags registered after this field are checked from the
        # command-builder side instead.
        for f in self._collect_all_flags():
            if _flag_param_name(f.name) == name:
                _check_flag_configfield_default(f.name, f.presence, f.default, cf)
        self._config_fields[name] = cf
        return cf

    def _register_framework_field(
        self,
        name: str,
        type: type,
        help: str,
    ) -> ConfigField:
        """Register a framework-owned config field (e.g. _schema_version).

        Framework fields must start with underscore. They are declared by the
        framework, not the user, and cannot conflict with user fields.
        """
        if not name.startswith("_"):
            raise ValueError(
                f'framework field name "{name}" must start with underscore'
            )
        if name in self._framework_fields:
            raise ValueError(f'duplicate framework field name "{name}"')
        if name in self._config_fields:
            raise ValueError(
                f'framework field name "{name}" conflicts with user config field'
            )
        # Framework fields are always optional (no default required from user).
        # Use _MISSING as default since they are managed internally.
        cf = ConfigField(name=name, type=type, help=help)
        self._framework_fields[name] = cf
        return cf

    def error_check(self, name: str) -> Callable[[F], F]:
        """Decorator registering an error-severity check implementation.

        The decorated function takes ``(ctx, reporter)`` where ``reporter`` is
        an :class:`ErrorReporter` (annotate it as such for mypy binding). It
        must return a :class:`_CheckOutcome` obtained from that reporter. The
        check must be declared ``severity = "error"`` in checks.toml.
        """
        return self._make_check_decorator(name, "error")

    def warn_check(self, name: str) -> Callable[[F], F]:
        """Decorator registering a warn-severity check implementation.

        The decorated function takes ``(ctx, reporter)`` where ``reporter`` is
        a :class:`WarnReporter` (which structurally lacks ``error``, so a warn
        check cannot cascade). The check must be declared ``severity = "warn"``.
        """
        return self._make_check_decorator(name, "warn")

    def _make_check_decorator(self, name: str, form: str) -> Callable[[F], F]:
        """Build the shared registration decorator for error/warn checks.

        Enforces the double-entry contract (declared vs registered) and
        cross-checks the registration FORM against the TOML-declared severity so
        that ``@app.error_check`` on a severity="warn" definition is a hard error.
        """
        def decorator(fn: F) -> F:
            if not self._checks_enabled:
                raise ValueError(
                    f'cannot register check "{name}": '
                    f"checks not enabled"
                )
            if name not in self._check_defs:
                raise ValueError(
                    f'cannot register check "{name}": '
                    f"not declared in checks.toml"
                )
            cdef = self._check_defs[name]
            if cdef.impl is not None:
                raise ValueError(f'check "{name}": duplicate registration')
            if cdef.severity != form:
                used = f"@app.{form}_check"
                want = f"@app.{cdef.severity}_check"
                raise ValueError(
                    f'check "{name}": declared severity "{cdef.severity}" in '
                    f"checks.toml but registered via {used}; use {want}"
                )
            reporter_cls = ErrorReporter if form == "error" else WarnReporter

            def run(ctx: CheckContext) -> _CheckOutcome:
                reporter = reporter_cls()
                return fn(ctx, reporter)

            cdef.impl = run
            cdef.impl_form = form
            return fn
        return decorator

    def _validate_check_registrations(self) -> str | None:
        """Validate that all declared checks have registered implementations.

        Returns an error message if any are missing, or None if all OK.
        """
        if not self._checks_enabled:
            return None
        missing = sorted(
            name for name, cdef in self._check_defs.items()
            if cdef.impl is None
        )
        if missing:
            return (
                "checks declared in checks.toml but not registered: "
                + ", ".join(missing)
            )
        return None

    def tag_contract(self, tag: str, *, requires_flag: str) -> None:
        """Declare that any command with the given tag must have the named flag."""
        if not _IDENTIFIER_RE.fullmatch(tag):
            raise ValueError(f'invalid tag name "{tag}": must match [a-z][a-z0-9-]*')
        self._tag_contracts[tag] = requires_flag

    def _validate_tag_contracts(self) -> str | None:
        """Check that all tag contracts are satisfied.

        Returns an error message if any command violates a contract, or None.
        """
        if not self._tag_contracts:
            return None

        def _check_commands(commands: dict) -> str | None:
            for cmd in commands.values():
                if cmd.passthrough is not None:
                    continue
                for tag in cmd.tags:
                    if tag in self._tag_contracts:
                        required_flag = self._tag_contracts[tag]
                        flag_names = {f.name for f in cmd.flags} | {f.name for f in self._global_flags}
                        if required_flag not in flag_names:
                            return (
                                f'command "{cmd.name}": tag "{tag}" requires '
                                f'flag "--{required_flag}"'
                            )
            return None

        def _check_groups(groups: dict) -> str | None:
            for group in groups.values():
                err = _check_commands(group.commands)
                if err:
                    return err
                err = _check_groups(group._groups)
                if err:
                    return err
            return None

        err = _check_commands(self._commands)
        if err:
            return err
        return _check_groups(self._groups)

    def _resolve_config_data(
        self,
        runtime_path_override: str | None = None,
        hermetic: bool = False,
        is_runtime_flag: bool = False,
    ) -> _ConfigLoadResult:
        """Single entry point for all config loading.

        is_runtime_flag indicates the path came from --config (hard error on missing).
        """
        if hermetic:
            return _ConfigLoadResult()
        override = runtime_path_override or self.config_path
        return _load_config(
            self.name,
            config_path_override=override,
            config_format=self.config_format,
            is_runtime_flag=is_runtime_flag,
        )

    def _validate_config_fields(self, cmd: Command, config_data: dict) -> str | None:
        """Validate config file contents against the command's bound config fields.

        Checks:
        1. Each bound required config field exists in config with the correct type.
        2. Each key in config matches a registered flag, config field, or framework
           field. Unknown keys are hard errors.

        Returns an error message string, or None if all OK.
        """
        # Check bound required config fields exist with correct type
        for cf_name in cmd.config_fields:
            cf = self._config_fields.get(cf_name)
            if cf is None:
                # Should not happen (validated at registration), but be defensive
                return f'config field "{cf_name}" is not registered'
            found, value = _nested_get(config_data, cf_name)
            if not found:
                if cf.required:
                    return (
                        f'required config field "{cf_name}" is missing from '
                        f"config file"
                    )
                # Optional and missing -- that is fine
                continue
            # Validate type
            err = _check_config_field_type(cf, value)
            if err:
                return err

        # Check all keys in config file are known
        all_config_keys = _collect_nested_keys(config_data)
        # Build set of known keys
        all_flags = self._collect_all_flags()
        known_flag_keys = {_flag_param_name(f.name) for f in all_flags}
        known_field_keys = set(self._config_fields.keys())
        known_framework_keys = set(self._framework_fields.keys())

        for key in all_config_keys:
            if key in known_flag_keys:
                continue
            if key in known_field_keys:
                continue
            if key in known_framework_keys:
                continue
            return f'unknown key "{key}" in config file'

        return None

    def set_check_context(self, factory: Callable) -> None:
        """Set the factory function that creates CheckContext for check runs.

        The factory is called with no arguments and must return a CheckContext.
        """
        self._check_context_factory = factory

    def _wrap_check_context(self, base):
        """Augment a tool-supplied check context with connection-env access
        (hermetic-suppressed). When no connection envs are declared, the base
        context is returned unchanged so the common case is unaffected."""
        if not self._connection_envs:
            return base
        return _CheckContextWithConn(base, self._connection_env_names, self._last_hermetic)

    def set_scope_adapter(self, adapter: Callable) -> None:
        """Set the scope adapter callback for scoped checks.

        The adapter is called as ``adapter(context, scope_string)`` and must
        return one of:

        - a replacement context object -- used as the check's context (context
          projection), or
        - a :class:`SkipCheck` directive -- skips the check with the given
          reason (no cascade, no exit-code change).

        The adapter can no longer mint arbitrary outcomes: it either projects
        the context or skips. (This is the Python-only scope hook; Go has no
        scope adapter -- see the note in the Go ``check.go``.)
        """
        self._scope_adapter = adapter

    def register_check_provider(
        self, provider: Callable[[], list[CheckSpec]],
    ) -> None:
        """Register a provider that supplies check specs at materialization time.

        Three check-system hooks (do not confuse them):

        1. Check provider (this method) -- REGISTRY POPULATION. A provider
           returns a list of fully-formed check specs (metadata + a ceiling-typed
           impl). Providers are the TOML-less way to add checks: they run lazily
           at the first registry read (materialization) and their specs go
           through the same single add-path as TOML-declared checks, so a name
           colliding with a TOML check or another provider's check is the usual
           hard error. Registering a provider ENABLES the check system (a
           TOML-less app with a provider gets a working ``check`` command).
        2. Check-context factory (:meth:`set_check_context`) -- PROJECT
           CONSTRUCTION. Called once per run to build the CheckContext handed to
           every check impl. Answers "what project are we checking?".
        3. Scope adapter (:meth:`set_scope_adapter`, Python-only) -- PER-CHECK
           CONTEXT PROJECTION. Called per scoped check to project the context or
           skip the check.

        A provider decides WHICH checks exist; the context factory decides WHAT
        project they see; the scope adapter decides HOW an individual check sees
        that project.

        A provider that returns an empty list is honest-empty (no checks for
        this context) and a valid no-op. A provider that raises is a hard error
        in every mode.

        Reentrancy: a provider must not trigger check execution during
        materialization (e.g. by calling :meth:`run_checks` or the check
        command). Doing so re-enters materialization while it is in progress --
        behavior is undefined (unbounded recursion). A provider's job is to
        return specs, nothing else.
        """
        if not callable(provider):
            raise ValueError("check provider must be callable")
        self._enable_checks()
        self._check_providers.append(provider)
        # Registering a new provider invalidates any prior materialization.
        self._provider_materialized_cwd = None

    def reset_check_provider_cache(self) -> None:
        """Drop provider-sourced definitions and clear the materialization memo.

        The next registry read re-runs all providers. Intended for tests and
        long-lived singletons. Does NOT unregister the providers themselves.
        """
        for name in self._provider_sourced_names:
            self._check_defs.pop(name, None)
        self._provider_sourced_names = set()
        self._provider_materialized_cwd = None

    def _materialize_check_providers(self) -> None:
        """Run providers and insert their specs, memoized on the cwd.

        Single chokepoint called at the start of every registry read (the check
        command handler and :meth:`run_checks`). A repeat call in the same cwd
        is a cheap no-op; a cwd change re-runs the providers (dropping the
        previous provider-sourced defs first).
        """
        if not self._check_providers:
            return
        cwd = os.getcwd()
        if self._provider_materialized_cwd == cwd:
            return
        # First materialization or cwd changed: drop stale provider defs, re-run.
        for name in self._provider_sourced_names:
            self._check_defs.pop(name, None)
        self._provider_sourced_names = set()
        for provider in self._check_providers:
            result = provider()  # a raising provider is a hard error in every mode
            if result is None:
                result = []
            if not isinstance(result, (list, tuple)):
                raise ValueError(
                    f"check provider must return a list of CheckSpec, "
                    f"got {type(result).__name__}"
                )
            for spec in result:
                if not isinstance(spec, CheckSpec):
                    raise ValueError(
                        f"check provider returned a non-CheckSpec value: {spec!r}"
                    )
                if spec.severity != spec._impl_form:
                    used = f"{spec._impl_form}_check_spec"
                    want = f"{spec.severity}_check_spec"
                    raise ValueError(
                        f'check "{spec.name}": declared severity '
                        f'"{spec.severity}" but registered via {used}; '
                        f"use {want}"
                    )
                cdef = _CheckDef(
                    name=spec.name, tags=list(spec.tags), severity=spec.severity,
                    fast=spec.fast, pure=spec.pure,
                    needs_network=spec.needs_network,
                    depends_on=list(spec.depends_on), scope=spec.scope,
                    impl=spec._impl, impl_form=spec._impl_form,
                )
                # Routes through the single add-path: a name colliding with a
                # TOML check or another provider's check is the usual hard error.
                self._add_check_def(cdef)
                self._provider_sourced_names.add(spec.name)
        self._provider_materialized_cwd = cwd

    def run_checks(
        self,
        context: CheckContext,
        *,
        tag_expr: str | None = None,
        name_glob: str | None = None,
        run_all: bool = False,
        ignore_warnings: bool = False,
        pure_only: bool = False,
    ) -> tuple[list[CheckRunResult], list[str], int]:
        """Run checks programmatically with filtering and dependency resolution.

        Returns (results, impure_listed, exit_code):

        - results: the executed checks as a list of CheckRunResult.
        - impure_listed: the ordered names of checks NOT executed because of the
          purity partition (empty unless ``pure_only`` is set). Listed checks
          contribute nothing to the exit code -- a consumer renders them as e.g.
          ``"would run: <name> (impure)"``.
        - exit_code: 0 if all executed checks pass (or all warn with
          ``ignore_warnings``), else 1.

        With ``pure_only`` set, only checks that are declared pure AND do not
        need network access execute; every other selected check (including a
        pure check that depends on a listed one) is listed instead. The default
        (``pure_only`` False) is byte-identical to the previous behavior.
        """
        if not self._checks_enabled:
            raise ValueError("checks are not enabled on this App")
        # Materialize provider-sourced checks before any registry read.
        self._materialize_check_providers()
        err = self._validate_check_registrations()
        if err:
            raise ValueError(err)
        selected = _filter_checks(self._check_defs, tag_expr, name_glob, run_all)
        if not selected:
            return ([], [], 0)
        order = _resolve_check_order(self._check_defs, selected)
        raw_results, impure_listed, exit_code = _run_checks(
            self._check_defs, order, context, ignore_warnings,
            scope_adapter=self._scope_adapter, pure_only=pure_only,
        )
        results = [
            CheckRunResult(name=name, outcome=outcome, duration_ms=duration_ms)
            for name, outcome, duration_ms in raw_results
        ]
        return (results, impure_listed, exit_code)

    def _enable_checks(self) -> None:
        """Turn on the check system exactly once.

        Flips ``_checks_enabled``, initializes the check registry if it is not
        already present, and registers the auto-generated ``check`` command a
        single time. Idempotent: calling it again is a no-op. Callers (currently
        the TOML-loading branches) route through this so that future check
        sources share the same enablement path.
        """
        if getattr(self, "_checks_enabled", False):
            return
        self._checks_enabled = True
        if not hasattr(self, "_check_defs"):
            self._check_defs: dict[str, _CheckDef] = {}
        self._register_check_command()
        # The built-in effects-bypass lint rides the same provider hook the
        # built-in cli-test-coverage check uses. Appended directly (not through
        # register_check_provider) because that method routes back here.
        self._check_providers.append(self._effects_bypass_provider)
        self._provider_materialized_cwd = None

    def _add_check_def(self, cdef: _CheckDef) -> None:
        """Single internal insertion point for check definitions.

        Rejects duplicate names as a hard error and inserts the definition into
        the registry. TOML loading routes through here; this is also the future
        insertion point for provider-sourced definitions.
        """
        if cdef.name in self._check_defs:
            raise ValueError(f'duplicate check definition "{cdef.name}"')
        self._check_defs[cdef.name] = cdef

    def _register_check_command(self) -> None:
        """Register the auto-generated 'check' command when checks.toml exists."""
        app_ref = self  # capture for closure

        def _check_handler(
            ctx, *, all: bool, tag: str, name: str,
            list: bool, ignore_warnings: bool,
            **_kw,
        ) -> int:
            # --verbose, --dry-run and --json are framework-owned reserved
            # names, so the check command declares none of them and reads their
            # values off the Context instead. The machine output is this
            # command's payload (contract §19.4), which is why --json is not a
            # flag here any more. The handler never branches on ctx.json: the
            # payload call is mode-independent and every human line goes
            # through a context writer, so machine mode carries the text as
            # diagnostics and stdout keeps exactly one document (§19.1, §19.4).
            verbose = ctx.verbose
            dry_run = ctx.dry_run
            # Materialize provider-sourced checks before any registry read
            # (covers the list, dry-run, and execution branches below).
            app_ref._materialize_check_providers()
            # Treat empty strings as "not provided"
            tag_expr = tag if tag else None
            name_glob = name if name else None

            if list:
                ctx.payload(_check_list_items(app_ref._check_defs))
                _check_list_mode(app_ref._check_defs, ctx)
                return 0

            # Determine if any execution filter is active
            has_filter = all or tag_expr is not None or name_glob is not None

            if not has_filter:
                # No flags: show help for the check command
                check_cmd = app_ref._commands["check"]
                prefix = app_ref._find_command_prefix(check_cmd)
                ctx.info(_format_command_help(app_ref, check_cmd, prefix))
                return 0

            # Resolve filters and order
            selected = _filter_checks(app_ref._check_defs, tag_expr, name_glob, all)
            if not selected:
                ctx.info("No checks matched the given filters.")
                return 0
            order = _resolve_check_order(app_ref._check_defs, selected)

            # Both a full run and a dry run execute checks, so both need a
            # context. --dry-run selects the purity partition instead of a
            # separate list-without-running branch: the checks declared pure
            # really run (that is what makes a rehearsal mean something) and the
            # impure remainder is rendered as the would-run plan.
            if app_ref._check_context_factory is None:
                ctx.error(
                    "error: no check context configured. "
                    "Call app.set_check_context(factory) before running."
                )
                return 1
            context = app_ref._wrap_check_context(app_ref._check_context_factory())
            raw_results, impure_listed, exit_code = _run_checks(
                app_ref._check_defs, order, context, ignore_warnings,
                scope_adapter=app_ref._scope_adapter, pure_only=dry_run,
            )

            results_wrapped = [
                CheckRunResult(name=n, outcome=o, duration_ms=d)
                for n, o, d in raw_results
            ]
            ctx.payload(_check_result_items(results_wrapped))
            output = format_check_results(results_wrapped, verbose)
            if output:
                ctx.info(output)
            if dry_run:
                _check_dry_run_mode(
                    app_ref._check_defs, impure_listed, order, ctx,
                )

            return exit_code

        # Filter out extra flags that already exist as global flags to avoid
        # collisions -- the handler receives global flag values automatically.
        global_flag_names = {gf.name for gf in self._global_flags}
        candidate_extra_flags = [
            Flag(name="all", type=bool, default=False, help="Run every registered check regardless of tag or name filters"),
            Flag(name="tag", type=str, default="", help="Tag DSL expression to select checks (e.g. 'changelog & !quality')"),
            Flag(name="name", type=str, default="", help="Glob pattern to filter checks by name (e.g. 'hash-*', '*coverage*')"),
            Flag(name="list", type=bool, default=False, help="List all registered checks with their tags and exit without running"),
            Flag(name="ignore-warnings", type=bool, default=False, help="Treat warn-severity results as passing so they do not cause nonzero exit"),
        ]
        extra_flags = [f for f in candidate_extra_flags if f.name not in global_flag_names]
        # read_only: the check command's only writes are framework-blessed
        # CACHE_WRITEs (the coverage manifest), which never trip enforcement.
        self._commands["check"] = self._build_framework_command(
            "check",
            help="Run project checks registered via the check framework and report results",
            effect=EFFECT_READ_ONLY,
            handler=_check_handler,
            extra_flags=extra_flags,
            payload_schema=_CHECK_PAYLOAD_SCHEMA,
        )

    def command(
        self,
        name: str,
        *,
        help: str,
        effect: str | None = None,
        consequential: bool = False,
        dry_run_supported: bool = True,
        dry_run_unsupported_reason: str | None = None,
        update_of: "UpdateOf | None" = None,
        payload_schema: dict | None = None,
        owns_stdout: bool = False,
        args: list[Arg] | None = None,
        flag_sets: list[FlagSet] | None = None,
        constraints: list[AtLeastOne | AllOrNone | Requires | Implies] | None = None,
        passthrough: Passthrough | None = None,
        grants: list[Grant] | None = None,
        forwarding: Forwarding | None = None,
        tags: set[str] | None = None,
        hidden: bool = False,
        interactive: bool = False,
        config_fields: list[str] | None = None,
    ) -> Callable[[F], F]:
        """Decorator to register a top-level command."""

        def decorator(func: F) -> F:
            handler_localns = dict(sys._getframe(1).f_locals)
            cmd = _build_and_validate_command(
                name,
                help=help,
                effect=effect,
                consequential=consequential,
                dry_run_supported=dry_run_supported,
                dry_run_unsupported_reason=dry_run_unsupported_reason,
                update_of=update_of,
                payload_schema=payload_schema,
                owns_stdout=owns_stdout,
                handler=func,
                args=args,
                flag_sets=flag_sets,
                constraints=constraints,
                env_prefix=self.env_prefix,
                global_flags=self._global_flags,
                passthrough=passthrough,
                grants=grants,
                forwarding=forwarding,
                tags=tags,
                inherited_tags=None,
                hidden=hidden,
                interactive=interactive,
                config_fields=config_fields,
                config_fields_ref=self._config_fields,
                infra_root_names=self._infra_root_names,
                connection_env_names=self._connection_env_names,
                handler_localns=handler_localns,
            )
            self._commands[name] = cmd
            return func

        return decorator

    def group(self, name: str, *, help: str, tags: set[str] | None = None,
              hidden: bool = False) -> Group:
        """Create and register a command group."""
        own_tags = frozenset(tags or set())
        grp = Group(name=name, help=help, env_prefix=self.env_prefix,
                     _global_flags=self._global_flags,
                     tags=own_tags,
                     _accumulated_tags=own_tags,
                     hidden=hidden,
                     _config_fields_ref=self._config_fields,
                     _infra_root_names=self._infra_root_names,
                     _connection_env_names=self._connection_env_names)
        self._groups[name] = grp
        return grp

    def deprecate(self, name: str, *, message: str,
                  effect: str | None = None) -> None:
        """Register a deprecated top-level command.

        Deprecated entries are classification-EXEMPT: they have no handler and
        execute nothing, so passing ``effect=`` is a registration-time error.
        """
        if effect is not None:
            _raise_deprecated_command_effect(name)
        if not name or not name.strip():
            raise ValueError("deprecated command name must be a non-empty string")
        if not message or not message.strip():
            raise ValueError(f'deprecated command "{name}": message must not be empty')
        if name in self._commands:
            raise ValueError(
                f'deprecated command "{name}" collides with an existing command'
            )
        if name in self._groups:
            raise ValueError(
                f'deprecated command "{name}" collides with an existing group'
            )
        if name in self._deprecated:
            raise ValueError(
                f'deprecated command "{name}" is already registered'
            )
        self._deprecated[name] = DeprecatedCommand(name=name, message=message)

    def _collect_all_flags(self) -> list[Flag]:
        """Collect all flags (global + all commands in all groups), for config show."""
        flags: list[Flag] = list(self._global_flags)
        seen_names: set[str] = {f.name for f in flags}
        for cmd in self._commands.values():
            for f in cmd.flags:
                if f.name not in seen_names:
                    flags.append(f)
                    seen_names.add(f.name)

        def _collect_from_group(grp: Group) -> None:
            for cmd in grp.commands.values():
                for f in cmd.flags:
                    if f.name not in seen_names:
                        flags.append(f)
                        seen_names.add(f.name)
            for sub in grp._groups.values():
                _collect_from_group(sub)

        for name, grp in self._groups.items():
            if name == "config":
                continue  # skip auto-generated config group
            _collect_from_group(grp)
        return flags

    def _colliding_config_fields(self) -> dict[str, ConfigField]:
        """Return {flag_param_name: ConfigField} for config fields whose name
        equals a flag's param name.

        Such config fields are validation-only: they annotate the colliding
        flag rather than rendering as a separate config key. Callers use this to
        render the key once (on the flag line, with the config field's help as a
        trailing annotation).
        """
        flag_params = {_flag_param_name(f.name) for f in self._collect_all_flags()}
        return {
            name: cf
            for name, cf in self._config_fields.items()
            if name in flag_params
        }

    def _confirm_consequential(self, cmd: "Command", cmd_path: str) -> None:
        """The framework-owned confirm protocol.

        Fires before dispatching a command that DECLARES ITSELF consequential,
        on the real CLI path, when neither --dry-run nor
        --approve-consequential was passed. A plain ``mutating`` command never
        prompts: classification answers "should a dry run record rather than
        execute?", which is a different question from "are these effects worth
        interrupting someone for?". Never fires on the programmatic paths
        (test/call/_invoke/MCP), which have no TTY contract and would hang.

        A consequential PASSTHROUGH is not exempt: the framework knows LESS
        about what is about to happen, not more.
        """
        if not cmd.consequential:
            return
        if self._last_dry_run or self._last_approve_consequential:
            return
        io = self._confirm_io
        if not io.is_interactive():
            print(_msg_confirm_non_interactive(), file=sys.stderr)
            sys.exit(1)
        print(_msg_confirm_prompt(cmd_path), file=sys.stderr, end="", flush=True)
        try:
            answer = io.read_line()
        except (EOFError, KeyboardInterrupt):
            answer = ""
        if _strip_confirm_line(answer) not in ("y", "Y"):
            print(_msg_confirm_declined(), file=sys.stderr)
            sys.exit(1)

    def _arm_effects(self, cmd: "Command", cmd_path: str, *,
                     dry_run: bool, out=None) -> "_Effects":
        """Arm the effects handle for one dispatch (the runtime seal).

        Called at EVERY ctx-construction site that dispatches a handler, so
        there is no path on which ctx.effects is missing or a carrier escapes
        unpoisoned. The log itself is reset by :meth:`_begin_dispatch`, which
        runs earlier so pre-handler CACHE_WRITEs (coverage shards) land in the
        same dispatch's log.
        """
        return _Effects(
            cmd=cmd,
            cmd_path=cmd_path,
            dry_run=dry_run,
            log=self._effect_log,
            allowlist=self._proc_observe_allowlist,
            trace=_TraceIdentity(
                app=self.name,
                version=self.version,
                command=cmd_path,
                dry_run=dry_run,
                machine_mode=self._last_json,
                quiet=self._last_quiet,
                verbose=self._last_verbose,
                approve_consequential=self._last_approve_consequential,
                effect=cmd.effect,
            ),
            out=out,
            json=self._last_json,
        )

    def _begin_dispatch(self) -> None:
        """Start a new dispatch: reset the structured effect log."""
        self._effect_log = _EffectLog()

    def _render_dry_log(self, cmd_path: str, out, err, *, aborted: bool) -> None:
        """Write the would-do log for a dry run. No-op outside dry mode.

        Called on every exit path out of a dispatch, so a handler that leaves
        through ``sys.exit`` or an exception still shows the preview it was
        asked for. The log always goes to stdout and is never suppressed by
        ``--quiet``: it is dry mode's primary output.

        ``aborted`` marks a dispatch that did not finish. The log is still
        written -- the recorded effects are owed either way -- and the marker
        that follows it on stderr says the reader cannot assume the list is
        the whole preview. The truncation path (which ends the preview for its
        own pinned reason) renders itself and never comes through here.
        """
        if not self._last_dry_run:
            return
        # A handler that claimed the render AND produced the bytes already has
        # the log in the stream; re-emitting it here would duplicate it. A
        # claim that never rendered falls through and is rendered (§19.7).
        if not self._effect_log.seam_suppressed():
            print(self._effect_log.render(), file=out)
        if aborted:
            print(
                _msg_dry_run_aborted(self._effect_log.next_seq(), cmd_path),
                file=err,
            )

    def _record_cache_write(self, path: str) -> None:
        """Record a framework-blessed CACHE_WRITE.

        The closed list of sites is exactly three: the schema dump, the
        test-coverage shards, and the test-coverage manifest. CACHE_WRITEs have
        no public method, never appear in the would-do log, never trip
        read-only enforcement, and EXECUTE even in dry mode -- which is why
        they always carry ``recorded: false``.
        """
        log = self._effect_log
        log.append(_EffectRecord(
            seq=log.next_cache_seq(),
            kind=CACHE_WRITE,
            verb="cache",
            detail=path,
            recorded=False,
        ))

    def effect_log(self) -> list[dict]:
        """Return the structured effect records of the most recent dispatch.

        Public API (contract §14.3's amendment). It is the envelope's source
        (§19.3), so it is part of the surface consumers may rely on and it is
        in the api-surface catalog rather than excluded from it. The records
        are populated in both modes, so a live run's effects read as readily as
        a dry run's.
        """
        return self._effect_log.to_list()

    def _set_confirm_io(self, io: "_ConfirmIO | None") -> None:
        """Swap the stdin side of the confirm protocol (test-only seam).

        ``None`` restores the real stdin reader. The TypeScript twin is
        ``setConfirmIO`` in ``confirm.ts`` and the Go twin is
        ``App.SetConfirmIO``; all three change WHERE the answer comes from,
        never WHETHER the protocol runs. Private by name because Python has no
        package-private visibility -- this is not public API.
        """
        self._confirm_io = io if io is not None else _REAL_CONFIRM_IO

    def _build_framework_command(
        self,
        name: str,
        *,
        help: str,
        effect: str,
        handler: Callable,
        args: list[Arg] | None = None,
        extra_flags: list[Flag] | None = None,
        interactive: bool = False,
        payload_schema: dict | None = None,
        owns_stdout: bool = False,
    ) -> Command:
        """Build one of strictcli's own auto-registered commands.

        Framework-internal commands (``check`` and the five ``config``
        subcommands) go through the same single validated registration path as
        every consumer command -- there is no direct-``Command``-construction
        bypass left. Their handlers absorb the app's app-defined global flag
        values through ``**kwargs``, which is legal only because they declare
        forwarding, and the private ``_framework_internal`` marker (unreachable
        from any public factory) makes the framework verify that the handler is
        actually defined in this module.
        """
        return _build_and_validate_command(
            name,
            help=help,
            effect=effect,
            handler=handler,
            args=args,
            flag_sets=None,
            constraints=None,
            env_prefix=self.env_prefix,
            global_flags=self._global_flags,
            passthrough=None,
            forwarding=Forwarding(reason=_FRAMEWORK_INTERNAL_FORWARDING_REASON),
            framework_internal=True,
            extra_flags=extra_flags,
            interactive=interactive,
            payload_schema=payload_schema,
            owns_stdout=owns_stdout,
        )

    def _register_config_group(self) -> None:
        """Register the auto-generated 'config' command group."""
        config_grp = Group(
            name="config",
            help="Manage persistent configuration values stored in the config file",
            env_prefix=self.env_prefix,
            _global_flags=self._global_flags,
        )

        app_ref = self  # capture for closures

        # config path
        def _config_path_handler(ctx, **_kw) -> None:
            ctx.info(_config_path(
                app_ref.name,
                override=app_ref.config_path,
                config_format=app_ref.config_format,
            ))

        config_grp.commands["path"] = self._build_framework_command(
            "path",
            help="Print the absolute path to this application's config file and nothing else, so the value can be piped straight into another command. The path is $XDG_CONFIG_HOME/<app>/config.<toml|json> (falling back to ~/.config), or the explicit override the application was built with. Printing it does not create the file, and reports the same path whether or not one exists yet.",
            effect=EFFECT_READ_ONLY,
            handler=_config_path_handler,
        )

        # config show
        #
        # Source resolution uses the shared precedence chain: env > config > default.
        # "cli" is structurally impossible here -- config show is a subcommand,
        # so the app's own flags were never passed on the command line.
        def _config_show_handler(ctx, **_kw) -> int:
            # If there was a config parse error, show it instead of values
            if app_ref._config_parse_err:
                ctx.error(f"error: {app_ref._config_parse_err}")
                return 1
            # --json is framework-owned (contract §19.1): the object below is
            # this command's payload, not a locally-flagged print, and it is
            # supplied UNCONDITIONALLY (§19.4). Instance validation lives at the
            # emission seam, so a config value machine mode could not carry --
            # a float above 2^53 -- costs the human rendering nothing.
            config_data = app_ref._config_data
            all_flags = app_ref._collect_all_flags()
            colliding = app_ref._colliding_config_fields()
            result = {}
            for f in all_flags:
                param = _flag_param_name(f.name)
                value, source = _resolve_flag_show_source(f, config_data)
                # The SAME serializer the dumped schema uses (§13, §25.10): a
                # `RelativeToRoot` default is not a JSON value, and this
                # surface prints what the configuration says rather than what
                # a run would produce, so it publishes the declared env var
                # and path parts and never the resolved path.
                result[param] = {
                    "value": _serialize_default_value(value), "source": source,
                }
            # Include config fields (skip those colliding with a flag: they
            # are validation-only and render once, on the flag entry).
            for cf_name, cf in app_ref._config_fields.items():
                if cf_name in colliding:
                    continue
                found, value = _nested_get(config_data, cf_name)
                if found:
                    source = "config"
                elif not isinstance(cf.default, _MissingSentinel):
                    value = cf.default
                    source = "default"
                else:
                    value = None
                    source = "not set"
                entry: dict = {
                    "value": value,
                    "source": source,
                    "type": cf.type.__name__,
                    "required": cf.required,
                    "help": cf.help,
                }
                if not isinstance(cf.default, _MissingSentinel):
                    entry["default"] = cf.default
                result[cf_name] = entry
            # Infrastructure section (roots + handshakes + connections)
            if app_ref._infra_root_order or app_ref._handshake_order or app_ref._connection_order:
                infra: dict = {}
                for ev in app_ref._infra_root_order:
                    infra[ev] = {
                        "kind": "root",
                        "source": "env" if app_ref._infra_root_from_env[ev] else "default",
                        "resolved": app_ref._infra_roots[ev],
                    }
                for ev in app_ref._handshake_order:
                    is_set = ev in os.environ
                    hs_entry: dict = {
                        "kind": "handshake",
                        "set": is_set,
                        "help": app_ref._handshake_envs[ev],
                    }
                    if is_set:
                        hs_entry["value"] = os.environ[ev]
                    infra[ev] = hs_entry
                for ev in app_ref._connection_order:
                    is_set = ev in os.environ
                    conn_entry: dict = {
                        "kind": "connection",
                        "set": is_set,
                        "help": app_ref._connection_envs[ev],
                    }
                    if is_set:
                        conn_entry["value"] = os.environ[ev]
                    infra[ev] = conn_entry
                result["__infrastructure__"] = infra
            # Sorted keys at every level: the three implementations build
            # this object in three orders (Go marshals a map, which sorts
            # recursively), and the payload is compared byte-for-byte by
            # conformance.
            ctx.payload(_deep_sorted(result))

            # The human rendering is unconditional too, and goes through the
            # context writers: one info call per line, so nothing here can put a
            # second document on stdout, and in machine mode the same lines ride
            # the envelope's diagnostics (§19.1) exactly as the check command's
            # table does.
            for f in all_flags:
                param = _flag_param_name(f.name)
                value, source = _resolve_flag_show_source(f, config_data)
                line = f"{param} = {_format_config_value(value)}  (source: {source})"
                # A colliding config field annotates the flag line (rendered once).
                cf_collide = colliding.get(param)
                if cf_collide is not None:
                    line += f"  -- {cf_collide.help}"
                ctx.info(line)
            # Include config fields in plain output (skip colliding ones: they
            # are rendered as an annotation on the flag line above).
            non_colliding_fields = {
                n: cf for n, cf in app_ref._config_fields.items()
                if n not in colliding
            }
            if non_colliding_fields:
                ctx.info("")
                ctx.info("Config fields:")
                for cf_name, cf in non_colliding_fields.items():
                    found, value = _nested_get(config_data, cf_name)
                    if found:
                        source = "config"
                    elif not isinstance(cf.default, _MissingSentinel):
                        value = cf.default
                        source = "default"
                    else:
                        value = None
                        source = "not set"
                    req_str = "required" if cf.required else "optional"
                    ctx.info(
                        f"  {cf_name} ({cf.type.__name__}, {req_str})"
                        f" = {_format_config_value(value)}"
                        f"  (source: {source})"
                        f"  -- {cf.help}"
                    )
            # Infrastructure section (roots + handshakes + connections)
            if app_ref._infra_root_order or app_ref._handshake_order or app_ref._connection_order:
                ctx.info("")
                ctx.info("Infrastructure:")
                for ev in app_ref._infra_root_order:
                    src = "env-set" if app_ref._infra_root_from_env[ev] else "default"
                    ctx.info(f"  {ev} (root) = {app_ref._infra_roots[ev]}  (source: {src})")
                for ev in app_ref._handshake_order:
                    if ev in os.environ:
                        ctx.info(f"  {ev} (handshake) = {os.environ[ev]}  (set)  -- {app_ref._handshake_envs[ev]}")
                    else:
                        ctx.info(f"  {ev} (handshake) = <unset>  -- {app_ref._handshake_envs[ev]}")
                for ev in app_ref._connection_order:
                    if ev in os.environ:
                        ctx.info(f"  {ev} (connection) = {os.environ[ev]}  (set)  -- {app_ref._connection_envs[ev]}")
                    else:
                        ctx.info(f"  {ev} (connection) = <unset>  -- {app_ref._connection_envs[ev]}")
            return 0

        # --plain is the only local flag left: the machine form moved to the
        # framework-owned --json (contract §19.1), which cannot be declared
        # here, so the two-flag mutex group went with it.
        config_show_flags = [
            Flag(name="plain", type=bool, default=False, help="Display config values in a human-readable table format"),
        ]
        config_grp.commands["show"] = self._build_framework_command(
            "show",
            help="Show every flag and config field with its effective value and where that value came from, resolved through the precedence chain environment variable, then config file, then declared default. Declared infrastructure roots, handshake and connection environment variables are listed too. Choose --plain for an aligned human-readable table; the framework-owned --json yields the same information as a machine-readable object carrying each entry's type, default and help text.",
            effect=EFFECT_READ_ONLY,
            handler=_config_show_handler,
            extra_flags=config_show_flags,
            payload_schema=_CONFIG_SHOW_PAYLOAD_SCHEMA,
        )

        # config set
        def _config_set_handler(
            ctx, key,
            write: "_ConfigSetValue | _ConfigSetClear | _ConfigSetDefault",
            **_kw,
        ) -> int:
            path = _config_path(
                app_ref.name,
                override=app_ref.config_path,
                config_format=app_ref.config_format,
            )
            # Every mutation this handler performs rides ctx.effects: the
            # command is classified `mutating`, so a dry run must RECORD them
            # and change nothing.
            effects = ctx.effects
            _ensure_config_dir(effects, path)
            # Read existing config (use already-loaded data from parse time)
            existing = app_ref._config_data

            # Look up the key against registered flags and config fields
            all_flags = app_ref._collect_all_flags()
            matched_flag = None
            matched_config_field = None
            for f in all_flags:
                if _flag_param_name(f.name) == key:
                    matched_flag = f
                    break
            if matched_flag is None:
                # Check config fields
                if key in app_ref._config_fields:
                    matched_config_field = app_ref._config_fields[key]
            if matched_flag is None and matched_config_field is None:
                print(f"config set: unknown key '{key}'", file=sys.stderr)
                return 1

            # Config field path: simpler handling (no repeatable, no mutex)
            if matched_config_field is not None:
                return _config_set_field(
                    effects, key, write, matched_config_field, existing, path,
                    app_ref.config_format,
                )

            # The elected member says what to write. The three illegal corners
            # the old bools made expressible are unrepresentable now (§27.1).
            # --clear: repeatable/dict flags only
            if isinstance(write, _ConfigSetClear):
                if matched_flag.compound == "dict":
                    cleared: object = {}
                elif matched_flag.repeatable:
                    cleared = []
                else:
                    print("config set: --clear is only for repeatable flags",
                          file=sys.stderr)
                    return 1
                _write_config_set(effects, existing, path, app_ref.config_format, key, cleared)
                return 0

            # --default: remove the key from config
            if isinstance(write, _ConfigSetDefault):
                if not _write_config_unset(effects, existing, path, app_ref.config_format, key):
                    print(f"config set: key '{key}' not in config",
                          file=sys.stderr)
                    return 1
                return 0

            # The value member carries its payload under the reserved field
            # name.
            value = write.value

            # Coerce the string value to the flag's type
            if matched_flag.compound == "dict":
                # Dict flags: parse as JSON
                try:
                    parsed = json.loads(value)
                except json.JSONDecodeError as e:
                    print(f"config set: key '{key}': invalid JSON: {e}",
                          file=sys.stderr)
                    return 1
                if not isinstance(parsed, dict):
                    print(f"config set: key '{key}': expected JSON object",
                          file=sys.stderr)
                    return 1
                typed_value = {}
                for dk, dv in parsed.items():
                    try:
                        typed_value[dk] = _coerce_config_scalar(
                            dv, matched_flag.value_type,
                        )
                    except ValueError as e:
                        print(
                            f"config set: key '{key}': value for '{dk}': {e}",
                            file=sys.stderr,
                        )
                        return 1
            elif matched_flag.repeatable:
                # Split on comma, coerce each element
                parts = _split_escaped(value, ",")
                try:
                    if matched_flag.type == int:
                        typed_value = [_strict_int(p) for p in parts]
                    elif matched_flag.type == float:
                        coerced = []
                        for p in parts:
                            try:
                                coerced.append(_strict_float(p))
                            except ValueError as fe:
                                msg = str(fe)
                                if msg in ("NaN is not allowed",
                                           "Inf is not allowed"):
                                    raise
                                raise ValueError(
                                    f"expected float, got '{p}'"
                                ) from fe
                        typed_value = coerced
                    else:  # str
                        typed_value = parts
                except ValueError as e:
                    print(f"config set: key '{key}': {e}", file=sys.stderr)
                    return 1
                # Unique enforcement
                if matched_flag.unique:
                    dup = _find_duplicate(typed_value)
                    if dup is not None:
                        print(
                            f"config set: key '{key}': duplicate value "
                            f"'{_format_value_for_error(dup)}'",
                            file=sys.stderr,
                        )
                        return 1
            else:
                try:
                    if matched_flag.type == bool:
                        typed_value = _strict_bool(value)
                    elif matched_flag.type == int:
                        typed_value = _strict_int(value)
                    elif matched_flag.type == float:
                        try:
                            typed_value = _strict_float(value)
                        except ValueError as fe:
                            msg = str(fe)
                            if msg in ("NaN is not allowed",
                                       "Inf is not allowed"):
                                raise
                            raise ValueError(
                                f"expected float, got '{value}'"
                            ) from fe
                    else:  # str
                        typed_value = value
                except ValueError as e:
                    print(f"config set: key '{key}': {e}", file=sys.stderr)
                    return 1

            _write_config_set(effects, existing, path, app_ref.config_format, key, typed_value)
            return 0

        # The write selection is attached the way every selector is: to the
        # handler, in declaration order. `_build_framework_command` builds
        # through the one validated registration path, which reads it back off
        # the handler exactly as it does for a consumer command.
        _config_set_handler = choice_flag(
            "write",
            help="What to write at the key: a value, a clear, or a reset to the declared default",
            choices=[_ConfigSetValue, _ConfigSetClear, _ConfigSetDefault],
            elect_by=_ELECT_MEMBER_FLAGS,
            presence="required",
        )(_config_set_handler)
        config_grp.commands["set"] = self._build_framework_command(
            "set",
            help="Write a persistent value into the config file so it overrides a flag's declared default on every later run. The value is coerced to the flag's own type and rejected if it does not fit: repeatable flags take a comma-separated list (backslash-escape a literal comma) and are checked for duplicates, dict flags take a JSON object. Use --default to drop a key back to its default, and --clear to empty a repeatable flag.",
            effect=EFFECT_MUTATING,
            handler=_config_set_handler,
            args=[
                Arg(name="key", help="The config key to set, matching a registered flag name",
                    presence="required"),
            ],
        )

        # config edit
        def _config_edit_handler(ctx, **_kw) -> int:
            path = _config_path(
                app_ref.name,
                override=app_ref.config_path,
                config_format=app_ref.config_format,
            )
            effects = ctx.effects
            _ensure_config_dir(effects, path)
            if not os.path.isfile(path):
                effects.write(path, "" if app_ref.config_format == "toml" else "{}\n")
            editor = os.environ.get("EDITOR", "vi")
            # LAUNCHING AN EDITOR IS A MUTATION. Routed through the handle, a
            # dry run records `run: <editor> <path>` and never opens anything;
            # a bare subprocess.run here would open the user's editor during a
            # run that announced it would change nothing.
            #
            # check=True (the default) is what keeps the preview walking: a
            # failed operation is an error, not a value (§2.5.4), so nothing
            # here ever reads an exit code off a carrier.
            try:
                effects.run([editor, path], stream=True)
            except (OSError, EffectFailed) as e:
                print(f"error: editor failed: {e}", file=sys.stderr)
                return 1
            return 0

        config_grp.commands["edit"] = self._build_framework_command(
            "edit",
            help="Open this application's config file in the editor named by $EDITOR, falling back to vi. The parent directory and an empty config file are created first if they do not exist, so the editor always opens something. Launching the editor counts as a mutation: under --dry-run the command records the editor invocation and opens nothing.",
            effect=EFFECT_MUTATING,
            handler=_config_edit_handler,
            interactive=True,
        )

        # config init
        def _config_init_handler(ctx, **_kw) -> int:
            cfg_path = _config_path(
                app_ref.name,
                override=app_ref.config_path,
                config_format=app_ref.config_format,
            )
            if os.path.isfile(cfg_path):
                print(
                    f"config init: config file already exists: {cfg_path}",
                    file=sys.stderr,
                )
                return 1
            effects = ctx.effects
            _ensure_config_dir(effects, cfg_path)
            if app_ref.config_format == "toml":
                content = _generate_config_template_toml(
                    app_ref._collect_all_flags(),
                    app_ref._config_fields,
                )
            else:
                content = _generate_config_template_json(
                    app_ref._collect_all_flags(),
                    app_ref._config_fields,
                )
            effects.write(cfg_path, content)
            ctx.info(cfg_path)
            return 0

        config_grp.commands["init"] = self._build_framework_command(
            "init",
            help="Create a starter config file listing every flag and config field the application declares, each commented with its help text, type and default value, so the file documents itself. The format follows whichever of TOML or JSON the application was built for. Refuses with an error if a config file already exists rather than overwriting it; the created path is printed on success.",
            effect=EFFECT_MUTATING,
            handler=_config_init_handler,
        )

        self._groups["config"] = config_grp

    def _pre_scan_reserved_flags(self, argv: list[str]) -> dict:
        """Pre-scan for the framework-owned reserved flags.

        Handles --dump-schema, --mcp, --config, --hermetic and the effects-regime
        quartet --dry-run/--approve-consequential/--quiet/--verbose.

        Two regions, two rulesets (contract §7.2, amended):

        - The **pre-command region** (before the first non-flag token, before
          ``--``) recognizes every reserved flag. Known global flags and their
          values are skipped so that a global-flag value matching a command name
          does not terminate the region early.
        - The **command region** recognizes ONLY the quartet, anywhere, exactly
          like --help/-h. --hermetic/--config/--dump-schema/--mcp stay
          pre-command-only. See _scan_command_region_quartet.

        Returns a dict with keys: dump_schema, serve_mcp, hermetic, config_path,
        dry_run, approve_consequential, quiet, verbose, err, cleaned_argv.
        """
        # Build a set of known global flag tokens with value-taking info
        known_flags: dict[str, bool] = {}  # token -> takes_value
        for f in self._global_flags:
            known_flags[f"--{f.name}"] = f.type is not bool
            if f.short:
                known_flags[f"-{f.short}"] = f.type is not bool
            if f.type is bool and f.negatable:
                known_flags[f"--no-{f.name}"] = False

        result: dict = {}
        exclude_indices: set[int] = set()
        # Index where the command region begins; -1 means "never reached one"
        # (a bare -- or an unknown flag-like token ended the scan for good).
        command_region_from = -1
        i = 0
        while i < len(argv):
            tok = argv[i]

            # -- terminates the whole scan: everything after it is data
            if tok == "--":
                break

            # Non-flag token = the command token: the command region starts here
            if not tok.startswith("-") or tok == "-":
                command_region_from = i
                break

            # --dump-schema
            if tok == "--dump-schema":
                result["dump_schema"] = True
                return result

            # --mcp
            if tok == "--mcp":
                result["serve_mcp"] = True
                return result

            # --hermetic (boolean, no value)
            if tok == "--hermetic":
                result["hermetic"] = True
                exclude_indices.add(i)
                i += 1
                continue

            # The reserved quartet plus --json: booleans, no values, stripped
            # from argv and delivered on the Context (never as handler kwargs).
            if tok in _RESERVED_PRESCAN_TOKENS:
                result[_RESERVED_PRESCAN_TOKENS[tok]] = True
                exclude_indices.add(i)
                i += 1
                continue

            # --config=<value>
            if tok.startswith("--config="):
                if not self.config:
                    result["err"] = (
                        "--config is not available: this app does not use config files"
                    )
                    return result
                val = tok[len("--config="):]
                if not val:
                    result["err"] = "flag '--config' requires a value"
                    return result
                result["config_path"] = val
                exclude_indices.add(i)
                i += 1
                continue

            # --config <value>
            if tok == "--config":
                if not self.config:
                    result["err"] = (
                        "--config is not available: this app does not use config files"
                    )
                    return result
                if i + 1 >= len(argv):
                    result["err"] = "flag '--config' requires a value"
                    return result
                result["config_path"] = argv[i + 1]
                exclude_indices.add(i)
                exclude_indices.add(i + 1)
                i += 2
                continue

            # Known global flag with --flag=value form: skip
            if tok.startswith("--") and "=" in tok:
                eq_pos = tok.index("=")
                flag_part = tok[:eq_pos]
                if flag_part in known_flags:
                    i += 1
                    continue
                # Unknown flag-like token: stop
                break

            # Known global flag: skip it (and its value if non-bool)
            if tok in known_flags:
                if known_flags[tok]:
                    i += 2
                else:
                    i += 1
                continue

            # Unknown flag-like token: stop
            break

        if command_region_from >= 0:
            self._scan_command_region_quartet(
                argv, command_region_from, result, exclude_indices,
            )

        if exclude_indices:
            result["cleaned_argv"] = [
                tok for j, tok in enumerate(argv) if j not in exclude_indices
            ]
        else:
            result["cleaned_argv"] = argv

        return result

    def _scan_command_region_quartet(
        self,
        argv: list[str],
        start: int,
        result: dict,
        exclude_indices: set[int],
    ) -> None:
        """Recognize the reserved quartet in the command region of argv.

        Contract §7.2 (amended 2026-08-04): the quartet's four tokens are
        recognized ANYWHERE in argv, exactly like --help/-h, because their
        applicability is per-command -- requiring them before the command name
        was backwards. Only the quartet is recognized here; --hermetic,
        --config, --dump-schema and --mcp remain pre-command-only.

        The scan stops for good at two boundaries:

        - a bare ``--``, after which every token is positional data;
        - a **passthrough** command's name, after which every token belongs to
          the child process and is forwarded byte-for-byte. Eating a child's own
          --verbose would silently change what the child does.

        Routing tokens are walked through the group/command tree so a quartet
        token may sit anywhere among them. Nothing here raises: routing errors
        are the real parse's job.

        Both boundaries are visible in the ``dry_run_supported=False`` refusal,
        which reads the flag this scan resolved. ``app cmd -- --dry-run`` and
        ``app passthrough --dry-run`` are NOT refused, because in neither case
        did the operator ask this app for a dry run: after ``--`` the token is
        the command's own data, and after a passthrough's name it is the child
        process's flag. ``app --dry-run passthrough`` IS refused -- there the
        token is unambiguously addressed to this app.
        """
        groups = self._groups
        commands = self._commands
        routing_done = False
        i = start
        while i < len(argv):
            tok = argv[i]

            if tok == "--":
                return

            if tok.startswith("-") and tok != "-":
                if tok in _RESERVED_PRESCAN_TOKENS:
                    result[_RESERVED_PRESCAN_TOKENS[tok]] = True
                    exclude_indices.add(i)
                i += 1
                continue

            # A non-flag token: a routing token until routing resolves.
            if not routing_done:
                grp = groups.get(tok)
                if grp is not None:
                    groups = grp._groups
                    commands = grp.commands
                    i += 1
                    continue
                cmd = commands.get(tok)
                if cmd is not None and cmd.passthrough is not None:
                    return
                # Resolved a normal command, or hit an unknown/deprecated token
                # the real parse will report: routing is over either way.
                routing_done = True

            i += 1

    def _parse(self, argv: list[str]) -> tuple[Command, dict[str, object] | list[str], dict[str, str]]:
        """Parse argv (without program name) into a resolved Command and kwargs.

        For normal commands, returns (Command, kwargs_dict, sources).
        For passthrough commands, returns (Command, raw_args_list, {}).
        Callers disambiguate by checking cmd.passthrough.

        After parsing, self._last_global_values holds the parsed global flag
        values (used by passthrough command handlers).
        """

        # Step 1: intercept app-level --help/-h, --version/-v
        if not argv or argv == ["--help"] or argv == ["-h"]:
            raise _HelpRequested(target=self)
        if argv == ["--version"] or argv == ["-v"]:
            raise _VersionRequested()

        # Position-aware pre-scan: intercept --dump-schema, --mcp, --config, --hermetic
        # in the pre-command region only (before command name, before --).
        pre_scan = self._pre_scan_reserved_flags(argv)

        # Record the reserved quartet -- and --json beside it -- for the
        # dispatch ctx. This runs BEFORE the pre-scan's own exits so every
        # parse error from here on knows whether the run is in machine mode
        # and can emit the envelope the mode owes it (§19.2).
        self._last_dry_run = bool(pre_scan.get("dry_run"))
        self._last_approve_consequential = bool(
            pre_scan.get("approve_consequential")
        )
        self._last_quiet = bool(pre_scan.get("quiet"))
        self._last_json = bool(pre_scan.get("json"))
        self._last_verbose = bool(pre_scan.get("verbose"))

        if pre_scan.get("dump_schema"):
            raise _DumpSchemaRequested()
        if pre_scan.get("serve_mcp"):
            raise _McpRequested()
        if pre_scan.get("err"):
            raise _ParseError(pre_scan["err"])

        is_hermetic = bool(pre_scan.get("hermetic"))
        # Record for the dispatch ctx: connection env access is suppressed under
        # --hermetic so connection-dependent behavior (incl. checks) skips.
        self._last_hermetic = is_hermetic

        # --hermetic + --config mutual exclusion
        if is_hermetic and pre_scan.get("config_path"):
            raise _ParseError("--hermetic and --config are mutually exclusive")

        # Load config data once at parse time.
        # When hermetic is active, skip config loading entirely (even XDG defaults).
        # Capture any parse error to handle config subcommand exemption later.
        config_load_err: str | None = None
        if self.config and not is_hermetic:
            runtime_override = pre_scan.get("config_path")
            hermetic = self.no_default_config_path and not runtime_override
            is_runtime_flag = bool(runtime_override)
            result = self._resolve_config_data(
                runtime_path_override=runtime_override,
                hermetic=hermetic,
                is_runtime_flag=is_runtime_flag,
            )
            if result.parse_err:
                config_load_err = result.parse_err
                self._config_data = {}
            else:
                self._config_data = result.data
        elif is_hermetic:
            # Hermetic mode: no config data at all
            self._config_data = None

        # Step 1.5: parse global flags before command routing
        # Use cleaned argv (--config/--hermetic stripped) for the rest of the pipeline
        cleaned_argv = pre_scan.get("cleaned_argv", argv)
        self._stdin_consumed_by: str | None = None
        global_values, global_source_map, remaining = self._parse_global_flags(
            cleaned_argv, hermetic=is_hermetic,
        )
        self._last_global_values = global_values

        # Step 2: route to command or group (iterative traversal for arbitrary depth)
        # If global flag parsing stopped at --, strip it before routing
        if remaining and remaining[0] == "--":
            remaining = remaining[1:]

        if not remaining or remaining == ["--help"] or remaining == ["-h"]:
            raise _HelpRequested(target=self)

        cmd, rest, path = self._resolve_command(remaining)
        self._last_resolved_path = path

        # Check for command-level --help/-h anywhere in remaining tokens
        # (but not after "--" separator, which makes everything literal)
        if _tokens_contain_help(rest):
            raise _HelpRequested(target=cmd)

        # A command that declares dry_run_supported=False refuses --dry-run
        # here, on every argv path (run/test/harness) at once, and AFTER the
        # command-help check above so `--help` always beats the refusal: asking
        # what a command does must never be answered with a refusal to preview
        # it. self._last_dry_run was set by the pre-scan, so this covers both
        # `app --dry-run cmd` and `app cmd --dry-run`; see
        # _scan_command_region_quartet for the two boundaries that make a
        # trailing --dry-run invisible here (a bare `--`, and a passthrough
        # command's name).
        if self._last_dry_run and not cmd.dry_run_supported:
            refused_path = ".".join(path + [cmd.name])
            raise _ParseError(
                f"--dry-run is not supported by command '{refused_path}': "
                f"{cmd.dry_run_unsupported_reason}"
            )

        # Config subcommand exemption: config edit, config path, config set
        # are exempt from config load errors (self-lock prevention).
        # config show handles the error specially (shows it as output).
        is_config_subcommand = bool(path) and path[0] == "config"

        # --hermetic + config subcommand = hard error
        if is_hermetic and is_config_subcommand:
            raise _ParseError("--hermetic cannot be used with config commands")

        if config_load_err:
            if not is_config_subcommand:
                raise _ParseError(config_load_err)
            # Store for config show to pick up
            self._config_parse_err = config_load_err

        # Step 2.5: validate config fields (exempt config subcommands)
        if (self.config and self._config_fields
                and not is_config_subcommand):
            err = self._validate_config_fields(cmd, self._config_data)
            if err:
                raise _ParseError(err)

        # Passthrough commands: skip all flag/arg parsing, forward raw args
        if cmd.passthrough is not None:
            return cmd, rest, {}

        # Step 3: parse remaining tokens for the resolved command
        # Pass stdin_consumed_by as a mutable single-element list so
        # _parse_command can update the shared state.
        stdin_state: list[str | None] = [self._stdin_consumed_by]
        self._last_selector_diagnostics = []
        try:
            (
                cmd, kwargs, post_global, sources, writes, unsets,
            ) = _parse_command(
                cmd, rest, self._global_flags, config_data=self._config_data,
                stdin_consumed_by=stdin_state,
                conflict_mode=self.config_conflict_mode,
                hermetic=is_hermetic,
                infra_roots=self._infra_roots,
                out_diagnostics=self._last_selector_diagnostics,
            )
        except _ParseError as e:
            prefix_parts = [self.name] + path + [cmd.name]
            e.command_prefix = " ".join(prefix_parts)
            raise
        # This invocation's write set and the properties it cleared (§27.5,
        # §27.6): the envelope's `writes` member, the would-do log's write-set
        # line and `ctx.unset` all read them off here.
        self._last_writes = writes
        self._last_unsets = unsets

        # Step 4: merge global flag values into kwargs
        # Post-command global flags override pre-command ones
        for gf in self._global_flags:
            if gf.name in post_global:
                global_values[gf.name] = post_global[gf.name]
            kwargs[_flag_param_name(gf.name)] = global_values[gf.name]

        # Merge global sources into command sources. This mirrors the VALUE
        # merge above: for a global set post-command, _parse_command already
        # placed the correct (cli) source into `sources`, so the pre-command
        # source label (typically "default") must NOT overwrite it.
        post_global_params = {_flag_param_name(n) for n in post_global}
        for k, v in global_source_map.items():
            if k in post_global_params:
                continue  # post-command position wins
            sources[k] = v

        return cmd, kwargs, sources

    def _resolve_command(
        self, path_segments: list[str]
    ) -> tuple[Command, list[str], list[str]]:
        """Traverse groups/commands tree to resolve a command from path segments.

        Takes the remaining argv tokens after global flag parsing (group names,
        command name, and command arguments).  Consumes group and command tokens
        from the front, returning the resolved Command, the unconsumed tokens
        (command arguments), and the list of group names traversed.

        Raises _HelpRequested for group-level help and _ParseError for
        deprecated or unknown commands.
        """
        current_groups = self._groups
        current_commands = self._commands
        current_deprecated = self._deprecated
        path: list[str] = []  # tracks group names for error messages and help prefix

        while path_segments:
            token = path_segments[0]

            if token in current_groups:
                group = current_groups[token]
                path.append(token)
                path_segments = path_segments[1:]

                if not path_segments or path_segments[0] in ("--help", "-h"):
                    raise _HelpRequested(target=group)

                # Descend into group
                current_groups = group._groups
                current_commands = group.commands
                current_deprecated = group.deprecated
                continue

            if token in current_commands:
                cmd = current_commands[token]
                rest = path_segments[1:]
                return cmd, rest, path

            if token in current_deprecated:
                dep = current_deprecated[token]
                raise _ParseError(
                    f"command '{token}' is deprecated: {dep.message}"
                )

            # Unknown command -- include path in error message
            if path:
                raise _ParseError(
                    f"unknown command '{token}' in '{' '.join(path)}'",
                    command_prefix=f"{self.name} {' '.join(path)}",
                )
            raise _ParseError(f"unknown command '{token}'")

        # Loop ended without finding a command -- path_segments was exhausted
        # by group traversal. This means the last group had no subcommand.
        # (Already handled by the help check inside the loop, but guard
        # against edge cases.)
        raise _HelpRequested(target=group)  # noqa: F821 -- 'group' always set when loop body ran

    def _parse_global_flags(
        self, argv: list[str], *, hermetic: bool = False,
    ) -> tuple[dict[str, object], dict[str, str], list[str]]:
        """Parse global flags from argv, returning (global_values, global_sources, remaining_tokens).

        Scans tokens from left to right. Global flags are consumed; the first
        non-global-flag token (the command name) and everything after it are
        returned as remaining tokens. A bare ``--`` stops global flag parsing
        and is included in the remaining tokens.

        When hermetic is True, env var and config resolution are skipped entirely.
        """
        if not self._global_flags:
            return {}, {}, argv

        # Build lookup tables
        long_lookup: dict[str, Flag] = {}
        short_lookup: dict[str, Flag] = {}
        negation_lookup: dict[str, Flag] = {}

        for f in self._global_flags:
            long_lookup[f"--{f.name}"] = f
            if f.short:
                short_lookup[f"-{f.short}"] = f
            if f.type is bool and f.negatable:
                negation_lookup[f"--no-{f.name}"] = f

        cli_set: dict[str, object] = {}
        remaining: list[str] = []
        i = 0

        def _store_value(f: Flag, value: object) -> None:
            """Store a parsed value, appending to a list for repeatable flags."""
            if f.compound == "dict":
                if f.name not in cli_set:
                    cli_set[f.name] = {}
                # value is a (key, val) tuple from _parse_dict_value
                k, v = value
                if k in cli_set[f.name]:
                    raise _ParseError(
                        f"--{f.name}: duplicate key '{k}'"
                    )
                cli_set[f.name][k] = v
            elif f.repeatable:
                if f.name not in cli_set:
                    cli_set[f.name] = []
                if f.unique and value in cli_set[f.name]:
                    raise _ParseError(
                        f"--{f.name}: duplicate value "
                        f"'{_format_value_for_error(value)}'"
                    )
                cli_set[f.name].append(value)
            else:
                cli_set[f.name] = value

        while i < len(argv):
            tok = argv[i]

            # -- stops global flag parsing; include it in remaining
            if tok == "--":
                remaining = argv[i:]
                break

            # --flag=value form
            if tok.startswith("--") and "=" in tok:
                eq_pos = tok.index("=")
                flag_part = tok[:eq_pos]
                value_part = tok[eq_pos + 1:]

                if flag_part in long_lookup:
                    f = long_lookup[flag_part]
                    if f.type is bool and f.compound != "dict":
                        raise _ParseError(
                            f"flag '{flag_part}' is a boolean flag and does not take a value"
                        )
                    if f.compound == "dict":
                        _store_dict_flag(f, value_part, cli_set)
                    elif f.type is int:
                        try:
                            _store_value(f, _strict_int(value_part))
                        except ValueError as e:
                            raise _ParseError(f"--{f.name}: {e}")
                    elif f.type is float:
                        try:
                            _store_value(f, _strict_float(value_part))
                        except ValueError as e:
                            raise _float_parse_error(f.name, value_part, e)
                    else:
                        resolved, self._stdin_consumed_by = _resolve_at_prefix(
                            f.name, value_part, self._stdin_consumed_by,
                        )
                        _store_value(f, resolved)
                    i += 1
                    continue
                elif flag_part in negation_lookup:
                    raise _ParseError(
                        f"flag '{flag_part}' is a boolean negation and does not take a value"
                    )
                else:
                    # Not a global flag -- this is the command name region
                    remaining = argv[i:]
                    break

            # --no-flag negation
            if tok in negation_lookup:
                f = negation_lookup[tok]
                cli_set[f.name] = False
                i += 1
                continue

            # --flag (long form)
            if tok.startswith("--") and tok in long_lookup:
                f = long_lookup[tok]
                if f.type is bool and f.compound != "dict":
                    cli_set[f.name] = True
                    i += 1
                else:
                    if i + 1 < len(argv):
                        raw = argv[i + 1]
                        if f.compound == "dict":
                            _store_dict_flag(f, raw, cli_set)
                        elif f.type is int:
                            try:
                                _store_value(f, _strict_int(raw))
                            except ValueError as e:
                                raise _ParseError(f"--{f.name}: {e}")
                        elif f.type is float:
                            try:
                                _store_value(f, _strict_float(raw))
                            except ValueError as e:
                                raise _float_parse_error(f.name, raw, e)
                        else:
                            resolved, self._stdin_consumed_by = _resolve_at_prefix(
                                f.name, raw, self._stdin_consumed_by,
                            )
                            _store_value(f, resolved)
                        i += 2
                    else:
                        raise _ParseError(f"flag '{tok}' requires a value")
                continue

            # -x (short form)
            if tok.startswith("-") and len(tok) == 2 and tok in short_lookup:
                f = short_lookup[tok]
                if f.type is bool and f.compound != "dict":
                    cli_set[f.name] = True
                    i += 1
                else:
                    if i + 1 < len(argv):
                        raw = argv[i + 1]
                        if f.compound == "dict":
                            _store_dict_flag(f, raw, cli_set)
                        elif f.type is int:
                            try:
                                _store_value(f, _strict_int(raw))
                            except ValueError as e:
                                raise _ParseError(f"--{f.name}: {e}")
                        elif f.type is float:
                            try:
                                _store_value(f, _strict_float(raw))
                            except ValueError as e:
                                raise _float_parse_error(f.name, raw, e)
                        else:
                            resolved, self._stdin_consumed_by = _resolve_at_prefix(
                                f.name, raw, self._stdin_consumed_by,
                            )
                            _store_value(f, resolved)
                        i += 2
                    else:
                        raise _ParseError(f"flag '{tok}' requires a value")
                continue

            # Not a global flag -- this is the command name or unknown token
            remaining = argv[i:]
            break
        else:
            # Loop completed without break -- all tokens consumed
            remaining = []

        # Track sources for global flags. Values already in cli_set are CLI.
        global_sources: dict[str, str] = {}
        for k in cli_set:
            global_sources[_flag_param_name(k)] = "cli"
        env_names: set[str] = set()
        config_names: set[str] = set()

        # Resolve env vars for global flags not set by CLI (skipped under --hermetic)
        for f in self._global_flags:
            if hermetic:
                break
            if f.name in cli_set:
                continue
            if f.env is not None:
                env_val = os.environ.get(f.env)
                if env_val is not None:
                    holder = [self._stdin_consumed_by]
                    cli_set[f.name] = _resolve_flag_env_value(f, env_val, holder)
                    self._stdin_consumed_by = holder[0]
                    env_names.add(f.name)

        # Resolve config values for global flags not set by CLI or env.
        # In conflict mode "error", detect config+cli/env overlaps.
        # (Skipped under --hermetic since config is not loaded.)
        if self._config_data and not hermetic:
            for f in self._global_flags:
                param = _flag_param_name(f.name)
                if param not in self._config_data:
                    continue
                # Effective mode: per-flag override if set, else the app default.
                effective_mode = (
                    f.conflict_mode
                    if not isinstance(f.conflict_mode, _MissingSentinel)
                    else self.config_conflict_mode
                )
                if f.name in cli_set:
                    # Conflict ONLY when config diverges from the CLI/env value.
                    if effective_mode == "error":
                        try:
                            coerced = _coerce_config_value(self._config_data[param], f)
                        except ValueError as e:
                            raise _ParseError(
                                f"--{f.name}: config value error: {e}"
                            )
                        if not _values_equal_for_conflict(cli_set[f.name], coerced, f):
                            existing_source = global_sources.get(param, "cli")
                            raise _ParseError(
                                f"flag '{f.name}' set in both "
                                f"{existing_source} and config; remove one"
                            )
                    continue  # cli-wins, or error mode with matching values
                try:
                    coerced = _coerce_config_value(self._config_data[param], f)
                except ValueError as e:
                    raise _ParseError(
                        f"--{f.name}: config value error: {e}"
                    )
                if f.unique and isinstance(coerced, list):
                    dup = _find_duplicate(coerced)
                    if dup is not None:
                        raise _ParseError(
                            f"--{f.name}: config value error: "
                            f"duplicate value "
                            f"'{_format_value_for_error(dup)}'"
                        )
                cli_set[f.name] = coerced
                config_names.add(f.name)

        # Assign sources for env and config values
        for name in env_names:
            global_sources[_flag_param_name(name)] = "env"
        for name in config_names:
            global_sources[_flag_param_name(name)] = "config"

        # Apply defaults for global flags not set by CLI or env
        for f in self._global_flags:
            if f.name in cli_set:
                continue
            src_label = "default"
            if f.presence == _PRESENCE_DEFAULT:
                if isinstance(f.default, RelativeToRoot):
                    cli_set[f.name] = _resolve_infra_root_path(
                        f.default, self._infra_roots,
                    )
                    src_label = "infra"
                elif f.compound == "dict":
                    cli_set[f.name] = dict(f.default)
                elif f.repeatable:
                    cli_set[f.name] = list(f.default)
                else:
                    cli_set[f.name] = f.default
            elif f.presence == _PRESENCE_OPTIONAL:
                cli_set[f.name] = None
            else:
                if f.type is bool and f.negatable:
                    raise _ParseError(
                        f"global flag '--{f.name}' must be passed as "
                        f"--{f.name} or --no-{f.name}"
                    )
                if f.type is bool and not f.negatable:
                    raise _ParseError(
                        f"global flag '--{f.name}' must be passed as "
                        f"--{f.name}"
                    )
                raise _ParseError(f"global flag '--{f.name}' is required")
            global_sources[_flag_param_name(f.name)] = src_label

        # Validate choices for global flags
        for f in self._global_flags:
            if f.name in cli_set:
                _validate_choices(
                    f.name, cli_set[f.name], f.repeatable, f.choices,
                    f.retired_choices,
                )

        return cli_set, global_sources, remaining

    def _find_command_prefix(self, cmd: Command) -> str:
        """Find the group prefix for a command (for help formatting).

        Traverses the group tree recursively to find the full path.
        """
        def _search_groups(groups: dict[str, Group], path: list[str]) -> str | None:
            for group in groups.values():
                if cmd in group.commands.values():
                    return " ".join(path + [group.name]) + " "
                result = _search_groups(group._groups, path + [group.name])
                if result is not None:
                    return result
            return None

        return _search_groups(self._groups, []) or ""

    def run(self) -> None:
        """Run the CLI application, reading from sys.argv."""
        result = self._dispatch(sys.argv[1:], sys.stdout, sys.stderr, "run")
        sys.exit(result.exit_code)

    def test(self, argv: list[str]) -> Result:
        """Run the CLI with given argv, capturing output and exit code."""
        stdout_buf = io.StringIO()
        stderr_buf = io.StringIO()
        # The redirect wraps the whole dispatch so a handler that bypasses the
        # context writers and calls print() is captured too.
        with contextlib.redirect_stdout(stdout_buf), contextlib.redirect_stderr(stderr_buf):
            result = self._dispatch(argv, stdout_buf, stderr_buf, "test")
        return Result(
            stdout=stdout_buf.getvalue(),
            stderr=stderr_buf.getvalue(),
            exit_code=result.exit_code,
            data=(None if result.payload is _MISSING else result.payload),
        )

    def _dispatch(self, argv: list[str], out, err, mode: str) -> "_DispatchResult":
        """The single dispatch seam shared by ``run()`` and ``test()``.

        Parses, renders the pre-dispatch outcomes (help, version, schema dump,
        MCP, parse errors), executes the handler and finishes through the ONE
        ordered exit step (:meth:`_finish_dispatch`), which owns the payload,
        the would-do log and the exit code on every path out of the handler.
        """
        # Machine mode is not known until the pre-scan runs inside _parse, so
        # the flag starts false on every dispatch: a stale value from an
        # earlier run must never decide what this one emits. The registration
        # validations below run BEFORE the pre-scan and therefore cannot reach
        # machine mode at all -- they are app-definition errors, not run
        # results, and they emit no envelope.
        self._last_json = False
        # A previous dispatch's write set must never decide what this one
        # reports, on any exit path.
        self._last_writes = None
        self._last_unsets = set()

        check_err = self._validate_check_registrations()
        if check_err:
            print(f"error: {check_err}", file=err)
            return _DispatchResult(1)
        tag_err = self._validate_tag_contracts()
        if tag_err:
            print(f"error: {tag_err}", file=err)
            return _DispatchResult(1)

        try:
            cmd, data, sources = self._parse(argv)
        except _HelpRequested as e:
            if isinstance(e.target, App):
                print(_format_app_help(self), file=out)
            elif isinstance(e.target, Group):
                print(_format_group_help(self, e.target), file=out)
            elif isinstance(e.target, Command):
                prefix = self._find_command_prefix(e.target)
                print(_format_command_help(self, e.target, prefix), file=out)
            return _DispatchResult(0)
        except _VersionRequested:
            print(_format_version(self), file=out)
            return _DispatchResult(0)
        except _DumpSchemaRequested:
            try:
                path = _write_schema(self)
            except RuntimeError as e:
                print(f"error: {e}", file=err)
                return _DispatchResult(1)
            print(path, file=out)
            return _DispatchResult(0)
        except _McpRequested:
            if mode == "test":
                # In test mode, MCP requires real stdin/stdout; just acknowledge
                print("error: --mcp requires interactive stdin/stdout", file=err)
                return _DispatchResult(1)
            self.serve_mcp()
            return _DispatchResult(0)
        except _ParseError as e:
            print(f"error: {e}", file=err)
            prefix = e.command_prefix or self.name
            print(f"try '{prefix} --help'", file=err)
            # A run that ended before a command resolved still owes machine
            # mode its one document, with a null command (§19.2). The parse
            # error's own text stays on stderr: it does not go through the
            # context writers, so it is not one of the diagnostics the
            # envelope carries.
            if self._last_json:
                self._emit_envelope(
                    out, command=None, exit_code=1,
                    dry_run=self._last_dry_run, payload=_MISSING,
                    preview=[], preview_error=None, diagnostics=[],
                )
            return _DispatchResult(1)

        self._begin_dispatch()
        cmd_path = ".".join(self._last_resolved_path + [cmd.name])
        # Record test-coverage hit (command-level only, test mode only).
        if mode == "test" and self._coverage_shard_path is not None:
            self._record_coverage(cmd_path)
        # Store sources for function handlers that need provenance info
        self._last_sources = sources
        ctx = Context(
            stdout=out, stderr=err, sources=sources,
            infra=self._infra_access(self._last_hermetic),
            dry_run=self._last_dry_run,
            approve_consequential=self._last_approve_consequential,
            quiet=self._last_quiet, verbose=self._last_verbose,
            json=self._last_json,
            effects=self._arm_effects(
                cmd, cmd_path, dry_run=self._last_dry_run, out=out,
            ),
            command_name=cmd.name,
            payload_schema=cmd.payload_schema,
            unsets=self._last_unsets,
        )
        # The would-do log's unnumbered write-set line (contract §27.5, §3.2).
        # It renders in DRY MODE ONLY, immediately after the header and before
        # line `1.`: a live run's write set rides the envelope instead.
        if self._last_writes is not None and self._last_dry_run:
            self._effect_log.write_set_line = self._last_writes.log_line()
        # Every ambient binding a non-elected scope skipped is NAMED, one line
        # per binding, in declaration order, at debug level -- hidden by
        # default, shown by --verbose, and carried in machine mode's
        # diagnostics whatever the human stream did (§24.6).
        for line in getattr(self, "_last_selector_diagnostics", ()) or ():
            ctx.debug(line)
        if mode == "run":
            # The confirm protocol fires only on the real CLI path.
            self._confirm_consequential(cmd, cmd_path)
        # The ordered exit step runs on EVERY exit path out of the dispatch,
        # not just the normal return: the operator asked for a preview and the
        # effects were recorded, so a handler that unwinds through sys.exit or
        # an exception still owes them the list. The clause set below is
        # exhaustive by construction -- BaseException is the root of the
        # hierarchy, so no unwind can slip past it.
        try:
            if cmd.passthrough is not None:
                handler_return = cmd.passthrough.handler(
                    ctx, cmd.name, data, self._last_global_values,
                )
            else:
                handler_return = cmd.handler(ctx, **data)
            exit_code = _interpret_handler_return(handler_return)
        except _DryRunTruncated as trunc:
            return self._finish_dispatch(
                ctx, cmd_path, 1, out, err,
                truncated=trunc, aborted=False,
                owns_stdout=cmd.owns_stdout,
            )
        except SystemExit as e:
            code = e.code if isinstance(e.code, int) else (1 if e.code else 0)
            self._finish_dispatch(
                ctx, cmd_path, code, out, err, aborted=False,
                owns_stdout=cmd.owns_stdout,
            )
            if mode == "run":
                # The real CLI path lets the exit propagate untouched, so a
                # non-integer sys.exit() argument keeps printing itself.
                raise
            return _DispatchResult(code, ctx._payload_value)
        except BaseException:
            self._finish_dispatch(
                ctx, cmd_path, 1, out, err, aborted=True,
                owns_stdout=cmd.owns_stdout,
            )
            raise
        return self._finish_dispatch(
            ctx, cmd_path, exit_code, out, err,
            owns_stdout=cmd.owns_stdout,
        )

    def _finish_dispatch(
        self, ctx: "Context", cmd_path: str, exit_code: int, out, err,
        *, truncated: "_DryRunTruncated | None" = None,
        aborted: bool = False, owns_stdout: bool = False,
    ) -> "_DispatchResult":
        """The ONE ordered exit step: payload, preview log, exit code.

        Reachable from all four ways out of a dispatch (normal return, an
        explicit ``sys.exit``, a truncated preview and an unwinding abort), so
        there is exactly one place that decides what the framework emits at the
        end of a run and in what order.

        In machine mode this step emits the envelope INSTEAD of the human
        stream's would-do log, truncation error and abort marker: those texts
        become the envelope's ``preview`` and ``preview_error`` members
        (§19.1, §19.3), and stdout carries exactly one document.
        """
        if ctx._json:
            # The emission seam owns instance validation (§19.4, §19.5): the
            # value is checked here, where the envelope is about to carry it,
            # and nowhere else. A human-mode run never reaches this line, so a
            # payload the envelope could not represent costs it nothing.
            self._validate_emitted_payload(ctx)
            # A command that declared stdout ownership keeps stdout for its own
            # document, and the envelope moves to stderr with the diagnostics
            # it carries (contract §19.6). Leaving it on stdout would re-create
            # the two-documents-on-one-stream collision §19.1 exists to remove.
            self._emit_envelope(
                err if owns_stdout else out,
                command=cmd_path,
                exit_code=exit_code,
                dry_run=ctx._dry_run,
                payload=ctx._payload_value,
                preview=self._effect_log.to_list(),
                preview_error=self._preview_error(
                    cmd_path, ctx._dry_run, truncated, aborted,
                ),
                diagnostics=ctx._diagnostics,
                writes=(
                    self._last_writes.envelope_member()
                    if self._last_writes is not None else None
                ),
            )
            return _DispatchResult(exit_code, ctx._payload_value)
        if truncated is not None:
            # The truncation path ends the preview for its own pinned reason:
            # it renders the log it already has and its own error, and never
            # goes through the generic would-do rendering.
            if not truncated.log.seam_suppressed():
                print(truncated.log.render(), file=out)
            print(truncated.message, file=err)
        else:
            self._render_dry_log(cmd_path, out, err, aborted=aborted)
        return _DispatchResult(exit_code, ctx._payload_value)

    def _validate_emitted_payload(self, ctx: "Context") -> None:
        """Validate the payload the envelope is about to carry (§19.5).

        The schema check, JSON representability and the 2^53 magnitude guard
        all live here, at the one seam where the value becomes a document. A
        deviation fails the run rather than shipping a wrong shape.
        """
        if ctx._payload_value is _MISSING:
            return
        found = _validate_payload_value(ctx._payload_value, ctx._payload_schema)
        if found is not None:
            path, detail = found
            raise RuntimeError(
                _msg_payload_invalid(ctx._command_name, path, detail)
            )

    def _preview_error(
        self, cmd_path: str, dry_run: bool,
        truncated: "_DryRunTruncated | None", aborted: bool,
    ) -> dict | None:
        """Build the envelope's ``preview_error`` member (§19.3).

        The two terminal conditions are mutually exclusive by §3.5's table.
        Each carries the §12.5 / §12.11 text byte-identically rather than
        restating it, so there is one text per condition.

        The abort branch is dry-mode-only, exactly as the human stream's
        marker is: the message says "dry-run preview ends at step N", which is
        not a true sentence about a live run.
        """
        if truncated is not None:
            return {
                "kind": "truncated",
                "step": truncated.step,
                "command": truncated.cmd_path,
                "brand": truncated.brand,
                "message": truncated.message,
            }
        if aborted and dry_run:
            step = self._effect_log.next_seq()
            return {
                "kind": "aborted",
                "step": step,
                "command": cmd_path,
                "brand": None,
                "message": _msg_dry_run_aborted(step, cmd_path),
            }
        return None

    def _emit_envelope(
        self, out, *, command: str | None, exit_code: int, dry_run: bool,
        payload: object, preview: list[dict], preview_error: dict | None,
        diagnostics: list[dict], writes: dict | None = None,
    ) -> None:
        """Write the envelope, machine mode's sole stdout document (§19.2).

        Field order follows §19.2's table: optional and for readability only,
        since conformance compares parsed structures. Record keys are sorted so
        the three implementations' serializers agree byte-for-byte.

        The serialization follows §19.5's escaping regime -- plain UTF-8,
        escaping only what JSON mandates -- and it is NOT written through the
        quiet-suppressible writers, so ``--quiet`` has no mechanism by which to
        reach it.
        """
        envelope = {
            "interface_version": _INTERFACE_VERSION,
            "app": self.name,
            "app_version": self.version,
            "command": command,
            "exit_code": exit_code,
            "payload": None if payload is _MISSING else payload,
            "dry_run": dry_run,
            # The write set of a command declaring `update_of` (§19.2's
            # amendment, §27.5). Null on every command that declares none,
            # NEVER absent, and populated in BOTH modes: it is a function of
            # the declaration and the invocation, not of the mode.
            "writes": writes,
            "preview": [
                {key: rec[key] for key in sorted(rec)} for rec in preview
            ],
            "preview_error": preview_error,
            "diagnostics": diagnostics,
        }
        # No ``default=`` fallback: §19.5's emission-time validation already
        # refused every value the encoder could not represent, so a coercion
        # here could only invent a shape no declared schema describes. The two
        # siblings have no such fallback either.
        print(
            json.dumps(
                envelope, separators=(",", ":"), ensure_ascii=False,
            ),
            file=out,
        )

    def _invoke(
        self, command_path: str, kwargs: dict[str, object],
        *, approve_consequential: bool = False, flat: bool = False,
    ) -> object:
        """Invoke a command programmatically with pre-typed kwargs.

        This is the internal pipeline for programmatic invocation. It bypasses
        CLI parsing, env var resolution, config file loading, and stdin
        handling. The caller provides fully-typed values directly.

        Args:
            command_path: dot-separated path to the command
                (e.g. "deploy" or "config.set").
            kwargs: handler keyword arguments. Flag names use underscores
                (e.g. dry_run). Positional args use their declared name.
                For passthrough commands, pass a single key "_args" with
                a list of raw string arguments.
            approve_consequential: the caller's explicit consent. A command
                that declares itself consequential is refused without it.

        Returns:
            The handler's return value (structured data, int, or None).

        Raises:
            _ParseError: if validation fails (missing required flags,
                mutex violations, dependency errors, etc.), or if the command
                is consequential and no consent was supplied.
            _HelpRequested: if the command path resolves to a group
                with no subcommand.
        """
        path_segments = command_path.split(".")
        cmd, _rest, _path = self._resolve_command(path_segments)

        # The consent check (contract §8.5). There is no terminal here, so the
        # confirm protocol's prompt cannot fire -- the caller must have said so
        # in the call. Checked before anything is dispatched or recorded.
        if cmd.consequential and not approve_consequential:
            raise _ParseError(
                _msg_call_consequential_unconsented(command_path)
            )

        self._begin_dispatch()
        # Record test-coverage hit (command-level only).
        if self._coverage_shard_path is not None:
            self._record_coverage(command_path)

        # Passthrough commands: forward raw args to the passthrough handler
        if cmd.passthrough is not None:
            raw_args = kwargs.get("_args", [])

            # Build set of known global flag param names
            global_param_names: set[str] = set()
            for gf in self._global_flags:
                global_param_names.add(_flag_param_name(gf.name))

            # Validate that all kwargs keys are either "_args" or known global flags
            for key in kwargs:
                if key == "_args":
                    continue
                if key not in global_param_names:
                    raise _ParseError(
                        f"unknown parameter '{key}' for passthrough command '{cmd.name}'"
                    )

            # Build global values from kwargs, applying defaults for missing flags
            global_values: dict[str, object] = {}
            for gf in self._global_flags:
                param_name = _flag_param_name(gf.name)
                if param_name in kwargs:
                    global_values[param_name] = kwargs[param_name]
                elif gf.presence == _PRESENCE_DEFAULT:
                    if isinstance(gf.default, RelativeToRoot):
                        global_values[param_name] = _resolve_infra_root_path(
                            gf.default, self._infra_roots,
                        )
                    else:
                        global_values[param_name] = gf.default
                elif gf.presence == _PRESENCE_OPTIONAL:
                    global_values[param_name] = None
                else:
                    raise _ParseError(
                        f"global flag '--{gf.name}' is required"
                    )

            # Programmatic dispatch: --dry-run is not reachable (argv parsing
            # is bypassed entirely) and the confirm protocol's PROMPT never
            # fires -- there is no terminal. The requirement itself is honoured
            # above, and the caller's consent is delivered to the handler here.
            ctx = Context(
                stdout=sys.stdout, stderr=sys.stderr, sources={},
                infra=self._infra_access(),
                approve_consequential=approve_consequential,
                effects=self._arm_effects(cmd, command_path, dry_run=False),
                command_name=cmd.name,
                payload_schema=cmd.payload_schema,
            )
            result = cmd.passthrough.handler(
                ctx, cmd.name, raw_args, global_values,
            )
            _interpret_handler_return(result)  # validate return type
            # The programmatic surface keeps its capture: it returns the
            # payload the handler supplied (contract §19.4).
            if ctx._payload_value is not _MISSING:
                return ctx._payload_value
            if isinstance(result, Outcome):
                return None
            return result

        # Build reverse mapping: param_name (underscore) -> the declared Flag
        param_to_flag: dict[str, Flag] = {}
        for f in cmd.flags:
            param_to_flag[_flag_param_name(f.name)] = f

        # Also map global flags
        global_flag_names: set[str] = set()
        for gf in self._global_flags:
            param_to_flag[_flag_param_name(gf.name)] = gf
            global_flag_names.add(gf.name)

        # Collect arg names for this command
        arg_names: set[str] = {a.name for a in cmd.args}

        # A key naming nothing this command declares is a fact about the
        # object's SHAPE, and shape is decided before anything else -- exactly
        # as an unknown flag outranks every election, scope, value and presence
        # problem on the command line, wherever it sits in argv (§24.3, and
        # item 224's reason: the phase order is a property of the parser, not
        # of the input). The selector's own key and every scoped name at every
        # depth are properties of the flat schema, so supplying one is a scope
        # question and never a shape one.
        declared_params = set(param_to_flag) | arg_names | (
            _flat_selector_params(cmd) if flat
            else {_flag_param_name(s.name) for s in cmd.selectors}
        )
        for key in kwargs:
            if key not in declared_params:
                raise _ParseError(
                    f"unknown parameter '{key}' for command '{cmd.name}'"
                )

        # A selector's value is the same record a handler receives: a choice
        # instance, pre-typed (§24.11). The flat machine form is converted into
        # one at the protocol boundary instead, through the SAME election,
        # scope and presence machinery the argv path uses. Either door is run
        # in STAGES rather than end to end, because the value stage is shared
        # with the command's own flags (below).
        selector_result: _SelectorResult | None = None
        door: _FlatSelectorDoor | _RecordSelectorDoor | None = None
        if cmd.selectors:
            door = (
                _FlatSelectorDoor(cmd, kwargs, infra_roots=self._infra_roots)
                if flat
                else _RecordSelectorDoor(
                    cmd, kwargs, infra_roots=self._infra_roots,
                )
            )
            kwargs = {
                k: v for k, v in kwargs.items() if k not in door.consumed
            }

        # The VALUE stage: ONE sweep in the COMMAND's own declaration order
        # (§24.3, §24.11, §18.25 item 249). Each declaration in the order it
        # was written, a selector taking its POSITION in that walk and the
        # values of its elected record read there, at every depth -- root flags
        # and selectors interleaved, never partitioned into scoped-first and
        # root-second. An object has no order of its own, so the order the
        # caller happened to write its keys in decides nothing (§21.4's
        # reason). Supplied values are marked _Source.CLI; absent flags get
        # _Source.DEFAULT when _validate_and_build_kwargs applies defaults.
        store = _SourcedStore()
        # The properties this call CLEARED, spelled `null` on the property's
        # own key at this door (§27.6).
        unsets: set[str] = set()
        for m in cmd.members:
            if isinstance(m, _Selector):
                if door is not None:
                    door.read(m)
                continue
            key = _flag_param_name(m.name)
            if key in kwargs:
                # The one stated carve-out from "null is legal for nothing"
                # (§24.11's amendment, §27.6): a NULLABLE PROPERTY is the one
                # declaration for which null is a VALUE -- it means clear this
                # property on the resource, which is a write. It delivers
                # absence, reports `provided()` true, and is what `ctx.unset`
                # answers true for. The carve-out is a property of the
                # DECLARATION and never of the door: a null anywhere else is
                # refused by the rule below, unchanged.
                if kwargs[key] is None and m.nullable:
                    unsets.add(m.name)
                    store.set(m.name, None, _Source.CLI)
                    continue
                store.set(
                    m.name,
                    _check_pre_typed_value(m, kwargs[key], machine_boundary=flat),
                    _Source.CLI,
                )
        # An app-level global is a declaration like any other, and it follows
        # the command's own: a command declares nothing after them.
        for gf in self._global_flags:
            key = _flag_param_name(gf.name)
            if key in kwargs:
                store.set(
                    gf.name,
                    _check_pre_typed_value(gf, kwargs[key], machine_boundary=flat),
                    _Source.CLI,
                )

        # The PRESENCE stage for the selectors, which runs once every value in
        # the call has been read: a missing required flag inside a scope, and a
        # selector nobody elected, are both reported behind every value.
        if door is not None:
            selector_result = door.presence()

        # A positional's value is pre-typed exactly as a flag's is, so it is
        # handed on AS SUPPLIED -- never stringified into a token the caller
        # did not write. The declaration decides what it may be, in the
        # positional phase where the argv path already decides it (§24.11's
        # rule read onto §23.3's declaration).
        supplied_args = {a.name: kwargs[a.name] for a in cmd.args if a.name in kwargs}

        # Validate and build final kwargs via the shared validation pipeline
        (
            _cmd, final_kwargs, _global_cli_set, invoke_sources, writes,
        ) = _validate_and_build_kwargs(
            cmd, store, [], global_flag_names, self._infra_roots,
            selector_result=selector_result,
            pre_typed_args=supplied_args,
            machine_boundary=flat,
            unsets=unsets,
        )
        self._last_writes = writes
        self._last_unsets = unsets

        # Merge global flag values into final kwargs
        for gf in self._global_flags:
            if gf.name in _global_cli_set:
                final_kwargs[_flag_param_name(gf.name)] = _global_cli_set[gf.name]
            elif _flag_param_name(gf.name) not in final_kwargs:
                # Global flag not provided -- apply its declared presence
                if gf.presence == _PRESENCE_DEFAULT:
                    if isinstance(gf.default, RelativeToRoot):
                        final_kwargs[_flag_param_name(gf.name)] = _resolve_infra_root_path(gf.default, self._infra_roots)
                        invoke_sources[_flag_param_name(gf.name)] = "infra"
                    else:
                        final_kwargs[_flag_param_name(gf.name)] = gf.default
                elif gf.presence == _PRESENCE_OPTIONAL:
                    final_kwargs[_flag_param_name(gf.name)] = None

        # Store sources for function handlers that need provenance info
        self._last_sources = invoke_sources

        ctx = Context(
            stdout=sys.stdout, stderr=sys.stderr, sources=invoke_sources,
            infra=self._infra_access(),
            approve_consequential=approve_consequential,
            effects=self._arm_effects(cmd, command_path, dry_run=False),
            command_name=cmd.name,
            payload_schema=cmd.payload_schema,
            unsets=unsets,
        )
        result = cmd.handler(ctx, **final_kwargs)
        _interpret_handler_return(result)  # validate return type
        # The programmatic surface keeps its capture: it returns the payload
        # the handler supplied (contract §19.4).
        if ctx._payload_value is not _MISSING:
            return ctx._payload_value
        if isinstance(result, Outcome):
            return None
        return result

    def call(
        self, command_path: str, *, approve_consequential: bool = False,
        **kwargs: object,
    ) -> object:
        """Invoke a command programmatically and return its result.

        Unlike _invoke(), this is the public API. It converts internal
        _ParseError exceptions to InvokeError so callers don't need to
        depend on private types.

        Args:
            command_path: dot-separated path to the command
                (e.g. "deploy" or "config.set").
            approve_consequential: the caller's explicit consent, the
                programmatic counterpart of ``--approve-consequential``.
                Keyword-only, and never a handler kwarg: the name is
                framework-reserved, so no command can declare a parameter
                that collides with it. A command that declares itself
                consequential is refused without it. Read-only and plain
                mutating commands ignore it.
            **kwargs: handler keyword arguments. Flag names use underscores
                (e.g. dry_run). Positional args use their declared name.
                For passthrough commands, pass _args=[...] for raw arguments.

        Returns:
            The handler's return value (structured data, int, or None).

        Raises:
            InvokeError: if validation fails (unknown command, missing
                required flags, mutex violations, dependency errors, etc.),
                or if the command is consequential and no consent was given.
        """
        return self._call_with_kwargs(
            command_path, kwargs,
            approve_consequential=approve_consequential,
        )

    def _call_with_kwargs(
        self, command_path: str, kwargs: dict[str, object],
        *, approve_consequential: bool, flat: bool = False,
    ) -> object:
        """call() with the handler kwargs as a dict instead of a splat.

        The MCP server routes through here rather than ``call(**arguments)``:
        an ``approve_consequential`` key inside a tools/call ``arguments``
        object is a parameter of the command's own namespace -- no command can
        declare that reserved name, so it must surface as the usual
        unknown-parameter error, exactly as it does in the siblings whose
        kwargs are a map. Splatting it would silently promote it to consent.
        """
        try:
            return self._invoke(
                command_path, kwargs,
                approve_consequential=approve_consequential, flat=flat,
            )
        except _ParseError as e:
            raise InvokeError(str(e)) from e
        except _HelpRequested:
            raise InvokeError(
                f"'{command_path}' is a group, not a command"
            )

    async def acall(
        self, command_path: str, *, approve_consequential: bool = False,
        **kwargs: object,
    ) -> object:
        """Async version of call(). Runs the handler in a thread.

        Args:
            command_path: dot-separated path to the command.
            approve_consequential: the caller's explicit consent (same as
                call()).
            **kwargs: handler keyword arguments (same as call()).

        Returns:
            The handler's return value (structured data, int, or None).

        Raises:
            InvokeError: if validation fails, or if the command is
                consequential and no consent was given.
        """
        import asyncio
        # to_thread takes func positional-only, so a handler kwarg can never
        # shadow it.
        return await asyncio.to_thread(
            self.call, command_path,
            approve_consequential=approve_consequential, **kwargs,
        )

    def json_schema(self, command_path: str) -> dict:
        """Produce a JSON Schema parameters object for a command's flags and args.

        Args:
            command_path: dot-separated path to the command (e.g. "deploy"
                or "config.show").

        Returns:
            A JSON Schema object with "type": "object", "properties",
            "required", and "additionalProperties": false.

        Raises:
            InvokeError: if the command path is invalid or resolves to a group.
        """
        path_segments = command_path.split(".")
        try:
            cmd, _rest, _path = self._resolve_command(path_segments)
        except _ParseError as e:
            raise InvokeError(str(e)) from e
        except _HelpRequested:
            raise InvokeError(
                f"'{command_path}' is a group, not a command"
            )
        return _build_json_schema(cmd)

    def as_tools(self) -> list[Tool]:
        """Export non-hidden, non-interactive leaf commands as Tool descriptors.

        Returns a list of Tool objects, one per eligible command plus a
        router tool. Each tool's execute function wraps acall().
        """
        tools: list[Tool] = []
        command_paths: list[str] = []

        # Collect leaf commands from top-level
        for name, cmd in self._commands.items():
            if cmd.hidden or cmd.interactive:
                continue
            path = name
            tools.append(self._make_tool(path, cmd))
            command_paths.append(path)

        # Collect leaf commands from groups (recursive)
        for group_name, group in self._groups.items():
            self._collect_tools_from_group(
                group, [group_name], tools, command_paths,
            )

        # Build the router tool
        tools.append(self._make_router_tool(command_paths))

        return tools

    def _collect_tools_from_group(
        self,
        group: Group,
        path: list[str],
        tools: list[Tool],
        command_paths: list[str],
    ) -> None:
        """Recursively collect non-hidden, non-interactive commands from a group."""
        if group.hidden:
            return
        for cmd_name, cmd in group.commands.items():
            if cmd.hidden or cmd.interactive:
                continue
            dotted = ".".join(path + [cmd_name])
            tools.append(self._make_tool(dotted, cmd))
            command_paths.append(dotted)
        for sub_name, sub_group in group._groups.items():
            self._collect_tools_from_group(
                sub_group, path + [sub_name], tools, command_paths,
            )

    def _make_tool(self, command_path: str, cmd: Command) -> Tool:
        """Build a Tool for a single command."""
        app_ref = self

        async def execute(
            *, approve_consequential: bool = False, **kwargs: object,
        ) -> object:
            # The tool descriptor publishes the FLAT projection (§24.11), so
            # the values that come back are flat too and are converted into
            # elected records at this boundary.
            import asyncio
            return await asyncio.to_thread(
                app_ref._call_with_kwargs, command_path, dict(kwargs),
                approve_consequential=approve_consequential, flat=True,
            )

        return Tool(
            name=command_path,
            description=_tool_description(cmd, cmd.help),
            parameters=_build_json_schema(cmd),
            effect=cmd.effect,
            consequential=cmd.consequential,
            execute=execute,
        )

    def _make_router_tool(self, command_paths: list[str]) -> Tool:
        """Build the router tool that dispatches to per-command tools."""
        app_ref = self

        async def execute(
            command: str | None = None, *,
            approve_consequential: bool = False, **kwargs: object,
        ) -> object:
            if command is None:
                return command_paths[:]
            return await app_ref.acall(
                command,
                approve_consequential=approve_consequential,
                **kwargs,
            )

        parameters: dict = {
            "type": "object",
            "properties": {
                "command": {
                    "type": "string",
                    "description": (
                        "Command to execute (dot-separated path)"
                    ),
                    "enum": command_paths[:],
                },
            },
            "required": ["command"],
            "additionalProperties": False,
        }
        # The router can reach a mutating command, so it classifies as
        # mutating. It is NOT itself consequential: the routed command's own
        # requirement is checked when the call reaches it, and the router
        # forwards the caller's consent unchanged. Marking the router
        # consequential would demand consent for routing to a read_only
        # command, which confirms nothing.
        return Tool(
            name=self.name,
            description=f"Route to {self.name} commands",
            parameters=parameters,
            effect=EFFECT_MUTATING,
            consequential=False,
            execute=execute,
        )

    def serve_mcp(
        self,
        *,
        input: io.TextIOBase | None = None,
        output: io.TextIOBase | None = None,
    ) -> None:
        """Run a JSON-RPC 2.0 MCP server on stdin/stdout.

        Reads one JSON object per line from input (default: sys.stdin),
        writes one JSON object per line to output (default: sys.stdout).
        Handles server/discover, tools/list and tools/call under protocol
        2026-07-28, the retained initialize handshake of the era before it,
        and notifications.

        The server runs until input is exhausted (EOF).
        """
        _run_mcp_server(self, input=input, output=output)


# JSON Schema type mapping for tool export
_JSON_SCHEMA_TYPES = {
    str: "string",
    int: "integer",
    float: "number",
    bool: "boolean",
}


def _scalar_fragment(t: type, choices: list | None) -> dict:
    """One scalar row of §25.2's fragment table, with its `enum` if any."""
    frag: dict = {"type": _JSON_SCHEMA_TYPES[t]}
    if choices is not None:
        frag["enum"] = list(choices)
    return frag


def _value_schema_fragment(decl: "Flag | Arg") -> dict:
    """The JSON Schema fragment describing the value a declaration delivers.

    A real fragment from the closed four-keyword subset (`type`, `items`,
    `additionalProperties`, `enum`), with JSON Schema's own type names, emitted
    in that key order (contract §25.2).

    ARITY IS VALUE SHAPE (§25.3): a repeatable scalar flag and a `list[T]` flag
    publish the identical array fragment, and a variadic arg does the same in
    either spelling -- which is what makes the deleted `repeatable` key a
    second spelling of a fact the shape already carries.

    An optional declaration emits the plain type: there is no `null` in any
    fragment and no type list, because presence is the sole authority on
    absence and a nullable fragment would be a second statement about it.

    The same function feeds the MCP projection, so an arity and an `enum`
    placement cannot disagree between the two doors (§25.13's amendment). The
    one fact the doors state differently is NULLABILITY: the projection folds
    `"null"` into a nullable property's type list, which this subset cannot
    express, and the dump carries it as the sibling `nullable` key instead
    (§27.9, §27.10).
    """
    compound = getattr(decl, "compound", "scalar")
    if compound == "dict":
        return {
            "type": "object",
            "additionalProperties": {
                "type": _JSON_SCHEMA_TYPES[decl.value_type],
            },
        }
    array = (
        compound == "list"
        or bool(getattr(decl, "repeatable", False))
        or bool(getattr(decl, "variadic", False))
    )
    if array:
        return {"type": "array", "items": _scalar_fragment(decl.type, decl.choices)}
    return _scalar_fragment(decl.type, decl.choices)


def _build_json_schema(cmd: Command) -> dict:
    """Build a JSON Schema parameters object for a command's flags and args."""
    properties: dict = {}
    required: list[str] = []

    for f in cmd.flags:
        param_name = _flag_param_name(f.name)
        prop: dict = _value_schema_fragment(f)
        prop["description"] = f.help
        # A NULLABLE property publishes a type list including "null" (contract
        # §27.10). This projection is not bound by §25.2's four-keyword subset
        # -- it already emits anyOf and dependentRequired -- and here the null
        # is a VALUE the declaration names rather than a spelling of absence,
        # so publishing it in the type is publishing the declaration. A caller
        # that cannot see it cannot clear anything.
        if f.nullable and "type" in prop:
            prop["type"] = [prop["type"], "null"]

        properties[param_name] = prop

        # Requiredness is read straight off the declaration (contract §13's
        # presence-round amendment): a parameter is in `required` iff its
        # declared presence is `required`, flags and args alike, bools included.
        if f.presence == _PRESENCE_REQUIRED:
            required.append(param_name)

    for a in cmd.args:
        prop = _value_schema_fragment(a)
        prop["description"] = a.help

        properties[a.name] = prop

        if a.presence == _PRESENCE_REQUIRED:
            required.append(a.name)

    # The selector projection is FLATTEN plus a description map (§24.11). The
    # selector contributes one property named after itself; every scoped flag
    # contributes a top-level property and NEVER appears in `required` -- its
    # requiredness is conditional on an election, and the schema has no
    # vocabulary for that. A member-spelled selector projects IDENTICALLY to a
    # token-spelled one: tokenization is a command-line fact and there are no
    # tokens at this boundary.
    for sel in cmd.selectors:
        param = _flag_param_name(sel.name)
        properties[param] = {
            "type": "string",
            "enum": [c.name for c in sel.choices],
            "description": sel.help,
        }
        if sel.presence == _PRESENCE_REQUIRED:
            required.append(param)
    for group in cmd.sites.values():
        site = group[0]
        if site.kind == "selector":
            if not site.path:
                continue
            sel = site.decl
            properties[_flag_param_name(sel.name)] = {
                "type": "string",
                "enum": [c.name for c in sel.choices],
                "description": sel.help,
            }
            continue
        if site.kind == "member":
            payload = site.choice.payload
            if payload is None:
                continue
            prop = _value_schema_fragment(payload)
            prop["description"] = payload.help
            properties[_flag_param_name(site.name)] = prop
            continue
        f = site.decl
        prop = _value_schema_fragment(f)
        prop["description"] = f.help
        properties[_flag_param_name(f.name)] = prop

    # The rule-carrying keywords sit AFTER `required` and BEFORE
    # `additionalProperties` (§26.12): beside the two keys they qualify, ahead
    # of the key that closes the object.
    schema: dict = {
        "type": "object",
        "properties": properties,
        "required": required,
    }
    schema.update(_constraint_keywords(cmd))
    schema["additionalProperties"] = False
    return schema


# The closed set of reasons a partial projection may append (§26.12).
_MCP_REASON_SELECTORS = 'the "true" and "non_empty" selectors'
_MCP_REASON_NESTING = "the nested grouping"
_MCP_REASON_INJECTION = "the injection"


def _mcp_member_property(member_name: str, kinds: dict) -> str:
    """A leaf member's PROPERTY name -- the caller writes keys, not argv."""
    if kinds.get(member_name) == _MEMBER_KIND_ARG:
        return member_name
    return _flag_param_name(member_name)


def _constraint_leaf_properties(c: object, kinds: dict, by_name: dict) -> list[str]:
    """Every leaf property a co-occurrence constraint reaches, in order."""
    out: list[str] = []
    for m in c.members:
        if kinds.get(m.name) == _MEMBER_KIND_CONSTRAINT:
            for leaf in _constraint_leaf_properties(by_name[m.name], kinds, by_name):
                if leaf not in out:
                    out.append(leaf)
            continue
        prop = _mcp_member_property(m.name, kinds)
        if prop not in out:
            out.append(prop)
    return out


def _constraint_has_non_present_selector(
    c: object, kinds: dict, by_name: dict,
) -> bool:
    for m in c.members:
        if kinds.get(m.name) == _MEMBER_KIND_CONSTRAINT:
            if _constraint_has_non_present_selector(by_name[m.name], kinds, by_name):
                return True
            continue
        if m.resolved_when != _WHEN_PRESENT:
            return True
    return False


def _at_least_one_branches(c: object, kinds: dict, by_name: dict) -> list[dict]:
    """One `anyOf` branch per member (§26.12).

    An all-or-none member becomes ONE branch listing all of its leaves in
    `required`; a nested at-least-one's branches are INLINED into the parent's
    `anyOf`, which is what makes safegit's site project with no loss at all.
    """
    branches: list[dict] = []
    for m in c.members:
        if kinds.get(m.name) == _MEMBER_KIND_CONSTRAINT:
            nested = by_name[m.name]
            if isinstance(nested, AtLeastOne):
                branches.extend(_at_least_one_branches(nested, kinds, by_name))
            else:
                branches.append({
                    "required": _constraint_leaf_properties(nested, kinds, by_name),
                })
            continue
        branches.append({"required": [_mcp_member_property(m.name, kinds)]})
    return branches


def _constraint_keywords(cmd: Command) -> dict:
    """The JSON Schema keywords a command's constraints project (§26.12).

    These sit BESIDE `properties` and `required` on one flat object schema,
    add no structure, and degrade safely: a client that ignores an unknown
    keyword sends a call the framework refuses at call time with the parser's
    own sentence. That runtime refusal is the authority; the schema is
    advisory, which is why `anyOf` here does not contradict §24.11's refusal of
    `oneOf` for selectors.

    One at-least-one emits its branches as the object's own `anyOf`; two or
    more emit `allOf: [{anyOf: ...}, ...]`, one element per constraint in
    declaration order (§18.31 item 284). `dependentRequired` needs no
    equivalent: it is a map, so every all-or-none and every `requires` merges
    into the one key by name, deduplicated and in declaration order.
    """
    update_branches = _update_any_of_branches(cmd)
    if not cmd.constraints and not update_branches:
        return {}
    kinds, by_name = _constraint_index(cmd)
    any_ofs: list[list[dict]] = []
    dependent_required: dict[str, list[str]] = {}
    # The at-least-one-property rule is NOT a constraint (§26.14's answer,
    # §27.4) but it borrows this machinery, wrapping pin included: it counts as
    # an `anyOf`-producing rule, and its branch comes FIRST, it being the
    # command's own declaration rather than an entry in the constraint list.
    if update_branches:
        any_ofs.append(update_branches)

    def add_dependency(key: str, values: list[str]) -> None:
        bucket = dependent_required.setdefault(key, [])
        for v in values:
            if v not in bucket:
                bucket.append(v)

    for c in cmd.constraints:
        if isinstance(c, AtLeastOne):
            any_ofs.append(_at_least_one_branches(c, kinds, by_name))
        elif isinstance(c, AllOrNone):
            nested = any(
                kinds.get(m.name) == _MEMBER_KIND_CONSTRAINT for m in c.members
            )
            if nested or _constraint_has_non_present_selector(c, kinds, by_name):
                # The keyword cannot carry a group as an operand, and it says a
                # key is present rather than true or non-empty.
                continue
            props = [_mcp_member_property(m.name, kinds) for m in c.members]
            for p in props:
                add_dependency(p, [o for o in props if o != p])
        elif isinstance(c, Requires):
            add_dependency(
                _flag_param_name(c.flag), [_flag_param_name(c.depends_on)],
            )
    keywords: dict = {}
    if len(any_ofs) == 1:
        keywords["anyOf"] = any_ofs[0]
    elif len(any_ofs) > 1:
        # One object schema carries one `anyOf`, and two at-least-one
        # constraints are two independent rules that must BOTH hold: merging
        # their branches would say "satisfy either rule", and dropping one
        # would be the silent omission §26.12 forbids.
        keywords["allOf"] = [{"anyOf": branches} for branches in any_ofs]
    if dependent_required:
        keywords["dependentRequired"] = dependent_required
    return keywords


def _constraint_mcp_sentence(c: object, kinds: dict, by_name: dict) -> str:
    """The constraint's own sentence in the description block (§26.12).

    Members render in PROPERTY names, never CLI tokens: the caller writes
    keys, not argv. `implies` states its injected value as ` = <value>`,
    spelled `true` or `false` -- a false value is a VALUE, not a name, and
    rendering it as `no_<target>` would describe a key the schema does not
    carry and the caller can never send, hiding the one fact the line exists
    to publish (§18.31 item 283). The CLI's `--no-` negation stays where a
    reader types tokens (§26.10). Neither dependency line says anything else:
    no per-family commentary, and no clause of its own.
    """
    if isinstance(c, AtLeastOne):
        return f"at least one of: {_render_member_list(c, kinds, by_name, False)}"
    if isinstance(c, AllOrNone):
        return f"all or none of: {_render_member_list(c, kinds, by_name, False)}"
    if isinstance(c, Requires):
        return (
            f"{_flag_param_name(c.flag)} requires "
            f"{_flag_param_name(c.depends_on)}"
        )
    value = "true" if c.value else "false"
    return (
        f"{_flag_param_name(c.flag)} implies "
        f"{_flag_param_name(c.implies)} = {value}"
    )


def _constraint_loss_reason(c: object, kinds: dict, by_name: dict) -> str:
    """The remainder clause's reason, from a CLOSED set (§26.12).

    An EXACT projection takes no clause at all, so the presence of a clause
    tells a reader exactly where the schema is weaker than the rule.
    """
    if isinstance(c, Implies):
        return _MCP_REASON_INJECTION
    if isinstance(c, Requires):
        return ""
    if isinstance(c, AtLeastOne):
        # The nesting itself is carried exactly: an all-or-none member becomes
        # one branch and a nested at-least-one's branches are inlined. Only
        # the selectors are lost, and that verdict is RECURSIVE -- a
        # `when="true"` at any depth is a rule the parent's own `anyOf` states
        # falsely, its branch claiming the key's presence is enough.
        if _constraint_has_non_present_selector(c, kinds, by_name):
            return _MCP_REASON_SELECTORS
        return ""
    if any(kinds.get(m.name) == _MEMBER_KIND_CONSTRAINT for m in c.members):
        return _MCP_REASON_NESTING
    if _constraint_has_non_present_selector(c, kinds, by_name):
        return _MCP_REASON_SELECTORS
    return ""


def _constraint_description_line(
    c: object, kinds: dict, by_name: dict,
) -> str:
    """One line of the `Constraints (enforced at call time):` block (§26.12).

    A PARTIAL projection appends ` -- not expressed in the schema: <reason>`
    from a closed set; an EXACT one appends no clause at all. The block names
    every constraint either way, so the presence of a clause tells a reader
    exactly where the schema is weaker than the rule -- there is no third
    verdict in which a rule reaches the boundary unstated. The clause is
    appended HERE, by the block's own mechanism, for every family alike.
    """
    sentence = _constraint_mcp_sentence(c, kinds, by_name)
    reason = _constraint_loss_reason(c, kinds, by_name)
    if not reason:
        return sentence
    return f"{sentence} -- not expressed in the schema: {reason}"


def _constraint_description_block(cmd: Command) -> list[str]:
    if not cmd.constraints:
        return []
    kinds, by_name = _constraint_index(cmd)
    lines = ["Constraints (enforced at call time):"]
    for c in cmd.constraints:
        lines.append(f"  {_constraint_description_line(c, kinds, by_name)}")
    return lines


def _tool_description(cmd: Command, help_text: str) -> str:
    """The tool description, plus the scope block a selector needs (§24.11)
    and the constraint block §26.12 adds beside it.

    The scope structure survives in the DESCRIPTION, appended as a
    deterministic block so an agent can read the constraint it cannot see in
    the schema. The cost is stated rather than discovered: an agent cannot see
    the scope rule before it calls; it learns by being refused. The constraint
    block follows it, separated by a blank line when both exist, and a
    constraint is never silently dropped from a tool schema.
    """
    blocks: list[str] = []
    if cmd.selectors:
        lines: list[str] = ["Scoped parameters (enforced at call time):"]
        _scope_description_lines(cmd.selectors, (), lines)
        blocks.append("\n".join(lines))
    constraint_lines = _constraint_description_block(cmd)
    if constraint_lines:
        blocks.append("\n".join(constraint_lines))
    # The update block, appended after both when they exist and separated by a
    # blank line -- the established order (contract §27.10).
    update_lines = _update_description_lines(cmd)
    if update_lines:
        blocks.append("\n".join(update_lines))
    if not blocks:
        return help_text
    return help_text + "\n\n" + "\n\n".join(blocks)


def _scope_description_lines(
    members: tuple, path: tuple, out: list[str],
) -> None:
    """One line per scope, at every depth, in declaration order (§24.11)."""
    for m in members:
        if not isinstance(m, _Selector):
            continue
        for c in m.choices:
            child = path + ((_flag_param_name(m.name), c.name),)
            key = " ".join(f"{s}={v}" for s, v in child)
            params: list[str] = []
            # A member's payload is a parameter of the scope, listed first and
            # under the member's OWN name -- `target=profile: profile
            # (required)` (§24.11, item 222). It is the schema property an
            # agent must send to elect that member with a value, so a block
            # that omitted it described the scope as empty.
            if c.payload is not None:
                params.append(
                    f"{_flag_param_name(c.payload.name)} "
                    f"({_scope_presence_text(c.payload)})"
                )
            for entry in c.members:
                name = _flag_param_name(entry.name)
                if isinstance(entry, _Selector):
                    presence = (
                        "required" if entry.presence == _PRESENCE_REQUIRED
                        else f"default: "
                        f"{entry.choice_by_class(type(entry.default)).name}"
                    )
                else:
                    presence = _scope_presence_text(entry)
                params.append(f"{name} ({presence})")
            out.append(
                f"  {key}: " + (", ".join(params) if params else "(no parameters)")
            )
            _scope_description_lines(c.members, child, out)


def _scope_presence_text(f: Flag) -> str:
    if f.presence == _PRESENCE_REQUIRED:
        return "required"
    if f.presence == _PRESENCE_OPTIONAL:
        return "optional"
    return f"default: {_format_declared_default_for_help(f)}"


def _tokens_contain_help(tokens: list[str]) -> bool:
    """Check if --help or -h appears in tokens before any -- separator."""
    for tok in tokens:
        if tok == "--":
            return False
        if tok == "--help" or tok == "-h":
            return True
    return False


def _validate_choices(
    name: str,
    val: object,
    repeatable: bool,
    choices: list | None,
    retired: tuple["RetiredChoice", ...] | None = None,
    *,
    is_arg: bool = False,
) -> None:
    """Validate a resolved flag or arg value against its choices list.

    Raises _ParseError on an invalid value. is_arg selects the message prefix
    ("argument 'name':" instead of "--name:"); the two f-strings are kept as
    full literals so conformance/check_error_parity.py can extract them.
    A None value is exempt from validation: None only arises when the flag or
    arg was not passed (an unset mutex flag, or default=None on an arg) -- a
    CLI-supplied value is never None.

    A RETIRED spelling is checked first, so a reader who typed a value that
    used to work is told what replaced it instead of being handed the list it
    is missing from. Every source that reaches this funnel today -- command
    line, env var, config file, and the programmatic doors -- takes the retired
    refusal for free; nothing new is resolved here.
    """
    if (choices is None and retired is None) or val is None:
        return
    vals = val if repeatable else [val]
    for v in vals:
        message, is_retired = _retired_choice_message(v, retired)
        if is_retired:
            v_str = _format_value_for_error(v)
            if is_arg:
                raise _ParseError(
                    f"argument '{name}': value '{v_str}' retired: {message}"
                )
            raise _ParseError(f"--{name}: value '{v_str}' retired: {message}")
        if choices is not None and v not in choices:
            choices_str = ", ".join(
                _format_float_canonical(c) if isinstance(c, float) else str(c)
                for c in choices
            )
            v_str = _format_float_canonical(v) if isinstance(v, float) else str(v)
            if is_arg:
                raise _ParseError(
                    f"argument '{name}': invalid value '{v_str}', "
                    f"must be one of: {choices_str}"
                )
            raise _ParseError(
                f"--{name}: invalid value '{v_str}', must be one of: {choices_str}"
            )


def _raise_flag_required(f: Flag):
    """The presence refusal a required flag takes at ROOT scope (§23, §12.13).

    Which sentence a declaration takes is a fact about the declaration and not
    about where it was written: a required bool names the TOKENS that satisfy
    it -- `--x` IS the value and `--no-x` is the other one, so "is required"
    would leave a reader looking for a value to type -- and a non-negatable
    bool names the only token it has. The scoped path calls this and appends
    §12.13's suffixes to whatever it says, so one declaration never says two
    different things about itself (§18.24 item 245).
    """
    if f.type is bool and f.negatable:
        raise _ParseError(
            f"flag '--{f.name}' must be passed as "
            f"--{f.name} or --no-{f.name}"
        )
    if f.type is bool and not f.negatable:
        raise _ParseError(
            f"flag '--{f.name}' must be passed as --{f.name}"
        )
    raise _ParseError(f"flag '--{f.name}' is required")


def _validate_and_build_kwargs(
    cmd: Command,
    store: _SourcedStore,
    positionals: list[str],
    global_flag_names: set[str],
    infra_roots: dict[str, str] | None = None,
    selector_result: "_SelectorResult | None" = None,
    pre_typed_args: dict[str, object] | None = None,
    machine_boundary: bool = False,
    unsets: set | None = None,
) -> tuple[
    Command, dict[str, object], dict[str, object], dict[str, str],
    "_UpdateState | None",
]:
    """Validate parsed values and build the kwargs dict for the command handler.

    This is the second half of command parsing: implies resolution, dependency
    checks, defaults, choices validation, custom validation, positional arg
    resolution, and kwargs building. It operates on sourced values in the store
    and doesn't care how they were produced.

    ``selector_result`` carries the already-elected records (contract §24): a
    selector's four parse phases run before this function, because a scope
    violation must be reported before any missing-required-flag consequence of
    it (§24.3's precedence rule).

    ``pre_typed_args`` carries the positionals of a PROGRAMMATIC call, keyed by
    arg name and already of the caller's own types -- the argv path's
    ``positionals`` is a list of tokens waiting to be parsed, and the two
    cannot be the same list. When it is supplied, ``positionals`` is empty and
    step 6 checks each supplied value against its declaration instead of
    parsing a token (§24.11's rule read onto §23.3's declaration). The step
    itself is unmoved, so a bad positional value keeps the argv path's own
    place among the phases at both doors. ``machine_boundary`` says which door
    those values came through: the flat machine form accommodates a JSON
    decoder's integral number for an `int` declaration and the record front
    door does not (§24.11 item 247). It says nothing on the argv path, where
    every positional is a token that has yet to be parsed.

    ``unsets`` names the properties this invocation CLEARED (§27.6):
    ``--unset-<prop>`` on the command line, or ``null`` on the property's own
    key at a machine door. A cleared property is a provision, so it satisfies
    the at-least-one-property rule and joins the write set.

    Returns (cmd, kwargs, global_cli_set, sources, writes) where sources maps
    flag param names to source labels (cli/env/config/default/implied) and
    writes is this invocation's write set, None on every command that declares
    no update.
    """
    # Step 4.55: resolve Implies injections (before the co-occurrence families,
    # so an implied value can engage a member -- §26.4's pipeline position).
    # Implied values are stored with _Source.IMPLIED.
    for dep in cmd.constraints:
        if isinstance(dep, Implies):
            if store.is_present_for_deps(dep.flag):
                if store.has(dep.implies):
                    if store[dep.implies] != dep.value:
                        neg = "no-" if not dep.value else ""
                        explicit_neg = "" if not dep.value else "no-"
                        raise _ParseError(
                            _msg_constraint_prefix(dep.name)
                            + f"flag '--{dep.flag}' implies '--{neg}{dep.implies}', "
                            f"but '--{explicit_neg}{dep.implies}' was explicitly provided"
                        )
                else:
                    store.set(dep.implies, dep.value, _Source.IMPLIED)

    # Step 4.6: enforce the constraint system (before defaults, so a declared
    # default cannot engage a member). is_present_for_deps: cli, env, config
    # and implied count; default and infra do NOT -- and a selector member
    # reads the same distinction off its election's own source (§26.2).
    selector_engaged: dict[str, bool] = {}
    if selector_result is not None:
        for sel in cmd.selectors:
            if sel.is_member_spelled:
                continue
            source = selector_result.sources.get(_flag_param_name(sel.name))
            if source is not None:
                selector_engaged[sel.name] = source != "default"
    _enforce_constraints(
        cmd, store, _arg_engagement_inputs(cmd, positionals, pre_typed_args),
        selector_engaged,
    )

    # The at-least-one-property rule, and the write set it guarantees is never
    # empty (contract §27.4, §27.5). It is the framework's own rule about a
    # declared property set rather than a constraint the command wrote, so it
    # is evaluated after every declared constraint has had its say -- and, like
    # them, BEFORE defaults are applied, over the one provided-ness predicate
    # with no source filter.
    writes = _evaluate_update(cmd, store, unsets or set())

    # Step 5: apply the declared presence (contract §23.1). Nothing is derived
    # here any more: no empty-collection default for compound flags, no
    # "no default means required" branch, and no mutex-member exemption -- a
    # member declares `optional` like anything else and the group enforces
    # cardinality on top of that.
    for f in cmd.flags:
        if store.has(f.name):
            continue
        if f.presence == _PRESENCE_DEFAULT:
            if isinstance(f.default, RelativeToRoot):
                # A RelativeToRoot marker resolves through the declared infra
                # roots and reports source "infra" (a declared default whose
                # label says WHICH default it was).
                resolved = _resolve_infra_root_path(f.default, infra_roots or {})
                store.set(f.name, resolved, _Source.INFRA)
            elif f.compound == "dict":
                store.set(f.name, dict(f.default), _Source.DEFAULT)
            elif f.repeatable:
                store.set(f.name, list(f.default), _Source.DEFAULT)
            else:
                store.set(f.name, f.default, _Source.DEFAULT)
        elif f.presence == _PRESENCE_OPTIONAL:
            # Absence is delivered as absence, for every type: a compound flag
            # that wants an empty collection declares one.
            store.set(f.name, None, _Source.DEFAULT)
        else:
            # Declared required and nothing supplied it, from any source.
            _raise_flag_required(f)

    # Step 5.5: validate choices
    for f in cmd.flags:
        if store.has(f.name):
            _validate_choices(
                f.name, store[f.name], f.repeatable, f.choices,
                f.retired_choices,
            )

    # Step 5.6: custom validation. It runs on a SUPPLIED value only: never on
    # absence, and never on a declared default (§23.5's validate row).
    for f in cmd.flags:
        if f.validate is None or not store.is_present_for_deps(f.name):
            continue
        value = store[f.name]
        if value is None:
            continue
        if f.repeatable:
            for val in value:
                try:
                    f.validate(val)
                except ValueError as e:
                    raise _ParseError(f"--{f.name}: {e}")
        else:
            try:
                f.validate(value)
            except ValueError as e:
                raise _ParseError(f"--{f.name}: {e}")

    # Step 6: resolve positional args
    arg_values: dict[str, object] = {}
    has_variadic = cmd.args and cmd.args[-1].variadic
    fixed_args = cmd.args[:-1] if has_variadic else cmd.args
    if pre_typed_args is not None:
        # The programmatic doors: the value is the caller's own, so nothing
        # parses it and nothing stringifies it -- the declaration checks it
        # (§24.11 item 240, read onto a positional). Presence is answered by
        # the key's absence, exactly as an argv path answers it with a token
        # that was never typed.
        for a in cmd.args:
            if a.name in pre_typed_args:
                value = _check_pre_typed_arg_value(
                    a, pre_typed_args[a.name],
                    machine_boundary=machine_boundary,
                )
                if (
                    a.variadic
                    and a.presence == _PRESENCE_REQUIRED
                    and len(value) == 0  # type: ignore[arg-type]
                ):
                    # An empty array is the flat spelling of no tokens at all.
                    raise _ParseError(f"missing required argument '{a.name}'")
                arg_values[a.name] = value
            elif a.variadic:
                if a.presence == _PRESENCE_REQUIRED:
                    raise _ParseError(f"missing required argument '{a.name}'")
                arg_values[a.name] = []
            elif a.presence == _PRESENCE_REQUIRED:
                raise _ParseError(f"missing required argument '{a.name}'")
            elif a.presence == _PRESENCE_DEFAULT:
                arg_values[a.name] = a.default
            else:
                arg_values[a.name] = None
    else:
        for idx, a in enumerate(fixed_args):
            if idx < len(positionals):
                arg_values[a.name] = _coerce_arg_value(a, positionals[idx])
            elif a.presence == _PRESENCE_REQUIRED:
                raise _ParseError(f"missing required argument '{a.name}'")
            elif a.presence == _PRESENCE_DEFAULT:
                arg_values[a.name] = a.default
            else:
                # An optional arg delivers absence as a PRESENT key, never as a
                # missing kwarg (contract §23.3).
                arg_values[a.name] = None
        if has_variadic:
            va = cmd.args[-1]
            remaining_positionals = positionals[len(fixed_args):]
            if va.presence == _PRESENCE_REQUIRED and len(remaining_positionals) == 0:
                raise _ParseError(f"missing required argument '{va.name}'")
            arg_values[va.name] = [
                _coerce_arg_value(va, p) for p in remaining_positionals
            ]
        elif len(positionals) > len(cmd.args):
            raise _ParseError(f"unexpected argument '{positionals[len(cmd.args)]}'")

    # Step 6.5: validate arg choices
    for a in cmd.args:
        if a.name in arg_values:
            _validate_choices(
                a.name, arg_values[a.name], a.variadic, a.choices,
                a.retired_choices, is_arg=True,
            )

    # Step 7: build kwargs dict (command flags only)
    kwargs: dict[str, object] = {}
    for f in cmd.flags:
        kwargs[_flag_param_name(f.name)] = store[f.name]
    for a in cmd.args:
        if a.name in arg_values:
            kwargs[a.name] = arg_values[a.name]
    # Delivery is ONE tagged value per selector, under the selector's own key.
    # Sub-flags are never top-level handler arguments, at any depth, which is
    # what keeps §23's delivery invariant untouched rather than merely
    # compatible (§24.1).
    if selector_result is not None:
        kwargs.update(selector_result.values)

    # Separate out global flag values parsed from post-command tokens
    global_cli_set: dict[str, object] = {}
    for name in global_flag_names:
        if store.has(name):
            global_cli_set[name] = store[name]

    # Build source map: param-name -> source label (for Context.source())
    sources: dict[str, str] = {}
    raw_sources = store.source_map()
    for f in cmd.flags:
        if f.name in raw_sources:
            sources[_flag_param_name(f.name)] = raw_sources[f.name]
    # Global flags parsed post-command emit their source label too (always
    # "cli" here, since post-command tokens are CLI-only -- env and config for
    # globals are resolved in the pre-command global-flag pass). Without this,
    # `tool cmd --global X` would report source "default" for the global.
    for name in global_flag_names:
        if name in raw_sources:
            sources[_flag_param_name(name)] = raw_sources[name]
    # A selector's own key is in the per-parse store like any flag's, and
    # answers `ctx.provided` / `ctx.source` as any flag does (§24.5, §24.9).
    # Its SCOPE's names are deliberately absent: a scoped name is not unique
    # command-wide, so asking for one raises the existing unknown-name error
    # rather than inventing a second vocabulary.
    if selector_result is not None:
        sources.update(selector_result.sources)

    return cmd, kwargs, global_cli_set, sources, writes


# ---------------------------------------------------------------------------
# Selector parsing (contract §24.3)
#
# Parsing is PHASED, and the phases are what make order independence, the
# distinct out-of-scope error, and that error's priority over a missing
# required flag fall out instead of being special-cased:
#
#   1. tokenize every occurrence, without interpreting any of it
#   2. resolve elections, outermost first, then recursively inside each
#      elected choice
#   3. validate scope membership of every supplied flag
#   4. resolve values and presence within the LIVE scopes only
#
# Error precedence is pinned by that order: election -> scope -> value ->
# presence, so a command line with several problems reports the same error
# every time and never one that depends on declaration order.
# ---------------------------------------------------------------------------


@dataclass
class _Occ:
    """One scoped token occurrence, uninterpreted."""

    name: str
    raw: object  # str for value tokens, True/False for bool-style tokens
    token: str
    # Candidate names, when a short is reused by sibling scopes: which one it
    # binds to is decided once the elections are known (§24.7).
    alts: tuple[str, ...] = ()
    # The argv index this occurrence was tokenized from, which is what lets the
    # value phase sweep root and scoped occurrences in ONE command-line order
    # (§24.3). Occurrences the flat door manufactures carry no argv position and
    # never reach that sweep.
    seq: int = -1


@dataclass
class _ElectionState:
    """What phase 2 decided, read by phases 3 and 4."""

    elected: dict[tuple, "_ChoiceSpec | None"] = field(default_factory=dict)
    origin: dict[tuple, str] = field(default_factory=dict)
    from_default: set[tuple] = field(default_factory=set)
    declined: dict[tuple, list[str]] = field(default_factory=dict)


@dataclass
class _SelectorResult:
    """The delivered records, their sources, and the run's skipped bindings."""

    values: dict[str, object] = field(default_factory=dict)
    sources: dict[str, str] = field(default_factory=dict)
    diagnostics: list[str] = field(default_factory=list)


def _msg_election_origin_env(var: str) -> str:
    return f" from env var '{var}'"


def _msg_election_origin_config(key: str) -> str:
    return f" from config key '{key}'"


_MSG_ELECTION_ORIGIN_DEFAULT = " by default"


def _msg_election_origin_suffix(origin: str) -> str:
    """` (elected<origin>)`, appended AFTER the scope suffix (§12.13).

    A command-line election produces the EMPTY suffix rather than a bare
    `(elected)`: the wrapper exists exactly when the clause it wraps does.
    """
    if not origin:
        return ""
    return f" (elected{origin})"


def _msg_scope_suffix(path_text: str) -> str:
    """` under '<scope path>'`, empty at root scope (§12.13)."""
    if not path_text:
        return ""
    return f" under '{path_text}'"


def _msg_flag_out_of_scope(x: str, owners: str, why: str) -> str:
    return f"flag '--{x}' is only valid under {owners}, but {why}"


def _msg_scope_why_elected(path_text: str, origin: str) -> str:
    return f"'{path_text}' was elected{origin}"


def _msg_scope_why_not_provided(sel: str) -> str:
    return f"'--{sel}' was not provided"


def _msg_scope_why_no_member_elected(members: str) -> str:
    return f"none of {members} was elected"


def _msg_selector_elected_twice(sel: str, values: list[str]) -> str:
    spelled = " and ".join(f"'{v}'" for v in values)
    return f"--{sel}: elected more than once, as {spelled}"


def _msg_ambient_binding_skipped_env(var: str, x: str, path_text: str) -> str:
    return (
        f"not consulted: env var '{var}' binds flag '--{x}' under "
        f"'{path_text}', which was not elected"
    )


def _msg_ambient_binding_skipped_config(key: str, x: str, path_text: str) -> str:
    return (
        f"not consulted: config key '{key}' binds flag '--{x}' under "
        f"'{path_text}', which was not elected"
    )


def _sel_key(path: tuple, sel_name: str) -> tuple:
    return path + ((sel_name, "*"),)


def _path_is_live(path: tuple, state: _ElectionState) -> bool:
    prefix: tuple = ()
    for sel_name, choice_name in path:
        elected = state.elected.get(_sel_key(prefix, sel_name))
        if elected is None or elected.name != choice_name:
            return False
        prefix = prefix + ((sel_name, choice_name),)
    return True


def _path_origin(path: tuple, state: _ElectionState) -> str:
    """The outermost non-command-line election on a live path, if any.

    An election from a non-CLI source names itself in every message it causes
    (§24.6): a required sub-flag missing under a scope elected by an
    environment variable would otherwise blame a command line that does not
    contain the cause.
    """
    prefix: tuple = ()
    for sel_name, choice_name in path:
        origin = state.origin.get(_sel_key(prefix, sel_name), "")
        if origin:
            return origin
        prefix = prefix + ((sel_name, choice_name),)
    return ""


def _elect_all(
    members: tuple,
    path: tuple,
    occs: list[_Occ],
    state: _ElectionState,
    *,
    config_data: dict | None,
    hermetic: bool,
    pre_typed: bool,
) -> None:
    """Phase 2: resolve elections, outermost first, then recursively."""
    for m in members:
        if not isinstance(m, _Selector):
            continue
        key = _sel_key(path, m.name)
        elected: _ChoiceSpec | None = None
        origin = ""
        if m.is_member_spelled:
            elected, origin = _elect_member_spelled(m, occs, state, key)
        else:
            elected, origin = _elect_token_spelled(
                m, occs, config_data=config_data, hermetic=hermetic,
                pre_typed=pre_typed,
            )
        state.elected[key] = elected
        state.origin[key] = origin
        if origin == _MSG_ELECTION_ORIGIN_DEFAULT:
            state.from_default.add(key)
        if elected is not None:
            _elect_all(
                elected.members, path + ((m.name, elected.name),), occs, state,
                config_data=config_data, hermetic=hermetic, pre_typed=pre_typed,
            )


def _elect_token_spelled(
    sel: _Selector,
    occs: list[_Occ],
    *,
    config_data: dict | None,
    hermetic: bool,
    pre_typed: bool,
) -> tuple["_ChoiceSpec | None", str]:
    """A token-spelled selector elects from any source (§24.6, ruling S5)."""
    seen = [o for o in occs if o.name == sel.name]
    if len(seen) > 1:
        # Last-wins is right for a plain flag and wrong for an election:
        # discarding a value would discard a whole scope with it (§12.13).
        raise _ParseError(_msg_selector_elected_twice(
            sel.name, [str(o.raw) for o in seen],
        ))
    if seen:
        return _lookup_choice(sel, str(seen[0].raw)), ""
    if not pre_typed and not hermetic and sel.env is not None:
        env_val = os.environ.get(sel.env)
        if env_val is not None:
            return (
                _lookup_choice(sel, env_val),
                _msg_election_origin_env(sel.env),
            )
    if not pre_typed and not hermetic and config_data:
        key = _flag_param_name(sel.name)
        if key in config_data:
            raw = config_data[key]
            if not isinstance(raw, str):
                raise _ParseError(
                    f"--{sel.name}: config value error: expected str, "
                    f"got {_config_typename(raw)}"
                )
            return (
                _lookup_choice(sel, raw),
                _msg_election_origin_config(key),
            )
    if sel.presence == _PRESENCE_DEFAULT:
        return (
            sel.choice_by_class(type(sel.default)),
            _MSG_ELECTION_ORIGIN_DEFAULT,
        )
    return None, ""


def _lookup_choice(sel: _Selector, value: str) -> "_ChoiceSpec":
    """Resolve a choice by name, reusing the existing invalid-choice sentence.

    A value that names no declared choice gains NO new template: a migrated
    declaration must not change the bytes a user reads (§12.13's reuse table).
    """
    spec = sel.choice_by_name(value)
    if spec is None:
        names = ", ".join(c.name for c in sel.choices)
        raise _ParseError(
            f"--{sel.name}: invalid value '{value}', must be one of: {names}"
        )
    return spec


def _elect_member_spelled(
    sel: _Selector,
    occs: list[_Occ],
    state: _ElectionState,
    key: tuple,
) -> tuple["_ChoiceSpec | None", str]:
    """A member-spelled selector elects from the command line ONLY (§24.6).

    §21's per-type election rules survive verbatim here: a payload-less member
    elects only on `--<name>` and `--no-<name>` DECLINES; a payload-carrying
    member elects on presence with any value, including "".
    """
    elected: list[_ChoiceSpec] = []
    declined: list[_ChoiceSpec] = []
    for c in sel.choices:
        hits = [o for o in occs if o.name == c.name]
        if not hits:
            continue
        if c.payload is None:
            if hits[-1].raw is True:
                elected.append(c)
            else:
                declined.append(c)
        else:
            elected.append(c)
    state.declined[key] = [c.name for c in declined]
    clause = (
        _msg_mutex_decline_clause(declined[0].name) if declined else ""
    )
    if len(elected) > 1:
        names = " and ".join(f"--{c.name}" for c in elected)
        raise _ParseError(f"{names} are mutually exclusive")
    if len(elected) == 1 and declined:
        declined_names = " and ".join(f"--no-{c.name}" for c in declined)
        raise _ParseError(
            f"{declined_names} cannot be combined with "
            f"--{elected[0].name}{clause}"
        )
    if elected:
        return elected[0], ""
    if sel.presence == _PRESENCE_DEFAULT:
        return (
            sel.choice_by_class(type(sel.default)),
            _MSG_ELECTION_ORIGIN_DEFAULT,
        )
    return None, ""


def _resolve_short_occurrences(
    cmd: Command, occs: list[_Occ], state: _ElectionState,
) -> None:
    """Bind a sibling-reused short to the name whose scope is live (§24.7)."""
    for occ in occs:
        if len(occ.alts) < 2:
            continue
        for candidate in occ.alts:
            if any(
                _path_is_live(s.path, state) for s in cmd.sites.get(candidate, ())
            ):
                occ.name = candidate
                break


def _validate_scopes(
    cmd: Command, occs: list[_Occ], state: _ElectionState,
) -> None:
    """Phase 3: every supplied flag must sit in a live scope (§24.3)."""
    member_spelled = _member_spelling_map(cmd.selectors)
    for occ in occs:
        group = cmd.sites.get(occ.name, ())
        if any(_path_is_live(s.path, state) for s in group):
            continue
        owners = " or ".join(
            f"'{_render_scope_path(s.path, member_spelled)}'" for s in group
        )
        why = _scope_why(group[0], state, cmd, member_spelled)
        raise _ParseError(_msg_flag_out_of_scope(occ.name, owners, why))


def _scope_why(
    site: _Site,
    state: _ElectionState,
    cmd: Command,
    member_spelled: dict[str, bool],
) -> str:
    """Blame the OUTERMOST unsatisfied election on the first owner's path.

    A flag two levels down whose outer election is the one that failed blames
    the outer election, not the dead selector directly above it: that is the
    token the reader would have to change (§24.3, §12.13).
    """
    prefix: tuple = ()
    for sel_name, choice_name in site.path:
        key = _sel_key(prefix, sel_name)
        elected = state.elected.get(key)
        if elected is None:
            sel = _find_selector(cmd.selectors, sel_name)
            if sel is not None and sel.is_member_spelled:
                members = ", ".join(f"--{c.name}" for c in sel.choices)
                return _msg_scope_why_no_member_elected(members)
            return _msg_scope_why_not_provided(sel_name)
        if elected.name != choice_name:
            elected_path = _render_scope_path(
                prefix + ((sel_name, elected.name),), member_spelled,
            )
            return _msg_scope_why_elected(
                elected_path, state.origin.get(key, ""),
            )
        prefix = prefix + ((sel_name, choice_name),)
    raise AssertionError("site is in scope")  # pragma: no cover


def _live_site(
    cmd: Command, state: _ElectionState, name: str,
) -> "_Site | None":
    """The one declaration a supplied scoped name has THIS run (§24.7).

    Sibling scopes may reuse a name, and they are mutually exclusive, so at
    most one of a name's sites is ever live. Scope validation has already run
    when this is consulted, so a name with no live site is one the parse
    already refused.
    """
    for site in cmd.sites.get(name, ()):
        if _path_is_live(site.path, state):
            return site
    return None


def _coerce_scoped_occurrence(
    site: "_Site", occ: _Occ, store: dict, stdin_consumed_by: list,
) -> None:
    """Coerce ONE scoped occurrence into ``store``, keyed by the flag's name.

    The per-occurrence shape is what makes the value phase one command-line
    ordered sweep across root and scoped tokens alike (§24.3): every
    occurrence is interpreted where it was typed, so `--retries nope
    --retries 1` reports the token that will not parse instead of quietly
    keeping the last one. Root-scope and scoped names are disjoint by
    registration (§24.7), so one store serves every live scope.
    """
    f = site.value_flag
    if f.compound == "dict":
        _store_dict_flag(f, str(occ.raw), store)
        return
    if f.repeatable:
        coerced = _coerce_scoped_value(f, occ.raw, stdin_consumed_by)
        collected = store.setdefault(f.name, [])
        if f.unique and coerced in collected:
            raise _ParseError(
                f"--{f.name}: duplicate value "
                f"'{_format_value_for_error(coerced)}'"
            )
        collected.append(coerced)
        return
    store[f.name] = _coerce_scoped_value(f, occ.raw, stdin_consumed_by)


def _find_selector(selectors: tuple, name: str) -> "_Selector | None":
    for s in selectors:
        if not isinstance(s, _Selector):
            continue
        if s.name == name:
            return s
        for c in s.choices:
            found = _find_selector(tuple(c.members), name)
            if found is not None:
                return found
    return None


def _report_skipped_bindings(
    members: tuple,
    path: tuple,
    state: _ElectionState,
    out: list[str],
    *,
    config_data: dict | None,
    member_spelled: dict[str, bool],
    live: bool = True,
) -> None:
    """Name every ambient binding a non-elected scope skipped (§24.6).

    Conditional bindings are a DECLARATION property, not a runtime adaptation:
    the binding's condition is written in the declaration, the framework
    evaluates the same condition the same way every run, and the same command
    line plus the same environment always produces the same values. What is
    refused is the SILENT part -- and it is refused by surfacing.
    """
    for m in members:
        if isinstance(m, Flag):
            if live:
                continue
            path_text = _render_scope_path(path, member_spelled)
            if m.env is not None and os.environ.get(m.env) is not None:
                out.append(_msg_ambient_binding_skipped_env(
                    m.env, m.name, path_text,
                ))
            key = _flag_param_name(m.name)
            if config_data and key in config_data:
                out.append(_msg_ambient_binding_skipped_config(
                    key, m.name, path_text,
                ))
            continue
        for c in m.choices:
            child = path + ((m.name, c.name),)
            _report_skipped_bindings(
                c.members, child, state, out,
                config_data=config_data, member_spelled=member_spelled,
                live=live and _path_is_live(child, state),
            )


def _widen_integral_number(value: object, declared: type) -> object:
    """An integral number satisfies an `int` declaration (§24.11 item 247).

    JSON has ONE number type, and a document may write an integer as ``7.0``.
    Go's decoder produces a `float64` for every number and TypeScript's a
    `number`, so refusing an integral float at the machine boundary would
    refuse every integer that door can carry -- both accommodate it already.
    Python's decoder is the one that keeps the distinction, so the
    accommodation is the DOOR's rather than the decoder's: it applies where a
    machine boundary produced the value and nowhere else, which is why the
    caller has to ask for it.

    A FRACTIONAL number is left exactly as it is and earns the declaration's
    own refusal, and the widening reaches an `int` declaration only -- a float
    handed to a `str` or a `bool` is still a float, which is what Python's
    runtime holds and therefore what a refusal names.
    """
    if declared is int and isinstance(value, float) and value.is_integer():
        return int(value)
    return value


def _widen_pre_typed_value(f: Flag, value: object) -> object:
    """:func:`_widen_integral_number` over one flag's whole value shape."""
    if f.compound == "dict":
        if isinstance(value, dict):
            return {
                k: _widen_integral_number(v, f.value_type)
                for k, v in value.items()
            }
        return value
    if f.repeatable:
        if isinstance(value, list):
            return [_widen_integral_number(v, f.type) for v in value]
        return value
    return _widen_integral_number(value, f.type)


def _check_pre_typed_value(
    f: Flag, value: object, *, machine_boundary: bool,
) -> object:
    """Check one PRE-TYPED value against its declaration (§24.11).

    The flat machine form and the programmatic front door hand the framework
    values that are already typed, so nothing parses them -- but the
    declaration still decides what they may be, exactly as it does for a token
    that has to be parsed first. *Pre-typed* means ALREADY OF THE DECLARED
    TYPE, never exempt from the declaration. The check is the one the config
    reader already runs over an already-typed document
    (:func:`_coerce_config_value`, the same closed set of four types with the
    same sentences), because a flat object and a config document pose the
    identical question: does this value satisfy the flag's declared type?

    ``None`` is not a legal value for anything. Optionality has ONE spelling
    (§23.4): a flag that may be absent declares ``optional`` and is delivered
    absent when the key is simply not there, so a null carries nothing the
    declaration cannot already say -- and a flag that may NOT be absent would
    otherwise have its presence rule answered by a value the declaration
    forbids.

    ``machine_boundary`` says which DOOR the value came through, because one
    thing about a value depends on it: an integral number satisfies an `int`
    declaration where a JSON decoder produced it (§24.11 item 247), and
    Python's own numeric model governs where one did not. The flat machine form
    passes it; the record front door -- `call()`, which takes Python's own
    values -- does not.
    """
    if machine_boundary:
        value = _widen_pre_typed_value(f, value)
    try:
        return _coerce_config_value(value, f)
    except ValueError as e:
        raise _ParseError(f"--{f.name}: {e}")


def _check_pre_typed_arg_value(
    a: Arg, value: object, *, machine_boundary: bool,
) -> object:
    """:func:`_check_pre_typed_value` for a POSITIONAL (§23.3, §24.11).

    Same machinery and the same closed set of four types -- a positional's
    declaration says what a value may be exactly as a flag's does, so the
    programmatic doors may not deliver a value it forbids, and may not turn one
    into a token by stringifying it either. Only the wrapper differs: the arg's
    own name in the prefix every arg-side value refusal already uses
    (``argument '<name>'``), never ``--<name>``.

    A variadic arg is a SEQUENCE of positionals rather than one value of a
    collection type, so an array spreads into one element per entry and each
    element is checked on its own -- and anything else is the single element it
    looks like, which is the one positional a command line would have typed.
    """
    if a.variadic:
        items = value if isinstance(value, list) else [value]
        return [
            _check_pre_typed_arg_element(
                a, item, machine_boundary=machine_boundary,
            )
            for item in items
        ]
    return _check_pre_typed_arg_element(
        a, value, machine_boundary=machine_boundary,
    )


def _check_pre_typed_arg_element(
    a: Arg, value: object, *, machine_boundary: bool,
) -> object:
    """One pre-typed positional value against the type its arg declares.

    A positional is a declaration exactly as a flag is, so the machine
    boundary's number accommodation reaches it too (§24.11 item 247).
    """
    if machine_boundary:
        value = _widen_integral_number(value, a.type)
    try:
        return _coerce_config_scalar(value, a.type)
    except ValueError as e:
        raise _ParseError(f"argument '{a.name}': {e}")


def _coerce_scoped_value(
    f: Flag, raw: object, stdin_consumed_by: list,
) -> object:
    """Coerce one supplied scoped value, with the root surface's own errors."""
    if isinstance(raw, bool):
        return raw
    text = str(raw)
    if f.type is int:
        try:
            return _strict_int(text)
        except ValueError as e:
            raise _ParseError(f"--{f.name}: {e}")
    if f.type is float:
        try:
            return _strict_float(text)
        except ValueError as e:
            raise _float_parse_error(f.name, text, e)
    resolved, stdin_consumed_by[0] = _resolve_at_prefix(
        f.name, text, stdin_consumed_by[0],
    )
    return resolved


def _check_scoped_config_conflict(
    f: Flag,
    cli_value: object,
    *,
    config_data: dict | None,
    hermetic: bool,
    pre_typed: bool,
    conflict_mode: str,
) -> None:
    """The both-sources conflict check, applied inside a live scope."""
    if pre_typed or hermetic or not config_data:
        return
    effective = (
        f.conflict_mode
        if not isinstance(f.conflict_mode, _MissingSentinel)
        else conflict_mode
    )
    if effective != "error":
        return
    key = _flag_param_name(f.name)
    if key not in config_data:
        return
    try:
        coerced = _coerce_config_value(config_data[key], f)
    except ValueError as e:
        raise _ParseError(f"--{f.name}: config value error: {e}")
    if not _values_equal_for_conflict(cli_value, coerced, f):
        raise _ParseError(
            f"flag '{f.name}' set in both cli and config; remove one"
        )


def _resolve_scoped_value(
    f: Flag,
    occs: list[_Occ],
    *,
    config_data: dict | None,
    hermetic: bool,
    pre_typed: bool,
    stdin_consumed_by: list,
    conflict_mode: str = "cli-wins",
    cli_values: dict[str, object] | None = None,
) -> tuple[object, str] | None:
    """The VALUE phase for one scoped flag, or None when nothing supplied it.

    Split from the presence phase because §24.3 pins the two as separate
    phases: every value inside the live scopes is interpreted before any
    missing required flag is reported, so `--via email --retries abc` names
    the integer that will not parse rather than the subject it never reached.

    ``cli_values`` is the argv path's own coercion sweep, already run in
    command-line order across root and scoped occurrences alike (§24.3,
    §18.28 item 262): where it is supplied, a supplied token's value was
    coerced there and this function only decides what the AMBIENT sources say
    about the flags no token named. The programmatic doors pass nothing and
    coerce here, in the command's declaration order (§18.25 item 249).
    """
    if cli_values is not None:
        if f.name in cli_values:
            value = cli_values[f.name]
            _check_scoped_config_conflict(
                f, value, config_data=config_data, hermetic=hermetic,
                pre_typed=pre_typed, conflict_mode=conflict_mode,
            )
            return value, "cli"
        hits = []
    else:
        hits = [o for o in occs if o.name == f.name]
    if hits:
        if pre_typed:
            # A scoped value is only ever pre-typed at the FLAT machine
            # boundary: the record front door reads a record's fields through
            # its own walk and never reaches this machinery. So `pre_typed`
            # here IS the machine boundary, and the number accommodation it
            # carries applies (§24.11 item 247).
            value = _check_pre_typed_value(f, hits[-1].raw, machine_boundary=True)
        elif f.compound == "dict":
            store: dict = {}
            for o in hits:
                _store_dict_flag(f, str(o.raw), store)
            value = store[f.name]
        elif f.repeatable:
            value = []
            for o in hits:
                coerced = _coerce_scoped_value(f, o.raw, stdin_consumed_by)
                if f.unique and coerced in value:
                    raise _ParseError(
                        f"--{f.name}: duplicate value "
                        f"'{_format_value_for_error(coerced)}'"
                    )
                value.append(coerced)
        else:
            value = _coerce_scoped_value(f, hits[-1].raw, stdin_consumed_by)
        _check_scoped_config_conflict(
            f, value, config_data=config_data, hermetic=hermetic,
            pre_typed=pre_typed, conflict_mode=conflict_mode,
        )
        return value, "cli"
    # An ambient binding is consulted exactly when its scope is elected, which
    # is the only path that reaches this function (§24.6).
    if not pre_typed and not hermetic and f.env is not None:
        env_val = os.environ.get(f.env)
        if env_val is not None:
            return (
                _resolve_flag_env_value(f, env_val, stdin_consumed_by),
                "env",
            )
    if not pre_typed and not hermetic and config_data:
        key = _flag_param_name(f.name)
        if key in config_data:
            try:
                coerced = _coerce_config_value(config_data[key], f)
            except ValueError as e:
                raise _ParseError(f"--{f.name}: config value error: {e}")
            return coerced, "config"
    return None


def _check_scoped_value(
    f: Flag, value: object, source: str,
) -> tuple[object, str]:
    """Choices and ``validate``, applied to a SUPPLIED scoped value (§24.3).

    Which source supplied it never enters into it: an env or config binding
    inside an elected scope is a supplied value, so the declaration's closed
    set and its callback both apply, exactly as they do on the root surface
    (steps 5.5 and 5.6). Only a declared default escapes ``validate``.
    """
    _validate_choices(f.name, value, f.repeatable, f.choices, f.retired_choices)
    if f.validate is not None and value is not None:
        for v in (value if f.repeatable else [value]):
            try:
                f.validate(v)
            except ValueError as e:
                raise _ParseError(f"--{f.name}: {e}")
    return value, source


def _apply_scoped_presence(
    f: Flag, *, scope_suffix: str, origin_suffix: str,
    infra_roots: dict[str, str] | None = None,
) -> tuple[object, str]:
    """The PRESENCE phase for one scoped flag no value phase supplied.

    A scope is not a second declaration language (§24.3): a `RelativeToRoot`
    default resolves through the declared infrastructure roots and reports
    source ``infra`` inside a scope exactly as it does on the root surface, so
    a handler reads a path rather than a marker it would have to resolve
    itself.
    """
    if f.presence == _PRESENCE_DEFAULT:
        if isinstance(f.default, RelativeToRoot):
            try:
                resolved = _resolve_infra_root_path(f.default, infra_roots or {})
            except ValueError as e:
                # The marker's own sentence, authored where it is raised, plus
                # §12.13's suffixes. Registration never sees inside a scope, so
                # this is where an undeclared root is reported.
                raise _ParseError(str(e) + scope_suffix + origin_suffix)
            return resolved, "infra"
        if f.compound == "dict":
            return dict(f.default), "default"
        if f.repeatable:
            return list(f.default), "default"
        return f.default, "default"
    if f.presence == _PRESENCE_OPTIONAL:
        return None, "default"
    # A scope is not a second declaration language (§24.3): the sentence is
    # whatever the ROOT path's presence resolution says for this declaration --
    # a required bool names its two tokens inside a scope exactly as it does at
    # root -- and §12.13's suffixes follow that complete sentence, in the order
    # item 239 closed (§18.24 item 245).
    try:
        _raise_flag_required(f)
    except _ParseError as e:
        raise _ParseError(str(e) + scope_suffix + origin_suffix) from None


def _coerce_member_payload(
    spec: _ChoiceSpec,
    occs: list[_Occ],
    *,
    config_data: dict | None,
    hermetic: bool,
    pre_typed: bool,
    stdin_consumed_by: list,
    conflict_mode: str,
    cli_values: dict[str, object] | None = None,
) -> object:
    """The value phase for one elected member's payload (§24.4, §24.7).

    A member flag's own presence is `required`, read as required once this
    member is elected (§24.4). On the command line the token and its value are
    one occurrence, so an elected member always carries one; at the flat
    machine boundary the selector's own property can elect a member whose
    payload property is absent, and that is the flat reading of `--profile`
    with nothing after it -- refused with the command line's own sentence
    rather than delivered as a silent None (§24.11).

    ``cli_values`` carries the argv path's own coercion sweep, exactly as it
    does for an ordinary scoped flag: a payload is one more occurrence, and it
    takes its position in command-line order with the rest (§24.3).
    """
    if cli_values is not None:
        if spec.name not in cli_values:
            raise _ParseError(f"flag '--{spec.name}' requires a value")
        value = cli_values[spec.name]
    else:
        hits = [o for o in occs if o.name == spec.name]
        raw = hits[-1].raw if hits else _MISSING
        if raw is _MISSING:
            raise _ParseError(f"flag '--{spec.name}' requires a value")
        value = (
            # Pre-typed here is the flat machine boundary, as it is for every
            # other scoped value (§24.11 item 247).
            _check_pre_typed_value(spec.payload, raw, machine_boundary=True)
            if pre_typed
            else _coerce_scoped_value(spec.payload, raw, stdin_consumed_by)
        )
    # §21.3's `config_conflict_mode="error"` carve-out survives untouched
    # on member flags (§21's box, item 119): it is a value-hygiene check
    # about the operator's own configuration, and it runs even where the
    # config value itself is never consulted.
    _check_scoped_config_conflict(
        spec.payload, value,
        config_data=config_data, hermetic=hermetic,
        pre_typed=pre_typed, conflict_mode=conflict_mode,
    )
    return value


def _run_scope_value_phase(
    members: tuple,
    path: tuple,
    occs: list[_Occ],
    state: _ElectionState,
    values: dict,
    *,
    config_data: dict | None,
    hermetic: bool,
    pre_typed: bool,
    stdin_consumed_by: list,
    conflict_mode: str,
    cli_values: dict[str, object] | None = None,
    deferred_checks: list | None = None,
) -> None:
    """Interpret every value inside the live scopes, at every depth (§24.3).

    The value phase runs to completion before the presence phase begins, so a
    token that will not coerce is reported wherever it sits in the scope tree,
    ahead of any required flag that was never supplied. Results land in
    ``values`` keyed by (scope path, flag name) and the presence pass reads
    them from there: coercing twice would consume stdin twice on an @-prefix.

    ``deferred_checks``, when supplied, collects (flag, key) for the closed-set
    and ``validate`` checks instead of running them here. That is the argv
    path's shape and only its: §18.20 item 226 pins that every coercion failure
    outranks every ``validate`` refusal, and a check run beside its own
    coercion would let a scoped callback fire ahead of a later token's coercion.
    The programmatic doors pass nothing and keep item 249's declaration-ordered
    sweep, where each declaration is read to completion in turn.
    """
    for m in members:
        if isinstance(m, Flag):
            resolved = _resolve_scoped_value(
                m, occs, config_data=config_data, hermetic=hermetic,
                pre_typed=pre_typed, stdin_consumed_by=stdin_consumed_by,
                conflict_mode=conflict_mode, cli_values=cli_values,
            )
            if resolved is not None:
                key = (path, m.name)
                if deferred_checks is None:
                    values[key] = _check_scoped_value(m, *resolved)
                else:
                    values[key] = resolved
                    deferred_checks.append((m, key))
            continue
        key = _sel_key(path, m.name)
        elected = state.elected.get(key)
        if elected is None or key in state.from_default:
            # An unelected selector is the presence phase's business, and a
            # defaulted selection was decided by the declaration (§24.5).
            continue
        child = path + ((m.name, elected.name),)
        if elected.payload is not None:
            values[(child, elected.name)] = (
                _coerce_member_payload(
                    elected, occs, config_data=config_data, hermetic=hermetic,
                    pre_typed=pre_typed, stdin_consumed_by=stdin_consumed_by,
                    conflict_mode=conflict_mode, cli_values=cli_values,
                ),
                "cli",
            )
        _run_scope_value_phase(
            elected.members, child, occs, state, values,
            config_data=config_data, hermetic=hermetic, pre_typed=pre_typed,
            stdin_consumed_by=stdin_consumed_by, conflict_mode=conflict_mode,
            cli_values=cli_values, deferred_checks=deferred_checks,
        )


def _instantiate_choice(
    sel: _Selector,
    spec: _ChoiceSpec,
    path: tuple,
    occs: list[_Occ],
    state: _ElectionState,
    values: dict,
    *,
    config_data: dict | None,
    hermetic: bool,
    pre_typed: bool,
    stdin_consumed_by: list,
    member_spelled: dict[str, bool],
    conflict_mode: str = "cli-wins",
    infra_roots: dict[str, str] | None = None,
) -> object:
    """Build one elected choice's record: the tag plus that choice's fields."""
    record_values: dict[str, object] = {}
    sources: dict[str, str] = {}
    if spec.payload is not None:
        payload_value, payload_source = values[(path, spec.name)]
        record_values[_SCOPE_RESERVED_VALUE] = payload_value
        sources[_SCOPE_RESERVED_VALUE] = payload_source
    inner = _build_scope_values(
        spec.members, path, occs, state, values,
        config_data=config_data, hermetic=hermetic, pre_typed=pre_typed,
        stdin_consumed_by=stdin_consumed_by, member_spelled=member_spelled,
        conflict_mode=conflict_mode, infra_roots=infra_roots,
    )
    record_values.update(inner.values)
    sources.update(inner.sources)
    record = spec.cls(**record_values)
    object.__setattr__(record, _RECORD_SOURCES_ATTR, sources)
    return record


def _resolve_declared_marker(
    ref: RelativeToRoot,
    path: tuple,
    *,
    member_spelled: dict[str, bool],
    infra_roots: dict[str, str] | None,
    origin_suffix: str = "",
) -> str:
    """One `RelativeToRoot` default declared inside a scope, resolved (§24.6).

    The marker's own sentence plus §12.13's two suffixes, in the order item 239
    closed them: registration never sees inside a scope, so an undeclared root
    is reported where the default is applied, naming the scope the declaration
    lives in and -- when the operator's own tokens do not name the election
    that reached it -- what elected that scope.
    """
    try:
        return _resolve_infra_root_path(ref, infra_roots or {})
    except ValueError as e:
        suffix = _msg_scope_suffix(_render_scope_path(path, member_spelled))
        raise _ParseError(str(e) + suffix + origin_suffix) from None


def _declared_default_record(
    sel: "_Selector",
    instance: object,
    parent_path: tuple,
    *,
    member_spelled: dict[str, bool],
    infra_roots: dict[str, str] | None,
    state: "_ElectionState | None" = None,
) -> object:
    """A defaulted selection is complete and delivered as declared (§24.5).

    Electing a choice on the command line never borrows the default's values,
    and a default is never rebuilt from the invocation: it is one complete
    selection, and every field of it was decided by the declaration.

    "Decided by the declaration" is exactly what makes a `RelativeToRoot` field
    of one the framework's job: the marker is the declaration's spelling of
    "resolve this one through the declared infrastructure roots", so it is
    resolved HERE, at delivery, labelled `infra` -- at every door, argv
    included -- rather than handed to a handler that would have to resolve it
    itself (§24.6, §18.23 item 237). The rule reaches every depth: a selection
    defaulted inside a defaulted selection is the same fact one level down.

    A COMPOUND default is copied for the same reason it is copied everywhere
    else (§24.5): the declaration is read again by every later run of the
    process, so a handler that appends to the list it was handed must not be
    able to reach it.

    The declared instance IS the declaration, so it is never rewritten. When a
    field has to be resolved or copied (at any depth), the delivered record is
    a new one built from the same class; when none does, the declaration's own
    object is delivered, as it always was.

    A refusal raised down here says WHAT elected the scope it names, because
    nothing the operator typed did: `(elected by default)` follows the scope
    suffix in §12.13's order (item 239), and an ambient election further out
    outranks it -- an env or config binding that reached this selection is the
    cause a reader cannot see in their own command line (§24.6). ``state`` is
    the election phase's record of that, and the doors that have no election
    phase have no ambient source either, so the selection is the declaration's
    there by construction.
    """
    spec = sel.choice_by_class(type(instance))
    if spec is None:  # pragma: no cover - registration proves the default's class
        return instance
    path = parent_path + ((sel.name, spec.name),)
    origin_suffix = _msg_election_origin_suffix(
        _path_origin(path, state) if state is not None
        else _MSG_ELECTION_ORIGIN_DEFAULT
    )
    values: dict[str, object] = {}
    sources: dict[str, str] = {}
    rebuilt = False
    if spec.payload is not None:
        values[_SCOPE_RESERVED_VALUE] = getattr(instance, _SCOPE_RESERVED_VALUE)
        sources[_SCOPE_RESERVED_VALUE] = "default"
    for m in spec.members:
        key = _flag_param_name(m.name)
        raw = getattr(instance, key)
        if isinstance(m, Flag):
            sources[key] = "default"
            if isinstance(raw, RelativeToRoot):
                values[key] = _resolve_declared_marker(
                    raw, path, member_spelled=member_spelled,
                    infra_roots=infra_roots, origin_suffix=origin_suffix,
                )
                sources[key] = "infra"
                rebuilt = True
                continue
            if m.compound == "dict" and isinstance(raw, dict):
                values[key] = dict(raw)
                rebuilt = True
                continue
            if m.repeatable and isinstance(raw, list):
                values[key] = list(raw)
                rebuilt = True
                continue
            values[key] = raw
            continue
        nested = _declared_default_record(
            m, raw, path, member_spelled=member_spelled,
            infra_roots=infra_roots, state=state,
        )
        values[key] = nested
        sources[key] = "default"
        rebuilt = rebuilt or nested is not raw
    if not rebuilt:
        if getattr(instance, _RECORD_SOURCES_ATTR, None) is None:
            object.__setattr__(instance, _RECORD_SOURCES_ATTR, sources)
        return instance
    record = spec.cls(**values)
    object.__setattr__(record, _RECORD_SOURCES_ATTR, sources)
    return record


def _build_scope_values(
    members: tuple,
    path: tuple,
    occs: list[_Occ],
    state: _ElectionState,
    values: dict,
    *,
    config_data: dict | None,
    hermetic: bool,
    pre_typed: bool,
    stdin_consumed_by: list,
    member_spelled: dict[str, bool],
    conflict_mode: str = "cli-wins",
    infra_roots: dict[str, str] | None = None,
) -> _SelectorResult:
    """The PRESENCE phase, building the records the value phase filled (§24.3).

    ``values`` is what :func:`_run_scope_value_phase` produced; a flag missing
    from it is one no source supplied, which is where presence decides.
    """
    result = _SelectorResult()
    path_text = _render_scope_path(path, member_spelled)
    scope_suffix = _msg_scope_suffix(path_text)
    origin_suffix = _msg_election_origin_suffix(_path_origin(path, state))
    # §12.13's two suffixes are one composed thing and travel together in this
    # order: the scope names WHERE the requirement lives, the origin names WHAT
    # caused it, and a clause that follows them is a note appended to a
    # complete sentence rather than an aside spliced into the middle of one.
    presence_suffix = scope_suffix + origin_suffix
    for m in members:
        if isinstance(m, Flag):
            resolved = values.get((path, m.name))
            if resolved is None:
                resolved = _apply_scoped_presence(
                    m, scope_suffix=scope_suffix, origin_suffix=origin_suffix,
                    infra_roots=infra_roots,
                )
            value, source = resolved
            result.values[_flag_param_name(m.name)] = value
            result.sources[_flag_param_name(m.name)] = source
            continue
        key = _sel_key(path, m.name)
        elected = state.elected.get(key)
        param = _flag_param_name(m.name)
        if elected is None:
            if m.presence != _PRESENCE_REQUIRED:  # pragma: no cover - defensive
                raise AssertionError("a selector declares required or a default")
            if m.is_member_spelled:
                names = ", ".join(f"--{c.name}" for c in m.choices)
                declined = state.declined.get(key, [])
                clause = (
                    _msg_mutex_decline_clause(declined[0]) if declined else ""
                )
                # The decline clause is a note about the token that WAS typed,
                # so it follows both suffixes (§21.4 appended to §12.13).
                raise _ParseError(
                    f"one of {names} is required{presence_suffix}{clause}"
                )
            raise _ParseError(
                f"flag '--{m.name}' is required{scope_suffix}{origin_suffix}"
            )
        if key in state.from_default:
            result.values[param] = _declared_default_record(
                m, m.default, path, member_spelled=member_spelled,
                infra_roots=infra_roots, state=state,
            )
            result.sources[param] = "default"
            continue
        result.values[param] = _instantiate_choice(
            m, elected, path + ((m.name, elected.name),), occs, state, values,
            config_data=config_data, hermetic=hermetic, pre_typed=pre_typed,
            stdin_consumed_by=stdin_consumed_by, member_spelled=member_spelled,
            conflict_mode=conflict_mode, infra_roots=infra_roots,
        )
        origin = state.origin.get(key, "")
        result.sources[param] = (
            "env" if origin.startswith(" from env var")
            else "config" if origin.startswith(" from config key")
            else "cli"
        )
    return result


def _elect_and_validate_scopes(
    cmd: Command,
    occs: list[_Occ],
    *,
    config_data: dict | None = None,
    hermetic: bool = False,
    pre_typed: bool = False,
) -> _ElectionState:
    """Phases 1-3: elect, bind the reused shorts, validate scope membership.

    The STRUCTURAL half of selector resolution, split out so a caller can run
    it before any value is coerced anywhere on the command line. Parse-problem
    precedence is normative for every command, not only for one that declares
    a selector: an unknown flag, an unknown choice and a scope violation are
    all facts about the command line's SHAPE, and shape is decided before any
    token's text is interpreted as a value.
    """
    state = _ElectionState()
    _elect_all(
        cmd.selectors, (), occs, state,
        config_data=config_data, hermetic=hermetic, pre_typed=pre_typed,
    )
    _resolve_short_occurrences(cmd, occs, state)
    _validate_scopes(cmd, occs, state)
    return state


def _resolve_selectors(
    cmd: Command,
    occs: list[_Occ],
    *,
    config_data: dict | None = None,
    hermetic: bool = False,
    pre_typed: bool = False,
    stdin_consumed_by: list | None = None,
    conflict_mode: str = "cli-wins",
    state: _ElectionState | None = None,
    infra_roots: dict[str, str] | None = None,
    cli_values: dict[str, object] | None = None,
) -> _SelectorResult:
    """Run all four phases over a command's selectors, on the ARGV path.

    ``state`` carries the result of phases 1-3 when a caller has already run
    them (:func:`_elect_and_validate_scopes`); passing it never re-runs them.
    ``cli_values`` carries what the command line's own coercion sweep produced
    for the scoped tokens, already interleaved with the root ones in
    command-line order (§24.3).
    """
    if stdin_consumed_by is None:
        stdin_consumed_by = [None]
    member_spelled = _member_spelling_map(cmd.selectors)
    if state is None:
        state = _elect_and_validate_scopes(
            cmd, occs,
            config_data=config_data, hermetic=hermetic, pre_typed=pre_typed,
        )
    # §24.3's phase order, spelled as two passes: every value inside the live
    # scopes is interpreted first, and only then does presence decide.
    values: dict = {}
    deferred_checks: list = []
    _run_scope_value_phase(
        cmd.selectors, (), occs, state, values,
        config_data=config_data, hermetic=hermetic, pre_typed=pre_typed,
        stdin_consumed_by=stdin_consumed_by, conflict_mode=conflict_mode,
        cli_values=cli_values, deferred_checks=deferred_checks,
    )
    # The closed set and `validate`, once nothing is left to coerce: a
    # coercion failure anywhere on the command line outranks every callback
    # refusal (§18.20 item 226), scoped callbacks included.
    for f, key in deferred_checks:
        values[key] = _check_scoped_value(f, *values[key])
    result = _build_scope_values(
        cmd.selectors, (), occs, state, values,
        config_data=config_data, hermetic=hermetic, pre_typed=pre_typed,
        stdin_consumed_by=stdin_consumed_by, member_spelled=member_spelled,
        conflict_mode=conflict_mode, infra_roots=infra_roots,
    )
    if not pre_typed and not hermetic:
        _report_skipped_bindings(
            cmd.selectors, (), state, result.diagnostics,
            config_data=config_data, member_spelled=member_spelled,
        )
    return result


def _flat_selector_params(cmd: Command) -> set[str]:
    """Every property name a selector contributes to the MCP projection.

    The selector's own name, plus every scoped flag at every depth -- which is
    exactly the flattening §24.11 pins, and the set the flat form must be
    stripped of before the ordinary kwargs machinery sees it.
    """
    names: set[str] = {_flag_param_name(s.name) for s in cmd.selectors}
    for group in cmd.sites.values():
        for site in group:
            names.add(_flag_param_name(site.name))
    return names


def _flat_occurrences(cmd: Command, arguments: dict) -> list[_Occ]:
    """Convert the flat machine form into occurrences (§24.11).

    A member-spelled selector projects IDENTICALLY to a token-spelled one:
    tokenization is a command-line fact and there are no tokens at this
    boundary, so a member's payload flattens under the member's own name.

    Supplying a member's payload key IS electing that member, exactly as
    typing `--profile work` is: the key becomes its OWN occurrence, so every
    member the arguments name reaches phase 1 together. A second member beside
    an election is then the ordinary double election of §21.4, refused with the
    parser's own sentence instead of being silently discarded -- and a payload
    key with no selector key elects its member, which is the flat reading of a
    command line that never types the selector's name.

    A PAYLOAD-LESS member's own key is that member's token too: `true` elects
    it as `--<name>` does and an explicit `false` DECLINES it as `--no-<name>`
    does. Ignoring the key would make the flat form a second election
    vocabulary rather than the command line with its tokens removed, and would
    discard a whole scope in silence. A decline never overrides the selector
    property naming the same member: that property IS an election, and a JSON
    object has no order for a later key to win by.
    """
    occs: list[_Occ] = []
    seen_selectors: set[int] = set()

    def add_selector(sel: _Selector) -> None:
        if id(sel) in seen_selectors:
            return
        seen_selectors.add(id(sel))
        param = _flag_param_name(sel.name)
        if not sel.is_member_spelled:
            if param in arguments:
                occs.append(_Occ(sel.name, arguments[param], f"--{sel.name}"))
            return
        elected: list = []
        declined: list = []
        if param in arguments:
            value = arguments[param]
            spec = sel.choice_by_name(str(value))
            if spec is None:
                names = ", ".join(c.name for c in sel.choices)
                raise _ParseError(
                    f"--{sel.name}: invalid value '{value}', "
                    f"must be one of: {names}"
                )
            elected.append(spec)
        for c in sel.choices:
            if any(e is c for e in elected):
                continue
            key = _flag_param_name(c.name)
            if key not in arguments:
                continue
            if c.payload is None and arguments[key] is False:
                declined.append(c)
                continue
            elected.append(c)
        for spec in elected:
            if spec.payload is None:
                payload: object = True
            else:
                # The election is carried by the occurrence whether or not the
                # payload property came with it, because the selector's own
                # property elects too. `_MISSING` is what the value phase reads
                # to tell "elected, nothing supplied" from a supplied null.
                payload = arguments.get(_flag_param_name(spec.name), _MISSING)
            occs.append(_Occ(spec.name, payload, f"--{spec.name}"))
        for spec in declined:
            occs.append(_Occ(spec.name, False, f"--no-{spec.name}"))

    for sel in cmd.selectors:
        add_selector(sel)
    for group in cmd.sites.values():
        for site in group:
            if site.kind == "selector":
                add_selector(site.decl)
            elif site.kind == "member":
                add_selector(site.decl)
    for group in cmd.sites.values():
        site = group[0]
        if site.kind != "flag":
            continue
        param = _flag_param_name(site.name)
        if param in arguments:
            occs.append(_Occ(site.name, arguments[param], f"--{site.name}"))
    return occs


class _RecordSelectorDoor:
    """`call()` takes the elected record, pre-typed (§24.11).

    The phase order is the PARSER's, so it governs this door too (§24.3,
    §18.24 item 243): every record's shape is decided over the whole command
    before any value is read, the value sweep then runs in DECLARATION order at
    every depth, and presence is last. A record IS its election here -- the
    class is the tag -- and `@choice`'s frozen dataclass refuses a missing
    field and an unknown field at construction, so the scope and presence
    problems a flat object can spell inside a record cannot be reached at all.

    *Pre-typed* means ALREADY OF THE DECLARED TYPE, never exempt from the
    declaration (§24.11 item 240): a record's fields ARE the scope's flags, so
    every value one carries is checked against the declaration it was supplied
    against, exactly as the flat door checks the same value under the same
    declaration.

    The door is split into its three stages rather than run end to end, because
    the value stage is ONE declaration-ordered sweep the command's own flags
    take part in (§18.25 item 249): :meth:`read` is called where the selector
    stands among the command's declarations, and :meth:`presence` runs once
    every value in the call has been read.
    """

    def __init__(
        self, cmd: Command, kwargs: dict[str, object],
        *, infra_roots: dict[str, str] | None,
    ) -> None:
        self._cmd = cmd
        self._infra_roots = infra_roots
        self._member_spelled = _member_spelling_map(cmd.selectors)
        self._built: dict[str, object] = {}
        # The keys the door itself consumes: a selector's own parameter, which
        # the command's flag sweep must not read again.
        self.consumed = {_flag_param_name(s.name) for s in cmd.selectors}
        # Stage 1 -- SHAPE, over the whole command and to unlimited depth,
        # before any value is read anywhere.
        self._supplied: list[tuple[str, _Selector, _ChoiceSpec, object]] = []
        for sel in cmd.selectors:
            param = _flag_param_name(sel.name)
            if param not in kwargs:
                continue
            record = kwargs[param]
            spec = _record_choice_spec(cmd.name, sel, record)
            _walk_record_shape(cmd.name, spec, record)
            self._supplied.append((param, sel, spec, record))

    def read(self, sel: _Selector) -> None:
        """Stage 2 for ONE selector: its record's values, at every depth."""
        for param, supplied_sel, spec, record in self._supplied:
            if supplied_sel is not sel:
                continue
            self._built[param] = _record_from_caller(
                self._cmd.name, sel, spec, record, ((sel.name, spec.name),),
                member_spelled=self._member_spelled,
                infra_roots=self._infra_roots,
            )

    def presence(self) -> _SelectorResult:
        """Stage 3 -- what is left once nothing above refused."""
        result = _SelectorResult()
        for sel in self._cmd.selectors:
            param = _flag_param_name(sel.name)
            if param in self._built:
                result.values[param] = self._built[param]
                result.sources[param] = "cli"
                continue
            if sel.presence == _PRESENCE_DEFAULT:
                result.values[param] = _declared_default_record(
                    sel, sel.default, (),
                    member_spelled=self._member_spelled,
                    infra_roots=self._infra_roots,
                )
                result.sources[param] = "default"
                continue
            if sel.is_member_spelled:
                names = ", ".join(f"--{c.name}" for c in sel.choices)
                raise _ParseError(f"one of {names} is required")
            raise _ParseError(f"flag '--{sel.name}' is required")
        return result


class _FlatSelectorDoor:
    """The flat machine form's selector resolution, split into its stages.

    The flat object is converted into the SAME occurrences the argv path
    produces and run through the same four phases (§24.11), with the value
    phase split out so it can take its position in the command's own
    declaration-ordered sweep (§18.25 item 249): election and scope are settled
    command-wide in the constructor, :meth:`read` interprets one selector's
    scoped values where that selector is declared, and :meth:`presence` builds
    every record once no value is left to read.
    """

    def __init__(
        self, cmd: Command, kwargs: dict[str, object],
        *, infra_roots: dict[str, str] | None,
    ) -> None:
        self._cmd = cmd
        self._infra_roots = infra_roots
        self._occs = _flat_occurrences(cmd, kwargs)
        self._member_spelled = _member_spelling_map(cmd.selectors)
        self._values: dict = {}
        # Nothing on this door can consume stdin: every value is pre-typed, so
        # no @-prefix is ever resolved. The list is the shared machinery's.
        self._stdin_consumed_by: list = [None]
        # Every property name a selector contributes to the flat schema -- the
        # selector's own name plus every scoped name at every depth.
        self.consumed = _flat_selector_params(cmd)
        # Stages 1-3 -- ELECTION and SCOPE, command-wide, before any value.
        self._state = _elect_and_validate_scopes(cmd, self._occs, pre_typed=True)

    def read(self, sel: _Selector) -> None:
        """The VALUE phase for ONE selector's live scopes, at every depth."""
        _run_scope_value_phase(
            (sel,), (), self._occs, self._state, self._values,
            config_data=None, hermetic=False, pre_typed=True,
            stdin_consumed_by=self._stdin_consumed_by,
            conflict_mode="cli-wins",
        )

    def presence(self) -> _SelectorResult:
        """The PRESENCE phase, building the records the value phase filled."""
        return _build_scope_values(
            self._cmd.selectors, (), self._occs, self._state, self._values,
            config_data=None, hermetic=False, pre_typed=True,
            stdin_consumed_by=self._stdin_consumed_by,
            member_spelled=self._member_spelled, conflict_mode="cli-wins",
            infra_roots=self._infra_roots,
        )


def _record_choice_spec(
    cmd_name: str, sel: _Selector, value: object,
) -> _ChoiceSpec:
    """The choice a supplied record elects, or the shape refusal (§24.11).

    A record is its own election at this door, so a value that is not an
    instance of a declared choice carries no election to read: it is a fact
    about the record's SHAPE, and the same fact wherever it sits -- a
    selector's own parameter or a field bound to a nested selector.
    """
    spec = sel.choice_by_class(type(value))
    if spec is None:
        _raise_invoke_selector_not_record(cmd_name, sel, value)
    return spec


def _is_declared_selection_default(sel: _Selector, value: object) -> bool:
    """Is this field holding the very selection its declaration wrote?

    A defaulted selection is COMPLETE by registration (§24.5) and is delivered
    as declared, so the walk stops at it: the caller supplied no election here,
    exactly as a command line that never typed the selector's token.
    """
    return sel.presence == _PRESENCE_DEFAULT and value is sel.default


def _walk_record_shape(
    cmd_name: str, spec: _ChoiceSpec, record: object,
) -> None:
    """Phase 1 over one supplied record: every nested selection it carries.

    Declaration order, depth-first, and nothing else is looked at -- a value
    problem beside a nested field that holds no record is a later phase's, and
    the phases decide over the whole command rather than per selector.
    """
    for m in spec.members:
        if isinstance(m, Flag):
            continue
        value = getattr(record, _flag_param_name(m.name))
        if _is_declared_selection_default(m, value):
            continue
        _walk_record_shape(
            cmd_name, _record_choice_spec(cmd_name, m, value), value,
        )


def _record_field_value(
    f: Flag,
    raw: object,
    path: tuple,
    *,
    member_spelled: dict[str, bool],
    infra_roots: dict[str, str] | None,
) -> tuple[object, str]:
    """One field of a supplied record, against the flag it declares (§24.11).

    A scope is not a second declaration language (§24.3), so the two spellings
    a complete record uses for "the declaration decides this one" resolve the
    way every other door resolves them: a `RelativeToRoot` default reaches the
    handler as the resolved path labelled `infra` (§18.23 item 237), and an
    optional field -- which has no dataclass default, so the caller writes the
    `None` an absent key says at the flat door -- delivers absence.

    Everything else is a value the caller supplied, and is checked against the
    type its declaration names by the machinery every pre-typed value goes
    through.
    """
    if f.presence == _PRESENCE_DEFAULT and isinstance(raw, RelativeToRoot):
        return _resolve_declared_marker(
            raw, path, member_spelled=member_spelled, infra_roots=infra_roots,
        ), "infra"
    if f.presence == _PRESENCE_OPTIONAL and raw is None:
        return None, "default"
    # The RECORD door takes Python's own values, so Python's own numeric model
    # governs them: an integral float is a float here (§24.11 item 247).
    return _check_pre_typed_value(f, raw, machine_boundary=False), "default"


def _record_from_caller(
    cmd_name: str,
    sel: _Selector,
    spec: _ChoiceSpec,
    record: object,
    path: tuple,
    *,
    member_spelled: dict[str, bool],
    infra_roots: dict[str, str] | None,
) -> object:
    """Phase 2 over one supplied record: its values, in declaration order.

    The payload first and then the scope's flags as they are declared, which is
    the order :func:`_instantiate_choice` builds the same record in from argv --
    a keyword-argument list has no order of its own, so the order the caller
    happened to write it in decides nothing.

    A new record is built rather than the caller's own object rewritten: a
    checked value can differ from the one supplied (an integer widens to the
    float its declaration names), and the object the caller built is theirs.
    """
    values: dict[str, object] = {}
    sources: dict[str, str] = {}
    if spec.payload is not None:
        values[_SCOPE_RESERVED_VALUE] = _check_pre_typed_value(
            spec.payload, getattr(record, _SCOPE_RESERVED_VALUE),
            machine_boundary=False,
        )
        # Which label a supplied field earns is not this round's question: the
        # record door has reported `default` for every field it delivers since
        # the door existed, and it still does.
        sources[_SCOPE_RESERVED_VALUE] = "default"
    for m in spec.members:
        key = _flag_param_name(m.name)
        raw = getattr(record, key)
        if isinstance(m, Flag):
            values[key], sources[key] = _record_field_value(
                m, raw, path,
                member_spelled=member_spelled, infra_roots=infra_roots,
            )
            continue
        if _is_declared_selection_default(m, raw):
            values[key] = _declared_default_record(
                m, raw, path, member_spelled=member_spelled,
                infra_roots=infra_roots,
            )
            sources[key] = "default"
            continue
        # Phase 1 already proved this field holds a declared choice, so the
        # lookup is the same one again rather than a second chance to refuse.
        sub = _record_choice_spec(cmd_name, m, raw)
        values[key] = _record_from_caller(
            cmd_name, m, sub, raw, path + ((m.name, sub.name),),
            member_spelled=member_spelled, infra_roots=infra_roots,
        )
        sources[key] = "default"
    built = spec.cls(**values)
    object.__setattr__(built, _RECORD_SOURCES_ATTR, sources)
    return built


def _raise_invoke_selector_not_record(
    cmd_name: str, sel: _Selector, value: object,
):
    """Authored: `call()` was handed something that is not an elected record."""
    raise _ParseError(
        f"parameter '{_flag_param_name(sel.name)}' for command '{cmd_name}' "
        f"must be an instance of a declared choice of '--{sel.name}' "
        f"({_render_union(sel)}), got {type(value).__name__}"
    )


def _parse_command(
    cmd: Command,
    tokens: list[str],
    global_flags: list[Flag] | None = None,
    config_data: dict | None = None,
    stdin_consumed_by: list[str | None] | None = None,
    conflict_mode: str = "cli-wins",
    hermetic: bool = False,
    infra_roots: dict[str, str] | None = None,
    out_diagnostics: list[str] | None = None,
) -> tuple[Command, dict[str, object], dict[str, object], dict[str, str]]:
    """Parse tokens against a resolved command's flags and args.

    Returns (cmd, kwargs, global_cli_set, sources) where global_cli_set contains
    any global flag values parsed from tokens appearing after the command name.

    stdin_consumed_by is a mutable single-element list tracking which flag
    has already consumed stdin via @-. Updated in-place.

    When hermetic is True, env var and config resolution are skipped entirely.
    """
    if stdin_consumed_by is None:
        stdin_consumed_by = [None]

    # Build flag lookup dicts
    long_lookup: dict[str, Flag] = {}  # --flag-name -> Flag
    short_lookup: dict[str, Flag] = {}  # -x -> Flag
    negation_lookup: dict[str, Flag] = {}  # --no-flag-name -> Flag

    # The clear vocabulary's minted spelling (§27.6). It is framework-owned and
    # reaches the handler on the Context; it is NOT negatable, so
    # `--no-unset-<x>` names nothing and is refused by the ordinary
    # unknown-flag path.
    unset_lookup: dict[str, Flag] = {}  # --unset-flag-name -> Flag

    for f in cmd.flags:
        long_lookup[f"--{f.name}"] = f
        if f.short:
            short_lookup[f"-{f.short}"] = f
        if f.type is bool and f.negatable:
            negation_lookup[f"--no-{f.name}"] = f
        if f.nullable:
            unset_lookup[f"--{_unset_flag_name(f.name)}"] = f

    # Also include global flags in the lookup tables so they are recognized
    # when placed after the command name
    global_flag_names: set[str] = set()
    if global_flags:
        for f in global_flags:
            long_lookup[f"--{f.name}"] = f
            if f.short:
                short_lookup[f"-{f.short}"] = f
            if f.type is bool and f.negatable:
                negation_lookup[f"--no-{f.name}"] = f
            global_flag_names.add(f.name)

    # Track which flags were set by CLI args
    cli_set: dict[str, object] = {}  # flag.name -> value
    positionals: list[str] = []
    # Every ROOT-scope flag occurrence, recorded without interpreting its text.
    # The token scan decides SHAPE only -- which flag a token names, whether it
    # consumes the next argv element -- and every coercion happens afterwards,
    # so a structural problem is always reported before a value problem
    # whatever their order on the command line. `raw` is a str for a value
    # token and True/False for a bool-style one, and the leading int is the
    # argv index that recorded it -- the value phase sweeps root and scoped
    # occurrences in ONE command-line order, so both lists carry their position.
    root_occs: list[tuple[int, Flag, object]] = []
    # Phase 1: every scoped occurrence, collected WITHOUT interpreting any of
    # it. Whether a token consumes the next argv element is decided here, before
    # any choice is elected -- which is why sibling scopes may reuse a name only
    # with an identical value shape (§24.3).
    scoped_occs: list[_Occ] = []

    def _scoped_site(token_name: str) -> "_Site | None":
        group = cmd.sites.get(token_name)
        return group[0] if group else None

    def _consume_scoped(tok: str, idx: int) -> int | None:
        """Record one scoped occurrence, or return None if `tok` is not one."""
        name = tok[2:] if tok.startswith("--") else ""
        inline: str | None = None
        alts: tuple[str, ...] = ()
        if tok.startswith("--") and "=" in tok:
            head, inline = tok.split("=", 1)
            name = head[2:]
        elif tok.startswith("-") and len(tok) == 2:
            candidates = cmd.shorts.get(tok[1], ())
            if not candidates:
                return None
            name = candidates[0]
            alts = candidates
        elif not tok.startswith("--"):
            return None
        if name.startswith("no-"):
            target = name[3:]
            site = _scoped_site(target)
            if site is not None and site.negatable:
                if inline is not None:
                    raise _ParseError(
                        f"flag '--{name}' is a boolean negation and does not "
                        f"take a value"
                    )
                scoped_occs.append(_Occ(target, False, tok, seq=idx))
                return idx + 1
        site = _scoped_site(name)
        if site is None:
            return None
        if not site.takes_value:
            if inline is not None:
                raise _ParseError(
                    f"flag '--{name}' is a boolean flag and does not take a value"
                )
            scoped_occs.append(_Occ(name, True, tok, alts, seq=idx))
            return idx + 1
        if inline is not None:
            scoped_occs.append(_Occ(name, inline, tok, alts, seq=idx))
            return idx + 1
        if idx + 1 >= len(tokens):
            # The token AS TYPED, which is what the root-scope path and both
            # sibling implementations report: a reader who typed `-r` is told
            # about `-r`, not about the long form it resolved to.
            raise _ParseError(f"flag '{tok}' requires a value")
        scoped_occs.append(_Occ(name, tokens[idx + 1], tok, alts, seq=idx))
        return idx + 2

    def _store_value(f: Flag, value: object) -> None:
        """Store a parsed value, appending to a list for repeatable flags."""
        if f.compound == "dict":
            if f.name not in cli_set:
                cli_set[f.name] = {}
            # value is a (key, val) tuple from _parse_dict_value
            k, v = value
            if k in cli_set[f.name]:
                raise _ParseError(
                    f"--{f.name}: duplicate key '{k}'"
                )
            cli_set[f.name][k] = v
        elif f.repeatable:
            if f.name not in cli_set:
                cli_set[f.name] = []
            if f.unique and value in cli_set[f.name]:
                raise _ParseError(
                    f"--{f.name}: duplicate value "
                    f"'{_format_value_for_error(value)}'"
                )
            cli_set[f.name].append(value)
        else:
            cli_set[f.name] = value

    i = 0
    stop_flags = False  # set when -- is encountered

    while i < len(tokens):
        tok = tokens[i]

        if stop_flags or not tok.startswith("-") or tok == "-":
            positionals.append(tok)
            i += 1
            continue

        if tok == "--":
            stop_flags = True
            i += 1
            continue

        # A scoped token. Root and scoped names are disjoint by registration
        # (§24.7), so consulting the site table first cannot shadow a root flag.
        if cmd.sites:
            consumed = _consume_scoped(tok, i)
            if consumed is not None:
                i = consumed
                continue

        # --flag=value form
        if tok.startswith("--") and "=" in tok:
            eq_pos = tok.index("=")
            flag_part = tok[:eq_pos]
            value_part = tok[eq_pos + 1 :]

            if flag_part in long_lookup:
                f = long_lookup[flag_part]
                if f.type is bool and f.compound != "dict":
                    raise _ParseError(
                        f"flag '{flag_part}' is a boolean flag and does not take a value"
                    )
                root_occs.append((i, f, value_part))
            elif flag_part in negation_lookup:
                raise _ParseError(
                    f"flag '{flag_part}' is a boolean negation and does not take a value"
                )
            else:
                raise _ParseError(f"unknown flag '{flag_part}'")
            i += 1
            continue

        # --no-flag negation
        if tok in negation_lookup:
            f = negation_lookup[tok]
            root_occs.append((i, f, False))
            i += 1
            continue

        # --unset-flag: the clear vocabulary's minted spelling. It carries no
        # value of its own -- clearing is one act, not a value -- and it is
        # checked against the property's own occurrences below.
        if tok in unset_lookup:
            root_occs.append((i, unset_lookup[tok], _UNSET_OCC))
            i += 1
            continue

        # --flag (long form without =)
        if tok.startswith("--"):
            if tok in long_lookup:
                f = long_lookup[tok]
                if f.type is bool and f.compound != "dict":
                    root_occs.append((i, f, True))
                    i += 1
                else:
                    # str/int/float/dict flag: consume next token as value
                    if i + 1 < len(tokens):
                        root_occs.append((i, f, tokens[i + 1]))
                        i += 2
                    else:
                        raise _ParseError(f"flag '{tok}' requires a value")
            else:
                raise _ParseError(f"unknown flag '{tok}'")
            continue

        # -x (short form)
        if tok.startswith("-") and len(tok) == 2 and tok in short_lookup:
            f = short_lookup[tok]
            if f.type is bool and f.compound != "dict":
                root_occs.append((i, f, True))
                i += 1
            else:
                # str/int/float/dict flag: consume next token as value
                if i + 1 < len(tokens):
                    root_occs.append((i, f, tokens[i + 1]))
                    i += 2
                else:
                    raise _ParseError(f"flag '{tok}' requires a value")
            continue

        # Token starts with "-" but doesn't match any known flag;
        # treat as a positional arg (e.g. negative numbers like -7, -3.14)
        positionals.append(tok)
        i += 1

    # The structural half of selector resolution runs HERE, between the token
    # scan and the first coercion: an unknown choice and a scope violation
    # outrank a value that will not parse, whichever came first in argv.
    election_state: _ElectionState | None = None
    if cmd.selectors:
        election_state = _elect_and_validate_scopes(
            cmd, scoped_occs, config_data=config_data, hermetic=hermetic,
        )

    # Phase 4: the value pass over EVERY occurrence, root and scoped alike, in
    # COMMAND-LINE order (§24.3). One sweep, not two: partitioning root values
    # ahead of scoped ones would make which of two true refusals is printed
    # depend on a declaration the operator cannot see, which is the outcome the
    # phase order exists to prevent (§18.28 item 262; Go is the reference).
    # Only coercion happens here -- the closed set and `validate` run in a
    # later pass, so every coercion failure outranks every callback refusal
    # (§18.20 item 226).
    scoped_cli: dict[str, object] = {}
    # A property written AND cleared in one invocation is refused before either
    # is read: the two tokens state opposite things about one property, and no
    # order of application makes one of them true (§27.6).
    unsets: set[str] = {
        f.name for _, f, raw in root_occs if raw is _UNSET_OCC
    }
    if unsets:
        for _, f, raw in root_occs:
            if raw is not _UNSET_OCC and f.name in unsets:
                raise _ParseError(_msg_update_value_and_unset(f.name))
    merged = [(seq, True, (f, raw)) for seq, f, raw in root_occs]
    merged.extend((o.seq, False, o) for o in scoped_occs)
    merged.sort(key=lambda entry: entry[0])
    for _, is_root, item in merged:
        if not is_root:
            site = _live_site(cmd, election_state, item.name)
            if site is None or site.kind == "selector":
                # The election IS a selector token's value, and a scope
                # violation was already refused above.
                continue
            if site.kind == "member" and site.choice.payload is None:
                continue  # a payload-less member token elects; it carries no value
            _coerce_scoped_occurrence(site, item, scoped_cli, stdin_consumed_by)
            continue
        f, raw = item
        if raw is _UNSET_OCC:
            continue
        if raw is True or raw is False:
            cli_set[f.name] = raw
        elif f.compound == "dict":
            _store_dict_flag(f, raw, cli_set)
        elif f.type is int:
            try:
                _store_value(f, _strict_int(raw))
            except ValueError as e:
                raise _ParseError(f"--{f.name}: {e}")
        elif f.type is float:
            try:
                _store_value(f, _strict_float(raw))
            except ValueError as e:
                raise _float_parse_error(f.name, raw, e)
        else:
            resolved, stdin_consumed_by[0] = _resolve_at_prefix(
                f.name, raw, stdin_consumed_by[0],
            )
            _store_value(f, resolved)

    # A cleared property is SUPPLIED: the value it delivers is absence, the
    # same None an untouched property delivers, and the two are told apart by
    # `provided()` -- true here, because the invocation caused the write -- and
    # by `ctx.unset` (§27.6). Entering it in the store also stops env, config
    # and the declared-default step from filling the gap the clear opened.
    for _name in unsets:
        cli_set[_name] = None

    # Track which flag names are set by env vs config (for source attribution).
    env_names: set[str] = set()
    config_names: set[str] = set()

    # Step 4: resolve env vars for flags not set by CLI (skipped under --hermetic)
    for f in cmd.flags:
        if hermetic:
            break
        if f.name in cli_set:
            continue
        if f.env is not None:
            env_val = os.environ.get(f.env)
            if env_val is not None:
                cli_set[f.name] = _resolve_flag_env_value(
                    f, env_val, stdin_consumed_by,
                )
                env_names.add(f.name)

    # Step 4.2: resolve config values for flags not set by CLI or env.
    # In conflict mode "error", detect when config would set a flag
    # already set by CLI or env. (Skipped under --hermetic.)
    if config_data and not hermetic:
        for f in cmd.flags:
            param = _flag_param_name(f.name)
            if param not in config_data:
                continue
            # Effective mode: per-flag override if set, else the app default.
            effective_mode = (
                f.conflict_mode
                if not isinstance(f.conflict_mode, _MissingSentinel)
                else conflict_mode
            )
            if f.name in cli_set:
                # Flag set by CLI or env, config also has a value. This is a
                # conflict ONLY when the values diverge; identical values agree.
                if effective_mode == "error":
                    try:
                        coerced = _coerce_config_value(config_data[param], f)
                    except ValueError as e:
                        raise _ParseError(
                            f"--{f.name}: config value error: {e}"
                        )
                    if not _values_equal_for_conflict(cli_set[f.name], coerced, f):
                        existing_source = "env" if f.name in env_names else "cli"
                        raise _ParseError(
                            f"flag '{f.name}' set in both "
                            f"{existing_source} and config; remove one"
                        )
                continue  # cli-wins, or error mode with matching values
            try:
                coerced = _coerce_config_value(config_data[param], f)
            except ValueError as e:
                raise _ParseError(
                    f"--{f.name}: config value error: {e}"
                )
            if f.unique and isinstance(coerced, list):
                dup = _find_duplicate(coerced)
                if dup is not None:
                    raise _ParseError(
                        f"--{f.name}: config value error: "
                        f"duplicate value "
                        f"'{_format_value_for_error(dup)}'"
                    )
            cli_set[f.name] = coerced
            config_names.add(f.name)

    # Step 4.3: config-conflict detection for GLOBAL flags parsed AFTER the
    # command name (`tool cmd --global X`). This is CONFLICT-DETECTION ONLY:
    # config values for globals were already APPLIED during the pre-command
    # global-flag pass (_parse_global_flags), so applying them again here would
    # be a second application site -- wrong even if idempotent. We must never
    # write a config value into cli_set for a global here. Globals that reach
    # cli_set at this point are purely CLI-parsed (post-command env for globals
    # is never resolved here), so the divergence source is always "cli".
    if config_data and not hermetic and global_flags:
        for f in global_flags:
            if f.name not in cli_set:
                continue
            param = _flag_param_name(f.name)
            if param not in config_data:
                continue
            effective_mode = (
                f.conflict_mode
                if not isinstance(f.conflict_mode, _MissingSentinel)
                else conflict_mode
            )
            if effective_mode != "error":
                continue
            try:
                coerced = _coerce_config_value(config_data[param], f)
            except ValueError as e:
                raise _ParseError(f"--{f.name}: config value error: {e}")
            if not _values_equal_for_conflict(cli_set[f.name], coerced, f):
                raise _ParseError(
                    f"flag '{f.name}' set in both cli and config; remove one"
                )

    # Wrap cli_set into a _SourcedStore with proper source attribution.
    # CLI-parsed values are _Source.CLI, env-resolved values are _Source.ENV,
    # and config-resolved values are _Source.CONFIG.
    store = _SourcedStore()
    for k, v in cli_set.items():
        if k in env_names:
            store.set(k, v, _Source.ENV)
        elif k in config_names:
            store.set(k, v, _Source.CONFIG)
        else:
            store.set(k, v, _Source.CLI)

    selector_result = None
    if cmd.selectors:
        selector_result = _resolve_selectors(
            cmd, scoped_occs, config_data=config_data, hermetic=hermetic,
            stdin_consumed_by=stdin_consumed_by, conflict_mode=conflict_mode,
            state=election_state, infra_roots=infra_roots,
            cli_values=scoped_cli,
        )
        if out_diagnostics is not None:
            out_diagnostics.extend(selector_result.diagnostics)

    cmd, kwargs, global_cli_set, sources, writes = _validate_and_build_kwargs(
        cmd, store, positionals, global_flag_names, infra_roots,
        selector_result=selector_result, unsets=unsets,
    )
    return cmd, kwargs, global_cli_set, sources, writes, unsets


def _flag_param_name(flag_name: str) -> str:
    """Convert a flag name like '--dry-run' to a Python parameter name 'dry_run'.

    If the result is a Python keyword (e.g. 'global', 'class'), appends '_'
    per PEP 8 convention (e.g. 'global_', 'class_').
    """
    name = flag_name.lstrip("-").replace("-", "_")
    if keyword.iskeyword(name):
        name += "_"
    return name


def _build_and_validate_command(
    name: str,
    *,
    help: str,
    effect: str | None,
    consequential: bool = False,
    dry_run_supported: bool = True,
    dry_run_unsupported_reason: str | None = None,
    update_of: "UpdateOf | None" = None,
    payload_schema: dict | None = None,
    owns_stdout: bool = False,
    handler: Callable,
    args: list[Arg] | None,
    flag_sets: list[FlagSet] | None,
    constraints: list[AtLeastOne | AllOrNone | Requires | Implies] | None = None,
    env_prefix: str | None,
    global_flags: list[Flag] | None = None,
    passthrough: Passthrough | None = None,
    grants: list[Grant] | None = None,
    forwarding: Forwarding | None = None,
    framework_internal: bool = False,
    extra_flags: list[Flag] | None = None,
    tags: set[str] | None = None,
    inherited_tags: frozenset[str] | None = None,
    hidden: bool = False,
    interactive: bool = False,
    config_fields: list[str] | None = None,
    config_fields_ref: dict[str, ConfigField] | None = None,
    infra_root_names: frozenset[str] | None = None,
    connection_env_names: frozenset[str] | None = None,
    handler_localns: dict | None = None,
) -> Command:
    """Build a Command from a decorated handler, validate everything.

    This is the single registration path: every command in every app -- including
    strictcli's own framework-internal ``check`` and ``config`` commands -- is
    built here, so classification, signature validation and flag validation are
    unbypassable.
    """
    if not help or not help.strip():
        raise ValueError(f'command "{name}": missing help text')

    # Classification is mandatory and has no default.
    if effect is None:
        _raise_command_effect_missing(name)
    if effect not in _EFFECT_VALUES:
        _raise_command_effect_invalid(name, effect)

    # A read_only command cannot be consequential: it changes nothing, so
    # there is nothing to interrupt anyone for (contract §8.1).
    if consequential and effect == EFFECT_READ_ONLY:
        _raise_command_read_only_consequential(name)

    # The dry-run declaration: illegal on read_only, mandatory reason, and no
    # orphan reason. Checked here as well as in Command.__post_init__ so the
    # message names the command before any later validation can fire.
    _validate_dry_run_declaration(
        name, effect, dry_run_supported, dry_run_unsupported_reason,
    )

    resolved_grants = _validate_grants(name, grants)

    # Declared forwarding: the reason is mandatory and non-empty.
    if forwarding is not None:
        if not isinstance(forwarding.reason, str) or not forwarding.reason.strip():
            _raise_forwarding_reason_empty(name)

    # The framework-internal marker is only claimable by handlers defined in
    # this module. A consumer that reaches the marker by any route -- monkey-
    # patching, reflection, subclassing -- fails loudly here rather than
    # silently inheriting a framework exemption.
    if framework_internal:
        if getattr(handler, "__module__", None) != __name__:
            _raise_framework_internal_handler_foreign(name)

    effective_tags = (inherited_tags or frozenset()) | frozenset(tags or set())

    # Validate config_fields bindings (before passthrough check so both paths get it)
    resolved_config_fields: tuple[str, ...] = ()
    if config_fields:
        if config_fields_ref is None:
            config_fields_ref = {}
        for cf_name in config_fields:
            if cf_name not in config_fields_ref:
                raise ValueError(
                    f'command "{name}": config_fields references unknown '
                    f'config field "{cf_name}"'
                )
        resolved_config_fields = tuple(config_fields)

    # Passthrough commands must not have flags, args, flag sets or selectors
    if passthrough is not None:
        decorator_decls = list(getattr(handler, "_strictcli_flags", []))
        decorator_flags = [d for d in decorator_decls if isinstance(d, Flag)]
        decorator_selectors = [
            d for d in decorator_decls if isinstance(d, _Selector)
        ]
        decorator_args = list(getattr(handler, "_strictcli_args", []))
        has_flags = bool(decorator_flags)
        has_args = bool(args) or bool(decorator_args)
        has_flag_sets = bool(flag_sets)
        has_selectors = bool(decorator_selectors)
        if has_flags or has_args or has_flag_sets or has_selectors:
            parts = []
            if has_flags:
                parts.append("flags")
            if has_args:
                parts.append("args")
            if has_flag_sets:
                parts.append("flag sets")
            if has_selectors:
                parts.append("choice flags")
            raise ValueError(
                f'command "{name}": passthrough commands cannot have '
                + ", ".join(parts)
            )
        return Command(
            name=name,
            help=help,
            handler=None,
            effect=effect,
            consequential=consequential,
            dry_run_supported=dry_run_supported,
            dry_run_unsupported_reason=dry_run_unsupported_reason,
            # Carried rather than dropped, exactly as Go carries it: the
            # passthrough early-return sits ahead of §27.11's steps in both
            # implementations, so a passthrough that declares an update keeps
            # the declaration and publishes it (§27 authors no guard for a
            # state a passthrough's own refusal already makes unusable -- it
            # can declare no flags, so it can name no property).
            update_of=update_of,
            payload_schema=payload_schema,
            owns_stdout=owns_stdout,
            passthrough=passthrough,
            tags=effective_tags,
            hidden=hidden,
            interactive=interactive,
            config_fields=resolved_config_fields,
            grants=resolved_grants,
            forwarding=forwarding,
            _framework_internal=framework_internal,
        )

    # Collect flags and selectors attached by @strictcli.flag / @choice_flag.
    # Reverse because Python decorators execute bottom-to-top, so the list
    # is in reverse declaration order. The two share one list so their relative
    # declaration order survives to help rendering (§24.10).
    decorator_decls: list[object] = list(
        reversed(getattr(handler, "_strictcli_flags", []))
    )
    decorator_flags: list[Flag] = [
        d for d in decorator_decls if isinstance(d, Flag)
    ]
    selectors: list[_Selector] = [
        d for d in decorator_decls if isinstance(d, _Selector)
    ]
    # Collect args attached by @strictcli.arg decorators -- reversed for the
    # same reason as flags, so positional binding follows declaration order.
    decorator_args: list[Arg] = list(reversed(getattr(handler, "_strictcli_args", [])))

    # Merge explicit args parameter
    all_args = list(args) if args else []
    all_args.extend(decorator_args)

    # Merge flag sets into flags
    resolved_flag_sets = list(flag_sets) if flag_sets else []
    flag_set_flags: list[Flag] = []
    for flag_set in resolved_flag_sets:
        flag_set_flags.extend(flag_set.flags)

    # All flags: decorator flags + flag set flags + extra flags
    all_flags = decorator_flags + flag_set_flags
    if extra_flags:
        all_flags.extend(extra_flags)
    # The ordered member list help renders from: declared flags and selectors
    # interleaved, then flag-set and framework-added flags.
    all_members: list[object] = list(decorator_decls) + list(flag_set_flags)
    if extra_flags:
        all_members.extend(extra_flags)

    # Validate: no duplicate flag names (catches flag-set flags overlapping
    # with decorator flags)
    seen_flag_names: set[str] = set()
    for f in all_flags:
        if f.name in seen_flag_names:
            raise ValueError(f'command "{name}": duplicate flag name "{f.name}"')
        seen_flag_names.add(f.name)

    # Validate: no collision with global flags
    if global_flags:
        global_flag_names = {gf.name for gf in global_flags}
        for f in all_flags:
            if f.name in global_flag_names:
                raise ValueError(
                    f'command "{name}": flag "{f.name}" collides with a global flag'
                )

    # Build the site table -- every token a scoped declaration can accept --
    # and run the cross-scope name and short rules over it (§24.7).
    site_list = _walk_sites(tuple(selectors), ())
    _validate_scoped_names(name, all_flags, selectors, global_flags, site_list)
    sites: dict[str, tuple[_Site, ...]] = {}
    for site in site_list:
        sites.setdefault(site.name, []).append(site)  # type: ignore[union-attr]
    sites = {k: tuple(v) for k, v in sites.items()}
    scoped_shorts: dict[str, tuple[str, ...]] = {}
    for site in site_list:
        if site.kind == "member":
            short = site.choice.short
        else:
            short = getattr(site.decl, "short", None)
        if not short:
            continue
        names = scoped_shorts.get(short, ())
        if site.name not in names:
            scoped_shorts[short] = names + (site.name,)
    # A short reused by sibling scopes for TWO DIFFERENT names is resolved
    # after the election, which only works when the token tokenizes the same
    # way whatever the outcome -- the same reason §24.7 constrains sibling name
    # reuse -- and only when no election token depends on it.
    for short, names in scoped_shorts.items():
        if len(names) < 2:
            continue
        shapes = set()
        for token in names:
            for site in sites[token]:
                if site.kind != "flag":
                    _raise_short_on_ambiguous_election(name, short, token)
                shapes.add(_value_shape(site.decl))
        if len(shapes) > 1:
            _raise_short_shape_mismatch(name, short, names[0], names[1])

    # Validate: a command flag colliding with a config field (validation-only
    # coexistence) must have an agreeing default. Config fields registered after
    # this command are checked from the App.config_field() side instead.
    if config_fields_ref:
        for f in all_flags:
            cf = config_fields_ref.get(_flag_param_name(f.name))
            if cf is not None:
                _check_flag_configfield_default(f.name, f.presence, f.default, cf)

    # Validate: no duplicate arg names
    seen_arg_names: set[str] = set()
    for a in all_args:
        if a.name in seen_arg_names:
            raise ValueError(f'command "{name}": duplicate arg name "{a.name}"')
        seen_arg_names.add(a.name)

    # Validate: variadic arg constraints
    variadic_count = sum(1 for a in all_args if a.variadic)
    if variadic_count > 1:
        raise ValueError(f'command "{name}": at most one variadic arg is allowed')
    if variadic_count == 1 and not all_args[-1].variadic:
        variadic_name = next(a.name for a in all_args if a.variadic)
        raise ValueError(f'command "{name}": variadic arg "{variadic_name}" must be the last arg')

    # Validate: flag help text
    for f in all_flags:
        if not f.help or not f.help.strip():
            raise ValueError(
                f'command "{name}": flag "{f.name}" missing help text'
            )

    # Validate: env prefix
    if env_prefix is not None:
        for f in all_flags:
            if f.env is not None and f.prefixed:
                expected_prefix = f"{env_prefix}_"
                if not f.env.startswith(expected_prefix):
                    raise ValueError(
                        f'command "{name}": env var "{f.env}" for flag "{f.name}" '
                        f'must start with "{expected_prefix}" (or set prefixed=false)'
                    )

    # Validate: handler signature matches declared flags and args
    sig = inspect.signature(handler)
    has_var_keyword = any(
        p.kind == inspect.Parameter.VAR_KEYWORD
        for p in sig.parameters.values()
    )
    param_names = set(sig.parameters.keys())

    # The first parameter is always the context slot: the framework injects a
    # Context as the handler's first positional argument at dispatch time. It
    # is never matched against a flag or arg, and needs no annotation.
    params_list = list(sig.parameters.values())
    if params_list:
        param_names.discard(params_list[0].name)

    expected_names: set[str] = set()
    for f in all_flags:
        expected_names.add(_flag_param_name(f.name))
    for s in selectors:
        expected_names.add(_flag_param_name(s.name))
    for a in all_args:
        expected_names.add(a.name)
    # Global flags are also passed to handlers
    if global_flags:
        for gf in global_flags:
            expected_names.add(_flag_param_name(gf.name))

    # Guard v2: a **kwargs handler no longer gets a blanket exemption from the
    # "declare everything" guarantee. It must declare forwarding, which waives
    # ONLY the signature cross-check -- flags and args are still fully declared
    # and still fully parsed.
    if has_var_keyword and forwarding is None:
        _raise_handler_var_keyword_undeclared(name)

    # A **kwargs handler cannot carry a selector at all, forwarding or not: the
    # elected value must reach a named, ANNOTATED parameter, because the
    # annotation is what makes the handler's `match` exhaustive (§12.13, S11).
    if has_var_keyword and selectors:
        for s in selectors:
            # The refusal is about the elected value FALLING INTO **kwargs: a
            # handler that names the parameter and annotates it has the
            # annotation that makes `match` exhaustive, which is the whole of
            # what this guard protects. The framework's own `config set` is
            # exactly that shape -- it names `write` and absorbs the app's
            # globals through **kwargs (§10.4's forwarding).
            if _flag_param_name(s.name) not in param_names:
                _raise_handler_kwargs_with_selector(name)

    if not has_var_keyword:
        # Check each selector has a matching parameter
        for s in selectors:
            pname = _flag_param_name(s.name)
            if pname not in param_names:
                raise ValueError(
                    f'command "{name}": handler missing parameter "{pname}" '
                    f'for flag "{s.name}"'
                )

        # Check each flag has a matching parameter
        for f in all_flags:
            pname = _flag_param_name(f.name)
            if pname not in param_names:
                raise ValueError(
                    f'command "{name}": handler missing parameter "{pname}" '
                    f'for flag "{f.name}"'
                )

        # Check each arg has a matching parameter
        for a in all_args:
            if a.name not in param_names:
                raise ValueError(
                    f'command "{name}": handler missing parameter "{a.name}" '
                    f'for arg "{a.name}"'
                )

        # Check for extra parameters
        extra = param_names - expected_names
        if extra:
            extra_name = sorted(extra)[0]
            raise ValueError(
                f'command "{name}": handler has extra parameter "{extra_name}" '
                f"not matching any flag or arg"
            )

    # The mandatory handler-annotation check (§24.12, §12.13). The parameter
    # bound to a selector must be annotated with exactly the declared union;
    # without it a developer could annotate one choice class and `assert_never`
    # would pass the type checker while silently skipping branches. Annotations
    # resolve through `typing.get_type_hints` against the handler's module
    # globals PLUS the decorating frame's locals, so a name importable only
    # under TYPE_CHECKING is a registration error naming it rather than a
    # NameError at import time.
    if selectors:
        _check_handler_selector_annotations(
            name, handler, selectors, handler_localns or {},
        )

    # A handler parameter bound to an OPTIONAL flag or arg must itself default
    # to None (contract §23.3, L1.6). Anything else re-introduces at the handler
    # boundary the sentinel the declaration just removed, and Python is the only
    # implementation with per-parameter defaults to check.
    declared_optional: list[tuple[str, Flag | Arg]] = []
    for f in all_flags:
        if f.presence == _PRESENCE_OPTIONAL:
            declared_optional.append((_flag_param_name(f.name), f))
    if global_flags:
        for gf in global_flags:
            if gf.presence == _PRESENCE_OPTIONAL:
                declared_optional.append((_flag_param_name(gf.name), gf))
    for a in all_args:
        if a.presence == _PRESENCE_OPTIONAL:
            declared_optional.append((a.name, a))
    for pname, decl in declared_optional:
        param = sig.parameters.get(pname)
        if param is None:
            # A **kwargs handler (guard v2 forwarding) names no parameter, so
            # there is no per-parameter default to re-sentinelize with.
            continue
        if param.default is inspect.Parameter.empty:
            # No per-parameter default exists here either -- the same absent
            # site Go and TypeScript have everywhere. The framework passes the
            # value as a keyword argument on every dispatch, and requiring
            # `=None` regardless would force handlers to reorder their
            # parameters (Python forbids a bare parameter after a defaulted
            # one) for a hazard that is not present.
            continue
        if param.default is None:
            continue
        if isinstance(decl, Arg):
            _raise_handler_param_optional_arg_default(name, pname, decl.name)
        _raise_handler_param_optional_flag_default(name, pname, decl.name)

    # Validate the constraint set (§26.8's pinned resolution order).
    resolved_constraints = list(constraints) if constraints else []
    _scoped_paths: dict[str, str] = {}
    if site_list:
        _member_spelled = _member_spelling_map(tuple(selectors))
        for _site in site_list:
            # A member-spelled choice's own token is declared BY the election it
            # carries rather than beside it, so the scope it belongs to is its
            # own segment: a constraint naming `file` is told it is declared
            # under '--file', which is the token the reader would type.
            _path = _site.path
            if _site.kind == "member":
                _path = _path + ((_site.decl.name, _site.choice.name),)
            if _path and _site.name not in _scoped_paths:
                _scoped_paths[_site.name] = _render_scope_path(
                    _path, _member_spelled,
                )
    # The update declaration, in §27.11's pinned eight-step order. It runs
    # BEFORE the constraint passes because its first step is the
    # mutating-default ban, which is a fact about the command's own
    # classification and is independent of every rule declared on top of it.
    _validate_update(
        name, effect, update_of, all_members, all_flags, selectors, all_args,
        _scoped_paths, {s.name for s in site_list}, global_flags,
    )

    _validate_constraint_set(
        name, resolved_constraints, all_flags, all_args, _scoped_paths,
        selectors,
    )

    # Validate flag-default RelativeToRoot markers against declared roots.
    _root_names = infra_root_names or frozenset()
    for f in all_flags:
        if isinstance(f.default, RelativeToRoot) and f.default.env_var not in _root_names:
            raise ValueError(
                f'command "{name}": flag "{f.name}": RelativeToRoot references '
                f'undeclared infra root "{f.default.env_var}"; declare it as an infra root'
            )

    # Validate connection-URL bindings against declared connection envs.
    _conn_names = connection_env_names or frozenset()
    for f in all_flags:
        _validate_connection_binding(f, _conn_names)

    return Command(
        name=name,
        help=help,
        handler=handler,
        effect=effect,
        consequential=consequential,
        dry_run_supported=dry_run_supported,
        dry_run_unsupported_reason=dry_run_unsupported_reason,
        update_of=update_of,
        payload_schema=payload_schema,
        owns_stdout=owns_stdout,
        flags=tuple(all_flags),
        args=tuple(all_args),
        flag_sets=tuple(resolved_flag_sets),
        selectors=tuple(selectors),
        members=tuple(all_members),
        sites=sites,
        shorts=scoped_shorts,
        constraints=tuple(resolved_constraints),
        tags=effective_tags,
        hidden=hidden,
        interactive=interactive,
        config_fields=resolved_config_fields,
        grants=resolved_grants,
        forwarding=forwarding,
        _framework_internal=framework_internal,
    )


def flag(
    name: str,
    *,
    short: str | None = None,
    type: type = str,
    presence: object = _MISSING,
    default: object = _MISSING,
    help: str,
    env: str | None = None,
    env_separator: str | None = None,
    prefixed: bool = True,
    negatable: object = _MISSING,
    choices: list | None = None,
    retired_choices: list | None = None,
    validate: Callable | None = None,
    repeatable: bool = False,
    unique: object = _MISSING,
    conflict_mode: object = _MISSING,
    connection_url: bool = False,
    connection_env: str | None = None,
    nullable: bool = False,
) -> Callable[[F], F]:
    """Module-level decorator to attach a Flag to a command handler."""

    def decorator(func: F) -> F:
        f = Flag(
            name=name,
            short=short,
            type=type,
            presence=presence,
            default=default,
            help=help,
            env=env,
            env_separator=env_separator,
            prefixed=prefixed,
            negatable=negatable,
            choices=choices,
            retired_choices=retired_choices,
            validate=validate,
            repeatable=repeatable,
            unique=unique,
            conflict_mode=conflict_mode,
            connection_url=connection_url,
            connection_env=connection_env,
            nullable=nullable,
        )
        if not hasattr(func, "_strictcli_flags"):
            func._strictcli_flags = []
        func._strictcli_flags.append(f)
        return func

    return decorator


def arg(
    name: str,
    *,
    help: str,
    presence: object = _MISSING,
    default: object = _MISSING,
    variadic: bool = False,
    type: type = str,
    choices: list | None = None,
    retired_choices: list | None = None,
) -> Callable[[F], F]:
    """Module-level decorator to attach an Arg to a command handler."""

    def decorator(func: F) -> F:
        a = Arg(
            name=name, help=help, presence=presence, default=default,
            variadic=variadic, type=type, choices=choices,
            retired_choices=retired_choices,
        )
        if not hasattr(func, "_strictcli_args"):
            func._strictcli_args = []
        func._strictcli_args.append(a)
        return func

    return decorator


# ---------------------------------------------------------------------------
# Help text formatters
# ---------------------------------------------------------------------------


def _format_version(app: App) -> str:
    """Format version string: '{name} {version}'."""
    return f"{app.name} {app.version}"


def _format_app_help(app: App) -> str:
    """Format app-level help shown when the user runs 'myapp --help'."""
    lines: list[str] = [f"{app.name} v{app.version} -- {app.help}"]

    visible_commands = {n: c for n, c in app._commands.items() if not c.hidden}
    if visible_commands:
        lines.append("")
        lines.append("Commands:")
        names = list(visible_commands.keys())
        max_len = max(len(n) for n in names)
        for name in names:
            cmd = visible_commands[name]
            padding = max_len - len(name) + 4
            lines.append(f"  {name}{' ' * padding}{cmd.help}")

    visible_groups = {n: g for n, g in app._groups.items() if not g.hidden}
    if visible_groups:
        lines.append("")
        lines.append("Groups:")
        names = list(visible_groups.keys())
        max_len = max(len(n) for n in names)
        for name in names:
            grp = visible_groups[name]
            padding = max_len - len(name) + 4
            lines.append(f"  {name}{' ' * padding}{grp.help}")

    if app._deprecated:
        lines.append("")
        lines.append("Deprecated:")
        names = list(app._deprecated.keys())
        max_len = max(len(n) for n in names)
        for name in names:
            dep = app._deprecated[name]
            padding = max_len - len(name) + 4
            lines.append(f"  {name}{' ' * padding}{dep.message}")

    if app._global_flags:
        lines.append("")
        lines.append("Global flags:")
        flag_strs = []
        for f in app._global_flags:
            parts = [f"--{f.name}"]
            if f.short:
                parts.append(f"-{f.short}")
            flag_strs.append((", ".join(parts), f.help))
        max_flag_len = max(len(s[0]) for s in flag_strs)
        for flag_str, help_text in flag_strs:
            padding = max_flag_len - len(flag_str) + 4
            lines.append(f"  {flag_str}{' ' * padding}{help_text}")

    if app._infra_root_order or app._handshake_order or app._connection_order:
        lines.append("")
        lines.append("Infrastructure:")
        lines.append("  (location/handshake env vars; not suppressed by --hermetic)")
        all_evs = list(app._infra_root_order) + list(app._handshake_order) + list(app._connection_order)
        max_len = max(len(ev) for ev in all_evs)
        for ev in app._infra_root_order:
            padding = max_len - len(ev) + 4
            lines.append(f"  {ev}{' ' * padding}root (default: {app._infra_root_defaults[ev]})")
        for ev in app._handshake_order:
            padding = max_len - len(ev) + 4
            lines.append(f"  {ev}{' ' * padding}{app._handshake_envs[ev]}")
        for ev in app._connection_order:
            padding = max_len - len(ev) + 4
            lines.append(f"  {ev}{' ' * padding}connection URL, suppressed by --hermetic ({app._connection_envs[ev]})")

    lines.append("")
    lines.append(f"Use '{app.name} <command> --help' for more information.")

    return "\n".join(lines)


def _format_group_help(app: App, group: Group, path: list[str] | None = None) -> str:
    """Format group-level help shown when the user runs 'myapp group --help'.

    ``path`` is the list of group names leading to this group (e.g. ['dns', 'zone']).
    When None, the path is computed by searching the app's group tree.
    """
    if path is None:
        path = _find_group_path(app, group)
    full_path = " ".join(path)
    lines: list[str] = [f"{app.name} {full_path} -- {group.help}"]

    visible_commands = {n: c for n, c in group.commands.items() if not c.hidden}
    if visible_commands:
        lines.append("")
        lines.append("Commands:")
        names = list(visible_commands.keys())
        max_len = max(len(n) for n in names)
        for name in names:
            cmd = visible_commands[name]
            padding = max_len - len(name) + 4
            lines.append(f"  {name}{' ' * padding}{cmd.help}")

    visible_groups = {n: g for n, g in group._groups.items() if not g.hidden}
    if visible_groups:
        lines.append("")
        lines.append("Groups:")
        names = list(visible_groups.keys())
        max_len = max(len(n) for n in names)
        for name in names:
            sub = visible_groups[name]
            padding = max_len - len(name) + 4
            lines.append(f"  {name}{' ' * padding}{sub.help}")

    if group.deprecated:
        lines.append("")
        lines.append("Deprecated:")
        names = list(group.deprecated.keys())
        max_len = max(len(n) for n in names)
        for name in names:
            dep = group.deprecated[name]
            padding = max_len - len(name) + 4
            lines.append(f"  {name}{' ' * padding}{dep.message}")

    lines.append("")
    lines.append(
        f"Use '{app.name} {full_path} <command> --help' for more information."
    )

    return "\n".join(lines)


def _find_group_path(app: App, target: Group) -> list[str]:
    """Find the full path (list of group names) from app root to the target group."""
    def _search(groups: dict[str, Group], path: list[str]) -> list[str] | None:
        for name, grp in groups.items():
            current = path + [name]
            if grp is target:
                return current
            result = _search(grp._groups, current)
            if result is not None:
                return result
        return None

    result = _search(app._groups, [])
    # Fallback: just use the group name (shouldn't happen in practice)
    return result if result is not None else [target.name]


def _build_flag_spec(f: Flag) -> str:
    """Build the left-column spec string for a flag (e.g. '--target, -t <str>')."""
    parts: list[str] = []
    if f.type is bool and f.negatable and f.compound == "scalar":
        parts.append(f"--{f.name}, --no-{f.name}")
        if f.short:
            parts.append(f"-{f.short}")
    else:
        parts.append(f"--{f.name}")
        if f.short:
            parts.append(f"-{f.short}")
    spec = ", ".join(parts)
    if f.compound == "list":
        type_name = _TYPE_NAMES.get(f.item_type, "str")
        spec += f" <{type_name}>"
    elif f.compound == "dict":
        type_name = _TYPE_NAMES.get(f.value_type, "str")
        spec += f" <key={type_name}>"
    elif f.type is str:
        spec += " <str>"
    elif f.type is int:
        spec += " <int>"
    elif f.type is float:
        spec += " <float>"
    # A nullable property renders its minted clear spelling on the SAME line,
    # exactly as a negatable bool renders `--x, --no-x` (contract §27.6): one
    # line, one help text, and one presence part -- §23.8's invariant is
    # untouched, because the minted spelling is a second way to write to one
    # declaration rather than a second declaration.
    if f.nullable:
        spec += ", --" + _unset_flag_name(f.name)
    return spec


def _build_flag_meta(f: Flag, *, block: bool = False) -> str:
    """Build the bracketed metadata suffix for a flag.

    ``block`` is set when the flag's choices render as an indented block
    beneath it (§24.10), which is where the one-line ``[choices: a, b]`` form
    would say the same thing twice.
    """
    meta_parts: list[str] = []
    if f.compound == "list":
        meta_parts.append("list")
    elif f.compound == "dict":
        meta_parts.append("dict")
    elif f.repeatable:
        meta_parts.append("repeatable")
    if f.unique is True:
        meta_parts.append("unique")
    if f.choices is not None and not block:
        choices_str = ", ".join(str(c) for c in f.choices)
        meta_parts.append(f"choices: {choices_str}")
    if f.env is not None:
        if f.env_separator is not None:
            meta_parts.append(f"env: {f.env} (sep: {f.env_separator})")
        else:
            meta_parts.append(f"env: {f.env}")
    # Exactly one presence part, last on the line (contract §23.8).
    meta_parts.append(_format_presence_for_help(f.presence, f))
    return " [" + "] [".join(meta_parts) + "]"


def _format_presence_for_help(presence: str, decl: "Flag | Arg") -> str:
    """The one bracketed presence part every flag and arg line carries.

    `required` and `optional` render as the bare word; a declared default
    renders its value. A declared EMPTY collection renders `[]` / `{}` rather
    than nothing: it is a declaration now, and a declaration that rendered as
    blank would leave one line in the help output with no presence part.
    """
    if presence == _PRESENCE_REQUIRED:
        return "required"
    if presence == _PRESENCE_OPTIONAL:
        return "optional"
    return f"default: {_format_declared_default_for_help(decl)}"


def _format_declared_default_for_help(decl: "Flag | Arg") -> str:
    """The declared default's value, rendered the same way on both surfaces.

    A flag and a positional arg declaring the same value render it
    identically -- the branches below select on the SHAPE of the declaration
    (only a flag can be repeatable or a dict), never on which surface declared
    it. The arg side used to render its value through ``str`` alone, which put
    a bool's `True` next to a flag's `true` in the same help output.
    """
    return _format_value_for_help(decl.default, decl)


def _format_value_for_help(value: object, decl: "Flag | Arg") -> str:
    """§23.8's value formatter: one value, rendered under one declaration.

    The value is a parameter rather than always ``decl.default`` because a
    defaulted selector renders the fields of its default INSTANCE through this
    same formatter (§24.10, item 215) -- a bool field there is `false`, exactly
    as it is on the flag's own line.
    """
    compound = getattr(decl, "compound", "scalar")
    if compound == "dict":
        if not value:
            return "{}"
        return _format_default_for_help(value)
    if getattr(decl, "repeatable", False):
        if not value:
            return "[]"
        return ", ".join(_format_value_for_error(elem) for elem in value)
    if decl.type is bool and compound == "scalar":
        return "true" if value else "false"
    return _format_default_for_help(value)


def _renders_as_block(f: "Flag | Arg") -> bool:
    """A choice-carrying declaration renders as an indented block iff any of
    its choices carries help or a scope; otherwise it keeps today's one-line
    ``[choices: a, b, c]`` form (§24.10).

    The rule is content-keyed, never surface-keyed: a positional arg whose
    entries carry help renders the same block a flag's do.
    """
    if f.choice_records is None:
        return False
    return any(r.help for r in f.choice_records)


def _build_selector_spec(sel: _Selector) -> str:
    """The left-column spec for a selector's own line.

    A member-spelled selector has no token to render, so its own line carries
    its name bare -- it is the handler's key and the noun errors use, never
    something a user types (§24.10).
    """
    if sel.is_member_spelled:
        return sel.name
    spec = f"--{sel.name}"
    if sel.short:
        spec += f", -{sel.short}"
    return f"{spec} <choice>"


def _build_member_spec(spec: _ChoiceSpec) -> str:
    """The left-column spec for one member flag under member spelling.

    The short comes from the choice rather than from its payload: a
    payload-less member declares one too, on `@choice(short=...)`.
    """
    out = f"--{spec.name}"
    if spec.short:
        out += f", -{spec.short}"
    payload = spec.payload
    if payload is not None and payload.type is not bool:
        out += f" <{_TYPE_NAMES[payload.type]}>"
    return out


def _format_selector_presence(sel: _Selector) -> str:
    """The one presence part a selector's line carries (§23.8, §24.10).

    A defaulted selector renders its COMPLETE elected value -- the choice plus
    the fields its scope needed -- because that is what the default is (§24.5).
    Each field goes through §23.8's value formatter, the one the field's own
    line already uses, so a bool reads `false` here too (item 215).
    """
    if sel.presence == _PRESENCE_REQUIRED:
        return " [required]"
    spec = sel.choice_by_class(type(sel.default))
    inner = ", ".join(
        f"{f.name}="
        f"{_format_value_for_help(getattr(sel.default, _flag_param_name(f.name)), f)}"
        for f in spec.members
        if isinstance(f, Flag)
    )
    rendered = spec.name + (f" ({inner})" if inner else "")
    return f" [default: {rendered}]"


def _flag_block_rows(
    members: tuple, indent: int,
) -> list[tuple[int, str, str, str]]:
    """Every line of the command's flag block, as (indent, spec, help, meta).

    A choice line indents two columns past its selector's line, and a choice's
    scoped flags indent two columns past their choice; recursion adds two per
    level (§24.10).
    """
    rows: list[tuple[int, str, str, str]] = []
    for m in members:
        if isinstance(m, Flag):
            block = _renders_as_block(m)
            rows.append((
                indent, _build_flag_spec(m), m.help,
                _build_flag_meta(m, block=block),
            ))
            if block:
                for record in m.choice_records or ():
                    rows.append((
                        indent + 2,
                        _format_default_for_help(record.value),
                        record.help or "",
                        "",
                    ))
            continue
        sel: _Selector = m
        presence = _format_selector_presence(sel)
        if sel.is_member_spelled:
            rows.append((
                indent, _build_selector_spec(sel),
                f"{sel.help} (exactly one of the following)", presence,
            ))
            for c in sel.choices:
                rows.append((
                    indent + 2, _build_member_spec(c), c.help, " [required]",
                ))
                rows.extend(_flag_block_rows(c.members, indent + 4))
            continue
        rows.append((indent, _build_selector_spec(sel), sel.help, presence))
        for c in sel.choices:
            rows.append((indent + 2, c.name, c.help, ""))
            rows.extend(_flag_block_rows(c.members, indent + 4))
    return rows


def _format_dry_run_section(cmd: Command) -> list[str]:
    """The `Dry run:` section of command help, or nothing.

    Rendered only for a command that declares ``dry_run_supported=False``: the
    baseline (dry run works) needs no announcement, and a section on every
    command would be noise. Byte-identical across implementations.
    """
    if cmd.dry_run_supported:
        return []
    return [
        "",
        "Dry run:",
        f"  --dry-run is not supported: {cmd.dry_run_unsupported_reason}",
    ]


def _constraint_help_sentence(
    c: object, kinds: dict, by_name: dict,
) -> str:
    """The per-family sentence a `Constraints:` line carries (§26.10)."""
    if isinstance(c, AtLeastOne):
        return f"at least one of {_render_member_list(c, kinds, by_name, True)}"
    if isinstance(c, AllOrNone):
        return f"all or none of {_render_member_list(c, kinds, by_name, True)}"
    if isinstance(c, Requires):
        return f"--{c.flag} requires --{c.depends_on}"
    # `Implies` renders `--no-<implies>` when the declared value is false,
    # which is the negation spelling its own violation sentence already uses.
    # No `=true` spelling is invented.
    target = c.implies if c.value else f"no-{c.implies}"
    return f"--{c.flag} implies --{target}"


def _format_constraints_section(cmd: Command) -> list[str]:
    """The `Constraints:` block (§26.10) -- the first rendering this system has
    ever had in any implementation.

    A declared rule that changes whether an invocation is accepted, and that
    the operator cannot see in `--help`, is the same erasure the presence round
    ended for requiredness. The DECLARED NAME renders in the position a flag
    name occupies, so the identifier a violation prints is discoverable in the
    help the operator already read, and the block computes ONE alignment column
    across itself alone -- it never shares the flag block's column. A nested
    constraint gets its own line AND appears inside its parent's line, because
    it is both a rule of its own and an operand. No presence part is repeated:
    every flag line already carries exactly one, and a constraint states a rule
    over members rather than a property of one.
    """
    if not cmd.constraints:
        return []
    kinds, by_name = _constraint_index(cmd)
    rows = [
        (c.name, _constraint_help_sentence(c, kinds, by_name))
        for c in cmd.constraints
    ]
    column = max(2 + len(n) for n, _ in rows) + 4
    lines = ["", "Constraints:"]
    for n, sentence in rows:
        lines.append(f"  {n}{' ' * (column - 2 - len(n))}{sentence}".rstrip())
    return lines


def _format_command_help(app: App, cmd: Command, prefix: str = "") -> str:
    """Format command-level help shown when the user runs 'myapp cmd --help'."""
    lines: list[str] = [f"{app.name} {prefix}{cmd.name} -- {cmd.help}"]

    # Rendered before the passthrough early-return: a passthrough command can
    # declare the refusal too, and its help is the only place the reason would
    # otherwise be visible.
    lines.extend(_format_dry_run_section(cmd))

    # Passthrough commands show only the header line (no flags/args section)
    if cmd.passthrough is not None:
        return "\n".join(lines)

    if cmd.args:
        lines.append("")
        lines.append("Arguments:")
        # The content-keyed block rule reaches positional args too: an arg
        # whose choices entries carry help renders the indented block, exactly
        # as a flag's do (§24.10). ONE alignment column across the whole
        # Arguments section, deepest entry included.
        arg_rows: list[tuple[int, str, str, str]] = []
        for a in cmd.args:
            dn = f"{a.name}..." if a.variadic else a.name
            block = _renders_as_block(a)
            meta_parts: list[str] = []
            if a.type is not str:
                meta_parts.append(f"type: {a.type.__name__}")
            if a.choices is not None and not block:
                choices_str = ", ".join(str(c) for c in a.choices)
                meta_parts.append(f"choices: {choices_str}")
            # Exactly one presence part, last on the line. A required
            # positional now renders `[required]` -- it was previously the one
            # declaration in the framework whose presence was invisible.
            meta_parts.append(_format_presence_for_help(a.presence, a))
            arg_rows.append((
                2, dn, a.help, " [" + "] [".join(meta_parts) + "]",
            ))
            if block:
                for record in a.choice_records or ():
                    arg_rows.append((
                        4,
                        _format_default_for_help(record.value),
                        record.help or "",
                        "",
                    ))
        column = max(indent + len(spec) for indent, spec, _, _ in arg_rows) + 4
        for indent, spec, help_text, meta in arg_rows:
            padding = column - indent - len(spec)
            lines.append(
                f"{' ' * indent}{spec}{' ' * padding}{help_text}{meta}".rstrip()
            )

    # The command's own flag block. ONE alignment column is computed across the
    # whole block, deepest entry included, so help text starts in the same
    # column everywhere on the page (§24.10).
    rows = _flag_block_rows(cmd.members, 2)
    if rows:
        lines.append("")
        lines.append("Flags:")
        column = max(indent + len(spec) for indent, spec, _, _ in rows) + 4
        for indent, spec, help_text, meta in rows:
            padding = column - indent - len(spec)
            lines.append(
                f"{' ' * indent}{spec}{' ' * padding}{help_text}{meta}".rstrip()
            )

    lines.extend(_format_constraints_section(cmd))

    # Global flags
    if app._global_flags:
        lines.append("")
        lines.append("Global flags:")
        specs = [_build_flag_spec(f) for f in app._global_flags]
        max_spec = max(len(s) for s in specs)
        for f, spec in zip(app._global_flags, specs):
            padding = max_spec - len(spec) + 4
            meta = _build_flag_meta(f)
            lines.append(f"  {spec}{' ' * padding}{f.help}{meta}")

    return "\n".join(lines)


# ---------------------------------------------------------------------------
# Tag DSL
# ---------------------------------------------------------------------------

_TAG_NAME_RE = re.compile(r"[a-z][a-z0-9-]*")


def _tagdsl_tokenize(expr: str) -> list[tuple[str, str, int]]:
    """Tokenize a tag expression into (type, value, position) tuples."""
    tokens: list[tuple[str, str, int]] = []
    i = 0
    while i < len(expr):
        ch = expr[i]
        if ch.isspace():
            i += 1
            continue
        if ch == "&":
            tokens.append(("AND", "&", i))
            i += 1
        elif ch == "|":
            tokens.append(("OR", "|", i))
            i += 1
        elif ch == "^":
            tokens.append(("XOR", "^", i))
            i += 1
        elif ch == "-":
            tokens.append(("DIFF", "-", i))
            i += 1
        elif ch == "!":
            tokens.append(("NOT", "!", i))
            i += 1
        elif ch == "(":
            tokens.append(("LPAREN", "(", i))
            i += 1
        elif ch == ")":
            tokens.append(("RPAREN", ")", i))
            i += 1
        else:
            m = _TAG_NAME_RE.match(expr, i)
            if m:
                tokens.append(("IDENT", m.group(), i))
                i = m.end()
            else:
                raise ValueError(
                    f'tag expression: unexpected character "{ch}" at position {i}'
                )
    return tokens


def _tagdsl_parse(tokens: list[tuple[str, str, int]]) -> tuple:
    """Parse tag expression tokens into an AST using recursive descent.

    Precedence (tightest first): NOT, AND, XOR, OR, DIFF.
    """
    pos = 0

    def peek() -> tuple[str, str, int] | None:
        nonlocal pos
        if pos < len(tokens):
            return tokens[pos]
        return None

    def consume() -> tuple[str, str, int]:
        nonlocal pos
        tok = tokens[pos]
        pos += 1
        return tok

    def end_pos() -> int:
        if not tokens:
            return 0
        last = tokens[-1]
        return last[2] + len(last[1])

    def parse_atom() -> tuple:
        tok = peek()
        if tok is None:
            raise ValueError(
                f"tag expression: unexpected end of expression "
                f"at position {end_pos()}"
            )
        if tok[0] == "NOT":
            consume()
            child = parse_atom()
            return ("not", child)
        if tok[0] == "LPAREN":
            consume()
            node = parse_diff()
            closing = peek()
            if closing is None or closing[0] != "RPAREN":
                raise ValueError(
                    f'tag expression: expected ")" at position {end_pos()}'
                )
            consume()
            return node
        if tok[0] == "IDENT":
            consume()
            return ("ident", tok[1])
        raise ValueError(
            f'tag expression: unexpected token "{tok[1]}" at position {tok[2]}'
        )

    def parse_and() -> tuple:
        left = parse_atom()
        while True:
            tok = peek()
            if tok is None or tok[0] != "AND":
                break
            consume()
            right = parse_atom()
            left = ("and", left, right)
        return left

    def parse_xor() -> tuple:
        left = parse_and()
        while True:
            tok = peek()
            if tok is None or tok[0] != "XOR":
                break
            consume()
            right = parse_and()
            left = ("xor", left, right)
        return left

    def parse_or() -> tuple:
        left = parse_xor()
        while True:
            tok = peek()
            if tok is None or tok[0] != "OR":
                break
            consume()
            right = parse_xor()
            left = ("or", left, right)
        return left

    def parse_diff() -> tuple:
        left = parse_or()
        while True:
            tok = peek()
            if tok is None or tok[0] != "DIFF":
                break
            consume()
            right = parse_or()
            left = ("diff", left, right)
        return left

    result = parse_diff()
    tok = peek()
    if tok is not None:
        raise ValueError(
            f'tag expression: unexpected token "{tok[1]}" at position {tok[2]}'
        )
    return result


def _tagdsl_evaluate(ast: tuple, tags: set[str]) -> bool:
    """Evaluate a tag DSL AST against a set of tags."""
    kind = ast[0]
    if kind == "ident":
        return ast[1] in tags
    if kind == "not":
        return not _tagdsl_evaluate(ast[1], tags)
    if kind == "and":
        return _tagdsl_evaluate(ast[1], tags) and _tagdsl_evaluate(ast[2], tags)
    if kind == "or":
        return _tagdsl_evaluate(ast[1], tags) or _tagdsl_evaluate(ast[2], tags)
    if kind == "xor":
        return _tagdsl_evaluate(ast[1], tags) != _tagdsl_evaluate(ast[2], tags)
    if kind == "diff":
        return _tagdsl_evaluate(ast[1], tags) and not _tagdsl_evaluate(ast[2], tags)
    raise ValueError(f"tag expression: unknown AST node {kind!r}")


def _match_tag_expr(expr: str, tags: set[str]) -> bool:
    """Evaluate a tag expression against a set of tags. Returns bool."""
    tokens = _tagdsl_tokenize(expr)
    if not tokens:
        raise ValueError("tag expression: empty expression")
    ast = _tagdsl_parse(tokens)
    return _tagdsl_evaluate(ast, tags)


# ---------------------------------------------------------------------------
# Check runner
# ---------------------------------------------------------------------------


def _filter_checks(
    check_defs: dict[str, _CheckDef],
    tag_expr: str | None,
    name_glob: str | None,
    run_all: bool,
) -> set[str]:
    """Filter checks by tag expression and/or name glob.

    Returns the set of selected check names.
    """
    if run_all:
        return set(check_defs.keys())

    by_tag: set[str] | None = None
    by_name: set[str] | None = None

    if tag_expr is not None:
        by_tag = {
            name for name, cdef in check_defs.items()
            if _match_tag_expr(tag_expr, set(cdef.tags))
        }

    if name_glob is not None:
        by_name = {
            name for name in check_defs
            if fnmatch.fnmatch(name, name_glob)
        }

    if by_tag is not None and by_name is not None:
        return by_tag & by_name
    if by_tag is not None:
        return by_tag
    if by_name is not None:
        return by_name
    return set()


def _resolve_check_order(
    check_defs: dict[str, _CheckDef], selected: set[str],
) -> list[str]:
    """Resolve execution order via topological sort, pulling in dependencies.

    If a selected check depends on an unselected check, the dependency is
    pulled into the execution set. Raises ValueError on cycles.
    """
    # Expand selected to include all transitive dependencies
    expanded: set[str] = set()
    stack = list(selected)
    while stack:
        name = stack.pop()
        if name in expanded:
            continue
        expanded.add(name)
        for dep in check_defs[name].depends_on:
            if dep not in expanded:
                stack.append(dep)

    # Build adjacency and in-degree for Kahn's algorithm
    in_degree: dict[str, int] = {name: 0 for name in expanded}
    dependents: dict[str, list[str]] = {name: [] for name in expanded}

    for name in expanded:
        for dep in check_defs[name].depends_on:
            if dep in expanded:
                dependents[dep].append(name)
                in_degree[name] += 1

    # Kahn's algorithm
    queue: deque[str] = deque(
        name for name in sorted(expanded) if in_degree[name] == 0
    )
    order: list[str] = []

    while queue:
        node = queue.popleft()
        order.append(node)
        for child in sorted(dependents[node]):
            in_degree[child] -= 1
            if in_degree[child] == 0:
                queue.append(child)

    if len(order) != len(expanded):
        # Cycle detection: find a cycle for the error message
        remaining = expanded - set(order)
        cycle = _find_cycle(check_defs, remaining)
        raise ValueError(f"check dependency cycle: {cycle}")

    return order


def _find_cycle(
    check_defs: dict[str, _CheckDef], nodes: set[str],
) -> str:
    """Find and format a cycle among the given nodes for error reporting."""
    # DFS to find a cycle path
    visited: set[str] = set()
    path: list[str] = []
    path_set: set[str] = set()

    def dfs(node: str) -> str | None:
        visited.add(node)
        path.append(node)
        path_set.add(node)
        for dep in check_defs[node].depends_on:
            if dep not in nodes:
                continue
            if dep in path_set:
                # Found cycle: extract from dep to current node back to dep
                cycle_start = path.index(dep)
                cycle_path = path[cycle_start:] + [dep]
                return " -> ".join(cycle_path)
            if dep not in visited:
                result = dfs(dep)
                if result:
                    return result
        path.pop()
        path_set.discard(node)
        return None

    for node in sorted(nodes):
        if node not in visited:
            result = dfs(node)
            if result:
                return result
    return " -> ".join(sorted(nodes))


def _check_is_pure(cdef: _CheckDef) -> bool:
    """Whether a check is executable under the purity partition: declared pure
    AND not requiring network access. Everything else is "impure"."""
    return cdef.pure and not cdef.needs_network


def _run_checks(
    check_defs: dict,
    check_names: list[str],
    context: CheckContext,
    ignore_warnings: bool,
    scope_adapter: object | None = None,
    pure_only: bool = False,
) -> tuple[list[tuple[str, _CheckOutcome, int]], list[str], int]:
    """Execute checks in order, skipping dependents of gated (FAIL) checks.

    Returns (results_list, impure_listed, exit_code). Each results_list entry is
    (name, outcome, duration_ms) where duration_ms is the wall-clock time in
    integer milliseconds spent inside the impl (0 for non-executed checks).
    impure_listed holds the
    ordered names of checks left unexecuted by the purity partition (empty
    unless pure_only=True); listed checks contribute nothing to the exit code.
    exit_code is 0 if all executed checks pass (or all warn with
    ignore_warnings=True), 1 otherwise.

    Purity partition (pure_only): only pure, non-network checks execute; every
    other check is listed. A check also joins the listing if any dependency was
    listed (its precondition cannot be verified). The failed-dependency cascade
    takes precedence over the listing.
    """
    results: list[tuple[str, _CheckOutcome, int]] = []
    # Checks whose dependents should be cascade-skipped: cascade keys ONLY on a
    # derived FAIL (an error-severity problem present) or a cascade-skip. A WARN
    # outcome satisfies the dependency (dependents still run) and only affects
    # the exit code -- warn-severity checks physically cannot cascade because
    # WarnReporter lacks error-minting. An explicit SKIP is not a failure.
    failed_checks: set[str] = set()
    # Checks listed (not executed) under the purity partition, so dependents
    # whose precondition cannot be verified join the listing.
    listed_checks: set[str] = set()
    impure_listed: list[str] = []
    exit_code = 0

    def record(name: str, outcome: _CheckOutcome) -> None:
        nonlocal exit_code
        status = _derive_status(outcome)
        if status == "fail":
            failed_checks.add(name)
            exit_code = 1
        elif status == "warn":
            if not ignore_warnings:
                exit_code = 1
        # "pass" / "skip": no cascade, no exit code change.

    for name in check_names:
        cdef = check_defs[name]

        # Check if any dependency failed
        failed_dep = None
        for dep in cdef.depends_on:
            if dep in failed_checks:
                failed_dep = dep
                break

        if failed_dep is not None:
            outcome = _mint_skip(f'skipped: dependency "{failed_dep}" failed')
            failed_checks.add(name)
            results.append((name, outcome, 0))
            exit_code = 1
            continue

        # Purity partition: list (do not execute) impure checks and any check
        # that depends on a listed one. Listed checks contribute no exit code.
        if pure_only:
            listed = not _check_is_pure(cdef)
            if not listed:
                listed = any(dep in listed_checks for dep in cdef.depends_on)
            if listed:
                listed_checks.add(name)
                impure_listed.append(name)
                continue

        # Apply scope adapter if the check has a scope and an adapter is set.
        # The adapter returns a replacement context OR a SkipCheck directive.
        check_context = context
        if cdef.scope and scope_adapter is not None:
            adapted = scope_adapter(context, cdef.scope)
            if isinstance(adapted, SkipCheck):
                outcome = _mint_skip(f"skipped: {adapted.reason}")
                results.append((name, outcome, 0))
                # Explicit skip: no cascade, no exit code change.
                continue
            # A non-SkipCheck return is used as the check's replacement context.
            # Enforce the adapter contract: it must satisfy the CheckContext
            # protocol (expose a project_root attribute). Anything else is a
            # hard error rather than a bogus context silently handed to the impl.
            if not hasattr(adapted, "project_root"):
                raise TypeError(
                    f'scope adapter for check "{name}" returned {adapted!r}; a '
                    f"scope adapter must return a SkipCheck or a CheckContext "
                    f"(an object exposing a project_root attribute)"
                )
            check_context = adapted

        # Capture wall-clock duration around the impl call only.
        _start = time.perf_counter()
        try:
            outcome = cdef.impl(check_context)
        except Exception as exc:  # noqa: BLE001 -- containment is the point
            # A raising impl is contained here and reported as that check's own
            # failure: one broken check must not abort the whole run, and every
            # other selected check still executes. BaseException (a
            # KeyboardInterrupt, a SystemExit) is deliberately NOT contained --
            # those are the operator ending the process, not a broken check.
            duration_ms = int((time.perf_counter() - _start) * 1000)
            outcome = _mint_check_abort(name, exc)
            results.append((name, outcome, duration_ms))
            record(name, outcome)
            continue
        duration_ms = int((time.perf_counter() - _start) * 1000)
        # Belt-and-braces: an impl must return a reporter-minted outcome.
        if not isinstance(outcome, _CheckOutcome):
            raise TypeError(
                f'check "{name}" returned {outcome!r}, not an outcome minted by '
                f"its reporter (use passed/skipped/found)"
            )
        results.append((name, outcome, duration_ms))
        record(name, outcome)

    return results, impure_listed, exit_code


# ---------------------------------------------------------------------------
# Check command output helpers
# ---------------------------------------------------------------------------

_CHECK_STATUS_LABELS = {"pass": "PASS", "fail": "FAIL", "warn": "WARN", "skip": "SKIP"}


def _check_list_items(check_defs: dict[str, _CheckDef]) -> list[dict]:
    """The check listing as machine data (the check command's payload)."""
    items = []
    for cdef in sorted(check_defs.values(), key=lambda c: c.name):
        entry: dict = {"name": cdef.name, "tags": cdef.tags, "severity": cdef.severity}
        if cdef.scope:
            entry["scope"] = cdef.scope
        items.append(entry)
    return items


def _check_list_mode(check_defs: dict[str, _CheckDef], ctx: "Context") -> None:
    """Write the human-readable check listing through the context writer.

    The whole table is ONE ``ctx.info`` call, so machine mode carries it as a
    single diagnostic (contract §19.1) rather than one per row.
    """
    # Sort alphabetically for deterministic output matching Go
    sorted_defs = sorted(check_defs.values(), key=lambda c: c.name)

    if not check_defs:
        ctx.info("No checks defined.")
        return

    # Compute column widths
    name_width = max(len(cdef.name) for cdef in sorted_defs)
    name_width = max(name_width, len("NAME"))
    tags_width = max(len(", ".join(cdef.tags)) for cdef in sorted_defs)
    tags_width = max(tags_width, len("TAGS"))

    lines = [f"{'NAME':<{name_width}}   {'TAGS':<{tags_width}}   SEVERITY"]
    for cdef in sorted_defs:
        tags_str = ", ".join(cdef.tags)
        lines.append(
            f"{cdef.name:<{name_width}}   {tags_str:<{tags_width}}   {cdef.severity}"
        )
    ctx.info("\n".join(lines))


def _check_dry_run_mode(
    check_defs: dict[str, _CheckDef], listed: list[str], order: list[str],
    ctx: "Context",
) -> None:
    """Write the would-run plan for the checks a dry run did NOT execute.

    ``listed`` is the purity partition's remainder (the impure checks and any
    check whose dependency was listed); ``order`` is the full selected order,
    used only to decide which dependencies are worth naming. The header is
    printed even when nothing was left over -- an empty plan is a statement
    ("everything selected ran"), the same way the framework's own would-do log
    prints its header with an empty body.

    The whole plan is ONE ``ctx.info`` call, so machine mode carries it as a
    single diagnostic (contract §19.1).
    """
    lines = [f"Would run {len(listed)} check{'s' if len(listed) != 1 else ''}:"]
    for i, name in enumerate(listed, 1):
        cdef = check_defs[name]
        purity = "pure" if _check_is_pure(cdef) else "impure"
        deps = [d for d in cdef.depends_on if d in set(order)]
        if deps:
            lines.append(f"  {i}. {name} (depends on: {', '.join(deps)}) [{purity}]")
        else:
            lines.append(f"  {i}. {name} [{purity}]")
    ctx.info("\n".join(lines))



def format_check_results(
    results: list[CheckRunResult], verbose: bool = False,
) -> str:
    """Format check results as a human-readable aligned string.

    Shows the derived status label, name, and message, with minted problems
    listed under the check row grouped by severity (error problems first, then
    warn problems), each tagged with its severity. Problems appear for
    fail/warn/skip outcomes or when verbose is True.
    """
    if not results:
        return ""

    name_width = max(len(r.name) for r in results)
    lines: list[str] = []
    counts = {"pass": 0, "fail": 0, "warn": 0, "skip": 0}

    for r in results:
        status = r.status
        counts[status] += 1
        label = _CHECK_STATUS_LABELS[status]
        row = f"{label}  {r.name:<{name_width}}    {r.outcome.message}"
        # Under --verbose, append the per-check duration in a stable, pattern-
        # matchable shape: "(<n>ms)".
        if verbose:
            row += f" ({r.duration_ms}ms)"
        lines.append(row)

        show_problems = verbose or status in ("fail", "warn", "skip")
        if show_problems:
            for p in r.outcome._ordered_problems():
                lines.append(f"        [{p.severity}] {p.text}")
        # Notes are verdict-inert and surface ONLY under --verbose, on every
        # outcome including a pass.
        if verbose:
            for n in r.outcome.notes:
                lines.append(f"        [note] {n}")

    # Under --verbose, append a trailing blank line and a count summary.
    if verbose:
        lines.append("")
        lines.append(
            f"{counts['pass']} passed / {counts['fail']} failed / "
            f"{counts['warn']} warned / {counts['skip']} skipped"
        )

    return "\n".join(lines)


def _check_result_items(results: list[CheckRunResult]) -> list[dict]:
    """Check results as machine data (the check command's run payload).

    Each entry carries the derived status plus the minted problems (each with
    its severity and text). Problems serialize as [] when empty.
    """
    return [
        {
            "name": r.name,
            "status": r.status,
            "message": r.outcome.message,
            "problems": [
                {"severity": p.severity, "text": p.text}
                for p in r.outcome.problems
            ],
            "notes": list(r.outcome.notes),
            "duration_ms": r.duration_ms,
        }
        for r in results
    ]


def format_check_results_json(results: list[CheckRunResult]) -> str:
    """Format check results as a JSON string."""
    return json.dumps(_check_result_items(results), separators=(",", ":"))


# ---------------------------------------------------------------------------
# Schema serialization (--dump-schema)
# ---------------------------------------------------------------------------

_TYPE_NAMES = {str: "str", bool: "bool", int: "int", float: "float"}


def _serialize_choice_records(records: tuple) -> list[dict]:
    """A value flag's `choices=` entries, as the records item 164 made them.

    The machine-readable half of a choices declaration lives in the fragment's
    `enum`; this is the human-readable half, which JSON Schema has no
    vocabulary for (§25.5). `help` is OMITTED when the entry declares none, so
    the two spellings of "no help" -- an absent one and Go's empty string --
    cannot produce different bytes for the same declaration.
    """
    out: list[dict] = []
    for r in records:
        entry: dict = {"value": r.value}
        if r.help:
            entry["help"] = r.help
        out.append(entry)
    return out


def _serialize_default_value(value: object) -> object:
    """A declared default, as the schema and `config show --json` publish it.

    A RelativeToRoot marker is emitted in its machine-stable shape: only the
    declared env var and path parts, never the resolved machine-specific path.
    One helper serves both surfaces because they publish the same fact -- the
    declaration -- and §13 pins exactly one shape for it (§25.10, §18.28
    item 265).
    """
    if isinstance(value, RelativeToRoot):
        return _serialize_marker(value)
    return value


def _serialize_flag(f: Flag) -> dict:
    """Serialize a Flag to a JSON-serializable dict (contract §25).

    Keys are emitted in the canonical order §25.9 pins for a flag entry;
    nothing is sorted at serialization time.
    """
    d: dict = {
        "name": f.name,
        "help": f.help,
        # The value's shape is a real JSON Schema fragment now, and the v1
        # `type` key -- which had three spellings across three
        # implementations -- is gone with `repeatable`, whose fact the shape
        # already carries (§25.2, §25.3).
        "value_schema": _value_schema_fragment(f),
    }
    if f.short is not None:
        d["short"] = f.short
    # Presence is ALWAYS emitted: it is a mandatory declaration, so there is no
    # baseline to omit against (contract §13's presence-round amendment). The
    # requiredness erasure -- a required flag and an optional one serializing
    # identically -- ends here.
    d["presence"] = f.presence
    # `default` is emitted exactly when presence is "default", and then always,
    # whatever the value: [], {}, "", false and 0 are declarations rather than
    # the absence of one, so the omit-when-empty rules are gone.
    if f.presence == _PRESENCE_DEFAULT:
        d["default"] = _serialize_default_value(f.default)
    if f.env is not None:
        d["env"] = f.env
    if f.env_separator is not None:
        d["env_separator"] = f.env_separator
    # Omitted when true, which is the framework's behavior: the key appears
    # exactly on the flags that depart from it (§25.11).
    if not f.prefixed:
        d["prefixed"] = False
    if f.choice_records is not None:
        d["choices"] = _serialize_choice_records(f.choice_records)
    if f.retired_choices:
        d["retired_choices"] = _serialize_retired_choices(f.retired_choices)
    if f.unique is True:
        d["unique"] = True
    # Per-flag conflict mode: serialized only when explicitly set. Absence
    # means "inherit the app default", which v2 publishes as
    # `config_conflict_mode`, so the effective mode is finally computable from
    # the dump alone (§25.11).
    if not isinstance(f.conflict_mode, _MissingSentinel):
        d["conflict_mode"] = f.conflict_mode
    negatable = f.negatable if f.type is bool and f.compound == "scalar" else None
    if negatable is not None:
        d["negatable"] = negatable
    # Emitted only when declared true; absence means the property cannot be
    # cleared, which is the baseline. There is NO second flag entry for the
    # minted `--unset-<prop>`: it is derived from this key exactly as
    # `--no-<x>` is derived from `negatable`, and the dump publishes
    # declarations (§27.9, §13's amendment).
    if f.nullable:
        d["nullable"] = True
    return d


def _serialize_retired_choices(retired: tuple["RetiredChoice", ...]) -> dict:
    """A declaration's retired spellings, as a map from spelling to message.

    SORTED ascending by key -- the treatment a group's `deprecated` map already
    gets, and for the same reason: a keyed object whose declaration order no
    implementation is required to retain has sort order as its only reachable
    canon. The key is the spelling rendered through the error-value formatter,
    so an int, a float and a string key identically in all three
    implementations. The map is omitted entirely when nothing is retired.
    """
    messages = {_format_value_for_error(rc.value): rc.message for rc in retired}
    return {key: messages[key] for key in sorted(messages)}


def _serialize_choice_object(c: "_ChoiceSpec") -> dict:
    """One choice of one selector: `name`, `help`, and its scope (§25.6).

    `flags` is omitted when the scope is empty. A member-spelled choice's
    payload is the FIRST entry of that array, under the reserved name `value`
    with `presence: "required"` -- the payload is supplied by electing the
    member, and required-once-elected is exactly what a member flag's presence
    means. The scope's own declared flags follow it, in declaration order.
    """
    entry: dict = {"name": c.name, "help": c.help}
    scope: list[dict] = []
    if c.payload is not None:
        payload = _serialize_flag(c.payload)
        payload["name"] = _SCOPE_RESERVED_VALUE
        payload["presence"] = _PRESENCE_REQUIRED
        scope.append(payload)
    for m in c.members:
        scope.append(
            _serialize_selector(m) if isinstance(m, _Selector)
            else _serialize_flag(m)
        )
    if scope:
        entry["flags"] = scope
    return entry


def _serialize_selector_default(sel: _Selector) -> dict:
    """A selector's declared default, as the flat map §25.6 pins.

    `{"choice": "<name>", "<field>": <value>, ...}`: the choice's name under
    the reserved key `choice`, followed by each field that has a value in the
    default selection, in declaration order. A field with no value is omitted,
    which is unambiguous because `null` is not a declarable default anywhere in
    the framework.
    """
    spec = sel.choice_by_class(type(sel.default))
    flat: dict = {"choice": spec.name}
    if spec.payload is not None:
        value = getattr(sel.default, _SCOPE_RESERVED_VALUE, None)
        if value is not None:
            flat[_SCOPE_RESERVED_VALUE] = _serialize_default_value(value)
    for m in spec.members:
        if not isinstance(m, Flag):
            continue
        value = getattr(sel.default, _flag_param_name(m.name), None)
        if value is None:
            continue
        flat[m.name] = _serialize_default_value(value)
    return flat


def _serialize_selector(sel: _Selector) -> dict:
    """Serialize one selector, in the encoding §25.6 pins.

    A selector flag has NO `value_schema`, and its absence is the declaration:
    a selector's value is a variant -- one tagged record chosen from several,
    each with a different set of fields -- and the closed four-keyword subset
    cannot express one. Publishing a wrong fragment would be worse than
    publishing none, because a reader would validate against it.

    `elect_by` is the discriminator: an entry carrying it is a selector, and
    its `choices` are choice objects; an entry without it is an ordinary flag,
    and its `choices` (if any) are value records. Each scoped entry is a FULL
    flag entry, which is what makes recursion free -- a nested selector is an
    entry inside a `flags` array carrying its own `choices` and `elect_by`, to
    any depth.
    """
    d: dict = {"name": sel.name, "help": sel.help}
    if sel.short is not None:
        d["short"] = sel.short
    d["presence"] = sel.presence
    if sel.presence == _PRESENCE_DEFAULT:
        d["default"] = _serialize_selector_default(sel)
    if sel.env is not None:
        d["env"] = sel.env
    d["choices"] = [_serialize_choice_object(c) for c in sel.choices]
    d["elect_by"] = sel.elect_by
    return d


def _serialize_arg(a: Arg) -> dict:
    """Serialize an Arg to a JSON-serializable dict (contract §25).

    `variadic` SURVIVES the arity rule that deleted `repeatable`, and the
    asymmetry is deliberate: it names a token-consumption rule -- this arg
    takes every remaining positional token, and only the last arg may -- which
    a consumer needs in order to render `<files>...` in a usage line.
    """
    d: dict = {
        "name": a.name,
        "help": a.help,
        "value_schema": _value_schema_fragment(a),
    }
    # Same always-emitted presence key as a flag entry; the old `required` key
    # is deleted rather than kept beside it (contract §13's amendment).
    d["presence"] = a.presence
    if a.presence == _PRESENCE_DEFAULT:
        d["default"] = _serialize_default_value(a.default)
    if a.variadic:
        d["variadic"] = a.variadic
    if a.choice_records is not None:
        d["choices"] = _serialize_choice_records(a.choice_records)
    if a.retired_choices:
        d["retired_choices"] = _serialize_retired_choices(a.retired_choices)
    return d


def _deep_sorted(value: object) -> object:
    """Recursively sort dict keys, matching Go's map marshaling.

    Used by the framework's own machine payloads so the three implementations
    emit byte-identical documents; a consumer's payload is emitted exactly as
    it was supplied.
    """
    if isinstance(value, dict):
        return {k: _deep_sorted(value[k]) for k in sorted(value)}
    if isinstance(value, list):
        return [_deep_sorted(v) for v in value]
    return value


def _serialize_command(cmd: Command) -> dict:
    """Serialize a Command to a JSON-serializable dict.

    Identity fields (name, help) are always included.
    Other fields are omitted when they match the schema defaults.
    """
    d: dict = {
        "name": cmd.name,
        "help": cmd.help,
        # Always emitted: classification is mandatory, so there is no default
        # to omit against.
        "effect": cmd.effect,
    }
    # Omitted when false: consequential is NOT mandatory, and absence means
    # "not consequential" (contract §8.1, §13).
    if cmd.consequential:
        d["consequential"] = True
    # Emitted only when declared: dry run is supported unless a command says
    # otherwise, so the pair appears exactly on the commands that refuse it.
    if not cmd.dry_run_supported:
        d["dry_run_supported"] = False
        d["dry_run_unsupported_reason"] = cmd.dry_run_unsupported_reason
    # The update declaration, published as TWO command-entry keys where it is
    # one nested record on the declaration surface: a consumer asking what
    # write mode a command has should not have to descend (§27.9, §13's
    # amendment). Omitted together when the command declares no update, and the
    # pair is atomic by construction.
    if cmd.update_of is not None:
        d["update_of"] = _serialize_update_of(cmd.update_of)
        d["write_mode"] = cmd.update_of.write_mode
    # The payload contract, published verbatim (contract §19.5): the inline
    # literal is the sole canonical artifact, so the dump carries it as
    # written rather than a re-rendering of it.
    if cmd.payload_schema is not None:
        d["payload_schema"] = cmd.payload_schema
    # Emitted only when declared true; absence means the framework owns stdout,
    # which is the baseline (contract §13's 2026-08-13 amendment, §19.6).
    if cmd.owns_stdout:
        d["owns_stdout"] = True
    if cmd.passthrough is not None:
        d["passthrough"] = True
    # Flags and selectors share ONE array, interleaved in declaration order: a
    # selector IS a flag (§24.2), and the provisional `selectors` key that kept
    # them apart is replaced by §25.6's encoding, where the presence of
    # `elect_by` is what tells a reader which shape it is holding.
    flags = [
        _serialize_selector(m) if isinstance(m, _Selector)
        else _serialize_flag(m)
        for m in cmd.members
    ]
    if flags:
        d["flags"] = flags
    # The grouping v1 discarded when it merged a set's flags into the command's
    # flag list. Members keep their ordinary entries above, so this adds a
    # grouping without duplicating a declaration (§25.11).
    if cmd.flag_sets:
        d["flag_sets"] = [
            {"name": fs.name, "flags": [f.name for f in fs.flags]}
            for fs in cmd.flag_sets
        ]
    args = [_serialize_arg(a) for a in cmd.args]
    if args:
        d["args"] = args
    tags = sorted(cmd.tags)
    if tags:
        d["tags"] = tags
    # The constraint catalogue (§25.7's co-occurrence-round amendment). Four
    # types, a mandatory `name` on each, and a `members` array of
    # {kind, name, when} records in declaration order. The encoding is COMPLETE
    # rather than indicative: the resolved `kind` is published so a consumer
    # never has to search the flag and arg lists, `when` is always emitted on a
    # flag or arg member and never on a constraint member (which is why it
    # takes no `defaults` block entry), and nesting is encoded as
    # constraint-kind members rather than flattened into leaves.
    constraints: list[dict] = []
    if cmd.constraints:
        kinds, _by_name = _constraint_index(cmd)
        for dep in cmd.constraints:
            if isinstance(dep, _CO_OCCURRENCE_FAMILIES):
                members: list[dict] = []
                for m in dep.members:
                    kind = kinds[m.name]
                    entry: dict = {"kind": kind, "name": m.name}
                    if kind != _MEMBER_KIND_CONSTRAINT:
                        entry["when"] = m.resolved_when
                    members.append(entry)
                constraints.append({
                    "type": (
                        "at_least_one" if isinstance(dep, AtLeastOne)
                        else "all_or_none"
                    ),
                    "name": dep.name,
                    "members": members,
                })
            elif isinstance(dep, Requires):
                constraints.append({
                    "type": "requires",
                    "name": dep.name,
                    "flag": dep.flag,
                    "depends_on": dep.depends_on,
                })
            elif isinstance(dep, Implies):
                constraints.append({
                    "type": "implies",
                    "name": dep.name,
                    "flag": dep.flag,
                    "implies": dep.implies,
                    "value": dep.value,
                })
    if constraints:
        d["constraints"] = constraints
    if cmd.hidden:
        d["hidden"] = True
    if cmd.interactive:
        d["interactive"] = True
    if cmd.config_fields:
        d["config_fields"] = list(cmd.config_fields)
    if cmd.grants:
        d["grants"] = [
            {"name": g.name, "reason": g.reason, "kind": g.kind}
            for g in cmd.grants
        ]
    if cmd.forwarding is not None:
        d["forwarding"] = {"reason": cmd.forwarding.reason}
    return d


def _serialize_group(group: Group) -> dict:
    """Serialize a Group to a JSON-serializable dict (recursive).

    Identity fields (name, help) are always included.
    Other fields are omitted when they match the schema defaults.
    """
    d: dict = {
        "name": group.name,
        "help": group.help,
    }
    commands = {name: _serialize_command(cmd) for name, cmd in group.commands.items()}
    if commands:
        d["commands"] = commands
    groups = {name: _serialize_group(g) for name, g in group._groups.items()}
    if groups:
        d["groups"] = groups
    deprecated = {name: dep.message for name, dep in group.deprecated.items()}
    if deprecated:
        d["deprecated"] = deprecated
    tags = sorted(group.tags)
    if tags:
        d["tags"] = tags
    if group.hidden:
        d["hidden"] = True
    return d


def _build_schema_defaults() -> dict:
    """The machine-readable map of what an OMITTED key means (contract §25.10).

    Keys with no baseline are absent from this block on purpose, and that list
    is exactly the set of always-emitted facts: `name`, `help`, `version`,
    `schema_version`, `project_id`, `effect`, `presence`, `value_schema` on
    every entry that has one, a choice object's `name` and `help`, a choice
    record's `value`, a config field's `help` and `required`, and a check's six
    mandatory fields.

    `default` on a flag or arg has no baseline either: since presence became
    the authority, it is emitted exactly when `presence` is `"default"`, and a
    `null` baseline for it would state something false.

    `value_schema`'s one exception is not an omission at a baseline: a SELECTOR
    carries no fragment at all and its absence IS the declaration, so a
    baseline would have to say what an absent fragment means and every answer
    it could give is false for the one entry that omits the key.
    """
    return {
        "schema_version": 2,
        "app": {
            "env_prefix": None,
            "config": False,
            "config_format": "json",
            "config_path": None,
            "config_conflict_mode": "cli-wins",
            "proc_observe_allowlist": [],
            "global_flags": [],
            "commands": {},
            "groups": {},
            "deprecated": {},
            "tag_contracts": {},
            "checks": {},
            "config_fields": {},
            "infra": {},
        },
        "flag": {
            "short": None,
            "env": None,
            "env_separator": None,
            "prefixed": True,
            "choices": None,
            "elect_by": None,
            "unique": False,
            "conflict_mode": None,
            "negatable": None,
            "nullable": False,
        },
        "arg": {
            "variadic": False,
            "choices": None,
        },
        # The two choice entities, which is what makes this block the complete
        # omission map it is defined to be: a selector choice object's `flags`
        # is omitted when the scope is empty, and a value-flag choice record's
        # `help` is omitted when the entry declares none.
        "choice": {"flags": []},
        "choice_record": {"help": None},
        "command": {
            "consequential": False,
            "dry_run_supported": True,
            "dry_run_unsupported_reason": None,
            "update_of": None,
            "write_mode": None,
            "payload_schema": None,
            "owns_stdout": False,
            "passthrough": False,
            "flags": [],
            "flag_sets": [],
            "args": [],
            "tags": [],
            "constraints": [],
            "hidden": False,
            "interactive": False,
            "config_fields": [],
            "grants": [],
            "forwarding": None,
        },
        "group": {
            "commands": {},
            "groups": {},
            "deprecated": {},
            "tags": [],
            "hidden": False,
        },
        "config_field": {"default": None, "bound_commands": []},
        "check": {"scope": None},
        "infra": {"roots": [], "handshakes": [], "connections": []},
    }


def _read_project_id() -> str:
    """Read project name from pyproject.toml in the current working directory."""
    pyproject_path = Path(os.getcwd()) / "pyproject.toml"
    if not pyproject_path.exists():
        raise RuntimeError(
            "Cannot determine project_id: pyproject.toml not found "
            "or missing [project].name"
        )
    with open(pyproject_path, "rb") as f:
        data = tomllib.load(f)
    project_name = data.get("project", {}).get("name")
    if not project_name:
        raise RuntimeError(
            "Cannot determine project_id: pyproject.toml not found "
            "or missing [project].name"
        )
    return project_name


def _collect_config_field_bindings(
    commands: dict[str, Command],
    bindings: dict[str, list[str]],
    path: list[str],
) -> None:
    """Walk commands and record which commands bind each config field."""
    for cmd in commands.values():
        cmd_path = " ".join(path + [cmd.name])
        for cf_name in cmd.config_fields:
            if cf_name in bindings:
                bindings[cf_name].append(cmd_path)


def _collect_config_field_bindings_from_group(
    group: Group,
    bindings: dict[str, list[str]],
    path: list[str],
) -> None:
    """Recursively walk groups to collect config field bindings."""
    group_path = path + [group.name]
    _collect_config_field_bindings(group.commands, bindings, group_path)
    for sub in group._groups.values():
        _collect_config_field_bindings_from_group(sub, bindings, group_path)


def _dump_schema_core(app: App) -> dict:
    """Build the full schema dict, excluding ``project_id``.

    This is the CWD-free, filesystem-free core of schema production. It reads
    only the in-memory ``App`` (name, version, help, flags, commands, groups,
    etc.). ``project_id`` is added later by the file-writer path, since it is
    the only field that requires reading ``pyproject.toml`` from the CWD.

    Fields whose values match the schema defaults are omitted. The top-level
    ``defaults`` key documents what each missing field means.
    """
    schema: dict = {
        "schema_version": 2,
        "defaults": _build_schema_defaults(),
        "name": app.name,
        "version": app.version,
        "help": app.help,
    }
    if app.env_prefix is not None:
        schema["env_prefix"] = app.env_prefix
    if app.config:
        schema["config"] = app.config
    # The three app-level config keys v1 was blind to. Until v2 an app could
    # relocate every user's config file, or switch it from JSON to TOML, while
    # its dumped schema stayed byte-identical (§25.11).
    if app.config_format != "json":
        schema["config_format"] = app.config_format
    # The DECLARATION, never the resolution: a declared literal path as
    # declared, and a RelativeToRoot in its machine-stable marker shape. The
    # resolved absolute path is a property of the dumping machine.
    declared_config_path = app._config_path_declared
    if declared_config_path is not None:
        schema["config_path"] = _serialize_default_value(declared_config_path)
    if app.config_conflict_mode != "cli-wins":
        schema["config_conflict_mode"] = app.config_conflict_mode
    if app._proc_observe_allowlist:
        schema["proc_observe_allowlist"] = [
            list(prefix) for prefix in app._proc_observe_allowlist
        ]
    global_flags = [_serialize_flag(f) for f in app._global_flags]
    if global_flags:
        schema["global_flags"] = global_flags
    commands = {name: _serialize_command(cmd) for name, cmd in app._commands.items()}
    if commands:
        schema["commands"] = commands
    groups = {name: _serialize_group(grp) for name, grp in app._groups.items()}
    if groups:
        schema["groups"] = groups
    # `deprecated`, `tag_contracts` and `checks` are emitted SORTED ascending
    # by key: no implementation retains a declaration order for all three, and
    # a canon that cannot be produced from what an implementation holds is not
    # a canon (§25.9). Every key here is ASCII by registration rule, so byte,
    # code-point and UTF-16 order coincide.
    deprecated = {
        name: app._deprecated[name].message for name in sorted(app._deprecated)
    }
    if deprecated:
        schema["deprecated"] = deprecated
    if app._tag_contracts:
        schema["tag_contracts"] = {
            k: app._tag_contracts[k] for k in sorted(app._tag_contracts)
        }
    if app._checks_enabled:
        checks_schema: dict = {}
        for name in sorted(app._check_defs):
            # The dumped `checks` block must be a function of the DECLARATION
            # alone. A provider materializes into the same registry lazily and
            # per-cwd, so iterating the whole registry made a dump taken after
            # a check run differ from one taken before it (§25.7). The
            # exclusion is structural here rather than a comment.
            if name in app._provider_sourced_names:
                continue
            cdef = app._check_defs[name]
            entry = {
                "tags": cdef.tags,
                "severity": cdef.severity,
                "fast": cdef.fast,
                "pure": cdef.pure,
                "needs_network": cdef.needs_network,
                "depends_on": cdef.depends_on,
            }
            if cdef.scope:
                entry["scope"] = cdef.scope
            checks_schema[name] = entry
        # Omitted when empty, which is the baseline the `defaults` block
        # states: an app whose only checks are provider-sourced publishes no
        # block at all rather than an empty one.
        if checks_schema:
            schema["checks"] = checks_schema
    if app._config_fields:
        # Build field definitions with bound command info
        cf_schema: dict = {}
        # Collect which commands bind each field
        bindings: dict[str, list[str]] = {
            name: [] for name in app._config_fields
        }
        _collect_config_field_bindings(app._commands, bindings, [])
        for grp in app._groups.values():
            _collect_config_field_bindings_from_group(grp, bindings, [])

        for name, cf in app._config_fields.items():
            # Config fields are scalar-only in every implementation, so the
            # fragment is always a scalar row. `required` STAYS beside it: it
            # is not §23's presence declaration under another name -- a config
            # field has no CLI surface and no three-way declaration, and
            # `required` there means "the config file must contain it".
            entry: dict = {
                "value_schema": _scalar_fragment(cf.type, None),
                "help": cf.help,
                "required": cf.required,
            }
            if not isinstance(cf.default, _MissingSentinel):
                entry["default"] = cf.default
            if bindings.get(name):
                entry["bound_commands"] = bindings[name]
            cf_schema[name] = entry
        schema["config_fields"] = cf_schema
    # infra: only present when roots or handshake vars are declared. Resolved
    # root values are intentionally EXCLUDED -- the schema must be machine-stable
    # (not machine-specific). Only the declared env var and default path (both
    # stable declarations) are emitted for roots.
    if app._infra_root_order or app._handshake_order or app._connection_order:
        infra: dict = {}
        if app._infra_root_order:
            infra["roots"] = [
                {"env_var": ev, "default": app._infra_root_defaults[ev]}
                for ev in app._infra_root_order
            ]
        if app._handshake_order:
            infra["handshakes"] = [
                {"env_var": ev, "help": app._handshake_envs[ev]}
                for ev in app._handshake_order
            ]
        if app._connection_order:
            infra["connections"] = [
                {"env_var": ev, "help": app._connection_envs[ev]}
                for ev in app._connection_order
            ]
        schema["infra"] = infra
    return schema


def _dump_schema(app: App) -> dict:
    """Produce the full schema dict including ``project_id`` (reads the CWD).

    Delegates the bulk of the work to :func:`_dump_schema_core` and inserts
    ``project_id`` immediately after ``defaults`` so the on-disk layout is
    stable and byte-identical to the core dict once ``project_id`` is removed.
    """
    core = _dump_schema_core(app)
    project_id = _read_project_id()
    result: dict = {}
    for key, value in core.items():
        result[key] = value
        if key == "defaults":
            result["project_id"] = project_id
    return result


def _check_schema_project_id(file_path: str, new_project_id: str) -> None:
    """Verify that an existing schema file belongs to the same project.

    Raises RuntimeError on mismatch. Silently passes on: missing file,
    unreadable file, JSON without project_id field, or matching project_id.
    """
    try:
        with open(file_path) as f:
            existing = json.loads(f.read())
    except (OSError, json.JSONDecodeError, ValueError):
        return
    existing_id = existing.get("project_id")
    if existing_id is None:
        return
    if existing_id != new_project_id:
        raise RuntimeError(
            f"Schema mismatch: existing schema belongs to project "
            f"'{existing_id}', not '{new_project_id}'. "
            f"Run from the correct project directory."
        )


def _canonical_json_string(value: str) -> str:
    """One JSON string literal, escaped exactly as the canon mandates.

    `"` and `\\` are escaped, control characters below U+0020 use JSON's short
    escapes where one exists and `\\u00XX` otherwise -- and nothing else is
    escaped. Non-ASCII is raw UTF-8 (``ensure_ascii=False``), the
    HTML-significant characters `<`, `>` and `&` are literal, and `/` is never
    escaped (§25.8).
    """
    return json.dumps(value, ensure_ascii=False)


def _write_canonical_json(value: object, depth: int, out: list[str]) -> None:
    """Append one value's canonical encoding to ``out``."""
    if isinstance(value, dict):
        if not value:
            out.append("{}")
            return
        out.append("{\n")
        inner = "  " * (depth + 1)
        items = list(value.items())
        for i, (key, member) in enumerate(items):
            out.append(inner)
            out.append(_canonical_json_string(str(key)))
            out.append(": ")
            _write_canonical_json(member, depth + 1, out)
            out.append(",\n" if i < len(items) - 1 else "\n")
        out.append("  " * depth + "}")
        return
    if isinstance(value, (list, tuple)):
        if not value:
            out.append("[]")
            return
        out.append("[\n")
        inner = "  " * (depth + 1)
        for i, element in enumerate(value):
            out.append(inner)
            _write_canonical_json(element, depth + 1, out)
            out.append(",\n" if i < len(value) - 1 else "\n")
        out.append("  " * depth + "]")
        return
    if value is None:
        out.append("null")
        return
    if isinstance(value, bool):
        out.append("true" if value else "false")
        return
    if isinstance(value, int):
        # Bare integer token: no decimal point, no exponent, no separators.
        out.append(str(value))
        return
    if isinstance(value, float):
        # Every float goes through the canonical float form the repo already
        # owns, the same one the three implementations share byte-for-byte.
        # `json.dumps` would render it through `repr`, which differs from SCF
        # on a zero-padded exponent (`1e-07` against `1e-7`).
        out.append(_format_float_canonical(value))
        return
    if isinstance(value, str):
        out.append(_canonical_json_string(value))
        return
    raise TypeError(f"schema value of unserializable type: {type(value).__name__}")


def _canonical_json(value: object) -> str:
    """The dumper-independent encoding of a whole schema document (§25.8).

    Two-space indent, one member or element per line, `": "` between a key and
    its value, empty containers inline. A repository whose schema file is
    written sometimes by one implementation and sometimes by another must see a
    diff exactly when something changed.
    """
    out: list[str] = []
    _write_canonical_json(value, 0, out)
    return "".join(out)


def _write_schema(app: App) -> str:
    """Write the schema to the app's declared location and return the path.

    The location is decided once, at App construction (``App.schema_path``, or
    the framework's ``.strictcli/schema.json`` anchored at the construction-time
    cwd) -- never at the caller's working directory at dump time.
    """
    schema = _dump_schema(app)
    file_path = app._schema_out_path
    dir_path = os.path.dirname(file_path)
    if dir_path:
        os.makedirs(dir_path, exist_ok=True)
    _check_schema_project_id(file_path, schema["project_id"])
    # Exactly one trailing newline at end of file.
    with open(file_path, "w", encoding="utf-8") as f:
        f.write(_canonical_json(schema) + "\n")
    app._record_cache_write(file_path)
    return file_path


# MCP server (--mcp)
#
# The server speaks two eras (effects contract §22):
#
#   MODERN (2026-07-28) -- stateless. There is no handshake: every request
#   carries its protocol version and the client's capabilities in `_meta`,
#   every result carries a `resultType`, and `server/discover` advertises the
#   supported versions, the capabilities and the server identity that the
#   handshake used to carry.
#
#   LEGACY (2025-11-25) -- the `initialize` handshake, the newest of the
#   handshake-based revisions. It is selected by an `initialize` request and
#   scoped to this process, which is exactly the dual-era rule the modern
#   revision specifies for a server serving both. That era has no
#   input-required result, so the same confirmation is delivered as a
#   server-initiated `elicitation/create` request (contract §22.7).
#
# A request that carries neither the modern metadata nor a preceding
# `initialize` is malformed and is refused. Nothing is inferred.

_MCP_PROTOCOL_VERSION = "2026-07-28"
_MCP_LEGACY_PROTOCOL_VERSION = "2025-11-25"

# The reserved `_meta` keys of the modern revision.
_MCP_META_PROTOCOL_VERSION = "io.modelcontextprotocol/protocolVersion"
_MCP_META_CLIENT_CAPABILITIES = "io.modelcontextprotocol/clientCapabilities"
_MCP_META_CLIENT_INFO = "io.modelcontextprotocol/clientInfo"
_MCP_META_SERVER_INFO = "io.modelcontextprotocol/serverInfo"
_MCP_META_LOG_LEVEL = "io.modelcontextprotocol/logLevel"
_MCP_META_SUBSCRIPTION_ID = "io.modelcontextprotocol/subscriptionId"

# A key under a prefix the protocol reserves for itself is either one this
# revision defines or one this server does not speak; the second is refused
# rather than ignored.
_MCP_RECOGNIZED_RESERVED_META_KEYS = frozenset({
    _MCP_META_PROTOCOL_VERSION,
    _MCP_META_CLIENT_CAPABILITIES,
    _MCP_META_CLIENT_INFO,
    _MCP_META_LOG_LEVEL,
    _MCP_META_SUBSCRIPTION_ID,
})

# The named feature the server declares (campaign decision 26). A NAME, never a
# version number: a new name appears only if the confirmation dance changes
# incompatibly.
_MCP_FEATURE_CONSEQUENTIAL_CONFIRMATION = (
    "dev.smmh.strictcli/consequential-confirmation"
)

# Cacheability of the list surfaces. The tool list is derived from the app's
# static command registration, so it cannot vary per client (public) and cannot
# change while the process runs.
_MCP_CACHE_TTL_MS = 3600000
_MCP_CACHE_SCOPE = "public"

# JSON-RPC and MCP error codes. -32020 (HeaderMismatch) belongs to the HTTP
# transport, which this server does not speak; -32021
# (MissingRequiredClientCapability) is what a consequential call from a client
# that cannot render the confirmation is answered with.
_MCP_ERR_PARSE = -32700
_MCP_ERR_METHOD_NOT_FOUND = -32601
_MCP_ERR_INVALID_PARAMS = -32602
_MCP_ERR_INTERNAL = -32603
_MCP_ERR_MISSING_CLIENT_CAPABILITY = -32021
_MCP_ERR_UNSUPPORTED_PROTOCOL_VERSION = -32022

# The revision forbids a server sending an input request the client never said
# it could fulfil, and assigns -32021 for saying so. `data.requiredCapabilities`
# is a client-capabilities object, and this server names FORM mode: a client
# that declared only URL-mode elicitation cannot render this question either.
_MCP_MSG_MISSING_ELICITATION = (
    "Server requires the elicitation capability for this request"
)
_MCP_REQUIRED_ELICITATION_CAPABILITIES = {"elicitation": {"form": {}}}

_MCP_META_PREFIX_LABEL_RE = re.compile(r"^[A-Za-z](?:[A-Za-z0-9-]*[A-Za-z0-9])?$")
_MCP_META_NAME_RE = re.compile(r"^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$")


def _mcp_meta_key_valid(key: str) -> bool:
    """True when `key` matches the protocol's `_meta` key-name grammar.

    A key is an optional dot-separated prefix, a slash, and a name. The name may
    be empty; when it is not, it begins and ends alphanumeric and may carry
    hyphens, underscores and dots in between.
    """
    parts = key.split("/")
    if len(parts) == 1:
        name = parts[0]
    elif len(parts) == 2:
        for label in parts[0].split("."):
            if not _MCP_META_PREFIX_LABEL_RE.match(label):
                return False
        name = parts[1]
    else:
        return False
    return name == "" or bool(_MCP_META_NAME_RE.match(name))


def _mcp_meta_key_reserved(key: str) -> bool:
    """True when `key` sits under a prefix the protocol reserves for itself.

    Any prefix whose SECOND label is `modelcontextprotocol` or `mcp` is
    reserved -- so `io.modelcontextprotocol/` and `com.mcp.tools/` are, and
    `com.example.mcp/` is not.
    """
    parts = key.split("/")
    if len(parts) != 2:
        return False
    labels = parts[0].split(".")
    return len(labels) >= 2 and labels[1] in ("modelcontextprotocol", "mcp")


def _mcp_validate_meta(meta: dict) -> str | None:
    """Validate one request's `_meta` block; return the refusal, or None.

    Called only once the block is known to carry the protocol version, which is
    what selects the modern era in the first place.
    """
    # Sorted, not in the document's own order: a request carrying more than one
    # offending key must be refused by naming the SAME key in all three
    # implementations (§22.2), and Go has to sort because its map iteration is
    # randomized. Python's sort is by code point, which is UTF-8 byte order --
    # the order Go's sort.Strings gives.
    for key in sorted(meta):
        if not _mcp_meta_key_valid(key):
            return f"invalid _meta key name: '{key}'"
        if (
            _mcp_meta_key_reserved(key)
            and key not in _MCP_RECOGNIZED_RESERVED_META_KEYS
        ):
            return f"unrecognized reserved _meta key: '{key}'"
    if not isinstance(meta[_MCP_META_PROTOCOL_VERSION], str):
        return f"_meta['{_MCP_META_PROTOCOL_VERSION}'] must be a string"
    if _MCP_META_CLIENT_CAPABILITIES not in meta:
        return (
            "missing required request metadata: "
            f"_meta['{_MCP_META_CLIENT_CAPABILITIES}']"
        )
    if not isinstance(meta[_MCP_META_CLIENT_CAPABILITIES], dict):
        return f"_meta['{_MCP_META_CLIENT_CAPABILITIES}'] must be an object"
    if _MCP_META_CLIENT_INFO in meta and not isinstance(
        meta[_MCP_META_CLIENT_INFO], dict,
    ):
        return f"_meta['{_MCP_META_CLIENT_INFO}'] must be an object"
    return None


def _mcp_server_info(app: App) -> dict:
    """The identity every modern result carries in its own `_meta`."""
    return {_MCP_META_SERVER_INFO: {"name": app.name, "version": app.version}}


def _mcp_complete_result(app: App, req_id: object, body: dict) -> dict:
    """Wrap a modern result body: `resultType` in front, server identity behind."""
    result: dict = {"resultType": "complete"}
    result.update(body)
    result["_meta"] = _mcp_server_info(app)
    return {"jsonrpc": "2.0", "id": req_id, "result": result}


# --- The continuation primitive (contract §22.4) ----------------------------
#
# The protocol is stateless: a server that needs more input answers with an
# input-required result and whatever it must remember, and the client echoes
# that back on a retry that is otherwise a fresh, independent request. The
# state therefore travels THROUGH the client, which makes it attacker-
# controlled input rather than server memory.

#: How long a minted continuation stays usable. Long enough for a human to
#: answer the confirmation the client renders, short enough that a captured
#: blob is worth little.
_MCP_CONTINUATION_TTL_SECONDS = 300

#: The key the confirmation elicitation is filed under, in both directions.
_MCP_CONFIRMATION_KEY = "consequential-confirmation"


#: The base64url alphabet, in value order -- the one spelling §22.4 defines.
_B64URL_ALPHABET = (
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
)
_B64URL_VALUES = {char: value for value, char in enumerate(_B64URL_ALPHABET)}


def _b64url(raw: bytes) -> str:
    """Unpadded base64url -- the blob is a URL-safe opaque token."""
    return base64.urlsafe_b64encode(raw).decode("ascii").rstrip("=")


def _b64url_canonical(text: str) -> bool:
    """True when `text` is the ONE unpadded base64url spelling of some bytes.

    The three languages' decoders do not agree on their own -- Python's ignores
    stray characters and accepts padding, Node's ignores anything outside the
    alphabet, and Go's skips newlines -- and all three ignore the trailing bits
    of a final character that does not fill a byte, so a blob's last character
    can be altered without changing what it decodes to. The blob is
    attacker-controlled input, so the spelling is checked here, before any
    decoder sees it, identically in all three implementations.
    """
    remainder = len(text) % 4
    if remainder == 1:
        # No byte string encodes to a length one past a multiple of four.
        return False
    for char in text:
        if char not in _B64URL_VALUES:
            return False
    if remainder == 0:
        return True
    # Two characters carry one byte and three carry two, so the final character
    # has 4 or 2 bits left over, and a canonical encoder leaves them zero.
    leftover = 0b1111 if remainder == 2 else 0b11
    return not _B64URL_VALUES[text[-1]] & leftover


def _b64url_decode(text: str) -> bytes | None:
    """Decode canonical unpadded base64url, or None when the text is not one."""
    if not _b64url_canonical(text):
        return None
    padding = "=" * (-len(text) % 4)
    try:
        return base64.urlsafe_b64decode(text + padding)
    except (ValueError, binascii.Error):
        return None


def _mcp_canonical_json(value: object) -> str:
    """A canonical encoding of a JSON value, for digesting.

    Keys are sorted, there is no insignificant whitespace, and floats take the
    framework's canonical form -- so the digest depends on what the caller
    said, never on how their encoder spelled it.
    """
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, float):
        return _format_float_canonical(value)
    if isinstance(value, int):
        return str(value)
    if isinstance(value, str):
        return json.dumps(value, ensure_ascii=False)
    if isinstance(value, list):
        return "[" + ",".join(_mcp_canonical_json(v) for v in value) + "]"
    if isinstance(value, dict):
        parts = [
            json.dumps(str(k), ensure_ascii=False) + ":" + _mcp_canonical_json(v)
            for k, v in sorted(value.items(), key=lambda kv: str(kv[0]))
        ]
        return "{" + ",".join(parts) + "}"
    return json.dumps(str(value), ensure_ascii=False)


def _mcp_request_digest(method: str, tool_name: str, arguments: dict) -> str:
    """Digest the originating request: the method and its salient parameters."""
    material = "\n".join([method, tool_name, _mcp_canonical_json(arguments)])
    return _b64url(hashlib.sha256(material.encode("utf-8")).digest())


def _mcp_principal(meta: dict) -> str:
    """The principal a modern request declares, from its own metadata."""
    return _mcp_principal_of(meta.get(_MCP_META_CLIENT_INFO))


def _mcp_principal_of(info: object) -> str:
    """The client a state is minted for, as the client declares itself.

    On this transport there is no authenticated principal; the declaration is
    self-reported and the binding is a consistency check, not authentication.
    What actually contains a stolen blob is the per-process key. Both eras
    carry the same self-report -- per request, or once at the handshake.
    """
    if not isinstance(info, dict):
        return ""
    name = info.get("name")
    version = info.get("version")
    return "{}/{}".format(
        name if isinstance(name, str) else "",
        version if isinstance(version, str) else "",
    )


class _MCPContinuation:
    """Mints and verifies the integrity-protected continuation state blob.

    The blob is `<payload>.<mac>`, both unpadded base64url, where the MAC is
    HMAC-SHA256 over the payload bytes under a key minted for this process and
    never emitted. A blob is therefore unforgeable without reading this
    process's memory, and worthless to any other process.

    The payload binds three things the protocol requires be checked on receipt
    -- the principal it was issued to, an expiry, and a digest of the
    originating request -- plus a unique id, which is what makes single use
    enforceable: those three bound the replay window but do not close it.
    """

    def __init__(self) -> None:
        self._key = os.urandom(32)
        self._consumed: dict[str, int] = {}

    def _mac(self, payload: bytes) -> bytes:
        return hmac.new(self._key, payload, hashlib.sha256).digest()

    def mint(self, principal: str, digest: str, now: float | None = None) -> str:
        moment = time.time() if now is None else now
        payload = {
            "v": 1,
            "jti": _b64url(os.urandom(16)),
            "prin": principal,
            "exp": int(moment) + _MCP_CONTINUATION_TTL_SECONDS,
            "req": digest,
        }
        raw = json.dumps(
            payload, separators=(",", ":"), ensure_ascii=False,
        ).encode("utf-8")
        return f"{_b64url(raw)}.{_b64url(self._mac(raw))}"

    def verify(
        self,
        blob: str,
        principal: str,
        digest: str,
        now: float | None = None,
    ) -> str | None:
        """Verify and CONSUME a blob; return None when good, else the refusal.

        A blob that passes every check is consumed here, so a second
        presentation of the same blob is refused even though it is still
        perfectly well-formed, unexpired and correctly bound.
        """
        moment = int(time.time() if now is None else now)
        parts = blob.split(".")
        if len(parts) != 2:
            return "requestState failed verification"
        raw = _b64url_decode(parts[0])
        mac = _b64url_decode(parts[1])
        if raw is None or mac is None:
            return "requestState failed verification"
        if not hmac.compare_digest(mac, self._mac(raw)):
            return "requestState failed verification"
        try:
            payload = json.loads(raw.decode("utf-8"))
        except (ValueError, UnicodeDecodeError):
            return "requestState failed verification"
        if not isinstance(payload, dict) or payload.get("v") != 1:
            return "requestState failed verification"
        jti = payload.get("jti")
        if not isinstance(jti, str):
            return "requestState failed verification"
        expiry = payload.get("exp")
        if not isinstance(expiry, int) or isinstance(expiry, bool):
            return "requestState failed verification"
        self._prune(moment)
        if expiry <= moment:
            return "requestState has expired"
        if payload.get("prin") != principal:
            return "requestState was issued to a different client"
        if payload.get("req") != digest:
            return "requestState does not match this request"
        if jti in self._consumed:
            return "requestState has already been used"
        self._consumed[jti] = expiry
        return None

    def _prune(self, moment: int) -> None:
        """Forget consumed ids that can no longer be replayed anyway."""
        for jti in [j for j, exp in self._consumed.items() if exp <= moment]:
            del self._consumed[jti]


def _mcp_collect_commands(app: App) -> dict[str, tuple[Command, str]]:
    """Collect non-hidden, non-interactive leaf commands as {dotted_path: (cmd, help)}.

    Returns a dict mapping dotted command paths to (Command, help_text) tuples.
    """
    commands: dict[str, tuple[Command, str]] = {}

    for name, cmd in app._commands.items():
        if cmd.hidden or cmd.interactive:
            continue
        commands[name] = (cmd, cmd.help)

    def _collect_from_group(
        group: Group, path: list[str],
    ) -> None:
        if group.hidden:
            return
        for cmd_name, cmd in group.commands.items():
            if cmd.hidden or cmd.interactive:
                continue
            dotted = ".".join(path + [cmd_name])
            commands[dotted] = (cmd, cmd.help)
        for sub_name, sub_group in group._groups.items():
            _collect_from_group(sub_group, path + [sub_name])

    for group_name, group in app._groups.items():
        _collect_from_group(group, [group_name])

    return commands


def _mcp_jsonrpc_error(
    req_id: object, code: int, message: str, data: object = None,
) -> dict:
    """Build a JSON-RPC 2.0 error response."""
    error: dict = {"code": code, "message": message}
    if data is not None:
        error["data"] = data
    return {
        "jsonrpc": "2.0",
        "id": req_id,
        "error": error,
    }


class _MCPLegacySession:
    """What the legacy handshake establishes, scoped to this process.

    In that era the session IS the client's declaration, exactly as the
    per-request `_meta` block is in the modern one: the capabilities and the
    identity arrive once, at `initialize`, and every later request is read
    against them.
    """

    def __init__(self) -> None:
        self.active = False
        self.capabilities: dict = {}
        self.client_info: dict = {}

    def open(self, params: dict) -> None:
        self.active = True
        caps = params.get("capabilities")
        self.capabilities = caps if isinstance(caps, dict) else {}
        info = params.get("clientInfo")
        self.client_info = info if isinstance(info, dict) else {}


class _MCPChannel:
    """The line channel to the client, in both directions.

    The legacy confirmation is a request the SERVER sends, so the loop has to
    be able to write one and read its answer in the middle of serving a call.
    Anything else that arrives while an answer is awaited is held here and
    served afterwards -- the loop stays one request at a time, and no client
    line is dropped.
    """

    def __init__(self, inp: object, out: object) -> None:
        self._lines = iter(inp)  # type: ignore[call-overload]
        self._out = out
        self._held: deque = deque()

    def next_line(self) -> str | None:
        """The next line to serve: what was held first, then the stream."""
        if self._held:
            return self._held.popleft()
        return next(self._lines, None)

    def write(self, message: dict) -> None:
        self._out.write(json.dumps(message) + "\n")  # type: ignore[attr-defined]
        self._out.flush()  # type: ignore[attr-defined]

    def await_response(self, req_id: str) -> dict | None:
        """Read until the response to `req_id` arrives, or the stream ends.

        A response carrying another id answers nothing this server sent and is
        discarded; anything else is held for the main loop.
        """
        for raw in self._lines:
            text = raw.strip()
            if not text:
                continue
            try:
                msg = json.loads(text)
            except json.JSONDecodeError:
                self._held.append(raw)
                continue
            if isinstance(msg, dict) and ("result" in msg or "error" in msg):
                if msg.get("id") == req_id:
                    return msg
                continue
            self._held.append(raw)
        return None


def _mcp_handle_initialize(
    app: App, req_id: object, params: dict, session: _MCPLegacySession,
) -> dict:
    """Handle the legacy-era 'initialize' handshake, and select that era.

    The modern revision has no handshake; this method is what a legacy client
    opens with, and answering it is what puts this process into legacy
    semantics for every later request that carries no modern metadata. This
    server speaks one legacy revision, so it always answers with that one --
    the negotiation rule says to answer with the latest version supported.

    The declared feature is advertised here too, under the key that revision
    gives a non-standard server capability: one name, two advertisements.
    """
    session.open(params)
    return {
        "jsonrpc": "2.0",
        "id": req_id,
        "result": {
            "protocolVersion": _MCP_LEGACY_PROTOCOL_VERSION,
            "capabilities": {
                "tools": {},
                "experimental": {
                    _MCP_FEATURE_CONSEQUENTIAL_CONFIRMATION: {},
                },
            },
            "serverInfo": {
                "name": app.name,
                "version": app.version,
            },
        },
    }


def _mcp_handle_discover(app: App, req_id: object) -> dict:
    """Handle 'server/discover' -- the modern era's mandatory discovery call.

    It replaces the handshake: supported versions, capabilities and identity in
    one request. The declared feature is a NAME rather than a version number,
    so a client learns that this server runs the confirmation dance without
    having to infer it from a revision date.
    """
    return _mcp_complete_result(app, req_id, {
        "supportedVersions": [_MCP_PROTOCOL_VERSION],
        "capabilities": {
            "tools": {},
            "extensions": {_MCP_FEATURE_CONSEQUENTIAL_CONFIRMATION: {}},
        },
        "instructions": app.help,
        "ttlMs": _MCP_CACHE_TTL_MS,
        "cacheScope": _MCP_CACHE_SCOPE,
    })


def _mcp_handle_tools_list(
    app: App,
    commands: dict[str, tuple[Command, str]],
    req_id: object,
    *,
    modern: bool,
) -> dict:
    """Handle the MCP 'tools/list' request."""
    tools = []
    for dotted_path, (cmd, help_text) in commands.items():
        # The classification sits BESIDE inputSchema, never inside it: it
        # describes the tool, not an argument the caller passes. Same emission
        # rule as the schema dump -- `effect` always, `consequential` only when
        # true (absence means "not consequential").
        entry: dict = {
            "name": dotted_path,
            "description": _tool_description(cmd, help_text),
            "effect": cmd.effect,
            "inputSchema": _build_json_schema(cmd),
        }
        if cmd.consequential:
            entry["consequential"] = True
        tools.append(entry)
    if not modern:
        return {
            "jsonrpc": "2.0",
            "id": req_id,
            "result": {"tools": tools},
        }
    return _mcp_complete_result(app, req_id, {
        "tools": tools,
        "ttlMs": _MCP_CACHE_TTL_MS,
        "cacheScope": _MCP_CACHE_SCOPE,
    })


def _mcp_tool_result(
    app: App, req_id: object, text: str, *, modern: bool, is_error: bool = False,
) -> dict:
    """Build a tool result -- the one place the two eras' shapes differ."""
    body: dict = {"content": [{"type": "text", "text": text}]}
    if is_error:
        body["isError"] = True
    if not modern:
        return {"jsonrpc": "2.0", "id": req_id, "result": body}
    return _mcp_complete_result(app, req_id, body)


def _mcp_client_declares_elicitation(meta: dict) -> bool:
    """True when a modern request's client declared a form elicitation."""
    return _mcp_declares_form_elicitation(
        meta.get(_MCP_META_CLIENT_CAPABILITIES),
    )


def _mcp_declares_form_elicitation(caps: object) -> bool:
    """True when a capabilities block says the client can render a form.

    An empty `elicitation` object means form mode, which the protocol states
    for compatibility with clients written before the modes existed. The two
    eras deliver their capabilities differently -- per request, or once at the
    handshake -- and read them the same way.
    """
    if not isinstance(caps, dict):
        return False
    elicitation = caps.get("elicitation")
    if not isinstance(elicitation, dict):
        return False
    if not elicitation:
        return True
    return isinstance(elicitation.get("form"), dict)


def _mcp_confirmation_request(cmd_path: str) -> dict:
    """The elicitation that asks a human, through the client, to confirm.

    Same words as the terminal prompt (§12.6) minus its keystroke hint: one
    vocabulary for one question, however it is delivered.
    """
    return {
        "method": "elicitation/create",
        "params": {
            "mode": "form",
            "message": f"about to run consequential command '{cmd_path}'. Proceed?",
            "requestedSchema": {
                "type": "object",
                "properties": {
                    "proceed": {
                        "type": "boolean",
                        "title": "Proceed",
                        "description": "Whether to run the consequential command.",
                    },
                },
                "required": ["proceed"],
            },
        },
    }


def _mcp_input_required(
    app: App, req_id: object, cmd_path: str, request_state: str,
) -> dict:
    """The interim result: what is needed, and the state to echo back with it."""
    return {
        "jsonrpc": "2.0",
        "id": req_id,
        "result": {
            "resultType": "input_required",
            "inputRequests": {
                _MCP_CONFIRMATION_KEY: _mcp_confirmation_request(cmd_path),
            },
            "requestState": request_state,
            "_meta": _mcp_server_info(app),
        },
    }


def _mcp_confirmation_verdict(answer: object) -> str:
    """Read one elicitation result: 'accept', 'reject', 'absent' or 'malformed'.

    An `accept` carrying `proceed: false` is a refusal, not an approval: the
    action names what the client did with the dialogue, and the field is the
    answer to the question.
    """
    if answer is None:
        return "absent"
    if not isinstance(answer, dict):
        return "malformed"
    action = answer.get("action")
    if action in ("decline", "cancel"):
        return "reject"
    if action != "accept":
        return "malformed"
    content = answer.get("content")
    if not isinstance(content, dict):
        return "malformed"
    return "accept" if content.get("proceed") is True else "reject"


def _mcp_confirmation_exchange(
    app: App,
    req_id: object,
    params: dict,
    tool_name: str,
    arguments: dict,
    *,
    commands: dict[str, tuple[Command, str]],
    meta: dict,
    continuation: _MCPContinuation | None,
    already_consented: bool,
) -> dict | bool:
    """Run the confirmation round-trip for one call.

    Returns a response to send back (a refusal, or the interim result asking
    for confirmation), or the consent the call may proceed with.
    """
    principal = _mcp_principal(meta)
    digest = _mcp_request_digest("tools/call", tool_name, arguments)
    consented = already_consented

    if "requestState" in params:
        state = params["requestState"]
        if not isinstance(state, str):
            return _mcp_jsonrpc_error(
                req_id, _MCP_ERR_INVALID_PARAMS,
                "parameter 'requestState' must be a string",
            )
        responses = params.get("inputResponses", {})
        if not isinstance(responses, dict):
            return _mcp_jsonrpc_error(
                req_id, _MCP_ERR_INVALID_PARAMS,
                "parameter 'inputResponses' must be an object",
            )
        if continuation is None:
            return _mcp_jsonrpc_error(
                req_id, _MCP_ERR_INVALID_PARAMS, "requestState failed verification",
            )
        refusal = continuation.verify(state, principal, digest)
        if refusal is not None:
            return _mcp_jsonrpc_error(req_id, _MCP_ERR_INVALID_PARAMS, refusal)
        verdict = _mcp_confirmation_verdict(responses.get(_MCP_CONFIRMATION_KEY))
        if verdict == "malformed":
            return _mcp_jsonrpc_error(
                req_id, _MCP_ERR_INVALID_PARAMS,
                f"inputResponses['{_MCP_CONFIRMATION_KEY}'] is not an "
                f"elicitation result",
            )
        if verdict == "reject":
            return _mcp_tool_result(
                app, req_id, _msg_confirm_declined(), modern=True, is_error=True,
            )
        if verdict == "accept":
            consented = True
        else:
            # The state was good but the answer never came. The protocol says
            # to ask again rather than error -- with a fresh state, since the
            # one just presented is spent.
            return _mcp_input_required(
                app, req_id, tool_name, continuation.mint(principal, digest),
            )
    elif "inputResponses" in params:
        # An answer whose state is missing cannot be verified, and an
        # unverifiable answer is not an answer.
        return _mcp_jsonrpc_error(
            req_id, _MCP_ERR_INVALID_PARAMS,
            "parameter 'inputResponses' requires the requestState it was "
            "issued with",
        )

    if consented:
        return True
    entry = commands.get(tool_name)
    if entry is None or not entry[0].consequential:
        return consented
    if continuation is not None and _mcp_client_declares_elicitation(meta):
        return _mcp_input_required(
            app, req_id, tool_name, continuation.mint(principal, digest),
        )
    # A client that cannot render the confirmation is told what it would have
    # to declare, in the code the revision assigns -- never how to proceed
    # without confirming.
    return _mcp_jsonrpc_error(
        req_id, _MCP_ERR_MISSING_CLIENT_CAPABILITY, _MCP_MSG_MISSING_ELICITATION,
        {"requiredCapabilities": dict(_MCP_REQUIRED_ELICITATION_CAPABILITIES)},
    )


def _mcp_legacy_confirmation(
    tool_name: str,
    arguments: dict,
    *,
    commands: dict[str, tuple[Command, str]],
    session: _MCPLegacySession,
    continuation: _MCPContinuation,
    channel: _MCPChannel,
) -> bool | None:
    """Ask a legacy client to confirm, over a request the server sends.

    Returns True when the client accepted, False when the exchange aborted,
    and None when there was nothing to ask -- either the command is not
    consequential, or this client never declared it could render the form, in
    which case the call reaches the consent seam unconsented and gets its
    refusal.

    The continuation blob rides as the JSON-RPC request id: JSON-RPC obliges
    the client to echo an id back verbatim, which is the same obligation the
    modern era puts on `requestState`, so the correlation needs no second
    mechanism. It is verified on return through the same path, and a matching
    id that fails any of those checks confirms nothing.
    """
    entry = commands.get(tool_name)
    if entry is None or not entry[0].consequential:
        return None
    if not _mcp_declares_form_elicitation(session.capabilities):
        return None
    principal = _mcp_principal_of(session.client_info)
    digest = _mcp_request_digest("tools/call", tool_name, arguments)
    state = continuation.mint(principal, digest)
    channel.write({
        "jsonrpc": "2.0", "id": state, **_mcp_confirmation_request(tool_name),
    })
    answer = channel.await_response(state)
    # Consumption is unconditional once the blob is on the wire: EVERY exit
    # below spends it, not just the one that reads a well-formed result. A blob
    # an aborted exchange left live is still bound to this principal and this
    # request digest for its whole five minutes, which is a `requestState` the
    # client can present on the modern path with an acceptance it wrote itself
    # -- for the very call the abort refused.
    verified = continuation.verify(state, principal, digest) is None
    if answer is None or "result" not in answer:
        # A JSON-RPC error, or a stream that ended before an answer arrived.
        # There is no re-ask in this era: the server is holding the request
        # open, so a non-answer is a decision.
        return False
    if not verified:
        return False
    return _mcp_confirmation_verdict(answer["result"]) == "accept"


def _mcp_handle_tools_call(
    app: App,
    req_id: object,
    params: dict,
    *,
    modern: bool,
    commands: dict[str, tuple[Command, str]] | None = None,
    meta: dict | None = None,
    continuation: _MCPContinuation | None = None,
    session: _MCPLegacySession | None = None,
    channel: _MCPChannel | None = None,
) -> dict:
    """Handle the MCP 'tools/call' request."""
    if "name" not in params:
        return _mcp_jsonrpc_error(
            req_id, -32602, "missing required parameter: name",
        )
    tool_name = params["name"]
    if not isinstance(tool_name, str):
        return _mcp_jsonrpc_error(
            req_id, -32602, "parameter 'name' must be a string",
        )

    # Unknown tools are NOT a -32602 protocol error: like Go, the name is
    # passed to app.call(), whose invocation error surfaces as tool-result
    # error content (isError) below.
    arguments = params.get("arguments", {})
    if not isinstance(arguments, dict):
        return _mcp_jsonrpc_error(
            req_id, -32602, "parameter 'arguments' must be an object",
        )

    # Consent is a top-level param, a sibling of `name` and `arguments` --
    # never a member of `arguments`, which is the command's own argument
    # namespace and is published with additionalProperties: false. There is no
    # server-side default: absent means "not consented", and a consequential
    # tool is then refused.
    approve_consequential = params.get("approve_consequential", False)
    if not isinstance(approve_consequential, bool):
        return _mcp_jsonrpc_error(
            req_id, -32602,
            "parameter 'approve_consequential' must be a boolean",
        )

    if modern:
        outcome_or_consent = _mcp_confirmation_exchange(
            app, req_id, params, tool_name, arguments,
            commands=commands or {},
            meta=meta or {},
            continuation=continuation,
            already_consented=approve_consequential,
        )
        if isinstance(outcome_or_consent, dict):
            return outcome_or_consent
        approve_consequential = outcome_or_consent
    elif (
        not approve_consequential
        and session is not None
        and continuation is not None
        and channel is not None
    ):
        answered = _mcp_legacy_confirmation(
            tool_name, arguments,
            commands=commands or {},
            session=session,
            continuation=continuation,
            channel=channel,
        )
        if answered is False:
            return _mcp_tool_result(
                app, req_id, _msg_confirm_declined(), modern=False,
                is_error=True,
            )
        if answered is True:
            approve_consequential = True

    try:
        result = app._call_with_kwargs(
            tool_name, dict(arguments),
            approve_consequential=approve_consequential, flat=True,
        )
    except InvokeError as e:
        return _mcp_tool_result(app, req_id, str(e), modern=modern, is_error=True)
    except Exception as e:
        return _mcp_tool_result(app, req_id, str(e), modern=modern, is_error=True)

    return _mcp_tool_result(
        app, req_id, json.dumps(result, default=str), modern=modern,
    )


def _run_mcp_server(
    app: App,
    *,
    input: io.TextIOBase | None = None,
    output: io.TextIOBase | None = None,
) -> None:
    """Run the MCP JSON-RPC 2.0 server loop.

    Reads one JSON object per line from input, writes responses to output.
    Notifications (no 'id' field) get no response.
    """
    inp = input if input is not None else sys.stdin
    out = output if output is not None else sys.stdout

    commands = _mcp_collect_commands(app)
    # The one piece of connection state a dual-era server is allowed: an
    # `initialize` request selects legacy semantics for this process and
    # carries that client's declaration. Modern requests carry everything they
    # need and never consult it.
    session = _MCPLegacySession()
    # The continuation minting key and the spent-id set. Both are per process:
    # a blob is unforgeable outside this process and unusable twice inside it.
    continuation = _MCPContinuation()
    channel = _MCPChannel(inp, out)

    while True:
        raw = channel.next_line()
        if raw is None:
            break
        line = raw.strip()
        if not line:
            continue

        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            # Malformed JSON -- send parse error if we can
            channel.write(_mcp_jsonrpc_error(None, -32700, "Parse error"))
            continue

        # A non-object JSON value is a parse error, matching Go (which
        # unmarshals directly into a struct). The guard is retained -- deleting
        # it would crash on the msg.get(...) calls below -- but it now redirects
        # to the same -32700 "Parse error" response instead of -32600.
        if not isinstance(msg, dict):
            channel.write(_mcp_jsonrpc_error(None, -32700, "Parse error"))
            continue

        req_id = msg.get("id")
        method = msg.get("method", "")
        params = msg.get("params", {})

        # Notifications have no 'id' -- don't send a response. Neither does a
        # response the client sent when this server asked for none.
        if "id" not in msg or "result" in msg or "error" in msg:
            continue

        if not isinstance(params, dict):
            params = {}

        if method == "initialize":
            channel.write(_mcp_handle_initialize(app, req_id, params, session))
            continue

        meta = params.get("_meta")
        if "_meta" in params and not isinstance(meta, dict):
            resp = _mcp_jsonrpc_error(
                req_id, _MCP_ERR_INVALID_PARAMS,
                "parameter '_meta' must be an object",
            )
        elif isinstance(meta, dict) and _MCP_META_PROTOCOL_VERSION in meta:
            resp = _mcp_dispatch_modern(
                app, commands, req_id, method, params, meta, continuation,
            )
        elif session.active:
            resp = _mcp_dispatch_legacy(
                app, commands, req_id, method, params,
                session=session, continuation=continuation, channel=channel,
            )
        else:
            resp = _mcp_jsonrpc_error(
                req_id, _MCP_ERR_INVALID_PARAMS,
                "missing required request metadata: "
                f"_meta['{_MCP_META_PROTOCOL_VERSION}']",
            )

        channel.write(resp)


def _mcp_dispatch_modern(
    app: App,
    commands: dict[str, tuple[Command, str]],
    req_id: object,
    method: str,
    params: dict,
    meta: dict,
    continuation: _MCPContinuation,
) -> dict:
    """Dispatch one modern-era request: metadata, then version, then method."""
    invalid = _mcp_validate_meta(meta)
    if invalid is not None:
        return _mcp_jsonrpc_error(req_id, _MCP_ERR_INVALID_PARAMS, invalid)
    version = meta[_MCP_META_PROTOCOL_VERSION]
    if version != _MCP_PROTOCOL_VERSION:
        return _mcp_jsonrpc_error(
            req_id, _MCP_ERR_UNSUPPORTED_PROTOCOL_VERSION,
            "Unsupported protocol version",
            {"supported": [_MCP_PROTOCOL_VERSION], "requested": version},
        )
    if method == "server/discover":
        return _mcp_handle_discover(app, req_id)
    if method == "tools/list":
        return _mcp_handle_tools_list(app, commands, req_id, modern=True)
    if method == "tools/call":
        return _mcp_handle_tools_call(
            app, req_id, params, modern=True,
            commands=commands, meta=meta, continuation=continuation,
        )
    return _mcp_jsonrpc_error(
        req_id, _MCP_ERR_METHOD_NOT_FOUND, f"Method not found: {method}",
    )


def _mcp_dispatch_legacy(
    app: App,
    commands: dict[str, tuple[Command, str]],
    req_id: object,
    method: str,
    params: dict,
    *,
    session: _MCPLegacySession,
    continuation: _MCPContinuation,
    channel: _MCPChannel,
) -> dict:
    """Dispatch one legacy-era request, in that revision's own shapes."""
    if method == "tools/list":
        return _mcp_handle_tools_list(app, commands, req_id, modern=False)
    if method == "tools/call":
        return _mcp_handle_tools_call(
            app, req_id, params, modern=False, commands=commands,
            session=session, continuation=continuation, channel=channel,
        )
    return _mcp_jsonrpc_error(
        req_id, _MCP_ERR_METHOD_NOT_FOUND, f"Method not found: {method}",
    )
