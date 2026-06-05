package query

import (
	"testing"

	"gorm.io/gen/field"
	gormclause "gorm.io/gorm/clause"
)

func TestResolveCreateOptions(t *testing.T) {
	name := field.NewString("", "name")
	opts := ResolveCreateOptions([]CreateOption{
		WithCreateOmit(name),
		WithCreateOnConflict(gormclause.OnConflict{UpdateAll: true}),
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
	code := field.NewString("", "code")
	opts := ResolveCreateOptions([]CreateOption{
		Create().
			Omit(name).
			WithOmit(code).
			OnConflict(gormclause.OnConflict{UpdateAll: true}).
			Build(),
	})

	if len(opts.OmitFields) != 2 {
		t.Fatalf("omit fields length = %d, want 2", len(opts.OmitFields))
	}
	if len(opts.Clauses) != 1 {
		t.Fatalf("clauses length = %d, want 1", len(opts.Clauses))
	}
}
