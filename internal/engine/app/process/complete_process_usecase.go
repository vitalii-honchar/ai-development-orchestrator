package process

import (
	"context"

	"go-software-factory/internal/core/domain"
)

type CompleteProcessUseCase struct{ repository ProcessRepository }

func NewCompleteProcessUseCase(repository ProcessRepository) *CompleteProcessUseCase {
	return &CompleteProcessUseCase{repository: repository}
}

func (u *CompleteProcessUseCase) Complete(
	ctx context.Context, model *domain.Process, result *domain.Parameters,
) (*domain.Process, error) {
	if model == nil {
		return nil, errProcessRequired
	}
	if err := model.Complete(result); err != nil {
		return nil, err
	}

	return persistUpdate(ctx, u.repository, model)
}
