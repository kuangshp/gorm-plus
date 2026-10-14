package httpstatic

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestServeNewUploadsAndHTTPFileSemantics(t *testing.T) {
	dir := t.TempDir()
	server, err := (Config{Prefix: "/uploads", Directory: dir}).Open()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	request := func(method, name string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, name, nil)
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodGet, "/uploads/a.png", nil).Code; got != http.StatusNotFound {
		t.Fatalf("missing file status = %d", got)
	}
	image := "\x89PNG\r\n\x1a\nimage-content"
	if err := os.WriteFile(filepath.Join(dir, "a.png"), []byte(image), 0600); err != nil {
		t.Fatal(err)
	}
	w := request(http.MethodGet, "/uploads/a.png", nil)
	if w.Code != http.StatusOK || w.Body.String() != image || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("new upload was not immediately served: %d %v", w.Code, w.Header())
	}
	w = request(http.MethodHead, "/uploads/a.png", nil)
	if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Content-Length") != strconv.Itoa(len(image)) {
		t.Fatalf("HEAD response: %d %v", w.Code, w.Header())
	}
	w = request(http.MethodGet, "/uploads/a.png", map[string]string{"Range": "bytes=0-3"})
	if w.Code != http.StatusPartialContent || w.Body.String() != image[:4] {
		t.Fatalf("Range response: %d %q", w.Code, w.Body.String())
	}
	for _, path := range []string{"/uploads/", "/uploads-other/a.png", "/a.png"} {
		if got := request(http.MethodGet, path, nil).Code; got != http.StatusNotFound {
			t.Fatalf("unexpected file access at %s: %d", path, got)
		}
	}
	if got := request(http.MethodPost, "/uploads/a.png", nil).Code; got != http.StatusNotFound {
		t.Fatalf("POST must not act as an upload endpoint: %d", got)
	}
}

func TestStaticDirectoryBoundaries(t *testing.T) {
	dir := t.TempDir()
	public := filepath.Join(dir, "public")
	if err := os.MkdirAll(filepath.Join(public, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"secret.txt": "outside", "public/.env": "hidden", "public/nested/a.txt": "inside",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	server, err := (Config{Prefix: "/uploads/", Directory: public}).Open()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for _, path := range []string{"/uploads/.env", "/uploads/../secret.txt", "/uploads/nested/", "/uploads/"} {
		w := httptest.NewRecorder()
		server.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "outside") || strings.Contains(w.Body.String(), "hidden") {
			t.Fatalf("private path exposed: %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	if err := os.Symlink(filepath.Join(dir, "secret.txt"), filepath.Join(public, "link.txt")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/uploads/link.txt", nil))
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "outside") {
		t.Fatalf("outside symlink target exposed: %d %s", w.Code, w.Body.String())
	}
}

func TestStaticConfigRejectsInvalidPaths(t *testing.T) {
	for _, prefix := range []string{"", "/", "//", "uploads", "/uploads/../", "/uploads?x=1", "/uploads#x", "/uploads\\files"} {
		if server, err := (Config{Prefix: prefix, Directory: t.TempDir()}).Open(); err == nil {
			server.Close()
			t.Errorf("accepted invalid prefix %q", prefix)
		}
	}
	if _, err := (Config{Prefix: "/uploads"}).Open(); err == nil {
		t.Fatal("accepted empty directory")
	}
	if _, err := (Config{Prefix: "/uploads", Directory: filepath.Join(t.TempDir(), "missing")}).Open(); err == nil {
		t.Fatal("accepted missing directory")
	}
}
