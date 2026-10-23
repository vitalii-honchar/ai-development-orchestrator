package process_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/core/app/outbox"
	"go-software-factory/internal/core/domain"
)

//nolint:paralleltest // Captures the process-global slog logger; parallel tests resume after this test returns.
func TestProcessExecutionLogsAttemptsAndProgress(t *testing.T) {
	const (
		dispatchOutcome   = "dispatch"
		waitingOutcome    = "waiting"
		completedOutcome  = "completed"
		failedStepOutcome = "failed step"
		readErrorOutcome  = "read error"
		idleOutcome       = "idle"
	)
	for _, outcome := range []string{
		dispatchOutcome, waitingOutcome, completedOutcome, failedStepOutcome, readErrorOutcome, idleOutcome,
	} {
		t.Run(outcome, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			definition := newTestDefinition(t, []string{testRoot}, nil)
			fixture := newExecutionFixture(t, definition, []string{testRoot})
			want := "process_attempt_finished"
			switch outcome {
			case waitingOutcome:
				fixture.stepStates[testRoot] = domain.ProcessStepStatePending
			case completedOutcome:
				definition.Graph.Nodes[testRoot].Type = domain.ProcessStepTypeTerminal
				want = "process_completed"
			case failedStepOutcome:
				fixture.stepStates[testRoot] = domain.ProcessStepStateFailed
				want = "process_failed"
			case readErrorOutcome:
				fixture.definitions.getByID = func(context.Context, uuid.UUID) (*domain.ProcessDefinition, error) {
					return nil, errors.New("definition storage unavailable")
				}
				want = "process_attempt_failed"
			case idleOutcome:
				fixture.records = nil
			}
			err := fixture.executor().Execute(t.Context())
			if (err != nil) != (outcome == readErrorOutcome) {
				t.Fatalf("unexpected execution error: %v", err)
			}
			if outcome == idleOutcome {
				if output.Len() != 0 {
					t.Fatalf("idle polling produced logs: %s", &output)
				}

				return
			}
			events := processLogEvents(t, &output)
			start := events["process_attempt_started"]
			finish := events[want]
			if start == nil || finish == nil || start["started_at"] == nil ||
				start["attempt"] != float64(1) || finish["duration"] == nil {
				t.Fatalf("attempt timing or outcome was missing: %s", &output)
			}
			for _, event := range events {
				if event["process_id"] != fixture.pending.ID.String() {
					t.Fatalf("event cannot be correlated to its process: %v", event)
				}
			}
			switch outcome {
			case dispatchOutcome:
				queued := events["process_step_dispatch_requested"]
				if queued == nil || queued["step_id"] != fixture.queued[0].ID.String() ||
					queued["step_name"] != testRoot || finish["next_attempt_at"] == nil {
					t.Fatalf("step dispatch or next attempt was missing: %s", &output)
				}
			case waitingOutcome:
				if events["process_waiting_for_step"] == nil {
					t.Fatalf("waiting reason was missing: %s", &output)
				}
			case failedStepOutcome:
				cause := events["process_blocked_by_failed_step"]
				if cause == nil || cause["error"] != "worker failure" || finish["error"] == nil {
					t.Fatalf("terminal failure lost the underlying step error: %s", &output)
				}
			case readErrorOutcome:
				if !strings.Contains(finish["error"].(string), "load process definition") ||
					!strings.Contains(finish["error"].(string), "definition storage unavailable") {
					t.Fatalf("error did not identify the failed operation and cause: %s", &output)
				}
			}
		})
	}
}

func processLogEvents(t *testing.T, output *bytes.Buffer) map[string]map[string]any {
	t.Helper()
	events := make(map[string]map[string]any)
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events[event["event"].(string)] = event
	}

	return events
}

const (
	testPrompt = "prompt"
	testModel  = "model"
)

