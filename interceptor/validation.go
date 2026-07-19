// Package interceptor 提供可复用的 gRPC 服务端拦截器。
package interceptor

import (
	"context"
	"errors"
	"fmt"

	"buf.build/go/protovalidate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// UnaryValidationInterceptor 在业务 Handler 执行前统一校验所有带 Protovalidate 规则的请求。
//
// 校验采用 Fail Fast 模式，只将第一个违规字段作为 gRPC InvalidArgument 错误返回；
// 完整的结构化 Violations 会附加到 gRPC Status Details 中。
func UnaryValidationInterceptor(
	ctx context.Context,
	req any,
	_ *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	message, ok := req.(proto.Message)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "请求参数类型错误")
	}
	if err := protovalidate.Validate(message, protovalidate.WithFailFast()); err != nil {
		return nil, validationStatusError(err)
	}
	return handler(ctx, req)
}

// validationStatusError 将 Protovalidate 错误转换为包含字段路径的 gRPC 错误。
func validationStatusError(err error) error {
	var validationErr *protovalidate.ValidationError
	if !errors.As(err, &validationErr) || len(validationErr.Violations) == 0 {
		return status.Error(codes.InvalidArgument, "请求参数校验失败")
	}

	violation := validationErr.Violations[0]
	field := protovalidate.FieldPathString(violation.Proto.GetField())
	if field == "" {
		field = "request"
	}

	grpcStatus := status.New(
		codes.InvalidArgument,
		fmt.Sprintf("参数 %s 错误：%s", field, violation.Proto.GetMessage()),
	)
	if statusWithDetails, detailErr := grpcStatus.WithDetails(validationErr.ToProto()); detailErr == nil {
		return statusWithDetails.Err()
	}
	return grpcStatus.Err()
}
