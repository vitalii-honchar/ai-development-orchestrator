"""Enforce coverage for newly added or modified Go lines."""

import argparse
from bisect import bisect_right
import os
from pathlib import Path
import re
import subprocess
import sys


TOKENS = re.compile(
    rb'//[^\n]*|/\*.*?\*/|"(?:\\.|[^"\\])*"|\'(?:\\.|[^\'\\])*\'|`[^`]*`|[^\s]',
    re.DOTALL,
)
RECORD = re.compile(r"(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)")
HUNK = re.compile(r"^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@", re.MULTILINE)


def git(*arguments):
    return subprocess.run(
        ["git", *arguments], check=True, text=True, capture_output=True,
    ).stdout


def changed_lines(base):
    baseline = git("merge-base", "HEAD", base).strip()
    paths = git("diff", "--no-ext-diff", "--no-renames", "--name-only", "-z",
                "--diff-filter=AM", baseline, "--", "*.go").split("\0")
    untracked = git("ls-files", "--others", "--exclude-standard", "-z", "--", "*.go").split("\0")
    changes = {}
    for path in sorted(set(paths + untracked) - {""}):
        if path.endswith("_test.go") or "vendor" in Path(path).parts:
            continue
        if path in untracked:
            changes[path] = set(range(1, len(Path(path).read_bytes().splitlines()) + 1))
            continue
        diff = git("diff", "--no-ext-diff", "--no-renames", "--unified=0", baseline, "--", path)
        lines = set()
        for match in HUNK.finditer(diff):
            start, count = int(match[1]), int(match[2] if match[2] is not None else 1)
            lines.update(range(start, start + count))
        changes[path] = lines
    return changes


def read_profile(path):
    module = next(line.split()[1].strip('"') for line in Path("go.mod").read_text().splitlines()
                  if line.startswith("module "))
    records = {}
    with path.open() as profile:
        if profile.readline().strip() not in {"mode: set", "mode: count", "mode: atomic"}:
            raise ValueError("invalid coverage profile header")
        for line in profile:
            match = RECORD.fullmatch(line.strip())
            if match is None:
                raise ValueError(f"invalid coverage record: {line.strip()}")
            filename = match[1]
            if filename.startswith(module + "/"):
                filename = filename[len(module) + 1:]
            elif Path(filename).is_absolute():
                filename = str(Path(filename).relative_to(Path.cwd()))
            filename = str(Path(filename))
            start_line, start_col, end_line, end_col, statements, count = map(int, match.groups()[1:])
            if not statements:
                continue
            block = (start_line, start_col, end_line, end_col)
            blocks = records.setdefault(filename, {})
            blocks[block] = blocks.get(block, False) or count > 0
    return records


def executable_lines(path, blocks):
    source = path.read_bytes()
    offsets = [0] + [match.end() for match in re.finditer(b"\n", source)]
    ranges = [(offsets[first - 1] + first_col - 1, offsets[last - 1] + last_col - 1, hit)
              for (first, first_col, last, last_col), hit in blocks.items()]
    lines = {}
    for token in TOKENS.finditer(source):
        value = token.group()
        if value.startswith((b"//", b"/*")) or value in {b"{", b"}", b";"}:
            continue
        for start, end, hit in ranges:
            first, last = max(start, token.start()), min(end, token.end())
            if first >= last:
                continue
            for line in range(bisect_right(offsets, first), bisect_right(offsets, last - 1) + 1):
                # A line containing multiple instrumented blocks must cover all of them.
                lines[line] = lines.get(line, True) and hit
    return lines


def report(changes, records):
    total, covered = 0, 0
    missing = []
    for filename, changed in changes.items():
        measured = executable_lines(Path(filename), records.get(filename, {}))
        for line in sorted(changed & measured.keys()):
            total += 1
            if measured[line]:
                covered += 1
            else:
                missing.append(f"{filename}:{line}")
    if total:
        message = (f"New-code coverage: {100 * covered / total:.2f}% "
                   f"({covered}/{total} executable lines; required > 80%).")
    else:
        message = "New-code coverage: no executable Go lines changed."
    print(message)
    if missing:
        print("Uncovered new code:\n" + "\n".join(missing))
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with Path(summary).open("a") as output:
            output.write(f"\n### New-code coverage\n\n{message}\n")
            if missing:
                output.write("\nUncovered new code:\n\n```text\n" + "\n".join(missing) + "\n```\n")
    return 1 if total and covered * 100 <= total * 80 else 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", default="origin/main", help="Git baseline (default: origin/main)")
    parser.add_argument("--profile", type=Path, default=Path("coverage.out"))
    arguments = parser.parse_args()
    try:
        records = read_profile(arguments.profile)
        return report(changed_lines(arguments.base), records)
    except (OSError, ValueError, StopIteration, IndexError, subprocess.CalledProcessError) as error:
        detail = error.stderr.strip() if isinstance(error, subprocess.CalledProcessError) else str(error)
        print(f"Coverage check failed: {detail}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
