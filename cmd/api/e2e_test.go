package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aifgrouplaos/candidate-api/internal/tenant"
	"github.com/fasthttp/websocket"
	"github.com/google/uuid"
)

// TestE2E runs the real API binary against the configured stack: every documented
// candidate flow for the first two manifest tenants, then a reset of the first tenant
// with isolation checks. It deletes tenant data, so it only runs through `make e2e`.
func TestE2E(t *testing.T) {
	if os.Getenv("E2E") != "1" {
		t.Skip("set E2E=1 (make e2e) to run against a local stack")
	}
	local := func(host string) bool {
		return host == "localhost" || host == "127.0.0.1" || strings.HasPrefix(host, "localhost:") || strings.HasPrefix(host, "127.0.0.1:")
	}
	if os.Getenv("APP_ENV") != "development" || !local(os.Getenv("DB_HOST")) || !local(os.Getenv("MINIO_ENDPOINT")) {
		t.Fatal("refusing: E2E resets a tenant, so it needs APP_ENV=development and local DB_HOST and MINIO_ENDPOINT")
	}
	tenants, err := readManifest(os.Getenv("E2E_MANIFEST"))
	if err != nil || len(tenants) < 2 {
		t.Fatalf("E2E_MANIFEST needs at least two tenants: %v", err)
	}
	// The API runs outside the repository so it reads only this process's environment, never .env.
	root := t.TempDir()
	bin := filepath.Join(t.TempDir(), "candidate-api")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	operator := func(args ...string) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	base := "http://localhost:" + os.Getenv("APP_PORT")
	manifest := os.Getenv("E2E_MANIFEST")

	operator("-tenant-setup", manifest)
	stop := startAPI(t, bin, root, base)
	first := exercise(t, base, tenants[0])
	second := exercise(t, base, tenants[1])
	for _, pair := range [][2]tenantRun{{first, second}, {second, first}} {
		assertIsolated(t, pair[0], pair[1])
	}

	stop()
	operator("-tenant-reset", first.tenantID)
	operator("-tenant-setup", manifest)
	startAPI(t, bin, root, base)

	for _, token := range []string{first.admin.token, first.employee.token} {
		first.admin.with(token).call("GET", "/auth/me", nil, 401, nil)
	}
	login(t, base, first.created, 401)
	admin := login(t, base, first.adminAccount, 200)
	var list []struct{ ID, Email string }
	admin.call("GET", "/employees", nil, 200, &list)
	if len(list) != 1 || list[0].Email != first.employee.email {
		t.Errorf("after reset, employees = %+v, want only the initial Employee", list)
	}
	var projects []any
	admin.call("GET", "/projects", nil, 200, &projects)
	if len(projects) != 0 {
		t.Errorf("after reset, %d Projects remain", len(projects))
	}
	employee := login(t, base, first.employeeAccount, 200)
	var conversations []struct{ LastMessage any }
	employee.call("GET", "/chat/conversations", nil, 200, &conversations)
	if len(conversations) != 1 || conversations[0].LastMessage != nil {
		t.Errorf("after reset, initial Employee conversations = %+v, want one empty", conversations)
	}

	if status := download(t, first.avatar); status != 404 {
		t.Errorf("reset tenant's avatar download status = %d, want 404", status)
	}
	if status := download(t, second.avatar); status != 200 {
		t.Errorf("other tenant's avatar download status = %d, want 200", status)
	}
	second.admin.call("GET", "/employees/"+second.createdID, nil, 200, nil)
	second.admin.call("GET", "/projects/"+second.projectID, nil, 200, nil)
	var messages []struct{ ID string }
	second.createdClient.call("GET", "/chat/conversations/"+second.conversationID+"/messages?limit=100", nil, 200, &messages)
	if !containsID(messages, second.messageID) {
		t.Error("the other tenant lost its chat message during reset")
	}
	second.admin.call("DELETE", "/projects/"+second.projectID, nil, 200, nil)
	second.admin.call("DELETE", "/employees/"+second.createdID, nil, 200, nil)
}

type tenantRun struct {
	tenantID                       string
	adminAccount, employeeAccount  credentials
	created                        credentials
	admin, employee, createdClient client
	createdID, projectID, avatar   string
	conversationID, messageID      string
}

type credentials struct{ email, password string }

