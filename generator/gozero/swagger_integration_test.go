package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	swaggergen "github.com/kuangshp/gorm-plus/generator/swagger"
)

// TestGoctlSwaggerIntegration 验证真实 goctl、生成的 main 和独立部署后的内嵌资源。
func TestGoctlSwaggerIntegration(t *testing.T) {
	if os.Getenv("GORMPLUS_GOCTL_INTEGRATION") != "1" {
		t.Skip("set GORMPLUS_GOCTL_INTEGRATION=1 to run goctl integration")
	}
	if _, err := exec.LookPath("goctl"); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	spec := "syntax = \"v1\"\ntype Output {\n ID int64 `json:\"id\"`\n}\nservice sample-api {\n @handler Echo\n get /echo returns (Output)\n @handler Remove\n delete /remove\n @handler Download\n get /download returns (string)\n}\n"
	for name, value := range map[string]string{
		"sample.api": "import \"./desc/routes.api\"\n", "desc/routes.api": spec,
		"go.mod": fmt.Sprintf("module swaggerfixture\n\ngo 1.25.5\nrequire (\n github.com/kuangshp/gorm-plus v0.0.0\n github.com/zeromicro/go-zero v1.10.2\n)\nreplace github.com/kuangshp/gorm-plus => %s\n", root),
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	output, err := Run(context.Background(), RunOptions{}, "api", "go", "--api", filepath.Join(dir, "sample.api"), "--dir", dir, "--style=goZero")
	if err != nil {
		t.Fatalf("generate service: %v\n%s", err, output)
	}
	// 让 API 的真实响应可与文档结构比较。
	logicPath := filepath.Join(dir, "internal/logic/echoLogic.go")
	logic, err := os.ReadFile(logicPath)
	if err != nil {
		t.Fatal(err)
	}
	logic = bytes.Replace(logic, []byte("// todo: add your logic here and delete this line"), []byte("resp = &types.Output{ID: 17}"), 1)
	if err := os.WriteFile(logicPath, logic, 0600); err != nil {
		t.Fatal(err)
	}
	build := func(binary string) {
		t.Helper()
		command := exec.Command("go", "build", "-mod=mod", "-o", binary, ".")
		command.Dir = dir
		command.Env = append(os.Environ(), "GOWORK=off")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build fixture: %v\n%s", err, output)
		}
	}
	binary := filepath.Join(dir, "service")
	build(binary)
	t.Run("disabled with placeholder", func(t *testing.T) {
		base := startHTTPFixture(t, dir, binary, "SwaggerConfig:\n  Enabled: false\n", func(output string) {
			if strings.Contains(output, "Swagger UI:") || strings.Contains(output, "Swagger JSON:") {
				t.Errorf("disabled Swagger printed documentation URLs: %s", output)
			}
		})
		status, _, _ := requestHTTPFixture(t, base, http.MethodGet, "/swagger/swagger.json", "", "")
		if status != http.StatusNotFound {
			t.Fatalf("disabled Swagger still exposed: %d", status)
		}
	})
	t.Run("enabled without generated document fails", func(t *testing.T) {
		file := filepath.Join(dir, "missing.yaml")
		config := "Name: missing\nHost: 127.0.0.1\nPort: 19999\nLogRotation:\n  Enabled: false\nSwaggerConfig:\n  Enabled: true\n"
		if err := os.WriteFile(file, []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "-f", file)
		command.Dir = dir
		output, err := command.CombinedOutput()
		if err == nil || ctx.Err() != nil || !bytes.Contains(output, []byte("Swagger 文档未生成")) {
			t.Fatalf("missing document was not rejected: %v\n%s", err, output)
		}
	})
	configPath := filepath.Join(dir, "swagger-config.yaml")
	if err := os.WriteFile(configPath, []byte("SwaggerConfig:\n  Enabled: true\n  Path: /docs\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := swaggergen.Generate(context.Background(), swaggergen.Options{ConfigFile: configPath, Dir: dir}); err != nil {
		t.Fatalf("generate Swagger from imported API: %v\n%s", err, output)
	}
	// 只带二进制到另一个空目录运行，证明不读取 API、swagger.json 或 UI 静态目录。
	deployment := t.TempDir()
	binary = filepath.Join(deployment, "service")
	build(binary)
	t.Run("embedded document matches response and bypass", func(t *testing.T) {
		uploads := t.TempDir()
		if err := os.WriteFile(filepath.Join(uploads, "image.txt"), []byte("static remains available"), 0600); err != nil {
			t.Fatal(err)
		}
		var base string
		base = startHTTPFixture(t, deployment, binary, fmt.Sprintf(`Host: 0.0.0.0
SwaggerConfig:
  Enabled: true
  Path: /docs
ResponseConfig:
  Enabled: true
  BypassPaths: ["/download"]
StaticConfig:
  Enabled: true
  Directory: %q
  Prefix: /uploads
`, uploads), func(output string) {
			for _, line := range []string{"Swagger UI: " + base + "/docs/\n", "Swagger JSON: " + base + "/docs/swagger.json\n"} {
				if !strings.Contains(output, line) {
					t.Errorf("missing complete documentation URL %q: %s", line, output)
				}
			}
		})
		for route, contentType := range map[string]string{
			"/docs/": "text/html", "/docs/index.html": "text/html",
			"/docs/swagger-ui.css": "text/css", "/docs/swagger-ui-bundle.js": "application/javascript",
		} {
			status, headers, body := requestHTTPFixture(t, base, http.MethodGet, route, "", "")
			if status != http.StatusOK || len(body) == 0 || !strings.HasPrefix(headers.Get("Content-Type"), contentType) {
				t.Fatalf("embedded asset %s: %d %v", route, status, headers)
			}
		}
		_, _, body := requestHTTPFixture(t, base, http.MethodGet, "/docs/swagger.json", "", "")
		var spec map[string]any
		if err := json.Unmarshal(body, &spec); err != nil || spec["swagger"] != "2.0" || spec["code"] != nil {
			t.Fatalf("document was wrapped or invalid: %s, %v", body, err)
		}
		paths := spec["paths"].(map[string]any)
		schema := func(route, method string) map[string]any {
			return paths[route].(map[string]any)[method].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["schema"].(map[string]any)
		}
		if schema("/echo", "get")["properties"].(map[string]any)["result"] == nil || schema("/download", "get")["type"] != "string" {
			t.Fatalf("response/bypass schema not applied: %v", paths)
		}
		if schema("/remove", "delete")["properties"].(map[string]any)["code"] == nil {
			t.Fatal("no-response operation missing envelope")
		}
		_, _, actual := requestHTTPFixture(t, base, http.MethodGet, "/echo", "", "")
		if !bytes.Contains(actual, []byte(`"result":{"id":17}`)) {
			t.Fatalf("actual response differs from document: %s", actual)
		}
		status, _, body := requestHTTPFixture(t, base, http.MethodGet, "/uploads/image.txt", "", "")
		if status != http.StatusOK || string(body) != "static remains available" {
			t.Fatal("Swagger replaced the static file server")
		}
		status, _, body = requestHTTPFixture(t, base, http.MethodHead, "/docs/swagger.json", "", "")
		if status != http.StatusOK || len(body) != 0 {
			t.Fatal("HEAD document route is broken")
		}
	})
	t.Run("response disabled and Swagger disabled", func(t *testing.T) {
		base := startHTTPFixture(t, deployment, binary, "SwaggerConfig:\n  Enabled: true\nResponseConfig:\n  Enabled: false\n")
		_, _, document := requestHTTPFixture(t, base, http.MethodGet, "/swagger/swagger.json", "", "")
		if bytes.Contains(document, []byte(`"result"`)) {
			t.Fatal("raw responses were wrapped in the document")
		}
		base = startHTTPFixture(t, deployment, binary, "SwaggerConfig:\n  Enabled: false\n")
		status, _, _ := requestHTTPFixture(t, base, http.MethodGet, "/swagger/swagger.json", "", "")
		if status != http.StatusNotFound {
			t.Fatal("runtime disabling did not hide the compiled document")
		}
	})
}
