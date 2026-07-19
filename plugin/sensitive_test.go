package plugin

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type sensitiveUser struct {
	ID          uint
	Phone       string `gorm:"-"`
	PhoneCipher string `gorm:"column:phone_cipher"`
	PhoneIndex  string `gorm:"column:phone_index;uniqueIndex"`
}

type generatedSensitiveUser struct {
	ID          uint
	Phone       string `gorm:"-" json:"phone" gormplus:"type:phone;cipher:phone_cipher;index:phone_index"`
	PhoneCipher string `gorm:"column:phone_cipher"`
	PhoneIndex  string `gorm:"column:phone_index;uniqueIndex"`
}

func TestSensitivePluginEncryptQueryAndReturnModes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&sensitiveUser{}); err != nil {
		t.Fatal(err)
	}
	showPlain := false
	p, err := NewSensitivePlugin(SensitiveConfig{
		EncryptionKey: []byte("0123456789abcdef0123456789abcdef"),
		IndexKey:      []byte("index-key-must-be-kept-separate-32"),
		Fields: []SensitiveFieldConfig{{
			PlainField:  "Phone",
			CipherField: "PhoneCipher",
			IndexField:  "phone_index",
			Normalize: func(value string) string {
				return strings.ReplaceAll(strings.TrimSpace(value), " ", "")
			},
			ReturnModeResolver: func(context.Context) SensitiveReturnMode {
				if showPlain {
					return SensitiveReturnPlain
				}
				return SensitiveReturnMasked
			},
			Mask: SensitiveMaskConfig{Prefix: 3, Suffix: 4, Replacement: "*"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Use(p); err != nil {
		t.Fatal(err)
	}

	created := sensitiveUser{Phone: "138 0013 8000"}
	if err = db.Create(&created).Error; err != nil {
		t.Fatal(err)
	}
	var raw sensitiveUser
	if err = db.Session(&gorm.Session{SkipHooks: true}).First(&raw, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if raw.PhoneCipher == "13800138000" || raw.PhoneCipher == "" || raw.PhoneIndex == "" {
		t.Fatalf("数据库字段未正确加密或生成索引: %#v", raw)
	}

	query, err := p.WhereSensitiveEqual(db, "phone_index", "13800138000")
	if err != nil {
		t.Fatal(err)
	}
	var masked sensitiveUser
	if err = query.First(&masked).Error; err != nil {
		t.Fatal(err)
	}
	if masked.Phone != "138****8000" {
		t.Fatalf("masked phone = %q", masked.Phone)
	}

	showPlain = true
	var plain sensitiveUser
	if err = db.First(&plain, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if plain.Phone != "13800138000" {
		t.Fatalf("plain phone = %q", plain.Phone)
	}
}

func TestSensitivePluginRejectsInvalidKeys(t *testing.T) {
	if _, err := NewSensitivePlugin(SensitiveConfig{
		EncryptionKey: []byte("short"),
		IndexKey:      []byte("also-short"),
		Fields:        []SensitiveFieldConfig{{PlainField: "Phone", CipherField: "Cipher", IndexField: "Index"}},
	}); err == nil {
		t.Fatal("invalid encryption key should fail")
	}
}

func TestSensitivePluginSimpleAPI(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&sensitiveUser{}); err != nil {
		t.Fatal(err)
	}
	p, err := RegisterSensitive(db, SensitiveConfig{
		Key:    []byte("one-master-key-for-sensitive-data"),
		Fields: []SensitiveFieldConfig{PhoneField("Phone")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&sensitiveUser{Phone: "+86 138-0013-8000"}).Error; err != nil {
		t.Fatal(err)
	}

	var masked sensitiveUser
	if err = p.WhereEqual(db, "Phone", "13800138000").First(&masked).Error; err != nil {
		t.Fatal(err)
	}
	if masked.Phone != "138****8000" {
		t.Fatalf("masked phone = %q", masked.Phone)
	}

	var plain sensitiveUser
	ctx := WithSensitivePlaintext(context.Background())
	if err = p.WhereEqual(db.WithContext(ctx), "Phone", "13800138000").First(&plain).Error; err != nil {
		t.Fatal(err)
	}
	if plain.Phone != "13800138000" {
		t.Fatalf("plain phone = %q", plain.Phone)
	}

	// 更新时使用仅携带主键和新明文的独立对象，避免把查询得到的脱敏值重新保存。
	update := sensitiveUser{ID: plain.ID, Phone: "13900139000"}
	if err = db.Model(&update).Select("PhoneCipher", "PhoneIndex").Updates(&update).Error; err != nil {
		t.Fatal(err)
	}
	var updated sensitiveUser
	if err = p.WhereEqual(db.WithContext(ctx), "Phone", "13900139000").First(&updated).Error; err != nil {
		t.Fatal(err)
	}
	if updated.Phone != "13900139000" {
		t.Fatalf("updated phone = %q", updated.Phone)
	}
	if err = p.WhereEqual(db, "Phone", "13800138000").First(&sensitiveUser{}).Error; err != gorm.ErrRecordNotFound {
		t.Fatalf("old phone query error = %v, want record not found", err)
	}
}

func TestSensitivePluginReadsGeneratedTags(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&generatedSensitiveUser{}); err != nil {
		t.Fatal(err)
	}
	p, err := RegisterSensitive(db, SensitiveConfig{Key: []byte("one-master-key-for-sensitive-data")})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&generatedSensitiveUser{Phone: "13800138000"}).Error; err != nil {
		t.Fatal(err)
	}
	var user generatedSensitiveUser
	if err = p.WhereEqual(db, "Phone", "13800138000").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if user.Phone != "138****8000" {
		t.Fatalf("generated tag phone = %q", user.Phone)
	}
}
