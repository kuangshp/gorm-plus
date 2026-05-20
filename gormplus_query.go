package gormplus

import (
	"context"

	"github.com/kuangshp/gorm-plus/query"
	"gorm.io/gorm"
)

// ================== Query 原生 gorm 链式条件构造器 ==================

// IQueryBuilder 原生 gorm 扩展条件构造器接口。
// 链式拼装扩展条件后调用 Build() 返回原生 *gorm.DB，继续使用所有 gorm 原生方法。
type IQueryBuilder = query.IQueryBuilder

// Query 创建原生 gorm 链式条件构造器。
//
// 使用示例：
//
//	// 分页列表查询
//	built := gormplus.Query[*model.Account](db, ctx).
//	    LLike("username", username).                        // 空时自动跳过
//	    WhereIf(status != 0, "status = ?", status).         // false 时跳过
//	    BetweenIfNotZero("created_at", startTime, endTime). // 任一零值时跳过
//	    WhereIf(len(ids) > 0, "dept_id IN ?", ids).
//	    Build()
//	var total int64
//	built.Count(&total)
//	built.Order("created_at DESC").Limit(pageSize).Offset((page-1)*pageSize).Find(&list)
//
//	// OR 分组：WHERE status = 1 OR (role = 99 AND org_id = 10)
//	gormplus.Query[*model.Account](db, ctx).
//	    WhereIf(true, "status = ?", 1).
//	    OrGroup(func(q gormplus.IQueryBuilder) {
//	        q.WhereIf(role != 0, "role = ?", role).
//	          WhereIf(orgID != 0, "org_id = ?", orgID)
//	    }).Build().Find(&list)
//
// var Query = query.NewQuery
func Query[T any](db *gorm.DB, ctx context.Context) query.IQueryBuilder {
	return query.NewQuery[T](db, ctx)
}

// FindByPage 泛型分页查询，返回 (数据列表, 总数, error)。
// 适合结果直接映射到 model struct 的列表查询，内部 Count 时自动去掉 ORDER BY。
//
// 使用示例：
//
//	list, total, err := gormplus.FindByPage[*model.Account](
//	    gormplus.Query[*model.Account](db, ctx).
//	        LLike("username", username).
//	        WhereIf(status != 0, "status = ?", status).
//	        Build().Order("created_at DESC"),
//	    pageNum, pageSize,
//	)
func FindByPage[T any](q *gorm.DB, pageNum, pageSize int) ([]T, int64, error) {
	return query.FindByPage[T](q, pageNum, pageSize)
}

// ScanByPage 泛型分页扫描，返回 (数据列表, 总数, error)。
// 使用 Scan 代替 Find，适合联表查询、自定义 SELECT 字段映射到 VO 的场景。
//
// 使用示例：
//
//	type AccountVO struct {
//	    ID       int64  `json:"id"`
//	    Username string `json:"username"`
//	    DeptName string `json:"deptName"` // 来自 JOIN
//	}
//
//	list, total, err := gormplus.ScanByPage[AccountVO](
//	    gormplus.Query[*model.Account](db, ctx).
//	        LLike("a.username", username).
//	        Build().
//	        Select("a.id", "a.username", "d.name AS dept_name").
//	        Joins("LEFT JOIN sys_dept d ON d.id = a.dept_id").
//	        Order("a.created_at DESC"),
//	    pageNum, pageSize,
//	)
func ScanByPage[T any](q *gorm.DB, pageNum, pageSize int) ([]T, int64, error) {
	return query.ScanByPage[T](q, pageNum, pageSize)
}

// ================== GenWrap gorm-gen 类型安全链式构造器 ==================

// IGenWrapper gorm-gen 扩展条件构建器接口。

// DbPage 处理分页
func DbPage(pageNumber, pageSize int64) (offset int, limit int) {
	return query.DbPage(pageNumber, pageSize)
}
