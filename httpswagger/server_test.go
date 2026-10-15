package httpswagger

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kuangshp/gorm-plus/httpresponse"
)

const testDocument = `{"swagger":"2.0","host":"127.0.0.1","schemes":["https"],"info":{"title":"Test","version":"1"},"basePath":"/api","paths":{
 "/echo":{"post":{"responses":{"200":{"description":"ok","schema":{"$ref":"#/definitions/Output"}},"400":{"description":"bad request"}}}},
 "/health":{"get":{"responses":{"200":{"description":"raw"}}}},
 "/remove":{"delete":{"responses":{"200":{"description":"empty"},"204":{"description":"no content"}}}},
 "/parameters":{"parameters":[{"name":"id"}]}
},"definitions":{"Output":{"type":"object","properties":{"id":{"type":"integer","example":9007199254740993}}}}}`

func TestDocumentMatchesRuntimeResponseConfig(t *testing.T) {
	data, err := PrepareDocument([]byte(testDocument), httpresponse.Config{Enabled: true, BypassPaths: []string{" /api/health "}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "9007199254740993") {
		t.Fatal("large integer example lost precision")
	}
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if spec["host"] != nil || spec["schemes"] != nil {
		t.Fatal("Try it out must use the current browser origin")
	}
	paths := spec["paths"].(map[string]any)
	response := func(route, method, status string) map[string]any {
		return paths[route].(map[string]any)[method].(map[string]any)["responses"].(map[string]any)[status].(map[string]any)
	}
	props := response("/echo", "post", "200")["schema"].(map[string]any)["properties"].(map[string]any)
	if props["code"] == nil || props["message"] == nil || props["result"].(map[string]any)["$ref"] != "#/definitions/Output" {
		t.Fatalf("invalid response envelope: %v", props)
	}
	if response("/health", "get", "200")["schema"] != nil || response("/remove", "delete", "204")["schema"] != nil || response("/echo", "post", "400")["schema"] != nil {
		t.Fatal("bypass paths, no-content and non-success responses must retain their schema")
	}
	emptyProps := response("/remove", "delete", "200")["schema"].(map[string]any)["properties"].(map[string]any)
	if emptyProps["result"] != nil || emptyProps["code"] == nil {
		t.Fatalf("empty response should contain code/message only: %v", emptyProps)
	}
	raw, err := PrepareDocument([]byte(testDocument), httpresponse.Config{})
	if err != nil || strings.Contains(string(raw), `"result"`) {
		t.Fatalf("response wrapping disabled: %s, %v", raw, err)
	}
}

func TestServeEmbeddedUIAndDocument(t *testing.T) {
	server, err := (Config{Enabled: true, Path: "/api/docs/"}).Handler([]byte(testDocument), httpresponse.Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for route, contentType := range map[string]string{
		"/api/docs/": "text/html", "/api/docs/index.html": "text/html",
		"/api/docs/swagger.json": "application/json", "/api/docs/swagger-ui.css": "text/css",
		"/api/docs/swagger-ui-bundle.js": "application/javascript", "/api/docs/LICENSE": "text/plain",
		"/api/docs/swagger-ui-bundle.js.LICENSE.txt": "text/plain",
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := httptest.NewRecorder()
			server.ServeHTTP(w, httptest.NewRequest(method, route, nil))
			if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), contentType) {
				t.Fatalf("%s %s: %d %v", method, route, w.Code, w.Header())
			}
			if method == http.MethodHead && w.Body.Len() != 0 || method == http.MethodGet && w.Body.Len() == 0 {
				t.Fatalf("unexpected %s body length %d", method, w.Body.Len())
			}
			if route == "/api/docs/" && (strings.Contains(w.Body.String(), "https://") || method == http.MethodGet && !strings.Contains(w.Body.String(), "validatorUrl:null")) {
				t.Fatal("UI must use local assets and disable the external validator")
			}
		}
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/docs", nil))
	if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != "/api/docs/" {
		t.Fatal("missing canonical slash redirect")
	}
	for _, route := range []string{"/api/docs/../LICENSE", "/api/docs/private", "/other/swagger.json"} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest(http.MethodGet, route, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("unexpected resource exposed: %s", route)
		}
	}
	w = httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/docs/swagger.json", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal("POST accepted")
	}
}

func TestSwaggerConfigAndMissingDocument(t *testing.T) {
	if server, err := (Config{}).Handler(nil, httpresponse.Config{}); err != nil || server != nil {
		t.Fatal("disabled Swagger must not require a document")
	}
	if _, err := (Config{Enabled: true}).Handler([]byte(Placeholder), httpresponse.Config{}); !errors.Is(err, ErrMissingDocument) {
		t.Fatalf("placeholder accepted: %v", err)
	}
	for _, prefix := range []string{"/", "//", "/docs/../", "docs", "/docs/:id", "/docs?x", "/docs/{id}", "/docs%2fx"} {
		if _, err := (Config{Path: prefix}).Prefix(); err == nil {
			t.Errorf("invalid prefix accepted: %q", prefix)
		}
	}
	for _, document := range []string{"", "null", "[]", `{"openapi":"3.0.0","paths":{}}`, `{"swagger":"2.0","paths":[]}`} {
		if _, err := PrepareDocument([]byte(document), httpresponse.Config{}); err == nil {
			t.Errorf("invalid document accepted: %s", document)
		}
	}
}
