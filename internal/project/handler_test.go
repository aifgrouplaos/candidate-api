package project

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

func TestProjectRoutesContract(t *testing.T) {
	token := jwt.New(config.JWT{Secret: "test-secret"})
	app := fiber.New(fiber.Config{ErrorHandler: httpresponse.Error})
	NewProjectHandler(NewProjectUsecase(newMemoryRepository())).RegisterRoutes(app, auth.Authentication(token, activeSessions{}))
	bearer := func(p auth.Principal) string {
		value, err := token.Sign(contract.Claims{"sub": p.UserID, "tenantId": p.TenantID, "role": string(p.Role), "sid": "s1"}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return "Bearer " + value
	}
	body, err := json.Marshal(validInput())
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, payload, key string, actor auth.Principal) (int, map[string]json.RawMessage) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", bearer(actor))
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
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

	status, created := do(http.MethodPost, "/projects", string(body), "key-1", adminA)
	var project ProjectView
	if err := json.Unmarshal(created["data"], &project); status != fiber.StatusCreated || err != nil || project.ID == "" {
		t.Fatalf("create: %d %s", status, created["data"])
	}
	if status, replay := do(http.MethodPost, "/projects", string(body), "key-1", adminA); status != fiber.StatusCreated || string(replay["data"]) != string(created["data"]) {
		t.Fatalf("replay: %d %s", status, replay["data"])
	}

	cases := []struct {
		name, method, path, body, key string
		actor                         auth.Principal
		status                        int
	}{
		{"missing key", http.MethodPost, "/projects", string(body), "", adminA, fiber.StatusBadRequest},
		{"malformed JSON", http.MethodPost, "/projects", "{", "key-2", adminA, fiber.StatusBadRequest},
		{"employee create", http.MethodPost, "/projects", string(body), "key-3", employeeA, fiber.StatusForbidden},
		{"changed payload", http.MethodPost, "/projects", strings.Replace(string(body), "HR system", "Other", 1), "key-1", adminA, fiber.StatusConflict},
		{"invalid payload", http.MethodPost, "/projects", `{"phases":[]}`, "key-4", adminA, fiber.StatusUnprocessableEntity},
		{"employee get", http.MethodGet, "/projects/" + project.ID, "", "", employeeA, fiber.StatusOK},
		{"other tenant get", http.MethodGet, "/projects/" + project.ID, "", "", adminB, fiber.StatusNotFound},
		{"bad list query", http.MethodGet, "/projects?page=x", "", "", employeeA, fiber.StatusBadRequest},
		{"employee delete", http.MethodDelete, "/projects/" + project.ID, "", "", employeeA, fiber.StatusForbidden},
		{"other tenant delete", http.MethodDelete, "/projects/" + project.ID, "", "", adminB, fiber.StatusNotFound},
	}
	for _, c := range cases {
		if status, _ := do(c.method, c.path, c.body, c.key, c.actor); status != c.status {
			t.Errorf("%s: status %d, want %d", c.name, status, c.status)
		}
	}

	for path, want := range map[string]string{
		"/projects?search=HR": `[{"id":"` + project.ID + `"`,
		"/lookups/task-types": `["feature","bug"]`,
		"/lookups/priorities": `["low","medium","high","critical"]`,
	} {
		status, got := do(http.MethodGet, path, "", "", employeeA)
		if status != fiber.StatusOK || !strings.HasPrefix(string(got["data"]), want) || strings.Contains(string(got["data"]), `"phases"`) {
			t.Errorf("%s: %d %s", path, status, got["data"])
		}
	}
	if _, got := do(http.MethodGet, "/projects", "", "", employeeA); string(got["meta"]) != `{"page":1,"limit":20,"total":1,"totalPages":1}` {
		t.Errorf("list meta = %s", got["meta"])
	}

	if status, got := do(http.MethodDelete, "/projects/"+project.ID, "", "", adminA); status != fiber.StatusOK || string(got["data"]) != "null" {
		t.Fatalf("delete: %d %s", status, got["data"])
	}
	if status, _ := do(http.MethodGet, "/projects/"+project.ID, "", "", adminA); status != fiber.StatusNotFound {
		t.Fatalf("get after delete: %d", status)
	}
}
