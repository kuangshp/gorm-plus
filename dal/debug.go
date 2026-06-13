package dal

import (
	"context"
	"log"
	"runtime"
	"strings"
	"time"

	"github.com/kuangshp/gorm-plus/internal/sqlcaller"
)

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// Debug //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func debugLog(
	d *DAL,
	ctx context.Context,
	sqlFile string,
	sqlText string,
	args []any,
	cost time.Duration,
	err error,
) {
	if !d.opts.debug {
		return
	}
	log.Printf(
		"[DAL] caller=%s file=%s cost=%s err=%v\nsql=\n%s\nargs=%+v\n",
		dalCaller(ctx), sqlFile, cost, err, sqlText, args,
	)
}

func debugWarnEmpty(d *DAL, ctx context.Context, sqlFile string) {
	if !d.opts.debug {
		return
	}
	log.Printf(
		"[DAL][WARN] caller=%s 返回零行，请确认 SQL 路径和查询条件是否正确 file=%s\n",
		dalCaller(ctx), sqlFile,
	)
}

func dalCaller(ctx context.Context) string {
	if caller, ok := sqlcaller.FromContext(ctx); ok {
		return caller.String()
	}
	if caller, ok := lookupDALStackCaller(); ok {
		return caller.String()
	}
	return ""
}

func lookupDALStackCaller() (sqlcaller.Caller, bool) {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if acceptDALFrame(frame) {
			return sqlcaller.Caller{File: frame.File, Line: frame.Line}, true
		}
		if !more {
			break
		}
	}
	return sqlcaller.Caller{}, false
}

func acceptDALFrame(frame runtime.Frame) bool {
	if frame.File == "" || strings.HasSuffix(frame.File, "_test.go") || strings.HasSuffix(frame.File, ".gen.go") {
		return false
	}
	if strings.HasPrefix(frame.Function, "github.com/kuangshp/gorm-plus.") ||
		strings.HasPrefix(frame.Function, "github.com/kuangshp/gorm-plus/") {
		return false
	}
	file := strings.ReplaceAll(frame.File, "\\", "/")
	for _, part := range []string{"/repository/", "/dao/"} {
		if strings.Contains(file, part) {
			return false
		}
	}
	return true
}

func runBeforeHooks(d *DAL, ctx context.Context, sqlFile string, args []any) {
	for _, h := range d.opts.hooks {
		h.Before(ctx, sqlFile, args)
	}
}

func runAfterHooks(d *DAL, ctx context.Context, sqlFile string, args []any, cost time.Duration, err error) {
	for _, h := range d.opts.hooks {
		h.After(ctx, sqlFile, args, cost, err)
	}
}
