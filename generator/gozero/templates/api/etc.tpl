Name: {{.serviceName}}
Host: {{.host}}
Port: {{.port}}

# 日志级别和格式；LogRotation.Enabled=true 时统一由下方文件日志管理器输出。
Log:
  ServiceName: {{.serviceName}} # 日志服务名；未配置时沿用顶层 Name。
  Mode: console # 关闭 LogRotation 后生效，可选 console、file、volume。
  Path: ./logs/{{.serviceName}} # 关闭 LogRotation 且 Mode 为 file/volume 时的存储路径。
  # 日志内容格式，同时应用于 logx、slog 和标准库 log。
  # json：每行一个 JSON 对象，便于日志平台采集和检索。
  # plain：可读文本，便于直接查看。
  # 两种格式均写入 .log 文件，归档后为 .log.gz。
  Encoding: json # 可选 json、plain，默认 json。
  Level: info # 可选 debug、info、error、severe。
  Stat: true # 是否记录 go-zero 统计日志。
  MaxContentLength: 0 # logx 单条日志内容最大字节数；0 不限制，超出后截断并标记 truncated。

# 文件日志管理，同时接管 logx、slog 和标准库 log。
LogRotation:
  Enabled: true
  Path: ./logs/{{.serviceName}} # 自动创建；相对路径以进程工作目录为基准，每个进程使用独立目录。
  Rotation: daily # daily 按天、hourly 按小时；使用进程本地时区。
  CompressAfterDays: 3 # 日志所属时段结束超过 3 天后压缩为 .gz；0 禁用压缩。
  KeepDays: 30 # 日志所属时段结束超过 30 天后自动删除（包括 .gz）；0 永久保留。
  CheckIntervalMinutes: 60 # 启动时及每隔指定分钟检查压缩、删除；删除优先于压缩。

# 统一响应配置。
ResponseConfig:
  Enabled: true
  # 跳过统一 code/message/result 包装的路径，例如健康检查、下载接口。
  # 成功数据原样返回，无返回值接口保持空响应；失败返回原始错误文本，默认 HTTP 400。
  # 精确匹配实际 URL 路径（包含路由前缀，不含查询参数），不支持前缀或通配符匹配。
  # 例如 ["/health", "/api/v1/files/download"]；[] 表示不跳过任何路径，仅 Enabled=true 时生效。
  # 此配置只控制响应格式，认证、参数校验和 CORS 仍按原配置执行。
  BypassPaths: []

# 跨域配置，按项目需要调整来源和头部。
CorsConfig:
  Enabled: true
  Origins:
    - "*" # 可改为项目实际来源，例如 https://admin.example.com。
  AllowHeaders: [] # 额外允许的请求头，例如 ["X-Client-ID"]。
  # 允许前端 JavaScript 在跨域请求中读取的额外响应头；[] 表示不额外暴露。
  # 例如导出 Excel：后端设置 Content-Disposition: attachment; filename="report.xlsx"。
  # 前端需要读取下载文件名时，配置为 ["Content-Disposition"]，再调用：
  # const response = await fetch(downloadUrl);
  # const disposition = response.headers.get("Content-Disposition");
  # disposition 是完整头部值，需再提取 filename / filename*；此配置不会自动设置文件名。
  ExposeHeaders: []

# 本机公开静态文件配置，独立于 CORS；上传接口仍由业务项目实现。
StaticConfig:
  Enabled: false # 按需开启；开启后文件可通过公开 URL 访问。
  Prefix: /uploads # URL 路径前缀：/uploads/a.png。
  Directory: ./uploads # 已存在的本机目录，相对路径以进程工作目录为基准。
  # 例如将图片保存到 ./uploads/a.png 后，可访问 http://服务地址/uploads/a.png。
  # 仅提供普通文件，不列出目录；目录外的符号链接目标及隐藏路径不能读取。
