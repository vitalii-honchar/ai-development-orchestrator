package logger_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-software-factory/internal/core/logger"
)

//nolint:paralleltest // Configures process-global stdout and slog defaults.
func TestConfiguredLoggerRetainsEventTimestamps(t *testing.T) {
	output, err := os.Create(filepath.Join(t.TempDir(), "events.log"))
	if err != nil {
		t.Fatal(err)
	}
	previousOutput, previousLogger := os.Stdout, slog.Default()
	t.Cleanup(func() {
		os.Stdout = previousOutput
		slog.SetDefault(previousLogger)
		_ = output.Close()
	})
	os.Stdout = output
	logger.ConfigureLogger(true)
	slog.Info("Step completed", "event", "step_completed")
	data, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "time=") || !strings.Contains(string(data), "event=step_completed") {
		t.Fatalf("configured logger dropped the event timestamp: %s", data)
	}
}
