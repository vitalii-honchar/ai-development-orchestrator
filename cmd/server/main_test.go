package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/core/dsl"
	"go-software-factory/internal/engine/app/process"
	"go-software-factory/internal/engine/infra/storage/sqlite"
	"go-software-factory/internal/worker"
)

const engineServiceName = "engine"
const startEngineEvent = "start engine"

const workerServiceName = "worker"
const startWorkerEvent = "start worker"
const stopWorkerEvent = "stop worker"
const stopEngineEvent = "stop engine"

func TestBuildSolutionExampleRunsCodexStepsInOrder(t *testing.T) {
	source, err := os.ReadFile("../../examples/build-solution.yaml")
	if err != nil {
		t.Fatal(err)
	}
	definition, err := dsl.ProcessDefinitionDslParse(source)
	if err != nil {
		t.Fatal(err)
	}
	workdir := t.TempDir()
	binary := filepath.Join(t.TempDir(), "codex")
	script := `#!/bin/sh
set -eu
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-last-message) output="$2"; shift ;;
  esac
  shift
done
prompt=$(cat)
printf '%s\n' "$prompt" >> prompts.log
plan="$PWD/docs/plans/solution-plan-20261019T120000.123456Z.md"
report="$PWD/docs/plans/implementation-report-20261019T120001.123456Z.md"
case "$prompt" in
  *"Brainstorm a solution"*)
    mkdir -p docs/plans
    printf 'Complete plan with acceptance criteria beyond the short summary.\n' > "$plan"
    printf '{"summary":"Short","path_to_plan":"%s"}' "$plan" > "$output"
    ;;
  *"Implement the solution described"*)
    case "$prompt" in *"<path_to_plan>$plan</path_to_plan>"*) ;; *) exit 11 ;; esac
    cat "$plan" > "$report"
    printf '{"summary":"Implemented","path_to_report":"%s"}' "$report" > "$output"
    ;;
  *"Test the solution described"*)
    case "$prompt" in *"<path_to_plan>$plan</path_to_plan>"*) ;; *) exit 12 ;; esac
    case "$prompt" in *"<path_to_implementation_report>$report</path_to_implementation_report>"*) ;; *) exit 13 ;; esac
    cmp "$plan" "$report"
    test_report="$PWD/docs/plans/test-report-20261019T120002.123456Z.md"
    printf 'PASS: full plan and implementation report were available.\n' > "$test_report"
    printf '{"summary":"PASS","path_to_report":"%s"}' "$test_report" > "$output"
    ;;
  *) exit 14 ;;
esac
`
	// #nosec G306 -- This private test fixture must be executable by its owner.
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FACTORY_CODEX_BIN", binary)
	db, err := sqlite.NewDB(&sqlite.SqliteConfig{
		Path: filepath.Join(t.TempDir(), "example.db"), MaxOpenConns: 2, Pragmas: sqlite.PragmasSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })
	ctx := t.Context()
	if err := db.Start(ctx); err != nil {
		t.Fatal(err)
	}
	definitions := sqlite.NewProcessDefinitionRepository(db.DB)
	if err := definitions.Save(ctx, definition); err != nil {
		t.Fatal(err)
	}
	processes := sqlite.NewProcessRepository(db.DB)
	steps := sqlite.NewProcessStepRepository(db.DB)
	parent, err := process.NewCreateProcessUseCase(processes, definitions).Create(ctx, definition.ID, testParameters(map[string]domain.ParameterValue{
		"working_directory": {Type: domain.ParameterTypeFilePath, Value: workdir},
		"description":       {Type: domain.ParameterTypeString, Value: "Build a task tracker with labels."},
	}))
	if err != nil {
		t.Fatal(err)
	}
	executor := process.NewExecuteProcessesUseCase(processes, steps, definitions)
	w, err := worker.NewWorker(&worker.WorkerDependencies{ProcessStepRepository: steps})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop() })
	for i, name := range []string{"brainstorm", "implement", "test"} {
		if err := executor.Execute(ctx); err != nil {
			t.Fatal(err)
		}
		queued, err := steps.GetByProcessID(ctx, parent.ID)
		if err != nil || len(queued) != i+1 {
			t.Fatalf("stage %q: queued=%+v error=%v", name, queued, err)
		}
		for _, step := range queued {
			if step.State != domain.ProcessStepStatePending {
				continue
			}
			if step.ProcessStepDefinitionName != name || step.Type != domain.ProcessStepTypeAICodingAgent ||
				step.Params.Get("working_directory").Value != workdir ||
				step.Context.String("description") != "Build a task tracker with labels." {
				t.Fatalf("stage %q: incorrect dispatch %+v", name, step)
			}
		}
		if err := w.ExecuteProcessStepsUseCase.Execute(ctx); err != nil {
			t.Fatal(err)
		}
		// Advance the scheduler's due time without waiting a minute between stages.
		if _, err := db.ExecContext(ctx, "UPDATE processes SET executed_at=NULL WHERE id=?", parent.ID.String()); err != nil {
			t.Fatal(err)
		}
	}
	if err := executor.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := processes.GetByID(ctx, parent.ID)
	if err != nil || stored.State != domain.ProcessStateCompleted {
		t.Fatalf("example did not complete: %+v, %v", stored, err)
	}
	if stored.Context == nil || len(stored.Context.StepOutputs) != 3 ||
		stored.Context.Inputs.String("working_directory") != workdir {
		t.Fatalf("process context was not persisted across stages: %+v", stored.Context)
	}
	root, err := os.OpenRoot(workdir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	trace, err := root.ReadFile("prompts.log")
	if err != nil {
		t.Fatal(err)
	}
	position := -1
	for _, heading := range []string{"Brainstorm a solution", "Implement the solution described", "Test the solution described"} {
		next := strings.Index(string(trace), heading)
		if next <= position {
			t.Fatalf("Codex did not receive prompts in order: %s", trace)
		}
		position = next
	}
	if strings.Count(string(trace), "Build a task tracker with labels.") != 3 {
		t.Fatalf("Codex did not receive the description in each prompt: %s", trace)
	}
}

