# Build a solution with an AI coding agent

Save `build-solution.yaml` in the control plane's process-definition editor, then
trigger that definition with these inputs:

| Input | Type | Value |
| --- | --- | --- |
| `working_directory` | `FILE_PATH` | Absolute path to the project directory; the worker creates it and initializes Git if needed. |
| `description` | `STRING` | The feature or application to build, including its constraints. |

Programmatic callers construct validated parameters before creating the process:

```go
inputs, err := domain.NewParameters(map[string]domain.ParameterValue{
    "working_directory": {Type: domain.ParameterTypeFilePath, Value: "/absolute/path/to/project"},
    "description":       {Type: domain.ParameterTypeString, Value: "Build a task tracker with labels."},
})
if err != nil {
    return err
}
created, err := eng.CreateProcessUseCase.Create(ctx, definitionID, inputs)
```

The engine and worker must be running. The worker currently uses an installed,
authenticated Codex CLI; `AI_CODING_AGENT` is the process contract and Codex is its
current implementation. Git must be installed on the worker. Before starting
Codex, the worker creates missing directories and initializes a Git repository
when the directory is not already inside one. Existing repositories are reused.

The workflow has three agent steps followed by a terminal node:

1. **brainstorm** creates `docs/plans/solution-plan-<datetime>.md`. Its configured
   `output_json` schema requires `summary` and `path_to_plan`.
2. **implement** receives that exact path through context, reads the complete plan,
   implements it, and creates `docs/plans/implementation-report-<datetime>.md`.
3. **test** receives the plan and implementation-report paths, checks the solution,
   and creates `docs/plans/test-report-<datetime>.md`.

The prompts require unique UTC timestamps and prohibit overwriting earlier files.
Summaries are for display; the complete documents carry the handoff.

Context is a plain map of typed entries. Its sources explicitly select process
inputs or fields from a completed step's structured output:

```yaml
context:
  description:
    type: STRING
    from: inputs.description
  path_to_plan:
    type: FILE_PATH
    from: steps.brainstorm.output.path_to_plan
```

Authors write plain instructions in `inputs.prompt.value` and configure their
response schema in `inputs.output_json.value` with type `JSON_SCHEMA`. JSON Schema
uses its standard lowercase JSON types such as `object` and `string`. The backend
wraps the task in `<constraints>`, `<behaviour>`, `<output-schema>`, and `<inputs>`,
in that order, escaping XML text and appending context last. No prompt interpolation
or step-specific instructions to return a summary are needed.

Each response must match that step's schema before it can complete. Malformed or
schema-invalid responses follow the existing retry/failure policy. A test report
containing a FAIL or BLOCKED verdict is still task data; that verdict alone does
not mark the process as failed.

Attempt timeouts are 10, 30, and 15 minutes. Failed steps retry after
`retry_timeout: 1m`, which is also the default when omitted. Neither the process
nor its steps set `max_retries`, so retries are unlimited. A positive
`max_retries` caps the total number of attempts. The engine currently advances a
running process at most once per minute.
