package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"go-software-factory/internal/core/validator"
)

var ErrInvalidParameters = errors.New("invalid parameters")

type ParameterType string

const (
	ParameterTypeString          ParameterType = "STRING"
	ParameterTypeReference       ParameterType = "REFERENCE"
	ParameterTypeReferencesArray ParameterType = "REFERENCES_ARRAY"
	ParameterTypeJSON            ParameterType = "JSON"
	ParameterTypeFilePath        ParameterType = "FILE_PATH"
	ParameterTypeJSONSchema      ParameterType = "JSON_SCHEMA"
)

type ParameterValue struct {
	Type     ParameterType `json:"type"`
	Value    any           `json:"value,omitempty"`
	Required bool          `json:"required,omitempty"`
	From     string        `json:"from,omitempty"`
}

type ParameterValueOpt func(*ParameterValue)

func WithParameterValueType(value ParameterType) ParameterValueOpt {
	return func(model *ParameterValue) { model.Type = value }
}

func WithParameterValueValue(value any) ParameterValueOpt {
	return func(model *ParameterValue) { model.Value = value }
}

func WithParameterValueRequired(value bool) ParameterValueOpt {
	return func(model *ParameterValue) { model.Required = value }
}

func WithParameterValueFrom(value string) ParameterValueOpt {
	return func(model *ParameterValue) { model.From = value }
}

func NewParameterValue(opts ...ParameterValueOpt) (*ParameterValue, error) {
	model := &ParameterValue{}
	for _, opt := range opts {
		opt(model)
	}
	if err := model.validate(); err != nil {
		return nil, err
	}

	return model, nil
}

func (v ParameterValue) String() string {
	value, _ := v.Value.(string)

	return value
}

func (v *ParameterValue) validate() error {
	switch v.Type {
	case ParameterTypeString, ParameterTypeReference, ParameterTypeReferencesArray,
		ParameterTypeJSON, ParameterTypeFilePath, ParameterTypeJSONSchema:
	default:
		return fmt.Errorf("%w: unsupported type %q", ErrInvalidParameters, v.Type)
	}
	if !utf8.ValidString(v.From) || (v.From != "" && v.Value != nil) {
		return fmt.Errorf("%w: invalid source metadata", ErrInvalidParameters)
	}
	if v.Value == nil {
		return nil // A validated declaration, resolved before executable step creation.
	}
	if v.Type == ParameterTypeJSON || v.Type == ParameterTypeJSONSchema || v.Type == ParameterTypeReferencesArray {
		if _, ok := v.Value.(string); !ok {
			encoded, err := json.Marshal(v.Value)
			if err != nil {
				return fmt.Errorf("encode parameter value: %w", err)
			}
			v.Value = string(encoded)
		}
	}
	value, ok := v.Value.(string)
	if !ok || !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s value must be valid text", ErrInvalidParameters, v.Type)
	}
	switch v.Type {
	case ParameterTypeJSON:
		if !json.Valid([]byte(value)) {
			return fmt.Errorf("%w: invalid JSON", ErrInvalidParameters)
		}
	case ParameterTypeJSONSchema:
		if err := validator.JSONSchema(value); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidParameters, err)
		}
	case ParameterTypeFilePath:
		if !filepath.IsAbs(value) || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("%w: FILE_PATH must be an absolute path", ErrInvalidParameters)
		}
	case ParameterTypeReference:
		return validateReference(value)
	case ParameterTypeReferencesArray:
		var references []string
		if err := json.Unmarshal([]byte(value), &references); err != nil {
			return fmt.Errorf("decode references: %w", err)
		}
		for _, reference := range references {
			if err := validateReference(reference); err != nil {
				return err
			}
		}
	}

	return nil
}

func validateReference(value string) error {
	origin, name, found := strings.Cut(value, ":")
	if !found {
		return fmt.Errorf("%w: invalid reference %q", ErrInvalidParameters, value)
	}
	_, err := NewReference(WithReferenceOrigin(Origin(origin)), WithReferenceValue(name))

	return err
}

// Parameters owns validated entries. Accessors return values rather than a mutable map.
type Parameters struct {
	values map[string]ParameterValue
}

func NewParameters(values map[string]ParameterValue) (*Parameters, error) {
	params := &Parameters{}
	for key, value := range values {
		if err := params.Set(key, value); err != nil {
			return nil, err
		}
	}

	return params, nil
}

func (p *Parameters) Set(key string, value ParameterValue) error {
	if strings.TrimSpace(key) == "" || !utf8.ValidString(key) {
		return fmt.Errorf("%w: invalid parameter name %q", ErrInvalidParameters, key)
	}
	if err := value.validate(); err != nil {
		return fmt.Errorf("parameter %q: %w", key, err)
	}
	if p.values == nil {
		p.values = make(map[string]ParameterValue)
	}
	p.values[key] = value

	return nil
}

func (p *Parameters) Get(key string) ParameterValue {
	if p == nil {
		return ParameterValue{}
	}

	return p.values[key]
}

func (p *Parameters) Has(key string) bool {
	if p == nil {
		return false
	}
	_, exists := p.values[key]

	return exists
}

func (p *Parameters) Delete(key string) {
	if p != nil {
		delete(p.values, key)
	}
}

func (p *Parameters) String(key string) string {
	return p.Get(key).String()
}

func (p *Parameters) Len() int {
	if p == nil {
		return 0
	}

	return len(p.values)
}

func (p *Parameters) Entries() iter.Seq2[string, ParameterValue] {
	return func(yield func(string, ParameterValue) bool) {
		if p == nil {
			return
		}
		for key, value := range p.values {
			if !yield(key, value) {
				return
			}
		}
	}
}

func (p *Parameters) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	if p.values == nil {
		return []byte("{}"), nil
	}
	encoded, err := json.Marshal(p.values)
	if err != nil {
		return nil, fmt.Errorf("encode parameters: %w", err)
	}

	return encoded, nil
}

func (p *Parameters) UnmarshalJSON(data []byte) error {
	var values map[string]ParameterValue
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&values); err != nil {
		return fmt.Errorf("decode parameters: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: expected one JSON object", ErrInvalidParameters)
	}
	validated, err := NewParameters(values)
	if err != nil {
		return err
	}
	p.values = validated.values

	return nil
}
