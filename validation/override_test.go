package validation

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
)

func TestProjectRulesOverrideDefaultsAndEarlierOptions(t *testing.T) {
	instance, err := New(
		WithRules(Rule{Tag: "mobile", Message: "{0} 旧提示", Validate: func(validator.FieldLevel) bool { return false }}),
		WithRules(Rule{Tag: "mobile", Message: "{0} 项目手机格式错误", Validate: func(fl validator.FieldLevel) bool {
			return fl.Field().String() == "local-phone"
		}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	type request struct {
		Phone string `json:"phone" validate:"mobile"`
	}
	if err := instance.Validate(request{Phone: "local-phone"}); err != nil {
		t.Fatal(err)
	}
	if got := instance.FirstMessage(instance.Validate(request{Phone: "13800138000"})); got != "phone 项目手机格式错误" {
		t.Fatalf("project override was not applied: %q", got)
	}
	// 覆盖只影响该实例，不改变包级默认校验器。
	if err := Validate(request{Phone: "13800138000"}); err != nil {
		t.Fatal(err)
	}
}

func TestOverrideStandardTranslation(t *testing.T) {
	instance, err := New(WithRules(Rule{
		Tag: "email", Message: "{0} 使用项目邮箱格式",
		Validate: func(fl validator.FieldLevel) bool { return strings.HasSuffix(fl.Field().String(), "@internal") },
	}))
	if err != nil {
		t.Fatal(err)
	}
	type request struct {
		Email string `json:"email" validate:"email"`
	}
	if err := instance.Validate(request{Email: "name@internal"}); err != nil {
		t.Fatal(err)
	}
	if got := instance.FirstMessage(instance.Validate(request{Email: "name@example.com"})); got != "email 使用项目邮箱格式" {
		t.Fatal(got)
	}
}

func TestFieldLabelsAndErrorChain(t *testing.T) {
	instance, err := New(WithFieldTagName("label"))
	if err != nil {
		t.Fatal(err)
	}
	type request struct {
		Name string `json:"name" label:"姓名" validate:"required"`
	}
	raw := instance.Engine().Struct(request{})
	translated := instance.Translate(fmt.Errorf("wrapped: %w", raw))
	var fields validator.ValidationErrors
	if !errors.As(translated, &fields) || !strings.Contains(translated.Error(), "姓名") {
		t.Fatalf("missing field name or original error: %v", translated)
	}
	if instance.Translate(translated) != translated {
		t.Fatal("translated errors must not be translated twice")
	}
	parseErr := errors.New("invalid JSON")
	if instance.Translate(parseErr) != parseErr || instance.FirstMessage(nil) != "" {
		t.Fatal("non-validation errors and nil must pass through")
	}
}

func TestInvalidConfiguration(t *testing.T) {
	if _, err := Configure(nil); err == nil {
		t.Fatal("nil engine accepted")
	}
	for _, rule := range []Rule{{Tag: " "}, {Tag: "custom"}} {
		if _, err := New(WithRules(rule)); err == nil {
			t.Fatalf("invalid rule accepted: %+v", rule)
		}
	}
}

func TestRuleBoundaries(t *testing.T) {
	instance, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tag, value string
		valid      bool
	}{
		{"mobile", "13800138000", true}, {"mobile", "+8613800138000", false},
		{"mobile", "12345678901", false}, {"checkMobile", "1380013800", false},
		{"myEmail", "name@example.com", true}, {"checkEmail", "Name <name@example.com>", false},
		{"checkEmail", "name@example.com ", false}, {"date", "2024-02-29", true},
		{"date", "2025-02-29", false}, {"dateTime", "2026-10-15T10:00:00", false},
		{"decimal", "1.23", true}, {"decimal", "1.234", false},
		{"afterDate", time.Now().In(time.Local).AddDate(0, 0, 1).Format(time.DateOnly), true},
		{"afterDate", time.Now().In(time.Local).AddDate(0, 0, -1).Format(time.DateOnly), false},
	} {
		if err := instance.Engine().Var(tc.value, tc.tag); (err == nil) != tc.valid {
			t.Errorf("Var(%q, %q): %v, want valid=%v", tc.value, tc.tag, err, tc.valid)
		}
	}
}
