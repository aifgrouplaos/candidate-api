package chat

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/apierror"
	"github.com/aifgrouplaos/candidate-api/pkg/pagination"
	"github.com/aifgrouplaos/candidate-api/pkg/realtime"
	"github.com/google/uuid"
)

const (
	defaultMessageLimit = 30
	maxMessageLimit     = 100
	maxTextLength       = 2000
	statusSent          = "sent"
	statusDelivered     = "delivered"
	statusRead          = "read"
	ticketTTL           = 60 * time.Second
	maxSessionsPerUser  = 10
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

type TicketView struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expiresIn"`
}

// ChatSession is one live WebSocket connection of an authenticated session.
type ChatSession struct {
	auth.Principal
	*realtime.Session
}

// event is the envelope of every WebSocket event.
type event struct {
	Type string    `json:"type"`
	Data any       `json:"data"`
	TS   time.Time `json:"ts"`
}

// clientEvent holds the data fields of every client event type; each type reads its own.
type clientEvent struct {
	Type string `json:"type"`
	Data struct {
		ConversationID          string `json:"conversationId"`
		LastContiguousMessageID string `json:"lastContiguousMessageId"`
		LastReadMessageID       string `json:"lastReadMessageId"`
		IsTyping                *bool  `json:"isTyping"`
	} `json:"data"`
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
	// IssueTicket returns a one-use ticket that opens a WebSocket session as caller.
	IssueTicket(ctx context.Context, caller auth.Principal) (*TicketView, error)
	// Connect redeems a ticket and starts a live session that receives both participants'
	// chat events. It returns UNAUTHORIZED for an unknown, used, or expired ticket.
	Connect(ctx context.Context, ticket string) (*ChatSession, error)
	// Disconnect ends s, announcing its user offline when it was their last session.
	Disconnect(ctx context.Context, s *ChatSession)
	// Receive handles one client event, reporting a failure to s as an error event.
	Receive(ctx context.Context, s *ChatSession, raw []byte)
	// Active reports whether the authenticated session behind s is still valid.
	Active(ctx context.Context, s *ChatSession) bool
}

type chatUsecase struct {
	repo      ChatRepository
	avatarURL AvatarURL
	sessions  auth.SessionValidator
	hub       *realtime.Hub
	tickets   *realtime.Tickets[auth.Principal]
}

func NewChatUsecase(repo ChatRepository, avatarURL AvatarURL, sessions auth.SessionValidator) ChatUsecase {
	return &chatUsecase{
		repo: repo, avatarURL: avatarURL, sessions: sessions,
		hub: realtime.NewHub(maxSessionsPerUser), tickets: realtime.NewTickets[auth.Principal](ticketTTL),
	}
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
	message := &Message{ConversationID: id, SenderID: sender.UserID, ClientMessageID: clientMessageID.String(), Text: input.Text}
	stored, err := u.repo.Send(ctx, message)
	if err != nil {
		return nil, err
	}
	if stored.Text != input.Text {
		return nil, apierror.IdempotencyConflict
	}
	view := messageView(stored)
	if stored.ID == message.ID {
		u.broadcast(ctx, id, encode("message.new", view))
	}
	return view, nil
}

func (u *chatUsecase) MarkRead(ctx context.Context, reader auth.Principal, id string, input ReadInput) error {
	return u.acknowledge(ctx, reader, id, input.LastReadMessageID, true)
}

// acknowledge advances reader's delivery or read position and broadcasts each status change.
func (u *chatUsecase) acknowledge(ctx context.Context, reader auth.Principal, id, messageID string, read bool) error {
	if _, err := u.repo.FindByID(ctx, reader, id); err != nil {
		return err
	}
	changed, err := u.repo.Acknowledge(ctx, reader, id, messageID, read)
	if errors.Is(err, errCursorNotFound) {
		field := "lastContiguousMessageId"
		if read {
			field = "lastReadMessageId"
		}
		return cursorInvalid(field, field+" must be a message in this conversation.")
	}
	if err != nil || len(changed) == 0 {
		return err
	}
	events := make([][]byte, len(changed))
	for i, m := range changed {
		view := messageView(m)
		events[i] = encode("message.status", map[string]any{"messageId": m.ID, "conversationId": id, "status": view.Status, "readAt": view.ReadAt})
	}
	u.broadcast(ctx, id, events...)
	return nil
}

// broadcast sends events, then each participant's own view of the conversation and unread
// total, to every live session of both participants. Failures only cost live updates;
// clients resync over REST.
func (u *chatUsecase) broadcast(ctx context.Context, id string, events ...[]byte) {
	participants, err := u.repo.Participants(ctx, id)
	if err != nil {
		slog.Error("chat broadcast failed", "error", err)
		return
	}
	for _, p := range participants {
		if !u.hub.Online(p.UserID) {
			continue
		}
		for _, e := range events {
			u.hub.Send(p.UserID, e)
		}
		conversation, err := u.Get(ctx, p, id)
		total, totalErr := u.repo.UnreadTotal(ctx, p)
		if err := errors.Join(err, totalErr); err != nil {
			slog.Error("chat broadcast failed", "error", err)
			continue
		}
		u.hub.Send(p.UserID, encode("conversation.updated", conversation))
		u.hub.Send(p.UserID, encode("unread.updated", UnreadCountView{Total: total}))
	}
}

