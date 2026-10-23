package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/engine/app/process"
	"go-software-factory/internal/engine/app/processdefinition"
	"go-software-factory/internal/engine/infra/storage/sqlite"
)

func TestEngineReadsExecutionsAndSteps(t *testing.T) {
	const stepName = "test"
	t.Setenv("FACTORY_SQLITE_PATH", filepath.Join(t.TempDir(), "execution-reads.db"))
	t.Setenv("ADMIN_USERNAME", "")
	t.Setenv("ADMIN_PASSWORD", "")
	eng, err := NewEngine()
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Stop() })
	ctx := t.Context()
	graph := domain.NewProcessGraph()
	if err := graph.AddNode(&domain.ProcessStepDefinition{Name: stepName, Type: "worker.test"}); err != nil {
		t.Fatal(err)
	}
	definition := newProcessDefinitionFixture(t, domain.WithProcessDefinitionGraph(*graph),
		domain.WithProcessDefinitionState(domain.ProcessDefinitionStateActive))
	definition, err = eng.CreateProcessDefinitionUseCase.Create(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	p, err := eng.CreateProcessUseCase.Create(ctx, definition.ID, testParameters(map[string]domain.ParameterValue{
		"message": {Type: domain.ParameterTypeString, Value: "inspect this execution"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := eng.GetProcessUseCase.GetByID(ctx, p.ID)
	if err != nil || stored.ID != p.ID || stored.Context == nil ||
		stored.Context.Inputs.String("message") != "inspect this execution" {
		t.Fatalf("read execution: %+v, %v", stored, err)
	}
	all, err := eng.GetProcessUseCase.GetAll(ctx)
	if err != nil || len(all) != 1 || all[0].ID != p.ID {
		t.Fatalf("read execution history: %+v, %v", all, err)
	}
	if _, err := eng.GetProcessUseCase.GetByID(ctx, uuid.New()); !errors.Is(err, domain.ErrProcessNotFound) {
		t.Fatalf("missing execution: %v", err)
	}
	now := time.Now().UTC()
	step := &domain.ProcessStep{
		ID: uuid.New(), ProcessID: p.ID, ProcessStepDefinitionName: stepName,
		Type: "worker.test", State: domain.ProcessStepStatePending,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := eng.ProcessStepRepository.Save(ctx, step); err != nil {
		t.Fatal(err)
	}
	steps, err := eng.GetProcessStepUseCase.GetByProcessID(ctx, p.ID)
	if err != nil || len(steps) != 1 || steps[0].ProcessStepDefinitionName != stepName || steps[0].ProcessID != p.ID {
		t.Fatalf("read steps: %+v, %v", steps, err)
	}
	other, err := eng.GetProcessStepUseCase.GetByProcessID(ctx, uuid.New())
	if err != nil || len(other) != 0 {
		t.Fatalf("steps leaked across executions: %+v, %v", other, err)
	}
}

func TestEngineSchedulerQueuesPendingSteps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "engine.db")
	t.Setenv("FACTORY_SQLITE_PATH", path)
	t.Setenv("FACTORY_STORAGE_MODE", "sqlite")
	t.Setenv("FACTORY_PROCESS_EXECUTOR_RATE", "10ms")
	t.Setenv("FACTORY_PROCESS_EXECUTOR_TIMEOUT", "1s")
	t.Setenv("ADMIN_USERNAME", "")
	t.Setenv("ADMIN_PASSWORD", "")
	eng, err := NewEngine()
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := eng.Stop(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	graph := domain.NewProcessGraph()
	node, err := domain.NewProcessStepDefinition(
		domain.WithProcessStepDefinitionName("build"),
		domain.WithProcessStepDefinitionType("custom.build"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AddNode(node); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	definition, err := domain.NewProcessDefinition(
		domain.WithNewProcessDefinitionID(),
		domain.WithProcessDefinitionName("scheduled"),
		domain.WithProcessDefinitionState(domain.ProcessDefinitionStateActive),
		domain.WithProcessDefinitionVersion(1),
		domain.WithProcessDefinitionGraph(*graph),
		domain.WithProcessDefinitionCreatedAt(now),
		domain.WithProcessDefinitionUpdatedAt(now),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.CreateProcessDefinitionUseCase.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}
	p, err := eng.CreateProcessUseCase.Create(ctx, definition.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	observer, err := sqlite.NewDB(&sqlite.SqliteConfig{Path: path, MaxOpenConns: 1, Pragmas: sqlite.PragmasSettings})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := observer.Close(); err != nil {
			t.Error(err)
		}
	}()
	steps := sqlite.NewProcessStepRepository(observer.DB)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		rows, err := steps.GetByProcessID(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 {
			if len(rows) != 1 || rows[0].State != domain.ProcessStepStatePending || rows[0].Type != "custom.build" {
				t.Fatalf("scheduled steps: %+v", rows)
			}

			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("scheduler did not queue process step")
		case <-ticker.C:
		}
	}
	if eng.UpdateProcessUseCase == nil || eng.CompleteProcessUseCase == nil || eng.FailProcessUseCase == nil {
		t.Fatal("lifecycle use cases not exposed")
	}
}

const (
	joinStepName        = "join"
	terminalStepName    = "end"
	rootStepName        = "root"
	failingStepName     = "bad"
	successfulStepName  = "good"
	sourceParameterName = "source"
)

func TestProcessBFSBranchesJoinsAndRestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "bfs.db")
	db, ctx := newProcessDefinitionTestDB(t, path)
	p := executionFixture(
		t,
		ctx,
		db,
		[]string{"a", "b", "c", "d", joinStepName, "independent", terminalStepName},
		[][2]string{
			{"a", "b"},
			{"a", "c"},
			{"c", "d"},
			{"b", joinStepName},
			{"d", joinStepName},
			{joinStepName, terminalStepName},
		},
		10,
		terminalStepName,
	)
	assertCurrentSteps(t, ctx, db, p.ID, "a", "independent")
	runDueExecution(t, ctx, db)
	assertStepNames(t, ctx, db, p.ID, "a", "independent")
	rows, _ := sqlite.NewProcessStepRepository(db.DB).GetByProcessID(ctx, p.ID)
	originalID := rows[0].ID
	for range 3 {
		runDueExecution(t, ctx, db)
	}
	after, _ := sqlite.NewProcessStepRepository(db.DB).GetByProcessID(ctx, p.ID)
	if len(after) != 2 || after[0].ID != originalID {
		t.Fatalf("waiting duplicated steps: %+v", after)
	}
	pending, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if err != nil || pending.State != domain.ProcessStatePending || pending.Reliability.Executions != 4 ||
		pending.Reliability.ExecutedAt == nil {
		t.Fatalf("waiting acquisitions or state: %+v %v", pending, err)
	}
	setStepState(t, ctx, db, p.ID, "a", "COMPLETED")
	if err := db.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	db, ctx = newProcessDefinitionTestDB(t, path)
	runDueExecution(t, ctx, db)
	assertStepNames(t, ctx, db, p.ID, "a", "b", "c", "independent")
	assertCurrentSteps(t, ctx, db, p.ID, "b", "c", "independent")
	setStepState(t, ctx, db, p.ID, "b", "COMPLETED")
	runDueExecution(t, ctx, db)
	assertStepNames(t, ctx, db, p.ID, "a", "b", "c", "independent")
	assertCurrentSteps(t, ctx, db, p.ID, "c", "independent", joinStepName)
	setStepState(t, ctx, db, p.ID, "c", "COMPLETED")
	runDueExecution(t, ctx, db)
	assertStepNames(t, ctx, db, p.ID, "a", "b", "c", "d", "independent")
	assertCurrentSteps(t, ctx, db, p.ID, "d", "independent", joinStepName)
	setStepState(t, ctx, db, p.ID, "d", "COMPLETED")
	runDueExecution(t, ctx, db)
	assertStepNames(t, ctx, db, p.ID, "a", "b", "c", "d", "independent", joinStepName)
	setStepState(t, ctx, db, p.ID, joinStepName, "COMPLETED")
	runDueExecution(t, ctx, db)
	completed, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if err != nil || completed.State != domain.ProcessStateCompleted || completed.Reliability.ExecutionTime <= 0 ||
		completed.Reliability.Executions != 9 {
		t.Fatalf("completion: %+v %v", completed, err)
	}
	assertStepNames(t, ctx, db, p.ID, "a", "b", "c", "d", "independent", joinStepName)
	runDueExecution(t, ctx, db)
	final, _ := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if final.Version != completed.Version {
		t.Fatal("terminal process reprocessed")
	}
}

func TestProcessCompletesOnlyWhenTerminalIsReached(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		nodes     []string
		edges     [][2]string
		terminals []string
		completed []string
		wantState domain.ProcessState
		wantSteps []string
	}{
		{
			name: "terminal root stops traversal", nodes: []string{terminalStepName, "after"},
			edges: [][2]string{{terminalStepName, "after"}}, terminals: []string{terminalStepName},
			wantState: domain.ProcessStateCompleted,
		},
		{
			name: "terminal behind pending step is not reached", nodes: []string{"a", terminalStepName},
			edges: [][2]string{{"a", terminalStepName}}, terminals: []string{terminalStepName},
			wantState: domain.ProcessStatePending, wantSteps: []string{"a"},
		},
		{
			name: "completed nodes without terminal do not complete process", nodes: []string{"a"},
			completed: []string{"a"}, wantState: domain.ProcessStatePending, wantSteps: []string{"a"},
		},
		{
			name: "one path reaching a shared terminal completes process", nodes: []string{"a", "b", terminalStepName},
			edges: [][2]string{{"a", terminalStepName}, {"b", terminalStepName}}, terminals: []string{terminalStepName},
			completed: []string{"a"}, wantState: domain.ProcessStateCompleted, wantSteps: []string{"a", "b"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "terminal.db"))
			p := executionFixture(t, ctx, db, tc.nodes, tc.edges, 3, tc.terminals...)
			runDueExecution(t, ctx, db)
			if len(tc.completed) > 0 {
				for _, name := range tc.completed {
					setStepState(t, ctx, db, p.ID, name, "COMPLETED")
				}
				runDueExecution(t, ctx, db)
			}
			stored, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.State != tc.wantState {
				t.Fatalf("state=%s want %s", stored.State, tc.wantState)
			}
			assertStepNames(t, ctx, db, p.ID, tc.wantSteps...)
		})
	}
}

func TestProcessFailedStepIsTerminal(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "failed-step.db"))
	p := executionFixture(t, ctx, db, []string{"a", "b"}, [][2]string{{"a", "b"}}, 5)
	runDueExecution(t, ctx, db)
	setStepState(t, ctx, db, p.ID, "a", "FAILED")
	runDueExecution(t, ctx, db)
	failed, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if err != nil || failed.State != domain.ProcessStateFailed || failed.Reliability.Executions != 2 ||
		failed.Result.Get("error").Value == "" {
		t.Fatalf("failed step not propagated: %+v %v", failed, err)
	}
	assertStepNames(t, ctx, db, p.ID, "a")
	stepRows, err := sqlite.NewProcessStepRepository(db.DB).GetByProcessID(ctx, p.ID)
	if err != nil || stepRows[0].State != domain.ProcessStepStateFailed {
		t.Fatalf("failed step reset: %+v %v", stepRows, err)
	}
}

