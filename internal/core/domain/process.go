package domain

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"uuid"

	"go-software-factory/internal/core/validator"
)

var ErrNoDetailsInResult = errors.New("failed state requires error details in result")

var contextParameterName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

type ProcessState string

const (
	ProcessStatePending    ProcessState = "PENDING"
	ProcessStateInProgress ProcessState = "IN_PROGRESS"
	ProcessStateCompleted  ProcessState = "COMPLETED"
	ProcessStateFailed     ProcessState = "FAILED"
)

type Process struct {
	ID    uuid.UUID    `json:"id"    validate:"required"`
	State ProcessState `json:"state" validate:"required,oneof=PENDING IN_PROGRESS COMPLETED FAILED"`

	ProcessDefinitionID uuid.UUID          `json:"process_definition_id" validate:"required"`
	CurrentSteps        []string           `json:"current_steps"         validate:"dive,notblank,utf8text"`
	Context             *ProcessContext    `json:"context,omitempty"`
	Result              *Parameters        `json:"result,omitempty"`
	Reliability         ProcessReliability `json:"reliability"`
	Version             int                `json:"version"               validate:"gte=1"`
	CreatedAt           time.Time          `json:"created_at,omitempty"  validate:"required_with=UpdatedAt"`
	UpdatedAt           time.Time          `json:"updated_at,omitempty"  validate:"omitempty,gtefield=CreatedAt"`
}

type ProcessOpt func(*Process)

func WithNewProcessID() ProcessOpt {
	return func(model *Process) {
		model.ID = uuid.New()
	}
}

func WithProcessID(value uuid.UUID) ProcessOpt {
	return func(model *Process) {
		model.ID = value
	}
}

func WithProcessState(value ProcessState) ProcessOpt {
	return func(model *Process) {
		model.State = value
	}
}

func WithProcessProcessDefinitionID(value uuid.UUID) ProcessOpt {
	return func(model *Process) {
		model.ProcessDefinitionID = value
	}
}

func WithProcessContext(value *ProcessContext) ProcessOpt {
	return func(model *Process) {
		model.Context = value
	}
}

func WithProcessCurrentSteps(value []string) ProcessOpt {
	return func(model *Process) {
		model.CurrentSteps = value
	}
}

func WithProcessResult(value *Parameters) ProcessOpt {
	return func(model *Process) {
		model.Result = value
	}
}

func WithProcessReliability(value ProcessReliability) ProcessOpt {
	return func(model *Process) {
		model.Reliability = value
	}
}

func WithProcessVersion(value int) ProcessOpt {
	return func(model *Process) {
		model.Version = value
	}
}

func WithProcessCreatedAt(value time.Time) ProcessOpt {
	return func(model *Process) {
		model.CreatedAt = value
	}
}

func WithProcessUpdatedAt(value time.Time) ProcessOpt {
	return func(model *Process) {
		model.UpdatedAt = value
	}
}

func NewProcess(opts ...ProcessOpt) (*Process, error) {
	model := &Process{Context: &ProcessContext{}}
	for _, opt := range opts {
		opt(model)
	}
	if err := model.Validate(); err != nil {
		return nil, err
	}

	return model, nil
}

// Validate checks the process without modifying it.
func (p *Process) Validate() error {
	if _, err := validator.Struct(p); err != nil {
		return err
	}
	if p.State == ProcessStateFailed && p.Result.Len() == 0 {
		return ErrNoDetailsInResult
	}

	return nil
}

func (p *Process) IsFinalState() bool {
	return p.State == ProcessStateCompleted || p.State == ProcessStateFailed
}

func (p *Process) Acquire(timeout time.Duration) error {
	if p.IsFinalState() {
		return ErrProcessTerminal
	}
	p.Reliability.Executions++
	p.UpdatedAt = time.Now().UTC()
	next := p.UpdatedAt.Add(timeout)
	p.Reliability.ExecutedAt = &next

	return p.Validate()
}

func (p *Process) UpdateCurrentSteps(steps []string) error {
	p.CurrentSteps = steps
	p.UpdatedAt = time.Now().UTC()

	return p.Validate()
}

