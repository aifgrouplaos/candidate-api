package chat

import (
	"context"
	"encoding/json"
	"io"
	"net"
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
	"github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v2"
)

type activeSessions struct{}

func (activeSessions) SessionActive(context.Context, string, string, string) (bool, error) {
	return true, nil
}

type chatClient func(method, path string, actor *auth.Principal, body string) (int, map[string]json.RawMessage)

func newChatClient(t *testing.T) chatClient {
	do, _ := newChatServer(t)
	return do
}

// newChatServer serves the chat routes over a repository holding three Employee messages
// and returns a REST client plus the WebSocket URL.
func newChatServer(t *testing.T) (chatClient, string) {
	token := jwt.New(config.JWT{Secret: "test-secret"})
	app := fiber.New(fiber.Config{ErrorHandler: httpresponse.Error, DisableStartupMessage: true})
	handler := NewChatHandler(NewChatUsecase(newRepositoryWithMessages(3), signAvatar, activeSessions{}))
	handler.RegisterRoutes(app, auth.Authentication(token, activeSessions{}))
	handler.RegisterWebSocket(app, []string{allowedOrigin})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go app.Listener(listener)
	t.Cleanup(func() { _ = app.Shutdown() })
	return func(method, path string, actor *auth.Principal, body string) (int, map[string]json.RawMessage) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
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
	}, "ws://" + listener.Addr().String() + "/ws/chat"
}

func TestChatRoutesContract(t *testing.T) {
	do := newChatClient(t)
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
		status, body := do(tc.method, tc.path, tc.actor, "")
		if status != tc.status || len(body) != len(tc.keys) {
			t.Fatalf("%s %s: %d %v", tc.method, tc.path, status, body)
		}
		for _, key := range tc.keys {
			if _, ok := body[key]; !ok {
				t.Fatalf("%s %s: missing %q in %v", tc.method, tc.path, key, body)
			}
		}
	}

	_, page := do(http.MethodGet, "/chat/conversations/c1/messages?limit=2&before=m3", &adminA, "")
	if string(page["meta"]) != `{"hasMoreBefore":false,"hasMoreAfter":true,"nextBefore":null}` {
		t.Fatalf("messages meta = %s", page["meta"])
	}
}

// TestChatWriteRoutesContract runs in order: each case sees the state earlier cases left.
func TestChatWriteRoutesContract(t *testing.T) {
	do := newChatClient(t)
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

const allowedOrigin = "http://localhost:3000"

// liveChat is a REST client plus WebSocket connections opened with real tickets.
type liveChat struct {
	t   *testing.T
	do  chatClient
	url string
}

func newLiveChat(t *testing.T) *liveChat {
	do, url := newChatServer(t)
	return &liveChat{t: t, do: do, url: url}
}

func (l *liveChat) ticket(actor *auth.Principal) string {
	l.t.Helper()
	status, body := l.do(http.MethodPost, "/chat/ws-ticket", actor, "")
	var ticket TicketView
	if err := json.Unmarshal(body["data"], &ticket); status != fiber.StatusOK || err != nil || ticket.ExpiresIn != 60 {
		l.t.Fatalf("ticket: %d %v", status, body)
	}
	return ticket.Ticket
}

func (l *liveChat) dial(ticket, origin string) *websocket.Conn {
	l.t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(l.url+"?ticket="+ticket, http.Header{"Origin": {origin}})
	if err != nil {
		l.t.Fatal(err)
	}
	l.t.Cleanup(func() { conn.Close() })
	return conn
}

func (l *liveChat) connect(actor *auth.Principal) *websocket.Conn {
	return l.dial(l.ticket(actor), allowedOrigin)
}

// next skips other events until one of eventType arrives and returns its data.
func next(t *testing.T, conn *websocket.Conn, eventType string) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		var e struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
			TS   time.Time       `json:"ts"`
		}
		if err := conn.ReadJSON(&e); err != nil {
			t.Fatalf("waiting for %s: %v", eventType, err)
		}
		if e.TS.IsZero() {
			t.Fatalf("%s event without ts", e.Type)
		}
		if e.Type == eventType {
			return string(e.Data)
		}
	}
}

func send(t *testing.T, conn *websocket.Conn, event string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(event)); err != nil {
		t.Fatal(err)
	}
}

func wantClosed(t *testing.T, conn *websocket.Conn, code int) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := conn.ReadMessage()
	if !websocket.IsCloseError(err, code) {
		t.Fatalf("close err = %v, want code %d", err, code)
	}
}

