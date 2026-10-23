package stepexecutor_test

import (
	"context"
	"encoding/xml"
	"errors"
	"strings"
	"testing"
	"uuid"

	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/worker/app/stepexecutor"
)

const (
	testPrompt     = "prompt"
	testOutputJSON = "output_json"
)

type codingExecutorFunc func(context.Context, stepexecutor.AICodingArguments) (string, error)

func (f codingExecutorFunc) Execute(ctx context.Context, args stepexecutor.AICodingArguments) (string, error) {
	return f(ctx, args)
}

func TestAICodingStepExecutorMatches(t *testing.T) {
	t.Parallel()
	executor := stepexecutor.NewAICodingStepExecutor(nil)
	for _, stepType := range []domain.ProcessStepType{"AI_CODING_AGENT", "TERMINAL", "TEST_STEP_TYPE", "custom"} {
		got, err := executor.Matches(t.Context(), &domain.ProcessStep{Type: stepType})
		if err != nil || got != (stepType == "AI_CODING_AGENT") {
			t.Fatalf("type=%s matches=%v error=%v", stepType, got, err)
		}
	}
}

func TestAICodingStepExecutorRendersContextAndDynamicOutputSchema(t *testing.T) {
	t.Parallel()
	const schema = `{"type":"object","properties":{"summary":{"type":"string"},"path_to_plan":{"type":"string"}},` +
		`"required":["summary","path_to_plan"],"additionalProperties":false}`
	params := codingParameters(t.TempDir())
	testSetParameters(params, testOutputJSON, domain.ParameterValue{Type: domain.ParameterTypeJSONSchema, Value: schema})
	testSetParameters(params, testPrompt, domain.ParameterValue{
		Type: domain.ParameterTypeString, Value: "Plan <the solution> & preserve {{ literal }}.",
	})
	inputs := testParameters(map[string]domain.ParameterValue{
		"description": {Type: domain.ParameterTypeString, Value: "Build <tasks> & labels"},
		"docs":        {Type: domain.ParameterTypeString, Value: "https://example.com/?a=1&b=2"},
	})
	var args stepexecutor.AICodingArguments
	executor := stepexecutor.NewAICodingStepExecutor(codingExecutorFunc(func(
		_ context.Context, received stepexecutor.AICodingArguments,
	) (string, error) {
		args = received

		return `{"summary":"Planned","path_to_plan":"/project/docs/plans/solution-plan-20261019.md"}`, nil
	}))
	step := &domain.ProcessStep{Params: params, Context: inputs, ProcessStepDefinitionName: "brainstorm",
		ID:          uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ProcessID:   uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Reliability: domain.ProcessReliability{Executions: 7}}
	result, err := executor.Execute(t.Context(), step)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.String("output"), `"path_to_plan"`) {
		t.Fatal("dynamic output fields were discarded")
	}
	if args.OutputSchema != schema {
		t.Fatal("configured output schema was replaced")
	}
	if args.ProcessID != step.ProcessID.String() || args.StepID != step.ID.String() ||
		args.StepName != "brainstorm" || args.Attempt != 7 {
		t.Fatalf("execution audit lost step correlation: %+v", args)
	}
	var rendered struct {
		Behaviour string `xml:"behaviour"`
		Schema    string `xml:"output-schema"`
		Inputs    struct {
			Description string `xml:"description"`
			Docs        string `xml:"docs"`
		} `xml:"inputs"`
	}
	if err := xml.Unmarshal([]byte("<prompt>"+args.Prompt+"</prompt>"), &rendered); err != nil {
		t.Fatal(err)
	}
	if rendered.Behaviour != "Plan <the solution> & preserve {{ literal }}." || rendered.Schema != schema ||
		rendered.Inputs.Description != "Build <tasks> & labels" || rendered.Inputs.Docs != "https://example.com/?a=1&b=2" {
		t.Fatalf("escaped XML changed prompt data: %+v", rendered)
	}
	position := -1
	for _, section := range []string{"<constraints>", "<behaviour>", "<output-schema>", "<inputs>"} {
		next := strings.Index(args.Prompt, section)
		if next <= position {
			t.Fatalf("incorrect prompt section order: %s", args.Prompt)
		}
		position = next
	}
}

func TestAICodingStepExecutorDefaultsAndOverrides(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		model  string
		effort string
	}{
		{name: "defaults", model: "gpt-6.1-sol", effort: "xhigh"},
		{name: "overrides", model: "configured-model", effort: "medium"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			workdir := t.TempDir()
			params := codingParameters(workdir)
			if tc.name == "overrides" {
				testSetParameters(params, "model", domain.ParameterValue{Type: domain.ParameterTypeString, Value: tc.model})
				testSetParameters(params, "reasoning_effort", domain.ParameterValue{
					Type: domain.ParameterTypeString, Value: tc.effort,
				})
			}
			executor := stepexecutor.NewAICodingStepExecutor(codingExecutorFunc(func(
				_ context.Context, args stepexecutor.AICodingArguments,
			) (string, error) {
				if args.WorkingDirectory != workdir || args.Model != tc.model || args.ReasoningEffort != tc.effort {
					t.Fatalf("execution arguments: %+v", args)
				}

				return `{"summary":"Executed the task."}`, nil
			}))
			result, err := executor.Execute(t.Context(), &domain.ProcessStep{Params: params})
			if err != nil || result.String("output") != `{"summary":"Executed the task."}` {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}

func TestAICodingStepExecutorRejectsInvalidOutput(t *testing.T) {
	t.Parallel()
	for _, output := range []string{"", "plain text", `{} {}`, "{\"summary\":\"\xff\"}"} {
		t.Run(output, func(t *testing.T) {
			t.Parallel()
			executor := stepexecutor.NewAICodingStepExecutor(codingExecutorFunc(func(
				context.Context, stepexecutor.AICodingArguments,
			) (string, error) {
				return output, nil
			}))
			result, err := executor.Execute(t.Context(), &domain.ProcessStep{Params: codingParameters(t.TempDir())})
			if err == nil || result != nil {
				t.Fatalf("accepted invalid output %q: %+v, %v", output, result, err)
			}
		})
	}
}

func TestAICodingStepExecutorPropagatesExecutionError(t *testing.T) {
	t.Parallel()
	cause := errors.New("agent execution failed")
	executor := stepexecutor.NewAICodingStepExecutor(codingExecutorFunc(func(
		context.Context, stepexecutor.AICodingArguments,
	) (string, error) {
		return "", cause
	}))
	result, err := executor.Execute(t.Context(), &domain.ProcessStep{Params: codingParameters(t.TempDir())})
	if result != nil || !errors.Is(err, cause) {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func codingParameters(workdir string) *domain.Parameters {
	return testParameters(map[string]domain.ParameterValue{
		testOutputJSON: {Type: domain.ParameterTypeJSONSchema, Value: `{
  "type": "object",
  "properties": {"summary": {"type": "string"}},
  "required": ["summary"],
  "additionalProperties": false
}`},
		testPrompt:          {Type: domain.ParameterTypeString, Value: "Run the task."},
		"working_directory": {Type: domain.ParameterTypeFilePath, Value: workdir},
	})
}

func testParameters(values map[string]domain.ParameterValue) *domain.Parameters {
	params, err := domain.NewParameters(values)
	if err != nil {
		panic(err)
	}

	return params
}

func testSetParameters(params *domain.Parameters, name string, value domain.ParameterValue) {
	if err := params.Set(name, value); err != nil {
		panic(err)
	}
}