func TestProcessRetriesPreservePartiallyQueuedFrontier(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "partial.db"))
	p := executionFixture(t, ctx, db, []string{"a", "b"}, nil, 3)
	if _, err := db.ExecContext(
		ctx,
		`CREATE TRIGGER reject_b
		BEFORE INSERT ON process_steps
		WHEN NEW.process_step_definition_name = 'b'
		BEGIN SELECT RAISE(ABORT, 'temporary write failure'); END;`,
	); err != nil {
		t.Fatal(err)
	}
	executor := process.NewExecuteProcessesUseCase(
		sqlite.NewProcessRepository(db.DB),
		sqlite.NewProcessStepRepository(db.DB),
		sqlite.NewProcessDefinitionRepository(db.DB),
	)
	if err := executor.Execute(ctx); err == nil {
		t.Fatal("insertion failure hidden")
	}
	assertStepNames(t, ctx, db, p.ID, "a")
	before, _ := sqlite.NewProcessStepRepository(db.DB).GetByProcessID(ctx, p.ID)
	pending, _ := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if pending.State != domain.ProcessStatePending || pending.Reliability.Executions != 1 {
		t.Fatalf("retry not recorded: %+v", pending)
	}
	if _, err := db.ExecContext(ctx, "DROP TRIGGER reject_b"); err != nil {
		t.Fatal(err)
	}
	runDueExecution(t, ctx, db)
	assertStepNames(t, ctx, db, p.ID, "a", "b")
	after, _ := sqlite.NewProcessStepRepository(db.DB).GetByProcessID(ctx, p.ID)
	if before[0].ID != after[0].ID {
		t.Fatal("retry recreated queued work")
	}
	runDueExecution(t, ctx, db)
	pending, _ = sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if pending.Reliability.Executions != 3 {
		t.Fatalf("acquisition count=%d want 3", pending.Reliability.Executions)
	}
}

