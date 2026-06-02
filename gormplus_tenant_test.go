package gormplus

import (
	"context"
	"testing"
)

func TestTenantConfigUsesRootPackageTypes(t *testing.T) {
	t.Parallel()

	_ = TenantConfig[int64]{
		TenantField:     "company_id",
		DuplicatePolicy: PolicyReplace,
		TenantFields: []TenantFieldConfig[int64]{
			{Field: "company_id"},
			{
				Field: "org_id",
				GetTenantID: func(ctx context.Context) (int64, bool) {
					return 1, true
				},
			},
		},
		JoinTableOverrides: []JoinTenantConfig[int64]{
			{Table: "sys_contract_detail", Field: "company_id"},
		},
	}
}
