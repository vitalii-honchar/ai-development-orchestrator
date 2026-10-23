package processdefinition

import (
	"context"
	"errors"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"
)

var errProcessDefinitionChangesRequired = errors.New("process definition changes are required")

type UpdateProcessDefinitionUseCase struct {
	repository ProcessDefinitionRepository
}

func NewUpdateProcessDefinitionUseCase(repository ProcessDefinitionRepository) *UpdateProcessDefinitionUseCase {
	return &UpdateProcessDefinitionUseCase{repository: repository}
}

// Update expects validated changes with the version originally read by the caller.
func (u *UpdateProcessDefinitionUseCase) Update(
	ctx context.Context, id uuid.UUID, changes *domain.ProcessDefinition,
) (*domain.ProcessDefinition, error) {
	if changes == nil {
		return nil, errProcessDefinitionChangesRequired
	}

	existing, err := u.repository.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if changes.Version != existing.Version {
		return nil, domain.ErrProcessDefinitionVersionConflict
	}

	changes.ID = existing.ID
	changes.State = existing.State
	changes.CreatedAt = existing.CreatedAt
	changes.UpdatedAt = time.Now().UTC()
	if err := u.repository.Update(ctx, changes); err != nil {
		return nil, err
	}

	return u.repository.GetByID(ctx, id)
}
