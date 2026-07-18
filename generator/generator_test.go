package generator

import (
	"go/format"
	"reflect"
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

func TestTimeTypesUseStringForAPIAndInt64ForProto(t *testing.T) {
	if got := getGoTypeForApiDto("datetime"); got != "string" {
		t.Fatalf("getGoTypeForApiDto(datetime) = %q, want string", got)
	}
	if got := getProtoType("datetime"); got != "int64" {
		t.Fatalf("getProtoType(datetime) = %q, want int64", got)
	}
	if got := getProtoType("timestamp"); got != "int64" {
		t.Fatalf("getProtoType(timestamp) = %q, want int64", got)
	}
}

func TestProtoMapperTemplatesGenerateValidGo(t *testing.T) {
	data := ProtoMapperTemplateData{
		ModelName: "SysUser", ModelNameLower: "sysUser", TableComment: "系统用户",
		Package: "example.com/project", ModelPkgPath: "internal/dal/model/entity", ModelPkgName: "entity",
		ProtoPkgPath: "apps/rpc/pb", ProtoPkgName: "pb", APITypesPkgPath: "apps/admin/internal/types",
		HasTimeField: true, HasDecimalField: true, HasFloatField: true,
		Columns: []ColumnInfo{
			{Name: "id", FieldName: "ID", ParamName: "Id"},
			{Name: "started_at", FieldName: "StartedAt", ParamName: "StartedAt", IsTimeType: true},
			{Name: "amount", FieldName: "Amount", ParamName: "Amount", IsDecimalType: true},
			{Name: "ratio", FieldName: "Ratio", ParamName: "Ratio", IsFloatType: true},
		},
		WritableColumns: []ColumnInfo{
			{Name: "started_at", FieldName: "StartedAt", ParamName: "StartedAt", IsTimeType: true},
			{Name: "amount", FieldName: "Amount", ParamName: "Amount", IsDecimalType: true},
			{Name: "ratio", FieldName: "Ratio", ParamName: "Ratio", IsFloatType: true},
		},
	}
	for _, templatePath := range []string{
		"template/entity_proto_mapper_template.txt",
		"template/api_proto_mapper_template.txt",
	} {
		generated, err := renderTemplate(templatePath, data)
		if err != nil {
			t.Fatalf("renderTemplate(%s): %v", templatePath, err)
		}
		if _, err := format.Source([]byte(generated)); err != nil {
			t.Fatalf("generated mapper from %s is invalid Go: %v\n%s", templatePath, err, generated)
		}
	}
}

func TestRenderProtoTemplateProvidesApiEquivalentCRUDMethods(t *testing.T) {
	got, err := renderTemplate("template/proto_template.txt", ProtoTemplateData{
		TableName: "sys_user", ModelName: "SysUser", TableComment: "系统用户",
		ProtoPackage: "rpc",
		ModelColumns: []ColumnInfo{
			{Name: "id", FieldName: "ID", ParamName: "id", FieldType: "int64", Comment: "主键"},
			{Name: "site_code", FieldName: "SiteCode", ParamName: "siteCode", FieldType: "string", Comment: "站点编码"},
			{Name: "balance", FieldName: "Balance", ParamName: "balance", FieldType: "double", Comment: "余额"},
			{Name: "created_at", FieldName: "CreatedAt", ParamName: "createdAt", FieldType: "string", Comment: "创建时间"},
		},
		WritableColumns: []ColumnInfo{
			{Name: "site_code", FieldName: "SiteCode", ParamName: "siteCode", FieldType: "string", Comment: "站点编码"},
			{Name: "balance", FieldName: "Balance", ParamName: "balance", FieldType: "double", Comment: "余额"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	mustContain := []string{
		`syntax = "proto3";`,
		"package rpc;",
		`import "base.proto";`,
		"// CreateSysUserReq 创建系统用户请求。",
		"message CreateSysUserReq",
		"string siteCode = 1;",
		"double balance = 2;",
		"PageRequest page = 1;",
		"PageInfo pageInfo = 1;",
		"// GetSysUserPage 分页查询系统用户。",
		"rpc CreateSysUser(CreateSysUserReq) returns (EmptyResponse)",
		"rpc DeleteSysUserById(SysUserIdReq) returns (EmptyResponse)",
		"rpc BatchDeleteSysUserByIdList(BatchDeleteSysUserByIdListReq) returns (EmptyResponse)",
		"rpc ModifySysUserById(ModifySysUserReq) returns (EmptyResponse)",
		"rpc GetSysUserPage(PageSysUserReq)",
		"rpc GetSysUserList(EmptyRequest)",
		"rpc GetSysUserDetail(SysUserIdReq) returns (SysUserDetailResponse)",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Fatalf("generated proto missing %q\n%s", want, got)
		}
	}
}

func TestBaseAndBusinessProtoUseSamePackage(t *testing.T) {
	pkg := getProtoPackage("github.com/example/user-rpc")
	if pkg != "user_rpc" {
		t.Fatalf("getProtoPackage() = %q, want %q", pkg, "user_rpc")
	}

	got, err := renderTemplate("template/base_proto_template.txt", ProtoTemplateData{ProtoPackage: pkg})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"package user_rpc;", `option go_package = "./user_rpc";`, "message EmptyRequest {}", "message EmptyResponse {}"} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated base proto missing %q\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"BaseResponse", "OperationResponse"} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("generated base proto should not contain %q\n%s", unwanted, got)
		}
	}
}

