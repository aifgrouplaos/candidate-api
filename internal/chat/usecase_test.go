package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/pagination"
)

const (
	tenantA = "aaaaaaaa-0000-0000-0000-000000000000"
	tenantB = "bbbbbbbb-0000-0000-0000-000000000000"
)

var (
	adminA    = auth.Principal{UserID: "admin-a", TenantID: tenantA, Role: auth.RoleAdmin}
	adminB    = auth.Principal{UserID: "admin-b", TenantID: tenantB, Role: auth.RoleAdmin}
	employeeA = auth.Principal{UserID: "user-e1", TenantID: tenantA, Role: auth.RoleEmployee}
	otherA    = auth.Principal{UserID: "user-e2", TenantID: tenantA, Role: auth.RoleEmployee}
)

// memoryRepository holds conversation c1 between adminA and employeeA, owned by user-e1.
type memoryRepository struct {
	messages   []*Message
	lastFilter ConversationFilter
}

func (r *memoryRepository) conversation() *Conversation {
	c := &Conversation{ID: "c1", TenantID: tenantA, EmployeeID: "e1", EmployeeName: "Somchai", EmployeeAvatar: ptr("avatars/" + tenantA + "/e1/a.png")}
	if len(r.messages) != 0 {
		c.LastMessage = r.messages[len(r.messages)-1]
	}
	return c
}

func (r *memoryRepository) canRead(reader auth.Principal) bool {
	return reader.TenantID == tenantA && (reader.Role == auth.RoleAdmin || reader.UserID == employeeA.UserID)
}

// List ignores search and unread filtering; the Postgres test covers them.
func (r *memoryRepository) List(_ context.Context, reader auth.Principal, filter ConversationFilter) ([]*Conversation, int64, error) {
	r.lastFilter = filter
	if !r.canRead(reader) {
		return []*Conversation{}, 0, nil
	}
	return []*Conversation{r.conversation()}, 1, nil
}

func (r *memoryRepository) FindByID(_ context.Context, reader auth.Principal, id string) (*Conversation, error) {
	if id != "c1" || !r.canRead(reader) {
		return nil, errs.NotFound("Conversation not found.")
	}
	return r.conversation(), nil
}

func (r *memoryRepository) Messages(_ context.Context, filter MessageFilter) ([]*Message, error) {
	lo, hi, newest := 0, len(r.messages), true
	if cursor := filter.Before + filter.After; cursor != "" {
		i := r.index(cursor)
		if i < 0 {
			return nil, errCursorNotFound
		}
		if filter.After != "" {
			lo, newest = i+1, false
		} else {
			hi = i
		}
	}
	if newest {
		lo = max(lo, hi-filter.Limit)
	} else {
		hi = min(hi, lo+filter.Limit)
	}
	return r.messages[lo:hi], nil
}

func (r *memoryRepository) index(id string) int {
	for i, m := range r.messages {
		if m.ID == id {
			return i
		}
	}
	return -1
}

func newRepositoryWithMessages(n int) *memoryRepository {
	repo := &memoryRepository{}
	for i := 1; i <= n; i++ {
		sender := &auth.User{ID: employeeA.UserID, FullName: "Somchai", Role: auth.RoleEmployee}
		repo.messages = append(repo.messages, &Message{ID: fmt.Sprintf("m%d", i), ConversationID: "c1", Sequence: int64(i), SenderID: sender.ID, Sender: sender, Text: "hi"})
	}
	return repo
}

func signAvatar(_ context.Context, tenantID string, key *string) (*string, error) {
	if key == nil {
		return nil, nil
	}
	return ptr("https://files.example.test/" + tenantID + "/" + *key), nil
}

func ptr[T any](v T) *T { return &v }

func errorCode(err error) string {
	if appErr, ok := errs.IsAppError(err); ok {
		return appErr.Code
	}
	return fmt.Sprint(err)
}

func ids(messages []*MessageView) string {
	var result []string
	for _, m := range messages {
		result = append(result, m.ID)
	}
	return strings.Join(result, ",")
}

func TestListPresentsConversationsWithSignedAvatar(t *testing.T) {
	repo := &memoryRepository{}
	views, meta, err := NewChatUsecase(repo, signAvatar).List(context.Background(), adminA, ListQuery{Query: pagination.Query{Page: 2, Limit: 5, Search: "  som  "}, UnreadOnly: true})
	if err != nil || len(views) != 1 {
		t.Fatalf("views = %v, err %v", views, err)
	}
	if repo.lastFilter != (ConversationFilter{Search: "som", UnreadOnly: true, Offset: 5, Limit: 5}) {
		t.Fatalf("filter = %+v", repo.lastFilter)
	}
	if meta != (pagination.Meta{Page: 2, Limit: 5, Total: 1, TotalPages: 1}) {
		t.Fatalf("meta = %+v", meta)
	}
	got, _ := json.Marshal(views[0])
	want := `{"id":"c1","employee":{"id":"e1","fullName":"Somchai","avatarUrl":"https://files.example.test/` + tenantA + `/avatars/` + tenantA + `/e1/a.png"},"lastMessage":null,"unreadCount":0,"updatedAt":"0001-01-01T00:00:00Z"}`
	if string(got) != want {
		t.Fatalf("view = %s", got)
	}
}

func TestConversationLastMessagePreview(t *testing.T) {
	view, err := NewChatUsecase(newRepositoryWithMessages(2), signAvatar).Get(context.Background(), adminA, "c1")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(view.LastMessage)
	if string(got) != `{"id":"m2","text":"hi","senderRole":"employee","createdAt":"0001-01-01T00:00:00Z"}` {
		t.Fatalf("lastMessage = %s", got)
	}
}

