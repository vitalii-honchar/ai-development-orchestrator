package validator

import (
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func JSONSchema(schema string) error {
	_, err := compileJSONSchema(schema)

	return err
}

func JSONMatchesSchema(schema, value string) error {
	compiled, err := compileJSONSchema(schema)
	if err != nil {
		return err
	}
	instance, err := jsonschema.UnmarshalJSON(strings.NewReader(value))
	if err != nil {
		return fmt.Errorf("decode JSON instance: %w", err)
	}
	if err := compiled.Validate(instance); err != nil {
		return fmt.Errorf("validate JSON instance: %w", err)
	}

	return nil
}

func compileJSONSchema(schema string) (*jsonschema.Schema, error) {
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(schema))
	if err != nil {
		return nil, fmt.Errorf("decode JSON schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(nil)
	// No external loader: schemas are self-contained, including local $defs.
	if err := compiler.AddResource("urn:factory:output", document); err != nil {
		return nil, fmt.Errorf("register JSON schema: %w", err)
	}
	compiled, err := compiler.Compile("urn:factory:output")
	if err != nil {
		return nil, fmt.Errorf("compile JSON schema: %w", err)
	}

	return compiled, nil
}
