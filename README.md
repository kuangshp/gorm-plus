# gorm-plus

基于 [gorm](https://gorm.io) 和 [gorm-gen](https://github.com/go-gorm/gen) 的增强扩展包。

**核心能力：** 链式条件构造器 · gorm-gen 类型安全扩展 · 多租户自动注入 · 数据权限自动注入 · 自动填充插件 · 多数据源管理（任意驱动）· SingleFlight 可插拔缓存 · 慢查询监控 · 代码生成器

---

## 安装

```bash
go get github.com/kuangshp/gorm-plus
```

---

## 目录结构

```
gorm-plus/
├── gormplus.go           # 统一入口，所有功能的顶层导出
├── version.go
├── query/
│   ├── query_builder.go  # IQueryBuilder：原生 gorm 链式条件构造器
│   ├── gen_wrapper.go    # IGenWrapper：gorm-gen 类型安全链式构造器
│   ├── slow_query.go     # 慢查询监控 gorm 插件
│   └── utils.go
├── plugin/
│   ├── ctx.go            # ctx 解析器（屏蔽 gin / go-zero / fiber 框架差异）
│   ├── tenant.go         # 多租户插件
│   ├── dataPermission.go # 数据权限插件
│   └── autoOperator.go   # 自动填充插件
├── datasource/
│   └── manager.go        # 多数据源管理（任意 gorm 驱动 / 主从分离 / 读写分离）
├── sf/
│   └── sf.go             # SingleFlight + 可插拔缓存（防缓存击穿）
└── generator/            # 代码生成器
```

---

## 快速开始

```go
import (
    "gorm.io/driver/mysql"   // 按需替换为 postgres / sqlite / sqlserver
    gormplus "github.com/kuangshp/gorm-plus"
)

func main() {
    // ① ctx 解析器（gin 项目必须注册；go-zero / fiber 跳过）
    gormplus.RegisterCtxResolver(func(ctx context.Context) context.Context {
        if ginCtx, ok := ctx.(*gin.Context); ok {
            return ginCtx.Request.Context()
        }
        return ctx
    })

    // ② 多数据源（Dialector 外部传入，不内置任何驱动）
    gormplus.DS.Register("default", gormplus.DataSourceGroupConfig{
        Master: gormplus.DataSourceNodeConfig{
            Dialector: mysql.Open("root:pwd@tcp(master:3306)/mydb?charset=utf8mb4&parseTime=True"),
            Pool:      gormplus.DataSourcePoolConfig{MaxOpen: 50, MaxIdle: 10},
        },
        Slaves: []gormplus.DataSourceNodeConfig{
            {Dialector: mysql.Open("root:pwd@tcp(slave:3306)/mydb?charset=utf8mb4&parseTime=True")},
        },
    })

    // ③ 打开 DB
    db, _ := gorm.Open(mysql.Open(dsn), &gorm.Config{})

    // ④ 多租户插件
    gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
        TenantField:   "tenant_id",
        ExcludeTables: []string{"sys_config", "sys_dict"},
    })

    // ⑤ 数据权限插件
    gormplus.RegisterDataPermission(db, gormplus.DataPermissionConfig{
        ExcludeTables: []string{"sys_config", "sys_dict"},
    })

    // ⑥ 自动填充插件
    db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
        Fields: []gormplus.FieldConfig{
            {Name: "CreatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true},
            {Name: "UpdatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true, OnUpdate: true},
        },
    }))

    // ⑦ 慢查询监控
    gormplus.RegisterSlowQuery(db, gormplus.SlowQueryConfig{
        Threshold: 200 * time.Millisecond,
        Logger: func(ctx context.Context, info gormplus.SlowQueryInfo) {
            log.Printf("[慢查询] cost=%v table=%s sql=%s", info.Duration, info.Table, info.SQL)
        },
    })

    // ⑧ 优雅退出
    defer gormplus.StopSFCache()
    defer gormplus.DS.Close()

    r := gin.New()
    r.Use(OperatorMiddleware(), TenantMiddleware(), DataPermissionMiddleware())
    r.Run(":8080")
}
```

---

## 一、ctx 解析器

插件读取 ctx 数据前先调用解析器，屏蔽不同框架的 ctx 类型差异。

| 框架 | 是否需要注册 | 业务代码传 ctx |
|------|------------|--------------|
| gin | **必须注册** | `db.WithContext(c)` 直接传 `*gin.Context` |
| go-zero | 无需注册 | `db.WithContext(r.Context())` |
| fiber | 无需注册 | `db.WithContext(c.UserContext())` |

```go
gormplus.RegisterCtxResolver(func(ctx context.Context) context.Context {
    if ginCtx, ok := ctx.(*gin.Context); ok {
        return ginCtx.Request.Context()
    }
    return ctx
})

// 注册后可直接传 *gin.Context，无需手动 c.Request.Context()
db.WithContext(c).Find(&list)
dao.Entity.WithContext(c).Find()
```

---

## 二、多数据源管理

不内置任何驱动依赖，通过 `Dialector` 字段外部传入，支持任意 gorm 驱动。

### 注册数据源

```go
// MySQL
import "gorm.io/driver/mysql"
gormplus.DS.Register("default", gormplus.DataSourceGroupConfig{
    Master: gormplus.DataSourceNodeConfig{
        Dialector: mysql.Open("root:pwd@tcp(master:3306)/mydb?charset=utf8mb4&parseTime=True"),
        Pool:      gormplus.DataSourcePoolConfig{MaxOpen: 50, MaxIdle: 10},
    },
    Slaves: []gormplus.DataSourceNodeConfig{
        {Dialector: mysql.Open("root:pwd@tcp(slave1:3306)/mydb?charset=utf8mb4&parseTime=True")},
        {Dialector: mysql.Open("root:pwd@tcp(slave2:3306)/mydb?charset=utf8mb4&parseTime=True")},
    },
})

// PostgreSQL
import "gorm.io/driver/postgres"
gormplus.DS.Register("pg", gormplus.DataSourceGroupConfig{
    Master: gormplus.DataSourceNodeConfig{
        Dialector: postgres.Open("host=localhost user=root password=pwd dbname=mydb port=5432 sslmode=disable"),
    },
})

// SQLite（适合单元测试）
import "gorm.io/driver/sqlite"
gormplus.DS.Register("test", gormplus.DataSourceGroupConfig{
    Master: gormplus.DataSourceNodeConfig{Dialector: sqlite.Open(":memory:")},
})

// 多数据源混用
gormplus.DS.Register("analytics", gormplus.DataSourceGroupConfig{
    Master: gormplus.DataSourceNodeConfig{Dialector: postgres.Open(analyticsDSN)},
})
```

### 中间件标记读写

```go
func DSMiddleware(name string) gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx := gormplus.DSWithName(c.Request.Context(), name)
        if c.Request.Method == http.MethodGet {
            ctx = gormplus.DSWithRead(ctx)  // GET → 从库
        } else {
            ctx = gormplus.DSWithWrite(ctx) // 其他 → 主库
        }
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

### Repository 层获取 DB

```go
// 推荐：Auto 自动读取 context 决定数据源和读写
func (r *OrderRepo) List(ctx context.Context) ([]*Order, error) {
    db, err := gormplus.DS.Auto(ctx)
    if err != nil { return nil, err }
    var list []*Order
    return list, db.WithContext(ctx).Find(&list).Error
}

// 显式指定
db, err := gormplus.DS.Write("default")              // 主库
db, err := gormplus.DS.Read("default")               // 从库
db, err := gormplus.DS.WriteCtx(ctx, "analytics")    // 指定数据源主库

// 健康检查
results := gormplus.DS.Ping()
// map[string]error{"default:master": nil, "default:slave0": nil}
```

---

## 三、原生 gorm 链式条件构造器（Query）

```go
// 分页列表查询
built := gormplus.Query[*model.Account](db, ctx).
    LLike("username", username).                        // 空时自动跳过
    WhereIf(status != 0, "status = ?", status).         // false 时跳过
    BetweenIfNotZero("created_at", startTime, endTime). // 任一零值时跳过
    WhereIf(len(ids) > 0, "dept_id IN ?", ids).
    Build()
var total int64
built.Count(&total)
built.Order("created_at DESC").Limit(pageSize).Offset((page-1)*pageSize).Find(&list)

// 泛型分页（一步到位）
list, total, err := gormplus.FindByPage[*model.Account](
    gormplus.Query[*model.Account](db, ctx).
        LLike("username", username).
        WhereIf(status != 0, "status = ?", status).
        Build().Order("created_at DESC"),
    pageNum, pageSize,
)

// 联表 + 映射到 VO（用 ScanByPage）
type AccountVO struct {
    ID       int64  `json:"id"`
    Username string `json:"username"`
    DeptName string `json:"deptName"`
}
list, total, err := gormplus.ScanByPage[AccountVO](
    gormplus.Query[*model.Account](db, ctx).
        LLike("a.username", username).
        Build().
        Select("a.id", "a.username", "d.name AS dept_name").
        Joins("LEFT JOIN sys_dept d ON d.id = a.dept_id").
        Order("a.created_at DESC"),
    pageNum, pageSize,
)

// AND 分组：WHERE (username LIKE '%kw%' OR email LIKE '%kw%')
gormplus.Query[*model.Account](db, ctx).
    WhereGroup(func(q gormplus.IQueryBuilder) {
        q.Like("username", keyword).
          WhereIf(true, "email LIKE ?", "%"+keyword+"%")
    }).Build().Find(&list)

// OR 分组：WHERE status = 1 OR (role = 99 AND org_id = 10)
gormplus.Query[*model.Account](db, ctx).
    WhereIf(true, "status = ?", 1).
    OrGroup(func(q gormplus.IQueryBuilder) {
        q.WhereIf(role != 0, "role = ?", role).
          WhereIf(orgID != 0, "org_id = ?", orgID)
    }).Build().Find(&list)
```

| 方法 | 说明 |
|------|------|
| `Like / LLike / RLike` | 模糊查询，值为空自动跳过 |
| `BetweenIfNotZero` | 范围查询，任一零值跳过 |
| `WhereIf(cond, sql, args...)` | 条件成立时追加 AND |
| `WhereGroup(fn)` | AND 括号分组 |
| `OrGroup(fn)` | OR 括号分组 |
| `RawWhere / RawOrWhere / RawWhereIf` | 原生 SQL 条件 |
| `Build()` | 返回 `*gorm.DB` |

---

## 四、gorm-gen 类型安全链式构造器（GenWrap）

```go
// 基础查询
list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
    LLike(dao.AccountEntity.Username, username).
    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
    Apply().
    Order(dao.AccountEntity.CreatedAt.Desc()).
    Limit(pageSize).Offset((page-1)*pageSize).
    Find()

// 联表查询（使用别名）
list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
    As("a").
    RawWhere("a.username LIKE ?", "%"+username+"%").
    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
    Apply().
    Select(dao.AccountEntity.ID, dao.AccountEntity.Username).
    Find()

// AND 简单分组：WHERE (status = 1 AND role = 2)
gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
    WhereGroup(dao.AccountEntity.Status.Eq(1), dao.AccountEntity.Role.Eq(2)).
    Apply().Find()

// AND 函数分组（组内可用 WhereIf / Like 等完整能力）
// => WHERE (username LIKE '%admin' AND status = 1)
gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
    WhereGroupFn(func(w gormplus.IGenWrapper[dao.IAccountEntityDo]) {
        w.LLike(dao.AccountEntity.Username, username).
          WhereIf(status != 0, dao.AccountEntity.Status.Eq(status))
    }).Apply().Find()

// OR 函数分组：WHERE status = 1 OR (username LIKE '%admin' AND role = 99)
gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
    WhereIf(true, dao.AccountEntity.Status.Eq(1)).
    OrGroupFn(func(w gormplus.IGenWrapper[dao.IAccountEntityDo]) {
        w.LLike(dao.AccountEntity.Username, username).
          WhereIf(role != 0, dao.AccountEntity.Role.Eq(role))
    }).Apply().Find()
```

---

## 五、多租户插件（自动注入）

注册一次，所有数据库操作自动注入租户条件，业务代码零改动。

### 用法一：单字段（向后兼容）

```go
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:   "tenant_id",
    ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
})

