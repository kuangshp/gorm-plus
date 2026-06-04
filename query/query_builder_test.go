package query

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type builderTestModel struct {
	ID     int64
	Name   string
	Code   string
	Status int
}

func newDryRunBuilder(t *testing.T) IQueryBuilder {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return NewQuery[builderTestModel](db, context.Background())
}

func TestQueryBuilderWhereOrGroupUsesAndOutsideOrInside(t *testing.T) {
	sql := newDryRunBuilder(t).
		WhereIf(true, "status = ?", 1).
		WhereOrGroup(func(q IQueryBuilder) {
			q.Like("name", "acme").
				Like("code", "AC")
		}).
		ToSQL()

	if !strings.Contains(sql, "WHERE status = 1 AND (`name` LIKE \"%acme%\" OR `code` LIKE \"%AC%\")") {
		t.Fatalf("expected AND outside OR group, got SQL: %s", sql)
	}
}

func TestQueryBuilderOptionalOrLikesSkipEmptyValues(t *testing.T) {
	sql := newDryRunBuilder(t).
		WhereIf(true, "status = ?", 1).
		WhereOrGroup(func(q IQueryBuilder) {
			q.Like("name", "").
				Like("code", "AC")
		}).
		ToSQL()

	if !strings.Contains(sql, "WHERE status = 1 AND `code` LIKE \"%AC%\"") {
		t.Fatalf("expected non-empty LIKE to remain, got SQL: %s", sql)
	}
	if strings.Contains(sql, "`name` LIKE") || strings.Contains(sql, "%%") {
		t.Fatalf("expected empty LIKE to be skipped, got SQL: %s", sql)
	}
}

func TestQueryBuilderOrLikeAndSkippedGroups(t *testing.T) {
	sql := newDryRunBuilder(t).
		WhereIf(true, "status = ?", 1).
		OrLike("name", "acme").
		WhereGroup(nil).
		WhereGroupIf(false, func(q IQueryBuilder) {
			q.WhereIf(true, "code = ?", "AC")
		}).
		OrGroupIf(false, func(q IQueryBuilder) {
			q.WhereIf(true, "code = ?", "AC")
		}).
		ToSQL()

	if !strings.Contains(sql, "WHERE status = 1 OR `name` LIKE \"%acme%\"") {
		t.Fatalf("expected OR LIKE condition, got SQL: %s", sql)
	}
	if strings.Contains(sql, "code =") {
		t.Fatalf("expected skipped groups not to affect SQL, got SQL: %s", sql)
	}
}
