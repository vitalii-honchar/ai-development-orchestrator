package process

import (
	"context"
	"slices"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"
)

type CreateProcessUseCase struct {
	repository  ProcessRepository
	definitions ProcessDefinitionRepository
}

func NewCreateProcessUseCase(
	repository ProcessRepository, definitions ProcessDefinitionRepository,
) *CreateProcessUseCase {
	return &CreateProcessUseCase{repository: repository, definitions: definitions}
}

func (u *CreateProcessUseCase) Create(
	ctx context.Context, definitionID uuid.UUID, inputs *domain.Parameters,
) (*domain.Process, error) {
	definition, err := u.definitions.GetByID(ctx, definitionID)
	if err != nil {
		return nil, err
	}
	if definition.State != domain.ProcessDefinitionStateActive {
		return nil, ErrProcessDefinitionInactive
	}
	var currentSteps []string
	for _, root := range definition.Graph.GetStartNodes() {
		currentSteps = append(currentSteps, root.Name)
	}
	slices.Sort(currentSteps)
	processContext, err := domain.NewProcessContext(inputs, nil)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	model, err := domain.NewProcess(
		domain.WithNewProcessID(), domain.WithProcessState(domain.ProcessStatePending),
		domain.WithProcessProcessDefinitionID(definitionID),
		domain.WithProcessContext(processContext), domain.WithProcessVersion(1),
		domain.WithProcessCurrentSteps(currentSteps),
		domain.WithProcessCreatedAt(now), domain.WithProcessUpdatedAt(now),
	)
	if err != nil {
		return nil, err
	}
	if err := u.repository.Save(ctx, model); err != nil {
		return nil, err
	}

	return u.repository.GetByID(ctx, model.ID)
}