func TestListRejectsOverlongSearch(t *testing.T) {
	_, _, err := NewChatUsecase(&memoryRepository{}, signAvatar).List(context.Background(), adminA, ListQuery{Query: pagination.Query{Search: strings.Repeat("a", 101)}})
	if errorCode(err) != "VALIDATION_ERROR" {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenReturnsTheEmployeesConversation(t *testing.T) {
	uc := NewChatUsecase(&memoryRepository{}, signAvatar)
	view, err := uc.Open(context.Background(), employeeA)
	if err != nil || view.ID != "c1" {
		t.Fatalf("view = %+v, err %v", view, err)
	}
	if _, err := uc.Open(context.Background(), adminA); errorCode(err) != "FORBIDDEN" {
		t.Fatalf("admin open err = %v", err)
	}
}

func TestGetAndMessagesRequireParticipant(t *testing.T) {
	uc := NewChatUsecase(newRepositoryWithMessages(1), signAvatar)
	for _, reader := range []auth.Principal{adminB, otherA} {
		if _, err := uc.Get(context.Background(), reader, "c1"); errorCode(err) != "NOT_FOUND" {
			t.Fatalf("%s get err = %v", reader.UserID, err)
		}
		if _, _, err := uc.Messages(context.Background(), reader, "c1", MessageQuery{}); errorCode(err) != "NOT_FOUND" {
			t.Fatalf("%s messages err = %v", reader.UserID, err)
		}
	}
	for _, reader := range []auth.Principal{adminA, employeeA} {
		if _, err := uc.Get(context.Background(), reader, "c1"); err != nil {
			t.Fatalf("%s get err = %v", reader.UserID, err)
		}
	}
}

func TestMessagesCursorPages(t *testing.T) {
	uc := NewChatUsecase(newRepositoryWithMessages(5), signAvatar)
	cases := []struct {
		name  string
		query MessageQuery
		ids   string
		meta  string
	}{
		{"newest page", MessageQuery{Limit: 2}, "m4,m5", `{"hasMoreBefore":true,"hasMoreAfter":false,"nextBefore":"m4"}`},
		{"all fit", MessageQuery{}, "m1,m2,m3,m4,m5", `{"hasMoreBefore":false,"hasMoreAfter":false,"nextBefore":null}`},
		{"older page", MessageQuery{Limit: 2, Before: "m4"}, "m2,m3", `{"hasMoreBefore":true,"hasMoreAfter":true,"nextBefore":"m2"}`},
		{"oldest page", MessageQuery{Limit: 2, Before: "m3"}, "m1,m2", `{"hasMoreBefore":false,"hasMoreAfter":true,"nextBefore":null}`},
		{"newer page", MessageQuery{Limit: 2, After: "m1"}, "m2,m3", `{"hasMoreBefore":true,"hasMoreAfter":true,"nextBefore":"m2"}`},
		{"caught up", MessageQuery{Limit: 2, After: "m3"}, "m4,m5", `{"hasMoreBefore":true,"hasMoreAfter":false,"nextBefore":"m4"}`},
		{"nothing newer", MessageQuery{After: "m5"}, "", `{"hasMoreBefore":true,"hasMoreAfter":false,"nextBefore":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messages, meta, err := uc.Messages(context.Background(), employeeA, "c1", tc.query)
			if err != nil {
				t.Fatal(err)
			}
			gotMeta, _ := json.Marshal(meta)
			if ids(messages) != tc.ids || string(gotMeta) != tc.meta {
				t.Fatalf("ids = %s, meta = %s", ids(messages), gotMeta)
			}
		})
	}
}

func TestMessagesLimitDefaultsAndCap(t *testing.T) {
	uc := NewChatUsecase(newRepositoryWithMessages(120), signAvatar)
	for limit, want := range map[int]int{0: 30, -1: 30, 100: 100, 500: 100} {
		messages, _, err := uc.Messages(context.Background(), adminA, "c1", MessageQuery{Limit: limit})
		if err != nil || len(messages) != want {
			t.Fatalf("limit %d: got %d, err %v", limit, len(messages), err)
		}
	}
}

func TestMessagesEmptyHistory(t *testing.T) {
	messages, meta, err := NewChatUsecase(&memoryRepository{}, signAvatar).Messages(context.Background(), adminA, "c1", MessageQuery{})
	got, _ := json.Marshal(messages)
	gotMeta, _ := json.Marshal(meta)
	if err != nil || string(got) != "[]" || string(gotMeta) != `{"hasMoreBefore":false,"hasMoreAfter":false,"nextBefore":null}` {
		t.Fatalf("messages = %s, meta = %s, err %v", got, gotMeta, err)
	}
}

func TestMessagesRejectsInvalidCursors(t *testing.T) {
	uc := NewChatUsecase(newRepositoryWithMessages(2), signAvatar)
	for _, query := range []MessageQuery{{Before: "m2", After: "m1"}, {Before: "missing"}, {After: "missing"}} {
		if _, _, err := uc.Messages(context.Background(), adminA, "c1", query); errorCode(err) != "VALIDATION_ERROR" {
			t.Fatalf("%+v: err = %v", query, err)
		}
	}
}

func TestMessageView(t *testing.T) {
	messages, _, err := NewChatUsecase(newRepositoryWithMessages(1), signAvatar).Messages(context.Background(), adminA, "c1", MessageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(messages[0])
	want := `{"id":"m1","clientMessageId":"","conversationId":"c1","sequence":1,"sender":{"id":"user-e1","fullName":"Somchai","role":"employee"},"text":"hi","status":"sent","createdAt":"0001-01-01T00:00:00Z","readAt":null}`
	if string(got) != want {
		t.Fatalf("view = %s", got)
	}
}
