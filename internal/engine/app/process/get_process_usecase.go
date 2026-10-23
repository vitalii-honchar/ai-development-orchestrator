package process

import (
	"context"
	"uuid"

	"go-software-factory/internal/core/domain"
)

type GetProcessUseCase struct {
	repository ProcessRepository
}

func NewGetProcessUseCase(repository ProcessRepository) *GetProcessUseCase {
	return &GetProcessUseCase{repository: repository}
}

func (u *GetProcessUseCase) GetByID(ctx context.Context, id uuid.UUID) (*domain.Process, error) {
	return u.repository.GetByID(ctx, id)
}

func (u *GetProcessUseCase) GetAll(ctx context.Context) ([]domain.Process, error) {
	return u.repository.GetAll(ctx)
}
