package apierror

import (
	"testing"

	"github.com/BounkhongDev/bkgo/errs"
)

func TestFieldErrors(t *testing.T) {
	var v FieldErrors
	v.Length("name", "ສະບາຍ", 1, 5, "too long")
	if err := v.Err(); err != nil {
		t.Fatalf("5 runes within 1–5 should pass, got %v", err)
	}
	v.Length("name", "", 1, 5, "required")
	v.Add("code", "taken")
	appErr, ok := errs.IsAppError(v.Err())
	if !ok || appErr.Code != "VALIDATION_ERROR" || len(appErr.Data.([]FieldError)) != 2 {
		t.Fatalf("got %+v", appErr)
	}
}
