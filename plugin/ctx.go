package plugin

import "context"

// ================== context 工具 ==================

// ctxResolver 全局 ctx 解析函数，默认直接返回（适配标准 context，如 go-zero）。
// 通过 RegisterCtxResolver 替换为框架特定逻辑（如 gin）。
var ctxResolver = func(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// RegisterCtxResolver 注册自定义 ctx 解析器，程序启动时调用一次。
//
// 解决 gin 项目直接传 *gin.Context 给 db.WithContext() 时，
// 插件无法从 *gin.Context 读取到中间件写入 Request.Context() 数据的问题。
//
// gin 项目注册示例：
//
//	plugin.RegisterCtxResolver(func(ctx context.Context) context.Context {
//	    if ginCtx, ok := ctx.(*gin.Context); ok {
//	        return ginCtx.Request.Context()
//	    }
//	    return ctx
//	})
//
// go-zero / fiber 使用标准 context，无需注册。
func RegisterCtxResolver(fn func(context.Context) context.Context) {
	if fn != nil {
		ctxResolver = fn
	}
}

// resolveCtx 使用已注册的解析器转换 ctx，屏蔽框架差异。
func resolveCtx(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctxResolver(ctx)
}
