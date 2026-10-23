package storage

import (
	"context"
	"errors"
	"fmt"

	"go-software-factory/internal/core/app/processstep"
	"go-software-factory/internal/engine/app/auth"
	"go-software-factory/internal/engine/app/process"
	"go-software-factory/internal/engine/app/processdefinition"
	"go-software-factory/internal/engine/infra/storage/sqlite"
)

type Storage struct {
	mode                        StorageMode
	sqliteDB                    *sqlite.DB
	ProcessDefinitionRepository processdefinition.ProcessDefinitionRepository
	UserRepository              auth.UserRepository
	ProcessRepository           process.ProcessRepository
	ProcessStepRepository       processstep.ProcessStepRepository
}

type StorageMode string

const (
	StorageModeSqlite   = "sqlite"
	StorageModePostgres = "postgres"
)

type StorageConfig struct {
	Mode           StorageMode
	SqliteConfig   *sqlite.SqliteConfig
	DatabaseConfig *DatabaseConfig
}

type DatabaseConfig struct {
	DbUrl            string
	MigrationsFolder string
}

var errStorageMode = errors.New("unsupported storage mode")

func NewStorage(cfg *StorageConfig) (*Storage, error) {
	if cfg.Mode != StorageModeSqlite {
		return nil, fmt.Errorf("%w: %s", errStorageMode, cfg.Mode)
	}

	sqliteDB, err := sqlite.NewDB(cfg.SqliteConfig)
	if err != nil {
		return nil, err
	}

	return &Storage{
		mode:                        cfg.Mode,
		ProcessRepository:           sqlite.NewProcessRepository(sqliteDB.DB),
		ProcessStepRepository:       sqlite.NewProcessStepRepository(sqliteDB.DB),
		UserRepository:              sqlite.NewUserRepository(sqliteDB.DB),
		sqliteDB:                    sqliteDB,
		ProcessDefinitionRepository: sqlite.NewProcessDefinitionRepository(sqliteDB.DB),
	}, nil
}

func (s *Storage) Start(ctx context.Context) error {
	if s.mode != StorageModeSqlite {
		return fmt.Errorf("%w: %s", errStorageMode, s.mode)
	}

	if err := s.sqliteDB.Start(ctx); err != nil {
		return err
	}

	return s.sqliteDB.Ping(ctx)
}

func (s *Storage) Stop(ctx context.Context) error {
	if s.mode != StorageModeSqlite {
		return fmt.Errorf("%w: %s", errStorageMode, s.mode)
	}

	return s.sqliteDB.Stop(ctx)
}

func (s *Storage) Name() string {
	return "storage"
}
