package datasource

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ================== 多数据源管理（含自动切换）==================
//
// 核心功能：
//   - 命名数据源注册，一主多从架构，从库轮询负载均衡
//   - 懒连接：首次 Write/Read 时才建立连接，启动不阻塞
//   - 连接池独立配置，提供生产推荐默认值
//   - 【自动切换】通过 context 携带数据源名，无需每次手动传 name
//   - 【自动读写分离】通过 context 标记读/写意图，自动选主库或从库
//   - 支持任意 gorm 驱动（MySQL / PostgreSQL / SQLite / SQL Server 等）
//   - 运行时热注册，Ping 健康检查，优雅关闭
//
// ── 第一步：注册数据源（main.go 启动时调用一次）────────────────
//
// 驱动通过 NodeConfig.Dialector 传入，不内置任何驱动依赖。
//
// MySQL（需 go get gorm.io/driver/mysql）：
//
//	import "gorm.io/driver/mysql"
//
//	var DS = datasource.NewManager()
//	DS.Register("default", datasource.GroupConfig{
//	    Master: datasource.NodeConfig{
//	        Dialector: mysql.Open("root:pwd@tcp(master:3306)/mydb?charset=utf8mb4&parseTime=True&loc=Local"),
//	        Pool: datasource.PoolConfig{MaxOpen: 50, MaxIdle: 10},
//	    },
//	    Slaves: []datasource.NodeConfig{
//	        {Dialector: mysql.Open("root:pwd@tcp(slave1:3306)/mydb?charset=utf8mb4&parseTime=True&loc=Local")},
//	        {Dialector: mysql.Open("root:pwd@tcp(slave2:3306)/mydb?charset=utf8mb4&parseTime=True&loc=Local")},
//	    },
//	})
//
// PostgreSQL（需 go get gorm.io/driver/postgres）：
//
//	import "gorm.io/driver/postgres"
//
//	DS.Register("pg", datasource.GroupConfig{
//	    Master: datasource.NodeConfig{
//	        Dialector: postgres.Open("host=localhost user=root password=pwd dbname=mydb port=5432 sslmode=disable TimeZone=Asia/Shanghai"),
//	    },
//	})
//
// SQLite（需 go get gorm.io/driver/sqlite，适合单元测试）：
//
//	import "gorm.io/driver/sqlite"
//
//	DS.Register("test", datasource.GroupConfig{
//	    Master: datasource.NodeConfig{
//	        Dialector: sqlite.Open(":memory:"),
//	    },
//	})
//
// SQL Server（需 go get gorm.io/driver/sqlserver）：
//
//	import "gorm.io/driver/sqlserver"
//
//	DS.Register("mssql", datasource.GroupConfig{
//	    Master: datasource.NodeConfig{
//	        Dialector: sqlserver.Open("sqlserver://user:pwd@localhost:1433?database=mydb"),
//	    },
//	})
//
// 多数据源（不同业务库分开注册）：
//
//	DS.Register("default",   datasource.GroupConfig{Master: datasource.NodeConfig{Dialector: mysql.Open(mainDSN)}})
//	DS.Register("analytics", datasource.GroupConfig{Master: datasource.NodeConfig{Dialector: mysql.Open(analyticsDSN)}})
//	DS.Register("archive",   datasource.GroupConfig{Master: datasource.NodeConfig{Dialector: postgres.Open(archiveDSN)}})
//
// ── 第二步：Middleware 写入 context（可选，推荐与读写分离配合使用）──
//
// 固定数据源：
//
//	func DSMiddleware(name string) gin.HandlerFunc {
//	    return func(c *gin.Context) {
//	        ctx := datasource.WithName(c.Request.Context(), name)
//	        c.Request = c.Request.WithContext(ctx)
//	        c.Next()
//	    }
//	}
//
// 读写分离（GET 走从库，其余走主库）：
//
//	func RWMiddleware(name string) gin.HandlerFunc {
//	    return func(c *gin.Context) {
//	        ctx := datasource.WithName(c.Request.Context(), name)
//	        if c.Request.Method == http.MethodGet {
//	            ctx = datasource.WithRead(ctx)  // 读操作 → 从库
//	        } else {
//	            ctx = datasource.WithWrite(ctx) // 写操作 → 主库
//	        }
//	        c.Request = c.Request.WithContext(ctx)
//	        c.Next()
//	    }
//	}
//
// ── 第三步：Repository 层获取 DB ─────────────────────────────
//
// 推荐：Auto(ctx) 自动读取 context 决定数据源和读写（与 Middleware 配合）：
//
//	func (r *OrderRepo) List(ctx context.Context) ([]*Order, error) {
//	    db, err := DS.Auto(ctx) // 自动：数据源=context中的名称，读=从库
//	    if err != nil { return nil, err }
//	    var list []*Order
//	    return list, db.WithContext(ctx).Find(&list).Error
//	}
//
//	func (r *OrderRepo) Create(ctx context.Context, o *Order) error {
//	    db, err := DS.Auto(ctx) // 自动：数据源=context中的名称，写=主库
//	    if err != nil { return err }
//	    return db.WithContext(ctx).Create(o).Error
//	}
//
// 显式指定（不依赖 Middleware，直接指定数据源名和读写）：
//
//	db, err := DS.Write("default")              // 主库
//	db, err := DS.Read("default")               // 从库
//	db, err := DS.WriteCtx(ctx, "analytics")    // 指定数据源主库
//	db, err := DS.ReadCtx(ctx, "analytics")     // 指定数据源从库
//
// ── 第四步：优雅退出 ─────────────────────────────────────────
//
//	func main() {
//	    defer DS.Close() // 关闭所有数据库连接
//	}
//
// ── 健康检查 ─────────────────────────────────────────────────
//
//	results := DS.Ping()
//	// map[string]error{"default:master": nil, "default:slave0": nil}
//	for label, err := range results {
//	    if err != nil {
//	        log.Printf("数据源 %s 不可用: %v", label, err)
//	    }
//	}

