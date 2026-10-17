# gorm-plus

基于 [gorm](https://gorm.io) 和 [gorm-gen](https://github.com/go-gorm/gen) 的增强扩展包。开箱即用的代码生成器、SingleFlight + 可插拔缓存、多租户/数据权限插件、多数据源管理、SQL 文件化查询。

**核心能力:** 代码生成器 · gorm-gen 类型安全扩展 · 原生 gorm 链式 · SingleFlight 可插拔缓存 · DAL SQL 文件化 · 多数据源(任意驱动) · 多租户自动注入 · 数据权限自动注入 · 自动填充 · 敏感字段加密 · 慢查询监控 · 事务工具

## 为什么已经有 gorm-gen，还需要本库？

gorm-gen 解决的是"类型安全的 ORM 代码生成"——但实际业务里还有一大堆**重复样板代码**它不管:可选条件 if 包裹、模糊查询拼 `%`、单飞 + 缓存防击穿、多租户字段自动注入、写后失效缓存、分页查询 + 总数、列表查询的 cache key 漂移等等。

下表把 gorm-gen 原生写法和 gorm-plus 提供的能力做了对照,**所有差异都是高频痛点,不是锦上添花**:

| 场景 | gorm-gen 原生 | gorm-plus | 价值 |
|---|---|---|---|
| **可选条件 if 包裹** | `if status != 0 { q = q.Where(dao.X.Status.Eq(status)) }` 每个可选字段都要 if | `.WhereIf(status != 0, dao.X.Status.Eq(status))` 一行 | 业务代码少 50% if |
| **模糊查询自动拼 %** | `q.Where(dao.X.Name.Like("%"+kw+"%"))` 手动拼,空值还要 if | `.LLike(dao.X.Name, kw)` 空值自动跳过 | 防止空关键字查全表 |
| **前端 0/1/2 排序方向** | `if req.Sort == 1 { q.Order(...Asc()) } else if req.Sort == 2 { q.Order(...Desc()) }` | `.OrderTriState(req.Sort, asc, desc)` 一行 | 一字段一行,默认 0 跳过 |
| **缓存击穿防护** | 自己实现 singleflight + redis 双 check | `gormplus.SF(fn, name, args, ttl)` 一行 | 一次 SQL 100 并发→只打 1 次 DB |
| **写后失效缓存** | 手写 redis.Del 每个相关 key | generator 模板自动生成 `invalidateWriteCaches` | 11 个相关前缀一次清,零样板 |
| **列表 cache 漂移** | 不同 Where 条件命中同一 key,串数据 | `.WithCacheArgsFromStruct(req)` DTO 一行灌入 | 自动按 query 参数隔离缓存 |
| **多租户字段** | 每个查询/Insert 手动加 `tenant_id = ?` | 注册插件并提供租户 context 后注入条件及创建字段 | 一次注册,全局生效 |
| **数据权限** | 每个查询拼 `dept_id IN (...)` | 从请求 context 取得业务注入函数，追加行级权限条件 | 同上 |
| **自动填充字段** | 每个 BeforeCreate hook 手写 `created_at = now()` 等 | 配置 Getter 后自动填充 created_by/updated_by 等操作人字段 | 同上 |
| **复杂 SQL 写代码里** | `db.Raw("SELECT ...").Find(...)` 字符串拼接难维护 | DAL: SQL 写 .sql 文件,go 里 `gormplus.DALQuery[T](ctx, "x.sql", args)` | 复杂 SQL/视图/JOIN 走文件,git diff 清晰 |
| **多数据源/读写分离** | 自己包装多个 *gorm.DB | `gormplus.DS.Auto(ctx)` 按 ctx 标记取得句柄 | 中间件标记,数据访问层选择并绑定句柄 |
| **慢查询监控** | 自己写 gorm plugin 或日志解析 | 注册插件 + 设阈值/钩子,自动上报 | 自定义 Logger 对接日志或监控 |
| **写代码 + 改字段→重写 Repo** | gorm-gen 只生成基础 DAO,Repo 全手写 | generator 一次生成完整 Repository(CRUD + 分页 + 缓存失效 + WithTx) | 改表→重跑生成器即可 |
| **事务跨 Repo 调用** | `db.Transaction(func(tx *gorm.DB) error { dao.Use(tx).X... })` 每次手动 Use | `gormplus.TransactionAs(db, dao.Use, func(tx *dao.Query))` 类型安全 | Repository.WithTx(tx) 统一增删改查 |
| **EXPLAIN/Debug** | 改全局 logger 配置 / 手动写 db.Debug() | `.PrintSQL() / .ToSQL() / .Explain()` 链式一行 | 排查 SQL 不动配置 |

gorm-gen 生成类型安全的 DAO；gorm-plus 在其上补充查询构建、仓储、事务及业务插件的接入能力。

## 目录导航

按使用频率分组,点击跳转到对应章节。


**🚀 入门**

- [安装](#安装)
- [为什么需要本库](#为什么已经有-gorm-gen还需要本库)
- [目录结构](#目录结构)
- [快速开始](#快速开始)
- [一、代码生成器](#一代码生成器)
  - [goctl 内嵌模板与 Makefile](#goctl-内嵌模板)
  - [Swagger 文档配置与自动生成](#swagger-文档配置与自动生成)
  - [Proto 生成与参数校验](#proto-生成与参数校验)
  - [结构体校验与 Gin 接入](#结构体校验与-gin-接入)
  - [业务自定义 CEL 校验](#业务自定义-cel-校验)
  - [gRPC 全局参数校验](#grpc-全局参数校验)
  - [RPC Context 透传](#rpc-context-透传)

**🔍 查询能力**

- [二、gorm-gen 类型安全链式(GenWrap)](#二gorm-gen-类型安全链式genwrap)
- [三、原生 gorm 链式(Query)](#三原生-gorm-链式query)
- [四、SingleFlight + 可插拔缓存(SF)](#四singleflight--可插拔缓存sf)
  - [缓存使用红线（必读）](#缓存使用红线必读)
- [五、DAL SQL 文件化查询](#五dal-sql-文件化查询)
- [六、事务工具](#六事务工具)

**🔌 插件 & 工具**

- [七、多租户插件](#七多租户插件)
- [八、数据权限插件](#八数据权限插件)
- [九、自动填充插件](#九自动填充插件)
- [十、多数据源管理](#十多数据源管理)
- [十一、Context 解析器](#十一context-解析器)
- [十二、慢查询监控](#十二慢查询监控)
- [十三、敏感字段加密](#十三敏感字段加密)
- [依赖](#依赖)

---

## 安装


```bash
go get github.com/kuangshp/gorm-plus
```

---

## 目录结构

```
gorm-plus/
├── gormplus.go                  # 包文档 + ctx 解析器(顶层入口)
├── gormplus_query.go            # 原生 gorm 链式查询转发(IQueryBuilder/Query)
├── gormplus_genwrap.go          # gorm-gen 类型安全链式转发(IGenWrapper/Wrap)
├── gormplus_executor.go         # 查询执行器(ExecuteQuery/ExecutePage/BuildArgs)
├── gormplus_page.go             # 分页工具(FindByPage/ScanByPage)
├── gormplus_sf.go               # SingleFlight + 缓存转发(SF/SFNoCache/RawValue 等)
├── gormplus_transaction.go      # 事务工具(Transaction/TransactionAs 泛型版)
├── gormplus_dal.go              # DAL SQL 文件化转发(DALQuery/DALExec 等)
├── gormplus_datasource.go       # 多数据源管理转发
├── gormplus_tenant.go           # 多租户插件转发
├── gormplus_permission.go       # 数据权限插件转发
├── gormplus_autofill.go         # 自动填充插件转发
├── gormplus_slowquery.go        # 慢查询监控转发
├── gormplus_generator.go        # 代码生成器入口转发
├── gormplus_interceptor.go      # gRPC 校验及 Context 透传拦截器转发
├── version.go
│
├── query/                       # 链式查询构造器
│   ├── query_builder.go         # IQueryBuilder:原生 gorm 链式条件
│   ├── gen_wrapper.go           # IGenWrapper:gorm-gen 类型安全链式
│   ├── query_option.go          # QueryOption/WithCache/WithCacheArgs 等
│   ├── query_executor.go        # ExecuteQuery/ExecutePage(sf+cache 装饰器)
│   ├── slow_query.go            # 慢查询监控 gorm 插件
│   └── utils.go
│
├── sf/                          # SingleFlight + 可插拔缓存
│   ├── sf.go                    # 核心:SFCache 接口、RawValue 协议、内存缓存
│   └── sf_test.go               # 34 个测试用例(含 Redis 路径回归)
│
├── dal/                         # SQL 文件化查询(embed + 泛型)
│   ├── dal.go                   # 包文档
│   ├── instance.go              # DAL/NewDal/全局 defaultDAL
│   ├── provider.go              # DBProvider
│   ├── loader.go                # SQLLoader/EmbedLoader
│   ├── options.go               # Option/WithDebug 等
│   ├── hook.go                  # Hook 接口(慢 SQL 监控)
│   ├── query.go                 # Query/QueryOne/Count/Page
│   ├── tx.go                    # WithTx/TxQuery/TxExec
│   ├── must.go                  # MustExec/MustQueryOne
│   ├── debug.go                 # debug 日志
│   └── dal_test.go              # 47 个测试用例
│
├── plugin/                      # GORM 插件集合
│   ├── ctx.go                   # ctx 解析器(屏蔽 gin/go-zero/fiber 差异)
│   ├── tenant.go                # 多租户插件(多字段、多表、JOIN 别名识别)
│   ├── dataPermission.go        # 数据权限插件
│   └── autoOperator.go          # 自动填充插件(创建人、更新时间等)
│
├── datasource/                  # 多数据源管理
│   └── manager.go               # 任意 gorm 驱动 / 主从 / 读写分离
│
├── interceptor/                 # gRPC 服务端/客户端拦截器
│   ├── validation.go            # Protovalidate 全局参数校验
│   └── context_propagation.go   # 租户与操作人 Context 透传
│
├── httpswagger/                 # 独立 Swagger 配置、内嵌 UI 与运行时文档适配
│
└── generator/                   # 代码生成器
    ├── generator.go             # 主逻辑
    ├── dialector.go             # 多驱动支持(MySQL/Postgres/SQLite/SQLServer)
    ├── config.go                # YAML 配置
    ├── generator.example.yaml
    ├── gozero/                  # 内嵌 goctl 模板与标准库执行器
    ├── swagger/                 # 按业务 YAML 开关调用 goctl 生成内嵌文档
    └── template/                # 代码模板
        ├── repository_gen_template.txt   # 自动生成(含缓存失效)
        ├── repository_template.txt       # 用户可改(自定义查询示例)
        ├── api_template.txt  # go-zero开发中使用 
        ├── proto_template.txt # go-zero RPC proto 文件
        ├── base_proto_template.txt # Proto 统一基础响应
        ├── base_api_template.txt  # go-zero开发中使用
        ├── dto_template.txt
        ├── vo_template.txt
        ├── mapper_template.txt # 未使用 Proto 时生成 DTO/VO ↔ Entity 映射
        ├── entity_proto_mapper_template.txt # Entity ↔ Proto 映射
        └── api_proto_mapper_template.txt # API types ↔ Proto 映射
```

Mapper 使用两个独立输出路径：`proto_mapper_path` 输出 Entity ↔ Proto 映射，`api_mapper_path` 输出 API types ↔ Proto 映射；未配置 `proto_path` 时，`api_mapper_path` 输出 DTO/VO ↔ Entity 映射。

旧版 Go 配置 `MapperPath` 和 YAML 配置 `mapper_path` 仍可使用：未配置 `proto_path` 时直接输出到该目录；配置 `proto_path` 时，Entity ↔ Proto 输出到其 `entityproto` 子目录，同时配置 `api_path` 时 API types ↔ Proto 输出到其 `apiproto` 子目录。显式配置的 `APIMapperPath` / `api_mapper_path`、`ProtoMapperPath` / `proto_mapper_path` 分别优先，不会被旧配置覆盖。

> **顶层 `gormplus_*.go` 文件**只做类型别名 + 函数转发,把分包的 API 聚合到 `gormplus` 命名空间。业务方一律用 `gormplus.XXX` 调用,不需要直接 import 子包(`sf`/`query`/`plugin`/`dal` 等)。

---

## 快速开始

按参考项目的分层，把数据库插件注册放到 `initialize`，把认证结果转换为插件 context 的逻辑放到 `internal/middleware`，最后挂载到受保护的路由组。

下面的启动装配函数使用已经创建好的 `*gorm.DB`、项目已有的认证中间件和业务路由注册函数；将导入路径 `your-app` 替换为自己的 module。`RegisterAccountPlugins` 的实现见 [Context 解析器](#十一context-解析器)，两个业务中间件的完整代码见 [数据权限插件](#八数据权限插件) 和 [自动填充插件](#九自动填充插件)。

```go
package initialize

import (
    "your-app/internal/middleware"
    "github.com/gin-gonic/gin"
    "gorm.io/gorm"
)

// 启动时调用一次；数据库连接和路由 Handler 由业务项目创建。
func SetupAccountAPI(router *gin.Engine, db *gorm.DB,
    authMiddleware gin.HandlerFunc, registerRoutes func(*gin.RouterGroup),
) error {
    // ① 注册插件、Gin Context 适配和默认连接，检查初始化错误。
    if err := RegisterAccountPlugins(db); err != nil {
        return err
    }
    // ② 认证先完成，再写入操作人和数据权限 context。
    accounts := router.Group("/account", authMiddleware,
        middleware.OperatorMiddleware(),
        middleware.DataPermissionMiddleware(db),
    )
    // ③ 挂载已有 Handler，用不同账号请求后核对 SQL、数据范围和操作人字段。
    registerRoutes(accounts)
    return nil
}
```

如使用 Casbin，将项目的接口权限中间件接在数据权限中间件之后。多租户、敏感字段和慢查询按需接入，分别见对应章节；不要把本例当成所有插件都已默认启用。

---

## 一、代码生成器

生成流程按 `db_type` 使用 MySQL、PostgreSQL、SQLite、SQL Server 驱动读取表列表和字段元数据，包含主键（含联合主键）、自增、可空、长度、精度和注释。SQLite 自动跳过 `sqlite_` 内部表，表注释缺失时使用模型名。元数据、模板渲染和文件写入失败会返回错误，请检查生成入口的返回值。

常见类型别名（如 PostgreSQL 的 `int8`、`numeric`、`bool`、`jsonb`，SQL Server 的 `datetime2`、`bit`）会同步映射到 Model 和 DTO/VO/Mapper。数据库特有的数组、空间类型、自定义类型仍需按业务调整模板；API/DTO 的修改请求仍沿用默认 `id` 约定，非标准主键的请求契约需自行调整。

数据库集成回归已在 SQLite 3.45.1 和 PostgreSQL 18.1 实际执行，覆盖全量/单表增量生成、生成代码编译、CRUD、分页、关联查询、联合主键、软删除/物理删除、事务提交/回滚、嵌套保存点、事务缓存隔离和预编译语句。PostgreSQL 另验证 `FOR UPDATE` 会阻塞另一事务的冲突更新。MySQL、SQL Server 的真实数据库集成仍需在各自环境验证。已有 DTO/VO/Mapper 按保留策略不会覆盖，升级后请对照新模板同步受影响字段。

PostgreSQL 连接参数会分别引用和转义，支持空密码以及含空格、单引号、反斜杠的值，避免密码配置影响目标数据库名。PostgreSQL 的 `character varying(n)` / `character(n)` 元数据统一按 `varchar(n)` / `char(n)` 生成字符串长度校验。

SQLite 不支持 `FOR UPDATE` 行锁，也不提供与 PostgreSQL `NUMERIC` 相同的固定十进制精度。实际回归中，`NUMERIC(18,6)` 写入 `999999999999.999999` 后，SQLite 读回 `1000000000000`，PostgreSQL 保留原值；Go 的 `decimal.Decimal` 无法恢复数据库已经丢失的精度。SQLite 精确金额可按业务约定使用整数最小单位存储。详见 [SQLite 类型与 NUMERIC affinity](https://sqlite.org/datatype3.html#type_affinity)。

#### 重跑数据库兼容测试

```bash
# 默认运行 SQLite 集成测试；没有 PostgreSQL 地址时跳过 PostgreSQL 集成测试。
go test ./generator -run 'Test(SQLite|Postgres)Generation' -v -count=1

# 使用独立 PostgreSQL 测试实例；账号须具备 CREATEDB 权限。
# 测试会创建随机命名的临时数据库，各运行用例使用独立 schema，并在结束时清理。
GORMPLUS_TEST_POSTGRES_DSN='postgres://postgres:password@127.0.0.1:5432/postgres?sslmode=disable' \
  go test ./generator -run 'Test(SQLite|Postgres)Generation' -v -count=1
```

添加 `GOFLAGS=-race` 可同时对生成代码的集成测试启用竞态检测。

### 配置与运行

#### 复用业务配置：根目录 generator.go

将下面的代码复制到业务项目根目录的 `generator.go`，修改 `Package` 为该项目 `go.mod` 中的 module，并按项目结构调整输出目录。示例使用 Viper 读取业务 YAML 的 `dataSource` 节点；项目未引入 Viper 时先安装：

```bash
go get github.com/spf13/viper
```

```go
//go:build ignore

// 在项目根目录运行：go run generator.go（默认读取 application.dev.yml）。
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/kuangshp/gorm-plus"
	"github.com/spf13/viper"
)

func main() {
	env := flag.String("envString", "dev", "配置环境：dev、test、prod")
	configFile := flag.String("config", "", "指定业务 YAML 文件，优先于按环境选择的配置文件")
	flag.Parse()
	if err := generate(*env, *configFile); err != nil {
		log.Fatal(err)
	}
}

func generate(env, configFile string) error {
	if env != "dev" && env != "test" && env != "prod" {
		return fmt.Errorf("配置环境必须是 dev、test 或 prod")
	}
	if configFile == "" {
		configFile = "application." + env + ".yml"
	}
	cfg := viper.New()
	cfg.SetConfigFile(configFile)
	if err := cfg.ReadInConfig(); err != nil {
		return fmt.Errorf("读取数据库配置失败: %w", err)
	}
	source := cfg.Sub("dataSource")
	if source == nil {
		return fmt.Errorf("配置缺少 dataSource")
	}
	if source.GetString("host") == "" || source.GetInt("port") <= 0 ||
		source.GetString("username") == "" || source.GetString("database") == "" {
		return fmt.Errorf("dataSource 的 host、port、username、database 不能为空")
	}
	// 当前库的生成器不支持传递 TLS 参数，避免静默忽略连接要求。
	if tls := source.GetString("tls"); tls != "" && tls != "false" {
		return fmt.Errorf("当前 gorm-plus 生成器不支持 dataSource.tls")
	}
	return gormplus.Generate(&gormplus.GeneratorConfig{
		DBType:        source.GetString("driver"),    // 数据库类型。
		Host:          source.GetString("host"),      // 数据库地址。
		Port:          source.GetInt("port"),         // 数据库端口。
		Username:      source.GetString("username"),  // 数据库账号。
		Password:      source.GetString("password"),  // 数据库密码。
		Database:      source.GetString("database"),  // 业务数据库名。
		DtoPath:       "internal/dal/dto",            // DTO 输出目录。
		VoPath:        "internal/dal/vo",             // VO 输出目录。
		OutPath:       "internal/dal/dao",            // DAO 输出目录。
		ModelPkgPath:  "internal/dal/entity",         // 实体输出目录。
		RepoPath:      "internal/dal/repository",     // Repository 输出目录。
		Package:       "ai-interview-assistant/api",  // 改为业务项目 go.mod 的 module。
		ExcludeTables: []string{"schema_migrations"}, // 排除迁移记录表。
	})
}
```

业务配置示例（其他业务节点可以保留）：

```yaml
# application.dev.yml
dataSource:
  driver: mysql
  host: localhost
  port: 3306
  username: your_username
  password: "your_password"
  database: your_database
  tls: false
```

在业务项目根目录运行：

```bash
# 默认读取 application.dev.yml
go run generator.go

# 读取 application.test.yml
go run generator.go -envString test

# 直接指定业务配置文件（相对路径或绝对路径）
go run generator.go -config ./etc/web-api.yaml
```

`-config` 指定后直接读取该文件，未指定时按 `-envString` 选择 `application.<env>.yml`。如果业务配置使用 `MysqlConfig` 节点和 `driverName` 字段，将示例中的 `cfg.Sub("dataSource")` 改为 `cfg.Sub("MysqlConfig")`，`DBType` 改为读取 `source.GetString("driverName")`，并相应调整错误提示。当前生成器不接收业务 YAML 中的 `charset`、`loc`，MySQL 连接固定使用 `utf8mb4`、`parseTime=True`、`loc=Local`。

#### 独立生成器配置

也可以继续使用独立的 `generator.yaml`，通过 `gormplus.LoadGeneratorConfig` 加载生成器自己的平铺配置。上面的 `-config` 示例读取的是业务 YAML，两者的配置结构不同。

```yaml
# generator.yaml
db_type: mysql
host: localhost
port: 3306
username: root
password: your_password
database: your_database
out_path: ./dal/query
model_pkg_path: ./dal/model
repo_path: ./dal/repository
api_path: ./apps/admin/desc
proto_path: ./apps/rpc/proto
vo_path: ./api/vo
dto_path: ./api/dto
api_mapper_path: ./internal/mapper/api
proto_mapper_path: ./internal/mapper/proto
# 可选；为空时使用 gorm-plus 内嵌的 goctl 模板
# goctl_template_home: ./tools/goctl
package: your_package
exclude_tables:
  - sys_config
  - sys_dict
```

`base.proto` 与各业务 Proto 使用同一个包，Proto 中的 `package` 和 `go_package` 根据生成器的 `package` 配置生成。

```go
cfg, err := gormplus.LoadGeneratorConfig("./generator.yaml")
if err != nil {
	log.Fatal(err)
}

if err := gormplus.Generate(cfg); err != nil {
	log.Fatal(err)
}
// 运行后提示输入表名：
// - 输入表名：只生成该表的 Model / Repository / API / Proto / VO / DTO
// - 直接回车：生成所有未排除表的 Model / Repository / API / Proto / VO / DTO
```

> **注意**：数据模型（Model）每次都会重新生成覆盖；Repository / API / Proto / VO / DTO 文件已存在时自动跳过，不会覆盖已有的自定义代码。

### goctl 内嵌模板

使用项目本地的 `goctl/` 模板目录：

```bash
goctl api go -api *.api --dir . --style=goZero --home ../../goctl
```

使用 gorm-plus 内嵌模板：

```bash
gormplus goctl api go -api *.api --dir . --style=goZero
```

两种命令生成方式一致；第二种会自动使用 gorm-plus 内嵌模板，不需要传递 `--home`。

#### 使用 Makefile 一键生成

在业务服务的 `.api` 文件所在目录创建 `Makefile`。例如 `apps/shop` 中有 `shop.api`，可以直接复制以下内容，无需配置变量：

```makefile
.PHONY: gen install-tools validate swagger run run-open build

# 使用 gorm-plus 内嵌模板生成代码：先安装工具，再校验，最后生成。
gen: install-tools validate
	gormplus goctl api go --api shop.api --dir . --style=goZero

# 安装或更新工具。
install-tools:
	go install github.com/zeromicro/go-zero/tools/goctl@latest
	go install github.com/kuangshp/gorm-plus/cmd/gormplus@latest

# 先确保工具已安装，再校验 API 定义。
validate: install-tools
	goctl api validate --api shop.api

# 按同一份服务配置生成 Swagger；关闭时准备编译占位文件。
# 首次使用先执行 make install-tools，日常启动和构建沿用已安装的工具。
swagger:
	gormplus swagger --config etc/shop-api.yaml

# 先准备文档，再启动；不重新生成 Handler 或覆盖业务代码。
run: swagger
	go run . -f etc/shop-api.yaml

# 本地开发可选：文档接口就绪后自动打开浏览器，最长等待 30 秒。
run-open: swagger
	@gormplus swagger-open --config etc/shop-api.yaml &
	go run . -f etc/shop-api.yaml

# 先准备文档，再构建包含文档的二进制。
build: swagger
	mkdir -p bin
	go build -o bin/shop .
```

进入该目录运行：

```bash
cd apps/shop
make gen

# 也可以单独安装工具，或安装工具后只校验 API。
make install-tools
make validate

# 按 SwaggerConfig 开关准备文档，再启动或构建。
make run
make build
# 本地启动并自动打开文档页面。
make run-open
```

将 `shop.api` 替换为自己的 API 文件名；Makefile 中命令行前必须使用 **Tab** 缩进。`gen → validate → install-tools` 的依赖关系保证按安装、校验、生成的顺序执行，即使使用 `make -j gen` 也不会抢先校验；校验失败时不会执行生成，无需在 `gen` 中重复写校验命令。

每次 `make gen` 或 `make validate` 都会先安装或更新工具。请确保 Go 命令安装目录在 `PATH` 中：优先使用 `go env GOBIN`，为空时使用 `go env GOPATH` 对应的 `bin` 目录。安装命令不会升级业务项目的依赖，业务项目的 `go.mod` 也需要引入包含对应模板能力的 gorm-plus 版本；生成后运行 `go mod tidy` 整理依赖。

### Swagger 文档配置与自动生成

内嵌 go-zero 模板提供默认关闭的 `SwaggerConfig`。开启后，通过上面的 `make run` / `make build` 自动生成文档，并将 Swagger JSON 和固定版本的 Swagger UI 页面、JS、CSS 一起内嵌进二进制。页面与 API 使用同一端口，不依赖 CDN 或上传文件的 `StaticConfig`。

在业务配置 `etc/shop-api.yaml` 中设置：

```yaml
# Swagger 文档，默认关闭。
SwaggerConfig:
  Enabled: true # make run/build 会先生成最新文档；运行时注册文档路由。
  Path: /swagger # 页面地址 /swagger/；JSON 地址 /swagger/swagger.json。
  ApiFile: shop.api # 相对于执行命令的服务目录；自动处理入口中的 import。
  # 唯一的 .api 入口可填 ""；多个入口时必须指定，不能随意挑选或使用 *.api。
```

进入服务目录执行：

```bash
cd apps/shop
make install-tools # 首次安装或需要升级工具时执行。
make run           # 生成文档后启动，读取 etc/shop-api.yaml。
make build         # 生成文档后构建 bin/shop。
make run-open      # 本地启动，并在文档接口就绪后打开浏览器。
```

服务启动后访问 `http://127.0.0.1:服务端口/swagger/`。也可以单独执行 `gormplus swagger --config etc/shop-api.yaml` 更新文档；从仓库根目录操作时可用 `gormplus swagger --dir apps/shop --config etc/shop-api.yaml`。`--config` 和 `ApiFile` 的相对路径均以 `--dir` 指定的服务目录为基准，默认当前目录。

开启 Swagger 后，启动入口会打印完整页面和 JSON 地址。例如监听 `0.0.0.0:8888` 时：

```text
Swagger UI: http://127.0.0.1:8888/swagger/
Swagger JSON: http://127.0.0.1:8888/swagger/swagger.json
Starting server at 0.0.0.0:8888...
```

地址使用实际端口和 `SwaggerConfig.Path`；监听全部网卡时输出本机访问地址，配置服务端 TLS 证书时使用 `https`。关闭 Swagger 时不打印文档地址。已有 main 不会被 goctl 自动覆盖，需同步新版入口中的地址打印代码。

`run-open` 通过 `gormplus swagger-open --config ...` 检查文档 JSON 就绪后再打开浏览器，不依赖固定等待时间；默认等待 30 秒，可用 `--timeout 60s` 调整。该命令只用于本地开发，Swagger 关闭时直接跳过。

生成命令读取同一份 YAML 的开关：关闭时写入占位，开启时调用 `goctl api swagger` 生成 `swagger/swagger.json`；API 校验或生成失败会返回非零状态，阻止后续构建、启动，并使旧文档失效。开启后尚未生成有效文档的程序会明确报错，不能仅修改 YAML 就取得新接口文档。该命令需要支持 `api swagger --filename` 的 goctl，已验证版本为 1.10.2；不需要额外安装 `goctl-swagger` 或 `swag`。

文档 JSON 按运行时的 `ResponseConfig` 自动适配为 `code/message/result`，无返回值的成功接口展示 `code/message`；关闭统一响应或匹配 `BypassPaths` 的路径保留原始结构。文档自身始终原样输出。部署只需带二进制和服务配置，无需再复制文档目录或安装 goctl。

完整说明见 [Swagger 配置、部署与 Air 热重载](docs/swagger.md)，包含 API 信息、自定义路径、生产配置、已有项目接入和独立运行时用法。

### go-zero 文件日志管理

新服务的 YAML 默认包含以下配置；`Log` 控制日志级别、格式及 go-zero 统计日志，`LogRotation` 控制文件存储和归档：

```yaml
Log:
  ServiceName: my-service
  Mode: console
  Path: ./logs/my-service # LogRotation 关闭且 Mode 为 file/volume 时生效
  Encoding: json # json 每行一个 JSON 对象；plain 可读文本
  Level: info # debug、info、error、severe
  Stat: true
  MaxContentLength: 0 # logx 日志内容最大字节数；0 不限制

LogRotation:
  Enabled: true
  Path: ./logs/my-service
  Rotation: daily # daily 按天，hourly 按小时
  CompressAfterDays: 3 # 0 禁用压缩
  KeepDays: 30 # 0 永久保留
  CheckIntervalMinutes: 60
```

生成入口将 `logx`、`slog` 和标准库 `log` 接入同一个文件 Writer。启用 `LogRotation` 后，`Log.Mode` 的输出目标由该 Writer 接管；`Log.Encoding` 和 `Log.Level` 同时应用于 `logx` 与 `slog`，标准库 `log` 通过默认 `slog` Handler 按 INFO 级别记录，遵循相同格式和级别过滤。`json` 为每行一个 JSON 对象，便于日志平台采集；`plain` 为可读文本。两种格式的文件扩展名均为 `.log`，压缩后为 `.log.gz`；各日志库保留自己的字段名称。关闭 `LogRotation` 时沿用 go-zero 原生 `Log` 配置。`logrotate` 包只依赖标准库，不引入 go-zero 依赖。

启动入口在读取配置后统一调用 `logx.MustSetup`，复制完整的 `c.Log`，保留 `Stat`、`MaxContentLength` 及其他原生字段。`Log.ServiceName` 未配置时使用顶层 `Name`；启用 `LogRotation` 时使用 `LogRotation.Path` 存储文件，关闭后原生文件日志使用 `Log.Path`。

目录自动创建，相对路径以进程工作目录为基准，每个进程应配置独立目录。使用进程本地时区，按天生成 `app-2026-10-15.log`，按小时生成 `app-2026-10-15-09.log`。同一时段重启后继续追加，每次写入及定期检查会切换到当前时段。

压缩和删除期限从日志所属时段结束后计算，一天按 24 小时计；例如 `CompressAfterDays: 3` 表示日/小时日志所属时段结束超过 72 小时后压缩为 `.log.gz`。启动时及每隔 `CheckIntervalMinutes` 分钟在后台检查归档，即使没有新日志也会执行。超过 `KeepDays` 的原日志及压缩文件自动删除，删除优先于压缩，压缩不会重新计算保留期限。压缩完整写入成功后才删除源文件。

仅管理当前目录中本包生成的 `app-日期.log`、`app-日期-小时.log` 及对应 `.gz` 文件；切换按天/按小时分割后，已有文件仍按期限归档。配置天数允许 0–36500，检查间隔允许 1–1440 分钟。已有项目使用这项功能时，需要同步 YAML、Config 中的 `LogRotation logrotate.Config` 及生成入口中的日志初始化代码。

### 通用 HTTP 响应、CORS 与静态文件

内嵌 goctl 模板默认接入 `httpresponse` 统一响应和 `httpcors` 跨域配置，并提供默认关闭的 `httpstatic` 本机静态文件访问。三个包都只依赖 Go 标准库，供业务项目独立引用；生成的 go-zero 入口负责注册对应功能。

新服务的 YAML 会包含以下配置，项目可以自行调整或将对应的 `Enabled` 设为 `false`：

```yaml
ResponseConfig:
  Enabled: true
  # 跳过统一 code/message/result 包装的路径，例如健康检查、下载接口。
  # 成功数据原样返回，无返回值接口保持空响应；失败返回原始错误文本，默认 HTTP 400。
  # 精确匹配实际 URL 路径（包含路由前缀，不含查询参数），不支持前缀或通配符匹配。
  # 例如 ["/health", "/api/v1/files/download"]；[] 表示不跳过任何路径，仅 Enabled=true 时生效。
  # 此配置只控制响应格式，认证、参数校验和 CORS 仍按原配置执行。
  BypassPaths: []

CorsConfig:
  Enabled: true
  Origins:
    - "*" # 按项目需要改为具体来源，例如 https://admin.example.com
  AllowHeaders: [] # 在 go-zero 内置请求头上追加，例如 ["X-Client-ID"]
  # 允许前端 JavaScript 在跨域请求中读取的额外响应头；[] 表示不额外暴露。
  # 例如导出 Excel：后端设置 Content-Disposition: attachment; filename="report.xlsx"。
  # 前端需要读取下载文件名时，配置为 ["Content-Disposition"]，再调用：
  # const response = await fetch(downloadUrl);
  # const disposition = response.headers.get("Content-Disposition");
  # disposition 是完整头部值，需再提取 filename / filename*；此配置不会自动设置文件名。
  ExposeHeaders: []

# 本机公开文件访问，独立于 CORS；按需开启。
StaticConfig:
  Enabled: false
  Prefix: /uploads # URL 路径前缀。
  Directory: ./uploads # 已存在的本机目录，相对路径以进程工作目录为基准。
# ./uploads/a.png 对应 http://服务地址/uploads/a.png。
```

统一成功响应：

```json
{"code":0,"message":"请求成功","result":{"id":1}}
```

默认失败响应（HTTP 200，业务状态通过 `code` 判断）：

```json
{"code":1,"message":"操作失败"}
```

`result` 为 nil 时省略。无返回值接口成功时也会返回 `{"code":0,"message":"请求成功"}`；关闭统一响应或将路径加入 `BypassPaths` 后，无返回值接口保持空响应。已返回 `httpresponse.Response` 或 `*httpresponse.Response` 的结果不会重复包装。下载、流式输出等自定义响应接口应使用自己的 Handler，或将其精确路径加入 `BypassPaths`。

项目可以自定义业务错误码及 HTTP 状态码，包装过的错误也能识别：

```go
import (
    "net/http"

    "github.com/kuangshp/gorm-plus/httpresponse"
)

// 在返回 error 的业务方法中使用，错误码由项目自行约定。
err := httpresponse.NewError(10001, "无操作权限")
err.HTTPStatus = http.StatusForbidden // 可选；未设置时使用 HTTP 200
return nil, err
```

上述错误输出 HTTP 403，响应体为 `{"code":10001,"message":"无操作权限"}`。普通 `error` 默认使用 code 1 和 `err.Error()`；日志通过标准库 `slog` 记录服务名和原始错误，项目可配置自己的 slog logger 或替换错误回调。跳过包装的路径返回原始成功数据或原始错误，错误默认使用 HTTP 400，设置了自定义 HTTP 状态码时沿用该状态码。

已有服务可手动接入以下代码（示例中的 `c` 为服务配置）：

```go
// 配置结构增加：
// ResponseConfig httpresponse.Config
// CorsConfig     httpcors.Config
// StaticConfig   httpstatic.Config

var options []rest.RunOption
if c.StaticConfig.Enabled {
    files, err := c.StaticConfig.Open()
    if err != nil {
        log.Fatal(err) // 使用标准库 log；目录不存在等配置错误会在启动时提示。
    }
    defer files.Close()
    options = append(options, rest.WithNotFoundHandler(files))
}
if c.CorsConfig.Enabled {
    options = append(options, rest.WithCustomCors(
        c.CorsConfig.AddHeaders, nil, c.CorsConfig.Origins...,
    ))
}
server := rest.MustNewServer(c.RestConf, options...)
defer server.Stop()

if c.ResponseConfig.Enabled {
    httpx.SetOkHandler(httpresponse.OkHandler)
    httpx.SetErrorHandlerCtx(httpresponse.ErrHandler(c.Name))
    server.Use(httpresponse.BypassMiddleware(c.ResponseConfig.BypassPaths...))
}
```

CORS 的来源匹配和 OPTIONS 预检由 go-zero 处理；`httpcors.Config.AddHeaders` 追加允许/暴露的请求头，保留已有头部并去重。`Origins` 为空时沿用 go-zero 的允许所有来源行为。响应回调是 go-zero 的进程级设置，应在启动时统一注册；业务项目可替换为自己的同签名回调。

#### ExposeHeaders：跨域读取 Excel 下载文件名

后端导出 Excel 时，可通过 `Content-Disposition` 响应头指定下载文件名：

```http
Content-Disposition: attachment; filename="report.xlsx"
```

跨域前端需要通过 JavaScript 读取该头时，在 `CorsConfig` 中设置：

```yaml
ExposeHeaders: ["Content-Disposition"]
```

前端读取示例（在 async 函数或支持顶层 await 的模块中使用）：

```javascript
const response = await fetch(downloadUrl);
const disposition = response.headers.get("Content-Disposition");
// 例如得到 'attachment; filename="report.xlsx"'，再从 filename / filename* 提取文件名。
```

`ExposeHeaders` 只允许读取响应头，不会替后端设置 `Content-Disposition` 或生成文件名。跨域时未暴露该头，即使网络面板能看到它，JavaScript 也读不到；响应未携带该头时读取结果同样为 null。仅使用 `<img src="图片地址">` 展示图片时，通常保持 `ExposeHeaders: []` 即可。

#### 本机静态文件：通过 URL 访问上传图片

静态目录与 CORS 是独立配置。文件上传和落盘由业务接口负责；`StaticConfig` 负责将已保存的公开文件映射为 URL：

```yaml
StaticConfig:
  Enabled: true
  Prefix: /uploads
  Directory: ./uploads
```

例如在服务的启动工作目录中创建 `uploads`，将图片保存为 `./uploads/avatar.png`，前端即可通过 `http://localhost:8888/uploads/avatar.png` 访问。子目录同样保留映射关系：`./uploads/2026/10/a.png` 对应 `/uploads/2026/10/a.png`。端口、域名和前缀按项目实际值调整。

`Directory` 也可以使用绝对路径，例如 `/srv/shop/uploads`；启用时目录必须已经存在。相对目录以**进程工作目录**为基准，不是相对于 YAML 文件，也不会自动定位项目根目录。上传接口保存文件时使用的目录应与这里一致。

文件服务通过 `rest.WithNotFoundHandler` 挂载，业务路由优先匹配。图片直接返回文件内容和对应 MIME 类型，不经过 `code/message/result` 包装，不需要为每个文件配置 `BypassPaths`。支持 GET、HEAD、Range 及条件请求；不缓存文件是否存在，首次访问返回 404 的文件上传后也可立即读取。CORS 配置同样作用于静态文件。

启用后该目录中的普通文件通过公开 URL 提供，不走业务路由上的认证逻辑，应只放公开资源；目录列表、隐藏路径以及指向目录外的符号链接目标不提供访问。该功能默认关闭，也不创建上传接口。需要鉴权的附件由业务 Handler 处理。

#### 自定义认证错误及额外响应字段

新项目统一使用 `httpresponse.NewError` 返回认证错误，由业务码识别错误分类，未匹配的错误交给 `httpresponse.ErrHandler`。下面的完整代码可保存为业务项目的 `common/response/response.go`；`10024`、`AUTH_SESSION_INVALID` 和恢复动作都是该项目自行约定的值，不是 gorm-plus 的默认规则。

```go
package response

import (
	"context"
	"errors"
	"net/http"

	"github.com/kuangshp/gorm-plus/httpresponse"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/trace"
)

const (
	AuthSessionInvalidCode      int64 = 10024                  // 项目约定的会话失效业务码。
	ErrorCodeAuthSessionInvalid       = "AUTH_SESSION_INVALID" // 供前端判断的稳定错误分类。
	RecoveryRestartLogin              = "restartLogin"         // 前端收到后重新登录。
)

// AuthErrorResponse 嵌入通用响应结构，按项目需要追加错误分类、恢复动作和链路标识。
type AuthErrorResponse struct {
	*httpresponse.Response
	ErrorCode string `json:"errorCode,omitempty"`
	Recovery  string `json:"recovery,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

// ErrHandler 先处理项目认证错误，其他错误继续使用 gorm-plus 的默认处理。
func ErrHandler(name string) func(context.Context, error) (int, any) {
	fallback := httpresponse.ErrHandler(name)
	return func(ctx context.Context, err error) (int, any) {
		// 保留默认的空错误处理，以及健康检查、下载等路径的跳过包装规则。
		if err == nil || httpresponse.IsBypassed(ctx) {
			return fallback(ctx, err)
		}

		var business *httpresponse.Error
		// 根据稳定业务码分类，errors.As 也能识别 fmt.Errorf("...: %w", err) 的包装。
		if errors.As(err, &business) && business != nil &&
			business.Code == AuthSessionInvalidCode {
			logx.WithContext(ctx).Infof("【%s】认证错误: %v", name, err)
			return http.StatusOK, &AuthErrorResponse{
				Response: &httpresponse.Response{
					Code:    AuthSessionInvalidCode,
					Message: business.Message,
				},
				ErrorCode: ErrorCodeAuthSessionInvalid,
				Recovery:  RecoveryRestartLogin,
				RequestID: trace.TraceIDFromContext(ctx), // 没有 traceId 时为空，JSON 中省略。
			}
		}
		return fallback(ctx, err)
	}
}
```

在项目启动入口导入 `customresponse "你的module/common/response"`，将默认的错误回调替换为自己的回调，成功响应和跳过路径中间件继续复用：

```go
if c.ResponseConfig.Enabled {
    httpx.SetOkHandler(httpresponse.OkHandler)
    httpx.SetErrorHandlerCtx(customresponse.ErrHandler(c.Name))
    server.Use(httpresponse.BypassMiddleware(c.ResponseConfig.BypassPaths...))
}
```

认证失败时可以输出 HTTP 200 和以下结构（有链路标识时额外携带 `requestId`）：

```json
{"code":10024,"message":"请先登录","errorCode":"AUTH_SESSION_INVALID","recovery":"restartLogin"}
```

在返回 error 的业务方法中，统一使用项目定义的业务码常量：

```go
return httpresponse.NewError(response.AuthSessionInvalidCode, "请先登录")
```

示例中的 `response` 为业务项目的 `common/response` 包。同一类认证错误可以使用不同提示文案，例如“token过期”，分类始终按 `AuthSessionInvalidCode` 判断。通过 `%w` 包装后仍能识别业务码，响应提示使用原始的 `business.Message`。认证分支使用项目的 go-zero `logx` 记录日志，其他错误交给默认处理器，仍使用 `slog`。

已有 main、Config、YAML 和 Handler 不会因升级库自动重写。使用包含这些包和模板的 gorm-plus 版本、更新 `gormplus` 命令后，新服务会自动接入；已有服务需合并上面的启动配置，并按需要调整无返回值 Handler。自定义 `--home` 的项目需同步模板。完整生成、编译及 HTTP 测试可运行：

```bash
GORMPLUS_GOCTL_INTEGRATION=1 go test ./httpresponse ./httpcors ./httpstatic ./generator/gozero
```

### 数字与字符串入参自动兼容

普通 DTO 和内嵌 go-zero Handler 模板已接入 `github.com/kuangshp/gorm-plus/decimaljson`，不依赖 HTTP 框架。普通 `string` 字段无需额外标签，就能自动接收 JSON 数字：

```go
Amount string `json:"amount"`
```

前端提交 `{"amount":12.20}` 或 `{"amount":"12.20"}`，解析后 `Amount` 都为字符串 `"12.20"`。数字直接按 JSON 原文转成字符串，不经过浮点数，因此保留末尾零、大整数和指数形式，也不受 float64 范围限制。普通字符串仍可接收文本和空字符串；布尔值、对象、数组不会自动转成字符串。缺失/null 的必填判断仍交给原有校验。

- **普通 DTO**：生成 `Create...DTO.UnmarshalJSON` 和 `Modify...DTO.UnmarshalJSON`，使用 `encoding/json` 的框架自动调用。修改请求单独解析，避免嵌入的 Create DTO 吞掉 ID。
- **go-zero**：生成的 Handler 先调用 `decimaljson.PrepareRequest(r, &req)`，再调用原有 `httpx.Parse` 与 `validation.Validate`，保留路径、表单、请求头解析和校验。单独使用 `json.Unmarshal` 解码 goctl 生成的 types 不会启用 Handler 的兼容步骤。
- **手写 `.api`**：直接使用下方示例中的普通 `string` 字段即可自动转换，无需 `decimal:"true"`。金额规则仍通过 `validate:"required,decimal"` 等标签交给 `validation.Validate` 校验。旧的 `decimal:"true"` 和 `validate:"decimal"` 继续兼容，并保留原有小数格式检查（空字符串、非法小数和超出 float64 范围的值返回解析错误）。原有 `float32/float64` 字段自动兼容数值字符串，解析后仍是浮点类型。
- **整数**：`int` / `int8` / `int16` / `int32` / `int64` 及对应的无符号整数类型无需额外标签，同时接受 JSON 整数和十进制整数字符串。例如 `ID int64` 可以接收 `{"id":1}` 或 `{"id":"1"}`。解析不经过浮点数，大整数保持精度；小数、指数形式、空字符串、非法文本、目标类型溢出及无符号整数的负值会报错。已有 `json:",string"` 整数字段也兼容两种入参形式。
- **嵌套数据**：支持嵌入结构体、对象、数组、切片和字符串键 map；数字与字符串兼容同样适用于指针和嵌套字段。日期、`[]byte`、自定义 `UnmarshalJSON` 类型及响应格式保持原有处理。

手写 `.api` 示例：

```go
type SavePriceReq {
    ID int64 `json:"id"`
    Amount string `json:"amount"`
    Rate string `json:"rate" validate:"required,decimal"`
}
```

生成方式：

```bash
gormplus goctl api go -api price.api --dir . --style=goZero
# 使用自己维护的 --home 时，先导出新版模板：
gormplus export-goctl-templates --output ./tools/goctl
```

业务项目需使用包含对应兼容能力的 gorm-plus 版本，并更新本地 `gormplus` 命令。未发布时可在业务项目用 `go mod edit -replace` 指向当前 gorm-plus 源码。生成器会跳过已有 DTO，goctl 也不会覆盖已有 Handler；已接入 `decimaljson.Unmarshal` 或 `decimaljson.PrepareRequest` 的文件升级库即可获得普通字符串与数字的自动兼容，尚未接入的文件需手动添加上述方法，或在保留业务逻辑后重新生成。

Swagger UI 的提交校验仍依据文档中的字段类型：字符串字段在页面中应填写 `"12.20"`。后端的自动转换不会修改 Swagger 的类型声明；通过客户端直接发送 JSON 数字同样可解析。

测试真实 goctl 与 go-zero 参数解析（需安装 goctl）：

```bash
GORMPLUS_GOCTL_INTEGRATION=1 go test ./decimaljson ./generator/...
```

### 结构体校验与 Gin 接入

结构体校验统一使用 `github.com/kuangshp/gorm-plus/validation`，不再依赖 `go-utils/k/validation`。提供 `Validate`、`HasRules`、中文错误翻译，以及复用已有引擎的 `Configure`。公共规则包括手机、邮箱、日期、小数等；项目通过 `WithRules` 集中追加规则，同名规则同时覆盖校验函数和中文提示，避免重复注册翻译。

```go
import "github.com/kuangshp/gorm-plus/validation"

type Request struct {
    Mobile string `json:"mobile" validate:"required,mobile"`
}

err := validation.Validate(&Request{}) // 返回首个中文校验错误。
```

go-zero 新 Handler 自动使用新导入路径；已有 Handler 和项目自定义模板需手动替换旧路径。Gin 项目使用 `binding` 标签，在启动时将公共规则配置到 Gin 原有引擎，`ShouldBindJSON` 后只翻译错误，无需再次校验。

完整中文示例见 [结构体校验与 Gin 接入](docs/validation.md)，包含参考 gof_cos_ser 的 `utils.ShowErrorMessage`、统一 `InitValidate`、项目规则覆盖和旧包迁移步骤。示例不会额外定义一套 Gin 校验引擎。

### Proto 生成与参数校验

配置 `proto_path` 后，生成器会为每张表生成业务 Proto，并在该目录首次生成 `base.proto`。业务 Proto 会自动引入公共定义和 Protovalidate：

```proto
import "proto/base.proto";
import "buf/validate/validate.proto";
```

其中 `proto/base.proto` 的 `proto` 会根据 `proto_path` 最后一级目录动态生成。例如 `proto_path: ./apps/rpc/desc` 会生成 `import "desc/base.proto";`。

生成器会根据数据库字段自动添加校验规则：

- 非空字段：Create 请求生成 `required`。
- `varchar(n)`：生成 `string.max_len = n`。
- `char(n)`：生成 `string.len = n`。
- 字段注释中的数字枚举：生成 `in` 校验，例如 `1、正常，2、禁用`。
- `decimal`：生成 `double.finite = true`，拒绝 `NaN` 和正负无穷值。
- `date`：使用 `base.proto` 中的 `date_format`，格式为 `YYYY-MM-DD`。
- `datetime`、`timestamp`：使用 `date_time_format`，格式为 `YYYY-MM-DD HH:mm:ss`。
- ID：必须大于 0；批量 ID 列表不能为空且每个 ID 必须大于 0。
- 分页请求：`page` 必填；查询条件均为 `optional`。

生成结果示例：

```proto
message PageSiteReq {
  PageRequest page = 1 [(buf.validate.field).required = true];

  optional string siteCode = 2 [
    (buf.validate.field).string.max_len = 32
  ];

  optional string currency = 3 [
    (buf.validate.field).string.len = 3
  ];

  optional int64 status = 4 [
    (buf.validate.field).int64 = {in: [1, 2]}
  ];

  optional string businessDate = 5 [
    (buf.validate.field).string.(date_format) = true
  ];

  optional string lastLoginAt = 6 [
    (buf.validate.field).string.(date_time_format) = true
  ];

  optional double balance = 7 [
    (buf.validate.field).double.finite = true
  ];
}
```

时间字段在请求和响应中的约定不同：Create、Modify、Page 请求使用字符串；响应 Model 使用 Unix 秒 `int64`。字符串到数据库时间类型的解析由业务方处理，生成的 mapper 不规定时区或解析方式。

### 业务自定义 CEL 校验

数据库字段规则无法覆盖的业务语义，可以直接在业务 Proto 中增加自定义 CEL。例如用户名或邮箱关键字要求长度在 5～190 个字符之间：

```proto
message SysUserKeywordReq {
  string keyword = 1 [
    (buf.validate.field).string = {min_len: 5, max_len: 190},
    (buf.validate.field).cel = {
      id: "sys_user.keyword.length"
      message: "用户名或者邮箱长度必须在5到190个字符之间"
      expression: "this.size() >= 5 && this.size() <= 190"
    }
  ]; // 用户名或者邮箱
}
```

自定义 CEL 的 `id` 应在项目内保持唯一，建议采用 `{模块}.{字段}.{规则}` 格式。`message` 应填写可直接展示给用户的业务提示。

全局校验拦截器处理提示信息时遵循以下顺序：

1. Rule ID 存在于默认或自定义 `validationMessages` 时，使用映射后的提示。
2. Rule ID 未配置时，直接使用 CEL 中声明的 `message`。
3. 两者都没有时，返回“参数不符合要求”。

因此上面的校验失败时会直接返回：

```text
字段【keyword】:用户名或者邮箱长度必须在5到190个字符之间
```

`SysUserKeywordReq` 属于业务专用消息，应添加到对应业务 Proto 中；生成器只自动维护能从数据库结构稳定推导出的通用 CRUD 校验，避免为所有数据表生成不适用的关键字查询消息。

使用生成的校验注解前，需要在 RPC 项目中引入 Protovalidate：

```bash
go get buf.build/go/protovalidate
```

### gRPC 全局参数校验

工具库已经内置统一 Unary 拦截器，在进入业务 Logic 前校验所有带 Protovalidate 规则的请求。go-zero RPC 服务可以直接注册：

```go
import gormplus "github.com/kuangshp/gorm-plus"

// 在进入业务 Logic 前统一执行 Proto 参数校验。
s.AddUnaryInterceptors(gormplus.UnaryValidationInterceptor)
```

需要扩展或覆盖默认中文提示时，创建自定义拦截器：

```go
validationInterceptor := gormplus.NewUnaryValidationInterceptor(
	gormplus.WithValidationMessages(map[string]string{
		// 覆盖内置提示
		"required": "此字段必须填写",
		// 增加业务自定义规则提示
		"sys_user.keyword.length": "用户名或者邮箱长度不正确",
	}),
)

s.AddUnaryInterceptors(validationInterceptor)
```

不传任何 Option 时仍使用全部内置提示：

```go
s.AddUnaryInterceptors(gormplus.NewUnaryValidationInterceptor())
```

自定义 map 会与默认提示合并：相同 Rule ID 覆盖默认值，新 Rule ID 作为扩展；创建拦截器时会复制 map，之后修改原 map 不会影响运行中的拦截器。

也可以按独立子包引用：

```go
import "github.com/kuangshp/gorm-plus/interceptor"

s.AddUnaryInterceptors(interceptor.UnaryValidationInterceptor)
```

内置拦截器采用 Fail Fast 模式，只返回第一个违规字段，并将完整的结构化 `Violations` 放入 gRPC Status Details。客户端既可以直接展示错误消息，也可以按字段路径和规则 ID 做精确处理。

<details>
<summary>查看拦截器核心实现</summary>

如需定制错误消息或校验策略，可以参考 [`interceptor/validation.go`](interceptor/validation.go)：

```go
package interceptor

import (
	"context"
	"errors"
	"fmt"

	"buf.build/go/protovalidate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

var validationMessages = map[string]string{
	"required":           "不能为空",
	"string.min_len":     "长度不能小于规定值",
	"string.max_len":     "长度不能超过规定值",
	"string.len":         "长度不符合要求",
	"string.pattern":     "格式不符合要求",
	"string.email":       "邮箱格式不正确",
	"string.uuid":        "UUID格式不正确",
	"string.date_format": "日期格式必须为 YYYY-MM-DD",
	"string.date_time_format": "时间格式必须为 YYYY-MM-DD HH:mm:ss",
	"int64.gt":           "必须大于规定值",
	"int64.gte":          "不能小于规定值",
	"int64.lt":           "必须小于规定值",
	"int64.lte":          "不能大于规定值",
	"int64.in":           "不在允许的取值范围内",
	"repeated.min_items": "至少需要一项数据",
	"repeated.max_items": "数据项数量超过限制",
	"repeated.unique":    "数据项不能重复",
}

// UnaryValidationInterceptor 在业务 Logic 执行前统一校验所有带 Protovalidate 规则的请求。
func UnaryValidationInterceptor(
	ctx context.Context,
	req any,
	_ *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	message, ok := req.(proto.Message)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "请求参数类型错误")
	}
	if err := protovalidate.Validate(message, protovalidate.WithFailFast()); err != nil {
		return nil, validationStatusError(err)
	}
	return handler(ctx, req)
}

// validationStatusError 将 Protovalidate 错误转换为包含字段路径的 gRPC 错误。
func validationStatusError(err error) error {
	var validationErr *protovalidate.ValidationError
	if !errors.As(err, &validationErr) || len(validationErr.Violations) == 0 {
		return status.Error(codes.InvalidArgument, "请求参数校验失败")
	}

	violation := validationErr.Violations[0]
	field := protovalidate.FieldPathString(violation.Proto.GetField())
	if field == "" {
		field = "request"
	}

	grpcStatus := status.New(
		codes.InvalidArgument,
		fmt.Sprintf("字段【%s】:%s", field, validationMessage(violation)),
	)
	// 同时附加结构化 Violations，支持客户端按字段和规则 ID 精确处理。
	if statusWithDetails, detailErr := grpcStatus.WithDetails(validationErr.ToProto()); detailErr == nil {
		return statusWithDetails.Err()
	}
	return grpcStatus.Err()
}

func validationMessage(violation *protovalidate.Violation) string {
	if message, ok := validationMessages[violation.Proto.GetRuleId()]; ok {
		return message
	}
	if message := violation.Proto.GetMessage(); message != "" {
		return message
	}
	return "参数不符合要求"
}
```

### RPC Context 透传

工具库提供成对的 gRPC 客户端和服务端拦截器，用于把 API context 中的租户 ID、操作人 ID 等信息通过 metadata 传递到 RPC，并恢复成数据库插件可以直接读取的 context 值：

```text
API middleware
  → context（租户、操作人）
  → gRPC client interceptor
  → metadata
  → gRPC server interceptor
  → RPC context
  → db.WithContext(ctx)
  → Tenant / AutoFill plugin
```

在 API 创建 RPC Client 时声明实际需要透传的字段：

```go
contextFields := []gormplus.ContextMetadataField{
	gormplus.PropagateTenantID[int64](), // Tenant 插件租户 ID
	gormplus.PropagateOperatorID[int64](), // AutoFill 插件操作人 ID
	gormplus.PropagateContextKey[string](gormplus.CtxContextKey2), // 操作人姓名
	gormplus.PropagateContextKey[string]("loginUserId"),
	// 固定值：不读取当前 ctx，每次 RPC 调用都传递该值。
	gormplus.PropagateContextValue[string]("serviceSource", "admin-api"),
}

rpcClient := zrpc.MustNewClient(
	c.SiteRpc,
	zrpc.WithUnaryClientInterceptor(
		gormplus.NewUnaryContextClientInterceptor(contextFields...),
	),
)
```

测试或固定租户场景可以直接传入租户 ID，不需要再包一层客户端拦截器：

```go
contextFields := []gormplus.ContextMetadataField{
	gormplus.PropagateTenantID[int64](10),
	gormplus.PropagateOperatorID[int64](20),
	gormplus.PropagateContextValue[int64]("company_id", 12),
}
```

`PropagateTenantID[int64]()` 从当前 Context 读取租户，`PropagateTenantID[int64](10)` 固定传递租户 `10`；`PropagateOperatorID[int64]()` 从 `CtxContextKey1` 读取操作人，`PropagateOperatorID[int64](20)` 固定传递操作人 `20`。它们在 RPC 服务端都会恢复成对应插件可以直接识别的 Context。

RPC 服务端只需注册一个通用恢复拦截器，不需要重复声明字段和类型：

```go
s.AddUnaryInterceptors(
	interceptor.UnaryJaegerTraceInterceptor,
	gormplus.UnaryContextServerInterceptor,
	gormplus.UnaryValidationInterceptor,
)
```

之后在 RPC Logic 中使用相同 key 读取：

```go
tenantID := gormplus.TenantIDFromCtx[int64](l.ctx)
operatorID := gormplus.CtxGetter[int64](gormplus.CtxContextKey1)(l.ctx)
operatorName := gormplus.CtxGetter[string](gormplus.CtxContextKey2)(l.ctx)
loginUserID, _ := l.ctx.Value("loginUserId").(string)
```

API 只需声明并写入需要透传的字段，RPC 端会根据载荷携带的基础类型自动恢复。目前支持 `string`、`bool`、各类整数和浮点数。字段不存在时自动跳过，不会阻止 RPC 调用；业务项目无需另外创建 `context_propagation.go`。

API 中间件先把认证结果写入请求 context：

```go
func TenantOperatorMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// tenantID、operatorID 来自已验证的登录凭证。
		ctx := gormplus.WithTenantID(r.Context(), tenantID)
		ctx = context.WithValue(ctx, gormplus.CtxContextKey1, operatorID)
		next(w, r.WithContext(ctx))
	}
}
```

API Logic 调用 RPC 时必须继续传递当前请求的 `l.ctx`。租户和操作人彼此独立：Context 中存在什么就透传什么，缺少某个字段不会影响其他字段，也不会阻止 RPC 调用。

RPC Logic 执行数据库操作时继续使用收到的 context：

```go
func (l *CreateSiteLogic) CreateSite(req *site.CreateSiteReq) (*site.EmptyResponse, error) {
	entity := mapper.CreateReqToEntity(req)
	if err := l.svcCtx.DB.WithContext(l.ctx).Create(entity).Error; err != nil {
		return nil, err
	}
	return &site.EmptyResponse{}, nil
}
```

`PropagateTenantID` 会在 RPC 端通过 `WithTenantID` 恢复租户，因此现有 Tenant 插件可直接在 `db.WithContext(l.ctx)` 的查询、更新和删除中追加租户条件。操作人字段恢复到对应的 `CtxContextKey`，供 AutoFill 插件在新增和更新时填充；操作人不会自动成为查询条件。

metadata 值经过 JSON 和 Base64URL 编码，可安全传递字符串、中文和常见标量类型。租户及操作人 metadata 只能在可信的内部 RPC 链路中使用；RPC 服务对外暴露时，还应通过认证、mTLS 或网关策略防止客户端伪造身份信息。

#### 全局与局部使用

- **全局**：为某个 RPC Client 注册客户端拦截器，该 Client 的所有调用都会尝试透传；字段不存在时自动跳过。
- **局部**：只给需要租户或操作人的 RPC Client 注册拦截器，公共 Client 不注册。
- **按请求**：即使 Client 已全局注册，也只会透传当前 context 中实际存在的字段。

租户和操作人互相独立，可以只声明或只存在其中一个。必须强制租户的接口，应在 API 认证中间件或业务鉴权逻辑中校验，不再通过 Context 透传配置控制。

---

## 二、gorm-gen 类型安全链式（GenWrap）


```go
// 基础查询
list, err := gormplus.
	GenWrap(dao.AccountEntity.WithContext(ctx)).
	LLike(dao.AccountEntity.Username, username).
	WhereIf(status != 0, dao.AccountEntity.Status.Eq(status)).
	Apply().
	Order(dao.AccountEntity.CreatedAt.Desc()).
	Limit(pageSize).Offset((page - 1) * pageSize).
	Find()

// 联表查询（使用别名）
list, err := gormplus.
	GenWrap(dao.AccountEntity.WithContext(ctx)).
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

## 三、原生 gorm 链式（Query）


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
built.Order("created_at DESC").Limit(pageSize).Offset((page - 1) * pageSize).Find(&list)

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
| `ForUpdate() / ForUpdateIf(cond)` | 查询时加 `FOR UPDATE` 悲观行锁，需要在同一事务内查询和修改 |
| `Build()` | 返回 `*gorm.DB` |

### 悲观行锁简写：ForUpdate

> **必须在显式事务中使用。** `.ForUpdate()`、`.ForUpdateIf(true)` 和 `QueryOption{ForUpdate: true}` 都只设置查询锁，不会自动开启事务，也不会检查调用方是否处于事务中。加锁查询和后续修改必须使用事务回调中的同一个 `tx`，不能混用普通 `db` 或全局 DAO。

默认自动提交模式下，每条 SQL 各自结束事务。事务外单独执行加锁查询，锁无法持续保护后续的判断和修改，因此不能依赖这种写法防止并发冲突。必须把“加锁查询 → 判断 → 修改”放进 `Transaction` / `TransactionCtx` 或 `TransactionAs` / `TransactionAsCtx` 的同一个回调中；事务提交或回滚后释放锁。

需要先查询、判断，再修改同一条数据，并且存在并发冲突时，可以直接调用 `.ForUpdate()`，等价于 `.Clauses(clause.Locking{Strength: "UPDATE"})`。`Query`、`GenWrap` 和 Repository 查询选项 `QueryOpt` 都支持；`.ForUpdateIf(condition)` 仅在条件为 true 时添加锁，false 时不改变已有查询。

这里使用的是**悲观锁**：查询时先获取数据库行锁，再根据查询结果判断和修改。其他事务对相同记录的冲突写操作或加锁查询通常需要等待，直到当前事务提交或回滚释放锁。适合扣库存、余额扣减、订单状态流转等需要防止并发冲突的场景。

下面以扣库存为例，代码放在业务项目的 Repository 扩展方法中；`db`、`ctx`、`productID` 和 `quantity` 由该方法提供，`ProductEntity` 及字段按实际生成的实体替换：

```go
// 正确：查询和修改都使用本次事务的 tx.ProductEntity。
return gormplus.TransactionAsCtx(ctx, db, dao.Use, func(tx *dao.Query) error {
    product, err := gormplus.GenWrap(tx.ProductEntity.WithContext(ctx)).
        Where(dao.ProductEntity.ID.Eq(productID)).
        ForUpdate().
        Apply().First()
    if err != nil {
        return err
    }
    if product.Stock < quantity {
        return errors.New("库存不足")
    }
    _, err = tx.ProductEntity.WithContext(ctx).
        Where(dao.ProductEntity.ID.Eq(productID)).
        Update(dao.ProductEntity.Stock, product.Stock-quantity)
    return err
})
```

使用原生 GORM 时，把事务连接传给 builder：

```go
// 放在 Transaction / TransactionCtx 的回调内，tx 为 *gorm.DB。
err := gormplus.Query[model.ProductEntity](tx, ctx).
    WhereIf(true, "id = ?", productID).
    ForUpdate().
    Build().First(&product).Error
```

Repository 查询选项也支持直接构造结构体，`ForUpdate` 字段可以传布尔表达式：

```go
// 此处仅构造选项；执行时必须传给绑定了同一事务 tx 的读取方法。
// 固定开启悲观锁，等价于 gormplus.QueryOpt().ForUpdate().Build()。
opt := gormplus.QueryOption{ForUpdate: true}

// 按业务操作决定是否开启，等价于 ForUpdateIf(...)。
opt = gormplus.QueryOption{
    ForUpdate: action == "deduct_stock" || action == "refund_stock",
}
```

生成的 Repository 会通过 `MergeQueryOptions` 将 `ForUpdate: true` 转换为原生行锁子句；合并多个选项时，只要其中一个开启，就会保留锁，后续的默认值或 false 不会取消已有锁。自行消费查询选项时，也先调用 `gormplus.MergeQueryOptions(opt)` 获取对应 `Clauses`。

上述选项应传给 `productRepository.WithTx(tx)` 返回的仓储。`FindById`、`FindOneWrapper` 等读取方法都会使用绑定连接；`ForUpdate` 本身不会绑定或开启事务。

正确的 Repository 写法，把“加锁查询 → 修改”放进同一个绑定仓储：

```go
err := gormplus.TransactionAsCtx(ctx, db, dao.Use, func(tx *dao.Query) error {
    products := productRepository.WithTx(tx)
    product, err := products.FindById(ctx, productID, gormplus.QueryOption{ForUpdate: true})
    if err != nil {
        return err
    }
    if product.Stock < quantity {
        return errors.New("库存不足")
    }
    return products.UpdateById(ctx, productID,
        gormplus.WithUpdateColumns(tx.ProductEntity.Stock.Value(product.Stock-quantity)),
    )
})
```

`ForUpdate` 只添加查询锁，不自动创建事务或修改数据。锁随事务提交/回滚释放；查询和后续修改必须使用同一事务连接。数据库需要支持 `FOR UPDATE`，例如 MySQL/InnoDB 或 PostgreSQL；SQLite 不提供这种行锁。只有需要防止并发冲突时才添加，避免在持锁期间调用外部接口或执行耗时操作。

通过 `ExecuteQuery` / `ExecutePage` 执行的加锁查询会自动跳过缓存和 SingleFlight，包括用原生 `Clauses(clause.Locking{...})` 指定的行锁。新生成的 Repository 模板也会在读取缓存前识别 Wrapper 回调中的锁；已有 Repository 使用 Wrapper 回调加锁且启用了缓存时，需要重新生成对应 Repository。直接使用 `GenWrap(...).Apply().First()` 时不经过缓存执行器。

---

## 四、SingleFlight + 可插拔缓存（SF）


### 它解决什么问题

业务里经常遇到这几个场景：

- **缓存击穿**：热 key 失效瞬间大量请求同时打 DB
- **重复查询**：同一秒内同样的查询打了 100 次 DB
- **缓存一致性**：写完数据库后旧缓存还在

SF（SingleFlight + Cache）一次解决：同一瞬间并发请求合并成一次、结果按 TTL 缓存、写操作支持精确/前缀失效。

### 缓存使用红线（必读）

| 红线 | 说明 | 正确做法 |
|---|---|---|
| **列表/条件查询必须带完整 cache args** | 生成模板默认 args 往往只有分页等字段，**不含 Where 条件**；不同条件共用同一 key 会串数据 | 使用 `WithCacheArgs` / `WithCacheArgsFromStruct(req)` / `BuildArgsFromStruct`；不确定时宁可不缓存 |
| **Redis Get 必须返回 `RawValue`** | 直接返回 `[]byte` 会导致类型还原失败、命中率静默为 0 | 参考独立模块 [`examples/redis-sfcache`](./examples/redis-sfcache) |
| **生产 Redis 必须实现前缀删除** | 未实现 `SFCachePrefixDeleter` 时，`SFInvalidatePrefix(es)` **静默 no-op**（兼容旧行为） | 实现 SCAN 版 `DelByPrefix`（建议再加 `DelByPrefixes`）；可用 `SetCachePrefixInvalidateNoopHandler` 观测漏配 |
| **行锁查询不走缓存** | `ForUpdate` / locking clause 经 `ExecuteQuery`/`ExecutePage` 时自动跳过 SF/Cache | 勿对加锁读手动套 `SF` |
| **坏缓存会降级到 `fn()`** | JSON 损坏或类型不匹配时删坏 key 并回源 DB，可用 `SetCacheUnwrapErrorHandler` 观测 | 不要假设「缓存错误会直接失败」 |

性能相关：同一进程可跑库内基准，例如 `go test ./sf ./query -bench=. -benchmem`。

### 查询流程图

#### 整体链路：从前端请求到数据库

业务方最常关心的是:**一个请求进来,sf+cache 在哪里、命中和未命中时分别走什么路径**。

执行顺序遵循 go-zero `sqlc.CachedConn` 模式:**先查缓存(外层),未命中才进 singleflight(内层)**——命中场景零开销,未命中场景由 sf 合并并发请求保护数据库。

```mermaid
flowchart LR
    Client([客户端<br/>浏览器/APP])

    subgraph App[业务服务进程]
        direction TB
        Handler[Handler<br/>HTTP 路由层]
        Service[Service<br/>业务逻辑层]
        Repo[Repository<br/>generator 自动生成]

        Handler --> Service
        Service --> Repo
    end

    subgraph SFLayer[gormplus.SF + Cache 层]
        direction TB
        CacheRead[(① 缓存<br/>内存 / Redis)]
        SFExec{② sf 合并并发<br/>同 key 只跑一次}

        CacheRead -. 未命中才进 .-> SFExec
    end

    DB[(MySQL / PG<br/>真实数据库)]

    Client -- HTTP 请求 --> Handler
    Repo -- FindById / FindList<br/>带 WithCache --> CacheRead

    CacheRead == 命中 ==> Repo
    SFExec -- 未命中<br/>打 DB --> DB
    DB -- 返回 + 回填 --> CacheRead
    CacheRead -. 回填后返回 .-> Repo

    Repo --> Service --> Handler
    Handler -- HTTP 响应 --> Client

    classDef hit fill:#d4edda,stroke:#28a745
    classDef miss fill:#fff3cd,stroke:#ffc107
    class CacheRead hit
    class DB miss
```

阅读要点:

- **缓存命中(粗实线)**:请求进来直接命中缓存返回,**根本不进 singleflight、不打 DB**。同一秒来 100 个相同请求都走这条路
- **缓存未命中**:才进入 singleflight,把 N 个并发请求合并成 1 次去打 DB,其余 N-1 个等待并共享结果(防缓存击穿)
- **回填**:DB 查回的数据 `cache.Set` 后,下一次同 key 请求直接命中缓存
- **写操作(Create/Update/Delete)**:模板自动调 `invalidateWriteCaches` 把缓存前缀清掉,下次读会重新打 DB → 回填缓存


### 方式一：内存缓存（默认，零配置）

```go
// 无需任何配置，懒加载内存缓存
defer gormplus.StopSFCache() // 退出时停掉后台清理 goroutine

// ① 带缓存（30 秒）
list, err := gormplus.SF(func() ([]*model.Account, error) {
	var result []*model.Account
	err := gormplus.Query[*model.Account](db, ctx).
		WhereIf(status != 0, "status = ?", status).
		Build().Find(&result)
	return result, err
}, "Account.List", map[string]any{"status": status, "page": pageNum}, 30*time.Second)

// ② 纯 singleflight（不缓存，只合并并发，适合余额、详情等实时数据）
account, err := gormplus.SFNoCache(func() (*model.Account, error) {
	var a model.Account
	err := db.WithContext(ctx).Where("id = ?", id).First(&a).Error
	return &a, err
}, "Account.Detail", map[string]any{"id": id})

// ③ 写后失效（精确失效，args 必须和查询时一致）
gormplus.SFInvalidate("Account.FindById", map[string]any{"id": id})
```

### 方式二：Redis 缓存（多实例部署推荐）

⚠️ **重要**：Redis 缓存的 `Get` 必须返回 `gormplus.RawValue(b)` 而不是反序列化后的 `any`，否则 sf 包内部的类型断言会失败，导致缓存命中率永远为 0（业务无感知，但每次都打 DB）。

```go
import (
	"context"
	"encoding/json"
	"time"

	gormplus "github.com/kuangshp/gorm-plus"
	"github.com/redis/go-redis/v9"
)

// 实现 SFCache 接口（Get / Set / Del 必选）+ 三个可选接口
type RedisSFCache struct {
	rdb    *redis.Client
	prefix string
}

// 必选：Get 返回 RawValue 让框架自动反序列化到业务期望的类型 T
func (c *RedisSFCache) Get(key string) (any, bool) {
	b, err := c.rdb.Get(context.Background(), c.prefix+key).Bytes()
	if err != nil {
		return nil, false
	}
	return gormplus.RawValue(b), true // ← 关键：用 RawValue 包装字节流
}

// 必选：Set 把任意 T 序列化为 []byte 写入 Redis
func (c *RedisSFCache) Set(key string, val any, ttl time.Duration) {
	b, err := json.Marshal(val)
	if err != nil {
		return
	}
	c.rdb.Set(context.Background(), c.prefix+key, b, ttl)
}

// 必选：精确删除
func (c *RedisSFCache) Del(key string) {
	c.rdb.Del(context.Background(), c.prefix+key)
}

// 可选 ①：实现 SFCachePrefixDeleter 支持前缀失效（SFInvalidatePrefix 需要）
//
//	用 SCAN 而非 KEYS，避免阻塞 Redis 集群
func (c *RedisSFCache) DelByPrefix(prefix string) {
	ctx := context.Background()
	var cursor uint64
	for {
		keys, next, err := c.rdb.Scan(ctx, cursor, c.prefix+prefix+"*", 500).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			c.rdb.Del(ctx, keys...)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

// 可选 ②：实现 SFCachePrefixBatchDeleter 支持批量前缀失效（性能优化，强烈推荐）
//
//	一次 pipeline 处理多个前缀，比循环调用 DelByPrefix 快 N 倍
//	generator 模板里的 invalidateWriteCaches 会一次清 11 个前缀
func (c *RedisSFCache) DelByPrefixes(prefixes []string) {
	ctx := context.Background()
	pipe := c.rdb.Pipeline()
	for _, prefix := range prefixes {
		var cursor uint64
		for {
			keys, next, err := c.rdb.Scan(ctx, cursor, c.prefix+prefix+"*", 500).Result()
			if err != nil {
				break
			}
			if len(keys) > 0 {
				pipe.Del(ctx, keys...)
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	_, _ = pipe.Exec(ctx)
}

// 可选 ③：实现 SFCacheCloser，让 StopSFCache 自动关闭 Redis 客户端
func (c *RedisSFCache) Close() error {
	return c.rdb.Close()
}

// ===== 启动时注册（强制只能调用一次，重复会 panic）=====
func main() {
	rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	gormplus.RegisterCache(&RedisSFCache{rdb: rdb, prefix: "myapp:sf:"})
	defer gormplus.StopSFCache() // 自动调用 Close 关闭 Redis 客户端

	// 业务代码与内存缓存完全一致，无需任何改动
	list, err := gormplus.SF(fn, "Account.List", args, 30*time.Second)
}
```

| 维度 | 内存缓存（默认） | Redis 缓存 |
|---|---|---|
| 配置 | 零配置 | 启动期 `RegisterCache` 一次 |
| 适用 | 单机、开发测试 | 多实例部署、缓存共享 |
| 退出清理 | `defer StopSFCache()` | 同左（自动调用 Close） |
| 业务代码 | 完全一样 | 完全一样 |
| 前缀失效 | 内置支持 | 需实现 `DelByPrefix` |
| 批量失效 | 内置支持 | 推荐实现 `DelByPrefixes` |

### 主动失效：精确 vs 前缀

| API | 何时用 | 示例 |
|---|---|---|
| `SFInvalidate(fnName, args)` | args 完全可预知（如 FindById） | `gormplus.SFInvalidate("user.FindById", gormplus.BuildArgs("id", 1))` |
| `SFInvalidatePrefix(fnName)` | args 不可穷举（如 FindList、Count，Where 条件千变万化） | `gormplus.SFInvalidatePrefix("user.FindList")` |
| `SFInvalidatePrefixes(fnNames)` | 一次清多个方法（generator 模板用） | 见下文 |

**前缀失效安全保护**：`fnName` 为空字符串或不含点号且短于 3 字符的可疑前缀会被静默拒绝，避免误清全部缓存。

方法名匹配 `sf:user.FindList:`，表名匹配 `sf:user.`。整表失效同时清除详情、列表、分页及统计等方法的所有参数组合；单个与批量接口均支持表名尾点号，且不会清除 `user_role` 等其他表的缓存。自定义 Redis 缓存仍需实现前缀删除接口。

```go
// ① 精确失效：FindById 更新后
repo.UpdateById(ctx, id, gormplus.WithUpdateColumns(...))
gormplus.SFInvalidate("user.FindById", gormplus.BuildArgs("id", id))

// ② 前缀失效：删一条记录后，列表/统计缓存全清
repo.DeleteById(ctx, id)
gormplus.SFInvalidatePrefix("user.FindList")
gormplus.SFInvalidatePrefix("user.FindPage")
gormplus.SFInvalidatePrefix("user.Count")

// ③ 批量前缀失效（推荐，Redis 场景下一次 pipeline 处理）
gormplus.SFInvalidatePrefixes([]string{
	"user.FindList",
	"user.FindPage",
	"user.Count",
	"user.Exists",
})

// ④ 一把清整张表（注意尾部要带点号，避免误伤 user_role 等前缀相同的表）
gormplus.SFInvalidatePrefix("user.")
```

### generator 模板自动失效

代码生成器产出的 Repository 在写操作后**自动失效缓存**，业务方无需手动调用：

| 写操作 | 自动失效范围 |
|---|---|
| `Create` / `CreateBatch` | List / Page / Count / Exists 等 11 个前缀（一次批量） |
| `UpdateById` / `UpdateMapById` | 同上 + 该 ID 的 FindById 精确失效 |
| `DeleteById` / `DeleteByIdList` | 同上 + 该 ID 的 FindById 精确失效 |
| 条件 `Update/Delete`（不知道影响哪些 ID） | 整张表所有缓存按前缀失效（保守策略） |

生成的代码里有两个辅助方法：

```go
// invalidateWriteCaches 失效所有受写操作影响的缓存（List/Page/Count/Exists 全部前缀）。
// 用 SFInvalidatePrefixes 批量接口，Redis 场景下一次 pipeline 完成。
func (r *defaultUserRepository) invalidateWriteCaches() {
	gormplus.SFInvalidatePrefixes([]string{
		"user.FindList", "user.FindListByWrapper",
		"user.FindPage", "user.FindPageByWrapper",
		"user.FindByIdList",
		"user.FindOne", "user.FindOneWrapper",
		"user.Count", "user.CountByWrapper",
		"user.Exists", "user.ExistsByWrapper",
	})
}

// invalidateAllTableCaches 失效整张表所有缓存（条件 Update/Delete 用）。
func (r *defaultUserRepository) invalidateAllTableCaches() {
	gormplus.SFInvalidatePrefix("user.")
}
```

### 列表/分页查询：必须显式声明 cache args

主键查询（`FindById`、`FindByIdList`）框架自动把主键写进 args，cache key 全局唯一。但 **`FindList` / `FindPage` / `FindOne` / `Count` / `Exists` 这些方法的 Where 条件是 `gen.Condition` 接口类型，无法稳定序列化**——必须用 `WithCacheArgs` 把影响查询的参数显式声明出来，否则会出现 **不同 Where 命中同一 cache key 串数据** 的脏读问题。

```go
// ❌ 危险：两次查询条件不同但 cache key 相同 → 串数据
repo.FindList(ctx, query.Query().Where(...).WithCache(5*time.Minute).Build())

// ✅ 正确：显式声明所有影响查询的参数
repo.FindList(ctx, query.Query().
Where(dao.User.Status.Eq(status), dao.User.UserID.Eq(userId)).
WithCache(5*time.Minute).
WithCacheArgs("status", status, "user_id", userId).   // ← 关键
Build())

// ✅ 从 HTTP DTO 灌入（多个 Args 会自动合并）
repo.FindPage(ctx, page, size, query.Query().
Where(buildConditions(req)...).
WithCache(30*time.Second).
WithCacheArgs("user_id", userId).         // service 层贡献
WithCacheArgsMap(req.ToMap()).            // handler 层贡献
Build())
```

### 缓存反序列化失败的观测

Redis 数据被外部改坏、跨版本结构变更等场景下，缓存反序列化会失败。框架默认**降级到 DB**，对业务透明——但这意味着缓存悄悄失效却没人知道。注入观测钩子可监控：

```go
gormplus.SetCacheUnwrapErrorHandler(func(key string, err error) {
	zap.L().Warn("cache unwrap failed",
		zap.String("key", key),
		zap.Error(err),
	)
	metrics.CacheUnwrapErrors.Inc()
})
```

钩子会在高频路径执行，要尽量快、不阻塞。钩子内 panic 会被框架吞掉，不影响主流程。

### 缓存 TTL 建议

| 场景 | 推荐 TTL | API |
|---|---|---|
| 列表 / 统计查询 | 3s ~ 30s | `WithCache(30*time.Second)` |
| 配置 / 字典数据（几乎不变） | 1min ~ 5min | `WithCache(5*time.Minute)` |
| 详情 / 实时数据（用户余额等） | 0 | `WithSingleFlight(0)` 或 `SFNoCache` |

### 注册期 vs 运行期：RegisterCache / ForceReplaceCache

```go
// 启动期注册一次（强制幂等保护，重复调用会 panic）
gormplus.RegisterCache(redisCache)

// 测试隔离 / 运维灰度切换缓存层（带数据丢失风险，慎用）
func TestXxx(t *testing.T) {
	gormplus.ForceReplaceCache(gormplus.NewMemoryCache())
	defer gormplus.StopSFCache()
	// ... 测试逻辑
}
```

---

## 五、DAL SQL 文件化查询


不同于链式条件构造器，DAL 模块将 SQL 完整写在独立 `.sql` 文件中，
通过 `//go:embed` 打包进二进制，天然支持复杂 SQL、DBA 审核、版本管理。

### 推荐目录结构

```
your-project/
└── query/dal/
    ├── init.go       ← embed 声明 + 初始化（调用方编写）
    └── rawsql/
        ├── account/
        │   ├── list.sql
        │   ├── page.sql
        │   ├── count_page.sql
        │   └── find_by_id.sql
        └── order/
            ├── page.sql
            └── count_page.sql
```

### 初始化

```go
// init.go（embed 必须在调用方包内声明）
package yourpkg

import (
	"embed"
	gormplus "github.com/kuangshp/gorm-plus"
	"io/fs"
	"log"
	"time"
)

//go:embed rawsql
var SQLFS embed.FS

func InitDAL(db *gorm.DB) {
	sub, _ := fs.Sub(SQLFS, "rawsql") // 可选,写上这个在下面的时候"account/find_by_id.sql"不需要加rawsql
	d, err := gormplus.NewDal(
		db,
		gormplus.NewEmbedLoader(sub),
		gormplus.WithDALDebug(true), // 开发环境开启
		gormplus.WithDALCacheCleanup(30*time.Minute), // 可选
	)
	if err != nil {
		log.Fatal(err)
	}
	defer d.Close() // 程序退出时停止后台 goroutine
}
```

### 单数据源使用

```go
// 查询多条（位置参数 ?）
rows, err := gormplus.DALQuery[AccountVO](ctx, "account/list.sql", 1, 10, 0)

// 查询单条（位置参数 ?）
account, err := gormplus.DALQueryOne[AccountVO](ctx, "account/find_by_id.sql", 123)
if err != nil {
	return err
}
if account == nil {
	return errors.New("账号不存在")
}

// 命名参数查询（@name）
rows, err := gormplus.DALQueryNamed[AccountVO](ctx, "account/search.sql", map[string]any{
	"username": "张", "status": 1, "limit": 10, "offset": 0,
})

// 分页查询（count SQL 自动推导：page.sql → count_page.sql）
result, err := gormplus.DALQueryPage[AccountVO](
	ctx, "account/page.sql",
	[]any{1},     // 业务过滤参数，同时传给 count SQL
	[]any{10, 0}, // 分页参数（LIMIT, OFFSET），仅传给数据 SQL
)
// result.List — 当页数据  result.Total — 总条数

// 命名参数分页
result, err := gormplus.DALQueryPageNamed[OrderVO](ctx, "order/page.sql", map[string]any{
	"account_id": 123, "status": 1, "limit": 10, "offset": 0,
})

// 执行（INSERT / UPDATE / DELETE）
err := gormplus.DALExec(ctx, "account/disable.sql", 123)

// 执行并返回影响行数
res, err := gormplus.DALExecAffected(ctx, "account/update_status.sql", 0, 123)
if res.RowsAffected == 0 {
	return errors.New("记录不存在")
}

// 查询数量
total, err := gormplus.DALCount(ctx, "account/count_page.sql", 1)
```

### SQL 文件示例

```sql
-- rawsql/account/page.sql（位置参数 ?）
SELECT id, username, status, created_at
FROM   account
WHERE  status = ? AND deleted_at IS NULL
ORDER BY created_at DESC LIMIT ? OFFSET ?

-- rawsql/account/count_page.sql（与 page.sql 过滤条件完全一致，去掉分页）
SELECT COUNT(*) FROM account WHERE status = ? AND deleted_at IS NULL

-- rawsql/account/search.sql（命名参数 @name，空值/-1 表示不过滤）
SELECT id, username, status FROM account
WHERE deleted_at IS NULL
  AND (@username = ''  OR username LIKE CONCAT('%', @username, '%'))
  AND (@status  = -1  OR status   = @status)
ORDER BY created_at DESC LIMIT @limit OFFSET @offset
```

### 事务

```go
err := gormplus.DALWithTx(ctx, func(tx *gorm.DB) error {
	// 加锁查库存（FOR UPDATE）
	stock, err := gormplus.DALTxQueryOne[StockVO](ctx, tx, "stock/find_for_update.sql", productID)
	if err != nil {
		return err
	}
	if stock == nil || stock.Quantity < qty {
		return errors.New("库存不足")
	}

	// 扣库存
	if err := gormplus.DALTxExec(ctx, tx, "stock/deduct.sql", qty, productID, qty); err != nil {
		return err
	}
	// 创建订单
	return gormplus.DALTxExec(ctx, tx, "order/insert.sql", accountID, productID, qty, amount, orderNo)
})
```

### 多数据源

```go
// 初始化第二个数据源
reportSub, _ := fs.Sub(reportSQLFS, "rawsql")
reportDAL, _ := gormplus.NewDal(reportDB, gormplus.NewEmbedLoader(reportSub))

// 请求入口注入一次，后续写法完全不变
ctx = gormplus.WithDALDB(ctx, reportDAL)
rows, err := gormplus.DALQuery[ReportVO](ctx, "report/monthly.sql", 2024)
```

### Hook（慢 SQL 监控）

```go
type SlowDALHook struct{ Threshold time.Duration }

func (h *SlowDALHook) Before(ctx context.Context, sqlFile string, args []any) {}
func (h *SlowDALHook) After(ctx context.Context, sqlFile string, args []any, cost time.Duration, err error) {
	if cost > h.Threshold {
		log.Printf("[慢SQL] file=%s cost=%s", sqlFile, cost)
	}
}

d, err := gormplus.NewDal(db, gormplus.NewEmbedLoader(sub),
gormplus.WithDALDebug(true),
gormplus.WithDALHook(&SlowDALHook{Threshold: 200 * time.Millisecond}),
)
```

| 函数 | 说明 |
|------|------|
| `NewDal` | 初始化全局默认 DAL 实例，返回句柄（用于 Close） |
| `NewDalWithProvider` | 使用自定义 DBProvider（读写分离、多租户） |
| `WithDALDB` | 将指定实例注入 context，多数据源切换 |
| `DALPreload` | 预热 SQL 缓存，启动时校验路径 |
| `DALQuery[T]` | 查询多条（位置参数 ?） |
| `DALQueryOne[T]` | 查询单条（位置参数 ?） |
| `DALQueryNamed[T]` | 命名参数查询多条（@name） |
| `DALQueryOneNamed[T]` | 命名参数查询单条（@name） |
| `DALQueryPage[T]` | 位置参数分页，count SQL 自动推导 |
| `DALQueryPageNamed[T]` | 命名参数分页 |
| `DALExec` | 执行 SQL，不关心影响行数 |
| `DALExecAffected` | 执行 SQL 并返回影响行数 |
| `DALCount` | 查询数量 |
| `DALWithTx` | 开启事务（自动提交/回滚） |
| `DALTxQuery[T]` | 事务中查询多条 |
| `DALTxQueryOne[T]` | 事务中查询单条 |
| `DALTxQueryNamed[T]` | 事务中命名参数查询 |
| `DALTxCount` | 事务中查询数量 |
| `DALTxExec` | 事务中执行 SQL |
| `DALMustExec` | 执行失败 panic（初始化阶段） |
| `DALMustQueryOne[T]` | 查询失败或不存在时 panic |


---

## 六、事务工具

### Repository 默认连接与多数据源

启动时注册一次默认连接，构造仓储时可选传入其他 `*gorm.DB`：

```go
gormplus.Use(mainDB)

users := repository.NewUserRepository()          // 使用 mainDB
reports := repository.NewUserRepository(reportDB) // 使用 reportDB
archives := repository.NewUserRepository(archiveDB) // 使用 archiveDB
```

每个实例固定构造时选中的连接，显式传入句柄不会修改默认连接。`Use` 同步初始化生成的业务 DAO，仍可使用 `dao.UserEntity.ID` 等字段表达式。`Use` 应在服务启动阶段调用；业务请求通过传参选择连接。变更默认连接只影响之后构造的仓储，已构造实例保持原连接。只配置 `dao.SetDefault(db)` 的旧项目也继续支持无参构造；同时配置时以 `gormplus.Use` 为准。

允许在包级变量中提前无参构造仓储：默认连接尚未初始化时，操作返回 `gorm.ErrInvalidDB`，不会固定无效连接。启动时完成 `gormplus.Use(db)` 或 `dao.SetDefault(db)` 后，首次操作会安全绑定当时的默认连接；绑定成功后保持固定，后续切换默认库不会改变该实例。数据库初始化仍须在接收并发业务请求之前完成。

构造函数最多接受一个句柄；显式传入 `nil`、未初始化句柄或多个句柄会 panic，便于初始化时暴露配置错误。各连接的插件和连接池仍由业务项目初始化，工具库不会自动关闭传入的句柄。事务继续使用 `WithTx(tx)`；构造函数直接传入本库创建的事务句柄也会保留事务检查和提交后失效规则。

生成仓储的缓存名称使用 `数据源摘要.表名.方法名`，不同库的同表同 ID 不会互相命中，写入也只失效当前数据源的缓存。摘要来自驱动类型和完整 DSN，不包含明文连接信息；同一数据源应复用同一个句柄或完全一致的连接配置。自定义驱动、仅传入底层 `Conn`、缺少 DSN 或 SQLite 内存库无法确定稳定身份，生成仓储会跳过缓存和 SingleFlight，直接查询。数据源隔离不会自动代替租户、权限和筛选参数的缓存隔离。

自定义仓储方法使用 `r.cacheName("方法名")`，以及 `gormplus.RepositoryQueryOptions(db, opt)` 保持相同规则。仓储外手动失效生成仓储的缓存时，名称也通过 `gormplus.RepositoryCacheName(db, "user.FindById")` 构造。

已有扩展 `.go` 文件不会被生成器覆盖。重新生成 `_gen.go` 后，在扩展文件引入 `gorm.io/gorm`，将已有构造函数改为：

```go
func NewUserRepository(dbs ...*gorm.DB) UserRepository {
    return &customerUserRepository{
        defaultUserRepository: newDefaultUserRepository(dbs...),
        // 保留已有业务依赖初始化。
    }
}
```

### Repository 事务绑定

生成的 Repository 统一使用 `WithTx(tx)` 绑定事务副本，增删改查继续调用同一套普通方法：

```go
// 普通调用：没有加入外部事务。
err := userRepository.Create(ctx, user)

// 事务调用：同一个 tx 可以绑定多个 Repository。
err = gormplus.TransactionAsCtx(ctx, db, dao.Use, func(tx *dao.Query) error {
    users := userRepository.WithTx(tx)
    profiles := profileRepository.WithTx(tx)

    if err := users.Create(ctx, user); err != nil {
        return err
    }
    // user.ID 已回填；任意一步返回错误，两个仓储的写入一起回滚。
    profile.UserId = user.ID // 字段名按实际生成的模型调整。
    return profiles.Create(ctx, profile)
}) // 回调返回 nil 时提交，返回 error 或 panic 时回滚；panic 继续向外传播。
```

`WithTx` **只绑定已有事务，不开启或提交事务**，返回完整业务 Repository 接口，原仓储不变。必须使用返回值：`users := userRepository.WithTx(tx)`，或者直接 `userRepository.WithTx(tx).Create(ctx, user)`。只调用 `userRepository.WithTx(tx)` 而不接收返回值，不会改变后续调用的连接。

不加 `WithTx` 就不会加入这个外部事务；GORM 自身默认的单次写入事务机制仍然有效。即使传入相同的 `ctx`，原仓储也不会自动加入事务。

### 同一事务中查询、修改、删除

```go
err := gormplus.TransactionAsCtx(ctx, db, dao.Use, func(tx *dao.Query) error {
    users := userRepository.WithTx(tx)
    user, err := users.FindById(ctx, userID, gormplus.QueryOpt().ForUpdate().Build())
    if err != nil {
        return err
    }
    if user.Status != 1 {
        return errors.New("用户状态不允许修改")
    }
    return users.UpdateById(ctx, userID,
        gormplus.WithUpdateColumns(tx.UserEntity.Status.Value(2)),
    )
})
```

`Create`、`CreateBatch`、全部 `Update*` / `Delete*`，以及 `FindById`、列表、分页、`Count`、`Exists` 和 Wrapper 查询都从绑定仓储取连接。行锁和后续修改因此可以使用同一个事务。

事务中的生成查询会自动跳过缓存和 SingleFlight，即使传入 `WithCache` / `WithSingleFlight` 也一样，避免读到事务外旧值或把未提交数据共享给其他请求。事务中的生成写入只登记受影响的表，最外层提交后按表去重失效缓存；回滚不执行这些失效。提交报错时结果可能不确定，会保守失效。自定义缓存后端须实现 `SFCachePrefixDeleter` 或 `SFCachePrefixBatchDeleter`，才能执行表前缀失效。

当前进程还会隔离失效前已经运行的 SingleFlight 查询，并阻止这些旧查询在失效后回填缓存；这不是跨进程的强一致缓存协议。多实例共享 Redis 时，仍应按业务设置合理 TTL；余额、库存等关键判断使用事务内查询。

### 四个事务入口

| API | 回调参数 | 用法 |
|---|---|---|
| `Transaction(db, fn, opts...)` | `*gorm.DB` | 原生 GORM / DAL；需要仓储时在回调内 `dao.Use(tx)` |
| `TransactionCtx(ctx, db, fn, opts...)` | 带上下文的 `*gorm.DB` | 同上，自动传递请求上下文 |
| `TransactionAs(db, dao.Use, fn, opts...)` | `*dao.Query` | 类型安全的 DAO / Repository |
| `TransactionAsCtx(ctx, db, dao.Use, fn, opts...)` | `*dao.Query` | 推荐，带请求上下文的 DAO / Repository |

`dao.Query` 属于业务项目，由 `dao.Use` 注入，公共包无需反向导入业务 DAO。转换函数的签名与当前 gorm-gen 一致：`func(*gorm.DB, ...gen.DOOption) *dao.Query`。

```go
// 原生事务入口也能绑定 Repository。
err := gormplus.TransactionCtx(ctx, db, func(tx *gorm.DB) error {
    users := userRepository.WithTx(dao.Use(tx))
    return users.Create(ctx, user)
})
```

事务必须通过以上四个入口创建。`WithTx(nil)`、绑定普通连接、绑定由原生 `db.Begin()` / `db.Transaction()` / `dao.Query.Transaction()` 创建且未被本库跟踪的事务，都会在操作时返回错误，不会悄悄改用全局 DAO。未跟踪事务返回 `gormplus.ErrUnmanagedTransaction`。事务提交、回滚或退出嵌套保存点后，绑定仓储不可再用，操作返回 `sql.ErrTxDone`。

### 关联查询与业务扩展

绑定仓储的 Wrapper 查询沿用事务连接。生成的泛型 `FindUserJoinList[T]` / `FindUserJoinPage[T]` / `FindUserJoinOne[T]` 是独立函数，没有仓储实例，需要显式传入使用事务 DAO 的 builder：

```go
err := gormplus.TransactionAsCtx(ctx, db, dao.Use, func(tx *dao.Query) error {
    rows, err := repository.FindUserJoinList[UserWithDepartmentVO](ctx,
        func(ctx context.Context) dao.IUserEntityDo {
            return tx.UserEntity.WithContext(ctx).
                LeftJoin(tx.DepartmentEntity, tx.DepartmentEntity.ID.EqCol(tx.UserEntity.DeptID))
        },
        func(w gormplus.IGenWrapper[dao.IUserEntityDo]) {
            w.Where(tx.UserEntity.Status.Eq(1))
        },
    )
    if err != nil {
        return err
    }
    // 使用 rows 处理本次事务中的数据。
    _ = rows
    return nil
})
```

`WithTx` 会复制 `customerUserRepository` 和内嵌的基础仓储，保留业务接口、覆盖方法及注入依赖。业务扩展文件不会被覆盖；互斥锁等不可复制状态应通过指针持有，绑定操作也不要与原仓储字段的修改并发进行。

自定义方法必须先调用 `r.checkTx()`，通过 `r.source().UserEntity.WithContext(ctx)` 获取连接；不要直接使用全局 DAO。自定义查询用 `opt.WithLockingFrom(tx.UnderlyingDB())` 配合 `ExecuteQuery` / `ExecutePage`，才能同样跳过事务内缓存和 SingleFlight。自定义写操作成功后调用 `r.invalidateAllTableCaches()`，可复用提交后失效机制。最简单的扩展方式是组合已有普通仓储方法，这些方法已经处理上述逻辑。

### 混用 DAL、DAO 与 Repository

```go
err := gormplus.TransactionAsCtx(ctx, db, dao.Use, func(tx *dao.Query) error {
    // 从事务 DAO 取底层连接，DAL 与 Repository 使用同一事务。
    txDB := tx.OrderEntity.UnderlyingDB()
    if err := gormplus.DALTxExec(ctx, txDB, "stock/deduct.sql", quantity, productID); err != nil {
        return err
    }
    return orderRepository.WithTx(tx).Create(ctx, order)
})
```

DAL 和直接 DAO 写入没有经过生成的 Repository，不会自动登记缓存失效。如修改了有缓存的表，应在回调中取得 `scope, err := gormplus.RepositoryTransactionScope(txDB)`，检查错误后调用 `scope.InvalidateTable("stock")`；这里只登记表名，外层成功提交后才失效该表缓存。不要把事务外的 `db` 传给 DAL。

### 嵌套事务、隔离级别与更新方式

同一事务连接内再次调用本库事务入口时，沿用 GORM 保存点：子事务成功只把缓存失效登记合并到父级，父级提交才发布；子事务回滚会丢弃自己的登记。传入外层事务连接，不能重新传普通 `db`。设置 GORM `DisableNestedTransaction: true` 关闭保存点后，不允许再嵌套调用。事务回调内不要手动 `Commit` / `Rollback`，也不要并发使用事务连接。

隔离级别仍通过可选参数传入，支持程度由数据库驱动决定：

```go
err := gormplus.TransactionAsCtx(ctx, db, dao.Use, func(tx *dao.Query) error {
    return userRepository.WithTx(tx).Create(ctx, user)
}, &sql.TxOptions{Isolation: sql.LevelSerializable})
```

**此次为不兼容更新**：生成接口移除 `CreateTx`、`CreateBatchTx`、`UpdateByIdTx`、`DeleteByIdTx` 等所有重复的 Repository `*Tx` 方法。升级公共包后重新运行业务项目的 `go run generator.go`，把旧调用统一改为 `repository.WithTx(tx).普通方法(ctx, ...)`。生成的 `_gen.go` 会更新，业务扩展 `.go` 文件保留；若曾自定义覆盖基础方法或手工改变仓储结构，需要按上述连接与缓存约定调整。DAL 自身的 `DALTxExec` 等方法继续保留。

---

## 七、多租户插件

下面按“注册插件 → 定义并接入中间件 → 验证效果”说明。数据权限、自动填充和 Gin Context 适配参考 `gin-admin-api` 的 `initialize/db.go`、`internal/middleware`、`internal/datascope` 和账号路由；该项目当前没有注册租户、敏感字段或慢查询插件，这三部分按本库当前 API 补充。

参考项目依赖的是较早版本的 gorm-plus。这里沿用它的接入分层和业务字段，构造函数、事务及插件 API 以当前工具库为准。

**共同约定：**

- 数据库初始化阶段为每个实际使用的 `*gorm.DB` 注册插件，检查注册错误，再调用 `gormplus.Use(db)` 注册默认连接。
- 认证中间件先校验登录状态，再写入 `c.Set("accountId", int64账号ID)`、`c.Set("userName", 用户名)`；租户接口还需写入可信的 `tenantId`。示例不在每个插件中重复解析 token。
- 后续中间件把插件需要的数据写入 `c.Request.Context()`，Service / Repository 一路传递这个 context。下面的数据库片段放在 Repository 或本地验证代码中。
- 示例验证先不启用查询缓存，确认 SQL 过滤正确后，再单独设计租户、权限及筛选条件的缓存隔离。

### 1、先注册插件

本节假设业务表 `sys_order` 已存在 `tenant_id` 字段，实体中也有对应字段。注册插件不会创建字段；没有租户列的表不会因此获得租户隔离。示例 SQL 按 MySQL 展示。

```go
// 数据库初始化阶段调用一次；需要导入 gormplus、gorm。
func RegisterTenantPlugin(db *gorm.DB) error {
    return gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
        TenantField: "tenant_id",
        DuplicatePolicy: gormplus.PolicyAppend,
        ExcludeTables: []string{"sys_config", "sys_dict"},
    })
}
```

租户受保护接口使用 `PolicyAppend`，始终追加当前租户条件。默认 `PolicySkip` 会信任业务已有的租户 AND 条件；需要这种行为时再显式选择。下方高级配置均为注册时的可选方案，不要在同一个连接上重复注册插件。

### 2、定义并接入中间件

```go
// internal/middleware/TenantMiddleware.go
// 导入 net/http、github.com/gin-gonic/gin、gormplus。
func TenantMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        value, exists := c.Get("tenantId") // 由认证结果或服务端租户选择逻辑写入。
        tenantID, ok := value.(int64)
        if !exists || !ok || tenantID <= 0 {
            c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "缺少有效租户信息"})
            return
        }
        ctx := gormplus.WithTenantID(c.Request.Context(), tenantID)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

```go
// 先认证，再注入租户；authMiddleware 是项目已有的认证中间件。
orders := router.Group("/orders", authMiddleware, middleware.TenantMiddleware())
orders.GET("", orderHandler.List)
```

认证逻辑必须先确认账号有权使用该租户，不能直接把前端传来的任意租户 ID 写入 context。插件自身在缺少租户 context 时会跳过注入，因此受保护路由要由上述中间件拒绝缺失信息的请求。

### 3、验证效果

准备两个租户各自的订单，用不同租户账号访问同一列表、详情和修改接口。在 Repository 中继续使用请求 context：

```go
// sysOrderRepository 在启动时通过 repository.NewSysOrderRepository() 注入。
list, err := sysOrderRepository.FindList(ctx)
// tenantID=1001：WHERE tenant_id = 1001。
// tenantID=2002：WHERE tenant_id = 2002。

err = sysOrderRepository.UpdateMapById(ctx, orderID, map[string]any{"status": 2})
// 同时带主键和 tenant_id 条件，不能更新另一租户的订单。
```

| 验证操作 | 预期结果 |
|---|---|
| 租户 1001 查询列表、详情 | 只能返回属于 1001 的记录 |
| 1001 使用另一租户的订单 ID 修改 | 另一租户数据不变；更新方法本身可能返回 nil，要核对影响行数或数据库数据 |
| 新增订单不手填 `tenant_id` | 默认创建策略将实体租户字段设置为当前租户 |
| 不带有效 `tenantId` 访问受保护路由 | 中间件返回 403，不执行数据库操作 |
| 租户字段放入 OR 条件 | 插件返回错误，拒绝执行 |

可在本地对 Repository 使用的数据库开启 `db.Debug()` 或配置 GORM SQL logger，核对实际 SQL。联表、排除表和重复条件的进阶用法见下文。

### 扩展配置：同一张表注入多个租户字段

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
ctx = context.WithValue(ctx, "orgID", int64(200))

// 生成：WHERE `tenant_id` = 1001 AND `org_id` = 200
```

### 扩展配置：不同表用不同字段名

```go
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
	TenantField: "tenant_id", // 兜底字段
	TableFields: map[string][]gormplus.TenantFieldConfig[int64]{
		"sys_contract": {{Field: "company_id"}}, // 改用 company_id
		"sys_order": { // 同时注入两个字段
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
gormplus.RegisterTenant(db, gormplus.TenantConfig[int64]{
	TenantField:          "tenant_id",
	AutoInjectJoinTables: gormplus.BoolPtr(false),
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
	// DuplicatePolicy: gormplus.PolicyAppend,  // 完成 OR 安全检查后追加，不去重
})

// ③ OR 危险条件自动拒绝
db.WithContext(ctx).Where("tenant_id = ? OR status = 1", 9999).Find(&list)
// Error: tenant: 检测到租户字段 "tenant_id" 出现在 OR 条件中，已拒绝执行

// 已有租户条件也不能绕过后续 OR 检查；查询、更新和删除都会在执行前拒绝。
db.WithContext(ctx).Where("tenant_id = ?", 1001).Or("tenant_id = ?", 2002).Find(&list)
```

三种重复条件策略都会完整检查后续及嵌套条件。普通业务 OR 可以正常使用，插件将其作为整体再追加隔离条件，例如 `(name = ? OR name = ?) AND tenant_id = ?`，避免 OR 的另一分支绕过租户过滤。

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

## 八、数据权限插件

数据权限负责“当前账号可以操作哪些行”。参考项目把账号、角色、部门权限计算放在 `internal/datascope`，中间件只负责取得当前账号、构建权限上下文并交给插件；Casbin 中间件另行判断能否访问接口。

### 1、先注册插件

```go
// initialize/db.go；返回错误，由启动入口决定终止初始化。
func RegisterDataPermissionPlugin(db *gorm.DB) error {
    return gormplus.RegisterDataPermission(db, gormplus.DataPermissionConfig{
        ExcludeTables: []string{
            "casbin_rule", "sys_account_post", "sys_account_role",
            "sys_role", "sys_role_resources", "sys_role_custom_dept", "sys_resources",
        },
    })
}
```

这份排除列表来自参考项目，不是其他项目的通用权限配置。读取账号、角色、部门以构建权限时，尚未挂载本次请求的注入函数，避免权限服务查询自身时递归注入。

### 2、定义并接入中间件

下面的 `datascope.NewService(db).BuildContext(ctx, accountID)`、`datascope.Context` 和 `datascope.Inject` 都是**业务项目代码**，不是 gorm-plus 提供的 API。在参考项目中可直接复用；其他项目需按自己的账号、角色和部门模型实现。中间件文件需导入 `net/http`、Gin、gormplus、gorm，以及业务 `internal/datascope` 包。

```go
// internal/middleware/DataPermissionMiddleware.go
func DataPermissionMiddleware(db *gorm.DB) gin.HandlerFunc {
    service := datascope.NewService(db)
    return func(c *gin.Context) {
        value, exists := c.Get("accountId")
        accountID, ok := value.(int64)
        if !exists || !ok || accountID <= 0 {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "请先登录"})
            return
        }
        scope, err := service.BuildContext(c.Request.Context(), accountID)
        if err != nil {
            c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "获取数据权限失败"})
            return
        }
        reqCtx := c.Request.Context()
        if scope.IsSuperAdmin() {
            reqCtx = gormplus.SkipDataPermission(reqCtx)
        } else {
            reqCtx = gormplus.WithDataPermission(reqCtx, datascope.Inject(scope))
        }
        c.Request = c.Request.WithContext(reqCtx)
        c.Set("dataScopeContext", scope) // 业务层有需要时可读取计算结果。
        c.Next()
    }
}
```

`BuildContext` 从数据库读取当前账号状态、管理员标志、角色和部门范围，合并多个有效角色的权限，并对部门 ID 去重。参考项目的枚举如下，**与旧版 README 的编号不同**：

| DataScope | 含义 | 权限服务的计算结果 |
|---|---|---|
| 1 | 全部数据 | 不追加数据范围条件 |
| 2 | 本部门 | 把当前部门放入 `DeptIDs` |
| 3 | 本部门及下级 | 查询本部门及子部门，合并到 `DeptIDs` |
| 4 | 仅本人 | 使用当前 `AccountID` |
| 5 | 自定义部门 | 从角色自定义部门关联表取得 `DeptIDs` |

参考项目的 `datascope.Context` 保存 `AccountID`、`IsAdmin`、`DeptID`、`RoleIDs`、`DataScope`、`DeptIDs`；`IsSuperAdmin()` 判断 `IsAdmin == 1`。查询字段按业务表区分，不能对所有表统一追加 `created_by`：

```go
// internal/datascope/injector.go
// 导入 gorm；Context 来自同包，DataScope 编号按上表。
func Inject(scope *Context) func(*gorm.DB, string) {
    return func(db *gorm.DB, tableName string) {
        if scope == nil {
            db.Where("1 = 0")
            return
        }
        if scope.IsSuperAdmin() || scope.DataScope == 1 {
            return
        }
        if scope.DataScope != 4 { // 部门权限先按业务表选择过滤列。
            switch tableName {
            case "sys_account", "sys_post":
                if len(scope.DeptIDs) == 0 {
                    db.Where("1 = 0")
                } else {
                    db.Where(tableName+".dept_id IN ?", scope.DeptIDs)
                }
                return
            case "sys_dept":
                if len(scope.DeptIDs) == 0 {
                    db.Where("1 = 0")
                } else {
                    db.Where(tableName+".id IN ?", scope.DeptIDs)
                }
                return
            }
        }
        // 仅本人，以及参考项目中其他受控表，按创建人过滤。
        if scope.AccountID <= 0 {
            db.Where("1 = 0")
            return
        }
        db.Where(tableName+".created_by = ?", scope.AccountID)
    }
}
```

上面是便于阅读的收敛示例：部门和子部门的查询在 `BuildContext` 中完成，不在每条业务 SQL 的回调里反复查角色。`tableName` 来自插件；自定义业务表、别名或联表时，应按真实列和权限语义调整注入函数。

```go
// 参考账号路由的实际顺序。
accounts := router.Group("/account",
    authMiddleware,
    middleware.OperatorMiddleware(),
    middleware.DataPermissionMiddleware(db),
    casbinMiddleware,
)
accounts.GET("/list", accountHandler.List)
accounts.POST("/page", accountHandler.Page)
```

### 3、验证效果

在本地准备不同部门、不同创建人的账号记录，分别以“全部数据”“本部门”“仅本人”的账号请求同一接口。Repository 无需重复拼权限 SQL：

```go
list, err := sysAccountRepository.FindList(ctx)

// 自定义 VO 分页放在 Repository 扩展中；voList 是业务 VO 切片。
offset, limit := gormplus.DbPage(req.PageNumber, req.PageSize)
total, err := dao.SysAccountEntity.WithContext(ctx).ScanByPage(&voList, offset, limit)
```

| 场景 | 预期 SQL / 结果 |
|---|---|
| 仅本人，账号 ID=1001 | `sys_account.created_by = 1001` |
| 本部门或自定义部门，`DeptIDs=[10,11]` | `sys_account.dept_id IN (10,11)` |
| 同一部门范围查询 `sys_dept` | `sys_dept.id IN (10,11)`，不是 `sys_dept.dept_id` |
| 查询按部门过滤的表，但部门范围为空 | `1 = 0`，返回零行 |
| 超级管理员 / 全部数据 | 不追加数据权限范围；其他租户、软删除等条件仍独立生效 |
| 修改或删除其他权限范围的 ID | 原记录不变；同时核对 SQL 条件和数据库数据 |
| 缺少登录账号 / 权限服务失败 | 在中间件终止，不进入业务查询 |

#### Find、Scan 与原生 SQL 的边界

权限条件覆盖 `Find`、通过 `Model` / `Table` 构建的 `Scan`、`Rows`、`Row`、更新和删除。业务 OR 会先分组，再通过 AND 连接权限条件。

`Scan(&list)` 接收到切片可映射多行；`Scan(&item)` 接收到结构体只映射一行，SQL 不会因此自动增加 `LIMIT 1`，需要单条时使用 `Limit(1).Scan(&item)`。自定义 VO 应明确源 `Model` / `Table`。

`Raw(sql).Scan(...)` 和 DAL 原生 SQL 不会自动重写 SQL 来补权限条件，需要在 SQL 中显式约束。没有挂载注入函数、排除表或使用 `SkipDataPermission` 时不会添加权限范围；受保护接口由中间件把关。

---

## 九、自动填充插件

参考项目使用 `accountId → CtxContextKey1 → CreatedBy / UpdatedBy`，避免每个新增、修改接口手动填写操作人。

### 1、先注册插件

```go
// initialize/db.go
func RegisterAutoFillPlugin(db *gorm.DB) error {
    return db.Use(gormplus.NewAutoFillPlugin(gormplus.AutoFillConfig{
        Fields: []gormplus.FieldConfig{
            {Name: "CreatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true},
            {Name: "UpdatedBy", Getter: gormplus.CtxGetter[int64](gormplus.CtxContextKey1), OnCreate: true, OnUpdate: true},
        },
    }))
}
```

`Name` 是实体字段名，也支持数据库列名。参考实体包含 `CreatedBy int64` / `UpdatedBy int64`，对应 `created_by` / `updated_by`。Getter 的类型必须与中间件写入的值一致，不能一个写 `int`、另一个读 `int64`。创建和更新时间由 GORM 的时间字段约定另行处理。

### 2、定义并接入中间件

```go
// internal/middleware/OperatorMiddleware.go
// 导入 context、net/http、Gin、gormplus。
func OperatorMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        value, exists := c.Get("accountId")
        accountID, ok := value.(int64)
        if !exists || !ok || accountID <= 0 {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "请先登录"})
            return
        }
        ctx := context.WithValue(c.Request.Context(), gormplus.CtxContextKey1, accountID)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

```go
accounts := router.Group("/account", authMiddleware, middleware.OperatorMiddleware())
accounts.POST("", accountHandler.Create)
accounts.PUT("/:id", accountHandler.Update)
```

需要同时使用数据权限时，按上一节的顺序把 `DataPermissionMiddleware` 放到操作人中间件之后。直接把 `*gin.Context` 传入仓储的项目，再按 [Context 解析器](#十一context-解析器) 注册适配。

### 3、验证效果

通过两个不同账号新增和修改同一条业务数据，新增/修改请求 DTO 都不要接收 `CreatedBy` / `UpdatedBy`，让操作人来自认证上下文。

```go
// Repository 使用请求 context，业务代码不手填操作人。
err := sysAccountRepository.Create(ctx, accountEntity)
err = sysAccountRepository.UpdateMapById(ctx, accountID, map[string]any{"nickname": "新昵称"})
```

| 操作 | 数据库中应看到的值 |
|---|---|
| 账号 1001 新增 | `created_by=1001`，`updated_by=1001` |
| 账号 2002 修改 | `created_by` 保留 1001，`updated_by=2002` |
| 配置 `CtxContextKey2` 对应姓名字段 | 中间件还需写入 `string` 类型姓名，并为实体姓名字段增加 `FieldConfig` |

缺少操作人 context 或类型不匹配时，`CtxGetter` 返回零值，插件不会代替认证中间件报错。原生 `Exec` / DAL SQL 没有实体 Schema，不会自动补操作人字段；这类 SQL 要显式传参。

---

## 十、多数据源管理

数据源管理器通过 `Dialector` 接收驱动和连接配置。它负责选择句柄，具体的租户、数据权限及自动填充插件仍需注册到每个实际使用的数据库连接。

### 1、先注册数据源

```go
// 启动阶段；mainDSN、replicaDSN、analyticsDSN 来自项目配置。
// 导入 MySQL 驱动、gormplus。
gormplus.DS.Register("default", gormplus.DataSourceGroupConfig{
    Master: gormplus.DataSourceNodeConfig{
        Dialector: mysql.Open(mainDSN),
        Pool: gormplus.DataSourcePoolConfig{MaxOpen: 50, MaxIdle: 10},
    },
    Slaves: []gormplus.DataSourceNodeConfig{
        {Dialector: mysql.Open(replicaDSN)},
    },
})
gormplus.DS.Register("analytics", gormplus.DataSourceGroupConfig{
    Master: gormplus.DataSourceNodeConfig{Dialector: mysql.Open(analyticsDSN)},
})
```

PostgreSQL、SQLite、SQL Server 分别传入对应驱动的 Dialector 即可。仅配置 Dialector 的从库也会注册；空从库配置跳过，没有有效从库时读操作回退到主库。

### 2、定义并接入中间件

```go
// internal/middleware/DSMiddleware.go；导入 net/http、Gin、gormplus。
func DSMiddleware(name string) gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx := gormplus.DSWithName(c.Request.Context(), name)
        if c.Request.Method == http.MethodGet {
            ctx = gormplus.DSWithRead(ctx)
        } else {
            ctx = gormplus.DSWithWrite(ctx)
        }
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

```go
reports := router.Group("/reports", authMiddleware, middleware.DSMiddleware("analytics"))
reports.GET("", reportHandler.List)
```

`GET → 从库` 是示例策略。POST 分页等读取接口、写后立即读取、事务和要求读到最新值的接口，按业务显式选择主库或从库。

### 3、验证效果

生成的仓储固定使用构造时的句柄。中间件只写 context 标记，**不会自动改变已经构造好的 Repository**；需要动态路由时，在业务的数据访问工厂 / Repository 扩展层解析句柄后再构造相应仓储：

```go
db, err := gormplus.DS.Auto(ctx)
if err != nil {
    return nil, err
}
repo := repository.NewSysAccountRepository(db)
return repo.FindList(ctx)
```

固定三个业务库时，也可在启动阶段分别构造仓储并注入 Service，无需每次动态选择，见 [Repository 默认连接与多数据源](#repository-默认连接与多数据源)。

| 请求 / 调用 | 应选择的句柄 |
|---|---|
| `default` + 读标记 | default 从库，多个从库轮询 |
| `default` + 写标记 | default 主库 |
| `analytics` + 读标记，未配置从库 | analytics 主库 |
| 没有读标记 | 默认走所选数据源的主库 |

在本地给各库放入可区分的数据，核对返回结果和 SQL 日志中的目标实例。`gormplus.DS.Ping()` 可查看 `default:master`、`default:slave0` 等节点状态，应用退出时调用 `gormplus.DS.Close()` 释放管理器已打开的连接。

---

## 十一、Context 解析器

这是 Gin 请求上下文与租户、权限、自动填充插件之间的适配步骤。参考项目在 `initialize/db.go` 注册一次；不是每个请求注册一次。

### 1、先注册解析器

```go
// initialize/db.go；导入 context、Gin、gormplus。
func RegisterGinContextResolver() {
    gormplus.RegisterCtxResolver(func(ctx context.Context) context.Context {
        if c, ok := ctx.(*gin.Context); ok && c.Request != nil {
            return c.Request.Context()
        }
        return ctx
    })
}
```

参考项目的启动装配可以写成下面这样；这些注册函数对应本页各插件章节，由业务初始化包提供：

```go
func RegisterAccountPlugins(db *gorm.DB) error {
    RegisterGinContextResolver()
    if err := RegisterDataPermissionPlugin(db); err != nil {
        return err
    }
    if err := RegisterAutoFillPlugin(db); err != nil {
        return err
    }
    // 租户、慢查询、敏感字段按业务需要在这里另行注册并检查错误。
    gormplus.Use(db)
    return nil
}
```

### 2、定义并接入中间件

复用 [操作人中间件](#九自动填充插件) 和 [数据权限中间件](#八数据权限插件)，它们都更新 `c.Request`。认证先于两者执行：

```go
accounts := router.Group("/account",
    authMiddleware,
    middleware.OperatorMiddleware(),
    middleware.DataPermissionMiddleware(db),
    casbinMiddleware,
)
accounts.GET("/list", accountHandler.List)

// Handler 调用 Service 时优先传标准请求 context。
list, err := accountService.List(c.Request.Context())
```

始终传 `c.Request.Context()` 的 Gin 项目无需额外解析器；项目习惯直接把 `c` 传给 Repository / DAO 时，解析器让上述三个插件能读到 Request.Context 中的值。敏感字段返回模式及慢查询 trace 示例使用标准请求 context，不依赖这个解析器。

### 3、验证效果

在完成认证和操作人中间件之后，临时检查两种传法读取到的操作人是否一致：

```go
getter := gormplus.CtxGetter[int64](gormplus.CtxContextKey1)
fromRequest := getter(c.Request.Context())
fromGin := getter(c)
// 当前账号是 1001 时，两者都应该是 int64(1001)。
```

然后执行一次新增、修改和权限列表请求，核对操作人字段及 SQL 权限范围。若只有 `getter(c)` 为零值，检查是否注册解析器；两种传法都为零值时，检查中间件顺序、`accountId` 值类型及是否更新了 `c.Request`。

| 传入数据库层的 context | 是否需要 Gin 解析器 |
|---|---|
| Gin 的 `c.Request.Context()` | 不需要 |
| 直接传 `*gin.Context` | 对上述插件需要注册 |
| go-zero 的 `r.Context()` / Logic 的标准 `ctx` | 不需要 |
| Fiber 的 `c.UserContext()` | 不需要 |

---

## 十二、慢查询监控

慢查询插件在数据库操作结束后记录耗时，不需要权限注入函数。中间件的作用是给 SQL 日志补上当前请求的 trace ID。

### 1、先注册插件

```go
// initialize/db.go；导入 context、log、time、gormplus、gorm 和业务 middleware 包。
func RegisterSlowQueryPlugin(db *gorm.DB) error {
    return gormplus.RegisterSlowQuery(db, gormplus.SlowQueryConfig{
        Threshold: 200 * time.Millisecond,
        Logger: func(ctx context.Context, info gormplus.SlowQueryInfo) {
            traceID, _ := ctx.Value(middleware.SQLTraceKey{}).(string)
            log.Printf("[SLOW SQL] trace=%s cost=%v table=%s rows=%d err=%v",
                traceID, info.Duration, info.Table, info.RowsAffected, info.Error)
        },
    })
}
```

阈值不大于 0 时使用默认的 200ms。`info.SQL` 是替换了参数的完整 SQL，需要排查时可在受控日志中输出；生产日志应按业务处理其中的敏感参数。Query、Create、Update、Delete、Row、Raw 均注册了耗时回调。

### 2、定义并接入中间件

```go
// internal/middleware/TraceMiddleware.go
// 导入 context、strconv、time、Gin。
type SQLTraceKey struct{}

func TraceMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        traceID := c.GetString("requestId") // 优先沿用项目日志中间件生成的请求 ID。
        if traceID == "" {
            traceID = strconv.FormatInt(time.Now().UnixNano(), 10)
        }
        ctx := context.WithValue(c.Request.Context(), SQLTraceKey{}, traceID)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

```go
router.Use(middleware.TraceMiddleware())
// 如果项目已有 trace 中间件，直接复用它的 key 和请求 context，无需重复生成。
```

### 3、验证效果

在本地 MySQL 测试连接中，使用经过中间件的请求 context 执行一次可控慢 SQL；这只是验证片段，不需要新增业务接口：

```go
err := db.WithContext(ctx).Exec("SELECT SLEEP(?)", 0.25).Error
// 预期出现 [SLOW SQL]，cost 约 250ms，trace 与当前请求一致。
```

正常耗时低于阈值的 SQL 不应触发这个慢查询回调。SQL 本身出错时 `info.Error` 可携带错误，但只有耗时达到阈值才会进入这个 Logger；它不能代替完整的错误日志。`SLEEP` 是 MySQL 验证语法，其他数据库使用各自的测试方式。

---

## 十三、敏感字段加密

敏感字段插件负责写入时生成存储值和盲索引，实体查询后按请求权限返回脱敏值或明文。本节是按当前库补充的示例；参考项目没有配置这些密文列，接入前要先完成自己的表结构和实体生成。

### 1、先注册插件

本节采用**加密存储**模式。实体包含一个业务展示字段和两个数据库字段：

```go
type SensitiveAccount struct {
    ID          int64  `gorm:"primaryKey" json:"id"`
    Phone       string `gorm:"-" json:"phone"`
    PhoneCipher string `gorm:"column:phone_cipher" json:"-"`
    PhoneIndex  string `gorm:"column:phone_index;index" json:"-"`
}
```

`Phone` 不直接映射数据库；`phone_cipher`、`phone_index` 列需要预先存在。密文和盲索引字段通过 `json:"-"` 隐藏，不能随实体响应直接返回。

```go
// initialize/db.go
func RegisterSensitivePlugin(db *gorm.DB, secretKey []byte) (*gormplus.SensitivePlugin, error) {
    phone := gormplus.PhoneField("Phone")
    phone.EncryptAtRest = true
    return gormplus.RegisterSensitive(db, gormplus.SensitiveConfig{
        Key: secretKey,
        Fields: []gormplus.SensitiveFieldConfig{phone},
    })
}
```

主密钥从业务安全配置中读取，至少 16 字节；插件派生独立的加密密钥和 HMAC 索引密钥。将返回的 `*gormplus.SensitivePlugin` 注入 Service / Repository，后续使用同一个实例构造手机号索引条件。

`PhoneField("Phone")` 默认匹配 `PhoneCipher`、`PhoneIndex` 和列 `phone_index`，规范化空格、横线及 `+86`，默认返回脱敏手机号。**`EncryptAtRest` 默认是 false，必须显式设 true 才保存 AES-GCM 密文。** false 模式下，虽然列名仍叫 `phone_cipher`，存储内容其实是规范化明文；已有数据不能直接切换模式，需先迁移。

通过生成器配置敏感字段时，也要保持一致：

```go
SensitiveFields: []gormplus.GeneratorSensitiveField{
    {Table: "sys_user", Field: "phone", Type: "phone", EncryptAtRest: true},
},
// 业务字段的生成 tag 将包含 encrypt:true。
```

### 2、定义并接入中间件

```go
// internal/middleware/SensitiveReturnMiddleware.go
// 导入 Gin、gormplus。
func SensitiveReturnMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        ctx := c.Request.Context()
        // 该标志必须由服务端鉴权结果设置，不读取前端的“显示明文”参数。
        if c.GetBool("canViewSensitive") {
            ctx = gormplus.WithSensitivePlaintext(ctx)
        } else {
            ctx = gormplus.WithSensitiveMasked(ctx)
        }
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

```go
accounts := router.Group("/account", authMiddleware, sensitiveAuthorizationMiddleware,
    middleware.SensitiveReturnMiddleware())
accounts.GET("/detail/:id", accountHandler.Detail)
```

`sensitiveAuthorizationMiddleware` 是项目自己的鉴权逻辑，校验查看敏感明文的权限后设置 `canViewSensitive`。Repository 使用 `c.Request.Context()` 对应的标准 context，返回模式才能跟随本次请求；未设置标志默认脱敏。

### 3、验证效果

下面是放在 Repository / 本地验证代码中的实体操作；请求 DTO 只接收原始手机号，不接收密文和索引。

```go
account := SensitiveAccount{Phone: "13800138000"}
if err := db.WithContext(ctx).Create(&account).Error; err != nil {
    return err
}

var found SensitiveAccount
err := sensitive.WhereEqual(db.WithContext(ctx), "Phone", "13800138000").First(&found).Error
// 普通请求：found.Phone == "138****8000"。
// 有明文权限的请求：found.Phone == "13800138000"。
```

使用生成仓储时，索引条件写法如下；`sysUserRepository` 和 `sensitive` 都由业务初始化注入：

```go
list, err := sysUserRepository.FindList(ctx,
    gormplus.QueryOpt().Where(
        sensitive.PhoneEq(dao.SysUserEntity.PhoneIndex, phone),
    ).Build(),
)
```

更新时传入原始明文，不能把查询出来的 `138****8000` 再保存：

```go
update := SensitiveAccount{ID: accountID, Phone: "13900139000"}
err := db.WithContext(ctx).Model(&update).
    Select("PhoneCipher", "PhoneIndex").Updates(&update).Error
```

| 验证位置 / 操作 | 加密存储模式的预期效果 |
|---|---|
| 数据库 `phone_cipher` | AES-GCM 密文，不等于原手机号 |
| 数据库 `phone_index` | HMAC 查询索引，不等于原手机号 |
| 同一手机号再次写入 | 密文可以变化，规范化后的索引保持一致 |
| 普通账号查询、序列化为 JSON | `phone` 脱敏，不出现 `phone_cipher` / `phone_index` |
| 有明文权限的账号查询 | `phone` 返回完整手机号 |
| 更新为新手机号 | 新手机号能查到，旧手机号不再匹配该记录 |

插件通过 Query 回调处理实体的 `Find` / `First` 等查询；不要假设任意 `Raw`、`Scan` 或自定义 VO 都会自动填充敏感展示字段。示例不启用缓存；开启后要把权限和返回模式纳入缓存隔离，避免有权限与无权限请求共享结果。

更多生成配置及 CRUD 说明见 [敏感字段完整文档](plugin/sensitive.md)。模糊、后四位和号段查询需要另行设计索引，当前盲索引提供精确等值匹配。

---

## 依赖


- `gorm.io/gorm`
- `gorm.io/gen`
- `gopkg.in/yaml.v3`
- `buf.build/go/protovalidate`（Proto 参数校验）
- `google.golang.org/grpc`（RPC 拦截器）
- `google.golang.org/protobuf`
- 数据库驱动由用户按需引入（`gorm.io/driver/mysql`、`gorm.io/driver/postgres` 等）
