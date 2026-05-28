package query

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

type wrapperTestDO struct {
	db *gorm.DB
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
		BetweenIfNotZero(nil, 1, 2).
		WhereGroupFn(nil).
		OrGroupFn(nil)

	if !w.group.isEmpty() {
		t.Fatalf("expected nil inputs to be skipped, got %d conditions", len(w.group.conds))
	}
}
