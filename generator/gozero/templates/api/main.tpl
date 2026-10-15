package main

import (
	_ "embed"
	"flag"
	"fmt"
    "log"
    "log/slog"
    "net"
    "net/http"
    "strconv"

    "github.com/kuangshp/gorm-plus/httpresponse"
    "github.com/zeromicro/go-zero/rest/httpx"
    "github.com/zeromicro/go-zero/core/logx"

	{{.importPackages}}
)

var configFile = flag.String("f", "etc/{{.serviceName}}.yaml", "the config file")

// 关闭文档时使用占位文件；make run/build 会按同一份 YAML 准备最新文档。
//go:embed swagger/swagger.json
var swaggerDocument []byte

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

    // 尽早校验内嵌文档；关闭时无需有效文档，开启但未生成时停止启动。
    docs, err := c.SwaggerConfig.Handler(swaggerDocument, c.ResponseConfig)
    if err != nil {
        log.Fatal(err)
    }

    // 完整沿用 LogConf，包括 ServiceName、Stat、Encoding、Level、Path、MaxContentLength 等。
    logConfig := c.Log
    if logConfig.ServiceName == "" {
        logConfig.ServiceName = c.Name
    }
    if c.LogRotation.Enabled {
        // 启用自定义归档时，先初始化级别和格式，再替换 Writer 管理文件。
        logConfig.Mode = "console"
    }
    logx.MustSetup(logConfig)

    if c.LogRotation.Enabled {
        logs, err := c.LogRotation.Open()
        if err != nil {
            log.Fatal(err)
        }
        defer logs.Close()
        logx.SetWriter(logx.NewWriter(logs))
        var level slog.Level
        switch c.Log.Level {
        case "debug":
            level = slog.LevelDebug
        case "error":
            level = slog.LevelError
        case "severe":
            level = slog.LevelError + 4
        }
        handlerOptions := &slog.HandlerOptions{Level: level}
        var logHandler slog.Handler = slog.NewJSONHandler(logs, handlerOptions)
        if c.Log.Encoding == "plain" {
            logHandler = slog.NewTextHandler(logs, handlerOptions)
        }
        // SetDefault 同时将标准库 log 转交给该 Handler，保持存储格式一致。
        slog.SetDefault(slog.New(logHandler))
    }

    // 根据配置启用本机静态文件和跨域访问。
    var options []rest.RunOption
    if c.StaticConfig.Enabled {
        // 将已存在的公开目录映射为静态 URL，作为未匹配业务路由时的文件处理器。
        files, err := c.StaticConfig.Open()
        if err != nil {
            log.Fatal(err)
        }
        defer files.Close()
        options = append(options, rest.WithNotFoundHandler(files))
    }
    if c.CorsConfig.Enabled {
        options = append(options, rest.WithCustomCors(c.CorsConfig.AddHeaders, nil, c.CorsConfig.Origins...))
    }
	server := rest.MustNewServer(c.RestConf, options...)
	defer server.Stop()

    if c.ResponseConfig.Enabled {
        // 注册统一响应；项目有认证错误分类等需求时，可替换为自己的同签名回调。
        httpx.SetOkHandler(httpresponse.OkHandler)
        httpx.SetErrorHandlerCtx(httpresponse.ErrHandler(c.Name))
        // 精确匹配的健康检查、下载等路径保留原始响应。
        server.Use(httpresponse.BypassMiddleware(c.ResponseConfig.BypassPaths...))
    }

	ctx := svc.NewServiceContext(c)
	handler.RegisterHandlers(server, ctx)

    if docs != nil {
        // 使用独立路由，保留 StaticConfig 的文件处理器；原样输出页面和 JSON。
        for _, route := range docs.Routes() {
            for _, method := range []string{http.MethodGet, http.MethodHead} {
                server.AddRoute(rest.Route{Method: method, Path: route, Handler: docs.ServeHTTP})
            }
        }
        // 监听全部网卡时，输出本机可直接访问的地址；IPv6 由 JoinHostPort 正确加括号。
        host := c.Host
        if host == "" || host == "0.0.0.0" || host == "::" {
            host = "127.0.0.1"
        }
        scheme := "http"
        if c.CertFile != "" || c.KeyFile != "" {
            scheme = "https"
        }
        address := scheme + "://" + net.JoinHostPort(host, strconv.Itoa(c.Port)) + docs.Routes()[0]
        fmt.Printf("Swagger UI: %s/\n", address)
        fmt.Printf("Swagger JSON: %s/swagger.json\n", address)
    }

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
