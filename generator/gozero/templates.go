// Package gozero manages the goctl templates used by the gorm-plus generator.
// It intentionally uses only the Go standard library and does not import
// go-zero or another HTTP framework.
package gozero

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/api/*.tpl
var templateFiles embed.FS

// WriteTemplateHome exports the embedded templates into a goctl --home
// directory. Existing template files with the same names are overwritten.
func WriteTemplateHome(destination string) error {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return errors.New("goctl template destination must not be empty")
	}

	return fs.WalkDir(templateFiles, "templates", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel("templates", path)
		if err != nil {
			return fmt.Errorf("resolve embedded template path %q: %w", path, err)
		}
		targetPath := filepath.Join(destination, relativePath)
		if entry.IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("create goctl template directory %q: %w", targetPath, err)
			}
			return nil
		}

		content, err := templateFiles.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded goctl template %q: %w", path, err)
		}
		if err := os.WriteFile(targetPath, content, 0644); err != nil {
			return fmt.Errorf("write goctl template %q: %w", targetPath, err)
		}
		return nil
	})
}
