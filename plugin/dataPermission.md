### 一、使用方式
* 1、注册
```go
plugin.RegisterCtxResolver(func(ctx context.Context) context.Context {
    if ginCtx, ok := ctx.(*gin.Context); ok {
        return ginCtx.Request.Context()
    }
    return ctx
})
// 注册数据权限插件
plugin.RegisterDataPermission(db, plugin.DataPermissionConfig{
    ExcludeTables: []string{"sys_config", "sys_dict"},
    InjectMode:    plugin.DataPermissionModeWhere,
})
```

* 2、定义中间件
```go
func DataPermissionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		fmt.Println("开始进入权限中间件")
		dataScope := "2"
		injectFn := func(db *gorm.DB, tableName string) {
			switch dataScope {
			case "2":
				db.Where(tableName+".created_by IN (?)", 1)
			}
		}

		ctx := plugin.WithDataPermission(c.Request.Context(), injectFn)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
```

* 3、在接口中调试使用
```sql
 SELECT * FROM `account` WHERE `account`.`id` = 29 AND account.created_by IN (1) AND `account`.`deleted_at` IS NULL ORDER BY `account`.`id` LIMIT 1
```

* 4、其他
```go
// 3. 业务代码零改动，自动注入
db.WithContext(ctx).Find(&list)

// 4. 超管跳过
ctx = plugin.SkipDataPermission(ctx)
db.WithContext(ctx).Find(&allData)
```