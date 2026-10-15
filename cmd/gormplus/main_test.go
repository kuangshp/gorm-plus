package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSwaggerCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "service.yaml"), []byte("SwaggerConfig:\n  Enabled: false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"swagger", "--config", "service.yaml", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "swagger/swagger.json")); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"swagger-open", "--config", "service.yaml", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{{"swagger"}, {"swagger", "--unknown"}, {"swagger", "unexpected"}} {
		if err := run(arguments); err == nil {
			t.Fatalf("invalid arguments accepted: %v", arguments)
		}
	}
	if err := run([]string{"swagger-open", "--timeout", "0s"}); err == nil {
		t.Fatal("unbounded browser wait accepted")
	}
}
