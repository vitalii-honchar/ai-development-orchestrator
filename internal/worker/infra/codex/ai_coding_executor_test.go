package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"go-software-factory/internal/worker/app/stepexecutor"
	"go-software-factory/internal/worker/infra/codex"
)

const testPrompt = "prompt"

//nolint:paralleltest // Captures the process-global logger while a real child process streams output.
func TestAICodingExecutorLogsOutputBeforeChildExits(t *testing.T) {
	logs := make(chan []byte, 128)
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(auditCapture{logs}, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	binary, recordings := fakeCodex(t, `printf '%s\n' '{"type":"thread.started","thread_id":"native-thread"}'
printf 'checking build dependencies\n' >&2
while [ ! -f "$record_dir/release" ]; do sleep 0.02; done
printf '%s' '{"summary":"Executed the task."}' > "$output"
`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	args := codexArguments(t.TempDir())
	args.ProcessID, args.StepID, args.StepName, args.Attempt = "process-123", "step-123", "brainstorm", 7
	done := make(chan error, 1)
	go func() {
		_, err := codex.NewAICodingExecutor(binary).Execute(ctx, args)
		done <- err
	}()
	streams := make(map[string]bool)
	deadline := time.After(3 * time.Second)
	for len(streams) < 2 {
		select {
		case line := <-logs:
			var event map[string]any
			if err := json.Unmarshal(line, &event); err != nil {
				t.Fatal(err)
			}
			if event["event"] != "codex_output" {
				continue
			}
			if event["execution_id"] == nil || event["execution_id"] == "" || event["sequence"] == nil {
				t.Fatalf("output cannot be correlated: %s", line)
			}
			if event["process_id"] != "process-123" || event["step_id"] != "step-123" ||
				event["step_name"] != "brainstorm" || event["attempt"] != float64(7) {
				t.Fatalf("step correlation missing: %s", line)
			}
			switch event["stream"] {
			case "stdout":
				if event["codex_event_type"] != "thread.started" || event["codex_thread_id"] != "native-thread" {
					t.Fatalf("native thread correlation missing: %s", line)
				}
			case "stderr":
				if event["output"] != "checking build dependencies" {
					t.Fatalf("stderr was not streamed: %s", line)
				}
			default:
				t.Fatalf("unexpected stream: %s", line)
			}
			streams[event["stream"].(string)] = true
		case err := <-done:
			t.Fatalf("child exited before releasing it: %v", err)
		case <-deadline:
			t.Fatal("Codex stdout and stderr were not logged while the child was running")
		}
	}
	if err := os.WriteFile(filepath.Join(recordings, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("logging blocked child completion")
	}
}

type auditCapture struct{ lines chan []byte }

func (w auditCapture) Write(data []byte) (int, error) {
	w.lines <- append([]byte(nil), data...)

	return len(data), nil
}

func TestAICodingExecutorExecutesWithGenericArguments(t *testing.T) {
	t.Parallel()
	binary, recordings := fakeCodex(t, `printf 'progress on stdout\n'
printf 'progress on stderr\n' >&2
printf '%s' '{"summary":"Executed the task."}' > "$output"
`)
	workdir := t.TempDir()
	input := codexArguments(workdir)
	input.Prompt = "Inspect this directory.\nKeep $(touch injected) literal."
	output, err := codex.NewAICodingExecutor(binary).Execute(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if output != `{"summary":"Executed the task."}` {
		t.Fatalf("output=%q", output)
	}
	args := strings.Split(strings.TrimSuffix(readRecording(t, recordings, "args"), "\n"), "\n")
	if len(args) != 17 {
		t.Fatalf("unexpected arguments: %q", args)
	}
	want := []string{"--ask-for-approval", "never", "exec", "--sandbox", "danger-full-access",
		"--model", "configured-model", "--config", `model_reasoning_effort="medium"`,
		"--output-schema", args[10], "--output-last-message", args[12], "--color", "never", "--json", "-"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args=%q want %q", args, want)
	}
	if readRecording(t, recordings, testPrompt) != input.Prompt {
		t.Fatal("execution changed the supplied prompt")
	}
	if readRecording(t, recordings, "schema.json") != input.OutputSchema {
		t.Fatal("execution changed the supplied schema")
	}
	gotDir, err := filepath.EvalSymlinks(strings.TrimSpace(readRecording(t, recordings, "cwd")))
	if err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.EvalSymlinks(workdir)
	if err != nil || gotDir != wantDir {
		t.Fatalf("working directory=%s want %s: %v", gotDir, wantDir, err)
	}
	if _, err := os.Stat(filepath.Dir(args[10])); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary execution files remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "injected")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prompt executed as shell code: %v", err)
	}
}

func TestAICodingExecutorPreparesWorkingDirectory(t *testing.T) {
	t.Parallel()
	for _, missing := range []bool{false, true} {
		t.Run(strconv.FormatBool(missing), func(t *testing.T) {
			t.Parallel()
			workdir := t.TempDir()
			if missing {
				workdir = filepath.Join(workdir, "missing", "project")
			}
			binary, _ := fakeCodex(t, `git rev-parse --is-inside-work-tree > "$record_dir/repository"
printf '%s' '{"summary":"Executed in a Git repository."}' > "$output"
`)
			output, err := codex.NewAICodingExecutor(binary).Execute(t.Context(), codexArguments(workdir))
			if err != nil {
				t.Fatal(err)
			}
			if output != `{"summary":"Executed in a Git repository."}` {
				t.Fatalf("output=%q", output)
			}
			if info, err := os.Stat(filepath.Join(workdir, ".git")); err != nil || !info.IsDir() {
				t.Fatalf("working directory was not initialized as a Git repository: %v", err)
			}
		})
	}
}

func TestAICodingExecutorPreservesExistingRepository(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	initialize := exec.CommandContext(t.Context(), "git", "init", "--quiet")
	initialize.Dir = repo
	if output, err := initialize.CombinedOutput(); err != nil {
		t.Fatalf("initialize fixture repository: %v: %s", err, output)
	}
	config := readRecording(t, filepath.Join(repo, ".git"), "config")
	workdir := filepath.Join(repo, "new", "subdirectory")
	binary, _ := fakeCodex(t, `git rev-parse --show-toplevel > "$record_dir/repository"
printf '%s' '{"summary":"Reused the existing repository."}' > "$output"
`)
	executor := codex.NewAICodingExecutor(binary)
	for range 2 {
		if _, err := executor.Execute(t.Context(), codexArguments(workdir)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(workdir, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created a nested Git repository: %v", err)
	}
	if got := readRecording(t, filepath.Join(repo, ".git"), "config"); got != config {
		t.Fatal("changed the existing repository configuration")
	}
}

func TestAICodingExecutorRejectsFileAsWorkingDirectory(t *testing.T) {
	t.Parallel()
	binary, recordings := fakeCodex(t, "exit 0\n")
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := codex.NewAICodingExecutor(binary).Execute(t.Context(), codexArguments(file))
	if err == nil || !strings.Contains(err.Error(), "prepare working directory") {
		t.Fatalf("file accepted as a working directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(recordings, "args")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Codex launched before directory preparation succeeded: %v", err)
	}
}

func TestAICodingExecutorReportsGitInitializationFailure(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, ".git"), []byte("invalid git file"), 0o600); err != nil {
		t.Fatal(err)
	}
	binary, recordings := fakeCodex(t, "exit 0\n")
	_, err := codex.NewAICodingExecutor(binary).Execute(t.Context(), codexArguments(workdir))
	if err == nil || !strings.Contains(err.Error(), "initialize Git repository") {
		t.Fatalf("Git initialization failure was not reported: %v", err)
	}
	if _, err := os.Stat(filepath.Join(recordings, "args")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Codex launched before Git initialization succeeded: %v", err)
	}
}

func TestCodexExecutionErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "exit failure", body: "printf 'authentication failed' >&2\nexit 7\n", want: "authentication failed"},
		{name: "missing output", body: "exit 0\n", want: "read Codex output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			binary, _ := fakeCodex(t, tc.body)
			_, err := codex.NewAICodingExecutor(binary).Execute(t.Context(), codexArguments(t.TempDir()))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v want %q", err, tc.want)
			}
			if tc.name == "exit failure" && !strings.Contains(err.Error(), "exit status 7") {
				t.Fatalf("exit status omitted: %v", err)
			}
		})
	}
	t.Run("missing binary", func(t *testing.T) {
		t.Parallel()
		_, err := codex.NewAICodingExecutor(filepath.Join(t.TempDir(), "missing-codex")).Execute(t.Context(),
			codexArguments(t.TempDir()))
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing executable error=%v", err)
		}
	})
}

