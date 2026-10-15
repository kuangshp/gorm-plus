package gozero

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTemplateHome(t *testing.T) {
	destination := t.TempDir()
	if err := WriteTemplateHome(destination); err != nil {
		t.Fatalf("WriteTemplateHome() error = %v", err)
	}

	expectedFiles := []string{
		"config.tpl",
		"context.tpl",
		"etc.tpl",
		"handler.tpl",
		"logic.tpl",
		"main.tpl",
		"middleware.tpl",
	}
	for _, name := range expectedFiles {
		path := filepath.Join(destination, "api", name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("exported template %q: %v", path, err)
		}
	}

	handlerContent, err := os.ReadFile(filepath.Join(destination, "api", "handler.tpl"))
	if err != nil {
		t.Fatalf("read handler template: %v", err)
	}
	handler := string(handlerContent)
	if !strings.Contains(handler, "github.com/kuangshp/gorm-plus/validation") {
		t.Fatal("handler template does not use shared validation package")
	}
	if strings.Contains(handler, "github.com/kuangshp/go-utils") {
		t.Fatal("handler template still depends on go-utils")
	}
	if strings.Contains(handler, "kg-api/common/utils") {
		t.Fatal("handler template still contains project-specific validation import")
	}
}

func TestWriteTemplateHomeRejectsEmptyDestination(t *testing.T) {
	if err := WriteTemplateHome(" "); err == nil {
		t.Fatal("WriteTemplateHome() error = nil, want empty destination error")
	}
}
