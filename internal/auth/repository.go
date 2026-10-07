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

const whereActiveID = "id = ? AND revoked_at IS NULL"

// deadRetention keeps revoked and expired sessions and refresh tokens for audit.
const deadRetention = 7 * 24 * time.Hour

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

func (r *authRepository) FindActiveUser(ctx context.Context, userID, tenantID string) (*User, error) {
	var user User
	err := r.db.Session(ctx).Where("id = ? AND tenant_id = ? AND active = ?", userID, tenantID, true).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errs.ErrUnauthorized
	}
	return &user, err
}

func (r *authRepository) EmployeeID(ctx context.Context, userID, tenantID string) (*string, error) {
	var ids []string
	err := r.db.Session(ctx).Table("employees").
		Where("user_id = ? AND tenant_id = ? AND deleted_at IS NULL", userID, tenantID).
		Limit(1).Pluck("id", &ids).Error
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	return &ids[0], nil
}

func (r *authRepository) CreateSession(ctx context.Context, session *AuthSession, token *RefreshToken) error {
	return r.db.Transaction(ctx, func(tx *gorm.DB) error {
		// ponytail: two logins committing together can both stay active; a partial unique
		// index on user_id WHERE revoked_at IS NULL would close that.
		now := time.Now().UTC()
		// ponytail: pruning runs only at login, so a user who never returns keeps dead
		// rows until tenant reset; retention is a minimum, not a maximum.
		cutoff := now.Add(-deadRetention)
		if err := tx.Where("user_id = ? AND (revoked_at < ? OR expires_at < ?)", session.UserID, cutoff, cutoff).
			Delete(&RefreshToken{}).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ? AND (revoked_at < ? OR NOT EXISTS (SELECT 1 FROM refresh_tokens WHERE refresh_tokens.session_id = auth_sessions.id))", session.UserID, cutoff).
			Delete(&AuthSession{}).Error; err != nil {
			return err
		}
		for _, row := range []any{&AuthSession{}, &RefreshToken{}} {
			if err := tx.Model(row).Where("user_id = ? AND revoked_at IS NULL", session.UserID).Update("revoked_at", now).Error; err != nil {
				return err
			}
		}
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
		result := tx.Model(&RefreshToken{}).Where(whereActiveID, old.ID).Update("revoked_at", now)
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
		Where(whereActiveID, old.SessionID).First(&session).Error; err != nil {
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
		result := tx.Model(&AuthSession{}).Where(whereActiveID, sessionID).Update("revoked_at", now)
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
