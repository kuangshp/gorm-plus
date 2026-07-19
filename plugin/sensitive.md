# 敏感字段插件使用文档

敏感字段插件使用 AES-GCM 随机密文保存手机号等敏感数据，并使用 HMAC-SHA256 盲索引支持精确查询。

本文以手机号为例，完整说明模型、注册、插入、查询、返回控制和安全更新方式。

## 一、数据库字段

可直接执行的 MySQL 示例文件：[sensitive_example.sql](sensitive_example.sql)。

推荐使用两个数据库字段：

```sql
phone_cipher VARCHAR(255) NOT NULL COMMENT '手机号密文',
phone_index  VARCHAR(64)  NOT NULL COMMENT '手机号查询索引',
UNIQUE KEY uk_phone_index (phone_index)
```

两个字段用途不同：

- `phone_cipher` 保存可以解密的 AES-GCM 随机密文。
- `phone_index` 保存不可逆的 HMAC 查询索引，用于手机号等值查询和唯一约束。

随机密文不能直接执行等值查询，因此需要额外的 `phone_index`。

## 二、模型定义

### 使用代码生成器（推荐）

在 `gormplus.Generate` 配置中声明表名和业务字段：

```go
cfg := &gormplus.GeneratorConfig{
	// 其他数据库和输出路径配置……
	SensitiveFields: []gormplus.GeneratorSensitiveField{{
		Table:       "user",
		Field:       "phone",
		Type:        "phone",
		CipherField: "phone_cipher",
		IndexField:  "phone_index",
		EncryptAtRest: false,
	}},
}

if err := gormplus.Generate(cfg); err != nil {
	return err
}
```

YAML 配置：

```yaml
sensitive_fields:
  - table: user
    field: phone
    type: phone
    cipher_field: phone_cipher
    index_field: phone_index
    encrypt_at_rest: false
```

生成器会在实体中增加业务字段：

```go
Phone string `gorm:"-" json:"phone" gormplus:"type:phone;cipher:phone_cipher;index:phone_index;encrypt:false"`
```

同时会把数据库存储字段自动设置为 `json:"-"`：

```go
PhoneCipher string `gorm:"column:phone_cipher" json:"-"`
PhoneIndex  string `gorm:"column:phone_index" json:"-"`
```

因此 `phoneCipher` 和 `phoneIndex` 不会出现在返回给前端的 JSON 中。

生成 API、Proto、DTO、VO 和 Mapper 时也会自动过滤这两个内部字段，对外只生成 `phone`。Repository 和 DAO 仍会保留 `PhoneCipher`、`PhoneIndex`，因为数据库持久化及索引查询需要它们。

其中 `gorm:"-"` 表示该字段只用于业务输入输出，不会在数据库增加 `phone` 列。数据库仍然只有 `phone_cipher` 和 `phone_index`。

### 手动定义模型

```go
type User struct {
	ID int64 `gorm:"column:id;primaryKey" json:"id"`

	// Phone 是业务输入和返回字段，不直接映射数据库。
	Phone string `gorm:"-" json:"phone"`

	// 下面两个字段只供插件和数据库使用，不返回给前端。
	PhoneCipher string `gorm:"column:phone_cipher" json:"-"`
	PhoneIndex  string `gorm:"column:phone_index;uniqueIndex" json:"-"`

	Nickname string `gorm:"column:nickname" json:"nickname"`
}

func (User) TableName() string {
	return "user"
}
```

不要删除 `Phone` 上的 `gorm:"-"`，否则 GORM 会尝试把手机号明文写入数据库。

## 三、注册插件

使用代码生成器产生 `gormplus` tag 后，运行时只需配置主密钥：

```go
sensitive, err := gormplus.RegisterSensitive(db, gormplus.SensitiveConfig{
	Key: []byte(os.Getenv("SENSITIVE_MASTER_KEY")),
})
if err != nil {
	return fmt.Errorf("注册敏感字段插件失败: %w", err)
}
```

