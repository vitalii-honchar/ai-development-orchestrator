package sqlite

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/core/app/outbox"
	"go-software-factory/internal/core/domain"
)

func TestProcessRepositoryStoresContextAsJSONB(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "process-context.db"))
	repository := NewProcessRepository(db.DB)
	model := processFixture(t, uuid.New())
	model.Context = &domain.ProcessContext{
		Inputs: testParameters(map[string]domain.ParameterValue{
			"description": {Type: domain.ParameterTypeString, Value: "Build a tracker"},
		}),
		StepOutputs: map[string]*domain.Parameters{
			"brainstorm": testParameters(map[string]domain.ParameterValue{
				testOutputParameter: {Type: domain.ParameterTypeJSON, Value: `{"path_to_plan":"/project/plan.md"}`},
			}),
		},
	}
	if err := repository.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	var storageType string
	var validJSONB bool
	if err := db.QueryRowContext(ctx,
		"SELECT typeof(context), json_valid(context, 8) FROM processes WHERE id=?", model.ID.String(),
	).Scan(&storageType, &validJSONB); err != nil || storageType != "blob" || !validJSONB {
		t.Fatalf("context storage: %s, valid=%t, error=%v", storageType, validJSONB, err)
	}
	stored, err := repository.GetByID(ctx, model.ID)
	if err != nil || !reflect.DeepEqual(stored.Context, model.Context) {
		t.Fatalf("context round trip: %+v, %v", stored, err)
	}
	model.Context.StepOutputs["implement"] = testParameters(map[string]domain.ParameterValue{
		testOutputParameter: {Type: domain.ParameterTypeJSON, Value: `{"summary":"Implemented"}`},
	})
	if err := repository.Update(ctx, model); err != nil {
		t.Fatal(err)
	}
	pending, err := repository.GetPending(ctx)
	if err != nil || len(pending) != 1 || !reflect.DeepEqual(pending[0].Context, model.Context) {
		t.Fatalf("updated context: %+v, %v", pending, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE processes SET context='{}' WHERE id=?", model.ID.String()); err == nil {
		t.Fatal("accepted plain JSON text in the JSONB column")
	}
}

func TestGetAllProcessesIncludesTerminalStatesAndOrdersNewestFirst(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "all-processes.db"))
	repository := NewProcessRepository(db.DB)
	empty, err := repository.GetAll(ctx)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty history: %v, %v", empty, err)
	}
	created := time.Now().UTC().Add(-time.Hour)
	states := []domain.ProcessState{domain.ProcessStateCompleted, domain.ProcessStateFailed, domain.ProcessStatePending}
	ids := make([]uuid.UUID, 0, len(states))
	for i, state := range states {
		p := processFixture(t, uuid.New())
		p.State = state
		p.CreatedAt = created.Add(time.Duration(i) * time.Minute)
		p.UpdatedAt = p.CreatedAt
		p.Context = &domain.ProcessContext{Inputs: testParameters(map[string]domain.ParameterValue{
			"message": {Type: domain.ParameterTypeString, Value: "hello"},
		})}
		if state == domain.ProcessStateFailed {
			p.Result = testParameters(map[string]domain.ParameterValue{
				testErrorParameter: {Type: domain.ParameterTypeString, Value: "test failure"},
			})
		}
		if err := repository.Save(ctx, p); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	rows, err := repository.GetAll(ctx)
	if err != nil || len(rows) != 3 {
		t.Fatalf("history: %v, %v", rows, err)
	}
	for i := range rows {
		if rows[i].ID != ids[2-i] || rows[i].State != states[2-i] || rows[i].Context.Inputs.String("message") != "hello" {
			t.Fatalf("history row %d: %+v", i, rows[i])
		}
	}
	if rows[1].Result.String(testErrorParameter) != "test failure" {
		t.Fatal("failed process result was lost")
	}
	if _, err := repository.GetAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetAll(ctx); err == nil {
		t.Fatal("closed storage returned a successful history")
	}
}

