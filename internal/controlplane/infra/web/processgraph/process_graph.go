package processgraph

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"go-software-factory/internal/core/domain"
)

type View struct {
	Diagram             string
	Nodes               []Node
	Execution           bool
	DefinitionAvailable bool
}

type Node struct {
	ID         string
	Name       string
	Type       domain.ProcessStepType
	State      string
	StateLabel string
	Current    bool
	Note       string
	Next       []string
	Step       *domain.ProcessStep
}

// Build uses definition connections and overlays only recorded execution data.
func Build(definition *domain.ProcessDefinition, execution *domain.Process, steps []domain.ProcessStep) View {
	view := View{Execution: execution != nil, DefinitionAvailable: definition != nil}
	names := make(map[string]bool, len(steps))
	if definition != nil {
		for name := range definition.Graph.Nodes {
			names[name] = true
		}
	}
	if execution != nil {
		for _, name := range execution.CurrentSteps {
			names[name] = true
		}
	}
	recorded := make(map[string]*domain.ProcessStep, len(steps))
	for i := range steps {
		name := steps[i].ProcessStepDefinitionName
		names[name], recorded[name] = true, &steps[i]
	}
	for i, name := range slices.Sorted(maps.Keys(names)) {
		node := Node{ID: fmt.Sprintf("step%d", i), Name: name, Step: recorded[name]}
		if definition != nil && definition.Graph.Nodes[name] != nil {
			node.Type = definition.Graph.Nodes[name].Type
			node.Next = slices.Sorted(slices.Values(definition.Graph.Edges[name]))
		}
		if node.Step != nil {
			node.Type = node.Step.Type
		}
		view.Nodes = append(view.Nodes, node)
	}
	if execution != nil {
		view.applyStates(execution)
	}
	view.Diagram = diagram(view.Nodes)

	return view
}

// OrderedNodes follows connections for the progress list, tolerating retry cycles.
func (v *View) OrderedNodes() []Node {
	nodes := make(map[string]Node, len(v.Nodes))
	for _, node := range v.Nodes {
		nodes[node.Name] = node
	}
	visited := make(map[string]bool, len(v.Nodes))
	ordered := make([]Node, 0, len(v.Nodes))
	var visit func(Node)
	visit = func(node Node) {
		if visited[node.Name] {
			return
		}
		visited[node.Name] = true
		for i := len(node.Next) - 1; i >= 0; i-- {
			if next, exists := nodes[node.Next[i]]; exists {
				visit(next)
			}
		}
		ordered = append(ordered, node)
	}
	for i := len(v.Nodes) - 1; i >= 0; i-- {
		visit(v.Nodes[i])
	}
	slices.Reverse(ordered)

	return ordered
}

func (v *View) applyStates(execution *domain.Process) {
	for i := range v.Nodes {
		node := &v.Nodes[i]
		node.State, node.StateLabel = "NOT_STARTED", "Not started"
		node.Note = "This step has not started."
		if node.Step != nil {
			node.State = string(node.Step.State)
			switch node.Step.State {
			case domain.ProcessStepStatePending:
				node.StateLabel = "Queued"
			case domain.ProcessStepStateCompleted:
				node.StateLabel = "Completed"
			case domain.ProcessStepStateFailed:
				node.StateLabel = "Failed"
			}
			node.Note = ""
		} else if node.Type == domain.ProcessStepTypeTerminal {
			node.State, node.StateLabel = "NOT_REACHED", "Not reached"
			node.Note = "The workflow ends here."
			if execution.State == domain.ProcessStateCompleted {
				node.State, node.StateLabel = "UNRECORDED", "Not recorded"
				node.Note += " The execution did not record which end event was reached."
			}
		}
		if !execution.IsFinalState() && slices.Contains(execution.CurrentSteps, node.Name) &&
			(node.Step == nil || !node.Step.IsFinalState()) {
			node.Current = true
			if node.Step == nil && node.Type != domain.ProcessStepTypeTerminal {
				node.State, node.StateLabel = "WAITING", "Waiting"
				node.Note = "Waiting to start."
			}
		}
	}
}

func diagram(nodes []Node) string {
	var result strings.Builder
	result.WriteString("flowchart TB\n")
	ids := make(map[string]string, len(nodes))
	for index, node := range nodes {
		ids[node.Name] = node.ID
		open, closeShape, class := "(\"", "\")", "task"
		switch node.Type {
		case domain.ProcessStepTypeAICodingAgent:
			class = "aiCodingTask"
		case domain.ProcessStepTypeTerminal:
			open, closeShape, class = "(((\"", "\")))", "endEvent"
		}
		text := node.Name
		if node.State != "" {
			status := node.StateLabel
			if node.Current {
				status = "Current"
			}
			text += " · " + status
		}
		fmt.Fprintf(&result, "%s%s%s%s\nclass %s %s;\nclass %s step-node-%d;\n", node.ID, open,
			label(text), closeShape, node.ID, class, node.ID, index)
		switch node.State {
		case "NOT_STARTED", "NOT_REACHED", "WAITING":
			fmt.Fprintf(&result, "class %s notStarted;\n", node.ID)
		case "PENDING":
			fmt.Fprintf(&result, "class %s pending;\n", node.ID)
		case "COMPLETED":
			fmt.Fprintf(&result, "class %s completed;\n", node.ID)
		case "FAILED":
			fmt.Fprintf(&result, "class %s failed;\n", node.ID)
		}
		if node.Current {
			fmt.Fprintf(&result, "class %s current;\n", node.ID)
		}
	}
	for _, node := range nodes {
		for _, target := range node.Next {
			fmt.Fprintf(&result, "%s --> %s\n", node.ID, ids[target])
		}
	}
	result.WriteString("classDef task fill:#242424,stroke:#a1a1a1,color:#ededed,stroke-width:2px;\n" +
		"classDef aiCodingTask fill:#102b46,stroke:#71b7ff,color:#e2f1ff,stroke-width:2px;\n" +
		"classDef endEvent fill:#381b20,stroke:#fa949e,color:#ffdfe3,stroke-width:4px;\n" +
		"classDef notStarted fill:#10272c,stroke:#527077,color:#a6b7b9,stroke-width:1px;\n" +
		"classDef pending fill:#27312d,stroke:#a28e6a,color:#eadbc1;\n" +
		"classDef completed fill:#16392e,stroke:#81c9a7,color:#c6eed8;\n" +
		"classDef failed fill:#422a27,stroke:#ed9c91,color:#ffd8d1;\n" +
		"classDef current fill:#34352d,stroke:#e4a17b,color:#fff0dd,stroke-width:3px;\n")

	return result.String()
}

func label(name string) string {
	var encoded strings.Builder
	// Numeric entities keep step names out of Mermaid syntax and HTML.
	for _, character := range name {
		fmt.Fprintf(&encoded, "#%d;", character)
	}

	return encoded.String()
}
