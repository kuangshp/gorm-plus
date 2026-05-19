package query

import (
	"gorm.io/gen"
	"gorm.io/gen/field"
	"time"
)

type SingleFlightOption struct {
	KeyPrefix string
	TTL       time.Duration
	Enable    bool
}

type CacheOption struct {
	Key    string
	TTL    time.Duration
	Enable bool
}

// QueryOption 查询参数结构体
type QueryOption struct {
	Cond       []gen.Condition     // 原生条件
	Order      []field.Expr        // 排序字段
	Select     []field.Expr        // 指定查询字段
	OmitFields []field.Expr        // 排除字段
	Limit      *int                // 查询条数
	SF         *SingleFlightOption // 使用sf并发处理
	Cache      *CacheOption        // 使用缓存
}

// QueryBuilder 链式构建器
type QueryBuilder struct {
	option QueryOption
}

// Query 创建构建器
func Query() *QueryBuilder {
	return &QueryBuilder{
		option: QueryOption{},
	}
}

// Where 条件
func (q *QueryBuilder) Where(cond ...gen.Condition) *QueryBuilder {
	q.option.Cond = append(
		q.option.Cond,
		cond...,
	)
	return q
}

// Order 排序
func (q *QueryBuilder) Order(fields ...field.Expr) *QueryBuilder {
	q.option.Order = append(
		q.option.Order,
		fields...,
	)
	return q
}

// Select 指定字段
func (q *QueryBuilder) Select(fields ...field.Expr) *QueryBuilder {
	q.option.Select = append(
		q.option.Select,
		fields...,
	)
	return q
}

// Omit 排除字段
func (q *QueryBuilder) Omit(fields ...field.Expr) *QueryBuilder {
	q.option.OmitFields = append(
		q.option.OmitFields,
		fields...,
	)
	return q
}

// Limit 查询条数
func (q *QueryBuilder) Limit(limit int) *QueryBuilder {
	q.option.Limit = &limit
	return q
}

// WithSingleFlight 启用singleflight
func (q *QueryBuilder) WithSingleFlight(keyPrefix string, ttl time.Duration) *QueryBuilder {
	q.option.SF = &SingleFlightOption{
		Enable:    true,
		KeyPrefix: keyPrefix,
		TTL:       ttl,
	}
	return q
}

// WithCache 启用缓存
func (q *QueryBuilder) WithCache(key string, ttl time.Duration) *QueryBuilder {
	q.option.Cache = &CacheOption{
		Enable: true,
		Key:    key,
		TTL:    ttl,
	}
	return q
}

// Build 构建QueryOption
func (q *QueryBuilder) Build() QueryOption {
	return q.option
}

// MergeQueryOptions 合并配置
func MergeQueryOptions(opts ...QueryOption) QueryOption {
	var result QueryOption
	for _, opt := range opts {
		// Cond
		if len(opt.Cond) > 0 {
			result.Cond = append(
				result.Cond,
				opt.Cond...,
			)
		}
		// Order
		if len(opt.Order) > 0 {
			result.Order = append(
				result.Order,
				opt.Order...,
			)
		}

		// Select
		if len(opt.Select) > 0 {
			result.Select = append(
				result.Select,
				opt.Select...,
			)
		}

		// Omit
		if len(opt.OmitFields) > 0 {
			result.OmitFields = append(
				result.OmitFields,
				opt.OmitFields...,
			)
		}

		// Limit
		if opt.Limit != nil {
			result.Limit = opt.Limit
		}

		// SF
		if opt.SF != nil {
			result.SF = opt.SF
		}
		// Cache
		if opt.Cache != nil {
			result.Cache = opt.Cache
		}
	}
	return result
}