插件会从生成实体的 tag 自动读取 `Phone`、`phone_cipher` 和 `phone_index`。

### 数据库存储开关

`EncryptAtRest` 是字段级开关，默认为 `false`。数据库保存规范化后的明文，但接口仍然默认返回脱敏值：

```go
gormplus.SensitiveConfig{
	Key: secretKey,
	Fields: []gormplus.SensitiveFieldConfig{{
		PlainField:     "Phone",
		CipherField:    "PhoneCipher",
		IndexField:     "PhoneIndex",
		IndexColumn:    "phone_index",
		EncryptAtRest: false,
		ReturnMode:     gormplus.SensitiveReturnMasked,
	}},
}
```

```text
数据库存储：13800138000
接口默认返回：138****8000
明文权限返回：13800138000
```

需要数据库加密时开启：

```go
gormplus.SensitiveConfig{
	Key: secretKey,
	Fields: []gormplus.SensitiveFieldConfig{{
		PlainField:     "Phone",
		CipherField:    "PhoneCipher",
		IndexField:     "PhoneIndex",
		IndexColumn:    "phone_index",
		EncryptAtRest: true,
		ReturnMode:     gormplus.SensitiveReturnMasked,
	}},
}
```

```text
数据库存储：AES-GCM 随机密文
接口默认返回：138****8000
明文权限返回：解密后的 13800138000
```

该开关只影响当前字段的数据库存储形式，不影响 `PhoneEq`、查询索引和返回脱敏逻辑。关闭加密时建议把 `cipher_field` 配成中性列名，例如 `phone_value`；继续使用 `phone_cipher` 也可以正常工作，但名称容易产生误解。

不同字段可以使用不同策略：

```go
Fields: []gormplus.SensitiveFieldConfig{
	{
		PlainField:     "Phone",
		CipherField:    "PhoneCipher",
		IndexField:     "PhoneIndex",
		IndexColumn:    "phone_index",
		EncryptAtRest: false, // 手机号数据库存明文，接口默认脱敏
		ReturnMode:     gormplus.SensitiveReturnMasked,
	},
	{
		PlainField:     "IDCard",
		CipherField:    "IDCardCipher",
		IndexField:     "IDCardIndex",
		IndexColumn:    "id_card_index",
		EncryptAtRest: true, // 身份证数据库存密文
		ReturnMode:     gormplus.SensitiveReturnMasked,
	},
}
```

已有数据的生产环境不能直接切换该开关。明文与密文模式互相切换时必须迁移存储列，否则旧数据会按错误模式读取。

没有使用生成器或需要覆盖默认规则时，可以显式配置 `PhoneField("Phone")`：

```go
sensitive, err := gormplus.RegisterSensitive(db, gormplus.SensitiveConfig{
	// 从 KMS、Vault 或安全环境变量读取，不要写死在源码中。
	Key: []byte(os.Getenv("SENSITIVE_MASTER_KEY")),
	Fields: []gormplus.SensitiveFieldConfig{
		gormplus.PhoneField("Phone"),
	},
})
if err != nil {
	return fmt.Errorf("注册敏感字段插件失败: %w", err)
}
```

主密钥不能少于 16 字节。插件会从主密钥自动派生相互独立的加密密钥和查询索引密钥。

`PhoneField("Phone")` 默认约定：

```text
业务字段：Phone
密文字段：PhoneCipher
索引字段：PhoneIndex
索引列名：phone_index
默认返回：138****8000
```

手机号写入前会自动移除空格、横线、括号和 `+86`。

## 四、插入数据

插入时只给 `Phone` 传原始手机号：

```go
user := User{
	Phone:    "13800138000",
	Nickname: "张三",
}

if err := db.WithContext(ctx).Create(&user).Error; err != nil {
	return err
}
```

插件会在创建 SQL 执行前自动设置：

```text
PhoneCipher = AES-GCM("13800138000")
PhoneIndex  = HMAC-SHA256("13800138000")
```

不要自行给 `PhoneCipher`、`PhoneIndex` 赋值。

