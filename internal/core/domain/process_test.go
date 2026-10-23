package domain_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"
)

func TestAgentStepRejectsInvalidExecutionParameters(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, key string
		value     *domain.ParameterValue
	}{
		{name: "missing prompt", key: testPromptParameter},
		{
			name:  "blank prompt",
			key:   testPromptParameter,
			value: &domain.ParameterValue{Type: domain.ParameterTypeString, Value: " \n"},
		},
		{
			name:  "wrong prompt type",
			key:   testPromptParameter,
			value: &domain.ParameterValue{Type: domain.ParameterTypeReference, Value: "PROCESS:prompt"},
		},
		{name: "missing schema", key: testOutputSchemaParameter},
		{
			name:  "wrong directory type",
			key:   testWorkingDirectoryParameter,
			value: &domain.ParameterValue{Type: domain.ParameterTypeString, Value: t.TempDir()},
		},
		{
			name:  "file as directory",
			key:   testWorkingDirectoryParameter,
			value: &domain.ParameterValue{Type: domain.ParameterTypeFilePath, Value: file},
		},
		{name: "blank model", key: "model", value: &domain.ParameterValue{Type: domain.ParameterTypeString, Value: ""}},
		{
			name:  "wrong reasoning type",
			key:   "reasoning_effort",
			value: &domain.ParameterValue{Type: domain.ParameterTypeReference, Value: "PROCESS:effort"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			values := map[string]domain.ParameterValue{
				testPromptParameter:           {Type: domain.ParameterTypeString, Value: "Plan a solution"},
				testWorkingDirectoryParameter: {Type: domain.ParameterTypeFilePath, Value: t.TempDir()},
				testOutputSchemaParameter:     {Type: domain.ParameterTypeJSONSchema, Value: `{"type":"object"}`},
			}
			delete(values, tc.key)
			if tc.value != nil {
				values[tc.key] = domain.ParameterValue{Type: tc.value.Type, Value: tc.value.Value}
			}
			_, err := domain.NewProcessStep(
				domain.WithNewProcessStepID(), domain.WithProcessStepProcessID(uuid.New()),
				domain.WithProcessStepState(domain.ProcessStepStatePending),
				domain.WithProcessStepProcessStepDefinitionName("agent"),
				domain.WithProcessStepType(domain.ProcessStepTypeAICodingAgent),
				domain.WithProcessStepParams(testParameters(values)),
			)
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("invalid %q accepted: %v", tc.key, err)
			}
		})
	}
}

func TestAgentStepAcceptsMissingWorkingDirectory(t *testing.T) {
	t.Parallel()
	workdir := filepath.Join(t.TempDir(), "missing", "project")
	_, err := domain.NewProcessStep(
		domain.WithNewProcessStepID(), domain.WithProcessStepProcessID(uuid.New()),
		domain.WithProcessStepState(domain.ProcessStepStatePending),
		domain.WithProcessStepProcessStepDefinitionName("agent"),
		domain.WithProcessStepType(domain.ProcessStepTypeAICodingAgent),
		domain.WithProcessStepParams(testParameters(map[string]domain.ParameterValue{
			testPromptParameter:           {Type: domain.ParameterTypeString, Value: "Plan a solution"},
			testWorkingDirectoryParameter: {Type: domain.ParameterTypeFilePath, Value: workdir},
			testOutputSchemaParameter:     {Type: domain.ParameterTypeJSONSchema, Value: `{"type":"object"}`},
		})),
	)
	if err != nil {
		t.Fatalf("step creation rejected a directory the worker can create: %v", err)
	}
	if _, err := os.Stat(workdir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("step creation changed the filesystem: %v", err)
	}
}

const (
	testFeature              = "feature"
	testUnknown              = "unknown"
	testError                = "error"
	testMessage              = "message"
	testOutput               = "output"
	testBuildStep            = "build"
	testTrackerDescription   = "Build a tracker"
	testPayloadParameter     = "payload"
	testDescriptionParameter = "description"
)

