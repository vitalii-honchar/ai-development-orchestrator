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

type ProcessStepRepository struct{ db *sql.DB }

func NewProcessStepRepository(db *sql.DB) *ProcessStepRepository {
	return &ProcessStepRepository{db: db}
}

const stepColumns = `s.id, s.state, s.process_id, s.process_step_definition_name, s.type,
 s.params, s.result, s.reliability, s.execution_policy, s.version, s.created_at, s.updated_at, s.context`

// Save queues a ready step once, without overwriting worker-owned state.
func (r *ProcessStepRepository) Save(ctx context.Context, step *domain.ProcessStep) error {
	if err := step.Validate(); err != nil {
		return err
	}
	params, result, reliability, policy, contextData, err := encodeStep(step)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO process_steps
 (id,state,process_id,process_step_definition_name,type,params,result,reliability,
 execution_policy,version,created_at,updated_at,executed_at,context)
 SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,?
 WHERE EXISTS (SELECT 1 FROM processes WHERE id=? AND state='PENDING')
 ON CONFLICT(process_id,process_step_definition_name) DO NOTHING`,
		step.ID.String(), step.State, step.ProcessID.String(), step.ProcessStepDefinitionName, step.Type,
		params, result, reliability, policy, step.Version, step.CreatedAt, step.UpdatedAt,
		stepExecutedAt(step), contextData, step.ProcessID.String())
	if err != nil {
		return fmt.Errorf("save process step %s: %w", step.ID, err)
	}

	return nil
}

func (r *ProcessStepRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.ProcessStep, error) {
	step := &domain.ProcessStep{}
	err := scanStep(r.db.QueryRowContext(ctx,
		`SELECT `+stepColumns+` FROM process_steps s WHERE s.id=?`,
		id.String()), step)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrProcessStepNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get process step %s: %w", id, err)
	}

	return step, nil
}

func (r *ProcessStepRepository) GetByProcessID(ctx context.Context, id uuid.UUID) ([]domain.ProcessStep, error) {
	return r.query(ctx, `SELECT `+stepColumns+` FROM process_steps s
 WHERE s.process_id=? ORDER BY s.process_step_definition_name`, id.String())
}

func (r *ProcessStepRepository) GetPending(ctx context.Context) ([]domain.ProcessStep, error) {
	return r.query(ctx, `SELECT `+stepColumns+` FROM process_steps s
 JOIN processes p ON p.id=s.process_id
 WHERE s.state='PENDING' AND p.state='PENDING'
 AND (s.executed_at IS NULL OR s.executed_at<=?) ORDER BY s.created_at,s.id`, time.Now().UTC())
}

func (r *ProcessStepRepository) Update(ctx context.Context, step *domain.ProcessStep) error {
	if err := step.Validate(); err != nil {
		return err
	}
	_, result, reliability, _, _, err := encodeStep(step)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE process_steps SET state=?,result=?,reliability=?,
 executed_at=?,updated_at=?,version=version+1
 WHERE id=? AND version=? AND state='PENDING'`,
		step.State, result, reliability, stepExecutedAt(step), step.UpdatedAt, step.ID.String(), step.Version)
	if err != nil {
		return fmt.Errorf("update process step %s: %w", step.ID, err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated steps: %w", err)
	}
	if count == 0 {
		return r.updateConflict(ctx, step)
	}
	step.Version++

	return nil
}

func (r *ProcessStepRepository) updateConflict(ctx context.Context, step *domain.ProcessStep) error {
	stored, err := r.GetByID(ctx, step.ID)
	if err != nil {
		return err
	}
	if stored.Version != step.Version {
		return fmt.Errorf("%w: %w", domain.ErrProcessStepVersionConflict, outbox.ErrOutboxClaimLost)
	}

	return fmt.Errorf("%w: %w", domain.ErrProcessStepTerminal, outbox.ErrOutboxClaimLost)
}

func (r *ProcessStepRepository) query(ctx context.Context, query string, args ...any) ([]domain.ProcessStep, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query process steps: %w", err)
	}
	defer func() { _ = rows.Close() }()
	steps := make([]domain.ProcessStep, 0)
	for rows.Next() {
		steps = append(steps, domain.ProcessStep{})
		if err := scanStep(rows, &steps[len(steps)-1]); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate process steps: %w", err)
	}

	return steps, nil
}

func scanStep(row interface{ Scan(...any) error }, step *domain.ProcessStep) error {
	var id, processID, params, result, reliability, policy, contextData string
	if err := row.Scan(&id, &step.State, &processID, &step.ProcessStepDefinitionName, &step.Type,
		&params, &result, &reliability, &policy, &step.Version, &step.CreatedAt, &step.UpdatedAt, &contextData); err != nil {
		return fmt.Errorf("scan process step: %w", err)
	}
	var err error
	if step.ID, err = uuid.Parse(id); err != nil {
		return fmt.Errorf("decode step ID: %w", err)
	}
	if step.ProcessID, err = uuid.Parse(processID); err != nil {
		return fmt.Errorf("decode step process ID: %w", err)
	}
	for _, field := range []struct {
		data   string
		target any
	}{
		{contextData, &step.Context},
		{params,
			&step.Params},
		{result,
			&step.Result},
		{reliability,
			&step.Reliability},
		{policy,
			&step.ExecutionPolicy},
	} {
		if err := json.Unmarshal([]byte(field.data), field.target); err != nil {
			return fmt.Errorf("decode step fields: %w", err)
		}
	}

	return step.Validate()
}

func encodeStep(step *domain.ProcessStep) (params, result, reliability, policy, contextData string, err error) {
	values := []any{step.Params, step.Result, step.Reliability, step.ExecutionPolicy, step.Context}
	encoded := make([]string, len(values))
	for i, value := range values {
		data, encodeErr := json.Marshal(value)
		if encodeErr != nil {
			return "", "", "", "", "", fmt.Errorf("encode process step: %w", encodeErr)
		}
		encoded[i] = string(data)
	}

	return encoded[0], encoded[1], encoded[2], encoded[3], encoded[4], nil
}

func stepExecutedAt(step *domain.ProcessStep) sql.NullTime {
	if step.Reliability.ExecutedAt == nil {
		return sql.NullTime{}
	}

	return sql.NullTime{Time: step.Reliability.ExecutedAt.UTC(), Valid: true}
}
