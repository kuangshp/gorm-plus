package swagger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuangshp/gorm-plus/httpswagger"
)

func writeFixture(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDisabledDoesNotRequireAPIOrGoctl(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "etc/app.yaml", "SwaggerConfig:\n  Enabled: false\n")
	writeFixture(t, dir, "swagger/swagger.json", `{"old":true}`)
	if _, err := Generate(context.Background(), Options{ConfigFile: "etc/app.yaml", Dir: dir, Executable: "/does-not-exist"}); err != nil {
		t.Fatal(err)
	}
	document, err := os.ReadFile(filepath.Join(dir, "swagger/swagger.json"))
	if err != nil || string(document) != httpswagger.Placeholder {
		t.Fatalf("disabled build retained an old document: %s, %v", document, err)
	}
}

func TestGenerateAndInvalidateOldDocumentOnFailure(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "etc/app.yaml", "swaggerconfig:\n  enabled: true\n  path: /docs\n  apifile: desc/main.api\nResponseConfig:\n  Enabled: true\n")
	writeFixture(t, dir, "desc/main.api", "syntax = \"v1\"")
	executable := filepath.Join(t.TempDir(), "fake goctl")
	script := `#!/bin/sh
while [ "$#" -gt 0 ]; do
 if [ "$1" = "--dir" ]; then shift; output="$1"; fi
 shift
done
printf '%s' '{"swagger":"2.0","info":{"title":"fixture","version":"1"},"paths":{}}' > "$output/swagger.json"
`
	if err := os.WriteFile(executable, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	options := Options{ConfigFile: "etc/app.yaml", Dir: dir, Executable: executable}
	if output, err := Generate(context.Background(), options); err != nil {
		t.Fatalf("Generate: %v\n%s", err, output)
	}
	document, _ := os.ReadFile(filepath.Join(dir, "swagger/swagger.json"))
	if strings.Contains(string(document), "placeholder") || !strings.Contains(string(document), "fixture") {
		t.Fatalf("document was not generated: %s", document)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(context.Background(), options); err == nil {
		t.Fatal("goctl failure ignored")
	}
	document, _ = os.ReadFile(filepath.Join(dir, "swagger/swagger.json"))
	if string(document) != httpswagger.Placeholder {
		t.Fatal("failed generation left the previous document active")
	}
}

func TestAPISelectionAndConfigErrors(t *testing.T) {
	dir := t.TempDir()
	options := Options{ConfigFile: "app.yaml", Dir: dir, Executable: "/does-not-exist"}
	if _, err := Generate(nil, options); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := Generate(context.Background(), Options{}); err == nil {
		t.Fatal("missing config accepted")
	}
	for _, config := range []string{
		"SwaggerConfig: [", "SwaggerConfig:\n  Enabled: true\n  Path: /\n",
		"SwaggerConfig:\n  Enabled: true\n  Enabled: false\n",
		"SwaggerConfig:\n  Enabled: true\n  ApiFile: absent.api\n",
	} {
		writeFixture(t, dir, "app.yaml", config)
		if _, err := Generate(context.Background(), options); err == nil {
			t.Errorf("invalid config accepted: %s", config)
		}
	}
	writeFixture(t, dir, "app.yaml", "SwaggerConfig:\n  Enabled: true\n")
	for count := 0; count < 3; count++ {
		_, err := Generate(context.Background(), options)
		if count != 1 && (err == nil || !strings.Contains(err.Error(), "ApiFile")) {
			t.Fatalf("%d API files should require an explicit entry: %v", count, err)
		}
		writeFixture(t, dir, string(rune('a'+count))+".api", "syntax = \"v1\"")
	}
}
