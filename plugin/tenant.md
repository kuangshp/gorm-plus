### 一、使用方式
* 1、直接注册
```go
// 注册插件
// 注册解析器：遇到 *gin.Context 自动提取 Request.Context()
plugin.RegisterCtxResolver(func(ctx context.Context) context.Context {
    if ginCtx, ok := ctx.(*gin.Context); ok {
        return ginCtx.Request.Context()
    }
    return ctx
})

// 注册多租户插件
plugin.RegisterTenant(db, plugin.TenantConfig[int64]{
    TenantField: "tenant_id",
    InjectMode:  plugin.ModeScopes,
})
```

* 2、在中间件中
```go
func TenantMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID := int64(1)
		ctx := plugin.WithTenantID(c.Request.Context(), tenantID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
```