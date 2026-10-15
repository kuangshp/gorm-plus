// Package validation 基于 go-playground/validator 提供结构体校验和中文错误翻译，
// 不依赖 Gin、go-zero 或 go-utils。
package validation

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/locales/zh"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	zhTranslations "github.com/go-playground/validator/v10/translations/zh"
)

// Rule 定义校验规则及中文提示；Message 中的 {0} 表示字段名。
type Rule struct {
	Tag      string
	Message  string
	Validate validator.Func
}

// Option 配置校验器。
type Option func(*config)

type config struct {
	fieldTagName string
	rules        []Rule
}

// WithFieldTagName 指定错误提示使用的字段名标签，默认为 json。
// 例如传入 label 后可使用 label:"手机号"；它不改变 validate/binding 校验标签。
func WithFieldTagName(tagName string) Option {
	return func(cfg *config) {
		cfg.fieldTagName = tagName
	}
}

// WithRules 注册项目规则；同名规则以最后一次配置为准，同时覆盖校验函数和提示。
// 请在启动时统一配置，不要在处理请求时重新注册规则。
func WithRules(rules ...Rule) Option {
	return func(cfg *config) {
		cfg.rules = append(cfg.rules, rules...)
	}
}

// Validator 持有校验引擎和中文翻译器，可复用框架已有的引擎。
type Validator struct {
	engine     *validator.Validate
	translator ut.Translator
}

// Error 表示按结构体字段顺序取得的首个校验错误，并保留原始错误链。
type Error struct {
	Field   string
	Tag     string
	Message string
	Cause   error
}

// Error 返回中文提示。
func (e *Error) Error() string {
	return e.Message
}

// Unwrap 返回原始错误，支持 errors.As/Is。
func (e *Error) Unwrap() error {
	return e.Cause
}

// New 创建独立校验器，使用 validate 标签。
func New(options ...Option) (*Validator, error) {
	return Configure(validator.New(), options...)
}

// Configure 在已有引擎上注册通用规则、字段名和中文翻译。
// 保留引擎原有的校验标签，例如 Gin 的 binding；应在首次校验和启动服务前调用一次。
// 返回的实例必须保留，用于翻译该引擎的自定义规则错误。
func Configure(engine *validator.Validate, options ...Option) (*Validator, error) {
	if engine == nil {
		return nil, errors.New("validator engine must not be nil")
	}

	cfg := config{
		fieldTagName: "json",
		rules:        defaultRules(),
	}
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}
	rules, err := mergeRules(cfg.rules)
	if err != nil {
		return nil, err
	}

	registerFieldName(engine, cfg.fieldTagName)
	chineseLocale := zh.New()
	translator, found := ut.New(chineseLocale, chineseLocale).GetTranslator("zh")
	if !found {
		return nil, errors.New("Chinese validator translator is unavailable")
	}
	if err := zhTranslations.RegisterDefaultTranslations(engine, translator); err != nil {
		return nil, fmt.Errorf("register default Chinese translations: %w", err)
	}
	for _, rule := range rules {
		if err := registerRule(engine, translator, rule); err != nil {
			return nil, err
		}
	}

	return &Validator{
		engine:     engine,
		translator: translator,
	}, nil
}

// Engine 返回底层引擎，便于启动阶段注册结构体级校验等扩展。
func (v *Validator) Engine() *validator.Validate {
	return v.engine
}

// Validate 校验结构体并返回首个中文错误。
// Gin 的 ShouldBindJSON 已执行校验，应使用 FirstMessage/Translate 翻译其错误。
func (v *Validator) Validate(value any) error {
	if v == nil || v.engine == nil {
		return errors.New("validator is not initialized")
	}
	return v.Translate(v.engine.Struct(value))
}