// exercise runs the documented login, Employee, avatar, Project, and chat flows in one tenant.
func exercise(t *testing.T, base string, tn tenant.Tenant) tenantRun {
	t.Helper()
	run := tenantRun{
		adminAccount:    credentials{tn.Admin.Email, os.Getenv(tn.Admin.PasswordEnv)},
		employeeAccount: credentials{tn.Employee.Email, os.Getenv(tn.Employee.PasswordEnv)},
	}
	run.admin = login(t, base, run.adminAccount, 200)
	run.employee = login(t, base, run.employeeAccount, 200)
	var me struct{ TenantID, Role string }
	run.admin.call("GET", "/auth/me", nil, 200, &me)
	if me.Role != "admin" {
		t.Fatalf("%s role = %q", tn.Admin.Email, me.Role)
	}
	run.tenantID = me.TenantID

	var departments []struct{ ID string }
	run.admin.call("GET", "/departments", nil, 200, &departments)
	if len(departments) < 5 {
		t.Fatalf("got %d departments, want at least 5", len(departments))
	}
	run.admin.call("GET", "/lookups/task-types", nil, 200, nil)
	run.admin.call("GET", "/lookups/priorities", nil, 200, nil)

	run.created = credentials{fmt.Sprintf("e2e-%d@example.test", time.Now().UnixNano()), "e2e-password"}
	var employee struct {
		ID        string
		Version   int
		AvatarURL *string
	}
	run.admin.call("POST", "/employees", map[string]any{
		"fullName": "E2E Employee", "email": run.created.email, "password": run.created.password,
		"phone": "020 5555 5555", "departmentId": departments[0].ID, "position": "Developer",
		"status": "active", "hireDate": "2026-01-05",
	}, 201, &employee)
	run.createdID = employee.ID
	var found []struct{ ID string }
	run.admin.call("GET", "/employees?search="+run.created.email, nil, 200, &found)
	if !containsID(found, employee.ID) {
		t.Error("search did not find the created Employee")
	}
	run.admin.call("PATCH", "/employees/"+employee.ID, map[string]any{"position": "Lead", "version": employee.Version}, 200, nil)
	run.admin.call("PATCH", "/employees/"+employee.ID, map[string]any{"position": "Stale", "version": employee.Version}, 409, nil)

	run.createdClient = login(t, base, run.created, 200)
	run.createdClient.call("GET", "/employees", nil, 403, nil)
	run.createdClient.upload("/employees/"+employee.ID+"/avatar", &employee)
	if employee.AvatarURL == nil {
		t.Fatal("avatar upload returned no avatarUrl")
	}
	run.avatar = *employee.AvatarURL
	if status := download(t, run.avatar); status != 200 {
		t.Errorf("presigned avatar download status = %d", status)
	}

	project := map[string]any{
		"name": "E2E Project", "code": "E2E-" + uuid.NewString()[:8], "ownerId": employee.ID,
		"startDate": "2026-11-01", "endDate": "2026-12-31",
		"phases": []any{map[string]any{
			"name": "Build", "order": 1, "startDate": "2026-11-01", "endDate": "2026-11-30",
			"tasks": []any{map[string]any{
				"title": "Fix login", "type": "bug", "priority": "high", "assigneeId": employee.ID,
				"estimateHours": 4, "dueDate": "2026-11-20", "severity": "major",
			}},
		}},
	}
	key := uuid.NewString()
	var created, retried struct{ ID string }
	run.admin.withKey(key).call("POST", "/projects", project, 201, &created)
	run.admin.withKey(key).call("POST", "/projects", project, 201, &retried)
	if retried.ID != created.ID {
		t.Error("an idempotent Project retry created a second Project")
	}
	run.projectID = created.ID
	var detail struct{ Phases []struct{ Tasks []any } }
	run.createdClient.call("GET", "/projects/"+created.ID, nil, 200, &detail)
	if len(detail.Phases) != 1 || len(detail.Phases[0].Tasks) != 1 {
		t.Errorf("Project detail = %+v, want one Phase with one Task", detail)
	}

	var conversations []struct{ ID string }
	run.createdClient.call("GET", "/chat/conversations", nil, 200, &conversations)
	if len(conversations) != 1 {
		t.Fatalf("created Employee has %d conversations, want 1", len(conversations))
	}
	run.conversationID = conversations[0].ID
	events := run.createdClient.dial()
	defer events.Close()
	var message struct{ ID string }
	run.admin.call("POST", "/chat/conversations/"+run.conversationID+"/messages",
		map[string]any{"clientMessageId": uuid.NewString(), "text": "Hello from E2E"}, 201, &message)
	run.messageID = message.ID
	awaitEvent(t, events, "message.new", message.ID)
	var unread struct{ Total int }
	run.createdClient.call("GET", "/chat/unread-count", nil, 200, &unread)
	if unread.Total != 1 {
		t.Errorf("unread total = %d, want 1", unread.Total)
	}
	run.createdClient.call("POST", "/chat/conversations/"+run.conversationID+"/read", map[string]any{"lastReadMessageId": message.ID}, 200, nil)
	run.createdClient.call("GET", "/chat/unread-count", nil, 200, &unread)
	if unread.Total != 0 {
		t.Errorf("unread total after read = %d, want 0", unread.Total)
	}
	return run
}

