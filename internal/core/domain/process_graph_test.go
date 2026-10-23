package domain

import (
	"reflect"
	"testing"
)

func TestProcessGraph(t *testing.T) {
	t.Parallel()
	var graph ProcessGraph
	graph.RemoveEdge(testUnknown, testUnknown)
	first := &ProcessStepDefinition{Name: testBuild}
	last := &ProcessStepDefinition{Name: "test"}
	for _, node := range []*ProcessStepDefinition{first, last} {
		if err := graph.AddNode(node); err != nil {
			t.Fatal(err)
		}
	}
	if graph.AddNode(first) == nil ||
		graph.AddNode(nil) == nil ||
		graph.AddNode(&ProcessStepDefinition{}) == nil ||
		graph.AddEdge(first.Name, testUnknown) == nil {
		t.Fatal("accepted invalid graph mutation")
	}
	for range 2 {
		if err := graph.AddEdge(first.Name, last.Name); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(graph.GetSuccessors(first.Name), []*ProcessStepDefinition{last}) ||
		!reflect.DeepEqual(graph.GetPredecessors(last.Name), []*ProcessStepDefinition{first}) {
		t.Fatal("incorrect neighbors or duplicate edge")
	}
	if !reflect.DeepEqual(graph.GetStartNodes(), []*ProcessStepDefinition{first}) ||
		!reflect.DeepEqual(graph.GetEndNodes(), []*ProcessStepDefinition{last}) ||
		graph.HasCycle() ||
		graph.validate() != nil {
		t.Fatal("incorrect entry, exit, or cycle detection")
	}
	if err := graph.AddEdge(last.Name, first.Name); err != nil {
		t.Fatal(err)
	}
	if !graph.HasCycle() || graph.validate() != nil {
		t.Fatal("cycle not detected or rejected")
	}
	graph.RemoveEdge(last.Name, first.Name)
	if graph.HasCycle() || graph.validate() != nil {
		t.Fatal("edge removal left inconsistent graph")
	}
}

func TestProcessGraphValidation(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*ProcessGraph, string, string){
		"nil node": func(g *ProcessGraph, first, last string) {
			g.Nodes[first] = nil
		},
		"mismatched name": func(g *ProcessGraph, first, last string) {
			g.Nodes[first].Name = testUnknown
		},
		"unknown source": func(g *ProcessGraph, first, last string) {
			g.Edges[testUnknown] = []string{last}
		},
		"unknown target": func(g *ProcessGraph, first, last string) {
			g.Edges[first] = []string{testUnknown}
		},
		"missing reverse": func(g *ProcessGraph, first, last string) {
			g.IncomingEdges[last] = nil
		},
		"missing forward": func(g *ProcessGraph, first, last string) {
			g.Edges[first] = nil
		},
		"duplicate edge": func(g *ProcessGraph, first, last string) {
			g.Edges[first] = append(g.Edges[first], last)
		},
		"duplicate reverse": func(g *ProcessGraph, first, last string) {
			g.IncomingEdges[last] = append(g.IncomingEdges[last], first)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			graph := NewProcessGraph()
			first, last := testBuild, "test"
			if err := graph.AddNode(&ProcessStepDefinition{Name: first}); err != nil {
				t.Fatal(err)
			}
			if err := graph.AddNode(&ProcessStepDefinition{Name: last}); err != nil {
				t.Fatal(err)
			}
			if err := graph.AddEdge(first, last); err != nil {
				t.Fatal(err)
			}
			mutate(graph, first, last)
			if err := graph.validate(); err == nil {
				t.Fatal("accepted invalid graph")
			}
		})
	}
}
