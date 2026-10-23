// Package decimaljson normalizes numeric JSON input without rounding its text.
package decimaljson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

var decimalText = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// Unmarshal accepts JSON numbers for string fields without extra tags,
// and numeric strings for integer and float fields.
// Generated DTOs call it with an alias that has no UnmarshalJSON method.
func Unmarshal(data []byte, target any) error {
	normalized, err := Normalize(data, target)
	if err != nil {
		return err
	}
	return json.Unmarshal(normalized, target)
}

// Normalize preserves numeric text and leaves unrelated fields unchanged.
func Normalize(data []byte, target any) ([]byte, error) {
	typ := reflect.TypeOf(target)
	if typ == nil || typ.Kind() != reflect.Pointer {
		return nil, fmt.Errorf("decimaljson: target must be a pointer")
	}
	return normalize(data, typ.Elem(), false, "request")
}

// PrepareRequest normalizes a JSON body before the framework's usual parser.
// The limit matches go-zero's 8 MiB JSON limit. Forms and empty bodies are untouched.
func PrepareRequest(r *http.Request, target any) error {
	if r.Body == nil || r.Body == http.NoBody || !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		return nil
	}
	const maxBodyLen = 8 << 20
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBodyLen+1))
	if err != nil {
		return err
	}
	if len(data) > maxBodyLen {
		return fmt.Errorf("decimaljson: JSON body exceeds 8 MiB")
	}
	if len(data) == 0 {
		return nil
	}
	normalized, err := Normalize(data, target)
	if err != nil {
		return err
	}
	normalized, err = prepareJSONValues(normalized, reflect.ValueOf(target))
	if err != nil {
		return err
	}
	if err := r.Body.Close(); err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(normalized))
	r.ContentLength = int64(len(normalized))
	return nil
}

func prepareJSONValues(data []byte, target reflect.Value) ([]byte, error) { // 为 go-zero 的 jsonvalue 字段保留原始 JSON，其余字段继续交给原解析器。
	for target.IsValid() && target.Kind() == reflect.Pointer {
		if target.IsNil() {
			return data, nil
		}
		target = target.Elem()
	}
	if !target.IsValid() || target.Kind() != reflect.Struct {
		return data, nil
	}
	var fields map[string]json.RawMessage
	// 仅对含标记的类型解析对象，普通请求不增加 JSON 重编码。
	if !hasJSONValues(target.Type()) {
		return data, nil
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if err := assignJSONValues(fields, target); err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func hasJSONValues(typ reflect.Type) bool { // 检查当前请求及嵌入字段中的 JSON 标记。
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Tag.Get("jsonvalue") == "true" {
			return true
		}
		if field.Anonymous && field.Type.Kind() == reflect.Struct && hasJSONValues(field.Type) {
			return true
		}
	}
	return false
}

func assignJSONValues(fields map[string]json.RawMessage, target reflect.Value) error { // 提前填充原生 JSON 字段，从后续解析的请求体移除对应键。
	typ := target.Type()
	for i := 0; i < typ.NumField(); i++ {
		field, value := typ.Field(i), target.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.Anonymous && name == "" && field.Type.Kind() == reflect.Struct {
			if err := assignJSONValues(fields, value); err != nil {
				return err
			}
			continue
		}
		if field.Tag.Get("jsonvalue") != "true" || name == "-" {
			continue
		}
		if !value.CanSet() || field.Type != reflect.TypeOf((*any)(nil)).Elem() {
			return fmt.Errorf("decimaljson: jsonvalue field %s must be an exported interface{}", field.Name)
		}
		if name == "" {
			name = field.Name
		}
		raw, exists := fields[name]
		if !exists {
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			value.SetZero()
		} else {
			value.Set(reflect.ValueOf(json.RawMessage(raw)))
		}
		delete(fields, name)
	}
	return nil
}

// decimalField identifies string fields explicitly intended for decimal input.
func decimalField(field reflect.StructField) bool {
	if field.Tag.Get("decimal") == "true" {
		return true
	}
	for _, rule := range strings.Split(field.Tag.Get("validate"), ",") {
		if rule == "decimal" {
			return true
		}
	}
	return false
}

// normalize walks nested objects and collections without converting through float64.
func normalize(data []byte, typ reflect.Type, decimal bool, path string) ([]byte, error) {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) {
		return data, nil
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	// Custom scalar types (time.Time, decimal.Decimal, etc.) own their JSON format.
	if reflect.PointerTo(typ).Implements(reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()) {
		return data, nil
	}
	if integerKind(typ.Kind()) {
		return normalizeInteger(data, typ, path)
	}
	// 普通字符串自动接收 JSON 数字，直接保留原文，避免浮点转换丢失精度。
	// 带 decimal 标签的字段仍沿用下面的既有小数格式检查。
	if typ.Kind() == reflect.String && !decimal {
		if len(data) > 0 && (data[0] == '-' || (data[0] >= '0' && data[0] <= '9')) && json.Valid(data) {
			return json.Marshal(string(data))
		}
		return data, nil
	}
	if (typ.Kind() == reflect.String && decimal) || typ.Kind() == reflect.Float32 || typ.Kind() == reflect.Float64 {
		text := string(data)
		if len(data) > 0 && data[0] == '"' {
			if err := json.Unmarshal(data, &text); err != nil {
				return nil, err
			}
		} else if !json.Valid(data) {
			return nil, fmt.Errorf("%s: invalid decimal", path)
		}
		if !decimalText.MatchString(text) {
			return nil, fmt.Errorf("%s: invalid decimal", path)
		}
		bits := 64
		if typ.Kind() == reflect.Float32 {
			bits = 32
		}
		number, err := strconv.ParseFloat(text, bits)
		if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("%s: invalid decimal", path)
		}
		if typ.Kind() == reflect.String {
			return json.Marshal(text)
		}
		// Float destinations must receive a valid JSON number, not Go-only syntax.
		if !json.Valid([]byte(text)) {
			return nil, fmt.Errorf("%s: invalid decimal", path)
		}
		return []byte(text), nil
	}
	switch typ.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, err
		}
		if err := normalizeFields(fields, typ, path); err != nil {
			return nil, err
		}
		return json.Marshal(fields)
	case reflect.Slice, reflect.Array:
		if typ.Elem().Kind() == reflect.Uint8 {
			return data, nil
		}
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, err
		}
		for i, item := range items {
			normalized, err := normalize(item, typ.Elem(), decimal, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return nil, err
			}
			items[i] = normalized
		}
		return json.Marshal(items)
	case reflect.Map:
		if typ.Key().Kind() != reflect.String {
			return data, nil
		}
		var items map[string]json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, err
		}
		for key, item := range items {
			normalized, err := normalize(item, typ.Elem(), decimal, path+"."+key)
			if err != nil {
				return nil, err
			}
			items[key] = normalized
		}
		return json.Marshal(items)
	}
	return data, nil
}

