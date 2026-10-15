# Swagger 配置、部署与 Air 热重载

gorm-plus 使用 goctl 内置的 `api swagger` 从 `.api` 生成 Swagger 2.0 文档，支持聚合入口中的 `import`。`httpswagger` 在运行时提供固定版本的本地 Swagger UI 资源，并按当前响应配置适配 JSON。核心运行时只依赖 Go 标准库和本库的 `httpresponse`。

## 配置与命令

```yaml
SwaggerConfig:
  Enabled: true # 缺省 false。开启文档路由，并由 make run/build 在构建前准备文档。
  Path: /swagger # 页面 /swagger/、JSON /swagger/swagger.json；必须避开现有业务路由。
  ApiFile: shop.api # 相对服务目录；唯一 .api 入口可填 ""，多个入口必须明确指定。
```

在业务服务目录运行：

```bash
gormplus swagger --config etc/shop-api.yaml
```

也可在仓库根目录运行：

```bash
gormplus swagger --dir apps/shop --config etc/shop-api.yaml
```

`--dir` 默认当前目录，`--config` 和 YAML 中 `ApiFile` 的相对路径都以该服务目录为基准，不随配置文件所在目录变化。YAML 键名兼容大小写；生成命令只读取 Swagger 与响应配置，不初始化数据库、Redis 或业务服务。不要同时运行多个针对同一服务目录的文档生成/构建流程。

行为约定：

- **开启**：先使旧文档失效，再调用 goctl 生成、检查有效 JSON，成功后原子更新 `swagger/swagger.json`；失败时返回非零状态，Makefile 停止后续命令。
- **关闭**：不调用 goctl，不要求 `.api` 文件存在，写入带标记的占位文件，让 `go:embed` 正常编译并清除旧文档。
- **运行时**：关闭时不注册文档路由；开启但二进制只有占位文档时明确报错，提示先生成并重新构建。

运行时不执行代码生成。只修改 YAML 不能更新二进制里的文档，直接执行 `go build`、`go run` 不会自动执行 Makefile。`ApiFile` 仅供开发/构建工具读取，部署后的程序不访问该路径。

开启 Swagger 后，生成的启动入口会输出可直接访问的完整地址。例如 `Host: 0.0.0.0`、`Port: 8888`、`Path: /swagger`：

```text
Swagger UI: http://127.0.0.1:8888/swagger/
Swagger JSON: http://127.0.0.1:8888/swagger/swagger.json
```

空 Host、`0.0.0.0`、`::` 转为本机访问地址；指定 IP/域名时沿用配置，IPv6 自动加方括号。配置服务自身的 TLS 证书后打印 `https`，反向代理的外部域名和协议则以部署入口为准。关闭 Swagger 不打印地址。已有服务需合并新版 main 的打印代码，升级依赖或重新运行 goctl 不会自动改写旧 main。

## Makefile：先生成，再运行或构建

首次使用先安装支持该功能的 gormplus 命令和 goctl；本次使用 goctl **1.10.2** 验证，需支持 `api swagger --filename`。`go install` 只安装命令，业务 `go.mod` 也应使用包含 `httpswagger` 和新版模板的 gorm-plus 版本。未发布时，可临时使用本地 `replace` 并在 gorm-plus 仓库执行 `go install ./cmd/gormplus` 测试。

```makefile
.PHONY: swagger run run-open build swagger-prod build-prod

# 开发配置和启动配置保持一致。
swagger:
	gormplus swagger --config etc/shop-api.yaml

run: swagger
	go run . -f etc/shop-api.yaml

# 可选本地入口：后台等待文档就绪，成功后打开浏览器。
run-open: swagger
	@gormplus swagger-open --config etc/shop-api.yaml &
	go run . -f etc/shop-api.yaml

build: swagger
	mkdir -p bin
	go build -o bin/shop .

# 生产构建使用对应配置；其中 SwaggerConfig.Enabled 可以为 false。
swagger-prod:
	gormplus swagger --config etc/shop-api.prod.yaml

build-prod: swagger-prod
	mkdir -p bin
	go build -o bin/shop .
```

命令行前使用 Tab 缩进。`make run` 不会重新生成 Handler 或覆盖业务代码；API 路由、类型发生变化时仍需先按项目流程运行 `make gen`。开发和生产使用不同开关时应分别构建，不要将开发环境的占位文档或旧文档当作生产文档。

`make run-open` 只供本地开发：`swagger-open` 读取同一配置中的 Host、Port、SwaggerConfig.Path，轮询文档 JSON 确认就绪后打开一次浏览器。默认等待最多 30 秒，可用 `--timeout 60s` 调整；关闭 Swagger 时直接跳过。`0.0.0.0` / `::` 会转换为本地访问地址。macOS 使用 `open`、Windows 使用系统 URL 打开方式、Linux 使用 `xdg-open`；没有桌面环境时可手动访问日志中的地址。部署启动及默认 `make run` 不自动打开浏览器。

部署示例（路径和端口按业务项目调整）：

```bash
./bin/shop -f etc/shop-api.prod.yaml
```

构建产物可从任意工作目录运行，Swagger 不再依赖磁盘上的 `swagger/` 或 `static/`。日志、上传目录等其他业务配置的相对路径仍遵循原有约定。文档路由默认公开，是否开启由部署配置决定。

