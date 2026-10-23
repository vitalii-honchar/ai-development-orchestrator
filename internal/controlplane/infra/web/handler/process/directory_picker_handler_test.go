package process

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDirectoryPickerResponse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		path   string
		err    error
		status int
		body   string
	}{
		{"selected", "/Users/example/My project", nil, http.StatusOK, `"path":"/Users/example/My project"`},
		{"cancelled", "", errDirectoryPickerCancelled, http.StatusNoContent, ""},
		{"unsupported", "", errDirectoryPickerUnsupported, http.StatusNotImplemented,
			"Folder selection is available when the server runs locally on macOS."},
		{"failed", "", errors.New("private system failure"), http.StatusInternalServerError,
			"Unable to open the folder chooser."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			picker := &directoryPicker{choose: func(ctx context.Context) (string, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Error("native dialog must have a bounded lifetime")
				}

				return tc.path, tc.err
			}}
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/processes/choose-directory", nil)
			picker.Handle(ctx)
			ctx.Writer.WriteHeaderNow()
			if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.body) ||
				strings.Contains(response.Body.String(), "private system failure") {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestChooseDirectory(t *testing.T) {
	if runtime.GOOS != "darwin" {
		if _, err := chooseDirectory(t.Context()); !errors.Is(err, errDirectoryPickerUnsupported) {
			t.Fatalf("non-macOS picker error: %v", err)
		}

		return
	}
	for _, tc := range []struct {
		name, script, path string
		wantErr            error
		wantFailure        bool
	}{
		{"selected", "printf '/tmp/My project/\\n'", "/tmp/My project/", nil, false},
		{"cancelled", "printf 'User canceled. (-128)' >&2; exit 1", "", errDirectoryPickerCancelled, true},
		{"failed", "exit 2", "", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			executable := filepath.Join(bin, "osascript")
			// #nosec G306 -- This private temporary test fixture must be executable by its owner.
			if err := os.WriteFile(executable, []byte("#!/bin/sh\n"+tc.script+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			path, err := chooseDirectory(t.Context())
			if path != tc.path || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) ||
				(err != nil) != tc.wantFailure {
				t.Fatalf("picker = %q, %v", path, err)
			}
		})
	}
}
