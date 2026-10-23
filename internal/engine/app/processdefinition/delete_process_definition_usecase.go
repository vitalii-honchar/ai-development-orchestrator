package processdefinition

import (
	"context"
	"uuid"
)

type DeleteProcessDefinitionUseCase struct {
	repository ProcessDefinitionRepository
}

func NewDeleteProcessDefinitionUseCase(repository ProcessDefinitionRepository) *DeleteProcessDefinitionUseCase {
	return &DeleteProcessDefinitionUseCase{repository: repository}
}

func (u *DeleteProcessDefinitionUseCase) Delete(ctx context.Context, id uuid.UUID) error {
	return u.repository.Delete(ctx, id)
}
