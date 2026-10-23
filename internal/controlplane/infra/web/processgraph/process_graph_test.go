package processgraph

import (
	"strings"
	"testing"
	"time"

	"go-software-factory/internal/core/domain"
)

const (
	unrecordedState = "UNRECORDED"
	buildStep       = "build"
	endStep         = "end"
	customStepType  = "CUSTOM_STEP"
	prepareStep     = "prepare"
	inspectStep     = "inspect"
	verifyStep      = "verify"
	finishStep      = "finish"
)

func TestBuildNamesCyclesAndTypes(t *testing.T) {
	t.Parallel()
	definition := &domain.ProcessDefinition{}
	graph := &definition.Graph
	for _, node := range []domain.ProcessStepDefinition{
		{Name: buildStep, Type: domain.ProcessStepTypeAICodingAgent},
		{Name: endStep, Type: domain.ProcessStepTypeTerminal},
		{Name: "a\" ]\nclick step0 \"javascript:alert(1)\""},
		{Name: "<script> 🚀"}, {Name: "isolated", Type: customStepType},
	} {
		if err := graph.AddNode(&node); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range [][2]string{{buildStep, endStep}, {endStep, buildStep}, {endStep, endStep}} {
		if err := graph.AddEdge(edge[0], edge[1]); err != nil {
			t.Fatal(err)
		}
	}
	view := Build(definition, nil, nil)
	if !strings.HasPrefix(view.Diagram, "flowchart TB\n") || len(view.Nodes) != 5 ||
		strings.Contains(view.Diagram, "click ") || strings.Contains(view.Diagram, "<script>") ||
		strings.Count(view.Diagram, " --> ") != 3 || !strings.Contains(view.Diagram, "step3 --> step3") {
		t.Fatalf("graph lost connections or contains unsafe labels: %s", view.Diagram)
	}
	for _, style := range []string{
		"class step2 aiCodingTask;\nclass step2 step-node-2;", "class step3 endEvent;", "class step4 task;", `step3((("`,
	} {
		if !strings.Contains(view.Diagram, style) {
			t.Fatalf("missing type shape/color %q in %s", style, view.Diagram)
		}
	}
	if Build(definition, nil, nil).Diagram != view.Diagram {
		t.Fatal("graph output is not deterministic")
	}
}

func TestDiagramLabelsIncludeExecutionStatus(t *testing.T) {
	t.Parallel()
	definition := &domain.ProcessDefinition{Graph: domain.ProcessGraph{
		Nodes: map[string]*domain.ProcessStepDefinition{
			inspectStep: {Name: inspectStep, Type: domain.ProcessStepTypeAICodingAgent},
		},
	}}
	steps := []domain.ProcessStep{{ProcessStepDefinitionName: inspectStep, Type: domain.ProcessStepTypeAICodingAgent,
		State: domain.ProcessStepStateCompleted, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}
	for _, executing := range []bool{false, true} {
		var execution *domain.Process
		if executing {
			execution = &domain.Process{State: domain.ProcessStateCompleted}
		}
		view := Build(definition, execution, steps)
		want := `step0("#105;#110;#115;#112;#101;#99;#116;")`
		if executing {
			want = `step0("#105;#110;#115;#112;#101;#99;#116;#32;#183;#32;#67;#111;#109;#112;#108;#101;#116;#101;#100;")`
		}
		if !strings.Contains(view.Diagram, want) {
			t.Fatalf("graph label is missing its name or execution status: %s", view.Diagram)
		}
		if executing && (view.Nodes[0].Step != &steps[0] || view.Nodes[0].State != "COMPLETED") {
			t.Fatal("step details must remain available when graph labels are simplified")
		}
	}
}

func TestBuildExecutionIncludesUnqueuedAndTerminalNodes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 19, 8, 0, 0, 0, time.UTC)
	definition := &domain.ProcessDefinition{Graph: domain.ProcessGraph{
		Nodes: map[string]*domain.ProcessStepDefinition{
			prepareStep: {Name: prepareStep, Type: customStepType},
			inspectStep: {Name: inspectStep, Type: domain.ProcessStepTypeAICodingAgent},
			verifyStep:  {Name: verifyStep, Type: customStepType},
			finishStep:  {Name: finishStep, Type: customStepType},
			endStep:     {Name: endStep, Type: domain.ProcessStepTypeTerminal},
		},
		Edges: map[string][]string{
			prepareStep: {inspectStep, verifyStep}, inspectStep: {finishStep}, verifyStep: {finishStep}, finishStep: {endStep},
		},
	}}
	steps := []domain.ProcessStep{
		{ProcessStepDefinitionName: prepareStep, Type: customStepType, State: domain.ProcessStepStateCompleted,
			CreatedAt: now, UpdatedAt: now.Add(time.Second)},
		{ProcessStepDefinitionName: inspectStep, Type: domain.ProcessStepTypeAICodingAgent,
			State:     domain.ProcessStepStateFailed,
			CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(3 * time.Second)},
	}
	view := Build(definition, &domain.Process{State: domain.ProcessStateFailed}, steps)
	if len(view.Nodes) != 5 || strings.Count(view.Diagram, " --> ") != 5 {
		t.Fatalf("execution graph omits unqueued nodes or branch/join connections: %+v", view)
	}
	if view.Nodes[0].State != "NOT_REACHED" || view.Nodes[1].State != "NOT_STARTED" ||
		view.Nodes[2].State != "FAILED" || view.Nodes[3].State != "COMPLETED" ||
		view.Nodes[4].State != "NOT_STARTED" || view.Nodes[2].Step != &steps[1] {
		t.Fatalf("execution states do not match persisted steps: %+v", view.Nodes)
	}
	completed := Build(definition, &domain.Process{State: domain.ProcessStateCompleted}, nil)
	if completed.Nodes[0].State != unrecordedState || completed.Nodes[0].Step != nil {
		t.Fatal("end event should be shown without inventing a worker record")
	}
	definition.Graph.Nodes["other_end"] = &domain.ProcessStepDefinition{
		Name: "other_end", Type: domain.ProcessStepTypeTerminal,
	}
	ambiguous := Build(definition, &domain.Process{State: domain.ProcessStateCompleted}, nil)
	if ambiguous.Nodes[0].State != unrecordedState {
		t.Fatal("multiple end events must not all be marked as reached")
	}
}

