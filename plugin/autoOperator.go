package plugin

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ================== context Key ==================

type contextKey string

const (
	// CtxOperatorKey 操作人标识在 context 中的默认 key。
	// 中间件写入时使用此 key，OperatorGetter 读取时也使用此 key。
	//
	// 写入示例：
	//   // int64 类型
	//   ctx = context.WithValue(ctx, plugin.CtxOperatorKey, int64(1001))
	//   // string 类型（UUID 场景）
	//   ctx = context.WithValue(ctx, plugin.CtxOperatorKey, "550e8400-uuid-abc")
	CtxOperatorKey1  contextKey = "operator1"
	CtxOperatorKey2  contextKey = "operator2"
	CtxOperatorKey3  contextKey = "operator3"
	CtxOperatorKey4  contextKey = "operator4"
	CtxOperatorKey5  contextKey = "operator5"
	CtxOperatorKey6  contextKey = "operator6"
	CtxOperatorKey7  contextKey = "operator7"
	CtxOperatorKey8  contextKey = "operator8"
	CtxOperatorKey9  contextKey = "operator9"
	CtxOperatorKey10 contextKey = "operator10"
)

// ================== FieldGetter ==================

// FieldGetter 从 context 中获取字段值的函数类型。
// 返回 any，支持 int64 / string / uuid 等任意类型，
// gorm 会根据 struct 字段的实际类型自动处理。
type FieldGetter func(ctx context.Context) any

