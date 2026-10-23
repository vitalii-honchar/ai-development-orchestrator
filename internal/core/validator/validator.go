package validator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-playground/validator/v10"
)

var validate = newValidator()

func newValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	for tag, rule := range map[string]validator.Func{
		"notblank":   notBlank,
		"pathsyntax": pathSyntax,
		"nonnull_if": nonNullIf,
		"utf8text":   utf8Text,
		"json_if":    jsonIf,
	} {
		if err := v.RegisterValidation(tag, rule); err != nil {
			panic(fmt.Errorf("register validation %s: %w", tag, err))
		}
	}

	return v
}

func utf8Text(field validator.FieldLevel) bool {
	return utf8.ValidString(field.Field().String())
}

func jsonIf(field validator.FieldLevel) bool {
	params := strings.Fields(field.Param())
	if len(params) != 2 {
		panic("json_if requires a field name and a value")
	}
	if field.Parent().FieldByName(params[0]).String() != params[1] {
		return true
	}

	return json.Valid([]byte(field.Field().String()))
}

func notBlank(field validator.FieldLevel) bool {
	return strings.TrimSpace(field.Field().String()) != ""
}

func pathSyntax(field validator.FieldLevel) bool {
	return notBlank(field) && !strings.ContainsRune(field.Field().String(), '\x00')
}

// nonNullIf requires a nonempty, non-null JSON value when the sibling field
// matches any of the supplied values. The json tag validates JSON syntax.
func nonNullIf(field validator.FieldLevel) bool {
	params := strings.Fields(field.Param())
	if len(params) < 2 {
		panic("nonnull_if requires a field name and at least one value")
	}

	other := field.Parent().FieldByName(params[0])
	for _, value := range params[1:] {
		if other.String() == value {
			result := bytes.TrimSpace(field.Field().Bytes())

			return len(result) > 0 && !bytes.Equal(result, []byte("null"))
		}
	}

	return true
}

func Struct[T any](s T) (T, error) {
	if err := validate.Struct(s); err != nil {
		var zero T

		return zero, fmt.Errorf("validate: %w", err)
	}

	return s, nil
}
