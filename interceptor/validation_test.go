package interceptor

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestUnaryValidationInterceptorRejectsNonProtoRequest(t *testing.T) {
	called := false
	_, err := UnaryValidationInterceptor(context.Background(), struct{}{}, nil, func(context.Context, any) (any, error) {
		called = true
		return nil, nil
	})
	if called {
		t.Fatal("非 Proto 请求不应进入业务 Handler")
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}

func TestUnaryValidationInterceptorPassesValidProtoRequest(t *testing.T) {
	req := &emptypb.Empty{}
	want := &emptypb.Empty{}
	got, err := UnaryValidationInterceptor(context.Background(), req, &grpc.UnaryServerInfo{}, func(_ context.Context, value any) (any, error) {
		if value != req {
			t.Fatalf("Handler 收到的请求发生变化: got %p, want %p", value, req)
		}
		return want, nil
	})
	if err != nil {
		t.Fatalf("UnaryValidationInterceptor() error = %v", err)
	}
	if got != want {
		t.Fatalf("UnaryValidationInterceptor() = %p, want %p", got, want)
	}
}

func TestValidationStatusErrorFallsBackForNonValidationError(t *testing.T) {
	err := validationStatusError(context.Canceled)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if status.Convert(err).Message() != "请求参数校验失败" {
		t.Fatalf("message = %q, want %q", status.Convert(err).Message(), "请求参数校验失败")
	}
}