// ── 连接池配置 ────────────────────────────────────────────────

// PoolConfig 连接池配置。零值字段自动使用 DefaultPool 对应值。
type PoolConfig struct {
	// MaxOpen 最大开放连接数，建议 CPU×4~8，0 时用默认值 50
	MaxOpen int
	// MaxIdle 最大空闲连接数，建议 MaxOpen/2，0 时用默认值 10
	MaxIdle int
	// MaxLifetime 连接最大存活时间，须小于 MySQL wait_timeout（默认 8h），0 时用默认值 30min
	MaxLifetime time.Duration
	// MaxIdleTime 空闲连接最大存活时间，0 时用默认值 10min
	MaxIdleTime time.Duration
}

// DefaultPool 生产推荐的默认连接池参数
var DefaultPool = PoolConfig{
	MaxOpen:     50,
	MaxIdle:     10,
	MaxLifetime: 30 * time.Minute,
	MaxIdleTime: 10 * time.Minute,
}

// ── 节点 / 组配置 ──────────────────────────────────────────────

// NodeConfig 单个数据库节点配置
type NodeConfig struct {
	// Dialector gorm 方言驱动（与 DSN 二选一，优先级高于 DSN）。
	// 支持任意 gorm 驱动：MySQL、PostgreSQL、SQLite、SQL Server 等。
	//
	// MySQL 示例：
	//   Dialector: mysql.Open("root:pwd@tcp(127.0.0.1:3306)/mydb?charset=utf8mb4&parseTime=True")
	//
	// PostgreSQL 示例：
	//   Dialector: postgres.Open("host=localhost user=root password=pwd dbname=mydb port=5432 sslmode=disable")
	//
	// SQLite 示例：
	//   Dialector: sqlite.Open("mydb.db")
	//
	// 同时配置 Dialector 和 DSN 时，Dialector 优先。
	Dialector gorm.Dialector

	// DSN gorm 连接字符串（向后兼容，Dialector 为 nil 时使用）。
	// ⚠️ 使用 DSN 时内部默认使用 MySQL 驱动，如需其他数据库请改用 Dialector。
	//
	// 示例："root:pwd@tcp(127.0.0.1:3306)/mydb?charset=utf8mb4&parseTime=True&loc=Local"
	//
	// Deprecated: 建议改用 Dialector 明确指定驱动，避免隐式依赖 MySQL。
	DSN string

	// Pool 连接池配置，零值字段自动使用 DefaultPool
	Pool PoolConfig

	// GormConfig 自定义 gorm 配置，nil 时使用内置默认配置（Warn 级别日志）
	GormConfig *gorm.Config
}

