package gormplus

import (
	"testing"

	"google.golang.org/grpc"
)

func TestRootPackageExportsZeroConfigPropagationInterceptors(t *testing.T) {
	var client grpc.UnaryClientInterceptor = UnaryInt64ContextPropagationClientInterceptor
	var server grpc.UnaryServerInterceptor = UnaryInt64ContextPropagationServerInterceptor
	if client == nil || server == nil {
		t.Fatal("zero-config propagation interceptor is nil")
	}
}

func TestRootPackageExportsArbitraryContextPropagation(t *testing.T) {
	fields := []ContextMetadataField{
		PropagateContextKey[int64]("tenantId"),
		PropagateContextKey[int64]("operatorId"),
	}
	if NewUnaryContextClientInterceptor(fields...) == nil {
		t.Fatal("NewUnaryContextClientInterceptor returned nil")
	}
	var server grpc.UnaryServerInterceptor = UnaryContextServerInterceptor
	if server == nil {
		t.Fatal("UnaryContextServerInterceptor is nil")
	}
}
