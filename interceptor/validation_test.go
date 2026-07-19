package interceptor

import (
	"context"
	"testing"

	validate "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"buf.build/go/protovalidate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestValidationMessageUsesChineseStandardRuleMessage(t *testing.T) {
	violation := &protovalidate.Violation{Proto: &validate.Violation{
		RuleId:  proto.String("string.max_len"),
		Message: proto.String("value length must be at most 32 characters"),
	}}
	if got, want := validationMessage(violation), "长度不能超过规定值"; got != want {
		t.Fatalf("validationMessage() = %q, want %q", got, want)
	}
}

func TestValidationMessageFallsBackToOriginalMessage(t *testing.T) {
	violation := &protovalidate.Violation{Proto: &validate.Violation{
		RuleId:  proto.String("string.date_format"),
		Message: proto.String("日期格式必须为 YYYY-MM-DD"),
	}}
	if got, want := validationMessage(violation), "日期格式必须为 YYYY-MM-DD"; got != want {
		t.Fatalf("validationMessage() = %q, want %q", got, want)
	}
}

func TestValidationMessageUsesCustomCELMessage(t *testing.T) {
	violation := &protovalidate.Violation{Proto: &validate.Violation{
		RuleId:  proto.String("sys_user.keyword.length"),
		Message: proto.String("用户名或者邮箱长度必须在5到190个字符之间"),
	}}
	if got, want := validationMessage(violation), "用户名或者邮箱长度必须在5到190个字符之间"; got != want {
		t.Fatalf("validationMessage() = %q, want %q", got, want)
	}
}

func TestValidationMessageUsesDefaultMessage(t *testing.T) {
	violation := &protovalidate.Violation{Proto: &validate.Violation{RuleId: proto.String("custom.unknown")}}
	if got, want := validationMessage(violation), "参数不符合要求"; got != want {
		t.Fatalf("validationMessage() = %q, want %q", got, want)
	}
}

func TestWithValidationMessagesExtendsAndOverridesDefaults(t *testing.T) {
	customMessages := map[string]string{
		"required":           "此字段必须填写",
		"string.date_format": "日期格式不正确",
	}
	cfg := newValidationInterceptorConfig(WithValidationMessages(customMessages))
	customMessages["required"] = "修改后不应生效"

	tests := []struct {
		ruleID string
		want   string
	}{
		{ruleID: "required", want: "此字段必须填写"},
		{ruleID: "string.date_format", want: "日期格式不正确"},
		{ruleID: "string.max_len", want: "长度不能超过规定值"},
	}
	for _, tt := range tests {
		violation := &protovalidate.Violation{Proto: &validate.Violation{RuleId: proto.String(tt.ruleID)}}
		if got := validationMessageWithMessages(violation, cfg.messages); got != tt.want {
			t.Errorf("rule %s: got %q, want %q", tt.ruleID, got, tt.want)
		}
	}
}

func TestNewUnaryValidationInterceptorWithoutOptionsUsesDefaults(t *testing.T) {
	if interceptor := NewUnaryValidationInterceptor(); interceptor == nil {
		t.Fatal("NewUnaryValidationInterceptor() returned nil")
	}
}

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