// 中间件写入
func TenantMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        tenantID := int64(1001) // 从 JWT 解析
        ctx := gormplus.WithTenantID(c.Request.Context(), tenantID)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}

// 业务代码零改动
db.WithContext(ctx).Find(&list)      // WHERE `tenant_id` = 1001
db.WithContext(ctx).Create(&account) // 自动填充 tenant_id 字段
```

### 用法二：同一张表注入多个租户字段

```go
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantFields: []gormplus.TenantFieldConfig[int64]{
        {Field: "tenant_id"}, // 使用默认 WithTenantID 写入的值
        {Field: "org_id", GetTenantID: func(ctx context.Context) (int64, bool) {
            id, ok := ctx.Value("orgID").(int64)
            return id, ok && id != 0
        }},
    },
})

// 中间件同时写入两个值
ctx := gormplus.WithTenantID(c.Request.Context(), int64(1001))
ctx  = context.WithValue(ctx, "orgID", int64(200))

// 生成：WHERE `tenant_id` = 1001 AND `org_id` = 200
```

### 用法三：不同表用不同字段名

```go
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField: "tenant_id", // 兜底字段
    TableFields: map[string][]gormplus.TenantFieldConfig[int64]{
        "sys_contract": {{Field: "company_id"}},          // 改用 company_id
        "sys_order": {                                    // 同时注入两个字段
            {Field: "tenant_id"},
            {Field: "org_id", GetTenantID: orgGetter},
        },
        "sys_log": {}, // 空 slice = 跳过该表
    },
    ExcludeTables: []string{"sys_config", "sys_dict"},
})

