### 查询范围与 Scan

插件覆盖 Query 和 Row 回调，支持 `Find` 以及通过 `Model` / `Table` 构建的 `Scan`、`Rows`、`Row`。权限过滤不依赖接收目标的类型：

```go
// 切片接收多行，按权限过滤。
db.WithContext(ctx).Model(&Account{}).Scan(&list)

// 结构体接收一行；Limit(1) 才会限制数据库查询行数。
db.WithContext(ctx).Model(&Account{}).Order("id ASC").Limit(1).Scan(&item)
```

普通业务 OR 会先整体分组，再追加权限条件。`Raw(sql).Scan(...)` 不会自动重写原生 SQL，必须在 SQL 中自行写入权限限制。排除表、`SkipDataPermission` 和未设置权限注入函数时沿用原有行为。

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
