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

func TestTenantCreatePolicyFillIfZero(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := RegisterTenant[int64](db, TenantConfig[int64]{
		TenantField:  "tenant_id",
		CreatePolicy: CreatePolicyFillIfZero,
	}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}

	ctx := WithTenantID(context.Background(), int64(1001))
	zeroTenant := tenantFieldTestModel{Name: "zero"}
	if err := db.WithContext(ctx).Create(&zeroTenant).Error; err != nil {
		t.Fatalf("create zero tenant: %v", err)
	}
	if zeroTenant.TenantID != 1001 {
		t.Fatalf("zero tenant field was not filled, got %d", zeroTenant.TenantID)
	}

	existingTenant := tenantFieldTestModel{Name: "existing", TenantID: 2002}
	if err := db.WithContext(ctx).Create(&existingTenant).Error; err != nil {
		t.Fatalf("create existing tenant: %v", err)
	}
	if existingTenant.TenantID != 2002 {
		t.Fatalf("existing tenant field should be preserved, got %d", existingTenant.TenantID)
	}
}

func TestTenantCreatePolicyRejectMismatch(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := RegisterTenant[int64](db, TenantConfig[int64]{
		TenantField:  "tenant_id",
		CreatePolicy: CreatePolicyRejectMismatch,
	}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}

	ctx := WithTenantID(context.Background(), int64(1001))
	row := tenantFieldTestModel{Name: "mismatch", TenantID: 2002}
	if err := db.WithContext(ctx).Create(&row).Error; err == nil {
		t.Fatal("expected mismatched tenant create to fail")
	}
}

func TestTenantRawSQLFieldBoundary(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := RegisterTenant[int64](db, TenantConfig[int64]{TenantField: "company_id"}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}

	ctx := WithTenantID(context.Background(), int64(3))
	var rows []tenantJoinOrderModel
	stmt := db.WithContext(ctx).
		Where("old_company_id = ? OR status = ?", 99, 1).
		Find(&rows).
		Statement

	if stmt.Error != nil {
		t.Fatalf("old_company_id should not be treated as company_id, err=%v", stmt.Error)
	}
	sql := stmt.SQL.String()
	if !strings.Contains(sql, "`company_id` = ?") {
		t.Fatalf("expected tenant field to still be injected, sql=%s", sql)
	}
}

