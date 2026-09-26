+++
title = "strictcli"
description = "strictcli is a strict CLI framework for Python, Go and TypeScript: declare everything, infer nothing, get identical behavior from three idiomatic surfaces."
nav_order = 0
+++

# strictcli

A strict CLI framework with three first-class implementations -- Python, Go, and TypeScript -- kept in behavioral lockstep by a shared conformance suite.

Declare everything, infer nothing. Help text is mandatory on every app, group, command, flag, and argument. Types are limited to `str`, `bool`, `int`, and `float`, parsed strictly. Every command declares its effect on the world, and the framework derives consent and previewing from that declaration.

Build in whichever of the three languages you prefer, writing it the way that language is written: Python keyword arguments and decorators, Go functional options, TypeScript discriminated unions and full type inference. Lockstep binds behavior -- semantics, help bytes, schema fields, and the sentence of every error -- not the spelling you type. See [Language idioms](language-idioms.md).

## Start here

- [Python quickstart](python-quickstart.md)
- [Go quickstart](go-quickstart.md)
- [TypeScript quickstart](typescript-quickstart.md)

## Guides

- [Language idioms](language-idioms.md) -- why the three declaration surfaces are deliberately different, and what parity actually binds
- [Architecture and internals](architecture.md) -- the parse pipeline, registration-time validation, and the schema format
- [Flag system](flag-system.md) -- flags, arguments, choice flags and their declaration scopes, constraints, and the reserved quartet
- [Conformance](conformance.md) -- how the three implementations are proven identical
- [Consequential confirmation over MCP](mcp-confirmation.md) -- how a tool asks a human before it runs
- [Process trace store](process-trace-store.md) -- how process ancestry is recorded and shared across tools

## Reference

- [API reference](gen-index.md) -- every module in all three implementations