func TestExecuteProcessesUseCaseResolvesOnlyCompletedOutputs(t *testing.T) {
	t.Parallel()
	for _, state := range []domain.ProcessStepState{domain.ProcessStepStateCompleted, domain.ProcessStepStatePending} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()
			definition := newTestDefinition(t, []string{testRoot}, nil)
			definition.Graph.Nodes[testRoot].Params = testParameters(map[string]domain.ParameterValue{
				"source": {Type: domain.ParameterTypeString, From: "steps.prior.output.value"},
			})
			fixture := newExecutionFixture(t, definition, []string{testRoot})
			fixture.steps.getByProcessID = func(context.Context, uuid.UUID) ([]domain.ProcessStep, error) {
				return []domain.ProcessStep{{ProcessStepDefinitionName: "prior", State: state,
					Result: testParameters(map[string]domain.ParameterValue{
						"output": {Type: domain.ParameterTypeJSON, Value: `{"value":"complete calculation"}`},
					})}}, nil
			}
			err := fixture.executor().Execute(t.Context())
			if state == domain.ProcessStepStatePending {
				if err == nil || len(fixture.queued) != 0 {
					t.Fatal("dispatched a step using an unfinished calculation")
				}

				return
			}
			if err != nil || len(fixture.queued) != 1 ||
				fixture.queued[0].Params.String("source") != "complete calculation" {
				t.Fatalf("completed output was not resolved: %+v, %v", fixture.queued, err)
			}
		})
	}
}

func TestExecuteProcessesUseCasePassesDeclaredInputs(t *testing.T) {
	t.Parallel()
	definition := newTestDefinition(t, []string{testRoot}, nil)
	definition.Graph.Nodes[testRoot].Params = testParameters(map[string]domain.ParameterValue{
		testPrompt: {Type: domain.ParameterTypeString, Required: true},
		testModel:  {Type: domain.ParameterTypeString},
		"absent":   {Type: domain.ParameterTypeString},
	})
	fixture := newExecutionFixture(t, definition, []string{testRoot})
	fixture.records[0].Context.Inputs = testParameters(map[string]domain.ParameterValue{
		testPrompt: {Type: domain.ParameterTypeString, Value: "Inspect the repository."},
		testModel:  {Type: domain.ParameterTypeString, Value: "selected-model"},
		"other":    {Type: domain.ParameterTypeString, Value: "unrelated input"},
	})
	if err := fixture.executor().Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := testParameters(map[string]domain.ParameterValue{
		testPrompt: {Type: domain.ParameterTypeString, Value: "Inspect the repository."},
		testModel:  {Type: domain.ParameterTypeString, Value: "selected-model"},
	})
	if len(fixture.queued) != 1 || !reflect.DeepEqual(fixture.queued[0].Params, want) {
		t.Fatalf("queued steps = %+v; want declared inputs %+v", fixture.queued, want)
	}
}

func TestExecuteProcessesUseCaseUsesStepConfiguredValues(t *testing.T) {
	t.Parallel()
	definition := newTestDefinition(t, []string{testRoot}, nil)
	definition.Graph.Nodes[testRoot].Params = testParameters(map[string]domain.ParameterValue{
		testPrompt: {Type: domain.ParameterTypeString, Value: "Keep {{ .description }} as plain task text."},
	})
	fixture := newExecutionFixture(t, definition, []string{testRoot})
	fixture.records[0].Context.Inputs = testParameters(map[string]domain.ParameterValue{
		testPrompt: {Type: domain.ParameterTypeString, Value: "runtime prompt"},
	})
	if err := fixture.executor().Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.queued) != 1 ||
		fixture.queued[0].Params.String(testPrompt) != "Keep {{ .description }} as plain task text." {
		t.Fatalf("incorrect step configuration: %+v", fixture.queued)
	}
}

func TestExecuteProcessesUseCaseRejectsInvalidStepInputs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		required bool
		inputs   *domain.Parameters
	}{
		{name: "required input missing", required: true},
		{name: "required input wrong type", required: true,
			inputs: testParameters(map[string]domain.ParameterValue{
				testPrompt: {Type: domain.ParameterTypeReference, Value: "PROCESS:prompt"},
			})},
		{name: "optional input wrong type",
			inputs: testParameters(map[string]domain.ParameterValue{
				testPrompt: {Type: domain.ParameterTypeReference, Value: "PROCESS:prompt"},
			})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			definition := newTestDefinition(t, []string{testRoot}, nil)
			definition.Graph.Nodes[testRoot].Params = testParameters(map[string]domain.ParameterValue{
				testPrompt: {Type: domain.ParameterTypeString, Required: tc.required},
			})
			fixture := newExecutionFixture(t, definition, []string{testRoot})
			fixture.records[0].Context.Inputs = tc.inputs
			err := fixture.executor().Execute(t.Context())
			if err == nil || !strings.Contains(err.Error(), testPrompt) || len(fixture.queued) != 0 {
				t.Fatalf("error=%v queued=%+v; want input error without dispatch", err, fixture.queued)
			}
		})
	}
}

