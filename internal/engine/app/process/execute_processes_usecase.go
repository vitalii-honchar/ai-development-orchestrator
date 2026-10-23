package process

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"go-software-factory/internal/core/app/outbox"
	"go-software-factory/internal/core/domain"
)

const processLogEvent = "event"

type ExecuteProcessesUseCase struct {
	executor    *outbox.TransactionalOutboxExecutor[domain.Process, *domain.Process]
	processes   ProcessRepository
	steps       ProcessStepRepository
	definitions ProcessDefinitionRepository
	complete    *CompleteProcessUseCase
	fail        *FailProcessUseCase
}

func NewExecuteProcessesUseCase(
	processes ProcessRepository, steps ProcessStepRepository, definitions ProcessDefinitionRepository,
) *ExecuteProcessesUseCase {
	useCase := &ExecuteProcessesUseCase{
		processes: processes, steps: steps, definitions: definitions,
		complete: NewCompleteProcessUseCase(processes), fail: NewFailProcessUseCase(processes),
	}
	useCase.executor = outbox.NewTransactionalOutboxExecutor[domain.Process](
		processes, useCase.processAttempt, time.Minute,
	)

	return useCase
}

func (u *ExecuteProcessesUseCase) Execute(ctx context.Context) error {
	return u.executor.Execute(ctx)
}

func (u *ExecuteProcessesUseCase) processAttempt(ctx context.Context, model *domain.Process) error {
	started := time.Now()
	log := processAttemptLogger(model)
	log.InfoContext(ctx, "Process execution attempt started", processLogEvent, "process_attempt_started",
		"started_at", started.UTC(), "current_steps", model.CurrentSteps)
	err := u.process(ctx, model)
	log = log.With("duration", time.Since(started))
	if err != nil {
		log.ErrorContext(ctx, "Process execution attempt failed", processLogEvent, "process_attempt_failed",
			"error", err, "attempted_state", model.State, "lease_expires_at", model.Reliability.ExecutedAt)

		return fmt.Errorf("execute process %s: %w", model.ID, err)
	}
	switch model.State {
	case domain.ProcessStateCompleted:
		log.InfoContext(ctx, "Process completed", processLogEvent, "process_completed")
	case domain.ProcessStateFailed:
		log.ErrorContext(ctx, "Process failed", processLogEvent, "process_failed", "error", model.Result.String("error"))
	default:
		log.InfoContext(ctx, "Process execution attempt finished", processLogEvent, "process_attempt_finished",
			"state", model.State, "current_steps", model.CurrentSteps, "next_attempt_at", model.Reliability.ExecutedAt)
	}

	return nil
}

func (u *ExecuteProcessesUseCase) process(ctx context.Context, model *domain.Process) error {
	definition, err := u.definitions.GetByID(ctx, model.ProcessDefinitionID)
	if errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		_, failErr := u.fail.Fail(ctx, model, err)

		return failErr
	}
	// Without a definition we cannot determine the retry policy. Report read
	// failures and leave the process available for the next scheduler pass.
	if err != nil {
		return fmt.Errorf("load process definition: %w", err)
	}
	if definition.Reliability.MaxRetries > 0 && model.Reliability.Executions > definition.Reliability.MaxRetries {
		_, err := u.fail.Fail(
			ctx,
			model,
			fmt.Errorf("%w after %d acquisitions", errProcessExecutionBudget, model.Reliability.Executions),
		)

		return err
	}
	graph := &definition.Graph
	if len(graph.Nodes) == 0 {
		_, err := u.fail.Fail(ctx, model, errProcessGraphEmpty)

		return err
	}
	steps, contextChanged, err := u.loadSteps(ctx, model)
	if err != nil {
		return err
	}
	states := make(map[string]domain.ProcessStepState, len(steps))
	for index := range steps {
		step := &steps[index]
		if step.State == domain.ProcessStepStateFailed {
			return u.failForStep(ctx, model, step)
		}
		states[step.ProcessStepDefinitionName] = step.State
	}
	queue := model.CurrentSteps
	nextSteps := make([]string, 0, len(queue))
	visited := make(map[string]bool, len(queue))
	for head := 0; head < len(queue); head++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := queue[head]
		if visited[name] {
			continue
		}
		stop, err := u.visit(ctx, model, definition, name, model.Context, states, visited, &queue, &nextSteps)
		if stop || err != nil {
			return err
		}
	}

	return u.updateCurrentSteps(ctx, model, nextSteps, contextChanged)
}

