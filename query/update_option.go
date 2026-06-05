package query

import (
	"gorm.io/gen/field"
	gormclause "gorm.io/gorm/clause"
)

// UpdateOptions 是 Repository 更新操作的可选行为集合。
type UpdateOptions struct {
	// Columns 更新时使用的字段赋值表达式。
	Columns []field.AssignExpr
	// Clauses 更新时附加 GORM clause。
	Clauses []gormclause.Expression
}

// UpdateBuilder 更新操作链式构建器。
type UpdateBuilder struct {
	option UpdateOptions
}

// UpdateOption 是 Repository 更新操作的可选参数。
type UpdateOption interface {
	applyUpdateOption(*UpdateOptions)
}

type updateOption struct {
	apply func(*UpdateOptions)
}

// Update 创建更新操作链式构建器。
func Update() *UpdateBuilder {
	return &UpdateBuilder{}
}

// WithColumns 设置更新字段。
func (b *UpdateBuilder) WithColumns(columns ...field.AssignExpr) *UpdateBuilder {
	b.option.Columns = append(b.option.Columns, columns...)
	return b
}

// Columns 是 WithColumns 的短别名。
func (b *UpdateBuilder) Columns(columns ...field.AssignExpr) *UpdateBuilder {
	return b.WithColumns(columns...)
}

// WithClauses 更新时附加 GORM clause。
func (b *UpdateBuilder) WithClauses(clauses ...gormclause.Expression) *UpdateBuilder {
	b.option.Clauses = append(b.option.Clauses, clauses...)
	return b
}

// Clauses 是 WithClauses 的短别名。
func (b *UpdateBuilder) Clauses(clauses ...gormclause.Expression) *UpdateBuilder {
	return b.WithClauses(clauses...)
}

// Build 构建更新操作可选参数。
func (b *UpdateBuilder) Build() UpdateOption {
	opt := b.option
	return updateOption{
		apply: func(opts *UpdateOptions) {
			mergeUpdateOptions(opts, opt)
		},
	}
}

// WithUpdateColumns 设置更新字段。
func WithUpdateColumns(columns ...field.AssignExpr) UpdateOption {
	return Update().WithColumns(columns...).Build()
}

// WithUpdateClauses 更新时附加 GORM clause。
func WithUpdateClauses(clauses ...gormclause.Expression) UpdateOption {
	return Update().WithClauses(clauses...).Build()
}

func (o updateOption) applyUpdateOption(opts *UpdateOptions) {
	if o.apply != nil {
		o.apply(opts)
	}
}

func mergeUpdateOptions(dst *UpdateOptions, src UpdateOptions) {
	dst.Columns = append(dst.Columns, src.Columns...)
	dst.Clauses = append(dst.Clauses, src.Clauses...)
}

// ResolveUpdateOptions 合并更新操作可选参数。
func ResolveUpdateOptions(options []UpdateOption) UpdateOptions {
	var opts UpdateOptions
	for _, option := range options {
		if option != nil {
			option.applyUpdateOption(&opts)
		}
	}
	return opts
}
