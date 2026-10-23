package scheduler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/worker/app/processstep"
	"go-software-factory/internal/worker/infra/scheduler"
)

type failingRepository struct{ cause error }

func (r failingRepository) GetPending(context.Context) ([]domain.ProcessStep, error) {
	return nil, r.cause
}
func (failingRepository) Update(context.Context, *domain.ProcessStep) error { return nil }
func TestProcessStepExecutorJob(t *testing.T) {
	t.Parallel()
	cause := errors.New("repository unavailable")
	job := scheduler.NewProcessStepExecutorJob(processstep.NewExecuteProcessStepsUseCase(failingRepository{cause}, nil))
	if job.Rate() != time.Second || job.Timeout() != time.Hour || job.Name() != "process-step-executor" {
		t.Fatal("unexpected job settings")
	}
	if err := job.Run(t.Context()); !errors.Is(err, cause) {
		t.Fatal(err)
	}
}