// assertIsolated checks that other cannot reach any of run's tenant records.
func assertIsolated(t *testing.T, run, other tenantRun) {
	t.Helper()
	other.admin.call("GET", "/employees/"+run.createdID, nil, 404, nil)
	other.admin.call("PATCH", "/employees/"+run.createdID, map[string]any{"position": "x", "version": 1}, 404, nil)
	other.admin.call("DELETE", "/employees/"+run.createdID, nil, 404, nil)
	other.admin.call("GET", "/projects/"+run.projectID, nil, 404, nil)
	other.admin.call("DELETE", "/projects/"+run.projectID, nil, 404, nil)
	other.admin.call("GET", "/chat/conversations/"+run.conversationID, nil, 404, nil)
	other.admin.call("GET", "/chat/conversations/"+run.conversationID+"/messages", nil, 404, nil)
	other.admin.call("POST", "/chat/conversations/"+run.conversationID+"/messages",
		map[string]any{"clientMessageId": uuid.NewString(), "text": "intrusion"}, 404, nil)
	other.createdClient.call("GET", "/chat/conversations/"+run.conversationID, nil, 404, nil)
	var projects []struct{ ID string }
	other.admin.call("GET", "/projects?limit=100", nil, 200, &projects)
	if containsID(projects, run.projectID) {
		t.Error("a Project is listed in another tenant")
	}
	var employees []struct{ ID string }
	other.admin.call("GET", "/employees?limit=100&search=e2e-", nil, 200, &employees)
	if containsID(employees, run.createdID) {
		t.Error("an Employee is listed in another tenant")
	}
}

// startAPI starts the binary from the repository root, waits for /health, and returns a stop func.
func startAPI(t *testing.T, bin, root, base string) func() {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Dir = root
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}
	t.Cleanup(stop)
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if res, err := http.Get(base + "/health"); err == nil {
			res.Body.Close()
			return stop
		}
	}
	stop()
	t.Fatalf("API did not become healthy; is %s already in use?\n%s", base, logs.String())
	return nil
}

type client struct {
	t                *testing.T
	base, token, key string
	email            string
}

func login(t *testing.T, base string, account credentials, want int) client {
	t.Helper()
	c := client{t: t, base: base, email: account.email}
	var session struct{ AccessToken string }
	c.call("POST", "/auth/login", map[string]string{"email": account.email, "password": account.password}, want, &session)
	c.token = session.AccessToken
	return c
}

func (c client) with(token string) client  { c.token = token; return c }
func (c client) withKey(key string) client { c.key = key; return c }

// call sends a JSON request to /api/v1 and decodes the envelope's data into out.
func (c client) call(method, path string, body any, want int, out any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	c.send(method, path, "application/json", reader, want, out)
}

func (c client) upload(path string, out any) {
	c.t.Helper()
	var img, form bytes.Buffer
	_ = png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	w := multipart.NewWriter(&form)
	part, _ := w.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="file"; filename="avatar.png"`},
		"Content-Type":        {"image/png"},
	})
	_, _ = part.Write(img.Bytes())
	_ = w.Close()
	c.send("POST", path, w.FormDataContentType(), &form, 200, out)
}

func (c client) send(method, path, contentType string, body io.Reader, want int, out any) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+"/api/v1"+path, body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", "http://localhost:5173")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.key != "" {
		req.Header.Set("Idempotency-Key", c.key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != want {
		c.t.Fatalf("%s %s as %s: status %d, want %d: %s", method, path, c.email, res.StatusCode, want, raw)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		c.t.Errorf("%s %s: Access-Control-Allow-Origin = %q", method, path, got)
	}
	if out != nil && want < 300 {
		var envelope struct{ Data json.RawMessage }
		if err := json.Unmarshal(raw, &envelope); err != nil || json.Unmarshal(envelope.Data, out) != nil {
			c.t.Fatalf("%s %s: cannot decode %s", method, path, raw)
		}
	}
}

// dial opens the chat WebSocket with a fresh one-use ticket, as a browser on an allowed origin.
func (c client) dial() *websocket.Conn {
	c.t.Helper()
	var ticket struct{ Ticket string }
	c.call("POST", "/chat/ws-ticket", nil, 200, &ticket)
	url := strings.Replace(c.base, "http", "ws", 1) + "/ws/chat?ticket=" + ticket.Ticket
	conn, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Origin": {"http://localhost:5173"}})
	if err != nil {
		c.t.Fatalf("dial chat WebSocket: %v", err)
	}
	return conn
}

func awaitEvent(t *testing.T, conn *websocket.Conn, eventType, id string) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var event struct {
			Type string
			Data struct{ ID string }
		}
		if err := conn.ReadJSON(&event); err != nil {
			t.Fatalf("waiting for %s %s: %v", eventType, id, err)
		}
		if event.Type == eventType && event.Data.ID == id {
			return
		}
	}
}

func download(t *testing.T, url string) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatalf("avatar download: %v", err)
	}
	res.Body.Close()
	return res.StatusCode
}

func containsID(items []struct{ ID string }, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
