package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type Component interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type Service struct {
	Name            string
	Components      []Component
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
}

func (s *Service) Start() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.StartupTimeout)
	defer cancel()

	slog.With("service", s.Name).Info("Starting a service")

	for _, c := range s.Components {
		if err := c.Start(ctx); err != nil {
			slog.With("service", s.Name, "component", c.Name()).Error("Error during the component start")

			return fmt.Errorf("start %s component %s: %w", s.Name, c.Name(), err)
		}
	}

	slog.With("service", s.Name).Info("Service started")

	return nil
}

func (s *Service) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.ShutdownTimeout)
	defer cancel()

	slog.With("service", s.Name).Info("Stopping a service")

	for i := len(s.Components) - 1; i >= 0; i-- {
		c := s.Components[i]
		if err := c.Stop(ctx); err != nil {
			slog.With("service", s.Name, "component", c.Name()).Error("Error during the component stop")

			return fmt.Errorf("stop %s component %s: %w", s.Name, c.Name(), err)
		}
	}

	slog.With("service", s.Name).Info("Service stopped")

	return nil
}

func Must(s *Service, err error) *Service {
	if err != nil {
		panic(err)
	}

	return s
}
