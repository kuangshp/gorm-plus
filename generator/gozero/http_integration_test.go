package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGoctlHTTPIntegration 编译并启动真实生成的服务，验证响应封装和 CORS 配置。
func TestGoctlHTTPIntegration(t *testing.T) {
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
	spec := "syntax = \"v1\"\ntype Input {\n Name string `json:\"name\" validate:\"required\"`\n}\ntype Output {\n Message string `json:\"message\"`\n}\nservice sample-api {\n @handler Echo\n post /echo (Input) returns (Output)\n @handler Remove\n delete /remove\n @handler Health\n get /health\n}\n"
	mod := fmt.Sprintf("module httpfixture\n\ngo 1.26.10\n\nrequire (\n github.com/kuangshp/gorm-plus v0.0.0\n github.com/zeromicro/go-zero v1.10.2\n)\nreplace github.com/kuangshp/gorm-plus => %s\n", root)
	for name, content := range map[string]string{"sample.api": spec, "go.mod": mod} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	output, err := Run(context.Background(), RunOptions{}, "api", "go", "--api", filepath.Join(dir, "sample.api"), "--dir", dir, "--style", "goZero")
	if err != nil {
		t.Fatalf("goctl: %v\n%s", err, output)
	}
	generatedConfig, err := os.ReadFile(filepath.Join(dir, "etc/sample-api.yaml"))
	if err != nil || !bytes.Contains(generatedConfig, []byte("ResponseConfig:")) || !bytes.Contains(generatedConfig, []byte("CorsConfig:")) || !bytes.Contains(generatedConfig, []byte("StaticConfig:")) || !bytes.Contains(generatedConfig, []byte("LogRotation:")) {
		t.Fatalf("generated YAML is missing HTTP configuration: %s, %v", generatedConfig, err)
	}
	// 验证 logx、slog 和标准库 log 都遵循配置的存储格式。
	echoLogic := filepath.Join(dir, "internal/logic/echoLogic.go")
	logic, err := os.ReadFile(echoLogic)
	if err != nil {
		t.Fatal(err)
	}
	logic = bytes.Replace(logic, []byte("import ("), []byte("import (\n\"log\""), 1)
	logic = bytes.Replace(logic, []byte("// todo: add your logic here and delete this line"), []byte("l.Infof(\"fixture-logx\")\nlog.Print(\"fixture-standard-log\")"), 1)
	if err := os.WriteFile(echoLogic, logic, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "service")
	build := exec.Command("go", "build", "-mod=mod", "-o", binary, ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("generated service does not compile: %v\n%s", err, output)
	}
	resolvedModules, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil || bytes.Contains(resolvedModules, []byte("github.com/kuangshp/go-utils")) {
		t.Fatalf("generated service must not require go-utils: %s, %v", resolvedModules, err)
	}

	for _, settings := range []struct{ rotation, encoding string }{
		{"daily", "json"}, {"hourly", "json"}, {"daily", "plain"},
	} {
		t.Run("file logs "+settings.rotation+" "+settings.encoding, func(t *testing.T) {
			logs := t.TempDir()
			// 用相对于当前时间的时段名，验证启动归档，无需等待清理周期。
			compressName := filepath.Join(logs, "app-"+time.Now().Add(-5*24*time.Hour).Format("2006-01-02")+".log")
			deleteName := filepath.Join(logs, "app-"+time.Now().Add(-40*24*time.Hour).Format("2006-01-02")+".log.gz")
			for _, name := range []string{compressName, deleteName} {
				if err := os.WriteFile(name, []byte("archive"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			base := startHTTPFixture(t, dir, binary, fmt.Sprintf(`Log:
  Mode: console
  Level: info
  Encoding: %s
LogRotation:
  Enabled: true
  Path: %q
  Rotation: %s
  CompressAfterDays: 3
  KeepDays: 30
  CheckIntervalMinutes: 60
`, settings.encoding, logs, settings.rotation))
			requestHTTPFixture(t, base, http.MethodPost, "/echo", `{"name":"test"}`, "")
			requestHTTPFixture(t, base, http.MethodPost, "/echo", `{`, "")
			layout := "2006-01-02"
			if settings.rotation == "hourly" {
				layout = "2006-01-02-15"
			}
			path := filepath.Join(logs, "app-"+time.Now().Format(layout)+".log")
			contents, err := os.ReadFile(path)
			if err != nil || !bytes.Contains(contents, []byte("fixture-logx")) || !bytes.Contains(contents, []byte("HTTP request failed")) || !bytes.Contains(contents, []byte("fixture-standard-log")) {
				t.Fatalf("loggers did not reach configured file: %s, %v", contents, err)
			}
			for _, line := range bytes.Split(bytes.TrimSpace(contents), []byte("\n")) {
				if json.Valid(line) != (settings.encoding == "json") {
					t.Fatalf("line does not match %s encoding: %s", settings.encoding, line)
				}
			}
			// 启动归档在后台运行，HTTP 可用时压缩可能尚未结束。
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
				_, compressErr := os.Stat(compressName + ".gz")
				_, deleteErr := os.Stat(deleteName)
				if compressErr == nil && os.IsNotExist(deleteErr) {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if _, err := os.Stat(compressName + ".gz"); err != nil {
				t.Fatalf("startup compression did not run: %v", err)
			}
			if _, err := os.Stat(deleteName); !os.IsNotExist(err) {
				t.Fatalf("startup deletion did not run: %v", err)
			}
		})
	}

	t.Run("native file logs", func(t *testing.T) {
		logs := t.TempDir()
		base := startHTTPFixture(t, dir, binary, fmt.Sprintf(`Log:
  ServiceName: native-logs
  Mode: file
  Path: %q
  Level: info
  Encoding: json
  Stat: false
  MaxContentLength: 8
LogRotation:
  Enabled: false
`, logs))
		requestHTTPFixture(t, base, http.MethodPost, "/echo", `{"name":"test"}`, "")
		// 原生文件 Writer 异步写入；验证配置路径、格式和内容截断均保留。
		path := filepath.Join(logs, "access.log")
		var contents []byte
		var err error
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			contents, err = os.ReadFile(path)
			if err == nil && bytes.Contains(contents, []byte(`"content":"fixture-"`)) && bytes.Contains(contents, []byte(`"truncated":true`)) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("native logging configuration not applied: %s, %v", contents, err)
	})

	t.Run("enabled", func(t *testing.T) {
		base := startHTTPFixture(t, dir, binary, `ResponseConfig:
  Enabled: true
  BypassPaths: ["/health"]
CorsConfig:
  Enabled: true
  Origins: ["http://example.com"]
  AllowHeaders: ["X-Client-ID"]
  ExposeHeaders: ["Content-Disposition"]
`)
		for _, request := range []struct {
			method, path, body string
			code               int64
		}{
			{http.MethodPost, "/echo", `{"name":"test"}`, 0},
			{http.MethodDelete, "/remove", "", 0},
			{http.MethodPost, "/echo", `{"name":""}`, 1},
			{http.MethodPost, "/echo", `{`, 1},
		} {
			status, _, body := requestHTTPFixture(t, base, request.method, request.path, request.body, "http://example.com")
			var response struct {
				Code    *int64 `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(body, &response); err != nil || status != http.StatusOK || response.Code == nil || *response.Code != request.code || response.Message == "" {
				t.Fatalf("%s %s: status=%d, body=%s, err=%v", request.method, request.path, status, body, err)
			}
			if request.body == `{"name":""}` && !strings.Contains(response.Message, "必填") {
				t.Fatalf("validation error was not translated: %s", body)
			}
		}
		status, _, body := requestHTTPFixture(t, base, http.MethodGet, "/health", "", "http://example.com")
		if status != http.StatusOK || len(body) != 0 {
			t.Fatalf("bypassed health endpoint was wrapped: %d %s", status, body)
		}
		for _, method := range []string{http.MethodPost, http.MethodOptions} {
			status, headers, _ := requestHTTPFixture(t, base, method, "/echo", `{"name":"test"}`, "http://example.com")
			if headers.Get("Access-Control-Allow-Origin") != "http://example.com" ||
				!strings.Contains(headers.Get("Access-Control-Allow-Headers"), "Authorization") ||
				!strings.Contains(headers.Get("Access-Control-Allow-Headers"), "X-Client-ID") ||
				!strings.Contains(headers.Get("Access-Control-Expose-Headers"), "Content-Disposition") {
				t.Fatalf("%s did not receive expected CORS headers: %v", method, headers)
			}
			if method == http.MethodOptions && status != http.StatusNoContent {
				t.Fatalf("preflight status = %d", status)
			}
		}
		_, headers, _ := requestHTTPFixture(t, base, http.MethodPost, "/echo", `{"name":"test"}`, "http://other.example")
		if headers.Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("unconfigured origin received CORS authorization")
		}
	})

	t.Run("static uploads", func(t *testing.T) {
		uploads := filepath.Join(dir, "uploads")
		if err := os.Mkdir(uploads, 0700); err != nil {
			t.Fatal(err)
		}
		base := startHTTPFixture(t, dir, binary, `CorsConfig:
  Enabled: true
  Origins: ["http://example.com"]
StaticConfig:
  Enabled: true
  Prefix: /uploads
  Directory: ./uploads
`)
		status, _, _ := requestHTTPFixture(t, base, http.MethodGet, "/uploads/new.png", "", "http://example.com")
		if status != http.StatusNotFound {
			t.Fatalf("missing upload status = %d", status)
		}
		image := "\x89PNG\r\n\x1a\nnew-image"
		if err := os.WriteFile(filepath.Join(uploads, "new.png"), []byte(image), 0600); err != nil {
			t.Fatal(err)
		}
		status, headers, body := requestHTTPFixture(t, base, http.MethodGet, "/uploads/new.png", "", "http://example.com")
		if status != http.StatusOK || string(body) != image || headers.Get("Content-Type") != "image/png" || headers.Get("Access-Control-Allow-Origin") != "http://example.com" {
			t.Fatalf("new upload was not immediately served with CORS: %d %v %q", status, headers, body)
		}
		status, _, body = requestHTTPFixture(t, base, http.MethodHead, "/uploads/new.png", "", "http://example.com")
		if status != http.StatusOK || len(body) != 0 {
			t.Fatalf("static HEAD response: %d %q", status, body)
		}
		status, _, body = requestHTTPFixture(t, base, http.MethodDelete, "/remove", "", "http://example.com")
		if status != http.StatusOK || !bytes.Contains(body, []byte(`"code":0`)) {
			t.Fatalf("static file service interfered with business routes: %d %s", status, body)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		base := startHTTPFixture(t, dir, binary, "ResponseConfig:\n  Enabled: false\nCorsConfig:\n  Enabled: false\n")
		status, headers, body := requestHTTPFixture(t, base, http.MethodPost, "/echo", `{"name":"test"}`, "http://example.com")
		if status != http.StatusOK || string(body) != "null" || headers.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("disabled settings were ignored: status=%d, body=%s, headers=%v", status, body, headers)
		}
		status, _, body = requestHTTPFixture(t, base, http.MethodDelete, "/remove", "", "")
		if status != http.StatusOK || len(body) != 0 {
			t.Fatalf("disabled empty response changed: %d %s", status, body)
		}
		status, _, _ = requestHTTPFixture(t, base, http.MethodGet, "/uploads/new.png", "", "")
		if status != http.StatusNotFound {
			t.Fatalf("static files were exposed while disabled: %d", status)
		}
	})

	t.Run("defaults", func(t *testing.T) {
		base := startHTTPFixture(t, dir, binary, "")
		_, headers, body := requestHTTPFixture(t, base, http.MethodDelete, "/remove", "", "http://example.com")
		if !bytes.Contains(body, []byte(`"code":0`)) || headers.Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("default configuration was not applied: body=%s, headers=%v", body, headers)
		}
	})
}

func startHTTPFixture(t *testing.T, dir, binary, config string, checkOutput ...func(string)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "service.yaml")
	host := "127.0.0.1"
	if strings.HasPrefix(config, "Host:") {
		line, rest, _ := strings.Cut(config, "\n")
		host = strings.TrimSpace(strings.TrimPrefix(line, "Host:"))
		config = rest
	}
	contents := fmt.Sprintf("Name: sample-api\nMode: test\nHost: %s\nPort: %d\n", host, port)
	if !strings.HasPrefix(config, "Log:\n") {
		contents += "Log:\n  Level: error\n"
	}
	contents += config
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := exec.Command(binary, "-f", path)
	command.Dir = dir
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		// Wait 后再读取输出，避免与 os/exec 的复制 goroutine 并发访问 bytes.Buffer。
		for _, check := range checkOutput {
			check(output.String())
		}
		if t.Failed() {
			t.Log(output.String())
		}
	})
	address := fmt.Sprintf("127.0.0.1:%d", port)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return "http://" + address
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("generated service did not start")
	return ""
}

func requestHTTPFixture(t *testing.T, base, method, path, body, origin string) (int, http.Header, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	if method == http.MethodOptions {
		request.Header.Set("Access-Control-Request-Method", http.MethodPost)
		request.Header.Set("Access-Control-Request-Headers", "X-Client-ID")
	}
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, content
}
