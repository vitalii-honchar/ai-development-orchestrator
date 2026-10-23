package domain

import "errors"

var (
	ErrProcessNotFound            = errors.New("process not found")
	ErrProcessVersionConflict     = errors.New("process version conflict")
	ErrProcessTerminal            = errors.New("process is terminal")
	ErrProcessStepNotFound        = errors.New("process step not found")
	ErrProcessStepVersionConflict = errors.New("process step version conflict")
	ErrProcessStepTerminal        = errors.New("process step is terminal")
)
