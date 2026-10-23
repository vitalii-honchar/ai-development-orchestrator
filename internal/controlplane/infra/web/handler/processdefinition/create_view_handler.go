package processdefinition

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"go-software-factory/internal/controlplane/infra/web/handler"
	"go-software-factory/internal/core/domain"
	"go-software-factory/internal/core/dsl"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	csrf "github.com/utrack/gin-csrf"
)

const exampleDefinition = `name: Release
description: Build, test, and publish

inputs:
  source:
    type: REFERENCE
    required: true

reliability:
  timeout: 30m
  max_retries: 2
  retry_delay: 5s

steps:
  build:
    type: custom.build
    inputs:
      source:
        type: REFERENCE
        required: true
    next: [test, package]

  test:
    type: custom.test
    next: [publish]

  package:
    type: custom.package
    next: [publish]

  publish:
    type: custom.publish
`

type createView struct {
	useCase createProcessDefinitionUseCase
	method  string
}

func NewCreateView() handler.Handler {
	return &createView{method: http.MethodGet}
}

func NewCreate(useCase createProcessDefinitionUseCase) handler.Handler {
	return &createView{
		useCase: useCase,
		method:  http.MethodPost,
	}
}

func (v *createView) Group() string {
	return groupProcessDefinitions
}

func (v *createView) Method() string {
	return v.method
}

func (v *createView) Path() string {
	if v.method == http.MethodGet {
		return "/new"
	}

	return "/"
}

func (v *createView) Handle(c *gin.Context) {
	if v.method == http.MethodGet {
		renderCreate(c, http.StatusOK, exampleDefinition, "")

		return
	}

	source := c.PostForm("yaml")
	definition, err := dsl.ProcessDefinitionDslParse([]byte(source))
	if err != nil {
		renderCreate(c, http.StatusUnprocessableEntity, source, err.Error())

		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	created, err := v.useCase.Create(ctx, definition)
	if errors.Is(err, domain.ErrProcessDefinitionDuplicateName) {
		renderCreate(c, http.StatusConflict, source,
			"An active process definition with this name already exists. Choose a different name.")

		return
	}
	if err != nil {
		slog.Error("Create process definition failed", "err", err)
		renderCreate(c, http.StatusInternalServerError, source, "Unable to create process definition. Please try again.")

		return
	}
	c.Redirect(http.StatusSeeOther, "/process-definitions/"+created.ID.String())
}

func renderCreate(c *gin.Context, status int, source, message string) {
	token := csrf.GetToken(c)
	// gin-csrf ignores session save errors; do not render an unusable token.
	if err := sessions.Default(c).Save(); err != nil {
		slog.Error("Save creation session failed", "err", err)
		c.String(http.StatusInternalServerError, "Unable to open process definition editor")

		return
	}
	c.HTML(status, "processdefinitions/create", gin.H{
		"CSRFToken": token, "YAML": source, "Error": message,
		"ErrorTitle":    "Process definition could not be created",
		"FormAction":    "/process-definitions/",
		"CancelPath":    "/process-definitions/",
		"SubmitLabel":   "Create process definition",
		"EditorState":   "Active",
		"EditorVersion": 1,
		"EditorHint":    "Validation runs when you create the definition. It will be saved as Active, version 1.",
	})
}
