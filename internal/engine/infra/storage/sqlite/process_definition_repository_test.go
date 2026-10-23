package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"
)

func TestProcessDefinitionRepositoryPersistence(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "factory.db")
	db, ctx := newProcessDefinitionTestDB(t, path)
	repository := NewProcessDefinitionRepository(db.DB)
	want := newProcessDefinitionFixture(t)
	if err := repository.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := repository.GetByID(ctx, want.ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %+v, %v; want %+v", got, err, want)
	}
	var graphType, reliabilityType, inputsType string
	if err := db.QueryRowContext(ctx, `
		SELECT typeof(graph), typeof(reliability), typeof(inputs)
		FROM process_definitions WHERE id = ?
	`, want.ID.String()).Scan(&graphType, &reliabilityType, &inputsType); err != nil {
		t.Fatal(err)
	}
	if graphType != sqliteTextType || reliabilityType != sqliteTextType || inputsType != sqliteTextType {
		t.Fatalf("JSON storage types = (%s, %s, %s), want TEXT", graphType, reliabilityType, inputsType)
	}

	if _, err := db.ExecContext(ctx, `
		CREATE TRIGGER reject_definition_update BEFORE UPDATE ON process_definitions
		BEGIN SELECT RAISE(ABORT, 'unexpected update'); END;
	`); err != nil {
		t.Fatal(err)
	}
	duplicate := newProcessDefinitionFixture(t,
		domain.WithProcessDefinitionID(want.ID),
		domain.WithProcessDefinitionName("Ignored duplicate"),
		domain.WithProcessDefinitionDescription(""),
		domain.WithProcessDefinitionState(domain.ProcessDefinitionStateArchived),
		domain.WithProcessDefinitionVersion(want.Version+1),
		domain.WithProcessDefinitionGraph(*domain.NewProcessGraph()),
		domain.WithProcessDefinitionReliability(domain.ProcessDefinitionReliability{}),
		domain.WithProcessDefinitionInputs(nil),
		domain.WithProcessDefinitionCreatedAt(want.CreatedAt.Add(time.Minute)),
		domain.WithProcessDefinitionUpdatedAt(want.UpdatedAt.Add(time.Hour)),
	)
	if err := repository.Save(ctx, duplicate); err != nil {
		t.Fatal(err)
	}
	got, err = repository.GetByID(ctx, want.ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("duplicate insert changed original: %+v, %v; want %+v", got, err, want)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM process_definitions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate insert created %d rows, want 1", count)
	}
	if err := db.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, reopenedCtx := newProcessDefinitionTestDB(t, path)
	repository = NewProcessDefinitionRepository(reopened.DB)
	got, err = repository.GetByID(reopenedCtx, want.ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("after reopening = %+v, %v; want %+v", got, err, want)
	}
	if err := repository.Save(reopenedCtx, duplicate); err != nil {
		t.Fatal(err)
	}
	// Empty maps and nil maps must retain their distinct JSON representations.
	for i, inputs := range []*domain.Parameters{nil, {}} {
		definition := newProcessDefinitionFixture(t,
			domain.WithProcessDefinitionGraph(*domain.NewProcessGraph()),
			domain.WithProcessDefinitionReliability(domain.ProcessDefinitionReliability{}),
			domain.WithProcessDefinitionInputs(inputs),
			domain.WithProcessDefinitionName(fmt.Sprintf("Optional inputs %d", i)),
		)
		if err := repository.Save(reopenedCtx, definition); err != nil {
			t.Fatal(err)
		}
		got, err = repository.GetByID(reopenedCtx, definition.ID)
		if err != nil || !reflect.DeepEqual(got, definition) {
			t.Fatalf("optional fields round trip = %+v, %v; want %+v", got, err, definition)
		}
	}
}