func TestFilterExcludedTables(t *testing.T) {
	tables := []string{"sys_user", "sys_config", "sys_dict", "biz_order"}
	got := filterExcludedTables(tables, []string{" Sys_Config ", "`SYS_DICT`", ""})
	want := []string{"sys_user", "biz_order"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filterExcludedTables() = %#v, want %#v", got, want)
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
		"Create(ctx context.Context, m *model.SequenceEntity, opts ...gormplus.CreateOption) error",
		"CreateBatch(ctx context.Context, m []*model.SequenceEntity, opts ...gormplus.CreateOption) error",
		"if len(m) == 0",
		"createOpts := gormplus.ResolveCreateOptions(opts)",
		"tx = tx.Omit(createOpts.OmitFields...)",
		"tx = tx.Clauses(createOpts.Clauses...)",
		"if len(q.Clauses) > 0",
		"tx = tx.Clauses(q.Clauses...)",
		"if len(deleteOpts.Clauses) > 0",
		"tx = tx.Clauses(deleteOpts.Clauses...)",
		"UpdateById(ctx context.Context, sequenceId string, opts ...gormplus.UpdateOption) error",
		"UpdateByWrapper(ctx context.Context, fn func(gormplus.IGenWrapper[dao.ISequenceEntityDo]), opts ...gormplus.UpdateOption) error",
		"updateOpts := gormplus.ResolveUpdateOptions(opts)",
		"if len(updateOpts.Columns) == 0",
		"if len(data) == 0",
		"tx = tx.Clauses(updateOpts.Clauses...)",
		"UpdateSimple(updateOpts.Columns...)",
		"UpdateMapById(ctx context.Context, sequenceId string, data map[string]any) error",
		"DeleteById(ctx context.Context, sequenceId string, opts ...gormplus.DeleteOption) error",
		"if len(sequenceIds) == 0",
		"FindById(ctx context.Context, sequenceId string, query ...gormplus.QueryOption)",
		"tx = tx.Select(opt.Select...)",
		"tx = tx.Omit(opt.OmitFields...)",
		"dao.SequenceEntity.BizType.Eq(sequenceId)",
		`gormplus.BuildArgs("biz_type", sequenceId)`,
		"deleteOpts := gormplus.ResolveDeleteOptions(opts)",
		"if deleteOpts.Physical",
		"baseTx = baseTx.Clauses(deleteOpts.Clauses...)",
		"baseTx = baseTx.Clauses(opt.Clauses...)",
		"tx = tx.Unscoped()",
		"if q.Unscoped",
		"if opt.Unscoped",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Fatalf("generated repository missing %q", want)
		}
	}

	mustNotContain := []string{
		"Create(ctx context.Context, m *model.SequenceEntity, omitFields ...field.Expr) error",
		"gorm.io/gen/field",
		"columns ...field.AssignExpr",
		"UpdateByIdWithOption",
		"DeleteById(ctx context.Context, sequenceId int64) error",
		`gormplus.BuildArgs("id", sequenceId)`,
		"PhysicalDeleteById(",
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
		"CreateTx(ctx context.Context, tx *dao.Query, m *model.SequenceEntity, opts ...gormplus.CreateOption) error",
		"CreateBatchTx(ctx context.Context, tx *dao.Query, m []*model.SequenceEntity, opts ...gormplus.CreateOption) error",
		"DeleteById(ctx context.Context, id int64, bizType string, opts ...gormplus.DeleteOption) error",
		"DeleteByIdList(ctx context.Context, sequenceIds []SequencePrimaryKey, opts ...gormplus.DeleteOption) error",
		"FindById(ctx context.Context, id int64, bizType string, query ...gormplus.QueryOption)",
		"dao.SequenceEntity.ID.Eq(id)",
		"dao.SequenceEntity.BizType.Eq(bizType)",
		`gormplus.BuildArgs("id", id, "biz_type", bizType)`,
		"deleteOpts := gormplus.ResolveDeleteOptions(opts)",
		"if deleteOpts.Physical",
		"tx = tx.Unscoped()",
		"if q.Unscoped",
		"if opt.Unscoped",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Fatalf("generated repository missing %q", want)
		}
	}

	mustNotContain := []string{
		"Where(dao.SequenceEntity.ID.Eq(sequenceId))",
		`gormplus.BuildArgs("id", sequenceId)`,
		"PhysicalDeleteById(",
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
