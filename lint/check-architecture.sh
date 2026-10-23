#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
arch_lint="$(go env GOCACHE)/go-software-factory-lint/go-arch-lint"
fixture=$(mktemp -d "${TMPDIR:-/tmp}/factory-architecture.XXXXXX")
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
mkdir -p "$fixture/lint"
cp go.mod "$fixture/go.mod"
cp lint/architecture-*.yml "$fixture/lint/"
for package in core/domain core/app/outbox core/validator core/config core/dsl core/logger core/scheduler core/service engine/app/process worker/app/processstep worker/app/stepexecutor worker/infra/scheduler controlplane; do
 mkdir -p "$fixture/internal/$package"
 printf 'package sample\nconst Value=1\n' > "$fixture/internal/$package/probe.go"
done
check() {
 "$arch_lint" check --project-path "$fixture" --arch-file "$fixture/lint/architecture-$1.yml" --output-color=false
}
check layers
check services
reject() {
 rules=$1
 source=$2
 dependency=$3
 printf 'package sample\nimport "go-software-factory/internal/%s"\nvar _=sample.Value\n' "$dependency" > "$fixture/internal/$source/probe.go"
 if check "$rules" > "$fixture/output" 2>&1; then
  cat "$fixture/output" >&2
  printf 'Expected %s to reject %s importing %s\n' "$rules" "$source" "$dependency" >&2
  exit 1
 fi
 if ! grep -Eq 'dependency|not allowed|not in allowed|not matches' "$fixture/output"; then
  cat "$fixture/output" >&2
  printf 'Architecture fixture failed for an unexpected reason\n' >&2
  exit 1
 fi
 printf 'package sample\nconst Value=1\n' > "$fixture/internal/$source/probe.go"
}
reject services worker/app/processstep engine/app/process
reject services core/app/outbox worker/app/processstep
reject services engine/app/process worker/app/processstep
reject layers worker/app/processstep worker/infra/scheduler
printf '%s\n' 'Architecture import rejection checks passed.'