func TestExecuteProcessesUseCaseTraversal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		nodes       []string
		edges       [][2]string
		terminals   []string
		current     []string
		states      map[string]domain.ProcessStepState
		wantQueued  []string
		wantCurrent []string
		wantState   domain.ProcessState
		wantUpdates int
	}{
		{
			name: "queue missing step", nodes: []string{testRoot}, current: []string{testRoot},
			wantQueued: []string{testRoot}, wantCurrent: []string{testRoot},
			wantState: domain.ProcessStatePending, wantUpdates: 1,
		},
		{
			name: "wait for pending step", nodes: []string{testRoot}, current: []string{testRoot},
			states:      map[string]domain.ProcessStepState{testRoot: domain.ProcessStepStatePending},
			wantCurrent: []string{testRoot}, wantState: domain.ProcessStatePending, wantUpdates: 1,
		},
		{
			name: "resume from saved position", nodes: []string{testRoot, "a"},
			edges: [][2]string{{testRoot, "a"}}, current: []string{"a"},
			states:      map[string]domain.ProcessStepState{"a": domain.ProcessStepStatePending},
			wantCurrent: []string{"a"},
			wantState:   domain.ProcessStatePending, wantUpdates: 1,
		},
		{
			name: "blocked join remains current", nodes: []string{"a", "b", testJoin},
			edges: [][2]string{{"a", testJoin}, {"b", testJoin}}, current: []string{testJoin},
			states: map[string]domain.ProcessStepState{
				"a": domain.ProcessStepStateCompleted, "b": domain.ProcessStepStatePending,
			},
			wantCurrent: []string{testJoin}, wantState: domain.ProcessStatePending, wantUpdates: 2,
		},
		{
			name: "converging paths queue join once", nodes: []string{"a", "b", testJoin},
			edges: [][2]string{{"a", testJoin}, {"b", testJoin}}, current: []string{"a", "b"},
			states: map[string]domain.ProcessStepState{
				"a": domain.ProcessStepStateCompleted, "b": domain.ProcessStepStateCompleted,
			},
			wantQueued: []string{testJoin}, wantCurrent: []string{testJoin},
			wantState: domain.ProcessStatePending, wantUpdates: 2,
		},
		{
			name: "duplicate current positions visit once", nodes: []string{"a"}, current: []string{"a", "a"},
			wantQueued: []string{"a"}, wantCurrent: []string{"a"},
			wantState: domain.ProcessStatePending, wantUpdates: 2,
		},
		{
			name: "completed cycle stops at new work", nodes: []string{"a", "b", "c"},
			edges: [][2]string{{"a", "b"}, {"b", "a"}, {"b", "c"}}, current: []string{"a"},
			states: map[string]domain.ProcessStepState{
				"a": domain.ProcessStepStateCompleted, "b": domain.ProcessStepStateCompleted,
			},
			wantQueued: []string{"c"}, wantCurrent: []string{"c"},
			wantState: domain.ProcessStatePending, wantUpdates: 2,
		},
		{
			name: "terminal ends completed chain in one pass", nodes: []string{"a", "b", testTerminal},
			edges: [][2]string{{"a",
				"b"},
				{"b",
					testTerminal}},
			terminals: []string{testTerminal},
			current:   []string{"a"},

			states: map[string]domain.ProcessStepState{
				"a": domain.ProcessStepStateCompleted, "b": domain.ProcessStepStateCompleted,
			},
			wantCurrent: []string{"a"}, wantState: domain.ProcessStateCompleted, wantUpdates: 2,
		},
		{
			name: "breadth first terminal precedes deeper work", nodes: []string{testRoot, "a", testBreadthTerminal},
			edges: [][2]string{{testRoot, "a"}}, terminals: []string{testBreadthTerminal},
			current: []string{testRoot, testBreadthTerminal},
			states:  map[string]domain.ProcessStepState{testRoot: domain.ProcessStepStateCompleted},
			wantCurrent: []string{testRoot,
				testBreadthTerminal},
			wantState:   domain.ProcessStateCompleted,
			wantUpdates: 2,
		},
		{
			name: "completed graph without terminal stays pending", nodes: []string{"a"}, current: []string{"a"},
			states:    map[string]domain.ProcessStepState{"a": domain.ProcessStepStateCompleted},
			wantState: domain.ProcessStatePending, wantUpdates: 2,
		},
		{
			name: "empty position never restarts roots", nodes: []string{testRoot},
			wantState: domain.ProcessStatePending, wantUpdates: 1,
		},
		{
			name: "missing current node fails process", nodes: []string{testRoot}, current: []string{"removed"},
			wantCurrent: []string{"removed"}, wantState: domain.ProcessStateFailed, wantUpdates: 2,
		},
		{
			name: "empty graph fails process", current: []string{testRoot},
			wantCurrent: []string{testRoot}, wantState: domain.ProcessStateFailed, wantUpdates: 2,
		},
		{
			name: "failed step outside current position fails process", nodes: []string{testRoot, "a"},
			current: []string{testRoot}, states: map[string]domain.ProcessStepState{"a": domain.ProcessStepStateFailed},
			wantCurrent: []string{testRoot}, wantState: domain.ProcessStateFailed, wantUpdates: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			definition := newTestDefinition(t, tc.nodes, tc.edges, tc.terminals...)
			fixture := newExecutionFixture(t, definition, tc.current)
			if tc.states != nil {
				fixture.stepStates = tc.states
			}
			fixture.acquired.Result = testParameters(map[string]domain.ParameterValue{
				testOutput: {Type: domain.ParameterTypeString, Value: "existing output"},
			})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if err := fixture.executor().Execute(ctx); err != nil {
				t.Fatal(err)
			}
			queued := make([]string, 0, len(fixture.queued))
			for _, step := range fixture.queued {
				queued = append(queued, step.ProcessStepDefinitionName)
				if step.ID == uuid.Nil() || step.ProcessID != fixture.acquired.ID ||
					step.State != domain.ProcessStepStatePending ||
					step.Type != definition.Graph.Nodes[step.ProcessStepDefinitionName].Type {
					t.Fatalf("incorrect worker dispatch: %+v", step)
				}
			}
			if !slices.Equal(queued, tc.wantQueued) || !slices.Equal(fixture.acquired.CurrentSteps, tc.wantCurrent) ||
				fixture.acquired.State != tc.wantState || len(fixture.updatedIDs) != tc.wantUpdates {
				t.Fatalf("queued=%v current=%v state=%s updates=%d; want %v %v %s %d",
					queued, fixture.acquired.CurrentSteps, fixture.acquired.State, len(fixture.updatedIDs),
					tc.wantQueued, tc.wantCurrent, tc.wantState, tc.wantUpdates)
			}
			if tc.wantState == domain.ProcessStateCompleted &&
				(fixture.acquired.Result.Get(testOutput).Value != "existing output" ||
					fixture.acquired.Reliability.ExecutionTime <= 0) {
				t.Fatal("terminal completion lost the result or elapsed time")
			}
			if tc.wantState == domain.ProcessStateFailed && fixture.acquired.Result.String("error") == "" {
				t.Fatal("process failure omitted its cause")
			}
		})
	}
}

