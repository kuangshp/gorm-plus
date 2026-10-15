package datasource

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDialectorOnlySlaves(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager()
	t.Cleanup(func() { manager.Close() })
	manager.Register("main", GroupConfig{
		Master: NodeConfig{Dialector: sqlite.Open(filepath.Join(dir, "master.db"))},
		Slaves: []NodeConfig{
			{}, // 空节点仍跳过。
			{Dialector: sqlite.Open(filepath.Join(dir, "slave1.db"))},
			{Dialector: sqlite.Open(filepath.Join(dir, "slave2.db")), DSN: "ignored-invalid-dsn"},
		},
	})
	if group := manager.groups["main"]; len(group.slaves) != 2 || group.master.db != nil || group.slaves[0].db != nil {
		t.Fatal("registration must retain dialector-only slaves and remain lazy")
	}
	master, err := manager.Write("main")
	if err != nil {
		t.Fatal(err)
	}
	checkNode := func(db *gorm.DB, want string) {
		t.Helper()
		var databases []struct{ File string }
		if err := db.Raw("PRAGMA database_list").Scan(&databases).Error; err != nil || len(databases) != 1 || filepath.Base(databases[0].File) != want {
			t.Fatalf("database = %+v, %v; want %s", databases, err, want)
		}
	}
	checkNode(master, "master.db")
	seen := map[string]int{}
	ctx := WithRead(context.Background())
	for i := 0; i < 4; i++ {
		db, err := manager.Auto(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if db.Statement.Context != ctx {
			t.Fatal("lost context")
		}
		var databases []struct{ File string }
		if err := db.Raw("PRAGMA database_list").Scan(&databases).Error; err != nil {
			t.Fatal(err)
		}
		seen[filepath.Base(databases[0].File)]++
	}
	if len(seen) != 2 || seen["slave1.db"] != 2 || seen["slave2.db"] != 2 {
		t.Fatalf("read round robin = %v", seen)
	}
	write, err := manager.Auto(WithWrite(ctx))
	if err != nil {
		t.Fatal(err)
	}
	checkNode(write, "master.db")
}

func TestEmptySlavesFallbackToMaster(t *testing.T) {
	manager := NewManager()
	t.Cleanup(func() { manager.Close() })
	manager.Register("main", GroupConfig{Master: NodeConfig{Dialector: sqlite.Open(":memory:")}, Slaves: []NodeConfig{{}}})
	master, err := manager.Write("main")
	if err != nil {
		t.Fatal(err)
	}
	read, err := manager.Read("main")
	if err != nil || read != master {
		t.Fatalf("expected master fallback, got %p vs %p, %v", read, master, err)
	}
}
