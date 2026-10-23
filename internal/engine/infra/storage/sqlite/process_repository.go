package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"go-software-factory/internal/core/app/outbox"
	"go-software-factory/internal/core/domain"
)

type ProcessRepository struct{ db *sql.DB }

func NewProcessRepository(db *sql.DB) *ProcessRepository {
	return &ProcessRepository{db: db}
}

const processColumns = `p.id, p.state, p.process_definition_id, p.current_steps, json(p.context), p.result,
 p.executions, p.execution_time, p.executed_at, p.version, p.created_at, p.updated_at`

func (r *ProcessRepository) GetAll(ctx context.Context) ([]domain.Process, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+processColumns+`
  FROM processes p ORDER BY p.created_at DESC, p.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("query process history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	processes := make([]domain.Process, 0)
	for rows.Next() {
		processes = append(processes, domain.Process{})
		if err := scanProcess(rows, &processes[len(processes)-1]); err != nil {
			return nil, fmt.Errorf("scan process history: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate process history: %w", err)
	}

	return processes, nil
}

func (r *ProcessRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Process, error) {
	model := &domain.Process{}
	err := scanProcess(r.db.QueryRowContext(
		ctx,
		`SELECT `+processColumns+` FROM processes p WHERE p.id = ?`,
		id.String(),
	), model)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrProcessNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get process %s: %w", id, err)
	}

	return model, nil
}

func (r *ProcessRepository) Save(ctx context.Context, model *domain.Process) error {
	currentSteps, err := json.Marshal(model.CurrentSteps)
	if err != nil {
		return fmt.Errorf("encode process current steps: %w", err)
	}
	contextData, err := json.Marshal(model.Context)
	if err != nil {
		return fmt.Errorf("encode process context: %w", err)
	}
	result, err := json.Marshal(model.Result)
	if err != nil {
		return fmt.Errorf("encode process result: %w", err)
	}
	var executedAt sql.NullTime
	if model.Reliability.ExecutedAt != nil {
		executedAt = sql.NullTime{Time: model.Reliability.ExecutedAt.UTC(), Valid: true}
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO processes
  (id, state, process_definition_id, current_steps, context, result,
   executions, execution_time, executed_at, version, created_at, updated_at)
  VALUES (?, ?, ?, ?, jsonb(?), ?, ?, ?, ?, ?, ?, ?)`,
		model.ID.String(), model.State, model.ProcessDefinitionID.String(),
		string(currentSteps), string(contextData), string(result),
		model.Reliability.Executions, model.Reliability.ExecutionTime, executedAt,
		model.Version, model.CreatedAt, model.UpdatedAt)
	if err != nil {
		return fmt.Errorf("save process %s: %w", model.ID, err)
	}

	return nil
}

func (r *ProcessRepository) Update(ctx context.Context, model *domain.Process) error {
	currentSteps, err := json.Marshal(model.CurrentSteps)
	if err != nil {
		return fmt.Errorf("encode process current steps: %w", err)
	}
	contextData, err := json.Marshal(model.Context)
	if err != nil {
		return fmt.Errorf("encode process context: %w", err)
	}
	result, err := json.Marshal(model.Result)
	if err != nil {
		return fmt.Errorf("encode process result: %w", err)
	}
	var executedAt sql.NullTime
	if model.Reliability.ExecutedAt != nil {
		executedAt = sql.NullTime{Time: model.Reliability.ExecutedAt.UTC(), Valid: true}
	}
	res, err := r.db.ExecContext(ctx, `UPDATE processes
  SET state = ?, current_steps = ?, context = jsonb(?), result = ?, executions = ?,
   execution_time = ?, executed_at = ?, updated_at = ?, version = version + 1
  WHERE id = ? AND version = ? AND state IN ('PENDING', 'IN_PROGRESS')`,
		model.State, string(currentSteps), string(contextData), string(result),
		model.Reliability.Executions, model.Reliability.ExecutionTime, executedAt,
		model.UpdatedAt, model.ID.String(), model.Version)
	if err != nil {
		return fmt.Errorf("update process %s: %w", model.ID, err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated processes: %w", err)
	}
	if count == 0 {
		existing, err := r.GetByID(ctx, model.ID)
		if err != nil {
			return err
		}
		if existing.Version != model.Version {
			return fmt.Errorf("%w: %w", domain.ErrProcessVersionConflict, outbox.ErrOutboxClaimLost)
		}

		return fmt.Errorf("%w: %w", domain.ErrProcessTerminal, outbox.ErrOutboxClaimLost)
	}

	model.Version++

	return nil
}

// GetPending selects due processes independently of their definition or retry budget.
func (r *ProcessRepository) GetPending(ctx context.Context) ([]domain.Process, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+processColumns+`
  FROM processes p
  WHERE p.state = 'PENDING'
   AND (p.executed_at IS NULL OR p.executed_at <= ?)
  ORDER BY p.created_at, p.id`, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("query processes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	processes := make([]domain.Process, 0)
	for rows.Next() {
		processes = append(processes, domain.Process{})
		if err := scanProcess(rows, &processes[len(processes)-1]); err != nil {
			return nil, fmt.Errorf("scan pending process: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending processes: %w", err)
	}

	return processes, nil
}

func scanProcess(row interface{ Scan(...any) error }, model *domain.Process) error {
	var id, definitionID, currentSteps, contextData, result string
	if err := row.Scan(
		&id,
		&model.State,
		&definitionID,
		&currentSteps,
		&contextData,
		&result,
		&model.Reliability.Executions,
		&model.Reliability.ExecutionTime,
		&model.Reliability.ExecutedAt,
		&model.Version,
		&model.CreatedAt,
		&model.UpdatedAt,
	); err != nil {
		return fmt.Errorf("scan process: %w", err)
	}
	var err error
	if model.ID, err = uuid.Parse(id); err != nil {
		return fmt.Errorf("decode process ID: %w", err)
	}
	if model.ProcessDefinitionID, err = uuid.Parse(definitionID); err != nil {
		return fmt.Errorf("decode process definition ID: %w", err)
	}
	if err := json.Unmarshal([]byte(currentSteps), &model.CurrentSteps); err != nil {
		return fmt.Errorf("decode process current steps: %w", err)
	}
	if err := json.Unmarshal([]byte(contextData), &model.Context); err != nil {
		return fmt.Errorf("decode process context: %w", err)
	}
	if err := json.Unmarshal([]byte(result), &model.Result); err != nil {
		return fmt.Errorf("decode process result: %w", err)
	}
	if err := model.Validate(); err != nil {
		return err
	}

	return nil
}