func TestProcessExecutionBudgetExhaustion(t *testing.T) {
	t.Parallel()
	for _, max := range []int{0, 1, 2} {
		t.Run(strconv.Itoa(max), func(t *testing.T) {
			t.Parallel()
			db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "retries.db"))
			p := executionFixture(t, ctx, db, []string{"a"}, nil, max)
			if _, err := db.ExecContext(
				ctx,
				`CREATE TRIGGER reject_steps
		BEFORE INSERT ON process_steps
		BEGIN SELECT RAISE(ABORT, 'unavailable'); END;`,
			); err != nil {
				t.Fatal(err)
			}
			executor := process.NewExecuteProcessesUseCase(
				sqlite.NewProcessRepository(db.DB),
				sqlite.NewProcessStepRepository(db.DB),
				sqlite.NewProcessDefinitionRepository(db.DB),
			)
			attempts := max
			if attempts == 0 {
				attempts = 3
			}
			for i := 1; i <= attempts; i++ {
				makeProcessesDue(t, ctx, db)
				if err := executor.Execute(ctx); err == nil {
					t.Fatal("error hidden")
				}
				stored, _ := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
				if stored.Reliability.Executions != i {
					t.Fatalf("executions=%d want %d", stored.Reliability.Executions, i)
				}
				if stored.State != domain.ProcessStatePending {
					t.Fatalf("failed before the next due pass: %+v", stored)
				}
			}
			if max == 0 {
				if _, err := db.ExecContext(ctx, "DROP TRIGGER reject_steps"); err != nil {
					t.Fatal(err)
				}
				runDueExecution(t, ctx, db)
				stored, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
				if err != nil || stored.State != domain.ProcessStatePending || stored.Reliability.Executions != 4 {
					t.Fatalf("unlimited process did not recover after repeated errors: %+v, %v", stored, err)
				}
				assertStepNames(t, ctx, db, p.ID, "a")

				return
			}
			runDueExecution(t, ctx, db)
			stored, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
			if err != nil || stored.State != domain.ProcessStateFailed || stored.Reliability.Executions != attempts+1 {
				t.Fatalf("execution budget not enforced: %+v, %v", stored, err)
			}
		})
	}
}

func TestProcessGraphValidationAndMissingDefinition(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		nodes     []string
		edges     [][2]string
		wantState domain.ProcessState
		wantSteps []string
	}{
		{"empty", nil, nil, domain.ProcessStateFailed, nil},
		{"cycle", []string{"a", "b"}, [][2]string{{"a", "b"}, {"b", "a"}}, domain.ProcessStatePending, nil},
		{
			"disconnected cycle",
			[]string{rootStepName, "a", "b"},
			[][2]string{{"a", "b"}, {"b", "a"}},
			domain.ProcessStatePending,
			[]string{rootStepName},
		},
		{
			"reachable cycle",
			[]string{rootStepName, "a", "b"},
			[][2]string{{rootStepName, "a"}, {"a", "b"}, {"b", "a"}},
			domain.ProcessStatePending,
			[]string{rootStepName},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "graph.db"))
			p := executionFixture(t, ctx, db, tc.nodes, tc.edges, 3)
			runDueExecution(t, ctx, db)
			stored, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.State != tc.wantState {
				t.Fatalf("state=%s want %s", stored.State, tc.wantState)
			}
			assertStepNames(t, ctx, db, p.ID, tc.wantSteps...)
		})
	}
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "missing.db"))
	p := processFixture(t, uuid.New())
	if err := sqlite.NewProcessRepository(db.DB).Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	runDueExecution(t, ctx, db)
	stored, _ := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if stored.State != domain.ProcessStateFailed {
		t.Fatal("missing definition stranded process")
	}
}

func TestProcessErrorDoesNotBlockOtherProcesses(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "isolation.db"))
	bad := executionFixture(t, ctx, db, []string{failingStepName}, nil, 2)
	good := executionFixture(t, ctx, db, []string{successfulStepName}, nil, 2)
	if _, err := db.ExecContext(
		ctx,
		`CREATE TRIGGER reject_bad
		BEFORE INSERT ON process_steps
		WHEN NEW.process_step_definition_name = 'bad'
		BEGIN SELECT RAISE(ABORT, 'unavailable'); END;`,
	); err != nil {
		t.Fatal(err)
	}
	executor := process.NewExecuteProcessesUseCase(
		sqlite.NewProcessRepository(db.DB),
		sqlite.NewProcessStepRepository(db.DB),
		sqlite.NewProcessDefinitionRepository(db.DB),
	)
	if err := executor.Execute(ctx); err == nil {
		t.Fatal("failed process error hidden")
	}
	assertStepNames(t, ctx, db, bad.ID)
	assertStepNames(t, ctx, db, good.ID, successfulStepName)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := executor.Execute(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func executionFixture(
	t *testing.T, ctx context.Context, db *sqlite.DB, names []string, edges [][2]string,
	maxRetries int, terminalNames ...string,
) *domain.Process {
	t.Helper()
	graph := domain.NewProcessGraph()
	for _, name := range names {
		node, err := domain.NewProcessStepDefinition(
			domain.WithProcessStepDefinitionName(name),
			domain.WithProcessStepDefinitionType("worker."+domain.ProcessStepType(name)),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := graph.AddNode(node); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range terminalNames {
		graph.Nodes[name].Type = domain.ProcessStepTypeTerminal
	}
	for _, edge := range edges {
		if err := graph.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	definition := newProcessDefinitionFixture(
		t,
		domain.WithProcessDefinitionName(uuid.New().String()),
		domain.WithProcessDefinitionGraph(*graph),
		domain.WithProcessDefinitionReliability(domain.ProcessDefinitionReliability{MaxRetries: maxRetries}),
	)
	definitions := sqlite.NewProcessDefinitionRepository(db.DB)
	if err := definitions.Save(ctx, definition); err != nil {
		t.Fatal(err)
	}
	p, err := process.NewCreateProcessUseCase(sqlite.NewProcessRepository(db.DB), definitions).
		Create(ctx, definition.ID, nil)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func runDueExecution(t *testing.T, ctx context.Context, db *sqlite.DB) {
	t.Helper()
	makeProcessesDue(t, ctx, db)
	if err := process.NewExecuteProcessesUseCase(
		sqlite.NewProcessRepository(db.DB),
		sqlite.NewProcessStepRepository(db.DB),
		sqlite.NewProcessDefinitionRepository(db.DB),
	).Execute(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertStepNames(t *testing.T, ctx context.Context, db *sqlite.DB, id uuid.UUID, want ...string) {
	t.Helper()
	rows, err := sqlite.NewProcessStepRepository(db.DB).GetByProcessID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.ProcessStepDefinitionName)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("steps=%v want %v", got, want)
	}
}

func setStepState(t *testing.T, ctx context.Context, db *sqlite.DB, id uuid.UUID, name, state string) {
	t.Helper()
	// A future worker owns this write; the engine only observes it.
	result, err := db.ExecContext(
		ctx,
		`UPDATE process_steps SET state = ?,
		 result = '{"error":{"type":"STRING","value":"worker failed"}}'
		WHERE process_id = ? AND process_step_definition_name = ?`,
		state,
		id.String(),
		name,
	)
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		t.Fatalf("worker update affected %d: %v", count, err)
	}
}

func TestProcessCompletionWriteFailureReturnsErrorUntilNextPass(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "lifecycle-write.db"))
	p := executionFixture(
		t,
		ctx,
		db,
		[]string{"a", terminalStepName},
		[][2]string{{"a", terminalStepName}},
		2,
		terminalStepName,
	)
	runDueExecution(t, ctx, db)
	setStepState(t, ctx, db, p.ID, "a", "COMPLETED")
	makeProcessesDue(t, ctx, db)
	if _, err := db.ExecContext(
		ctx,
		`CREATE TRIGGER reject_completion
		BEFORE UPDATE ON processes
		WHEN NEW.state = 'COMPLETED'
		BEGIN SELECT RAISE(ABORT, 'lifecycle write unavailable'); END;`,
	); err != nil {
		t.Fatal(err)
	}
	executor := process.NewExecuteProcessesUseCase(
		sqlite.NewProcessRepository(db.DB),
		sqlite.NewProcessStepRepository(db.DB),
		sqlite.NewProcessDefinitionRepository(db.DB),
	)
	if err := executor.Execute(ctx); err == nil {
		t.Fatal("lifecycle error hidden")
	}
	stored, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if err != nil || stored.State != domain.ProcessStatePending || stored.Reliability.Executions != 2 {
		t.Fatalf("completion error changed persisted state: %+v %v", stored, err)
	}
	runDueExecution(t, ctx, db)
	stored, err = sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if err != nil || stored.State != domain.ProcessStateFailed || stored.Reliability.Executions != 3 {
		t.Fatalf("next pass did not fail the exhausted process: %+v %v", stored, err)
	}
}

func makeProcessesDue(t *testing.T, ctx context.Context, db *sqlite.DB) {
	t.Helper()
	// Simulate the next due pass without sleeping for the acquisition delay.
	_, err := db.ExecContext(ctx, `UPDATE processes SET executed_at = ?
	 WHERE state = 'PENDING' AND executed_at IS NOT NULL`, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
}

func TestProcessLoweredRetryBudgetDoesNotStrand(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "lowered-budget.db"))
	p := executionFixture(t, ctx, db, []string{"a"}, nil, 3)
	if _, err := db.ExecContext(
		ctx,
		`CREATE TRIGGER reject_steps
		BEFORE INSERT ON process_steps
		BEGIN SELECT RAISE(ABORT, 'unavailable'); END;`,
	); err != nil {
		t.Fatal(err)
	}
	executor := process.NewExecuteProcessesUseCase(
		sqlite.NewProcessRepository(db.DB),
		sqlite.NewProcessStepRepository(db.DB),
		sqlite.NewProcessDefinitionRepository(db.DB),
	)
	if err := executor.Execute(ctx); err == nil {
		t.Fatal("expected insertion error")
	}
	if _, err := db.ExecContext(ctx, "DROP TRIGGER reject_steps"); err != nil {
		t.Fatal(err)
	}
	definitions := sqlite.NewProcessDefinitionRepository(db.DB)
	definition, err := definitions.GetByID(ctx, p.ProcessDefinitionID)
	if err != nil {
		t.Fatal(err)
	}
	definition.Reliability.MaxRetries = 1
	if err := definitions.Update(ctx, definition); err != nil {
		t.Fatal(err)
	}
	runDueExecution(t, ctx, db)
	stored, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if err != nil || stored.State != domain.ProcessStateFailed || stored.Reliability.Executions != 2 {
		t.Fatalf("lowered budget stranded process: %+v %v", stored, err)
	}
	assertStepNames(t, ctx, db, p.ID)
}

type failBeforeInsertSteps struct {
	*sqlite.ProcessStepRepository
	processes *sqlite.ProcessRepository
}

func (s failBeforeInsertSteps) Save(ctx context.Context, step *domain.ProcessStep) error {
	// Force a terminal transition after traversal, immediately before the real insert.
	p, err := s.processes.GetByID(ctx, step.ProcessID)
	if err != nil {
		return err
	}
	if _, err := process.NewFailProcessUseCase(s.processes).Fail(ctx, p, errors.New("concurrent failure")); err != nil {
		return err
	}

	return s.ProcessStepRepository.Save(ctx, step)
}

func TestProcessDoesNotQueueStepAfterConcurrentFailure(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "concurrent-terminal.db"))
	p := executionFixture(t, ctx, db, []string{"a"}, nil, 3)
	processes := sqlite.NewProcessRepository(db.DB)
	steps := sqlite.NewProcessStepRepository(db.DB)
	executor := process.NewExecuteProcessesUseCase(
		processes,
		failBeforeInsertSteps{steps, processes},
		sqlite.NewProcessDefinitionRepository(db.DB),
	)
	if err := executor.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := processes.GetByID(ctx, p.ID)
	if err != nil || stored.State != domain.ProcessStateFailed || stored.Reliability.Executions != 1 {
		t.Fatalf("concurrent failure overwritten: %+v %v", stored, err)
	}
	assertStepNames(t, ctx, db, p.ID)
}

