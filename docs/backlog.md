# Backlog

Tasks are added, updated, or removed at the user's request. Recording a task does not authorize implementation.

When the user asks to execute the backlog, work through unchecked tasks in listed order, or follow the user's specified selection and order. Mark tasks complete only after implementation and verification.

- [ ] Refactor `ExecuteProcessesUseCase` into a reusable transactional outbox processing abstraction.
  Context: Extract the shared item-processing flow from `internal/engine/app/process/execute_processes_usecase.go` into library code that can be reused by both process and step processors, keeping their domain-specific processing separate.

- [ ] Require at least one `TERMINAL` step when creating a process definition.
  Context: Return an error if the graph has no step of type `ProcessStepTypeTerminal`. The executor completes a process when it reaches a `TERMINAL` step. Add creation validation in `internal/engine/app/processdefinition/create_process_definition_usecase.go`, with the model in `internal/core/domain/process_definition.go`; this validation is deferred.

- [ ] Extract process acquisition into `AcquireProcessesUseCase`.
  Context: Move `ExecuteProcessesUseCase.acquire` from `internal/engine/app/process/execute_processes_usecase.go` into a separate use case and delegate acquisition to it to simplify `ExecuteProcessesUseCase`. Preserve the existing acquisition, persistence/reload, cancellation, and error-handling behavior.

- [ ] Configure a code coverage report and GitHub CI.
  Context: Set up automated Go checks in GitHub CI and generate a code coverage report for the project, including `ExecuteProcessesUseCase` in `internal/engine/app/process/execute_processes_usecase.go`. Configuration is deferred; no coverage threshold or reporting service has been selected.

- [ ] Create architecture tests that enforce the dependency rule.
  Context: Verify that package dependencies point inward toward the domain and that inner layers do not depend on outer layers. Fail tests when an import violates the rule.

- [ ] Refactor slices of pointers into slices of values.
  Context: Replace `[]*T` with `[]T` throughout the project to follow `AGENTS.md`, including process and process-step repository contracts and implementations, `internal/engine/app/process/execute_processes_usecase.go`, and `internal/core/domain/process_graph.go`. Update callers and corresponding tests while preserving direct model mutation through slice elements or their addresses.
