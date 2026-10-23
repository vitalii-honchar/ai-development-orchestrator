package processstep_test

import (
	"context"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/worker/app/processstep"
)

const testStepType domain.ProcessStepType = "TEST_STEP_TYPE"

var _ processstep.ProcessStepRepository = (*repository)(nil)
var _ processstep.StepExecutor = strategy(nil)

type repository struct {
	records   []domain.ProcessStep
	updates   int
	updateErr error
}

func (r *repository) GetPending(context.Context) ([]domain.ProcessStep, error) { return r.records, nil }
func (r *repository) Update(ctx context.Context, p *domain.ProcessStep) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updates++
	p.Version++

	return nil
}

type strategy func(context.Context, *domain.ProcessStep) (*domain.Parameters, error)

func (s strategy) Execute(ctx context.Context, p *domain.ProcessStep) (*domain.Parameters, error) {
	return s(ctx, p)
}

func (strategy) Matches(_ context.Context, p *domain.ProcessStep) (bool, error) {
	return p.Type == testStepType, nil
}

func fixture() []domain.ProcessStep {
	now := time.Now().UTC()

	return []domain.ProcessStep{{ID: uuid.New(),
		ProcessID:                 uuid.New(),
		State:                     domain.ProcessStepStatePending,
		ProcessStepDefinitionName: "test",
		Type:                      testStepType,
		Version:                   1,
		CreatedAt:                 now,
		UpdatedAt:                 now,
		ExecutionPolicy:           domain.ProcessDefinitionReliability{MaxRetries: 2}}}
}
