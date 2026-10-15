package validation

import (
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
)

var decimalMatcher = regexp.MustCompile(`^\d*(\.\d{1,2})?$`)

// 沿用 go-utils 的手机号码规则，不再依赖其 k 包。
var mobileMatcher = regexp.MustCompile(`^1(?:3\d|4[4-9]|5[0-35-9]|6[67]|7[013-8]|8\d|9\d)\d{8}$`)

func defaultRules() []Rule {
	return []Rule{
		{
			Tag:      "afterDate",
			Message:  "{0} 必须要晚于当前日期",
			Validate: validateAfterDate,
		},
		{
			Tag:      "checkMobile",
			Message:  "{0} 非法的手机号码格式!",
			Validate: validateMobile,
		},
		{
			Tag:      "mobile",
			Message:  "{0} 非法的手机号码格式!",
			Validate: validateMobile,
		},
		{
			Tag:      "checkEmail",
			Message:  "{0} 非法的邮箱格式!",
			Validate: validateEmail,
		},
		{
			Tag:      "myEmail",
			Message:  "{0} 非法的邮箱格式!",
			Validate: validateEmail,
		},
		{
			Tag:      "date",
			Message:  "{0} 时间格式必须为2006-01-02!",
			Validate: validateDate,
		},
		{
			Tag:      "dateTime",
			Message:  "{0} 时间格式必须为2006-01-02 15:04:05!",
			Validate: validateDateTime,
		},
		{
			Tag:      "decimal",
			Message:  "{0} 格式必须为有效数字!",
			Validate: validateDecimal,
		},
	}
}

func validateAfterDate(fieldLevel validator.FieldLevel) bool {
	date, err := time.ParseInLocation(time.DateOnly, fieldLevel.Field().String(), time.Local)
	if err != nil {
		return false
	}
	now := time.Now().In(time.Local)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	return date.After(today)
}

func validateMobile(fieldLevel validator.FieldLevel) bool {
	return mobileMatcher.MatchString(fieldLevel.Field().String())
}

func validateEmail(fieldLevel validator.FieldLevel) bool {
	value := fieldLevel.Field().String()
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && !strings.ContainsAny(value, " \t\r\n")
}

func validateDate(fieldLevel validator.FieldLevel) bool {
	_, err := time.Parse(time.DateOnly, fieldLevel.Field().String())
	return err == nil
}

func validateDateTime(fieldLevel validator.FieldLevel) bool {
	_, err := time.Parse(time.DateTime, fieldLevel.Field().String())
	return err == nil
}

func validateDecimal(fieldLevel validator.FieldLevel) bool {
	return decimalMatcher.MatchString(fieldLevel.Field().String())
}