func (u *chatUsecase) IssueTicket(_ context.Context, caller auth.Principal) (*TicketView, error) {
	if !caller.Role.Valid() {
		return nil, errs.ErrUnauthorized
	}
	ticket, err := u.tickets.Issue(caller)
	if err != nil {
		return nil, err
	}
	return &TicketView{Ticket: ticket, ExpiresIn: int(ticketTTL.Seconds())}, nil
}

func (u *chatUsecase) Connect(ctx context.Context, ticket string) (*ChatSession, error) {
	principal, ok := u.tickets.Redeem(ticket)
	if !ok || !u.active(ctx, principal) {
		return nil, errs.ErrUnauthorized
	}
	counterparts, err := u.repo.Counterparts(ctx, principal)
	if err != nil {
		return nil, err
	}
	session, first, err := u.hub.Join(principal.UserID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	for _, id := range counterparts {
		if !u.hub.Online(id) {
			continue
		}
		if first {
			u.hub.Send(id, presence(principal.UserID, true, now))
		}
		u.hub.SendTo(session, presence(id, true, now))
	}
	return &ChatSession{Principal: principal, Session: session}, nil
}

func (u *chatUsecase) Disconnect(ctx context.Context, s *ChatSession) {
	if !u.hub.Leave(s.Session) {
		return
	}
	counterparts, err := u.repo.Counterparts(ctx, s.Principal)
	if err != nil {
		slog.Error("chat presence failed", "error", err)
		return
	}
	offline := presence(s.UserID, false, time.Now().UTC())
	for _, id := range counterparts {
		u.hub.Send(id, offline)
	}
}

func (u *chatUsecase) Receive(ctx context.Context, s *ChatSession, raw []byte) {
	var in clientEvent
	err := json.Unmarshal(raw, &in)
	if err != nil {
		err = errs.ErrBadRequest
	} else {
		err = u.handle(ctx, s.Principal, in)
	}
	if err != nil {
		u.hub.SendTo(s.Session, encode("error", eventError(err)))
	}
}

func (u *chatUsecase) handle(ctx context.Context, p auth.Principal, in clientEvent) error {
	switch d := in.Data; in.Type {
	case "message.delivered":
		return u.acknowledge(ctx, p, d.ConversationID, d.LastContiguousMessageID, false)
	case "message.read":
		return u.acknowledge(ctx, p, d.ConversationID, d.LastReadMessageID, true)
	case "typing":
		return u.typing(ctx, p, d.ConversationID, d.IsTyping)
	case "ping":
		return nil
	}
	return errs.BadRequest("Unknown event type.")
}

// typing relays p's typing state to the other participant without storing it.
func (u *chatUsecase) typing(ctx context.Context, p auth.Principal, id string, isTyping *bool) error {
	if isTyping == nil {
		return apierror.Validation([]apierror.FieldError{{Field: "isTyping", Message: "isTyping must be a boolean."}})
	}
	if _, err := u.repo.FindByID(ctx, p, id); err != nil {
		return err
	}
	participants, err := u.repo.Participants(ctx, id)
	if err != nil {
		return err
	}
	relay := encode("typing", map[string]any{"conversationId": id, "userId": p.UserID, "isTyping": *isTyping})
	for _, other := range participants {
		if other.UserID != p.UserID {
			u.hub.Send(other.UserID, relay)
		}
	}
	return nil
}

func (u *chatUsecase) Active(ctx context.Context, s *ChatSession) bool {
	return u.active(ctx, s.Principal)
}

func (u *chatUsecase) active(ctx context.Context, p auth.Principal) bool {
	ok, err := u.sessions.SessionActive(ctx, p.SessionID, p.UserID, p.TenantID)
	if err != nil {
		slog.Error("chat session check failed", "error", err)
	}
	return err == nil && ok
}

func presence(userID string, online bool, at time.Time) []byte {
	return encode("presence", map[string]any{"userId": userID, "online": online, "lastSeenAt": at})
}

func encode(eventType string, data any) []byte {
	raw, _ := json.Marshal(event{Type: eventType, Data: data, TS: time.Now().UTC()})
	return raw
}

// eventError reports err to a client without internal details.
func eventError(err error) map[string]string {
	appErr, ok := errs.IsAppError(err)
	if !ok || appErr.Status >= 500 {
		slog.Error("chat event failed", "error", err)
		return map[string]string{"code": "INTERNAL_ERROR", "message": "An unexpected error occurred."}
	}
	return map[string]string{"code": appErr.Code, "message": appErr.Message}
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
	view := &MessageView{
		ID: m.ID, ClientMessageID: m.ClientMessageID, ConversationID: m.ConversationID, Sequence: m.Sequence,
		Sender: sender, Text: m.Text, Status: statusSent, CreatedAt: m.CreatedAt.UTC(),
	}
	if m.DeliveredAt != nil {
		view.Status = statusDelivered
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
