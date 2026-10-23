package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParametersValidateConstructionAndDecoding(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		`{"bad":{"type":"unknown","value":"text"}}`,
		`{" ":{"type":"STRING","value":"text"}}`,
		`{"name":{"type":"STRING","value":42}}`,
		`{"path":{"type":"FILE_PATH","value":"relative/path"}}`,
		`{"schema":{"type":"JSON_SCHEMA","value":{"type":"wrong"}}}`,
		`{"name":{"type":"STRING","value":"both","from":"inputs.name"}}`,
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			var params Parameters
			if err := json.Unmarshal([]byte(source), &params); err == nil {
				t.Fatalf("accepted invalid parameters: %s", source)
			}
		})
	}
}

func TestParametersKeepSourcesOpaque(t *testing.T) {
	t.Parallel()
	params, err := NewParameters(map[string]ParameterValue{
		"name": {Type: ParameterTypeString, From: "records.current.name"},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var restored Parameters
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Get("name").From != "records.current.name" {
		t.Fatal("source metadata changed during round trip")
	}
}

func TestParametersProtectValidatedEntries(t *testing.T) {
	t.Parallel()
	values := map[string]ParameterValue{"name": {Type: ParameterTypeString, Value: testOriginalValue}}
	params, err := NewParameters(values)
	if err != nil {
		t.Fatal(err)
	}
	values["name"] = ParameterValue{Type: ParameterTypeString, Value: "changed"}
	if params.String("name") != testOriginalValue {
		t.Fatal("constructor retained a mutable map alias")
	}
	if err := params.Set("name", ParameterValue{Type: ParameterTypeString, Value: 42}); err == nil {
		t.Fatal("invalid update accepted")
	}
	if params.String("name") != testOriginalValue {
		t.Fatal("invalid update changed validated data")
	}
}

func testParameters(values map[string]ParameterValue) *Parameters {
	params, err := NewParameters(values)
	if err != nil {
		panic(err)
	}

	return params
}

func TestParameterValueAndDeclaration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		typeName ParameterType
		value    string
	}{
		{ParameterTypeString, ""},
		{ParameterTypeReference, "PROCESS:feature"},
		{ParameterTypeReferencesArray, `["PROCESS:feature","RESOURCE:source"]`},
		{ParameterTypeJSON, `{"summary":"Ran the task."}`},
	} {
		value, err := NewParameterValue(WithParameterValueType(tc.typeName), WithParameterValueValue(tc.value))
		if err != nil || value.Type != tc.typeName || value.Value != tc.value {
			t.Fatalf("parameter value: %+v, %v", value, err)
		}
		for _, required := range []bool{false, true} {
			input, err := NewParameterValue(WithParameterValueType(tc.typeName), WithParameterValueRequired(required))
			if err != nil || input.Type != tc.typeName || input.Required != required {
				t.Fatalf("input: %+v, %v", input, err)
			}
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var restored ParameterValue
		if err := json.Unmarshal(data, &restored); err != nil || !reflect.DeepEqual(value, &restored) {
			t.Fatalf("parameter JSON round trip: %+v, %v", restored, err)
		}
	}
	for _, typeName := range []ParameterType{"", testUnknown, ParameterType(string([]byte{0xff}))} {
		if model, err := NewParameterValue(WithParameterValueType(typeName)); err == nil || model != nil {
			t.Fatal("accepted invalid parameter type")
		}
		if model, err := NewParameterValue(WithParameterValueType(typeName)); err == nil || model != nil {
			t.Fatal("accepted invalid input type")
		}
	}
	if model, err := NewParameterValue(
		WithParameterValueType(ParameterTypeString),
		WithParameterValueValue(string([]byte{0xff})),
	); err == nil || model != nil {
		t.Fatal("accepted invalid value text")
	}
}

func TestJSONParameterValidation(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "not JSON", `{"summary":`, `{} {}`} {
		if model, err := NewParameterValue(
			WithParameterValueType(ParameterTypeJSON), WithParameterValueValue(value),
		); err == nil || model != nil {
			t.Fatalf("accepted invalid JSON %q", value)
		}
	}
}

const (
	testContextDescription = "description"
	testTrackerFeature     = "Build a tracker"
	testOriginalValue      = "original"
	testOutputField        = "output"
	testBrainstormStep     = "brainstorm"
)
