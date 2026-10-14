package logrotate

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{Path: t.TempDir(), Rotation: "daily", CompressAfterDays: 3, KeepDays: 30, CheckIntervalMinutes: 60}
}

func testWriter(t *testing.T, config Config, now *time.Time) *Writer {
	t.Helper()
	w := &Writer{config: config, now: func() time.Time { return *now }}
	t.Cleanup(func() {
		if w.file != nil {
			_ = w.file.Close()
		}
	})
	return w
}

func TestRotationAndRestartAppend(t *testing.T) {
	for _, rotation := range []string{"daily", "hourly"} {
		t.Run(rotation, func(t *testing.T) {
			config := testConfig(t)
			config.Rotation = rotation
			hour := 23
			if rotation == "hourly" {
				hour = 15 // 在同一天内跨小时，避免仅验证了跨天行为。
			}
			now := time.Date(2026, 10, 15, hour, 59, 0, 0, time.FixedZone("CST", 8*60*60))
			w := testWriter(t, config, &now)
			if _, err := w.Write([]byte("first\n")); err != nil {
				t.Fatal(err)
			}
			firstFile := w.filename
			// 同时验证同一时段重启不会覆盖日志。
			restarted := testWriter(t, config, &now)
			if _, err := restarted.Write([]byte("restart\n")); err != nil {
				t.Fatal(err)
			}
			now = now.Add(2 * time.Minute)
			if _, err := w.Write([]byte("next\n")); err != nil {
				t.Fatal(err)
			}
			if firstFile == w.filename {
				t.Fatal("did not rotate at period boundary")
			}
			first, err := os.ReadFile(firstFile)
			if err != nil || string(first) != "first\nrestart\n" {
				t.Fatalf("previous logs lost: %q, %v", first, err)
			}
			next, err := os.ReadFile(w.filename)
			if err != nil || string(next) != "next\n" {
				t.Fatalf("new period logs: %q, %v", next, err)
			}
		})
	}
}

func TestArchiveAgeAndScope(t *testing.T) {
	c := testConfig(t)
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	w := testWriter(t, c, &now)
	for _, name := range []string{
		"app-2026-10-15.log", "app-2026-10-12.log", "app-2026-10-11.log",
		"app-2026-10-11-09.log", "app-2026-09-01.log", "app-2026-09-02.log.gz",
		"business.log", "app-invalid.log", "app-2026-10-16.log",
	} {
		if err := os.WriteFile(filepath.Join(c.Path, name), []byte("log contents\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.log")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(c.Path, "app-2026-08-01.log")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(c.Path, "app-2026-08-02.log"), 0700); err != nil {
		t.Fatal(err)
	}
	// “超过 3 天”不包含时段结束后刚好 72 小时的边界。
	boundary := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	if err := w.cleanup(boundary); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.Path, "app-2026-10-11.log")); err != nil {
		t.Fatalf("log compressed before exceeding threshold: %v", err)
	}
	if err := w.cleanup(now); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app-2026-10-11.log", "app-2026-10-11-09.log"} {
		path := filepath.Join(c.Path, name)
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("compressed source still exists: %s, %v", name, err)
		}
		file, err := os.Open(path + ".gz")
		if err != nil {
			t.Fatal(err)
		}
		zipper, err := gzip.NewReader(file)
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(zipper)
		_ = zipper.Close()
		_ = file.Close()
		if err != nil || string(contents) != "log contents\n" {
			t.Fatalf("archive contents: %q, %v", contents, err)
		}
	}
	for _, name := range []string{"app-2026-09-01.log", "app-2026-09-02.log.gz"} {
		if _, err := os.Stat(filepath.Join(c.Path, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expired archive still exists: %s, %v", name, err)
		}
	}
	for _, name := range []string{"app-2026-10-15.log", "app-2026-10-12.log", "business.log", "app-invalid.log", "app-2026-10-16.log", "app-2026-08-01.log", "app-2026-08-02.log"} {
		if _, err := os.Lstat(filepath.Join(c.Path, name)); err != nil {
			t.Fatalf("unrelated or recent file changed: %s, %v", name, err)
		}
	}
	// 改变归档的 mtime 不延长保留期限。
	archive := filepath.Join(c.Path, "app-2026-10-11.log.gz")
	now = now.Add(35 * 24 * time.Hour)
	if err := os.Chtimes(archive, now, now); err != nil {
		t.Fatal(err)
	}
	if err := w.cleanup(now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(archive); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compression reset retention: %v", err)
	}
}

func TestCleanupRotatesIdleWriterAndDisabledRetention(t *testing.T) {
	c := testConfig(t)
	c.CompressAfterDays, c.KeepDays = 0, 0
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	w := testWriter(t, c, &now)
	if _, err := w.Write([]byte("idle\n")); err != nil {
		t.Fatal(err)
	}
	previous := w.filename
	now = now.Add(45 * 24 * time.Hour)
	if err := w.cleanup(now); err != nil {
		t.Fatal(err)
	}
	if previous == w.filename {
		t.Fatal("idle writer was not rotated")
	}
	if contents, err := os.ReadFile(previous); err != nil || string(contents) != "idle\n" {
		t.Fatalf("disabled retention changed logs: %q, %v", contents, err)
	}
	c.CompressAfterDays = 3
	w.config = c
	if err := w.cleanup(now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(previous + ".gz"); err != nil {
		t.Fatal("idle log was not compressed:", err)
	}
}

func TestCompressionFailurePreservesSource(t *testing.T) {
	c := testConfig(t)
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	w := testWriter(t, c, &now)
	name := filepath.Join(c.Path, "app-2026-10-01.log")
	if err := os.WriteFile(name, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name+".gz", []byte("existing archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := w.cleanup(now); err == nil {
		t.Fatal("archive collision did not fail")
	}
	for path, expected := range map[string]string{name: "source", name + ".gz": "existing archive"} {
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != expected {
			t.Fatalf("file lost on compression failure: %s %q %v", path, contents, err)
		}
	}
	entries, err := os.ReadDir(c.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatal("temporary archive left after failure")
		}
	}
}

func TestConcurrentWriteAndClose(t *testing.T) {
	w, err := testConfig(t).Open()
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				if _, err := fmt.Fprintln(w, "entry"); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	workers.Wait()
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := w.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	contents, err := os.ReadFile(w.filename)
	if err != nil || strings.Count(string(contents), "entry\n") != 800 {
		t.Fatalf("concurrent logs lost: %v", err)
	}
	if _, err := w.Write([]byte("closed")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.Path = " " },
		func(c *Config) { c.Rotation = "size" },
		func(c *Config) { c.CompressAfterDays = -1 },
		func(c *Config) { c.KeepDays = 36501 },
		func(c *Config) { c.CheckIntervalMinutes = 0 },
	} {
		c := testConfig(t)
		change(&c)
		if w, err := c.Open(); err == nil {
			_ = w.Close()
			t.Fatal("invalid configuration accepted")
		}
	}
}
