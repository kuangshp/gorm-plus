package dal

import (
	"context"
	"log"
	"time"
)

////////////////////////////////////////////////////////////////////////////////
/////////////////////////////////// Debug //////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func debugLog(
	d *DAL,
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
		"[DAL] file=%s cost=%s err=%v\nsql=\n%s\nargs=%+v\n",
		sqlFile, cost, err, sqlText, args,
	)
}

func debugWarnEmpty(d *DAL, sqlFile string) {
	if !d.opts.debug {
		return
	}
	log.Printf(
		"[DAL][WARN] 返回零行，请确认 SQL 路径和查询条件是否正确 file=%s\n",
		sqlFile,
	)
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
