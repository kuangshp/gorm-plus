package gormplus

import (
	"testing"
	"time"

	"gorm.io/gen/field"
	gormclause "gorm.io/gorm/clause"
)

func TestRepositoryOptionPublicUsage(t *testing.T) {
	name := field.NewString("", "name")
	status := field.NewInt("", "status")

	createOpt := Create().
		Omit(name).
		OnConflict(gormclause.OnConflict{UpdateAll: true}).
		Build()
	if resolved := ResolveCreateOptions([]CreateOption{createOpt}); len(resolved.OmitFields) != 1 || len(resolved.Clauses) != 1 {
		t.Fatalf("unexpected create options: %+v", resolved)
	}

	deleteOpt := Delete().
		Physical().
		Clauses(gormclause.Returning{}).
		Build()
	if resolved := ResolveDeleteOptions([]DeleteOption{deleteOpt}); !resolved.Physical || len(resolved.Clauses) != 1 {
		t.Fatalf("unexpected delete options: %+v", resolved)
	}

	queryOpt := QueryOpt().
		Where(status.Eq(1)).
		Clauses(gormclause.Locking{Strength: "UPDATE"}).
		WithDeleted().
		WithCache(time.Second).
		Build()
	if resolved := MergeQueryOptions(queryOpt); !resolved.Unscoped || len(resolved.Cond) != 1 || len(resolved.Clauses) != 1 {
		t.Fatalf("unexpected query options: %+v", resolved)
	}

	updateOpt := Update().
		Columns(name.Value("alice")).
		Clauses(gormclause.Returning{}).
		Build()
	if resolved := ResolveUpdateOptions([]UpdateOption{updateOpt}); len(resolved.Columns) != 1 || len(resolved.Clauses) != 1 {
		t.Fatalf("unexpected update options: %+v", resolved)
	}
}
