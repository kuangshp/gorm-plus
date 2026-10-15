package plugin

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func isolationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if err := db.AutoMigrate(&tenantFieldTestModel{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&[]tenantFieldTestModel{
		{ID: 1, TenantID: 2002, Name: "foreign"},
		{ID: 2, TenantID: 1001, Name: "own-a"},
		{ID: 3, TenantID: 1001, Name: "own-b"},
	}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTenantORCannotBypassExistingCondition(t *testing.T) {
	queries := map[string]func(*gorm.DB) *gorm.DB{
		"later tenant OR": func(db *gorm.DB) *gorm.DB {
			return db.Where("tenant_id = ?", 1001).Or("tenant_id = ?", 2002)
		},
		"later business OR": func(db *gorm.DB) *gorm.DB {
			return db.Where("tenant_id = ?", 1001).Or("name = ?", "foreign")
		},
		"nested AND": func(db *gorm.DB) *gorm.DB {
			return db.Where(clause.And(
				clause.Eq{Column: "tenant_id", Value: 1001},
				clause.And(clause.Eq{Column: "name", Value: "own-a"}, clause.Or(clause.Eq{Column: "tenant_id", Value: 2002})),
			))
		},
		"named raw OR": func(db *gorm.DB) *gorm.DB {
			return db.Where("tenant_id = ?", 1001).Where("tenant_id = @tenant OR name = @name", map[string]any{"tenant": 2002, "name": "foreign"})
		},
		"tenant IN inside OR": func(db *gorm.DB) *gorm.DB {
			return db.Where("tenant_id = ?", 1001).Or(clause.IN{Column: "tenant_id", Values: []any{2002}})
		},
	}
	for policyName, policy := range map[string]DuplicateTenantPolicy{"skip": PolicySkip, "replace": PolicyReplace, "append": PolicyAppend} {
		for name, build := range queries {
			for _, operation := range []string{"Find", "Scan", "Update", "Delete"} {
				t.Run(policyName+"/"+name+"/"+operation, func(t *testing.T) {
					db := isolationTestDB(t)
					if err := RegisterTenant(db, TenantConfig[int64]{TenantField: "tenant_id", DuplicatePolicy: policy}); err != nil {
						t.Fatal(err)
					}
					ctx := WithTenantID(context.Background(), int64(1001))
					tx := build(db.WithContext(ctx).Model(&tenantFieldTestModel{}).Where("id > ?", 0))
					var rows []tenantFieldTestModel
					var result *gorm.DB
					switch operation {
					case "Find":
						result = tx.Find(&rows)
					case "Scan":
						result = tx.Scan(&rows)
					case "Update":
						result = tx.Update("name", "changed")
					case "Delete":
						result = tx.Delete(&tenantFieldTestModel{})
					}
					if result.Error == nil || !strings.Contains(result.Error.Error(), "OR") {
						t.Fatalf("unsafe OR not rejected: error=%v rows=%v", result.Error, rows)
					}
					var unchanged []tenantFieldTestModel
					expected := []tenantFieldTestModel{
						{ID: 1, TenantID: 2002, Name: "foreign"},
						{ID: 2, TenantID: 1001, Name: "own-a"},
						{ID: 3, TenantID: 1001, Name: "own-b"},
					}
					if err := db.Order("id ASC").Find(&unchanged).Error; err != nil || !reflect.DeepEqual(unchanged, expected) {
						t.Fatalf("rejected query changed rows: %v, %v", unchanged, err)
					}
				})
			}
		}
	}
}

func TestTenantBusinessORRemainsScoped(t *testing.T) {
	for name, build := range map[string]func(*gorm.DB) *gorm.DB{
		"top-level OR": func(db *gorm.DB) *gorm.DB { return db.Where("name = ?", "own-a").Or("name = ?", "foreign") },
		"grouped OR":   func(db *gorm.DB) *gorm.DB { return db.Where("name = ? OR name = ?", "own-a", "foreign") },
		"explicit tenant AND grouped business OR": func(db *gorm.DB) *gorm.DB {
			return db.Where("tenant_id = ?", 1001).Where(clause.Or(
				clause.Eq{Column: "name", Value: "own-a"},
				clause.Eq{Column: "name", Value: "foreign"},
			))
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := isolationTestDB(t)
			if err := RegisterTenant(db, TenantConfig[int64]{TenantField: "tenant_id"}); err != nil {
				t.Fatal(err)
			}
			ctx := WithTenantID(context.Background(), int64(1001))
			var rows []tenantFieldTestModel
			if err := build(db.WithContext(ctx)).Find(&rows).Error; err != nil || len(rows) != 1 || rows[0].ID != 2 {
				t.Fatalf("business OR escaped tenant scope: %v %v", rows, err)
			}
		})
	}
}

func TestDataPermissionSkipAndExcludedTablesOnScan(t *testing.T) {
	for _, mode := range []string{"skip", "excluded", "no permission context"} {
		t.Run(mode, func(t *testing.T) {
			db := isolationTestDB(t)
			cfg := DataPermissionConfig{}
			if mode == "excluded" {
				cfg.ExcludeTables = []string{"tenant_field_test_models"}
			}
			if err := RegisterDataPermission(db, cfg); err != nil {
				t.Fatal(err)
			}
			ctx := WithDataPermission(context.Background(), func(db *gorm.DB, _ string) { db.Where("tenant_id = ?", 1001) })
			if mode == "skip" {
				ctx = SkipDataPermission(ctx)
			} else if mode == "no permission context" {
				ctx = context.Background()
			}
			var rows []tenantFieldTestModel
			if err := db.WithContext(ctx).Model(&tenantFieldTestModel{}).Scan(&rows).Error; err != nil || len(rows) != 3 {
				t.Fatalf("Scan compatibility changed: %v %v", rows, err)
			}
		})
	}
}

func TestTenantAndDataPermissionComposeOnScan(t *testing.T) {
	for _, tenantFirst := range []bool{true, false} {
		name := "permission first"
		if tenantFirst {
			name = "tenant first"
		}
		t.Run(name, func(t *testing.T) {
			db := isolationTestDB(t)
			tenant := func() error { return RegisterTenant(db, TenantConfig[int64]{TenantField: "tenant_id"}) }
			permission := func() error { return RegisterDataPermission(db, DataPermissionConfig{}) }
			registrations := []func() error{permission, tenant}
			if tenantFirst {
				registrations = []func() error{tenant, permission}
			}
			for _, register := range registrations {
				if err := register(); err != nil {
					t.Fatal(err)
				}
			}
			ctx := WithTenantID(context.Background(), int64(1001))
			ctx = WithDataPermission(ctx, func(db *gorm.DB, _ string) { db.Where("id <= ?", 2) })
			var rows []tenantFieldTestModel
			if err := db.WithContext(ctx).Model(&tenantFieldTestModel{}).Where("name = ?", "foreign").Or("name = ?", "own-a").Scan(&rows).Error; err != nil || len(rows) != 1 || rows[0].ID != 2 {
				t.Fatalf("combined scopes failed: %v %v", rows, err)
			}
		})
	}
}

func TestDataPermissionFindAndScanTargets(t *testing.T) {
	db := isolationTestDB(t)
	if err := RegisterDataPermission(db, DataPermissionConfig{}); err != nil {
		t.Fatal(err)
	}
	ctx := WithDataPermission(context.Background(), func(db *gorm.DB, _ string) { db.Where("tenant_id = ?", 1001) })
	base := func() *gorm.DB { return db.WithContext(ctx).Model(&tenantFieldTestModel{}).Order("id ASC") }
	t.Run("Find slice", func(t *testing.T) {
		var rows []tenantFieldTestModel
		if err := base().Find(&rows).Error; err != nil || len(rows) != 2 || rows[0].ID != 2 || rows[1].ID != 3 {
			t.Fatalf("Find permissions: %v %v", rows, err)
		}
	})
	t.Run("Scan slice", func(t *testing.T) {
		var rows []tenantFieldTestModel
		if err := base().Scan(&rows).Error; err != nil || len(rows) != 2 || rows[0].ID != 2 || rows[1].ID != 3 {
			t.Fatalf("Scan slice permissions: %v %v", rows, err)
		}
	})
	t.Run("Scan struct", func(t *testing.T) {
		var row tenantFieldTestModel
		result := base().Scan(&row)
		if result.Error != nil || row.ID != 2 {
			t.Fatalf("Scan struct permissions: %v %v", row, result.Error)
		}
		if _, limited := result.Statement.Clauses["LIMIT"]; limited {
			t.Fatal("Scan struct unexpectedly added LIMIT")
		}
	})
	t.Run("Table Scan projection", func(t *testing.T) {
		var rows []struct {
			ID   int64
			Name string
		}
		result := db.WithContext(ctx).Table("tenant_field_test_models").Select("id, name").Order("id ASC").Scan(&rows)
		if result.Error != nil || len(rows) != 2 || rows[0].ID != 2 || rows[1].ID != 3 {
			t.Fatalf("Table projection permissions: %v %v", rows, result.Error)
		}
	})
	t.Run("Rows", func(t *testing.T) {
		rows, err := base().Select("id").Rows()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil || !reflect.DeepEqual(ids, []int64{2, 3}) {
			t.Fatalf("Rows permissions: %v %v", ids, err)
		}
	})
	t.Run("Row", func(t *testing.T) {
		var id int64
		if err := base().Select("id").Row().Scan(&id); err != nil || id != 2 {
			t.Fatalf("Row permissions: %d %v", id, err)
		}
	})
	t.Run("Count", func(t *testing.T) {
		var count int64
		if err := base().Count(&count).Error; err != nil || count != 2 {
			t.Fatalf("Count permissions: %d %v", count, err)
		}
	})
	t.Run("business OR", func(t *testing.T) {
		var rows []tenantFieldTestModel
		if err := base().Where("name = ?", "foreign").Or("name = ?", "own-a").Scan(&rows).Error; err != nil || len(rows) != 1 || rows[0].ID != 2 {
			t.Fatalf("permission OR escaped scope: %v %v", rows, err)
		}
	})
}
