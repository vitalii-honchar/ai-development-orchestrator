package process_test

import (
	"context"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/engine/app/process"
)

const (
	testRoot            = "root"
	testJoin            = "join"
	testTerminal        = "end"
	testOutput          = "output"
	testUnknown         = "unknown"
	testReloadFailure   = "reload failure"
	testCompletion      = "completion"
	testDefinitionStage = "definition"
	testReloadStage     = "reload"
	testStepReadStage   = "steps"
	testStepSaveStage   = "step save"
	testProgressStage   = "progress"
	testFailureStage    = "failure"
	testBreadthTerminal = "z-terminal"
)

type processRepositoryStub struct {
	getAll     func(context.Context) ([]domain.Process, error)
	getPending func(context.Context) ([]domain.Process, error)
	getByID    func(context.Context, uuid.UUID) (*domain.Process, error)
	save       func(context.Context, *domain.Process) error
	update     func(context.Context, *domain.Process) error
}

func (r *processRepositoryStub) GetAll(ctx context.Context) ([]domain.Process, error) {
	return r.getAll(ctx)
}

func (r *processRepositoryStub) GetPending(ctx context.Context) ([]domain.Process, error) {
	return r.getPending(ctx)
}

func (r *processRepositoryStub) GetByID(ctx context.Context, id uuid.UUID) (*domain.Process, error) {
	return r.getByID(ctx, id)
}

func (r *processRepositoryStub) Save(ctx context.Context, model *domain.Process) error {
	return r.save(ctx, model)
}

func (r *processRepositoryStub) Update(ctx context.Context, model *domain.Process) error {
	return r.update(ctx, model)
}

type definitionRepositoryStub struct {
	getByID func(context.Context, uuid.UUID) (*domain.ProcessDefinition, error)
}

func (r definitionRepositoryStub) GetByID(ctx context.Context, id uuid.UUID) (*domain.ProcessDefinition, error) {
	return r.getByID(ctx, id)
}

type stepRepositoryStub struct {
	getByProcessID func(context.Context, uuid.UUID) ([]domain.ProcessStep, error)
	save           func(context.Context, *domain.ProcessStep) error
}

func (r *stepRepositoryStub) GetByProcessID(ctx context.Context, id uuid.UUID) ([]domain.ProcessStep, error) {
	return r.getByProcessID(ctx, id)
}

func (r *stepRepositoryStub) Save(ctx context.Context, step *domain.ProcessStep) error {
	return r.save(ctx, step)
}

