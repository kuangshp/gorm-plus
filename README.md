# gorm-plus

基于 [gorm](https://gorm.io) 和 [gorm-gen](https://github.com/go-gorm/gen) 的增强扩展包。

提供：**链式条件构造器** · **gorm-gen 类型安全扩展** · **多租户自动注入** · **数据权限自动注入** · **自动填充插件** · **多数据源管理** · **SingleFlight 可插拔缓存** · **慢查询监控** · **代码生成器**

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
│   ├── ctx.go            # ctx 解析器（屏蔽 gin/go-zero/fiber 框架差异）
│   ├── tenant.go         # 多租户插件
│   ├── dataPermission.go # 数据权限插件
│   └── autoOperator.go   # 自动填充插件
├── datasource/
│   └── manager.go        # 多数据源管理（主从分离、读写分离）
├── sf/
│   └── sf.go             # SingleFlight + 可插拔缓存（防缓存击穿）
└── generator/            # 代码生成器
```

---

## 快速开始（推荐初始化顺序）

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/gin-gonic/gin"
    gormplus "github.com/kuangshp/gorm-plus"
    "gorm.io/driver/mysql"
    "gorm.io/gorm"
)

func main() {
    // ① 注册 ctx 解析器（gin 项目必须；go-zero / fiber 跳过）
    gormplus.RegisterCtxResolver(func(ctx context.Context) context.Context {
        if ginCtx, ok := ctx.(*gin.Context); ok {
            return ginCtx.Request.Context()
        }
        return ctx
    })

    // ② 打开 DB
    db, err := gorm.Open(mysql.Open("root:pwd@tcp(127.0.0.1:3306)/mydb?charset=utf8mb4&parseTime=True"), &gorm.Config{})
    if err != nil { log.Fatal(err) }

    // ③ 注册多租户插件
    gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
        TenantField:   "tenant_id",
        ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
    })

    // ④ 注册数据权限插件
    gormplus.RegisterDataPermission(db, gormplus.DataPermissionConfig{
        ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
    })

    // ⑤ 注册自动填充插件
    db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
        Fields: []gormplus.FieldConfig{
            {Name: "CreatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true},
            {Name: "UpdatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true, OnUpdate: true},
        },
    }))

    // ⑥ 注册慢查询监控
    gormplus.RegisterSlowQuery(db, gormplus.SlowQueryConfig{
        Threshold: 200 * time.Millisecond,
        Logger: func(ctx context.Context, info gormplus.SlowQueryInfo) {
            log.Printf("[慢查询] cost=%v table=%s sql=%s", info.Duration, info.Table, info.SQL)
        },
    })

    // ⑦ 注册多数据源
    gormplus.DS.Register("default", gormplus.DataSourceGroupConfig{
        Master: gormplus.DataSourceNodeConfig{DSN: "root:pwd@tcp(master:3306)/mydb?charset=utf8mb4&parseTime=True"},
        Slaves: []gormplus.DataSourceNodeConfig{
            {DSN: "root:pwd@tcp(slave:3306)/mydb?charset=utf8mb4&parseTime=True"},
        },
    })

    // ⑧ 注册路由中间件
    r := gin.New()
    r.Use(OperatorMiddleware())
    r.Use(TenantMiddleware())
    r.Use(DataPermissionMiddleware())

    // ⑨ 优雅退出
    defer gormplus.StopSFCache()
    defer gormplus.DS.Close()

    r.Run(":8080")
}
```

---

## 一、ctx 解析器（多框架兼容）

插件读取 ctx 数据前会先调用解析器，屏蔽不同框架的 ctx 类型差异。

| 框架 | ctx 类型 | 是否需要注册 | 业务代码传 ctx 方式 |
|------|---------|------------|-------------------|
| gin | `*gin.Context` | **必须注册** | `db.WithContext(c)` 直接传 |
| go-zero | 标准 `context.Context` | 无需注册 | `db.WithContext(r.Context())` |
| fiber | `c.UserContext()` 是标准 context | 无需注册 | `db.WithContext(c.UserContext())` |

```go
// gin 项目启动时注册（一次）
gormplus.RegisterCtxResolver(func(ctx context.Context) context.Context {
    if ginCtx, ok := ctx.(*gin.Context); ok {
        return ginCtx.Request.Context()
    }
    return ctx
})

// 注册后业务代码直接传 *gin.Context，无需 c.Request.Context()
db.WithContext(c).Find(&list)        // ✅
dao.Entity.WithContext(c).Find()     // ✅ gorm-gen 也支持
```

---

## 二、原生 gorm 链式条件构造器（Query）

```go
// 分页列表查询
built := gormplus.Query[*model.Account](db, ctx).
    LLike("username", username).                        // 空时自动跳过
    WhereIf(status != 0, "status = ?", status).         // false 时跳过
    BetweenIfNotZero("created_at", startTime, endTime). // 任一零值时跳过
    WhereIf(len(deptIDs) > 0, "dept_id IN ?", deptIDs).
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

// 联表 + 映射到 VO
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

// 条件分组
built = gormplus.Query[*model.Account](db, ctx).
    WhereIf(orgID != 0, "org_id = ?", orgID).
    WhereGroup(func(q gormplus.IQueryBuilder) {
        q.Like("username", keyword).
          RawOrWhere("email LIKE ?", "%"+keyword+"%")
    }).
    Build()
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

## 三、gorm-gen 类型安全链式构造器（GenWrap）

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
    LLike(dao.AccountEntity.Username, username).
    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
    Apply().
    Select(dao.AccountEntity.ID, dao.AccountEntity.Username).
    Find()

// 简单分组：固定条件
list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
    WhereGroup(
        dao.AccountEntity.Status.Eq(1),
        dao.AccountEntity.Role.Eq(2),
    ).Apply().Find()
// => WHERE (status = 1 AND role = 2)

// 函数分组：动态条件（组内可用 WhereIf / Like 等完整能力）
list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
    WhereGroupFn(func(w gormplus.IGenWrapper[dao.IAccountEntityDo]) {
        w.LLike(dao.AccountEntity.Username, username).
          WhereIf(status != 0, dao.AccountEntity.Status.Eq(status))
    }).Apply().Find()
// => WHERE (username LIKE '%admin' AND status = 1)

// OR 函数分组
list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
    WhereIf(true, dao.AccountEntity.Status.Eq(1)).
    OrGroupFn(func(w gormplus.IGenWrapper[dao.IAccountEntityDo]) {
        w.LLike(dao.AccountEntity.Username, username).
          WhereIf(role != 0, dao.AccountEntity.Role.Eq(role))
    }).Apply().Find()
// => WHERE status = 1 OR (username LIKE '%admin' AND role = 99)
```

---

## 四、多租户插件（自动注入）

注册一次，所有数据库操作自动注入租户条件，业务代码零改动。

### 用法一：单字段（向后兼容）

```go
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:   "tenant_id",
    ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
})
```

中间件写入：

```go
func TenantMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        tenantID := int64(1001) // 从 JWT 解析
        ctx := gormplus.WithTenantID(c.Request.Context(), tenantID)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

业务代码零改动：

```go
db.WithContext(ctx).Find(&list)      // WHERE `tenant_id` = 1001
db.WithContext(ctx).Create(&account) // 自动填充 tenant_id 字段
```

### 用法二：多字段（同一张表注入多个字段）

```go
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantFields: []gormplus.TenantFieldConfig[int64]{
        {Field: "tenant_id"},
        {Field: "org_id", GetTenantID: func(ctx context.Context) (int64, bool) {
            id, ok := ctx.Value("orgID").(int64)
            return id, ok && id != 0
        }},
    },
    ExcludeTables: []string{"sys_config"},
})

