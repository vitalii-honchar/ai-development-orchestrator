package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/gin-contrib/multitemplate"
	"go-software-factory/internal/core/domain"
)

//go:embed templates
var templatesFS embed.FS

func newRenderer() multitemplate.Renderer {
	renderer := multitemplate.NewRenderer()

	var layouts, components, pages []string
	err := fs.WalkDir(templatesFS, "templates", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}

		switch {
		case strings.HasPrefix(path, "templates/layouts/"):
			layouts = append(layouts, path)
		case strings.HasPrefix(path, "templates/components/"):
			components = append(components, path)
		case strings.HasPrefix(path, "templates/pages/"):
			pages = append(pages, path)
		}

		return nil
	})
	if err != nil {
		panic(err)
	}

	// Generate template map
	functions := template.FuncMap{
		"executionDuration": formatExecutionDuration, "inputLabel": inputLabel, "inputNames": inputNames,
	}
	for _, page := range pages {
		files := make([]string, 0, 1+len(layouts)+len(components))
		files = append(files, page)
		files = append(files, layouts...)
		files = append(files, components...)

		name := strings.TrimPrefix(page, "templates/pages/")
		name = strings.TrimSuffix(name, ".html")

		renderer.AddFromFSFuncs(name, functions, templatesFS, files...)
	}

	return renderer
}

func inputLabel(name string) string {
	label := []rune(strings.ReplaceAll(name, "_", " "))
	if len(label) > 0 {
		label[0] = unicode.ToUpper(label[0])
	}

	return string(label)
}

func inputNames(params *domain.Parameters) []string {
	names := make([]string, 0, params.Len())
	for name := range params.Entries() {
		names = append(names, name)
	}
	slices.SortFunc(names, func(first, second string) int {
		if params.Get(first).Required != params.Get(second).Required {
			if params.Get(first).Required {
				return -1
			}

			return 1
		}

		return strings.Compare(first, second)
	})

	return names
}

func formatExecutionDuration(duration time.Duration) string {
	unit, suffix := time.Second, "s"
	if duration >= time.Minute {
		unit, suffix = time.Minute, "m"
	}

	return fmt.Sprintf("%d%s", duration.Round(unit)/unit, suffix)
}
