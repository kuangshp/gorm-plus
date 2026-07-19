package gormplus

import (
	"context"

	"github.com/kuangshp/gorm-plus/plugin"
	"gorm.io/gen"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

type SensitiveReturnMode = plugin.SensitiveReturnMode
type SensitiveMaskConfig = plugin.SensitiveMaskConfig
type SensitiveFieldConfig = plugin.SensitiveFieldConfig
type SensitiveConfig = plugin.SensitiveConfig
type SensitivePlugin = plugin.SensitivePlugin

const (
	SensitiveReturnMasked = plugin.SensitiveReturnMasked
	SensitiveReturnPlain  = plugin.SensitiveReturnPlain
	SensitiveReturnCipher = plugin.SensitiveReturnCipher
)

func NewSensitivePlugin(cfg SensitiveConfig) (*SensitivePlugin, error) {
	return plugin.NewSensitivePlugin(cfg)
}

func RegisterSensitive(db *gorm.DB, cfg SensitiveConfig) (*SensitivePlugin, error) {
	return plugin.RegisterSensitive(db, cfg)
}

func PhoneField(name string) SensitiveFieldConfig { return plugin.PhoneField(name) }

func WithSensitivePlaintext(ctx context.Context) context.Context {
	return plugin.WithSensitivePlaintext(ctx)
}

func WithSensitiveCiphertext(ctx context.Context) context.Context {
	return plugin.WithSensitiveCiphertext(ctx)
}

func WithSensitiveMasked(ctx context.Context) context.Context {
	return plugin.WithSensitiveMasked(ctx)
}

// SensitiveEq 为 gorm-gen 构造敏感字段等值条件。
// column 传对应的索引字段，例如 dao.SysUserEntity.PhoneIndex。
func SensitiveEq(sensitive *SensitivePlugin, logicalField, value string, column field.String) gen.Condition {
	return column.Eq(sensitive.IndexValue(logicalField, value))
}

// SensitivePhoneEq 为 gorm-gen 构造手机号等值条件。
// 推荐直接使用 sensitive.PhoneEq(column, phone)，此函数用于不便调用实例方法的场景。
func SensitivePhoneEq(sensitive *SensitivePlugin, column field.String, phone string) gen.Condition {
	return sensitive.PhoneEq(column, phone)
}
