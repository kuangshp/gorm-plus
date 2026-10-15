package httpswagger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/kuangshp/gorm-plus/httpresponse"
)

// PrepareDocument 将 goctl 的 Swagger 2.0 文档适配到实际运行配置。
// 使用 json.Number 保留示例中的大整数；不修改调用方传入的字节。
func PrepareDocument(document []byte, response httpresponse.Config) ([]byte, error) {
	if !json.Valid(document) {
		return nil, fmt.Errorf("Swagger 文档必须是有效 JSON")
	}
	var spec map[string]any
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	if err := decoder.Decode(&spec); err != nil {
		return nil, fmt.Errorf("读取 Swagger 文档: %w", err)
	}
	if spec["x-gormplus-placeholder"] == true {
		return nil, ErrMissingDocument
	}
	paths, ok := spec["paths"].(map[string]any)
	if spec["swagger"] != "2.0" || !ok {
		return nil, fmt.Errorf("需要 goctl api swagger 生成的 Swagger 2.0 文档")
	}
	// 移除生成文档中的固定 host/scheme，使用浏览器当前来源，
	// 避免页面可打开但 Try it out 请求错误的域名或端口。
	delete(spec, "host")
	delete(spec, "schemes")
	if response.Enabled {
		bypass := make(map[string]bool, len(response.BypassPaths))
		for _, route := range response.BypassPaths {
			bypass[strings.TrimSpace(route)] = true
		}
		basePath, _ := spec["basePath"].(string)
		for route, value := range paths {
			if bypass[path.Join("/", basePath, route)] {
				continue
			}
			operations, _ := value.(map[string]any)
			for method, value := range operations {
				switch method {
				case "get", "post", "put", "patch", "delete", "head", "options":
				default:
					continue
				}
				operation, _ := value.(map[string]any)
				responses, _ := operation["responses"].(map[string]any)
				for status, value := range responses {
					code, err := strconv.Atoi(status)
					if err != nil || code < 200 || code >= 300 || code == 204 || code == 205 || method == "head" {
						continue
					}
					item, ok := value.(map[string]any)
					if !ok || item["$ref"] != nil {
						continue
					}
					properties := map[string]any{
						"code":    map[string]any{"type": "integer", "format": "int64", "example": 0, "description": "业务码，0 表示成功"},
						"message": map[string]any{"type": "string", "example": "请求成功", "description": "业务提示"},
					}
					if schema := item["schema"]; schema != nil {
						properties["result"] = schema
					}
					item["schema"] = map[string]any{"type": "object", "required": []string{"code", "message"}, "properties": properties}
				}
			}
		}
	}
	return json.MarshalIndent(spec, "", "  ")
}
