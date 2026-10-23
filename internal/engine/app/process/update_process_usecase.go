package process

import (
	"context"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"
)

type UpdateProcessUseCase struct{ repository ProcessRepository }

type ProcessUpdateParams struct {
	Context      *domain.ProcessContext
	Result       *domain.Parameters
	Reliability  domain.ProcessReliability
	CurrentSteps []string
	Version      int
}

func NewUpdateProcessUseCase(repository ProcessRepository) *UpdateProcessUseCase {
	return &UpdateProcessUseCase{repository: repository}
}

// Update replaces the editable fields using the version originally read by the caller.
func (u *UpdateProcessUseCase) Update(
	ctx context.Context, id uuid.UUID, params *ProcessUpdateParams,
) (*domain.Process, error) {
	if params == nil {
		return nil, errProcessChangesRequired
	}
	existing, err := u.repository.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing.Version != params.Version {
		return nil, domain.ErrProcessVersionConflict
	}
	if existing.IsFinalState() {
		return nil, domain.ErrProcessTerminal
	}
	existing.Context = params.Context
	existing.Result = params.Result
	existing.Reliability = params.Reliability
	existing.CurrentSteps = params.CurrentSteps
	existing.UpdatedAt = time.Now().UTC()
	if err := existing.Validate(); err != nil {
		return nil, err
	}

	return persistUpdate(ctx, u.repository, existing)
}
