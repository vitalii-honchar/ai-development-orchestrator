package engine

import (
	"go-software-factory/internal/core/app/processstep"
	"go-software-factory/internal/core/scheduler"
	"go-software-factory/internal/core/service"
	"go-software-factory/internal/engine/app/auth"
	"go-software-factory/internal/engine/app/process"
	"go-software-factory/internal/engine/app/processdefinition"
	engineprocessstep "go-software-factory/internal/engine/app/processstep"
	"go-software-factory/internal/engine/config"
	infraauth "go-software-factory/internal/engine/infra/auth"
	infrascheduler "go-software-factory/internal/engine/infra/scheduler"
	"go-software-factory/internal/engine/infra/storage"
)

type Engine struct {
	ProcessStepRepository processstep.ProcessStepRepository
	*service.Service
	LoginUseCase           *auth.LoginUseCase
	CreateProcessUseCase   *process.CreateProcessUseCase
	UpdateProcessUseCase   *process.UpdateProcessUseCase
	CompleteProcessUseCase *process.CompleteProcessUseCase
	FailProcessUseCase     *process.FailProcessUseCase
	GetProcessUseCase      *process.GetProcessUseCase
	GetProcessStepUseCase  *engineprocessstep.GetProcessStepUseCase

	GetProcessDefinitionUseCase    *processdefinition.GetProcessDefinitionUseCase
	CreateProcessDefinitionUseCase *processdefinition.CreateProcessDefinitionUseCase
	UpdateProcessDefinitionUseCase *processdefinition.UpdateProcessDefinitionUseCase
	DeleteProcessDefinitionUseCase *processdefinition.DeleteProcessDefinitionUseCase
}

func NewEngine() (*Engine, error) {
	cfg, err := config.FromEnv()
	if err != nil {
		return nil, err
	}

	stg, err := storage.NewStorage(cfg.Storage)
	if err != nil {
		return nil, err
	}

	sched, err := scheduler.NewScheduler()
	if err != nil {
		return nil, err
	}
	definitions := stg.ProcessDefinitionRepository
	executor := process.NewExecuteProcessesUseCase(
		stg.ProcessRepository,
		stg.ProcessStepRepository,
		definitions,
	)
	if err := sched.AtFixedRate(infrascheduler.NewProcessExecutorJob(
		executor,
		cfg.ProcessExecutorRate,
		cfg.ProcessExecutorTimeout,
	)); err != nil {
		_ = sched.Shutdown()

		return nil, err
	}

	return &Engine{
		ProcessStepRepository: stg.ProcessStepRepository,
		LoginUseCase:          auth.NewLoginUseCase(stg.UserRepository, infraauth.Verify),
		CreateProcessUseCase: process.NewCreateProcessUseCase(
			stg.ProcessRepository,
			definitions,
		),
		UpdateProcessUseCase:           process.NewUpdateProcessUseCase(stg.ProcessRepository),
		CompleteProcessUseCase:         process.NewCompleteProcessUseCase(stg.ProcessRepository),
		FailProcessUseCase:             process.NewFailProcessUseCase(stg.ProcessRepository),
		GetProcessUseCase:              process.NewGetProcessUseCase(stg.ProcessRepository),
		GetProcessStepUseCase:          engineprocessstep.NewGetProcessStepUseCase(stg.ProcessStepRepository),
		GetProcessDefinitionUseCase:    processdefinition.NewGetProcessDefinitionUseCase(definitions),
		CreateProcessDefinitionUseCase: processdefinition.NewCreateProcessDefinitionUseCase(definitions),
		UpdateProcessDefinitionUseCase: processdefinition.NewUpdateProcessDefinitionUseCase(definitions),
		DeleteProcessDefinitionUseCase: processdefinition.NewDeleteProcessDefinitionUseCase(definitions),
		Service: &service.Service{
			Name:            "engine",
			StartupTimeout:  cfg.StartupTimeout,
			ShutdownTimeout: cfg.ShutdownTimeout,
			Components: []service.Component{
				stg,
				infraauth.NewAdminInitializer(stg.UserRepository, cfg.AdminUsername, cfg.AdminPassword),
				sched,
			},
		},
	}, nil
}
