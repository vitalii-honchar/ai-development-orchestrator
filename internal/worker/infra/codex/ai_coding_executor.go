package codex

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go-software-factory/internal/worker/app/stepexecutor"
)

const codexSandboxMode = "danger-full-access"

type AICodingExecutor struct{ binary string }

func NewAICodingExecutor(binary string) *AICodingExecutor {
	return &AICodingExecutor{binary: binary}
}

func (e *AICodingExecutor) Execute(ctx context.Context, args stepexecutor.AICodingArguments) (string, error) {
	if err := e.prepareWorkingDirectory(ctx, args.WorkingDirectory); err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp("", "factory-codex-step-")
	if err != nil {
		return "", fmt.Errorf("create Codex execution directory: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(dir)
	}()

	schemaPath := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(args.OutputSchema), 0o600); err != nil {
		return "", fmt.Errorf("write Codex output schema: %w", err)
	}
	outputPath := filepath.Join(dir, "output.json")
	log := slog.With("execution_id", rand.Text(), "process_id", args.ProcessID,
		"step_id", args.StepID, "step_name", args.StepName, "attempt", args.Attempt,
		"codex_binary", e.binary, "sandbox", codexSandboxMode)
	stdout := newAuditWriter(ctx, log, "stdout")
	stderr := newAuditWriter(ctx, log, "stderr")
	defer stdout.Flush()
	defer stderr.Flush()
	cmd := e.createOsCommand(ctx, args, schemaPath, outputPath)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("run Codex: %w", ctx.Err())
		}

		return "", fmt.Errorf("run Codex: %w; stderr: %s", err, stderr.String())
	}

	return e.readOutput(dir)
}

func (*AICodingExecutor) prepareWorkingDirectory(ctx context.Context, workingDirectory string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("prepare working directory: %w", err)
	}
	if err := os.MkdirAll(workingDirectory, 0o750); err != nil {
		return fmt.Errorf("prepare working directory: %w", err)
	}
	check := exec.CommandContext(ctx, "git", "rev-parse", "--git-dir")
	check.Dir = workingDirectory
	if err := check.Run(); err == nil {
		return nil
	} else {
		if ctx.Err() != nil {
			return fmt.Errorf("inspect Git repository: %w", ctx.Err())
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return fmt.Errorf("inspect Git repository: %w", err)
		}
	}
	initialize := exec.CommandContext(ctx, "git", "init", "--quiet")
	initialize.Dir = workingDirectory
	if output, err := initialize.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("initialize Git repository: %w", ctx.Err())
		}

		return fmt.Errorf("initialize Git repository: %w; stderr: %s", err, strings.TrimSpace(string(output)))
	}

	return nil
}

func (e *AICodingExecutor) createOsCommand(
	ctx context.Context, args stepexecutor.AICodingArguments, schemaPath, outputPath string,
) *exec.Cmd {
	// #nosec G204 -- The deployment config selects the executable; argv and stdin never pass through a shell.
	cmd := exec.CommandContext(ctx, e.binary,
		"--ask-for-approval", "never", "exec", "--sandbox", codexSandboxMode,
		"--model", args.Model, "--config", "model_reasoning_effort="+strconv.Quote(args.ReasoningEffort),
		"--output-schema", schemaPath, "--output-last-message", outputPath, "--color", "never", "--json", "-",
	)
	cmd.Dir = args.WorkingDirectory
	cmd.Stdin = strings.NewReader(args.Prompt)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}

			return fmt.Errorf("stop Codex process group: %w", err)
		}

		return nil
	}
	cmd.WaitDelay = time.Second

	return cmd
}

func (*AICodingExecutor) readOutput(dir string) (string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", fmt.Errorf("open Codex output directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile("output.json")
	if err != nil {
		return "", fmt.Errorf("read Codex output: %w", err)
	}

	return string(data), nil
}
