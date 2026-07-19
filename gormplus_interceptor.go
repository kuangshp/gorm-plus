package gormplus

import (
	"context"

	"github.com/kuangshp/gorm-plus/interceptor"
	"google.golang.org/grpc"
)

// UnaryValidationInterceptor 在 gRPC 业务 Handler 执行前统一执行 Protovalidate 参数校验。
var UnaryValidationInterceptor = interceptor.UnaryValidationInterceptor

type ValidationInterceptorOption = interceptor.ValidationInterceptorOption

var WithValidationMessages = interceptor.WithValidationMessages

func NewUnaryValidationInterceptor(options ...ValidationInterceptorOption) grpc.UnaryServerInterceptor {
	return interceptor.NewUnaryValidationInterceptor(options...)
}

const (
	TenantIDMetadataKey   = interceptor.TenantIDMetadataKey
	OperatorIDMetadataKey = interceptor.OperatorIDMetadataKey
)

type ContextMetadataField = interceptor.ContextMetadataField

// ContextPropagationConfig 配置 API → RPC 的 context 透传。
// 根包使用独立结构体，避免依赖泛型类型别名的编译器版本限制。
type ContextPropagationConfig[T comparable] struct {
	TenantMetadataKey string
	RequireTenant     bool
	Fields            []ContextMetadataField
}

func NewContextMetadataField[T any](metadataKey string, contextKey any) ContextMetadataField {
	return interceptor.NewContextMetadataField[T](metadataKey, contextKey)
}

// PropagateContextKey 声明一个使用字符串 key 的透传字段。
func PropagateContextKey[T any](key any) ContextMetadataField {
	return interceptor.PropagateContextKey[T](key)
}

// PropagateContextValue 声明一个固定值透传字段，不读取当前 context。
func PropagateContextValue[T any](key any, value T) ContextMetadataField {
	return interceptor.PropagateContextValue(key, value)
}

// NewUnaryContextClientInterceptor 创建任意 Context Key 的客户端透传拦截器。
func NewUnaryContextClientInterceptor(fields ...ContextMetadataField) grpc.UnaryClientInterceptor {
	return interceptor.NewUnaryContextClientInterceptor(fields...)
}

// UnaryContextServerInterceptor 自动恢复 API 声明的任意字符串 Context Key。
func UnaryContextServerInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	return interceptor.UnaryContextServerInterceptor(ctx, req, info, handler)
}

func DefaultInt64ContextPropagationConfig() ContextPropagationConfig[int64] {
	return contextPropagationConfigFromInterceptor(interceptor.DefaultInt64ContextPropagationConfig())
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
	return interceptor.UnaryInt64ContextPropagationClientInterceptor(ctx, method, req, reply, cc, invoker, opts...)
}

// UnaryInt64ContextPropagationServerInterceptor 是 int64 租户 ID + int64 操作人 ID 的零配置服务端拦截器。
func UnaryInt64ContextPropagationServerInterceptor(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	return interceptor.UnaryInt64ContextPropagationServerInterceptor(ctx, req, info, handler)
}

func UnaryContextPropagationClientInterceptor[T comparable](cfg ContextPropagationConfig[T]) grpc.UnaryClientInterceptor {
	return interceptor.UnaryContextPropagationClientInterceptor(contextPropagationConfigToInterceptor(cfg))
}

func UnaryContextPropagationServerInterceptor[T comparable](cfg ContextPropagationConfig[T]) grpc.UnaryServerInterceptor {
	return interceptor.UnaryContextPropagationServerInterceptor(contextPropagationConfigToInterceptor(cfg))
}

func contextPropagationConfigToInterceptor[T comparable](cfg ContextPropagationConfig[T]) interceptor.ContextPropagationConfig[T] {
	return interceptor.ContextPropagationConfig[T]{
		TenantMetadataKey: cfg.TenantMetadataKey,
		RequireTenant:     cfg.RequireTenant,
		Fields:            cfg.Fields,
	}
}

func contextPropagationConfigFromInterceptor[T comparable](cfg interceptor.ContextPropagationConfig[T]) ContextPropagationConfig[T] {
	return ContextPropagationConfig[T]{
		TenantMetadataKey: cfg.TenantMetadataKey,
		RequireTenant:     cfg.RequireTenant,
		Fields:            cfg.Fields,
	}
}
