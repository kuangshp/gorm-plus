package validation

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
)

func TestValidateUsesJSONFieldNameAndFirstError(t *testing.T) {
	type request struct {
		StartTime string `json:"startTime" validate:"required,dateTime"`
		Mobile    string `json:"mobile" validate:"required,checkMobile"`
	}

	err := Validate(&request{})
	if err == nil {
		t.Fatal("Validate() error = nil, want validation error")
	}
	if got := err.Error(); !strings.Contains(got, "startTime") {
		t.Fatalf("Validate() error = %q, want JSON field name", got)
	}
	if !strings.Contains(err.Error(), "必填") {
		t.Fatalf("Validate() did not translate required rule: %v", err)
	}
	var validationErr *Error
	if !errors.As(err, &validationErr) {
		t.Fatalf("Validate() error type = %T, want *Error", err)
	}
	if validationErr.Tag != "required" {
		t.Fatalf("Validate() tag = %q, want required", validationErr.Tag)
	}
}

func TestCommonRules(t *testing.T) {
	tests := []struct {
		name  string
		tag   string
		value string
		valid bool
	}{
		{name: "date", tag: "date", value: "2026-08-06", valid: true},
		{name: "invalid calendar date", tag: "date", value: "2026-02-30", valid: false},
		{name: "date time", tag: "dateTime", value: "2026-08-06 12:13:14", valid: true},
		{name: "invalid date time", tag: "dateTime", value: "2026-08-06 25:13:14", valid: false},
		{name: "mobile", tag: "checkMobile", value: "13800138000", valid: true},
		{name: "mobile alias", tag: "mobile", value: "13800138000", valid: true},
		{name: "email", tag: "checkEmail", value: "dev@example.com", valid: true},
		{name: "decimal", tag: "decimal", value: "12.30", valid: true},
		{name: "too many decimal places", tag: "decimal", value: "12.345", valid: false},
	}

	instance, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := instance.Engine()
			err := engine.Var(test.value, test.tag)
			if (err == nil) != test.valid {
				t.Fatalf("Var(%q, %q) error = %v, valid = %v", test.value, test.tag, err, test.valid)
			}
		})
	}
}

func TestConfigureKeepsExistingValidationTagName(t *testing.T) {
	engine := validator.New()
	engine.SetTagName("binding")
	instance, err := Configure(engine)
	if err != nil {
		t.Fatalf("Configure() error = %v", err)
	}
	type request struct {
		StartTime string `json:"startTime" binding:"required,dateTime" validate:"-"`
	}
	if err := instance.Validate(&request{}); err == nil {
		t.Fatal("Validate() error = nil, want binding tag validation error")
	}
}

func TestHasRules(t *testing.T) {
	type nestedRequest struct {
		StartTime string `json:"startTime" validate:"omitempty,dateTime"`
	}
	tests := []struct {
		name  string
		value any
		want  bool
	}{
		{
			name: "direct rule",
			value: struct {
				Name string `json:"name" validate:"required"`
			}{},
			want: true,
		},
		{
			name: "nested rule",
			value: struct {
				Filter nestedRequest `json:"filter"`
			}{},
			want: true,
		},
		{
			name: "no rule",
			value: struct {
				Name string `json:"name"`
			}{},
			want: false,
		},
		{
			name: "ignored nested field",
			value: struct {
				Filter nestedRequest `json:"filter" validate:"-"`
			}{},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := HasRules(test.value); got != test.want {
				t.Fatalf("HasRules() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestHasRulesForTag(t *testing.T) {
	type request struct {
		Name string `json:"name" binding:"required"`
	}
	if HasRules(request{}) {
		t.Fatal("HasRules() = true, want validate tags only")
	}
	if !HasRulesForTag(request{}, "binding") {
		t.Fatal("HasRulesForTag() = false, want binding rule")
	}
}

func TestCustomRuleAndWrappedError(t *testing.T) {
	instance, err := New(WithRules(Rule{
		Tag:     "startsWithA",
		Message: "{0} 必须以A开头",
		Validate: func(fieldLevel validator.FieldLevel) bool {
			return strings.HasPrefix(fieldLevel.Field().String(), "A")
		},
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	type request struct {
		Name string `json:"name" validate:"startsWithA"`
	}
	rawErr := instance.Engine().Struct(&request{Name: "Bob"})
	message := instance.FirstMessage(fmt.Errorf("wrapped: %w", rawErr))
	if message != "name 必须以A开头" {
		t.Fatalf("FirstMessage() = %q, want custom translated message", message)
	}
}
