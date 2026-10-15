package decimaljson

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestStringNumbersPreserveText verifies conversion without float64 rounding or range limits.
func TestStringNumbersPreserveText(t *testing.T) {
	for _, text := range []string{"12", "12.20", "-0", "-0.000", "1.2300e+20", "9007199254740993", "18446744073709551616", "1e999"} {
		t.Run(text, func(t *testing.T) {
			for _, raw := range []string{text, fmt.Sprintf("%q", text)} {
				var got struct {
					Value string `json:"value"`
				}
				if err := Unmarshal([]byte(`{"value":`+raw+`}`), &got); err != nil || got.Value != text {
					t.Fatalf("input %s: got %q, err %v", raw, got.Value, err)
				}
			}
		})
	}
	for _, raw := range []string{`true`, `false`, `[]`, `{}`, `01`, `+1`, `NaN`, `1.`, `1e`} {
		var got string
		if err := Unmarshal([]byte(raw), &got); err == nil {
			t.Errorf("accepted non-string/non-number input %s", raw)
		}
	}
	for _, text := range []string{"", "abc", "0012", "NaN"} {
		var got string
		if err := Unmarshal([]byte(fmt.Sprintf("%q", text)), &got); err != nil || got != text {
			t.Fatalf("ordinary text changed: %q, %v", got, err)
		}
	}
}

// TestNestedStringNumbers covers pointers, embedded fields and collection elements.
func TestNestedStringNumbers(t *testing.T) {
	type label string
	type Base struct {
		ID string `json:"id"`
	}
	var got struct {
		Base
		Amount  *string           `json:"amount"`
		Missing *string           `json:"missing"`
		Null    *string           `json:"null"`
		Name    label             `json:"name"`
		List    []string          `json:"list"`
		Array   [2]string         `json:"array"`
		ByName  map[string]string `json:"byName"`
	}
	body := `{"id":9007199254740993,"amount":12.20,"null":null,"name":7,"list":[1,"abc"],"array":[2.00,"3"],"byName":{"x":-4.50}}`
	if err := Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "9007199254740993" || got.Amount == nil || *got.Amount != "12.20" || got.Missing != nil || got.Null != nil ||
		got.Name != "7" || got.List[0] != "1" || got.List[1] != "abc" || got.Array != [2]string{"2.00", "3"} || got.ByName["x"] != "-4.50" {
		t.Fatalf("unexpected conversion: %+v", got)
	}
}

type customString string

func (value *customString) UnmarshalJSON(data []byte) error {
	*value = customString(data)
	return nil
}

// TestStringCustomDecoderAndQuotedTag preserves explicit JSON decoding contracts.
func TestStringCustomDecoderAndQuotedTag(t *testing.T) {
	var got struct {
		Custom customString `json:"custom"`
		Quoted string       `json:"quoted,string"`
	}
	if err := Unmarshal([]byte(`{"custom":12.20,"quoted":"\"abc\""}`), &got); err != nil || got.Custom != "12.20" || got.Quoted != "abc" {
		t.Fatalf("explicit decoding changed: %+v, %v", got, err)
	}
}

// TestPrepareRequestStrings exercises the request-body entry point used by generated handlers.
func TestPrepareRequestStrings(t *testing.T) {
	var got struct {
		ID     int64  `json:"id"`
		Amount string `json:"amount"`
	}
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"id":"12","amount":12.20}`))
	r.Header.Set("Content-Type", "application/json")
	if err := PrepareRequest(r, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.ID != 12 || got.Amount != "12.20" {
		t.Fatalf("request conversion: %+v, %v", got, err)
	}
}