func TestNewProcess(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	want := &domain.Process{
		ID:                  uuid.New(),
		State:               domain.ProcessStatePending,
		ProcessDefinitionID: uuid.New(),
		Context: &domain.ProcessContext{Inputs: testParameters(map[string]domain.ParameterValue{
			testFeature: {Type: domain.ParameterTypeString, Value: "Search"},
		})},
		Result: testParameters(map[string]domain.ParameterValue{
			testOutput: {Type: domain.ParameterTypeString, Value: "Built"},
		}),
		Version:     2,
		CreatedAt:   now,
		UpdatedAt:   now,
		Reliability: domain.ProcessReliability{Executions: 1, ExecutionTime: time.Second, ExecutedAt: &now},
	}
	opts := []domain.ProcessOpt{
		domain.WithProcessID(want.ID),
		domain.WithProcessState(want.State),
		domain.WithProcessProcessDefinitionID(want.ProcessDefinitionID),
		domain.WithProcessContext(want.Context),
		domain.WithProcessResult(want.Result),
		domain.WithProcessReliability(want.Reliability),
		domain.WithProcessVersion(want.Version),
		domain.WithProcessCreatedAt(want.CreatedAt),
		domain.WithProcessUpdatedAt(want.UpdatedAt),
	}
	got, err := domain.NewProcess(opts...)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("creation = %+v, %v; want %+v", got, err, want)
	}
	if got, err := domain.NewProcess(); err == nil || got != nil {
		t.Fatal("accepted an empty process")
	}
	for _, tc := range []struct {
		name   string
		option domain.ProcessOpt
	}{
		{"zero ID", domain.WithProcessID(uuid.Nil())},
		{"zero definition ID", domain.WithProcessProcessDefinitionID(uuid.Nil())},
		{"unknown state", domain.WithProcessState("UNKNOWN")},
		{"zero version", domain.WithProcessVersion(0)},
		{"negative executions", domain.WithProcessReliability(domain.ProcessReliability{Executions: -1})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NewProcess(append(opts, tc.option)...)
			if err == nil || got != nil {
				t.Fatalf("accepted invalid process: %+v, %v", got, err)
			}
		})
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var restored domain.Process
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&restored, want) {
		t.Fatal("JSON round trip changed the process")
	}
	for _, state := range []domain.ProcessState{
		domain.ProcessStateInProgress,
		domain.ProcessStateCompleted,
		domain.ProcessStateFailed,
	} {
		model, err := domain.NewProcess(append(opts, domain.WithProcessState(state))...)
		if err != nil || model.State != state {
			t.Fatalf("state %s: %+v, %v", state, model, err)
		}
	}
	model, err := domain.NewProcess(append(opts, domain.WithProcessUpdatedAt(now.Add(-time.Second)))...)
	if err == nil || model != nil {
		t.Fatal("accepted invalid process chronology")
	}
	model, err = domain.NewProcess(append(opts, domain.WithProcessCreatedAt(time.Time{}))...)
	if err == nil || model != nil {
		t.Fatal("accepted updated_at without created_at")
	}
}

func validStepOptions(extra ...domain.ProcessStepOpt) []domain.ProcessStepOpt {
	return append([]domain.ProcessStepOpt{
		domain.WithNewProcessStepID(),
		domain.WithProcessStepState(domain.ProcessStepStatePending),
		domain.WithProcessStepProcessID(uuid.New()),
		domain.WithProcessStepProcessStepDefinitionName(testBuildStep),
		domain.WithProcessStepType("custom.worker.v2"),
	}, extra...)
}

