package gormplus

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm/logger"
)

type bufferLogWriter struct {
	buf *bytes.Buffer
}

func (w bufferLogWriter) Printf(format string, args ...interface{}) {
	fmt.Fprintf(w.buf, format, args...)
}

func TestSQLCallerLoggerUsesContextCaller(t *testing.T) {
	var buf bytes.Buffer
	l := NewSQLCallerLogger(bufferLogWriter{buf: &buf}, logger.Config{
		LogLevel: logger.Info,
		Colorful: false,
	}, WithSQLCallerStackLookup(false))

	ctx := ContextWithSQLCaller(context.Background(), SQLCaller{
		File: "/app/internal/logic/user_logic.go",
		Line: 42,
	})
	l.Trace(ctx, time.Now(), func() (string, int64) {
		return "SELECT * FROM users WHERE id = 1", 1
	}, nil)

	out := buf.String()
	if !strings.Contains(out, "/app/internal/logic/user_logic.go:42") {
		t.Fatalf("expected context caller in sql log, got %q", out)
	}
	if !strings.Contains(out, "SELECT * FROM users WHERE id = 1") {
		t.Fatalf("expected sql in log, got %q", out)
	}
}

func TestWithSQLCallerRecordsCaller(t *testing.T) {
	ctx := recordSQLCallerForTest(context.Background())
	caller, ok := SQLCallerFromContext(ctx)
	if !ok {
		t.Fatal("expected sql caller in context")
	}
	if !strings.HasSuffix(caller.File, "gormplus_sql_logger_test.go") {
		t.Fatalf("expected test file caller, got %s", caller.File)
	}
}

func TestSQLCallerLoggerAcceptFrameSkipsRepository(t *testing.T) {
	l := NewSQLCallerLogger(bufferLogWriter{buf: &bytes.Buffer{}}, logger.Config{}).(*sqlCallerLogger)

	if l.acceptFrame(runtimeFrame("/app/internal/repository/user_repo.go", 10)) {
		t.Fatal("expected repository frame to be skipped")
	}
	if !l.acceptFrame(runtimeFrame("/app/internal/logic/user_logic.go", 20)) {
		t.Fatal("expected logic frame to be accepted")
	}
}

func recordSQLCallerForTest(ctx context.Context) context.Context {
	return WithSQLCaller(ctx)
}

func runtimeFrame(file string, line int) runtime.Frame {
	return runtime.Frame{File: file, Line: line}
}
