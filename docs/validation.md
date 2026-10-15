# 结构体校验与 Gin 接入

校验能力统一由 `github.com/kuangshp/gorm-plus/validation` 提供，不依赖 go-utils、Gin 或 go-zero。业务项目只保留项目专用规则，通过启动时配置复用公共校验器。

## 普通 Go / go-zero

```go
import "github.com/kuangshp/gorm-plus/validation"

type Request struct {
	Mobile    string `json:"mobile" validate:"required,mobile"`
	StartTime string `json:"startTime" validate:"required,dateTime"`
}

// 普通 Go 调用；err 已包含首个字段的中文提示。
err := validation.Validate(&Request{})
```

go-zero 内嵌模板自动使用新包，在 `httpx.Parse` 后调用 `HasRules` / `Validate`，错误交给 `httpx.ErrorCtx`。已有 Handler 不会被 goctl 自动覆盖，需要手动将旧导入路径 `github.com/kuangshp/go-utils/k/validation` 替换为新路径；本地自定义 `--home` 也要同步模板。

| 入口 | 用途 |
| --- | --- |
| `Validate(value)` | 使用 `validate` 标签进行校验，返回首个中文错误 |
| `HasRules(value)` | 判断结构体及未忽略的嵌套结构体是否包含 `validate` 规则 |
| `HasRulesForTag(value, "binding")` | 检查指定标签；不执行校验 |
| `New(options...)` | 创建独立校验器，使用 `validate` 标签 |
| `Configure(engine, options...)` | 配置已有引擎，保留它原有的校验标签 |
| `instance.FirstMessage(err)` / `Translate(err)` | 翻译该实例的校验错误，不再次校验数据 |
| `WithRules(rules...)` | 注册项目规则；同名规则由最后一次配置覆盖函数和提示 |
| `WithFieldTagName("label")` | 从 `label:"手机号"` 等标签取提示中的字段名，默认取 `json` |

字段按结构体顺序取第一个校验错误；`*validation.Error` 包含 `Field`、`Tag`、`Message`，通过 `Unwrap` 保留原始错误。JSON 格式、字段类型不匹配等非字段校验错误原样返回，不会伪装成中文规则提示。

### 默认规则

除了 validator 自带的 `required`、`min`、`max`、`gte`、`email` 等规则，还提供：

| 标签 | 说明 |
| --- | --- |
| `mobile` / `checkMobile` | 中国大陆手机号码，沿用原 go-utils 规则 |
| `checkEmail` / `myEmail` | 邮箱地址，不接受显示名或空白字符 |
| `date` | 合法日期，格式 `2006-01-02` |
| `dateTime` | 合法日期时间，格式 `2006-01-02 15:04:05` |
| `afterDate` | 晚于本地时区的今天，不包含今天 |
| `decimal` | 沿用旧包的非负小数字符串规则，至多两位小数 |

规则与 JSON 类型转换是两步：`validation` 不把 `"1"` 转成整数；整数、小数兼容使用 `decimaljson`。必填字段仍需写 `required`，可选字段可写 `omitempty,dateTime`。需要更高金额精度时，应覆盖 `decimal` 规则，或选用项目自己的标签。

## Gin：统一初始化，保留现有 Handler

下面参考 gof_cos_ser 的 `utils/validator.go`、`initialize/validate.go` 和 `ShouldBindJSON → utils.ShowErrorMessage → utils.Fail` 调用链。示例 module 为 `flycos`；其他项目替换自己的 module 导入路径。

Gin 已在 `ShouldBindJSON` / `ShouldBindQuery` 中按 `binding` 标签执行校验。启动时复用 `binding.Validator.Engine()`，错误发生后只翻译，Handler 无需再次调用 `validation.Validate`，也不需要替换 Gin 的 `binding.Validator`。

### 1. 替换 utils/validator.go

统一保存配置后的实例，删除原文件的 `init()`、`InitTrans`、全局 `Trans` 和手写翻译注册代码。不要把本示例叠加到旧初始化逻辑旁边。移除 `RemoveTopStruct` 前确认其他位置没有使用它。

<!-- gin-example: utils/validator.go -->
```go
package utils

import (
	"errors"
	"sync"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/kuangshp/gorm-plus/validation"
)

var (
	requestValidator   *validation.Validator
	initValidationOnce sync.Once
	initValidationErr  error
)

// InitValidation 在启动阶段调用一次，全部项目规则必须在第一次调用时传入。
// 重复调用不会重新注册，也不会追加规则；初始化失败应终止启动。
func InitValidation(rules ...validation.Rule) error {
	initValidationOnce.Do(func() {
		if binding.Validator == nil {
			initValidationErr = errors.New("Gin 参数校验器已禁用")
			return
		}
		engine, ok := binding.Validator.Engine().(*validator.Validate)
		if !ok {
			initValidationErr = errors.New("不支持的 Gin 参数校验引擎")
			return
		}
		// Configure 保留 Gin 的 binding 标签，注册公共规则和中文翻译。
		requestValidator, initValidationErr = validation.Configure(
			engine, validation.WithRules(rules...),
		)
	})
	return initValidationErr
}

// ShowErrorMessage 保留旧调用入口；这里只翻译，不再次校验请求。
func ShowErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	if requestValidator == nil {
		return err.Error()
	}
	// 必须使用配置 Gin 引擎时返回的实例，才能识别项目规则的中文提示。
	return requestValidator.FirstMessage(err)
}
```

