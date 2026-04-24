// Package plugin 提供 gorm 数据权限插件，通过 gorm Callback 钩子自动注入数据权限条件。
//
// # 与多租户插件的区别
//
// 多租户插件注入的是固定条件（tenant_id = ?），所有表规则相同。
// 数据权限插件注入的条件由业务层决定，不同表、不同角色规则不同，
// 因此注入函数（InjectFn）通过 gin 中间件动态传入，插件本身不耦合任何业务 SQL。
//
// # 工作原理
//
// 数据权限的流转路径：
//
//	解析用户权限信息 → gin 中间件传入 InjectFn → WithDataPermission 写入 ctx
//	→ db.WithContext(ctx) → Callback 自动调用 InjectFn 注入条件
//
// # 快速接入（三步）
//
// 第一步：程序启动时注册插件（一次）
//
//	func main() {
//	    db, _ := gorm.Open(mysql.Open(dsn), &gorm.Config{})
//
//	    if err := plugin.RegisterDataPermission(db, plugin.DataPermissionConfig{
//	        ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
//	    }); err != nil {
//	        log.Fatalf("注册数据权限插件失败: %v", err)
//	    }
//	}
//
// 第二步：gin 中间件解析权限信息并传入 InjectFn
//
//	func DataPermissionMiddleware() gin.HandlerFunc {
//	    return func(c *gin.Context) {
//	        // 从 JWT 或 Redis 解析当前用户的数据权限信息
//	        claims, err := jwt.ParseToken(c.GetHeader("Authorization"))
//	        if err != nil {
//	            c.Next()
//	            return
//	        }
//
//	        // 定义数据权限注入函数，插件 Callback 触发时自动调用
//	        // db：当前 gorm 实例；tableName：当前操作的表名
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
//	            // default: 全部数据，不加条件
//	            }
//	        }
//
//	        // 将 injectFn 写入 ctx，后续 db.WithContext(ctx) 自动携带
//	        ctx := plugin.WithDataPermission(c.Request.Context(), injectFn)
//	        c.Request = c.Request.WithContext(ctx)
//	        c.Next()
//	    }
//	}
//
// 第三步：业务代码正常使用，无需任何改动
//
//	// 查询：自动调用 injectFn 注入数据权限条件
//	db.WithContext(ctx).Find(&list)
//
//	// 更新：自动注入
//	db.WithContext(ctx).Model(&data).Updates(map[string]any{"status": 1})
//
//	// 删除：自动注入
//	db.WithContext(ctx).Delete(&data, id)
//
// # 跳过数据权限
//
//	// 超管或内部统计接口跳过数据权限过滤
//	ctx = plugin.SkipDataPermission(ctx)
//	db.WithContext(ctx).Find(&allData) // 无数据权限条件
//
// # 排除表
//
//	// 注册时静态配置排除表
//	plugin.RegisterDataPermission(db, plugin.DataPermissionConfig{
//	    ExcludeTables: []string{"sys_config", "sys_dict"},
//	})
//
//	// 运行时动态添加排除表
//	plugin.AddDataPermissionExcludeTable(db, "log_audit")
//
//	// 运行时动态移除排除表
//	plugin.RemoveDataPermissionExcludeTable(db, "sys_dict")
package plugin

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"gorm.io/gorm"
)

// ================== InjectFn 类型 ==================

// DataPermissionInjectFn 数据权限条件注入函数类型。
//
// 由业务层在 gin 中间件中实现并传入，插件 Callback 触发时自动调用。
// 参数：
//   - db：当前 gorm 实例，直接调用 db.Where(...) 追加条件即可
//   - tableName：当前操作的表名（已去掉库名前缀和反引号，小写）
//
// 示例（sys 体系，按 DataScope 注入）：
//
//	injectFn := func(db *gorm.DB, tableName string) {
//	    switch claims.DataScope {
//	    case "2":
//	        db.Where(tableName+".create_by IN (SELECT sys_user.user_id FROM sys_role_dept LEFT JOIN sys_user ON sys_user.dept_id = sys_role_dept.dept_id WHERE sys_role_dept.role_id = ?)", claims.RoleId)
//	    case "3":
//	        db.Where(tableName+".create_by IN (SELECT user_id FROM sys_user WHERE dept_id = ?)", claims.DeptId)
//	    case "4":
//	        db.Where(tableName+".create_by IN (SELECT user_id FROM sys_user WHERE dept_id IN (SELECT dept_id FROM sys_dept WHERE dept_path LIKE ?))", "%/"+strconv.FormatInt(claims.DeptId, 10)+"/%")
//	    case "5":
//	        db.Where(tableName+".create_by = ?", claims.UserId)
//	    }
//	}
//
// 示例（按 org_id 隔离的自定义业务）：
//
//	injectFn := func(db *gorm.DB, tableName string) {
//	    if claims.OrgId > 0 {
//	        db.Where(tableName+".org_id = ?", claims.OrgId)
//	    }
//	}
type DataPermissionInjectFn func(db *gorm.DB, tableName string)

