package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
)

func TestStartAppliesEmbeddedMigrationsOnce(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfg := &SqliteConfig{
		Path:         filepath.Join(t.TempDir(), "factory database.db"),
		MaxOpenConns: 1,
		Pragmas:      PragmasSettings,
	}
	db, err := NewDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Stop(ctx)
	})
	if err := db.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var version int
	var dirty bool
	if err := db.QueryRowContext(ctx, "SELECT version, dirty FROM schema_migrations").Scan(
		&version,
		&dirty,
	); err != nil {
		t.Fatal(err)
	}
	if version != 6 || dirty {
		t.Fatalf("migration state = (%d, %t), want (6, false)", version, dirty)
	}
	// A second start must skip applied migrations rather than rewrite their version.
	_, err = db.ExecContext(ctx, `
		CREATE TRIGGER reject_version_delete BEFORE DELETE ON schema_migrations
		BEGIN SELECT RAISE(ABORT, 'migration replayed'); END;
		CREATE TRIGGER reject_version_insert BEFORE INSERT ON schema_migrations
		BEGIN SELECT RAISE(ABORT, 'migration replayed'); END;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Start(ctx); err != nil {
		t.Fatalf("repeat start: %v", err)
	}
	if err := db.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	if err := reopened.Start(ctx); err != nil {
		t.Fatalf("start after reopening database: %v", err)
	}
	if err := reopened.Ping(ctx); err != nil {
		t.Fatalf("database must remain open after migrations: %v", err)
	}
}

func TestStartRejectsDirtyDatabase(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := NewDB(&SqliteConfig{
		Path:         filepath.Join(t.TempDir(), "factory.db"),
		MaxOpenConns: 1,
		Pragmas:      PragmasSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	if err := db.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE schema_migrations SET dirty = true"); err != nil {
		t.Fatal(err)
	}
	var dirtyErr migrate.ErrDirty
	if err := db.Start(ctx); !errors.As(err, &dirtyErr) || dirtyErr.Version != 6 {
		t.Fatalf("start dirty database = %v, want dirty version 6", err)
	}
}

func TestStartRejectsCancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := NewDB(&SqliteConfig{
		Path:         filepath.Join(t.TempDir(), "factory.db"),
		MaxOpenConns: 1,
		Pragmas:      PragmasSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	cancel()
	if err := db.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("start with cancelled context = %v, want context.Canceled", err)
	}
}

func TestProcessContextMigrationPreservesExistingData(t *testing.T) {
	t.Parallel()
	db, err := NewDB(&SqliteConfig{Path: filepath.Join(t.TempDir(), "legacy.db"),
		MaxOpenConns: 1, Pragmas: PragmasSettings})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Stop(t.Context()) })
	for _, name := range []string{"004_processes.up.sql", "005_process_step_context.up.sql"} {
		source, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(t.Context(), string(source)); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.ExecContext(t.Context(), `
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, dirty BOOLEAN NOT NULL);
INSERT INTO schema_migrations VALUES (5, false);
INSERT INTO processes (id,state,process_definition_id,current_steps,inputs,result,
 executions,execution_time,version,created_at,updated_at)
VALUES ('process','PENDING','definition','[]',
 '{"description":{"type":"STRING","value":"feature"}}','null',0,0,1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
INSERT INTO process_steps (id,state,process_id,process_step_definition_name,type,params,result,
 reliability,execution_policy,version,created_at,updated_at)
VALUES ('done','COMPLETED','process','brainstorm','AI_CODING_AGENT','null',
 '{"output":{"type":"JSON","value":"{\"path_to_plan\":\"/project/plan.md\"}"}}',
 '{}','{}',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),
 ('waiting','PENDING','process','implement','AI_CODING_AGENT','null','null',
 '{}','{}',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	var inputs, output, storageType string
	var pending, oldColumn int
	if err := db.QueryRowContext(t.Context(), `SELECT typeof(context),
 json_extract(context,'$.inputs.description.value'),
 json_extract(context,'$.step_outputs.brainstorm.output.value'),
 json_type(context,'$.step_outputs.implement') IS NOT NULL,
 (SELECT count(*) FROM pragma_table_info('processes') WHERE name='inputs') FROM processes`).Scan(
		&storageType, &inputs, &output, &pending, &oldColumn,
	); err != nil {
		t.Fatal(err)
	}
	if storageType != "blob" || inputs != "feature" || output != `{"path_to_plan":"/project/plan.md"}` ||
		pending != 0 || oldColumn != 0 {
		t.Fatalf("migrated context: %s, %s, %s, pending=%d, old inputs=%d",
			storageType, inputs, output, pending, oldColumn)
	}
}

func TestPragmasApplyToEveryConnection(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := NewDB(&SqliteConfig{
		Path:         filepath.Join(t.TempDir(), "factory.db"),
		MaxOpenConns: 2,
		Pragmas:      PragmasSettings,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Stop(ctx); err != nil {
			t.Error(err)
		}
	}()
	first, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := first.Close(); err != nil {
			t.Error(err)
		}
	}()
	second, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	}()
	for name, want := range map[string]int{
		"foreign_keys": 1,
		"synchronous":  2,
		"busy_timeout": 5000,
		"temp_store":   2,
		"cache_size":   -2000,
	} {
		for i, conn := range []*sql.Conn{first, second} {
			var got int
			if err := conn.QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("connection %d pragma %s = %d, want %d", i, name, got, want)
			}
		}
	}
}

const (
	sqliteTextType      = "text"
	sourceParameterName = "source"
	adminUsername       = "admin"
)

func newProcessDefinitionTestDB(t *testing.T, path string) (*DB, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	db, err := NewDB(&SqliteConfig{Path: path, MaxOpenConns: 2, Pragmas: PragmasSettings})
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
