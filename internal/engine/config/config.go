package config

import (
	"errors"
	"fmt"
	"go-software-factory/internal/core/config"
	"go-software-factory/internal/engine/infra/storage"
	"go-software-factory/internal/engine/infra/storage/sqlite"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	AdminUsername          string
	AdminPassword          string
	Production             bool
	Address                string
	Storage                *storage.StorageConfig
	DataDir                string
	DefinitionsDir         string
	AllowedRoots           []string
	CodexBin               string
	StartupTimeout         time.Duration
	ShutdownTimeout        time.Duration
	ProcessExecutorRate    time.Duration
	ProcessExecutorTimeout time.Duration
}

type StorageType string

const StorageLocal StorageType = "local"

var (
	errMaxOpenConns     = errors.New("FACTORY_SQLITE_MAX_OPEN_CONNS must be an integer from 1 to 32")
	errExecutorRate     = errors.New("FACTORY_PROCESS_EXECUTOR_RATE must be a positive duration")
	errExecutorTimeout  = errors.New("FACTORY_PROCESS_EXECUTOR_TIMEOUT must be a positive duration")
	errConfigRequired   = errors.New("config required")
	errAdminCredentials = errors.New("ADMIN_USERNAME and ADMIN_PASSWORD must be set together")
	errStorageMode      = errors.New("unsupported FACTORY_STORAGE_MODE")
	errLoopbackAddress  = errors.New("FACTORY_ADDRESS must bind to loopback")
	errRequiredPaths    = errors.New("data, definitions, Codex binary, and allowed roots are required")
	errEmptyAllowedRoot = errors.New("empty allowed root")
	errAllowedRoot      = errors.New("allowed root must exist")
	errTimeouts         = errors.New("startup and shutdown timeouts must be positive")
)

func FromEnv() (*Config, error) {
	_ = godotenv.Load()
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	maxOpenConns, err := strconv.Atoi(config.GetWithDefault("FACTORY_SQLITE_MAX_OPEN_CONNS", "4"))
	if err != nil || maxOpenConns < 1 || maxOpenConns > 32 {
		return nil, errMaxOpenConns
	}
	executorRate, err := time.ParseDuration(config.GetWithDefault("FACTORY_PROCESS_EXECUTOR_RATE", "1s"))
	if err != nil || executorRate <= 0 {
		return nil, errExecutorRate
	}
	executorTimeout, err := time.ParseDuration(config.GetWithDefault("FACTORY_PROCESS_EXECUTOR_TIMEOUT", "30s"))
	if err != nil || executorTimeout <= 0 {
		return nil, errExecutorTimeout
	}
	dataDir := config.GetWithDefault("FACTORY_DATA_DIR", filepath.Join(cwd, ".factory-data"))
	roots := strings.Split(config.GetWithDefault("FACTORY_ALLOWED_ROOTS", cwd), string(os.PathListSeparator))
	cfg := &Config{
		AdminUsername: os.Getenv("ADMIN_USERNAME"),
		AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		Production:    config.GetWithDefault("PRODUCTION", "false") == "true",
		Address:       config.GetWithDefault("FACTORY_ADDRESS", "127.0.0.1:8716"),
		Storage: &storage.StorageConfig{
			Mode: storage.StorageMode(config.GetWithDefault("FACTORY_STORAGE_MODE", string(storage.StorageModeSqlite))),
			SqliteConfig: &sqlite.SqliteConfig{
				Path:         config.GetWithDefault("FACTORY_SQLITE_PATH", filepath.Join(dataDir, "factory.db")),
				MaxOpenConns: maxOpenConns,
				Pragmas:      sqlite.PragmasSettings,
			},
		},
		DataDir:                config.GetWithDefault("FACTORY_DATA_DIR", filepath.Join(cwd, ".factory-data")),
		DefinitionsDir:         config.GetWithDefault("FACTORY_DEFINITIONS_DIR", filepath.Join(cwd, "processes")),
		AllowedRoots:           roots,
		CodexBin:               config.GetWithDefault("FACTORY_CODEX_BIN", "codex"),
		StartupTimeout:         10 * time.Second,
		ShutdownTimeout:        15 * time.Second,
		ProcessExecutorRate:    executorRate,
		ProcessExecutorTimeout: executorTimeout,
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if c == nil {
		return errConfigRequired
	}
	if (c.AdminUsername == "") != (c.AdminPassword == "") {
		return errAdminCredentials
	}
	if c.Storage.Mode != storage.StorageModeSqlite {
		return fmt.Errorf("%w: %q", errStorageMode, c.Storage.Mode)
	}
	host, _, err := net.SplitHostPort(c.Address)
	if err != nil {
		return fmt.Errorf("invalid FACTORY_ADDRESS: %w", err)
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errLoopbackAddress
		}
	}
	if c.DataDir == "" || c.DefinitionsDir == "" || c.CodexBin == "" || len(c.AllowedRoots) == 0 {
		return errRequiredPaths
	}
	for _, root := range c.AllowedRoots {
		if root == "" {
			return errEmptyAllowedRoot
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("%w: %s", errAllowedRoot, root)
		}
	}
	if c.StartupTimeout <= 0 || c.ShutdownTimeout <= 0 {
		return errTimeouts
	}

	return nil
}
