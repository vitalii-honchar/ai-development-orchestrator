package processstep_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/worker/app/processstep"
)

//nolint:paralleltest // Captures the process-global slog logger; parallel tests resume after this test returns.
func TestStepExecutionLogsOutcomesAndPersistedRetries(t *testing.T) {
	const (
		completedOutcome        = "completed"
		retryOutcome            = "retry"
		persistenceErrorOutcome = "persistence error"
		exhaustedOutcome        = "exhausted"
	)
	for _, outcome := range []string{completedOutcome, retryOutcome, persistenceErrorOutcome, exhaustedOutcome} {
		t.Run(outcome, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			repo := &repository{records: fixture()}
			cause := errors.New("Codex authentication unavailable")
			if outcome == exhaustedOutcome {
				repo.records[0].Reliability.Executions = 2
			}
			executor := strategy(func(context.Context, *domain.ProcessStep) (*domain.Parameters, error) {
				if outcome == exhaustedOutcome {
					t.Fatal("executed a step after exhausting its budget")
				}
				if outcome == persistenceErrorOutcome {
					repo.updateErr = errors.New("retry persistence unavailable")
				}
				if outcome != completedOutcome {
					return nil, cause
				}

				return testParameters(map[string]domain.ParameterValue{
					"output": {Type: domain.ParameterTypeString, Value: "private output payload"},
				}), nil
			})
			err := processstep.NewExecuteProcessStepsUseCase(repo, []processstep.StepExecutor{executor}).Execute(t.Context())
			if (err != nil) != (outcome == retryOutcome || outcome == persistenceErrorOutcome) {
				t.Fatalf("unexpected error: %v", err)
			}
			events := stepLogEvents(t, &output)
			start := events["step_attempt_started"]
			if start == nil || start["started_at"] == nil || start["attempt"] == nil {
				t.Fatalf("attempt start was not logged: %s", &output)
			}
			for _, event := range events {
				if event["process_id"] != repo.records[0].ProcessID.String() ||
					event["step_id"] != repo.records[0].ID.String() || event["step_name"] != "test" {
					t.Fatalf("event cannot be correlated to its step: %v", event)
				}
			}
			switch outcome {
			case completedOutcome:
				if events["step_completed"] == nil || strings.Contains(output.String(), "private output payload") {
					t.Fatalf("completion missing or leaked output data: %s", &output)
				}
			case retryOutcome:
				retry := events["step_retry_scheduled"]
				if retry == nil || retry["error"] != cause.Error() || retry["next_attempt_at"] == nil ||
					retry["duration"] == nil || retry["state_persisted"] != true {
					t.Fatalf("persisted retry and error details were not logged: %s", &output)
				}
			case persistenceErrorOutcome:
				failed := events["step_attempt_failed"]
				if events["step_retry_scheduled"] != nil || failed == nil || failed["state_persisted"] != false ||
					!strings.Contains(failed["error"].(string), "retry persistence unavailable") {
					t.Fatalf("logs incorrectly claimed a retry was persisted: %s", &output)
				}
			case exhaustedOutcome:
				failed := events["step_failed"]
				if failed == nil || !strings.Contains(failed["error"].(string), "budget exhausted") {
					t.Fatalf("retry exhaustion cause was not logged: %s", &output)
				}
			}
		})
	}
}

func stepLogEvents(t *testing.T, output *bytes.Buffer) map[string]map[string]any {
	t.Helper()
	events := make(map[string]map[string]any)
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		events[event["event"].(string)] = event
	}

	return events
}

func TestAgentSchemaFailureIsRecordedWithoutCompletingStep(t *testing.T) {
	t.Parallel()
	repo := &repository{records: fixture()}
	repo.records[0].Type = domain.ProcessStepTypeAICodingAgent
	repo.records[0].Params = testParameters(map[string]domain.ParameterValue{
		"output_json": {Type: domain.ParameterTypeJSONSchema, Value: `{"type":"object","required":["path_to_plan"]}`},
	})
	executor := matchingStrategy{
		matches: func(context.Context, *domain.ProcessStep) (bool, error) { return true, nil },
		strategy: strategy(func(context.Context, *domain.ProcessStep) (*domain.Parameters, error) {
			return testParameters(map[string]domain.ParameterValue{
				"output": {Type: domain.ParameterTypeJSON, Value: `{"summary":"No path"}`},
			}), nil
		}),
	}
	err := processstep.NewExecuteProcessStepsUseCase(repo, []processstep.StepExecutor{executor}).Execute(t.Context())
	if err == nil || repo.records[0].State != domain.ProcessStepStatePending ||
		!strings.Contains(repo.records[0].Result.String("error"), "path_to_plan") ||
		repo.records[0].Reliability.ExecutedAt.Sub(repo.records[0].UpdatedAt) != time.Minute {
		t.Fatalf("schema failure was not retained for retry: %+v, %v", repo.records[0], err)
	}
}

