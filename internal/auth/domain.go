package auth

import (
	"context"
	"time"
)

type Role string

const (
	RoleAdmin    Role = "admin"
	RoleEmployee Role = "employee"
)

type User struct {
	ID           string    `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID     string    `json:"tenantId" gorm:"type:uuid;not null;index"`
	Email        string    `json:"email" gorm:"not null;uniqueIndex:idx_users_email_lower,expression:LOWER(email)"`
	PasswordHash string    `json:"-" gorm:"not null"`
	Role         Role      `json:"role" gorm:"not null"`
	FullName     string    `json:"fullName" gorm:"not null"`
	Active       bool      `json:"-" gorm:"not null;default:true"`
	CreatedAt    time.Time `json:"createdAt" gorm:"autoCreateTime"`
	UpdatedAt    time.Time `json:"updatedAt" gorm:"autoUpdateTime"`
}

func (User) TableName() string { return "users" }

type RefreshToken struct {
	ID        string     `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	UserID    string     `json:"userId" gorm:"type:uuid;not null;index"`
	TokenHash string     `json:"-" gorm:"not null;uniqueIndex"`
	ExpiresAt time.Time  `json:"expiresAt" gorm:"not null;index"`
	RevokedAt *time.Time `json:"revokedAt"`
	CreatedAt time.Time  `json:"createdAt" gorm:"autoCreateTime"`
}

func (RefreshToken) TableName() string { return "refresh_tokens" }

type AuthRepository interface {
	FindActiveUserByEmail(ctx context.Context, email string) (*User, error)
	FindUserByRefreshToken(ctx context.Context, hash string, now time.Time) (*User, error)
	SaveRefreshToken(ctx context.Context, token *RefreshToken) error
	RotateRefreshToken(ctx context.Context, oldHash string, expectedUser *User, replacement *RefreshToken, now time.Time) error
	RevokeRefreshToken(ctx context.Context, hash, userID string, now time.Time) error
}
