package query

import (
	"testing"

	"gorm.io/gen"
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
}

func TestSplitDeleteConditions(t *testing.T) {
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
}