func TestExecuteProcessesUseCaseExecutionBudget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		limit      int
		executions int
		wantState  domain.ProcessState
	}{
		{"omitted budget allows initial pass", 0, 1, domain.ProcessStatePending},
		{"omitted budget permits unlimited passes", 0, 1000, domain.ProcessStatePending},
		{"last allowed pass executes", 2, 2, domain.ProcessStatePending},
		{"next pass fails before traversal", 2, 3, domain.ProcessStateFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			definition := newTestDefinition(t, []string{testRoot}, nil)
			definition.Reliability.MaxRetries = tc.limit
			fixture := newExecutionFixture(t, definition, []string{testRoot})
			fixture.pending.Reliability.Executions = tc.executions - 1
			if err := fixture.executor().Execute(t.Context()); err != nil {
				t.Fatal(err)
			}
			if fixture.acquired.State != tc.wantState || fixture.acquired.Reliability.Executions != tc.executions {
				t.Fatalf("budget state=%s executions=%d",
					fixture.acquired.State,
					fixture.acquired.Reliability.Executions)
			}
			if tc.wantState == domain.ProcessStateFailed {
				if len(fixture.queued) != 0 || fixture.acquired.Result.String("error") == "" {
					t.Fatal("exhausted process queued work or omitted failure details")
				}
			} else if len(fixture.queued) != 1 {
				t.Fatal("execution budget failed before the last permitted pass")
			}
		})
	}
}

