package domain

import "errors"

var ErrProcessDefinitionNotFound = errors.New("process definition not found")
var ErrProcessDefinitionDuplicateName = errors.New("process definition name already exists in this state")
var ErrProcessDefinitionVersionConflict = errors.New("process definition version conflict")
