package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BounkhongDev/bkgo/adapter/jwt"
	"github.com/BounkhongDev/bkgo/config"
	"github.com/BounkhongDev/bkgo/contract"
	"github.com/BounkhongDev/bkgo/errs"
	"golang.org/x/crypto/bcrypt"
)

type memoryAuthRepository struct {
	user   *User
	tokens map[string]*RefreshToken
}

func (r *memoryAuthRepository) FindActiveUserByEmail(_ context.Context, email string) (*User, error) {
	if r.user == nil || !r.user.Active || r.user.Email != email {
		return nil, errs.ErrUnauthorized
	}
	return r.user, nil
}

func (r *memoryAuthRepository) FindUserByRefreshToken(_ context.Context, hash string, now time.Time) (*User, error) {
	token := r.tokens[hash]
	if token == nil || token.RevokedAt != nil || !token.ExpiresAt.After(now) || r.user == nil || !r.user.Active {
		return nil, errs.ErrUnauthorized
	}
	return r.user, nil
}

func (r *memoryAuthRepository) SaveRefreshToken(_ context.Context, token *RefreshToken) error {
	token.UserID = r.user.ID
	r.tokens[token.TokenHash] = token
	return nil
}

func (r *memoryAuthRepository) RotateRefreshToken(_ context.Context, oldHash string, expectedUser *User, replacement *RefreshToken, now time.Time) error {
	old := r.tokens[oldHash]
	if old == nil || old.RevokedAt != nil || !old.ExpiresAt.After(now) || r.user == nil || !r.user.Active {
		return errs.ErrUnauthorized
	}
	if r.user.ID != expectedUser.ID || r.user.TenantID != expectedUser.TenantID || r.user.Role != expectedUser.Role {
		return errs.ErrUnauthorized
	}
	old.RevokedAt = &now
	replacement.UserID = r.user.ID
	r.tokens[replacement.TokenHash] = replacement
	return nil
}

func (r *memoryAuthRepository) RevokeRefreshToken(_ context.Context, hash, userID string, now time.Time) error {
	token := r.tokens[hash]
	if token == nil || token.UserID != userID || token.RevokedAt != nil || !token.ExpiresAt.After(now) {
		return errs.ErrUnauthorized
	}
	token.RevokedAt = &now
	return nil
}

func TestLoginRotateAndLogout(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	repo := &memoryAuthRepository{
		user:   &User{ID: "user-1", TenantID: "tenant-1", Email: "admin@example.test", PasswordHash: string(hash), Role: RoleAdmin, Active: true},
		tokens: make(map[string]*RefreshToken),
	}
	tokens := jwt.New(config.JWT{Secret: "test-secret"})
	uc := NewAuthUsecase(repo, tokens)

	login, err := uc.Login(context.Background(), Credentials{Email: "ADMIN@example.test", Password: "password-123"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	claims, err := tokens.Verify(login.AccessToken)
	if err != nil || claims["sub"] != "user-1" || claims["tenantId"] != "tenant-1" || claims["role"] != "admin" {
		t.Fatalf("unexpected access-token claims %v, error %v", claims, err)
	}
	if _, exists := repo.tokens[login.RefreshToken]; exists {
		t.Fatal("refresh token was stored in plaintext")
	}
	oldHash := hashToken(login.RefreshToken)
	if repo.tokens[oldHash] == nil {
		t.Fatal("refresh token hash was not stored")
	}

	refreshed, err := uc.Refresh(context.Background(), RefreshInput{RefreshToken: login.RefreshToken})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.User != nil {
		t.Fatal("refresh response unexpectedly included the login user")
	}
	if repo.tokens[oldHash].RevokedAt == nil {
		t.Fatal("old refresh token was not revoked")
	}
	if _, err := uc.Refresh(context.Background(), RefreshInput{RefreshToken: login.RefreshToken}); err == nil {
		t.Fatal("reused refresh token was accepted")
	}
	expiredRaw := "expired-refresh-token"
	repo.tokens[hashToken(expiredRaw)] = &RefreshToken{
		UserID: "user-1", TokenHash: hashToken(expiredRaw), ExpiresAt: time.Now().Add(-time.Second),
	}
	if _, err := uc.Refresh(context.Background(), RefreshInput{RefreshToken: expiredRaw}); err == nil {
		t.Fatal("expired refresh token was accepted")
	}
	if err := uc.Logout(context.Background(), "user-1", RefreshInput{RefreshToken: refreshed.RefreshToken}); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := uc.Refresh(context.Background(), RefreshInput{RefreshToken: refreshed.RefreshToken}); err == nil {
		t.Fatal("logged-out refresh token was accepted")
	}
}

func TestLoginRejectsUnknownUserAndWrongPasswordGenerically(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	repo := &memoryAuthRepository{
		user:   &User{ID: "user-1", TenantID: "tenant-1", Email: "admin@example.test", PasswordHash: string(hash), Role: RoleAdmin, Active: true},
		tokens: make(map[string]*RefreshToken),
	}
	uc := NewAuthUsecase(repo, jwt.New(config.JWT{Secret: "test-secret"}))
	for _, input := range []Credentials{
		{Email: "missing@example.test", Password: "password-123"},
		{Email: "admin@example.test", Password: "wrong-password"},
	} {
		_, err := uc.Login(context.Background(), input)
		appErr, ok := errs.IsAppError(err)
		if !ok || appErr.Code != "UNAUTHORIZED" {
			t.Fatalf("expected generic unauthorized error, got %v", err)
		}
		if appErr.Message != errs.ErrUnauthorized.Message {
			t.Fatalf("credential failure message differs: %q", appErr.Message)
		}
	}
	if err := uc.Logout(context.Background(), "other-user", RefreshInput{RefreshToken: "missing"}); !errors.Is(err, errs.ErrUnauthorized) {
		appErr, ok := errs.IsAppError(err)
		if !ok || appErr.Code != "UNAUTHORIZED" {
			t.Fatalf("logout with unknown token must be unauthorized, got %v", err)
		}
	}
}

func TestSigningFailureLeavesRefreshTokenUsable(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("password-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	oldRaw := "current-refresh-token"
	oldHash := hashToken(oldRaw)
	repo := &memoryAuthRepository{
		user: &User{ID: "user-1", TenantID: "tenant-1", Email: "admin@example.test", PasswordHash: string(passwordHash), Role: RoleAdmin, Active: true},
		tokens: map[string]*RefreshToken{
			oldHash: {UserID: "user-1", TokenHash: oldHash, ExpiresAt: time.Now().Add(time.Hour)},
		},
	}
	uc := NewAuthUsecase(repo, jwt.New(config.JWT{}))
	if _, err := uc.Login(context.Background(), Credentials{Email: "admin@example.test", Password: "password-123"}); err == nil {
		t.Fatal("login succeeded without a signing secret")
	}
	if len(repo.tokens) != 1 {
		t.Fatal("failed login created a refresh token")
	}
	if _, err := uc.Refresh(context.Background(), RefreshInput{RefreshToken: oldRaw}); err == nil {
		t.Fatal("refresh succeeded without a signing secret")
	}
	if repo.tokens[oldHash].RevokedAt != nil || len(repo.tokens) != 1 {
		t.Fatal("failed refresh consumed or replaced the old refresh token")
	}
}

var _ AuthRepository = (*memoryAuthRepository)(nil)
var _ contract.Token = (*jwt.JWT)(nil)
