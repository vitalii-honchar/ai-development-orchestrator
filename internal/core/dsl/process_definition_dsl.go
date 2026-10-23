package dsl

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"go-software-factory/internal/core/domain"
)

var (
	errInvalidUTF8           = errors.New("process definition YAML: invalid UTF-8")
	errExpectedMapping       = errors.New("process definition YAML: expected one mapping document")
	errDefinitionRequired    = errors.New("process definition is required")
	errMissingStepDefinition = errors.New("missing definition")
)

type ProcessDefinitionDsl struct {
	Name        string                              `yaml:"name,omitempty"`
	Description string                              `yaml:"description,omitempty"`
	Inputs      map[string]InputDsl                 `yaml:"inputs,omitempty"`
	Reliability ReliabilityDsl                      `yaml:"reliability,omitempty"`
	Steps       map[string]ProcessStepDefinitionDsl `yaml:"steps"`
}

type ProcessStepDefinitionDsl struct {
	Type        string              `yaml:"type"`
	Inputs      map[string]InputDsl `yaml:"inputs,omitempty"`
	Context     map[string]InputDsl `yaml:"context,omitempty"`
	Reliability ReliabilityDsl      `yaml:"reliability,omitempty"`
	Next        []string            `yaml:"next,omitempty"`
}

type InputDsl struct {
	Type     string `yaml:"type"`
	Required bool   `yaml:"required"`
	Value    any    `yaml:"value,omitempty"`
	From     string `yaml:"from,omitempty"`
}

type ReliabilityDsl struct {
	Timeout      string `yaml:"timeout,omitempty"`
	MaxRetries   int    `yaml:"max_retries,omitempty"`
	RetryDelay   string `yaml:"retry_delay,omitempty"`
	RetryTimeout string `yaml:"retry_timeout,omitempty"`
}

// ProcessDefinitionDslParse converts one YAML definition into a new active definition.
// Step names are local to this definition; only the definition receives a UUID.
func ProcessDefinitionDslParse(data []byte) (*domain.ProcessDefinition, error) {
	dto, err := decodeProcessDefinitionDsl(data)
	if err != nil {
		return nil, err
	}

	return dto.toDomain()
}

func decodeProcessDefinitionDsl(data []byte) (*ProcessDefinitionDsl, error) {
	if !utf8.Valid(data) {
		return nil, errInvalidUTF8
	}
	file, err := parser.ParseBytes(data, 0)
	if err != nil {
		return nil, fmt.Errorf("process definition YAML: %w", err)
	}
	if len(file.Docs) != 1 || file.Docs[0].Body == nil {
		return nil, errExpectedMapping
	}
	if _, ok := file.Docs[0].Body.(*ast.MappingNode); !ok {
		return nil, errExpectedMapping
	}
	var dto ProcessDefinitionDsl
	// The YAML decoder otherwise coerces numbers and booleans into strings,
	// and fractional numbers into integers. Keep DSL scalar types explicit.
	if err := yaml.UnmarshalWithOptions(data, &dto, yaml.Strict(),
		yaml.CustomUnmarshaler[string](decodeScalar[string]),
		yaml.CustomUnmarshaler[int](decodeScalar[int]),
		yaml.CustomUnmarshaler[bool](decodeScalar[bool]),
	); err != nil {
		return nil, fmt.Errorf("process definition YAML: %w", err)
	}

	return &dto, nil
}

func (dto *ProcessDefinitionDsl) toDomain() (*domain.ProcessDefinition, error) {
	inputs, err := convertInputs(dto.Inputs)
	if err != nil {
		return nil, fmt.Errorf("inputs: %w", err)
	}
	reliability, err := dto.Reliability.toDomain()
	if err != nil {
		return nil, fmt.Errorf("reliability: %w", err)
	}
	graph := domain.NewProcessGraph()
	names := slices.Sorted(maps.Keys(dto.Steps))
	for _, name := range names {
		node, err := dto.Steps[name].toDomain(name)
		if err != nil {
			return nil, err
		}
		if err := graph.AddNode(node); err != nil {
			return nil, fmt.Errorf("steps[%q]: %w", name, err)
		}
	}
	for _, name := range names {
		for _, target := range dto.Steps[name].Next {
			if err := graph.AddEdge(name, target); err != nil {
				return nil, fmt.Errorf("steps[%q].next: unknown step %q: %w", name, target, err)
			}
		}
	}
	now := time.Now().UTC()
	definition, err := domain.NewProcessDefinition(
		domain.WithNewProcessDefinitionID(),
		domain.WithProcessDefinitionName(dto.Name),
		domain.WithProcessDefinitionDescription(dto.Description),
		domain.WithProcessDefinitionState(domain.ProcessDefinitionStateActive),
		domain.WithProcessDefinitionVersion(1),
		domain.WithProcessDefinitionInputs(inputs),
		domain.WithProcessDefinitionReliability(*reliability),
		domain.WithProcessDefinitionGraph(*graph),
		domain.WithProcessDefinitionCreatedAt(now),
		domain.WithProcessDefinitionUpdatedAt(now),
	)
	if err != nil {
		return nil, fmt.Errorf("process definition: %w", err)
	}

	return definition, nil
}