// 查询 sys_contract：WHERE `company_id` = 1001
// 查询 sys_order：  WHERE `tenant_id` = 1001 AND `org_id` = 200
// 查询 sys_log：    无租户条件（跳过）
// 查询其他表：      WHERE `tenant_id` = 1001（兜底）
```

### 联表查询（JOIN 自动注入，别名自动识别）

```go
// 零配置，直接写 JOIN，关联表和别名自动处理
db.WithContext(ctx).
    Table("sys_order a").
    Joins("LEFT JOIN sys_order_item b ON b.order_id = a.id").
    Joins("LEFT JOIN sys_user u ON u.id = a.user_id").
    Find(&list)
// 自动生成：
// WHERE `a`.`tenant_id` = 1001
//   AND `b`.`tenant_id` = 1001   ← 别名 b 自动识别
//   AND `u`.`tenant_id` = 1001   ← 别名 u 自动识别

// 排除不需要租户过滤的公共关联表
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:       "tenant_id",
    ExcludeJoinTables: []string{"sys_dict", "sys_config"},
})

// 关联表字段名不同时覆盖（仅需配置差异部分）
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField: "tenant_id",
    JoinTableOverrides: []gormplus.JoinTenantConfig[int64]{
        {Table: "sys_contract_detail", Field: "company_id"},
    },
})

