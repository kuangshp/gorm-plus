package plugin

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SensitiveReturnMode 控制敏感字段查询后的展示形式。
type SensitiveReturnMode uint8

const (
	SensitiveReturnMasked SensitiveReturnMode = iota
	SensitiveReturnPlain
	SensitiveReturnCipher
)

// SensitiveMaskConfig 配置脱敏显示，例如手机号保留前三后四得到 138****8000。
type SensitiveMaskConfig struct {
	Prefix      int
	Suffix      int
	Replacement string
}

// SensitiveFieldConfig 描述一个敏感字段。
// PlainField 是业务结构体字段，建议标记 gorm:"-"；CipherField 和 IndexField 是数据库字段。
type SensitiveFieldConfig struct {
	PlainField  string
	CipherField string
	IndexField  string
	// IndexColumn 是查询使用的数据库列名；为空时根据 IndexField 自动转为 snake_case。
	IndexColumn string
	Normalize   func(string) string
	ReturnMode  SensitiveReturnMode
	// ReturnModeResolver 可按当前请求权限动态决定返回明文、密文或脱敏值，优先于 ReturnMode。
	ReturnModeResolver func(context.Context) SensitiveReturnMode
	Mask               SensitiveMaskConfig
}

// SensitiveConfig 配置字段加密插件。常规场景只需配置 Key 和 Fields。
type SensitiveConfig struct {
	// Key 是主密钥，插件会自动派生相互独立的加密密钥和查询索引密钥。
	Key []byte
	// EncryptionKey、IndexKey 是高级配置；仅在不使用 Key 时需要分别提供。
	EncryptionKey []byte
	IndexKey      []byte
	Fields        []SensitiveFieldConfig
}

type SensitivePlugin struct {
	cfg   SensitiveConfig
	aead  cipher.AEAD
	byKey map[string]SensitiveFieldConfig
}

type sensitiveReturnModeContextKey struct{}

// WithSensitivePlaintext 允许当前查询将敏感字段解密为明文。
func WithSensitivePlaintext(ctx context.Context) context.Context {
	return context.WithValue(ctx, sensitiveReturnModeContextKey{}, SensitiveReturnPlain)
}

// WithSensitiveCiphertext 让当前查询返回数据库密文。
func WithSensitiveCiphertext(ctx context.Context) context.Context {
	return context.WithValue(ctx, sensitiveReturnModeContextKey{}, SensitiveReturnCipher)
}

// WithSensitiveMasked 让当前查询返回脱敏值（默认行为）。
func WithSensitiveMasked(ctx context.Context) context.Context {
	return context.WithValue(ctx, sensitiveReturnModeContextKey{}, SensitiveReturnMasked)
}

// PhoneField 返回手机号字段的默认配置。
// name 为业务字段名，例如 Phone；默认使用 PhoneCipher、PhoneIndex 和 phone_index。
func PhoneField(name string) SensitiveFieldConfig {
	return SensitiveFieldConfig{
		PlainField:  name,
		CipherField: name + "Cipher",
		IndexField:  name + "Index",
		IndexColumn: toSnakeCase(name) + "_index",
		Normalize:   normalizePhone,
		ReturnMode:  SensitiveReturnMasked,
		Mask:        SensitiveMaskConfig{Prefix: 3, Suffix: 4, Replacement: "*"},
	}
}

func NewSensitivePlugin(cfg SensitiveConfig) (*SensitivePlugin, error) {
	if len(cfg.Key) > 0 {
		if len(cfg.Key) < 16 {
			return nil, errors.New("敏感字段 Key 不能少于 16 字节")
		}
		cfg.EncryptionKey = deriveSensitiveKey(cfg.Key, "gorm-plus/encryption")
		cfg.IndexKey = deriveSensitiveKey(cfg.Key, "gorm-plus/blind-index")
	}
	block, err := aes.NewCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("敏感字段 EncryptionKey 无效: %w", err)
	}
	if len(cfg.IndexKey) < 16 {
		return nil, errors.New("敏感字段 IndexKey 不能少于 16 字节")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	p := &SensitivePlugin{cfg: cfg, aead: aead, byKey: make(map[string]SensitiveFieldConfig, len(cfg.Fields))}
	for i := range p.cfg.Fields {
		field := &p.cfg.Fields[i]
		if field.PlainField == "" || field.CipherField == "" || field.IndexField == "" {
			return nil, errors.New("PlainField、CipherField 和 IndexField 不能为空")
		}
		if field.Normalize == nil {
			field.Normalize = strings.TrimSpace
		}
		if field.Mask.Replacement == "" {
			field.Mask.Replacement = "*"
		}
		if field.Mask.Prefix == 0 && field.Mask.Suffix == 0 {
			field.Mask.Prefix, field.Mask.Suffix = 3, 4
		}
		if field.IndexColumn == "" {
			field.IndexColumn = toSnakeCase(field.IndexField)
		}
		p.byKey[field.PlainField] = *field
		p.byKey[field.IndexField] = *field
		p.byKey[field.IndexColumn] = *field
	}
	return p, nil
}

