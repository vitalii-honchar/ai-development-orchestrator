package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go-software-factory/internal/core/domain"
)

const outboxLogEvent = "event"

var (
	// ErrOutboxClaimLost identifies a competing acquisition or terminal record.
	ErrOutboxClaimLost = errors.New("outbox claim lost")
	errProcessorPanic  = errors.New("outbox processor panicked")
)

// TransactionalOutboxRecord describes the mutable lifecycle shared by processes and steps.
type TransactionalOutboxRecord interface {
	Acquire(time.Duration) error
	Fail(*domain.Parameters) error
	Complete(*domain.Parameters) error
	IsFinalState() bool
}

type Storage[T any] interface {
	GetPending(context.Context) ([]T, error)
	// Update persists optimistically and advances the supplied record's version on success.
	// It wraps ErrOutboxClaimLost when another claimant or a terminal record prevents the update.
	Update(context.Context, *T) error
}

// T keeps records contiguous; R preserves their concrete pointer type and mutable lifecycle.
type TransactionalOutboxExecutor[T any, R interface {
	*T
	TransactionalOutboxRecord
}] struct {
	storage   Storage[T]
	processor func(context.Context, R) error
	timeout   time.Duration
}

func NewTransactionalOutboxExecutor[T any, R interface {
	*T
	TransactionalOutboxRecord
}](
	storage Storage[T], processor func(context.Context, R) error, timeout time.Duration,
) *TransactionalOutboxExecutor[T, R] {
	return &TransactionalOutboxExecutor[T, R]{storage: storage, processor: processor, timeout: timeout}
}

func (e *TransactionalOutboxExecutor[T, R]) Execute(ctx context.Context) error {
	records, err := e.storage.GetPending(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to load pending executions", outboxLogEvent, "execution_query_failed",
			"record_type", fmt.Sprintf("%T", records), "error", err)

		return err
	}
	errs := make([]error, len(records))
	deadlines := make([]time.Time, len(records))
	acquired := make([]int, 0, len(records))
	for index := range records {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		record := R(&records[index])
		if record.IsFinalState() {
			continue
		}
		deadlines[index] = time.Now().Add(e.timeout)
		if err := record.Acquire(e.timeout); err != nil {
			executionRecordLogger(record).ErrorContext(ctx, "Execution acquisition failed",
				outboxLogEvent, "execution_claim_failed", "operation", "acquire", "error", err)
			errs[index] = fmt.Errorf("acquire record %d: %w", index, err)

			continue
		}
		if err := e.storage.Update(ctx, &records[index]); err != nil {
			if !errors.Is(err, ErrOutboxClaimLost) {
				executionRecordLogger(record).ErrorContext(ctx, "Failed to persist execution acquisition",
					outboxLogEvent, "execution_claim_failed", "operation", "persist_acquisition", "error", err)
				errs[index] = fmt.Errorf("persist acquisition %d: %w", index, err)
			} else {
				executionRecordLogger(record).DebugContext(ctx, "Execution acquisition skipped after a competing claim",
					outboxLogEvent, "execution_claim_lost", "error", err)
			}

			continue
		}
		acquired = append(acquired, index)
	}
	var wg sync.WaitGroup
	for _, index := range acquired {
		wg.Go(func() { errs[index] = e.process(ctx, R(&records[index]), deadlines[index]) })
	}
	wg.Wait()

	return errors.Join(append(errs, ctx.Err())...)
}

func (e *TransactionalOutboxExecutor[T, R]) process(ctx context.Context, record R, deadline time.Time) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("%w: %v", errProcessorPanic, value)
			executionRecordLogger(record).ErrorContext(ctx, "Execution processor panicked",
				outboxLogEvent, "execution_processor_panicked", "error", err, "lease_expires_at", deadline)
		}
	}()
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		executionRecordLogger(record).ErrorContext(ctx, "Execution was cancelled before its processor started",
			outboxLogEvent, "execution_dispatch_failed", "error", err, "lease_expires_at", deadline)

		return err
	}

	return e.processor(ctx, record)
}

func executionRecordLogger(record any) *slog.Logger {
	switch model := record.(type) {
	case *domain.Process:
		return slog.With("process_id", model.ID.String(), "definition_id", model.ProcessDefinitionID.String(),
			"attempt", model.Reliability.Executions)
	case *domain.ProcessStep:
		return slog.With("process_id", model.ProcessID.String(), "step_id", model.ID.String(),
			"step_name", model.ProcessStepDefinitionName, "attempt", model.Reliability.Executions)
	default:
		return slog.Default()
	}
}
