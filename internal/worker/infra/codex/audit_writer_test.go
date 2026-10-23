package codex_test

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-software-factory/internal/worker/infra/codex"
)

//nolint:paralleltest // Captures the process-global slog logger.
func TestAICodingExecutorBoundsAuditChunksWithoutLosingOutput(t *testing.T) {
	logs := make(chan []byte, 128)
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(auditCapture{logs}, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	binary, recordings := fakeCodex(t, `cat "$record_dir/stdoutfixture"
printf '%s' '{"summary":"Executed the task."}' > "$output"
`)
	want := strings.Repeat("界", 70000) + "tail"
	if err := os.WriteFile(filepath.Join(recordings, "stdoutfixture"), []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := codex.NewAICodingExecutor(binary).Execute(t.Context(), codexArguments(t.TempDir())); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	var sequence float64
	for len(logs) > 0 {
		var event map[string]any
		if err := json.Unmarshal(<-logs, &event); err != nil {
			t.Fatal(err)
		}
		if event["event"] != "codex_output" || event["stream"] != "stdout" {
			continue
		}
		chunk := event["output"].(string)
		if len(chunk) > 16384 || event["sequence"].(float64) != sequence+1 {
			t.Fatalf("unbounded or unordered output: length=%d sequence=%v", len(chunk), event["sequence"])
		}
		sequence++
		output.WriteString(chunk)
	}
	if output.String() != want {
		t.Fatalf("audit lost Unicode or the final unterminated fragment: got %d bytes want %d", output.Len(), len(want))
	}
}
