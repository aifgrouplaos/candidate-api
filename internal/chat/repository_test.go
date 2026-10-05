package chat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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

func wantInbox(t *testing.T, repo ChatRepository, reader auth.Principal, filter ConversationFilter, want string) {
	t.Helper()
	if _, names := list(t, repo, reader, filter); names != want {
		t.Fatalf("%+v inbox %+v = %s, want %s", reader, filter, names, want)
	}
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
	wantInbox(t, repo, adminB, ConversationFilter{}, "Other Tenant")
	wantInbox(t, repo, somchaiLogin, ConversationFilter{}, "Somchai")

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
	wantInbox(t, repo, adminA, ConversationFilter{}, "Somchai,Anna")
	wantInbox(t, repo, adminA, ConversationFilter{UnreadOnly: true}, "Somchai")
	wantInbox(t, repo, adminA, ConversationFilter{Search: "ANNA@"}, "Anna")
	if page, _ := list(t, repo, adminA, ConversationFilter{Offset: 1, Limit: 1}); len(page) != 1 || page[0].EmployeeName != "Anna" {
		t.Fatalf("second page = %v", page)
	}

	// A Deleted Employee's conversation is hidden, then returns unchanged on restore.
	if err := f.employees.Delete(ctx, tenantA, somchai.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	wantInbox(t, repo, adminA, ConversationFilter{}, "Anna")
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

func TestSendAndReadAgainstPostgres(t *testing.T) {
	db := newTestDB(t)
	f := &chatFixture{t: t, db: db, employees: employee.NewEmployeeRepository(db, ProvisionConversation)}
	repo := NewChatRepository(db)
	ctx := context.Background()
	admin := f.admin(tenantA, "admin@example.test")
	_, somchai := f.employee(tenantA, "Somchai", "somchai@example.test")
	f.employee(tenantA, "Anna", "anna@example.test")
	conversations, _ := list(t, repo, admin, ConversationFilter{})
	conversation := conversations[1].ID

	send := func(sender auth.Principal, clientID, text string) *Message {
		t.Helper()
		m, err := repo.Send(ctx, &Message{ConversationID: conversation, SenderID: sender.UserID, ClientMessageID: clientID, Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	clientID := func(i int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", i) }

	// Concurrent sends, including retries of one clientMessageId, get gapless sequences.
	var wg sync.WaitGroup
	for i := 1; i <= 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, text := clientID(99), "retry"
			if i%2 == 0 {
				id, text = clientID(i/2), "hi"
			}
			if _, err := repo.Send(ctx, &Message{ConversationID: conversation, SenderID: somchai.UserID, ClientMessageID: id, Text: text}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var stored []*Message
	db.Raw().Where("conversation_id = ?", conversation).Order("sequence").Find(&stored)
	if len(stored) != 11 || stored[0].Sequence != 1 || stored[10].Sequence != 11 {
		t.Fatalf("stored = %d messages", len(stored))
	}
	first, retry := send(somchai, clientID(1), "ignored"), send(somchai, clientID(1), "ignored")
	if first.ID != retry.ID || first.Text != "hi" || first.Sender == nil || first.Sender.FullName != "Somchai" {
		t.Fatalf("replay = %+v, %+v", first, retry)
	}
	reply := send(admin, clientID(1), "reply")
	if reply.ID == first.ID || reply.Sequence != 12 {
		t.Fatalf("admin reply = %+v", reply)
	}
	wantInbox(t, repo, admin, ConversationFilter{}, "Somchai,Anna")

	unread := func(reader auth.Principal) (int, int64) {
		t.Helper()
		c, err := repo.FindByID(ctx, reader, conversation)
		total, totalErr := repo.UnreadTotal(ctx, reader)
		if err != nil || totalErr != nil {
			t.Fatal(err, totalErr)
		}
		return c.UnreadCount, total
	}
	if count, total := unread(admin); count != 11 || total != 11 {
		t.Fatalf("admin unread = %d, total %d", count, total)
	}

	// Reading through a message marks only the other participant's earlier messages.
	fifthID := stored[4].ID
	if err := repo.MarkRead(ctx, admin, conversation, fifthID); err != nil {
		t.Fatal(err)
	}
	if count, total := unread(admin); count != 6 || total != 6 {
		t.Fatalf("after read admin unread = %d, total %d", count, total)
	}
	var readAt time.Time
	db.Raw().Model(&Message{}).Where("id = ?", fifthID).Pluck("read_at", &readAt)

	// An older position never moves the read position back or restamps readAt.
	if err := repo.MarkRead(ctx, admin, conversation, stored[2].ID); err != nil {
		t.Fatal(err)
	}
	var again time.Time
	db.Raw().Model(&Message{}).Where("id = ?", fifthID).Pluck("read_at", &again)
	if count, _ := unread(admin); count != 6 || !again.Equal(readAt) {
		t.Fatalf("older read: unread %d, readAt %v -> %v", count, readAt, again)
	}

	if err := repo.MarkRead(ctx, admin, conversation, reply.ID); err != nil {
		t.Fatal(err)
	}
	if count, total := unread(admin); count != 0 || total != 0 {
		t.Fatalf("all read admin unread = %d, total %d", count, total)
	}
	wantInbox(t, repo, admin, ConversationFilter{UnreadOnly: true}, "")
	if count, total := unread(somchai); count != 1 || total != 1 {
		t.Fatalf("employee unread = %d, total %d", count, total)
	}
	if err := repo.MarkRead(ctx, admin, conversation, "not-a-uuid"); !errors.Is(err, errCursorNotFound) {
		t.Fatalf("bad id err = %v", err)
	}
}
