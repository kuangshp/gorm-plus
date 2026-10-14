package config

import (
    {{.authImport}}
    "github.com/kuangshp/gorm-plus/httpcors"
    "github.com/kuangshp/gorm-plus/httpresponse"
    "github.com/kuangshp/gorm-plus/httpstatic"
    "github.com/kuangshp/gorm-plus/logrotate"
)

type Config struct {
	rest.RestConf
	{{.auth}}
	{{.jwtTrans}}
    LogRotation logrotate.Config // 按天/小时写日志、延迟压缩及过期清理。
    ResponseConfig httpresponse.Config // 统一响应及跳过包装的路径。
    CorsConfig httpcors.Config // 跨域来源、额外请求头和暴露的响应头。
    StaticConfig httpstatic.Config // 可选的本机公开文件目录与 URL 映射。
}
