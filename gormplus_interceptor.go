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

// PropagateTenantID 声明 Tenant 插件使用的租户 ID；传值时使用固定值，否则从 context 读取。
func PropagateTenantID[T comparable](fixedValue ...T) ContextMetadataField {
	return interceptor.PropagateTenantID(fixedValue...)
}

// PropagateOperatorID 声明 AutoFill 插件默认使用的操作人 ID；传值时使用固定值。
func PropagateOperatorID[T any](fixedValue ...T) ContextMetadataField {
	return interceptor.PropagateOperatorID(fixedValue...)
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
