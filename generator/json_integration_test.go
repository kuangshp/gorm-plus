package generator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gozerogen "github.com/kuangshp/gorm-plus/generator/gozero"
)

func TestGeneratedJSONGoZero(t *testing.T) { // 验证实际生成的 go-zero 字符串类型、解析器及两类 Mapper 保留 JSON 文本。
	if os.Getenv("GORMPLUS_GOCTL_INTEGRATION") != "1" {
		t.Skip("set GORMPLUS_GOCTL_INTEGRATION=1 to run JSON goctl integration")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, value string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", fmt.Sprintf("module jsonfixture\n\ngo 1.26.10\nrequire (\n github.com/kuangshp/gorm-plus v0.0.0\n github.com/zeromicro/go-zero v1.10.2\n)\nreplace github.com/kuangshp/gorm-plus => %s\n", root))
	t.Chdir(dir)
	cfg := &Config{DBType: "sqlite", Database: filepath.Join(dir, "json.db"), OutPath: "dao", ModelPkgPath: "model", Package: "jsonfixture"}
	db, err := OpenDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	t.Cleanup(func() { pool.Close() })
	// 先建表并读取真实元数据，再运行项目生成器。
	ddl := "CREATE TABLE samples (id INTEGER PRIMARY KEY, name VARCHAR(40) NOT NULL, attributes JSONB NOT NULL, metadata JSON)"
	write("sql/20261023JSON集成测试.sql", ddl)
	if err := db.Exec(ddl).Error; err != nil {
		t.Fatal(err)
	}
	columns, err := getTableColumns(db, "samples")
	if err != nil {
		t.Fatal(err)
	}
	oldLines, oldIndex := inputLines, inputIndex
	t.Cleanup(func() { inputLines, inputIndex = oldLines, oldIndex })
	inputLines, inputIndex = []string{"samples"}, 0
	t.Setenv("GENERATOR_TEMPLATE_DIR", filepath.Join(root, "generator/template"))
	if err := Generate(cfg); err != nil {
		t.Fatal(err)
	}
	api, err := generateApiFile("samples", columns, "Samples", db, filepath.Join(root, "generator/template/api_template.txt"))
	if err != nil {
		t.Fatal(err)
	}
	write("desc/samples.api", api)
	base, err := os.ReadFile(filepath.Join(root, "generator/template/base_api_template.txt"))
	if err != nil {
		t.Fatal(err)
	}
	write("desc/base.api", string(base))
	if output, err := gozerogen.Run(context.Background(), gozerogen.RunOptions{}, "api", "go", "--api", filepath.Join(dir, "desc/samples.api"), "--dir", dir, "--style", "goZero"); err != nil {
		t.Fatalf("goctl: %v\n%s", err, output)
	}
	data := buildMapperData("samples", columns, "Samples", db, "jsonfixture", "model", "", "", true)
	mapper, err := renderTemplate(filepath.Join(root, "generator/template/mapper_template.txt"), data)
	if err != nil {
		t.Fatal(err)
	}
	mapper = strings.Replace(mapper, "// TODO: 填写 go-zero types 包的实际 import 路径，例如：", `"jsonfixture/internal/types"`, 1)
	write("internal/mapper/samples.go", mapper)
	// 保持 RPC 的字符串协议，使用测试消息形状校验边界转换，不修改业务 Proto。
	write("internal/pb/messages.go", `package pb
type CreateSamplesReq struct { Name *string; Attributes *string; Metadata *string }
type ModifySamplesReq struct { Id int64; Name *string; Attributes *string; Metadata *string }
type PageRequest struct { PageSize int64; PageNumber int64 }
type PageSamplesReq struct { Page *PageRequest; Name *string; Attributes *string; Metadata *string }
type SamplesModel struct { Id int64; Name string; Attributes string; Metadata string }
`)
	protoData := buildProtoMapperData("samples", columns, "Samples", db, "jsonfixture", "model", "pb", "internal/types")
	protoMapper, err := renderTemplate(filepath.Join(root, "generator/template/api_proto_mapper_template.txt"), protoData)
	if err != nil {
		t.Fatal(err)
	}
	protoMapper = strings.Replace(protoMapper, "// TODO: 请手动引入 API types 包和 Proto 包，并分别使用 types、pb 作为包名。", "import (\"jsonfixture/internal/types\"; \"jsonfixture/internal/pb\")", 1)
	write("internal/apiproto/samples.go", protoMapper)
	source, err := os.ReadFile(filepath.Join(root, "generator/testdata/json_gozero_test.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	write("internal/mapper/json_test.go", string(source))
	command := exec.Command("go", "test", "-mod=mod", "./...")
	command.Dir, command.Env = dir, append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated JSON integration: %v\n%s", err, output)
	}
}
