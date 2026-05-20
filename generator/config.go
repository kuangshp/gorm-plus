package generator

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config 代码生成器配置
type Config struct {
	// 数据库配置
	// DBType 数据库类型,支持:
	//   - "mysql"
	//   - "postgres"(或 "postgresql" / "pg")
	//   - "sqlite"(或 "sqlite3";此时 Database 字段直接作为 sqlite 文件路径)
	//   - "sqlserver"(或 "mssql")
	// 大小写不敏感。详见 DBType 枚举。
	DBType   string `yaml:"db_type"`  // 数据库类型,详见上方说明
	Host     string `yaml:"host"`     // 数据库地址(sqlite 忽略)
	Port     int    `yaml:"port"`     // 数据库端口(postgres 默认 5432,sqlserver 默认 1433,sqlite 忽略)
	Username string `yaml:"username"` // 数据库账号(sqlite 忽略)
	Password string `yaml:"password"` // 数据库密码(sqlite 忽略)
	Database string `yaml:"database"` // 数据库名(sqlite 时为文件路径,如 "./data.db")

	// 生成路径配置
	OutPath      string `yaml:"out_path"`       // dao输出路径，如 "./query/dao"
	ModelPkgPath string `yaml:"model_pkg_path"` // model包路径，如 "./query/model"
	RepoPath     string `yaml:"repo_path"`      // repository输出路径，如 "./query/repository"
	ApiPath      string `yaml:"api_path"`       // api desc路径，如 "./apps/admin/desc"
	VoPath       string `yaml:"vo_path"`        // vo输出路径，如 "./query/vo"
	DtoPath      string `yaml:"dto_path"`       // dto输出路径，如 "./apps/admin/dto"
	MapperPath   string `yaml:"mapper_path"`    // mapper输出路径，如 "./query/mapper"

	// 项目包名
	Package string `yaml:"package"` // 项目包名，如 "esim-api"
}

// LoadConfig 从YAML文件加载配置
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	return &cfg, nil
}
