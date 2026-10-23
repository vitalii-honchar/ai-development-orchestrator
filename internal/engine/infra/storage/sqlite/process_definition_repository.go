package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"

	"github.com/mattn/go-sqlite3"
)

type ProcessDefinitionRepository struct {
	db *sql.DB
}

func NewProcessDefinitionRepository(db *sql.DB) *ProcessDefinitionRepository {
	return &ProcessDefinitionRepository{db: db}
}

func (p *ProcessDefinitionRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.ProcessDefinition, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, name, description, state, version, graph, reliability, inputs, created_at, updated_at
		FROM process_definitions
		WHERE id = ?
	`, id.String())
	definition, err := p.scanProcessDefinition(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get process definition %s: %w", id, domain.ErrProcessDefinitionNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get process definition %s: %w", id, err)
	}

	return definition, nil
}

func (p *ProcessDefinitionRepository) Save(ctx context.Context, processDefinition *domain.ProcessDefinition) error {
	graphJSON, err := json.Marshal(processDefinition.Graph)
	if err != nil {
		return fmt.Errorf("encode process definition graph: %w", err)
	}
	reliabilityJSON, err := json.Marshal(processDefinition.Reliability)
	if err != nil {
		return fmt.Errorf("encode process definition reliability: %w", err)
	}
	inputsJSON, err := json.Marshal(processDefinition.Inputs)
	if err != nil {
		return fmt.Errorf("encode process definition inputs: %w", err)
	}

	_, err = p.db.ExecContext(ctx, `
		INSERT INTO process_definitions
		 (id, name, description, state, version, graph, reliability, inputs, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, processDefinition.ID.String(), processDefinition.Name, processDefinition.Description,
		processDefinition.State, processDefinition.Version,
		string(graphJSON), string(reliabilityJSON), string(inputsJSON),
		processDefinition.CreatedAt, processDefinition.UpdatedAt)
	if err != nil {
		return fmt.Errorf("save process definition %s: %w", processDefinition.ID, p.writeError(err))
	}

	return nil
}

func (p *ProcessDefinitionRepository) GetAll(ctx context.Context) ([]domain.ProcessDefinition, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, name, description, state, version, graph, reliability, inputs, created_at, updated_at
		FROM process_definitions
	`)
	if err != nil {
		return nil, fmt.Errorf("get all process definitions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	definitions := make([]domain.ProcessDefinition, 0)
	for rows.Next() {
		definition, err := p.scanProcessDefinition(rows)
		if err != nil {
			return nil, fmt.Errorf("get all process definitions: %w", err)
		}
		definitions = append(definitions, *definition)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate process definitions: %w", err)
	}

	return definitions, nil
}

func (p *ProcessDefinitionRepository) Update(ctx context.Context, definition *domain.ProcessDefinition) error {
	graphJSON, err := json.Marshal(definition.Graph)
	if err != nil {
		return fmt.Errorf("encode process definition graph: %w", err)
	}
	reliabilityJSON, err := json.Marshal(definition.Reliability)
	if err != nil {
		return fmt.Errorf("encode process definition reliability: %w", err)
	}
	inputsJSON, err := json.Marshal(definition.Inputs)
	if err != nil {
		return fmt.Errorf("encode process definition inputs: %w", err)
	}

	result, err := p.db.ExecContext(ctx, `
		UPDATE process_definitions
		SET name = ?, description = ?, graph = ?, reliability = ?, inputs = ?, updated_at = ?, version = version + 1
		WHERE id = ? AND version = ?
	`, definition.Name, definition.Description, string(graphJSON), string(reliabilityJSON),
		string(inputsJSON), definition.UpdatedAt, definition.ID.String(), definition.Version)
	if err != nil {
		return fmt.Errorf("update process definition %s: %w", definition.ID, p.writeError(err))
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update process definition %s: %w", definition.ID, p.writeError(err))
	}
	if count == 0 {
		if _, err := p.GetByID(ctx, definition.ID); err != nil {
			return err
		}

		return domain.ErrProcessDefinitionVersionConflict
	}

	return nil
}

func (p *ProcessDefinitionRepository) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := p.db.ExecContext(ctx, "DELETE FROM process_definitions WHERE id = ?", id.String())
	if err != nil {
		return fmt.Errorf("delete process definition %s: %w", id, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete process definition %s: %w", id, err)
	}
	if count == 0 {
		return domain.ErrProcessDefinitionNotFound
	}

	return nil
}

func (p *ProcessDefinitionRepository) writeError(err error) error {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique {
		return fmt.Errorf("%w: %w", domain.ErrProcessDefinitionDuplicateName, err)
	}

	return err
}

func (p *ProcessDefinitionRepository) scanProcessDefinition(
	row interface{ Scan(...any) error },
) (*domain.ProcessDefinition, error) {
	var storedID string
	var name, description string
	var state domain.ProcessDefinitionState
	var version int
	var graphJSON, reliabilityJSON, inputsJSON string
	var createdAt, updatedAt time.Time
	err := row.Scan(
		&storedID,
		&name,
		&description,
		&state,
		&version,
		&graphJSON,
		&reliabilityJSON,
		&inputsJSON,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan process definition: %w", err)
	}
	id, err := uuid.Parse(storedID)
	if err != nil {
		return nil, fmt.Errorf("decode process definition ID: %w", err)
	}

	var graph domain.ProcessGraph
	if err := json.Unmarshal([]byte(graphJSON), &graph); err != nil {
		return nil, fmt.Errorf("decode process definition graph: %w", err)
	}
	var reliability domain.ProcessDefinitionReliability
	if err := json.Unmarshal([]byte(reliabilityJSON), &reliability); err != nil {
		return nil, fmt.Errorf("decode process definition reliability: %w", err)
	}
	var inputs *domain.Parameters
	if err := json.Unmarshal([]byte(inputsJSON), &inputs); err != nil {
		return nil, fmt.Errorf("decode process definition inputs: %w", err)
	}

	return domain.NewProcessDefinition(
		domain.WithProcessDefinitionID(id),
		domain.WithProcessDefinitionName(name),
		domain.WithProcessDefinitionDescription(description),
		domain.WithProcessDefinitionState(state),
		domain.WithProcessDefinitionVersion(version),
		domain.WithProcessDefinitionGraph(graph),
		domain.WithProcessDefinitionReliability(reliability),
		domain.WithProcessDefinitionInputs(inputs),
		domain.WithProcessDefinitionCreatedAt(createdAt),
		domain.WithProcessDefinitionUpdatedAt(updatedAt),
	)
}
