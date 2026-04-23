package plugin

import (
	"context"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type contextKey string

const CtxContextKey1 contextKey = "operator1"
const CtxContextKey2 contextKey = "operator2"
const CtxContextKey3 contextKey = "operator3"
const CtxContextKey4 contextKey = "operator4"

// FieldGetter 从 context 中获取字段值的函数
type FieldGetter func(ctx context.Context) any

// FieldConfig 单个字段的配置
// Name 填 Go 结构体字段名（如 "UpdatedBy"）或列名（如 "updated_by"）均可
// ColName 由插件自动从 schema 中解析，无需手动填写
type FieldConfig struct {
	Name     string
	Getter   FieldGetter
	OnCreate bool
	OnUpdate bool
}

// AutoFillConfig 插件配置
type AutoFillConfig struct {
	Fields []FieldConfig
}

// AutoFillPlugin 自动填充插件，支持自定义字段和取值函数
type AutoFillPlugin struct {
	cfg AutoFillConfig
}

func NewAutoFillPlugin(cfg AutoFillConfig) *AutoFillPlugin {
	return &AutoFillPlugin{cfg: cfg}
}

func (p *AutoFillPlugin) Name() string { return "AutoFillPlugin" }

func (p *AutoFillPlugin) Initialize(db *gorm.DB) error {
	if err := db.Callback().Create().Before("gorm:create").
		Register("auto_fill:before_create", p.beforeCreate); err != nil {
		return err
	}
	if err := db.Callback().Update().Before("gorm:update").
		Register("auto_fill:before_update", p.beforeUpdate); err != nil {
		return err
	}
	if err := db.Callback().Update().Before("gorm:update").
		Register("auto_fill:before_update_column", p.beforeUpdateColumn); err != nil {
		return err
	}
	return nil
}

// beforeCreate 处理 Create / CreateInBatches / Save（新增）
func (p *AutoFillPlugin) beforeCreate(tx *gorm.DB) {
	if !hasSchema(tx) {
		return
	}
	for _, f := range p.cfg.Fields {
		if !f.OnCreate {
			continue
		}
		schemaField := tx.Statement.Schema.LookUpField(f.Name)
		if schemaField == nil {
			continue
		}
		val := f.Getter(tx.Statement.Context)
		tx.Statement.SetColumn(schemaField.DBName, val, true)
	}
}

// beforeUpdate 处理 Update / Updates / UpdateSimple / Save（更新）
// SkipHooks=false 的路径
func (p *AutoFillPlugin) beforeUpdate(tx *gorm.DB) {
	if !hasSchema(tx) || tx.Statement.SkipHooks {
		return
	}
	for _, f := range p.cfg.Fields {
		if !f.OnUpdate {
			continue
		}
		schemaField := tx.Statement.Schema.LookUpField(f.Name)
		if schemaField == nil {
			continue
		}
		val := f.Getter(tx.Statement.Context)
		// 普通 Updates/Update 路径
		tx.Statement.SetColumn(schemaField.DBName, val, true)
		// UpdateSimple 路径：clause.Set 已存在时直接追加
		injectIntoClauseSet(tx, schemaField.DBName, val)
	}
}

// beforeUpdateColumn 处理 UpdateColumn / UpdateColumnSimple / UpdateColumns
// SkipHooks=true 的路径，SetColumn 不生效，直接操作 Dest map
func (p *AutoFillPlugin) beforeUpdateColumn(tx *gorm.DB) {
	if !hasSchema(tx) || !tx.Statement.SkipHooks {
		return
	}
	for _, f := range p.cfg.Fields {
		if !f.OnUpdate {
			continue
		}
		schemaField := tx.Statement.Schema.LookUpField(f.Name)
		if schemaField == nil {
			continue
		}
		val := f.Getter(tx.Statement.Context)
		switch dest := tx.Statement.Dest.(type) {
		case map[string]interface{}:
			dest[schemaField.DBName] = val
		default:
			// struct 场景退回 SetColumn
			tx.Statement.SetColumn(schemaField.DBName, val, true)
		}
	}
}

// injectIntoClauseSet 专门处理 UpdateSimple 的表达式更新路径
// UpdateSimple 直接构建 clause.Set，SetColumn 对它不生效
// 需要把字段追加到已有的 clause.Set 中
func injectIntoClauseSet(tx *gorm.DB, colName string, value any) {
	setClause, ok := tx.Statement.Clauses["SET"]
	if !ok {
		return
	}
	set, ok := setClause.Expression.(clause.Set)
	if !ok {
		return
	}
	// 避免重复注入
	for _, a := range set {
		if a.Column.Name == colName {
			return
		}
	}
	set = append(set, clause.Assignment{
		Column: clause.Column{Name: colName},
		Value:  value,
	})
	setClause.Expression = set
	tx.Statement.Clauses["SET"] = setClause
}

func hasSchema(tx *gorm.DB) bool {
	return tx.Statement != nil && tx.Statement.Schema != nil
}

// ── 内置 Getter 工厂函数 ──────────────────────────────────────────────────────

// CtxGetter 从 context 读取指定类型的值，T 为目标类型
func CtxGetter[T any](key any) FieldGetter {
	return func(ctx context.Context) any {
		if ctx == nil {
			var zero T
			return zero
		}
		v := ctx.Value(key)
		if v == nil {
			var zero T
			return zero
		}
		if val, ok := v.(T); ok {
			return val
		}
		var zero T
		return zero
	}
}