// ================== context key ==================

// dpInjectKey 存储 InjectFn 的 ctx key，私有类型避免外部冲突。
type dpInjectKey struct{}

// dpSkipKey 存储跳过标记的 ctx key。
type dpSkipKey struct{}

// ================== context 工具 ==================

// WithDataPermission 将数据权限注入函数写入 context，通常在 gin 中间件中调用。
//
// 示例：
//
//	injectFn := func(db *gorm.DB, tableName string) {
//	    db.Where(tableName+".create_by = ?", claims.UserId)
//	}
//	ctx := plugin.WithDataPermission(c.Request.Context(), injectFn)
//	c.Request = c.Request.WithContext(ctx)
func WithDataPermission(ctx context.Context, fn DataPermissionInjectFn) context.Context {
	return context.WithValue(ctx, dpInjectKey{}, fn)
}

// DataPermissionFromCtx 从 context 中读取数据权限注入函数，不存在时返回 nil。
func DataPermissionFromCtx(ctx context.Context) DataPermissionInjectFn {
	fn, _ := ctx.Value(dpInjectKey{}).(DataPermissionInjectFn)
	return fn
}

// SkipDataPermission 返回一个标记了跳过数据权限过滤的新 context。
// 用于超管查看全量数据、内部统计、定时任务等不需要数据隔离的场景。
//
// ⚠️ 注意：此 ctx 应仅在受控的特权接口中使用。
//
// 示例：
//
//	ctx = plugin.SkipDataPermission(ctx)
//	db.WithContext(ctx).Find(&allData) // 无数据权限条件
func SkipDataPermission(ctx context.Context) context.Context {
	return context.WithValue(ctx, dpSkipKey{}, true)
}

// ================== 配置 ==================

// DataPermissionConfig 数据权限插件配置。
type DataPermissionConfig struct {
	// ExcludeTables 不参与数据权限过滤的表名列表（精确匹配，不含库名前缀，不区分大小写）。
	// 通常用于公共配置表、字典表、菜单表等不需要数据隔离的表。
	// 示例：[]string{"sys_config", "sys_dict_data", "sys_menu"}
	ExcludeTables []string
}

// ================== 插件实现 ==================

const dataPermissionPluginName = "gorm-plus:data_permission"

// dataPermissionPlugin gorm.Plugin 接口实现。
type dataPermissionPlugin struct {
	excludeSet map[string]struct{} // 排除表集合，key 为小写表名
	mu         sync.RWMutex        // 保护 excludeSet 的读写锁
}

// Name 返回插件唯一名称。
func (p *dataPermissionPlugin) Name() string { return dataPermissionPluginName }

// Initialize 向 gorm 注册 Query / Update / Delete 三类操作的钩子。
// Create 通常不需要数据权限过滤，故不注册。
func (p *dataPermissionPlugin) Initialize(db *gorm.DB) error {
	for _, op := range []struct {
		name string
		reg  func(string, func(*gorm.DB)) error
	}{
		{"query", func(n string, fn func(*gorm.DB)) error {
			return db.Callback().Query().Before("gorm:query").Register(n, fn)
		}},
		{"update", func(n string, fn func(*gorm.DB)) error {
			return db.Callback().Update().Before("gorm:update").Register(n, fn)
		}},
		{"delete", func(n string, fn func(*gorm.DB)) error {
			return db.Callback().Delete().Before("gorm:delete").Register(n, fn)
		}},
	} {
		if err := op.reg(p.Name()+":"+op.name, p.inject); err != nil {
			return fmt.Errorf("RegisterDataPermission: 注册 %s 钩子失败: %w", op.name, err)
		}
	}
	return nil
}

// inject 在 Query / Update / Delete 执行前调用 InjectFn 注入数据权限条件。
func (p *dataPermissionPlugin) inject(db *gorm.DB) {
	if db.Statement == nil || db.Statement.Context == nil {
		return
	}
	ctx := db.Statement.Context

	// 检查是否显式跳过
	if skip, ok := ctx.Value(dpSkipKey{}).(bool); ok && skip {
		return
	}

	// 检查是否为排除表
	tableName := p.tableName(db)
	if p.isExcluded(tableName) {
		return
	}

	// 从 ctx 获取业务层传入的注入函数
	fn := DataPermissionFromCtx(ctx)
	if fn == nil {
		// ctx 中无注入函数（如未登录、中间件未设置），跳过
		return
	}

	// 调用业务层注入函数，由业务层决定追加什么条件
	fn(db, tableName)
}

