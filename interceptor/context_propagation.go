package interceptor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
)

// ContextMetadataField 描述一个需要在 API 与 RPC context 之间透传的字段。
// 请使用 NewContextMetadataField 创建，以确保客户端和服务端使用相同的数据类型。
type ContextMetadataField struct {
	metadataKey string
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
