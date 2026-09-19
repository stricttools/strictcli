#!/usr/bin/env python3
"""Error parity check for strictcli conformance.

Extracts error message patterns from N implementations (currently Python,
Go, and TypeScript), normalizes them to a common signature form, and
verifies:
  1. Every signature extracted from any implementation is accounted for in all
     others (present or excluded with rationale).
  2. Every parse-time error signature is covered by at least one conformance
     test case.

The comparison is N-way symmetric: each implementation's extracted messages
form a multiset keyed by normalized signature.  Adding a new target requires
an explicit status answer for every signature that differs.

Exit 0 if all checks pass, exit 1 with a diff report otherwise.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------

CONFORMANCE_DIR = Path(__file__).resolve().parent
PROJECT_ROOT = CONFORMANCE_DIR.parent
PY_SOURCE = PROJECT_ROOT / "python" / "strictcli" / "__init__.py"
GO_ERRORS = PROJECT_ROOT / "go" / "strictcli" / "errors.go"
TS_ERRORS = PROJECT_ROOT / "typescript" / "src" / "errors.ts"
CASES_DIR = CONFORMANCE_DIR / "cases"

# ---------------------------------------------------------------------------
# Implementation registry
# ---------------------------------------------------------------------------

IMPLEMENTATIONS = ("python", "go", "typescript")

# ---------------------------------------------------------------------------
# Unified signature status manifest
#
# Shape: {signature: {impl_name: status_string}}
#
# Status strings:
#   "excluded:<rationale>"         -- not present in this impl, by design
#   "dead_code:<rationale>"        -- present but unreachable at runtime
#   "coverage_deferred:<rationale>" -- present but coverage deferred
#
# Signatures NOT listed here are expected to be present in ALL
# implementations.  Signatures listed here need a status for every
# implementation where the default ("present and coverable") does not hold.
# ---------------------------------------------------------------------------

SIGNATURE_STATUS: dict[str, dict[str, str]] = {
    # =======================================================================
    # Python-present, Go-excluded
    # =======================================================================

    # -- The dry-run declaration's orphan-reason guard (Python/TS only) --
    # Python and TS take the declaration as two independent spec keys, so a
    # reason without `dry_run_supported=false` is representable and must be
    # rejected. Go's WithDryRunUnsupported(reason) is the only way to set
    # either field and always sets both, which makes the orphan shape
    # unrepresentable rather than merely unchecked.

    # -- Handler signature validation (Python only) --
    'command *: handler missing parameter * for flag *': {
        "go": "excluded:Go uses map[string]interface{} kwargs, no handler signature validation",
        "typescript": "excluded:TS handlers take a single kwargs object like Go; no handler signature validation",
    },
    'command *: handler missing parameter * for arg *': {
        "go": "excluded:Go uses map[string]interface{} kwargs, no handler signature validation",
        "typescript": "excluded:TS handlers take a single kwargs object like Go; no handler signature validation",
    },
    'command *: handler has extra parameter * not matching any flag or arg': {
        "go": "excluded:Go uses map[string]interface{} kwargs, no handler signature validation",
        "typescript": "excluded:TS handlers take a single kwargs object like Go; no handler signature validation",
    },


    # -- Python internal float errors --
    'invalid literal for float(): *': {
        "go": "excluded:Python internal ValueError from _strict_float, surfaces as 'expected float'",
        "typescript": "excluded:Python internal ValueError from _strict_float; TS float parsing surfaces 'expected float' directly",
    },

    # -- Python generic _require_non_empty_str --
    '*.* must be a non-empty string': {
        "go": "excluded:Python uses generic _require_non_empty_str; Go has entity-specific messages",
        "typescript": "excluded:Python generic dotted template; TS parameterizes the label and mirrors Go's entity-specific templates",
    },

    # -- SkipCheck (Python-only scope adapter) --
    'SkipCheck.reason must be a non-empty string': {
        "go": "excluded:Python-only scope-adapter skip directive; Go has no scope adapter",
        "typescript": "excluded:Python-only scope-adapter skip directive; TS matches Go: no scope adapter",
    },

    # -- Check provider validation (Python dynamic, Go static) --
    'check provider must be callable': {
        "go": "excluded:Go RegisterCheckProvider takes a typed func value; no callable check needed",
    },
    'check provider must return a list of CheckSpec, got *': {
        "go": "excluded:Go provider return type is []CheckSpec (statically typed); no runtime check",
    },
    'check provider returned a non-CheckSpec value: *': {
        "go": "excluded:Go provider elements are CheckSpec (statically typed); no runtime check",
    },

    # -- Python f-string vs Go fmt.Sprintf bracket differences --
    'Flag *: default * is not in choices *': {
        "go": "excluded:Python f-string normalizes without brackets; Go counterpart is 'Flag *: default * is not in choices [*]'",
        "typescript": "excluded:TS mirrors Go's bracketed rendering 'Flag *: default * is not in choices [*]'",
    },

    # -- Python Implies value type validation --
    'command *: Implies value must be a bool, got *': {
        "go": "excluded:Go Implies struct has typed bool Value field; no runtime type check needed",
    },

    # -- Python tag DSL --
    'tag expression: unknown AST node *': {
        "go": "excluded:Python uses tuple-based AST with string dispatch; Go uses typed interfaces",
        "typescript": "excluded:Python tuple-based AST with string dispatch; TS uses typed AST node objects",
    },

    # -- Python config format validation --
    'App.config_format must be "json" or "toml", got *': {
        "go": "excluded:Go uses fmt.Fprintf+os.Exit with %q quoting; Python uses ValueError with !r quoting",
    },

    # -- Python field name vs Go option function name --
    'cannot use both checks_path and checks_embed': {
        "go": "excluded:Go uses option function names (WithChecks/WithChecksEmbed); Python uses field names (checks_path/checks_embed)",
        "typescript": "excluded:TS emits Go's option spelling ('cannot use both WithChecks and WithChecksEmbed')",
    },
    'App.config_conflict_mode must be "cli-wins" or "error", got *': {
        "go": "excluded:Go counterpart is 'WithConfigConflictMode: mode must be ...' (option function name)",
    },
    'Flag *: conflict_mode must be "cli-wins" or "error", got *': {
        "go": "excluded:Go counterpart is 'ConflictMode: mode must be ...' (option function name)",
    },

    # -- Python unique bool validation --
    'Flag *: unique must be True or False': {
        "go": "excluded:Go uses typed bool field for Unique; no runtime type check needed",
        "typescript": "excluded:TS unique is a typed boolean option; no runtime type check needed",
    },


    # -- Compound type structural differences --
    '*: dict key type must be str, got *': {
        "go": "excluded:Python generic {context}: pattern; Go uses DictOf typed constructor",
        "typescript": "excluded:Python type-DSL parser {context}: pattern; TS uses typed schema constructors (dictOf)",
    },
    '*: dict type requires type arguments (e.g., dict[str, int]), got bare dict': {
        "go": "excluded:Python generic {context}: pattern; Go uses DictOf typed constructor",
        "typescript": "excluded:Python type-DSL parser {context}: pattern; TS uses typed schema constructors (dictOf)",
    },
    '*: dict type takes exactly two type arguments, got *': {
        "go": "excluded:Python generic {context}: pattern; Go uses DictOf typed constructor",
        "typescript": "excluded:Python type-DSL parser {context}: pattern; TS uses typed schema constructors (dictOf)",
    },
    '*: dict value type must be str, int, or float, got *': {
        "go": "excluded:Python generic {context}: pattern; Go uses DictOf typed constructor",
        "typescript": "excluded:Python type-DSL parser {context}: pattern; TS uses typed schema constructors (dictOf)",
    },
    '*: list item type must be str, int, or float, got *': {
        "go": "excluded:Python generic {context}: pattern; Go uses ListOf typed constructor",
        "typescript": "excluded:Python type-DSL parser {context}: pattern; TS uses typed schema constructors (listOf)",
    },
    '*: list type requires an item type (e.g., list[int]), got bare list': {
        "go": "excluded:Python generic {context}: pattern; Go uses ListOf typed constructor",
        "typescript": "excluded:Python type-DSL parser {context}: pattern; TS uses typed schema constructors (listOf)",
    },
    '*: list type takes exactly one type argument, got *': {
        "go": "excluded:Python generic {context}: pattern; Go uses ListOf typed constructor",
        "typescript": "excluded:Python type-DSL parser {context}: pattern; TS uses typed schema constructors (listOf)",
    },
    '*: type must be str, bool, int, float, list[T], or dict[str, T], got *': {
        "go": "excluded:Python generic {context}: pattern; Go uses separate typed constructors",
        "typescript": "excluded:Python type-DSL parser {context}: pattern; TS uses typed schema constructors",
    },

    # -- Python compound type validation (Flag context) --
    'Flag *: * is not of type float': {
        "go": "excluded:Python generic {context} pattern; Go uses typed constructors with separate messages",
        "typescript": "excluded:Python generic {context} element validation; TS uses a parameterized type-name template",
    },
    'Flag *: * is not of type int': {
        "go": "excluded:Python generic {context} pattern; Go uses typed constructors with separate messages",
        "typescript": "excluded:Python generic {context} element validation; TS uses a parameterized type-name template",
    },
    'Flag *: * is not of type str': {
        "go": "excluded:Python generic {context} pattern; Go uses typed constructors with separate messages",
        "typescript": "excluded:Python generic {context} element validation; TS uses a parameterized type-name template",
    },

    # -- Dict/list parse-time messages (Python JSON-based) --
    '--*: JSON key must be a string, got *': {
        "go": "excluded:Python dict flag JSON parsing; Go handles via typed coercion in parse.go",
        "typescript": "excluded:JSON.parse object keys are always strings; the non-string-key branch is unreachable in TS",
    },
    '--*: JSON value for key * must be a number, got *': {
        "go": "excluded:Python dict flag JSON value validation; Go handles via typed coercion",
    },
    '--*: JSON value for key * must be a string, got *': {
        "go": "excluded:Python dict flag JSON value validation; Go handles via typed coercion",
    },
    '--*: JSON value for key * must be an integer, got *': {
        "go": "excluded:Python dict flag JSON value validation; Go handles via typed coercion",
    },
    '--*: JSON value must be an object, got *': {
        "go": "excluded:Python dict flag JSON object validation; Go handles via typed coercion",
    },
    '--*: duplicate key *': {
        "go": "excluded:Python dict flag duplicate key detection; Go handles via map overwrite",
    },
    '--*: empty key in *': {
        "go": "excluded:Python dict flag empty key validation; Go handles differently",
    },
    '--*: env var * must be a JSON object, got *': {
        "go": "excluded:Python dict flag env var validation; Go handles via typed coercion",
    },
    '--*: expected key=value or JSON, got *': {
        "go": "excluded:Python dict flag format validation; Go handles via typed coercion",
    },
    '--*: invalid JSON in env var *: *': {
        "go": "excluded:Python dict flag JSON parse error; Go handles via typed coercion",
    },
    '--*: invalid JSON: *': {
        "go": "excluded:Python dict flag JSON parse error; Go handles via typed coercion",
    },
    '--*: unsupported value type *': {
        "go": "excluded:Python dict flag unsupported type; Go handles via typed constructors",
    },
    '--*: value for key *: *': {
        "go": "excluded:Python dict flag per-key value error; Go handles via typed coercion",
    },

    # -- Arg compound type messages (Python wording) --
    'Arg *: default * is not in choices *': {
        "go": "excluded:Python f-string normalizes without brackets; Go counterpart uses [%s]",
        "typescript": "excluded:TS mirrors Go's bracketed rendering 'Arg *: default * is not in choices [*]'",
    },
    'Arg *: dict type is not supported on args': {
        "go": "excluded:Go uses 'positional arguments' wording instead of 'args'",
    },
    'Arg *: list item type must be str, int, or float, got *': {
        "go": "excluded:Python includes 'got' clause; Go omits it",
        "typescript": "excluded:TS catalog carries the Go-wording template (no 'got' clause); typed schemas make the branch unreachable",
    },
    'Arg *: list type on args requires variadic=True': {
        "go": "excluded:Go uses lowercase variadic=true; Python uses variadic=True",
    },
    'Arg *: list type requires an item type (e.g., list[int]), got bare list': {
        "go": "excluded:Python includes full example; Go has different wording",
        "typescript": "excluded:Python type-DSL parsing; a bare list type is inexpressible with TS typed schemas",
    },
    'Arg *: list type takes exactly one type argument, got *': {
        "go": "excluded:Python generic pattern; Go has different wording",
        "typescript": "excluded:Python type-DSL parsing; wrong type-argument counts are inexpressible with TS typed schemas",
    },

    # -- Flag compound type Python-only messages --
    'Flag *: dict default key * must be a string': {
        "go": "excluded:Python validates dict default keys; Go uses typed map[string]interface{} assertion",
    },
    'Flag *: dict flag default must be a dict': {
        "go": "excluded:Python uses 'dict'; Go uses 'map[string]interface{}'",
        "typescript": "excluded:TS uses 'dict flag default must be a Map' (language-idiomatic, like Go's map[string]interface{})",
    },
    'Flag *: dict type cannot be combined with repeatable=True': {
        "go": "excluded:Go forbids compound+repeatable differently; no direct counterpart",
    },
    'Flag *: dict type cannot be combined with unique': {
        "go": "excluded:Go forbids compound+unique differently; no direct counterpart",
    },
    'Flag *: dict type cannot use env_separator (env vars are parsed as JSON)': {
        "go": "excluded:Go validates list/env interaction differently; no direct counterpart",
    },
    'Flag.type must be str, bool, int, float, list[T], or dict[str, T], got *': {
        "go": "excluded:Go uses typed constructors (ListOf/DictOf); no runtime type check needed",
        "typescript": "excluded:TS flag factories take typed schema strings; invalid types are compile-time errors",
    },

    # -- Typed arg parse-time messages --
    'argument *: *': {
        "go": "excluded:Python generic 'argument' prefix wrapper; Go produces typed errors at parse level",
    },
    'argument *: expected float, got *': {
        "go": "excluded:Python typed arg float parsing; Go handles at parse level with different prefix",
    },

    # -- Required-bool prefix structural difference --
    "flag '--*' is required": {
        "go": "excluded:Go uses parameterized prefix in applyFlagDefault; signature is '*flag --*...'",
    },
    "flag '--*' must be passed as --*": {
        "go": "excluded:Go uses parameterized prefix in applyFlagDefault; signature is '*flag --*...'",
    },
    "flag '--*' must be passed as --* or --no-*": {
        "go": "excluded:Go uses parameterized prefix in applyFlagDefault; signature is '*flag --*...'",
    },
    "global flag '--*' is required": {
        "go": "excluded:Go uses parameterized prefix in applyFlagDefault; signature is '*flag --*...'",
        "typescript": "excluded:TS uses parameterized prefix in parse.ts like Go applyFlagDefault; template shape is '*flag --*...'",
    },
    "global flag '--*' must be passed as --*": {
        "go": "excluded:Go uses parameterized prefix in applyFlagDefault; signature is '*flag --*...'",
        "typescript": "excluded:TS uses parameterized prefix in parse.ts like Go applyFlagDefault; template shape is '*flag --*...'",
    },
    "global flag '--*' must be passed as --* or --no-*": {
        "go": "excluded:Go uses parameterized prefix in applyFlagDefault; signature is '*flag --*...'",
        "typescript": "excluded:TS uses parameterized prefix in parse.ts like Go applyFlagDefault; template shape is '*flag --*...'",
    },

    # -- InfraEnv structural / extraction asymmetries --
    'command *: flag *: RelativeToRoot references undeclared infra root *; declare it as an infra root': {
        "go": "excluded:Go has no command-context flag-marker validation; it validates per-flag at registration",
    },

    # =======================================================================
    # Go-present, Python-excluded
    # =======================================================================

    # -- Go entity-specific help validation --
    'App.help must be a non-empty string': {
        "python": "excluded:Go entity-specific; Python generic '*.* must be a non-empty string'",
    },
    'Arg.help must be a non-empty string': {
        "python": "excluded:Go entity-specific; Python generic '*.* must be a non-empty string'",
    },
    'Flag.help must be a non-empty string': {
        "python": "excluded:Go entity-specific; Python generic '*.* must be a non-empty string'",
    },
    'Group.help must be a non-empty string': {
        "python": "excluded:Go entity-specific; Python generic '*.* must be a non-empty string'",
    },

    # -- Go bracket-formatted choices --
    'Flag *: default * is not in choices [*]': {
        "python": "excluded:Go fmt.Sprintf normalizes with brackets; Python counterpart is 'Flag *: default * is not in choices *'",
    },

    # -- Go cycle detection --
    'check dependency cycle detected involving *': {
        "python": "excluded:Go expansion-phase cycle detection; Python only reports cycles via path format",
    },
    'check dependency cycle detected': {
        "python": "excluded:Go Kahn fallback when cycle path not found; Python always finds cycle path",
    },

    # -- Go path.Match error --
    'invalid glob pattern *: *': {
        "python": "excluded:Go-specific path.Match error; Python fnmatch never errors on patterns",
    },

    # -- Go env var error wrapper --
    '* (from env var *)': {
        "python": "excluded:Go generic env var error wrapper; Python embeds env var in specific messages",
    },

    # -- Go option function names --
    'cannot use both WithChecks and WithChecksEmbed': {
        "python": "excluded:Go uses option function names (WithChecks/WithChecksEmbed); Python uses field names (checks_path/checks_embed)",
    },
    'WithConfigConflictMode: mode must be "cli-wins" or "error", got *': {
        "python": "excluded:Python counterpart is 'App.config_conflict_mode must be ...' (field name)",
    },
    'ConflictMode: mode must be "cli-wins" or "error", got *': {
        "python": "excluded:Python counterpart is 'Flag ...: conflict_mode must be ...' (flag kwarg name)",
    },

    # -- Go config coercion --
    'expected integer, got float': {
        "python": "excluded:Go plain-string return in coerceConfigScalarLong; Python generic 'expected integer, got *'",
    },

    # -- Go compound type messages --
    'Arg *: default * is not in choices [*]': {
        "python": "excluded:Go fmt.Sprintf with brackets; Python counterpart normalizes without brackets",
    },
    'Arg *: dict type is not supported on positional arguments': {
        "python": "excluded:Go uses 'positional arguments' wording; Python uses 'args'",
    },
    'Arg *: list item type must be str, int, or float': {
        "python": "excluded:Go omits 'got' clause; Python includes it",
    },
    'Arg *: list type requires variadic=true': {
        "python": "excluded:Go uses lowercase variadic=true; Python uses variadic=True",
    },
    'DictOf: value type must be str, int, or float, got *': {
        "python": "excluded:Go typed constructor validation; Python uses generic {context}: pattern",
    },
    'Flag *: default element *: *': {
        "python": "excluded:Go type-specific default element validation (Python uses generic pattern)",
    },
    'Flag *: default value for key *: *': {
        "python": "excluded:Go type-specific dict default validation; Python validates generically",
    },
    'Flag *: dict flag default must be a map[string]interface{}': {
        "python": "excluded:Go typed assertion for dict default; Python uses isinstance check",
    },
    'Flag *: list flag default must be a []interface{}': {
        "python": "excluded:Go typed assertion for list default; Python uses isinstance check",
    },
    'ListOf: item type must be str, int, or float, got *': {
        "python": "excluded:Go typed constructor validation; Python uses generic {context}: pattern",
    },

    # -- Go config field help validation --
    'config field *: help text is required': {
        "python": "excluded:Go entity-specific; Python generic '*.* must be a non-empty string'",
    },
    'framework field *: help text is required': {
        "python": "excluded:Go entity-specific; Python generic '*.* must be a non-empty string'",
    },
    'framework field *: invalid name, must match [a-z][a-z0-9_]*(.[a-z][a-z0-9_]*)* (lowercase, dots for sections)': {
        "python": "excluded:Go has separate framework field validation; Python uses ConfigField.__post_init__",
    },

    # -- Go config coercion short-name errors --
    'expected bool, got *': {
        "python": "excluded:Go coerceConfigScalarShort raw return; Python wraps with 'config field' prefix",
    },
    'expected int, got *': {
        "python": "excluded:Go coerceConfigScalarShort raw return; Python wraps with 'config field' prefix",
    },
    'expected int, got float': {
        "python": "excluded:Go coerceConfigScalarShort raw return; Python wraps with 'config field' prefix",
    },
    'expected str, got *': {
        "python": "excluded:Go coerceConfigScalarShort raw return; Python wraps with 'config field' prefix",
    },
    'expected array for list flag, got *': {
        "python": "excluded:Go coerceConfigValue for ListType flags; Python uses 'repeatable flag'",
    },

    # -- Go invoke/routing errors --
    'no command specified': {
        "python": "excluded:Go routing returns error; Python shows help when no command given",
    },
    'passthrough command: _args must be []string': {
        "python": "excluded:Go typed system requires []string assertion; Python uses duck typing",
    },
    'dict flag *: expected map type, got *': {
        "python": "excluded:Go invoke coerceInvokeDict; Python uses isinstance with different message",
    },

    # -- Go list flag env separator --
    '--*: list flag with env requires env_separator': {
        "python": "excluded:Go-specific list/env interaction validation; Python handles differently",
    },

    # -- Go InfraEnv structural asymmetries --
    'duplicate infra root env var *': {
        "python": "excluded:Python infra_root is a dict keyed by env var; duplicates are impossible by construction",
    },
    'duplicate handshake env var *': {
        "python": "excluded:Python handshake_env is a dict keyed by env var; duplicates are impossible by construction",
    },
    'duplicate connection env var *': {
        "python": "excluded:Python connection_env is a dict keyed by env var; duplicates are impossible by construction",
        "typescript": "excluded:TS connectionEnv is a Record keyed by env var; duplicates are impossible by construction",
    },

    # -- Go schema.go errors (go.mod project_id, schema mismatch) --
    'Cannot determine project_id: go.mod not found': {
        "python": "excluded:Go schema uses go.mod for project_id; Python uses pyproject.toml/setup.py",
        "typescript": "excluded:TS schema uses package.json for project_id (each language names its own project file)",
    },
    'Cannot determine project_id: error reading go.mod: %w': {
        "python": "excluded:Go schema uses go.mod for project_id; Python uses pyproject.toml/setup.py",
        "typescript": "excluded:TS schema uses package.json for project_id (each language names its own project file)",
    },
    'Cannot determine project_id: no module directive in go.mod': {
        "python": "excluded:Go schema uses go.mod for project_id; Python uses pyproject.toml/setup.py",
        "typescript": "excluded:TS schema uses package.json for project_id (each language names its own project file)",
    },
    "Schema mismatch: existing schema belongs to project *, not *. Run from the correct project directory.": {
        "python": "excluded:Go schema project_id validation; Python equivalent validates differently",
    },

    # -- Go outcome.go errors (typed generics Get/GetOpt) --
    'strictcli.Get: no such key *': {
        "python": "excluded:Go typed generic helper; Python uses kwargs[key] directly",
    },
    'strictcli.Get: key * is nil (not provided); use GetOpt for optional values': {
        "python": "excluded:Go typed generic helper; Python uses kwargs[key] directly",
    },
    'strictcli.Get: key * has dynamic type *, want *': {
        "python": "excluded:Go typed generic helper; Python is dynamically typed",
    },
    'strictcli.GetOpt: no such key *': {
        "python": "excluded:Go typed generic helper; Python uses kwargs.get(key) directly",
    },
    'strictcli.GetOpt: key * has dynamic type *, want *': {
        "python": "excluded:Go typed generic helper; Python is dynamically typed",
    },

    # -- Go context.go errors (InfraValue, ConnectionEnvValue, Source) --
    'InfraValue: * is not a declared infra root, handshake, or connection env var': {
        "python": "excluded:Go Context.InfraValue panic; Python equivalent raises KeyError natively",
    },
    'ConnectionEnvValue: * is not a declared connection env var': {
        "python": "excluded:Go Context.ConnectionEnvValue panic; Python equivalent raises KeyError natively",
    },
    'no source info for flag *': {
        "python": "excluded:Go Context.Source panic; Python equivalent raises KeyError natively",
    },

    # -- Go tool.go errors (JsonSchema) --
    'JsonSchema: *': {
        "python": "excluded:Go App.JsonSchema method panic; Python equivalent is json_schema() with different error",
    },
    "JsonSchema: * is a group, not a command": {
        "python": "excluded:Go App.JsonSchema method panic; Python equivalent is json_schema() with different error",
    },

    # -- Go check_runner.go errors (outcome not minted) --
    'check * returned an outcome not minted by its reporter; use reporter methods (Passed/Skipped/Found)': {
        "python": "excluded:Go runtime assertion for reporter-minted outcomes; Python uses type checking",
    },

    # =======================================================================
    # Dead code: present in both implementations but unreachable at runtime.
    # Excluded from coverage checks (no conformance test can trigger them).
    # =======================================================================
    'command *: flag * missing help text': {
        "python": "dead_code:Flag constructors validate help before command-level check can fire",
        "go": "dead_code:Flag constructors validate help before command-level check can fire",
        "typescript": "dead_code:Flag constructors validate help before command-level check can fire",
    },

    # =======================================================================
    # Coverage-deferred: present in both implementations but require test
    # infrastructure not yet built.  Excluded from coverage checks but
    # remain parity-checked.
    # =======================================================================
    '--*: config value error: *': {
        "python": "coverage_deferred:Needs config file fixture support in conformance framework",
        "go": "coverage_deferred:Needs config file fixture support in conformance framework",
        "typescript": "coverage_deferred:Needs config file fixture support in conformance framework",
    },
    '--*: config value error: duplicate value *': {
        "python": "coverage_deferred:Needs config file fixture support in conformance framework",
        "go": "coverage_deferred:Needs config file fixture support in conformance framework",
        "typescript": "coverage_deferred:Needs config file fixture support in conformance framework",
    },
    '--*: cannot read stdin': {
        "python": "coverage_deferred:Requires stdin piping to subprocess, not supported in conformance runner",
        "go": "coverage_deferred:Requires stdin piping to subprocess, not supported in conformance runner",
        "typescript": "coverage_deferred:Requires stdin piping to subprocess, not supported in conformance runner",
    },
    '--*: stdin (@-) can only be used once per invocation': {
        "python": "coverage_deferred:Requires stdin piping to subprocess, not supported in conformance runner",
        "go": "coverage_deferred:Requires stdin piping to subprocess, not supported in conformance runner",
        "typescript": "coverage_deferred:Requires stdin piping to subprocess, not supported in conformance runner",
    },
    '--*: file exceeds 1 MB limit': {
        "python": "coverage_deferred:Requires a >1MB fixture file, impractical for conformance suite",
        "go": "coverage_deferred:Requires a >1MB fixture file, impractical for conformance suite",
        "typescript": "coverage_deferred:Requires a >1MB fixture file, impractical for conformance suite",
    },
    '--*: cannot read file: *': {
        "python": "coverage_deferred:Requires a file with restricted permissions, platform-dependent",
        "go": "coverage_deferred:Requires a file with restricted permissions, platform-dependent",
        "typescript": "coverage_deferred:Requires a file with restricted permissions, platform-dependent",
    },

    # =======================================================================
    # TypeScript-present, Python/Go-excluded
    # =======================================================================

    # -- TS invoke group-path message (Python wording, errors.ts documents it) --
    '* is a group, not a command': {
        "python": "excluded:Python raises InvokeError with the same text; InvokeError raises are not extracted (only _ParseError/ValueError)",
        "go": "excluded:Go inlines 'no command resolved from path' in invoke.go; no conformance case distinguishes them",
    },

    # -- TS schema project_id (package.json; language-specific project files) --
    'Cannot determine project_id: package.json not found': {
        "python": "excluded:Python uses pyproject.toml/setup.py for project_id",
        "go": "excluded:Go uses go.mod for project_id",
    },
    'Cannot determine project_id: error reading package.json: *': {
        "python": "excluded:Python uses pyproject.toml/setup.py for project_id",
        "go": "excluded:Go uses go.mod for project_id",
    },
    'Cannot determine project_id: no name field in package.json': {
        "python": "excluded:Python uses pyproject.toml/setup.py for project_id",
        "go": "excluded:Go uses go.mod for project_id",
    },

    # -- TS CheckOutcome mint guard (token approach, errors.ts documents it) --
    'CheckOutcome cannot be constructed directly; obtain one from a reporter (passed/skipped/found)': {
        "python": "excluded:Python _CheckOutcome.__post_init__ mint guard raises TypeError; TypeError raises are not extracted",
        "go": "excluded:Go seals CheckOutcome structurally (unexported fields); no runtime guard message",
    },

    # -- Tag contract violation (inline in both siblings, centralized in TS) --
    'command *: tag * requires flag "--*"': {
        "python": "excluded:Python builds the violation string inline in _validate_tag_contracts (returned, not raised)",
        "go": "excluded:Go template is an inline fmt.Sprintf in strictcli.go checkCommandTagContract, not in errors.go",
    },

    # -- TS handler return contract (Python template with TS type names) --
    'command handler must return number (exit code), undefined (exit 0), or strictcli.outcome(...); got *': {
        "python": "excluded:Python counterpart is a TypeError with int/None wording; TypeError raises are not extracted",
        "go": "excluded:Go handlers return the typed Outcome; a non-outcome return is inexpressible",
    },
    'strictcli.outcome: exit_code must be an integer number; got *': {
        "python": "excluded:Python outcome() relies on the int annotation; no runtime exit_code check",
        "go": "excluded:Go Exit(code int) is statically typed",
    },

    # -- Router tool command validation (inline in Go, absent in Python) --
    'command must be a string': {
        "python": "excluded:Python router execute relies on the JSON schema contract; no runtime string check",
        "go": "excluded:Go counterpart is an inline InvokeError literal in tool.go makeRouterTool, not in errors.go",
    },

    # -- TS TOML 1.0 acceptance gate (smol-toml accepts 1.1; siblings' parsers
    #    are TOML-1.0-native and reject 1.1 constructs with their own errors) --
    "invalid escape sequence '\\*' in basic string (TOML 1.1 construct; strictcli requires TOML 1.0)": {
        "python": "excluded:TS-only TOML 1.0 acceptance gate; Python tomllib rejects 1.1 constructs with its own parser errors",
        "go": "excluded:TS-only TOML 1.0 acceptance gate; go-toml-edit rejects 1.1 constructs with its own parser errors",
    },
    'newline inside inline table (TOML 1.1 construct; strictcli requires TOML 1.0)': {
        "python": "excluded:TS-only TOML 1.0 acceptance gate; Python tomllib rejects 1.1 constructs with its own parser errors",
        "go": "excluded:TS-only TOML 1.0 acceptance gate; go-toml-edit rejects 1.1 constructs with its own parser errors",
    },
    'trailing comma in inline table (TOML 1.1 construct; strictcli requires TOML 1.0)': {
        "python": "excluded:TS-only TOML 1.0 acceptance gate; Python tomllib rejects 1.1 constructs with its own parser errors",
        "go": "excluded:TS-only TOML 1.0 acceptance gate; go-toml-edit rejects 1.1 constructs with its own parser errors",
    },
    'time without seconds (TOML 1.1 construct; strictcli requires TOML 1.0)': {
        "python": "excluded:TS-only TOML 1.0 acceptance gate; Python tomllib rejects 1.1 constructs with its own parser errors",
        "go": "excluded:TS-only TOML 1.0 acceptance gate; go-toml-edit rejects 1.1 constructs with its own parser errors",
    },
    'datetime without seconds (TOML 1.1 construct; strictcli requires TOML 1.0)': {
        "python": "excluded:TS-only TOML 1.0 acceptance gate; Python tomllib rejects 1.1 constructs with its own parser errors",
        "go": "excluded:TS-only TOML 1.0 acceptance gate; go-toml-edit rejects 1.1 constructs with its own parser errors",
    },

    # -- TS config set splicer invariants (comment-preserving single-key edit) --
    'internal: TOML splice verification failed: keys other than * changed': {
        "python": "excluded:TS-only config set splicer invariant; Python edits TOML in place via tomlkit",
        "go": "excluded:TS-only config set splicer invariant; Go edits TOML in place via go-toml-edit",
    },
    'internal: TOML splice: key * not found in document': {
        "python": "excluded:TS-only config set splicer invariant; Python edits TOML in place via tomlkit",
        "go": "excluded:TS-only config set splicer invariant; Go edits TOML in place via go-toml-edit",
    },

    # -- Effects handle accessor spelling (effects contract §2.1 pins one
    #    accessor per language, and the message names it) --
    'ctx.effects is unavailable: this Context was constructed outside a command dispatch': {
        "go": "excluded:Go's accessor is the method ctx.Effects() (effects contract §2.1); its counterpart names that spelling",
    },
    'ctx.Effects() is unavailable: this Context was constructed outside a command dispatch': {
        "python": "excluded:Python's accessor is the property ctx.effects (effects contract §2.1); its counterpart names that spelling",
        "typescript": "excluded:TS's accessor is the getter ctx.effects (effects contract §2.1); its counterpart names that spelling",
    },

    # -- Effects regime: templates whose trigger the conformance runner cannot
    #    reach (effects contract §14.5) --
    # The confirm protocol's own two templates are NOT among them any more: the
    # framework's test-only confirm seam (case-schema `confirm_stdin_interactive`)
    # says the answer channel is interactive and leaves the answer coming from
    # the case's piped stdin, so cases/effects_consequential.json asserts the
    # prompt and the decline byte-for-byte on every target.
    'command *: effects.http failed: * * returned *': {
        "python": "coverage_deferred:Requires issuing a real network request, which conformance cases must not do",
        "go": "coverage_deferred:Requires issuing a real network request, which conformance cases must not do",
        "typescript": "coverage_deferred:Requires issuing a real network request, which conformance cases must not do",
    },

    # -- Effects regime: the §12.10 argument type guards. Go's effect signatures
    #    are statically typed where Python's and TypeScript's are not, so three
    #    of the family are inexpressible there (contract §12.10, §17) --
    'command *: effects.* argv must be a sequence of strings, not *': {
        "go": "excluded:Go's argv parameter is typed []any -- a non-sequence argv is a compile error, not a runtime one",
    },
    "command *: effects.chmod parameter 'mode' must be an int, got *": {
        "go": "excluded:Go's Chmod takes mode int positionally -- a non-int mode is a compile error, not a runtime one",
    },
    "command *: effects.http parameter 'method' must be a string, got *": {
        "go": "excluded:Go's HTTP takes method string positionally -- a non-string method is a compile error, not a runtime one",
    },

    # -- Effects regime: registration-time declarations Go and TypeScript type
    #    statically, so their runtime guards have no counterpart there --
    'command *: grants must be Grant instances, got *': {
        "go": "excluded:Go's WithGrants takes ...Grant (a struct type); a non-Grant element is a compile error",
        "typescript": "excluded:TS's grants option is typed readonly Grant[]; a non-Grant element is a compile error",
    },
    'proc_observe_allowlist entries must be lists of strings, got *': {
        "go": "excluded:Go's WithProcObserveAllowlist takes [][]string; a non-string element is a compile error",
    },

    # =======================================================================
    # The retired test-coverage boolean (contract §12.12)
    #
    # The refusal names the option that replaced it, so it carries a
    # per-language spelling for the same reason the presence family below does:
    # one sentence, each language's own spellings inside it. Each
    # implementation's red-green test asserts its own line -- Python
    # tests/test_coverage.py TestRetiredBooleanRefused, Go
    # coverage_test.go TestCoverageRetiredBooleanRefused, TypeScript
    # checks_coverage.test.ts "the retired boolean is refused naming the
    # directory option".
    # =======================================================================

    'test_coverage is not accepted; declare the directory holding coverage/ and test-coverage.json with test_coverage_dir': {
        "go": "excluded:one sentence in three spellings (contract §12.12); Go carries the WithTestCoverage/WithTestCoverageDir line and asserts it in coverage_test.go",
        "typescript": "excluded:one sentence in three spellings (contract §12.12); TypeScript carries the testCoverage/testCoverageDir line and asserts it in checks_coverage.test.ts",
    },
    'WithTestCoverage is not accepted; declare the directory holding coverage/ and test-coverage.json with WithTestCoverageDir': {
        "python": "excluded:one sentence in three spellings (contract §12.12); Python carries the test_coverage/test_coverage_dir line and asserts it in tests/test_coverage.py",
        "typescript": "excluded:one sentence in three spellings (contract §12.12); TypeScript carries the testCoverage/testCoverageDir line and asserts it in checks_coverage.test.ts",
    },
    'testCoverage is not accepted; declare the directory holding coverage/ and test-coverage.json with testCoverageDir': {
        "python": "excluded:one sentence in three spellings (contract §12.12); Python carries the test_coverage/test_coverage_dir line and asserts it in tests/test_coverage.py",
        "go": "excluded:one sentence in three spellings (contract §12.12); Go carries the WithTestCoverage/WithTestCoverageDir line and asserts it in coverage_test.go",
    },

    # =======================================================================
    # The presence declaration (contract §12.12, §23)
    #
    # Every template in this family carries a per-language noun phrase, because
    # the thing it names is a SPELLING and the three languages spell it
    # differently: the sentence is byte-identical and the spellings inside it
    # are each language's own. So each implementation's variant is a signature
    # its siblings genuinely do not carry, and the parity assertion lives in
    # conformance/cases/presence_registration.json, which asserts the whole
    # line per target rather than a shared one.
    #
    # The declared-twice template is deliberately absent from this block: its
    # spellings are interpolated arguments rather than literal text, so the
    # three implementations share one signature and match without an exclusion.
    # =======================================================================

    # -- Nothing declared: the zero case, three spellings --
    'Flag *: presence is undeclared: declare exactly one of presence="required", presence="optional", or default=<value>': {
        "go": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "typescript": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },
    'Flag *: presence is undeclared: declare exactly one of Required(), Optional(), or Default(<value>)': {
        "python": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "typescript": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },
    'Flag *: presence is undeclared: declare exactly one of *, *, or *': {
        "python": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "go": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },
    'Arg *: presence is undeclared: declare exactly one of presence="required", presence="optional", or default=<value>': {
        "go": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "typescript": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },
    'Arg *: presence is undeclared: declare exactly one of ArgRequired(), ArgOptional(), or ArgDefault(<value>)': {
        "python": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "typescript": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },
    'Arg *: presence is undeclared: declare exactly one of *, *, or *': {
        "python": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "go": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },

    # -- The null-valued default's redirect, three spellings, two surfaces --
    'Flag *: default=None does not declare optionality: use presence="optional" (it delivers None when the flag is absent)': {
        "go": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "typescript": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },
    'Flag *: Default(nil) does not declare optionality: use Optional() (it delivers nil when the flag is absent)': {
        "python": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "typescript": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },
    'Flag *: default: null does not declare optionality: use presence: "optional" (it delivers undefined when the flag is absent)': {
        "python": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "go": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },
    'Arg *: default=None does not declare optionality: use presence="optional" (it delivers None when the arg is absent)': {
        "go": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "typescript": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },
    'Arg *: ArgDefault(nil) does not declare optionality: use ArgOptional() (it delivers nil when the arg is absent)': {
        "python": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "typescript": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },
    'Arg *: default: null does not declare optionality: use presence: "optional" (it delivers undefined when the arg is absent)': {
        "python": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "go": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },

    # -- A variadic arg declaring a default, three spellings --
    'Arg *: a variadic arg cannot declare default=: it always delivers a list, so declare presence="required" for at least one value or presence="optional" for possibly none': {
        "go": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "typescript": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },
    'Arg *: a variadic arg cannot declare ArgDefault(): it always delivers a list, so declare ArgRequired() for at least one value or ArgOptional() for possibly none': {
        "python": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "typescript": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },
    'Arg *: a variadic arg cannot declare *: it always delivers a list, so declare * for at least one value or * for possibly none': {
        "python": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
        "go": 'excluded:the presence family pins ONE sentence in three spellings (contract §12.12), and each implementation carries only its own; presence_registration.json asserts all three, per target',
    },

    # -- Python-only: a presence= value that is neither fact --
    '* *: presence must be "required" or "optional", got *; a default value is declared with default=<value>': {
        "go": "excluded:Python-only by construction (§12.12's amendment, item 153): Python spells the declaration as a keyword taking a string, and Go's three FlagOptions and TypeScript's discriminated union have no input that could carry a bad presence value",
        "typescript": "excluded:Python-only by construction (§12.12's amendment, item 153): Python spells the declaration as a keyword taking a string, and Go's three FlagOptions and TypeScript's discriminated union have no input that could carry a bad presence value",
    },

    # -- TypeScript-only: presence: "default" carrying no value --
    'Flag *: * requires a default value: declare default: <value>, or * for no value': {
        "python": "excluded:TypeScript-only by construction (§12.12's amendment, item 153): TS is the only language whose default spelling has two parts, so the half-written declaration is inexpressible in Python's default=<value> and Go's Default(v), which ARE the value",
        "go": "excluded:TypeScript-only by construction (§12.12's amendment, item 153): TS is the only language whose default spelling has two parts, so the half-written declaration is inexpressible in Python's default=<value> and Go's Default(v), which ARE the value",
    },
    'Arg *: * requires a default value: declare default: <value>, or * for no value': {
        "python": "excluded:TypeScript-only by construction (§12.12's amendment, item 153): TS is the only language whose default spelling has two parts, so the half-written declaration is inexpressible in Python's default=<value> and Go's Default(v), which ARE the value",
        "go": "excluded:TypeScript-only by construction (§12.12's amendment, item 153): TS is the only language whose default spelling has two parts, so the half-written declaration is inexpressible in Python's default=<value> and Go's Default(v), which ARE the value",
    },
    'presence: "default" with default: *': {
        "python": "excluded:TypeScript-only by construction (§12.12's amendment, item 153): TS is the only language whose default spelling has two parts, so the half-written declaration is inexpressible in Python's default=<value> and Go's Default(v), which ARE the value",
        "go": "excluded:TypeScript-only by construction (§12.12's amendment, item 153): TS is the only language whose default spelling has two parts, so the half-written declaration is inexpressible in Python's default=<value> and Go's Default(v), which ARE the value",
    },

    # -- Python-only: the handler-parameter check --
    "command *: handler parameter * is bound to optional flag '--*' and must default to None": {
        "go": 'excluded:Python-only (§12.12, item 147): Go and TypeScript handlers receive one kwargs map / one args object, so there is no per-parameter default to re-sentinelize with. An absent site, not a skipped check',
        "typescript": 'excluded:Python-only (§12.12, item 147): Go and TypeScript handlers receive one kwargs map / one args object, so there is no per-parameter default to re-sentinelize with. An absent site, not a skipped check',
    },
    'command *: handler parameter * is bound to optional arg * and must default to None': {
        "go": 'excluded:Python-only (§12.12, item 147): Go and TypeScript handlers receive one kwargs map / one args object, so there is no per-parameter default to re-sentinelize with. An absent site, not a skipped check',
        "typescript": 'excluded:Python-only (§12.12, item 147): Go and TypeScript handlers receive one kwargs map / one args object, so there is no per-parameter default to re-sentinelize with. An absent site, not a skipped check',
    },

    # =======================================================================
    # The scoped-selector construct (contract §12.13, §24, §18.19 item 220)
    #
    # Three shapes of divergence live in this block, and each is expected
    # rather than tolerated:
    #
    #   1. ONE SENTENCE, THREE SPELLINGS. A template naming a spelling carries
    #      a per-language noun phrase (§12.12's mechanism), so each
    #      implementation's variant is a signature its siblings genuinely do
    #      not carry. The parity assertion is per target, in
    #      cases/selector_registration.json and cases/choice_records.json.
    #
    #   2. ONE TEMPLATE, ONE PARAMETERIZED PREFIX. Python parameterizes the
    #      Flag/Arg surface and TypeScript parameterizes the
    #      `Choice "c" of "sel": ` prefix, where the siblings inline both.
    #      Same sentence, different signature.
    #
    #   3. A LANGUAGE-SPECIFIC FAMILY. Go authored two (its twin constructors
    #      and its identity values), Python authored ten (keyword strings,
    #      dataclass fields and annotations), and TypeScript authored none.
    #      §18.19 item 220 ratifies all three sets.
    # =======================================================================

    ' by default': {
        'python': 'excluded:the clause is composed into the sentence at the call site in this implementation rather than carried as a template of its own; cases/selector_scope.json asserts the composed sentence on all three targets',
    },
    ' from config key *': {
        'python': 'excluded:the clause is composed into the sentence at the call site in this implementation rather than carried as a template of its own; cases/selector_scope.json asserts the composed sentence on all three targets',
    },
    ' from config key **': {
        'go': 'excluded:the clause is composed into the sentence at the call site in this implementation rather than carried as a template of its own; cases/selector_scope.json asserts the composed sentence on all three targets',
        'typescript': 'excluded:the clause is composed into the sentence at the call site in this implementation rather than carried as a template of its own; cases/selector_scope.json asserts the composed sentence on all three targets',
    },
    '* *: choice *: *': {
        'go': 'excluded:Python parameterizes the Flag/Arg prefix into one template where the siblings twin it (contract §12.13, §18.19 item 219); the twinned signatures carry the assertion',
        'typescript': 'excluded:Python parameterizes the Flag/Arg prefix into one template where the siblings twin it (contract §12.13, §18.19 item 219); the twinned signatures carry the assertion',
    },
    'Flag *: choice *: *': {
        'python': 'excluded:Python parameterizes the Flag/Arg prefix into one template where the siblings twin it (contract §12.13, §18.19 item 219); the twinned signatures carry the assertion',
    },
    'Arg *: choice *: *': {
        'python': 'excluded:Python parameterizes the Flag/Arg prefix into one template where the siblings twin it (contract §12.13, §18.19 item 219); the twinned signatures carry the assertion',
    },
    '* *: choices must be a non-empty list': {
        'go': 'excluded:Python parameterizes the Flag/Arg prefix into one template where the siblings twin it (contract §12.13, §18.19 item 219); the twinned signatures carry the assertion',
        'typescript': 'excluded:Python parameterizes the Flag/Arg prefix into one template where the siblings twin it (contract §12.13, §18.19 item 219); the twinned signatures carry the assertion',
    },
    'Flag *: choices must be a non-empty list': {
        'python': 'excluded:Python parameterizes the Flag/Arg prefix into one template where the siblings twin it (contract §12.13, §18.19 item 219); the twinned signatures carry the assertion',
    },
    'Arg *: choices must be a non-empty list': {
        'python': 'excluded:Python parameterizes the Flag/Arg prefix into one template where the siblings twin it (contract §12.13, §18.19 item 219); the twinned signatures carry the assertion',
    },
    '* *: choices entry * is a bare value: declare it as *': {
        'go': 'excluded:Python parameterizes the Flag/Arg prefix into one template where the siblings twin it (contract §12.13, §18.19 item 219); the twinned signatures carry the assertion',
        'typescript': 'excluded:Python parameterizes the Flag/Arg prefix into one template where the siblings twin it (contract §12.13, §18.19 item 219); the twinned signatures carry the assertion',
    },
    # -- Retired choices: one Python-only entry-shape refusal --
    # The rules themselves (a live spelling, a duplicate, an empty message, a
    # bool carrier, a retired default, and retired choices without choices) are
    # twinned per surface in all three implementations and match without an
    # exclusion. Only the ENTRY SHAPE diverges: Python's `retired_choices=`
    # keyword takes a list of anything, so a bare value is a runtime refusal,
    # where Go's variadic `...RetiredChoiceValue` and TypeScript's
    # `RetiredChoiceRecord` tuple make the same mis-declaration a compile
    # error. cases/retired_choices.json asserts it per target.
    '* *: retired_choices entry * is a bare value: declare it as *': {
        'go': "excluded:Python-only: the keyword takes a list of anything, where Go's variadic RetiredChoiceValue parameter refuses a bare value at compile time",
        'typescript': "excluded:Python-only: the keyword takes a list of anything, where TypeScript's RetiredChoiceRecord tuple refuses a bare value at compile time",
    },

    '* *: choices entry * is the choice class *, which declares a scope: a choice with a scope belongs to a choice flag, declared with *': {
        'go': "excluded:Python-only (contract §12.13, §18.19 item 220): errChoicesEntryIsChoiceClass names the @choice class twin; Go's Ch and TypeScript's record literal are distinct types, so the sibling mis-declaration is a compile error",
        'typescript': "excluded:Python-only (contract §12.13, §18.19 item 220): errChoicesEntryIsChoiceClass names the @choice class twin; Go's Ch and TypeScript's record literal are distinct types, so the sibling mis-declaration is a compile error",
    },
    'Flag *: choices entry * is a bare value: declare it as *': {
        'go': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
        'python': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
    },
    'Flag *: choices entry * is a bare value: declare it as Ch(<value>, "<help>")': {
        'python': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
        'typescript': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
    },
    'Arg *: choices entry * is a bare value: declare it as *': {
        'go': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
        'python': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
    },
    'Arg *: choices entry * is a bare value: declare it as Ch(<value>, "<help>")': {
        'python': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
        'typescript': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
    },
    'Choice * of *: ': {
        'go': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
        'python': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    '*a member flag must declare *, read as required once this member is elected': {
        'go': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
        'python': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    '*a token-spelled choice cannot carry a payload: the token names the choice, and a choice that carries its own value belongs to a member-spelled choice flag, declared with *': {
        'go': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
        'python': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    # -- The member-short round's two refusals (§24.4, §24.12) --
    # Each is a place a short can be declared where no member flag exists to
    # carry it. Both are Python/TypeScript-only: Go's declaration surface
    # cannot express either state.
    '*a token-spelled choice cannot carry a short: the token names the choice, and only a member-spelled choice has a flag of its own to carry one': {
        'go': "excluded:Go's Choice(name, help, flags ...Flag) has no short slot at all -- only a member's electing Flag carries one -- so a short on a token-spelled choice is unconstructable in Go rather than refused (the class §18.19 item 213 records)",
        'python': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    'Choice * of *: a token-spelled choice cannot carry a short: the token names the choice, and only a member-spelled choice has a flag of its own to carry one': {
        'go': "excluded:Go's Choice(name, help, flags ...Flag) has no short slot at all -- only a member's electing Flag carries one -- so a short on a token-spelled choice is unconstructable in Go rather than refused (the class §18.19 item 213 records)",
        'typescript': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    '*a payload-carrying member declares its short on its payload: *': {
        'go': "excluded:Go's MemberChoice takes ONE flag that types the payload AND carries the short, so a member declaring its short in two places has no Go spelling to refuse",
        'python': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    'Choice * of *: a payload-carrying member declares its short on its payload: *': {
        'go': "excluded:Go's MemberChoice takes ONE flag that types the payload AND carries the short, so a member declaring its short in two places has no Go spelling to refuse",
        'typescript': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_member_short.json asserts each, per target',
    },
    "*flag '--*' collides with a command-level flag of the same name: the scoped one could never be reached": {
        'go': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
        'python': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    "*flag '--*' collides with the choice flag's own name": {
        'go': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
        'python': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    "*flag name 'choice' is reserved by the framework: it tags the delivered record": {
        'go': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
        'python': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    "*flag name 'value' is reserved by the framework: it carries a member-spelled choice's own payload": {
        'go': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
        'python': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    '*help text is required': {
        'go': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
        'python': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    "*positional args cannot be declared inside a choice scope: a positional's meaning would depend on an election that may be typed after it": {
        'go': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
        'python': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    "Choice * of *: flag '--*' collides with a command-level flag of the same name: the scoped one could never be reached": {
        'typescript': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    "Choice * of *: flag '--*' collides with the choice flag's own name": {
        'typescript': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    "Choice * of *: flag name 'choice' is reserved by the framework: it tags the delivered record": {
        'typescript': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    "Choice * of *: flag name 'value' is reserved by the framework: it carries a member-spelled choice's own payload": {
        'typescript': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    "Choice * of *: positional args cannot be declared inside a choice scope: a positional's meaning would depend on an election that may be typed after it": {
        # §12.13 pins this row "All three", and the implementations disagree
        # with the document in the way §18.19 exists to record: Go's
        # Choice(name, help, flags ...Flag) is variadic over Flag, and an Arg
        # is a different type, so a positional inside a scope is
        # UNCONSTRUCTABLE in Go rather than merely unraised -- the same class
        # errSelectorDefaultIncomplete and errMemberFlagPresence are in.
        # Python reaches it through an Arg in the choice class's body and
        # TypeScript through a widened flags map; cases/selector_registration.json
        # asserts it on those two targets.
        'go': 'excluded:Choice(name, help, flags ...Flag) is variadic over Flag and an Arg is a different type, so a scoped positional is unconstructable in Go rather than refused (the class §18.19 item 213 records)',
        'typescript': "excluded:TypeScript parameterizes the `Choice \"c\" of \"sel\": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signature carries the same sentence",
    },
    'Choice * of *: help text is required': {
        'typescript': 'excluded:TypeScript parameterizes the `Choice "c" of "sel": ` prefix into a choicePrefix() helper where the siblings inline it; the inlined signatures carry the same sentence',
    },
    'Choice * of *: a token-spelled choice cannot carry a payload: the token names the choice, and a choice that carries its own value belongs to a member-spelled choice flag, declared with *': {
        'go': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
        'typescript': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
    },
    'Choice * of *: a token-spelled choice cannot carry a payload: the token names the choice, and a choice that carries its own value belongs to a member-spelled choice flag, declared with MemberChoiceFlag(...)': {
        'python': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
        'typescript': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
    },
    'Flag *: a choice flag cannot declare *: an absent selection is a choice nobody named, so name it as a choice of its own': {
        'go': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
    },
    'Flag *: a choice flag cannot declare Optional(): an absent selection is a choice nobody named, so name it as a choice of its own': {
        'python': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
        'typescript': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
    },
    'Flag *: * names no declared choice: must be one of: *': {
        'go': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
    },
    'Flag *: Default(*) names no declared choice: must be one of: *': {
        'python': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
        'typescript': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
    },
    'Flag *: * elects choice *, whose flag carries a value nothing supplies: only a payload-less member can be a default': {
        'go': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
    },
    'Flag *: Default(*) elects choice *, whose flag carries a value nothing supplies: only a payload-less member can be a default': {
        'python': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
        'typescript': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
    },
    "Flag *: * elects choice *, whose scope declares the required flag '--*': a defaulted selection must be complete with nothing typed": {
        'go': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
        'python': "excluded:Python's default IS a choice instance (contract §24.5), so an incomplete defaulted selection is unconstructable rather than refused -- there is nothing to check and no error to raise",
    },
    "Flag *: Default(*) elects choice *, whose scope declares the required flag '--*': a defaulted selection must be complete with nothing typed": {
        'typescript': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
        'python': "excluded:Python's default IS a choice instance (contract §24.5), so an incomplete defaulted selection is unconstructable rather than refused -- there is nothing to check and no error to raise",
    },
    'Choice * of *: a member flag must declare Required(), read as required once this member is elected': {
        'typescript': 'excluded:the selector family pins ONE sentence in three spellings (contract §12.13), and each implementation carries only its own; cases/selector_registration.json and cases/choice_records.json assert all three, per target',
        'python': "excluded:member_value(help=...) takes no presence keyword and a frozen dataclass's field is required by construction, so the refused state is unconstructable in Python (contract §18.19 item 213)",
    },
    'Choice * of *: a member-spelled choice flag declares its choices with MemberChoice(...), which names the flag that elects the choice': {
        'python': 'excluded:Go-only (contract §12.13, §18.19 item 220): ChoiceFlag and MemberChoiceFlag are twins over one FlagOption interface, so a plain Choice(...) can reach the member-spelled constructor; Python spells member election with a keyword on the selector and has no such input',
        'typescript': "excluded:Go-only (contract §12.13, §18.19 item 220): ChoiceFlag and MemberChoiceFlag are twins over one FlagOption interface, so a plain Choice(...) can reach the member-spelled constructor; TypeScript's factory takes its own choice shape and has no such input",
    },
    'Choice * of *: a choice value belongs to exactly one choice flag; it is already declared by *': {
        'python': "excluded:Go-only (contract §12.13, §18.19 item 220): a Go choice is a VALUE WITH IDENTITY, so the same *ChoiceDecl can be written into two selectors; Python's choice classes have no aliasing site",
        'typescript': "excluded:Go-only (contract §12.13, §18.19 item 220): a Go choice is a VALUE WITH IDENTITY, so the same *ChoiceDecl can be written into two selectors; TypeScript's keyed map has no aliasing site",
    },
    'strictcli.GetElected: no such key *': {
        'python': "excluded:Go-only (contract §12.13, §18.19 item 220): GetElected is Go's typed accessor over the kwargs map; Python and TypeScript deliver the record as a named handler value",
        'typescript': "excluded:Go-only (contract §12.13, §18.19 item 220): GetElected is Go's typed accessor over the kwargs map; Python and TypeScript deliver the record as a named handler value",
    },
    'strictcli.GetElected: key * has dynamic type *, want *strictcli.Elected': {
        'python': "excluded:Go-only (contract §12.13, §18.19 item 220): GetElected is Go's typed accessor over the kwargs map",
        'typescript': "excluded:Go-only (contract §12.13, §18.19 item 220): GetElected is Go's typed accessor over the kwargs map",
    },
    'strictcli.Match: case * is not a choice of choice flag *': {
        'python': "excluded:Go-only (contract §12.13, §18.19 item 220): Match is exhaustive AT DISPATCH because Go has no sealed union (contract §24.12); Python's match and TypeScript's switch are checked by their type systems",
        'typescript': "excluded:Go-only (contract §12.13, §18.19 item 220): Match is exhaustive AT DISPATCH because Go has no sealed union (contract §24.12); Python's match and TypeScript's switch are checked by their type systems",
    },
    'strictcli.Match: choice * of choice flag * has two cases': {
        'python': 'excluded:Go-only (contract §12.13, §18.19 item 220): Match is exhaustive at dispatch; the sibling languages have no MatchCase list to duplicate',
        'typescript': 'excluded:Go-only (contract §12.13, §18.19 item 220): Match is exhaustive at dispatch; the sibling languages have no MatchCase list to duplicate',
    },
    'strictcli.Match: choice flag * has no case for *': {
        'python': 'excluded:Go-only (contract §12.13, §18.19 item 220): Match is exhaustive at dispatch; the sibling languages have no MatchCase list to leave incomplete',
        'typescript': 'excluded:Go-only (contract §12.13, §18.19 item 220): Match is exhaustive at dispatch; the sibling languages have no MatchCase list to leave incomplete',
    },
    "--*: a choice flag's value must be strictcli.Elect(<choice>, ...) or a choice name, got *": {
        'python': "excluded:Go-only (contract §12.13, §18.19 item 220): Call() takes an Elect(choice, Fields) value in Go; Python's twin refusal names a choice INSTANCE and is its own Python-only template",
        'typescript': "excluded:Go-only (contract §12.13, §18.19 item 220): Call() takes an Elect(choice, Fields) value in Go; TypeScript's call takes the union member object, which the type system checks",
    },
    '--*: elected value names choice *, which is not declared by this choice flag': {
        'python': "excluded:Go-only (contract §12.13, §18.19 item 220, ratified as a permanent split by §18.25 item 251): a Go record holds a declared choice of SOME selector, so it can say the choice is real and belongs elsewhere; Python's call() takes an instance of a declared choice class and TypeScript's a string tag, and neither is holding a declaration. cases/record_door_divergences.json asserts the refusal on every target and acknowledges the sentence.",
        'typescript': "excluded:Go-only (contract §12.13, §18.19 item 220, ratified as a permanent split by §18.25 item 251): a Go record holds a declared choice of SOME selector, so it can say the choice is real and belongs elsewhere; Python's call() takes an instance of a declared choice class and TypeScript's a string tag, and neither is holding a declaration. cases/record_door_divergences.json asserts the refusal on every target and acknowledges the sentence.",
    },
    'schema value of unserializable type: *': {
        'python': "excluded:Go-only (contract §12.13, §18.19 item 220): v2's ordered writer is hand-written in Go and must reject a value its type switch does not know; Python's json.dumps and TypeScript's writer have their own paths",
        'typescript': "excluded:Go-only (contract §12.13, §18.19 item 220): v2's ordered writer is hand-written in Go and must reject a value its type switch does not know; TypeScript's writer is hand-written but typed",
    },
    'Flag *: elect_by is undeclared: declare elect_by=* or elect_by=*': {
        'go': 'excluded:Python-only (contract §12.13, §18.19 item 220): elect_by is a keyword string in Python; Go and TypeScript spell the two elections as twin constructors, so the undeclared state is unrepresentable',
        'typescript': 'excluded:Python-only (contract §12.13, §18.19 item 220): elect_by is a keyword string in Python; Go and TypeScript spell the two elections as twin constructors, so the undeclared state is unrepresentable',
    },
    'Flag *: elect_by must be * or *, got *': {
        'go': 'excluded:Python-only (contract §12.13, §18.19 item 220): only a keyword taking a string can carry a mis-spelled value',
        'typescript': 'excluded:Python-only (contract §12.13, §18.19 item 220): only a keyword taking a string can carry a mis-spelled value',
    },
    'Flag *: choices entry * is *, not a choice class: declare it with @choice(...)': {
        'go': "excluded:Python-only (contract §12.13, §18.19 item 220): Python's choices= takes decorated classes; Go's variadic takes *ChoiceDecl and TypeScript's map takes ChoiceDef, both compile-checked",
        'typescript': "excluded:Python-only (contract §12.13, §18.19 item 220): Python's choices= takes decorated classes; Go's variadic takes *ChoiceDecl and TypeScript's map takes ChoiceDef, both compile-checked",
    },
    'Choice * of *: field * declares no flag: declare it with sub_flag(...), sub_choice_flag(...) or member_value(...)': {
        'go': "excluded:Python-only (contract §12.13, §18.19 item 220): a scope is a dataclass body in Python, so a field can exist without a declaration; Go's and TypeScript's scopes are flag lists",
        'typescript': "excluded:Python-only (contract §12.13, §18.19 item 220): a scope is a dataclass body in Python, so a field can exist without a declaration; Go's and TypeScript's scopes are flag lists",
    },
    "Choice * of *: member_value(...) declares the payload on field *: a member-spelled choice's payload is delivered under the reserved name 'value'": {
        'go': "excluded:Python-only (contract §12.13, §18.19 item 220): Python's payload is a NAMED dataclass field, so it can be misplaced; Go's is the member flag itself and TypeScript's is the choice record's `value` key",
        'typescript': "excluded:Python-only (contract §12.13, §18.19 item 220): Python's payload is a NAMED dataclass field, so it can be misplaced; Go's is the member flag itself and TypeScript's is the choice record's `value` key",
    },
    'Choice * of *: the annotation of field * cannot be resolved at registration: a choice class must be importable at run time, not only under TYPE_CHECKING': {
        'go': 'excluded:Python-only (contract §12.13, §18.19 item 220): only Python resolves annotations at registration',
        'typescript': 'excluded:Python-only (contract §12.13, §18.19 item 220): only Python resolves annotations at registration',
    },
    "Choice * of *: field * is bound to choice flag '--*' and must be annotated *, got *": {
        'go': 'excluded:Python-only (contract §12.13, §18.19 item 220): only Python has a per-field annotation to check',
        'typescript': 'excluded:Python-only (contract §12.13, §18.19 item 220): only Python has a per-field annotation to check',
    },
    'Flag *: * must be an instance of a declared choice class, got *': {
        'go': "excluded:Python-only (contract §12.13, §18.19 item 220): Python's default IS a choice instance (contract §24.5); Go's and TypeScript's default NAMES a choice",
        'typescript': "excluded:Python-only (contract §12.13, §18.19 item 220): Python's default IS a choice instance (contract §24.5); Go's and TypeScript's default NAMES a choice",
    },
    'command *: a command declaring a choice flag cannot use a **kwargs handler: the elected value must reach a named, annotated parameter': {
        'go': "excluded:Python-only (contract §12.13, §18.19 item 220): Go's handler receives one map[string]interface{} and TypeScript's one inferred args object, so neither has a per-parameter annotation to check",
        'typescript': "excluded:Python-only (contract §12.13, §18.19 item 220): Go's handler receives one map[string]interface{} and TypeScript's one inferred args object, so neither has a per-parameter annotation to check",
    },
    'command *: handler parameter * annotation * cannot be resolved at registration: a choice class must be importable at run time, not only under TYPE_CHECKING': {
        'go': 'excluded:Python-only (contract §12.13, §18.19 item 220): only Python resolves handler annotations at registration',
        'typescript': 'excluded:Python-only (contract §12.13, §18.19 item 220): only Python resolves handler annotations at registration',
    },
    "command *: handler parameter * is bound to choice flag '--*' and must be annotated *, got *": {
        'go': 'excluded:Python-only (contract §12.13, §18.19 item 220): only Python has a per-parameter annotation to check, which is what makes assert_never sound',
        'typescript': 'excluded:Python-only (contract §12.13, §18.19 item 220): only Python has a per-parameter annotation to check, which is what makes assert_never sound',
    },
    "parameter * for command * must be an instance of a declared choice of '--*' (*), got *": {
        'go': "excluded:Python-only (contract §12.13, §18.19 item 220, ratified as a permanent split by §18.25 item 251): a Python record is an INSTANCE of a class, so its refusal names the type it got against the union it declared; a Go record holds a *ChoiceDecl and a TypeScript record a string tag, and each names what its runtime is holding. cases/record_door_divergences.json asserts the refusal on every target and acknowledges the sentence.",
        'typescript': "excluded:Python-only (contract §12.13, §18.19 item 220, ratified as a permanent split by §18.25 item 251): a Python record is an INSTANCE of a class, so its refusal names the type it got against the union it declared; a Go record holds a *ChoiceDecl and a TypeScript record a string tag, and each names what its runtime is holding. cases/record_door_divergences.json asserts the refusal on every target and acknowledges the sentence.",
    },
    '--*: config value error: expected str, got *': {
        'go': 'excluded:Python-only (contract §12.13, §18.19 item 220): Python coerces a config-sourced election through its own typed reader; Go and TypeScript reuse the ordinary config-value error',
        'typescript': 'excluded:Python-only (contract §12.13, §18.19 item 220): Python coerces a config-sourced election through its own typed reader; Go and TypeScript reuse the ordinary config-value error',
        'python': "coverage_deferred:needs a config file whose selector key holds a non-string, which the case schema's config_content spells but no case asserts yet",
    },
    "flag '--*' is required**": {
        'go': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets",
        'typescript': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets",
    },
    'one of * is required**': {
        'go': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets",
        'typescript': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets",
    },
    'one of * is required*': {
        'python': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets",
    },
    'one of * is required': {
        'go': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets",
        'typescript': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets",
    },
    "flag '--*' requires a value": {
        'go': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets, as does cases/selector_flat_boundary.json for the flat door's missing-payload refusal (contract §18.22 item 233)",
        'typescript': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets, as does cases/selector_flat_boundary.json for the flat door's missing-payload refusal (contract §18.22 item 233)",
    },
    "flag '--*' is a boolean flag and does not take a value": {
        'go': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets",
        'typescript': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets",
    },
    "flag '--*' is a boolean negation and does not take a value": {
        'go': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets",
        'typescript': "excluded:Python's scoped parse sites interpolate the scope and origin suffixes into the same template, so its signature carries the extra placeholders; the sentence is identical and cases/selector_scope.json asserts it on all three targets",
    },

    # =======================================================================
    # The constraint system (contract §12.15, §26; ledger §18.30, §18.31)
    #
    # The same three shapes of divergence the selector block above records,
    # one construct over:
    #
    #   1. ONE SENTENCE, THREE SPELLINGS. The four member-naming guards write
    #      a per-language spelling inside the sentence (§12.12's mechanism,
    #      ratified for all four by §18.31 item 287), so each implementation
    #      carries a signature its siblings genuinely do not. The parity
    #      assertion is per target, in cases/constraint_registration.json.
    #
    #   2. ONE SENTENCE, ONE PARAMETERIZED PREFIX. Python and TypeScript carry
    #      `constraint "<c>": ` as a template of its own and compose it; Go
    #      inlines it into every sentence. Python additionally composes the
    #      two dependency families' sentences at the call site, so neither
    #      half is a template the extractor can see. cases/constraints.json
    #      asserts the composed sentences on all three targets.
    #
    #   3. LANGUAGE-SPECIFIC TEMPLATES. `errConstraintMinMembers` and
    #      `errConstraintMemberNotRecord` are Go-excluded by construction --
    #      Go's constructors take two named members before the variadic tail
    #      and its member is a typed value, so both states are compile errors
    #      (§12.15's exclusion list, §18.31 item 288). Python authored three
    #      more that only a keyword taking a string or an untyped sequence can
    #      reach.
    # =======================================================================

    # -- (2) the prefix, and the two sentences composed around it --
    'constraint *: ': {
        'go': 'excluded:Python and TypeScript carry the `constraint "<c>": ` prefix as a template of its own; Go inlines it into each sentence, so there is no prefix template to extract',
    },
    'constraint *: * must be used together': {
        'typescript': 'excluded:TypeScript composes the prefix through constraintPrefix(c), so its signature carries a leading interpolation where Python and Go carry the literal; cases/constraints.json asserts the composed sentence on all three targets',
    },
    '** must be used together': {
        'python': 'excluded:TypeScript composes the prefix through constraintPrefix(c), so its signature carries a leading interpolation where Python and Go carry the literal; cases/constraints.json asserts the composed sentence on all three targets',
        'go': 'excluded:TypeScript composes the prefix through constraintPrefix(c), so its signature carries a leading interpolation where Python and Go carry the literal; cases/constraints.json asserts the composed sentence on all three targets',
    },
    'constraint *: at least one of * is required*': {
        'typescript': 'excluded:TypeScript composes the prefix through constraintPrefix(c), so its signature carries a leading interpolation where Python and Go carry the literal; cases/constraints.json asserts the composed sentence, decline clause included, on all three targets',
    },
    '*at least one of * is required*': {
        'python': 'excluded:TypeScript composes the prefix through constraintPrefix(c), so its signature carries a leading interpolation where Python and Go carry the literal; cases/constraints.json asserts the composed sentence, decline clause included, on all three targets',
        'go': 'excluded:TypeScript composes the prefix through constraintPrefix(c), so its signature carries a leading interpolation where Python and Go carry the literal; cases/constraints.json asserts the composed sentence, decline clause included, on all three targets',
    },
    "constraint *: flag '--*' requires '--*'": {
        'python': "excluded:Python composes the prefix and this sentence at the CALL site (§26.13 leaves the sentence byte-identical after the prefix), so neither half is a template of its own; Go inlines the whole sentence and TypeScript composes its own prefix. cases/constraints.json asserts the composed sentence on all three targets",
        'typescript': "excluded:TypeScript composes the prefix through constraintPrefix(c) where Go inlines it; cases/constraints.json asserts the composed sentence on all three targets",
    },
    "*flag '--*' requires '--*'": {
        'python': "excluded:Python composes the prefix and this sentence at the CALL site, so neither half is a template of its own; cases/constraints.json asserts the composed sentence on all three targets",
        'go': "excluded:TypeScript composes the prefix through constraintPrefix(c) where Go inlines it into the sentence; cases/constraints.json asserts the composed sentence on all three targets",
    },
    "constraint *: flag '--*' implies '--**', but '--**' was explicitly provided": {
        'python': "excluded:Python composes the prefix and this sentence at the CALL site (§26.13 leaves the sentence byte-identical after the prefix), so neither half is a template of its own; cases/constraints.json asserts the composed sentence on all three targets",
        'typescript': "excluded:TypeScript composes the prefix through constraintPrefix(c) where Go inlines it; cases/constraints.json asserts the composed sentence on all three targets",
    },
    "*flag '--*' implies '--**', but '--**' was explicitly provided": {
        'python': "excluded:Python composes the prefix and this sentence at the CALL site, so neither half is a template of its own; cases/constraints.json asserts the composed sentence on all three targets",
        'go': "excluded:TypeScript composes the prefix through constraintPrefix(c) where Go inlines it into the sentence; cases/constraints.json asserts the composed sentence on all three targets",
    },

    # -- (1) the four member-naming guards, one sentence per language --
    'command *: constraint * member * declares *: a member the invocation must always supply leaves the constraint nothing to decide': {
        'python': 'excluded:the constraint family pins ONE sentence in three spellings (§12.15, §18.31 item 287), and each implementation carries only its own; cases/constraint_registration.json asserts all three, per target',
        'go': 'excluded:the constraint family pins ONE sentence in three spellings (§12.15, §18.31 item 287), and each implementation carries only its own; cases/constraint_registration.json asserts all three, per target',
    },
    'command *: constraint * member * declares Required(): a member the invocation must always supply leaves the constraint nothing to decide': {
        'python': "excluded:Go's spelling. §18.31 item 287 records that Go prints the FLAG spelling Required() for an arg member too: the template's prefix names the constraint rather than a surface, so it never claims to quote the arg surface's own option. cases/constraint_registration.json asserts all three, per target",
        'typescript': "excluded:Go's spelling; cases/constraint_registration.json asserts all three, per target",
    },
    'command *: constraint * member * declares presence="required": a member the invocation must always supply leaves the constraint nothing to decide': {
        'go': "excluded:Python's spelling; cases/constraint_registration.json asserts all three, per target",
        'typescript': "excluded:Python's spelling; cases/constraint_registration.json asserts all three, per target",
    },
    'command *: constraint * member * is a bool and must declare its election: * counts only a true value, * counts any': {
        'python': "excluded:TypeScript's spelling, interpolated from its WHEN_* constants; cases/constraint_registration.json asserts all three, per target",
        'go': "excluded:TypeScript's spelling, interpolated from its WHEN_* constants; cases/constraint_registration.json asserts all three, per target",
    },
    'command *: constraint * member * is a bool and must declare its election: WhenTrue() counts only a true value, WhenPresent() counts any': {
        'python': "excluded:Go's spelling; cases/constraint_registration.json asserts all three, per target",
        'typescript': "excluded:Go's spelling; cases/constraint_registration.json asserts all three, per target",
    },
    'command *: constraint * member * is a bool and must declare its election: when="true" counts only a true value, when="present" counts any': {
        'go': "excluded:Python's spelling; cases/constraint_registration.json asserts all three, per target",
        'typescript': "excluded:Python's spelling; cases/constraint_registration.json asserts all three, per target",
    },
    'command *: constraint * member * declares *, which needs a bool; * is a *': {
        'python': "excluded:TypeScript's spelling; cases/constraint_registration.json asserts all three, per target, over the whole closed `<t>` vocabulary §18.31 item 289 pins",
        'go': "excluded:TypeScript's spelling; cases/constraint_registration.json asserts all three, per target, over the whole closed `<t>` vocabulary §18.31 item 289 pins",
    },
    'command *: constraint * member * declares WhenTrue(), which needs a bool; * is a *': {
        'python': "excluded:Go's spelling; cases/constraint_registration.json asserts all three, per target",
        'typescript': "excluded:Go's spelling; cases/constraint_registration.json asserts all three, per target",
    },
    'command *: constraint * member * declares when="true", which needs a bool; * is a *': {
        'go': "excluded:Python's spelling; cases/constraint_registration.json asserts all three, per target",
        'typescript': "excluded:Python's spelling; cases/constraint_registration.json asserts all three, per target",
    },
    'command *: constraint * member * declares *, which needs a string or a collection; * is a *': {
        'python': "excluded:TypeScript's spelling; cases/constraint_registration.json asserts all three, per target",
        'go': "excluded:TypeScript's spelling; cases/constraint_registration.json asserts all three, per target",
    },
    'command *: constraint * member * declares WhenNonEmpty(), which needs a string or a collection; * is a *': {
        'python': "excluded:Go's spelling; cases/constraint_registration.json asserts all three, per target",
        'typescript': "excluded:Go's spelling; cases/constraint_registration.json asserts all three, per target",
    },
    'command *: constraint * member * declares when="non_empty", which needs a string or a collection; * is a *': {
        'go': "excluded:Python's spelling; cases/constraint_registration.json asserts all three, per target",
        'typescript': "excluded:Python's spelling; cases/constraint_registration.json asserts all three, per target",
    },

    # -- (3) the two Go-excluded guards, and Python's three record guards --
    'command *: constraint * must declare at least two members, got *': {
        'go': "excluded:Go-excluded by construction (§12.15, §18.30 item 275): AtLeastOne/AllOrNone take two named members before the variadic tail, so a one-member constraint does not COMPILE. cases/registration_errors.json asserts it on Python and TypeScript",
    },
    'command *: constraint * member * is a bare name: declare it as *': {
        'python': "excluded:TypeScript's spelling, rendered with the placeholder literal `{ name: \"<x>\" }` (§18.31 item 288); cases/constraint_registration.json asserts both reachable targets",
        'go': "excluded:Go-excluded by construction (§12.15): its member is a typed ConstraintMember, so a bare name does not COMPILE",
    },
    'command *: constraint * member * is a bare name: declare it as Member("<x>")': {
        'go': "excluded:Go-excluded by construction (§12.15): its member is a typed ConstraintMember, so a bare name does not COMPILE",
        'typescript': "excluded:Python's spelling, rendered with the placeholder literal (§18.31 item 288); cases/constraint_registration.json asserts both reachable targets",
    },
    'Member *: when must be "present", "true" or "non_empty", got *': {
        'go': 'excluded:Python-only (§12.15, §18.31 item 288): only a string-taking keyword can reach it. Go declares the selector with WhenTrue()/WhenPresent()/WhenNonEmpty() and TypeScript with a literal union, so a typo is a compile error in both',
        'typescript': 'excluded:Python-only (§12.15, §18.31 item 288): only a string-taking keyword can reach it. Go declares the selector with WhenTrue()/WhenPresent()/WhenNonEmpty() and TypeScript with a literal union, so a typo is a compile error in both',
    },
    'Member name must be a non-empty string': {
        'go': "excluded:Python-only: Member is a frozen dataclass validating its own field where Go's Member(name string) and TypeScript's `{ name: string }` are checked by the type system, and an empty name reaches the shared errConstraintMemberUnknown there",
        'typescript': "excluded:Python-only: Member is a frozen dataclass validating its own field where Go's Member(name string) and TypeScript's `{ name: string }` are checked by the type system, and an empty name reaches the shared errConstraintMemberUnknown there",
    },
    'command *: constraints must be AtLeastOne, AllOrNone, Requires or Implies declarations, got *': {
        'go': "excluded:Python-only: Go's closed Constraint interface and TypeScript's Constraint union make a non-constraint entry a compile error",
        'typescript': "excluded:Python-only: Go's closed Constraint interface and TypeScript's Constraint union make a non-constraint entry a compile error",
    },
    'constraint *: members must be a list of * records': {
        'go': "excluded:Python-only: `members` is a typed variadic in Go and a `[M, M, ...M[]]` tuple in TypeScript, so a non-sequence does not compile",
        'typescript': "excluded:Python-only: `members` is a typed variadic in Go and a `[M, M, ...M[]]` tuple in TypeScript, so a non-sequence does not compile",
    },

    # =======================================================================
    # The update-command construct (contract §12.16, §27; ledger §18.33,
    # §18.34 item 327)
    #
    # ONE SHAPE OF DIVERGENCE, AND ONLY ONE. Item 327 struck §12.16's original
    # "no template in this section is excluded in any implementation" and
    # replaced it with the claim that survives: no template here names a state
    # only one surface can reach. What was false was the conclusion, because
    # FOUR of the seventeen templates quote a DECLARATION SPELLING inside the
    # sentence -- `errMutatingDefault` (<default-spelling>, plus
    # <required-spelling> and <optional-spelling> in its remedy clause),
    # `errUpdatePropertyPresence` (<required-spelling>, <optional-spelling>),
    # `errNullableNotProperty` and `errUnsetNameReserved` (<nullable-spelling>).
    #
    # That is §12.12's mechanism, which §12.16 invokes by reusing that
    # section's three rows and adding <nullable-spelling> to them: the
    # sentence is byte-identical and the spellings inside it are each
    # language's own. This checker compares literal strings and has no
    # substitution model, so each spelling appears in one implementation's
    # extraction and not in its siblings'. The parity assertion is per target,
    # in cases/update_registration.json and
    # cases/update_mutating_default_ban.json.
    #
    # Where Python and TypeScript interpolate their spelling from a constant
    # they share a normalized signature, so only Go's literal needs an
    # exclusion; where all three spell it differently, all three signatures
    # appear. The remaining thirteen templates -- eleven registration guards
    # and both parse-time violations -- are cross-language and take no
    # exclusion at all.
    # =======================================================================

    # -- errMutatingDefault (§27.1), the one guard here that fires on a
    #    command declaring no update at all --
    'command *: * declares * on a mutating command: absence would write a value the invocation never stated (declare presence="required" or presence="optional", or apply the fallback in the handler and say so in its help)': {
        'go': "excluded:Python's spelling (§12.16, §18.34 item 327); cases/update_mutating_default_ban.json asserts all three, per target",
        'typescript': "excluded:Python's spelling (§12.16, §18.34 item 327); cases/update_mutating_default_ban.json asserts all three, per target",
    },
    'command *: * declares Default(*) on a mutating command: absence would write a value the invocation never stated (declare Required() or Optional(), or apply the fallback in the handler and say so in its help)': {
        'python': "excluded:Go's spelling (§12.16, §18.34 item 327). §12.16 records that Go prints the FLAG spelling Default(<v>) for an arg too: the template's prefix names the command rather than a surface. cases/update_mutating_default_ban.json asserts all three, per target",
        'typescript': "excluded:Go's spelling (§12.16, §18.34 item 327); cases/update_mutating_default_ban.json asserts all three, per target",
    },
    'command *: * declares * on a mutating command: absence would write a value the invocation never stated (declare * or *, or apply the fallback in the handler and say so in its help)': {
        'python': "excluded:TypeScript's spelling, interpolated from its PRESENCE_SPELLING_* constants (§12.16, §18.34 item 327); cases/update_mutating_default_ban.json asserts all three, per target",
        'go': "excluded:TypeScript's spelling, interpolated from its PRESENCE_SPELLING_* constants (§12.16, §18.34 item 327); cases/update_mutating_default_ban.json asserts all three, per target",
    },

    # -- errUpdatePropertyPresence (§27.3) --
    'command *: update of * property * declares presence="required": a property is absent exactly when it is not being written, and the presence declaration for that is presence="optional"': {
        'go': "excluded:Python's spelling (§12.16, §18.34 item 327); cases/update_registration.json asserts all three, per target",
        'typescript': "excluded:Python's spelling (§12.16, §18.34 item 327); cases/update_registration.json asserts all three, per target",
    },
    'command *: update of * property * declares Required(): a property is absent exactly when it is not being written, and the presence declaration for that is Optional()': {
        'python': "excluded:Go's spelling (§12.16, §18.34 item 327); cases/update_registration.json asserts all three, per target",
        'typescript': "excluded:Go's spelling (§12.16, §18.34 item 327); cases/update_registration.json asserts all three, per target",
    },
    'command *: update of * property * declares *: a property is absent exactly when it is not being written, and the presence declaration for that is *': {
        'python': "excluded:TypeScript's spelling, interpolated from its PRESENCE_SPELLING_* constants (§12.16, §18.34 item 327); cases/update_registration.json asserts all three, per target",
        'go': "excluded:TypeScript's spelling, interpolated from its PRESENCE_SPELLING_* constants (§12.16, §18.34 item 327); cases/update_registration.json asserts all three, per target",
    },

    # -- errNullableNotProperty (§27.6). Python and TypeScript interpolate
    #    their <nullable-spelling> from a constant, so they normalize to one
    #    signature and only Go's literal Nullable() needs an exclusion. --
    'command *: * declares * but is not a property of an update: only a property can be cleared': {
        'go': "excluded:the Python and TypeScript spellings, each interpolated from that implementation's <nullable-spelling> constant (§12.16, §18.34 item 327); cases/update_registration.json asserts all three, per target",
    },
    'command *: * declares Nullable() but is not a property of an update: only a property can be cleared': {
        'python': "excluded:Go's spelling (§12.16, §18.34 item 327); cases/update_registration.json asserts all three, per target",
        'typescript': "excluded:Go's spelling (§12.16, §18.34 item 327); cases/update_registration.json asserts all three, per target",
    },

    # -- errUnsetNameReserved (§27.6), the step that runs last because it is
    #    the only one reading the flag namespace back --
    'command *: flag name "unset-*" is reserved: property \'--*\' declares *, which mints \'--unset-*\'': {
        'go': "excluded:the Python and TypeScript spellings, each interpolated from that implementation's <nullable-spelling> constant (§12.16, §18.34 item 327); cases/update_registration.json asserts all three, per target",
    },
    'command *: flag name "unset-*" is reserved: property \'--*\' declares Nullable(), which mints \'--unset-*\'': {
        'python': "excluded:Go's spelling (§12.16, §18.34 item 327); cases/update_registration.json asserts all three, per target",
        'typescript': "excluded:Go's spelling (§12.16, §18.34 item 327); cases/update_registration.json asserts all three, per target",
    },

}


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _extract_raise_arg(source: str, pos: int) -> str | None:
    """Given a position right after 'raise ExcType(', extract the argument.

    Uses parenthesis counting to find the matching ')'.
    Returns the content between the parens (exclusive).
    """
    depth = 1
    i = pos
    while i < len(source) and depth > 0:
        ch = source[i]
        if ch == "(":
            depth += 1
        elif ch == ")":
            depth -= 1
        elif ch in ('"', "'"):
            # Skip over string literals
            quote = ch
            i += 1
            while i < len(source) and source[i] != quote:
                if source[i] == "\\":
                    i += 1  # skip escaped char
                i += 1
        i += 1
    if depth == 0:
        return source[pos : i - 1]
    return None


def _extract_string_literals(arg_text: str) -> tuple[str, bool] | None:
    """Extract and concatenate all string literal pieces from a raise argument.

    Handles: f"...", f'...', "...", '...' and implicit concatenation.
    Returns ``(template, truncated)`` or None if no strings found.

    ``truncated`` is True when the literal run stopped at a non-string
    expression (``f"..." + ", ".join(parts)``), meaning the real message
    continues with dynamic text the extractor cannot see.  Callers append a
    placeholder for it.  A complete literal that merely ENDS with a space (the
    confirm prompt, `'... Proceed? [y/N] '`) is not truncated and gets none --
    the distinction has to be made here, where the parse state is known, not
    guessed from the trailing character later.
    """
    parts: list[str] = []
    i = 0
    text = arg_text.strip()

    # The argument must BEGIN with a string literal (optionally f-prefixed).
    # Otherwise it is a variable expression (e.g., raise _ParseError(err) or
    # raise _ParseError(pre_scan["err"])) whose embedded string literals are
    # subscript keys, not error message text.
    if not text or text[0] not in ('"', "'", "f") or (
        text[0] == "f" and (len(text) < 2 or text[1] not in ('"', "'"))
    ):
        return None

    truncated = False
    while i < len(text):
        # Skip whitespace and newlines
        if text[i] in " \t\n\r":
            i += 1
            continue

        # Check for f-string prefix
        if text[i] == "f" and i + 1 < len(text) and text[i + 1] in ('"', "'"):
            i += 1
            # Fall through to string extraction below

        if text[i] in ('"', "'"):
            quote = text[i]
            i += 1
            part = []
            while i < len(text) and text[i] != quote:
                if text[i] == "\\":
                    part.append(text[i + 1])
                    i += 2
                else:
                    part.append(text[i])
                    i += 1
            if i < len(text):
                i += 1  # skip closing quote
            parts.append("".join(part))
            continue

        # A separator the caller does not care about (a comma before the next
        # argument, a closing paren) ends the literal run cleanly; anything
        # else -- the + operator, ", ".join(...) -- means the message continues
        # with text we cannot see.
        if parts:
            truncated = text[i] not in ",)"
            break
        i += 1

    if not parts:
        return None
    return "".join(parts), truncated


# ---------------------------------------------------------------------------
# 1. Extract error patterns from Python source
# ---------------------------------------------------------------------------

# The exception types whose messages are user-facing templates, and the
# category each one lands in.  `_ParseError` is the parse-time formatter;
# `ValueError` is registration-time (raised while an app is being built);
# `EffectFailed` is a call-time effect failure (effects contract §12.8), which
# a conformance case reaches through argv like any parse-time error, so it
# shares that category and is coverage-checked.
#
# TypeError and RuntimeError are deliberately NOT scanned -- SIGNATURE_STATUS
# carries rationales that depend on that ("TypeError raises are not
# extracted").  A template carried on one of those exceptions is made visible
# by giving it a `_msg_*` function, below.
_PY_RAISED_TEMPLATE_TYPES = {
    "_ParseError": "parse",
    "ValueError": "registration",
    "EffectFailed": "parse",
}

# Templates that are never raised through a scanned exception type: printed to
# stderr (the confirm protocol, §12.6), or carried on `_DryRunTruncated`
# (§12.5), `TypeError` or `RuntimeError`.  Python spells these as `_msg_*`
# functions returning the finished string -- one function per template,
# mirroring Go's `err*`/`prompt*` functions in errors.go and their TypeScript
# twins.
_PY_MSG_FUNC_PAT = re.compile(r"^def (_msg_\w+)\(", re.MULTILINE)

# The `_msg_*` functions whose messages are parse-time.  Go and TypeScript
# express the same split with a "(parse-time)" section header; Python has no
# sections, so the membership is listed.  It is the confirm protocol (§12.6,
# which TypeScript files under its own parse-time header) plus the truncation
# error, which effects contract §12.5 pins to "the parse-time section of the
# catalogs", plus §12.8's option guard.  The remaining `_msg_*` templates are
# the effect-parameter type guards of §12.10, which the contract keeps in the
# registration-time section; they stay registration-time here too.
_PY_PARSE_TIME_MSG_FUNCS = frozenset({
    "_msg_confirm_prompt",
    "_msg_confirm_non_interactive",
    "_msg_confirm_declined",
    "_msg_dry_run_truncated",
    "_msg_dry_run_aborted",
    # §12.8's option guard. Python raises it as a TypeError (matching every
    # other call-time argument guard on the handle), so it needs a `_msg_*`
    # function to be visible at all -- and §12.8 is a parse-time section in
    # Go's and TypeScript's catalogs, so it is one here too.
    "_msg_effect_option_not_accepted",
    # The mutex decline clause (effects contract §21.4). Python spells it as a
    # `_msg_*` function because two raise sites share it; Go and TypeScript
    # carry it in their parse-time catalog sections, so it is parse-time here.
    "_msg_mutex_decline_clause",
})
_PY_TOP_LEVEL_DEF_PAT = re.compile(r"^(?:def |class |@)", re.MULTILINE)
_PY_RETURN_PAT = re.compile(r"^    return\s", re.MULTILINE)


def _truncation_marker(fmt_str: str, truncated: bool) -> str:
    """Append a placeholder when the literal run was cut short by an expression.

    The marker is a `{...}` field so normalize_python() turns it into the same
    `*` any other interpolation becomes.
    """
    return fmt_str + "{...}" if truncated else fmt_str


def extract_python_errors(source: str) -> list[tuple[str, str]]:
    """Extract (category, format_string) pairs from Python source.

    Two surfaces: raised templates (see _PY_RAISED_TEMPLATE_TYPES) and the
    `_msg_*` template functions (see _PY_MSG_FUNC_PAT).
    """
    results: list[tuple[str, str]] = []

    names = "|".join(_PY_RAISED_TEMPLATE_TYPES)
    pattern = re.compile(rf'raise\s+({names})\(')
    for m in pattern.finditer(source):
        category = _PY_RAISED_TEMPLATE_TYPES[m.group(1)]
        arg_start = m.end()
        arg_text = _extract_raise_arg(source, arg_start)
        if arg_text is None:
            continue
        extracted = _extract_string_literals(arg_text)
        if extracted is None:
            continue
        fmt_str, truncated = extracted
        results.append((category, _truncation_marker(fmt_str, truncated)))

    results.extend(extract_python_message_templates(source))
    return results


def extract_python_message_templates(source: str) -> list[tuple[str, str]]:
    """Extract (category, format_string) pairs from `_msg_*` functions.

    Each such function is a single `return <string literal>` (possibly an
    implicitly concatenated, parenthesized run of f-strings).  The body is
    bounded by the next top-level `def`/`class`/decorator, so a return inside a
    nested helper can never be mistaken for the template.
    """
    results: list[tuple[str, str]] = []

    for m in _PY_MSG_FUNC_PAT.finditer(source):
        nxt = _PY_TOP_LEVEL_DEF_PAT.search(source, m.end())
        body = source[m.end() : nxt.start() if nxt else len(source)]
        rm = _PY_RETURN_PAT.search(body)
        if rm is None:
            continue
        expr = body[rm.end():].lstrip()
        if expr.startswith("("):
            expr = expr[1:]
        extracted = _extract_string_literals(expr)
        if extracted is None:
            continue
        fmt_str, truncated = extracted
        category = (
            "parse" if m.group(1) in _PY_PARSE_TIME_MSG_FUNCS else "registration"
        )
        results.append((category, _truncation_marker(fmt_str, truncated)))

    return results


# ---------------------------------------------------------------------------
# 2. Extract error patterns from Go source
# ---------------------------------------------------------------------------

# Section header shared by the Go and TypeScript catalogs: a dashed line, one
# or more comment lines, a dashed line.  A continuation line may be a bare `//`
# (an empty comment line), which is how a section separates its title from its
# explanatory prose -- the pattern must not stop there, or the whole section is
# missed and its templates silently inherit the previous section's category.
SECTION_HEADER_PAT = re.compile(
    r'// -{10,}\n((?://.*\n)+?)// -{10,}\n',
)


def _section_category(header: str) -> str:
    """Classify a matched section header as 'parse' or 'registration'.

    The marker lives in the section's TITLE line (`... (parse-time)`,
    `... (TS-only; parse-time)`).  Only that line is consulted: a section's
    prose may legitimately name other sections and their categories, and
    scanning the whole block would misread such a mention as the section's own
    marker.
    """
    title = header.splitlines()[0]
    return "parse" if "parse-time" in title else "registration"


def extract_go_errors(errors_src: str) -> list[tuple[str, str]]:
    """Extract (category, format_string) pairs from Go source.

    All Go user-facing error templates are centralized in errors.go; it is
    the single extraction source.  errors.go contains fmt.Sprintf("...") and
    fmt.Errorf("...") patterns that were extracted from other source files,
    plus const string literals.  Templates are grouped into sections
    delimited by dashed header comments.  A section whose title line carries
    the "parse-time" marker holds parse-time errors; all other sections hold
    registration-time templates.
    """
    results: list[tuple[str, str]] = []

    # fmt.Sprintf("...") -- error format functions
    errors_sprintf = re.compile(
        r'fmt\.Sprintf\(\s*"((?:[^"\\]|\\.)*)"',
    )
    # fmt.Errorf("...") -- error-returning builders
    errorf_pat = re.compile(
        r'fmt\.Errorf\(\s*"((?:[^"\\]|\\.)*)"',
    )
    # const errXxx = "..." -- plain string constants
    const_err_pat = re.compile(
        r'^const\s+err\w+\s*=\s*"((?:[^"\\]|\\.)*)"',
        re.MULTILINE,
    )
    # Split errors.go into (category, body) segments. Content before the
    # first header (package clause, imports) has no templates but is
    # scanned as registration for uniformity.
    segments: list[tuple[str, str]] = []
    prev_end = 0
    prev_category = "registration"
    for hm in SECTION_HEADER_PAT.finditer(errors_src):
        segments.append((prev_category, errors_src[prev_end:hm.start()]))
        prev_category = _section_category(hm.group(1))
        prev_end = hm.end()
    segments.append((prev_category, errors_src[prev_end:]))
    for category, body in segments:
        for m in errors_sprintf.finditer(body):
            results.append((category, m.group(1)))
        for m in errorf_pat.finditer(body):
            results.append((category, m.group(1)))
        for m in const_err_pat.finditer(body):
            results.append((category, m.group(1)))

    return results


# ---------------------------------------------------------------------------
# 2b. Extract error patterns from TypeScript source
# ---------------------------------------------------------------------------

def extract_typescript_errors(errors_src: str) -> list[tuple[str, str]]:
    """Extract (category, format_string) pairs from TypeScript source.

    All TS user-facing error templates are centralized in errors.ts, which
    mirrors errors.go one-to-one: the same dashed section headers group the
    templates, and a header whose title line carries the "parse-time" marker
    marks every template in that section as a parse-time error.  Each named
    errXxx function returns a
    single template literal (or plain string literal); we extract the literal
    from each return statement.

    Content before the first section header (the error classes and the q()
    quoting helper) contains no error templates and is skipped entirely.
    """
    results: list[tuple[str, str]] = []

    # return `...`; / return "..."; / return '...';
    ret_backtick = re.compile(r'return\s+`((?:[^`\\]|\\.)*)`')
    ret_dq = re.compile(r'return\s+"((?:[^"\\]|\\.)*)"')
    ret_sq = re.compile(r"return\s+'((?:[^'\\]|\\.)*)'")
    # A CLAUSE template is empty in its degenerate case and carries text
    # otherwise, which TypeScript spells as a ternary whose false branch is the
    # literal -- `return path === "" ? "" : ` under '${path}'`;`. The scope
    # suffix and the origin wrapper are both that shape (contract §12.13), and
    # a reader that only sees `return <literal>` would report them as missing
    # in TypeScript alone.
    ret_ternary = re.compile(r':\s*`((?:[^`\\]|\\.)*)`\s*;')
    # A parameterless clause is a module constant rather than a function --
    # §12.13's `errElectionOriginDefault` is pinned as exactly that in Go too.
    const_dq = re.compile(r'export const err\w+\s*=\s*"((?:[^"\\]|\\.)*)"')
    segments: list[tuple[str, str]] = []
    prev_end: int | None = None
    prev_category = "registration"
    for hm in SECTION_HEADER_PAT.finditer(errors_src):
        if prev_end is not None:
            segments.append((prev_category, errors_src[prev_end:hm.start()]))
        # else: skip the pre-header preamble (classes + q() helper)
        prev_category = _section_category(hm.group(1))
        prev_end = hm.end()
    if prev_end is not None:
        segments.append((prev_category, errors_src[prev_end:]))

    for category, body in segments:
        for pat in (ret_backtick, ret_dq, ret_sq, ret_ternary, const_dq):
            for m in pat.finditer(body):
                results.append((category, m.group(1)))

    return results


# ---------------------------------------------------------------------------
# 3. Normalize to common signatures
# ---------------------------------------------------------------------------

def normalize_python(fmt_str: str) -> str:
    """Normalize a Python f-string template to a signature.

    Replaces {anything} (including {x!r}, {x!s}) with *.
    Then normalizes quoted placeholders: '*' and "*" become *.

    Truncated concatenation (f"..." + expr) is marked at extraction time with
    a trailing `{...}` field, which this turns into the same * as any other
    interpolation -- see _truncation_marker.
    """
    sig = re.sub(r"\{[^}]*\}", "*", fmt_str)
    # Normalize quoted * placeholders
    sig = re.sub(r"""['"](\*)['""]""", r"\1", sig)
    sig = re.sub(r"""['"](\*)['"']""", r"\1", sig)
    return sig


