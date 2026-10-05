package chat

import (
	"context"
	"errors"
	"slices"

	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/errs"
	"github.com/aifgrouplaos/candidate-api/internal/auth"
	"github.com/aifgrouplaos/candidate-api/pkg/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errNotFound = *errs.NotFound("Conversation not found.")

type chatRepository struct {
	db contract.ORM
}

func NewChatRepository(db contract.ORM) ChatRepository {
	return &chatRepository{db: db}
}

// ProvisionConversation creates the Employee's conversation inside tx unless it exists.
func ProvisionConversation(tx *gorm.DB, tenantID, employeeID string) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&Conversation{TenantID: tenantID, EmployeeID: employeeID}).Error
}

func (r *chatRepository) List(ctx context.Context, reader auth.Principal, filter ConversationFilter) ([]*Conversation, int64, error) {
	query := r.readable(ctx, reader)
	if filter.Search != "" {
		pattern := utils.ContainsPattern(filter.Search)
		query = query.Where("(e.full_name ILIKE ? OR e.email ILIKE ? OR e.employee_code ILIKE ?)", pattern, pattern, pattern)
	}
	if filter.UnreadOnly {
		query = query.Where("EXISTS (SELECT 1 FROM messages m WHERE m.conversation_id = conversations.id AND m.sender_id <> ?)", reader.UserID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	conversations := []*Conversation{}
	err := selectView(query, reader).Order("conversations.updated_at DESC").Order("conversations.id DESC").
		Offset(filter.Offset).Limit(filter.Limit).Find(&conversations).Error
	if err != nil {
		return nil, 0, err
	}
	return conversations, total, r.attachLastMessages(ctx, conversations)
}

func (r *chatRepository) FindByID(ctx context.Context, reader auth.Principal, id string) (*Conversation, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, errNotFound
	}
	var conversation Conversation
	err := selectView(r.readable(ctx, reader), reader).Where("conversations.id = ?", id).First(&conversation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return &conversation, r.attachLastMessages(ctx, []*Conversation{&conversation})
}

func (r *chatRepository) Messages(ctx context.Context, filter MessageFilter) ([]*Message, error) {
	query := r.db.Session(ctx).Preload("Sender").Where("conversation_id = ?", filter.ConversationID)
	newestFirst := filter.After == ""
	if cursor := filter.Before + filter.After; cursor != "" {
		sequence, err := r.sequence(ctx, filter.ConversationID, cursor)
		if err != nil {
			return nil, err
		}
		if newestFirst {
			query = query.Where("sequence < ?", sequence)
		} else {
			query = query.Where("sequence > ?", sequence)
		}
	}
	messages := []*Message{}
	err := query.Order(clause.OrderByColumn{Column: clause.Column{Name: "sequence"}, Desc: newestFirst}).
		Limit(filter.Limit).Find(&messages).Error
	if newestFirst {
		slices.Reverse(messages)
	}
	return messages, err
}

func (r *chatRepository) sequence(ctx context.Context, conversationID, messageID string) (int64, error) {
	if _, err := uuid.Parse(messageID); err != nil {
		return 0, errCursorNotFound
	}
	var sequences []int64
	err := r.db.Session(ctx).Model(&Message{}).Where("id = ? AND conversation_id = ?", messageID, conversationID).
		Pluck("sequence", &sequences).Error
	if err == nil && len(sequences) == 0 {
		return 0, errCursorNotFound
	}
	if err != nil {
		return 0, err
	}
	return sequences[0], nil
}

// readable selects the conversations reader may access, joined to their live Employee as e.
// Anyone who is not an Admin is limited to the conversation of their own Employee login.
func (r *chatRepository) readable(ctx context.Context, reader auth.Principal) *gorm.DB {
	query := r.db.Session(ctx).Model(&Conversation{}).
		Joins("JOIN employees e ON e.id = conversations.employee_id AND e.deleted_at IS NULL").
		Where("conversations.tenant_id = ?", reader.TenantID)
	if reader.Role != auth.RoleAdmin {
		query = query.Where("e.user_id = ?", reader.UserID)
	}
	return query
}

// selectView adds the Employee summary and reader's unread count, which counts only
// messages from the other participant.
// ponytail: there is no stored read position yet, so every such message is unread; the
// unread count and unreadOnly filter must also compare against it once it exists.
func selectView(query *gorm.DB, reader auth.Principal) *gorm.DB {
	return query.Select(`conversations.*, e.full_name AS employee_name, e.avatar_url AS employee_avatar,
		(SELECT COUNT(*) FROM messages m WHERE m.conversation_id = conversations.id AND m.sender_id <> ?) AS unread_count`, reader.UserID)
}

func (r *chatRepository) attachLastMessages(ctx context.Context, conversations []*Conversation) error {
	if len(conversations) == 0 {
		return nil
	}
	byID := make(map[string]*Conversation, len(conversations))
	ids := make([]string, len(conversations))
	for i, c := range conversations {
		byID[c.ID], ids[i] = c, c.ID
	}
	var last []*Message
	err := r.db.Session(ctx).Preload("Sender").Select("DISTINCT ON (conversation_id) *").
		Where("conversation_id IN ?", ids).
		Order("conversation_id").Order("sequence DESC").Find(&last).Error
	for _, m := range last {
		byID[m.ConversationID].LastMessage = m
	}
	return err
}
