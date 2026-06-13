package sqlcaller

import (
	"context"
	"runtime"
	"strconv"
)

type contextKey struct{}

// Caller 表示 SQL 日志希望展示的业务调用位置。
type Caller struct {
	File string
	Line int
}

func (c Caller) String() string {
	if c.File == "" || c.Line <= 0 {
		return ""
	}
	return c.File + ":" + strconv.Itoa(c.Line)
}

// WithCaller 在 ctx 中记录当前调用点。
func WithCaller(ctx context.Context) context.Context {
	return WithCallerSkip(ctx, 1)
}

// WithCallerSkip 与 WithCaller 类似,但允许调用方额外跳过封装层。
func WithCallerSkip(ctx context.Context, skip int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, file, line, ok := runtime.Caller(skip + 1); ok {
		return ContextWithCaller(ctx, Caller{File: file, Line: line})
	}
	return ctx
}

// ContextWithCaller 把明确的调用位置写入 ctx。
func ContextWithCaller(ctx context.Context, caller Caller) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if caller.File == "" || caller.Line <= 0 {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, caller)
}

// FromContext 从 ctx 读取 SQL 日志调用位置。
func FromContext(ctx context.Context) (Caller, bool) {
	if ctx == nil {
		return Caller{}, false
	}
	caller, ok := ctx.Value(contextKey{}).(Caller)
	return caller, ok && caller.File != "" && caller.Line > 0
}
