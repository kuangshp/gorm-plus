package generator

import (
	"go/format"
	"strings"
	"testing"
)

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
	if data.PrimaryKeyType != "int64" {
		t.Fatalf("PrimaryKeyType = %q, want %q", data.PrimaryKeyType, "int64")
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
	if data.PrimaryKeyType != "int64" {
		t.Fatalf("PrimaryKeyType = %q, want %q", data.PrimaryKeyType, "int64")
	}
}

func TestBuildRepoDataUsesStringPrimaryKeyType(t *testing.T) {
	data := buildRepoData([]ColumnInfo{
		{Name: "biz_type", Type: "varchar(64)", IsKey: true},
		{Name: "counter", Type: "bigint"},
	}, "Sequence", "github.com/example/app", "dal/dao", "dal/model", "sequence")

	if data.PrimaryKeyField != "BizType" {
		t.Fatalf("PrimaryKeyField = %q, want %q", data.PrimaryKeyField, "BizType")
	}
	if data.PrimaryKeyColumn != "biz_type" {
		t.Fatalf("PrimaryKeyColumn = %q, want %q", data.PrimaryKeyColumn, "biz_type")
	}
	if data.PrimaryKeyType != "string" {
		t.Fatalf("PrimaryKeyType = %q, want %q", data.PrimaryKeyType, "string")
	}
}

func TestGenerateRepositoryFileUsesPrimaryKeyTypeAndColumn(t *testing.T) {
	got, err := generateRepositoryFile([]ColumnInfo{
		{Name: "biz_type", Type: "varchar(64)", IsKey: true},
		{Name: "counter", Type: "bigint"},
	}, "Sequence", "github.com/example/app", "dal/dao", "dal/model", "template/repository_gen_template.txt", "sequence")
	if err != nil {
		t.Fatal(err)
	}
	assertGeneratedGoFormats(t, got)

	mustContain := []string{
		"DeleteById(ctx context.Context, sequenceId string) error",
		"FindById(ctx context.Context, sequenceId string, query ...gormplus.QueryOption)",
		"dao.SequenceEntity.BizType.Eq(sequenceId)",
		`gormplus.BuildArgs("biz_type", sequenceId)`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Fatalf("generated repository missing %q", want)
		}
	}

	mustNotContain := []string{
		"DeleteById(ctx context.Context, sequenceId int64) error",
		`gormplus.BuildArgs("id", sequenceId)`,
	}
	for _, unwanted := range mustNotContain {
		if strings.Contains(got, unwanted) {
			t.Fatalf("generated repository contains %q", unwanted)
		}
	}
}

func TestGenerateRepositoryFileUsesCompositePrimaryKeys(t *testing.T) {
	got, err := generateRepositoryFile([]ColumnInfo{
		{Name: "id", Type: "int", IsKey: true, Extra: "auto_increment"},
		{Name: "biz_type", Type: "varchar(64)", IsKey: true},
		{Name: "counter", Type: "bigint"},
	}, "Sequence", "github.com/example/app", "dal/dao", "dal/model", "template/repository_gen_template.txt", "sequence")
	if err != nil {
		t.Fatal(err)
	}
	assertGeneratedGoFormats(t, got)

	mustContain := []string{
		"type SequencePrimaryKey struct",
		"ID int64 `json:\"id\"`",
		"BizType string `json:\"biz_type\"`",
		"DeleteById(ctx context.Context, id int64, bizType string) error",
		"DeleteByIdList(ctx context.Context, sequenceIds []SequencePrimaryKey) error",
		"FindById(ctx context.Context, id int64, bizType string, query ...gormplus.QueryOption)",
		"dao.SequenceEntity.ID.Eq(id)",
		"dao.SequenceEntity.BizType.Eq(bizType)",
		`gormplus.BuildArgs("id", id, "biz_type", bizType)`,
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Fatalf("generated repository missing %q", want)
		}
	}

	mustNotContain := []string{
		"Where(dao.SequenceEntity.ID.Eq(sequenceId))",
		`gormplus.BuildArgs("id", sequenceId)`,
	}
	for _, unwanted := range mustNotContain {
		if strings.Contains(got, unwanted) {
			t.Fatalf("generated repository contains %q", unwanted)
		}
	}
}

func assertGeneratedGoFormats(t *testing.T, src string) {
	t.Helper()
	if _, err := format.Source([]byte(src)); err != nil {
		t.Fatalf("generated Go code is not formattable: %v\n%s", err, src)
	}
}
