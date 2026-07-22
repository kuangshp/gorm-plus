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
	OutPath         string `yaml:"out_path"`          // dao输出路径，如 "./query/dao"
	ModelPkgPath    string `yaml:"model_pkg_path"`    // model包路径，如 "./query/model"
	RepoPath        string `yaml:"repo_path"`         // repository输出路径，如 "./query/repository"
	ApiPath         string `yaml:"api_path"`          // api desc路径，如 "./apps/admin/desc"
	ProtoPath       string `yaml:"proto_path"`        // proto文件输出路径，如 "./apps/rpc"
	VoPath          string `yaml:"vo_path"`           // vo输出路径，如 "./query/vo"
	DtoPath         string `yaml:"dto_path"`          // dto输出路径，如 "./apps/admin/dto"
	APIMapperPath   string `yaml:"api_mapper_path"`   // API types/DTO/VO mapper输出路径，如 "./query/mapper/api"
	ProtoMapperPath string `yaml:"proto_mapper_path"` // Entity/Proto mapper输出路径，如 "./query/mapper/proto"

	// 项目包名
	Package string `yaml:"package"` // 项目包名，如 "esim-api"

	// ExcludeTables 不执行代码生成的表名列表（不区分大小写）。
	ExcludeTables []string `yaml:"exclude_tables"`

	// SensitiveFields 为指定表生成敏感业务字段及 gormplus tag。
	// 数据库需存在对应的密文列和索引列，例如 phone_cipher、phone_index。
	SensitiveFields []SensitiveFieldConfig `yaml:"sensitive_fields"`
}

// SensitiveFieldConfig 配置代码生成器中的敏感字段。
type SensitiveFieldConfig struct {
	Table       string `yaml:"table"`        // 数据库表名
	Field       string `yaml:"field"`        // 业务字段名或列名，例如 phone
	Type        string `yaml:"type"`         // 敏感类型，例如 phone；为空默认 phone
	CipherField string `yaml:"cipher_field"` // 密文列；为空默认 {field}_cipher
	IndexField  string `yaml:"index_field"`  // 索引列；为空默认 {field}_index
	// EncryptAtRest 默认 false 保存明文；true 保存 AES-GCM 密文。
	EncryptAtRest bool `yaml:"encrypt_at_rest"`
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
