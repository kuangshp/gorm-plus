package query

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormclause "gorm.io/gorm/clause"
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

func TestQueryBuilderJoinColumnHelpersQuoteTableAlias(t *testing.T) {
	sql := newDryRunBuilder(t).
		Like("d.name", "sales").
		BetweenIfNotZero("d.created_at", 1, 2).
		ToSQL()

	if !strings.Contains(sql, "`d`.`name` LIKE \"%sales%\"") {
		t.Fatalf("expected LIKE to keep join table alias, got SQL: %s", sql)
	}
	if !strings.Contains(sql, "`d`.`created_at` BETWEEN 1 AND 2") {
		t.Fatalf("expected BETWEEN to keep join table alias, got SQL: %s", sql)
	}
	if strings.Contains(sql, "`d.name`") || strings.Contains(sql, "`d.created_at`") {
		t.Fatalf("expected dotted columns to be quoted by segment, got SQL: %s", sql)
	}
}

func TestQueryBuilderWhereIfKeepsExplicitJoinTableAlias(t *testing.T) {
	sql := newDryRunBuilder(t).
		WhereIf(true, "d.status = ?", 1).
		WhereIf(true, "d.created_at BETWEEN ? AND ?", 1, 2).
		ToSQL()

	if !strings.Contains(sql, "d.status = 1") {
		t.Fatalf("expected WhereIf to keep explicit join table alias, got SQL: %s", sql)
	}
	if !strings.Contains(sql, "d.created_at BETWEEN 1 AND 2") {
		t.Fatalf("expected WhereIf BETWEEN to keep explicit join table alias, got SQL: %s", sql)
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

func TestQueryBuilderJoinSoftDeleteHelpers(t *testing.T) {
	sql := newDryRunBuilder(t).
		WhereIf(true, "status = ?", 1).
		WhereNotDeleted("d").
		WhereDeleted("u").
		ToSQL()

	if !strings.Contains(sql, "d.deleted_at IS NULL") {
		t.Fatalf("expected joined table not-deleted condition, got SQL: %s", sql)
	}
	if !strings.Contains(sql, "u.deleted_at IS NOT NULL") {
		t.Fatalf("expected joined table deleted condition, got SQL: %s", sql)
	}
}

func TestQueryOptionWithDeleted(t *testing.T) {
	opt := MergeQueryOptions(Query().WithDeleted().Build())
	if !opt.Unscoped {
		t.Fatal("expected WithDeleted to enable unscoped query")
	}

	opt = MergeQueryOptions(Query().WithUnscoped().Build())
	if !opt.Unscoped {
		t.Fatal("expected WithUnscoped to enable unscoped query")
	}
}

func TestQueryOptionClauses(t *testing.T) {
	opt := MergeQueryOptions(
		Query().Clauses(gormclause.Locking{Strength: "UPDATE"}).Build(),
		Query().WithClauses(gormclause.Locking{Strength: "SHARE"}).Build(),
	)
	if len(opt.Clauses) != 2 {
		t.Fatalf("clauses length = %d, want 2", len(opt.Clauses))
	}
}

func TestQueryBuilderClauses(t *testing.T) {
	db := newDryRunBuilder(t).
		Clauses(gormclause.Locking{Strength: "UPDATE"}).
		Build()

	if _, ok := db.Statement.Clauses["FOR"]; !ok {
		t.Fatalf("expected locking clause to be applied, clauses=%v", db.Statement.Clauses)
	}
}

func TestWithQueryOptionArgsIncludesUnscoped(t *testing.T) {
	args := withQueryOptionArgs(map[string]any{"id": int64(1)}, Query().WithDeleted().Build())
	if args["id"] != int64(1) {
		t.Fatalf("id arg = %v, want 1", args["id"])
	}
	if args["__unscoped"] != true {
		t.Fatalf("__unscoped arg = %v, want true", args["__unscoped"])
	}
}
