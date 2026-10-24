package generator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedDecimalDTO compiles and executes the actual generated DTO methods.
func TestGeneratedDecimalDTO(t *testing.T) {
	columns := buildApiColumns([]ColumnInfo{
		{Name: "amount", Type: "decimal(12,4)"},
		{Name: "rate", Type: "double"},
		{Name: "name", Type: "varchar(30)"},
	})
	data := VoTemplateData{
		ModelName:    "Price",
		TableComment: "价格",
		Columns:      columns,
	}
	source, err := renderTemplate("template/dto_template.txt", data)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":   fmt.Sprintf("module decimalfixture\n\ngo 1.26.10\n\nrequire github.com/kuangshp/gorm-plus v0.0.0\nreplace github.com/kuangshp/gorm-plus => %s\n", root),
		"price.go": source,
		"price_test.go": `package dto
import ("encoding/json"; "testing")
func TestRoundTrip(t *testing.T) {
 for _, body := range []string{
  "{\"id\":17,\"amount\":12.345678,\"rate\":1.25,\"name\":\"test\"}",
  "{\"id\":\"17\",\"amount\":\"12.345678\",\"rate\":\"1.25\",\"name\":\"test\"}",
 } {
  var req ModifyPriceDTO
  if err:=json.Unmarshal([]byte(body),&req);err!=nil {t.Fatal(err)}
  if req.ID!=17 || req.Amount!="12.345678" || req.Rate!="1.25" || req.Name!="test" {t.Fatalf("lost fields: %+v",req)}
 }
 var modified ModifyPriceDTO
 if err:=json.Unmarshal([]byte("{\"id\":\"9223372036854775807\"}"),&modified);err!=nil || modified.ID!=9223372036854775807 {t.Fatalf("integer precision: %+v, %v",modified,err)}
 if err:=json.Unmarshal([]byte("{\"id\":\"9223372036854775808\"}"),&modified);err==nil {t.Fatal("accepted integer overflow")}
 var req CreatePriceDTO
 if err:=json.Unmarshal([]byte("{\"amount\":\"abc\"}"),&req);err==nil {t.Fatal("accepted invalid amount")}
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("go", "test", "-mod=mod", ".")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated DTO failed: %v\n%s", err, output)
	}
	apiSource, err := renderTemplate("template/api_template.txt", ApiTemplateData{
		ModelName:    "Price",
		Columns:      columns,
		ModelColumns: columns,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(apiSource, `decimal:"true"`) != 4 {
		t.Fatalf("create and page decimal fields are not marked:\n%s", apiSource)
	}
}
