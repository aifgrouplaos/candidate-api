package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	gormadapter "github.com/BounkhongDev/bkgo/adapter/gorm"
	"github.com/BounkhongDev/bkgo/config"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/internal/employee"
)

// newTestDB needs TEST_POSTGRES_DSN in key=value form, for example
// "host=localhost port=5435 user=postgres password=secret dbname=candidate-api_db sslmode=disable".
func newTestDB(t *testing.T) *gormadapter.DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not set")
	}
	admin, err := gormadapter.New(config.Postgres{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("chat_test_%d", time.Now().UnixNano())
	if err := admin.Raw().Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Raw().Exec("DROP SCHEMA " + schema + " CASCADE")
		admin.Close()
	})
	db, err := gormadapter.New(config.Postgres{DSN: dsn + " search_path=" + schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Raw().AutoMigrate(&auth.User{}, &auth.AuthSession{}, &auth.RefreshToken{}, &employee.Department{}, &employee.Employee{}, &Conversation{}, &Message{}); err != nil {
		t.Fatal(err)
	}
	return db
}

type chatFixture struct {
	t         *testing.T
	db        *gormadapter.DB
	employees employee.EmployeeRepository
}

func (f *chatFixture) admin(tenantID, email string) auth.Principal {
	f.t.Helper()
	user := &auth.User{TenantID: tenantID, Email: email, PasswordHash: "hash", Role: auth.RoleAdmin, FullName: "Admin", Active: true}
	if err := f.db.Raw().Create(user).Error; err != nil {
		f.t.Fatal(err)
	}
	return auth.Principal{UserID: user.ID, TenantID: tenantID, Role: auth.RoleAdmin}
}

// employee provisions an Employee login the way POST /employees does.
func (f *chatFixture) employee(tenantID, name, email string) (*employee.Employee, auth.Principal) {
	f.t.Helper()
	e := &employee.Employee{TenantID: tenantID, FullName: name, Email: email, Status: employee.StatusActive}
	login := &auth.User{TenantID: tenantID, Email: email, PasswordHash: "hash", Role: auth.RoleEmployee, FullName: name, Active: true}
	if err := f.employees.Create(context.Background(), e, login, 200); err != nil {
		f.t.Fatal(err)
	}
	return e, auth.Principal{UserID: login.ID, TenantID: tenantID, Role: auth.RoleEmployee}
}

// send stores messages directly; IDs are chosen so their order differs from Sequence.
func (f *chatFixture) send(conversationID string, sender auth.Principal, sequence int64, text string) *Message {
	f.t.Helper()
	id := fmt.Sprintf("%08d-0000-0000-0000-%s", 100-sequence, conversationID[24:])
	m := &Message{ID: id, ConversationID: conversationID, Sequence: sequence, SenderID: sender.UserID, ClientMessageID: id, Text: text}
	if err := f.db.Raw().Create(m).Error; err != nil {
		f.t.Fatal(err)
	}
	f.db.Raw().Model(&Conversation{}).Where("id = ?", conversationID).Update("updated_at", time.Now())
	return m
}

func list(t *testing.T, repo ChatRepository, reader auth.Principal, filter ConversationFilter) ([]*Conversation, string) {
	t.Helper()
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	conversations, total, err := repo.List(context.Background(), reader, filter)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range conversations {
		names = append(names, c.EmployeeName)
	}
	if int(total) < len(conversations) {
		t.Fatalf("total %d < page %d", total, len(conversations))
	}
	return conversations, strings.Join(names, ",")
}

func TestConversationsAgainstPostgres(t *testing.T) {
	db := newTestDB(t)
	f := &chatFixture{t: t, db: db, employees: employee.NewEmployeeRepository(db, ProvisionConversation)}
	repo := NewChatRepository(db)
	ctx := context.Background()

	adminA := f.admin(tenantA, "admin-a@example.test")
	adminB := f.admin(tenantB, "admin-b@example.test")
	somchai, somchaiLogin := f.employee(tenantA, "Somchai", "somchai@example.test")
	_, annaLogin := f.employee(tenantA, "Anna", "anna@example.test")
	f.employee(tenantB, "Other Tenant", "other@example.test")

	// Admin inbox: newest update first; the conversation exists before any message.
	conversations, names := list(t, repo, adminA, ConversationFilter{})
	if names != "Anna,Somchai" || conversations[0].LastMessage != nil || conversations[0].UnreadCount != 0 {
		t.Fatalf("admin inbox = %s, first %+v", names, conversations[0])
	}
	somchaiConversation := conversations[1]
	if _, names := list(t, repo, adminB, ConversationFilter{}); names != "Other Tenant" {
		t.Fatalf("tenant B inbox = %s", names)
	}
	if _, names := list(t, repo, somchaiLogin, ConversationFilter{}); names != "Somchai" {
		t.Fatalf("employee sees %s", names)
	}

	// Only the Employee and their tenant's Admin may read the conversation.
	for _, reader := range []auth.Principal{adminB, annaLogin, {UserID: somchaiLogin.UserID, TenantID: tenantB, Role: auth.RoleEmployee}} {
		if _, err := repo.FindByID(ctx, reader, somchaiConversation.ID); errorCode(err) != "NOT_FOUND" {
			t.Fatalf("%+v read err = %v", reader, err)
		}
	}
	if _, err := repo.FindByID(ctx, adminA, "not-a-uuid"); errorCode(err) != "NOT_FOUND" {
		t.Fatalf("malformed id err = %v", err)
	}

	f.send(somchaiConversation.ID, somchaiLogin, 1, "first")
	f.send(somchaiConversation.ID, adminA, 2, "reply")
	last := f.send(somchaiConversation.ID, somchaiLogin, 3, "latest")

	got, err := repo.FindByID(ctx, adminA, somchaiConversation.ID)
	if err != nil || got.EmployeeID != somchai.ID || got.UnreadCount != 2 || got.LastMessage == nil || got.LastMessage.ID != last.ID || got.LastMessage.Sender.FullName != "Somchai" {
		t.Fatalf("admin view = %+v, last %+v, err %v", got, got.LastMessage, err)
	}
	if got, _ := repo.FindByID(ctx, somchaiLogin, somchaiConversation.ID); got.UnreadCount != 1 {
		t.Fatalf("employee unread = %d", got.UnreadCount)
	}
	if _, names := list(t, repo, adminA, ConversationFilter{}); names != "Somchai,Anna" {
		t.Fatalf("inbox after message = %s", names)
	}
	if _, names := list(t, repo, adminA, ConversationFilter{UnreadOnly: true}); names != "Somchai" {
		t.Fatalf("unread inbox = %s", names)
	}
	if _, names := list(t, repo, adminA, ConversationFilter{Search: "ANNA@"}); names != "Anna" {
		t.Fatalf("search = %s", names)
	}
	if page, _ := list(t, repo, adminA, ConversationFilter{Offset: 1, Limit: 1}); len(page) != 1 || page[0].EmployeeName != "Anna" {
		t.Fatalf("second page = %v", page)
	}

	// A Deleted Employee's conversation is hidden, then returns unchanged on restore.
	if err := f.employees.Delete(ctx, tenantA, somchai.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, names := list(t, repo, adminA, ConversationFilter{}); names != "Anna" {
		t.Fatalf("inbox after delete = %s", names)
	}
	f.employee(tenantA, "Somchai Restored", "somchai@example.test")
	restored, err := repo.FindByID(ctx, adminA, somchaiConversation.ID)
	if err != nil || restored.EmployeeName != "Somchai Restored" || restored.LastMessage.ID != last.ID {
		t.Fatalf("restored = %+v, err %v", restored, err)
	}
}

func TestMessagesAgainstPostgres(t *testing.T) {
	db := newTestDB(t)
	f := &chatFixture{t: t, db: db, employees: employee.NewEmployeeRepository(db, ProvisionConversation)}
	repo := NewChatRepository(db)
	ctx := context.Background()
	admin := f.admin(tenantA, "admin@example.test")
	_, somchai := f.employee(tenantA, "Somchai", "somchai@example.test")
	_, anna := f.employee(tenantA, "Anna", "anna@example.test")
	conversations, _ := list(t, repo, admin, ConversationFilter{})
	annaConversation, somchaiConversation := conversations[0].ID, conversations[1].ID

	if messages, err := repo.Messages(ctx, MessageFilter{ConversationID: somchaiConversation, Limit: 10}); err != nil || len(messages) != 0 {
		t.Fatalf("empty history = %v, err %v", messages, err)
	}
	var sent []*Message
	for i := int64(1); i <= 5; i++ {
		sent = append(sent, f.send(somchaiConversation, somchai, i, fmt.Sprint("m", i)))
	}
	other := f.send(annaConversation, anna, 1, "other")

	texts := func(filter MessageFilter) string {
		t.Helper()
		filter.ConversationID = somchaiConversation
		messages, err := repo.Messages(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		var result []string
		for _, m := range messages {
			result = append(result, m.Text)
		}
		return strings.Join(result, ",")
	}
	cases := map[string]MessageFilter{
		"m4,m5":    {Limit: 2},
		"m2,m3":    {Limit: 2, Before: sent[3].ID},
		"m3,m4":    {Limit: 2, After: sent[1].ID},
		"m1,m2,m3": {Limit: 10, Before: sent[3].ID},
		"":         {Limit: 10, After: sent[4].ID},
	}
	for want, filter := range cases {
		if got := texts(filter); got != want {
			t.Fatalf("%+v = %q, want %q", filter, got, want)
		}
	}
	for _, cursor := range []MessageFilter{{Before: other.ID}, {After: "not-a-uuid"}} {
		cursor.ConversationID, cursor.Limit = somchaiConversation, 10
		if _, err := repo.Messages(ctx, cursor); !errors.Is(err, errCursorNotFound) {
			t.Fatalf("%+v err = %v", cursor, err)
		}
	}
	messages, _ := repo.Messages(ctx, MessageFilter{ConversationID: somchaiConversation, Limit: 1})
	if messages[0].Sender == nil || messages[0].Sender.Role != auth.RoleEmployee {
		t.Fatalf("sender = %+v", messages[0].Sender)
	}
}
