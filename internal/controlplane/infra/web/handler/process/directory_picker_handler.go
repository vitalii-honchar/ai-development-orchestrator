package process

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go-software-factory/internal/controlplane/infra/web/handler"
)

var (
	errDirectoryPickerCancelled   = errors.New("folder selection cancelled")
	errDirectoryPickerUnsupported = errors.New("native folder selection requires macOS")
)

type directoryPicker struct {
	choose func(context.Context) (string, error)
}

func NewDirectoryPicker() handler.Handler {
	return &directoryPicker{choose: chooseDirectory}
}

func (*directoryPicker) Group() string  { return groupProcesses }
func (*directoryPicker) Method() string { return http.MethodPost }
func (*directoryPicker) Path() string   { return "/choose-directory" }

func (p *directoryPicker) Handle(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	path, err := p.choose(ctx)
	switch {
	case errors.Is(err, errDirectoryPickerCancelled):
		c.Status(http.StatusNoContent)
	case errors.Is(err, errDirectoryPickerUnsupported):
		c.JSON(http.StatusNotImplemented, gin.H{
			"error": "Folder selection is available when the server runs locally on macOS.",
		})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Unable to open the folder chooser."})
	default:
		c.JSON(http.StatusOK, gin.H{"path": path})
	}
}

func chooseDirectory(ctx context.Context) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errDirectoryPickerUnsupported
	}
	// The script is fixed; no request data is interpreted as AppleScript or shell code.
	output, err := exec.CommandContext(ctx, "osascript", "-e",
		`activate`, "-e",
		`return POSIX path of (choose folder with prompt "Choose a working directory")`).Output()
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && strings.Contains(string(exitError.Stderr), "(-128)") {
		return "", errDirectoryPickerCancelled
	}
	if err != nil {
		return "", fmt.Errorf("open folder chooser: %w", err)
	}

	return strings.TrimSuffix(string(output), "\n"), nil
}