type matchingStrategy struct {
	strategy
	matches func(context.Context, *domain.ProcessStep) (bool, error)
}

func (s matchingStrategy) Matches(ctx context.Context, step *domain.ProcessStep) (bool, error) {
	return s.matches(ctx, step)
}

func TestStepExecutionSelectsFirstMatchingStrategy(t *testing.T) {
	t.Parallel()
	repo := &repository{records: fixture()}
	repo.records[0].Params = testParameters(map[string]domain.ParameterValue{
		"executor_version": {Type: domain.ParameterTypeString, Value: "2"},
	})
	var calls []string
	newStrategy := func(name, version string) matchingStrategy {
		return matchingStrategy{
			strategy: strategy(func(context.Context, *domain.ProcessStep) (*domain.Parameters, error) {
				calls = append(calls, "execute "+name)

				return testParameters(map[string]domain.ParameterValue{
					"executor": {Type: domain.ParameterTypeString, Value: name},
				}), nil
			}),
			matches: func(_ context.Context, step *domain.ProcessStep) (bool, error) {
				calls = append(calls, "match "+name)

				return step.Type == testStepType && step.Params.Get("executor_version").Value == version, nil
			},
		}
	}
	ex := processstep.NewExecuteProcessStepsUseCase(repo, []processstep.StepExecutor{
		newStrategy("v1", "1"), newStrategy("v2", "2"), newStrategy("later v2", "2"),
	})
	if err := ex.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"match v1", "match v2", "execute v2"}) ||
		repo.records[0].State != domain.ProcessStepStateCompleted || repo.records[0].Result.Get("executor").Value != "v2" {
		t.Fatalf("selection calls=%v record=%+v", calls, repo.records[0])
	}
}

func TestStepExecutionReturnsMatchingError(t *testing.T) {
	t.Parallel()
	repo := &repository{records: fixture()}
	cause := errors.New("matching unavailable")
	executor := matchingStrategy{
		strategy: strategy(func(context.Context, *domain.ProcessStep) (*domain.Parameters, error) {
			t.Error("executed after matching failed")

			return nil, nil
		}),
		matches: func(context.Context, *domain.ProcessStep) (bool, error) { return false, cause },
	}
	ex := processstep.NewExecuteProcessStepsUseCase(
		repo, []processstep.StepExecutor{executor, matchingStrategy{
			matches: func(context.Context, *domain.ProcessStep) (bool, error) {
				t.Error("checked another executor after matching failed")

				return false, nil
			},
		}},
	)
	if err := ex.Execute(t.Context()); !errors.Is(err, cause) {
		t.Fatalf("matching error=%v want %v", err, cause)
	}
	if repo.records[0].State != domain.ProcessStepStatePending ||
		repo.records[0].Result.Get("error").Value != cause.Error() ||
		repo.records[0].Reliability.ExecutedAt.Sub(repo.records[0].UpdatedAt) != time.Minute {
		t.Fatalf("matching failure was not retained for retry: %+v", repo.records[0])
	}
}

func TestStepExecutionReturnsNotFoundWhenNothingMatches(t *testing.T) {
	t.Parallel()
	repo := &repository{records: fixture()}
	repo.records[0].Type = "UNSUPPORTED_STEP_TYPE"
	executor := strategy(func(context.Context, *domain.ProcessStep) (*domain.Parameters, error) {
		t.Error("executed a strategy that did not match")

		return nil, nil
	})
	ex := processstep.NewExecuteProcessStepsUseCase(repo, []processstep.StepExecutor{executor})
	if err := ex.Execute(t.Context()); !errors.Is(err, processstep.ErrStepExecutorNotFound) {
		t.Fatalf("no matching executor: %v", err)
	}
	if repo.records[0].State != domain.ProcessStepStatePending {
		t.Fatal("unmatched step completed")
	}
}

