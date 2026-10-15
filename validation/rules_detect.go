package validation

import (
	"reflect"
	"strings"
	"sync"
)

type rulesCacheKey struct {
	typeOf  reflect.Type
	tagName string
}

var rulesCache sync.Map

// HasRules 判断结构体及未忽略的嵌套结构体是否包含有效 validate 标签。
func HasRules(value any) bool {
	return HasRulesForTag(value, "validate")
}

// HasRulesForTag 检查指定的校验标签，支持 binding 等自定义标签名。
func HasRulesForTag(value any, tagName string) bool {
	typeOf := indirectType(reflect.TypeOf(value))
	tagName = strings.TrimSpace(tagName)
	if typeOf == nil || typeOf.Kind() != reflect.Struct || tagName == "" {
		return false
	}

	cacheKey := rulesCacheKey{
		typeOf:  typeOf,
		tagName: tagName,
	}
	if cached, ok := rulesCache.Load(cacheKey); ok {
		return cached.(bool)
	}
	hasRules := typeHasRules(typeOf, tagName, make(map[reflect.Type]bool))
	rulesCache.Store(cacheKey, hasRules)
	return hasRules
}

func typeHasRules(typeOf reflect.Type, tagName string, visiting map[reflect.Type]bool) bool {
	typeOf = indirectType(typeOf)
	if typeOf == nil || typeOf.Kind() != reflect.Struct || visiting[typeOf] {
		return false
	}
	visiting[typeOf] = true
	defer delete(visiting, typeOf)

	for index := 0; index < typeOf.NumField(); index++ {
		field := typeOf.Field(index)
		if field.PkgPath != "" {
			continue
		}
		tag, exists := field.Tag.Lookup(tagName)
		tag = strings.TrimSpace(tag)
		if exists && tag == "-" {
			continue
		}
		if exists && tag != "" {
			return true
		}
		if typeHasRules(field.Type, tagName, visiting) {
			return true
		}
	}
	return false
}

func indirectType(typeOf reflect.Type) reflect.Type {
	for typeOf != nil && typeOf.Kind() == reflect.Pointer {
		typeOf = typeOf.Elem()
	}
	return typeOf
}
