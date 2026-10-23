package validator_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go-software-factory/internal/core/validator"

	playground "github.com/go-playground/validator/v10"
)

type sample struct {
	Name   string `validate:"required,notblank"`
	Path   string `validate:"required,pathsyntax"`
	State  string
	Result json.RawMessage `validate:"nonnull_if=State DONE ERROR,omitempty,json"`
}

const testBuild = "build"

func TestStruct(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		value sample
		field string
	}{
		{"valid", sample{Name: " café - build_1 ", Path: "./missing/directory/"}, ""},
		{"unicode blank", sample{Name: "\u2003\t", Path: "."}, "Name"},
		{"NUL path", sample{Name: testBuild, Path: "a\x00b"}, "Path"},
		{"blank path", sample{Name: testBuild, Path: " \n\t"}, "Path"},
		{"terminal absent", sample{Name: testBuild, Path: ".", State: "DONE"}, "Result"},
		{
			"other terminal null",
			sample{Name: testBuild, Path: ".", State: "ERROR", Result: json.RawMessage(`null`)},
			"Result",
		},
		{"terminal false", sample{Name: testBuild, Path: ".", State: "DONE", Result: json.RawMessage(`false`)}, ""},
		{"optional null", sample{Name: testBuild, Path: ".", State: "WAITING", Result: json.RawMessage(`null`)}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := tc.value
			got, err := validator.Struct(&input)
			if tc.field == "" {
				if err != nil || got != &input {
					t.Fatalf("expected original pointer, got %p, %v", got, err)
				}

				return
			}
			var failures playground.ValidationErrors
			if got != nil || !errors.As(err, &failures) || len(failures) != 1 {
				t.Fatalf("expected nil and one wrapped field error, got %+v, %v", got, err)
			}
			if failures[0].Field() != tc.field || !strings.HasPrefix(err.Error(), "validate: ") {
				t.Fatalf("expected wrapped %s error, got %v", tc.field, err)
			}
		})
	}
}

func TestStructValueFailureReturnsZero(t *testing.T) {
	t.Parallel()
	type named struct {
		Name string `validate:"required,notblank"`
	}
	got, err := validator.Struct(named{Name: " "})
	if err == nil || got != (named{}) {
		t.Fatalf("expected zero value and error, got %+v, %v", got, err)
	}
}