func TestStepPolicyTimeoutIncludesMatching(t *testing.T) {
	t.Parallel()
	repo := &repository{records: fixture()}
	repo.records[0].ExecutionPolicy.Timeout = time.Millisecond
	executor := matchingStrategy{
		strategy: strategy(func(context.Context, *domain.ProcessStep) (*domain.Parameters, error) {
			t.Error("executed after matching exceeded the timeout")

			return nil, nil
		}),
		matches: func(ctx context.Context, _ *domain.ProcessStep) (bool, error) {
			<-ctx.Done()

			return true, nil
		},
	}
	ex := processstep.NewExecuteProcessStepsUseCase(repo, []processstep.StepExecutor{executor})
	if err := ex.Execute(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("matching timeout: %v", err)
	}
	if repo.records[0].State != domain.ProcessStepStatePending {
		t.Fatal("timed out step completed")
	}
}

func TestStepExecutionSelectsStrategyAndCompletes(t *testing.T) {
	t.Parallel()
	repo := &repository{records: fixture()}
	want := testParameters(map[string]domain.ParameterValue{"answer": {Type: domain.ParameterTypeString, Value: "ok"}})
	earliestDeadline := time.Now().Add(time.Hour)
	execute := strategy(func(ctx context.Context, p *domain.ProcessStep) (*domain.Parameters, error) {
		if p.Reliability.Executions != 1 || p.Version != 2 {
			t.Error("unclaimed step")
		}
		deadline, ok := ctx.Deadline()
		if !ok || deadline.Before(earliestDeadline) || p.Reliability.ExecutedAt == nil ||
			p.Reliability.ExecutedAt.Before(earliestDeadline) {
			t.Errorf("step deadline=%v lease=%v, want at least %v", deadline, p.Reliability.ExecutedAt, earliestDeadline)
		}

		return want, nil
	})
	ex := processstep.NewExecuteProcessStepsUseCase(
		repo, []processstep.StepExecutor{execute},
	)
	if err := ex.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if repo.records[0].State != domain.ProcessStepStateCompleted ||
		repo.records[0].Result.Get("answer").Value != "ok" ||
		repo.records[0].Version != 3 {
		t.Fatalf("completed: %+v", repo.records[0])
	}
}
func TestStepExecutionRetriesUntilBudgetExhausted(t *testing.T) {
	t.Parallel()
	repo := &repository{records: fixture()}
	cause := errors.New("temporary failure")
	var calls atomic.Int32
	execute := strategy(func(context.Context, *domain.ProcessStep) (*domain.Parameters, error) {
		calls.Add(1)

		return nil, cause
	})
	ex := processstep.NewExecuteProcessStepsUseCase(
		repo, []processstep.StepExecutor{execute},
	)
	for range 2 {
		if err := ex.Execute(t.Context()); !errors.Is(err, cause) {
			t.Fatal(err)
		}
		if repo.records[0].State != domain.ProcessStepStatePending {
			t.Fatal("retriable step failed early")
		}
	}
	if err := ex.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if repo.records[0].State != domain.ProcessStepStateFailed ||
		calls.Load() != 2 ||
		repo.records[0].Result.Get("error").Value == "" {
		t.Fatalf("exhausted: %+v calls=%d", repo.records[0], calls.Load())
	}
}

func TestStepExecutionFailureSchedulesRetry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		retryAfter time.Duration
		want       time.Duration
	}{
		{"default", 0, time.Minute},
		{"configured", 15 * time.Second, 15 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := &repository{records: fixture()}
			repo.records[0].ExecutionPolicy.RetryTimeout = tc.retryAfter
			cause := errors.New("temporary CLI failure")
			ex := processstep.NewExecuteProcessStepsUseCase(repo, []processstep.StepExecutor{
				strategy(func(context.Context, *domain.ProcessStep) (*domain.Parameters, error) {
					return nil, cause
				}),
			})
			before := time.Now().Add(tc.want)
			if err := ex.Execute(t.Context()); !errors.Is(err, cause) {
				t.Fatal(err)
			}
			after := time.Now().Add(tc.want)
			step := &repo.records[0]
			if step.Reliability.ExecutedAt == nil || step.Reliability.ExecutedAt.Before(before) ||
				step.Reliability.ExecutedAt.After(after) || step.Reliability.Executions != 1 ||
				step.State != domain.ProcessStepStatePending || step.Result.String("error") != cause.Error() {
				t.Fatalf("failed step was not scheduled for retry after %s: %+v", tc.want, step)
			}
		})
	}
}

