package gormplus

import (
	"github.com/kuangshp/gorm-plus/query"
	"gorm.io/gen/field"
	"gorm.io/gorm/clause"
)

// UpdateOptions 是 Repository 更新操作的可选行为集合。
type UpdateOptions = query.UpdateOptions

// UpdateBuilder 更新操作链式构建器。
type UpdateBuilder = query.UpdateBuilder

// UpdateOption 是 Repository 更新操作的可选参数。
type UpdateOption = query.UpdateOption

// Update 创建更新操作链式构建器。
func Update() *UpdateBuilder {
	return query.Update()
}

// WithUpdateColumns 设置更新字段。
func WithUpdateColumns(columns ...field.AssignExpr) UpdateOption {
	return query.WithUpdateColumns(columns...)
}

// WithUpdateClauses 更新时附加 GORM clause。
func WithUpdateClauses(clauses ...clause.Expression) UpdateOption {
	return query.WithUpdateClauses(clauses...)
}

// ResolveUpdateOptions 合并更新操作可选参数。
func ResolveUpdateOptions(options []UpdateOption) UpdateOptions {
	return query.ResolveUpdateOptions(options)
}