func (step ProcessStepDefinitionDsl) toDomain(name string) (*domain.ProcessStepDefinition, error) {
	params, err := convertInputs(step.Inputs)
	if err != nil {
		return nil, fmt.Errorf("steps[%q].inputs: %w", name, err)
	}
	context, err := convertInputs(step.Context)
	if err != nil {
		return nil, fmt.Errorf("steps[%q].context: %w", name, err)
	}
	stepReliability, err := step.Reliability.toDomain()
	if err != nil {
		return nil, fmt.Errorf("steps[%q].reliability: %w", name, err)
	}
	node, err := domain.NewProcessStepDefinition(
		domain.WithProcessStepDefinitionName(name),
		domain.WithProcessStepDefinitionType(domain.ProcessStepType(step.Type)),
		domain.WithProcessStepDefinitionParams(params),
		domain.WithProcessStepDefinitionContext(context),
		domain.WithProcessStepDefinitionReliability(*stepReliability),
	)
	if err != nil {
		return nil, fmt.Errorf("steps[%q]: %w", name, err)
	}

	return node, nil
}

func decodeScalar[T any](value *T, data []byte) error {
	encoded, err := yaml.YAMLToJSON(data)
	if err != nil {
		return fmt.Errorf("convert YAML scalar to JSON: %w", err)
	}
	if err := json.Unmarshal(encoded, value); err != nil {
		return fmt.Errorf("decode scalar: %w", err)
	}

	return nil
}

func convertInputs(inputs map[string]InputDsl) (*domain.Parameters, error) {
	if inputs == nil {
		return nil, nil
	}
	values := make(map[string]domain.ParameterValue, len(inputs))
	for name, input := range inputs {
		if err := input.validateSource(); err != nil {
			return nil, fmt.Errorf("parameter %q: %w", name, err)
		}
		values[name] = domain.ParameterValue{
			Type: domain.ParameterType(input.Type), Required: input.Required, Value: input.Value, From: input.From,
		}
	}

	return domain.NewParameters(values)
}

func (input InputDsl) validateSource() error {
	if input.From == "" {
		return nil
	}
	if name, ok := strings.CutPrefix(input.From, "inputs."); ok && strings.TrimSpace(name) != "" {
		return nil
	}
	if path, ok := strings.CutPrefix(input.From, "steps."); ok {
		step, field, found := strings.Cut(path, ".output.")
		if found && strings.TrimSpace(step) != "" && strings.TrimSpace(field) != "" {
			return nil
		}
	}

	return fmt.Errorf("%w: invalid source expression %q", domain.ErrInvalidParameters, input.From)
}

func (r ReliabilityDsl) toDomain() (*domain.ProcessDefinitionReliability, error) {
	var timeout, retryDelay, retryTimeout time.Duration
	var err error
	if r.Timeout != "" {
		timeout, err = time.ParseDuration(r.Timeout)
		if err != nil {
			return nil, fmt.Errorf("timeout: %w", err)
		}
	}
	if r.RetryDelay != "" {
		retryDelay, err = time.ParseDuration(r.RetryDelay)
		if err != nil {
			return nil, fmt.Errorf("retry_delay: %w", err)
		}
	}
	if r.RetryTimeout != "" {
		retryTimeout, err = time.ParseDuration(r.RetryTimeout)
		if err != nil {
			return nil, fmt.Errorf("retry_timeout: %w", err)
		}
	}

	return domain.NewProcessDefinitionReliability(
		domain.WithProcessDefinitionReliabilityTimeout(timeout),
		domain.WithProcessDefinitionReliabilityMaxRetries(r.MaxRetries),
		domain.WithProcessDefinitionReliabilityRetryDelay(retryDelay),
		domain.WithProcessDefinitionReliabilityRetryTimeout(retryTimeout),
	)
}

// ProcessDefinitionDslMarshal writes editable YAML without persistence metadata.
// Comments and formatting from the original source are not stored in the domain.
func ProcessDefinitionDslMarshal(definition *domain.ProcessDefinition) ([]byte, error) {
	if definition == nil {
		return nil, errDefinitionRequired
	}

	dto := &ProcessDefinitionDsl{
		Name:        definition.Name,
		Description: definition.Description,
		Inputs:      inputsFromDomain(definition.Inputs),
		Reliability: reliabilityFromDomain(definition.Reliability),
		Steps:       make(map[string]ProcessStepDefinitionDsl, len(definition.Graph.Nodes)),
	}
	for name, step := range definition.Graph.Nodes {
		if step == nil {
			return nil, fmt.Errorf("steps[%q]: %w", name, errMissingStepDefinition)
		}

		dto.Steps[name] = ProcessStepDefinitionDsl{
			Type:        string(step.Type),
			Inputs:      inputsFromDomain(step.Params),
			Context:     inputsFromDomain(step.Context),
			Reliability: reliabilityFromDomain(step.Reliability),
			Next:        definition.Graph.Edges[name],
		}
	}

	encoded, err := yaml.MarshalWithOptions(dto, yaml.Indent(2))
	if err != nil {
		return nil, fmt.Errorf("marshal process definition YAML: %w", err)
	}

	return encoded, nil
}

func inputsFromDomain(inputs *domain.Parameters) map[string]InputDsl {
	if inputs == nil {
		return nil
	}
	result := make(map[string]InputDsl, inputs.Len())
	for name, input := range inputs.Entries() {
		// Keep validated JSON encoded: decoding into any changes scalar types and number precision.
		result[name] = InputDsl{Type: string(input.Type), Required: input.Required, Value: input.Value, From: input.From}
	}

	return result
}

func reliabilityFromDomain(reliability domain.ProcessDefinitionReliability) ReliabilityDsl {
	result := ReliabilityDsl{MaxRetries: reliability.MaxRetries}
	if reliability.Timeout != 0 {
		result.Timeout = reliability.Timeout.String()
	}
	if reliability.RetryDelay != 0 {
		result.RetryDelay = reliability.RetryDelay.String()
	}
	if reliability.RetryTimeout != 0 {
		result.RetryTimeout = reliability.RetryTimeout.String()
	}

	return result
}
