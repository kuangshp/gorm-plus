package gormplus

import (
	"testing"

	"gorm.io/gen"
	"gorm.io/gen/field"
	gormclause "gorm.io/gorm/clause"
)

func TestResolveDeleteOptions(t *testing.T) {
	opts := ResolveDeleteOptions([]DeleteOption{WithPhysicalDelete()})
	if !opts.Physical {
		t.Fatal("expected physical delete option to be enabled")
	}
}

func TestDeleteBuilder(t *testing.T) {
	opts := ResolveDeleteOptions([]DeleteOption{
		Delete().WithPhysicalDelete().Build(),
	})
	if !opts.Physical {
		t.Fatal("expected builder to enable physical delete")
	}

	opts = ResolveDeleteOptions([]DeleteOption{
		Delete().Physical().Build(),
	})
	if !opts.Physical {
		t.Fatal("expected short builder alias to enable physical delete")
	}

	opts = ResolveDeleteOptions([]DeleteOption{
		Delete().Clauses(gormclause.Locking{Strength: "UPDATE"}).Build(),
		WithDeleteClauses(gormclause.Locking{Strength: "SHARE"}),
	})
	if len(opts.Clauses) != 2 {
		t.Fatalf("clauses length = %d, want 2", len(opts.Clauses))
	}
}

func TestSplitDeleteConditions(t *testing.T) {
	id := gen.Condition(field.NewInt("", "id").Eq(1))

	conditions, opts := SplitDeleteConditions(nil)
	if len(conditions) != 0 {
		t.Fatalf("conditions length = %d, want 0", len(conditions))
	}
	if opts.Physical {
		t.Fatal("physical delete should be disabled without option")
	}

	conditions, opts = SplitDeleteConditions([]gen.Condition{WithPhysicalDelete()})
	if len(conditions) != 0 {
		t.Fatalf("conditions length = %d, want 0", len(conditions))
	}
	if !opts.Physical {
		t.Fatal("expected physical delete option to be enabled")
	}

	conditions, opts = SplitDeleteConditions([]gen.Condition{Delete().WithPhysicalDelete().Build()})
	if len(conditions) != 0 {
		t.Fatalf("conditions length = %d, want 0", len(conditions))
	}
	if !opts.Physical {
		t.Fatal("expected builder option to enable physical delete")
	}

	conditions, opts = SplitDeleteConditions([]gen.Condition{
		Delete().
			Physical().
			Clauses(gormclause.Returning{}).
			Build(),
	})
	if len(conditions) != 0 {
		t.Fatalf("conditions length = %d, want 0", len(conditions))
	}
	if !opts.Physical {
		t.Fatal("expected builder option to enable physical delete")
	}
	if len(opts.Clauses) != 1 {
		t.Fatalf("clauses length = %d, want 1", len(opts.Clauses))
	}

	conditions, opts = SplitDeleteConditions([]gen.Condition{
		id,
		Delete().
			Physical().
			Clauses(gormclause.Returning{}).
			Build(),
	})
	if len(conditions) != 1 {
		t.Fatalf("conditions length = %d, want 1", len(conditions))
	}
	if !opts.Physical {
		t.Fatal("expected builder option to enable physical delete")
	}
	if len(opts.Clauses) != 1 {
		t.Fatalf("clauses length = %d, want 1", len(opts.Clauses))
	}
}
