package query

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gen/field"
	"gorm.io/gorm"
)

func BenchmarkGenWrap_Apply_SimpleWhere(b *testing.B) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		b.Fatal(err)
	}
	statusCol := field.NewInt64("", "status")
	nameCol := field.NewString("", "username")

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		do := &wrapperTestDO{db: db.Table("accounts")}
		_ = Wrap(do).
			WhereIf(true, statusCol.Eq(1)).
			LLike(nameCol, "admin").
			BetweenIfNotZero(statusCol, int64(1), int64(2)).
			OrderTriState(2, statusCol, statusCol).
			Apply()
	}
}

func BenchmarkBuildArgsFromStruct(b *testing.B) {
	type req struct {
		PageNumber int64  `json:"pageNumber"`
		PageSize   int64  `json:"pageSize"`
		Status     int64  `json:"status"`
		Keyword    string `json:"keyword"`
	}
	r := req{PageNumber: 1, PageSize: 20, Status: 2, Keyword: "hello"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = BuildArgsFromStruct(r)
	}
}
