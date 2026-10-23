package processdefinition

import (
	"go-software-factory/internal/controlplane/infra/web/handler"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

type listView struct {
	useCase getProcessDefinitionUseCase
}

func NewListView(useCase getProcessDefinitionUseCase) handler.Handler {
	return &listView{
		useCase: useCase,
	}
}

func (l *listView) Group() string {
	return groupProcessDefinitions
}

func (l *listView) Method() string {
	return http.MethodGet
}

func (l *listView) Path() string {
	return "/"
}

func (l *listView) Handle(c *gin.Context) {
	slog.Info("Get Process Definitions")

	processDefinitions, err := l.useCase.GetAll(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})

		return
	}

	c.HTML(http.StatusOK, "processdefinitions/page", gin.H{
		templateParamProcessDefinitions: processDefinitions,
	})
}
