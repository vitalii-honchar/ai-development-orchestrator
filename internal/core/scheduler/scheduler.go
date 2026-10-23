package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/go-co-op/gocron/v2"
)

type (
	Scheduler struct {
		gocron.Scheduler
		ctx    context.Context
		cancel context.CancelFunc
	}

	SchedulerJob interface {
		Name() string
		Rate() time.Duration
		Timeout() time.Duration
		Run(ctx context.Context) error
	}
)

func NewScheduler() (*Scheduler, error) {
	s, err := gocron.NewScheduler()
	if err != nil {
		return nil, fmt.Errorf("gocron new scheduler: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Scheduler{
		Scheduler: s, ctx: ctx, cancel: cancel,
	}, nil
}

func (s *Scheduler) AtFixedRate(job SchedulerJob) error {
	_, err := s.NewJob(
		gocron.DurationJob(job.Rate()),
		gocron.NewTask(func() error {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic recovered in scheduled job",
						"jobName", job.Name(),
						"panic", r,
						"stacktrace", debug.Stack(),
					)
				}
			}()

			ctx, cancel := context.WithTimeout(s.ctx, job.Timeout())
			defer cancel()
			if err := job.Run(ctx); err != nil {
				slog.Error("Error occurred in scheduled job",
					"jobName", job.Name(),
					"error", err,
				)

				return fmt.Errorf("run job %q: %w", job.Name(), err)
			}

			return nil
		}),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
		gocron.WithStartAt(gocron.WithStartImmediately()),
	)
	if err != nil {
		return fmt.Errorf("schedule job %q: %w", job.Name(), err)
	}

	return nil
}

func (s *Scheduler) Name() string {
	return "scheduler"
}

func (s *Scheduler) Start(context.Context) error {
	s.Scheduler.Start()

	return nil
}

func (s *Scheduler) Stop(ctx context.Context) error {
	s.cancel()
	if err := s.ShutdownWithContext(ctx); err != nil {
		return fmt.Errorf("shutdown scheduler: %w", err)
	}

	return nil
}
