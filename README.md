# gorm-plus

基于 [gorm-gen](https://github.com/go-gorm/gen) 的增强扩展包。

提供：**链式条件构造器** · **SingleFlight 缓存** · **多租户自动注入** · **多数据源自动切换** · **自动填充插件** · **慢查询监控**

## 功能特性

### 1. 增强查询构建器 (query)

- **链式调用**: 支持 `Eq`, `EqIfNotZero`, `In`, `InIfNotEmpty`, `Like`, `Gt`, `Gte`, `Lt`, `Lte`, `OrderByDesc`, `OrderByAsc`, `Page` 等常用查询条件
- **单飞模式 (singleflight)**: 内置 singleflight 请求合并 + 本地缓存，有效防止缓存击穿
- **双引擎支持**: 同时支持原生 GORM 和 gorm-gen 生成的代码

### 2. 自动填充插件 (plugin)

- **自动填充**: Create / Update / Save 时自动从 context 中获取值填充到对应字段
- **灵活配置**: 支持自定义字段名、取值函数、创建/更新独立控制
- **零侵入**: 业务层代码无需任何修改

### 3. 代码生成器 (generator)

- **数据库表同步**: 直接从数据库表结构生成 Model、Repository、API 文件
- **多文件生成**: 自动生成基础 repository (`xxx_base.go`)、接口定义 (`xxx_interface.go`)、扩展实现 (`xxx.go`)
- **API 生成**: 自动生成 go-zero 风格的 API 定义文件
- **智能推断**: 自动识别字段类型、验证规则、枚举值等

## 安装

```bash
go get github.com/kuangshp/gorm-plus
```

## 目录结构

```
gorm-plus/
├── query/
│   ├── query_builder.go   # IQueryBuilder：原生 gorm 链式条件构造器
│   ├── gen_wrapper.go     # IGenWrapper：gorm-gen 类型安全链式构造器
│   ├── slow_query.go      # 慢查询监控 gorm 插件
│   └── utils.go           # 工具函数
├── sf/
│   └── sf.go              # SF / SFNoCache / SFWithTTL / SFInvalidate / StopSFCache
├── tenant/
│   └── tenant.go          # 多租户 gorm 插件（自动注入 + 自动跳过）
├── datasource/
│   └── manager.go         # 多数据源管理（自动切换 + 读写分离）
├── plugin/
│   └── autoOperator.go     # 自动填充插件（Create/Update 自动填充字段）
├── generator/
│   ├── config.go
│   ├── generator.go
│   └── template/
├── go.mod
└── version.go
```

---

## 一、多租户（tenant/tenant.go）

### 注册

```go
// main.go 或 wire.go，启动时调用一次
err := tenant.RegisterTenant(db, tenant.TenantConfig{
    TenantField:   "tenant_id",                              // 租户字段名
    ExcludeTables: []string{"sys_config", "sys_dict"},       // 不参与过滤的表
    // GetTenantID 留空自动使用 DefaultGetTenantID
})
```

### Middleware 注入租户 ID（Gin 示例）

```go
func TenantMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        // 可从 JWT Claims、Header、Session 等读取
        tenantID := c.GetHeader("X-Tenant-ID")
        ctx := tenant.WithTenantID(c.Request.Context(), tenantID)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}

// 路由注册
r.Use(TenantMiddleware())
```

### 业务层零改动

```go
// 自动追加 WHERE tenant_id = 'T001'
db.WithContext(ctx).Find(&orders)

// 自动填充 tenant_id 字段
db.WithContext(ctx).Create(&order)

// 自动追加 WHERE tenant_id = 'T001' AND id = ?
db.WithContext(ctx).Save(&order)
```

### 超管跳过租户

```go
ctx = tenant.SkipTenant(ctx)
db.WithContext(ctx).Find(&allOrders) // 无 tenant_id 条件
```

### 动态维护排除表

```go
tenant.AddExcludeTable("log_audit", "sys_trace") // 运行时添加
tenant.RemoveExcludeTable("sys_dict")            // 重新参与租户过滤
tables := tenant.ExcludedTables()               // 查看当前排除表列表
```

### 在 Service 层读取当前租户 ID

```go
tenantID := tenant.TenantIDFromCtx(ctx)
```

---

## 二、自动填充插件（plugin/autoOperator.go）

### 功能说明

在 Create / Update / Save 等写入操作时，自动从 context 中获取值并填充到对应字段。

### 注册

```go
// 方式一：使用 gorm-plus 导出的便捷函数
err := gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
    Fields: []gormplus.FieldConfig{
        {
            Name:     "Creator",
            Getter:   CtxGetter[int64](gormplus.CtxContextKey1),
            OnCreate: true,
        },
        {
            Name:     "Updater",
            Getter:   CtxGetter[int64](gormplus.CtxContextKey2),
            OnUpdate: true,
        },
    },
}).Initialize(db)

// 方式二
	db.Use(plugin.NewAutoFillPlugin(plugin.AutoFillConfig{
		Fields: []plugin.FieldConfig{
			{
				Name:     "created_by",
				Getter:   plugin.CtxGetter[int64](plugin.CtxContextKey1),
				OnCreate: true,
				OnUpdate: false, // 创建人不随更新改变
			},
			{
				Name:     "updated_by",
				Getter:   plugin.CtxGetter[int64](plugin.CtxContextKey1),
				OnCreate: true,
				OnUpdate: true,
			},
			{
				Name:     "created_name",
				Getter:   plugin.CtxGetter[string](plugin.CtxContextKey2),
				OnCreate: true,
				OnUpdate: true,
			},
		},
	}))
```

### 在中间件中填充数据

```go
// 这里以gin为参考示例
func OperatorMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从 JWT claims 或 session 中拿用户名
		accountId, exists := c.Get("accountId")
		fmt.Println("OperatorMiddleware 拿到的 accountId:", accountId, "exists:", exists)
		ctx := context.WithValue(c.Request.Context(), plugin.CtxContextKey1, accountId)
		ctx = context.WithValue(c.Request.Context(), plugin.CtxContextKey2, "admin")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
```

### 使用

```go
dao.AccountEntity.WithContext(ctx.Request.Context()).
		Create(&entity.AccountEntity{})
```

---

## 三、多数据源自动切换（datasource/manager.go）

### 注册

```go
var DS = datasource.NewManager()

func init() {
    DS.Register("default", datasource.GroupConfig{
        Master: datasource.NodeConfig{
            DSN: "root:pwd@tcp(master:3306)/mydb?charset=utf8mb4&parseTime=True&loc=Local",
            Pool: datasource.PoolConfig{MaxOpen: 50, MaxIdle: 10, MaxLifetime: 30 * time.Minute},
        },
        Slaves: []datasource.NodeConfig{
            {DSN: "root:pwd@tcp(slave1:3306)/mydb?charset=utf8mb4&parseTime=True&loc=Local"},
            {DSN: "root:pwd@tcp(slave2:3306)/mydb?charset=utf8mb4&parseTime=True&loc=Local"},
        },
    })

    DS.Register("analytics", datasource.GroupConfig{
        Master: datasource.NodeConfig{
            DSN: "root:pwd@tcp(analytics:3306)/stats?charset=utf8mb4&parseTime=True&loc=Local",
        },
    })
}
```

### Middleware 写入数据源名 + 读写标记（推荐）

```go
// 方式一：固定数据源 + HTTP 方法决定读写
func DSMiddleware(name string) gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx := datasource.WithName(c.Request.Context(), name)
        if c.Request.Method == http.MethodGet {
            ctx = datasource.WithRead(ctx)   // 读 → 从库
        } else {
            ctx = datasource.WithWrite(ctx)  // 写 → 主库
        }
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}

// 路由注册
r.Group("/api").Use(DSMiddleware("default"))
r.Group("/analytics").Use(DSMiddleware("analytics"))
```

### Repository 层用 Auto 自动获取 DB

```go
type OrderRepo struct{}

// List 自动读从库（Middleware 已标记 WithRead）
func (r *OrderRepo) List(ctx context.Context, req *dto.ListReq) ([]*Order, int64, error) {
    db, err := DS.Auto(ctx)   // 自动：数据源=default，读=从库
    if err != nil {
        return nil, 0, err
    }
    return query.FindPage[*Order](
        query.NewQuery(db.Model(&Order{})).
            LikeIfNotEmpty("order_no", req.OrderNo).
            EqIfNotNil("status", req.Status).
            OrderByDesc("created_at"),
        req.PageNum, req.PageSize,
    )
}

// Create 自动走主库（Middleware 已标记 WithWrite）
func (r *OrderRepo) Create(ctx context.Context, o *Order) error {
    db, err := DS.Auto(ctx)   // 自动：数据源=default，写=主库
    if err != nil {
        return err
    }
    return db.Create(o).Error
}
```

### 也可显式指定（精细控制）

```go
db, err := DS.WriteCtx(ctx, "default")    // 强制主库
db, err := DS.ReadCtx(ctx, "analytics")   // 强制 analytics 从库
db  := DS.MustWrite("default")            // 启动阶段获取，失败 panic
```

### 健康检查 + 优雅关闭

```go
// /health 接口
results := DS.Ping()
for key, err := range results {
    if err != nil {
        log.Printf("[WARN] 数据源 %s 不健康: %v", key, err)
    }
}

// 应用退出
defer DS.Close()
```

---

## 四、链式条件构造器

### IQueryBuilder（原生 gorm）

```go
var list []*model.Order
total, err := query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
    EqIfNotZero("user_id", userID).
    EqIfNotNil("status", statusPtr).    // nil 时跳过，ptr(0) 也生效
    LikeIfNotEmpty("order_no", keyword).
    BetweenIfNotZero("amount", minAmt, maxAmt).
    InIfNotEmpty("dept_id", deptIDs).
    OrderByDesc("created_at").
    Page(pageNum, pageSize).
    FindAndCount(&list)
```

### IGenWrapper（gorm-gen 类型安全）

```go
results, err := query.GenWrap(dao.OrderEntity.WithContext(ctx)).
    Always(dao.OrderEntity.Status.Eq(1)).
    EqIfNotZero(dao.OrderEntity.UserID.Eq(userID), userID).
    GteIfNotZero(dao.OrderEntity.CreatedAt.Gte(startTime), startTime).
    InIfNotEmpty(dao.OrderEntity.ID.In(ids...), ids).
    OrderDesc(dao.OrderEntity.CreatedAt).
    Page(pageNum, pageSize).
    Apply().
    Find()
```

### 泛型分页

```go
list, total, err := query.FindPage[*model.Order](q, pageNum, pageSize)
list, total, err := query.ScanPage[OrderVO](q, pageNum, pageSize)  // 联表 VO
```

---

## 五、SF 查询缓存

```go
// 5 分钟缓存（默认）
list, err := sf.SF(func() ([]*model.Order, error) {
    var result []*model.Order
    err := query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
        EqIfNotZero("user_id", userID).Find(&result)
    return result, err
}, "Order.List", map[string]any{"user_id": userID})

// 纯 singleflight（不缓存，只合并并发）
order, err := sf.SFNoCache(func() (*model.Order, error) { ... }, "Order.Detail", map[string]any{"id": id})

// 写后主动失效缓存
sf.SFInvalidate("Order.List", map[string]any{"user_id": userID})

// 应用退出时停止后台清理 goroutine
defer sf.StopSFCache()
```

---

## 六、慢查询监控

```go
err := query.RegisterSlowQuery(db, query.SlowQueryConfig{
    Threshold: 200 * time.Millisecond,
    Logger: func(ctx context.Context, info query.SlowQueryInfo) {
        zap.L().Warn("慢查询",
            zap.Duration("cost",  info.Duration),
            zap.String("table",  info.Table),
            zap.String("sql",    info.SQL),
            zap.Int64("rows",    info.RowsAffected),
        )
    },
})
```

---

## 七、推荐初始化顺序

```go
func main() {
    // 1. 打开 gorm DB
    db, _ := gorm.Open(mysql.Open(dsn), &gorm.Config{})

    // 2. 注册多租户插件
    tenant.RegisterTenant(db, tenant.TenantConfig{
        TenantField:   "tenant_id",
        ExcludeTables: []string{"sys_config", "sys_dict"},
    })

    // 3. 注册慢查询监控
    query.RegisterSlowQuery(db, query.SlowQueryConfig{
        Threshold: 200 * time.Millisecond,
        Logger:    myZapLogger,
    })

    // 4. 注册多数据源
    DS.Register("default", datasource.GroupConfig{
        Master: datasource.NodeConfig{DSN: masterDSN},
        Slaves: []datasource.NodeConfig{{DSN: slave1DSN}, {DSN: slave2DSN}},
    })

    // 5. 应用退出时清理
    defer sf.StopSFCache()
    defer DS.Close()

    // 6. 启动 HTTP Server
    r := gin.New()
    r.Use(TenantMiddleware())        // 注入租户 ID
    r.Use(DSMiddleware("default"))   // 注入数据源 + 读写标记
    r.Run(":8080")
}
```

---

## 八、代码生成器

```go
import "github.com/kuangshp/gorm-plus/generator"

cfg := &generator.Config{
    Host:       "localhost",
    Port:       3306,
    Username:   "root",
    Password:   "password",
    Database:   "your_database",
    OutPath:    "./dal/model",
    ModelPkgPath: "./dal/model/entity",
    RepoPath:   "./dal/repository",
    ApiPath:    "./api",
    Package:    "your_package",
}

err := generator.Generate(cfg)
```

---

## 查询条件说明

| 方法 | 说明 | 示例 |
|------|------|------|
| `Eq` | 等于条件 | `Eq("status", 1)` |
| `EqIfNotZero` | 值不为零时才添加条件 | `EqIfNotZero("user_id", userId)` |
| `EqIfNotNil` | 值不为 nil 时才添加条件 | `EqIfNotNil("status", statusPtr)` |
| `In` | IN 查询 | `In("id", ids...)` |
| `InIfNotEmpty` | 切片非空时才添加 IN 条件 | `InIfNotEmpty("id", ids)` |
| `Like` | 模糊匹配 | `Like("name", "%keyword%")` |
| `LikeIfNotEmpty` | 切片非空时才添加模糊匹配 | `LikeIfNotEmpty("order_no", keyword)` |
| `BetweenIfNotZero` | 范围查询 | `BetweenIfNotZero("amount", minAmt, maxAmt)` |
| `Gt` / `Gte` | 大于 / 大于等于 | `Gt("create_time", timestamp)` |
| `Lt` / `Lte` | 小于 / 小于等于 | `Lte("status", 0)` |
| `OrderByDesc` | 降序排序 | `OrderByDesc("created_at")` |
| `OrderByAsc` | 升序排序 | `OrderByAsc("id")` |
| `Page` | 分页查询 | `Page(pageNum, pageSize)` |

---

## 缓存策略建议

| 场景 | 推荐 TTL | 说明 |
|------|----------|------|
| 列表/统计查询 | 3s ~ 30s | 实时性要求不高，可较长缓存 |
| 配置/字典数据 | 1min ~ 5min | 几乎不变的数据 |
| 详情/用户数据 | 0 (SFNoCache) | 实时性要求高，不缓存 |

---

## 依赖

- gorm.io/gorm
- gorm.io/gen
- gorm.io/driver/mysql
- shopspring/decimal

