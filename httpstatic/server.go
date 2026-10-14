// Package httpstatic 将服务器上的公开文件目录映射到 URL 路径，不依赖具体 HTTP 框架。
package httpstatic

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
)

// Config 配置本机静态文件访问；上传和文件命名仍由业务项目实现。
type Config struct {
	Enabled   bool   `json:",default=false"`     // 默认关闭；启用后公开指定目录中的普通文件。
	Prefix    string `json:",default=/uploads"`  // URL 路径前缀，例如 /uploads，不能为根路径 /。
	Directory string `json:",default=./uploads"` // 已存在的本机目录；相对路径以进程工作目录为基准。
}

// Server 实现 http.Handler，可通过 go-zero 的 rest.WithNotFoundHandler 挂载。
// 文件直接输出，不经过统一 JSON 响应包装；不缓存文件是否存在，上传后可立即读取。
type Server struct {
	root   *os.Root
	prefix string
}

// Open 校验配置并打开目录。调用方启用该功能后调用，并在服务退出时 Close。
// 根目录句柄限制访问范围，目录外的符号链接目标不会被读取。
func (c Config) Open() (*Server, error) {
	prefix := strings.TrimSuffix(c.Prefix, "/")
	if !strings.HasPrefix(prefix, "/") || prefix == "" || prefix == "/" || path.Clean(prefix) != prefix ||
		strings.ContainsAny(prefix, "?#\\") {
		return nil, fmt.Errorf("StaticConfig.Prefix 必须是非根路径的 URL 前缀，例如 /uploads")
	}
	if strings.TrimSpace(c.Directory) == "" {
		return nil, fmt.Errorf("StaticConfig.Directory 不能为空")
	}
	root, err := os.OpenRoot(c.Directory)
	if err != nil {
		return nil, fmt.Errorf("打开静态文件目录失败: %w", err)
	}
	return &Server{
		root:   root,
		prefix: prefix,
	}, nil
}

// Close 关闭目录句柄，应在 HTTP 服务停止处理请求后调用。
func (s *Server) Close() error {
	return s.root.Close()
}

// ServeHTTP 只允许 GET / HEAD 读取指定前缀内的文件，其他请求返回 404。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !strings.HasPrefix(r.URL.Path, s.prefix+"/") {
		http.NotFound(w, r)
		return
	}
	file, err := (filesOnly{root: s.root}).Open(strings.TrimPrefix(r.URL.Path, s.prefix))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// 标准库处理 MIME、HEAD、Range 和条件请求，不对文件存在性做缓存。
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

// filesOnly 禁用目录浏览及隐藏文件，仅提供目录范围内的普通文件。
type filesOnly struct {
	root *os.Root
}

func (f filesOnly) Open(name string) (http.File, error) {
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	if name == "" {
		return nil, os.ErrNotExist
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return nil, os.ErrNotExist
		}
	}
	file, err := f.root.Open(name)
	if err != nil {
		return nil, err
	}
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		_ = file.Close()
		return nil, os.ErrNotExist
	}
	return file, nil
}