// RecordStepOutputs adds completed results to the persisted execution context.
func (p *Process) RecordStepOutputs(steps []ProcessStep) (bool, error) {
	changed := p.Context == nil
	if p.Context == nil {
		p.Context = &ProcessContext{}
	}
	for _, step := range steps {
		if step.State != ProcessStepStateCompleted {
			continue
		}
		if _, exists := p.Context.StepOutputs[step.ProcessStepDefinitionName]; exists {
			continue
		}
		if p.Context.StepOutputs == nil {
			p.Context.StepOutputs = make(map[string]*Parameters)
		}
		p.Context.StepOutputs[step.ProcessStepDefinitionName] = step.Result
		changed = true
	}
	if !changed {
		return false, nil
	}
	p.UpdatedAt = time.Now().UTC()

	return true, p.Validate()
}

func (p *Process) Complete(result *Parameters) error {
	if p.IsFinalState() {
		return ErrProcessTerminal
	}
	p.State = ProcessStateCompleted
	p.Result = result
	p.finish()

	return p.Validate()
}

func (p *Process) Fail(result *Parameters) error {
	if p.IsFinalState() {
		return ErrProcessTerminal
	}
	p.State = ProcessStateFailed
	p.Result = result
	p.finish()

	return p.Validate()
}

func (p *Process) finish() {
	now := time.Now().UTC()
	p.UpdatedAt = now
	p.Reliability.ExecutionTime = now.Sub(p.CreatedAt)
}

type ProcessStepState string

const (
	ProcessStepStatePending   ProcessStepState = "PENDING"
	ProcessStepStateCompleted ProcessStepState = "COMPLETED"
	ProcessStepStateFailed    ProcessStepState = "FAILED"
)

type ProcessStepType string

const (
	ProcessStepTypeTerminal      ProcessStepType = "TERMINAL"
	ProcessStepTypeAICodingAgent ProcessStepType = "AI_CODING_AGENT"
)

type ProcessStep struct {
	ID        uuid.UUID        `json:"id"         validate:"required"`
	State     ProcessStepState `json:"state"      validate:"required,oneof=PENDING COMPLETED FAILED"`
	ProcessID uuid.UUID        `json:"process_id" validate:"required"`

	ProcessStepDefinitionName string `json:"process_step_definition_name" validate:"required,notblank,utf8text"`

	Type            ProcessStepType              `json:"type"              validate:"required,notblank,utf8text"`
	Params          *Parameters                  `json:"params,omitempty"`
	Context         *Parameters                  `json:"context,omitempty"`
	Result          *Parameters                  `json:"result,omitempty"`
	Reliability     ProcessReliability           `json:"reliability"`
	ExecutionPolicy ProcessDefinitionReliability `json:"execution_policy"`
	Version         int                          `json:"version"           validate:"gte=1"`
	CreatedAt       time.Time                    `json:"created_at"        validate:"required"`
	UpdatedAt       time.Time                    `json:"updated_at"        validate:"required,gtefield=CreatedAt"`
}

type ProcessStepOpt func(*ProcessStep)

func WithProcessStepVersion(value int) ProcessStepOpt {
	return func(p *ProcessStep) { p.Version = value }
}
func WithProcessStepCreatedAt(value time.Time) ProcessStepOpt {
	return func(p *ProcessStep) { p.CreatedAt = value }
}
func WithProcessStepUpdatedAt(value time.Time) ProcessStepOpt {
	return func(p *ProcessStep) { p.UpdatedAt = value }
}
func WithProcessStepExecutionPolicy(value ProcessDefinitionReliability) ProcessStepOpt {
	return func(p *ProcessStep) { p.ExecutionPolicy = value }
}

func WithNewProcessStepID() ProcessStepOpt {
	return func(model *ProcessStep) {
		model.ID = uuid.New()
	}
}

func WithProcessStepID(value uuid.UUID) ProcessStepOpt {
	return func(model *ProcessStep) {
		model.ID = value
	}
}

func WithProcessStepState(value ProcessStepState) ProcessStepOpt {
	return func(model *ProcessStep) {
		model.State = value
	}
}

func WithProcessStepProcessID(value uuid.UUID) ProcessStepOpt {
	return func(model *ProcessStep) {
		model.ProcessID = value
	}
}

