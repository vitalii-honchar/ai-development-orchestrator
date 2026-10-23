package domain

import (
	"encoding/json"
	"fmt"
	"strings"

	"go-software-factory/internal/core/validator"
)

// ProcessContext contains the initial inputs and completed step results for one process.
type ProcessContext struct {
	Inputs      *Parameters            `json:"inputs,omitempty"`
	StepOutputs map[string]*Parameters `json:"step_outputs,omitempty" validate:"dive,keys,notblank,utf8text,endkeys"`
}

func NewProcessContext(inputs *Parameters, stepOutputs map[string]*Parameters) (*ProcessContext, error) {
	return validator.Struct(&ProcessContext{Inputs: inputs, StepOutputs: stepOutputs})
}

// Resolve reads a process input or a field from a completed step's structured output.
func (c *ProcessContext) Resolve(source string, kind ParameterType) (ParameterValue, error) {
	if c == nil {
		return ParameterValue{}, fmt.Errorf("%w: missing source %q", ErrInvalidParameters, source)
	}
	if name, ok := strings.CutPrefix(source, "inputs."); ok {
		if c.Inputs.Has(name) {
			return c.Inputs.Get(name), nil
		}

		return ParameterValue{}, fmt.Errorf("%w: missing source %q", ErrInvalidParameters, source)
	}
	path, ok := strings.CutPrefix(source, "steps.")
	if !ok {
		return ParameterValue{}, fmt.Errorf("%w: unsupported source %q", ErrInvalidParameters, source)
	}
	stepName, field, found := strings.Cut(path, ".output.")
	if !found || strings.TrimSpace(stepName) == "" || strings.TrimSpace(field) == "" {
		return ParameterValue{}, fmt.Errorf("%w: invalid source expression %q", ErrInvalidParameters, source)
	}
	result, exists := c.StepOutputs[stepName]
	if !exists {
		return ParameterValue{}, fmt.Errorf("%w: missing completed output %q", ErrInvalidParameters, source)
	}
	var output map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.String("output")), &output); err != nil {
		return ParameterValue{}, fmt.Errorf("decode referenced output: %w", err)
	}
	value, exists := output[field]
	if !exists {
		return ParameterValue{}, fmt.Errorf("%w: missing completed output %q", ErrInvalidParameters, source)
	}

	return c.outputValue(kind, source, value)
}

func (*ProcessContext) outputValue(kind ParameterType, source string, value json.RawMessage) (ParameterValue, error) {
	switch kind {
	case ParameterTypeJSON, ParameterTypeJSONSchema, ParameterTypeReferencesArray:
		return ParameterValue{Type: kind, Value: string(value)}, nil
	default:
		var text *string
		if err := json.Unmarshal(value, &text); err != nil {
			return ParameterValue{}, fmt.Errorf("decode referenced text: %w", err)
		}
		if text == nil {
			return ParameterValue{}, fmt.Errorf("%w: source %q must be text", ErrInvalidParameters, source)
		}

		return ParameterValue{Type: kind, Value: *text}, nil
	}
}
