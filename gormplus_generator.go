package gormplus

import (
	"context"

	"github.com/kuangshp/gorm-plus/generator"
	"github.com/kuangshp/gorm-plus/generator/gozero"
)

// ================== 代码生成器 ==================

// GeneratorConfig 代码生成器配置，通过 YAML 文件加载或直接构造。
type GeneratorConfig = generator.Config
type GeneratorSensitiveField = generator.SensitiveFieldConfig

// LoadGeneratorConfig 从 YAML 文件加载代码生成器配置。
//
// 使用示例：
//
//	cfg, err := gormplus.LoadGeneratorConfig("./generator.yaml")
//	if err != nil { log.Fatal(err) }
func LoadGeneratorConfig(path string) (*generator.Config, error) {
	return generator.LoadConfig(path)
}

// Generate 执行代码生成，根据数据库表结构生成 Model / Repository / API 文件。
// 运行时会提示输入表名，直接回车则生成所有表的 Model（其他文件不生成）。
//
// 使用示例：
//
//	cfg, _ := gormplus.LoadGeneratorConfig("./generator.yaml")
//	if err := gormplus.Generate(cfg); err != nil {
//	    log.Fatal(err)
//	}
func Generate(cfg *generator.Config) error {
	return generator.Generate(cfg)
}

// GoctlRunOptions 配置 goctl 可执行文件和可选的外部模板目录。
type GoctlRunOptions = gozero.RunOptions

// RunGoctl 使用 gorm-plus 内嵌模板执行 goctl，工具库本身不依赖 go-zero。
func RunGoctl(ctx context.Context, options GoctlRunOptions, arguments ...string) ([]byte, error) {
	return gozero.Run(ctx, options, arguments...)
}

// WriteGoctlTemplateHome 将内嵌模板导出到指定的 goctl --home 目录。
func WriteGoctlTemplateHome(destination string) error {
	return gozero.WriteTemplateHome(destination)
}
