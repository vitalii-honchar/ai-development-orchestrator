package dsl

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
	"uuid"

	"go-software-factory/internal/core/domain"
)

func TestProcessDefinitionDslPlainAgentContext(t *testing.T) {
	t.Parallel()
	definition, err := ProcessDefinitionDslParse([]byte(`steps:
  implement:
    type: AI_CODING_AGENT
    context:
      description:
        type: STRING
        from: inputs.description
      path_to_plan:
        type: FILE_PATH
        from: steps.brainstorm.output.path_to_plan
    inputs:
      output_json:
        type: JSON_SCHEMA
        value:
          type: object
          properties:
            summary: {type: string}
          required: [summary]
          additionalProperties: false
`))
	if err != nil {
		t.Fatal(err)
	}
	step := definition.Graph.Nodes["implement"]
	if step.Context.Get("description").From != "inputs.description" ||
		step.Context.Get("path_to_plan").Type != domain.ParameterTypeFilePath ||
		step.Params.Get("output_json").Type != domain.ParameterTypeJSONSchema {
		t.Fatalf("incorrect agent context or schema: %+v", step)
	}
	encoded, err := ProcessDefinitionDslMarshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ProcessDefinitionDslParse(encoded)
	if err != nil || !reflect.DeepEqual(restored.Graph, definition.Graph) {
		t.Fatalf("context/schema round trip failed: %v\n%s", err, encoded)
	}
}

func TestProcessDefinitionDslRejectsInvalidContextExpressions(t *testing.T) {
	t.Parallel()
	for _, expression := range []string{
		"records.current.name", "inputs.", "steps.brainstorm", "steps..output.path_to_plan",
		"steps.brainstorm.output.", "brainstorm.output.path_to_plan", " ",
	} {
		t.Run(expression, func(t *testing.T) {
			t.Parallel()
			source := "steps:\n  build:\n    type: AI_CODING_AGENT\n    context:\n      source:\n" +
				"        type: STRING\n        from: '" + expression + "'\n"
			_, err := ProcessDefinitionDslParse([]byte(source))
			if err == nil || !strings.Contains(err.Error(), `steps["build"].context: parameter "source"`) {
				t.Fatalf("invalid context expression %q: %v", expression, err)
			}
		})
	}
}

func TestProcessDefinitionDslJSONValueRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		typeName domain.ParameterType
		value    string
	}{
		{"string", domain.ParameterTypeJSON, `"hello"`},
		{"numeric string", domain.ParameterTypeJSON, `"123"`},
		{testJSONNull, domain.ParameterTypeJSON, testJSONNull},
		{"large integer", domain.ParameterTypeJSON, `9007199254740993`},
		{"huge integer", domain.ParameterTypeJSON, `123456789012345678901234567890`},
		{"nested integer", domain.ParameterTypeJSON, `{"number":9007199254740993}`},
		{"schema", domain.ParameterTypeJSONSchema, `{"type":"integer","minimum":9007199254740993}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			definition, err := ProcessDefinitionDslParse([]byte("steps:\n  build:\n    type: custom.build\n"))
			if err != nil {
				t.Fatal(err)
			}
			definition.Inputs, err = domain.NewParameters(map[string]domain.ParameterValue{
				"payload": {Type: tc.typeName, Value: tc.value},
			})
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := ProcessDefinitionDslMarshal(definition)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := ProcessDefinitionDslParse(encoded)
			if err != nil {
				t.Fatalf("round trip failed: %v\n%s", err, encoded)
			}
			if got := restored.Inputs.String("payload"); got != tc.value {
				t.Fatalf("JSON changed from %s to %s\n%s", tc.value, got, encoded)
			}
		})
	}
}

func TestProcessDefinitionDslConfiguredStepInputRoundTrip(t *testing.T) {
	t.Parallel()
	definition, err := ProcessDefinitionDslParse([]byte(`name: Configured prompt
steps:
  build:
    type: CODEX
    inputs:
      prompt:
        type: STRING
        required: true
        value: "Implement {{ .description }}"
      empty:
        type: STRING
        value: ""
`))
	if err != nil {
		t.Fatal(err)
	}
	// Persistence serializes the domain graph as JSON before reconstructing it.
	stored, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	var restored domain.ProcessDefinition
	if err := json.Unmarshal(stored, &restored); err != nil {
		t.Fatal(err)
	}
	encoded, err := ProcessDefinitionDslMarshal(&restored)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := ProcessDefinitionDslParse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"prompt": "Implement {{ .description }}", "empty": ""} {
		value := reparsed.Graph.Nodes["build"].Params.Get(name).Value
		if value == nil || value != want {
			t.Fatalf("configured input %q lost during round trip: %s", name, encoded)
		}
	}
	if !reflect.DeepEqual(definition.Graph, reparsed.Graph) {
		t.Fatal("configured step inputs changed during round trip")
	}
}

func TestProcessDefinitionDslParse(t *testing.T) {
	t.Parallel()
	before := time.Now().UTC()
	definition, err := ProcessDefinitionDslParse([]byte(`name: Release
description: Build and publish
inputs:
  source: {type: REFERENCE, required: true}
reliability: {timeout: 30m, max_retries: 2, retry_delay: 5s}
steps:
  publish: {type: custom.publish}
  build:
    type: custom.build
    inputs:
      source: {type: REFERENCE, required: true}
      files: {type: REFERENCES_ARRAY}
      command: {type: STRING}
    reliability: {timeout: 1m}
    next: [test, package, test]
  test: {type: custom.test, next: [publish]}
  package: {type: custom.package, next: [publish]}
  isolated: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	if definition.Name != "Release" ||
		definition.Description != "Build and publish" ||
		definition.ID == uuid.Nil() ||
		definition.State != domain.ProcessDefinitionStateActive ||
		definition.Version != 1 ||
		definition.CreatedAt.Before(before) ||
		definition.CreatedAt.After(time.Now()) ||
		!definition.CreatedAt.Equal(definition.UpdatedAt) ||
		definition.CreatedAt.Location() != time.UTC {
		t.Fatalf("incorrect definition metadata: %+v", definition)
	}
	if !reflect.DeepEqual(definition.Inputs.Get("source"), domain.ParameterValue{
		Type:     domain.ParameterTypeReference,
		Required: true,
	}) {
		t.Fatal("incorrect process inputs")
	}
	if definition.Reliability.Timeout != 30*time.Minute ||
		definition.Reliability.MaxRetries != 2 ||
		definition.Reliability.RetryDelay != 5*time.Second {
		t.Fatal("incorrect reliability")
	}
	graph := &definition.Graph
	if len(graph.Nodes) != 5 ||
		!reflect.DeepEqual(graph.Edges["build"], []string{"test", "package"}) ||
		!reflect.DeepEqual(graph.IncomingEdges["publish"], []string{"package", "test"}) ||
		graph.HasCycle() {
		t.Fatalf("incorrect graph: %+v", graph)
	}
	build := graph.Nodes["build"]
	if build.Name != "build" ||
		build.Type != "custom.build" ||
		build.Params.Get("source") != definition.Inputs.Get("source") ||
		build.Params.Len() != 3 ||
		build.Reliability.Timeout != time.Minute ||
		build.Reliability.MaxRetries != 0 ||
		graph.Nodes["publish"].Reliability.Timeout != 0 {
		t.Fatalf("incorrect step: %+v", build)
	}
}

func TestProcessDefinitionDslRetryTimeoutRoundTrip(t *testing.T) {
	t.Parallel()
	definition, err := ProcessDefinitionDslParse([]byte(`steps:
  build:
    type: custom.build
    reliability: {timeout: 10m, retry_timeout: 90s}
`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := ProcessDefinitionDslMarshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ProcessDefinitionDslParse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range []domain.ProcessDefinitionReliability{
		definition.Graph.Nodes["build"].Reliability, restored.Graph.Nodes["build"].Reliability,
	} {
		if policy.Timeout != 10*time.Minute || policy.RetryTimeout != 90*time.Second {
			t.Fatalf("execution timeout and retry timeout were not preserved independently: %+v", policy)
		}
	}
}

func TestProcessDefinitionDslParseEmptyDefinitionsAndCycles(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"{}",
		"name: Draft",
		"steps: {}",
		"steps: {a: {next: [b]}, b: {next: [a]}}",
		"steps: {a: {next: [a]}}",
		"steps: {build: {}, Build: {}, 'Build release': {}}",
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			first, err := ProcessDefinitionDslParse([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			second, err := ProcessDefinitionDslParse([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			if first.ID == second.ID {
				t.Fatal("definition ID reused")
			}
			if strings.Contains(source, "next:") && !first.Graph.HasCycle() {
				t.Fatal("cycle lost")
			}
		})
	}
}

func TestProcessDefinitionDslParseRejectsInvalid(t *testing.T) {
	t.Parallel()
	for name, source := range map[string]string{
		"empty":                 "",
		"comments":              "# hi",
		testJSONNull:            testJSONNull,
		"sequence":              "[]",
		"scalar":                "hello",
		"documents":             "{}\n---\n{}",
		"empty second document": "{}\n---\n",
		"syntax":                "steps: [",
		"unknown":               "unexpected: true", "state": "state: ACTIVE", "version": "version: 2", "id": "id: example",
		"duplicate step": "steps:\n  build: {}\n  build: {}", "duplicate field": "name: One\nname: Two",
		"unknown step field":     "steps: {build: {input: {}}}",
		"blank name":             "steps: {' ': {}}",
		"unknown target":         "steps: {build: {next: [missing]}}",
		"case sensitive target":  "steps: {build: {next: [Build]}}",
		"invalid next shape":     "steps: {build: {next: test}}",
		"numeric next":           "steps: {build: {next: [123]}}",
		"numeric name":           "name: 12",
		"boolean type":           "steps: {build: {type: true}}",
		"input type":             "inputs: {source: {type: unknown}}",
		"input required":         "inputs: {source: {type: STRING, required: yes}}",
		"input name":             "inputs: {' ': {type: STRING}}",
		"step input":             "steps: {build: {inputs: {source: {type: unknown}}}}",
		"timeout":                "reliability: {timeout: tomorrow}",
		"negative timeout":       "reliability: {timeout: -1s}",
		"long timeout":           "reliability: {timeout: 721h}",
		"retry delay":            "reliability: {retry_delay: 25h}",
		"invalid retry timeout":  "reliability: {retry_timeout: tomorrow}",
		"negative retry timeout": "reliability: {retry_timeout: -1s}",
		"long retry timeout":     "reliability: {retry_timeout: 25h}",
		"negative retries":       "reliability: {max_retries: -1}",
		"fractional retries":     "reliability: {max_retries: 1.5}",
		"step reliability":       "steps: {build: {reliability: {timeout: wrong}}}",
		"UTF-8":                  string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if definition, err := ProcessDefinitionDslParse([]byte(source)); err == nil || definition != nil {
				t.Fatalf("accepted invalid YAML: %q => %+v, %v", source, definition, err)
			}
		})
	}
}

func TestProcessDefinitionDslParseErrorContext(t *testing.T) {
	t.Parallel()
	for source, want := range map[string]string{
		"steps: {build: {next: [missing]}}":                 `steps["build"].next`,
		"steps: {build: {inputs: {source: {type: wrong}}}}": `steps["build"].inputs: parameter "source"`,
		"steps: {build: {reliability: {timeout: wrong}}}":   `steps["build"].reliability: timeout`,
		"name: Release\nunknown: true":                      "[2:1]",
	} {
		_, err := ProcessDefinitionDslParse([]byte(source))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v does not contain %q", err, want)
		}
	}
}

func TestProcessDefinitionDslMarshalRoundTrip(t *testing.T) {
	t.Parallel()
	original, err := ProcessDefinitionDslParse([]byte(`name: 'Release <script>'
description: |
  Build and review.
  Preserve multiple lines.
inputs:
  source: {type: REFERENCE, required: true}
reliability: {timeout: 30m, max_retries: 2, retry_delay: 5s}
steps:
  'Build "release" 🚀':
    type: custom.build
    inputs:
      files: {type: REFERENCES_ARRAY, required: false}
    reliability: {timeout: 1m, max_retries: 1, retry_delay: 2s}
    next: [review]
  review:
    next: ['Build "release" 🚀']
  isolated: {}
`))
	if err != nil {
		t.Fatal(err)
	}

	encoded, err := ProcessDefinitionDslMarshal(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"id:",
		"state:",
		"version:",
		"created_at:",
		"updated_at:",
		"incoming_edges:",
		"params:",
	} {
		if strings.Contains(string(encoded), field) {
			t.Fatalf("domain field %q leaked into YAML: %s", field, encoded)
		}
	}

	restored, err := ProcessDefinitionDslParse(encoded)
	if err != nil {
		t.Fatalf("cannot parse generated YAML: %v\n%s", err, encoded)
	}
	restored.ID = original.ID
	restored.CreatedAt = original.CreatedAt
	restored.UpdatedAt = original.UpdatedAt
	if !reflect.DeepEqual(original, restored) {
		t.Fatalf("round trip changed content: original=%+v restored=%+v\n%s", original, restored, encoded)
	}
	second, err := ProcessDefinitionDslMarshal(original)
	if err != nil || string(second) != string(encoded) {
		t.Fatal("YAML output is not stable")
	}
}

func TestProcessDefinitionDslMarshalEmpty(t *testing.T) {
	t.Parallel()
	if _, err := ProcessDefinitionDslMarshal(nil); err == nil {
		t.Fatal("accepted nil definition")
	}

	definition, err := ProcessDefinitionDslParse([]byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := ProcessDefinitionDslMarshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ProcessDefinitionDslParse(encoded)
	if err != nil || len(restored.Graph.Nodes) != 0 {
		t.Fatalf("empty definition round trip: %v\n%s", err, encoded)
	}
}

const testJSONNull = "null"
