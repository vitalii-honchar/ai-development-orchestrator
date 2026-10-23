package stepexecutor

import (
	"context"
	"encoding/xml"
	"fmt"
	"slices"
	"strings"

	"go-software-factory/internal/core/domain"
)

const agentConstraints = "Follow the repository instructions and " +
	"complete the requested task using the " +
	"supplied inputs. Stay within the requested " +
	"scope. Be concise. Use the output schema for " +
	"your final JSON response."

const (
	promptParam            = "prompt"
	workingDirectoryParam  = "working_directory"
	modelParam             = "model"
	reasoningEffortParam   = "reasoning_effort"
	outputJSONParam        = "output_json"
	defaultModel           = "gpt-6.1-sol"
	defaultReasoningEffort = "xhigh"
)

// AICodingArguments contains the execution data supplied to a coding agent.
type AICodingArguments struct {
	ProcessID        string
	StepID           string
	StepName         string
	Attempt          int
	Prompt           string
	WorkingDirectory string
	Model            string
	ReasoningEffort  string
	OutputSchema     string
}

// AICodingExecutor runs a coding agent without depending on process-step models.
type AICodingExecutor interface {
	Execute(context.Context, AICodingArguments) (string, error)
}

type AICodingStepExecutor struct {
	executor AICodingExecutor
}

func NewAICodingStepExecutor(executor AICodingExecutor) *AICodingStepExecutor {
	return &AICodingStepExecutor{executor: executor}
}

func (*AICodingStepExecutor) Matches(_ context.Context, step *domain.ProcessStep) (bool, error) {
	return step.Type == domain.ProcessStepTypeAICodingAgent, nil
}

func (e *AICodingStepExecutor) Execute(ctx context.Context, step *domain.ProcessStep) (*domain.Parameters, error) {
	args := e.executionParameters(step.Params)
	args.ProcessID, args.StepID = step.ProcessID.String(), step.ID.String()
	args.StepName, args.Attempt = step.ProcessStepDefinitionName, step.Reliability.Executions
	prompt, err := e.renderPrompt(step)
	if err != nil {
		return nil, err
	}
	args.Prompt = prompt
	output, err := e.executor.Execute(ctx, args)
	if err != nil {
		return nil, err
	}

	return domain.NewParameters(map[string]domain.ParameterValue{
		"output": {Type: domain.ParameterTypeJSON, Value: output},
	})
}

func (*AICodingStepExecutor) executionParameters(values *domain.Parameters) AICodingArguments {
	args := AICodingArguments{
		WorkingDirectory: values.String(workingDirectoryParam),
		Model:            defaultModel,
		ReasoningEffort:  defaultReasoningEffort,
		OutputSchema:     values.String(outputJSONParam),
	}
	if values.Has(modelParam) {
		args.Model = values.String(modelParam)
	}
	if values.Has(reasoningEffortParam) {
		args.ReasoningEffort = values.String(reasoningEffortParam)
	}

	return args
}

func (*AICodingStepExecutor) renderPrompt(step *domain.ProcessStep) (string, error) {
	var text strings.Builder
	encoder := xml.NewEncoder(&text)
	for _, section := range []struct{ name, content string }{
		{"constraints", agentConstraints},
		{"behaviour", step.Params.String(promptParam)},
		{"output-schema", step.Params.String(outputJSONParam)},
	} {
		if err := encoder.EncodeElement(section.content, xml.StartElement{Name: xml.Name{Local: section.name}}); err != nil {
			return "", fmt.Errorf("render agent prompt: %w", err)
		}
		text.WriteByte('\n')
	}
	inputs := xml.StartElement{Name: xml.Name{Local: "inputs"}}
	if err := encoder.EncodeToken(inputs); err != nil {
		return "", fmt.Errorf("render agent inputs: %w", err)
	}
	names := make([]string, 0, step.Context.Len())
	for name := range step.Context.Entries() {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := encoder.EncodeElement(step.Context.String(name), xml.StartElement{
			Name: xml.Name{Local: name},
		}); err != nil {
			return "", fmt.Errorf("render agent input %q: %w", name, err)
		}
	}
	if err := encoder.EncodeToken(inputs.End()); err != nil {
		return "", fmt.Errorf("close agent inputs: %w", err)
	}
	if err := encoder.Flush(); err != nil {
		return "", fmt.Errorf("flush agent prompt: %w", err)
	}

	return text.String(), nil
}