// tableName 从 gorm Statement 中提取纯表名（去掉库名前缀和反引号，转小写）。
func (p *dataPermissionPlugin) tableName(db *gorm.DB) string {
	if db.Statement == nil {
		return ""
	}
	name := db.Statement.Table
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	return strings.ToLower(strings.Trim(name, "`"))
}

// isExcluded 判断表名是否在排除列表中（线程安全）。
func (p *dataPermissionPlugin) isExcluded(table string) bool {
	if table == "" {
		return true
	}
	p.mu.RLock()
	_, ok := p.excludeSet[table]
	p.mu.RUnlock()
	return ok
}

// ================== 插件注册 ==================

// RegisterDataPermission 向指定 DB 注册数据权限插件，整个应用只需调用一次。
//
// 注册后所有经过 db.WithContext(ctx) 的 Query / Update / Delete 操作，
// 若 ctx 中存在通过 WithDataPermission 写入的注入函数，则自动调用注入数据权限条件。
//
// 示例：
//
//	if err := plugin.RegisterDataPermission(db, plugin.DataPermissionConfig{
//	    ExcludeTables: []string{"sys_config", "sys_dict", "sys_menu"},
//	}); err != nil {
//	    log.Fatalf("注册数据权限插件失败: %v", err)
//	}
func RegisterDataPermission(db *gorm.DB, cfg DataPermissionConfig) error {
	excludeSet := make(map[string]struct{}, len(cfg.ExcludeTables))
	for _, t := range cfg.ExcludeTables {
		excludeSet[strings.ToLower(t)] = struct{}{}
	}
	return db.Use(&dataPermissionPlugin{excludeSet: excludeSet})
}

// NewDataPermissionPlugin 工厂函数，返回插件实例供手动注册（db.Use）。
//
// 示例：
//
//	p, err := plugin.NewDataPermissionPlugin(plugin.DataPermissionConfig{
//	    ExcludeTables: []string{"sys_config"},
//	})
//	if err != nil { ... }
//	db.Use(p)
func NewDataPermissionPlugin(cfg DataPermissionConfig) (gorm.Plugin, error) {
	excludeSet := make(map[string]struct{}, len(cfg.ExcludeTables))
	for _, t := range cfg.ExcludeTables {
		excludeSet[strings.ToLower(t)] = struct{}{}
	}
	return &dataPermissionPlugin{excludeSet: excludeSet}, nil
}

// ================== 动态排除表 ==================

// getDataPermissionPlugin 从 gorm DB 的插件注册表中取出数据权限插件实例。
func getDataPermissionPlugin(db *gorm.DB) (*dataPermissionPlugin, error) {
	raw, ok := db.Config.Plugins[dataPermissionPluginName]
	if !ok {
		return nil, fmt.Errorf("data_permission: 插件未注册，请先调用 RegisterDataPermission")
	}
	p, ok := raw.(*dataPermissionPlugin)
	if !ok {
		return nil, fmt.Errorf("data_permission: 插件类型断言失败")
	}
	return p, nil
}

// AddDataPermissionExcludeTable 运行时动态添加不参与数据权限过滤的表，线程安全。
//
// 示例：
//
//	plugin.AddDataPermissionExcludeTable(db, "log_audit", "sys_trace")
func AddDataPermissionExcludeTable(db *gorm.DB, tables ...string) error {
	p, err := getDataPermissionPlugin(db)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range tables {
		p.excludeSet[strings.ToLower(t)] = struct{}{}
	}
	return nil
}

// RemoveDataPermissionExcludeTable 运行时动态移除排除表，使其重新参与数据权限过滤，线程安全。
//
// 示例：
//
//	plugin.RemoveDataPermissionExcludeTable(db, "sys_dict")
func RemoveDataPermissionExcludeTable(db *gorm.DB, tables ...string) error {
	p, err := getDataPermissionPlugin(db)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, t := range tables {
		delete(p.excludeSet, strings.ToLower(t))
	}
	return nil
}

// DataPermissionExcludedTables 返回当前所有排除表的名称列表快照，用于调试和运维查询。
//
// 示例：
//
//	tables, err := plugin.DataPermissionExcludedTables(db)
//	if err == nil {
//	    fmt.Println("当前数据权限排除表:", tables)
//	}
func DataPermissionExcludedTables(db *gorm.DB) ([]string, error) {
	p, err := getDataPermissionPlugin(db)
	if err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	tables := make([]string, 0, len(p.excludeSet))
	for t := range p.excludeSet {
		tables = append(tables, t)
	}
	return tables, nil
}
