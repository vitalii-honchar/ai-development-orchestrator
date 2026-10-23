package domain

import (
	"time"
	"uuid"

	"go-software-factory/internal/core/validator"
)

type ProcessDefinitionState string

const (
	ProcessDefinitionStateDraft    ProcessDefinitionState = "DRAFT"
	ProcessDefinitionStateActive   ProcessDefinitionState = "ACTIVE"
	ProcessDefinitionStateArchived ProcessDefinitionState = "ARCHIVED"
)

type ProcessDefinition struct {
	ID          uuid.UUID                    `json:"id"                    validate:"required"`
	Name        string                       `json:"name,omitempty"        validate:"utf8text"`
	Description string                       `json:"description,omitempty" validate:"utf8text"`
	State       ProcessDefinitionState       `json:"state"                 validate:"required,oneof=DRAFT ACTIVE ARCHIVED"`
	Version     int                          `json:"version"               validate:"gte=1"`
	Graph       ProcessGraph                 `json:"graph"`
	Reliability ProcessDefinitionReliability `json:"reliability,omitempty"`
	Inputs      *Parameters                  `json:"inputs,omitempty"`
	CreatedAt   time.Time                    `json:"created_at"            validate:"required"`
	UpdatedAt   time.Time                    `json:"updated_at"            validate:"required,gtefield=CreatedAt"`
}

type ProcessDefinitionOpt func(*ProcessDefinition)

func WithNewProcessDefinitionID() ProcessDefinitionOpt {
	return func(model *ProcessDefinition) {
		model.ID = uuid.New()
	}
}

func WithProcessDefinitionID(value uuid.UUID) ProcessDefinitionOpt {
	return func(model *ProcessDefinition) {
		model.ID = value
	}
}

func WithProcessDefinitionName(value string) ProcessDefinitionOpt {
	return func(model *ProcessDefinition) {
		model.Name = value
	}
}

func WithProcessDefinitionDescription(value string) ProcessDefinitionOpt {
	return func(model *ProcessDefinition) {
		model.Description = value
	}
}

func WithProcessDefinitionState(value ProcessDefinitionState) ProcessDefinitionOpt {
	return func(model *ProcessDefinition) {
		model.State = value
	}
}

func WithProcessDefinitionVersion(value int) ProcessDefinitionOpt {
	return func(model *ProcessDefinition) {
		model.Version = value
	}
}

func WithProcessDefinitionGraph(value ProcessGraph) ProcessDefinitionOpt {
	return func(model *ProcessDefinition) {
		model.Graph = value
	}
}

func WithProcessDefinitionReliability(value ProcessDefinitionReliability) ProcessDefinitionOpt {
	return func(model *ProcessDefinition) {
		model.Reliability = value
	}
}

func WithProcessDefinitionInputs(value *Parameters) ProcessDefinitionOpt {
	return func(model *ProcessDefinition) {
		model.Inputs = value
	}
}

func WithProcessDefinitionCreatedAt(value time.Time) ProcessDefinitionOpt {
	return func(model *ProcessDefinition) {
		model.CreatedAt = value
	}
}

func WithProcessDefinitionUpdatedAt(value time.Time) ProcessDefinitionOpt {
	return func(model *ProcessDefinition) {
		model.UpdatedAt = value
	}
}

func NewProcessDefinition(opts ...ProcessDefinitionOpt) (*ProcessDefinition, error) {
	model := &ProcessDefinition{Graph: *NewProcessGraph()}
	for _, opt := range opts {
		opt(model)
	}
	if _, err := validator.Struct(model); err != nil {
		return nil, err
	}
	if err := model.Graph.validate(); err != nil {
		return nil, err
	}

	return model, nil
}

type ProcessStepDefinition struct {
	Name        string                       `json:"name"              validate:"required,notblank,utf8text"`
	Type        ProcessStepType              `json:"type"              validate:"omitempty,notblank,utf8text"`
	Reliability ProcessDefinitionReliability `json:"reliability"`
	Params      *Parameters                  `json:"params,omitempty"`
	Context     *Parameters                  `json:"context,omitempty"`
}