// GroupConfig 一个数据源组的完整配置（一主多从）
type GroupConfig struct {
	// Master 主库（读写，必填）
	Master NodeConfig
	// Slaves 从库列表（只读，可为空；为空时读操作 fallback 主库）
	Slaves []NodeConfig
}

// ── 内部节点（懒连接）──────────────────────────────────────────

type dbNode struct {
	once sync.Once
	cfg  NodeConfig
	db   *gorm.DB
	err  error
}

func (n *dbNode) DB() (*gorm.DB, error) {
	n.once.Do(func() { n.db, n.err = openDB(n.cfg) })
	return n.db, n.err
}

// dbGroup 一组数据源（一主多从）
type dbGroup struct {
	master   *dbNode
	slaves   []*dbNode
	slaveIdx atomic.Uint64
}

func (g *dbGroup) masterDB() (*gorm.DB, error) { return g.master.DB() }

func (g *dbGroup) slaveDB() (*gorm.DB, error) {
	if len(g.slaves) == 0 {
		return g.master.DB()
	}
	idx := g.slaveIdx.Add(1) % uint64(len(g.slaves))
	return g.slaves[idx].DB()
}

// ── Manager ────────────────────────────────────────────────────

// Manager 多数据源管理器（线程安全）
type Manager struct {
	mu      sync.RWMutex
	groups  map[string]*dbGroup
	defName string // 默认数据源名（第一个注册的）
}

// NewManager 创建多数据源管理器
func NewManager() *Manager {
	return &Manager{groups: make(map[string]*dbGroup)}
}

// Register 注册一个命名数据源组（支持运行时热注册）。
// 第一个注册的数据源自动成为默认数据源（Auto 在 context 无数据源名时使用）。
// Master.DSN 为空时 panic。
func (m *Manager) Register(name string, cfg GroupConfig) {
	if cfg.Master.Dialector == nil && cfg.Master.DSN == "" {
		panic(fmt.Sprintf("datasource.Register: 数据源 [%s] 的主库必须配置 Dialector 或 DSN", name))
	}
	g := &dbGroup{master: &dbNode{cfg: cfg.Master}}
	for _, s := range cfg.Slaves {
		if s.DSN != "" {
			g.slaves = append(g.slaves, &dbNode{cfg: s})
		}
	}
	m.mu.Lock()
	m.groups[name] = g
	if m.defName == "" {
		m.defName = name
	}
	m.mu.Unlock()
}

// SetDefault 手动设置默认数据源名（覆盖首次注册的自动设置）。
func (m *Manager) SetDefault(name string) {
	m.mu.Lock()
	m.defName = name
	m.mu.Unlock()
}

// ── 自动切换（核心新增）────────────────────────────────────────

// Auto 根据 context 自动决定数据源和读写类型，是 Repository 层的首选调用方式。
//
// 决策规则：
//  1. 从 context 读取数据源名（WithName 写入），无则使用默认数据源
//  2. 从 context 读取读写标记（WithRead/WithWrite 写入），无标记时默认走主库
//  3. 读标记 → 从库（轮询，无从库 fallback 主库）
//  4. 写标记 → 主库
//
// 示例：
//
//	db, err := DS.Auto(ctx)
func (m *Manager) Auto(ctx context.Context) (*gorm.DB, error) {
	name := NameFromCtx(ctx)
	if name == "" {
		m.mu.RLock()
		name = m.defName
		m.mu.RUnlock()
	}
	if name == "" {
		return nil, fmt.Errorf("datasource.Auto: 未找到数据源名且未设置默认数据源")
	}
	g, err := m.group(name)
	if err != nil {
		return nil, err
	}
	var db *gorm.DB
	if IsRead(ctx) {
		db, err = g.slaveDB()
	} else {
		db, err = g.masterDB()
	}
	if err != nil {
		return nil, err
	}
	return db.WithContext(ctx), nil
}

