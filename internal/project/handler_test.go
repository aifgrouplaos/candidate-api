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

// routeClient sends authenticated requests to the Project routes backed by a memory repository.
type routeClient struct {
	t     *testing.T
	app   *fiber.App
	token contract.Token
}

func newRouteClient(t *testing.T) *routeClient {
	token := jwt.New(config.JWT{Secret: "test-secret"})
	app := fiber.New(fiber.Config{ErrorHandler: httpresponse.Error})
	NewProjectHandler(NewProjectUsecase(newMemoryRepository())).RegisterRoutes(app, auth.Authentication(token, activeSessions{}))
	return &routeClient{t: t, app: app, token: token}
}

func (c *routeClient) do(method, path, payload, key string, actor auth.Principal) (int, map[string]json.RawMessage) {
	c.t.Helper()
	bearer, err := c.token.Sign(contract.Claims{"sub": actor.UserID, "tenantId": actor.TenantID, "role": string(actor.Role), "sid": "s1"}, time.Minute)
	if err != nil {
		c.t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+bearer)
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := c.app.Test(request, -1)
	if err != nil {
		c.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		c.t.Fatalf("%s %s: invalid JSON %s", method, path, raw)
	}
	return response.StatusCode, decoded
}

func TestProjectRoutesContract(t *testing.T) {
	do := newRouteClient(t).do
	body, err := json.Marshal(validInput())
	if err != nil {
		t.Fatal(err)
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

	status, list := do(http.MethodGet, "/projects?search=HR", "", "", employeeA)
	data := string(list["data"])
	if status != fiber.StatusOK || !strings.HasPrefix(data, `[{"id":"`+project.ID+`"`) || strings.Contains(data, `"phases"`) ||
		string(list["meta"]) != `{"page":1,"limit":20,"total":1,"totalPages":1}` {
		t.Errorf("list: %d %s %s", status, data, list["meta"])
	}

	if status, got := do(http.MethodDelete, "/projects/"+project.ID, "", "", adminA); status != fiber.StatusOK || string(got["data"]) != "null" {
		t.Fatalf("delete: %d %s", status, got["data"])
	}
	if status, _ := do(http.MethodGet, "/projects/"+project.ID, "", "", adminA); status != fiber.StatusNotFound {
		t.Fatalf("get after delete: %d", status)
	}
}

func TestLookupRoutes(t *testing.T) {
	do := newRouteClient(t).do
	for path, want := range map[string]string{
		"/lookups/task-types": `["feature","bug"]`,
		"/lookups/priorities": `["low","medium","high","critical"]`,
	} {
		if status, got := do(http.MethodGet, path, "", "", employeeA); status != fiber.StatusOK || string(got["data"]) != want {
			t.Errorf("%s: %d %s", path, status, got["data"])
		}
	}
}