type errorAfterConcurrentAcquisitionSteps struct {
	*sqlite.ProcessStepRepository
	processes *sqlite.ProcessRepository
	cause     error
}

func (s errorAfterConcurrentAcquisitionSteps) GetByProcessID(
	ctx context.Context, id uuid.UUID,
) ([]domain.ProcessStep, error) {
	p, err := s.processes.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := p.Acquire(time.Hour); err != nil {
		return nil, err
	}
	if err := s.processes.Update(ctx, p); err != nil {
		return nil, err
	}

	return nil, s.cause
}

func TestProcessErrorDoesNotModifyAnotherAcquisition(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "retry-conflict.db"))
	p := executionFixture(t, ctx, db, []string{"a"}, nil, 3)
	processes := sqlite.NewProcessRepository(db.DB)
	cause := errors.New("step read failed")
	steps := errorAfterConcurrentAcquisitionSteps{sqlite.NewProcessStepRepository(db.DB), processes, cause}
	executor := process.NewExecuteProcessesUseCase(processes, steps, sqlite.NewProcessDefinitionRepository(db.DB))
	if err := executor.Execute(ctx); !errors.Is(err, cause) {
		t.Fatalf("step read error: %v", err)
	}
	stored, err := processes.GetByID(ctx, p.ID)
	if err != nil || stored.Version != 3 || stored.Reliability.Executions != 2 ||
		stored.State != domain.ProcessStatePending {
		t.Fatalf("error handling changed the newer acquisition: %+v, %v", stored, err)
	}
	assertStepNames(t, ctx, db, p.ID)
}

type acquisitionObservingSteps struct {
	*sqlite.ProcessStepRepository
	processes *sqlite.ProcessRepository
	reads     atomic.Int32
}

func (s *acquisitionObservingSteps) GetByProcessID(ctx context.Context, id uuid.UUID) ([]domain.ProcessStep, error) {
	s.reads.Add(1)
	pending, err := s.processes.GetPending(ctx)
	if err != nil {
		return nil, err
	}
	if len(pending) != 0 {
		return nil, fmt.Errorf("traversal started with %d unacquired processes", len(pending))
	}

	return s.ProcessStepRepository.GetByProcessID(ctx, id)
}