func TestTenantRawSQLTenantFieldOrIsRejected(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := RegisterTenant[int64](db, TenantConfig[int64]{TenantField: "company_id"}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}

	ctx := WithTenantID(context.Background(), int64(3))
	var rows []tenantJoinOrderModel
	err = db.WithContext(ctx).
		Where("company_id = ? OR status = ?", 99, 1).
		Find(&rows).
		Error
	if err == nil {
		t.Fatal("expected tenant field in OR condition to be rejected")
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

func TestTenantPolicyReplaceKeepsOtherAliasTenantCondition(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&tenantJoinOrderModel{}, &tenantJoinSupplierModel{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	if err := RegisterTenant[int64](db, TenantConfig[int64]{
		TenantField:     "company_id",
		DuplicatePolicy: PolicyReplace,
	}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}

	ctx := WithTenantID(context.Background(), int64(3))
	var rows []tenantJoinOrderModel
	stmt := db.Session(&gorm.Session{DryRun: true}).
		WithContext(ctx).
		Model(&tenantJoinOrderModel{}).
		Where("`tenant_join_suppliers`.`company_id` = ?", int64(9)).
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
		t.Fatalf("expected main tenant field to be injected, sql=%s", sql)
	}
	if !strings.Contains(sql, "`tenant_join_suppliers`.`company_id` = ?") {
		t.Fatalf("expected supplier tenant condition to be preserved, sql=%s", sql)
	}
	if !containsVar(stmt.Vars, int64(9)) {
		t.Fatalf("expected supplier tenant arg 9 to be preserved, vars=%v sql=%s", stmt.Vars, sql)
	}
}

func TestTenantJoinScanAlsoInjectsTenantFields(t *testing.T) {
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
		Scan(&rows).
		Statement

	sql := stmt.SQL.String()
	if !strings.Contains(sql, "`tenant_join_orders`.`company_id` = ?") {
		t.Fatalf("expected scan main tenant field to be prefixed, sql=%s", sql)
	}
	if !strings.Contains(sql, "`tenant_join_suppliers`.`company_id` = ?") {
		t.Fatalf("expected scan join table tenant field to be injected, sql=%s", sql)
	}
	if strings.Contains(sql, " WHERE `company_id` = ?") || strings.Contains(sql, " AND `company_id` = ?") {
		t.Fatalf("expected scan to have no unqualified tenant field, sql=%s", sql)
	}
}

func TestTenantJoinQueryMethodsInjectTenantFields(t *testing.T) {
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
	tests := []struct {
		name string
		run  func(*gorm.DB) *gorm.Statement
	}{
		{
			name: "first",
			run: func(tx *gorm.DB) *gorm.Statement {
				var row tenantJoinOrderModel
				return tx.First(&row).Statement
			},
		},
		{
			name: "take",
			run: func(tx *gorm.DB) *gorm.Statement {
				var row tenantJoinOrderModel
				return tx.Take(&row).Statement
			},
		},
		{
			name: "last",
			run: func(tx *gorm.DB) *gorm.Statement {
				var row tenantJoinOrderModel
				return tx.Last(&row).Statement
			},
		},
		{
			name: "find",
			run: func(tx *gorm.DB) *gorm.Statement {
				var rows []tenantJoinOrderModel
				return tx.Find(&rows).Statement
			},
		},
		{
			name: "pluck",
			run: func(tx *gorm.DB) *gorm.Statement {
				var ids []int64
				return tx.Pluck("tenant_join_orders.id", &ids).Statement
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := tt.run(newTenantJoinDryRunDB(db, ctx))
			assertTenantJoinSQL(t, stmt.SQL.String(), tt.name)
		})
	}
}

func TestTenantJoinRowMethodsInjectTenantFields(t *testing.T) {
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
	tests := []struct {
		name string
		run  func(*gorm.DB) *gorm.Statement
	}{
		{
			name: "scan",
			run: func(tx *gorm.DB) *gorm.Statement {
				var rows []tenantJoinOrderModel
				return tx.Scan(&rows).Statement
			},
		},
		{
			name: "rows",
			run: func(tx *gorm.DB) *gorm.Statement {
				_, _ = tx.Rows()
				return tx.Statement
			},
		},
		{
			name: "row",
			run: func(tx *gorm.DB) *gorm.Statement {
				_ = tx.Row()
				return tx.Statement
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := tt.run(newTenantJoinDryRunDB(db, ctx))
			assertTenantJoinSQL(t, stmt.SQL.String(), tt.name)
		})
	}
}

func TestTenantJoinFindByPageQueryPathsInjectTenantFields(t *testing.T) {
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
	base := func() *gorm.DB {
		return db.Session(&gorm.Session{DryRun: true}).
			WithContext(ctx).
			Model(&tenantJoinOrderModel{}).
			Clauses(clause.From{Joins: []clause.Join{{
				Type:  clause.LeftJoin,
				Table: clause.Table{Name: "tenant_join_suppliers"},
				ON: clause.Where{Exprs: []clause.Expression{
					clause.Expr{SQL: "`tenant_join_suppliers`.`id` = `tenant_join_orders`.`supplier_id`"},
				}},
			}}})
	}

	var rows []tenantJoinOrderModel
	findStmt := base().Offset(0).Limit(5).Find(&rows).Statement
	assertTenantJoinSQL(t, findStmt.SQL.String(), "find")

	var total int64
	countStmt := base().Offset(-1).Limit(-1).Count(&total).Statement
	assertTenantJoinSQL(t, countStmt.SQL.String(), "count")
}

func newTenantJoinDryRunDB(db *gorm.DB, ctx context.Context) *gorm.DB {
	return db.Session(&gorm.Session{DryRun: true}).
		WithContext(ctx).
		Model(&tenantJoinOrderModel{}).
		Clauses(clause.From{Joins: []clause.Join{{
			Type:  clause.LeftJoin,
			Table: clause.Table{Name: "tenant_join_suppliers"},
			ON: clause.Where{Exprs: []clause.Expression{
				clause.Expr{SQL: "`tenant_join_suppliers`.`id` = `tenant_join_orders`.`supplier_id`"},
			}},
		}}})
}

func assertTenantJoinSQL(t *testing.T, sql string, op string) {
	t.Helper()
	if !strings.Contains(sql, "`tenant_join_orders`.`company_id` = ?") {
		t.Fatalf("expected %s main tenant field to be prefixed, sql=%s", op, sql)
	}
	if !strings.Contains(sql, "`tenant_join_suppliers`.`company_id` = ?") {
		t.Fatalf("expected %s join table tenant field to be injected, sql=%s", op, sql)
	}
	if strings.Contains(sql, " WHERE `company_id` = ?") || strings.Contains(sql, " AND `company_id` = ?") {
		t.Fatalf("expected %s to have no unqualified tenant field, sql=%s", op, sql)
	}
}

func containsVar(vars []any, target any) bool {
	for _, v := range vars {
		if v == target {
			return true
		}
	}
	return false
}
