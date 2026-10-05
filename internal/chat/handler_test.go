package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

	do := func(method, path string, actor *auth.Principal, body ...string) (int, map[string]json.RawMessage) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(strings.Join(body, "")))
		request.Header.Set("Content-Type", "application/json")
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

	send := `{"clientMessageId":"8f14e45f-ceea-467f-a8f1-6d1b8c2a0001","text":"Hello"}`
	writes := []struct {
		method, path, body string
		actor              *auth.Principal
		status             int
		want               string
	}{
		{http.MethodPost, "/chat/conversations/c1/messages", send, nil, fiber.StatusUnauthorized, `"UNAUTHORIZED"`},
		{http.MethodPost, "/chat/conversations/c1/messages", send, &adminB, fiber.StatusNotFound, `"NOT_FOUND"`},
		{http.MethodPost, "/chat/conversations/c1/messages", `{`, &adminA, fiber.StatusBadRequest, `"BAD_REQUEST"`},
		{http.MethodPost, "/chat/conversations/c1/messages", `{"clientMessageId":"x","text":""}`, &adminA, fiber.StatusUnprocessableEntity, `"field":"text"`},
		{http.MethodPost, "/chat/conversations/c1/messages", send, &adminA, fiber.StatusCreated, `"sequence":4`},
		{http.MethodPost, "/chat/conversations/c1/messages", send, &adminA, fiber.StatusCreated, `"sequence":4`},
		{http.MethodPost, "/chat/conversations/c1/messages", strings.Replace(send, "Hello", "Changed", 1), &adminA, fiber.StatusConflict, `"IDEMPOTENCY_CONFLICT"`},
		{http.MethodGet, "/chat/unread-count", "", &adminA, fiber.StatusOK, `{"total":3}`},
		{http.MethodPost, "/chat/conversations/c1/read", `{"lastReadMessageId":"m3"}`, &adminB, fiber.StatusNotFound, `"NOT_FOUND"`},
		{http.MethodPost, "/chat/conversations/c1/read", `{"lastReadMessageId":"nope"}`, &adminA, fiber.StatusUnprocessableEntity, `"field":"lastReadMessageId"`},
		{http.MethodPost, "/chat/conversations/c1/read", `{"lastReadMessageId":"m2"}`, &adminA, fiber.StatusOK, `null`},
		{http.MethodGet, "/chat/unread-count", "", &adminA, fiber.StatusOK, `{"total":1}`},
		{http.MethodGet, "/chat/unread-count", "", &employeeA, fiber.StatusOK, `{"total":1}`},
		{http.MethodPost, "/chat/conversations/c1/read", `{"lastReadMessageId":"m4"}`, &employeeA, fiber.StatusOK, `null`},
		{http.MethodGet, "/chat/unread-count", "", &employeeA, fiber.StatusOK, `{"total":0}`},
		{http.MethodGet, "/chat/unread-count", "", &adminA, fiber.StatusOK, `{"total":1}`},
		{http.MethodGet, "/chat/unread-count", "", nil, fiber.StatusUnauthorized, `"UNAUTHORIZED"`},
	}
	for _, tc := range writes {
		status, body := do(tc.method, tc.path, tc.actor, tc.body)
		got := string(body["data"]) + string(body["error"])
		if status != tc.status || !strings.Contains(got, tc.want) {
			t.Fatalf("%s %s %s: %d %s", tc.method, tc.path, tc.body, status, got)
		}
	}
}
