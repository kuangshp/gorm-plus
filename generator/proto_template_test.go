package generator

import (
	"strings"
	"testing"
)

func TestProtoTemplateValidationIsEffective(t *testing.T) {
	data := ProtoTemplateData{
		ModelName:       "Site",
		TableComment:    "站点",
		ProtoPackage:    "site",
		BaseProtoImport: "proto/base.proto",
		ModelColumns: []ColumnInfo{
			{Name: "id", ParamName: "id", FieldType: "int64", Comment: "主键"},
			{Name: "site_code", ParamName: "siteCode", FieldType: "string", Type: "varchar(32)", Comment: "站点编码"},
			{Name: "currency", ParamName: "currency", FieldType: "string", Type: "char(3)", Comment: "币种"},
			{Name: "status", ParamName: "status", FieldType: "int64", Type: "tinyint", Comment: "状态：1、正常，2、禁用"},
			{Name: "last_login_at", ParamName: "lastLoginAt", FieldType: "int64", Type: "datetime", Comment: "最后登录时间"},
		},
		WritableColumns: []ColumnInfo{
			{Name: "site_code", ParamName: "siteCode", FieldType: "string", Type: "varchar(32)", Comment: "站点编码"},
			{Name: "site_name", ParamName: "siteName", FieldType: "string", Type: "varchar(100)", CanNull: true, Comment: "站点名称"},
			{Name: "currency", ParamName: "currency", FieldType: "string", Type: "char(3)", Comment: "币种"},
			{Name: "status", ParamName: "status", FieldType: "int64", Type: "tinyint", CanNull: true, Comment: "状态：1、正常，2、禁用"},
			{Name: "last_login_at", ParamName: "lastLoginAt", FieldType: "string", Type: "datetime", CanNull: true, Comment: "最后登录时间"},
			{Name: "birthday", ParamName: "birthday", FieldType: "string", Type: "date", CanNull: true, Comment: "生日"},
			{Name: "balance", ParamName: "balance", FieldType: "double", Type: "decimal(12,2)", CanNull: true, Comment: "余额"},
		},
	}

	generated, err := renderTemplate("template/proto_template.txt", data)
	if err != nil {
		t.Fatalf("渲染 proto_template.txt 失败: %v", err)
	}

	mustContain := []string{
		`import "proto/base.proto";`,
		`import "buf/validate/validate.proto";`,
		"int64 lastLoginAt = 5;",
		"optional string lastLoginAt = 5 [",
		"(buf.validate.field).string.(date_time_format) = true",
		"(buf.validate.field).string.(date_format) = true",
		"(buf.validate.field).double.finite = true",
		"(buf.validate.field).required = true",
		"(buf.validate.field).string.max_len = 32",
		"(buf.validate.field).string.max_len = 100",
		"(buf.validate.field).string.len = 3",
		"(buf.validate.field).int64 = {in: [1, 2]}",
		"PageRequest page = 1 [(buf.validate.field).required = true];",
		"int64 id = 1 [(buf.validate.field).int64.gt = 0];",
		"repeated int64 idList = 1 [(buf.validate.field).repeated = {min_items: 1, items: {int64: {gt: 0}}}]",
	}
	for _, want := range mustContain {
		if !strings.Contains(generated, want) {
			t.Errorf("生成的 Proto 缺少 %q\n--- generated ---\n%s", want, generated)
		}
	}

	createStart := strings.Index(generated, "message CreateSiteReq")
	modifyStart := strings.Index(generated, "message ModifySiteReq")
	if createStart < 0 || modifyStart < 0 || createStart >= modifyStart {
		t.Fatalf("无法定位 CreateSiteReq/ModifySiteReq\n%s", generated)
	}
	createMessage := generated[createStart:modifyStart]
	if strings.Contains(createMessage, "optional string siteName = 2 [\n    (buf.validate.field).required = true") {
		t.Errorf("可空字段 siteName 不应生成 required 校验\n%s", createMessage)
	}
}