## Air 热重载

在服务目录保存 `.air.toml`：

```toml
root = "."
tmp_dir = "tmp"

[build]
cmd = "gormplus swagger --config etc/shop-api.yaml && go build -o ./tmp/shop ."
entrypoint = ["./tmp/shop", "-f", "etc/shop-api.yaml"]
include_ext = ["go", "api", "yaml", "yml", "mod", "sum"]
exclude_dir = ["tmp", "bin", "swagger", "logs", ".git"]
delay = 1000
stop_on_error = true
send_interrupt = true
kill_delay = "500ms"

[misc]
clean_on_exit = true
```

`air -c .air.toml` 会在构建前准备文档；监听 `.api` 可覆盖聚合入口和导入文件。排除 `swagger` 输出目录可避免生成文件触发循环构建。路由/类型的代码生成仍由 `make gen` 完成，Air 不会替你更新 Handler 或 Types。

## API 文档内容

标题、描述、版本等信息写在 `.api` 的 `info` 中，聚合入口建议统一维护公共信息：

```go
syntax = "v1"

info (
    title: "Shop API"
    description: "商城接口文档"
    version: "1.0"
    useDefinitions: true
)
```

接口分组、字段说明、认证信息遵循 [goctl Swagger 官方语法](https://go-zero.dev/reference/cli-guide/swagger/)。不要另外启用 `wrapCodeMsg`：其格式为 `code/msg/data`，与本库的 `code/message/result` 不同；由本运行时统一处理响应外层。请保留 `.api` 中真实的业务返回类型，不要为文档再声明一层包装 DTO。

`ResponseConfig.Enabled=true` 时，成功响应 schema 加上 `code/message/result`；原始返回值为 `$ref`、对象、数组或字符串时，完整保留为 `result`。无返回值的 HTTP 200 接口展示 `code/message`，`result` 不作为必需字段。HTTP 204/205、HEAD 和非 2xx 声明保留原定义。业务自定义错误码、额外响应字段、下载文件等特殊返回仍需按真实接口完善文档，本功能不会推断所有自定义 Handler 行为。

`BypassPaths` 按包含 `basePath` 的完整路径精确匹配，跳过包装；与现有运行时一样不支持前缀或通配符。对于 `/:id` 等动态路由中特定 ID 的例外，一个静态 Swagger operation 无法同时精确表达所有返回差异，需要项目补充说明。文档中的 `host`、`schemes` 会移除，让 Try it out 使用当前浏览器来源；保留 API 的 `basePath`。

页面资源与 JSON 原样写出，不走 `httpx` 的统一响应函数，也不使用上传文件的 `StaticConfig`。UI 使用相对资源路径，因此支持 `/swagger` 或 `/api/docs` 等自定义前缀，不写死域名和端口。所有页面资源内嵌，关闭 Swagger UI 的在线校验，不需要 CDN；固定资源版本和许可证见 [assets/README.md](../httpswagger/assets/README.md)。

## 已有服务与自定义模板接入

升级库不会重写已有 main、Config 或 YAML。已有服务需要合并：

1. Config 新增 `SwaggerConfig httpswagger.Config`，YAML 添加上述配置。
2. main 导入 `_ "embed"`，添加 `//go:embed swagger/swagger.json` 和 `var swaggerDocument []byte`。
3. 配置加载后调用 `c.SwaggerConfig.Handler(swaggerDocument, c.ResponseConfig)`，检查错误；返回非 nil 时将其 `Routes()` 逐个注册为 GET、HEAD，Handler 使用 `docs.ServeHTTP`。
4. 更新 Makefile/Air，执行 `gormplus swagger --config ...` 准备占位或真实文档后再构建。

新项目通过 `gormplus goctl api go ...` 生成时，会自动准备占位且不覆盖已有文档。使用导出的 `--home` 直接运行原生 goctl 时，应在首次构建前执行 `gormplus swagger`。自定义模板需合并上述 main/config/etc 变更。

独立接入标准 HTTP 服务器时，可使用相同的运行时接口：

```go
docs, err := swaggerConfig.Handler(swaggerDocument, responseConfig)
if err != nil {
    return err
}
if docs != nil {
    // mux 为 Go 标准库 *http.ServeMux；前缀加斜杠以覆盖 UI 资源路径。
    mux.Handle(docs.Routes()[0], docs)
    mux.Handle(docs.Routes()[0]+"/", docs)
}
```

go-zero 的路由注册方式见内嵌 [main.tpl](../generator/gozero/templates/api/main.tpl)。独立注册文档路由即可，不要覆盖原来为上传目录配置的 NotFoundHandler。

## 验证

```bash
go test ./httpswagger ./generator/swagger ./cmd/gormplus ./generator/gozero
GORMPLUS_GOCTL_INTEGRATION=1 go test ./generator/gozero -run TestGoctlSwaggerIntegration -count=1 -v
```

集成测试使用真实 goctl 解析带 import 的 API、构建服务，并只携带二进制到空目录运行，检查开关、缺少文档的启动错误、UI 资源、HEAD、响应包装、白名单和静态文件服务共存。
