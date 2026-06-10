package gormplus_test

import (
	"context"
	"testing"

	gormplus "github.com/kuangshp/gorm-plus"
)

func TestTenantConfigSupportsRootPackageTypesExternally(t *testing.T) {
	t.Parallel()

	_, err := gormplus.NewTenantPlugin(gormplus.TenantConfig[int64]{
		TenantField:          "tenant_id",
		AutoInjectJoinTables: gormplus.BoolPtr(false),
		DuplicatePolicy:      gormplus.PolicyReplace,
		TenantFields: []gormplus.TenantFieldConfig[int64]{
			{Field: "tenant_id"},
			{
				Field: "org_id",
				GetTenantID: func(ctx context.Context) (int64, bool) {
					return 1, true
				},
			},
		},
		TableFields: map[string][]gormplus.TenantFieldConfig[int64]{
			"sys_contract": {{Field: "company_id"}},
		},
		JoinTableOverrides: []gormplus.JoinTenantConfig[int64]{
			{Table: "sys_contract_detail", Field: "company_id"},
		},
	})
	if err != nil {
		t.Fatalf("NewTenantPlugin() error = %v", err)
	}
}