func TestProcessDefinitionRepositoryGetAll(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "factory.db"))
	repository := NewProcessDefinitionRepository(db.DB)
	if got, err := repository.GetAll(ctx); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty get all = %+v, %v; want empty slice", got, err)
	}

	states := []domain.ProcessDefinitionState{
		domain.ProcessDefinitionStateDraft,
		domain.ProcessDefinitionStateActive,
		domain.ProcessDefinitionStateArchived,
	}
	want := make(map[uuid.UUID]domain.ProcessDefinition)
	for i := 0; i < 105; i++ {
		definition := newProcessDefinitionFixture(
			t,
			domain.WithProcessDefinitionState(states[i%len(states)]),
			domain.WithProcessDefinitionName(fmt.Sprintf("Definition %d", i)),
		)
		if err := repository.Save(ctx, definition); err != nil {
			t.Fatal(err)
		}
		want[definition.ID] = *definition
	}
	got, err := repository.GetAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("get all returned %d definitions, want %d", len(got), len(want))
	}
	for _, definition := range got {
		expected, ok := want[definition.ID]
		if !ok || !reflect.DeepEqual(definition, expected) {
			t.Fatalf("get all returned unexpected definition: %+v", definition)
		}
		delete(want, definition.ID)
	}
}

func TestProcessDefinitionRepositoryMissingAndCancelled(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "factory.db"))
	repository := NewProcessDefinitionRepository(db.DB)
	if got, err := repository.GetByID(ctx, uuid.New()); got != nil ||
		!errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		t.Fatalf("missing ID = %+v, %v; want domain.ErrProcessDefinitionNotFound", got, err)
	}
	cancelledCtx, cancel := context.WithTimeout(ctx, time.Second)
	cancel()
	want := newProcessDefinitionFixture(t)
	if err := repository.Save(cancelledCtx, want); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save = %v, want context.Canceled", err)
	}
	if got, err := repository.GetByID(cancelledCtx, want.ID); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled get = %+v, %v; want context.Canceled", got, err)
	}
	if got, err := repository.GetAll(cancelledCtx); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled get all = %+v, %v; want context.Canceled", got, err)
	}
	if got, err := repository.GetByID(ctx, want.ID); got != nil ||
		!errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		t.Fatalf("cancelled save persisted a row: %+v, %v", got, err)
	}
}

func TestProcessDefinitionRepositoryRejectsInvalidJSONTypes(t *testing.T) {
	t.Parallel()
	for column, value := range map[string]string{
		"graph":       `[]`,
		"reliability": `[]`,
		"inputs":      `[]`,
	} {
		t.Run(column, func(t *testing.T) {
			t.Parallel()
			db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "factory.db"))
			repository := NewProcessDefinitionRepository(db.DB)
			want := newProcessDefinitionFixture(t)
			if err := repository.Save(ctx, want); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(
				ctx,
				"UPDATE process_definitions SET "+column+" = ? WHERE id = ?",
				value,
				want.ID.String(),
			); err != nil {
				t.Fatal(err)
			}
			if got, err := repository.GetByID(ctx, want.ID); got != nil || err == nil {
				t.Fatalf("accepted invalid stored %s: %+v, %v", column, got, err)
			}
			if got, err := repository.GetAll(ctx); got != nil || err == nil {
				t.Fatalf("get all accepted invalid stored %s: %+v, %v", column, got, err)
			}
		})
	}
}

func TestProcessDefinitionSchemaJSONChecks(t *testing.T) {
	t.Parallel()
	db, ctx := newProcessDefinitionTestDB(t, filepath.Join(t.TempDir(), "factory.db"))
	want := newProcessDefinitionFixture(t)
	if err := NewProcessDefinitionRepository(db.DB).Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	for _, assignment := range []string{
		"graph = 'invalid JSON'",
		"reliability = 'invalid JSON'",
		"inputs = 'invalid JSON'",
	} {
		if _, err := db.ExecContext(
			ctx,
			"UPDATE process_definitions SET "+assignment+" WHERE id = ?",
			want.ID.String(),
		); err == nil {
			t.Errorf("schema accepted %s", assignment)
		}
	}
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

func testParameters(values map[string]domain.ParameterValue) *domain.Parameters {
	params, err := domain.NewParameters(values)
	if err != nil {
		panic(err)
	}

	return params
}