func TestExecuteProcessesUseCaseQueryErrorAndEmptyBatch(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{nil, errors.New("pending query unavailable")} {
		fixture := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
		fixture.processes.getPending = func(context.Context) ([]domain.Process, error) { return nil, cause }
		if err := fixture.executor().Execute(t.Context()); !errors.Is(err, cause) {
			t.Fatalf("pending query error=%v want %v", err, cause)
		}
		if len(fixture.updatedIDs) != 0 || len(fixture.queued) != 0 {
			t.Fatal("empty or failed query acquired or dispatched work")
		}
	}
}

func TestExecuteProcessesUseCaseMissingDefinitionFailsProcess(t *testing.T) {
	t.Parallel()
	fixture := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
	fixture.definitions.getByID = func(context.Context, uuid.UUID) (*domain.ProcessDefinition, error) {
		return nil, domain.ErrProcessDefinitionNotFound
	}
	if err := fixture.executor().Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fixture.acquired.State != domain.ProcessStateFailed || len(fixture.queued) != 0 ||
		!strings.Contains(fixture.acquired.Result.String("error"), domain.ErrProcessDefinitionNotFound.Error()) {
		t.Fatalf("missing definition did not fail process: %+v", fixture.acquired)
	}
}

type processFixturePair struct {
	processes *processRepositoryStub
	steps     *stepRepositoryStub
}

func TestExecuteProcessesUseCaseAcquiresAllCandidatesBeforeTraversal(t *testing.T) {
	t.Parallel()
	first := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
	second := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
	var claims atomic.Int32
	for _, fixture := range []processFixturePair{{first.processes, first.steps}, {second.processes, second.steps}} {
		update := fixture.processes.update
		fixture.processes.update = func(ctx context.Context, p *domain.Process) error {
			claims.Add(1)

			return update(ctx, p)
		}
		read := fixture.steps.getByProcessID
		fixture.steps.getByProcessID = func(ctx context.Context, id uuid.UUID) ([]domain.ProcessStep, error) {
			if claims.Load() != 2 {
				t.Error("traversal began before all claims")
			}

			return read(ctx, id)
		}
	}
	before := time.Now().UTC()
	if err := batchExecutor(first, second).Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, next := range []time.Time{*first.acquired.Reliability.ExecutedAt, *second.acquired.Reliability.ExecutedAt} {
		if next.Before(before.Add(time.Minute)) {
			t.Fatal("lease shorter than one minute")
		}
	}
}

func TestExecuteProcessesUseCaseSkipsLostAcquisition(t *testing.T) {
	t.Parallel()
	storageErr := errors.New("acquisition unavailable")
	for _, tc := range []struct {
		name  string
		cause error
		want  error
	}{
		{"version conflict", errors.Join(domain.ErrProcessVersionConflict, outbox.ErrOutboxClaimLost), nil},
		{"already terminal", errors.Join(domain.ErrProcessTerminal, outbox.ErrOutboxClaimLost), nil},
		{"storage error", storageErr, storageErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bad := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
			good := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
			bad.processes.update = func(context.Context, *domain.Process) error { return tc.cause }
			if err := batchExecutor(bad, good).Execute(t.Context()); !errors.Is(err, tc.want) {
				t.Fatalf("acquisition error=%v want %v", err, tc.want)
			}
			if len(bad.queued) != 0 || len(good.queued) != 1 {
				t.Fatal("lost acquisition was processed or blocked the healthy process")
			}
		})
	}
}

func TestExecuteProcessesUseCaseAggregatesErrorsAndContinuesBatch(t *testing.T) {
	t.Parallel()
	acquireErr := errors.New("acquisition unavailable")
	readErr := errors.New("step read unavailable")
	saveErr := errors.New("step save unavailable")
	acquireFailure := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
	readFailure := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
	saveFailure := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
	good := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
	acquireFailure.processes.update = func(context.Context, *domain.Process) error { return acquireErr }
	readFailure.steps.getByProcessID = func(context.Context, uuid.UUID) ([]domain.ProcessStep, error) {
		return nil, readErr
	}
	saveFailure.steps.save = func(context.Context, *domain.ProcessStep) error { return saveErr }
	err := batchExecutor(acquireFailure, readFailure, saveFailure, good).Execute(t.Context())
	for _, failure := range []struct {
		fixture *executionFixture
		cause   error
	}{
		{acquireFailure, acquireErr}, {readFailure, readErr}, {saveFailure, saveErr},
	} {
		if !errors.Is(err, failure.cause) {
			t.Fatalf("batch omitted a cause: %v", err)
		}
		if len(failure.fixture.queued) != 0 {
			t.Fatal("failed process dispatched work")
		}
	}
	if len(good.queued) != 1 || good.queued[0].ProcessStepDefinitionName != testRoot {
		t.Fatal("errors stopped the healthy process from dispatching work")
	}
}

