package domain

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
	"uuid"
)

func TestProcessDefinition(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.October, 6, 10, 0, 0, 123456789, time.UTC)
	reliability, err := NewProcessDefinitionReliability(
		WithProcessDefinitionReliabilityTimeout(time.Minute),
		WithProcessDefinitionReliabilityMaxRetries(2),
		WithProcessDefinitionReliabilityRetryDelay(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	step, err := NewProcessStepDefinition(
		WithProcessStepDefinitionName("Build"),
		WithProcessStepDefinitionType(ProcessStepType("custom.worker.v2")),
		WithProcessStepDefinitionReliability(*reliability),
		WithProcessStepDefinitionParams(testParameters(map[string]ParameterValue{
			testCommand: {Type: ParameterTypeString, Required: true},
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	graph := NewProcessGraph()
	if err := graph.AddNode(step); err != nil {
		t.Fatal(err)
	}
	model, err := NewProcessDefinition(
		WithNewProcessDefinitionID(),
		WithProcessDefinitionName("Build"),
		WithProcessDefinitionDescription("Build a project"),
		WithProcessDefinitionState(ProcessDefinitionStateDraft),
		WithProcessDefinitionVersion(1),
		WithProcessDefinitionGraph(*graph),
		WithProcessDefinitionReliability(*reliability),
		WithProcessDefinitionInputs(testParameters(map[string]ParameterValue{
			"feature": {Type: ParameterTypeString, Required: true},
		})),
		WithProcessDefinitionCreatedAt(createdAt),
		WithProcessDefinitionUpdatedAt(createdAt.Add(time.Minute)),
	)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["created_at"] == nil || fields["updated_at"] == nil {
		t.Fatal("timestamp JSON field names are missing")
	}
	var restored ProcessDefinition
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(model, &restored) {
		t.Fatal("definition changed after JSON round trip")
	}
	for _, option := range []ProcessDefinitionOpt{
		WithProcessDefinitionID(uuid.Nil()),
		WithProcessDefinitionState("UNKNOWN"),
		WithProcessDefinitionVersion(0),
		WithProcessDefinitionName(string([]byte{0xff})),
		WithProcessDefinitionDescription(string([]byte{0xff})),
		WithProcessDefinitionReliability(ProcessDefinitionReliability{Timeout: -1}),
		WithProcessDefinitionGraph(ProcessGraph{Nodes: map[string]*ProcessStepDefinition{testBuild: nil}}),
	} {
		got, err := NewProcessDefinition(
			WithNewProcessDefinitionID(),
			WithProcessDefinitionState(ProcessDefinitionStateDraft),
			WithProcessDefinitionVersion(1),
			WithProcessDefinitionCreatedAt(createdAt),
			WithProcessDefinitionUpdatedAt(createdAt),
			option,
		)
		if err == nil || got != nil {
			t.Fatalf("accepted invalid definition: %+v, %v", got, err)
		}
	}
	generate := WithNewProcessDefinitionID()
	first, second := new(ProcessDefinition), new(ProcessDefinition)
	generate(first)
	generate(second)
	if first.ID == uuid.Nil() || first.ID == second.ID {
		t.Fatal("ID option reused a UUID")
	}
}

func TestProcessStepDefinitionCreatesAgentWithResolvedContext(t *testing.T) {
	t.Parallel()
	definition := &ProcessStepDefinition{
		Name: "implement", Type: "AI_CODING_AGENT",
		Params: testParameters(map[string]ParameterValue{
			"working_directory": {Type: ParameterTypeFilePath, Required: true},
			"prompt":            {Type: ParameterTypeString, Value: "Implement the complete plan from context."},
			"output_json":       {Type: ParameterTypeJSONSchema, Value: `{"type":"object"}`},
		}),
		Context: testParameters(map[string]ParameterValue{
			testContextDescription: {Type: ParameterTypeString, From: "inputs.description"},
			"path_to_plan":         {Type: ParameterTypeFilePath, From: "steps.brainstorm.output.path_to_plan"},
		}),
	}
	parent := &Process{ID: uuid.New(), Context: &ProcessContext{Inputs: testParameters(map[string]ParameterValue{
		"working_directory":    {Type: ParameterTypeFilePath, Value: t.TempDir()},
		testContextDescription: {Type: ParameterTypeString, Value: testTrackerFeature},
	})}}
	previous := []ProcessStep{{ProcessStepDefinitionName: testBrainstormStep, State: ProcessStepStateCompleted,
		Result: testParameters(map[string]ParameterValue{
			testOutputField: {Type: ParameterTypeJSON, Value: "{\"summary\":\"Short summary\"," +
				"\"path_to_plan\":\"/project/docs/plans/solution-" +
				"plan-20261019.md\"}"},
		})}}
	context, err := NewProcessContext(parent.Context.Inputs, map[string]*Parameters{
		testBrainstormStep: previous[0].Result,
	})
	if err != nil {
		t.Fatal(err)
	}
	step, err := definition.CreateStep(parent.ID, context)
	if err != nil {
		t.Fatal(err)
	}
	if step.ProcessID != parent.ID || step.Context.String(testContextDescription) != testTrackerFeature ||
		step.Context.String("path_to_plan") != "/project/docs/plans/solution-plan-20261019.md" ||
		step.Params.String("prompt") != "Implement the complete plan from context." {
		t.Fatalf("incorrect materialized step: %+v", step)
	}
	if _, err := definition.CreateStep(parent.ID, &ProcessContext{Inputs: parent.Context.Inputs}); err == nil {
		t.Fatal("created step without its referenced plan output")
	}
}

func TestProcessDefinitionTimestamps(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.October, 6, 10, 0, 0, 123456789, time.UTC)
	for _, tc := range []struct {
		name      string
		createdAt time.Time
		updatedAt time.Time
		valid     bool
	}{
		{name: "missing both"},
		{name: "missing creation", updatedAt: createdAt},
		{name: "missing update", createdAt: createdAt},
		{name: "update before creation", createdAt: createdAt, updatedAt: createdAt.Add(-time.Nanosecond)},
		{name: "equal timestamps", createdAt: createdAt, updatedAt: createdAt, valid: true},
		{name: "later update", createdAt: createdAt, updatedAt: createdAt.Add(time.Nanosecond), valid: true},
		{
			name:      "equal instants in different zones",
			createdAt: createdAt,
			updatedAt: createdAt.In(time.FixedZone("+02:00", 2*60*60)),
			valid:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			model, err := NewProcessDefinition(
				WithNewProcessDefinitionID(),
				WithProcessDefinitionState(ProcessDefinitionStateDraft),
				WithProcessDefinitionVersion(1),
				WithProcessDefinitionCreatedAt(tc.createdAt),
				WithProcessDefinitionUpdatedAt(tc.updatedAt),
			)
			if tc.valid {
				if err != nil || model == nil || !model.CreatedAt.Equal(tc.createdAt) || !model.UpdatedAt.Equal(tc.updatedAt) {
					t.Fatalf("valid timestamps = %+v, %v", model, err)
				}
			} else if err == nil || model != nil {
				t.Fatalf("accepted invalid timestamps: %+v, %v", model, err)
			}
		})
	}
}

func TestProcessStepDefinitionValidation(t *testing.T) {
	t.Parallel()
	for _, option := range []ProcessStepDefinitionOpt{
		WithProcessStepDefinitionName(""),
		WithProcessStepDefinitionName(" "),
		WithProcessStepDefinitionName(string([]byte{0xff})),
		WithProcessStepDefinitionType(" "),
		WithProcessStepDefinitionReliability(ProcessDefinitionReliability{RetryDelay: -1}),
		WithProcessStepDefinitionReliability(ProcessDefinitionReliability{MaxRetries: -1}),
		WithProcessStepDefinitionReliability(ProcessDefinitionReliability{RetryTimeout: -1}),
	} {
		got, err := NewProcessStepDefinition(WithProcessStepDefinitionName(testBuild), option)
		if err == nil || got != nil {
			t.Fatalf("accepted invalid step: %+v, %v", got, err)
		}
	}
	for _, option := range []ProcessDefinitionReliabilityOpt{
		WithProcessDefinitionReliabilityTimeout(-1),
		WithProcessDefinitionReliabilityTimeout(30*24*time.Hour + 1),
		WithProcessDefinitionReliabilityMaxRetries(-1),
		WithProcessDefinitionReliabilityRetryDelay(-1),
		WithProcessDefinitionReliabilityRetryDelay(24*time.Hour + 1),
		WithProcessDefinitionReliabilityRetryTimeout(-1),
		WithProcessDefinitionReliabilityRetryTimeout(24*time.Hour + 1),
	} {
		got, err := NewProcessDefinitionReliability(option)
		if err == nil || got != nil {
			t.Fatalf("accepted invalid reliability: %+v, %v", got, err)
		}
	}
}

const (
	testUnknown = "unknown"
	testCommand = "command"
	testBuild   = "build"
)
