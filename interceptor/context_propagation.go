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
	// TenantIDMetadataKey 是租户 ID 的默认 gRPC metadata key。
	TenantIDMetadataKey = "x-gorm-plus-tenant-id"
	// OperatorIDMetadataKey 是操作人 ID 的默认 gRPC metadata key。
	OperatorIDMetadataKey = "x-gorm-plus-operator-id"
	// ContextValuesMetadataKey 是任意 Context Key 透传使用的内部 metadata key。
	ContextValuesMetadataKey = "x-gorm-plus-context-values"
)

var (
	defaultInt64ClientContextPropagation = UnaryContextPropagationClientInterceptor(DefaultInt64ContextPropagationConfig())
	defaultInt64ServerContextPropagation = UnaryContextPropagationServerInterceptor(DefaultInt64ContextPropagationConfig())
)

// ContextMetadataField 描述一个需要在 API 与 RPC context 之间透传的字段。
// 请使用 NewContextMetadataField 创建，以确保客户端和服务端使用相同的数据类型。
type ContextMetadataField struct {
	metadataKey string
	contextKey  any
	wireKey     string
	valueKind   reflect.Kind
	extract     func(context.Context) (string, bool, error)
	inject      func(context.Context, string) (context.Context, error)
}

// NewContextMetadataField 创建一个类型安全的 context metadata 字段。
// contextKey 通常使用 gormplus.CtxContextKey1～CtxContextKey10。
func NewContextMetadataField[T any](metadataKey string, contextKey any) ContextMetadataField {
	key := normalizeMetadataKey(metadataKey)
	return ContextMetadataField{
		metadataKey: key,
		extract: func(ctx context.Context) (string, bool, error) {
			value := ctx.Value(contextKey)
			if value == nil {
				return "", false, nil
			}
			typed, ok := value.(T)
			if !ok {
				return "", false, fmt.Errorf("context 字段 %s 类型错误: got %T", key, value)
			}
			encoded, err := encodeMetadataValue(typed)
			return encoded, true, err
		},
		inject: func(ctx context.Context, encoded string) (context.Context, error) {
			value, err := decodeMetadataValue[T](encoded)
			if err != nil {
				return ctx, err
			}
			return context.WithValue(ctx, contextKey, value), nil
		},
	}
}

