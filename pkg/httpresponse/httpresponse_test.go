package httpresponse

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/pkg/apierror"
	"github.com/gofiber/fiber/v2"
)

func TestError(t *testing.T) {
	for name, tc := range map[string]struct {
		err         error
		path        string
		wantStatus  int
		wantCode    string
		wantMessage string
		wantDetails bool
	}{
		"fixed unauthorized message":  {errs.Unauthorized("wrong password"), "/", 401, "UNAUTHORIZED", "Authentication credentials are invalid.", false},
		"internal details hidden":     {errs.Internal("db password leaked"), "/", 500, "INTERNAL_ERROR", "An unexpected error occurred.", false},
		"plain error hidden":          {errors.New("boom"), "/", 500, "INTERNAL_ERROR", "An unexpected error occurred.", false},
		"caller message passes":       {errs.NotFound("Employee not found."), "/", 404, "NOT_FOUND", "Employee not found.", false},
		"validation keeps details":    {apierror.Validation([]apierror.FieldError{{Field: "email", Message: "Required."}}), "/", 422, "VALIDATION_ERROR", "The request is invalid.", true},
		"rate limited":                {apierror.RateLimited, "/", 429, "RATE_LIMITED", "Rate limit exceeded.", false},
		"unknown route uses envelope": {nil, "/missing", 404, "NOT_FOUND", "Resource not found.", false},
	} {
		t.Run(name, func(t *testing.T) {
			app := fiber.New(fiber.Config{ErrorHandler: Error})
			app.Get("/", func(*fiber.Ctx) error { return tc.err })
			request := httptest.NewRequest(http.MethodGet, tc.path, nil)
			request.Header.Set(fiber.HeaderXRequestID, "req-1")
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
					Details any    `json:"details"`
				} `json:"error"`
				RequestID string `json:"requestId"`
			}
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.wantStatus || body.Error.Code != tc.wantCode || body.Error.Message != tc.wantMessage ||
				(body.Error.Details != nil) != tc.wantDetails || body.RequestID != "req-1" {
				t.Fatalf("got %d %+v", response.StatusCode, body)
			}
		})
	}
}
