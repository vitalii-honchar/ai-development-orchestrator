package controlplane

import (
	"fmt"

	"go-software-factory/internal/controlplane/config"
	"go-software-factory/internal/controlplane/infra/web"
	"go-software-factory/internal/controlplane/infra/web/handler"
	"go-software-factory/internal/controlplane/infra/web/handler/process"
	"go-software-factory/internal/controlplane/infra/web/handler/processdefinition"
	"go-software-factory/internal/controlplane/infra/web/middleware"
	"go-software-factory/internal/core/service"
	"go-software-factory/internal/engine"
)

func NewControlPlane(eng *engine.Engine) (*service.Service, error) {
	cfg, err := config.FromEnv()
	if err != nil {
		return nil, fmt.Errorf("load control-plane configuration: %w", err)
	}

	return &service.Service{
		Name:            "control-plane",
		StartupTimeout:  cfg.StartupTimeout,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Components: []service.Component{
			web.NewWebServer(
				cfg.WebConfig,
				[]middleware.Middleware{middleware.NewLoggerMiddleware()},
				[]handler.Handler{
					processdefinition.NewListView(eng.GetProcessDefinitionUseCase),
					processdefinition.NewCreateView(),
					processdefinition.NewCreate(eng.CreateProcessDefinitionUseCase),
					processdefinition.NewDetailView(eng.GetProcessDefinitionUseCase),
					processdefinition.NewUpdate(eng.GetProcessDefinitionUseCase, eng.UpdateProcessDefinitionUseCase),
					process.NewListView(eng.GetProcessUseCase, eng.GetProcessDefinitionUseCase),
					process.NewDetailView(eng.GetProcessUseCase, eng.GetProcessStepUseCase, eng.GetProcessDefinitionUseCase),
					process.NewCreateView(eng.GetProcessDefinitionUseCase),
					process.NewCreate(eng.GetProcessDefinitionUseCase, eng.CreateProcessUseCase),
					process.NewDirectoryPicker(),
				},
				eng.LoginUseCase,
			),
		},
	}, nil
}
