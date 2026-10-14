package httpresponse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOkHandler(t *testing.T) {
	ctx := context.Background()
	response := OkHandler(ctx, map[string]int{"id": 17}).(*Response)
	if response.Code != 0 || response.Message != "请求成功" || response.Result.(map[string]int)["id"] != 17 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if OkHandler(ctx, response) != response {
		t.Fatal("already wrapped response was wrapped again")
	}
	encoded, err := json.Marshal(OkHandler(ctx, nil))
	if err != nil || string(encoded) != `{"code":0,"message":"请求成功"}` {
		t.Fatalf("empty success response: %s, %v", encoded, err)
	}
}

func TestErrHandler(t *testing.T) {
	handler := ErrHandler("test-api")
	status, value := handler(context.Background(), errors.New("操作失败"))
	response := value.(*Response)
	if status != http.StatusOK || response.Code != 1 || response.Message != "操作失败" {
		t.Fatalf("plain error: status=%d, body=%+v", status, response)
	}
	business := NewError(10001, "无操作权限")
	business.HTTPStatus = http.StatusForbidden
	status, value = handler(context.Background(), fmt.Errorf("wrapped: %w", business))
	response = value.(*Response)
	if status != http.StatusForbidden || response.Code != 10001 || response.Message != business.Message {
		t.Fatalf("wrapped business error: status=%d, body=%+v", status, response)
	}
	business.HTTPStatus = -1
	if status, _ := handler(context.Background(), business); status != http.StatusInternalServerError {
		t.Fatalf("invalid HTTP status was not handled: %d", status)
	}
}

func TestBypassMiddlewareMatchesExactPathAndPreservesContext(t *testing.T) {
	type contextKey struct{}
	middleware := BypassMiddleware(" /health ", "/download", "")
	for path, wantBypass := range map[string]bool{
		"/health": true, "/health?full=1": true, "/health/detail": false, "/health-check": false, "/users": false,
	} {
		t.Run(path, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r = r.WithContext(context.WithValue(r.Context(), contextKey{}, "kept"))
			called := false
			middleware(func(_ http.ResponseWriter, r *http.Request) {
				called = true
				if IsBypassed(r.Context()) != wantBypass || r.Context().Value(contextKey{}) != "kept" {
					t.Fatal("bypass decision or request context changed")
				}
				result := OkHandler(r.Context(), "ready")
				if wantBypass && result != "ready" {
					t.Fatal("success was wrapped for a bypassed path")
				}
				err := errors.New("not ready")
				status, body := ErrHandler("test-api")(r.Context(), err)
				if wantBypass && (status != http.StatusBadRequest || body != err) {
					t.Fatal("bypassed error did not preserve its original body")
				}
			})(httptest.NewRecorder(), r)
			if !called {
				t.Fatal("next handler was not called")
			}
		})
	}
}
