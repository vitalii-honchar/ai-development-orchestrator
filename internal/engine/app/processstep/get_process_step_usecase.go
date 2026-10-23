package processstep

import (
	"context"
	"uuid"

	"go-software-factory/internal/core/domain"
)

type processStepRepository interface {
	GetByProcessID(context.Context, uuid.UUID) ([]domain.ProcessStep, error)
}

type GetProcessStepUseCase struct {
	repository processStepRepository
}

func NewGetProcessStepUseCase(repository processStepRepository) *GetProcessStepUseCase {
	return &GetProcessStepUseCase{repository: repository}
}

func (u *GetProcessStepUseCase) GetByProcessID(ctx context.Context, id uuid.UUID) ([]domain.ProcessStep, error) {
	return u.repository.GetByProcessID(ctx, id)
}