这里的 `sync.Once` 是业务项目的启动保护。库的 `Configure` 允许不同引擎分别配置；所有规则应在首次参数校验和启动 HTTP 服务前完成注册，不要在请求期间调用注册方法。

### 2. 替换 initialize/validate.go

简单项目直接返回 `utils.InitValidation()`，公共规则不必再定义。

对于已有的 gof_cos_ser，保留原 `validators` 函数，通过规则配置集中注册。下面同时演示覆盖公共规则和追加项目规则，不再手写 `RegisterValidation` / `RegisterTranslation`：

<!-- gin-example: initialize/validate.go -->
```go
package initialize

import (
	"flycos/utils"
	"flycos/validators"

	"github.com/kuangshp/gorm-plus/validation"
)

func InitValidate() error {
	return utils.InitValidation(
		// 保留老项目的校验函数和文案；同名规则会覆盖公共默认规则。
		validation.Rule{Tag: "mobile", Message: "{0} 非法的手机号码!", Validate: validators.ValidateMobile},
		validation.Rule{Tag: "myEmail", Message: "{0} 邮箱号码非法!", Validate: validators.ValidateEmail},
		validation.Rule{Tag: "date", Message: "{0} 时间格式非法必须为2006-01-02格式!", Validate: validators.ValidateDate},
		validation.Rule{Tag: "dateTime", Message: "{0} 时间格式非法必须为2006-01-02 15:04:05格式!", Validate: validators.ValidateDateTime},
		// 以下是项目专用规则，公共库不包含这些业务实现或依赖。
		validation.Rule{Tag: "checkName", Message: "{0}用户名长度必须是3-12位字符", Validate: validators.ValidateName},
		validation.Rule{Tag: "chineseId", Message: "{0}身份证号码格式错误", Validate: validators.ValidateId},
		validation.Rule{Tag: "cronspec", Message: "{0}定时规则格式非法，需为6段cron表达式(秒 分 时 日 月 周)", Validate: validators.ValidateCronSpec},
	)
}
```

这里保留老项目的手机号、邮箱、日期函数，是为了保留既有校验边界和提示。确认使用公共默认行为后，删除相应配置条目和不再引用的项目函数即可。`myEmail` 默认等同 `checkEmail`；项目原正则不一定与之完全相同。身份证、用户名、cron 等项目规则继续放在项目 `validators/` 中，库里不再复制一份。

### 3. 启动时处理初始化错误

将原来的 `initialize.InitValidate()` 调用改为以下代码，放在启动 HTTP 服务之前；不再调用旧 `InitTrans` 或另一套规则注册入口：

```go
if err := initialize.InitValidate(); err != nil {
	log.Fatalf("初始化参数校验失败: %v", err)
}
```

`log` 为标准库 `log`；已有日志系统也可以按项目惯例记录错误并终止启动。不要忽略注册失败后继续启动。

### 4. DTO 和 Handler 沿用 binding

```go
type CreateUserReq struct {
	Mobile    string `json:"mobile" binding:"required,mobile"`
	StartTime string `json:"startTime" binding:"required,dateTime"`
	Name      string `json:"name" binding:"required,checkName"`
	Cron      string `json:"cron" binding:"omitempty,cronspec"`
}
```

原控制器方法中的调用保持不变：

```go
var req CreateUserReq
if err := ctx.ShouldBindJSON(&req); err != nil {
	utils.Fail(ctx, utils.ShowErrorMessage(err))
	return
}
// 继续处理业务；ShouldBindJSON 已完成校验，不再调用 validation.Validate(&req)。
```

这样可以保留项目现有 `code/message/result` 响应格式。`FirstMessage(nil)` 返回空字符串，字段错误使用 JSON 字段名和中文提示，JSON 语法错误仍按原始解析错误处理。自定义规则的错误必须由 `requestValidator.FirstMessage` 翻译，不能换用包级 `validation.FirstMessage`，后者属于另一个默认引擎。

## 从 go-utils 迁移

1. 业务项目使用包含本包的 gorm-plus 版本；未发布时可临时将模块 `replace` 指向本地源码验证。
2. 把所有 `github.com/kuangshp/go-utils/k/validation` 导入改为 `github.com/kuangshp/gorm-plus/validation`，同步自定义 goctl 模板。
3. Gin 项目按上文合并旧翻译器、规则初始化和错误翻译入口，保留 `binding` 标签。已有其他校验注册也要合并或确认不与公共规则重名，避免后续注册再次覆盖。
4. 运行 `go mod tidy`。如果还使用 go-utils 的其他工具，继续保留它；本校验包不依赖 go-utils，也不会通过反向依赖把它引回来。

本次迁移删除 go-utils 的 `k/validation`，不保留兼容转调包。仍使用旧导入路径的项目在升级到删除该包的版本时会编译失败，需先替换导入并升级 gorm-plus；已发布的历史版本不受工作区删除影响。

验证公共规则、真实 Gin 绑定和 goctl 生成服务：

```bash
go test ./validation ./generator/gozero
GORMPLUS_GIN_INTEGRATION=1 go test ./validation -run TestGinIntegration -count=1
GORMPLUS_GOCTL_INTEGRATION=1 go test ./generator/gozero -count=1
```

Gin 测试在临时模块中编译以上示例，gorm-plus 本身不新增 Gin 依赖；goctl 集成测试需要本机已安装 `goctl`。
