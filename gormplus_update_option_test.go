package gormplus

import (
	"testing"

	"gorm.io/gorm/clause"
)

func TestResolveUpdateOptions(t *testing.T) {
	opt := Update().Clauses(clause.Returning{}).Build()
	resolved := ResolveUpdateOptions([]UpdateOption{
		nil,
		WithUpdateClauses(clause.Locking{Strength: "UPDATE"}),
		opt,
	})

	if len(resolved.Clauses) != 2 {
		t.Fatalf("len(Clauses) = %d, want 2", len(resolved.Clauses))
	}
}
