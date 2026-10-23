package process

import (
	"context"
	"errors"
	"net/http"
	"time"
	"uuid"

	"github.com/gin-gonic/gin"
	"go-software-factory/internal/controlplane/infra/web/handler"
	"go-software-factory/internal/controlplane/infra/web/processgraph"
	"go-software-factory/internal/core/domain"
)

type detailView struct {
	processes   getProcessUseCase
	steps       getProcessStepUseCase
	definitions getProcessDefinitionUseCase
}

type stepView struct {
	Node   processgraph.Node
	Params []parameterView
	Result []parameterView
}

func NewDetailView(
	processes getProcessUseCase, steps getProcessStepUseCase, definitions getProcessDefinitionUseCase,
) handler.Handler {
	return &detailView{processes: processes, steps: steps, definitions: definitions}
}

func (*detailView) Group() string  { return groupProcesses }
func (*detailView) Method() string { return http.MethodGet }
func (*detailView) Path() string   { return "/:id" }

func (v *detailView) Handle(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.String(http.StatusNotFound, "Execution not found")

		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	execution, err := v.processes.GetByID(ctx, id)
	if errors.Is(err, domain.ErrProcessNotFound) {
		c.String(http.StatusNotFound, "Execution not found")

		return
	}
	if err != nil {
		renderError(c, "Unable to load execution", err)

		return
	}
	definition, err := v.definitions.GetByID(ctx, execution.ProcessDefinitionID)
	if err != nil && !errors.Is(err, domain.ErrProcessDefinitionNotFound) {
		renderError(c, "Unable to load execution", err)

		return
	}
	steps, err := v.steps.GetByProcessID(ctx, execution.ID)
	if err != nil {
		renderError(c, "Unable to load execution steps", err)

		return
	}
	graph := processgraph.Build(definition, execution, steps)
	views := make([]stepView, len(graph.Nodes))
	for i, node := range graph.OrderedNodes() {
		views[i].Node = node
		if node.Step != nil {
			views[i].Params, views[i].Result = parameterViews(node.Step.Params), parameterViews(node.Step.Result)
		}
	}
	var inputs *domain.Parameters
	if execution.Context != nil {
		inputs = execution.Context.Inputs
	}
	c.HTML(http.StatusOK, "processes/detail", gin.H{
		"Process": execution, "Definition": definition, "Steps": views, "ProcessesPage": true, "Graph": graph,
		"ProcessInputs": parameterViews(inputs), "ProcessResult": parameterViews(execution.Result),
	})
}
