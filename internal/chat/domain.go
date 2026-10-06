package chat

import (
	"context"
	"errors"
	"time"

	"github.com/aifgrouplaos/candidate-api/internal/auth"
)

// Conversation is the one-to-one chat between an Employee and the Admin of its tenant.
type Conversation struct {
	ID         string    `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID   string    `gorm:"type:uuid;not null;index"`
	EmployeeID string    `gorm:"type:uuid;not null;uniqueIndex"`
	CreatedAt  time.Time `gorm:"autoCreateTime"`
	UpdatedAt  time.Time `gorm:"autoUpdateTime;index"`
	// The fields below are computed for the reading participant; they are not stored.
	EmployeeName   string   `gorm:"->;-:migration"`
	EmployeeAvatar *string  `gorm:"->;-:migration"`
	UnreadCount    int      `gorm:"->;-:migration"`
	LastMessage    *Message `gorm:"-"`
}

func (Conversation) TableName() string { return "conversations" }

// Message is ordered by Sequence, which increases within its conversation; IDs carry no order.
// A sender's ClientMessageID identifies one message within the conversation.
type Message struct {
	ID              string     `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	ConversationID  string     `gorm:"type:uuid;not null;uniqueIndex:idx_messages_conversation_sequence;uniqueIndex:idx_messages_client_message"`
	Sequence        int64      `gorm:"not null;uniqueIndex:idx_messages_conversation_sequence"`
	SenderID        string     `gorm:"type:uuid;not null;uniqueIndex:idx_messages_client_message"`
	Sender          *auth.User `gorm:"foreignKey:SenderID"`
	ClientMessageID string     `gorm:"not null;uniqueIndex:idx_messages_client_message"`
	Text            string     `gorm:"not null"`
	CreatedAt       time.Time  `gorm:"autoCreateTime"`
	// DeliveredAt and ReadAt are when the recipient's delivery and read positions first
	// reached this message; reading also delivers.
	DeliveredAt *time.Time
	ReadAt      *time.Time
}

func (Message) TableName() string { return "messages" }

// ConversationFilter selects one page of the conversations reader may access.
type ConversationFilter struct {
	Search     string
	UnreadOnly bool
	Offset     int
	Limit      int
}

// MessageFilter selects up to Limit messages before or after a cursor message, or the
// newest messages when both cursors are empty.
type MessageFilter struct {
	ConversationID string
	Before         string
	After          string
	Limit          int
}

var errCursorNotFound = errors.New("cursor message not found")

// AvatarURL presigns an Employee's stored avatar key for an authorized reader.
type AvatarURL func(ctx context.Context, tenantID string, key *string) (*string, error)

type ChatRepository interface {
	// List returns one page of the conversations reader may access, most recently updated
	// first, plus the total number of matches. Admins see their tenant's conversations and
	// Employees see only their own; conversations of Deleted Employees are hidden.
	List(ctx context.Context, reader auth.Principal, filter ConversationFilter) ([]*Conversation, int64, error)
	// FindByID returns NOT_FOUND unless reader may access the conversation.
	FindByID(ctx context.Context, reader auth.Principal, id string) (*Conversation, error)
	// Messages returns messages in ascending Sequence with their Sender. It returns
	// errCursorNotFound when a cursor is not a message in the conversation. It does not
	// authorize; callers must load the conversation through FindByID first.
	Messages(ctx context.Context, filter MessageFilter) ([]*Message, error)
	// Send stores m with the conversation's next Sequence and moves the conversation to
	// the top of the inbox, unless m's sender already sent its ClientMessageID there. It
	// returns the stored message with its Sender either way. It does not authorize.
	Send(ctx context.Context, m *Message) (*Message, error)
	// Acknowledge marks the other participant's messages through messageID as delivered to,
	// or read by, reader and returns the messages whose status changed. It returns
	// errCursorNotFound when messageID is not in the conversation. It does not authorize.
	Acknowledge(ctx context.Context, reader auth.Principal, conversationID, messageID string, read bool) ([]*Message, error)
	// UnreadTotal counts reader's unread messages across the conversations reader may access.
	UnreadTotal(ctx context.Context, reader auth.Principal) (int64, error)
	// Participants returns the active users who may access the conversation: its live
	// Employee's login and the tenant's Admin. SessionID is empty.
	Participants(ctx context.Context, conversationID string) ([]auth.Principal, error)
	// Counterparts returns the IDs of the active users user can chat with: the tenant's
	// Admin for an Employee, and the tenant's Employee logins for an Admin.
	Counterparts(ctx context.Context, user auth.Principal) ([]string, error)
}
