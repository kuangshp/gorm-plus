# GORM-Plus

gorm-plus 是对 GORM 的增强扩展库，提供更便捷的查询构建器和代码生成器。

## 功能特性

### 1. 增强查询构建器 (query)

- **链式调用**: 支持 `Eq`, `EqIfNotZero`, `In`, `InIfNotEmpty`, `Like`, `Gt`, `Gte`, `Lt`, `Lte`, `OrderByDesc`, `OrderByAsc`, `Page` 等常用查询条件
- **单飞模式 (singleflight)**: 内置 singleflight 请求合并 + 本地缓存，有效防止缓存击穿
- **双引擎支持**: 同时支持原生 GORM 和 gorm-gen 生成的代码

### 2. 代码生成器 (generator)

- **数据库表同步**: 直接从数据库表结构生成 Model、Repository、API 文件
- **多文件生成**: 自动生成基础 repository (`xxx_base.go`)、接口定义 (`xxx_interface.go`)、扩展实现 (`xxx.go`)
- **API 生成**: 自动生成 go-zero 风格的 API 定义文件
- **智能推断**: 自动识别字段类型、验证规则、枚举值等

## 安装

```bash
go get github.com/kuangshp/gorm-plus
```

## 快速开始

### 查询构建器

#### 原生 GORM 用法

```go
import "gorm-plus/query"

var result []*model.Order
err := query.NewQuery(db.WithContext(ctx).Model(&model.Order{})).
    EqIfNotZero("user_id", userId).
    EqIfNotZero("status", status).
    OrderByDesc("created_at").
    Page(pageNum, pageSize).
    Find(&result)
```

#### gorm-gen 用法

```go
import "gorm-plus/query"

var result []InterviewVo
err := query.GenWrap(dao.InterviewEntity.WithContext(ctx)).
    EqIfNotZero(dao.InterviewEntity.Status.Eq(int64(status)), status).
    InIfNotEmpty(dao.InterviewEntity.ID.In(ids...), ids).
    Apply().Scan(&result)
```

### Singleflight 缓存

```go
import "gorm-plus/query"

// 使用默认缓存（5分钟）
list, err := query.SF(fn, "Order.List", args)

// 不缓存，只合并并发请求
list, err := query.SFNoCache(fn, "Order.Detail", args)

// 指定缓存时长
list, err := query.SF(fn, "Order.List", args, 10*time.Second)
```

### 代码生成器

```go
import "gorm-plus/generator"

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

## 项目结构

```
gorm-plus/
├── query/               # 查询构建器
│   ├── query_builder.go # 链式查询构建器
│   ├── sf.go           # singleflight 缓存封装
│   ├── gen_wrapper.go  # gorm-gen 包装器
│   └── utils.go        # 工具函数
└── generator/           # 代码生成器
    ├── config.go       # 配置定义
    ├── generator.go    # 生成器主逻辑
    └── template/        # 模板文件
```

## 查询条件说明

| 方法 | 说明 | 示例 |
|------|------|------|
| `Eq` | 等于条件 | `Eq("status", 1)` |
| `EqIfNotZero` | 值不为零时才添加条件 | `EqIfNotZero("user_id", userId)` |
| `In` | IN 查询 | `In("id", ids...)` |
| `InIfNotEmpty` | 切片非空时才添加 IN 条件 | `InIfNotEmpty("id", ids)` |
| `Like` | 模糊匹配 | `Like("name", "%keyword%")` |
| `Gt` / `Gte` | 大于 / 大于等于 | `Gt("create_time", timestamp)` |
| `Lt` / `Lte` | 小于 / 小于等于 | `Lte("status", 0)` |
| `OrderByDesc` | 降序排序 | `OrderByDesc("created_at")` |
| `OrderByAsc` | 升序排序 | `OrderByAsc("id")` |
| `Page` | 分页查询 | `Page(pageNum, pageSize)` |

## 缓存策略建议

| 场景 | 推荐 TTL | 说明 |
|------|----------|------|
| 列表/统计查询 | 3s ~ 30s | 实时性要求不高，可较长缓存 |
| 配置/字典数据 | 1min ~ 5min | 几乎不变的数据 |
| 详情/用户数据 | 0 (SFNoCache) | 实时性要求高，不缓存 |

## 依赖

- gorm.io/gorm
- gorm.io/gen
- gorm.io/driver/mysql
- shopspring/decimal
