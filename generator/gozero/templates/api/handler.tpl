package {{.PkgName}}

import (
	"net/http"

	"github.com/zeromicro/go-zero/rest/httpx"

	{{if .HasRequest}}"github.com/kuangshp/go-utils/k/validation"
 "github.com/kuangshp/gorm-plus/decimaljson"{{end}}
	{{.ImportPackages}}
)

// {{.HandlerName}} 处理接口请求。
func {{.HandlerName}}(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		{{if .HasRequest}}var req types.{{.RequestType}}
		// 兼容整数、小数的数字与字符串入参，随后沿用 go-zero 的参数解析和校验。
 if err := decimaljson.PrepareRequest(r, &req); err != nil {
 httpx.ErrorCtx(r.Context(), w, err)
 return
 }
 if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		// 校验请求数据。
		if validation.HasRules(&req) {
			if validateErr := validation.Validate(&req); validateErr != nil {
				httpx.ErrorCtx(r.Context(), w, validateErr)
				return
			}
		}
		{{end}}l := {{.LogicName}}.New{{.LogicType}}(r.Context(), svcCtx, r, w)
		{{if .HasResp}}resp, {{end}}err := l.{{.Call}}({{if .HasRequest}}&req{{end}})
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
		}{{if .HasResp}} else {
			httpx.OkJsonCtx(r.Context(), w, resp)
		}{{end}}
	}
}
