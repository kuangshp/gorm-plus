// Package httpcors 提供通用 CORS 配置和头部追加方法，不依赖具体 HTTP 框架。
package httpcors

import (
	"net/http"
	"strings"
)

// Config 在生成的 go-zero 服务中配合 rest.WithCustomCors 使用。
// 来源匹配由 go-zero 处理；Origins 为空时沿用其允许所有来源的默认行为。
type Config struct {
	Enabled      bool     `json:",default=true"` // 是否启用 CORS；通过 go-zero 加载配置时默认 true。
	Origins      []string `json:",optional"`     // 允许的来源，例如 https://admin.example.com 或 *。
	AllowHeaders []string `json:",optional"`     // 在框架默认允许的请求头上追加，例如 X-Client-ID。
	// ExposeHeaders 允许跨域前端 JavaScript 读取指定的额外响应头。
	// 导出 Excel 时可填写 Content-Disposition，再通过 response.headers.get("Content-Disposition") 读取文件名头。
	// 只控制头部是否可读取，下载文件名仍需由后端设置；空列表表示不额外暴露响应头。
	ExposeHeaders []string `json:",optional"`
}

// AddHeaders 为普通请求和 OPTIONS 预检追加头部，保留框架原有头部。
// 作为 rest.WithCustomCors 的 middlewareFn 参数传入。
func (c Config) AddHeaders(header http.Header) {
	mergeHeader(header, "Access-Control-Allow-Headers", c.AllowHeaders)
	mergeHeader(header, "Access-Control-Expose-Headers", c.ExposeHeaders)
}

// mergeHeader 合并已有和新增的头部值，去除空值，并忽略大小写去重。
func mergeHeader(header http.Header, name string, extra []string) {
	if len(extra) == 0 {
		return
	}
	seen := make(map[string]bool)
	var values []string
	for _, line := range append(header.Values(name), extra...) {
		for _, value := range strings.Split(line, ",") {
			value = strings.TrimSpace(value)
			key := strings.ToLower(value)
			if value != "" && !seen[key] {
				seen[key] = true
				values = append(values, value)
			}
		}
	}
	if len(values) > 0 {
		header.Set(name, strings.Join(values, ", "))
	}
}
