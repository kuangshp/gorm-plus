package query

import "reflect"

// ================== 工具函数 ==================
func isZeroVal(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	if canBeNil(rv.Kind()) && rv.IsNil() {
		return true
	}
	if val, ok := v.(interface{ IsZero() bool }); ok {
		return val.IsZero()
	}
	switch val := v.(type) {
	case int:
		return val == 0
	case int8:
		return val == 0
	case int16:
		return val == 0
	case int32:
		return val == 0
	case int64:
		return val == 0
	case uint:
		return val == 0
	case uint8:
		return val == 0
	case uint16:
		return val == 0
	case uint32:
		return val == 0
	case uint64:
		return val == 0
	case float32:
		return val == 0
	case float64:
		return val == 0
	case string:
		return val == ""
	case bool:
		return !val
	default:
		return rv.IsZero()
	}
}

func canBeNil(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	default:
		return false
	}
}