// MustAuto 同 Auto，失败时 panic（适合启动阶段验证配置）。
func (m *Manager) MustAuto(ctx context.Context) *gorm.DB {
	db, err := m.Auto(ctx)
	if err != nil {
		panic(fmt.Sprintf("datasource.MustAuto: %v", err))
	}
	return db
}

// ── 显式指定（精细控制）────────────────────────────────────────

// Write 获取指定数据源的主库（写操作）
func (m *Manager) Write(name string) (*gorm.DB, error) {
	g, err := m.group(name)
	if err != nil {
		return nil, err
	}
	return g.masterDB()
}

// Read 获取指定数据源的从库（读操作，无从库时 fallback 主库）
func (m *Manager) Read(name string) (*gorm.DB, error) {
	g, err := m.group(name)
	if err != nil {
		return nil, err
	}
	return g.slaveDB()
}

// WriteCtx 获取指定数据源的主库并注入 context
func (m *Manager) WriteCtx(ctx context.Context, name string) (*gorm.DB, error) {
	db, err := m.Write(name)
	if err != nil {
		return nil, err
	}
	return db.WithContext(ctx), nil
}

// ReadCtx 获取指定数据源的从库并注入 context
func (m *Manager) ReadCtx(ctx context.Context, name string) (*gorm.DB, error) {
	db, err := m.Read(name)
	if err != nil {
		return nil, err
	}
	return db.WithContext(ctx), nil
}

// MustWrite 获取主库，失败时 panic（适合启动阶段）
func (m *Manager) MustWrite(name string) *gorm.DB {
	db, err := m.Write(name)
	if err != nil {
		panic(fmt.Sprintf("datasource.MustWrite(%q): %v", name, err))
	}
	return db
}

// ── 管理接口 ───────────────────────────────────────────────────

// Names 返回所有已注册的数据源名称
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.groups))
	for k := range m.groups {
		names = append(names, k)
	}
	return names
}

// Ping 检查所有数据源节点连通性，返回 "name:role" → error 的映射。
// nil 表示正常，非 nil 表示异常。适合 /health 接口调用。
func (m *Manager) Ping() map[string]error {
	m.mu.RLock()
	snapshot := make(map[string]*dbGroup, len(m.groups))
	for k, v := range m.groups {
		snapshot[k] = v
	}
	m.mu.RUnlock()

	result := make(map[string]error)
	for name, g := range snapshot {
		mdb, err := g.masterDB()
		if err != nil {
			result[name+":master"] = err
		} else if sqlDB, e := mdb.DB(); e != nil {
			result[name+":master"] = e
		} else {
			result[name+":master"] = sqlDB.Ping()
		}
		for i, slave := range g.slaves {
			key := fmt.Sprintf("%s:slave%d", name, i)
			sdb, err := slave.DB()
			if err != nil {
				result[key] = err
				continue
			}
			sqlDB, err := sdb.DB()
			if err != nil {
				result[key] = err
				continue
			}
			result[key] = sqlDB.Ping()
		}
	}
	return result
}

// Close 关闭所有数据源连接，应用退出时调用。
func (m *Manager) Close() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for name, g := range m.groups {
		_ = closeNode(g.master, name+":master")
		for i, s := range g.slaves {
			_ = closeNode(s, fmt.Sprintf("%s:slave%d", name, i))
		}
	}
}

func (m *Manager) group(name string) (*dbGroup, error) {
	m.mu.RLock()
	g, ok := m.groups[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("datasource: 数据源 [%s] 未注册，已注册: %v", name, m.Names())
	}
	return g, nil
}

// ── 内部工具 ───────────────────────────────────────────────────

