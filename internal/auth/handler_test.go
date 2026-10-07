package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BounkhongDev/bkgo/adapter/jwt"
	"github.com/BounkhongDev/bkgo/config"
	"github.com/BounkhongDev/bkgo/contract"
	"github.com/aifgrouplaos/candidate-api/pkg/httpresponse"
	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

func TestLoginEndpointContract(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("password-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	repo := &memoryAuthRepository{
		user:   &User{ID: "user-1", TenantID: "tenant-1", Email: "admin@example.test", PasswordHash: string(passwordHash), Role: RoleAdmin, Active: true},
		tokens: make(map[string]*RefreshToken), sessions: make(map[string]*AuthSession),
	}
	token := jwt.New(config.JWT{Secret: "test-secret"})
	handler := NewAuthHandler(NewAuthUsecase(repo, token))
	app := fiber.New(fiber.Config{ErrorHandler: httpresponse.Error})
	pass := func(c *fiber.Ctx) error { return c.Next() }
	handler.RegisterRoutes(app, pass, pass, Authentication(token, repo))

	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"admin@example.test","password":"password-123"}`))
	request.Header.Set("Content-Type", fiber.MIMEApplicationJSON)
	request.Header.Set("X-Request-Id", "test-request")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", response.StatusCode)
	}
	if response.Header.Get("X-Request-Id") != "test-request" {
		t.Fatalf("request ID header = %q", response.Header.Get("X-Request-Id"))
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || len(body["data"]) == 0 {
		t.Fatalf("login response must contain only data: %s", body)
	}
	var session Session
	if err := json.Unmarshal(body["data"], &session); err != nil {
		t.Fatal(err)
	}
	if session.AccessToken == "" || session.RefreshToken == "" || session.TokenType != "Bearer" || session.ExpiresIn != 1800 {
		t.Fatalf("invalid session response: %+v", session)
	}

	request = httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"admin@example.test","password":"wrong"}`))
	request.Header.Set("Content-Type", fiber.MIMEApplicationJSON)
	response, err = app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid credentials status = %d, want 401", response.StatusCode)
	}
	var failure struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		RequestID string `json:"requestId"`
	}
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error.Code != "UNAUTHORIZED" || failure.RequestID == "" {
		t.Fatalf("invalid credential response: %+v", failure)
	}
}

func TestLoginAccessTokenAuthenticatesLogout(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("password-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	repo := &memoryAuthRepository{
		user:   &User{ID: "user-1", TenantID: "tenant-1", Email: "admin@example.test", PasswordHash: string(passwordHash), Role: RoleAdmin, Active: true},
		tokens: make(map[string]*RefreshToken), sessions: make(map[string]*AuthSession),
	}
	token := jwt.New(config.JWT{Secret: "test-secret"})
	app := fiber.New(fiber.Config{ErrorHandler: httpresponse.Error})
	pass := func(c *fiber.Ctx) error { return c.Next() }
	NewAuthHandler(NewAuthUsecase(repo, token)).RegisterRoutes(app, pass, pass, Authentication(token, repo))

	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"admin@example.test","password":"password-123"}`))
	request.Header.Set("Content-Type", fiber.MIMEApplicationJSON)
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", response.StatusCode)
	}
	var login struct{ Data Session }
	if err := json.NewDecoder(response.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}

	request = httptest.NewRequest(http.MethodPost, "/auth/logout", strings.NewReader(`{"refreshToken":"`+login.Data.RefreshToken+`"}`))
	request.Header.Set("Content-Type", fiber.MIMEApplicationJSON)
	request.Header.Set("Authorization", "Bearer "+login.Data.AccessToken)
	response, err = app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("logout status = %d, want 200", response.StatusCode)
	}
}

func TestMeReturnsTheCallersEmployeeID(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("password-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	employeeID := "employee-1"
	repo := &memoryAuthRepository{
		user:       &User{ID: "user-2", TenantID: "tenant-1", Email: "somchai@example.test", FullName: "Somchai", PasswordHash: string(passwordHash), Role: RoleEmployee, Active: true},
		employeeID: &employeeID,
		tokens:     make(map[string]*RefreshToken), sessions: make(map[string]*AuthSession),
	}
	token := jwt.New(config.JWT{Secret: "test-secret"})
	app := fiber.New(fiber.Config{ErrorHandler: httpresponse.Error})
	pass := func(c *fiber.Ctx) error { return c.Next() }
	NewAuthHandler(NewAuthUsecase(repo, token)).RegisterRoutes(app, pass, pass, Authentication(token, repo))

	request := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"email":"somchai@example.test","password":"password-123"}`))
	request.Header.Set("Content-Type", fiber.MIMEApplicationJSON)
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var login struct{ Data Session }
	if err := json.NewDecoder(response.Body).Decode(&login); err != nil {
		t.Fatal(err)
	}
	if login.Data.User == nil || login.Data.User.EmployeeID == nil || *login.Data.User.EmployeeID != employeeID {
		t.Fatalf("login user = %+v", login.Data.User)
	}

	request = httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	response, err = app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous /auth/me status = %d, want 401", response.StatusCode)
	}

	request = httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+login.Data.AccessToken)
	response, err = app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var me struct{ Data AuthenticatedUser }
	if err := json.NewDecoder(response.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || me.Data.ID != "user-2" || me.Data.TenantID != "tenant-1" ||
		me.Data.Role != RoleEmployee || me.Data.FullName != "Somchai" || me.Data.EmployeeID == nil || *me.Data.EmployeeID != employeeID {
		t.Fatalf("/auth/me status %d body %+v", response.StatusCode, me.Data)
	}
}

func TestAuthenticationRequiresValidIdentityTenantRoleAndExpiry(t *testing.T) {
	token := jwt.New(config.JWT{Secret: "test-secret"})
	app := fiber.New(fiber.Config{ErrorHandler: httpresponse.Error})
	repo := &memoryAuthRepository{
		user:     &User{ID: "user-1", TenantID: "tenant-1", Active: true},
		sessions: map[string]*AuthSession{"session-1": {ID: "session-1", UserID: "user-1", TenantID: "tenant-1"}},
	}
	app.Get("/protected", Authentication(token, repo), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	cases := []struct {
		name   string
		claims contract.Claims
		ttl    time.Duration
		status int
	}{
		{name: "valid", claims: contract.Claims{"sub": "user-1", "tenantId": "tenant-1", "role": "admin", "sid": "session-1"}, ttl: time.Hour, status: http.StatusOK},
		{name: "missing tenant", claims: contract.Claims{"sub": "user-1", "role": "admin"}, ttl: time.Hour, status: http.StatusUnauthorized},
		{name: "missing role", claims: contract.Claims{"sub": "user-1", "tenantId": "tenant-1"}, ttl: time.Hour, status: http.StatusUnauthorized},
		{name: "invalid role", claims: contract.Claims{"sub": "user-1", "tenantId": "tenant-1", "role": "operator"}, ttl: time.Hour, status: http.StatusUnauthorized},
		{name: "expired", claims: contract.Claims{"sub": "user-1", "tenantId": "tenant-1", "role": "admin"}, ttl: -time.Minute, status: http.StatusUnauthorized},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			value, err := token.Sign(test.claims, test.ttl)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", "Bearer "+value)
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
			}
		})
	}
}
