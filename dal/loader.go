package dal

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"
)

////////////////////////////////////////////////////////////////////////////////
//////////////////////////////// SQL Loader ////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

// SQLLoader SQL 文件加载器接口
//
// 内置实现：EmbedLoader，基于 fs.FS（embed.FS）。
// 也可自行实现，例如从数据库、网络加载 SQL。
type SQLLoader interface {
	Load(file string) (string, error)
	ClearCache()
}

// EmbedLoader 基于 fs.FS 的 SQL Loader
//
// 适用场景：SQL 文件通过 //go:embed 在编译期打包进二进制，
// 生产部署只需一个可执行文件，无需上传任何 .sql 文件。
//
// 使用示例：
//
//	//go:embed rawsql
//	var SQLFS embed.FS
//
//	// 推荐：fs.Sub 去掉顶层目录前缀，调用时路径更简洁
//	sub, _ := fs.Sub(SQLFS, "rawsql")
//	dal.NewDal(db, dal.NewEmbedLoader(sub))
//
//	// 调用时只需相对路径
//	dal.Query[UserVO](ctx, "account/list.sql", args...)
type EmbedLoader struct {
	fs    fs.FS
	cache sync.Map
	group singleflight.Group
}

// NewEmbedLoader 创建基于 fs.FS 的 Loader
func NewEmbedLoader(fsys fs.FS) *EmbedLoader {
	return &EmbedLoader{fs: fsys}
}

// Load 从 embed.FS 加载 SQL 文件，自动缓存、并发安全、singleflight 防击穿
func (l *EmbedLoader) Load(file string) (string, error) {
	file = filepath.ToSlash(file)

	if v, ok := l.cache.Load(file); ok {
		return v.(string), nil
	}

	v, err, _ := l.group.Do(file, func() (interface{}, error) {
		b, err := fs.ReadFile(l.fs, file)
		if err != nil {
			return nil, fmt.Errorf("dal.EmbedLoader.Load [%s]: %w", file, err)
		}

		sqlText := strings.TrimSpace(string(b))
		l.cache.Store(file, sqlText)
		return sqlText, nil
	})

	if err != nil {
		return "", err
	}

	return v.(string), nil
}

// ClearCache 清空所有已缓存的 SQL
func (l *EmbedLoader) ClearCache() {
	l.cache.Range(func(k, _ any) bool {
		l.cache.Delete(k)
		return true
	})
}