func newTestProcess(t *testing.T, opts ...domain.ProcessOpt) *domain.Process {
	t.Helper()
	created := time.Now().UTC().Add(-time.Hour)
	model, err := domain.NewProcess(append([]domain.ProcessOpt{
		domain.WithNewProcessID(), domain.WithProcessProcessDefinitionID(uuid.New()),
		domain.WithProcessState(domain.ProcessStatePending), domain.WithProcessVersion(1),
		domain.WithProcessCreatedAt(created), domain.WithProcessUpdatedAt(created),
	}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}

	return model
}

func newTestDefinition(t *testing.T, names []string, edges [][2]string, terminals ...string) *domain.ProcessDefinition {
	t.Helper()
	definition, err := domain.NewProcessDefinition(
		domain.WithNewProcessDefinitionID(), domain.WithProcessDefinitionState(domain.ProcessDefinitionStateActive),
		domain.WithProcessDefinitionVersion(1), domain.WithProcessDefinitionCreatedAt(time.Now().UTC()),
		domain.WithProcessDefinitionUpdatedAt(time.Now().UTC()),
		domain.WithProcessDefinitionReliability(domain.ProcessDefinitionReliability{MaxRetries: 5}),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		node, err := domain.NewProcessStepDefinition(
			domain.WithProcessStepDefinitionName(name),
			domain.WithProcessStepDefinitionType("worker."+domain.ProcessStepType(name)),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := definition.Graph.AddNode(node); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range edges {
		if err := definition.Graph.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range terminals {
		definition.Graph.Nodes[name].Type = domain.ProcessStepTypeTerminal
	}

	return definition
}

type executionFixture struct {
	definition  *domain.ProcessDefinition
	acquired    *domain.Process
	pending     *domain.Process
	processes   *processRepositoryStub
	steps       *stepRepositoryStub
	definitions definitionRepositoryStub
	records     []domain.Process
	queued      []domain.ProcessStep
	updatedIDs  []uuid.UUID
	stepStates  map[string]domain.ProcessStepState
}

func newExecutionFixture(t *testing.T, definition *domain.ProcessDefinition, current []string) *executionFixture {
	t.Helper()
	fixture := &executionFixture{definition: definition, stepStates: make(map[string]domain.ProcessStepState)}
	now := time.Now().UTC().Add(-time.Hour)
	fixture.records = []domain.Process{{ID: uuid.New(),
		ProcessDefinitionID: definition.ID,
		State:               domain.ProcessStatePending,
		Context:             &domain.ProcessContext{},
		Version:             1,
		CreatedAt:           now,
		UpdatedAt:           now,
		CurrentSteps:        current}}
	fixture.pending = &fixture.records[0]
	fixture.acquired = fixture.pending
	fixture.processes = &processRepositoryStub{
		getPending: func(context.Context) ([]domain.Process, error) {
			return fixture.records, nil
		},
		getByID: func(_ context.Context, id uuid.UUID) (*domain.Process, error) {
			if id != fixture.acquired.ID {
				t.Fatalf("reload process ID=%s want %s", id, fixture.acquired.ID)
			}

			return fixture.acquired, nil
		},
		update: func(_ context.Context, model *domain.Process) error {
			if err := model.Validate(); err != nil {
				t.Fatalf("use case tried to persist an invalid model: %v", err)
			}
			fixture.updatedIDs = append(fixture.updatedIDs, model.ID)
			model.Version++

			return nil
		},
	}
	fixture.definitions = definitionRepositoryStub{getByID: func(_ context.Context, id uuid.UUID) (
		*domain.ProcessDefinition, error,
	) {
		if id != definition.ID {
			t.Fatalf("definition ID=%s want %s", id, definition.ID)
		}

		return definition, nil
	}}
	fixture.steps = &stepRepositoryStub{
		getByProcessID: func(_ context.Context, id uuid.UUID) ([]domain.ProcessStep, error) {
			if id != fixture.acquired.ID {
				t.Fatalf("step query process ID=%s want %s", id, fixture.acquired.ID)
			}
			steps := make([]domain.ProcessStep, 0, len(fixture.stepStates))
			for name, state := range fixture.stepStates {
				steps = append(steps, domain.ProcessStep{
					ID: uuid.New(), ProcessID: id, ProcessStepDefinitionName: name, State: state,
					Type: "worker." + domain.ProcessStepType(name),
					Result: testParameters(map[string]domain.ParameterValue{
						"error": {Type: domain.ParameterTypeString, Value: "worker failure"},
					}),
				})
			}

			return steps, nil
		},
		save: func(_ context.Context, step *domain.ProcessStep) error {
			if err := step.Validate(); err != nil {
				t.Fatalf("use case queued an invalid step: %v", err)
			}
			fixture.queued = append(fixture.queued, domain.ProcessStep{ID: step.ID, ProcessID: step.ProcessID,
				ProcessStepDefinitionName: step.ProcessStepDefinitionName,
				State:                     step.State,
				Type:                      step.Type,
				Params:                    step.Params,
				ExecutionPolicy:           step.ExecutionPolicy})

			return nil
		},
	}

	return fixture
}

func (f *executionFixture) executor() *process.ExecuteProcessesUseCase {
	return process.NewExecuteProcessesUseCase(f.processes, f.steps, f.definitions)
}

func batchExecutor(fixtures ...*executionFixture) *process.ExecuteProcessesUseCase {
	byProcess := make(map[uuid.UUID]*executionFixture, len(fixtures))
	byDefinition := make(map[uuid.UUID]*executionFixture, len(fixtures))
	for _, fixture := range fixtures {
		byProcess[fixture.acquired.ID] = fixture
		byDefinition[fixture.definition.ID] = fixture
	}
	processes := &processRepositoryStub{
		getPending: func(ctx context.Context) ([]domain.Process, error) {
			pending := make([]domain.Process, 0, len(fixtures))
			for _, fixture := range fixtures {
				models, err := fixture.processes.GetPending(ctx)
				if err != nil {
					return nil, err
				}
				pending = append(pending, models...)
			}

			for i, fixture := range fixtures {
				fixture.pending = &pending[i]
				fixture.acquired = &pending[i]
			}

			return pending, nil
		},
		getByID: func(ctx context.Context, id uuid.UUID) (*domain.Process, error) {
			return byProcess[id].processes.GetByID(ctx, id)
		},
		update: func(ctx context.Context, model *domain.Process) error {
			return byProcess[model.ID].processes.Update(ctx, model)
		},
	}
	steps := &stepRepositoryStub{
		getByProcessID: func(ctx context.Context, id uuid.UUID) ([]domain.ProcessStep, error) {
			return byProcess[id].steps.GetByProcessID(ctx, id)
		},
		save: func(ctx context.Context, step *domain.ProcessStep) error {
			return byProcess[step.ProcessID].steps.Save(ctx, step)
		},
	}
	definitions := definitionRepositoryStub{getByID: func(ctx context.Context, id uuid.UUID) (
		*domain.ProcessDefinition, error,
	) {
		return byDefinition[id].definitions.GetByID(ctx, id)
	}}

	return process.NewExecuteProcessesUseCase(processes, steps, definitions)
}
