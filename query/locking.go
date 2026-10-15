package query

import (
	"gorm.io/gorm"
	gormclause "gorm.io/gorm/clause"
)

// withForUpdateClause 将字段形式的行锁选项转换为原生子句，不修改调用方的切片。
// 已有最终 UPDATE 锁保留其 Table / Options；true 不会被后续普通选项取消。
func (opt QueryOption) withForUpdateClause() QueryOption {
	if !opt.ForUpdate {
		return opt
	}
	for i := len(opt.Clauses) - 1; i >= 0; i-- {
		var locking gormclause.Locking
		switch expression := opt.Clauses[i].(type) {
		case gormclause.Locking:
			locking = expression
		case *gormclause.Locking:
			if expression == nil {
				continue
			}
			locking = *expression
		default:
			continue
		}
		if locking.Strength == gormclause.LockingStrengthUpdate {
			return opt
		}
		break
	}
	opt.Clauses = append(append([]gormclause.Expression(nil), opt.Clauses...),
		gormclause.Locking{Strength: gormclause.LockingStrengthUpdate})
	return opt
}

// WithLockingFrom 将实际查询中的行锁子句同步到选项，供 Repository 决定是否使用缓存。
// 生成代码在执行查询前调用，兼容在 GenWrap 回调中设置 ForUpdate 的情况。
func (opt QueryOption) WithLockingFrom(db *gorm.DB) QueryOption {
	if db != nil && db.Statement != nil {
		if locking, ok := db.Statement.Clauses["FOR"]; ok {
			// 新建切片，避免修改调用方原有的 Clauses 底层数组。
			opt.Clauses = append(append([]gormclause.Expression(nil), opt.Clauses...), locking.Expression)
		}
	}
	return opt
}

func hasRowLock(clauses []gormclause.Expression) bool {
	for _, expression := range clauses {
		switch locking := expression.(type) {
		case gormclause.Locking:
			return true
		case *gormclause.Locking:
			if locking != nil {
				return true
			}
		}
	}
	return false
}
