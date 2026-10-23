package domain

import (
	"fmt"

	"go-software-factory/internal/core/validator"
)

type Origin string

const (
	OriginProcess  Origin = "PROCESS"
	OriginResource Origin = "RESOURCE"
)

type Reference struct {
	Origin Origin `json:"origin" validate:"required,oneof=PROCESS RESOURCE"`
	Value  string `json:"value"  validate:"required,notblank,utf8text"`
}

type ReferenceOpt func(*Reference)

func WithReferenceOrigin(value Origin) ReferenceOpt {
	return func(model *Reference) {
		model.Origin = value
	}
}

func WithReferenceValue(value string) ReferenceOpt {
	return func(model *Reference) {
		model.Value = value
	}
}

func NewReference(opts ...ReferenceOpt) (*Reference, error) {
	model := &Reference{}
	for _, opt := range opts {
		opt(model)
	}

	return validator.Struct(model)
}

func (r *Reference) ToString() string {
	return fmt.Sprintf("%s:%s", r.Origin, r.Value)
}