## 五、按手机号查询

不能这样查询：

```go
// 错误：数据库没有手机号明文列。
db.Where("phone = ?", phone).First(&user)

// 错误：AES-GCM 每次加密结果不同，不能通过新密文匹配旧密文。
db.Where("phone_cipher = ?", encryptedPhone).First(&user)
```

应通过插件生成查询索引：

```go
var user User
err := sensitive.
	WhereEqual(db.WithContext(ctx), "Phone", "13800138000").
	First(&user).Error
if err != nil {
	return err
}
```

插件实际生成类似条件：

```sql
WHERE phone_index = ?
```

默认查询结果：

```go
fmt.Println(user.Phone) // 138****8000
```

### 使用 gorm-gen Repository 查询

`Phone` 使用了 `gorm:"-"`，不是数据库列，因此不会出现在 `dao.SysUserEntity` 中。DAO 中只会出现真实数据库字段 `PhoneCipher` 和 `PhoneIndex`，这是正常行为。

不能强行生成并使用：

```go
// 不存在，也不能直接把手机号明文用于 SQL 条件。
dao.SysUserEntity.Phone.Eq(phone)
```

使用插件的 `PhoneEq` 可以一行完成手机号规范化、HMAC 索引计算和 `PhoneIndex` 查询。下面是完整的 Gin Handler 示例，查询结果默认返回脱敏手机号：

```go
func (a *SysUser) GetTest1Api(ctx *gin.Context) {
	phone := ctx.Query("phone")
	if phone == "" {
		a.Fail(ctx, "手机号不能为空", nil)
		return
	}

	list, err := a.SysUserRepository.FindList(
		ctx,
		gormplus.QueryOpt().Where(
			a.SensitivePlugin.PhoneEq(dao.SysUserEntity.PhoneIndex, phone),
		).Build(),
	)
	if err != nil || len(list) == 0 {
		a.Fail(ctx, "查询失败", err)
		return
	}

	a.Success(ctx, list)
}
```

如果当前接口已完成明文查看权限校验，可以把查询 Context 改为：

```go
list, err := a.SysUserRepository.FindList(
	gormplus.WithSensitivePlaintext(ctx),
	gormplus.QueryOpt().Where(
		a.SensitivePlugin.PhoneEq(dao.SysUserEntity.PhoneIndex, phone),
	).Build(),
)
```

其中 `a.SensitivePlugin` 是注册插件时保存的实例：

```go
sensitive, err := gormplus.RegisterSensitive(db, gormplus.SensitiveConfig{
	Key: []byte(os.Getenv("SENSITIVE_MASTER_KEY")),
})
if err != nil {
	return err
}

svcCtx.SensitivePlugin = sensitive
```

也可以分两步构造条件：

```go
phoneIndex := a.SensitivePlugin.IndexValue("Phone", phone)

list, err := a.SysUserRepository.FindList(
	ctx,
	gormplus.QueryOpt().Where(
		dao.SysUserEntity.PhoneIndex.Eq(phoneIndex),
	).Build(),
)
```

普通列表查询不需要额外处理，查询后插件会逐条设置 `Phone`：

```go
func (a *SysUser) GetListApi(ctx *gin.Context) {
	list, err := a.SysUserRepository.FindList(ctx)
	if err != nil || len(list) == 0 {
		a.Fail(ctx, "查询失败", err)
		return
	}

	// Phone 默认返回类似 138****8000 的脱敏值。
	a.Success(ctx, list)
}
```

如果当前接口已经完成查看手机号明文的权限校验，可以使用 `WithSensitivePlaintext`：

```go
func (a *SysUser) GetTest2Api(ctx *gin.Context) {
	list, err := a.SysUserRepository.FindList(
		gormplus.WithSensitivePlaintext(ctx),
	)
	if err != nil || len(list) == 0 {
		a.Fail(ctx, "查询失败", err)
		return
	}

	// Phone 返回完整明文，例如 13800138000。
	a.Success(ctx, list)
}
```