func TestProcessAcquisitionSchedulesEveryCandidateBeforeTraversal(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "acquire.db"))
	first := executionFixture(t, ctx, db, []string{"a"}, nil, 3)
	second := executionFixture(t, ctx, db, []string{"b"}, nil, 3)
	repository := sqlite.NewProcessRepository(db.DB)
	steps := &acquisitionObservingSteps{
		ProcessStepRepository: sqlite.NewProcessStepRepository(db.DB), processes: repository,
	}
	executor := process.NewExecuteProcessesUseCase(repository, steps, sqlite.NewProcessDefinitionRepository(db.DB))
	before := time.Now().UTC()
	if err := executor.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		stored, err := repository.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		next := stored.Reliability.ExecutedAt
		if next == nil || next.Before(before.Add(time.Minute)) || next.After(after.Add(time.Minute)) {
			t.Fatalf("next execution = %v; want acquisition time + 1 minute", next)
		}
		if stored.Version != 2 || stored.Reliability.Executions != 1 || stored.State != domain.ProcessStatePending {
			t.Fatalf("acquired process: %+v", stored)
		}
	}
	if err := executor.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if steps.reads.Load() != 2 {
		t.Fatalf("traversals = %d; processes were executed before due", steps.reads.Load())
	}
	makeProcessesDue(t, ctx, db)
	if err := executor.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	if steps.reads.Load() != 4 {
		t.Fatalf("due processes not executed again: %d traversals", steps.reads.Load())
	}
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		stored, err := repository.GetByID(ctx, id)
		if err != nil || stored.Reliability.ExecutedAt == nil ||
			!stored.Reliability.ExecutedAt.After(time.Now().UTC()) {
			t.Fatalf("overdue process was not rescheduled in the future: %+v, %v", stored, err)
		}
	}
}

type acquisitionBarrierRepository struct {
	*sqlite.ProcessRepository
	ready   chan<- struct{}
	release <-chan struct{}
}

func (r acquisitionBarrierRepository) GetPending(ctx context.Context) ([]domain.Process, error) {
	pending, err := r.ProcessRepository.GetPending(ctx)
	if err != nil {
		return nil, err
	}
	r.ready <- struct{}{}
	select {
	case <-r.release:
		return pending, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestConcurrentExecutorsSkipLostAcquisition(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "concurrent-acquire.db"))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p := executionFixture(t, ctx, db, []string{"a"}, nil, 3)
	repository := sqlite.NewProcessRepository(db.DB)
	steps := &acquisitionObservingSteps{
		ProcessStepRepository: sqlite.NewProcessStepRepository(db.DB), processes: repository,
	}
	ready, release := make(chan struct{}, 2), make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		processes := acquisitionBarrierRepository{sqlite.NewProcessRepository(db.DB), ready, release}
		executor := process.NewExecuteProcessesUseCase(processes, steps, sqlite.NewProcessDefinitionRepository(db.DB))
		go func() { results <- executor.Execute(ctx) }()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("executors did not read the same pending snapshot")
		}
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	stored, err := repository.GetByID(ctx, p.ID)
	if err != nil || stored.Version != 2 || stored.Reliability.Executions != 1 || steps.reads.Load() != 1 {
		t.Fatalf("process executed more than once: %+v, reads=%d, error=%v", stored, steps.reads.Load(), err)
	}
	assertStepNames(t, ctx, db, p.ID, "a")
}

func TestProcessAcquisitionFailureSkipsProcessAndContinues(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "failed-acquire.db"))
	bad := executionFixture(t, ctx, db, []string{failingStepName}, nil, 3)
	good := executionFixture(t, ctx, db, []string{successfulStepName}, nil, 3)
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_acquisition BEFORE UPDATE ON processes
 WHEN OLD.id = '`+bad.ID.String()+`' BEGIN SELECT RAISE(ABORT, 'acquisition unavailable'); END;`); err != nil {
		t.Fatal(err)
	}
	repository := sqlite.NewProcessRepository(db.DB)
	executor := process.NewExecuteProcessesUseCase(
		repository,
		sqlite.NewProcessStepRepository(db.DB),
		sqlite.NewProcessDefinitionRepository(db.DB),
	)
	if err := executor.Execute(ctx); err == nil {
		t.Fatal("acquisition error hidden")
	}
	stored, err := repository.GetByID(ctx, bad.ID)
	if err != nil || stored.Version != 1 || stored.Reliability.Executions != 0 || stored.Reliability.ExecutedAt != nil {
		t.Fatalf("unacquired process changed: %+v, %v", stored, err)
	}
	assertStepNames(t, ctx, db, bad.ID)
	assertStepNames(t, ctx, db, good.ID, successfulStepName)
}

func assertCurrentSteps(t *testing.T, ctx context.Context, db *sqlite.DB, id uuid.UUID, want ...string) {
	t.Helper()
	p, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.CurrentSteps, want) {
		t.Fatalf("current steps=%v want %v", p.CurrentSteps, want)
	}
}

func TestProcessCurrentStepsResumeAfterRestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "current-steps.db")
	db, ctx := newProcessDefinitionTestDB(t, path)
	p := executionFixture(
		t,
		ctx,
		db,
		[]string{"a", "b", terminalStepName},
		[][2]string{{"a", "b"}, {"b", terminalStepName}},
		4,
		terminalStepName,
	)
	assertCurrentSteps(t, ctx, db, p.ID, "a")
	runDueExecution(t, ctx, db)
	setStepState(t, ctx, db, p.ID, "a", "COMPLETED")
	runDueExecution(t, ctx, db)
	assertCurrentSteps(t, ctx, db, p.ID, "b")
	if err := db.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	db, ctx = newProcessDefinitionTestDB(t, path)
	assertCurrentSteps(t, ctx, db, p.ID, "b")
	// Remove the old step to prove resumption does not replay the graph root.
	if _, err := db.ExecContext(
		ctx,
		`DELETE FROM process_steps WHERE process_id = ? AND process_step_definition_name = 'a'`,
		p.ID.String(),
	); err != nil {
		t.Fatal(err)
	}
	runDueExecution(t, ctx, db)
	assertStepNames(t, ctx, db, p.ID, "b")
	setStepState(t, ctx, db, p.ID, "b", "COMPLETED")
	runDueExecution(t, ctx, db)
	stored, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if err != nil || stored.State != domain.ProcessStateCompleted {
		t.Fatalf("resumed completion: %+v, %v", stored, err)
	}
	assertStepNames(t, ctx, db, p.ID, "b")
}

func TestProcessCurrentStepsWriteFailurePreservesProgressForRetry(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "progress-error.db"))
	p := executionFixture(t, ctx, db, []string{"a", "b"}, [][2]string{{"a", "b"}}, 3)
	runDueExecution(t, ctx, db)
	setStepState(t, ctx, db, p.ID, "a", "COMPLETED")
	makeProcessesDue(t, ctx, db)
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_progress BEFORE UPDATE ON processes
 WHEN NEW.current_steps != OLD.current_steps BEGIN SELECT RAISE(ABORT, 'progress unavailable'); END;`); err != nil {
		t.Fatal(err)
	}
	executor := process.NewExecuteProcessesUseCase(
		sqlite.NewProcessRepository(db.DB),
		sqlite.NewProcessStepRepository(db.DB),
		sqlite.NewProcessDefinitionRepository(db.DB),
	)
	if err := executor.Execute(ctx); err == nil {
		t.Fatal("progress persistence error hidden")
	}
	assertCurrentSteps(t, ctx, db, p.ID, "a")
	assertStepNames(t, ctx, db, p.ID, "a", "b")
	steps, err := sqlite.NewProcessStepRepository(db.DB).GetByProcessID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if err != nil || stored.State != domain.ProcessStatePending || stored.Reliability.Executions != 2 {
		t.Fatalf("progress retry: %+v, %v", stored, err)
	}
	if _, err := db.ExecContext(ctx, "DROP TRIGGER reject_progress"); err != nil {
		t.Fatal(err)
	}
	runDueExecution(t, ctx, db)
	assertCurrentSteps(t, ctx, db, p.ID, "b")
	after, err := sqlite.NewProcessStepRepository(db.DB).GetByProcessID(ctx, p.ID)
	if err != nil || len(after) != len(steps) || after[1].ID != steps[1].ID {
		t.Fatalf("progress retry recreated queued work: %+v, %v", after, err)
	}
}

