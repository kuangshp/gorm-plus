package gormplus

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

var repositoryDefaultDB atomic.Pointer[gorm.DB]
var repositoryDefaults = struct {
	sync.Mutex
	initializers map[string]func(*gorm.DB)
}{initializers: make(map[string]func(*gorm.DB))}

// Use 在启动时注册生成仓储的默认连接。已构造的仓储不会随默认连接变化。
// 显式连接通过 NewXRepository(db) 传入，不修改默认连接。
func Use(db *gorm.DB) {
	if err := validateRepositoryDB(db); err != nil {
		panic(err)
	}
	if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); ok {
		panic("gormplus.Use: 默认连接不能是事务连接")
	}
	repositoryDefaults.Lock()
	defer repositoryDefaults.Unlock()
	repositoryDefaultDB.Store(db)
	for _, initialize := range repositoryDefaults.initializers {
		initialize(db)
	}
}

// RegisterRepositoryDefault 供生成代码注册业务 DAO 初始化函数，按包路径去重。
// 公共库无需导入业务 DAO；Use 后 dao.XEntity 字段表达式也可直接使用。
func RegisterRepositoryDefault(key string, initialize func(*gorm.DB)) {
	if key == "" || initialize == nil {
		panic("Repository DAO 注册名称和初始化函数不能为空")
	}
	repositoryDefaults.Lock()
	defer repositoryDefaults.Unlock()
	if _, exists := repositoryDefaults.initializers[key]; exists {
		return
	}
	repositoryDefaults.initializers[key] = initialize
	if db := DefaultDB(); db != nil {
		initialize(db)
	}
}

// DefaultDB 返回 Use 注册的默认连接；尚未注册时返回 nil。
func DefaultDB() *gorm.DB { return repositoryDefaultDB.Load() }

// ResolveRepositoryDB 供生成的构造函数选择连接，最多接受一个显式句柄。
// 未注册默认连接时返回 nil，允许兼容业务项目原有的 dao.SetDefault。
func ResolveRepositoryDB(dbs ...*gorm.DB) (*gorm.DB, error) {
	if len(dbs) > 1 {
		return nil, fmt.Errorf("Repository 构造函数最多接收一个数据库连接")
	}
	if len(dbs) == 0 {
		return DefaultDB(), nil
	}
	if err := validateRepositoryDB(dbs[0]); err != nil {
		return nil, err
	}
	return dbs[0], nil
}

func validateRepositoryDB(db *gorm.DB) error {
	if db == nil || db.Config == nil || db.Statement == nil || db.Statement.ConnPool == nil {
		return fmt.Errorf("Repository 数据库连接不能为空或未初始化")
	}
	return db.Error
}

// RepositoryCacheName 给生成仓储的缓存名称添加数据源维度。
// 同一数据源应复用相同连接配置；DSN 的原文不会进入缓存键。
func RepositoryCacheName(db *gorm.DB, name string) string {
	if scope, ok := repositoryCacheScope(db); ok {
		return scope + "." + name
	}
	return name
}

// RepositoryQueryOptions 保留事务/行锁规则；无法识别的数据源不共享缓存或 SF。
// SQLite 内存库没有跨进程稳定身份，也直接查询，避免不同内存库串用缓存。
func RepositoryQueryOptions(db *gorm.DB, opt QueryOption) QueryOption {
	opt = opt.WithLockingFrom(db)
	if _, ok := repositoryCacheScope(db); !ok {
		opt.Cache, opt.SF = nil, nil
	}
	return opt
}

func repositoryCacheScope(db *gorm.DB) (string, bool) {
	if db == nil || db.Config == nil || db.Dialector == nil {
		return "", false
	}
	var dsn string
	switch d := db.Dialector.(type) {
	case *mysql.Dialector:
		if d.Config != nil && d.Conn == nil {
			dsn = d.DSN
		}
	case *postgres.Dialector:
		if d.Config != nil && d.Conn == nil {
			dsn = d.DSN
		}
	case *sqlserver.Dialector:
		if d.Config != nil && d.Conn == nil {
			dsn = d.DSN
		}
	case *sqlite.Dialector:
		if d.Conn != nil {
			return "", false
		}
		dsn = d.DSN
		if dsn == ":memory:" || strings.Contains(dsn, "mode=memory") || strings.HasPrefix(dsn, "file::memory:") {
			return "", false
		}
		if dsn != "" && !strings.HasPrefix(dsn, "file:") {
			path, options, hasOptions := strings.Cut(dsn, "?")
			absolute, err := filepath.Abs(path)
			if err != nil {
				return "", false
			}
			dsn = absolute
			if hasOptions {
				dsn += "?" + options
			}
		}
	default:
		return "", false
	}
	if dsn == "" {
		return "", false
	}
	sum := sha256.Sum256([]byte(db.Dialector.Name() + "\x00" + dsn))
	return fmt.Sprintf("ds_%x", sum[:16]), true
}
