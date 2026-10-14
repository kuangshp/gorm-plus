// Package logrotate 提供按天或按小时写入、延迟压缩和过期清理的日志文件 Writer。
// 仅依赖标准库，可供 logx、slog 和 log 等日志库复用。
package logrotate

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config 配置文件日志；未启用时由项目沿用原有日志输出方式。
type Config struct {
	Enabled bool `json:",default=false"`
	// Path 相对路径以进程工作目录为基准；同一目录应只由一个进程管理。
	Path string `json:",default=logs"`
	// Rotation 可选 daily、hourly，使用进程本地时区。
	Rotation string `json:",default=daily,options=[daily,hourly]"`
	// CompressAfterDays 在日志所属时段结束后超过指定天数压缩为 .gz；0 禁用。
	CompressAfterDays int `json:",default=3"`
	// KeepDays 在日志所属时段结束后超过指定天数删除，包括 .gz；0 永久保留。
	KeepDays int `json:",default=30"`
	// CheckIntervalMinutes 定期检查归档文件，启动时也会检查。
	CheckIntervalMinutes int `json:",default=60"`
}

// Writer 串行追加日志，每次写入按当前时间选择文件；后台独立清理归档文件。
type Writer struct {
	config        Config
	mu            sync.Mutex
	file          *os.File
	filename      string
	closed        bool
	stop          chan struct{}
	done          chan struct{}
	maintenanceMu sync.Mutex
	// now 用于测试跨时段及保留期限，不依赖实际等待。
	now func() time.Time
}

var _ io.WriteCloser = (*Writer)(nil)

// Open 校验配置、创建目录及当前日志文件，并启动归档检查。
// Enabled 由调用方判断；Open 不修改全局日志器。
func (c Config) Open() (*Writer, error) {
	if strings.TrimSpace(c.Path) == "" {
		return nil, errors.New("logrotate: Path must not be empty")
	}
	if c.Rotation != "daily" && c.Rotation != "hourly" {
		return nil, errors.New("logrotate: Rotation must be daily or hourly")
	}
	if c.CompressAfterDays < 0 || c.CompressAfterDays > 36500 || c.KeepDays < 0 || c.KeepDays > 36500 {
		return nil, errors.New("logrotate: retention days must be between 0 and 36500")
	}
	if c.CheckIntervalMinutes < 1 || c.CheckIntervalMinutes > 1440 {
		return nil, errors.New("logrotate: CheckIntervalMinutes must be between 1 and 1440")
	}
	if err := os.MkdirAll(c.Path, 0750); err != nil {
		return nil, fmt.Errorf("logrotate: create directory: %w", err)
	}
	w := &Writer{config: c, stop: make(chan struct{}), done: make(chan struct{}), now: time.Now}
	if err := w.openFile(w.now()); err != nil {
		return nil, err
	}
	go w.maintain()
	return w, nil
}

// Write 以追加方式写入日志，跨时段或重启后不会覆盖已有文件。
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, os.ErrClosed
	}
	if err := w.openFile(w.now()); err != nil {
		return 0, err
	}
	return w.file.Write(p)
}

// Close 停止后台检查并关闭文件，可重复调用。
func (w *Writer) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		<-w.done
		return nil
	}
	w.closed = true
	close(w.stop)
	err := w.file.Close()
	w.mu.Unlock()
	<-w.done
	return err
}

func (w *Writer) openFile(now time.Time) error {
	layout := "2006-01-02"
	if w.config.Rotation == "hourly" {
		layout = "2006-01-02-15"
	}
	filename := filepath.Join(w.config.Path, "app-"+now.Format(layout)+".log")
	if filename == w.filename {
		return nil
	}
	// 避免意外覆盖已存在的归档；不跟随日志文件的符号链接。
	if _, err := os.Lstat(filename + ".gz"); err == nil {
		return fmt.Errorf("logrotate: period already archived: %s", filename)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if info, err := os.Lstat(filename); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("logrotate: not a regular file: %s", filename)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return fmt.Errorf("logrotate: open log file: %w", err)
	}
	if w.file != nil {
		err = w.file.Close()
	}
	w.file, w.filename = file, filename
	return err
}

func (w *Writer) maintain() {
	defer close(w.done)
	ticker := time.NewTicker(time.Duration(w.config.CheckIntervalMinutes) * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		default:
		}
		if err := w.cleanup(w.now()); err != nil {
			// 直接写 stderr，避免通过正在管理的日志器递归记录错误。
			log.New(os.Stderr, "logrotate: ", log.LstdFlags).Print(err)
		}
		select {
		case <-w.stop:
			return
		case <-ticker.C:
		}
	}
}

func (w *Writer) cleanup(now time.Time) error {
	w.maintenanceMu.Lock()
	defer w.maintenanceMu.Unlock()
	// 即使当前没有新日志，也切换到当前时段，允许旧文件正常归档。
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	err := w.openFile(now)
	w.mu.Unlock()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(w.config.Path)
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		end, compressed, ok := periodEnd(entry.Name(), now.Location())
		if !ok || !end.Before(now) {
			continue
		}
		filename := filepath.Join(w.config.Path, entry.Name())
		w.mu.Lock()
		active := filename == w.filename
		closed := w.closed
		w.mu.Unlock()
		if closed {
			break
		}
		if active {
			continue
		}
		// 保留期限按原日志时段计算，压缩不会重置期限；删除优先于压缩。
		switch {
		case expired(end, now, w.config.KeepDays):
			err = os.Remove(filename)
		case !compressed && expired(end, now, w.config.CompressAfterDays):
			err = compressFile(filename)
		default:
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", filename, err))
		}
	}
	return errors.Join(failures...)
}

func expired(end, now time.Time, days int) bool {
	return days > 0 && now.After(end.Add(time.Duration(days)*24*time.Hour))
}

// periodEnd 仅识别本包生成的日/小时文件；切换分割方式后旧文件仍受管理。
func periodEnd(name string, location *time.Location) (time.Time, bool, bool) {
	compressed := strings.HasSuffix(name, ".gz")
	base := strings.TrimSuffix(name, ".gz")
	if !strings.HasPrefix(base, "app-") || !strings.HasSuffix(base, ".log") {
		return time.Time{}, false, false
	}
	stamp := strings.TrimSuffix(strings.TrimPrefix(base, "app-"), ".log")
	for _, layout := range []string{"2006-01-02", "2006-01-02-15"} {
		start, err := time.ParseInLocation(layout, stamp, location)
		if err != nil || start.Format(layout) != stamp {
			continue
		}
		if layout == "2006-01-02" {
			return start.AddDate(0, 0, 1), compressed, true
		}
		return start.Add(time.Hour), compressed, true
	}
	return time.Time{}, false, false
}

func compressFile(filename string) error {
	source, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer source.Close()
	archive, err := os.CreateTemp(filepath.Dir(filename), ".logrotate-*.tmp")
	if err != nil {
		return err
	}
	temporary := archive.Name()
	defer func() {
		_ = archive.Close()
		_ = os.Remove(temporary)
	}()
	zipper := gzip.NewWriter(archive)
	_, copyErr := io.Copy(zipper, source)
	if err := errors.Join(copyErr, zipper.Close()); err != nil {
		return err
	}
	if err := archive.Sync(); err != nil {
		return err
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := source.Close(); err != nil {
		return err
	}
	// 完整压缩成功后才发布归档。硬链接原子创建且不会覆盖已存在的归档。
	if err := os.Link(temporary, filename+".gz"); err != nil {
		return err
	}
	return os.Remove(filename)
}
