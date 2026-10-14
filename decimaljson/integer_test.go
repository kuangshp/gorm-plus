package decimaljson

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestUnmarshalIntegerWidths(t *testing.T) {
	tests := []struct {
		name        string
		typ         reflect.Type
		min, max    string
		below, over string
	}{
		{"int8", reflect.TypeOf(int8(0)), "-128", "127", "-129", "128"},
		{"int16", reflect.TypeOf(int16(0)), "-32768", "32767", "-32769", "32768"},
		{"int32", reflect.TypeOf(int32(0)), "-2147483648", "2147483647", "-2147483649", "2147483648"},
		{"int64", reflect.TypeOf(int64(0)), "-9223372036854775808", "9223372036854775807", "-9223372036854775809", "9223372036854775808"},
		{"uint8", reflect.TypeOf(uint8(0)), "0", "255", "-1", "256"},
		{"uint16", reflect.TypeOf(uint16(0)), "0", "65535", "-1", "65536"},
		{"uint32", reflect.TypeOf(uint32(0)), "0", "4294967295", "-1", "4294967296"},
		{"uint64", reflect.TypeOf(uint64(0)), "0", "18446744073709551615", "-1", "18446744073709551616"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, text := range []string{tt.min, tt.max, "1"} {
				for _, body := range []string{text, fmt.Sprintf("%q", text)} {
					target := reflect.New(tt.typ)
					if err := Unmarshal([]byte(body), target.Interface()); err != nil {
						t.Fatalf("%s: %v", body, err)
					}
					encoded, err := json.Marshal(target.Interface())
					if err != nil || string(encoded) != text {
						t.Fatalf("%s lost integer precision: %s, %v", body, encoded, err)
					}
				}
			}
			for _, text := range []string{tt.below, tt.over} {
				for _, body := range []string{text, fmt.Sprintf("%q", text)} {
					if err := Unmarshal([]byte(body), reflect.New(tt.typ).Interface()); err == nil {
						t.Fatalf("accepted out-of-range value %s", body)
					}
				}
			}
		})
	}
	var platform struct {
		Signed   int  `json:"signed"`
		Unsigned uint `json:"unsigned"`
	}
	if err := Unmarshal([]byte(`{"signed":"-1","unsigned":"1"}`), &platform); err != nil || platform.Signed != -1 || platform.Unsigned != 1 {
		t.Fatalf("platform integer conversion: %+v, %v", platform, err)
	}
}

func TestUnmarshalIntegerRejectsInvalidInputs(t *testing.T) {
	for _, body := range []string{
		`""`, `"abc"`, `"1.5"`, `"1.0"`, `"1e3"`, `" 1"`, `"1 "`,
		`"0x10"`, `"1_000"`, `"NaN"`, `true`, `[]`, `{}`, `1.5`, `1.0`, `1e3`, `01`,
	} {
		var req struct {
			ID int64 `json:"id"`
		}
		if err := Unmarshal([]byte(`{"id":`+body+`}`), &req); err == nil {
			t.Errorf("accepted invalid integer %s", body)
		}
	}
}

func TestUnmarshalNestedIntegersAndStringTags(t *testing.T) {
	type item struct {
		ID int64 `json:"id"`
	}
	type base struct {
		ID int64 `json:"id"`
	}
	type request struct {
		base
		Count    *int64           `json:"count"`
		Missing  *int64           `json:"missing"`
		Null     *int64           `json:"null"`
		NullID   int64            `json:"nullId"`
		Quoted   int64            `json:"quoted,string"`
		Unsigned uint64           `json:"unsigned,string"`
		Items    []item           `json:"items"`
		IDs      [2]int64         `json:"ids"`
		ByName   map[string]int64 `json:"byName"`
	}
	for _, quoted := range []string{`"17"`, `17`} {
		var req request
		body := `{"id":"9007199254740993","count":"-2","null":null,"nullId":null,"quoted":` + quoted + `,"unsigned":"18446744073709551615","items":[{"id":"3"}],"ids":["4",5],"byName":{"first":"6"}}`
		if err := Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		if req.ID != 9007199254740993 || req.Count == nil || *req.Count != -2 || req.Quoted != 17 ||
			req.Unsigned != ^uint64(0) || req.Items[0].ID != 3 || req.IDs != [2]int64{4, 5} || req.ByName["first"] != 6 ||
			req.Null != nil || req.Missing != nil || req.NullID != 0 {
			t.Fatalf("unexpected integer conversion: %+v", req)
		}
	}
	var req request
	if err := Unmarshal([]byte(`{"items":[{"id":"bad"}]}`), &req); err == nil || !strings.Contains(err.Error(), "request.items[0].id") {
		t.Fatalf("nested error did not identify the integer field: %v", err)
	}
}

type customInteger int64

func (value *customInteger) UnmarshalJSON(data []byte) error {
	if string(data) != `"custom"` {
		return fmt.Errorf("unexpected custom integer input")
	}
	*value = 7
	return nil
}

func TestCustomIntegerAndBytesKeepTheirDecoding(t *testing.T) {
	var req struct {
		Value customInteger `json:"value"`
		Bytes []byte        `json:"bytes"`
	}
	if err := Unmarshal([]byte(`{"value":"custom","bytes":"aGVsbG8="}`), &req); err != nil || req.Value != 7 || string(req.Bytes) != "hello" {
		t.Fatalf("custom scalar or byte decoding changed: %+v, %v", req, err)
	}
}

func TestPrepareRequestIntegers(t *testing.T) {
	for _, body := range []string{`{"id":9223372036854775807}`, `{"id":"9223372036854775807"}`} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		var req struct {
			ID int64 `json:"id"`
		}
		if err := PrepareRequest(r, &req); err != nil {
			t.Fatal(err)
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID != 9223372036854775807 {
			t.Fatalf("prepared integer body lost precision: %d, %v", req.ID, err)
		}
	}
}