// Translate 将字段校验错误（包括通过 %w 包装的错误）转换为首个中文错误。
// JSON 格式、类型不匹配等非字段校验错误原样返回。
func (v *Validator) Translate(err error) error {
	if err == nil {
		return nil
	}
	if v == nil || v.translator == nil {
		return err
	}
	var translated *Error
	if errors.As(err, &translated) {
		return err
	}

	var validationErrors validator.ValidationErrors
	if !errors.As(err, &validationErrors) || len(validationErrors) == 0 {
		return err
	}
	fieldError := validationErrors[0]
	return &Error{
		Field:   fieldError.Field(),
		Tag:     fieldError.Tag(),
		Message: fieldError.Translate(v.translator),
		Cause:   err,
	}
}

// FirstMessage 返回首个中文提示；err 为 nil 时返回空字符串。
func (v *Validator) FirstMessage(err error) string {
	translatedErr := v.Translate(err)
	if translatedErr == nil {
		return ""
	}
	return translatedErr.Error()
}

var (
	defaultOnce      sync.Once
	defaultValidator *Validator
	defaultErr       error
)

// Default 返回延迟初始化的默认校验器，使用 validate 标签。
// 项目专属规则请通过 New/Configure 创建实例，不要在并发请求中修改默认引擎。
func Default() (*Validator, error) {
	defaultOnce.Do(func() {
		defaultValidator, defaultErr = New()
	})
	return defaultValidator, defaultErr
}

// Validate 使用默认校验器校验结构体，适用于 go-zero Handler 或普通 Go 调用。
func Validate(value any) error {
	defaultInstance, err := Default()
	if err != nil {
		return err
	}
	return defaultInstance.Validate(value)
}

// FirstMessage 使用默认校验器翻译错误；自定义规则应使用对应实例的 FirstMessage。
func FirstMessage(err error) string {
	defaultInstance, initErr := Default()
	if initErr != nil {
		return initErr.Error()
	}
	return defaultInstance.FirstMessage(err)
}

func registerFieldName(engine *validator.Validate, tagName string) {
	engine.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.SplitN(field.Tag.Get(tagName), ",", 2)[0]
		if name == "-" {
			return ""
		}
		if name == "" {
			return field.Name
		}
		return name
	})
}

func registerRule(engine *validator.Validate, translator ut.Translator, rule Rule) error {
	if strings.TrimSpace(rule.Tag) == "" {
		return errors.New("validation rule tag must not be empty")
	}
	if rule.Validate == nil {
		return fmt.Errorf("validation rule %q has no validator function", rule.Tag)
	}
	if err := engine.RegisterValidation(rule.Tag, rule.Validate); err != nil {
		return fmt.Errorf("register validation rule %q: %w", rule.Tag, err)
	}
	if err := engine.RegisterTranslation(
		rule.Tag,
		translator,
		func(translator ut.Translator) error {
			// 允许替换标准规则的中文提示；同名项目规则已在注册前合并。
			return translator.Add(rule.Tag, rule.Message, true)
		},
		func(translator ut.Translator, fieldError validator.FieldError) string {
			message, err := translator.T(fieldError.Tag(), fieldError.Field())
			if err != nil {
				return fieldError.Error()
			}
			return message
		},
	); err != nil {
		return fmt.Errorf("register translation for rule %q: %w", rule.Tag, err)
	}
	return nil
}

// mergeRules 先合并同名规则再注册，避免重复添加翻译导致初始化失败。
func mergeRules(rules []Rule) ([]Rule, error) {
	merged := make([]Rule, 0, len(rules))
	positions := make(map[string]int, len(rules))
	for _, rule := range rules {
		rule.Tag = strings.TrimSpace(rule.Tag)
		if rule.Tag == "" {
			return nil, errors.New("validation rule tag must not be empty")
		}
		if rule.Validate == nil {
			return nil, fmt.Errorf("validation rule %q has no validator function", rule.Tag)
		}
		if index, ok := positions[rule.Tag]; ok {
			merged[index] = rule
		} else {
			positions[rule.Tag] = len(merged)
			merged = append(merged, rule)
		}
	}
	return merged, nil
}
