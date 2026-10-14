package gozero

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoctlDecimalIntegration verifies real goctl types and go-zero's real parser.
func TestGoctlDecimalIntegration(t *testing.T) {
	if os.Getenv("GORMPLUS_GOCTL_INTEGRATION") != "1" {
		t.Skip("set GORMPLUS_GOCTL_INTEGRATION=1 to run goctl integration")
	}
	if _, err := exec.LookPath("goctl"); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	spec := "syntax = \"v1\"\ntype Item {\n Amount string `json:\"amount\" decimal:\"true\"`\n Quantity int64 `json:\"quantity\"`\n}\ntype Input {\n ID int64 `path:\"id\"`\n Count int64 `json:\"count\"`\n QuotedCount int64 `json:\"quotedCount,string\"`\n Amount string `json:\"amount\" decimal:\"true\"`\n Legacy float64 `json:\"legacy\"`\n Items []Item `json:\"items\"`\n}\nservice decimal-api {\n @handler Check\n post /check/:id (Input) returns (Item)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "decimal.api"), []byte(spec), 0600); err != nil {
		t.Fatal(err)
	}
	mod := fmt.Sprintf("module decimalfixture\n\ngo 1.25.5\n\nrequire (\n github.com/kuangshp/gorm-plus v0.0.0\n github.com/zeromicro/go-zero v1.10.2\n)\nreplace github.com/kuangshp/gorm-plus => %s\n", root)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := Run(context.Background(), RunOptions{}, "api", "go", "--api", filepath.Join(dir, "decimal.api"), "--dir", dir, "--style", "goZero")
	if err != nil {
		t.Fatalf("goctl: %v\n%s", err, output)
	}
	handler, err := os.ReadFile(filepath.Join(dir, "internal/handler/checkHandler.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(handler), "decimaljson.PrepareRequest(r, &req)") {
		t.Fatalf("handler missing normalization:\n%s", handler)
	}
	testSource := `package types
import (
 "strings"
 "testing"
 "net/http/httptest"
 "github.com/kuangshp/gorm-plus/decimaljson"
 "github.com/zeromicro/go-zero/rest/httpx"
 "github.com/zeromicro/go-zero/rest/pathvar"
)
func TestActualParser(t *testing.T) {
 for _, body:=range []string{
  "{\"count\":9223372036854775807,\"quotedCount\":17,\"amount\":12.345678,\"legacy\":\"1.25\",\"items\":[{\"amount\":3.5,\"quantity\":9007199254740993}]}",
  "{\"count\":\"9223372036854775807\",\"quotedCount\":\"17\",\"amount\":\"12.345678\",\"legacy\":1.25,\"items\":[{\"amount\":\"3.5\",\"quantity\":\"9007199254740993\"}]}",
 } {
  r:=httptest.NewRequest("POST","/check/17",strings.NewReader(body))
  r.Header.Set("Content-Type","application/json")
  r=pathvar.WithVars(r,map[string]string{"id":"17"})
  var req Input
  if err:=decimaljson.PrepareRequest(r,&req);err!=nil {t.Fatal(err)}
  if err:=httpx.Parse(r,&req);err!=nil {t.Fatal(err)}
  if req.ID!=17 || req.Count!=9223372036854775807 || req.QuotedCount!=17 || req.Amount!="12.345678" || req.Legacy!=1.25 || req.Items[0].Amount!="3.5" || req.Items[0].Quantity!=9007199254740993 {t.Fatalf("bad request: %+v",req)}
 }
 for _, body:=range []string{"{\"count\":\"1.5\"}","{\"count\":\"9223372036854775808\"}"} {
  r:=httptest.NewRequest("POST","/check/17",strings.NewReader(body))
  r.Header.Set("Content-Type","application/json")
  var req Input
  if err:=decimaljson.PrepareRequest(r,&req);err==nil {t.Fatalf("accepted invalid integer: %s",body)}
 }
}
`
	if err := os.WriteFile(filepath.Join(dir, "internal/types/decimal_test.go"), []byte(testSource), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-mod=mod", "./internal/types")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go-zero parser failed: %v\n%s", err, output)
	}
}
