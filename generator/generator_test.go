package generator

import "testing"

func TestGenerateValidateRuleBuildsOneofFromChineseEnumComment(t *testing.T) {
	rule := generateValidateRule(ColumnInfo{
		Name:    "status",
		Type:    "int",
		CanNull: false,
		Comment: "客户状态：1、潜在、2、试用、3、活跃、4、待定、5、流失",
	})

	want := "required,oneof=1 2 3 4 5"
	if rule != want {
		t.Fatalf("generateValidateRule() = %q, want %q", rule, want)
	}
}

func TestGenerateValidateRuleBuildsOneofFromLegacyEnumComment(t *testing.T) {
	rule := generateValidateRule(ColumnInfo{
		Name:      "is_enabled",
		FieldType: "int64",
		CanNull:   false,
		Comment:   "1是启用，2是禁用",
	})

	want := "required,oneof=1 2"
	if rule != want {
		t.Fatalf("generateValidateRule() = %q, want %q", rule, want)
	}
}

func TestGenerateValidateRuleAddsDecimalForDecimalSQLType(t *testing.T) {
	rule := generateValidateRule(ColumnInfo{
		Name:    "amount",
		Type:    "decimal(10,2)",
		CanNull: false,
	})

	want := "required,decimal"
	if rule != want {
		t.Fatalf("generateValidateRule() = %q, want %q", rule, want)
	}
}

func TestGenerateValidateRuleAddsDecimalForNullableDecimalSQLType(t *testing.T) {
	rule := generateValidateRule(ColumnInfo{
		Name:    "amount",
		Type:    "decimal(10,2)",
		CanNull: true,
	})

	want := "decimal"
	if rule != want {
		t.Fatalf("generateValidateRule() = %q, want %q", rule, want)
	}
}

func TestBuildRepoDataPrefersAutoIncrementPrimaryKey(t *testing.T) {
	data := buildRepoData([]ColumnInfo{
		{Name: "id", Type: "int", IsKey: true, Extra: "auto_increment"},
		{Name: "biz_type", Type: "varchar(64)", IsKey: true},
		{Name: "counter", Type: "bigint"},
	}, "Sequence", "github.com/example/app", "dal/dao", "dal/model", "sequence")

	if data.PrimaryKeyField != "ID" {
		t.Fatalf("PrimaryKeyField = %q, want %q", data.PrimaryKeyField, "ID")
	}
	if data.PrimaryKeyColumn != "id" {
		t.Fatalf("PrimaryKeyColumn = %q, want %q", data.PrimaryKeyColumn, "id")
	}
}

func TestBuildRepoDataUsesFirstPrimaryKeyWhenNoAutoIncrement(t *testing.T) {
	data := buildRepoData([]ColumnInfo{
		{Name: "tenant_id", Type: "bigint", IsKey: true},
		{Name: "biz_type", Type: "varchar(64)", IsKey: true},
	}, "Sequence", "github.com/example/app", "dal/dao", "dal/model", "sequence")

	if data.PrimaryKeyField != "TenantID" {
		t.Fatalf("PrimaryKeyField = %q, want %q", data.PrimaryKeyField, "TenantID")
	}
	if data.PrimaryKeyColumn != "tenant_id" {
		t.Fatalf("PrimaryKeyColumn = %q, want %q", data.PrimaryKeyColumn, "tenant_id")
	}
}