// 关闭 JOIN 自动注入
falseVal := false
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:          "tenant_id",
    AutoInjectJoinTables: &falseVal,
})
```

### 安全保护

```go
// ① 默认禁止无业务条件的全表 Update / Delete
db.WithContext(ctx).Model(&Account{}).Updates(map[string]any{"status": 0})
// Error: tenant: 禁止无业务条件的全表 Update（表: account）

// 加业务条件才允许
db.WithContext(ctx).Model(&Account{}).Where("dept_id = ?", deptID).Updates(...)

// 临时放开（批量任务、数据迁移）
ctx = gormplus.AllowGlobalOperation(ctx)
db.WithContext(ctx).Model(&Account{}).Updates(map[string]any{"status": 0})

// 配置层永久放开
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:       "tenant_id",
    AllowGlobalUpdate: true,
    AllowGlobalDelete: true,
})

// ② 重复条件策略（默认 PolicySkip）
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:     "tenant_id",
    DuplicatePolicy: gormplus.PolicySkip,    // 默认：已有 AND 条件时跳过注入
    // DuplicatePolicy: gormplus.PolicyReplace, // 强制替换为 ctx 中的值
    // DuplicatePolicy: gormplus.PolicyAppend,  // 直接追加不检查
})

// ③ OR 危险条件自动拒绝
db.WithContext(ctx).Where("tenant_id = ? OR status = 1", 9999).Find(&list)
// Error: tenant: 检测到租户字段 "tenant_id" 出现在 OR 条件中，已拒绝执行
```

### 覆盖租户 ID / 超管跳过

```go
// 覆盖租户 ID（需开启 AllowOverrideTenantID）
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:           "tenant_id",
    AllowOverrideTenantID: true,
})
ctx = gormplus.WithOverrideTenantID(ctx, int64(2002))
db.WithContext(ctx).Find(&list) // WHERE tenant_id = 2002

