package httpcors

import (
	"net/http"
	"testing"
)

func TestAddHeadersPreservesDefaultsAndDeduplicates(t *testing.T) {
	header := make(http.Header)
	header.Add("Access-Control-Allow-Headers", "Content-Type, Authorization")
	header.Add("Access-Control-Expose-Headers", "Content-Length")
	config := Config{
		AllowHeaders:  []string{"authorization", "X-Client-ID, X-Trace-ID", "", " X-Client-ID "},
		ExposeHeaders: []string{"Content-Disposition", "content-length"},
	}
	config.AddHeaders(header)
	config.AddHeaders(header) // 普通响应与回退处理可能重复追加头部，验证不会重复写入。
	if got := header.Get("Access-Control-Allow-Headers"); got != "Content-Type, Authorization, X-Client-ID, X-Trace-ID" {
		t.Fatalf("allowed headers = %q", got)
	}
	if got := header.Get("Access-Control-Expose-Headers"); got != "Content-Length, Content-Disposition" {
		t.Fatalf("exposed headers = %q", got)
	}
	if header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("header helper must leave origin selection to the HTTP framework")
	}
}

func TestEmptyConfigLeavesHeadersUnchanged(t *testing.T) {
	header := make(http.Header)
	header.Add("Vary", "Origin")
	(Config{}).AddHeaders(header)
	if len(header) != 1 || header.Get("Vary") != "Origin" {
		t.Fatalf("empty configuration changed headers: %v", header)
	}
}