func TestExecuteProcessesUseCaseCancellationRetainsEarlierErrors(t *testing.T) {
	t.Parallel()
	first := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
	second := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cause := errors.New("acquisition unavailable")
	first.processes.update = func(context.Context, *domain.Process) error {
		cancel()

		return cause
	}
	err := batchExecutor(first, second).Execute(ctx)
	if !errors.Is(err, cause) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation discarded an earlier acquisition error: %v", err)
	}
	if len(first.queued) != 0 || len(second.queued) != 0 || len(second.updatedIDs) != 0 {
		t.Fatal("cancellation continued acquiring or dispatching processes")
	}
}

func TestExecuteProcessesUseCaseAcquisitionValidationFailureSkipsProcess(t *testing.T) {
	t.Parallel()
	bad := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
	good := newExecutionFixture(t, newTestDefinition(t, []string{testRoot}, nil), []string{testRoot})
	bad.pending.Version = 0
	if err := batchExecutor(bad, good).Execute(t.Context()); err == nil {
		t.Fatal("acquisition accepted an invalid process")
	}
	if len(bad.updatedIDs) != 0 || len(bad.queued) != 0 || len(good.queued) != 1 {
		t.Fatal("invalid acquisition was persisted or stopped the healthy process")
	}
}

func TestExecuteProcessesUseCaseReturnsDependencyErrors(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{
		testDefinitionStage, testStepReadStage, testStepSaveStage,
		testProgressStage, testCompletion, testFailureStage,
	} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			definition := newTestDefinition(t, []string{testRoot, "a"}, [][2]string{{testRoot, "a"}})
			fixture := newExecutionFixture(t, definition, []string{testRoot})
			cause := errors.New("dependency unavailable")
			switch stage {
			case testReloadStage:
				fixture.processes.getByID = func(context.Context,
					uuid.UUID) (*domain.Process,
					error) {
					return nil,
						cause
				}
			case testDefinitionStage:
				fixture.definitions.getByID = func(context.Context, uuid.UUID) (*domain.ProcessDefinition, error) {
					return nil, cause
				}
			case testStepReadStage:
				fixture.steps.getByProcessID = func(context.Context, uuid.UUID) ([]domain.ProcessStep, error) {
					return nil, cause
				}
			case testStepSaveStage:
				fixture.steps.save = func(context.Context, *domain.ProcessStep) error { return cause }
			case testProgressStage:
				fixture.stepStates[testRoot] = domain.ProcessStepStateCompleted
			case testCompletion:
				definition.Graph.Nodes[testRoot].Type = domain.ProcessStepTypeTerminal
			case testFailureStage:
				fixture.stepStates[testRoot] = domain.ProcessStepStateFailed
			}
			if stage == testProgressStage || stage == testCompletion || stage == testFailureStage {
				update := fixture.processes.update
				fixture.processes.update = func(ctx context.Context, model *domain.Process) error {
					if model.Version > 1 {
						return cause
					}

					return update(ctx, model)
				}
			}
			if err := fixture.executor().Execute(t.Context()); !errors.Is(err, cause) {
				t.Fatalf("%s error not returned: %v", stage, err)
			}
			if fixture.pending.Reliability.Executions != 1 || fixture.acquired.Reliability.Executions != 1 {
				t.Fatal("dependency error consumed extra executions")
			}
			if stage == testDefinitionStage && fixture.acquired.State != domain.ProcessStatePending {
				t.Fatal("temporary definition read failure failed the process")
			}
		})
	}
}

func TestExecuteProcessesUseCaseCancellationDuringTraversal(t *testing.T) {
	t.Parallel()
	fixture := newExecutionFixture(t, newTestDefinition(t, []string{"a", "b"}, nil), []string{"a", "b"})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	save := fixture.steps.save
	fixture.steps.save = func(ctx context.Context, step *domain.ProcessStep) error {
		err := save(ctx, step)
		cancel()

		return err
	}
	if err := fixture.executor().Execute(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	if len(fixture.queued) != 1 || len(fixture.updatedIDs) != 1 || !slices.Equal(fixture.acquired.CurrentSteps,
		[]string{"a",
			"b"}) {
		t.Fatal("cancellation advanced graph or dispatched more steps")
	}
}
