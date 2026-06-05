package query

import (
	"gorm.io/gen"
	gormclause "gorm.io/gorm/clause"
)

// DeleteOptions 是 Repository 删除操作的可选行为集合。
type DeleteOptions struct {
	// Physical 为 true 时使用 GORM Unscoped 物理删除。
	Physical bool
	// Clauses 删除时附加 GORM clause。
	Clauses []gormclause.Expression
}

// DeleteBuilder 删除操作链式构建器。
type DeleteBuilder struct {
	option DeleteOptions
}

// DeleteOption 是 Repository 删除操作的可选参数。
//
// 它同时实现 gen.Condition，因此可直接传给 DeleteByCondition。
type DeleteOption interface {
	gen.Condition
	applyDeleteOption(*DeleteOptions)
}

type deleteOption struct {
	apply func(*DeleteOptions)
}

// Delete 创建删除操作链式构建器。
func Delete() *DeleteBuilder {
	return &DeleteBuilder{}
}

// WithPhysicalDelete 将删除操作切换为物理删除。
func (b *DeleteBuilder) WithPhysicalDelete() *DeleteBuilder {
	b.option.Physical = true
	return b
}

// Physical 是 WithPhysicalDelete 的短别名。
func (b *DeleteBuilder) Physical() *DeleteBuilder {
	return b.WithPhysicalDelete()
}

// WithClauses 删除时附加 GORM clause。
func (b *DeleteBuilder) WithClauses(clauses ...gormclause.Expression) *DeleteBuilder {
	b.option.Clauses = append(b.option.Clauses, clauses...)
	return b
}

// Clauses 是 WithClauses 的短别名。
func (b *DeleteBuilder) Clauses(clauses ...gormclause.Expression) *DeleteBuilder {
	return b.WithClauses(clauses...)
}

// Build 构建删除操作可选参数。
func (b *DeleteBuilder) Build() DeleteOption {
	opt := b.option
	return deleteOption{
		apply: func(opts *DeleteOptions) {
			mergeDeleteOptions(opts, opt)
		},
	}
}

// WithPhysicalDelete 将删除操作切换为物理删除。
//
// 保留函数式写法，等价于 Delete().WithPhysicalDelete().Build()。
func WithPhysicalDelete() DeleteOption {
	return Delete().WithPhysicalDelete().Build()
}

// WithDeleteClauses 删除时附加 GORM clause。
func WithDeleteClauses(clauses ...gormclause.Expression) DeleteOption {
	return Delete().WithClauses(clauses...).Build()
}

func (o deleteOption) BeCond() interface{} { return nil }

func (o deleteOption) CondError() error { return nil }

func (o deleteOption) applyDeleteOption(opts *DeleteOptions) {
	if o.apply != nil {
		o.apply(opts)
	}
}

func mergeDeleteOptions(dst *DeleteOptions, src DeleteOptions) {
	if src.Physical {
		dst.Physical = true
	}
	dst.Clauses = append(dst.Clauses, src.Clauses...)
}

// ResolveDeleteOptions 合并删除操作可选参数。
func ResolveDeleteOptions(options []DeleteOption) DeleteOptions {
	var opts DeleteOptions
	for _, option := range options {
		if option != nil {
			option.applyDeleteOption(&opts)
		}
	}
	return opts
}

// SplitDeleteConditions 从 gen.Condition 列表中剥离 DeleteOption。
func SplitDeleteConditions(conditions []gen.Condition) ([]gen.Condition, DeleteOptions) {
	var opts DeleteOptions
	filtered := make([]gen.Condition, 0, len(conditions))
	for _, condition := range conditions {
		if option, ok := condition.(DeleteOption); ok {
			option.applyDeleteOption(&opts)
			continue
		}
		filtered = append(filtered, condition)
	}
	return filtered, opts
}
