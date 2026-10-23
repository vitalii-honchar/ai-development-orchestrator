package processstep

import (
	"context"
	"uuid"

	"go-software-factory/internal/core/domain"
)

type ProcessStepRepository interface {
	Save(context.Context, *domain.ProcessStep) error
	GetByID(context.Context, uuid.UUID) (*domain.ProcessStep, error)
	GetByProcessID(context.Context, uuid.UUID) ([]domain.ProcessStep, error)
	GetPending(context.Context) ([]domain.ProcessStep, error)
	Update(context.Context, *domain.ProcessStep) error
}
