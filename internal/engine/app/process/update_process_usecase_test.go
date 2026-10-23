package process_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/engine/app/process"
)

func TestUpdateProcessUseCaseAppliesOnlyEditableFields(t *testing.T) {
	t.Parallel()
	existing := newTestProcess(t)
	id, definitionID, createdAt := existing.ID, existing.ProcessDefinitionID, existing.CreatedAt
	next := time.Now().UTC().Add(time.Minute)
	params := &process.ProcessUpdateParams{
		Version: 1, CurrentSteps: []string{"a"},
		Context: &domain.ProcessContext{Inputs: testParameters(map[string]domain.ParameterValue{
			testOutput: {Type: domain.ParameterTypeString, Value: "new input"},
		})},
		Result: testParameters(map[string]domain.ParameterValue{
			testOutput: {Type: domain.ParameterTypeString, Value: "partial result"},
		}),
		Reliability: domain.ProcessReliability{Executions: 2, ExecutionTime: time.Second, ExecutedAt: &next},
	}
	reloaded := newTestProcess(t, domain.WithProcessID(id), domain.WithProcessVersion(2))
	reads, writes := 0, 0
	repository := &processRepositoryStub{
		getByID: func(_ context.Context, requested uuid.UUID) (*domain.Process, error) {
			if requested != id {
				t.Fatal("update loaded a different process")
			}
			reads++
			if reads == 1 {
				return existing, nil
			}

			return reloaded, nil
		},
		update: func(_ context.Context, model *domain.Process) error {
			writes++
			if model != existing || model.ID != id || model.ProcessDefinitionID != definitionID ||
				model.State != domain.ProcessStatePending || !model.CreatedAt.Equal(createdAt) || model.Version != 1 ||
				!reflect.DeepEqual(model.Context, params.Context) || !reflect.DeepEqual(model.Result, params.Result) ||
				!reflect.DeepEqual(model.CurrentSteps, params.CurrentSteps) ||
				!reflect.DeepEqual(model.Reliability, params.Reliability) {
				t.Fatalf("update changed protected fields or omitted editable fields: %+v", model)
			}

			return nil
		},
	}
	before := time.Now().UTC()
	updated, err := process.NewUpdateProcessUseCase(repository).Update(t.Context(), id, params)
	if err != nil || updated != reloaded || reads != 2 || writes != 1 ||
		existing.UpdatedAt.Before(before) || existing.UpdatedAt.After(time.Now().UTC()) {
		t.Fatalf("update result=%v err=%v reads=%d writes=%d", updated, err, reads, writes)
	}
}

func TestUpdateProcessUseCaseRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		state  domain.ProcessState
		params *process.ProcessUpdateParams
		want   error
	}{
		{name: "nil params"},
		{name: "stale version", params: &process.ProcessUpdateParams{Version: 2}, want: domain.ErrProcessVersionConflict},
		{
			name: "terminal", state: domain.ProcessStateCompleted,
			params: &process.ProcessUpdateParams{Version: 1}, want: domain.ErrProcessTerminal,
		},
		{name: "invalid current step", params: &process.ProcessUpdateParams{Version: 1, CurrentSteps: []string{" "}}},
		{name: "invalid reliability", params: &process.ProcessUpdateParams{
			Version: 1, Reliability: domain.ProcessReliability{Executions: -1},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			existing := newTestProcess(t)
			if tc.state != "" {
				existing.State = tc.state
			}
			writes := 0
			repository := &processRepositoryStub{
				getByID: func(context.Context, uuid.UUID) (*domain.Process, error) { return existing, nil },
				update: func(context.Context, *domain.Process) error {
					writes++

					return nil
				},
			}
			result, err := process.NewUpdateProcessUseCase(repository).Update(t.Context(), existing.ID, tc.params)
			if result != nil || err == nil || writes != 0 || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("invalid update result=%v err=%v writes=%d", result, err, writes)
			}
		})
	}
}

func TestUpdateProcessUseCaseReturnsRepositoryReadError(t *testing.T) {
	t.Parallel()
	repository := &processRepositoryStub{getByID: func(context.Context, uuid.UUID) (*domain.Process, error) {
		return nil, domain.ErrProcessNotFound
	}}
	result, err := process.NewUpdateProcessUseCase(repository).Update(
		t.Context(), uuid.New(), &process.ProcessUpdateParams{Version: 1},
	)
	if result != nil || !errors.Is(err, domain.ErrProcessNotFound) {
		t.Fatalf("missing process update result=%v err=%v", result, err)
	}
}