func WithProcessStepProcessStepDefinitionName(value string) ProcessStepOpt {
	return func(model *ProcessStep) {
		model.ProcessStepDefinitionName = value
	}
}

func WithProcessStepType(value ProcessStepType) ProcessStepOpt {
	return func(model *ProcessStep) {
		model.Type = value
	}
}

func WithProcessStepParams(value *Parameters) ProcessStepOpt {
	return func(model *ProcessStep) {
		model.Params = value
	}
}

func WithProcessStepContext(value *Parameters) ProcessStepOpt {
	return func(model *ProcessStep) { model.Context = value }
}

func WithProcessStepResult(value *Parameters) ProcessStepOpt {
	return func(model *ProcessStep) {
		model.Result = value
	}
}

func WithProcessStepReliability(value ProcessReliability) ProcessStepOpt {
	return func(model *ProcessStep) {
		model.Reliability = value
	}
}

func NewProcessStep(opts ...ProcessStepOpt) (*ProcessStep, error) {
	model := newProcessStep(opts...)
	if err := model.Validate(); err != nil {
		return nil, err
	}
	if err := model.validateParams(); err != nil {
		return nil, err
	}

	return model, nil
}

func newProcessStep(opts ...ProcessStepOpt) *ProcessStep {
	now := time.Now().UTC()
	model := &ProcessStep{Version: 1, CreatedAt: now, UpdatedAt: now}
	for _, opt := range opts {
		opt(model)
	}

	return model
}

// Resolve materializes this step's parameters and context from its execution sources.
func (p *ProcessStep) Resolve(context *ProcessContext) error {
	params, err := p.resolveParameters(p.Params, context)
	if err != nil {
		return err
	}
	values, err := p.resolveParameters(p.Context, context)
	if err != nil {
		return err
	}
	p.Params = params
	p.Context = values

	return nil
}

// Validate checks the process step without modifying it.
func (p *ProcessStep) Validate() error {
	if _, err := validator.Struct(p); err != nil {
		return err
	}
	if p.State == ProcessStepStateFailed && p.Result.Len() == 0 {
		return ErrNoDetailsInResult
	}

	return nil
}

func (p *ProcessStep) IsFinalState() bool {
	return p.State == ProcessStepStateCompleted || p.State == ProcessStepStateFailed
}
func (p *ProcessStep) Acquire(timeout time.Duration) error {
	if p.IsFinalState() {
		return ErrProcessStepTerminal
	}
	p.Reliability.Executions++
	p.UpdatedAt = time.Now().UTC()
	next := p.UpdatedAt.Add(timeout)
	p.Reliability.ExecutedAt = &next

	return p.Validate()
}

// Retry records a failed attempt and schedules the next acquisition without consuming another attempt.
func (p *ProcessStep) Retry(result *Parameters) error {
	if p.IsFinalState() {
		return ErrProcessStepTerminal
	}
	p.Result = result
	p.UpdatedAt = time.Now().UTC()
	retryTimeout := p.ExecutionPolicy.RetryTimeout
	if retryTimeout == 0 {
		retryTimeout = time.Minute
	}
	next := p.UpdatedAt.Add(retryTimeout)
	p.Reliability.ExecutedAt = &next

	return p.Validate()
}

func (p *ProcessStep) Complete(result *Parameters) error {
	if p.IsFinalState() {
		return ErrProcessStepTerminal
	}
	if p.Type == ProcessStepTypeAICodingAgent {
		if err := validator.JSONMatchesSchema(p.Params.String("output_json"), result.String("output")); err != nil {
			return err
		}
	}
	p.State = ProcessStepStateCompleted
	p.Result = result
	p.finish()

	return p.Validate()
}
func (p *ProcessStep) Fail(result *Parameters) error {
	if p.IsFinalState() {
		return ErrProcessStepTerminal
	}
	p.State = ProcessStepStateFailed
	p.Result = result
	p.finish()

	return p.Validate()
}

