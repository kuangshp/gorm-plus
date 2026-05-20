package dal

import (
	"context"

	"gorm.io/gorm"
)

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// DB Provider ///////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// DBProvider 数据库提供器接口
//
// 通过实现此接口可以支持：
//   - 单库
//   - 多库
//   - 读写分离
//   - 多租户
//   - 分库分表
type DBProvider interface {
	Get(ctx context.Context) *gorm.DB
}

// singleDBProvider 单数据库实现（包内私有）
type singleDBProvider struct {
	db *gorm.DB
}

func (p *singleDBProvider) Get(ctx context.Context) *gorm.DB {
	return p.db.WithContext(ctx)
}
