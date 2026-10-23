package process

import (
	"context"

	"go-software-factory/internal/core/domain"
)

type FailProcessUseCase struct{ repository ProcessRepository }

func NewFailProcessUseCase(repository ProcessRepository) *FailProcessUseCase {
	return &FailProcessUseCase{repository: repository}
}

func (u *FailProcessUseCase) Fail(ctx context.Context, model *domain.Process, cause error) (*domain.Process, error) {
	if model == nil || cause == nil {
		return nil, errProcessFailureRequired
	}
	result, err := domain.NewParameters(map[string]domain.ParameterValue{
		"error": {Type: domain.ParameterTypeString, Value: cause.Error()},
	})
	if err != nil {
		return nil, err
	}
	if err := model.Fail(result); err != nil {
		return nil, err
	}

	return persistUpdate(ctx, u.repository, model)
}
