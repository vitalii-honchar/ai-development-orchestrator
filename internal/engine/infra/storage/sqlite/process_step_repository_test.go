package sqlite

import (
	"context"
	"errors"
	"go-software-factory/internal/core/app/outbox"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"
)

func TestStepInsertionPreservesWorkerState(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "steps.db"))
	p := processFixture(t, uuid.New())
	if err := NewProcessRepository(db.DB).Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	repository := NewProcessStepRepository(db.DB)
	step, err := domain.NewProcessStep(
		domain.WithNewProcessStepID(),
		domain.WithProcessStepState(domain.ProcessStepStatePending),
		domain.WithProcessStepProcessID(p.ID),
		domain.WithProcessStepProcessStepDefinitionName("build"),
		domain.WithProcessStepType("custom.build"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Save(ctx, step); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(
		ctx,
		"UPDATE process_steps SET state = 'COMPLETED' WHERE id = ?",
		step.ID.String(),
	); err != nil {
		t.Fatal(err)
	}
	duplicate, err := domain.NewProcessStep(
		domain.WithNewProcessStepID(),
		domain.WithProcessStepState(domain.ProcessStepStatePending),
		domain.WithProcessStepProcessID(p.ID),
		domain.WithProcessStepProcessStepDefinitionName("build"),
		domain.WithProcessStepType("custom.build"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Save(ctx, duplicate); err != nil {
		t.Fatal(err)
	}
	rows, err := repository.GetByProcessID(ctx, p.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != step.ID || rows[0].State != domain.ProcessStepStateCompleted ||
		rows[0].Type != "custom.build" {
		t.Fatalf("duplicate overwrote worker: %+v %v", rows, err)
	}
	rows, err = repository.GetByProcessID(ctx, uuid.New())
	if err != nil || len(rows) != 0 {
		t.Fatalf("other process: %+v %v", rows, err)
	}
}

func TestProcessStepContextRoundTrip(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "context.db"))
	parent := processFixture(t, uuid.New())
	if err := NewProcessRepository(db.DB).Save(ctx, parent); err != nil {
		t.Fatal(err)
	}
	step, err := domain.NewProcessStep(
		domain.WithNewProcessStepID(), domain.WithProcessStepProcessID(parent.ID),
		domain.WithProcessStepState(domain.ProcessStepStatePending),
		domain.WithProcessStepProcessStepDefinitionName("implement"), domain.WithProcessStepType("custom"),
		domain.WithProcessStepContext(testParameters(map[string]domain.ParameterValue{
			"path_to_plan": {Type: domain.ParameterTypeFilePath, Value: "/project/docs/plans/solution-plan-20261019.md"},
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	repository := NewProcessStepRepository(db.DB)
	if err := repository.Save(ctx, step); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetByID(ctx, step.ID)
	if err != nil || stored.Context.String("path_to_plan") != "/project/docs/plans/solution-plan-20261019.md" {
		t.Fatalf("step context was not persisted: %+v, %v", stored, err)
	}
}

func TestStepOutboxClaimsAndEligibility(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "claims.db"))
	parent := processFixture(t, uuid.New())
	processes := NewProcessRepository(db.DB)
	if err := processes.Save(ctx, parent); err != nil {
		t.Fatal(err)
	}
	repo := NewProcessStepRepository(db.DB)
	step,
		err := domain.NewProcessStep(domain.WithNewProcessStepID(),
		domain.WithProcessStepState(domain.ProcessStepStatePending),
		domain.WithProcessStepProcessID(parent.ID),
		domain.WithProcessStepProcessStepDefinitionName("test"),
		domain.WithProcessStepType("TEST_STEP_TYPE"),
		domain.WithProcessStepExecutionPolicy(domain.ProcessDefinitionReliability{MaxRetries: 3}))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, step); err != nil {
		t.Fatal(err)
	}
	pending, err := repo.GetPending(ctx)
	if err != nil || len(pending) != 1 || pending[0].ExecutionPolicy.MaxRetries != 3 {
		t.Fatalf("pending=%+v %v", pending, err)
	}
	first, err := repo.GetByID(ctx, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := repo.GetByID(ctx, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Acquire(time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, first); err != nil {
		t.Fatal(err)
	}
	if first.Version != 2 {
		t.Fatal("update did not advance supplied version")
	}
	if err := stale.Acquire(time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, stale); !errors.Is(err, domain.ErrProcessStepVersionConflict) ||
		!errors.Is(err, outbox.ErrOutboxClaimLost) {
		t.Fatalf("stale acquisition=%v", err)
	}
	if got, err := repo.GetPending(ctx); err != nil || len(got) != 0 {
		t.Fatalf("leased step selected: %+v %v", got, err)
	}
	result := testParameters(map[string]domain.ParameterValue{
		testOutputParameter: {Type: domain.ParameterTypeJSON, Value: `{"summary":"Executed the task."}`},
	})
	if err := first.Complete(result); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, first); err != nil {
		t.Fatal(err)
	}
	if first.Version != 3 {
		t.Fatal("completion version not advanced")
	}
	completed, err := repo.GetByID(ctx, step.ID)
	if err != nil || completed.Result.Get("output") != result.Get("output") {
		t.Fatalf("persisted JSON result=%+v error=%v", completed, err)
	}
	if err := stale.Complete(nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, stale); !errors.Is(err, domain.ErrProcessStepVersionConflict) ||
		!errors.Is(err, outbox.ErrOutboxClaimLost) {
		t.Fatalf("stale completion=%v", err)
	}
	if _, err := repo.GetByID(ctx, uuid.New()); !errors.Is(err, domain.ErrProcessStepNotFound) {
		t.Fatal(err)
	}
}
func TestStepRetryPersistsNextEligibility(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "retry.db"))
	parent := processFixture(t, uuid.New())
	if err := NewProcessRepository(db.DB).Save(ctx, parent); err != nil {
		t.Fatal(err)
	}
	repo := NewProcessStepRepository(db.DB)
	step, err := domain.NewProcessStep(
		domain.WithNewProcessStepID(), domain.WithProcessStepState(domain.ProcessStepStatePending),
		domain.WithProcessStepProcessID(parent.ID), domain.WithProcessStepProcessStepDefinitionName("retry"),
		domain.WithProcessStepType("TEST_STEP_TYPE"),
		domain.WithProcessStepExecutionPolicy(domain.ProcessDefinitionReliability{RetryTimeout: 15 * time.Second}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, step); err != nil {
		t.Fatal(err)
	}
	if err := step.Acquire(time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, step); err != nil {
		t.Fatal(err)
	}
	result := testParameters(map[string]domain.ParameterValue{
		testErrorParameter: {Type: domain.ParameterTypeString, Value: "temporary CLI failure"},
	})
	if err := step.Retry(result); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, step); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetByID(ctx, step.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ExecutionPolicy.RetryTimeout != 15*time.Second || stored.Reliability.Executions != 1 ||
		stored.Reliability.ExecutedAt.Sub(stored.UpdatedAt) != 15*time.Second ||
		stored.Result.String(testErrorParameter) != "temporary CLI failure" {
		t.Fatalf("retry state was not persisted: %+v", stored)
	}
	if got, err := repo.GetPending(ctx); err != nil || len(got) != 0 {
		t.Fatalf("selected a retry before it was due: %+v, %v", got, err)
	}
	past := time.Now().UTC().Add(-time.Second)
	stored.Reliability.ExecutedAt = &past
	if err := repo.Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.GetPending(ctx); err != nil || len(got) != 1 || got[0].ID != step.ID {
		t.Fatalf("due retry was not eligible: %+v, %v", got, err)
	}
}

func TestStepOutboxExcludesTerminalParentsAndReopens(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "parents.db")
	db, ctx := newProcessDefinitionTestDB(t, path)
	parent := processFixture(t, uuid.New())
	processes := NewProcessRepository(db.DB)
	if err := processes.Save(ctx, parent); err != nil {
		t.Fatal(err)
	}
	repo := NewProcessStepRepository(db.DB)
	step,
		err := domain.NewProcessStep(domain.WithNewProcessStepID(),
		domain.WithProcessStepState(domain.ProcessStepStatePending),
		domain.WithProcessStepProcessID(parent.ID),
		domain.WithProcessStepProcessStepDefinitionName("test"),
		domain.WithProcessStepType("TEST_STEP_TYPE"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, step); err != nil {
		t.Fatal(err)
	}
	if err := parent.Complete(nil); err != nil {
		t.Fatal(err)
	}
	if err := processes.Update(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.GetPending(ctx); err != nil || len(got) != 0 {
		t.Fatalf("terminal parent step selected: %+v %v", got, err)
	}
	if err := step.Acquire(time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, step); err != nil {
		t.Fatalf("update step: %v", err)
	}
	if err := db.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, reopenCtx := newProcessDefinitionTestDB(t, path)
	got, err := NewProcessStepRepository(reopened.DB).GetByID(reopenCtx, step.ID)
	if err != nil || got.Version != 2 || got.Reliability.Executions != 1 || got.CreatedAt.IsZero() {
		t.Fatalf("reopened=%+v %v", got, err)
	}
}

type stepClaimBarrier struct {
	*ProcessStepRepository
	ready   chan<- struct{}
	release <-chan struct{}
}

func (r stepClaimBarrier) GetPending(ctx context.Context) ([]domain.ProcessStep, error) {
	steps, err := r.ProcessStepRepository.GetPending(ctx)
	if err != nil {
		return nil, err
	}
	r.ready <- struct{}{}
	select {
	case <-r.release:
		return steps, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestCompetingStepExecutorsClaimOnce(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "competing-steps.db"))
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	parent := processFixture(t, uuid.New())
	if err := NewProcessRepository(db.DB).Save(ctx, parent); err != nil {
		t.Fatal(err)
	}
	repo := NewProcessStepRepository(db.DB)
	step, err := domain.NewProcessStep(
		domain.WithNewProcessStepID(), domain.WithProcessStepState(domain.ProcessStepStatePending),
		domain.WithProcessStepProcessID(parent.ID), domain.WithProcessStepProcessStepDefinitionName("competing"),
		domain.WithProcessStepType("TEST_STEP_TYPE"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, step); err != nil {
		t.Fatal(err)
	}
	ready, release := make(chan struct{}, 2), make(chan struct{})
	results := make(chan error, 2)
	var calls atomic.Int32
	for range 2 {
		executor := outbox.NewTransactionalOutboxExecutor[domain.ProcessStep](stepClaimBarrier{repo, ready, release},
			func(ctx context.Context, step *domain.ProcessStep) error {
				calls.Add(1)
				if err := step.Complete(nil); err != nil {
					return err
				}

				return repo.Update(ctx, step)
			}, time.Minute)
		go func() { results <- executor.Execute(ctx) }()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("executors did not read the same pending record")
		}
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	stored, err := repo.GetByID(ctx, step.ID)
	if err != nil || calls.Load() != 1 || stored.Version != 3 || stored.State != domain.ProcessStepStateCompleted {
		t.Fatalf("competing execution: calls=%d record=%+v err=%v", calls.Load(), stored, err)
	}
}
