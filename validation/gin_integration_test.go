package validation

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGinIntegration 在临时模块中编译文档原文，验证真实 Gin 绑定只校验一次。
// Gin 仅供该临时模块使用，不成为 gorm-plus 的依赖。
func TestGinIntegration(t *testing.T) {
	if os.Getenv("GORMPLUS_GIN_INTEGRATION") != "1" {
		t.Skip("set GORMPLUS_GIN_INTEGRATION=1 to run Gin integration")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	document, err := os.ReadFile(filepath.Join(root, "docs/validation.md"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":              fmt.Sprintf("module flycos\n\ngo 1.26.10\n\nrequire (\n github.com/gin-gonic/gin v1.12.0\n github.com/kuangshp/gorm-plus v0.0.0\n)\nreplace github.com/kuangshp/gorm-plus => %s\n", root),
		"validators/rules.go": ginProjectRules,
		"gin_test.go":         ginBindingTests,
	}
	for _, name := range []string{"utils/validator.go", "initialize/validate.go"} {
		_, rest, ok := strings.Cut(string(document), "<!-- gin-example: "+name+" -->")
		if !ok {
			t.Fatalf("missing documented example %s", name)
		}
		_, rest, ok = strings.Cut(rest, "```go\n")
		if !ok {
			t.Fatalf("missing Go code for %s", name)
		}
		source, _, ok := strings.Cut(rest, "\n```")
		if !ok {
			t.Fatalf("unterminated Go example %s", name)
		}
		files[name] = source + "\n"
	}
	for name, source := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("go", "test", "-mod=mod", "-race", "./...")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("documented Gin integration failed: %v\n%s", err, output)
	}
}

// 以下项目规则仅用于验证文档装配和同名覆盖，不复制参考项目的业务实现。
const ginProjectRules = `package validators
import (
 "sync/atomic"
 "github.com/go-playground/validator/v10"
)
var MobileCalls atomic.Int64
func ValidateMobile(fl validator.FieldLevel) bool { MobileCalls.Add(1); return fl.Field().String()=="project-phone" }
func ValidateEmail(fl validator.FieldLevel) bool { return fl.Field().String()=="project-email" }
func ValidateDate(fl validator.FieldLevel) bool { return fl.Field().String()=="project-date" }
func ValidateDateTime(fl validator.FieldLevel) bool { return fl.Field().String()=="project-time" }
func ValidateName(fl validator.FieldLevel) bool { return fl.Field().String()=="project-name" }
func ValidateId(fl validator.FieldLevel) bool { return fl.Field().String()=="project-id" }
func ValidateCronSpec(fl validator.FieldLevel) bool { return fl.Field().String()=="project-cron" }
`

const ginBindingTests = "package fixture\n" + `
import (
 "encoding/json"
 "errors"
 "fmt"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
 "flycos/initialize"
 "flycos/utils"
 "flycos/validators"
 "github.com/gin-gonic/gin"
 "github.com/kuangshp/gorm-plus/validation"
)
type Request struct {
 Mobile string ` + "`json:\"mobile\" binding:\"required,mobile\"`" + `
 Date string ` + "`json:\"date\" binding:\"required,date\"`" + `
 Time string ` + "`json:\"time\" binding:\"required,dateTime\"`" + `
 Email string ` + "`json:\"email\" binding:\"required,myEmail\"`" + `
 Name string ` + "`json:\"name\" binding:\"required,checkName\"`" + `
 ID string ` + "`json:\"id\" binding:\"required,chineseId\"`" + `
 Cron string ` + "`json:\"cron\" binding:\"omitempty,cronspec\"`" + `
 Common string ` + "`json:\"common\" binding:\"required,checkMobile\"`" + `
 Ignored string ` + "`json:\"ignored\" validate:\"required\"`" + `
}
func TestDocumentedGinBinding(t *testing.T) {
 gin.SetMode(gin.TestMode)
 if err:=initialize.InitValidate();err!=nil {t.Fatal(err)}
 // 重复启动调用不能重复添加翻译，也不覆盖第一份规则。
 if err:=utils.InitValidation(validation.Rule{Tag:"mobile",Message:"bad",Validate:validators.ValidateEmail});err!=nil {t.Fatal(err)}
 router:=gin.New()
 var lastErr error
 router.POST("/users",func(ctx *gin.Context){
  var req Request
  if err:=ctx.ShouldBindJSON(&req);err!=nil {
   lastErr=err
   ctx.JSON(http.StatusOK,gin.H{"code":1,"message":utils.ShowErrorMessage(err)})
   return
  }
  ctx.JSON(http.StatusOK,gin.H{"code":0})
 })
 valid:=map[string]string{"mobile":"project-phone","date":"project-date","time":"project-time","email":"project-email","name":"project-name","id":"project-id","cron":"project-cron","common":"13800138000"}
 send:=func(body string) map[string]any {
  req:=httptest.NewRequest(http.MethodPost,"/users",strings.NewReader(body));req.Header.Set("Content-Type","application/json")
  w:=httptest.NewRecorder();router.ServeHTTP(w,req)
  var out map[string]any
  if err:=json.Unmarshal(w.Body.Bytes(),&out);err!=nil {t.Fatal(err)}
  return out
 }
 body,_:=json.Marshal(valid)
 validators.MobileCalls.Store(0)
 if out:=send(string(body));out["code"]!=float64(0) {t.Fatal(out)}
 if got:=validators.MobileCalls.Load();got!=1 {t.Fatalf("validated %d times, want once",got)}
 for field,message:=range map[string]string{
  "mobile":"mobile 非法的手机号码!","date":"date 时间格式非法必须为2006-01-02格式!",
  "time":"time 时间格式非法必须为2006-01-02 15:04:05格式!","email":"email 邮箱号码非法!",
  "name":"name用户名长度必须是3-12位字符","id":"id身份证号码格式错误",
  "cron":"cron定时规则格式非法，需为6段cron表达式(秒 分 时 日 月 周)",
 } {
  original:=valid[field];valid[field]="invalid";body,_:=json.Marshal(valid)
  out:=send(string(body));valid[field]=original
  if out["code"]!=float64(1)||out["message"]!=message {t.Fatalf("%s: %v",field,out)}
  wrapped:=fmt.Errorf("wrapped: %w",lastErr)
  if utils.ShowErrorMessage(wrapped)!=message {t.Fatal("wrapped validation error lost translation")}
 }
 // required 由 Gin 的 binding 引擎执行，且没有 validate 标签的二次校验。
 if out:=send("{}");out["code"]!=float64(1)||!strings.Contains(out["message"].(string),"mobile") {t.Fatal(out)}
 send("{")
 if utils.ShowErrorMessage(lastErr)!=lastErr.Error() {t.Fatal("JSON parse error was translated as validation error")}
 parseErr:=errors.New("bad input")
 if utils.ShowErrorMessage(parseErr)!="bad input"||utils.ShowErrorMessage(nil)!="" {t.Fatal("unexpected generic error translation")}
}
`
