package scheduler

import (
	"context"
	"time"

	"go-software-factory/internal/engine/app/process"
)

type ProcessExecutorJob struct {
	executor *process.ExecuteProcessesUseCase
	rate     time.Duration
	timeout  time.Duration
}

func NewProcessExecutorJob(executor *process.ExecuteProcessesUseCase, rate, timeout time.Duration) *ProcessExecutorJob {
	return &ProcessExecutorJob{executor: executor, rate: rate, timeout: timeout}
}

func (j *ProcessExecutorJob) Name() string {
	return "process-executor"
}

func (j *ProcessExecutorJob) Rate() time.Duration {
	return j.rate
}

func (j *ProcessExecutorJob) Timeout() time.Duration {
	return j.timeout
}

func (j *ProcessExecutorJob) Run(ctx context.Context) error {
	return j.executor.Execute(ctx)
}