func TestProcessEmptyCurrentStepsDoNotRestart(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "empty-progress.db"))
	p := executionFixture(t, ctx, db, []string{"a"}, nil, 3)
	runDueExecution(t, ctx, db)
	setStepState(t, ctx, db, p.ID, "a", "COMPLETED")
	runDueExecution(t, ctx, db)
	assertCurrentSteps(t, ctx, db, p.ID)
	if _, err := db.ExecContext(ctx, "DELETE FROM process_steps WHERE process_id = ?", p.ID.String()); err != nil {
		t.Fatal(err)
	}
	runDueExecution(t, ctx, db)
	assertStepNames(t, ctx, db, p.ID)
	stored, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if err != nil || stored.State != domain.ProcessStatePending {
		t.Fatalf("empty progress must still require a terminal: %+v, %v", stored, err)
	}
}

func TestProcessMissingCurrentStepFails(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "missing-current-step.db"))
	p := executionFixture(t, ctx, db, []string{"a"}, nil, 3)
	if err := p.UpdateCurrentSteps([]string{"removed"}); err != nil {
		t.Fatal(err)
	}
	if err := sqlite.NewProcessRepository(db.DB).Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	runDueExecution(t, ctx, db)
	stored, err := sqlite.NewProcessRepository(db.DB).GetByID(ctx, p.ID)
	if err != nil || stored.State != domain.ProcessStateFailed {
		t.Fatalf("missing current step: %+v, %v", stored, err)
	}
	assertStepNames(t, ctx, db, p.ID)
}

func TestProcessLifecycle(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "process.db")
	db, ctx := newProcessDefinitionTestDB(t, path)
	definitions := sqlite.NewProcessDefinitionRepository(db.DB)
	definition := newProcessDefinitionFixture(t)
	if err := definitions.Save(ctx, definition); err != nil {
		t.Fatal(err)
	}
	repository := sqlite.NewProcessRepository(db.DB)
	create := process.NewCreateProcessUseCase(repository, definitions)
	inputs := testParameters(map[string]domain.ParameterValue{
		sourceParameterName: {Type: domain.ParameterTypeString, Value: "hello"},
	})
	created, err := create.Create(ctx, definition.ID, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == uuid.Nil() || created.State != domain.ProcessStatePending || created.Version != 1 ||
		created.CreatedAt.IsZero() ||
		!reflect.DeepEqual(created.Context.Inputs, inputs) {
		t.Fatalf("created: %+v", created)
	}
	if created.Reliability.Executions != 0 {
		t.Fatal("new process consumed retries")
	}
	if _, err := create.Create(ctx, uuid.New(), nil); !errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		t.Fatalf("missing definition: %v", err)
	}

	nextExecution := time.Now().UTC().Add(time.Minute)
	changes := &process.ProcessUpdateParams{
		Version: created.Version,
		Context: &domain.ProcessContext{Inputs: testParameters(map[string]domain.ParameterValue{
			sourceParameterName: {Type: domain.ParameterTypeString, Value: "updated"},
		})},
		Result: testParameters(map[string]domain.ParameterValue{
			"answer": {Type: domain.ParameterTypeString, Value: "partial"},
		}),
		CurrentSteps: []string{"queued"},
		Reliability: domain.ProcessReliability{
			Executions:    1,
			ExecutionTime: time.Second,
			ExecutedAt:    &nextExecution,
		},
	}
	update := process.NewUpdateProcessUseCase(repository)
	updated, err := update.Update(ctx, created.ID, changes)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID || updated.State != domain.ProcessStatePending ||
		updated.ProcessDefinitionID != definition.ID ||
		updated.Version != 2 ||
		updated.Context.Inputs.Get(sourceParameterName).Value != "updated" ||
		!reflect.DeepEqual(updated.Result, changes.Result) ||
		!reflect.DeepEqual(updated.CurrentSteps, changes.CurrentSteps) ||
		!reflect.DeepEqual(updated.Reliability, changes.Reliability) ||
		!updated.UpdatedAt.After(created.UpdatedAt) ||
		!updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("update: %+v", updated)
	}
	if _, err := update.Update(ctx, created.ID, nil); err == nil {
		t.Fatal("update without parameters accepted")
	}
	if _, err := update.Update(
		ctx,
		created.ID,
		&process.ProcessUpdateParams{Version: created.Version},
	); !errors.Is(err, domain.ErrProcessVersionConflict) {
		t.Fatalf("stale update: %v", err)
	}
	invalid := &process.ProcessUpdateParams{
		Version:     updated.Version,
		Reliability: domain.ProcessReliability{Executions: -1},
	}
	if _, err := update.Update(ctx, updated.ID, invalid); err == nil {
		t.Fatal("invalid update accepted")
	}
	unchanged, err := repository.GetByID(ctx, updated.ID)
	if err != nil || !reflect.DeepEqual(unchanged, updated) {
		t.Fatalf("invalid update changed persisted process: %+v, %v", unchanged, err)
	}
	result := testParameters(map[string]domain.ParameterValue{"answer": {Type: domain.ParameterTypeString, Value: "done"}})
	completed, err := process.NewCompleteProcessUseCase(repository).Complete(ctx, updated, result)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != domain.ProcessStateCompleted || completed.Version != 3 ||
		completed.Reliability.ExecutionTime <= 0 ||
		!reflect.DeepEqual(completed.Result, result) {
		t.Fatalf("completion: %+v", completed)
	}
	if updated.State != domain.ProcessStateCompleted || !reflect.DeepEqual(updated.Result, result) {
		t.Fatal("completion did not mutate the supplied model")
	}
	if _, err := process.NewFailProcessUseCase(repository).Fail(
		ctx,
		unchanged,
		errors.New("stale failure"),
	); !errors.Is(err, domain.ErrProcessVersionConflict) {
		t.Fatalf("stale failure: %v", err)
	}
	if _, err := update.Update(
		ctx,
		completed.ID,
		&process.ProcessUpdateParams{Version: completed.Version},
	); !errors.Is(err, domain.ErrProcessTerminal) {
		t.Fatalf("terminal update: %v", err)
	}
	if _, err := process.NewCompleteProcessUseCase(repository).Complete(
		ctx,
		completed,
		nil,
	); !errors.Is(err, domain.ErrProcessTerminal) {
		t.Fatalf("terminal completion: %v", err)
	}
	if err := db.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, reopenCtx := newProcessDefinitionTestDB(t, path)
	persisted, err := sqlite.NewProcessRepository(reopened.DB).GetByID(reopenCtx, completed.ID)
	if err != nil || !reflect.DeepEqual(persisted, completed) {
		t.Fatalf("reopen: %+v %v", persisted, err)
	}
}

