package jsonutil_test

import (
	"errors"
	"testing"

	"github.com/kuangshp/gorm-plus/jsonutil"
	"gorm.io/datatypes"
)

func TestJSONConversionsPreserveText(t *testing.T) { // 双向转换保留完整 JSON 文本、空白及大整数和高精度小数。
	for _, test := range []struct {
		name string // 合法 JSON 的测试场景。
		text string // 转换后应原样保留的 JSON 文本。
	}{
		{"object", `{"color":"red","labels":["new","sale"]}`},
		{"array", `[1,true,null,{"中文":"红色"}]`},
		{"empty object", `{}`},
		{"empty array", `[]`},
		{"null", `null`},
		{"true", `true`},
		{"false", `false`},
		{"string", `"hello\n世界"`},
		{"empty string value", `""`},
		{"zero", `0`},
		{"negative", `-12`},
		{"large integer", `90071992547409931234567890`},
		{"precise decimal", `12.3456789012345678900`},
		{"large exponent", `1e1000`},
		{"nested numbers", `{"id":9007199254740993,"amount":12.34567890123456789}`},
		{"whitespace", " \n{ \"color\": \"red\", \"labels\": [] }\t\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value, err := jsonutil.ParseJSON(test.text)
			if err != nil || string(value) != test.text {
				t.Fatalf("ParseJSON: got %q, %v; want %q", value, err, test.text)
			}
			text, err := jsonutil.JSONString(value)
			if err != nil || text != test.text {
				t.Fatalf("JSONString: got %q, %v; want %q", text, err, test.text)
			}
		})
	}
}

func TestJSONConversionsRejectInvalidDocuments(t *testing.T) { // 双向校验拒绝残缺、非法转义和多个 JSON 文档，不返回无效内容。
	for _, text := range []string{
		" ", "\n\t", `plain text`, `{"color":}`, `{"color":"red"`,
		`{"color":"red",}`, `[1,]`, `{} []`, `null true`, `{} trailing`,
		`"\x41"`, `"unterminated`, "\"line\nfeed\"", `01`, `+1`, `.5`, `1.`, `NaN`, `Infinity`,
	} {
		t.Run(text, func(t *testing.T) {
			value, err := jsonutil.ParseJSON(text)
			if !errors.Is(err, jsonutil.ErrInvalidJSON) || value != nil {
				t.Fatalf("ParseJSON accepted invalid document: %q, %v", value, err)
			}
			result, err := jsonutil.JSONString(datatypes.JSON(text))
			if !errors.Is(err, jsonutil.ErrInvalidJSON) || result != "" {
				t.Fatalf("JSONString accepted invalid document: %q, %v", result, err)
			}
		})
	}
}

func TestJSONConversionsOptionalValues(t *testing.T) { // 可选空值和 JSON null 各自保留语义，空白字符串仍属于非法 JSON。
	value, err := jsonutil.ParseJSON("")
	if err != nil || value != nil {
		t.Fatalf("empty string: got %q, %v; want nil", value, err)
	}
	for _, value := range []datatypes.JSON{nil, {}} {
		text, err := jsonutil.JSONString(value)
		if err != nil || text != "" {
			t.Fatalf("empty JSON: got %q, %v; want empty string", text, err)
		}
	}
	value, err = jsonutil.ParseJSON("null")
	if err != nil || value == nil || string(value) != "null" {
		t.Fatalf("JSON null: got %q, %v; want non-nil JSON null", value, err)
	}
}