func RegisterSensitive(db *gorm.DB, cfg SensitiveConfig) (*SensitivePlugin, error) {
	p, err := NewSensitivePlugin(cfg)
	if err != nil {
		return nil, err
	}
	if err = db.Use(p); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *SensitivePlugin) Name() string { return "gorm_plus:sensitive" }

func (p *SensitivePlugin) Initialize(db *gorm.DB) error {
	if err := db.Callback().Create().Before("gorm:create").Register(p.Name()+":create", p.beforeWrite); err != nil {
		return err
	}
	if err := db.Callback().Update().Before("gorm:update").Register(p.Name()+":update", p.beforeWrite); err != nil {
		return err
	}
	return db.Callback().Query().After("gorm:query").Register(p.Name()+":query", p.afterQuery)
}

// BlindIndex 返回规范化值的 HMAC-SHA256 查询索引。
func (p *SensitivePlugin) BlindIndex(indexField, value string) (string, error) {
	field, ok := p.byKey[indexField]
	if !ok {
		return "", fmt.Errorf("未配置敏感索引字段 %q", indexField)
	}
	return blindIndex(p.cfg.IndexKey, field.Normalize(value)), nil
}

// WhereSensitiveEqual 为敏感字段添加等值查询条件。
func (p *SensitivePlugin) WhereSensitiveEqual(db *gorm.DB, indexField, value string) (*gorm.DB, error) {
	index, err := p.BlindIndex(indexField, value)
	if err != nil {
		return db, err
	}
	field := p.byKey[indexField]
	return db.Where(clause.Eq{Column: clause.Column{Name: field.IndexColumn}, Value: index}), nil
}

// WhereEqual 是常用的敏感字段等值查询，field 使用业务字段名，例如 Phone。
// 配置错误会写入返回的 *gorm.DB.Error，可直接继续链式调用。
func (p *SensitivePlugin) WhereEqual(db *gorm.DB, field, value string) *gorm.DB {
	if _, ok := p.byKey[field]; !ok {
		defaultField := PhoneField(field)
		index := blindIndex(p.cfg.IndexKey, defaultField.Normalize(value))
		return db.Where(clause.Eq{Column: clause.Column{Name: defaultField.IndexColumn}, Value: index})
	}
	query, err := p.WhereSensitiveEqual(db, field, value)
	if err != nil {
		db.AddError(err)
		return db
	}
	return query
}

