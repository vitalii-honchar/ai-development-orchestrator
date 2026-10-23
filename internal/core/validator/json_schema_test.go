package validator_test

import (
	"os"
	"path/filepath"
	"testing"

	"go-software-factory/internal/core/validator"
)

func TestJSONSchemaDoesNotLoadExternalFiles(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "external.json")
	if err := os.WriteFile(path, []byte(`{"type":"string"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validator.JSONSchema(`{"$ref":"file://` + path + `"}`); err == nil {
		t.Fatal("schema validation loaded an external file")
	}
}

func TestJSONSchema(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, schema string
		valid        bool
	}{
		{name: "object schema", schema: "{\"type\":\"object\",\"properties\":{\"path_to_plan\"" +
			":{\"type\":\"string\"}},\"required\":[\"path_to_plan" +
			"\"]}", valid: true},
		{name: "local reference", schema: `{"$defs":{"path":{"type":"string"}},"$ref":"#/$defs/path"}`, valid: true},
		{name: "invalid JSON", schema: "{"},
		{name: "invalid type", schema: `{"type":"unknown"}`},
		{name: "invalid required", schema: `{"required":"path"}`},
		{name: "external reference", schema: `{"$ref":"file:///private/tmp/not-a-schema.json"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validator.JSONSchema(tc.schema); (err == nil) != tc.valid {
				t.Fatalf("schema validity=%v want %v: %v", err == nil, tc.valid, err)
			}
		})
	}
}

func TestJSONMatchesSchema(t *testing.T) {
	t.Parallel()
	const schema = "{\"type\":\"object\",\"properties\":{\"summary\":{\"ty" +
		"pe\":\"string\"},\"path_to_plan\":{\"type\":\"string\"" +
		"}},\"required\":[\"summary\",\"path_to_plan\"]," +
		"\"additionalProperties\":false}"
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{value: `{"summary":"Chosen approach","path_to_plan":"docs/plans/solution-plan-20261019.md"}`, valid: true},
		{value: `{"summary":"Missing path"}`},
		{value: `{"summary":"Wrong type","path_to_plan":42}`},
		{value: `{"summary":"Extra","path_to_plan":"plan.md","extra":true}`},
		{value: `{} {}`},
		{value: `null`},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Parallel()
			if err := validator.JSONMatchesSchema(schema, tc.value); (err == nil) != tc.valid {
				t.Fatalf("instance validity=%v want %v: %v", err == nil, tc.valid, err)
			}
		})
	}
}
