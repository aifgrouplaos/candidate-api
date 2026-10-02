package employee

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
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

func TestEmployeeRoutesContract(t *testing.T) {
	token := jwt.New(config.JWT{Secret: "test-secret"})
	app := fiber.New(fiber.Config{ErrorHandler: httpresponse.Error})
	NewEmployeeHandler(NewEmployeeUsecase(newMemoryRepository(), nil, "")).RegisterRoutes(app, auth.Authentication(token, activeSessions{}))
	bearer := func(p auth.Principal) string {
		value, err := token.Sign(contract.Claims{"sub": p.UserID, "tenantId": p.TenantID, "role": string(p.Role), "sid": "s1"}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return "Bearer " + value
	}
	call := func(method, path, authorization, body string) (int, map[string]json.RawMessage) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", fiber.MIMEApplicationJSON)
		request.Header.Set("Authorization", authorization)
		response, err := app.Test(request)
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

	if status, _ := call(http.MethodGet, "/employees", "", ""); status != http.StatusUnauthorized {
		t.Errorf("anonymous list = %d", status)
	}
	if status, _ := call(http.MethodGet, "/departments", "", ""); status != http.StatusUnauthorized {
		t.Errorf("anonymous departments = %d", status)
	}
	status, body := call(http.MethodPost, "/employees", bearer(adminA), `{"fullName":"New Person","email":"new@example.test","password":"password-123","role":"admin"}`)
	if status != http.StatusCreated || strings.Contains(string(body["data"]), "password") || strings.Contains(string(body["data"]), "tenant") {
		t.Errorf("create = %d %s", status, body["data"])
	}
	status, body = call(http.MethodGet, "/employees?limit=2&sortBy=fullName", bearer(adminA), "")
	if status != http.StatusOK || string(body["meta"]) != `{"page":1,"limit":2,"total":3,"totalPages":2}` {
		t.Errorf("list = %d meta %s", status, body["meta"])
	}
	if status, body = call(http.MethodPatch, "/employees/e1", bearer(employeeA), `{"version":1,"role":"admin"}`); status != http.StatusForbidden || body["error"] == nil {
		t.Errorf("role patch = %d %s", status, body["error"])
	}
	if status, _ = call(http.MethodPatch, "/employees/e1", bearer(employeeA), `{"version":"one"}`); status != http.StatusBadRequest {
		t.Errorf("malformed patch = %d", status)
	}
	if status, body = call(http.MethodDelete, "/employees/e2", bearer(adminA), ""); status != http.StatusOK || string(body["data"]) != "null" {
		t.Errorf("delete = %d %s", status, body["data"])
	}
	if status, body = call(http.MethodGet, "/departments", bearer(employeeA), ""); status != http.StatusOK || !strings.Contains(string(body["data"]), `"name":"IT"`) {
		t.Errorf("departments = %d %s", status, body["data"])
	}
}

func TestUploadAvatarRoute(t *testing.T) {
	token := jwt.New(config.JWT{Secret: "test-secret"})
	repo := newMemoryRepository()
	files := newMemoryStorage()
	app := fiber.New(fiber.Config{ErrorHandler: httpresponse.Error})
	NewEmployeeHandler(NewEmployeeUsecase(repo, files, avatarBucket)).RegisterRoutes(app, auth.Authentication(token, activeSessions{}))
	bearer := func(p auth.Principal) string {
		value, err := token.Sign(contract.Claims{"sub": p.UserID, "tenantId": p.TenantID, "role": string(p.Role), "sid": "s1"}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return "Bearer " + value
	}
	send := func(authorization, contentType string, data []byte) (int, map[string]json.RawMessage) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		header := textproto.MIMEHeader{}
		header.Set("Content-Disposition", `form-data; name="file"; filename="avatar.bin"`)
		header.Set("Content-Type", contentType)
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/employees/e1/avatar", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.Header.Set("Authorization", authorization)
		response, err := app.Test(request, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("invalid JSON %s", raw)
		}
		return response.StatusCode, decoded
	}

	if status, _ := send("", "image/jpeg", imageBytes(16, jpegMagic)); status != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", status)
	}
	status, body := send(bearer(employeeA), "image/jpeg", imageBytes(16, jpegMagic))
	if status != http.StatusOK || !strings.Contains(string(body["data"]), `"avatarUrl":"https://files.example.test/`) || strings.Contains(string(body["data"]), `"avatarUrl":"avatars/`) {
		t.Fatalf("upload = %d %s", status, body["data"])
	}
	if status, body = send(bearer(employeeA), "image/gif", []byte("GIF89a")); status != http.StatusUnprocessableEntity || !strings.Contains(string(body["error"]), `"file"`) {
		t.Fatalf("gif = %d %s", status, body["error"])
	}
	request := httptest.NewRequest(http.MethodPost, "/employees/e1/avatar", strings.NewReader(`{"file":"nope"}`))
	request.Header.Set("Content-Type", fiber.MIMEApplicationJSON)
	request.Header.Set("Authorization", bearer(employeeA))
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing file = %d", response.StatusCode)
	}
	var otherBody bytes.Buffer
	writer := multipart.NewWriter(&otherBody)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="avatar.bin"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(imageBytes(16, pngMagic)); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	other := httptest.NewRequest(http.MethodPost, "/employees/e2/avatar", &otherBody)
	other.Header.Set("Content-Type", writer.FormDataContentType())
	other.Header.Set("Authorization", bearer(employeeA))
	response, err = app.Test(other, -1)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("other employee = %d %s", response.StatusCode, raw)
	}
	files.failUpload = true
	if status, body = send(bearer(adminA), "image/png", imageBytes(16, pngMagic)); status != http.StatusInternalServerError || strings.Contains(string(body["error"]), "storage") {
		t.Fatalf("storage failure = %d %s", status, body["error"])
	}
}