func integerKind(kind reflect.Kind) bool {
	return (kind >= reflect.Int && kind <= reflect.Int64) ||
		(kind >= reflect.Uint && kind <= reflect.Uint64)
}

// normalizeInteger parses at the destination width without losing precision to float64.
func normalizeInteger(data []byte, typ reflect.Type, path string) ([]byte, error) {
	text := string(data)
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &text); err != nil {
			return nil, fmt.Errorf("%s: invalid integer", path)
		}
	} else if !json.Valid(data) {
		return nil, fmt.Errorf("%s: invalid integer", path)
	}
	if typ.Kind() >= reflect.Int && typ.Kind() <= reflect.Int64 {
		number, err := strconv.ParseInt(text, 10, typ.Bits())
		if err != nil {
			return nil, fmt.Errorf("%s: invalid integer or out of range", path)
		}
		return []byte(strconv.FormatInt(number, 10)), nil
	}
	number, err := strconv.ParseUint(text, 10, typ.Bits())
	if err != nil {
		return nil, fmt.Errorf("%s: invalid unsigned integer or out of range", path)
	}
	return []byte(strconv.FormatUint(number, 10)), nil
}

// normalizeFields includes embedded request structs such as Modify/Create DTOs.
func normalizeFields(values map[string]json.RawMessage, typ reflect.Type, path string) error {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" && !field.Anonymous {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if field.Anonymous && name == "" && fieldType.Kind() == reflect.Struct {
			if err := normalizeFields(values, fieldType, path); err != nil {
				return err
			}
			continue
		}
		if name == "" {
			name = field.Name
		}
		raw, exists := values[name]
		if !exists {
			continue
		}
		normalized, err := normalize(raw, field.Type, decimalField(field), path+"."+name)
		if err != nil {
			return err
		}
		if (fieldType.Kind() == reflect.Float32 || fieldType.Kind() == reflect.Float64 || integerKind(fieldType.Kind())) &&
			!reflect.PointerTo(fieldType).Implements(reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()) {
			for _, option := range strings.Split(field.Tag.Get("json"), ",")[1:] {
				if option == "string" && !bytes.Equal(normalized, []byte("null")) {
					normalized, err = json.Marshal(string(normalized))
					if err != nil {
						return err
					}
				}
			}
		}
		values[name] = normalized
	}
	return nil
}