func (u *ExecuteProcessesUseCase) failForStep(
	ctx context.Context, model *domain.Process, step *domain.ProcessStep,
) error {
	processAttemptLogger(model).ErrorContext(ctx, "Process cannot advance because a step failed",
		processLogEvent, "process_blocked_by_failed_step", "step_id", step.ID.String(),
		"step_name", step.ProcessStepDefinitionName, "error", step.Result.String("error"))
	_, err := u.fail.Fail(ctx, model, fmt.Errorf("%w: %q", errProcessStepFailed, step.ProcessStepDefinitionName))

	return err
}

func (u *ExecuteProcessesUseCase) loadSteps(
	ctx context.Context, model *domain.Process,
) ([]domain.ProcessStep, bool, error) {
	steps, err := u.steps.GetByProcessID(ctx, model.ID)
	if err != nil {
		return nil, false, fmt.Errorf("load process steps: %w", err)
	}
	changed, err := model.RecordStepOutputs(steps)

	return steps, changed, err
}

func (u *ExecuteProcessesUseCase) updateCurrentSteps(
	ctx context.Context, model *domain.Process, nextSteps []string, contextChanged bool,
) error {
	slices.Sort(nextSteps)
	stepsChanged := !slices.Equal(model.CurrentSteps, nextSteps)
	if stepsChanged {
		if err := model.UpdateCurrentSteps(nextSteps); err != nil {
			return err
		}
	}
	if stepsChanged || contextChanged {
		if err := u.processes.Update(ctx, model); err != nil {
			return fmt.Errorf("persist process progress: %w", err)
		}
	}

	return nil
}

func (u *ExecuteProcessesUseCase) visit(
	ctx context.Context,
	model *domain.Process,
	definition *domain.ProcessDefinition,
	name string,
	processContext *domain.ProcessContext,
	states map[string]domain.ProcessStepState,
	visited map[string]bool,
	queue, nextSteps *[]string,
) (bool, error) {
	visited[name] = true
	graph := &definition.Graph
	node := graph.Nodes[name]
	if node == nil {
		_, err := u.fail.Fail(ctx, model, fmt.Errorf("%w: %q", errProcessStepMissing, name))

		return true, err
	}
	if node.Type == domain.ProcessStepTypeTerminal {
		_, err := u.complete.Complete(ctx, model, model.Result)
		if err != nil {
			return true, err
		}

		return true, nil
	}
	switch states[name] {
	case domain.ProcessStepStateCompleted:
		processAttemptLogger(model).InfoContext(ctx, "Process advancing after completed step",
			processLogEvent, "process_step_advanced", "step_name", name, "next_steps", graph.Edges[name])
		*queue = append(*queue, graph.Edges[name]...)
	case domain.ProcessStepStatePending:
		processAttemptLogger(model).InfoContext(ctx, "Process waiting for step execution",
			processLogEvent, "process_waiting_for_step", "step_name", name)
		*nextSteps = append(*nextSteps, name)
	default:
		*nextSteps = append(*nextSteps, name)
		for _, predecessor := range graph.IncomingEdges[name] {
			if states[predecessor] != domain.ProcessStepStateCompleted {
				processAttemptLogger(model).InfoContext(ctx, "Process step waiting for predecessor",
					processLogEvent, "process_waiting_for_predecessor", "step_name", name,
					"predecessor", predecessor, "predecessor_state", states[predecessor])

				return false, nil
			}
		}
		step, err := node.CreateStep(model.ID, processContext)
		if err != nil {
			return true, fmt.Errorf("step %q: %w", name, err)
		}
		processAttemptLogger(model).InfoContext(ctx, "Process step dispatch requested",
			processLogEvent, "process_step_dispatch_requested", "step_id", step.ID.String(),
			"step_name", name, "step_type", step.Type)
		if err := u.steps.Save(ctx, step); err != nil {
			return true, fmt.Errorf("queue step %q: %w", name, err)
		}
	}

	return false, nil
}

func processAttemptLogger(model *domain.Process) *slog.Logger {
	return slog.With("process_id", model.ID.String(), "definition_id", model.ProcessDefinitionID.String(),
		"attempt", model.Reliability.Executions)
}
