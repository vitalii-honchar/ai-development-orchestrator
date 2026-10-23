#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
export GOPROXY=off
export GOSUMDB=off
make lint-tools
golangci_lint="$(go env GOCACHE)/go-software-factory-lint/golangci-lint"
report=$(mktemp "${TMPDIR:-/tmp}/factory-copy-rule.XXXXXX")
trap 'rm -f "$report"' EXIT HUP INT TERM

if "$golangci_lint" run ./lint/testdata/copies --enable-only=gocritic \
    --max-issues-per-linter=0 --max-same-issues=0 --output.text.path="$report"; then
    printf '%s\n' 'Expected the defensive model copies to fail lint.' >&2
    exit 1
fi
if [ "$(awk '/defensive model copy/ { count++ } END { print count+0 }' "$report")" -ne 5 ]; then
    cat "$report" >&2
    printf '%s\n' 'Expected all five defensive-copy patterns to be rejected.' >&2
    exit 1
fi

"$golangci_lint" run ./lint/testdata/pointers --enable-only=gocritic
