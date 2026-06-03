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

	want := "omitempty,decimal"
	if rule != want {
		t.Fatalf("generateValidateRule() = %q, want %q", rule, want)
	}
}

func TestBuildApiColumnsAddsDecimalValidateRule(t *testing.T) {
	columns := buildApiColumns([]ColumnInfo{
		{Name: "amount", Type: "decimal(10,2)", CanNull: false},
	})

	if len(columns) != 1 {
		t.Fatalf("len(columns) = %d, want 1", len(columns))
	}
	want := "required,decimal"
	if columns[0].Validate != want {
		t.Fatalf("Validate = %q, want %q", columns[0].Validate, want)
	}
}

func TestGenerateValidateRuleAddsStringLengthRules(t *testing.T) {
	tests := []struct {
		name string
		col  ColumnInfo
		want string
	}{
		{
			name: "varchar max",
			col:  ColumnInfo{Name: "name", Type: "varchar(64)", CanNull: false},
			want: "required,max=64",
		},
		{
			name: "nullable varchar max",
			col:  ColumnInfo{Name: "nickname", Type: "varchar(32)", CanNull: true},
			want: "omitempty,max=32",
		},
		{
			name: "char len",
			col:  ColumnInfo{Name: "code", Type: "char(6)", CanNull: false},
			want: "required,len=6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := generateValidateRule(tt.col)
			if rule != tt.want {
				t.Fatalf("generateValidateRule() = %q, want %q", rule, tt.want)
			}
		})
	}
}

func TestGenerateValidateRuleAddsIntegerRules(t *testing.T) {
	tests := []struct {
		name string
		col  ColumnInfo
		want string
	}{
		{
			name: "id",
			col:  ColumnInfo{Name: "user_id", Type: "bigint", CanNull: false},
			want: "required,number,gte=1",
		},
		{
			name: "unsigned",
			col:  ColumnInfo{Name: "count", Type: "int unsigned", CanNull: false},
			want: "required,gte=0",
		},
		{
			name: "nullable unsigned",
			col:  ColumnInfo{Name: "count", Type: "int unsigned", CanNull: true},
			want: "omitempty,gte=0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := generateValidateRule(tt.col)
			if rule != tt.want {
				t.Fatalf("generateValidateRule() = %q, want %q", rule, tt.want)
			}
		})
	}
}

func TestGenerateValidateRuleAddsFormatRules(t *testing.T) {
	tests := []struct {
		name string
		col  ColumnInfo
		want string
	}{
		{
			name: "email",
			col:  ColumnInfo{Name: "email", Type: "varchar(128)", CanNull: false},
			want: "required,email,max=128",
		},
		{
			name: "url",
			col:  ColumnInfo{Name: "callback_url", Type: "varchar(255)", CanNull: true},
			want: "omitempty,url,max=255",
		},
		{
			name: "ip",
			col:  ColumnInfo{Name: "login_ip", Type: "varchar(64)", CanNull: true},
			want: "omitempty,ip,max=64",
		},
		{
			name: "json",
			col:  ColumnInfo{Name: "extra", Type: "json", CanNull: true},
			want: "omitempty,json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := generateValidateRule(tt.col)
			if rule != tt.want {
				t.Fatalf("generateValidateRule() = %q, want %q", rule, tt.want)
			}
		})
	}
}

func TestGenerateValidateRuleDoesNotMarkIntegerPrimaryKeyAsUUID(t *testing.T) {
	rule := generateValidateRule(ColumnInfo{
		Name:    "id",
		Type:    "bigint",
		IsKey:   true,
		CanNull: false,
	})

	want := "required,number,gte=1"
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
