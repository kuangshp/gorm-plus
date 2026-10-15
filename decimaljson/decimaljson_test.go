package decimaljson

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUnmarshalDecimalInputs(t *testing.T) {
	type input struct {
		Amount string    `json:"amount" decimal:"true"`
		Rate   string    `json:"rate" validate:"required,decimal"`
		Legacy float64   `json:"legacy"`
		Small  float32   `json:"small"`
		Quoted float64   `json:"quoted,string"`
		Name   string    `json:"name"`
		Count  int       `json:"count"`
		Date   time.Time `json:"date"`
	}
	for _, amount := range []string{`12.345678`, `"12.345678"`, `0`, `"0.0000"`, `1.234e2`} {
		var got input
		err := Unmarshal([]byte(`{"amount":`+amount+`,"rate":"7.0000","legacy":"2.25","small":1.5,"quoted":3.5,"name":"abc","count":2,"date":"2026-09-09T00:00:00Z"}`), &got)
		if err != nil {
			t.Fatal(err)
		}
		if got.Amount != strings.Trim(amount, `"`) || got.Legacy != 2.25 || got.Small != 1.5 || got.Quoted != 3.5 || got.Date.Year() != 2026 {
			t.Fatalf("unexpected conversion: %+v", got)
		}
	}
	for _, value := range []string{`""`, `"abc"`, `"NaN"`, `"Inf"`, `"1e999"`, `"0x1p2"`, `"1_000"`, `true`, `[]`, `{}`} {
		var got input
		if err := Unmarshal([]byte(`{"amount":`+value+`}`), &got); err == nil {
			t.Fatalf("accepted invalid value: %s", value)
		}
	}
	var got input
	if err := Unmarshal([]byte(`{"name":12}`), &got); err != nil || got.Name != "12" {
		t.Fatalf("ordinary string did not accept number: %+v, %v", got, err)
	}
	if err := Unmarshal([]byte(`{"count":"12"}`), &got); err != nil || got.Count != 12 {
		t.Fatalf("integer string was not accepted: count=%d, err=%v", got.Count, err)
	}
	if err := Unmarshal([]byte(`{"small":"1e50"}`), &got); err == nil {
		t.Fatal("float32 overflow accepted")
	}
	if err := Unmarshal([]byte(`{"amount":null}`), &got); err != nil || got.Amount != "" {
		t.Fatal("null semantics changed")
	}
}

func TestNestedAndEmbedded(t *testing.T) {
	type Item struct {
		Amount string `json:"amount" decimal:"true"`
	}
	type Create struct{ Item }
	type Modify struct {
		ID int64 `json:"id"`
		Create
		Items   []*Item         `json:"items"`
		ByName  map[string]Item `json:"byName"`
		Amounts []string        `json:"amounts" decimal:"true"`
	}
	var got Modify
	err := Unmarshal([]byte(`{"id":37,"amount":1.23456,"items":[{"amount":2.5},null],"byName":{"x":{"amount":3}},"amounts":[4,"5.0000"]}`), &got)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 37 || got.Amount != "1.23456" || got.Items[0].Amount != "2.5" || got.ByName["x"].Amount != "3" || got.Amounts[1] != "5.0000" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestPrepareRequest(t *testing.T) {
	type input struct {
		Amount string `json:"amount" decimal:"true"`
	}
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"amount":12.345678}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.ContentLength = -1 // chunked JSON must become visible to httpx.ParseJsonBody.
	var got input
	if err := PrepareRequest(req, &got); err != nil {
		t.Fatal(err)
	}
	if req.ContentLength <= 0 {
		t.Fatal("body length was not restored")
	}
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil || got.Amount != "12.345678" {
		t.Fatalf("decode: %+v, %v", got, err)
	}
	form := httptest.NewRequest("POST", "/", strings.NewReader("amount=12.34"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	original := form.Body
	if err := PrepareRequest(form, &got); err != nil || form.Body != original {
		t.Fatal("form body was changed")
	}
	large := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat(" ", (8<<20)+1)))
	large.Header.Set("Content-Type", "application/json")
	if err := PrepareRequest(large, &got); err == nil {
		t.Fatal("oversized JSON accepted")
	}
}
