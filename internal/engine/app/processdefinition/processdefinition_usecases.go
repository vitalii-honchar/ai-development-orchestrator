package processdefinition

import (
	"context"
	"go-software-factory/internal/core/domain"
	"uuid"
)

type ProcessDefinitionRepository interface {
	GetByID(ctx context.Context, ID uuid.UUID) (*domain.ProcessDefinition, error)
	Save(ctx context.Context, processDefinition *domain.ProcessDefinition) error
	Update(ctx context.Context, processDefinition *domain.ProcessDefinition) error
	Delete(ctx context.Context, id uuid.UUID) error
	GetAll(ctx context.Context) ([]domain.ProcessDefinition, error)
}
