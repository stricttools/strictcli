# A declared network effect, listed by `--dry-run` like other effects

## Context

The effects regime records every subprocess, file write, and HTTP call so that `--dry-run` performs none of them and
prints a "Would do:" list. A program that opens its own network connections (for example an SMTP submission or an
IMAP session made through a consumer's own connection layer) has no effect strictcli knows about: under `--dry-run`
its "Would do:" footer stays empty, and the command must print its own preview instead. A consumer building mail
sending over SMTP had to do exactly that: its dry run shows sender, recipients, subjects, and sizes in the command's
own output while strictcli's footer is empty.

## Required change

- A declared network effect in the effects handle: opening a connection to a host and port (and, where the protocol
  allows, what will be sent), recorded in dry mode and listed in the "Would do:" output and the `--json` document
  like the existing effects, and refused in dry mode.
- Decide its granularity: one entry per connection, or per protocol-level action (each message an SMTP session would
  deliver).
- The framework-use lint should then refuse raw `net.Dial` and `tls.Dial` in consumer code outside the effects handle,
  as it does for other effects.

## Affected areas

The effects handle and its dry-mode log, the help document if effects are published there, the framework-use lint,
docs.

## Effort estimate

About 1 to 2 days.