func TestProcessFailureAndRepositoryErrors(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "fail.db"))
	repository := sqlite.NewProcessRepository(db.DB)
	original := processFixture(t, uuid.New())
	if err := repository.Save(ctx, original); err != nil {
		t.Fatal(err)
	}
	fail := process.NewFailProcessUseCase(repository)
	if _, err := fail.Fail(ctx, original, nil); err == nil {
		t.Fatal("failure without details accepted")
	}
	failed, err := fail.Fail(ctx, original, errors.New("worker exhausted retries"))
	if err != nil {
		t.Fatal(err)
	}
	if failed.State != domain.ProcessStateFailed || failed.Result.Get("error").Value != "worker exhausted retries" {
		t.Fatalf("failure: %+v", failed)
	}
	if original.State != domain.ProcessStateFailed {
		t.Fatal("failure did not mutate the supplied model")
	}
	if _, err := repository.GetByID(ctx, uuid.New()); !errors.Is(err, domain.ErrProcessNotFound) {
		t.Fatalf("missing: %v", err)
	}
	missing := processFixture(t, uuid.New())
	if err := repository.Update(ctx, missing); !errors.Is(err, domain.ErrProcessNotFound) {
		t.Fatalf("missing update: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repository.GetPending(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled query: %v", err)
	}
	if err := repository.Save(cancelled, missing); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save: %v", err)
	}
	if got, err := repository.GetPending(ctx); err != nil || len(got) != 0 {
		t.Fatalf("terminal selected: %+v %v", got, err)
	}
}

