package gormplus

import (
	"github.com/kuangshp/gorm-plus/generator"
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
