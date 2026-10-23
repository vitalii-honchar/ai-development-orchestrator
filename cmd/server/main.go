package main

import (
	"context"
	"errors"
	"os/signal"
	"syscall"

	"go-software-factory/internal/controlplane"
	"go-software-factory/internal/engine"
	"go-software-factory/internal/worker"
)

type serverService interface {
	Start() error
	Stop() error
}

func main() {
	services, err := newServices()
	if err != nil {
		panic(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, services); err != nil {
		panic(err)
	}
}

func newServices() ([]serverService, error) {
	eng, err := engine.NewEngine()
	if err != nil {
		return nil, err
	}
	wrk, err := worker.NewWorker(&worker.WorkerDependencies{ProcessStepRepository: eng.ProcessStepRepository})
	if err != nil {
		_ = eng.Stop()

		return nil, err
	}
	control, err := controlplane.NewControlPlane(eng)
	if err != nil {
		_ = wrk.Stop()
		_ = eng.Stop()

		return nil, err
	}

	return []serverService{eng.Service, wrk.Service, control}, nil
}

func run(ctx context.Context, services []serverService) (err error) {
	started := 0
	defer func() {
		for i := started - 1; i >= 0; i-- {
			if stopErr := services[i].Stop(); stopErr != nil {
				err = errors.Join(err, stopErr)

				return
			}
		}
	}()
	for _, s := range services {
		if err := s.Start(); err != nil {
			return err
		}
		started++
	}
	<-ctx.Done()

	return nil
}
