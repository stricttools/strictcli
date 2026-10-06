# Schema dump: omit fields that match defaults

## Problem

`--dump-schema` output is bloated because every field is emitted even when its value matches the documented default. For a private consumer's CLI (455 commands, 569 flags, 382 args), this produces a 13,728-line / 418KB schema.json.

## Analysis

**Flag fields that are almost always default:**

| Field | Default | Flags at default | % at default |
|-------|---------|------------------|--------------|
| `env` | null | 569/569 | 100% |
| `hidden` | false | 569/569 | 100% |
| `choices` | null | 539/569 | 95% |
| `repeatable` | false | 537/569 | 95% |
| `short` | null | 443/569 | 78% |
| `negatable` | null | 411/569 | 73% |

**Arg fields:**

| Field | Default | Args at default | % at default |
|-------|---------|-----------------|--------------|
| `variadic` | false | 374/382 | 98% |
| `required` | true | 382/382 | 100% |

**Command fields:**

| Field | Default | Commands at default | % at default |
|-------|---------|---------------------|--------------|
| `passthrough` | false | 454/455 | 99% |
| `flags` | [] | 195/455 | 42% |
| `args` | [] | 125/455 | 27% |

## Impact

- Pretty-printed: 13,728 lines to 8,448 lines (38% reduction)
- Bytes: 418KB to 241KB (42% reduction)
- Compact: 206KB to 115KB (44% reduction)

## Proposed solution

`--dump-schema` should not emit fields whose value equals the documented default (null, false, empty array). Schema consumers already need to know the defaults to handle missing fields. This is the same convention used by OpenAPI, JSON Schema, and most schema formats.

### Example: `htz server create` flag `--type`

Before (11 lines):
```json
{
  "name": "--type",
  "type": "str",
  "help": "Server type",
  "short": null,
  "default": null,
  "env": null,
  "choices": null,
  "repeatable": false,
  "negatable": null,
  "hidden": false
}
```

After (3 lines):
```json
{
  "name": "--type",
  "type": "str",
  "help": "Server type"
}
```

## Out of scope

Help string deduplication ("Domain name" appears 121x, "Skip confirmation prompt" 106x). Marginal gain, adds complexity. The default-omission rule handles the bulk of the bloat.

## Effort

Small. The schema dump logic already knows the defaults for each field. Filter them out before serialization.