func TestNewProcessStep(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	want := &domain.ProcessStep{
		Version: 1, CreatedAt: now, UpdatedAt: now,
		ID:                        uuid.New(),
		State:                     domain.ProcessStepStatePending,
		ProcessID:                 uuid.New(),
		ProcessStepDefinitionName: testBuildStep,
		Type:                      "custom.worker.v2",
		Params: testParameters(map[string]domain.ParameterValue{
			"worker_setting": {Type: domain.ParameterTypeReference, Value: "PROCESS:feature"},
		}),
		Result: testParameters(map[string]domain.ParameterValue{
			testOutput: {Type: domain.ParameterTypeString, Value: "Done"},
		}),
		Reliability: domain.ProcessReliability{Executions: 2, ExecutionTime: time.Second},
	}
	opts := []domain.ProcessStepOpt{
		domain.WithProcessStepVersion(want.Version),
		domain.WithProcessStepCreatedAt(want.CreatedAt),
		domain.WithProcessStepUpdatedAt(want.UpdatedAt),

		domain.WithProcessStepID(want.ID),
		domain.WithProcessStepState(want.State),
		domain.WithProcessStepProcessID(want.ProcessID),
		domain.WithProcessStepProcessStepDefinitionName(want.ProcessStepDefinitionName),
		domain.WithProcessStepType(want.Type),
		domain.WithProcessStepParams(want.Params),
		domain.WithProcessStepResult(want.Result),
		domain.WithProcessStepReliability(want.Reliability),
	}
	got, err := domain.NewProcessStep(opts...)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("creation = %+v, %v; want %+v", got, err, want)
	}
	if got, err := domain.NewProcessStep(); err == nil || got != nil {
		t.Fatal("accepted an empty process step")
	}
	for _, tc := range []struct {
		name   string
		option domain.ProcessStepOpt
	}{
		{"zero ID", domain.WithProcessStepID(uuid.Nil())},
		{"zero process ID", domain.WithProcessStepProcessID(uuid.Nil())},
		{"empty definition name", domain.WithProcessStepProcessStepDefinitionName("")},
		{"blank definition name", domain.WithProcessStepProcessStepDefinitionName(" ")},
		{"invalid definition name", domain.WithProcessStepProcessStepDefinitionName(string([]byte{0xff}))},
		{"unknown state", domain.WithProcessStepState("EXECUTING")},
		{"missing type", domain.WithProcessStepType("")},
		{"blank type", domain.WithProcessStepType(" ")},
		{"invalid type text", domain.WithProcessStepType(domain.ProcessStepType(string([]byte{0xff})))},
		{"negative duration", domain.WithProcessStepReliability(domain.ProcessReliability{ExecutionTime: -1})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := domain.NewProcessStep(append(opts, tc.option)...)
			if err == nil || got != nil {
				t.Fatalf("accepted invalid process step: %+v, %v", got, err)
			}
		})
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var restored domain.ProcessStep
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&restored, want) {
		t.Fatal("JSON round trip changed the process step")
	}
	for _, state := range []domain.ProcessStepState{domain.ProcessStepStateCompleted, domain.ProcessStepStateFailed} {
		model, err := domain.NewProcessStep(append(opts, domain.WithProcessStepState(state))...)
		if err != nil || model.State != state {
			t.Fatalf("state %s: %+v, %v", state, model, err)
		}
	}
	if _, err := domain.NewProcessStep(validStepOptions()...); err != nil {
		t.Fatal("worker without parameters rejected:", err)
	}
}

func TestFailuresRequireResults(t *testing.T) {
	t.Parallel()
	for _, result := range []*domain.Parameters{
		nil,
		{},
		testParameters(map[string]domain.ParameterValue{
			testError: {Type: domain.ParameterTypeString, Value: "worker failed"},
		}),
	} {
		process, processErr := domain.NewProcess(
			domain.WithNewProcessID(),
			domain.WithProcessState(domain.ProcessStateFailed),
			domain.WithProcessProcessDefinitionID(uuid.New()),
			domain.WithProcessVersion(1),
			domain.WithProcessResult(result),
		)
		step, stepErr := domain.NewProcessStep(
			validStepOptions(
				domain.WithProcessStepState(domain.ProcessStepStateFailed),
				domain.WithProcessStepResult(result),
			)...,
		)
		if result.Len() == 0 {
			if process != nil ||
				step != nil ||
				!errors.Is(processErr, domain.ErrNoDetailsInResult) ||
				!errors.Is(stepErr, domain.ErrNoDetailsInResult) {
				t.Fatalf("missing failure result accepted: %v, %v", processErr, stepErr)
			}
		} else if processErr != nil || stepErr != nil {
			t.Fatalf("valid failure result rejected: %v, %v", processErr, stepErr)
		}
	}
}