// PropagateContextKey 声明一个透传字段。
// key 支持字符串以及 plugin.CtxOperatorKey1～CtxOperatorKey10；RPC 服务端会恢复为同一个 key。
func PropagateContextKey[T any](key any) ContextMetadataField {
	wireKey, restoredKey, ok := contextKeyForWire(key)
	field := NewContextMetadataField[T](contextKeyMetadataName(wireKey), key)
	if !ok {
		field.contextKey = nil
		return field
	}
	field.contextKey = restoredKey
	field.wireKey = wireKey
	field.valueKind = reflect.TypeOf((*T)(nil)).Elem().Kind()
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

// ContextPropagationConfig 配置 API → RPC 的 context 透传。
type ContextPropagationConfig[T comparable] struct {
	// TenantMetadataKey 是租户 ID 的 metadata key；为空时使用 TenantIDMetadataKey。
	TenantMetadataKey string
	// RequireTenant 为 true 时，缺少租户 ID 会直接返回 Unauthenticated。
	RequireTenant bool
	// Fields 是操作人、操作人姓名、部门 ID 等扩展字段。
	Fields []ContextMetadataField
}

// DefaultInt64ContextPropagationConfig 返回 int64 租户 ID + int64 操作人 ID 的默认配置。
// 操作人 ID 从 plugin.CtxOperatorKey1 读取并在 RPC 服务端恢复到同一个 key。
func DefaultInt64ContextPropagationConfig() ContextPropagationConfig[int64] {
	return ContextPropagationConfig[int64]{
		TenantMetadataKey: TenantIDMetadataKey,
		Fields: []ContextMetadataField{
			NewContextMetadataField[int64](OperatorIDMetadataKey, plugin.CtxOperatorKey1),
		},
	}
}

// UnaryInt64ContextPropagationClientInterceptor 是 int64 租户 ID + int64 操作人 ID 的零配置客户端拦截器。
func UnaryInt64ContextPropagationClientInterceptor(
	ctx context.Context,
	method string,
	req, reply any,
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	return defaultInt64ClientContextPropagation(ctx, method, req, reply, cc, invoker, opts...)
}

// UnaryInt64ContextPropagationServerInterceptor 是 int64 租户 ID + int64 操作人 ID 的零配置服务端拦截器。
// 租户和操作人彼此独立，缺少任意一项都会自动跳过。
func UnaryInt64ContextPropagationServerInterceptor(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	return defaultInt64ServerContextPropagation(ctx, req, info, handler)
}

// UnaryContextPropagationClientInterceptor 将 API context 中的租户及操作人信息写入 outgoing metadata。
func UnaryContextPropagationClientInterceptor[T comparable](cfg ContextPropagationConfig[T]) grpc.UnaryClientInterceptor {
	cfg = normalizePropagationConfig(cfg)
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if tenantID, ok := plugin.DefaultGetTenantID[T](ctx); ok {
			encoded, err := encodeMetadataValue(tenantID)
			if err != nil {
				return status.Errorf(codes.Internal, "编码租户 ID 失败: %v", err)
			}
			ctx = metadata.AppendToOutgoingContext(ctx, cfg.TenantMetadataKey, encoded)
		} else if cfg.RequireTenant {
			return status.Error(codes.Unauthenticated, "缺少租户 ID")
		}

		for _, field := range cfg.Fields {
			encoded, ok, err := field.extract(ctx)
			if err != nil {
				return status.Errorf(codes.Internal, "编码 metadata %s 失败: %v", field.metadataKey, err)
			}
			if ok {
				ctx = metadata.AppendToOutgoingContext(ctx, field.metadataKey, encoded)
			}
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// UnaryContextPropagationServerInterceptor 从 incoming metadata 恢复租户及操作人 context。
// 恢复后的 ctx 会传给业务 Logic，数据库操作需继续使用 db.WithContext(ctx)。
func UnaryContextPropagationServerInterceptor[T comparable](cfg ContextPropagationConfig[T]) grpc.UnaryServerInterceptor {
	cfg = normalizePropagationConfig(cfg)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		if value, ok := firstMetadataValue(md, cfg.TenantMetadataKey); ok {
			tenantID, err := decodeMetadataValue[T](value)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "租户 ID metadata 无效: %v", err)
			}
			ctx = plugin.WithTenantID(ctx, tenantID)
		} else if cfg.RequireTenant {
			return nil, status.Error(codes.Unauthenticated, "缺少租户 ID")
		}

		for _, field := range cfg.Fields {
			value, ok := firstMetadataValue(md, field.metadataKey)
			if !ok {
				continue
			}
			var err error
			ctx, err = field.inject(ctx, value)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "metadata %s 无效: %v", field.metadataKey, err)
			}
		}
		return handler(ctx, req)
	}
}

func normalizePropagationConfig[T comparable](cfg ContextPropagationConfig[T]) ContextPropagationConfig[T] {
	if strings.TrimSpace(cfg.TenantMetadataKey) == "" {
		cfg.TenantMetadataKey = TenantIDMetadataKey
	} else {
		cfg.TenantMetadataKey = normalizeMetadataKey(cfg.TenantMetadataKey)
	}
	return cfg
}

func normalizeMetadataKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

func contextKeyMetadataName(key string) string {
	key = normalizeMetadataKey(key)
	var builder strings.Builder
	builder.WriteString("x-gorm-plus-context-")
	for _, char := range key {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '-', char == '_', char == '.':
			builder.WriteRune(char)
		default:
			builder.WriteByte('-')
		}
	}
	return strings.TrimRight(builder.String(), "-")
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
