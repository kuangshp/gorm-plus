package interceptor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/kuangshp/gorm-plus/plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	// ContextValuesMetadataKey 是任意 Context Key 透传使用的内部 metadata key。
	ContextValuesMetadataKey = "x-gorm-plus-context-values"
)

// ContextMetadataField 描述一个需要在 API 与 RPC context 之间透传的字段。
type ContextMetadataField struct {
	contextKey any
	wireKey    string
	valueKind  reflect.Kind
	extract    func(context.Context) (string, bool, error)
}

func newContextMetadataField[T any](contextKey any) ContextMetadataField {
	return ContextMetadataField{
		extract: func(ctx context.Context) (string, bool, error) {
			value := ctx.Value(contextKey)
			if value == nil {
				return "", false, nil
			}
			typed, ok := value.(T)
			if !ok {
				return "", false, fmt.Errorf("context 字段类型错误: got %T", value)
			}
			encoded, err := encodeMetadataValue(typed)
			return encoded, true, err
		},
	}
}

// PropagateContextKey 声明一个透传字段。
// key 支持字符串以及 plugin.CtxOperatorKey1～CtxOperatorKey10；RPC 服务端会恢复为同一个 key。
func PropagateContextKey[T any](key any) ContextMetadataField {
	wireKey, restoredKey, ok := contextKeyForWire(key)
	field := newContextMetadataField[T](key)
	if !ok {
		field.contextKey = nil
		return field
	}
	field.contextKey = restoredKey
	field.wireKey = wireKey
	field.valueKind = reflect.TypeOf((*T)(nil)).Elem().Kind()
	return field
}

// PropagateTenantID 声明 Tenant 插件使用的租户 ID 透传字段。
// 客户端通过 plugin.DefaultGetTenantID 读取，RPC 服务端通过 plugin.WithTenantID 恢复。
func PropagateTenantID[T comparable]() ContextMetadataField {
	field := ContextMetadataField{
		contextKey: struct{}{},
		wireKey:    "gormplus.tenant",
		valueKind:  reflect.TypeOf((*T)(nil)).Elem().Kind(),
	}
	field.extract = func(ctx context.Context) (string, bool, error) {
		value, ok := plugin.DefaultGetTenantID[T](ctx)
		if !ok {
			return "", false, nil
		}
		encoded, err := encodeMetadataValue(value)
		return encoded, true, err
	}
	return field
}

// PropagateContextValue 声明一个固定值透传字段。
// 客户端不读取当前 context，每次 RPC 调用都会将 value 写入 metadata，服务端恢复到指定 key。
func PropagateContextValue[T any](key any, value T) ContextMetadataField {
	wireKey, restoredKey, ok := contextKeyForWire(key)
	field := newContextMetadataField[T](key)
	if !ok {
		field.contextKey = nil
		return field
	}
	field.contextKey = restoredKey
	field.wireKey = wireKey
	field.valueKind = reflect.TypeOf((*T)(nil)).Elem().Kind()
	field.extract = func(context.Context) (string, bool, error) {
		encoded, err := encodeMetadataValue(value)
		return encoded, true, err
	}
	return field
}

// NewUnaryContextClientInterceptor 创建任意 Context Key 的客户端透传拦截器。
// fields 只描述实际需要透传的字段，未出现在 context 中的字段会自动跳过。
func NewUnaryContextClientInterceptor(fields ...ContextMetadataField) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		payload := make([]propagatedContextValue, 0, len(fields))
		for _, field := range fields {
			if field.contextKey == nil || field.wireKey == "" {
				return status.Error(codes.Internal, "Context Key 必须是字符串或 gorm-plus 内置操作人 key")
			}
			value, ok, err := field.extract(ctx)
			if err != nil {
				return status.Errorf(codes.Internal, "编码 context key %s 失败: %v", field.wireKey, err)
			}
			if ok {
				payload = append(payload, propagatedContextValue{Key: field.wireKey, Kind: field.valueKind.String(), Value: value})
			}
		}
		if len(payload) > 0 {
			encoded, err := encodeMetadataValue(payload)
			if err != nil {
				return status.Errorf(codes.Internal, "编码 context metadata 失败: %v", err)
			}
			ctx = metadata.AppendToOutgoingContext(ctx, ContextValuesMetadataKey, encoded)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// UnaryContextServerInterceptor 自动恢复 API 声明的任意字符串 Context Key，无需 RPC 端重复声明字段。
func UnaryContextServerInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	encoded, ok := firstMetadataValue(md, ContextValuesMetadataKey)
	if !ok {
		return handler(ctx, req)
	}
	values, err := decodeMetadataValue[[]propagatedContextValue](encoded)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "context metadata 无效: %v", err)
	}
	for _, item := range values {
		value, err := decodeContextValue(item.Kind, item.Value)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "context key %s 无效: %v", item.Key, err)
		}
		if item.Key == "gormplus.tenant" {
			ctx = withTenantIDValue(ctx, value)
			continue
		}
		contextKey, ok := contextKeyFromWire(item.Key)
		if !ok {
			return nil, status.Errorf(codes.InvalidArgument, "不支持的 context key %s", item.Key)
		}
		ctx = context.WithValue(ctx, contextKey, value)
	}
	return handler(ctx, req)
}

