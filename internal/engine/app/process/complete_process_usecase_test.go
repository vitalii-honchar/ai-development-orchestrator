package process_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"uuid"

	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/engine/app/process"
)

func TestCompleteProcessUseCaseMutatesAndReloadsProcess(t *testing.T) {
	t.Parallel()
	model := newTestProcess(t)
	result := testParameters(map[string]domain.ParameterValue{
		testOutput: {Type: domain.ParameterTypeString, Value: "finished"},
	})
	reloaded := newTestProcess(t, domain.WithProcessID(model.ID), domain.WithProcessVersion(2),
		domain.WithProcessState(domain.ProcessStateCompleted), domain.WithProcessResult(result))
	writes := 0
	repository := &processRepositoryStub{
		update: func(_ context.Context, updated *domain.Process) error {
			writes++
			if updated != model || updated.State != domain.ProcessStateCompleted || !reflect.DeepEqual(updated.Result, result) ||
				updated.Reliability.ExecutionTime != updated.UpdatedAt.Sub(updated.CreatedAt) {
				t.Fatalf("completion did not mutate the supplied process correctly: %+v", updated)
			}

			return nil
		},
		getByID: func(_ context.Context, id uuid.UUID) (*domain.Process, error) {
			if writes != 1 || id != reloaded.ID {
				t.Fatal("completion reloaded before persistence or used a different ID")
			}

			return reloaded, nil
		},
	}
	completed, err := process.NewCompleteProcessUseCase(repository).Complete(t.Context(), model, result)
	if err != nil || completed != reloaded || writes != 1 {
		t.Fatalf("completion result=%v err=%v writes=%d", completed, err, writes)
	}
}

func TestCompleteProcessUseCaseRejectsInvalidRequestsAndReturnsPersistenceErrors(t *testing.T) {
	t.Parallel()
	cause := errors.New("completion persistence unavailable")
	for _, tc := range []struct {
		name      string
		nilModel  bool
		terminal  bool
		result    *domain.Parameters
		updateErr error
		reloadErr error
		wantErr   error
		writes    int
	}{
		{name: "nil process", nilModel: true},
		{name: "terminal process", terminal: true, wantErr: domain.ErrProcessTerminal},
		{name: "update failure", updateErr: cause, wantErr: cause, writes: 1},
		{name: testReloadFailure, reloadErr: cause, wantErr: cause, writes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			model := newTestProcess(t)
			if tc.nilModel {
				model = nil
			} else if tc.terminal {
				model.State = domain.ProcessStateCompleted
			}
			writes := 0
			repository := &processRepositoryStub{
				update: func(context.Context, *domain.Process) error {
					writes++

					return tc.updateErr
				},
				getByID: func(context.Context, uuid.UUID) (*domain.Process, error) { return nil, tc.reloadErr },
			}
			completed, err := process.NewCompleteProcessUseCase(repository).Complete(t.Context(), model, tc.result)
			if completed != nil || err == nil || writes != tc.writes || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("completion result=%v err=%v writes=%d want %d", completed, err, writes, tc.writes)
			}
		})
	}
}

func testParameters(values map[string]domain.ParameterValue) *domain.Parameters {
	params, err := domain.NewParameters(values)
	if err != nil {
		panic(err)
	}

	return params
}
