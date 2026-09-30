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

func (r *authRepository) SaveRefreshToken(ctx context.Context, token *RefreshToken) error {
	return r.db.Session(ctx).Create(token).Error
}

func (r *authRepository) FindUserByRefreshToken(ctx context.Context, hash string, now time.Time) (*User, error) {
	user, _, err := findRefreshUser(r.db.Session(ctx), hash, now)
	return user, err
}

func (r *authRepository) RotateRefreshToken(ctx context.Context, oldHash string, expectedUser *User, replacement *RefreshToken, now time.Time) error {
	return r.db.Transaction(ctx, func(tx *gorm.DB) error {
		user, old, err := findRefreshUser(tx, oldHash, now)
		if err != nil {
			return err
		}
		if user.ID != expectedUser.ID || user.TenantID != expectedUser.TenantID || user.Role != expectedUser.Role {
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
	var user User
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND active = ?", old.UserID, true).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errs.ErrUnauthorized
		}
		return nil, nil, err
	}
	return &user, &old, nil
}

func (r *authRepository) RevokeRefreshToken(ctx context.Context, hash, userID string, now time.Time) error {
	result := r.db.Session(ctx).Model(&RefreshToken{}).
		Where("token_hash = ? AND user_id = ? AND expires_at > ? AND revoked_at IS NULL", hash, userID, now).
		Update("revoked_at", now)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errs.ErrUnauthorized
	}
	return nil
}
