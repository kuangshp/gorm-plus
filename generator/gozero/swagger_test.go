package gozero

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kuangshp/gorm-plus/httpswagger"
)

func TestSwaggerPlaceholderOnlyForGeneratedMainAndPreservesDocument(t *testing.T) {
	for _, mode := range []string{"go", "new"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n//go:embed swagger/swagger.json\n"), 0600); err != nil {
				t.Fatal(err)
			}
			arguments := []string{"api", "go", "--dir=" + dir}
			if mode == "new" {
				arguments = []string{"api", "new", "--style", "goZero", dir}
			}
			if err := ensureSwaggerPlaceholder(arguments); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "swagger/swagger.json")
			data, err := os.ReadFile(file)
			if err != nil || string(data) != httpswagger.Placeholder {
				t.Fatalf("missing compile placeholder: %s, %v", data, err)
			}
			const document = `{"real":"document"}`
			if err := os.WriteFile(file, []byte(document), 0600); err != nil {
				t.Fatal(err)
			}
			if err := ensureSwaggerPlaceholder(arguments); err != nil {
				t.Fatal(err)
			}
			data, _ = os.ReadFile(file)
			if string(data) != document {
				t.Fatal("code regeneration overwrote an existing Swagger document")
			}
		})
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureSwaggerPlaceholder([]string{"api", "go", "-dir", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "swagger")); !os.IsNotExist(err) {
		t.Fatal("unrelated custom template received Swagger artifacts")
	}
}