func TestProcessReliability(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	model, err := domain.NewProcessReliability(
		domain.WithProcessReliabilityExecutions(2),
		domain.WithProcessReliabilityExecutionTime(time.Minute),
		domain.WithProcessReliabilityExecutedAt(now),
	)
	if err != nil ||
		model.Executions != 2 ||
		model.ExecutionTime != time.Minute ||
		model.ExecutedAt == nil ||
		!model.ExecutedAt.Equal(now) {
		t.Fatalf("reliability: %+v, %v", model, err)
	}
	for _, reliability := range []domain.ProcessReliability{
		{Executions: -1},
		{ExecutionTime: -1},
	} {
		got, err := domain.NewProcessReliability(
			domain.WithProcessReliabilityExecutions(reliability.Executions),
			domain.WithProcessReliabilityExecutionTime(reliability.ExecutionTime),
		)
		if err == nil || got != nil {
			t.Fatalf("accepted invalid reliability: %+v, %v", got, err)
		}
		step, err := domain.NewProcessStep(validStepOptions(domain.WithProcessStepReliability(reliability))...)
		if err == nil || step != nil {
			t.Fatal("step accepted invalid reliability")
		}
	}
}

func TestProcessValidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		change  domain.ProcessOpt
		valid   bool
		wantErr error
	}{
		{name: "valid existing process", valid: true},
		{name: "missing ID", change: domain.WithProcessID(uuid.Nil())},
		{name: "invalid version", change: domain.WithProcessVersion(0)},
		{
			name:   "invalid nested reliability",
			change: domain.WithProcessReliability(domain.ProcessReliability{Executions: -1}),
		},
		{
			name:    "failed without details",
			change:  domain.WithProcessState(domain.ProcessStateFailed),
			wantErr: domain.ErrNoDetailsInResult,
		},
		{name: "failed with details", change: func(p *domain.Process) {
			p.State = domain.ProcessStateFailed
			p.Result = testParameters(map[string]domain.ParameterValue{
				testError: {Type: domain.ParameterTypeString, Value: "worker failed"},
			})
		}, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := &domain.Process{
				ID: uuid.New(), State: domain.ProcessStatePending,
				ProcessDefinitionID: uuid.New(), Version: 1,
			}
			if tc.change != nil {
				tc.change(p)
			}
			err := p.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("Validate() = %v, want valid=%v", err, tc.valid)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("Validate() = %v, want %v", err, tc.wantErr)
			}
		})
	}
	var missing *domain.Process
	if err := missing.Validate(); err == nil {
		t.Fatal("nil process accepted")
	}
}

