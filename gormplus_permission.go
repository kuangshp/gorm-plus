package gormplus

import (
	"context"

	"github.com/kuangshp/gorm-plus/plugin"
	"gorm.io/gorm"
)

// ================== 数据权限插件 ==================

// DataPermissionConfig 数据权限插件配置。
type DataPermissionConfig = plugin.DataPermissionConfig

// DataPermissionInjectFn 数据权限条件注入函数类型。
// 由业务层在中间件中实现，插件 Callback 触发时自动调用。
// 参数 db 用于追加条件，tableName 为当前表名（小写，已去掉库名前缀和反引号）。
type DataPermissionInjectFn = plugin.DataPermissionInjectFn

// RegisterDataPermission 向指定 DB 注册数据权限插件，整个应用只需调用一次。
//
// 注册后所有 db.WithContext(ctx) 的 Query / Update / Delete 操作，
// 若 ctx 中存在通过 WithDataPermission 写入的注入函数，则自动调用注入数据权限条件。
//
// 使用示例：
//
//	gormplus.RegisterDataPermission(db, gormplus.DataPermissionConfig{
//	    ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
//	})
func RegisterDataPermission(db *gorm.DB, cfg plugin.DataPermissionConfig) error {
	return plugin.RegisterDataPermission(db, cfg)
}

// NewDataPermissionPlugin 工厂函数，返回数据权限插件实例供手动 db.Use() 注册。
func NewDataPermissionPlugin(cfg plugin.DataPermissionConfig) (gorm.Plugin, error) {
	return plugin.NewDataPermissionPlugin(cfg)
}

// WithDataPermission 将数据权限注入函数写入 context，通常在中间件中调用。
//
// 使用示例：
//
//	func DataPermissionMiddleware() gin.HandlerFunc {
//	    return func(c *gin.Context) {
//	        claims, err := jwt.ParseToken(c.GetHeader("Authorization"))
//	        if err != nil { c.Next(); return }
//	        injectFn := func(db *gorm.DB, tableName string) {
//	            switch claims.DataScope {
//	            case "2": // 本角色相关部门
//	                db.Where(tableName+".create_by IN (SELECT sys_user.user_id FROM sys_role_dept LEFT JOIN sys_user ON sys_user.dept_id = sys_role_dept.dept_id WHERE sys_role_dept.role_id = ?)", claims.RoleId)
//	            case "3": // 本部门
//	                db.Where(tableName+".create_by IN (SELECT user_id FROM sys_user WHERE dept_id = ?)", claims.DeptId)
//	            case "4": // 本部门及子部门
//	                db.Where(tableName+".create_by IN (SELECT user_id FROM sys_user WHERE dept_id IN (SELECT dept_id FROM sys_dept WHERE dept_path LIKE ?))", "%/"+strconv.FormatInt(claims.DeptId, 10)+"/%")
//	            case "5": // 仅本人
//	                db.Where(tableName+".create_by = ?", claims.UserId)
//	            // default: 全部数据，不加任何条件
//	            }
//	        }
//	        ctx := gormplus.WithDataPermission(c.Request.Context(), injectFn)
//	        c.Request = c.Request.WithContext(ctx)
//	        c.Next()
//	    }
//	}
func WithDataPermission(ctx context.Context, fn plugin.DataPermissionInjectFn) context.Context {
	return plugin.WithDataPermission(ctx, fn)
}

// DataPermissionFromCtx 从 context 中读取数据权限注入函数，不存在时返回 nil。
func DataPermissionFromCtx(ctx context.Context) plugin.DataPermissionInjectFn {
	return plugin.DataPermissionFromCtx(ctx)
}

// SkipDataPermission 返回跳过数据权限过滤的 context（超管、定时任务、内部统计专用）。
//
//	ctx = gormplus.SkipDataPermission(ctx)
//	db.WithContext(ctx).Find(&allData) // 无数据权限条件
func SkipDataPermission(ctx context.Context) context.Context {
	return plugin.SkipDataPermission(ctx)
}

// AddDataPermissionExcludeTable 运行时动态添加不参与数据权限过滤的表（线程安全）。
func AddDataPermissionExcludeTable(db *gorm.DB, tables ...string) error {
	return plugin.AddDataPermissionExcludeTable(db, tables...)
}

// RemoveDataPermissionExcludeTable 运行时动态移除排除表（线程安全）。
func RemoveDataPermissionExcludeTable(db *gorm.DB, tables ...string) error {
	return plugin.RemoveDataPermissionExcludeTable(db, tables...)
}

// DataPermissionExcludedTables 返回数据权限当前所有排除表快照（调试用）。
func DataPermissionExcludedTables(db *gorm.DB) ([]string, error) {
	return plugin.DataPermissionExcludedTables(db)
}

// ================== 自动填充插件 ==================