// 超管跳过所有租户过滤
ctx = gormplus.SkipTenant(ctx)
db.WithContext(ctx).Find(&all) // 无任何租户条件

// 动态维护排除表
gormplus.AddExcludeTable[int64](db, "log_audit")
gormplus.RemoveExcludeTable[int64](db, "sys_dict")
tables, _ := gormplus.ExcludedTables[int64](db)
```

---

## 六、数据权限插件

注入逻辑由业务层定义，插件不耦合任何业务 SQL。

```go
// 注册
gormplus.RegisterDataPermission(db, gormplus.DataPermissionConfig{
    ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
})

// 中间件定义注入函数
func DataPermissionMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, err := jwt.ParseToken(c.GetHeader("Authorization"))
        if err != nil { c.Next(); return }
        injectFn := func(db *gorm.DB, tableName string) {
            switch claims.DataScope {
            case "2": // 本角色相关部门
                db.Where(tableName+".create_by IN (SELECT sys_user.user_id FROM sys_role_dept LEFT JOIN sys_user ON sys_user.dept_id = sys_role_dept.dept_id WHERE sys_role_dept.role_id = ?)", claims.RoleId)
            case "3": // 本部门
                db.Where(tableName+".create_by IN (SELECT user_id FROM sys_user WHERE dept_id = ?)", claims.DeptId)
            case "4": // 本部门及子部门
                db.Where(tableName+".create_by IN (SELECT user_id FROM sys_user WHERE dept_id IN (SELECT dept_id FROM sys_dept WHERE dept_path LIKE ?))", "%/"+strconv.FormatInt(claims.DeptId, 10)+"/%")
            case "5": // 仅本人
                db.Where(tableName+".create_by = ?", claims.UserId)
            }
        }
        ctx := gormplus.WithDataPermission(c.Request.Context(), injectFn)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}

// 业务代码零改动
db.WithContext(ctx).Find(&list) // 自动注入数据权限条件

// 超管跳过
ctx = gormplus.SkipDataPermission(ctx)
db.WithContext(ctx).Find(&allData)
```

---

## 七、自动填充插件

```go
// 中间件写入操作人信息
func OperatorMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, _ := jwt.ParseToken(c.GetHeader("Authorization"))
        ctx := context.WithValue(c.Request.Context(), gormplus.CtxContextKey1, claims.UserID)   // 操作人 ID
        ctx  = context.WithValue(ctx,                 gormplus.CtxContextKey2, claims.Username) // 操作人姓名
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}

// 注册插件
db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
    Fields: []gormplus.FieldConfig{
        {Name: "CreatedBy",   Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1),  OnCreate: true},
        {Name: "UpdatedBy",   Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1),  OnCreate: true, OnUpdate: true},
        {Name: "CreatedName", Getter: gormplus.CtxGetter[string](gormplus.CtxContextKey2), OnCreate: true},
        {Name: "UpdatedName", Getter: gormplus.CtxGetter[string](gormplus.CtxContextKey2), OnCreate: true, OnUpdate: true},
    },
}))

// 业务代码零改动
db.WithContext(ctx).Create(&account)              // CreatedBy / CreatedName 自动填充
db.WithContext(ctx).Model(&account).Updates(data) // UpdatedBy / UpdatedName 自动填充
```

---

## 八、SingleFlight + 可插拔缓存（SF）

### 方式一：内存缓存（默认，零配置）

```go
// 无需任何配置，直接使用
defer gormplus.StopSFCache() // 退出时停止后台清理 goroutine

