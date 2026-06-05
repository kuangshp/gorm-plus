package gormplus

import (
	"testing"

	"gorm.io/gen/field"
	"gorm.io/gorm/clause"
)

func TestResolveCreateOptions(t *testing.T) {
	name := field.NewString("", "name")
	opts := ResolveCreateOptions([]CreateOption{
		WithCreateOmit(name),
		WithCreateOnConflict(clause.OnConflict{UpdateAll: true}),
	})

	if len(opts.OmitFields) != 1 {
		t.Fatalf("omit fields length = %d, want 1", len(opts.OmitFields))
	}
	if len(opts.Clauses) != 1 {
		t.Fatalf("clauses length = %d, want 1", len(opts.Clauses))
	}
}

func TestCreateBuilder(t *testing.T) {
	name := field.NewString("", "name")
	opts := ResolveCreateOptions([]CreateOption{
		Create().
			WithOmit(name).
			WithOnConflict(clause.OnConflict{UpdateAll: true}).
			Build(),
	})

	if len(opts.OmitFields) != 1 {
		t.Fatalf("omit fields length = %d, want 1", len(opts.OmitFields))
	}
	if len(opts.Clauses) != 1 {
		t.Fatalf("clauses length = %d, want 1", len(opts.Clauses))
	}
}
