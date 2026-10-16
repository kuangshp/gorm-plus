package generator

import (
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"
)

func getTables(db *gorm.DB) ([]string, error) {
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return nil, err
	}
	if db.Dialector.Name() == "sqlite" {
		filtered := make([]string, 0, len(tables))
		for _, table := range tables {
			if !strings.HasPrefix(strings.ToLower(table), "sqlite_") {
				filtered = append(filtered, table)
			}
		}
		tables = filtered
	}
	sort.Strings(tables)
	return tables, nil
}

// 元数据交给当前驱动读取，避免生成流程依赖 MySQL 的 SHOW 语法。
func getTableColumns(db *gorm.DB, tableName string) ([]ColumnInfo, error) {
	columns, err := db.Migrator().ColumnTypes(tableName)
	if err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("表 %q 没有可读取的字段", tableName)
	}
	result := make([]ColumnInfo, len(columns))
	for i, col := range columns {
		result[i] = columnInfo(col)
		switch db.Dialector.Name() {
		case "sqlserver":
			switch result[i].Type {
			case "bit":
				result[i].Type = "boolean"
			case "timestamp", "rowversion":
				result[i].Type = "binary(8)"
			}
		case "mysql":
			if result[i].Type == "bit" || strings.HasPrefix(result[i].Type, "bit(") {
				result[i].Type = "binary"
			}
		}
	}
	// SQL Server 驱动的 ColumnTypes 尚未读取 MS_Description。
	if db.Dialector.Name() == "sqlserver" {
		var comments []struct{ Name, Comment string }
		if err := db.Raw(`SELECT c.name AS name, CAST(ep.value AS nvarchar(max)) AS comment
FROM sys.extended_properties ep JOIN sys.columns c
ON ep.major_id = c.object_id AND ep.minor_id = c.column_id
WHERE ep.class = 1 AND ep.name = 'MS_Description' AND ep.major_id = OBJECT_ID(?)`, tableName).Scan(&comments).Error; err != nil {
			return nil, err
		}
		byName := make(map[string]string, len(comments))
		for _, c := range comments {
			byName[c.Name] = c.Comment
		}
		for i := range result {
			result[i].Comment = byName[result[i].Name]
		}
	}
	return result, nil
}

func columnInfo(col gorm.ColumnType) ColumnInfo {
	typ, ok := col.ColumnType()
	if !ok || typ == "" {
		typ = col.DatabaseTypeName()
	}
	typ = strings.ToLower(strings.TrimSpace(typ))
	// PostgreSQL 的 format_type 返回 SQL 标准别名，统一后才能生成长度校验。
	for _, alias := range []struct{ from, to string }{{"character varying", "varchar"}, {"character", "char"}} {
		if typ == alias.from || strings.HasPrefix(typ, alias.from+"(") {
			typ = alias.to + strings.TrimPrefix(typ, alias.from)
			break
		}
	}
	// PostgreSQL / SQL Server 通常将精度和长度分开返回。
	if !strings.Contains(typ, "(") {
		if isDecimalSQLType(typ) {
			if precision, scale, ok := col.DecimalSize(); ok && precision > 0 {
				typ += fmt.Sprintf("(%d,%d)", precision, scale)
			}
		} else if strings.Contains(typ, "char") || strings.Contains(typ, "binary") {
			if length, ok := col.Length(); ok && length > 0 {
				typ += fmt.Sprintf("(%d)", length)
			}
		}
	}
	nullable, _ := col.Nullable()
	primaryKey, _ := col.PrimaryKey()
	comment, _ := col.Comment()
	extra := ""
	if autoIncrement, _ := col.AutoIncrement(); autoIncrement {
		// 统一供主键选择使用（兼容 MySQL 的旧 Extra 格式）。
		extra = "auto_increment"
	}
	return ColumnInfo{Name: col.Name(), Type: typ, CanNull: nullable, IsKey: primaryKey, Extra: extra, Comment: comment}
}

// 表注释缺失时调用方使用模型名；SQLite 不支持表注释。
func getTableComment(db *gorm.DB, tableName string) string {
	var comment string
	switch db.Dialector.Name() {
	case "mysql":
		if table, err := db.Migrator().TableType(tableName); err == nil {
			comment, _ = table.Comment()
		}
	case "postgres":
		stmt := &gorm.Statement{DB: db}
		db.Raw("SELECT COALESCE(obj_description(to_regclass(?), 'pg_class'), '')", stmt.Quote(tableName)).Scan(&comment)
	case "sqlserver":
		db.Raw(`SELECT CAST(value AS nvarchar(max)) FROM sys.extended_properties
WHERE class = 1 AND major_id = OBJECT_ID(?) AND minor_id = 0 AND name = 'MS_Description'`, tableName).Scan(&comment)
	}
	return comment
}
