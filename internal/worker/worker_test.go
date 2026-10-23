package worker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/core/app/processstep"
	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/worker"
)

type codexRepository struct {
	processstep.ProcessStepRepository
	records []domain.ProcessStep
}

func (r *codexRepository) GetPending(context.Context) ([]domain.ProcessStep, error) {
	return r.records, nil
}
func (*codexRepository) Update(_ context.Context, step *domain.ProcessStep) error {
	if err := step.Validate(); err != nil {
		return err
	}
	step.Version++

	return nil
}

func TestWorkerExecutesCodexStep(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fake codex")
	script := `#!/bin/sh
set -eu
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-last-message) output="$2"; shift ;;
    --sandbox) sandbox="$2"; shift ;;
  esac
  shift
done
[ "$sandbox" = 'danger-full-access' ] || { printf 'requested full access was not configured' >&2; exit 8; }
printf '%s' '{"summary":"Inspected the repository."}' > "$output"
`
	// #nosec G306 -- This private test fixture must be executable by its owner.
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FACTORY_CODEX_BIN", binary)
	now := time.Now().UTC()
	repo := &codexRepository{records: []domain.ProcessStep{{
		ID: uuid.New(), ProcessID: uuid.New(), ProcessStepDefinitionName: "inspect",
		State: domain.ProcessStepStatePending, Type: "AI_CODING_AGENT", Version: 1, CreatedAt: now, UpdatedAt: now,
		Params: testParameters(map[string]domain.ParameterValue{
			"output_json": {Type: domain.ParameterTypeJSONSchema, Value: `{
  "type": "object",
  "properties": {
    "summary": {
      "type": "string"
    }
  },
  "required": [
    "summary"
  ],
  "additionalProperties": false
}`},
			"prompt":            {Type: domain.ParameterTypeString, Value: "Inspect the repository."},
			"working_directory": {Type: domain.ParameterTypeFilePath, Value: t.TempDir()},
		}),
	}}}
	w, err := worker.NewWorker(&worker.WorkerDependencies{ProcessStepRepository: repo})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Stop(); err != nil {
			t.Error(err)
		}
	})
	if err := w.ExecuteProcessStepsUseCase.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	result := repo.records[0].Result.Get("output")
	var output struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(result.String()), &output); err != nil {
		t.Fatal(err)
	}
	if repo.records[0].State != domain.ProcessStepStateCompleted ||
		result.Type != domain.ParameterTypeJSON || output.Summary != "Inspected the repository." {
		t.Fatalf("Codex step result = %+v", repo.records[0])
	}
}

type observingRepository struct {
	processstep.ProcessStepRepository
	reads atomic.Int32
}

func (r *observingRepository) GetPending(context.Context) ([]domain.ProcessStep, error) {
	r.reads.Add(1)

	return nil, nil
}
func (*observingRepository) Update(context.Context, *domain.ProcessStep) error { return nil }
func TestWorkerPollsAndStops(t *testing.T) {
	t.Parallel()
	repo := &observingRepository{}
	w, err := worker.NewWorker(&worker.WorkerDependencies{ProcessStepRepository: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for repo.reads.Load() < 2 {
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatal("worker did not poll every second")
		}
	}
	if err := w.Stop(); err != nil {
		t.Fatal(err)
	}
	reads := repo.reads.Load()
	time.Sleep(30 * time.Millisecond)
	if repo.reads.Load() != reads {
		t.Fatal("worker polled after shutdown")
	}
}
func TestWorkerRequiresRepository(t *testing.T) {
	t.Parallel()
	for _, deps := range []worker.WorkerDependencies{{}} {
		if _, err := worker.NewWorker(&deps); err == nil {
			t.Fatal("accepted missing repository")
		}
	}
	if _, err := worker.NewWorker(nil); err == nil {
		t.Fatal("accepted missing dependencies")
	}
}

type blockedRepository struct {
	processstep.ProcessStepRepository
	started chan struct{}
	exited  chan struct{}
	release chan struct{}
}

func (r blockedRepository) GetPending(ctx context.Context) ([]domain.ProcessStep, error) {
	close(r.started)
	select {
	case <-ctx.Done():
	case <-r.release:
	}
	close(r.exited)

	return nil, ctx.Err()
}
func (blockedRepository) Update(context.Context, *domain.ProcessStep) error { return nil }
func TestWorkerShutdownCancelsAndDrainsActiveJob(t *testing.T) {
	t.Parallel()
	repo := blockedRepository{
		started: make(chan struct{}), exited: make(chan struct{}), release: make(chan struct{}),
	}
	w, err := worker.NewWorker(&worker.WorkerDependencies{ProcessStepRepository: repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-repo.started:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	done := make(chan error, 1)
	go func() { done <- w.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-repo.exited:
		default:
			t.Fatal("shutdown returned before active job exited")
		}
	case <-time.After(200 * time.Millisecond):
		close(repo.release)
		<-done
		t.Fatal("shutdown did not cancel the active job")
	}
}

func testParameters(values map[string]domain.ParameterValue) *domain.Parameters {
	params, err := domain.NewParameters(values)
	if err != nil {
		panic(err)
	}

	return params
}
