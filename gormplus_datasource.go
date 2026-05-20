package gormplus

import (
	"github.com/kuangshp/gorm-plus/datasource"
)

// ================== 多数据源管理 ==================

// DataSourceManager 多数据源管理器类型别名。
type DataSourceManager = datasource.Manager

// DataSourceGroupConfig 数据源组配置（一主多从）。
type DataSourceGroupConfig = datasource.GroupConfig

// DataSourceNodeConfig 单个数据源节点配置。
// 通过 Dialector 字段外部传入驱动，不内置任何数据库依赖：
//
//	// MySQL
//	import "gorm.io/driver/mysql"
//	DataSourceNodeConfig{Dialector: mysql.Open(dsn)}
//
//	// PostgreSQL
//	import "gorm.io/driver/postgres"
//	DataSourceNodeConfig{Dialector: postgres.Open(dsn)}
//
//	// SQLite（测试场景）
//	import "gorm.io/driver/sqlite"
//	DataSourceNodeConfig{Dialector: sqlite.Open(":memory:")}
type DataSourceNodeConfig = datasource.NodeConfig

// DataSourcePoolConfig 连接池配置。
// 零值字段自动使用 DataSourceDefaultPool（MaxOpen=50, MaxIdle=10, MaxLifetime=30min）。
type DataSourcePoolConfig = datasource.PoolConfig

var (
	// DS 全局多数据源管理器，支持一主多从、读写分离、context 自动切换。
	// 通过 Dialector 字段传入驱动，支持 MySQL / PostgreSQL / SQLite 等任意 gorm 驱动。
	//
	// MySQL 一主两从：
	//
	//   import "gorm.io/driver/mysql"
	//
	//   gormplus.DS.Register("default", gormplus.DataSourceGroupConfig{
	//       Master: gormplus.DataSourceNodeConfig{
	//           Dialector: mysql.Open("root:pwd@tcp(master:3306)/mydb?charset=utf8mb4&parseTime=True"),
	//           Pool:      gormplus.DataSourcePoolConfig{MaxOpen: 50, MaxIdle: 10},
	//       },
	//       Slaves: []gormplus.DataSourceNodeConfig{
	//           {Dialector: mysql.Open("root:pwd@tcp(slave1:3306)/mydb?charset=utf8mb4&parseTime=True")},
	//           {Dialector: mysql.Open("root:pwd@tcp(slave2:3306)/mydb?charset=utf8mb4&parseTime=True")},
	//       },
	//   })
	//
	// PostgreSQL：
	//
	//   import "gorm.io/driver/postgres"
	//   gormplus.DS.Register("pg", gormplus.DataSourceGroupConfig{
	//       Master: gormplus.DataSourceNodeConfig{
	//           Dialector: postgres.Open("host=localhost user=root password=pwd dbname=mydb port=5432 sslmode=disable"),
	//       },
	//   })
	//
	// Repository 层获取 DB（读走从库，写走主库）：
	//
	//   db, err := gormplus.DS.Auto(ctx)
	DS = datasource.NewManager()

	// DataSourceDefaultPool 默认连接池配置（生产推荐值：MaxOpen=50, MaxIdle=10, MaxLifetime=30min）。
	DataSourceDefaultPool = datasource.DefaultPool

	// NewDataSourceManager 创建独立的数据源管理器（多实例场景使用）。
	NewDataSourceManager = datasource.NewManager

	// DSWithName 将数据源名写入 ctx，DS.Auto(ctx) 会读取它选择对应数据源。
	//   ctx = gormplus.DSWithName(ctx, "analytics")
	DSWithName = datasource.WithName

	// DSNameFrom 从 ctx 读取数据源名。
	DSNameFrom = datasource.NameFromCtx

	// DSWithRead 标记 ctx 为读操作，DS.Auto(ctx) 将选择从库。
	//   ctx = gormplus.DSWithRead(ctx)
	DSWithRead = datasource.WithRead

	// DSWithWrite 标记 ctx 为写操作，DS.Auto(ctx) 将选择主库。
	//   ctx = gormplus.DSWithWrite(ctx)
	DSWithWrite = datasource.WithWrite

	// DSIsRead 判断 ctx 是否标记了读操作。
	DSIsRead = datasource.IsRead

	// DSIsWrite 判断 ctx 是否标记了写操作。
	DSIsWrite = datasource.IsWrite
)