func TestCodexCancellationStopsChildCommands(t *testing.T) {
	t.Parallel()
	binary, recordings := fakeCodex(t, `(
  sleep 1
  printf 'child survived' > "$record_dir/escaped"
) &
printf 'started' > "$record_dir/started"
wait
`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := codex.NewAICodingExecutor(binary).Execute(ctx, codexArguments(recordings))
		done <- err
	}()
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(filepath.Join(recordings, "started")); err == nil {
			break
		}
		select {
		case <-tick.C:
		case err := <-done:
			t.Fatalf("CLI exited before starting: %v", err)
		case <-deadline:
			t.Fatal("CLI did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CLI did not stop on cancellation")
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(recordings, "escaped")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child command survived cancellation: %v", err)
	}
}

func TestCodexDeadline(t *testing.T) {
	t.Parallel()
	binary, _ := fakeCodex(t, "sleep 30\n")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := codex.NewAICodingExecutor(binary).Execute(ctx, codexArguments(t.TempDir()))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error=%v", err)
	}
}

func codexArguments(workdir string) stepexecutor.AICodingArguments {
	return stepexecutor.AICodingArguments{
		WorkingDirectory: workdir,
		Prompt:           "Run the task.",
		Model:            "configured-model",
		ReasoningEffort:  "medium",
		OutputSchema: `{"type":"object","properties":{"summary":{"type":"string"}},` +
			`"required":["summary"],"additionalProperties":false}`,
	}
}

func fakeCodex(t *testing.T, body string) (binary, recordings string) {
	t.Helper()
	recordings = t.TempDir()
	binary = filepath.Join(recordings, "fake codex")
	script := `#!/bin/sh
set -eu
record_dir=$(dirname "$0")
printf '%s\n' "$@" > "$record_dir/args"
pwd -P > "$record_dir/cwd"
cat > "$record_dir/prompt"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-last-message) output="$2"; shift ;;
    --output-schema) cp "$2" "$record_dir/schema.json"; shift ;;
  esac
  shift
done
` + body
	// #nosec G306 -- This private test fixture must be executable by its owner.
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	return binary, recordings
}

func readRecording(t *testing.T, dir, name string) string {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}
