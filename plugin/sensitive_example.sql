-- 敏感字段插件 MySQL 示例表
--
-- 对应生成器配置：
-- sensitive_fields:
--   - table: sys_user
--     field: phone
--     type: phone
--     cipher_field: phone_cipher
--     index_field: phone_index
--
-- 注意：
-- 1. 不创建 phone 明文列。
-- 2. phone_cipher 保存 AES-GCM 随机密文。
-- 3. phone_index 保存 HMAC-SHA256 盲索引，用于等值查询和唯一约束。

CREATE TABLE `sys_user` (
    `id` BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键 ID',
    `username` VARCHAR(64) NOT NULL COMMENT '用户名',
    `phone_cipher` VARCHAR(255) NOT NULL DEFAULT '' COMMENT '手机号 AES-GCM 密文',
    `phone_index` VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin DEFAULT NULL COMMENT '手机号 HMAC 查询索引',
    `status` TINYINT NOT NULL DEFAULT 1 COMMENT '状态：1正常，2禁用',
    `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_sys_user_username` (`username`),
    UNIQUE KEY `uk_sys_user_phone_index` (`phone_index`),
    KEY `idx_sys_user_status` (`status`)
) ENGINE=InnoDB
  DEFAULT CHARSET=utf8mb4
  COLLATE=utf8mb4_unicode_ci
  COMMENT='系统用户表（手机号加密存储示例）';

-- 示例：手机号不能直接写入数据库。
-- 请通过 GORM 实体的 Phone 业务字段创建数据，插件会自动填写下面两个字段：
--
-- db.Create(&model.SysUserEntity{
--     Username: "zhangsan",
--     Phone:    "13800138000",
-- })
--
-- 数据库最终只保存 phone_cipher 和 phone_index。