def normalize_go(fmt_str: str) -> str:
    """Normalize a Go fmt.Sprintf format string to a signature.

    First unescapes Go string escapes (\\\" -> \", \\n -> newline, etc.).
    Then replaces %s, %d, %v, %q, %T with *.
    %q produces a Go-quoted string (with surrounding double quotes), so we
    treat it like * rather than "*".
    Then normalizes quoted placeholders: '*' becomes *.
    """
    # Unescape Go string literal escape sequences
    sig = fmt_str.replace('\\"', '"')
    sig = sig.replace('\\n', '\n')
    sig = sig.replace('\\t', '\t')
    sig = sig.replace('\\\\', '\\')
    sig = re.sub(r"%[sdvqT]", "*", sig)
    # Normalize surrounding quotes on placeholders: '*' -> *
    sig = re.sub(r"'(\*)'", r"\1", sig)
    return sig


def normalize_typescript(fmt_str: str) -> str:
    """Normalize a TypeScript template literal to a signature.

    First unescapes JS string escapes (\\\\ -> \\, \\` -> `, \\n -> newline,
    etc.).  Then replaces ${...} interpolations with *.  Interpolations that
    call q() (Go strconv.Quote semantics, the %q analog) are treated like *
    rather than "*", matching normalize_go's handling of %q.
    Then normalizes quoted placeholders: '*' and "*" become * (both quote
    styles, matching normalize_python -- TS registration templates quote
    names with literal double quotes where Python uses !r).
    """
    # Unescape JS string literal escape sequences
    out: list[str] = []
    i = 0
    while i < len(fmt_str):
        if fmt_str[i] == "\\" and i + 1 < len(fmt_str):
            nxt = fmt_str[i + 1]
            out.append({"n": "\n", "t": "\t"}.get(nxt, nxt))
            i += 2
        else:
            out.append(fmt_str[i])
            i += 1
    sig = "".join(out)
    sig = re.sub(r"\$\{[^}]*\}", "*", sig)
    # Normalize surrounding quotes on placeholders: '*' and "*" -> *
    sig = re.sub(r"""['"](\*)['"]""", r"\1", sig)
    return sig


