# kwargs handler skip has no conformance test cases

## Problem

The `**kwargs` handler skip feature (added in 0.7.1) allows Python command handlers to accept `**kwargs` without strictcli rejecting them for having unexpected parameters. A private consumer uses it in a `make_handler` wrapper pattern.

The feature has Python-only e2e tests but no conformance test cases in `conformance/cases/`. This is reasonable since `**kwargs` is a Python-specific language feature with no Go equivalent — Go doesn't validate handler signatures at all.

## What's needed

Decide whether this needs conformance coverage or should be documented as a Python-specific extension that is out of conformance scope. If the latter, the conformance README or documentation should note which features are language-specific and excluded from cross-language testing.