func (p *SensitivePlugin) beforeWrite(db *gorm.DB) {
	if db.Error != nil || db.Statement == nil || !db.Statement.ReflectValue.IsValid() {
		return
	}
	if err := walkStructs(db.Statement.ReflectValue, func(value reflect.Value) error {
		for _, field := range p.fieldsForValue(value) {
			plain, ok := stringField(value, field.PlainField)
			if !ok || plain == "" {
				continue
			}
			plain = field.Normalize(plain)
			ciphertext, err := p.encrypt(plain)
			if err != nil {
				return err
			}
			if err := setStringField(value, field.CipherField, ciphertext); err != nil {
				return err
			}
			if err := setStringField(value, field.IndexField, blindIndex(p.cfg.IndexKey, plain)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		db.AddError(err)
	}
}

func (p *SensitivePlugin) afterQuery(db *gorm.DB) {
	if db.Error != nil || db.Statement == nil || !db.Statement.ReflectValue.IsValid() {
		return
	}
	ctx := db.Statement.Context
	if err := walkStructs(db.Statement.ReflectValue, func(value reflect.Value) error {
		for _, field := range p.fieldsForValue(value) {
			ciphertext, ok := stringField(value, field.CipherField)
			if !ok || ciphertext == "" {
				continue
			}
			mode := field.ReturnMode
			if field.ReturnModeResolver != nil {
				mode = field.ReturnModeResolver(ctx)
			}
			if contextMode, ok := ctx.Value(sensitiveReturnModeContextKey{}).(SensitiveReturnMode); ok {
				mode = contextMode
			}
			output := ciphertext
			if mode != SensitiveReturnCipher {
				plain, err := p.decrypt(ciphertext)
				if err != nil {
					return err
				}
				output = plain
				if mode == SensitiveReturnMasked {
					output = maskSensitive(plain, field.Mask)
				}
			}
			if err := setStringField(value, field.PlainField, output); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		db.AddError(err)
	}
}

// fieldsForValue 合并显式配置和生成器写入实体 tag 的敏感字段配置。
func (p *SensitivePlugin) fieldsForValue(value reflect.Value) []SensitiveFieldConfig {
	fields := append([]SensitiveFieldConfig(nil), p.cfg.Fields...)
	configured := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		configured[field.PlainField] = struct{}{}
	}
	typeOf := value.Type()
	for i := 0; i < typeOf.NumField(); i++ {
		structField := typeOf.Field(i)
		tag := strings.TrimSpace(structField.Tag.Get("gormplus"))
		if tag == "" {
			continue
		}
		if _, exists := configured[structField.Name]; exists {
			continue
		}
		options := make(map[string]string)
		for _, item := range strings.Split(tag, ";") {
			parts := strings.SplitN(item, ":", 2)
			if len(parts) == 2 {
				options[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
		cipherColumn, indexColumn := options["cipher"], options["index"]
		if cipherColumn == "" || indexColumn == "" {
			continue
		}
		field := PhoneField(structField.Name)
		field.CipherField = cipherColumn
		field.IndexField = indexColumn
		field.IndexColumn = indexColumn
		fields = append(fields, field)
	}
	return fields
}

func (p *SensitivePlugin) encrypt(plain string) (string, error) {
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := p.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (p *SensitivePlugin) decrypt(encoded string) (string, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(data) < p.aead.NonceSize() {
		return "", errors.New("敏感字段密文无效")
	}
	nonce := data[:p.aead.NonceSize()]
	plain, err := p.aead.Open(nil, nonce, data[p.aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("敏感字段解密失败")
	}
	return string(plain), nil
}

func blindIndex(key []byte, value string) string {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func deriveSensitiveKey(master []byte, purpose string) []byte {
	h := hmac.New(sha256.New, master)
	_, _ = h.Write([]byte(purpose))
	return h.Sum(nil)
}

func normalizePhone(value string) string {
	value = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(value))
	if strings.HasPrefix(value, "+86") {
		value = strings.TrimPrefix(value, "+86")
	}
	return value
}

func toSnakeCase(value string) string {
	var result strings.Builder
	for i, char := range value {
		if i > 0 && char >= 'A' && char <= 'Z' {
			result.WriteByte('_')
		}
		result.WriteRune(char)
	}
	return strings.ToLower(result.String())
}

func maskSensitive(value string, cfg SensitiveMaskConfig) string {
	runes := []rune(value)
	if cfg.Prefix+cfg.Suffix >= len(runes) {
		return strings.Repeat(cfg.Replacement, len(runes))
	}
	return string(runes[:cfg.Prefix]) + strings.Repeat(cfg.Replacement, len(runes)-cfg.Prefix-cfg.Suffix) + string(runes[len(runes)-cfg.Suffix:])
}

func walkStructs(value reflect.Value, fn func(reflect.Value) error) error {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Struct:
		return fn(value)
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := walkStructs(value.Index(i), fn); err != nil {
				return err
			}
		}
	}
	return nil
}

func stringField(value reflect.Value, name string) (string, bool) {
	field := configuredField(value, name)
	if !field.IsValid() || field.Kind() != reflect.String {
		return "", false
	}
	return field.String(), true
}

func setStringField(value reflect.Value, name, content string) error {
	field := configuredField(value, name)
	if !field.IsValid() || field.Kind() != reflect.String || !field.CanSet() {
		return fmt.Errorf("敏感字段 %s 不存在、不是 string 或不可写", name)
	}
	field.SetString(content)
	return nil
}

func configuredField(value reflect.Value, name string) reflect.Value {
	if field := value.FieldByName(name); field.IsValid() {
		return field
	}
	typeOf := value.Type()
	for i := 0; i < typeOf.NumField(); i++ {
		for _, option := range strings.Split(typeOf.Field(i).Tag.Get("gorm"), ";") {
			if strings.TrimPrefix(option, "column:") == name && strings.HasPrefix(option, "column:") {
				return value.Field(i)
			}
		}
	}
	return reflect.Value{}
}
