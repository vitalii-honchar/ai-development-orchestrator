package processdefinition

import (
	"context"
	"go-software-factory/internal/core/domain"
	"uuid"
)

type GetProcessDefinitionUseCase struct {
	repository ProcessDefinitionRepository
}

func NewGetProcessDefinitionUseCase(
	repository ProcessDefinitionRepository,
) *GetProcessDefinitionUseCase {
	return &GetProcessDefinitionUseCase{repository: repository}
}

func (g *GetProcessDefinitionUseCase) GetByID(ctx context.Context, id uuid.UUID) (*domain.ProcessDefinition, error) {
	return g.repository.GetByID(ctx, id)
}

func (g *GetProcessDefinitionUseCase) GetAll(ctx context.Context) ([]domain.ProcessDefinition, error) {
	return g.repository.GetAll(ctx)
}
