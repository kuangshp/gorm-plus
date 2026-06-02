package plugin

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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
