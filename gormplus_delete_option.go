package gormplus

import (
	"github.com/kuangshp/gorm-plus/query"
	"gorm.io/gen"
)

// DeleteOptions 是 Repository 删除操作的可选行为集合。
type DeleteOptions = query.DeleteOptions

// DeleteBuilder 删除操作链式构建器。
type DeleteBuilder = query.DeleteBuilder

// DeleteOption 是 Repository 删除操作的可选参数。
type DeleteOption = query.DeleteOption

// Delete 创建删除操作链式构建器。
func Delete() *DeleteBuilder {
	return query.Delete()
}

// WithPhysicalDelete 将删除操作切换为物理删除。
//
// 保留函数式写法，等价于 Delete().WithPhysicalDelete().Build()。
func WithPhysicalDelete() DeleteOption {
	return query.WithPhysicalDelete()
}

// ResolveDeleteOptions 合并删除操作可选参数。
func ResolveDeleteOptions(options []DeleteOption) DeleteOptions {
	return query.ResolveDeleteOptions(options)
}

// SplitDeleteConditions 从 gen.Condition 列表中剥离 DeleteOption。
func SplitDeleteConditions(conditions []gen.Condition) ([]gen.Condition, DeleteOptions) {
	return query.SplitDeleteConditions(conditions)
}
