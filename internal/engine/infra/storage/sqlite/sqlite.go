package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"go-software-factory/internal/core/validator"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/mattn/go-sqlite3"
)

type SqliteConfig struct {
	Path         string            `validate:"required"`
	MaxOpenConns int               `validate:"required,min=1,max=32"`
	Pragmas      map[string]string `validate:"required"`
}

const migrationTimeout = 5 * time.Minute

var errDBNotOpen = errors.New("db is not open")
var errContextExecutionUnsupported = errors.New("sqlite connection does not support context execution")

//go:embed migrations
var migrationsFS embed.FS

var PragmasSettings = map[string]string{
	"journal_mode": "WAL",
	"synchronous":  "FULL",
	"busy_timeout": "5000",
	"foreign_keys": "ON",
	"temp_store":   "MEMORY",
	"cache_size":   "-2000", // 2 MB
}

var PragmasMessages = map[string]string{
	"journal_mode":       "WAL",
	"synchronous":        "NORMAL",
	"busy_timeout":       "5000",
	"temp_store":         "MEMORY",
	"cache_size":         "-8000",    // 8 MB
	"mmap_size":          "67108864", // 64 MB
	"wal_autocheckpoint": "1000",
}

type DB struct {
	*sql.DB
	dbURL string
}

func NewDB(cfg *SqliteConfig) (*DB, error) {
	validated, err := validator.Struct(cfg)
	if err != nil {
		return nil, fmt.Errorf("validate db config: %w", err)
	}

	path, err := filepath.Abs(validated.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite database path: %w", err)
	}
	databaseURI := (&url.URL{Scheme: "file", Path: path}).String()
	sqldb := open(databaseURI, validated.Pragmas)
	sqldb.SetMaxOpenConns(validated.MaxOpenConns)
	sqldb.SetMaxIdleConns(validated.MaxOpenConns)

	q := url.Values{}
	for name, value := range validated.Pragmas {
		q.Set("_"+name, value)
	}

	return &DB{DB: sqldb, dbURL: "sqlite3://" + databaseURI + "?" + q.Encode()}, nil
}

func (d *DB) Start(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, migrationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.Ping(ctx); err != nil {
		return err
	}

	return d.runMigrations(ctx)
}

func (d *DB) Stop(_ context.Context) error {
	if d.DB == nil {
		return nil
	}
	if err := d.Close(); err != nil {
		return fmt.Errorf("close db: %w", err)
	}

	return nil
}

func (d *DB) Ping(ctx context.Context) error {
	if d.DB == nil {
		return errDBNotOpen
	}
	if err := d.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	return nil
}

func open(dsn string, pragmas map[string]string) *sql.DB {
	q := url.Values{}
	if value, ok := pragmas["busy_timeout"]; ok {
		q.Set("_busy_timeout", value)
	}
	if enc := q.Encode(); enc != "" {
		dsn += "?" + enc
	}

	return sql.OpenDB(&sqliteConnector{dsn: dsn, pragmas: pragmas})
}

// sqliteConnector applies all configured pragmas to every pooled connection.
// go-sqlite3's DSN only supports a subset of SQLite pragmas.
type sqliteConnector struct {
	dsn     string
	pragmas map[string]string
}

func (c *sqliteConnector) Driver() driver.Driver {
	return &sqlite3.SQLiteDriver{}
}

func (c *sqliteConnector) Connect(ctx context.Context) (driver.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.Driver().Open(c.dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite connection: %w", err)
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		_ = conn.Close()

		return nil, errContextExecutionUnsupported
	}
	for name, value := range c.pragmas {
		query := fmt.Sprintf(
			`PRAGMA "%s" = '%s'`,
			strings.ReplaceAll(name, `"`, `""`),
			strings.ReplaceAll(value, "'", "''"),
		)
		if _, err := execer.ExecContext(ctx, query, nil); err != nil {
			_ = conn.Close()

			return nil, fmt.Errorf("set pragma %s: %w", name, err)
		}
	}

	return conn, nil
}

func (d *DB) runMigrations(ctx context.Context) (err error) {
	source, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("open sqlite migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", source, d.dbURL)
	if err != nil {
		_ = source.Close()

		return fmt.Errorf("initialize sqlite migrations: %w", err)
	}
	defer func() {
		sourceErr, databaseErr := m.Close()
		if closeErr := errors.Join(sourceErr, databaseErr); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close sqlite migrations: %w", closeErr))
		}
	}()

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply sqlite migrations: %w", err)
	}

	return ctx.Err()
}
