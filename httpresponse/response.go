// Package httpresponse 提供统一 HTTP 响应封装，不依赖具体 HTTP 框架。
package httpresponse

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

// Config 配置生成的 go-zero 服务是否启用统一响应及哪些路径跳过包装。
type Config struct {
	Enabled bool `json:",default=true"` // 是否启用统一响应；通过 go-zero 加载配置时默认 true。
	// BypassPaths 指定跳过统一 code/message/result 包装的实际 URL 路径。
	// 包含路由前缀，不包含查询参数；仅精确匹配，不支持前缀或通配符匹配。
	// 空列表表示不跳过任何路径；生成的入口仅在 Enabled=true 时注册跳过中间件。
	// 只影响响应格式，不改变认证、参数校验或 CORS 配置。
	BypassPaths []string `json:",optional"`
}

// Response 是默认响应结构，项目可嵌入该结构体追加自己的响应字段。
type Response struct {
	Code    int64  `json:"code"`             // 业务状态码：0 成功，1 默认失败，其余由项目约定。
	Message string `json:"message"`          // 供用户阅读的提示信息。
	Result  any    `json:"result,omitempty"` // 业务数据；nil 时省略。
}

// Success 包装成功响应。
func Success(result any) *Response {
	return &Response{Code: 0, Message: "请求成功", Result: result}
}

// Fail 包装失败响应，默认业务状态码为 1。
func Fail(message string) *Response {
	return &Response{Code: 1, Message: message}
}

// Error 允许项目指定业务错误码、提示信息和 HTTP 状态码。
// 支持通过 errors.As 从包装过的错误中提取。
type Error struct {
	Code       int64  // 业务错误码，由项目自行约定，成功码 0 不应用于错误。
	Message    string // 错误提示信息。
	HTTPStatus int    // 可选 HTTP 状态码；0 表示使用默认 HTTP 200。
}

// NewError 创建业务错误；HTTP 状态码默认使用 200，可通过返回值自行设置。
func NewError(code int64, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Error 返回业务错误提示，实现 error 接口。
func (e *Error) Error() string {
	return e.Message
}

// OkHandler 可通过 go-zero 的 httpx.SetOkHandler 注册统一成功响应。
// 已包装的响应及明确跳过包装的请求会原样返回，避免重复包装。
func OkHandler(ctx context.Context, value any) any {
	if IsBypassed(ctx) {
		return value
	}
	switch value.(type) {
	case Response, *Response:
		return value
	default:
		return Success(value)
	}
}

// ErrHandler 可通过 go-zero 的 httpx.SetErrorHandlerCtx 注册统一错误响应。
// 普通错误默认返回 HTTP 200、code 1；业务 Error 保留自定义错误码和 HTTP 状态码。
// 跳过包装的请求返回原始错误，默认 HTTP 400，也可使用业务错误指定的 HTTP 状态码。
// 日志使用标准库 slog，项目可通过 slog.SetDefault 配置日志器。
// 自定义认证错误、错误翻译及额外响应字段时，可在项目回调中先处理，未匹配时调用本回调。
func ErrHandler(serviceName string) func(context.Context, error) (int, any) {
	return func(ctx context.Context, err error) (int, any) {
		if err == nil {
			return http.StatusOK, OkHandler(ctx, nil)
		}
		status := http.StatusOK
		response := Fail(err.Error())
		var business *Error
		if errors.As(err, &business) && business != nil {
			response.Code = business.Code
			response.Message = business.Message
			if business.HTTPStatus != 0 {
				status = business.HTTPStatus
			}
		}
		if status < 200 || status > 599 {
			status = http.StatusInternalServerError
		}
		slog.ErrorContext(ctx, "HTTP request failed", "service", serviceName, "error", err)
		if IsBypassed(ctx) {
			if status == http.StatusOK {
				status = http.StatusBadRequest
			}
			return status, err
		}
		return status, response
	}
}

// bypassKey 是跳过响应包装的私有上下文键，避免与项目的上下文键冲突。
type bypassKey struct{}

// IsBypassed 判断当前请求是否应保留原始响应格式。
// 自定义错误回调应优先检查该标记，以保留健康检查、下载等接口的跳过规则。
func IsBypassed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(bypassKey{}).(bool)
	return value
}

// BypassMiddleware 为精确匹配的路径标记跳过包装，例如健康检查或下载接口。
// 可通过 go-zero 的 server.Use 注册，也可作为标准 http.HandlerFunc 中间件使用。
func BypassMiddleware(paths ...string) func(http.HandlerFunc) http.HandlerFunc {
	bypass := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		if path = strings.TrimSpace(path); path != "" {
			bypass[path] = struct{}{}
		}
	}
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if _, ok := bypass[r.URL.Path]; ok {
				r = r.WithContext(context.WithValue(r.Context(), bypassKey{}, true))
			}
			next(w, r)
		}
	}
}