func openDB(cfg NodeConfig) (*gorm.DB, error) {
	gormCfg := cfg.GormConfig
	if gormCfg == nil {
		gormCfg = &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)}
	}

	// 优先使用 Dialector，支持任意 gorm 驱动（MySQL / PostgreSQL / SQLite 等）
	dialector := cfg.Dialector
	if dialector == nil {
		// 向后兼容：DSN 非空时报错提示用户改用 Dialector
		if cfg.DSN == "" {
			return nil, fmt.Errorf("datasource: Dialector 和 DSN 不能同时为空，请通过 NodeConfig.Dialector 传入数据库驱动")
		}
		return nil, fmt.Errorf(
			"datasource: 不再内置 MySQL 驱动依赖，请改用 NodeConfig.Dialector 明确指定驱动。\n" +
				"示例（MySQL）：\n" +
				"  import \"gorm.io/driver/mysql\"\n" +
				"  NodeConfig{Dialector: mysql.Open(\"" + cfg.DSN + "\")}\n" +
				"示例（PostgreSQL）：\n" +
				"  import \"gorm.io/driver/postgres\"\n" +
				"  NodeConfig{Dialector: postgres.Open(dsn)}",
		)
	}

	db, err := gorm.Open(dialector, gormCfg)
	if err != nil {
		return nil, fmt.Errorf("datasource: 连接数据库失败: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("datasource: 获取 sql.DB 失败: %w", err)
	}
	applyPool(sqlDB, cfg.Pool)
	return db, nil
}

func applyPool(sqlDB *sql.DB, pool PoolConfig) {
	maxOpen := pool.MaxOpen
	if maxOpen <= 0 {
		maxOpen = DefaultPool.MaxOpen
	}
	maxIdle := pool.MaxIdle
	if maxIdle <= 0 {
		maxIdle = DefaultPool.MaxIdle
	}
	maxLifetime := pool.MaxLifetime
	if maxLifetime <= 0 {
		maxLifetime = DefaultPool.MaxLifetime
	}
	maxIdleTime := pool.MaxIdleTime
	if maxIdleTime <= 0 {
		maxIdleTime = DefaultPool.MaxIdleTime
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(maxLifetime)
	sqlDB.SetConnMaxIdleTime(maxIdleTime)
}

func closeNode(n *dbNode, label string) error {
	if n == nil || n.db == nil {
		return nil
	}
	sqlDB, err := n.db.DB()
	if err != nil {
		return fmt.Errorf("datasource.Close [%s]: %w", label, err)
	}
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("datasource.Close [%s]: %w", label, err)
	}
	return nil
}

// ── context 工具 ────────────────────────────────────────────────

type dsNameKey struct{}
type dsRWKey struct{}

const (
	dsRWRead  = "read"
	dsRWWrite = "write"
)

// WithName 将数据源名写入 context，Auto() 会读取它。
//
//	ctx = datasource.WithName(ctx, "analytics")
func WithName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, dsNameKey{}, name)
}

// NameFromCtx 从 context 读取数据源名。
func NameFromCtx(ctx context.Context) string {
	name, _ := ctx.Value(dsNameKey{}).(string)
	return name
}

// WithRead 标记 context 为读操作，Auto() 将选择从库。
// 推荐在 HTTP GET Middleware 中统一设置，无需业务代码感知。
//
//	ctx = datasource.WithRead(ctx)
func WithRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, dsRWKey{}, dsRWRead)
}

// WithWrite 标记 context 为写操作，Auto() 将选择主库。
// Auto() 默认即走主库；显式调用 WithWrite 可提升语义清晰度。
//
//	ctx = datasource.WithWrite(ctx)
func WithWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, dsRWKey{}, dsRWWrite)
}

// IsRead 判断 context 是否标记了读操作。
func IsRead(ctx context.Context) bool {
	v, _ := ctx.Value(dsRWKey{}).(string)
	return v == dsRWRead
}

// IsWrite 判断 context 是否标记了写操作。
func IsWrite(ctx context.Context) bool {
	v, _ := ctx.Value(dsRWKey{}).(string)
	return v == dsRWWrite || v == ""
}