## 六、控制返回内容

gorm-plus 根包导出了插件类型和三种返回模式：

```go
type SensitivePlugin = plugin.SensitivePlugin

const (
	SensitiveReturnMasked = plugin.SensitiveReturnMasked // 返回脱敏值
	SensitiveReturnPlain  = plugin.SensitiveReturnPlain  // 返回完整明文
	SensitiveReturnCipher = plugin.SensitiveReturnCipher // 返回数据库密文
)
```

注册成功后返回的 `sensitive` 类型就是 `*gormplus.SensitivePlugin`，建议保存在 ServiceContext 中供 Repository 查询使用：

```go
type ServiceContext struct {
	DB              *gorm.DB
	SensitivePlugin *gormplus.SensitivePlugin
}

sensitive, err := gormplus.RegisterSensitive(db, gormplus.SensitiveConfig{
	Key: []byte(os.Getenv("SENSITIVE_MASTER_KEY")),
})
if err != nil {
	return err
}

svcCtx := &ServiceContext{
	DB:              db,
	SensitivePlugin: sensitive,
}
```

`SensitiveReturnMasked`、`SensitiveReturnPlain`、`SensitiveReturnCipher` 主要用于字段级高级配置：

```go
sensitive, err := gormplus.RegisterSensitive(db, gormplus.SensitiveConfig{
	Key: []byte(os.Getenv("SENSITIVE_MASTER_KEY")),
	Fields: []gormplus.SensitiveFieldConfig{{
		PlainField:  "Phone",
		CipherField: "PhoneCipher",
		IndexField:  "PhoneIndex",
		IndexColumn: "phone_index",
		EncryptAtRest: false,
		ReturnMode:  gormplus.SensitiveReturnMasked,
		Mask: gormplus.SensitiveMaskConfig{
			Prefix:      3,
			Suffix:      4,
			Replacement: "*",
		},
	}},
})
```

对应结果：

| 返回模式 | `Phone` 内容 | 适用场景 |
|---|---|---|
| `SensitiveReturnMasked` | `138****8000` | 普通列表、详情，推荐默认值 |
| `SensitiveReturnPlain` | `13800138000` | 已完成敏感数据查看权限校验 |
| `SensitiveReturnCipher` | 数据库存储原值 | 内部审计、迁移等特殊场景；关闭加密时该值就是明文 |

如果配置了 `ReturnModeResolver`，还可以根据请求 Context 动态返回：

```go
ReturnModeResolver: func(ctx context.Context) gormplus.SensitiveReturnMode {
	if canViewPlainPhone(ctx) {
		return gormplus.SensitiveReturnPlain
	}
	return gormplus.SensitiveReturnMasked
},
```

大多数业务不需要显式填写 `ReturnMode`，直接使用下面的 Context 方法更简单。

### 默认返回脱敏值

```go
err := db.WithContext(ctx).First(&user, userID).Error
fmt.Println(user.Phone) // 138****8000
```

也可以明确指定脱敏返回：

```go
ctx = gormplus.WithSensitiveMasked(ctx)
err := db.WithContext(ctx).First(&user, userID).Error
```

### 有权限时返回明文

必须先由服务端完成权限判断：

```go
if !canViewPlainPhone(ctx) {
	return errors.New("没有查看完整手机号的权限")
}

plainCtx := gormplus.WithSensitivePlaintext(ctx)
err := db.WithContext(plainCtx).First(&user, userID).Error
fmt.Println(user.Phone) // 13800138000
```

不要直接信任前端传入的 `showPlaintext=true`，必须结合当前登录用户权限判断。

### 返回数据库密文

```go
cipherCtx := gormplus.WithSensitiveCiphertext(ctx)
err := db.WithContext(cipherCtx).First(&user, userID).Error
fmt.Println(user.Phone) // AES-GCM 密文
```

一般业务接口建议返回脱敏值，而不是数据库密文。

## 七、详情页面和更新请求必须分离

详情接口和更新接口不要直接复用 `User` 数据库实体。

