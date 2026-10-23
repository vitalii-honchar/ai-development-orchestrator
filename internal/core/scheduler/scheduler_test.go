package scheduler_test

import (
	"context"
	"testing"
	"time"

	"go-software-factory/internal/core/scheduler"
)

type blockingJob struct{ started, exited chan struct{} }

func (blockingJob) Name() string           { return "blocking-job" }
func (blockingJob) Rate() time.Duration    { return time.Second }
func (blockingJob) Timeout() time.Duration { return 30 * time.Second }
func (j blockingJob) Run(ctx context.Context) error {
	close(j.started)
	<-ctx.Done()
	close(j.exited)

	return ctx.Err()
}
func TestStopCancelsAndDrainsJob(t *testing.T) {
	t.Parallel()
	sched, err := scheduler.NewScheduler()
	if err != nil {
		t.Fatal(err)
	}
	job := blockingJob{make(chan struct{}), make(chan struct{})}
	if err := sched.AtFixedRate(job); err != nil {
		t.Fatal(err)
	}
	if err := sched.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-job.started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := sched.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-job.exited:
	default:
		t.Fatal("scheduler returned before job stopped")
	}
}