func (p *ProcessStep) resolveParameters(parameters *Parameters, context *ProcessContext) (*Parameters, error) {
	if parameters == nil {
		return nil, nil
	}
	resolved, _ := NewParameters(nil)
	for name, declared := range parameters.Entries() {
		value, exists, err := p.resolvedValue(name, declared, context)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", name, err)
		}
		if !exists {
			continue
		}
		if value.Type != declared.Type || value.Value == nil || value.From != "" {
			return nil, fmt.Errorf("%w: %q requires a resolved %s value", ErrInvalidParameters, name, declared.Type)
		}
		if err := resolved.Set(name, value); err != nil {
			return nil, err
		}
	}

	return resolved, nil
}

func (p *ProcessStep) resolvedValue(
	name string, declared ParameterValue, context *ProcessContext,
) (ParameterValue, bool, error) {
	if declared.From != "" {
		value, err := context.Resolve(declared.From, declared.Type)

		return value, true, err
	}
	if declared.Value != nil {
		return declared, true, nil
	}
	if context != nil && context.Inputs.Has(name) {
		return context.Inputs.Get(name), true, nil
	}
	if declared.Required {
		return ParameterValue{}, false, fmt.Errorf("%w: %q is required", ErrInvalidParameters, name)
	}

	return ParameterValue{}, false, nil
}

func (*ProcessStep) validateResolved(parameters *Parameters) error {
	for name, value := range parameters.Entries() {
		if value.From != "" || value.Value == nil {
			return fmt.Errorf("%w: %q is unresolved", ErrInvalidParameters, name)
		}
	}

	return nil
}

func (p *ProcessStep) validateParams() error {
	if err := p.validateResolved(p.Params); err != nil {
		return err
	}
	if err := p.validateResolved(p.Context); err != nil {
		return err
	}
	if p.Type != ProcessStepTypeAICodingAgent {
		return nil
	}
	for name, typeName := range map[string]ParameterType{
		"prompt": ParameterTypeString, "working_directory": ParameterTypeFilePath, "output_json": ParameterTypeJSONSchema,
	} {
		if p.Params.Get(name).Type != typeName || strings.TrimSpace(p.Params.String(name)) == "" {
			return fmt.Errorf("%w: %q requires a nonempty %s", ErrInvalidParameters, name, typeName)
		}
	}
	for _, name := range []string{"model", "reasoning_effort"} {
		if !p.Params.Has(name) {
			continue
		}
		value := p.Params.Get(name)
		if value.Type != ParameterTypeString || strings.TrimSpace(value.String()) == "" {
			return fmt.Errorf("%w: %q must be a nonempty STRING", ErrInvalidParameters, name)
		}
	}
	for name := range p.Context.Entries() {
		if !contextParameterName.MatchString(name) {
			return fmt.Errorf("%w: context key %q must be an XML element name", ErrInvalidParameters, name)
		}
	}
	if p.State != ProcessStepStatePending {
		return nil
	}
	info, err := os.Stat(p.Params.String("working_directory"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("working_directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: working_directory must be a directory", ErrInvalidParameters)
	}

	return nil
}

func (p *ProcessStep) finish() {
	p.UpdatedAt = time.Now().UTC()
	p.Reliability.ExecutionTime = p.UpdatedAt.Sub(p.CreatedAt)
}

type ProcessReliability struct {
	Executions    int           `json:"executions,omitempty"     validate:"gte=0"`
	ExecutionTime time.Duration `json:"execution_time,omitempty" validate:"gte=0"`
	// ExecutedAt is the next eligible execution time; nil means never acquired.
	ExecutedAt *time.Time `json:"executed_at"`
}

type ProcessReliabilityOpt func(*ProcessReliability)

func WithProcessReliabilityExecutions(value int) ProcessReliabilityOpt {
	return func(model *ProcessReliability) {
		model.Executions = value
	}
}

func WithProcessReliabilityExecutionTime(value time.Duration) ProcessReliabilityOpt {
	return func(model *ProcessReliability) {
		model.ExecutionTime = value
	}
}

func WithProcessReliabilityExecutedAt(value time.Time) ProcessReliabilityOpt {
	return func(model *ProcessReliability) {
		model.ExecutedAt = &value
	}
}

func NewProcessReliability(opts ...ProcessReliabilityOpt) (*ProcessReliability, error) {
	model := &ProcessReliability{}
	for _, opt := range opts {
		opt(model)
	}

	return validator.Struct(model)
}
