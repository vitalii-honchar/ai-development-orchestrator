package scheduler

import (
	"context"
	"time"

	"go-software-factory/internal/worker/app/processstep"
)

type ProcessStepExecutorJob struct {
	executor *processstep.ExecuteProcessStepsUseCase
}

func NewProcessStepExecutorJob(executor *processstep.ExecuteProcessStepsUseCase) *ProcessStepExecutorJob {
	return &ProcessStepExecutorJob{executor: executor}
}

func (*ProcessStepExecutorJob) Name() string {
	return "process-step-executor"
}

func (*ProcessStepExecutorJob) Rate() time.Duration {
	return time.Second
}

func (*ProcessStepExecutorJob) Timeout() time.Duration {
	return time.Hour
}

func (j *ProcessStepExecutorJob) Run(ctx context.Context) error {
	return j.executor.Execute(ctx)
}