详情响应：

```go
type UserDetailVO struct {
	ID       int64  `json:"id"`
	Phone    string `json:"phone"`
	Nickname string `json:"nickname"`
}
```

更新请求使用指针表示“是否修改手机号”：

```go
type UpdateUserReq struct {
	ID       int64   `json:"id"`
	Nickname string  `json:"nickname"`
	// nil 表示不修改手机号；非 nil 时必须是新的手机号明文。
	Phone *string `json:"phone,optional"`
}
```

### 用户没有修改手机号

详情返回：

```json
{
  "id": 1001,
  "phone": "138****8000",
  "nickname": "张三"
}
```

前端只提交修改过的普通字段，不提交 `phone`：

```json
{
  "id": 1001,
  "nickname": "李四"
}
```

后端只更新普通字段：

```go
err := db.WithContext(ctx).
	Model(&User{}).
	Where("id = ?", req.ID).
	Update("nickname", req.Nickname).Error
```

此时 `phone_cipher` 和 `phone_index` 都不会变化。

### 用户修改手机号

前端提交新的手机号明文：

```json
{
  "id": 1001,
  "nickname": "李四",
  "phone": "13900139000"
}
```

后端先更新普通字段，再单独更新手机号：

```go
func UpdateUser(ctx context.Context, db *gorm.DB, req *UpdateUserReq) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&User{}).
			Where("id = ?", req.ID).
			Update("nickname", req.Nickname).Error; err != nil {
			return err
		}

		if req.Phone == nil {
			return nil
		}
		if !isValidPhone(*req.Phone) {
			return errors.New("手机号格式错误")
		}

		// 使用独立对象，Phone 中只放新的原始明文。
		update := User{
			ID:    req.ID,
			Phone: *req.Phone,
		}
		return tx.Model(&update).
			Select("PhoneCipher", "PhoneIndex").
			Updates(&update).Error
	})
}
```

插件会同时更新新手机号的密文和查询索引。更新后，新手机号可以查询到，旧手机号无法再查询到。

## 八、禁止将密文或脱敏值回写

不要对详情查询得到的对象直接执行：

```go
// 错误：user.Phone 可能是 138****8000、明文或数据库密文。
db.Save(&user)
```

也不要把详情响应直接绑定到数据库实体后保存：

```go
// 错误：可能把 138****8000 当作新手机号再次加密。
var user User
_ = httpx.Parse(r, &user)
db.Save(&user)
```

否则可能出现：

```text
138****8000
  → 被当作新手机号
  → 再次加密
  → 数据库中的真实手机号被破坏
```

正确规则：

```text
详情字段只负责展示
更新请求未传手机号表示保持不变
更新请求传手机号时必须是新的原始明文
密文和脱敏值永远不能作为更新输入
```

## 九、推荐的 Handler 示例

```go
func (l *UpdateUserLogic) UpdateUser(req *types.UpdateUserReq) error {
	return l.svcCtx.DB.WithContext(l.ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"nickname": req.Nickname,
		}
		if err := tx.Model(&User{}).
			Where("id = ?", req.ID).
			Updates(updates).Error; err != nil {
			return err
		}

		if req.Phone == nil {
			return nil
		}

		phoneUpdate := User{ID: req.ID, Phone: *req.Phone}
		return tx.Model(&phoneUpdate).
			Select("PhoneCipher", "PhoneIndex").
			Updates(&phoneUpdate).Error
	})
}
```

## 十、限制与安全建议

- 当前支持 `string` 类型敏感字段的精确等值查询。
- 不支持直接对随机密文执行 `LIKE`、后四位或号段查询。
- 需要后四位查询时应设计独立索引，不能复用完整手机号索引。
- 主密钥应由 KMS、Vault 或安全环境变量提供，不应提交到 Git。
- 查看明文的接口应进行权限校验并记录审计日志。
- 日志中禁止打印手机号明文、密文、主密钥和查询索引。
- 更新手机号前应进行格式校验，必要时增加短信验证。
