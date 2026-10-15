// Package swagger 按业务 YAML 配置调用 goctl 生成可供服务内嵌的 Swagger 文档。
package swagger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kuangshp/gorm-plus/httpresponse"
	"github.com/kuangshp/gorm-plus/httpswagger"
	"gopkg.in/yaml.v3"
)

// Options 中的相对配置路径和 ApiFile 均以 Dir 服务目录为基准，Dir 默认当前目录。
type Options struct {
	ConfigFile string
	Dir        string
	Executable string // 可选 goctl 路径，默认从 PATH 查找。
}

type serviceConfig struct {
	Host           string             `yaml:"host"`
	Port           int                `yaml:"port"`
	SwaggerConfig  httpswagger.Config `yaml:"swaggerconfig"`
	ResponseConfig struct {
		Enabled     *bool    `yaml:"enabled"`
		BypassPaths []string `yaml:"bypasspaths"`
	} `yaml:"responseconfig"`
}

// Generate 在开启时重新生成原始文档；关闭时写入占位，防止旧文档被意外打包。
// 文档字段的 code/message/result 适配在运行时进行，支持部署后切换 ResponseConfig。
// 开启后的任何生成错误都会使旧文档失效；调用方应据此停止构建或启动。
func Generate(ctx context.Context, options Options) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("Swagger 生成上下文不能为空")
	}
	dir := options.Dir
	if dir == "" {
		dir = "."
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	cfg, err := loadConfig(dir, options.ConfigFile)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(dir, "swagger", "swagger.json")
	if err := writeDocument(target, []byte(httpswagger.Placeholder)); err != nil {
		return nil, err
	}
	if !cfg.SwaggerConfig.Enabled {
		return []byte("Swagger 已关闭，已准备编译占位文件。\n"), nil
	}
	if _, err := cfg.SwaggerConfig.Prefix(); err != nil {
		return nil, err
	}
	apiPath := cfg.SwaggerConfig.ApiFile
	if apiPath == "" {
		matches, err := filepath.Glob(filepath.Join(dir, "*.api"))
		if err != nil {
			return nil, err
		}
		if len(matches) != 1 {
			return nil, fmt.Errorf("服务目录有 %d 个 .api 文件，请配置 SwaggerConfig.ApiFile", len(matches))
		}
		apiPath = matches[0]
	}
	apiPath = resolve(dir, apiPath)
	if _, err := os.Stat(apiPath); err != nil {
		return nil, fmt.Errorf("读取 Swagger API 定义: %w", err)
	}
	executable := options.Executable
	if executable == "" {
		executable, err = exec.LookPath("goctl")
		if err != nil {
			return nil, fmt.Errorf("生成 Swagger 需要先安装 goctl: %w", err)
		}
	}
	temporary, err := os.MkdirTemp("", "gormplus-swagger-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	command := exec.CommandContext(ctx, executable, "api", "swagger", "--api", apiPath, "--dir", temporary, "--filename", "swagger")
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("goctl Swagger 生成失败: %w", err)
	}
	document, err := os.ReadFile(filepath.Join(temporary, "swagger.json"))
	if err != nil {
		return output, fmt.Errorf("读取 goctl Swagger 输出: %w", err)
	}
	response := httpresponse.Config{Enabled: true, BypassPaths: cfg.ResponseConfig.BypassPaths}
	if cfg.ResponseConfig.Enabled != nil {
		response.Enabled = *cfg.ResponseConfig.Enabled
	}
	// 生成阶段先验证文档可适配，再保存原始文档，供运行时依据实际配置处理。
	if _, err := httpswagger.PrepareDocument(document, response); err != nil {
		return output, err
	}
	if err := writeDocument(target, document); err != nil {
		return output, err
	}
	return append(output, []byte("Swagger 已生成："+target+"\n")...), nil
}

func loadConfig(dir, configFile string) (serviceConfig, error) {
	var cfg serviceConfig
	if strings.TrimSpace(configFile) == "" {
		return cfg, errors.New("请通过 --config 指定服务 YAML 配置")
	}
	data, err := os.ReadFile(resolve(dir, configFile))
	if err != nil {
		return cfg, fmt.Errorf("读取 Swagger 配置: %w", err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return cfg, fmt.Errorf("解析 Swagger 配置: %w", err)
	}
	// 与 go-zero 配置键名不区分大小写的行为保持一致；不输出其他业务配置。
	normalizeKeys(&node)
	if err := node.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("读取 Swagger 配置字段: %w", err)
	}
	return cfg, nil
}

func resolve(dir, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(dir, name)
}

func normalizeKeys(node *yaml.Node) {
	if node.Kind == yaml.MappingNode {
		for index := 0; index < len(node.Content); index += 2 {
			node.Content[index].Value = strings.ToLower(node.Content[index].Value)
		}
	}
	for _, child := range node.Content {
		normalizeKeys(child)
	}
}

// writeDocument 通过同目录临时文件重命名，避免构建读到只写入一半的 JSON。
func writeDocument(target string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(target), ".swagger-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Chmod(0644); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), target)
}
