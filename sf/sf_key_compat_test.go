package sf

import (
	"testing"
)

// TestBuildSFKey_ByteCompatibility 金丝雀：优化前后 cache key 必须逐字节一致。
// 期望值取自优化前 marshalSorted + fmt.Sprintf("%x") 实现（2026-10-15 基线）。
func TestBuildSFKey_ByteCompatibility(t *testing.T) {
	cases := []struct {
		name string
		fn   string
		args map[string]any
		want string
	}{
		{name: "nil_args", fn: "users.FindById", args: nil, want: "sf:users.FindById:noargs"},
		{name: "empty_map", fn: "users.FindById", args: map[string]any{}, want: "sf:users.FindById:noargs"},
		{name: "id_int64", fn: "users.FindById", args: map[string]any{"id": int64(1)}, want: "sf:users.FindById:d2ce28b9a7fd7e4407e2b0fd499b7fe4"},
		{
			name: "page_args",
			fn:   "sys_user.FindPage",
			args: map[string]any{"id": int64(1), "status": int64(2), "page": 1, "size": 20, "keyword": "hello"},
			want: "sf:sys_user.FindPage:314005da61f751ea95b34fd712505328",
		},
		{
			name: "json_html_escape",
			fn:   "x.Y",
			args: map[string]any{"a": true, "b": nil, "c": "x&y"},
			want: "sf:x.Y:e66b7770dea1915c40d769a83aac73f8",
		},
		{
			name: "nested",
			fn:   "x.Y",
			args: map[string]any{"m": map[string]any{"z": 1, "a": 2}, "n": []any{1, "two"}},
			want: "sf:x.Y:619600c2ebab377ed5441353e5284afe",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildSFKey(tc.fn, tc.args)
			if got != tc.want {
				t.Fatalf("buildSFKey mismatch\n want %q\n got  %q", tc.want, got)
			}
		})
	}
}

func TestMarshalSorted_HTMLEscapeAmpersand(t *testing.T) {
	b, err := marshalSorted(map[string]any{"c": "x&y"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"c":"x\u0026y"}`
	if string(b) != want {
		t.Fatalf("marshalSorted bytes changed\n want %s\n got  %s", want, b)
	}
}