func TestStepExecutionWithoutRetryLimitRemainsPending(t *testing.T) {
	t.Parallel()
	repo := &repository{records: fixture()}
	repo.records[0].ExecutionPolicy.MaxRetries = 0
	repo.records[0].Reliability.Executions = 1000
	cause := errors.New("temporary failure")
	ex := processstep.NewExecuteProcessStepsUseCase(repo, []processstep.StepExecutor{
		strategy(func(context.Context, *domain.ProcessStep) (*domain.Parameters, error) { return nil, cause }),
	})
	for range 3 {
		if err := ex.Execute(t.Context()); !errors.Is(err, cause) {
			t.Fatalf("unlimited retries stopped executing: %v", err)
		}
	}
	if repo.records[0].State != domain.ProcessStepStatePending || repo.records[0].Reliability.Executions != 1003 {
		t.Fatalf("unlimited retry step: %+v", repo.records[0])
	}
}

func TestStepExecutionPanicSchedulesRetry(t *testing.T) {
	t.Parallel()
	repo := &repository{records: fixture()}
	ex := processstep.NewExecuteProcessStepsUseCase(repo, []processstep.StepExecutor{
		strategy(func(context.Context, *domain.ProcessStep) (*domain.Parameters, error) { panic("strategy panic") }),
	})
	err := ex.Execute(t.Context())
	if err == nil || !strings.Contains(repo.records[0].Result.String("error"), "strategy panic") ||
		repo.records[0].State != domain.ProcessStepStatePending ||
		repo.records[0].Reliability.ExecutedAt.Sub(repo.records[0].UpdatedAt) != time.Minute {
		t.Fatalf("panic did not schedule a retry: %+v, %v", repo.records[0], err)
	}
}

func TestStepUnknownStrategyAndCancelledSuccess(t *testing.T) {
	t.Parallel()
	repo := &repository{records: fixture()}
	ex := processstep.NewExecuteProcessStepsUseCase(repo, nil)
	if err := ex.Execute(t.Context()); !errors.Is(err, processstep.ErrStepExecutorNotFound) {
		t.Fatalf("unknown type: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	execute := strategy(func(context.Context, *domain.ProcessStep) (*domain.Parameters, error) {
		cancel()

		return nil, nil
	})
	ex = processstep.NewExecuteProcessStepsUseCase(
		repo, []processstep.StepExecutor{execute},
	)
	if err := ex.Execute(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if repo.records[0].State != domain.ProcessStepStatePending {
		t.Fatal("cancelled work completed")
	}
	if repo.records[0].Result.String("error") != context.Canceled.Error() ||
		repo.records[0].Reliability.ExecutedAt.Sub(repo.records[0].UpdatedAt) != time.Minute {
		t.Fatalf("cancelled work did not retain a retry: %+v", repo.records[0])
	}
}
func TestStepPolicyTimeoutAndPersistenceFailure(t *testing.T) {
	t.Parallel()
	repo := &repository{records: fixture()}
	repo.records[0].ExecutionPolicy.Timeout = time.Millisecond
	execute := strategy(func(ctx context.Context, _ *domain.ProcessStep) (*domain.Parameters, error) {
		<-ctx.Done()

		return nil, ctx.Err()
	})
	ex := processstep.NewExecuteProcessStepsUseCase(
		repo, []processstep.StepExecutor{execute},
	)
	if err := ex.Execute(t.Context()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if repo.records[0].Reliability.ExecutedAt.Sub(repo.records[0].UpdatedAt) != time.Minute {
		t.Fatalf("execution timeout retained the one-hour lease: %+v", repo.records[0])
	}
	cause := errors.New("persistence unavailable")
	repo.updateErr = cause
	if err := ex.Execute(t.Context()); !errors.Is(err, cause) {
		t.Fatal(err)
	}
}

func testParameters(values map[string]domain.ParameterValue) *domain.Parameters {
	params, err := domain.NewParameters(values)
	if err != nil {
		panic(err)
	}

	return params
}
