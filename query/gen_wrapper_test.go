package query

import (
	"context"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

type wrapperTestDO struct {
	db *gorm.DB
}

type softDeleteWrapperModel struct {
	ID        int64
	DeletedAt gorm.DeletedAt
}

func (d *wrapperTestDO) UnderlyingDB() *gorm.DB {
	return d.db
}

func (d *wrapperTestDO) WithContext(ctx context.Context) *wrapperTestDO {
	return &wrapperTestDO{db: d.db.WithContext(ctx)}
}

func (d *wrapperTestDO) ReplaceDB(db *gorm.DB) {
	d.db = db
}

func TestGenWrapperOrderDefaultAppliesWhenNoExplicitOrder(t *testing.T) {
	w := &GenWrapper[*wrapperTestDO]{}
	w.OrderDefault(RawField("id DESC"))

	if len(w.orders) != 0 {
		t.Fatalf("expected no explicit orders, got %d", len(w.orders))
	}
	if len(w.defaultOrders) != 1 {
		t.Fatalf("expected one default order, got %d", len(w.defaultOrders))
	}
	if len(w.effectiveOrders()) != 1 {
		t.Fatalf("expected default order to be effective, got %d", len(w.effectiveOrders()))
	}
}

func TestGenWrapperOrderDefaultIgnoredWhenExplicitOrderComesAfter(t *testing.T) {
	w := &GenWrapper[*wrapperTestDO]{}
	w.OrderDefault(RawField("id DESC")).
		Order(RawField("created_at ASC"), RawField("name DESC"))

	if len(w.orders) != 2 {
		t.Fatalf("expected two explicit orders, got %d", len(w.orders))
	}
	if len(w.defaultOrders) != 1 {
		t.Fatalf("expected one stored default order, got %d", len(w.defaultOrders))
	}
	if len(w.effectiveOrders()) != 2 {
		t.Fatalf("expected explicit orders to be effective, got %d", len(w.effectiveOrders()))
	}
}

func TestGenWrapperOrderDefaultIgnoredWhenExplicitOrderComesBefore(t *testing.T) {
	w := &GenWrapper[*wrapperTestDO]{}
	w.Order(RawField("created_at ASC"), RawField("name DESC")).
		OrderDefault(RawField("id DESC"))

	if len(w.orders) != 2 {
		t.Fatalf("expected two explicit orders, got %d", len(w.orders))
	}
	if len(w.defaultOrders) != 0 {
		t.Fatalf("expected no stored default order, got %d", len(w.defaultOrders))
	}
	if len(w.effectiveOrders()) != 2 {
		t.Fatalf("expected explicit orders to be effective, got %d", len(w.effectiveOrders()))
	}
}

func TestGenWrapperNilInputsAreSkipped(t *testing.T) {
	w := &GenWrapper[*wrapperTestDO]{group: newCondGroup()}

	w.Like(nil, "admin").
		LLike(nil, "admin").
		RLike(nil, "admin").
		Like(RawField("name"), "").
		LLike(RawField("name"), "").
		RLike(RawField("name"), "").
		OrLike(RawField("name"), "").
		OrLLike(RawField("name"), "").
		OrRLike(RawField("name"), "").
		BetweenIfNotZero(nil, 1, 2).
		WhereOrGroup(nil).
		WhereOrGroupIf(true, nil).
		WhereGroupFn(nil).
		OrGroupFn(nil)

	if !w.group.isEmpty() {
		t.Fatalf("expected nil inputs to be skipped, got %d conditions", len(w.group.conds))
	}
}

func TestGenWrapperWhereOrGroupUsesAndOutsideOrInside(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	w := &GenWrapper[*wrapperTestDO]{
		do:    &wrapperTestDO{db: db.Table("companies")},
		ctx:   context.Background(),
		group: newCondGroup(),
	}

	sql := w.Where(RawField("status = ?", 1)).
		WhereOrGroup(
			RawField("name LIKE ?", "%acme%"),
			RawField("code LIKE ?", "%AC%"),
		).
		ToSQL()

	if !strings.Contains(sql, "WHERE status = 1 AND (name LIKE \"%acme%\" OR code LIKE \"%AC%\")") {
		t.Fatalf("expected AND outside OR group, got SQL: %s", sql)
	}
}

func TestGenWrapperWhereGroupFnCanBuildOptionalOrLikes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	w := &GenWrapper[*wrapperTestDO]{
		do:    &wrapperTestDO{db: db.Table("companies")},
		ctx:   context.Background(),
		group: newCondGroup(),
	}

	name := field.NewString("", "name")
	code := field.NewString("", "code")

	sql := w.Where(RawField("status = ?", 1)).
		WhereGroupFn(func(g IGenWrapper[*wrapperTestDO]) {
			g.Like(name, "").
				OrLike(code, "AC")
		}).
		ToSQL()

	if !strings.Contains(sql, "WHERE status = 1 AND code LIKE \"%AC%\"") {
		t.Fatalf("expected empty LIKE to be skipped and non-empty OR LIKE to remain, got SQL: %s", sql)
	}
	if strings.Contains(sql, "name LIKE") || strings.Contains(sql, "%%") {
		t.Fatalf("expected empty LIKE to be skipped, got SQL: %s", sql)
	}
}

func TestGenWrapperJoinSoftDeleteHelpers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	w := &GenWrapper[*wrapperTestDO]{
		do:    &wrapperTestDO{db: db.Table("companies")},
		ctx:   context.Background(),
		group: newCondGroup(),
	}

	sql := w.WhereNotDeleted("d").
		WhereDeleted("u").
		ToSQL()

	if !strings.Contains(sql, "d.deleted_at IS NULL") {
		t.Fatalf("expected joined table not-deleted condition, got SQL: %s", sql)
	}
	if !strings.Contains(sql, "u.deleted_at IS NOT NULL") {
		t.Fatalf("expected joined table deleted condition, got SQL: %s", sql)
	}
}

func TestGenWrapperAsUsesAliasForSoftDelete(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	w := &GenWrapper[*wrapperTestDO]{
		do:    &wrapperTestDO{db: db.Model(&softDeleteWrapperModel{})},
		ctx:   context.Background(),
		group: newCondGroup(),
	}

	sql := w.As("a").ToSQL()
	if !strings.Contains(sql, "a.deleted_at IS NULL") {
		t.Fatalf("expected alias deleted_at condition, got SQL: %s", sql)
	}
	if strings.Contains(sql, "soft_delete_wrapper_models.deleted_at IS NULL") {
		t.Fatalf("expected original table deleted_at condition to be disabled, got SQL: %s", sql)
	}
}

func TestGenWrapperAsWithDeletedSkipsAliasSoftDelete(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	w := &GenWrapper[*wrapperTestDO]{
		do:    &wrapperTestDO{db: db.Model(&softDeleteWrapperModel{})},
		ctx:   context.Background(),
		group: newCondGroup(),
	}

	sql := w.As("a").WithDeleted().ToSQL()
	if strings.Contains(sql, "deleted_at IS NULL") {
		t.Fatalf("expected deleted_at condition to be skipped, got SQL: %s", sql)
	}
}
