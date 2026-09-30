package auth

import (
	"context"
	"errors"
	"time"

	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/errs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type authRepository struct{ db contract.ORM }

func NewAuthRepository(db contract.ORM) AuthRepository { return &authRepository{db: db} }

func (r *authRepository) FindActiveUserByEmail(ctx context.Context, email string) (*User, error) {
	var user User
	err := r.db.Session(ctx).Where("LOWER(email) = ? AND active = ?", email, true).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errs.ErrUnauthorized
	}
	return &user, err
}

func (r *authRepository) CreateSession(ctx context.Context, session *AuthSession, token *RefreshToken) error {
	return r.db.Transaction(ctx, func(tx *gorm.DB) error {
		token.UserID = session.UserID
		token.SessionID = session.ID
		if err := tx.Create(session).Error; err != nil {
			return err
		}
		return tx.Create(token).Error
	})
}

func (r *authRepository) FindUserByRefreshToken(ctx context.Context, hash string, now time.Time) (*User, *RefreshToken, error) {
	return findRefreshUser(r.db.Session(ctx), hash, now)
}

func (r *authRepository) RotateRefreshToken(ctx context.Context, oldHash string, expectedUser *User, replacement *RefreshToken, now time.Time) error {
	return r.db.Transaction(ctx, func(tx *gorm.DB) error {
		user, old, err := findRefreshUser(tx, oldHash, now)
		if err != nil {
			return err
		}
		if user.ID != expectedUser.ID || user.TenantID != expectedUser.TenantID || user.Role != expectedUser.Role || replacement.SessionID != old.SessionID {
			return errs.ErrUnauthorized
		}
		result := tx.Model(&RefreshToken{}).Where("id = ? AND revoked_at IS NULL", old.ID).Update("revoked_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errs.ErrUnauthorized
		}
		replacement.UserID = user.ID
		return tx.Create(replacement).Error
	})
}

func findRefreshUser(db *gorm.DB, hash string, now time.Time) (*User, *RefreshToken, error) {
	var old RefreshToken
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("token_hash = ? AND expires_at > ? AND revoked_at IS NULL", hash, now).
		First(&old).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errs.ErrUnauthorized
		}
		return nil, nil, err
	}
	var session AuthSession
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND revoked_at IS NULL", old.SessionID).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errs.ErrUnauthorized
		}
		return nil, nil, err
	}
	var user User
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND active = ?", old.UserID, true).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errs.ErrUnauthorized
		}
		return nil, nil, err
	}
	if user.ID != session.UserID || user.TenantID != session.TenantID {
		return nil, nil, errs.ErrUnauthorized
	}
	return &user, &old, nil
}

func (r *authRepository) RevokeSession(ctx context.Context, hash, userID, sessionID string, now time.Time) error {
	return r.db.Transaction(ctx, func(tx *gorm.DB) error {
		user, token, err := findRefreshUser(tx, hash, now)
		if err != nil {
			return err
		}
		if user.ID != userID || token.SessionID != sessionID {
			return errs.ErrUnauthorized
		}
		result := tx.Model(&AuthSession{}).Where("id = ? AND revoked_at IS NULL", sessionID).Update("revoked_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errs.ErrUnauthorized
		}
		return tx.Model(&RefreshToken{}).Where("session_id = ? AND revoked_at IS NULL", sessionID).Update("revoked_at", now).Error
	})
}

func (r *authRepository) SessionActive(ctx context.Context, sessionID, userID, tenantID string) (bool, error) {
	var count int64
	err := r.db.Session(ctx).Model(&AuthSession{}).
		Joins("JOIN users ON users.id = auth_sessions.user_id").
		Where("auth_sessions.id = ? AND auth_sessions.user_id = ? AND auth_sessions.tenant_id = ? AND auth_sessions.revoked_at IS NULL AND users.active = ?", sessionID, userID, tenantID, true).
		Count(&count).Error
	return count == 1, err
}
