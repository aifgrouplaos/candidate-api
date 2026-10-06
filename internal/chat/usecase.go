package chat

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/apierror"
	"github.com/aifgrouplaos/candidate-api/pkg/pagination"
	"github.com/google/uuid"
)

const (
	defaultMessageLimit = 30
	maxMessageLimit     = 100
	maxTextLength       = 2000
	statusSent          = "sent"
	statusRead          = "read"
)

var errForbidden = *errs.Forbidden("You do not have permission to perform this action.")

type ListQuery struct {
	pagination.Query
	UnreadOnly bool `query:"unreadOnly"`
}

// MessageQuery reads messages before or after an exclusive cursor message ID.
type MessageQuery struct {
	Limit  int    `query:"limit"`
	Before string `query:"before"`
	After  string `query:"after"`
}

type SendInput struct {
	ClientMessageID string `json:"clientMessageId"`
	Text            string `json:"text"`
}

type ReadInput struct {
	LastReadMessageID string `json:"lastReadMessageId"`
}

type UnreadCountView struct {
	Total int64 `json:"total"`
}

type MessageMeta struct {
	HasMoreBefore bool    `json:"hasMoreBefore"`
	HasMoreAfter  bool    `json:"hasMoreAfter"`
	NextBefore    *string `json:"nextBefore"`
}