func TestProcessCompleteAndFail(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		transition func(*domain.Process, *domain.Parameters) error
		wantState  domain.ProcessState
	}{
		{"complete", (*domain.Process).Complete, domain.ProcessStateCompleted},
		{"fail", (*domain.Process).Fail, domain.ProcessStateFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, initialState := range []domain.ProcessState{domain.ProcessStatePending,
				domain.ProcessStateInProgress} {
				next := time.Now().UTC().Add(time.Minute)
				for _, executedAt := range []*time.Time{nil, &next} {
					p := domain.Process{
						ID:                  uuid.New(),
						ProcessDefinitionID: uuid.New(),
						State:               initialState,
						Version:             1,
						CreatedAt:           time.Now().UTC().Add(-time.Hour),
						Reliability:         domain.ProcessReliability{ExecutedAt: executedAt},
					}
					result := testParameters(map[string]domain.ParameterValue{testMessage: {Type: domain.ParameterTypeString,
						Value: "result details"}})
					before := time.Now().UTC()
					if err := tc.transition(&p, result); err != nil {
						t.Fatal(err)
					}
					after := time.Now().UTC()
					if p.State != tc.wantState || !p.IsFinalState() || !reflect.DeepEqual(p.Result, result) {
						t.Fatalf("transition: %+v", p)
					}
					if p.UpdatedAt.Before(before) || p.UpdatedAt.After(after) {
						t.Fatalf("update time: %v", p.UpdatedAt)
					}
					if !reflect.DeepEqual(p.Reliability.ExecutedAt, executedAt) {
						t.Fatal("completion changed the scheduled execution time")
					}
					if err := p.Validate(); err != nil {
						t.Fatalf("transition left an invalid process: %v", err)
					}
					if p.Reliability.ExecutionTime != p.UpdatedAt.Sub(p.CreatedAt) {
						t.Fatal("incorrect execution duration")
					}
				}
			}
		})
	}
}

func TestProcessTransitionsRejectFinalState(t *testing.T) {
	t.Parallel()
	for _, state := range []domain.ProcessState{domain.ProcessStateCompleted, domain.ProcessStateFailed} {
		for _, transition := range []func(*domain.Process, *domain.Parameters) error{
			(*domain.Process).Complete,
			(*domain.Process).Fail,
		} {
			p := domain.Process{
				ID:                  uuid.New(),
				ProcessDefinitionID: uuid.New(),
				State:               state,
				Version:             1,
				Result: testParameters(map[string]domain.ParameterValue{testMessage: {Type: domain.ParameterTypeString,
					Value: "original"}}),
			}
			result := testParameters(map[string]domain.ParameterValue{
				testMessage: {Type: domain.ParameterTypeString, Value: "replacement"},
			})
			if err := transition(&p, result); !errors.Is(err, domain.ErrProcessTerminal) {
				t.Fatalf("final state accepted: %v", err)
			}
			if p.State != state || p.Result.Get(testMessage).Value != "original" {
				t.Fatal("rejected transition changed the terminal result")
			}
		}
	}
}

func TestProcessTransitionsReturnValidationErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		transition func(*domain.Process, *domain.Parameters) error
		result     *domain.Parameters
		wantErr    error
	}{
		{"fail missing details", (*domain.Process).Fail, nil, domain.ErrNoDetailsInResult},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := domain.Process{
				ID:                  uuid.New(),
				ProcessDefinitionID: uuid.New(),
				State:               domain.ProcessStatePending,
				Version:             1,
				CreatedAt:           time.Now().UTC().Add(-time.Hour),
			}
			err := tc.transition(&p, tc.result)
			if err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("invalid result: %v", err)
			}
			if !p.IsFinalState() || !reflect.DeepEqual(p.Result, tc.result) {
				t.Fatal("validation failure rolled back the mutation")
			}
		})
	}
}

