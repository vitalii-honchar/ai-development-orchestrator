package process

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	csrf "github.com/utrack/gin-csrf"
	"go-software-factory/internal/controlplane/infra/web/handler"
	"go-software-factory/internal/core/domain"
	engineprocess "go-software-factory/internal/engine/app/process"
)

var errExecutionInputs = errors.New("invalid execution inputs")

type createView struct {
	definitions getProcessDefinitionUseCase
	create      createProcessUseCase
	method      string
}

func NewCreateView(definitions getProcessDefinitionUseCase) handler.Handler {
	return &createView{definitions: definitions, method: http.MethodGet}
}

func NewCreate(definitions getProcessDefinitionUseCase, create createProcessUseCase) handler.Handler {
	return &createView{definitions: definitions, create: create, method: http.MethodPost}
}

func (*createView) Group() string    { return groupProcessDefinitions }
func (v *createView) Method() string { return v.method }
func (*createView) Path() string     { return "/:id/execute" }

func (v *createView) Handle(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.String(http.StatusNotFound, "Process definition not found")

		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	definition, err := v.definitions.GetByID(ctx, id)
	if errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		c.String(http.StatusNotFound, "Process definition not found")

		return
	}
	if err != nil {
		renderError(c, "Unable to load process definition", err)

		return
	}
	if definition.State != domain.ProcessDefinitionStateActive {
		c.String(http.StatusConflict, "Only ACTIVE process definitions can be executed")

		return
	}
	if v.method == http.MethodGet {
		v.render(c, http.StatusOK, definition, nil, "")

		return
	}
	params, values, err := executionInputs(c, definition.Inputs)
	if err != nil {
		v.render(c, http.StatusUnprocessableEntity, definition, values, err.Error())

		return
	}
	execution, err := v.create.Create(ctx, id, params)
	if errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		c.String(http.StatusNotFound, "Process definition not found")

		return
	}
	if errors.Is(err, engineprocess.ErrProcessDefinitionInactive) {
		c.String(http.StatusConflict, "Only ACTIVE process definitions can be executed")

		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "Start process execution failed", "err", err)
		v.render(c, http.StatusInternalServerError, definition, values, "Unable to start execution. Please try again.")

		return
	}
	c.Redirect(http.StatusSeeOther, "/processes/"+execution.ID.String())
}

func (*createView) render(
	c *gin.Context, status int, definition *domain.ProcessDefinition, values map[string]string, message string,
) {
	token := csrf.GetToken(c)
	if err := sessions.Default(c).Save(); err != nil {
		renderError(c, "Unable to open execution form", err)

		return
	}
	c.HTML(status, "processes/create", gin.H{
		"Definition": definition, "InputValues": values, "Error": message, "CSRFToken": token,
	})
}

func executionInputs(c *gin.Context, declared *domain.Parameters) (*domain.Parameters, map[string]string, error) {
	if err := c.Request.ParseForm(); err != nil {
		return nil, nil, fmt.Errorf("%w: unable to read form", errExecutionInputs)
	}
	values := make(map[string]string, declared.Len())
	for name := range declared.Entries() {
		values[name] = c.Request.PostForm.Get("input." + name)
	}
	for field := range c.Request.PostForm {
		if name, ok := strings.CutPrefix(field, "input."); ok {
			if !declared.Has(name) {
				return nil, values, fmt.Errorf("%w: unknown input %q", errExecutionInputs, name)
			}
		}
	}
	params := new(domain.Parameters)
	entries := maps.Collect(declared.Entries())
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		input := entries[name]
		if len(c.Request.PostForm["input."+name]) > 1 {
			return nil, values, fmt.Errorf("%w: input %q must be supplied once", errExecutionInputs, name)
		}
		value := values[name]
		if strings.TrimSpace(value) == "" {
			if input.Required {
				return nil, values, fmt.Errorf("%w: input %q is required", errExecutionInputs, name)
			}

			continue
		}
		if err := params.Set(name, domain.ParameterValue{Type: input.Type, Value: value}); err != nil {
			return nil, values, fmt.Errorf("%w: input %q must be a valid %s value", errExecutionInputs, name, input.Type)
		}
	}

	return params, values, nil
}
