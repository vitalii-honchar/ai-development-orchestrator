package processdefinition

import (
	"context"
	"uuid"

	"go-software-factory/internal/core/domain"
)

type getProcessDefinitionUseCase interface {
	GetByID(ctx context.Context, id uuid.UUID) (*domain.ProcessDefinition, error)
	GetAll(ctx context.Context) ([]domain.ProcessDefinition, error)
}

type createProcessDefinitionUseCase interface {
	Create(ctx context.Context, definition *domain.ProcessDefinition) (*domain.ProcessDefinition, error)
}

type updateProcessDefinitionUseCase interface {
	Update(ctx context.Context, id uuid.UUID, changes *domain.ProcessDefinition) (*domain.ProcessDefinition, error)
}

const groupProcessDefinitions = "process-definitions"

const (
	templateParamProcessDefinitions = "ProcessDefinitions"
)
