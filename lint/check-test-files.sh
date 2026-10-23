#!/bin/sh
set -eu

test_files=$(mktemp "${TMPDIR:-/tmp}/factory-test-files.XXXXXX")
trap 'rm -f "$test_files"' EXIT HUP INT TERM
find . -type d \( -name .git -o -name vendor \) -prune -o -type f -name '*_test.go' -print > "$test_files"

status=0
while IFS= read -r test_file; do
    case "$test_file" in
        */e2e/*) continue ;;
    esac

    source_file="${test_file%_test.go}.go"
    if [ ! -f "$source_file" ]; then
        printf '%s\n' "$test_file must map to $source_file; put tests in the matching <filename>_test.go file." >&2
        status=1
    fi
done < "$test_files"
exit "$status"
