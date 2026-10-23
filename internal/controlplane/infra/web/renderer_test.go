package web

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/gin-gonic/gin"
	"go-software-factory/internal/controlplane/infra/web/processgraph"
	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/core/dsl"
)

func TestFormatExecutionDuration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		duration time.Duration
		want     string
	}{
		{0, "0s"},
		{400 * time.Millisecond, "0s"},
		{600 * time.Millisecond, "1s"},
		{6543364 * time.Microsecond, "7s"},
		{59400 * time.Millisecond, "59s"},
		{59600 * time.Millisecond, "60s"},
		{time.Minute + 470363*time.Microsecond, "1m"},
		{time.Minute + 29*time.Second, "1m"},
		{time.Minute + 30*time.Second, "2m"},
		{13*time.Minute + 40*time.Second, "14m"},
		{2*time.Hour + 3*time.Minute + 21*time.Second, "123m"},
	} {
		t.Run(tc.duration.String(), func(t *testing.T) {
			t.Parallel()
			if got := formatExecutionDuration(tc.duration); got != tc.want {
				t.Fatalf("duration %s: got %q, want %q", tc.duration, got, tc.want)
			}
		})
	}
}

func TestExecutionFormPreservesTypedValues(t *testing.T) {
	t.Parallel()
	definition, err := dsl.ProcessDefinitionDslParse([]byte(`name: Build
inputs:
  description: {type: STRING, required: true}
  working_directory: {type: FILE_PATH, required: true}
steps: {done: {type: TERMINAL}}
`))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	if err := newRenderer().Instance("processes/create", gin.H{
		"Definition": definition, "CSRFToken": "test-token",
		"InputValues": map[string]string{"description": "Build <script> safely", "working_directory": "/tmp/My project"},
	}).Render(response); err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if strings.Index(body, `for="input-description"`) > strings.Index(body, `for="input-working_directory"`) {
		t.Fatal("execution inputs must keep a stable name order")
	}
	for _, value := range []string{`name="input.description"`, `data-text-editor`, `Choose folder`,
		`Working directory`, `Build &lt;script&gt; safely`, `/tmp/My project`} {
		if !strings.Contains(body, value) {
			t.Fatalf("execution form lost field or submitted value %q", value)
		}
	}
}

func TestExecutionFormRequiredInputsComeFirst(t *testing.T) {
	t.Parallel()
	definition, err := dsl.ProcessDefinitionDslParse([]byte(`name: Build
inputs:
  optional_a: {type: STRING}
  required_z: {type: STRING, required: true}
  required_b: {type: STRING, required: true}
  optional_c: {type: STRING}
steps: {done: {type: TERMINAL}}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(inputNames(definition.Inputs), ","); got != "required_b,required_z,optional_a,optional_c" {
		t.Fatalf("required fields must precede optional fields in alphabetical order: %s", got)
	}
}

func TestExecutionFormSimpleInputsStayNative(t *testing.T) {
	t.Parallel()
	definition, err := dsl.ProcessDefinitionDslParse([]byte(`name: Build
inputs:
  model: {type: STRING}
  reasoning_effort: {type: STRING}
  branch: {type: STRING, required: true}
  schema: {type: JSON_SCHEMA, required: true}
steps: {done: {type: TERMINAL}}
`))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	if err := newRenderer().Instance("processes/create", gin.H{
		"Definition": definition, "CSRFToken": "test-token",
		"InputValues": map[string]string{"model": "custom-model", "branch": "release\nnotes", "schema": `{"type":"object"}`},
	}).Render(response); err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	for _, name := range []string{"model", "reasoning_effort"} {
		pattern := `<input[^>]+name="input\.` + name + `"[^>]*>`
		if !regexp.MustCompile(pattern).MatchString(body) {
			t.Fatalf("%s needs a compact native input", name)
		}
	}
	if !regexp.MustCompile(`(?s)<textarea[^>]+name="input\.branch"[^>]*>release\nnotes</textarea>`).MatchString(body) {
		t.Fatal("generic strings must preserve multiline values")
	}
	if strings.Contains(body, `data-label="Branch"`) {
		t.Fatal("simple string fields must work without loading a code editor")
	}
	for _, name := range []string{"branch", "schema"} {
		pattern := `<textarea[^>]+name="input\.` + name + `"[^>]*>`
		control := regexp.MustCompile(pattern).FindString(body)
		if !strings.Contains(control, "required") || !strings.Contains(control, "aria-describedby=") {
			t.Fatalf("%s must retain validation and accessible guidance", name)
		}
	}
}

func TestExecutionTemplatesRoundDurations(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	executions := []domain.Process{{
		ID: uuid.New(), ProcessDefinitionID: uuid.New(), State: domain.ProcessStateCompleted,
		Version: 1, CreatedAt: now, UpdatedAt: now,
		Reliability: domain.ProcessReliability{ExecutionTime: 13*time.Minute + 40*time.Second},
	}}
	steps := []domain.ProcessStep{{
		ID: uuid.New(), ProcessID: executions[0].ID, State: domain.ProcessStepStateCompleted,
		ProcessStepDefinitionName: "inspect", Type: domain.ProcessStepTypeAICodingAgent,
		Version: 1, CreatedAt: now, UpdatedAt: now,
		Reliability: domain.ProcessReliability{ExecutionTime: 6543364 * time.Microsecond},
	}}
	graph := processgraph.Build(nil, &executions[0], steps)
	data := gin.H{
		"ProcessesPage": true, "Processes": executions, "Process": &executions[0],
		"DefinitionNames": map[uuid.UUID]string{executions[0].ProcessDefinitionID: "Duration example"},
		"Graph":           graph,
		"Steps":           []gin.H{{"Node": graph.Nodes[0], "Params": []any{}, "Result": []any{}}},
	}
	for _, page := range []string{"processes/page", "processes/detail"} {
		t.Run(page, func(t *testing.T) {
			t.Parallel()
			response := httptest.NewRecorder()
			if err := newRenderer().Instance(page, data).Render(response); err != nil {
				t.Fatal(err)
			}
			body := response.Body.String()
			if !strings.Contains(body, ">14m<") || strings.Contains(body, "13m40s") {
				t.Fatal("execution duration was not rounded to whole minutes")
			}
			if page == "processes/detail" &&
				(!strings.Contains(body, ">7s<") || strings.Contains(body, "6.543364s")) {
				t.Fatal("step duration was not rounded to whole seconds")
			}
		})
	}
}

func TestExecutionNextStepLinksWorkWithoutJavaScript(t *testing.T) {
	t.Parallel()
	definition, err := dsl.ProcessDefinitionDslParse([]byte(`name: Review
steps:
  inspect: {type: manual.review, next: [done]}
  done: {type: TERMINAL}
`))
	if err != nil {
		t.Fatal(err)
	}
	process := &domain.Process{ID: uuid.New(), State: domain.ProcessStatePending}
	graph := processgraph.Build(definition, process, nil)
	entries := make([]gin.H, 0, len(graph.Nodes))
	var destination string
	for _, node := range graph.Nodes {
		entries = append(entries, gin.H{"Node": node})
		if node.Name == "done" {
			destination = node.ID + "-detail"
		}
	}
	response := httptest.NewRecorder()
	data := gin.H{"Process": process, "Graph": graph, "Steps": entries}
	if err := newRenderer().Instance("processes/detail", data).Render(response); err != nil {
		t.Fatal(err)
	}
	if destination == "" || !strings.Contains(response.Body.String(), `href="#`+destination+`" data-inspect-next="done"`) {
		t.Fatal("next-step links must reach the named step before JavaScript enhancement")
	}
}
