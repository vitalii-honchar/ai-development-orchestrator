package process_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/engine/app/process"
)

func TestCreateProcessUseCaseInitializesProcess(t *testing.T) {
	t.Parallel()
	definition := newTestDefinition(t, []string{"z", "a", "b"}, [][2]string{{"a", "b"}})
	inputs := testParameters(map[string]domain.ParameterValue{
		testOutput: {Type: domain.ParameterTypeString, Value: "requested input"},
	})
	var saved *domain.Process
	repository := &processRepositoryStub{
		save: func(_ context.Context, model *domain.Process) error {
			saved = model

			return nil
		},
		getByID: func(_ context.Context, id uuid.UUID) (*domain.Process, error) {
			if saved == nil || id != saved.ID {
				t.Fatal("creation reloaded before saving or used a different ID")
			}

			return saved, nil
		},
	}
	definitions := definitionRepositoryStub{getByID: func(_ context.Context, id uuid.UUID) (
		*domain.ProcessDefinition, error,
	) {
		if id != definition.ID {
			t.Fatal("creation loaded a different definition")
		}

		return definition, nil
	}}
	before := time.Now().UTC()
	model, err := process.NewCreateProcessUseCase(repository, definitions).Create(t.Context(), definition.ID, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if model != saved || model.ID == uuid.Nil() || model.ProcessDefinitionID != definition.ID ||
		model.State != domain.ProcessStatePending || model.Version != 1 || model.Reliability.Executions != 0 ||
		model.Reliability.ExecutedAt != nil || !slices.Equal(model.CurrentSteps, []string{"a", "z"}) ||
		!reflect.DeepEqual(model.Context.Inputs, inputs) || !model.CreatedAt.Equal(model.UpdatedAt) ||
		model.CreatedAt.Before(before) || model.CreatedAt.After(time.Now().UTC()) {
		t.Fatalf("incorrect initial process: %+v", model)
	}
}

func TestCreateProcessUseCaseRejectsInvalidRequestsAndReturnsDependencyErrors(t *testing.T) {
	t.Parallel()
	cause := errors.New("creation dependency unavailable")
	for _, tc := range []struct {
		name      string
		state     domain.ProcessDefinitionState
		inputs    *domain.Parameters
		readErr   error
		saveErr   error
		reloadErr error
		wantSaved bool
	}{
		{name: "draft", state: domain.ProcessDefinitionStateDraft},
		{name: "archived", state: domain.ProcessDefinitionStateArchived},
		{name: "missing definition", readErr: domain.ErrProcessDefinitionNotFound},
		{name: "definition read failure", readErr: cause},
		{name: "save failure", state: domain.ProcessDefinitionStateActive, saveErr: cause, wantSaved: true},
		{name: testReloadFailure, state: domain.ProcessDefinitionStateActive, reloadErr: cause, wantSaved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			definition := newTestDefinition(t, []string{testRoot}, nil)
			definition.State = tc.state
			saved := false
			repository := &processRepositoryStub{
				save: func(context.Context, *domain.Process) error {
					saved = true

					return tc.saveErr
				},
				getByID: func(context.Context, uuid.UUID) (*domain.Process, error) { return nil, tc.reloadErr },
			}
			definitions := definitionRepositoryStub{getByID: func(context.Context, uuid.UUID) (
				*domain.ProcessDefinition, error,
			) {
				return definition, tc.readErr
			}}
			model, err := process.NewCreateProcessUseCase(repository, definitions).Create(t.Context(), definition.ID, tc.inputs)
			if model != nil || err == nil || saved != tc.wantSaved {
				t.Fatalf("rejected creation model=%v err=%v saved=%v want saved=%v", model, err, saved, tc.wantSaved)
			}
			for _, expected := range []error{tc.readErr, tc.saveErr, tc.reloadErr} {
				if expected != nil && !errors.Is(err, expected) {
					t.Fatalf("creation lost dependency cause: %v", err)
				}
			}
		})
	}
}
