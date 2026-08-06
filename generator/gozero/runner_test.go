package gozero

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunGeneratesHandlerWithEmbeddedTemplates(t *testing.T) {
	if _, err := exec.LookPath("goctl"); err != nil {
		t.Skip("goctl is not installed")
	}

	projectDirectory := t.TempDir()
	apiPath := filepath.Join(projectDirectory, "demo.api")
	apiDefinition := `syntax = "v1"

info (
	title: "template test"
)

type PingReq {
	Name string ` + "`json:\"name\" validate:\"required\"`" + `
}

type PingResp {
	Message string ` + "`json:\"message\"`" + `
}

service demo-api {
	@handler Ping
	get /ping (PingReq) returns (PingResp)
}
`
	if err := os.WriteFile(apiPath, []byte(apiDefinition), 0644); err != nil {
		t.Fatalf("write API fixture: %v", err)
	}

	output, err := Run(
		context.Background(),
		RunOptions{},
		"api", "go", "-api", apiPath, "--dir", projectDirectory, "--style=goZero",
	)
	if err != nil {
		t.Fatalf("Run() error = %v\n%s", err, output)
	}
	handlerPath := filepath.Join(projectDirectory, "internal", "handler", "pingHandler.go")
	handlerContent, err := os.ReadFile(handlerPath)
	if err != nil {
		t.Fatalf("read generated handler: %v", err)
	}
	handler := string(handlerContent)
	if !strings.Contains(handler, "github.com/kuangshp/go-utils/k/validation") {
		t.Fatalf("generated handler does not use shared validation package:\n%s", handler)
	}
	if !strings.Contains(handler, "validation.Validate(&req)") {
		t.Fatalf("generated handler does not validate its request:\n%s", handler)
	}
	if !strings.Contains(handler, "validation.HasRules(&req)") {
		t.Fatalf("generated handler does not check for validation rules:\n%s", handler)
	}
}

func TestHasTemplateHomeArgument(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		want      bool
	}{
		{
			name:      "separate flag",
			arguments: []string{"api", "go", "--home", "/tmp/templates"},
			want:      true,
		},
		{
			name:      "equals flag",
			arguments: []string{"api", "go", "--home=/tmp/templates"},
			want:      true,
		},
		{
			name:      "missing flag",
			arguments: []string{"api", "go"},
			want:      false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasTemplateHomeArgument(test.arguments); got != test.want {
				t.Fatalf("hasTemplateHomeArgument() = %v, want %v", got, test.want)
			}
		})
	}
}
