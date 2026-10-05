package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BounkhongDev/bkgo/adapter/jwt"
	"github.com/BounkhongDev/bkgo/config"
	"github.com/BounkhongDev/bkgo/contract"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/httpresponse"
	"github.com/gofiber/fiber/v2"
)

type activeSessions struct{}

func (activeSessions) SessionActive(context.Context, string, string, string) (bool, error) {
	return true, nil
}

func TestChatRoutesContract(t *testing.T) {
	token := jwt.New(config.JWT{Secret: "test-secret"})
	app := fiber.New(fiber.Config{ErrorHandler: httpresponse.Error})
	NewChatHandler(NewChatUsecase(newRepositoryWithMessages(3), signAvatar)).RegisterRoutes(app, auth.Authentication(token, activeSessions{}))

	do := func(method, path string, actor *auth.Principal) (int, map[string]json.RawMessage) {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		if actor != nil {
			bearer, err := token.Sign(contract.Claims{"sub": actor.UserID, "tenantId": actor.TenantID, "role": string(actor.Role), "sid": "s1"}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		response, err := app.Test(request, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("%s %s: invalid JSON %s", method, path, raw)
		}
		return response.StatusCode, decoded
	}

	cases := []struct {
		method, path string
		actor        *auth.Principal
		status       int
		keys         []string
	}{
		{http.MethodGet, "/chat/conversations", nil, fiber.StatusUnauthorized, []string{"error", "requestId"}},
		{http.MethodGet, "/chat/conversations?page=1&limit=20&unreadOnly=true", &adminA, fiber.StatusOK, []string{"data", "meta"}},
		{http.MethodGet, "/chat/conversations?unreadOnly=maybe", &adminA, fiber.StatusBadRequest, []string{"error", "requestId"}},
		{http.MethodPost, "/chat/conversations", &employeeA, fiber.StatusOK, []string{"data"}},
		{http.MethodPost, "/chat/conversations", &adminA, fiber.StatusForbidden, []string{"error", "requestId"}},
		{http.MethodGet, "/chat/conversations/c1", &employeeA, fiber.StatusOK, []string{"data"}},
		{http.MethodGet, "/chat/conversations/c1", &adminB, fiber.StatusNotFound, []string{"error", "requestId"}},
		{http.MethodGet, "/chat/conversations/c1/messages?limit=2&before=m3", &adminA, fiber.StatusOK, []string{"data", "meta"}},
		{http.MethodGet, "/chat/conversations/c1/messages?limit=abc", &adminA, fiber.StatusBadRequest, []string{"error", "requestId"}},
		{http.MethodGet, "/chat/conversations/c1/messages?before=m3&after=m1", &adminA, fiber.StatusUnprocessableEntity, []string{"error", "requestId"}},
	}
	for _, tc := range cases {
		status, body := do(tc.method, tc.path, tc.actor)
		if status != tc.status || len(body) != len(tc.keys) {
			t.Fatalf("%s %s: %d %v", tc.method, tc.path, status, body)
		}
		for _, key := range tc.keys {
			if _, ok := body[key]; !ok {
				t.Fatalf("%s %s: missing %q in %v", tc.method, tc.path, key, body)
			}
		}
	}

	_, page := do(http.MethodGet, "/chat/conversations/c1/messages?limit=2&before=m3", &adminA)
	if string(page["meta"]) != `{"hasMoreBefore":false,"hasMoreAfter":true,"nextBefore":null}` {
		t.Fatalf("messages meta = %s", page["meta"])
	}
}
