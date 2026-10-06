# Recursive group nesting (arbitrary depth)

## Context

strictcli currently supports 2-level nesting: app -> group -> command. Both Python and Go hard-code a 2-token dispatch in their parsers. A private consumer needs 3 levels (app -> service -> group -> command) and cannot migrate without this.

## Problem

- That consumer has 12 services, each containing command groups, totaling 300+ commands at 3 levels from the app root
- 6 Namecheap commands go to 4 levels (can be flattened to 3)
- The per-service sub-App workaround is architecturally unsound
- strictcli is the only blocker preventing that consumer's migration; all other features (type=int, choices, mutex, repeatable, passthrough, global flags) are already supported

## Proposed solution

Replace hard-coded 2-token dispatch with iterative group traversal. Groups can contain subgroups via a new `Group.group()` method. The parser consumes tokens in a loop while they match group names, then dispatches to the command.

### API

Python:
```python
nch = app.group("nch", help="Namecheap operations")
dns = nch.group("dns", help="DNS management")

@dns.command("servers", help="list DNS servers")
def dns_servers(): ...
```

Go:
```go
nch := app.Group("nch", "Namecheap operations")
dns := nch.Group("dns", "DNS management")
dns.Command("servers", "list DNS servers", handler)
```

### Implementation

Python (~250-350 LOC):
- Add `_groups` dict to Group class
- Add `Group.group()` method
- Replace 2-token dispatch in `_parse()` with iterative loop
- Track full path for command prefix calculation
- Update `_format_group_help()` to show subgroups

Go (~300-400 LOC):
- Add `Groups` map to Group struct
- Add `Group.Group()` method
- Replace 2-token dispatch in `doParse()` with iterative loop
- Track full path for prefix/help
- Update `formatGroupHelp()` to show subgroups

Conformance (~50-100 test cases):
- Deep nesting (3, 4+ levels)
- Help at every nesting level
- Deprecated subgroups
- Error messages with full paths

### Cascading changes

- Help formatting: detect whether a group contains subgroups, commands, or both
- Error messages: show full path (e.g., "unknown command 'xyz' in 'nch dns'")
- Deprecation: `Group.deprecate()` must work at any level
- Command prefix: track breadcrumb path during parsing

## Consumer

A private consumer (primary). No other current consumer needs this, but it future-proofs all 10+ consumers.

## Effort

7-10 days total (Python + Go + conformance).