func WithProcessStepDefinitionContext(value *Parameters) ProcessStepDefinitionOpt {
	return func(model *ProcessStepDefinition) { model.Context = value }
}

type ProcessStepDefinitionOpt func(*ProcessStepDefinition)

func WithProcessStepDefinitionName(value string) ProcessStepDefinitionOpt {
	return func(model *ProcessStepDefinition) {
		model.Name = value
	}
}

func WithProcessStepDefinitionType(value ProcessStepType) ProcessStepDefinitionOpt {
	return func(model *ProcessStepDefinition) {
		model.Type = value
	}
}

func WithProcessStepDefinitionReliability(value ProcessDefinitionReliability) ProcessStepDefinitionOpt {
	return func(model *ProcessStepDefinition) {
		model.Reliability = value
	}
}

func WithProcessStepDefinitionParams(value *Parameters) ProcessStepDefinitionOpt {
	return func(model *ProcessStepDefinition) {
		model.Params = value
	}
}

func NewProcessStepDefinition(opts ...ProcessStepDefinitionOpt) (*ProcessStepDefinition, error) {
	model := &ProcessStepDefinition{}
	for _, opt := range opts {
		opt(model)
	}

	return validator.Struct(model)
}

func (d *ProcessStepDefinition) CreateStep(processID uuid.UUID, context *ProcessContext) (*ProcessStep, error) {
	step := newProcessStep(
		WithNewProcessStepID(), WithProcessStepProcessID(processID), WithProcessStepState(ProcessStepStatePending),
		WithProcessStepProcessStepDefinitionName(d.Name), WithProcessStepType(d.Type),
		WithProcessStepExecutionPolicy(d.Reliability), WithProcessStepParams(d.Params), WithProcessStepContext(d.Context),
	)
	if err := step.Resolve(context); err != nil {
		return nil, err
	}
	if err := step.Validate(); err != nil {
		return nil, err
	}
	if err := step.validateParams(); err != nil {
		return nil, err
	}

	return step, nil
}

type ProcessDefinitionReliability struct {
	Timeout      time.Duration `json:"timeout,omitempty"       validate:"gte=0,lte=2592000000000000"`
	MaxRetries   int           `json:"max_retries,omitempty"   validate:"gte=0"`
	RetryDelay   time.Duration `json:"retry_delay,omitempty"   validate:"gte=0,lte=86400000000000"`
	RetryTimeout time.Duration `json:"retry_timeout,omitempty" validate:"gte=0,lte=86400000000000"`
}

type ProcessDefinitionReliabilityOpt func(*ProcessDefinitionReliability)

func WithProcessDefinitionReliabilityTimeout(value time.Duration) ProcessDefinitionReliabilityOpt {
	return func(model *ProcessDefinitionReliability) {
		model.Timeout = value
	}
}

func WithProcessDefinitionReliabilityMaxRetries(value int) ProcessDefinitionReliabilityOpt {
	return func(model *ProcessDefinitionReliability) {
		model.MaxRetries = value
	}
}

func WithProcessDefinitionReliabilityRetryDelay(value time.Duration) ProcessDefinitionReliabilityOpt {
	return func(model *ProcessDefinitionReliability) {
		model.RetryDelay = value
	}
}

func WithProcessDefinitionReliabilityRetryTimeout(value time.Duration) ProcessDefinitionReliabilityOpt {
	return func(model *ProcessDefinitionReliability) {
		model.RetryTimeout = value
	}
}

func NewProcessDefinitionReliability(opts ...ProcessDefinitionReliabilityOpt) (*ProcessDefinitionReliability, error) {
	model := &ProcessDefinitionReliability{}
	for _, opt := range opts {
		opt(model)
	}

	return validator.Struct(model)
}
