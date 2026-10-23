package processdefinition

import (
	"context"
	"go-software-factory/internal/core/domain"
)

type CreateProcessDefinitionUseCase struct {
	repository ProcessDefinitionRepository
}

func NewCreateProcessDefinitionUseCase(repository ProcessDefinitionRepository) *CreateProcessDefinitionUseCase {
	return &CreateProcessDefinitionUseCase{
		repository: repository,
	}
}

func (c *CreateProcessDefinitionUseCase) Create(
	ctx context.Context, processDefinition *domain.ProcessDefinition,
) (*domain.ProcessDefinition, error) {
	processDefinition.State = domain.ProcessDefinitionStateActive
	processDefinition.Version = 1
	if err := c.repository.Save(ctx, processDefinition); err != nil {
		return nil, err
	}

	return c.repository.GetByID(ctx, processDefinition.ID)
}
