package domain

import "testing"

func TestProcessContextResolvesInputsAndStepOutputs(t *testing.T) {
	t.Parallel()
	context, err := NewProcessContext(
		testParameters(map[string]ParameterValue{
			"description": {Type: ParameterTypeString, Value: "Build a tracker"},
		}),
		map[string]*Parameters{
			testBrainstormStep: testParameters(map[string]ParameterValue{
				"output": {Type: ParameterTypeJSON, Value: `{"path":"/project/plan.md","number":9007199254740993}`},
			}),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source string
		kind   ParameterType
		want   string
	}{
		{"inputs.description", ParameterTypeString, "Build a tracker"},
		{"steps.brainstorm.output.path", ParameterTypeFilePath, "/project/plan.md"},
		{"steps.brainstorm.output.number", ParameterTypeJSON, "9007199254740993"},
	} {
		value, err := context.Resolve(tc.source, tc.kind)
		if err != nil || value.String() != tc.want || value.Type != tc.kind {
			t.Fatalf("resolve %q = %+v, %v", tc.source, value, err)
		}
	}
	for _, source := range []string{
		"inputs.missing", "steps.pending.output.path", "steps.brainstorm.output.missing", "brainstorm.output.path",
	} {
		if _, err := context.Resolve(source, ParameterTypeString); err == nil {
			t.Fatalf("resolved unavailable source %q", source)
		}
	}
}

func TestNewProcessContextRejectsInvalidStepNames(t *testing.T) {
	t.Parallel()
	if _, err := NewProcessContext(nil, map[string]*Parameters{" ": nil}); err == nil {
		t.Fatal("accepted a blank step name")
	}
}
