package decimaljson

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrepareNativeJSONValues(t *testing.T) { // 原始 JSON 只提取带标记的字段，嵌入请求仍保留普通字段。
	type create struct {
		Attributes any    `json:"attributes,optional" jsonvalue:"true"` // 交给业务层的原始 JSON。
		Name       string `json:"name"`                                 // 保持原解析器处理的字符串。
	}
	type modify struct {
		create       // 模拟生成的修改请求。
		ID     int64 `json:"id"` // 普通数值字段继续自动兼容字符串。
	}
	for _, raw := range []string{`{"id":9007199254740993,"price":12.34567890123456789}`, `[{},true,null]`, `{}`, `[]`, `null`} {
		t.Run(raw, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(`{"id":"17","name":"sample","attributes":`+raw+`}`))
			r.Header.Set("Content-Type", "application/json")
			var req modify
			if err := PrepareRequest(r, &req); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(req.Attributes)
			if err != nil || string(data) != raw {
				t.Fatalf("raw=%s error=%v", data, err)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			var remaining map[string]json.RawMessage
			if err := json.Unmarshal(body, &remaining); err != nil {
				t.Fatal(err)
			}
			if _, exists := remaining["attributes"]; exists {
				t.Fatalf("raw value sent to incompatible parser: %s", body)
			}
			if string(remaining["id"]) != "17" || string(remaining["name"]) != `"sample"` {
				t.Fatalf("other fields changed: %s", body)
			}
		})
	}
	var req create
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"sample"}`))
	r.Header.Set("Content-Type", "application/json")
	if err := PrepareRequest(r, &req); err != nil || req.Attributes != nil {
		t.Fatalf("missing field: %+v %v", req, err)
	}
}

func TestPrepareNativeJSONInvalidType(t *testing.T) { // 错误类型的 jsonvalue 标记必须返回错误，不隐式丢弃输入。
	var req struct {
		Attributes string `json:"attributes,optional" jsonvalue:"true"` // 故意错误的配置。
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"attributes":{}}`))
	r.Header.Set("Content-Type", "application/json")
	if err := PrepareRequest(r, &req); err == nil {
		t.Fatal("invalid jsonvalue target accepted")
	}
}