func TestPendingProcessesExecutionTimeEligibility(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "pending.db"))
	repository := NewProcessRepository(db.DB)
	definitions := NewProcessDefinitionRepository(db.DB)
	definition := newProcessDefinitionFixture(
		t,
		domain.WithProcessDefinitionReliability(domain.ProcessDefinitionReliability{
			MaxRetries: 1,
			RetryDelay: time.Hour,
		}),
	)
	if err := definitions.Save(ctx, definition); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	pastWithOffset := past.In(time.FixedZone("offset", 3*60*60))
	for _, tc := range []struct {
		name                        string
		executedAt                  *time.Time
		state                       domain.ProcessState
		failures                    int
		missingDefinition, eligible bool
	}{
		{name: "never executed", state: domain.ProcessStatePending, eligible: true},
		{name: "due", executedAt: &past, state: domain.ProcessStatePending, eligible: true},
		{name: "due now", executedAt: &now, state: domain.ProcessStatePending, eligible: true},
		{name: "due with offset", executedAt: &pastWithOffset, state: domain.ProcessStatePending, eligible: true},
		{name: "future", executedAt: &future, state: domain.ProcessStatePending},
		{name: "exhausted still selected", state: domain.ProcessStatePending, failures: 5, eligible: true},
		{
			name:       "retry delay not queried",
			executedAt: &past,
			state:      domain.ProcessStatePending,
			failures:   1,
			eligible:   true,
		},
		{name: "missing definition", state: domain.ProcessStatePending, missingDefinition: true, eligible: true},
		{name: "completed", state: domain.ProcessStateCompleted},
		{name: "failed", state: domain.ProcessStateFailed},
		{name: "in progress", state: domain.ProcessStateInProgress},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := processFixture(t, definition.ID)
			if tc.missingDefinition {
				p.ProcessDefinitionID = uuid.New()
			}
			p.State = tc.state
			p.Reliability.Executions = tc.failures
			p.Reliability.ExecutedAt = tc.executedAt
			if p.State == domain.ProcessStateFailed {
				p.Result = testParameters(map[string]domain.ParameterValue{
					testErrorParameter: {Type: domain.ParameterTypeString, Value: "failed"},
				})
			}
			if err := repository.Save(ctx, p); err != nil {
				t.Fatal(err)
			}
			if tc.executedAt == nil {
				var isNull bool
				if err := db.QueryRowContext(
					ctx,
					"SELECT executed_at IS NULL FROM processes WHERE id = ?",
					p.ID.String(),
				).Scan(&isNull); err != nil || !isNull {
					t.Fatalf("unset executed_at must be SQL NULL: %v, %v", isNull, err)
				}
			}
			rows, err := repository.GetPending(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows {
				if row.ID == p.ID {
					found = true
				}
			}
			if found != tc.eligible {
				t.Fatalf("selected=%v want %v", found, tc.eligible)
			}
		})
	}
}

func TestProcessReliabilityColumns(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "reliability-columns.db"))
	repository := NewProcessRepository(db.DB)
	p := processFixture(t, uuid.New())
	next := time.Now().UTC().Add(time.Minute)
	p.Reliability = domain.ProcessReliability{Executions: 2, ExecutionTime: time.Second, ExecutedAt: &next}
	if err := repository.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	var executions int
	var executionTime time.Duration
	var executedAt time.Time
	if err := db.QueryRowContext(
		ctx,
		"SELECT executions, execution_time, executed_at FROM processes WHERE id = ?",
		p.ID.String(),
	).Scan(
		&executions,
		&executionTime,
		&executedAt,
	); err != nil {
		t.Fatal(err)
	}
	if executions != 2 || executionTime != time.Second || !executedAt.Equal(next) {
		t.Fatalf("stored reliability: %d, %v, %v", executions, executionTime, executedAt)
	}
	stored, err := repository.GetByID(ctx, p.ID)
	if err != nil || !reflect.DeepEqual(stored, p) {
		t.Fatalf("reliability round trip: %+v, %v", stored, err)
	}
	p.Reliability = domain.ProcessReliability{Executions: 3, ExecutionTime: 2 * time.Second}
	if err := repository.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	stored, err = repository.GetByID(ctx, p.ID)
	if err != nil || !reflect.DeepEqual(stored.Reliability, p.Reliability) {
		t.Fatalf("reliability update: %+v, %v", stored, err)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA index_info(processes_state_executed_at_idx)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	var columns []string
	for rows.Next() {
		var sequence, columnID int
		var column string
		if err := rows.Scan(&sequence, &columnID, &column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, []string{"state", "executed_at"}) {
		t.Fatalf("pending index columns: %v", columns)
	}
}

func TestProcessUpdateRejectsLostClaims(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		terminal bool
		want     error
	}{
		{name: "stale version", want: domain.ErrProcessVersionConflict},
		{name: "terminal process", terminal: true, want: domain.ErrProcessTerminal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "lost-claims.db"))
			repository := NewProcessRepository(db.DB)
			model := processFixture(t, uuid.New())
			if tc.terminal {
				if err := model.Complete(nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := repository.Save(ctx, model); err != nil {
				t.Fatal(err)
			}
			stale, err := repository.GetByID(ctx, model.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.terminal {
				if err := repository.Update(ctx, model); err != nil {
					t.Fatal(err)
				}
			}
			if err := repository.Update(ctx, stale); !errors.Is(err, tc.want) ||
				!errors.Is(err, outbox.ErrOutboxClaimLost) {
				t.Fatalf("lost claim=%v want %v and %v", err, tc.want, outbox.ErrOutboxClaimLost)
			}
		})
	}
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

const (
	testOutputParameter = "output"
	testErrorParameter  = "error"
)
