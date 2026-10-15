package plugin

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// hasORConnector 判断表达式列表是否通过单元素 OrConditions 使用 OR 连接。
// GORM 多元素 OrConditions 自带括号，单元素则会改变相邻条件的连接方式。
func hasORConnector(exprs []clause.Expression) bool {
	for _, expr := range exprs {
		if or, ok := expr.(clause.OrConditions); ok && len(or.Exprs) == 1 {
			return true
		}
	}
	return false
}

// groupWhereForIsolation 将已有 A OR B 整体包裹，确保追加过滤得到 (A OR B) AND scope。
// 避免变成 A OR (B AND scope)，也避免修改共享的原始表达式切片。
func groupWhereForIsolation(db *gorm.DB) {
	whereClause, ok := db.Statement.Clauses["WHERE"]
	if !ok {
		return
	}
	where, ok := whereClause.Expression.(clause.Where)
	if !ok || !hasORConnector(where.Exprs) {
		return
	}
	exprs := append([]clause.Expression(nil), where.Exprs...)
	whereClause.Expression = clause.Where{Exprs: []clause.Expression{clause.And(exprs...)}}
	db.Statement.Clauses["WHERE"] = whereClause
}
