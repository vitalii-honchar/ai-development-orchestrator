package process

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"uuid"

	"github.com/gin-gonic/gin"
	"go-software-factory/internal/core/domain"
)

const (
	groupProcessDefinitions = "process-definitions"
	groupProcesses          = "processes"
)

type getProcessUseCase interface {
	GetAll(context.Context) ([]domain.Process, error)
	GetByID(context.Context, uuid.UUID) (*domain.Process, error)
}

type getProcessStepUseCase interface {
	GetByProcessID(context.Context, uuid.UUID) ([]domain.ProcessStep, error)
}

type getProcessDefinitionUseCase interface {
	GetAll(context.Context) ([]domain.ProcessDefinition, error)
	GetByID(context.Context, uuid.UUID) (*domain.ProcessDefinition, error)
}

type createProcessUseCase interface {
	Create(context.Context, uuid.UUID, *domain.Parameters) (*domain.Process, error)
}

type parameterView struct {
	Name    string
	Type    domain.ParameterType
	Value   string
	Summary string
}

func parameterViews(params *domain.Parameters) []parameterView {
	views := make([]parameterView, 0, params.Len())
	entries := maps.Collect(params.Entries())
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		param := entries[name]
		view := parameterView{Name: name, Type: param.Type, Value: param.String()}
		if param.Type == domain.ParameterTypeJSON || param.Type == domain.ParameterTypeJSONSchema {
			var formatted bytes.Buffer
			if err := json.Indent(&formatted, []byte(param.String()), "", "  "); err == nil {
				view.Value = formatted.String()
			}
			var output struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal([]byte(param.String()), &output); err == nil {
				view.Summary = output.Summary
			}
		}
		views = append(views, view)
	}

	return views
}

func renderError(c *gin.Context, message string, err error) {
	slog.ErrorContext(c.Request.Context(), message, "err", err)
	c.String(http.StatusInternalServerError, "%s", message)
}