// 带缓存（30 秒）
list, err := gormplus.SF(func() ([]*model.Account, error) {
    var result []*model.Account
    err := gormplus.Query[*model.Account](db, ctx).
        WhereIf(status != 0, "status = ?", status).
        Build().Find(&result)
    return result, err
}, "Account.List", map[string]any{"status": status, "page": pageNum}, 30*time.Second)

// 纯 singleflight（不缓存，只合并并发）
account, err := gormplus.SFNoCache(func() (*model.Account, error) {
    var a model.Account
    err := db.WithContext(ctx).Where("id = ?", id).First(&a).Error
    return &a, err
}, "Account.Detail", map[string]any{"id": id})

// 写操作后主动失效缓存
gormplus.SFInvalidate("Account.List", map[string]any{"status": status})
```

### 方式二：Redis 缓存（多实例部署推荐）

```go
// 实现 SFCache 接口
type RedisSFCache struct {
    rdb    *redis.Client
    prefix string
}

func (c *RedisSFCache) Get(key string) (any, bool) {
    val, err := c.rdb.Get(context.Background(), c.prefix+key).Bytes()
    if err != nil { return nil, false }
    var result any
    if err := json.Unmarshal(val, &result); err != nil { return nil, false }
    return result, true
}
func (c *RedisSFCache) Set(key string, val any, ttl time.Duration) {
    b, _ := json.Marshal(val)
    c.rdb.Set(context.Background(), c.prefix+key, b, ttl)
}
func (c *RedisSFCache) Del(key string) {
    c.rdb.Del(context.Background(), c.prefix+key)
}

// 启动时注册（必须在第一次调用 SF 之前）
gormplus.RegisterCache(&RedisSFCache{rdb: rdb, prefix: "myapp:sf:"})

// 业务代码与内存缓存完全一致，无需任何改动
list, err := gormplus.SF(fn, "Account.List", args, 30*time.Second)
```

| | 内存缓存（默认） | Redis 缓存 |
|---|---|---|
| 配置 | 零配置 | 启动时 `RegisterCache` 一次 |
| 适用 | 单机、开发测试 | 多实例部署、缓存共享 |
| 退出清理 | `defer StopSFCache()` | 用户自行管理 Redis 连接 |
| 业务代码 | 完全一样 | 完全一样 |

### 缓存 TTL 建议

| 场景 | 推荐 TTL |
|------|---------|
| 列表 / 统计查询 | 3s ~ 30s |
| 配置 / 字典数据 | 1min ~ 5min |
| 详情 / 实时数据 | 0（SFNoCache）|

---

## 九、慢查询监控

```go
gormplus.RegisterSlowQuery(db, gormplus.SlowQueryConfig{
    Threshold: 200 * time.Millisecond, // 超过此阈值记录，0 时自动设为 200ms
    Logger: func(ctx context.Context, info gormplus.SlowQueryInfo) {
        zap.L().Warn("慢查询",
            zap.Duration("cost",  info.Duration),
            zap.String("table",   info.Table),
            zap.String("sql",     info.SQL),       // 已替换 ?，可直接 EXPLAIN
            zap.Int64("rows",     info.RowsAffected),
            zap.Error(info.Error),
        )
    },
})
```

---

## 十、代码生成器

```yaml
# generator.yaml
host: localhost
port: 3306
username: root
password: your_password
database: your_database
out_path: ./dal/query
model_pkg_path: ./dal/model
repo_path: ./dal/repository
api_path: ./api/desc
vo_path: ./api/vo
dto_path: ./api/dto
package: your_package
```

```go
cfg, err := gormplus.LoadGeneratorConfig("./generator.yaml")
if err != nil { log.Fatal(err) }

if err := gormplus.Generate(cfg); err != nil {
    log.Fatal(err)
}
// 运行后提示输入表名：
// - 输入表名：只生成该表的 Model / Repository / API / VO / DTO
// - 直接回车：生成所有表的 Model（其他文件不生成）
```

> **注意**：数据模型（Model）每次都会重新生成覆盖；Repository / API / VO / DTO 文件已存在时自动跳过，不会覆盖已有的自定义代码。

---

## 依赖

- `gorm.io/gorm`
- `gorm.io/gen`
- `gopkg.in/yaml.v3`
- 数据库驱动由用户按需引入（`gorm.io/driver/mysql`、`gorm.io/driver/postgres` 等）
