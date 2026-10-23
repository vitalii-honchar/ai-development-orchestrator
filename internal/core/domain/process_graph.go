package domain

import (
	"errors"
	"fmt"
	"slices"

	"go-software-factory/internal/core/validator"
)

var (
	errProcessGraphNilNode         = errors.New("process graph: nil node")
	errProcessGraphDuplicateNode   = errors.New("process graph: duplicate node name")
	errProcessGraphUnknownEndpoint = errors.New("process graph: unknown edge endpoint")
	errProcessGraphInvalidNode     = errors.New("process graph: invalid node")
	errProcessGraphUnknownNode     = errors.New("process graph: unknown node")
	errProcessGraphInvalidEdge     = errors.New("process graph: invalid edge")
)

type ProcessGraph struct {
	Edges         map[string][]string               `json:"edges"`
	Nodes         map[string]*ProcessStepDefinition `json:"nodes"`
	IncomingEdges map[string][]string               `json:"incoming_edges"`
}

// NewProcessGraph creates a new empty process graph
func NewProcessGraph() *ProcessGraph {
	return &ProcessGraph{
		Edges:         make(map[string][]string),
		Nodes:         make(map[string]*ProcessStepDefinition),
		IncomingEdges: make(map[string][]string),
	}
}

// AddNode adds a node to the graph
func (g *ProcessGraph) AddNode(node *ProcessStepDefinition) error {
	if node == nil {
		return errProcessGraphNilNode
	}
	if _, err := validator.Struct(node); err != nil {
		return err
	}
	if g.Nodes == nil {
		g.Nodes = make(map[string]*ProcessStepDefinition)
	}
	if g.Edges == nil {
		g.Edges = make(map[string][]string)
	}
	if g.IncomingEdges == nil {
		g.IncomingEdges = make(map[string][]string)
	}
	if _, exists := g.Nodes[node.Name]; exists {
		return fmt.Errorf("%w %q", errProcessGraphDuplicateNode, node.Name)
	}
	g.Nodes[node.Name] = node

	// Initialize empty adjacency lists if they don't exist
	if g.Edges[node.Name] == nil {
		g.Edges[node.Name] = []string{}
	}

	if g.IncomingEdges[node.Name] == nil {
		g.IncomingEdges[node.Name] = []string{}
	}

	return nil
}

// AddEdge adds a directed edge from source to target
func (g *ProcessGraph) AddEdge(sourceName, targetName string) error {
	if g.Nodes[sourceName] == nil || g.Nodes[targetName] == nil {
		return errProcessGraphUnknownEndpoint
	}
	if g.Edges == nil {
		g.Edges = make(map[string][]string)
	}
	if g.IncomingEdges == nil {
		g.IncomingEdges = make(map[string][]string)
	}
	if !slices.Contains(g.Edges[sourceName], targetName) {
		g.Edges[sourceName] = append(g.Edges[sourceName], targetName)
	}
	if !slices.Contains(g.IncomingEdges[targetName], sourceName) {
		g.IncomingEdges[targetName] = append(g.IncomingEdges[targetName], sourceName)
	}

	return nil
}

// RemoveEdge removes a directed edge
func (g *ProcessGraph) RemoveEdge(sourceName, targetName string) {
	if edges, exists := g.Edges[sourceName]; exists {
		g.Edges[sourceName] = slices.DeleteFunc(edges, func(name string) bool {
			return name == targetName
		})
	}
	if edges, exists := g.IncomingEdges[targetName]; exists {
		g.IncomingEdges[targetName] = slices.DeleteFunc(edges, func(name string) bool {
			return name == sourceName
		})
	}
}

// GetSuccessors returns all nodes that directly follow the given node
func (g *ProcessGraph) GetSuccessors(nodeName string) []*ProcessStepDefinition {
	return g.nodesByName(g.Edges[nodeName])
}

// GetPredecessors returns all nodes that directly precede the given node
func (g *ProcessGraph) GetPredecessors(nodeName string) []*ProcessStepDefinition {
	return g.nodesByName(g.IncomingEdges[nodeName])
}

// GetStartNodes returns all nodes with no incoming edges (entry points)
func (g *ProcessGraph) GetStartNodes() []*ProcessStepDefinition {
	var startNodes []*ProcessStepDefinition

	for nodeName, node := range g.Nodes {
		if len(g.IncomingEdges[nodeName]) == 0 {
			startNodes = append(startNodes, node)
		}
	}

	return startNodes
}

// GetEndNodes returns all nodes with no outgoing edges (exit points)
func (g *ProcessGraph) GetEndNodes() []*ProcessStepDefinition {
	var endNodes []*ProcessStepDefinition

	for nodeName, node := range g.Nodes {
		if len(g.Edges[nodeName]) == 0 {
			endNodes = append(endNodes, node)
		}
	}

	return endNodes
}

// HasCycle detects if the graph contains any cycles
func (g *ProcessGraph) HasCycle() bool {
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	for nodeName := range g.Nodes {
		if !visited[nodeName] && g.hasCycleUtil(nodeName, visited, recStack) {
			return true
		}
	}

	return false
}

func (g *ProcessGraph) nodesByName(names []string) []*ProcessStepDefinition {
	var nodes []*ProcessStepDefinition
	for _, name := range names {
		if node := g.Nodes[name]; node != nil {
			nodes = append(nodes, node)
		}
	}

	return nodes
}

// Helper function for cycle detection
func (g *ProcessGraph) hasCycleUtil(nodeName string, visited, recStack map[string]bool) bool {
	visited[nodeName] = true
	recStack[nodeName] = true

	for _, targetName := range g.Edges[nodeName] {
		if !visited[targetName] {
			if g.hasCycleUtil(targetName, visited, recStack) {
				return true
			}
		} else if recStack[targetName] {
			return true
		}
	}

	recStack[nodeName] = false

	return false
}

func (g *ProcessGraph) validate() error {
	for name, node := range g.Nodes {
		if node == nil || node.Name != name {
			return fmt.Errorf("%w %s", errProcessGraphInvalidNode, name)
		}
		if _, err := validator.Struct(node); err != nil {
			return err
		}
	}
	for _, direction := range []struct {
		edges   map[string][]string
		reverse map[string][]string
	}{{g.Edges, g.IncomingEdges}, {g.IncomingEdges, g.Edges}} {
		for source, targets := range direction.edges {
			if g.Nodes[source] == nil {
				return fmt.Errorf("%w %s", errProcessGraphUnknownNode, source)
			}
			seen := make(map[string]bool, len(targets))
			for _, target := range targets {
				if g.Nodes[target] == nil || seen[target] || !slices.Contains(direction.reverse[target], source) {
					return fmt.Errorf("%w %s -> %s", errProcessGraphInvalidEdge, source, target)
				}
				seen[target] = true
			}
		}
	}

	return nil
}
