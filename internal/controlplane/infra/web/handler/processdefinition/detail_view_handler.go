package processdefinition

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"
	"uuid"

	"go-software-factory/internal/controlplane/infra/web/handler"
	"go-software-factory/internal/controlplane/infra/web/processgraph"
	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/core/dsl"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	csrf "github.com/utrack/gin-csrf"
)

type detailView struct {
	getUseCase    getProcessDefinitionUseCase
	updateUseCase updateProcessDefinitionUseCase
	method        string
}

func NewDetailView(useCase getProcessDefinitionUseCase) handler.Handler {
	return &detailView{getUseCase: useCase, method: http.MethodGet}
}

func NewUpdate(getUseCase getProcessDefinitionUseCase, updateUseCase updateProcessDefinitionUseCase) handler.Handler {
	return &detailView{
		getUseCase:    getUseCase,
		updateUseCase: updateUseCase,
		method:        http.MethodPost,
	}
}

func (v *detailView) Group() string {
	return groupProcessDefinitions
}

func (v *detailView) Method() string {
	return v.method
}

func (v *detailView) Path() string {
	return "/:id"
}

func (v *detailView) Handle(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.String(http.StatusNotFound, "Process definition not found")

		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	definition, err := v.getUseCase.GetByID(ctx, id)
	if err != nil {
		v.renderLoadError(c, err)

		return
	}

	if v.method == http.MethodPost {
		v.update(c, ctx, definition)

		return
	}

	if c.Query("mode") == "edit" {
		source, err := dsl.ProcessDefinitionDslMarshal(definition)
		if err != nil {
			v.renderLoadError(c, err)

			return
		}
		v.renderEdit(c, http.StatusOK, definition, string(source), "", definition.Version)

		return
	}

	c.HTML(http.StatusOK, "processdefinitions/detail", gin.H{
		"Definition": definition,
		"Graph":      processgraph.Build(definition, nil, nil),
	})
}

func (v *detailView) update(c *gin.Context, ctx context.Context, definition *domain.ProcessDefinition) {
	source := c.PostForm("yaml")
	version, err := strconv.Atoi(c.PostForm("version"))
	if err != nil || version < 1 {
		v.renderEdit(c, http.StatusUnprocessableEntity, definition, source,
			"A valid definition version is required. Reload the editor before saving.", definition.Version)

		return
	}
	changes, err := dsl.ProcessDefinitionDslParse([]byte(source))
	if err != nil {
		v.renderEdit(c, http.StatusUnprocessableEntity, definition, source, err.Error(), version)

		return
	}

	changes.Version = version
	updated, err := v.updateUseCase.Update(ctx, definition.ID, changes)
	if errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		v.renderLoadError(c, err)

		return
	}
	if errors.Is(err, domain.ErrProcessDefinitionVersionConflict) {
		v.renderEdit(c, http.StatusConflict, definition, source,
			"This definition changed since you opened the editor. "+
				"Reload it and apply your changes to the latest version.", version)

		return
	}
	if errors.Is(err, domain.ErrProcessDefinitionDuplicateName) {
		v.renderEdit(c, http.StatusConflict, definition, source,
			"A process definition with this name already exists in this state. Choose a different name.", version)

		return
	}
	if err != nil {
		slog.Error("Update process definition failed", "err", err)
		v.renderEdit(c, http.StatusInternalServerError, definition, source,
			"Unable to save process definition. Please try again.", version)

		return
	}

	c.Redirect(http.StatusSeeOther, "/process-definitions/"+updated.ID.String())
}

func (v *detailView) renderEdit(
	c *gin.Context, status int, definition *domain.ProcessDefinition, source, message string, version int,
) {
	token := csrf.GetToken(c)
	if err := sessions.Default(c).Save(); err != nil {
		v.renderLoadError(c, err)

		return
	}

	path := "/process-definitions/" + definition.ID.String()
	c.HTML(status, "processdefinitions/detail", gin.H{
		"Definition":      definition,
		"EditMode":        true,
		"CSRFToken":       token,
		"YAML":            source,
		"Error":           message,
		"ErrorTitle":      "Process definition could not be saved",
		"FormAction":      path,
		"CancelPath":      path,
		"SubmitLabel":     "Save changes",
		"EditorState":     string(definition.State),
		"EditorVersion":   version,
		"ExpectedVersion": version,
		"EditorHint": "Saving updates this definition. Its ID, state, and creation time stay the same. " +
			"The version increases to protect against conflicting edits.",
	})
}

func (v *detailView) renderLoadError(c *gin.Context, err error) {
	if errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		c.String(http.StatusNotFound, "Process definition not found")

		return
	}

	slog.Error("Load process definition failed", "err", err)
	c.String(http.StatusInternalServerError, "Unable to load process definition")
}
