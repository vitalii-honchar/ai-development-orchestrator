# Software Factory

A Go process engine with a control plane that renders process definitions from SQLite using the engine's existing `GetAll` method. The UI uses Go HTML templates styled with Tailwind CSS.

## Run

From the repository root:

```sh
cp .env.example .env
mkdir -p .factory-data
go build -o bin/factoryd ./cmd/server
./bin/factoryd
```

Open [the definitions list](http://127.0.0.1:8716/process-definition/).

`.env` configures `FACTORY_WEB_SERVER_PORT` and the SQLite database. The example uses `.factory-data/control-plane-test.db`. Existing environment variables take precedence.

## Fake test data

```sh
go run ./scripts/seed-control-plane
```

Inserts six fake definitions into the configured SQLite database. Repeating the command does not duplicate them.

## Checks

```sh
go test ./...
go vet ./...
make lint
```

Run tests with coverage and require **more than 80%** coverage of new or modified
executable Go lines (Python 3 is required):

```sh
make coverage                         # feature branch or local changes vs origin/main
make coverage COVERAGE_BASE=HEAD~1    # changes since the previous commit
go tool cover -html=coverage.out      # inspect covered and uncovered code in a browser
```

The comparison starts at the merge base with `COVERAGE_BASE` and includes committed,
staged, unstaged, and untracked Go files. The rule checks executable lines measured
by Go's coverage profile; test files, comments, blank lines, and braces do not count.
Coverage across all test packages contributes to the result. Exactly 80% fails;
changes with no measured executable Go lines pass. Uncovered changed lines are
listed in the output.

CI runs the same check on every push. Feature branches are compared with
`origin/main`; pushes to `main` are compared with the previous pushed commit,
including every commit in the push. Coverage totals and uncovered new lines appear
in the GitHub job summary.

Enable the pre-commit hook:

```sh
make hooks
```

The lint tools are pinned as Go tool dependencies in separate modules:

- `lint/golangci-lint/go.mod`: golangci-lint v2.14.0.
- `lint/go-arch-lint/go.mod`: go-arch-lint v1.19.0.

The application and each lint tool have their own committed `vendor/` directory.
`make lint` builds the tools from vendored source and runs the cached executables
from the repository root. Tool builds use `-trimpath` and store binaries under
`$(go env GOCACHE)/go-software-factory-lint` so staged snapshots can reuse them.
Lint disables module and checksum downloads (`GOPROXY=off`, `GOSUMDB=off`) and
uses trimmed paths for package compilation. The first run takes longer while the
tools are built. Formatting and test-file naming checks skip vendored source.

To refresh the vendor directories from dependencies already in the module cache:

```sh
GOPROXY=off GOSUMDB=off go mod vendor
GOPROXY=off GOSUMDB=off go -C lint/golangci-lint mod vendor
GOPROXY=off GOSUMDB=off go -C lint/go-arch-lint mod vendor
```

The hook runs `make lint` against the staged Git tree. This formats the snapshot,
checks that every `<filename>_test.go` has a matching `<filename>.go`, runs both
architecture configurations, and runs golangci-lint. A failed check blocks the
commit. The Makefile is the single entry point for all lint commands.

Test-file naming requires a sibling source file with the exact matching name:
`user_repository_test.go` must have `user_repository.go` in the same directory.
The only exception is tests under an `e2e` directory (`**/e2e/**`), which test the
app as a black box and do not require a matching source file.

The architecture checks use go-arch-lint's import-only mode (`deepScan: false`):

- `lint/architecture-layers.yml` applies the same domain, app, and infra rules to
  every service under `internal/`. Domain packages may import other domain
  packages and `core/validator`. App may import app and domain packages and the
  validator; infra may also import infra and shared core helpers. Service roots
  and config packages assemble dependencies. Integration tests are excluded
  from layer restrictions.
- `lint/architecture-services.yml` applies to production and test files. Core
  may only import its own packages; engine may import core; controlplane may
  import engine and core. Add new services and their allowed dependencies here.

Both checks allow standard-library imports. Unknown internal packages fail
classification. Third-party imports are allowed outside the domain layer;
`core/validator` may use its validation library.

The custom go-critic Ruleguard checks reject
defensive struct copies such as `copy := *model`; mutate model pointers directly.
Scalar pointer dereferences and value conversion for DTOs remain valid.
Run `./lint/check-rules.sh` to verify the copy checks against failing and passing examples.

## Process execution

The engine registers its process executor with `internal/core/scheduler`.
`FACTORY_PROCESS_EXECUTOR_RATE` controls the polling interval (default `1s`);
`FACTORY_PROCESS_EXECUTOR_TIMEOUT` bounds each pass (default `30s`). Both accept
positive Go durations.

`Engine` exposes `CreateProcessUseCase`, `UpdateProcessUseCase`,
`CompleteProcessUseCase`, and `FailProcessUseCase`. Creation takes an ACTIVE
process definition ID and input parameters. Updates and terminal
operations use optimistic process versions.

Each pass selects PENDING processes whose `executed_at` is null or due. Before
traversal, the engine acquires each candidate by optimistically updating
`executed_at` to the current time plus one minute. Version conflicts are skipped;
only acquired processes advance. Process reliability is stored in `executions`,
`execution_time`, and nullable `executed_at` columns, indexed by `(state, executed_at)`.

New processes initialize `CurrentSteps` from the graph roots once. Acquired
processes resume breadth-first traversal from those persisted step names.
Completed steps are replaced by their successors; pending steps and blocked joins
remain in `CurrentSteps`. Changes are saved with optimistic locking, so subsequent
passes and service restarts resume without retracing the completed path.
Completed steps unlock successors, joins wait for all predecessors, and missing ready steps are
inserted once as PENDING. Existing PENDING steps wait for future workers. A FAILED
step fails its process. Reaching a step of type `TERMINAL` completes the process
immediately, without queuing that step for a worker or requiring every graph node
to complete. Empty graphs fail execution; cycles alone do not fail a process.
The worker executes queued steps. Declared step inputs are copied by name and
type from the process inputs; missing required values and type mismatches prevent
dispatch. Step parameter declarations and context can supply typed values or explicit
`from` references to process inputs and completed step outputs. `ProcessStep`
resolves these during creation and validates its execution parameters; the process
executor only orchestrates. Conditional routing and loop iterations are not implemented yet. Requiring a `TERMINAL` step when creating a
definition is recorded in the backlog.

`Process.Reliability.Executions` counts successful acquisitions, including passes
that wait on pending steps. Acquisition saves the increment together with the
next execution time. A positive definition `MaxRetries=N` permits N passes to
traverse; the next acquisition fails the process before traversal. Omitted or zero
`max_retries` permits unlimited passes. Orchestration errors are returned directly, and a later due
scheduler tick retries from persisted state. All passes currently use the fixed
one-minute acquisition delay; definition `RetryDelay` does not affect scheduling.
Retries preserve persisted steps. Lowering a definition budget below a pending
process's counter fails that process on its next due pass without queuing more steps.
Step insertion also atomically checks that the process is still PENDING. A definition
read failure leaves the process available for the next due pass; a missing definition
fails it. Each pass reads the current process definition; running processes do not
pin a definition snapshot.

`ExecutionTime` records total elapsed time since `CreatedAt` when a process completes or fails.

## Start and inspect executions

Open an ACTIVE process definition and select **Start execution**. Supply its
declared inputs and submit the form to queue a new process. Required fields and
JSON inputs are validated before creation. The worker executes the queued steps.

The **Processes** navigation item opens `/processes/`, with the newest executions
first. Filter by process definition, or select **View processes** on a definition
to open its execution history with the filter selected. Refresh preserves the
filter; **Clear filter** restores the full history.

Open an execution to inspect its state, current steps, inputs, result,
execution passes, and timestamps. Definitions and executions share a top-to-bottom
graph: AI coding tasks are blue and terminal end events are red
double circles. Graph nodes display step names only. Select a node to inspect its
type, state, timestamps, attempts, duration, parameters, and result; **Browse step details**
also works if the graph cannot load. Codex JSON results display the summary with
expandable structured output. Use **Refresh execution** to read the latest persisted state.

Graph connections reflect the current definition, since executions do not pin a
definition snapshot. Unqueued steps and end events appear too. End events have no
worker record or separate timing. The UI does not infer which end event was
reached from the current, editable definition.

For a runnable example, create a definition in the UI using
[`examples/test-process.yaml`](examples/test-process.yaml). It runs an `AI_CODING_AGENT`
step followed by `TERMINAL`. Start it with:

- `working_directory`: an absolute path to the repository to inspect.
- `prompt`: `Read README.md and summarize this repository in two sentences. Do not modify files.`
- `reasoning_effort`: optionally `low` for this small inspection.
- `model`: optionally override the worker's default model.

Configure `FACTORY_CODEX_BIN` to point to an authenticated, current Codex CLI
supporting `exec --output-schema`. If several CLIs are installed, use its absolute
path. The process can remain PENDING after its step completes until the engine's
next eligible pass; the current acquisition delay is one minute. A terminal node
completes the process directly and does not create a persisted worker step.

To see a split and join, use
[`examples/branching-test-process.yaml`](examples/branching-test-process.yaml)
with the same inputs. Its five steps are `prepare` → parallel `inspect` / `verify`
→ `finish` → `end`. The four worker steps use `AI_CODING_AGENT`, with structured
summaries passed through process context. The engine waits for both branches
before queuing `finish`. This example makes four Codex calls; all prompts request
inspection without modifying files.

## AI coding agent steps

Use `type: AI_CODING_AGENT`. The current implementation runs Codex CLI
non-interactively with `--output-schema`; install and authenticate it before
starting the worker. `FACTORY_CODEX_BIN` selects its executable (default `codex`).
Every AI coding step launches Codex with `--sandbox danger-full-access` and
`--ask-for-approval never`, allowing unrestricted execution, network, and Docker
access without approval prompts. There is no permission setting.

Codex runs with `--json`. Its stdout events and stderr lines are logged live as
`event=codex_output`, including process ID, step ID/name, attempt, execution ID,
stream, and a sequence number within that stream. Native thread-start events also
include `codex_thread_id`. Output longer than 16 KiB is split into ordered chunks;
`partial=true` marks a fragment, including trailing output without a newline.
These audit records contain Codex messages, tool commands, and tool output. The
worker does not add the input prompt as a log field. The last 16 KiB of stderr is
retained for execution errors; the full stream remains available through the logger.

See [the three-step build example](examples/README.md) for a process that accepts a
working directory and feature description, then brainstorms, implements, and tests
the solution using full artifact files passed through explicit context references.

| Step input | Type | Required | Meaning |
| --- | --- | --- | --- |
| `working_directory` | `FILE_PATH` | Yes | Absolute path to the project directory; created if missing. |
| `prompt` | `STRING` | Yes | Plain task instructions. |
| `output_json` | `JSON_SCHEMA` | Yes | The JSON Schema for this step's response. |
| `model` | `STRING` | No | Default `gpt-6.1-sol`. |
| `reasoning_effort` | `STRING` | No | Default `xhigh`; supported values depend on the model. |

Domain parameter types use uppercase snake case: `STRING`, `REFERENCE`,
`REFERENCES_ARRAY`, `JSON`, `FILE_PATH`, and `JSON_SCHEMA`. `Parameters` is a shared,
validated container for declarations and values. Its constructor, setters, and
JSON decoding validate typed entries. `FILE_PATH` validates absolute path syntax;
agent step creation rejects an existing file as `working_directory` but accepts a
missing directory. Before launching Codex, the worker creates missing directories
and initializes Git if the directory is not already inside a repository. Git must
be installed on the worker. Codex retains its repository check and uses the
full-access execution mode.
Schemas are self-contained and may use local references, such as `$defs`.
Existing definitions and persisted values using `CODEX` or lowercase parameter
types must be converted or recreated. Agent steps now require `output_json`.

Step reliability separates the attempt duration from the delay after a failure:

| Setting | Behavior |
| --- | --- |
| `timeout` | Maximum duration of one attempt; the worker also enforces its one-hour execution limit. |
| `retry_timeout` | Delay after a failed attempt, default `1m`. |
| `max_retries` | Omitted or zero means unlimited attempts; a positive value caps total attempts. |

Execution, setup, schema, matching, timeout, cancellation, and executor panic
errors retain their details and schedule the next step attempt after
`retry_timeout`. Running steps retain their one-hour acquisition lease for crash
recovery; a returned failure replaces that lease with the retry time. Optimistic
locking prevents a stale attempt from overwriting a newer acquisition.

A step's `context` is a plain map of typed entries, for example:

```yaml
context:
  description:
    type: STRING
    from: inputs.description
  path_to_plan:
    type: FILE_PATH
    from: steps.brainstorm.output.path_to_plan
```

The DSL validates `from` expressions while parsing the definition. `ProcessContext`
holds the original inputs and completed step outputs; it resolves references when
`ProcessStep` creates its execution parameters. Missing sources and type mismatches
prevent dispatch. Configured `value` entries take precedence over same-named process inputs.
`Parameters` stores validated typed data and opaque source metadata without knowing
the process expression syntax.

`Process.Context` is stored as JSONB in the existing `processes.context` column.
`Process.Context.Inputs` replaces `Process.Inputs`. The migration preserves existing
inputs and completed outputs, and the engine records newly completed outputs while
advancing the process, including when a join is waiting. No separate context table is used.
No prompt templates are evaluated. The backend renders XML sections in this order:
`constraints`, `behaviour`, `output-schema`, `inputs`. Context values are escaped and
rendered as named elements in the final section.

The `output_json` parameter is passed directly as the CLI's response schema and
included in the structured prompt. The worker saves the returned JSON as
`step.Result.Get("output")`, with type `JSON`; its string value contains the JSON
text. `ProcessStep.Complete` checks the response against the configured schema
before marking completion. A response can therefore contain both a display
summary and artifact paths without forcing a fixed response shape on other tasks.

The worker explicitly selects the model and reasoning effort, always uses
`danger-full-access` with approval policy `never`, and inherits authentication
settings from its environment/configuration. Nonzero exits retain the exit status and the bounded
stderr tail in the `error` result. Malformed or schema-invalid output
also follows the step's retry policy. Cancellation terminates the CLI process
group, including child commands. The adapter currently targets Unix systems.