func TestBuildWithoutDefinitionPreservesRecordedSteps(t *testing.T) {
	t.Parallel()
	steps := []domain.ProcessStep{{ProcessStepDefinitionName: "removed", Type: domain.ProcessStepTypeAICodingAgent,
		State: domain.ProcessStepStatePending}}
	view := Build(nil, &domain.Process{State: domain.ProcessStatePending}, steps)
	if view.DefinitionAvailable || len(view.Nodes) != 1 || view.Nodes[0].Name != "removed" ||
		view.Nodes[0].State != "PENDING" || strings.Contains(view.Diagram, " --> ") {
		t.Fatalf("unavailable definition should preserve recorded steps without inventing edges: %+v", view)
	}
}

func TestCompletedProcessDoesNotInferEndFromCurrentDefinition(t *testing.T) {
	t.Parallel()
	definition := &domain.ProcessDefinition{Graph: domain.ProcessGraph{
		Nodes: map[string]*domain.ProcessStepDefinition{
			"replacement_end": {Name: "replacement_end", Type: domain.ProcessStepTypeTerminal},
		},
	}}
	view := Build(definition, &domain.Process{State: domain.ProcessStateCompleted}, nil)
	if view.Nodes[0].State != unrecordedState {
		t.Fatal("a completed process must not imply it reached a terminal in an editable definition")
	}
}

func TestExecutionHighlightsCurrentStepsAndCompletedWork(t *testing.T) {
	t.Parallel()
	definition := &domain.ProcessDefinition{Graph: domain.ProcessGraph{
		Nodes: map[string]*domain.ProcessStepDefinition{
			prepareStep: {Name: prepareStep, Type: customStepType},
			buildStep:   {Name: buildStep, Type: domain.ProcessStepTypeAICodingAgent},
			verifyStep:  {Name: verifyStep, Type: customStepType},
		},
	}}
	steps := []domain.ProcessStep{
		{ProcessStepDefinitionName: prepareStep, State: domain.ProcessStepStateCompleted},
		{ProcessStepDefinitionName: buildStep, State: domain.ProcessStepStatePending},
	}
	execution := &domain.Process{State: domain.ProcessStatePending, CurrentSteps: []string{buildStep, verifyStep}}
	view := Build(definition, execution, steps)
	if view.Nodes[0].StateLabel != "Queued" || view.Nodes[1].StateLabel != "Completed" ||
		view.Nodes[2].StateLabel != "Waiting" {
		t.Fatalf("step labels do not distinguish completed and queued work: %+v", view.Nodes)
	}
	for _, class := range []string{"class step0 current;", "class step1 completed;", "class step2 current;"} {
		if !strings.Contains(view.Diagram, class) {
			t.Fatalf("graph is missing execution state %q: %s", class, view.Diagram)
		}
	}
	execution.State = domain.ProcessStateFailed
	view = Build(definition, execution, steps)
	if strings.Contains(view.Diagram, " current;") {
		t.Fatal("a final execution must not show an active step")
	}
}

func TestOrderedNodesFollowWorkflowConnections(t *testing.T) {
	t.Parallel()
	const (
		brainstorm = "brainstorm"
		implement  = "implement"
		test       = "test"
		done       = "done"
		cycle      = "cycle"
	)
	view := View{Nodes: []Node{
		{Name: brainstorm, Next: []string{implement}},
		{Name: done},
		{Name: implement, Next: []string{test}},
		{Name: test, Next: []string{done}},
		{Name: cycle, Next: []string{cycle}},
	}}
	nodes := view.OrderedNodes()
	for i, name := range []string{brainstorm, implement, test, done, cycle} {
		if nodes[i].Name != name {
			t.Fatalf("step %d = %s, want %s", i, nodes[i].Name, name)
		}
	}
	if view.Nodes[1].Name != done {
		t.Fatal("ordering changed graph node identities")
	}
}

func TestOrderedNodesKeepCycleBeforeItsSuccessor(t *testing.T) {
	t.Parallel()
	const begin, retry, done = "begin", "z_retry", "a_done"
	view := View{Nodes: []Node{
		{Name: done},
		{Name: begin, Next: []string{retry}},
		{Name: retry, Next: []string{retry, done}},
	}}
	for i, node := range view.OrderedNodes() {
		if want := []string{begin, retry, done}[i]; node.Name != want {
			t.Fatalf("node %d = %s, want %s", i, node.Name, want)
		}
	}
}

func TestBuildPreservesUnrecordedCurrentStepWithoutDefinition(t *testing.T) {
	t.Parallel()
	view := Build(nil, &domain.Process{
		State: domain.ProcessStatePending, CurrentSteps: []string{buildStep},
	}, nil)
	if len(view.Nodes) != 1 || view.Nodes[0].Name != buildStep || !view.Nodes[0].Current ||
		view.Nodes[0].StateLabel != "Waiting" {
		t.Fatalf("saved current step was lost: %+v", view.Nodes)
	}
}
