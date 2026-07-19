package gormplus

import (
	"context"

	"github.com/kuangshp/gorm-plus/plugin"
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
