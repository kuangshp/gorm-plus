package gormplus

import (
	"testing"

	"google.golang.org/grpc"
)

func TestRootPackageExportsArbitraryContextPropagation(t *testing.T) {
	fields := []ContextMetadataField{
		PropagateTenantID[int64](),
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
