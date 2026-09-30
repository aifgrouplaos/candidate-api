package auth

import (
	"log/slog"
	"strings"

	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type AuthHandler struct{ usecase AuthUsecase }

func NewAuthHandler(usecase AuthUsecase) *AuthHandler { return &AuthHandler{usecase: usecase} }

func (h *AuthHandler) RegisterRoutes(r fiber.Router, authenticated fiber.Handler) {
	r.Post("/auth/login", h.Login)
	r.Post("/auth/refresh", h.Refresh)
	r.Post("/auth/logout", authenticated, h.Logout)
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var input Credentials
	if err := c.BodyParser(&input); err != nil {
		return writeError(c, errs.ErrBadRequest)
	}
	result, err := h.usecase.Login(c.UserContext(), input)
	if err != nil {
		return writeError(c, err)
	}
	return writeSuccess(c, result)
}

func (h *AuthHandler) Refresh(c *fiber.Ctx) error {
	var input RefreshInput
	if err := c.BodyParser(&input); err != nil {
		return writeError(c, errs.ErrBadRequest)
	}
	result, err := h.usecase.Refresh(c.UserContext(), input)
	if err != nil {
		return writeError(c, err)
	}
	return writeSuccess(c, result)
}

func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	var input RefreshInput
	if err := c.BodyParser(&input); err != nil {
		return writeError(c, errs.ErrBadRequest)
	}
	principal, ok := PrincipalFrom(c)
	if !ok {
		return writeError(c, errs.ErrUnauthorized)
	}
	if err := h.usecase.Logout(c.UserContext(), principal.UserID, input); err != nil {
		return writeError(c, err)
	}
	return writeSuccess(c, nil)
}

type Principal struct {
	UserID   string
	TenantID string
	Role     Role
}

const principalKey = "authPrincipal"

func PrincipalFrom(c *fiber.Ctx) (Principal, bool) {
	principal, ok := c.Locals(principalKey).(Principal)
	return principal, ok
}

// Authentication verifies token expiry and requires the identity, tenant, and role claims.
func Authentication(token contract.Token) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if token == nil {
			return writeError(c, errs.ErrUnauthorized)
		}
		header := c.Get(fiber.HeaderAuthorization)
		if !strings.HasPrefix(header, "Bearer ") {
			return writeError(c, errs.ErrUnauthorized)
		}
		claims, err := token.Verify(strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			return writeError(c, errs.ErrUnauthorized)
		}
		userID, userOK := claims["sub"].(string)
		tenantID, tenantOK := claims["tenantId"].(string)
		role, roleOK := claims["role"].(string)
		if !userOK || userID == "" || !tenantOK || tenantID == "" || !roleOK ||
			(role != string(RoleAdmin) && role != string(RoleEmployee)) {
			return writeError(c, errs.ErrUnauthorized)
		}
		c.Locals(principalKey, Principal{UserID: userID, TenantID: tenantID, Role: Role(role)})
		return c.Next()
	}
}

type successResponse struct {
	Data any `json:"data"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

type errorResponse struct {
	Error     errorBody `json:"error"`
	RequestID string    `json:"requestId"`
}

func writeSuccess(c *fiber.Ctx, data any) error {
	setRequestID(c)
	return c.JSON(successResponse{Data: data})
}

func writeError(c *fiber.Ctx, err error) error {
	setRequestID(c)
	status, code, message, details := fiber.StatusInternalServerError, "INTERNAL_ERROR", "An unexpected error occurred.", any(nil)
	if appErr, ok := errs.IsAppError(err); ok {
		status, code, message = appErr.Status, appErr.Code, appErr.Message
		if status >= fiber.StatusInternalServerError {
			code, message = "INTERNAL_ERROR", "An unexpected error occurred."
		}
		if appErr.Code == "UNAUTHORIZED" {
			message = "Authentication credentials are invalid."
		}
		if appErr.Code == "VALIDATION_ERROR" {
			details = appErr.Data
		}
	} else {
		slog.Error("auth request failed", "error", err, "path", c.Path())
	}
	return c.Status(status).JSON(errorResponse{
		Error:     errorBody{Code: code, Message: message, Details: details},
		RequestID: c.GetRespHeader("X-Request-Id"),
	})
}

func setRequestID(c *fiber.Ctx) {
	id := strings.TrimSpace(c.Get("X-Request-Id"))
	if id == "" {
		id = uuid.NewString()
	}
	c.Set("X-Request-Id", id)
}
