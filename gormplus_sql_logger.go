package gormplus

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm/logger"
)

type sqlCallerContextKey struct{}

// SQLCaller 表示 SQL 日志希望展示的业务调用位置。
type SQLCaller struct {
	File string
	Line int
}

func (c SQLCaller) String() string {
	if c.File == "" || c.Line <= 0 {
		return ""
	}
	return c.File + ":" + strconv.Itoa(c.Line)
}

// WithSQLCaller 在 ctx 中记录当前调用点,SQLCallerLogger 会优先打印这个位置。
//
// 适用于业务代码希望强制指定 SQL 日志跳转位置的场景:
//
//	ctx = gormplus.WithSQLCaller(ctx)
//	repo.FindById(ctx, id)
func WithSQLCaller(ctx context.Context) context.Context {
	return WithSQLCallerSkip(ctx, 1)
}

// WithSQLCallerSkip 与 WithSQLCaller 类似,但允许调用方额外跳过封装层。
func WithSQLCallerSkip(ctx context.Context, skip int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, file, line, ok := runtime.Caller(skip + 1); ok {
		return ContextWithSQLCaller(ctx, SQLCaller{File: file, Line: line})
	}
	return ctx
}

// ContextWithSQLCaller 把明确的调用位置写入 ctx。
func ContextWithSQLCaller(ctx context.Context, caller SQLCaller) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if caller.File == "" || caller.Line <= 0 {
		return ctx
	}
	return context.WithValue(ctx, sqlCallerContextKey{}, caller)
}

// SQLCallerFromContext 从 ctx 读取 SQL 日志调用位置。
func SQLCallerFromContext(ctx context.Context) (SQLCaller, bool) {
	if ctx == nil {
		return SQLCaller{}, false
	}
	caller, ok := ctx.Value(sqlCallerContextKey{}).(SQLCaller)
	return caller, ok && caller.File != "" && caller.Line > 0
}

// SQLCallerLoggerOption 调整 SQLCallerLogger 的调用栈定位策略。
type SQLCallerLoggerOption func(*sqlCallerLogger)

// WithSQLCallerSkipPath 增加调用栈跳过路径片段。
//
// 例如项目里的 repository 目录不希望出现在 SQL 日志里:
//
//	gormplus.WithSQLCallerSkipPath("/repository/")
func WithSQLCallerSkipPath(parts ...string) SQLCallerLoggerOption {
	return func(l *sqlCallerLogger) {
		l.skipPathParts = append(l.skipPathParts, parts...)
	}
}

// WithSQLCallerStackLookup 设置没有显式 ctx caller 时是否从运行时调用栈中查找业务位置。
func WithSQLCallerStackLookup(enable bool) SQLCallerLoggerOption {
	return func(l *sqlCallerLogger) {
		l.stackLookup = enable
	}
}

// NewSQLCallerLogger 创建一个按业务调用点打印 SQL 的 GORM logger。
//
// 它兼容 GORM 默认 logger 的输出字段,但 Trace 的 file:line 会优先使用:
//  1. ctx 中由 WithSQLCaller / ContextWithSQLCaller 写入的位置
//  2. 当前调用栈中跳过 GORM、gorm-plus、*.gen.go、repository/dao 等后的第一帧
//
// 初始化示例:
//
//	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
//	    Logger: gormplus.NewSQLCallerLogger(nil, logger.Config{
//	        SlowThreshold: 200 * time.Millisecond,
//	        LogLevel:      logger.Info,
//	        Colorful:      true,
//	    }),
//	})
func NewSQLCallerLogger(writer logger.Writer, config logger.Config, opts ...SQLCallerLoggerOption) logger.Interface {
	if writer == nil {
		writer = log.New(os.Stdout, "\r\n", log.LstdFlags)
	}
	l := &sqlCallerLogger{
		Writer:      writer,
		Config:      config,
		stackLookup: true,
		skipPathParts: []string{
			"/gorm.io/gorm@",
			"/gorm.io/gen@",
			"/github.com/kuangshp/gorm-plus/",
			"/github.com/kuangshp/gorm-plus@",
			"/repository/",
			"\\repository\\",
			"/dao/",
			"\\dao\\",
		},
	}
	l.refreshFormat()
	for _, opt := range opts {
		if opt != nil {
			opt(l)
		}
	}
	return l
}

type sqlCallerLogger struct {
	logger.Writer
	logger.Config
	infoStr, warnStr, errStr            string
	traceStr, traceErrStr, traceWarnStr string
	stackLookup                         bool
	skipPathParts                       []string
}