func TestUpdateProcessDefinition(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "update.db")
	db, ctx := newProcessDefinitionTestDB(t, path)
	repository := sqlite.NewProcessDefinitionRepository(db.DB)
	now := time.Now().UTC()
	original := newProcessDefinitionFixture(t,
		domain.WithProcessDefinitionCreatedAt(now.Add(-time.Hour)),
		domain.WithProcessDefinitionUpdatedAt(now.Add(-time.Minute)),
	)
	if err := repository.Save(ctx, original); err != nil {
		t.Fatal(err)
	}

	changes := newProcessDefinitionFixture(t,
		domain.WithProcessDefinitionName("Updated release"),
		domain.WithProcessDefinitionDescription("New description"),
		domain.WithProcessDefinitionState(domain.ProcessDefinitionStateDraft),
		domain.WithProcessDefinitionVersion(original.Version),
		domain.WithProcessDefinitionGraph(*domain.NewProcessGraph()),
		domain.WithProcessDefinitionInputs(testParameters(map[string]domain.ParameterValue{
			"message": {Type: domain.ParameterTypeString},
		})),
		domain.WithProcessDefinitionReliability(domain.ProcessDefinitionReliability{Timeout: time.Second}),
	)
	useCase := processdefinition.NewUpdateProcessDefinitionUseCase(repository)
	updated, err := useCase.Update(ctx, original.ID, changes)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != original.ID || updated.State != original.State || updated.Version != original.Version+1 ||
		!updated.CreatedAt.Equal(original.CreatedAt) ||
		!updated.UpdatedAt.After(original.UpdatedAt) {
		t.Fatalf("update changed identity/lifecycle metadata: %+v", updated)
	}
	if updated.Name != changes.Name || updated.Description != changes.Description ||
		!reflect.DeepEqual(updated.Graph, changes.Graph) ||
		!reflect.DeepEqual(updated.Inputs, changes.Inputs) ||
		updated.Reliability != changes.Reliability {
		t.Fatalf("content not updated: %+v", updated)
	}
	definitions, err := repository.GetAll(ctx)
	if err != nil || len(definitions) != 1 {
		t.Fatalf("update inserted a new definition: %+v %v", definitions, err)
	}
	if _, err := useCase.Update(ctx, uuid.New(), changes); !errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		t.Fatalf("missing update: %v", err)
	}
	missing := newProcessDefinitionFixture(t, domain.WithNewProcessDefinitionID())
	if err := repository.Update(ctx, missing); !errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		t.Fatalf("missing repository update: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := repository.Update(cancelled, updated); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled update: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`CREATE TRIGGER reject_update
		BEFORE UPDATE ON process_definitions
		BEGIN SELECT RAISE(ABORT, 'update unavailable'); END;`,
	); err != nil {
		t.Fatal(err)
	}
	failedChanges, err := repository.GetByID(ctx, updated.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := useCase.Update(ctx, original.ID, failedChanges); err == nil {
		t.Fatal("storage failure hidden")
	}
	if err := db.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, reopenedCtx := newProcessDefinitionTestDB(t, path)
	persisted, err := sqlite.NewProcessDefinitionRepository(reopened.DB).GetByID(reopenedCtx, original.ID)
	if err != nil || !reflect.DeepEqual(updated, persisted) {
		t.Fatalf("update not preserved after reopening: %+v %v", persisted, err)
	}
}

func TestCreateProcessDefinitionDefaultsAndUniqueName(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "unique.db"))
	repository := sqlite.NewProcessDefinitionRepository(db.DB)
	create := processdefinition.NewCreateProcessDefinitionUseCase(repository)
	first := newProcessDefinitionFixture(
		t,
		domain.WithProcessDefinitionState(domain.ProcessDefinitionStateArchived),
		domain.WithProcessDefinitionVersion(7),
	)
	created, err := create.Create(ctx, first)
	if err != nil || created.State != domain.ProcessDefinitionStateActive || created.Version != 1 {
		t.Fatalf("creation defaults: %+v %v", created, err)
	}
	duplicate := newProcessDefinitionFixture(t, domain.WithProcessDefinitionState(domain.ProcessDefinitionStateDraft))
	if _, err := create.Create(ctx, duplicate); !errors.Is(err, domain.ErrProcessDefinitionDuplicateName) {
		t.Fatalf("duplicate name accepted: %v", err)
	}
	archived := newProcessDefinitionFixture(t, domain.WithProcessDefinitionState(domain.ProcessDefinitionStateArchived))
	if err := repository.Save(ctx, archived); err != nil {
		t.Fatalf("same name in different state rejected: %v", err)
	}
	other := newProcessDefinitionFixture(t, domain.WithProcessDefinitionName("Other"))
	if err := repository.Save(ctx, other); err != nil {
		t.Fatal(err)
	}
	other.Name = created.Name
	if err := repository.Update(ctx, other); !errors.Is(err, domain.ErrProcessDefinitionDuplicateName) {
		t.Fatalf("duplicate name on update accepted: %v", err)
	}
	unchanged, err := repository.GetByID(ctx, other.ID)
	if err != nil || unchanged.Name != "Other" || unchanged.Version != other.Version {
		t.Fatalf("failed rename changed record: %+v %v", unchanged, err)
	}
}

func TestProcessDefinitionOptimisticLock(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "lock.db"))
	repository := sqlite.NewProcessDefinitionRepository(db.DB)
	now := time.Now().UTC()
	definition := newProcessDefinitionFixture(
		t,
		domain.WithProcessDefinitionCreatedAt(now.Add(-time.Hour)),
		domain.WithProcessDefinitionUpdatedAt(now.Add(-time.Minute)),
	)
	if err := repository.Save(ctx, definition); err != nil {
		t.Fatal(err)
	}
	first, err := repository.GetByID(ctx, definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := repository.GetByID(ctx, definition.ID)
	if err != nil {
		t.Fatal(err)
	}
	first.Name = "First writer"
	stale.Name = "Stale writer"
	if err := repository.Update(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := repository.Update(ctx, stale); !errors.Is(err, domain.ErrProcessDefinitionVersionConflict) {
		t.Fatalf("stale repository write accepted: %v", err)
	}
	useCase := processdefinition.NewUpdateProcessDefinitionUseCase(repository)
	if _, err := useCase.Update(ctx, stale.ID, stale); !errors.Is(err, domain.ErrProcessDefinitionVersionConflict) {
		t.Fatalf("stale use case write accepted: %v", err)
	}
	persisted, err := repository.GetByID(ctx, definition.ID)
	if err != nil || persisted.Name != first.Name || persisted.Version != definition.Version+1 {
		t.Fatalf("incorrect persisted version: %+v %v", persisted, err)
	}
	persisted.Name = "Next writer"
	updated, err := useCase.Update(ctx, persisted.ID, persisted)
	if err != nil || updated.Version != definition.Version+2 {
		t.Fatalf("next version: %+v %v", updated, err)
	}
}

func TestDeleteProcessDefinition(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "delete.db")
	db, ctx := newProcessDefinitionTestDB(t, path)
	repository := sqlite.NewProcessDefinitionRepository(db.DB)
	definition := newProcessDefinitionFixture(t)
	if err := repository.Save(ctx, definition); err != nil {
		t.Fatal(err)
	}
	useCase := processdefinition.NewDeleteProcessDefinitionUseCase(repository)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := useCase.Delete(cancelled, definition.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled delete: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`CREATE TRIGGER reject_delete
		BEFORE DELETE ON process_definitions
		BEGIN SELECT RAISE(ABORT, 'delete unavailable'); END;`,
	); err != nil {
		t.Fatal(err)
	}
	if err := useCase.Delete(ctx, definition.ID); err == nil {
		t.Fatal("delete failure hidden")
	}
	if _, err := repository.GetByID(ctx, definition.ID); err != nil {
		t.Fatal("failed deletion removed record")
	}
	if _, err := db.ExecContext(ctx, "DROP TRIGGER reject_delete"); err != nil {
		t.Fatal(err)
	}
	if err := useCase.Delete(ctx, definition.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetByID(ctx, definition.ID); !errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		t.Fatalf("deleted record still exists: %v", err)
	}
	var count int
	if err := db.QueryRowContext(
		ctx,
		"SELECT count(*) FROM process_definitions WHERE id = ?",
		definition.ID.String(),
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("not a hard delete: %d %v", count, err)
	}
	if err := useCase.Delete(ctx, definition.ID); !errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		t.Fatalf("missing delete: %v", err)
	}
	if err := db.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, reopenedCtx := newProcessDefinitionTestDB(t, path)
	repository = sqlite.NewProcessDefinitionRepository(reopened.DB)
	if _, err := repository.GetByID(reopenedCtx, definition.ID); !errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		t.Fatalf("deletion not persisted: %v", err)
	}
	replacement := newProcessDefinitionFixture(t)
	if _, err := processdefinition.NewCreateProcessDefinitionUseCase(repository).Create(
		reopenedCtx,
		replacement,
	); err != nil {
		t.Fatalf("deleted name cannot be reused: %v", err)
	}
}

func newProcessDefinitionTestDB(t *testing.T, path string) (*sqlite.DB, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	db, err := sqlite.NewDB(&sqlite.SqliteConfig{Path: path, MaxOpenConns: 2, Pragmas: sqlite.PragmasSettings})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := db.Start(ctx); err != nil {
		t.Fatal(err)
	}

	return db, ctx
}

func newProcessDefinitionFixture(t *testing.T, opts ...domain.ProcessDefinitionOpt) *domain.ProcessDefinition {
	t.Helper()
	createdAt := time.Date(2026, time.October, 6, 10, 0, 0, 123456789, time.UTC)
	reliability, err := domain.NewProcessDefinitionReliability(
		domain.WithProcessDefinitionReliabilityTimeout(time.Minute),
		domain.WithProcessDefinitionReliabilityMaxRetries(2),
		domain.WithProcessDefinitionReliabilityRetryDelay(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	graph := domain.NewProcessGraph()
	for _, worker := range []string{"custom.build.v2", "custom.review.v3"} {
		step, err := domain.NewProcessStepDefinition(
			domain.WithProcessStepDefinitionName(worker),
			domain.WithProcessStepDefinitionType(domain.ProcessStepType(worker)),
			domain.WithProcessStepDefinitionReliability(*reliability),
			domain.WithProcessStepDefinitionParams(
				testParameters(map[string]domain.ParameterValue{
					sourceParameterName: {Type: domain.ParameterTypeReference, Required: true},
				}),
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := graph.AddNode(step); err != nil {
			t.Fatal(err)
		}
	}
	names := make([]string, 0, len(graph.Nodes))
	for name := range graph.Nodes {
		names = append(names, name)
	}
	if err := graph.AddEdge(names[0], names[1]); err != nil {
		t.Fatal(err)
	}
	if err := graph.AddEdge(names[1], names[0]); err != nil {
		t.Fatal(err)
	}
	defaults := []domain.ProcessDefinitionOpt{
		domain.WithNewProcessDefinitionID(),
		domain.WithProcessDefinitionName("Release '🚀'"),
		domain.WithProcessDefinitionDescription("Build\nReview"),
		domain.WithProcessDefinitionState(domain.ProcessDefinitionStateActive),
		domain.WithProcessDefinitionVersion(3),
		domain.WithProcessDefinitionGraph(*graph),
		domain.WithProcessDefinitionReliability(*reliability),
		domain.WithProcessDefinitionInputs(testParameters(map[string]domain.ParameterValue{
			"command": {Type: domain.ParameterTypeString, Required: true},
			"sources": {Type: domain.ParameterTypeReferencesArray},
		})),
		domain.WithProcessDefinitionCreatedAt(createdAt),
		domain.WithProcessDefinitionUpdatedAt(createdAt.Add(time.Minute)),
	}
	model, err := domain.NewProcessDefinition(append(defaults, opts...)...)
	if err != nil {
		t.Fatal(err)
	}

	return model
}

func processFixture(t *testing.T, definitionID uuid.UUID) *domain.Process {
	t.Helper()
	now := time.Now().UTC()
	p, err := domain.NewProcess(
		domain.WithNewProcessID(),
		domain.WithProcessState(domain.ProcessStatePending),
		domain.WithProcessProcessDefinitionID(definitionID),
		domain.WithProcessVersion(1),
		domain.WithProcessCreatedAt(now),
		domain.WithProcessUpdatedAt(now),
	)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

func testParameters(values map[string]domain.ParameterValue) *domain.Parameters {
	params, err := domain.NewParameters(values)
	if err != nil {
		panic(err)
	}

	return params
}