def deduplicate_signatures(
    items: list[tuple[str, str, str]],
) -> dict[str, list[tuple[str, str]]]:
    """Deduplicate by signature, keeping track of origins.

    Input: list of (category, raw_pattern, signature)
    Returns: {signature: [(category, raw_pattern), ...]}
    """
    result: dict[str, list[tuple[str, str]]] = {}
    for cat, raw, sig in items:
        if sig not in result:
            result[sig] = []
        entry = (cat, raw)
        if entry not in result[sig]:
            result[sig].append(entry)
    return result


# ---------------------------------------------------------------------------
# 4. N-way parity comparison
# ---------------------------------------------------------------------------

def _get_status(sig: str, impl: str) -> str | None:
    """Return the declared status for a signature in an implementation.

    Returns None if no status is declared (meaning the signature is expected
    to be present in this impl by default).
    """
    entry = SIGNATURE_STATUS.get(sig)
    if entry is None:
        return None
    return entry.get(impl)


def _is_excluded(status: str | None) -> bool:
    """Return True if the status indicates a parity exclusion."""
    return status is not None and status.startswith("excluded:")


def _is_dead_code(status: str | None) -> bool:
    """Return True if the status indicates dead code."""
    return status is not None and status.startswith("dead_code:")


def _is_coverage_deferred(status: str | None) -> bool:
    """Return True if the status indicates deferred coverage."""
    return status is not None and status.startswith("coverage_deferred:")


