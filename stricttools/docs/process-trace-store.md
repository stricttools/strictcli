+++
title = "Process Trace Store"
description = "How strictcli records process ancestry: the STRICTCLI_TRACE_PARENT variable, an append-only JSONL store, ULIDs, hour partitions and the lookup rule."
nav_group = "Guides"
nav_order = 20
+++

# Process Trace Store

> **Status: implemented in all three implementations, shipping with the framework's
> machine-interface release.** This page is normative and complete. It was written before any code
> existed -- the convention is the effects contract's own (§19.8 of `.stricttools/docs/history/_effects-contract.md`
> designs compositional child previews the same way), and writing the specification first is what
> let three implementations arrive at the same behaviour instead of converging afterwards. The
> Python, Go and TypeScript implementations now write to the store exactly as described. No
> *released* version does yet: the store ships alongside machine mode and the envelope.
>
> One consequence while that remains true. A tool that implements this page writes into a store few
> others write to yet: that is harmless and expected, because participation is open and a dangling
> or absent parent identifier is legal by design. A consumer that reads an empty or missing store is
> in the same case as one reading a store that was pruned -- handled by the
> [Consumers](#consumers) rules, not by a special case.
>
> The contract items governing the store -- observational-only, and the best-effort failure
> carve-out -- are §20 of the effects contract. This page owns everything else: the variable, the
> line, the partitions, the identifiers and the failure marker. Spellings marked *(authored at the
> implementation round)* below were pinned when the implementations were written, at points where
> this page had been silent; they are recorded in the contract's §18.9, item 112.
>
> **Amended 2026-08-13 at the lookup-rule audit.** An independent audit found this page's lookup
> rule falsified by its own rolling rule: the range invariant is one-sided, and a reader must walk
> backward through older partitions when the binary search misses. The amendment is marked in place
> under [Partitions](#partitions), and the entry example's identifier -- which decoded to a
> different instant than the `spawned_at` beside it -- was regenerated. The amendment is recorded in
> the contract's §18.9, item 112.

When one command-line tool runs another, the second one has no reliable way to say who invoked it.
Every tool that has wanted the answer has invented its own channel -- an environment marker, a
`--called-by` flag, a guess from the parent process -- and each of those answers is local to one
pair of tools.

strictcli records process ancestry **universally** instead: at the seam where it spawns a child, the
framework writes one line describing the invocation doing the spawning and hands that line's
identifier to the child. Anything that wants the chain reads it from a shared, append-only store.

This page is the specification. It is written so that a tool which is not strictcli -- in any
language -- can participate correctly by following it. The `STRICTCLI_` prefix names the store's
home and its owner; it does not restrict who may write to it.

**What this store is not:** it is not an input to behaviour. No strictcli code path branches on the
ancestry stack, and the framework exposes no accessor for it. That is a ratified contract item,
enforced by conformance sweeps, and it is what makes a forged identifier a harmless false claim
rather than an exploit.

## Propagation

Ancestry travels through exactly one environment variable, and that variable carries exactly one
value: the identifier of the entry describing the process that spawned this one. Everything else --
the chain, the ancestors' identities, the depth -- is derived by reading the store, so the variable
never grows and never needs to be trimmed, split or elided.

```text
STRICTCLI_TRACE_PARENT=01JZ8X4M6N7QK2WVBD3F5RTYAC
```

- **It carries exactly one thing: the parent entry's identifier.** Not a chain, not a serialized
  stack, not a JSON object. The value is constant-size (26 characters), so nothing grows as a chain
  deepens and no elision machinery is ever needed.
- **It is composed into the child's environment**, at the spawn seam, as part of building that
  child's environment. Nothing is mutated in place: the spawning process's own environment is never
  modified, and the variable is never a channel back up.
- **The entry is written immediately before the child-start attempt**, because the identifier must
  exist to be composed into the environment the child is started with. An entry therefore exists
  even when the start itself fails -- there is no retraction, and a record of an invocation that
  tried to start a child is still a true record of that invocation. *(authored at the
  implementation round)*
- **When the entry could not be written, the variable is removed from the child's environment**
  rather than left at whatever this process inherited. A lost record must not silently re-attribute
  the child to its grandparent: an absent link is honest, a wrong one is not. *(authored at the
  implementation round)*
- **An inherited value that is not a valid identifier under the profile records `parent_id: null`.**
  It is never copied into the entry verbatim, because every identifier a store holds must be
  parseable by the strict profile -- and the pollution is still visible where it actually is, in the
  consumer's own environment, which the [Consumers](#consumers) rules cover. It never affects the
  run in any other way. *(authored at the implementation round)*
- **A foreign or dangling identifier is legal by design.** A parent id that resolves to no entry --
  because the store was pruned, because the writer was another tool, because someone set the
  variable by hand -- is not an error. Consumers record the dangling reference as an anomaly (see
  [Consumers](#consumers)) and carry on.
- **It rides `ssh` by command-prefixing**, since `ssh` does not forward arbitrary environment
  variables:

```bash
ssh host STRICTCLI_TRACE_PARENT="$STRICTCLI_TRACE_PARENT" mytool subcommand
```

  The chain then spans machines, with the remote entries in the remote host's own store.

## The store

```text
~/.local/share/strictcli/trace/
```

- **The path is literal.** The store is exactly `~/.local/share/strictcli/trace/`: expand `~` to
  the invoking user's home directory, and consult nothing else. It is deliberately **not** derived
  from `XDG_DATA_HOME`, and not from any other environment variable, despite matching the XDG
  default -- a writer that honoured `XDG_DATA_HOME` and one that did not would write to two
  different stores on the same machine, and a chain crossing them would dangle at both ends while
  both writers were behaving correctly. One literal path is the entire location rule, and it is the
  reason two implementations can be written independently and still link. The same literalness
  covers the failure marker, which lives inside this directory.
- **Append-only JSONL.** Each entry is one JSON object on one line.
- **One entry is one `O_APPEND` `write()` of one line**, including its terminating newline. Not a
  buffered stream flushed later, not two writes. A single `write()` to a file opened `O_APPEND` is
  atomic against other concurrent writers on a local filesystem, and that atomicity is the store's only
  concurrency mechanism: there is no lock, no coordinator, no daemon.
- **Local filesystems only.** NFS breaks `O_APPEND` atomicity, so a store on NFS can interleave two
  writers inside one line. Do not place the store on a network filesystem; a reader that finds a
  torn line records it as an anomaly and skips it.
- **Encoding:** UTF-8, compact JSON (no embedded newlines anywhere in the line), terminated by
  exactly one `\n`. Escape only what JSON mandates -- quotes, backslashes, control characters -- and
  emit everything else literally. Key order within the object is irrelevant; readers compare
  structurally.
- **Participation is open.** Any tool that writes conforming lines to this store, and propagates the
  variable, is a participant. There is no registration and no issuing authority.
- **Directories are created on write.** A missing store directory is created (mode `0700`) by
  whoever writes next. Deleting the store is a supported thing to do: it means tracing resumes from
  empty, not that tracing dies.
- **Files are created with mode `0600`** -- partitions and the failure marker alike. The store
  records who ran what on this machine, so a partition inherits the directory's own privacy instead
  of whatever the writing process's file-creation mask would have granted. *(authored at the
  implementation round)*

## Partitions

Entries live in files labelled with a UTC hour, but a label is a **range start**, not a promise
about contents: a file holds nothing older than its own label, and may hold entries newer than the
next file's label. A busy machine produces one file per hour; an idle one produces a single file
spanning days. Both are correct, and both are searched the same way (the lookup rule below).

```text
~/.local/share/strictcli/trace/2026-08-13T04.jsonl
~/.local/share/strictcli/trace/2026-08-13T09.jsonl
~/.local/share/strictcli/trace/2026-08-14T11.jsonl
```

- **The filename is the range's start**, formatted `YYYY-MM-DDTHH` in UTC, plus `.jsonl`.
- **A file's range is half-open**: it starts at its own label and ends where the next file's label
  begins. The greatest-named file's range is open-ended. A label is therefore *not* a promise that
  the file holds only that hour -- an idle machine may write one file covering three days.
  *(amended 2026-08-13, lookup-rule audit)* The range is where a reader looks **first**; it is not a
  bound on contents at the top end, because a file that has not yet rolled keeps taking entries after
  a newer-labelled partition exists.
- **Writers append to the greatest-named file.** That file is the active partition.
- **Rolling.** Before appending, a writer creates the next partition when **both** conditions hold:
  1. the active file's size is at least **8 MB**, and
  2. the current UTC hour is **later** than the active file's label.

  The new file is created with `O_EXCL` and named for the current UTC hour. If creation fails
  because another writer won the race, the loser simply appends to the winner's file -- no retry
  loop, no coordination. Worst-case file size is therefore the 8 MB threshold plus one hour of
  writes.
- **The clamp invariant, which bounds the bottom only.** A writer whose clock reads earlier than
  the active partition's range start **clamps** its minted timestamp to that range start. Every
  entry's embedded timestamp is therefore **at or after its own file's label**, without exception.
  ~~Every entry's embedded timestamp therefore lies within its file's range, without exception --
  which is what makes lookup a deterministic **binary search over filenames**: to find entries
  around an instant, binary-search the sorted filenames for the greatest label not after it, and
  read that file.~~ *(amended 2026-08-13, lookup-rule audit -- see the box below: the top of the
  range is NOT bounded, and one binary search is not enough.)* A clock that jumps backwards (NTP
  correction, a VM resuming from a snapshot) costs a small ordering distortion inside one file and
  never breaks the lookup.
- **Lookup: binary search, then walk backward on a miss.** To find the entry an identifier names,
  binary-search the sorted filenames for the greatest label **not after** that identifier's embedded
  timestamp and read that file; **if the entry is not there, continue with the next older partition,
  and so on until it is found or the partitions are exhausted.** The backward walk is required for
  correctness, not an optimization: the rolling rule above strands entries above their own file's
  label, so the partition an identifier's timestamp points at is where the entry is *most likely*
  to be, never where it must be. A search that stops after one file reports entries that exist as
  missing.

> **Amendment (2026-08-13, lookup-rule audit): the range invariant is one-sided, and lookup walks
> backward.** This page claimed that every entry's timestamp lay inside its file's half-open range
> and that one binary search therefore always found it. The rolling rule falsifies the top half of
> that claim, and the writers were right while the lookup rule was wrong. A file rolls only when it
> is at least 8 MB **and** the hour has advanced, so a small file keeps taking entries hour after
> hour; when it finally crosses 8 MB in some later hour, the next write creates a partition labelled
> for *that* hour. The earlier file is then left holding entries whose timestamps are at or beyond
> the newer file's label. The audit constructed exactly that store: an hour-09 entry living in the
> `...T04` partition beside a `...T09` partition, which the single-search rule reports as missing --
> and a consumer then records a dangling parent that is not dangling.
>
> Nothing about writing changes: the clamp still bounds the bottom, and it was always the only bound
> the writers established. What changes is the reader's rule, which now walks backward through older
> partitions on a miss, and the invariant's statement, which is one-sided. *(authored at the
> lookup-rule audit; recorded in the contract's §18.9, item 112.)*
- **All date arithmetic is UTC epoch arithmetic.** No local time, no timezone database, no DST, no
  calendar code. Formatting a label is integer division of an epoch-millisecond value by 3 600 000
  and rendering the result; comparing labels is string comparison, which the format makes equivalent
  to time order.
- **Readers ignore any file in the directory that does not match the partition name pattern.** The
  failure marker below is such a file.

## Identifiers

Entries are identified by ULIDs under a strict pinned profile. The profile exists because a
lenient parser is how one identifier becomes two strings that fail to link: every rule below is
either a rejection the base specification leaves optional, or a layout fact a partition lookup
depends on. Writers mint independently, so the profile is the only thing keeping them consistent.

- **26 characters**, Crockford base32, alphabet exactly
  `0123456789ABCDEFGHJKMNPQRSTVWXYZ` (no `I`, `L`, `O`, `U`).
- **Canonical uppercase only.** A lowercase identifier is rejected on parse -- not
  case-normalized. Crockford's own specification permits case-insensitive decoding; this profile
  deliberately does not, because two spellings of one identifier means two strings that compare
  unequal and one chain that fails to link.
- **Overflow is rejected on parse.** 26 base32 characters encode 130 bits while a ULID is 128, so
  the first character must not exceed `7`. Anything larger is invalid, not truncated.
- **Layout**: the first 48 bits are the timestamp in milliseconds since the Unix epoch (UTC), the
  remaining 80 bits are random.
- **Every writer mints its own**, from its own clock plus 80 random bits from a cryptographically
  secure source. There is **no issuing authority**, no sequence file and no coordination. Collision
  probability for a thousand identifiers minted in the same millisecond is about 4e-19.
- **The timestamp is the clamped one** (see the clamp invariant above): a writer clamps first, then
  mints, so the identifier and the entry's `spawned_at` always agree and are never older than the
  label of the file the entry is written to. *(amended 2026-08-13, lookup-rule audit: they may be
  newer than the next file's label -- see the amendment box under Partitions.)*
- **Shared cross-language test vectors** pin encoding and parsing -- valid canonical forms, lowercase
  rejection, overflow rejection, alphabet violations, and the timestamp round-trip.

> **Informative aside, not a requirement.** A ULID is 128 bits, and so is a W3C `traceparent`
> trace-id. A ULID re-encoded as 32 lowercase hex characters is therefore a spec-legal trace-id,
> which leaves a bridge to distributed-tracing systems reachable later at zero cost today. (AWS
> X-Ray's trace ids embed a timestamp the same way, so the shape has precedent.) Nothing in this
> store emits or consumes `traceparent`.

## The entry

One JSON object per line, with every key always present -- an absent key is a malformed line, not a
defaulted one. The object describes an invocation: which app, which command, under which
reserved-flag state, in which process, at which instant, descending from which parent. It never
describes what the invocation was asked to do beyond the command's own path.

```json
{"id":"01KZWNEG8HJPCTVGNVF2MQ0Y5E","parent_id":null,"app":"rlsbl","version":"0.61.2","command":"release.run","dry_run":false,"machine_mode":false,"quiet":false,"verbose":true,"approve_consequential":true,"effect":"mutating","pid":48213,"spawned_at":"2026-08-13T04:17:52.913Z"}
```

The identifier's first 48 bits decode to `2026-08-13T04:17:52.913Z`, which is exactly what
`spawned_at` renders -- the two always agree, and an example where they do not is a broken example.
*(The line printed here until 2026-08-13 carried an identifier decoding to 2025-07-03T19:45:10.869Z
beside that `spawned_at`; corrected at the lookup-rule audit.)*

| Key | Type | Meaning |
|-----|------|---------|
| `id` | string | This entry's ULID. The value a child receives as `STRICTCLI_TRACE_PARENT`. |
| `parent_id` | string \| null | The `STRICTCLI_TRACE_PARENT` this process inherited; `null` when it inherited none (a root). May dangle. |
| `app` | string | The spawning app's declared name. |
| `version` | string | The spawning app's declared version. |
| `command` | string \| null | The dotted command path being executed (`release.run`); `null` when no command resolved. |
| `dry_run` | boolean | Whether the spawning invocation was in dry-run mode. |
| `machine_mode` | boolean | Whether the spawning invocation was in machine-output mode. |
| `quiet` | boolean | The spawning invocation's `--quiet` state. |
| `verbose` | boolean | The spawning invocation's `--verbose` state. |
| `approve_consequential` | boolean | Whether consent was given in advance. This is a consent audit trail: it records that a human or a caller accepted a consequential operation, at the moment they did. |
| `effect` | string | The command's effect classification: `"read_only"` or `"mutating"`. |
| `pid` | integer | The **spawning** process's own pid. A witness a consumer can cross-check; not the child's. |
| `spawned_at` | string | The instant this entry was minted -- the moment of the spawn. RFC 3339 in UTC with exactly three fractional digits and a `Z` suffix (`2026-08-13T04:17:52.913Z`). It is exactly the clamped millisecond embedded in `id`, rendered. |

**Never argv.** Arguments carry secrets -- tokens, passwords, private paths -- and a store that is
written unconditionally, forever, in the background, must not be where they end up. The command path
is recorded; what was passed to it is not.

**There is no chain identifier.** No `trace_id`, no `root_id`, no depth counter. The chain is derived
by walking `parent_id` to the root, and a derived value that is also stored is a value that can
disagree with itself.

**An entry describes the invocation doing the spawning.** A process that spawns three children writes
three entries -- same identity, three identifiers -- so each child links to a distinct entry. A
process that never spawns writes nothing; it is visible through the variable it inherited, which its
own consumers read.

## Failure policy

**Tracing is best-effort by declared design.** This is deliberate and it is scoped to this store
alone -- everything else in strictcli fails closed. The store's failure mode is losing a record of
what happened, never doing the wrong thing, and nothing may depend on it, so a store that cannot be
written costs observability and nothing else.

- **A write failure never fails the run**, never prints a diagnostic, and never changes an exit code.
- **No retries**, ever.
- **A write-once marker.** On the first write failure, the writer creates
  `~/.local/share/strictcli/trace/write-failure.marker` with `O_EXCL`, containing the first-failure
  timestamp in `spawned_at`'s exact format followed by one `\n`. If the file already exists, nothing
  happens -- that is the whole point of write-once. There is **no counter**: counters require
  read-modify-write, which races, and the number would not change anyone's next action. A disk-full
  condition blinds the marker too; that is accepted. The marker's timestamp is the writer's clock at
  the moment of failure and is **not clamped** -- no partition was selected, so there is no range to
  clamp it into. *(authored at the implementation round)*
- **Directories are auto-created**, as described above.
- **The primary detection channel is not the marker.** It is consumers noticing dangling parent
  identifiers when they capture -- a real signal from a real reader, rather than a file nobody opens.
  The marker is the corroborating detail once someone goes looking.

## Consumers

A consumer is any tool that wants to record who invoked it -- an archival tool stamping a deletion,
an audit log, a continuous-integration reporter. Consumers are the store's readers, and the rules
below exist so that a record written today still means something after the store has been pruned,
after the writing tool has been upgraded, and after a line somewhere turned out to be malformed.

**Resolve at capture time.** When the consumer records its own event, it should:

1. read `STRICTCLI_TRACE_PARENT` from its own environment;
2. resolve that entry from the store by the [lookup rule](#partitions) -- binary search to the
   greatest label not after the identifier's timestamp, then backward through older partitions on a
   miss -- and repeat for each `parent_id` up to the root;
3. **embed the flattened chain in its own record**, alongside the identifiers themselves.

A consumer that stops at the first partition the binary search lands on will report entries that
exist as dangling, because the rolling rule strands entries above their file's label.

Embedding the resolved chain rather than only the identifiers makes the consumer's record
self-contained forever. **Age-based pruning of the store -- compressing old partitions, deleting
older ones -- can therefore never orphan a record.** Keeping the identifiers alongside it preserves
correlation with any store data that still exists.

**Malformed data is recorded verbatim as an anomaly.** A polluted environment variable, a torn final
line, an entry missing a key, a parent that resolves to nothing: the consumer records what it saw,
marks it anomalous, and continues. It must neither brick the tool nor discard the observation
silently -- an anomaly that vanishes is indistinguishable from a chain that was fine.

**Do not expect an API.** strictcli exposes no accessor for the ancestry stack, deliberately.
Consumers parse the environment variable and read the store themselves, which is exactly what keeps
the store observational: nothing in the framework can branch on data no framework code reads.
