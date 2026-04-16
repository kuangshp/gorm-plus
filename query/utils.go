package query

import (
	"fmt"
	"reflect"
	"time"
)

// ================== 值判断工具 ==================

// isZeroVal 判断值是否为零值。
// 零值规则：数值=0，字符串=""，bool=false，指针/slice/map=nil，time.Time.IsZero()=true。
func isZeroVal(v any) bool {
	if v == nil {
		return true
	}
	if t, ok := v.(time.Time); ok {
		return t.IsZero()
	}
	if t, ok := v.(*time.Time); ok {
		return t == nil || t.IsZero()
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return rv.IsNil()
	default:
		return rv.IsZero()
	}
}

// isNilVal 仅判断是否为 nil，不关心零值。
// 用于区分"不传"（nil）和"传了 0/空字符串"的场景。
func isNilVal(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return rv.IsNil()
	default:
		panic("unhandled default case")
	}
	return false
}

// isEmptyVal 判断 slice/map 是否为 nil 或长度为 0。
func isEmptyVal(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map:
		return rv.IsNil() || rv.Len() == 0
	default:
		panic("unhandled default case")
	}
	return false
}

// derefVal 解引用指针，返回指针指向的实际值，非指针原样返回。
func derefVal(v any) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr && !rv.IsNil() {
		return rv.Elem().Interface()
	}
	return v
}

// resolveSelects 将 []any 类型的 selects 转换为 gorm db.Select 可接受的参数。
// 支持三种元素类型混用：string、field.Expr（gorm-gen 字段对象）、fmt.Stringer（CaseWhenBuilder）。
func resolveSelects(selects []any) any {
	strs := make([]string, 0, len(selects))
	for _, s := range selects {
		switch v := s.(type) {
		case string:
			strs = append(strs, v)
		case fmt.Stringer:
			strs = append(strs, v.String())
		default:
			strs = append(strs, fmt.Sprintf("%v", v))
		}
	}
	return strs
}