def _is_coverage_excluded(sig: str) -> bool:
    """Return True if a signature is excluded from coverage checks.

    A signature is coverage-excluded if:
    - It is excluded from at least one implementation (parity exclusion), or
    - It is dead code in all implementations, or
    - It has deferred coverage in all implementations.
    """
    entry = SIGNATURE_STATUS.get(sig)
    if entry is None:
        return False

    # Any parity exclusion removes it from coverage
    for impl in IMPLEMENTATIONS:
        status = entry.get(impl)
        if _is_excluded(status):
            return True

    # Dead code in all implementations
    if all(_is_dead_code(entry.get(impl)) for impl in IMPLEMENTATIONS):
        return True

    # Coverage deferred in all implementations
    if all(_is_coverage_deferred(entry.get(impl)) for impl in IMPLEMENTATIONS):
        return True

    return False


def check_parity(
    impl_sigs: dict[str, dict[str, list[tuple[str, str]]]],
) -> list[str]:
    """N-way parity check across all implementations.

    For each signature in the union of all implementations' extracted sets,
    verifies that every implementation either has the signature or has an
    exclusion rationale declared in SIGNATURE_STATUS.
    """
    errors: list[str] = []
    all_sigs: set[str] = set()
    for sigs in impl_sigs.values():
        all_sigs.update(sigs.keys())

    for sig in sorted(all_sigs):
        for impl in IMPLEMENTATIONS:
            found = sig in impl_sigs[impl]
            status = _get_status(sig, impl)

            if found and _is_excluded(status):
                # Found in the impl but declared as excluded -- stale exclusion.
                # This is a warning, not an error (the exclusion is overly
                # conservative). Skip for now; could be tightened later.
                pass
            elif not found and status is None:
                # Not found and no exclusion declared -- parity error.
                # Which impls DO have it?
                sources = [
                    name for name, sigs in impl_sigs.items() if sig in sigs
                ]
                origins = []
                for name in sources:
                    raw_examples = ", ".join(
                        repr(raw) for _, raw in impl_sigs[name][sig][:2]
                    )
                    origins.append(f"{name}: {raw_examples}")
                errors.append(
                    f"{impl} missing error (no exclusion): {sig!r} "
                    f"(in: {'; '.join(origins)})"
                )

    return errors


