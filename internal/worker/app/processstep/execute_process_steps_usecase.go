package processstep

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"go-software-factory/internal/core/app/outbox"
	"go-software-factory/internal/core/domain"
)

const stepLogEvent = "event"

type ExecuteProcessStepsUseCase struct {
	executor   *outbox.TransactionalOutboxExecutor[domain.ProcessStep, *domain.ProcessStep]
	repository ProcessStepRepository
	strategies []StepExecutor
}

func NewExecuteProcessStepsUseCase(
	repository ProcessStepRepository, strategies []StepExecutor,
) *ExecuteProcessStepsUseCase {
	useCase := &ExecuteProcessStepsUseCase{repository: repository, strategies: slices.Clone(strategies)}
	useCase.executor = outbox.NewTransactionalOutboxExecutor[domain.ProcessStep](
		repository, useCase.processAttempt, time.Hour,
	)

	return useCase
}

func (u *ExecuteProcessStepsUseCase) Execute(ctx context.Context) error {
	return u.executor.Execute(ctx)
}

func (u *ExecuteProcessStepsUseCase) processAttempt(ctx context.Context, step *domain.ProcessStep) error {
	started := time.Now()
	log := slog.With("process_id", step.ProcessID.String(), "step_id", step.ID.String(),
		"step_name", step.ProcessStepDefinitionName, "step_type", step.Type, "attempt", step.Reliability.Executions)
	log.InfoContext(ctx, "Step execution attempt started", stepLogEvent, "step_attempt_started",
		"started_at", started.UTC(), "timeout", step.ExecutionPolicy.Timeout,
		"max_retries", step.ExecutionPolicy.MaxRetries, "lease_expires_at", step.Reliability.ExecutedAt)
	persisted, err := u.process(ctx, step)
	log = log.With("duration", time.Since(started), "state_persisted", persisted)
	if !persisted {
		log.ErrorContext(ctx, "Step execution attempt failed without persisting its outcome",
			stepLogEvent, "step_attempt_failed", "error", err, "attempted_state", step.State)

		return err
	}
	switch step.State {
	case domain.ProcessStepStateCompleted:
		log.InfoContext(ctx, "Step completed", stepLogEvent, "step_completed")
	case domain.ProcessStepStateFailed:
		log.ErrorContext(ctx, "Step failed", stepLogEvent, "step_failed", "error", step.Result.String("error"))
	default:
		log.WarnContext(ctx, "Step execution attempt failed; retry scheduled", stepLogEvent, "step_retry_scheduled",
			"error", step.Result.String("error"), "next_attempt_at", step.Reliability.ExecutedAt)
	}

	return err
}

func (u *ExecuteProcessStepsUseCase) process(ctx context.Context, step *domain.ProcessStep) (bool, error) {
	if step.ExecutionPolicy.MaxRetries > 0 && step.Reliability.Executions > step.ExecutionPolicy.MaxRetries {
		cause := fmt.Errorf("%w after %d acquisitions; last error: %s",
			errStepExecutionBudget,
			step.Reliability.Executions,
			step.Result.String("error"))
		if err := step.Fail(failure(cause)); err != nil {
			return false, err
		}

		err := u.repository.Update(ctx, step)

		return err == nil, err
	}
	result, err := u.execute(ctx, step)
	if err == nil {
		err = ctx.Err()
		if err == nil {
			err = step.Complete(result)
		}
	}
	if err != nil {
		if retryErr := step.Retry(failure(err)); retryErr != nil {
			return false, errors.Join(err, retryErr)
		}
		// Preserve retry eligibility even when an attempt ended with cancellation or a deadline.
		retryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		persistErr := u.repository.Update(retryCtx, step)

		return persistErr == nil, errors.Join(fmt.Errorf("execute step %s: %w", step.ID, err), persistErr)
	}

	err = u.repository.Update(ctx, step)

	return err == nil, err
}

func (u *ExecuteProcessStepsUseCase) execute(
	ctx context.Context, step *domain.ProcessStep,
) (result *domain.Parameters, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("%w: %v", errStepExecutorPanic, value)
		}
	}()
	if step.ExecutionPolicy.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, step.ExecutionPolicy.Timeout)
		defer cancel()
	}
	for _, executor := range u.strategies {
		matches, err := executor.Matches(ctx, step)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			return nil, err
		}
		if !matches {
			continue
		}
		result, err := executor.Execute(ctx, step)
		if err == nil {
			err = ctx.Err()
		}

		return result, err
	}

	return nil, fmt.Errorf("%w: %s", ErrStepExecutorNotFound, step.Type)
}

func failure(err error) *domain.Parameters {
	params, _ := domain.NewParameters(map[string]domain.ParameterValue{
		"error": {Type: domain.ParameterTypeString, Value: strings.ToValidUTF8(err.Error(), "\uFFFD")},
	})

	return params
}
