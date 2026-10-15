package swagger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

// Open 等待文档接口就绪，再调用本机浏览器；仅供显式执行的本地开发命令使用。
// 不重新生成文档、不启动服务；调用方必须设置超时或取消条件。
func Open(ctx context.Context, options Options) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("Swagger 打开上下文不能为空")
	}
	dir := options.Dir
	if dir == "" {
		dir = "."
	}
	cfg, err := loadConfig(dir, options.ConfigFile)
	if err != nil {
		return nil, err
	}
	if !cfg.SwaggerConfig.Enabled {
		return []byte("Swagger 已关闭，不打开浏览器。\n"), nil
	}
	prefix, err := cfg.SwaggerConfig.Prefix()
	if err != nil {
		return nil, err
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("打开 Swagger 需要配置有效的服务 Port")
	}
	host := cfg.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	address := "http://" + net.JoinHostPort(host, strconv.Itoa(cfg.Port)) + prefix + "/"
	if err := openWhenReady(ctx, address, openBrowser); err != nil {
		return nil, err
	}
	return []byte("Swagger UI: " + address + "\n"), nil
}

func openWhenReady(ctx context.Context, address string, opener func(context.Context, string) error) error {
	client := &http.Client{Timeout: time.Second}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"swagger.json", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			var document struct {
				Swagger string `json:"swagger"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&document)
			response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil && document.Swagger == "2.0" {
				return opener(ctx, address)
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待 Swagger 文档就绪失败 (%s): %w", address, ctx.Err())
		case <-ticker.C:
		}
	}
}

func openBrowser(ctx context.Context, address string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "open", address)
	case "windows":
		command = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", address)
	default:
		command = exec.CommandContext(ctx, "xdg-open", address)
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("打开浏览器失败，请手动访问 %s: %w", address, err)
	}
	return nil
}
