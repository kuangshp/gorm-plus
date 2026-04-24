# gorm-plus

基于 [gorm](https://gorm.io) 和 [gorm-gen](https://github.com/go-gorm/gen) 的增强扩展包。

提供：**链式条件构造器** · **gorm-gen 类型安全扩展** · **多租户自动注入** · **数据权限自动注入** · **自动填充插件** · **多数据源管理** · **SingleFlight 缓存** · **慢查询监控** · **代码生成器**

---

## 安装

```bash
go get github.com/kuangshp/gorm-plus
```

---

## 目录结构

```
gorm-plus/
├── gormplus.go          # 统一入口，所有功能的导出
├── query/
│   ├── query_builder.go # IQueryBuilder：原生 gorm 链式条件构造器
│   ├── gen_wrapper.go   # IGenWrapper：gorm-gen 类型安全链式构造器
│   ├── slow_query.go    # 慢查询监控 gorm 插件
│   └── utils.go         # 工具函数
├── plugin/
│   ├── ctx.go           # ctx 解析器（屏蔽 gin/go-zero/fiber 框架差异）
│   ├── tenant.go        # 多租户插件（自动注入 WHERE tenant_id = ?）
│   ├── dataPermission.go# 数据权限插件（按角色/部门隔离数据）
│   └── autoOperator.go  # 自动填充插件（创建人/更新人自动写入）
├── datasource/
│   └── manager.go       # 多数据源管理（主从分离、读写分离）
├── sf/
│   └── sf.go            # SingleFlight + 内存缓存（防缓存击穿）
├── generator/
│   ├── generator.go     # 代码生成器
│   ├── config.go        # 配置加载
│   └── template/        # 代码模板
└── version.go
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

    // ② 打开 gorm DB
    db, err := gorm.Open(mysql.Open("root:pwd@tcp(127.0.0.1:3306)/mydb?charset=utf8mb4&parseTime=True"), &gorm.Config{})
    if err != nil {
        log.Fatal(err)
    }

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
            {Name: "CreatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxOperatorKey1), OnCreate: true},
            {Name: "UpdatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxOperatorKey1), OnCreate: true, OnUpdate: true},
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
    r.Use(OperatorMiddleware())       // 写入操作人 ID
    r.Use(TenantMiddleware())         // 写入租户 ID
    r.Use(DataPermissionMiddleware()) // 写入数据权限

    // ⑨ 优雅退出
    defer gormplus.StopSFCache()
    defer gormplus.DS.Close()

    r.Run(":8080")
}
```

---

## 一、ctx 解析器（多框架兼容）

插件读取 ctx 数据前会先调用解析器，屏蔽不同框架的 ctx 类型差异，让业务代码直接传 ctx 无需手动转换。

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

// 注册后，业务代码直接传 *gin.Context，无需 c.Request.Context()
db.WithContext(c).Find(&list)     // ✅ 直接传 gin.Context
dao.Entity.WithContext(c).Find()  // ✅ gorm-gen 也支持
```

---

## 二、原生 gorm 链式条件构造器（Query）

替代手动 if 判断，链式拼装条件后调用 `Build()` 返回 `*gorm.DB`。

```go
// 分页列表查询
built := gormplus.Query[*model.Account](db, ctx).
    LLike("username", username).                       // 空时自动跳过
    WhereIf(status != 0, "status = ?", status).        // false 时跳过
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

// 联表查询 + 映射到 VO
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
built := gormplus.Query[*model.Account](db, ctx).
    WhereIf(orgID != 0, "org_id = ?", orgID).
    WhereGroup(func(q gormplus.IQueryBuilder) {
        // 组内：username LIKE '%kw%' OR email LIKE '%kw%'
        q.Like("username", keyword).
          RawOrWhere("email LIKE ?", "%"+keyword+"%")
    }).
    Build()
```

| 方法 | 说明 | 生效条件 |
|------|------|---------|
| `Like(col, val)` | `WHERE col LIKE '%val%'` | val 非空 |
| `LLike(col, val)` | `WHERE col LIKE '%val'` | val 非空 |
| `RLike(col, val)` | `WHERE col LIKE 'val%'` | val 非空 |
| `BetweenIfNotZero(col, min, max)` | `WHERE col BETWEEN min AND max` | min 和 max 均非零 |
| `WhereIf(cond, sql, args...)` | 追加 AND 条件 | cond 为 true |
| `WhereGroup(fn)` | AND 括号分组 | fn 内有条件 |
| `OrGroup(fn)` | OR 括号分组 | fn 内有条件 |
| `RawWhere(sql, args...)` | 追加原生 AND 条件 | 无条件 |
| `RawOrWhere(sql, args...)` | 追加原生 OR 条件 | 无条件 |
| `RawWhereIf(cond, sql, args...)` | 追加原生 AND 条件 | cond 为 true |
| `Build()` | 返回 `*gorm.DB` | - |

---

## 三、gorm-gen 类型安全链式构造器（GenWrap）

在 gorm-gen 生成的 DO 上扩展模糊查询、条件分组等能力，调用 `Apply()` 后返回原生 DO。

```go
// 基础查询
list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
    LLike(dao.AccountEntity.Username, username).
    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
    Apply().
    Order(dao.AccountEntity.CreatedAt.Desc()).
    Limit(pageSize).Offset((page-1)*pageSize).
    Find()

// 联表查询（使用别名 + gorm-gen 原生 Select / Joins）
// Apply() 返回原生 DO 后，Select 和 Joins 使用 gorm-gen 生成的类型安全字段
list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
    As("a").
    LLike(dao.AccountEntity.Username, username).
    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
    Apply().
    // Select 使用 gorm-gen 生成的 field.Expr，保持类型安全
    Select(dao.AccountEntity.ID, dao.AccountEntity.Username, dao.SysDept.Name.As("dept_name")).
    // Joins 使用 gorm-gen 生成的关联方法
    Joins(dao.AccountEntity.SysDept.On(dao.AccountEntity.DeptID.EqCol(dao.SysDept.ID))).
    Find()

// 如果联表字段在 gorm-gen 中没有定义关联，可以混用原生 gorm 字符串写法：
// Apply() 后返回的是 *gorm.DO，可以直接调用底层 gorm 的 Joins
list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
    LLike(dao.AccountEntity.Username, username).
    WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
    Apply().
    // 扫描到自定义 VO 时建议用 Scan，此时 Select 传字符串也可以
    Select(dao.AccountEntity.ID, dao.AccountEntity.Username).
    Find()

// 联表 + 扫描到 VO（推荐用 Query + ScanByPage，而非 GenWrap）
// gorm-gen 联表查询更推荐在 dao 层定义好关联后直接使用，
// 或通过原生 Query 构造器处理复杂联表场景
type AccountVO struct {
    ID       int64  `json:"id"`
    Username string `json:"username"`
    DeptName string `json:"deptName"`
}
list, total, err := gormplus.ScanByPage[AccountVO](
    gormplus.Query[*model.Account](db, ctx).
        LLike("a.username", username).
        WhereIf(status != 0, "a.status = ?", status).
        Build().
        Select("a.id", "a.username", "d.name AS dept_name").
        Joins("LEFT JOIN sys_dept d ON d.id = a.dept_id").
        Order("a.created_at DESC"),
    pageNum, pageSize,
)

// 简单分组（条件固定）
list, err := gormplus.GenWrap(dao.AccountEntity.WithContext(ctx)).
    WhereGroup(
        dao.AccountEntity.Status.Eq(1),
        dao.AccountEntity.Role.Eq(2),
    ).Apply().Find()
// => WHERE (status = 1 AND role = 2)

// 函数分组（组内可用 WhereIf / Like 等完整能力）
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

| 方法 | 说明 |
|------|------|
| `As(alias)` | 设置表别名，用于联表查询 |
| `Like / LLike / RLike` | 模糊查询，值为空自动跳过 |
| `BetweenIfNotZero` | 范围查询，零值自动跳过 |
| `WhereIf(cond, exprs...)` | 条件成立时追加 AND |
| `WhereGroup(exprs...)` | AND 括号分组（固定条件） |
| `OrGroup(exprs...)` | OR 括号分组（固定条件） |
| `WhereGroupFn(fn)` | AND 括号分组（动态条件，组内可用全部能力） |
| `OrGroupFn(fn)` | OR 括号分组（动态条件，组内可用全部能力） |
| `RawWhere / RawOrWhere / RawWhereIf` | 原生 SQL 条件 |
| `Apply()` | 返回原生 DO，继续使用 gorm-gen 原生方法 |

---

## 四、多租户插件（自动注入）

注册一次，所有数据库操作自动注入租户条件，业务代码零改动。

### 注册

```go
// int64 类型租户 ID
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
    TenantField:   "tenant_id",
    ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
})

// string 类型租户 ID
gormplus.RegisterTenant(db, gormplus.TenantConfig[string]{
    TenantField:   "tenant_id",
    ExcludeTables: []string{"sys_config"},
})
```

### 中间件写入租户 ID

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

### 业务代码零改动

```go
db.WithContext(ctx).Find(&list)       // 自动加 WHERE tenant_id = 1001
db.WithContext(ctx).Create(&account)  // 自动填充 tenant_id 字段
db.WithContext(ctx).Save(&account)    // 自动加 WHERE tenant_id = 1001
```

### 超管跳过 / 动态排除表

```go
// 超管跳过租户过滤
ctx = gormplus.SkipTenant(ctx)
db.WithContext(ctx).Find(&allData) // 无 tenant_id 条件

// 动态维护排除表
gormplus.AddExcludeTable[int64](db, "log_audit")
gormplus.RemoveExcludeTable[int64](db, "sys_dict")
```

---

## 五、数据权限插件（按角色/部门隔离）

注入逻辑由业务层在中间件中定义，插件本身不耦合任何业务 SQL。

### 注册

```go
gormplus.RegisterDataPermission(db, gormplus.DataPermissionConfig{
    ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
})
```

### 中间件定义注入函数

```go
func DataPermissionMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, err := jwt.ParseToken(c.GetHeader("Authorization"))
        if err != nil {
            c.Next()
            return
        }
        injectFn := func(db *gorm.DB, tableName string) {
            switch claims.DataScope {
            case "2": // 本角色相关部门的用户数据
                db.Where(tableName+".create_by IN (SELECT sys_user.user_id FROM sys_role_dept LEFT JOIN sys_user ON sys_user.dept_id = sys_role_dept.dept_id WHERE sys_role_dept.role_id = ?)", claims.RoleId)
            case "3": // 本部门数据
                db.Where(tableName+".create_by IN (SELECT user_id FROM sys_user WHERE dept_id = ?)", claims.DeptId)
            case "4": // 本部门及子部门数据
                db.Where(tableName+".create_by IN (SELECT user_id FROM sys_user WHERE dept_id IN (SELECT dept_id FROM sys_dept WHERE dept_path LIKE ?))", "%/"+strconv.FormatInt(claims.DeptId, 10)+"/%")
            case "5": // 仅本人数据
                db.Where(tableName+".create_by = ?", claims.UserId)
            // default: 全部数据，不加条件
            }
        }
        ctx := gormplus.WithDataPermission(c.Request.Context(), injectFn)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

### 业务代码零改动

```go
db.WithContext(ctx).Find(&list) // 自动注入数据权限条件

// 超管跳过
ctx = gormplus.SkipDataPermission(ctx)
db.WithContext(ctx).Find(&allData)
```

---

## 六、自动填充插件（创建人/更新人）

在 Create / Update 操作前自动从 context 填充指定字段，无需业务代码手动赋值。

### 中间件写入操作人信息

```go
func OperatorMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, _ := jwt.ParseToken(c.GetHeader("Authorization"))
        ctx := context.WithValue(c.Request.Context(), gormplus.CtxOperatorKey1, claims.AccountId)   // 操作人 ID
        ctx  = context.WithValue(ctx,                 gormplus.CtxOperatorKey2, claims.AccountName) // 操作人姓名
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

### 注册插件

```go
db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
    Fields: []gormplus.FieldConfig{
        // 操作人 ID（int64）
        {Name: "CreatedBy",   Getter: gormplus.CtxGetter[int64](gormplus.CtxOperatorKey1),  OnCreate: true},
        {Name: "UpdatedBy",   Getter: gormplus.CtxGetter[int64](gormplus.CtxOperatorKey1),  OnCreate: true, OnUpdate: true},
        // 操作人姓名（string）
        {Name: "CreatedName", Getter: gormplus.CtxGetter[string](gormplus.CtxOperatorKey2), OnCreate: true},
        {Name: "UpdatedName", Getter: gormplus.CtxGetter[string](gormplus.CtxOperatorKey2), OnCreate: true, OnUpdate: true},
    },
}))
```

### 业务代码零改动

```go
db.WithContext(ctx).Create(&account)        // CreatedBy / UpdatedBy 自动填充
db.WithContext(ctx).Model(&account).Updates(data) // UpdatedBy 自动填充
```

---

## 七、多数据源管理（主从分离）

### 注册数据源

```go
gormplus.DS.Register("default", gormplus.DataSourceGroupConfig{
    Master: gormplus.DataSourceNodeConfig{
        DSN:  "root:pwd@tcp(master:3306)/mydb?charset=utf8mb4&parseTime=True",
        Pool: gormplus.DataSourcePoolConfig{MaxOpen: 50, MaxIdle: 10, MaxLifetime: 30 * time.Minute},
    },
    Slaves: []gormplus.DataSourceNodeConfig{
        {DSN: "root:pwd@tcp(slave1:3306)/mydb?charset=utf8mb4&parseTime=True"},
        {DSN: "root:pwd@tcp(slave2:3306)/mydb?charset=utf8mb4&parseTime=True"},
    },
})
```

### 中间件自动标记读写

```go
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
```

### Repository 层自动获取 DB

```go
func (r *AccountRepo) List(ctx context.Context) ([]*model.Account, error) {
    db, err := gormplus.DS.Auto(ctx) // 自动：数据源=default，读=从库
    if err != nil {
        return nil, err
    }
    var list []*model.Account
    return list, db.Find(&list).Error
}

func (r *AccountRepo) Create(ctx context.Context, a *model.Account) error {
    db, err := gormplus.DS.Auto(ctx) // 自动：数据源=default，写=主库
    if err != nil {
        return err
    }
    return db.Create(a).Error
}
```

---

## 八、SingleFlight + 可插拔缓存（SF）

防止缓存击穿，合并并发请求。缓存实现可插拔：默认内存缓存，也可注入 Redis 等自定义实现。

### 方式一：内存缓存（默认，零配置）

无需任何配置，SF 内部自动初始化内存缓存，适合单机部署和开发测试环境。

```go
// main.go
func main() {
    // 无需注册任何缓存，直接使用
    defer gormplus.StopSFCache() // 退出时停止后台清理 goroutine

    // 启动服务 ...
}

// 业务代码
// 带缓存（30 秒）
list, err := gormplus.SF(func() ([]*model.Account, error) {
    var result []*model.Account
    err := gormplus.Query[*model.Account](db, ctx).
        WhereIf(status != 0, "status = ?", status).
        Build().Find(&result)
    return result, err
}, "Account.List", map[string]any{"status": status, "page": pageNum}, 30*time.Second)

// 纯 singleflight（不缓存，只合并并发请求）
account, err := gormplus.SFNoCache(func() (*model.Account, error) {
    var a model.Account
    err := db.WithContext(ctx).Where("id = ?", id).First(&a).Error
    return &a, err
}, "Account.Detail", map[string]any{"id": id})

// 写操作后主动失效缓存
gormplus.SFInvalidate("Account.List", map[string]any{"status": status})
```

### 方式二：Redis 缓存（多实例部署，实现接口后注册）

实现 `sf.SFCache` 接口，启动时注册一次，业务代码与内存缓存完全相同无需改动。
适合多实例部署、需要跨进程共享缓存的生产环境。

**第一步：实现 SFCache 接口**

```go
// redis_cache.go
type RedisSFCache struct {
    rdb    *redis.Client
    prefix string // key 前缀，避免与其他业务 key 冲突
}

func NewRedisSFCache(rdb *redis.Client, prefix string) *RedisSFCache {
    if prefix == "" {
        prefix = "sf:"
    }
    return &RedisSFCache{rdb: rdb, prefix: prefix}
}

// Get 从 Redis 读取缓存，key 不存在或已过期返回 (nil, false)
func (c *RedisSFCache) Get(key string) (any, bool) {
    val, err := c.rdb.Get(context.Background(), c.prefix+key).Bytes()
    if err != nil {
        return nil, false // redis.Nil 或其他错误均降级为未命中
    }
    var result any
    if err := json.Unmarshal(val, &result); err != nil {
        return nil, false
    }
    return result, true
}

// Set 写入 Redis，ttl 后自动过期
func (c *RedisSFCache) Set(key string, val any, ttl time.Duration) {
    b, err := json.Marshal(val)
    if err != nil {
        return // 序列化失败静默跳过，不影响主流程
    }
    c.rdb.Set(context.Background(), c.prefix+key, b, ttl)
}

// Del 主动删除指定 key（供 SFInvalidate 使用）
func (c *RedisSFCache) Del(key string) {
    c.rdb.Del(context.Background(), c.prefix+key)
}
```

**第二步：启动时注册（一次）**

```go
// main.go
func main() {
    // 初始化 Redis
    rdb := redis.NewClient(&redis.Options{
        Addr:     "localhost:6379",
        Password: "",
        DB:       0,
    })

    // 注册 Redis 缓存，替换默认内存缓存
    sf.RegisterCache(NewRedisSFCache(rdb, "myapp:sf:"))

    // Redis 连接由用户管理，无需调用 StopSFCache
    defer rdb.Close()

    // 启动服务 ...
}
```

**第三步：业务代码与内存缓存完全相同，无需任何改动**

```go
// 用法与内存缓存完全一致
list, err := gormplus.SF(func() ([]*model.Account, error) {
    var result []*model.Account
    err := gormplus.Query[*model.Account](db, ctx).
        WhereIf(status != 0, "status = ?", status).
        Build().Find(&result)
    return result, err
}, "Account.List", map[string]any{"status": status, "page": pageNum}, 30*time.Second)

// 失效缓存（会删除 Redis 中对应的 key）
gormplus.SFInvalidate("Account.List", map[string]any{"status": status})
```

### 两种方式对比

| | 内存缓存（默认） | Redis 缓存 |
|---|---|---|
| 配置 | 无需任何配置 | 启动时 `RegisterCache` 一次 |
| 适用场景 | 单机、开发测试环境 | 多实例部署、需要缓存共享 |
| 退出清理 | `defer gormplus.StopSFCache()` | 用户自行管理 Redis 连接 |
| 业务代码 | 完全一样，无需修改 | 完全一样，无需修改 |

### 缓存策略建议

| 场景 | 推荐 TTL | 说明 |
|------|---------|------|
| 列表/统计查询 | 3s ~ 30s | 实时性要求不高 |
| 配置/字典数据 | 1min ~ 5min | 几乎不变 |
| 详情/用户数据 | 0（SFNoCache）| 实时性要求高，不缓存 |

---

## 九、慢查询监控

```go
gormplus.RegisterSlowQuery(db, gormplus.SlowQueryConfig{
    Threshold: 200 * time.Millisecond, // 超过此阈值记录慢查询
    Logger: func(ctx context.Context, info gormplus.SlowQueryInfo) {
        log.Printf("[慢查询] cost=%v table=%s rows=%d sql=%s",
            info.Duration, info.Table, info.RowsAffected, info.SQL)
    },
})
```

---

## 十、代码生成器

根据数据库表结构自动生成 Model、Repository、API 文件。

```yaml
# generator.yaml
host: localhost
port: 3306
username: root
password: your_password
database: your_database
out_path: ./dal/model
model_pkg_path: ./dal/model/entity
repo_path: ./dal/repository
api_path: ./api
package: your_package
```

```go
cfg, err := gormplus.LoadGeneratorConfig("./generator.yaml")
if err != nil {
    log.Fatal(err)
}
if err := gormplus.Generate(cfg); err != nil {
    log.Fatal(err)
}
```

---

## 依赖

- `gorm.io/gorm`
- `gorm.io/gen`
- `gorm.io/driver/mysql`
- `github.com/shopspring/decimal`
