package outbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/core/app/outbox"
	"go-software-factory/internal/core/domain"
)

//nolint:paralleltest // Captures the process-global slog logger; parallel tests resume after this test returns.
func TestExecutorLogsAcquisitionFailureWithRecordIdentity(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	cause := errors.New("acquisition database unavailable")
	st := &storage{records: records(1), update: func(context.Context, *domain.Process) error { return cause }}
	executor := outbox.NewTransactionalOutboxExecutor[domain.Process](st,
		func(context.Context, *domain.Process) error {
			t.Error("processed a record whose acquisition failed")

			return nil
		}, time.Minute)
	if err := executor.Execute(t.Context()); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil {
		t.Fatalf("acquisition error was not logged: %v; output=%s", err, &output)
	}
	if event["event"] != "execution_claim_failed" || event["process_id"] != st.records[0].ID.String() ||
		event["error"] != cause.Error() || event["operation"] != "persist_acquisition" {
		t.Fatalf("acquisition failure lost its identity or cause: %v", event)
	}
}

type storage struct {
	records []domain.Process
	update  func(context.Context, *domain.Process) error
	readErr error
}

func (s *storage) GetPending(context.Context) ([]domain.Process, error) { return s.records, s.readErr }
func (s *storage) Update(ctx context.Context, p *domain.Process) error {
	if s.update != nil {
		return s.update(ctx, p)
	}
	p.Version++

	return nil
}
func records(count int) []domain.Process {
	result := make([]domain.Process, count)
	for i := range result {
		result[i] = domain.Process{ID: uuid.New(),
			ProcessDefinitionID: uuid.New(),
			State:               domain.ProcessStatePending,
			Version:             1,
			CreatedAt:           time.Now().UTC()}
	}

	return result
}
func TestExecutorAcquiresBeforeConcurrentDispatch(t *testing.T) {
	t.Parallel()
	st := &storage{records: records(3)}
	var claims atomic.Int32
	st.update = func(_ context.Context, p *domain.Process) error {
		claims.Add(1)
		p.Version++

		return nil
	}
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	ex := outbox.NewTransactionalOutboxExecutor[domain.Process](st, func(ctx context.Context, p *domain.Process) error {
		if claims.Load() != 3 || p.Version != 2 || p.Reliability.Executions != 1 {
			return errors.New("dispatch before acquisition")
		}
		started <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, time.Minute)
	done := make(chan error, 1)
	go func() { done <- ex.Execute(ctx) }()
	for range 3 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("records were not processed concurrently")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for i := range st.records {
		if st.records[i].State != domain.ProcessStatePending {
			t.Fatal("callback success completed a waiting record")
		}
	}
}
func TestExecutorJoinsErrorsAndSkipsLostClaims(t *testing.T) {
	t.Parallel()
	claimErr, processErr := errors.New("claim unavailable"), errors.New("processor unavailable")
	st := &storage{records: records(4)}
	st.update = func(_ context.Context, p *domain.Process) error {
		switch p.ID {
		case st.records[0].ID:
			return fmt.Errorf("competing acquisition: %w", outbox.ErrOutboxClaimLost)
		case st.records[1].ID:
			return claimErr
		}
		p.Version++

		return nil
	}
	var calls atomic.Int32
	ex := outbox.NewTransactionalOutboxExecutor[domain.Process](st, func(context.Context, *domain.Process) error {
		calls.Add(1)

		return processErr
	}, time.Minute)
	err := ex.Execute(t.Context())
	if !errors.Is(err, claimErr) || !errors.Is(err, processErr) ||
		errors.Is(err, outbox.ErrOutboxClaimLost) || calls.Load() != 2 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}
func TestExecutorCancellationReadFailureAndPanic(t *testing.T) {
	t.Parallel()
	cause := errors.New("read failed")
	ex := outbox.NewTransactionalOutboxExecutor[domain.Process](&storage{readErr: cause},
		func(context.Context, *domain.Process) error {
			t.Error("processed after read failure")

			return nil
		}, time.Minute)
	if err := ex.Execute(t.Context()); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	ex = outbox.NewTransactionalOutboxExecutor[domain.Process](&storage{records: records(1)},
		func(context.Context, *domain.Process) error {
			t.Error("processed cancelled record")

			return nil
		}, time.Minute)
	if err := ex.Execute(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ex = outbox.NewTransactionalOutboxExecutor[domain.Process](&storage{records: records(1)},
		func(context.Context, *domain.Process) error {
			panic("strategy panic")
		},
		time.Minute)
	if err := ex.Execute(t.Context()); err == nil {
		t.Fatal("panic not reported")
	}
}
func TestExecutorBoundsProcessingAndRetainsAcquisitionErrors(t *testing.T) {
	t.Parallel()
	st := &storage{records: records(2)}
	cause := errors.New("claim failed")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	st.update = func(context.Context, *domain.Process) error {
		cancel()

		return cause
	}
	ex := outbox.NewTransactionalOutboxExecutor[domain.Process](st,
		func(context.Context, *domain.Process) error {
			return nil
		},
		time.Minute)
	if err := ex.Execute(ctx); !errors.Is(err, cause) || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ex = outbox.NewTransactionalOutboxExecutor[domain.Process](&storage{records: records(1)},
		func(ctx context.Context, _ *domain.Process) error {
			<-ctx.Done()

			return ctx.Err()
		}, 10*time.Millisecond)
	if err := ex.Execute(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
