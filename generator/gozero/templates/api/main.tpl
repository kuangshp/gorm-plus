package main

import (
	"flag"
	"fmt"
    "log"
    "log/slog"

    "github.com/kuangshp/gorm-plus/httpresponse"
    "github.com/zeromicro/go-zero/rest/httpx"
    "github.com/zeromicro/go-zero/core/logx"

	{{.importPackages}}
)

var configFile = flag.String("f", "etc/{{.serviceName}}.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)

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

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
