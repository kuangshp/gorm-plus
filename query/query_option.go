package query

import (
	"gorm.io/gen"
	"gorm.io/gen/field"
)

// QueryOption 查询参数结构体
type QueryOption struct {
	Cond   []gen.Condition // 原生条件
	Order  []field.Expr    // 排序字段
	Select []field.Expr    // 指定查询字段
	Limit  *int            // 查询条数
}
