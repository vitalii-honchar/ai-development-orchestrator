package process

import (
	"context"
	"net/http"
	"slices"
	"time"
	"uuid"

	"github.com/gin-gonic/gin"
	"go-software-factory/internal/controlplane/infra/web/handler"
	"go-software-factory/internal/core/domain"
)

const processDefinitionFilterParam = "process_definition_id"

type listView struct {
	processes   getProcessUseCase
	definitions getProcessDefinitionUseCase
}

func NewListView(processes getProcessUseCase, definitions getProcessDefinitionUseCase) handler.Handler {
	return &listView{processes: processes, definitions: definitions}
}

func (*listView) Group() string  { return groupProcesses }
func (*listView) Method() string { return http.MethodGet }
func (*listView) Path() string   { return "/" }

func (v *listView) Handle(c *gin.Context) {
	selected := c.Query(processDefinitionFilterParam)
	var definitionID uuid.UUID
	if selected != "" {
		id, err := uuid.Parse(selected)
		if err != nil {
			c.String(http.StatusBadRequest, "Invalid process definition filter")

			return
		}
		definitionID = id
		selected = id.String()
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	processes, err := v.processes.GetAll(ctx)
	if err != nil {
		renderError(c, "Unable to load executions", err)

		return
	}
	if selected != "" {
		processes = slices.DeleteFunc(processes, func(execution domain.Process) bool {
			return execution.ProcessDefinitionID != definitionID
		})
	}
	definitions, err := v.definitions.GetAll(ctx)
	if err != nil {
		renderError(c, "Unable to load executions", err)

		return
	}
	names := make(map[uuid.UUID]string, len(definitions))
	for i := range definitions {
		names[definitions[i].ID] = definitions[i].Name
	}
	_, selectedExists := names[definitionID]
	c.HTML(http.StatusOK, "processes/page", gin.H{
		"Processes": processes, "DefinitionNames": names, "ProcessesPage": true,
		"ProcessDefinitions": definitions, "SelectedDefinitionID": selected, "SelectedDefinitionExists": selectedExists,
	})
}
