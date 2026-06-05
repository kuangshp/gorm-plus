package query

import (
	"testing"

	gormclause "gorm.io/gorm/clause"
)

func TestResolveUpdateOptions(t *testing.T) {
	opts := ResolveUpdateOptions([]UpdateOption{
		nil,
		WithUpdateClauses(gormclause.Locking{Strength: "UPDATE"}),
		Update().Clauses(gormclause.Returning{}).Build(),
	})

	if len(opts.Clauses) != 2 {
		t.Fatalf("len(Clauses) = %d, want 2", len(opts.Clauses))
	}
}
