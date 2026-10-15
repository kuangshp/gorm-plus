package swagger

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOpenWaitsForActualDocumentAndOnlyOpensOnce(t *testing.T) {
	var requests, opened atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/docs/swagger.json" {
			t.Error("wrong readiness URL", r.URL.Path)
		}
		if requests.Add(1) == 1 {
			// 就算返回 200，统一错误响应也不能当成文档就绪。
			w.Write([]byte(`{"code":1,"message":"not ready"}`))
			return
		}
		w.Write([]byte(`{"swagger":"2.0"}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := openWhenReady(ctx, server.URL+"/docs/", func(_ context.Context, url string) error {
		opened.Add(1)
		if url != server.URL+"/docs/" {
			t.Error("wrong browser URL", url)
		}
		return nil
	})
	if err != nil || requests.Load() < 2 || opened.Load() != 1 {
		t.Fatalf("readiness not respected: %v, requests=%d, opened=%d", err, requests.Load(), opened.Load())
	}
}

func TestOpenTimeoutAndDisabledConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := openWhenReady(ctx, "http://127.0.0.1:1/docs/", func(context.Context, string) error {
		t.Fatal("browser opened before readiness")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	dir := t.TempDir()
	writeFixture(t, dir, "app.yaml", "SwaggerConfig:\n  Enabled: false\n")
	if _, err := Open(context.Background(), Options{Dir: dir, ConfigFile: "app.yaml"}); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dir, "app.yaml", "SwaggerConfig:\n  Enabled: true\n")
	if _, err := Open(context.Background(), Options{Dir: dir, ConfigFile: "app.yaml"}); err == nil {
		t.Fatal("missing port accepted")
	}
}
