package process_test

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/engine/app/process"
)

func TestFailProcessUseCaseMutatesAndReloadsProcess(t *testing.T) {
	t.Parallel()
	model := newTestProcess(t)
	cause := errors.New("worker exhausted retries")
	reloaded := newTestProcess(t, domain.WithProcessID(model.ID), domain.WithProcessVersion(2),
		domain.WithProcessState(domain.ProcessStateFailed),
		domain.WithProcessResult(testParameters(map[string]domain.ParameterValue{
			"error": {Type: domain.ParameterTypeString, Value: cause.Error()},
		})))
	writes := 0
	repository := &processRepositoryStub{
		update: func(_ context.Context, updated *domain.Process) error {
			writes++
			if updated != model || updated.State != domain.ProcessStateFailed ||
				updated.Result.Get("error").Type != domain.ParameterTypeString ||
				updated.Result.String("error") != cause.Error() ||
				updated.Reliability.ExecutionTime != updated.UpdatedAt.Sub(updated.CreatedAt) {
				t.Fatalf("failure did not mutate the supplied process correctly: %+v", updated)
			}

			return nil
		},
		getByID: func(_ context.Context, id uuid.UUID) (*domain.Process, error) {
			if writes != 1 || id != reloaded.ID {
				t.Fatal("failure reloaded before persistence or used a different ID")
			}

			return reloaded, nil
		},
	}
	failed, err := process.NewFailProcessUseCase(repository).Fail(t.Context(), model, cause)
	if err != nil || failed != reloaded || writes != 1 {
		t.Fatalf("failure result=%v err=%v writes=%d", failed, err, writes)
	}
}

func TestFailProcessUseCaseRejectsInvalidRequestsAndReturnsPersistenceErrors(t *testing.T) {
	t.Parallel()
	cause := errors.New("failure persistence unavailable")
	for _, tc := range []struct {
		name      string
		nilModel  bool
		nilCause  bool
		terminal  bool
		invalid   bool
		updateErr error
		reloadErr error
		wantErr   error
		writes    int
	}{
		{name: "nil process", nilModel: true},
		{name: "nil cause", nilCause: true},
		{name: "terminal process", terminal: true, wantErr: domain.ErrProcessTerminal},
		{name: "invalid model", invalid: true},
		{name: "update failure", updateErr: cause, wantErr: cause, writes: 1},
		{name: testReloadFailure, reloadErr: cause, wantErr: cause, writes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			model := newTestProcess(t)
			failure := errors.New("worker failure")
			switch {
			case tc.nilModel:
				model = nil
			case tc.terminal:
				model.State = domain.ProcessStateCompleted
			case tc.invalid:
				model.Version = 0
			}
			if tc.nilCause {
				failure = nil
			}
			writes := 0
			repository := &processRepositoryStub{
				update: func(context.Context, *domain.Process) error {
					writes++

					return tc.updateErr
				},
				getByID: func(context.Context, uuid.UUID) (*domain.Process, error) { return nil, tc.reloadErr },
			}
			failed, err := process.NewFailProcessUseCase(repository).Fail(t.Context(), model, failure)
			if failed != nil || err == nil || writes != tc.writes || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("failure result=%v err=%v writes=%d want %d", failed, err, writes, tc.writes)
			}
		})
	}
}
