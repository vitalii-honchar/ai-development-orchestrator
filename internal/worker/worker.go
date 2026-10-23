package worker

import (
	"errors"
	"time"

	coreprocessstep "go-software-factory/internal/core/app/processstep"
	"go-software-factory/internal/core/config"
	"go-software-factory/internal/core/scheduler"
	"go-software-factory/internal/core/service"
	"go-software-factory/internal/worker/app/processstep"
	"go-software-factory/internal/worker/app/stepexecutor"
	"go-software-factory/internal/worker/infra/codex"
	infrascheduler "go-software-factory/internal/worker/infra/scheduler"
)

var errRepositoryRequired = errors.New("worker process step repository is required")

type Worker struct {
	*service.Service
	ExecuteProcessStepsUseCase *processstep.ExecuteProcessStepsUseCase
}

type WorkerDependencies struct {
	ProcessStepRepository coreprocessstep.ProcessStepRepository
}

func NewWorker(dependencies *WorkerDependencies) (*Worker, error) {
	if dependencies == nil || dependencies.ProcessStepRepository == nil {
		return nil, errRepositoryRequired
	}
	sched, err := scheduler.NewScheduler()
	if err != nil {
		return nil, err
	}
	executor := processstep.NewExecuteProcessStepsUseCase(dependencies.ProcessStepRepository,
		[]processstep.StepExecutor{
			stepexecutor.NewAICodingStepExecutor(
				codex.NewAICodingExecutor(config.GetWithDefault("FACTORY_CODEX_BIN", "codex")),
			),
		})
	if err := sched.AtFixedRate(infrascheduler.NewProcessStepExecutorJob(executor)); err != nil {
		_ = sched.Shutdown()

		return nil, err
	}

	return &Worker{ExecuteProcessStepsUseCase: executor, Service: &service.Service{
		Name:            "worker",
		StartupTimeout:  10 * time.Second,
		ShutdownTimeout: 15 * time.Second,
		Components:      []service.Component{sched},
	}}, nil
}