func TestProcessCurrentSteps(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Add(-time.Hour)
	p, err := domain.NewProcess(
		domain.WithNewProcessID(),
		domain.WithProcessProcessDefinitionID(uuid.New()), domain.WithProcessState(domain.ProcessStatePending),
		domain.WithProcessVersion(1), domain.WithProcessCreatedAt(now), domain.WithProcessUpdatedAt(now),
		domain.WithProcessCurrentSteps([]string{"start"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.CurrentSteps, []string{"start"}) {
		t.Fatalf("initial steps: %v", p.CurrentSteps)
	}
	before := time.Now().UTC()
	if err := p.UpdateCurrentSteps([]string{"left", "right"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.CurrentSteps, []string{"left", "right"}) ||
		p.UpdatedAt.Before(before) ||
		p.UpdatedAt.After(time.Now().UTC()) {
		t.Fatalf("updated steps: %+v", p)
	}
	for _, invalid := range []string{"", " ", string([]byte{0xff})} {
		invalidProcess := &domain.Process{
			ID: uuid.New(), ProcessDefinitionID: uuid.New(),
			State: domain.ProcessStatePending, Version: 1, CreatedAt: now,
		}
		if err := invalidProcess.UpdateCurrentSteps([]string{invalid}); err == nil {
			t.Fatalf("invalid current step accepted: %q", invalid)
		}
	}
	if err := p.UpdateCurrentSteps(nil); err != nil {
		t.Fatalf("empty current steps rejected: %v", err)
	}
}

func TestProcessAcquireCountsExecutions(t *testing.T) {
	t.Parallel()
	p := &domain.Process{
		ID:                  uuid.New(),
		ProcessDefinitionID: uuid.New(),
		State:               domain.ProcessStatePending,
		Version:             1,
		CreatedAt:           time.Now().UTC().Add(-time.Hour),
	}
	for executions := 1; executions <= 2; executions++ {
		before := time.Now().UTC()
		if err := p.Acquire(time.Minute); err != nil {
			t.Fatal(err)
		}
		after := time.Now().UTC()
		next := p.Reliability.ExecutedAt
		if p.Reliability.Executions != executions ||
			next == nil ||
			next.Before(before.Add(time.Minute)) ||
			next.After(after.Add(time.Minute)) {
			t.Fatalf("acquisition: %+v", p)
		}
	}
}

func TestProcessStepOutboxLifecycle(t *testing.T) {
	t.Parallel()
	step, err := domain.NewProcessStep(validStepOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	record := step
	before := time.Now().UTC()
	if err := record.Acquire(time.Minute); err != nil {
		t.Fatal(err)
	}
	if step.Version != 1 ||
		step.CreatedAt.IsZero() ||
		step.Reliability.Executions != 1 ||
		step.Reliability.ExecutedAt.Before(before.Add(time.Minute)) {
		t.Fatalf("acquired: %+v", step)
	}
	result := testParameters(map[string]domain.ParameterValue{"result": {Type: domain.ParameterTypeString, Value: "ok"}})
	if err := record.Complete(result); err != nil {
		t.Fatal(err)
	}
	if !record.IsFinalState() || step.State != domain.ProcessStepStateCompleted || step.Reliability.ExecutionTime < 0 {
		t.Fatalf("completed: %+v", step)
	}
	if err := record.Acquire(time.Minute); !errors.Is(err, domain.ErrProcessStepTerminal) {
		t.Fatalf("terminal acquisition: %v", err)
	}
	if err := record.Fail(result); !errors.Is(err, domain.ErrProcessStepTerminal) {
		t.Fatalf("terminal failure: %v", err)
	}
	if err := record.Retry(result); !errors.Is(err, domain.ErrProcessStepTerminal) {
		t.Fatalf("terminal retry: %v", err)
	}
	step, err = domain.NewProcessStep(validStepOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	if err := step.Fail(testParameters(map[string]domain.ParameterValue{
		"error": {Type: domain.ParameterTypeString, Value: "failed"},
	})); err != nil {
		t.Fatal(err)
	}
	if !step.IsFinalState() || step.State != domain.ProcessStepStateFailed {
		t.Fatal("failure did not mutate the step")
	}
}

func testParameters(values map[string]domain.ParameterValue) *domain.Parameters {
	params, err := domain.NewParameters(values)
	if err != nil {
		panic(err)
	}

	return params
}

func TestAgentStepValidatesParametersAndOutput(t *testing.T) {
	t.Parallel()
	params := testParameters(map[string]domain.ParameterValue{
		testPromptParameter:           {Type: domain.ParameterTypeString, Value: "Brainstorm a solution"},
		testWorkingDirectoryParameter: {Type: domain.ParameterTypeFilePath, Value: t.TempDir()},
		testOutputSchemaParameter: {Type: domain.ParameterTypeJSONSchema, Value: `{
  "type": "object",
  "properties": {
    "path_to_plan": {
      "type": "string"
    }
  },
  "required": [
    "path_to_plan"
  ],
  "additionalProperties": false
}`},
	})
	create := func() (*domain.ProcessStep, error) {
		return domain.NewProcessStep(domain.WithNewProcessStepID(), domain.WithProcessStepProcessID(uuid.New()),
			domain.WithProcessStepProcessStepDefinitionName("brainstorm"),
			domain.WithProcessStepState(domain.ProcessStepStatePending),
			domain.WithProcessStepType("AI_CODING_AGENT"), domain.WithProcessStepParams(params))
	}
	step, err := create()
	if err != nil {
		t.Fatal(err)
	}
	invalid := testParameters(map[string]domain.ParameterValue{
		"output": {Type: domain.ParameterTypeJSON, Value: `{"summary":"No plan path"}`},
	})
	if err := step.Complete(invalid); err == nil || step.State != domain.ProcessStepStatePending {
		t.Fatal("schema-invalid output marked the agent step complete")
	}
	valid := testParameters(map[string]domain.ParameterValue{
		"output": {Type: domain.ParameterTypeJSON, Value: `{"path_to_plan":"/project/docs/plans/plan.md"}`},
	})
	if err := step.Complete(valid); err != nil || step.State != domain.ProcessStepStateCompleted {
		t.Fatalf("valid output rejected: %v", err)
	}
	params.Delete(testPromptParameter)
	if _, err := create(); err == nil {
		t.Fatal("agent step created without a prompt")
	}
}

const (
	testPromptParameter           = "prompt"
	testOutputSchemaParameter     = "output_json"
	testWorkingDirectoryParameter = "working_directory"
)

func TestProcessStepResolveExplicitSources(t *testing.T) {
	t.Parallel()
	declarations := testParameters(map[string]domain.ParameterValue{
		testDescriptionParameter: {Type: domain.ParameterTypeString, From: "inputs.description"},
		"plan":                   {Type: domain.ParameterTypeFilePath, From: "steps.brainstorm.output.path_to_plan"},
	})
	inputs := testParameters(map[string]domain.ParameterValue{
		testDescriptionParameter: {Type: domain.ParameterTypeString, Value: testTrackerDescription},
	})
	context := &domain.ProcessContext{Inputs: inputs, StepOutputs: map[string]*domain.Parameters{
		"brainstorm": testParameters(map[string]domain.ParameterValue{
			testOutput: {Type: domain.ParameterTypeJSON, Value: `{"path_to_plan":"/project/docs/plans/solution-plan.md"}`},
		}),
	}}
	step := &domain.ProcessStep{Params: declarations, Context: declarations}
	if err := step.Resolve(context); err != nil {
		t.Fatal(err)
	}
	if step.Params.String(testDescriptionParameter) != testTrackerDescription ||
		step.Params.String("plan") != "/project/docs/plans/solution-plan.md" ||
		step.Context.String(testDescriptionParameter) != testTrackerDescription ||
		step.Context.String("plan") != "/project/docs/plans/solution-plan.md" {
		t.Fatalf("incorrect resolved values: %+v", step)
	}
	unready := &domain.ProcessStep{Context: declarations}
	if err := unready.Resolve(&domain.ProcessContext{Inputs: inputs}); err == nil {
		t.Fatal("read an output absent from the process context")
	}
	missing := &domain.ProcessStep{Params: declarations}
	if err := missing.Resolve(nil); err == nil {
		t.Fatal("missing explicit source accepted")
	}
}

func TestProcessRecordsCompletedOutputsInContext(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	inputs := testParameters(map[string]domain.ParameterValue{
		testDescriptionParameter: {Type: domain.ParameterTypeString, Value: testTrackerDescription},
	})
	model, err := domain.NewProcess(
		domain.WithNewProcessID(), domain.WithProcessProcessDefinitionID(uuid.New()),
		domain.WithProcessState(domain.ProcessStatePending), domain.WithProcessVersion(1),
		domain.WithProcessCreatedAt(now), domain.WithProcessUpdatedAt(now),
		domain.WithProcessContext(&domain.ProcessContext{Inputs: inputs}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result := testParameters(map[string]domain.ParameterValue{
		testOutput: {Type: domain.ParameterTypeJSON, Value: `{"summary":"Implemented"}`},
	})
	steps := []domain.ProcessStep{
		{ProcessStepDefinitionName: testBuildStep, State: domain.ProcessStepStateCompleted, Result: result},
		{ProcessStepDefinitionName: "waiting", State: domain.ProcessStepStatePending, Result: result},
		{ProcessStepDefinitionName: "failed", State: domain.ProcessStepStateFailed, Result: result},
	}
	changed, err := model.RecordStepOutputs(steps)
	if err != nil || !changed || len(model.Context.StepOutputs) != 1 ||
		model.Context.StepOutputs[testBuildStep] != result || model.Context.Inputs != inputs {
		t.Fatalf("recorded outputs: %+v, changed=%t, error=%v", model.Context, changed, err)
	}
	changed, err = model.RecordStepOutputs(steps)
	if err != nil || changed {
		t.Fatalf("unchanged outputs requested another write: %t, %v", changed, err)
	}
}

func TestProcessStepResolveJSONOutputWithoutChangingValues(t *testing.T) {
	t.Parallel()
	for _, payload := range []string{
		`"hello"`, `"123"`, `null`, `9007199254740993`, `123456789012345678901234567890`,
		`{"number":9007199254740993,"text":"123"}`, `["hello",null,9007199254740993]`,
	} {
		t.Run(payload, func(t *testing.T) {
			t.Parallel()
			step := &domain.ProcessStep{Context: testParameters(map[string]domain.ParameterValue{
				testPayloadParameter: {Type: domain.ParameterTypeJSON, From: "steps.build.output.payload"},
			})}
			context := &domain.ProcessContext{StepOutputs: map[string]*domain.Parameters{
				testBuildStep: testParameters(map[string]domain.ParameterValue{
					testOutput: {Type: domain.ParameterTypeJSON, Value: `{"payload":` + payload + `}`},
				}),
			}}
			if err := step.Resolve(context); err != nil {
				t.Fatal(err)
			}
			if got := step.Context.String(testPayloadParameter); got != payload {
				t.Fatalf("output changed from %s to %s", payload, got)
			}
		})
	}
}

func TestProcessStepResolveTextRejectsNonStringOutputs(t *testing.T) {
	t.Parallel()
	for _, payload := range []string{`null`, `123`, `true`, `{}`} {
		step := &domain.ProcessStep{Params: testParameters(map[string]domain.ParameterValue{
			testPayloadParameter: {Type: domain.ParameterTypeString, From: "steps.build.output.payload"},
		})}
		context := &domain.ProcessContext{StepOutputs: map[string]*domain.Parameters{
			testBuildStep: testParameters(map[string]domain.ParameterValue{
				testOutput: {Type: domain.ParameterTypeJSON, Value: `{"payload":` + payload + `}`},
			}),
		}}
		if err := step.Resolve(context); err == nil {
			t.Fatalf("accepted %s as text", payload)
		}
	}
}

func TestProcessStepResolveRejectsUnsupportedSources(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"build.output.payload", "steps.build.output."} {
		step := &domain.ProcessStep{Context: testParameters(map[string]domain.ParameterValue{
			testPayloadParameter: {Type: domain.ParameterTypeString, From: source},
		})}
		context := &domain.ProcessContext{StepOutputs: map[string]*domain.Parameters{
			testBuildStep: testParameters(map[string]domain.ParameterValue{
				testOutput: {Type: domain.ParameterTypeJSON, Value: `{"":"empty","payload":"text"}`},
			}),
		}}
		if err := step.Resolve(context); err == nil {
			t.Fatalf("resolved unsupported expression %q", source)
		}
	}
}
