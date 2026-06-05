package plugin

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type tenantFieldTestModel struct {
	ID       int64  `gorm:"column:id"`
	Name     string `gorm:"column:name"`
	TenantID int64  `gorm:"column:tenant_id"`
}

func (tenantFieldTestModel) TableName() string {
	return "tenant_field_test_models"
}

type noTenantFieldTestModel struct {
	ID   int64  `gorm:"column:id"`
	Name string `gorm:"column:name"`
}

type tenantJoinOrderModel struct {
	ID         int64 `gorm:"column:id"`
	SupplierID int64 `gorm:"column:supplier_id"`
	CompanyID  int64 `gorm:"column:company_id"`
}

func (tenantJoinOrderModel) TableName() string {
	return "tenant_join_orders"
}

type tenantJoinSupplierModel struct {
	ID        int64 `gorm:"column:id"`
	CompanyID int64 `gorm:"column:company_id"`
}

func (tenantJoinSupplierModel) TableName() string {
	return "tenant_join_suppliers"
}

type noTenantJoinSupplierModel struct {
	ID int64 `gorm:"column:id"`
}

func (noTenantJoinSupplierModel) TableName() string {
	return "no_tenant_join_suppliers"
}

func (noTenantFieldTestModel) TableName() string {
	return "no_tenant_field_test_models"
}

func newTenantFieldTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := RegisterTenant[int64](db, TenantConfig[int64]{TenantField: "tenant_id"}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}
	return db
}

func TestTenantQuerySkipsMissingTenantField(t *testing.T) {
	db := newTenantFieldTestDB(t)
	ctx := WithTenantID(context.Background(), int64(1001))

	var noTenantRows []noTenantFieldTestModel
	noTenantStmt := db.WithContext(ctx).Find(&noTenantRows).Statement
	if sql := noTenantStmt.SQL.String(); strings.Contains(sql, "tenant_id") {
		t.Fatalf("query without tenant field should not include tenant condition, sql=%s", sql)
	}

	var tenantRows []tenantFieldTestModel
	tenantStmt := db.WithContext(ctx).Find(&tenantRows).Statement
	if sql := tenantStmt.SQL.String(); !strings.Contains(sql, "tenant_id") {
		t.Fatalf("query with tenant field should include tenant condition, sql=%s", sql)
	}
}

func TestTenantCreateSkipsMissingTenantField(t *testing.T) {
	db := newTenantFieldTestDB(t)
	ctx := WithTenantID(context.Background(), int64(1001))

	noTenantRow := noTenantFieldTestModel{Name: "public"}
	noTenantStmt := db.WithContext(ctx).Create(&noTenantRow).Statement
	if sql := noTenantStmt.SQL.String(); strings.Contains(sql, "tenant_id") {
		t.Fatalf("create without tenant field should not include tenant column, sql=%s", sql)
	}

	tenantRow := tenantFieldTestModel{Name: "tenant"}
	if err := db.WithContext(ctx).Create(&tenantRow).Error; err != nil {
		t.Fatalf("create with tenant field: %v", err)
	}
	if tenantRow.TenantID != 1001 {
		t.Fatalf("tenant field was not filled, got %d", tenantRow.TenantID)
	}
}

func TestTenantJoinPrefixesMainTableAndInjectsJoinTableWithTenantField(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&tenantJoinOrderModel{}, &tenantJoinSupplierModel{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := RegisterTenant[int64](db, TenantConfig[int64]{TenantField: "company_id"}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}

	ctx := WithTenantID(context.Background(), int64(3))
	var rows []tenantJoinOrderModel
	stmt := db.Session(&gorm.Session{DryRun: true}).
		WithContext(ctx).
		Model(&tenantJoinOrderModel{}).
		Clauses(clause.From{Joins: []clause.Join{{
			Type:  clause.LeftJoin,
			Table: clause.Table{Name: "tenant_join_suppliers"},
			ON: clause.Where{Exprs: []clause.Expression{
				clause.Expr{SQL: "`tenant_join_suppliers`.`id` = `tenant_join_orders`.`supplier_id`"},
			}},
		}}}).
		Find(&rows).
		Statement

	sql := stmt.SQL.String()
	if !strings.Contains(sql, "`tenant_join_orders`.`company_id` = ?") {
		t.Fatalf("expected main tenant field to be prefixed, sql=%s", sql)
	}
	if !strings.Contains(sql, "`tenant_join_suppliers`.`company_id` = ?") {
		t.Fatalf("expected join table tenant field to be injected, sql=%s", sql)
	}
	if strings.Contains(sql, " WHERE `company_id` = ?") || strings.Contains(sql, " AND `company_id` = ?") {
		t.Fatalf("expected no unqualified tenant field, sql=%s", sql)
	}
}

func TestTenantJoinSkipsJoinTableWithoutTenantField(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&tenantJoinOrderModel{}, &noTenantJoinSupplierModel{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := RegisterTenant[int64](db, TenantConfig[int64]{TenantField: "company_id"}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}

	ctx := WithTenantID(context.Background(), int64(3))
	var rows []tenantJoinOrderModel
	stmt := db.Session(&gorm.Session{DryRun: true}).
		WithContext(ctx).
		Model(&tenantJoinOrderModel{}).
		Clauses(clause.From{Joins: []clause.Join{{
			Type:  clause.LeftJoin,
			Table: clause.Table{Name: "no_tenant_join_suppliers"},
			ON: clause.Where{Exprs: []clause.Expression{
				clause.Expr{SQL: "`no_tenant_join_suppliers`.`id` = `tenant_join_orders`.`supplier_id`"},
			}},
		}}}).
		Find(&rows).
		Statement

	sql := stmt.SQL.String()
	if !strings.Contains(sql, "`tenant_join_orders`.`company_id` = ?") {
		t.Fatalf("expected main tenant field to be prefixed, sql=%s", sql)
	}
	if strings.Contains(sql, "`no_tenant_join_suppliers`.`company_id`") {
		t.Fatalf("expected join table without tenant field to be skipped, sql=%s", sql)
	}
}
