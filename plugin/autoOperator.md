## 一、注册方式
* 1、在`gin`中使用
```go
// 注册解析器：遇到 *gin.Context 自动提取 Request.Context()
plugin.RegisterCtxResolver(func(ctx context.Context) context.Context {
    if ginCtx, ok := ctx.(*gin.Context); ok {
        return ginCtx.Request.Context()
    }
    return ctx
})
db.Use(plugin.NewAutoFillPlugin(plugin.AutoFillConfig{
    Fields: []plugin.FieldConfig{
        // int64 操作人
        {Name: "CreatedBy", Getter: plugin.CtxGetter[int64](plugin.CtxOperatorKey1), OnCreate: true},
        {Name: "UpdatedBy", Getter: plugin.CtxGetter[int64](plugin.CtxOperatorKey1), OnCreate: true, OnUpdate: true},
        
        // string UUID 操作人
        {Name: "created_name", Getter: plugin.CtxGetter[string](plugin.CtxOperatorKey2), OnCreate: true},
		// 完全自定义数据源
        {Name: "Source", Getter: func(ctx context.Context) any {
            if src, ok := resolveCtx(ctx).Value("source").(string); ok {
            return src
            }
            return "unknown"
        }, OnCreate: true},
    },
}))

// 之后业务代码直接传 *gin.Context，无需 a.Ctx(ctx)
db.WithContext(ctx).Create(&order)       // ctx 是 *gin.Context 也生效
db.WithContext(ctx).Model(&x).Updates(d)
```

* 2、在`go-zero`中使用
```go
// go-zero 用标准 context，默认实现直接返回，无需注册
db.Use(&plugin.AutoOperatorPlugin{})

// 业务代码正常传 ctx
db.WithContext(ctx).Create(&order)
```

* 3、在`echo / fiber `中
```go
// echo
plugin.RegisterCtxResolver(func(ctx context.Context) context.Context {
    if echoCtx, ok := ctx.(echo.Context); ok {
        return echoCtx.Request().Context()
    }
    return ctx
})
```

## 二、定义中间件
* 1、在`gin`中
```go
func OperatorMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        // 从 JWT claims 或 session 中拿用户名
        accountId, exists := c.Get("accountId")
        fmt.Println("OperatorMiddleware 拿到的 accountId:", accountId, "exists:", exists)
        ctx := context.WithValue(c.Request.Context(), plugin.CtxOperatorKey1, accountId)
        ctx = context.WithValue(ctx, plugin.CtxOperatorKey2, "李四")
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

* 2、在`go-zero`中
```go
func GoZeroOperatorMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// go-zero 内置 JWT 中间件将 payload 写入 ctx，key 固定为 "payload"
		// payload 结构取决于 JWT claims 的定义
		if payload, ok := r.Context().Value("payload").(map[string]interface{}); ok {
			if accountId, exists := payload["accountId"]; exists {
				ctx := context.WithValue(r.Context(), plugin.CtxOperatorKey, cast.ToInt64(accountId))
				r = r.WithContext(ctx)
			}
		}
		fmt.Println("GoZeroOperatorMiddleware accountId:", r.Context().Value(plugin.CtxOperatorKey))
		next(w, r)
	}
}
```

* 3、在`echo / fiber `中
```go
func FiberOperatorMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		accountId := c.Locals("accountId")
		fmt.Println("FiberOperatorMiddleware accountId:", accountId)
		if accountId != nil {
			// Fiber 通过 UserContext 传递标准 context，gorm 用 WithContext(c.UserContext())
			ctx := context.WithValue(c.UserContext(), plugin.CtxOperatorKey, cast.ToInt64(accountId))
			c.SetUserContext(ctx)
		}
		return c.Next()
	}
}
```