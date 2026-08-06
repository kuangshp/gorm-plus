package {{.pkgName}}

import (
	"net/http"

	{{.imports}}
)

type {{.logic}} struct {
	logx.Logger
	ctx     context.Context
	svcCtx  *svc.ServiceContext
	Request *http.Request
	W       http.ResponseWriter
}

// New{{.logic}} 创建接口逻辑。
func New{{.logic}}(ctx context.Context, svcCtx *svc.ServiceContext, request *http.Request, w http.ResponseWriter) *{{.logic}} {
	return &{{.logic}}{
		Logger:  logx.WithContext(ctx),
		ctx:     ctx,
		svcCtx:  svcCtx,
		Request: request,
		W:       w,
	}
}

// {{.function}} 处理接口业务。
func (l *{{.logic}}) {{.function}}({{.request}}) {{.responseType}} {
	// todo: add your logic here and delete this line

	{{.returnString}}
}
