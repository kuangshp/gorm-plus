package interceptor

import (
	"context"
	"testing"

	"github.com/kuangshp/gorm-plus/plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestTenantAndOperatorContextRoundTrip(t *testing.T) {
	fields := []ContextMetadataField{
		PropagateTenantID[int64](),
		PropagateContextKey[int64](plugin.CtxOperatorKey1),
		PropagateContextKey[string](plugin.CtxOperatorKey2),
	}
	apiCtx := plugin.WithTenantID(context.Background(), int64(1001))
	apiCtx = context.WithValue(apiCtx, plugin.CtxOperatorKey1, int64(2002))
	apiCtx = context.WithValue(apiCtx, plugin.CtxOperatorKey2, "张三")

	clientInterceptor := NewUnaryContextClientInterceptor(fields...)
	var outgoingMD metadata.MD
	err := clientInterceptor(apiCtx, "/site.Site/Create", nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		outgoingMD, _ = metadata.FromOutgoingContext(ctx)
		return nil
	})
	if err != nil {
		t.Fatalf("client interceptor: %v", err)
	}

	serverCtx := metadata.NewIncomingContext(context.Background(), outgoingMD)
	_, err = UnaryContextServerInterceptor(serverCtx, nil, nil, func(ctx context.Context, _ any) (any, error) {
		if got := plugin.TenantIDFromCtx[int64](ctx); got != 1001 {
			t.Fatalf("tenant ID = %d, want 1001", got)
		}
		if got := plugin.CtxGetter[int64](plugin.CtxOperatorKey1)(ctx); got != int64(2002) {
			t.Fatalf("operator ID = %#v, want 2002", got)
		}
		if got := plugin.CtxGetter[string](plugin.CtxOperatorKey2)(ctx); got != "张三" {
			t.Fatalf("operator name = %#v, want 张三", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("server interceptor: %v", err)
	}
}

func TestArbitraryContextKeysRoundTrip(t *testing.T) {
	fields := []ContextMetadataField{
		PropagateContextKey[int64]("tenantId"),
		PropagateContextKey[int64]("operatorId"),
		PropagateContextKey[string]("loginUserId"),
	}
	apiCtx := context.WithValue(context.Background(), "tenantId", int64(1001))
	apiCtx = context.WithValue(apiCtx, "operatorId", int64(2002))
	apiCtx = context.WithValue(apiCtx, "loginUserId", "user-3003")

	clientInterceptor := NewUnaryContextClientInterceptor(fields...)
	var outgoingMD metadata.MD
	err := clientInterceptor(apiCtx, "/site.Site/Create", nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		outgoingMD, _ = metadata.FromOutgoingContext(ctx)
		return nil
	})
	if err != nil {
		t.Fatalf("client interceptor: %v", err)
	}

	serverInterceptor := UnaryContextServerInterceptor
	_, err = serverInterceptor(metadata.NewIncomingContext(context.Background(), outgoingMD), nil, nil, func(ctx context.Context, _ any) (any, error) {
		if got := ctx.Value("tenantId"); got != int64(1001) {
			t.Fatalf("tenantId = %#v, want 1001", got)
		}
		if got := ctx.Value("operatorId"); got != int64(2002) {
			t.Fatalf("operatorId = %#v, want 2002", got)
		}
		if got := ctx.Value("loginUserId"); got != "user-3003" {
			t.Fatalf("loginUserId = %#v, want user-3003", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("server interceptor: %v", err)
	}
}

func TestBuiltInOperatorContextKeyRoundTrip(t *testing.T) {
	fields := []ContextMetadataField{
		PropagateContextKey[int64](plugin.CtxOperatorKey1),
		PropagateContextKey[string](plugin.CtxOperatorKey2),
	}
	apiCtx := context.WithValue(context.Background(), plugin.CtxOperatorKey1, int64(2002))
	apiCtx = context.WithValue(apiCtx, plugin.CtxOperatorKey2, "张三")

	clientInterceptor := NewUnaryContextClientInterceptor(fields...)
	var outgoingMD metadata.MD
	err := clientInterceptor(apiCtx, "/site.Site/Create", nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		outgoingMD, _ = metadata.FromOutgoingContext(ctx)
		return nil
	})
	if err != nil {
		t.Fatalf("client interceptor: %v", err)
	}

	_, err = UnaryContextServerInterceptor(metadata.NewIncomingContext(context.Background(), outgoingMD), nil, nil, func(ctx context.Context, _ any) (any, error) {
		if got := plugin.CtxGetter[int64](plugin.CtxOperatorKey1)(ctx); got != int64(2002) {
			t.Fatalf("operator ID = %#v, want 2002", got)
		}
		if got := plugin.CtxGetter[string](plugin.CtxOperatorKey2)(ctx); got != "张三" {
			t.Fatalf("operator name = %#v, want 张三", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("server interceptor: %v", err)
	}
}

func TestFixedContextValueRoundTrip(t *testing.T) {
	fields := []ContextMetadataField{
		PropagateContextValue[string]("serviceSource", "admin-api"),
		PropagateContextValue[int64]("defaultSiteId", 1001),
	}
	clientInterceptor := NewUnaryContextClientInterceptor(fields...)
	var outgoingMD metadata.MD
	err := clientInterceptor(context.Background(), "/site.Site/Create", nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		outgoingMD, _ = metadata.FromOutgoingContext(ctx)
		return nil
	})
	if err != nil {
		t.Fatalf("client interceptor: %v", err)
	}

	_, err = UnaryContextServerInterceptor(metadata.NewIncomingContext(context.Background(), outgoingMD), nil, nil, func(ctx context.Context, _ any) (any, error) {
		if got := ctx.Value("serviceSource"); got != "admin-api" {
			t.Fatalf("serviceSource = %#v, want admin-api", got)
		}
		if got := ctx.Value("defaultSiteId"); got != int64(1001) {
			t.Fatalf("defaultSiteId = %#v, want 1001", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("server interceptor: %v", err)
	}
}
