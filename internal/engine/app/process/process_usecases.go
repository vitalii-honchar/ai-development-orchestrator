package process

import (
	"context"
	"errors"
	"uuid"

	"go-software-factory/internal/core/domain"
)

var (
	errProcessRequired           = errors.New("process is required")
	errProcessChangesRequired    = errors.New("process changes are required")
	errProcessFailureRequired    = errors.New("process and failure cause are required")
	ErrProcessDefinitionInactive = errors.New("process definition must be ACTIVE")
	errProcessExecutionBudget    = errors.New("process execution budget exhausted")
	errProcessGraphEmpty         = errors.New("process graph must be nonempty")
	errProcessStepFailed         = errors.New("process step failed")
	errProcessStepMissing        = errors.New("current process step is missing from the graph")
)

type ProcessRepository interface {
	GetByID(context.Context, uuid.UUID) (*domain.Process, error)
	GetAll(context.Context) ([]domain.Process, error)
	Save(context.Context, *domain.Process) error
	Update(context.Context, *domain.Process) error
	GetPending(context.Context) ([]domain.Process, error)
}

type ProcessDefinitionRepository interface {
	GetByID(context.Context, uuid.UUID) (*domain.ProcessDefinition, error)
}

type ProcessStepRepository interface {
	Save(context.Context, *domain.ProcessStep) error
	GetByProcessID(context.Context, uuid.UUID) ([]domain.ProcessStep, error)
}

func persistUpdate(ctx context.Context, repository ProcessRepository, p *domain.Process) (*domain.Process, error) {
	if err := repository.Update(ctx, p); err != nil {
		return nil, err
	}

	return repository.GetByID(ctx, p.ID)
}
