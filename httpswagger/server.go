// Package httpswagger 提供内嵌 Swagger UI 和文档服务，不依赖 HTTP 框架或 CDN。
package httpswagger

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/kuangshp/gorm-plus/httpresponse"
)

// UI 固定使用 Swagger UI v5.33.2；来源、许可证和校验和见 assets/README.md。
//
//go:embed assets/swagger-ui.css assets/swagger-ui-bundle.js assets/LICENSE assets/swagger-ui-bundle.js.LICENSE.txt
var assets embed.FS

// Placeholder 为关闭文档时使用的占位文件，确保 go:embed 始终可编译。
// 开启文档时遇到该标记会明确报错，不会提供空文档或旧文档。
const Placeholder = `{"swagger":"2.0","info":{"title":"Swagger disabled","version":"0"},"paths":{},"x-gormplus-placeholder":true}` + "\n"

// ErrMissingDocument 表示尚未生成有效文档，需要先运行 gormplus swagger。
var ErrMissingDocument = errors.New("Swagger 文档未生成，请先运行 gormplus swagger --config <配置文件>，再重新构建程序")

// Config 控制文档访问；ApiFile 只供生成命令使用，运行时不读取 API 源文件。
type Config struct {
	Enabled bool   `json:",default=false" yaml:"enabled"`
	Path    string `json:",default=/swagger" yaml:"path"`
	ApiFile string `json:",optional" yaml:"apifile"` // 为空时，生成命令查找服务目录中唯一的 .api 文件。
}

// Server 提供标准 http.Handler；Routes 中的路径应独立注册到业务服务器。
// 它直接写出 HTML/JSON，不经过 httpx 的统一响应回调。
type Server struct {
	prefix   string
	document []byte
}

// Handler 在启用时验证并准备文档；关闭时返回 nil，不要求文档已生成。
// 响应格式按当前运行配置适配，同一二进制可切换统一响应和精确路径白名单。
func (c Config) Handler(document []byte, response httpresponse.Config) (*Server, error) {
	if !c.Enabled {
		return nil, nil
	}
	prefix, err := c.Prefix()
	if err != nil {
		return nil, err
	}
	prepared, err := PrepareDocument(document, response)
	if err != nil {
		return nil, err
	}
	return &Server{prefix: prefix, document: prepared}, nil
}

// Prefix 返回规范的文档前缀，禁止根路径、路由参数和通配符。
func (c Config) Prefix() (string, error) {
	raw := strings.TrimSpace(c.Path)
	if raw == "" {
		raw = "/swagger"
	}
	prefix := strings.TrimSuffix(raw, "/")
	if prefix == "" || prefix == "/" || !strings.HasPrefix(prefix, "/") || path.Clean(prefix) != prefix || strings.ContainsAny(prefix, "?#\\:*{}% \t\r\n") {
		return "", fmt.Errorf("SwaggerConfig.Path 必须是非根目录的普通 URL 路径: %q", c.Path)
	}
	return prefix, nil
}

// Routes 返回需要注册的精确路径；分别注册 GET 和 HEAD 即可。
// go-zero 会归一化末尾斜杠，所以只注册一次前缀，Handler 内再处理重定向。
func (s *Server) Routes() []string {
	return []string{s.prefix, s.prefix + "/index.html", s.prefix + "/swagger.json",
		s.prefix + "/swagger-ui.css", s.prefix + "/swagger-ui-bundle.js",
		s.prefix + "/LICENSE", s.prefix + "/swagger-ui-bundle.js.LICENSE.txt"}
}

// ServeHTTP 提供固定资源，不允许目录遍历或任意文件访问。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == s.prefix {
		http.Redirect(w, r, s.prefix+"/", http.StatusTemporaryRedirect)
		return
	}
	var data []byte
	var contentType string
	switch r.URL.Path {
	case s.prefix + "/", s.prefix + "/index.html":
		data, contentType = []byte(indexHTML), "text/html; charset=utf-8"
	case s.prefix + "/swagger.json":
		data, contentType = s.document, "application/json; charset=utf-8"
		w.Header().Set("Cache-Control", "no-store")
	case s.prefix + "/swagger-ui.css":
		data, _ = assets.ReadFile("assets/swagger-ui.css")
		contentType = "text/css; charset=utf-8"
	case s.prefix + "/swagger-ui-bundle.js":
		data, _ = assets.ReadFile("assets/swagger-ui-bundle.js")
		contentType = "application/javascript; charset=utf-8"
	case s.prefix + "/LICENSE", s.prefix + "/swagger-ui-bundle.js.LICENSE.txt":
		data, _ = assets.ReadFile("assets/" + path.Base(r.URL.Path))
		contentType = "text/plain; charset=utf-8"
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, path.Base(r.URL.Path), time.Time{}, bytes.NewReader(data))
}

const indexHTML = `<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Swagger API 文档</title><link rel="stylesheet" href="swagger-ui.css"></head>
<body><div id="swagger-ui"></div><script src="swagger-ui-bundle.js"></script>
<script>window.ui = SwaggerUIBundle({url:"swagger.json",dom_id:"#swagger-ui",deepLinking:true,
displayRequestDuration:true,docExpansion:"none",validatorUrl:null,presets:[SwaggerUIBundle.presets.apis]});</script>
</body></html>`
