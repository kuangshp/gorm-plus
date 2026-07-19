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

// validationMessages 统一维护 Protovalidate 标准规则的中文提示。
// 未配置的规则会回退到 Protovalidate 原始错误信息。
var validationMessages = map[string]string{
	"required":           "不能为空",
	"string.min_len":     "长度不能小于规定值",
	"string.max_len":     "长度不能超过规定值",
	"string.len":         "长度不符合要求",
	"string.pattern":     "格式不符合要求",
	"string.email":       "邮箱格式不正确",
	"string.uuid":        "UUID格式不正确",
	"int64.gt":           "必须大于规定值",
	"int64.gte":          "不能小于规定值",
	"int64.lt":           "必须小于规定值",
	"int64.lte":          "不能大于规定值",
	"int64.in":           "不在允许的取值范围内",
	"repeated.min_items": "至少需要一项数据",
	"repeated.max_items": "数据项数量超过限制",
	"repeated.unique":    "数据项不能重复",
}

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
		fmt.Sprintf("字段【%s】:%s", field, validationMessage(violation)),
	)
	if statusWithDetails, detailErr := grpcStatus.WithDetails(validationErr.ToProto()); detailErr == nil {
		return statusWithDetails.Err()
	}
	return grpcStatus.Err()
}

// validationMessage 根据规则 ID 返回中文提示，未配置时保留原始错误信息。
func validationMessage(violation *protovalidate.Violation) string {
	if message, ok := validationMessages[violation.Proto.GetRuleId()]; ok {
		return message
	}
	if message := violation.Proto.GetMessage(); message != "" {
		return message
	}
	return "参数不符合要求"
}
