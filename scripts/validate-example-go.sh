#!/usr/bin/env bash
#
# Validate one Go documentation example.
#
# Invoked by `selfdoc check` for every fenced ```go block marked `validate`,
# via the "examples" key in selfdoc.json. selfdoc assembles the block into a
# scratch file and substitutes its path for {file}; that path arrives as $1.
#
# A marked block is a complete `package main` program importing
# github.com/stricttools/strictcli/go/strictcli. It is compiled inside a
# throwaway module whose `replace` directive points that import path at THIS
# checkout, so examples are validated against the working tree rather than
# whatever version the proxy happens to serve. Compiling is not enough:
# strictcli's registration guardrails are runtime panics, so the built binary
# is also executed (with no arguments, which prints help and exits 0).
#
# Any failure is a hard error -- no skips, no warnings.

set -euo pipefail

if [ $# -ne 1 ]; then
    echo "usage: $(basename "$0") <snippet.go>" >&2
    exit 2
fi

snippet=$1
if [ ! -f "$snippet" ]; then
    echo "validate-example-go: no such snippet: $snippet" >&2
    exit 2
fi

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
go_module="$repo_root/go"

if [ ! -f "$go_module/go.mod" ]; then
    echo "validate-example-go: no Go module at $go_module" >&2
    exit 2
fi
if ! command -v go >/dev/null 2>&1; then
    echo "validate-example-go: the go toolchain is not installed" >&2
    exit 2
fi

# Mirror the real module's language version so the throwaway module can never
# lag behind a feature the examples use.
go_directive=$(awk '/^go /{print $2; exit}' "$go_module/go.mod")
if [ -z "$go_directive" ]; then
    echo "validate-example-go: no 'go' directive in $go_module/go.mod" >&2
    exit 2
fi

work=$(mktemp -d -t strictcli-example-go-XXXXXX)
trap 'rm -rf "$work"' EXIT

cp "$snippet" "$work/main.go"
# The checkout's go.sum already carries every transitive hash the library
# needs, so copying it keeps the build reproducible and offline-capable.
cp "$go_module/go.sum" "$work/go.sum"
cat > "$work/go.mod" <<EOF
module strictcli.example

go $go_directive

require github.com/stricttools/strictcli/go v0.0.0

replace github.com/stricttools/strictcli/go => $go_module
EOF

# A go.work file anywhere above the temp directory would silently override the
# replace directive and validate the example against the wrong sources.
export GOWORK=off
# -mod=mod lets the go command complete the throwaway module's requirement
# graph from the replaced library's own go.mod. Without it the build stops at
# "updates to go.mod needed", since only the direct requirement is written
# above and the library's transitive ones are not. The edits land in the temp
# directory and are discarded with it.
export GOFLAGS=-mod=mod

set +e
build_output=$(cd "$work" && go build -o example . 2>&1)
build_status=$?
set -e
if [ $build_status -ne 0 ]; then
    echo "go example failed to build (exit $build_status):" >&2
    echo "$build_output" >&2
    exit 1
fi

set +e
run_output=$(cd "$work" && ./example 2>&1)
run_status=$?
set -e
if [ $run_status -ne 0 ]; then
    echo "go example built but exited $run_status when run with no arguments:" >&2
    echo "$run_output" >&2
    exit 1
fi
