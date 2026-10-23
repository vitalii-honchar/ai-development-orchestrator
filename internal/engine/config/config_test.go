package config

import (
	"path/filepath"
	"testing"
)

func TestFromEnvConfiguresSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("FACTORY_STORAGE_MODE", "sqlite")
	t.Setenv("FACTORY_SQLITE_PATH", path)
	t.Setenv("FACTORY_SQLITE_MAX_OPEN_CONNS", "3")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage.SqliteConfig.Path != path || cfg.Storage.SqliteConfig.MaxOpenConns != 3 ||
		cfg.Storage.SqliteConfig.Pragmas["foreign_keys"] != "ON" {
		t.Fatalf("unexpected SQLite configuration: %+v", cfg.Storage.SqliteConfig)
	}
	if cfg.StartupTimeout <= 0 || cfg.ShutdownTimeout <= 0 {
		t.Fatal("service timeouts must be finite and positive")
	}
}

func TestFromEnvRejectsUnsupportedStorageAndPoolSizes(t *testing.T) {
	for _, mode := range []string{"local", "postgres", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FACTORY_STORAGE_MODE", mode)
			if _, err := FromEnv(); err == nil {
				t.Fatal("unsupported storage mode accepted")
			}
		})
	}
	for _, size := range []string{"0", "33", "invalid"} {
		t.Run(size, func(t *testing.T) {
			t.Setenv("FACTORY_STORAGE_MODE", "sqlite")
			t.Setenv("FACTORY_SQLITE_MAX_OPEN_CONNS", size)
			if _, err := FromEnv(); err == nil {
				t.Fatal("invalid pool size accepted")
			}
		})
	}
}

//nolint:paralleltest // The credential table changes process environment with t.Setenv.
func TestAdminCredentials(t *testing.T) {
	for _, tc := range []struct {
		username, password string
		valid              bool
	}{
		{"", "", true}, {"admin", "password", true}, {"admin", "", false}, {"", "password", false},
	} {
		t.Setenv("ADMIN_USERNAME", tc.username)
		t.Setenv("ADMIN_PASSWORD", tc.password)
		cfg, err := FromEnv()
		if (err == nil) != tc.valid {
			t.Fatalf("credential pair validity: %v", err)
		}
		if err == nil && (cfg.AdminUsername != tc.username || cfg.AdminPassword != tc.password) {
			t.Fatal("credentials not configured")
		}
	}
}

func TestProcessExecutorConfig(t *testing.T) {
	t.Setenv("FACTORY_PROCESS_EXECUTOR_RATE", "25ms")
	t.Setenv("FACTORY_PROCESS_EXECUTOR_TIMEOUT", "2s")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ProcessExecutorRate.String() != "25ms" || cfg.ProcessExecutorTimeout.String() != "2s" {
		t.Fatalf("executor config: %+v", cfg)
	}
	for _, key := range []string{"FACTORY_PROCESS_EXECUTOR_RATE", "FACTORY_PROCESS_EXECUTOR_TIMEOUT"} {
		for _, value := range []string{"0s", "-1s", "invalid"} {
			t.Run(key+value, func(t *testing.T) {
				t.Setenv(key, value)
				if _, err := FromEnv(); err == nil {
					t.Fatal("invalid scheduler duration accepted")
				}
			})
		}
	}
}
