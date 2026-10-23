---
name: go-programming
description: Use when writing, modifying, or reviewing Go code, tests, package placement, or architecture boundaries in the go-software-factory repository.
---

# Go programming

## Architecture

Use the repository's DDD-inspired onion architecture: domain models express business concepts and invariants, application use cases own workflow and dependency interfaces, and infrastructure implements adapters. Wire dependencies explicitly in service composition roots. Both layer and service dependency rules must allow every import.

Before choosing package ownership, defining interfaces, adding packages or services, changing wiring, or reviewing architecture boundaries, read the [architecture conventions](references/architecture.md). That reference explains the DDD concepts, Go adaptations, module layout, and exact lint rules.

## Enum values

Use uppercase snake case for enum values, such as `FILE_PATH`, `JSON_SCHEMA`, and `AI_CODING_AGENT`. Keep Go constant identifiers idiomatic, such as `ParameterTypeFilePath`.

## Mutable models

Pass domain model pointers and mutate them directly. Never copy model structs by dereferencing pointers (`copy := *model`) or clone models to preserve their original state. Validation or persistence errors may leave models mutated; callers must discard consumed models and reload persisted state when needed.

## Slices of values

Never use slices of pointers such as `[]*domain.Process`; use `[]domain.Process`. Mutate slice elements directly or pass their addresses (`&processes[i]`).

## Adjacent tests

Keep a strict 1:1 mapping: tests for `<filename>.go` belong in adjacent `<filename>_test.go`. Never split them across multiple test files or create test files without matching production files. Only tests under `**/e2e/**` are exempt.

## Verification

Run the relevant Go tests and `make lint`. Existing enforcement lives in the [copy checks](../../../lint/rules.go) and [test-file guard](../../../lint/check-test-files.sh). Lint covers specific patterns; follow all conventions above even when checks pass.