func TestWebSocketAuthorizesConnections(t *testing.T) {
	l := newLiveChat(t)
	if status, _ := l.do(http.MethodPost, "/chat/ws-ticket", nil, ""); status != fiber.StatusUnauthorized {
		t.Fatalf("anonymous ticket status = %d", status)
	}
	if status, _ := l.do(http.MethodGet, "/ws/chat", nil, ""); status != fiber.StatusUpgradeRequired {
		t.Fatalf("plain GET status = %d", status)
	}
	wantClosed(t, l.dial("unknown", allowedOrigin), closeUnauthorized)
	wantClosed(t, l.dial(l.ticket(&adminA), "https://evil.example"), closeUnauthorized)

	ticket := l.ticket(&employeeA)
	l.dial(ticket, allowedOrigin)
	wantClosed(t, l.dial(ticket, allowedOrigin), closeUnauthorized)

	// A live session of another tenant cannot act on the conversation.
	outsider := l.connect(&adminB)
	send(t, outsider, `{"type":"message.read","data":{"conversationId":"c1","lastReadMessageId":"m1"}}`)
	if got := next(t, outsider, "error"); !strings.Contains(got, `"code":"NOT_FOUND"`) {
		t.Fatalf("cross-tenant read = %s", got)
	}
	send(t, outsider, `{"type":"typing","data":{"conversationId":"c1","isTyping":true}}`)
	if got := next(t, outsider, "error"); !strings.Contains(got, `"code":"NOT_FOUND"`) {
		t.Fatalf("cross-tenant typing = %s", got)
	}
	for _, bad := range []string{`{`, `{"type":"shout","data":{}}`} {
		send(t, outsider, bad)
		if got := next(t, outsider, "error"); !strings.Contains(got, `"code":"BAD_REQUEST"`) {
			t.Fatalf("%s = %s", bad, got)
		}
	}
}

func TestWebSocketDeliversEventsToEverySession(t *testing.T) {
	l := newLiveChat(t)
	admin := l.connect(&adminA)
	phone, laptop := l.connect(&employeeA), l.connect(&employeeA)
	if got := next(t, admin, "presence"); !strings.Contains(got, `"online":true,"userId":"user-e1"`) {
		t.Fatalf("presence = %s", got)
	}
	if got := next(t, phone, "presence"); !strings.Contains(got, `"online":true,"userId":"admin-a"`) {
		t.Fatalf("employee sees admin presence = %s", got)
	}

	l.do(http.MethodPost, "/chat/conversations/c1/messages", &adminA, `{"clientMessageId":"8f14e45f-ceea-467f-a8f1-6d1b8c2a0001","text":"Hello"}`)
	for _, conn := range []*websocket.Conn{admin, phone, laptop} {
		if got := next(t, conn, "message.new"); !strings.Contains(got, `"id":"m4"`) || !strings.Contains(got, `"status":"sent"`) {
			t.Fatalf("message.new = %s", got)
		}
	}
	if got := next(t, admin, "unread.updated"); got != `{"total":3}` {
		t.Fatalf("admin unread = %s", got)
	}
	if got := next(t, phone, "conversation.updated"); !strings.Contains(got, `"lastMessage":{"id":"m4"`) {
		t.Fatalf("employee conversation = %s", got)
	}

	send(t, phone, `{"type":"message.delivered","data":{"conversationId":"c1","lastContiguousMessageId":"m4"}}`)
	want := `{"conversationId":"c1","messageId":"m4","readAt":null,"status":"delivered"}`
	for _, conn := range []*websocket.Conn{admin, laptop} {
		if got := next(t, conn, "message.status"); got != want {
			t.Fatalf("delivered status = %s", got)
		}
	}

	send(t, laptop, `{"type":"message.read","data":{"conversationId":"c1","lastReadMessageId":"m4"}}`)
	if got := next(t, admin, "message.status"); !strings.Contains(got, `"messageId":"m4","readAt":"2026-10-05T08:30:00Z","status":"read"`) {
		t.Fatalf("read status = %s", got)
	}
	next(t, phone, "message.status")
	if got := next(t, phone, "message.status"); !strings.Contains(got, `"status":"read"`) {
		t.Fatalf("phone read status = %s", got)
	}
	if got := next(t, phone, "unread.updated"); got != `{"total":0}` {
		t.Fatalf("employee unread after read = %s", got)
	}

	send(t, admin, `{"type":"typing","data":{"conversationId":"c1","isTyping":true}}`)
	if got := next(t, laptop, "typing"); got != `{"conversationId":"c1","isTyping":true,"userId":"admin-a"}` {
		t.Fatalf("typing = %s", got)
	}

	phone.Close()
	laptop.Close()
	if got := next(t, admin, "presence"); !strings.Contains(got, `"online":false,"userId":"user-e1"`) {
		t.Fatalf("offline presence = %s", got)
	}
}

func TestWebSocketReconnectResyncsOverREST(t *testing.T) {
	l := newLiveChat(t)
	l.connect(&employeeA).Close()

	l.do(http.MethodPost, "/chat/conversations/c1/messages", &adminA, `{"clientMessageId":"8f14e45f-ceea-467f-a8f1-6d1b8c2a0001","text":"While away"}`)
	reconnected := l.connect(&employeeA)
	if _, page := l.do(http.MethodGet, "/chat/conversations/c1/messages?after=m3", &employeeA, ""); !strings.Contains(string(page["data"]), `"text":"While away"`) {
		t.Fatalf("resync = %s", page["data"])
	}
	l.do(http.MethodPost, "/chat/conversations/c1/messages", &adminA, `{"clientMessageId":"8f14e45f-ceea-467f-a8f1-6d1b8c2a0002","text":"Welcome back"}`)
	if got := next(t, reconnected, "message.new"); !strings.Contains(got, `"text":"Welcome back"`) {
		t.Fatalf("live after reconnect = %s", got)
	}
}
