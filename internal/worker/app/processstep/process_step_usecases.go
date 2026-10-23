package processstep

import (
	"context"
	"errors"

	"go-software-factory/internal/core/domain"
)

var (
	ErrStepExecutorNotFound = errors.New("step executor not found")
	errStepExecutionBudget  = errors.New("step execution budget exhausted")
	errStepExecutorPanic    = errors.New("step executor panicked")
)

type ProcessStepRepository interface {
	GetPending(context.Context) ([]domain.ProcessStep, error)
	Update(context.Context, *domain.ProcessStep) error
}

type StepExecutor interface {
	Execute(context.Context, *domain.ProcessStep) (*domain.Parameters, error)
	Matches(context.Context, *domain.ProcessStep) (bool, error)
}