type ConversationView struct {
	ID          string          `json:"id"`
	Employee    EmployeeSummary `json:"employee"`
	LastMessage *MessagePreview `json:"lastMessage"`
	UnreadCount int             `json:"unreadCount"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type EmployeeSummary struct {
	ID        string  `json:"id"`
	FullName  string  `json:"fullName"`
	AvatarURL *string `json:"avatarUrl"`
}

type MessagePreview struct {
	ID         string    `json:"id"`
	Text       string    `json:"text"`
	SenderRole auth.Role `json:"senderRole"`
	CreatedAt  time.Time `json:"createdAt"`
}

type MessageView struct {
	ID              string     `json:"id"`
	ClientMessageID string     `json:"clientMessageId"`
	ConversationID  string     `json:"conversationId"`
	Sequence        int64      `json:"sequence"`
	Sender          SenderView `json:"sender"`
	Text            string     `json:"text"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"createdAt"`
	ReadAt          *time.Time `json:"readAt"`
}

type SenderView struct {
	ID       string    `json:"id"`
	FullName string    `json:"fullName"`
	Role     auth.Role `json:"role"`
}

type ChatUsecase interface {
	List(ctx context.Context, reader auth.Principal, query ListQuery) ([]*ConversationView, pagination.Meta, error)
	// Open returns the calling Employee's conversation, which is created with the Employee.
	Open(ctx context.Context, reader auth.Principal) (*ConversationView, error)
	Get(ctx context.Context, reader auth.Principal, id string) (*ConversationView, error)
	// Messages returns one ascending page of a conversation's history.
	Messages(ctx context.Context, reader auth.Principal, id string, query MessageQuery) ([]*MessageView, MessageMeta, error)
	// Send saves a message from either participant. Repeating a clientMessageId with the
	// same text returns the original message; different text is IDEMPOTENCY_CONFLICT.
	Send(ctx context.Context, sender auth.Principal, id string, input SendInput) (*MessageView, error)
	// MarkRead advances the reader's read position through the given message.
	MarkRead(ctx context.Context, reader auth.Principal, id string, input ReadInput) error
	UnreadCount(ctx context.Context, reader auth.Principal) (*UnreadCountView, error)
}

type chatUsecase struct {
	repo      ChatRepository
	avatarURL AvatarURL
}

func NewChatUsecase(repo ChatRepository, avatarURL AvatarURL) ChatUsecase {
	return &chatUsecase{repo: repo, avatarURL: avatarURL}
}

func (u *chatUsecase) List(ctx context.Context, reader auth.Principal, query ListQuery) ([]*ConversationView, pagination.Meta, error) {
	var v apierror.FieldErrors
	search := query.Check(&v)
	if err := v.Err(); err != nil {
		return nil, pagination.Meta{}, err
	}
	page, limit, offset := query.Bounds()
	conversations, total, err := u.repo.List(ctx, reader, ConversationFilter{Search: search, UnreadOnly: query.UnreadOnly, Offset: offset, Limit: limit})
	if err != nil {
		return nil, pagination.Meta{}, err
	}
	views := make([]*ConversationView, len(conversations))
	for i, conversation := range conversations {
		if views[i], err = u.present(ctx, conversation); err != nil {
			return nil, pagination.Meta{}, err
		}
	}
	return views, pagination.NewMeta(page, limit, total), nil
}

func (u *chatUsecase) Open(ctx context.Context, reader auth.Principal) (*ConversationView, error) {
	if reader.Role != auth.RoleEmployee {
		return nil, errForbidden
	}
	conversations, _, err := u.repo.List(ctx, reader, ConversationFilter{Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(conversations) == 0 {
		return nil, errNotFound
	}
	return u.present(ctx, conversations[0])
}

func (u *chatUsecase) Get(ctx context.Context, reader auth.Principal, id string) (*ConversationView, error) {
	conversation, err := u.repo.FindByID(ctx, reader, id)
	if err != nil {
		return nil, err
	}
	return u.present(ctx, conversation)
}

func (u *chatUsecase) Messages(ctx context.Context, reader auth.Principal, id string, query MessageQuery) ([]*MessageView, MessageMeta, error) {
	if _, err := u.repo.FindByID(ctx, reader, id); err != nil {
		return nil, MessageMeta{}, err
	}
	cursorField := "before"
	if query.After != "" {
		cursorField = "after"
	}
	if query.Before != "" && query.After != "" {
		return nil, MessageMeta{}, cursorInvalid(cursorField, "Use either before or after, not both.")
	}
	limit := query.Limit
	if limit < 1 {
		limit = defaultMessageLimit
	}
	limit = min(limit, maxMessageLimit)

	messages, err := u.repo.Messages(ctx, MessageFilter{ConversationID: id, Before: query.Before, After: query.After, Limit: limit + 1})
	if errors.Is(err, errCursorNotFound) {
		return nil, MessageMeta{}, cursorInvalid(cursorField, "Cursor must be a message in this conversation.")
	}
	if err != nil {
		return nil, MessageMeta{}, err
	}
	messages, meta := messagePage(messages, limit, query)
	views := make([]*MessageView, len(messages))
	for i, message := range messages {
		views[i] = messageView(message)
	}
	return views, meta, nil
}

func (u *chatUsecase) Send(ctx context.Context, sender auth.Principal, id string, input SendInput) (*MessageView, error) {
	if _, err := u.repo.FindByID(ctx, sender, id); err != nil {
		return nil, err
	}
	var v apierror.FieldErrors
	clientMessageID, err := uuid.Parse(input.ClientMessageID)
	if err != nil {
		v.Add("clientMessageId", "clientMessageId must be a UUID.")
	}
	if strings.TrimSpace(input.Text) == "" || utf8.RuneCountInString(input.Text) > maxTextLength || strings.ContainsRune(input.Text, 0) {
		v.Add("text", "Text must be 1 to 2,000 characters of plain text.")
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	stored, err := u.repo.Send(ctx, &Message{ConversationID: id, SenderID: sender.UserID, ClientMessageID: clientMessageID.String(), Text: input.Text})
	if err != nil {
		return nil, err
	}
	if stored.Text != input.Text {
		return nil, apierror.IdempotencyConflict
	}
	return messageView(stored), nil
}

func (u *chatUsecase) MarkRead(ctx context.Context, reader auth.Principal, id string, input ReadInput) error {
	if _, err := u.repo.FindByID(ctx, reader, id); err != nil {
		return err
	}
	err := u.repo.MarkRead(ctx, reader, id, input.LastReadMessageID)
	if errors.Is(err, errCursorNotFound) {
		return cursorInvalid("lastReadMessageId", "lastReadMessageId must be a message in this conversation.")
	}
	return err
}

func (u *chatUsecase) UnreadCount(ctx context.Context, reader auth.Principal) (*UnreadCountView, error) {
	total, err := u.repo.UnreadTotal(ctx, reader)
	if err != nil {
		return nil, err
	}
	return &UnreadCountView{Total: total}, nil
}

// messagePage trims the extra row fetched past limit and describes what lies beyond the page.
func messagePage(messages []*Message, limit int, query MessageQuery) ([]*Message, MessageMeta) {
	more := len(messages) > limit
	if more && query.After != "" {
		messages = messages[:limit]
	} else if more {
		messages = messages[1:]
	}
	// A cursor message lies on the side opposite the page, so that side always has more.
	meta := MessageMeta{
		HasMoreBefore: query.After != "" || more,
		HasMoreAfter:  query.Before != "" || (query.After != "" && more),
	}
	if meta.HasMoreBefore && len(messages) > 0 {
		meta.NextBefore = &messages[0].ID
	} else if meta.HasMoreBefore {
		meta.NextBefore = &query.After
	}
	return messages, meta
}

func (u *chatUsecase) present(ctx context.Context, c *Conversation) (*ConversationView, error) {
	avatarURL, err := u.avatarURL(ctx, c.TenantID, c.EmployeeAvatar)
	if err != nil {
		return nil, err
	}
	result := &ConversationView{
		ID:          c.ID,
		Employee:    EmployeeSummary{ID: c.EmployeeID, FullName: c.EmployeeName, AvatarURL: avatarURL},
		UnreadCount: c.UnreadCount,
		UpdatedAt:   c.UpdatedAt.UTC(),
	}
	if m := c.LastMessage; m != nil {
		view := messageView(m)
		result.LastMessage = &MessagePreview{ID: m.ID, Text: m.Text, SenderRole: view.Sender.Role, CreatedAt: view.CreatedAt}
	}
	return result, nil
}

func messageView(m *Message) *MessageView {
	sender := SenderView{ID: m.SenderID}
	if m.Sender != nil {
		sender.FullName, sender.Role = m.Sender.FullName, m.Sender.Role
	}
	// ponytail: "delivered" needs WebSocket receipts, so unread messages report "sent".
	view := &MessageView{
		ID: m.ID, ClientMessageID: m.ClientMessageID, ConversationID: m.ConversationID, Sequence: m.Sequence,
		Sender: sender, Text: m.Text, Status: statusSent, CreatedAt: m.CreatedAt.UTC(),
	}
	if m.ReadAt != nil {
		readAt := m.ReadAt.UTC()
		view.Status, view.ReadAt = statusRead, &readAt
	}
	return view
}

func cursorInvalid(field, message string) error {
	return apierror.Validation([]apierror.FieldError{{Field: field, Message: message}})
}
