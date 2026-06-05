package gormplus

import (
	"github.com/kuangshp/gorm-plus/query"
	"gorm.io/gen/field"
	"gorm.io/gorm/clause"
)

// CreateOptions 是 Repository 创建操作的可选行为集合。
type CreateOptions = query.CreateOptions

// CreateBuilder 创建操作链式构建器。
type CreateBuilder = query.CreateBuilder

// CreateOption 是 Repository 创建操作的可选参数。
type CreateOption = query.CreateOption

// Create 创建创建操作链式构建器。
func Create() *CreateBuilder {
	return query.Create()
}

// WithCreateOmit 创建时忽略指定字段。
func WithCreateOmit(fields ...field.Expr) CreateOption {
	return query.WithCreateOmit(fields...)
}

// WithCreateClauses 创建时附加 GORM clause。
func WithCreateClauses(clauses ...clause.Expression) CreateOption {
	return query.WithCreateClauses(clauses...)
}

// WithCreateOnConflict 创建时附加 GORM OnConflict clause。
func WithCreateOnConflict(onConflict clause.OnConflict) CreateOption {
	return query.WithCreateOnConflict(onConflict)
}

// ResolveCreateOptions 合并创建操作可选参数。
func ResolveCreateOptions(options []CreateOption) CreateOptions {
	return query.ResolveCreateOptions(options)
}
