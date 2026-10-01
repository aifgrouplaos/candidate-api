package auth

import (
	"strings"

	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/pkg/httpresponse"
	"github.com/gofiber/fiber/v2"
)

type AuthHandler struct{ usecase AuthUsecase }

func NewAuthHandler(usecase AuthUsecase) *AuthHandler { return &AuthHandler{usecase: usecase} }

// RegisterRoutes takes the protected-route middleware as handlers because a Fiber
// Group("", middleware) on the same prefix would also run it on login and refresh.
func (h *AuthHandler) RegisterRoutes(router fiber.Router, loginLimit, refreshLimit fiber.Handler, protected ...fiber.Handler) {
	router.Post("/auth/login", loginLimit, h.Login)
	router.Post("/auth/refresh", refreshLimit, h.Refresh)
	router.Post("/auth/logout", append(protected, h.Logout)...)
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var input Credentials
	if err := c.BodyParser(&input); err != nil {
		return errs.ErrBadRequest
	}
	result, err := h.usecase.Login(c.UserContext(), input)
	if err != nil {
		return err
	}
	return httpresponse.Success(c, result)
}

func (h *AuthHandler) Refresh(c *fiber.Ctx) error {
	var input RefreshInput
	if err := c.BodyParser(&input); err != nil {
		return errs.ErrBadRequest
	}
	result, err := h.usecase.Refresh(c.UserContext(), input)
	if err != nil {
		return err
	}
	return httpresponse.Success(c, result)
}

func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	var input RefreshInput
	if err := c.BodyParser(&input); err != nil {
		return errs.ErrBadRequest
	}
	principal, ok := PrincipalFrom(c)
	if !ok {
		return errs.ErrUnauthorized
	}
	if err := h.usecase.Logout(c.UserContext(), principal.UserID, principal.SessionID, input); err != nil {
		return err
	}
	return httpresponse.Success(c, nil)
}

type Principal struct {
	UserID    string
	TenantID  string
	SessionID string
	Role      Role
}

const principalKey = "authPrincipal"

func PrincipalFrom(c *fiber.Ctx) (Principal, bool) {
	principal, ok := c.Locals(principalKey).(Principal)
	return principal, ok
}

// Authentication verifies token expiry and requires the identity, tenant, and role claims.
func Authentication(token contract.Token, sessions SessionValidator) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if token == nil || sessions == nil {
			return errs.ErrUnauthorized
		}
		raw, ok := strings.CutPrefix(c.Get(fiber.HeaderAuthorization), "Bearer ")
		if !ok {
			return errs.ErrUnauthorized
		}
		claims, err := token.Verify(raw)
		if err != nil {
			return errs.ErrUnauthorized
		}
		userID, _ := claims[claimUserID].(string)
		tenantID, _ := claims[claimTenantID].(string)
		role, _ := claims[claimRole].(string)
		sessionID, _ := claims[claimSessionID].(string)
		if userID == "" || tenantID == "" || sessionID == "" || !Role(role).Valid() {
			return errs.ErrUnauthorized
		}
		active, err := sessions.SessionActive(c.UserContext(), sessionID, userID, tenantID)
		if err != nil {
			return errs.Internal("could not verify authentication session")
		}
		if !active {
			return errs.ErrUnauthorized
		}
		c.Locals(principalKey, Principal{UserID: userID, TenantID: tenantID, SessionID: sessionID, Role: Role(role)})
		return c.Next()
	}
}
