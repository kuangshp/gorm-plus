## 在`gin`中使用

```go
// 1. 启动注册（一次）
plugin.RegisterDataPermission(db, plugin.DataPermissionConfig{
    ExcludeTables: []string{"sys_config", "sys_dict"},
})

// 2. gin 中间件传入 InjectFn
func DataPermissionMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, _ := jwt.ParseToken(c.GetHeader("Authorization"))
        
        injectFn := func(db *gorm.DB, tableName string) {
            switch claims.DataScope {
            case "2":
                db.Where(tableName+".create_by IN (...role...)", claims.RoleId)
            case "3":
                db.Where(tableName+".create_by IN (...dept...)", claims.DeptId)
            case "5":
                db.Where(tableName+".create_by = ?", claims.UserId)
            }
        }
        
        ctx := plugin.WithDataPermission(c.Request.Context(), injectFn)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}

// 3. 业务代码零改动，自动注入
db.WithContext(ctx).Find(&list)

// 4. 超管跳过
ctx = plugin.SkipDataPermission(ctx)
db.WithContext(ctx).Find(&allData)
```

