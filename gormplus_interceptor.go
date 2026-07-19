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

type ContextMetadataField = interceptor.ContextMetadataField

// PropagateContextKey 声明一个使用字符串 key 的透传字段。
func PropagateContextKey[T any](key any) ContextMetadataField {
	return interceptor.PropagateContextKey[T](key)
}

// PropagateTenantID 声明 Tenant 插件使用的租户 ID 透传字段。
func PropagateTenantID[T comparable]() ContextMetadataField {
	return interceptor.PropagateTenantID[T]()
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
