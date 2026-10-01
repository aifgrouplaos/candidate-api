// Package httpresponse writes the API_SPEC success and error envelopes.
package httpresponse

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// fixedMessages replace caller messages so responses never reveal which credential
// or rate-limit bucket failed, or any internal detail.
var fixedMessages = map[string]string{
	"UNAUTHORIZED":   "Authentication credentials are invalid.",
	"TOKEN_EXPIRED":  "The access token has expired.",
	"RATE_LIMITED":   "Rate limit exceeded.",
	"INTERNAL_ERROR": "An unexpected error occurred.",
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

func Success(c *fiber.Ctx, data any) error {
	setRequestID(c)
	return c.JSON(fiber.Map{"data": data})
}

func Page(c *fiber.Ctx, data, meta any) error {
	setRequestID(c)
	return c.JSON(fiber.Map{"data": data, "meta": meta})
}

// Error is the app-wide fiber.ErrorHandler; handlers and middleware just return errors.
func Error(c *fiber.Ctx, err error) error {
	setRequestID(c)
	status, code, message, details := fiber.StatusInternalServerError, "INTERNAL_ERROR", "", any(nil)
	var fiberErr *fiber.Error
	if appErr, ok := errs.IsAppError(err); ok {
		status, code, message = appErr.Status, appErr.Code, appErr.Message
		if code == "VALIDATION_ERROR" {
			details = appErr.Data
		}
	} else if errors.As(err, &fiberErr) {
		status, code = fiberErr.Code, "BAD_REQUEST"
		if status == fiber.StatusNotFound {
			code, message = "NOT_FOUND", "Resource not found."
		}
	} else {
		slog.Error("request failed", "error", err, "path", c.Path())
	}
	if status >= fiber.StatusInternalServerError {
		status, code, details = fiber.StatusInternalServerError, "INTERNAL_ERROR", nil
	}
	if fixed, ok := fixedMessages[code]; ok {
		message = fixed
	}
	if message == "" {
		message = "The request could not be processed."
	}
	return c.Status(status).JSON(fiber.Map{
		"error":     errorBody{Code: code, Message: message, Details: details},
		"requestId": c.GetRespHeader(fiber.HeaderXRequestID),
	})
}

func setRequestID(c *fiber.Ctx) {
	id := strings.TrimSpace(c.Get(fiber.HeaderXRequestID))
	if id == "" {
		id = uuid.NewString()
	}
	c.Set(fiber.HeaderXRequestID, id)
}
