// Package jsonutil 提供 JSON 字符串与 GORM 数据库 JSON 类型之间的双向转换。
package jsonutil

import (
	"encoding/json"
	"errors"

	"gorm.io/datatypes"
)

// ErrInvalidJSON 表示非空内容不是完整、合法的 JSON 文档。
var ErrInvalidJSON = errors.New("jsonutil: invalid JSON")

// ParseJSON 校验 JSON 字符串并将其转换为数据库 JSON 类型。
// 空字符串返回 nil，可用于允许 SQL NULL 的可选字段。
// 非空内容必须是完整、合法的 JSON；转换时保留原始空白和数字文本，不重复编码。
func ParseJSON(value string) (datatypes.JSON, error) { // 校验 JSON 字符串并转换为数据库 JSON，空字符串表示可选空值。
	if value == "" {
		return nil, nil
	}
	data := datatypes.JSON(value)
	if !json.Valid(data) {
		return nil, ErrInvalidJSON
	}
	return data, nil
}

// JSONString 校验数据库 JSON 内容并返回原始字符串。
// nil 或零长度内容返回空字符串；非空内容必须是完整、合法的 JSON。
// 非法内容返回错误，不会直接传递给调用方。
func JSONString(value datatypes.JSON) (string, error) { // 校验数据库 JSON 后返回原始字符串，空值返回空字符串。
	if len(value) == 0 {
		return "", nil
	}
	if !json.Valid(value) {
		return "", ErrInvalidJSON
	}
	return string(value), nil
}
