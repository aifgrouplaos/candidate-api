// Package apierror holds the API_SPEC error codes that bkgo errs lacks. Use errs for
// BAD_REQUEST, UNAUTHORIZED, FORBIDDEN, NOT_FOUND, CONFLICT, and INTERNAL_ERROR; never
// errs.ErrUnprocessable, because the spec reports 422 as VALIDATION_ERROR.
package apierror

import (
	"net/http"

	"github.com/BounkhongDev/bkgo/errs"
)

var (
	TokenExpired        = errs.AppError{Status: http.StatusUnauthorized, Code: "TOKEN_EXPIRED", Message: "The access token has expired."}
	VersionConflict     = errs.AppError{Status: http.StatusConflict, Code: "VERSION_CONFLICT", Message: "The resource was changed by another request."}
	IdempotencyConflict = errs.AppError{Status: http.StatusConflict, Code: "IDEMPOTENCY_CONFLICT", Message: "The idempotency key was reused with a different payload."}
	RateLimited         = errs.AppError{Status: http.StatusTooManyRequests, Code: "RATE_LIMITED", Message: "Rate limit exceeded."}
)

// FieldError reports one invalid field; Field uses bracket paths such as phases[0].name.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func Validation(details []FieldError) *errs.AppError {
	return &errs.AppError{Status: http.StatusUnprocessableEntity, Code: "VALIDATION_ERROR", Message: "The request is invalid.", Data: details}
}