type propagatedContextValue struct {
	Key   string `json:"key"`
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func withTenantIDValue(ctx context.Context, value any) context.Context {
	switch tenantID := value.(type) {
	case string:
		return plugin.WithTenantID(ctx, tenantID)
	case int:
		return plugin.WithTenantID(ctx, tenantID)
	case int8:
		return plugin.WithTenantID(ctx, tenantID)
	case int16:
		return plugin.WithTenantID(ctx, tenantID)
	case int32:
		return plugin.WithTenantID(ctx, tenantID)
	case int64:
		return plugin.WithTenantID(ctx, tenantID)
	case uint:
		return plugin.WithTenantID(ctx, tenantID)
	case uint8:
		return plugin.WithTenantID(ctx, tenantID)
	case uint16:
		return plugin.WithTenantID(ctx, tenantID)
	case uint32:
		return plugin.WithTenantID(ctx, tenantID)
	case uint64:
		return plugin.WithTenantID(ctx, tenantID)
	default:
		return ctx
	}
}

func contextKeyForWire(key any) (string, any, bool) {
	switch key {
	case plugin.CtxOperatorKey1:
		return "gormplus.operator1", plugin.CtxOperatorKey1, true
	case plugin.CtxOperatorKey2:
		return "gormplus.operator2", plugin.CtxOperatorKey2, true
	case plugin.CtxOperatorKey3:
		return "gormplus.operator3", plugin.CtxOperatorKey3, true
	case plugin.CtxOperatorKey4:
		return "gormplus.operator4", plugin.CtxOperatorKey4, true
	case plugin.CtxOperatorKey5:
		return "gormplus.operator5", plugin.CtxOperatorKey5, true
	case plugin.CtxOperatorKey6:
		return "gormplus.operator6", plugin.CtxOperatorKey6, true
	case plugin.CtxOperatorKey7:
		return "gormplus.operator7", plugin.CtxOperatorKey7, true
	case plugin.CtxOperatorKey8:
		return "gormplus.operator8", plugin.CtxOperatorKey8, true
	case plugin.CtxOperatorKey9:
		return "gormplus.operator9", plugin.CtxOperatorKey9, true
	case plugin.CtxOperatorKey10:
		return "gormplus.operator10", plugin.CtxOperatorKey10, true
	}
	if stringKey, ok := key.(string); ok && stringKey != "" {
		return "string." + stringKey, stringKey, true
	}
	return "", nil, false
}

func contextKeyFromWire(wireKey string) (any, bool) {
	if strings.HasPrefix(wireKey, "string.") {
		key := strings.TrimPrefix(wireKey, "string.")
		return key, key != ""
	}
	_, key, ok := contextKeyForWireName(wireKey)
	return key, ok
}

func contextKeyForWireName(wireKey string) (string, any, bool) {
	operatorKeys := []any{
		plugin.CtxOperatorKey1, plugin.CtxOperatorKey2, plugin.CtxOperatorKey3,
		plugin.CtxOperatorKey4, plugin.CtxOperatorKey5, plugin.CtxOperatorKey6,
		plugin.CtxOperatorKey7, plugin.CtxOperatorKey8, plugin.CtxOperatorKey9,
		plugin.CtxOperatorKey10,
	}
	for index, key := range operatorKeys {
		name := fmt.Sprintf("gormplus.operator%d", index+1)
		if wireKey == name {
			return name, key, true
		}
	}
	return "", nil, false
}

func firstMetadataValue(md metadata.MD, key string) (string, bool) {
	values := md.Get(key)
	return first(values)
}

func first(values []string) (string, bool) {
	if len(values) == 0 || values[0] == "" {
		return "", false
	}
	return values[0], true
}

func encodeMetadataValue[T any](value T) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeMetadataValue[T any](encoded string) (T, error) {
	var value T
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, err
	}
	return value, nil
}

func decodeContextValue(kind, encoded string) (any, error) {
	switch kind {
	case reflect.String.String():
		return decodeMetadataValue[string](encoded)
	case reflect.Bool.String():
		return decodeMetadataValue[bool](encoded)
	case reflect.Int.String():
		return decodeMetadataValue[int](encoded)
	case reflect.Int8.String():
		return decodeMetadataValue[int8](encoded)
	case reflect.Int16.String():
		return decodeMetadataValue[int16](encoded)
	case reflect.Int32.String():
		return decodeMetadataValue[int32](encoded)
	case reflect.Int64.String():
		return decodeMetadataValue[int64](encoded)
	case reflect.Uint.String():
		return decodeMetadataValue[uint](encoded)
	case reflect.Uint8.String():
		return decodeMetadataValue[uint8](encoded)
	case reflect.Uint16.String():
		return decodeMetadataValue[uint16](encoded)
	case reflect.Uint32.String():
		return decodeMetadataValue[uint32](encoded)
	case reflect.Uint64.String():
		return decodeMetadataValue[uint64](encoded)
	case reflect.Float32.String():
		return decodeMetadataValue[float32](encoded)
	case reflect.Float64.String():
		return decodeMetadataValue[float64](encoded)
	default:
		return nil, fmt.Errorf("不支持的 context 值类型 %q", kind)
	}
}