// 中间件写入两个值
ctx := gormplus.WithTenantID(c.Request.Context(), int64(1001))
ctx  = context.WithValue(ctx, "orgID", int64(200))

// 生成：WHERE `tenant_id` = 1001 AND `org_id` = 200
```

### 用法三：不同表用不同字段名

```go
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField: "tenant_id", // 兜底字段
    TableFields: map[string][]gormplus.TenantFieldConfig[int64]{
        "sys_contract": {{Field: "company_id"}},    // 该表改用 company_id
        "sys_order": {                              // 该表同时注入两个字段
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

默认所有 JOIN 关联表自动注入租户条件，别名从语句中自动解析，零配置：

```go
db.WithContext(ctx).
    Table("sys_order a").
    Joins("LEFT JOIN sys_order_item b ON b.order_id = a.id").
    Joins("LEFT JOIN sys_user u ON u.id = a.user_id").
    Find(&list)
// 自动生成：
// WHERE `a`.`tenant_id` = 1001
//   AND `b`.`tenant_id` = 1001  ← 别名 b 自动识别
//   AND `u`.`tenant_id` = 1001  ← 别名 u 自动识别

// 排除不需要租户过滤的关联表（公共字典表）
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:       "tenant_id",
    ExcludeJoinTables: []string{"sys_dict", "sys_config"},
})

// 关联表字段名不同时，通过 JoinTableOverrides 覆盖
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField: "tenant_id",
    JoinTableOverrides: []gormplus.JoinTenantConfig[int64]{
        {Table: "sys_contract_detail", Field: "company_id"},
    },
})

// 关闭自动注入
falseVal := false
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:          "tenant_id",
    AutoInjectJoinTables: &falseVal,
})
```

### 安全保护

```go
// 默认禁止无业务条件的全表 Update / Delete
db.WithContext(ctx).Model(&Account{}).Updates(map[string]any{"status": 0})
// Error: tenant: 禁止无业务条件的全表 Update（表: account）

// 加了业务条件才允许
db.WithContext(ctx).Model(&Account{}).Where("dept_id = ?", deptID).Updates(...)

// 临时放开（批量任务、数据迁移）
ctx = gormplus.AllowGlobalOperation(ctx)
db.WithContext(ctx).Model(&Account{}).Updates(map[string]any{"status": 0})

// 配置层永久允许
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:       "tenant_id",
    AllowGlobalUpdate: true,
    AllowGlobalDelete: true,
})
```

### 重复条件策略（DuplicateTenantPolicy）

```go
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:     "tenant_id",
    DuplicatePolicy: gormplus.PolicySkip,    // 默认：发现已有 AND 条件时跳过注入
    // DuplicatePolicy: gormplus.PolicyReplace, // 强制以 ctx 值替换业务代码写的租户条件
    // DuplicatePolicy: gormplus.PolicyAppend,  // 不检查直接追加（性能最好）
})

// PolicySkip（默认）：业务代码写了租户条件时不重复注入
db.WithContext(ctx).Where("tenant_id = ?", 1001).Find(&list)
// 生成：WHERE tenant_id = 1001（不重复）

// PolicyReplace：强制以 ctx 值覆盖（防业务代码写错误值）
db.WithContext(ctx).Where("tenant_id = ?", 9999).Find(&list)
// 生成：WHERE tenant_id = 1001（替换为 ctx 中的正确值）
```

### 覆盖租户 ID（切换租户查询）

```go
// 注册时开启
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:           "tenant_id",
    AllowOverrideTenantID: true,
})

// 超管管理后台：查看租户 2002 的数据
ctx = gormplus.WithOverrideTenantID(ctx, int64(2002))
db.WithContext(ctx).Find(&list) // WHERE tenant_id = 2002
```

### 其他操作

```go
// 超管跳过所有租户过滤
ctx = gormplus.SkipTenant(ctx)
db.WithContext(ctx).Find(&all) // 无任何租户条件

// 动态维护排除表
gormplus.AddExcludeTable[int64](db, "log_audit")
gormplus.RemoveExcludeTable[int64](db, "sys_dict")
tables, _ := gormplus.ExcludedTables[int64](db)
```

---

## 五、数据权限插件（按角色/部门隔离）

注入逻辑由业务层在中间件中定义，插件不耦合任何业务 SQL。

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

## 六、自动填充插件（创建人/更新人）

```go
// 中间件写入操作人信息
func OperatorMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, _ := jwt.ParseToken(c.GetHeader("Authorization"))
        ctx := context.WithValue(c.Request.Context(), gormplus.CtxContextKey1, claims.AccountId)   // 操作人 ID
        ctx  = context.WithValue(ctx,                 gormplus.CtxContextKey2, claims.AccountName) // 操作人姓名
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
db.WithContext(ctx).Create(&account)         // CreatedBy / UpdatedBy 自动填充
db.WithContext(ctx).Model(&account).Updates(data) // UpdatedBy 自动填充
```

---

## 七、多数据源管理（主从分离）

```go
// 注册数据源
gormplus.DS.Register("default", gormplus.DataSourceGroupConfig{
    Master: gormplus.DataSourceNodeConfig{
        DSN:  "root:pwd@tcp(master:3306)/mydb?charset=utf8mb4&parseTime=True",
        Pool: gormplus.DataSourcePoolConfig{MaxOpen: 50, MaxIdle: 10},
    },
    Slaves: []gormplus.DataSourceNodeConfig{
        {DSN: "root:pwd@tcp(slave1:3306)/mydb?charset=utf8mb4&parseTime=True"},
        {DSN: "root:pwd@tcp(slave2:3306)/mydb?charset=utf8mb4&parseTime=True"},
    },
})

// 中间件自动标记读写
func DSMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx := gormplus.DSWithName(c.Request.Context(), "default")
        if c.Request.Method == http.MethodGet {
            ctx = gormplus.DSWithRead(ctx)  // GET → 从库
        } else {
            ctx = gormplus.DSWithWrite(ctx) // 其他 → 主库
        }
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}

// Repository 层自动获取 DB
func (r *AccountRepo) List(ctx context.Context) ([]*model.Account, error) {
    db, err := gormplus.DS.Auto(ctx) // 自动：数据源=default，读=从库
    if err != nil { return nil, err }
    var list []*model.Account
    return list, db.WithContext(ctx).Find(&list).Error
}

// 健康检查
results := gormplus.DS.Ping()
// {"default:master": nil, "default:slave0": nil}
```

---

## 八、SingleFlight + 可插拔缓存（SF）

### 方式一：内存缓存（默认，零配置）

```go
// 直接使用，无需任何配置
defer gormplus.StopSFCache() // 退出时停止后台清理

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

### 方式二：Redis 缓存（实现接口后注册）

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

// 启动时注册（一次）
gormplus.RegisterCache(&RedisSFCache{rdb: rdb, prefix: "myapp:sf:"})

// 业务代码与内存缓存完全相同，无需任何改动
list, err := gormplus.SF(fn, "Account.List", args, 30*time.Second)
```

| | 内存缓存（默认） | Redis 缓存 |
|---|---|---|
| 配置 | 无需任何配置 | 启动时 `RegisterCache` 一次 |
| 适用 | 单机、开发测试 | 多实例部署、缓存共享 |
| 退出清理 | `defer StopSFCache()` | 用户自行管理 Redis 连接 |
| 业务代码 | 完全一样 | 完全一样 |

### 缓存策略建议

| 场景 | 推荐 TTL |
|------|---------|
| 列表/统计查询 | 3s ~ 30s |
| 配置/字典数据 | 1min ~ 5min |
| 详情/实时数据 | 0（SFNoCache）|

---

## 九、慢查询监控

```go
gormplus.RegisterSlowQuery(db, gormplus.SlowQueryConfig{
    Threshold: 200 * time.Millisecond,
    Logger: func(ctx context.Context, info gormplus.SlowQueryInfo) {
        zap.L().Warn("慢查询",
            zap.Duration("cost",  info.Duration),
            zap.String("table",   info.Table),
            zap.String("sql",     info.SQL),       // 已替换 ? 为实际参数，可直接 EXPLAIN
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
```

---

## 依赖

- `gorm.io/gorm`
- `gorm.io/gen`
- `gorm.io/driver/mysql`
- `gopkg.in/yaml.v3`