func (l *sqlCallerLogger) refreshFormat() {
	l.infoStr = "%s\n[info] "
	l.warnStr = "%s\n[warn] "
	l.errStr = "%s\n[error] "
	l.traceStr = "%s\n[%.3fms] [rows:%v] %s"
	l.traceWarnStr = "%s %s\n[%.3fms] [rows:%v] %s"
	l.traceErrStr = "%s %s\n[%.3fms] [rows:%v] %s"

	if l.Colorful {
		l.infoStr = logger.Green + "%s\n" + logger.Reset + logger.Green + "[info] " + logger.Reset
		l.warnStr = logger.BlueBold + "%s\n" + logger.Reset + logger.Magenta + "[warn] " + logger.Reset
		l.errStr = logger.Magenta + "%s\n" + logger.Reset + logger.Red + "[error] " + logger.Reset
		l.traceStr = logger.Green + "%s\n" + logger.Reset + logger.Yellow + "[%.3fms] " + logger.BlueBold + "[rows:%v]" + logger.Reset + " %s"
		l.traceWarnStr = logger.Green + "%s " + logger.Yellow + "%s\n" + logger.Reset + logger.RedBold + "[%.3fms] " + logger.Yellow + "[rows:%v]" + logger.Magenta + " %s" + logger.Reset
		l.traceErrStr = logger.RedBold + "%s " + logger.MagentaBold + "%s\n" + logger.Reset + logger.Yellow + "[%.3fms] " + logger.BlueBold + "[rows:%v]" + logger.Reset + " %s"
	}
}

func (l *sqlCallerLogger) LogMode(level logger.LogLevel) logger.Interface {
	newLogger := *l
	newLogger.LogLevel = level
	newLogger.refreshFormat()
	return &newLogger
}

func (l *sqlCallerLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= logger.Info {
		l.Printf(l.infoStr+msg, append([]interface{}{l.caller(ctx)}, data...)...)
	}
}

func (l *sqlCallerLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= logger.Warn {
		l.Printf(l.warnStr+msg, append([]interface{}{l.caller(ctx)}, data...)...)
	}
}

func (l *sqlCallerLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= logger.Error {
		l.Printf(l.errStr+msg, append([]interface{}{l.caller(ctx)}, data...)...)
	}
}

func (l *sqlCallerLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.LogLevel <= logger.Silent {
		return
	}

	elapsed := time.Since(begin)
	caller := l.caller(ctx)
	switch {
	case err != nil && l.LogLevel >= logger.Error && (!errors.Is(err, logger.ErrRecordNotFound) || !l.IgnoreRecordNotFoundError):
		sql, rows := fc()
		l.printTrace(l.traceErrStr, caller, err, elapsed, rows, sql)
	case elapsed > l.SlowThreshold && l.SlowThreshold != 0 && l.LogLevel >= logger.Warn:
		sql, rows := fc()
		l.printTrace(l.traceWarnStr, caller, fmt.Sprintf("SLOW SQL >= %v", l.SlowThreshold), elapsed, rows, sql)
	case l.LogLevel == logger.Info:
		sql, rows := fc()
		if rows == -1 {
			l.Printf(l.traceStr, caller, float64(elapsed.Nanoseconds())/1e6, "-", sql)
		} else {
			l.Printf(l.traceStr, caller, float64(elapsed.Nanoseconds())/1e6, rows, sql)
		}
	}
}

func (l *sqlCallerLogger) ParamsFilter(ctx context.Context, sql string, params ...interface{}) (string, []interface{}) {
	if l.ParameterizedQueries {
		return sql, nil
	}
	return sql, params
}

func (l *sqlCallerLogger) printTrace(format string, caller string, msg any, elapsed time.Duration, rows int64, sql string) {
	if rows == -1 {
		l.Printf(format, caller, msg, float64(elapsed.Nanoseconds())/1e6, "-", sql)
		return
	}
	l.Printf(format, caller, msg, float64(elapsed.Nanoseconds())/1e6, rows, sql)
}

func (l *sqlCallerLogger) caller(ctx context.Context) string {
	if caller, ok := SQLCallerFromContext(ctx); ok {
		return caller.String()
	}
	if l.stackLookup {
		if caller, ok := l.lookupStackCaller(); ok {
			return caller.String()
		}
	}
	return ""
}

func (l *sqlCallerLogger) lookupStackCaller() (SQLCaller, bool) {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if l.acceptFrame(frame) {
			return SQLCaller{File: frame.File, Line: frame.Line}, true
		}
		if !more {
			break
		}
	}
	return SQLCaller{}, false
}

func (l *sqlCallerLogger) acceptFrame(frame runtime.Frame) bool {
	if frame.File == "" || strings.HasSuffix(frame.File, "_test.go") || strings.HasSuffix(frame.File, ".gen.go") {
		return false
	}
	if strings.HasPrefix(frame.Function, "github.com/kuangshp/gorm-plus.") ||
		strings.HasPrefix(frame.Function, "github.com/kuangshp/gorm-plus/") {
		return false
	}
	file := filepathSlash(frame.File)
	for _, part := range l.skipPathParts {
		if part == "" {
			continue
		}
		if strings.Contains(file, filepathSlash(part)) {
			return false
		}
	}
	return true
}

func filepathSlash(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}