// CtxGetter 内置 Getter 工厂：从 context 中读取指定 key 的值，T 为期望类型。
// 类型不匹配时返回 T 的零值。
//
// 使用示例：
//
//	// 读取 int64 类型的操作人 ID
//	plugin.CtxGetter[int64](plugin.CtxOperatorKey)
//
//	// 读取 string 类型的操作人 ID（UUID 场景）
//	plugin.CtxGetter[string](plugin.CtxOperatorKey)
//
//	// 读取自定义 key 的值
//	plugin.CtxGetter[string]("myCustomKey")
func CtxGetter[T any](key any) FieldGetter {
	return func(ctx context.Context) any {
		if ctx == nil {
			var zero T
			return zero
		}
		// 先通过解析器转换 ctx，兼容 *gin.Context 等框架特定类型
		ctx = resolveCtx(ctx)
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

// OperatorGetter 专用操作人 Getter 工厂，从 CtxOperatorKey 读取操作人标识。
// T 为操作人 ID 的类型，支持 int64 / string / uuid.UUID 等。
//
// 与 CtxGetter[T](CtxOperatorKey) 等价，语义更清晰。
//
// 使用示例：
//
//	// int64 类型操作人
//	plugin.OperatorGetter[int64]()
//
//	// string 类型操作人（UUID 场景）
//	plugin.OperatorGetter[string]()

// ================== FieldConfig ==================

// FieldConfig 单个字段的自动填充配置。
//
// Name 填 Go 结构体字段名（如 "UpdatedBy"）或列名（如 "updated_by"）均可，
// 插件自动通过 gorm schema 解析出实际列名。
//
// 示例：
//
//	plugin.FieldConfig{
//	    Name:     "CreatedBy",
//	    Getter:   plugin.OperatorGetter[int64](),
//	    OnCreate: true,
//	    OnUpdate: false,
//	}
type FieldConfig struct {
	// Name Go 结构体字段名或数据库列名，插件自动解析，两者均可
	Name string

	// Getter 从 context 中获取该字段值的函数
	// 使用内置工厂：plugin.OperatorGetter[int64]() 或 plugin.CtxGetter[string]("myKey")
	// 也可自定义：func(ctx context.Context) any { return ctx.Value("myKey") }
	Getter FieldGetter

	// OnCreate 是否在 Create 时填充此字段
	OnCreate bool

	// OnUpdate 是否在 Update 时填充此字段
	OnUpdate bool
}

// ================== AutoFillConfig ==================

// AutoFillConfig 自动填充插件配置。
//
// 完整示例：
//
//	plugin.AutoFillConfig{
//	    Fields: []plugin.FieldConfig{
//	        // CreatedBy：仅 Create 时填充，int64 类型操作人
//	        {Name: "CreatedBy", Getter: plugin.OperatorGetter[int64](), OnCreate: true},
//	        // UpdatedBy：Create 和 Update 都填充，int64 类型操作人
//	        {Name: "UpdatedBy", Getter: plugin.OperatorGetter[int64](), OnCreate: true, OnUpdate: true},
//	        // TenantID：Create 时填充，string 类型租户
//	        {Name: "TenantID", Getter: plugin.CtxGetter[string]("tenantId"), OnCreate: true},
//	    },
//	}
type AutoFillConfig struct {
	Fields []FieldConfig
}

// ================== AutoFillPlugin ==================

// AutoFillPlugin gorm 自动填充插件，支持任意字段和自定义 Getter。
//
// 注册示例：
//
//	db.Use(plugin.NewAutoFillPlugin(plugin.AutoFillConfig{
//	    Fields: []plugin.FieldConfig{
//	        {Name: "CreatedBy", Getter: plugin.OperatorGetter[int64](), OnCreate: true},
//	        {Name: "UpdatedBy", Getter: plugin.OperatorGetter[int64](), OnCreate: true, OnUpdate: true},
//	    },
//	}))
//
// UUID 操作人示例：
//
//	db.Use(plugin.NewAutoFillPlugin(plugin.AutoFillConfig{
//	    Fields: []plugin.FieldConfig{
//	        {Name: "CreatedBy", Getter: plugin.OperatorGetter[string](), OnCreate: true},
//	        {Name: "UpdatedBy", Getter: plugin.OperatorGetter[string](), OnCreate: true, OnUpdate: true},
//	    },
//	}))
//
// 多字段混合示例（操作人 + 租户 + 自定义字段）：
//
//	db.Use(plugin.NewAutoFillPlugin(plugin.AutoFillConfig{
//	    Fields: []plugin.FieldConfig{
//	        {Name: "CreatedBy", Getter: plugin.OperatorGetter[int64](), OnCreate: true},
//	        {Name: "UpdatedBy", Getter: plugin.OperatorGetter[int64](), OnCreate: true, OnUpdate: true},
//	        {Name: "TenantID",  Getter: plugin.CtxGetter[string]("tenantId"), OnCreate: true},
//	        {Name: "Source",    Getter: func(ctx context.Context) any {
//	            // 自定义逻辑：从 ctx 读取请求来源
//	            if src, ok := resolveCtx(ctx).Value("source").(string); ok {
//	                return src
//	            }
//	            return "unknown"
//	        }, OnCreate: true},
//	    },
//	}))
type AutoFillPlugin struct {
	cfg AutoFillConfig
}

// NewAutoFillPlugin 创建自动填充插件实例。
func NewAutoFillPlugin(cfg AutoFillConfig) *AutoFillPlugin {
	return &AutoFillPlugin{cfg: cfg}
}

// Name 返回插件唯一名称。
func (p *AutoFillPlugin) Name() string { return "gorm:auto_fill" }

// Initialize 注册 Create / Update 两类操作的钩子。
func (p *AutoFillPlugin) Initialize(db *gorm.DB) error {
	// Create 前：填充 OnCreate=true 的字段
	if err := db.Callback().Create().Before("gorm:create").
		Register("auto_fill:before_create", p.beforeCreate); err != nil {
		return err
	}
	// Update 前（普通路径，SkipHooks=false）：填充 OnUpdate=true 的字段
	if err := db.Callback().Update().Before("gorm:update").
		Register("auto_fill:before_update", p.beforeUpdate); err != nil {
		return err
	}
	// Update 前（SkipHooks=true 路径，如 UpdateColumn）：填充 OnUpdate=true 的字段
	if err := db.Callback().Update().Before("gorm:update").
		Register("auto_fill:before_update_column", p.beforeUpdateColumn); err != nil {
		return err
	}
	return nil
}

// beforeCreate 处理 Create / CreateInBatches / Save（新增）。
// 填充所有 OnCreate=true 的字段。
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

// beforeUpdate 处理 Update / Updates / UpdateSimple / Save（更新），SkipHooks=false 路径。
// 填充所有 OnUpdate=true 的字段。
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
		// UpdateSimple 路径（表达式更新），clause.Set 已存在时追加
		injectIntoClauseSet(tx, schemaField.DBName, val)
	}
}

// beforeUpdateColumn 处理 UpdateColumn / UpdateColumns，SkipHooks=true 路径。
// SetColumn 在此路径不生效，需直接操作 Dest map 或退回 SetColumn。
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
			// map 路径直接写入列名
			dest[schemaField.DBName] = val
		default:
			// struct 路径退回 SetColumn
			tx.Statement.SetColumn(schemaField.DBName, val, true)
		}
	}
}

// ================== 内部工具 ==================

// hasSchema 判断 Statement 和 Schema 是否已初始化。
// 原生 SQL（db.Exec 等）无 Schema，跳过自动填充。
func hasSchema(tx *gorm.DB) bool {
	return tx.Statement != nil && tx.Statement.Schema != nil
}

// injectIntoClauseSet 将字段追加到已存在的 clause.Set 中。
// 专门处理 UpdateSimple 表达式更新路径，SetColumn 对此路径无效。
func injectIntoClauseSet(tx *gorm.DB, colName string, value any) {
	setClause, ok := tx.Statement.Clauses["SET"]
	if !ok {
		return // 不是 UpdateSimple 路径，跳过
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
