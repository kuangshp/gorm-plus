package query

import (
	"gorm.io/gen/field"
	gormclause "gorm.io/gorm/clause"
)

// CreateOptions 是 Repository 创建操作的可选行为集合。
type CreateOptions struct {
	// OmitFields 创建时忽略指定字段。
	OmitFields []field.Expr
	// Clauses 创建时附加 GORM clause，例如 clause.OnConflict。
	Clauses []gormclause.Expression
}

// CreateBuilder 创建操作链式构建器。
type CreateBuilder struct {
	option CreateOptions
}

// CreateOption 是 Repository 创建操作的可选参数。
type CreateOption interface {
	applyCreateOption(*CreateOptions)
}

type createOption struct {
	apply func(*CreateOptions)
}

// Create 创建创建操作链式构建器。
func Create() *CreateBuilder {
	return &CreateBuilder{}
}

// WithOmit 创建时忽略指定字段。
func (b *CreateBuilder) WithOmit(fields ...field.Expr) *CreateBuilder {
	b.option.OmitFields = append(b.option.OmitFields, fields...)
	return b
}

// Omit 是 WithOmit 的短别名。
func (b *CreateBuilder) Omit(fields ...field.Expr) *CreateBuilder {
	return b.WithOmit(fields...)
}

// WithClauses 创建时附加 GORM clause。
func (b *CreateBuilder) WithClauses(clauses ...gormclause.Expression) *CreateBuilder {
	b.option.Clauses = append(b.option.Clauses, clauses...)
	return b
}

// Clauses 是 WithClauses 的短别名。
func (b *CreateBuilder) Clauses(clauses ...gormclause.Expression) *CreateBuilder {
	return b.WithClauses(clauses...)
}

// WithOnConflict 创建时附加 GORM OnConflict clause。
func (b *CreateBuilder) WithOnConflict(onConflict gormclause.OnConflict) *CreateBuilder {
	return b.WithClauses(onConflict)
}

// OnConflict 是 WithOnConflict 的短别名。
func (b *CreateBuilder) OnConflict(onConflict gormclause.OnConflict) *CreateBuilder {
	return b.WithOnConflict(onConflict)
}

// Build 构建创建操作可选参数。
func (b *CreateBuilder) Build() CreateOption {
	opt := b.option
	return createOption{
		apply: func(opts *CreateOptions) {
			mergeCreateOptions(opts, opt)
		},
	}
}

// WithCreateOmit 创建时忽略指定字段。
func WithCreateOmit(fields ...field.Expr) CreateOption {
	return Create().WithOmit(fields...).Build()
}

// WithCreateClauses 创建时附加 GORM clause。
func WithCreateClauses(clauses ...gormclause.Expression) CreateOption {
	return Create().WithClauses(clauses...).Build()
}

// WithCreateOnConflict 创建时附加 GORM OnConflict clause。
func WithCreateOnConflict(onConflict gormclause.OnConflict) CreateOption {
	return Create().WithOnConflict(onConflict).Build()
}

func (o createOption) applyCreateOption(opts *CreateOptions) {
	if o.apply != nil {
		o.apply(opts)
	}
}

func mergeCreateOptions(dst *CreateOptions, src CreateOptions) {
	dst.OmitFields = append(dst.OmitFields, src.OmitFields...)
	dst.Clauses = append(dst.Clauses, src.Clauses...)
}

// ResolveCreateOptions 合并创建操作可选参数。
func ResolveCreateOptions(options []CreateOption) CreateOptions {
	var opts CreateOptions
	for _, option := range options {
		if option != nil {
			option.applyCreateOption(&opts)
		}
	}
	return opts
}