type lifecycleService struct {
	name     string
	events   *[]string
	startErr error
}

func (s lifecycleService) Start() error {
	*s.events = append(*s.events, "start "+s.name)

	return s.startErr
}
func (s lifecycleService) Stop() error {
	*s.events = append(*s.events, "stop "+s.name)

	return nil
}
func TestServerStopsWorkerBeforeEngine(t *testing.T) {
	t.Parallel()
	var events []string
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	services := []serverService{lifecycleService{engineServiceName, &events, nil}, lifecycleService{workerServiceName, &events, nil}}
	if err := run(ctx, services); err != nil {
		t.Fatal(err)
	}
	want := []string{startEngineEvent, startWorkerEvent, stopWorkerEvent, stopEngineEvent}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v", events)
	}
}
func TestServerStopsStartedServicesOnStartupFailure(t *testing.T) {
	t.Parallel()
	var events []string
	cause := errors.New("worker start failed")
	services := []serverService{lifecycleService{engineServiceName, &events, nil}, lifecycleService{workerServiceName, &events, cause}}
	if err := run(t.Context(), services); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{startEngineEvent, startWorkerEvent, stopEngineEvent}) {
		t.Fatal(events)
	}
}

func TestServerExecutesEngineStepsThroughWorker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	binary := filepath.Join(t.TempDir(), "codex")
	script := `#!/bin/sh
set -eu
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-last-message) output="$2"; shift ;;
  esac
  shift
done
cat > /dev/null
printf '%s' '{"summary":"Executed through the worker."}' > "$output"
`
	// #nosec G306 -- This private test fixture must be executable by its owner.
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FACTORY_CODEX_BIN", binary)
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FACTORY_SQLITE_PATH", path)
	t.Setenv("FACTORY_STORAGE_MODE", "sqlite")
	t.Setenv("FACTORY_PROCESS_EXECUTOR_RATE", "20ms")
	t.Setenv("FACTORY_WEB_SERVER_PORT", strconv.Itoa(port))
	t.Setenv("WEB_AUTH_SECRET_KEY", strings.Repeat("x", 64))
	t.Setenv("ADMIN_USERNAME", "")
	t.Setenv("ADMIN_PASSWORD", "")
	db, err := sqlite.NewDB(&sqlite.SqliteConfig{Path: path, MaxOpenConns: 2, Pragmas: sqlite.PragmasSettings})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Stop(context.Background()) })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := db.Start(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	definition,
		err := domain.NewProcessDefinition(domain.WithNewProcessDefinitionID(),
		domain.WithProcessDefinitionName("worker integration"),
		domain.WithProcessDefinitionState(domain.ProcessDefinitionStateActive),
		domain.WithProcessDefinitionVersion(1),
		domain.WithProcessDefinitionCreatedAt(now),
		domain.WithProcessDefinitionUpdatedAt(now),
		domain.WithProcessDefinitionReliability(domain.ProcessDefinitionReliability{MaxRetries: 5}))
	if err != nil {
		t.Fatal(err)
	}
	if err := definition.Graph.AddNode(&domain.ProcessStepDefinition{Name: "example",
		Type: domain.ProcessStepTypeAICodingAgent,
		Params: testParameters(map[string]domain.ParameterValue{
			"prompt":            {Type: domain.ParameterTypeString, Value: "Execute through the worker."},
			"working_directory": {Type: domain.ParameterTypeFilePath, Value: t.TempDir()},
			"output_json": {Type: domain.ParameterTypeJSONSchema, Value: `{
  "type": "object", "properties": {"summary": {"type": "string"}},
  "required": ["summary"], "additionalProperties": false
}`},
		}),
		Reliability: domain.ProcessDefinitionReliability{MaxRetries: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := definition.Graph.AddNode(&domain.ProcessStepDefinition{Name: "end",
		Type: domain.ProcessStepTypeTerminal}); err != nil {
		t.Fatal(err)
	}
	if err := definition.Graph.AddEdge("example", "end"); err != nil {
		t.Fatal(err)
	}
	definitions := sqlite.NewProcessDefinitionRepository(db.DB)
	if err := definitions.Save(ctx, definition); err != nil {
		t.Fatal(err)
	}
	processes := sqlite.NewProcessRepository(db.DB)
	parent, err := process.NewCreateProcessUseCase(processes, definitions).Create(ctx, definition.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	services, err := newServices()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- run(ctx, services) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	steps := sqlite.NewProcessStepRepository(db.DB)
	for {
		select {
		case <-ctx.Done():
			t.Fatal("server did not complete the workflow")
		case <-ticker.C:
		}
		records, err := steps.GetByProcessID(ctx, parent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) == 1 && records[0].State == domain.ProcessStepStateCompleted {
			if records[0].ExecutionPolicy.MaxRetries != 2 {
				t.Fatal("step policy was not queued by the engine")
			}
			if _,
				err := db.ExecContext(ctx,
				"UPDATE processes SET executed_at=NULL WHERE id=?",
				parent.ID.String()); err != nil {
				t.Fatal(err)
			}
		}
		stored, err := processes.GetByID(ctx, parent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.State == domain.ProcessStateCompleted {
			return
		}
	}
}

type stopFailingService struct {
	lifecycleService
	cause error
}

func (s stopFailingService) Stop() error {
	_ = s.lifecycleService.Stop()

	return s.cause
}
func TestServerRetainsEngineWhenWorkerCannotDrain(t *testing.T) {
	t.Parallel()
	var events []string
	cause := errors.New("worker still running")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	services := []serverService{lifecycleService{engineServiceName, &events, nil}, stopFailingService{lifecycleService{workerServiceName, &events, nil}, cause}}
	if err := run(ctx, services); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	want := []string{startEngineEvent, startWorkerEvent, stopWorkerEvent}
	if !reflect.DeepEqual(events, want) {
		t.Fatal(events)
	}
}

func testParameters(values map[string]domain.ParameterValue) *domain.Parameters {
	params, err := domain.NewParameters(values)
	if err != nil {
		panic(err)
	}

	return params
}