# ---------------------------------------------------------------------------
# 5. Check test coverage
# ---------------------------------------------------------------------------

def extract_test_stderr(cases_dir: Path) -> list[str]:
    """Extract all stderr assertion strings from conformance test cases."""
    assertions: list[str] = []
    for json_file in sorted(cases_dir.glob("*.json")):
        cases = json.loads(json_file.read_text())
        for case in cases:
            expect = case.get("expect", {})
            if "stderr_equals" in expect:
                assertions.append(expect["stderr_equals"])
            if "stderr_contains" in expect:
                val = expect["stderr_contains"]
                if isinstance(val, str):
                    assertions.append(val)
                elif isinstance(val, list):
                    assertions.extend(val)
    return assertions


def signature_matches_assertion(sig: str, assertion: str) -> bool:
    """Check if a signature could match a concrete stderr assertion.

    Converts the signature to a regex where * matches any non-empty substring,
    then checks if the assertion contains a match.
    """
    parts = sig.split("*")
    escaped = [re.escape(p) for p in parts]
    pattern = ".+?".join(escaped)
    try:
        return bool(re.search(pattern, assertion))
    except re.error:
        return False


# ---------------------------------------------------------------------------
# 6. N-way shape diagnostic
# ---------------------------------------------------------------------------

def diagnose_new_target(
    target_name: str,
    impl_sigs: dict[str, dict[str, list[tuple[str, str]]]],
) -> list[str]:
    """Report every signature that would need an explicit answer for a new
    implementation target.

    A new target inherits no status entries, so every signature in the union
    requires either extraction (the new impl produces it) or an exclusion
    entry in SIGNATURE_STATUS.
    """
    all_sigs: set[str] = set()
    for sigs in impl_sigs.values():
        all_sigs.update(sigs.keys())

    needs_answer: list[str] = []
    for sig in sorted(all_sigs):
        status = _get_status(sig, target_name)
        if status is None:
            needs_answer.append(sig)

    return needs_answer


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> int:
    py_source = PY_SOURCE.read_text()
    go_errors_source = GO_ERRORS.read_text()
    ts_errors_source = TS_ERRORS.read_text()

    # Extract raw error patterns
    py_raw = extract_python_errors(py_source)
    go_raw = extract_go_errors(go_errors_source)
    ts_raw = extract_typescript_errors(ts_errors_source)

    # Normalize to signatures
    py_items = [(cat, raw, normalize_python(raw)) for cat, raw in py_raw]
    go_items = [(cat, raw, normalize_go(raw)) for cat, raw in go_raw]
    ts_items = [(cat, raw, normalize_typescript(raw)) for cat, raw in ts_raw]

    py_sigs = deduplicate_signatures(py_items)
    go_sigs = deduplicate_signatures(go_items)
    ts_sigs = deduplicate_signatures(ts_items)

    impl_sigs: dict[str, dict[str, list[tuple[str, str]]]] = {
        "python": py_sigs,
        "go": go_sigs,
        "typescript": ts_sigs,
    }

    all_errors: list[str] = []

    # --- Check 1: N-way parity ---
    all_errors.extend(check_parity(impl_sigs))

    # --- Check 2: Test coverage ---
    # Only parse-time errors (category='parse') can be tested through the
    # conformance framework which exercises CLI behavior (argv -> stderr).
    # Registration-time errors (panics in Go, ValueError in Python during
    # app setup) are tested by each implementation's own unit tests.
    test_assertions = extract_test_stderr(CASES_DIR)

    # Build set of parse-time signatures only
    parse_sigs: set[str] = set()
    for impl_name, sigs in impl_sigs.items():
        for sig, origins in sigs.items():
            if any(cat == "parse" for cat, _ in origins):
                parse_sigs.add(sig)

    # Check coverage (excluding coverage-excluded signatures)
    uncovered: list[str] = []
    for sig in sorted(parse_sigs):
        if _is_coverage_excluded(sig):
            continue
        covered = any(
            signature_matches_assertion(sig, assertion)
            for assertion in test_assertions
        )
        if not covered:
            uncovered.append(sig)

    for sig in uncovered:
        sources = [
            name for name, sigs in impl_sigs.items() if sig in sigs
        ]
        all_errors.append(
            f"Uncovered error signature: {sig!r} "
            f"(in: {', '.join(sources)})"
        )

    if all_errors:
        print(f"Error parity check FAILED ({len(all_errors)} issue(s)):\n")
        for err in all_errors:
            print(f"  - {err}")
        return 1

    # Summary on success
    all_sigs = set()
    for sigs in impl_sigs.values():
        all_sigs.update(sigs.keys())
    matched = set.intersection(*(set(s.keys()) for s in impl_sigs.values()))

    # Count exclusions per implementation
    excl_counts: dict[str, int] = {impl: 0 for impl in IMPLEMENTATIONS}
    dead_count = 0
    deferred_count = 0
    for sig, entry in SIGNATURE_STATUS.items():
        if sig not in all_sigs:
            # Stale entry (signature no longer extracted) -- skip counting
            continue
        is_dead = all(_is_dead_code(entry.get(impl)) for impl in IMPLEMENTATIONS)
        is_deferred = all(
            _is_coverage_deferred(entry.get(impl)) for impl in IMPLEMENTATIONS
        )
        if is_dead:
            dead_count += 1
        elif is_deferred:
            deferred_count += 1
        else:
            for impl in IMPLEMENTATIONS:
                if _is_excluded(entry.get(impl)):
                    excl_counts[impl] += 1

    coverable = parse_sigs - {s for s in parse_sigs if _is_coverage_excluded(s)}
    covered_count = len(coverable - set(uncovered))

    print("Error parity check passed.")
    print(f"  Matched signatures: {len(matched)}")
    for impl in IMPLEMENTATIONS:
        print(f"  {impl}-excluded: {excl_counts[impl]}")
    print(f"  Dead code (excluded): {dead_count}")
    print(f"  Coverage deferred: {deferred_count}")
    print(f"  Parse-time coverage: {covered_count}/{len(coverable)} signatures covered")
    print(f"  Total signatures: {len(all_sigs)}")

    # N-way shape diagnostic: show what a hypothetical third target would need
    fake_target = "_test_target"
    needs_answer = diagnose_new_target(fake_target, impl_sigs)
    print(f"  N-way shape check: adding target {fake_target!r} would require "
          f"{len(needs_answer)} explicit answers")

    return 0


if __name__ == "__main__":
    sys.exit(main())
