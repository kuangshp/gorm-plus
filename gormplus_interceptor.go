package gormplus

import (
	"github.com/kuangshp/gorm-plus/interceptor"
	"google.golang.org/grpc"
)

// UnaryValidationInterceptor 在 gRPC 业务 Handler 执行前统一执行 Protovalidate 参数校验。
var UnaryValidationInterceptor = interceptor.UnaryValidationInterceptor

const (
	TenantIDMetadataKey   = interceptor.TenantIDMetadataKey
	OperatorIDMetadataKey = interceptor.OperatorIDMetadataKey
)

type ContextMetadataField = interceptor.ContextMetadataField
type ContextPropagationConfig[T comparable] = interceptor.ContextPropagationConfig[T]

func NewContextMetadataField[T any](metadataKey string, contextKey any) ContextMetadataField {
	return interceptor.NewContextMetadataField[T](metadataKey, contextKey)
}

func DefaultInt64ContextPropagationConfig() ContextPropagationConfig[int64] {
	return interceptor.DefaultInt64ContextPropagationConfig()
}

func UnaryContextPropagationClientInterceptor[T comparable](cfg ContextPropagationConfig[T]) grpc.UnaryClientInterceptor {
	return interceptor.UnaryContextPropagationClientInterceptor(cfg)
}

func UnaryContextPropagationServerInterceptor[T comparable](cfg ContextPropagationConfig[T]) grpc.UnaryServerInterceptor {
	return interceptor.UnaryContextPropagationServerInterceptor(cfg)
}
