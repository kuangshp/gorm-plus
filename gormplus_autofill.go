package gormplus

import (
	"github.com/kuangshp/gorm-plus/plugin"
)

// AutoFillConfig 自动填充插件配置。
type AutoFillConfig = plugin.AutoFillConfig

// FieldConfig 单个字段的自动填充配置。
// Name 填 Go 结构体字段名（如 "UpdatedBy"）或数据库列名（如 "updated_by"）均可，
// 插件通过 gorm schema 自动解析实际列名。
type FieldConfig = plugin.FieldConfig

// FieldGetter 从 context 中获取字段值的函数类型，返回 any 支持 int64 / string 等任意类型。
type FieldGetter = plugin.FieldGetter

// context key 常量，用于在中间件和自动填充插件之间传递字段值。
// 支持同时传递最多 10 个字段，按用途建议如下：
//
//	// 中间件写入示例
//	ctx := context.WithValue(c.Request.Context(), gormplus.CtxContextKey1, claims.UserID)   // 操作人 ID
//	ctx  = context.WithValue(ctx,                 gormplus.CtxContextKey2, claims.Username) // 操作人姓名
//	c.Request = c.Request.WithContext(ctx)
var (
	CtxContextKey1  = plugin.CtxOperatorKey1  // 建议存操作人 ID
	CtxContextKey2  = plugin.CtxOperatorKey2  // 建议存操作人姓名
	CtxContextKey3  = plugin.CtxOperatorKey3  // 建议存部门 ID
	CtxContextKey4  = plugin.CtxOperatorKey4  // 自定义
	CtxContextKey5  = plugin.CtxOperatorKey5  // 自定义
	CtxContextKey6  = plugin.CtxOperatorKey6  // 自定义
	CtxContextKey7  = plugin.CtxOperatorKey7  // 自定义
	CtxContextKey8  = plugin.CtxOperatorKey8  // 自定义
	CtxContextKey9  = plugin.CtxOperatorKey9  // 自定义
	CtxContextKey10 = plugin.CtxOperatorKey10 // 自定义
)

// NewAutoFillPlugin 创建自动填充插件实例，通过 db.Use() 注册。
//
// 使用示例：
//
//	// 基础：操作人 ID（int64）
//	db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
//	    Fields: []gormplus.FieldConfig{
//	        {Name: "CreatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true},
//	        {Name: "UpdatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true, OnUpdate: true},
//	    },
//	}))
//
//	// 进阶：操作人 ID + 操作人姓名（多字段）
//	db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
//	    Fields: []gormplus.FieldConfig{
//	        {Name: "CreatedBy",   Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1),  OnCreate: true},
//	        {Name: "UpdatedBy",   Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1),  OnCreate: true, OnUpdate: true},
//	        {Name: "CreatedName", Getter: gormplus.CtxGetter[string](gormplus.CtxContextKey2), OnCreate: true},
//	        {Name: "UpdatedName", Getter: gormplus.CtxGetter[string](gormplus.CtxContextKey2), OnCreate: true, OnUpdate: true},
//	    },
//	}))
//
//	// UUID 操作人（string 类型）
//	db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
//	    Fields: []gormplus.FieldConfig{
//	        {Name: "CreatedBy", Getter: gormplus.CtxGetter[string](gormplus.CtxContextKey1), OnCreate: true},
//	        {Name: "UpdatedBy", Getter: gormplus.CtxGetter[string](gormplus.CtxContextKey1), OnCreate: true, OnUpdate: true},
//	    },
//	}))
func NewAutoFillPlugin(cfg plugin.AutoFillConfig) *plugin.AutoFillPlugin {
	return plugin.NewAutoFillPlugin(cfg)
}

// CtxGetter 从 context 读取指定 key 的值，T 为期望类型。
// 类型不匹配或 key 不存在时返回 T 的零值。
// 内部自动调用 resolveCtx，注册 RegisterCtxResolver 后直接传 *gin.Context 也可生效。
//
// 使用示例：
//
//	gormplus.CtxGetter[int64](gormplus.CtxContextKey1)   // 读取 int64 类型操作人 ID
//	gormplus.CtxGetter[string](gormplus.CtxContextKey2)  // 读取 string 类型操作人姓名
//	gormplus.CtxGetter[string]("myKey")                  // 读取自定义 key 的值
func CtxGetter[T any](key any) plugin.FieldGetter {
	return plugin.CtxGetter[T](key)
}
